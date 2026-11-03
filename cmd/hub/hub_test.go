package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
	assert.Equal(t, []any{"instance.*"}, config[keyEvents], "the summary stream says the rest")
	assert.Equal(t, []any{"*"}, config["allowed-hosts"])
	require.Len(t, fake.puts, 1)
	put := fake.puts[0]
	assert.Equal(t, "http://hub.tailnet:8080/hooks", put["url"])
	assert.Equal(t, "s3cret", put["secret"])
	assert.Equal(t, []any{"instance.*"}, put["events"])
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

// join --allow-update lets the hub update the AgentAPIs: it writes
// allow-update: true, which a join without it and leave keep.
func TestJoinAllowUpdate(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	t.Setenv("AGENTAPI_API_TOKEN", "")
	t.Setenv("AGENTAPI_HUB_SECRET", "")
	flags := []string{"--no-check", "--config", configPath, "--registry-dir", filepath.Join(dir, "instances")}

	out, err := run(t, append([]string{"join", "http://hub.tailnet:3290/hooks"}, flags...)...)
	require.NoError(t, err, out)
	config, err := userconfig.Load(configPath)
	require.NoError(t, err)
	assert.Nil(t, config[keyAllowUpdate], "off unless asked")
	assert.NotContains(t, out, "update")

	out, err = run(t, append([]string{"join", "http://hub.tailnet:3290/hooks", "--allow-update"}, flags...)...)
	require.NoError(t, err, out)
	assert.Contains(t, out, "The hub can update the AgentAPIs that start from now on")
	config, err = userconfig.Load(configPath)
	require.NoError(t, err)
	assert.Equal(t, true, config[keyAllowUpdate])
	assert.Equal(t, "http://hub.tailnet:3290/hooks", config[keyURL])

	_, err = run(t, append([]string{"join", "http://hub.tailnet:3290/hooks"}, flags...)...)
	require.NoError(t, err)
	config, err = userconfig.Load(configPath)
	require.NoError(t, err)
	assert.Equal(t, true, config[keyAllowUpdate], "joining again keeps it")

	_, err = run(t, append([]string{"leave"}, flags[1:]...)...)
	require.NoError(t, err)
	config, err = userconfig.Load(configPath)
	require.NoError(t, err)
	assert.Nil(t, config[keyURL])
	assert.Equal(t, true, config[keyAllowUpdate], "leaving keeps it")
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

// join --public-url 'http://{ip}:{port}': the config file keeps the
// template; an AgentAPI that fills in {ip} itself gets it too, and follows
// the machine's address; an older one gets the address now. join reaches
// an AgentAPI whose registry URL names an address the machine no longer
// has at its port on localhost.
func TestJoinIP(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	registryDir := filepath.Join(dir, "instances")
	t.Setenv("AGENTAPI_API_TOKEN", "")
	t.Setenv("AGENTAPI_HUB_SECRET", "")
	var resolvedFor []string
	defaultResolve := resolveIP
	resolveIP = func(hubURL string) (string, error) {
		resolvedFor = append(resolvedFor, hubURL)
		return "192.168.9.49", nil
	}
	t.Cleanup(func() { resolveIP = defaultResolve })

	hostname, _ := os.Hostname()
	fakes := map[string]*fakeAgentAPI{}
	for i, v := range []string{"2.1.2", "2.2.0", "0.0.0-dev", ""} {
		fake := &fakeAgentAPI{current: map[string]any{"url": "", "timeout_seconds": 5, "max_attempts": 3}}
		srv := httptest.NewServer(fake)
		t.Cleanup(srv.Close)
		port := srv.Listener.Addr().(*net.TCPAddr).Port
		url := srv.URL
		if v == "2.2.0" {
			// Its public URL, from before DHCP gave the machine another
			// address.
			url = "http://127.0.0.2:1"
		}
		fakes[v] = fake
		_, err := registry.Write(registryDir, registry.Entry{WebhookInstance: instanceapi.WebhookInstance{
			ID: fmt.Sprintf("host:%d", port), ServerID: fmt.Sprintf("s%d", i), URL: url, Hostname: hostname, Port: port, PID: os.Getpid(), AgentType: "claude", Version: v,
		}, StartedAt: time.Now()})
		require.NoError(t, err)
	}

	out, err := run(t, "join", "http://192.168.9.210:3290/hooks", "--secret", "s", "--public-url", "http://{ip}:{port}", "--no-check", "--config", configPath, "--registry-dir", registryDir)
	require.NoError(t, err, out)
	assert.Equal(t, []string{"http://192.168.9.210:3290/hooks"}, resolvedFor, "toward the hub")
	assert.Contains(t, out, "{ip} is 192.168.9.49 now")
	assert.Equal(t, 2, strings.Count(out, "too old for {ip}, it reports http://192.168.9.49:"), out)
	config, err := userconfig.Load(configPath)
	require.NoError(t, err)
	assert.Equal(t, "http://{ip}:{port}", config[keyPublic], "the file keeps the template")

	publicURL := func(v string) any {
		t.Helper()
		fake := fakes[v]
		fake.mu.Lock()
		defer fake.mu.Unlock()
		require.Len(t, fake.puts, 1, v)
		return fake.puts[0]["public_url"]
	}
	assert.Regexp(t, `^http://192\.168\.9\.49:\d+$`, publicURL("2.1.2"), "too old for {ip}")
	assert.Regexp(t, `^http://192\.168\.9\.49:\d+$`, publicURL(""), "too old to say its version")
	assert.Regexp(t, `^http://\{ip\}:\d+$`, publicURL("2.2.0"), "fills it in itself; reached on localhost")
	assert.Regexp(t, `^http://\{ip\}:\d+$`, publicURL("0.0.0-dev"), "a development build")

	// No address for {ip}: nothing is written.
	resolveIP = func(string) (string, error) { return "", errors.New("no interface has an IPv4 address") }
	other := filepath.Join(dir, "other.yaml")
	_, err = run(t, "join", "http://192.168.9.210:3290/hooks", "--public-url", "http://{ip}:{port}", "--no-check", "--config", other, "--registry-dir", filepath.Join(dir, "none"))
	require.ErrorContains(t, err, "no address of this machine for {ip}")
	_, statErr := os.Stat(other)
	assert.True(t, os.IsNotExist(statErr))
}

// The AgentAPIs that fill in {ip} themselves: those of a release after
// 2.1.2, development builds, and those at least as new as this agentapi.
func TestSupportsIP(t *testing.T) {
	for _, tc := range []struct {
		version, own string
		want         bool
	}{
		{"2.1.2", "2.2.0", false},
		{"2.1.1", "0.0.0-dev", false},
		{"1.4.0", "2.2.0", false},
		{"", "2.2.0", false},
		{"not a version", "2.2.0", false},
		{"2.1.3", "2.1.3", true},
		{"2.2.0", "2.2.0", true},
		{"3.0.0", "2.2.0", true},
		{"0.0.0-dev", "2.2.0", true},
		{"2.1.2-4-g3e48afc", "2.1.2-4-g3e48afc", true},
		{"2.1.2-9-gabcdef0", "2.1.2-4-g3e48afc", true},
		{"2.1.2-1-gabcdef0", "2.1.2-4-g3e48afc", false},
	} {
		assert.Equal(t, tc.want, supportsIP(tc.version, tc.own), "%s with agentapi %s", tc.version, tc.own)
	}
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

// lockedBuffer is the output a running hub writes while a test reads it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Once it listens, the hub logs the command that joins a machine, with the
// port it got, and never the secret: others may read the log.
func TestServeLogsHowToJoin(t *testing.T) {
	t.Setenv("AGENTAPI_HUB_LOG_LEVEL", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out lockedBuffer
	cmd := serveCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{
		"--token", "t", "--secret", "join-secret", "--bind", "127.0.0.1", "--port", "0",
		"--state", filepath.Join(t.TempDir(), "instances.json"),
	})
	served := make(chan error, 1)
	go func() { served <- cmd.ExecuteContext(ctx) }()
	require.Eventually(t, func() bool { return strings.Contains(out.String(), "Add dialog") }, 10*time.Second, 10*time.Millisecond)
	cancel()
	require.NoError(t, <-served)

	logged := out.String()
	assert.Regexp(t, `command="agentapi hub join http://127\.0\.0\.1:[1-9][0-9]*/hooks --public-url 'http://\{ip\}:\{port\}'"`, logged)
	assert.Contains(t, logged, "AGENTAPI_HUB_SECRET")
	assert.NotContains(t, logged, "join-secret")
}

// An unknown --log-level (or AGENTAPI_HUB_LOG_LEVEL) stops the hub before
// it starts.
func TestServeRefusesAnUnknownLogLevel(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		env  string
	}{
		"a flag":     {args: []string{"--log-level", "verbose"}},
		"a variable": {env: "loud"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("AGENTAPI_HUB_LOG_LEVEL", tc.env)
			cmd := serveCmd()
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs(append(tc.args, "--no-token", "--secret", "s", "--bind", "127.0.0.1", "--port", "0",
				"--state", filepath.Join(t.TempDir(), "instances.json")))
			assert.ErrorContains(t, cmd.Execute(), "invalid --log-level: unknown log level")
		})
	}
}

// The hub writes its file a moment after a change, and once more when it
// stops: a change just before it stops is kept.
func TestServeKeepsTheLastChangeOnStop(t *testing.T) {
	t.Setenv("AGENTAPI_HUB_LOG_LEVEL", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	statePath := filepath.Join(t.TempDir(), "instances.json")
	var out lockedBuffer
	cmd := serveCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--token", "t", "--secret", "s3cret", "--bind", "127.0.0.1", "--port", "0", "--state", statePath})
	served := make(chan error, 1)
	go func() { served <- cmd.ExecuteContext(ctx) }()
	var address string
	require.Eventually(t, func() bool {
		_, rest, ok := strings.Cut(out.String(), `msg="Hub listening" address=`)
		address, _, _ = strings.Cut(rest, " ")
		return ok && address != ""
	}, 10*time.Second, 10*time.Millisecond)

	body, err := json.Marshal(map[string]any{
		"id": "d1", "type": "instance.heartbeat", "created_at": time.Now(),
		"instance": map[string]any{"id": "dev:1", "server_id": "s1", "url": "http://127.0.0.1:1"},
	})
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, "http://"+address+"/hooks", bytes.NewReader(body))
	require.NoError(t, err)
	timestamp := fmt.Sprint(time.Now().Unix())
	req.Header.Set(instanceapi.HeaderTimestamp, timestamp)
	req.Header.Set(instanceapi.HeaderSignature, instanceapi.Sign("s3cret", timestamp, body))
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	cancel()
	require.NoError(t, <-served)

	data, err := os.ReadFile(statePath)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"dev:1"`)
}
