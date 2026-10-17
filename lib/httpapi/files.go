package httpapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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

func (s *Server) registerFileRoutes() {
	huma.Get(s.api, "/files/content", s.getFileContent, func(o *huma.Operation) {
		o.Description = "Returns a file the chat shows, for the file paths in tool calls and messages: its text, up to 1 MiB. Only files in the agent's working directory can be read; a binary file comes back without content."
		o.Errors = []int{400, 403, 404}
	})
	huma.Get(s.api, "/files/diff", s.getFileDiff, func(o *huma.Operation) {
		o.Description = "Returns the uncommitted changes of a file in the agent's working directory: git's unified diff of the working tree against HEAD, where an untracked file shows as added. Outside a git repository the status is not_in_repo."
		o.Errors = []int{400, 403, 404}
	})
}

// workspacePath resolves path, absolute or relative to the agent's working
// directory, to a path relative to that directory. Paths outside it are
// refused: these endpoints show the agent's work, not the machine.
func (s *Server) workspacePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", huma.Error400BadRequest("path is empty")
	}
	if s.cwd == "" {
		return "", huma.Error404NotFound("the agent's working directory is unknown")
	}
	rel := filepath.Clean(path)
	if filepath.IsAbs(rel) {
		rel = relativeTo(s.cwd, rel)
		if rel == "" {
			// The agent may report the path with symlinks resolved.
			if real, err := filepath.EvalSymlinks(s.cwd); err == nil {
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

func (s *Server) getFileContent(_ context.Context, input *FilePathQuery) (*FileContentResponse, error) {
	rel, err := s.workspacePath(input.Path)
	if err != nil {
		return nil, err
	}
	// The root keeps symlinks inside the working directory from leading out.
	root, err := os.OpenRoot(s.cwd)
	if err != nil {
		return nil, fmt.Errorf("open the agent's working directory: %w", err)
	}
	defer func() { _ = root.Close() }()
	f, err := root.Open(rel)
	if err != nil {
		return nil, fileError(input.Path, err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, fileError(input.Path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, huma.Error400BadRequest(fmt.Sprintf("%s is not a file", input.Path))
	}
	data, err := io.ReadAll(io.LimitReader(f, maxViewedBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", input.Path, err)
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

func (s *Server) getFileDiff(ctx context.Context, input *FilePathQuery) (*FileDiffResponse, error) {
	rel, err := s.workspacePath(input.Path)
	if err != nil {
		return nil, err
	}
	resp := &FileDiffResponse{}
	resp.Body.Path = input.Path
	resp.Body.Relative = filepath.ToSlash(rel)

	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	abs := filepath.Join(s.cwd, rel)
	// git runs in the closest directory that exists: a deleted file's
	// directory may be gone too.
	dir := filepath.Dir(abs)
	for dir != s.cwd && !isDir(dir) {
		dir = filepath.Dir(dir)
	}
	spec, err := filepath.Rel(dir, abs)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", input.Path, err)
	}
	// The directory's path in the repository, e.g. "app/".
	prefix, err := runGit(ctx, dir, "rev-parse", "--show-prefix")
	if err != nil {
		resp.Body.Status = FileDiffNotInRepo
		return resp, nil
	}
	_, statErr := os.Lstat(abs)
	exists := statErr == nil
	_, headErr := runGit(ctx, dir, "rev-parse", "--verify", "--quiet", "HEAD")
	_, trackedErr := runGit(ctx, dir, "ls-files", "--error-unmatch", "--", ":(literal)"+spec)
	tracked := trackedErr == nil

	var out gitOutput
	switch {
	case tracked && headErr == nil:
		out, err = gitDiff(ctx, dir, "HEAD", "--", ":(literal)"+spec)
		resp.Body.Status = diffStatus(out.text)
	case exists:
		// Untracked, or staged in a repository without commits: all of it
		// is new. The prefixes name it by its path in the repository, as
		// the diff of a tracked file does.
		repoDir := strings.TrimSpace(prefix.text)
		out, err = gitDiff(ctx, dir, "--no-index", "--src-prefix=a/"+repoDir, "--dst-prefix=b/"+repoDir, "--", os.DevNull, spec)
		resp.Body.Status = FileDiffUntracked
		if tracked {
			resp.Body.Status = FileDiffAdded
		}
	default:
		return nil, huma.Error404NotFound(fmt.Sprintf("%s doesn't exist", input.Path))
	}
	if err != nil {
		return nil, fmt.Errorf("git diff %s: %w", input.Path, err)
	}
	resp.Body.Diff = out.text
	resp.Body.Truncated = out.truncated
	return resp, nil
}

// diffStatus reads how a file changed from its diff against HEAD.
func diffStatus(diff string) FileDiffStatus {
	switch {
	case diff == "":
		return FileDiffUnchanged
	case strings.Contains(diff, "\nnew file mode "):
		return FileDiffAdded
	case strings.Contains(diff, "\ndeleted file mode "):
		return FileDiffDeleted
	}
	return FileDiffModified
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// gitOutput is what a git command printed, cut at maxViewedBytes.
type gitOutput struct {
	text      string
	truncated bool
}

// runGit runs git in dir without taking the index lock the agent may be
// using.
func runGit(ctx context.Context, dir string, args ...string) (gitOutput, error) {
	args = append([]string{"-c", "core.quotepath=off", "-c", "core.fsmonitor=false", "-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	stdout := &cappedBuffer{max: maxViewedBytes}
	var stderr bytes.Buffer
	cmd.Stdout = stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if msg := strings.TrimSpace(stderr.String()); err != nil && msg != "" {
		err = fmt.Errorf("%w: %s", err, msg)
	}
	return gitOutput{text: stdout.String(), truncated: stdout.dropped}, err
}

// gitDiff runs git diff in dir without the programs a repository can
// configure for it (external diff drivers, textconv filters). Exit status 1
// is how `diff --no-index` says the files differ, not an error.
func gitDiff(ctx context.Context, dir string, args ...string) (gitOutput, error) {
	out, err := runGit(ctx, dir, append([]string{"diff", "--no-color", "--no-ext-diff", "--no-textconv"}, args...)...)
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 && slices.Contains(args, "--no-index") {
		err = nil
	}
	return out, err
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
