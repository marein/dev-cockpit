package claude

import (
	"strings"
	"testing"

	"github.com/local/dev-cockpit/internal/coder"
)

const (
	userLine      = `{"type":"user","sessionId":"s1","cwd":"/projects/demo","timestamp":"2026-07-26T10:00:00Z","message":{"role":"user","content":"write the README"}}`
	toolUseLine   = `{"type":"assistant","sessionId":"s1","cwd":"/projects/demo","timestamp":"2026-07-26T10:00:01Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Write","input":{}}]}}`
	toolResultLn  = `{"type":"user","sessionId":"s1","cwd":"/projects/demo","timestamp":"2026-07-26T10:00:02Z","message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}`
	answerLine    = `{"type":"assistant","sessionId":"s1","cwd":"/projects/demo","timestamp":"2026-07-26T10:00:03Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"hm"},{"type":"text","text":"The README is written."}]}}`
	draftLine     = `{"type":"last-prompt","lastPrompt":"Escape soll neutral sein, nimm es raus","leafUuid":"u1","sessionId":"s1"}`
	queuedLine    = `{"type":"queue-operation","operation":"enqueue","timestamp":"2026-07-26T10:00:04Z","sessionId":"s1","content":"and now the tests"}`
	modeLine      = `{"type":"mode","mode":"normal","sessionId":"s1"}`
	sidechainLine = `{"type":"assistant","isSidechain":true,"sessionId":"s1","cwd":"/projects/demo","timestamp":"2026-07-26T10:00:05Z","message":{"role":"assistant","content":[{"type":"text","text":"subagent talking"}]}}`
)

// A turn that ended is visible in the transcript: the coder's own answer is the
// last thing recorded. Nobody has to compare two screenshots for that.
func TestATranscriptSaysWhenTheTurnIsOver(t *testing.T) {
	root := t.TempDir()
	writeTranscript(t, root, "demo", "s1", userLine, toolUseLine, toolResultLn, answerLine)
	r := &sessionRepository{stateRoot: root}

	activity, err := r.activity("s1", 0, coder.ActivityBudget)
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	if !activity.Finished {
		t.Fatalf("want the turn reported as over:\n%s", activity.Text)
	}
	if activity.Screen {
		t.Fatal("a transcript is not a screen")
	}
	for _, want := range []string{"user: write the README", "coder ran Write", "coder: The README is written."} {
		if !strings.Contains(activity.Text, want) {
			t.Fatalf("the reading misses %q:\n%s", want, activity.Text)
		}
	}
}

// A turn still running ends on something the coder owes an answer to: a tool it
// called, a result it has not read yet, or a prompt it just received.
func TestATranscriptSaysWhenTheCoderIsStillWorking(t *testing.T) {
	root := t.TempDir()
	r := &sessionRepository{stateRoot: root}

	cases := []struct {
		name  string
		lines []string
	}{
		{"a tool it still has to run", []string{userLine, toolUseLine}},
		{"a result it has not answered", []string{userLine, toolUseLine, toolResultLn}},
		{"a prompt it just received", []string{answerLine, userLine}},
		{"a subagent working for it", []string{userLine, toolUseLine, sidechainLine}},
		{"nothing recorded yet", []string{modeLine}},
	}
	for _, tc := range cases {
		writeTranscript(t, root, "demo", "s1", tc.lines...)
		activity, err := r.activity("s1", 0, coder.ActivityBudget)
		if err != nil {
			t.Fatalf("%s: activity: %v", tc.name, err)
		}
		if activity.Finished {
			t.Fatalf("%s: want the coder reported as working:\n%s", tc.name, activity.Text)
		}
	}
}

// The one that started this: claude renders its own suggestion for the next
// prompt into the input line and records it as `last-prompt`, and queued input
// sits in the transcript too. Neither is a message from the user, so neither may
// count as one: not as text a check reads, and not as movement that makes a
// finished turn look busy.
func TestACodersOwnDraftIsNotAUserMessage(t *testing.T) {
	root := t.TempDir()
	writeTranscript(t, root, "demo", "s1", userLine, answerLine, draftLine, queuedLine, modeLine)
	r := &sessionRepository{stateRoot: root}

	activity, err := r.activity("s1", 0, coder.ActivityBudget)
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	if !activity.Finished {
		t.Fatalf("a draft in the input line ended the turn:\n%s", activity.Text)
	}
	if strings.Contains(activity.Text, "Escape soll neutral sein") {
		t.Fatalf("the draft reached the reading:\n%s", activity.Text)
	}
	if strings.Contains(activity.Text, "and now the tests") {
		t.Fatalf("queued input reached the reading:\n%s", activity.Text)
	}
	if !strings.HasSuffix(strings.TrimSpace(activity.Text), "coder: The README is written.") {
		t.Fatalf("want the coder's answer last:\n%s", activity.Text)
	}
}

// A subagent's conversation belongs to the subagent. It is recorded in the same
// file, and reading it as the session's own words would report a job on what
// somebody else said.
func TestASubagentsWordsAreNotTheSessionsWords(t *testing.T) {
	root := t.TempDir()
	writeTranscript(t, root, "demo", "s1", userLine, sidechainLine, answerLine)
	r := &sessionRepository{stateRoot: root}

	activity, err := r.activity("s1", 0, coder.ActivityBudget)
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	if strings.Contains(activity.Text, "subagent talking") {
		t.Fatalf("the sidechain reached the reading:\n%s", activity.Text)
	}
}

// The transcript is found by the session id alone, in the store the coder owns.
// A session that has none is an error a check can report, not a guess.
func TestASessionWithoutATranscriptIsAnError(t *testing.T) {
	r := &sessionRepository{stateRoot: t.TempDir()}
	if _, err := r.activity("nothing-here", 0, coder.ActivityBudget); err == nil {
		t.Fatal("want an error for a session with no transcript")
	}
	if _, err := r.activity("", 0, coder.ActivityBudget); err == nil {
		t.Fatal("want an error for an empty session id")
	}
	if _, err := r.activity("../escape", 0, coder.ActivityBudget); err == nil {
		t.Fatal("want an error for an id that is a path")
	}
}

// The reading is bounded: a long session costs the same as a short one.
func TestAReadingKeepsTheEndAndStaysBounded(t *testing.T) {
	root := t.TempDir()
	long := strings.Repeat("x", 2000)
	var lines []string
	for i := 0; i < 60; i++ {
		lines = append(lines, `{"type":"assistant","sessionId":"s1","cwd":"/projects/demo","message":{"role":"assistant","content":[{"type":"text","text":"`+long+`"}]}}`)
	}
	lines = append(lines, answerLine)
	writeTranscript(t, root, "demo", "s1", lines...)
	r := &sessionRepository{stateRoot: root}

	activity, err := r.activity("s1", 0, coder.ActivityBudget)
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	if len([]rune(activity.Text)) > coder.ActivityBudget {
		t.Fatalf("the reading is %d runes, want at most %d", len([]rune(activity.Text)), coder.ActivityBudget)
	}
	if !strings.HasSuffix(activity.Text, "coder: The README is written.") {
		t.Fatalf("want the end kept:\n%s", activity.Text[len(activity.Text)-80:])
	}
}

// A session that has run for days must not cost a check its whole history: only
// the end of the transcript is read.
func TestOnlyTheEndOfALongTranscriptIsRead(t *testing.T) {
	root := t.TempDir()
	filler := `{"type":"assistant","sessionId":"s1","cwd":"/projects/demo","message":{"role":"assistant","content":[{"type":"text","text":"` + strings.Repeat("old ", 200) + `"}]}}`
	var lines []string
	for len(strings.Join(lines, "\n")) < activityTailBytes*3 {
		lines = append(lines, filler)
	}
	lines = append(lines, userLine, answerLine)
	writeTranscript(t, root, "demo", "s1", lines...)
	r := &sessionRepository{stateRoot: root}

	activity, err := r.activity("s1", 0, coder.ActivityBudget)
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	if !activity.Finished {
		t.Fatalf("the end of the file was not read:\n%s", activity.Text)
	}
	if !strings.HasSuffix(strings.TrimSpace(activity.Text), "coder: The README is written.") {
		t.Fatalf("want the last message at the end:\n%s", activity.Text)
	}
	if !strings.Contains(activity.Text, "user: write the README") {
		t.Fatalf("want the last exchange kept:\n%s", activity.Text)
	}
}

// The newest message is the one a check judges against, so it gets the whole
// budget first: a 3000 rune final report arrives whole in the default reading,
// the older lines keep their per-line cut and share what is left, and the
// oldest fall off first. Only a newest message over the whole budget is cut,
// and the reading stays within the budget, only a cut notice may stand past it.
func TestTheNewestMessageGetsTheBudgetFirst(t *testing.T) {
	root := t.TempDir()
	said := func(text string) string {
		return `{"type":"assistant","sessionId":"s1","cwd":"/projects/demo","message":{"role":"assistant","content":[{"type":"text","text":"` + text + `"}]}}`
	}
	oldLong := func(i int) string {
		return "old-" + string(rune('0'+i)) + "-" + strings.Repeat("y", 800)
	}
	report := "final-" + strings.Repeat("r", 2994)
	var lines []string
	for i := 0; i < 10; i++ {
		lines = append(lines, said(oldLong(i)))
	}
	lines = append(lines, said(report))
	writeTranscript(t, root, "demo", "s1", lines...)
	r := &sessionRepository{stateRoot: root}

	activity, err := r.activity("s1", 0, coder.ActivityBudget)
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	if !strings.Contains(activity.Text, report) {
		t.Fatalf("a 3000 rune final report has to arrive whole:\n%.200s", activity.Text)
	}
	if strings.Contains(activity.Text, oldLong(9)) || !strings.Contains(activity.Text, "… [cut: 600 of ") {
		t.Fatalf("an older line has to keep the per-line cut, said visibly:\n%.200s", activity.Text)
	}
	if !strings.Contains(activity.Text, "old-9-") {
		t.Fatalf("what still fits next to the report is the newest of the older lines:\n%.200s", activity.Text)
	}
	if strings.Contains(activity.Text, "old-0-") {
		t.Fatalf("the oldest lines have to fall off first:\n%.200s", activity.Text)
	}
	if got := len([]rune(activity.Text)); got > coder.ActivityBudget {
		t.Fatalf("the reading is %d runes, the budget has to hold", got)
	}

	// A newest message over the whole budget is the one case that is cut, and
	// the cut says how much of the message is shown. Only that notice may
	// stand past the budget.
	writeTranscript(t, root, "demo", "s1", said("final-"+strings.Repeat("r", 4500)))
	activity, err = r.activity("s1", 0, coder.ActivityBudget)
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	if !strings.Contains(activity.Text, "… [cut: 4000 of ") || !strings.Contains(activity.Text, "runes shown, use --full for the whole message]") {
		t.Fatalf("a newest message over the whole budget has to say what was cut:\n%.200s", activity.Text)
	}
	if got := len([]rune(activity.Text)); got > coder.ActivityBudget+80 {
		t.Fatalf("the reading is %d runes, the budget has to hold up to the cut notice", got)
	}
}

// --full lifts the cap: every message arrives whole, and it composes with
// --entries, so one entry full is the whole last message however long it is.
func TestAFullReadingIsUncut(t *testing.T) {
	root := t.TempDir()
	said := func(text string) string {
		return `{"type":"assistant","sessionId":"s1","cwd":"/projects/demo","message":{"role":"assistant","content":[{"type":"text","text":"` + text + `"}]}}`
	}
	older := "older-" + strings.Repeat("y", 1500)
	report := "final-" + strings.Repeat("r", 9000)
	writeTranscript(t, root, "demo", "s1", said(older), said(report))
	r := &sessionRepository{stateRoot: root}

	full, err := r.activity("s1", 0, 0)
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	for _, want := range []string{older, report} {
		if !strings.Contains(full.Text, want) {
			t.Fatalf("a full reading has to carry every message whole:\n%.200s", full.Text)
		}
	}
	if strings.Contains(full.Text, "[cut:") {
		t.Fatalf("a full reading must not cut:\n%.200s", full.Text)
	}

	one, err := r.activity("s1", 1, 0)
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	if !strings.Contains(one.Text, report) || strings.Contains(one.Text, "older-") {
		t.Fatalf("one entry full has to be exactly the whole last message:\n%.200s", one.Text)
	}
}

// One entry is only the newest message, and it carries it whole up to the
// budget. This is what replaces the detour of a coder writing its report into
// a file.
func TestOneEntryKeepsOnlyTheNewestMessage(t *testing.T) {
	root := t.TempDir()
	long := strings.TrimSpace(strings.Repeat("report ", 300))
	last := `{"type":"assistant","sessionId":"s1","cwd":"/projects/demo","message":{"role":"assistant","content":[{"type":"text","text":"` + long + `"}]}}`
	writeTranscript(t, root, "demo", "s1", userLine, answerLine, last)
	r := &sessionRepository{stateRoot: root}

	whole, err := r.activity("s1", 1, coder.ActivityBudget)
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	if !strings.Contains(whole.Text, long) {
		t.Fatalf("one entry has to carry the whole message:\n%s", whole.Text)
	}
	if strings.Contains(whole.Text, "write the README") {
		t.Fatalf("one entry must keep only the last message:\n%s", whole.Text)
	}
}

// The capability is what the manager asks for, so the coder has to carry it.
func TestClaudeReportsSessionActivity(t *testing.T) {
	root := t.TempDir()
	writeTranscript(t, root, "demo", "s1", userLine, answerLine)
	c := &Coder{sessions: &sessionRepository{stateRoot: root}}

	activity, err := c.SessionActivity("s1", 0, coder.ActivityBudget)
	if err != nil {
		t.Fatalf("SessionActivity: %v", err)
	}
	if !activity.Finished || activity.Screen {
		t.Fatalf("want a finished transcript reading, got %+v", activity)
	}
}

// The case the owner saw: a coder writes a sentence and then opens a question,
// and the tool call of that question only reaches the transcript once it was
// answered. What is recorded is a turn that ends on the coder's own words, which
// reads as finished. The transcript alone cannot tell a waiting coder from a
// finished one, which is why the screen decides that one question, see
// coder.Activity.Question.
func TestAnOpenQuestionIsNotVisibleInTheTranscript(t *testing.T) {
	root := t.TempDir()
	preamble := `{"type":"assistant","sessionId":"s1","cwd":"/projects/demo","message":{"role":"assistant","content":[{"type":"text","text":"I need one decision before I go on."}]}}`
	writeTranscript(t, root, "demo", "s1", userLine, preamble)
	r := &sessionRepository{stateRoot: root}

	activity, err := r.activity("s1", 0, coder.ActivityBudget)
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	if !activity.Finished {
		t.Fatal("the transcript is expected to look finished here, that is the whole problem")
	}
}
