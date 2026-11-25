package agentio

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// TitleUpdates serializes background renames of one agent session. A newer
// request supersedes older ones that have not been sent.
type TitleUpdates struct {
	mu       sync.Mutex
	revision atomic.Uint64
}

func (u *TitleUpdates) Revision() uint64 { return u.revision.Load() }

// Invalidate drops queued renames, for example when a new session opens.
func (u *TitleUpdates) Invalidate() { u.revision.Add(1) }

func (u *TitleUpdates) Send(send func(context.Context)) {
	revision := u.revision.Add(1)
	go func() {
		u.mu.Lock()
		defer u.mu.Unlock()
		if u.Revision() != revision {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		send(ctx)
	}()
}
