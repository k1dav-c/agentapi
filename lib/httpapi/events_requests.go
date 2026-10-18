package httpapi

import (
	"slices"

	"github.com/coder/agentapi/lib/native"
)

// EmitRequest stores a structured request's latest state and broadcasts it.
// A change in the number of pending requests is also a status change.
func (e *EventEmitter) EmitRequest(state native.RequestState) {
	e.mu.Lock()
	defer e.mu.Unlock()
	before := e.statusChangeBody().PendingRequests
	idx := slices.IndexFunc(e.requests, func(r native.RequestState) bool { return r.ID == state.ID })
	if idx < 0 && state.Status != native.RequestStatusPending {
		// A request closed after a reset belongs to the conversation that
		// was cleared (the restarted agent's requests expire after
		// DELETE /messages cleared them), not to this one.
		return
	}
	if idx < 0 {
		e.requests = append(e.requests, state)
		e.requestSeq = append(e.requestSeq, 0)
		idx = len(e.requests) - 1
	} else {
		e.requests[idx] = state
	}
	e.seq++
	e.requestSeq[idx] = e.seq
	if e.persister != nil {
		e.persister.PutRequest(state, e.seq)
		e.persister.SetSeq(e.epoch, e.seq)
	}
	e.trimRequestsLocked()
	e.handoffRevision++
	e.notifyChannels(EventTypeRequestUpdate, RequestUpdateBody{RequestState: state, Seq: e.seq})
	if e.statusChangeBody().PendingRequests != before {
		e.notifyChannels(EventTypeStatusChange, e.statusChangeBody())
	}
}

// trimRequestsLocked drops the closed requests beyond the
// native.MaxClosedRequests the conversation keeps too: those that closed
// first. Caller holds e.mu.
func (e *EventEmitter) trimRequestsLocked() {
	drop := native.ExcessClosed(len(e.requests), func(i int) *native.RequestState { return &e.requests[i] })
	for n, i := range drop {
		// The n dropped before were in front of it.
		i -= n
		if e.persister != nil {
			e.persister.DeleteRequest(e.requests[i].ID)
		}
		e.requests = slices.Delete(e.requests, i, i+1)
		e.requestSeq = slices.Delete(e.requestSeq, i, i+1)
	}
}

// Requests returns the stored structured requests, oldest first.
func (e *EventEmitter) Requests() []native.RequestState {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.requests)
}

// Request returns the stored request with the given ID.
func (e *EventEmitter) Request(id string) (native.RequestState, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if i := slices.IndexFunc(e.requests, func(r native.RequestState) bool { return r.ID == id }); i >= 0 {
		return e.requests[i], true
	}
	return native.RequestState{}, false
}
