package sessions

import (
	"fmt"
	"sync"

	"github.com/coder/agentapi/lib/pricing"
)

// Store reads the sessions an agent had in a working directory from the
// agent's session logs, and keeps what it read for the next request: the
// logs of a long session run to hundreds of megabytes. The server holds
// one for its agent and directory. It is safe for concurrent use.
type Store struct {
	agent  string
	cwd    string
	prices *pricing.Table
	h      historyLog

	mu sync.Mutex
	// summaries and calls hold what was read from each log, by path.
	summaries map[string]cachedSummary
	calls     map[string]cachedCallList
}

// Config is what a Store needs besides the agent and its directory.
type Config struct {
	// Prices price the model calls; nil is the built-in prices.
	Prices *pricing.Table
}

// Supported reports whether a Store reads the sessions of an agent of
// agentType.
func Supported(agentType string) bool {
	_, ok := historyLogFor(agentType)
	return ok
}

// New returns a Store of the sessions an agent of agentType ("claude",
// "codex" or "pi") had in cwd.
func New(agentType, cwd string, config Config) (*Store, error) {
	h, ok := historyLogFor(agentType)
	if !ok {
		return nil, fmt.Errorf("no session history for agent %q", agentType)
	}
	prices := config.Prices
	if prices == nil {
		prices = pricing.Builtin()
	}
	return &Store{
		agent:     agentType,
		cwd:       cwd,
		prices:    prices,
		h:         h,
		summaries: make(map[string]cachedSummary),
		calls:     make(map[string]cachedCallList),
	}, nil
}
