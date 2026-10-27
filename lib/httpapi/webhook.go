package httpapi

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

	"github.com/danielgtaylor/huma/v2"

	"github.com/coder/quartz"

	"github.com/coder/agentapi/lib/instanceapi"
	mf "github.com/coder/agentapi/lib/msgfmt"
	"github.com/coder/agentapi/lib/statedb"
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
	// How long shutting down waits for instance.stopped to be delivered.
	webhookStopTimeout = 5 * time.Second
	webhookQueueSize   = 1024
)

// WebhookEventTypes are the events a webhook can be sent, for
// --webhook-events. A pattern ending in .* selects a group, * all of them.
var WebhookEventTypes = []string{
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

type WebhookConfig struct {
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
	// link to its chat or call its API.
	PublicURL string
}

func (c WebhookConfig) validate() error {
	if c.URL != "" {
		if err := validateHTTPURL(c.URL, "webhook URL"); err != nil {
			return err
		}
	}
	if c.PublicURL != "" {
		if err := validateHTTPURL(c.PublicURL, "public URL"); err != nil {
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
			return fmt.Errorf("unknown webhook event %q; the events are %s, a group such as task.*, or *", pattern, strings.Join(WebhookEventTypes, ", "))
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
	return slices.ContainsFunc(WebhookEventTypes, func(event string) bool { return webhookEventMatches(pattern, event) })
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
	ServerID       string       `json:"server_id" doc:"Random identifier of this AgentAPI run, as server_id in GET /status."`
	Status         AgentStatus  `json:"status" doc:"Current agent status."`
	PreviousStatus AgentStatus  `json:"previous_status" doc:"Previous agent status."`
	AgentType      mf.AgentType `json:"agent_type" doc:"Type of agent being used."`
	Transport      Transport    `json:"transport" doc:"Backend transport being used."`
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

type webhookDispatcher struct {
	mu        sync.RWMutex
	config    WebhookConfig
	tmpl      *template.Template // nil when using the default JSON payload
	logger    *slog.Logger
	clock     quartz.Clock
	serverID  string
	agentType mf.AgentType
	transport Transport
	instance  WebhookInstance
	// startup is the configuration AgentAPI started with.
	startup WebhookConfig
	queue   chan queuedEvent
	// snapshot, if set, gives the data of instance.started and
	// instance.heartbeat.
	snapshot func() any
	// configChanged wakes the heartbeat loop when the interval changes.
	configChanged chan struct{}
}

// ExpandPublicURL fills in a public URL's placeholders; see
// instanceapi.ExpandPublicURL.
func ExpandPublicURL(publicURL string, port int) string {
	return instanceapi.ExpandPublicURL(publicURL, port)
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

func newWebhookDispatcher(config WebhookConfig, logger *slog.Logger, agentType mf.AgentType, transport Transport, clock quartz.Clock) (*webhookDispatcher, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	config = webhookConfigWithDefaults(config)
	tmpl, err := parseWebhookTemplate(config.PayloadTemplate)
	if err != nil {
		return nil, err
	}
	serverID := randomWebhookID()
	return &webhookDispatcher{
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
	}, nil
}

func webhookConfigWithDefaults(config WebhookConfig) WebhookConfig {
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
	Startup string        `json:"startup"`
	Config  WebhookConfig `json:"config"`
}

// webhookFingerprint identifies a startup configuration.
func webhookFingerprint(config WebhookConfig) string {
	data, _ := json.Marshal(config)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// stored is the configuration to keep in the state database.
func (d *webhookDispatcher) stored() string {
	data, _ := json.Marshal(storedWebhook{Startup: webhookFingerprint(d.startup), Config: d.configSnapshot()})
	return string(data)
}

// restore applies the configuration kept in the state database, unless
// AgentAPI started with another one than it replaced. It reports whether it
// did.
func (d *webhookDispatcher) restore(stored string) (bool, error) {
	var s storedWebhook
	if err := json.Unmarshal([]byte(stored), &s); err != nil {
		return false, err
	}
	if s.Startup != webhookFingerprint(d.startup) {
		return false, nil
	}
	return true, d.updateConfig(s.Config)
}

// setInstance describes the AgentAPI that sends the events.
func (d *webhookDispatcher) setInstance(instance WebhookInstance) {
	d.mu.Lock()
	defer d.mu.Unlock()
	instance.ServerID = d.serverID
	instance.AgentType = d.agentType
	instance.Transport = d.transport
	d.instance = instance
}

func (d *webhookDispatcher) instanceSnapshot() WebhookInstance {
	d.mu.RLock()
	defer d.mu.RUnlock()
	instance := d.instance
	if d.config.InstanceID != "" {
		instance.ID = d.config.InstanceID
	}
	if d.config.PublicURL != "" {
		instance.URL = d.config.PublicURL
	}
	return instance
}

func (d *webhookDispatcher) configSnapshot() WebhookConfig {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.config
}

func (d *webhookDispatcher) updateConfig(config WebhookConfig) error {
	config = webhookConfigWithDefaults(config)
	if err := config.validate(); err != nil {
		return err
	}
	tmpl, err := parseWebhookTemplate(config.PayloadTemplate)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.config = config
	d.tmpl = tmpl
	select {
	case d.configChanged <- struct{}{}:
	default:
	}
	return nil
}

// wants reports whether events of type eventType are sent.
func (d *webhookDispatcher) wants(eventType string) bool {
	config := d.configSnapshot()
	return config.URL != "" && slices.ContainsFunc(config.Events, func(pattern string) bool { return webhookEventMatches(pattern, eventType) })
}

func randomWebhookID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(value[:])
}

func (d *webhookDispatcher) statusChanged(previous, current AgentStatus) {
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
func (d *webhookDispatcher) emit(eventType string, data any) bool {
	return d.enqueue(eventType, data, nil)
}

func (d *webhookDispatcher) enqueue(eventType string, data any, done chan struct{}) bool {
	if !d.wants(eventType) {
		return false
	}
	instance := d.instanceSnapshot()
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

// start delivers the queued events, in order, until ctx ends, and sends
// instance.started and the heartbeats.
func (d *webhookDispatcher) start(ctx context.Context) {
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
}

func (d *webhookDispatcher) snapshotData() any {
	if d.snapshot == nil {
		return struct{}{}
	}
	return d.snapshot()
}

func (d *webhookDispatcher) heartbeats(ctx context.Context) {
	for {
		interval := d.configSnapshot().Heartbeat
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

// stop sends instance.stopped and waits, until ctx ends or for at most
// webhookStopTimeout, for it to be delivered. Call it before ending the
// context start was given.
func (d *webhookDispatcher) stop(ctx context.Context) {
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

func (d *webhookDispatcher) renderBody(event WebhookEvent) ([]byte, error) {
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

func (d *webhookDispatcher) deliver(ctx context.Context, event queuedEvent) {
	if event.done != nil {
		defer close(event.done)
	}
	body, err := d.renderBody(event.WebhookEvent)
	if err != nil {
		d.logger.Error("Failed to render webhook body", "error", err)
		return
	}
	config := d.configSnapshot()
	client := &http.Client{Timeout: config.Timeout}
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

// permanentWebhookError is a response that trying again won't change: a
// client error other than a timeout or rate limit.
type permanentWebhookError struct{ status string }

func (e *permanentWebhookError) Error() string {
	return fmt.Sprintf("unexpected HTTP status %s", e.status)
}

func (d *webhookDispatcher) sendWithConfig(ctx context.Context, config WebhookConfig, client *http.Client, event WebhookEvent, body []byte) error {
	_, err := d.send(ctx, config, client, event, body)
	return err
}

// send POSTs one delivery of event and returns the receiver's status.
func (d *webhookDispatcher) send(ctx context.Context, config WebhookConfig, client *http.Client, event WebhookEvent, body []byte) (string, error) {
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
	if response.StatusCode >= 400 && response.StatusCode < 500 &&
		response.StatusCode != http.StatusRequestTimeout && response.StatusCode != http.StatusTooManyRequests {
		return response.Status, &permanentWebhookError{status: response.Status}
	}
	return response.Status, fmt.Errorf("unexpected HTTP status %s", response.Status)
}

type WebhookConfigResponse struct {
	Body struct {
		URL              string   `json:"url" doc:"Webhook destination. Empty means webhook delivery is disabled."`
		TimeoutSeconds   int      `json:"timeout_seconds" doc:"Timeout for each delivery attempt, in seconds."`
		MaxAttempts      int      `json:"max_attempts" doc:"Maximum number of delivery attempts."`
		PayloadTemplate  string   `json:"payload_template" doc:"Go text/template for the webhook POST body. Empty means the default JSON payload is used."`
		Events           []string `json:"events" doc:"The event types sent, or patterns of them (task.*, *)."`
		SecretSet        bool     `json:"secret_set" doc:"Deliveries are signed (X-AgentAPI-Signature-256). The secret itself is never returned."`
		HeartbeatSeconds int      `json:"heartbeat_seconds" doc:"How often instance.heartbeat is sent, in seconds; 0 when it is off."`
		InstanceID       string   `json:"instance_id" doc:"The instance ID in every event."`
		PublicURL        string   `json:"public_url" doc:"Where this AgentAPI can be reached, in every event; empty when not set."`
	}
}

type WebhookConfigRequest struct {
	Body struct {
		URL              string    `json:"url" doc:"Webhook destination. Set to an empty string to disable delivery."`
		TimeoutSeconds   int       `json:"timeout_seconds" minimum:"1" maximum:"3600" doc:"Timeout for each delivery attempt, in seconds."`
		MaxAttempts      int       `json:"max_attempts" minimum:"1" maximum:"10" doc:"Maximum number of delivery attempts."`
		PayloadTemplate  *string   `json:"payload_template,omitempty" doc:"Go text/template for custom webhook payload body. Omit to preserve; set to empty string to clear. Available fields: .ID, .Type, .CreatedAt, .Instance, .Data (the event's data), and for run.status_changed .ServerID, .Status, .PreviousStatus, .AgentType, .Transport."`
		Events           *[]string `json:"events,omitempty" doc:"The event types to send, or patterns of them (task.*, *). Omit to preserve; an empty list sends run.status_changed alone."`
		Secret           *string   `json:"secret,omitempty" doc:"Secret that signs every delivery with HMAC-SHA256. Omit to preserve; set to empty string to stop signing."`
		HeartbeatSeconds *int      `json:"heartbeat_seconds,omitempty" minimum:"0" maximum:"86400" doc:"How often instance.heartbeat is sent, in seconds; 0 turns it off. Omit to preserve."`
		InstanceID       *string   `json:"instance_id,omitempty" doc:"The instance ID in every event. Omit to preserve; set to empty string for <hostname>:<port>."`
		PublicURL        *string   `json:"public_url,omitempty" doc:"Where this AgentAPI can be reached, in every event. Omit to preserve; set to empty string to clear."`
	}
}

func (w *webhookAPI) getWebhookConfig(_ context.Context, _ *struct{}) (*WebhookConfigResponse, error) {
	config := w.webhook.configSnapshot()
	response := &WebhookConfigResponse{}
	response.Body.URL = config.URL
	response.Body.TimeoutSeconds = int(config.Timeout / time.Second)
	response.Body.MaxAttempts = config.MaxAttempts
	response.Body.PayloadTemplate = config.PayloadTemplate
	response.Body.Events = config.Events
	response.Body.SecretSet = config.Secret != ""
	response.Body.HeartbeatSeconds = max(0, int(config.Heartbeat/time.Second))
	response.Body.InstanceID = w.webhook.instanceSnapshot().ID
	response.Body.PublicURL = config.PublicURL
	return response, nil
}

func (w *webhookAPI) updateWebhookConfig(_ context.Context, request *WebhookConfigRequest) (*WebhookConfigResponse, error) {
	config := w.webhook.configSnapshot()
	previousURL := config.URL
	config.URL = request.Body.URL
	config.Timeout = time.Duration(request.Body.TimeoutSeconds) * time.Second
	config.MaxAttempts = request.Body.MaxAttempts
	if request.Body.PayloadTemplate != nil {
		config.PayloadTemplate = *request.Body.PayloadTemplate
	}
	if request.Body.Events != nil {
		config.Events = *request.Body.Events
	}
	if request.Body.Secret != nil {
		config.Secret = *request.Body.Secret
	}
	if request.Body.HeartbeatSeconds != nil {
		config.Heartbeat = time.Duration(*request.Body.HeartbeatSeconds) * time.Second
		if config.Heartbeat == 0 {
			config.Heartbeat = -1
		}
	}
	if request.Body.InstanceID != nil {
		config.InstanceID = *request.Body.InstanceID
	}
	if request.Body.PublicURL != nil {
		config.PublicURL = *request.Body.PublicURL
	}
	if err := w.webhook.updateConfig(config); err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	if w.stateDB != nil {
		w.stateDB.SetMeta(statedb.MetaWebhook, w.webhook.stored())
	}
	if w.instanceChanged != nil {
		w.instanceChanged()
	}
	// A new receiver (agentapi hub join) learns of this AgentAPI now
	// rather than at the next heartbeat.
	if config.URL != "" && config.URL != previousURL {
		w.webhook.emit(webhookEventInstanceHeartbeat, w.webhook.snapshotData())
	}
	return w.getWebhookConfig(context.Background(), nil)
}

// WebhookTestResponse is the outcome of POST /webhook/test.
type WebhookTestResponse struct {
	Body struct {
		OK bool `json:"ok" doc:"The receiver answered with a 2xx status."`
		// Status is empty when no response came.
		Status string `json:"status,omitempty" doc:"The receiver's HTTP status, such as 204 No Content."`
		Error  string `json:"error,omitempty" doc:"Why the delivery failed."`
	}
}

// test delivers a webhook.test event once, now, and reports the outcome.
func (d *webhookDispatcher) test(ctx context.Context) (status string, err error) {
	config := d.configSnapshot()
	if config.URL == "" {
		return "", errors.New("no webhook URL is set")
	}
	instance := d.instanceSnapshot()
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
	return d.send(ctx, config, &http.Client{Timeout: config.Timeout}, event, body)
}

func (w *webhookAPI) testWebhook(ctx context.Context, _ *struct{}) (*WebhookTestResponse, error) {
	response := &WebhookTestResponse{}
	status, err := w.webhook.test(ctx)
	response.Body.OK = err == nil
	response.Body.Status = status
	if err != nil {
		response.Body.Error = err.Error()
	}
	return response, nil
}

// webhookAPI reads and changes the webhook, and keeps a change in the
// state database (nil without one).
type webhookAPI struct {
	webhook *webhookDispatcher
	stateDB *statedb.DB
	// instanceChanged tells the summary's subscribers and the registry the
	// instance's ID or URL may have changed.
	instanceChanged func()
}

func (s *Server) registerWebhookRoutes() {
	w := &webhookAPI{webhook: s.webhook, stateDB: s.stateDB, instanceChanged: s.instanceChanged}
	huma.Get(s.api, "/webhook", w.getWebhookConfig, func(o *huma.Operation) {
		o.Tags = []string{"Webhook"}
		o.Summary = "Get webhook configuration"
		o.Description = "Returns the mutable webhook configuration. The secret is never returned, only whether one is set."
	})
	huma.Put(s.api, "/webhook", w.updateWebhookConfig, func(o *huma.Operation) {
		o.Tags = []string{"Webhook"}
		o.Summary = "Update webhook configuration"
		o.Description = "Updates webhook delivery immediately. Startup flags provide the initial values; this endpoint can replace or disable them without restarting the agent."
		o.Errors = []int{400}
	})
	huma.Post(s.api, "/webhook/test", w.testWebhook, func(o *huma.Operation) {
		o.Tags = []string{"Webhook"}
		o.Summary = "Send a test webhook event"
		o.Description = "Sends a webhook.test event to the webhook URL once, signed like every event, and reports the receiver's answer."
	})
}
