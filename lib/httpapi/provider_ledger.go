package httpapi

import (
	"log/slog"
	"sync"

	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/providerlog"
)

// ledgerWriter appends what the native conversation reports of the
// provider its session runs on (native.Config.ProviderUsed) to the provider
// ledger, in the background and in order: the conversation reports it
// with its lock held, and the ledger waits for other processes' appends.
// A nil ledgerWriter records nothing.
type ledgerWriter struct {
	ledger *providerlog.Ledger
	agent  string
	logger *slog.Logger

	mu      sync.Mutex
	pending []providerlog.Entry
	// running says a goroutine appends pending; done is closed when it
	// stops.
	running bool
	done    chan struct{}
}

// newLedgerWriter returns the writer of ledger for an agent of agentType;
// nil without a ledger.
func newLedgerWriter(ledger *providerlog.Ledger, agentType string, logger *slog.Logger) *ledgerWriter {
	if ledger == nil {
		return nil
	}
	return &ledgerWriter{ledger: ledger, agent: agentType, logger: logger}
}

// record appends use to the ledger: the conversation's ProviderUsed.
func (w *ledgerWriter) record(use native.ProviderUse) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending = append(w.pending, providerlog.Entry{At: use.At, Agent: w.agent, Session: use.Session, Provider: use.Provider})
	if !w.running {
		w.running = true
		w.done = make(chan struct{})
		go w.write(w.done)
	}
}

// write appends the pending entries until there are none, then closes
// done.
func (w *ledgerWriter) write(done chan struct{}) {
	defer close(done)
	for {
		w.mu.Lock()
		batch := w.pending
		w.pending = nil
		if len(batch) == 0 {
			w.running = false
			w.mu.Unlock()
			return
		}
		w.mu.Unlock()
		for _, e := range batch {
			if err := w.ledger.Record(e); err != nil {
				w.logger.Warn("Failed to record the session's model provider; its calls may count as another provider's", "session", e.Session, "provider", e.Provider, "error", err)
			}
		}
	}
}

// wait returns once what was recorded so far is appended.
func (w *ledgerWriter) wait() {
	if w == nil {
		return
	}
	w.mu.Lock()
	done, running := w.done, w.running
	w.mu.Unlock()
	if running {
		<-done
	}
}
