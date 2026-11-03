// Package netaddr finds this machine's IPv4 addresses: the ones other
// machines may reach it at (the URLs a hub is joined by), and the one it
// reaches a given host from ({ip} in --public-url).
package netaddr

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

// Interface is a network interface of this machine.
type Interface struct {
	Name     string
	Up       bool
	Loopback bool
	Addrs    []netip.Addr
}

// Address is an IPv4 address and the name of its interface.
type Address struct {
	Addr      netip.Addr
	Interface string
}

// containerPrefixes start the names of the interfaces of the containers
// and VMs on this machine, which other machines don't reach it by.
var containerPrefixes = []string{
	"docker", "br-", "veth", "virbr", "cni", "flannel", "cali", "cilium", "weave", "kube",
	"podman", "lxc", "lxd", "vmnet", "vboxnet",
}

// cgnat is the shared address space (RFC 6598), which Tailscale and other
// VPNs give their machines.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// Reachable are the IPv4 addresses of interfaces that other machines may
// reach this one at: no loopback, link-local, container or bridge address,
// nor one of an interface that is down. Private networks (RFC 1918) come
// first, then CGNAT, then the rest, each in the interfaces' order.
func Reachable(interfaces []Interface) []Address {
	var found []Address
	seen := map[netip.Addr]bool{}
	for _, iface := range interfaces {
		if !iface.Up || iface.Loopback || slices.ContainsFunc(containerPrefixes, func(prefix string) bool { return strings.HasPrefix(iface.Name, prefix) }) {
			continue
		}
		for _, addr := range iface.Addrs {
			addr = addr.Unmap()
			if !addr.Is4() || !addr.IsGlobalUnicast() || seen[addr] {
				continue
			}
			seen[addr] = true
			found = append(found, Address{Addr: addr, Interface: iface.Name})
		}
	}
	slices.SortStableFunc(found, func(a, b Address) int { return rank(a.Addr) - rank(b.Addr) })
	return found
}

// rank orders the addresses by how likely other machines reach this one
// at them: a private network, then CGNAT, then the rest.
func rank(addr netip.Addr) int {
	switch {
	case addr.IsPrivate():
		return 0
	case cgnat.Contains(addr):
		return 1
	default:
		return 2
	}
}

// Interfaces are this machine's network interfaces.
func Interfaces() ([]Interface, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	list := make([]Interface, 0, len(ifaces))
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		found := Interface{Name: iface.Name, Up: iface.Flags&net.FlagUp != 0, Loopback: iface.Flags&net.FlagLoopback != 0}
		for _, a := range addrs {
			var ip net.IP
			switch a := a.(type) {
			case *net.IPNet:
				ip = a.IP
			case *net.IPAddr:
				ip = a.IP
			}
			if addr, ok := netip.AddrFromSlice(ip); ok {
				found.Addrs = append(found.Addrs, addr)
			}
		}
		list = append(list, found)
	}
	return list, nil
}

// Target is the host and port a URL's requests go to ("" for no URL or
// one without a host): what Resolver finds the route to.
func Target(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	return net.JoinHostPort(u.Hostname(), port)
}

// DefaultTTL is how long Resolver keeps an answer.
const DefaultTTL = 30 * time.Second

// DialTimeout bounds a lookup of the route: a hostname target is resolved
// by DNS first, which may hang.
const DialTimeout = 3 * time.Second

// Resolver finds the IPv4 address this machine reaches a target (host and
// port) from, as the routing table picks it, and keeps the answer for a
// while: it changes when DHCP gives the machine another address. Without
// a target, or when there is no IPv4 route to it, it takes the first
// address Reachable finds. When it finds none it keeps its last answer.
//
// It sends no packet: it only "connects" a UDP socket, which picks the
// route and the source address.
type Resolver struct {
	// Dial opens the UDP socket; nil is a net.Dialer's DialContext. It is
	// given a context that ends within DialTimeout.
	Dial func(ctx context.Context, network, address string) (net.Conn, error)
	// Interfaces lists the machine's interfaces; nil is Interfaces.
	Interfaces func() ([]Interface, error)
	// Now is the time; nil is time.Now.
	Now func() time.Time
	// TTL is how long an answer is kept; 0 is DefaultTTL.
	TTL time.Duration
	// Logger, if set, is told once when no address is found, and again
	// once one is.
	Logger *slog.Logger

	mu     sync.Mutex
	target string
	at     time.Time
	ip     string // the last address found
	err    error  // why the last lookup found none
}

// Resolve returns the address this machine reaches target from. On a
// failure it returns its last answer, if any, with the error.
func (r *Resolver) Resolve(target string) (string, error) {
	return r.ResolveContext(context.Background(), target)
}

// ResolveContext is Resolve, giving up on the route when ctx ends or after
// DialTimeout. It may take that long when the answer is stale: callers
// that hold a lock others wait for should keep its last answer instead.
func (r *Resolver) ResolveContext(ctx context.Context, target string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	ttl := r.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if !r.at.IsZero() && r.target == target && now.Sub(r.at) < ttl {
		return r.ip, r.err
	}
	ip, err := r.lookup(ctx, target)
	r.target, r.at = target, now
	if err != nil {
		if r.err == nil && r.Logger != nil {
			r.Logger.Warn("Found no IPv4 address of this machine for {ip}; keeping the last one", "target", target, "last", r.ip, "error", err)
		}
		r.err = err
		return r.ip, err
	}
	if r.err != nil && r.Logger != nil {
		r.Logger.Info("Found this machine's IPv4 address for {ip} again", "target", target, "ip", ip)
	}
	r.ip, r.err = ip, nil
	return ip, nil
}

func (r *Resolver) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Resolver) lookup(ctx context.Context, target string) (string, error) {
	var routeErr error
	if target != "" {
		addr, err := r.route(ctx, target)
		if err == nil {
			return addr.String(), nil
		}
		routeErr = err
	}
	list := Interfaces
	if r.Interfaces != nil {
		list = r.Interfaces
	}
	interfaces, err := list()
	if err != nil {
		return "", errors.Join(routeErr, fmt.Errorf("list the network interfaces: %w", err))
	}
	if found := Reachable(interfaces); len(found) > 0 {
		return found[0].Addr.String(), nil
	}
	return "", errors.Join(routeErr, errors.New("no interface has an IPv4 address other machines may reach"))
}

// route is the local address of a UDP socket connected to target.
func (r *Resolver) route(ctx context.Context, target string) (netip.Addr, error) {
	dial := (&net.Dialer{}).DialContext
	if r.Dial != nil {
		dial = r.Dial
	}
	ctx, cancel := context.WithTimeout(ctx, DialTimeout)
	defer cancel()
	conn, err := dial(ctx, "udp4", target)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("find the route to %s: %w", target, err)
	}
	defer func() { _ = conn.Close() }()
	local, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return netip.Addr{}, fmt.Errorf("find the route to %s: no UDP address", target)
	}
	addr, ok := netip.AddrFromSlice(local.IP)
	if addr = addr.Unmap(); !ok || !addr.Is4() || addr.IsUnspecified() {
		return netip.Addr{}, fmt.Errorf("find the route to %s: no IPv4 source address", target)
	}
	return addr, nil
}
