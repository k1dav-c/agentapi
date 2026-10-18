package native_test

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/native"
)

// The 8-byte PNG signature is enough for content sniffing.
var pngData = []byte("\x89PNG\r\n\x1a\n0000")

func TestInputFromTextMakesImagePathsAbsolute(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "shot.png"), pngData, 0o600))
	t.Chdir(dir)

	in := native.InputFromText(`look @"shot.png"`)
	assert.Equal(t, []native.Image{{Path: filepath.Join(dir, "shot.png"), MediaType: "image/png"}}, in.Images)
	assert.Equal(t, `look @"shot.png"`, in.Text, "the text keeps the mention as written")
}

func TestReadImage(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	png := filepath.Join(dir, "shot.png")
	require.NoError(t, os.WriteFile(png, pngData, 0o600))
	bmp := filepath.Join(dir, "shot.bmp")
	require.NoError(t, os.WriteFile(bmp, []byte("BM0000000000"), 0o600))
	large := filepath.Join(dir, "large.png")
	require.NoError(t, os.WriteFile(large, append(pngData, make([]byte, native.MaxImageBytes)...), 0o600))

	data, mediaType, err := native.ReadImage(png)
	require.NoError(t, err)
	assert.Equal(t, pngData, data)
	assert.Equal(t, "image/png", mediaType)

	_, _, err = native.ReadImage(bmp)
	assert.ErrorContains(t, err, "not a PNG, JPEG, GIF or WebP image")
	_, _, err = native.ReadImage(large)
	assert.ErrorContains(t, err, "larger than 10 MB")
	_, _, err = native.ReadImage(dir)
	assert.ErrorContains(t, err, "not a regular file")
	_, _, err = native.ReadImage(filepath.Join(dir, "missing.png"))
	assert.Error(t, err)

	encoded, err := native.Image{Path: png, MediaType: "image/png"}.Base64()
	require.NoError(t, err)
	assert.Equal(t, base64.StdEncoding.EncodeToString(pngData), encoded)
}
