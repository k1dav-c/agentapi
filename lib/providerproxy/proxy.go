// Package providerproxy is the local proxy the agents call model providers
// through, so that an agent never gets a provider's API key: AgentAPI
// listens on 127.0.0.1, on a port of its own, for the agent's calls, which
// carry a token AgentAPI makes up for the process, and sends them on to the
// provider with its key. It learns how each provider answers (its health)
// along the way.
//
// The proxy starts on the first call of Endpoint, when an agent is to run
// on a provider: an AgentAPI whose agent never does opens no port.
package providerproxy

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/quartz"

	"github.com/coder/agentapi/lib/agentstate"
	"github.com/coder/agentapi/lib/provider"
)

// Styles are the APIs a provider is called with, the second part of a
// path: /<provider>/<style>/<rest>.
const (
	// StyleAnthropic is the Anthropic Messages API, under the provider's
	// ClaudeBaseURL (Claude Code).
	StyleAnthropic = "anthropic"
	// StyleOpenAI is the OpenAI API, under its OpenAIBaseURL (Codex).
	StyleOpenAI = "openai"
)

// The upstream timeouts: a connection and its TLS handshake, then the
// provider's response headers, which take as long as Claude Code waits
// for an answer (API_TIMEOUT_MS's default). The body has none: a turn's
// answer streams for minutes.
const (
	dialTimeout           = 30 * time.Second
	tlsTimeout            = 15 * time.Second
	responseHeaderTimeout = 10 * time.Minute
)

// Providers are the providers the proxy sends calls to, looked up on each
// call: a provider the hub or the config file changed applies at once.
// *provider.Source is one.
type Providers interface {
	Lookup(id string) (provider.Listed, bool)
}

// Endpoint is where an agent calls a provider through the proxy.
type Endpoint struct {
	// AnthropicBaseURL is the base Claude Code calls /v1/messages under
	// (ANTHROPIC_BASE_URL).
	AnthropicBaseURL string
	// OpenAIBaseURL is the base Codex calls /responses under (a model
	// provider's base_url).
	OpenAIBaseURL string
	// Token authenticates the agent to the proxy, as a bearer token or an
	// x-api-key: the API key the agent has.
	Token string
}

// Options configure a Proxy; the zero value is the defaults.
type Options struct {
	Logger *slog.Logger
	// Clock times the calls; nil is the real clock.
	Clock quartz.Clock
	// Transport sends the calls on; nil is one with the timeouts above.
	Transport http.RoundTripper
}

// Proxy is the provider proxy of an AgentAPI server.
type Proxy struct {
	providers Providers
	logger    *slog.Logger
	clock     quartz.Clock
	// client sends the calls on, following redirects without the key to
	// another origin.
	client *http.Client
	health *healthBook

	mu sync.Mutex
	// base is http://127.0.0.1:<port> once it listens.
	base   string
	token  string
	srv    *http.Server
	closed bool
}

// New returns the proxy of providers. It listens from the first Endpoint.
func New(providers Providers, opts Options) *Proxy {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Clock == nil {
		opts.Clock = quartz.NewReal()
	}
	if opts.Transport == nil {
		opts.Transport = &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:     true,
			TLSHandshakeTimeout:   tlsTimeout,
			ResponseHeaderTimeout: responseHeaderTimeout,
			ExpectContinueTimeout: time.Second,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
		}
	}
	return &Proxy{
		providers: providers,
		logger:    opts.Logger,
		clock:     opts.Clock,
		client:    &http.Client{Transport: opts.Transport, CheckRedirect: provider.KeepKeyOnOrigin(nil)},
		health:    newHealthBook(),
	}
}

// ErrClosed is returned by Endpoint once the proxy closed.
var ErrClosed = errors.New("the provider proxy is closed")

// Endpoint is where an agent calls the provider with the id. The proxy
// starts listening on the first call; an error means it can't, and the
// agent can't run on a provider.
func (p *Proxy) Endpoint(id string) (Endpoint, error) {
	if p == nil {
		return Endpoint{}, errors.New("AgentAPI has no provider proxy")
	}
	base, token, err := p.listen()
	if err != nil {
		return Endpoint{}, fmt.Errorf("start AgentAPI's provider proxy: %w", err)
	}
	prefix := base + "/" + url.PathEscape(id)
	return Endpoint{AnthropicBaseURL: prefix + "/" + StyleAnthropic, OpenAIBaseURL: prefix + "/" + StyleOpenAI, Token: token}, nil
}

// listen starts the proxy if it doesn't run yet, and returns its base URL
// and token.
func (p *Proxy) listen() (string, string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case p.closed:
		return "", "", ErrClosed
	case p.base != "":
		return p.base, p.token, nil
	}
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", "", err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", "", err
	}
	p.token = hex.EncodeToString(token[:])
	p.base = "http://" + ln.Addr().String()
	// No write timeout: the answers stream.
	p.srv = &http.Server{
		Handler:           http.HandlerFunc(p.serve),
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          slog.NewLogLogger(p.logger.Handler(), slog.LevelDebug),
	}
	srv := p.srv
	go func() { _ = srv.Serve(ln) }()
	p.logger.Info("Started the model provider proxy", "address", ln.Addr().String())
	return p.base, p.token, nil
}

// Close stops the proxy, and the calls under way.
func (p *Proxy) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	if p.srv == nil {
		return nil
	}
	return p.srv.Close()
}

// Health is how the provider with the id answered over the last hour; nil
// when the agent called it none.
func (p *Proxy) Health(id string) *agentstate.ProviderHealth {
	if p == nil {
		return nil
	}
	return p.health.of(id, p.clock.Now())
}

// AllHealth is the health of every provider the agent called over the last
// hour, by ID.
func (p *Proxy) AllHealth() map[string]agentstate.ProviderHealth {
	if p == nil {
		return nil
	}
	return p.health.all(p.clock.Now())
}

// authorized reports whether r carries the proxy's token, as a bearer
// token or an x-api-key.
func (p *Proxy) authorized(r *http.Request) bool {
	p.mu.Lock()
	token := p.token
	p.mu.Unlock()
	given := r.Header.Get("x-api-key")
	if auth := r.Header.Get("Authorization"); auth != "" {
		scheme, value, ok := strings.Cut(auth, " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") {
			return false
		}
		given = strings.TrimSpace(value)
	}
	return token != "" && subtle.ConstantTimeCompare([]byte(given), []byte(token)) == 1
}

// serve sends an agent's call on to its provider.
func (p *Proxy) serve(w http.ResponseWriter, r *http.Request) {
	id, style, rest := route(r.URL.EscapedPath())
	if !p.authorized(r) {
		writeError(w, style, http.StatusUnauthorized, "AgentAPI's provider proxy refused the call: it doesn't carry the proxy's token.")
		return
	}
	if style != StyleAnthropic && style != StyleOpenAI {
		writeError(w, style, http.StatusNotFound, "AgentAPI's provider proxy serves /<provider>/anthropic/... and /<provider>/openai/... only.")
		return
	}
	if dotSegments(rest) {
		// The call stays under the provider's base URL.
		writeError(w, style, http.StatusBadRequest, "AgentAPI's provider proxy takes no . or .. in a path.")
		return
	}
	listed, ok := p.providers.Lookup(id)
	if !ok {
		writeError(w, style, http.StatusNotFound, fmt.Sprintf("AgentAPI has no model provider %q: it was removed from ~/.agentapi/config.yaml or the hub. Choose another provider.", id))
		return
	}
	base := listed.ClaudeBaseURL()
	if style == StyleOpenAI {
		base = listed.OpenAIBaseURL()
	}
	if rest != "" {
		base += "/" + rest
	}
	target, err := url.Parse(base)
	if err != nil {
		writeError(w, style, http.StatusBadGateway, fmt.Sprintf("The base URL of model provider %s isn't valid.", listed.DisplayName()))
		return
	}
	target.RawQuery = r.URL.RawQuery
	c := &call{proxy: p, provider: id, name: listed.DisplayName(), style: style, request: r, start: p.clock.Now()}
	key := listed.APIKey
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL = target
			pr.Out.Host = ""
			// The agent's token stays here; the provider gets its key the way
			// the agents send one: Claude Code's ANTHROPIC_AUTH_TOKEN and
			// Codex's env_key are bearer tokens.
			pr.Out.Header.Del("Authorization")
			pr.Out.Header.Del("x-api-key")
			if key != "" {
				pr.Out.Header.Set("Authorization", "Bearer "+key)
			}
		},
		Transport:      roundTripper(p.send),
		FlushInterval:  -1,
		ModifyResponse: c.answered,
		ErrorHandler:   c.failed,
		ErrorLog:       slog.NewLogLogger(p.logger.Handler(), slog.LevelDebug),
	}
	// The agent's request body may still stream while the answer does: an
	// HTTP/1 server would otherwise read the rest of it first.
	_ = http.NewResponseController(w).EnableFullDuplex()
	rp.ServeHTTP(w, r)
}

// route splits an escaped path /<provider>/<style>/<rest> into its parts;
// rest stays escaped.
func route(path string) (id, style, rest string) {
	parts := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 3)
	if len(parts) < 2 {
		return "", "", ""
	}
	id, err := url.PathUnescape(parts[0])
	if err != nil {
		return "", "", ""
	}
	if len(parts) == 3 {
		rest = parts[2]
	}
	return id, parts[1], rest
}

// send sends a call on with the client, which follows redirects. The
// call came in as a server request: net/http refuses its RequestURI in a
// client's.
func (p *Proxy) send(out *http.Request) (*http.Response, error) {
	out.RequestURI = ""
	return p.client.Do(out)
}

// dotSegments reports whether an escaped path has a . or .. segment.
func dotSegments(path string) bool {
	for segment := range strings.SplitSeq(path, "/") {
		if unescaped, err := url.PathUnescape(segment); err != nil || unescaped == "." || unescaped == ".." {
			return true
		}
	}
	return false
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// writeError answers a call the proxy can't send on, in the error format
// of the API the agent speaks.
func writeError(w http.ResponseWriter, style string, status int, message string) {
	var body any
	if style == StyleOpenAI {
		body = map[string]any{"error": map[string]any{"message": message, "type": "agentapi_proxy_error"}}
	} else {
		errType := "api_error"
		switch status {
		case http.StatusUnauthorized:
			errType = "authentication_error"
		case http.StatusNotFound:
			errType = "not_found_error"
		}
		body = map[string]any{"type": "error", "error": map[string]any{"type": errType, "message": message}}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
