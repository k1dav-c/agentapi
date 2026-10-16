package server

import (
	"fmt"
	"log/slog"

	"github.com/coder/agentapi/lib/native"
)

// nativeAdapterConfig is what a native adapter needs to start its agent.
type nativeAdapterConfig struct {
	// Program and Args are the agent command line given after --.
	Program string
	Args    []string
	CWD     string
	Logger  *slog.Logger
}

// nativeFactory returns the adapter factory for agents that have a native
// transport.
func nativeFactory(agentType AgentType, cfg nativeAdapterConfig) (native.Factory, error) {
	switch agentType {
	case AgentTypeClaude, AgentTypeCodex, AgentTypePi:
		return nil, fmt.Errorf("the native transport for %s is not available yet", agentType)
	case AgentTypeGoose, AgentTypeAider, AgentTypeGemini, AgentTypeCopilot, AgentTypeAmp,
		AgentTypeCursor, AgentTypeAuggie, AgentTypeAmazonQ, AgentTypeOpencode, AgentTypeKimi,
		AgentTypeCustom:
	}
	return nil, fmt.Errorf("the native transport supports claude, codex and pi, not %s", agentType)
}
