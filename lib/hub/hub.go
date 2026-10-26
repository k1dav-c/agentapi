// Package hub is agentapi hub serve: a server that watches many AgentAPIs.
// They join it with their webhook (agentapi hub join): their signed
// instance.* events add them, and the hub follows each one's summary
// stream (GET /events?only=summary). A browser signs in to the hub, gets
// every instance and its changes from one event stream, and opens any of
// them in the chat through the hub, which sends the instance's API token
// for it: the browser never holds the instances' tokens or reaches them
// itself.
package hub

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/coder/agentapi/lib/httpapi"
)

// Config is how the hub runs.
type Config struct {
	// Token is what a browser signs in with; it is required.
	Token string
	// Secret checks the instances' webhook events; it is required, since
	// an event names the URL the hub sends an instance token to.
	Secret string
	// InstanceToken is sent to the instances that don't have their own.
	InstanceToken string
	// StatePath keeps the instances across restarts; empty keeps none.
	StatePath string
	// ChatBasePath is where the hub serves the chat UI, by default /chat.
	ChatBasePath string
	Logger       *slog.Logger
	// Client reaches the instances; nil is a client that uses the proxy
	// of the environment (HTTP_PROXY, ALL_PROXY for a VPN in userspace).
	Client *http.Client
	// Now is the clock; nil is time.Now.
	Now func() time.Time
	// ConnectGrace is how long the hub keeps trying an instance it can't
	// reach before it calls it unreachable; zero is 10 seconds.
	ConnectGrace time.Duration
}

// Hub is the server.
type Hub struct {
	config     Config
	logger     *slog.Logger
	store      *store
	client     *http.Client
	deliveries deliveries
	now        func() time.Time

	ctx      context.Context
	watchMu  sync.Mutex
	watchers map[string]context.CancelFunc
	watchWG  sync.WaitGroup
}

// New returns the hub of config, with the instances it kept.
func New(config Config) (*Hub, error) {
	if config.Token == "" {
		return nil, errors.New("the hub needs a token to sign in with (--token)")
	}
	if config.Secret == "" {
		return nil, errors.New("the hub needs the secret the instances sign their events with (--secret)")
	}
	if config.ChatBasePath == "" {
		config.ChatBasePath = "/chat"
	}
	if config.Logger == nil {
		config.Logger = slog.New(slog.DiscardHandler)
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.ConnectGrace == 0 {
		config.ConnectGrace = defaultConnectGrace
	}
	client := config.Client
	if client == nil {
		client = &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, ResponseHeaderTimeout: 15 * time.Second}}
	}
	st, err := newStore(config.StatePath)
	if err != nil {
		return nil, err
	}
	return &Hub{
		config: config, logger: config.Logger, store: st, client: client, now: config.Now,
		watchers: map[string]context.CancelFunc{},
	}, nil
}

// Run follows the instances' summaries until ctx ends.
func (h *Hub) Run(ctx context.Context) {
	h.watchMu.Lock()
	h.ctx = ctx
	h.watchMu.Unlock()
	for _, instance := range h.store.list() {
		if instance.Connection != ConnectionStopped {
			h.watch(instance)
		}
	}
	<-ctx.Done()
	h.watchWG.Wait()
}

// tokenFor is the API token the hub sends to instance.
func (h *Hub) tokenFor(instance Instance) string {
	if instance.Token != "" {
		return instance.Token
	}
	return h.config.InstanceToken
}

// Handler serves the hub: the webhook receiver, sign-in, the API, the
// instances through the proxy, and the chat UI.
func (h *Hub) Handler() http.Handler {
	r := chi.NewRouter()
	r.Post("/hooks", h.receiveWebhook)
	r.Get("/api/session", h.getSession)
	r.Post("/api/session", h.createSession)
	r.Delete("/api/session", h.deleteSession)
	r.Group(func(r chi.Router) {
		r.Use(h.requireSession)
		r.Get("/api/instances", h.listInstances)
		r.Post("/api/instances", h.addInstance)
		r.Patch("/api/instances/{id}", h.updateInstance)
		r.Delete("/api/instances/{id}", h.removeInstance)
		r.Get("/api/events", h.streamEvents)
		r.Handle("/i/{id}", http.HandlerFunc(h.proxy))
		r.Handle("/i/{id}/*", http.HandlerFunc(h.proxy))
	})
	chat := http.StripPrefix(h.config.ChatBasePath, httpapi.FileServerWithIndexFallback(h.config.ChatBasePath))
	r.Handle(h.config.ChatBasePath, chat)
	r.Handle(h.config.ChatBasePath+"/*", chat)
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, h.config.ChatBasePath+"/hub/", http.StatusTemporaryRedirect)
	})
	return r
}

// The browser's session: a cookie derived from the hub token, which a
// changed token invalidates. SameSite=Lax keeps other sites from sending
// it with a POST.
const (
	sessionCookie  = "agentapi_hub_session"
	sessionContext = "agentapi-hub-session-v1"
	sessionMaxAge  = 30 * 24 * time.Hour
)

func (h *Hub) sessionValue() string {
	mac := hmac.New(sha256.New, []byte(h.config.Token))
	mac.Write([]byte(sessionContext))
	return hex.EncodeToString(mac.Sum(nil))
}

// signedIn reports whether r carries the session cookie or the hub token.
func (h *Hub) signedIn(r *http.Request) bool {
	if c, err := r.Cookie(sessionCookie); err == nil && hmac.Equal([]byte(c.Value), []byte(h.sessionValue())) {
		return true
	}
	if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return hmac.Equal([]byte(bearer), []byte(h.config.Token))
	}
	return false
}

func (h *Hub) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.signedIn(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "sign in to the hub"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Hub) getSession(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": h.signedIn(r)})
}

func (h *Hub) createSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "send {\"token\": ...}"})
		return
	}
	if !hmac.Equal([]byte(body.Token), []byte(h.config.Token)) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "wrong token"})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: h.sessionValue(), Path: "/", MaxAge: int(sessionMaxAge.Seconds()),
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
	})
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": true})
}

func (h *Hub) deleteSession(w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": false})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// publicList is the instances without their tokens.
func publicList(instances []Instance) []Instance {
	list := make([]Instance, len(instances))
	for i, instance := range instances {
		list[i] = instance.public()
	}
	return list
}

func (h *Hub) listInstances(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"instances": publicList(h.store.list())})
}

// addInstance adds an instance by its URL (POST /api/instances): the hub
// asks it for its summary, which says its ID and name.
func (h *Hub) addInstance(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL   string `json:"url"`
		Token string `json:"token"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "send {\"url\": ..., \"token\": ...}"})
		return
	}
	u, err := url.Parse(body.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "url must be an http or https URL"})
		return
	}
	base := strings.TrimRight(body.URL, "/")
	summary, err := h.fetchSummary(r.Context(), base, body.Token)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	id := summary.Instance.ID
	if id == "" {
		id = u.Host
	}
	instance, _, err := h.store.update(id, true, true, func(i *Instance) {
		i.URL = base
		i.Token = body.Token
		i.Source = SourceManual
		i.Name = summary.Instance.Name
		i.ServerID = summary.Instance.ServerID
		i.Hostname = summary.Instance.Hostname
		i.Port = summary.Instance.Port
		i.CWD = summary.Instance.CWD
		i.AgentType = string(summary.Instance.AgentType)
		i.LastSeen = h.now()
		i.Summary = summary
		i.Connection = ConnectionConnecting
		i.Error = ""
	})
	if err != nil {
		h.logger.Warn("Failed to keep the hub's instances", "error", err)
	}
	h.watch(instance)
	writeJSON(w, http.StatusOK, instance.public())
}

func (h *Hub) fetchSummary(ctx context.Context, base, token string) (*httpapi.SummaryBody, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/summary", nil)
	if err != nil {
		return nil, err
	}
	if token == "" {
		token = h.config.InstanceToken
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("unreachable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, errors.New("the instance needs its API token")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /summary: %s (is it AgentAPI 1.1 or later?)", resp.Status)
	}
	var summary httpapi.SummaryBody
	if err := json.NewDecoder(resp.Body).Decode(&summary); err != nil {
		return nil, fmt.Errorf("GET /summary: %w", err)
	}
	return &summary, nil
}

// maxGroupName is the longest group name, in characters.
const maxGroupName = 40

// updateInstance changes what the hub's page sets on an instance (PATCH
// /api/instances/{id}): {"group": "..."} puts it in a group, "" in none.
func (h *Hub) updateInstance(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Group *string `json:"group"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil || body.Group == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "send {\"group\": ...}"})
		return
	}
	group := strings.Join(strings.Fields(*body.Group), " ")
	if utf8.RuneCountInString(group) > maxGroupName {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("a group name has at most %d characters", maxGroupName)})
		return
	}
	updated, ok, err := h.store.update(instanceParam(r), false, true, func(i *Instance) { i.Group = group })
	if err != nil {
		h.logger.Warn("Failed to keep the hub's instances", "error", err)
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such instance"})
		return
	}
	writeJSON(w, http.StatusOK, updated.public())
}

func (h *Hub) removeInstance(w http.ResponseWriter, r *http.Request) {
	id := instanceParam(r)
	h.unwatch(id)
	removed, err := h.store.remove(id)
	if err != nil {
		h.logger.Warn("Failed to keep the hub's instances", "error", err)
	}
	if !removed {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such instance"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// hubHeartbeat is how often the browser's stream gets a heartbeat.
const hubHeartbeat = 15 * time.Second

// streamEvents sends the browser the instances (instances) and then each
// change: instance, with the instance, or removed, with its ID.
func (h *Hub) streamEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	instances, ch := h.store.subscribe()
	defer h.store.unsubscribe(ch)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	send := func(event string, data any) bool {
		encoded, err := json.Marshal(data)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, encoded); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	if !send("instances", map[string]any{"instances": publicList(instances)}) {
		return
	}
	heartbeat := time.NewTicker(hubHeartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if !send("heartbeat", map[string]time.Time{"time": h.now()}) {
				return
			}
		case event, ok := <-ch:
			if !ok {
				return
			}
			if event.Removed != "" {
				if !send("removed", map[string]string{"id": event.Removed}) {
					return
				}
				continue
			}
			if !send("instance", event.Instance.public()) {
				return
			}
		}
	}
}
