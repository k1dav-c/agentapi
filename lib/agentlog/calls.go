package agentlog

import (
	"time"

	"github.com/coder/agentapi/lib/pricing"
)

// Call is a model call as a session log records it.
type Call struct {
	Time   time.Time
	Model  string
	Tokens pricing.Tokens
	// Key identifies the call across logs: a fork's log copies the calls
	// of the session it forked. Empty when the log doesn't say.
	Key string
	// Cost is what the agent itself said the call cost (Pi), or -1.
	Cost float64
	// Report, when set, makes this not a call but a Claude Code cost-state
	// record: what Claude Code counted so far for each model of the
	// session, its subagents' included.
	Report map[string]ModelReport
}

// ModelReport is what an agent counted for one model.
type ModelReport struct {
	USD    float64
	Tokens pricing.Tokens
}

// CallReader reads the model calls a session log records, a line at a
// time; it is the reader of one log, and goes on with the lines appended
// to it later.
type CallReader interface {
	Line(line []byte)
	// Calls returns the calls read so far, which the caller may keep.
	Calls() []Call
	// Size is about how many bytes of memory the reader holds.
	Size() int
}

// CallSize is about how many bytes a Call takes, for Size.
const CallSize = 160
