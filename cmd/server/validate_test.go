package server

import (
	"bytes"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/statedb"
)

// --validate checks the settings as a start would, from flags, variables
// and the config file, and starts nothing: it leaves no PID file, registry
// entry or state database.
func TestValidate(t *testing.T) {
	run := func(t *testing.T, dir, config string, env map[string]string, args ...string) (string, error) {
		t.Helper()
		isolateViper(t)
		for key, value := range env {
			t.Setenv(key, value)
		}
		configPath := filepath.Join(dir, "config.yaml")
		if config != "" {
			require.NoError(t, os.WriteFile(configPath, []byte(config), 0o600))
		}
		serverCmd := CreateServerCmd()
		var out bytes.Buffer
		serverCmd.SetOut(&out)
		serverCmd.SetErr(io.Discard)
		serverCmd.SetArgs(append([]string{
			"--validate", "--config", configPath, "--registry-dir", filepath.Join(dir, "instances"),
			"--pid-file", filepath.Join(dir, "agentapi.pid"), "--state-db", filepath.Join(dir, "state.db"),
		}, args...))
		err := serverCmd.Execute()
		return out.String(), err
	}

	t.Run("ok", func(t *testing.T) {
		dir := t.TempDir()
		out, err := run(t, dir, "", nil, "--type", "custom", "--api-token", "--", "native-demo")
		require.NoError(t, err)
		assert.Equal(t, fmt.Sprintf("ok state-db-schema=%d\n", statedb.Schema()), out, "one line, no log")
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		assert.Empty(t, entries, "it writes nothing")
	})

	for name, tc := range map[string]struct {
		config string
		env    map[string]string
		args   []string
		want   string
	}{
		"an unknown flag":            {args: []string{"--nope", "--", "native-demo"}, want: "unknown flag: --nope"},
		"a value":                    {args: []string{"--transport", "bogus", "--", "native-demo"}, want: `unknown --transport "bogus"`},
		"a value in the config":      {config: "transport: bogus\n", args: []string{"--", "native-demo"}, want: `unknown --transport "bogus"`},
		"a variable":                 {env: map[string]string{"AGENTAPI_BIND": "127.0.0.1:80"}, args: []string{"--", "native-demo"}, want: "invalid --bind"},
		"the log level":              {config: "log-level: loud\n", args: []string{"--", "native-demo"}, want: "invalid --log-level"},
		"a port":                     {args: []string{"--port", "70000", "--", "native-demo"}, want: "invalid --port 70000"},
		"the agent type":             {args: []string{"--type", "nope", "--", "native-demo"}, want: "not nope"},
		"the native agent's args":    {args: []string{"--type", "codex", "--", "codex", "--sandbox"}, want: "--sandbox"},
		"an agent of AgentAPI 2":     {args: []string{"--type", "aider", "--", "aider"}, want: "for other agents, use AgentAPI v2.5.1"},
		"a custom agent":             {args: []string{"--", "bash"}, want: "not a custom agent"},
		"the PTY transport":          {args: []string{"--transport", "pty", "--", "native-demo"}, want: "AgentAPI 3 has no PTY transport"},
		"the PTY transport (config)": {config: "transport: pty\n", args: []string{"--", "native-demo"}, want: "AgentAPI 3 has no PTY transport"},
		"the PTY transport (env)":    {env: map[string]string{"AGENTAPI_TRANSPORT": "pty"}, args: []string{"--", "native-demo"}, want: "AgentAPI 3 has no PTY transport"},
		"allowed hosts":              {args: []string{"--allowed-hosts", "localhost:80", "--", "native-demo"}, want: "failed to parse allowed hosts"},
		"allowed origins":            {env: map[string]string{"AGENTAPI_ALLOWED_ORIGINS": "http://a,http://b"}, args: []string{"--", "native-demo"}, want: "failed to parse allowed origins"},
		"the webhook":                {config: "webhook-url: ftp://hub\n", args: []string{"--", "native-demo"}, want: "webhook URL must use http or https"},
		"a placeholder":              {args: []string{"--public-url", "http://{hostname}:{nope}/", "--", "native-demo"}, want: "public URL"},
		"the prices file":            {args: []string{"--prices", "/nonexistent/prices.json", "--", "native-demo"}, want: "--prices"},
		"a removed transport (acp)":  {args: []string{"--experimental-acp", "--", "native-demo"}, want: "ACP transport was removed"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			out, err := run(t, dir, tc.config, tc.env, append([]string{"--type", "custom"}, tc.args...)...)
			require.ErrorContains(t, err, tc.want)
			assert.NotContains(t, out, validateOK)
		})
	}

	t.Run("a state database a newer AgentAPI changed", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "state.db")
		db, err := sql.Open("sqlite", path)
		require.NoError(t, err)
		newer := statedb.Schema() + 1
		for _, query := range []string{
			"CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)",
			fmt.Sprintf("INSERT INTO meta (key, value) VALUES ('min_writer_schema', '%d'), ('min_writer_version', '99.0.0')", newer),
			fmt.Sprintf("PRAGMA user_version = %d", newer),
		} {
			_, err := db.Exec(query)
			require.NoError(t, err)
		}
		require.NoError(t, db.Close())
		_, err = run(t, dir, "", nil, "--type", "custom", "--", "native-demo")
		require.ErrorContains(t, err, "needs AgentAPI 99.0.0 or later")

		var schema int
		db, err = sql.Open("sqlite", path)
		require.NoError(t, err)
		defer func() { _ = db.Close() }()
		require.NoError(t, db.QueryRow("PRAGMA user_version").Scan(&schema))
		assert.Equal(t, newer, schema, "left as it was")
	})
}
