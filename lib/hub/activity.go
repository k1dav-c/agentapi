package hub

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"
	// The page names its time zone; a machine without the zone database
	// (Windows) knows the zones too.
	_ "time/tzdata"

	"github.com/coder/agentapi/lib/instanceapi"
)

// The activity: what the agents did each hour of the last year, as the
// hub saw it, for the page's heatmap (GET /api/activity). The hub keeps it
// in activity.json next to its file of instances, hour by hour in UTC, so
// the page adds up the days in its own time zone.
//
// Work is the work time the hub saw (workLog): each span that ended, split
// across the hours it covers, and the spans going on as of the request.
// Spend and tokens come from the instances' summaries: an AgentAPI lists
// what its scope spent in each of the last 25 hours (SummarySpend.Hours),
// and its figure for an hour is the hour's figure, which grows while the
// hour goes on. So the hub sets a scope's hours rather than adding to
// them, and instances of one scope set the same hours to the same figures.
// Once an hour is older than activityRecent, no summary lists it any more:
// its figures are final, and the hub adds them into the hour's totals.
// AgentAPIs before 4.2 don't list their hours: their spend doesn't count.

const (
	// activityKeep is how long the hub keeps the hours: a year and a day,
	// so a year back in any time zone is whole.
	activityKeep = 366 * 24 * time.Hour
	// activityRecent is how long the hub keeps a scope's hours apart: past
	// it, no summary lists them.
	activityRecent = 48 * time.Hour
	// activityAhead is how far ahead of the hub's clock an hour a summary
	// lists may be (a machine whose clock runs fast).
	activityAhead = 2 * time.Hour
	// maxActivityScopes bounds the scopes whose hours the hub keeps apart;
	// past it, a new scope's hours don't count until others' are final.
	maxActivityScopes = 1000
	// maxActivityDays is the most days GET /api/activity answers: 53 weeks.
	maxActivityDays = 371
	// activityPersistInterval is how long the hub gathers changes before it
	// writes activity.json, a file of up to a year of hours.
	activityPersistInterval = 15 * time.Second
	// activityVersion is the version of activity.json this hub writes.
	activityVersion = 1
)

// activitySpend is what a scope spent in an hour.
type activitySpend struct {
	usd    float64
	tokens instanceapi.SummaryTokens
}

func (s *activitySpend) add(o activitySpend) {
	s.usd += o.usd
	s.tokens.Input += o.tokens.Input
	s.tokens.Output += o.tokens.Output
	s.tokens.CacheWrite += o.tokens.CacheWrite
	s.tokens.CacheRead += o.tokens.CacheRead
	s.tokens.Total += o.tokens.Total
}

// activityHour is an hour's totals: the work time, in seconds, and what
// the scopes spent.
type activityHour struct {
	work int64
	activitySpend
}

// activity is the hub's record of the hours. Its hours are keyed by their
// start in Unix seconds.
type activity struct {
	mu sync.Mutex
	// started is when the hub began keeping it.
	started time.Time
	// hours are the totals of each hour: the work, and the spend of the
	// hours that are final.
	hours map[int64]*activityHour
	// recent are the hours of each scope summaries may still list.
	recent map[string]map[int64]activitySpend
	// counted is, for each instance, the end of the work the hub added up:
	// a span is counted from there, never twice.
	counted map[string]time.Time
	// persist writes the file; nil keeps none.
	persist *persister
}

// activityPath is the activity's file next to the instances' file
// statePath.
func activityPath(statePath string) string {
	return filepath.Join(filepath.Dir(statePath), "activity.json")
}

// activityFile is activity.json. Each hour is an array of numbers: its
// start in Unix seconds, then, in hours, the work seconds, and the USD,
// input, output, cache_write and cache_read tokens; in recent, the same
// without the work.
type activityFile struct {
	Version int                     `json:"version"`
	Started time.Time               `json:"started"`
	Hours   [][7]float64            `json:"hours"`
	Recent  map[string][][6]float64 `json:"recent,omitempty"`
	Counted map[string]int64        `json:"counted,omitempty"`
}

// newActivity returns the activity kept in path (empty keeps none), at now.
// A file that doesn't parse is moved aside, and the hub starts a new one: it
// holds statistics, not what the hub needs to run.
func newActivity(path string, logger *slog.Logger, now time.Time) *activity {
	a := &activity{started: now.UTC().Truncate(time.Second), hours: map[int64]*activityHour{}, recent: map[string]map[int64]activitySpend{}, counted: map[string]time.Time{}}
	if path == "" {
		return a
	}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		logger.Warn("Failed to read the hub's activity; starting a new one", "file", path, "error", err)
	default:
		var file activityFile
		err := json.Unmarshal(data, &file)
		if err == nil && file.Version != activityVersion {
			err = errors.New("a version this hub doesn't read")
		}
		if err != nil {
			aside := path + ".corrupt-" + now.UTC().Format("20060102T150405Z")
			if renameErr := os.Rename(path, aside); renameErr != nil {
				logger.Warn("Failed to move the hub's activity aside", "file", path, "error", renameErr)
			}
			logger.Warn("The hub's activity doesn't parse; moved it aside and starting a new one", "file", path, "moved_to", aside, "error", err)
			break
		}
		a.load(file)
		if err := writeFileAtomic(path+".bak", data); err != nil {
			logger.Warn("Failed to keep a copy of the hub's activity", "file", path+".bak", "error", err)
		}
	}
	a.foldLocked(now)
	a.persist = newPersisterEvery(path, "activity", activityPersistInterval, logger, a.snapshot)
	return a
}

// load takes what file keeps.
func (a *activity) load(file activityFile) {
	if !file.Started.IsZero() {
		a.started = file.Started
	}
	for _, row := range file.Hours {
		hour := a.hourLocked(int64(row[0]))
		hour.work += int64(row[1])
		hour.add(spendOfRow(row[2:]))
	}
	for scope, rows := range file.Recent {
		hours := map[int64]activitySpend{}
		for _, row := range rows {
			hours[int64(row[0])] = spendOfRow(row[1:])
		}
		a.recent[scope] = hours
	}
	for id, end := range file.Counted {
		a.counted[id] = time.Unix(end, 0)
	}
}

// spendOfRow is the spend of a row of the file: USD, input, output,
// cache_write and cache_read.
func spendOfRow(row []float64) activitySpend {
	tokens := instanceapi.SummaryTokens{Input: int(row[1]), Output: int(row[2]), CacheWrite: int(row[3]), CacheRead: int(row[4])}
	tokens.Total = tokens.Input + tokens.Output + tokens.CacheWrite + tokens.CacheRead
	return activitySpend{usd: row[0], tokens: tokens}
}

func (s activitySpend) row() [5]float64 {
	return [5]float64{s.usd, float64(s.tokens.Input), float64(s.tokens.Output), float64(s.tokens.CacheWrite), float64(s.tokens.CacheRead)}
}

// snapshot is the file of the activity as it is.
func (a *activity) snapshot() ([]byte, error) {
	a.mu.Lock()
	file := activityFile{Version: activityVersion, Started: a.started, Hours: make([][7]float64, 0, len(a.hours)), Recent: map[string][][6]float64{}, Counted: map[string]int64{}}
	for _, at := range slices.Sorted(maps.Keys(a.hours)) {
		hour := a.hours[at]
		spend := hour.row()
		file.Hours = append(file.Hours, [7]float64{float64(at), float64(hour.work), spend[0], spend[1], spend[2], spend[3], spend[4]})
	}
	for scope, hours := range a.recent {
		rows := make([][6]float64, 0, len(hours))
		for _, at := range slices.Sorted(maps.Keys(hours)) {
			spend := hours[at].row()
			rows = append(rows, [6]float64{float64(at), spend[0], spend[1], spend[2], spend[3], spend[4]})
		}
		file.Recent[scope] = rows
	}
	for id, end := range a.counted {
		file.Counted[id] = end.Unix()
	}
	a.mu.Unlock()
	return json.Marshal(file)
}

// changedLocked says the file is behind. Caller holds a.mu.
func (a *activity) changedLocked() {
	if a.persist != nil {
		a.persist.mark()
	}
}

// hourLocked is the totals of the hour at, made when missing. Caller holds
// a.mu.
func (a *activity) hourLocked(at int64) *activityHour {
	hour, ok := a.hours[at]
	if !ok {
		hour = &activityHour{}
		a.hours[at] = hour
	}
	return hour
}

// setSpend sets what scope spent in each of hours, as a summary at now
// lists them: an hour no summary may list any more, or too far ahead, is
// left as it is.
func (a *activity) setSpend(scope string, hours []instanceapi.SummaryHour, now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.foldLocked(now)
	from, to := now.Add(-activityRecent).Unix(), now.Add(activityAhead).Unix()
	kept, ok := a.recent[scope]
	if !ok && len(a.recent) >= maxActivityScopes {
		return
	}
	changed := false
	for _, h := range hours {
		at := h.Hour.Unix()
		at -= at % 3600
		if at < from || at > to {
			continue
		}
		spend := activitySpend{usd: h.USD, tokens: h.Tokens}
		spend.tokens.Total = spend.tokens.Input + spend.tokens.Output + spend.tokens.CacheWrite + spend.tokens.CacheRead
		if kept == nil {
			kept = map[int64]activitySpend{}
			a.recent[scope] = kept
		}
		if old, ok := kept[at]; !ok || old != spend {
			kept[at] = spend
			changed = true
		}
	}
	if changed {
		a.changedLocked()
	}
}

// addWork adds the work of the instance id from start to end, but what of
// it the hub counted already, at now.
func (a *activity) addWork(id string, start, end, now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if counted := a.counted[id]; start.Before(counted) {
		start = counted
	}
	if !end.After(start) {
		return
	}
	a.counted[id] = end
	splitHours(start, end, func(at, seconds int64) {
		a.hourLocked(at).work += seconds
	})
	a.foldLocked(now)
	a.changedLocked()
}

// splitHours calls add with the start of each hour from start to end and
// the whole seconds of it within them.
func splitHours(start, end time.Time, add func(at, seconds int64)) {
	from, to := start.Unix(), end.Unix()
	for from < to {
		at := from - from%3600
		next := min(at+3600, to)
		add(at, next-from)
		from = next
	}
}

// foldLocked adds the scopes' hours that are final into the totals, and
// forgets what is older than activityKeep. Caller holds a.mu.
func (a *activity) foldLocked(now time.Time) {
	final := now.Add(-activityRecent).Unix()
	keep := now.Add(-activityKeep)
	changed := false
	for scope, hours := range a.recent {
		for at, spend := range hours {
			if at < final {
				if at >= keep.Unix() {
					a.hourLocked(at).add(spend)
				}
				delete(hours, at)
				changed = true
			}
		}
		if len(hours) == 0 {
			delete(a.recent, scope)
		}
	}
	for at := range a.hours {
		if at < keep.Unix() {
			delete(a.hours, at)
			changed = true
		}
	}
	for id, end := range a.counted {
		if end.Before(keep) {
			delete(a.counted, id)
		}
	}
	if changed {
		a.changedLocked()
	}
}

// activityBody is GET /api/activity: the totals of each day of the last
// days in the time zone asked for, the earliest first, and since, the
// first day the hub kept the activity, before which a day's zeros mean
// nothing.
type activityBody struct {
	Days     []activityDay `json:"days" nullable:"false"`
	Since    string        `json:"since" doc:"The first day the hub kept its activity, as YYYY-MM-DD in the time zone asked for: the days before it aren't counted."`
	TimeZone string        `json:"time_zone" doc:"The time zone of the days."`
}

// activityDay is a day's totals, as the hub saw them.
type activityDay struct {
	Date        string                    `json:"date" doc:"The day, as YYYY-MM-DD."`
	WorkSeconds int64                     `json:"work_seconds" doc:"How long the agents worked that day, as the hub saw it, each one's time added up."`
	USD         float64                   `json:"usd" doc:"What the agents' sessions spent that day, API-equivalent at list prices; each scope once. AgentAPIs before 4.2 don't count."`
	Tokens      instanceapi.SummaryTokens `json:"tokens" doc:"The tokens of the calls usd adds up."`
}

// days is the totals of the days days to now in loc, each hour in the day
// it starts in, with the spans of work going on (ongoing: since when each
// instance works) up to now.
func (a *activity) days(loc *time.Location, days int, now time.Time, ongoing map[string]time.Time) activityBody {
	local := now.In(loc)
	year, month, day := local.Date()
	body := activityBody{Days: make([]activityDay, days), TimeZone: loc.String()}
	index := make(map[string]int, days)
	for n := range days {
		date := time.Date(year, month, day-(days-1-n), 0, 0, 0, 0, loc).Format(time.DateOnly)
		body.Days[n].Date = date
		index[date] = n
	}
	dayOf := func(at int64) *activityDay {
		if n, ok := index[time.Unix(at, 0).In(loc).Format(time.DateOnly)]; ok {
			return &body.Days[n]
		}
		return nil
	}
	addSpend := func(d *activityDay, spend activitySpend) {
		d.USD += spend.usd
		d.Tokens.Input += spend.tokens.Input
		d.Tokens.Output += spend.tokens.Output
		d.Tokens.CacheWrite += spend.tokens.CacheWrite
		d.Tokens.CacheRead += spend.tokens.CacheRead
		d.Tokens.Total += spend.tokens.Total
	}

	a.mu.Lock()
	earliest := a.started
	note := func(at int64) {
		if t := time.Unix(at, 0); t.Before(earliest) {
			earliest = t
		}
	}
	for at, hour := range a.hours {
		note(at)
		if d := dayOf(at); d != nil {
			d.WorkSeconds += hour.work
			addSpend(d, hour.activitySpend)
		}
	}
	for _, hours := range a.recent {
		for at, spend := range hours {
			note(at)
			if d := dayOf(at); d != nil {
				addSpend(d, spend)
			}
		}
	}
	for id, since := range ongoing {
		if counted := a.counted[id]; since.Before(counted) {
			since = counted
		}
		splitHours(since, now, func(at, seconds int64) {
			note(at)
			if d := dayOf(at); d != nil {
				d.WorkSeconds += seconds
			}
		})
	}
	a.mu.Unlock()

	for n := range body.Days {
		body.Days[n].USD = math.Round(body.Days[n].USD*1e6) / 1e6
	}
	body.Since = earliest.In(loc).Format(time.DateOnly)
	return body
}

// close stops writing the file, after it writes what is pending.
func (a *activity) close() error {
	if a.persist == nil {
		return nil
	}
	return a.persist.close()
}

func (a *activity) flush() error {
	if a.persist == nil {
		return nil
	}
	return a.persist.flush()
}

// activityDays is the activity's days in loc: days of them to now, with
// the spans of work going on.
func (s *store) activityDays(loc *time.Location, days int) activityBody {
	s.mu.Lock()
	now := s.clock.Now()
	ongoing := map[string]time.Time{}
	for id, i := range s.instances {
		if !i.work.since.IsZero() {
			ongoing[id] = i.work.since
		}
	}
	s.mu.Unlock()
	// A span that ends meanwhile is counted up to where it ended once:
	// the activity counts each instance's work from where it stopped.
	return s.activity.days(loc, days, now, ongoing)
}

// getActivity answers GET /api/activity: the totals of each day of the
// last days (?days=, maxActivityDays by default and at most) in the time
// zone ?tz= names (an IANA name such as Asia/Taipei; UTC by default).
func (h *Hub) getActivity(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	loc := time.UTC
	if tz := query.Get("tz"); tz != "" {
		var err error
		if loc, err = time.LoadLocation(tz); err != nil || tz == "Local" {
			writeError(w, http.StatusBadRequest, "tz must name a time zone, such as Asia/Taipei")
			return
		}
	}
	days := maxActivityDays
	if given := query.Get("days"); given != "" {
		n, err := strconv.Atoi(given)
		if err != nil || n < 1 || n > maxActivityDays {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("days must be from 1 to %d", maxActivityDays))
			return
		}
		days = n
	}
	writeJSON(w, http.StatusOK, h.store.activityDays(loc, days))
}
