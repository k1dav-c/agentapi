package codexlog

import "github.com/coder/agentapi/lib/agentlog"

// Format is Codex's session-log format.
var Format agentlog.Format = format{}

type format struct{}

func (format) Agent() string                               { return "codex" }
func (format) NewParser() agentlog.LineParser              { return NewParser() }
func (format) NewEventParser() agentlog.SessionEventParser { return NewEventParser() }
func (format) NewNamer() agentlog.SessionNamer             { return NewNamer() }
func (format) NewCalls() agentlog.CallReader               { return NewCalls() }

// recordTypes are the types of the records Codex 0.163 logs: those the
// parsers read, and those they know to skip (its world state, compactions,
// a second count of tokens).
var recordTypes = map[string]bool{
	"session_meta": true, "response_item": true, "event_msg": true, "turn_context": true,
	"compacted": true, "token_usage_record": true, "world_state": true,
	"inter_agent_communication_metadata": true, "retained_context": true,
}

// Record says what record a line holds; Codex writes its version in the
// session_meta line.
func (format) Record(line []byte) agentlog.Record {
	typ, payload, valid := agentlog.RecordOf(line, "payload")
	r := agentlog.Record{Type: typ, Known: recordTypes[typ], Valid: valid}
	if typ == "session_meta" {
		r.Version, _ = agentlog.StringField(payload, "cli_version")
	}
	return r
}
