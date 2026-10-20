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
