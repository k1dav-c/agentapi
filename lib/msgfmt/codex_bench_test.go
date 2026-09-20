package msgfmt

import (
	"os"
	"path/filepath"
	"testing"
)

// loadFixture reads a Codex test fixture file from testdata/format/codex/.
func loadFixture(b *testing.B, scenario, file string) string {
	b.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "format", "codex", scenario, file))
	if err != nil {
		b.Fatal(err)
	}
	return string(data)
}

// BenchmarkCodexFormat benchmarks the full Codex formatting pipeline
// (FormatAgentMessage → formatCodexMessage → RemoveUserInput →
// removeCodexMessageBox → trimEmptyLines → collapseBlankLines)
// using real screen fixtures.
func BenchmarkCodexFormat(b *testing.B) {
	b.Run("first_message", func(b *testing.B) {
		msg := loadFixture(b, "first_message", "msg.txt")
		user := loadFixture(b, "first_message", "user.txt")
		b.ReportAllocs()
		for b.Loop() {
			FormatAgentMessage(AgentTypeCodex, msg, user)
		}
	})

	b.Run("thinking", func(b *testing.B) {
		msg := loadFixture(b, "thinking", "msg.txt")
		user := loadFixture(b, "thinking", "user.txt")
		b.ReportAllocs()
		for b.Loop() {
			FormatAgentMessage(AgentTypeCodex, msg, user)
		}
	})

	b.Run("second_message", func(b *testing.B) {
		msg := loadFixture(b, "second_message", "msg.txt")
		user := loadFixture(b, "second_message", "user.txt")
		b.ReportAllocs()
		for b.Loop() {
			FormatAgentMessage(AgentTypeCodex, msg, user)
		}
	})

	b.Run("confirmation_box", func(b *testing.B) {
		msg := loadFixture(b, "confirmation_box", "msg.txt")
		// confirmation_box has no user input
		b.ReportAllocs()
		for b.Loop() {
			FormatAgentMessage(AgentTypeCodex, msg, "")
		}
	})

	b.Run("multi_line_input", func(b *testing.B) {
		msg := loadFixture(b, "multi-line-input", "msg.txt")
		user := loadFixture(b, "multi-line-input", "user.txt")
		b.ReportAllocs()
		for b.Loop() {
			FormatAgentMessage(AgentTypeCodex, msg, user)
		}
	})

	b.Run("remove_task_tool_call", func(b *testing.B) {
		msg := loadFixture(b, "remove-task-tool-call", "msg.txt")
		user := loadFixture(b, "remove-task-tool-call", "user.txt")
		b.ReportAllocs()
		for b.Loop() {
			FormatAgentMessage(AgentTypeCodex, msg, user)
		}
	})
}
