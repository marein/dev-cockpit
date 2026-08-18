package statusline

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// payload is what claude writes to the renderer's stdin, cut down to the
// fields the values here read.
const payload = `{
  "model": {"display_name": "Opus 5 (1M context)"},
  "workspace": {"current_dir": "/root/projects/dev-cockpit"},
  "cwd": "/root/projects/dev-cockpit",
  "cost": {"total_cost_usd": 1.2449},
  "context_window": {"used_percentage": 42.5},
  "rate_limits": {"five_hour": {"used_percentage": 16.0}}
}`

// testEnv is a redraw with a home of its own, so the usage call finds no
// login and asks nothing, and a cache of its own.
func testEnv(t *testing.T) Env {
	t.Helper()
	return Env{Now: time.Now(), Home: t.TempDir(), CacheDir: t.TempDir(), UsageURL: "http://127.0.0.1:1/usage"}
}

func draw(t *testing.T, entries []Entry, stdin string) string {
	t.Helper()
	return Render(context.Background(), entries, []byte(stdin), testEnv(t))
}

func TestRenderTheDefaultLine(t *testing.T) {
	out := draw(t, Defaults(), payload)
	// The weekly values are not in this payload and the usage API answers
	// nothing here, so those entries and the separators leading to them fall
	// away and the line neither ends in a separator nor carries two in a row.
	want := "\x1b[36mOpus 5\x1b[0m \x1b[2m·\x1b[0m \x1b[2mc\x1b[0m \x1b[32m42.5%\x1b[0m \x1b[2m·\x1b[0m \x1b[2m5\x1b[0m \x1b[32m16%\x1b[0m\n"
	if out != want {
		t.Fatalf("the default line is\n%q\nwant\n%q", out, want)
	}
}

func TestRenderColorsANumberByItsBounds(t *testing.T) {
	entries := Normalize([]Entry{{Kind: KindValue, Value: "context", Thresholds: []Threshold{
		{At: 0, Color: "green"}, {At: 50, Color: "yellow"}, {At: 80, Color: "red"},
	}}})
	cases := map[string]string{
		"12":    "\x1b[32m12%\x1b[0m\n",
		"50":    "\x1b[33m50%\x1b[0m\n",
		"49.99": "\x1b[32m49.99%\x1b[0m\n",
		"80.01": "\x1b[31m80.01%\x1b[0m\n",
	}
	for used, want := range cases {
		if out := draw(t, entries, `{"context_window": {"used_percentage": `+used+`}}`); out != want {
			t.Errorf("%s%% renders %q, want %q", used, out, want)
		}
	}
}

func TestRenderDropsAnEntryNobodyAnswered(t *testing.T) {
	entries := Normalize([]Entry{
		{Kind: KindValue, Value: "model", Color: "cyan"},
		{Kind: KindSeparator, Text: "·"},
		{Kind: KindValue, Value: "cost", Thresholds: []Threshold{{At: 0, Color: "green"}}},
	})
	if out := draw(t, entries, `{"model": {"display_name": "Opus 5"}}`); out != "\x1b[36mOpus 5\x1b[0m\n" {
		t.Fatalf("a line whose second value is missing is %q", out)
	}
}

func TestRenderWritesOneLinePerBreak(t *testing.T) {
	entries := Normalize([]Entry{
		{Kind: KindValue, Value: "model", Color: "default"},
		{Kind: KindBreak},
		{Kind: KindValue, Value: "dir", Color: "default"},
	})
	if out := draw(t, entries, `{"model": {"display_name": "Opus 5"}, "cwd": "/root/projects/dev-cockpit"}`); out != "Opus 5\ndev-cockpit\n" {
		t.Fatalf("the two lines are %q", out)
	}
}

// A payload that is no JSON, or one shaped otherwise than claude writes it,
// takes the values it should have carried and nothing else.
func TestRenderKeepsWhatNeedsNoPayload(t *testing.T) {
	entries := Normalize([]Entry{
		{Kind: KindValue, Value: "model", Color: "cyan"},
		{Kind: KindSeparator, Text: "·"},
		{Kind: KindValue, Value: FreeTextValue, Text: "eax", Color: "default"},
	})
	for _, stdin := range []string{"", "not json", `{"model": "Opus 5"}`, `[1, 2]`} {
		if out := draw(t, entries, stdin); out != "eax\n" {
			t.Errorf("%q renders %q, want the free text alone", stdin, out)
		}
	}
}

// The cache directory can be gone or unwritable, and the line still stands.
func TestRenderDrawsWithoutItsCache(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(blocked, []byte("not a directory\n"), 0o600); err != nil {
		t.Fatalf("write the blocking file: %v", err)
	}
	entries := Normalize([]Entry{
		{Kind: KindValue, Value: "model", Color: "cyan"},
		{Kind: KindValue, Value: "week_top", Thresholds: []Threshold{{At: 0, Color: "green"}}},
		{Kind: KindValue, Value: "cost_turn", Thresholds: []Threshold{{At: 0, Color: "green"}}},
	})
	env := testEnv(t)
	env.CacheDir = filepath.Join(blocked, "cache")
	out := Render(context.Background(), entries, []byte(`{"session_id": "abc", "model": {"display_name": "Opus 5"}, "cost": {"total_cost_usd": 1}}`), env)
	if out != "\x1b[36mOpus 5\x1b[0m\n" {
		t.Fatalf("the line is %q", out)
	}
}

// What the machine answers about itself has to answer something, or its entry
// can never stand in a line.
func TestEverySystemValueAnswersSomething(t *testing.T) {
	for _, value := range Values {
		if value.source != fromSystem {
			continue
		}
		if out := draw(t, []Entry{{Kind: KindValue, Value: value.ID}}, "{}"); strings.TrimSpace(out) == "" {
			t.Errorf("%s answers nothing", value.ID)
		}
	}
}

func TestRenderTakesALabelAsItStands(t *testing.T) {
	entries := Normalize([]Entry{{Kind: KindValue, Value: "model", Label: `it's $(touch pwned) "`, LabelColor: "dim", Color: "cyan"}})
	want := "\x1b[2mit's $(touch pwned) \"\x1b[0m \x1b[36mOpus 5\x1b[0m\n"
	if out := draw(t, entries, `{"model": {"display_name": "Opus 5"}}`); out != want {
		t.Fatalf("the label renders %q, want %q", out, want)
	}
}

// A length of time is one unit of time on the line, and its bound is compared
// against minutes.
func TestRenderLengthsOfTime(t *testing.T) {
	entries := Normalize([]Entry{{Kind: KindValue, Value: "duration", Thresholds: []Threshold{
		{At: 0, Color: "green"}, {At: 60, Color: "red"},
	}}})
	cases := map[string]string{
		"45000":     "\x1b[32m45s\x1b[0m\n",
		"900000":    "\x1b[32m15m\x1b[0m\n",
		"3600000":   "\x1b[31m1h\x1b[0m\n",
		"180000000": "\x1b[31m2d\x1b[0m\n",
	}
	for ms, want := range cases {
		if out := draw(t, entries, `{"cost": {"total_duration_ms": `+ms+`}}`); out != want {
			t.Errorf("%sms renders %q, want %q", ms, out, want)
		}
	}
}

// usageServer answers the usage API and counts how often it was asked.
func usageServer(t *testing.T, body string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer t" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server, &calls
}

// loggedIn is a home with claude's login in it.
func loggedIn(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatalf("make the claude directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", ".credentials.json"), []byte(`{"claudeAiOauth": {"accessToken": "t"}}`), 0o600); err != nil {
		t.Fatalf("write the credentials: %v", err)
	}
	return home
}

func TestRenderOnlyPaysForWhatTheLineShows(t *testing.T) {
	server, calls := usageServer(t, `{"limits": [{"kind": "weekly_scoped", "percent": 82, "resets_at": "2999-01-01T00:00:00Z"}]}`)
	env := testEnv(t)
	env.Home = loggedIn(t)
	env.UsageURL = server.URL
	// The weekly limit and its reset are in the payload, so a line of those
	// two asks no network at all.
	weekly := Normalize([]Entry{
		{Kind: KindValue, Value: "model", Color: "cyan"},
		{Kind: KindValue, Value: "week", Thresholds: []Threshold{{At: 0, Color: "green"}}},
		{Kind: KindValue, Value: "reset", Thresholds: []Threshold{{At: 0, Color: "blue"}}},
	})
	Render(context.Background(), weekly, []byte(`{"rate_limits": {"seven_day": {"used_percentage": 50, "resets_at": 32503680000}}}`), env)
	if calls.Load() != 0 {
		t.Fatalf("a line without the one model limit asked the usage API %d times", calls.Load())
	}
	top := Normalize([]Entry{{Kind: KindValue, Value: "week_top", Label: "F", Thresholds: []Threshold{{At: 0, Color: "green"}}}})
	if out := Render(context.Background(), top, []byte(`{}`), env); out != "F \x1b[32m82%\x1b[0m\n" {
		t.Fatalf("the one model limit is %q", out)
	}
	// A fresh answer stands for two minutes, every redraw in between reads it
	// out of the cache.
	Render(context.Background(), top, []byte(`{}`), env)
	if calls.Load() != 1 {
		t.Fatalf("two redraws asked the usage API %d times, want once", calls.Load())
	}
	env.Now = env.Now.Add(usageMaxAge + time.Second)
	Render(context.Background(), top, []byte(`{}`), env)
	if calls.Load() != 2 {
		t.Fatalf("an answer older than two minutes was not asked again (%d calls)", calls.Load())
	}
}

// A refused call keeps the answer before it, and a refusal is no reason to ask
// again on the next redraw: the stamp moves before the call.
func TestRenderKeepsTheUsageAnswerThroughARefusal(t *testing.T) {
	var calls atomic.Int32
	var refuse atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		if refuse.Load() {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"limits": [{"kind": "weekly_scoped", "percent": 40}]}`))
	}))
	t.Cleanup(server.Close)
	env := testEnv(t)
	env.Home = loggedIn(t)
	env.UsageURL = server.URL
	top := Normalize([]Entry{{Kind: KindValue, Value: "week_top"}})
	Render(context.Background(), top, []byte(`{}`), env)
	refuse.Store(true)
	env.Now = env.Now.Add(usageMaxAge + time.Second)
	if out := Render(context.Background(), top, []byte(`{}`), env); out != "40%\n" {
		t.Fatalf("after a refusal the line says %q, want the last answer", out)
	}
	if out := Render(context.Background(), top, []byte(`{}`), env); out != "40%\n" || calls.Load() != 2 {
		t.Fatalf("a redraw after the refusal says %q after %d calls, want the answer and no third call", out, calls.Load())
	}
}

// The usage cache outlives a refused call, and the API answers 429 for long
// stretches. A weekly bucket whose resets_at has passed belongs to a week that
// is over, so it drops out instead of standing red beside the fresh weekly
// percentage from the payload. resets_at comes as an ISO time with fractional
// seconds and an offset, the shape the API answers with.
func TestRenderDropsAWeeklyLimitPastItsReset(t *testing.T) {
	env := testEnv(t)
	entries := Normalize([]Entry{{Kind: KindValue, Value: "week_top", Label: "F", LabelColor: "dim", Thresholds: []Threshold{{At: 0, Color: "green"}, {At: 80, Color: "red"}}}})
	cache := func(resetsAt string) {
		body := `{"limits": [{"kind": "weekly_all", "percent": 48, "resets_at": "2999-01-01T00:00:00.000000+00:00"},
		  {"kind": "weekly_scoped", "percent": 95, "resets_at": "` + resetsAt + `"}]}`
		path := filepath.Join(env.CacheDir, usageCacheName)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write the cache: %v", err)
		}
		if err := os.WriteFile(path+".stamp", nil, 0o600); err != nil {
			t.Fatalf("write the stamp: %v", err)
		}
	}
	cache("2020-01-05T23:59:59.727697+00:00")
	if out := Render(context.Background(), entries, []byte(`{}`), env); out != "\n" {
		t.Fatalf("a bucket past its reset says %q, want nothing", out)
	}
	cache("2999-01-05T23:59:59.727697+02:00")
	if out := Render(context.Background(), entries, []byte(`{}`), env); out != "\x1b[2mF\x1b[0m \x1b[31m95%\x1b[0m\n" {
		t.Fatalf("a bucket before its reset says %q, want F 95%%", out)
	}
}

// The free text is the entry's own and reaches the line as it stands.
func TestRenderWritesFreeText(t *testing.T) {
	entries := Normalize([]Entry{{Kind: KindValue, Value: FreeTextValue, Text: `on "eax"`, Color: "cyan"}})
	if out := draw(t, entries, "{}"); out != "\x1b[36mon \"eax\"\x1b[0m\n" {
		t.Fatalf("the free text renders %q", out)
	}
}

// A control character in a payload value would either write a second line or
// reach the terminal as a command, so it is cut out where the value is read.
func TestRenderKeepsTheLineWhenAPayloadValueCarriesAControlCharacter(t *testing.T) {
	entries := Normalize([]Entry{
		{Kind: KindValue, Value: "model", Color: "cyan"},
		{Kind: KindSeparator, Text: "·"},
		{Kind: KindValue, Value: "version", Color: "default"},
		{Kind: KindSeparator, Text: "·"},
		{Kind: KindValue, Value: "context", Thresholds: []Threshold{{At: 0, Color: "green"}}},
	})
	body := `{"model": {"display_name": "Op\nus\u001f 5 (1M)"}, "version": "\u001b[31m2.1.233", "context_window": {"used_percentage": 42}}`
	want := "\x1b[36mOpus 5\x1b[0m \x1b[2m·\x1b[0m 2.1.233 \x1b[2m·\x1b[0m \x1b[32m42%\x1b[0m\n"
	if out := draw(t, entries, body); out != want {
		t.Fatalf("the line is %q, want %q", out, want)
	}
}

// Two amounts on one line are written the same way: to the cent, always.
func TestRenderWritesMoneyToTheCent(t *testing.T) {
	entries := Normalize([]Entry{{Kind: KindValue, Value: "cost", Color: "default"}})
	cases := map[string]string{"1.5": "$1.50", "2": "$2.00", "1.2449": "$1.24", "12.345": "$12.35", "0.005": "$0.01"}
	for amount, want := range cases {
		if out := draw(t, entries, `{"cost": {"total_cost_usd": `+amount+`}}`); out != want+"\n" {
			t.Errorf("%s renders %q, want %q", amount, out, want)
		}
	}
	rate := Normalize([]Entry{{Kind: KindValue, Value: "burn"}})
	if out := draw(t, rate, `{"cost": {"total_cost_usd": 2.5, "total_duration_ms": 3600000}}`); out != "$2.50/h\n" {
		t.Errorf("the burn rate is %q, want $2.50/h", out)
	}
}

// A bound is compared against the number on the screen: $1.499 stands as
// $1.50 on the line, so a bound at 1.50 is reached, for the total and for the
// last request's rise alike.
func TestRenderComparesABoundAgainstWhatIsPrinted(t *testing.T) {
	bounds := []Threshold{{At: 0, Color: "green"}, {At: 1.5, Color: "red"}}
	total := Normalize([]Entry{{Kind: KindValue, Value: "cost", Thresholds: bounds}})
	if out := draw(t, total, `{"cost": {"total_cost_usd": 1.499}}`); out != "\x1b[31m$1.50\x1b[0m\n" {
		t.Errorf("a cost of 1.499 prints %q, want $1.50 in red", out)
	}
	if out := draw(t, total, `{"cost": {"total_cost_usd": 1.494}}`); out != "\x1b[32m$1.49\x1b[0m\n" {
		t.Errorf("a cost of 1.494 prints %q, want $1.49 in green", out)
	}
	env := testEnv(t)
	turn := Normalize([]Entry{{Kind: KindValue, Value: "cost_turn", Thresholds: bounds}})
	reading := func(cost string) string {
		return Render(context.Background(), turn, []byte(`{"session_id": "33333333-2222-3333-4444-555555555555", "cost": {"total_cost_usd": `+cost+`}}`), env)
	}
	reading("0")
	if out := reading("1.499"); out != "\x1b[31m$1.50\x1b[0m\n" {
		t.Errorf("a rise of 1.499 prints %q, want $1.50 in red like the total", out)
	}
}

// The limits are a plan's numbers. An API key has none, and the payload then
// carries no rate_limits at all: the entries leave the line together with the
// separators that would otherwise lead nowhere.
func TestRenderDropsTheLimitsWithoutAPlan(t *testing.T) {
	entries := Normalize([]Entry{
		{Kind: KindValue, Value: "model", Color: "cyan"},
		{Kind: KindSeparator, Text: "·"},
		{Kind: KindValue, Value: "session", Label: "5", LabelColor: "dim", Thresholds: []Threshold{{At: 0, Color: "green"}}},
		{Kind: KindSeparator, Text: "·"},
		{Kind: KindValue, Value: "week", Label: "w", LabelColor: "dim", Thresholds: []Threshold{{At: 0, Color: "green"}}},
		{Kind: KindSeparator, Text: "·"},
		{Kind: KindValue, Value: "reset", Label: "↻", LabelColor: "dim", Thresholds: []Threshold{{At: 0, Color: "blue"}}},
		{Kind: KindSeparator, Text: "·"},
		{Kind: KindValue, Value: "dir", Color: "default"},
	})
	want := "\x1b[36mOpus 5\x1b[0m \x1b[2m·\x1b[0m dev-cockpit\n"
	if out := draw(t, entries, `{"model": {"display_name": "Opus 5"}, "cwd": "/root/projects/dev-cockpit"}`); out != want {
		t.Fatalf("without rate limits the line is %q, want %q", out, want)
	}
}

// A count that rounds up to a thousand of its unit is one of the next unit.
func TestRenderMovesACountUpAUnitWhenItRoundsThere(t *testing.T) {
	entries := Normalize([]Entry{{Kind: KindValue, Value: "context_size"}})
	for count, want := range map[string]string{
		"999": "999", "1000": "1k", "999949": "999.9k", "999950": "1M",
		"1500000": "1.5M", "999949999": "999.9M", "999950000": "1G", "2500000000": "2.5G",
	} {
		if out := draw(t, entries, `{"context_window": {"context_window_size": `+count+`}}`); out != want+"\n" {
			t.Errorf("%s tokens read %q, want %q", count, out, want)
		}
	}
}

// The reset of a limit is a moment, and the line shows the time left to it.
func TestRenderCountsDownToAReset(t *testing.T) {
	env := testEnv(t)
	entries := Normalize([]Entry{{Kind: KindValue, Value: "session_reset", Thresholds: []Threshold{{At: 0, Color: "blue"}}}})
	resets := env.Now.Add(2*time.Hour + 30*time.Second).Unix()
	body := `{"rate_limits": {"five_hour": {"used_percentage": 10, "resets_at": ` + itoa(int(resets)) + `}}}`
	if out := Render(context.Background(), entries, []byte(body), env); out != "\x1b[34m2h\x1b[0m\n" {
		t.Fatalf("two hours to the reset read %q", out)
	}
}
