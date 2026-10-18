package screentracker

import (
	"context"
)

// caller MUST hold c.lock
func (c *PTYConversation) snapshotLocked(screen string) {
	region := c.cfg.StabilityScreen(screen)
	if c.snapshots > 0 && region == c.lastSnapshotScreen {
		c.stableCount++
	} else {
		c.stableCount = 1
	}
	c.lastSnapshotScreen = region
	c.lastScreen = screen
	if c.snapshots < c.stableSnapshotsThreshold {
		c.snapshots++
	}
	c.updateLastAgentMessageLocked(screen, c.cfg.Clock.Now())
}

// isScreenStableLocked returns true if the screen content has been stable
// for the required number of snapshots. Caller MUST hold c.lock.
// Equivalent to "buffer is full and all snapshots are identical", computed
// incrementally via stableCount instead of comparing every snapshot.
func (c *PTYConversation) isScreenStableLocked() bool {
	return c.snapshots >= c.stableSnapshotsThreshold &&
		c.stableCount >= c.stableSnapshotsThreshold
}

func (c *PTYConversation) wakeSnapshot() {
	select {
	case c.snapshotWake <- struct{}{}:
	default:
	}
}

// watchScreen uses an adaptive interval: the base SnapshotInterval (25ms)
// while the screen is settling toward stability, and a longer activeInterval
// (6× base) while the agent is actively streaming output. Once fully settled,
// it sleeps without a timer until PTY output, a queued send, or Reset wakes
// it. Buffering notifications coalesces bursts and keeps rendering bounded.
func (c *PTYConversation) watchScreen(ctx context.Context, source interface{ ScreenUpdates() <-chan struct{} }, snapshot func() error) {
	settleInterval := c.cfg.SnapshotInterval     // 25ms — for stability detection
	activeInterval := c.cfg.SnapshotInterval * 6 // 150ms — during streaming
	interval := settleInterval
	var prevScreen string

	for {
		timer := c.cfg.Clock.NewTimer(interval, "snapshot")
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		// Consume output already covered by the next snapshot. Output arriving
		// during or after the read remains pending, so no final update is lost.
		select {
		case <-source.ScreenUpdates():
		default:
		}
		select {
		case <-c.snapshotWake:
		default:
		}
		if err := snapshot(); err != nil {
			return
		}
		c.lock.Lock()
		curScreen := c.lastSnapshotScreen
		settled := c.isScreenStableLocked() && !c.sendingMessage && !c.writingMessage && c.pending == nil
		c.lock.Unlock()

		if settled {
			// Park until something happens.
			select {
			case <-ctx.Done():
				return
			case <-source.ScreenUpdates():
			case <-c.snapshotWake:
			}
			interval = settleInterval // just woke — sample fast
		} else if curScreen != prevScreen {
			// Screen is actively changing — no point sampling at full rate.
			interval = activeInterval
		} else {
			// Screen stopped changing but stableCount hasn't reached
			// threshold yet — keep the fast cadence for quick detection.
			interval = settleInterval
		}
		prevScreen = curScreen
	}
}
