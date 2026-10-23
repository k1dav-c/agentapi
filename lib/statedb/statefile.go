package statedb

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"

	conv "github.com/coder/agentapi/lib/conversation"
)

// StateFile is what --state-file kept, the PTY transport's persistence
// before the state database: the messages, written when AgentAPI stopped.
type StateFile struct {
	Version           int            `json:"version"`
	Messages          []conv.Message `json:"messages"`
	InitialPrompt     string         `json:"initial_prompt"`
	InitialPromptSent bool           `json:"initial_prompt_sent"`
}

// ErrNotEmpty is returned by ImportStateFile when the database already
// holds a conversation, which the file would overwrite.
var ErrNotEmpty = errors.New("the state database already holds a conversation")

// ImportStateFile copies the conversation of the --state-file at path into
// d, which must hold none yet, and records meta (the agent type, transport
// and working directory the conversation belongs to). It returns how many
// messages it copied, and 0 without error when there is no file.
func (d *DB) ImportStateFile(path string, meta map[string]string) (int, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read state file: %w", err)
	}
	var state StateFile
	if err := json.Unmarshal(data, &state); err != nil {
		return 0, fmt.Errorf("state file %s is not valid JSON: %w", path, err)
	}
	if state.Version != 1 {
		return 0, fmt.Errorf("state file %s has version %d; only version 1 can be imported", path, state.Version)
	}
	snap, err := d.Load()
	if err != nil {
		return 0, err
	}
	if !snap.Empty() {
		return 0, ErrNotEmpty
	}
	for key, value := range meta {
		d.SetMeta(key, value)
	}
	if state.InitialPromptSent {
		d.SetMeta(MetaInitialPrompt, state.InitialPrompt)
	}
	for i, m := range state.Messages {
		// The conversation's IDs are its positions.
		m.Id = i
		d.PutMessage(m, uint64(i+1))
	}
	d.SetSeq(newEpoch(), uint64(len(state.Messages)))
	// Load marked the file as open; it is as consistent as a cleanly
	// closed one once the import is written.
	d.SetMeta(metaClean, "1")
	d.Flush()
	return len(state.Messages), nil
}

// newEpoch returns a new conversation epoch.
func newEpoch() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
