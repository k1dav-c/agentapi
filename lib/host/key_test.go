package host

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseKey(t *testing.T) {
	for _, s := range []string{"acme/widget#12", "acme~widget~12"} {
		k, err := ParseKey(s)
		require.NoError(t, err, s)
		assert.Equal(t, Key{Owner: "acme", Name: "widget", Issue: 12}, k)
		assert.Equal(t, "acme/widget#12", k.String())
		assert.Equal(t, "acme~widget~12", k.Slug())
		assert.Equal(t, "agent/issue-12", k.Branch())
	}
	for _, s := range []string{"", "acme#1", "acme/widget#0", "acme/widget#-1", "acme/widget#01", "acme/../x#1", "a/b/c#1", "acme/wid get#1", "acme~widget", "../x~y~1"} {
		_, err := ParseKey(s)
		assert.Error(t, err, s)
	}
}

func TestRepoFromRemote(t *testing.T) {
	for remote, want := range map[string]string{
		"git@github.com:acme/widget.git":         "acme/widget",
		"git@github.com:acme/widget":             "acme/widget",
		"https://github.com/acme/widget.git":     "acme/widget",
		"https://github.com/acme/widget/":        "acme/widget",
		"ssh://git@github.com/acme/my.repo.git":  "acme/my.repo",
		"https://x-access-token@github.com/a/b":  "a/b",
		"https://gitlab.com/acme/widget.git":     "",
		"git@gitlab.com:acme/widget.git":         "",
		"https://github.com/acme":                "",
		"https://evil.com/@github.com/acme/repo": "",
	} {
		got, ok := RepoFromRemote(remote)
		assert.Equal(t, want, got, remote)
		assert.Equal(t, want != "", ok, remote)
	}
}

func TestParsePorts(t *testing.T) {
	first, last, err := parsePorts("4100-4199")
	require.NoError(t, err)
	assert.Equal(t, 4100, first)
	assert.Equal(t, 4199, last)
	first, last, err = parsePorts("5000")
	require.NoError(t, err)
	assert.Equal(t, [2]int{5000, 5000}, [2]int{first, last})
	for _, bad := range []string{"", "a-b", "200-100", "0-10", "1-70000"} {
		_, _, err := parsePorts(bad)
		assert.Error(t, err, bad)
	}
}

func TestConfigDefaults(t *testing.T) {
	home := t.TempDir()
	config, err := Config{Agents: []string{"native-demo"}, Scan: []string{"~/src"}, AgentAPI: "/bin/agentapi"}.withDefaults(home)
	require.NoError(t, err)
	assert.Equal(t, DefaultPort, config.Port)
	assert.Equal(t, home+"/work", config.Workdir)
	assert.Equal(t, []string{home + "/work", home + "/src"}, config.Scan)
	assert.Equal(t, "native-demo", config.Agent)
	assert.Equal(t, home+"/.agentapi/host.token", config.TokenFile)
	assert.True(t, config.CloneMissing())
	assert.Equal(t, []string{"pi", "--approve"}, config.AgentCommands["pi"])

	_, err = Config{Agents: []string{"aider"}}.withDefaults(home)
	assert.ErrorContains(t, err, "no command")
	_, err = Config{Agents: []string{"native-demo"}, Agent: "claude"}.withDefaults(home)
	assert.ErrorContains(t, err, "isn't one of")
}
