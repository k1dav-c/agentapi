package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/coder/agentapi/lib/native"
)

// loginHint tells the user how to log Codex in.
var loginHint = native.LoginRequired{
	Agent:   "Codex",
	Message: "Codex is not logged in.",
	Command: "codex login",
	Detail:  "Signs in with your ChatGPT plan; `printenv OPENAI_API_KEY | codex login --with-api-key` uses an API key instead.",
}

// getAccountResponse is the result of account/read.
type getAccountResponse struct {
	Account            json.RawMessage `json:"account"`
	RequiresOpenaiAuth bool            `json:"requiresOpenaiAuth"`
}

// checkLogin asks app-server for its account. No account where the model
// provider needs an OpenAI login means Codex is not logged in; app-server
// doesn't read OPENAI_API_KEY, so a key in the environment changes
// nothing. A provider that needs no login (requiresOpenaiAuth false), or
// an app-server without account/read, lets the start go ahead.
func (a *Adapter) checkLogin(ctx context.Context) error {
	result, err := a.call(ctx, "account/read", struct{}{}, nil)
	if err != nil {
		if ctx.Err() != nil || a.loopEnded() {
			return fmt.Errorf("read the codex account: %w", err)
		}
		a.log.Debug("codex app-server didn't report its account; starting anyway", "error", err)
		return nil
	}
	var resp getAccountResponse
	if err := json.Unmarshal(result, &resp); err != nil {
		a.log.Debug("Unexpected account/read result; starting anyway", "error", err)
		return nil
	}
	loggedIn := len(resp.Account) > 0 && string(resp.Account) != "null"
	a.log.Debug("Codex account", "logged_in", loggedIn, "requires_openai_auth", resp.RequiresOpenaiAuth)
	if !loggedIn && resp.RequiresOpenaiAuth {
		return &native.LoginRequiredError{Login: loginHint}
	}
	return nil
}

// authErrorPattern matches (lower-case) errors of a missing or expired
// login: HTTP 401, a token that can't be refreshed, "sign in again".
var authErrorPattern = regexp.MustCompile(`\b401\b|unauthorized|refresh token|access token could not be refreshed|not logged in|log ?in again|sign in again|codex login`)

// isAuthError reports whether a turn error is a login problem: the
// unauthorized error code, an HTTP 401 on any of the error kinds, or a
// login message.
func isAuthError(e *turnError) bool {
	if e == nil {
		return false
	}
	if len(e.CodexErrorInfo) > 0 {
		var code string
		if json.Unmarshal(e.CodexErrorInfo, &code) == nil {
			if code == "unauthorized" {
				return true
			}
		} else {
			var kinds map[string]struct {
				HTTPStatusCode *int `json:"httpStatusCode"`
			}
			if json.Unmarshal(e.CodexErrorInfo, &kinds) == nil {
				for _, kind := range kinds {
					if kind.HTTPStatusCode != nil && *kind.HTTPStatusCode == 401 {
						return true
					}
				}
			}
		}
	}
	return authErrorPattern.MatchString(strings.ToLower(turnErrorText(e)))
}

// reportLogin tells the sink the login is gone, once per turn.
func (s *loopState) reportLogin(t *turnState) {
	if t != nil {
		if t.loginReported {
			return
		}
		t.loginReported = true
	}
	s.sink().LoginRequired(loginHint)
}
