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
	"cmp"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coder/quartz"
	"github.com/go-chi/chi/v5"

	"github.com/coder/agentapi/internal/version"
	"github.com/coder/agentapi/lib/httpapi"
	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/netaddr"
	"github.com/coder/agentapi/lib/sse"
)

// Config is how the hub runs.
type Config struct {
	// Token is what a browser signs in with; it is required, unless
	// NoToken.
	Token string
	// NoToken lets in everyone who reaches the hub, without signing in:
	// for a hub only a sign-in of its own reaches (a Coder workspace app,
	// a private VPN). Token must be empty.
	NoToken bool
	// Secret checks the instances' webhook events; it is required, since
	// an event names the URL the hub sends an instance token to.
	Secret string
	// InstanceToken is sent to the instances that don't have their own.
	InstanceToken string
	// AllowedHosts, when set, are the names browsers may reach the hub by,
	// besides IP addresses and localhost ("*" is any): another name points
	// at it only to get around the browser's same-origin rule (DNS
	// rebinding), which matters most with NoToken. Empty allows any.
	AllowedHosts []string
	// Bind and Port are where the hub listens (an empty Bind is every
	// interface), which the URLs machines join it by name (GET /api/join).
	Bind string
	Port int
	// StatePath keeps the instances across restarts; empty keeps none.
	StatePath string
	// ChatBasePath is where the hub serves the chat UI, by default /chat.
	ChatBasePath string
	Logger       *slog.Logger
	// Client reaches the instances; nil is a client that uses the proxy
	// of the environment (HTTP_PROXY, ALL_PROXY for a VPN in userspace).
	Client *http.Client
	// Clock is the clock; nil is the real one.
	Clock quartz.Clock
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
	deliveries *deliveries
	// badSignatures limits the log of events refused for their signature.
	badSignatures logLimiter
	clock         quartz.Clock
	// interfaces lists the machine's network interfaces, for the join URLs.
	interfaces func() ([]netaddr.Interface, error)

	ctx      context.Context
	watchMu  sync.Mutex
	watchers map[string]context.CancelFunc
	watchWG  sync.WaitGroup
}

// New returns the hub of config, with the instances it kept.
func New(config Config) (*Hub, error) {
	switch {
	case config.NoToken && config.Token != "":
		return nil, errors.New("the hub takes a token to sign in with (--token) or none (--no-token), not both")
	case !config.NoToken && config.Token == "":
		return nil, errors.New("the hub needs a token to sign in with (--token), or --no-token to let in everyone who reaches it")
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
	if config.Clock == nil {
		config.Clock = quartz.NewReal()
	}
	if config.ConnectGrace == 0 {
		config.ConnectGrace = defaultConnectGrace
	}
	client := config.Client
	if client == nil {
		client = &http.Client{Transport: newTransport()}
	}
	st, err := newStore(config.StatePath, config.Logger, config.Clock)
	if err != nil {
		return nil, err
	}
	var deliveriesFile string
	if config.StatePath != "" {
		deliveriesFile = deliveriesPath(config.StatePath)
	}
	return &Hub{
		config: config, logger: config.Logger, store: st, client: client, clock: config.Clock,
		deliveries: newDeliveries(deliveriesFile, config.Logger, config.Clock.Now()),
		interfaces: netaddr.Interfaces, watchers: map[string]context.CancelFunc{},
	}, nil
}

// newTransport is how the hub reaches the instances by default: the
// environment's proxy, and http.DefaultTransport's limits on connecting (a
// dial and TLS handshake that time out, keep-alives, idle connections), so
// an instance that doesn't answer a SYN doesn't keep a watch or a proxied
// request waiting for the system's TCP timeout. A response's headers must
// come within 15 seconds; its body has no limit, since the summary streams
// and the proxy's event streams stay open.
func newTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyFromEnvironment
	transport.ResponseHeaderTimeout = 15 * time.Second
	return transport
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

// Close writes the changes the hub's files haven't kept yet, and stops
// writing them: it writes a change within a second, and on Close. Call it
// once the hub stops, after Run returned.
func (h *Hub) Close() error {
	return errors.Join(h.store.close(), h.deliveries.close())
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
	// The instances' responses through the proxy come compressed already.
	r.Use(httpapi.Compress())
	// Instances sign their webhook events, whatever name or origin they
	// come by.
	r.Post("/hooks", h.receiveWebhook)
	r.Group(func(r chi.Router) {
		r.Use(h.checkHost)
		r.Use(httpapi.CrossOriginProtection(nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h.logger.Warn("Refused a request a page of another origin sent", "method", r.Method, "path", r.URL.Path, "origin", r.Header.Get("Origin"))
			writeError(w, http.StatusForbidden, "a page of another origin sent this request")
		})))
		h.routes(r)
	})
	return r
}

// routes are the hub's routes for browsers.
func (h *Hub) routes(r chi.Router) {
	r.Get("/api/session", h.getSession)
	r.Post("/api/session", h.createSession)
	r.Delete("/api/session", h.deleteSession)
	r.Group(func(r chi.Router) {
		r.Use(h.requireSession)
		r.Get("/api/instances", h.listInstances)
		r.Post("/api/instances", h.addInstance)
		r.Patch("/api/instances/{id}", h.updateInstance)
		r.Delete("/api/instances/{id}", h.removeInstance)
		r.Put("/api/pins", h.setPins)
		r.Get("/api/events", h.streamEvents)
		r.Get("/api/join", h.getJoin)
		r.Handle("/i/{id}", http.HandlerFunc(h.proxy))
		r.Handle("/i/{id}/*", http.HandlerFunc(h.proxy))
	})
	chat := http.StripPrefix(h.config.ChatBasePath, httpapi.FileServerWithIndexFallback(h.config.ChatBasePath))
	r.Handle(h.config.ChatBasePath, chat)
	r.Handle(h.config.ChatBasePath+"/*", chat)
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, h.config.ChatBasePath+"/hub/", http.StatusTemporaryRedirect)
	})
}

// checkHost refuses a request for a name the hub doesn't answer to, when
// AllowedHosts are set. IP addresses and localhost always pass: a page
// can't take over an address the way it takes over a name it controls.
func (h *Hub) checkHost(next http.Handler) http.Handler {
	if len(h.config.AllowedHosts) == 0 || slices.Contains(h.config.AllowedHosts, "*") {
		return next
	}
	allowed := make(map[string]bool, len(h.config.AllowedHosts))
	for _, host := range h.config.AllowedHosts {
		allowed[strings.ToLower(host)] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if name, _, err := net.SplitHostPort(host); err == nil {
			host = name
		}
		host = strings.ToLower(strings.Trim(host, "[]"))
		if net.ParseIP(host) != nil || host == "localhost" || allowed[host] {
			next.ServeHTTP(w, r)
			return
		}
		h.logger.Warn("Refused a request for a name not in --allowed-hosts", "host", r.Host, "path", r.URL.Path)
		writeError(w, http.StatusMisdirectedRequest, "the hub doesn't answer to "+host+"; add it to --allowed-hosts")
	})
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

// signedIn reports whether r carries the session cookie or the hub token;
// everyone is, on a hub without a token.
func (h *Hub) signedIn(r *http.Request) bool {
	if h.config.NoToken {
		return true
	}
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
			writeError(w, http.StatusUnauthorized, "sign in to the hub")
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
		writeError(w, http.StatusBadRequest, "send {\"token\": ...}")
		return
	}
	if h.config.NoToken {
		writeJSON(w, http.StatusOK, map[string]bool{"authenticated": true})
		return
	}
	if !hmac.Equal([]byte(body.Token), []byte(h.config.Token)) {
		writeError(w, http.StatusUnauthorized, "wrong token")
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

// writeError answers {"error": message}.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// instanceList is GET /api/instances, and the first event of the browser's
// stream: the instances, without their tokens, and the hub's own version,
// which the page tells the older instances by.
type instanceList struct {
	Instances  []Instance `json:"instances"`
	HubVersion string     `json:"hub_version"`
}

func newInstanceList(instances []Instance, now time.Time) instanceList {
	list := make([]Instance, len(instances))
	for i, instance := range instances {
		list[i] = instance.sent(now)
	}
	return instanceList{Instances: list, HubVersion: version.Version}
}

func (h *Hub) listInstances(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, newInstanceList(h.store.list(), h.clock.Now()))
}

// addInstance adds an instance by its URL (POST /api/instances): the hub
// asks it for its summary, which says its ID and name.
func (h *Hub) addInstance(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL   string `json:"url"`
		Token string `json:"token"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "send {\"url\": ..., \"token\": ...}")
		return
	}
	u, err := url.Parse(body.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		writeError(w, http.StatusBadRequest, "url must be an http or https URL")
		return
	}
	base := strings.TrimRight(body.URL, "/")
	summary, err := h.fetchSummary(r.Context(), base, body.Token)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	id := summary.Instance.ID
	if id == "" {
		id = u.Host
	}
	if existing, ok := h.store.get(id); ok && existing.URL != base {
		// Adding a URL is no way to move an instance: the hub would send
		// its token there.
		writeError(w, http.StatusConflict, "the hub has "+id+" at "+existing.URL+" already; forget it first")
		return
	}
	instance, _ := h.store.update(id, true, func(i *Instance) {
		i.WebhookInstance = summary.Instance
		i.ID = id
		i.URL = base
		i.Token = body.Token
		i.Source = SourceManual
		i.LastSeen = h.clock.Now()
		i.Summary = summary
		i.Connection = ConnectionConnecting
		i.Error = ""
	})
	h.watch(instance)
	writeJSON(w, http.StatusOK, instance.sent(h.clock.Now()))
}

func (h *Hub) fetchSummary(ctx context.Context, base, token string) (*instanceapi.SummaryBody, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/summary", nil)
	if err != nil {
		return nil, err
	}
	if token = cmp.Or(token, h.config.InstanceToken); token != "" {
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
	var summary instanceapi.SummaryBody
	if err := json.NewDecoder(resp.Body).Decode(&summary); err != nil {
		return nil, fmt.Errorf("GET /summary: %w", err)
	}
	return &summary, nil
}

// maxGroupName is the longest group name, in characters.
const maxGroupName = 40

// updateInstance changes what the hub's page sets on an instance (PATCH
// /api/instances/{id}): {"group": "..."} puts it in a group, "" in none;
// {"muted": true} mutes what it waits for now, until that changes, and
// {"muted": false} ends the mute; {"pinned": true} pins it below the
// pinned ones, and {"pinned": false} unpins it.
func (h *Hub) updateInstance(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Group  *string `json:"group"`
		Muted  *bool   `json:"muted"`
		Pinned *bool   `json:"pinned"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil || (body.Group == nil && body.Muted == nil && body.Pinned == nil) {
		writeError(w, http.StatusBadRequest, "send {\"group\": ...}, {\"muted\": ...} or {\"pinned\": ...}")
		return
	}
	var group string
	if body.Group != nil {
		group = strings.Join(strings.Fields(*body.Group), " ")
		if utf8.RuneCountInString(group) > maxGroupName {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("a group name has at most %d characters", maxGroupName))
			return
		}
	}
	var nothingToMute bool
	updated, ok := h.store.update(instanceParam(r), false, func(i *Instance) {
		if body.Muted != nil {
			key := ""
			if *body.Muted {
				key = instanceapi.AttentionKey(i.Summary)
				nothingToMute = key == ""
			}
			if nothingToMute {
				return
			}
			i.Muted = key
		}
		if body.Group != nil {
			i.Group = group
		}
		switch {
		case body.Pinned == nil:
		case !*body.Pinned:
			i.Pin = 0
		case i.Pin == 0:
			i.Pin = pinLast
		}
	})
	if !ok {
		writeError(w, http.StatusNotFound, "no such instance")
		return
	}
	if nothingToMute {
		writeError(w, http.StatusConflict, "it doesn't need you now: nothing to mute")
		return
	}
	writeJSON(w, http.StatusOK, updated.sent(h.clock.Now()))
}

// setPins orders the instances pinned to the top of the hub's page (PUT
// /api/pins): {"ids": [...]} lists each of them once, from the top. One
// the hub doesn't have is 404; a list that isn't exactly the pinned ones
// (one was pinned, unpinned or forgotten since) is 409, with the pinned
// ones in their order.
func (h *Hub) setPins(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil || body.IDs == nil {
		writeError(w, http.StatusBadRequest, "send {\"ids\": [...]}: the pinned instances, from the top")
		return
	}
	listed := make(map[string]bool, len(body.IDs))
	for _, id := range body.IDs {
		if listed[id] {
			writeError(w, http.StatusBadRequest, id+" is listed twice")
			return
		}
		listed[id] = true
	}
	pins, err := h.store.setPins(body.IDs)
	var unknown unknownInstanceError
	switch {
	case errors.As(err, &unknown):
		writeError(w, http.StatusNotFound, err.Error())
		return
	case errors.Is(err, errPinsDiffer):
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "ids": pins})
		return
	}
	writeJSON(w, http.StatusOK, map[string][]string{"ids": pins})
}

func (h *Hub) removeInstance(w http.ResponseWriter, r *http.Request) {
	id := instanceParam(r)
	h.unwatch(id)
	if !h.store.remove(id) {
		writeError(w, http.StatusNotFound, "no such instance")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// hubHeartbeat is how often the browser's stream gets a heartbeat.
const hubHeartbeat = 15 * time.Second

// streamEvents sends the browser the instances and the hub's version
// (instances), and then each change: instance, with the instance, or
// removed, with its ID.
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
		if err := sse.Write(w, event, encoded); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	if !send("instances", newInstanceList(instances, h.clock.Now())) {
		return
	}
	heartbeat := h.clock.NewTicker(hubHeartbeat, "hub", "browser heartbeat")
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if !send("heartbeat", map[string]time.Time{"time": h.clock.Now()}) {
				return
			}
			// The work times that moved come as changes, to every
			// subscriber; the store tells each instance once.
			h.store.refreshWork()
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
			if !send("instance", event.Instance.sent(h.clock.Now())) {
				return
			}
		}
	}
}
