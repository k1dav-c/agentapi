package native

import (
	"regexp"
	"strings"
)

// authErrorPattern matches (lower-case) errors of a missing, expired or
// rejected login as agents and model providers word them: an HTTP 401, a
// missing or invalid API key, an OAuth token that expired or can't be
// refreshed, and the agents' own "log in again" hints.
var authErrorPattern = regexp.MustCompile(`\b401\b|unauthorized|not authenticated|not logged in|` +
	`authentication[ _](?:error|failed)|failed to authenticate|re-?authenticate|` +
	`invalid (?:x-)?api key|incorrect api key|no api key|` +
	`oauth token|refresh token|access token could not be refreshed|login expired|` +
	`log ?in again|sign in again|/login\b|codex login`)

// IsAuthError reports whether an agent's error text says that its login is
// missing or no longer works. Adapters check their protocol's structured
// signals (status codes, error codes) as well.
func IsAuthError(text string) bool {
	return authErrorPattern.MatchString(strings.ToLower(text))
}

// limitErrorPattern matches (lower-case) errors of an account that reached
// a usage limit or ran out of credits, as agents and model providers word
// them: Claude Code's "You've hit your limit" family, Codex's "usage limit",
// and providers' quota errors. A transient rate limit (too many requests)
// is not one: the agent retries those.
var limitErrorPattern = regexp.MustCompile(`you've (?:hit|reached) your|you're out of (?:extra )?usage|` +
	`out of usage|usage limit|usage allocation|usage credits|` +
	`exceeded your current quota|insufficient_quota|quota exceeded|resource_exhausted`)

// IsLimitError reports whether an agent's error text says that the account
// reached a usage limit. Adapters check their protocol's structured signals
// (error codes, rate limit events) as well.
func IsLimitError(text string) bool {
	return limitErrorPattern.MatchString(strings.ToLower(text))
}
