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

	"github.com/coder/agentapi/lib/httpapi"
)

const (
	// watchRetryMin and watchRetryMax bound the wait before the hub opens
	// an instance's summary stream again.
	watchRetryMin = time.Second
	watchRetryMax = 30 * time.Second
	// watchIdle is how long a summary stream may stay silent: it sends a
	// heartbeat every 15 seconds.
	watchIdle = 45 * time.Second
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

// follow reads the instance's summary stream until ctx ends, opening it
// again after a failure. Within the connect grace a failure leaves the
// instance connecting (with its last summary, if it had one); it is
// unreachable once it has failed for longer.
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
		err := h.readSummaries(ctx, instance, func(summary httpapi.SummaryBody) {
			connected = true
			failing = time.Time{}
			wait = watchRetryMin
			_, _, _ = h.store.update(id, false, false, func(i *Instance) {
				i.Summary = &summary
				i.Connection = ConnectionConnected
				i.Error = ""
				if summary.Instance.Name != "" {
					i.Name = summary.Instance.Name
				}
			})
		})
		if ctx.Err() != nil {
			return
		}
		message := "the stream ended"
		if err != nil {
			message = err.Error()
		}
		if failing.IsZero() {
			failing = h.now()
		}
		grace := h.now().Sub(failing) < h.config.ConnectGrace
		_, _, _ = h.store.update(id, false, false, func(i *Instance) {
			if i.Connection == ConnectionStopped {
				return
			}
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
		if connected {
			wait = watchRetryMin
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, watchRetryMax)
	}
}

// readSummaries opens GET /events?only=summary of instance and calls got
// with every summary until the stream ends.
func (h *Hub) readSummaries(ctx context.Context, instance Instance, got func(httpapi.SummaryBody)) error {
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
	idle := time.AfterFunc(watchIdle, cancel)
	defer idle.Stop()
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	var event, data string
	for scanner.Scan() {
		idle.Reset(watchIdle)
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		case line == "":
			if event == "summary_update" && data != "" {
				var summary httpapi.SummaryBody
				if err := json.Unmarshal([]byte(data), &summary); err == nil {
					if instance.ServerID != "" && summary.Instance.ServerID != "" && summary.Instance.ServerID != instance.ServerID {
						// Another run of it, which its next heartbeat
						// introduces; until then the hub shows this one.
						_, _, _ = h.store.update(instance.ID, false, true, func(i *Instance) { i.ServerID = summary.Instance.ServerID })
						instance.ServerID = summary.Instance.ServerID
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
