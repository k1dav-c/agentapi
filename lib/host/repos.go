package host

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// RepoFromRemote returns owner/name of a github.com remote URL: scp-like
// (git@github.com:o/r.git), ssh:// or https://.
func RepoFromRemote(remote string) (string, bool) {
	remote = strings.TrimSpace(remote)
	var path string
	switch {
	case strings.HasPrefix(remote, "git@github.com:"):
		path = strings.TrimPrefix(remote, "git@github.com:")
	default:
		for _, prefix := range []string{"https://github.com/", "http://github.com/", "ssh://git@github.com/", "git://github.com/"} {
			if rest, ok := strings.CutPrefix(remote, prefix); ok {
				path = rest
				break
			}
		}
		if path == "" {
			// https://user@github.com/o/r
			if rest, ok := strings.CutPrefix(remote, "https://"); ok {
				if at := strings.Index(rest, "@github.com/"); at >= 0 && !strings.Contains(rest[:at], "/") {
					path = rest[at+len("@github.com/"):]
				}
			}
		}
	}
	path = strings.TrimSuffix(strings.TrimSuffix(path, "/"), ".git")
	owner, name, err := ParseRepo(path)
	if err != nil {
		return "", false
	}
	return owner + "/" + name, true
}

// ScanRepos finds the clones of GitHub repositories in dirs and up to two
// levels below them, by their origin, as owner/name to directory. A
// repository found more than once is the one under workdir, else the first.
// Worktrees (whose .git is a file) and the launcher's worktree directories
// are skipped.
func ScanRepos(ctx context.Context, dirs []string, workdir string) map[string]string {
	found := map[string]string{}
	under := func(dir string) bool {
		rel, err := filepath.Rel(workdir, dir)
		return err == nil && !strings.HasPrefix(rel, "..")
	}
	consider := func(dir string) {
		if info, err := os.Stat(filepath.Join(dir, ".git")); err != nil || !info.IsDir() {
			return
		}
		remote, err := git(ctx, dir, nil, "config", "--get", "remote.origin.url")
		if err != nil {
			return
		}
		repo, ok := RepoFromRemote(remote)
		if !ok {
			return
		}
		if prev, ok := found[repo]; ok && (under(prev) || !under(dir)) {
			return
		}
		found[repo] = dir
	}
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		consider(dir)
		if depth == 2 {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			name := entry.Name()
			if !entry.IsDir() || strings.HasPrefix(name, ".") || strings.HasSuffix(name, worktreesSuffix) {
				continue
			}
			walk(filepath.Join(dir, name), depth+1)
		}
	}
	for _, dir := range slices.Compact(slices.Clone(dirs)) {
		walk(dir, 0)
	}
	return found
}

// worktreesSuffix names the directory beside a clone that holds its jobs'
// worktrees: <repo>.worktrees/<issue>.
const worktreesSuffix = ".worktrees"

// git runs git in dir and returns its output without the final newline.
func git(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = env
	}
	out, err := cmd.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok && len(exit.Stderr) > 0 {
			return "", &gitError{args: args, stderr: strings.TrimSpace(string(exit.Stderr)), err: err}
		}
		return "", &gitError{args: args, err: err}
	}
	return strings.TrimRight(string(out), "\n"), nil
}

type gitError struct {
	args   []string
	stderr string
	err    error
}

func (e *gitError) Error() string {
	msg := "git " + strings.Join(e.args, " ") + ": " + e.err.Error()
	if e.stderr != "" {
		msg += ": " + e.stderr
	}
	return msg
}

func (e *gitError) Unwrap() error { return e.err }
