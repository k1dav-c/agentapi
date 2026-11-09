package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/coder/agentapi/lib/htmlpreview"
	"github.com/coder/agentapi/lib/util"
	"github.com/coder/agentapi/lib/workspacefs"
)

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
	FileDiffModified  = FileDiffStatus(workspacefs.DiffModified)
	FileDiffAdded     = FileDiffStatus(workspacefs.DiffAdded)
	FileDiffDeleted   = FileDiffStatus(workspacefs.DiffDeleted)
	FileDiffUntracked = FileDiffStatus(workspacefs.DiffUntracked)
	FileDiffUnchanged = FileDiffStatus(workspacefs.DiffUnchanged)
	// FileDiffNotInRepo: the file isn't in a git repository, so there is
	// nothing to compare it with.
	FileDiffNotInRepo = FileDiffStatus(workspacefs.DiffNotInRepo)
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
// file viewer (lib/workspacefs): their text, their diff, a preview of an
// HTML file, an image, and the search for a path the agent named.
type fileAPI struct {
	cwd string
}

func (f *fileAPI) dir() workspacefs.Dir { return workspacefs.Dir(f.cwd) }

// fileError is err, a refusal of lib/workspacefs, as the error response the
// chat shows; other errors are returned as they are.
func fileError(err error) error {
	var refused *workspacefs.Error
	if !errors.As(err, &refused) {
		return err
	}
	switch refused.Kind {
	case workspacefs.Invalid:
		return huma.Error400BadRequest(refused.Message)
	case workspacefs.Forbidden:
		return huma.Error403Forbidden(refused.Message)
	case workspacefs.NotFound:
		return huma.Error404NotFound(refused.Message)
	}
	return err
}

// httpStatus is the status of an error made with huma, or 0.
func httpStatus(err error) int {
	var status huma.StatusError
	if errors.As(err, &status) {
		return status.GetStatus()
	}
	return 0
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

func (f *fileAPI) getFileContent(_ context.Context, input *FilePathQuery) (*FileContentResponse, error) {
	content, err := f.dir().Content(input.Path)
	if err != nil {
		return nil, fileError(err)
	}
	resp := &FileContentResponse{}
	resp.Body.Path = input.Path
	resp.Body.Relative = content.Relative
	resp.Body.Size = content.Size
	resp.Body.Content = content.Text
	resp.Body.Truncated = content.Truncated
	resp.Body.Binary = content.Binary
	return resp, nil
}

// getFileDiff serves a file's uncommitted changes against HEAD
// (workspacefs.Dir.Diff).
func (f *fileAPI) getFileDiff(ctx context.Context, input *FilePathQuery) (*FileDiffResponse, error) {
	diff, err := f.dir().Diff(ctx, input.Path)
	if err != nil {
		return nil, fileError(err)
	}
	resp := &FileDiffResponse{}
	resp.Body.Path = input.Path
	resp.Body.Relative = diff.Relative
	resp.Body.Status = FileDiffStatus(diff.Status)
	resp.Body.Diff = diff.Text
	resp.Body.Truncated = diff.Truncated
	return resp, nil
}

type FileFindResponse struct {
	Body struct {
		Matches []workspacefs.FileMatch `json:"matches" nullable:"false" doc:"Files whose path ends with the one asked for, the likeliest first."`
		// Complete is false when the search stopped at its bounds.
		Complete bool `json:"complete" doc:"The whole working directory was searched; false when the search stopped at its bounds (50,000 entries or 2 seconds)."`
	}
}

// FileMatch is a file GET /files/find found.

// findFile handles GET /files/find (workspacefs.Dir.Find).
func (f *fileAPI) findFile(ctx context.Context, input *FilePathQuery) (*FileFindResponse, error) {
	matches, complete, err := f.dir().Find(ctx, input.Path)
	if err != nil {
		return nil, fileError(err)
	}
	resp := &FileFindResponse{}
	resp.Body.Matches = matches
	resp.Body.Complete = complete
	return resp, nil
}

type FilePreviewResponse struct {
	Body struct {
		Path     string                `json:"path" doc:"The path as given."`
		Relative string                `json:"relative" doc:"The path relative to the agent's working directory."`
		HTML     string                `json:"html" doc:"The page, with the stylesheets, scripts, images, fonts and media it refers to by relative path inlined as data: URLs (up to 4 MiB each, 16 MiB in all), and in-memory localStorage, sessionStorage and document.cookie where the page can't use them: for an iframe's srcdoc, sandboxed without allow-same-origin."`
		Missing  []htmlpreview.Missing `json:"missing" nullable:"false" doc:"References that weren't inlined: not found, outside the working directory, hidden, of a type that doesn't fit their use, or too large."`
	}
}

// registerFilePreviewRoutes adds the routes that preview files: an HTML
// page, and the images of Markdown files and the file viewer.
func (s *Server) registerFilePreviewRoutes() {
	f := &fileAPI{cwd: s.cwd}
	huma.Get(s.api, "/files/preview", f.getFilePreview, func(o *huma.Operation) {
		o.Description = "Returns an HTML file in the agent's working directory as a page that shows by itself, for the chat's preview: the stylesheets, scripts, images, fonts and media it refers to by relative path are inlined. Show it in an iframe sandboxed without allow-same-origin, so its scripts get no access to the chat or the API."
		o.Errors = []int{400, 403, 404}
	})
	// The image itself, so not part of the OpenAPI schema.
	s.router.Get("/files/image", f.serveFileImage)
}

func (f *fileAPI) getFilePreview(ctx context.Context, input *FilePathQuery) (*FilePreviewResponse, error) {
	page, rel, err := f.dir().Preview(ctx, input.Path)
	if err != nil {
		return nil, fileError(err)
	}
	resp := &FilePreviewResponse{}
	resp.Body.Path = input.Path
	resp.Body.Relative = rel
	resp.Body.HTML = page.HTML
	resp.Body.Missing = page.Missing
	if resp.Body.Missing == nil {
		resp.Body.Missing = []htmlpreview.Missing{}
	}
	return resp, nil
}

// serveFileImage handles GET /files/image?path=: an image file in the
// agent's working directory, for the images of a Markdown preview and of
// the file viewer. Like GET /uploads it only serves images, which the
// browser is told not to sniff or run: raster images by their content, and
// SVG (which can carry script) only in a sandbox where nothing runs or
// loads.
func (f *fileAPI) serveFileImage(w http.ResponseWriter, r *http.Request) {
	given := r.URL.Query().Get("path")
	rel, err := f.dir().Resolve(given)
	if err != nil {
		writeStatusError(w, fileError(err))
		return
	}
	// Checked before it is opened: opening a FIFO would wait for a writer.
	file, info, err := f.dir().Open(given)
	if err != nil {
		writeStatusError(w, fileError(err))
		return
	}
	defer func() { _ = file.Close() }()

	contentType := ""
	if strings.EqualFold(path.Ext(rel), ".svg") {
		contentType = "image/svg+xml"
	} else {
		head := make([]byte, 512)
		n, err := io.ReadFull(file, head)
		if err != nil && n == 0 && !errors.Is(err, io.EOF) {
			http.Error(w, "failed to read the file", http.StatusInternalServerError)
			return
		}
		if sniffed := http.DetectContentType(head[:n]); previewableImageTypes[sniffed] || sniffed == "image/x-icon" {
			contentType = sniffed
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			http.Error(w, "failed to read the file", http.StatusInternalServerError)
			return
		}
	}
	if contentType == "" {
		http.Error(w, given+" isn't an image", http.StatusUnsupportedMediaType)
		return
	}
	header := w.Header()
	header.Set("Content-Type", contentType)
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	// Files change as the agent works: the browser checks every time
	// (Last-Modified), and keeps an image that didn't change.
	header.Set("Cache-Control", "private, no-cache")
	http.ServeContent(w, r, "", info.ModTime(), file)
}

// writeStatusError writes err as plain text with the status a huma error
// carries, or 500.
func writeStatusError(w http.ResponseWriter, err error) {
	status := httpStatus(err)
	if status == 0 {
		status = http.StatusInternalServerError
	}
	http.Error(w, err.Error(), status)
}
