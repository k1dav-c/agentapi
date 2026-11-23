package hub

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/coder/agentapi/lib/webpush"
)

// maxPushLabel is the most characters of a device's label.
const maxPushLabel = 60

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

func (h *Hub) getPush(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, pushInfo{PublicKey: webpush.PublicKey(h.push.devices.key)})
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
	kept, err := h.push.devices.renew(sub)
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
	if _, err := h.push.devices.remove(byEndpoint(endpoint)); err != nil {
		writeError(w, http.StatusInternalServerError, "couldn't forget the subscription: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Hub) listPushDevices(w http.ResponseWriter, _ *http.Request) {
	subs, err := h.push.devices.list(h.clock.Now())
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
	sub, ok, err := h.push.devices.update(chi.URLParam(r, "id"), func(sub *pushSubscription) { change.apply(&sub.PushPrefs) })
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
	removed, err := h.push.devices.remove(func(o pushSubscription) bool { return o.id() == id })
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
	sub, ok := h.push.devices.find(endpoint)
	if !ok || sub.expired(h.clock.Now()) {
		writeError(w, http.StatusNotFound, "the hub doesn't push to this browser")
		return
	}
	payload := pushPayload{Kind: pushTest, Title: "Tendyard Hub", Body: "Notifications reach this device.", Tag: "agentapi-hub-test", Badge: h.needingYou()}
	err := h.push.send(r.Context(), sub, payload, webpush.UrgencyHigh)
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
