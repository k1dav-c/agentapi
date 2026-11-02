// Package restart implements `agentapi restart`: the AgentAPIs running on
// this machine restart in place (SIGUSR2) with the program they were
// started as, which an update may have replaced, and it waits until they
// serve again.
package restart

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/coder/agentapi/lib/registry"
)

const (
	// DefaultTimeout is how long Run waits for each AgentAPI to serve again.
	DefaultTimeout = time.Minute
	pollInterval   = 200 * time.Millisecond
	probeTimeout   = 2 * time.Second
)

var (
	allFlag     bool
	registryDir string
	timeout     time.Duration
)

var RestartCmd = &cobra.Command{
	Use:   "restart [port...]",
	Short: "Restart running AgentAPIs in place",
	Long: "Have the AgentAPIs running on this machine (in the registry, see agentapi list) restart in place: " +
		"each stops as for a shutdown and runs the program it was started as again, in the same process, " +
		"so a new binary that agentapi update installed takes over. Name the ports to restart, or --all. " +
		"Under sudo, the AgentAPIs of the user who ran sudo restart.",
	RunE: func(cmd *cobra.Command, args []string) error {
		ports := make([]int, 0, len(args))
		for _, arg := range args {
			port, err := strconv.Atoi(strings.TrimPrefix(arg, ":"))
			if err != nil || port <= 0 {
				return fmt.Errorf("%q isn't a port", arg)
			}
			ports = append(ports, port)
		}
		if len(ports) == 0 && !allFlag {
			return errors.New("name the ports of the AgentAPIs to restart, or --all")
		}
		cmd.SilenceUsage = true
		return Run(cmd.Context(), Options{Ports: ports, All: allFlag, RegistryDir: registryDir, Timeout: timeout, Out: cmd.OutOrStdout()})
	},
}

func init() {
	RestartCmd.Flags().BoolVar(&allFlag, "all", false, "Restart every AgentAPI of yours running on this machine")
	RestartCmd.Flags().StringVar(&registryDir, "registry-dir", "", "Registry directory (default ~/.agentapi/instances of the user, also under sudo)")
	RestartCmd.Flags().DurationVar(&timeout, "timeout", DefaultTimeout, "How long to wait for each AgentAPI to serve again")
}

// Options says which AgentAPIs Run restarts.
type Options struct {
	// Ports are those of the AgentAPIs to restart; All restarts every one.
	Ports []int
	All   bool
	// RegistryDir is the registry to read; empty is the invoking user's.
	RegistryDir string
	// Executable, if set, restarts only the AgentAPIs that run that
	// program (the one an update replaced), where the system says which.
	Executable string
	// Timeout bounds the wait for each AgentAPI; zero is DefaultTimeout.
	Timeout time.Duration
	Out     io.Writer
}

// Run restarts the AgentAPIs opts names, one after another, and waits
// until each serves again. Only the invoking user's AgentAPIs restart,
// and only those whose registry entry names a running agentapi server
// process. The one this command runs under, if any, goes last.
func Run(ctx context.Context, opts Options) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	owner, err := invokingUser(os.Getenv, os.Geteuid(), os.Getuid(), user.Lookup, os.UserHomeDir)
	if err != nil {
		return err
	}
	dir := opts.RegistryDir
	if dir == "" {
		if owner.home == "" {
			return fmt.Errorf("can't find the home directory of %s; pass --registry-dir", owner.name)
		}
		dir = registry.DirIn(owner.home)
	}
	entries, err := registry.List(dir)
	if err != nil {
		return fmt.Errorf("read the registry %s: %w", dir, err)
	}
	hostname, _ := os.Hostname()
	selection := selectTargets(entries, opts, owner.uid, hostname, processOf)
	for _, note := range selection.skipped {
		_, _ = fmt.Fprintln(opts.Out, note)
	}
	for _, problem := range selection.problems {
		_, _ = fmt.Fprintln(opts.Out, problem)
	}
	if len(selection.targets) == 0 {
		if len(selection.problems) > 0 {
			return errors.New("no AgentAPI restarted")
		}
		_, _ = fmt.Fprintln(opts.Out, "No AgentAPI to restart.")
		return nil
	}
	targets, last := lastIfAncestor(selection.targets, ancestors(os.Getpid()))
	failed := len(selection.problems)
	for i, target := range targets {
		name := describe(target)
		if last && i == len(targets)-1 {
			_, _ = fmt.Fprintf(opts.Out, "This command runs under %s, so it restarts that one last and may end with it.\n", name)
		}
		_, _ = fmt.Fprintf(opts.Out, "Restarting %s…\n", name)
		if err := restartOne(ctx, dir, target, opts.Timeout, opts.Out); err != nil {
			_, _ = fmt.Fprintf(opts.Out, "%s didn't restart: %v\n", name, err)
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d AgentAPIs didn't restart", failed, len(targets)+len(selection.problems))
	}
	return nil
}

// restartOne signals target's process and waits until it serves again.
func restartOne(ctx context.Context, dir string, target registry.Entry, timeout time.Duration, out io.Writer) error {
	start := time.Now()
	if err := signalRestart(target.PID); err != nil {
		return fmt.Errorf("signal process %d: %w", target.PID, err)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	entry, err := awaitRestart(ctx, dir, target, &http.Client{Timeout: probeTimeout}, processOf)
	if err != nil {
		return err
	}
	version := ""
	if entry.Version != "" {
		version = ", agentapi " + entry.Version
	}
	_, _ = fmt.Fprintf(out, "%s serves again after %s (pid %d%s).\n", describe(entry), time.Since(start).Round(100*time.Millisecond), entry.PID, version)
	return nil
}

// owner is the user whose AgentAPIs a restart signals, and where their
// registry is.
type owner struct {
	name string
	uid  int
	// home is empty when it can't be found.
	home string
}

// invokingUser returns who ran agentapi: under sudo (root with SUDO_USER),
// the user who ran sudo rather than root. Otherwise it is the process's
// user, with $HOME as the home directory, as agentapi server has it.
func invokingUser(getenv func(string) string, euid, uid int, lookup func(string) (*user.User, error), homeDir func() (string, error)) (owner, error) {
	if name := getenv("SUDO_USER"); euid == 0 && name != "" && name != "root" {
		u, err := lookup(name)
		if err == nil {
			id, err := strconv.Atoi(u.Uid)
			if err != nil {
				return owner{}, fmt.Errorf("the user ID of %s: %w", name, err)
			}
			return owner{name: name, uid: id, home: u.HomeDir}, nil
		}
		// Not in /etc/passwd, which is all a static build reads.
		id, convErr := strconv.Atoi(getenv("SUDO_UID"))
		if convErr != nil {
			return owner{}, fmt.Errorf("find SUDO_USER %s: %w", name, err)
		}
		return owner{name: name, uid: id}, nil
	}
	home, _ := homeDir()
	name := strconv.Itoa(uid)
	if u, err := user.LookupId(name); err == nil {
		name = u.Username
	}
	return owner{name: name, uid: uid, home: home}, nil
}

// process is what the system says of a running process.
type process struct {
	uid  int
	ppid int
	// args is its command line.
	args []string
	// exe is its program's path, without " (deleted)"; empty where the
	// system doesn't say.
	exe string
}

// selection is what selectTargets found.
type selection struct {
	targets []registry.Entry
	// skipped run another program than Options.Executable; problems are
	// entries that can't restart, and ports that have none.
	skipped, problems []string
}

// selectTargets picks the entries opts names whose process runs as uid:
// on hostname (a shared directory holds other machines' entries), and an
// agentapi server process, which inspect describes.
func selectTargets(entries []registry.Entry, opts Options, uid int, hostname string, inspect func(pid int) (process, error)) selection {
	var s selection
	found := map[int]bool{}
	for _, entry := range entries {
		if entry.Hostname != hostname || (!opts.All && !slices.Contains(opts.Ports, entry.Port)) {
			continue
		}
		found[entry.Port] = true
		name := describe(entry)
		if entry.PID <= 0 {
			s.problems = append(s.problems, name+" has no process ID in the registry; not restarting it.")
			continue
		}
		proc, err := inspect(entry.PID)
		switch {
		case err != nil:
			s.problems = append(s.problems, fmt.Sprintf("%s: process %d is gone: %v.", name, entry.PID, err))
		case proc.uid != uid:
			s.problems = append(s.problems, fmt.Sprintf("%s: process %d belongs to another user (uid %d); not restarting it.", name, entry.PID, proc.uid))
		case !isServer(proc.args):
			s.problems = append(s.problems, fmt.Sprintf("%s: process %d isn't an agentapi server (%s); not restarting it.", name, entry.PID, strings.Join(proc.args, " ")))
		case !entry.RestartsInPlace:
			s.problems = append(s.problems, fmt.Sprintf("%s runs agentapi %s, which can't restart in place; restart it yourself this once.", name, cmp.Or(entry.Version, "of an earlier release")))
		case opts.Executable != "" && proc.exe != "" && !samePath(proc.exe, opts.Executable):
			s.skipped = append(s.skipped, fmt.Sprintf("%s runs %s, not %s; not restarting it.", name, proc.exe, opts.Executable))
		default:
			s.targets = append(s.targets, entry)
		}
	}
	for _, port := range opts.Ports {
		if !found[port] {
			s.problems = append(s.problems, fmt.Sprintf("No AgentAPI of yours is registered on port %d.", port))
		}
	}
	return s
}

// isServer reports whether args, a command line, runs agentapi server.
func isServer(args []string) bool {
	if len(args) < 2 || !strings.HasPrefix(filepath.Base(args[0]), "agentapi") {
		return false
	}
	for _, arg := range args[1:] {
		if arg == "--" {
			return false
		}
		if !strings.HasPrefix(arg, "-") {
			return arg == "server"
		}
	}
	return false
}

// samePath reports whether paths a and b name the same file, or are the
// same path once cleaned.
func samePath(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	infoA, errA := os.Stat(a)
	infoB, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(infoA, infoB)
}

// lastIfAncestor moves the target this command runs under (one of
// ancestors) to the end, and reports whether there was one: restarting it
// closes its agent, which may end this command.
func lastIfAncestor(targets []registry.Entry, ancestors []int) ([]registry.Entry, bool) {
	for i, target := range targets {
		if slices.Contains(ancestors, target.PID) {
			ordered := append(slices.Delete(slices.Clone(targets), i, i+1), target)
			return ordered, true
		}
	}
	return targets, false
}

// ancestors returns the IDs of the processes above pid, its parent first.
func ancestors(pid int) []int {
	var out []int
	for len(out) < 64 && pid > 1 {
		proc, err := processOf(pid)
		if err != nil || proc.ppid <= 0 || slices.Contains(out, proc.ppid) {
			break
		}
		out = append(out, proc.ppid)
		pid = proc.ppid
	}
	return out
}

// describe names an AgentAPI in the output.
func describe(entry registry.Entry) string {
	name := entry.Name
	if name == "" {
		name = entry.ID
	}
	return fmt.Sprintf("%s (:%d)", name, entry.Port)
}

// awaitRestart waits until the AgentAPI of old serves again and returns
// its new entry: one for the same process with a new server ID, which it
// writes once it listens again, whose port answers GET /status as that
// server, or asks for a token.
func awaitRestart(ctx context.Context, dir string, old registry.Entry, client *http.Client, inspect func(pid int) (process, error)) (registry.Entry, error) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		if _, err := inspect(old.PID); err != nil {
			return registry.Entry{}, fmt.Errorf("its process %d exited; see its log", old.PID)
		}
		if entries, err := registry.List(dir); err == nil {
			for _, entry := range entries {
				if entry.PID == old.PID && entry.ServerID != old.ServerID && answers(ctx, client, entry) {
					return entry, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			if stillRuns(dir, old) {
				// The server checks that its program runs before it stops.
				return registry.Entry{}, errors.New("it still runs as before: it didn't restart, and its log says why")
			}
			return registry.Entry{}, fmt.Errorf("it didn't serve again in time (%w)", ctx.Err())
		case <-ticker.C:
		}
	}
}

// stillRuns reports whether old's entry is still in the registry.
func stillRuns(dir string, old registry.Entry) bool {
	entries, _ := registry.List(dir)
	return slices.ContainsFunc(entries, func(e registry.Entry) bool { return e.ServerID == old.ServerID })
}

// answers reports whether entry's AgentAPI answers GET /status as that
// server (its server ID), or asks for a token, whose answer has none.
func answers(ctx context.Context, client *http.Client, entry registry.Entry) bool {
	for _, base := range probeURLs(entry) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/status", nil)
		if err != nil {
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		var status struct {
			ServerID string `json:"server_id"`
		}
		decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&status)
		_ = resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusUnauthorized:
			return true
		case resp.StatusCode == http.StatusOK && decodeErr == nil && status.ServerID == entry.ServerID:
			return true
		}
	}
	return false
}

// probeURLs are where this machine reaches entry's AgentAPI: its port on
// localhost, then its URL when that is plain HTTP (one that listens on a
// single address).
func probeURLs(entry registry.Entry) []string {
	var urls []string
	if entry.Port > 0 {
		urls = append(urls, fmt.Sprintf("http://127.0.0.1:%d", entry.Port))
	}
	if url := strings.TrimRight(entry.URL, "/"); strings.HasPrefix(url, "http://") && !slices.Contains(urls, url) {
		urls = append(urls, url)
	}
	return urls
}
