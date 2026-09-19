package assistant

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
	"time"
)

// berlin is the zone the daylight saving cases are written in: it moves its
// clock, which is the whole point, and both changeovers fall on a Sunday at an
// hour a schedule can be written for.
func berlin(t *testing.T) *time.Location {
	t.Helper()
	loc, err := LoadZone("Europe/Berlin")
	if err != nil {
		t.Fatalf("Europe/Berlin: %v", err)
	}
	return loc
}

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
		got, err := NextCron(spec, from, time.Local)
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
		if _, err := NextCron(spec, time.Now(), time.Local); err == nil {
			t.Fatalf("want %q refused", spec)
		}
	}
}

// TestCronReadsTheScheduleInItsOwnZone is what the whole zone carries: nine
// o'clock is a wall clock somewhere, and which somewhere decides the moment.
// The same five fields are asked in two zones and answer two different
// moments, neither of them the server's, so nothing here can be passing by
// accident on a host that happens to run in one of them.
func TestCronReadsTheScheduleInItsOwnZone(t *testing.T) {
	from := time.Date(2026, 9, 21, 3, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		zone string
		want time.Time
	}{
		// 09:00 in Berlin is 07:00 UTC that day, in New York 13:00 UTC.
		{"Europe/Berlin", time.Date(2026, 9, 21, 7, 0, 0, 0, time.UTC)},
		{"America/New_York", time.Date(2026, 9, 21, 13, 0, 0, 0, time.UTC)},
		{"UTC", time.Date(2026, 9, 21, 9, 0, 0, 0, time.UTC)},
	} {
		loc, err := LoadZone(c.zone)
		if err != nil {
			t.Fatalf("%s: %v", c.zone, err)
		}
		got, err := NextCron("0 9 * * *", from, loc)
		if err != nil {
			t.Fatalf("%s: %v", c.zone, err)
		}
		if !got.Equal(c.want) {
			t.Fatalf("0 9 * * * in %s after %s is %s, want %s", c.zone, from, got.UTC(), c.want)
		}
	}
}

// TestCronSkipsTheSpringGap pins the first of the two daylight saving cases the
// minute walk hands over and nothing here works around: on the night Berlin
// moves 02:00 to 03:00, 02:30 does not exist, no minute matches, and a daily
// half past two falls out for that one day. The tick after the one before the
// changeover is the day after it, never 03:30 and never a repeat.
func TestCronSkipsTheSpringGap(t *testing.T) {
	loc := berlin(t)
	// 2026-03-29 is the Sunday the clock jumps from 02:00 to 03:00.
	before := time.Date(2026, 3, 28, 2, 30, 0, 0, loc)
	got, err := NextCron("30 2 * * *", before, loc)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 3, 30, 2, 30, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("after %s the next 02:30 is %s, want %s: the gap day has no 02:30 and is skipped", before, got, want)
	}
}

// TestCronFiresTwiceInTheAutumnHour pins the other one: on the night Berlin
// moves 03:00 back to 02:00, 02:30 happens twice, matches twice, and the
// schedule fires twice, an hour apart. Both ticks are real moments and the
// second is found by asking again from the first, which is exactly what the
// reactor does after a tick.
func TestCronFiresTwiceInTheAutumnHour(t *testing.T) {
	loc := berlin(t)
	// 2026-10-25 is the Sunday the clock falls back from 03:00 to 02:00.
	from := time.Date(2026, 10, 25, 0, 0, 0, 0, loc)
	first, err := NextCron("30 2 * * *", from, loc)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NextCron("30 2 * * *", first, loc)
	if err != nil {
		t.Fatal(err)
	}
	if got := second.Sub(first); got != time.Hour {
		t.Fatalf("the two 02:30 of the changeover night are %s apart, want 1h (%s then %s)", got, first, second)
	}
	if h, m := second.Hour(), second.Minute(); h != 2 || m != 30 {
		t.Fatalf("the second tick is %02d:%02d, want 02:30 on the same day", h, m)
	}
	if first.Day() != 25 || second.Day() != 25 {
		t.Fatalf("both ticks belong to the 25th, got %s and %s", first, second)
	}
}

func TestLoadZoneRefusesWhatIsNoZone(t *testing.T) {
	// An offset carries no changeover rules, "Local" is whichever host reads
	// it, and a name nothing knows is a typo. Each refusal names the field,
	// the way ParseCron names the five cron fields.
	for _, name := range []string{"", "   ", "+02:00", "CEST", "Local", "local", "Mars/Olympus", "Europe/Berlin/extra"} {
		loc, err := LoadZone(name)
		if err == nil {
			t.Fatalf("want %q refused, got %v", name, loc)
		}
		if !strings.Contains(err.Error(), "zone field") {
			t.Fatalf("the refusal of %q has to name the field, got %q", name, err)
		}
	}
}

// TestZoneDataTravelsInTheBinary pins the blank import of time/tzdata. What it
// buys cannot be observed from a test on this host, which has a zone database
// of its own and would load every name with or without it; what it prevents is
// a slim image where /usr/share/zoneinfo is absent and every name but UTC is
// refused. So the import is pinned where it can actually be deleted by
// accident, and the zones are loaded beside it to show the names are real.
func TestZoneDataTravelsInTheBinary(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "cron.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	embedded := false
	for _, imported := range file.Imports {
		if imported.Path.Value == `"time/tzdata"` && imported.Name != nil && imported.Name.Name == "_" {
			embedded = true
		}
	}
	if !embedded {
		t.Fatal("cron.go has to carry the blank import of time/tzdata: without it a host with no zone database refuses every schedule but UTC")
	}
	for _, name := range []string{"Europe/Berlin", "America/New_York", "Asia/Tokyo", "UTC"} {
		if _, err := LoadZone(name); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestServerZoneNamesAZoneThatLoads(t *testing.T) {
	// Whatever this host answers, it has to be a name the cockpit can store
	// and read back: a schedule falls back to it, and a name nothing loads
	// would refuse every schedule made without a zone of its own.
	t.Setenv("TZ", "Asia/Tokyo")
	if got := ServerZone(); got != "Asia/Tokyo" {
		t.Fatalf("with TZ set the server zone is %q, want Asia/Tokyo", got)
	}
	t.Setenv("TZ", "Mars/Olympus")
	name := ServerZone()
	if _, err := LoadZone(name); err != nil {
		t.Fatalf("a TZ nothing knows must fall through to a name that loads, got %q: %v", name, err)
	}
}
