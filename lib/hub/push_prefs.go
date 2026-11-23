package hub

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// maxPushGroups is how many groups a device may follow.
const maxPushGroups = 50

// PushPrefs are what a device gets pushed. Exported, as Record is, so
// the schema has the fields of the types that embed it.
type PushPrefs struct {
	// Finished pushes the finished tasks too, besides what needs you.
	Finished bool `json:"finished" doc:"Push when an agent finishes a task, not only when it needs you."`
	// Issues pushes how the issues' work goes: a pull request opened, a
	// job that failed, one done.
	Issues bool `json:"issues" doc:"Push the issues' work: a pull request opened, a job failed, a job done."`
	// QuietHours, when set, are when nothing is pushed.
	QuietHours *quietHours `json:"quiet_hours,omitempty" doc:"When nothing is pushed to the device; missing for never."`
	// Groups and Pinned pick the agents whose needs and tasks are pushed:
	// those in Groups ("" for the ungrouped ones), and with Pinned the
	// pinned ones; neither is every agent. The issues' pushes aren't
	// picked by them.
	Groups []string `json:"groups" nullable:"false" doc:"The groups whose agents are pushed (\"\" for the ungrouped ones); with pinned false, none is every agent."`
	Pinned bool     `json:"pinned" doc:"Push the pinned agents, besides those of groups."`
}

// quietHours is a time of day, in a time zone, when nothing is pushed:
// from Start up to End, past midnight when End comes before it.
type quietHours struct {
	Start    string `json:"start" doc:"When the quiet starts, HH:MM." example:"22:00"`
	End      string `json:"end" doc:"When it ends, HH:MM." example:"08:00"`
	TimeZone string `json:"time_zone" doc:"The IANA time zone the times are in, such as Asia/Taipei." example:"Asia/Taipei"`
}

func parseClock(s string) (int, bool) {
	t, err := time.Parse("15:04", s)
	if err != nil || len(s) != 5 {
		return 0, false
	}
	return t.Hour()*60 + t.Minute(), true
}

func (q quietHours) validate() error {
	start, okStart := parseClock(q.Start)
	end, okEnd := parseClock(q.End)
	if !okStart || !okEnd {
		return errors.New("quiet_hours start and end are times of day, HH:MM")
	}
	if start == end {
		return errors.New("quiet_hours start and end are the same time")
	}
	if _, err := loadLocation(q.TimeZone); err != nil || q.TimeZone == "" {
		return fmt.Errorf("quiet_hours time_zone %q isn't an IANA time zone", q.TimeZone)
	}
	return nil
}

// quiet says whether at falls in the quiet hours.
func (q *quietHours) quiet(at time.Time) bool {
	if q == nil {
		return false
	}
	start, okStart := parseClock(q.Start)
	end, okEnd := parseClock(q.End)
	loc, err := loadLocation(q.TimeZone)
	if !okStart || !okEnd || err != nil {
		return false
	}
	local := at.In(loc)
	now := local.Hour()*60 + local.Minute()
	if start < end {
		return start <= now && now < end
	}
	return now >= start || now < end
}

// follows says whether the device follows the agent i: its group, or its
// pin. groups are the groups the agents are in now ("" for none): a group
// followed that no agent is in any more (renamed, or emptied) counts for
// nothing, and a device left following none of them gets every agent, as
// one that follows none.
func (p PushPrefs) follows(i Instance, groups map[string]bool) bool {
	if p.Pinned && i.Pin > 0 {
		return true
	}
	followed := false
	for _, group := range p.Groups {
		if groups[group] {
			followed = true
			if group == i.Group {
				return true
			}
		}
	}
	return !followed && !p.Pinned
}

// wants says whether the device gets payload at now; i is the agent it
// is about, nil for an issue's, and groups the agents' groups now
// (follows).
func (p PushPrefs) wants(payload pushPayload, i *Instance, now time.Time, groups map[string]bool) bool {
	switch payload.Kind {
	case pushTest:
		return true
	case pushFinished:
		if !p.Finished {
			return false
		}
	case pushIssue:
		if !p.Issues {
			return false
		}
	case pushNeedsYou:
	}
	if i != nil && !p.follows(*i, groups) {
		return false
	}
	return !p.QuietHours.quiet(now)
}

// pushPrefsChange is PATCH /api/push/devices/{id}: the settings it sets,
// the others left out.
type pushPrefsChange struct {
	Finished *bool `json:"finished,omitempty"`
	Issues   *bool `json:"issues,omitempty"`
	// QuietHours sets the quiet hours; an empty object removes them.
	QuietHours *quietHours `json:"quiet_hours,omitempty" doc:"The quiet hours; {} removes them."`
	Groups     *[]string   `json:"groups,omitempty"`
	Pinned     *bool       `json:"pinned,omitempty"`
}

func (c pushPrefsChange) validate() error {
	if c.QuietHours != nil && *c.QuietHours != (quietHours{}) {
		if err := c.QuietHours.validate(); err != nil {
			return err
		}
	}
	if c.Groups != nil {
		if len(*c.Groups) > maxPushGroups {
			return fmt.Errorf("a device follows at most %d groups", maxPushGroups)
		}
		for _, group := range *c.Groups {
			if utf8.RuneCountInString(group) > maxGroupName || group != strings.Join(strings.Fields(group), " ") {
				return fmt.Errorf("%q isn't a group name", group)
			}
		}
	}
	return nil
}

func (c pushPrefsChange) apply(p *PushPrefs) {
	if c.Finished != nil {
		p.Finished = *c.Finished
	}
	if c.Issues != nil {
		p.Issues = *c.Issues
	}
	if c.QuietHours != nil {
		if *c.QuietHours == (quietHours{}) {
			p.QuietHours = nil
		} else {
			quiet := *c.QuietHours
			p.QuietHours = &quiet
		}
	}
	if c.Groups != nil {
		groups := slices.Clone(*c.Groups)
		slices.Sort(groups)
		p.Groups = slices.Compact(groups)
	}
	if c.Pinned != nil {
		p.Pinned = *c.Pinned
	}
}

// locations keeps the time zones loaded: each push checks every device's
// quiet hours, and time.LoadLocation reads the zone's file each time.
var locations sync.Map

func loadLocation(name string) (*time.Location, error) {
	if loc, ok := locations.Load(name); ok {
		return loc.(*time.Location), nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, err
	}
	locations.Store(name, loc)
	return loc, nil
}
