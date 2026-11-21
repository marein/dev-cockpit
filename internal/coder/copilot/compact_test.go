package copilot

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
	r := testRunner(t)
	cmd, err := r.CompactCommand(assistant.TurnRequest{SessionID: sessionID, Resume: true, Workdir: t.TempDir(), Prompt: "ignored", Model: "claude-sonnet-5"})
	if err != nil {
		t.Fatalf("command: %v", err)
	}
	if cmd.Name != "copilot" || len(cmd.Args) < 2 || cmd.Args[0] != "-p" || cmd.Args[1] != "/compact" {
		t.Fatalf("the compaction runs as %s %v", cmd.Name, cmd.Args)
	}
	argv := strings.Join(cmd.Args, " ")
	if !strings.Contains(argv, "--resume "+sessionID) || !strings.Contains(argv, "--model claude-sonnet-5") || strings.Contains(argv, "ignored") {
		t.Fatalf("the compaction runs as %s", argv)
	}
}

// The records of `copilot -p /compact` (GitHub Copilot CLI 1.0.114). The
// reading after an applied one comes from the session's shutdown record,
// which already holds the compacted size; a run that exits 1 sends none.
func TestACompactionReportsHowItWent(t *testing.T) {
	start := `{"type":"session.compaction_start","data":{"currentTokens":31448,"tokenLimit":200000,"trigger":"manual","execution":"direct"}}`
	for _, tc := range []struct {
		name     string
		complete string
		exit     string
		outcome  string
		reason   string
	}{
		{"applied", `{"type":"session.compaction_complete","data":{"success":true,"outcome":"applied","preCompactionTokens":31448,"postCompactionTokens":26580}}`, "0", assistant.CompactApplied, ""},
		{"nothing", `{"type":"session.compaction_complete","data":{"success":false,"error":"Nothing to compact.","outcome":"not_applicable","outcomeReason":"nothing_to_compact"}}`, "1", assistant.CompactNothing, "Nothing to compact."},
		{"failed", `{"type":"session.compaction_complete","data":{"success":false,"error":"The summary was too long.","outcome":"failed"}}`, "1", "", "The summary was too long."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := testRunner(t)
			turnEvents(t, r, `{"type":"session.shutdown","data":{"currentModel":"claude-sonnet-5","currentTokens":26583}}`)
			events := runTurnWith(t, r, strings.Join([]string{start, tc.complete, `{"type":"result","sessionId":"` + sessionID + `","exitCode":` + tc.exit + `}`}, "\n"))
			report := compactOf(events)
			if report == nil || report.Outcome != tc.outcome || report.Reason != tc.reason {
				t.Fatalf("want %s, got %+v in %+v", tc.name, report, events)
			}
			if textOf(events) != "" {
				t.Fatalf("a compaction streamed %q", textOf(events))
			}
			usage := false
			for _, ev := range events {
				usage = usage || (ev.Kind == assistant.EventUsage && ev.Usage.Tokens == 26583)
			}
			if usage != (tc.exit == "0") {
				t.Fatalf("the reading after the compaction is wrong: %+v", events)
			}
		})
	}
}

// A compaction stopped or killed before it ended leaves its start record as the
// last one that carries currentTokens, with the model under another key. The
// reading stays the one of the last run that shut down.
func TestAStoppedCompactionLeavesTheReadingAsItWas(t *testing.T) {
	r := testRunner(t)
	turnEvents(t, r,
		`{"type":"session.shutdown","data":{"shutdownType":"routine","currentModel":"claude-sonnet-5","currentTokens":34518}}`,
		`{"type":"session.resume","data":{"selectedModel":"claude-sonnet-5"}}`,
		`{"type":"session.compaction_start","data":{"model":"claude-sonnet-5","currentTokens":34485,"tokenLimit":200000,"trigger":"manual","execution":"direct"}}`,
	)
	events := runTurnWith(t, r, `{"type":"session.compaction_start","data":{"model":"claude-sonnet-5","currentTokens":34485}}`)
	for _, ev := range events {
		if ev.Kind == assistant.EventUsage && (ev.Usage.Tokens != 34518 || ev.Usage.Model != "claude-sonnet-5" || ev.Usage.Window == 0) {
			t.Fatalf("a stopped compaction read %+v", ev.Usage)
		}
	}
}
