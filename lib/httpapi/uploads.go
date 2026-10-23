package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/coder/agentapi/lib/transcript"
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
func (s *Server) storeRichImages(msg *transcript.RichMessage) {
	for i := range msg.Content {
		block := &msg.Content[i]
		if len(block.Images) == 0 {
			continue
		}
		var stored []transcript.RichImage
		for _, image := range block.Images {
			url, err := s.uploads.saveImage(image.Data)
			if err != nil {
				s.logger.Debug("Failed to store tool result image", "tool_use_id", block.ToolUseID, "error", err)
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

// serveUploadRequest answers POST /upload, as the operation's middleware,
// which never passes the request on to huma's handler. huma (v2.32)
// decodes a multipart body into its struct by adding the struct's fields to
// the operation's parameters on every request: a data race between
// requests, and each upload added a parameter to the OpenAPI schema. (huma
// fixed it in v2.37, which needs Go 1.25.) The operation stays registered
// with huma for the schema, and the responses are written as huma writes
// them.
func (s *Server) serveUploadRequest(ctx huma.Context, _ func(huma.Context)) {
	r, w := humachi.Unwrap(ctx)
	// A larger body would only be parsed into temporary files to be
	// refused.
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize+uploadFormOverhead)
	if err := r.ParseMultipartForm(humachi.MultipartMaxMemory); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			_ = huma.WriteErr(s.api, ctx, http.StatusBadRequest, "file size exceeds 10MB limit")
			return
		}
		s.uploadInvalid(ctx, "body", "cannot read multipart form: "+err.Error())
		return
	}
	files := r.MultipartForm.File["file"]
	switch len(files) {
	case 0:
		s.uploadInvalid(ctx, "file", "File required")
		return
	case 1:
	default:
		s.uploadInvalid(ctx, "file", "Multiple files received but only one was expected")
		return
	}
	file, err := files[0].Open()
	if err != nil {
		s.uploadInvalid(ctx, "file", "Failed to open file")
		return
	}
	defer func() { _ = file.Close() }()
	resp, err := s.saveUpload(files[0].Filename, file)
	var statusErr huma.StatusError
	switch {
	case errors.As(err, &statusErr):
		_ = huma.WriteErr(s.api, ctx, statusErr.GetStatus(), statusErr.Error())
	case err != nil:
		_ = huma.WriteErr(s.api, ctx, http.StatusInternalServerError, "unexpected error occurred", err)
	default:
		s.writeHumaResponse(ctx, http.StatusOK, resp.Body)
	}
}

// uploadInvalid answers an upload whose form doesn't fit, as huma's
// validation does.
func (s *Server) uploadInvalid(ctx huma.Context, location, message string) {
	_ = huma.WriteErr(s.api, ctx, http.StatusUnprocessableEntity, "validation failed", &huma.ErrorDetail{Location: location, Message: message})
}

// writeHumaResponse writes body with status as huma writes a handler's
// response: in the content type the client accepts, through the API's
// transformers (which link the body's schema).
func (s *Server) writeHumaResponse(ctx huma.Context, status int, body any) {
	contentType, err := s.api.Negotiate(ctx.Header("Accept"))
	if err != nil {
		_ = huma.WriteErr(s.api, ctx, http.StatusNotAcceptable, "unable to marshal response", err)
		return
	}
	transformed, err := s.api.Transform(ctx, strconv.Itoa(status), body)
	if err != nil {
		_ = huma.WriteErr(s.api, ctx, http.StatusInternalServerError, "unexpected error occurred", err)
		return
	}
	ctx.SetHeader("Content-Type", contentType)
	ctx.SetStatus(status)
	if err := s.api.Marshal(ctx.BodyWriter(), contentType, transformed); err != nil {
		s.logger.Error("Failed to write the response", "path", ctx.Operation().Path, "error", err)
	}
}

// saveUpload stores an uploaded file under the base name of the one the
// browser sent.
func (s *Server) saveUpload(name string, file io.Reader) (*UploadResponse, error) {
	buf, err := io.ReadAll(io.LimitReader(file, maxUploadSize+1))
	if err != nil {
		return nil, fmt.Errorf("failed to upload file: %w", err)
	}
	if len(buf) > maxUploadSize {
		return nil, huma.Error400BadRequest("file size exceeds 10MB limit")
	}
	outPath, _, err := s.uploads.save(filepath.Base(name), buf)
	if err != nil {
		return nil, fmt.Errorf("failed to write file: %w", err)
	}
	resp := &UploadResponse{}
	resp.Body.Ok = true
	resp.Body.FilePath = outPath
	return resp, nil
}

// describeUpload describes POST /upload for the OpenAPI schema; the
// operation's middleware, serveUploadRequest, answers the requests.
func (s *Server) describeUpload(context.Context, *struct {
	RawBody huma.MultipartFormFiles[UploadRequest]
},
) (*UploadResponse, error) {
	return nil, errors.New("POST /upload is served by serveUploadRequest")
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
	huma.Post(s.api, "/upload", s.describeUpload, func(o *huma.Operation) {
		o.Description = "Upload files to the specified upload path."
		o.Middlewares = append(o.Middlewares, s.serveUploadRequest)
	})

	// Uploaded images, for the chat to show (not part of the OpenAPI
	// schema: the response is the image itself).
	s.router.Get("/uploads/{checksum}/{name}", s.serveUpload)
}
