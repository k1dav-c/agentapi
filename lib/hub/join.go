package hub

import (
	"net"
	"net/http"
	"net/netip"
	"strconv"

	"github.com/coder/agentapi/lib/netaddr"
)

// JoinURL is a URL machines join the hub by (agentapi hub join <URL>),
// with the network interface of its address; none for the --bind address.
type JoinURL struct {
	URL       string `json:"url"`
	Interface string `json:"interface,omitempty"`
}

// joinURLs are the webhook URLs of a hub that listens on bind and port:
// bind's, when it listens on one address; otherwise one for each address
// netaddr.Reachable finds, private networks (RFC 1918) first, then CGNAT,
// then the rest.
func joinURLs(bind string, port int, interfaces []netaddr.Interface) []JoinURL {
	if !allInterfaces(bind) {
		return []JoinURL{{URL: hooksURL(bind, port)}}
	}
	found := netaddr.Reachable(interfaces)
	urls := make([]JoinURL, len(found))
	for i, f := range found {
		urls[i] = JoinURL{URL: hooksURL(f.Addr.String(), port), Interface: f.Interface}
	}
	return urls
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

// JoinURLs are the webhook URLs machines join the hub by, the likeliest
// first; none when it finds no address other machines may reach.
func (h *Hub) JoinURLs() []JoinURL {
	var interfaces []netaddr.Interface
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
