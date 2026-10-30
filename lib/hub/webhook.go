package hub

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/coder/agentapi/lib/instanceapi"
)

const (
	// maxWebhookBody is the largest event the hub reads.
	maxWebhookBody = 1 << 20
	// seenDeliveries is how many delivery IDs the hub remembers, to drop an
	// event sent again (a retry, or a replay of one it got).
	seenDeliveries = 4096
)

// webhookEvent is an AgentAPI webhook event as the hub reads it: the data
// is left as it came.
type webhookEvent struct {
	instanceapi.WebhookEvent
	Data json.RawMessage `json:"data"`
}

// deliveries remembers the IDs of the events the hub took.
type deliveries struct {
	mu    sync.Mutex
	seen  map[string]struct{}
	order []string
}

// add reports whether id is new, and remembers it.
func (d *deliveries) add(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.seen == nil {
		d.seen = map[string]struct{}{}
	}
	if _, ok := d.seen[id]; ok {
		return false
	}
	d.seen[id] = struct{}{}
	d.order = append(d.order, id)
	if len(d.order) > seenDeliveries {
		delete(d.seen, d.order[0])
		d.order = d.order[1:]
	}
	return true
}

// receiveWebhook takes an AgentAPI's webhook event (POST /hooks): its
// instance.* events add, update and stop the instance. Any other event,
// which a machine that joined with an earlier version still sends, only
// says it is alive: the summary stream says what the instance does.
func (h *Hub) receiveWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBody+1))
	if err != nil || len(body) > maxWebhookBody {
		http.Error(w, "unreadable event", http.StatusBadRequest)
		return
	}
	if err := instanceapi.Verify(h.config.Secret, r.Header, body, h.clock.Now()); err != nil {
		// A wrong --secret on the instance's side shows here.
		h.logger.Warn("Refused an event with an invalid or missing signature, or more than 10 minutes off",
			"reason", err, "from", r.RemoteAddr,
			"instance", r.Header.Get(instanceapi.HeaderInstance), "event", r.Header.Get(instanceapi.HeaderEvent))
		http.Error(w, "invalid or missing signature", http.StatusUnauthorized)
		return
	}
	var event webhookEvent
	if err := json.Unmarshal(body, &event); err != nil || event.Instance == nil || event.Instance.ID == "" {
		http.Error(w, "not an AgentAPI event", http.StatusBadRequest)
		return
	}
	if event.ID != "" && !h.deliveries.add(event.ID) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	instance := *event.Instance
	switch event.Type {
	case instanceapi.EventInstanceStarted, instanceapi.EventInstanceHeartbeat:
		url := instanceURL(instance)
		if url == "" {
			http.Error(w, "the instance names no URL or host and port", http.StatusBadRequest)
			return
		}
		// An older AgentAPI doesn't say how often it sends a heartbeat.
		var state instanceapi.WebhookInstanceState
		_ = json.Unmarshal(event.Data, &state)
		h.upsertFromWebhook(instance, url, state.HeartbeatSeconds)
	case instanceapi.EventInstanceStopped:
		h.stopped(instance.ID, instance.ServerID)
	default:
		// The instance is alive; one the hub doesn't know yet is added
		// with its next heartbeat.
		_, _, _ = h.store.update(instance.ID, false, func(i *Instance) {
			if sameRun(i.ServerID, instance.ServerID) {
				i.LastSeen = h.clock.Now()
			}
		})
	}
	w.WriteHeader(http.StatusNoContent)
}

// defaultHeartbeat is how often an AgentAPI sends instance.heartbeat
// unless told otherwise.
const defaultHeartbeat = time.Minute

// seenWithin is how recently the hub must have heard from the instance's
// current run to take it as running: as long as it takes to miss
// stoppedAfter heartbeats.
func (i Instance) seenWithin() time.Duration {
	heartbeat := defaultHeartbeat
	if i.HeartbeatSeconds > 0 {
		heartbeat = time.Duration(i.HeartbeatSeconds) * time.Second
	}
	return stoppedAfter * heartbeat
}

// sameRun reports whether an event of the run serverID is of the run the
// hub knows, current; either unknown is.
func sameRun(current, serverID string) bool {
	return current == "" || serverID == "" || current == serverID
}

// upsertFromWebhook adds or updates the instance an instance.* event
// describes, and (re)starts watching it. Another run that names another
// URL is refused while the hub follows the run it knows, which it heard
// from recently: two machines with the same name (and so the same
// instance ID) would take turns at every heartbeat.
func (h *Hub) upsertFromWebhook(instance instanceapi.WebhookInstance, url string, heartbeatSeconds int) {
	var restart bool
	now := h.clock.Now()
	updated, _, err := h.store.update(instance.ID, true, func(i *Instance) {
		if !sameRun(i.ServerID, instance.ServerID) && i.URL != url &&
			i.Connection == ConnectionConnected && now.Sub(i.LastSeen) < i.seenWithin() {
			if i.refusedRun != instance.ServerID {
				h.logger.Warn("Two AgentAPIs report the same instance ID; keeping the one the hub follows",
					"instance", instance.ID, "url", i.URL, "refused_url", url, "refused_server_id", instance.ServerID)
				i.refusedRun = instance.ServerID
			}
			return
		}
		restart = i.URL != url || i.ServerID != instance.ServerID || i.Connection == ConnectionStopped
		i.WebhookInstance = instance
		i.URL = url
		i.HeartbeatSeconds = heartbeatSeconds
		i.refusedRun = ""
		if i.Source == "" {
			i.Source = SourceWebhook
		}
		i.LastSeen = now
		if restart {
			h.logger.Info("Following an instance", "instance", instance.ID, "url", url, "server_id", instance.ServerID)
			i.Connection = ConnectionConnecting
			i.Error = ""
		}
	})
	if err != nil {
		h.logger.Warn("Failed to keep the hub's instances", "error", err)
	}
	if restart {
		h.watch(updated)
	}
}

// stopped marks an instance stopped and stops watching it, unless a newer
// run of it (another server ID) already started.
func (h *Hub) stopped(id, serverID string) {
	stopped := false
	_, _, err := h.store.update(id, false, func(i *Instance) {
		if !sameRun(i.ServerID, serverID) {
			return
		}
		h.logger.Info("An instance stopped", "instance", id)
		i.Connection = ConnectionStopped
		i.Error = ""
		i.answeredBy = ""
		i.LastSeen = h.clock.Now()
		stopped = true
	})
	if err != nil {
		h.logger.Warn("Failed to keep the hub's instances", "error", err)
	}
	if stopped {
		h.unwatchStopped(id)
	}
}
