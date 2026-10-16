package codex

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/coder/agentapi/lib/jsonlwatcher"
)

// The adapter shapes its rich messages like jsonlwatcher.CodexParser does
// for the rollout file of the same activity, because the chat UI renders
// that shape:
//
//   - one assistant message per turn whose blocks (text, thinking, tool_use)
//     grow as items arrive, re-sent whole under the same MessageID;
//   - a "result-<toolUseID>" user message per tool result;
//   - a user message per user input.
//
// One difference: a message steered into a running turn starts a new
// assistant message (MessageID "<turnID>-<n>"), so the chat files the
// activity after the steer under the steering message.

// msgState is a rich message being built, sent at most once per snapshot
// interval while it streams.
type msgState struct {
	build    func() (jsonlwatcher.RichMessage, bool)
	dirty    bool
	lastEmit time.Time
}

// block is one content block of an assistant message.
type block struct {
	jsonlwatcher.RichContentBlock
	// parts hold reasoning summary and content texts, joined into
	// Thinking.
	summary []string
	content []string
}

// segment is one assistant message of a turn.
type segment struct {
	msgState
	id        string
	timestamp time.Time
	blocks    []*block
	usage     *jsonlwatcher.Usage
	stop      string
}

// itemState is an item (tool call, message, reasoning) of a turn.
type itemState struct {
	id   string
	kind string // item type
	seg  *segment
	blk  *block
	done bool
	// result is the tool result message, once there is output.
	result *msgState
	// resultBlock is the current tool result.
	resultBlock jsonlwatcher.RichContentBlock
	output      strings.Builder
	startedAt   time.Time
	// changes of a fileChange and server of an mcpToolCall, for their
	// approval requests.
	changes []fileUpdateChange
	server  string
}

// turnState is the turn app-server is working on.
type turnState struct {
	id          string
	segments    []*segment
	items       map[string]*itemState
	order       []*itemState
	promptSeen  bool
	interrupted time.Time
	// failure is the error of a failed turn, from the error notification.
	failure string
	// loginReported: the turn failed for want of a login, and the sink
	// was told.
	loginReported bool
}

func (t *turnState) current() *segment { return t.segments[len(t.segments)-1] }

func (s *segment) snapshot(model string) (jsonlwatcher.RichMessage, bool) {
	var content []jsonlwatcher.RichContentBlock
	for _, b := range s.blocks {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) == "" {
				continue
			}
		case "thinking":
			if b.Thinking == "" {
				continue
			}
		}
		content = append(content, b.RichContentBlock)
	}
	if len(content) == 0 {
		return jsonlwatcher.RichMessage{}, false
	}
	return jsonlwatcher.RichMessage{
		MessageID:  s.id,
		Role:       "assistant",
		Content:    content,
		Model:      model,
		StopReason: s.stop,
		Usage:      s.usage,
		Timestamp:  s.timestamp,
	}, true
}

// joinReasoning joins reasoning texts like codexParser does for a rollout
// reasoning record: trimmed, blank ones dropped, separated by a blank line.
func joinReasoning(summary, content []string) string {
	var parts []string
	for _, text := range append(append([]string(nil), summary...), content...) {
		if text = strings.TrimSpace(text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// commandDisplay is the command to show for a commandExecution: the inner
// command of Codex's `bash -lc` wrapper.
func commandDisplay(command string, actions []commandAction) string {
	var commands []string
	for _, a := range actions {
		if strings.TrimSpace(a.Command) != "" {
			commands = append(commands, a.Command)
		}
	}
	if len(commands) > 0 {
		return strings.Join(commands, "\n")
	}
	return unwrapShell(command)
}

// unwrapShell returns the script of a `<shell> -lc '<script>'` command line.
func unwrapShell(command string) string {
	for _, flag := range []string{" -lc ", " -c "} {
		_, script, ok := strings.Cut(command, flag)
		if !ok {
			continue
		}
		script = strings.TrimSpace(script)
		if len(script) >= 2 && script[0] == '\'' && script[len(script)-1] == '\'' {
			return strings.ReplaceAll(script[1:len(script)-1], `'\''`, `'`)
		}
		if len(script) >= 2 && script[0] == '"' && script[len(script)-1] == '"' {
			return script[1 : len(script)-1]
		}
		return script
	}
	return command
}

func mustJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return data
}

// toolUse returns the tool name and input the chat shows for a tool item,
// or ok false for items that are not tool calls.
func toolUse(it *threadItem) (name string, input json.RawMessage, ok bool) {
	switch it.Type {
	case "commandExecution":
		in := map[string]string{"cmd": commandDisplay(it.Command, it.CommandActions)}
		if it.CWD != "" {
			in["workdir"] = it.CWD
		}
		return "exec_command", mustJSON(in), true
	case "fileChange":
		return "apply_patch", fileChangeInput(it.Changes), true
	case "mcpToolCall":
		return "mcp__" + it.Server + "__" + it.Tool, rawOrEmpty(it.Arguments), true
	case "dynamicToolCall":
		name := it.Tool
		if it.Namespace != nil && *it.Namespace != "" {
			name = *it.Namespace + "__" + it.Tool
		}
		return name, rawOrEmpty(it.Arguments), true
	case "webSearch":
		in := map[string]any{"query": it.Query}
		if len(it.Action) > 0 && string(it.Action) != "null" {
			in["action"] = it.Action
		}
		return "web_search", mustJSON(in), true
	case "imageView":
		return "view_image", mustJSON(map[string]string{"path": it.Path}), true
	}
	return "", nil, false
}

func rawOrEmpty(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage("{}")
	}
	return raw
}

// fileChangeInput is the tool input of a fileChange: the changes, and the
// file's path when there is one, so the chat names it.
func fileChangeInput(changes []fileUpdateChange) json.RawMessage {
	type change struct {
		Path     string `json:"path"`
		Kind     string `json:"kind"`
		MovePath string `json:"move_path,omitempty"`
		Diff     string `json:"diff"`
	}
	in := struct {
		FilePath string   `json:"file_path,omitempty"`
		Changes  []change `json:"changes"`
	}{Changes: []change{}}
	for _, c := range changes {
		ch := change{Path: c.Path, Kind: c.Kind.Type, Diff: c.Diff}
		if c.Kind.MovePath != nil {
			ch.MovePath = *c.Kind.MovePath
		}
		in.Changes = append(in.Changes, ch)
	}
	if len(changes) == 1 {
		in.FilePath = changes[0].Path
	}
	return mustJSON(in)
}

// fileChangeDetail renders changes as a unified diff for an approval.
func fileChangeDetail(changes []fileUpdateChange) string {
	var sb strings.Builder
	for _, c := range changes {
		from, to := c.Path, c.Path
		switch c.Kind.Type {
		case "add":
			from = "/dev/null"
		case "delete":
			to = "/dev/null"
		}
		if c.Kind.MovePath != nil && *c.Kind.MovePath != "" {
			to = *c.Kind.MovePath
		}
		fmt.Fprintf(&sb, "--- %s\n+++ %s\n%s", from, to, c.Diff)
		if !strings.HasSuffix(c.Diff, "\n") {
			sb.WriteString("\n")
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// toolResult returns the result block of a completed tool item.
func toolResult(it *threadItem) jsonlwatcher.RichContentBlock {
	text, status := "", "completed"
	var images []jsonlwatcher.RichImage
	switch it.Type {
	case "commandExecution":
		if it.AggregatedOutput != nil {
			text = strings.TrimRight(*it.AggregatedOutput, "\n")
		}
		switch it.Status {
		case "declined":
			status = "failed"
			if text == "" {
				text = "Command declined by the user"
			}
		case "failed":
			status = "failed"
		}
		if it.ExitCode != nil && *it.ExitCode != 0 {
			// The same suffix codexParser writes; the chat reads the exit
			// code from it.
			status = "failed"
			text = strings.TrimLeft(fmt.Sprintf("%s\nExit code: %d", text, *it.ExitCode), "\n")
		}
	case "fileChange":
		switch it.Status {
		case "declined":
			status, text = "failed", "Patch declined by the user"
		case "failed":
			status, text = "failed", "Patch failed"
		default:
			text = patchSummary(it.Changes)
		}
	case "mcpToolCall":
		if it.Result != nil {
			text, images = mcpContent(it.Result.Content)
		}
		if it.Status == "failed" {
			status = "failed"
			if it.Error != nil && it.Error.Message != "" {
				text = it.Error.Message
			}
		}
	case "dynamicToolCall":
		text, images = mcpContent(it.ContentItems)
		if it.Status == "failed" || (it.Success != nil && !*it.Success) {
			status = "failed"
		}
	case "webSearch":
		text = webSearchSummary(it)
	case "imageView":
		if img, ok := readImage(it.Path); ok {
			images = append(images, img)
		} else {
			status, text = "failed", "Could not read "+it.Path
		}
	}
	isError := status == "failed"
	return jsonlwatcher.RichContentBlock{
		Type:      "tool_result",
		ToolUseID: it.ID,
		Text:      text,
		Status:    status,
		IsError:   &isError,
		Images:    images,
	}
}

// patchSummary is apply_patch's own success output.
func patchSummary(changes []fileUpdateChange) string {
	lines := []string{"Success. Updated the following files:"}
	for _, c := range changes {
		switch c.Kind.Type {
		case "add":
			lines = append(lines, "A "+c.Path)
		case "delete":
			lines = append(lines, "D "+c.Path)
		default:
			path := c.Path
			if c.Kind.MovePath != nil && *c.Kind.MovePath != "" {
				path = *c.Kind.MovePath
			}
			lines = append(lines, "M "+path)
		}
	}
	return strings.Join(lines, "\n")
}

func webSearchSummary(it *threadItem) string {
	var action struct {
		Type    string   `json:"type"`
		Query   string   `json:"query"`
		Queries []string `json:"queries"`
		URL     string   `json:"url"`
		Pattern string   `json:"pattern"`
	}
	_ = json.Unmarshal(it.Action, &action)
	switch action.Type {
	case "openPage":
		return "Opened " + action.URL
	case "findInPage":
		return fmt.Sprintf("Searched %s for %q", action.URL, action.Pattern)
	}
	queries := action.Queries
	if len(queries) == 0 && action.Query != "" {
		queries = []string{action.Query}
	}
	if len(queries) == 0 && it.Query != "" {
		queries = []string{it.Query}
	}
	return "Searched the web: " + strings.Join(queries, "; ")
}

// mcpContent joins the text of MCP content blocks and decodes their
// images.
func mcpContent(blocks []json.RawMessage) (string, []jsonlwatcher.RichImage) {
	var texts []string
	var images []jsonlwatcher.RichImage
	for _, raw := range blocks {
		var b struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Data     string `json:"data"`
			MimeType string `json:"mimeType"`
			// Dynamic tools use inputText / inputImage with a data URL.
			ImageURL string `json:"imageUrl"`
		}
		if json.Unmarshal(raw, &b) != nil {
			continue
		}
		switch b.Type {
		case "text", "inputText":
			if b.Text != "" {
				texts = append(texts, b.Text)
			}
		case "image":
			if data, err := base64.StdEncoding.DecodeString(b.Data); err == nil && len(data) > 0 {
				images = append(images, jsonlwatcher.RichImage{MediaType: b.MimeType, Data: data})
			}
		case "inputImage":
			header, data, ok := strings.Cut(strings.TrimPrefix(b.ImageURL, "data:"), ",")
			mediaType, isBase64 := strings.CutSuffix(header, ";base64")
			if !ok || !isBase64 {
				continue
			}
			if decoded, err := base64.StdEncoding.DecodeString(data); err == nil && len(decoded) > 0 {
				images = append(images, jsonlwatcher.RichImage{MediaType: mediaType, Data: decoded})
			}
		}
	}
	return strings.Join(texts, "\n"), images
}

// maxImageBytes bounds images read from disk for imageView items.
const maxImageBytes = 10 << 20

func readImage(path string) (jsonlwatcher.RichImage, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxImageBytes {
		return jsonlwatcher.RichImage{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return jsonlwatcher.RichImage{}, false
	}
	mediaType := http.DetectContentType(data)
	if !strings.HasPrefix(mediaType, "image/") {
		return jsonlwatcher.RichImage{}, false
	}
	return jsonlwatcher.RichImage{MediaType: mediaType, Data: data}, true
}

// absPath resolves path against dir.
func absPath(path, dir string) string {
	if filepath.IsAbs(path) || dir == "" {
		return path
	}
	return filepath.Join(dir, path)
}

func msTime(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

func strPtr(s string) *string { return &s }

func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
