// Command fakeclaude plays Claude Code for the native transport's end-to-end
// tests: `auth status --json` reports a login, and the stream-json mode
// (-p --input-format stream-json) answers the control requests AgentAPI
// sends and each user message with one turn, "Echo: <message>", in the
// frames a real CLI writes (see lib/native/claude/testdata).
package main

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const version = "2.1.299"

func main() {
	args := os.Args[1:]
	switch {
	case len(args) >= 2 && args[0] == "auth" && args[1] == "status":
		fmt.Println(`{"loggedIn":true,"authMethod":"claude.ai"}`)
		return
	case len(args) == 1 && (args[0] == "--version" || args[0] == "-v"):
		fmt.Printf("%s (Claude Code)\n", version)
		return
	}
	if !contains(args, "-p") {
		fmt.Fprintln(os.Stderr, "fakeclaude only speaks stream-json (-p)")
		os.Exit(2)
	}
	session := flagValue(args, "--session-id")
	if resume := flagValue(args, "--resume"); resume != "" && !contains(args, "--fork-session") {
		session = resume
	}
	if session == "" {
		session = newID()
	}
	if err := serve(session); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// serve reads AgentAPI's frames from stdin until it closes.
func serve(session string) error {
	out := json.NewEncoder(os.Stdout)
	cwd, _ := os.Getwd()
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for in.Scan() {
		var msg map[string]any
		if json.Unmarshal(in.Bytes(), &msg) != nil {
			continue
		}
		switch msg["type"] {
		case "control_request":
			request, _ := msg["request"].(map[string]any)
			if err := out.Encode(controlResponse(msg["request_id"], request["subtype"])); err != nil {
				return err
			}
		case "user":
			for _, frame := range turn(session, cwd, userText(msg)) {
				if err := out.Encode(frame); err != nil {
					return err
				}
			}
		}
	}
	return in.Err()
}

// controlResponse answers a control request the way Claude Code does, or
// with an error for one it isn't asked in these tests.
func controlResponse(id, subtype any) map[string]any {
	var response map[string]any
	switch subtype {
	case "initialize":
		response = map[string]any{"claude_code_version": version, "current_permission_mode": "default", "session_state": "idle"}
	case "get_settings":
		response = map[string]any{"effective": map[string]any{}, "applied": map[string]any{}}
	case "generate_session_title":
		response = map[string]any{"title": "Fake session"}
	case "interrupt", "set_permission_mode", "set_model", "set_max_thinking_tokens", "apply_flag_settings":
		response = map[string]any{}
	default:
		return map[string]any{"type": "control_response", "response": map[string]any{
			"subtype": "error", "request_id": id, "error": fmt.Sprintf("fakeclaude doesn't answer %v", subtype),
		}}
	}
	return map[string]any{"type": "control_response", "response": map[string]any{
		"subtype": "success", "request_id": id, "response": response,
	}}
}

// turn is the output of one turn that answers text.
func turn(session, cwd, text string) []map[string]any {
	reply := "Echo: " + text
	messageID := "msg_" + strings.ReplaceAll(newID(), "-", "")
	usage := map[string]any{"input_tokens": 2, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0, "output_tokens": 5}
	return []map[string]any{
		{"type": "system", "subtype": "init", "cwd": cwd, "session_id": session, "model": "claude-haiku-5-5", "permissionMode": "default", "claude_code_version": version, "uuid": newID()},
		{"type": "assistant", "message": map[string]any{
			"model": "claude-haiku-5-5", "id": messageID, "type": "message", "role": "assistant",
			"content":     []map[string]any{{"type": "text", "text": reply}},
			"stop_reason": "end_turn", "usage": usage,
		}, "parent_tool_use_id": nil, "session_id": session, "uuid": newID()},
		{
			"type": "result", "subtype": "success", "is_error": false, "duration_ms": 10, "duration_api_ms": 10, "num_turns": 1,
			"result": reply, "stop_reason": "end_turn", "session_id": session, "total_cost_usd": 0.0001, "usage": usage, "uuid": newID(),
		},
	}
}

// userText is the text of a user frame, whose content is a string or a
// list of blocks.
func userText(msg map[string]any) string {
	message, _ := msg["message"].(map[string]any)
	switch content := message["content"].(type) {
	case string:
		return content
	case []any:
		var parts []string
		for _, block := range content {
			if b, ok := block.(map[string]any); ok && b["type"] == "text" {
				text, _ := b["text"].(string)
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func contains(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// flagValue is the value of --name=value or --name value.
func flagValue(args []string, name string) string {
	for i, a := range args {
		if v, ok := strings.CutPrefix(a, name+"="); ok {
			return v
		}
		if a == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
