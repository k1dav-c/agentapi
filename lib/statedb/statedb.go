// Package statedb keeps a native session's conversation in a SQLite file so
// it survives AgentAPI restarts: messages, rich messages, timeline events,
// requests, the queue, and what is needed to resume the agent's session.
//
// Writes are asynchronous: callers enqueue changes, which a background
// goroutine applies in batched transactions. Close flushes what is pending.
package statedb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	sqlite "modernc.org/sqlite" // SQLite driver without cgo.
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/coder/agentapi/lib/jsonlwatcher"
	"github.com/coder/agentapi/lib/native"
	st "github.com/coder/agentapi/lib/screentracker"
)

// flushInterval is how long changes may wait before they are written.
const flushInterval = 200 * time.Millisecond

// maxPending is how many pending changes are written without waiting for
// the next flush.
const maxPending = 256

// A batch of changes that fails to commit is tried writeAttempts times in
// all, waiting retryDelay before the first retry and twice as long before
// each next one, before its changes are given up.
const (
	writeAttempts = 4
	retryDelay    = 50 * time.Millisecond
)

// migrations take the schema from version i to i+1, so migrations[0]
// creates it; PRAGMA user_version holds the version. A change to the
// schema is a new migration at the end: one that ran never changes.
var migrations = []string{
	`CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS messages (id INTEGER PRIMARY KEY, seq INTEGER NOT NULL, data TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS rich_messages (pos INTEGER PRIMARY KEY, seq INTEGER NOT NULL, data TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS session_events (id INTEGER PRIMARY KEY, data TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS requests (id TEXT PRIMARY KEY, pos INTEGER NOT NULL, seq INTEGER NOT NULL, data TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS queue (pos INTEGER PRIMARY KEY, data TEXT NOT NULL);`,
}

// Meta keys.
const (
	MetaAgentType   = "agent_type"
	MetaTransport   = "transport"
	MetaCWD         = "cwd"
	MetaEpoch       = "epoch"
	MetaSeq         = "seq"
	MetaSessionRef  = "session_ref"
	MetaSessionName = "session_name"
	// MetaSettings holds the settings chosen for the session (JSON). A
	// reset keeps them.
	MetaSettings    = "settings"
	MetaNextQueueID = "next_queue_id"
	// metaClean is set while the file is closed cleanly; see Snapshot.Clean.
	metaClean = "clean"
)

// Keys of pending writes; see op.
const (
	metaKeyPrefix = "meta:"
	queueKey      = "queue"
)

// Message is a stored conversation message with the seq of its last change.
type Message struct {
	Message st.ConversationMessage
	Seq     uint64
}

// RichMessage is a stored rich message with the seq of its last change.
type RichMessage struct {
	Message jsonlwatcher.RichMessage
	Seq     uint64
}

// Request is a stored request with the seq of its last change.
type Request struct {
	State native.RequestState
	Seq   uint64
}

// QueuedMessage is a stored queued user message.
type QueuedMessage struct {
	ID      int       `json:"id"`
	Content string    `json:"content"`
	Time    time.Time `json:"time"`
}

// Snapshot is everything stored.
type Snapshot struct {
	Meta         map[string]string
	Messages     []Message
	RichMessages []RichMessage
	Events       []jsonlwatcher.SessionEvent
	Requests     []Request
	Queue        []QueuedMessage
	// Clean is set when the AgentAPI that wrote the file closed it. After a
	// crash it is not, and the last changes before the crash may be
	// missing.
	Clean bool
}

// Empty reports whether nothing was stored yet.
func (s Snapshot) Empty() bool { return len(s.Meta) == 0 }

// Seq returns the stored seq.
func (s Snapshot) Seq() uint64 {
	n, _ := strconv.ParseUint(s.Meta[MetaSeq], 10, 64)
	return n
}

// NextQueueID returns the stored next queue ID.
func (s Snapshot) NextQueueID() int {
	n, _ := strconv.Atoi(s.Meta[MetaNextQueueID])
	return n
}

// op is a pending write. Ops with the same key replace each other while
// pending, so only the latest state of a row is written.
type op struct {
	key string
	run func(*sql.Tx) error
}

// DB is an open state database.
type DB struct {
	db     *sql.DB
	logger *slog.Logger

	mu      sync.Mutex
	pending []op
	// index maps the key of a pending op to its position, for ops queued
	// since the last keyless op.
	index  map[string]int
	wake   chan struct{}
	closed bool
	// lost is the error that made changes be given up. The file then
	// misses changes, so Close doesn't mark it clean. failing is set while
	// writes fail, so onWriteError is called once per outage.
	lost         error
	failing      bool
	onWriteError func(error)
	done         chan struct{}
	stop         context.CancelFunc
	// writeMu serializes writes, so batches commit in the order they were
	// taken from pending.
	writeMu sync.Mutex
}

// Open opens or creates the database at path, and its directory. Both are
// created accessible to their owner only: they hold the conversation. A
// file can be open in one AgentAPI at a time.
func Open(path string, logger *slog.Logger) (*DB, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create state database directory: %w", err)
	}
	// SQLite creates the -wal file with the mode of the database file.
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open state database: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("open state database: %w", err)
	}
	sqlDB, err := sql.Open("sqlite", dataSourceName(path))
	if err != nil {
		return nil, fmt.Errorf("open state database: %w", err)
	}
	// One connection: SQLite has one writer, and the pragmas and the lock
	// belong to the connection.
	sqlDB.SetMaxOpenConns(1)
	// In exclusive locking mode the lock of the first write is held until
	// the database is closed. Take it now, so a second AgentAPI fails here
	// instead of mixing its changes with this one's.
	if _, err := sqlDB.Exec("BEGIN IMMEDIATE; COMMIT"); err != nil {
		_ = sqlDB.Close()
		if isBusy(err) {
			return nil, fmt.Errorf("state database %s is in use by another AgentAPI", path)
		}
		return nil, fmt.Errorf("open state database: %w", err)
	}
	if err := migrate(sqlDB); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	d := &DB{
		db:     sqlDB,
		logger: logger,
		index:  make(map[string]int),
		wake:   make(chan struct{}, 1),
		done:   make(chan struct{}),
		stop:   cancel,
	}
	go d.writeLoop(ctx)
	return d, nil
}

// dataSourceName returns the SQLite URI of the database at path.
// busy_timeout lets an AgentAPI that is still closing the file finish
// first. locking_mode(EXCLUSIVE) must come before journal_mode(WAL): the
// WAL index is then kept in memory rather than in a -shm file other
// processes could open.
func dataSourceName(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	// ?, # and % have a meaning in a URI.
	path = strings.NewReplacer("%", "%25", "?", "%3F", "#", "%23").Replace(path)
	return "file:" + path + "?_pragma=busy_timeout(1000)&_pragma=locking_mode(EXCLUSIVE)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
}

// isBusy reports whether err says that another connection holds the lock.
func isBusy(err error) bool {
	var sqliteErr *sqlite.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code()&0xff == sqlite3.SQLITE_BUSY
}

// migrate runs the migrations the database hasn't had.
func migrate(db *sql.DB) error {
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read state database version: %w", err)
	}
	if version > len(migrations) {
		return fmt.Errorf("state database was written by a newer AgentAPI (schema %d, this one knows %d)", version, len(migrations))
	}
	for ; version < len(migrations); version++ {
		if err := migrateTo(db, version+1); err != nil {
			return fmt.Errorf("migrate the state database to schema %d: %w", version+1, err)
		}
	}
	return nil
}

// migrateTo runs the migration to version, and records the version, in one
// transaction.
func migrateTo(db *sql.DB, version int) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(migrations[version-1]); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Load reads everything stored. Call it before any writes. From then on
// the file doesn't count as closed cleanly until Close.
func (d *DB) Load() (Snapshot, error) {
	snap := Snapshot{Meta: map[string]string{}}
	rows, err := d.db.Query("SELECT key, value FROM meta")
	if err != nil {
		return snap, fmt.Errorf("read meta: %w", err)
	}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			_ = rows.Close()
			return snap, err
		}
		snap.Meta[k] = v
	}
	if err := closeRows(rows); err != nil {
		return snap, err
	}
	_, snap.Clean = snap.Meta[metaClean]
	delete(snap.Meta, metaClean)

	if err := d.scanJSON("messages", "SELECT seq, data FROM messages ORDER BY id", func(seq uint64, data []byte) error {
		var m st.ConversationMessage
		if err := json.Unmarshal(data, &m); err != nil {
			return err
		}
		snap.Messages = append(snap.Messages, Message{Message: m, Seq: seq})
		return nil
	}); err != nil {
		return snap, fmt.Errorf("read messages: %w", err)
	}
	if err := d.scanJSON("rich_messages", "SELECT seq, data FROM rich_messages ORDER BY pos", func(seq uint64, data []byte) error {
		var m jsonlwatcher.RichMessage
		if err := json.Unmarshal(data, &m); err != nil {
			return err
		}
		snap.RichMessages = append(snap.RichMessages, RichMessage{Message: m, Seq: seq})
		return nil
	}); err != nil {
		return snap, fmt.Errorf("read rich messages: %w", err)
	}
	if err := d.scanJSON("session_events", "SELECT 0, data FROM session_events ORDER BY id", func(_ uint64, data []byte) error {
		var e jsonlwatcher.SessionEvent
		if err := json.Unmarshal(data, &e); err != nil {
			return err
		}
		snap.Events = append(snap.Events, e)
		return nil
	}); err != nil {
		return snap, fmt.Errorf("read timeline: %w", err)
	}
	if err := d.scanJSON("requests", "SELECT seq, data FROM requests ORDER BY pos", func(seq uint64, data []byte) error {
		var r native.RequestState
		if err := json.Unmarshal(data, &r); err != nil {
			return err
		}
		snap.Requests = append(snap.Requests, Request{State: r, Seq: seq})
		return nil
	}); err != nil {
		return snap, fmt.Errorf("read requests: %w", err)
	}
	if err := d.scanJSON("queue", "SELECT 0, data FROM queue ORDER BY pos", func(_ uint64, data []byte) error {
		var q QueuedMessage
		if err := json.Unmarshal(data, &q); err != nil {
			return err
		}
		snap.Queue = append(snap.Queue, q)
		return nil
	}); err != nil {
		return snap, fmt.Errorf("read queue: %w", err)
	}
	if _, err := d.db.Exec("DELETE FROM meta WHERE key = ?", metaClean); err != nil {
		return snap, fmt.Errorf("mark the state database as open: %w", err)
	}
	return snap, nil
}

// scanJSON calls decode with every row query returns from table. A row
// decode fails on is skipped with a warning: the rest of the conversation
// is worth more than one broken row, and a server that doesn't start
// shows none of it.
func (d *DB) scanJSON(table, query string, decode func(seq uint64, data []byte) error) error {
	rows, err := d.db.Query(query)
	if err != nil {
		return err
	}
	skipped := 0
	var skipErr error
	for rows.Next() {
		var seq uint64
		var data []byte
		if err := rows.Scan(&seq, &data); err != nil {
			_ = rows.Close()
			return err
		}
		if err := decode(seq, data); err != nil {
			skipped++
			skipErr = err
		}
	}
	if skipped > 0 {
		d.logger.Warn("Skipped rows of the state database that can't be read", "table", table, "rows", skipped, "error", skipErr)
	}
	return closeRows(rows)
}

func closeRows(rows *sql.Rows) error {
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	return rows.Close()
}

// enqueue adds a write; see enqueueLocked.
func (d *DB) enqueue(key string, run func(*sql.Tx) error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.enqueueLocked(key, run)
}

// enqueueLocked adds a write. A pending write with the same key is
// replaced: keyed writes store the latest state of what their key names. A
// write without a key changes rows other keys name (a truncation, a reset),
// so it is a barrier: writes queued after it are never moved before it.
// Caller holds d.mu.
func (d *DB) enqueueLocked(key string, run func(*sql.Tx) error) {
	if d.closed {
		return
	}
	switch i, ok := d.index[key]; {
	case key == "":
		d.index = make(map[string]int)
		d.pending = append(d.pending, op{run: run})
	case ok:
		d.pending[i].run = run
	default:
		d.index[key] = len(d.pending)
		d.pending = append(d.pending, op{key: key, run: run})
	}
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// OnWriteError sets a function that is called when changes are given up
// because they can't be written, once until writes succeed again. It is
// called on the writing goroutine with no lock held.
func (d *DB) OnWriteError(fn func(error)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.onWriteError = fn
}

// SetMeta stores a meta value.
func (d *DB) SetMeta(key, value string) {
	d.enqueue(metaKeyPrefix+key, metaWrite(key, value))
}

// metaWrite returns the write that stores a meta value.
func metaWrite(key, value string) func(*sql.Tx) error {
	return func(tx *sql.Tx) error {
		_, err := tx.Exec("INSERT INTO meta(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", key, value)
		return err
	}
}

// SetSessionRef stores what resumes the agent's session.
func (d *DB) SetSessionRef(ref string) { d.SetMeta(MetaSessionRef, ref) }

// SetSessionName stores the name the agent gave the session.
func (d *DB) SetSessionName(name string) { d.SetMeta(MetaSessionName, name) }

// SetSettings stores the settings chosen for the session.
func (d *DB) SetSettings(settings native.Settings) {
	data, err := json.Marshal(settings)
	if err != nil {
		d.logger.Error("Failed to encode the settings", "error", err)
		return
	}
	d.SetMeta(MetaSettings, string(data))
}

// Settings returns the stored settings chosen for the session.
func (s Snapshot) Settings() (native.Settings, error) {
	var settings native.Settings
	data, ok := s.Meta[MetaSettings]
	if !ok || data == "" {
		return settings, nil
	}
	if err := json.Unmarshal([]byte(data), &settings); err != nil {
		return native.Settings{}, fmt.Errorf("decode the stored settings: %w", err)
	}
	return settings, nil
}

// SetSeq stores the latest change sequence of epoch.
func (d *DB) SetSeq(epoch string, seq uint64) {
	d.SetMeta(MetaEpoch, epoch)
	d.SetMeta(MetaSeq, strconv.FormatUint(seq, 10))
}

// PutMessage stores a conversation message.
func (d *DB) PutMessage(m st.ConversationMessage, seq uint64) {
	data, err := json.Marshal(m)
	if err != nil {
		d.logger.Error("Failed to encode message for the state database", "error", err)
		return
	}
	d.enqueue("message:"+strconv.Itoa(m.Id), func(tx *sql.Tx) error {
		_, err := tx.Exec("INSERT INTO messages(id, seq, data) VALUES(?, ?, ?) ON CONFLICT(id) DO UPDATE SET seq = excluded.seq, data = excluded.data", m.Id, seq, data)
		return err
	})
}

// TruncateMessages removes the messages with an ID of at least fromID.
func (d *DB) TruncateMessages(fromID int) {
	d.enqueue("", func(tx *sql.Tx) error {
		_, err := tx.Exec("DELETE FROM messages WHERE id >= ?", fromID)
		return err
	})
}

// PutRichMessage stores the rich message at position pos.
func (d *DB) PutRichMessage(pos int, m jsonlwatcher.RichMessage, seq uint64) {
	data, err := json.Marshal(m)
	if err != nil {
		d.logger.Error("Failed to encode rich message for the state database", "error", err)
		return
	}
	d.enqueue("rich:"+strconv.Itoa(pos), func(tx *sql.Tx) error {
		_, err := tx.Exec("INSERT INTO rich_messages(pos, seq, data) VALUES(?, ?, ?) ON CONFLICT(pos) DO UPDATE SET seq = excluded.seq, data = excluded.data", pos, seq, data)
		return err
	})
}

// AppendSessionEvents stores timeline events.
func (d *DB) AppendSessionEvents(events []jsonlwatcher.SessionEvent) {
	for _, e := range events {
		data, err := json.Marshal(e)
		if err != nil {
			d.logger.Error("Failed to encode timeline event for the state database", "error", err)
			continue
		}
		id := e.EventID
		d.enqueue("event:"+strconv.Itoa(id), func(tx *sql.Tx) error {
			_, err := tx.Exec("INSERT INTO session_events(id, data) VALUES(?, ?) ON CONFLICT(id) DO UPDATE SET data = excluded.data", id, data)
			return err
		})
	}
}

// PutRequest stores a request. pos orders requests by when they were
// first stored.
func (d *DB) PutRequest(state native.RequestState, seq uint64) {
	data, err := json.Marshal(state)
	if err != nil {
		d.logger.Error("Failed to encode request for the state database", "error", err)
		return
	}
	id := state.ID
	d.enqueue("request:"+id, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO requests(id, pos, seq, data)
			VALUES(?, (SELECT COALESCE(MAX(pos), 0) + 1 FROM requests), ?, ?)
			ON CONFLICT(id) DO UPDATE SET seq = excluded.seq, data = excluded.data`, id, seq, data)
		return err
	})
}

// DeleteRequest removes a request.
func (d *DB) DeleteRequest(id string) {
	d.enqueue("request:"+id, func(tx *sql.Tx) error {
		_, err := tx.Exec("DELETE FROM requests WHERE id = ?", id)
		return err
	})
}

// PutQueue replaces the stored queue.
func (d *DB) PutQueue(queue []QueuedMessage, nextID int) {
	encoded := make([][]byte, 0, len(queue))
	for _, q := range queue {
		data, err := json.Marshal(q)
		if err != nil {
			d.logger.Error("Failed to encode queued message for the state database", "error", err)
			return
		}
		encoded = append(encoded, data)
	}
	d.enqueue(queueKey, func(tx *sql.Tx) error {
		if _, err := tx.Exec("DELETE FROM queue"); err != nil {
			return err
		}
		for i, data := range encoded {
			if _, err := tx.Exec("INSERT INTO queue(pos, data) VALUES(?, ?)", i, data); err != nil {
				return err
			}
		}
		return nil
	})
	d.SetMeta(MetaNextQueueID, strconv.Itoa(nextID))
}

// Reset removes the conversation (messages, rich messages, timeline,
// requests) and the session ref, keeping the agent identity. Pending
// writes of the old conversation are dropped.
func (d *DB) Reset(epoch string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	kept := d.pending[:0]
	d.index = make(map[string]int)
	for _, o := range d.pending {
		// Keep meta and queue writes; the conversation rows are deleted.
		if o.key == queueKey || strings.HasPrefix(o.key, metaKeyPrefix) {
			d.index[o.key] = len(kept)
			kept = append(kept, o)
		}
	}
	d.pending = kept
	d.enqueueLocked("", func(tx *sql.Tx) error {
		for _, table := range []string{"messages", "rich_messages", "session_events", "requests"} {
			if _, err := tx.Exec("DELETE FROM " + table); err != nil {
				return err
			}
		}
		_, err := tx.Exec("DELETE FROM meta WHERE key IN (?, ?)", MetaSessionRef, MetaSessionName)
		return err
	})
	d.enqueueLocked(metaKeyPrefix+MetaEpoch, metaWrite(MetaEpoch, epoch))
}

func (d *DB) writeLoop(ctx context.Context) {
	defer close(d.done)
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			d.write()
			return
		case <-ticker.C:
			d.write()
		case <-d.wake:
			// Gather more changes before writing, unless there are many.
			d.mu.Lock()
			n := len(d.pending)
			d.mu.Unlock()
			if n >= maxPending {
				d.write()
			}
		}
	}
}

// write applies the pending writes in one transaction. A batch that fails
// is tried again before newer writes are taken, and given up after
// writeAttempts.
func (d *DB) write() {
	d.writeMu.Lock()
	d.mu.Lock()
	ops := d.pending
	d.pending = nil
	d.index = make(map[string]int)
	d.mu.Unlock()
	if len(ops) == 0 {
		d.writeMu.Unlock()
		return
	}
	err := d.apply(ops)
	for attempt, delay := 1, retryDelay; err != nil && attempt < writeAttempts; attempt, delay = attempt+1, delay*2 {
		d.logger.Warn("Failed to write the state database; trying again", "attempt", attempt, "error", err)
		time.Sleep(delay)
		err = d.apply(ops)
	}
	var report func(error)
	d.mu.Lock()
	if err != nil {
		d.lost = err
		if !d.failing {
			report = d.onWriteError
		}
	}
	d.failing = err != nil
	d.mu.Unlock()
	d.writeMu.Unlock()
	if err != nil {
		d.logger.Error("Failed to write the state database; changes are lost", "changes", len(ops), "error", err)
		if report != nil {
			report(err)
		}
	}
}

func (d *DB) apply(ops []op) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	for _, o := range ops {
		if err := o.run(tx); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// Flush writes the changes enqueued so far.
func (d *DB) Flush() {
	d.write()
}

// Close writes what is pending and closes the database. Unless changes
// were lost, the file is marked as closed cleanly (see Snapshot.Clean).
// Changes that couldn't be written are reported in the error.
func (d *DB) Close() error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	// Later writes are ignored; the write loop writes the pending ones.
	d.closed = true
	d.mu.Unlock()
	d.stop()
	<-d.done
	d.mu.Lock()
	lost := d.lost
	d.mu.Unlock()
	var errs []error
	if lost != nil {
		errs = append(errs, fmt.Errorf("changes to the state database were lost: %w", lost))
	} else if err := d.apply([]op{{run: metaWrite(metaClean, "1")}}); err != nil {
		errs = append(errs, fmt.Errorf("mark the state database as closed: %w", err))
	}
	if err := d.db.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close state database: %w", err))
	}
	return errors.Join(errs...)
}
