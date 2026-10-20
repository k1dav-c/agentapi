package server

import (
	"fmt"
	"log/slog"

	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/native/claude"
	"github.com/coder/agentapi/lib/native/codex"
	"github.com/coder/agentapi/lib/native/demo"
	nativepi "github.com/coder/agentapi/lib/native/pi"
)

// nativeAdapterConfig is what a native adapter needs to start its agent.
type nativeAdapterConfig struct {
	// Program and Args are the agent command line given after --.
	Program string
	Args    []string
	CWD     string
	Logger  *slog.Logger
}

// hasNativeAdapter reports whether the native transport can drive the agent:
// Claude Code, Codex, Pi and the demo agent.
func hasNativeAdapter(agentType AgentType, program string) bool {
	switch agentType {
	case AgentTypeClaude, AgentTypeCodex, AgentTypePi:
		return true
	case AgentTypeCustom:
		return program == demo.Program
	case AgentTypeGoose, AgentTypeAider, AgentTypeGemini, AgentTypeCopilot, AgentTypeAmp,
		AgentTypeCursor, AgentTypeAuggie, AgentTypeAmazonQ, AgentTypeOpencode, AgentTypeKimi:
	}
	return false
}

// nativeFactory returns the adapter factory for agents that have a native
// transport.
func nativeFactory(agentType AgentType, cfg nativeAdapterConfig) (native.Factory, error) {
	if cfg.Program == demo.Program && (agentType == AgentTypeClaude || agentType == AgentTypeCodex || agentType == AgentTypePi) {
		// The demo poses as the agent: AgentAPI reads that agent's session
		// logs for it (its earlier sessions and subagents).
		return demo.Factory(demo.Options{}), nil
	}
	switch agentType {
	case AgentTypeClaude:
		return claude.NewFactory(claude.Config{
			Program: cfg.Program,
			Args:    cfg.Args,
			CWD:     cfg.CWD,
			Logger:  cfg.Logger,
		})
	case AgentTypeCodex:
		return codex.NewFactory(codex.Config{
			Program: cfg.Program,
			Args:    cfg.Args,
			CWD:     cfg.CWD,
			Logger:  cfg.Logger,
		})
	case AgentTypePi:
		return nativepi.NewFactory(nativepi.Config{
			Program: cfg.Program,
			Args:    cfg.Args,
			CWD:     cfg.CWD,
			Logger:  cfg.Logger,
		})
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
