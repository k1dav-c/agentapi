package pi

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/coder/quartz"

	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/native"
)

// Question IDs of the requests made from extension dialogs.
const (
	// choiceQuestion answers a select dialog.
	choiceQuestion = "choice"
	// valueQuestion answers an input or editor dialog.
	valueQuestion = "value"
)

// Option IDs of confirm dialogs and of cancelling a question.
const (
	optionYes    = "yes"
	optionNo     = "no"
	optionCancel = "cancel"
)

// dialog is an open extension dialog (select, confirm, input, editor).
type dialog struct {
	id     string
	method string
	// approval: the request was reported as an approval (a select or
	// confirm while a tool runs, or any confirm).
	approval bool
	options  []string
	timer    *quartz.Timer
	// answering: Respond is writing the answer. The dialog stays open
	// until the write succeeded, so a failed answer can be sent again.
	answering bool
	// unconfirmed: the answer's write didn't finish, so Pi may yet get it.
	// The dialog takes no other answer, and isn't cancelled.
	unconfirmed bool
}

func (d *dialog) stopTimer() {
	if d.timer != nil {
		d.timer.Stop()
	}
}

// reply translates a response into the extension_ui_response.
func (d *dialog) reply(r native.Response) (uiResponse, error) {
	out := uiResponse{Type: "extension_ui_response", ID: d.id}
	if r.OptionID == optionCancel && !d.approval {
		out.Cancelled = true
		return out, nil
	}
	switch d.method {
	case "confirm":
		switch r.OptionID {
		case optionYes:
			out.Confirmed = ptr(true)
		case optionNo:
			out.Confirmed = ptr(false)
		default:
			return out, fmt.Errorf("%w: answer yes or no", native.ErrInvalidResponse)
		}
	case "select":
		value := r.OptionID
		if !d.approval {
			answers := r.Answers[choiceQuestion]
			if len(answers) != 1 {
				return out, fmt.Errorf("%w: pick one option", native.ErrInvalidResponse)
			}
			value = answers[0]
		}
		if !slices.Contains(d.options, value) {
			return out, fmt.Errorf("%w: %q is not an option", native.ErrInvalidResponse, value)
		}
		out.Value = &value
	case "input", "editor":
		answers := r.Answers[valueQuestion]
		if len(answers) != 1 {
			return out, fmt.Errorf("%w: give one answer", native.ErrInvalidResponse)
		}
		out.Value = &answers[0]
	default:
		return out, fmt.Errorf("%w: unknown dialog %q", native.ErrInvalidResponse, d.method)
	}
	return out, nil
}

func ptr[T any](v T) *T { return &v }

// The beginnings of the words that classify the select options of an
// approval.
var (
	denyWords   = []string{"block", "deny", "reject", "decline", "skip", "disallow"}
	cancelWords = []string{"cancel", "abort", "stop"}
	allowWords  = []string{"allow", "yes", "approve", "accept", "run", "continue", "ok"}
)

// optionKind classifies a select option of an approval by the words of its
// label; "no" counts only as a word of its own ("Normal mode" isn't a
// denial). A denial or a cancel wins ("Always deny"); otherwise "always"
// makes the option an allow_always, with or without an allow word.
func optionKind(label string) native.OptionKind {
	words := strings.FieldsFunc(strings.ToLower(label), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	has := func(beginnings []string) bool {
		return slices.ContainsFunc(words, func(w string) bool {
			return slices.ContainsFunc(beginnings, func(b string) bool { return strings.HasPrefix(w, b) })
		})
	}
	switch {
	case slices.Contains(words, "no") || has(denyWords):
		return native.OptionKindDeny
	case has(cancelWords):
		return native.OptionKindCancel
	case slices.Contains(words, "always"):
		return native.OptionKindAllowAlways
	case has(allowWords):
		return native.OptionKindAllow
	}
	return native.OptionKindOther
}

func (a *Adapter) handleUIRequest(req uiRequest) {
	switch req.Method {
	case "select", "confirm", "input", "editor":
		a.openDialog(req)
	case "notify":
		if req.NotifyType == "error" {
			a.loop.sink.Error(req.Message, conv.ErrorLevelError)
			return
		}
		a.log.Info("Pi extension notification", "type", req.NotifyType, "message", req.Message)
	case "setTitle":
		a.log.Debug("Pi extension set the title", "title", req.Title)
		// The title stands in for a session name until Pi has one.
		if a.loop.sessionName == "" && strings.TrimSpace(req.Title) != "" {
			a.loop.sink.SessionName(req.Title)
		}
	case "setStatus", "setWidget", "set_editor_text":
		a.log.Debug("Pi extension UI update", "method", req.Method, "key", req.StatusKey+req.WidgetKey, "status", req.StatusText)
	default:
		a.log.Debug("Unknown Pi extension UI request", "method", req.Method)
	}
}

// openDialog reports a dialog as a request. A select or confirm while a
// tool runs is the extension's approval of that tool call (a tool_call
// hook); other selects and text inputs are questions.
func (a *Adapter) openDialog(req uiRequest) {
	if a.isTurnActive() || a.hasPendingTurn() {
		a.beginTurn()
		a.loop.turnDialogs = append(a.loop.turnDialogs, req.ID)
	}

	d := &dialog{id: req.ID, method: req.Method, options: req.Options}
	r := native.Request{ID: req.ID, Title: req.Title}
	var tool *runningTool
	if n := len(a.loop.tools); n > 0 {
		tool = &a.loop.tools[n-1]
	}
	switch req.Method {
	case "confirm":
		d.approval = true
		r.Kind = native.RequestKindApproval
		r.Detail = req.Message
		r.Options = []native.RequestOption{
			{ID: optionYes, Label: "Yes", Kind: native.OptionKindAllow},
			{ID: optionNo, Label: "No", Kind: native.OptionKindDeny},
		}
	case "select":
		if tool != nil {
			d.approval = true
			r.Kind = native.RequestKindApproval
			for _, o := range req.Options {
				r.Options = append(r.Options, native.RequestOption{ID: o, Label: o, Kind: optionKind(o)})
			}
			break
		}
		r.Kind = native.RequestKindQuestion
		q := native.Question{ID: choiceQuestion, Question: req.Title}
		for _, o := range req.Options {
			q.Options = append(q.Options, native.QuestionOption{Label: o})
		}
		r.Questions = []native.Question{q}
	case "input", "editor":
		r.Kind = native.RequestKindQuestion
		r.Detail = req.Placeholder
		if req.Method == "editor" {
			r.Detail = req.Prefill
		}
		r.Questions = []native.Question{{ID: valueQuestion, Question: req.Title, FreeText: true}}
	}
	if r.Kind == native.RequestKindQuestion {
		r.Options = []native.RequestOption{{ID: optionCancel, Label: "Cancel", Kind: native.OptionKindCancel}}
	}
	if d.approval && tool != nil {
		r.ToolName, r.ToolUseID, r.Input = tool.name, tool.id, tool.args
	}
	if req.Timeout > 0 {
		// Pi answers with the default itself once the timeout passes.
		id := req.ID
		d.timer = a.clock.AfterFunc(time.Duration(req.Timeout)*time.Millisecond, func() {
			a.post(func() { a.expireDialog(id) })
		})
	}
	a.mu.Lock()
	a.dialogs[req.ID] = d
	a.mu.Unlock()
	a.loop.sink.RequestOpened(r)
	// The prompt that ran an extension command is under way. Its
	// StartTurn returns once the dialog can be answered.
	a.acceptPrompt()
}

// hasPendingTurn reports whether a prompt that starts a turn awaits its
// response.
func (a *Adapter) hasPendingTurn() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, p := range a.pending {
		if p.kind == cmdTurn {
			return true
		}
	}
	return false
}

// expireDialog closes a dialog Pi stopped waiting for.
func (a *Adapter) expireDialog(id string) {
	a.mu.Lock()
	_, open := a.dialogs[id]
	delete(a.dialogs, id)
	a.mu.Unlock()
	if open {
		a.loop.sink.RequestClosed(id)
	}
}
