package workspacefs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// What a Dir refuses says why, for lib/httpapi's status; the chat shows
// the message. The endpoints' tests are lib/httpapi's (files_test.go).
func TestDirRefusals(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o600))
	refusal := func(err error) (ErrorKind, string) {
		t.Helper()
		var refused *Error
		require.True(t, errors.As(err, &refused), "%v", err)
		return refused.Kind, refused.Message
	}

	rel, err := Dir(dir).Resolve(filepath.Join(dir, "a.txt"))
	require.NoError(t, err)
	assert.Equal(t, "a.txt", rel)

	_, err = Dir(dir).Resolve(" ")
	kind, message := refusal(err)
	assert.Equal(t, Invalid, kind)
	assert.Equal(t, "path is empty", message)
	_, err = Dir("").Resolve("a.txt")
	kind, _ = refusal(err)
	assert.Equal(t, NotFound, kind)
	_, err = Dir(dir).Resolve("../a.txt")
	kind, message = refusal(err)
	assert.Equal(t, Forbidden, kind)
	assert.Equal(t, "../a.txt is outside the agent's working directory", message)

	_, err = Dir(dir).Content("missing.txt")
	kind, message = refusal(err)
	assert.Equal(t, NotFound, kind)
	assert.Equal(t, "missing.txt doesn't exist", message)
	_, err = Dir(dir).Content(".")
	kind, _ = refusal(err)
	assert.Equal(t, Invalid, kind, "not a file")

	file, info, err := Dir(dir).Open("a.txt")
	require.NoError(t, err)
	assert.EqualValues(t, 1, info.Size())
	require.NoError(t, file.Close())
}
