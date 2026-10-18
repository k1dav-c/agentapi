package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/coder/agentapi/lib/htmlpreview"
)

// maxPreviewPage is how large an HTML file GET /files/preview shows.
const maxPreviewPage = 4 << 20

type FilePreviewResponse struct {
	Body struct {
		Path     string                `json:"path" doc:"The path as given."`
		Relative string                `json:"relative" doc:"The path relative to the agent's working directory."`
		HTML     string                `json:"html" doc:"The page, with the stylesheets, scripts, images, fonts and media it refers to by relative path inlined as data: URLs (up to 4 MiB each, 16 MiB in all), and in-memory localStorage, sessionStorage and document.cookie where the page can't use them: for an iframe's srcdoc, sandboxed without allow-same-origin."`
		Missing  []htmlpreview.Missing `json:"missing" doc:"References that weren't inlined: not found, outside the working directory, hidden, of a type that doesn't fit their use, or too large."`
	}
}

// registerFilePreviewRoutes adds the routes that preview files: an HTML
// page, and the images of Markdown files and the file viewer.
func (s *Server) registerFilePreviewRoutes() {
	huma.Get(s.api, "/files/preview", s.getFilePreview, func(o *huma.Operation) {
		o.Description = "Returns an HTML file in the agent's working directory as a page that shows by itself, for the chat's preview: the stylesheets, scripts, images, fonts and media it refers to by relative path are inlined. Show it in an iframe sandboxed without allow-same-origin, so its scripts get no access to the chat or the API."
		o.Errors = []int{400, 403, 404}
	})
	// The image itself, so not part of the OpenAPI schema.
	s.router.Get("/files/image", s.serveFileImage)
}

func (s *Server) getFilePreview(_ context.Context, input *FilePathQuery) (*FilePreviewResponse, error) {
	rel, err := s.workspacePath(input.Path)
	if err != nil {
		return nil, err
	}
	if ext := strings.ToLower(filepath.Ext(rel)); ext != ".html" && ext != ".htm" {
		return nil, huma.Error400BadRequest(fmt.Sprintf("%s isn't an HTML file", input.Path))
	}
	// The root keeps the page and what it refers to inside the working
	// directory, symlinks included.
	root, err := os.OpenRoot(s.cwd)
	if err != nil {
		return nil, fmt.Errorf("open the agent's working directory: %w", err)
	}
	defer func() { _ = root.Close() }()
	data, _, err := readRegular(root, rel, input.Path, maxPreviewPage)
	if err != nil {
		return nil, err
	}
	if len(data) > maxPreviewPage {
		return nil, huma.Error400BadRequest(fmt.Sprintf("%s is larger than %d MiB, too large to preview", input.Path, maxPreviewPage>>20))
	}
	page, err := htmlpreview.Inline(root.FS(), filepath.ToSlash(rel), data)
	if err != nil {
		return nil, fmt.Errorf("preview %s: %w", input.Path, err)
	}
	resp := &FilePreviewResponse{}
	resp.Body.Path = input.Path
	resp.Body.Relative = filepath.ToSlash(rel)
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
func (s *Server) serveFileImage(w http.ResponseWriter, r *http.Request) {
	given := r.URL.Query().Get("path")
	rel, err := s.workspacePath(given)
	if err != nil {
		writeStatusError(w, err)
		return
	}
	root, err := os.OpenRoot(s.cwd)
	if err != nil {
		http.Error(w, "the agent's working directory can't be opened", http.StatusInternalServerError)
		return
	}
	defer func() { _ = root.Close() }()
	info, err := root.Stat(rel)
	if err != nil {
		writeStatusError(w, fileError(given, err))
		return
	}
	if !info.Mode().IsRegular() {
		// Checked before it is opened: opening a FIFO would wait for a
		// writer.
		http.Error(w, given+" is not a file", http.StatusBadRequest)
		return
	}
	f, err := root.Open(rel)
	if err != nil {
		writeStatusError(w, fileError(given, err))
		return
	}
	defer func() { _ = f.Close() }()

	contentType := ""
	if strings.EqualFold(path.Ext(rel), ".svg") {
		contentType = "image/svg+xml"
	} else {
		head := make([]byte, 512)
		n, err := io.ReadFull(f, head)
		if err != nil && n == 0 && !errors.Is(err, io.EOF) {
			http.Error(w, "failed to read the file", http.StatusInternalServerError)
			return
		}
		if sniffed := http.DetectContentType(head[:n]); previewableImageTypes[sniffed] || sniffed == "image/x-icon" {
			contentType = sniffed
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
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
	http.ServeContent(w, r, "", info.ModTime(), f)
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
