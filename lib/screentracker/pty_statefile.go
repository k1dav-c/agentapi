package screentracker

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type AgentState struct {
	Version           int                   `json:"version"`
	Messages          []ConversationMessage `json:"messages"`
	InitialPrompt     string                `json:"initial_prompt"`
	InitialPromptSent bool                  `json:"initial_prompt_sent"`
}

// LoadStateStatus represents the state of loading persisted conversation state.
type LoadStateStatus int

const (
	// LoadStatePending indicates state loading has not been attempted yet.
	LoadStatePending LoadStateStatus = iota
	// LoadStateSucceeded indicates state was successfully loaded.
	LoadStateSucceeded
	// LoadStateFailed indicates state loading was attempted but failed.
	LoadStateFailed
)

func (c *PTYConversation) SaveState() error {
	c.lock.Lock()
	defer c.lock.Unlock()

	stateFile := c.cfg.StatePersistenceConfig.StateFile
	saveState := c.cfg.StatePersistenceConfig.SaveState

	if !saveState {
		c.cfg.Logger.Info("State persistence is disabled")
		return nil
	}

	// Skip if not dirty
	if !c.dirty {
		c.cfg.Logger.Info("Skipping state save: no changes since last save")
		return nil
	}

	conversation := c.messagesLocked()

	// Serialize initial prompt from message parts
	var initialPromptStr string
	if len(c.cfg.InitialPrompt) > 0 {
		initialPromptStr = buildStringFromMessageParts(c.cfg.InitialPrompt)
	}

	// Create directory if it doesn't exist
	dir := filepath.Dir(stateFile)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}

	// Use atomic write: write to temp file, then rename to target path
	tempFile := stateFile + ".tmp"
	f, err := os.OpenFile(tempFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("failed to create temp state file: %w", err)
	}

	// Clean up temp file on error (before successful rename)
	var renamed bool
	defer func() {
		if !renamed {
			if removeErr := os.Remove(tempFile); removeErr != nil && !os.IsNotExist(removeErr) {
				c.cfg.Logger.Warn("Failed to clean up temp state file", "path", tempFile, "err", removeErr)
			}
		}
	}()

	// Encode directly to file to avoid loading entire JSON into memory
	encoder := json.NewEncoder(f)
	if err := encoder.Encode(AgentState{
		Version:           1,
		Messages:          conversation,
		InitialPrompt:     initialPromptStr,
		InitialPromptSent: c.initialPromptSent,
	}); err != nil {
		_ = f.Close()
		return fmt.Errorf("failed to encode state: %w", err)
	}

	// Flush to disk before rename for crash safety
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("failed to sync state file: %w", err)
	}

	// Close file before rename
	if err := f.Close(); err != nil {
		return fmt.Errorf("failed to close temp state file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tempFile, stateFile); err != nil {
		return fmt.Errorf("failed to rename state file: %w", err)
	}
	renamed = true

	// Clear dirty flag after successful save
	c.dirty = false

	c.cfg.Logger.Info("State saved successfully", "path", stateFile)

	return nil
}

// loadStateLocked loads the state, this method assumes that caller holds the Lock.
// Returns (error, shouldEmit) where shouldEmit indicates if the error should be emitted to the user.
func (c *PTYConversation) loadStateLocked() (error, bool) {
	stateFile := c.cfg.StatePersistenceConfig.StateFile
	loadState := c.cfg.StatePersistenceConfig.LoadState

	if !loadState || c.loadStateStatus != LoadStatePending {
		return nil, false
	}

	// Check if file exists
	if _, err := os.Stat(stateFile); os.IsNotExist(err) {
		c.cfg.Logger.Info("No previous state to load (file does not exist)", "path", stateFile)
		return fmt.Errorf("No previous state to load (file does not exist)"), false
	}

	// Open state file
	f, err := os.Open(stateFile)
	if err != nil {
		return fmt.Errorf("failed to open state file: %w", err), true
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			c.cfg.Logger.Warn("Failed to close state file", "path", stateFile, "err", closeErr)
		}
	}()

	var agentState AgentState
	decoder := json.NewDecoder(f)
	if err := decoder.Decode(&agentState); err != nil {
		return fmt.Errorf("failed to unmarshal state (corrupted or invalid JSON): %w", err), true
	}

	// Validate version
	if agentState.Version != 1 {
		return fmt.Errorf("unsupported state file version %d (expected 1)", agentState.Version), true
	}

	// Handle initial prompt restoration:
	// - If a new initial prompt was provided via flags, check if it differs from the saved one.
	//   If different, mark as not sent (will be sent). If same, preserve sent status.
	// - If no new prompt provided, restore the saved prompt and its sent status.
	c.initialPromptSent = agentState.InitialPromptSent
	if len(c.cfg.InitialPrompt) > 0 {
		isDifferent := buildStringFromMessageParts(c.cfg.InitialPrompt) != agentState.InitialPrompt
		if isDifferent {
			c.initialPromptSent = false
		}
		// If same prompt, keep agentState.InitialPromptSent
	} else if agentState.InitialPrompt != "" {
		c.cfg.InitialPrompt = []MessagePart{MessagePartText{Content: agentState.InitialPrompt}}
	}

	c.messages = agentState.Messages

	c.dirty = false

	c.cfg.Logger.Info("Successfully loaded state", "path", stateFile, "messages", len(c.messages))
	return nil, false
}
