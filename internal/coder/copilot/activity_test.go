package copilot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/coder"
)

const (
	sessionStartEvent = `{"type":"session.start","data":{"sessionId":"s1"},"id":"e0","timestamp":"2026-07-23T15:57:36.925Z"}`
	userEvent         = `{"type":"user.message","data":{"content":"write the README","transformedContent":"<current_datetime>...</current_datetime>\n\nwrite the README"},"id":"e1"}`
	turnStartEvent    = `{"type":"assistant.turn_start","data":{"turnId":"0"},"id":"e2"}`
	toolEvent         = `{"type":"tool.execution_start","data":{"toolCallId":"c1","toolName":"write"},"id":"e3"}`
	toolDoneEvent     = `{"type":"tool.execution_complete","data":{"toolCallId":"c1"},"id":"e4"}`
	answerEvent       = `{"type":"assistant.message","data":{"messageId":"m1","content":"The README is written.","phase":"final_answer"},"id":"e5"}`
	turnEndEvent      = `{"type":"assistant.turn_end","data":{"turnId":"0"},"id":"e6"}`
)

func writeEvents(t *testing.T, root, id string, lines ...string) {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// A turn that ended is copilot's own record: assistant.turn_end stands in the
// log. Nobody has to compare two screenshots for that.
func TestAnEventLogSaysWhenTheTurnIsOver(t *testing.T) {
	root := t.TempDir()
	writeEvents(t, root, "s1", sessionStartEvent, userEvent, turnStartEvent, toolEvent, toolDoneEvent, answerEvent, turnEndEvent)
	r := &sessionRepository{stateRoot: root}

	activity, err := r.activity("s1", 0, coder.ActivityBudget)
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	if !activity.Finished {
		t.Fatalf("want the turn reported as over:\n%s", activity.Text)
	}
	if activity.Screen {
		t.Fatal("an event log is not a screen")
	}
	for _, want := range []string{"user: write the README", "coder ran write", "coder: The README is written."} {
		if !strings.Contains(activity.Text, want) {
			t.Fatalf("the reading misses %q:\n%s", want, activity.Text)
		}
	}
	if strings.Contains(activity.Text, "current_datetime") {
		t.Fatalf("the transformed prompt is bookkeeping, not the user's words:\n%s", activity.Text)
	}
}

// A turn still running ends on something that is not its end marker: a prompt
// that just arrived, a turn that just started, or a tool still running.
func TestAnEventLogSaysWhenTheCoderIsStillWorking(t *testing.T) {
	root := t.TempDir()
	r := &sessionRepository{stateRoot: root}

	cases := []struct {
		name  string
		lines []string
	}{
		{"a prompt it just received", []string{sessionStartEvent, turnEndEvent, userEvent}},
		{"a turn it just started", []string{sessionStartEvent, userEvent, turnStartEvent}},
		{"a tool it is still running", []string{sessionStartEvent, userEvent, turnStartEvent, toolEvent}},
		{"nothing recorded yet", []string{sessionStartEvent}},
	}
	for _, tc := range cases {
		writeEvents(t, root, "s1", tc.lines...)
		activity, err := r.activity("s1", 0, coder.ActivityBudget)
		if err != nil {
			t.Fatalf("%s: activity: %v", tc.name, err)
		}
		if activity.Finished {
			t.Fatalf("%s: want the coder reported as working:\n%s", tc.name, activity.Text)
		}
	}
}

// The event log is found by the session id alone, in the store the coder owns.
// A session that has none is an error a check can report, not a guess.
func TestASessionWithoutAnEventLogIsAnError(t *testing.T) {
	r := &sessionRepository{stateRoot: t.TempDir()}
	if _, err := r.activity("nothing-here", 0, coder.ActivityBudget); err == nil {
		t.Fatal("want an error for a session with no event log")
	}
	if _, err := r.activity("", 0, coder.ActivityBudget); err == nil {
		t.Fatal("want an error for an empty session id")
	}
	if _, err := r.activity("../escape", 0, coder.ActivityBudget); err == nil {
		t.Fatal("want an error for an id that is a path")
	}
}

// The reading is bounded: a long session costs the same as a short one, and
// the end is what is kept.
func TestAReadingKeepsTheEndAndStaysBounded(t *testing.T) {
	root := t.TempDir()
	long := strings.Repeat("x", 2000)
	lines := []string{sessionStartEvent}
	for i := 0; i < 60; i++ {
		lines = append(lines, `{"type":"assistant.message","data":{"messageId":"m","content":"`+long+`"}}`)
	}
	lines = append(lines, answerEvent, turnEndEvent)
	writeEvents(t, root, "s1", lines...)
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

// The newest message gets the whole budget first, the same rule the claude
// reader applies: a 3000 rune final report arrives whole in the default
// reading, the older lines keep their per-line cut and the oldest fall off
// first, and only a newest message over the whole budget is cut.
func TestTheNewestMessageGetsTheBudgetFirst(t *testing.T) {
	root := t.TempDir()
	said := func(text string) string {
		return `{"type":"assistant.message","data":{"messageId":"m","content":"` + text + `"}}`
	}
	oldLong := func(i int) string {
		return "old-" + string(rune('0'+i)) + "-" + strings.Repeat("y", 800)
	}
	report := "final-" + strings.Repeat("r", 2994)
	lines := []string{sessionStartEvent}
	for i := 0; i < 10; i++ {
		lines = append(lines, said(oldLong(i)))
	}
	lines = append(lines, said(report), turnEndEvent)
	writeEvents(t, root, "s1", lines...)
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
	if !strings.Contains(activity.Text, "old-9-") || strings.Contains(activity.Text, "old-0-") {
		t.Fatalf("next to the report the newest older line fits, the oldest fall off:\n%.200s", activity.Text)
	}
	if got := len([]rune(activity.Text)); got > coder.ActivityBudget {
		t.Fatalf("the reading is %d runes, the budget has to hold", got)
	}

	writeEvents(t, root, "s1", sessionStartEvent, said("final-"+strings.Repeat("r", 4500)), turnEndEvent)
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

// --full lifts the cap here too: every message arrives whole, and it composes
// with --entries.
func TestAFullReadingIsUncut(t *testing.T) {
	root := t.TempDir()
	said := func(text string) string {
		return `{"type":"assistant.message","data":{"messageId":"m","content":"` + text + `"}}`
	}
	older := "older-" + strings.Repeat("y", 1500)
	report := "final-" + strings.Repeat("r", 9000)
	writeEvents(t, root, "s1", sessionStartEvent, said(older), said(report), turnEndEvent)
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

// One entry is only the newest message, carried whole up to the budget.
func TestOneEntryKeepsOnlyTheNewestMessage(t *testing.T) {
	root := t.TempDir()
	long := strings.TrimSpace(strings.Repeat("report ", 300))
	writeEvents(t, root, "s1", sessionStartEvent, userEvent, turnStartEvent,
		`{"type":"assistant.message","data":{"messageId":"m1","content":"`+long+`"}}`, turnEndEvent)
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

// Only the end of a long log is read: a session that has run for days must not
// cost a check its whole history.
func TestOnlyTheEndOfALongEventLogIsRead(t *testing.T) {
	root := t.TempDir()
	filler := `{"type":"assistant.message","data":{"messageId":"m","content":"` + strings.Repeat("old ", 200) + `"}}`
	var lines []string
	for len(strings.Join(lines, "\n")) < activityTailBytes*3 {
		lines = append(lines, filler)
	}
	lines = append(lines, userEvent, turnStartEvent, answerEvent, turnEndEvent)
	writeEvents(t, root, "s1", lines...)
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
