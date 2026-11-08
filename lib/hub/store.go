package hub

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/quartz"
	"github.com/danielgtaylor/huma/v2"

	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/util"
)

// Source says how the hub learned of an instance.
type Source string

const (
	// SourceWebhook: the instance's webhook events (agentapi hub join).
	SourceWebhook Source = "webhook"
	// SourceManual: someone added it in the hub.
	SourceManual Source = "manual"
)

var sourceValues = []Source{SourceWebhook, SourceManual}

func (s Source) Schema(r huma.Registry) *huma.Schema {
	return util.OpenAPISchema(r, "Source", sourceValues)
}

// Instance is an AgentAPI the hub watches, as GET /api/instances has it.
type Instance struct {
	Record
	Connection instanceapi.Connection   `json:"connection"`
	Error      string                   `json:"error,omitempty"`
	Summary    *instanceapi.SummaryBody `json:"summary,omitempty"`
	// State is what the hub's page shows of it, from the three above and
	// Muted.
	State instanceapi.State `json:"state"`
	// Work is how long the hub saw it working, as of when the hub sends it
	// (sent); nil inside the store.
	Work *WorkTime `json:"work,omitempty"`

	// answeredBy is the ID of another AgentAPI that answers at URL, while
	// it does: the proxy doesn't send it this instance's requests.
	answeredBy string
	// failed is whether the hub's watch failed at URL past the connect
	// grace since it last reached it there: the URL is dead, and an event
	// may move the instance (refuseMove).
	failed bool
	// confirmedURL is the URL at which the hub last read the instance's
	// summary, kept in the file: after a restart the hub follows it there
	// first, not at a URL an event moved it to that never answered.
	confirmedURL string
	// refusedRun is the server ID of the last run whose URL change the hub
	// refused, to log it once.
	refusedRun string
	// activity is the last activityKey the hub knew of it, kept in the file:
	// ChangedAt moves when that changes. Empty before the hub knew one.
	activity string
	// work is when the hub saw it working, kept in the file.
	work workLog
	// workSent is the work time the hub last told the subscribers of, to
	// tell them again once its windows moved (refreshWork).
	workSent workMinutes
	// heard, set by a change that loses its summary stream, is when the
	// stream last said anything: a span of work ends there.
	heard time.Time
}

// Record is what the hub keeps of an instance in its file: not its
// summary or connection, which the hub finds out again.
type Record struct {
	// WebhookInstance is who it is, as its events and summary say. Its ID
	// (--instance-id) is what the hub's routes use; URL is where the hub
	// reaches it.
	instanceapi.WebhookInstance
	Source Source `json:"source"`
	// LastSeen is when the hub last heard from the instance's current run:
	// its events and its summaries.
	LastSeen time.Time `json:"last_seen"`
	// ChangedAt is when the hub last saw the instance change what its row
	// shows it does (activityKey), or first saw it. Zero for one a hub
	// kept before it recorded this, until its next change.
	ChangedAt time.Time `json:"changed_at,omitzero"`
	// HeartbeatSeconds is how often the current run sends
	// instance.heartbeat, as its instance.* events say; 0 when unknown or
	// off.
	HeartbeatSeconds int `json:"heartbeat_seconds,omitempty"`
	// Group is the group someone put the instance in on the hub's page;
	// empty is none. The instance's own events never change it.
	Group string `json:"group,omitempty"`
	// Muted is what the instance waited for when someone muted it on the
	// hub's page (instanceapi.AttentionKey); empty is not muted. It ends
	// once that changes, so the next thing the instance needs counts again.
	Muted string `json:"muted,omitempty"`
	// Pin is the instance's place among those pinned to the top of the
	// hub's page, from 1; 0 is not pinned. The store numbers the pinned
	// ones 1, 2, … without gaps.
	Pin int `json:"pin,omitempty"`
	// Token, for a manual instance that has its own, is sent to it; it
	// never leaves the hub.
	Token string `json:"token,omitempty" hidden:"true"`
}

// public is the instance as the browser sees it: without its token.
func (i Instance) public() Instance {
	i.Token = ""
	return i
}

// sent is the instance as the hub sends it to the browser at now: public,
// with its work time.
func (i Instance) sent(now time.Time) Instance {
	i = i.public()
	work := i.work.at(now)
	i.Work = &work
	return i
}

// working says the hub sees the instance working: its state, while the
// hub follows its summary stream. One that is connecting again shows its
// last summary, which the hub doesn't see go on.
func (i Instance) working() bool {
	return i.State == instanceapi.StateWorking && i.Connection == instanceapi.ConnectionConnected
}

// seen is the instance as browsers see it, to tell a change they would see
// from one they wouldn't (a heartbeat): when it was last heard of shows
// only while it is offline.
func (i Instance) seen() []byte {
	i = i.public()
	if i.State != instanceapi.StateUnreachable && i.State != instanceapi.StateStopped {
		i.LastSeen = time.Time{}
	}
	data, _ := json.Marshal(i)
	return data
}

// state is what the hub's page shows of the instance.
func (i Instance) state() instanceapi.State {
	return instanceapi.StateOf(i.Connection, i.Summary, i.Muted)
}

// activityKey stands for what the instance's row shows it does, which
// ChangedAt follows: whether the hub reaches it, its state, its latest
// task starting or ending, what it waits for, its title, queue, update
// and version. Not what someone did on the hub's page (a group, a mute,
// reading a finished task), what moves on its own (spend, usage, the
// agent's latest message), a stream that drops and comes back, or when it
// was last heard of. Empty while the hub doesn't know what it does: it
// hasn't read a summary of it since the hub started. A hash, so the file
// keeps no titles or commands.
func (i Instance) activityKey() string {
	switch {
	case i.Connection == instanceapi.ConnectionStopped || i.Connection == instanceapi.ConnectionUnreachable:
		return string(i.Connection)
	case i.Summary == nil:
		return ""
	}
	s := i.Summary
	activity := struct {
		Status          instanceapi.AgentStatus    `json:"status"`
		Lifecycle       instanceapi.LifecycleState `json:"lifecycle"`
		Attention       instanceapi.Attention      `json:"attention"`
		Title           string                     `json:"title"`
		PendingRequests int                        `json:"pending_requests"`
		Request         string                     `json:"request,omitempty"`
		QueuedMessages  int                        `json:"queued_messages"`
		Task            int                        `json:"task,omitempty"`
		TaskStatus      instanceapi.TaskStatus     `json:"task_status,omitempty"`
		Login           string                     `json:"login,omitempty"`
		LimitStop       time.Time                  `json:"limit_stop,omitzero"`
		RetryWait       time.Time                  `json:"retry_wait,omitzero"`
		Update          instanceapi.UpdateState    `json:"update,omitempty"`
		UpdateError     string                     `json:"update_error,omitempty"`
		Version         string                     `json:"version,omitempty"`
	}{
		Status: s.Status, Lifecycle: s.Lifecycle, Attention: s.Attention, Title: s.Title,
		PendingRequests: s.PendingRequests, QueuedMessages: s.QueuedMessages, Version: s.Instance.Version,
	}
	if s.Request != nil {
		activity.Request = s.Request.ID
	}
	if s.Task != nil {
		activity.Task, activity.TaskStatus = s.Task.ID, s.Task.Status
	}
	if s.LoginRequired != nil {
		activity.Login = s.LoginRequired.Message
	}
	if s.LimitStop != nil {
		activity.LimitStop = s.LimitStop.StoppedAt
	}
	if s.RetryWait != nil {
		activity.RetryWait = s.RetryWait.StoppedAt
	}
	if s.Update != nil {
		activity.Update, activity.UpdateError = s.Update.State, s.Update.Error
	}
	data, _ := json.Marshal(activity)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}

// instanceURL is where the hub reaches the instance an event names: its
// public URL, else its host and port.
func instanceURL(instance instanceapi.WebhookInstance) string {
	if instance.URL != "" {
		return strings.TrimRight(instance.URL, "/")
	}
	if instance.Hostname != "" && instance.Port != 0 {
		return fmt.Sprintf("http://%s:%d", instance.Hostname, instance.Port)
	}
	return ""
}

// store holds the instances, keeps them in a file and tells subscribers
// of changes.
type store struct {
	// persist writes the file of instances; nil keeps none.
	persist *persister
	clock   quartz.Clock

	mu        sync.Mutex
	instances map[string]*Instance
	// favorites are the prompts kept on the hub, in their order; never
	// nil.
	favorites []Favorite
	subs      map[chan storeEvent]struct{}
}

// storeEvent is a change: the instance, the ID of one removed, or the
// favorites.
type storeEvent struct {
	Instance  *Instance
	Removed   string
	Favorites []Favorite
}

// storedInstance is an instance in the file.
type storedInstance struct {
	Record
	Stopped bool `json:"stopped,omitempty"`
	// Activity is its activityKey: after a restart, a summary that says
	// what the last one did isn't a change.
	Activity string `json:"activity,omitempty"`
	// ConfirmedURL is its confirmedURL. A hub before 3.0.1 ignores it.
	ConfirmedURL string `json:"confirmed_url,omitempty"`
	// Work is when the hub saw it working; the store tells a change of it
	// apart (workLog.track), so kept leaves it out.
	Work *storedWork `json:"work,omitempty"`
}

func (i Instance) stored() storedInstance {
	return storedInstance{Record: i.Record, Stopped: i.Connection == instanceapi.ConnectionStopped, Activity: i.activity, ConfirmedURL: i.confirmedURL, Work: i.work.stored()}
}

// kept is what the file keeps of the instance, but when it was last seen
// and its work, to compare.
func (i Instance) kept() storedInstance {
	stored := storedInstance{Record: i.Record, Stopped: i.Connection == instanceapi.ConnectionStopped, Activity: i.activity, ConfirmedURL: i.confirmedURL}
	stored.LastSeen = time.Time{}
	return stored
}

// newStore returns the store that keeps the instances in path (empty keeps
// none), with those it kept, on clock.
func newStore(path string, logger *slog.Logger, clock quartz.Clock) (*store, error) {
	s := &store{clock: clock, instances: map[string]*Instance{}, favorites: []Favorite{}, subs: map[chan storeEvent]struct{}{}}
	if path == "" {
		return s, nil
	}
	file, err := readState(path, logger)
	if err != nil {
		return nil, err
	}
	// A file edited by hand may have favorites the API refuses: the hub
	// keeps those it would take.
	if favorites, err := normalizeFavorites(file.Favorites); err == nil {
		s.favorites = favorites
	} else if len(file.Favorites) > 0 {
		logger.Warn("The hub's favorites in its file aren't valid; starting without them. The file as it was is in the .bak next to it", "file", path, "error", err)
	}
	now := clock.Now()
	for _, si := range file.Instances {
		connection := instanceapi.ConnectionConnecting
		if si.Stopped {
			connection = instanceapi.ConnectionStopped
		}
		i := &Instance{Record: si.Record, Connection: connection, activity: si.Activity, confirmedURL: si.ConfirmedURL, work: si.Work.log(si.LastSeen, now)}
		i.Pin = max(i.Pin, 0)
		if i.confirmedURL != "" && i.Source != SourceManual {
			// Moved by an event to a URL that never answered: the hub
			// tries where it last reached it first.
			i.URL = i.confirmedURL
		}
		i.State = i.state()
		s.instances[si.ID] = i
	}
	// A file edited by hand may have gaps or twins.
	s.numberPinsLocked()
	s.persist = newPersister(path, "instances", logger, s.snapshot)
	return s, nil
}

// snapshot is the file of the instances as they are.
func (s *store) snapshot() ([]byte, error) {
	s.mu.Lock()
	stored := make([]storedInstance, 0, len(s.instances))
	for _, i := range s.sortedLocked() {
		stored = append(stored, i.stored())
	}
	favorites := slices.Clone(s.favorites)
	s.mu.Unlock()
	return encodeState(stored, favorites)
}

// changedLocked says the file is behind; it is written soon after. Caller
// holds s.mu.
func (s *store) changedLocked() {
	if s.persist != nil {
		s.persist.mark()
	}
}

// close ends the spans of work going on, writes what the file is behind
// on, and stops writing it. The hub saw the instances work until it
// stopped; after it starts again, they count from their next summary.
func (s *store) close() error {
	s.mu.Lock()
	now := s.clock.Now()
	for _, i := range s.instances {
		if !i.work.since.IsZero() {
			i.work.end(now, now)
			s.changedLocked()
		}
	}
	s.mu.Unlock()
	if s.persist == nil {
		return nil
	}
	return s.persist.close()
}

func (s *store) sortedLocked() []*Instance {
	list := make([]*Instance, 0, len(s.instances))
	for _, i := range s.instances {
		list = append(list, i)
	}
	slices.SortFunc(list, func(a, b *Instance) int { return strings.Compare(a.ID, b.ID) })
	return list
}

// list returns copies of the instances, by ID.
func (s *store) list() []Instance {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listLocked()
}

// listLocked is list. Caller holds s.mu.
func (s *store) listLocked() []Instance {
	list := make([]Instance, 0, len(s.instances))
	for _, i := range s.sortedLocked() {
		list = append(list, *i)
	}
	return list
}

func (s *store) get(id string) (Instance, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, ok := s.instances[id]
	if !ok {
		return Instance{}, false
	}
	return *i, true
}

// pinLast, as a change sets an instance's Pin, pins it below the pinned
// ones: the store numbers it after them.
const pinLast = math.MaxInt

// update changes the instance id with change, or adds it when create is
// set, and reports whether it did. The file is written (soon after) when
// what it keeps changed; when the instance was last seen isn't worth a
// write of its own and goes with the next one. ChangedAt moves when the
// change is one its row shows of what it does (activityKey), and for a new
// instance. A change of its Pin numbers the pinned ones again. A span of
// work starts or ends when the instance starts or stops working
// (workLog.track); its ChangedAt doesn't move for that.
func (s *store) update(id string, create bool, change func(*Instance)) (Instance, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, ok := s.instances[id]
	if !ok {
		if !create {
			return Instance{}, false
		}
		i = &Instance{Connection: instanceapi.ConnectionConnecting}
		i.ID = id
		s.instances[id] = i
	}
	before, seen, pin := i.kept(), i.seen(), i.Pin
	change(i)
	var renumbered []*Instance
	if i.Pin != pin {
		renumbered = s.numberPinsLocked()
	}
	i.State = i.state()
	activity := i.activityKey()
	// One kept by a hub that didn't record changes has nothing to differ
	// from until the hub knows what it does.
	known := i.activity != "" || !i.ChangedAt.IsZero()
	now := s.clock.Now()
	if !ok || (activity != "" && activity != i.activity && known) {
		i.ChangedAt = now
	}
	if activity != "" {
		i.activity = activity
	}
	worked := i.work.track(i.working(), now, i.heard)
	i.heard = time.Time{}
	if !ok || worked || i.kept() != before {
		s.changedLocked()
	}
	notify := !ok || worked || !bytes.Equal(i.seen(), seen)
	if notify {
		i.workSent = i.work.at(now).minutes()
	}
	changed := *i
	if notify {
		s.notifyLocked(storeEvent{Instance: &changed})
	}
	s.notifyEachLocked(renumbered, i)
	return changed, true
}

// remove forgets the instance id, and its pin: the pinned ones below it
// move up.
func (s *store) remove(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, ok := s.instances[id]
	if !ok {
		return false
	}
	delete(s.instances, id)
	var renumbered []*Instance
	if i.Pin > 0 {
		renumbered = s.numberPinsLocked()
	}
	s.changedLocked()
	s.notifyLocked(storeEvent{Removed: id})
	s.notifyEachLocked(renumbered, nil)
	return true
}

// pinnedLocked returns the pinned instances from the top: by Pin, then by
// ID. Caller holds s.mu.
func (s *store) pinnedLocked() []*Instance {
	var pinned []*Instance
	for _, i := range s.instances {
		if i.Pin > 0 {
			pinned = append(pinned, i)
		}
	}
	slices.SortFunc(pinned, func(a, b *Instance) int { return cmp.Or(cmp.Compare(a.Pin, b.Pin), strings.Compare(a.ID, b.ID)) })
	return pinned
}

// numberPinsLocked numbers the pinned instances 1, 2, … in their order and
// returns those whose Pin it changed. Caller holds s.mu.
func (s *store) numberPinsLocked() []*Instance {
	var renumbered []*Instance
	for n, i := range s.pinnedLocked() {
		if i.Pin != n+1 {
			i.Pin = n + 1
			renumbered = append(renumbered, i)
		}
	}
	return renumbered
}

// errPinsDiffer: the IDs to order the pinned instances by aren't exactly
// the pinned ones, such as when one was pinned or unpinned since.
var errPinsDiffer = errors.New("list each pinned instance once, and only those")

// unknownInstanceError is an ID the hub doesn't have.
type unknownInstanceError string

func (e unknownInstanceError) Error() string { return "no such instance: " + string(e) }

// setPins orders the pinned instances as ids, from the top, and returns
// the pinned ones' IDs in their order. ids must list each pinned instance
// once and nothing else: an ID the hub doesn't have is an
// unknownInstanceError, any other difference errPinsDiffer, and neither
// changes anything.
func (s *store) setPins(ids []string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pinned := s.pinnedLocked()
	current := make([]string, len(pinned))
	for n, i := range pinned {
		current[n] = i.ID
	}
	for _, id := range ids {
		if _, ok := s.instances[id]; !ok {
			return current, unknownInstanceError(id)
		}
	}
	// The pinned IDs are unique: equal once sorted, ids lists each once.
	if !slices.Equal(slices.Sorted(slices.Values(ids)), slices.Sorted(slices.Values(current))) {
		return current, errPinsDiffer
	}
	var renumbered []*Instance
	for n, id := range ids {
		if i := s.instances[id]; i.Pin != n+1 {
			i.Pin = n + 1
			renumbered = append(renumbered, i)
		}
	}
	if len(renumbered) == 0 {
		return current, nil
	}
	s.changedLocked()
	s.notifyEachLocked(renumbered, nil)
	return slices.Clone(ids), nil
}

// refreshWork tells the subscribers of each instance whose work time, in
// whole minutes, moved since they last heard of it: a span that slides out
// of a window changes it without a change of the instance. The page adds
// the span going on itself.
func (s *store) refreshWork() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	for _, i := range s.sortedLocked() {
		minutes := i.work.at(now).minutes()
		if minutes != i.workSent {
			i.workSent = minutes
			changed := *i
			s.notifyLocked(storeEvent{Instance: &changed})
		}
	}
}

// subscribe returns the instances, the favorites and a channel of the
// changes after them.
func (s *store) subscribe() ([]Instance, []Favorite, chan storeEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := make(chan storeEvent, 256)
	s.subs[ch] = struct{}{}
	return s.listLocked(), slices.Clone(s.favorites), ch
}

func (s *store) unsubscribe(ch chan storeEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.subs[ch]; ok {
		delete(s.subs, ch)
		close(ch)
	}
}

// notifyEachLocked tells the subscribers of each instance in changed but
// except, which they hear of on its own. Caller holds s.mu.
func (s *store) notifyEachLocked(changed []*Instance, except *Instance) {
	for _, i := range changed {
		if i != except {
			instance := *i
			s.notifyLocked(storeEvent{Instance: &instance})
		}
	}
}

// notifyLocked sends event to the subscribers; one that fell behind is
// dropped, and reconnects. Caller holds s.mu.
func (s *store) notifyLocked(event storeEvent) {
	for ch := range s.subs {
		select {
		case ch <- event:
		default:
			delete(s.subs, ch)
			close(ch)
		}
	}
}
