package assistant

import (
	"testing"
	"time"
)

func TestCronNextWalksTheSchedule(t *testing.T) {
	// A Friday morning, in the zone the schedule is written in.
	from := time.Date(2026, 9, 18, 10, 7, 30, 0, time.Local)
	for spec, want := range map[string]time.Time{
		"* * * * *":         time.Date(2026, 9, 18, 10, 8, 0, 0, time.Local),
		"*/30 9-17 * * 1-5": time.Date(2026, 9, 18, 10, 30, 0, 0, time.Local),
		"0 9 * * 1-5":       time.Date(2026, 9, 21, 9, 0, 0, 0, time.Local),
		"15 14 1 * *":       time.Date(2026, 10, 1, 14, 15, 0, 0, time.Local),
		"0 0 * * 0,6":       time.Date(2026, 9, 19, 0, 0, 0, 0, time.Local),
		"5,35 * * * *":      time.Date(2026, 9, 18, 10, 35, 0, 0, time.Local),
		"0 8 25 12 *":       time.Date(2026, 12, 25, 8, 0, 0, 0, time.Local),
	} {
		got, err := NextCron(spec, from)
		if err != nil {
			t.Fatalf("%s: %v", spec, err)
		}
		if !got.Equal(want) {
			t.Fatalf("%s: next after %s is %s, want %s", spec, from, got, want)
		}
	}
}

func TestCronRefusesWhatNoCrontabRuns(t *testing.T) {
	for _, spec := range []string{"", "* * * *", "60 * * * *", "* 24 * * *", "a * * * *", "*/0 * * * *", "5-1 * * * *", "0 0 31 2 *"} {
		if _, err := NextCron(spec, time.Now()); err == nil {
			t.Fatalf("want %q refused", spec)
		}
	}
}
