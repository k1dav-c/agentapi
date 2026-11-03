package workspacefs

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxViewedBytes is how much of a file Content returns, and of a diff Diff
// returns.
const MaxViewedBytes = 1 << 20

// gitTimeout bounds the git commands of Diff.
const gitTimeout = 10 * time.Second

// Dir is the agent's working directory, whose files the chat shows: their
// text, their diff, a preview of an HTML file, an image, and the search for
// a path the agent named. Paths outside it are refused, and symlinks inside
// it can't lead out. An empty Dir is unknown, and refuses every path.
type Dir string

// ErrorKind is why a Dir refused a request.
type ErrorKind int

const (
	// Invalid: the path is empty, isn't a file, or the file doesn't fit
	// the request.
	Invalid ErrorKind = iota + 1
	// Forbidden: the path is outside the directory, or can't be read.
	Forbidden
	// NotFound: the file, or the directory itself, is unknown.
	NotFound
)

// Error is a request a Dir refused, with the message to show.
type Error struct {
	Kind    ErrorKind
	Message string
}

func (e *Error) Error() string { return e.Message }

func refuse(kind ErrorKind, message string) error { return &Error{Kind: kind, Message: message} }

// errorKind is the kind of a refusal, or 0 for another error.
func errorKind(err error) ErrorKind {
	var refused *Error
	if errors.As(err, &refused) {
		return refused.Kind
	}
	return 0
}

// DiffStatus is how a file differs from the last commit.
type DiffStatus string

const (
	DiffModified  DiffStatus = "modified"
	DiffAdded     DiffStatus = "added"
	DiffDeleted   DiffStatus = "deleted"
	DiffUntracked DiffStatus = "untracked"
	DiffUnchanged DiffStatus = "unchanged"
	// DiffNotInRepo: the file isn't in a git repository, so there is
	// nothing to compare it with.
	DiffNotInRepo DiffStatus = "not_in_repo"
)

// Resolve resolves path, absolute or relative to the agent's working
// directory, to a path relative to that directory. Paths outside it are
// refused: the chat shows the agent's work, not the machine.
func (d Dir) Resolve(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", refuse(Invalid, "path is empty")
	}
	if d == "" {
		return "", refuse(NotFound, "the agent's working directory is unknown")
	}
	rel := filepath.Clean(path)
	if filepath.IsAbs(rel) {
		rel = relativeTo(string(d), rel)
		if rel == "" {
			// The agent may report the path with symlinks resolved.
			if real, err := filepath.EvalSymlinks(string(d)); err == nil {
				rel = relativeTo(real, filepath.Clean(path))
			}
		}
	}
	if rel == "" || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", refuse(Forbidden, fmt.Sprintf("%s is outside the agent's working directory", path))
	}
	return rel, nil
}

// relativeTo returns path relative to base, or "" if it isn't inside it.
func relativeTo(base, path string) string {
	rel, err := filepath.Rel(base, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	return rel
}

// Content is a file's text, as Dir.Content returns it.
type Content struct {
	// Relative is the path relative to the directory, with slashes.
	Relative string
	Size     int64
	// Text is up to MaxViewedBytes of the file; empty for a binary file.
	Text      string
	Truncated bool
	// Binary: the file isn't UTF-8 text, so it has no Text.
	Binary bool
}

// Content reads the file at path: up to MaxViewedBytes of its text.
func (d Dir) Content(path string) (Content, error) {
	rel, err := d.Resolve(path)
	if err != nil {
		return Content{}, err
	}
	// The root keeps symlinks inside the working directory from leading out.
	root, err := os.OpenRoot(string(d))
	if err != nil {
		return Content{}, fmt.Errorf("open the agent's working directory: %w", err)
	}
	defer func() { _ = root.Close() }()
	data, info, err := readRegular(root, rel, path, MaxViewedBytes)
	if err != nil {
		return Content{}, err
	}

	content := Content{Relative: filepath.ToSlash(rel), Size: info.Size()}
	if len(data) > MaxViewedBytes {
		data = trimPartialRune(data[:MaxViewedBytes])
		content.Truncated = true
	}
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		content.Binary = true
		return content, nil
	}
	content.Text = string(data)
	return content, nil
}

// readRegular reads up to limit+1 bytes of a regular file in root: one more
// than limit says the file is longer. It is checked before it is opened,
// since opening a FIFO would wait for a writer.
func readRegular(root *os.Root, rel, path string, limit int) ([]byte, fs.FileInfo, error) {
	info, err := root.Stat(rel)
	if err != nil {
		return nil, nil, fileError(path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, nil, refuse(Invalid, fmt.Sprintf("%s is not a file", path))
	}
	f, err := root.Open(rel)
	if err != nil {
		return nil, nil, fileError(path, err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", path, err)
	}
	return data, info, nil
}

// fileError maps an error opening a file in the working directory to the
// refusal the chat shows.
func fileError(path string, err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return refuse(NotFound, fmt.Sprintf("%s doesn't exist", path))
	}
	// A symlink leading out of the working directory, or no permission.
	return refuse(Forbidden, fmt.Sprintf("%s can't be read: %v", path, err))
}

// trimPartialRune drops an incomplete UTF-8 sequence that cutting data left
// at its end.
func trimPartialRune(data []byte) []byte {
	for i := 0; i < utf8.UTFMax-1 && len(data) > 0; i++ {
		if r, size := utf8.DecodeLastRune(data); r != utf8.RuneError || size != 1 {
			break
		}
		data = data[:len(data)-1]
	}
	return data
}

// Diff is a file's uncommitted changes, as Dir.Diff returns them.
type Diff struct {
	// Relative is the path relative to the directory, with slashes.
	Relative string
	Status   DiffStatus
	// Text is git's unified diff of the working tree against HEAD, up to
	// MaxViewedBytes; empty when unchanged or not in a repository.
	Text      string
	Truncated bool
}

// Diff returns the uncommitted changes of the file at path against HEAD. In the
// repository git only reads objects and the index (rev-parse, ls-files,
// cat-file), which runs none of the programs a repository can configure,
// such as clean filters (an agent that can write .git/config could run a
// command when the user looks at a diff). The diff compares two copies in a
// directory of its own; the working copy is read through the root, so a
// symlink can't lead out.
func (d Dir) Diff(ctx context.Context, path string) (Diff, error) {
	rel, err := d.Resolve(path)
	if err != nil {
		return Diff{}, err
	}
	diff := Diff{Relative: filepath.ToSlash(rel)}
	root, err := os.OpenRoot(string(d))
	if err != nil {
		return Diff{}, fmt.Errorf("open the agent's working directory: %w", err)
	}
	defer func() { _ = root.Close() }()

	// The working copy, unless it is gone.
	current, _, err := readRegular(root, rel, path, maxDiffInput)
	exists := err == nil
	if err != nil && errorKind(err) != NotFound {
		return Diff{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	dir, spec, err := d.gitDir(root, rel, path)
	if err != nil {
		return Diff{}, err
	}
	// The directory's path in the repository, e.g. "app/".
	prefix, err := runGit(ctx, dir, MaxViewedBytes, "rev-parse", "--show-prefix")
	if err != nil {
		diff.Status = DiffNotInRepo
		return diff, nil
	}
	repoPath := strings.TrimSpace(prefix.text) + filepath.ToSlash(spec)
	head, headErr := runGit(ctx, dir, maxDiffInput, "cat-file", "blob", "HEAD:"+repoPath)
	inHead := headErr == nil
	_, trackedErr := runGit(ctx, dir, MaxViewedBytes, "ls-files", "--error-unmatch", "--", ":(literal)"+spec)

	var old *string
	if inHead {
		old = &head.text
	}
	var cur *string
	if exists {
		text := string(current)
		cur = &text
	}
	switch {
	case inHead && exists:
		diff.Status = DiffModified
		same := *old == *cur
		if head.truncated || len(current) > maxDiffInput {
			// Too large to compare here: compare git's object ids.
			same, _ = sameAsHead(ctx, dir, root, rel, repoPath)
		}
		if same {
			diff.Status = DiffUnchanged
			return diff, nil
		}
	case inHead:
		diff.Status = DiffDeleted
	case exists && trackedErr == nil:
		// Staged, in a repository without commits too.
		diff.Status = DiffAdded
	case exists:
		diff.Status = DiffUntracked
	default:
		return Diff{}, refuse(NotFound, fmt.Sprintf("%s doesn't exist", path))
	}
	if head.truncated || len(current) > maxDiffInput {
		// Too large to diff here.
		diff.Truncated = true
		return diff, nil
	}
	out, err := diffCopies(ctx, repoPath, old, cur)
	if err != nil {
		return Diff{}, fmt.Errorf("git diff %s: %w", path, err)
	}
	diff.Text = out.text
	diff.Truncated = out.truncated
	return diff, nil
}

// gitDir returns the directory git runs in for the file rel (path as the
// client named it) and the file's path relative to it. That is the closest
// directory that exists (a deleted file's directory may be gone too), which
// must be inside the working directory itself, not reached through a
// symlink.
func (d Dir) gitDir(root *os.Root, rel, path string) (dir, spec string, err error) {
	dirRel := filepath.Dir(rel)
	for dirRel != "." {
		if info, err := root.Stat(dirRel); err == nil && info.IsDir() {
			break
		}
		dirRel = filepath.Dir(dirRel)
	}
	if info, err := root.Lstat(dirRel); err != nil || !info.IsDir() {
		return "", "", refuse(Forbidden, fmt.Sprintf("%s is outside the agent's working directory", path))
	}
	dir = filepath.Join(string(d), dirRel)
	spec, err = filepath.Rel(dir, filepath.Join(string(d), rel))
	if err != nil {
		return "", "", fmt.Errorf("resolve %s: %w", path, err)
	}
	return dir, spec, nil
}

// maxDiffInput is how large a file Diff compares.
const maxDiffInput = 4 << 20

// sameAsHead reports whether the working file rel is, byte for byte, the
// blob HEAD has at repoPath. It compares git's object ids, computed here,
// for files too large to compare by content; git only reads the id.
func sameAsHead(ctx context.Context, dir string, root *os.Root, rel, repoPath string) (bool, error) {
	out, err := runGit(ctx, dir, MaxViewedBytes, "rev-parse", "--verify", "--quiet", "HEAD:"+repoPath)
	if err != nil {
		return false, err
	}
	want := strings.TrimSpace(out.text)
	var h hash.Hash
	switch len(want) {
	case 2 * sha1.Size:
		h = sha1.New()
	case 2 * sha256.Size:
		h = sha256.New()
	default:
		return false, fmt.Errorf("unexpected object id %q", want)
	}
	f, err := root.Open(rel)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return false, err
	}
	_, _ = fmt.Fprintf(h, "blob %d\x00", info.Size())
	if _, err := io.Copy(h, f); err != nil {
		return false, err
	}
	return hex.EncodeToString(h.Sum(nil)) == want, nil
}

// diffCopies diffs old against cur (nil: the file doesn't exist on that
// side) as git would for repoPath, outside any repository and without the
// user's git configuration.
func diffCopies(ctx context.Context, repoPath string, old, cur *string) (gitOutput, error) {
	tmp, err := os.MkdirTemp("", "agentapi-diff-")
	if err != nil {
		return gitOutput{}, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	side := func(name string, content *string) (string, error) {
		if content == nil {
			return os.DevNull, nil
		}
		path := filepath.Join(name, filepath.FromSlash(repoPath))
		full := filepath.Join(tmp, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			return "", err
		}
		return path, os.WriteFile(full, []byte(*content), 0o600)
	}
	a, err := side("a", old)
	if err != nil {
		return gitOutput{}, err
	}
	b, err := side("b", cur)
	if err != nil {
		return gitOutput{}, err
	}
	args := []string{
		"-c", "core.quotepath=off", "-c", "core.attributesFile=" + os.DevNull,
		"diff", "--no-index", "--no-color", "--no-ext-diff", "--no-textconv",
	}
	dir, rel := tmp, filepath.FromSlash(repoPath)
	switch {
	case old == nil:
		// From the copy's directory, git names both sides a/path and
		// b/path, as git diff does, rather than after the copy.
		dir, args = filepath.Join(tmp, "b"), append(args, "--", os.DevNull, rel)
	case cur == nil:
		dir, args = filepath.Join(tmp, "a"), append(args, "--", rel, os.DevNull)
	default:
		args = append(args, "--no-prefix", "--", a, b)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(gitEnv(),
		// Not the user's configuration, nor a repository the temporary
		// directory may be in.
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_CEILING_DIRECTORIES="+filepath.Dir(tmp))
	stdout := &cappedBuffer{max: MaxViewedBytes}
	var stderr bytes.Buffer
	cmd.Stdout = stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	// Exit status 1 is how diff --no-index says the files differ.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		err = nil
	}
	if msg := strings.TrimSpace(stderr.String()); err != nil && msg != "" {
		err = fmt.Errorf("%w: %s", err, msg)
	}
	return gitOutput{text: stdout.String(), truncated: stdout.dropped}, err
}

// gitOutput is what a git command printed, cut at its limit.
type gitOutput struct {
	text      string
	truncated bool
}

// gitEnv is git's environment: AgentAPI's, without its own variables (its
// API token among them), and without taking the index lock the agent may
// be using.
func gitEnv() []string {
	env := make([]string, 0, len(os.Environ())+2)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "AGENTAPI_") {
			env = append(env, kv)
		}
	}
	return append(env, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
}

// runGit runs git in dir and keeps up to limit bytes of what it prints.
func runGit(ctx context.Context, dir string, limit int, args ...string) (gitOutput, error) {
	args = append([]string{"-c", "core.quotepath=off", "-c", "core.fsmonitor=false", "-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = gitEnv()
	stdout := &cappedBuffer{max: limit}
	var stderr bytes.Buffer
	cmd.Stdout = stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if msg := strings.TrimSpace(stderr.String()); err != nil && msg != "" {
		err = fmt.Errorf("%w: %s", err, msg)
	}
	return gitOutput{text: stdout.String(), truncated: stdout.dropped}, err
}

// cappedBuffer keeps the first max bytes written to it and drops the rest,
// cut at a rune boundary.
type cappedBuffer struct {
	bytes.Buffer
	max     int
	dropped bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.Len(); room < len(p) {
		b.dropped = true
		if room > 0 {
			b.Buffer.Write(p[:room])
		}
		return len(p), nil
	}
	return b.Buffer.Write(p)
}

func (b *cappedBuffer) String() string {
	if b.dropped {
		return string(trimPartialRune(b.Bytes()))
	}
	return b.Buffer.String()
}
