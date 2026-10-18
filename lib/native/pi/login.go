package pi

import (
	"context"
	"encoding/json"
	"fmt"
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

// hasModel reports whether the state names a model.
func (s sessionState) hasModel() bool {
	m := s.Model
	return m != nil && m.ID != unknownModel && m.Provider != unknownModel
}

// checkLogin decides whether Pi, whose state names no model, can work.
// Without credentials Pi reports the model "unknown" and lists no
// available models (it only lists models it has a key or login for); only
// then does the start stop. A chosen model without credentials (--model)
// shows when the first prompt is refused.
func (a *Adapter) checkLogin(ctx context.Context) error {
	res, err := a.request(ctx, cmdQuery, simpleCommand{Type: "get_available_models"})
	if err != nil {
		return fmt.Errorf("pi didn't list its models: %w", err)
	}
	if res.err != nil {
		a.log.Debug("Pi didn't list its models; starting anyway", "error", res.err)
		return nil
	}
	var list struct {
		Models *[]json.RawMessage `json:"models"`
	}
	if json.Unmarshal(res.data, &list) != nil || list.Models == nil {
		return nil
	}
	a.log.Debug("Pi has no model", "available_models", len(*list.Models))
	if len(*list.Models) > 0 {
		return nil
	}
	return &native.LoginRequiredError{Login: loginHint}
}

// isAuthError reports whether an error of Pi or its provider says that the
// provider login is missing or no longer works. native.IsAuthError covers
// Pi's "No API key found for …", its "Authentication failed for …" of an
// expired OAuth login and the providers' errors; without any provider Pi
// says it has no model instead.
func isAuthError(text string) bool {
	if native.IsAuthError(text) {
		return true
	}
	l := strings.ToLower(text)
	return strings.Contains(l, "no model selected") || strings.Contains(l, "no models available")
}

// reportLogin tells the sink the provider login is missing, once per turn.
func (a *Adapter) reportLogin() {
	if a.loop.loginReported {
		return
	}
	a.loop.loginReported = true
	a.loop.sink.LoginRequired(loginHint)
}
