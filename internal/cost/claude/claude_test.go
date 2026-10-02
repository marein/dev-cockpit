package claude

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/cost"
	"github.com/marein/dev-cockpit/internal/cost/price"
)

var t0 = time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)

// assistant is one transcript line of a call, in claude's own shape.
func assistant(id, req, model string, at time.Time, in, out, read, w5, w1 int64) string {
	reqID := "null"
	if req != "" {
		reqID = `"` + req + `"`
	}
	return fmt.Sprintf(`{"parentUuid":"p","isSidechain":false,"type":"assistant","cwd":"/p/shop","sessionId":"x","requestId":%s,`+
		`"message":{"id":"%s","model":"%s","role":"assistant","content":[{"type":"text","text":"hi"}],`+
		`"usage":{"input_tokens":%d,"output_tokens":%d,"cache_read_input_tokens":%d,"cache_creation_input_tokens":%d,`+
		`"cache_creation":{"ephemeral_5m_input_tokens":%d,"ephemeral_1h_input_tokens":%d},"output_tokens_details":{"thinking_tokens":1},`+
		`"server_tool_use":{"web_search_requests":0,"web_fetch_requests":0},"service_tier":"standard","speed":"standard"}},`+
		`"uuid":"u","timestamp":"%s"}`+"\n", reqID, id, model, in, out, read, w5+w1, w5, w1, at.Format(time.RFC3339Nano))
}

func stateLine(models map[string]float64, unknown bool) string {
	usage := map[string]any{}
	total := 0.0
	for m, v := range models {
		usage[m] = map[string]any{"inputTokens": 1, "outputTokens": 1, "costUSD": v}
		total += v
	}
	raw, _ := json.Marshal(map[string]any{
		"type": "cost-state", "sessionId": "x", "totalCostUSD": total, "startTime": t0.UnixMilli(),
		"modelUsage": usage, "hasUnknownModelCost": unknown,
	})
	return string(raw) + "\n"
}

const userLine = `{"type":"user","cwd":"/p/shop","message":{"role":"user","content":"go"},"timestamp":"2026-10-02T09:59:00Z"}` + "\n"

// opus is opus 5.5 at 4, 20, 0.2, 5, 8 USD per million tokens.
func opus(in, out, read, w5, w1 int64) float64 {
	return float64(in*4+out*20)/1e6 + float64(read)*0.2/1e6 + float64(w5*5+w1*8)/1e6
}

type harness struct {
	t      *testing.T
	root   string
	src    *Source
	cursor json.RawMessage
	now    time.Time
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	root := t.TempDir()
	return &harness{t: t, root: root, src: NewSource(root, price.Snapshot, nil), now: t0.Add(time.Hour)}
}

func (h *harness) path(dir, session string) string {
	return filepath.Join(h.root, dir, session+".jsonl")
}

func (h *harness) append(path, text string) {
	h.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		h.t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		h.t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) collect() []cost.Entry {
	h.t.Helper()
	return h.collectFrom(nil)
}

func (h *harness) collectFrom(captured any) []cost.Entry {
	h.t.Helper()
	entries, cursor, err := h.src.Collect(h.cursor, captured, h.now)
	if err != nil {
		h.t.Fatal(err)
	}
	h.cursor = cursor
	return entries
}

func sum(entries []cost.Entry, topUp bool) float64 {
	s := 0.0
	for _, e := range entries {
		if e.TopUp == topUp {
			s += e.USD
		}
	}
	return s
}

func near(t *testing.T, what string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("%s = %.9f, want %.9f", what, got, want)
	}
}

// claude writes one line per content block of a response, each with the
// full usage; a read may end between them.
func TestOneCallOverSeveralLinesIsBookedOnce(t *testing.T) {
	h := newHarness(t)
	p := h.path("-p-shop", "s1")
	call := assistant("msg_1", "req_1", "claude-opus-5-5", t0, 10, 200, 30000, 0, 4000)
	h.append(p, userLine+call+call)
	got := h.collect()
	if len(got) != 1 {
		t.Fatalf("entries = %+v", got)
	}
	e := got[0]
	near(t, "usd", e.USD, opus(10, 200, 30000, 0, 4000))
	if !e.At.Equal(t0) || e.Model != "claude-opus-5-5" || e.CWD != "/p/shop" || e.Session != "s1" || e.Tokens.CacheWrite1h != 4000 {
		t.Fatalf("entry = %+v", e)
	}
	h.append(p, call)
	if got := h.collect(); len(got) != 0 {
		t.Fatalf("the third line of the call booked again: %+v", got)
	}
	if got := h.collect(); len(got) != 0 {
		t.Fatalf("an idle read booked: %+v", got)
	}
}

// Of two lines of one call with different usage, the larger counts: the
// growth is booked, never the sum.
func TestAGrowingCallBooksItsGrowth(t *testing.T) {
	h := newHarness(t)
	p := h.path("-p-shop", "s1")
	h.append(p, assistant("msg_1", "", "nemotron-3-ultra", t0, 100, 0, 0, 0, 0))
	h.collect()
	h.append(p, assistant("msg_1", "", "nemotron-3-ultra", t0, 100, 40, 60, 0, 0))
	got := h.collect()
	if len(got) != 1 || got[0].Tokens != (price.Tokens{Output: 40, CacheRead: 60}) || !got[0].Unpriced || got[0].USD != 0 {
		t.Fatalf("growth = %+v", got)
	}
}

func TestNoticesAndErrorsBookNothing(t *testing.T) {
	h := newHarness(t)
	p := h.path("-p-shop", "s1")
	h.append(p, assistant("8d1f-uuid", "", "<synthetic>", t0, 0, 0, 0, 0, 0))
	errLine := `{"type":"assistant","isApiErrorMessage":true,"message":{"id":"msg_e","model":"claude-opus-5-5","usage":{"input_tokens":5,"output_tokens":5}},"timestamp":"2026-10-02T10:00:00Z"}` + "\n"
	h.append(p, errLine)
	if got := h.collect(); len(got) != 0 {
		t.Fatalf("entries = %+v", got)
	}
}

// A sub-agent's calls are the parent session's, read before the parent's
// running total so the top-up does not take them a second time.
func TestSubAgentsBookUnderTheParentBeforeItsTotal(t *testing.T) {
	h := newHarness(t)
	main := h.path("-p-shop", "s1")
	sub := filepath.Join(h.root, "-p-shop", "s1", "subagents", "agent-a1.jsonl")
	parentCall := opus(10, 100, 0, 0, 1000)
	subCall := opus(5, 3, 2000, 500, 0)
	h.append(main, assistant("msg_1", "req_1", "claude-opus-5-5", t0, 10, 100, 0, 0, 1000))
	h.append(sub, assistant("msg_s", "req_s", "claude-opus-5-5", t0.Add(time.Minute), 5, 3, 2000, 500, 0))
	h.append(main, stateLine(map[string]float64{"claude-opus-5-5": parentCall + subCall + 0.5}, false))
	got := h.collect()
	near(t, "calls", sum(got, false), parentCall+subCall)
	near(t, "top-up", sum(got, true), 0.5)
	for _, e := range got {
		if e.Session != "s1" {
			t.Fatalf("a sub-agent call left the session: %+v", e)
		}
	}
}

// The total tops up only what the calls do not explain, once, per model,
// and never books less than nothing.
func TestTheTotalTopsUpTheGapOnce(t *testing.T) {
	h := newHarness(t)
	p := h.path("-p-shop", "s1")
	call := opus(1000, 2000, 50000, 0, 3000)
	h.append(p, assistant("msg_1", "req_1", "claude-opus-5-5", t0, 1000, 2000, 50000, 0, 3000))
	h.append(p, stateLine(map[string]float64{"claude-opus-5-5[1m]": call + 0.25, "claude-haiku-4-5-20251001": 0.04}, false))
	got := h.collect()
	near(t, "top-up", sum(got, true), 0.29)
	for _, e := range got {
		if e.TopUp && (e.Model == "claude-opus-5-5[1m]" || !e.At.Equal(t0)) {
			t.Fatalf("top-up = %+v", e)
		}
	}
	// The same total again, a second copy of it, and a resumed process
	// that restored an older one: nothing.
	h.append(p, stateLine(map[string]float64{"claude-opus-5-5": call + 0.25, "claude-haiku-4-5-20251001": 0.04}, false))
	h.append(p, stateLine(map[string]float64{"claude-opus-5-5": call}, false))
	if got := h.collect(); len(got) != 0 {
		t.Fatalf("a repeated total booked: %+v", got)
	}
	// A total below the calls, a killed process' calls stand: nothing.
	h.append(p, assistant("msg_2", "req_2", "claude-opus-5-5", t0.Add(time.Minute), 10, 10, 0, 0, 0))
	h.append(p, stateLine(map[string]float64{"claude-opus-5-5": call + 0.25}, false))
	got = h.collect()
	if len(got) != 1 || got[0].TopUp {
		t.Fatalf("after a kill = %+v", got)
	}
	// Under a cent waits until it adds up to one.
	h.append(p, stateLine(map[string]float64{"claude-opus-5-5": call + 0.25 + opus(10, 10, 0, 0, 0) + 0.006}, false))
	if got := h.collect(); len(got) != 0 {
		t.Fatalf("a gap under a cent booked: %+v", got)
	}
	h.append(p, stateLine(map[string]float64{"claude-opus-5-5": call + 0.25 + opus(10, 10, 0, 0, 0) + 0.012}, false))
	near(t, "the gap that added up", sum(h.collect(), true), 0.012)
}

// Ollama models have no list price: their tokens count, no money, and
// claude's own total, which prices them at a claude rate, is not spend.
func TestUnknownModelsCountTokensAndNeverTopUp(t *testing.T) {
	h := newHarness(t)
	p := h.path("-p-shop", "s1")
	h.append(p, assistant("msg_1", "", "nemotron-3-ultra", t0, 1000, 10, 0, 0, 0))
	h.append(p, assistant("msg_2", "req_2", "claude-sonnet-5-5", t0, 10, 10, 0, 0, 0))
	h.append(p, stateLine(map[string]float64{"nemotron-3-ultra:cloud": 4.33, "claude-sonnet-5-5": 1}, true))
	got := h.collect()
	if len(got) != 2 || !got[0].Unpriced || got[0].USD != 0 || got[0].Tokens.Input != 1000 || got[1].Unpriced {
		t.Fatalf("entries = %+v", got)
	}
	h.append(p, stateLine(map[string]float64{"claude-sonnet-5-5": 2}, false))
	if got := h.collect(); len(got) != 0 {
		t.Fatalf("a session with an unknown model was topped up: %+v", got)
	}
}

// A fork copies its parent's calls under the same ids, and its total holds
// the parent's spend: the copies are booked once, by whoever came first, and
// the fork's top-up does not take them again.
func TestAForkBooksTheParentsCallsOnce(t *testing.T) {
	h := newHarness(t)
	parent := h.path("-p-shop", "aaaa")
	fork := h.path("-p-shop", "bbbb")
	first := opus(100, 100, 0, 0, 1000)
	own := opus(50, 50, 0, 0, 0)
	h.append(parent, assistant("msg_1", "req_1", "claude-opus-5-5", t0, 100, 100, 0, 0, 1000))
	h.collect()
	h.append(fork, assistant("msg_1", "req_1", "claude-opus-5-5", t0, 100, 100, 0, 0, 1000))
	h.append(fork, assistant("msg_1", "req_1", "claude-opus-5-5", t0, 100, 100, 0, 0, 1000))
	h.append(fork, assistant("msg_2", "req_2", "claude-opus-5-5", t0.Add(time.Minute), 50, 50, 0, 0, 0))
	h.append(fork, stateLine(map[string]float64{"claude-opus-5-5": first + own}, false))
	got := h.collect()
	if len(got) != 1 || got[0].Session != "bbbb" {
		t.Fatalf("fork entries = %+v", got)
	}
	near(t, "fork", got[0].USD, own)
}

// The cockpit's delete removes a transcript while claude is still exiting,
// and claude writes its exit lines into a new file: the total there tops up
// only what the session did not book while it ran. A session the ledger
// never saw is booked from the total alone.
func TestAnExitRecordOnlyTopsUp(t *testing.T) {
	h := newHarness(t)
	p := h.path("-p-shop", "s1")
	call := opus(100, 100, 0, 0, 1000)
	h.append(p, userLine+assistant("msg_1", "req_1", "claude-opus-5-5", t0, 100, 100, 0, 0, 1000))
	h.collect()
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	h.collect()
	exit := `{"type":"last-prompt","lastPrompt":"go","sessionId":"s1"}` + "\n" + stateLine(map[string]float64{"claude-opus-5-5": call + 0.3}, false)
	h.append(p, exit)
	got := h.collect()
	if len(got) != 1 || !got[0].TopUp {
		t.Fatalf("exit record = %+v", got)
	}
	near(t, "exit record", got[0].USD, 0.3)

	h.append(h.path("-p-gone", "s2"), exit)
	got = h.collect()
	if len(got) != 1 || !got[0].TopUp {
		t.Fatalf("unseen exit record = %+v", got)
	}
	near(t, "unseen", got[0].USD, call+0.3)
}

// A transcript without a cwd line names its directory only through the name
// of the directory it lies in.
func TestThePlaceComesFromTheDirectoryName(t *testing.T) {
	places := []string{"/root/projects/dev-cockpit", "/root/projects/dev-cockpit-x", "/root/projects/*",
		"/s/assistant/instances/abc/workspace", "/s/assistant/instances/*/workspace"}
	s := NewSource("", nil, func() []string { return places })
	cases := map[string]string{
		"-root-projects-dev-cockpit":                          "/root/projects/dev-cockpit",
		"-root-projects-dev-cockpit-x":                        "/root/projects/dev-cockpit-x",
		"-root-projects-dev-cockpit-worktree-combo":           "/root/projects/dev-cockpit-worktree-combo",
		"-s-assistant-instances-abc-workspace":                "/s/assistant/instances/abc/workspace",
		"-s-assistant-instances-0f3a-9c-workspace":            "/s/assistant/instances/0f3a-9c/workspace",
		"-tmp-claude-0-ctxrun":                                "",
		"-root-projects-":                                     "",
		"-root--local-state-dev-cockpit-assistant-instances-": "",
	}
	for dir, want := range cases {
		if got := s.placeOf(dir); got != want {
			t.Errorf("placeOf(%q) = %q, want %q", dir, got, want)
		}
	}
	if got := encodeDir("/root/.local/state/x_y z"); got != "-root--local-state-x-y-z" {
		t.Fatalf("encodeDir = %q", got)
	}

	h := newHarness(t)
	h.src.places = func() []string { return []string{filepath.Join("/root/projects", "*")} }
	h.append(h.path("-root-projects-shop", "s9"), stateLine(map[string]float64{"claude-opus-5-5": 1}, false))
	got := h.collect()
	if len(got) != 1 || got[0].CWD != "/root/projects/shop" {
		t.Fatalf("entries = %+v", got)
	}
}

// A file replaced under the same name is read from the top: what was booked
// stays booked once.
func TestAReplacedFileBooksNothingTwice(t *testing.T) {
	h := newHarness(t)
	p := h.path("-p-shop", "s1")
	long := `{"type":"summary","summary":"` + fmt.Sprintf("%0300d", 0) + `"}` + "\n"
	h.append(p, long+assistant("msg_1", "req_1", "claude-opus-5-5", t0, 1, 1, 0, 0, 0))
	h.collect()
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	h.append(p, userLine+long+assistant("msg_1", "req_1", "claude-opus-5-5", t0, 1, 1, 0, 0, 0)+
		assistant("msg_2", "req_2", "claude-opus-5-5", t0, 2, 2, 0, 0, 0))
	got := h.collect()
	if len(got) != 1 {
		t.Fatalf("entries = %+v", got)
	}
	near(t, "new call", got[0].USD, opus(2, 2, 0, 0, 0))
}

// A capture holds what the session's files gained past the last read, and
// its head: read after the files went, it books exactly that, and nothing of
// another session moves.
func TestACaptureBooksWhatTheGoneFilesGainedAndNothingElse(t *testing.T) {
	h := newHarness(t)
	long := `{"type":"summary","summary":"` + fmt.Sprintf("%0300d", 0) + `"}` + "\n"
	p1, p2 := h.path("-p-shop", "s1"), h.path("-p-shop", "s2")
	sub := filepath.Join(h.root, "-p-shop", "s1", "subagents", "agent-a.jsonl")
	h.append(p1, long+assistant("msg_1", "req_1", "claude-opus-5-5", t0, 1, 1, 0, 0, 0))
	h.append(p2, assistant("msg_2", "req_2", "claude-opus-5-5", t0, 1, 1, 0, 0, 0))
	h.collect()
	h.append(p1, assistant("msg_3", "req_3", "claude-opus-5-5", t0, 2, 2, 0, 0, 0)+stateLine(map[string]float64{"claude-opus-5-5": 1}, false))
	h.append(sub, assistant("msg_4", "req_4", "claude-opus-5-5", t0, 3, 3, 0, 0, 0))
	h.append(p2, assistant("msg_5", "req_5", "claude-opus-5-5", t0, 4, 4, 0, 0, 0))
	capt := h.src.Capture("s1")
	if c := capt.(captured); c.files[p1].from == 0 || len(c.files[p1].head) != headSize || len(c.files) != 2 {
		t.Fatalf("the capture read more than the rest: from %d, %d files", c.files[p1].from, len(c.files))
	}
	for _, p := range []string{p1, sub} {
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	got := h.collectFrom(capt)
	near(t, "calls", sum(got, false), opus(2, 2, 0, 0, 0)+opus(3, 3, 0, 0, 0))
	near(t, "top-up", sum(got, true), 1-opus(1, 1, 0, 0, 0)-opus(2, 2, 0, 0, 0)-opus(3, 3, 0, 0, 0))
	for _, e := range got {
		if e.Session != "s1" {
			t.Fatalf("another session was read: %+v", e)
		}
	}
	if got := h.collect(); len(got) != 1 || got[0].Session != "s2" {
		t.Fatalf("the next read of the disk = %+v", got)
	}
}

func TestParseCostStateFoldsTheContextWindow(t *testing.T) {
	models, unknown, ok := ParseCostState([]byte(stateLine(map[string]float64{"claude-opus-5[1m]": 1, "claude-opus-5": 2}, false)))
	if !ok || unknown || len(models) != 1 || models["claude-opus-5"] != 3 {
		t.Fatalf("models %v unknown %v ok %v", models, unknown, ok)
	}
	if _, _, ok := ParseCostState([]byte(`{"type":"user","content":"cost-state"}`)); ok {
		t.Fatal("a line that names cost-state parsed")
	}
}

// The cockpit never writes a null entry into its cursor, a hand edited
// cursors.json may: it is read as absent.
func TestANullCursorEntryIsReadAsAbsent(t *testing.T) {
	h := newHarness(t)
	h.append(h.path("-p-shop", "s1"), assistant("msg_1", "req_1", "claude-opus-5-5", t0, 1, 1, 0, 0, 0))
	h.cursor = json.RawMessage(`{"files":{"/gone.jsonl":null},"sessions":{"s1":null,"s2":null}}`)
	got := h.collect()
	if len(got) != 1 || got[0].Session != "s1" {
		t.Fatalf("entries = %+v", got)
	}
}

// A call id is kept while the session's transcript is on disk, a resume may
// copy it again; once the file is gone the ids go and the sums stay.
func TestTheCallIdsLiveAsLongAsTheTranscript(t *testing.T) {
	h := newHarness(t)
	p := h.path("-p-shop", "s1")
	h.append(p, assistant("msg_1", "req_1", "claude-opus-5-5", t0, 1, 1, 0, 0, 0))
	read := func() *sessionState {
		var st state
		if err := json.Unmarshal(h.cursor, &st); err != nil {
			t.Fatal(err)
		}
		return st.Sessions["s1"]
	}
	h.collect()
	h.collect()
	if ss := read(); ss == nil || len(ss.Messages) != 1 {
		t.Fatalf("the id went while the file is there: %+v", ss)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	h.collect()
	if ss := read(); ss == nil || len(ss.Messages) != 0 || ss.Booked["claude-opus-5-5"] == 0 {
		t.Fatalf("after the file went: %+v", ss)
	}
}
