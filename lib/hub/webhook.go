package hub

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/agentapi/lib/instanceapi"
)

const (
	// maxWebhookBody is the largest event the hub reads.
	maxWebhookBody = 1 << 20
	// seenDeliveries is the most delivery IDs the hub remembers, to drop
	// an event sent again (a retry, or a replay of one it got).
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

// deliveries remembers the IDs of the events the hub took, each until its
// signature no longer holds: a copy of one is refused until then. It keeps
// them in a file next to the instances', so a hub that restarts still
// refuses a copy of an event it took just before.
type deliveries struct {
	mu sync.Mutex
	// seen is when each ID's signature stops holding.
	seen  map[string]time.Time
	order []string
	// persist writes the file; nil keeps none.
	persist *persister
}

// deliveriesFile is the file of delivery IDs.
type deliveriesFile struct {
	Version    int              `json:"version"`
	Deliveries []storedDelivery `json:"deliveries"`
}

type storedDelivery struct {
	ID      string    `json:"id"`
	Expires time.Time `json:"expires"`
}

// deliveriesPath is the file of delivery IDs next to the instances' file
// statePath: instances.deliveries.json for instances.json.
func deliveriesPath(statePath string) string {
	return strings.TrimSuffix(statePath, ".json") + ".deliveries.json"
}

// newDeliveries returns the delivery IDs kept in path (empty keeps none)
// whose signature still holds at now. A file that doesn't parse is
// dropped: it holds nothing the hub can't lose but copies it would refuse.
func newDeliveries(path string, logger *slog.Logger, now time.Time) *deliveries {
	d := &deliveries{seen: map[string]time.Time{}}
	if path == "" {
		return d
	}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		logger.Warn("Failed to read the delivery IDs the hub took", "file", path, "error", err)
	}
	if err == nil {
		var file deliveriesFile
		if err := json.Unmarshal(data, &file); err != nil {
			logger.Warn("The file of delivery IDs the hub took doesn't parse; starting without them", "file", path, "error", err)
		}
		for _, delivery := range file.Deliveries {
			if _, ok := d.seen[delivery.ID]; !ok && delivery.Expires.After(now) {
				d.seen[delivery.ID] = delivery.Expires
				d.order = append(d.order, delivery.ID)
			}
		}
	}
	d.persist = newPersister(path, "delivery IDs", logger, d.snapshot)
	return d
}

// add reports whether id is new at now, and remembers it until expires.
func (d *deliveries) add(id string, expires, now time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if until, ok := d.seen[id]; ok && until.After(now) {
		return false
	}
	if _, ok := d.seen[id]; !ok {
		d.order = append(d.order, id)
	}
	d.seen[id] = expires
	// The oldest go first: once their signature no longer holds, and
	// beyond seenDeliveries.
	for len(d.order) > 0 && (len(d.order) > seenDeliveries || !d.seen[d.order[0]].After(now)) {
		delete(d.seen, d.order[0])
		d.order = d.order[1:]
	}
	if d.persist != nil {
		d.persist.mark()
	}
	return true
}

func (d *deliveries) snapshot() ([]byte, error) {
	d.mu.Lock()
	file := deliveriesFile{Version: 1, Deliveries: make([]storedDelivery, 0, len(d.order))}
	for _, id := range d.order {
		file.Deliveries = append(file.Deliveries, storedDelivery{ID: id, Expires: d.seen[id]})
	}
	d.mu.Unlock()
	return json.Marshal(file)
}

func (d *deliveries) flush() error {
	if d.persist == nil {
		return nil
	}
	return d.persist.flush()
}

func (d *deliveries) close() error {
	if d.persist == nil {
		return nil
	}
	return d.persist.close()
}

// receiveWebhook takes an AgentAPI's webhook event (POST /hooks): its
// instance.* events add, update and stop the instance. Any other event,
// which a machine that joined with an earlier version still sends, only
// says it is alive: the summary stream says what the instance does.
func (h *Hub) receiveWebhook(w http.ResponseWriter, r *http.Request) {
	event, ok := h.readEvent(w, r)
	if !ok {
		return
	}
	instance := *event.Instance
	// Verify read the timestamp; a copy is refused for as long as it
	// would pass.
	signedAt, _ := strconv.ParseInt(r.Header.Get(instanceapi.HeaderTimestamp), 10, 64)
	if !h.deliveries.add(event.ID, time.Unix(signedAt, 0).Add(instanceapi.MaxSkew), h.clock.Now()) {
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

// readEvent reads the body of a webhook delivery: whole, signed with the
// hub's secret, an AgentAPI event with an instance and a delivery ID, and
// for an instance.* event the run it is of. Otherwise it answers why not,
// and reports false.
func (h *Hub) readEvent(w http.ResponseWriter, r *http.Request) (webhookEvent, bool) {
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
		return webhookEvent{}, false
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
		return webhookEvent{}, false
	}
	var event webhookEvent
	if err := json.Unmarshal(body, &event); err != nil || event.Instance == nil || event.Instance.ID == "" {
		http.Error(w, "not an AgentAPI event", http.StatusBadRequest)
		return webhookEvent{}, false
	}
	if event.ID == "" {
		// Every AgentAPI delivery has one. Without it the hub couldn't tell
		// the event from a copy of it, which would work again for as long
		// as its signature holds.
		h.logger.Warn("Refused an event without a delivery ID", "instance", event.Instance.ID, "event", event.Type, "from", r.RemoteAddr)
		http.Error(w, "the event has no delivery ID (id): is it AgentAPI?", http.StatusBadRequest)
		return webhookEvent{}, false
	}
	isInstanceEvent := event.Type == instanceapi.EventInstanceStarted || event.Type == instanceapi.EventInstanceHeartbeat ||
		event.Type == instanceapi.EventInstanceStopped
	if isInstanceEvent && event.Instance.ServerID == "" {
		// Every AgentAPI since 1.0 names its run. An event without one
		// would pass for the run the hub follows, and move it.
		h.logger.Warn("Refused an instance event that names no server ID", "instance", event.Instance.ID, "event", event.Type, "from", r.RemoteAddr)
		http.Error(w, "the event names no server_id: is it AgentAPI 1.0 or later?", http.StatusBadRequest)
		return webhookEvent{}, false
	}
	return event, true
}

// sameRun reports whether an event of the run serverID is of the run the
// hub knows, current. An unknown current run (the hub's first sighting) is
// no conflict; an event that names no run is of none.
func sameRun(current, serverID string) bool {
	return current == "" || (serverID != "" && current == serverID)
}

// upsertFromWebhook adds or updates the instance an instance.* event
// describes, and (re)starts watching it. An event that names another URL
// moves the instance there only once the hub knows its URL is dead, and
// never one added by hand (refuseMove): the hub sends the instance's token,
// and the user's messages, where it is.
func (h *Hub) upsertFromWebhook(instance instanceapi.WebhookInstance, url string, heartbeatSeconds int) {
	var restart bool
	now := h.clock.Now()
	updated, _ := h.store.update(instance.ID, true, func(i *Instance) {
		moved := i.URL != "" && i.URL != url
		if moved {
			refuse, check := h.refuseMove(i, instance, url)
			if check {
				// Stopped, as an event said: whether its URL still
				// answers decides.
				i.Connection = ConnectionConnecting
				i.Error = ""
				restart = true
			}
			if refuse {
				return
			}
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
			i.failed = false
			i.answeredBy = ""
		}
	})
	if restart {
		h.watch(updated)
	}
}

// refuseMove reports whether an event of instance must not move i to url,
// and logs why once per run. An instance added by hand stays where it was
// added. A joined one moves only once the hub knows its URL is dead: it
// failed past the connect grace there (unreachable, or stopped after it
// missed its heartbeats), or another AgentAPI answers there. Not while the
// hub reaches it, nor while it connects (after the hub restarted, or a
// stream dropped): an event of another machine that has the secret would
// take it over, and get its token, messages and approvals. Two machines
// with the same name (the same instance ID) would also take turns at every
// heartbeat. check is set for one stopped as an event said, which is no
// sign its URL is dead: the hub tries that URL again, and the event after
// it fails there moves it.
func (h *Hub) refuseMove(i *Instance, instance instanceapi.WebhookInstance, url string) (refuse, check bool) {
	var reason string
	switch {
	case i.Source == SourceManual:
		reason = "An instance added by hand keeps its URL; refused an event that names another"
	case i.answeredBy != "" || (i.failed && (i.Connection == ConnectionUnreachable || i.Connection == ConnectionStopped)):
		return false, false
	case i.Connection == ConnectionStopped:
		check = true
		reason = "A stopped instance's event names another URL; the hub tries its URL first, and moves it if that doesn't answer"
	case i.Connection == ConnectionConnecting:
		reason = "An instance's event names another URL while the hub connects to its URL; it moves only if that doesn't answer"
	case sameRun(i.ServerID, instance.ServerID):
		reason = "An instance's event names another URL; the hub keeps following it where it reaches it"
	default:
		reason = "Two AgentAPIs report the same instance ID; keeping the one the hub follows"
	}
	if i.refusedRun != instance.ServerID || check {
		h.logger.Warn(reason, "instance", instance.ID, "url", i.URL, "refused_url", url, "refused_server_id", instance.ServerID)
		i.refusedRun = instance.ServerID
	}
	return true, check
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
