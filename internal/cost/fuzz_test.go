package cost

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const (
	realMonth = `{
  "rows": [
    {"at": "2026-10-02T09:55:00Z", "coder": "claude", "session": "00000000-0000-4000-8000-000000000001", "model": "claude-opus-5-5", "usd": 1.2345, "tokens": {"input": 12, "output": 3400, "cache_read": 120000, "cache_write_1h": 4000}},
    {"at": "2026-09-20T00:00:00Z", "coder": "claude", "session": "00000000-0000-4000-8000-000000000002", "model": "claude-sonnet-5-5", "usd": 0, "tokens": {"input": 5}, "unpriced": true},
    {"at": "2026-10-01T10:00:00Z", "coder": "claude", "session": "00000000-0000-4000-8000-000000000001", "model": "claude-opus-5-5", "usd": 0.5, "top_up": true}
  ],
  "cursors": {"claude": {"files": {"/root/.claude/projects/-p-shop/s1.jsonl": {"size": 10, "offset": 10}}}}
}`
	realSessions = `{
  "sessions": {
    "claude/00000000-0000-4000-8000-000000000001": {"kind": "coder", "project": "shop", "name": "fix", "known": true, "seen": "2026-10-02T00:00:00Z"},
    "claude/00000000-0000-4000-8000-000000000003": {"kind": "assistant", "assistant": "a1", "known": true, "seen": "2026-10-01T00:00:00Z"}
  },
  "assistants": {"a1": {"name": "Helper", "seen": "2026-10-01T00:00:00Z"}}
}`
)

func checkReport(t *testing.T, r Report) {
	t.Helper()
	ok := func(v float64) bool { return v >= 0 && !math.IsInf(v, 0) }
	if !ok(r.Today) || !ok(r.Week) || !ok(r.Month) || !ok(r.TopUp) {
		t.Fatalf("report holds %g, %g, %g, %g", r.Today, r.Week, r.Month, r.TopUp)
	}
	for _, list := range [][]Share{r.Projects, r.Assistants} {
		for _, s := range list {
			if !ok(s.USD) {
				t.Fatalf("share %+v", s)
			}
		}
	}
	for _, b := range r.Buckets {
		if !ok(b.USD) {
			t.Fatalf("bucket %+v", b)
		}
	}
}

// quarantined says a file that does not parse went aside and nothing was
// read over it, and that one that parses stayed.
func quarantined(t *testing.T, path string, data []byte, v any) {
	t.Helper()
	_, err := os.Stat(path + ".broken")
	broken := !errors.Is(err, fs.ErrNotExist)
	if want := len(data) > 0 && json.Unmarshal(data, v) != nil; broken != want {
		t.Fatalf("%s quarantined %v, want %v", filepath.Base(path), broken, want)
	}
}

// FuzzCostFolder reads any bytes as the current and an older month file and
// as sessions.json, then books, notes owners and answers queries. Nothing
// panics, garbage goes aside and every amount the pages read is usable.
func FuzzCostFolder(f *testing.F) {
	log.SetOutput(io.Discard)
	f.Cleanup(func() { log.SetOutput(os.Stderr) })
	f.Add([]byte(realMonth), []byte(realMonth), []byte(realSessions))
	f.Add([]byte(`{"rows":[{"at":"2026-10-02T09:55:00Z","coder":"claude","session":"s","usd":1e308},{"at":"0001-01-01T00:00:00Z","coder":"claude","session":"s","usd":-1}]}`),
		[]byte(`{"rows":[{"at":"9999-12-31T23:59:59Z","coder":"claude","session":"s","usd":9999999,"tokens":{"input":9223372036854775807}}]}`),
		[]byte(`{"sessions":{"claude/s":null},"assistants":null}`))
	f.Add([]byte(`{"rows":[{"at":"2026-10-02T09:55:00Z","usd":"1"}],"cursors":5}`), []byte(`null`), []byte(`{"sessions":[]}`))
	f.Add([]byte("\x00"), []byte(`{"rows":null}`), []byte(`{"sessions":{"claude/s":{"kind":"assistant","assistant":"","seen":"x"}}}`))
	f.Add([]byte(``), []byte(`[`), []byte(`{}`))
	f.Fuzz(func(t *testing.T, current, older, sessions []byte) {
		dir := t.TempDir()
		folder := filepath.Join(dir, "cost")
		if err := os.MkdirAll(folder, 0o700); err != nil {
			t.Fatal(err)
		}
		files := map[string][]byte{"rows-2026-10.json": current, "rows-2026-09.json": older, sessionsFile: sessions}
		for name, data := range files {
			if err := os.WriteFile(filepath.Join(folder, name), data, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
		src := &fakeSource{entries: []Entry{{Session: "s1", CWD: "/p/shop", At: now.Add(-time.Hour), Model: "claude-opus-5-5", USD: 1, Tokens: Tokens{Input: 10}}}}
		s := New(dir, testPlace, src)
		s.now = func() time.Time { return now }
		s.zone = func() *time.Location { return time.UTC }

		checkReport(t, s.Query(time.UTC, Span{From: AddDays(now, -29), To: AddDays(now, 1), Step: StepDay}))
		if err := s.Collect(); err != nil {
			t.Fatalf("collect: %v", err)
		}
		quarantined(t, filepath.Join(folder, "rows-2026-10.json"), current, &monthFile{})
		quarantined(t, filepath.Join(folder, "rows-2026-09.json"), older, &monthFile{})
		quarantined(t, filepath.Join(folder, sessionsFile), sessions, &directory{})
		if err := s.NoteOwners(Owners{Coders: []CoderSession{{Coder: "claude", Session: "s1", Name: "fix", CWD: "/p/shop"}}, Assistants: map[string]string{"a1": "Helper"}}); err != nil {
			t.Fatalf("note owners: %v", err)
		}
		for _, span := range []Span{
			{From: DayStart(now), To: AddDays(now, 1), Step: StepHour, By: BySession},
			{From: AddDays(now, -89), To: AddDays(now, 1), Step: StepWeek, By: ByModel, Filter: Filter{Kind: KindCoder}},
			{From: AddMonths(now, -12), To: AddMonths(now, 1), Step: StepMonth, Filter: Filter{Assistants: true}},
			{From: DayStart(now)},
		} {
			checkReport(t, s.Query(time.UTC, span))
		}
		for _, r := range s.rows() {
			if !r.usable() {
				t.Fatalf("read an unusable row %+v", r)
			}
		}
		s.ModelRates()
		if err := s.Collect(); err != nil {
			t.Fatalf("idle collect: %v", err)
		}
	})
}
