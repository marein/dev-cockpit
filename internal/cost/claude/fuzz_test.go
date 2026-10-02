package claude

import (
	"encoding/json"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/cost"
	"github.com/marein/dev-cockpit/internal/cost/price"
)

// Lines in the shape of a real transcript, the ids replaced.
const (
	realCall      = `{"parentUuid":"00000000-0000-4000-8000-000000000001","isSidechain":false,"message":{"model":"claude-opus-5","id":"msg_0000000000000000000001","type":"message","role":"assistant","content":[{"type":"text","text":"I'll start by verifying the tag state."}],"stop_reason":"tool_use","stop_sequence":null,"stop_details":null,"usage":{"input_tokens":2,"cache_creation_input_tokens":40692,"cache_read_input_tokens":0,"output_tokens":295,"output_tokens_details":{"thinking_tokens":0},"server_tool_use":{"web_search_requests":0,"web_fetch_requests":0},"service_tier":"standard","cache_creation":{"ephemeral_1h_input_tokens":40692,"ephemeral_5m_input_tokens":0},"inference_geo":"not_available","iterations":[{"input_tokens":2,"output_tokens":295,"cache_read_input_tokens":0,"cache_creation_input_tokens":40692,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":40692},"type":"message"}],"speed":"standard"},"diagnostics":null},"requestId":"req_0000000000000000000001","type":"assistant","uuid":"00000000-0000-4000-8000-000000000002","timestamp":"2026-08-30T22:50:22.825Z","userType":"external","entrypoint":"cli","cwd":"/p/shop","sessionId":"s1","version":"2.1.251","gitBranch":"master"}` + "\n"
	realToolCall  = `{"parentUuid":"00000000-0000-4000-8000-000000000002","isSidechain":false,"message":{"model":"claude-opus-5","id":"msg_0000000000000000000001","type":"message","role":"assistant","content":[{"type":"tool_use","id":"toolu_000000000000000000000001","name":"Bash","input":{"command":"git tag","description":"List tags"},"caller":{"type":"direct"}}],"stop_reason":"tool_use","usage":{"input_tokens":2,"cache_creation_input_tokens":40692,"cache_read_input_tokens":0,"output_tokens":311,"server_tool_use":{"web_search_requests":0,"web_fetch_requests":0},"service_tier":"standard","cache_creation":{"ephemeral_1h_input_tokens":40692,"ephemeral_5m_input_tokens":0},"speed":"standard"}},"requestId":"req_0000000000000000000001","type":"assistant","uuid":"00000000-0000-4000-8000-000000000003","timestamp":"2026-08-30T22:50:24.242Z","cwd":"/p/shop","sessionId":"s1"}` + "\n"
	realUser      = `{"parentUuid":null,"isSidechain":false,"type":"user","message":{"role":"user","content":"go"},"uuid":"00000000-0000-4000-8000-000000000004","timestamp":"2026-08-30T22:50:20.000Z","cwd":"/p/shop","sessionId":"s1"}` + "\n"
	realCostState = `{"type":"cost-state","sessionId":"s1","totalCostUSD":1.738094,"totalAPIDuration":166533,"totalDuration":3732952,"startTime":1790867653551,"modelUsage":{"claude-opus-5-5":{"inputTokens":576,"outputTokens":13104,"cacheReadInputTokens":3026670,"cacheCreationInputTokens":108547,"webSearchRequests":0,"costUSD":1.738094}},"hasUnknownModelCost":false}` + "\n"
	realSubCall   = `{"parentUuid":null,"isSidechain":true,"agentId":"a1","message":{"model":"claude-sonnet-5-5","id":"msg_0000000000000000000009","type":"message","role":"assistant","content":[],"usage":{"input_tokens":12,"cache_creation_input_tokens":900,"cache_read_input_tokens":4000,"output_tokens":80,"cache_creation":{"ephemeral_5m_input_tokens":900,"ephemeral_1h_input_tokens":0},"server_tool_use":{"web_search_requests":2},"speed":"fast"}},"requestId":"req_0000000000000000000009","type":"assistant","timestamp":"2026-08-30T22:50:23.000Z","cwd":"/p/shop","sessionId":"s1"}` + "\n"
)

// recorder keeps what the source handed the ledger.
type recorder struct {
	*Source
	entries []cost.Entry
}

func (r *recorder) Collect(cursor json.RawMessage, session string, now time.Time) ([]cost.Entry, json.RawMessage, error) {
	entries, next, err := r.Source.Collect(cursor, session, now)
	r.entries = append(r.entries, entries...)
	return entries, next, err
}

func fuzzWrite(t *testing.T, path string, data []byte, flag int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|flag, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
}

// snapshotDir reads every file under dir.
func snapshotDir(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		out[p] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func checkRows(t *testing.T, dir string) {
	t.Helper()
	paths, _ := filepath.Glob(filepath.Join(dir, "rows-*.json"))
	for _, p := range paths {
		var f struct {
			Rows    []cost.Row                 `json:"rows"`
			Cursors map[string]json.RawMessage `json:"cursors"`
		}
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &f); err != nil {
			t.Fatalf("%s does not parse: %v", p, err)
		}
		for _, r := range f.Rows {
			tk := r.Tokens
			if !cost.Usable(r.USD) || tk.Input < 0 || tk.Output < 0 || tk.CacheRead < 0 || tk.CacheWrite5m < 0 || tk.CacheWrite1h < 0 || tk.WebSearch < 0 {
				t.Fatalf("booked an unusable row %+v", r)
			}
		}
		var st state
		if err := json.Unmarshal(f.Cursors["claude"], &st); len(f.Cursors["claude"]) > 0 && err != nil {
			t.Fatalf("cursor does not parse: %v", err)
		}
		for id, ss := range st.Sessions {
			for _, m := range []map[string]float64{ss.Booked, ss.Copied, ss.TopUp} {
				for model, v := range m {
					if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
						t.Fatalf("session %s holds %g USD of %s", id, v, model)
					}
				}
			}
		}
	}
}

// FuzzTranscript reads a session's file, one of its sub-agents and a fork
// through the ledger, the main file in two writes split anywhere, a line
// too. Whatever the bytes, nothing panics, every booked amount is a usable
// one and a read of the same bytes again books nothing.
func FuzzTranscript(f *testing.F) {
	log.SetOutput(io.Discard)
	f.Cleanup(func() { log.SetOutput(os.Stderr) })
	huge := `{"type":"assistant","timestamp":"2026-09-30T10:00:00Z","message":{"id":"msg_h","model":"claude-opus-5-5","content":[{"type":"text","text":"` +
		strings.Repeat("x", 70_000) + `"}],"usage":{"input_tokens":1,"output_tokens":2}}}` + "\n"
	absurd := `{"type":"assistant","timestamp":"2026-09-30T10:00:00Z","message":{"id":"msg_n","model":"claude-opus-5-5","usage":{"input_tokens":-1000000000000,"output_tokens":9223372036854775807,"cache_creation_input_tokens":-5,"cache_creation":{"ephemeral_5m_input_tokens":9223372036854775807,"ephemeral_1h_input_tokens":9223372036854775807}}}}` + "\n"
	negative := `{"type":"assistant","timestamp":"2026-09-30T10:00:00Z","message":{"id":"msg_m","model":"claude-opus-5-5","usage":{"input_tokens":-1000000,"output_tokens":10}}}` + "\n"
	seeds := []struct {
		main, sub, fork string
		cut             uint16
	}{
		{realUser + realCall + realToolCall + realCostState, realSubCall, "", 0},
		{realUser + realCall + realToolCall + realCostState, realSubCall, realUser + realCall + realToolCall, 300},
		{realUser + realCall + realToolCall[:200], "", "", 1000},
		{realUser + huge + realCall, "", "", 100},
		{absurd + realCostState, "", "", 0},
		{negative + realCostState, "", "", 0},
		{absurd + strings.Replace(realCostState, "1.738094", "1e308", 2), absurd, absurd, 0},
		{`{"type":"cost-state","totalCostUSD":1e308,"modelUsage":{"claude-opus-5-5":{"costUSD":1e308},"claude-opus-5-5[1m]":{"costUSD":1e308}}}` + "\n", "", "", 0},
		{`{"type":"cost-state","totalCostUSD":-5,"modelUsage":{"claude-opus-5-5":{"costUSD":-5}}}` + "\n", "", "", 0},
		{`{"type":"assistant","message":{"id":"","model":"claude-opus-5","usage":{"input_tokens":"5"}}}` + "\n" + `{"type":7,"message":[],"usage":{` + "\n", "\x00\xff\n", "null\n", 7},
		{`{"type":"assistant","message":{"id":"m","model":"claude-opus-5","usage":{"input_tokens":1e30}}}` + "\n", "", "", 0},
	}
	for _, s := range seeds {
		f.Add([]byte(s.main), []byte(s.sub), []byte(s.fork), s.cut)
	}
	f.Fuzz(func(t *testing.T, main, sub, fork []byte, cut uint16) {
		root, stateDir := t.TempDir(), t.TempDir()
		src := &recorder{Source: NewSource(root, price.Snapshot, func() []string { return []string{"/p/shop", "/p/*/x"} }, func() int { return cost.DefaultRetention })}
		svc := cost.New(stateDir, nil, src)
		dir := filepath.Join(stateDir, "cost")
		mainPath := filepath.Join(root, "-p-shop", "s1.jsonl")
		c := int(cut) % (len(main) + 1)
		fuzzWrite(t, mainPath, main[:c], os.O_TRUNC)
		fuzzWrite(t, filepath.Join(root, "-p-shop", "s1", "subagents", "agent-a1.jsonl"), sub, os.O_TRUNC)
		fuzzWrite(t, filepath.Join(root, "-p-shop", "s2.jsonl"), fork, os.O_TRUNC)
		if err := svc.Collect(); err != nil {
			t.Fatalf("first collect: %v", err)
		}
		fuzzWrite(t, mainPath, main[c:], os.O_APPEND)
		if err := svc.Collect(); err != nil {
			t.Fatalf("second collect: %v", err)
		}
		for _, e := range src.entries {
			if !(e.USD >= 0) {
				t.Fatalf("source handed over %g USD: %+v", e.USD, e)
			}
		}
		checkRows(t, dir)
		before := snapshotDir(t, dir)
		src.entries = nil
		if err := svc.Collect(); err != nil {
			t.Fatalf("idle collect: %v", err)
		}
		if len(src.entries) > 0 {
			t.Fatalf("the same bytes booked again: %+v", src.entries)
		}
		after := snapshotDir(t, dir)
		for p, data := range after {
			if before[p] != data {
				t.Fatalf("an idle collect rewrote %s:\n%s\nwas\n%s", p, data, before[p])
			}
		}
		if len(after) != len(before) {
			t.Fatalf("an idle collect changed the files: %d, was %d", len(after), len(before))
		}
	})
}
