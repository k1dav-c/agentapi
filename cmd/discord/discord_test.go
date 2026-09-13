package discord

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCredentialsAreLoadedFromConfigInsteadOfEnvironment(t *testing.T) {
	for _, key := range []string{"AGENTAPI_API_TOKEN", "TEMPORAL_API_KEY", "DISCORD_BOT_TOKEN"} {
		t.Setenv(key, "private-test-credential")
	}
	command := CreateCommand()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{"--help"})
	require.NoError(t, command.Execute())
	require.NotContains(t, output.String(), "private-test-credential")
	for _, name := range []string{"agent-token", "temporal-api-key", "bot-token"} {
		value, err := command.Flags().GetString(name)
		require.NoError(t, err)
		require.Empty(t, value)
	}
}
