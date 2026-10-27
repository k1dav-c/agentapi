package hub

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/agentapi/lib/instanceapi"
)

// Source says how the hub learned of an instance.
type Source string

const (
	// SourceWebhook: the instance's webhook events (agentapi hub join).
	SourceWebhook Source = "webhook"
	// SourceManual: someone added it in the hub.
	SourceManual Source = "manual"
)

// Connection is how the hub's watch of an instance's summary goes.
type Connection = instanceapi.Connection

const (
	ConnectionConnecting  = instanceapi.ConnectionConnecting
	ConnectionConnected   = instanceapi.ConnectionConnected
	ConnectionUnreachable = instanceapi.ConnectionUnreachable
	ConnectionStopped     = instanceapi.ConnectionStopped
)

// Instance is an AgentAPI the hub watches, as GET /api/instances has it.
type Instance struct {
	record
	Connection Connection               `json:"connection"`
	Error      string                   `json:"error,omitempty"`
	Summary    *instanceapi.SummaryBody `json:"summary,omitempty"`
	// State is what the hub's page shows of it, from the three above and
	// Muted.
	State instanceapi.State `json:"state"`

	// answeredBy is the ID of another AgentAPI that answers at URL, while
	// it does: the proxy doesn't send it this instance's requests.
	answeredBy string
	// refusedRun is the server ID of the last other run whose URL change
	// the hub refused, to log it once.
	refusedRun string
}

// record is what the hub keeps of an instance in its file: not its
// summary or connection, which the hub finds out again.
type record struct {
	// WebhookInstance is who it is, as its events and summary say. Its ID
	// (--instance-id) is what the hub's routes use; URL is where the hub
	// reaches it.
	instanceapi.WebhookInstance
	Source Source `json:"source"`
	// LastSeen is when the hub last heard from the instance's current run:
	// its events and its summaries.
	LastSeen time.Time `json:"last_seen"`
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
	// Token, for a manual instance that has its own, is sent to it; it
	// never leaves the hub.
	Token string `json:"token,omitempty"`
}

// public is the instance as the browser sees it: without its token.
func (i Instance) public() Instance {
	i.Token = ""
	return i
}

// state is what the hub's page shows of the instance.
func (i Instance) state() instanceapi.State {
	return instanceapi.StateOf(i.Connection, i.Summary, i.Muted)
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
	path string // where the instances are kept; empty keeps none

	mu        sync.Mutex
	instances map[string]*Instance
	subs      map[chan storeEvent]struct{}
}

// storeEvent is a change: the instance, or the ID of one removed.
type storeEvent struct {
	Instance *Instance
	Removed  string
}

// storedInstance is an instance in the file.
type storedInstance struct {
	record
	Stopped bool `json:"stopped,omitempty"`
}

func (i Instance) stored() storedInstance {
	return storedInstance{record: i.record, Stopped: i.Connection == ConnectionStopped}
}

// kept is what the file keeps of the instance, but when it was last seen.
func (i Instance) kept() storedInstance {
	stored := i.stored()
	stored.LastSeen = time.Time{}
	return stored
}

func newStore(path string) (*store, error) {
	s := &store{path: path, instances: map[string]*Instance{}, subs: map[chan storeEvent]struct{}{}}
	if path == "" {
		return s, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var stored []storedInstance
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	for _, si := range stored {
		connection := ConnectionConnecting
		if si.Stopped {
			connection = ConnectionStopped
		}
		i := &Instance{record: si.record, Connection: connection}
		i.State = i.state()
		s.instances[si.ID] = i
	}
	return s, nil
}

// saveLocked writes the instances to the file. Caller holds s.mu.
func (s *store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	stored := make([]storedInstance, 0, len(s.instances))
	for _, i := range s.sortedLocked() {
		stored = append(stored, i.stored())
	}
	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".instances-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
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

// update changes the instance id with change, or adds it when create is
// set, and reports whether it did. The file is written when what it keeps
// changed; when the instance was last seen isn't worth a write of its own
// and goes with the next one.
func (s *store) update(id string, create bool, change func(*Instance)) (Instance, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, ok := s.instances[id]
	if !ok {
		if !create {
			return Instance{}, false, nil
		}
		i = &Instance{Connection: ConnectionConnecting}
		i.ID = id
		s.instances[id] = i
	}
	before := i.kept()
	change(i)
	i.State = i.state()
	var err error
	if !ok || i.kept() != before {
		err = s.saveLocked()
	}
	changed := *i
	s.notifyLocked(storeEvent{Instance: &changed})
	return changed, true, err
}

func (s *store) remove(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.instances[id]; !ok {
		return false, nil
	}
	delete(s.instances, id)
	err := s.saveLocked()
	s.notifyLocked(storeEvent{Removed: id})
	return true, err
}

// subscribe returns the instances and a channel of the changes after them.
func (s *store) subscribe() ([]Instance, chan storeEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := make([]Instance, 0, len(s.instances))
	for _, i := range s.sortedLocked() {
		list = append(list, *i)
	}
	ch := make(chan storeEvent, 256)
	s.subs[ch] = struct{}{}
	return list, ch
}

func (s *store) unsubscribe(ch chan storeEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.subs[ch]; ok {
		delete(s.subs, ch)
		close(ch)
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
