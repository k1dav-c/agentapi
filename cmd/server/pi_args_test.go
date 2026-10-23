package server

import (
	"regexp"
	"testing"

	"github.com/coder/agentapi/lib/agentlog/pilog"
	"github.com/coder/agentapi/lib/httpapi"
	"github.com/stretchr/testify/assert"
)

func TestWithPiSessionID(t *testing.T) {
	got := withPiSessionID([]string{"--model", "gpt-5.5"})
	assert.Len(t, got, 4)
	assert.Equal(t, []string{"--model", "gpt-5.5", "--session-id"}, got[:3])
	// Pi accepts letters, numbers, ".", "_" and "-", starting and ending
	// with a letter or number.
	assert.Regexp(t, regexp.MustCompile(`^agentapi-[0-9a-f]{16}$`), got[3])
	assert.NotEqual(t, got[3], withPiSessionID(nil)[1], "each start gets a new session")

	for _, args := range [][]string{
		{"--continue"},
		{"-c"},
		{"--resume"},
		{"--session", "abc"},
		{"--session=abc"},
		{"--session-id", "mine"},
		{"--no-session"},
		{"--fork", "abc"},
		{"-p", "hi"},
		{"--mode", "rpc"},
	} {
		assert.Equal(t, args, withPiSessionID(args), "user-chosen session: %v", args)
	}
}

func TestIsPiCLI(t *testing.T) {
	assert.True(t, isPiCLI("pi"))
	assert.True(t, isPiCLI("/home/me/.local/bin/pi"))
	assert.False(t, isPiCLI("pi-wrapper"))
	assert.False(t, isPiCLI("/usr/bin/codex"))
}

func TestPiSessionOptions(t *testing.T) {
	assert.Equal(t, pilog.Session{ID: "abc", Dir: "/tmp/s"}, piSessionOptions([]string{"--session-dir=/tmp/s", "--model", "x", "--session-id", "abc"}))
	assert.Equal(t, pilog.Session{}, piSessionOptions([]string{"--continue"}))
}

func TestServerConfigPiSessionDir(t *testing.T) {
	agent := &runningAgent{}
	opts := serverOptions{agentType: AgentTypePi, transport: httpapi.TransportNative, agentArgs: []string{"--model", "x", "--session-dir=/data/pi"}}
	assert.Equal(t, "/data/pi", serverConfig(opts, agent, nil, "", "/work", nil).Native.PiSessionDir, "where history and costs read Pi's sessions")
	opts.agentArgs = []string{"--session-dir", "/data/pi"}
	assert.Equal(t, "/data/pi", serverConfig(opts, agent, nil, "", "/work", nil).Native.PiSessionDir)
	opts.agentType, opts.agentArgs = AgentTypeClaude, []string{"--session-dir", "/data/pi"}
	assert.Empty(t, serverConfig(opts, agent, nil, "", "/work", nil).Native.PiSessionDir)
}
