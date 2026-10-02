package cost

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// replaySource is a source over records that stay where they are: it hands
// over only what lies past its cursor, the way a CLI's transcripts are read.
type replaySource struct{ entries *[]Entry }

func (r replaySource) Coder() string { return "claude" }

func (r replaySource) Capture(string) any { return nil }

func (r replaySource) Collect(cursor json.RawMessage, _ any, _ time.Time) ([]Entry, json.RawMessage, error) {
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

func TestTheFolderHoldsAFileOfRowsPerMonthAndOnlyTheBookedMonthIsWritten(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	records := []Entry{
		{Session: "aug", At: time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC), Model: "opus", USD: 1},
		{Session: "sep", At: time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC), Model: "opus", USD: 2},
		{Session: "oct", At: now.Add(-time.Hour), Model: "opus", USD: 4},
	}
	dir := t.TempDir()
	s := newTestService(t, dir, owners{}, &now, replaySource{&records})
	if err := s.Collect(); err != nil {
		t.Fatal(err)
	}
	want := []string{"cursors.json", "rows-2026-08.json", "rows-2026-09.json", "rows-2026-10.json", "sessions.json"}
	if got := files(t, dir); !slices.Equal(got, want) {
		t.Fatalf("files %v, want %v", got, want)
	}
	old := now.Add(-48 * time.Hour)
	for _, month := range []string{"2026-08", "2026-09", "2026-10"} {
		if err := os.Chtimes(s.monthPath(month), old, old); err != nil {
			t.Fatal(err)
		}
	}
	records = append(records, Entry{Session: "oct", At: now, Model: "opus", USD: 8})
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
	if r := buildReport(s.read(), now, Range90d); !near(r.Total, 15) {
		t.Fatalf("total %v, want 15", r.Total)
	}
}

func TestAMonthPastTheRetentionGoesAndIsNeverBookedAgain(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	records := []Entry{
		{Session: "aug", At: time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC), Model: "opus", USD: 1},
		{Session: "sep", At: time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC), Model: "opus", USD: 2},
		{Session: "oct", At: now.Add(-time.Hour), Model: "opus", USD: 4},
	}
	dir := t.TempDir()
	who := owners{"aug": {Kind: KindCoder, Project: "a"}, "sep": {Kind: KindCoder, Project: "s"}, "oct": {Kind: KindCoder, Project: "o"}}
	s := newTestService(t, dir, who, &now, replaySource{&records})
	if err := s.Collect(); err != nil {
		t.Fatal(err)
	}
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
	if r := buildReport(s.read(), now, Range90d); !near(r.Total, 6) {
		t.Fatalf("total %v, want 6", r.Total)
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
