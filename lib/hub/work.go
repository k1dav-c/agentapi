package hub

import (
	"time"
)

const (
	// workDay and workWeek are the windows of the work time the hub sends.
	workDay  = 24 * time.Hour
	workWeek = 7 * 24 * time.Hour
	// maxWorkSpans bounds what the hub keeps of an instance's week: past
	// it, the oldest spans go.
	maxWorkSpans = 2000
)

// workSpan is a stretch of time the hub saw an instance working.
type workSpan struct {
	start, end time.Time
}

// workLog is when the hub saw an instance working: in the state working
// (instanceapi.StateWorking) while it followed its summary stream. It
// keeps the spans that ended within the last 7 days, oldest first, and
// since, when the one going on began (zero when none is).
//
// The hub counts only what it saw: time before it kept a log, while it was
// down or while it couldn't reach the instance isn't in it.
type workLog struct {
	spans []workSpan
	since time.Time
}

// WorkTime is how long the hub saw an instance working, as GET
// /api/instances and the browser's stream send it: in the last 24 hours
// and 7 days, from the spans that ended, when the hub sent it, and since
// when it works now. The page adds the time since WorkingSince itself, so
// the hub doesn't send the instance every second.
type WorkTime struct {
	Day          int64     `json:"last_24h_seconds"`
	Week         int64     `json:"last_7d_seconds"`
	WorkingSince time.Time `json:"working_since,omitzero"`
}

// at is the work time at now.
func (w workLog) at(now time.Time) WorkTime {
	work := WorkTime{WorkingSince: w.since}
	day, week := now.Add(-workDay), now.Add(-workWeek)
	for _, span := range w.spans {
		work.Day += overlap(span, day, now)
		work.Week += overlap(span, week, now)
	}
	return work
}

// overlap is the whole seconds of span between from and to.
func overlap(span workSpan, from, to time.Time) int64 {
	start, end := span.start, span.end
	if start.Before(from) {
		start = from
	}
	if end.After(to) {
		end = to
	}
	if !end.After(start) {
		return 0
	}
	return int64(end.Sub(start) / time.Second)
}

// track follows the instance's state at now, and reports whether the log
// changed: working starts a span, anything else ends the one going on.
// One that ends because the hub lost the instance's stream ends at heard,
// when the stream last said anything, if that is earlier: the hub doesn't
// count what it didn't see.
func (w *workLog) track(working bool, now, heard time.Time) bool {
	switch {
	case working && w.since.IsZero():
		w.since = now
		return true
	case !working && !w.since.IsZero():
		end := now
		if !heard.IsZero() && heard.Before(end) {
			end = heard
		}
		w.end(end, now)
		return true
	}
	return false
}

// end ends the span going on at end, and forgets what is older than a
// week at now.
func (w *workLog) end(end, now time.Time) {
	if end.After(w.since) {
		w.spans = append(w.spans, workSpan{start: w.since, end: end})
	}
	w.since = time.Time{}
	w.prune(now)
}

// prune forgets the spans that ended over a week before now, and the
// oldest past maxWorkSpans. It slices rather than moves them: copies of the
// instance share the array.
func (w *workLog) prune(now time.Time) {
	week := now.Add(-workWeek)
	first := 0
	for first < len(w.spans) && !w.spans[first].end.After(week) {
		first++
	}
	first = max(first, len(w.spans)-maxWorkSpans)
	if first > 0 {
		w.spans = w.spans[first:]
	}
}

// workMinutes is a work time as the page shows it, in minutes: the hub
// sends an instance again when its windows move by one.
type workMinutes struct {
	day, week int64
}

func (w WorkTime) minutes() workMinutes {
	return workMinutes{day: w.Day / 60, week: w.Week / 60}
}

// storedWork is a workLog in the file of instances: each span as its
// start and end in Unix seconds, and since, the start of the one going on.
// An older hub ignores it, and writes the file without it.
type storedWork struct {
	Spans [][2]int64 `json:"spans,omitempty"`
	Since int64      `json:"since,omitempty"`
}

// stored is the log in the file; nil when it is empty.
func (w workLog) stored() *storedWork {
	if len(w.spans) == 0 && w.since.IsZero() {
		return nil
	}
	stored := &storedWork{Spans: make([][2]int64, len(w.spans))}
	for n, span := range w.spans {
		stored.Spans[n] = [2]int64{span.start.Unix(), span.end.Unix()}
	}
	if !w.since.IsZero() {
		stored.Since = w.since.Unix()
	}
	return stored
}

// log is the log a hub kept, at now. A span that was going on when the hub
// stopped without closing it (it was killed) ends at lastSeen, when the hub
// last kept the instance's file: what it knew.
func (s *storedWork) log(lastSeen, now time.Time) workLog {
	var w workLog
	if s == nil {
		return w
	}
	for _, span := range s.Spans {
		start, end := time.Unix(span[0], 0), time.Unix(span[1], 0)
		if end.After(start) && (len(w.spans) == 0 || !start.Before(w.spans[len(w.spans)-1].end)) {
			w.spans = append(w.spans, workSpan{start: start, end: end})
		}
	}
	if s.Since > 0 {
		w.since = time.Unix(s.Since, 0)
		w.end(lastSeen, now)
	}
	w.prune(now)
	return w
}
