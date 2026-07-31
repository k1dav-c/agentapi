package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"text/template"
	"time"

	mf "github.com/coder/agentapi/lib/msgfmt"
	"github.com/danielgtaylor/huma/v2"
)

const (
	webhookEventRunStatusChanged = "run.status_changed"
	defaultWebhookTimeout        = 10 * time.Second
	defaultWebhookMaxAttempts    = 3
	webhookQueueSize             = 64
)

type WebhookConfig struct {
	URL             string
	Timeout         time.Duration
	MaxAttempts     int
	PayloadTemplate string
}

func (c WebhookConfig) validate() error {
	if c.URL != "" {
		parsed, err := url.ParseRequestURI(c.URL)
		if err != nil {
			return fmt.Errorf("invalid webhook URL: %w", err)
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return fmt.Errorf("webhook URL must use http or https")
		}
		if parsed.Host == "" {
			return fmt.Errorf("webhook URL must include a host")
		}
	}
	if c.Timeout < 0 {
		return fmt.Errorf("webhook timeout must not be negative")
	}
	if c.MaxAttempts < 0 {
		return fmt.Errorf("webhook max attempts must not be negative")
	}
	return nil
}

type WebhookEvent struct {
	ID        string           `json:"id" doc:"Unique delivery identifier."`
	Type      string           `json:"type" doc:"Event type. Currently run.status_changed."`
	CreatedAt time.Time        `json:"created_at" doc:"Time the event was created."`
	Data      WebhookEventData `json:"data"`
}

type WebhookEventData struct {
	RunID          string       `json:"run_id" doc:"Identifier for this AgentAPI process run."`
	Status         AgentStatus  `json:"status" doc:"Current agent status."`
	PreviousStatus AgentStatus  `json:"previous_status" doc:"Previous agent status."`
	AgentType      mf.AgentType `json:"agent_type" doc:"Type of agent being used."`
	Transport      Transport    `json:"transport" doc:"Backend transport being used."`
}

// WebhookTemplateData is the context available to custom payload templates.
type WebhookTemplateData struct {
	ID             string
	Type           string
	CreatedAt      time.Time
	RunID          string
	Status         string
	PreviousStatus string
	AgentType      string
	Transport      string
}

type webhookDispatcher struct {
	mu        sync.RWMutex
	config    WebhookConfig
	tmpl      *template.Template // nil when using the default JSON payload
	client    *http.Client
	logger    *slog.Logger
	runID     string
	agentType mf.AgentType
	transport Transport
	queue     chan WebhookEvent
}

func parseWebhookTemplate(templateStr string) (*template.Template, error) {
	if templateStr == "" {
		return nil, nil
	}
	tmpl, err := template.New("webhook").Parse(templateStr)
	if err != nil {
		return nil, fmt.Errorf("invalid webhook payload template: %w", err)
	}
	return tmpl, nil
}

func newWebhookDispatcher(config WebhookConfig, logger *slog.Logger, agentType mf.AgentType, transport Transport) (*webhookDispatcher, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	config = webhookConfigWithDefaults(config)
	tmpl, err := parseWebhookTemplate(config.PayloadTemplate)
	if err != nil {
		return nil, err
	}
	return &webhookDispatcher{
		config:    config,
		tmpl:      tmpl,
		client:    &http.Client{Timeout: config.Timeout},
		logger:    logger,
		runID:     randomWebhookID(),
		agentType: agentType,
		transport: transport,
		queue:     make(chan WebhookEvent, webhookQueueSize),
	}, nil
}

func webhookConfigWithDefaults(config WebhookConfig) WebhookConfig {
	if config.Timeout == 0 {
		config.Timeout = defaultWebhookTimeout
	}
	if config.MaxAttempts == 0 {
		config.MaxAttempts = defaultWebhookMaxAttempts
	}
	return config
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
	d.client = &http.Client{Timeout: config.Timeout}
	return nil
}

func randomWebhookID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(value[:])
}

func (d *webhookDispatcher) statusChanged(previous, current AgentStatus) {
	if d.configSnapshot().URL == "" {
		return
	}
	event := WebhookEvent{
		ID:        randomWebhookID(),
		Type:      webhookEventRunStatusChanged,
		CreatedAt: time.Now().UTC(),
		Data: WebhookEventData{
			RunID:          d.runID,
			Status:         current,
			PreviousStatus: previous,
			AgentType:      d.agentType,
			Transport:      d.transport,
		},
	}
	select {
	case d.queue <- event:
	default:
		d.logger.Warn("Webhook queue is full; dropping event", "event_id", event.ID, "event_type", event.Type)
	}
}

func (d *webhookDispatcher) start(ctx context.Context) {
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
}

func (d *webhookDispatcher) renderBody(event WebhookEvent) ([]byte, error) {
	d.mu.RLock()
	tmpl := d.tmpl
	d.mu.RUnlock()

	if tmpl == nil {
		return json.Marshal(event)
	}

	data := WebhookTemplateData{
		ID:             event.ID,
		Type:           event.Type,
		CreatedAt:      event.CreatedAt,
		RunID:          event.Data.RunID,
		Status:         string(event.Data.Status),
		PreviousStatus: string(event.Data.PreviousStatus),
		AgentType:      string(event.Data.AgentType),
		Transport:      string(event.Data.Transport),
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("execute webhook template: %w", err)
	}
	return buf.Bytes(), nil
}

func (d *webhookDispatcher) deliver(ctx context.Context, event WebhookEvent) {
	body, err := d.renderBody(event)
	if err != nil {
		d.logger.Error("Failed to render webhook body", "error", err)
		return
	}
	config := d.configSnapshot()
	client := &http.Client{Timeout: config.Timeout}
	for attempt := 1; attempt <= config.MaxAttempts; attempt++ {
		if err := d.sendWithConfig(ctx, config, client, event, body); err == nil {
			return
		} else if attempt == config.MaxAttempts {
			d.logger.Error("Webhook delivery failed", "event_id", event.ID, "attempts", attempt, "error", err)
		} else {
			d.logger.Warn("Webhook delivery failed; retrying", "event_id", event.ID, "attempt", attempt, "error", err)
		}
		if attempt < config.MaxAttempts {
			timer := time.NewTimer(time.Duration(attempt) * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}
}

func (d *webhookDispatcher) send(ctx context.Context, event WebhookEvent, body []byte) error {
	d.mu.RLock()
	config := d.config
	client := d.client
	d.mu.RUnlock()
	return d.sendWithConfig(ctx, config, client, event, body)
}

func (d *webhookDispatcher) sendWithConfig(ctx context.Context, config WebhookConfig, client *http.Client, event WebhookEvent, body []byte) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, config.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	timestamp := strconv.FormatInt(event.CreatedAt.Unix(), 10)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "AgentAPI-Webhook/1.0")
	request.Header.Set("X-AgentAPI-Delivery", event.ID)
	request.Header.Set("X-AgentAPI-Event", event.Type)
	request.Header.Set("X-AgentAPI-Timestamp", timestamp)

	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("unexpected HTTP status %s", response.Status)
	}
	return nil
}

type WebhookConfigResponse struct {
	Body struct {
		URL             string `json:"url" doc:"Webhook destination. Empty means webhook delivery is disabled."`
		TimeoutSeconds  int    `json:"timeout_seconds" doc:"Timeout for each delivery attempt, in seconds."`
		MaxAttempts     int    `json:"max_attempts" doc:"Maximum number of delivery attempts."`
		PayloadTemplate string `json:"payload_template" doc:"Go text/template for the webhook POST body. Empty means the default JSON payload is used."`
	}
}

type WebhookConfigRequest struct {
	Body struct {
		URL             string  `json:"url" doc:"Webhook destination. Set to an empty string to disable delivery."`
		TimeoutSeconds  int     `json:"timeout_seconds" minimum:"1" maximum:"3600" doc:"Timeout for each delivery attempt, in seconds."`
		MaxAttempts     int     `json:"max_attempts" minimum:"1" maximum:"10" doc:"Maximum number of delivery attempts."`
		PayloadTemplate *string `json:"payload_template,omitempty" doc:"Go text/template for custom webhook payload body. Omit to preserve; set to empty string to clear. Available fields: .ID, .Type, .CreatedAt, .RunID, .Status, .PreviousStatus, .AgentType, .Transport."`
	}
}

func (s *Server) getWebhookConfig(_ context.Context, _ *struct{}) (*WebhookConfigResponse, error) {
	config := s.webhook.configSnapshot()
	response := &WebhookConfigResponse{}
	response.Body.URL = config.URL
	response.Body.TimeoutSeconds = int(config.Timeout / time.Second)
	response.Body.MaxAttempts = config.MaxAttempts
	response.Body.PayloadTemplate = config.PayloadTemplate
	return response, nil
}

func (s *Server) updateWebhookConfig(_ context.Context, request *WebhookConfigRequest) (*WebhookConfigResponse, error) {
	current := s.webhook.configSnapshot()
	payloadTemplate := current.PayloadTemplate
	if request.Body.PayloadTemplate != nil {
		payloadTemplate = *request.Body.PayloadTemplate
	}
	config := WebhookConfig{
		URL:             request.Body.URL,
		Timeout:         time.Duration(request.Body.TimeoutSeconds) * time.Second,
		MaxAttempts:     request.Body.MaxAttempts,
		PayloadTemplate: payloadTemplate,
	}
	if err := s.webhook.updateConfig(config); err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	return s.getWebhookConfig(context.Background(), nil)
}
