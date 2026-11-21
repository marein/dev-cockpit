package assistant

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/approval"
	"github.com/marein/dev-cockpit/internal/statefile"
)

// compactingRunner answers a compaction with the events the test names and a
// turn with words of its own.
func compactingRunner(compaction ...Event) *fakeRunner {
	r := &fakeRunner{exists: true}
	r.answer = func(req TurnRequest) []Event {
		if req.Prompt == fakeCompactPrompt {
			return compaction
		}
		return []Event{{Kind: EventDelta, Text: "answer to " + req.Prompt}}
	}
	return r
}

func compactReport(outcome, reason string) Event {
	return Event{Kind: EventCompact, Compact: &CompactReport{Outcome: outcome, Reason: reason}}
}

func withAnswer(t *testing.T, svc *Service) Instance {
	t.Helper()
	created, err := svc.create("claude")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := svc.Send(created.ID, "first", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitIdle(t, svc, created.ID)
	c, _ := svc.Get(created.ID)
	return c
}

func compacted(t *testing.T, svc *Service, id string) Message {
	t.Helper()
	if _, err := svc.Compact(id); err != nil {
		t.Fatalf("compact: %v", err)
	}
	return waitIdle(t, svc, id)
}

// What the service hands the coder's compaction is no prompt at all, so
// neither the notes nor a queue can stand in front of the CLI's command, and
// the notes written before it reach the next real turn.
func TestACompactionCarriesNoPromptAndTheNotesWaitForTheNextTurn(t *testing.T) {
	runner := compactingRunner(compactReport(CompactApplied, ""))
	svc, _, _ := newTestService(t, runner)
	c := withAnswer(t, svc)
	svc.RecordApproval(ApprovalReport{Owner: c.ID, What: "Delete project shop", Outcome: approval.Outcome{Verdict: approval.Done, Text: "deleted", Quiet: true}})

	line := compacted(t, svc, c.ID)
	if line.Role != RoleCockpit || line.Compact == nil || line.Compact.Outcome != CompactApplied || line.State != StateComplete || line.Content != "" || line.Error != "" {
		t.Fatalf("the compaction line reads %+v", line)
	}
	if len(runner.compactions) != 1 || runner.compactions[0].Prompt != "" || !runner.compactions[0].Resume || runner.compactions[0].SessionID != c.NativeSessionID {
		t.Fatalf("the compaction was asked with %+v", runner.compactions)
	}

	if _, err := svc.Send(c.ID, "next", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitIdle(t, svc, c.ID)
	turns := runner.turns()
	prompt := turns[len(turns)-1].Prompt
	if !strings.HasPrefix(prompt, "Since your last answer the cockpit wrote 1 note into this thread") || !strings.HasSuffix(prompt, "\n\nnext") {
		t.Fatalf("the next turn's prompt lost the note: %q", prompt)
	}
}

// A message sent while the compaction runs waits like one sent during an
// answer, and goes out on its own once the compaction is through.
func TestAMessageSentWhileCompactingWaitsAndGoesAlone(t *testing.T) {
	runner := compactingRunner(compactReport(CompactApplied, ""))
	release := make(chan struct{})
	runner.hold = func(req TurnRequest) chan struct{} {
		if req.Prompt == fakeCompactPrompt {
			return release
		}
		return nil
	}
	svc, _, _ := newTestService(t, runner)
	c := withAnswer(t, svc)
	frames := collectFrames(svc, c.ID)

	if _, err := svc.Compact(c.ID); err != nil {
		t.Fatalf("compact: %v", err)
	}
	if svc.RunningSince(c.ID).IsZero() || !svc.List()[0].Running {
		t.Fatal("a running compaction does not count as working")
	}
	if snapshot, running, _, cancel := svc.Subscribe(c.ID); !running || !snapshot.Compact {
		cancel()
		t.Fatalf("a page connecting now hears %+v", snapshot)
	} else {
		cancel()
	}
	run, err := svc.Send(c.ID, "meanwhile", nil)
	if err != nil || !run.Queued {
		t.Fatalf("the message did not wait: %+v, %v", run, err)
	}
	if _, err := svc.Compact(c.ID); err == nil {
		t.Fatal("a second compaction started beside the first")
	}
	close(release)
	waitFor(t, "the waiting message to go out", func() bool { return len(runner.turns()) == 3 })
	final := waitIdle(t, svc, c.ID)
	if prompt := runner.turns()[2].Prompt; prompt != "meanwhile" {
		t.Fatalf("the waiting message went out as %q", prompt)
	}
	if final.Content != "answer to meanwhile" {
		t.Fatalf("the last answer is %q", final.Content)
	}
	got, _ := svc.Get(c.ID)
	roles := []string{}
	for _, m := range got.Messages {
		roles = append(roles, string(m.Role))
	}
	if strings.Join(roles, " ") != "user assistant cockpit user assistant" {
		t.Fatalf("the thread stands as %v", roles)
	}

	var start, end *StreamEvent
	for _, f := range frames.stop() {
		if f.MessageID != got.Messages[2].ID {
			continue
		}
		f := f
		switch f.Kind {
		case FrameStart:
			start = &f
		case FrameEnd:
			end = &f
		}
	}
	if start == nil || !start.Compact || end == nil || !end.Compact || end.State != string(StateComplete) {
		t.Fatalf("the compaction's frames read %+v and %+v", start, end)
	}
}

func TestACompactionIsRefusedWhileWorkingAndBeforeTheFirstAnswer(t *testing.T) {
	runner := compactingRunner(compactReport(CompactApplied, ""))
	release := make(chan struct{})
	runner.hold = func(req TurnRequest) chan struct{} {
		if req.Prompt == "slow" {
			return release
		}
		return nil
	}
	svc, store, _ := newTestService(t, runner)
	fresh, _ := svc.create("claude")
	if _, err := svc.Compact(fresh.ID); err == nil || !strings.Contains(err.Error(), "nothing to compact") {
		t.Fatalf("an assistant without an answer compacted: %v", err)
	}

	if _, err := svc.Send(fresh.ID, "slow", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	if _, err := svc.Compact(fresh.ID); err == nil || !strings.Contains(err.Error(), "still working") {
		t.Fatalf("a running turn did not refuse the compaction: %v", err)
	}
	close(release)
	waitIdle(t, svc, fresh.ID)

	c, _ := svc.Get(fresh.ID)
	c.Messages = append(c.Messages, Message{ID: statefile.NewID(), Role: RoleUser, Content: "waits", State: StateQueued, CreatedAt: time.Now().UTC()})
	store.Save(c)
	if _, err := svc.Compact(fresh.ID); err == nil || !strings.Contains(err.Error(), "waiting") {
		t.Fatalf("a waiting message did not refuse the compaction: %v", err)
	}
	if len(runner.compactions) != 0 {
		t.Fatalf("a refused compaction reached the coder: %+v", runner.compactions)
	}
}

type plainCoders struct{ runner *fakeRunner }

func (c plainCoders) Available() []CoderInfo {
	return []CoderInfo{{ID: "opencode", Label: "OpenCode", Runner: struct{ Runner }{c.runner}}}
}

func TestACoderWithoutCompactionIsNotOffered(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{exists: true, dir: t.TempDir()}
	svc := newService(NewStore(dir), NewRunStore(dir), plainCoders{runner: runner}, &fakeWorkdirs{root: t.TempDir()})
	t.Cleanup(func() { quiesce(t, svc, nil) })
	if svc.Compacts("opencode") || svc.Compacts("gone") {
		t.Fatal("a coder without compaction is offered one")
	}
	created, _ := svc.create("opencode")
	created.Messages = []Message{{ID: "a", Role: RoleAssistant, State: StateComplete}}
	svc.store.Save(created)
	if _, err := svc.Compact(created.ID); err != errCompactUnsupported {
		t.Fatalf("want the coder refused, got %v", err)
	}
}

// The line says what the coder's record says, and only that: an answer in
// words, an error and a stop never stand as a compaction, and copilot's exit
// code after nothing to compact is no failure.
func TestACompactionWritesHowItWent(t *testing.T) {
	for _, tc := range []struct {
		name    string
		events  []Event
		state   State
		outcome string
		error   string
	}{
		{"applied", []Event{compactReport(CompactApplied, "")}, StateComplete, CompactApplied, ""},
		{"nothing", []Event{compactReport(CompactNothing, "")}, StateComplete, CompactNothing, ""},
		{"nothing and a failed run", []Event{compactReport(CompactNothing, "Nothing to compact."), {Kind: EventError, Err: errFinish}}, StateComplete, CompactNothing, ""},
		{"refused with words", []Event{compactReport("", "Compaction failed: the summary was too long")}, StateFailed, "", "The coder reported no compaction. The coder said: Compaction failed: the summary was too long"},
		{"answered in words", []Event{{Kind: EventDelta, Text: "Sure, here is a summary."}}, StateFailed, "", "The coder reported no compaction. The coder said: Sure, here is a summary."},
		{"failed", []Event{{Kind: EventError, Err: errFinish}}, StateFailed, "", "The coder could not finish this answer."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, _ := newTestService(t, compactingRunner(tc.events...))
			c := withAnswer(t, svc)
			line := compacted(t, svc, c.ID)
			if line.State != tc.state || line.Compact == nil || line.Compact.Outcome != tc.outcome || line.Error != tc.error || line.Content != "" {
				t.Fatalf("the line reads state %s, compact %+v, error %q, content %q", line.State, line.Compact, line.Error, line.Content)
			}
		})
	}
}

var errFinish = errors.New("The coder could not finish this answer.")

func TestAStoppedCompactionSaysSo(t *testing.T) {
	runner := compactingRunner()
	svc, _, _ := newTestService(t, runner)
	c := withAnswer(t, svc)
	runner.mu.Lock()
	runner.block = make(chan struct{})
	runner.after = []Event{compactReport(CompactApplied, "")}
	runner.mu.Unlock()
	if _, err := svc.Compact(c.ID); err != nil {
		t.Fatalf("compact: %v", err)
	}
	if err := svc.Cancel(c.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	line := waitIdle(t, svc, c.ID)
	if line.State != StateCancelled || line.Error != "" || line.Compact == nil || line.Compact.Outcome != "" {
		t.Fatalf("the stopped compaction reads %+v", line)
	}
}

// The gauge never keeps a number that is not the context's any more: a
// reading that came with the compaction is the new one, a compaction without
// one leaves the old one pending until the next turn reads it, and a
// compaction that did not go through leaves it standing, whatever reading its
// run carried: copilot's last record of a stopped one names no model.
func TestTheGaugeAfterACompaction(t *testing.T) {
	usage := func(tokens int) Event {
		return Event{Kind: EventUsage, Usage: &ContextUsage{Model: "m", Tokens: tokens, Window: 100_000}}
	}
	runner := &fakeRunner{exists: true}
	var compaction []Event
	runner.answer = func(req TurnRequest) []Event {
		if req.Prompt == fakeCompactPrompt {
			return compaction
		}
		return []Event{{Kind: EventDelta, Text: "words"}, usage(50_000)}
	}
	svc, _, _ := newTestService(t, runner)
	c := withAnswer(t, svc)
	context := func() ContextUsage {
		got, _ := svc.Get(c.ID)
		return *got.Context
	}
	if got := context(); got.Percent() != 50 {
		t.Fatalf("the turn's reading is %+v", got)
	}

	compaction = []Event{compactReport("", "no"), {Kind: EventUsage, Usage: &ContextUsage{Tokens: 34_485}}}
	compacted(t, svc, c.ID)
	if got := context(); got.Pending || got.Percent() != 50 || got.Window != 100_000 {
		t.Fatalf("a compaction that did not go through moved the gauge: %+v", got)
	}

	frames := collectFrames(svc, c.ID)
	compaction = []Event{compactReport(CompactApplied, "")}
	compacted(t, svc, c.ID)
	if got := context(); !got.Pending || got.Known() || got.Percent() != 0 || got.Window != 100_000 {
		t.Fatalf("a compaction without a reading kept a number: %+v", got)
	}
	ended := false
	for _, f := range frames.stop() {
		ended = ended || (f.Kind == FrameEnd && f.ContextPending && f.Context == 0)
	}
	if !ended {
		t.Fatal("the end frame did not say the gauge waits for the next turn")
	}

	if _, err := svc.Send(c.ID, "again", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitIdle(t, svc, c.ID)
	if got := context(); got.Pending || got.Percent() != 50 {
		t.Fatalf("the next turn did not read the gauge again: %+v", got)
	}

	compaction = []Event{compactReport(CompactApplied, ""), usage(20_000)}
	compacted(t, svc, c.ID)
	if got := context(); got.Pending || got.Percent() != 20 {
		t.Fatalf("the compaction's own reading did not land: %+v", got)
	}
}

// A compaction outlives the server like a turn: the next one reads it as a
// compaction to its end, writes its line once and lets go of the assistant.
func TestACompactionSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	runner := compactingRunner()
	svc, _, _, runs := newTestServiceIn(t, dir, runner)
	c := withAnswer(t, svc)

	runner.mu.Lock()
	runner.block = make(chan struct{})
	runner.after = []Event{compactReport(CompactApplied, "")}
	runner.mu.Unlock()
	rec := RunRecord{ID: statefile.NewID(), Kind: RunCompact, Instance: c.ID, MessageID: statefile.NewID(), CoderID: "claude", SessionID: c.NativeSessionID, StartedAt: time.Now().UTC()}
	c.Messages = append(c.Messages, Message{ID: rec.MessageID, Role: RoleCockpit, RunID: rec.ID, State: StateStreaming, Compact: &Compaction{}, CreatedAt: time.Now().UTC()})
	svc.store.Save(c)
	if _, err := svc.launch(&rec, runner, TurnRequest{SessionID: c.NativeSessionID, Resume: true, Workdir: mustWorkdir(t, svc, c.ID)}); err != nil {
		t.Fatalf("launch: %v", err)
	}

	restarted, _, _, _ := newTestServiceIn(t, dir, runner)
	frames := collectFrames(restarted, c.ID)
	restarted.Recover()
	waitFor(t, "the restarted server to pick the compaction up", func() bool {
		return !restarted.RunningSince(c.ID).IsZero()
	})
	close(runner.block)
	line := waitIdle(t, restarted, c.ID)
	if line.ID != rec.MessageID || line.State != StateComplete || line.Compact == nil || line.Compact.Outcome != CompactApplied {
		t.Fatalf("the compaction ended as %+v", line)
	}
	got, _ := restarted.Get(c.ID)
	lines := 0
	for _, m := range got.Messages {
		if m.Compact != nil {
			lines++
		}
	}
	if lines != 1 || len(runs.List()) != 0 || restarted.List()[0].Running {
		t.Fatalf("after the restart: %d lines, %d runs, %+v", lines, len(runs.List()), restarted.List()[0])
	}
	compactFrames := 0
	for _, f := range frames.stop() {
		if f.MessageID == rec.MessageID && f.Compact && (f.Kind == FrameStart || f.Kind == FrameEnd) {
			compactFrames++
		}
	}
	if compactFrames != 2 {
		t.Fatalf("want the restarted stream to open and close the compaction, got %d frames", compactFrames)
	}
}

func TestACompactionLostToARestartIsInterrupted(t *testing.T) {
	dir := t.TempDir()
	runner := compactingRunner()
	svc, _, _, _ := newTestServiceIn(t, dir, runner)
	c := withAnswer(t, svc)
	c.Messages = append(c.Messages, Message{ID: statefile.NewID(), Role: RoleCockpit, RunID: "gone", State: StateStreaming, Compact: &Compaction{}, CreatedAt: time.Now().UTC()})
	svc.store.Save(c)
	if !svc.List()[0].Running {
		t.Fatal("a streaming compaction does not count as running in the index")
	}

	restarted, _, _, _ := newTestServiceIn(t, dir, runner)
	restarted.Recover()
	line := lastMessage(t, restarted, c.ID)
	if line.State != StateInterrupted || line.Error != compactInterrupted {
		t.Fatalf("the lost compaction reads %+v", line)
	}
	if entry := restarted.List()[0]; entry.Running || entry.Unfinished {
		t.Fatalf("the index reads %+v", entry)
	}
}
