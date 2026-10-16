package httpapi

import (
	"fmt"
	"slices"

	"github.com/coder/agentapi/lib/statedb"
)

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
	return state
}

// restoreQueue puts the stored queue back. Called before the server runs.
func (s *Server) restoreQueue(snap statedb.Snapshot) {
	for _, q := range snap.Queue {
		s.messageQueue = append(s.messageQueue, QueuedMessage{ID: q.ID, Content: q.Content, Time: q.Time})
		s.nextQueueID = max(s.nextQueueID, q.ID)
	}
	s.nextQueueID = max(s.nextQueueID, snap.NextQueueID())
	s.savedQueue = slices.Clone(s.messageQueue)
	s.savedNextQueueID = s.nextQueueID
}

// persistQueue stores the queue if it changed since it was last stored. The
// queue changes in many places; checking on the dispatch tick and on stop
// covers them all.
func (s *Server) persistQueue() {
	if s.stateDB == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.nextQueueID == s.savedNextQueueID && slices.Equal(s.messageQueue, s.savedQueue) {
		return
	}
	stored := make([]statedb.QueuedMessage, len(s.messageQueue))
	for i, q := range s.messageQueue {
		stored[i] = statedb.QueuedMessage{ID: q.ID, Content: q.Content, Time: q.Time}
	}
	s.stateDB.PutQueue(stored, s.nextQueueID)
	s.savedQueue = slices.Clone(s.messageQueue)
	s.savedNextQueueID = s.nextQueueID
}
