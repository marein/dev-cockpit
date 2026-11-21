package claude

import (
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/assistant"
)

func compactOf(events []assistant.Event) *assistant.CompactReport {
	for _, ev := range events {
		if ev.Kind == assistant.EventCompact {
			return ev.Compact
		}
	}
	return nil
}

func TestACompactionIsAResumedTurnOfTheBareCommand(t *testing.T) {
	r := &runner{sessions: stubSessions{}}
	cmd, err := r.CompactCommand(assistant.TurnRequest{SessionID: sessionID, Resume: true, Workdir: t.TempDir(), Prompt: "ignored", Model: "claude-opus-5"})
	if err != nil {
		t.Fatalf("command: %v", err)
	}
	args := cmd.Args
	if cmd.Name != "claude" || args[len(args)-2] != endOfOptions || args[len(args)-1] != "/compact" {
		t.Fatalf("the compaction runs as %s %v", cmd.Name, args)
	}
	argv := strings.Join(args, " ")
	if !strings.Contains(argv, "--resume "+sessionID) || !strings.Contains(argv, "--model claude-opus-5") || strings.Contains(argv, "ignored") {
		t.Fatalf("the compaction runs as %s", argv)
	}
}

// The stream of `claude -p /compact` (Claude Code 2.1.329): no assistant
// record, usage all 0, the boundary is the one sign it went through.
func TestACompactionReportsItsBoundaryAndNoUsage(t *testing.T) {
	events := runTurn(t, strings.Join([]string{
		`{"type":"system","subtype":"status","status":"compacting","session_id":"` + sessionID + `"}`,
		`{"type":"system","subtype":"status","status":null,"compact_result":"success","session_id":"` + sessionID + `"}`,
		initLine(`"Bash"`),
		`{"type":"system","subtype":"compact_boundary","session_id":"` + sessionID + `","compact_metadata":{"trigger":"manual","pre_tokens":33578,"post_tokens":9955}}`,
		`{"type":"user","message":{"role":"user","content":"This session is being continued from a previous conversation."},"session_id":"` + sessionID + `","isReplay":true}`,
		`{"type":"user","message":{"role":"user","content":"<local-command-stdout>Compacted </local-command-stdout>"},"session_id":"` + sessionID + `","isReplay":true}`,
		`{"type":"result","subtype":"success","is_error":false,"num_turns":0,"result":"","session_id":"` + sessionID + `","usage":{"input_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0},"modelUsage":{"claude-opus-5":{"contextWindow":200000}}}`,
	}, "\n"))
	if report := compactOf(events); report == nil || report.Outcome != assistant.CompactApplied {
		t.Fatalf("want the compaction applied, got %+v", events)
	}
	for _, ev := range events {
		if ev.Kind == assistant.EventUsage || ev.Kind == assistant.EventDelta || ev.Kind == assistant.EventError {
			t.Fatalf("a compaction sent %+v", ev)
		}
	}
}

// An empty session: claude says so as an assistant record of its own and
// ends the turn as a success.
func TestNothingToCompactIsReportedAsSuch(t *testing.T) {
	events := runTurn(t, strings.Join([]string{
		initLine(`"Bash"`),
		`{"type":"assistant","message":{"model":"<synthetic>","role":"assistant","usage":{"input_tokens":0,"output_tokens":0},"content":[{"type":"text","text":"Error: No messages to compact"}]},"parent_tool_use_id":null,"session_id":"` + sessionID + `"}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"","session_id":"` + sessionID + `","modelUsage":{}}`,
	}, "\n"))
	if report := compactOf(events); report == nil || report.Outcome != assistant.CompactNothing {
		t.Fatalf("want nothing to compact, got %+v", events)
	}
	if errorOf(events) != nil {
		t.Fatalf("nothing to compact failed the turn: %v", errorOf(events))
	}
}

// Claude 2.1.330 says it in other words, right after a compaction and on a
// session whose only turn failed, and its status record says it first.
func TestNothingToCompactIsReadInEitherWording(t *testing.T) {
	events := runTurn(t, strings.Join([]string{
		`{"type":"system","subtype":"status","status":"compacting","session_id":"` + sessionID + `"}`,
		`{"type":"system","subtype":"status","status":null,"compact_result":"failed","compact_error":"Not enough messages to compact.","session_id":"` + sessionID + `"}`,
		initLine(`"Bash"`),
		`{"type":"assistant","message":{"model":"<synthetic>","role":"assistant","usage":{"input_tokens":0,"output_tokens":0},"content":[{"type":"text","text":"Not enough messages to compact."}]},"parent_tool_use_id":null,"session_id":"` + sessionID + `","local_command_run":true,"local_command_source":"compact"}`,
		`{"type":"result","subtype":"success","is_error":false,"num_turns":0,"result":"Not enough messages to compact.","session_id":"` + sessionID + `","modelUsage":{}}`,
	}, "\n"))
	for _, ev := range events {
		if ev.Kind == assistant.EventCompact && (ev.Compact == nil || ev.Compact.Outcome != assistant.CompactNothing) {
			t.Fatalf("want every report to say nothing to compact, got %+v", ev.Compact)
		}
	}
	if report := compactOf(events); report == nil || errorOf(events) != nil {
		t.Fatalf("want nothing to compact and no failure, got %+v", events)
	}
}

// A compaction claude's status record says failed for another reason fails
// with claude's words; one it says succeeded is applied.
func TestTheStatusRecordSaysHowTheCompactionEnded(t *testing.T) {
	failed := runTurn(t, `{"type":"system","subtype":"status","status":null,"compact_result":"failed","compact_error":"Prompt is too long","session_id":"`+sessionID+`"}`)
	if report := compactOf(failed); report == nil || report.Outcome != "" || report.Reason != "Prompt is too long" {
		t.Fatalf("a failed compaction reads %+v", report)
	}
	applied := runTurn(t, `{"type":"system","subtype":"status","status":null,"compact_result":"success","session_id":"`+sessionID+`"}`)
	if report := compactOf(applied); report == nil || report.Outcome != assistant.CompactApplied {
		t.Fatalf("an applied compaction reads %+v", report)
	}
	working := runTurn(t, `{"type":"system","subtype":"status","status":"compacting","session_id":"`+sessionID+`"}`)
	if report := compactOf(working); report != nil {
		t.Fatalf("a status without a result reported %+v", report)
	}
}

// A normal turn sends no report: what decides a compaction is the CLI's own
// record, never words.
func TestAnAnswerIsNoCompaction(t *testing.T) {
	events := runTurn(t, strings.Join([]string{
		initLine(`"Bash"`),
		deltaLine("The /compact command is a built-in CLI command."),
		resultLine(sessionID),
	}, "\n"))
	if report := compactOf(events); report != nil {
		t.Fatalf("an answer reported a compaction: %+v", report)
	}
}
