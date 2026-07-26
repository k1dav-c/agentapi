package mcpconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mf "github.com/coder/agentapi/lib/msgfmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaudeStore(t *testing.T) {
	t.Parallel()
	workDir := t.TempDir()
	store, err := NewStore(mf.AgentTypeClaude, workDir)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(
		store.Path(),
		[]byte(`{"unrelated":true,"mcpServers":{"old":{"command":"old"}}}`),
		0o644,
	))
	require.NoError(t, store.Replace(Servers{
		"local": json.RawMessage(`{"command":"npx","args":["server"]}`),
	}))

	data, err := os.ReadFile(store.Path())
	require.NoError(t, err)
	var document map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &document))
	assert.JSONEq(t, `true`, string(document["unrelated"]))

	servers, err := store.Read()
	require.NoError(t, err)
	require.Len(t, servers, 1)
	assert.JSONEq(t, `{"command":"npx","args":["server"]}`, string(servers["local"]))
}

func TestCodexStore(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", root)
	configPath := filepath.Join(root, "config.toml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
model = "gpt-5"

[mcp_servers.old]
command = "old"

[mcp_servers.old.env]
TOKEN = "secret"

[projects."/workspace"]
trust_level = "trusted"
`), 0o644))

	store, err := NewStore(mf.AgentTypeCodex, "")
	require.NoError(t, err)
	require.NoError(t, store.Replace(Servers{
		"remote": json.RawMessage(`{"url":"https://mcp.example.com","startup_timeout_sec":20}`),
	}))

	data, err := os.ReadFile(configPath)
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, `model = "gpt-5"`)
	assert.Contains(t, content, `trust_level = "trusted"`)
	assert.NotContains(t, content, `command = "old"`)
	assert.Equal(t, 1, strings.Count(content, ">>> managed by agentapi"))

	servers, err := store.Read()
	require.NoError(t, err)
	require.Len(t, servers, 1)
	assert.JSONEq(t,
		`{"url":"https://mcp.example.com","startup_timeout_sec":20}`,
		string(servers["remote"]),
	)
}

func TestUnsupportedAgent(t *testing.T) {
	t.Parallel()
	_, err := NewStore(mf.AgentTypeGoose, "")
	assert.ErrorIs(t, err, ErrUnsupportedAgent)
	assert.False(t, SupportedAgent(mf.AgentTypeGoose))
	assert.True(t, SupportedAgent(mf.AgentTypeClaude))
	assert.True(t, SupportedAgent(mf.AgentTypeCodex))
}
