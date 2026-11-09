package workspacefs

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gitIn runs git in dir for a test's setup, without the user's
// configuration.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{
		"-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "init.defaultBranch=main",
		"-c", "commit.gpgsign=false",
	}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

// newRepo is a repository with a.txt, app/c.txt, gone/d.txt and big.txt
// (over maxDiffInput) committed.
func newRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git isn't installed")
	}
	repo := t.TempDir()
	gitIn(t, repo, "init", "--quiet")
	writeFile(t, filepath.Join(repo, "a.txt"), "one\n")
	writeFile(t, filepath.Join(repo, "app", "c.txt"), "app\n")
	writeFile(t, filepath.Join(repo, "gone", "d.txt"), "soon gone\n")
	writeFile(t, filepath.Join(repo, "big.txt"), strings.Repeat("big line\n", maxDiffInput/9+1))
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "commit", "--quiet", "-m", "first")
	return repo
}

func TestDiff(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	d := Dir(repo)
	diff := func(path string) Diff {
		t.Helper()
		got, err := d.Diff(t.Context(), path)
		require.NoError(t, err)
		return got
	}

	got := diff("a.txt")
	assert.Equal(t, Diff{Relative: "a.txt", Status: DiffUnchanged}, got)

	writeFile(t, filepath.Join(repo, "a.txt"), "two\n")
	got = diff("a.txt")
	assert.Equal(t, DiffModified, got.Status)
	assert.Contains(t, got.Text, "diff --git a/a.txt b/a.txt")
	assert.Contains(t, got.Text, "-one\n+two\n")
	assert.False(t, got.Truncated)

	require.NoError(t, os.RemoveAll(filepath.Join(repo, "gone")))
	got = diff("gone/d.txt")
	assert.Equal(t, DiffDeleted, got.Status, "its directory is gone too")
	assert.Contains(t, got.Text, "deleted file mode")
	assert.Contains(t, got.Text, "-soon gone")

	writeFile(t, filepath.Join(repo, "new.txt"), "fresh\n")
	got = diff("new.txt")
	assert.Equal(t, DiffUntracked, got.Status)
	assert.Contains(t, got.Text, "+++ b/new.txt")
	assert.Contains(t, got.Text, "+fresh")
	gitIn(t, repo, "add", "new.txt")
	assert.Equal(t, DiffAdded, diff("new.txt").Status, "staged")

	_, err := d.Diff(t.Context(), "missing.txt")
	var refused *Error
	require.True(t, errors.As(err, &refused), "%v", err)
	assert.Equal(t, NotFound, refused.Kind)
	assert.Equal(t, "missing.txt doesn't exist", refused.Message)

	// Too large to compare by content: git's object id says it is
	// unchanged; changed, there is no diff text.
	assert.Equal(t, DiffUnchanged, diff("big.txt").Status)
	f, err := os.OpenFile(filepath.Join(repo, "big.txt"), os.O_APPEND|os.O_WRONLY, 0)
	require.NoError(t, err)
	_, err = f.WriteString("one more\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	got = diff("big.txt")
	assert.Equal(t, Diff{Relative: "big.txt", Status: DiffModified, Truncated: true}, got)
}

// The working directory can be below the repository's top: the diff names
// the file by its path in the repository.
func TestDiffInASubdirectory(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	writeFile(t, filepath.Join(repo, "app", "c.txt"), "changed\n")
	got, err := Dir(filepath.Join(repo, "app")).Diff(t.Context(), "c.txt")
	require.NoError(t, err)
	assert.Equal(t, "c.txt", got.Relative)
	assert.Equal(t, DiffModified, got.Status)
	assert.Contains(t, got.Text, "diff --git a/app/c.txt b/app/c.txt")
}

func TestDiffOutsideARepository(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git isn't installed")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "a\n")
	got, err := Dir(dir).Diff(t.Context(), "a.txt")
	require.NoError(t, err)
	assert.Equal(t, Diff{Relative: "a.txt", Status: DiffNotInRepo}, got)
}

// A repository can configure programs git runs on a file, such as a clean
// filter; looking at a diff runs none of them.
func TestDiffRunsNoConfiguredProgram(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	marker := filepath.Join(t.TempDir(), "ran")
	writeFile(t, filepath.Join(repo, ".gitattributes"), "*.txt filter=evil diff=evil\n")
	gitIn(t, repo, "config", "filter.evil.clean", "touch "+marker+"; cat")
	gitIn(t, repo, "config", "diff.evil.textconv", "touch "+marker+"; cat")
	gitIn(t, repo, "config", "diff.external", "touch "+marker)
	writeFile(t, filepath.Join(repo, "a.txt"), "two\n")
	got, err := Dir(repo).Diff(t.Context(), "a.txt")
	require.NoError(t, err)
	assert.Equal(t, DiffModified, got.Status)
	assert.Contains(t, got.Text, "+two")
	assert.NoFileExists(t, marker)
}

// A file reached through a symlink out of the working directory isn't
// read.
func TestDiffRefusesASymlinkOut(t *testing.T) {
	t.Parallel()
	repo := newRepo(t)
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "secret.txt"), "secret\n")
	require.NoError(t, os.Symlink(outside, filepath.Join(repo, "out")))
	_, err := Dir(repo).Diff(t.Context(), "out/secret.txt")
	require.Error(t, err)
	var refused *Error
	require.True(t, errors.As(err, &refused), "%v", err)
	assert.NotContains(t, err.Error(), "secret\n")
}

func TestCappedBuffer(t *testing.T) {
	t.Parallel()
	b := &cappedBuffer{max: 5}
	n, err := b.Write([]byte("abc"))
	require.NoError(t, err)
	assert.Equal(t, 3, n)
	n, err = b.Write([]byte("dé"))
	require.NoError(t, err)
	assert.Equal(t, 3, n, "it takes everything, keeping what fits")
	assert.True(t, b.dropped)
	assert.Equal(t, "abcd", b.String(), "cut at a rune boundary")
}
