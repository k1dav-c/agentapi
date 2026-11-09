package statedb

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/coder/agentapi/lib/agentstate"
)

// storedStateVersion is the version of the MetaState object this AgentAPI
// writes. Adding a field to agentstate.StoredState keeps it: an older
// AgentAPI reads what it knows. A change that would mislead an older one
// raises it; an AgentAPI that finds a higher version reads no stored state
// and leaves the object as it is. Nothing else holds the state, so a bump
// costs an older AgentAPI the stored settings, limit stop and retry wait
// (not the conversation).
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
// MetaState and deletes them (see migrateState). AgentAPI 3.7.3 to 3.15
// wrote them next to MetaState too; MetaState wins, and the open deletes
// them.
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
	data, err := encodeStoredState(state)
	if err != nil {
		d.logger.Error("Failed to encode the conversation state", "error", err)
		return
	}
	d.enqueue(metaKeyPrefix+MetaState, metaWrite(MetaState, data))
}

// encodeStoredState returns the MetaState value that stores state.
func encodeStoredState(state agentstate.StoredState) (string, error) {
	data, err := json.Marshal(storedState{Version: storedStateVersion, StoredState: state})
	return string(data), err
}

// StoredState returns the stored part of the conversation's state. A part
// that can't be read is left out, and named in the error.
func (s Snapshot) StoredState() (agentstate.StoredState, error) {
	if data, ok := s.Meta[MetaState]; ok {
		return decodeStoredState(data)
	}
	return s.legacyState()
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

// legacyState reads the old keys.
func (s Snapshot) legacyState() (agentstate.StoredState, error) {
	var state agentstate.StoredState
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
	return state, errors.Join(errs...)
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

// migrateState deletes the old keys, after it writes MetaState from them
// when it is missing (a database of AgentAPI 3.7.2 or earlier), in one
// transaction, and updates snap to match. Load calls it.
func (d *DB) migrateState(snap *Snapshot) error {
	var present []string
	for _, key := range legacyKeys {
		if _, ok := snap.Meta[key]; ok {
			present = append(present, key)
		}
	}
	if len(present) == 0 {
		return nil
	}
	data, write := "", false
	if _, ok := snap.Meta[MetaState]; !ok {
		state, err := snap.legacyState()
		if err != nil {
			d.logger.Warn("Moving the readable part of the stored conversation state", "error", err)
		}
		if data, err = encodeStoredState(state); err != nil {
			return fmt.Errorf("encode the conversation state: %w", err)
		}
		write = true
	}
	if err := d.inTx(func(tx *sql.Tx) error {
		if write {
			if err := metaWrite(MetaState, data)(tx); err != nil {
				return err
			}
		}
		for _, key := range present {
			if _, err := tx.Exec("DELETE FROM meta WHERE key = ?", key); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("store the conversation state: %w", err)
	}
	if write {
		snap.Meta[MetaState] = data
	}
	for _, key := range present {
		delete(snap.Meta, key)
	}
	return nil
}

// inTx runs fn in a transaction, which it commits when fn succeeds.
func (d *DB) inTx(fn func(*sql.Tx) error) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
