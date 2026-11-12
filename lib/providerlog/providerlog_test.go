package providerlog

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/quartz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var t0 = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

func newLedger(t *testing.T) (*Ledger, *quartz.Mock) {
	t.Helper()
	clock := quartz.NewMock(t)
	clock.Set(t0)
	return Open(filepath.Join(t.TempDir(), "home", FileName), Options{Clock: clock}), clock
}

// Entries are lines of JSON, readable by the user only; each session's
// marks come in the order of their times, the agent's own as "".
func TestRecordAndRead(t *testing.T) {
	t.Parallel()
	l, _ := newLedger(t)
	marks, version := l.Sessions("claude")
	assert.Empty(t, marks, "no ledger yet")

	require.NoError(t, l.Record(Entry{At: t0, Agent: "claude", Session: "s1", Provider: "gw"}))
	require.NoError(t, l.Record(Entry{At: t0.Add(time.Hour), Agent: "claude", Session: "s1", Provider: "default"}))
	// Recorded out of order: a goroutine that wrote later.
	require.NoError(t, l.Record(Entry{At: t0.Add(-time.Hour), Agent: "claude", Session: "s1", Provider: "local"}))
	require.NoError(t, l.Record(Entry{At: t0, Agent: "codex", Session: "t1", Provider: "gw"}))
	require.Error(t, l.Record(Entry{At: t0, Agent: "claude"}), "no session")

	data, err := os.ReadFile(l.Path())
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	require.Len(t, lines, 4)
	assert.JSONEq(t, `{"at":"2026-11-01T13:00:00Z","agent":"claude","session":"s1","provider":"default"}`, lines[1])
	if runtime.GOOS != "windows" {
		info, err := os.Stat(l.Path())
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}

	marks, next := l.Sessions("claude")
	assert.NotEqual(t, version, next)
	assert.Equal(t, map[string][]Mark{"s1": {
		{At: t0.Add(-time.Hour), Provider: "local"},
		{At: t0, Provider: "gw"},
		{At: t0.Add(time.Hour), Provider: ""},
	}}, marks)
	codex, _ := l.Sessions("codex")
	assert.Equal(t, map[string][]Mark{"t1": {{At: t0, Provider: "gw"}}}, codex)

	// Nothing new: the same marks, the same version.
	again, same := l.Sessions("claude")
	assert.Equal(t, next, same)
	assert.Equal(t, marks, again)
}

// The ledger is read incrementally: what another process appended comes
// in, a line being written once it is whole, and the marks returned
// before stay as they were.
func TestReadIncrementally(t *testing.T) {
	t.Parallel()
	l, _ := newLedger(t)
	require.NoError(t, l.Record(Entry{At: t0, Agent: "claude", Session: "s1", Provider: "gw"}))
	before, _ := l.Sessions("claude")
	held := before["s1"]

	other := Open(l.Path(), Options{})
	require.NoError(t, other.Record(Entry{At: t0.Add(time.Minute), Agent: "claude", Session: "s1", Provider: "local"}))
	f, err := os.OpenFile(l.Path(), os.O_WRONLY|os.O_APPEND, 0)
	require.NoError(t, err)
	_, err = f.WriteString(`{"at":"2026-11-01T12:02:00Z","agent":"claude","session":"s2","pro`)
	require.NoError(t, err)

	marks, _ := l.Sessions("claude")
	assert.Equal(t, []Mark{{At: t0, Provider: "gw"}, {At: t0.Add(time.Minute), Provider: "local"}}, marks["s1"])
	assert.NotContains(t, marks, "s2", "not whole yet")
	assert.Equal(t, []Mark{{At: t0, Provider: "gw"}}, held, "unchanged")
	assert.Len(t, before, 1)

	_, err = f.WriteString("vider\":\"gw\"}\nnot json\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	marks, _ = l.Sessions("claude")
	assert.Equal(t, []Mark{{At: t0.Add(2 * time.Minute), Provider: "gw"}}, marks["s2"])
}

// Entries past Retention are dropped when a process rewrites the ledger:
// at its first record, then at most once a day. A reader reads the
// rewritten ledger again from its start.
func TestPrune(t *testing.T) {
	t.Parallel()
	l, clock := newLedger(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(l.Path()), 0o700))
	old := fmt.Sprintf(`{"at":%q,"agent":"claude","session":"old","provider":"gw"}`+"\n", t0.Add(-Retention-time.Hour).Format(time.RFC3339))
	kept := fmt.Sprintf(`{"at":%q,"agent":"claude","session":"kept","provider":"gw"}`+"\n", t0.Add(-Retention+time.Hour).Format(time.RFC3339))
	require.NoError(t, os.WriteFile(l.Path(), []byte(old+kept), 0o600))

	reader := Open(l.Path(), Options{})
	marks, _ := reader.Sessions("claude")
	assert.Len(t, marks, 2)

	require.NoError(t, l.Record(Entry{At: t0, Agent: "claude", Session: "new", Provider: "gw"}))
	data, err := os.ReadFile(l.Path())
	require.NoError(t, err)
	assert.Equal(t, kept+`{"at":"2026-11-01T12:00:00Z","agent":"claude","session":"new","provider":"gw"}`+"\n", string(data))
	marks, _ = reader.Sessions("claude")
	assert.NotContains(t, marks, "old", "read again from the start")
	assert.Contains(t, marks, "kept")
	assert.Contains(t, marks, "new")

	// Two hours on, kept is past Retention, but the ledger was rewritten
	// less than a day ago.
	clock.Advance(2 * time.Hour)
	require.NoError(t, l.Record(Entry{At: clock.Now(), Agent: "claude", Session: "new", Provider: "default"}))
	marks, _ = reader.Sessions("claude")
	assert.Contains(t, marks, "kept")
	clock.Advance(pruneInterval)
	require.NoError(t, l.Record(Entry{At: clock.Now(), Agent: "claude", Session: "new", Provider: "gw"}))
	marks, _ = reader.Sessions("claude")
	assert.NotContains(t, marks, "kept")
	assert.Len(t, marks["new"], 3)

	// Prune with nothing to drop leaves the file alone.
	info, err := os.Stat(l.Path())
	require.NoError(t, err)
	require.NoError(t, l.Prune())
	again, err := os.Stat(l.Path())
	require.NoError(t, err)
	assert.True(t, os.SameFile(info, again))
}

// Several processes append at once: every entry is a whole line.
func TestConcurrentRecords(t *testing.T) {
	t.Parallel()
	l, _ := newLedger(t)
	const writers, each = 8, 25
	var wg sync.WaitGroup
	for w := range writers {
		// A ledger each, as separate processes have.
		ledger := Open(l.Path(), Options{})
		wg.Go(func() {
			for i := range each {
				assert.NoError(t, ledger.Record(Entry{At: t0.Add(time.Duration(i) * time.Second), Agent: "claude", Session: fmt.Sprintf("s%d", w), Provider: "gw"}))
			}
		})
	}
	wg.Wait()
	marks, _ := l.Sessions("claude")
	require.Len(t, marks, writers)
	for _, m := range marks {
		assert.Len(t, m, each)
	}
}

func TestAt(t *testing.T) {
	t.Parallel()
	marks := []Mark{{At: t0, Provider: "gw"}, {At: t0.Add(time.Hour), Provider: ""}, {At: t0.Add(2 * time.Hour), Provider: "local"}}
	assert.Equal(t, "", At(marks, t0.Add(-time.Second)), "before the first: the agent's own")
	assert.Equal(t, "gw", At(marks, t0), "at a mark: its provider")
	assert.Equal(t, "gw", At(marks, t0.Add(59*time.Minute)))
	assert.Equal(t, "", At(marks, t0.Add(time.Hour)))
	assert.Equal(t, "local", At(marks, t0.Add(48*time.Hour)))
	assert.Equal(t, "", At(nil, t0))
}
