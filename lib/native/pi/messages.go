package pi

import (
	"encoding/base64"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/coder/quartz"

	"github.com/coder/agentapi/lib/native"
	"github.com/coder/agentapi/lib/transcript"
)

// streamMessage is an assistant message between message_start and
// message_end, built from message_update deltas.
type streamMessage struct {
	id        string
	timestamp int64
	model     string
	blocks    map[int]*streamBlock
}

type streamBlock struct {
	kind     string // "text", "thinking" or "toolCall"
	text     strings.Builder
	toolID   string
	toolName string
	input    json.RawMessage
}

// replace sets the text of a block to its final content.
func (b *streamBlock) replace(text string) {
	b.text.Reset()
	b.text.WriteString(text)
}

// snapshot is the message as streamed so far, in the shape the Pi parser
// gives the finished message.
func (s *streamMessage) snapshot() transcript.RichMessage {
	msg := transcript.RichMessage{
		MessageID: s.id,
		Role:      "assistant",
		Model:     s.model,
		Timestamp: native.UnixMilli(s.timestamp),
	}
	indexes := make([]int, 0, len(s.blocks))
	for i := range s.blocks {
		indexes = append(indexes, i)
	}
	sort.Ints(indexes)
	for _, i := range indexes {
		b := s.blocks[i]
		switch b.kind {
		case "text":
			if text := b.text.String(); strings.TrimSpace(text) != "" {
				msg.Content = append(msg.Content, transcript.RichContentBlock{Type: "text", Text: text})
			}
		case "thinking":
			if text := b.text.String(); strings.TrimSpace(text) != "" {
				msg.Content = append(msg.Content, transcript.RichContentBlock{Type: "thinking", Thinking: text})
			}
		case "toolCall":
			msg.Content = append(msg.Content, transcript.RichContentBlock{Type: "tool_use", ToolUseID: b.toolID, ToolName: b.toolName, ToolInput: b.input})
		}
	}
	return msg
}

// build returns the snapshot to send, unless it has no content yet.
func (s *streamMessage) build() (transcript.RichMessage, bool) {
	snap := s.snapshot()
	return snap, len(snap.Content) > 0
}

func (s *streamMessage) block(i int, kind string) *streamBlock {
	b := s.blocks[i]
	if b == nil || b.kind != kind {
		b = &streamBlock{kind: kind}
		s.blocks[i] = b
	}
	return b
}

// apply adds a message_update event to the message. It reports whether
// the snapshot changed.
func (s *streamMessage) apply(ev *streamEvent) bool {
	switch ev.Type {
	case "text_start":
		s.block(ev.ContentIndex, "text")
	case "text_delta":
		s.block(ev.ContentIndex, "text").text.WriteString(ev.Delta)
	case "text_end":
		s.block(ev.ContentIndex, "text").replace(ev.Content)
	case "thinking_start":
		s.block(ev.ContentIndex, "thinking")
	case "thinking_delta":
		s.block(ev.ContentIndex, "thinking").text.WriteString(ev.Delta)
	case "thinking_end":
		// The summary deltas can differ from the final text.
		s.block(ev.ContentIndex, "thinking").replace(ev.Content)
	case "toolcall_start":
		b := s.block(ev.ContentIndex, "toolCall")
		b.toolID, b.toolName = ev.ID, ev.ToolName
	case "toolcall_end":
		// The deltas before it are partial JSON; the input shows once it
		// is complete.
		b := s.block(ev.ContentIndex, "toolCall")
		if ev.ToolCall != nil {
			b.toolID, b.toolName, b.input = ev.ToolCall.ID, ev.ToolCall.Name, ev.ToolCall.Arguments
		}
	default:
		return false
	}
	return true
}

func (a *Adapter) handleMessageEvent(kind string, ev messageEvent) {
	var msg agentMessage
	if len(ev.Message) > 0 && a.drift.Decode(kind+" message", ev.Message, &msg) != nil {
		return
	}
	switch kind {
	case "message_start":
		if msg.Role == "assistant" {
			a.loop.stream = &streamMessage{
				id:        a.loop.uniqueID(messageID(msg)),
				timestamp: msg.Timestamp,
				model:     msg.Model,
				blocks:    make(map[int]*streamBlock),
			}
		}
	case "message_update":
		s := a.loop.stream
		if s == nil || ev.AssistantMessageEvent == nil || !s.apply(ev.AssistantMessageEvent) {
			return
		}
		a.emitThrottled(s.id, s.build)
	case "message_end":
		a.handleMessageEnd(msg, ev.Message)
	}
}

// handleMessageEnd emits the finished message. message_end carries the
// message as Pi writes it to its session file, so pilog's parser
// turns it into the same rich messages and timeline events.
func (a *Adapter) handleMessageEnd(msg agentMessage, raw json.RawMessage) {
	if msg.Role == "system" {
		return
	}
	var id string
	streamed := false
	switch msg.Role {
	case "assistant":
		if s := a.loop.stream; s != nil && s.timestamp == msg.Timestamp {
			id, streamed = s.id, a.sent(s.id)
		} else {
			id = a.loop.uniqueID(messageID(msg))
		}
		a.loop.stream = nil
		if msg.StopReason == "error" && msg.ErrorMessage == abortedRunMessage {
			// The extra message of a run aborted during a tool call.
			msg.StopReason = "aborted"
			raw = withoutError(raw)
		}
		switch msg.StopReason {
		case "error", "aborted", "length":
			a.loop.stopReason, a.loop.errMessage = msg.StopReason, msg.ErrorMessage
		case "stop", "toolUse":
			// A retry or a later message replaces an earlier failure.
			a.loop.stopReason, a.loop.errMessage = msg.StopReason, ""
		}
		if msg.Usage != nil && msg.StopReason != "error" && msg.StopReason != "aborted" {
			a.contextUsed(msg.Usage)
		}
	case "toolResult":
		id = messageID(msg)
		streamed = a.sent(id)
	default:
		id = a.loop.uniqueID(messageID(msg))
	}

	rich, err := a.parsePiMessage(id, raw)
	if err != nil {
		a.log.Warn("Failed to parse a Pi message", "error", err)
		return
	}
	events := a.loop.events.MessageEvents(rich)
	if msg.Role == "toolResult" {
		setToolResultImages(rich, msg.Content)
	}
	if len(rich) == 0 && streamed {
		// Replace what was streamed, e.g. whitespace-only text. Only an
		// assistant message can be streamed and come out empty.
		rich = []transcript.RichMessage{{MessageID: id, Role: "assistant", Content: []transcript.RichContentBlock{}, Timestamp: native.UnixMilli(msg.Timestamp)}}
	}
	for _, m := range rich {
		a.emitFinal(m)
	}
	if len(events) > 0 {
		a.loop.sink.SessionEvents(events)
	}
}

// parsePiMessage turns a Pi message into rich messages with pilog's
// parser, as the line of Pi's session file that holds it.
func (a *Adapter) parsePiMessage(id string, raw json.RawMessage) ([]transcript.RichMessage, error) {
	line, err := json.Marshal(struct {
		Type    string          `json:"type"`
		ID      string          `json:"id"`
		Message json.RawMessage `json:"message"`
	}{"message", id, raw})
	if err != nil {
		return nil, err
	}
	return a.loop.parser.ParseLine(line)
}

// withoutError drops errorMessage from a message.
func withoutError(raw json.RawMessage) json.RawMessage {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return raw
	}
	delete(fields, "errorMessage")
	fields["stopReason"] = json.RawMessage(`"aborted"`)
	out, err := json.Marshal(fields)
	if err != nil {
		return raw
	}
	return out
}

// setToolResultImages adds the images of a tool result's content (an
// image file the agent read), which the Pi parser leaves out, to its
// tool_result blocks.
func setToolResultImages(rich []transcript.RichMessage, content json.RawMessage) {
	images := contentImages(content)
	if len(images) == 0 {
		return
	}
	for i := range rich {
		for j := range rich[i].Content {
			if block := &rich[i].Content[j]; block.Type == "tool_result" {
				block.Images = images
			}
		}
	}
}

// contentImages decodes the images of a Pi content array.
func contentImages(raw json.RawMessage) []transcript.RichImage {
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil
	}
	var images []transcript.RichImage
	for _, b := range blocks {
		if b.Type != "image" || b.Data == "" {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(b.Data)
		if err != nil {
			continue
		}
		images = append(images, transcript.RichImage{MediaType: b.MimeType, Data: data})
	}
	return images
}

func (a *Adapter) handleToolEvent(kind string, ev toolEvent) {
	switch kind {
	case "tool_execution_start":
		a.loop.tools = append(a.loop.tools, runningTool{id: ev.ToolCallID, name: ev.ToolName, args: ev.Args})
	case "tool_execution_update":
		if ev.PartialResult == nil {
			return
		}
		// Bash output so far; each update holds all of it.
		a.emitThrottled(resultID(ev.ToolCallID), func() (transcript.RichMessage, bool) {
			return a.toolResultMessage(ev.ToolCallID, ev.ToolName, ev.PartialResult.Content, false, "running"), true
		})
	case "tool_execution_end":
		for i, t := range a.loop.tools {
			if t.id == ev.ToolCallID {
				a.loop.tools = append(a.loop.tools[:i], a.loop.tools[i+1:]...)
				break
			}
		}
		if ev.Result == nil {
			return
		}
		// The tool result message follows; show the result already.
		a.emitThrottled(resultID(ev.ToolCallID), func() (transcript.RichMessage, bool) {
			return a.toolResultMessage(ev.ToolCallID, ev.ToolName, ev.Result.Content, ev.IsError || ev.Result.IsError, ""), true
		})
	}
}

// toolResultMessage builds a tool result like the Pi parser does for the
// toolResult message, with status overridden while the tool runs.
func (a *Adapter) toolResultMessage(callID, toolName string, content json.RawMessage, isError bool, status string) transcript.RichMessage {
	if len(content) == 0 {
		content = json.RawMessage(`[]`)
	}
	raw, _ := json.Marshal(map[string]any{
		"role": "toolResult", "toolCallId": callID, "toolName": toolName,
		"content": content, "isError": isError, "timestamp": a.clock.Now().UnixMilli(),
	})
	rich, _ := a.parsePiMessage(resultID(callID), raw)
	if len(rich) == 0 {
		return transcript.RichMessage{MessageID: resultID(callID), Role: "user", Content: []transcript.RichContentBlock{}}
	}
	setToolResultImages(rich, content)
	msg := rich[0]
	if status != "" {
		for i := range msg.Content {
			msg.Content[i].Status = status
		}
	}
	return msg
}

// throttle limits how often one message is sent while it changes. A
// message has one from its first snapshot to its final state.
type throttle struct {
	last time.Time
	// pending builds the latest state, not sent yet.
	pending func() (transcript.RichMessage, bool)
	timer   *quartz.Timer
}

func (t *throttle) stop() {
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
}

// emitThrottled sends message id, which is still changing, at most once
// per interval; its latest state is sent when the interval ends. build
// makes the message when it is sent, so a fast stream isn't copied for
// every change; it returns false while there is nothing to show.
func (a *Adapter) emitThrottled(id string, build func() (transcript.RichMessage, bool)) {
	t := a.loop.throttles[id]
	if t == nil {
		// The first snapshot goes out at once.
		t = &throttle{}
		if a.sendThrottled(t, build) {
			a.loop.throttles[id] = t
		}
		return
	}
	if t.timer != nil {
		t.pending = build
		return
	}
	wait := a.interval - a.clock.Since(t.last)
	if wait <= 0 {
		a.sendThrottled(t, build)
		return
	}
	t.pending = build
	t.timer = a.clock.AfterFunc(wait, func() {
		a.post(func() { a.flushThrottle(id) })
	})
}

// sendThrottled sends what build makes, if anything, and reports whether
// it did.
func (a *Adapter) sendThrottled(t *throttle, build func() (transcript.RichMessage, bool)) bool {
	t.pending = nil
	msg, ok := build()
	if ok {
		t.last = a.clock.Now()
		a.loop.sink.RichMessage(msg)
	}
	return ok
}

// sent reports whether a snapshot of message id was sent and its final
// state wasn't.
func (a *Adapter) sent(id string) bool {
	return a.loop.throttles[id] != nil
}

// flushThrottle sends the state of message id that waited for the end of
// the interval.
func (a *Adapter) flushThrottle(id string) {
	t := a.loop.throttles[id]
	if t == nil {
		return
	}
	t.timer = nil
	if t.pending != nil {
		a.sendThrottled(t, t.pending)
	}
}

// emitFinal sends the final state of a message, dropping any snapshot
// still waiting.
func (a *Adapter) emitFinal(msg transcript.RichMessage) {
	if t := a.loop.throttles[msg.MessageID]; t != nil {
		t.stop()
		delete(a.loop.throttles, msg.MessageID)
	}
	a.loop.sink.RichMessage(msg)
}

// flushStream sends the last state of a message that was streaming when
// its run ended without message_end, or Pi exited.
func (a *Adapter) flushStream() {
	s := a.loop.stream
	if s == nil {
		return
	}
	a.loop.stream = nil
	if a.sent(s.id) {
		a.emitFinal(s.snapshot())
	}
}
