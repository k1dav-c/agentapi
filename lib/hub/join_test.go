package hub

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/netaddr"
)

func addrs(list ...string) []netip.Addr {
	parsed := make([]netip.Addr, len(list))
	for i, a := range list {
		parsed[i] = netip.MustParseAddr(a)
	}
	return parsed
}

// A machine's interfaces, as a workstation with Docker and Tailscale has
// them.
var workstation = []netaddr.Interface{
	{Name: "lo", Up: true, Loopback: true, Addrs: addrs("127.0.0.1", "::1")},
	{Name: "eth0", Up: true, Addrs: addrs("192.168.9.210", "fe80::1")},
	{Name: "tailscale0", Up: true, Addrs: addrs("100.116.90.98", "fd7a:115c:a1e0::1")},
	{Name: "docker0", Up: false, Addrs: addrs("172.17.0.1")},
	{Name: "br-8d0a10b426d8", Up: true, Addrs: addrs("172.18.0.1")},
	{Name: "veth1a2b3c", Up: true, Addrs: addrs("169.254.10.1")},
	{Name: "virbr0", Up: true, Addrs: addrs("192.168.122.1")},
	{Name: "ens5", Up: true, Addrs: addrs("203.0.113.7")},
	{Name: "wlan0", Up: true, Addrs: addrs("::ffff:10.0.0.5", "169.254.3.3")},
	{Name: "wlan1", Up: false, Addrs: addrs("10.0.0.6")},
	{Name: "eth1", Up: true, Addrs: addrs("192.168.9.210")},
}

// The join URLs are the --bind address's, or the machine's IPv4
// addresses that others may reach: no loopback, link-local, container or
// bridge address, nor one of an interface that is down; private networks
// first, then CGNAT, then the rest, each in the interfaces' order.
func TestJoinURLs(t *testing.T) {
	assert.Equal(t, []JoinURL{
		{URL: "http://192.168.9.210:3290/hooks", Interface: "eth0"},
		{URL: "http://10.0.0.5:3290/hooks", Interface: "wlan0"},
		{URL: "http://100.116.90.98:3290/hooks", Interface: "tailscale0"},
		{URL: "http://203.0.113.7:3290/hooks", Interface: "ens5"},
	}, joinURLs("", 3290, workstation))
	assert.Equal(t, joinURLs("", 3290, workstation), joinURLs("0.0.0.0", 3290, workstation), "every interface")
	assert.Equal(t, joinURLs("", 3290, workstation), joinURLs("::", 3290, workstation), "every interface")

	assert.Equal(t, []JoinURL{{URL: "http://100.64.0.7:3290/hooks"}}, joinURLs("100.64.0.7", 3290, workstation))
	assert.Equal(t, []JoinURL{{URL: "http://hub.vpn:3291/hooks"}}, joinURLs("hub.vpn", 3291, nil))
	assert.Equal(t, []JoinURL{{URL: "http://[fd00::1]:3290/hooks"}}, joinURLs("fd00::1", 3290, nil))
	assert.Equal(t, []JoinURL{{URL: "http://127.0.0.1:3290/hooks"}}, joinURLs("127.0.0.1", 3290, nil), "only this machine reaches it")

	none := joinURLs("", 3290, workstation[:1])
	assert.Empty(t, none)
	encoded, err := json.Marshal(none)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(encoded), "a list, also when empty")
}

// GET /api/join says where machines join the hub. It needs a sign-in, and
// has the secret only on a hub with a token: a hub without would give it
// to everyone who reaches its page.
func TestJoinAPI(t *testing.T) {
	get := func(t *testing.T, url, bearer string) (int, joinInfo, string, http.Header) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, url, nil)
		require.NoError(t, err)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		var info joinInfo
		if resp.StatusCode == http.StatusOK {
			require.NoError(t, json.Unmarshal(body, &info))
		}
		return resp.StatusCode, info, string(body), resp.Header
	}

	t.Run("a token", func(t *testing.T) {
		h, err := New(Config{Token: "t", Secret: "join-secret", Bind: "192.0.2.10", Port: 3290})
		require.NoError(t, err)
		srv := httptest.NewServer(h.Handler())
		defer srv.Close()

		code, _, body, _ := get(t, srv.URL+"/api/join", "")
		assert.Equal(t, http.StatusUnauthorized, code)
		assert.NotContains(t, body, "join-secret")

		code, info, _, header := get(t, srv.URL+"/api/join", "t")
		require.Equal(t, http.StatusOK, code)
		assert.Equal(t, []JoinURL{{URL: "http://192.0.2.10:3290/hooks"}}, info.URLs, "the --bind address")
		assert.Equal(t, "join-secret", info.Secret)
		assert.True(t, info.SecretShown)
		assert.Equal(t, "no-store", header.Get("Cache-Control"))

		// The instances and their stream never carry it.
		code, _, body, _ = get(t, srv.URL+"/api/instances", "t")
		require.Equal(t, http.StatusOK, code)
		assert.NotContains(t, body, "join-secret")
	})

	t.Run("no token", func(t *testing.T) {
		h, err := New(Config{NoToken: true, Secret: "join-secret", Port: 3290})
		require.NoError(t, err)
		h.interfaces = func() ([]netaddr.Interface, error) { return workstation, nil }
		srv := httptest.NewServer(h.Handler())
		defer srv.Close()

		code, info, body, _ := get(t, srv.URL+"/api/join", "")
		require.Equal(t, http.StatusOK, code)
		assert.Equal(t, joinURLs("", 3290, workstation), info.URLs, "the machine's addresses")
		assert.False(t, info.SecretShown)
		assert.Empty(t, info.Secret)
		assert.NotContains(t, body, "join-secret")
	})
}
