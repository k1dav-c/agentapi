package httpapi

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"time"

	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/convstate"
	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/statedb"
)

// restoredState is what the state database gave back on start.
type restoredState struct {
	snapshot statedb.Snapshot
	// messages continue the restored conversation, whose session the
	// agent resumes.
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
	if snap.Meta[statedb.MetaTransport] == transportPTY {
		// AgentAPI 2 kept it for the agent's terminal UI, which has no
		// session to resume: the agent starts a new one below it, and the
		// conversation goes on natively from now on.
		logger.Info("The state database holds a conversation of the PTY transport; the agent starts a new session below it")
		db.SetMeta(statedb.MetaTransport, string(instanceapi.TransportNative))
	}
	db.OnWriteError(func(err error) {
		emitter.EmitError(fmt.Sprintf("Failed to save the conversation in the state database; the latest changes won't survive a restart: %v", err), conv.ErrorLevelError)
	})
	if snap.Empty() {
		db.SetMeta(statedb.MetaAgentType, string(config.AgentType))
		db.SetMeta(statedb.MetaTransport, string(instanceapi.TransportNative))
		db.SetMeta(statedb.MetaCWD, config.CWD)
		db.SetSeq(emitter.Epoch(), 0)
		return restoredState{snapshot: snap}, nil
	}
	state := emitterStateFromSnapshot(snap)
	if !snap.Clean {
		// The changes AgentAPI made just before it crashed may be missing,
		// and the next ones would reuse their seqs: clients that saw them
		// must not resume their cache, so the conversation gets a new epoch.
		state.Epoch = convstate.NewEpoch()
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

// transportPTY is the transport AgentAPI 2 kept in the state database of a
// PTY agent.
const transportPTY = "pty"

// checkRestoredIdentity refuses a state database written for another agent
// or directory: resuming its session would continue the wrong work. One
// AgentAPI 2 wrote for the same agent with the PTY transport is fine.
func checkRestoredIdentity(snap statedb.Snapshot, config ServerConfig) error {
	if snap.Empty() {
		return nil
	}
	if transport := snap.Meta[statedb.MetaTransport]; transport != string(instanceapi.TransportNative) && transport != transportPTY {
		return fmt.Errorf("the state database belongs to another session (%s is %q, not %q); use another --state-db or remove it", statedb.MetaTransport, transport, instanceapi.TransportNative)
	}
	for key, want := range map[string]string{
		statedb.MetaAgentType: string(config.AgentType),
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
	lastID := snap.NextQueueID()
	for i, q := range snap.Queue {
		queued[i] = QueuedMessage{ID: q.ID, Content: q.Content, Time: q.Time}
		lastID = max(lastID, q.ID)
	}
	// Stored already: the restore isn't written back.
	s.queueStoreMu.Lock()
	s.savedQueue, s.savedNextQueueID = slices.Clone(queued), lastID
	s.queueStoreMu.Unlock()
	s.queue.Restore(queued, snap.NextQueueID())
}

// storeQueue stores the queue, as of its version-th change, unless it is
// stored already or a later change was. The queue's onChange calls it
// after every change: the state database's write of the queue replaces
// the one still pending, so changes in quick succession are written once.
func (s *Server) storeQueue(queued []QueuedMessage, lastID int, version uint64) {
	if s.stateDB == nil {
		return
	}
	s.queueStoreMu.Lock()
	defer s.queueStoreMu.Unlock()
	if version < s.savedQueueVersion {
		return
	}
	s.savedQueueVersion = version
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

// persistQueue stores the queue if it changed since it was last stored:
// storeQueue does after every change, and Stop and SaveState check once
// more.
func (s *Server) persistQueue() {
	s.storeQueue(s.queue.state())
}
