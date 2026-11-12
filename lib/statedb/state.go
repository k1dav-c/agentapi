package statedb

import (
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
const storedStateVersion = 2

// errNewerState: MetaState has a version this AgentAPI doesn't know.
var errNewerState = errors.New("a newer AgentAPI stored the conversation state")

// storedState is the JSON object under MetaState.
type storedState struct {
	Version int `json:"version"`
	agentstate.StoredState
}

// SetStoredState stores the part of the conversation's state that survives
// a restart, which replaces what was stored. It may keep state. An envelope
// this version couldn't decode is left for the version that wrote it.
func (d *DB) SetStoredState(state agentstate.StoredState) {
	data, err := encodeStoredState(state)
	if err != nil {
		d.logger.Error("Failed to encode the conversation state", "error", err)
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.preserveStoredState {
		d.enqueueLocked(metaKeyPrefix+MetaState, metaWrite(MetaState, data))
	}
}

// observeStoredState remembers whether snap's envelope must survive every
// later state change made by this older reader. Load runs before any writes.
func (d *DB) observeStoredState(snap Snapshot) {
	data, found := snap.Meta[MetaState]
	_, err := decodeStoredState(data)
	d.mu.Lock()
	d.preserveStoredState = found && err != nil
	d.mu.Unlock()
}

// encodeStoredState returns the MetaState value that stores state.
func encodeStoredState(state agentstate.StoredState) (string, error) {
	data, err := json.Marshal(storedState{Version: storedStateVersion, StoredState: state})
	return string(data), err
}

// StoredState returns the stored part of the conversation's state. A part
// that can't be read is left out, and named in the error. A database
// without MetaState has none: AgentAPI 3.7.2 and earlier kept each part
// under a key of its own, which isn't read anymore.
func (s Snapshot) StoredState() (agentstate.StoredState, error) {
	if data, ok := s.Meta[MetaState]; ok {
		return decodeStoredState(data)
	}
	return agentstate.StoredState{}, nil
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
