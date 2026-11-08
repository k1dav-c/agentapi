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
	"github.com/coder/agentapi/lib/userconfig"
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

// --allow-update writes allow-update: true into the config file, keeping
// what it says, whether the update installs or finds nothing newer, and
// not with --check.
func TestUpdateAllowUpdate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake program is a shell script")
	}
	for _, tc := range []struct {
		name            string
		current, latest string
		check           bool
		written         bool
	}{
		{"up to date", "2.5.0", "2.5.0", false, true},
		{"installed", "2.4.0", "2.5.0", false, true},
		{"checked only", "2.4.0", "2.5.0", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release := selfupdatetest.NewRelease(t, tc.latest, selfupdatetest.Script(tc.latest))
			program := selfupdatetest.WriteProgram(t, filepath.Join(t.TempDir(), "agentapi"), tc.current)
			path := filepath.Join(t.TempDir(), ".agentapi", "config.yaml")
			require.NoError(t, userconfig.Save(path, userconfig.Config{"webhook-url": "http://hub:3290/hooks"}))
			target, err := configTarget(path)
			require.NoError(t, err)
			var out bytes.Buffer
			require.NoError(t, update(&out, release.Source(), program, tc.current, options{check: tc.check, allowUpdate: target}))

			config, err := userconfig.Load(path)
			require.NoError(t, err)
			assert.Equal(t, "http://hub:3290/hooks", config["webhook-url"], "the rest stays")
			if tc.written {
				assert.Equal(t, true, config["allow-update"])
				assert.Contains(t, out.String(), "Wrote allow-update: true to "+path)
			} else {
				assert.Nil(t, config["allow-update"])
				assert.NotContains(t, out.String(), "allow-update")
			}
		})
	}
}

// A config file that doesn't exist yet is created.
func TestUpdateAllowUpdateNewFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "home", ".agentapi", "config.yaml")
	target, err := configTarget(path)
	require.NoError(t, err)
	var out bytes.Buffer
	require.NoError(t, allowUpdates(&out, target, false))
	config, err := userconfig.Load(path)
	require.NoError(t, err)
	assert.Equal(t, true, config["allow-update"])
	assert.Contains(t, out.String(), "agentapi restart --all")
}
