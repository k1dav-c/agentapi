package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/httpapi"
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
				assert.Empty(t, opts.stateFile)
				assert.Empty(t, opts.stateDB)
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
		"a state file is imported into a state DB next to it": {
			values: map[string]any{FlagStateFile: "/tmp/state.json"},
			check: func(t *testing.T, opts serverOptions) {
				assert.Equal(t, "/tmp/state.json", opts.stateFile)
				assert.True(t, opts.importState)
				assert.Equal(t, "/tmp/state.db", opts.stateDB)
				// Command lines written for the PTY transport's state file
				// keep it.
				assert.Equal(t, httpapi.TransportPTY, opts.transport)
			},
		},
		"--load-state=false skips the import; --state-db names the database": {
			values: map[string]any{FlagStateFile: "/tmp/state.db", FlagLoadState: false, FlagSaveState: true},
			check: func(t *testing.T, opts serverOptions) {
				assert.False(t, opts.importState)
				assert.Equal(t, "/tmp/state.db.sqlite", opts.stateDB, "not the state file itself")
				named, err := loadServerOptions(flagsWith(map[string]any{FlagStateFile: "/tmp/s.json", FlagStateDB: "/tmp/agent.db"}), []string{"claude"})
				require.NoError(t, err)
				assert.Equal(t, "/tmp/agent.db", named.stateDB)
			},
		},
		"a state file with native": {
			values: map[string]any{FlagTransport: "native", FlagStateFile: "/tmp/s.json"},
			check: func(t *testing.T, opts serverOptions) {
				assert.Equal(t, httpapi.TransportNative, opts.transport)
				assert.Equal(t, "/tmp/s.db", opts.stateDB)
			},
		},
		"PTY with a state DB": {
			values: map[string]any{FlagTransport: "pty", FlagStateDB: "/tmp/agent.db"},
			check: func(t *testing.T, opts serverOptions) {
				assert.Equal(t, httpapi.TransportPTY, opts.transport)
				assert.Equal(t, "/tmp/agent.db", opts.stateDB)
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
		"--registry keeps an entry in ~/.agentapi/instances or --registry-dir": {
			values: map[string]any{FlagRegistry: true},
			check: func(t *testing.T, opts serverOptions) {
				home, err := os.UserHomeDir()
				require.NoError(t, err)
				assert.Equal(t, filepath.Join(home, ".agentapi", "instances"), opts.registryDir)
				named, err := loadServerOptions(flagsWith(map[string]any{FlagRegistry: true, FlagRegistryDir: "/tmp/reg"}), []string{"claude"})
				require.NoError(t, err)
				assert.Equal(t, "/tmp/reg", named.registryDir)
				off, err := loadServerOptions(flagsWith(map[string]any{FlagRegistry: false, FlagRegistryDir: "/tmp/reg"}), []string{"claude"})
				require.NoError(t, err)
				assert.Empty(t, off.registryDir)
			},
		},
		"--public-url fills in {port}": {
			values: map[string]any{FlagPublicURL: "http://dev-1.vpn:{port}", FlagPort: 3290},
			check: func(t *testing.T, opts serverOptions) {
				assert.Equal(t, "http://dev-1.vpn:3290", opts.webhook.PublicURL)
			},
		},
		"--instance-id and --name name the instance; --webhook-instance-id is the old name": {
			values: map[string]any{FlagInstanceID: "build-box", FlagName: "API", FlagWebhookInstanceID: "old"},
			check: func(t *testing.T, opts serverOptions) {
				assert.Equal(t, "build-box", opts.webhook.InstanceID)
				assert.Equal(t, "API", opts.name)
				old, err := loadServerOptions(flagsWith(map[string]any{FlagWebhookInstanceID: "old"}), []string{"claude"})
				require.NoError(t, err)
				assert.Equal(t, "old", old.webhook.InstanceID)
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
		"an unknown transport":          {map[string]any{FlagTransport: "ssh"}, `unknown --transport "ssh" (one of: pty, native)`},
		"ACP, removed":                  {map[string]any{FlagTransport: "acp"}, "the experimental ACP transport was removed"},
		"--experimental-acp, removed":   {map[string]any{FlagExperimentalACP: true}, "the experimental ACP transport was removed"},
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
