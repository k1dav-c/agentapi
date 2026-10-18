package server

import (
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/httpapi"
	st "github.com/coder/agentapi/lib/screentracker"
)

// flagsWith returns flags with the terminal size the server needs, and
// values on top.
func flagsWith(values map[string]any) *viper.Viper {
	v := viper.New()
	v.Set(FlagTermWidth, uint16(80))
	v.Set(FlagTermHeight, uint16(1000))
	for key, value := range values {
		v.Set(key, value)
	}
	return v
}

func TestLoadServerOptions(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		args   []string
		values map[string]any
		check  func(t *testing.T, opts serverOptions)
	}{
		"the program names the agent": {
			args: []string{"claude", "--model", "opus"},
			check: func(t *testing.T, opts serverOptions) {
				assert.Equal(t, AgentTypeClaude, opts.agentType)
				assert.Equal(t, "claude", opts.agent)
				assert.Equal(t, []string{"--model", "opus"}, opts.agentArgs)
				assert.Equal(t, httpapi.TransportNative, opts.transport)
				assert.Equal(t, st.StatePersistenceConfig{}, opts.state)
			},
		},
		"codex, pi and the demo agent default to native": {
			args:   []string{"native-demo"},
			values: map[string]any{FlagType: "custom"},
			check: func(t *testing.T, opts serverOptions) {
				assert.Equal(t, httpapi.TransportNative, opts.transport)
				for _, agent := range []string{"codex", "pi"} {
					other, err := loadServerOptions(flagsWith(nil), []string{agent})
					require.NoError(t, err)
					assert.Equal(t, httpapi.TransportNative, other.transport, agent)
				}
			},
		},
		"an agent without a native adapter defaults to PTY": {
			args: []string{"aider"},
			check: func(t *testing.T, opts serverOptions) {
				assert.Equal(t, httpapi.TransportPTY, opts.transport)
				custom, err := loadServerOptions(flagsWith(nil), []string{"bash"})
				require.NoError(t, err)
				assert.Equal(t, httpapi.TransportPTY, custom.transport, "a custom agent")
			},
		},
		"--transport pty keeps a native agent on PTY": {
			values: map[string]any{FlagTransport: "pty"},
			check: func(t *testing.T, opts serverOptions) {
				assert.Equal(t, httpapi.TransportPTY, opts.transport)
			},
		},
		"--type names a wrapper's agent": {
			args:   []string{"my-wrapper"},
			values: map[string]any{FlagType: "codex"},
			check: func(t *testing.T, opts serverOptions) {
				assert.Equal(t, AgentTypeCodex, opts.agentType)
			},
		},
		"a state file loads and saves unless told not to": {
			values: map[string]any{FlagStateFile: "/tmp/state.json", FlagLoadState: false},
			check: func(t *testing.T, opts serverOptions) {
				assert.Equal(t, st.StatePersistenceConfig{StateFile: "/tmp/state.json", SaveState: true}, opts.state)
				// The state file is the PTY transport's persistence.
				assert.Equal(t, httpapi.TransportPTY, opts.transport)
			},
		},
		"--experimental-acp is --transport acp": {
			values: map[string]any{FlagExperimentalACP: true},
			check: func(t *testing.T, opts serverOptions) {
				assert.Equal(t, httpapi.TransportACP, opts.transport)
			},
		},
		"native with a state DB": {
			values: map[string]any{FlagStateDB: "/tmp/agent.db"},
			check: func(t *testing.T, opts serverOptions) {
				assert.Equal(t, httpapi.TransportNative, opts.transport)
				assert.Equal(t, "/tmp/agent.db", opts.stateDB)
			},
		},
		"a bound IPv6 address loses its brackets": {
			values: map[string]any{FlagBind: "[::1]"},
			check: func(t *testing.T, opts serverOptions) {
				assert.Equal(t, "::1", opts.bind)
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			args := tc.args
			if args == nil {
				args = []string{"claude"}
			}
			opts, err := loadServerOptions(flagsWith(tc.values), args)
			require.NoError(t, err)
			tc.check(t, opts)
		})
	}
}

func TestLoadServerOptionsRefuses(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		values map[string]any
		err    string
	}{
		"an unknown agent type":         {map[string]any{FlagType: "nope"}, "failed to parse agent type: invalid agent type: nope"},
		"a narrow terminal":             {map[string]any{FlagTermWidth: uint16(9)}, "term width must be at least 10"},
		"a short terminal":              {map[string]any{FlagTermHeight: uint16(9)}, "term height must be at least 10"},
		"--load-state without a file":   {map[string]any{FlagLoadState: true}, "--load-state requires --state-file to be set"},
		"--save-state without a file":   {map[string]any{FlagSaveState: true}, "--save-state requires --state-file to be set"},
		"an unknown transport":          {map[string]any{FlagTransport: "ssh"}, `unknown --transport "ssh" (one of: pty, acp, native)`},
		"ACP and native":                {map[string]any{FlagExperimentalACP: true, FlagTransport: "native"}, "--experimental-acp and --transport=native are mutually exclusive"},
		"a state file with ACP":         {map[string]any{FlagTransport: "acp", FlagStateFile: "/tmp/s.json"}, "ACP mode doesn't support state persistence"},
		"a state file with native":      {map[string]any{FlagTransport: "native", FlagStateFile: "/tmp/s.json"}, "the native transport doesn't support --state-file; use --state-db"},
		"a state DB with PTY":           {map[string]any{FlagTransport: "pty", FlagStateDB: "/tmp/agent.db"}, "--state-db needs --transport native"},
		"the OpenAPI schema with ACP":   {map[string]any{FlagPrintOpenAPI: true, FlagExperimentalACP: true}, "flags --print-openapi and --experimental-acp are mutually exclusive"},
		"a bind address with a port":    {map[string]any{FlagBind: "127.0.0.1:3284"}, "invalid --bind: '127.0.0.1:3284' must not include a port; use --port"},
		"a bind address that isn't one": {map[string]any{FlagBind: "not a host"}, "invalid --bind"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := loadServerOptions(flagsWith(tc.values), []string{"claude"})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.err)
		})
	}
}
