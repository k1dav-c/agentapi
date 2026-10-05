package httpapi

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"regexp"

	"github.com/go-chi/chi/v5"
)

// uploadChecksumRe matches the directory POST /upload stores a file in:
// the first 8 bytes of its SHA-256, in hex.
var uploadChecksumRe = regexp.MustCompile(`^[0-9a-f]{16}$`)

// previewableImageTypes are the uploaded files GET /uploads serves, by
// sniffed content type. SVG is left out (it can carry script), as is
// everything that isn't an image.
var previewableImageTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
	"image/bmp":  true,
}

// serveUpload handles GET /uploads/{checksum}/{name}: an image uploaded
// with POST /upload, so the chat can show it.
//
// The chat can tell the agent to run commands, so whatever this serves
// runs with that power if it is ever rendered as a page: it only reads
// this server's upload directory (through os.Root, so symlinks can't lead
// out of it), only serves files whose content is a raster image, and tells
// the browser not to sniff or run anything.
func (s *Server) serveUpload(w http.ResponseWriter, r *http.Request) {
	checksum := chi.URLParam(r, "checksum")
	name := chi.URLParam(r, "name")
	if !uploadChecksumRe.MatchString(checksum) || name == "" || name != path.Base(name) || name == "." || name == ".." {
		http.NotFound(w, r)
		return
	}
	if s.tempDir == "" {
		http.NotFound(w, r)
		return
	}
	root, err := os.OpenRoot(s.tempDir)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer func() { _ = root.Close() }()
	f, err := root.Open(path.Join(checksum, name))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}

	head := make([]byte, 512)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		http.Error(w, "failed to read file", http.StatusInternalServerError)
		return
	}
	contentType := http.DetectContentType(head[:n])
	if !previewableImageTypes[contentType] {
		http.Error(w, "only uploaded images can be previewed", http.StatusUnsupportedMediaType)
		return
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		http.Error(w, "failed to read file", http.StatusInternalServerError)
		return
	}

	header := w.Header()
	header.Set("Content-Type", contentType)
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	// The directory name is the content's checksum, so the file at a URL
	// doesn't change.
	header.Set("Cache-Control", "private, max-age=86400, immutable")
	http.ServeContent(w, r, name, info.ModTime(), f)
}
