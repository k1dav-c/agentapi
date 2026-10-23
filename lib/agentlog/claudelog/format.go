package claudelog

import "github.com/coder/agentapi/lib/agentlog"

// Format is Claude Code's session-log format.
var Format agentlog.Format = format{}

type format struct{}

func (format) Agent() string                               { return "claude" }
func (format) NewParser() agentlog.LineParser              { return NewParser() }
func (format) NewEventParser() agentlog.SessionEventParser { return NewEventParser() }
func (format) NewNamer() agentlog.SessionNamer             { return NewNamer() }
func (format) NewCalls() agentlog.CallReader               { return NewCalls() }

// recordTypes are the types of the records Claude Code 2.1 logs: those the
// parsers read, and those they know to skip (what it sent the API, its
// settings, file snapshots, links).
var recordTypes = map[string]bool{
	"user": true, "assistant": true, "system": true, "attachment": true,
	"summary": true, "ai-title": true, "custom-title": true, "last-prompt": true,
	"cost-state": true, "queue-operation": true, "mode": true, "permission-mode": true,
	"file-history-snapshot": true, "file-history-delta": true,
	"api-request": true, "api-request-blob": true, "api-request-shape": true,
	"pr-link": true, "frame-link": true, "atis-latch": true, "progress": true,
}

// Record says what record a line holds; Claude Code writes its version on
// most lines.
func (format) Record(line []byte) agentlog.Record {
	typ, version, valid := agentlog.RecordOf(line, "version")
	r := agentlog.Record{Type: typ, Known: recordTypes[typ], Valid: valid}
	r.Version, _ = agentlog.Unquote(version)
	return r
}
