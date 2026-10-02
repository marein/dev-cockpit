package cost

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeSource hands over what the test set and keeps a cursor that moves
// whenever it handed something over.
type fakeSource struct {
	coder   string
	entries []Entry
	reads   int
}

func (f *fakeSource) Coder() string {
	if f.coder == "" {
		return "claude"
	}
	return f.coder
}

func (f *fakeSource) Running(string) bool { return false }

func (f *fakeSource) Collect(json.RawMessage, string, time.Time) ([]Entry, json.RawMessage, error) {
	out := f.entries
	f.entries = nil
	if len(out) > 0 {
		f.reads++
	}
	raw, _ := json.Marshal(map[string]int{"reads": f.reads})
	return out, raw, nil
}

// owners are what the cockpit's events say about sessions before the test
// books: a coder by its name and project, an assistant by its name, which is
// its id too.
type owners map[string]Attribution

// testPlace reads /ws/<id> as an assistant's workspace and anything else as
// the project named like its last element.
func testPlace(cwd string) (string, string) {
	if id, ok := strings.CutPrefix(cwd, "/ws/"); ok {
		return id, ""
	}
	return "", filepath.Base(cwd)
}

func newTestService(t *testing.T, dir string, who owners, now *time.Time, sources ...Source) *Service {
	t.Helper()
	s := New(dir, testPlace, sources...)
	s.now = func() time.Time { return *now }
	s.zone = func() *time.Location { return time.UTC }
	o := Owners{Assistants: map[string]string{}}
	for session, a := range who {
		if a.Kind == KindAssistant {
			o.Coders = append(o.Coders, CoderSession{Coder: "claude", Session: session, CWD: "/ws/" + a.Name})
			o.Assistants[a.Name] = a.Name
			continue
		}
		o.Coders = append(o.Coders, CoderSession{Coder: "claude", Session: session, Name: a.Name, CWD: "/p/" + a.Project})
	}
	note(t, s.NoteOwners(o))
	return s
}

func note(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestEntriesAreBookedOnTheirOwnTimeWithTheirTokens(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	earlier := now.Add(-26 * time.Hour)
	src := &fakeSource{entries: []Entry{
		{Session: "s1", At: earlier.Add(time.Minute), Model: "opus", USD: 1, Tokens: Tokens{Input: 10, Output: 20}},
		{Session: "s1", At: earlier.Add(2 * time.Minute), Model: "opus", USD: 2, Tokens: Tokens{Input: 5, CacheWrite1h: 7}},
		{Session: "s1", At: now.Add(time.Hour), Model: "opus", USD: 4},
	}}
	s := newTestService(t, t.TempDir(), owners{"s1": {Kind: KindCoder, Project: "shop", Name: "fix"}}, &now, src)
	if err := s.Collect(); err != nil {
		t.Fatal(err)
	}
	rows := s.rows()
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	first := rows[0]
	if !first.At.Equal(earlier.Truncate(fineBucket)) || !near(first.USD, 3) ||
		first.Tokens != (Tokens{Input: 15, Output: 20, CacheWrite1h: 7}) || first.Project != "shop" {
		t.Fatalf("the earlier calls = %+v", first)
	}
	if !rows[1].At.Equal(now.Truncate(fineBucket)) {
		t.Fatalf("a call from the future is not booked now: %+v", rows[1])
	}
	r := buildReport(s.rows(), now, Range30d)
	if !near(total(r), 7) || !near(r.Buckets[28].USD, 3) || !near(r.Buckets[29].USD, 4) {
		t.Fatalf("report total %v days %v %v", total(r), r.Buckets[28].USD, r.Buckets[29].USD)
	}
}

func TestUnpricedAndTopUpAreCountedApart(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	src := &fakeSource{entries: []Entry{
		{Session: "s1", At: now, Model: "nemotron", Tokens: Tokens{Input: 100}, Unpriced: true},
		{Session: "s1", At: now, Model: "nemotron", Tokens: Tokens{Input: 50}, Unpriced: true},
		{Session: "s2", At: now, Model: "opus", USD: 2},
		{Session: "s2", At: now, Model: "opus", USD: 0.5, TopUp: true},
		{Session: "s3", At: now, Model: "opus"},
	}}
	s := newTestService(t, t.TempDir(), owners{}, &now, src)
	if err := s.Collect(); err != nil {
		t.Fatal(err)
	}
	r := buildReport(s.rows(), now, RangeToday)
	if r.Unpriced != 1 || !near(r.TopUp, 0.5) || !near(total(r), 2.5) {
		t.Fatalf("unpriced %d top-up %v total %v", r.Unpriced, r.TopUp, total(r))
	}
	for _, row := range s.rows() {
		if row.Session == "s3" {
			t.Fatalf("an entry without spend made a row: %+v", row)
		}
	}
	if len(s.rows()) != 3 {
		t.Fatalf("rows = %+v", s.rows())
	}
}

// One ledger for every CLI: the rows carry the coder they came from.
func TestEverySourceBooksUnderItsOwnCoder(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	a := &fakeSource{entries: []Entry{{Session: "x", At: now, Model: "m", USD: 1}}}
	b := &fakeSource{coder: "opencode", entries: []Entry{{Session: "x", At: now, Model: "m", USD: 2}}}
	s := newTestService(t, t.TempDir(), owners{}, &now, a, b)
	if err := s.Collect(); err != nil {
		t.Fatal(err)
	}
	l, err := s.load(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Rows) != 2 || l.Rows[0].Coder != "claude" || l.Rows[1].Coder != "opencode" || len(l.Cursors) != 2 {
		t.Fatalf("ledger = %+v", l)
	}
}

func TestAGuessIsImprovedAnEventNamesTheOwnerAndARenameReachesEveryRow(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	src := &fakeSource{entries: []Entry{{Session: "s1", At: now, Model: "opus", USD: 1}}}
	s := newTestService(t, t.TempDir(), owners{}, &now, src)
	_ = s.Collect()
	if r := s.rows()[0]; r.Kind != KindOther || r.Project != "" {
		t.Fatalf("first guess = %+v", r)
	}

	// A guess with a project replaces one without, the booked rows follow.
	now = now.Add(time.Minute)
	src.entries = []Entry{{Session: "s1", CWD: "/p/shop", At: now, Model: "opus", USD: 1}}
	_ = s.Collect()
	for _, r := range s.rows() {
		if r.Project != "shop" {
			t.Fatalf("the better guess did not reach every row: %+v", r)
		}
	}
	// A guess that says less changes nothing.
	src.entries = []Entry{{Session: "s1", At: now, Model: "opus", USD: 1}}
	_ = s.Collect()
	for _, r := range s.rows() {
		if r.Project != "shop" {
			t.Fatalf("a weaker guess replaced a better one: %+v", r)
		}
	}

	// The cockpit says it runs the session: it is a coder from then on, a
	// later directory guesses nothing, a rename reaches every row.
	note(t, s.NoteOwners(Owners{Coders: []CoderSession{{Coder: "claude", Session: "s1", Name: "checkout", CWD: "/p/shop"}}}))
	now = now.Add(time.Minute)
	src.entries = []Entry{{Session: "s1", CWD: "/p/other", At: now, Model: "opus", USD: 1}}
	_ = s.Collect()
	note(t, s.NoteOwners(Owners{Coders: []CoderSession{{Coder: "claude", Session: "s1", Name: "pay", CWD: "/p/shop"}}}))
	for _, r := range s.rows() {
		if r.Kind != KindCoder || r.Name != "pay" || r.Project != "shop" {
			t.Fatalf("a row lost its owner or its new name: %+v", r)
		}
	}
	rep := buildReport(s.rows(), now, Range30d)
	if len(rep.Projects) != 1 || !near(rep.Projects[0].USD, 4) {
		t.Fatalf("projects = %+v", rep.Projects)
	}
}

func TestAnAssistantIsOneOwnerUnderItsIdAndKeepsItsNameWhenGone(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	src := &fakeSource{entries: []Entry{
		{Session: "turn", CWD: "/ws/a1", At: now, Model: "opus", USD: 1},
		{Session: "check", CWD: "/ws/a1", At: now, Model: "opus", USD: 2},
		{Session: "other", CWD: "/ws/a2", At: now, Model: "opus", USD: 4},
	}}
	s := newTestService(t, t.TempDir(), owners{}, &now, src)
	note(t, s.NoteOwners(Owners{Assistants: map[string]string{"a1": "Ops", "a2": "Ops"}}))
	_ = s.Collect()
	r := buildReport(s.rows(), now, RangeToday)
	if len(r.Assistants) != 2 || r.Assistants[0].Assistant != "a2" || r.Assistants[1].Assistant != "a1" || !near(r.Assistants[1].USD, 3) || r.Assistants[1].Name != "Ops" {
		t.Fatalf("two assistants of one name are one line each: %+v", r.Assistants)
	}
	note(t, s.NoteOwners(Owners{Assistants: map[string]string{"a1": "Writer"}}))
	r = buildReport(s.rows(), now, RangeToday)
	if r.Assistants[1].Name != "Writer" {
		t.Fatalf("the rename did not reach the bookings: %+v", r.Assistants)
	}
}

func TestCompactFoldsOldBucketsIntoHoursWithTheirTokens(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	old := now.Add(-72 * time.Hour)
	rows := []Row{
		{At: old.Truncate(time.Hour).Add(5 * time.Minute), Session: "a", USD: 1, Tokens: Tokens{Input: 1}},
		{At: old.Truncate(time.Hour).Add(25 * time.Minute), Session: "a", USD: 2, Tokens: Tokens{Input: 2}},
		{At: old.Truncate(time.Hour).Add(25 * time.Minute), Session: "a", USD: 8, TopUp: true},
		{At: now.Add(-10 * time.Minute).Truncate(fineBucket), Session: "a", USD: 4},
	}
	out := compact(rows, now, time.UTC)
	if len(out) != 3 || !near(out[0].USD, 3) || out[0].Tokens.Input != 3 || !out[0].At.Equal(old.Truncate(time.Hour)) || !out[1].TopUp {
		t.Fatalf("compacted = %+v", out)
	}
}

func TestReportPeriods(t *testing.T) {
	loc := time.FixedZone("CEST", 2*3600)
	now := time.Date(2026, 10, 2, 14, 30, 0, 0, loc)
	l := ledger{Rows: []Row{
		{At: now.Add(-20 * time.Minute).UTC(), Attribution: Attribution{Kind: KindCoder, Project: "a"}, USD: 1},
		{At: now.Add(-3 * time.Hour).UTC(), Attribution: Attribution{Kind: KindOther, Project: "b"}, USD: 2},
		{At: time.Date(2026, 9, 30, 10, 0, 0, 0, loc).UTC(), Attribution: Attribution{Kind: KindAssistant}, USD: 4},
		{At: time.Date(2026, 9, 20, 10, 0, 0, 0, loc).UTC(), Attribution: Attribution{Kind: KindOther}, USD: 8},
	}}
	r := buildReport(l.Rows, now, RangeToday)
	if !near(r.Today, 3) || !near(r.Week, 7) || !near(r.Month, 3) || !near(total(r), 3) {
		t.Fatalf("today %v week %v month %v total %v", r.Today, r.Week, r.Month, total(r))
	}
	r = buildReport(l.Rows, now, Range30d)
	if !near(r.Buckets[29].USD, 3) || !near(r.Buckets[27].USD, 4) || !near(r.Buckets[17].USD, 8) {
		t.Fatalf("days = %v %v %v", r.Buckets[29].USD, r.Buckets[27].USD, r.Buckets[17].USD)
	}
}

func TestReportGroupsTheAssistantsApartFromNoProject(t *testing.T) {
	now := time.Date(2026, 10, 2, 14, 30, 0, 0, time.UTC)
	at := now.Add(-time.Hour)
	l := ledger{Rows: []Row{
		{At: at, Attribution: Attribution{Kind: KindCoder, Project: "shop"}, USD: 1},
		{At: at, Attribution: Attribution{Kind: KindAssistant, Assistant: "ops", Name: "Ops"}, USD: 2},
		{At: at, Coder: "claude", Session: "check", Attribution: Attribution{Kind: KindAssistant, Assistant: "ops", Name: "Ops"}, USD: 4},
		{At: at, Attribution: Attribution{Kind: KindAssistant, Assistant: "writer", Name: "Writer"}, USD: 8},
		{At: at, Coder: "claude", Session: "trigger", Attribution: Attribution{Kind: KindAssistant, Assistant: "ops", Name: "Ops"}, USD: 16},
		{At: at, Attribution: Attribution{Kind: KindOther}, USD: 32},
	}}
	r := buildReport(l.Rows, now, RangeToday)
	if len(r.Projects) != 3 || r.Projects[0].Assistants || r.Projects[0].Project != "" || !near(r.Projects[0].USD, 32) ||
		!r.Projects[1].Assistants || !near(r.Projects[1].USD, 30) || r.Projects[2].Project != "shop" {
		t.Fatalf("projects = %+v", r.Projects)
	}
	if len(r.Assistants) != 2 || r.Assistants[0].Name != "Ops" || !near(r.Assistants[0].USD, 22) || r.Assistants[1].Name != "Writer" {
		t.Fatalf("assistants = %+v", r.Assistants)
	}
}

func TestAnIdleCollectWritesNothingAndAnnouncesNothing(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	src := &fakeSource{entries: []Entry{{Session: "s1", At: now, Model: "opus", USD: 1}}}
	s := newTestService(t, t.TempDir(), owners{}, &now, src)
	announced := 0
	s.SetOnChange(func() { announced++ })
	if err := s.Collect(); err != nil {
		t.Fatal(err)
	}
	if announced != 1 {
		t.Fatalf("the first collect announced %d times, want 1", announced)
	}
	path := s.monthPath("2026-10")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	old := before.ModTime().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		now = now.Add(10 * time.Second)
		if err := s.Collect(); err != nil {
			t.Fatal(err)
		}
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(old) || announced != 1 {
		t.Fatalf("an idle collect rewrote the ledger at %v, announced %d times", after.ModTime(), announced)
	}
}

// Santiago's clocks jump from 00:00 to 01:00 on 2026-09-06, the day has no
// midnight. Its day starts with the jump, today stays the last of the thirty
// days, also 29 days later, when the day of the jump is the first one.
func TestDaysHoldWhereTheClockSkipsMidnight(t *testing.T) {
	loc, err := time.LoadLocation("America/Santiago")
	if err != nil {
		t.Skipf("no zone data: %v", err)
	}
	start := DayStart(time.Date(2026, 9, 6, 12, 0, 0, 0, loc))
	if start.Format("2006-01-02 15:04") != "2026-09-06 01:00" {
		t.Fatalf("the day of the jump starts at %v", start)
	}
	for _, now := range []time.Time{time.Date(2026, 9, 6, 12, 0, 0, 0, loc), time.Date(2026, 10, 5, 12, 0, 0, 0, loc)} {
		l := ledger{Rows: []Row{
			{At: now.Add(-time.Hour).UTC(), USD: 1},
			{At: start.UTC(), USD: 2},
		}}
		r := buildReport(l.Rows, now, Range30d)
		if got := r.Buckets[29].Start.Format("2006-01-02"); got != now.Format("2006-01-02") {
			t.Fatalf("now %v: the last day is %s", now, got)
		}
		if got := r.Buckets[0].Start.Format("2006-01-02"); got != now.AddDate(0, 0, -29).Format("2006-01-02") {
			t.Fatalf("now %v: the first day is %s", now, got)
		}
		for _, d := range r.Buckets {
			if !d.Start.Equal(DayStart(d.Start)) {
				t.Fatalf("now %v: day %v does not start at its first moment", now, d.Start)
			}
		}
		if !near(r.Buckets[0].USD+r.Buckets[29].USD, 3) || !near(r.Today, 1+map[bool]float64{true: 2}[now.Day() == 6]) {
			t.Fatalf("now %v: first %v last %v today %v", now, r.Buckets[0].USD, r.Buckets[29].USD, r.Today)
		}
	}
	if got := WeekStart(time.Date(2026, 9, 9, 12, 0, 0, 0, loc)).Format("2006-01-02 15:04"); got != "2026-09-07 00:00" {
		t.Fatalf("week start %s", got)
	}
	if got, _ := RangeSpan(Range30d, time.Date(2026, 10, 5, 12, 0, 0, 0, loc)); !got.Equal(start) {
		t.Fatalf("thirty days start at %v", got)
	}
}

func TestALedgerRowNoBookingCanProduceIsDroppedOnLoad(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	s := newTestService(t, dir, owners{}, &now)
	at := now.Add(-time.Hour).Format(time.RFC3339)
	raw := `{"rows":[
		{"at":"` + at + `","coder":"claude","session":"ok","kind":"coder","usd":1.5},
		{"at":"` + at + `","coder":"claude","session":"huge","kind":"coder","usd":1.6e308},
		{"at":"` + at + `","coder":"claude","session":"refund","kind":"coder","usd":-2},
		{"at":"` + at + `","coder":"claude","session":"tokens","kind":"coder","usd":1,"tokens":{"output":-5}}
	]}`
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.monthPath("2026-10"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	var sessions []string
	for _, r := range s.rows() {
		sessions = append(sessions, r.Session)
	}
	if strings.Join(sessions, ",") != "ok" {
		t.Fatalf("rows kept %v, want ok", sessions)
	}
}

// A delete waits for a collect that runs at most deleteWait, then goes on
// without booking; once nothing runs it books at once.
func TestADeleteDoesNotWaitLongForARunningCollect(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	s := newTestService(t, t.TempDir(), owners{}, &now, &fakeSource{})
	s.books <- struct{}{}
	started := time.Now()
	if err := s.BookSession("claude", "s1"); err == nil {
		t.Fatal("a delete booked while a collect held the books")
	}
	if took := time.Since(started); took < deleteWait || took > deleteWait+time.Second {
		t.Fatalf("the delete waited %v, want %v", took, deleteWait)
	}
	<-s.books
	if err := s.BookSession("claude", "s1"); err != nil {
		t.Fatal(err)
	}
}
