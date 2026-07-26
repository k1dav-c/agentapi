package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	mf "github.com/coder/agentapi/lib/msgfmt"
	st "github.com/coder/agentapi/lib/screentracker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebhookConfigValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		config  WebhookConfig
		wantErr string
	}{
		{name: "disabled"},
		{name: "valid", config: WebhookConfig{URL: "https://example.com/hook"}},
		{name: "invalid URL", config: WebhookConfig{URL: "://bad"}, wantErr: "invalid webhook URL"},
		{name: "unsupported scheme", config: WebhookConfig{URL: "ftp://example.com/hook"}, wantErr: "must use http or https"},
		{name: "missing host", config: WebhookConfig{URL: "https:///hook"}, wantErr: "must include a host"},
		{name: "negative timeout", config: WebhookConfig{URL: "https://example.com", Timeout: -time.Second}, wantErr: "timeout must not be negative"},
		{name: "negative attempts", config: WebhookConfig{URL: "https://example.com", MaxAttempts: -1}, wantErr: "max attempts must not be negative"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.config.validate()
			if tc.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.wantErr)
			}
		})
	}
}

func TestWebhookStatusChangesOnly(t *testing.T) {
	t.Parallel()

	dispatcher, err := newWebhookDispatcher(
		WebhookConfig{URL: "https://example.com/hook"},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		mf.AgentTypeCodex,
		TransportPTY,
	)
	require.NoError(t, err)

	emitter := NewEventEmitter(WithStatusChangeHandler(dispatcher.statusChanged))
	emitter.EmitStatus(st.ConversationStatusChanging)
	assert.Empty(t, dispatcher.queue)

	emitter.EmitStatus(st.ConversationStatusStable)
	require.Len(t, dispatcher.queue, 1)
	event := <-dispatcher.queue
	assert.Equal(t, webhookEventRunStatusChanged, event.Type)
	assert.Equal(t, AgentStatusRunning, event.Data.PreviousStatus)
	assert.Equal(t, AgentStatusStable, event.Data.Status)
	assert.Equal(t, mf.AgentTypeCodex, event.Data.AgentType)
	assert.Equal(t, TransportPTY, event.Data.Transport)
	assert.NotEmpty(t, event.ID)
	assert.NotEmpty(t, event.Data.RunID)

	emitter.EmitStatus(st.ConversationStatusStable)
	assert.Empty(t, dispatcher.queue)
}

func TestWebhookSend(t *testing.T) {
	t.Parallel()

	const secret = "test-secret"
	var receivedBody []byte
	var receivedHeader http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		receivedBody, err = io.ReadAll(r.Body)
		require.NoError(t, err)
		receivedHeader = r.Header.Clone()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	dispatcher, err := newWebhookDispatcher(
		WebhookConfig{URL: server.URL, Secret: secret},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		mf.AgentTypeClaude,
		TransportACP,
	)
	require.NoError(t, err)
	event := WebhookEvent{
		ID:        "delivery-1",
		Type:      webhookEventRunStatusChanged,
		CreatedAt: time.Unix(1_700_000_000, 0).UTC(),
		Data: WebhookEventData{
			RunID:          "run-1",
			Status:         AgentStatusStable,
			PreviousStatus: AgentStatusRunning,
			AgentType:      mf.AgentTypeClaude,
			Transport:      TransportACP,
		},
	}
	body, err := json.Marshal(event)
	require.NoError(t, err)
	require.NoError(t, dispatcher.send(t.Context(), event, body))

	assert.JSONEq(t, string(body), string(receivedBody))
	assert.Equal(t, "application/json", receivedHeader.Get("Content-Type"))
	assert.Equal(t, event.ID, receivedHeader.Get("X-AgentAPI-Delivery"))
	assert.Equal(t, event.Type, receivedHeader.Get("X-AgentAPI-Event"))
	assert.Equal(t, "1700000000", receivedHeader.Get("X-AgentAPI-Timestamp"))

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte("1700000000."))
	_, _ = mac.Write(body)
	assert.Equal(t, "sha256="+hex.EncodeToString(mac.Sum(nil)), receivedHeader.Get("X-AgentAPI-Signature-256"))
}

func TestWebhookSendRejectsNonSuccessfulResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "try later", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	dispatcher, err := newWebhookDispatcher(
		WebhookConfig{URL: server.URL},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		mf.AgentTypeClaude,
		TransportPTY,
	)
	require.NoError(t, err)
	event := WebhookEvent{ID: "delivery-1", CreatedAt: time.Now()}
	err = dispatcher.send(t.Context(), event, []byte(`{}`))
	require.ErrorContains(t, err, "503 Service Unavailable")
}

func TestWebhookConfigCanBeUpdatedAtRuntime(t *testing.T) {
	t.Parallel()

	dispatcher, err := newWebhookDispatcher(
		WebhookConfig{
			URL:         "https://initial.example.com/hook",
			Secret:      "initial-secret",
			Timeout:     5 * time.Second,
			MaxAttempts: 2,
		},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		mf.AgentTypeClaude,
		TransportPTY,
	)
	require.NoError(t, err)
	server := &Server{webhook: dispatcher}

	request := &WebhookConfigRequest{}
	request.Body.URL = "https://updated.example.com/hook"
	request.Body.TimeoutSeconds = 12
	request.Body.MaxAttempts = 4
	response, err := server.updateWebhookConfig(t.Context(), request)
	require.NoError(t, err)
	assert.Equal(t, request.Body.URL, response.Body.URL)
	assert.Equal(t, 12, response.Body.TimeoutSeconds)
	assert.Equal(t, 4, response.Body.MaxAttempts)
	assert.True(t, response.Body.SecretConfigured)
	assert.Equal(t, "initial-secret", dispatcher.configSnapshot().Secret)

	clearSecret := ""
	request.Body.URL = ""
	request.Body.Secret = &clearSecret
	response, err = server.updateWebhookConfig(t.Context(), request)
	require.NoError(t, err)
	assert.Empty(t, response.Body.URL)
	assert.False(t, response.Body.SecretConfigured)

	dispatcher.statusChanged(AgentStatusRunning, AgentStatusStable)
	assert.Empty(t, dispatcher.queue)
}
