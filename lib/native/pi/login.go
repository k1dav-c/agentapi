package pi

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/coder/agentapi/lib/native"
)

// loginHint tells the user how to give Pi a model provider. Pi logs in
// from its own terminal UI (/login); there is no command for it.
var loginHint = native.LoginRequired{
	Agent:   "Pi",
	Message: "Pi has no logged-in model provider.",
	Command: "pi",
	Detail:  "Start pi in a terminal and run /login, or set a provider key such as ANTHROPIC_API_KEY or OPENAI_API_KEY for AgentAPI.",
}

// unknownModel is what get_state reports as the model when Pi found none.
const unknownModel = "unknown"

// stateHasModel reports whether a get_state response names a model.
func stateHasModel(data json.RawMessage) bool {
	var state sessionState
	if json.Unmarshal(data, &state) != nil {
		// Not ours to judge; the handshake handles it.
		return true
	}
	m := state.Model
	return m != nil && m.ID != unknownModel && m.Provider != unknownModel
}

// checkLogin decides whether Pi can work, from its get_state response.
// Without credentials Pi reports the model "unknown" and lists no
// available models (it only lists models it has a key or login for); only
// then does the start stop. A chosen model without credentials (--model)
// shows when the first prompt is refused.
func (a *Adapter) checkLogin(ctx context.Context, state json.RawMessage) error {
	if stateHasModel(state) {
		return nil
	}
	res, err := a.request(ctx, cmdQuery, simpleCommand{Type: "get_available_models"})
	if err != nil {
		return fmt.Errorf("pi didn't list its models: %w", err)
	}
	if res.err != nil {
		a.logger.Debug("Pi didn't list its models; starting anyway", "error", res.err)
		return nil
	}
	var list struct {
		Models *[]json.RawMessage `json:"models"`
	}
	if json.Unmarshal(res.data, &list) != nil || list.Models == nil {
		return nil
	}
	a.logger.Debug("Pi has no model", "available_models", len(*list.Models))
	if len(*list.Models) > 0 {
		return nil
	}
	return &native.LoginRequiredError{Login: loginHint}
}

// authErrorPattern matches (lower-case) errors of a missing or rejected
// provider login: Pi's own ("No API key found for …", "No model
// selected", which point to /login) and providers' (HTTP 401, invalid
// keys, expired tokens).
var authErrorPattern = regexp.MustCompile(`no api key|no model selected|no models available|use /login|not authenticated|unauthorized|authentication_error|invalid api key|invalid x-api-key|incorrect api key|oauth token|refresh token|\b401\b`)

func isAuthError(text string) bool {
	return authErrorPattern.MatchString(strings.ToLower(text))
}

// reportLogin tells the sink the provider login is missing, once per turn.
func (a *Adapter) reportLogin() {
	if a.loop.loginReported {
		return
	}
	a.loop.loginReported = true
	a.loop.sink.LoginRequired(loginHint)
}
