package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/coder/agentapi/lib/jsonlwatcher"
	"github.com/danielgtaylor/huma/v2"
)

var (
	backgroundIDPattern = regexp.MustCompile(`(?i)(?:task|shell|session|cell)(?:\s+id)?["':=\s]+([a-z0-9][a-z0-9_-]*)`)
	outputPathPattern   = regexp.MustCompile(`(?:^|[\s"'=:])(/[^\s"']+\.(?:out|output))(?:\s|$)`)
	redirectPathPattern = regexp.MustCompile(`(?:^|[\s])>{1,2}\s*([^\s"';&]+\.(?:out|output))(?:\s|$)`)
)

type discoveredTool struct {
	name      string
	input     map[string]any
	result    string
	status    string
	isError   bool
	timestamp time.Time
	updatedAt time.Time
}

func (s *Server) getBackgroundTasks(ctx context.Context, input *struct{}) (*BackgroundTasksResponse, error) {
	resp := &BackgroundTasksResponse{}
	resp.Body.Tasks = s.backgroundTasks()
	return resp, nil
}

func (s *Server) getBackgroundTaskOutput(ctx context.Context, input *BackgroundTaskOutputRequest) (*BackgroundTaskOutputResponse, error) {
	tasks := s.backgroundTasks()
	var task *BackgroundTask
	for i := range tasks {
		if tasks[i].ID == input.ID {
			task = &tasks[i]
			break
		}
	}
	if task == nil {
		return nil, huma.Error404NotFound("background task output was not found")
	}

	if task.OutputPath == "" {
		content := []byte(task.Output)
		tail := input.Tail
		if tail <= 0 {
			tail = 128 * 1024
		}
		start := max(0, len(content)-tail)
		resp := &BackgroundTaskOutputResponse{}
		resp.Body.TaskID = task.ID
		resp.Body.Content = string(content[start:])
		resp.Body.Size = int64(len(content))
		resp.Body.Truncated = start > 0
		return resp, nil
	}

	path, err := s.safeBackgroundOutputPath(task.OutputPath)
	if err != nil {
		return nil, huma.Error403Forbidden("background task output path is not readable")
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, huma.Error404NotFound("background task output file does not exist")
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to open background task output")
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to inspect background task output")
	}
	tail := int64(input.Tail)
	if tail <= 0 {
		tail = 128 * 1024
	}
	start := max(int64(0), info.Size()-tail)
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return nil, huma.Error500InternalServerError("failed to seek background task output")
	}
	content, err := io.ReadAll(io.LimitReader(file, tail))
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to read background task output")
	}

	resp := &BackgroundTaskOutputResponse{}
	resp.Body.TaskID = task.ID
	resp.Body.Path = task.OutputPath
	resp.Body.Content = string(content)
	resp.Body.Size = info.Size()
	resp.Body.Truncated = start > 0
	return resp, nil
}

func (s *Server) backgroundTasks() []BackgroundTask {
	tasks := discoverBackgroundTasks(s.emitter.RichMessages(), string(s.agentType))
	for i := range tasks {
		if tasks[i].OutputPath != "" && !filepath.IsAbs(tasks[i].OutputPath) && s.cwd != "" {
			tasks[i].OutputPath = filepath.Join(s.cwd, tasks[i].OutputPath)
		}
	}
	return tasks
}

func (s *Server) safeBackgroundOutputPath(discovered string) (string, error) {
	cleaned := filepath.Clean(discovered)
	extension := strings.ToLower(filepath.Ext(cleaned))
	if extension != ".out" && extension != ".output" {
		return "", errors.New("unsupported output extension")
	}
	info, err := os.Lstat(cleaned)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("output is not a regular file")
	}
	allowedRoots := []string{s.cwd, os.TempDir()}
	for _, root := range allowedRoots {
		if root == "" {
			continue
		}
		absoluteRoot, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		relative, err := filepath.Rel(absoluteRoot, cleaned)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return cleaned, nil
		}
	}
	return "", errors.New("output is outside allowed roots")
}

func discoverBackgroundTasks(messages []jsonlwatcher.RichMessage, agentType string) []BackgroundTask {
	tools := map[string]*discoveredTool{}
	order := []string{}
	for _, message := range messages {
		for _, block := range message.Content {
			switch block.Type {
			case "tool_use":
				input := map[string]any{}
				_ = json.Unmarshal(block.ToolInput, &input)
				tools[block.ToolUseID] = &discoveredTool{
					name: block.ToolName, input: input, status: block.Status,
					timestamp: message.Timestamp, updatedAt: message.Timestamp,
				}
				order = append(order, block.ToolUseID)
			case "tool_result":
				if tool := tools[block.ToolUseID]; tool != nil {
					tool.result = block.Text
					tool.updatedAt = message.Timestamp
					tool.status = block.Status
					tool.isError = block.IsError != nil && *block.IsError
				}
			}
		}
	}

	tasks := []BackgroundTask{}
	byRuntimeID := map[string]int{}
	for _, toolUseID := range order {
		tool := tools[toolUseID]
		if tool == nil {
			continue
		}
		name := normalizeToolName(tool.name)
		if isBackgroundStart(name, tool) {
			runtimeID := firstString(tool.input, "task_id", "session_id", "shell_id")
			if runtimeID == "" {
				runtimeID = extractBackgroundID(tool.result)
			}
			if runtimeID == "" {
				runtimeID = toolUseID
			}
			outputPath := extractOutputPath(tool.result)
			if outputPath == "" {
				outputPath = extractRedirectPath(firstString(tool.input, "cmd", "command"))
			}
			task := BackgroundTask{
				ID: runtimeID, Name: backgroundTaskName(tool), Status: backgroundStatus(tool, true),
				AgentType: agentType, ToolUseID: toolUseID, OutputPath: outputPath,
				StartedAt: tool.timestamp, UpdatedAt: tool.updatedAt,
			}
			tasks = append(tasks, task)
			byRuntimeID[runtimeID] = len(tasks) - 1
			continue
		}

		if isBackgroundFollowup(name) {
			runtimeID := firstString(tool.input, "task_id", "session_id", "shell_id", "cell_id")
			taskIndex, found := byRuntimeID[runtimeID]
			if !found {
				continue
			}
			task := &tasks[taskIndex]
			task.Status = backgroundStatus(tool, false)
			task.UpdatedAt = tool.updatedAt
			task.Output = appendBackgroundOutput(task.Output, tool.result)
			if path := extractOutputPath(tool.result); path != "" {
				task.OutputPath = path
			}
		}
	}
	sort.SliceStable(tasks, func(i, j int) bool { return tasks[i].StartedAt.After(tasks[j].StartedAt) })
	return tasks
}

func normalizeToolName(name string) string {
	name = strings.ToLower(name)
	if index := strings.LastIndexAny(name, ".:"); index >= 0 {
		name = name[index+1:]
	}
	return name
}

func isBackgroundStart(name string, tool *discoveredTool) bool {
	if name == "bash" {
		value, _ := tool.input["run_in_background"].(bool)
		return value
	}
	if name != "exec_command" && name != "exec" {
		return false
	}
	result := strings.ToLower(tool.result)
	command := firstString(tool.input, "cmd", "command")
	return strings.Contains(result, "running with session id") ||
		strings.Contains(result, "running with cell id") ||
		strings.Contains(command, "run_in_background") ||
		strings.Contains(command, " >") && strings.HasSuffix(strings.TrimSpace(command), "&")
}

func isBackgroundFollowup(name string) bool {
	return name == "taskoutput" || name == "task_output" || name == "write_stdin" || name == "wait"
}

func backgroundTaskName(tool *discoveredTool) string {
	if description := firstString(tool.input, "description", "name"); description != "" {
		return description
	}
	command := strings.TrimSpace(firstString(tool.input, "cmd", "command"))
	if line, _, found := strings.Cut(command, "\n"); found {
		command = line
	}
	if len([]rune(command)) > 80 {
		command = string([]rune(command)[:79]) + "…"
	}
	if command != "" {
		return command
	}
	return tool.name
}

func backgroundStatus(tool *discoveredTool, started bool) string {
	if tool.isError || tool.status == "failed" {
		return "failed"
	}
	result := strings.ToLower(tool.result)
	if strings.Contains(result, "running") || strings.Contains(result, "still running") {
		return "running"
	}
	if strings.Contains(result, "exit code") ||
		strings.Contains(result, "exited with code") ||
		strings.Contains(result, "completed") ||
		strings.Contains(result, "finished") ||
		strings.Contains(result, `"retrieval_status":"success"`) ||
		strings.Contains(result, "<retrieval_status>success</retrieval_status>") {
		return "completed"
	}
	if started {
		return "running"
	}
	return "unknown"
}

func appendBackgroundOutput(current, next string) string {
	next = strings.TrimSpace(next)
	if next == "" || strings.Contains(current, next) {
		return current
	}
	if current == "" {
		return next
	}
	return current + "\n\n" + next
}

func firstString(input map[string]any, keys ...string) string {
	for _, key := range keys {
		switch value := input[key].(type) {
		case string:
			if value != "" {
				return value
			}
		case float64:
			return strconv.FormatInt(int64(value), 10)
		}
	}
	return ""
}

func extractBackgroundID(text string) string {
	match := backgroundIDPattern.FindStringSubmatch(text)
	if len(match) == 2 {
		return match[1]
	}
	return ""
}

func extractOutputPath(text string) string {
	match := outputPathPattern.FindStringSubmatch(text)
	if len(match) == 2 {
		return strings.TrimRight(match[1], ".,;:)")
	}
	return ""
}

func extractRedirectPath(command string) string {
	match := redirectPathPattern.FindStringSubmatch(command)
	if len(match) == 2 {
		return strings.TrimRight(match[1], ".,;:)")
	}
	return ""
}
