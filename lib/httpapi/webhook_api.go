package httpapi

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/coder/agentapi/internal/version"
	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/statedb"
	"github.com/coder/agentapi/lib/webhook"
)

// The webhook's configuration, its envelope and the instance it names;
// the dispatcher and the observer are lib/webhook's.
type (
	WebhookConfig   = webhook.Config
	WebhookEvent    = instanceapi.WebhookEvent
	WebhookInstance = instanceapi.WebhookInstance
)

// WebhookEventTypes are the events a webhook can be sent, for
// --webhook-events.
var WebhookEventTypes = webhook.EventTypes

// ExpandPublicURL fills in a public URL's placeholders; see
// instanceapi.ExpandPublicURL.
func ExpandPublicURL(publicURL string, port int) string {
	return instanceapi.ExpandPublicURL(publicURL, port)
}

// startWebhook describes this AgentAPI to the webhook and starts sending
// events: the instance's, and while the webhook sends any of them the
// conversation's, as the observer turns them into webhook events.
func (s *Server) startWebhook(config ServerConfig) {
	hostname, _ := os.Hostname()
	name := config.Name
	if name == "" && config.CWD != "" {
		name = filepath.Base(config.CWD)
	}
	s.webhook.SetInstance(WebhookInstance{
		ID:           net.JoinHostPort(hostname, strconv.Itoa(config.Port)),
		Name:         name,
		Hostname:     hostname,
		Port:         config.Port,
		PID:          os.Getpid(),
		CWD:          config.CWD,
		Version:      version.Version,
		WorkspaceURL: coderWorkspaceURL(),
	})
	s.webhook.SetSnapshot(func() any { return s.webhookInstanceState() })
	var taskCost func(since time.Time) *webhook.WebhookCost
	if s.sessions != nil {
		taskCost = s.webhookTaskCost
	}
	s.webhook.Observe(s.shutdownCtx, s.emitter, s.clock.Now(), taskCost)
	s.webhook.Start(s.shutdownCtx)
}

// webhookInstanceState is the data of the instance.* events.
func (s *Server) webhookInstanceState() instanceapi.WebhookInstanceState {
	status := s.emitter.StatusSnapshot()
	return instanceapi.WebhookInstanceState{
		Status: status.Status, Lifecycle: status.Lifecycle, AgentSessionID: status.AgentSessionID,
		SessionName: status.SessionName, PendingRequests: status.PendingRequests,
		LoginRequired: status.LoginRequired, LimitStop: status.LimitStop, Usage: status.Usage, Settings: status.Settings,
		// The queue has a lock of its own. s.mu would hold up Stop, whose
		// instance.stopped carries the queue's length, until a call into
		// the agent it is about to cancel ended by itself.
		QueuedMessages:   s.queue.Len(),
		HeartbeatSeconds: max(0, int(s.webhook.Config().Heartbeat/time.Second)),
	}
}

// webhookTaskCost is what the open session's model calls since a time
// cost, or nil when none are known.
func (s *Server) webhookTaskCost(since time.Time) *webhook.WebhookCost {
	id := s.native.SessionRef()
	if id == "" {
		return nil
	}
	costs, err := s.sessions.Costs()
	if err != nil {
		s.logger.Warn("Failed to read what the task cost", "error", err)
		return nil
	}
	var cost webhook.WebhookCost
	for _, call := range costs.Sessions[id] {
		if call.Time.Before(since) {
			continue
		}
		cost.USD += call.USD
		cost.Tokens = cost.Tokens.Add(call.Tokens)
		cost.Calls++
	}
	if cost.Calls == 0 {
		return nil
	}
	return &cost
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
		PublicURL        string   `json:"public_url" doc:"Where this AgentAPI can be reached, in every event, as set: {port}, {hostname} and {ip} stay unfilled; empty when not set."`
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
		PublicURL        *string   `json:"public_url,omitempty" doc:"Where this AgentAPI can be reached, in every event. {port} and {hostname} are filled in, and {ip} with the machine's IPv4 address on the route to the webhook URL, looked up again while AgentAPI runs. Omit to preserve; set to empty string to clear."`
	}
}

func (w *webhookAPI) getWebhookConfig(_ context.Context, _ *struct{}) (*WebhookConfigResponse, error) {
	config := w.webhook.Config()
	response := &WebhookConfigResponse{}
	response.Body.URL = config.URL
	response.Body.TimeoutSeconds = int(config.Timeout / time.Second)
	response.Body.MaxAttempts = config.MaxAttempts
	response.Body.PayloadTemplate = config.PayloadTemplate
	response.Body.Events = config.Events
	response.Body.SecretSet = config.Secret != ""
	response.Body.HeartbeatSeconds = max(0, int(config.Heartbeat/time.Second))
	response.Body.InstanceID = w.webhook.Instance().ID
	response.Body.PublicURL = config.PublicURL
	return response, nil
}

func (w *webhookAPI) updateWebhookConfig(_ context.Context, request *WebhookConfigRequest) (*WebhookConfigResponse, error) {
	config := w.webhook.Config()
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
	if err := w.webhook.UpdateConfig(config); err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	if w.stateDB != nil {
		w.stateDB.SetMeta(statedb.MetaWebhook, w.webhook.Stored())
	}
	if w.instanceChanged != nil {
		w.instanceChanged()
	}
	// A new receiver (agentapi hub join) learns of this AgentAPI now
	// rather than at the next heartbeat.
	if config.URL != "" && config.URL != previousURL {
		w.webhook.SendHeartbeat()
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

func (w *webhookAPI) testWebhook(ctx context.Context, _ *struct{}) (*WebhookTestResponse, error) {
	response := &WebhookTestResponse{}
	status, err := w.webhook.Test(ctx)
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
	webhook *webhook.Dispatcher
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
