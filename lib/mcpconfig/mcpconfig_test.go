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

func TestPiStore(t *testing.T) {
	agentDir := filepath.Join(t.TempDir(), "agent")
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	store, err := NewStore(mf.AgentTypePi, "")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(agentDir, "mcp.json"), store.Path())
	assert.True(t, SupportedAgent(mf.AgentTypePi))

	servers, err := store.Read()
	require.NoError(t, err)
	assert.Empty(t, servers, "no config file yet")

	// The agent dir doesn't exist yet either.
	require.NoError(t, store.Replace(Servers{
		"docs": json.RawMessage(`{"url":"https://example.com/mcp","headers":{"Authorization":"Bearer ${DOCS_TOKEN}"}}`),
	}))
	servers, err = store.Read()
	require.NoError(t, err)
	require.Len(t, servers, 1)
	assert.JSONEq(t, `{"url":"https://example.com/mcp","headers":{"Authorization":"Bearer ${DOCS_TOKEN}"}}`, string(servers["docs"]))
}

func TestCodexStoreDottedKeys(t *testing.T) {
	for name, existing := range map[string]string{
		"dotted keys":      "mcp_servers.docs.command = \"old\"\nmcp_servers.docs.env.TOKEN = \"x\"\n",
		"multi-line array": "mcp_servers.docs.args = [\n  \"a\", # [comment]\n  \"b]\",\n]\n",
		"inline table":     "mcp_servers = { docs = { command = \"old\" } }\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("CODEX_HOME", root)
			configPath := filepath.Join(root, "config.toml")
			require.NoError(t, os.WriteFile(configPath, []byte("model = \"gpt-5\"\n"+existing+"\n[projects.\"/w\"]\ntrust_level = \"trusted\"\n"), 0o600))
			store, err := NewStore(mf.AgentTypeCodex, "")
			require.NoError(t, err)
			require.NoError(t, store.Replace(Servers{"docs": json.RawMessage(`{"command":"npx","args":["x"]}`)}))

			servers, err := store.Read()
			require.NoError(t, err, "config.toml no longer parses")
			require.Len(t, servers, 1)
			assert.JSONEq(t, `{"command":"npx","args":["x"]}`, string(servers["docs"]))
			data, err := os.ReadFile(configPath)
			require.NoError(t, err)
			assert.Contains(t, string(data), `trust_level = "trusted"`)
			info, err := os.Stat(configPath)
			require.NoError(t, err)
			assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "the file keeps its mode")
		})
	}
}

func TestCodexStoreRefusesWhatItCantRewrite(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", root)
	configPath := filepath.Join(root, "config.toml")
	// A multi-line string holding a bracket: the rewrite by lines loses
	// track of the value, so it must not write.
	original := "model = \"gpt-5\"\nmcp_servers.docs.command = \"\"\"\n[x\n\"\"\"\n"
	require.NoError(t, os.WriteFile(configPath, []byte(original), 0o644))
	store, err := NewStore(mf.AgentTypeCodex, "")
	require.NoError(t, err)
	assert.Error(t, store.Replace(Servers{"docs": json.RawMessage(`{"command":"npx"}`)}))
	data, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Equal(t, original, string(data), "the file is left as it was")
}
