package native

import (
	"regexp"
	"strconv"
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
// is not one: it is a transient error (IsTransientError).
var limitErrorPattern = regexp.MustCompile(`you've (?:hit|reached) your|you're out of (?:extra )?usage|` +
	`out of usage|usage limit|usage allocation|usage credits|` +
	`exceeded your current quota|insufficient_quota|quota exceeded|resource_exhausted`)

// IsLimitError reports whether an agent's error text says that the account
// reached a usage limit. Adapters check their protocol's structured signals
// (error codes, rate limit events) as well.
func IsLimitError(text string) bool {
	return limitErrorPattern.MatchString(strings.ToLower(text))
}

// transientErrorPattern matches (lower-case) errors of an upstream failure
// that may pass by itself, as agents, model providers and gateways word
// them: an HTTP 5xx or 429 with its status, an overloaded or unavailable
// API, Claude Code's "server-side issue", a rate limit, a timeout and a
// dropped connection.
var transientErrorPattern = regexp.MustCompile(`api error:? (?:429|5\d\d)\b|` +
	`(?:status|code|http|error)[: ]+(?:429|5\d\d)\b|\b(?:429|5\d\d) (?:too many requests|internal server error|bad gateway|service unavailable|gateway time-?out)|` +
	`bad gateway|service (?:is )?(?:temporarily )?unavailable|gateway time-?out|internal server error|server[- ]side issue|` +
	`overloaded|server_busy|at capacity|rate[ _-]?limit|too many requests|` +
	`request timed out|timed out|connection (?:error|reset|refused|closed|lost)|` +
	`econnreset|etimedout|socket hang up|fetch failed|network error|stream (?:disconnected|ended|closed)`)

// IsTransientError reports whether an agent's error text says that an
// upstream request failed in a way that may pass: worth trying again in a
// while. A usage limit and a login problem say so too ("rate limit", a
// 429), which ClassifyFailure puts first.
func IsTransientError(text string) bool {
	return transientErrorPattern.MatchString(strings.ToLower(text))
}

// IsTransientStatus reports whether an upstream HTTP status is one that may
// pass: a 5xx, or 429 (too many requests).
func IsTransientStatus(status int) bool {
	return status == 429 || (status >= 500 && status <= 599)
}

// statusInTextPattern finds the HTTP status of a transient error in its
// text, e.g. "API Error: 502 Bad Gateway" or "unexpected status 503".
var statusInTextPattern = regexp.MustCompile(`(?:api error|status|code|http|error)[: ]+(429|5\d\d)\b|\b(429|5\d\d) (?:too many requests|internal server error|bad gateway|service unavailable|gateway time-?out)`)

// TransientStatus returns the HTTP status a transient error's text names,
// or 0.
func TransientStatus(text string) int {
	m := statusInTextPattern.FindStringSubmatch(strings.ToLower(text))
	if m == nil {
		return 0
	}
	status, _ := strconv.Atoi(m[1] + m[2])
	return status
}

// FailureHints are what an adapter found out about a failed turn, from its
// protocol's structured signals (status and error codes, rate limit
// events) and the error's text (IsLimitError, IsAuthError,
// IsTransientError).
type FailureHints struct {
	// Limit: the account reached a usage limit.
	Limit bool
	// Auth: the agent's login is missing or no longer works.
	Auth bool
	// Transient: the upstream failure may pass by itself.
	Transient bool
	// APIStatus is the HTTP status of a transient failure, or 0.
	APIStatus int
}

// ClassifyFailure gives a failed turn (StopReasonError) the stop reason
// hints call for. A usage limit comes first: it is StopReasonLimited even
// when the error also reads as a login problem or a 429. A login problem
// comes next and stays an error, as trying again can't help until the user
// logs in. Only then is a transient failure StopReasonTransient, with its
// APIStatus. A turn that didn't fail is left as it is.
func ClassifyFailure(result *TurnResult, hints FailureHints) {
	if result.StopReason != StopReasonError {
		return
	}
	switch {
	case hints.Limit:
		result.StopReason = StopReasonLimited
	case hints.Auth:
	case hints.Transient:
		result.StopReason, result.APIStatus = StopReasonTransient, hints.APIStatus
	}
}
