package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/native/procutil"
)

// testInitialize is an initialize answer with models, as Claude Code
// 2.1.302 sends it.
const testInitialize = `{"claude_code_version":"2.1.302","current_permission_mode":"bypassPermissions","models":[
	{"value":"default","resolvedModel":"claude-opus-5-5","displayName":"Default (recommended)","description":"Opus 5.5","supportsEffort":true,"supportedEffortLevels":["low","medium","high","xhigh","max"],"supportsAutoMode":true},
	{"value":"opus","resolvedModel":"claude-opus-5-5","displayName":"Opus","description":"Opus 5.5","supportsEffort":true,"supportedEffortLevels":["low","medium","high","xhigh","max"],"supportsAutoMode":true},
	{"value":"sonnet","resolvedModel":"claude-sonnet-5-5","displayName":"Sonnet","description":"Sonnet 5.5","supportsEffort":true,"supportedEffortLevels":["low","medium","high","xhigh","max"],"supportsAutoMode":true},
	{"value":"haiku","resolvedModel":"claude-haiku-5-5","displayName":"Haiku","description":"Haiku 5.5","supportsEffort":false,"supportsAutoMode":false}
]}`

// startedWith is started with initialize answered by init and get_settings
// by applied.
func startedWith(t *testing.T, cfg Config, init, applied string) (*Adapter, *recSink, *fakeProcess) {
	t.Helper()
	proc := newFakeProcess("")
	proc.settings = applied
	cfg.StartProcess = func(procutil.Command) (procutil.Process, error) { return proc, nil }
	cfg.AuthStatus = loggedIn
	cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg.Env = []string{}
	a := newTestAdapter(t, cfg, "")
	sink := &recSink{t: t}
	errCh := make(chan error, 1)
	go func() { errCh <- a.Start(context.Background(), sink) }()
	request := proc.waitWritten(t, 0)
	require.Equal(t, "control_request:initialize", signature(request))
	var compact bytes.Buffer
	require.NoError(t, json.Compact(&compact, []byte(init)))
	proc.answer(t, request, compact.String())
	require.NoError(t, <-errCh)
	t.Cleanup(func() { _ = a.Close() })
	return a, sink, proc
}

func TestSettingsAtStart(t *testing.T) {
	t.Parallel()
	a, _, _ := startedWith(t, Config{}, testInitialize, `{"model":"claude-opus-5-5","effort":"high"}`)
	assert.True(t, a.Capabilities().Settings)
	// Both default and opus run Opus 5.5; default is what the user didn't
	// choose.
	assert.Equal(t, native.Settings{PermissionMode: native.PermissionModeBypass, Model: "default", Effort: "high"}, a.Settings())

	options, err := a.SettingOptions(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []native.PermissionMode{native.PermissionModeBypass, native.PermissionModeAuto, native.PermissionModeAsk, native.PermissionModePlan}, options.PermissionModes)
	require.Len(t, options.Models, 4)
	assert.Equal(t, native.ModelOption{ID: "sonnet", Name: "Sonnet", Description: "Sonnet 5.5", Efforts: []string{"low", "medium", "high", "xhigh", "max"}}, options.Models[2])
	assert.Empty(t, options.Models[3].Efforts, "a model without efforts")

	// The model the user chose on the command line.
	init := strings.Replace(testInitialize, `"current_permission_mode":"bypassPermissions"`, `"current_permission_mode":"default"`, 1)
	a, _, _ = startedWith(t, Config{Args: []string{"--model", "opus", "--permission-mode", "default"}}, init, `{"model":"claude-opus-5-5","effort":"medium"}`)
	assert.Equal(t, native.Settings{PermissionMode: native.PermissionModeAsk, Model: "opus", Effort: "medium"}, a.Settings())
	// A model that isn't one of the options is reported by its name.
	a, _, _ = startedWith(t, Config{}, testInitialize, `{"model":"claude-opus-4-1","effort":"medium"}`)
	assert.Equal(t, "claude-opus-4-1", a.Settings().Model)
	// An older version without models or get_settings: the mode, and the
	// model once a turn runs on it.
	a, sink, proc := startedWith(t, Config{}, `{"current_permission_mode":"acceptEdits"}`, "")
	assert.Equal(t, native.Settings{}, a.Settings())
	proc.send(t, []byte(`{"type":"system","subtype":"init","session_id":"s1","model":"claude-opus-4-1"}`))
	proc.barrier(t)
	assert.Equal(t, native.Settings{Model: "claude-opus-4-1"}, a.Settings())
	sink.mu.Lock()
	assert.Len(t, sink.settings, 1)
	sink.mu.Unlock()
}

func TestSettingOptionsWithoutBypass(t *testing.T) {
	t.Parallel()
	factory, err := newFactory(Config{Env: []string{"PATH=/bin"}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}, 0)
	require.NoError(t, err)
	adapter, err := factory("")
	require.NoError(t, err)
	a := adapter.(*Adapter)
	a.models = []claudeModel{{Value: "haiku", ResolvedModel: "claude-haiku-5-5", SupportsAutoMode: new(bool)}}
	a.model = "haiku"
	options, err := a.SettingOptions(context.Background())
	require.NoError(t, err)
	// Root outside a sandbox can't bypass permissions, and Haiku has no
	// auto mode.
	assert.Equal(t, []native.PermissionMode{native.PermissionModeAsk, native.PermissionModePlan}, options.PermissionModes)
	assert.ErrorIs(t, a.ApplySettings(context.Background(), native.Settings{PermissionMode: native.PermissionModeBypass}), native.ErrInvalidSetting)
}

func TestApplySettings(t *testing.T) {
	t.Parallel()
	a, sink, proc := startedWith(t, Config{}, testInitialize, `{"model":"claude-opus-5-5","effort":"high"}`)
	ctx := context.Background()

	done := make(chan error, 1)
	go func() {
		done <- a.ApplySettings(ctx, native.Settings{Model: "sonnet", Effort: "low", PermissionMode: native.PermissionModeAsk})
	}()
	model, i := proc.waitRequest(t, 1, "set_model")
	assert.Equal(t, "sonnet", model["request"].(map[string]any)["model"])
	proc.answer(t, model, `{}`)
	effort, i := proc.waitRequest(t, i+1, "apply_flag_settings")
	assert.Equal(t, map[string]any{"effortLevel": "low"}, effort["request"].(map[string]any)["settings"])
	proc.answer(t, effort, `{}`)
	mode, _ := proc.waitRequest(t, i+1, "set_permission_mode")
	assert.Equal(t, "default", mode["request"].(map[string]any)["mode"])
	proc.answer(t, mode, `{"mode":"default"}`)
	require.NoError(t, <-done)
	assert.Equal(t, native.Settings{PermissionMode: native.PermissionModeAsk, Model: "sonnet", Effort: "low"}, a.Settings())

	// A refused mode is answered without it.
	go func() { done <- a.ApplySettings(ctx, native.Settings{PermissionMode: native.PermissionModeAuto}) }()
	mode, _ = proc.waitRequest(t, 1, "set_permission_mode")
	mode, _ = proc.waitRequest(t, 1+indexOf(proc, mode), "set_permission_mode")
	proc.answer(t, mode, `{}`)
	require.ErrorContains(t, <-done, "refused the auto permission mode")
	assert.Equal(t, native.PermissionModeAsk, a.Settings().PermissionMode)

	assert.ErrorIs(t, a.ApplySettings(ctx, native.Settings{Model: "gpt"}), native.ErrInvalidSetting)
	assert.Empty(t, sink.settings, "changes made through ApplySettings aren't reported back")
}

// indexOf returns the index of frame among those proc read.
func indexOf(proc *fakeProcess, frame map[string]any) int {
	for i, f := range proc.writtenFrames() {
		if f["request_id"] == frame["request_id"] {
			return i
		}
	}
	return -1
}

func TestClaudeChangesItsSettings(t *testing.T) {
	t.Parallel()
	a, sink, proc := startedWith(t, Config{}, testInitialize, `{"model":"claude-opus-5-5","effort":"high"}`)
	// An approved plan switched to acceptEdits, a mode of Claude Code's
	// own.
	proc.send(t, []byte(`{"type":"system","subtype":"status","status":null,"permissionMode":"acceptEdits"}`))
	// A turn runs on another model (a fallback).
	proc.send(t, []byte(`{"type":"system","subtype":"init","session_id":"s1","permissionMode":"acceptEdits","model":"claude-sonnet-5-5"}`))
	// The same model under its full name changes nothing.
	proc.send(t, []byte(`{"type":"system","subtype":"init","session_id":"s1","permissionMode":"acceptEdits","model":"claude-sonnet-5-5"}`))
	proc.barrier(t)
	assert.Equal(t, []native.Settings{
		{Model: "default", Effort: "high"},
		{Model: "sonnet", Effort: "high"},
	}, sink.settings)
	assert.Equal(t, native.Settings{Model: "sonnet", Effort: "high"}, a.Settings())
}
