package agentlog

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
)

// Format is an agent's session-log format: what reading its records
// takes. claudelog, codexlog and pilog each have one.
type Format interface {
	// Agent is the agent type whose logs these are.
	Agent() string
	NewParser() LineParser
	NewEventParser() SessionEventParser
	NewNamer() SessionNamer
	// NewCalls returns a reader of a log's model calls, which counts the
	// call records it can't read into drift (nil counts nothing).
	NewCalls(drift *Drift) CallReader
	// Record says what record a line holds.
	Record(line []byte) Record
}

// Record is what a log line is to its format.
type Record struct {
	// Type is the record's type; Known says the format reads records of
	// that type, or knows to skip them.
	Type  string
	Known bool
	// Valid is false for a line that isn't one whole JSON object.
	Valid bool
	// Version is the version of the agent (or of its log's format) the
	// record names, if it names one.
	Version string
}

// RecordOf returns the type of the record a line holds and whether it is
// one whole JSON object, with the value of the top-level field version if
// it has one, all in one pass over the line.
func RecordOf(line []byte, version string) (typ string, versionValue []byte, valid bool) {
	valid = Members(line, func(key, value []byte) bool {
		switch string(key) {
		case "type":
			typ, _ = Unquote(value)
		case version:
			versionValue = value
		}
		return true
	})
	return typ, versionValue, valid
}

// Drift counts what a log holds that its format doesn't know: records
// of types it doesn't read, lines that are no records at all, and records
// of model calls without the model or the usage the format gives them.
// Agents add record types and rename fields as they change, and a parser
// that falls behind loses what they hold without a word: a call would
// cost nothing. The counts tell. A Drift counts one log.
type Drift struct {
	format      Format
	unknown     map[string]int
	undecodable int
	calls       [callDrifts]int
	version     string
	// reported says what was reported: the records, and each CallDrift.
	reported      bool
	callsReported [callDrifts]bool
}

// CallDrift is what a CallReader found wrong with the record of a model
// call: the call is left out, or has no model to price it by.
type CallDrift int

const (
	// NoModelOrUsage: the record names no model, or has no usage.
	NoModelOrUsage CallDrift = iota
	// NoTokens: its usage reads as no tokens at all.
	NoTokens
	callDrifts
)

// callDriftWarnings say what each CallDrift means.
var callDriftWarnings = [callDrifts]string{
	NoModelOrUsage: "Session log has model calls without a model or usage; the agent may have changed its format, and they count no cost",
	NoTokens:       "Session log has model calls whose usage reads as no tokens; the agent may have changed its format, and they count no cost",
}

// NewDrift returns a Drift of a log of format.
func NewDrift(format Format) *Drift {
	return &Drift{format: format}
}

// Line counts a line of the log.
func (d *Drift) Line(line []byte) {
	r := d.format.Record(line)
	if r.Version != "" {
		d.version = r.Version
	}
	switch {
	case !r.Valid:
		d.undecodable++
	case !r.Known:
		if d.unknown == nil {
			d.unknown = make(map[string]int)
		}
		d.unknown[r.Type]++
	}
}

// Call counts the record of a model call that reads wrong as kind says.
// A nil Drift counts nothing.
func (d *Drift) Call(kind CallDrift) {
	if d != nil {
		d.calls[kind]++
	}
}

// Calls is how many records of model calls read wrong as kind says.
func (d *Drift) Calls(kind CallDrift) int {
	if d == nil {
		return 0
	}
	return d.calls[kind]
}

// Report logs what the log holds that its format doesn't know, at Warn
// level: each kind (the records, and each CallDrift) the first time there
// is any, once per log and not once per line. It reports whether it
// logged.
func (d *Drift) Report(logger *slog.Logger, path string) bool {
	logged := false
	if !d.reported && (len(d.unknown) > 0 || d.undecodable > 0) {
		d.reported, logged = true, true
		var unknown []string
		for _, typ := range slices.Sorted(maps.Keys(d.unknown)) {
			unknown = append(unknown, fmt.Sprintf("%q: %d", typ, d.unknown[typ]))
		}
		logger.Warn("Session log has records its parser doesn't know; the agent may have changed its format",
			"agent", d.format.Agent(), "version", d.version, "path", path,
			"unknown_record_types", strings.Join(unknown, ", "), "undecodable_lines", d.undecodable)
	}
	for kind, n := range d.calls {
		if n == 0 || d.callsReported[kind] {
			continue
		}
		d.callsReported[kind], logged = true, true
		logger.Warn(callDriftWarnings[kind], "agent", d.format.Agent(), "version", d.version, "path", path, "calls", n)
	}
	return logged
}

// Unmarshal is json.Unmarshal that keeps what it read of a value with a
// field of another type than v's: it leaves that field zero rather than
// failing, so a record isn't lost to one changed field, and its reader
// can tell what is missing (Drift).
func Unmarshal(data []byte, v any) error {
	err := json.Unmarshal(data, v)
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		return nil
	}
	return err
}
