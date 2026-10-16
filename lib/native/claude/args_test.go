package claude

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestArgs(t *testing.T) {
	t.Parallel()

	t.Run("defaults", func(t *testing.T) {
		t.Parallel()
		p, err := parseArgs([]string{"--model", "haiku", "--allowedTools", "Read", "Edit", "--mcp-config", "a.json"})
		require.NoError(t, err)
		args := buildArgs(p, "sid", "")
		assert.Equal(t, []string{
			"-p", "--output-format", "stream-json", "--input-format", "stream-json", "--verbose",
			"--include-partial-messages", "--replay-user-messages", "--permission-prompt-tool", "stdio",
			"--session-id=sid", "--permission-mode", "bypassPermissions", "--thinking", "adaptive", "--thinking-display", "summarized",
			"--model", "haiku", "--allowedTools", "Read", "Edit", "--mcp-config", "a.json",
		}, args)
	})

	t.Run("resume", func(t *testing.T) {
		t.Parallel()
		args := buildArgs(parsedArgs{}, "", "-abc")
		assert.Contains(t, args, "--resume=-abc")
		assert.NotContains(t, strings.Join(args, " "), "--session-id")
	})

	t.Run("user choices win", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			args     []string
			absent   []string
			contains []string
		}{
			{args: []string{"--permission-mode", "acceptEdits"}, absent: []string{"bypassPermissions"}},
			{args: []string{"--permission-mode=plan"}, absent: []string{"bypassPermissions"}},
			{args: []string{"--permission-mode", "default"}, absent: []string{"bypassPermissions"}},
			{args: []string{"--dangerously-skip-permissions"}, absent: []string{"--permission-mode"}},
			// Allowing bypass as an option doesn't choose a mode.
			{args: []string{"--allow-dangerously-skip-permissions"}, contains: []string{"bypassPermissions"}},
			{args: []string{"--thinking", "disabled"}, absent: []string{"adaptive", "summarized"}},
			{args: []string{"--max-thinking-tokens", "1000"}, absent: []string{"adaptive"}, contains: []string{"summarized"}},
			{args: []string{"--thinking-display", "omitted"}, absent: []string{"summarized"}, contains: []string{"adaptive"}},
		} {
			p, err := parseArgs(tc.args)
			require.NoError(t, err)
			args := buildArgs(p, "sid", "")
			for _, a := range tc.absent {
				assert.NotContains(t, args, a, tc.args)
			}
			for _, a := range tc.contains {
				assert.Contains(t, args, a, tc.args)
			}
		}
	})

	t.Run("protocol flags are dropped or rejected", func(t *testing.T) {
		t.Parallel()
		p, err := parseArgs([]string{"--verbose", "--include-partial-messages", "--replay-user-messages", "--model", "haiku"})
		require.NoError(t, err)
		assert.Equal(t, []string{"--model", "haiku"}, p.pass)

		for _, args := range [][]string{
			{"-p"},
			{"--print"},
			{"--output-format", "json"},
			{"--output-format=text"},
			{"--input-format", "text"},
			{"--resume", "abc"},
			{"-r"},
			{"--continue"},
			{"-c"},
			{"--session-id", "x"},
			{"--fork-session"},
			{"--permission-prompt-tool", "mcp__x"},
			{"--permission-prompts", "none"},
			{"--bg"},
		} {
			err := ValidateArgs(append([]string{"--model", "haiku"}, args...))
			require.Error(t, err, args)
			assert.Contains(t, err.Error(), "native transport", args)
		}
	})

	t.Run("flag values are not flags", func(t *testing.T) {
		t.Parallel()
		p, err := parseArgs([]string{"--append-system-prompt", "-p", "--add-dir", "a", "b", "--model", "haiku"})
		require.NoError(t, err)
		assert.Equal(t, []string{"--append-system-prompt", "-p", "--add-dir", "a", "b", "--model", "haiku"}, p.pass)
	})

	t.Run("no session persistence", func(t *testing.T) {
		t.Parallel()
		a, err := New(Config{Args: []string{"--no-session-persistence"}}, "")
		require.NoError(t, err)
		assert.False(t, a.Capabilities().Resume)
		a, err = New(Config{}, "")
		require.NoError(t, err)
		assert.True(t, a.Capabilities().Resume)
	})
}

func TestEnv(t *testing.T) {
	t.Parallel()
	// The environment of a process started from a Claude Code session's
	// Bash tool.
	env := buildEnv([]string{
		"PATH=/usr/bin",
		"HOME=/home/user",
		"CLAUDECODE=1",
		"CLAUDE_CODE_ENTRYPOINT=sdk-ts",
		"CLAUDE_CODE_SESSION_ID=4e82ebec-3420-4992-b3e4-043df2823dad",
		"CLAUDE_CODE_CHILD_SESSION=1",
		"CLAUDE_CODE_SESSION_ATTENDED=0",
		"CLAUDE_CODE_EXECPATH=/home/user/.local/share/claude/versions/2.1.299",
		"CLAUDE_CODE_MESSAGING_SOCKET=/tmp/cc-socks/1.sock",
		"CLAUDE_CODE_MESSAGING_TOKEN=secret",
		"CLAUDE_CODE_SSE_PORT=12345",
		"CLAUDE_PID=1",
		"CLAUDE_EFFORT=high",
		"CLAUDE_AGENT_SDK_VERSION=0.3.276",
		"AI_AGENT=claude-code",
		// Configuration stays.
		"CLAUDE_CONFIG_DIR=/home/user/.claude-work",
		"CLAUDE_CODE_USE_BEDROCK=1",
		"CLAUDE_CODE_MAX_OUTPUT_TOKENS=8000",
		"ANTHROPIC_API_KEY=key",
		"ANTHROPIC_BASE_URL=https://proxy",
	})
	assert.Equal(t, []string{
		"PATH=/usr/bin",
		"HOME=/home/user",
		"CLAUDE_CONFIG_DIR=/home/user/.claude-work",
		"CLAUDE_CODE_USE_BEDROCK=1",
		"CLAUDE_CODE_MAX_OUTPUT_TOKENS=8000",
		"ANTHROPIC_API_KEY=key",
		"ANTHROPIC_BASE_URL=https://proxy",
		"CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=1",
		"CLAUDE_CODE_ENTRYPOINT=sdk-cli",
	}, env)
}

func TestPlanOptionsFollowBypass(t *testing.T) {
	t.Parallel()
	ids := func(args ...string) []string {
		p, err := parseArgs(args)
		require.NoError(t, err)
		req := newPendingRequest("r", canUseTool{ToolName: toolExitPlanMode, Input: []byte(`{"plan":"x"}`)}, p.bypass)
		var out []string
		for _, o := range req.req.Options {
			out = append(out, o.ID)
		}
		return out
	}
	assert.Equal(t, []string{"bypassPermissions", "acceptEdits", "default", "deny"}, ids())
	assert.Equal(t, []string{"bypassPermissions", "acceptEdits", "default", "deny"}, ids("--permission-mode=bypassPermissions"))
	assert.Equal(t, []string{"bypassPermissions", "acceptEdits", "default", "deny"}, ids("--dangerously-skip-permissions"))
	assert.Equal(t, []string{"acceptEdits", "default", "deny"}, ids("--permission-mode", "plan"))
	assert.Equal(t, []string{"acceptEdits", "default", "deny"}, ids("--permission-mode", "default"))
}
