package server

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The config file's settings apply below the flags.
func TestReadUserConfig(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("webhook-url: http://hub:8080/hooks\nwebhook-events: [instance.*, task.*]\nport: 4000\ntypo: 1\n"), 0o600))
	flags := pflag.NewFlagSet("server", pflag.ContinueOnError)
	flags.String(FlagWebhookURL, "", "")
	flags.StringSlice(FlagWebhookEvents, nil, "")
	flags.Int(FlagPort, 3284, "")
	flags.String(FlagConfig, "", "")
	require.NoError(t, flags.Parse([]string{"--port=3285", "--config=" + path}))
	v := viper.New()
	require.NoError(t, v.BindPFlags(flags))

	readUserConfig(v, flags, slog.New(slog.NewTextHandler(io.Discard, nil)))
	assert.Equal(t, "http://hub:8080/hooks", v.GetString(FlagWebhookURL))
	assert.Equal(t, []string{"instance.*", "task.*"}, v.GetStringSlice(FlagWebhookEvents))
	assert.Equal(t, 3285, v.GetInt(FlagPort), "a flag wins")
}

// A file AgentAPI can't read doesn't keep the server from starting.
func TestReadUserConfigBroken(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("webhook-url: [\n"), 0o600))
	flags := pflag.NewFlagSet("server", pflag.ContinueOnError)
	flags.String(FlagWebhookURL, "", "")
	flags.String(FlagConfig, "", "")
	require.NoError(t, flags.Parse([]string{"--config=" + path}))
	v := viper.New()
	require.NoError(t, v.BindPFlags(flags))
	readUserConfig(v, flags, slog.New(slog.NewTextHandler(io.Discard, nil)))
	assert.Empty(t, v.GetString(FlagWebhookURL))
}

// Printing the schema doesn't read the config file: a hub in it would get
// this short run's events (make gen runs it).
func TestPrintOpenAPIIgnoresUserConfig(t *testing.T) {
	isolateViper(t)
	hits := make(chan string, 8)
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits <- r.Header.Get("X-AgentAPI-Event")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer hub.Close()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("webhook-url: "+hub.URL+"/hooks\nwebhook-events: [instance.*]\n"), 0o600))

	serverCmd := CreateServerCmd()
	setupCommandOutput(t, serverCmd)
	serverCmd.SetArgs([]string{"--print-openapi", "--config", path, "--registry-dir", t.TempDir(), "dummy-command"})
	require.NoError(t, serverCmd.Execute())
	select {
	case event := <-hits:
		t.Fatalf("the hub got %q from --print-openapi", event)
	case <-time.After(500 * time.Millisecond):
	}
}

// A state-db in the config file without {port} would be every server's,
// and the first one to start locks it: it is ignored, with a warning. A
// flag or a variable still names one.
func TestReadUserConfigSharedStateDB(t *testing.T) {
	for name, tc := range map[string]struct {
		file   string
		args   []string
		env    string
		want   string
		warned bool
	}{
		"without {port}": {file: "state-db: /data/agentapi.db\n", want: "", warned: true},
		"with {port}":    {file: "state-db: ~/.agentapi/state/{port}.db\n", want: "~/.agentapi/state/{port}.db"},
		"a flag":         {file: "state-db: /data/agentapi.db\n", args: []string{"--state-db=/data/mine.db"}, want: "/data/mine.db"},
		"a variable":     {file: "state-db: /data/agentapi.db\n", env: "/data/env.db", want: "/data/env.db"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("AGENTAPI_STATE_DB", tc.env)
			path := filepath.Join(t.TempDir(), "config.yaml")
			require.NoError(t, os.WriteFile(path, []byte(tc.file), 0o600))
			flags := pflag.NewFlagSet("server", pflag.ContinueOnError)
			flags.String(FlagStateDB, "", "")
			flags.String(FlagConfig, "", "")
			require.NoError(t, flags.Parse(append([]string{"--config=" + path}, tc.args...)))
			v := viper.New()
			require.NoError(t, v.BindPFlags(flags))
			v.SetEnvPrefix("AGENTAPI")
			v.AutomaticEnv()
			v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
			var logs bytes.Buffer

			readUserConfig(v, flags, slog.New(slog.NewTextHandler(&logs, nil)))
			assert.Equal(t, tc.want, v.GetString(FlagStateDB))
			assert.Equal(t, tc.warned, strings.Contains(logs.String(), "Ignoring state-db in the AgentAPI config file"), logs.String())
		})
	}
}
