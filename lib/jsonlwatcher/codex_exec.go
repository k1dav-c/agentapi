package jsonlwatcher

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Codex's code mode runs tools through one freeform "exec" tool whose input
// is a JavaScript snippet, e.g.
//
//	text(await tools.exec_command({cmd:"uname -s",max_output_tokens:1000}));
//
// and whose output is a "Script completed / Wall time / Output:" header
// followed by one JSON chunk per tool call:
//
//	{"chunk_id":"7b5f9c","wall_time_seconds":0.00002,"exit_code":0,"output":"Linux\n"}
//
// Shown as is, a command reads as JavaScript and its output as the header,
// with the real output cut off below it. These helpers recover the shell
// commands and their output so Codex tool calls look like everyone else's.

// codexExecCommandRe matches the cmd of a tools.exec_command({...}) call.
// The value is a JSON-style double-quoted string or a single-quoted one.
var codexExecCommandRe = regexp.MustCompile(`tools\.exec_command\(\s*\{[^{}]*?\bcmd\s*:\s*("(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*')`)

// codexExecToolCallRe matches any tools.<name>( call in a script.
var codexExecToolCallRe = regexp.MustCompile(`tools\.([A-Za-z_][A-Za-z0-9_]*)\(`)

// codexExecCommands returns the shell commands an exec script runs, or nil
// when the script calls anything other than exec_command (or nothing), in
// which case the script itself is the clearest thing to show.
func codexExecCommands(script string) []string {
	for _, call := range codexExecToolCallRe.FindAllStringSubmatch(script, -1) {
		if call[1] != "exec_command" {
			return nil
		}
	}
	var commands []string
	for _, match := range codexExecCommandRe.FindAllStringSubmatch(script, -1) {
		if command, ok := unquoteJSString(match[1]); ok && strings.TrimSpace(command) != "" {
			commands = append(commands, command)
		}
	}
	return commands
}

func unquoteJSString(quoted string) (string, bool) {
	if strings.HasPrefix(quoted, "'") {
		// Re-quote as JSON: escaped single quotes become plain ones, bare
		// double quotes get escaped.
		inner := quoted[1 : len(quoted)-1]
		inner = strings.ReplaceAll(inner, `\'`, `'`)
		inner = strings.ReplaceAll(inner, `"`, `\"`)
		quoted = `"` + inner + `"`
	}
	var s string
	if err := json.Unmarshal([]byte(quoted), &s); err != nil {
		return "", false
	}
	return s, true
}

// codexExecInput rewrites the input of a code-mode exec call into the
// {cmd} shape of a command tool, keeping the script. It returns the input
// unchanged when the script isn't just shell commands.
func codexExecInput(name string, input json.RawMessage) json.RawMessage {
	if name != "exec" {
		return input
	}
	var script string
	if err := json.Unmarshal(input, &script); err != nil {
		return input
	}
	commands := codexExecCommands(script)
	if len(commands) == 0 {
		return input
	}
	rewritten, err := json.Marshal(map[string]string{
		"cmd":    strings.Join(commands, "\n"),
		"script": script,
	})
	if err != nil {
		return input
	}
	return rewritten
}

// codexExecChunk is one tool call's result in a code-mode exec output.
type codexExecChunk struct {
	ChunkID  string  `json:"chunk_id"`
	ExitCode *int    `json:"exit_code"`
	Output   *string `json:"output"`
}

var codexExecHeaderRe = regexp.MustCompile(`^Script (completed|failed)\s*\nWall time [^\n]*\n(Output:\n?)?`)

// codexExecOutput extracts the command output from a code-mode exec
// result: the text of its JSON chunks without the script header, and the
// first non-zero exit code. ok is false when text isn't such a result.
func codexExecOutput(text string) (output string, exitCode int, ok bool) {
	header := codexExecHeaderRe.FindString(text)
	if header == "" {
		return "", 0, false
	}
	rest := text[len(header):]
	var outputs []string
	for _, line := range strings.Split(rest, "\n") {
		trimmed := strings.TrimSpace(line)
		var chunk codexExecChunk
		if !strings.HasPrefix(trimmed, "{") || json.Unmarshal([]byte(trimmed), &chunk) != nil || chunk.Output == nil {
			if trimmed != "" {
				outputs = append(outputs, line)
			}
			continue
		}
		outputs = append(outputs, strings.TrimRight(*chunk.Output, "\n"))
		if chunk.ExitCode != nil && *chunk.ExitCode != 0 && exitCode == 0 {
			exitCode = *chunk.ExitCode
		}
	}
	output = strings.Join(outputs, "\n")
	if exitCode != 0 {
		output = strings.TrimLeft(fmt.Sprintf("%s\nExit code: %d", output, exitCode), "\n")
	}
	return output, exitCode, true
}
