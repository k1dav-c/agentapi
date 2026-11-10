package statedb

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/coder/agentapi/lib/transcript"
)

// The server holds the rich messages and timeline events of the latest
// tasks only (convstate's hot window); the state database keeps them all,
// and reads the others a page at a time. Rich messages come in the order of
// their tasks, by position, and timeline events in the order of their
// times, by ID: both are found by a binary search on the rows, which reads
// one row's task or time at a time, not the rows themselves.

// historyChunk is how many rows a read that goes through many takes at a
// time. Each read holds the database's one connection, which the writes
// wait for, so a long one is cut into short ones.
const historyChunk = 500

// storedTask is what LoadRecent reads of a stored task.
type storedTask struct {
	ID        int       `json:"id"`
	StartedAt time.Time `json:"started_at"`
}

// LoadRecent is Load, but of the rich messages and timeline events it reads
// only those of the latest tasks, hot of them (all for 0), and from where
// the latest of the tasks before it started: Snapshot.Window says where.
func (d *DB) LoadRecent(hot int) (Snapshot, error) {
	return d.load(hot)
}

// Window says what of the conversation LoadRecent left in the database:
// the rich messages before position RichPos and the timeline events before
// ID EventID, those of the tasks before task Task, which started at Start.
// The latest change of those rich messages was ColdSeq. Cold is unset when
// it read everything.
type Window struct {
	Cold    bool
	Task    int
	Start   time.Time
	RichPos int
	EventID int
	ColdSeq uint64
}

// windowOf returns the window of the latest hot of tasks, stored in the
// order of their IDs.
func (d *DB) windowOf(tasks []Task, hot int) (Window, error) {
	var w Window
	if hot <= 0 || len(tasks) <= hot {
		return w, nil
	}
	var first storedTask
	if err := json.Unmarshal(tasks[len(tasks)-hot].Data, &first); err != nil {
		// The window starts where it can't be told: load everything.
		return w, nil
	}
	richPos, err := d.firstRichPos(first.ID)
	if err != nil {
		return w, err
	}
	eventID, err := d.firstEventID(first.StartedAt)
	if err != nil {
		return w, err
	}
	var coldSeq sql.NullInt64
	if err := d.db.QueryRow("SELECT MAX(seq) FROM rich_messages WHERE pos < ?", richPos).Scan(&coldSeq); err != nil {
		return w, err
	}
	return Window{Cold: true, Task: first.ID, Start: first.StartedAt, RichPos: richPos, EventID: eventID, ColdSeq: uint64(max(coldSeq.Int64, 0))}, nil
}

// bounds returns the lowest and the highest key of table (pos or id), and
// false when it is empty.
func (d *DB) bounds(table, key string) (lo, hi int, ok bool, err error) {
	var minKey, maxKey sql.NullInt64
	if err := d.db.QueryRow("SELECT MIN("+key+"), MAX("+key+") FROM "+table).Scan(&minKey, &maxKey); err != nil {
		return 0, 0, false, err
	}
	if !minKey.Valid {
		return 0, 0, false, nil
	}
	return int(minKey.Int64), int(maxKey.Int64), true, nil
}

// search returns the first key of table at or after lo for which before
// reports false, of the keys up to hi, or hi+1. before is told the value
// of field of the first row at or after a key: its rows are in order.
func (d *DB) search(table, key, field string, lo, hi int, before func(v sql.NullString) bool) (int, error) {
	query := "SELECT json_extract(data, '" + field + "') FROM " + table + " WHERE " + key + " >= ? ORDER BY " + key + " LIMIT 1"
	end := hi + 1
	for lo < end {
		mid := lo + (end-lo)/2
		var v sql.NullString
		if err := d.db.QueryRow(query, mid).Scan(&v); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				end = mid
				continue
			}
			return 0, err
		}
		if before(v) {
			lo = mid + 1
		} else {
			end = mid
		}
	}
	return lo, nil
}

// firstRichPos returns the position of the first rich message of task
// taskID or a later one, or the position after the last. A rich message
// without a task came before the first.
func (d *DB) firstRichPos(taskID int) (int, error) {
	lo, hi, ok, err := d.bounds("rich_messages", "pos")
	if err != nil || !ok {
		return 0, err
	}
	return d.search("rich_messages", "pos", "$.task_id", lo, hi, func(v sql.NullString) bool {
		if !v.Valid {
			return true
		}
		var id int
		_, err := fmt.Sscan(v.String, &id)
		return err == nil && id < taskID
	})
}

// firstEventID returns the ID of the first timeline event from at or
// later, or the ID after the last.
func (d *DB) firstEventID(at time.Time) (int, error) {
	lo, hi, ok, err := d.bounds("session_events", "id")
	if err != nil || !ok {
		return 1, err
	}
	return d.search("session_events", "id", "$.time", lo, hi, func(v sql.NullString) bool {
		t, err := time.Parse(time.RFC3339Nano, v.String)
		return err == nil && t.Before(at)
	})
}

// RichMessagesOf returns the stored rich messages of the tasks from task
// from up to task before (to the last for 0), and those before the first
// task with prelude. The page may hold a few more, of the tasks around it:
// the caller keeps those it wants.
func (d *DB) RichMessagesOf(from, before int, prelude bool) ([]transcript.RichMessage, error) {
	d.Flush()
	lo, hi, ok, err := d.bounds("rich_messages", "pos")
	if err != nil || !ok {
		return nil, err
	}
	if !prelude {
		if lo, err = d.firstRichPos(from); err != nil {
			return nil, err
		}
	}
	end := hi + 1
	if before > 0 {
		if end, err = d.firstRichPos(before); err != nil {
			return nil, err
		}
	}
	var msgs []transcript.RichMessage
	err = d.scanRange("rich_messages", "pos", lo, end, func(_ int, data []byte) error {
		var m transcript.RichMessage
		if err := json.Unmarshal(data, &m); err != nil {
			return nil // as Load does, a row that can't be read is left out
		}
		msgs = append(msgs, m)
		return nil
	})
	return msgs, err
}

// SessionEventsOf returns the stored timeline events from from up to
// before (to the last for zero), and all those before from with prelude.
func (d *DB) SessionEventsOf(from, before time.Time, prelude bool) ([]transcript.SessionEvent, error) {
	d.Flush()
	lo, hi, ok, err := d.bounds("session_events", "id")
	if err != nil || !ok {
		return nil, err
	}
	if !prelude {
		if lo, err = d.firstEventID(from); err != nil {
			return nil, err
		}
	}
	end := hi + 1
	if !before.IsZero() {
		if end, err = d.firstEventID(before); err != nil {
			return nil, err
		}
	}
	var events []transcript.SessionEvent
	err = d.scanRange("session_events", "id", lo, end, func(_ int, data []byte) error {
		var e transcript.SessionEvent
		if err := json.Unmarshal(data, &e); err != nil {
			return nil
		}
		events = append(events, e)
		return nil
	})
	return events, err
}

// EachSessionEvent calls fn with every stored timeline event, as stored
// (JSON), in order: the whole timeline, without holding it.
func (d *DB) EachSessionEvent(fn func(data []byte) error) error {
	d.Flush()
	lo, hi, ok, err := d.bounds("session_events", "id")
	if err != nil || !ok {
		return err
	}
	return d.scanRange("session_events", "id", lo, hi+1, func(_ int, data []byte) error { return fn(data) })
}

// EachEarlier calls fn with every stored rich message before position
// richPos and timeline event before ID eventID, as stored (JSON): what the
// server doesn't hold.
func (d *DB) EachEarlier(richPos, eventID int, fn func(data []byte) error) error {
	d.Flush()
	if err := d.scanRange("rich_messages", "pos", 0, richPos, func(_ int, data []byte) error { return fn(data) }); err != nil {
		return err
	}
	return d.scanRange("session_events", "id", 0, eventID, func(_ int, data []byte) error { return fn(data) })
}

// scanRange calls fn with the key and data of the rows of table whose key
// is from lo up to end, in order, historyChunk rows a query: the writes
// go on between them. fn's data is valid until it returns.
func (d *DB) scanRange(table, key string, lo, end int, fn func(key int, data []byte) error) error {
	query := "SELECT " + key + ", data FROM " + table + " WHERE " + key + " >= ? AND " + key + " < ? ORDER BY " + key + " LIMIT ?"
	type row struct {
		key  int
		data []byte
	}
	for lo < end {
		rows, err := d.db.Query(query, lo, end, historyChunk)
		if err != nil {
			return err
		}
		var chunk []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.key, &r.data); err != nil {
				_ = rows.Close()
				return err
			}
			chunk = append(chunk, r)
		}
		if err := closeRows(rows); err != nil {
			return err
		}
		if len(chunk) == 0 {
			return nil
		}
		for _, r := range chunk {
			if err := fn(r.key, r.data); err != nil {
				return err
			}
		}
		lo = chunk[len(chunk)-1].key + 1
	}
	return nil
}
