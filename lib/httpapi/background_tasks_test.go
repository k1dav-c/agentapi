package httpapi

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/agentapi/lib/jsonlwatcher"
	"github.com/stretchr/testify/require"
)

func TestDiscoverBackgroundTasks(t *testing.T) {
	t.Parallel()
	now := time.Now()
	isError := false
	tests := []struct {
		name      string
		agentType string
		toolName  string
		input     string
		result    string
		wantID    string
		wantPath  string
	}{
		{
			name: "claude bash", agentType: "claude", toolName: "Bash",
			input:    `{"command":"make test","description":"Run tests","run_in_background":true}`,
			result:   "Background task ID: task-42\nOutput: /tmp/claude/tasks/task-42.output",
			wantID:   "task-42",
			wantPath: "/tmp/claude/tasks/task-42.output",
		},
		{
			name: "codex exec", agentType: "codex", toolName: "exec_command",
			input:    `{"cmd":"go test ./..."}`,
			result:   "Process running with session ID 9912",
			wantID:   "9912",
			wantPath: "",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			messages := []jsonlwatcher.RichMessage{
				{
					MessageID: "assistant", Role: "assistant", Timestamp: now,
					Content: []jsonlwatcher.RichContentBlock{{
						Type: "tool_use", ToolUseID: "call-1", ToolName: test.toolName,
						ToolInput: json.RawMessage(test.input), Status: "running",
					}},
				},
				{
					MessageID: "result", Role: "user", Timestamp: now.Add(time.Second),
					Content: []jsonlwatcher.RichContentBlock{{
						Type: "tool_result", ToolUseID: "call-1", Text: test.result,
						Status: "completed", IsError: &isError,
					}},
				},
			}
			tasks := discoverBackgroundTasks(messages, test.agentType)
			require.Len(t, tasks, 1)
			require.Equal(t, test.wantID, tasks[0].ID)
			require.Equal(t, test.wantPath, tasks[0].OutputPath)
			require.Equal(t, "running", tasks[0].Status)
		})
	}
}

func TestDiscoverBackgroundTaskFollowup(t *testing.T) {
	t.Parallel()
	now := time.Now()
	isError := false
	messages := []jsonlwatcher.RichMessage{
		{
			MessageID: "assistant-start", Role: "assistant", Timestamp: now,
			Content: []jsonlwatcher.RichContentBlock{{
				Type: "tool_use", ToolUseID: "call-start", ToolName: "exec_command",
				ToolInput: json.RawMessage(`{"cmd":"go test ./..."}`),
			}},
		},
		{
			MessageID: "result-start", Role: "user", Timestamp: now.Add(time.Second),
			Content: []jsonlwatcher.RichContentBlock{{
				Type: "tool_result", ToolUseID: "call-start",
				Text: "Process running with session ID 9912", IsError: &isError,
			}},
		},
		{
			MessageID: "assistant-followup", Role: "assistant", Timestamp: now.Add(2 * time.Second),
			Content: []jsonlwatcher.RichContentBlock{{
				Type: "tool_use", ToolUseID: "call-followup", ToolName: "write_stdin",
				ToolInput: json.RawMessage(`{"session_id":9912}`),
			}},
		},
		{
			MessageID: "result-followup", Role: "user", Timestamp: now.Add(3 * time.Second),
			Content: []jsonlwatcher.RichContentBlock{{
				Type: "tool_result", ToolUseID: "call-followup",
				Text: "ok github.com/coder/agentapi\nProcess exited with code 0", IsError: &isError,
			}},
		},
	}

	tasks := discoverBackgroundTasks(messages, "codex")
	require.Len(t, tasks, 1)
	require.Equal(t, "completed", tasks[0].Status)
	require.Contains(t, tasks[0].Output, "ok github.com/coder/agentapi")
	require.Equal(t, now.Add(3*time.Second), tasks[0].UpdatedAt)
}
