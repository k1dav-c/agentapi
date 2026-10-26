package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/quartz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	conv "github.com/coder/agentapi/lib/conversation"
	mf "github.com/coder/agentapi/lib/msgfmt"
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
		quartz.NewReal(),
	)
	require.NoError(t, err)

	emitter := NewEventEmitter(WithStatusChangeHandler(dispatcher.statusChanged))
	emitter.EmitStatus(conv.StatusChanging)
	assert.Empty(t, dispatcher.queue)

	emitter.EmitStatus(conv.StatusStable)
	require.Len(t, dispatcher.queue, 1)
	event := <-dispatcher.queue
	assert.Equal(t, webhookEventRunStatusChanged, event.Type)
	data, ok := event.Data.(WebhookEventData)
	require.True(t, ok)
	assert.Equal(t, AgentStatusRunning, data.PreviousStatus)
	assert.Equal(t, AgentStatusStable, data.Status)
	assert.Equal(t, mf.AgentTypeCodex, data.AgentType)
	assert.Equal(t, TransportPTY, data.Transport)
	assert.NotEmpty(t, event.ID)
	assert.NotEmpty(t, data.ServerID)
	assert.Equal(t, data.ServerID, event.Instance.ServerID)

	emitter.EmitStatus(conv.StatusStable)
	assert.Empty(t, dispatcher.queue)
}

func TestWebhookSend(t *testing.T) {
	t.Parallel()

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
		WebhookConfig{URL: server.URL},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		mf.AgentTypeClaude,
		TransportACP,
		quartz.NewReal(),
	)
	require.NoError(t, err)
	event := WebhookEvent{
		ID:        "delivery-1",
		Type:      webhookEventRunStatusChanged,
		CreatedAt: time.Unix(1_700_000_000, 0).UTC(),
		Data: WebhookEventData{
			ServerID:       "run-1",
			Status:         AgentStatusStable,
			PreviousStatus: AgentStatusRunning,
			AgentType:      mf.AgentTypeClaude,
			Transport:      TransportACP,
		},
	}
	body, err := json.Marshal(event)
	require.NoError(t, err)
	require.NoError(t, dispatcher.sendWithConfig(t.Context(), dispatcher.config, &http.Client{}, event, body))

	assert.JSONEq(t, string(body), string(receivedBody))
	assert.Equal(t, "application/json", receivedHeader.Get("Content-Type"))
	assert.Equal(t, event.ID, receivedHeader.Get("X-AgentAPI-Delivery"))
	assert.Equal(t, event.Type, receivedHeader.Get("X-AgentAPI-Event"))
	assert.Equal(t, "1700000000", receivedHeader.Get("X-AgentAPI-Timestamp"))
	assert.Empty(t, receivedHeader.Get("X-AgentAPI-Signature-256"))
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
		quartz.NewReal(),
	)
	require.NoError(t, err)
	event := WebhookEvent{ID: "delivery-1", CreatedAt: time.Now()}
	err = dispatcher.sendWithConfig(t.Context(), dispatcher.config, &http.Client{}, event, []byte(`{}`))
	require.ErrorContains(t, err, "503 Service Unavailable")
}

func TestWebhookCustomPayloadTemplate(t *testing.T) {
	t.Parallel()

	var receivedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		receivedBody, err = io.ReadAll(r.Body)
		require.NoError(t, err)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	tmpl := `{"event":"{{.Type}}","status":"{{.Status}}","prev":"{{.PreviousStatus}}","agent":"{{.AgentType}}"}`
	dispatcher, err := newWebhookDispatcher(
		WebhookConfig{URL: server.URL, PayloadTemplate: tmpl},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		mf.AgentTypeClaude,
		TransportPTY,
		quartz.NewReal(),
	)
	require.NoError(t, err)

	event := WebhookEvent{
		ID:        "delivery-tmpl",
		Type:      webhookEventRunStatusChanged,
		CreatedAt: time.Unix(1_700_000_000, 0).UTC(),
		Data: WebhookEventData{
			ServerID:       "run-1",
			Status:         AgentStatusStable,
			PreviousStatus: AgentStatusRunning,
			AgentType:      mf.AgentTypeClaude,
			Transport:      TransportPTY,
		},
	}
	body, err := dispatcher.renderBody(event)
	require.NoError(t, err)
	require.NoError(t, dispatcher.sendWithConfig(t.Context(), dispatcher.config, &http.Client{}, event, body))

	assert.JSONEq(t, `{"event":"run.status_changed","status":"stable","prev":"running","agent":"claude"}`, string(receivedBody))
}

func TestWebhookInvalidPayloadTemplate(t *testing.T) {
	t.Parallel()

	_, err := newWebhookDispatcher(
		WebhookConfig{URL: "https://example.com/hook", PayloadTemplate: "{{.Invalid"},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		mf.AgentTypeClaude,
		TransportPTY,
		quartz.NewReal(),
	)
	require.ErrorContains(t, err, "invalid webhook payload template")
}

func TestWebhookPayloadTemplateUnknownField(t *testing.T) {
	t.Parallel()

	newDispatcher := func(tmpl string) error {
		_, err := newWebhookDispatcher(
			WebhookConfig{URL: "https://example.com/hook", PayloadTemplate: tmpl},
			slog.New(slog.NewTextHandler(io.Discard, nil)),
			mf.AgentTypeClaude,
			TransportPTY,
			quartz.NewReal(),
		)
		return err
	}
	// Renamed to .ServerID in 1.0.
	require.ErrorContains(t, newDispatcher(`{"run":"{{.RunID}}"}`), "can't evaluate field RunID")
	require.ErrorContains(t, newDispatcher(`{{.Instance.Nope}}`), "can't evaluate field Nope")
	// Data is a map: what an event lacks is empty, and functions that fail
	// on empty data don't reject the template.
	require.NoError(t, newDispatcher(`{"server":"{{.ServerID}}","host":"{{.Instance.Hostname}}","prompt":"{{.Data.prompt}}"}`))
	require.NoError(t, newDispatcher(`{{index .Data.items 0}}`))
}

func TestWebhookConfigCanBeUpdatedAtRuntime(t *testing.T) {
	t.Parallel()

	dispatcher, err := newWebhookDispatcher(
		WebhookConfig{
			URL:         "https://initial.example.com/hook",
			Timeout:     5 * time.Second,
			MaxAttempts: 2,
		},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		mf.AgentTypeClaude,
		TransportPTY,
		quartz.NewReal(),
	)
	require.NoError(t, err)
	server := &webhookAPI{webhook: dispatcher}

	// Update URL, timeout, and max attempts.
	request := &WebhookConfigRequest{}
	request.Body.URL = "https://updated.example.com/hook"
	request.Body.TimeoutSeconds = 12
	request.Body.MaxAttempts = 4
	response, err := server.updateWebhookConfig(t.Context(), request)
	require.NoError(t, err)
	assert.Equal(t, request.Body.URL, response.Body.URL)
	assert.Equal(t, 12, response.Body.TimeoutSeconds)
	assert.Equal(t, 4, response.Body.MaxAttempts)
	assert.Empty(t, response.Body.PayloadTemplate)

	// Set a payload template.
	tmpl := `{"s":"{{.Status}}"}`
	request.Body.PayloadTemplate = &tmpl
	response, err = server.updateWebhookConfig(t.Context(), request)
	require.NoError(t, err)
	assert.Equal(t, tmpl, response.Body.PayloadTemplate)

	// Omit PayloadTemplate (nil) — should preserve existing.
	request.Body.PayloadTemplate = nil
	response, err = server.updateWebhookConfig(t.Context(), request)
	require.NoError(t, err)
	assert.Equal(t, tmpl, response.Body.PayloadTemplate)

	// Clear PayloadTemplate with empty string.
	clearTmpl := ""
	request.Body.PayloadTemplate = &clearTmpl
	response, err = server.updateWebhookConfig(t.Context(), request)
	require.NoError(t, err)
	assert.Empty(t, response.Body.PayloadTemplate)

	// Disable webhook by clearing URL.
	request.Body.URL = ""
	response, err = server.updateWebhookConfig(t.Context(), request)
	require.NoError(t, err)
	assert.Empty(t, response.Body.URL)

	dispatcher.statusChanged(AgentStatusRunning, AgentStatusStable)
	assert.Empty(t, dispatcher.queue)
}

// A delivery that fails is tried again after a second, then after two, on
// the server's clock.
func TestWebhookRetries(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	clock := quartz.NewMock(t)
	dispatcher, err := newWebhookDispatcher(
		WebhookConfig{URL: server.URL, MaxAttempts: 3},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		mf.AgentTypeClaude,
		TransportPTY,
		clock,
	)
	require.NoError(t, err)
	trap := clock.Trap().NewTimer("webhook", "retry")
	defer trap.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		dispatcher.deliver(t.Context(), WebhookEvent{ID: "e1", Type: webhookEventRunStatusChanged, CreatedAt: clock.Now()})
	}()
	for _, wait := range []time.Duration{time.Second, 2 * time.Second} {
		call := trap.MustWait(t.Context())
		assert.Equal(t, wait, call.Duration)
		call.Release()
		clock.Advance(wait).MustWait(t.Context())
	}
	<-done
	assert.EqualValues(t, 3, calls.Load())
}

func TestExpandPublicURL(t *testing.T) {
	t.Parallel()
	hostname, _ := os.Hostname()
	assert.Equal(t, "http://dev-1.vpn:3286", ExpandPublicURL("http://dev-1.vpn:{port}", 3286))
	assert.Equal(t, "http://"+hostname+":3284/", ExpandPublicURL("http://{hostname}:{port}/", 3284))
	assert.Equal(t, "https://agent.example.com", ExpandPublicURL("https://agent.example.com", 3284))
}
