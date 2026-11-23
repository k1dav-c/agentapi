package hub

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/quartz"

	"github.com/coder/agentapi/lib/webpush"
)

// Web Push: the hub tells the browsers that subscribed (a phone with the
// page closed) that an agent needs them, that it finished a task, or how
// an issue's work went, through their push services. The page's service
// worker (chat/public/hub/sw.js) shows the notification.
//
//   - push.go: what a push is, and the pusher, which sends them.
//   - push_store.go: the devices, each a browser's subscription, kept in
//     push.json with the hub's VAPID key.
//   - push_prefs.go: what each device gets (PushPrefs).
//   - push_api.go: the routes of the page (/api/push, /api/push/devices).
//   - push_notify.go: the notifiers, which turn the store's and the
//     dispatch's changes into pushes.

const (
	// pushTimeout bounds a push service's answer.
	pushTimeout = 30 * time.Second
	// pushSubject is how a push service may reach whoever sends (VAPID's
	// sub): the project.
	pushSubject = "https://github.com/k1dav-c/agentapi"
)

// pushKind is what a push tells.
type pushKind string

const (
	pushNeedsYou pushKind = "needs-you"
	pushFinished pushKind = "finished"
	pushIssue    pushKind = "issue"
	pushTest     pushKind = "test"
)

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

// pusher sends the hub's pushes to the devices it keeps.
type pusher struct {
	devices *pushStore
	sender  *webpush.Sender
	clock   quartz.Clock
	logger  *slog.Logger
	// needingYou counts the agents that need you, for the app icon's
	// badge.
	needingYou func() int
}

func newPusher(devices *pushStore, client *http.Client, clock quartz.Clock, logger *slog.Logger, needingYou func() int) *pusher {
	sender := &webpush.Sender{Key: devices.key, Subject: pushSubject, Client: client, Now: func() time.Time { return clock.Now() }}
	return &pusher{devices: devices, sender: sender, clock: clock, logger: logger, needingYou: needingYou}
}

// sendAll sends payload to every device that wants it now; i is the
// agent it is about, nil for an issue's.
func (p *pusher) sendAll(ctx context.Context, payload pushPayload, i *Instance) {
	urgency := webpush.UrgencyNormal
	if payload.Kind == pushNeedsYou {
		urgency = webpush.UrgencyHigh
	}
	now := p.clock.Now()
	subs, err := p.devices.list(now)
	if err != nil {
		p.logger.Error("Couldn't forget the push devices whose session ended", "error", err)
	}
	payload.Badge = p.needingYou()
	var wg sync.WaitGroup
	for _, sub := range subs {
		if !sub.wants(payload, i, now) {
			continue
		}
		wg.Go(func() {
			err := p.send(ctx, sub, payload, urgency)
			if err != nil && !errors.Is(err, webpush.ErrGone) {
				p.logger.Warn("Couldn't push a notification", "push_service", pushService(sub.Endpoint), "error", err)
			}
		})
	}
	wg.Wait()
}

// send sends payload to sub and records the answer; it forgets sub once
// its push service says it ended.
func (p *pusher) send(ctx context.Context, sub pushSubscription, payload pushPayload, urgency webpush.Urgency) error {
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
	err = p.sender.Send(ctx, sub.Subscription, webpush.Message{Payload: data, TTL: ttl, Urgency: urgency, Topic: pushTopic(payload.Tag)})
	if errors.Is(err, webpush.ErrGone) {
		if _, rmErr := p.devices.remove(byEndpoint(sub.Endpoint)); rmErr != nil {
			p.logger.Error("Couldn't forget an ended push subscription", "error", rmErr)
		}
		p.logger.Info("Forgot a push subscription its push service ended", "push_service", pushService(sub.Endpoint))
		return err
	}
	if saveErr := p.devices.sent(sub.Endpoint, p.clock.Now(), err); saveErr != nil {
		p.logger.Error("Couldn't keep a push's answer", "error", saveErr)
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
