package httpapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/coder/agentapi/lib/jsonlwatcher"
)

// maxUploadSize is the largest file POST /upload takes.
const maxUploadSize = 10 << 20 // 10MB

// serveUpload handles GET /uploads/{checksum}/{name}: an image uploaded
// with POST /upload, or one from a tool result, so the chat can show it.
func (s *Server) serveUpload(w http.ResponseWriter, r *http.Request) {
	s.uploads.serve(w, r, chi.URLParam(r, "checksum"), chi.URLParam(r, "name"))
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
			url, err := s.uploads.saveImage(image.Data)
			if err != nil {
				s.logger.Debug("Failed to store tool result image", "tool_use_id", block.ToolUseID, "error", err)
				continue
			}
			stored = append(stored, jsonlwatcher.RichImage{MediaType: image.MediaType, URL: url})
		}
		block.Images = stored
	}
}

// uploadFiles handles POST /upload
func (s *Server) uploadFiles(ctx context.Context, input *struct {
	RawBody huma.MultipartFormFiles[UploadRequest]
},
) (*UploadResponse, error) {
	formData := input.RawBody.Data()
	buf, err := io.ReadAll(io.LimitReader(formData.File.File, maxUploadSize+1))
	if err != nil {
		return nil, fmt.Errorf("failed to upload file: %w", err)
	}
	if len(buf) > maxUploadSize {
		return nil, huma.Error400BadRequest("file size exceeds 10MB limit")
	}
	// Only the base name of what the browser sent.
	outPath, _, err := s.uploads.save(filepath.Base(formData.File.Filename), buf)
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
	switch removed, err := s.uploads.remove(); {
	case err != nil:
		s.logger.Error("Failed to clean up upload directory", "uploadDir", s.uploads.dir, "error", err)
	case removed:
		s.logger.Info("Cleaned up upload directory", "uploadDir", s.uploads.dir)
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
