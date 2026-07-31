package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	mf "github.com/coder/agentapi/lib/msgfmt"
	"github.com/danielgtaylor/huma/v2"
)

// UsageResponse is the huma response wrapper for GET /usage.
type UsageResponse struct {
	Body UsageResponseBody
}

// UsageResponseBody contains rate limit usage from the upstream API.
// For Claude agents, the data comes from Anthropic's unified rate limit headers.
// For Codex agents, it comes from OpenAI's rate limit headers.
type UsageResponseBody struct {
	// Common fields.
	Provider  string `json:"provider" doc:"The API provider (anthropic or openai)."`
	AgentType string `json:"agent_type" doc:"The agent type this usage applies to."`

	// Anthropic-specific fields (present when provider is anthropic).
	SubscriptionType    string     `json:"subscription_type,omitempty" doc:"Subscription plan type (e.g. pro, max). Anthropic only."`
	RateLimitTier       string     `json:"rate_limit_tier,omitempty" doc:"Rate limit tier identifier. Anthropic only."`
	Status              string     `json:"status,omitempty" doc:"Overall rate limit status (allowed or limited). Anthropic only."`
	FiveHourUtilization float64    `json:"five_hour_utilization,omitempty" doc:"Utilization for the 5-hour window (0.0 to 1.0). Anthropic only."`
	FiveHourReset       *time.Time `json:"five_hour_reset,omitempty" doc:"When the 5-hour window resets. Anthropic only."`
	FiveHourStatus      string     `json:"five_hour_status,omitempty" doc:"Rate limit status for the 5-hour window. Anthropic only."`
	SevenDayUtilization float64    `json:"seven_day_utilization,omitempty" doc:"Utilization for the 7-day window (0.0 to 1.0). Anthropic only."`
	SevenDayReset       *time.Time `json:"seven_day_reset,omitempty" doc:"When the 7-day window resets. Anthropic only."`
	SevenDayStatus      string     `json:"seven_day_status,omitempty" doc:"Rate limit status for the 7-day window. Anthropic only."`
	OverageUtilization  float64    `json:"overage_utilization,omitempty" doc:"Utilization for the overage window (0.0 to 1.0). Anthropic only."`
	OverageReset        *time.Time `json:"overage_reset,omitempty" doc:"When the overage window resets. Anthropic only."`
	OverageStatus       string     `json:"overage_status,omitempty" doc:"Rate limit status for the overage window. Anthropic only."`
	RepresentativeClaim string     `json:"representative_claim,omitempty" doc:"The window currently governing the rate limit. Anthropic only."`
	FallbackPercentage  float64    `json:"fallback_percentage,omitempty" doc:"Percentage of rate limit available as fallback (0.0 to 1.0). Anthropic only."`

	// OpenAI-specific fields (present when provider is openai).
	LimitRequests     int    `json:"limit_requests,omitempty" doc:"Maximum requests allowed in the current window. OpenAI only."`
	LimitTokens       int    `json:"limit_tokens,omitempty" doc:"Maximum tokens allowed in the current window. OpenAI only."`
	RemainingRequests int    `json:"remaining_requests,omitempty" doc:"Remaining requests in the current window. OpenAI only."`
	RemainingTokens   int    `json:"remaining_tokens,omitempty" doc:"Remaining tokens in the current window. OpenAI only."`
	ResetRequests     string `json:"reset_requests,omitempty" doc:"Duration until the request limit resets (e.g. 6m30s). OpenAI only."`
	ResetTokens       string `json:"reset_tokens,omitempty" doc:"Duration until the token limit resets (e.g. 30s). OpenAI only."`
}

// --- Claude / Anthropic ---

// claudeCredentials holds the OAuth credentials from ~/.claude/.credentials.json.
type claudeCredentials struct {
	ClaudeAIOAuth *struct {
		AccessToken      string `json:"accessToken"`
		SubscriptionType string `json:"subscriptionType"`
		RateLimitTier    string `json:"rateLimitTier"`
	} `json:"claudeAiOauth"`
}

// readClaudeCredentials reads the Claude Code OAuth credentials.
func readClaudeCredentials() (*claudeCredentials, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("detect home directory: %w", err)
	}
	credPath := filepath.Join(home, ".claude", ".credentials.json")
	data, err := os.ReadFile(credPath)
	if err != nil {
		return nil, fmt.Errorf("read credentials file: %w", err)
	}
	var creds claudeCredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("parse credentials: %w", err)
	}
	if creds.ClaudeAIOAuth == nil || creds.ClaudeAIOAuth.AccessToken == "" {
		return nil, fmt.Errorf("no Claude AI OAuth token found in credentials")
	}
	return &creds, nil
}

// anthropicAPIBaseURL is the Anthropic API base URL. Overridable in tests.
var anthropicAPIBaseURL = "https://api.anthropic.com"

// fetchAnthropicRateLimits makes a minimal API request and extracts rate limit headers.
func fetchAnthropicRateLimits(ctx context.Context, token string) (map[string]string, error) {
	url := anthropicAPIBaseURL + "/v1/messages"
	body := `{"model":"claude-haiku-4-5","max_tokens":1,"messages":[{"role":"user","content":"."}]}`

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request Anthropic API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Anthropic API returned HTTP %d", resp.StatusCode)
	}

	headers := make(map[string]string)
	for key, vals := range resp.Header {
		if len(vals) > 0 {
			headers[http.CanonicalHeaderKey(key)] = vals[0]
		}
	}
	return headers, nil
}

func buildAnthropicUsage(creds *claudeCredentials, headers map[string]string) UsageResponseBody {
	h := func(key string) string {
		return headers[http.CanonicalHeaderKey(key)]
	}

	body := UsageResponseBody{
		Provider:  "anthropic",
		AgentType: string(mf.AgentTypeClaude),

		SubscriptionType: creds.ClaudeAIOAuth.SubscriptionType,
		RateLimitTier:    creds.ClaudeAIOAuth.RateLimitTier,
		Status:           h("anthropic-ratelimit-unified-status"),

		FiveHourUtilization: parseFloat(h("anthropic-ratelimit-unified-5h-utilization")),
		FiveHourStatus:      h("anthropic-ratelimit-unified-5h-status"),

		SevenDayUtilization: parseFloat(h("anthropic-ratelimit-unified-7d-utilization")),
		SevenDayStatus:      h("anthropic-ratelimit-unified-7d-status"),

		OverageUtilization: parseFloat(h("anthropic-ratelimit-unified-overage-utilization")),
		OverageStatus:      h("anthropic-ratelimit-unified-overage-status"),

		RepresentativeClaim: h("anthropic-ratelimit-unified-representative-claim"),
		FallbackPercentage:  parseFloat(h("anthropic-ratelimit-unified-fallback-percentage")),
	}

	if t := parseUnixTimestamp(h("anthropic-ratelimit-unified-5h-reset")); !t.IsZero() {
		body.FiveHourReset = &t
	}
	if t := parseUnixTimestamp(h("anthropic-ratelimit-unified-7d-reset")); !t.IsZero() {
		body.SevenDayReset = &t
	}
	if t := parseUnixTimestamp(h("anthropic-ratelimit-unified-overage-reset")); !t.IsZero() {
		body.OverageReset = &t
	}

	return body
}

// --- Codex / OpenAI ---

// openaiAPIBaseURL is the OpenAI API base URL. Overridable in tests.
var openaiAPIBaseURL = "https://api.openai.com"

// codexAuth holds the OAuth credentials from ~/.codex/auth.json.
type codexAuth struct {
	AuthMode string `json:"auth_mode"`
	APIKey   string `json:"OPENAI_API_KEY"`
	Tokens   *struct {
		AccessToken string `json:"access_token"`
	} `json:"tokens"`
}

// readOpenAIAPIKey tries to find an OpenAI API key or Codex OAuth token.
// Priority: OPENAI_API_KEY env var → ~/.codex/auth.json.
func readOpenAIAPIKey() (string, error) {
	// 1. Environment variable (explicit API key).
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		return key, nil
	}

	// 2. Codex CLI auth.json (ChatGPT OAuth or API key stored by codex login).
	home, err := os.UserHomeDir()
	if err == nil {
		authPath := filepath.Join(home, ".codex", "auth.json")
		if data, err := os.ReadFile(authPath); err == nil {
			var auth codexAuth
			if err := json.Unmarshal(data, &auth); err == nil {
				// Prefer explicit API key if stored.
				if auth.APIKey != "" && auth.APIKey != "None" {
					return auth.APIKey, nil
				}
				// Fall back to ChatGPT OAuth access token.
				if auth.Tokens != nil && auth.Tokens.AccessToken != "" {
					return auth.Tokens.AccessToken, nil
				}
			}
		}
	}

	return "", fmt.Errorf("no OpenAI credentials found (set OPENAI_API_KEY or run codex login)")
}

// fetchOpenAIRateLimits makes a minimal API request and extracts rate limit headers.
// It tries the OpenAI Platform API first; if that fails with 401/429 (common when
// using a ChatGPT OAuth token instead of a Platform API key), it tries the ChatGPT
// backend API that Codex CLI uses.
func fetchOpenAIRateLimits(ctx context.Context, apiKey string) (map[string]string, error) {
	// Try OpenAI Platform API first.
	headers, err := fetchOpenAIPlatformRateLimits(ctx, apiKey)
	if err == nil {
		return headers, nil
	}

	// Fall back to ChatGPT backend API (used by Codex CLI with OAuth login).
	headers, chatgptErr := fetchChatGPTRateLimits(ctx, apiKey)
	if chatgptErr == nil {
		return headers, nil
	}

	// Return the original Platform API error since it's more actionable.
	return nil, err
}

func fetchOpenAIPlatformRateLimits(ctx context.Context, apiKey string) (map[string]string, error) {
	url := openaiAPIBaseURL + "/v1/chat/completions"
	body := `{"model":"gpt-4o-mini","max_completion_tokens":1,"messages":[{"role":"user","content":"."}]}`

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request OpenAI API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenAI API returned HTTP %d", resp.StatusCode)
	}

	headers := make(map[string]string)
	for key, vals := range resp.Header {
		if len(vals) > 0 {
			headers[http.CanonicalHeaderKey(key)] = vals[0]
		}
	}
	return headers, nil
}

// chatgptBackendURL is the ChatGPT backend API base URL. Overridable in tests.
var chatgptBackendURL = "https://chatgpt.com/backend-api"

func fetchChatGPTRateLimits(ctx context.Context, token string) (map[string]string, error) {
	url := chatgptBackendURL + "/accounts/check/v4-2023-04-27"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request ChatGPT API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("ChatGPT API returned HTTP %d", resp.StatusCode)
	}

	// Parse the JSON response body to extract plan/usage info.
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parse ChatGPT response: %w", err)
	}

	// Convert relevant fields into a flat header-like map for buildOpenAIUsage.
	headers := make(map[string]string)
	// Extract plan_type from the account info.
	if accounts, ok := result["accounts"].(map[string]any); ok {
		for _, acct := range accounts {
			if a, ok := acct.(map[string]any); ok {
				if planType, ok := a["plan_type"].(string); ok {
					headers["X-Chatgpt-Plan-Type"] = planType
				}
				if rateLimits, ok := a["rate_limits"].([]any); ok {
					for _, rl := range rateLimits {
						if r, ok := rl.(map[string]any); ok {
							if limit, ok := r["limit"].(float64); ok {
								headers["X-Ratelimit-Limit-Requests"] = fmt.Sprintf("%.0f", limit)
							}
							if remaining, ok := r["remaining"].(float64); ok {
								headers["X-Ratelimit-Remaining-Requests"] = fmt.Sprintf("%.0f", remaining)
							}
							if reset, ok := r["reset"].(string); ok {
								headers["X-Ratelimit-Reset-Requests"] = reset
							}
						}
					}
				}
			}
		}
	}

	return headers, nil
}

func buildOpenAIUsage(headers map[string]string) UsageResponseBody {
	h := func(key string) string {
		return headers[http.CanonicalHeaderKey(key)]
	}

	return UsageResponseBody{
		Provider:          "openai",
		AgentType:         string(mf.AgentTypeCodex),
		LimitRequests:     parseInt(h("x-ratelimit-limit-requests")),
		LimitTokens:       parseInt(h("x-ratelimit-limit-tokens")),
		RemainingRequests: parseInt(h("x-ratelimit-remaining-requests")),
		RemainingTokens:   parseInt(h("x-ratelimit-remaining-tokens")),
		ResetRequests:     h("x-ratelimit-reset-requests"),
		ResetTokens:       h("x-ratelimit-reset-tokens"),
	}
}

// --- Shared helpers ---

func parseUnixTimestamp(s string) time.Time {
	ts, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(ts, 0).UTC()
}

func parseFloat(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

func parseInt(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// --- Handler ---

// getUsage handles GET /usage. It calls the provider matching the running
// agent type directly.
func (s *Server) getUsage(ctx context.Context, _ *struct{}) (*UsageResponse, error) {
	switch s.agentType {
	case mf.AgentTypeCodex:
		return s.getOpenAIUsage(ctx)
	default:
		return s.getAnthropicUsage(ctx)
	}
}

func (s *Server) getAnthropicUsage(ctx context.Context) (*UsageResponse, error) {
	creds, err := readClaudeCredentials()
	if err != nil {
		return nil, huma.Error500InternalServerError(
			fmt.Sprintf("failed to read Claude credentials: %v", err),
		)
	}

	headers, err := fetchAnthropicRateLimits(ctx, creds.ClaudeAIOAuth.AccessToken)
	if err != nil {
		return nil, huma.Error502BadGateway(
			fmt.Sprintf("failed to fetch rate limits from Anthropic API: %v", err),
		)
	}

	body := buildAnthropicUsage(creds, headers)
	return &UsageResponse{Body: body}, nil
}

func (s *Server) getOpenAIUsage(ctx context.Context) (*UsageResponse, error) {
	apiKey, err := readOpenAIAPIKey()
	if err != nil {
		return nil, huma.Error500InternalServerError(
			fmt.Sprintf("failed to read OpenAI credentials: %v", err),
		)
	}

	headers, err := fetchOpenAIRateLimits(ctx, apiKey)
	if err != nil {
		return nil, huma.Error502BadGateway(
			fmt.Sprintf("failed to fetch rate limits from OpenAI API: %v", err),
		)
	}

	body := buildOpenAIUsage(headers)
	return &UsageResponse{Body: body}, nil
}
