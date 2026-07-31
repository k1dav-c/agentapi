package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseUnixTimestamp(t *testing.T) {
	t.Parallel()

	t.Run("valid", func(t *testing.T) {
		t.Parallel()
		ts := parseUnixTimestamp("1785945600")
		assert.Equal(t, time.Date(2026, 8, 5, 16, 0, 0, 0, time.UTC), ts)
	})

	t.Run("invalid", func(t *testing.T) {
		t.Parallel()
		ts := parseUnixTimestamp("not-a-number")
		assert.True(t, ts.IsZero())
	})
}

func TestParseFloat(t *testing.T) {
	t.Parallel()
	assert.InDelta(t, 0.08, parseFloat("0.08"), 0.001)
	assert.InDelta(t, 0.0, parseFloat("invalid"), 0.001)
	assert.InDelta(t, 0.5, parseFloat("0.5"), 0.001)
}

func TestParseInt(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 10000, parseInt("10000"))
	assert.Equal(t, 0, parseInt("invalid"))
}

func TestFetchAnthropicRateLimits(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/v1/messages", r.URL.Path)
			assert.Contains(t, r.Header.Get("Authorization"), "Bearer ")
			assert.Equal(t, "2023-06-01", r.Header.Get("Anthropic-Version"))

			w.Header().Set("anthropic-ratelimit-unified-status", "allowed")
			w.Header().Set("anthropic-ratelimit-unified-5h-status", "allowed")
			w.Header().Set("anthropic-ratelimit-unified-5h-reset", "1785499200")
			w.Header().Set("anthropic-ratelimit-unified-5h-utilization", "0.08")
			w.Header().Set("anthropic-ratelimit-unified-7d-status", "allowed")
			w.Header().Set("anthropic-ratelimit-unified-7d-reset", "1785945600")
			w.Header().Set("anthropic-ratelimit-unified-7d-utilization", "0.05")
			w.Header().Set("anthropic-ratelimit-unified-overage-status", "allowed")
			w.Header().Set("anthropic-ratelimit-unified-overage-reset", "1785542400")
			w.Header().Set("anthropic-ratelimit-unified-overage-utilization", "0.0")
			w.Header().Set("anthropic-ratelimit-unified-representative-claim", "five_hour")
			w.Header().Set("anthropic-ratelimit-unified-fallback-percentage", "0.5")

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"msg_test","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"claude-haiku-4-5","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
		}))
		defer srv.Close()

		origURL := anthropicAPIBaseURL
		anthropicAPIBaseURL = srv.URL
		defer func() { anthropicAPIBaseURL = origURL }()

		headers, err := fetchAnthropicRateLimits(t.Context(), "test-token")
		require.NoError(t, err)

		assert.Equal(t, "allowed", headers[http.CanonicalHeaderKey("anthropic-ratelimit-unified-status")])
		assert.Equal(t, "0.08", headers[http.CanonicalHeaderKey("anthropic-ratelimit-unified-5h-utilization")])
		assert.Equal(t, "0.05", headers[http.CanonicalHeaderKey("anthropic-ratelimit-unified-7d-utilization")])
		assert.Equal(t, "five_hour", headers[http.CanonicalHeaderKey("anthropic-ratelimit-unified-representative-claim")])
	})

	t.Run("api error", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer srv.Close()

		origURL := anthropicAPIBaseURL
		anthropicAPIBaseURL = srv.URL
		defer func() { anthropicAPIBaseURL = origURL }()

		_, err := fetchAnthropicRateLimits(t.Context(), "bad-token")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "401")
	})
}

func TestFetchOpenAIRateLimits(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/v1/chat/completions", r.URL.Path)
			assert.Contains(t, r.Header.Get("Authorization"), "Bearer ")

			w.Header().Set("x-ratelimit-limit-requests", "10000")
			w.Header().Set("x-ratelimit-limit-tokens", "200000")
			w.Header().Set("x-ratelimit-remaining-requests", "9999")
			w.Header().Set("x-ratelimit-remaining-tokens", "199990")
			w.Header().Set("x-ratelimit-reset-requests", "6m30s")
			w.Header().Set("x-ratelimit-reset-tokens", "30s")

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"chatcmpl-test","object":"chat.completion","model":"gpt-4o-mini","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
		}))
		defer srv.Close()

		origURL := openaiAPIBaseURL
		openaiAPIBaseURL = srv.URL
		defer func() { openaiAPIBaseURL = origURL }()

		headers, err := fetchOpenAIRateLimits(t.Context(), "test-key")
		require.NoError(t, err)

		assert.Equal(t, "10000", headers[http.CanonicalHeaderKey("x-ratelimit-limit-requests")])
		assert.Equal(t, "199990", headers[http.CanonicalHeaderKey("x-ratelimit-remaining-tokens")])
		assert.Equal(t, "6m30s", headers[http.CanonicalHeaderKey("x-ratelimit-reset-requests")])
	})

	t.Run("api error", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer srv.Close()

		origURL := openaiAPIBaseURL
		openaiAPIBaseURL = srv.URL
		defer func() { openaiAPIBaseURL = origURL }()

		_, err := fetchOpenAIRateLimits(t.Context(), "bad-key")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "401")
	})
}

func TestBuildAnthropicUsage(t *testing.T) {
	t.Parallel()

	creds := &claudeCredentials{
		ClaudeAIOAuth: &struct {
			AccessToken      string `json:"accessToken"`
			SubscriptionType string `json:"subscriptionType"`
			RateLimitTier    string `json:"rateLimitTier"`
		}{
			SubscriptionType: "max",
			RateLimitTier:    "default_claude_max_20x",
		},
	}
	headers := map[string]string{
		http.CanonicalHeaderKey("anthropic-ratelimit-unified-status"):               "allowed",
		http.CanonicalHeaderKey("anthropic-ratelimit-unified-5h-utilization"):       "0.08",
		http.CanonicalHeaderKey("anthropic-ratelimit-unified-5h-reset"):             "1785499200",
		http.CanonicalHeaderKey("anthropic-ratelimit-unified-5h-status"):            "allowed",
		http.CanonicalHeaderKey("anthropic-ratelimit-unified-7d-utilization"):       "0.05",
		http.CanonicalHeaderKey("anthropic-ratelimit-unified-7d-reset"):             "1785945600",
		http.CanonicalHeaderKey("anthropic-ratelimit-unified-7d-status"):            "allowed",
		http.CanonicalHeaderKey("anthropic-ratelimit-unified-representative-claim"): "five_hour",
	}

	body := buildAnthropicUsage(creds, headers)
	assert.Equal(t, "anthropic", body.Provider)
	assert.Equal(t, "claude", body.AgentType)
	assert.Equal(t, "max", body.SubscriptionType)
	assert.InDelta(t, 0.08, body.FiveHourUtilization, 0.001)
	assert.InDelta(t, 0.05, body.SevenDayUtilization, 0.001)
	assert.Equal(t, "five_hour", body.RepresentativeClaim)
	require.NotNil(t, body.FiveHourReset)
	require.NotNil(t, body.SevenDayReset)
}

func TestBuildOpenAIUsage(t *testing.T) {
	t.Parallel()

	headers := map[string]string{
		http.CanonicalHeaderKey("x-ratelimit-limit-requests"):     "10000",
		http.CanonicalHeaderKey("x-ratelimit-limit-tokens"):       "200000",
		http.CanonicalHeaderKey("x-ratelimit-remaining-requests"): "9999",
		http.CanonicalHeaderKey("x-ratelimit-remaining-tokens"):   "199990",
		http.CanonicalHeaderKey("x-ratelimit-reset-requests"):     "6m30s",
		http.CanonicalHeaderKey("x-ratelimit-reset-tokens"):       "30s",
	}

	body := buildOpenAIUsage(headers)
	assert.Equal(t, "openai", body.Provider)
	assert.Equal(t, "codex", body.AgentType)
	assert.Equal(t, 10000, body.LimitRequests)
	assert.Equal(t, 200000, body.LimitTokens)
	assert.Equal(t, 9999, body.RemainingRequests)
	assert.Equal(t, 199990, body.RemainingTokens)
	assert.Equal(t, "6m30s", body.ResetRequests)
	assert.Equal(t, "30s", body.ResetTokens)
}

func TestReadClaudeCredentials_MissingFile(t *testing.T) {
	t.Parallel()
	_, err := readClaudeCredentials()
	if err != nil {
		assert.Contains(t, err.Error(), "credentials")
	}
}
