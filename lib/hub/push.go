package hub

import (
	"context"
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/coder/agentapi/lib/atomicfile"
	"github.com/coder/agentapi/lib/webpush"
)

// Web Push: the hub tells the browsers that subscribed (a phone with the
// page closed) that an agent needs them, that it finished a task, or how
// an issue's work went, through their push services. The page's service
// worker (chat/public/hub/sw.js) shows the notification. Each browser is a
// device with its own settings (PushPrefs, push_prefs.go); the notifiers
// are in push_notify.go.

const (
	// maxPushSubscriptions is how many browsers the hub pushes to; a new
	// one past it replaces the oldest.
	maxPushSubscriptions = 50
	// pushTimeout bounds a push service's answer.
	pushTimeout = 30 * time.Second
	// pushSubject is how a push service may reach whoever sends (VAPID's
	// sub): the project.
	pushSubject = "https://github.com/k1dav-c/agentapi"
	// maxPushLabel is the most characters of a device's label.
	maxPushLabel = 60
	// pushTokenContext is what the hub token is hashed with for
	// pushSubscription.TokenID.
	pushTokenContext = "agentapi-hub-push"
)

// pushKind is what a push tells.
type pushKind string

const (
	pushNeedsYou pushKind = "needs-you"
	pushFinished pushKind = "finished"
	pushIssue    pushKind = "issue"
	pushTest     pushKind = "test"
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

// pushInfo is GET /api/push: what a browser subscribes with.
type pushInfo struct {
	PublicKey string `json:"public_key" doc:"The hub's VAPID public key, base64url: the applicationServerKey a browser subscribes with."`
}

// pushSubscribeBody is PUT /api/push/subscription: a browser's
// subscription, as PushSubscription.toJSON() has it, and its label.
type pushSubscribeBody struct {
	webpush.Subscription
	Label string `json:"label,omitempty" doc:"What the device is, as the page names it (such as iPhone · Home Screen); at most 60 characters."`
	// Finished is what pages of 5.8 sent; it sets the setting of a new
	// device only.
	Finished bool `json:"finished,omitempty" doc:"For a new device: push the finished tasks too. Its settings are changed with PATCH /api/push/devices/{id}."`
}

// pushEndpointBody is DELETE /api/push/subscription and POST
// /api/push/test: the subscription, by its endpoint.
type pushEndpointBody struct {
	Endpoint string `json:"endpoint"`
}

// pushDevice is a device as GET /api/push/devices has it: without its
// endpoint and keys.
type pushDevice struct {
	ID string `json:"id" doc:"The device's ID: the start of its endpoint's SHA-256, in hex."`
	PushPrefs
	Label       string     `json:"label,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	RenewedAt   *time.Time `json:"renewed_at,omitempty" doc:"When a signed-in page on the device last told the hub."`
	ExpiresAt   *time.Time `json:"expires_at,omitempty" doc:"When the device's session ends, and the hub stops pushing to it; missing for none."`
	LastSentAt  *time.Time `json:"last_sent_at,omitempty" doc:"When its push service last took a push."`
	LastError   string     `json:"last_error,omitempty" doc:"Why its push service last refused one, until it takes the next."`
	LastErrorAt *time.Time `json:"last_error_at,omitempty"`
}

// pushDevicesBody is GET /api/push/devices.
type pushDevicesBody struct {
	Devices []pushDevice `json:"devices" nullable:"false"`
}

func timeRef(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func (s pushSubscription) device() pushDevice {
	prefs := s.PushPrefs
	prefs.Groups = slices.Clone(prefs.Groups)
	if prefs.Groups == nil {
		prefs.Groups = []string{}
	}
	return pushDevice{
		ID: s.id(), PushPrefs: prefs, Label: s.Label, CreatedAt: s.CreatedAt,
		RenewedAt: timeRef(s.RenewedAt), ExpiresAt: timeRef(s.ExpiresAt), LastSentAt: timeRef(s.LastSentAt),
		LastError: s.LastError, LastErrorAt: timeRef(s.LastErrorAt),
	}
}

// pushPayload is what the service worker gets, encrypted to the browser.
type pushPayload struct {
	Kind  pushKind `json:"kind"`
	Title string   `json:"title"`
	Body  string   `json:"body"`
	// ID is the instance to open; Issue the issue's job to open in
	// Issues. Neither, for a test.
	ID    string `json:"id,omitempty"`
	Issue string `json:"issue,omitempty"`
	// Tag groups the notifications of an instance or issue: a new one
	// replaces the last.
	Tag string `json:"tag"`
	// Badge is how many agents need you as the hub sends it, for the app
	// icon's badge.
	Badge int `json:"badge"`
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

func (h *Hub) getPush(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, pushInfo{PublicKey: webpush.PublicKey(h.push.key)})
}

// putPushSubscription adds the browser to the devices the hub pushes to,
// or renews it (PUT /api/push/subscription), and answers the device. The
// device stays as long as the session of the page that sent it: the page
// sends it again on each load.
func (h *Hub) putPushSubscription(w http.ResponseWriter, r *http.Request) {
	var body pushSubscribeBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "send a push subscription: {\"endpoint\": ..., \"keys\": {\"p256dh\": ..., \"auth\": ...}}")
		return
	}
	if err := body.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	now := h.clock.Now()
	sub := pushSubscription{
		Subscription: body.Subscription, PushPrefs: PushPrefs{Finished: body.Finished},
		Label: clip(strings.Join(strings.Fields(body.Label), " "), maxPushLabel), CreatedAt: now, RenewedAt: now,
		TokenID: pushTokenID(h.config.Token),
	}
	if issued, ok := h.sessionIssued(r); ok {
		sub.ExpiresAt = issued.Add(sessionMaxAge)
	}
	kept, err := h.push.renew(sub)
	if err != nil {
		h.logger.Error("Couldn't keep a push subscription", "error", err)
		writeError(w, http.StatusInternalServerError, "couldn't keep the subscription: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, kept.device())
}

func readEndpoint(w http.ResponseWriter, r *http.Request) (string, bool) {
	var body pushEndpointBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil || body.Endpoint == "" {
		writeError(w, http.StatusBadRequest, "send {\"endpoint\": ...}")
		return "", false
	}
	return body.Endpoint, true
}

func (h *Hub) deletePushSubscription(w http.ResponseWriter, r *http.Request) {
	endpoint, ok := readEndpoint(w, r)
	if !ok {
		return
	}
	if _, err := h.push.remove(byEndpoint(endpoint)); err != nil {
		writeError(w, http.StatusInternalServerError, "couldn't forget the subscription: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Hub) listPushDevices(w http.ResponseWriter, _ *http.Request) {
	subs, err := h.push.list(h.clock.Now())
	if err != nil {
		h.logger.Error("Couldn't forget the push devices whose session ended", "error", err)
	}
	body := pushDevicesBody{Devices: make([]pushDevice, 0, len(subs))}
	for _, sub := range subs {
		body.Devices = append(body.Devices, sub.device())
	}
	writeJSON(w, http.StatusOK, body)
}

// patchPushDevice changes a device's settings (PATCH
// /api/push/devices/{id}): those sent, the rest left as they are.
func (h *Hub) patchPushDevice(w http.ResponseWriter, r *http.Request) {
	var change pushPrefsChange
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&change); err != nil {
		writeError(w, http.StatusBadRequest, "send the settings to change: {\"finished\", \"issues\", \"quiet_hours\", \"groups\", \"pinned\"}")
		return
	}
	if err := change.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	sub, ok, err := h.push.update(chi.URLParam(r, "id"), func(sub *pushSubscription) { change.apply(&sub.PushPrefs) })
	switch {
	case !ok:
		writeError(w, http.StatusNotFound, "no such device")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "couldn't keep the settings: "+err.Error())
	default:
		writeJSON(w, http.StatusOK, sub.device())
	}
}

func (h *Hub) deletePushDevice(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	removed, err := h.push.remove(func(o pushSubscription) bool { return o.id() == id })
	switch {
	case err != nil:
		writeError(w, http.StatusInternalServerError, "couldn't forget the device: "+err.Error())
	case !removed:
		writeError(w, http.StatusNotFound, "no such device")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// testPush sends the subscription at the endpoint a test notification
// (POST /api/push/test), whatever its settings, and answers once its push
// service took it: 404 for a subscription the hub doesn't have, 410 for
// one its push service ended (the hub forgets it), 502 for another
// refusal.
func (h *Hub) testPush(w http.ResponseWriter, r *http.Request) {
	endpoint, ok := readEndpoint(w, r)
	if !ok {
		return
	}
	sub, ok := h.push.find(endpoint)
	if !ok || sub.expired(h.clock.Now()) {
		writeError(w, http.StatusNotFound, "the hub doesn't push to this browser")
		return
	}
	payload := pushPayload{Kind: pushTest, Title: "Tendyard Hub", Body: "Notifications reach this device.", Tag: "agentapi-hub-test", Badge: h.needingYou()}
	err := h.sendPush(r.Context(), sub, payload, webpush.UrgencyHigh)
	var status *webpush.StatusError
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, webpush.ErrGone):
		writeError(w, http.StatusGone, "the push service ended this subscription; turn push on again")
	case errors.As(err, &status):
		writeError(w, http.StatusBadGateway, err.Error())
	default:
		writeError(w, http.StatusBadGateway, "couldn't reach the push service: "+err.Error())
	}
}

// sendPush sends payload to sub and records the answer; it forgets sub
// once its push service says it ended.
func (h *Hub) sendPush(ctx context.Context, sub pushSubscription, payload pushPayload, urgency webpush.Urgency) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, pushTimeout)
	defer cancel()
	ttl := time.Hour
	if payload.Kind == pushNeedsYou {
		ttl = 12 * time.Hour
	}
	err = h.pushSender.Send(ctx, sub.Subscription, webpush.Message{Payload: data, TTL: ttl, Urgency: urgency, Topic: pushTopic(payload.Tag)})
	if errors.Is(err, webpush.ErrGone) {
		if _, rmErr := h.push.remove(byEndpoint(sub.Endpoint)); rmErr != nil {
			h.logger.Error("Couldn't forget an ended push subscription", "error", rmErr)
		}
		h.logger.Info("Forgot a push subscription its push service ended", "push_service", pushService(sub.Endpoint))
		return err
	}
	if saveErr := h.push.sent(sub.Endpoint, h.clock.Now(), err); saveErr != nil {
		h.logger.Error("Couldn't keep a push's answer", "error", saveErr)
	}
	return err
}

// pushTopic is the Topic header of tag: a message the push service still
// holds for the browser is replaced by the next of the same instance.
func pushTopic(tag string) string {
	sum := sha256.Sum256([]byte(tag))
	return base64.RawURLEncoding.EncodeToString(sum[:])[:32]
}

// pushService is the host of a push endpoint, to log without the
// endpoint's secret path.
func pushService(endpoint string) string {
	rest, _ := strings.CutPrefix(endpoint, "https://")
	host, _, _ := strings.Cut(rest, "/")
	return host
}
