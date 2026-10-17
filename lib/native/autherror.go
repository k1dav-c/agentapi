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
