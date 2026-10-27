package claude

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/coder/quartz"

	"github.com/coder/agentapi/lib/agentlog/claudelog"
	"github.com/coder/agentapi/lib/native/agentio"
	"github.com/coder/agentapi/lib/transcript"
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
	// Usage is the turn's token usage; only the cache is read from it.
	Usage *resultUsage `json:"usage"`

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

// apiMessage is the message of an assistant frame or message_start event.
type apiMessage struct {
	ID         string            `json:"id"`
	Model      string            `json:"model"`
	Content    json.RawMessage   `json:"content"`
	StopReason *string           `json:"stop_reason"`
	Usage      *transcript.Usage `json:"usage"`
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
	Usage *transcript.Usage `json:"usage"`
}

// liveBlock is one content block of an assistant message: the final block
// once its assistant frame arrived, else what the deltas built so far.
type liveBlock struct {
	final    *transcript.RichContentBlock
	kind     string
	text     strings.Builder
	thinking strings.Builder
	toolID   string
	toolName string
}

func (b *liveBlock) rich() transcript.RichContentBlock {
	if b.final != nil {
		return *b.final
	}
	switch b.kind {
	case "thinking":
		return transcript.RichContentBlock{Type: "thinking", Thinking: b.thinking.String()}
	case "tool_use":
		return transcript.RichContentBlock{Type: "tool_use", ToolUseID: b.toolID, ToolName: b.toolName}
	default:
		return transcript.RichContentBlock{Type: b.kind, Text: b.text.String()}
	}
}

// assistantMessage is a main-thread assistant message being assembled.
type assistantMessage struct {
	msg      transcript.RichMessage
	blocks   []*liveBlock
	streamed bool
	sent     bool
	dirty    bool
	lastSent time.Time
}

// resultRef locates the tool_result of a tool call.
type resultRef struct {
	msg   *transcript.RichMessage
	index int
}

// streamParser turns stream-json frames into rich messages and timeline
// events of the same shape the session-log parsers produce. It differs from
// claudelog.Parser where the stream differs from the session log:
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
	// drift counts the frames whose fields changed type; nil in tests.
	drift *agentio.Drift
	// sessionID labels timeline events.
	sessionID string
	events    *claudelog.EventParser
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
		events:     claudelog.NewEventParser(),
		messages:   make(map[string]*assistantMessage),
		results:    make(map[string]resultRef),
		background: make(map[string]bool),
		userPrefix: newUUID()[:8],
	}
}

func (p *streamParser) message(id, model string) *assistantMessage {
	m, ok := p.messages[id]
	if !ok {
		m = &assistantMessage{msg: transcript.RichMessage{
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

// snapshot returns the message's current state. It shares the parser's
// values: the Sink copies what it keeps.
func (p *streamParser) snapshot(m *assistantMessage) transcript.RichMessage {
	m.sent = true
	m.dirty = false
	m.lastSent = p.clock.Now()
	out := m.msg
	out.Content = make([]transcript.RichContentBlock, 0, len(m.blocks))
	for _, b := range m.blocks {
		out.Content = append(out.Content, b.rich())
	}
	return out
}

// startMessage begins the message a message_start event announces; id is
// the frame's message ID, if it has one.
func (p *streamParser) startMessage(id string, msg *apiMessage) {
	if id == "" {
		id = msg.ID
	}
	p.current = id
	m := p.message(id, msg.Model)
	m.streamed = true
	if msg.Usage != nil && m.msg.Usage == nil {
		m.msg.Usage = msg.Usage
	}
}

// startBlock begins block index from a content_block_start event, unless
// its final state arrived already, and reports whether it did.
func (m *assistantMessage) startBlock(index int, raw json.RawMessage, drift *agentio.Drift) bool {
	var block claudelog.ContentBlock
	if drift.Decode("content_block_start", raw, &block) != nil {
		return false
	}
	for len(m.blocks) <= index {
		m.blocks = append(m.blocks, &liveBlock{})
	}
	b := m.blocks[index]
	if b.final == nil {
		b.kind, b.toolID, b.toolName = block.Type, block.ID, block.Name
		b.text.WriteString(block.Text)
		b.thinking.WriteString(block.Thinking)
	}
	m.dirty = true
	return true
}

// addDelta adds the text or thinking of a content_block_delta to block
// index, and reports whether it changed it: input_json_delta is parsed
// only once the block is complete, and signatures aren't shown.
func (m *assistantMessage) addDelta(index int, kind, text, thinking string) bool {
	if index >= len(m.blocks) || m.blocks[index].final != nil {
		return false
	}
	b := m.blocks[index]
	switch kind {
	case "text_delta":
		b.text.WriteString(text)
	case "thinking_delta":
		b.thinking.WriteString(thinking)
	default:
		return false
	}
	m.dirty = true
	return true
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
func (p *streamParser) flush() []transcript.RichMessage {
	var out []transcript.RichMessage
	for _, id := range p.order {
		m := p.messages[id]
		if m.dirty && len(m.blocks) > 0 && p.due(m) {
			out = append(out, p.snapshot(m))
		}
	}
	return out
}

// flushAll returns snapshots of every message with changes the throttle
// held back, due or not: the CLI's output ended.
func (p *streamParser) flushAll() []transcript.RichMessage {
	var out []transcript.RichMessage
	for _, id := range p.order {
		m := p.messages[id]
		if m.dirty && len(m.blocks) > 0 {
			out = append(out, p.snapshot(m))
		}
	}
	return out
}

// streamEvent applies a partial-message event of the main thread.
func (p *streamParser) streamEvent(f *frame) []transcript.RichMessage {
	if f.subagent() {
		return nil
	}
	var ev streamEvent
	if p.drift.Decode("stream_event", f.Event, &ev) != nil {
		return nil
	}
	id := f.APIMessageID
	if ev.Type == "message_start" && ev.Message != nil {
		p.startMessage(id, ev.Message)
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
		if !m.startBlock(ev.Index, ev.ContentBlock, p.drift) {
			return nil
		}
	case "content_block_delta":
		if !m.addDelta(ev.Index, ev.Delta.Type, ev.Delta.Text, ev.Delta.Thinking) {
			return nil
		}
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
		return []transcript.RichMessage{p.snapshot(m)}
	default:
		return nil
	}
	if len(m.blocks) == 0 || !m.dirty || !p.due(m) {
		return nil
	}
	return []transcript.RichMessage{p.snapshot(m)}
}

// assistant applies a complete content block of the main thread.
func (p *streamParser) assistant(f *frame) ([]transcript.RichMessage, []transcript.SessionEvent) {
	if f.subagent() {
		return nil, nil
	}
	var msg apiMessage
	if p.drift.Decode("assistant", f.Message, &msg) != nil || msg.ID == "" {
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
	for _, block := range claudelog.ContentBlocks(msg.Content) {
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
	return []transcript.RichMessage{p.snapshot(m)}, p.sessionEvents(f)
}

// user applies a user frame of the main thread: tool results, a replayed
// prompt or an interrupt marker.
func (p *streamParser) user(f *frame) ([]transcript.RichMessage, []transcript.SessionEvent) {
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
	rich := &transcript.RichMessage{MessageID: id, Role: "user", Timestamp: p.clock.Now()}
	var text string
	if json.Unmarshal(msg.Content, &text) == nil {
		rich.Content = []transcript.RichContentBlock{{Type: "text", Text: text}}
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
	return []transcript.RichMessage{*rich}, p.sessionEvents(f)
}

// userBlock converts one content block of a user frame, as the session
// log's parser does, with the status of a tool result.
func (p *streamParser) userBlock(raw json.RawMessage) (transcript.RichContentBlock, bool) {
	block, ok := claudelog.UserBlock(raw)
	if ok && block.Type == "tool_result" {
		block.Status = "completed"
		switch {
		case p.background[block.ToolUseID]:
			block.Status = "running"
		case block.IsError != nil && *block.IsError:
			block.Status = "failed"
		}
	}
	return block, ok
}

// sessionEvents converts a main-thread assistant or user frame to timeline
// events with the session-log event parser.
func (p *streamParser) sessionEvents(f *frame) []transcript.SessionEvent {
	var msg claudelog.Message
	if err := p.drift.Decode(f.Type, f.Message, &msg); err != nil {
		return nil
	}
	sessionID := f.SessionID
	if sessionID == "" {
		sessionID = p.sessionID
	}
	return p.events.Events(claudelog.Line{
		Type: f.Type, UUID: f.UUID, Timestamp: f.Timestamp, SessionID: sessionID, Message: &msg,
	})
}

// taskStarted notes a backgrounded task. Its tool result, which arrives
// right away, stays running until the task finishes.
func (p *streamParser) taskStarted(f *frame) []transcript.RichMessage {
	if !f.IsBackgrounded || f.ToolUseID == "" {
		return nil
	}
	p.background[f.ToolUseID] = true
	return p.setResultStatus(f.ToolUseID, "running")
}

// taskNotification ends a backgrounded task.
func (p *streamParser) taskNotification(f *frame) []transcript.RichMessage {
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

func (p *streamParser) setResultStatus(toolUseID, status string) []transcript.RichMessage {
	ref, ok := p.results[toolUseID]
	if !ok || ref.msg.Content[ref.index].Status == status {
		return nil
	}
	ref.msg.Content[ref.index].Status = status
	return []transcript.RichMessage{*ref.msg}
}

// patchToolInput fills in the input of a tool call whose block came without
// one (ExitPlanMode's plan is added when the CLI asks for permission).
func (p *streamParser) patchToolInput(toolUseID string, input json.RawMessage) []transcript.RichMessage {
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
			return []transcript.RichMessage{p.snapshot(m)}
		}
	}
	return nil
}

// endTurn sends the final state of the turn's messages and forgets them.
func (p *streamParser) endTurn() []transcript.RichMessage {
	var out []transcript.RichMessage
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
