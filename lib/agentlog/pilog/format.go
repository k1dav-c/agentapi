package pilog

import "github.com/coder/agentapi/lib/agentlog"

// Format is Pi's session-log format.
var Format agentlog.Format = format{}

type format struct{}

func (format) Agent() string                               { return "pi" }
func (format) NewParser() agentlog.LineParser              { return NewParser() }
func (format) NewEventParser() agentlog.SessionEventParser { return NewEventParser() }
func (format) NewNamer() agentlog.SessionNamer             { return NewNamer() }
func (format) NewCalls() agentlog.CallReader               { return NewCalls() }

// recordTypes are the types of the entries Pi 1.3 logs: those the parsers
// read, and those they know to skip (model and settings changes,
// compactions, branches, extensions' entries).
var recordTypes = map[string]bool{
	"session": true, "message": true, "model_change": true, "thinking_level_change": true,
	"session_info": true, "compaction": true, "branch_summary": true, "custom": true,
	"custom_message": true, "label": true, "context_edit": true, "usage": true,
}

// Record says what record a line holds. Pi writes no version of its own;
// the session header says the version of its log's format.
func (format) Record(line []byte) agentlog.Record {
	typ, version, valid := agentlog.RecordOf(line, "version")
	r := agentlog.Record{Type: typ, Known: recordTypes[typ], Valid: valid}
	if typ == "session" && len(version) > 0 {
		r.Version = "session format " + string(version)
	}
	return r
}
