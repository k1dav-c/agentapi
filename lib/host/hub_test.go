package host

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/instanceapi"
)

// fakeHub checks the launcher's signed requests and records them.
type fakeHub struct {
	mu     sync.Mutex
	events []instanceapi.WebhookEvent
	asked  []HubTokenRequest
}

func (f *fakeHub) handler(t *testing.T, secret string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := instanceapi.Verify(secret, r.Header, body, time.Now()); err != nil || r.Header.Get(instanceapi.HeaderDelivery) == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.URL.Path {
		case "/hooks":
			var e instanceapi.WebhookEvent
			require.NoError(t, json.Unmarshal(body, &e))
			f.events = append(f.events, e)
			w.WriteHeader(http.StatusNoContent)
		case HubTokenPath:
			var req HubTokenRequest
			require.NoError(t, json.Unmarshal(body, &req))
			f.asked = append(f.asked, req)
			_ = json.NewEncoder(w).Encode(Token{Value: "ghs_hub", ExpiresAt: time.Now().Add(time.Hour)})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func TestHubCredentials(t *testing.T) {
	f := &fakeHub{}
	srv := httptest.NewServer(f.handler(t, "s3cret"))
	defer srv.Close()
	creds := &HubCredentials{URL: srv.URL + "/hooks", Secret: "s3cret", Hostname: "pve", Client: srv.Client()}
	key := Key{Owner: "acme", Name: "widget", Issue: 4}
	for range 2 {
		token, err := creds.Token(context.Background(), key)
		require.NoError(t, err)
		assert.Equal(t, "ghs_hub", token.Value)
	}
	assert.Equal(t, []HubTokenRequest{{Hostname: "pve", Job: "acme~widget~4"}}, f.asked, "kept until it nears expiry")

	wrong := &HubCredentials{URL: srv.URL + "/hooks", Secret: "nope", Hostname: "pve", Client: srv.Client()}
	_, err := wrong.Token(context.Background(), key)
	assert.ErrorContains(t, err, "401")
}

func TestReportToHub(t *testing.T) {
	f := &fakeHub{}
	srv := httptest.NewServer(f.handler(t, "s3cret"))
	defer srv.Close()
	config, err := Config{Agents: []string{"native-demo"}, AgentAPI: "/bin/true", Dir: t.TempDir()}.withDefaults(t.TempDir())
	require.NoError(t, err)
	config.HubURL, config.HubSecret = srv.URL+"/hooks", "s3cret"
	l, err := New(config, slog.New(slog.DiscardHandler))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { l.reportLoop(ctx); close(done) }()
	require.Eventually(t, func() bool { f.mu.Lock(); defer f.mu.Unlock(); return len(f.events) == 1 }, 5*time.Second, 20*time.Millisecond)
	cancel()
	<-done
	f.mu.Lock()
	defer f.mu.Unlock()
	require.Len(t, f.events, 2)
	assert.Equal(t, EventHostStarted, f.events[0].Type)
	assert.Equal(t, EventHostStopped, f.events[1].Type)
	assert.Equal(t, "host~"+l.hostname, f.events[0].Instance.ID)
	data, _ := json.Marshal(f.events[0].Data)
	var d HostEventData
	require.NoError(t, json.Unmarshal(data, &d))
	assert.Equal(t, l.hostToken, d.Token)
	assert.Equal(t, []string{"native-demo"}, d.Agents)
	assert.True(t, d.HubCredentials)

	assert.Equal(t, []string{"instance.*", "task.*"}, withTaskEvents(nil))
	assert.Equal(t, []string{"instance.*", "request.*", "task.*"}, withTaskEvents([]string{"instance.*", "request.*"}))
	assert.Equal(t, []string{"*"}, withTaskEvents([]string{"*"}))
}
