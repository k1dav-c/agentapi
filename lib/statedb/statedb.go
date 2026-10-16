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
	"strconv"
	"sync"
	"time"

	_ "modernc.org/sqlite" // SQLite driver without cgo.

	"github.com/coder/agentapi/lib/jsonlwatcher"
	"github.com/coder/agentapi/lib/native"
	st "github.com/coder/agentapi/lib/screentracker"
)

// schemaVersion is stored in PRAGMA user_version.
const schemaVersion = 1

// flushInterval is how long changes may wait before they are written.
const flushInterval = 200 * time.Millisecond

const schema = `
CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS messages (id INTEGER PRIMARY KEY, seq INTEGER NOT NULL, data TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS rich_messages (pos INTEGER PRIMARY KEY, seq INTEGER NOT NULL, data TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS session_events (id INTEGER PRIMARY KEY, data TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS requests (id TEXT PRIMARY KEY, pos INTEGER NOT NULL, seq INTEGER NOT NULL, data TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS queue (pos INTEGER PRIMARY KEY, data TEXT NOT NULL);
`

// Meta keys.
const (
	MetaAgentType   = "agent_type"
	MetaTransport   = "transport"
	MetaCWD         = "cwd"
	MetaEpoch       = "epoch"
	MetaSeq         = "seq"
	MetaSessionRef  = "session_ref"
	MetaSessionName = "session_name"
	MetaNextQueueID = "next_queue_id"
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
	done   chan struct{}
	stop   context.CancelFunc
	// writeMu serializes writes, so batches commit in the order they were
	// taken from pending.
	writeMu sync.Mutex
}

// Open opens or creates the database at path.
func Open(path string, logger *slog.Logger) (*DB, error) {
	if logger == nil {
		logger = slog.Default()
	}
	// busy_timeout lets a second process fail with "locked" after a while
	// instead of immediately; WAL keeps reads from blocking the writer.
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)"
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open state database: %w", err)
	}
	// One connection: SQLite has one writer, and the pragmas above apply
	// per connection.
	sqlDB.SetMaxOpenConns(1)
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

func migrate(db *sql.DB) error {
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read state database version: %w", err)
	}
	if version > schemaVersion {
		return fmt.Errorf("state database was written by a newer AgentAPI (schema %d, this one knows %d)", version, schemaVersion)
	}
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("create state database tables: %w", err)
	}
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
		return fmt.Errorf("set state database version: %w", err)
	}
	return nil
}

// Load reads everything stored. Call it before any writes.
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

	if err := scanJSON(d.db, "SELECT seq, data FROM messages ORDER BY id", func(seq uint64, data []byte) error {
		var m st.ConversationMessage
		if err := json.Unmarshal(data, &m); err != nil {
			return err
		}
		snap.Messages = append(snap.Messages, Message{Message: m, Seq: seq})
		return nil
	}); err != nil {
		return snap, fmt.Errorf("read messages: %w", err)
	}
	if err := scanJSON(d.db, "SELECT seq, data FROM rich_messages ORDER BY pos", func(seq uint64, data []byte) error {
		var m jsonlwatcher.RichMessage
		if err := json.Unmarshal(data, &m); err != nil {
			return err
		}
		snap.RichMessages = append(snap.RichMessages, RichMessage{Message: m, Seq: seq})
		return nil
	}); err != nil {
		return snap, fmt.Errorf("read rich messages: %w", err)
	}
	if err := scanJSON(d.db, "SELECT 0, data FROM session_events ORDER BY id", func(_ uint64, data []byte) error {
		var e jsonlwatcher.SessionEvent
		if err := json.Unmarshal(data, &e); err != nil {
			return err
		}
		snap.Events = append(snap.Events, e)
		return nil
	}); err != nil {
		return snap, fmt.Errorf("read timeline: %w", err)
	}
	if err := scanJSON(d.db, "SELECT seq, data FROM requests ORDER BY pos", func(seq uint64, data []byte) error {
		var r native.RequestState
		if err := json.Unmarshal(data, &r); err != nil {
			return err
		}
		snap.Requests = append(snap.Requests, Request{State: r, Seq: seq})
		return nil
	}); err != nil {
		return snap, fmt.Errorf("read requests: %w", err)
	}
	if err := scanJSON(d.db, "SELECT 0, data FROM queue ORDER BY pos", func(_ uint64, data []byte) error {
		var q QueuedMessage
		if err := json.Unmarshal(data, &q); err != nil {
			return err
		}
		snap.Queue = append(snap.Queue, q)
		return nil
	}); err != nil {
		return snap, fmt.Errorf("read queue: %w", err)
	}
	return snap, nil
}

func scanJSON(db *sql.DB, query string, fn func(seq uint64, data []byte) error) error {
	rows, err := db.Query(query)
	if err != nil {
		return err
	}
	for rows.Next() {
		var seq uint64
		var data []byte
		if err := rows.Scan(&seq, &data); err != nil {
			_ = rows.Close()
			return err
		}
		if err := fn(seq, data); err != nil {
			_ = rows.Close()
			return err
		}
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

// enqueue adds a write. A pending write with the same key is replaced. A
// write without a key (a delete) is a barrier: writes queued after it are
// never moved before it.
func (d *DB) enqueue(key string, run func(*sql.Tx) error) {
	d.mu.Lock()
	defer d.mu.Unlock()
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

// SetMeta stores a meta value.
func (d *DB) SetMeta(key, value string) {
	d.enqueue("meta:"+key, func(tx *sql.Tx) error {
		_, err := tx.Exec("INSERT INTO meta(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", key, value)
		return err
	})
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
	d.enqueue("queue", func(tx *sql.Tx) error {
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
	kept := d.pending[:0]
	d.index = make(map[string]int)
	for _, o := range d.pending {
		// Keep meta and queue writes; the conversation rows are deleted.
		if o.key == "queue" || len(o.key) > 5 && o.key[:5] == "meta:" {
			d.index[o.key] = len(kept)
			kept = append(kept, o)
		}
	}
	d.pending = kept
	d.mu.Unlock()
	d.enqueue("", func(tx *sql.Tx) error {
		for _, table := range []string{"messages", "rich_messages", "session_events", "requests"} {
			if _, err := tx.Exec("DELETE FROM " + table); err != nil {
				return err
			}
		}
		_, err := tx.Exec("DELETE FROM meta WHERE key IN (?, ?)", MetaSessionRef, MetaSessionName)
		return err
	})
	d.SetMeta(MetaEpoch, epoch)
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
			if n >= 256 {
				d.write()
			}
		}
	}
}

// write applies the pending writes in one transaction.
func (d *DB) write() {
	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	d.mu.Lock()
	ops := d.pending
	d.pending = nil
	d.index = make(map[string]int)
	d.mu.Unlock()
	if len(ops) == 0 {
		return
	}
	if err := d.apply(ops); err != nil {
		d.logger.Error("Failed to write the state database", "error", err)
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

// Close writes what is pending and closes the database.
func (d *DB) Close() error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.mu.Unlock()
	d.stop()
	<-d.done
	d.mu.Lock()
	d.closed = true
	d.mu.Unlock()
	if err := d.db.Close(); err != nil {
		return errors.Join(errors.New("close state database"), err)
	}
	return nil
}
