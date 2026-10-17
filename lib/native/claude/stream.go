package claude

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/coder/quartz"

	"github.com/coder/agentapi/lib/jsonlwatcher"
)

// frame is one line of Claude Code's stream-json output. Only the fields
// the adapter reads are declared.
type frame struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	UUID      string `json:"uuid"`
	SessionID string `json:"session_id"`
	// ParentToolUseID is set on a subagent's frames: the Agent tool call
	// that runs it.
	ParentToolUseID *string         `json:"parent_tool_use_id"`
	Timestamp       string          `json:"timestamp"`
	Message         json.RawMessage `json:"message"`

	// stream_event
	Event        json.RawMessage `json:"event"`
	APIMessageID string          `json:"api_message_id"`

	// control_request, control_response, control_cancel_request
	RequestID string          `json:"request_id"`
	Request   json.RawMessage `json:"request"`
	Response  json.RawMessage `json:"response"`

	// system
	State          string `json:"state"`
	Title          string `json:"title"`
	ToolUseID      string `json:"tool_use_id"`
	IsBackgrounded bool   `json:"is_backgrounded"`
	// Status is a string on task frames and null or a string on status
	// frames.
	Status json.RawMessage `json:"status"`
	// PermissionMode is on init and status frames, Model on init frames.
	PermissionMode string `json:"permissionMode"`
	Model          string `json:"model"`

	// rate_limit_event
	RateLimitInfo json.RawMessage `json:"rate_limit_info"`

	// result
	IsError        bool            `json:"is_error"`
	Result         json.RawMessage `json:"result"`
	StopReason     *string         `json:"stop_reason"`
	TerminalReason string          `json:"terminal_reason"`
	Errors         []string        `json:"errors"`
	APIErrorStatus json.RawMessage `json:"api_error_status"`

	// assistant: the kind of API error a synthetic message reports, e.g.
	// "authentication_failed". Raw, so a frame with another shape still
	// parses.
	Error json.RawMessage `json:"error"`
}

// subagent reports whether the frame belongs to a subagent.
func (f *frame) subagent() bool { return f.ParentToolUseID != nil && *f.ParentToolUseID != "" }

// stringField decodes a JSON string, or returns "" for null or other values.
func stringField(raw json.RawMessage) string {
	var s string
	if len(raw) == 0 || json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}

// decode is json.Unmarshal that keeps what it could read when a field has
// an unexpected type: Unmarshal fills the other fields and reports only the
// first mismatch. A frame isn't lost to one field that changed shape; a
// lost result would never end the turn, a lost permission request never be
// answered.
func decode(data []byte, v any) error {
	err := json.Unmarshal(data, v)
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		return nil
	}
	return err
}

// apiMessage is the message of an assistant frame or message_start event.
type apiMessage struct {
	ID         string              `json:"id"`
	Model      string              `json:"model"`
	Content    json.RawMessage     `json:"content"`
	StopReason *string             `json:"stop_reason"`
	Usage      *jsonlwatcher.Usage `json:"usage"`
}

// streamEvent is the Anthropic SSE event of a stream_event frame.
type streamEvent struct {
	Type         string          `json:"type"`
	Index        int             `json:"index"`
	Message      *apiMessage     `json:"message"`
	ContentBlock json.RawMessage `json:"content_block"`
	Delta        struct {
		Type       string  `json:"type"`
		Text       string  `json:"text"`
		Thinking   string  `json:"thinking"`
		StopReason *string `json:"stop_reason"`
	} `json:"delta"`
	Usage *jsonlwatcher.Usage `json:"usage"`
}

// liveBlock is one content block of an assistant message: the final block
// once its assistant frame arrived, else what the deltas built so far.
type liveBlock struct {
	final    *jsonlwatcher.RichContentBlock
	kind     string
	text     strings.Builder
	thinking strings.Builder
	toolID   string
	toolName string
}

func (b *liveBlock) rich() jsonlwatcher.RichContentBlock {
	if b.final != nil {
		return *b.final
	}
	switch b.kind {
	case "thinking":
		return jsonlwatcher.RichContentBlock{Type: "thinking", Thinking: b.thinking.String()}
	case "tool_use":
		return jsonlwatcher.RichContentBlock{Type: "tool_use", ToolUseID: b.toolID, ToolName: b.toolName}
	default:
		return jsonlwatcher.RichContentBlock{Type: b.kind, Text: b.text.String()}
	}
}

// assistantMessage is a main-thread assistant message being assembled.
type assistantMessage struct {
	msg      jsonlwatcher.RichMessage
	blocks   []*liveBlock
	streamed bool
	sent     bool
	dirty    bool
	lastSent time.Time
}

// resultRef locates the tool_result of a tool call.
type resultRef struct {
	msg   *jsonlwatcher.RichMessage
	index int
}

// streamParser turns stream-json frames into rich messages and timeline
// events of the same shape the session-log parsers produce. It differs from
// jsonlwatcher.ClaudeParser where the stream differs from the session log:
// stop_reason and usage come from message_delta (assistant frames carry
// null), a tool result between the blocks of one message doesn't split it,
// text-only user frames (interrupt markers, replayed prompts) are not tool
// results, and subagent frames are left out (the session log keeps them in
// separate files too). Blocks are converted with the session-log parser's
// functions.
//
// It is used by the read loop only.
type streamParser struct {
	clock    quartz.Clock
	throttle time.Duration
	// sessionID labels timeline events.
	sessionID string
	events    *jsonlwatcher.ClaudeSessionEventParser
	messages  map[string]*assistantMessage
	order     []string
	results   map[string]resultRef
	// current is the message the last message_start began.
	current string
	// background holds the tool calls of backgrounded tasks (subagents,
	// background commands) that haven't finished; their results stay
	// running until the task reports.
	background map[string]bool
	// userPrefix and userSeq make ids for user frames without one. The
	// random prefix keeps them from repeating those of an earlier adapter,
	// whose messages the conversation keeps.
	userPrefix string
	userSeq    int
}

func newStreamParser(clock quartz.Clock, throttle time.Duration) *streamParser {
	return &streamParser{
		clock:      clock,
		throttle:   throttle,
		events:     jsonlwatcher.NewClaudeSessionEventParser(),
		messages:   make(map[string]*assistantMessage),
		results:    make(map[string]resultRef),
		background: make(map[string]bool),
		userPrefix: newUUID()[:8],
	}
}

func (p *streamParser) message(id, model string) *assistantMessage {
	m, ok := p.messages[id]
	if !ok {
		m = &assistantMessage{msg: jsonlwatcher.RichMessage{
			MessageID: id, Role: "assistant", Model: model, Timestamp: p.clock.Now(),
		}}
		p.messages[id] = m
		p.order = append(p.order, id)
	}
	if m.msg.Model == "" {
		m.msg.Model = model
	}
	return m
}

// snapshot returns the message's current state, sharing nothing with the
// parser (the server rewrites images in place).
func (p *streamParser) snapshot(m *assistantMessage) jsonlwatcher.RichMessage {
	m.sent = true
	m.dirty = false
	m.lastSent = p.clock.Now()
	out := m.msg
	out.Content = make([]jsonlwatcher.RichContentBlock, 0, len(m.blocks))
	for _, b := range m.blocks {
		out.Content = append(out.Content, cloneBlock(b.rich()))
	}
	if m.msg.Usage != nil {
		usage := *m.msg.Usage
		out.Usage = &usage
	}
	return out
}

// due reports whether a throttled snapshot of m may be sent now.
func (p *streamParser) due(m *assistantMessage) bool {
	return !m.sent || p.clock.Since(m.lastSent) >= p.throttle
}

// nextFlush returns when the first change the throttle held back may be
// sent, or the zero time if there is none.
func (p *streamParser) nextFlush() time.Time {
	var next time.Time
	for _, m := range p.messages {
		if !m.dirty || len(m.blocks) == 0 {
			continue
		}
		if at := m.lastSent.Add(p.throttle); next.IsZero() || at.Before(next) {
			next = at
		}
	}
	return next
}

// flush returns snapshots of the messages whose held-back changes may be
// sent now, so a pause in the stream doesn't leave them unsent.
func (p *streamParser) flush() []jsonlwatcher.RichMessage {
	var out []jsonlwatcher.RichMessage
	for _, id := range p.order {
		m := p.messages[id]
		if m.dirty && len(m.blocks) > 0 && p.due(m) {
			out = append(out, p.snapshot(m))
		}
	}
	return out
}

func cloneBlock(b jsonlwatcher.RichContentBlock) jsonlwatcher.RichContentBlock {
	if b.Images != nil {
		b.Images = append([]jsonlwatcher.RichImage(nil), b.Images...)
	}
	if b.IsError != nil {
		v := *b.IsError
		b.IsError = &v
	}
	return b
}

func cloneMessage(m jsonlwatcher.RichMessage) jsonlwatcher.RichMessage {
	content := make([]jsonlwatcher.RichContentBlock, len(m.Content))
	for i, b := range m.Content {
		content[i] = cloneBlock(b)
	}
	m.Content = content
	return m
}

// streamEvent applies a partial-message event of the main thread.
func (p *streamParser) streamEvent(f *frame) []jsonlwatcher.RichMessage {
	if f.subagent() {
		return nil
	}
	var ev streamEvent
	if json.Unmarshal(f.Event, &ev) != nil {
		return nil
	}
	id := f.APIMessageID
	if ev.Type == "message_start" && ev.Message != nil {
		if id == "" {
			id = ev.Message.ID
		}
		p.current = id
		m := p.message(id, ev.Message.Model)
		m.streamed = true
		if ev.Message.Usage != nil && m.msg.Usage == nil {
			m.msg.Usage = ev.Message.Usage
		}
		return nil
	}
	if id == "" {
		// The events of a message follow its message_start.
		id = p.current
	}
	m, ok := p.messages[id]
	if !ok {
		return nil
	}
	switch ev.Type {
	case "content_block_start":
		var block jsonlwatcher.ContentBlock
		if json.Unmarshal(ev.ContentBlock, &block) != nil {
			return nil
		}
		for len(m.blocks) <= ev.Index {
			m.blocks = append(m.blocks, &liveBlock{})
		}
		b := m.blocks[ev.Index]
		if b.final == nil {
			b.kind, b.toolID, b.toolName = block.Type, block.ID, block.Name
			b.text.WriteString(block.Text)
			b.thinking.WriteString(block.Thinking)
		}
		m.dirty = true
	case "content_block_delta":
		if ev.Index >= len(m.blocks) || m.blocks[ev.Index].final != nil {
			return nil
		}
		b := m.blocks[ev.Index]
		switch ev.Delta.Type {
		case "text_delta":
			b.text.WriteString(ev.Delta.Text)
		case "thinking_delta":
			b.thinking.WriteString(ev.Delta.Thinking)
		default:
			// input_json_delta is parsed only once complete; signatures
			// aren't shown.
			return nil
		}
		m.dirty = true
	case "message_delta":
		if ev.Delta.StopReason != nil {
			m.msg.StopReason = *ev.Delta.StopReason
		}
		if ev.Usage != nil {
			m.msg.Usage = ev.Usage
		}
		if !m.sent {
			return nil
		}
		return []jsonlwatcher.RichMessage{p.snapshot(m)}
	default:
		return nil
	}
	if len(m.blocks) == 0 || !m.dirty || !p.due(m) {
		return nil
	}
	return []jsonlwatcher.RichMessage{p.snapshot(m)}
}

// assistant applies a complete content block of the main thread.
func (p *streamParser) assistant(f *frame) ([]jsonlwatcher.RichMessage, []jsonlwatcher.SessionEvent) {
	if f.subagent() {
		return nil, nil
	}
	var msg apiMessage
	if json.Unmarshal(f.Message, &msg) != nil || msg.ID == "" {
		return nil, nil
	}
	m := p.message(msg.ID, msg.Model)
	if !m.streamed {
		// A message that wasn't streamed (a synthetic one such as "Not
		// logged in") has its only stop reason and usage here.
		if msg.StopReason != nil {
			m.msg.StopReason = *msg.StopReason
		}
		if msg.Usage != nil {
			m.msg.Usage = msg.Usage
		}
	}
	for _, block := range jsonlwatcher.ClaudeContentBlocks(msg.Content) {
		placed := false
		// Blocks complete in index order, each just before its
		// content_block_stop: the first one still streaming is this one.
		for _, b := range m.blocks {
			if b.final == nil {
				b.final = &block
				placed = true
				break
			}
		}
		if !placed {
			m.blocks = append(m.blocks, &liveBlock{final: &block})
		}
	}
	return []jsonlwatcher.RichMessage{p.snapshot(m)}, p.sessionEvents(f)
}

// user applies a user frame of the main thread: tool results, a replayed
// prompt or an interrupt marker.
func (p *streamParser) user(f *frame) ([]jsonlwatcher.RichMessage, []jsonlwatcher.SessionEvent) {
	if f.subagent() {
		return nil, nil
	}
	var msg struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(f.Message, &msg) != nil || len(msg.Content) == 0 {
		return nil, nil
	}
	id := f.UUID
	if id == "" {
		p.userSeq++
		id = fmt.Sprintf("user-%s-%d", p.userPrefix, p.userSeq)
	}
	rich := &jsonlwatcher.RichMessage{MessageID: id, Role: "user", Timestamp: p.clock.Now()}
	var text string
	if json.Unmarshal(msg.Content, &text) == nil {
		rich.Content = []jsonlwatcher.RichContentBlock{{Type: "text", Text: text}}
	} else {
		var parts []json.RawMessage
		if json.Unmarshal(msg.Content, &parts) != nil {
			return nil, nil
		}
		for _, part := range parts {
			block, ok := p.userBlock(part)
			if !ok {
				continue
			}
			if block.Type == "tool_result" && block.ToolUseID != "" {
				p.results[block.ToolUseID] = resultRef{msg: rich, index: len(rich.Content)}
			}
			rich.Content = append(rich.Content, block)
		}
	}
	if len(rich.Content) == 0 {
		return nil, nil
	}
	return []jsonlwatcher.RichMessage{cloneMessage(*rich)}, p.sessionEvents(f)
}

// userBlock converts one content block of a user frame.
func (p *streamParser) userBlock(raw json.RawMessage) (jsonlwatcher.RichContentBlock, bool) {
	var head struct {
		Type   string                    `json:"type"`
		Text   string                    `json:"text"`
		Source *jsonlwatcher.ImageSource `json:"source"`
	}
	if json.Unmarshal(raw, &head) != nil {
		return jsonlwatcher.RichContentBlock{}, false
	}
	switch head.Type {
	case "tool_result":
		var result jsonlwatcher.ToolResultBlock
		_ = decode(raw, &result)
		block := jsonlwatcher.ClaudeToolResult(result)
		block.Status = "completed"
		switch {
		case p.background[block.ToolUseID]:
			block.Status = "running"
		case block.IsError != nil && *block.IsError:
			block.Status = "failed"
		}
		return block, true
	case "text":
		return jsonlwatcher.RichContentBlock{Type: "text", Text: head.Text}, true
	case "image":
		block := jsonlwatcher.RichContentBlock{Type: "image"}
		if image, ok := jsonlwatcher.ClaudeImage(head.Source); ok {
			block.Images = []jsonlwatcher.RichImage{image}
		}
		return block, true
	default:
		return jsonlwatcher.RichContentBlock{Type: head.Type, Text: head.Text}, true
	}
}

// sessionEvents converts a main-thread assistant or user frame to timeline
// events with the session-log event parser.
func (p *streamParser) sessionEvents(f *frame) []jsonlwatcher.SessionEvent {
	var msg jsonlwatcher.JSONLMessage
	if err := json.Unmarshal(f.Message, &msg); err != nil {
		return nil
	}
	sessionID := f.SessionID
	if sessionID == "" {
		sessionID = p.sessionID
	}
	return p.events.Events(jsonlwatcher.JSONLLine{
		Type: f.Type, UUID: f.UUID, Timestamp: f.Timestamp, SessionID: sessionID, Message: &msg,
	})
}

// taskStarted notes a backgrounded task. Its tool result, which arrives
// right away, stays running until the task finishes.
func (p *streamParser) taskStarted(f *frame) []jsonlwatcher.RichMessage {
	if !f.IsBackgrounded || f.ToolUseID == "" {
		return nil
	}
	p.background[f.ToolUseID] = true
	return p.setResultStatus(f.ToolUseID, "running")
}

// taskNotification ends a backgrounded task.
func (p *streamParser) taskNotification(f *frame) []jsonlwatcher.RichMessage {
	if !p.background[f.ToolUseID] {
		return nil
	}
	delete(p.background, f.ToolUseID)
	status := "failed"
	if stringField(f.Status) == "completed" {
		status = "completed"
	}
	msgs := p.setResultStatus(f.ToolUseID, status)
	delete(p.results, f.ToolUseID)
	return msgs
}

func (p *streamParser) setResultStatus(toolUseID, status string) []jsonlwatcher.RichMessage {
	ref, ok := p.results[toolUseID]
	if !ok || ref.msg.Content[ref.index].Status == status {
		return nil
	}
	ref.msg.Content[ref.index].Status = status
	return []jsonlwatcher.RichMessage{cloneMessage(*ref.msg)}
}

// patchToolInput fills in the input of a tool call whose block came without
// one (ExitPlanMode's plan is added when the CLI asks for permission).
func (p *streamParser) patchToolInput(toolUseID string, input json.RawMessage) []jsonlwatcher.RichMessage {
	if toolUseID == "" || len(input) == 0 {
		return nil
	}
	for _, id := range p.order {
		m := p.messages[id]
		for _, b := range m.blocks {
			if b.final == nil || b.final.ToolUseID != toolUseID {
				continue
			}
			trimmed := bytes.TrimSpace(b.final.ToolInput)
			if len(trimmed) != 0 && !bytes.Equal(trimmed, []byte("{}")) {
				return nil
			}
			b.final.ToolInput = input
			return []jsonlwatcher.RichMessage{p.snapshot(m)}
		}
	}
	return nil
}

// endTurn sends the final state of the turn's messages and forgets them.
func (p *streamParser) endTurn() []jsonlwatcher.RichMessage {
	var out []jsonlwatcher.RichMessage
	for _, id := range p.order {
		m := p.messages[id]
		if len(m.blocks) > 0 && (m.dirty || !m.sent) {
			out = append(out, p.snapshot(m))
		}
	}
	p.messages = make(map[string]*assistantMessage)
	p.order = nil
	p.current = ""
	for id := range p.results {
		if !p.background[id] {
			delete(p.results, id)
		}
	}
	return out
}
