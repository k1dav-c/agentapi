package httpapi

import (
	"bytes"
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

func TestUploadStoreSaveFrom(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "uploads")
	store, err := newUploadStore(dir)
	require.NoError(t, err)
	// noTemp checks no temporary file is left in the store.
	noTemp := func() {
		t.Helper()
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		for _, entry := range entries {
			assert.False(t, strings.HasPrefix(entry.Name(), ".upload-"), entry.Name())
		}
	}

	content := bytes.Repeat([]byte("abc"), 1000)
	path, checksum, err := store.saveFrom("big.bin", bytes.NewReader(content), int64(len(content)))
	require.NoError(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, content, data)
	noTemp()

	// The same content again is the same file.
	again, sameChecksum, err := store.saveFrom("big.bin", bytes.NewReader(content), int64(len(content)))
	require.NoError(t, err)
	assert.Equal(t, path, again)
	assert.Equal(t, checksum, sameChecksum)
	noTemp()

	// One byte over the limit is refused, and nothing stays.
	_, _, err = store.saveFrom("big.bin", bytes.NewReader(content), int64(len(content))-1)
	require.ErrorIs(t, err, errUploadTooLarge)
	noTemp()

	for _, name := range []string{"", ".", "..", "a/b", "../b"} {
		_, _, err = store.saveFrom(name, bytes.NewReader([]byte("x")), 10)
		assert.Error(t, err, "name %q", name)
	}
	noTemp()
}

func TestUploadStoreRefusesALinkedDirectory(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "uploads")
	store, err := newUploadStore(dir)
	require.NoError(t, err)
	_, checksum, err := store.save("probe.txt", []byte("linked"))
	require.NoError(t, err)
	// The content's directory replaced by a link out of the store.
	outside := t.TempDir()
	require.NoError(t, os.RemoveAll(filepath.Join(dir, checksum)))
	require.NoError(t, os.Symlink(outside, filepath.Join(dir, checksum)))

	_, _, err = store.save("probe.txt", []byte("linked"))
	require.Error(t, err)
	entries, err := os.ReadDir(outside)
	require.NoError(t, err)
	assert.Empty(t, entries, "nothing is written through the link")
}

func TestUploadName(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("あ", 100) + ".png" // 300 bytes, then the extension
	for sent, want := range map[string]string{
		"notes.txt":              "notes.txt",
		"/tmp/notes.txt":         "notes.txt",
		"no\ttes\n.txt":          "notes.txt",
		"\x1b[31mred\x1b[0m.txt": "[31mred[0m.txt",
		"  spaced.txt ":          "spaced.txt",
		"bad\xffutf8.txt":        "badutf8.txt",
		long:                     strings.Repeat("あ", 83) + ".png",
		"":                       "",
		".":                      "",
		"..":                     "",
		"/":                      "",
		"dir/..":                 "",
		"\x00":                   "",
		" ":                      "",
	} {
		got, ok := uploadName(sent)
		assert.Equal(t, want, got, "%q", sent)
		assert.Equal(t, want != "", ok, "%q", sent)
		assert.LessOrEqual(t, len(got), maxUploadNameLength)
	}
}
