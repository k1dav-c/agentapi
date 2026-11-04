package server

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// program writes an executable file at dir/name that runs script.
func program(t *testing.T, dir, name, script string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o700))
	return path
}

func TestResolveExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no restart in place on Windows")
	}
	t.Parallel()
	dir := t.TempDir()
	real := program(t, filepath.Join(dir, "v2"), "agentapi", "exit 0")
	other := program(t, filepath.Join(dir, "other"), "agentapi", "exit 0")
	// An install that an update points to a new version.
	link := filepath.Join(dir, "bin", "agentapi")
	require.NoError(t, os.MkdirAll(filepath.Dir(link), 0o700))
	require.NoError(t, os.Symlink(real, link))
	running := func(path string) func() (string, error) {
		return func() (string, error) { return path, nil }
	}
	onPath := func(path string) func(string) (string, error) {
		return func(string) (string, error) { return path, nil }
	}
	notOnPath := func(string) (string, error) { return "", errors.New("not found") }

	got, err := resolveExecutable("agentapi", onPath(link), running(real))
	require.NoError(t, err)
	assert.Equal(t, link, got, "the name on PATH, which follows the symlink")

	got, err = resolveExecutable(link, notOnPath, running(real))
	require.NoError(t, err)
	assert.Equal(t, link, got, "a path")

	wd, err := os.Getwd()
	require.NoError(t, err)
	relative, err := filepath.Rel(wd, link)
	require.NoError(t, err)
	got, err = resolveExecutable(relative, notOnPath, running(real))
	require.NoError(t, err)
	assert.Equal(t, link, got, "a relative path, made absolute at startup")

	got, err = resolveExecutable("agentapi", onPath(other), running(real))
	require.NoError(t, err)
	assert.Equal(t, real, got, "PATH names another program than the one running")

	got, err = resolveExecutable("agentapi", notOnPath, running(real))
	require.NoError(t, err)
	assert.Equal(t, real, got)

	noProc := func() (string, error) { return "", errors.New("no /proc") }
	got, err = resolveExecutable("agentapi", onPath(link), noProc)
	require.NoError(t, err)
	assert.Equal(t, link, got)
	_, err = resolveExecutable("agentapi", notOnPath, noProc)
	assert.Error(t, err)
}

// The program runs with --version, then with --validate, before the server
// stops for a restart.
func TestSelfExecCheck(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no restart in place on Windows")
	}
	t.Parallel()
	dir := t.TempDir()
	works := &selfExec{path: program(t, dir, "agentapi", `echo "agentapi version 2.1.0"`), argv: []string{"agentapi", "server", "x"}}
	got, err := works.check(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "agentapi version 2.1.0", got.version)
	assert.True(t, got.validated)

	broken := &selfExec{path: program(t, dir, "broken", "exit 2"), argv: []string{"agentapi"}}
	_, err = broken.check(context.Background())
	assert.ErrorContains(t, err, "broken --version")

	gone := &selfExec{path: filepath.Join(dir, "gone"), argv: []string{"agentapi"}}
	_, err = gone.check(context.Background())
	assert.Error(t, err)

	_, err = (&selfExec{}).check(context.Background())
	assert.ErrorContains(t, err, "didn't find its program")
}

// A program still held open for writing runs once it is let go: Linux
// refuses to run it until then (ETXTBSY), as when a process forked while
// it was written (by an update, or another test) hasn't exec'd yet.
func TestSelfExecCheckWaitsForAWriter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no restart in place on Windows")
	}
	t.Parallel()
	s := &selfExec{path: program(t, t.TempDir(), "agentapi", `echo "agentapi version 2.1.0"`), argv: []string{"agentapi", "server"}}
	writer, err := os.OpenFile(s.path, os.O_WRONLY, 0)
	require.NoError(t, err)
	time.AfterFunc(100*time.Millisecond, func() { _ = writer.Close() })
	got, err := s.check(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "agentapi version 2.1.0", got.version)
}

// fakeStateDB answers CheckWriter with err, and records what it was asked.
type fakeStateDB struct {
	err     error
	knows   int
	program string
}

func (f *fakeStateDB) CheckWriter(knows int, program string) error {
	f.knows, f.program = knows, program
	return f.err
}

// --validate runs with the command line and the environment the restart
// runs, and its refusal keeps the server serving; a program older than
// --validate is only run.
func TestSelfExecCheckValidates(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no restart in place on Windows")
	}
	t.Parallel()
	dir := t.TempDir()
	record := filepath.Join(dir, "record")
	// A program that prints its version, records how --validate ran and
	// answers it with $ANSWER.
	prog := program(t, dir, "agentapi", `
if [ "$1" = --version ]; then echo "agentapi version 2.4.0"; exit 0; fi
printf '%s\n' "$@" > `+record+`
printf 'token=%s\n' "$`+generatedAPITokenEnv+`" >> `+record+`
case "$ANSWER" in
ok) echo "ok state-db-schema=7" ;;
old) echo "unknown flag: --validate" >&2; exit 1 ;;
refuse) echo "some log line" >&2; echo 'unknown --transport "bogus": AgentAPI 3 drives every agent natively' >&2; exit 1 ;;
silent) exit 3 ;;
esac`)
	argv := []string{"agentapi", "server", "--port", "3600", "--", "claude", "--model", "x"}
	env := func(answer string) []string {
		return []string{"PATH=/usr/bin:/bin", "ANSWER=" + answer, generatedAPITokenEnv + "=kept"}
	}

	db := &fakeStateDB{}
	self := &selfExec{path: prog, argv: argv, env: env("ok"), stateDB: db}
	got, err := self.check(context.Background())
	require.NoError(t, err)
	assert.Equal(t, checked{version: "agentapi version 2.4.0", validated: true}, got)
	recorded, err := os.ReadFile(record)
	require.NoError(t, err)
	assert.Equal(t, "server\n--port\n3600\n--validate\n--\nclaude\n--model\nx\ntoken=kept\n", string(recorded),
		"the restart's arguments, --validate before the agent's, and its environment")
	assert.Equal(t, 7, db.knows, "the state database is checked for the schema the program knows")
	assert.Equal(t, "2.4.0", db.program)

	db.err = errors.New("needs AgentAPI 9.0.0 or later")
	_, err = self.check(context.Background())
	assert.ErrorContains(t, err, prog+" wouldn't start: the state database needs AgentAPI 9.0.0 or later")

	self = &selfExec{path: prog, argv: argv, env: env("refuse")}
	_, err = self.check(context.Background())
	assert.EqualError(t, err, prog+` wouldn't start: unknown --transport "bogus": AgentAPI 3 drives every agent natively`, "the last line it wrote")

	self = &selfExec{path: prog, argv: argv, env: env("silent")}
	_, err = self.check(context.Background())
	assert.EqualError(t, err, prog+" wouldn't start: exit status 3")

	self = &selfExec{path: prog, argv: argv, env: env("old"), stateDB: &fakeStateDB{err: errors.New("not asked")}}
	got, err = self.check(context.Background())
	require.NoError(t, err, "AgentAPI 2.3.0 and earlier don't know --validate")
	assert.Equal(t, checked{version: "agentapi version 2.4.0"}, got)

	self = &selfExec{path: prog, argv: argv, env: env("unexpected"), stateDB: &fakeStateDB{}}
	_, err = self.check(context.Background())
	assert.ErrorContains(t, err, "--validate answered")
}

func TestValidateArgs(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{"server", "--validate", "--", "claude", "--"}, validateArgs([]string{"agentapi", "server", "--", "claude", "--"}))
	assert.Equal(t, []string{"server", "claude", "--validate"}, validateArgs([]string{"/bin/agentapi", "server", "claude"}))
}

// envValue returns name's value in env.
func envValue(env []string, name string) (string, bool) {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, name+"="); ok {
			return v, true
		}
	}
	return "", false
}

// A bare --api-token generates a token once: the restart in place runs
// the same arguments, and gets the token in its environment.
func TestGeneratedAPITokenSurvivesRestart(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	opts := serverOptions{apiToken: "generate", port: 3510}
	t.Setenv(generatedAPITokenEnv, "")

	self := &selfExec{env: []string{"PATH=/usr/bin", generatedAPITokenEnv + "=stale"}}
	token, err := resolveAPIToken(opts, logger, self)
	require.NoError(t, err)
	require.True(t, isGeneratedAPIToken(token))
	kept, ok := envValue(self.env, generatedAPITokenEnv)
	require.True(t, ok)
	assert.Equal(t, token, kept)
	assert.Len(t, self.env, 2)

	// The program runs again with self.env.
	t.Setenv(generatedAPITokenEnv, kept)
	again := &selfExec{env: self.env}
	token2, err := resolveAPIToken(opts, logger, again)
	require.NoError(t, err)
	assert.Equal(t, token, token2)
	_, set := os.LookupEnv(generatedAPITokenEnv)
	assert.False(t, set, "the agent's environment doesn't get it")

	// A value that isn't a generated token isn't taken.
	t.Setenv(generatedAPITokenEnv, "short")
	token3, err := resolveAPIToken(opts, logger, &selfExec{})
	require.NoError(t, err)
	assert.NotEqual(t, "short", token3)

	// A given token, or none, isn't carried.
	for _, given := range []string{"", "mine"} {
		t.Setenv(generatedAPITokenEnv, kept)
		self := &selfExec{env: []string{generatedAPITokenEnv + "=" + kept}}
		got, err := resolveAPIToken(serverOptions{apiToken: given}, logger, self)
		require.NoError(t, err)
		assert.Equal(t, given, got)
		_, ok := envValue(self.env, generatedAPITokenEnv)
		assert.False(t, ok, given)
	}
}

// Without a state database, the program a restart in place runs gets the
// same --initial-prompt but doesn't send it again: initialPromptSentEnv
// says it went out. With one, the restored conversation has it, and the
// variable isn't carried.
func TestInitialPromptNotSentAgainAfterRestart(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	t.Setenv(initialPromptSentEnv, "")
	require.NoError(t, os.Unsetenv(initialPromptSentEnv))

	self := &selfExec{env: []string{"PATH=/usr/bin"}}
	assert.Equal(t, "fix the bug", initialPromptOnce("fix the bug", false, logger, self))
	sent, ok := envValue(self.env, initialPromptSentEnv)
	require.True(t, ok, "the restarted program is told")

	// The program runs again with self.env.
	t.Setenv(initialPromptSentEnv, sent)
	again := &selfExec{env: self.env}
	assert.Empty(t, initialPromptOnce("fix the bug", false, logger, again))
	_, set := os.LookupEnv(initialPromptSentEnv)
	assert.False(t, set, "the agent's environment doesn't get it")
	_, ok = envValue(again.env, initialPromptSentEnv)
	assert.True(t, ok, "and a further restart doesn't send it either")

	// With a state database the prompt goes to the conversation, which
	// skips it once restored, and nothing is carried.
	t.Setenv(initialPromptSentEnv, sent)
	withDB := &selfExec{env: slices.Clone(self.env)}
	assert.Equal(t, "fix the bug", initialPromptOnce("fix the bug", true, logger, withDB))
	_, ok = envValue(withDB.env, initialPromptSentEnv)
	assert.False(t, ok)

	// No prompt, nothing carried.
	none := &selfExec{}
	assert.Empty(t, initialPromptOnce("", false, logger, none))
	assert.Empty(t, none.env)
}
