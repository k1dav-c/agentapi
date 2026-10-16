package server

import (
	"fmt"
	"log/slog"

	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/native/codex"
	"github.com/coder/agentapi/lib/native/demo"
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
	case AgentTypeCodex:
		return codex.NewFactory(codex.Config{
			Program: cfg.Program,
			Args:    cfg.Args,
			CWD:     cfg.CWD,
			Logger:  cfg.Logger,
		})
	case AgentTypeClaude, AgentTypePi:
		return nil, fmt.Errorf("the native transport for %s is not available yet", agentType)
	case AgentTypeCustom:
		// A simulated agent for developing clients; see lib/native/demo.
		if cfg.Program == demo.Program {
			return demo.Factory(demo.Options{}), nil
		}
	case AgentTypeGoose, AgentTypeAider, AgentTypeGemini, AgentTypeCopilot, AgentTypeAmp,
		AgentTypeCursor, AgentTypeAuggie, AgentTypeAmazonQ, AgentTypeOpencode, AgentTypeKimi:
	}
	return nil, fmt.Errorf("the native transport supports claude, codex and pi, not %s", agentType)
}
