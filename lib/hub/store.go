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

	"github.com/coder/agentapi/lib/httpapi"
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
type Connection string

const (
	ConnectionConnecting  Connection = "connecting"
	ConnectionConnected   Connection = "connected"
	ConnectionUnreachable Connection = "unreachable"
	// ConnectionStopped: the instance said it stopped (instance.stopped).
	ConnectionStopped Connection = "stopped"
)

// Instance is an AgentAPI the hub watches.
type Instance struct {
	// ID is the instance's ID (--instance-id), which the hub's routes use.
	ID        string    `json:"id"`
	Name      string    `json:"name,omitempty"`
	URL       string    `json:"url"`
	ServerID  string    `json:"server_id,omitempty"`
	Hostname  string    `json:"hostname,omitempty"`
	Port      int       `json:"port,omitempty"`
	CWD       string    `json:"cwd,omitempty"`
	AgentType string    `json:"agent_type,omitempty"`
	Source    Source    `json:"source"`
	LastSeen  time.Time `json:"last_seen"`
	// Group is the group someone put the instance in on the hub's page;
	// empty is none. The instance's own events never change it.
	Group string `json:"group,omitempty"`
	// Muted is what the instance waited for when someone muted it on the
	// hub's page (attentionKey); empty is not muted. It ends once that
	// changes, so the next thing the instance needs counts again.
	Muted string `json:"muted,omitempty"`
	// Token, for a manual instance that has its own, is sent to it; it
	// never leaves the hub.
	Token string `json:"token,omitempty"`

	Connection Connection           `json:"connection"`
	Error      string               `json:"error,omitempty"`
	Summary    *httpapi.SummaryBody `json:"summary,omitempty"`
}

// public is the instance as the browser sees it: without its token.
func (i Instance) public() Instance {
	i.Token = ""
	return i
}

// attentionKey is what an instance waits for, to mute: why it needs you
// and, for a request, which one; empty when it needs nothing.
func attentionKey(summary *httpapi.SummaryBody) string {
	if summary == nil || summary.Attention == "" || summary.Attention == httpapi.AttentionNone || summary.Attention == httpapi.AttentionExited {
		return ""
	}
	key := string(summary.Attention)
	if summary.Request != nil {
		key += ":" + summary.Request.ID
	}
	return key
}

// instanceURL is where the hub reaches the instance an event names: its
// public URL, else its host and port.
func instanceURL(instance httpapi.WebhookInstance) string {
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

// storedInstance is what the file keeps of an instance: not its summary or
// connection, which the hub finds out again.
type storedInstance struct {
	ID        string    `json:"id"`
	Name      string    `json:"name,omitempty"`
	URL       string    `json:"url"`
	ServerID  string    `json:"server_id,omitempty"`
	Hostname  string    `json:"hostname,omitempty"`
	Port      int       `json:"port,omitempty"`
	CWD       string    `json:"cwd,omitempty"`
	AgentType string    `json:"agent_type,omitempty"`
	Source    Source    `json:"source"`
	LastSeen  time.Time `json:"last_seen"`
	Token     string    `json:"token,omitempty"`
	Stopped   bool      `json:"stopped,omitempty"`
	Group     string    `json:"group,omitempty"`
	Muted     string    `json:"muted,omitempty"`
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
		s.instances[si.ID] = &Instance{
			ID: si.ID, Name: si.Name, URL: si.URL, ServerID: si.ServerID, Hostname: si.Hostname,
			Port: si.Port, CWD: si.CWD, AgentType: si.AgentType, Source: si.Source,
			LastSeen: si.LastSeen, Token: si.Token, Connection: connection, Group: si.Group, Muted: si.Muted,
		}
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
		stored = append(stored, storedInstance{
			ID: i.ID, Name: i.Name, URL: i.URL, ServerID: i.ServerID, Hostname: i.Hostname,
			Port: i.Port, CWD: i.CWD, AgentType: i.AgentType, Source: i.Source,
			LastSeen: i.LastSeen, Token: i.Token, Stopped: i.Connection == ConnectionStopped, Group: i.Group, Muted: i.Muted,
		})
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
// set, and reports whether it did. persist says whether what changed is
// kept in the file (not the summary or the connection).
func (s *store) update(id string, create, persist bool, change func(*Instance)) (Instance, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i, ok := s.instances[id]
	if !ok {
		if !create {
			return Instance{}, false, nil
		}
		i = &Instance{ID: id, Connection: ConnectionConnecting}
		s.instances[id] = i
		persist = true
	}
	change(i)
	var err error
	if persist {
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
