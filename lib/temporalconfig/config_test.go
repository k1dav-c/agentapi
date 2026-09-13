package temporalconfig

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfigRoundTripAndCredentialNames(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(root)
	require.NoError(t, err)
	config := Defaults()
	config.ChannelID = "123456789012345678"
	config.AllowedUsers = []string{"987654321098765432"}
	config.AgentToken = "agent-secret"
	config.TemporalAPIKey = "temporal-secret"
	config.BotToken = "discord-secret"
	require.NoError(t, store.Write(config))
	loaded, err := store.Read()
	require.NoError(t, err)
	require.Equal(t, config, loaded)
	data, err := os.ReadFile(store.Path())
	require.NoError(t, err)
	require.Contains(t, string(data), "agent-secret")
	require.Contains(t, string(data), "temporal-secret")
	require.Contains(t, string(data), "discord-secret")
}

func TestConfigRejectsTokensInEnvironmentNameFieldsAndInvalidIDs(t *testing.T) {
	config := Defaults()
	config.BotTokenEnv = "secret-token-value"
	require.ErrorContains(t, config.Validate(), "environment variable names")
	config = Defaults()
	config.ChannelID = "not-a-snowflake"
	require.ErrorContains(t, config.Validate(), "channel_id")
}

func TestProfilesMergeAndAtomicPath(t *testing.T) {
	store, err := NewStore(t.TempDir())
	require.NoError(t, err)
	config := Defaults()
	config.ChannelID = "123456789012345678"
	require.NoError(t, store.WriteProfiles(map[string]Config{"local": config}))
	profiles, err := store.ReadProfiles()
	require.NoError(t, err)
	require.Equal(t, config, profiles["local"])
	require.Equal(t, filepath.Join(store.Root, ".agentapi", "temporal-profiles.json"), store.ProfilesPath())
}
