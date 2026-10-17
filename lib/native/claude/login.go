package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/native/procutil"
)

// loginHint tells the user how to log Claude Code in.
var loginHint = native.LoginRequired{
	Agent:   "Claude Code",
	Message: "Claude Code is not logged in.",
	Command: "claude auth login",
	Detail:  "Uses your Claude subscription; add --console to bill an Anthropic Console account instead. On a machine without a browser, `claude setup-token` creates a token for CLAUDE_CODE_OAUTH_TOKEN.",
}

// authStatusTimeout bounds `claude auth status`.
const authStatusTimeout = 10 * time.Second

// maxAuthStatusOutput bounds what is read from `auth status`.
const maxAuthStatusOutput = 64 << 10

// runAuthStatus runs `claude auth status --json` and returns its stdout.
func runAuthStatus(ctx context.Context, c procutil.Command) ([]byte, error) {
	return procutil.Output(ctx, c, maxAuthStatusOutput)
}

// parseAuthStatus reads loggedIn from `claude auth status --json`. ok is
// false for output without it (an older CLI, a wrapper program). Nothing
// else is read: the output has the account's email and organization.
func parseAuthStatus(out []byte) (loggedIn, ok bool) {
	var status struct {
		LoggedIn *bool `json:"loggedIn"`
	}
	out = bytes.TrimSpace(out)
	if json.Unmarshal(out, &status) != nil {
		// A wrapper may print something before the JSON.
		start := bytes.LastIndex(out, []byte("\n{"))
		if start < 0 || json.Unmarshal(out[start+1:], &status) != nil {
			return false, false
		}
	}
	if status.LoggedIn == nil {
		return false, false
	}
	return *status.LoggedIn, true
}

// checkLogin asks the CLI whether it is logged in, with the environment
// the session gets. API keys, CLAUDE_CODE_OAUTH_TOKEN, an apiKeyHelper in
// the settings and Bedrock or Vertex all count as logged in. Only a clear
// "not logged in" stops the start; anything else lets it go ahead.
func (a *Adapter) checkLogin(ctx context.Context, env []string) error {
	if a.args.settings {
		// --settings can hold an apiKeyHelper, which `auth status` (which
		// doesn't take --settings) can't see.
		return nil
	}
	checkCtx, cancel := context.WithTimeout(ctx, authStatusTimeout)
	defer cancel()
	out, err := a.cfg.AuthStatus(checkCtx, procutil.Command{
		Program: a.cfg.Program,
		Args:    []string{"auth", "status", "--json"},
		Env:     env,
		Dir:     a.cfg.CWD,
	})
	loggedIn, ok := parseAuthStatus(out)
	if !ok {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		a.cfg.Logger.Debug("Could not read Claude Code's login status; starting it anyway", "error", err)
		return nil
	}
	a.cfg.Logger.Debug("Claude Code login status", "logged_in", loggedIn)
	if loggedIn {
		return nil
	}
	return &native.LoginRequiredError{Login: loginHint}
}

// authFailure reports whether a failed result is a login problem: an
// HTTP 401 from the API, or one of the CLI's login errors.
func authFailure(f *frame) bool {
	if strings.TrimSpace(string(f.APIErrorStatus)) == "401" {
		return true
	}
	if native.IsAuthError(stringField(f.Result)) {
		return true
	}
	return slices.ContainsFunc(f.Errors, native.IsAuthError)
}
