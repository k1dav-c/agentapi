package selfupdate

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/Masterminds/semver/v3"

	"github.com/coder/agentapi/lib/filelock"
)

// versionTimeout bounds a run of a program's --version.
const versionTimeout = 10 * time.Second

// Program is the program an update replaces.
type Program struct {
	// Path is the program as it is started. It may be a symlink, such as
	// /usr/local/bin/agentapi to ~/.local/bin/agentapi: the update leaves
	// it and replaces the file it names.
	Path string
	// Real is the file Path names, symlinks resolved. An update writes a
	// new file in its directory and renames it over Real.
	Real string
}

// Locate finds the file path names, following symlinks.
func Locate(path string) (Program, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Program{}, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return Program{}, fmt.Errorf("resolve %s: %w", path, err)
	}
	return Program{Path: abs, Real: real}, nil
}

// Blocked says why the user can't replace the program, and what to run
// once so that they can; it returns "" when they can. Replacing the
// program takes write access to its directory. One in a system directory,
// such as /usr/local/bin, is copied to ~/.local/bin, and its path becomes
// a symlink to the copy, which the user can replace without sudo.
func (p Program) Blocked() string {
	dir := filepath.Dir(p.Real)
	if writable(dir) == nil {
		return ""
	}
	what := p.Real
	// A path that differs from Real only by a symlinked directory (macOS's
	// /var, which is /private/var) isn't a link to it.
	if info, err := os.Lstat(p.Path); err == nil && info.Mode()&fs.ModeSymlink != 0 {
		what = fmt.Sprintf("%s (which %s links to)", p.Real, p.Path)
	}
	bin := "~/.local/bin"
	if home, err := os.UserHomeDir(); err == nil {
		bin = filepath.Join(home, ".local", "bin")
	}
	copied := filepath.Join(bin, filepath.Base(p.Path))
	if copied == p.Real {
		return fmt.Sprintf("can't replace %s: %s isn't writable", what, dir)
	}
	sudo := "sudo "
	if writable(filepath.Dir(p.Path)) == nil {
		sudo = ""
	}
	return fmt.Sprintf("can't replace %s: %s isn't writable. Move it to a directory you own, once: mkdir -p %s && cp %s %s && %sln -sf %s %s",
		what, dir, shellQuote(bin), shellQuote(p.Real), shellQuote(copied), sudo, shellQuote(copied), shellQuote(p.Path))
}

// shellSafe matches what a shell reads as one word as it is.
var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./~-]+$`)

// shellQuote quotes path for a shell, if it needs it.
func shellQuote(path string) string {
	if shellSafe.MatchString(path) {
		return path
	}
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}

// lockPath is the lock file next to the program.
func (p Program) lockPath() string {
	return filepath.Join(filepath.Dir(p.Real), "."+filepath.Base(p.Real)+".lock")
}

// Lock takes the lock file next to the program (.agentapi.lock), waiting
// while another update holds it until ctx ends: the AgentAPIs that share
// the program replace it one at a time. The file stays.
func (p Program) Lock(ctx context.Context) (unlock func(), err error) {
	return filelock.Lock(ctx, p.lockPath())
}

// RetryTextBusy runs run, which runs a program, again while it fails
// because the program is busy, for about a second or until ctx ends, and
// returns its last error. A program just written can't run while a
// process forked at the same time still holds the file open for writing,
// until that one execs (Linux's ETXTBSY, golang.org/issue/22315). It is
// let go within moments.
func RetryTextBusy(ctx context.Context, run func() error) error {
	err := run()
	for wait := 10 * time.Millisecond; errors.Is(err, syscall.ETXTBSY) && wait < time.Second; wait *= 2 {
		select {
		case <-ctx.Done():
			return err
		case <-time.After(wait):
		}
		err = run()
	}
	return err
}

// ProgramVersion runs the program at path with --version and returns the
// version it prints (agentapi version 1.2.3).
func ProgramVersion(ctx context.Context, path string) (*semver.Version, error) {
	ctx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	var out []byte
	err := RetryTextBusy(ctx, func() (err error) {
		out, err = exec.CommandContext(ctx, path, "--version").Output()
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("%s --version: %w", path, err)
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return nil, fmt.Errorf("%s --version printed nothing", path)
	}
	return ParseVersion(fields[len(fields)-1])
}

// Install puts the release's binary for this platform in place of the
// program, holding the lock: it downloads it into the program's directory,
// checks it against the release's checksums.txt, that it runs and is the
// release's version, and s.Check if set, and renames it over the program. Unless force,
// a program that is the release's version or newer by the time Install
// holds the lock stays, as another AgentAPI installed it meanwhile, and
// Install reports false.
func (s Source) Install(ctx context.Context, r *Release, p Program, force bool) (bool, error) {
	want, err := r.Version()
	if err != nil {
		return false, err
	}
	unlock, err := p.Lock(ctx)
	if err != nil {
		return false, err
	}
	defer unlock()
	if !force {
		if have, err := ProgramVersion(ctx, p.Real); err == nil && !have.LessThan(want) {
			return false, nil
		}
	}
	tmp, err := s.Download(ctx, r, PlatformAsset(), filepath.Dir(p.Real))
	if err != nil {
		return false, err
	}
	defer func() {
		// Still there unless the rename took it.
		if _, err := os.Lstat(tmp); err == nil {
			_ = os.Remove(tmp)
		}
	}()
	got, err := ProgramVersion(ctx, tmp)
	if err != nil {
		return false, fmt.Errorf("the downloaded binary doesn't run: %w", err)
	}
	if !got.Equal(want) {
		return false, fmt.Errorf("the downloaded binary says it is %s, not %s", got, want)
	}
	if s.Check != nil {
		if err := s.Check(ctx, tmp); err != nil {
			return false, err
		}
	}
	if err := Replace(tmp, p.Real); err != nil {
		return false, fmt.Errorf("replace binary: %w", err)
	}
	return true, nil
}
