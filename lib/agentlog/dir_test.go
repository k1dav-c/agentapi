package agentlog

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDirThroughASymlink(t *testing.T) {
	t.Parallel()
	root := RealPath(t.TempDir())
	real := filepath.Join(root, "real")
	link := filepath.Join(root, "link")
	require.NoError(t, os.Mkdir(real, 0o700))
	require.NoError(t, os.Symlink(real, link))
	other := filepath.Join(root, "other")
	require.NoError(t, os.Mkdir(other, 0o700))
	require.NoError(t, os.Symlink(other, filepath.Join(root, "other-link")))

	d := NewDir(link + "/")
	assert.Equal(t, link, d.Path())
	assert.Equal(t, []string{real, link}, d.Paths(), "the real path first: the agents record it")
	assert.True(t, d.Is(real))
	assert.True(t, d.Is(link))
	assert.True(t, d.Is(real+"/./"))
	assert.False(t, d.Is(other))
	assert.False(t, d.Is(filepath.Join(root, "other-link")))
	assert.False(t, d.Is(""))

	d = NewDir(real)
	assert.Equal(t, []string{real}, d.Paths())
	assert.True(t, d.Is(link), "a path an agent recorded through a link")

	gone := NewDir("/no/such/dir")
	assert.Equal(t, []string{"/no/such/dir"}, gone.Paths())
	assert.True(t, gone.Is("/no/such/dir/"))
}
