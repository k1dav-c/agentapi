package selfupdate_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/selfupdate"
	"github.com/coder/agentapi/lib/selfupdate/selfupdatetest"
)

// skipWithoutShell skips tests whose fake programs are shell scripts.
func skipWithoutShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake programs are shell scripts")
	}
}

// installed is the version the program at path prints.
func installed(t *testing.T, path string) string {
	t.Helper()
	version, err := selfupdate.ProgramVersion(context.Background(), path)
	require.NoError(t, err)
	return version.String()
}

func TestDownloadChecksTheManifest(t *testing.T) {
	t.Parallel()
	release := selfupdatetest.NewRelease(t, "2.2.0", selfupdatetest.Script("2.2.0"))
	latest, err := release.Source().Latest(context.Background())
	require.NoError(t, err)
	dir := t.TempDir()
	path, err := release.Source().Download(context.Background(), latest, selfupdate.PlatformAsset(), dir)
	require.NoError(t, err)
	assert.Equal(t, dir, filepath.Dir(path))
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, selfupdatetest.Script("2.2.0"), got)

	// A download that doesn't match checksums.txt is refused, and nothing
	// stays behind.
	tampered := selfupdatetest.NewRelease(t, "2.2.0", selfupdatetest.Script("2.2.0"))
	tampered.Tamper()
	latest, err = tampered.Source().Latest(context.Background())
	require.NoError(t, err)
	empty := t.TempDir()
	_, err = tampered.Source().Download(context.Background(), latest, selfupdate.PlatformAsset(), empty)
	require.ErrorContains(t, err, "checksum mismatch")
	entries, err := os.ReadDir(empty)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestProgramVersion(t *testing.T) {
	skipWithoutShell(t)
	t.Parallel()
	dir := t.TempDir()
	assert.Equal(t, "2.1.0", installed(t, selfupdatetest.WriteProgram(t, filepath.Join(dir, "agentapi"), "2.1.0")))

	broken := filepath.Join(dir, "broken")
	require.NoError(t, os.WriteFile(broken, []byte("#!/bin/sh\nexit 3\n"), 0o755))
	_, err := selfupdate.ProgramVersion(context.Background(), broken)
	assert.ErrorContains(t, err, "broken --version")

	silent := filepath.Join(dir, "silent")
	require.NoError(t, os.WriteFile(silent, []byte("#!/bin/sh\n"), 0o755))
	_, err = selfupdate.ProgramVersion(context.Background(), silent)
	assert.ErrorContains(t, err, "printed nothing")
}

// Locate follows a symlink to the file an update replaces.
func TestLocate(t *testing.T) {
	skipWithoutShell(t)
	t.Parallel()
	dir := t.TempDir()
	real := selfupdatetest.WriteProgram(t, filepath.Join(dir, "home", ".local", "bin", "agentapi"), "2.1.0")
	link := filepath.Join(dir, "usr", "local", "bin", "agentapi")
	require.NoError(t, os.MkdirAll(filepath.Dir(link), 0o755))
	require.NoError(t, os.Symlink(real, link))

	program, err := selfupdate.Locate(link)
	require.NoError(t, err)
	assert.Equal(t, link, program.Path)
	wantReal, err := filepath.EvalSymlinks(real)
	require.NoError(t, err)
	assert.Equal(t, wantReal, program.Real)

	_, err = selfupdate.Locate(filepath.Join(dir, "missing"))
	assert.Error(t, err)
}

// The program is replaced under the lock: of two updates at once, one
// downloads and replaces it, and the other finds the new version there.
func TestInstallUnderTheLock(t *testing.T) {
	skipWithoutShell(t)
	t.Parallel()
	release := selfupdatetest.NewRelease(t, "2.2.0", selfupdatetest.Script("2.2.0"))
	latest, err := release.Source().Latest(context.Background())
	require.NoError(t, err)
	program, err := selfupdate.Locate(selfupdatetest.WriteProgram(t, filepath.Join(t.TempDir(), "agentapi"), "2.1.0"))
	require.NoError(t, err)

	var wg sync.WaitGroup
	results := make([]bool, 2)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := release.Source().Install(context.Background(), latest, program, false)
			assert.NoError(t, err)
			results[i] = ok
		}()
	}
	wg.Wait()
	assert.ElementsMatch(t, []bool{true, false}, results, "one installs, the other finds it installed")
	assert.Equal(t, 1, release.Downloads())
	assert.Equal(t, "2.2.0", installed(t, program.Real))
	entries, err := os.ReadDir(filepath.Dir(program.Real))
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	assert.ElementsMatch(t, []string{"agentapi", ".agentapi.lock"}, names, "no download left behind")

	// --force installs again.
	ok, err := release.Source().Install(context.Background(), latest, program, true)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, 2, release.Downloads())
}

// An update waits while another holds the lock, until its context ends.
func TestInstallWaitsForTheLock(t *testing.T) {
	skipWithoutShell(t)
	t.Parallel()
	release := selfupdatetest.NewRelease(t, "2.2.0", selfupdatetest.Script("2.2.0"))
	latest, err := release.Source().Latest(context.Background())
	require.NoError(t, err)
	program, err := selfupdate.Locate(selfupdatetest.WriteProgram(t, filepath.Join(t.TempDir(), "agentapi"), "2.1.0"))
	require.NoError(t, err)
	unlock, err := program.Lock(context.Background())
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err = release.Source().Install(ctx, latest, program, false)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Zero(t, release.Downloads())

	done := make(chan error, 1)
	go func() {
		_, err := release.Source().Install(context.Background(), latest, program, false)
		done <- err
	}()
	time.Sleep(200 * time.Millisecond)
	assert.Zero(t, release.Downloads(), "still waiting")
	unlock()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("the install didn't take the lock")
	}
	assert.Equal(t, "2.2.0", installed(t, program.Real))
}

// A program on disk that is the release's version or newer stays; a
// download that doesn't run as the release's version is refused.
func TestInstallKeepsANewerProgram(t *testing.T) {
	skipWithoutShell(t)
	t.Parallel()
	release := selfupdatetest.NewRelease(t, "2.2.0", selfupdatetest.Script("2.2.0"))
	latest, err := release.Source().Latest(context.Background())
	require.NoError(t, err)
	program, err := selfupdate.Locate(selfupdatetest.WriteProgram(t, filepath.Join(t.TempDir(), "agentapi"), "2.3.0"))
	require.NoError(t, err)
	ok, err := release.Source().Install(context.Background(), latest, program, false)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Zero(t, release.Downloads())
	assert.Equal(t, "2.3.0", installed(t, program.Real))

	wrong := selfupdatetest.NewRelease(t, "2.4.0", selfupdatetest.Script("2.2.0"))
	latest, err = wrong.Source().Latest(context.Background())
	require.NoError(t, err)
	_, err = wrong.Source().Install(context.Background(), latest, program, false)
	require.ErrorContains(t, err, "says it is 2.2.0, not 2.4.0")
	assert.Equal(t, "2.3.0", installed(t, program.Real), "the program stays")

	tampered := selfupdatetest.NewRelease(t, "2.4.0", selfupdatetest.Script("2.4.0"))
	tampered.Tamper()
	latest, err = tampered.Source().Latest(context.Background())
	require.NoError(t, err)
	_, err = tampered.Source().Install(context.Background(), latest, program, false)
	require.ErrorContains(t, err, "checksum mismatch")
	assert.Equal(t, "2.3.0", installed(t, program.Real), "the program stays")
}

// A program whose directory the user can't write to is blocked, with the
// one-time fix in its real paths.
func TestBlocked(t *testing.T) {
	skipWithoutShell(t)
	if os.Geteuid() == 0 {
		t.Skip("root writes anywhere")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()

	writable := selfupdatetest.WriteProgram(t, filepath.Join(dir, "mine", "agentapi"), "2.1.0")
	program, err := selfupdate.Locate(writable)
	require.NoError(t, err)
	assert.Empty(t, program.Blocked())

	system := filepath.Join(dir, "usr", "local", "bin")
	installedThere := selfupdatetest.WriteProgram(t, filepath.Join(system, "agentapi"), "2.1.0")
	require.NoError(t, os.Chmod(system, 0o555))
	t.Cleanup(func() { _ = os.Chmod(system, 0o755) })
	program, err = selfupdate.Locate(installedThere)
	require.NoError(t, err)
	bin := filepath.Join(home, ".local", "bin")
	copied := filepath.Join(bin, "agentapi")
	assert.Equal(t, "can't replace "+program.Real+": "+filepath.Dir(program.Real)+" isn't writable. Move it to a directory you own, once: "+
		"mkdir -p "+bin+" && cp "+program.Real+" "+copied+" && sudo ln -sf "+copied+" "+program.Path, program.Blocked())

	// A link the user owns needs no sudo.
	link := filepath.Join(dir, "mine", "agentapi-link")
	require.NoError(t, os.Symlink(installedThere, link))
	program, err = selfupdate.Locate(link)
	require.NoError(t, err)
	blocked := program.Blocked()
	assert.Contains(t, blocked, "(which "+link+" links to)")
	assert.True(t, strings.HasSuffix(blocked, "&& ln -sf "+filepath.Join(bin, "agentapi-link")+" "+link), blocked)

	// Once the fix ran, the program is the user's copy behind the link.
	require.NoError(t, os.MkdirAll(bin, 0o755))
	selfupdatetest.WriteProgram(t, copied, "2.1.0")
	require.NoError(t, os.Remove(link))
	require.NoError(t, os.Symlink(copied, link))
	program, err = selfupdate.Locate(link)
	require.NoError(t, err)
	assert.Empty(t, program.Blocked())
}
