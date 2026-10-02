package cost

import (
	"encoding/json"
	"errors"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// replaySource is a source over records that stay where they are: it hands
// over only what lies past its cursor, the way a CLI's transcripts are read.
type replaySource struct{ entries *[]Entry }

func (r replaySource) Coder() string { return "claude" }

func (r replaySource) Running(string) bool { return false }

func (r replaySource) Collect(cursor json.RawMessage, _ string, _ time.Time) ([]Entry, json.RawMessage, error) {
	var read int
	json.Unmarshal(cursor, &read)
	all := *r.entries
	raw, _ := json.Marshal(len(all))
	return all[min(read, len(all)):], raw, nil
}

func files(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, "cost"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func mtime(t *testing.T, path string) time.Time {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.ModTime()
}

// bookMonths books one record in each of August, September and October,
// each on its own day, so each month has its own file.
func bookMonths(t *testing.T, s *Service, now *time.Time, records *[]Entry) {
	t.Helper()
	for _, r := range []Entry{
		{Session: "aug", At: time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC), Model: "opus", USD: 1},
		{Session: "sep", At: time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC), Model: "opus", USD: 2},
		{Session: "oct", At: time.Date(2026, 10, 3, 11, 0, 0, 0, time.UTC), Model: "opus", USD: 4},
	} {
		*now = r.At.Add(time.Hour)
		*records = append(*records, r)
		if err := s.Collect(); err != nil {
			t.Fatal(err)
		}
	}
}

func cursorsIn(t *testing.T, s *Service, month string) bool {
	t.Helper()
	var f monthFile
	if err := readFile(s.monthPath(month), &f); err != nil {
		t.Fatal(err)
	}
	return len(f.Cursors) > 0
}

// A row lives in the file of the month it was booked in, a late one of a
// closed month too: a booking writes the current month alone, and that file
// carries the cursors.
func TestTheFolderHoldsAFileOfRowsPerMonthAndOnlyTheBookedMonthIsWritten(t *testing.T) {
	var now time.Time
	var records []Entry
	dir := t.TempDir()
	s := newTestService(t, dir, owners{}, &now, replaySource{&records})
	bookMonths(t, s, &now, &records)
	want := []string{"rows-2026-08.json", "rows-2026-09.json", "rows-2026-10.json", "sessions.json"}
	if got := files(t, dir); !slices.Equal(got, want) {
		t.Fatalf("files %v, want %v", got, want)
	}
	if !cursorsIn(t, s, "2026-10") {
		t.Fatal("the newest month holds no cursors")
	}
	old := now.Add(-48 * time.Hour)
	for _, month := range []string{"2026-08", "2026-09", "2026-10"} {
		if err := os.Chtimes(s.monthPath(month), old, old); err != nil {
			t.Fatal(err)
		}
	}
	records = append(records,
		Entry{Session: "oct", At: now, Model: "opus", USD: 8},
		Entry{Session: "sep", At: time.Date(2026, 9, 30, 23, 0, 0, 0, time.UTC), Model: "opus", USD: 16})
	now = now.Add(10 * time.Second)
	if err := s.Collect(); err != nil {
		t.Fatal(err)
	}
	if !mtime(t, s.monthPath("2026-08")).Equal(old) || !mtime(t, s.monthPath("2026-09")).Equal(old) {
		t.Fatal("a closed month was rewritten")
	}
	if mtime(t, s.monthPath("2026-10")).Equal(old) {
		t.Fatal("the booked month was not written")
	}
	r := buildReport(s.rows(), now, Range90d)
	if !near(total(r), 31) || !near(r.Buckets[89-3].USD, 16) {
		t.Fatalf("total %v, the late call on its own day %v", total(r), r.Buckets[89-3].USD)
	}
}

func TestAMonthPastTheRetentionGoesAndIsNeverBookedAgain(t *testing.T) {
	var now time.Time
	var records []Entry
	dir := t.TempDir()
	who := owners{"aug": {Kind: KindCoder, Project: "a"}, "sep": {Kind: KindCoder, Project: "s"}, "oct": {Kind: KindCoder, Project: "o"}}
	s := newTestService(t, dir, who, &now, replaySource{&records})
	bookMonths(t, s, &now, &records)
	keep := 2
	s.SetRetention(func() int { return keep })
	now = now.Add(10 * time.Second)
	if err := s.Collect(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.monthPath("2026-08")); err != nil {
		t.Fatal("the retention took effect before the hourly upkeep")
	}
	now = now.Add(maintainEvery)
	if err := s.Collect(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.monthPath("2026-08")); !os.IsNotExist(err) {
		t.Fatalf("august is still there: %v", err)
	}

	// A restart reads the cursors that outlived the month: the records of
	// august are not read again.
	s = newTestService(t, dir, who, &now, replaySource{&records})
	s.SetRetention(func() int { return keep })
	now = now.Add(maintainEvery)
	if err := s.Collect(); err != nil {
		t.Fatal(err)
	}
	// A late record of a month past the retention is not booked either.
	records = append(records, Entry{Session: "late", At: time.Date(2026, 8, 30, 9, 0, 0, 0, time.UTC), Model: "opus", USD: 16})
	if err := s.Collect(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.monthPath("2026-08")); !os.IsNotExist(err) {
		t.Fatalf("august came back: %v", err)
	}
	if r := buildReport(s.rows(), now, Range90d); !near(total(r), 6) {
		t.Fatalf("total %v, want 6", total(r))
	}
}

func TestRetentionReadsTheSetting(t *testing.T) {
	for value, want := range map[string]int{"": 3, "x": 3, "1": 2, "2": 2, " 6 ": 6, "-4": 2} {
		if got := Retention(value); got != want {
			t.Errorf("Retention(%q) = %d, want %d", value, got, want)
		}
	}
}

func TestRowsBeforeTheLastThirtyDaysFoldIntoDaysOfTheZone(t *testing.T) {
	loc := time.FixedZone("CEST", 2*3600)
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, loc)
	day := time.Date(2026, 8, 20, 0, 0, 0, 0, loc)
	rows := []Row{
		{At: day.Add(30 * time.Minute).UTC(), Session: "a", Model: "opus", USD: 1, Tokens: Tokens{Input: 1}},
		{At: day.Add(23 * time.Hour).UTC(), Session: "a", Model: "opus", USD: 2, Tokens: Tokens{Input: 2}},
		{At: day.Add(23 * time.Hour).UTC(), Session: "b", Model: "opus", USD: 4},
		{At: now.Add(-29 * 24 * time.Hour).UTC(), Session: "a", Model: "opus", USD: 8},
	}
	out := compact(rows, now, loc)
	if len(out) != 3 || !out[0].At.Equal(day) || !near(out[0].USD, 3) || out[0].Tokens.Input != 3 || out[1].Session != "b" || !out[1].At.Equal(day) {
		t.Fatalf("folded %+v", out)
	}
	if want := now.Add(-29 * 24 * time.Hour).UTC().Truncate(time.Hour); !out[2].At.Equal(want) {
		t.Fatalf("a row of the last thirty days lost its hour: %v, want %v", out[2].At, want)
	}
}

func TestAnOwnerGoesWithItsRowsOnceNobodyNamedItWithinTheMonthsKept(t *testing.T) {
	cut := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	old, recent := cut.AddDate(0, 0, -1), cut.AddDate(0, 0, 1)
	d := directory{
		Sessions: map[string]owner{
			"claude/booked":    {Kind: KindCoder, Seen: old},
			"claude/forgotten": {Kind: KindCoder, Seen: old},
			"claude/announced": {Kind: KindCoder, Seen: recent},
			"claude/ops":       {Kind: KindAssistant, Assistant: "a1", Seen: old},
		},
		Assistants: map[string]assistantName{"a1": {Name: "Ops", Seen: old}, "a2": {Name: "Gone", Seen: old}, "a3": {Name: "New", Seen: recent}},
	}
	d.prune([]Row{{Coder: "claude", Session: "booked"}, {Coder: "claude", Session: "ops"}}, cut)
	if got := slices.Sorted(maps.Keys(d.Sessions)); !slices.Equal(got, []string{"claude/announced", "claude/booked", "claude/ops"}) {
		t.Fatalf("owners %v", got)
	}
	if got := slices.Sorted(maps.Keys(d.Assistants)); !slices.Equal(got, []string{"a1", "a3"}) {
		t.Fatalf("assistants %v", got)
	}
}

// The cursors live in the newest month file. A corrupt one is quarantined
// with its rows: the closed month before it still holds the cursors of its
// last write, the sources read on from there, and the lost month's spend is
// booked once again, not on top of anything.
func TestACorruptCurrentMonthFileDoesNotDoubleTheBookings(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	records := []Entry{{Session: "s", At: now.Add(-time.Hour), Model: "opus", USD: 1}}
	dir := t.TempDir()
	s := newTestService(t, dir, owners{}, &now, replaySource{&records})
	if err := s.Collect(); err != nil {
		t.Fatal(err)
	}
	now = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	records = append(records, Entry{Session: "s", At: now.Add(-time.Hour), Model: "opus", USD: 2})
	if err := s.Collect(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.monthPath("2026-10"), []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	now = now.Add(10 * time.Second)
	if err := s.Collect(); err != nil {
		t.Fatal(err)
	}
	if r := buildReport(s.rows(), now, Range30d); !near(total(r), 3) {
		t.Fatalf("total %v after a corrupt month file, want 3", total(r))
	}
}

// A months value so large that the month arithmetic overflows would cut
// every month away: the setting is held to MaxRetention, nothing goes.
func TestAHugeRetentionKeepsEverything(t *testing.T) {
	if got := Retention("9999999999999999"); got != MaxRetention {
		t.Fatalf("Retention = %d, want %d", got, MaxRetention)
	}
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	records := []Entry{{Session: "s", At: now.Add(-time.Hour), Model: "opus", USD: 5}}
	s := newTestService(t, t.TempDir(), owners{}, &now, replaySource{&records})
	s.SetRetention(func() int { return Retention("9999999999999999") })
	if err := s.Collect(); err != nil {
		t.Fatal(err)
	}
	now = now.Add(maintainEvery)
	if err := s.Collect(); err != nil {
		t.Fatal(err)
	}
	if r := buildReport(s.rows(), now, RangeToday); !near(total(r), 5) {
		t.Fatalf("total %v, want 5", total(r))
	}
}

// Two writes of the same size within one tick of the file system's clock
// look alike to a cache keyed on size and time: the service that wrote
// knows better, a read after a write shows what it wrote.
func TestAReadAfterAWriteInTheSameClockTickShowsTheWrite(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	records := []Entry{{Session: "s", At: now.Add(-time.Hour), Model: "opus", USD: 1}}
	s := newTestService(t, t.TempDir(), owners{}, &now, replaySource{&records})
	if err := s.Collect(); err != nil {
		t.Fatal(err)
	}
	path := s.monthPath("2026-10")
	first, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if r := buildReport(s.rows(), now, RangeToday); !near(total(r), 1) {
		t.Fatalf("total %v, want 1", total(r))
	}
	records = append(records, Entry{Session: "s", At: now.Add(-time.Hour), Model: "opus", USD: 1})
	now = now.Add(10 * time.Second)
	if err := s.Collect(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, first.ModTime(), first.ModTime()); err != nil {
		t.Fatal(err)
	}
	if second, _ := os.Stat(path); second.Size() != first.Size() {
		t.Fatalf("the two writes differ in size, %d and %d: the test needs them alike", first.Size(), second.Size())
	}
	if r := buildReport(s.rows(), now, RangeToday); !near(total(r), 2) {
		t.Fatalf("total %v after the second write, want 2", total(r))
	}
}

// A month file or sessions.json that exists but cannot be read is unknown,
// not empty: nothing is written over it until it reads again. A link to a
// directory is a file that is there and cannot be read, for root too.
func TestAnUnreadableFileIsNeitherReadAsEmptyNorOverwritten(t *testing.T) {
	for _, name := range []string{"rows-2026-10.json", sessionsFile} {
		t.Run(name, func(t *testing.T) {
			now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
			records := []Entry{{Session: "s", CWD: "/p/shop", At: now.Add(-time.Hour), Model: "opus", USD: 6}}
			dir := t.TempDir()
			s := newTestService(t, dir, owners{"s": {Kind: KindCoder, Project: "shop", Name: "fix"}}, &now, replaySource{&records})
			if err := s.Collect(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "cost", name)
			if err := os.Rename(path, path+".away"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(t.TempDir(), path); err != nil {
				t.Fatal(err)
			}
			records = append(records, Entry{Session: "s", At: now, Model: "opus", USD: 1})
			if err := s.Collect(); err == nil {
				t.Fatal("a collect over an unreadable file reported nothing")
			}
			if info, err := os.Lstat(path); err != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("the unreadable file was written over: %v", err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(path+".away", path); err != nil {
				t.Fatal(err)
			}
			now = now.Add(10 * time.Second)
			if err := s.Collect(); err != nil {
				t.Fatal(err)
			}
			if r := buildReport(s.rows(), now, RangeToday); !near(total(r), 7) {
				t.Fatalf("total %v, want 7", total(r))
			}
		})
	}
}

// A write that fails commits nothing: rows and cursors land together or not
// at all, and the next collect reads the same records again.
func TestAWriteThatKeepsFailingBooksNothingTwice(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	records := []Entry{{Session: "s", At: now.Add(-time.Hour), Model: "opus", USD: 2}}
	dir := t.TempDir()
	s := newTestService(t, dir, owners{}, &now, replaySource{&records})
	if err := s.Collect(); err != nil {
		t.Fatal(err)
	}
	block := s.monthPath("2026-10") + ".tmp"
	if err := os.Mkdir(block, 0o700); err != nil {
		t.Fatal(err)
	}
	records = append(records, Entry{Session: "s", At: now, Model: "opus", USD: 1})
	for i := range 5 {
		now = now.Add(10 * time.Second)
		if err := s.Collect(); err == nil {
			t.Fatalf("tick %d wrote through the blocked month file", i)
		}
	}
	if err := os.Remove(block); err != nil {
		t.Fatal(err)
	}
	now = now.Add(10 * time.Second)
	if err := s.Collect(); err != nil {
		t.Fatal(err)
	}
	if r := buildReport(s.rows(), now, RangeToday); !near(total(r), 3) {
		t.Fatalf("total %v after five failing ticks, want 3", total(r))
	}
}

// A failure the collector runs into on every tick is one line in the log
// until it changes or goes away.
func TestTheCollectorLogsAnErrorOnceUntilItChanges(t *testing.T) {
	var out strings.Builder
	log.SetOutput(&out)
	defer log.SetOutput(os.Stderr)
	var errs errorLog
	for range 10 {
		errs.report(errors.New("the folder cannot be read"))
	}
	errs.report(nil)
	errs.report(errors.New("the folder cannot be read"))
	errs.report(errors.New("the disk is full"))
	if got := strings.Count(out.String(), "cost: "); got != 3 {
		t.Fatalf("%d lines:\n%s", got, out.String())
	}
}
