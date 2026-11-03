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
	// badSignatureLogEvery is how often the hub logs the events it refuses
	// for their signature: a sender with a wrong --secret, or one probing
	// the hub, would fill the log.
	badSignatureLogEvery = time.Minute
)

// webhookReadTimeout bounds reading an event's body: a sender that trickles
// one in doesn't hold a connection and its handler. Only /hooks has one; a
// server-wide ReadTimeout would end the browsers' event streams.
var webhookReadTimeout = 10 * time.Second

// logLimiter lets a log line through at most once per interval.
type logLimiter struct {
	mu         sync.Mutex
	next       time.Time
	suppressed int
}

// allow reports whether to log now, and how many lines it held back since
// the last.
func (l *logLimiter) allow(now time.Time, every time.Duration) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Before(l.next) {
		l.suppressed++
		return false, 0
	}
	suppressed := l.suppressed
	l.next, l.suppressed = now.Add(every), 0
	return true, suppressed
}

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
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(webhookReadTimeout))
	body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBody+1))
	if err == nil && len(body) <= maxWebhookBody {
		// Read whole. After a failure, or a body too large, the deadline
		// stays: the server would wait for the rest before it answers.
		_ = rc.SetReadDeadline(time.Time{})
	}
	if err != nil || len(body) > maxWebhookBody {
		http.Error(w, "unreadable event", http.StatusBadRequest)
		return
	}
	if err := instanceapi.Verify(h.config.Secret, r.Header, body, h.clock.Now()); err != nil {
		// A wrong --secret on the instance's side shows here.
		if ok, suppressed := h.badSignatures.allow(h.clock.Now(), badSignatureLogEvery); ok {
			h.logger.Warn("Refused an event with an invalid or missing signature, or more than 10 minutes off",
				"reason", err, "from", r.RemoteAddr,
				"instance", r.Header.Get(instanceapi.HeaderInstance), "event", r.Header.Get(instanceapi.HeaderEvent),
				"refused_since_last_logged", suppressed)
		}
		http.Error(w, "invalid or missing signature", http.StatusUnauthorized)
		return
	}
	var event webhookEvent
	if err := json.Unmarshal(body, &event); err != nil || event.Instance == nil || event.Instance.ID == "" {
		http.Error(w, "not an AgentAPI event", http.StatusBadRequest)
		return
	}
	if event.ID == "" {
		// Every AgentAPI delivery has one. Without it the hub couldn't tell
		// the event from a copy of it, which would work again for as long
		// as its signature holds.
		h.logger.Warn("Refused an event without a delivery ID", "instance", event.Instance.ID, "event", event.Type, "from", r.RemoteAddr)
		http.Error(w, "the event has no delivery ID (id): is it AgentAPI?", http.StatusBadRequest)
		return
	}
	instance := *event.Instance
	isInstanceEvent := event.Type == instanceapi.EventInstanceStarted || event.Type == instanceapi.EventInstanceHeartbeat ||
		event.Type == instanceapi.EventInstanceStopped
	if isInstanceEvent && instance.ServerID == "" {
		// Every AgentAPI since 1.0 names its run. An event without one
		// would pass for the run the hub follows, and move it.
		h.logger.Warn("Refused an instance event that names no server ID", "instance", instance.ID, "event", event.Type, "from", r.RemoteAddr)
		http.Error(w, "the event names no server_id: is it AgentAPI 1.0 or later?", http.StatusBadRequest)
		return
	}
	if !h.deliveries.add(event.ID) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
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
		_, _ = h.store.update(instance.ID, false, func(i *Instance) {
			if sameRun(i.ServerID, instance.ServerID) {
				i.LastSeen = h.clock.Now()
			}
		})
	}
	w.WriteHeader(http.StatusNoContent)
}

// sameRun reports whether an event of the run serverID is of the run the
// hub knows, current. An unknown current run (the hub's first sighting) is
// no conflict; an event that names no run is of none.
func sameRun(current, serverID string) bool {
	return current == "" || (serverID != "" && current == serverID)
}

// upsertFromWebhook adds or updates the instance an instance.* event
// describes, and (re)starts watching it. An event that names another URL
// moves the instance there only when the hub doesn't follow it now, and
// never one added by hand (refuseMove): the hub sends the instance's token,
// and the user's messages, where it is.
func (h *Hub) upsertFromWebhook(instance instanceapi.WebhookInstance, url string, heartbeatSeconds int) {
	var restart bool
	now := h.clock.Now()
	updated, _ := h.store.update(instance.ID, true, func(i *Instance) {
		moved := i.URL != "" && i.URL != url
		if moved && h.refuseMove(i, instance, url) {
			return
		}
		restart = i.URL != url || i.ServerID != instance.ServerID || i.Connection == ConnectionStopped
		if moved {
			// A token given for the old URL is for the AgentAPI there.
			i.Token = ""
		}
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
	if restart {
		h.watch(updated)
	}
}

// refuseMove reports whether an event of instance must not move i to url,
// and logs why once per run. An instance added by hand stays where it was
// added. One the hub follows stays where the hub reaches it, whatever run
// the event names: two machines with the same name (and so the same
// instance ID) would take turns at every heartbeat, and an event of
// another machine that has the secret would take it over.
func (h *Hub) refuseMove(i *Instance, instance instanceapi.WebhookInstance, url string) bool {
	var reason string
	switch {
	case i.Source == SourceManual:
		reason = "An instance added by hand keeps its URL; refused an event that names another"
	case i.Connection != ConnectionConnected:
		return false
	case sameRun(i.ServerID, instance.ServerID):
		reason = "An instance's event names another URL; the hub keeps following it where it reaches it"
	default:
		reason = "Two AgentAPIs report the same instance ID; keeping the one the hub follows"
	}
	if i.refusedRun != instance.ServerID {
		h.logger.Warn(reason, "instance", instance.ID, "url", i.URL, "refused_url", url, "refused_server_id", instance.ServerID)
		i.refusedRun = instance.ServerID
	}
	return true
}

// stopped marks an instance stopped and stops watching it, unless a newer
// run of it (another server ID) already started, or it was added by hand.
// The hub watches one added by hand directly and keeps trying it: a
// forged instance.stopped would otherwise stop it for good, since no
// heartbeat of its own follows it again.
func (h *Hub) stopped(id, serverID string) {
	stopped := false
	_, _ = h.store.update(id, false, func(i *Instance) {
		if !sameRun(i.ServerID, serverID) || i.Source == SourceManual {
			return
		}
		h.logger.Info("An instance stopped", "instance", id)
		i.Connection = ConnectionStopped
		i.Error = ""
		i.answeredBy = ""
		i.LastSeen = h.clock.Now()
		stopped = true
	})
	if stopped {
		h.unwatchStopped(id)
	}
}
