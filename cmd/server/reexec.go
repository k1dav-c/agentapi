package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/agentapi/lib/selfupdate"
)

// errRestart is what serve returns once a restart in place (SIGUSR2, an
// update through the API) stopped the server to run it again (see
// selfExec).
var errRestart = errors.New("restarting in place")

// errNotServing answers a restart in place asked for before the server
// serves or once it stops.
var errNotServing = errors.New("AgentAPI isn't serving")

// checkTimeout bounds each run of the new program before a restart: its
// --version, then its --validate.
const checkTimeout = 10 * time.Second

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

	// restartSignals catches SIGUSR2 from startup on (listenForRestart),
	// nil until then.
	restartSignals chan os.Signal

	// stateDB is the server's open state database, which the program that
	// runs again must be able to write; nil without one. runServer sets
	// it before the server serves.
	stateDB stateDBWriter
}

// stateDBWriter is what a restart asks of the open state database.
type stateDBWriter interface {
	CheckWriter(knows int, program string) error
}

// newSelfExec takes how the server was started, at startup: once an update
// replaced the program, /proc/self/exe names a deleted file.
func newSelfExec() *selfExec {
	path, _ := resolveExecutable(os.Args[0], exec.LookPath, os.Executable)
	s := &selfExec{path: path, argv: slices.Clone(os.Args), env: os.Environ()}
	s.listenForRestart()
	return s
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

// checked is what check found out about the program.
type checked struct {
	// version is what its --version says.
	version string
	// validated is set if it checked the settings (--validate); a program
	// older than --validate only runs.
	validated bool
}

// check runs the program as the restart would run it: with --version, then
// with its arguments and environment and --validate, which checks the
// settings as a start does without starting anything. A program that went
// missing, doesn't run (one for another platform, a download cut short)
// or wouldn't start with these settings (a flag it doesn't know, a value
// it refuses, a state database it can't write) then keeps the server
// serving rather than stopped.
func (s *selfExec) check(ctx context.Context) (checked, error) {
	if s.path == "" {
		return checked{}, errors.New("AgentAPI didn't find its program at startup")
	}
	out, _, err := s.run(ctx, "--version")
	if err != nil {
		return checked{}, fmt.Errorf("%s --version: %w", s.path, err)
	}
	result := checked{version: strings.TrimSpace(string(out))}
	out, stderr, err := s.run(ctx, validateArgs(s.argv)...)
	if err != nil {
		if strings.Contains(string(stderr), "unknown flag: --"+FlagValidate) {
			// AgentAPI 2.3.0 and earlier.
			return result, nil
		}
		return checked{}, fmt.Errorf("%s wouldn't start: %s", s.path, reason(stderr, err))
	}
	result.validated = true
	if s.stateDB != nil {
		schema, ok := strings.CutPrefix(strings.TrimSpace(string(out)), validateOK)
		knows, convErr := strconv.Atoi(schema)
		if !ok || convErr != nil {
			return checked{}, fmt.Errorf("%s --validate answered %q", s.path, strings.TrimSpace(string(out)))
		}
		program, _ := strings.CutPrefix(result.version, "agentapi version ")
		if err := s.stateDB.CheckWriter(knows, program); err != nil {
			return checked{}, fmt.Errorf("%s wouldn't start: the state database %w", s.path, err)
		}
	}
	return result, nil
}

// run runs the program with args and the environment it runs again with,
// for at most checkTimeout, and returns its output.
func (s *selfExec) run(ctx context.Context, args ...string) (stdout, stderr []byte, err error) {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	var errBuf bytes.Buffer
	// The program may have just been written (by an update).
	err = selfupdate.RetryTextBusy(ctx, func() error {
		cmd := exec.CommandContext(ctx, s.path, args...)
		cmd.Env = s.env
		errBuf.Reset()
		cmd.Stderr = &errBuf
		// Output waits for what the program started, which keeps its
		// stdout.
		cmd.WaitDelay = time.Second
		var err error
		stdout, err = cmd.Output()
		return err
	})
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		err = fmt.Errorf("no answer within %s", checkTimeout)
	}
	return stdout, errBuf.Bytes(), err
}

// validateArgs are the arguments of argv, the command line the server
// runs again with, with --validate: before the agent's command after --.
func validateArgs(argv []string) []string {
	var args []string
	if len(argv) > 0 {
		args = slices.Clone(argv[1:])
	}
	at := slices.Index(args, "--")
	if at < 0 {
		at = len(args)
	}
	return slices.Insert(args, at, "--"+FlagValidate)
}

// reason is why a program that failed with err says it failed: the last
// line it wrote to stderr, or err.
func reason(stderr []byte, err error) string {
	lines := strings.Split(strings.TrimSpace(string(stderr)), "\n")
	if last := strings.TrimSpace(lines[len(lines)-1]); last != "" {
		return last
	}
	return err.Error()
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
	checked, err := s.check(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stop == nil || s.served.Err() != nil || ctx.Err() != nil {
		return errNotServing
	}
	s.logger.Info("Restarting in place", "reason", why, "program", s.path, "version", checked.version, "validated", checked.validated)
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

// setEnv sets name to value in the environment the program runs again
// with, or removes it if value is empty.
func (s *selfExec) setEnv(name, value string) {
	if s == nil {
		return
	}
	s.env = slices.DeleteFunc(s.env, func(kv string) bool { return strings.HasPrefix(kv, name+"=") })
	if value != "" {
		s.env = append(s.env, name+"="+value)
	}
}

// exec runs the program again in this process. It returns only if it
// couldn't.
func (s *selfExec) exec() error {
	return execProgram(s.path, s.argv, s.env)
}
