package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/coder/agentapi/lib/jsonlwatcher"
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
	if s.uploadDir == "" {
		http.NotFound(w, r)
		return
	}
	root, err := os.OpenRoot(s.uploadDir)
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

// storeRichImages saves the images in msg's tool results (such as an image
// file the agent read) to the upload directory and sets their URL, so the
// chat can show them through GET /uploads without carrying the image in
// every message. Images whose content isn't a previewable raster image, or
// that can't be saved, are dropped.
func (s *Server) storeRichImages(msg *jsonlwatcher.RichMessage) {
	for i := range msg.Content {
		block := &msg.Content[i]
		if len(block.Images) == 0 {
			continue
		}
		var stored []jsonlwatcher.RichImage
		for _, image := range block.Images {
			url, err := s.storeImage(image.Data)
			if err != nil {
				s.logger.Debug("Failed to store tool result image", "tool_use_id", block.ToolUseID, "error", err)
				continue
			}
			stored = append(stored, jsonlwatcher.RichImage{MediaType: image.MediaType, URL: url})
		}
		block.Images = stored
	}
}

// imageExtensions names a stored image by its sniffed content type.
var imageExtensions = map[string]string{
	"image/png":  "png",
	"image/jpeg": "jpg",
	"image/gif":  "gif",
	"image/webp": "webp",
	"image/bmp":  "bmp",
}

// storeImage saves data as <upload dir>/<checksum>/image.<ext>, the layout
// POST /upload uses, and returns the path GET /uploads serves it at.
func (s *Server) storeImage(data []byte) (string, error) {
	contentType := http.DetectContentType(data)
	if !previewableImageTypes[contentType] {
		return "", fmt.Errorf("not a previewable image: %s", contentType)
	}
	if s.uploadDir == "" {
		return "", errors.New("no upload directory")
	}
	hash := sha256.Sum256(data)
	checksum := hex.EncodeToString(hash[:8])
	name := "image." + imageExtensions[contentType]
	root, err := os.OpenRoot(s.uploadDir)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()
	if err := root.Mkdir(checksum, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", err
	}
	file := path.Join(checksum, name)
	// The name is the content's checksum: a file already there is this image.
	if _, err := root.Stat(file); err == nil {
		return "/uploads/" + checksum + "/" + name, nil
	}
	f, err := root.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return "/uploads/" + checksum + "/" + name, nil
}

// uploadFiles handles POST /upload
func (s *Server) uploadFiles(ctx context.Context, input *struct {
	RawBody huma.MultipartFormFiles[UploadRequest]
},
) (*UploadResponse, error) {
	formData := input.RawBody.Data()

	file := formData.File.File

	// Limit file size to 10MB
	const maxFileSize = 10 << 20 // 10MB
	buf, err := io.ReadAll(io.LimitReader(file, maxFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("failed to upload file: %w", err)
	}
	if len(buf) > maxFileSize {
		return nil, huma.Error400BadRequest("file size exceeds 10MB limit")
	}

	// Calculate checksum of the uploaded file to create unique subdirectory
	hash := sha256.Sum256(buf)
	checksum := hex.EncodeToString(hash[:8]) // Use first 8 bytes (16 hex chars)

	// Create checksum-based subdirectory in the upload directory
	dir := filepath.Join(s.uploadDir, checksum)
	err = os.MkdirAll(dir, 0o755)
	if err != nil {
		return nil, fmt.Errorf("failed to create upload directory: %w", err)
	}

	// Save individual file with original filename (extract just the base filename for security)
	filename := filepath.Base(formData.File.Filename)

	outPath := filepath.Join(dir, filename)
	err = os.WriteFile(outPath, buf, 0o644)
	if err != nil {
		return nil, fmt.Errorf("failed to write file: %w", err)
	}

	resp := &UploadResponse{}
	resp.Body.Ok = true
	resp.Body.FilePath = outPath
	return resp, nil
}

// cleanupUploadDir removes the upload directory and all its contents, unless
// the conversation that refers to them is kept.
func (s *Server) cleanupUploadDir() {
	if s.keepUploads {
		return
	}
	if err := os.RemoveAll(s.uploadDir); err != nil {
		s.logger.Error("Failed to clean up upload directory", "uploadDir", s.uploadDir, "error", err)
	} else {
		s.logger.Info("Cleaned up upload directory", "uploadDir", s.uploadDir)
	}
}

func (s *Server) registerUploadRoutes() {
	huma.Post(s.api, "/upload", s.uploadFiles, func(o *huma.Operation) {
		o.Description = "Upload files to the specified upload path."
	})

	// Uploaded images, for the chat to show (not part of the OpenAPI
	// schema: the response is the image itself).
	s.router.Get("/uploads/{checksum}/{name}", s.serveUpload)
}
