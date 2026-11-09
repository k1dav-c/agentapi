package host

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnitFile(t *testing.T) {
	unit := unitFile("/home/me/.local/bin/agentapi", "")
	assert.Contains(t, unit, "\nExecStart=/home/me/.local/bin/agentapi host serve\n")
	// Restarting the launcher leaves the jobs' AgentAPIs running.
	assert.Contains(t, unit, "\nKillMode=process\n")
	assert.Contains(t, unit, "\nWantedBy=default.target\n")

	unit = unitFile("/opt/my tools/agentapi", "/home/me/100%/config.yaml")
	assert.Contains(t, unit, `ExecStart="/opt/my tools/agentapi" host serve --config "/home/me/100%%/config.yaml"`)
	assert.False(t, strings.Contains(unit, "\r"))
}

func TestQuoteSystemd(t *testing.T) {
	assert.Equal(t, "/usr/bin/agentapi", quoteSystemd("/usr/bin/agentapi"))
	assert.Equal(t, `"/a b/\"c\"/$$HOME"`, quoteSystemd(`/a b/"c"/$HOME`))
}

// fakeRun replaces run for a test, and returns the commands it ran.
func fakeRun(t *testing.T) *[]string {
	t.Helper()
	var ran []string
	saved := run
	run = func(_ context.Context, name string, args ...string) (string, error) {
		ran = append(ran, name+" "+strings.Join(args, " "))
		return "", nil
	}
	t.Cleanup(func() { run = saved })
	return &ran
}

func TestUninstall(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ran := fakeRun(t)
	uninstall := func() string {
		var out bytes.Buffer
		cmd := uninstallCmd()
		cmd.SetOut(&out)
		cmd.SetArgs(nil)
		require.NoError(t, cmd.ExecuteContext(context.Background()))
		return out.String()
	}

	assert.Contains(t, uninstall(), "isn't installed")
	assert.Empty(t, *ran, "nothing to stop")

	path, err := writeUnit("/usr/bin/agentapi", "")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "systemd", "user", serviceName), path)
	assert.Contains(t, uninstall(), "Removed "+path)
	assert.NoFileExists(t, path)
	assert.Equal(t, []string{"systemctl --user disable --now " + serviceName, "systemctl --user daemon-reload"}, *ran)
}
