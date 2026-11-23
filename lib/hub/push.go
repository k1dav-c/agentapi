package hub

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/base64"
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
	"unicode/utf8"

	"github.com/coder/agentapi/lib/atomicfile"
	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/webpush"
)

// Web Push: the hub tells the browsers that subscribed (a phone with the
// page closed) that an agent needs them, or finished a task, through
// their push services. The page's service worker (chat/public/hub-sw.js)
// shows the notification.

const (
	// maxPushSubscriptions is how many browsers the hub pushes to; a new
	// one past it replaces the oldest.
	maxPushSubscriptions = 50
	// pushDelay is how long a change waits before the hub pushes it, and
	// is pushed only if it still holds: an approval answered at once, or a
	// finished task someone was looking at (the chat marks it read), is
	// none.
	pushDelay = 5 * time.Second
	// pushTimeout bounds a push service's answer.
	pushTimeout = 30 * time.Second
	// pushSubject is how a push service may reach whoever sends (VAPID's
	// sub): the project.
	pushSubject = "https://github.com/k1dav-c/agentapi"
	// maxPushText is the most characters of a notification's text.
	maxPushText = 200
)

// pushKind is what a push tells.
type pushKind string

const (
	pushNeedsYou pushKind = "needs-you"
	pushFinished pushKind = "finished"
	pushTest     pushKind = "test"
)

// pushSubscription is a browser the hub pushes to.
type pushSubscription struct {
	webpush.Subscription
	// Finished pushes the finished tasks too, besides what needs you.
	Finished  bool      `json:"finished"`
	CreatedAt time.Time `json:"created_at"`
}

// pushInfo is GET /api/push: what a browser subscribes with.
type pushInfo struct {
	PublicKey string `json:"public_key" doc:"The hub's VAPID public key, base64url: the applicationServerKey a browser subscribes with."`
}

// pushSubscribeBody is PUT /api/push/subscription: a browser's
// subscription, as PushSubscription.toJSON() has it, and what to push.
type pushSubscribeBody struct {
	webpush.Subscription
	Finished bool `json:"finished,omitempty" doc:"Push when an agent finishes a task too, not only when it needs you."`
}

// pushEndpointBody is DELETE /api/push/subscription and POST
// /api/push/test: the subscription, by its endpoint.
type pushEndpointBody struct {
	Endpoint string `json:"endpoint"`
}

// pushPayload is what the service worker gets, encrypted to the browser.
type pushPayload struct {
	Kind  pushKind `json:"kind"`
	Title string   `json:"title"`
	Body  string   `json:"body"`
	// ID is the instance to open; empty for a test.
	ID string `json:"id,omitempty"`
	// Tag groups the notifications of an instance: a new one replaces
	// the last.
	Tag string `json:"tag"`
}

// pushStore keeps the hub's VAPID key and the subscriptions in push.json
// beside the instances (0600), or in memory without a state path.
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

// newPushStore reads push.json beside statePath, or makes the key.
func newPushStore(statePath string) (*pushStore, error) {
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
			s.subs = stored.Subscriptions
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

func (s *pushStore) list() []pushSubscription {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.subs)
}

func (s *pushStore) find(endpoint string) (pushSubscription, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.subs, func(sub pushSubscription) bool { return sub.Endpoint == endpoint })
	if i < 0 {
		return pushSubscription{}, false
	}
	return s.subs[i], true
}

// put adds sub, or replaces the one with its endpoint.
func (s *pushStore) put(sub pushSubscription) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := slices.IndexFunc(s.subs, func(o pushSubscription) bool { return o.Endpoint == sub.Endpoint }); i >= 0 {
		sub.CreatedAt = s.subs[i].CreatedAt
		s.subs[i] = sub
	} else {
		if len(s.subs) >= maxPushSubscriptions {
			s.subs = slices.Delete(s.subs, 0, len(s.subs)-maxPushSubscriptions+1)
		}
		s.subs = append(s.subs, sub)
	}
	return s.saveLocked()
}

// remove forgets the subscription at endpoint; it says whether there was
// one.
func (s *pushStore) remove(endpoint string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.subs)
	s.subs = slices.DeleteFunc(s.subs, func(o pushSubscription) bool { return o.Endpoint == endpoint })
	if len(s.subs) == n {
		return false, nil
	}
	return true, s.saveLocked()
}

func (h *Hub) getPush(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, pushInfo{PublicKey: webpush.PublicKey(h.push.key)})
}

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
	sub := pushSubscription{Subscription: body.Subscription, Finished: body.Finished, CreatedAt: h.clock.Now()}
	if err := h.push.put(sub); err != nil {
		h.logger.Error("Couldn't keep a push subscription", "error", err)
		writeError(w, http.StatusInternalServerError, "couldn't keep the subscription: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	if _, err := h.push.remove(endpoint); err != nil {
		writeError(w, http.StatusInternalServerError, "couldn't forget the subscription: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// testPush sends the subscription at the endpoint a test notification
// (POST /api/push/test) and answers once its push service took it: 404
// for a subscription the hub doesn't have, 410 for one its push service
// ended (the hub forgets it), 502 for another refusal.
func (h *Hub) testPush(w http.ResponseWriter, r *http.Request) {
	endpoint, ok := readEndpoint(w, r)
	if !ok {
		return
	}
	sub, ok := h.push.find(endpoint)
	if !ok {
		writeError(w, http.StatusNotFound, "the hub doesn't push to this browser")
		return
	}
	payload := pushPayload{Kind: pushTest, Title: "Tendyard Hub", Body: "Notifications reach this device.", Tag: "agentapi-hub-test"}
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

// sendPush sends payload to sub, and forgets sub once its push service
// says it ended.
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
		if _, rmErr := h.push.remove(sub.Endpoint); rmErr != nil {
			h.logger.Error("Couldn't forget an ended push subscription", "error", rmErr)
		}
		h.logger.Info("Forgot a push subscription its push service ended", "push_service", pushService(sub.Endpoint))
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

// pushAll sends payload to every subscription that wants its kind.
func (h *Hub) pushAll(ctx context.Context, payload pushPayload) {
	urgency := webpush.UrgencyNormal
	if payload.Kind == pushNeedsYou {
		urgency = webpush.UrgencyHigh
	}
	var wg sync.WaitGroup
	for _, sub := range h.push.list() {
		if payload.Kind == pushFinished && !sub.Finished {
			continue
		}
		wg.Go(func() {
			err := h.sendPush(ctx, sub, payload, urgency)
			if err != nil && !errors.Is(err, webpush.ErrGone) {
				h.logger.Warn("Couldn't push a notification", "push_service", pushService(sub.Endpoint), "error", err)
			}
		})
	}
	wg.Wait()
}

// pushSeen is what the notifier last saw of an instance.
type pushSeen struct {
	// attention is what it waited for (instanceapi.AttentionKey), "" for
	// nothing: a push goes when that changes to something.
	attention string
	// task is its latest task, and whether it was running.
	task    int
	running bool
}

// seePush is what summary says, for pushSeen; ok is false when the hub
// doesn't know (no summary, or it can't reach the instance), which keeps
// what it saw before.
func seePush(i Instance) (pushSeen, bool) {
	if i.Summary == nil || i.Connection == instanceapi.ConnectionStopped || i.Connection == instanceapi.ConnectionUnreachable {
		return pushSeen{}, false
	}
	seen := pushSeen{attention: instanceapi.AttentionKey(i.Summary)}
	if task := i.Summary.Task; task != nil {
		seen.task = task.ID
		seen.running = task.EndedAt == nil
	}
	return seen, true
}

// notifyPush follows the instances' changes until ctx ends, and pushes
// what needs you and the tasks that finished. What it sees first of an
// instance (after the hub starts, or the instance comes) only tells it
// where the instance stands.
func (h *Hub) notifyPush(ctx context.Context) {
	for ctx.Err() == nil {
		instances, _, ch := h.store.subscribe()
		seen := make(map[string]pushSeen, len(instances))
		for _, i := range instances {
			if s, ok := seePush(i); ok {
				seen[i.ID] = s
			}
		}
		h.followPush(ctx, ch, seen)
		h.store.unsubscribe(ch)
	}
}

// followPush pushes the changes of ch until it closes (it fell behind)
// or ctx ends.
func (h *Hub) followPush(ctx context.Context, ch chan storeEvent, seen map[string]pushSeen) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			switch event.Kind {
			case eventRemoved:
				delete(seen, event.Removed)
			case eventInstance:
				i := event.Instance
				now, ok := seePush(i)
				if !ok {
					continue
				}
				before, known := seen[i.ID]
				seen[i.ID] = now
				if !known {
					continue
				}
				switch {
				case now.attention != "" && now.attention != before.attention:
					h.pushLater(ctx, i.ID, pushNeedsYou, now)
				case now.attention == "" && before.running && !now.running && before.task == now.task:
					h.pushLater(ctx, i.ID, pushFinished, now)
				}
			case eventSettings:
			}
		}
	}
}

// pushLater pushes kind for the instance id after pushDelay, if what it
// says still holds then.
func (h *Hub) pushLater(ctx context.Context, id string, kind pushKind, seen pushSeen) {
	if len(h.push.list()) == 0 {
		return
	}
	h.clock.AfterFunc(pushDelay, func() {
		if ctx.Err() != nil {
			return
		}
		i, ok := h.store.get(id)
		if !ok {
			return
		}
		payload, ok := pushFor(i, kind, seen)
		if !ok {
			return
		}
		go h.pushAll(ctx, payload)
	}, "hub", "push")
}

// pushFor is the notification of kind for i, if i still stands where the
// notifier saw it (seen): it still waits for the same thing, and isn't
// muted; or its task is still the one that finished, and unread.
func pushFor(i Instance, kind pushKind, seen pushSeen) (pushPayload, bool) {
	now, ok := seePush(i)
	if !ok {
		return pushPayload{}, false
	}
	name := i.Name
	if name == "" {
		name = i.ID
	}
	payload := pushPayload{Kind: kind, ID: i.ID, Tag: "agentapi-hub-" + i.ID}
	switch kind {
	case pushNeedsYou:
		if now.attention != seen.attention || i.State != instanceapi.StateNeedsYou {
			return pushPayload{}, false
		}
		payload.Title = name + " needs you"
		payload.Body = waitsFor(i.Summary)
	case pushFinished:
		if now.attention != "" || now.task != seen.task || now.running || !i.Summary.Unread {
			return pushPayload{}, false
		}
		payload.Title = name + " finished"
		payload.Body = i.Summary.Title
		if payload.Body == "" && i.Summary.Task != nil {
			payload.Body = i.Summary.Task.Prompt
		}
	case pushTest:
		return pushPayload{}, false
	}
	payload.Body = clip(strings.Join(strings.Fields(payload.Body), " "), maxPushText)
	payload.Title = clip(payload.Title, maxPushText)
	return payload, true
}

// waitsFor is what an instance that needs you waits for, as the hub's
// page says it (waitsFor in chat/src/lib/hub-format.ts).
func waitsFor(summary *instanceapi.SummaryBody) string {
	switch {
	case summary.Attention == instanceapi.AttentionLogin:
		if summary.LoginRequired != nil && summary.LoginRequired.Message != "" {
			return summary.LoginRequired.Message
		}
		return "Log in to continue"
	case summary.Attention == instanceapi.AttentionLimit:
		if summary.LimitStop != nil && summary.LimitStop.Message != "" {
			return summary.LimitStop.Message
		}
		return "The account reached its limit"
	case summary.Request != nil:
		return summary.Request.Title
	default:
		return "Waiting for your answer"
	}
}

// clip cuts s to max characters, with an ellipsis.
func clip(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max-1]) + "…"
}
