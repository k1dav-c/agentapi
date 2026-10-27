package attach

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/httpapi"
)

// statusServer answers GET /status with transport, for requests that carry
// token (any request when token is empty).
func statusServer(t *testing.T, token string, transport httpapi.Transport) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token != "" && r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		body := httpapi.StatusResponse{}
		body.Body.Transport = transport
		_ = json.NewEncoder(w).Encode(body.Body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestClientSendsTheToken(t *testing.T) {
	t.Parallel()
	srv := statusServer(t, "secret", httpapi.TransportPTY)

	transport, err := (&client{url: srv.URL, token: "secret"}).transport(t.Context())
	require.NoError(t, err)
	assert.Equal(t, httpapi.TransportPTY, transport)

	_, err = (&client{url: srv.URL}).transport(t.Context())
	assert.ErrorIs(t, err, errTokenNeeded)
	_, err = (&client{url: srv.URL, token: "wrong"}).transport(t.Context())
	assert.ErrorIs(t, err, errTokenRefused)
}

func TestOnlyATerminalCanBeAttachedTo(t *testing.T) {
	t.Parallel()
	assert.NoError(t, errNoTerminal(httpapi.TransportPTY))
	assert.ErrorContains(t, errNoTerminal("acp"), "acp transport")
	assert.ErrorContains(t, errNoTerminal(httpapi.TransportNative), "native transport")

	// runAttach refuses before it takes over the terminal.
	err := runAttach(&client{url: statusServer(t, "", httpapi.TransportNative).URL})
	assert.ErrorContains(t, err, "native transport")
	err = runAttach(&client{url: statusServer(t, "secret", httpapi.TransportPTY).URL})
	assert.ErrorIs(t, err, errTokenNeeded)
}
