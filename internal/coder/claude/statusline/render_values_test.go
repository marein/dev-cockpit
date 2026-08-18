package statusline

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The marks are a word or nothing: they stand there only while the payload
// says so.
func TestRenderMarksEffortFastModeAndALongContext(t *testing.T) {
	entries := Normalize([]Entry{
		{Kind: KindValue, Value: "effort", Color: "default"},
		{Kind: KindSeparator, Text: "·"},
		{Kind: KindValue, Value: "fast", Color: "default"},
		{Kind: KindSeparator, Text: "·"},
		{Kind: KindValue, Value: "over_200k", Color: "default"},
	})
	on := `{"effort": {"level": "xhigh"}, "fast_mode": true, "exceeds_200k_tokens": true}`
	if out := draw(t, entries, on); out != "xhigh \x1b[2m·\x1b[0m fast \x1b[2m·\x1b[0m >200k\n" {
		t.Fatalf("all three on read %q", out)
	}
	off := `{"fast_mode": false, "exceeds_200k_tokens": false}`
	if out := draw(t, entries, off); out != "\n" {
		t.Fatalf("all three off read %q, want nothing", out)
	}
}

// Thinking says both of its states, because it is on far more often than not
// and a mark for on alone would stand there all the time.
func TestRenderSaysWhetherThinkingIsOn(t *testing.T) {
	entries := Normalize([]Entry{{Kind: KindValue, Value: "thinking", Color: "default"}})
	cases := map[string]string{
		`{"thinking": {"enabled": true}}`:  "thinking\n",
		`{"thinking": {"enabled": false}}`: "no thinking\n",
		`{}`:                               "\n",
	}
	for body, want := range cases {
		if out := draw(t, entries, body); out != want {
			t.Errorf("%s reads %q, want %q", body, out, want)
		}
	}
}

// The cache stays warm until expires_at; past it the cache is cold, and before
// the first answer, or after one that cached nothing, there is no cache to
// speak of.
func TestRenderCountsDownTheWarmCache(t *testing.T) {
	env := testEnv(t)
	entries := Normalize([]Entry{{Kind: KindValue, Value: "cache_left", Thresholds: []Threshold{{At: 0, Color: "red"}, {At: 1, Color: "green"}}}})
	body := func(expires string) []byte {
		return []byte(`{"prompt_cache": {"warm": true, "ttl": "5m", "expires_at": ` + expires + `}}`)
	}
	if out := Render(context.Background(), entries, body(itoa(int(env.Now.Unix()+245))), env); out != "\x1b[32m4m\x1b[0m\n" {
		t.Errorf("four minutes left read %q", out)
	}
	if out := Render(context.Background(), entries, body(itoa(int(env.Now.Unix()-30))), env); out != "\x1b[31m0s\x1b[0m\n" {
		t.Errorf("a cold cache reads %q, want 0s", out)
	}
	if out := Render(context.Background(), entries, body("null"), env); out != "\n" {
		t.Errorf("a cache that cached nothing reads %q", out)
	}
	if out := Render(context.Background(), entries, []byte(`{}`), env); out != "\n" {
		t.Errorf("a session before its first answer reads %q", out)
	}
}

func TestRenderNamesThePullRequest(t *testing.T) {
	entries := Normalize([]Entry{{Kind: KindValue, Value: "pr", Color: "default"}})
	cases := map[string]string{
		`{"pr": {"number": 123, "url": "https://github.com/o/r/pull/123", "review_state": "changes_requested"}}`: "#123 changes requested\n",
		`{"pr": {"number": 123, "url": "https://github.com/o/r/pull/123"}}`:                                      "#123\n",
		`{"pr": {"number": 45, "url": "https://gitlab.com/o/r/-/merge_requests/45", "kind": "mr"}}`:              "!45\n",
		`{}`: "\n",
	}
	for body, want := range cases {
		if out := draw(t, entries, body); out != want {
			t.Errorf("%s reads %q, want %q", body, out, want)
		}
	}
}

// A weekly limit of one model is picked by the name the usage API gives it,
// in whatever case it is typed; no name takes the first one.
func TestRenderPicksTheWeeklyLimitOfANamedModel(t *testing.T) {
	env := testEnv(t)
	body := `{"limits": [
	  {"kind": "weekly_all", "percent": 48},
	  {"kind": "weekly_scoped", "percent": 30, "scope": {"model": {"display_name": "Opus"}}},
	  {"kind": "weekly_scoped", "percent": 95, "scope": {"model": {"display_name": "Fable"}}}]}`
	path := filepath.Join(env.CacheDir, usageCacheName)
	writeFile(t, path, body)
	writeFile(t, path+".stamp", "")
	cases := map[string]string{"": "30%\n", "Opus": "30%\n", "fable": "95%\n", " Fable ": "95%\n", "Sonnet": "\n"}
	for model, want := range cases {
		entries := Normalize([]Entry{{Kind: KindValue, Value: "week_top", Text: model}})
		if out := Render(context.Background(), entries, []byte(`{}`), env); out != want {
			t.Errorf("the model %q reads %q, want %q", model, out, want)
		}
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the cache went away: %v", err)
	}
}
