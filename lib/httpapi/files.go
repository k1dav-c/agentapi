package httpapi

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
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"

	"github.com/coder/agentapi/lib/util"
)

// maxViewedBytes is how much of a file GET /files/content returns, and of
// a diff GET /files/diff returns.
const maxViewedBytes = 1 << 20

// gitTimeout bounds the git commands of GET /files/diff.
const gitTimeout = 10 * time.Second

// FilePathQuery names a file the chat shows.
type FilePathQuery struct {
	Path string `query:"path" required:"true" doc:"The file: an absolute path inside the agent's working directory, or a path relative to it."`
}

type FileContentResponse struct {
	Body struct {
		Path      string `json:"path" doc:"The path as given."`
		Relative  string `json:"relative" doc:"The path relative to the agent's working directory."`
		Size      int64  `json:"size" doc:"Size of the file in bytes."`
		Content   string `json:"content" doc:"The file's text, up to 1 MiB; empty for a binary file."`
		Truncated bool   `json:"truncated" doc:"The content was cut at 1 MiB."`
		Binary    bool   `json:"binary" doc:"The file isn't UTF-8 text, so it has no content here."`
	}
}

// FileDiffStatus is how a file differs from the last commit.
type FileDiffStatus string

const (
	FileDiffModified  FileDiffStatus = "modified"
	FileDiffAdded     FileDiffStatus = "added"
	FileDiffDeleted   FileDiffStatus = "deleted"
	FileDiffUntracked FileDiffStatus = "untracked"
	FileDiffUnchanged FileDiffStatus = "unchanged"
	// FileDiffNotInRepo: the file isn't in a git repository, so there is
	// nothing to compare it with.
	FileDiffNotInRepo FileDiffStatus = "not_in_repo"
)

var FileDiffStatusValues = []FileDiffStatus{
	FileDiffModified, FileDiffAdded, FileDiffDeleted, FileDiffUntracked, FileDiffUnchanged, FileDiffNotInRepo,
}

func (s FileDiffStatus) Schema(r huma.Registry) *huma.Schema {
	return util.OpenAPISchema(r, "FileDiffStatus", FileDiffStatusValues)
}

type FileDiffResponse struct {
	Body struct {
		Path      string         `json:"path" doc:"The path as given."`
		Relative  string         `json:"relative" doc:"The path relative to the agent's working directory."`
		Status    FileDiffStatus `json:"status" doc:"modified, added (staged but not committed), deleted, untracked, unchanged, or not_in_repo when the file isn't in a git repository."`
		Diff      string         `json:"diff" doc:"Unified diff of the working tree against HEAD (an untracked file shows as added), up to 1 MiB; empty when unchanged or not in a repository."`
		Truncated bool           `json:"truncated" doc:"The diff was cut at 1 MiB."`
	}
}

// fileAPI serves the files of the agent's working directory to the chat's
// file viewer: their text, their diff, a preview of an HTML file, an image,
// and the search for a path the agent named.
type fileAPI struct {
	cwd string
}

func (s *Server) registerFileRoutes() {
	f := &fileAPI{cwd: s.cwd}
	huma.Get(s.api, "/files/content", f.getFileContent, func(o *huma.Operation) {
		o.Description = "Returns a file the chat shows, for the file paths in tool calls and messages: its text, up to 1 MiB. Only files in the agent's working directory can be read; a binary file comes back without content."
		o.Errors = []int{400, 403, 404}
	})
	huma.Get(s.api, "/files/find", f.findFile, func(o *huma.Operation) {
		o.Description = "Finds the files under the agent's working directory whose path ends with the one given, ignoring case: for a path the agent wrote relative to a repository below the working directory, or in the wrong case. Hidden directories and dependencies (node_modules, vendor, build output) are searched only when the path goes into them, and the search stops after 50,000 entries or 2 seconds. Returns up to 10 matches, those with the same case first, then the latest changed."
		o.Errors = []int{400, 404}
	})
	huma.Get(s.api, "/files/diff", f.getFileDiff, func(o *huma.Operation) {
		o.Description = "Returns the uncommitted changes of a file in the agent's working directory: git's unified diff of the working tree against HEAD, where an untracked file shows as added. Outside a git repository the status is not_in_repo."
		o.Errors = []int{400, 403, 404}
	})
}

// workspacePath resolves path, absolute or relative to the agent's working
// directory, to a path relative to that directory. Paths outside it are
// refused: these endpoints show the agent's work, not the machine.
func (f *fileAPI) workspacePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", huma.Error400BadRequest("path is empty")
	}
	if f.cwd == "" {
		return "", huma.Error404NotFound("the agent's working directory is unknown")
	}
	rel := filepath.Clean(path)
	if filepath.IsAbs(rel) {
		rel = relativeTo(f.cwd, rel)
		if rel == "" {
			// The agent may report the path with symlinks resolved.
			if real, err := filepath.EvalSymlinks(f.cwd); err == nil {
				rel = relativeTo(real, filepath.Clean(path))
			}
		}
	}
	if rel == "" || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", huma.Error403Forbidden(fmt.Sprintf("%s is outside the agent's working directory", path))
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

func (f *fileAPI) getFileContent(_ context.Context, input *FilePathQuery) (*FileContentResponse, error) {
	rel, err := f.workspacePath(input.Path)
	if err != nil {
		return nil, err
	}
	// The root keeps symlinks inside the working directory from leading out.
	root, err := os.OpenRoot(f.cwd)
	if err != nil {
		return nil, fmt.Errorf("open the agent's working directory: %w", err)
	}
	defer func() { _ = root.Close() }()
	data, info, err := readRegular(root, rel, input.Path, maxViewedBytes)
	if err != nil {
		return nil, err
	}

	resp := &FileContentResponse{}
	resp.Body.Path = input.Path
	resp.Body.Relative = filepath.ToSlash(rel)
	resp.Body.Size = info.Size()
	if len(data) > maxViewedBytes {
		data = trimPartialRune(data[:maxViewedBytes])
		resp.Body.Truncated = true
	}
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		resp.Body.Binary = true
		return resp, nil
	}
	resp.Body.Content = string(data)
	return resp, nil
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
		return nil, nil, huma.Error400BadRequest(fmt.Sprintf("%s is not a file", path))
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

// httpStatus is the status of an error made with huma, or 0.
func httpStatus(err error) int {
	var status huma.StatusError
	if errors.As(err, &status) {
		return status.GetStatus()
	}
	return 0
}

// fileError maps an error opening a file in the working directory to the
// response the chat shows.
func fileError(path string, err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return huma.Error404NotFound(fmt.Sprintf("%s doesn't exist", path))
	}
	// A symlink leading out of the working directory, or no permission.
	return huma.Error403Forbidden(fmt.Sprintf("%s can't be read: %v", path, err))
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

// getFileDiff serves a file's uncommitted changes against HEAD. In the
// repository git only reads objects and the index (rev-parse, ls-files,
// cat-file), which runs none of the programs a repository can configure,
// such as clean filters (an agent that can write .git/config could run a
// command when the user looks at a diff). The diff compares two copies in a
// directory of its own; the working copy is read through the root, so a
// symlink can't lead out.
func (f *fileAPI) getFileDiff(ctx context.Context, input *FilePathQuery) (*FileDiffResponse, error) {
	rel, err := f.workspacePath(input.Path)
	if err != nil {
		return nil, err
	}
	resp := &FileDiffResponse{}
	resp.Body.Path = input.Path
	resp.Body.Relative = filepath.ToSlash(rel)
	root, err := os.OpenRoot(f.cwd)
	if err != nil {
		return nil, fmt.Errorf("open the agent's working directory: %w", err)
	}
	defer func() { _ = root.Close() }()

	// The working copy, unless it is gone.
	current, _, err := readRegular(root, rel, input.Path, maxDiffInput)
	exists := err == nil
	if err != nil && httpStatus(err) != http.StatusNotFound {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	// git runs in the closest directory that exists (a deleted file's
	// directory may be gone too), which must be inside the working
	// directory itself, not reached through a symlink.
	dirRel := filepath.Dir(rel)
	for dirRel != "." {
		if info, err := root.Stat(dirRel); err == nil && info.IsDir() {
			break
		}
		dirRel = filepath.Dir(dirRel)
	}
	if info, err := root.Lstat(dirRel); err != nil || !info.IsDir() {
		return nil, huma.Error403Forbidden(fmt.Sprintf("%s is outside the agent's working directory", input.Path))
	}
	dir := filepath.Join(f.cwd, dirRel)
	spec, err := filepath.Rel(dir, filepath.Join(f.cwd, rel))
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", input.Path, err)
	}
	// The directory's path in the repository, e.g. "app/".
	prefix, err := runGit(ctx, dir, maxViewedBytes, "rev-parse", "--show-prefix")
	if err != nil {
		resp.Body.Status = FileDiffNotInRepo
		return resp, nil
	}
	repoPath := strings.TrimSpace(prefix.text) + filepath.ToSlash(spec)
	head, headErr := runGit(ctx, dir, maxDiffInput, "cat-file", "blob", "HEAD:"+repoPath)
	inHead := headErr == nil
	_, trackedErr := runGit(ctx, dir, maxViewedBytes, "ls-files", "--error-unmatch", "--", ":(literal)"+spec)

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
		resp.Body.Status = FileDiffModified
		same := *old == *cur
		if head.truncated || len(current) > maxDiffInput {
			// Too large to compare here: compare git's object ids.
			same, _ = sameAsHead(ctx, dir, root, rel, repoPath)
		}
		if same {
			resp.Body.Status = FileDiffUnchanged
			return resp, nil
		}
	case inHead:
		resp.Body.Status = FileDiffDeleted
	case exists && trackedErr == nil:
		// Staged, in a repository without commits too.
		resp.Body.Status = FileDiffAdded
	case exists:
		resp.Body.Status = FileDiffUntracked
	default:
		return nil, huma.Error404NotFound(fmt.Sprintf("%s doesn't exist", input.Path))
	}
	if head.truncated || len(current) > maxDiffInput {
		// Too large to diff here.
		resp.Body.Truncated = true
		return resp, nil
	}
	out, err := diffCopies(ctx, repoPath, old, cur)
	if err != nil {
		return nil, fmt.Errorf("git diff %s: %w", input.Path, err)
	}
	resp.Body.Diff = out.text
	resp.Body.Truncated = out.truncated
	return resp, nil
}

// maxDiffInput is how large a file GET /files/diff compares.
const maxDiffInput = 4 << 20

// sameAsHead reports whether the working file rel is, byte for byte, the
// blob HEAD has at repoPath. It compares git's object ids, computed here,
// for files too large to compare by content; git only reads the id.
func sameAsHead(ctx context.Context, dir string, root *os.Root, rel, repoPath string) (bool, error) {
	out, err := runGit(ctx, dir, maxViewedBytes, "rev-parse", "--verify", "--quiet", "HEAD:"+repoPath)
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
	stdout := &cappedBuffer{max: maxViewedBytes}
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
