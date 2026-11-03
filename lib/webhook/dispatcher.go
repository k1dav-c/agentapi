package webhook

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/coder/quartz"

	"github.com/coder/agentapi/lib/convstate"
	"github.com/coder/agentapi/lib/instanceapi"
	mf "github.com/coder/agentapi/lib/msgfmt"
	"github.com/coder/agentapi/lib/netaddr"
)

const (
	webhookEventRunStatusChanged = "run.status_changed"
	// webhookEventTest is sent on request (POST /webhook/test), whatever
	// the events chosen.
	webhookEventTest          = instanceapi.EventWebhookTest
	defaultWebhookTimeout     = 10 * time.Second
	defaultWebhookMaxAttempts = 3
	defaultWebhookHeartbeat   = 60 * time.Second
	// The longest wait between two attempts of a delivery.
	maxWebhookRetryDelay = 30 * time.Second
	// How long shutting down waits for instance.stopped to be delivered,
	// at most: no more than half the time Stop has left, which the HTTP
	// server's drain and the state database need too.
	webhookStopTimeout = 2 * time.Second
	webhookQueueSize   = 1024
)

// EventTypes are the events a webhook can be sent, for
// --webhook-events. A pattern ending in .* selects a group, * all of them.
var EventTypes = []string{
	webhookEventRunStatusChanged,
	webhookEventInstanceStarted, webhookEventInstanceHeartbeat, webhookEventInstanceStopped,
	webhookEventTaskStarted, webhookEventTaskCompleted, webhookEventTaskFailed,
	webhookEventRequestOpened, webhookEventRequestClosed,
	webhookEventLoginRequired, webhookEventLoginCompleted,
	webhookEventLimitStopped, webhookEventLimitCleared,
	webhookEventAgentError,
	webhookEventMessageCreated, webhookEventToolCompleted,
}

// defaultWebhookEvents are the events sent when none are chosen: those
// AgentAPI sent before the others existed.
var defaultWebhookEvents = []string{webhookEventRunStatusChanged}

// Config is the webhook's configuration: the startup flags, and PUT
// /webhook's changes.
type Config struct {
	URL             string
	Timeout         time.Duration
	MaxAttempts     int
	PayloadTemplate string
	// Events are the event types to send, or patterns of them (task.*, *);
	// empty sends run.status_changed alone.
	Events []string
	// Secret, when set, signs every delivery with HMAC-SHA256
	// (X-AgentAPI-Signature-256).
	Secret string
	// Heartbeat is how often instance.heartbeat is sent; 0 takes the
	// default, a negative value turns it off.
	Heartbeat time.Duration
	// InstanceID names this AgentAPI in every event; empty takes
	// <hostname>:<port>.
	InstanceID string
	// PublicURL is where this AgentAPI can be reached, for the receiver to
	// link to its chat or call its API. It may hold {port}, {hostname} and
	// {ip}, which every event and summary fill in (publicURL).
	PublicURL string
}

func (c Config) validate() error {
	if c.URL != "" {
		if err := validateHTTPURL(c.URL, "webhook URL"); err != nil {
			return err
		}
	}
	if c.PublicURL != "" {
		// With sample values for the placeholders: a port must be a number.
		sample := instanceapi.ExpandIP(instanceapi.ExpandPlaceholders(c.PublicURL, 1), "192.0.2.1")
		if err := validateHTTPURL(sample, "public URL"); err != nil {
			return err
		}
	}
	if c.Timeout < 0 {
		return fmt.Errorf("webhook timeout must not be negative")
	}
	if c.MaxAttempts < 0 {
		return fmt.Errorf("webhook max attempts must not be negative")
	}
	for _, pattern := range c.Events {
		if !validWebhookEventPattern(pattern) {
			return fmt.Errorf("unknown webhook event %q; the events are %s, a group such as task.*, or *", pattern, strings.Join(EventTypes, ", "))
		}
	}
	return nil
}

func validateHTTPURL(raw, what string) error {
	parsed, err := url.ParseRequestURI(raw)
	if err != nil {
		return fmt.Errorf("invalid %s: %w", what, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("%s must use http or https", what)
	}
	if parsed.Host == "" {
		return fmt.Errorf("%s must include a host", what)
	}
	return nil
}

func validWebhookEventPattern(pattern string) bool {
	return slices.ContainsFunc(EventTypes, func(event string) bool { return webhookEventMatches(pattern, event) })
}

// webhookEventMatches reports whether pattern (an event type, a group such
// as task.*, or *) selects event.
func webhookEventMatches(pattern, event string) bool {
	if pattern == "*" || pattern == event {
		return true
	}
	group, ok := strings.CutSuffix(pattern, ".*")
	return ok && strings.HasPrefix(event, group+".")
}

// The webhook's envelope and the instance it names are the contract with
// the receivers that watch many AgentAPIs (lib/instanceapi).
type (
	WebhookEvent    = instanceapi.WebhookEvent
	WebhookInstance = instanceapi.WebhookInstance
)

// queuedEvent is an event waiting to be delivered.
type queuedEvent struct {
	WebhookEvent
	// done, if set, is closed once the event was delivered or given up on.
	done chan struct{}
}

type WebhookEventData struct {
	ServerID       string                `json:"server_id" doc:"Random identifier of this AgentAPI run, as server_id in GET /status."`
	Status         convstate.AgentStatus `json:"status" doc:"Current agent status."`
	PreviousStatus convstate.AgentStatus `json:"previous_status" doc:"Previous agent status."`
	AgentType      mf.AgentType          `json:"agent_type" doc:"Type of agent being used."`
	Transport      instanceapi.Transport `json:"transport" doc:"Backend transport being used."`
}

// WebhookTemplateData is the context available to custom payload templates.
type WebhookTemplateData struct {
	ID        string
	Type      string
	CreatedAt time.Time
	// The fields of run.status_changed; empty for other events.
	ServerID       string
	Status         string
	PreviousStatus string
	AgentType      string
	Transport      string
	// Instance is the AgentAPI that sent the event; Data the event's data
	// as JSON decodes it (a map), for {{.Data.prompt}} and the like.
	Instance *WebhookInstance
	Data     any
}

// Dispatcher delivers an AgentAPI's webhook events: one at a time from a
// queue, with retries, signed, and with instance.started, the heartbeats
// and instance.stopped. Queuing never blocks, so the emitter queues events
// under its lock.
type Dispatcher struct {
	mu        sync.RWMutex
	config    Config
	tmpl      *template.Template // nil when using the default JSON payload
	logger    *slog.Logger
	clock     quartz.Clock
	serverID  string
	agentType mf.AgentType
	transport instanceapi.Transport
	instance  WebhookInstance
	// startup is the configuration AgentAPI started with.
	startup Config
	queue   chan queuedEvent
	// snapshot, if set, gives the data of instance.started and
	// instance.heartbeat.
	snapshot func() any
	// configured, if set, is called after the configuration changed.
	configured func()
	// configChanged wakes the heartbeat loop when the interval changes.
	configChanged chan struct{}
	// ip finds the address {ip} in the public URL stands for. Only
	// RefreshIP asks it, outside any lock: a lookup may wait on DNS, and
	// the events are built under the emitter's lock.
	ip *netaddr.Resolver
	// refreshMu makes the lookups take turns, so an older answer can't
	// replace a newer one.
	refreshMu sync.Mutex
	// ipMu guards publicAddr, the address {ip} stands for in the events
	// and summaries; empty until one is found.
	ipMu       sync.Mutex
	publicAddr string
	// urlChanged, if set, is called (in a goroutine of its own) once the
	// public URL changed because {ip} did.
	urlChanged func()
}

// SetServerID sets the identifier of this AgentAPI run the events carry,
// the one the status reports. Call it before Start.
func (d *Dispatcher) SetServerID(id string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.serverID = id
}

// SetSnapshot has f give the data of instance.started, instance.heartbeat
// and instance.stopped. Call it before Start.
func (d *Dispatcher) SetSnapshot(f func() any) { d.snapshot = f }

// OnURLChange has f called, in a goroutine of its own, once the public URL
// changed because the address {ip} stands for did. Call it before Start.
func (d *Dispatcher) OnURLChange(f func()) { d.urlChanged = f }

// SetResolver replaces the lookup of the address {ip} stands for, for
// tests. It isn't synchronized with RefreshIP: set it while no lookup runs.
func (d *Dispatcher) SetResolver(r *netaddr.Resolver) { d.ip = r }

// SendHeartbeat queues an instance.heartbeat now, if the webhook sends it.
func (d *Dispatcher) SendHeartbeat() {
	d.emit(webhookEventInstanceHeartbeat, d.snapshotData())
}

func parseWebhookTemplate(templateStr string) (*template.Template, error) {
	if templateStr == "" {
		return nil, nil
	}
	tmpl, err := template.New("webhook").Parse(templateStr)
	if err != nil {
		return nil, fmt.Errorf("invalid webhook payload template: %w", err)
	}
	// A field WebhookTemplateData lacks (.RunID before 1.0) fails only when
	// the template runs, so run it once on an empty event: it would fail
	// every delivery. Other errors may come from the empty data alone.
	sample := WebhookTemplateData{Instance: &WebhookInstance{}, Data: map[string]any{}}
	if err := tmpl.Execute(io.Discard, sample); err != nil && strings.Contains(err.Error(), "can't evaluate field") {
		return nil, fmt.Errorf("invalid webhook payload template: %w", err)
	}
	return tmpl, nil
}

// Check returns the error New would return for config.
func Check(config Config) error {
	_, err := checkedTemplate(config)
	return err
}

// checkedTemplate checks config and returns its payload template, nil for
// none.
func checkedTemplate(config Config) (*template.Template, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	return parseWebhookTemplate(config.PayloadTemplate)
}

// New returns a dispatcher of the events of an AgentAPI of agentType on
// transport, which Start starts sending.
func New(config Config, logger *slog.Logger, agentType mf.AgentType, transport instanceapi.Transport, clock quartz.Clock) (*Dispatcher, error) {
	tmpl, err := checkedTemplate(config)
	if err != nil {
		return nil, err
	}
	config = webhookConfigWithDefaults(config)
	serverID := randomWebhookID()
	return &Dispatcher{
		config:        config,
		tmpl:          tmpl,
		logger:        logger,
		clock:         clock,
		serverID:      serverID,
		agentType:     agentType,
		transport:     transport,
		instance:      WebhookInstance{ServerID: serverID, AgentType: agentType, Transport: transport},
		startup:       config,
		queue:         make(chan queuedEvent, webhookQueueSize),
		configChanged: make(chan struct{}, 1),
		ip:            &netaddr.Resolver{Now: func() time.Time { return clock.Now() }, Logger: logger},
	}, nil
}

func webhookConfigWithDefaults(config Config) Config {
	if config.Timeout == 0 {
		config.Timeout = defaultWebhookTimeout
	}
	if config.MaxAttempts == 0 {
		config.MaxAttempts = defaultWebhookMaxAttempts
	}
	if len(config.Events) == 0 {
		config.Events = defaultWebhookEvents
	}
	if config.Heartbeat == 0 {
		config.Heartbeat = defaultWebhookHeartbeat
	}
	return config
}

// storedWebhook is the webhook as the API left it, kept in the state
// database with the startup configuration it replaced: it applies again
// after a restart, unless AgentAPI starts with another configuration.
type storedWebhook struct {
	Startup string `json:"startup"`
	Config  Config `json:"config"`
}

// webhookFingerprint identifies a startup configuration.
func webhookFingerprint(config Config) string {
	data, _ := json.Marshal(config)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Stored is the configuration to keep in the state database.
func (d *Dispatcher) Stored() string {
	data, _ := json.Marshal(storedWebhook{Startup: webhookFingerprint(d.startup), Config: d.Config()})
	return string(data)
}

// Restore applies the configuration kept in the state database, unless
// AgentAPI started with another one than it replaced. It reports whether it
// did.
func (d *Dispatcher) Restore(stored string) (bool, error) {
	var s storedWebhook
	if err := json.Unmarshal([]byte(stored), &s); err != nil {
		return false, err
	}
	if s.Startup != webhookFingerprint(d.startup) {
		return false, nil
	}
	return true, d.UpdateConfig(s.Config)
}

// SetInstance describes the AgentAPI that sends the events.
func (d *Dispatcher) SetInstance(instance WebhookInstance) {
	d.mu.Lock()
	defer d.mu.Unlock()
	instance.ServerID = d.serverID
	instance.AgentType = d.agentType
	instance.Transport = d.transport
	d.instance = instance
}

// Instance is the AgentAPI the events name, with the instance ID and the
// public URL the configuration sets.
func (d *Dispatcher) Instance() WebhookInstance {
	d.mu.RLock()
	instance := d.instance
	config := d.config
	d.mu.RUnlock()
	if config.InstanceID != "" {
		instance.ID = config.InstanceID
	}
	if config.PublicURL != "" {
		if expanded := d.publicURL(config, instance.Port); expanded != "" {
			instance.URL = expanded
		}
	}
	return instance
}

// publicURL is the public URL with its placeholders filled in. {ip} is
// the address RefreshIP last found: empty (no URL) until it found one.
func (d *Dispatcher) publicURL(config Config, port int) string {
	expanded := instanceapi.ExpandPlaceholders(config.PublicURL, port)
	if !instanceapi.HasIP(expanded) {
		return expanded
	}
	ip := d.cachedIP()
	if ip == "" {
		return ""
	}
	return instanceapi.ExpandIP(expanded, ip)
}

func (d *Dispatcher) cachedIP() string {
	d.ipMu.Lock()
	defer d.ipMu.Unlock()
	return d.publicAddr
}

// RefreshIP looks up the address {ip} in the public URL stands for: the
// one this machine reaches the webhook's receiver from, looked up again
// once the last answer is 30 seconds old, so after DHCP gives the machine
// another address the next heartbeat names it. When no address is found
// the last one stays. It may wait on DNS (for at most
// netaddr.DialTimeout), so it runs before the first event, when the
// configuration changes, before every heartbeat and on a loop of its own,
// never under a lock the events are built under.
func (d *Dispatcher) RefreshIP(ctx context.Context) {
	config := d.Config()
	if !instanceapi.HasIP(config.PublicURL) {
		return
	}
	d.refreshMu.Lock()
	ip, _ := d.ip.ResolveContext(ctx, netaddr.Target(config.URL))
	d.refreshMu.Unlock()
	if ip == "" {
		return
	}
	d.ipMu.Lock()
	previous := d.publicAddr
	d.publicAddr = ip
	d.ipMu.Unlock()
	if previous != "" && previous != ip {
		d.logger.Info("The public URL changed with this machine's address", "ip", ip)
		if d.urlChanged != nil {
			go d.urlChanged()
		}
	}
}

// refreshIPs keeps {ip} current between heartbeats, for the summary, the
// registry and the allowed hosts, until ctx ends.
func (d *Dispatcher) refreshIPs(ctx context.Context) {
	ticker := d.clock.NewTicker(netaddr.DefaultTTL, "webhook", "ip")
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.RefreshIP(ctx)
		}
	}
}

// PublicIP is the address {ip} in the public URL stands for now; empty
// when it holds no {ip}.
func (d *Dispatcher) PublicIP() string {
	if !instanceapi.HasIP(d.Config().PublicURL) {
		return ""
	}
	return d.cachedIP()
}

// Config is the configuration in use.
func (d *Dispatcher) Config() Config {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.config
}

// UpdateConfig replaces the configuration, for the events queued from now
// on.
func (d *Dispatcher) UpdateConfig(config Config) error {
	config = webhookConfigWithDefaults(config)
	if err := config.validate(); err != nil {
		return err
	}
	tmpl, err := parseWebhookTemplate(config.PayloadTemplate)
	if err != nil {
		return err
	}
	d.mu.Lock()
	d.config = config
	d.tmpl = tmpl
	d.mu.Unlock()
	// The next events name the receiver's route.
	d.RefreshIP(context.Background())
	select {
	case d.configChanged <- struct{}{}:
	default:
	}
	if d.configured != nil {
		d.configured()
	}
	return nil
}

// wants reports whether events of type eventType are sent.
func (d *Dispatcher) wants(eventType string) bool {
	config := d.Config()
	return config.URL != "" && slices.ContainsFunc(config.Events, func(pattern string) bool { return webhookEventMatches(pattern, eventType) })
}

func randomWebhookID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(value[:])
}

// StatusChanged queues run.status_changed, for the emitter's status change
// handler.
func (d *Dispatcher) StatusChanged(previous, current convstate.AgentStatus) {
	d.emit(webhookEventRunStatusChanged, WebhookEventData{
		ServerID:       d.serverID,
		Status:         current,
		PreviousStatus: previous,
		AgentType:      d.agentType,
		Transport:      d.transport,
	})
}

// emit queues an event of type eventType with data, if such events are
// sent. It never blocks: when the queue is full the event is dropped.
func (d *Dispatcher) emit(eventType string, data any) bool {
	return d.enqueue(eventType, data, nil)
}

func (d *Dispatcher) enqueue(eventType string, data any, done chan struct{}) bool {
	if !d.wants(eventType) {
		return false
	}
	instance := d.Instance()
	event := queuedEvent{WebhookEvent: WebhookEvent{
		ID:        randomWebhookID(),
		Type:      eventType,
		CreatedAt: d.clock.Now().UTC(),
		Instance:  &instance,
		Data:      data,
	}, done: done}
	select {
	case d.queue <- event:
		return true
	default:
		d.logger.Warn("Webhook queue is full; dropping event", "event_id", event.ID, "event_type", event.Type)
		return false
	}
}

// Start delivers the queued events, in order, until ctx ends, and sends
// instance.started and the heartbeats.
func (d *Dispatcher) Start(ctx context.Context) {
	// instance.started names the address.
	d.RefreshIP(ctx)
	d.emit(webhookEventInstanceStarted, d.snapshotData())
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case event := <-d.queue:
				d.deliver(ctx, event)
			}
		}
	}()
	go d.heartbeats(ctx)
	go d.refreshIPs(ctx)
}

func (d *Dispatcher) snapshotData() any {
	if d.snapshot == nil {
		return struct{}{}
	}
	return d.snapshot()
}

func (d *Dispatcher) heartbeats(ctx context.Context) {
	for {
		interval := d.Config().Heartbeat
		var timer *quartz.Timer
		var tick <-chan time.Time
		if interval > 0 {
			timer = d.clock.NewTimer(interval, "webhook", "heartbeat")
			tick = timer.C
		}
		select {
		case <-ctx.Done():
		case <-d.configChanged:
		case <-tick:
			d.RefreshIP(ctx)
			d.emit(webhookEventInstanceHeartbeat, d.snapshotData())
		}
		if timer != nil {
			timer.Stop()
		}
		if ctx.Err() != nil {
			return
		}
	}
}

// Stop sends instance.stopped and waits for it to be delivered, for at
// most webhookStopTimeout and half the time ctx has left: the rest is for
// what Stop does after. Call it before ending the context Start was given.
func (d *Dispatcher) Stop(ctx context.Context) {
	if deadline, ok := ctx.Deadline(); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Until(deadline)/2)
		defer cancel()
	}
	done := make(chan struct{})
	if !d.enqueue(webhookEventInstanceStopped, d.snapshotData(), done) {
		return
	}
	timer := d.clock.NewTimer(webhookStopTimeout, "webhook", "stop")
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		d.logger.Warn("Gave up waiting for the instance.stopped webhook")
	case <-ctx.Done():
	}
}

func (d *Dispatcher) renderBody(event WebhookEvent) ([]byte, error) {
	d.mu.RLock()
	tmpl := d.tmpl
	d.mu.RUnlock()

	if tmpl == nil {
		return json.Marshal(event)
	}

	data := WebhookTemplateData{
		ID:        event.ID,
		Type:      event.Type,
		CreatedAt: event.CreatedAt,
		Instance:  event.Instance,
	}
	if status, ok := event.Data.(WebhookEventData); ok {
		data.ServerID = status.ServerID
		data.Status = string(status.Status)
		data.PreviousStatus = string(status.PreviousStatus)
		data.AgentType = string(status.AgentType)
		data.Transport = string(status.Transport)
	}
	// The data as JSON has it, so templates use its JSON field names.
	raw, err := json.Marshal(event.Data)
	if err != nil {
		return nil, fmt.Errorf("encode webhook data: %w", err)
	}
	if err := json.Unmarshal(raw, &data.Data); err != nil {
		return nil, fmt.Errorf("decode webhook data: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("execute webhook template: %w", err)
	}
	return buf.Bytes(), nil
}

// webhookRetryDelay is the wait after failed attempt number attempt: a
// second, then twice as long each time, up to maxWebhookRetryDelay.
func webhookRetryDelay(attempt int) time.Duration {
	delay := time.Second << min(attempt-1, 16)
	return min(delay, maxWebhookRetryDelay)
}

func (d *Dispatcher) deliver(ctx context.Context, event queuedEvent) {
	if event.done != nil {
		defer close(event.done)
	}
	body, err := d.renderBody(event.WebhookEvent)
	if err != nil {
		d.logger.Error("Failed to render webhook body", "error", err)
		return
	}
	config := d.Config()
	client := webhookClient(config.Timeout)
	for attempt := 1; attempt <= config.MaxAttempts; attempt++ {
		err := d.sendWithConfig(ctx, config, client, event.WebhookEvent, body)
		if err == nil {
			return
		}
		var permanent *permanentWebhookError
		if attempt == config.MaxAttempts || errors.As(err, &permanent) {
			d.logger.Error("Webhook delivery failed", "event_id", event.ID, "event_type", event.Type, "attempts", attempt, "error", err)
			return
		}
		d.logger.Warn("Webhook delivery failed; retrying", "event_id", event.ID, "event_type", event.Type, "attempt", attempt, "error", err)
		timer := d.clock.NewTimer(webhookRetryDelay(attempt), "webhook", "retry")
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// webhookClient sends deliveries, each within timeout. A redirect is the
// receiver's answer, not a hop: followed, the POST comes back as a GET on
// 302 and 303, and a sign-in page that answers 200 passes for a delivery.
func webhookClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// permanentWebhookError is a response that trying again won't change: a
// client error other than a timeout or rate limit.
type permanentWebhookError struct{ status string }

func (e *permanentWebhookError) Error() string {
	return fmt.Sprintf("unexpected HTTP status %s", e.status)
}

func (d *Dispatcher) sendWithConfig(ctx context.Context, config Config, client *http.Client, event WebhookEvent, body []byte) error {
	_, err := d.send(ctx, config, client, event, body)
	return err
}

// send POSTs one delivery of event and returns the receiver's status.
func (d *Dispatcher) send(ctx context.Context, config Config, client *http.Client, event WebhookEvent, body []byte) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, config.URL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	timestamp := strconv.FormatInt(event.CreatedAt.Unix(), 10)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "AgentAPI-Webhook/1.0")
	request.Header.Set(instanceapi.HeaderDelivery, event.ID)
	request.Header.Set(instanceapi.HeaderEvent, event.Type)
	request.Header.Set(instanceapi.HeaderTimestamp, timestamp)
	if event.Instance != nil && event.Instance.ID != "" {
		request.Header.Set(instanceapi.HeaderInstance, event.Instance.ID)
	}
	if config.Secret != "" {
		request.Header.Set(instanceapi.HeaderSignature, instanceapi.Sign(config.Secret, timestamp, body))
	}

	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return response.Status, nil
	}
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		target := response.Header.Get("Location")
		if location, err := response.Location(); err == nil {
			target = location.String()
		}
		return response.Status, fmt.Errorf("unexpected HTTP status %s: a redirect to %s, which webhooks don't follow (a sign-in page in front of the receiver?)", response.Status, target)
	}
	if response.StatusCode >= 400 && response.StatusCode < 500 &&
		response.StatusCode != http.StatusRequestTimeout && response.StatusCode != http.StatusTooManyRequests {
		return response.Status, &permanentWebhookError{status: response.Status}
	}
	return response.Status, fmt.Errorf("unexpected HTTP status %s", response.Status)
}

// Test delivers a webhook.test event once, now, and reports the outcome.
func (d *Dispatcher) Test(ctx context.Context) (status string, err error) {
	config := d.Config()
	if config.URL == "" {
		return "", errors.New("no webhook URL is set")
	}
	instance := d.Instance()
	event := WebhookEvent{
		ID:        randomWebhookID(),
		Type:      webhookEventTest,
		CreatedAt: d.clock.Now().UTC(),
		Instance:  &instance,
		Data:      map[string]string{"message": "A test event from AgentAPI"},
	}
	body, err := d.renderBody(event)
	if err != nil {
		return "", err
	}
	return d.send(ctx, config, webhookClient(config.Timeout), event, body)
}
