package hub

import (
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

// JoinURL is a URL machines join the hub by (agentapi hub join <URL>),
// with the network interface of its address; none for the --bind address.
type JoinURL struct {
	URL       string `json:"url"`
	Interface string `json:"interface,omitempty"`
}

// netInterface is a network interface of the hub's machine, as the join
// URLs are picked from.
type netInterface struct {
	name     string
	up       bool
	loopback bool
	addrs    []netip.Addr
}

// containerPrefixes start the names of the interfaces of the containers
// and VMs on the hub's machine, which other machines don't reach it by.
var containerPrefixes = []string{
	"docker", "br-", "veth", "virbr", "cni", "flannel", "cali", "cilium", "weave", "kube",
	"podman", "lxc", "lxd", "vmnet", "vboxnet",
}

// cgnat is the shared address space (RFC 6598), which Tailscale and other
// VPNs give their machines.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// joinURLs are the webhook URLs of a hub that listens on bind and port:
// bind's, when it listens on one address; otherwise one for each IPv4
// address of interfaces that other machines may reach, private networks
// (RFC 1918) first, then CGNAT, then the rest.
func joinURLs(bind string, port int, interfaces []netInterface) []JoinURL {
	if !allInterfaces(bind) {
		return []JoinURL{{URL: hooksURL(bind, port)}}
	}
	type ranked struct {
		JoinURL
		rank int
	}
	var found []ranked
	seen := map[netip.Addr]bool{}
	for _, iface := range interfaces {
		if !iface.up || iface.loopback || slices.ContainsFunc(containerPrefixes, func(prefix string) bool { return strings.HasPrefix(iface.name, prefix) }) {
			continue
		}
		for _, addr := range iface.addrs {
			addr = addr.Unmap()
			if !addr.Is4() || !addr.IsGlobalUnicast() || seen[addr] {
				continue
			}
			seen[addr] = true
			found = append(found, ranked{JoinURL{URL: hooksURL(addr.String(), port), Interface: iface.name}, rank(addr)})
		}
	}
	slices.SortStableFunc(found, func(a, b ranked) int { return a.rank - b.rank })
	urls := make([]JoinURL, len(found))
	for i, f := range found {
		urls[i] = f.JoinURL
	}
	return urls
}

// rank orders the addresses by how likely machines reach the hub at them:
// a private network, then CGNAT, then the rest.
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

// allInterfaces reports whether a hub that binds bind listens on every
// interface.
func allInterfaces(bind string) bool {
	if bind == "" {
		return true
	}
	addr, err := netip.ParseAddr(bind)
	return err == nil && addr.IsUnspecified()
}

func hooksURL(host string, port int) string {
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/hooks"
}

// machineInterfaces are this machine's network interfaces.
func machineInterfaces() ([]netInterface, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	list := make([]netInterface, 0, len(ifaces))
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		found := netInterface{name: iface.Name, up: iface.Flags&net.FlagUp != 0, loopback: iface.Flags&net.FlagLoopback != 0}
		for _, a := range addrs {
			var ip net.IP
			switch a := a.(type) {
			case *net.IPNet:
				ip = a.IP
			case *net.IPAddr:
				ip = a.IP
			}
			if addr, ok := netip.AddrFromSlice(ip); ok {
				found.addrs = append(found.addrs, addr)
			}
		}
		list = append(list, found)
	}
	return list, nil
}

// JoinURLs are the webhook URLs machines join the hub by, the likeliest
// first; none when it finds no address other machines may reach.
func (h *Hub) JoinURLs() []JoinURL {
	var interfaces []netInterface
	if allInterfaces(h.config.Bind) {
		var err error
		if interfaces, err = h.interfaces(); err != nil {
			h.logger.Warn("Failed to list the machine's network interfaces", "error", err)
		}
	}
	return joinURLs(h.config.Bind, h.config.Port, interfaces)
}

// joinInfo is GET /api/join: the URLs machines join the hub by, and the
// secret they sign their events with, which only a hub with a token
// shows: one without would show it to everyone who reaches its page.
type joinInfo struct {
	URLs        []JoinURL `json:"urls"`
	Secret      string    `json:"secret,omitempty"`
	SecretShown bool      `json:"secret_shown"`
}

func (h *Hub) getJoin(w http.ResponseWriter, _ *http.Request) {
	info := joinInfo{URLs: h.JoinURLs()}
	if !h.config.NoToken {
		info.Secret, info.SecretShown = h.config.Secret, true
	}
	// It may hold the secret: no cache keeps it.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, info)
}
