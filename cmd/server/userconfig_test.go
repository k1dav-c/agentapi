package server

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
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

// The config file's host section is agentapi host's: agentapi server
// doesn't call it a setting it lacks, as it does a typo.
func TestReadUserConfigOtherSections(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("host:\n  port: 3291\ntypo: 1\n"), 0o600))
	flags := pflag.NewFlagSet("server", pflag.ContinueOnError)
	flags.String(FlagConfig, "", "")
	require.NoError(t, flags.Parse([]string{"--config=" + path}))
	v := viper.New()
	require.NoError(t, v.BindPFlags(flags))
	var log bytes.Buffer
	readUserConfig(v, flags, slog.New(slog.NewTextHandler(&log, nil)))
	assert.Contains(t, log.String(), "settings=[typo]")
}

// allow-update: true in the config file lets clients update every server
// of the user through its API (agentapi hub join --allow-update writes it).
func TestReadUserConfigAllowUpdate(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("allow-update: true\n"), 0o600))
	flags := pflag.NewFlagSet("server", pflag.ContinueOnError)
	flags.Bool(FlagAllowUpdate, false, "")
	flags.String(FlagConfig, "", "")
	require.NoError(t, flags.Parse([]string{"--config=" + path}))
	v := flagsWith(nil)
	require.NoError(t, v.BindPFlags(flags))

	readUserConfig(v, flags, slog.New(slog.NewTextHandler(io.Discard, nil)))
	opts, err := loadServerOptions(v, []string{"claude"})
	require.NoError(t, err)
	assert.True(t, opts.allowUpdate)
	self := &selfExec{path: "/usr/local/bin/agentapi"}
	update := serverConfig(opts, &runningAgent{}, nil, "", t.TempDir(), nil, self).Update
	assert.True(t, update.Allowed)
	assert.Equal(t, self.path, update.Program, "the program it restarts into")
	assert.Equal(t, runtime.GOOS != "windows", update.Restart != nil, "restarts in place")
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

// Printing the schema leaves a PID file alone: make gen runs it in an
// agent's shell, whose AGENTAPI_PID_FILE is the agent's server's.
func TestPrintOpenAPILeavesThePIDFile(t *testing.T) {
	isolateViper(t)
	pidFile := filepath.Join(t.TempDir(), "agentapi.pid")
	require.NoError(t, os.WriteFile(pidFile, []byte("1\n"), 0o600))
	serverCmd := CreateServerCmd()
	setupCommandOutput(t, serverCmd)
	serverCmd.SetArgs([]string{"--print-openapi", "--pid-file", pidFile, "--registry-dir", t.TempDir(), "dummy-command"})
	require.NoError(t, serverCmd.Execute())
	data, err := os.ReadFile(pidFile)
	require.NoError(t, err)
	assert.Equal(t, "1\n", string(data))
}

// A state-db or pid-file in the config file without {port} would be every
// server's, and the first one to start takes it: it is ignored, with a
// warning. A flag or a variable still names one.
func TestReadUserConfigPerServerFiles(t *testing.T) {
	for _, setting := range []struct{ name, env, file string }{
		{FlagStateDB, "AGENTAPI_STATE_DB", "~/.agentapi/state/{port}.db"},
		{FlagPidFile, "AGENTAPI_PID_FILE", "~/.agentapi/run/{port}.pid"},
	} {
		for name, tc := range map[string]struct {
			file   string
			args   []string
			env    string
			want   string
			warned bool
		}{
			"without {port}": {file: "/data/agentapi", want: "", warned: true},
			"with {port}":    {file: setting.file, want: setting.file},
			"a flag":         {file: "/data/agentapi", args: []string{"--" + setting.name + "=/data/mine"}, want: "/data/mine"},
			"a variable":     {file: "/data/agentapi", env: "/data/env", want: "/data/env"},
		} {
			t.Run(setting.name+" "+name, func(t *testing.T) {
				t.Setenv(setting.env, tc.env)
				path := filepath.Join(t.TempDir(), "config.yaml")
				require.NoError(t, os.WriteFile(path, []byte(setting.name+": "+tc.file+"\n"), 0o600))
				flags := pflag.NewFlagSet("server", pflag.ContinueOnError)
				flags.String(FlagStateDB, "", "")
				flags.String(FlagPidFile, "", "")
				flags.String(FlagConfig, "", "")
				require.NoError(t, flags.Parse(append([]string{"--config=" + path}, tc.args...)))
				v := viper.New()
				require.NoError(t, v.BindPFlags(flags))
				v.SetEnvPrefix("AGENTAPI")
				v.AutomaticEnv()
				v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
				var logs bytes.Buffer

				readUserConfig(v, flags, slog.New(slog.NewTextHandler(&logs, nil)))
				assert.Equal(t, tc.want, v.GetString(setting.name))
				assert.Equal(t, tc.warned, strings.Contains(logs.String(), "Ignoring "+setting.name+" in the AgentAPI config file"), logs.String())
			})
		}
	}
}
