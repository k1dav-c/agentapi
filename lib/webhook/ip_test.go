package webhook

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/quartz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/convstate"
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
	d := testDispatcher(t, Config{
		URL:       "http://192.168.9.210:3290/hooks",
		Events:    []string{"instance.heartbeat"},
		Heartbeat: time.Minute,
		PublicURL: "http://{ip}:{port}",
	}, clock)
	d.ip = route.resolver(clock)
	var changed atomic.Int32
	d.urlChanged = func() { changed.Add(1) }
	d.snapshot = func() any { return WebhookInstanceState{Status: convstate.AgentStatusRunning} }
	d.SetInstance(WebhookInstance{ID: "dev-1:3285", Port: 3285})
	assert.Empty(t, d.Instance().URL, "not looked up yet")
	d.RefreshIP(t.Context())

	for range 5 {
		assert.Equal(t, "http://192.168.9.48:3285", d.Instance().URL)
	}
	assert.Equal(t, []string{"192.168.9.210:3290"}, route.dials(), "looked up once, toward the receiver")
	assert.Equal(t, "http://{ip}:{port}", d.Config().PublicURL, "the template stays")

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
	d := testDispatcher(t, Config{PublicURL: "http://{ip}:{port}"}, clock)
	d.ip = (&fakeRoute{}).resolver(clock)
	d.SetInstance(WebhookInstance{ID: "dev-1:3285", Port: 3285})
	assert.Empty(t, d.Instance().URL)

	// The other placeholders need no lookup.
	require.NoError(t, d.UpdateConfig(Config{PublicURL: "http://dev-1.vpn:{port}"}))
	assert.Equal(t, "http://dev-1.vpn:3285", d.Instance().URL)
}
