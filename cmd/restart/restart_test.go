package restart

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/agentapi/lib/instanceapi"
	"github.com/coder/agentapi/lib/registry"
)

func TestInvokingUser(t *testing.T) {
	t.Parallel()
	alice := &user.User{Username: "alice", Uid: "1001", HomeDir: "/home/alice"}
	lookup := func(name string) (*user.User, error) {
		if name == "alice" {
			return alice, nil
		}
		return nil, user.UnknownUserError(name)
	}
	home := func(dir string) func() (string, error) {
		return func() (string, error) { return dir, nil }
	}
	env := func(vars map[string]string) func(string) string {
		return func(key string) string { return vars[key] }
	}
	for name, tc := range map[string]struct {
		env       map[string]string
		euid, uid int
		home      string
		want      owner
		err       string
	}{
		"a user": {
			euid: 1000, uid: 1000, home: "/tmp/scratch-home",
			want: owner{uid: 1000, home: "/tmp/scratch-home"},
		},
		"sudo: the user who ran it, not root": {
			env:  map[string]string{"SUDO_USER": "alice", "SUDO_UID": "1001"},
			euid: 0, uid: 0, home: "/root",
			want: owner{name: "alice", uid: 1001, home: "/home/alice"},
		},
		"sudo -u: the target user": {
			env:  map[string]string{"SUDO_USER": "alice", "SUDO_UID": "1001"},
			euid: 1002, uid: 1002, home: "/home/bob",
			want: owner{uid: 1002, home: "/home/bob"},
		},
		"root without sudo": {
			euid: 0, uid: 0, home: "/root",
			want: owner{uid: 0, home: "/root"},
		},
		"sudo from root": {
			env:  map[string]string{"SUDO_USER": "root", "SUDO_UID": "0"},
			euid: 0, uid: 0, home: "/root",
			want: owner{uid: 0, home: "/root"},
		},
		"sudo, a user /etc/passwd doesn't list": {
			env:  map[string]string{"SUDO_USER": "carol", "SUDO_UID": "1005"},
			euid: 0, uid: 0, home: "/root",
			want: owner{name: "carol", uid: 1005},
		},
		"sudo, a user nobody knows": {
			env:  map[string]string{"SUDO_USER": "carol"},
			euid: 0, uid: 0, home: "/root",
			err: "find SUDO_USER carol",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := invokingUser(env(tc.env), tc.euid, tc.uid, lookup, home(tc.home))
			if tc.err != "" {
				assert.ErrorContains(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want.uid, got.uid)
			assert.Equal(t, tc.want.home, got.home)
			if tc.want.name != "" {
				assert.Equal(t, tc.want.name, got.name)
			}
		})
	}
}

func TestIsServer(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"agentapi", "server", "--port", "3286", "--", "claude"},
		{"/usr/local/bin/agentapi", "server", "--", "codex"},
		{"./agentapi-linux-amd64", "server", "--", "pi"},
		{"agentapi", "--some-flag", "server", "--", "claude"},
	} {
		assert.True(t, isServer(args), "%q", args)
	}
	for _, args := range [][]string{
		{"agentapi", "hub", "serve"},
		{"agentapi", "list"},
		{"agentapi"},
		{"python3", "server", "--", "claude"},
		{"/bin/sh", "-c", "agentapi server -- claude"},
		{"agentapi", "--", "server"},
	} {
		assert.False(t, isServer(args), "%q", args)
	}
}

// entry is a registry entry of an AgentAPI on host, port and pid.
func entry(host string, port, pid int) registry.Entry {
	return registry.Entry{WebhookInstance: instanceapi.WebhookInstance{
		ID: fmt.Sprintf("%s:%d", host, port), ServerID: fmt.Sprintf("run-%d", port), Hostname: host, Port: port, PID: pid,
	}, RestartsInPlace: true}
}

// oldRelease is the entry of an AgentAPI of a release that can't restart
// in place.
func oldRelease(host string, port, pid int) registry.Entry {
	e := entry(host, port, pid)
	e.RestartsInPlace = false
	e.Version = "2.0.0"
	return e
}

func TestSelectTargets(t *testing.T) {
	t.Parallel()
	const me = 1000
	server := []string{"/usr/local/bin/agentapi", "server", "--", "claude"}
	procs := map[int]process{
		11: {uid: me, args: server, exe: "/usr/local/bin/agentapi"},
		12: {uid: me, args: server, exe: "/usr/local/bin/agentapi"},
		13: {uid: 1001, args: server},
		14: {uid: me, args: []string{"python3", "-m", "http.server", "3288"}},
		16: {uid: me, args: server, exe: "/home/me/go/bin/agentapi"},
	}
	inspect := func(pid int) (process, error) {
		if p, ok := procs[pid]; ok {
			return p, nil
		}
		return process{}, os.ErrNotExist
	}
	entries := []registry.Entry{
		entry("box", 3285, 11),
		entry("box", 3286, 12),
		entry("box", 3287, 13),      // another user's
		entry("box", 3288, 14),      // another program took the PID
		entry("other", 3289, 11),    // another machine's, in a shared directory
		entry("box", 3290, 15),      // gone
		entry("box", 3291, 16),      // runs another binary
		entry("box", 3292, 0),       // no PID
		oldRelease("box", 3293, 12), // an earlier release
	}
	ports := func(entries []registry.Entry) []int {
		var out []int
		for _, e := range entries {
			out = append(out, e.Port)
		}
		return out
	}

	s := selectTargets(entries, Options{Ports: []int{3286}}, me, "box", inspect)
	assert.Equal(t, []int{3286}, ports(s.targets))
	assert.Empty(t, s.problems)

	s = selectTargets(entries, Options{All: true}, me, "box", inspect)
	assert.Equal(t, []int{3285, 3286, 3291}, ports(s.targets))
	require.Len(t, s.problems, 5)
	assert.Contains(t, s.problems[0], "belongs to another user (uid 1001)")
	assert.Contains(t, s.problems[1], "isn't an agentapi server (python3 -m http.server 3288)")
	assert.Contains(t, s.problems[2], "process 15 is gone")
	assert.Contains(t, s.problems[3], "has no process ID")
	assert.Equal(t, "box:3293 (:3293) runs agentapi 2.0.0, which can't restart in place; restart it yourself this once.", s.problems[4])

	s = selectTargets(entries, Options{All: true, Executable: "/usr/local/bin/agentapi"}, me, "box", inspect)
	assert.Equal(t, []int{3285, 3286}, ports(s.targets), "the binary the update replaced")
	require.Len(t, s.skipped, 1)
	assert.Contains(t, s.skipped[0], "runs /home/me/go/bin/agentapi")

	s = selectTargets(entries, Options{Ports: []int{3289, 3299}}, me, "box", inspect)
	assert.Empty(t, s.targets)
	assert.Equal(t, []string{"No AgentAPI of yours is registered on port 3289.", "No AgentAPI of yours is registered on port 3299."}, s.problems)
}

// The AgentAPI this command runs under restarts last: restarting it
// closes its agent, which may end the command.
func TestLastIfAncestor(t *testing.T) {
	t.Parallel()
	targets := []registry.Entry{entry("box", 3286, 20), entry("box", 3285, 10)}
	ordered, last := lastIfAncestor(targets, []int{30, 20, 1})
	assert.True(t, last)
	assert.Equal(t, []int{10, 20}, []int{ordered[0].PID, ordered[1].PID})
	assert.Equal(t, 20, targets[0].PID, "the targets stay as they were")
	same, last := lastIfAncestor(targets, []int{30, 1})
	assert.False(t, last)
	assert.Equal(t, targets, same)
}

func TestProcessOfThisProcess(t *testing.T) {
	t.Parallel()
	proc, err := processOf(os.Getpid())
	if errors.Is(err, os.ErrNotExist) {
		t.Skip("no /proc")
	}
	require.NoError(t, err)
	assert.Equal(t, os.Getuid(), proc.uid)
	assert.Equal(t, os.Getppid(), proc.ppid)
	assert.Equal(t, os.Args, proc.args)
	if proc.exe != "" {
		exe, err := os.Executable()
		require.NoError(t, err)
		assert.True(t, samePath(exe, proc.exe))
	}
	assert.Contains(t, ancestors(os.Getpid()), os.Getppid())
}

// awaitRestart waits for a registry entry of the same process with a new
// server ID whose port answers as that server.
func TestAwaitRestart(t *testing.T) {
	t.Parallel()
	hostname, _ := os.Hostname()
	var serverID string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"server_id":%q}`, serverID)
	}))
	defer ts.Close()
	u, err := url.Parse(ts.URL)
	require.NoError(t, err)
	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)
	dir := filepath.Join(t.TempDir(), "instances")
	old := entry(hostname, port, os.Getpid())
	alive := func(int) (process, error) { return process{}, nil }

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err = awaitRestart(ctx, dir, old, ts.Client(), alive)
	assert.ErrorContains(t, err, "didn't serve again in time", "no new entry")
	_, err = registry.Write(dir, old)
	require.NoError(t, err)
	ctx, cancel = context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err = awaitRestart(ctx, dir, old, ts.Client(), alive)
	assert.ErrorContains(t, err, "still runs as before", "the server refused")
	require.NoError(t, os.Remove(filepath.Join(dir, old.ServerID+".json")))

	restarted := old
	restarted.ServerID = "run-2"
	restarted.Version = "2.1.0"
	_, err = registry.Write(dir, restarted)
	require.NoError(t, err)
	ctx, cancel = context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err = awaitRestart(ctx, dir, old, ts.Client(), alive)
	assert.Error(t, err, "the port answers as another server")

	serverID = "run-2"
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := awaitRestart(ctx, dir, old, ts.Client(), alive)
	require.NoError(t, err)
	assert.Equal(t, "2.1.0", got.Version)

	gone := func(int) (process, error) { return process{}, os.ErrNotExist }
	_, err = awaitRestart(context.Background(), dir, old, ts.Client(), gone)
	assert.ErrorContains(t, err, "exited")
}

// A server that asks for a token answers 401, which counts: its server ID
// can't be read without the token.
func TestAnswersWithAToken(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer ts.Close()
	e := entry("box", 0, 1)
	e.URL = ts.URL
	assert.True(t, answers(context.Background(), ts.Client(), e))
	assert.Equal(t, []string{ts.URL}, probeURLs(e))
	e.Port, e.URL = 3286, "https://3286--app--dev.coder.example"
	assert.Equal(t, []string{"http://127.0.0.1:3286"}, probeURLs(e), "not through a sign-in")
}
