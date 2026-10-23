package httpapi

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/htmlpreview"
)

// pngHeader starts every PNG file.
const pngHeader = "\x89PNG\r\n\x1a\n"

func TestFilePreview(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "site", "index.html"), `<!doctype html><link rel="stylesheet" href="style.css"><img src="logo.png"><script src="../secret.js"></script>`)
	writeFile(t, filepath.Join(dir, "site", "style.css"), "body { color: red }")
	writeFile(t, filepath.Join(dir, "site", "logo.png"), pngHeader)
	writeFile(t, filepath.Join(dir, "notes.txt"), "notes")
	writeFile(t, filepath.Join(dir, "big.html"), strings.Repeat("x", maxPreviewPage+1))
	outside := filepath.Join(t.TempDir(), "secret.js")
	writeFile(t, outside, "secret")
	// A symlink in the working directory can't lead the preview out of it.
	require.NoError(t, os.Symlink(outside, filepath.Join(dir, "secret.js")))
	s := &fileAPI{cwd: dir}
	get := func(path string) (*FilePreviewResponse, error) {
		return s.getFilePreview(t.Context(), &FilePathQuery{Path: path})
	}

	resp, err := get("site/index.html")
	require.NoError(t, err)
	assert.Equal(t, "site/index.html", resp.Body.Relative)
	assert.Contains(t, resp.Body.HTML, `href="data:text/css;charset=utf-8;base64,`+base64.StdEncoding.EncodeToString([]byte("body { color: red }"))+`"`)
	assert.Contains(t, resp.Body.HTML, `src="data:image/png;base64,`+base64.StdEncoding.EncodeToString([]byte(pngHeader))+`"`)
	assert.Equal(t, []htmlpreview.Missing{{Ref: "../secret.js", Reason: "can't be read"}}, resp.Body.Missing)
	assert.NotContains(t, resp.Body.HTML, base64.StdEncoding.EncodeToString([]byte("secret")))

	for path, status := range map[string]int{
		"notes.txt":         400,
		"big.html":          400,
		"missing.html":      404,
		"../x.html":         403,
		outside + ".html":   403,
		"site/":             400,
		"site/index.html/x": 400,
	} {
		_, err := get(path)
		require.Error(t, err, path)
		assert.Equal(t, status, statusOf(t, err), path)
	}
}

func TestFileImage(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "img", "logo.png"), pngHeader+"rest")
	writeFile(t, filepath.Join(dir, "img", "fake.png"), "<script>alert(1)</script>")
	writeFile(t, filepath.Join(dir, "img", "icon.svg"), `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	writeFile(t, filepath.Join(dir, "page.html"), "<h1>hi</h1>")
	outside := filepath.Join(t.TempDir(), "secret.png")
	writeFile(t, outside, pngHeader)
	require.NoError(t, os.Symlink(outside, filepath.Join(dir, "link.png")))
	s := &fileAPI{cwd: dir}
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.serveFileImage(rec, httptest.NewRequest(http.MethodGet, "/files/image?path="+url.QueryEscape(path), nil))
		return rec
	}

	rec := get("img/logo.png")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, pngHeader+"rest", rec.Body.String())
	assert.Equal(t, "image/png", rec.Header().Get("Content-Type"))
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	assert.Contains(t, rec.Header().Get("Content-Security-Policy"), "sandbox")
	assert.NotEmpty(t, rec.Header().Get("Last-Modified"))

	// SVG is served in a sandbox where its script doesn't run.
	rec = get(filepath.Join(dir, "img", "icon.svg"))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "image/svg+xml", rec.Header().Get("Content-Type"))
	assert.Contains(t, rec.Header().Get("Content-Security-Policy"), "default-src 'none'")

	for path, status := range map[string]int{
		"img/fake.png": http.StatusUnsupportedMediaType,
		"page.html":    http.StatusUnsupportedMediaType,
		"img":          http.StatusBadRequest,
		"":             http.StatusBadRequest,
		"missing.png":  http.StatusNotFound,
		"../x.png":     http.StatusForbidden,
		outside:        http.StatusForbidden,
		"link.png":     http.StatusForbidden,
	} {
		assert.Equal(t, status, get(path).Code, path)
	}
}
