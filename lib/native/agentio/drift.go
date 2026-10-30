package agentio

import (
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
)

// Decode is json.Unmarshal that keeps what it could read when a field has
// an unexpected type: Unmarshal fills the other fields and reports only the
// first mismatch, which Decode returns as changed instead of failing. A
// record isn't lost to one field that changed shape: a lost turn end would
// never end the turn, a lost request never be answered. err is for data
// that isn't JSON, or a value that doesn't fit v at all.
func Decode(data []byte, v any) (changed *json.UnmarshalTypeError, err error) {
	err = json.Unmarshal(data, v)
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) && typeErr.Field != "" {
		return typeErr, nil
	}
	return nil, err
}

// Drift counts what an agent sends that its adapter doesn't read: records
// of types it doesn't know, fields whose values changed type, and records
// it can't decode at all. Agents change their protocols from version to
// version, and an adapter that falls behind loses what they send without a
// word. The first of each kind is logged at Warn, with the agent's version
// once the handshake told it; the rest at Debug. A Drift counts one
// process. Its methods may be called on a nil Drift, which counts nothing.
type Drift struct {
	agent  string
	logger *slog.Logger

	mu      sync.Mutex
	version string
	counts  map[string]int
}

// NewDrift returns a Drift for the agent named agent ("Claude Code").
func NewDrift(agent string, logger *slog.Logger) *Drift {
	return &Drift{agent: agent, logger: logger}
}

// SetVersion records the agent's version, from its handshake.
func (d *Drift) SetVersion(version string) {
	if d == nil || version == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.version = version
}

// Unknown counts a record of type typ, which the adapter doesn't know.
func (d *Drift) Unknown(typ string) {
	d.report("unknown "+typ, " sent a record of a type AgentAPI doesn't know; the agent may have changed its protocol",
		"type", typ)
}

// Changed counts a record of type typ with a field of another type than
// the adapter expects, which Decode left out. changed may be nil.
func (d *Drift) Changed(typ string, changed *json.UnmarshalTypeError) {
	if changed == nil {
		return
	}
	d.report("changed "+typ+" "+changed.Field, " sent a field of another type than AgentAPI expects; the rest of the record is read",
		"type", typ, "field", changed.Field, "value", changed.Value)
}

// Undecodable counts a record of type typ that doesn't decode at all, so
// the adapter ignores it.
func (d *Drift) Undecodable(typ string, err error) {
	d.report("undecodable "+typ, " sent a record AgentAPI can't decode; it is ignored",
		"type", typ, "error", err)
}

// Decode decodes data, a record of type typ, into v with Decode, and
// counts a field of another type or a record that doesn't decode.
func (d *Drift) Decode(typ string, data []byte, v any) error {
	changed, err := Decode(data, v)
	if err != nil {
		d.Undecodable(typ, err)
		return err
	}
	d.Changed(typ, changed)
	return nil
}

// Counts returns how often each kind of drift was seen, by kind: "unknown
// <type>", "changed <type> <field>" or "undecodable <type>".
func (d *Drift) Counts() map[string]int {
	out := map[string]int{}
	if d == nil {
		return out
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for k, n := range d.counts {
		out[k] = n
	}
	return out
}

func (d *Drift) report(kind, msg string, args ...any) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.counts == nil {
		d.counts = make(map[string]int)
	}
	d.counts[kind]++
	args = append(args, "version", d.version)
	if d.counts[kind] == 1 {
		d.logger.Warn(d.agent+msg, args...)
		return
	}
	d.logger.Debug(d.agent+msg, args...)
}
