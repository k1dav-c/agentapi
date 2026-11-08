package server

import (
	"log/slog"

	"github.com/coder/agentapi/lib/instanceapi"
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

// nativeFactory returns the factory of the agent's adapter. native-demo, a
// simulated agent for developing clients (lib/native/demo), runs as any
// type: posing as Claude Code, Codex or Pi, AgentAPI reads that agent's
// session logs for it (its earlier sessions and subagents).
func nativeFactory(agentType instanceapi.AgentType, cfg nativeAdapterConfig) (native.Factory, error) {
	if cfg.Program == demo.Program {
		return demo.Factory(demo.Options{}), nil
	}
	switch agentType {
	case instanceapi.AgentTypeClaude:
		return claude.NewFactory(claude.Config{
			Program: cfg.Program,
			Args:    cfg.Args,
			CWD:     cfg.CWD,
			Logger:  cfg.Logger,
		})
	case instanceapi.AgentTypeCodex:
		return codex.NewFactory(codex.Config{
			Program: cfg.Program,
			Args:    cfg.Args,
			CWD:     cfg.CWD,
			Logger:  cfg.Logger,
		})
	case instanceapi.AgentTypePi:
		return nativepi.NewFactory(nativepi.Config{
			Program: cfg.Program,
			Args:    cfg.Args,
			CWD:     cfg.CWD,
			Logger:  cfg.Logger,
		})
	case instanceapi.AgentTypeCustom:
	}
	return nil, unsupportedAgent("a custom agent")
}
