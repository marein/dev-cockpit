package statusline

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tokens are the payload's, the last request's four counts and the two
// sums over them, so this one reads no transcript.
func TestRenderReadsTheTokensOutOfThePayload(t *testing.T) {
	entries := Normalize([]Entry{
		{Kind: KindValue, Value: "tokens_cache_read", Label: "r", LabelColor: "dim"},
		{Kind: KindSeparator, Text: "·"},
		{Kind: KindValue, Value: "context_tokens", Label: "t", LabelColor: "dim"},
		{Kind: KindSeparator, Text: "·"},
		{Kind: KindValue, Value: "cache_hit", Label: "hit", LabelColor: "dim"},
	})
	// The shape of a real payload, measured: the four counts of the last
	// request under current_usage, the two totals beside them.
	body := `{"context_window": {"total_input_tokens": 41531, "total_output_tokens": 217, "used_percentage": 21, "current_usage": {"input_tokens": 2, "output_tokens": 217, "cache_read_input_tokens": 22124, "cache_creation_input_tokens": 19405}}}`
	// 41.5k in the window, the input side alone, and 22124 of those 41531
	// tokens came out of the cache.
	want := "\x1b[2mr\x1b[0m 22.1k \x1b[2m·\x1b[0m \x1b[2mt\x1b[0m 41.5k \x1b[2m·\x1b[0m \x1b[2mhit\x1b[0m 53.27%\n"
	if out := draw(t, entries, body); out != want {
		t.Fatalf("the token line is %q, want %q", out, want)
	}
}

// The window is the input side alone. Measured against claude 2.1.234: a
// payload carrying 32810 input and 1667 output tokens in a 200000 window says
// used_percentage 16, which is 32810 of 200000, and total_input_tokens is
// exactly the three input counts of current_usage added up. The four context
// entries therefore have to agree with each other on one line.
func TestRenderCountsTheWindowTheWayClaudeCountsIt(t *testing.T) {
	entries := Normalize([]Entry{
		{Kind: KindValue, Value: "context", Thresholds: []Threshold{{At: 0, Color: "green"}}},
		{Kind: KindSeparator, Text: "·"},
		{Kind: KindValue, Value: "context_tokens"},
		{Kind: KindSeparator, Text: "·"},
		{Kind: KindValue, Value: "context_left"},
		{Kind: KindSeparator, Text: "·"},
		{Kind: KindValue, Value: "context_size"},
	})
	body := `{"context_window": {"total_input_tokens": 32810, "total_output_tokens": 1667, "context_window_size": 200000, "used_percentage": 16,
	  "current_usage": {"input_tokens": 10, "output_tokens": 1667, "cache_creation_input_tokens": 8398, "cache_read_input_tokens": 24402}}}`
	want := "\x1b[32m16%\x1b[0m \x1b[2m·\x1b[0m 32.8k \x1b[2m·\x1b[0m 167.2k \x1b[2m·\x1b[0m 200k\n"
	if out := draw(t, entries, body); out != want {
		t.Fatalf("the context line is %q, want %q", out, want)
	}
	// A window nobody has put anything in yet is a window whose free room is
	// unknown, not one that is empty: the size alone answers nothing.
	left := Normalize([]Entry{{Kind: KindValue, Value: "context_left"}})
	if bare := draw(t, left, `{"context_window": {"context_window_size": 200000}}`); bare != "\n" {
		t.Fatalf("without a token count the room left is %q, want nothing", bare)
	}
}

// The burn rate refuses to answer early: a coder that has run for seconds would
// report a number nobody can spend.
func TestRenderHoldsTheBurnRateBackUntilItMeansSomething(t *testing.T) {
	entries := Normalize([]Entry{{Kind: KindValue, Value: "burn", Thresholds: []Threshold{{At: 0, Color: "green"}}}})
	if out := draw(t, entries, `{"cost": {"total_cost_usd": 0.09, "total_duration_ms": 11656}}`); out != "\n" {
		t.Fatalf("under a minute the burn rate says %q, want nothing", out)
	}
	if out := draw(t, entries, `{"cost": {"total_cost_usd": 0.1266185, "total_duration_ms": 60904}}`); out != "\x1b[32m$7.48/h\x1b[0m\n" {
		t.Fatalf("the burn rate is %q, want $7.48/h", out)
	}
}

// The cost of the last request is a difference against what the redraw before
// wrote down, so it needs two readings and keeps the last rise until a new one
// comes.
func TestRenderRemembersWhatTheLastRequestCost(t *testing.T) {
	env := testEnv(t)
	entries := Normalize([]Entry{{Kind: KindValue, Value: "cost_turn", Thresholds: []Threshold{{At: 0, Color: "green"}}}})
	reading := func(body string) string {
		return Render(context.Background(), entries, []byte(body), env)
	}
	payload := func(cost string) string {
		return `{"session_id": "11111111-2222-3333-4444-555555555555", "cost": {"total_cost_usd": ` + cost + `}}`
	}
	if out := reading(payload("0.090193")); out != "\n" {
		t.Fatalf("the first reading says %q, want nothing to compare against", out)
	}
	// The rise is 0.0364255, and it is rounded to the cent like every other
	// amount on the line rather than cut off at it.
	if out := reading(payload("0.1266185")); out != "\x1b[32m$0.04\x1b[0m\n" {
		t.Fatalf("the rise says %q, want $0.04", out)
	}
	// A line drawn again without a request in between keeps the last request's
	// cost instead of wiping it to nothing.
	if out := reading(payload("0.1266185")); out != "\x1b[32m$0.04\x1b[0m\n" {
		t.Fatalf("a repeat says %q, want the last rise kept", out)
	}
	// Another coder is another reading: its own file, no difference yet.
	if out := reading(`{"session_id": "22222222-2222-3333-4444-555555555555", "cost": {"total_cost_usd": 5}}`); out != "\n" {
		t.Fatalf("a second coder starts at %q, want nothing", out)
	}
	// An id that is no file name names no file.
	if out := reading(`{"session_id": "../escape", "cost": {"total_cost_usd": 5}}`); out != "\n" {
		t.Fatalf("an id with a path in it says %q", out)
	}
	if _, err := os.Stat(filepath.Join(env.CacheDir, "escape")); !os.IsNotExist(err) {
		t.Fatal("an id with a path in it wrote outside the cost folder")
	}
}

// sessionSums is the four session entries, the ones read out of the transcript.
func sessionSums() []Entry {
	return Normalize([]Entry{
		{Kind: KindValue, Value: "session_input", Label: "i", LabelColor: "dim"},
		{Kind: KindValue, Value: "session_output", Label: "o", LabelColor: "dim"},
		{Kind: KindValue, Value: "session_cache_read", Label: "r", LabelColor: "dim"},
		{Kind: KindValue, Value: "session_cache_write", Label: "w", LabelColor: "dim"},
	})
}

func writeTranscript(t *testing.T, lines string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, []byte(lines), 0o600); err != nil {
		t.Fatalf("write the transcript: %v", err)
	}
	return path
}

func inTranscript(path string) string { return `{"transcript_path": "` + path + `"}` }

// The session sums are the transcript's, and they are what the last request's
// counts cannot say.
func TestRenderAddsUpTheWholeTranscript(t *testing.T) {
	lines := strings.Join([]string{
		`{"type": "user", "message": {"role": "user", "content": "hi"}}`,
		`"a line that is no object at all"`,
		`{"type": "assistant", "message": {"usage": {"input_tokens": 2000, "output_tokens": 8000, "cache_creation_input_tokens": 1000, "cache_read_input_tokens": 4000}}}`,
		`{"type": "assistant", "message": {"usage": {"input_tokens": 3000, "output_tokens": 7000, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 6000}}}`,
	}, "\n") + "\n"
	want := "\x1b[2mi\x1b[0m 5k \x1b[2mo\x1b[0m 15k \x1b[2mr\x1b[0m 10k \x1b[2mw\x1b[0m 1k\n"
	if out := draw(t, sessionSums(), inTranscript(writeTranscript(t, lines))); out != want {
		t.Fatalf("the session sums are %q, want %q", out, want)
	}
	// Without a transcript to read they fall away instead of showing zeroes.
	if bare := draw(t, sessionSums(), inTranscript("/nowhere/at/all.jsonl")); bare != "\n" {
		t.Fatalf("without a transcript the line is %q, want it empty", bare)
	}
}

// A turn is a request, not a line. claude writes one record per content block
// of an answer, and every one of them repeats the **same** usage object of the
// whole request.
func TestRenderCountsATurnOnceHoweverManyBlocksItWrote(t *testing.T) {
	usage := `{"input_tokens": 2000, "output_tokens": 8000, "cache_creation_input_tokens": 1000, "cache_read_input_tokens": 4000}`
	block := func(id, kind string) string {
		return `{"type": "assistant", "requestId": "` + id + `", "uuid": "` + id + kind + `", "message": {"content": [{"type": "` + kind + `"}], "usage": ` + usage + `}}`
	}
	lines := strings.Join([]string{
		block("req_1", "thinking"), block("req_1", "text"), block("req_1", "tool_use"), block("req_1", "tool_use"),
		block("req_2", "text"),
	}, "\n") + "\n"
	// Two turns, not five: 4k in, 16k out, 8k read, 2k written.
	want := "\x1b[2mi\x1b[0m 4k \x1b[2mo\x1b[0m 16k \x1b[2mr\x1b[0m 8k \x1b[2mw\x1b[0m 2k\n"
	if out := draw(t, sessionSums(), inTranscript(writeTranscript(t, lines))); out != want {
		t.Fatalf("the session sums are %q, want %q", out, want)
	}
	// A record without a request id cannot be told from another one, so it is
	// counted as it stands rather than folded into one shared empty key.
	bare := `{"type": "assistant", "message": {"usage": ` + usage + `}}`
	if out := draw(t, sessionSums(), inTranscript(writeTranscript(t, bare+"\n"+bare+"\n"))); out != want {
		t.Fatalf("records without a request id sum to %q, want %q", out, want)
	}
}

// A conversation nobody has spent anything on yet has a transcript with no
// usage record in it. Four zeroes nobody measured are not an answer.
func TestRenderSaysNothingWithoutATurnToAddUp(t *testing.T) {
	for _, lines := range []string{"", `{"type": "user", "message": {"role": "user", "content": "hi"}}` + "\n"} {
		if out := draw(t, sessionSums(), inTranscript(writeTranscript(t, lines))); out != "\n" {
			t.Errorf("a conversation without a turn says %q, want nothing", out)
		}
	}
}

// The line is drawn while claude is appending to the very file it reads, so the
// last record is regularly half written. One unreadable record must not take
// the four sums with it.
func TestRenderKeepsTheSumsWhileTheTranscriptIsBeingWritten(t *testing.T) {
	lines := `{"type": "assistant", "requestId": "req_1", "message": {"usage": {"input_tokens": 2000, "output_tokens": 8000, "cache_creation_input_tokens": 1000, "cache_read_input_tokens": 4000}}}` + "\n" +
		`{"type": "assistant", "requestId": "req_2", "message": {"usage": {"input_t`
	want := "\x1b[2mi\x1b[0m 2k \x1b[2mo\x1b[0m 8k \x1b[2mr\x1b[0m 4k \x1b[2mw\x1b[0m 1k\n"
	if out := draw(t, sessionSums(), inTranscript(writeTranscript(t, lines))); out != want {
		t.Fatalf("with a half written last record the sums are %q, want %q", out, want)
	}
}

// Before claude measured the window, in a fresh session and after /clear, it
// sends total_input_tokens 0 beside a null percentage and a null
// current_usage. The count and the room left are as unknown as the percentage
// then, so all three drop out instead of claiming 0 and the whole window.
func TestRenderDropsTheWindowCountsBeforeClaudeMeasuredIt(t *testing.T) {
	entries := Normalize([]Entry{
		{Kind: KindValue, Value: "context", Thresholds: []Threshold{{At: 0, Color: "green"}}},
		{Kind: KindSeparator, Text: "·"},
		{Kind: KindValue, Value: "context_tokens"},
		{Kind: KindSeparator, Text: "·"},
		{Kind: KindValue, Value: "context_left"},
	})
	fresh := `{"context_window": {"total_input_tokens": 0, "current_usage": null, "used_percentage": null, "context_window_size": 1000000}}`
	if out := draw(t, entries, fresh); out != "\n" {
		t.Fatalf("an unmeasured window says %q, want nothing", out)
	}
	measured := `{"context_window": {"total_input_tokens": 40000, "used_percentage": 4, "context_window_size": 1000000}}`
	want := "\x1b[32m4%\x1b[0m \x1b[2m·\x1b[0m 40k \x1b[2m·\x1b[0m 960k\n"
	if out := draw(t, entries, measured); out != want {
		t.Fatalf("a measured window says %q, want %q", out, want)
	}
}
