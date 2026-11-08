package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/instanceapi"
)

// flagsWith returns flags with values.
func flagsWith(values map[string]any) *viper.Viper {
	v := viper.New()
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
				assert.Equal(t, instanceapi.AgentTypeClaude, opts.agentType)
				assert.Equal(t, "claude", opts.agent)
				assert.Equal(t, []string{"--model", "opus"}, opts.agentArgs)
				assert.Empty(t, opts.stateFile)
				assert.Empty(t, opts.stateDB)
			},
		},
		"codex, pi and the demo agent": {
			args:   []string{"native-demo"},
			values: map[string]any{FlagType: "custom"},
			check: func(t *testing.T, opts serverOptions) {
				assert.Equal(t, instanceapi.AgentTypeCustom, opts.agentType)
				for _, agent := range []instanceapi.AgentType{instanceapi.AgentTypeCodex, instanceapi.AgentTypePi} {
					other, err := loadServerOptions(flagsWith(nil), []string{string(agent)})
					require.NoError(t, err)
					assert.Equal(t, agent, other.agentType)
				}
			},
		},
		"--transport native, deprecated, and --term-width, without effect, are accepted": {
			values: map[string]any{FlagTransport: "native", FlagTermWidth: uint16(5)},
			check:  func(*testing.T, serverOptions) {},
		},
		"--type names a wrapper's agent": {
			args:   []string{"my-wrapper"},
			values: map[string]any{FlagType: "codex"},
			check: func(t *testing.T, opts serverOptions) {
				assert.Equal(t, instanceapi.AgentTypeCodex, opts.agentType)
			},
		},
		"a state file is imported into a state DB next to it": {
			values: map[string]any{FlagStateFile: "/tmp/state.json"},
			check: func(t *testing.T, opts serverOptions) {
				assert.Equal(t, "/tmp/state.json", opts.stateFile)
				assert.True(t, opts.importState)
				assert.Equal(t, "/tmp/state.db", opts.stateDB)
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
		"retries wait the default delays": {
			check: func(t *testing.T, opts serverOptions) {
				assert.Nil(t, opts.retryDelays, "the conversation's default: 30s, 1m, 2m, then 5m twelve times")
			},
		},
		"--retry-delays, from a flag, a variable or the config file": {
			values: map[string]any{FlagRetryDelays: []string{"10s", "1m"}},
			check: func(t *testing.T, opts serverOptions) {
				assert.Equal(t, []time.Duration{10 * time.Second, time.Minute}, opts.retryDelays)
				for value, want := range map[string][]time.Duration{
					"20s,2m":   {20 * time.Second, 2 * time.Minute},
					"20s 2m":   {20 * time.Second, 2 * time.Minute},
					"10s,1m*3": {10 * time.Second, time.Minute, time.Minute, time.Minute},
					"off":      {},
					"0":        {},
					"":         {},
				} {
					other, err := loadServerOptions(flagsWith(map[string]any{FlagRetryDelays: value}), []string{"claude"})
					require.NoError(t, err)
					assert.Equal(t, want, other.retryDelays, value)
				}
			},
		},
		"native with a state DB": {
			values: map[string]any{FlagStateDB: "/tmp/agent.db"},
			check: func(t *testing.T, opts serverOptions) {
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
		"--state-db and --pid-file fill in {port} and {hostname}; path settings expand ~": {
			values: map[string]any{
				FlagStateDB: "~/.agentapi/state/{hostname}-{port}.db", FlagPort: 3286,
				FlagPidFile: "~/run/{port}.pid", FlagPrices: "~/prices.json", FlagRegistryDir: "~/reg", FlagRegistry: true,
			},
			check: func(t *testing.T, opts serverOptions) {
				home, err := os.UserHomeDir()
				require.NoError(t, err)
				hostname, err := os.Hostname()
				require.NoError(t, err)
				assert.Equal(t, filepath.Join(home, ".agentapi", "state", hostname+"-3286.db"), opts.stateDB)
				assert.Equal(t, filepath.Join(home, "run", "3286.pid"), opts.pidFile)
				assert.Equal(t, filepath.Join(home, "prices.json"), opts.prices)
				assert.Equal(t, filepath.Join(home, "reg"), opts.registryDir)
				other, err := loadServerOptions(flagsWith(map[string]any{FlagStateDB: "~/.agentapi/state/{port}.db", FlagPort: 3285}), []string{"codex"})
				require.NoError(t, err)
				assert.Equal(t, filepath.Join(home, ".agentapi", "state", "3285.db"), other.stateDB, "another port, another file")
				file, err := loadServerOptions(flagsWith(map[string]any{FlagStateFile: "~/state.json"}), []string{"claude"})
				require.NoError(t, err)
				assert.Equal(t, filepath.Join(home, "state.json"), file.stateFile)
				assert.Equal(t, filepath.Join(home, "state.db"), file.stateDB)
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
		"an unknown agent type":          {map[string]any{FlagType: "nope"}, "AgentAPI 3 runs Claude Code, Codex and Pi natively, not nope"},
		"an agent of AgentAPI 2":         {map[string]any{FlagType: "aider"}, "for other agents, use AgentAPI v2.5.1"},
		"PTY, removed":                   {map[string]any{FlagTransport: "pty"}, "AgentAPI 3 has no PTY transport: it runs Claude Code, Codex and Pi natively; drop --transport pty (or AGENTAPI_TRANSPORT=pty), or use AgentAPI v2.5.1"},
		"--load-state without a file":    {map[string]any{FlagLoadState: true}, "--load-state requires --state-file to be set"},
		"--save-state without a file":    {map[string]any{FlagSaveState: true}, "--save-state requires --state-file to be set"},
		"a retry delay that isn't one":   {map[string]any{FlagRetryDelays: "30s,soon"}, `invalid --retry-delays: time: invalid duration "soon"`},
		"a retry delay of no time":       {map[string]any{FlagRetryDelays: "-1s"}, "invalid --retry-delays: -1s: a delay is longer than 0"},
		"a retry delay repeated no time": {map[string]any{FlagRetryDelays: "5m*0"}, "invalid --retry-delays: 5m*0: repeat a delay 1 or more times"},
		"too many retries":               {map[string]any{FlagRetryDelays: "30s,5m*100"}, "invalid --retry-delays: more than 100 retries"},
		"an unknown transport":           {map[string]any{FlagTransport: "ssh"}, `unknown --transport "ssh": AgentAPI 3 drives every agent natively`},
		"ACP, removed":                   {map[string]any{FlagTransport: "acp"}, "the experimental ACP transport was removed"},
		"--experimental-acp, removed":    {map[string]any{FlagExperimentalACP: true}, "the experimental ACP transport was removed"},
		"a bind address with a port":     {map[string]any{FlagBind: "127.0.0.1:3284"}, "invalid --bind: '127.0.0.1:3284' must not include a port; use --port"},
		"a bind address that isn't one":  {map[string]any{FlagBind: "not a host"}, "invalid --bind"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := loadServerOptions(flagsWith(tc.values), []string{"claude"})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.err)
		})
	}
}
