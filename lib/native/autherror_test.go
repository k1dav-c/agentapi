package native_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/coder/agentapi/lib/native"
)

func TestIsAuthError(t *testing.T) {
	t.Parallel()
	for _, text := range []string{
		// Claude Code
		"Not logged in · Please run /login",
		"Invalid API key · Fix external API key",
		"OAuth token has expired. Please obtain a new token or refresh your existing token.",
		`API Error: 401 {"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`,
		"Failed to authenticate. API Error: 401",
		// Codex
		"unexpected status 401 Unauthorized: Missing bearer or basic authentication in header",
		"Your access token could not be refreshed because your refresh token was already used. Please log out and sign in again.",
		"Not logged in. Run `codex login`.",
		// Pi and providers
		`No API key found for anthropic. Use /login or set an API key environment variable.`,
		`Authentication failed for "openai-codex". Credentials may have expired or network is unavailable. Run '/login openai-codex' to re-authenticate.`,
		"Incorrect API key provided: sk-xxxx.",
	} {
		assert.True(t, native.IsAuthError(text), text)
	}
	for _, text := range []string{
		"",
		"Overloaded",
		"API Error: 529 overloaded_error",
		"rate limit exceeded (429)",
		"The operation was aborted",
		"stream disconnected before completion: error sending request",
		"context window exceeded: 4010 tokens",
	} {
		assert.False(t, native.IsAuthError(text), text)
	}
}

func TestIsLimitError(t *testing.T) {
	t.Parallel()
	for _, text := range []string{
		// Claude Code
		"You've hit your limit · resets 3pm (UTC)",
		"You've hit your session limit · resets 3:40pm",
		"You've reached your Fable limit.",
		"You're out of extra usage · resets Oct 20",
		"Claude AI usage limit reached|1760000000",
		// Codex
		"You've hit your usage limit. Upgrade to Pro, or try again at 3:05 PM.",
		// Providers
		"429 You exceeded your current quota, please check your plan and billing details.",
		`{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"Quota exceeded for metric"}}`,
	} {
		assert.True(t, native.IsLimitError(text), text)
	}
	for _, text := range []string{
		"",
		"rate limit exceeded (429)",
		`API Error: 429 {"type":"error","error":{"type":"rate_limit_error","message":"Number of requests has exceeded your rate limit"}}`,
		"API Error: 529 overloaded_error",
		"Not logged in · Please run /login",
	} {
		assert.False(t, native.IsLimitError(text), text)
	}
}

// claude502 is what Claude Code ended a turn with when an inference
// gateway in front of the API failed.
const claude502 = "API Error: 502 Bad Gateway. This is a server-side issue, usually temporary — try again in a moment. If it persists, check your inference gateway (litellm-proxy.example.com)."

func TestIsTransientError(t *testing.T) {
	t.Parallel()
	for _, text := range []string{
		// Claude Code
		claude502,
		`API Error: 529 {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`,
		`API Error: 500 {"type":"error","error":{"type":"api_error","message":"Internal server error"}}`,
		`API Error: 429 {"type":"error","error":{"type":"rate_limit_error","message":"Number of requests has exceeded your rate limit"}}`,
		"API Error: Request timed out.",
		"API Error: Connection error.",
		// Codex
		"unexpected status 503 Service Unavailable: upstream connect error",
		"stream disconnected before completion: error sending request for url",
		"exceeded retry limit, last status: 502 Bad Gateway",
		// Pi and providers
		"503 Service Unavailable",
		"Overloaded",
		"fetch failed",
		"rate limit exceeded (429)",
	} {
		assert.True(t, native.IsTransientError(text), text)
	}
	for _, text := range []string{
		"",
		"Not logged in · Please run /login",
		"prompt is too long: 250000 tokens > 200000 maximum",
		"API Error: 400 invalid_request_error",
		"context window exceeded: 5020 tokens",
		"The operation was aborted",
		"Pi stopped with an error",
	} {
		assert.False(t, native.IsTransientError(text), text)
	}
}

func TestTransientStatus(t *testing.T) {
	t.Parallel()
	for text, status := range map[string]int{
		claude502: 502,
		"unexpected status 503 Service Unavailable": 503,
		"529 overloaded":                      0,
		"504 Gateway Timeout":                 504,
		"API Error: 429 rate_limit_error":     429,
		"Overloaded":                          0,
		"context window exceeded: 502 tokens": 0,
	} {
		assert.Equal(t, status, native.TransientStatus(text), text)
	}
	assert.True(t, native.IsTransientStatus(502))
	assert.True(t, native.IsTransientStatus(429))
	assert.False(t, native.IsTransientStatus(401))
	assert.False(t, native.IsTransientStatus(400))
}
