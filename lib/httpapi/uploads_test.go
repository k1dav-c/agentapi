package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coder/agentapi/lib/httpapi"
	"github.com/coder/agentapi/lib/logctx"
	"github.com/coder/agentapi/lib/msgfmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// uploadForPreview posts a file to /upload and returns the stored path.
func uploadForPreview(t *testing.T, base, name string, content []byte) string {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", name)
	require.NoError(t, err)
	_, err = part.Write(content)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	resp, err := http.Post(base+"/upload", writer.FormDataContentType(), &buf)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var body struct {
		FilePath string `json:"filePath"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.NotEmpty(t, body.FilePath)
	return body.FilePath
}

// previewURL is the GET /uploads URL for a stored upload path.
func previewURL(base, stored string) string {
	return base + "/uploads/" + filepath.Base(filepath.Dir(stored)) + "/" + filepath.Base(stored)
}

func TestServeUpload(t *testing.T) {
	t.Parallel()
	ctx := logctx.WithLogger(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv, err := httpapi.NewServer(ctx, httpapi.ServerConfig{
		AgentType:      msgfmt.AgentTypeClaude,
		ChatBasePath:   "/chat",
		AllowedHosts:   []string{"*"},
		AllowedOrigins: []string{"*"},
	})
	require.NoError(t, err)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.RGBA{R: 255, A: 255})
	var pngBytes bytes.Buffer
	require.NoError(t, png.Encode(&pngBytes, img))

	t.Run("serves an uploaded image", func(t *testing.T) {
		t.Parallel()
		stored := uploadForPreview(t, ts.URL, "screenshot 1.png", pngBytes.Bytes())
		resp, err := http.Get(previewURL(ts.URL, stored))
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "image/png", resp.Header.Get("Content-Type"))
		assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
		assert.Contains(t, resp.Header.Get("Content-Security-Policy"), "sandbox")
		got, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, pngBytes.Bytes(), got)
	})

	t.Run("the content decides, not the name", func(t *testing.T) {
		t.Parallel()
		for name, content := range map[string]string{
			"evil.png":  "<html><script>alert(1)</script></html>",
			"logo.svg":  `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`,
			"notes.txt": "just text",
		} {
			stored := uploadForPreview(t, ts.URL, name, []byte(content))
			resp, err := http.Get(previewURL(ts.URL, stored))
			require.NoError(t, err)
			_ = resp.Body.Close()
			assert.Equal(t, http.StatusUnsupportedMediaType, resp.StatusCode, name)
		}
	})

	t.Run("only files in the upload directory", func(t *testing.T) {
		t.Parallel()
		stored := uploadForPreview(t, ts.URL, "a.png", pngBytes.Bytes())
		checksum := filepath.Base(filepath.Dir(stored))

		// A symlink in the upload directory that points outside it.
		secret := filepath.Join(t.TempDir(), "secret.png")
		require.NoError(t, os.WriteFile(secret, pngBytes.Bytes(), 0o600))
		require.NoError(t, os.Symlink(secret, filepath.Join(filepath.Dir(stored), "link.png")))

		for _, path := range []string{
			"/uploads/" + checksum + "/link.png",
			"/uploads/" + checksum + "/..%2F..%2Fetc%2Fpasswd",
			"/uploads/../" + checksum + "/a.png",
			"/uploads/not-a-checksum/a.png",
			"/uploads/" + checksum + "/missing.png",
			"/uploads/" + checksum + "/",
		} {
			resp, err := http.Get(ts.URL + path)
			require.NoError(t, err)
			_ = resp.Body.Close()
			assert.NotEqual(t, http.StatusOK, resp.StatusCode, path)
			assert.False(t, strings.HasPrefix(resp.Header.Get("Content-Type"), "image/"), path)
		}
	})
}
