package claude

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/native"
)

func TestResponseTakesOfferedOptions(t *testing.T) {
	t.Parallel()
	// The session didn't start in bypassPermissions, so approving the
	// plan doesn't offer to go back to it.
	plan := newPendingRequest("r1", canUseTool{ToolName: toolExitPlanMode, Input: []byte(`{"plan":"x"}`)}, false)
	_, err := plan.response(native.Response{OptionID: optionBypass})
	require.ErrorIs(t, err, native.ErrInvalidResponse)
	out, err := plan.response(native.Response{OptionID: optionAcceptEdits})
	require.NoError(t, err)
	assert.Equal(t, "allow", out["behavior"])

	// Without suggestions there is no "allow always".
	approval := newPendingRequest("r2", canUseTool{ToolName: "Bash", Input: []byte(`{"command":"ls"}`)}, true)
	for _, id := range []string{optionAllowAlways, optionBypass, ""} {
		_, err := approval.response(native.Response{OptionID: id})
		require.ErrorIs(t, err, native.ErrInvalidResponse, id)
	}
	out, err = approval.response(native.Response{OptionID: optionDeny, Text: "no"})
	require.NoError(t, err)
	assert.Equal(t, "no", out["message"])
}

func TestInputSummary(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "ls -la …", inputSummary(json.RawMessage(`{"command":"ls -la\necho done","description":"List"}`)))
	assert.Equal(t, "/tmp/a.go", inputSummary(json.RawMessage(`{"file_path":"/tmp/a.go"}`)))
	assert.Empty(t, inputSummary(json.RawMessage(`{"todos":[]}`)))

	// A long command is cut between characters, not inside one.
	summary := inputSummary(json.RawMessage(fmt.Sprintf(`{"command":%q}`, "echo "+strings.Repeat("中文", 100))))
	assert.True(t, utf8.ValidString(summary))
	assert.Equal(t, 120, utf8.RuneCountInString(summary))
	assert.True(t, strings.HasSuffix(summary, "…"))
}
