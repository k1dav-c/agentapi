package codex

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/native"
)

// bootWith starts the adapter on a new thread whose thread/start result
// adds fields (JSON members) to the thread and model.
func (h *harness) bootWith(fields string) {
	h.t.Helper()
	h.start()
	init := h.expectSent("initialize")
	h.feedJSON(`{"id":` + string(init.ID) + `,"result":{"userAgent":"agentapi/0.160.0"}}`)
	h.expectSent("initialized")
	h.answerAccount()
	start := h.expectSent("thread/start")
	h.feedJSON(`{"id":` + string(start.ID) + `,"result":{"thread":{"id":"` + testThread + `","cwd":"/work","cliVersion":"0.160.0"},"model":"gpt-6-luna","cwd":"/work",` + fields + `}}`)
	h.settle()
	require.NoError(h.t, h.err("start"))
}

const testModels = `{"data":[
	{"id":"gpt-6-luna","model":"gpt-6-luna","displayName":"GPT-6-Luna","description":"Fast","hidden":false,"defaultReasoningEffort":"low",
	 "supportedReasoningEfforts":[{"reasoningEffort":"low","description":"l"},{"reasoningEffort":"medium","description":"m"}]},
	{"id":"gpt-6-astra","model":"gpt-6-astra","displayName":"GPT-6-Astra","description":"Frontier","hidden":false,"defaultReasoningEffort":"medium",
	 "supportedReasoningEfforts":[{"reasoningEffort":"medium","description":"m"},{"reasoningEffort":"high","description":"h"},{"reasoningEffort":"max","description":"x"}]},
	{"id":"codex-internal","model":"codex-internal","displayName":"Internal","description":"","hidden":true,"defaultReasoningEffort":"low","supportedReasoningEfforts":[]}
],"nextCursor":null}`

// options asks for the setting options, answering model/list if the
// adapter asks.
func (h *harness) options(answer bool) native.SettingOptions {
	h.t.Helper()
	var options native.SettingOptions
	h.act("options", func(ctx context.Context) (err error) {
		options, err = h.a.SettingOptions(ctx)
		return err
	})
	if answer {
		list := h.expectSent("model/list")
		h.feedJSON(`{"id":` + string(list.ID) + `,"result":` + testModels + `}`)
	}
	h.settle()
	require.NoError(h.t, h.err("options"))
	return options
}

func TestPolicyMode(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		p    policies
		want native.PermissionMode
	}{
		{policies{"never", "dangerFullAccess", "user"}, native.PermissionModeBypass},
		{policies{"on-request", "workspaceWrite", "auto_review"}, native.PermissionModeAuto},
		{policies{"on-request", "workspaceWrite", "guardian_subagent"}, native.PermissionModeAuto},
		{policies{"untrusted", "workspaceWrite", "user"}, native.PermissionModeAsk},
		{policies{"untrusted", "dangerFullAccess", "user"}, native.PermissionModeAsk},
		// Codex's own presets that AgentAPI has no mode for.
		{policies{"on-request", "workspaceWrite", "user"}, ""},
		{policies{"never", "readOnly", "user"}, ""},
		{policies{"", "", ""}, ""},
	} {
		assert.Equal(t, tc.want, policyMode(tc.p), tc.p)
	}
	for _, mode := range []native.PermissionMode{native.PermissionModeBypass, native.PermissionModeAuto, native.PermissionModeAsk} {
		p, ok := modePolicies(mode)
		require.True(t, ok, mode)
		assert.Equal(t, mode, policyMode(p), "the policies of %s amount to it", mode)
	}
	_, ok := modePolicies(native.PermissionModePlan)
	assert.False(t, ok)
}

func TestSettings(t *testing.T) {
	h := newHarness(t, "", nil)
	h.bootWith(`"approvalPolicy":"never","approvalsReviewer":"user","sandbox":{"type":"dangerFullAccess"},"reasoningEffort":null`)
	assert.True(t, h.a.Capabilities().Settings)
	assert.Equal(t, native.Settings{PermissionMode: native.PermissionModeBypass, Model: "gpt-6-luna"}, h.a.Settings())

	options := h.options(true)
	assert.Equal(t, []native.PermissionMode{native.PermissionModeBypass, native.PermissionModeAuto, native.PermissionModeAsk}, options.PermissionModes)
	require.Len(t, options.Models, 2, "hidden models are left out")
	assert.Equal(t, native.ModelOption{ID: "gpt-6-astra", Name: "GPT-6-Astra", Description: "Frontier", Efforts: []string{"medium", "high", "max"}, DefaultEffort: "medium"}, options.Models[1])
	// The thread runs on the model's default effort.
	assert.Equal(t, "low", h.a.Settings().Effort)
	h.options(false)

	// A change shows at once and goes with the next turn.
	require.NoError(t, h.a.ApplySettings(context.Background(), native.Settings{PermissionMode: native.PermissionModeAsk, Model: "gpt-6-astra", Effort: "high"}))
	assert.Equal(t, native.Settings{PermissionMode: native.PermissionModeAsk, Model: "gpt-6-astra", Effort: "high"}, h.a.Settings())
	h.expectNothingSent()
	h.act("turn", func(ctx context.Context) error { return h.a.StartTurn(ctx, native.Input{Text: "go"}) })
	sent := h.expectSent("turn/start")
	var params map[string]any
	require.NoError(t, json.Unmarshal(sent.Params, &params))
	assert.Equal(t, "untrusted", params["approvalPolicy"])
	assert.Equal(t, map[string]any{"type": "workspaceWrite"}, params["sandboxPolicy"])
	assert.Equal(t, "user", params["approvalsReviewer"])
	assert.Equal(t, "gpt-6-astra", params["model"])
	assert.Equal(t, "high", params["effort"])
	h.feedJSON(`{"id":` + string(sent.ID) + `,"result":{"turn":{"id":"turn-1","items":[],"status":"inProgress"}}}`)
	h.settle()
	require.NoError(t, h.err("turn"))
	h.completeTurn("turn-1", "completed", "")

	// Settings already sent aren't sent again; a failed turn/start keeps
	// them for the next one.
	require.NoError(t, h.a.ApplySettings(context.Background(), native.Settings{PermissionMode: native.PermissionModeAuto}))
	h.act("refused", func(ctx context.Context) error { return h.a.StartTurn(ctx, native.Input{Text: "go"}) })
	sent = h.expectSent("turn/start")
	h.feedJSON(`{"id":` + string(sent.ID) + `,"error":{"code":-32603,"message":"boom"}}`)
	h.settle()
	require.Error(t, h.err("refused"))
	h.act("turn", func(ctx context.Context) error { return h.a.StartTurn(ctx, native.Input{Text: "go"}) })
	sent = h.expectSent("turn/start")
	params = nil
	require.NoError(t, json.Unmarshal(sent.Params, &params))
	assert.Equal(t, "on-request", params["approvalPolicy"])
	assert.Equal(t, "auto_review", params["approvalsReviewer"])
	assert.NotContains(t, params, "model")
	assert.NotContains(t, params, "effort")
	h.feedJSON(`{"id":` + string(sent.ID) + `,"result":{"turn":{"id":"turn-2","items":[],"status":"inProgress"}}}`)
	h.settle()

	// A model without the current effort gets its default one.
	require.NoError(t, h.a.ApplySettings(context.Background(), native.Settings{Model: "gpt-6-luna"}))
	assert.Equal(t, "low", h.a.Settings().Effort)
	assert.ErrorIs(t, h.a.ApplySettings(context.Background(), native.Settings{Effort: "max"}), native.ErrInvalidSetting)
	assert.ErrorIs(t, h.a.ApplySettings(context.Background(), native.Settings{PermissionMode: native.PermissionModePlan}), native.ErrInvalidSetting)
}

func TestSettingsOfTheUsersPolicies(t *testing.T) {
	h := newHarness(t, "", []string{"-a", "on-request", "-s", "read-only"})
	h.bootWith(`"approvalPolicy":"on-request","approvalsReviewer":"user","sandbox":{"type":"readOnly"},"reasoningEffort":"medium"`)
	// A combination of the user's own is no mode of AgentAPI's, and isn't
	// changed until the user switches.
	assert.Equal(t, native.Settings{Model: "gpt-6-luna", Effort: "medium"}, h.a.Settings())
	h.act("turn", func(ctx context.Context) error { return h.a.StartTurn(ctx, native.Input{Text: "go"}) })
	sent := h.expectSent("turn/start")
	var params map[string]any
	require.NoError(t, json.Unmarshal(sent.Params, &params))
	assert.NotContains(t, params, "approvalPolicy")
	assert.NotContains(t, params, "sandboxPolicy")
}
