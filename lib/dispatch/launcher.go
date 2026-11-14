package dispatch

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"syscall"
	"time"
)

// A launcher's URL comes from its host.* events, which any machine with the
// hub's secret can send, and the hub calls it with the launcher's token:
// starts, messages, and the stops reconcileLocked sends by itself. So the
// hub calls only http or https URLs without credentials in them, and never
// an address of its own machine's (loopback, unless Config.AllowLoopback)
// or the link (link-local, such as a cloud's metadata at 169.254.169.254),
// nor an unspecified one: checked in HostEvent for an address in the URL,
// and at every connection for the address a name resolves to. Private
// addresses stay allowed: the launchers are on the LAN, or a VPN.

// checkLauncherURL reports why the hub doesn't call raw, a launcher's URL,
// or nil.
func checkLauncherURL(raw string, allowLoopback bool) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("the launcher's URL %q doesn't parse: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("the launcher's URL %q isn't http or https", raw)
	}
	if u.User != nil {
		return fmt.Errorf("the launcher's URL %q has credentials in it", raw)
	}
	if u.Hostname() == "" {
		return fmt.Errorf("the launcher's URL %q names no host", raw)
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil {
		return checkLauncherAddr(ip, allowLoopback)
	}
	return nil
}

// checkLauncherAddr reports why the hub doesn't connect to a launcher at
// ip, or nil.
func checkLauncherAddr(ip netip.Addr, allowLoopback bool) error {
	ip = ip.Unmap()
	switch {
	case ip.IsLoopback() && !allowLoopback:
		return fmt.Errorf("%s is the hub's own machine (loopback): start the hub with --allow-loopback-launchers for a launcher on it", ip)
	case ip.IsUnspecified() || (ip.Is4() && ip.As4()[0] == 0):
		return fmt.Errorf("%s is no machine's address", ip)
	case ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast():
		return fmt.Errorf("%s is link-local", ip)
	case ip.IsMulticast():
		return fmt.Errorf("%s is a multicast address", ip)
	}
	return nil
}

// newLauncherClient is the client the hub calls the launchers with: it
// connects only to the addresses checkLauncherAddr allows, straight (not
// through the environment's proxy, which would connect for it), and
// follows no redirect: the launcher's API never redirects, and a redirect
// would take the request elsewhere. A redirect is the answer, which
// callLauncher reports as the launcher's failure.
func newLauncherClient(allowLoopback bool) *http.Client {
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		// The address it connects to, after any name is resolved: every
		// connection, also after a DNS change.
		Control: func(_, address string, _ syscall.RawConn) error {
			addr, err := netip.ParseAddrPort(address)
			if err != nil {
				return fmt.Errorf("the launcher's address %s: %w", address, err)
			}
			return checkLauncherAddr(addr.Addr(), allowLoopback)
		},
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = dialer.DialContext
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
