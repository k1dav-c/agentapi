package pi

import (
	"encoding/base64"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/coder/agentapi/lib/jsonlwatcher"
)

// streamMessage is an assistant message between message_start and
// message_end, built from message_update deltas.
type streamMessage struct {
	id        string
	timestamp int64
	model     string
	blocks    map[int]*streamBlock
	// emitted: a snapshot was sent.
	emitted bool
}

type streamBlock struct {
	kind     string // "text", "thinking" or "toolCall"
	text     string
	toolID   string
	toolName string
	args     strings.Builder
	input    json.RawMessage
}

// snapshot is the message as streamed so far, in the shape the Pi parser
// gives the finished message.
func (s *streamMessage) snapshot() jsonlwatcher.RichMessage {
	msg := jsonlwatcher.RichMessage{
		MessageID: s.id,
		Role:      "assistant",
		Model:     s.model,
		Timestamp: msTime(s.timestamp),
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
			if strings.TrimSpace(b.text) != "" {
				msg.Content = append(msg.Content, jsonlwatcher.RichContentBlock{Type: "text", Text: b.text})
			}
		case "thinking":
			if strings.TrimSpace(b.text) != "" {
				msg.Content = append(msg.Content, jsonlwatcher.RichContentBlock{Type: "thinking", Thinking: b.text})
			}
		case "toolCall":
			block := jsonlwatcher.RichContentBlock{Type: "tool_use", ToolUseID: b.toolID, ToolName: b.toolName}
			if len(b.input) > 0 {
				block.ToolInput = b.input
			} else if raw := b.args.String(); json.Valid([]byte(raw)) {
				block.ToolInput = json.RawMessage(raw)
			}
			msg.Content = append(msg.Content, block)
		}
	}
	return msg
}

func (s *streamMessage) block(i int, kind string) *streamBlock {
	b := s.blocks[i]
	if b == nil || b.kind != kind {
		b = &streamBlock{kind: kind}
		s.blocks[i] = b
	}
	return b
}

// apply adds a message_update event to the message.
func (s *streamMessage) apply(ev *streamEvent) bool {
	switch ev.Type {
	case "text_start":
		s.block(ev.ContentIndex, "text")
	case "text_delta":
		s.block(ev.ContentIndex, "text").text += ev.Delta
	case "text_end":
		s.block(ev.ContentIndex, "text").text = ev.Content
	case "thinking_start":
		s.block(ev.ContentIndex, "thinking")
	case "thinking_delta":
		s.block(ev.ContentIndex, "thinking").text += ev.Delta
	case "thinking_end":
		// The summary deltas can differ from the final text.
		s.block(ev.ContentIndex, "thinking").text = ev.Content
	case "toolcall_start":
		b := s.block(ev.ContentIndex, "toolCall")
		b.toolID, b.toolName = ev.ID, ev.ToolName
	case "toolcall_delta":
		s.block(ev.ContentIndex, "toolCall").args.WriteString(ev.Delta)
	case "toolcall_end":
		b := s.block(ev.ContentIndex, "toolCall")
		if ev.ToolCall != nil {
			b.toolID, b.toolName, b.input = ev.ToolCall.ID, ev.ToolCall.Name, ev.ToolCall.Arguments
		}
	default:
		return false
	}
	return true
}

func msTime(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

func (a *Adapter) handleMessageEvent(kind string, ev messageEvent) {
	var msg agentMessage
	if len(ev.Message) > 0 {
		if err := json.Unmarshal(ev.Message, &msg); err != nil {
			a.logger.Warn("Malformed Pi message", "error", err)
			return
		}
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
		if snap := s.snapshot(); len(snap.Content) > 0 {
			s.emitted = true
			a.emitThrottled(snap)
		}
	case "message_end":
		a.handleMessageEnd(msg, ev.Message)
	}
}

// handleMessageEnd emits the finished message. message_end carries the
// message as Pi writes it to its session file, so jsonlwatcher's Pi parser
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
			id, streamed = s.id, s.emitted
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
	case "toolResult":
		id = messageID(msg)
		streamed = a.loop.throttles[id] != nil
	default:
		id = a.loop.uniqueID(messageID(msg))
	}

	line, err := json.Marshal(struct {
		Type    string          `json:"type"`
		ID      string          `json:"id"`
		Message json.RawMessage `json:"message"`
	}{"message", id, raw})
	if err != nil {
		return
	}
	rich, err := a.loop.parser.ParseLine(line)
	if err != nil {
		a.logger.Warn("Failed to parse a Pi message", "error", err)
		return
	}
	if msg.Role == "toolResult" {
		a.addToolResultDetails(rich, msg)
	}
	if len(rich) == 0 && streamed {
		// Replace what was streamed, e.g. whitespace-only text.
		rich = []jsonlwatcher.RichMessage{{MessageID: id, Role: roleOf(msg), Content: []jsonlwatcher.RichContentBlock{}, Timestamp: msTime(msg.Timestamp)}}
	}
	for _, m := range rich {
		a.emitFinal(m)
	}
	if events, err := a.loop.events.ParseSessionEvents(line); err == nil && len(events) > 0 {
		a.loop.sink.SessionEvents(events)
	}
}

func roleOf(msg agentMessage) string {
	if msg.Role == "assistant" {
		return "assistant"
	}
	return "user"
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

// addToolResultDetails adds what the Pi parser leaves out of a tool
// result: images (an image file the agent read) and a bash exit code.
func (a *Adapter) addToolResultDetails(rich []jsonlwatcher.RichMessage, msg agentMessage) {
	images := contentImages(msg.Content)
	code, hasCode := a.loop.exitCodes[msg.ToolCallID]
	delete(a.loop.exitCodes, msg.ToolCallID)
	for i := range rich {
		for j := range rich[i].Content {
			block := &rich[i].Content[j]
			if block.Type != "tool_result" {
				continue
			}
			block.Images = images
			if hasCode && code != 0 && block.Status != "failed" {
				isError := true
				block.Status, block.IsError = "failed", &isError
			}
		}
	}
}

// contentImages decodes the images of a Pi content array.
func contentImages(raw json.RawMessage) []jsonlwatcher.RichImage {
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil
	}
	var images []jsonlwatcher.RichImage
	for _, b := range blocks {
		if b.Type != "image" || b.Data == "" {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(b.Data)
		if err != nil {
			continue
		}
		images = append(images, jsonlwatcher.RichImage{MediaType: b.MimeType, Data: data})
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
		a.emitThrottled(a.toolResultMessage(ev.ToolCallID, ev.ToolName, ev.PartialResult.Content, false, "running"))
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
		if sc := ev.Result.StructuredContent; sc != nil && sc.ExitCode != nil {
			a.loop.exitCodes[ev.ToolCallID] = *sc.ExitCode
		}
		// The tool result message follows; show the result already.
		a.emitThrottled(a.toolResultMessage(ev.ToolCallID, ev.ToolName, ev.Result.Content, ev.IsError || ev.Result.IsError, ""))
	}
}

// toolResultMessage builds a tool result like the Pi parser does for the
// toolResult message, with status overridden while the tool runs.
func (a *Adapter) toolResultMessage(callID, toolName string, content json.RawMessage, isError bool, status string) jsonlwatcher.RichMessage {
	if len(content) == 0 {
		content = json.RawMessage(`[]`)
	}
	raw, _ := json.Marshal(map[string]any{
		"role": "toolResult", "toolCallId": callID, "toolName": toolName,
		"content": content, "isError": isError, "timestamp": time.Now().UnixMilli(),
	})
	line, _ := json.Marshal(map[string]any{"type": "message", "id": "result-" + callID, "message": json.RawMessage(raw)})
	rich, _ := a.loop.parser.ParseLine(line)
	if len(rich) == 0 {
		return jsonlwatcher.RichMessage{MessageID: "result-" + callID, Role: "user"}
	}
	msg := rich[0]
	for i := range msg.Content {
		msg.Content[i].Images = contentImages(content)
		if status != "" {
			msg.Content[i].Status = status
		}
	}
	return msg
}

// throttle limits how often one message is sent while it changes.
type throttle struct {
	last    time.Time
	pending *jsonlwatcher.RichMessage
	timer   *time.Timer
}

func (t *throttle) stop() {
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
}

// emitThrottled sends a snapshot of a message that is still changing, at
// most once per interval; the latest snapshot is sent when the interval
// ends.
func (a *Adapter) emitThrottled(msg jsonlwatcher.RichMessage) {
	t := a.loop.throttles[msg.MessageID]
	if t == nil {
		t = &throttle{}
		a.loop.throttles[msg.MessageID] = t
	}
	now := time.Now()
	if t.timer == nil && now.Sub(t.last) >= a.interval {
		t.last = now
		a.loop.sink.RichMessage(msg)
		return
	}
	t.pending = &msg
	if t.timer == nil {
		id := msg.MessageID
		t.timer = time.AfterFunc(a.interval-now.Sub(t.last), func() {
			a.post(func() { a.flushThrottle(id) })
		})
	}
}

func (a *Adapter) flushThrottle(id string) {
	t := a.loop.throttles[id]
	if t == nil {
		return
	}
	t.timer = nil
	if t.pending != nil {
		t.last = time.Now()
		a.loop.sink.RichMessage(*t.pending)
		t.pending = nil
	}
}

// emitFinal sends the final state of a message, dropping any snapshot
// still waiting.
func (a *Adapter) emitFinal(msg jsonlwatcher.RichMessage) {
	if t := a.loop.throttles[msg.MessageID]; t != nil {
		t.stop()
		delete(a.loop.throttles, msg.MessageID)
	}
	a.loop.sink.RichMessage(msg)
}

// flushStream sends the last snapshot of a message that was streaming
// when its run ended without message_end.
func (a *Adapter) flushStream() {
	s := a.loop.stream
	if s == nil {
		return
	}
	a.loop.stream = nil
	if s.emitted {
		a.emitFinal(s.snapshot())
	}
}
