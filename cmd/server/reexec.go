package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// errRestart is what serve returns once a restart in place (SIGUSR2, an
// update through the API) stopped the server to run it again (see
// selfExec).
var errRestart = errors.New("restarting in place")

// errNotServing answers a restart in place asked for before the server
// serves or once it stops.
var errNotServing = errors.New("AgentAPI isn't serving")

// versionCheckTimeout bounds the run of the new program's --version.
const versionCheckTimeout = 10 * time.Second

// selfExec runs the server again in place (SIGUSR2, an update through the
// API): in the same process, so its PID, a PID file and a parent that
// waits for it stay valid, with the program it was started as, its
// arguments and its environment. After an update replaced the program,
// the new one runs.
type selfExec struct {
	// path is the program, found at startup; empty if it wasn't.
	path string
	argv []string
	env  []string
	// requested is set once a restart stopped the server to run it again;
	// a shutdown signal clears it.
	requested atomic.Bool

	// mu makes a restart and a shutdown one after the other, so that the
	// second sees what the first did.
	mu sync.Mutex
	// served ends once the server stops, which stop does; both are nil
	// until serve arms the restart.
	served context.Context
	stop   context.CancelFunc
	logger *slog.Logger
}

// newSelfExec takes how the server was started, at startup: once an update
// replaced the program, /proc/self/exe names a deleted file.
func newSelfExec() *selfExec {
	path, _ := resolveExecutable(os.Args[0], exec.LookPath, os.Executable)
	return &selfExec{path: path, argv: slices.Clone(os.Args), env: os.Environ()}
}

// resolveExecutable returns the program to run again: argv0 as the shell
// found it (a path, or a name on PATH), if that is the program running;
// else the running program. argv0 follows a symlink that an update points
// to a new version, where the running program is the old one's file.
func resolveExecutable(argv0 string, lookPath func(string) (string, error), executable func() (string, error)) (string, error) {
	exe, exeErr := executable()
	var named string
	if strings.ContainsRune(argv0, '/') || strings.ContainsRune(argv0, filepath.Separator) {
		named = argv0
	} else if found, err := lookPath(argv0); err == nil {
		named = found
	}
	if named != "" {
		if abs, err := filepath.Abs(named); err == nil {
			named = abs
		}
		if exeErr == nil && sameFile(named, exe) {
			return named, nil
		}
		if exeErr != nil {
			if _, err := os.Stat(named); err == nil {
				return named, nil
			}
		}
	}
	if exeErr != nil {
		return "", exeErr
	}
	return exe, nil
}

// sameFile reports whether paths a and b name the same file.
func sameFile(a, b string) bool {
	infoA, errA := os.Stat(a)
	infoB, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(infoA, infoB)
}

// check runs the program with --version, as the restart would run it, and
// returns what it says. A program that went missing or doesn't run (one
// for another platform, a download cut short) then keeps the server
// serving rather than stopped.
func (s *selfExec) check(ctx context.Context) (string, error) {
	if s.path == "" {
		return "", errors.New("AgentAPI didn't find its program at startup")
	}
	ctx, cancel := context.WithTimeout(ctx, versionCheckTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.path, "--version")
	cmd.Env = s.env
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s --version: %w", s.path, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// arm has restart stop the server, whose context is served, with stop.
// serve calls it before it serves.
func (s *selfExec) arm(served context.Context, stop context.CancelFunc, logger *slog.Logger) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.served, s.stop, s.logger = served, stop, logger
}

// restart restarts the server in place: once its program runs (check),
// the server stops as for a shutdown, after which serve returns errRestart
// and the program runs again. It returns why it can't; the server goes on
// serving then. why says who asked, for the log.
func (s *selfExec) restart(ctx context.Context, why string) error {
	version, err := s.check(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stop == nil || s.served.Err() != nil || ctx.Err() != nil {
		return errNotServing
	}
	s.logger.Info("Restarting in place", "reason", why, "program", s.path, "version", version)
	s.requested.Store(true)
	s.stop()
	return nil
}

// shutdown stops the server with cancel for good: it wins over a restart
// under way.
func (s *selfExec) shutdown(cancel context.CancelFunc) {
	if s == nil {
		cancel()
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requested.Store(false)
	cancel()
}

// restarting reports whether a restart asked to run the server again.
func (s *selfExec) restarting() bool {
	return s != nil && s.requested.Load()
}

// exec runs the program again in this process. It returns only if it
// couldn't.
func (s *selfExec) exec() error {
	return execProgram(s.path, s.argv, s.env)
}
