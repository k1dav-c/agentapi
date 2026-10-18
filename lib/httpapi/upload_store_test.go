package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUploadStore(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "uploads")
	store, err := newUploadStore(dir)
	require.NoError(t, err)

	path, checksum, err := store.save("notes.txt", []byte("hello"))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, checksum, "notes.txt"), path)
	assert.Regexp(t, uploadChecksumRe, checksum)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(data))

	_, _, err = store.save("..", []byte("x"))
	assert.Error(t, err, "a name can't leave its directory")
	_, err = store.saveImage([]byte("<svg></svg>"))
	assert.ErrorContains(t, err, "not a previewable image")

	png := "\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 16)
	url, err := store.saveImage([]byte(png))
	require.NoError(t, err)
	assert.Regexp(t, `^/uploads/[0-9a-f]{16}/image\.png$`, url)
	parts := strings.Split(url, "/")
	for _, tc := range []struct {
		checksum, name string
		status         int
	}{
		{parts[2], parts[3], http.StatusOK},
		{checksum, "notes.txt", http.StatusUnsupportedMediaType},
		{parts[2], "..", http.StatusNotFound},
		{"not-a-checksum", "image.png", http.StatusNotFound},
	} {
		rec := httptest.NewRecorder()
		store.serve(rec, httptest.NewRequest(http.MethodGet, "/", nil), tc.checksum, tc.name)
		assert.Equal(t, tc.status, rec.Code, "%s/%s", tc.checksum, tc.name)
	}

	removed, err := store.remove()
	require.NoError(t, err)
	assert.False(t, removed, "a directory that was given is kept")
	assert.DirExists(t, dir)

	temp, err := newUploadStore("")
	require.NoError(t, err)
	removed, err = temp.remove()
	require.NoError(t, err)
	assert.True(t, removed)
	assert.NoDirExists(t, temp.dir)
}
