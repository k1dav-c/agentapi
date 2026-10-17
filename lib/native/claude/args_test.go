package claude

import (
	"bytes"
	"log/slog"
	"slices"
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
			"--session-id=sid", "--permission-mode", "bypassPermissions", "--allow-dangerously-skip-permissions",
			"--thinking", "adaptive", "--thinking-display", "summarized",
			"--model", "haiku", "--allowedTools", "Read", "Edit", "--mcp-config", "a.json",
		}, args)
		assert.Equal(t, "haiku", p.model)
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
			{args: []string{"--dangerously-skip-permissions"}, absent: []string{"--permission-mode", "--allow-dangerously-skip-permissions"}},
			// Allowing bypass as an option doesn't choose a mode.
			{args: []string{"--allow-dangerously-skip-permissions"}, contains: []string{"bypassPermissions"}},
			// A mode the user chose can still be switched to bypass.
			{args: []string{"--permission-mode", "default"}, contains: []string{"--allow-dangerously-skip-permissions"}},
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
			assert.LessOrEqual(t, strings.Count(strings.Join(args, " "), "--allow-dangerously-skip-permissions"), 1, tc.args)
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
			{"--rc"},
			{"--remote", "fix the bug"},
			{"--sdk-url", "ws://localhost:1234"},
			{"--resume-session-at", "msg_1"},
			{"--resume-drops-turn=msg_1"},
			// Claude Code splits groups of short flags.
			{"-pc"},
			{"-rabc"},
			{"-cn", "name"},
		} {
			_, err := parseArgs(append([]string{"--model", "haiku"}, args...))
			require.Error(t, err, args)
			assert.Contains(t, err.Error(), "native transport", args)
		}
		_, err = parseArgs([]string{"-cn", "name"})
		require.EqualError(t, err, "-c (in -cn) can't be used with the native transport: AgentAPI picks the session to resume")
	})

	t.Run("flag values are not flags", func(t *testing.T) {
		t.Parallel()
		p, err := parseArgs([]string{"--append-system-prompt", "-p", "--add-dir", "a", "b", "--model", "haiku"})
		require.NoError(t, err)
		assert.Equal(t, []string{"--append-system-prompt", "-p", "--add-dir", "a", "b", "--model", "haiku"}, p.pass)

		// In a group, a flag that takes a value takes the rest: --debug
		// with the filter "pc", a session named "pc".
		p, err = parseArgs([]string{"-dpc", "-npc", "-d2e"})
		require.NoError(t, err)
		assert.Equal(t, []string{"-dpc", "-npc", "-d2e"}, p.pass)
	})

	t.Run("no session persistence", func(t *testing.T) {
		t.Parallel()
		assert.False(t, newTestAdapter(t, Config{Args: []string{"--no-session-persistence"}}, "").Capabilities().Resume)
		assert.True(t, newTestAdapter(t, Config{}, "").Capabilities().Resume)
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

func TestRootDefaultMode(t *testing.T) {
	t.Parallel()
	// Claude Code exits at startup with bypassPermissions as root outside
	// a sandbox it knows of.
	for _, tc := range []struct {
		name string
		uid  int
		env  []string
		args []string
		// mode is the permission mode the adapter adds, if any.
		mode string
		// bypass: the session can switch to bypassPermissions.
		bypass bool
		warns  bool
	}{
		{name: "user", uid: 1000, mode: "bypassPermissions", bypass: true},
		{name: "user chose a mode", uid: 1000, args: []string{"--permission-mode", "acceptEdits"}, bypass: true},
		{name: "root", uid: 0, mode: "default", warns: true},
		{name: "root in a sandbox", uid: 0, env: []string{"IS_SANDBOX=1"}, mode: "bypassPermissions", bypass: true},
		{name: "root in bubblewrap", uid: 0, env: []string{"CLAUDE_CODE_BUBBLEWRAP=true"}, mode: "bypassPermissions", bypass: true},
		{name: "root with other values", uid: 0, env: []string{"IS_SANDBOX=true", "CLAUDE_CODE_BUBBLEWRAP=0"}, mode: "default", warns: true},
		{name: "root chose a mode", uid: 0, args: []string{"--permission-mode", "acceptEdits"}},
		// Claude Code refuses it; that is its error to show.
		{name: "root skips permissions", uid: 0, args: []string{"--dangerously-skip-permissions"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var logs bytes.Buffer
			factory, err := newFactory(Config{
				Args:   tc.args,
				Env:    append([]string{"PATH=/bin"}, tc.env...),
				Logger: slog.New(slog.NewTextHandler(&logs, nil)),
			}, tc.uid)
			require.NoError(t, err)
			// Every adapter of the factory (every restart) starts the same.
			for range 2 {
				adapter, err := factory("")
				require.NoError(t, err)
				a := adapter.(*Adapter)
				assert.Equal(t, tc.mode, a.args.mode)
				args := buildArgs(a.args, "sid", "")
				if tc.mode != "" {
					i := slices.Index(args, "--permission-mode")
					require.GreaterOrEqual(t, i, 0)
					assert.Equal(t, tc.mode, args[i+1])
				}
				assert.Equal(t, tc.bypass, a.args.allowBypass)
				assert.Equal(t, tc.bypass && !a.args.skipPermissions, slices.Contains(args, "--allow-dangerously-skip-permissions"))
				// Approving a plan offers bypassPermissions only in a session
				// that can switch to it.
				plan := newPendingRequest("r", canUseTool{ToolName: toolExitPlanMode, Input: []byte(`{"plan":"x"}`)}, a.args.allowBypass)
				assert.Equal(t, tc.bypass, plan.req.Options[0].ID == optionBypass)
			}
			// The warning says why and how to opt in, once.
			assert.Equal(t, tc.warns, strings.Contains(logs.String(), "IS_SANDBOX=1"), logs.String())
			assert.LessOrEqual(t, strings.Count(logs.String(), "level=WARN"), 1)
		})
	}
}
