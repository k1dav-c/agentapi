package netaddr

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func addrs(list ...string) []netip.Addr {
	parsed := make([]netip.Addr, len(list))
	for i, a := range list {
		parsed[i] = netip.MustParseAddr(a)
	}
	return parsed
}

// The addresses others may reach: no loopback, link-local, container or
// bridge address, nor one of an interface that is down; private networks
// first, then CGNAT, then the rest, each in the interfaces' order.
func TestReachable(t *testing.T) {
	found := Reachable([]Interface{
		{Name: "lo", Up: true, Loopback: true, Addrs: addrs("127.0.0.1", "::1")},
		{Name: "ens5", Up: true, Addrs: addrs("203.0.113.7")},
		{Name: "tailscale0", Up: true, Addrs: addrs("100.116.90.98", "fd7a:115c:a1e0::1")},
		{Name: "docker0", Up: true, Addrs: addrs("172.17.0.1")},
		{Name: "veth1a2b3c", Up: true, Addrs: addrs("169.254.10.1")},
		{Name: "eth0", Up: true, Addrs: addrs("fe80::1", "::ffff:192.168.9.49")},
		{Name: "wlan1", Up: false, Addrs: addrs("10.0.0.6")},
		{Name: "eth1", Up: true, Addrs: addrs("192.168.9.49")},
	})
	assert.Equal(t, []Address{
		{Addr: netip.MustParseAddr("192.168.9.49"), Interface: "eth0"},
		{Addr: netip.MustParseAddr("100.116.90.98"), Interface: "tailscale0"},
		{Addr: netip.MustParseAddr("203.0.113.7"), Interface: "ens5"},
	}, found)
	assert.Empty(t, Reachable(nil))
}

func TestTarget(t *testing.T) {
	assert.Equal(t, "192.168.9.210:3290", Target("http://192.168.9.210:3290/hooks"))
	assert.Equal(t, "hub.example:443", Target("https://hub.example/hooks"))
	assert.Equal(t, "hub.example:80", Target("http://hub.example"))
	assert.Equal(t, "[fd00::1]:3290", Target("http://[fd00::1]:3290/hooks"))
	assert.Empty(t, Target(""))
	assert.Empty(t, Target("not a url"))
}

// udpConn is a connected UDP socket as far as Resolver looks at it.
type udpConn struct {
	net.Conn
	local net.Addr
}

func (c udpConn) LocalAddr() net.Addr { return c.local }
func (c udpConn) Close() error        { return nil }

// fakeNet answers Resolver's dials with the source address it is given,
// and counts them.
type fakeNet struct {
	mu      sync.Mutex
	source  string // "" fails the dial
	dials   []string
	ifaces  []Interface
	listErr error
	lists   int
}

func (f *fakeNet) dial(_ context.Context, network, address string) (net.Conn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dials = append(f.dials, network+" "+address)
	if f.source == "" {
		return nil, errors.New("connect: network is unreachable")
	}
	return udpConn{local: &net.UDPAddr{IP: net.ParseIP(f.source), Port: 40000}}, nil
}

func (f *fakeNet) interfaces() ([]Interface, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lists++
	return f.ifaces, f.listErr
}

func (f *fakeNet) set(source string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.source = source
}

func newResolver(f *fakeNet, now *time.Time, logs *bytes.Buffer) *Resolver {
	return &Resolver{
		Dial:       f.dial,
		Interfaces: f.interfaces,
		Now:        func() time.Time { return *now },
		Logger:     slog.New(slog.NewTextHandler(logs, nil)),
	}
}

// {ip} is the source address of the route to the target, looked up again
// once the answer is 30 seconds old: after DHCP gives the machine another
// address, the next lookup finds it.
func TestResolverFollowsTheRoute(t *testing.T) {
	f := &fakeNet{source: "192.168.9.48"}
	now := time.Unix(1_800_000_000, 0)
	var logs bytes.Buffer
	r := newResolver(f, &now, &logs)

	ip, err := r.Resolve("192.168.9.210:3290")
	require.NoError(t, err)
	assert.Equal(t, "192.168.9.48", ip)
	assert.Equal(t, []string{"udp4 192.168.9.210:3290"}, f.dials, "IPv4 only")
	assert.Zero(t, f.lists, "the route answered")

	f.set("192.168.9.49")
	now = now.Add(29 * time.Second)
	ip, _ = r.Resolve("192.168.9.210:3290")
	assert.Equal(t, "192.168.9.48", ip, "kept for 30 seconds")
	assert.Len(t, f.dials, 1)

	now = now.Add(time.Second)
	ip, err = r.Resolve("192.168.9.210:3290")
	require.NoError(t, err)
	assert.Equal(t, "192.168.9.49", ip, "the new address")
	assert.Len(t, f.dials, 2)

	// Another target is looked up at once.
	f.set("100.116.90.5")
	ip, _ = r.Resolve("100.116.90.98:3290")
	assert.Equal(t, "100.116.90.5", ip)
	assert.Len(t, f.dials, 3)
	assert.Empty(t, logs.String())
}

// Without a target, or without an IPv4 route to it, {ip} is the first
// address Reachable finds.
func TestResolverFallsBackToTheInterfaces(t *testing.T) {
	f := &fakeNet{ifaces: []Interface{
		{Name: "lo", Up: true, Loopback: true, Addrs: addrs("127.0.0.1")},
		{Name: "docker0", Up: true, Addrs: addrs("172.17.0.1")},
		{Name: "tailscale0", Up: true, Addrs: addrs("100.116.90.5")},
		{Name: "eth0", Up: true, Addrs: addrs("192.168.9.48")},
	}}
	now := time.Unix(1_800_000_000, 0)
	var logs bytes.Buffer

	r := newResolver(f, &now, &logs)
	ip, err := r.Resolve("")
	require.NoError(t, err)
	assert.Equal(t, "192.168.9.48", ip)
	assert.Empty(t, f.dials, "no target, no dial")

	r = newResolver(f, &now, &logs)
	ip, err = r.Resolve("hub.vpn:3290")
	require.NoError(t, err)
	assert.Equal(t, "192.168.9.48", ip, "the dial failed")

	f.set("::1")
	r = newResolver(f, &now, &logs)
	ip, err = r.Resolve("[fd00::1]:3290")
	require.NoError(t, err)
	assert.Equal(t, "192.168.9.48", ip, "no IPv4 source address")
	assert.Empty(t, logs.String())
}

// When no address is found, the last one stays, and the failure is
// logged once until one is found again.
func TestResolverKeepsTheLastAddress(t *testing.T) {
	f := &fakeNet{source: "192.168.9.48"}
	now := time.Unix(1_800_000_000, 0)
	var logs bytes.Buffer
	r := newResolver(f, &now, &logs)
	ip, err := r.Resolve("192.168.9.210:3290")
	require.NoError(t, err)
	require.Equal(t, "192.168.9.48", ip)

	f.set("")
	for range 3 {
		now = now.Add(time.Minute)
		ip, err = r.Resolve("192.168.9.210:3290")
		require.Error(t, err)
		assert.Equal(t, "192.168.9.48", ip, "the last address")
	}
	assert.Equal(t, 1, strings.Count(logs.String(), "Found no IPv4 address"), "logged once")

	f.set("192.168.9.49")
	now = now.Add(time.Minute)
	ip, err = r.Resolve("192.168.9.210:3290")
	require.NoError(t, err)
	assert.Equal(t, "192.168.9.49", ip)
	assert.Contains(t, logs.String(), "again")

	// None ever found: nothing to keep.
	f = &fakeNet{listErr: errors.New("no interfaces")}
	r = newResolver(f, &now, &logs)
	ip, err = r.Resolve("")
	require.Error(t, err)
	assert.Empty(t, ip)
}

// A route lookup that hangs (DNS for a hostname target) gives up when the
// context ends, and within DialTimeout without one, falling back to the
// interfaces.
func TestResolverBoundsTheDial(t *testing.T) {
	f := &fakeNet{ifaces: []Interface{{Name: "eth0", Up: true, Addrs: addrs("192.168.9.48")}}}
	now := time.Unix(1_800_000_000, 0)
	var logs bytes.Buffer
	r := newResolver(f, &now, &logs)
	var deadline time.Time
	r.Dial = func(ctx context.Context, _, _ string) (net.Conn, error) {
		deadline, _ = ctx.Deadline()
		<-ctx.Done()
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	ip, err := r.ResolveContext(ctx, "hub.vpn:3290")
	require.NoError(t, err)
	assert.Equal(t, "192.168.9.48", ip)
	assert.Less(t, time.Since(start), DialTimeout)
	assert.False(t, deadline.IsZero(), "the dial has a deadline")
	assert.LessOrEqual(t, time.Until(deadline), DialTimeout)
}
