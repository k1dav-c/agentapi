package codexlog

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/coder/agentapi/lib/agentlog"
)

// Resolver finds the JSONL session file for a Codex process.
//
// Codex stores sessions at ~/.codex/sessions/YYYY/MM/DD/rollout-<ts>-<id>.jsonl.
// There is no PID mapping file, so we scan for the most recent session
// matching the working directory.
type Resolver struct {
	CWD         string
	NotBefore   time.Time
	SessionsDir string

	// metas holds the first line of each session file read so far:
	// Resolve runs on every poll, so each file is read once instead of
	// every time.
	metas Metas
}

// sessionDirs returns the date directories that may hold current
// session files: today's and yesterday's (in case of a timezone edge).
func sessionDirs(root string, now time.Time) []string {
	now = now.UTC()
	yesterday := now.AddDate(0, 0, -1)
	return []string{
		filepath.Join(root, now.Format("2006"), now.Format("01"), now.Format("02")),
		filepath.Join(root, yesterday.Format("2006"), yesterday.Format("01"), yesterday.Format("02")),
	}
}

// Resolve finds the Codex JSONL file by scanning the sessions directory.
//
// It looks in ~/.codex/sessions/ for today's date directory, then scans
// all JSONL files sorted by modification time (newest first), reading each
// file's first line to match the working directory.
func (r *Resolver) Resolve() (string, error) {
	sessionsDir, err := SessionsRoot(r.SessionsDir)
	if err != nil {
		return "", err
	}
	candidates := sessionDirs(sessionsDir, time.Now())

	type fileInfo struct {
		path    string
		modTime time.Time
	}

	var files []fileInfo
	for _, dir := range candidates {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // directory might not exist
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			files = append(files, fileInfo{
				path:    filepath.Join(dir, entry.Name()),
				modTime: info.ModTime(),
			})
		}
	}

	// Sort by modification time, newest first
	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.After(files[j].modTime)
	})
	listed := make(map[string]bool, len(files))
	for _, fi := range files {
		listed[fi.path] = true
	}
	// Forget the files no longer listed, such as yesterday's once the date
	// moves on.
	r.metas.Retain(func(path string) bool { return listed[path] })

	// Find the newest file that matches our CWD, which Codex records by its
	// real path.
	cwd := agentlog.NewDir(r.CWD)
	for _, fi := range files {
		meta, err := r.metas.Get(fi.path)
		if err != nil {
			continue
		}
		// Sub-agent threads share the cwd and are newer than the main
		// thread; following them would replace the session's messages
		// with a sub-agent's.
		if meta.IsSubAgent() {
			continue
		}
		startedAt, err := time.Parse(time.RFC3339Nano, meta.Timestamp)
		if err != nil {
			continue
		}
		if cwd.Is(meta.Payload.CWD) &&
			(r.NotBefore.IsZero() || !startedAt.Before(r.NotBefore)) {
			return fi.path, nil
		}
	}

	return "", fmt.Errorf("no Codex session file found for cwd %q", r.CWD)
}
