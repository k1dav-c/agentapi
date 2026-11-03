package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/quartz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/convstate"
	"github.com/coder/agentapi/lib/instanceapi"
	mf "github.com/coder/agentapi/lib/msgfmt"
)

func TestWebhookConfigValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		config  Config
		wantErr string
	}{
		{name: "disabled"},
		{name: "valid", config: Config{URL: "https://example.com/hook"}},
		{name: "invalid URL", config: Config{URL: "://bad"}, wantErr: "invalid webhook URL"},
		{name: "unsupported scheme", config: Config{URL: "ftp://example.com/hook"}, wantErr: "must use http or https"},
		{name: "missing host", config: Config{URL: "https:///hook"}, wantErr: "must include a host"},
		{name: "negative timeout", config: Config{URL: "https://example.com", Timeout: -time.Second}, wantErr: "timeout must not be negative"},
		{name: "negative attempts", config: Config{URL: "https://example.com", MaxAttempts: -1}, wantErr: "max attempts must not be negative"},
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

	dispatcher, err := New(
		Config{URL: "https://example.com/hook"},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		mf.AgentTypeCodex,
		instanceapi.TransportPTY,
		quartz.NewReal(),
	)
	require.NoError(t, err)

	emitter := convstate.NewEventEmitter(convstate.WithStatusChangeHandler(dispatcher.StatusChanged))
	emitter.EmitStatus(conv.StatusChanging)
	assert.Empty(t, dispatcher.queue)

	emitter.EmitStatus(conv.StatusStable)
	require.Len(t, dispatcher.queue, 1)
	event := <-dispatcher.queue
	assert.Equal(t, webhookEventRunStatusChanged, event.Type)
	data, ok := event.Data.(WebhookEventData)
	require.True(t, ok)
	assert.Equal(t, convstate.AgentStatusRunning, data.PreviousStatus)
	assert.Equal(t, convstate.AgentStatusStable, data.Status)
	assert.Equal(t, mf.AgentTypeCodex, data.AgentType)
	assert.Equal(t, instanceapi.TransportPTY, data.Transport)
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

	dispatcher, err := New(
		Config{URL: server.URL},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		mf.AgentTypeClaude,
		instanceapi.TransportPTY,
		quartz.NewReal(),
	)
	require.NoError(t, err)
	event := WebhookEvent{
		ID:        "delivery-1",
		Type:      webhookEventRunStatusChanged,
		CreatedAt: time.Unix(1_700_000_000, 0).UTC(),
		Data: WebhookEventData{
			ServerID:       "run-1",
			Status:         convstate.AgentStatusStable,
			PreviousStatus: convstate.AgentStatusRunning,
			AgentType:      mf.AgentTypeClaude,
			Transport:      instanceapi.TransportPTY,
		},
	}
	body, err := json.Marshal(event)
	require.NoError(t, err)
	_, err = dispatcher.send(t.Context(), dispatcher.config, &http.Client{}, event, body)
	require.NoError(t, err)

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

	dispatcher, err := New(
		Config{URL: server.URL},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		mf.AgentTypeClaude,
		instanceapi.TransportPTY,
		quartz.NewReal(),
	)
	require.NoError(t, err)
	event := WebhookEvent{ID: "delivery-1", CreatedAt: time.Now()}
	_, err = dispatcher.send(t.Context(), dispatcher.config, &http.Client{}, event, []byte(`{}`))
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
	dispatcher, err := New(
		Config{URL: server.URL, PayloadTemplate: tmpl},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		mf.AgentTypeClaude,
		instanceapi.TransportPTY,
		quartz.NewReal(),
	)
	require.NoError(t, err)

	event := WebhookEvent{
		ID:        "delivery-tmpl",
		Type:      webhookEventRunStatusChanged,
		CreatedAt: time.Unix(1_700_000_000, 0).UTC(),
		Data: WebhookEventData{
			ServerID:       "run-1",
			Status:         convstate.AgentStatusStable,
			PreviousStatus: convstate.AgentStatusRunning,
			AgentType:      mf.AgentTypeClaude,
			Transport:      instanceapi.TransportPTY,
		},
	}
	body, err := dispatcher.renderBody(event)
	require.NoError(t, err)
	_, err = dispatcher.send(t.Context(), dispatcher.config, &http.Client{}, event, body)
	require.NoError(t, err)

	assert.JSONEq(t, `{"event":"run.status_changed","status":"stable","prev":"running","agent":"claude"}`, string(receivedBody))
}

func TestWebhookInvalidPayloadTemplate(t *testing.T) {
	t.Parallel()

	_, err := New(
		Config{URL: "https://example.com/hook", PayloadTemplate: "{{.Invalid"},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		mf.AgentTypeClaude,
		instanceapi.TransportPTY,
		quartz.NewReal(),
	)
	require.ErrorContains(t, err, "invalid webhook payload template")
}

func TestWebhookPayloadTemplateUnknownField(t *testing.T) {
	t.Parallel()

	newDispatcher := func(tmpl string) error {
		_, err := New(
			Config{URL: "https://example.com/hook", PayloadTemplate: tmpl},
			slog.New(slog.NewTextHandler(io.Discard, nil)),
			mf.AgentTypeClaude,
			instanceapi.TransportPTY,
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
	dispatcher, err := New(
		Config{URL: server.URL, MaxAttempts: 3},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		mf.AgentTypeClaude,
		instanceapi.TransportPTY,
		clock,
	)
	require.NoError(t, err)
	trap := clock.Trap().NewTimer("webhook", "retry")
	defer trap.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		dispatcher.deliver(t.Context(), queuedEvent{WebhookEvent: WebhookEvent{ID: "e1", Type: webhookEventRunStatusChanged, CreatedAt: clock.Now()}})
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

// A receiver that redirects, such as a sign-in page in front of it, didn't
// take the event. Followed, the POST would come back as a GET and the
// sign-in page's 200 would count as delivered: a delivery doesn't follow
// it, fails, is tried again, and the log names where it pointed.
func TestWebhookRedirectIsNoDelivery(t *testing.T) {
	t.Parallel()
	var hooks, signIns atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/hooks", func(w http.ResponseWriter, r *http.Request) {
		hooks.Add(1)
		http.Redirect(w, r, "/login", http.StatusFound)
	})
	mux.HandleFunc("/login", func(w http.ResponseWriter, _ *http.Request) {
		signIns.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	var logs bytes.Buffer
	clock := quartz.NewMock(t)
	dispatcher, err := New(
		Config{URL: server.URL + "/hooks", MaxAttempts: 2},
		slog.New(slog.NewTextHandler(&logs, nil)),
		mf.AgentTypeClaude,
		instanceapi.TransportPTY,
		clock,
	)
	require.NoError(t, err)
	trap := clock.Trap().NewTimer("webhook", "retry")
	defer trap.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		dispatcher.deliver(t.Context(), queuedEvent{WebhookEvent: WebhookEvent{ID: "e1", Type: webhookEventRunStatusChanged, CreatedAt: clock.Now()}})
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	call, err := trap.Wait(ctx)
	require.NoError(t, err, "not tried again: the redirect counted as a delivery")
	call.Release()
	clock.Advance(call.Duration).MustWait(t.Context())
	<-done
	assert.EqualValues(t, 2, hooks.Load())
	assert.Zero(t, signIns.Load(), "not followed")
	assert.Contains(t, logs.String(), "Webhook delivery failed")
	assert.Contains(t, logs.String(), server.URL+"/login")

	// So does the test event.
	status, err := dispatcher.Test(t.Context())
	require.Error(t, err)
	assert.Equal(t, "302 Found", status)
	assert.Contains(t, err.Error(), server.URL+"/login")
	assert.Zero(t, signIns.Load())
}

// A webhook whose URL was cleared at runtime queues nothing.
func TestWebhookDisabledAtRuntime(t *testing.T) {
	t.Parallel()
	dispatcher := testDispatcher(t, Config{URL: "https://initial.example.com/hook"}, quartz.NewReal())
	require.NoError(t, dispatcher.UpdateConfig(Config{}))
	dispatcher.StatusChanged(convstate.AgentStatusRunning, convstate.AgentStatusStable)
	assert.Empty(t, dispatcher.queue)
}
