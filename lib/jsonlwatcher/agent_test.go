package jsonlwatcher

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestForAgent(t *testing.T) {
	t.Parallel()
	started := time.Unix(1700000000, 0)
	for _, agentType := range []string{"claude", "codex", "pi"} {
		agent, ok := ForAgent(agentType, 42, "/work", started)
		require.True(t, ok, agentType)
		assert.NotNil(t, agent.Resolver, agentType)
		assert.NotNil(t, agent.Parser, agentType)
		assert.NotNil(t, agent.Events, agentType)
		assert.NotNil(t, agent.Namer, agentType)
	}
	codex, _ := ForAgent("codex", 42, "/work", started)
	assert.Equal(t, &CodexResolver{PID: 42, CWD: "/work", NotBefore: started}, codex.Resolver)
	for _, agentType := range []string{"aider", "custom", ""} {
		_, ok := ForAgent(agentType, 42, "/work", started)
		assert.False(t, ok, agentType)
	}
}
