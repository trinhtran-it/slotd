package availability

import (
	"errors"
	"testing"
	"time"
)

func mustLoad(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load location %q: %v", name, err)
	}
	return loc
}

func ts(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return v
}

func clk(t *testing.T, s string) Clock {
	t.Helper()
	c, err := ParseClock(s)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestParseClock(t *testing.T) {
	tests := []struct {
		in      string
		want    Clock
		wantErr bool
	}{
		{in: "00:00", want: 0},
		{in: "09:30", want: 570},
		{in: "23:59", want: 1439},
		{in: "24:00", want: EndOfDay},
		{in: "24:01", wantErr: true},
		{in: "12:60", wantErr: true},
		{in: "9:00", wantErr: true},
		{in: " 9:00", wantErr: true},
		{in: "-1:00", wantErr: true},
		{in: "ab:cd", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseClock(tt.in)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidRule) {
					t.Fatalf("ParseClock(%q) error = %v, want ErrInvalidRule", tt.in, err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("ParseClock(%q) = %d, %v; want %d", tt.in, got, err, tt.want)
			}
			if got.String() != tt.in {
				t.Errorf("String() = %q, want %q", got.String(), tt.in)
			}
		})
	}
}

func TestValidateRules(t *testing.T) {
	tests := []struct {
		name    string
		rules   []Rule
		wantErr bool
	}{
		{name: "empty is valid", rules: nil},
		{name: "single", rules: []Rule{{time.Monday, 540, 1020}}},
		{name: "adjacent same day", rules: []Rule{{time.Monday, 540, 720}, {time.Monday, 720, 1020}}},
		{name: "same hours different days", rules: []Rule{{time.Monday, 540, 1020}, {time.Tuesday, 540, 1020}}},
		{name: "full day", rules: []Rule{{time.Sunday, 0, EndOfDay}}},
		{name: "overlap same day", rules: []Rule{{time.Monday, 720, 1020}, {time.Monday, 540, 721}}, wantErr: true},
		{name: "end before start", rules: []Rule{{time.Monday, 600, 540}}, wantErr: true},
		{name: "empty window", rules: []Rule{{time.Monday, 600, 600}}, wantErr: true},
		{name: "past end of day", rules: []Rule{{time.Monday, 600, EndOfDay + 1}}, wantErr: true},
		{name: "bad weekday", rules: []Rule{{time.Weekday(7), 540, 600}}, wantErr: true},
		{name: "too many", rules: make([]Rule, MaxRules+1), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRules(tt.rules)
			if tt.wantErr != (err != nil) {
				t.Fatalf("ValidateRules() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalidRule) {
				t.Fatalf("error %v does not wrap ErrInvalidRule", err)
			}
		})
	}
}

// Europe/Berlin DST in 2026:
//   - spring forward: Sun 2026-03-29, 02:00 CET (+01) jumps to 03:00 CEST (+02) at 01:00Z;
//   - fall back:      Sun 2026-10-25, 03:00 CEST (+02) falls to 02:00 CET (+01) at 01:00Z.
func TestWallToInstant(t *testing.T) {
	berlin := mustLoad(t, "Europe/Berlin")
	tests := []struct {
		name  string
		day   string
		clock string
		pick  pick
		want  string
	}{
		{name: "winter", day: "2026-01-12", clock: "09:00", want: "2026-01-12T08:00:00Z"},
		{name: "summer", day: "2026-07-13", clock: "09:00", want: "2026-07-13T07:00:00Z"},
		{name: "gap start resolves to transition", day: "2026-03-29", clock: "02:00", want: "2026-03-29T01:00:00Z"},
		{name: "gap middle resolves to transition", day: "2026-03-29", clock: "02:30", pick: latest, want: "2026-03-29T01:00:00Z"},
		{name: "just after gap", day: "2026-03-29", clock: "03:00", want: "2026-03-29T01:00:00Z"},
		{name: "repeated hour earliest", day: "2026-10-25", clock: "02:30", pick: earliest, want: "2026-10-25T00:30:00Z"},
		{name: "repeated hour latest", day: "2026-10-25", clock: "02:30", pick: latest, want: "2026-10-25T01:30:00Z"},
		{name: "end of day is next midnight", day: "2026-10-25", clock: "24:00", want: "2026-10-25T23:00:00Z"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			day := ts(t, tt.day+"T00:00:00Z")
			got := wallToInstant(day, clk(t, tt.clock), berlin, tt.pick)
			if want := ts(t, tt.want); !got.Equal(want) {
				t.Fatalf("wallToInstant(%s %s) = %s, want %s", tt.day, tt.clock, got, want)
			}
		})
	}
}

func TestSlots(t *testing.T) {
	utc := time.UTC
	berlin := mustLoad(t, "Europe/Berlin")

	tests := []struct {
		name     string
		loc      *time.Location
		rules    []Rule
		from, to string
		duration time.Duration
		busy     [][2]string
		want     []string // slot starts, RFC 3339
	}{
		{
			name:     "simple window",
			loc:      utc,
			rules:    []Rule{{time.Monday, 540, 720}}, // 09:00-12:00
			from:     "2026-01-12T00:00:00Z",
			to:       "2026-01-13T00:00:00Z",
			duration: time.Hour,
			want:     []string{"2026-01-12T09:00:00Z", "2026-01-12T10:00:00Z", "2026-01-12T11:00:00Z"},
		},
		{
			name:     "duration not dividing window drops the remainder",
			loc:      utc,
			rules:    []Rule{{time.Monday, 540, 630}}, // 09:00-10:30
			from:     "2026-01-12T00:00:00Z",
			to:       "2026-01-13T00:00:00Z",
			duration: time.Hour,
			want:     []string{"2026-01-12T09:00:00Z"},
		},
		{
			name:     "booking removes exact slot",
			loc:      utc,
			rules:    []Rule{{time.Monday, 540, 720}},
			from:     "2026-01-12T00:00:00Z",
			to:       "2026-01-13T00:00:00Z",
			duration: time.Hour,
			busy:     [][2]string{{"2026-01-12T10:00:00Z", "2026-01-12T11:00:00Z"}},
			want:     []string{"2026-01-12T09:00:00Z", "2026-01-12T11:00:00Z"},
		},
		{
			name:     "booking straddling two slots removes both",
			loc:      utc,
			rules:    []Rule{{time.Monday, 540, 720}},
			from:     "2026-01-12T00:00:00Z",
			to:       "2026-01-13T00:00:00Z",
			duration: time.Hour,
			busy:     [][2]string{{"2026-01-12T10:30:00Z", "2026-01-12T11:30:00Z"}},
			want:     []string{"2026-01-12T09:00:00Z"},
		},
		{
			name:     "touching booking does not block",
			loc:      utc,
			rules:    []Rule{{time.Monday, 540, 660}},
			from:     "2026-01-12T00:00:00Z",
			to:       "2026-01-13T00:00:00Z",
			duration: time.Hour,
			busy:     [][2]string{{"2026-01-12T08:00:00Z", "2026-01-12T09:00:00Z"}},
			want:     []string{"2026-01-12T09:00:00Z", "2026-01-12T10:00:00Z"},
		},
		{
			name:     "grid stays aligned to window start when from is mid-window",
			loc:      utc,
			rules:    []Rule{{time.Monday, 540, 720}},
			from:     "2026-01-12T09:15:00Z",
			to:       "2026-01-12T11:45:00Z",
			duration: time.Hour,
			want:     []string{"2026-01-12T10:00:00Z"},
		},
		{
			name:     "multi-day range and weekday filter",
			loc:      utc,
			rules:    []Rule{{time.Monday, 540, 600}, {time.Wednesday, 540, 600}},
			from:     "2026-01-12T00:00:00Z", // Monday
			to:       "2026-01-19T00:00:00Z",
			duration: time.Hour,
			want:     []string{"2026-01-12T09:00:00Z", "2026-01-14T09:00:00Z"},
		},
		{
			name:     "rule ending at 24:00",
			loc:      utc,
			rules:    []Rule{{time.Monday, 1380, EndOfDay}},
			from:     "2026-01-12T00:00:00Z",
			to:       "2026-01-14T00:00:00Z",
			duration: 30 * time.Minute,
			want:     []string{"2026-01-12T23:00:00Z", "2026-01-12T23:30:00Z"},
		},
		{
			name:     "berlin winter offset",
			loc:      berlin,
			rules:    []Rule{{time.Monday, 540, 660}},
			from:     "2026-01-12T00:00:00+01:00",
			to:       "2026-01-13T00:00:00+01:00",
			duration: time.Hour,
			want:     []string{"2026-01-12T09:00:00+01:00", "2026-01-12T10:00:00+01:00"},
		},
		{
			name:     "berlin business hours keep wall-clock times on spring-forward day",
			loc:      berlin,
			rules:    []Rule{{time.Sunday, 540, 1020}}, // 09:00-17:00
			from:     "2026-03-29T00:00:00+01:00",
			to:       "2026-03-30T00:00:00+02:00",
			duration: 4 * time.Hour,
			want:     []string{"2026-03-29T09:00:00+02:00", "2026-03-29T13:00:00+02:00"},
		},
		{
			name:     "berlin night shift on spring-forward day has 5 real hours",
			loc:      berlin,
			rules:    []Rule{{time.Sunday, 0, 360}}, // 00:00-06:00 local
			from:     "2026-03-29T00:00:00+01:00",
			to:       "2026-03-30T00:00:00+02:00",
			duration: time.Hour,
			want: []string{
				"2026-03-29T00:00:00+01:00",
				"2026-03-29T01:00:00+01:00",
				// 02:00 local does not exist
				"2026-03-29T03:00:00+02:00",
				"2026-03-29T04:00:00+02:00",
				"2026-03-29T05:00:00+02:00",
			},
		},
		{
			name:     "berlin night shift on fall-back day has 7 real hours",
			loc:      berlin,
			rules:    []Rule{{time.Sunday, 0, 360}},
			from:     "2026-10-25T00:00:00+02:00",
			to:       "2026-10-26T00:00:00+01:00",
			duration: time.Hour,
			want: []string{
				"2026-10-25T00:00:00+02:00",
				"2026-10-25T01:00:00+02:00",
				"2026-10-25T02:00:00+02:00", // 02:00 local, first time
				"2026-10-25T02:00:00+01:00", // 02:00 local, second time
				"2026-10-25T03:00:00+01:00",
				"2026-10-25T04:00:00+01:00",
				"2026-10-25T05:00:00+01:00",
			},
		},
		{
			name:     "rule starting inside the DST gap starts at the transition",
			loc:      berlin,
			rules:    []Rule{{time.Sunday, 150, 240}}, // 02:30-04:00 local
			from:     "2026-03-29T00:00:00+01:00",
			to:       "2026-03-30T00:00:00+02:00",
			duration: 30 * time.Minute,
			want:     []string{"2026-03-29T03:00:00+02:00", "2026-03-29T03:30:00+02:00"},
		},
		{
			name:     "rule entirely inside the DST gap yields nothing",
			loc:      berlin,
			rules:    []Rule{{time.Sunday, 120, 180}}, // 02:00-03:00 local
			from:     "2026-03-29T00:00:00+01:00",
			to:       "2026-03-30T00:00:00+02:00",
			duration: 30 * time.Minute,
			want:     nil,
		},
		{
			name:     "empty range",
			loc:      utc,
			rules:    []Rule{{time.Monday, 540, 720}},
			from:     "2026-01-12T12:00:00Z",
			to:       "2026-01-12T12:00:00Z",
			duration: time.Hour,
			want:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			from, to := ts(t, tt.from), ts(t, tt.to)
			busy := make([]Interval, 0, len(tt.busy))
			for _, b := range tt.busy {
				busy = append(busy, Interval{Start: ts(t, b[0]), End: ts(t, b[1])})
			}

			got := Slots(Windows(tt.rules, tt.loc, from, to), from, to, tt.duration, busy)

			if len(got) != len(tt.want) {
				t.Fatalf("got %d slots %v, want %d %v", len(got), got, len(tt.want), tt.want)
			}
			for i, s := range got {
				if want := ts(t, tt.want[i]); !s.Start.Equal(want) {
					t.Errorf("slot %d start = %s, want %s", i, s.Start.In(tt.loc).Format(time.RFC3339), tt.want[i])
				}
				if s.End.Sub(s.Start) != tt.duration {
					t.Errorf("slot %d length = %s, want %s", i, s.End.Sub(s.Start), tt.duration)
				}
			}
		})
	}
}

func TestIntervalOverlapsAndContains(t *testing.T) {
	base := ts(t, "2026-01-12T10:00:00Z")
	iv := func(startMin, endMin int) Interval {
		return Interval{Start: base.Add(time.Duration(startMin) * time.Minute), End: base.Add(time.Duration(endMin) * time.Minute)}
	}
	tests := []struct {
		name               string
		a, b               Interval
		overlaps, contains bool
	}{
		{name: "identical", a: iv(0, 60), b: iv(0, 60), overlaps: true, contains: true},
		{name: "inside", a: iv(0, 60), b: iv(15, 45), overlaps: true, contains: true},
		{name: "partial", a: iv(0, 60), b: iv(30, 90), overlaps: true},
		{name: "touching", a: iv(0, 60), b: iv(60, 120)},
		{name: "disjoint", a: iv(0, 60), b: iv(90, 120)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.Overlaps(tt.b); got != tt.overlaps {
				t.Errorf("Overlaps = %v, want %v", got, tt.overlaps)
			}
			if got := tt.b.Overlaps(tt.a); got != tt.overlaps {
				t.Errorf("Overlaps not symmetric")
			}
			if got := tt.a.Contains(tt.b); got != tt.contains {
				t.Errorf("Contains = %v, want %v", got, tt.contains)
			}
		})
	}
}
