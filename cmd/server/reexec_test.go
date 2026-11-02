package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

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

// The program runs with --version before the server stops for a restart.
func TestSelfExecCheck(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no restart in place on Windows")
	}
	t.Parallel()
	dir := t.TempDir()
	works := &selfExec{path: program(t, dir, "agentapi", `echo "agentapi version 2.1.0"`)}
	version, err := works.check(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "agentapi version 2.1.0", version)

	broken := &selfExec{path: program(t, dir, "broken", "exit 2")}
	_, err = broken.check(context.Background())
	assert.ErrorContains(t, err, "broken --version")

	gone := &selfExec{path: filepath.Join(dir, "gone")}
	_, err = gone.check(context.Background())
	assert.Error(t, err)

	_, err = (&selfExec{}).check(context.Background())
	assert.ErrorContains(t, err, "didn't find its program")
}
