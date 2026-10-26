package server

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

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
