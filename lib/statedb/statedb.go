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
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	sqlite "modernc.org/sqlite" // SQLite driver without cgo.
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/coder/agentapi/internal/version"
	"github.com/coder/agentapi/lib/agentstate"
	conv "github.com/coder/agentapi/lib/conversation"
	"github.com/coder/agentapi/lib/transcript"
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

// migration takes the schema from one version to the next.
type migration struct {
	// since is the AgentAPI release that brought it.
	since string
	// minWriter is the oldest schema an AgentAPI must know to write the
	// database once the migration ran. One that only adds (a table, a
	// column with a default) keeps the minWriter of the one before: an
	// older AgentAPI goes on writing the file and leaves alone what it
	// doesn't know, which its resets and truncations therefore don't
	// clear (the server checks restored tasks against the messages). One
	// that changes what an older AgentAPI writes raises it to its own
	// version.
	minWriter int
	sql       string
}

// migrations take the schema from version i to i+1, so migrations[0]
// creates it; PRAGMA user_version holds the version. A change to the
// schema is a new migration at the end: one that ran never changes.
var migrations = []migration{
	{since: "0.24.0", minWriter: 1, sql: `CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS messages (id INTEGER PRIMARY KEY, seq INTEGER NOT NULL, data TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS rich_messages (pos INTEGER PRIMARY KEY, seq INTEGER NOT NULL, data TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS session_events (id INTEGER PRIMARY KEY, data TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS requests (id TEXT PRIMARY KEY, pos INTEGER NOT NULL, seq INTEGER NOT NULL, data TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS queue (pos INTEGER PRIMARY KEY, data TEXT NOT NULL);`},
	// The tasks: a user's message, by its ID, and the agent's turn on it.
	{since: "1.0.0", minWriter: 1, sql: `CREATE TABLE IF NOT EXISTS tasks (id INTEGER PRIMARY KEY, seq INTEGER NOT NULL, data TEXT NOT NULL);`},
}

// Meta keys.
const (
	MetaAgentType   = "agent_type"
	MetaTransport   = "transport"
	MetaCWD         = "cwd"
	MetaEpoch       = "epoch"
	metaSeq         = "seq"
	MetaSessionRef  = "session_ref"
	MetaSessionName = "session_name"
	// MetaState holds the part of the conversation's state that survives a
	// restart (agentstate.StoredState: the settings chosen, a limit stop, a
	// retry wait) as one JSON object with a version; see storedState. A
	// reset keeps it.
	MetaState = "conversation_state"
	// MetaWebhook holds the webhook settings changed through the API, as
	// JSON. A reset keeps them.
	MetaWebhook = "webhook"
	// AgentAPI 2's PTY transport also kept initial_prompt, the initial
	// prompt the conversation got, which nothing reads anymore.
	// MetaSeenAt is when a client last showed the conversation to the
	// user (RFC 3339), which tells a task that ended since as unread. A
	// reset keeps it.
	MetaSeenAt      = "seen_at"
	metaNextQueueID = "next_queue_id"
	// metaClean is set while the file is closed cleanly; see Snapshot.Clean.
	metaClean = "clean"
	// metaMinWriterSchema is the oldest schema that may write the file
	// (see migration.minWriter), and metaMinWriterVersion the release that
	// brought it; without them, any may. A migration that raises it
	// records them. metaWriterVersion is the AgentAPI that last closed the
	// file. AgentAPI 1.9.0 and earlier don't read them, and refuse any
	// schema newer than theirs.
	metaMinWriterSchema  = "min_writer_schema"
	metaMinWriterVersion = "min_writer_version"
	metaWriterVersion    = "writer_version"
)

// Keys of pending writes; see op.
const (
	metaKeyPrefix = "meta:"
	queueKey      = "queue"
)

// Task is a stored task, as the server encoded it, with the seq of its last
// change.
type Task struct {
	Data json.RawMessage
	Seq  uint64
}

// Message is a stored conversation message with the seq of its last change.
type Message struct {
	Message conv.Message
	Seq     uint64
}

// RichMessage is a stored rich message with the seq of its last change.
type RichMessage struct {
	Message transcript.RichMessage
	Seq     uint64
}

// Request is a stored request with the seq of its last change.
type Request struct {
	State agentstate.RequestState
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
	Events       []transcript.SessionEvent
	Requests     []Request
	Queue        []QueuedMessage
	// Tasks are stored as the server encoded them, in the order of their
	// IDs.
	Tasks []Task
	// RichBase is the stored position of RichMessages[0]. Window says what
	// LoadRecent left in the database.
	RichBase int
	Window   Window
	// Clean is set when the AgentAPI that wrote the file closed it. After a
	// crash it is not, and the last changes before the crash may be
	// missing.
	Clean bool
}

// Empty reports whether nothing was stored yet.
func (s Snapshot) Empty() bool { return len(s.Meta) == 0 }

// Seq returns the stored seq.
func (s Snapshot) Seq() uint64 {
	n, _ := strconv.ParseUint(s.Meta[metaSeq], 10, 64)
	return n
}

// NextQueueID returns the stored next queue ID.
func (s Snapshot) NextQueueID() int {
	n, _ := strconv.Atoi(s.Meta[metaNextQueueID])
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
			return nil, fmt.Errorf("state database %s is %w", path, ErrInUse)
		}
		return nil, fmt.Errorf("open state database: %w", err)
	}
	if err := migrate(sqlDB); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("state database %s: %w", path, err)
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
	return fileURI(path) + "?_pragma=busy_timeout(1000)&_pragma=locking_mode(EXCLUSIVE)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
}

// fileURI returns the SQLite URI of the file at path, without parameters.
func fileURI(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	// ?, # and % have a meaning in a URI.
	return "file:" + strings.NewReplacer("%", "%25", "?", "%3F", "#", "%23").Replace(path)
}

// isBusy reports whether err says that another connection holds the lock.
func isBusy(err error) bool {
	var sqliteErr *sqlite.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code()&0xff == sqlite3.SQLITE_BUSY
}

// migrate runs the migrations the database hasn't had. A database a newer
// AgentAPI migrated further is left as it is, if this one may write it.
func migrate(db *sql.DB) error {
	var schema int
	if err := db.QueryRow("PRAGMA user_version").Scan(&schema); err != nil {
		return fmt.Errorf("read state database version: %w", err)
	}
	if schema > len(migrations) {
		return checkWriter(db, schema, len(migrations), version.Version)
	}
	for ; schema < len(migrations); schema++ {
		if err := migrateTo(db, schema+1); err != nil {
			return fmt.Errorf("migrate the state database to schema %d: %w", schema+1, err)
		}
	}
	return nil
}

// checkWriter returns an error unless an AgentAPI, release program, that
// knows schemas up to knows may write the database whose schema, newer than
// that, is schema.
func checkWriter(db *sql.DB, schema, knows int, program string) error {
	meta := map[string]string{}
	rows, err := db.Query("SELECT key, value FROM meta WHERE key IN (?, ?, ?)", metaMinWriterSchema, metaMinWriterVersion, metaWriterVersion)
	if err != nil {
		return fmt.Errorf("read state database version: %w", err)
	}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			_ = rows.Close()
			return err
		}
		meta[k] = v
	}
	if err := closeRows(rows); err != nil {
		return err
	}
	minWriter := 1
	if v, ok := meta[metaMinWriterSchema]; ok {
		if minWriter, err = strconv.Atoi(v); err != nil {
			minWriter = schema
		}
	}
	if minWriter <= knows {
		return nil
	}
	needs := "a newer AgentAPI"
	if v := meta[metaMinWriterVersion]; v != "" {
		needs = "AgentAPI " + v + " or later"
	}
	by := ""
	if v := meta[metaWriterVersion]; v != "" {
		by = ", last written by AgentAPI " + v
	}
	return fmt.Errorf("needs %s (schema %d%s; AgentAPI %s knows schema %d): update AgentAPI (agentapi update), or use another state database",
		needs, schema, by, program, knows)
}

// ErrInUse says that another AgentAPI has the database open.
var ErrInUse = errors.New("in use by another AgentAPI")

// Schema returns the newest schema this AgentAPI knows: it migrates an
// older database to it.
func Schema() int { return len(migrations) }

// Check reports why this AgentAPI couldn't open the database at path,
// without changing it or making a file: it reads it as it is, and doesn't
// migrate it. A missing file is fine, since Open creates it. A file with a
// write-ahead log is open in another AgentAPI, or one that didn't close it:
// what it holds is only known once opened, so the error wraps ErrInUse.
func Check(path string) error {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("state database: %w", err)
	}
	// An AgentAPI that has the file open keeps a -wal file next to it,
	// which closing it removes.
	if _, err := os.Stat(path + "-wal"); err == nil {
		return fmt.Errorf("state database %s has a write-ahead log: it is %w, or wasn't closed", path, ErrInUse)
	}
	// immutable: without locks, a -wal or a -shm file.
	sqlDB, err := sql.Open("sqlite", fileURI(path)+"?mode=ro&immutable=1")
	if err != nil {
		return fmt.Errorf("state database %s: %w", path, err)
	}
	defer func() { _ = sqlDB.Close() }()
	var schema int
	if err := sqlDB.QueryRow("PRAGMA user_version").Scan(&schema); err != nil {
		return fmt.Errorf("state database %s: %w", path, err)
	}
	if schema <= len(migrations) {
		return nil
	}
	if err := checkWriter(sqlDB, schema, len(migrations), version.Version); err != nil {
		return fmt.Errorf("state database %s: %w", path, err)
	}
	return nil
}

// CheckWriter returns an error unless an AgentAPI, release program, that
// knows schemas up to knows may write the open database (see Schema): one
// that would start with it, as a restart in place into another program
// does.
func (d *DB) CheckWriter(knows int, program string) error {
	var schema int
	if err := d.db.QueryRow("PRAGMA user_version").Scan(&schema); err != nil {
		return fmt.Errorf("read state database version: %w", err)
	}
	if schema <= knows {
		return nil
	}
	return checkWriter(d.db, schema, knows, program)
}

// migrateTo runs the migration to version, and records the version and the
// oldest schema that may write the database, in one transaction.
func migrateTo(db *sql.DB, version int) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(migrations[version-1].sql); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
		_ = tx.Rollback()
		return err
	}
	if minWriter := minWriterOf(version); minWriter > 1 {
		for _, kv := range [][2]string{
			{metaMinWriterSchema, strconv.Itoa(minWriter)},
			{metaMinWriterVersion, migrations[minWriter-1].since},
		} {
			if err := metaWrite(kv[0], kv[1])(tx); err != nil {
				_ = tx.Rollback()
				return err
			}
		}
	}
	return tx.Commit()
}

// minWriterOf returns the oldest schema that may write a database of schema
// version.
func minWriterOf(version int) int {
	minWriter := 1
	for _, m := range migrations[:version] {
		minWriter = max(minWriter, m.minWriter)
	}
	return minWriter
}

// Load reads everything stored. Call it before any writes. From then on
// the file doesn't count as closed cleanly until Close.
func (d *DB) Load() (Snapshot, error) {
	return d.load(0)
}

// load is LoadRecent.
func (d *DB) load(hot int) (Snapshot, error) {
	snap := Snapshot{Meta: map[string]string{}}
	if err := d.loadMeta(&snap); err != nil {
		return snap, err
	}
	if err := scanRows(d, "messages", "SELECT seq, data FROM messages ORDER BY id", func(m conv.Message, seq uint64) {
		snap.Messages = append(snap.Messages, Message{Message: m, Seq: seq})
	}); err != nil {
		return snap, fmt.Errorf("read messages: %w", err)
	}
	if err := d.scanJSON("tasks", "SELECT seq, data FROM tasks ORDER BY id", func(seq uint64, data []byte) error {
		snap.Tasks = append(snap.Tasks, Task{Data: append(json.RawMessage(nil), data...), Seq: seq})
		return nil
	}); err != nil {
		return snap, fmt.Errorf("read tasks: %w", err)
	}
	w, err := d.windowOf(snap.Tasks, hot)
	if err != nil {
		return snap, fmt.Errorf("find the latest tasks: %w", err)
	}
	snap.Window = w
	// The rich messages are stored by their place in the conversation: the
	// first one read is at RichBase, the next at RichBase+1 and so on.
	var base sql.NullInt64
	if err := d.db.QueryRow("SELECT MIN(pos) FROM rich_messages WHERE pos >= ?", w.RichPos).Scan(&base); err != nil {
		return snap, fmt.Errorf("read rich messages: %w", err)
	}
	snap.RichBase = w.RichPos
	if base.Valid {
		snap.RichBase = int(base.Int64)
	}
	if err := scanRows(d, "rich_messages", "SELECT seq, data FROM rich_messages WHERE pos >= ? ORDER BY pos", func(m transcript.RichMessage, seq uint64) {
		snap.RichMessages = append(snap.RichMessages, RichMessage{Message: m, Seq: seq})
	}, w.RichPos); err != nil {
		return snap, fmt.Errorf("read rich messages: %w", err)
	}
	if err := scanRows(d, "session_events", "SELECT 0, data FROM session_events WHERE id >= ? ORDER BY id", func(e transcript.SessionEvent, _ uint64) {
		snap.Events = append(snap.Events, e)
	}, w.EventID); err != nil {
		return snap, fmt.Errorf("read timeline: %w", err)
	}
	if err := scanRows(d, "requests", "SELECT seq, data FROM requests ORDER BY pos", func(r agentstate.RequestState, seq uint64) {
		snap.Requests = append(snap.Requests, Request{State: r, Seq: seq})
	}); err != nil {
		return snap, fmt.Errorf("read requests: %w", err)
	}
	if err := scanRows(d, "queue", "SELECT 0, data FROM queue ORDER BY pos", func(q QueuedMessage, _ uint64) {
		snap.Queue = append(snap.Queue, q)
	}); err != nil {
		return snap, fmt.Errorf("read queue: %w", err)
	}
	if _, err := d.db.Exec("DELETE FROM meta WHERE key = ?", metaClean); err != nil {
		return snap, fmt.Errorf("mark the state database as open: %w", err)
	}
	return snap, nil
}

// loadMeta reads the meta table into snap: whether the file was closed
// cleanly, and the conversation's values.
func (d *DB) loadMeta(snap *Snapshot) error {
	rows, err := d.db.Query("SELECT key, value FROM meta")
	if err != nil {
		return fmt.Errorf("read meta: %w", err)
	}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			_ = rows.Close()
			return err
		}
		snap.Meta[k] = v
	}
	if err := closeRows(rows); err != nil {
		return err
	}
	_, snap.Clean = snap.Meta[metaClean]
	// What the schema's versions take isn't the conversation's.
	for _, key := range []string{metaClean, metaMinWriterSchema, metaMinWriterVersion, metaWriterVersion} {
		delete(snap.Meta, key)
	}
	return nil
}

// scanRows is scanJSON for rows whose data is a T in JSON, which it passes
// to add with the row's seq.
func scanRows[T any](d *DB, table, query string, add func(v T, seq uint64), args ...any) error {
	return d.scanJSON(table, query, func(seq uint64, data []byte) error {
		var v T
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		add(v, seq)
		return nil
	}, args...)
}

// scanJSON calls decode with every row query returns from table. A row
// decode fails on is skipped with a warning: the rest of the conversation
// is worth more than one broken row, and a server that doesn't start
// shows none of it.
func (d *DB) scanJSON(table, query string, decode func(seq uint64, data []byte) error, args ...any) error {
	rows, err := d.db.Query(query, args...)
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

// SetSeenAt stores when a client last showed the conversation.
func (d *DB) SetSeenAt(at time.Time) {
	d.SetMeta(MetaSeenAt, at.UTC().Format(time.RFC3339Nano))
}

// SetSeq stores the latest change sequence of epoch.
func (d *DB) SetSeq(epoch string, seq uint64) {
	d.SetMeta(MetaEpoch, epoch)
	d.SetMeta(metaSeq, strconv.FormatUint(seq, 10))
}

// PutMessage stores a conversation message. It is encoded when it is
// written, on the writing goroutine: the emitter calls PutMessage with its
// lock held, on every update of a message that streams in, and of a
// message updated again before it was written only the latest is encoded.
func (d *DB) PutMessage(m conv.Message, seq uint64) {
	d.enqueue("message:"+strconv.Itoa(m.Id), func(tx *sql.Tx) error {
		data, ok := d.encode("message", m)
		if !ok {
			return nil
		}
		_, err := tx.Exec("INSERT INTO messages(id, seq, data) VALUES(?, ?, ?) ON CONFLICT(id) DO UPDATE SET seq = excluded.seq, data = excluded.data", m.Id, seq, data)
		return err
	})
}

// encode encodes v, a what, as JSON, or logs why it can't; a row that
// can't be encoded is left out rather than failing the others.
func (d *DB) encode(what string, v any) ([]byte, bool) {
	data, err := json.Marshal(v)
	if err != nil {
		d.logger.Error("Failed to encode a "+what+" for the state database", "error", err)
		return nil, false
	}
	return data, true
}

// TruncateMessages removes the messages with an ID of at least fromID.
func (d *DB) TruncateMessages(fromID int) {
	d.enqueue("", func(tx *sql.Tx) error {
		if _, err := tx.Exec("DELETE FROM messages WHERE id >= ?", fromID); err != nil {
			return err
		}
		// A task goes with the message that started it.
		_, err := tx.Exec("DELETE FROM tasks WHERE id >= ?", fromID)
		return err
	})
}

// DeleteTask removes the task the user's message id started.
func (d *DB) DeleteTask(id int) {
	d.enqueue("task:"+strconv.Itoa(id), func(tx *sql.Tx) error {
		_, err := tx.Exec("DELETE FROM tasks WHERE id = ?", id)
		return err
	})
}

// PutTask stores the task the user's message id started, as data (JSON).
func (d *DB) PutTask(id int, data []byte, seq uint64) {
	d.enqueue("task:"+strconv.Itoa(id), func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO tasks(id, seq, data) VALUES(?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET seq = excluded.seq, data = excluded.data`, id, seq, data)
		return err
	})
}

// PutRichMessage stores the rich message at position pos. Like a message,
// it is encoded when it is written, and of a message updated again before
// it was written only the latest is encoded: a rich message that streams
// in is sent whole on every change. m is not copied either (copying the
// whole message on every change made a stream take quadratic time), so
// the caller must not change m, the blocks of its content or what they
// point to once PutRichMessage returned. The emitter never does: it
// replaces a stored rich message, and merging into one makes new content.
func (d *DB) PutRichMessage(pos int, m transcript.RichMessage, seq uint64) {
	d.enqueue("rich:"+strconv.Itoa(pos), func(tx *sql.Tx) error {
		data, ok := d.encode("rich message", m)
		if !ok {
			return nil
		}
		_, err := tx.Exec("INSERT INTO rich_messages(pos, seq, data) VALUES(?, ?, ?) ON CONFLICT(pos) DO UPDATE SET seq = excluded.seq, data = excluded.data", pos, seq, data)
		return err
	})
}

// AppendSessionEvents stores timeline events.
func (d *DB) AppendSessionEvents(events []transcript.SessionEvent) {
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
func (d *DB) PutRequest(state agentstate.RequestState, seq uint64) {
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
	d.SetMeta(metaNextQueueID, strconv.Itoa(nextID))
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
		for _, table := range []string{"messages", "rich_messages", "session_events", "requests", "tasks"} {
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

// apply runs ops in one transaction, which it commits when they all
// succeed.
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
	} else if err := d.apply([]op{{run: metaWrite(metaClean, "1")}, {run: metaWrite(metaWriterVersion, version.Version)}}); err != nil {
		errs = append(errs, fmt.Errorf("mark the state database as closed: %w", err))
	}
	if err := d.db.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close state database: %w", err))
	}
	return errors.Join(errs...)
}
