package codex

import (
	"context"
	"encoding/json"
	"testing"
	"time"

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
	assert.Equal(t, native.PermissionModeValues, options.PermissionModes)
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

// planTurn runs a turn in the plan mode that ends with a plan, and returns
// the plan request it opened.
func (h *harness) planTurn(turnID string) native.Request {
	h.t.Helper()
	h.beginTurn(turnID)
	h.item("started", turnID, `{"type":"plan","id":"`+turnID+`-plan","text":""}`)
	h.item("completed", turnID, `{"type":"plan","id":"`+turnID+`-plan","text":"## Add hello.txt\n\nCreate it with hi."}`)
	h.completeTurn(turnID, "completed", "")
	s := h.rec.snapshot()
	require.NotEmpty(h.t, s.opened)
	return s.opened[len(s.opened)-1]
}

func TestPlanMode(t *testing.T) {
	h := newHarness(t, "", nil)
	h.bootWith(`"approvalPolicy":"never","approvalsReviewer":"user","sandbox":{"type":"dangerFullAccess"},"reasoningEffort":"low"`)
	h.options(true)
	require.NoError(t, h.a.ApplySettings(context.Background(), native.Settings{PermissionMode: native.PermissionModePlan}))
	assert.Equal(t, native.PermissionModePlan, h.a.Settings().PermissionMode)

	// The turn switches to the plan collaboration mode, on the thread's
	// model and effort; the policies stay.
	h.act("turn", func(ctx context.Context) error { return h.a.StartTurn(ctx, native.Input{Text: "add hello.txt"}) })
	sent := h.expectSent("turn/start")
	var params map[string]any
	require.NoError(t, json.Unmarshal(sent.Params, &params))
	assert.Equal(t, map[string]any{"mode": "plan", "settings": map[string]any{"model": "gpt-6-luna", "reasoning_effort": "low", "developer_instructions": nil}}, params["collaborationMode"])
	assert.NotContains(t, params, "approvalPolicy")
	h.feedJSON(`{"id":` + string(sent.ID) + `,"result":{"turn":{"id":"turn-1","items":[],"status":"inProgress"}}}`)
	h.settle()

	// The turn ends with a plan: it waits for the user instead of ending.
	h.item("completed", "turn-1", `{"type":"plan","id":"turn-1-plan","text":"## Add hello.txt\n\nCreate it with hi."}`)
	h.completeTurn("turn-1", "completed", "")
	s := h.rec.snapshot()
	assert.Empty(t, s.completions, "the turn goes on")
	require.Len(t, s.opened, 1)
	plan := s.opened[0]
	assert.Equal(t, native.RequestKindPlan, plan.Kind)
	assert.Contains(t, plan.Detail, "Create it with hi.")
	assert.Equal(t, []string{optionImplement, optionKeepPlanning}, []string{plan.Options[0].ID, plan.Options[1].ID})

	// Approved: Codex implements it in the default mode, and the mode is
	// the one before the plan again.
	h.act("respond", func(ctx context.Context) error {
		return h.a.Respond(ctx, plan.ID, native.Response{OptionID: optionImplement})
	})
	sent = h.expectSent("turn/start")
	params = nil
	require.NoError(t, json.Unmarshal(sent.Params, &params))
	assert.Equal(t, "default", params["collaborationMode"].(map[string]any)["mode"])
	assert.Equal(t, implementPrompt, params["input"].([]any)[0].(map[string]any)["text"])
	h.feedJSON(`{"id":` + string(sent.ID) + `,"result":{"turn":{"id":"turn-2","items":[],"status":"inProgress"}}}`)
	h.settle()
	require.NoError(t, h.err("respond"))
	h.eventually(func() bool { return len(h.rec.snapshot().settings) > 0 }, "the settings to be reported")
	s = h.rec.snapshot()
	assert.Equal(t, native.PermissionModeBypass, s.settings[len(s.settings)-1].PermissionMode)
	assert.Equal(t, native.PermissionModeBypass, h.a.Settings().PermissionMode)
	h.completeTurn("turn-2", "completed", "")
	assert.Len(t, h.rec.snapshot().completions, 1, "the implementation ends the turn")
}

func TestPlanFeedbackAndInterrupt(t *testing.T) {
	h := newHarness(t, "", nil)
	h.bootWith(`"approvalPolicy":"never","approvalsReviewer":"user","sandbox":{"type":"dangerFullAccess"}`)
	h.options(true)
	require.NoError(t, h.a.ApplySettings(context.Background(), native.Settings{PermissionMode: native.PermissionModePlan}))
	plan := h.planTurn("turn-1")

	// Feedback goes back to Codex, still in the plan mode.
	h.act("revise", func(ctx context.Context) error {
		return h.a.Respond(ctx, plan.ID, native.Response{OptionID: optionKeepPlanning, Text: "Use hello.md instead"})
	})
	sent := h.expectSent("turn/start")
	var params map[string]any
	require.NoError(t, json.Unmarshal(sent.Params, &params))
	assert.Equal(t, "Use hello.md instead", params["input"].([]any)[0].(map[string]any)["text"])
	assert.NotContains(t, params, "collaborationMode", "still planning")
	h.feedJSON(`{"id":` + string(sent.ID) + `,"result":{"turn":{"id":"turn-2","items":[],"status":"inProgress"}}}`)
	h.settle()
	require.NoError(t, h.err("revise"))
	assert.ErrorIs(t, h.a.Respond(context.Background(), plan.ID, native.Response{OptionID: optionImplement}), native.ErrRequestNotFound, "answered once")
	h.item("completed", "turn-2", `{"type":"plan","id":"turn-2-plan","text":"## Add hello.md"}`)
	h.completeTurn("turn-2", "completed", "")
	second := h.rec.snapshot().opened[1]

	// Interrupted while it waits: the request closes and the turn ends.
	require.NoError(t, h.a.Interrupt(context.Background()))
	h.eventually(func() bool { return len(h.rec.snapshot().completions) == 1 }, "the interrupted turn to end")
	s := h.rec.snapshot()
	assert.Contains(t, s.closed, second.ID)
	require.Len(t, s.completions, 1)
	assert.Equal(t, native.StopReasonInterrupted, s.completions[0].StopReason)
	assert.Equal(t, native.PermissionModePlan, h.a.Settings().PermissionMode)

	// No feedback: the turn ends, still in the plan mode.
	third := h.planTurn("turn-3")
	require.NoError(t, h.a.Respond(context.Background(), third.ID, native.Response{OptionID: optionKeepPlanning}))
	h.eventually(func() bool { return len(h.rec.snapshot().completions) == 2 }, "the turn to end")
	s = h.rec.snapshot()
	require.Len(t, s.completions, 2)
	assert.Equal(t, native.StopReasonEndTurn, s.completions[1].StopReason)
	assert.ErrorIs(t, h.a.Respond(context.Background(), third.ID, native.Response{OptionID: "maybe"}), native.ErrRequestNotFound)
}

func TestResumedThreadInPlanMode(t *testing.T) {
	h := newHarness(t, testThread, nil)
	h.start()
	init := h.expectSent("initialize")
	var params initializeParams
	require.NoError(t, json.Unmarshal(init.Params, &params))
	assert.True(t, params.Capabilities.ExperimentalAPI, "the plan mode is experimental")
	h.feedJSON(`{"id":` + string(init.ID) + `,"result":{"userAgent":"agentapi/0.160.0"}}`)
	h.expectSent("initialized")
	h.answerAccount()
	resume := h.expectSent("thread/resume")
	h.feedJSON(`{"id":` + string(resume.ID) + `,"result":{"thread":{"id":"` + testThread + `","cwd":"/work","cliVersion":"0.160.0"},"model":"gpt-6-luna","cwd":"/work",
		"approvalPolicy":"never","approvalsReviewer":"user","sandbox":{"type":"dangerFullAccess"},
		"collaborationMode":{"mode":"plan","settings":{"model":"gpt-6-luna","reasoning_effort":null,"developer_instructions":null}}}}`)
	h.settle()
	require.NoError(t, h.err("start"))
	assert.Equal(t, native.PermissionModePlan, h.a.Settings().PermissionMode)

	// Leaving it, and a new model, go with the next turn.
	h.options(true)
	require.NoError(t, h.a.ApplySettings(context.Background(), native.Settings{PermissionMode: native.PermissionModeAsk, Model: "gpt-6-astra"}))
	h.act("turn", func(ctx context.Context) error { return h.a.StartTurn(ctx, native.Input{Text: "go"}) })
	sent := h.expectSent("turn/start")
	var turn map[string]any
	require.NoError(t, json.Unmarshal(sent.Params, &turn))
	assert.Equal(t, "untrusted", turn["approvalPolicy"])
	assert.Equal(t, map[string]any{"mode": "default", "settings": map[string]any{"model": "gpt-6-astra", "reasoning_effort": "medium", "developer_instructions": nil}}, turn["collaborationMode"])
}

func TestUsage(t *testing.T) {
	h := newHarness(t, "", nil)
	h.boot()
	h.beginTurn("turn-1")
	// As Codex 0.160.0 sends them.
	h.feedJSON(`{"method":"thread/tokenUsage/updated","params":{"threadId":"` + testThread + `","turnId":"turn-1","tokenUsage":{
		"total":{"totalTokens":17721,"inputTokens":17716,"cachedInputTokens":0,"outputTokens":5,"reasoningOutputTokens":0},
		"last":{"totalTokens":17721,"inputTokens":17716,"cachedInputTokens":0,"outputTokens":5,"reasoningOutputTokens":0},
		"modelContextWindow":258400}}}`)
	h.feedJSON(`{"method":"account/rateLimits/updated","params":{"rateLimits":{"limitId":"codex","limitName":null,
		"primary":{"usedPercent":31,"windowDurationMins":10080,"resetsAt":1792651785},"secondary":{"usedPercent":4,"windowDurationMins":300,"resetsAt":1792260000},
		"credits":{"hasCredits":false,"unlimited":false,"balance":null},"planType":"team"}}}`)
	h.feedJSON(`{"method":"account/rateLimits/updated","params":{"rateLimits":{"limitId":"gpt-6-astra","limitName":"Astra",
		"primary":{"usedPercent":50,"windowDurationMins":300,"resetsAt":null},"secondary":null}}}`)
	h.sync()
	h.rec.mu.Lock()
	defer h.rec.mu.Unlock()
	require.Len(t, h.rec.usage, 3)
	usage := h.rec.usage[2]
	assert.Equal(t, &native.ContextUsage{Used: 17721, Size: 258400}, usage.Context)
	require.Len(t, usage.RateLimits, 3)
	assert.Equal(t, "gpt-6-astra-primary", usage.RateLimits[0].ID)
	assert.Equal(t, "Astra 5-hour limit", usage.RateLimits[0].Label)
	assert.Nil(t, usage.RateLimits[0].ResetsAt)
	assert.Equal(t, "Weekly limit", usage.RateLimits[1].Label)
	assert.InDelta(t, 31.0, *usage.RateLimits[1].UsedPercent, 0.001)
	assert.Equal(t, time.Unix(1792651785, 0).UTC(), *usage.RateLimits[1].ResetsAt)
	assert.Equal(t, "5-hour limit", usage.RateLimits[2].Label)
}
