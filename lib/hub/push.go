package hub

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
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
	// badge; groups are the groups the agents are in now, which a device's
	// Groups are matched against.
	needingYou func() int
	groups     func() map[string]bool
	// hosts are the push services it sends to (pushServiceHosts, and
	// Config.PushServiceHosts).
	hosts []string

	// The pushes under way in the background (goSend), which stop waits
	// for.
	mu      sync.Mutex
	stopped bool
	sending sync.WaitGroup
}

func newPusher(devices *pushStore, client *http.Client, hosts []string, clock quartz.Clock, logger *slog.Logger, needingYou func() int, groups func() map[string]bool) *pusher {
	sender := &webpush.Sender{Key: devices.key, Subject: pushSubject, Client: client, Now: func() time.Time { return clock.Now() }}
	return &pusher{
		devices: devices, sender: sender, clock: clock, logger: logger, needingYou: needingYou, groups: groups,
		hosts: append(slices.Clone(pushServiceHosts), hosts...),
	}
}

// goSendAll sends payload in the background (sendAll), unless the pusher
// stopped.
func (p *pusher) goSendAll(ctx context.Context, payload pushPayload, i *Instance) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return
	}
	p.sending.Go(func() { p.sendAll(ctx, payload, i) })
}

// stop waits for the pushes under way, and sends no more in the
// background. Call it once ctx of those ended.
func (p *pusher) stop() {
	p.mu.Lock()
	p.stopped = true
	p.mu.Unlock()
	p.sending.Wait()
}

// sendAll sends payload to every device that wants it now; i is the
// agent it is about, nil for an issue's.
func (p *pusher) sendAll(ctx context.Context, payload pushPayload, i *Instance) {
	if ctx.Err() != nil {
		return
	}
	urgency := webpush.UrgencyNormal
	if payload.Kind == pushNeedsYou {
		urgency = webpush.UrgencyHigh
	}
	now := p.clock.Now()
	payload.Badge = p.needingYou()
	groups := p.groups()
	var wg sync.WaitGroup
	for _, sub := range p.devices.list(now) {
		if !sub.wants(payload, i, now, groups) {
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
	if err := checkPushService(sub.Endpoint, p.hosts); err != nil {
		return err
	}
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
		p.devices.remove(sub.Endpoint)
		p.logger.Info("Forgot a push subscription its push service ended", "push_service", pushService(sub.Endpoint))
		return err
	}
	p.devices.sent(sub.Endpoint, p.clock.Now(), err)
	return err
}

// pushServiceHosts are the push services the hub sends to, by the end of
// their host name: the browsers' own (Chrome's and the other Chromium
// browsers', Safari's, Firefox's, Edge's). The hub refuses others, so a
// page can't have it send requests anywhere it reaches.
var pushServiceHosts = []string{".googleapis.com", ".push.apple.com", ".push.services.mozilla.com", ".notify.windows.com"}

// checkPushService refuses an endpoint whose host isn't one of hosts: a
// name, or .domain for its subdomains.
func checkPushService(endpoint string, hosts []string) error {
	u, err := url.Parse(endpoint)
	if err != nil {
		return err
	}
	host := strings.ToLower(u.Hostname())
	for _, allowed := range hosts {
		allowed = strings.ToLower(allowed)
		if host == strings.TrimPrefix(allowed, ".") || (strings.HasPrefix(allowed, ".") && strings.HasSuffix(host, allowed)) {
			return nil
		}
	}
	return errUnknownPushService
}

var errUnknownPushService = errors.New("the endpoint isn't a browser's push service (Google's, Apple's, Mozilla's or Microsoft's)")

// newPushClient is how the hub reaches the push services by default: the
// environment's proxy, no redirect (a push service answers where it is
// asked), and a response's headers within 15 seconds.
func newPushClient() *http.Client {
	return &http.Client{
		Transport: newTransport(),
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
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
