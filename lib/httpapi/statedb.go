package httpapi

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"time"

	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/statedb"
)

// restoredState is what the state database gave back on start.
type restoredState struct {
	snapshot statedb.Snapshot
	// messages continue the restored conversation: the native agent
	// resumes its session, a PTY agent starts anew below them.
	messages []conv.Message
	// resumeRef resumes the native agent's session.
	resumeRef string
	// settings were chosen for the session.
	settings native.Settings
	// limitStop is the turn a usage limit stopped.
	limitStop *native.LimitStop
}

// restoreState connects the state database to emitter, which stores its
// changes there: it restores the stored conversation, or records the
// identity of a new one, and reports writes that fail as agent errors. It
// does nothing without a state database.
func restoreState(config ServerConfig, db *statedb.DB, emitter *EventEmitter, logger *slog.Logger) (restoredState, error) {
	if db == nil {
		return restoredState{}, nil
	}
	snap, err := db.Load()
	if err != nil {
		return restoredState{}, fmt.Errorf("load state database: %w", err)
	}
	if err := checkRestoredIdentity(snap, config); err != nil {
		return restoredState{}, err
	}
	db.OnWriteError(func(err error) {
		emitter.EmitError(fmt.Sprintf("Failed to save the conversation in the state database; the latest changes won't survive a restart: %v", err), conv.ErrorLevelError)
	})
	if snap.Empty() {
		db.SetMeta(statedb.MetaAgentType, string(config.AgentType))
		db.SetMeta(statedb.MetaTransport, string(config.Transport))
		db.SetMeta(statedb.MetaCWD, config.CWD)
		db.SetSeq(emitter.Epoch(), 0)
		return restoredState{snapshot: snap}, nil
	}
	state := emitterStateFromSnapshot(snap)
	if !snap.Clean {
		// The changes AgentAPI made just before it crashed may be missing,
		// and the next ones would reuse their seqs: clients that saw them
		// must not resume their cache, so the conversation gets a new epoch.
		state.Epoch = newEpoch()
		db.SetSeq(state.Epoch, state.Seq)
		logger.Warn("The state database was not closed cleanly; clients reload the conversation")
	}
	emitter.Restore(state)
	// The agent that asked is gone; its requests can't be answered.
	now := config.Clock.Now()
	for _, r := range emitter.Requests() {
		if r.Status == native.RequestStatusPending {
			r.Status = native.RequestStatusExpired
			r.Capability = native.ResponseNotResumable
			r.ClosedAt = &now
			emitter.EmitRequest(r)
		}
	}
	resumeRef := snap.Meta[statedb.MetaSessionRef]
	emitter.SetAgentSessionID(resumeRef)
	settings, err := snap.Settings()
	if err != nil {
		// The agent starts with its own settings.
		logger.Warn("Ignoring the stored settings", "error", err)
	}
	limitStop, err := snap.LimitStop()
	if err != nil {
		logger.Warn("Ignoring the stored limit stop", "error", err)
	}
	emitter.SetLimitStop(limitStop)
	logger.Info("Restored conversation from the state database", "messages", len(state.Messages), "resume", resumeRef != "")
	return restoredState{snapshot: snap, messages: state.Messages, resumeRef: resumeRef, settings: settings, limitStop: limitStop}, nil
}

// checkRestoredIdentity refuses a state database written for another agent
// or directory: resuming its session would continue the wrong work.
func checkRestoredIdentity(snap statedb.Snapshot, config ServerConfig) error {
	if snap.Empty() {
		return nil
	}
	for key, want := range map[string]string{
		statedb.MetaAgentType: string(config.AgentType),
		statedb.MetaTransport: string(config.Transport),
		statedb.MetaCWD:       config.CWD,
	} {
		if got := snap.Meta[key]; got != want {
			return fmt.Errorf("the state database belongs to another session (%s is %q, not %q); use another --state-db or remove it", key, got, want)
		}
	}
	return nil
}

func emitterStateFromSnapshot(snap statedb.Snapshot) EmitterState {
	state := EmitterState{
		Epoch:         snap.Meta[statedb.MetaEpoch],
		Seq:           snap.Seq(),
		SessionEvents: snap.Events,
		SessionName:   snap.Meta[statedb.MetaSessionName],
	}
	if seen, err := time.Parse(time.RFC3339Nano, snap.Meta[statedb.MetaSeenAt]); err == nil {
		state.SeenAt = seen
	}
	for _, m := range snap.Messages {
		state.Messages = append(state.Messages, m.Message)
		state.MessageSeqs = append(state.MessageSeqs, m.Seq)
		state.Seq = max(state.Seq, m.Seq)
	}
	for _, m := range snap.RichMessages {
		state.RichMessages = append(state.RichMessages, m.Message)
		state.RichSeqs = append(state.RichSeqs, m.Seq)
		state.Seq = max(state.Seq, m.Seq)
	}
	for _, r := range snap.Requests {
		state.Requests = append(state.Requests, r.State)
		state.RequestSeqs = append(state.RequestSeqs, r.Seq)
		state.Seq = max(state.Seq, r.Seq)
	}
	for _, t := range snap.Tasks {
		var task Task
		if err := json.Unmarshal(t.Data, &task); err != nil {
			// The task is derived again from its message.
			continue
		}
		state.Tasks = append(state.Tasks, task)
		state.TaskSeqs = append(state.TaskSeqs, t.Seq)
		state.Seq = max(state.Seq, t.Seq)
	}
	return state
}

// restoreQueue puts the stored queue back. Called before the server runs.
func (s *Server) restoreQueue(snap statedb.Snapshot) {
	queued := make([]QueuedMessage, len(snap.Queue))
	for i, q := range snap.Queue {
		queued[i] = QueuedMessage{ID: q.ID, Content: q.Content, Time: q.Time}
	}
	s.queue.Restore(queued, snap.NextQueueID())
	s.savedQueue, s.savedNextQueueID = s.queue.Snapshot()
}

// persistQueue stores the queue if it changed since it was last stored. The
// queue changes in many places; checking on the dispatch tick covers them
// while the server runs, and Stop stores it once the HTTP server and the
// agent stopped, when nothing can change it anymore.
func (s *Server) persistQueue() {
	if s.stateDB == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	queued, lastID := s.queue.Snapshot()
	if lastID == s.savedNextQueueID && slices.Equal(queued, s.savedQueue) {
		return
	}
	stored := make([]statedb.QueuedMessage, len(queued))
	for i, q := range queued {
		stored[i] = statedb.QueuedMessage{ID: q.ID, Content: q.Content, Time: q.Time}
	}
	s.stateDB.PutQueue(stored, lastID)
	s.savedQueue = queued
	s.savedNextQueueID = lastID
}

// ptyInitialPrompt is the initial prompt the PTY conversation sends: none
// when the restored conversation got this prompt already, or when there is
// none. A prompt that goes out is recorded, so the next restart knows.
func ptyInitialPrompt(prompt []conv.MessagePart, config ServerConfig, db *statedb.DB, restored restoredState) []conv.MessagePart {
	if db == nil || config.InitialPrompt == "" {
		return prompt
	}
	if len(restored.messages) > 0 && restored.snapshot.Meta[statedb.MetaInitialPrompt] == config.InitialPrompt {
		return nil
	}
	// Recorded before the agent is ready for it: after a crash in between
	// it isn't sent again.
	db.SetMeta(statedb.MetaInitialPrompt, config.InitialPrompt)
	return prompt
}
