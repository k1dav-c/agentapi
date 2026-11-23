package hub

import (
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/coder/agentapi/lib/atomicfile"
	"github.com/coder/agentapi/lib/webpush"
)

const (
	// maxPushSubscriptions is how many browsers the hub pushes to; a new
	// one past it replaces the oldest.
	maxPushSubscriptions = 50
	// pushTokenContext is what the hub token is hashed with for
	// pushSubscription.TokenID.
	pushTokenContext = "agentapi-hub-push"
)

// pushSubscription is a browser the hub pushes to: a device.
type pushSubscription struct {
	webpush.Subscription
	PushPrefs
	// Label names the device, as its browser says (such as iPhone ·
	// Home Screen).
	Label     string    `json:"label,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	// RenewedAt is when a signed-in page last told the hub the
	// subscription (it does on each load).
	RenewedAt time.Time `json:"renewed_at,omitzero"`
	// ExpiresAt is when the session of the page that last told the hub
	// ends: the device isn't signed in past it, and the hub stops pushing
	// to it. Zero for one a hub token or a hub without one renewed.
	ExpiresAt time.Time `json:"expires_at,omitzero"`
	// TokenID stands for the hub token it was renewed under: a hub
	// started with another token forgets it.
	TokenID    string    `json:"token_id,omitempty"`
	LastSentAt time.Time `json:"last_sent_at,omitzero"`
	// LastError is why its push service last refused a push, until the
	// next one it takes.
	LastError   string    `json:"last_error,omitempty"`
	LastErrorAt time.Time `json:"last_error_at,omitzero"`
}

// id is the device's ID in the hub's API: the start of its endpoint's
// hash, since the endpoint lets whoever holds it push to the device.
func (s pushSubscription) id() string {
	return pushDeviceID(s.Endpoint)
}

func pushDeviceID(endpoint string) string {
	sum := sha256.Sum256([]byte(endpoint))
	return hex.EncodeToString(sum[:8])
}

// expired says whether the device's session ended at now.
func (s pushSubscription) expired(now time.Time) bool {
	return !s.ExpiresAt.IsZero() && !now.Before(s.ExpiresAt)
}

// pushStore keeps the hub's VAPID key and the devices in push.json beside
// the instances (0600), or in memory without a state path.
type pushStore struct {
	path string
	mu   sync.Mutex
	key  *ecdsa.PrivateKey
	subs []pushSubscription
}

// pushFile is push.json.
type pushFile struct {
	VAPIDKey      string             `json:"vapid_private_key"`
	Subscriptions []pushSubscription `json:"subscriptions"`
}

// newPushStore reads push.json beside statePath, or makes the key. It
// forgets the devices renewed under another hub token than tokenID's.
func newPushStore(statePath, tokenID string) (*pushStore, error) {
	s := &pushStore{}
	if statePath != "" {
		s.path = filepath.Join(filepath.Dir(statePath), "push.json")
		data, err := os.ReadFile(s.path)
		switch {
		case errors.Is(err, os.ErrNotExist):
		case err != nil:
			return nil, err
		default:
			var stored pushFile
			if err := json.Unmarshal(data, &stored); err != nil {
				return nil, fmt.Errorf("read %s: %w", s.path, err)
			}
			if s.key, err = webpush.ParsePrivateKey(stored.VAPIDKey); err != nil {
				return nil, fmt.Errorf("read %s: %w", s.path, err)
			}
			s.subs = slices.DeleteFunc(stored.Subscriptions, func(sub pushSubscription) bool { return sub.TokenID != tokenID })
			if len(s.subs) != len(stored.Subscriptions) {
				return s, s.saveLocked()
			}
			return s, nil
		}
	}
	key, err := webpush.GenerateKey()
	if err != nil {
		return nil, err
	}
	s.key = key
	// Kept now, so a browser that subscribes to it before any change
	// still matches after a restart.
	return s, s.saveLocked()
}

func (s *pushStore) saveLocked() error {
	if s.path == "" {
		return nil
	}
	encoded, err := webpush.EncodePrivateKey(s.key)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(pushFile{VAPIDKey: encoded, Subscriptions: s.subs}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	return atomicfile.Write(s.path, ".push.json-*", data)
}

// list is the devices whose session holds at now; it forgets the others.
func (s *pushStore) list(now time.Time) ([]pushSubscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.subs)
	s.subs = slices.DeleteFunc(s.subs, func(sub pushSubscription) bool { return sub.expired(now) })
	var err error
	if len(s.subs) != n {
		err = s.saveLocked()
	}
	return slices.Clone(s.subs), err
}

// any says whether the hub pushes to any device.
func (s *pushStore) any() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.subs) > 0
}

func (s *pushStore) find(endpoint string) (pushSubscription, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.subs, byEndpoint(endpoint))
	if i < 0 {
		return pushSubscription{}, false
	}
	return s.subs[i], true
}

// renew adds sub, or renews the device with its endpoint: its keys,
// label, session and token, keeping its settings and history. It returns
// the device as kept.
func (s *pushStore) renew(sub pushSubscription) (pushSubscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := slices.IndexFunc(s.subs, byEndpoint(sub.Endpoint)); i >= 0 {
		kept := s.subs[i]
		kept.Keys = sub.Keys
		if sub.Label != "" {
			kept.Label = sub.Label
		}
		kept.RenewedAt, kept.ExpiresAt, kept.TokenID = sub.RenewedAt, sub.ExpiresAt, sub.TokenID
		s.subs[i] = kept
		return kept, s.saveLocked()
	}
	if len(s.subs) >= maxPushSubscriptions {
		s.subs = slices.Delete(s.subs, 0, len(s.subs)-maxPushSubscriptions+1)
	}
	s.subs = append(s.subs, sub)
	return sub, s.saveLocked()
}

// update changes the device id; ok is false for one the hub doesn't have.
func (s *pushStore) update(id string, change func(*pushSubscription)) (sub pushSubscription, ok bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.subs, func(o pushSubscription) bool { return o.id() == id })
	if i < 0 {
		return pushSubscription{}, false, nil
	}
	change(&s.subs[i])
	return s.subs[i], true, s.saveLocked()
}

// sent records what the device's push service answered.
func (s *pushStore) sent(endpoint string, at time.Time, sendErr error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.subs, byEndpoint(endpoint))
	if i < 0 {
		return nil
	}
	if sendErr == nil {
		s.subs[i].LastSentAt, s.subs[i].LastError, s.subs[i].LastErrorAt = at, "", time.Time{}
	} else {
		s.subs[i].LastError, s.subs[i].LastErrorAt = clip(sendErr.Error(), 300), at
	}
	return s.saveLocked()
}

// remove forgets the devices match picks; it says whether there was one.
func (s *pushStore) remove(match func(pushSubscription) bool) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.subs)
	s.subs = slices.DeleteFunc(s.subs, match)
	if len(s.subs) == n {
		return false, nil
	}
	return true, s.saveLocked()
}

func byEndpoint(endpoint string) func(pushSubscription) bool {
	return func(o pushSubscription) bool { return o.Endpoint == endpoint }
}

// pushTokenID stands for the hub token the hub runs with; "" without one.
func pushTokenID(token string) string {
	if token == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte(pushTokenContext))
	return hex.EncodeToString(mac.Sum(nil))[:16]
}
