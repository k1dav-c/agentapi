package host

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
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
