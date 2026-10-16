package pi

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/native"
)

// noCredentialsState is get_state of Pi 1.0.2 with an empty
// PI_CODING_AGENT_DIR and no provider keys.
const noCredentialsState = `{"model":{"id":"unknown","name":"unknown","api":"unknown","provider":"unknown","baseUrl":"","reasoning":false,"input":[],"contextWindow":0,"maxTokens":0},"thinkingLevel":"off","isStreaming":false,"isCompacting":false,"steeringMode":"one-at-a-time","followUpMode":"one-at-a-time","sessionId":"01a1440e-b3a3-7317-b135-afa7fda22fac","autoCompactionEnabled":true,"messageCount":0,"pendingMessageCount":0}`

// answerModels answers get_available_models.
func (h *harness) answerModels(reply map[string]any) {
	h.t.Helper()
	cmd := h.pi.nextCmd()
	require.Equal(h.t, "get_available_models", cmd["type"])
	reply["id"] = cmd["id"]
	reply["type"] = "response"
	reply["command"] = "get_available_models"
	h.writeLine(reply)
}

func (h *harness) startResult() error {
	h.t.Helper()
	select {
	case err := <-h.startErr:
		return err
	case <-time.After(5 * time.Second):
		h.t.Fatal("Start didn't return")
		return nil
	}
}

func TestLoginCheckAtStart(t *testing.T) {
	t.Parallel()

	t.Run("no model and no models", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, Config{}, "")
		h.answerState(noCredentialsState)
		h.answerModels(map[string]any{"success": true, "data": map[string]any{"models": []any{}}})
		var login *native.LoginRequiredError
		require.ErrorAs(t, h.startResult(), &login)
		assert.Equal(t, loginHint, login.Login)
		assert.Equal(t, "pi", login.Login.Command)
		h.sink.mu.Lock()
		defer h.sink.mu.Unlock()
		assert.Empty(t, h.sink.refs, "a session that waits for a login isn't reported")
		assert.Empty(t, h.sink.events)
	})

	for name, reply := range map[string]map[string]any{
		"models available":  {"success": true, "data": map[string]any{"models": []any{map[string]any{"id": "claude-opus-4-8", "provider": "anthropic"}}}},
		"command unknown":   {"success": false, "error": "Unknown command: get_available_models"},
		"unexpected answer": {"success": true, "data": map[string]any{}},
	} {
		t.Run(name+" starts", func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, Config{}, "")
			h.answerState(noCredentialsState)
			h.answerModels(reply)
			require.NoError(t, h.startResult())
			h.settle()
			require.Eventually(t, func() bool {
				h.sink.mu.Lock()
				defer h.sink.mu.Unlock()
				return slices.Equal(h.sink.refs, []string{"01a1440e-b3a3-7317-b135-afa7fda22fac"})
			}, 5*time.Second, time.Millisecond, "the session is reported once Pi may start")
		})
	}

	t.Run("a model skips the check", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, Config{}, "")
		h.answerState(noState)
		require.NoError(t, h.startResult())
		assert.Equal(t, []string{"cmd:get_state"}, h.pi.events())
	})
}

func TestLoginLostMidSession(t *testing.T) {
	t.Parallel()

	t.Run("prompt refused", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, Config{}, "")
		h.answerState(`{"sessionId":"s","model":{"id":"claude-sonnet-4-5","provider":"anthropic","input":["text"]}}`)
		h.started()
		h.call("p", func() error { return h.a.StartTurn(h.ctx, native.Input{Text: "hi"}) })
		prompt := h.pi.nextCmd()
		// What Pi 1.0.2 answers for --model anthropic/… without a key.
		h.writeLine(map[string]any{
			"id": prompt["id"], "type": "response", "command": "prompt", "success": false,
			"error": "No API key found for anthropic.\n\nUse /login to log into a provider via OAuth or API key. See:\n  docs/providers.md\n  docs/models.md",
		})
		err := h.result("p")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "No API key found")
		h.sink.mu.Lock()
		defer h.sink.mu.Unlock()
		assert.Equal(t, []native.LoginRequired{loginHint}, h.sink.logins)
		assert.Zero(t, h.sink.started, "no turn started")
	})

	t.Run("provider rejects the key", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t, Config{}, "")
		h.answerState(noState)
		h.started()
		h.call("p", func() error { return h.a.StartTurn(h.ctx, native.Input{Text: "hi"}) })
		prompt := h.pi.nextCmd()
		failed := map[string]any{
			"role": "assistant", "content": []any{}, "provider": "anthropic", "model": "claude-opus-4-8", "stopReason": "error", "timestamp": 1,
			"errorMessage": `401 {"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`,
		}
		h.writeLine(map[string]any{"id": prompt["id"], "type": "response", "command": "prompt", "success": true, "data": map[string]any{"disposition": "started"}})
		h.writeLine(map[string]any{"type": "agent_start"})
		h.writeLine(map[string]any{"type": "message_start", "message": failed})
		h.writeLine(map[string]any{"type": "message_end", "message": failed})
		h.writeLine(map[string]any{"type": "agent_end", "messages": []any{}, "willRetry": false})
		h.writeLine(map[string]any{"type": "agent_settled"})
		require.NoError(t, h.result("p"))
		h.settle()
		h.sink.mu.Lock()
		defer h.sink.mu.Unlock()
		assert.Equal(t, []native.LoginRequired{loginHint}, h.sink.logins)
		require.Len(t, h.sink.results, 1)
		assert.Equal(t, "error", h.sink.results[0].StopReason)
		assert.Less(t, slices.Index(h.sink.log, "login_required"), slices.Index(h.sink.log, "turn_completed"))
	})
}

func TestIsAuthError(t *testing.T) {
	t.Parallel()
	for text, want := range map[string]bool{
		"No API key found for the selected model.\n\nUse /login to log into a provider via OAuth or API key.":    true,
		"No model selected.\n\nUse /login to log into a provider via OAuth or API key.":                          true,
		`401 {"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`:             true,
		"401 Incorrect API key provided: sk-dummy.":                                                              true,
		"Failed to refresh OAuth token for openai-codex":                                                         true,
		"Codex error: The 'gpt-5.3-codex-spark' model is not supported when using Codex with a ChatGPT account.": false,
		"429 rate limit exceeded":     false,
		"overloaded_error (HTTP 529)": false,
	} {
		assert.Equal(t, want, isAuthError(text), text)
	}
	assert.True(t, stateHasModel(json.RawMessage(noState)))
	assert.False(t, stateHasModel(json.RawMessage(noCredentialsState)))
	assert.False(t, stateHasModel(json.RawMessage(`{"sessionId":"s"}`)))
}
