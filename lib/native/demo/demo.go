// Package demo is a native.Adapter that simulates an agent without running
// one, for developing and testing clients such as the chat UI. Run it with
//
//	agentapi server --transport native --type custom -- native-demo
//
// Every turn streams a thinking block, runs a tool and streams a reply.
// Words in the message open requests the way a real agent would:
//
//   - "approve": a tool call that needs approval (allow, always allow, or
//     deny with feedback)
//   - "ask": a question request with a single-select, a multi-select and a
//     free-text question ("secret" adds a masked one)
//   - "plan": a plan to approve or reject with feedback
//   - "long": a turn that keeps working for about half a minute, to try
//     steering, interrupting and the queue
//   - "login": the agent loses its login (Sink.LoginRequired). The next
//     start (Check again, POST /login/retry) still finds no login, so the
//     conversation waits and tries again by itself; the start after that
//     works.
//
// Its settings can change (native.Configurable): in the ask permission
// mode every turn's first command needs approval, and in the plan mode
// every turn ends with a plan, whose approval switches to bypass. Every
// turn reports a growing, simulated context and rate limit usage.
package demo

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/coder/agentapi/lib/jsonlwatcher"
	"github.com/coder/agentapi/lib/native"
)

// Program is the agent command line that selects the demo adapter.
const Program = "native-demo"

// defaultStep is the pause between two streamed snapshots.
const defaultStep = 120 * time.Millisecond

// Options configures an Adapter.
type Options struct {
	// Step is the pause between two streamed snapshots; zero means 120 ms.
	Step time.Duration
	// Now returns the current time; nil means time.Now.
	Now func() time.Time
}

// LoginHint is what the demo reports when it simulates a missing login.
var LoginHint = native.LoginRequired{
	Agent:   "Native demo",
	Message: "The demo agent is not logged in.",
	Command: "native-demo login",
	Detail:  "Nothing to run: this login is simulated. **Check again** restarts the demo, whose first start still finds no login; AgentAPI then tries again by itself and the start after that works.",
}

// loginState is shared by the adapters of one factory: how many starts
// still find no login.
type loginState struct {
	mu         sync.Mutex
	startsLeft int
}

// missing reports whether a start finds no login, and counts the start.
func (l *loginState) missing() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.startsLeft == 0 {
		return false
	}
	l.startsLeft--
	return true
}

// lose makes the next start find no login.
func (l *loginState) lose() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.startsLeft = 1
}

// Adapter is the simulated agent.
type Adapter struct {
	step      time.Duration
	now       func() time.Time
	resumeRef string
	login     *loginState
	// prefix starts the IDs of the adapter's messages, tool calls and
	// requests, so they don't repeat those of an earlier adapter in the
	// same conversation.
	prefix string

	mu       sync.Mutex
	sink     native.Sink
	started  bool
	closed   bool
	turns    int
	active   *turn
	nextReq  int
	settings native.Settings
	done     chan struct{}
	once     sync.Once
}

var (
	_ native.Adapter      = (*Adapter)(nil)
	_ native.Configurable = (*Adapter)(nil)
)

// settingOptions are the demo's simulated models and modes.
var settingOptions = native.SettingOptions{
	PermissionModes: native.PermissionModeValues,
	Models: []native.ModelOption{
		{ID: "demo-swift", Name: "Swift", Description: "Quick answers", Efforts: []string{"low", "medium", "high"}, DefaultEffort: "medium"},
		{ID: "demo-deep", Name: "Deep", Description: "Thinks longer about hard problems", Efforts: []string{"low", "medium", "high", "max"}, DefaultEffort: "high"},
		{ID: "demo-plain", Name: "Plain", Description: "No reasoning"},
	},
}

// defaultSettings are the settings a demo agent starts with.
var defaultSettings = native.Settings{PermissionMode: native.PermissionModeBypass, Model: "demo-swift", Effort: "medium"}

// Factory returns a native.Factory creating demo adapters. Its adapters
// share the simulated login.
func Factory(opts Options) native.Factory {
	if opts.Step <= 0 {
		opts.Step = defaultStep
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	login := &loginState{}
	return func(resumeRef string) (native.Adapter, error) {
		return newAdapter(resumeRef, opts, login), nil
	}
}

// newAdapter returns an adapter for a new session, or for resumeRef.
func newAdapter(resumeRef string, opts Options, login *loginState) *Adapter {
	var nonce [4]byte
	_, _ = rand.Read(nonce[:])
	return &Adapter{
		step:      opts.Step,
		now:       opts.Now,
		resumeRef: resumeRef,
		login:     login,
		prefix:    "demo-" + hex.EncodeToString(nonce[:]),
		settings:  defaultSettings,
		done:      make(chan struct{}),
	}
}

// turn is the state of the turn being worked on.
type turn struct {
	ctx    context.Context
	cancel context.CancelFunc
	// steers holds messages added with Steer that the turn hasn't used yet;
	// steered is signaled when one arrives.
	steers  []string
	steered chan struct{}
	// waiting maps the open requests to the channel their answer goes to.
	waiting map[string]chan native.Response
}

func (a *Adapter) Start(_ context.Context, sink native.Sink) error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return native.ErrNotStarted
	}
	if a.login.missing() {
		a.mu.Unlock()
		return &native.LoginRequiredError{Login: LoginHint}
	}
	a.sink = sink
	a.started = true
	a.mu.Unlock()
	ref := a.resumeRef
	if ref == "" {
		ref = fmt.Sprintf("demo-%d", a.now().UnixNano())
	}
	sink.SessionRef(ref)
	sink.SessionName("Native demo")
	return nil
}

func (a *Adapter) StartTurn(_ context.Context, input native.Input) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.started || a.closed {
		return native.ErrNotStarted
	}
	if a.active != nil {
		return native.ErrTurnActive
	}
	ctx, cancel := context.WithCancel(context.Background())
	t := &turn{ctx: ctx, cancel: cancel, steered: make(chan struct{}, 1), waiting: map[string]chan native.Response{}}
	a.active = t
	a.turns++
	go a.run(t, a.turns, input)
	return nil
}

func (a *Adapter) Steer(_ context.Context, input native.Input) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.active == nil {
		return native.ErrNoActiveTurn
	}
	a.active.steers = append(a.active.steers, strings.TrimSpace(input.Text))
	select {
	case a.active.steered <- struct{}{}:
	default:
	}
	return nil
}

func (a *Adapter) Interrupt(context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.active == nil {
		return native.ErrNoActiveTurn
	}
	a.active.cancel()
	return nil
}

func (a *Adapter) Respond(_ context.Context, requestID string, response native.Response) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.active == nil {
		return native.ErrRequestNotFound
	}
	ch, ok := a.active.waiting[requestID]
	if !ok {
		return native.ErrRequestNotFound
	}
	delete(a.active.waiting, requestID)
	ch <- response
	return nil
}

func (a *Adapter) Capabilities() native.Capabilities {
	return native.Capabilities{Steer: true, Interrupt: true, Resume: true, Images: true, Settings: true}
}

func (a *Adapter) Settings() native.Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settings
}

func (a *Adapter) SettingOptions(context.Context) (native.SettingOptions, error) {
	return settingOptions, nil
}

// ApplySettings changes the settings. A new model without the current
// effort gets its default one.
func (a *Adapter) ApplySettings(_ context.Context, change native.Settings) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := settingOptions.Check(change, a.settings); err != nil {
		return err
	}
	settings := a.settings.Merge(change)
	if model, ok := settingOptions.Model(settings.Model); ok && change.Effort == "" && !slices.Contains(model.Efforts, settings.Effort) {
		settings.Effort = model.DefaultEffort
	}
	a.settings = settings
	return nil
}

func (a *Adapter) Wait() error {
	<-a.done
	return nil
}

func (a *Adapter) Close() error {
	a.mu.Lock()
	a.closed = true
	if a.active != nil {
		a.active.cancel()
	}
	a.mu.Unlock()
	a.once.Do(func() { close(a.done) })
	return nil
}

// takeSteers returns the messages steered into t since the last call.
func (a *Adapter) takeSteers(t *turn) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	steers := t.steers
	t.steers = nil
	return steers
}

// open registers a request of t and returns the channel its answer
// arrives on.
func (a *Adapter) open(t *turn) (string, chan native.Response) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.nextReq++
	id := fmt.Sprintf("%s-req-%d", a.prefix, a.nextReq)
	ch := make(chan native.Response, 1)
	t.waiting[id] = ch
	return id, ch
}

// finish ends turn t. The turn is cleared before the sink hears about it,
// so the next turn can start as soon as the conversation is idle.
func (a *Adapter) finish(t *turn, result native.TurnResult) {
	a.mu.Lock()
	if a.active == t {
		a.active = nil
	}
	sink := a.sink
	turns := a.turns
	a.mu.Unlock()
	t.cancel()
	if usage, ok := sink.(native.UsageSink); ok {
		usage.UsageChanged(demoUsage(turns, a.now()))
	}
	sink.TurnCompleted(result)
}

// demoUsage is the simulated usage after the nth turn: the context grows
// with every turn, as do the rate limits.
func demoUsage(n int, now time.Time) native.AgentUsage {
	fiveHour, weekly := min(float64(n)*7, 100), min(20+float64(n)*2, 100)
	fiveHourReset, weeklyReset := now.Add(3*time.Hour).Truncate(time.Minute), now.Add(4*24*time.Hour).Truncate(time.Hour)
	status := "allowed"
	if fiveHour >= 80 {
		status = "allowed_warning"
	}
	return native.AgentUsage{
		Context: &native.ContextUsage{Used: min(18000+n*9500, 200000), Size: 200000},
		RateLimits: []native.RateLimit{
			{ID: "five_hour", Label: "5-hour limit", UsedPercent: &fiveHour, ResetsAt: &fiveHourReset, Status: status},
			{ID: "seven_day", Label: "Weekly limit", UsedPercent: &weekly, ResetsAt: &weeklyReset},
		},
	}
}

// script plays one turn. Sink calls only happen on this goroutine, one
// turn at a time.
type script struct {
	a    *Adapter
	t    *turn
	n    int
	sink native.Sink
	text string
	// outcomes lists what happened, for the reply.
	outcomes []string
	tools    int
}

// errLoginLost ends a turn whose login is gone.
var errLoginLost = errors.New("login lost")

// errInterrupted ends a turn that was interrupted.
var errInterrupted = errors.New("interrupted")

func (a *Adapter) run(t *turn, n int, input native.Input) {
	a.mu.Lock()
	sink := a.sink
	a.mu.Unlock()
	s := &script{a: a, t: t, n: n, sink: sink, text: input.Text}
	sink.TurnStarted()
	if len(input.Images) > 0 {
		s.outcomes = append(s.outcomes, fmt.Sprintf("Looked at %d attached image(s)", len(input.Images)))
	}
	if err := s.play(); err != nil {
		if errors.Is(err, errLoginLost) {
			a.finish(t, native.TurnResult{StopReason: native.StopReasonError, Error: "Native demo: the API answered 401 Unauthorized (simulated)"})
			return
		}
		a.finish(t, native.TurnResult{StopReason: native.StopReasonInterrupted})
		return
	}
	a.finish(t, native.TurnResult{StopReason: native.StopReasonEndTurn})
}

func (s *script) play() error {
	words := keywords(s.text)
	mode := s.a.Settings().PermissionMode
	work := &message{id: s.id("work"), role: "assistant", at: s.a.now()}
	thinking := fmt.Sprintf("**Reading the request**\n\nThe user wrote %q. I'll look around the workspace first, then answer.", excerpt(s.text, 80))
	if err := s.streamThinking(work, thinking); err != nil {
		return err
	}
	var ask decide
	if mode == native.PermissionModeAsk {
		ask = s.askToRun("ls", "Lists the workspace.")
	}
	if err := s.tool(work, "Bash", map[string]any{"command": "ls", "description": "List the workspace"}, ask); err != nil {
		return err
	}
	if words["login"] {
		return s.loseLogin()
	}
	if words["approve"] {
		if err := s.tool(work, "Bash", map[string]any{"command": "rm -rf ./build && make release", "description": "Rebuild the release"}, s.approval); err != nil {
			return err
		}
	}
	if words["ask"] {
		questions := demoQuestions[:len(demoQuestions)-1]
		if words["secret"] {
			questions = demoQuestions
		}
		if err := s.tool(work, "AskUserQuestion", askInput(questions), s.question(questions)); err != nil {
			return err
		}
	}
	if words["plan"] || mode == native.PermissionModePlan {
		if err := s.tool(work, "ExitPlanMode", map[string]any{"plan": demoPlan}, s.plan); err != nil {
			return err
		}
	}
	return s.reply(words["long"])
}

// loseLogin reports a lost login the way a real agent does: the login
// first, then the failed turn. The next start of the factory's adapters
// still finds no login.
func (s *script) loseLogin() error {
	s.a.login.lose()
	s.sink.LoginRequired(LoginHint)
	return errLoginLost
}

// id returns the ID of a rich message or tool call of the turn.
func (s *script) id(name string) string {
	return fmt.Sprintf("%s-%d-%s", s.a.prefix, s.n, name)
}

// message is a rich message being built; send reports its current state.
type message struct {
	id     string
	role   string
	at     time.Time
	blocks []jsonlwatcher.RichContentBlock
	stop   string
	usage  *jsonlwatcher.Usage
}

func (s *script) send(m *message) {
	s.sink.RichMessage(jsonlwatcher.RichMessage{
		MessageID:  m.id,
		Role:       m.role,
		Content:    append([]jsonlwatcher.RichContentBlock(nil), m.blocks...),
		Model:      s.a.Settings().Model,
		StopReason: m.stop,
		Usage:      m.usage,
		Timestamp:  m.at,
	})
}

// pause waits the given number of steps. It fails once the turn is
// interrupted.
func (s *script) pause(steps int) error {
	timer := time.NewTimer(time.Duration(steps) * s.a.step)
	defer timer.Stop()
	select {
	case <-s.t.ctx.Done():
		return errInterrupted
	case <-timer.C:
		return nil
	}
}

func (s *script) streamThinking(m *message, thinking string) error {
	m.blocks = append(m.blocks, jsonlwatcher.RichContentBlock{Type: "thinking"})
	idx := len(m.blocks) - 1
	for _, chunk := range chunks(thinking, 4) {
		m.blocks[idx].Thinking += chunk
		s.send(m)
		if err := s.pause(1); err != nil {
			return err
		}
	}
	return nil
}

// decide asks the user about a tool call and returns the tool result.
type decide func(toolUseID string, input json.RawMessage) (result string, failed bool, err error)

// tool adds a tool call to m, asks with ask if it is set, and reports the
// result.
func (s *script) tool(m *message, name string, input any, ask decide) error {
	s.tools++
	id := "toolu_" + s.id(fmt.Sprint("tool-", s.tools))
	raw, _ := json.Marshal(input)
	m.blocks = append(m.blocks, jsonlwatcher.RichContentBlock{
		Type: "tool_use", ToolUseID: id, ToolName: name, ToolInput: raw, Status: "running",
	})
	idx := len(m.blocks) - 1
	s.send(m)
	result, failed := demoListing, false
	err := s.pause(3)
	if err == nil && ask != nil {
		result, failed, err = ask(id, raw)
	}
	if err != nil {
		// Interrupted: the tool call ends without running.
		result, failed = "Interrupted by the user", true
	}
	if failed {
		m.blocks[idx].Status = "failed"
	} else {
		m.blocks[idx].Status = "completed"
	}
	s.send(m)
	isErr := failed
	s.send(&message{
		id: s.id(fmt.Sprint("result-", s.tools)), role: "user", at: s.a.now(),
		blocks: []jsonlwatcher.RichContentBlock{{
			Type: "tool_result", ToolUseID: id, ToolName: name, Text: result, IsError: &isErr, Status: m.blocks[idx].Status,
		}},
	})
	if err != nil {
		return err
	}
	return s.pause(1)
}

// wait opens req and waits for its answer.
func (s *script) wait(req native.Request) (native.Response, error) {
	id, ch := s.a.open(s.t)
	req.ID = id
	req.CreatedAt = s.a.now()
	s.sink.RequestOpened(req)
	select {
	case resp := <-ch:
		return resp, nil
	case <-s.t.ctx.Done():
		return native.Response{}, errInterrupted
	}
}

func (s *script) approval(toolUseID string, input json.RawMessage) (string, bool, error) {
	resp, err := s.wait(native.Request{
		Kind:      native.RequestKindApproval,
		Title:     "Run rm -rf ./build && make release",
		Detail:    "Rebuilds the release artifacts from scratch. This **deletes** `./build` first.",
		ToolName:  "Bash",
		ToolUseID: toolUseID,
		Input:     input,
		Options: []native.RequestOption{
			{ID: "allow", Label: "Allow", Kind: native.OptionKindAllow},
			{ID: "allow_always", Label: "Always allow Bash in this session", Kind: native.OptionKindAllowAlways},
			{ID: "deny", Label: "Deny", Description: "Tell the agent what to do instead", Kind: native.OptionKindDeny},
		},
		Feedback: true,
	})
	if err != nil {
		return "", false, err
	}
	switch resp.OptionID {
	case "deny":
		s.outcomes = append(s.outcomes, "You **denied** the release build"+feedback(resp.Text))
		return "The user denied this command" + feedback(resp.Text), true, nil
	case "allow_always":
		s.outcomes = append(s.outcomes, "You allowed Bash for the rest of the session; the release was rebuilt")
	default:
		s.outcomes = append(s.outcomes, "You allowed the release build; it finished")
	}
	return "build/\n  agentapi-linux-amd64\n  agentapi-darwin-arm64\nRelease built.", false, nil
}

// demoQuestions are what "ask" asks; the last one, a secret, only with
// "secret".
var demoQuestions = []native.Question{
	{ID: "pm", Header: "Package manager", Question: "Which package manager should I use?", Options: []native.QuestionOption{
		{Label: "bun", Description: "Fast, what the chat uses"},
		{Label: "pnpm", Description: "Strict and disk-efficient"},
		{Label: "npm", Description: "Comes with Node.js"},
	}},
	{ID: "checks", Header: "Checks", Question: "Which checks should run before I commit?", MultiSelect: true, Options: []native.QuestionOption{
		{Label: "Lint"}, {Label: "Unit tests"}, {Label: "Type check"},
	}},
	{ID: "notes", Header: "Notes", Question: "Anything else I should know?", FreeText: true},
	{ID: "token", Header: "Token", Question: "Paste the deploy token", FreeText: true, Secret: true},
}

// askInput is the AskUserQuestion input that asks questions.
func askInput(questions []native.Question) map[string]any {
	var entries []map[string]any
	for _, q := range questions {
		entry := map[string]any{"header": q.Header, "question": q.Question}
		if len(q.Options) > 0 {
			var options []map[string]string
			for _, o := range q.Options {
				option := map[string]string{"label": o.Label}
				if o.Description != "" {
					option["description"] = o.Description
				}
				options = append(options, option)
			}
			entry["multiSelect"] = q.MultiSelect
			entry["options"] = options
		}
		entries = append(entries, entry)
	}
	return map[string]any{"questions": entries}
}

// question asks questions and reports the answers.
func (s *script) question(questions []native.Question) decide {
	return func(toolUseID string, input json.RawMessage) (string, bool, error) {
		resp, err := s.wait(native.Request{
			Kind: native.RequestKindQuestion, Title: "A few questions before I start",
			ToolName: "AskUserQuestion", ToolUseID: toolUseID, Input: input, Questions: questions,
		})
		if err != nil {
			return "", false, err
		}
		var parts []string
		for _, q := range questions {
			answer := strings.Join(resp.Answers[q.ID], ", ")
			if q.Secret && answer != "" {
				answer = "(" + fmt.Sprint(len(answer)) + " characters, hidden)"
			}
			parts = append(parts, fmt.Sprintf("%s: %s", q.Header, answer))
		}
		s.outcomes = append(s.outcomes, "You answered: "+strings.Join(parts, "; "))
		return "User answered.\n" + strings.Join(parts, "\n"), false, nil
	}
}

func (s *script) plan(toolUseID string, input json.RawMessage) (string, bool, error) {
	resp, err := s.wait(native.Request{
		Kind: native.RequestKindPlan, Title: "Ready to code?", Detail: demoPlan,
		ToolName: "ExitPlanMode", ToolUseID: toolUseID, Input: input,
		Options: []native.RequestOption{
			{ID: "approve", Label: "Approve the plan", Kind: native.OptionKindAllow},
			{ID: "reject", Label: "Keep planning", Kind: native.OptionKindDeny},
		},
		Feedback: true,
	})
	if err != nil {
		return "", false, err
	}
	if resp.OptionID == "reject" {
		s.outcomes = append(s.outcomes, "You sent the plan back"+feedback(resp.Text))
		return "The user rejected the plan" + feedback(resp.Text), true, nil
	}
	s.outcomes = append(s.outcomes, "You approved the plan")
	s.leavePlanMode()
	return "The user approved the plan.", false, nil
}

// leavePlanMode switches from the plan mode to bypass once a plan is
// approved, as Claude Code does.
func (s *script) leavePlanMode() {
	s.a.mu.Lock()
	if s.a.settings.PermissionMode != native.PermissionModePlan {
		s.a.mu.Unlock()
		return
	}
	s.a.settings.PermissionMode = native.PermissionModeBypass
	settings := s.a.settings
	s.a.mu.Unlock()
	if sink, ok := s.sink.(native.SettingsSink); ok {
		sink.SettingsChanged(settings)
	}
}

// askToRun returns a decide that asks to run command, for the ask mode.
func (s *script) askToRun(command, detail string) decide {
	return func(toolUseID string, input json.RawMessage) (string, bool, error) {
		resp, err := s.wait(native.Request{
			Kind: native.RequestKindApproval, Title: "Run " + command, Detail: detail,
			ToolName: "Bash", ToolUseID: toolUseID, Input: input,
			Options: []native.RequestOption{
				{ID: "allow", Label: "Allow", Kind: native.OptionKindAllow},
				{ID: "deny", Label: "Deny", Kind: native.OptionKindDeny},
			},
			Feedback: true,
		})
		if err != nil {
			return "", false, err
		}
		if resp.OptionID == "deny" {
			s.outcomes = append(s.outcomes, "You **denied** `"+command+"`"+feedback(resp.Text))
			return "The user denied this command" + feedback(resp.Text), true, nil
		}
		return demoListing, false, nil
	}
}

func (s *script) reply(long bool) error {
	m := &message{id: s.id("reply"), role: "assistant", at: s.a.now()}
	m.blocks = []jsonlwatcher.RichContentBlock{{Type: "text"}}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Here is what I did for **%s**:\n\n- Listed the workspace\n", excerpt(s.text, 60))
	for _, o := range s.outcomes {
		sb.WriteString("- " + o + "\n")
	}
	stream := func(text string) error {
		for _, chunk := range chunks(text, 6) {
			m.blocks[0].Text += chunk
			s.send(m)
			if err := s.pause(1); err != nil {
				return err
			}
		}
		// Steered messages are taken up between paragraphs.
		s.steer(m)
		return nil
	}
	finish := func(stop string) {
		m.stop = stop
		m.usage = &jsonlwatcher.Usage{InputTokens: 1200 + 40*len(s.text), OutputTokens: 4 * len(m.blocks[0].Text) / 3}
		s.send(m)
	}
	if err := stream(sb.String()); err != nil {
		finish(native.StopReasonInterrupted)
		return err
	}
	if long {
		for i := 1; i <= 25; i++ {
			if err := stream(fmt.Sprintf("\nStep %d of 25: still working…", i)); err != nil {
				finish(native.StopReasonInterrupted)
				return err
			}
			if err := s.pause(8); err != nil {
				finish(native.StopReasonInterrupted)
				return err
			}
			s.steer(m)
		}
	}
	if err := stream("\n\nThis is the native demo agent. Say *approve*, *ask*, *plan* or *long* to see more."); err != nil {
		finish(native.StopReasonInterrupted)
		return err
	}
	s.steer(m)
	finish(native.StopReasonEndTurn)
	return nil
}

// steer appends the messages steered into the turn to the reply.
func (s *script) steer(m *message) {
	for _, text := range s.a.takeSteers(s.t) {
		m.blocks[0].Text += fmt.Sprintf("\n\n> Steered: %s\n\nGot it, I'll take that into account.\n", text)
		s.send(m)
	}
}

// keywords returns the lowercased words of text.
func keywords(text string) map[string]bool {
	words := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) }) {
		words[w] = true
	}
	return words
}

func feedback(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return ": " + strings.TrimSpace(text)
}

func excerpt(text string, n int) string {
	text = strings.Join(strings.Fields(text), " ")
	if r := []rune(text); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return text
}

// chunks splits text into pieces of about n words, keeping the spacing.
func chunks(text string, n int) []string {
	var out []string
	var cur strings.Builder
	words := 0
	for _, r := range text {
		cur.WriteRune(r)
		if r != ' ' && r != '\n' {
			continue
		}
		if words++; words >= n {
			out = append(out, cur.String())
			cur.Reset()
			words = 0
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

const demoListing = "README.md\ncmd/\nchat/\nlib/\ngo.mod\nMakefile"

const demoPlan = `## Plan: add a dark mode toggle

1. Add a ` + "`theme`" + ` setting next to the notification preference
2. Read it in ` + "`ThemeProvider`" + ` and fall back to the system theme
3. Put a toggle in the header menu

| Step | Files | Risk |
| --- | --- | --- |
| Setting | ` + "`lib/settings.ts`" + ` | low |
| Provider | ` + "`theme-provider.tsx`" + ` | low |
| Toggle | ` + "`header.tsx`" + ` | low |

I won't touch the chat transcript styles.`
