package hub

import (
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/coder/agentapi/lib/webpush"
)

const (
	// maxPushSubscriptions is how many browsers the hub pushes to; a new
	// one past it replaces the oldest.
	maxPushSubscriptions = 50
	// pushTokenContext is what the hub token is hashed with for
	// pushSubscription.TokenID.
	pushTokenContext = "agentapi-hub-push"
	// maxPushRevoked is how many removed devices the hub remembers, to
	// refuse their pages' renewals.
	maxPushRevoked = 200
)

// errPushRevoked is a renewal of a device removed from another one: its
// page stops pushing rather than subscribe again.
var errPushRevoked = errors.New("this device was removed from the hub's push settings")

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
// the instances (0600), or in memory without a state path. A change is
// written in the background within a second (persister), and on close.
type pushStore struct {
	path    string
	persist *persister
	mu      sync.Mutex
	key     *ecdsa.PrivateKey
	subs    []pushSubscription
	// revoked are the IDs of the devices removed from the settings,
	// the latest last.
	revoked []string
}

// pushFile is push.json.
type pushFile struct {
	VAPIDKey      string             `json:"vapid_private_key"`
	Subscriptions []pushSubscription `json:"subscriptions"`
	Revoked       []string           `json:"revoked,omitempty"`
}

// newPushStore reads push.json beside statePath, or makes the key. It
// forgets the devices renewed under another hub token than tokenID's;
// those of 5.8, which kept no token, take this one.
func newPushStore(statePath, tokenID string, logger *slog.Logger) (*pushStore, error) {
	s := &pushStore{}
	if statePath == "" {
		key, err := webpush.GenerateKey()
		s.key = key
		return s, err
	}
	s.path = filepath.Join(filepath.Dir(statePath), "push.json")
	data, err := os.ReadFile(s.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if s.key, err = webpush.GenerateKey(); err != nil {
			return nil, err
		}
		// Kept now, so a browser that subscribes to it before any change
		// still matches after a restart.
		s.persist = newPersister(s.path, "push devices", logger, s.snapshot)
		s.persist.mark()
		if err := s.persist.flush(); err != nil {
			_ = s.persist.close()
			return nil, err
		}
		return s, nil
	case err != nil:
		return nil, err
	}
	var stored pushFile
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("read %s: %w", s.path, err)
	}
	if s.key, err = webpush.ParsePrivateKey(stored.VAPIDKey); err != nil {
		return nil, fmt.Errorf("read %s: %w", s.path, err)
	}
	s.revoked = stored.Revoked
	for _, sub := range stored.Subscriptions {
		if sub.TokenID == "" {
			sub.TokenID = tokenID
		}
		if sub.TokenID == tokenID {
			s.subs = append(s.subs, sub)
		}
	}
	s.persist = newPersister(s.path, "push devices", logger, s.snapshot)
	if len(s.subs) != len(stored.Subscriptions) {
		s.persist.mark()
	}
	return s, nil
}

func (s *pushStore) snapshot() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	encoded, err := webpush.EncodePrivateKey(s.key)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(pushFile{VAPIDKey: encoded, Subscriptions: s.subs, Revoked: s.revoked}, "", "  ")
}

// changedLocked has the file written. Caller holds s.mu.
func (s *pushStore) changedLocked() {
	if s.persist != nil {
		s.persist.mark()
	}
}

// close writes what the file doesn't have yet.
func (s *pushStore) close() error {
	if s.persist == nil {
		return nil
	}
	return s.persist.close()
}

// dropExpiredLocked forgets the devices whose session ended at now.
// Caller holds s.mu.
func (s *pushStore) dropExpiredLocked(now time.Time) {
	n := len(s.subs)
	s.subs = slices.DeleteFunc(s.subs, func(sub pushSubscription) bool { return sub.expired(now) })
	if len(s.subs) != n {
		s.changedLocked()
	}
}

// list is the devices whose session holds at now; it forgets the others.
func (s *pushStore) list(now time.Time) []pushSubscription {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dropExpiredLocked(now)
	return slices.Clone(s.subs)
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
// the device as kept. A device removed from the settings comes back only
// when its page turns push on (enable): a renewal of it is errPushRevoked.
// Past maxPushSubscriptions, the devices whose session ended go first,
// then the oldest.
func (s *pushStore) renew(sub pushSubscription, enable bool) (pushSubscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := sub.id()
	if i := slices.Index(s.revoked, id); i >= 0 {
		if !enable {
			return pushSubscription{}, errPushRevoked
		}
		s.revoked = slices.Delete(s.revoked, i, i+1)
	}
	defer s.changedLocked()
	if i := slices.IndexFunc(s.subs, byEndpoint(sub.Endpoint)); i >= 0 {
		kept := s.subs[i]
		kept.Keys = sub.Keys
		if sub.Label != "" {
			kept.Label = sub.Label
		}
		kept.RenewedAt, kept.ExpiresAt, kept.TokenID = sub.RenewedAt, sub.ExpiresAt, sub.TokenID
		s.subs[i] = kept
		return kept, nil
	}
	if len(s.subs) >= maxPushSubscriptions {
		s.dropExpiredLocked(sub.RenewedAt)
	}
	if len(s.subs) >= maxPushSubscriptions {
		s.subs = slices.Delete(s.subs, 0, len(s.subs)-maxPushSubscriptions+1)
	}
	s.subs = append(s.subs, sub)
	return sub, nil
}

// update changes the device id; ok is false for one the hub doesn't have.
func (s *pushStore) update(id string, change func(*pushSubscription)) (pushSubscription, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.subs, func(o pushSubscription) bool { return o.id() == id })
	if i < 0 {
		return pushSubscription{}, false
	}
	change(&s.subs[i])
	s.changedLocked()
	return s.subs[i], true
}

// sent records what the device's push service answered.
func (s *pushStore) sent(endpoint string, at time.Time, sendErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.subs, byEndpoint(endpoint))
	if i < 0 {
		return
	}
	if sendErr == nil {
		s.subs[i].LastSentAt, s.subs[i].LastError, s.subs[i].LastErrorAt = at, "", time.Time{}
	} else {
		s.subs[i].LastError, s.subs[i].LastErrorAt = clip(sendErr.Error(), 300), at
	}
	s.changedLocked()
}

// remove forgets the device at endpoint, its page's or its push
// service's doing; it says whether there was one.
func (s *pushStore) remove(endpoint string) bool {
	return s.removeWhere(byEndpoint(endpoint), false)
}

// revoke forgets the device id, removed from another device's settings,
// and refuses its page's renewals; it says whether there was one.
func (s *pushStore) revoke(id string) bool {
	return s.removeWhere(func(o pushSubscription) bool { return o.id() == id }, true)
}

func (s *pushStore) removeWhere(match func(pushSubscription) bool, revoke bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.subs)
	for _, sub := range s.subs {
		if revoke && match(sub) && !slices.Contains(s.revoked, sub.id()) {
			s.revoked = append(s.revoked, sub.id())
		}
	}
	if len(s.revoked) > maxPushRevoked {
		s.revoked = slices.Delete(s.revoked, 0, len(s.revoked)-maxPushRevoked)
	}
	s.subs = slices.DeleteFunc(s.subs, match)
	if len(s.subs) == n {
		return false
	}
	s.changedLocked()
	return true
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
