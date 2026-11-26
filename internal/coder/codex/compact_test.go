package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/clirun"
)

func answerLine(t *testing.T) string {
	t.Helper()
	line, err := json.Marshal(map[string]any{"type": recordEvent, "payload": map[string]any{"type": eventItem, "item": map[string]any{"type": itemCoder, "content": []map[string]string{{"type": "Text", "text": "noted"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	return string(line)
}

// The record that replaced the turns, the way 0.172.0 writes it: the type
// behind the timestamp and the ordinal, the history it kept running long.
func compactedLine(history int) string {
	return `{"timestamp":"2026-11-25T07:05:57.572Z","ordinal":20,"type":"compacted","payload":{"message":"","replacement_history":[{"type":"message","role":"user","content":[{"type":"input_text","text":"` +
		strings.Repeat("x", history) + `"}]}]}}`
}

// A compaction is the cockpit's own helper on codex's id of the session,
// behind the end of options, on the chat's model in the equals form; a
// session codex does not hold yet is never compacted.
func TestACompactionRunsTheHelperOnCodexsOwnID(t *testing.T) {
	r, home, project := assistantRunner(t)
	cockpitRollout(t, home, project)
	var _ assistant.Compactor = r

	cmd, err := r.CompactCommand(assistant.TurnRequest{SessionID: cockpitID, Resume: true, Model: "gpt-6.2-sol", Prompt: "ignored"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"codex", "session-compact", "--model=gpt-6.2-sol", "--", firstID}
	if cmd.Name != clirun.Executable() || !slices.Equal(cmd.Args, want) {
		t.Fatalf("command = %s %v, want %v", cmd.Name, cmd.Args, want)
	}
	cmd, err = r.CompactCommand(assistant.TurnRequest{SessionID: cockpitID, Resume: true})
	if err != nil || !slices.Equal(cmd.Args, []string{"codex", "session-compact", "--", firstID}) {
		t.Fatalf("without a model: %v, %v", cmd.Args, err)
	}
	if _, err := r.CompactCommand(assistant.TurnRequest{SessionID: cockpitID}); err == nil {
		t.Fatal("a session to create is no compaction")
	}
	if _, err := r.CompactCommand(assistant.TurnRequest{SessionID: "--last", Resume: true}); err == nil {
		t.Fatal("an id codex never mints is refused")
	}
}

// The compaction speaks codex's app server protocol: the thread loaded without
// its turns on the chat's model, then compacted, and done only once the turn
// it runs as completed with its compaction item. What another thread reports
// meanwhile is not this compaction's.
func TestCompactingSpeaksTheAppServerProtocol(t *testing.T) {
	answer := func(lines ...string) io.Reader { return strings.NewReader(strings.Join(lines, "\n") + "\n") }
	item := func(thread string) string {
		return `{"method":"item/completed","params":{"threadId":"` + thread + `","item":{"type":"contextCompaction","id":"c1"}}}`
	}
	ended := func(thread, status, message string) string {
		turnError := "null"
		if message != "" {
			turnError = `{"message":"` + message + `"}`
		}
		return `{"method":"turn/completed","params":{"threadId":"` + thread + `","turn":{"id":"t1","status":"` + status + `","error":` + turnError + `}}}`
	}
	var sent bytes.Buffer
	err := compactOver(&sent, answer(
		`{"id":0,"result":{}}`,
		`{"id":1,"result":{"thread":{"id":"`+firstID+`"}}}`,
		`{"method":"turn/started","params":{"threadId":"`+firstID+`"}}`,
		`{"id":2,"result":{}}`,
		ended(secondID, "completed", ""),
		item(firstID),
		ended(firstID, "completed", ""),
	), firstID, "gpt-6.2-sol")
	if err != nil {
		t.Fatal(err)
	}
	var requests []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(sent.String()), "\n") {
		var request map[string]any
		if err := json.Unmarshal([]byte(line), &request); err != nil {
			t.Fatalf("request is not one JSON line: %q", line)
		}
		requests = append(requests, request)
	}
	if len(requests) != 4 || requests[2]["method"] != "thread/resume" || requests[3]["method"] != "thread/compact/start" {
		t.Fatalf("requests = %v", requests)
	}
	resume := requests[2]["params"].(map[string]any)
	if resume["threadId"] != firstID || resume["excludeTurns"] != true || resume["model"] != "gpt-6.2-sol" {
		t.Fatalf("resume = %v", resume)
	}
	if start := requests[3]["params"].(map[string]any); start["threadId"] != firstID {
		t.Fatalf("compact = %v", start)
	}
	sent.Reset()
	if err := compactOver(&sent, answer(`{"id":0,"result":{}}`, `{"id":1,"result":{}}`, `{"id":2,"result":{}}`, item(firstID), ended(firstID, "completed", "")), firstID, ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sent.String(), `"model"`) {
		t.Fatalf("without a model the thread keeps its own: %s", sent.String())
	}

	for name, tc := range map[string]struct {
		lines []string
		want  string
	}{
		"failed in codex's words": {[]string{`{"id":2,"result":{}}`, ended(firstID, "failed", "You've hit your usage limit. Try again at 3:05 PM.")}, "You've hit your usage limit. Try again at 3:05 PM."},
		"interrupted":             {[]string{`{"id":2,"result":{}}`, item(firstID), ended(firstID, "interrupted", "")}, "codex interrupted the compaction."},
		"without a compaction":    {[]string{`{"id":2,"result":{}}`, ended(firstID, "completed", "")}, "codex ended the turn without a compaction."},
		"refused":                 {[]string{`{"id":2,"error":{"message":"thread not found"}}`}, "thread not found"},
		"gone before its end":     {[]string{`{"id":2,"result":{}}`, item(firstID)}, "codex ended before the compaction did."},
	} {
		lines := append([]string{`{"id":0,"result":{}}`, `{"id":1,"result":{}}`}, tc.lines...)
		if err := compactOver(io.Discard, answer(lines...), firstID, ""); err == nil || err.Error() != tc.want {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
	err = compactOver(io.Discard, answer(`{"id":0,"result":{}}`, `{"id":1,"error":{"message":"no rollout found for thread id x"}}`), firstID, "")
	if err == nil || err.Error() != "codex thread/resume: no rollout found for thread id x" {
		t.Fatalf("a thread codex cannot load: %v", err)
	}
}

// codex compacts whatever it is given, so the record decides: an answer since
// the start or the last compaction is something to compact, else nothing is,
// and no server starts for it. A compacted record longer than a reading takes
// still counts.
func TestOnlyAnAnswerSinceTheLastCompactionIsCompacted(t *testing.T) {
	sessions, home, project := testRepository(t)
	path := writeRollout(t, home, firstID, map[string]any{"cwd": project}, 0)
	talked := 0
	c := compactor{sessions: sessions, talk: func(ctx context.Context, talk func(w io.Writer, r io.Reader) error) error {
		talked++
		return nil
	}}
	appendLines := func(lines ...string) {
		t.Helper()
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()
		if _, err := f.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
			t.Fatal(err)
		}
	}
	check := func(want string) {
		t.Helper()
		before := talked
		rec := compact(context.Background(), c, firstID, "")
		if rec.Outcome != want || rec.Error != "" || rec.Type != compactRecordType || rec.ThreadID != firstID {
			t.Fatalf("record = %+v, want %s", rec, want)
		}
		if asked := talked > before; asked != (want == compactApplied) {
			t.Fatalf("codex was asked: %v for %s", asked, want)
		}
	}

	check(compactNothing)
	appendLines(`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage"}}}`, `{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"AgentMessage"}]}}`)
	check(compactNothing)
	appendLines(answerLine(t))
	check(compactApplied)
	appendLines(compactedLine(recordLineLimit + 1))
	check(compactNothing)
	appendLines(answerLine(t))
	check(compactApplied)

	c.talk = func(context.Context, func(io.Writer, io.Reader) error) error {
		return errors.New("codex interrupted the compaction.")
	}
	if rec := compact(context.Background(), c, firstID, ""); rec.Outcome != "" || rec.Error != "codex interrupted the compaction." {
		t.Fatalf("a refusal: %+v", rec)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if rec := compact(ctx, c, firstID, ""); rec.Outcome != "" || rec.Error != "It took longer than 15 minutes." {
		t.Fatalf("out of patience: %+v", rec)
	}
	if rec := compact(context.Background(), c, secondID, ""); rec.Outcome != "" || !strings.Contains(rec.Error, "could not be read") {
		t.Fatalf("a session without a record: %+v", rec)
	}
	if rec := compact(context.Background(), c, "--last", ""); rec.Outcome != "" || rec.Error == "" {
		t.Fatalf("an id codex never mints: %+v", rec)
	}
}

// The helper's record is the compaction's report, and the turn reads no
// context: the rollout holds none of what the compaction left.
func TestTheHelpersRecordIsTheCompactionsReport(t *testing.T) {
	r, home, project := assistantRunner(t)
	cockpitRollout(t, home, project, `{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":68000},"model_context_window":100000}}}`)
	for outcome, want := range map[string]*assistant.CompactReport{
		`"outcome":"applied"`:            {Outcome: assistant.CompactApplied},
		`"outcome":"nothing"`:            {Outcome: assistant.CompactNothing},
		`"error":"thread not found"`:     {Reason: "thread not found"},
		`"error":"It took longer than…"`: {Reason: "It took longer than…"},
	} {
		events := runTurn(t, r, `{"type":"compaction","thread_id":"`+firstID+`",`+outcome+`}`)
		if len(events) != 1 || events[0].Kind != assistant.EventCompact || *events[0].Compact != *want {
			t.Fatalf("%s: events = %+v", outcome, events)
		}
	}
	events := runTurn(t, r, `{"type":"compaction","thread_id":"`+secondID+`","outcome":"applied"}`)
	if err := failureOf(events); err == nil || !strings.Contains(err.Error(), "different conversation") {
		t.Fatalf("another thread's record: %+v", events)
	}
}

// A compaction starts the reading over: what codex counts behind it takes in
// nothing, and the next request is the context's size again.
func TestTheContextReadingStartsOverAtACompaction(t *testing.T) {
	count := func(tokens int) string {
		return `{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":` + strings.Repeat("9", tokens) + `},"model_context_window":100000}}}`
	}
	zero := `{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":0,"total_tokens":19821},"model_context_window":100000}}}`
	rollout := strings.Join([]string{count(4), compactedLine(recordLineLimit + 1), zero}, "\n") + "\n"
	if usage, ok := readContextUsage(strings.NewReader(rollout)); ok {
		t.Fatalf("right after a compaction usage = %+v, want none", usage)
	}
	usage, ok := readContextUsage(strings.NewReader(rollout + count(3) + "\n"))
	if !ok || usage.Tokens != 999 || usage.Window != 100000 {
		t.Fatalf("after the next request usage = %+v, %v", usage, ok)
	}
}
