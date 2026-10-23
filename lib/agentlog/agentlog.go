// Package agentlog has what reading the agents' session logs takes,
// whichever agent wrote them: reading their lines, and the interfaces the
// readers of each agent's records implement. The records themselves are
// read by claudelog (Claude Code), codexlog (Codex) and pilog (Pi).
package agentlog

import (
	"github.com/coder/agentapi/lib/transcript"
)

// LineParser parses one agent's session-log lines into rich messages.
type LineParser interface {
	// ParseLine processes a single JSONL line.
	// Returns completed (fully assembled) messages ready to emit.
	ParseLine(line []byte) (completed []transcript.RichMessage, err error)
	// FlushCompleted finalizes only pending messages that have a terminal
	// stop_reason (e.g. "end_turn", "max_tokens"). Messages still being
	// assembled (no stop_reason yet) are left in pending. This is safe to
	// call on every poll cycle without risk of splitting an in-progress turn.
	FlushCompleted() []transcript.RichMessage
	// Flush finalizes all pending messages regardless of state (e.g., on shutdown).
	Flush() []transcript.RichMessage
}

// SessionEventParser converts agent-specific JSONL records to timeline
// events.
type SessionEventParser interface {
	ParseSessionEvents(line []byte) ([]transcript.SessionEvent, error)
}

// SessionNamer follows the name an agent gives its session: Claude Code's
// generated title (or the one set with /rename), Codex's thread name, Pi's
// /name.
type SessionNamer interface {
	// Name reads one session log line and returns the session's name and
	// whether it changed.
	Name(line []byte) (string, bool)
}

// PolledSessionNamer is a SessionNamer whose name can also change outside
// the session log, so it needs checking now and then.
type PolledSessionNamer interface {
	SessionNamer
	Poll() (string, bool)
}

// SessionResolver finds the session log of a running agent process.
type SessionResolver interface {
	Resolve() (string, error)
}
