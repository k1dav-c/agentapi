package jsonlwatcher

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadCodexSessionMeta_LongFirstLine(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "rollout-test.jsonl")

	// Real session_meta lines embed base_instructions and can exceed 4KB.
	instructions := strings.Repeat("You are Codex, an agent based on GPT-5. ", 500) // ~20KB
	firstLine := fmt.Sprintf(
		`{"timestamp":"2026-07-25T14:21:42.025Z","type":"session_meta","payload":{"session_id":"test-session","cwd":"/home/user/project","base_instructions":{"text":%q}}}`,
		instructions,
	)
	content := firstLine + "\n" + `{"type":"event_msg","timestamp":"2026-07-25T14:21:43.000Z","payload":{"type":"task_started"}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	meta, err := readCodexSessionMeta(path)
	if err != nil {
		t.Fatalf("readCodexSessionMeta failed: %v", err)
	}
	if meta.Payload.CWD != "/home/user/project" {
		t.Errorf("CWD = %q, want /home/user/project", meta.Payload.CWD)
	}
	if meta.Timestamp != "2026-07-25T14:21:42.025Z" {
		t.Errorf("timestamp = %q, want session metadata timestamp", meta.Timestamp)
	}
}

func TestCodexResolver_IgnoresPreviousSessionForSameCWD(t *testing.T) {
	sessionsDir := t.TempDir()
	now := time.Now().UTC()
	dateDir := filepath.Join(
		sessionsDir,
		now.Format("2006"),
		now.Format("01"),
		now.Format("02"),
	)
	if err := os.MkdirAll(dateDir, 0o755); err != nil {
		t.Fatal(err)
	}

	notBefore := now.Add(-time.Minute)
	oldPath := writeCodexSession(
		t,
		dateDir,
		"old.jsonl",
		"/home/user/project",
		notBefore.Add(-time.Minute),
	)
	newPath := writeCodexSession(
		t,
		dateDir,
		"new.jsonl",
		"/home/user/project",
		notBefore.Add(time.Second),
	)

	// An old session may have the newest filesystem mtime because another
	// Codex process in the same directory is still writing to it.
	if err := os.Chtimes(oldPath, now.Add(time.Minute), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newPath, now, now); err != nil {
		t.Fatal(err)
	}

	resolver := &CodexResolver{
		CWD:         "/home/user/project",
		NotBefore:   notBefore,
		SessionsDir: sessionsDir,
	}
	got, err := resolver.Resolve()
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if got != newPath {
		t.Errorf("Resolve() = %q, want current session %q", got, newPath)
	}
}

func TestCodexResolver_WaitsWhenOnlyPreviousSessionExists(t *testing.T) {
	sessionsDir := t.TempDir()
	now := time.Now().UTC()
	dateDir := filepath.Join(
		sessionsDir,
		now.Format("2006"),
		now.Format("01"),
		now.Format("02"),
	)
	if err := os.MkdirAll(dateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeCodexSession(
		t,
		dateDir,
		"old.jsonl",
		"/home/user/project",
		now.Add(-time.Minute),
	)

	resolver := &CodexResolver{
		CWD:         "/home/user/project",
		NotBefore:   now,
		SessionsDir: sessionsDir,
	}
	if path, err := resolver.Resolve(); err == nil {
		t.Fatalf("Resolve() = %q, want no current session", path)
	}
}

func writeCodexSession(
	t *testing.T,
	dir string,
	name string,
	cwd string,
	timestamp time.Time,
) string {
	t.Helper()
	path := filepath.Join(dir, name)
	content := fmt.Sprintf(
		`{"timestamp":%q,"type":"session_meta","payload":{"session_id":%q,"cwd":%q}}`+"\n",
		timestamp.Format(time.RFC3339Nano),
		strings.TrimSuffix(name, ".jsonl"),
		cwd,
	)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
