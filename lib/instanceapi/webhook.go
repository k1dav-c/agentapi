package instanceapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/agentapi/lib/native"
)

// The webhook events an AgentAPI sends about itself, which a hub needs.
const (
	EventInstanceStarted   = "instance.started"
	EventInstanceHeartbeat = "instance.heartbeat"
	EventInstanceStopped   = "instance.stopped"
	// EventWebhookTest is sent on request (POST /webhook/test, agentapi
	// hub join), whatever the events chosen.
	EventWebhookTest = "webhook.test"
)

// The headers of a webhook delivery.
const (
	HeaderDelivery  = "X-AgentAPI-Delivery"
	HeaderEvent     = "X-AgentAPI-Event"
	HeaderInstance  = "X-AgentAPI-Instance"
	HeaderTimestamp = "X-AgentAPI-Timestamp"
	HeaderSignature = "X-AgentAPI-Signature-256"
)

// MaxSkew is how old (or how far ahead) a signed event may be: the sender
// retries for a while, and clocks differ.
const MaxSkew = 10 * time.Minute

// WebhookEvent is the body of a delivery, unless a payload template
// reshapes it.
type WebhookEvent struct {
	ID        string    `json:"id" doc:"Unique delivery identifier."`
	Type      string    `json:"type" doc:"Event type, such as run.status_changed or task.completed."`
	CreatedAt time.Time `json:"created_at" doc:"Time the event was created."`
	// Instance is the AgentAPI that sent the event.
	Instance *WebhookInstance `json:"instance,omitempty"`
	// Data depends on the type: WebhookInstanceState for instance.*.
	Data any `json:"data"`
}

// WebhookInstanceState is the data of instance.started, instance.heartbeat
// and instance.stopped: what the agent is doing.
type WebhookInstanceState struct {
	Status          AgentStatus           `json:"status"`
	Lifecycle       LifecycleState        `json:"lifecycle"`
	AgentSessionID  string                `json:"agent_session_id,omitempty"`
	SessionName     string                `json:"session_name,omitempty"`
	PendingRequests int                   `json:"pending_requests"`
	QueuedMessages  int                   `json:"queued_messages"`
	TerminalPrompt  string                `json:"terminal_prompt,omitempty"`
	LoginRequired   *native.LoginRequired `json:"login_required,omitempty"`
	LimitStop       *native.LimitStop     `json:"limit_stop,omitempty"`
	Usage           *native.AgentUsage    `json:"usage,omitempty"`
	Settings        *native.Settings      `json:"settings,omitempty"`
}

// Sign is the X-AgentAPI-Signature-256 of body sent at timestamp (Unix
// seconds): "sha256=" and the hex HMAC-SHA256, keyed with secret, of
// "<timestamp>.<body>".
func Sign(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// Verify checks that body, received with header, was signed with secret
// at most MaxSkew before or after now, and says why not.
func Verify(secret string, header http.Header, body []byte, now time.Time) error {
	timestamp := header.Get(HeaderTimestamp)
	unix, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return errors.New("no timestamp")
	}
	if age := now.Sub(time.Unix(unix, 0)); age > MaxSkew || age < -MaxSkew {
		return fmt.Errorf("signed more than %s from now", MaxSkew)
	}
	signature := header.Get(HeaderSignature)
	if signature == "" {
		return errors.New("no signature")
	}
	if !hmac.Equal([]byte(Sign(secret, timestamp, body)), []byte(signature)) {
		return errors.New("signed with another secret")
	}
	return nil
}
