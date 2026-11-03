package termexec

import (
	"testing"

	"github.com/ActiveState/termtest/xpty"
	"github.com/stretchr/testify/require"
)

// The PTY reader depends on xpty's unexported fields (see xptyOutput):
// an xpty upgrade that moves them fails here, not in a server's reader.
func TestXptyOutputFields(t *testing.T) {
	t.Parallel()
	xp, err := xpty.New(80, 24, false)
	require.NoError(t, err)
	t.Cleanup(func() { _ = xp.Close() })
	require.NotNil(t, xptyOutput(xp))
}
