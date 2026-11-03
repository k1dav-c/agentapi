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
	"github.com/coder/agentapi/lib/logctx"
	mf "github.com/coder/agentapi/lib/msgfmt"
	"github.com/coder/agentapi/lib/native/nativetest"
	"github.com/coder/agentapi/lib/netaddr"
)

// routeConn is a connected UDP socket as far as the resolver looks at it.
type routeConn struct {
	net.Conn
	local net.Addr
}

func (c routeConn) LocalAddr() net.Addr { return c.local }
func (c routeConn) Close() error        { return nil }

// fakeRoute stands for the machine's routing table: the source address of
// the route to the webhook's receiver, which DHCP may change.
type fakeRoute struct {
	mu      sync.Mutex
	source  string // "" has no route
	targets []string
}

func (f *fakeRoute) set(source string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.source = source
}

func (f *fakeRoute) dials() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.targets...)
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

// {ip} in the public URL is filled in for every event with the address on
// the route to the receiver, looked up at most every 30 seconds: after DHCP
// gives the machine another address, the next heartbeat names it, and the
// summary and registry learn of it.
func TestWebhookPublicURLFollowsTheAddress(t *testing.T) {
	t.Parallel()
	clock := quartz.NewMock(t)
	route := &fakeRoute{source: "192.168.9.48"}
	d := testDispatcher(t, WebhookConfig{
		URL:       "http://192.168.9.210:3290/hooks",
		Events:    []string{"instance.heartbeat"},
		Heartbeat: time.Minute,
		PublicURL: "http://{ip}:{port}",
	}, clock)
	d.ip = route.resolver(clock)
	var changed atomic.Int32
	d.urlChanged = func() { changed.Add(1) }
	d.snapshot = func() any { return WebhookInstanceState{Status: AgentStatusRunning} }
	d.setInstance(WebhookInstance{ID: "dev-1:3285", Port: 3285})
	assert.Empty(t, d.instanceSnapshot().URL, "not looked up yet")
	d.refreshIP(t.Context())

	for range 5 {
		assert.Equal(t, "http://192.168.9.48:3285", d.instanceSnapshot().URL)
	}
	assert.Equal(t, []string{"192.168.9.210:3290"}, route.dials(), "looked up once, toward the receiver")
	assert.Equal(t, "http://{ip}:{port}", d.configSnapshot().PublicURL, "the template stays")

	trap := clock.Trap().NewTimer("webhook", "heartbeat")
	defer trap.Close()
	ctx := t.Context()
	go d.heartbeats(ctx)
	trap.MustWait(ctx).Release()

	// The workspace got another address.
	route.set("192.168.9.49")
	clock.Advance(time.Minute).MustWait(ctx)
	trap.MustWait(ctx).Release()
	events := queued(d)
	require.Len(t, events, 1)
	assert.Equal(t, webhookEventInstanceHeartbeat, events[0].Type)
	assert.Equal(t, "http://192.168.9.49:3285", events[0].Instance.URL, "the new address")
	assert.Len(t, route.dials(), 2)
	require.Eventually(t, func() bool { return changed.Load() == 1 }, 5*time.Second, 10*time.Millisecond)

	// No route and no interface: the last address stays.
	route.set("")
	clock.Advance(time.Minute).MustWait(ctx)
	trap.MustWait(ctx).Release()
	events = queued(d)
	require.Len(t, events, 1)
	assert.Equal(t, "http://192.168.9.49:3285", events[0].Instance.URL)
	assert.EqualValues(t, 1, changed.Load())
}

// A public URL whose {ip} was never found is left out of the events.
func TestWebhookPublicURLWithoutAnAddress(t *testing.T) {
	t.Parallel()
	clock := quartz.NewMock(t)
	d := testDispatcher(t, WebhookConfig{PublicURL: "http://{ip}:{port}"}, clock)
	d.ip = (&fakeRoute{}).resolver(clock)
	d.setInstance(WebhookInstance{ID: "dev-1:3285", Port: 3285})
	assert.Empty(t, d.instanceSnapshot().URL)

	// The other placeholders need no lookup.
	require.NoError(t, d.updateConfig(WebhookConfig{PublicURL: "http://dev-1.vpn:{port}"}))
	assert.Equal(t, "http://dev-1.vpn:3285", d.instanceSnapshot().URL)
}

// PUT /webhook takes a public URL with placeholders (agentapi hub join
// sends {ip} to an AgentAPI that fills it in), and GET /webhook returns it
// as set; the summary names the address.
func TestWebhookAPIPublicURLTemplate(t *testing.T) {
	t.Parallel()
	clock := quartz.NewMock(t)
	d := testDispatcher(t, WebhookConfig{}, clock)
	d.ip = (&fakeRoute{source: "10.0.0.5"}).resolver(clock)
	d.setInstance(WebhookInstance{ID: "dev-1:3285", Port: 3285})
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
	assert.Equal(t, "http://10.0.0.5:3285", d.instanceSnapshot().URL)

	_, err = put("http://{ip}:{port}")
	require.NoError(t, err)
	assert.Equal(t, "http://10.0.0.5:3285", d.instanceSnapshot().URL)

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
		AgentType:      mf.AgentTypeClaude,
		Transport:      TransportNative,
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
	srv.webhook.ip = route.resolver(clock)
	srv.webhook.refreshIP(t.Context())

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
		AgentType:      mf.AgentTypeClaude,
		Transport:      TransportNative,
		Native:         NativeConfig{Factory: factory.New},
		ChatBasePath:   "/chat",
		AllowedHosts:   []string{"*"},
		AllowedOrigins: []string{"*"},
		Clock:          clock,
		Webhook:        WebhookConfig{URL: "http://192.0.2.10:3290/hooks", PublicURL: "http://{ip}:3285", Events: []string{webhookEventRunStatusChanged}},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })
	srv.webhook.ip = (&fakeRoute{source: "192.168.9.49"}).resolver(clock)
	srv.webhook.refreshIP(t.Context())

	// The next lookup hangs.
	dialing := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	srv.webhook.ip = &netaddr.Resolver{
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
	}
	go srv.webhook.refreshIP(t.Context())
	<-dialing

	done := make(chan SummaryBody)
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
