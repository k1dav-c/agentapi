package hub

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/hub"
	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/registry"
	"github.com/coder/agentapi/lib/userconfig"
)

// fakeAgentAPI serves /webhook like an AgentAPI and records the changes.
type fakeAgentAPI struct {
	mu      sync.Mutex
	current map[string]any
	puts    []map[string]any
}

func (f *fakeAgentAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path != "/webhook" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if r.Method == http.MethodPut {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.puts = append(f.puts, body)
		for k, v := range body {
			f.current[k] = v
		}
	}
	_ = json.NewEncoder(w).Encode(f.current)
}

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := HubCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestJoinStatusLeave(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	registryDir := filepath.Join(dir, "instances")
	t.Setenv("AGENTAPI_API_TOKEN", "")
	t.Setenv("AGENTAPI_HUB_SECRET", "")

	// A setting of the user's stays.
	require.NoError(t, userconfig.Save(configPath, userconfig.Config{"allowed-hosts": []string{"*"}}))
	fake := &fakeAgentAPI{current: map[string]any{"url": "", "timeout_seconds": 7, "max_attempts": 2}}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	hostname, _ := os.Hostname()
	_, err := registry.Write(registryDir, registry.Entry{WebhookInstance: instanceapi.WebhookInstance{ID: "host:1", Name: "api", ServerID: "a1", URL: srv.URL, Hostname: hostname, Port: 3286, PID: os.Getpid(), AgentType: "claude"}, StartedAt: time.Now()})
	require.NoError(t, err)
	flags := []string{"--config", configPath, "--registry-dir", registryDir}

	_, err = run(t, append([]string{"join", "ftp://hub"}, flags...)...)
	require.Error(t, err)

	// No hub runs there: --no-check.
	out, err := run(t, append([]string{"join", "http://hub.tailnet:8080/hooks", "--secret", "s3cret", "--public-url", "http://dev-1.vpn:{port}", "--no-check"}, flags...)...)
	require.NoError(t, err, out)
	assert.Contains(t, out, "api (")
	config, err := userconfig.Load(configPath)
	require.NoError(t, err)
	assert.Equal(t, "http://hub.tailnet:8080/hooks", config[keyURL])
	assert.Equal(t, "s3cret", config[keySecret])
	assert.Len(t, config[keyEvents], len(defaultEvents))
	assert.Equal(t, []any{"*"}, config["allowed-hosts"])
	require.Len(t, fake.puts, 1)
	put := fake.puts[0]
	assert.Equal(t, "http://hub.tailnet:8080/hooks", put["url"])
	assert.Equal(t, "s3cret", put["secret"])
	assert.EqualValues(t, 7, put["timeout_seconds"], "kept")
	assert.EqualValues(t, 2, put["max_attempts"], "kept")
	assert.Equal(t, "http://dev-1.vpn:3286", put["public_url"], "filled in for the instance")
	assert.Equal(t, "http://dev-1.vpn:{port}", config[keyPublic], "the file keeps the placeholder")

	out, err = run(t, append([]string{"status"}, flags...)...)
	require.NoError(t, err)
	assert.Contains(t, out, "Hub: http://hub.tailnet:8080/hooks")
	assert.Contains(t, out, "signed: true")
	assert.Contains(t, out, "reports to http://hub.tailnet:8080/hooks")

	out, err = run(t, append([]string{"leave"}, flags...)...)
	require.NoError(t, err, out)
	config, err = userconfig.Load(configPath)
	require.NoError(t, err)
	assert.Nil(t, config[keyURL])
	assert.Nil(t, config[keySecret])
	assert.Equal(t, []any{"*"}, config["allowed-hosts"])
	assert.Equal(t, "", fake.puts[1]["url"])
}

// An AgentAPI that needs a token and gets none keeps its webhook until it
// restarts; joining still writes the file.
func TestJoinNeedsToken(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	registryDir := filepath.Join(dir, "instances")
	t.Setenv("AGENTAPI_API_TOKEN", "")
	t.Setenv("AGENTAPI_HUB_SECRET", "env-secret")
	hostname, _ := os.Hostname()
	_, err := registry.Write(registryDir, registry.Entry{WebhookInstance: instanceapi.WebhookInstance{ID: "host:1", ServerID: "a1", URL: "http://127.0.0.1:1", Hostname: hostname, PID: os.Getpid()}, AuthRequired: true, StartedAt: time.Now()})
	require.NoError(t, err)

	out, err := run(t, "join", "http://hub/hooks", "--no-check", "--config", configPath, "--registry-dir", registryDir)
	require.Error(t, err)
	assert.Contains(t, out, "needs an API token")
	config, err := userconfig.Load(configPath)
	require.NoError(t, err)
	assert.Equal(t, "env-secret", config[keySecret], "the secret from the environment")
}

// join first checks that the hub takes this machine's events, and says why
// not: a wrong or missing secret, a sign-in page in front of the hub, no
// hub there, or a public URL a hub on another machine can't reach.
func TestJoinChecksHub(t *testing.T) {
	h, err := hub.New(hub.Config{Token: "t", Secret: "s3cret", StatePath: ""})
	require.NoError(t, err)
	hubSrv := httptest.NewServer(h.Handler())
	defer hubSrv.Close()
	signIn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://coder.example.com/login", http.StatusSeeOther)
	}))
	defer signIn.Close()
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()

	t.Setenv("AGENTAPI_API_TOKEN", "")
	t.Setenv("AGENTAPI_HUB_SECRET", "")
	join := func(args ...string) (string, userconfig.Config, error) {
		t.Helper()
		dir := t.TempDir()
		configPath := filepath.Join(dir, "config.yaml")
		out, err := run(t, append(append([]string{"join"}, args...), "--config", configPath, "--registry-dir", filepath.Join(dir, "instances"))...)
		config, loadErr := userconfig.Load(configPath)
		require.NoError(t, loadErr)
		return out, config, err
	}

	out, config, err := join(hubSrv.URL+"/hooks", "--secret", "s3cret", "--public-url", "http://127.0.0.1:{port}")
	require.NoError(t, err, out)
	assert.Contains(t, out, "took a signed test event")
	assert.Equal(t, hubSrv.URL+"/hooks", config[keyURL])

	_, config, err = join(hubSrv.URL+"/hooks", "--secret", "wrong")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refused the signature")
	assert.Nil(t, config[keyURL], "nothing written")

	_, _, err = join(hubSrv.URL + "/hooks")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "takes only signed events")

	_, _, err = join(signIn.URL+"/hooks", "--secret", "s3cret")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a sign-in page is in front of it")

	_, _, err = join(closedURL+"/hooks", "--secret", "s3cret")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "can't reach the hub")
	assert.Contains(t, err.Error(), "--no-check")

	_, _, err = join("http://hub.tailnet:3290/hooks", "--secret", "s3cret", "--public-url", "http://localhost:{port}")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "on another machine")
}
