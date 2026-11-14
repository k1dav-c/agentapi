package dispatch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/host"
	"github.com/coder/agentapi/lib/jsonapi"
)

// The URLs and addresses the hub calls a launcher at: http or https
// without credentials, on any address but the hub's own machine's
// (loopback, unless allowed), the link's and none.
func TestCheckLauncherURL(t *testing.T) {
	for raw, ok := range map[string]bool{
		"http://192.168.1.20:3291":        true,
		"http://10.0.0.5:3291/":           true,
		"https://172.16.4.1:3291":         true,
		"http://100.64.0.7:3291":          true,
		"http://203.0.113.5:3291":         true,
		"http://[fd00::5]:3291":           true,
		"http://pve-1.lan:3291":           true,
		"http://127.0.0.1:3291":           false,
		"http://127.1.2.3:3291":           false,
		"http://[::1]:3291":               false,
		"http://[::ffff:127.0.0.1]:3291":  false,
		"http://169.254.169.254/latest":   false,
		"http://[::ffff:169.254.169.254]": false,
		"http://[fe80::1]:3291":           false,
		"http://0.0.0.0:3291":             false,
		"http://0.1.2.3:3291":             false,
		"http://[::]:3291":                false,
		"http://224.0.0.1:3291":           false,
		"ftp://192.168.1.20/":             false,
		"file:///etc/passwd":              false,
		"http://user:pw@192.168.1.20/":    false,
		"http:///jobs":                    false,
		"192.168.1.20:3291":               false,
	} {
		err := checkLauncherURL(raw, false)
		assert.Equal(t, ok, err == nil, "%s: %v", raw, err)
	}
	assert.NoError(t, checkLauncherURL("http://127.0.0.1:3291", true), "allowed by the hub's flag")
	assert.NoError(t, checkLauncherURL("http://[::1]:3291", true))
	assert.Error(t, checkLauncherURL("http://169.254.169.254", true), "never")
	assert.Error(t, checkLauncherAddr(netip.MustParseAddr("0.0.0.0"), true))
}

// recorder is a server that counts the requests it gets.
func recorder(t *testing.T) (*httptest.Server, *atomic.Int32) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// A host event can't make the hub call an address it doesn't call a
// launcher at: not by its URL, not by a name that resolves there, not by a
// URL kept before the hub checked them, not by a redirect. A launcher at
// an allowed address is called.
func TestLauncherAddressRefused(t *testing.T) {
	now := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	newHub := func(allowLoopback bool) *Service {
		s, err := New(Config{
			StatePath: filepath.Join(t.TempDir(), "dispatch.json"), Secret: "hub-secret",
			Now: func() time.Time { return now }, Location: time.UTC, AllowLoopback: allowLoopback,
		})
		require.NoError(t, err)
		t.Cleanup(s.Close)
		return s
	}
	event := func(name, url string, jobs ...host.JobInfo) json.RawMessage {
		data, _ := json.Marshal(host.HostEventData{
			HostInfo: host.HostInfo{Hostname: name, URL: url, Repos: []string{"acme/widget"}, Agents: []string{"claude"}, DefaultAgent: "claude", MaxJobs: 2, Jobs: jobs},
			Token:    "token-" + name, HeartbeatSeconds: 60,
		})
		return data
	}
	// A job no start asked for: the hub stops it on the launcher by itself.
	orphan := host.JobInfo{Key: "acme/widget#7", Agent: "claude", InstanceID: "x~acme~widget~7", ServerID: "s", State: host.JobRunning}
	target, calls := recorder(t)
	port := target.URL[strings.LastIndex(target.URL, ":"):]

	s := newHub(false)
	for _, url := range []string{target.URL, "http://169.254.169.254" + port, "http://[::1]" + port, "http://0.0.0.0" + port} {
		err := s.HostEvent(host.EventHostHeartbeat, event("evil", url, orphan))
		assert.Error(t, err, url)
	}
	assert.Empty(t, s.Hosts(), "refused, the machine isn't added")

	// A name is checked where it resolves to, at the connection.
	require.NoError(t, s.HostEvent(host.EventHostHeartbeat, event("named", "http://localhost"+port, orphan)))
	_, err := s.Dispatch(context.Background(), Request{Action: ActionStart, Repo: "acme/widget", Issue: 12, Host: "named"})
	require.NoError(t, err)
	s.waitStarts()
	job := s.job("acme/widget#12")
	assert.Equal(t, JobFailed, job.State)
	assert.Contains(t, job.Error, "loopback")
	_, err = s.Dispatch(context.Background(), Request{Action: ActionSend, Repo: "acme/widget", Issue: 12, Message: "hi"})
	assert.Error(t, err)

	// A machine kept by a hub that didn't check its URL.
	s.mu.Lock()
	s.hosts["kept"] = &Host{HostInfo: host.HostInfo{Hostname: "kept", URL: target.URL}, Token: "token-kept"}
	s.mu.Unlock()
	s.stopOnLauncher(Host{HostInfo: host.HostInfo{Hostname: "kept", URL: target.URL}, Token: "token-kept"}, "acme/widget#7", true)
	time.Sleep(50 * time.Millisecond)
	assert.Zero(t, calls.Load(), "the forbidden address got no request")

	// A launcher allowed, on loopback with the hub's flag, is called; its
	// redirect elsewhere isn't followed.
	allowed := newHub(true)
	require.NoError(t, allowed.HostEvent(host.EventHostHeartbeat, event("local", target.URL, orphan)))
	assert.Eventually(t, func() bool { return calls.Load() == 1 }, 5*time.Second, 10*time.Millisecond, "the orphan job is stopped there")
	elsewhere, elsewhereCalls := recorder(t)
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	err = allowed.callLauncher(context.Background(), Host{HostInfo: host.HostInfo{Hostname: "r", URL: redirect.URL}, Token: "t"}, http.MethodDelete, "/jobs/acme~widget~7", nil, nil)
	assert.True(t, jsonapi.IsCode(err, "host_failed"), "%v", err)
	assert.Zero(t, elsewhereCalls.Load(), "the redirect isn't followed")
}
