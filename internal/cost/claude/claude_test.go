package claude

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
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
	return &harness{t: t, root: root, src: NewSource(root, price.Snapshot, nil, func() int { return cost.DefaultRetention }), now: t0.Add(time.Hour)}
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
	return h.collectSession("")
}

func (h *harness) collectSession(session string) []cost.Entry {
	h.t.Helper()
	entries, cursor, err := h.src.Collect(h.cursor, session, h.now)
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
	s := NewSource("", nil, func() []string { return places }, nil)
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

// A read of one session books what its files gained, its sub-agents' too,
// and leaves every other session to the next read of all of them.
func TestAReadOfOneSessionBooksItAloneAndNothingTwice(t *testing.T) {
	h := newHarness(t)
	p1, p2 := h.path("-p-shop", "s1"), h.path("-p-shop", "s2")
	sub := filepath.Join(h.root, "-p-shop", "s1", "subagents", "agent-a.jsonl")
	h.append(p1, assistant("msg_1", "req_1", "claude-opus-5-5", t0, 1, 1, 0, 0, 0))
	h.append(p2, assistant("msg_2", "req_2", "claude-opus-5-5", t0, 1, 1, 0, 0, 0))
	h.collect()
	h.append(p1, assistant("msg_3", "req_3", "claude-opus-5-5", t0, 2, 2, 0, 0, 0)+stateLine(map[string]float64{"claude-opus-5-5": 1}, false))
	h.append(sub, assistant("msg_4", "req_4", "claude-opus-5-5", t0, 3, 3, 0, 0, 0))
	h.append(p2, assistant("msg_5", "req_5", "claude-opus-5-5", t0, 4, 4, 0, 0, 0))
	got := h.collectSession("s1")
	near(t, "calls", sum(got, false), opus(2, 2, 0, 0, 0)+opus(3, 3, 0, 0, 0))
	near(t, "top-up", sum(got, true), 1-opus(1, 1, 0, 0, 0)-opus(2, 2, 0, 0, 0)-opus(3, 3, 0, 0, 0))
	for _, e := range got {
		if e.Session != "s1" {
			t.Fatalf("another session was read: %+v", e)
		}
	}
	if got := h.collect(); len(got) != 1 || got[0].Session != "s2" {
		t.Fatalf("the next read of every session = %+v", got)
	}
}

func TestParseCostStateFoldsTheContextWindow(t *testing.T) {
	models, _, unknown, ok := parseCostState([]byte(stateLine(map[string]float64{"claude-opus-5[1m]": 1, "claude-opus-5": 2}, false)))
	if !ok || unknown || len(models) != 1 || models["claude-opus-5"] != 3 {
		t.Fatalf("models %v unknown %v ok %v", models, unknown, ok)
	}
	if _, _, _, ok := parseCostState([]byte(`{"type":"user","content":"cost-state"}`)); ok {
		t.Fatal("a line that names cost-state parsed")
	}
}

// The cockpit never writes a null entry into its cursor, a hand edited
// month file may: it is read as absent.
func TestANullCursorEntryIsReadAsAbsent(t *testing.T) {
	h := newHarness(t)
	h.append(h.path("-p-shop", "s1"), assistant("msg_1", "req_1", "claude-opus-5-5", t0, 1, 1, 0, 0, 0))
	h.cursor = json.RawMessage(`{"files":{"/gone.jsonl":null},"sessions":{"s1":null,"s2":null}}`)
	got := h.collect()
	if len(got) != 1 || got[0].Session != "s1" {
		t.Fatalf("entries = %+v", got)
	}
}

// A session's entry lives as long as a file of it and goneKeep longer, a
// claude that still exits may write its total into a new file; then it
// goes, the cursor does not grow with every session ever run.
func TestASessionIsForgottenOnceItsFilesAreGone(t *testing.T) {
	h := newHarness(t)
	h.append(h.path("-p-shop", "keep"), assistant("msg_keep", "req_keep", "claude-opus-5-5", t0, 1, 1, 0, 0, 0))
	var paths []string
	for i := range 200 {
		session := fmt.Sprintf("s%d", i)
		paths = append(paths, h.path("-p-shop", session))
		h.append(paths[i], assistant("msg_"+session, "req_"+session, "claude-opus-5-5", t0, 1, 1, 0, 0, 0))
	}
	h.collect()
	for _, p := range paths {
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	h.collect()
	h.append(paths[0], stateLine(map[string]float64{"claude-opus-5-5": opus(1, 1, 0, 0, 0) + 0.3}, false))
	near(t, "the exit record of a gone session", sum(h.collect(), true), 0.3)
	if err := os.Remove(paths[0]); err != nil {
		t.Fatal(err)
	}
	h.collect()
	h.now = h.now.Add(goneKeep)
	h.collect()
	var st state
	if err := json.Unmarshal(h.cursor, &st); err != nil {
		t.Fatal(err)
	}
	if len(st.Sessions) != 1 || st.Sessions["keep"] == nil || len(st.Files) != 1 {
		t.Fatalf("%d sessions and %d files left, want the one on disk", len(st.Sessions), len(st.Files))
	}
}

// A transcript that comes back after its session was forgotten, a backup
// import restores ~/.claude/projects, was booked already: only what is
// written to it after it came back is booked.
func TestATranscriptThatComesBackIsNotBookedAgain(t *testing.T) {
	h := newHarness(t)
	h.append(h.path("-p-shop", "keep"), userLine)
	p := h.path("-p-shop", "s1")
	call := opus(100, 100, 0, 0, 1000)
	h.append(p, userLine+assistant("msg_1", "req_1", "claude-opus-5-5", t0, 100, 100, 0, 0, 1000))
	h.append(p, stateLine(map[string]float64{"claude-opus-5-5": call + 0.3}, false))
	if got := h.collect(); len(got) != 2 {
		t.Fatalf("first read = %+v", got)
	}
	saved, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	h.collect()
	h.now = h.now.Add(goneKeep)
	h.collect()
	if err := os.WriteFile(p, saved, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := h.collect(); len(got) != 0 {
		t.Fatalf("the restored transcript was booked again: %+v", got)
	}
	h.append(p, assistant("msg_2", "req_2", "claude-opus-5-5", t0.Add(2*time.Hour), 10, 10, 0, 0, 0))
	got := h.collect()
	if len(got) != 1 || got[0].TopUp {
		t.Fatalf("after the restore = %+v", got)
	}
	near(t, "the new call", got[0].USD, opus(10, 10, 0, 0, 0))

	// Once the months it booked into are gone, the session goes for good,
	// and what comes back books into those months, an exit record too.
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	h.collect()
	h.now = h.now.Add(goneKeep)
	h.collect()
	h.now = cost.AddMonths(h.now, cost.DefaultRetention+1)
	h.collect()
	var st state
	if err := json.Unmarshal(h.cursor, &st); err != nil {
		t.Fatal(err)
	}
	if len(st.Forgotten) != 0 {
		t.Fatalf("forgotten = %v", st.Forgotten)
	}
	h.append(p, `{"type":"last-prompt","lastPrompt":"go","sessionId":"s1"}`+"\n"+stateLine(map[string]float64{"claude-opus-5-5": call + 0.3}, false))
	if got := h.collect(); len(got) != 1 || !got[0].At.Equal(t0) {
		t.Fatalf("exit record of a session gone for good = %+v", got)
	}
}

// A total no session can reach is refused where money enters the ledger,
// and what is booked next to it stays exact.
func TestAnAbsurdTotalIsNotBooked(t *testing.T) {
	h := newHarness(t)
	h.append(h.path("-p-shop", "s1"), assistant("msg_1", "req_1", "claude-opus-5-5", t0, 1000, 1000, 0, 0, 0))
	h.append(h.path("-p-shop", "s2"), stateLine(map[string]float64{"claude-opus-5-5": 1.7e308}, false))
	h.append(h.path("-p-shop", "s3"), `{"type":"cost-state","totalCostUSD":1.7e308,"modelUsage":{"claude-opus-5-5":{"costUSD":1.7e308},"claude-opus-5-5[1m]":{"costUSD":1.7e308}}}`+"\n")
	s := cost.New(t.TempDir(), func(string) (string, string) { return "", "" }, h.src)
	if err := s.Collect(); err != nil {
		t.Fatal(err)
	}
	sum := 0.0
	for _, p := range s.Query(time.UTC, cost.Span{From: t0.Add(-time.Hour)}).Projects {
		sum += p.USD
	}
	near(t, "total", sum, opus(1000, 1000, 0, 0, 0))
}

// A listing that fails or comes back empty says nothing about what is gone:
// no cursor and no call id may move on it, or everything is booked again
// once the files can be listed.
func TestAFailedOrEmptyListingKeepsEveryCursor(t *testing.T) {
	h := newHarness(t)
	h.append(h.path("-p-shop", "s1"), assistant("msg_1", "req_1", "claude-opus-5-5", t0, 1000, 1000, 0, 0, 0))
	h.collect()
	before := string(h.cursor)
	away := h.root + ".away"
	try := func(what string) {
		t.Helper()
		_, cursor, _ := h.src.Collect(h.cursor, "", h.now)
		if string(cursor) != before {
			t.Fatalf("%s moved the cursor:\n%s\nwas\n%s", what, cursor, before)
		}
	}
	if err := os.Rename(h.root, away); err != nil {
		t.Fatal(err)
	}
	try("a missing root")
	if err := os.Mkdir(h.root, 0o755); err != nil {
		t.Fatal(err)
	}
	try("an empty root")
	if err := os.Remove(h.root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.root, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	try("a root that cannot be listed")
	if err := os.Remove(h.root); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(away, h.root); err != nil {
		t.Fatal(err)
	}
	if got := h.collect(); len(got) != 0 {
		t.Fatalf("the files came back and were booked again: %+v", got)
	}
}

// A host that never ran claude has no projects folder: that is a state, not
// a failure, nothing is read and nothing moves, and the collector has
// nothing to log every ten seconds.
func TestAMissingRootIsNothingToReadAndNoError(t *testing.T) {
	h := newHarness(t)
	h.append(h.path("-p-shop", "s1"), assistant("msg_1", "req_1", "claude-opus-5-5", t0, 1, 1, 0, 0, 0))
	h.collect()
	before := string(h.cursor)
	if err := os.Rename(h.root, h.root+".away"); err != nil {
		t.Fatal(err)
	}
	entries, cursor, err := h.src.Collect(h.cursor, "", h.now)
	if err != nil || len(entries) != 0 || string(cursor) != before {
		t.Fatalf("entries %+v, err %v, cursor moved %v", entries, err, string(cursor) != before)
	}
}

// A delete waits for claude itself, never for another process that names
// the session.
func TestOnlyAClaudeProcessNamingTheSessionIsRunning(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := NewSource(t.TempDir(), price.Snapshot, nil, nil)
	for _, argv := range [][]string{{bin, "--session-id", "s-run"}, {"sleep", "30", "s-other"}} {
		p := exec.Command(argv[0], argv[1:]...)
		if err := p.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = p.Process.Kill(); _ = p.Wait() })
	}
	deadline := time.Now().Add(2 * time.Second)
	for !src.Running("s-run") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !src.Running("s-run") || src.Running("s-other") || src.Running("s-none") {
		t.Fatalf("running: claude %v, other %v, none %v", src.Running("s-run"), src.Running("s-other"), src.Running("s-none"))
	}
}
