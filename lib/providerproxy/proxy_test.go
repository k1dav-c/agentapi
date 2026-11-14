package providerproxy_test

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/agentstate"
	"github.com/coder/agentapi/lib/provider"
	"github.com/coder/agentapi/lib/providerproxy"
)

// providers is a provider list a test changes.
type providers struct {
	mu   sync.Mutex
	list map[string]provider.Provider
}

func (p *providers) Lookup(id string) (provider.Listed, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	found, ok := p.list[id]
	return provider.Listed{Provider: found, Origin: provider.OriginConfig}, ok
}

func (p *providers) set(list ...provider.Provider) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.list = map[string]provider.Provider{}
	for _, found := range list {
		p.list[found.ID] = found
	}
}

func newProxy(t *testing.T, list ...provider.Provider) (*providerproxy.Proxy, *providers) {
	t.Helper()
	ps := &providers{}
	ps.set(list...)
	proxy := providerproxy.New(ps, providerproxy.Options{})
	t.Cleanup(func() { _ = proxy.Close() })
	return proxy, ps
}

func endpoint(t *testing.T, proxy *providerproxy.Proxy, id string) providerproxy.Endpoint {
	t.Helper()
	e, err := proxy.Endpoint(id)
	require.NoError(t, err)
	return e
}

func call(t *testing.T, method, url string, header http.Header, body io.Reader) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	require.NoError(t, err)
	for name, values := range header {
		req.Header[name] = values
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func bearer(token string) http.Header {
	return http.Header{"Authorization": {"Bearer " + token}}
}

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(data)
}

func TestEndpoint(t *testing.T) {
	t.Parallel()
	proxy, _ := newProxy(t)
	e := endpoint(t, proxy, "kimi")
	assert.Regexp(t, `^http://127\.0\.0\.1:\d+/kimi/anthropic$`, e.AnthropicBaseURL)
	assert.Equal(t, strings.TrimSuffix(e.AnthropicBaseURL, "/anthropic")+"/openai", e.OpenAIBaseURL)
	assert.Regexp(t, `^[0-9a-f]{64}$`, e.Token)
	again := endpoint(t, proxy, "glm")
	assert.Equal(t, e.Token, again.Token, "one token and port per process")
	assert.Equal(t, strings.TrimSuffix(e.OpenAIBaseURL, "/kimi/openai"), strings.TrimSuffix(again.OpenAIBaseURL, "/glm/openai"))

	other, _ := newProxy(t)
	assert.NotEqual(t, e.Token, endpoint(t, other, "kimi").Token)

	require.NoError(t, proxy.Close())
	_, err := proxy.Endpoint("kimi")
	assert.ErrorIs(t, err, providerproxy.ErrClosed)
	var none *providerproxy.Proxy
	_, err = none.Endpoint("kimi")
	assert.Error(t, err, "no proxy, no provider")
}

func TestAuth(t *testing.T) {
	t.Parallel()
	var contacted atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { contacted.Add(1) }))
	t.Cleanup(upstream.Close)
	proxy, _ := newProxy(t, provider.Provider{ID: "gw", BaseURL: upstream.URL + "/v1", APIKey: "sk-real"})
	e := endpoint(t, proxy, "gw")

	for name, header := range map[string]http.Header{
		"missing":             {},
		"wrong bearer":        bearer("nope"),
		"wrong x-api-key":     {"X-Api-Key": {"nope"}},
		"another scheme":      {"Authorization": {"Basic " + e.Token}},
		"the real key":        bearer("sk-real"),
		"wrong over a good":   {"Authorization": {"Bearer nope"}, "X-Api-Key": {e.Token}},
		"a prefix of a token": bearer(e.Token[:10]),
	} {
		resp := call(t, http.MethodPost, e.AnthropicBaseURL+"/v1/messages", header, nil)
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, name)
		assert.Contains(t, readAll(t, resp), "authentication_error", name)
	}
	// Unauthenticated, an unknown provider is no different.
	resp := call(t, http.MethodPost, strings.Replace(e.OpenAIBaseURL, "/gw/", "/nope/", 1)+"/responses", nil, nil)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Zero(t, contacted.Load(), "the provider isn't contacted")
	assert.Nil(t, proxy.Health("gw"), "nor counted")

	resp = call(t, http.MethodPost, e.AnthropicBaseURL+"/v1/messages", http.Header{"X-Api-Key": {e.Token}}, nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode, "x-api-key")
	resp = call(t, http.MethodPost, e.AnthropicBaseURL+"/v1/messages", http.Header{"Authorization": {"bearer " + e.Token}}, nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode, "the scheme in any case")
	assert.EqualValues(t, 2, contacted.Load())
}

// received is a request the upstream got.
type received struct {
	method, path, rawPath, query, body string
	header                             http.Header
	host                               string
}

func recorder(t *testing.T) (*httptest.Server, <-chan received) {
	t.Helper()
	got := make(chan received, 10)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got <- received{method: r.Method, path: r.URL.Path, rawPath: r.URL.EscapedPath(), query: r.URL.RawQuery, body: string(body), header: r.Header.Clone(), host: r.Host}
		w.Header().Set("X-Upstream", "yes")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(upstream.Close)
	return upstream, got
}

func TestRouting(t *testing.T) {
	t.Parallel()
	upstream, got := recorder(t)
	proxy, _ := newProxy(t,
		provider.Provider{ID: "gw", BaseURL: upstream.URL + "/llm/v1", APIKey: "sk-real"},
		provider.Provider{ID: "split", BaseURL: upstream.URL + "/openai", AnthropicBaseURL: upstream.URL + "/claude/", APIKey: "sk-split"},
		provider.Provider{ID: "open", BaseURL: upstream.URL + "/v1"},
	)
	gw := endpoint(t, proxy, "gw")

	// Claude Code: <ClaudeBaseURL>/v1/messages, its headers kept.
	resp := call(t, http.MethodPost, gw.AnthropicBaseURL+"/v1/messages?beta=true", http.Header{
		"Authorization":     {"Bearer " + gw.Token},
		"Anthropic-Version": {"2023-06-01"},
		"Anthropic-Beta":    {"fine-grained-tool-streaming-2025-05-14"},
		"User-Agent":        {"claude-cli/2.1"},
		"Content-Type":      {"application/json"},
		"Connection":        {"X-Hop"},
		"X-Hop":             {"dropped"},
	}, strings.NewReader(`{"model":"K3"}`))
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "yes", resp.Header.Get("X-Upstream"))
	assert.Equal(t, `{"ok":true}`, readAll(t, resp))
	r := <-got
	assert.Equal(t, http.MethodPost, r.method)
	assert.Equal(t, "/llm/v1/messages", r.path, "base_url without /v1, then the agent's path")
	assert.Equal(t, "beta=true", r.query)
	assert.Equal(t, `{"model":"K3"}`, r.body)
	assert.Equal(t, "Bearer sk-real", r.header.Get("Authorization"), "the real key, upstream only")
	assert.Empty(t, r.header.Values("X-Api-Key"))
	assert.Equal(t, "2023-06-01", r.header.Get("Anthropic-Version"))
	assert.Equal(t, "fine-grained-tool-streaming-2025-05-14", r.header.Get("Anthropic-Beta"))
	assert.Equal(t, "claude-cli/2.1", r.header.Get("User-Agent"))
	assert.Empty(t, r.header.Get("X-Hop"), "hop-by-hop headers stay here")
	assert.Empty(t, r.header.Get("X-Forwarded-For"))
	assert.Equal(t, strings.TrimPrefix(upstream.URL, "http://"), r.host)

	// Codex: <OpenAIBaseURL>/responses, x-api-key replaced too.
	resp = call(t, http.MethodPost, gw.OpenAIBaseURL+"/responses?a=1&b=%2F", http.Header{"X-Api-Key": {gw.Token}}, strings.NewReader(`{}`))
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	r = <-got
	assert.Equal(t, "/llm/v1/responses", r.path)
	assert.Equal(t, "a=1&b=%2F", r.query)
	assert.Equal(t, "Bearer sk-real", r.header.Get("Authorization"))
	assert.Empty(t, r.header.Values("X-Api-Key"), "the agent's token stays here")

	// An escaped path stays escaped.
	call(t, http.MethodGet, gw.OpenAIBaseURL+"/files/a%2Fb", bearer(gw.Token), nil)
	assert.Equal(t, "/llm/v1/files/a%2Fb", (<-got).rawPath)

	// anthropic_base_url, and an OpenAI base without its version.
	split := endpoint(t, proxy, "split")
	call(t, http.MethodPost, split.AnthropicBaseURL+"/v1/messages", bearer(split.Token), nil)
	r = <-got
	assert.Equal(t, "/claude/v1/messages", r.path)
	assert.Equal(t, "Bearer sk-split", r.header.Get("Authorization"))
	call(t, http.MethodPost, split.OpenAIBaseURL+"/responses", bearer(split.Token), nil)
	assert.Equal(t, "/openai/v1/responses", (<-got).path)

	// A provider without a key gets no credentials.
	open := endpoint(t, proxy, "open")
	call(t, http.MethodPost, open.OpenAIBaseURL+"/responses", bearer(open.Token), nil)
	r = <-got
	assert.Empty(t, r.header.Values("Authorization"))
	assert.Empty(t, r.header.Values("X-Api-Key"))

	// Another style.
	resp = call(t, http.MethodPost, strings.TrimSuffix(gw.OpenAIBaseURL, "openai")+"gemini/x", bearer(gw.Token), nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)

	// A call stays under the provider's base URL.
	for _, path := range []string{"/../admin", "/v1/%2e%2e/admin", "/./x"} {
		req, err := http.NewRequest(http.MethodGet, gw.OpenAIBaseURL, nil)
		require.NoError(t, err)
		// Opaque, so the client sends the path as it is.
		req.URL.Opaque = "//" + req.URL.Host + req.URL.Path + path
		req.Header.Set("Authorization", "Bearer "+gw.Token)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, path)
	}
	assert.Empty(t, got)
}

func TestUnknownAndRemovedProvider(t *testing.T) {
	t.Parallel()
	upstream, got := recorder(t)
	gw := provider.Provider{ID: "gw", BaseURL: upstream.URL + "/v1", APIKey: "sk-real"}
	source := provider.NewSource("", nil)
	source.SetHub([]provider.Provider{gw})
	proxy := providerproxy.New(source, providerproxy.Options{})
	t.Cleanup(func() { _ = proxy.Close() })

	e := endpoint(t, proxy, "gw")
	resp := call(t, http.MethodPost, e.OpenAIBaseURL+"/responses", bearer(e.Token), nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	<-got

	// The hub's key changes: the next call has the new one.
	gw.APIKey = "sk-rotated"
	source.SetHub([]provider.Provider{gw})
	call(t, http.MethodPost, e.OpenAIBaseURL+"/responses", bearer(e.Token), nil)
	assert.Equal(t, "Bearer sk-rotated", (<-got).header.Get("Authorization"))

	// The hub no longer has it.
	source.SetHub(nil)
	resp = call(t, http.MethodPost, e.OpenAIBaseURL+"/responses", bearer(e.Token), nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	body := readAll(t, resp)
	assert.Contains(t, body, `no model provider \"gw\"`)
	assert.Contains(t, body, `"error":{"message"`, "in OpenAI's format for Codex")
	unknown := endpoint(t, proxy, "nope")
	resp = call(t, http.MethodPost, unknown.AnthropicBaseURL+"/v1/messages", bearer(unknown.Token), nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Contains(t, readAll(t, resp), `"type":"not_found_error"`, "in Anthropic's format for Claude Code")
	assert.Empty(t, got)
}

func TestStreaming(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	finished := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(finished)
		_ = http.NewResponseController(w).EnableFullDuplex()
		// The request body streams too: its first line arrives before the
		// agent sends the rest.
		in := bufio.NewReader(r.Body)
		line, err := in.ReadString('\n')
		if err != nil || line != "first\n" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: message_start\ndata: {}\n\n"))
		w.(http.Flusher).Flush()
		<-release
		rest, _ := io.ReadAll(in)
		_, _ = w.Write([]byte("event: message_stop\ndata: " + string(rest) + "\n\n"))
	}))
	t.Cleanup(upstream.Close)
	proxy, _ := newProxy(t, provider.Provider{ID: "gw", BaseURL: upstream.URL + "/v1", APIKey: "sk-real"})
	e := endpoint(t, proxy, "gw")

	body, send := io.Pipe()
	go func() {
		_, _ = send.Write([]byte("first\n"))
	}()
	resp := call(t, http.MethodPost, e.AnthropicBaseURL+"/v1/messages", bearer(e.Token), body)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	events := bufio.NewReader(resp.Body)
	line, err := events.ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "event: message_start\n", line, "the first event before the provider finished")
	select {
	case <-finished:
		t.Fatal("the provider finished before the first event came through")
	default:
	}
	go func() {
		_, _ = send.Write([]byte("rest"))
		_ = send.Close()
	}()
	close(release)
	rest, err := io.ReadAll(events)
	require.NoError(t, err)
	assert.Equal(t, "data: {}\n\nevent: message_stop\ndata: rest\n\n", string(rest))
	require.NoError(t, resp.Body.Close())
	assert.Eventually(t, func() bool { return proxy.Health("gw") != nil }, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, 1, proxy.Health("gw").Requests)
}

func TestCancellation(t *testing.T) {
	t.Parallel()
	cancelled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(cancelled)
	}))
	t.Cleanup(upstream.Close)
	proxy, _ := newProxy(t, provider.Provider{ID: "gw", BaseURL: upstream.URL + "/v1", APIKey: "sk-real"})
	e := endpoint(t, proxy, "gw")

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.OpenAIBaseURL+"/responses", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+e.Token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	cancel()
	_ = resp.Body.Close()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("the provider's request wasn't cancelled")
	}
	// The agent stopped reading an answer that was going fine.
	assert.Eventually(t, func() bool { return proxy.Health("gw") != nil }, 5*time.Second, 10*time.Millisecond)
	assert.Zero(t, proxy.Health("gw").Failures)

	// A call cancelled before the answer doesn't count.
	stuck := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		close(stuck)
	}))
	t.Cleanup(slow.Close)
	proxy, _ = newProxy(t, provider.Provider{ID: "slow", BaseURL: slow.URL + "/v1"})
	e = endpoint(t, proxy, "slow")
	ctx, cancel = context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, e.OpenAIBaseURL+"/responses", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+e.Token)
	_, err = http.DefaultClient.Do(req)
	require.Error(t, err)
	<-stuck
	time.Sleep(50 * time.Millisecond)
	assert.Nil(t, proxy.Health("slow"))
}

func TestRedirects(t *testing.T) {
	t.Parallel()
	other, got := recorder(t)
	var origin *httptest.Server
	origin = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			http.Redirect(w, r, other.URL+"/v1/models?x=1", http.StatusFound)
		case "/v1/here":
			http.Redirect(w, r, "/v1/there", http.StatusFound)
		case "/v1/there":
			_, _ = w.Write([]byte(r.Header.Get("Authorization")))
		case "/v1/responses":
			http.Redirect(w, r, other.URL+"/v1/responses", http.StatusTemporaryRedirect)
		}
	}))
	t.Cleanup(origin.Close)
	proxy, _ := newProxy(t, provider.Provider{ID: "gw", BaseURL: origin.URL + "/v1", APIKey: "sk-real"})
	e := endpoint(t, proxy, "gw")

	resp := call(t, http.MethodGet, e.OpenAIBaseURL+"/models", bearer(e.Token), nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	r := <-got
	assert.Equal(t, "/v1/models", r.path)
	assert.Empty(t, r.header.Values("Authorization"), "no key to another origin")
	assert.Empty(t, r.header.Values("X-Api-Key"))

	resp = call(t, http.MethodGet, e.OpenAIBaseURL+"/here", bearer(e.Token), nil)
	assert.Equal(t, "Bearer sk-real", readAll(t, resp), "the key on the same origin")

	// A redirect of a body isn't followed, nor handed to the agent, which
	// would follow it with its token.
	resp = call(t, http.MethodPost, e.OpenAIBaseURL+"/responses", bearer(e.Token), strings.NewReader(`{"input":"x"}`))
	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
	assert.Empty(t, resp.Header.Get("Location"))
	assert.Contains(t, readAll(t, resp), "redirected")
	assert.Empty(t, got)
	health := proxy.Health("gw")
	require.NotNil(t, health)
	assert.Equal(t, 3, health.Requests)
	assert.Equal(t, 1, health.Failures)
	require.NotNil(t, health.LastFailure)
	assert.Equal(t, http.StatusTemporaryRedirect, health.LastFailure.Status)
	assert.NotContains(t, health.LastFailure.Message, "?", "no query")
}

func TestHealthOutcomes(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/ok":
			_, _ = w.Write([]byte("ok"))
		case "/v1/overloaded":
			w.WriteHeader(529)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`))
		case "/v1/limited":
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"Rate limit reached for requests","type":"requests"}}`))
		case "/v1/bad":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"unknown model"}}`))
		case "/v1/plain":
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("<html>" + strings.Repeat("x", 10000)))
		case "/v1/broken":
			w.Header().Set("Content-Length", "100")
			_, _ = w.Write([]byte("partial"))
			w.(http.Flusher).Flush()
			conn, _, _ := w.(http.Hijacker).Hijack()
			_ = conn.Close()
		}
	}))
	t.Cleanup(upstream.Close)
	down := httptest.NewServer(http.NotFoundHandler())
	down.Close()
	proxy, _ := newProxy(t,
		provider.Provider{ID: "gw", BaseURL: upstream.URL + "/v1", APIKey: "sk-real"},
		provider.Provider{ID: "down", BaseURL: down.URL + "/v1"},
	)
	e := endpoint(t, proxy, "gw")
	send := func(path string) int {
		resp := call(t, http.MethodPost, e.OpenAIBaseURL+path, bearer(e.Token), nil)
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	waitRequests := func(id string, n int) agentstate.ProviderHealth {
		t.Helper()
		require.Eventually(t, func() bool { h := proxy.Health(id); return h != nil && h.Requests == n }, 5*time.Second, 10*time.Millisecond)
		return *proxy.Health(id)
	}

	assert.Equal(t, http.StatusOK, send("/ok"))
	h := waitRequests("gw", 1)
	assert.Zero(t, h.Failures)
	require.NotNil(t, h.LastSuccessAt)
	assert.Nil(t, h.LastFailure)

	assert.Equal(t, 529, send("/overloaded"))
	h = waitRequests("gw", 2)
	assert.Equal(t, 1, h.Failures)
	require.NotNil(t, h.LastFailure)
	assert.Equal(t, agentstate.ProviderFailure{At: h.LastFailure.At, Kind: agentstate.ProviderFailureStatus, Status: 529, Message: "Overloaded"}, *h.LastFailure)

	assert.Equal(t, http.StatusTooManyRequests, send("/limited"))
	h = waitRequests("gw", 3)
	assert.Equal(t, 2, h.Failures, "a 429 is the provider's")
	assert.Equal(t, "Rate limit reached for requests", h.LastFailure.Message)

	assert.Equal(t, http.StatusBadRequest, send("/bad"))
	h = waitRequests("gw", 4)
	assert.Equal(t, 2, h.Failures, "another 4xx isn't")
	assert.Equal(t, 1, h.ClientErrors)

	assert.Equal(t, http.StatusBadGateway, send("/plain"))
	h = waitRequests("gw", 5)
	assert.Equal(t, "502 Bad Gateway", h.LastFailure.Message, "the status, without a JSON message")

	send("/broken")
	h = waitRequests("gw", 6)
	assert.Equal(t, 4, h.Failures)
	assert.Equal(t, agentstate.ProviderFailureStream, h.LastFailure.Kind)
	assert.Equal(t, http.StatusOK, h.LastFailure.Status)

	down2 := endpoint(t, proxy, "down")
	resp := call(t, http.MethodPost, down2.OpenAIBaseURL+"/responses", bearer(down2.Token), nil)
	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
	assert.Contains(t, readAll(t, resp), "couldn't get an answer")
	h = waitRequests("down", 1)
	assert.Equal(t, 1, h.Failures)
	assert.Equal(t, agentstate.ProviderFailureNetwork, h.LastFailure.Kind)
	assert.Contains(t, h.LastFailure.Message, "connect")
	assert.NotContains(t, h.LastFailure.Message, down.URL, "without the URL")
	assert.Nil(t, h.LastSuccessAt)

	all := proxy.AllHealth()
	assert.Len(t, all, 2)
	assert.True(t, all["gw"].Equal(*proxy.Health("gw")))
}

// An upgraded connection (a WebSocket) goes through as it is.
func TestUpgrade(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "websocket" || r.Header.Get("Authorization") != "Bearer sk-real" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		_ = rw.Flush()
		line, _ := rw.ReadString('\n')
		_, _ = rw.WriteString("echo " + line)
		_ = rw.Flush()
	}))
	t.Cleanup(upstream.Close)
	proxy, _ := newProxy(t, provider.Provider{ID: "gw", BaseURL: upstream.URL + "/v1", APIKey: "sk-real"})
	e := endpoint(t, proxy, "gw")

	req, err := http.NewRequest(http.MethodGet, e.OpenAIBaseURL+"/responses", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+e.Token)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
	conn, ok := resp.Body.(io.ReadWriteCloser)
	require.True(t, ok)
	defer func() { _ = conn.Close() }()
	_, err = conn.Write([]byte("hi\n"))
	require.NoError(t, err)
	line, err := bufio.NewReader(conn).ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "echo hi\n", line)
	assert.Equal(t, 1, proxy.Health("gw").Requests)
}
