package update

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/selfupdate/selfupdatetest"
)

// --check reports whether an update is available, --force or not; --force
// alone installs the latest regardless.
func TestUpdateCheck(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake program is a shell script")
	}
	for _, tc := range []struct {
		name            string
		current, latest string
		opts            options
		want            string
		downloads       int
	}{
		{"up to date", "2.5.0", "2.5.0", options{check: true}, "Already up to date.\n", 0},
		{"up to date, forced", "2.5.0", "2.5.0", options{check: true, force: true}, "Already up to date.\n", 0},
		{"older", "2.4.0", "2.5.0", options{check: true}, "Update available. Run 'agentapi update' to install.\n", 0},
		{"older, forced", "2.4.0", "2.5.0", options{check: true, force: true}, "Update available. Run 'agentapi update' to install.\n", 0},
		{"unparseable, forced", "dev", "2.5.0", options{check: true, force: true}, "Update available. Run 'agentapi update' to install.\n", 0},
		{"up to date, installed by force", "2.5.0", "2.5.0", options{force: true}, "Successfully updated agentapi from v2.5.0 to v2.5.0\n", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release := selfupdatetest.NewRelease(t, tc.latest, selfupdatetest.Script(tc.latest))
			program := selfupdatetest.WriteProgram(t, filepath.Join(t.TempDir(), "agentapi"), tc.current)
			var out bytes.Buffer
			require.NoError(t, update(&out, release.Source(), program, tc.current, tc.opts))
			assert.Contains(t, out.String(), "Latest version:  v"+tc.latest+"\n")
			assert.Truef(t, bytes.HasSuffix(out.Bytes(), []byte(tc.want)), "output %q", out.String())
			assert.Equal(t, tc.downloads, release.Downloads())
			data, err := os.ReadFile(program)
			require.NoError(t, err)
			if tc.downloads == 0 {
				assert.Equal(t, string(selfupdatetest.Script(tc.current)), string(data), "not replaced")
			}
		})
	}
}
