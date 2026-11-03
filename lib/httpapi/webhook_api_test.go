package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/quartz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/convstate"
	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/logctx"
	"github.com/coder/agentapi/lib/native/nativetest"
	"github.com/coder/agentapi/lib/netaddr"
	"github.com/coder/agentapi/lib/webhook"
)

func testDispatcher(t *testing.T, config WebhookConfig, clock quartz.Clock) *webhook.Dispatcher {
	t.Helper()
	if config.URL == "" {
		config.URL = "https://example.com/hook"
	}
	d, err := webhook.New(config, slog.New(slog.NewTextHandler(io.Discard, nil)), instanceapi.AgentTypeClaude, clock)
	require.NoError(t, err)
	return d
}

// routeConn is a connected UDP socket as far as the resolver looks at it.
type routeConn struct {
	net.Conn
	local net.Addr
}

func (c routeConn) LocalAddr() net.Addr { return c.local }
func (c routeConn) Close() error        { return nil }

// fakeRoute stands for the machine's routing table, as in lib/webhook: the
// source address of the route to the webhook's receiver.
type fakeRoute struct {
	mu      sync.Mutex
	source  string // "" has no route
	targets []string
}

func (f *fakeRoute) resolver(clock quartz.Clock) *netaddr.Resolver {
	return &netaddr.Resolver{
		Dial: func(_ context.Context, _, address string) (net.Conn, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.targets = append(f.targets, address)
			if f.source == "" {
				return nil, errors.New("connect: network is unreachable")
			}
			return routeConn{local: &net.UDPAddr{IP: net.ParseIP(f.source), Port: 40000}}, nil
		},
		Interfaces: func() ([]netaddr.Interface, error) { return nil, nil },
		Now:        func() time.Time { return clock.Now() },
	}
}

func TestWebhookConfigCanBeUpdatedAtRuntime(t *testing.T) {
	t.Parallel()

	dispatcher, err := webhook.New(
		WebhookConfig{
			URL:         "https://initial.example.com/hook",
			Timeout:     5 * time.Second,
			MaxAttempts: 2,
		},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		instanceapi.AgentTypeClaude,
		quartz.NewReal(),
	)
	require.NoError(t, err)
	server := &webhookAPI{webhook: dispatcher}

	// Update URL, timeout, and max attempts.
	request := &WebhookConfigRequest{}
	request.Body.URL = "https://updated.example.com/hook"
	request.Body.TimeoutSeconds = 12
	request.Body.MaxAttempts = 4
	response, err := server.updateWebhookConfig(t.Context(), request)
	require.NoError(t, err)
	assert.Equal(t, request.Body.URL, response.Body.URL)
	assert.Equal(t, 12, response.Body.TimeoutSeconds)
	assert.Equal(t, 4, response.Body.MaxAttempts)
	assert.Empty(t, response.Body.PayloadTemplate)

	// Set a payload template.
	tmpl := `{"s":"{{.Status}}"}`
	request.Body.PayloadTemplate = &tmpl
	response, err = server.updateWebhookConfig(t.Context(), request)
	require.NoError(t, err)
	assert.Equal(t, tmpl, response.Body.PayloadTemplate)

	// Omit PayloadTemplate (nil) — should preserve existing.
	request.Body.PayloadTemplate = nil
	response, err = server.updateWebhookConfig(t.Context(), request)
	require.NoError(t, err)
	assert.Equal(t, tmpl, response.Body.PayloadTemplate)

	// Clear PayloadTemplate with empty string.
	clearTmpl := ""
	request.Body.PayloadTemplate = &clearTmpl
	response, err = server.updateWebhookConfig(t.Context(), request)
	require.NoError(t, err)
	assert.Empty(t, response.Body.PayloadTemplate)

	// Disable webhook by clearing URL.
	request.Body.URL = ""
	response, err = server.updateWebhookConfig(t.Context(), request)
	require.NoError(t, err)
	assert.Empty(t, response.Body.URL)
}

// PUT /webhook takes a public URL with placeholders (agentapi hub join
// sends {ip} to an AgentAPI that fills it in), and GET /webhook returns it
// as set; the summary names the address.
func TestWebhookAPIPublicURLTemplate(t *testing.T) {
	t.Parallel()
	clock := quartz.NewMock(t)
	d := testDispatcher(t, WebhookConfig{}, clock)
	d.SetResolver((&fakeRoute{source: "10.0.0.5"}).resolver(clock))
	d.SetInstance(WebhookInstance{ID: "dev-1:3285", Port: 3285})
	api := &webhookAPI{webhook: d}

	put := func(publicURL string) (*WebhookConfigResponse, error) {
		request := &WebhookConfigRequest{}
		request.Body.URL = "http://192.168.9.210:3290/hooks"
		request.Body.TimeoutSeconds = 5
		request.Body.MaxAttempts = 3
		request.Body.PublicURL = &publicURL
		return api.updateWebhookConfig(t.Context(), request)
	}
	response, err := put("http://{ip}:3285")
	require.NoError(t, err)
	assert.Equal(t, "http://{ip}:3285", response.Body.PublicURL)
	assert.Equal(t, "http://10.0.0.5:3285", d.Instance().URL)

	_, err = put("http://{ip}:{port}")
	require.NoError(t, err)
	assert.Equal(t, "http://10.0.0.5:3285", d.Instance().URL)

	_, err = put("http://{ip}:{nope}")
	require.ErrorContains(t, err, "invalid public URL")
}

// The address {ip} stands for is an allowed host: --allowed-hosts can't
// name one that DHCP changes. Others stay refused.
func TestPublicIPIsAnAllowedHost(t *testing.T) {
	t.Parallel()
	ctx := logctx.WithLogger(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	factory := &nativetest.Factory{}
	clock := quartz.NewMock(t)
	srv, err := NewServer(ctx, ServerConfig{
		AgentType:      instanceapi.AgentTypeClaude,
		Native:         NativeConfig{Factory: factory.New},
		ChatBasePath:   "/chat",
		AllowedHosts:   []string{"localhost"},
		AllowedOrigins: []string{"*"},
		Clock:          clock,
		Webhook:        WebhookConfig{URL: "http://192.168.9.210:3290/hooks", PublicURL: "http://{ip}:3285"},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })
	route := &fakeRoute{source: "192.168.9.49"}
	srv.webhook.SetResolver(route.resolver(clock))
	srv.webhook.RefreshIP(t.Context())

	status := func(host string) int {
		req := httptest.NewRequest(http.MethodGet, "/status", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec.Code
	}
	assert.Equal(t, http.StatusOK, status("localhost:3285"))
	assert.Equal(t, http.StatusOK, status("192.168.9.49:3285"))
	assert.Equal(t, http.StatusBadRequest, status("192.168.9.48:3285"))
	assert.Equal(t, http.StatusBadRequest, status("evil.example:3285"))
}

// A lookup of {ip} that hangs (DNS for the receiver's hostname) holds up
// neither the events nor the summary, which the emitter builds under its
// lock: they name the last address found.
func TestSlowPublicIPLookupBlocksNoEvent(t *testing.T) {
	t.Parallel()
	ctx := logctx.WithLogger(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	factory := &nativetest.Factory{}
	clock := quartz.NewMock(t)
	srv, err := NewServer(ctx, ServerConfig{
		AgentType:      instanceapi.AgentTypeClaude,
		Native:         NativeConfig{Factory: factory.New},
		ChatBasePath:   "/chat",
		AllowedHosts:   []string{"*"},
		AllowedOrigins: []string{"*"},
		Clock:          clock,
		Webhook:        WebhookConfig{URL: "http://192.0.2.10:3290/hooks", PublicURL: "http://{ip}:3285", Events: []string{"run.status_changed"}},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })
	srv.webhook.SetResolver((&fakeRoute{source: "192.168.9.49"}).resolver(clock))
	srv.webhook.RefreshIP(t.Context())

	// The next lookup hangs.
	dialing := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	srv.webhook.SetResolver(&netaddr.Resolver{
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			close(dialing)
			select {
			case <-release:
			case <-ctx.Done():
			}
			return nil, errors.New("lookup hub.vpn: i/o timeout")
		},
		Interfaces: func() ([]netaddr.Interface, error) { return nil, nil },
		Now:        func() time.Time { return clock.Now() },
	})
	go srv.webhook.RefreshIP(t.Context())
	<-dialing

	done := make(chan convstate.SummaryBody)
	go func() {
		srv.emitter.EmitStatus(conv.StatusChanging)
		srv.emitter.EmitStatus(conv.StatusStable)
		done <- srv.emitter.Summary()
	}()
	select {
	case summary := <-done:
		assert.Equal(t, "http://192.168.9.49:3285", summary.Instance.URL, "the last address")
	case <-time.After(5 * time.Second):
		t.Fatal("the emitter waited for the lookup")
	}
}

// A test event goes out once, whatever the events chosen, and says how the
// receiver answered.
func TestWebhookTestEvent(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	var event string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		event = r.Header.Get("X-AgentAPI-Event")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	d := testDispatcher(t, WebhookConfig{URL: server.URL, Events: []string{"task.*"}}, quartz.NewReal())
	s := &webhookAPI{webhook: d}
	response, err := s.testWebhook(t.Context(), nil)
	require.NoError(t, err)
	assert.False(t, response.Body.OK)
	assert.Equal(t, "503 Service Unavailable", response.Body.Status)
	assert.Contains(t, response.Body.Error, "503")
	assert.EqualValues(t, 1, calls.Load(), "not retried")
	assert.Equal(t, instanceapi.EventWebhookTest, event)

	require.NoError(t, d.UpdateConfig(WebhookConfig{}))
	response, err = s.testWebhook(t.Context(), nil)
	require.NoError(t, err)
	assert.Equal(t, "no webhook URL is set", response.Body.Error)
}

// The instance.* events say how often the heartbeat comes, so a receiver
// knows when it missed some.
func TestWebhookInstanceStateHeartbeat(t *testing.T) {
	t.Parallel()
	srv, _ := newQuietNativeServer(t, nil)
	assert.Equal(t, 60, srv.webhookInstanceState().HeartbeatSeconds)
	off := 0
	request := &WebhookConfigRequest{}
	request.Body.TimeoutSeconds = 1
	request.Body.MaxAttempts = 1
	request.Body.HeartbeatSeconds = &off
	_, err := (&webhookAPI{webhook: srv.webhook}).updateWebhookConfig(t.Context(), request)
	require.NoError(t, err)
	assert.Zero(t, srv.webhookInstanceState().HeartbeatSeconds)
}

// The observer runs while the webhook sends events it makes (see
// lib/webhook): PUT /webhook starts and stops it.
func TestWebhookObserverRunsForItsEvents(t *testing.T) {
	t.Parallel()
	srv, _ := newQuietNativeServer(t, nil)
	subscriptions := srv.emitter.Subscribers
	assert.Zero(t, subscriptions())
	w := &webhookAPI{webhook: srv.webhook}
	put := func(url string, events ...string) {
		request := &WebhookConfigRequest{}
		request.Body.URL = url
		request.Body.TimeoutSeconds = 1
		request.Body.MaxAttempts = 1
		request.Body.Events = &events
		_, err := w.updateWebhookConfig(t.Context(), request)
		require.NoError(t, err)
	}
	put("http://127.0.0.1:1/hooks", "instance.*")
	put("http://127.0.0.1:1/hooks", "instance.*", "run.status_changed")
	assert.Never(t, func() bool { return subscriptions() > 0 }, 200*time.Millisecond, 10*time.Millisecond, "nothing for it to send")
	put("http://127.0.0.1:1/hooks", "instance.*", "task.*")
	require.Eventually(t, func() bool { return subscriptions() == 1 }, 5*time.Second, 10*time.Millisecond)
	put("http://127.0.0.1:2/hooks", "instance.*", "task.*")
	assert.Equal(t, 1, subscriptions(), "another URL keeps it")
	put("http://127.0.0.1:2/hooks", "instance.*")
	assert.Zero(t, subscriptions(), "stopped when its events went")
	put("http://127.0.0.1:2/hooks", "*")
	require.Eventually(t, func() bool { return subscriptions() == 1 }, 5*time.Second, 10*time.Millisecond)
	put("", "*")
	assert.Zero(t, subscriptions(), "stopped when the URL went")
}
