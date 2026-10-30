package hub

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coder/agentapi/lib/instanceapi"
)

const (
	// watchRetryMin and watchRetryMax bound the wait before the hub opens
	// an instance's summary stream again.
	watchRetryMin = time.Second
	watchRetryMax = 30 * time.Second
	// watchIdle is how long a summary stream may stay silent: it sends a
	// heartbeat every 15 seconds.
	watchIdle = 45 * time.Second
	// stoppedAfter is how many heartbeats an instance the hub can't reach
	// may miss before it counts as stopped.
	stoppedAfter = 3
	// defaultConnectGrace is how long the hub keeps trying before it calls
	// an instance unreachable: one that is starting doesn't listen yet when
	// its first event comes, and a stream drops now and then; both connect
	// again within it.
	defaultConnectGrace = 10 * time.Second
)

// watch starts (or starts again) following the summary of instance.
func (h *Hub) watch(instance Instance) {
	h.watchMu.Lock()
	defer h.watchMu.Unlock()
	if cancel, ok := h.watchers[instance.ID]; ok {
		cancel()
	}
	if h.ctx == nil || h.ctx.Err() != nil {
		return
	}
	ctx, cancel := context.WithCancel(h.ctx)
	h.watchers[instance.ID] = cancel
	h.watchWG.Add(1)
	go func() {
		defer h.watchWG.Done()
		h.follow(ctx, instance.ID)
	}()
}

// unwatch stops following the instance id.
func (h *Hub) unwatch(id string) {
	h.watchMu.Lock()
	defer h.watchMu.Unlock()
	if cancel, ok := h.watchers[id]; ok {
		cancel()
		delete(h.watchers, id)
	}
}

// unwatchStopped stops following the instance id if it is still stopped:
// a new run of it may have started meanwhile, with a watch of its own.
func (h *Hub) unwatchStopped(id string) {
	h.watchMu.Lock()
	defer h.watchMu.Unlock()
	if current, ok := h.store.get(id); ok && current.Connection != ConnectionStopped {
		return
	}
	if cancel, ok := h.watchers[id]; ok {
		cancel()
		delete(h.watchers, id)
	}
}

// follow reads the instance's summary stream until ctx ends, opening it
// again after a failure. Within the connect grace a failure leaves the
// instance connecting (with its last summary, if it had one); it is
// unreachable once it has failed for longer, and stopped once it also
// missed its heartbeats (stoppedAfter).
func (h *Hub) follow(ctx context.Context, id string) {
	wait := watchRetryMin
	// When the failures began; zero while the stream is open.
	var failing time.Time
	for ctx.Err() == nil {
		instance, ok := h.store.get(id)
		if !ok || instance.Connection == ConnectionStopped {
			return
		}
		connected := false
		err := h.readSummaries(ctx, instance, func(summary instanceapi.SummaryBody) {
			connected = true
			failing = time.Time{}
			wait = watchRetryMin
			h.unmuteChanged(id, &summary)
			h.applySummary(ctx, id, instance.URL, summary)
		})
		if ctx.Err() != nil {
			return
		}
		message := "the stream ended"
		if err != nil {
			message = err.Error()
		}
		now := h.clock.Now()
		if failing.IsZero() {
			failing = now
		}
		grace := now.Sub(failing) < h.config.ConnectGrace
		var other *otherInstanceError
		answered := errors.As(err, &other)
		stopped := false
		_, _, _ = h.store.update(id, false, func(i *Instance) {
			if ctx.Err() != nil || i.Connection == ConnectionStopped {
				return
			}
			if !grace && i.Source == SourceWebhook && i.HeartbeatSeconds > 0 &&
				now.Sub(i.LastSeen) >= stoppedAfter*time.Duration(i.HeartbeatSeconds)*time.Second {
				// Killed without instance.stopped, such as with its
				// workspace; its next heartbeat follows it again.
				h.logger.Info("An instance stopped sending heartbeats; it counts as stopped", "instance", id, "last_seen", i.LastSeen)
				i.Connection = ConnectionStopped
				i.Error = ""
				i.answeredBy = ""
				stopped = true
				return
			}
			if answered {
				// Not a failure to wait out: the hub would show, and send
				// approvals to, another agent.
				if i.answeredBy != other.id {
					h.logger.Warn("Another AgentAPI answers at an instance's URL", "instance", id, "url", i.URL, "answers", other.id)
				}
				i.Connection = ConnectionUnreachable
				i.Error = message
				i.answeredBy = other.id
				return
			}
			i.answeredBy = ""
			if grace {
				i.Connection = ConnectionConnecting
				i.Error = message
				return
			}
			if i.Connection != ConnectionUnreachable || i.Error != message {
				h.logger.Warn("An instance is unreachable", "instance", id, "url", i.URL, "error", message)
			}
			i.Connection = ConnectionUnreachable
			i.Error = message
		})
		if stopped {
			return
		}
		if connected {
			wait = watchRetryMin
		}
		timer := h.clock.NewTimer(wait, "hub", "retry")
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		wait = min(wait*2, watchRetryMax)
	}
}

// applySummary takes a summary that the watch of url read, and reports
// whether it did. One that comes after the instance stopped, or from a URL
// it left, is from a watch that ends.
func (h *Hub) applySummary(ctx context.Context, id, url string, summary instanceapi.SummaryBody) bool {
	now := h.clock.Now()
	applied := false
	_, _, _ = h.store.update(id, false, func(i *Instance) {
		if ctx.Err() != nil || i.Connection == ConnectionStopped || i.URL != url {
			return
		}
		applied = true
		i.Summary = &summary
		i.Connection = ConnectionConnected
		i.Error = ""
		i.answeredBy = ""
		i.LastSeen = now
		// Who it is, as it says: another run of it after a restart, which
		// its next heartbeat would introduce. The hub keeps where it
		// reaches it.
		id := i.ID
		i.WebhookInstance = summary.Instance
		i.ID, i.URL = id, url
	})
	return applied
}

// otherInstanceError: another AgentAPI answers at an instance's URL.
type otherInstanceError struct{ id string }

func (e *otherInstanceError) Error() string {
	return fmt.Sprintf("another AgentAPI answers at this URL (%s)", e.id)
}

// readSummaries opens GET /events?only=summary of instance and calls got
// with every summary until the stream ends, or until one comes from
// another instance.
func (h *Hub) readSummaries(ctx context.Context, instance Instance, got func(instanceapi.SummaryBody)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, instance.URL+"/events?only=summary", nil)
	if err != nil {
		return err
	}
	if token := h.tokenFor(instance); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := h.client.Do(req)
	if err != nil {
		return fmt.Errorf("unreachable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return errors.New("the instance needs an API token (--instance-token)")
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("GET /events?only=summary: %s", resp.Status)
	case !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream"):
		return errors.New("not an event stream: is it AgentAPI 1.1 or later?")
	}

	// A stream that stays silent past its heartbeats is dead.
	idle := h.clock.AfterFunc(watchIdle, cancel, "hub", "idle")
	defer idle.Stop()
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	var event, data string
	for scanner.Scan() {
		idle.Reset(watchIdle, "hub", "idle")
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		case line == "":
			if event == "summary_update" && data != "" {
				var summary instanceapi.SummaryBody
				if err := json.Unmarshal([]byte(data), &summary); err == nil {
					if summary.Instance.ID != "" && summary.Instance.ID != instance.ID {
						// Its address went to another machine, or two
						// machines have the same name.
						return &otherInstanceError{id: summary.Instance.ID}
					}
					got(summary)
				}
			}
			event, data = "", ""
		}
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		return err
	}
	if ctx.Err() != nil && h.ctx.Err() == nil {
		return errors.New("the event stream went silent")
	}
	return nil
}

// unmuteChanged ends the instance's mute once what it waits for changed
// (it logged in, or asks something else): the next thing counts again.
func (h *Hub) unmuteChanged(id string, summary *instanceapi.SummaryBody) {
	current, ok := h.store.get(id)
	if !ok || current.Muted == "" || current.Muted == instanceapi.AttentionKey(summary) {
		return
	}
	_, _, err := h.store.update(id, false, func(i *Instance) {
		if i.Muted == current.Muted {
			i.Muted = ""
		}
	})
	if err != nil {
		h.logger.Warn("Failed to keep the hub's instances", "error", err)
	}
	h.logger.Info("An instance's mute ended: what it waits for changed", "instance", id)
}
