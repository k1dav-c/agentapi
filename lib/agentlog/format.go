package agentlog

import (
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
	NewCalls() CallReader
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

// Drift counts what a log holds that its format doesn't know: records of
// types it doesn't read, and lines that are no records at all. Agents add
// record types as they change, and a parser that falls behind loses what
// they hold without a word: the counts tell. A Drift counts one log.
type Drift struct {
	format      Format
	unknown     map[string]int
	undecodable int
	version     string
	reported    bool
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

// Report logs what the log holds that its format doesn't know, at Warn
// level, the first time there is any: once per log, not once per line. It
// reports whether it logged.
func (d *Drift) Report(logger *slog.Logger, path string) bool {
	if d.reported || (len(d.unknown) == 0 && d.undecodable == 0) {
		return false
	}
	d.reported = true
	var unknown []string
	for _, typ := range slices.Sorted(maps.Keys(d.unknown)) {
		unknown = append(unknown, fmt.Sprintf("%q: %d", typ, d.unknown[typ]))
	}
	logger.Warn("Session log has records its parser doesn't know; the agent may have changed its format",
		"agent", d.format.Agent(), "version", d.version, "path", path,
		"unknown_record_types", strings.Join(unknown, ", "), "undecodable_lines", d.undecodable)
	return true
}
