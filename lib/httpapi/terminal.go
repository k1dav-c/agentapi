package httpapi

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/sse"

	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/shellterm"
)

// TerminalOutputBody is a chunk of the terminal's output.
type TerminalOutputBody struct {
	Data string `json:"data" doc:"Output as the shell wrote it, escape sequences included, base64-encoded. The first event of a connection holds the recent output, for a terminal emulator to replay."`
}

// TerminalExitBody ends a terminal stream.
type TerminalExitBody struct {
	Reason string `json:"reason" doc:"Why the stream ended: the shell exited (connect again for a new one), or there is no terminal."`
}

type TerminalInputRequest struct {
	Body struct {
		Data string `json:"data" doc:"Keys to type, as a terminal sends them (UTF-8, escape sequences included)."`
	}
}

type TerminalResizeRequest struct {
	Body struct {
		Cols uint16 `json:"cols" minimum:"1" maximum:"1000" doc:"Columns."`
		Rows uint16 `json:"rows" minimum:"1" maximum:"1000" doc:"Rows."`
	}
}

// terminalEnv is the environment of the terminal's shell: the agent's,
// without AgentAPI's own variables, which would configure an agentapi run
// in it.
func terminalEnv() []string {
	var env []string
	for _, kv := range native.AgentEnv(os.Environ()) {
		if !strings.HasPrefix(kv, "AGENTAPI_") {
			env = append(env, kv)
		}
	}
	return env
}

const noTerminal = "There is no terminal: it needs the native transport, and --terminal."

// subscribeTerminal streams the terminal's output, starting its shell if it
// doesn't run.
func (s *Server) subscribeTerminal(ctx context.Context, _ *struct{}, send sse.Sender) {
	if s.terminal == nil {
		_ = send.Data(TerminalExitBody{Reason: noTerminal})
		return
	}
	history, output, stop, err := s.terminal.Watch()
	if err != nil {
		_ = send.Data(TerminalExitBody{Reason: err.Error()})
		return
	}
	defer stop()
	if len(history) > 0 {
		if err := send.Data(TerminalOutputBody{Data: base64.StdEncoding.EncodeToString(history)}); err != nil {
			return
		}
	}
	for {
		select {
		case data, ok := <-output:
			if !ok {
				_ = send.Data(TerminalExitBody{Reason: "The shell exited."})
				return
			}
			if err := send.Data(TerminalOutputBody{Data: base64.StdEncoding.EncodeToString(data)}); err != nil {
				return
			}
		case <-s.shutdownCtx.Done():
			return
		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) terminalInput(_ context.Context, input *TerminalInputRequest) (*InterruptResponse, error) {
	if s.terminal == nil {
		return nil, huma.Error404NotFound(noTerminal)
	}
	if err := s.terminal.Write([]byte(input.Body.Data)); err != nil {
		if errors.Is(err, shellterm.ErrNotRunning) {
			return nil, huma.Error409Conflict("the terminal's shell is not running; GET /terminal starts it")
		}
		return nil, err
	}
	resp := &InterruptResponse{}
	resp.Body.Ok = true
	return resp, nil
}

func (s *Server) terminalResize(_ context.Context, input *TerminalResizeRequest) (*InterruptResponse, error) {
	if s.terminal == nil {
		return nil, huma.Error404NotFound(noTerminal)
	}
	if err := s.terminal.Resize(input.Body.Cols, input.Body.Rows); err != nil {
		return nil, err
	}
	resp := &InterruptResponse{}
	resp.Body.Ok = true
	return resp, nil
}

func (s *Server) registerTerminalRoutes() {
	sse.Register(s.api, huma.Operation{
		OperationID: "subscribeTerminal",
		Method:      http.MethodGet,
		Path:        "/terminal",
		Tags:        []string{"Terminal"},
		Summary:     "Watch the terminal",
		Description: "Streams a shell in the agent's working directory, for a terminal emulator: `output` events carry what it writes (the first one the recent output), and an `exit` event ends the stream when the shell exits. Connecting starts the shell if it doesn't run; it keeps running while nobody watches, and every connection shares it. Native transport only (on by default; --terminal=false turns it off).",
	}, map[string]any{
		"output": TerminalOutputBody{},
		"exit":   TerminalExitBody{},
	}, s.subscribeTerminal)
	huma.Post(s.api, "/terminal/input", s.terminalInput, func(o *huma.Operation) {
		o.Tags = []string{"Terminal"}
		o.Summary = "Type into the terminal"
		o.Errors = []int{404, 409}
	})
	huma.Post(s.api, "/terminal/resize", s.terminalResize, func(o *huma.Operation) {
		o.Tags = []string{"Terminal"}
		o.Summary = "Resize the terminal"
		o.Description = "Sets the terminal's size in cells; the last client to resize decides it."
		o.Errors = []int{404}
	})
}
