package httpapi

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPiUsageLogin(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	write := func(t *testing.T, auth, settings string) string {
		dir := t.TempDir()
		if auth != "" {
			require.NoError(t, os.WriteFile(filepath.Join(dir, "auth.json"), []byte(auth), 0o600))
		}
		if settings != "" {
			require.NoError(t, os.WriteFile(filepath.Join(dir, "settings.json"), []byte(settings), 0o600))
		}
		return dir
	}

	t.Run("ChatGPT login", func(t *testing.T) {
		dir := write(t, `{"openai-codex":{"type":"oauth","access":"tok-chatgpt","refresh":"r","expires":1}}`, `{"defaultProvider":"openai-codex"}`)
		provider, token, err := piUsageLogin(dir)
		require.NoError(t, err)
		assert.Equal(t, "openai", provider)
		assert.Equal(t, "tok-chatgpt", token)
	})

	t.Run("default provider wins", func(t *testing.T) {
		dir := write(t, `{"openai-codex":{"type":"oauth","access":"tok-chatgpt"},"anthropic":{"type":"oauth","access":"tok-claude"}}`, `{"defaultProvider":"anthropic"}`)
		provider, token, err := piUsageLogin(dir)
		require.NoError(t, err)
		assert.Equal(t, "anthropic", provider)
		assert.Equal(t, "tok-claude", token)
	})

	t.Run("default provider AgentAPI can't query falls back to a login it can", func(t *testing.T) {
		dir := write(t, `{"openai-codex":{"type":"oauth","access":"tok-chatgpt"}}`, `{"defaultProvider":"openrouter"}`)
		provider, _, err := piUsageLogin(dir)
		require.NoError(t, err)
		assert.Equal(t, "openai", provider)
	})

	t.Run("API key commands are only for Pi to run", func(t *testing.T) {
		dir := write(t, `{"openai":{"type":"api_key","key":"!pass show openai"}}`, "")
		_, _, err := piUsageLogin(dir)
		assert.Error(t, err)

		t.Setenv("OPENAI_API_KEY", "sk-env")
		provider, token, err := piUsageLogin(dir)
		require.NoError(t, err)
		assert.Equal(t, "openai", provider)
		assert.Equal(t, "sk-env", token)
	})

	t.Run("not logged in", func(t *testing.T) {
		_, _, err := piUsageLogin(write(t, `{}`, ""))
		assert.Error(t, err)
	})
}
