package jsonlwatcher

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Lines from a Codex code-mode session (internal metadata removed).
const (
	codexExecCallLine   = `{"timestamp":"2026-10-04T19:26:27.489Z","type":"response_item","payload":{"type":"custom_tool_call","status":"completed","call_id":"call_NDJ","name":"exec","input":"text(await tools.exec_command({cmd:\"uname -s\",max_output_tokens:1000}));\n"}}`
	codexExecOutputLine = `{"timestamp":"2026-10-04T19:26:29.314Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"call_NDJ","output":[{"type":"input_text","text":"Script completed\nWall time 0.1 seconds\nOutput:\n"},{"type":"input_text","text":"{\"chunk_id\":\"7b5f9c\",\"wall_time_seconds\":0.000027079,\"exit_code\":0,\"original_token_count\":2,\"output\":\"Linux\\n\"}"}]}}`
)

func TestCodexExecShowsTheShellCommandAndItsOutput(t *testing.T) {
	p := NewCodexParser()
	msgs, err := p.ParseLine([]byte(codexExecCallLine))
	require.NoError(t, err)
	require.NotEmpty(t, msgs)
	call := msgs[len(msgs)-1].Content[0]
	assert.Equal(t, "tool_use", call.Type)
	assert.Equal(t, "exec", call.ToolName)
	var input map[string]string
	require.NoError(t, json.Unmarshal(call.ToolInput, &input))
	assert.Equal(t, "uname -s", input["cmd"])
	assert.Contains(t, input["script"], "tools.exec_command")

	msgs, err = p.ParseLine([]byte(codexExecOutputLine))
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	result := msgs[0].Content[0]
	assert.Equal(t, "Linux", result.Text)
	assert.Equal(t, "completed", result.Status)
	require.NotNil(t, result.IsError)
	assert.False(t, *result.IsError)
}

func TestCodexExecSessionEvents(t *testing.T) {
	p := NewCodexSessionEventParser()
	events, err := p.ParseSessionEvents([]byte(codexExecCallLine))
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.JSONEq(t, `"uname -s"`, mustJSONField(t, events[0].ToolInput, "cmd"))
	events, err = p.ParseSessionEvents([]byte(codexExecOutputLine))
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "Linux", *events[0].Content)
}

func mustJSONField(t *testing.T, raw json.RawMessage, field string) string {
	t.Helper()
	var obj map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &obj))
	return string(obj[field])
}

func TestCodexExecCommands(t *testing.T) {
	for _, tc := range []struct {
		name   string
		script string
		want   []string
	}{
		{"one command", `text(await tools.exec_command({cmd:"uname -s",max_output_tokens:1000}));`, []string{"uname -s"}},
		{"escapes", `text(await tools.exec_command({cmd:"grep -n \"a b\" x.go\nls",workdir:"/tmp"}));`, []string{"grep -n \"a b\" x.go\nls"}},
		{"single quotes", `await tools.exec_command({ cmd: 'echo "hi" it\'s' })`, []string{`echo "hi" it's`}},
		{"several commands", "const a = await tools.exec_command({cmd:\"pwd\"});\ntext(a);\ntext(await tools.exec_command({cmd:\"ls\"}));", []string{"pwd", "ls"}},
		{"other tools keep the script", `text(await tools.apply_patch({patch:"..."})); text(await tools.exec_command({cmd:"ls"}));`, nil},
		{"no tools", `text("hello")`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, codexExecCommands(tc.script))
		})
	}

	// Inputs that aren't code-mode exec scripts are left alone.
	raw := json.RawMessage(`"*** Begin Patch\n*** End Patch"`)
	assert.Equal(t, raw, codexExecInput("apply_patch", raw))
	script := json.RawMessage(`"text(await tools.view_image({path:\"a.png\"}))"`)
	assert.Equal(t, script, codexExecInput("exec", script))
}

func TestCodexExecOutput(t *testing.T) {
	out, code, ok := codexExecOutput("Script completed\nWall time 0.1 seconds\nOutput:\n\n" +
		`{"chunk_id":"a","exit_code":0,"output":"pwd-out\n"}` + "\n" +
		`{"chunk_id":"b","exit_code":2,"output":"ls: cannot access 'x'\n"}`)
	require.True(t, ok)
	assert.Equal(t, 2, code)
	assert.Equal(t, "pwd-out\nls: cannot access 'x'\nExit code: 2", out)

	// A script error has no chunks; its message is kept.
	out, _, ok = codexExecOutput("Script failed\nWall time 0.0 seconds\nOutput:\nReferenceError: foo is not defined")
	require.True(t, ok)
	assert.Equal(t, "ReferenceError: foo is not defined", out)

	// Other tool output is left alone.
	_, _, ok = codexExecOutput("Chunk ID: 1\nProcess exited with code 0\nOutput:\nhi")
	assert.False(t, ok)
}

func TestCodexExecFailedCommandIsAFailedResult(t *testing.T) {
	p := NewCodexParser()
	line := `{"timestamp":"2026-10-04T19:26:29.314Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"c1","output":[{"type":"input_text","text":"Script completed\nWall time 0.1 seconds\nOutput:\n"},{"type":"input_text","text":"{\"chunk_id\":\"x\",\"exit_code\":1,\"output\":\"\"}"}]}}`
	msgs, err := p.ParseLine([]byte(line))
	require.NoError(t, err)
	result := msgs[0].Content[0]
	assert.Equal(t, "failed", result.Status)
	assert.Equal(t, "Exit code: 1", result.Text)
}
