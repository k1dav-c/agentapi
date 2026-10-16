package codex

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/native"
)

// handshakeAccount starts the adapter and answers account/read with
// result (or error).
func (h *harness) handshakeAccount(reply string) {
	h.t.Helper()
	h.start()
	init := h.expectSent("initialize")
	h.feedJSON(`{"id":` + string(init.ID) + `,"result":{"userAgent":"agentapi/0.160.0"}}`)
	h.expectSent("initialized")
	read := h.expectSent("account/read")
	h.feedJSON(`{"id":` + string(read.ID) + `,` + reply + `}`)
}

func TestLoginCheckAtStart(t *testing.T) {
	t.Run("not logged in", func(t *testing.T) {
		h := newHarness(t, "", nil)
		// What codex-cli 0.160.0 answers with an empty CODEX_HOME, with or
		// without OPENAI_API_KEY in its environment.
		h.handshakeAccount(`"result":{"account":null,"requiresOpenaiAuth":true,"workspaceRouting":null}`)
		h.settle()
		var login *native.LoginRequiredError
		require.ErrorAs(t, h.err("start"), &login)
		assert.Equal(t, loginHint, login.Login)
		assert.Equal(t, "codex login", login.Login.Command)
		h.expectNothingSent()
	})

	for name, reply := range map[string]string{
		"API key":                      `"result":{"account":{"type":"apiKey"},"requiresOpenaiAuth":true}`,
		"ChatGPT":                      `"result":{"account":{"type":"chatgpt","email":null,"planType":"plus"},"requiresOpenaiAuth":true}`,
		"provider without OpenAI":      `"result":{"account":null,"requiresOpenaiAuth":false}`,
		"older app-server":             `"error":{"code":-32601,"message":"method not found: account/read"}`,
		"unexpected result is ignored": `"result":"yes"`,
	} {
		t.Run(name+" starts the thread", func(t *testing.T) {
			h := newHarness(t, "", nil)
			h.handshakeAccount(reply)
			start := h.expectSent("thread/start")
			h.feedJSON(`{"id":` + string(start.ID) + `,"result":{"thread":{"id":"` + testThread + `","cwd":"/work","cliVersion":"0.160.0"},"model":"gpt-6-luna","cwd":"/work"}}`)
			h.settle()
			require.NoError(t, h.err("start"))
		})
	}
}

func TestLoginLostMidTurn(t *testing.T) {
	h := newHarness(t, "", nil)
	h.boot()
	h.beginTurn("turn-1")
	// What codex-cli 0.160.0 sends for a turn without a login: retried
	// errors, then the final one and the failed turn.
	retry := `{"method":"error","params":{"error":{"message":"Reconnecting... 2/5","codexErrorInfo":{"responseStreamDisconnected":{"httpStatusCode":401}},"additionalDetails":"unexpected status 401 Unauthorized: Missing bearer or basic authentication in header, url: wss://api.openai.com/v1/responses"},"willRetry":true,"threadId":"` + testThread + `","turnId":"turn-1"}}`
	final := `{"message":"unexpected status 401 Unauthorized: Missing bearer or basic authentication in header, url: https://api.openai.com/v1/responses","codexErrorInfo":{"httpConnectionFailed":{"httpStatusCode":401}},"additionalDetails":null}`
	h.feedJSON(retry)
	h.sync()
	assert.Empty(t, h.rec.snapshot().logins, "a retried error is not reported yet")
	h.feedJSON(`{"method":"error","params":{"error":` + final + `,"willRetry":false,"threadId":"` + testThread + `","turnId":"turn-1"}}`)
	h.completeTurn("turn-1", "failed", `,"error":`+final)
	s := h.rec.snapshot()
	assert.Equal(t, []native.LoginRequired{loginHint}, s.logins, "reported once")
	require.Len(t, s.completions, 1)
	assert.Contains(t, s.completions[0].Error, "401 Unauthorized")
	assert.Less(t, slices.Index(s.log, "login_required"), slices.Index(s.log, "turn_completed:error"))

	// A turn that fails for another reason is not a login problem.
	h.beginTurn("turn-2")
	h.completeTurn("turn-2", "failed", `,"error":{"message":"stream disconnected before completion","codexErrorInfo":{"responseStreamDisconnected":{"httpStatusCode":500}}}`)
	assert.Len(t, h.rec.snapshot().logins, 1)
	h.finish()
}

func TestIsAuthError(t *testing.T) {
	text := func(s string) *string { return &s }
	for _, tc := range []struct {
		name string
		err  turnError
		want bool
	}{
		{"unauthorized code", turnError{Message: "x", CodexErrorInfo: json.RawMessage(`"unauthorized"`)}, true},
		{"401 on a stream", turnError{Message: "Reconnecting", CodexErrorInfo: json.RawMessage(`{"responseStreamConnectionFailed":{"httpStatusCode":401}}`)}, true},
		{"expired refresh token", turnError{Message: "Your access token could not be refreshed because your refresh token has expired. Please log out and sign in again."}, true},
		{"401 in the details", turnError{Message: "request failed", AdditionalDetails: text("unexpected status 401 Unauthorized")}, true},
		{"usage limit", turnError{Message: "You've hit your usage limit.", CodexErrorInfo: json.RawMessage(`"usageLimitExceeded"`)}, false},
		{"server error", turnError{Message: "unexpected status 500", CodexErrorInfo: json.RawMessage(`{"httpConnectionFailed":{"httpStatusCode":500}}`)}, false},
		{"no status", turnError{Message: "stream closed", CodexErrorInfo: json.RawMessage(`{"httpConnectionFailed":{"httpStatusCode":null}}`)}, false},
		{"a number that contains 401", turnError{Message: "context window is 14010 tokens"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isAuthError(&tc.err))
		})
	}
	assert.False(t, isAuthError(nil))
}
