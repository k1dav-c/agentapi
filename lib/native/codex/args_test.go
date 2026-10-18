package codex

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want launchOptions
		err  string
	}{
		// Without a choice of the user's, the thread bypasses approvals and
		// the sandbox.
		{name: "none", want: launchOptions{approvalPolicy: "never", sandbox: "danger-full-access"}},
		{
			name: "config passes through",
			args: []string{"-c", `sandbox_mode="danger-full-access"`, "--config=approval_policy=\"untrusted\"", "--enable", "x", "--disable=y", "--strict-config"},
			want: launchOptions{appServerArgs: []string{"-c", `sandbox_mode="danger-full-access"`, "--config", `approval_policy="untrusted"`, "--enable", "x", "--disable", "y", "--strict-config"}, policyFromUser: true},
		},
		{
			name: "reasoning summary chosen by the user",
			args: []string{"-c", `model_reasoning_summary="concise"`},
			want: launchOptions{appServerArgs: []string{"-c", `model_reasoning_summary="concise"`}, summaryFromUser: true, approvalPolicy: "never", sandbox: "danger-full-access"},
		},
		{
			name: "another setting named like the reasoning summary",
			args: []string{"-c", "model_reasoning_summary_format=experimental"},
			want: launchOptions{appServerArgs: []string{"-c", "model_reasoning_summary_format=experimental"}, approvalPolicy: "never", sandbox: "danger-full-access"},
		},
		{
			name: "attached short values",
			args: []string{"-mgpt-6-luna", "-s=read-only", "-cmodel_reasoning_summary=none", "-C=sub"},
			want: launchOptions{
				model: "gpt-6-luna", sandbox: "read-only", cwd: "/work/sub",
				appServerArgs: []string{"-c", "model_reasoning_summary=none"}, summaryFromUser: true, policyFromUser: true,
			},
		},
		{name: "attached value of a rejected flag", args: []string{"-pwork"}, err: "codex flag -p is not supported with --transport native: profiles"},
		{name: "bypass takes no value", args: []string{"--dangerously-bypass-approvals-and-sandbox=false"}, err: "codex flag --dangerously-bypass-approvals-and-sandbox takes no value"},
		{name: "a pass-through switch takes no value", args: []string{"--strict-config=false"}, err: "codex flag --strict-config takes no value"},
		{name: "a dropped switch takes no value", args: []string{"--no-alt-screen=1"}, err: "codex flag --no-alt-screen takes no value"},
		{
			name: "top-level flags become thread settings",
			args: []string{"-m", "gpt-6-luna", "--sandbox=read-only", "-a", "on-request", "--cd", "/repo"},
			want: launchOptions{model: "gpt-6-luna", sandbox: "read-only", approvalPolicy: "on-request", cwd: "/repo", policyFromUser: true},
		},
		{name: "relative cd", args: []string{"-C", "sub/dir"}, want: launchOptions{cwd: "/work/sub/dir", approvalPolicy: "never", sandbox: "danger-full-access"}},
		{
			name: "the reviewer is a policy",
			args: []string{"-c", "approvals_reviewer=auto_review"},
			want: launchOptions{appServerArgs: []string{"-c", "approvals_reviewer=auto_review"}, policyFromUser: true},
		},
		{
			name: "bypass",
			args: []string{"--dangerously-bypass-approvals-and-sandbox"},
			want: launchOptions{approvalPolicy: "never", sandbox: "danger-full-access", policyFromUser: true},
		},
		{name: "app-server subcommand is allowed", args: []string{"app-server", "-c", "a=1"}, want: launchOptions{appServerArgs: []string{"-c", "a=1"}, approvalPolicy: "never", sandbox: "danger-full-access"}},
		{name: "terminal-only flags are dropped", args: []string{"--no-alt-screen", "--no-daemon"}, want: launchOptions{approvalPolicy: "never", sandbox: "danger-full-access"}},
		{name: "bad sandbox", args: []string{"-s", "open"}, err: `unknown sandbox mode "open"`},
		{name: "bad policy", args: []string{"-a", "sometimes"}, err: `unknown approval policy "sometimes"`},
		{name: "missing value", args: []string{"-m"}, err: "-m needs a value"},
		{name: "profile", args: []string{"-p", "work"}, err: "codex flag -p is not supported with --transport native: profiles"},
		{name: "image", args: []string{"--image", "a.png"}, err: "--image is not supported"},
		{name: "unknown flag", args: []string{"--frobnicate"}, err: "codex flag --frobnicate is not supported"},
		{name: "prompt", args: []string{"fix the bug"}, err: `codex argument "fix the bug" is not supported`},
		{name: "subcommand", args: []string{"exec", "hi"}, err: `codex argument "exec" is not supported`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseArgs(tt.args, "/work")
			if tt.err != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNewFactoryRejectsBadArgs(t *testing.T) {
	_, err := NewFactory(Config{Program: "codex", Args: []string{"resume", "--last"}})
	require.Error(t, err)
}
