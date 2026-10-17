package pi

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/native"
)

// lunaState is get_state of a session on GPT-6 Luna at medium thinking.
const lunaState = `{"model":{"id":"gpt-6-luna","name":"GPT-6 Luna","provider":"openai-codex","reasoning":true,"input":["text","image"],
	"thinkingLevelMap":{"off":"none","minimal":"low","low":"low","medium":"medium","high":"high","xhigh":"xhigh","max":"max"}},
	"thinkingLevel":"medium","isStreaming":false,"sessionId":"s1"}`

// piModels is get_available_models of Pi 1.0.2, shortened.
const piModels = `{"models":[
	{"id":"gpt-6-luna","name":"GPT-6 Luna","provider":"openai-codex","reasoning":true,"input":["text","image"],
	 "thinkingLevelMap":{"off":"none","minimal":"low","low":"low","medium":"medium","high":"high","xhigh":"xhigh","max":"max"}},
	{"id":"gpt-6.1-sol","name":"GPT-6.1 Sol","provider":"openai-codex","reasoning":true,"input":["text","image"],
	 "thinkingLevelMap":{"off":null,"minimal":"low","low":"low","medium":"medium","high":"high","xhigh":"xhigh","max":"max"}},
	{"id":"gpt-5.5","name":"GPT-5.5","provider":"openai-codex","reasoning":true,"input":["text"],"thinkingLevelMap":{"xhigh":"xhigh","minimal":"low"}},
	{"id":"llama","name":"Llama","provider":"local","reasoning":false,"input":["text"]}
]}`

func TestModelLevels(t *testing.T) {
	t.Parallel()
	off, low := (*string)(nil), "low"
	for _, tc := range []struct {
		m    piModel
		want []string
	}{
		{piModel{Reasoning: false}, nil},
		{piModel{Reasoning: true}, []string{"off", "minimal", "low", "medium", "high"}},
		{piModel{Reasoning: true, ThinkingLevelMap: map[string]*string{"off": off, "max": &low}}, []string{"minimal", "low", "medium", "high", "max"}},
	} {
		assert.Equal(t, tc.want, tc.m.levels())
	}
}

func TestSettings(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Config{}, "")
	h.answerState(lunaState)
	h.started()
	assert.True(t, h.a.Capabilities().Settings)
	assert.Equal(t, native.Settings{Model: "openai-codex/gpt-6-luna", Effort: "medium"}, h.a.Settings())

	var options native.SettingOptions
	h.call("options", func() (err error) {
		options, err = h.a.SettingOptions(context.Background())
		return err
	})
	cmd := h.pi.nextCmd()
	require.Equal(t, "get_available_models", cmd["type"])
	h.answer(cmd, piModels)
	require.NoError(t, h.result("options"))
	assert.Empty(t, options.PermissionModes, "Pi has no permission modes")
	require.Len(t, options.Models, 4)
	assert.Equal(t, native.ModelOption{ID: "openai-codex/gpt-6.1-sol", Name: "GPT-6.1 Sol", Description: "openai-codex", Efforts: []string{"minimal", "low", "medium", "high", "xhigh", "max"}}, options.Models[1])
	assert.Equal(t, []string{"off", "minimal", "low", "medium", "high", "xhigh"}, options.Models[2].Efforts)
	assert.Empty(t, options.Models[3].Efforts)

	// The model, then the level, then the state that says what Pi made of
	// them.
	h.call("apply", func() error {
		return h.a.ApplySettings(context.Background(), native.Settings{Model: "openai-codex/gpt-6.1-sol", Effort: "high"})
	})
	cmd = h.pi.nextCmd()
	require.Equal(t, "set_model", cmd["type"])
	assert.Equal(t, "openai-codex", cmd["provider"])
	assert.Equal(t, "gpt-6.1-sol", cmd["modelId"])
	h.answer(cmd, `{"id":"gpt-6.1-sol","provider":"openai-codex"}`)
	cmd = h.pi.nextCmd()
	require.Equal(t, "set_thinking_level", cmd["type"])
	assert.Equal(t, "high", cmd["level"])
	h.answer(cmd, "")
	cmd = h.pi.nextCmd()
	require.Equal(t, "get_state", cmd["type"])
	h.answer(cmd, `{"model":{"id":"gpt-6.1-sol","provider":"openai-codex","reasoning":true},"thinkingLevel":"high","sessionId":"s1"}`)
	require.NoError(t, h.result("apply"))
	assert.Equal(t, native.Settings{Model: "openai-codex/gpt-6.1-sol", Effort: "high"}, h.a.Settings())

	assert.ErrorIs(t, h.a.ApplySettings(context.Background(), native.Settings{PermissionMode: native.PermissionModeAsk}), native.ErrInvalidSetting)
	assert.ErrorIs(t, h.a.ApplySettings(context.Background(), native.Settings{Model: "openai-codex/llama"}), native.ErrInvalidSetting)

	// A level Pi changed by itself (an extension, or a model that clamped
	// it) is reported.
	h.write(`{"type":"thinking_level_changed","level":"max"}`)
	h.settle()
	h.sink.mu.Lock()
	assert.Equal(t, []native.Settings{{Model: "openai-codex/gpt-6.1-sol", Effort: "max"}}, h.sink.settings)
	h.sink.mu.Unlock()
}

func TestSettingsWithoutAModel(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Config{}, "")
	h.answerState(noCredentialsState)
	h.answerModels(map[string]any{"success": true, "data": map[string]any{"models": []any{map[string]any{"id": "x", "provider": "y"}}}})
	require.NoError(t, h.startResult())
	assert.Equal(t, native.Settings{}, h.a.Settings())
}

func TestCompact(t *testing.T) {
	t.Parallel()
	h := newHarness(t, Config{}, "")
	h.answerState(lunaState)
	h.started()
	assert.True(t, h.a.Capabilities().Compact)
	h.call("compact", func() error { return h.a.Compact(context.Background()) })
	cmd := h.pi.nextCmd()
	require.Equal(t, "compact", cmd["type"])
	h.answer(cmd, `{"summary":"...","firstKeptEntryId":"x","tokensBefore":5000}`)
	require.NoError(t, h.result("compact"))

	// As Pi 1.0.2 answers for a short session.
	h.call("again", func() error { return h.a.Compact(context.Background()) })
	h.refuse(h.pi.nextCmd(), "Nothing to compact (session too small)")
	var refused *native.RefusedError
	require.ErrorAs(t, h.result("again"), &refused)
	assert.Equal(t, "Nothing to compact (session too small)", refused.Reason)
}
