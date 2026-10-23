package httpapi

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func statusOf(t *testing.T, err error) int {
	t.Helper()
	var se huma.StatusError
	require.True(t, errors.As(err, &se), "not a status error: %v", err)
	return se.GetStatus()
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func TestFileContent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "hello\n")
	writeFile(t, filepath.Join(dir, "sub", "b.go"), "package b\n")
	writeFile(t, filepath.Join(dir, "bin.dat"), "PNG\x00\x01\x02")
	// The cut at 1 MiB falls inside the three bytes of the last 世.
	big := strings.Repeat("a", maxViewedBytes-1) + strings.Repeat("世", 10)
	writeFile(t, filepath.Join(dir, "big.txt"), big)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	writeFile(t, outside, "secret")
	require.NoError(t, os.Symlink(outside, filepath.Join(dir, "link.txt")))
	s := &fileAPI{cwd: dir}
	get := func(path string) (*FileContentResponse, error) {
		return s.getFileContent(t.Context(), &FilePathQuery{Path: path})
	}

	resp, err := get("a.txt")
	require.NoError(t, err)
	assert.Equal(t, "hello\n", resp.Body.Content)
	assert.Equal(t, "a.txt", resp.Body.Relative)
	assert.Equal(t, int64(6), resp.Body.Size)
	assert.False(t, resp.Body.Truncated || resp.Body.Binary)

	resp, err = get(filepath.Join(dir, "sub", "b.go"))
	require.NoError(t, err, "an absolute path inside the working directory")
	assert.Equal(t, "sub/b.go", resp.Body.Relative)
	assert.Equal(t, filepath.Join(dir, "sub", "b.go"), resp.Body.Path)

	resp, err = get("bin.dat")
	require.NoError(t, err)
	assert.True(t, resp.Body.Binary)
	assert.Empty(t, resp.Body.Content)

	resp, err = get("big.txt")
	require.NoError(t, err)
	assert.True(t, resp.Body.Truncated)
	assert.False(t, resp.Body.Binary, "a cut multi-byte character doesn't make the file binary")
	assert.LessOrEqual(t, len(resp.Body.Content), maxViewedBytes)
	assert.True(t, utf8.ValidString(resp.Body.Content))
	assert.Equal(t, int64(len(big)), resp.Body.Size)

	for path, status := range map[string]int{
		"":                            400,
		"sub":                         400,
		"missing.txt":                 404,
		"../x.txt":                    403,
		"sub/../../x.txt":             403,
		outside:                       403,
		"/etc/hostname":               403,
		"link.txt":                    403,
		filepath.Join(dir, "..", "x"): 403,
	} {
		_, err := get(path)
		require.Error(t, err, path)
		assert.Equal(t, status, statusOf(t, err), path)
	}
}

// git runs git in dir for test setup, without the user's configuration.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
}

func TestFileDiff(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("needs git")
	}
	// The server's git runs with the process environment.
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	t.Run("not in a repository", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "a.txt"), "x\n")
		resp, err := (&fileAPI{cwd: dir}).getFileDiff(t.Context(), &FilePathQuery{Path: "a.txt"})
		require.NoError(t, err)
		assert.Equal(t, FileDiffNotInRepo, resp.Body.Status)
		assert.Empty(t, resp.Body.Diff)
	})

	repo := t.TempDir()
	git(t, repo, "init", "-q")
	writeFile(t, filepath.Join(repo, "app", "changed.txt"), "one\n")
	writeFile(t, filepath.Join(repo, "app", "same.txt"), "same\n")
	writeFile(t, filepath.Join(repo, "app", "gone.txt"), "bye\n")
	writeFile(t, filepath.Join(repo, "app", "dir", "x.txt"), "x\n")
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-q", "-m", "init")
	writeFile(t, filepath.Join(repo, "app", "changed.txt"), "two\n")
	writeFile(t, filepath.Join(repo, "app", "new file.txt"), "fresh\n")
	writeFile(t, filepath.Join(repo, "app", "staged.txt"), "staged\n")
	git(t, repo, "add", "app/staged.txt")
	require.NoError(t, os.Remove(filepath.Join(repo, "app", "gone.txt")))
	require.NoError(t, os.RemoveAll(filepath.Join(repo, "app", "dir")))
	// The working directory is a subdirectory of the repository.
	s := &fileAPI{cwd: filepath.Join(repo, "app")}

	for _, tc := range []struct {
		path     string
		status   FileDiffStatus
		contains []string
	}{
		{"changed.txt", FileDiffModified, []string{"--- a/app/changed.txt", "+++ b/app/changed.txt", "-one", "+two"}},
		{"same.txt", FileDiffUnchanged, nil},
		{"new file.txt", FileDiffUntracked, []string{"diff --git a/app/new file.txt b/app/new file.txt", "new file mode", "+fresh", "+++ b/app/new file.txt"}},
		{"staged.txt", FileDiffAdded, []string{"new file mode", "+staged", "+++ b/app/staged.txt"}},
		{"gone.txt", FileDiffDeleted, []string{"diff --git a/app/gone.txt b/app/gone.txt", "deleted file mode", "--- a/app/gone.txt", "-bye"}},
		{"dir/x.txt", FileDiffDeleted, []string{"-x"}},
		{filepath.Join(repo, "app", "changed.txt"), FileDiffModified, []string{"+two"}},
	} {
		resp, err := s.getFileDiff(t.Context(), &FilePathQuery{Path: tc.path})
		require.NoError(t, err, tc.path)
		assert.Equal(t, tc.status, resp.Body.Status, tc.path)
		for _, want := range tc.contains {
			assert.Contains(t, resp.Body.Diff, want, tc.path)
		}
		if tc.status == FileDiffUnchanged {
			assert.Empty(t, resp.Body.Diff)
		}
	}

	t.Run("a repository without commits", func(t *testing.T) {
		dir := t.TempDir()
		git(t, dir, "init", "-q")
		writeFile(t, filepath.Join(dir, "a.txt"), "first\n")
		resp, err := (&fileAPI{cwd: dir}).getFileDiff(t.Context(), &FilePathQuery{Path: "a.txt"})
		require.NoError(t, err)
		assert.Equal(t, FileDiffUntracked, resp.Body.Status)
		assert.Contains(t, resp.Body.Diff, "+first")
		assert.Contains(t, resp.Body.Diff, "+++ b/a.txt")
	})

	_, err := s.getFileDiff(t.Context(), &FilePathQuery{Path: "never-existed.txt"})
	assert.Equal(t, 404, statusOf(t, err))
	_, err = s.getFileDiff(t.Context(), &FilePathQuery{Path: "../outside.txt"})
	assert.Equal(t, 403, statusOf(t, err))
	_, err = os.Stat(filepath.Join(repo, ".git", "index.lock"))
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestFileDiffStaysInTheWorkingDirectory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("needs git")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	// Another repository, reached through a symlink in the working
	// directory.
	other := t.TempDir()
	git(t, other, "init", "-q")
	writeFile(t, filepath.Join(other, "secret.env"), "TOKEN=abc123\n")
	work := t.TempDir()
	git(t, work, "init", "-q")
	require.NoError(t, os.Symlink(other, filepath.Join(work, "link")))
	s := &fileAPI{cwd: work}

	_, err := s.getFileDiff(t.Context(), &FilePathQuery{Path: "link/secret.env"})
	require.Error(t, err)
	assert.Equal(t, 403, statusOf(t, err))
	_, err = s.getFileDiff(t.Context(), &FilePathQuery{Path: "."})
	require.Error(t, err)
	assert.Equal(t, 400, statusOf(t, err), "a directory isn't a file")
}

func TestFileDiffRunsNoRepositoryPrograms(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("needs git")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	writeFile(t, filepath.Join(repo, "a.txt"), "one\n")
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-q", "-m", "init")
	// What an agent that can write files could set up: a clean filter
	// that runs a command whenever git reads the working copy.
	marker := filepath.Join(t.TempDir(), "ran")
	writeFile(t, filepath.Join(repo, ".gitattributes"), "*.txt filter=evil\n")
	git(t, repo, "config", "filter.evil.clean", "touch "+marker+"; cat")
	writeFile(t, filepath.Join(repo, "a.txt"), "two\n")

	resp, err := (&fileAPI{cwd: repo}).getFileDiff(t.Context(), &FilePathQuery{Path: "a.txt"})
	require.NoError(t, err)
	assert.Equal(t, FileDiffModified, resp.Body.Status)
	assert.Contains(t, resp.Body.Diff, "+two")
	_, err = os.Stat(marker)
	assert.ErrorIs(t, err, os.ErrNotExist, "the filter didn't run")
}

func TestFileDiffOfALargeFile(t *testing.T) {
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	large := strings.Repeat("0123456789abcdef\n", 300_000) // 5.1 MB, more than maxDiffInput
	writeFile(t, filepath.Join(repo, "big.txt"), large)
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-q", "-m", "init")
	s := &fileAPI{cwd: repo}

	// Too large to compare by content, yet unchanged.
	resp, err := s.getFileDiff(t.Context(), &FilePathQuery{Path: "big.txt"})
	require.NoError(t, err)
	assert.Equal(t, FileDiffUnchanged, resp.Body.Status)

	writeFile(t, filepath.Join(repo, "big.txt"), large+"one more line\n")
	resp, err = s.getFileDiff(t.Context(), &FilePathQuery{Path: "big.txt"})
	require.NoError(t, err)
	assert.Equal(t, FileDiffModified, resp.Body.Status)
	assert.True(t, resp.Body.Truncated)
}
