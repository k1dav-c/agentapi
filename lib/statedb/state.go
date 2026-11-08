package statedb

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/coder/agentapi/lib/agentstate"
)

// storedStateVersion is the version of the MetaState object this AgentAPI
// writes. Adding a field to agentstate.StoredState keeps it: an older
// AgentAPI reads what it knows. A change that would mislead an older one
// raises it, and an AgentAPI that finds a higher version reads the old keys
// instead (legacyKeys).
const storedStateVersion = 1

// errNewerState: MetaState has a version this AgentAPI doesn't know.
var errNewerState = errors.New("a newer AgentAPI stored the conversation state")

// storedState is the JSON object under MetaState.
type storedState struct {
	Version int `json:"version"`
	agentstate.StoredState
}

// Up to AgentAPI 3.7.2 each part of the stored state had a key of its own:
// the settings chosen (JSON, always set), the limit stop and the retry wait
// (JSON, empty for none). The first open of such a database moves them into
// MetaState (see migrateState). For one release they are written too, so an
// older AgentAPI opened on the file still finds them; a later release stops
// writing them.
const (
	legacySettings  = "settings"
	legacyLimitStop = "limit_stop"
	legacyRetryWait = "retry_wait"
)

// legacyKeys are the keys MetaState replaces.
var legacyKeys = []string{legacySettings, legacyLimitStop, legacyRetryWait}

// SetStoredState stores the part of the conversation's state that survives
// a restart, which replaces what was stored. It may keep state.
func (d *DB) SetStoredState(state agentstate.StoredState) {
	values, err := storedStateValues(state)
	if err != nil {
		d.logger.Error("Failed to encode the conversation state", "error", err)
		return
	}
	// One write, so the object and the old keys never disagree on disk.
	d.enqueue(metaKeyPrefix+MetaState, func(tx *sql.Tx) error {
		for _, kv := range values {
			if err := metaWrite(kv[0], kv[1])(tx); err != nil {
				return err
			}
		}
		return nil
	})
}

// storedStateValues returns the meta values that store state: MetaState
// and the old keys.
func storedStateValues(state agentstate.StoredState) ([][2]string, error) {
	data, err := json.Marshal(storedState{Version: storedStateVersion, StoredState: state})
	if err != nil {
		return nil, err
	}
	settings, err := json.Marshal(state.Settings)
	if err != nil {
		return nil, err
	}
	values := [][2]string{{MetaState, string(data)}, {legacySettings, string(settings)}}
	for _, kv := range []struct {
		key string
		v   any
		set bool
	}{
		{legacyLimitStop, state.LimitStop, state.LimitStop != nil},
		{legacyRetryWait, state.RetryWait, state.RetryWait != nil},
	} {
		value := ""
		if kv.set {
			encoded, err := json.Marshal(kv.v)
			if err != nil {
				return nil, err
			}
			value = string(encoded)
		}
		values = append(values, [2]string{kv.key, value})
	}
	return values, nil
}

// StoredState returns the stored part of the conversation's state. A part
// that can't be read is left out, and named in the error.
func (s Snapshot) StoredState() (agentstate.StoredState, error) {
	state, _, err := s.resolveState()
	return state, err
}

// resolveState returns the stored state and whether it differs from what
// MetaState holds: MetaState is missing (a database of AgentAPI 3.7.2 or
// earlier), or an older AgentAPI changed the old keys since it was written,
// or MetaState can't be read and the old keys can.
func (s Snapshot) resolveState() (agentstate.StoredState, bool, error) {
	legacy, hasLegacy, legacyErr := s.legacyState()
	data, hasObject := s.Meta[MetaState]
	if !hasObject {
		return legacy, hasLegacy, legacyErr
	}
	object, err := decodeStoredState(data)
	switch {
	case errors.Is(err, errNewerState):
		// The old keys, which the newer AgentAPI writes too while it does,
		// are read; its object is left as it is.
		return legacy, false, errors.Join(err, legacyErr)
	case err != nil && !hasLegacy:
		return agentstate.StoredState{}, false, err
	case err != nil:
		return legacy, true, errors.Join(err, legacyErr)
	case !hasLegacy, legacyErr != nil, sameStoredState(object, legacy):
		return object, false, nil
	}
	// An AgentAPI that doesn't know MetaState wrote the old keys last.
	return legacy, true, nil
}

// decodeStoredState decodes a MetaState value.
func decodeStoredState(data string) (agentstate.StoredState, error) {
	var stored storedState
	if err := json.Unmarshal([]byte(data), &stored); err != nil {
		return agentstate.StoredState{}, fmt.Errorf("decode the stored conversation state: %w", err)
	}
	if stored.Version > storedStateVersion {
		return agentstate.StoredState{}, fmt.Errorf("%w: version %d, this AgentAPI knows up to %d", errNewerState, stored.Version, storedStateVersion)
	}
	return stored.StoredState, nil
}

// legacyState reads the old keys, and reports whether any is set.
func (s Snapshot) legacyState() (agentstate.StoredState, bool, error) {
	var state agentstate.StoredState
	has := false
	for _, key := range legacyKeys {
		if _, ok := s.Meta[key]; ok {
			has = true
		}
	}
	var errs []error
	if data := s.Meta[legacySettings]; data != "" {
		if err := json.Unmarshal([]byte(data), &state.Settings); err != nil {
			state.Settings = agentstate.Settings{}
			errs = append(errs, fmt.Errorf("decode the stored settings: %w", err))
		}
	}
	var err error
	if state.LimitStop, err = legacyJSON[agentstate.LimitStop](s, legacyLimitStop, "limit stop"); err != nil {
		errs = append(errs, err)
	}
	if state.RetryWait, err = legacyJSON[agentstate.RetryWait](s, legacyRetryWait, "retry wait"); err != nil {
		errs = append(errs, err)
	}
	return state, has, errors.Join(errs...)
}

// legacyJSON decodes the JSON stored under key, or returns nil when there
// is none. what names it in errors.
func legacyJSON[T any](s Snapshot, key, what string) (*T, error) {
	data := s.Meta[key]
	if data == "" {
		return nil, nil
	}
	var v T
	if err := json.Unmarshal([]byte(data), &v); err != nil {
		return nil, fmt.Errorf("decode the stored %s: %w", what, err)
	}
	return &v, nil
}

// sameStoredState reports whether a and b store the same: their JSON is,
// which time zones read back differently don't change.
func sameStoredState(a, b agentstate.StoredState) bool {
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(ja, jb)
}

// migrateState writes MetaState from the old keys when it is missing, or
// when an older AgentAPI changed them since (see resolveState), and puts it
// in snap. Load calls it.
func (d *DB) migrateState(snap *Snapshot) error {
	state, changed, err := snap.resolveState()
	if !changed {
		return nil
	}
	if err != nil {
		d.logger.Warn("Moving the readable part of the stored conversation state", "error", err)
	}
	values, err := storedStateValues(state)
	if err != nil {
		return fmt.Errorf("encode the conversation state: %w", err)
	}
	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("store the conversation state: %w", err)
	}
	for _, kv := range values {
		if err := metaWrite(kv[0], kv[1])(tx); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("store the conversation state: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store the conversation state: %w", err)
	}
	for _, kv := range values {
		snap.Meta[kv[0]] = kv[1]
	}
	return nil
}
