package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsPiCLI(t *testing.T) {
	assert.True(t, isPiCLI("pi"))
	assert.True(t, isPiCLI("/home/me/.local/bin/pi"))
	assert.False(t, isPiCLI("pi-wrapper"))
	assert.False(t, isPiCLI("/usr/bin/codex"))
}

func TestPiSessionDir(t *testing.T) {
	assert.Equal(t, "/tmp/s", piSessionDir([]string{"--session-dir=/tmp/s", "--model", "x", "--session-id", "abc"}))
	assert.Empty(t, piSessionDir([]string{"--continue"}))
}

func TestServerConfigPiSessionDir(t *testing.T) {
	agent := &runningAgent{}
	opts := serverOptions{agentType: AgentTypePi, agentArgs: []string{"--model", "x", "--session-dir=/data/pi"}}
	assert.Equal(t, "/data/pi", serverConfig(opts, agent, nil, "", "/work", nil, nil).Native.PiSessionDir, "where history and costs read Pi's sessions")
	opts.agentArgs = []string{"--session-dir", "/data/pi"}
	assert.Equal(t, "/data/pi", serverConfig(opts, agent, nil, "", "/work", nil, nil).Native.PiSessionDir)
	opts.agentType, opts.agentArgs = AgentTypeClaude, []string{"--session-dir", "/data/pi"}
	assert.Empty(t, serverConfig(opts, agent, nil, "", "/work", nil, nil).Native.PiSessionDir)
}
