package availability

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"time"
)

// MaxRules caps how many weekly rules one provider may have.
const MaxRules = 100

// ErrInvalidRule reports a malformed or overlapping availability rule.
var ErrInvalidRule = errors.New("invalid availability rule")

// Clock is a local wall-clock time of day, in minutes since midnight.
// Valid values are 0 (00:00) through 1440 (24:00, only meaningful as an end time).
type Clock int

// EndOfDay is 24:00, the latest possible end of a rule.
const EndOfDay Clock = 24 * 60

// ParseClock parses "HH:MM" (24-hour, zero-padded). "24:00" is accepted as end of day.
func ParseClock(s string) (Clock, error) {
	digit := func(b byte) bool { return b >= '0' && b <= '9' }
	if len(s) != 5 || s[2] != ':' || !digit(s[0]) || !digit(s[1]) || !digit(s[3]) || !digit(s[4]) {
		return 0, fmt.Errorf("%w: time %q must be HH:MM", ErrInvalidRule, s)
	}
	h := int(s[0]-'0')*10 + int(s[1]-'0')
	m := int(s[3]-'0')*10 + int(s[4]-'0')
	c := Clock(h*60 + m)
	if m > 59 || c > EndOfDay {
		return 0, fmt.Errorf("%w: time %q out of range", ErrInvalidRule, s)
	}
	return c, nil
}

// String formats c as "HH:MM".
func (c Clock) String() string {
	return fmt.Sprintf("%02d:%02d", int(c)/60, int(c)%60)
}

// Rule is a weekly availability window in the provider's local time: every
// Weekday from Start (inclusive) to End (exclusive).
type Rule struct {
	Weekday time.Weekday
	Start   Clock
	End     Clock
}

// Interval is the half-open time range [Start, End).
type Interval struct {
	Start time.Time
	End   time.Time
}

// Overlaps reports whether i and o share any instant. Touching intervals do not overlap.
func (i Interval) Overlaps(o Interval) bool {
	return i.Start.Before(o.End) && o.Start.Before(i.End)
}

// Contains reports whether o lies entirely within i.
func (i Interval) Contains(o Interval) bool {
	return !o.Start.Before(i.Start) && !o.End.After(i.End)
}

// ValidateRules checks ranges and rejects rules that overlap on the same weekday.
// Adjacent rules (one ends when the next starts) are allowed.
func ValidateRules(rules []Rule) error {
	if len(rules) > MaxRules {
		return fmt.Errorf("%w: at most %d rules allowed", ErrInvalidRule, MaxRules)
	}
	for _, r := range rules {
		if r.Weekday < time.Sunday || r.Weekday > time.Saturday {
			return fmt.Errorf("%w: weekday %d out of range", ErrInvalidRule, r.Weekday)
		}
		if r.Start < 0 || r.End > EndOfDay || r.Start >= r.End {
			return fmt.Errorf("%w: %s %s-%s must satisfy 00:00 <= start < end <= 24:00",
				ErrInvalidRule, r.Weekday, r.Start, r.End)
		}
	}
	sorted := sortRules(rules)
	for i := 1; i < len(sorted); i++ {
		prev, cur := sorted[i-1], sorted[i]
		if prev.Weekday == cur.Weekday && prev.End > cur.Start {
			return fmt.Errorf("%w: %s %s-%s overlaps %s-%s",
				ErrInvalidRule, cur.Weekday, prev.Start, prev.End, cur.Start, cur.End)
		}
	}
	return nil
}

// Windows expands weekly rules into absolute availability windows that
// overlap [from, to). Windows are returned whole (not clipped to the range), sorted.
//
// Rules are wall-clock times in loc, so a 09:00-17:00 rule stays 09:00-17:00
// local on DST transition days. When a rule boundary falls in a DST gap
// (a skipped wall time) it resolves to the transition instant; when it falls in
// a DST overlap (a repeated wall time) the start takes the earlier instant and
// the end the later one, so the window covers every instant the wall clock
// spends inside it.
func Windows(rules []Rule, loc *time.Location, from, to time.Time) []Interval {
	if len(rules) == 0 || !from.Before(to) {
		return nil
	}
	span := Interval{Start: from, End: to}
	sorted := sortRules(rules)

	// A window never extends past its own local day, so local days
	// [day(from)-1, day(to)] cover every window that can overlap the range.
	first := civilDate(from.In(loc)).AddDate(0, 0, -1)
	last := civilDate(to.In(loc))

	var out []Interval
	for day := first; !day.After(last); day = day.AddDate(0, 0, 1) {
		for _, r := range sorted {
			if r.Weekday != day.Weekday() {
				continue
			}
			w := Interval{
				Start: wallToInstant(day, r.Start, loc, earliest),
				End:   wallToInstant(day, r.End, loc, latest),
			}
			if w.Start.Before(w.End) && w.Overlaps(span) {
				out = append(out, w)
			}
		}
	}
	return out
}

// Slots cuts windows into back-to-back slots of length d, starting at each
// window's start, and keeps those that lie within [from, to) and overlap no busy
// interval. Slot boundaries are aligned to the window start, not to from, so
// paging through time never shifts the grid.
func Slots(windows []Interval, from, to time.Time, d time.Duration, busy []Interval) []Interval {
	out := []Interval{}
	if d <= 0 || !from.Before(to) {
		return out
	}
	span := Interval{Start: from, End: to}
	for _, w := range windows {
		for s := w.Start; !s.Add(d).After(w.End); s = s.Add(d) {
			slot := Interval{Start: s, End: s.Add(d)}
			if !span.Contains(slot) || overlapsAny(busy, slot) {
				continue
			}
			out = append(out, slot)
		}
	}
	return out
}

func overlapsAny(busy []Interval, slot Interval) bool {
	for _, b := range busy {
		if b.Overlaps(slot) {
			return true
		}
	}
	return false
}

func sortRules(rules []Rule) []Rule {
	sorted := slices.Clone(rules)
	slices.SortFunc(sorted, func(a, b Rule) int {
		return cmp.Or(cmp.Compare(a.Weekday, b.Weekday), cmp.Compare(a.Start, b.Start))
	})
	return sorted
}

// civilDate returns t's calendar date as midnight UTC. Using UTC for calendar
// arithmetic avoids DST effects on AddDate and Weekday.
func civilDate(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

type pick int

const (
	earliest pick = iota
	latest
)

// wallToInstant returns the instant at which the wall clock in loc shows clock c
// on the civil date day (midnight UTC, see civilDate).
//
// time.Date leaves the result unspecified for wall times that do not exist or
// occur twice, so this resolves both cases explicitly:
//   - repeated (DST fall-back): returns the earliest or latest occurrence per p;
//   - skipped (DST spring-forward): returns the transition instant, the first
//     moment the clock shows a time later than c.
func wallToInstant(day time.Time, c Clock, loc *time.Location, p pick) time.Time {
	naive := day.Add(time.Duration(c) * time.Minute) // the wall time, read as if it were UTC

	// Offsets in effect a day either side cover both sides of any transition
	// near this wall time. Each offset gives a candidate instant; it is valid if
	// loc actually uses that offset at that instant.
	var valid []time.Time
	for _, probe := range []time.Time{naive.Add(-24 * time.Hour), naive, naive.Add(24 * time.Hour)} {
		_, off := probe.In(loc).Zone()
		t := naive.Add(-time.Duration(off) * time.Second)
		if _, got := t.In(loc).Zone(); got == off && !slices.ContainsFunc(valid, t.Equal) {
			valid = append(valid, t)
		}
	}

	if len(valid) == 0 {
		// Skipped wall time. Using the pre-transition offset lands after the
		// transition; the start of the zone in effect there is the transition.
		_, off := naive.Add(-24 * time.Hour).In(loc).Zone()
		start, _ := naive.Add(-time.Duration(off) * time.Second).In(loc).ZoneBounds()
		return start.UTC()
	}

	slices.SortFunc(valid, func(a, b time.Time) int { return a.Compare(b) })
	if p == latest {
		return valid[len(valid)-1].UTC()
	}
	return valid[0].UTC()
}
