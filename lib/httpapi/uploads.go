package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/coder/agentapi/lib/transcript"
)

// maxUploadSize is the largest file POST /upload takes.
const maxUploadSize = 100 << 20 // 100MB

// uploadTooLarge is the error for a larger file.
var uploadTooLarge = fmt.Sprintf("file size exceeds %dMB limit", maxUploadSize>>20)

// serveUpload handles GET /uploads/{checksum}/{name}: an image uploaded
// with POST /upload, or one from a tool result, so the chat can show it.
func (u *uploadAPI) serveUpload(w http.ResponseWriter, r *http.Request) {
	u.uploads.serve(w, r, chi.URLParam(r, "checksum"), chi.URLParam(r, "name"))
}

// storeRichImages saves the images in msg's tool results (such as an image
// file the agent read) to the upload directory and sets their URL, so the
// chat can show them through GET /uploads without carrying the image in
// every message. Images whose content isn't a previewable raster image, or
// that can't be saved, are dropped.
func (s *Server) storeRichImages(msg *transcript.RichMessage) {
	s.uploads.storeRichImages(msg, s.logger)
}

// storeRichImages is Server.storeRichImages; logger notes the images that
// can't be stored.
func (u *uploadStore) storeRichImages(msg *transcript.RichMessage, logger *slog.Logger) {
	cloned := false
	for i := range msg.Content {
		if len(msg.Content[i].Images) == 0 {
			continue
		}
		// The parser may share the content with a message it sent before,
		// which the emitter keeps and others read: change a copy.
		if !cloned {
			msg.Content = slices.Clone(msg.Content)
			cloned = true
		}
		block := &msg.Content[i]
		var stored []transcript.RichImage
		for _, image := range block.Images {
			url, err := u.saveImage(image.Data)
			if err != nil {
				logger.Debug("Failed to store tool result image", "tool_use_id", block.ToolUseID, "error", err)
				continue
			}
			stored = append(stored, transcript.RichImage{MediaType: image.MediaType, URL: url})
		}
		block.Images = stored
	}
}

// uploadFormOverhead is how much a POST /upload body may hold besides the
// file: the form's boundaries and part headers.
const uploadFormOverhead = 1 << 20

// uploadInput is the input of POST /upload.
type uploadInput struct {
	RawBody huma.MultipartFormFiles[UploadRequest]
}

// upload handles POST /upload.
func (u *uploadAPI) upload(_ context.Context, input *uploadInput) (*UploadResponse, error) {
	// The parts too large for memory went to temporary files, which
	// net/http removes only for its own copy of the request.
	defer func() { _ = input.RawBody.Form.RemoveAll() }()
	file := input.RawBody.Data().File
	defer func() { _ = file.Close() }()
	return u.saveUpload(file.Filename, file)
}

// saveUpload stores an uploaded file under the base name of the one the
// browser sent (uploadName).
func (u *uploadAPI) saveUpload(sent string, file io.Reader) (*UploadResponse, error) {
	name, ok := uploadName(sent)
	if !ok {
		return nil, huma.Error400BadRequest("the file has no name")
	}
	outPath, _, err := u.uploads.saveFrom(name, file, maxUploadSize)
	if errors.Is(err, errUploadTooLarge) {
		return nil, huma.Error400BadRequest(uploadTooLarge)
	}
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

// uploadAPI stores what the user uploads and serves uploaded images.
type uploadAPI struct {
	uploads *uploadStore
}

func (s *Server) registerUploadRoutes() {
	u := &uploadAPI{uploads: s.uploads}
	huma.Post(s.api, "/upload", u.upload, func(o *huma.Operation) {
		o.Description = "Upload files to the specified upload path."
		// A larger body would only be parsed into temporary files to be
		// refused.
		o.MaxBodyBytes = maxUploadSize + uploadFormOverhead
	})

	// Uploaded images, for the chat to show (not part of the OpenAPI
	// schema: the response is the image itself).
	s.router.Get("/uploads/{checksum}/{name}", u.serveUpload)
}
