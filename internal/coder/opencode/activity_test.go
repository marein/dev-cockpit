package opencode

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/coder"
)

// The row shapes mirror what `opencode db … --format json` answers for the
// activity query, recorded against opencode 1.18.23.

func userText(id, text string) map[string]any {
	return map[string]any{"message": id, "role": "user", "completed": 0, "failed": 0, "parttype": "text", "text": text, "synthetic": 0}
}

func coderText(id, text, finish string) map[string]any {
	return map[string]any{"message": id, "role": "assistant", "completed": 1, "failed": 0, "finish": finish, "parttype": "text", "text": text, "synthetic": 0}
}

func coderTool(id, tool, finish string, completed int) map[string]any {
	return map[string]any{"message": id, "role": "assistant", "completed": completed, "failed": 0, "finish": finish, "parttype": "tool", "tool": tool, "synthetic": 0}
}

func sessionOnly() map[string]any {
	return map[string]any{"message": nil, "role": nil, "completed": 0, "failed": 0}
}

// activityRepository answers the activity query from recorded rows.
func activityRepository(t *testing.T, rows []map[string]any) *sessionRepository {
	t.Helper()
	root := t.TempDir()
	touchDB(t, root)
	return &sessionRepository{
		dataRoot: root,
		query: func(sql string) ([]byte, error) {
			if !strings.Contains(sql, "AS message") {
				t.Fatalf("unexpected query: %s", sql)
			}
			data, err := json.Marshal(rows)
			if err != nil {
				t.Fatal(err)
			}
			return data, nil
		},
	}
}

// A turn that ended is opencode's own record: the newest message is the
// coder's answer, its completion time is written, and its finish reason does
// not hand on into a tool call.
func TestTheRecordSaysWhenTheTurnIsOver(t *testing.T) {
	r := activityRepository(t, []map[string]any{
		userText("m1", "write the README"),
		coderTool("m2", "write", "tool-calls", 1),
		coderText("m3", "The README is written.", "stop"),
	})
	activity, err := r.activity("ses_aaa", 0, coder.ActivityBudget)
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	if !activity.Finished {
		t.Fatalf("want the turn reported as over:\n%s", activity.Text)
	}
	if activity.Screen {
		t.Fatal("a record is not a screen")
	}
	for _, want := range []string{"user: write the README", "coder ran write", "coder: The README is written."} {
		if !strings.Contains(activity.Text, want) {
			t.Fatalf("the reading misses %q:\n%s", want, activity.Text)
		}
	}
}

// A turn still running ends on something that is not its end: a prompt that
// just arrived, an answer whose end is not written yet, or a step that
// handed on into a tool call.
func TestTheRecordSaysWhenTheCoderIsStillWorking(t *testing.T) {
	cases := []struct {
		name string
		rows []map[string]any
	}{
		{"a prompt it just received", []map[string]any{coderText("m1", "done", "stop"), userText("m2", "next task")}},
		{"an answer without its end", []map[string]any{userText("m1", "go"), {"message": "m2", "role": "assistant", "completed": 0, "failed": 0, "parttype": "text", "text": "working", "synthetic": 0}}},
		{"a step that handed on into a tool", []map[string]any{userText("m1", "go"), coderTool("m2", "bash", "tool-calls", 1)}},
		{"nothing recorded yet", []map[string]any{sessionOnly()}},
	}
	for _, tc := range cases {
		r := activityRepository(t, tc.rows)
		activity, err := r.activity("ses_aaa", 0, coder.ActivityBudget)
		if err != nil {
			t.Fatalf("%s: activity: %v", tc.name, err)
		}
		if activity.Finished {
			t.Fatalf("%s: want the coder reported as working:\n%s", tc.name, activity.Text)
		}
	}
}

// An answer that died with an error writes no completion time, only the
// error: the turn is still over, nothing more is coming.
func TestAFailedAnswerCountsAsOver(t *testing.T) {
	r := activityRepository(t, []map[string]any{
		userText("m1", "go"),
		{"message": "m2", "role": "assistant", "completed": 0, "failed": 1, "parttype": "text", "text": "half", "synthetic": 0},
	})
	activity, err := r.activity("ses_aaa", 0, coder.ActivityBudget)
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	if !activity.Finished {
		t.Fatalf("want a failed turn reported as over:\n%s", activity.Text)
	}
}

// A synthetic part is injected bookkeeping, not the user's words.
func TestSyntheticPartsStayOut(t *testing.T) {
	r := activityRepository(t, []map[string]any{
		{"message": "m1", "role": "user", "completed": 0, "failed": 0, "parttype": "text", "text": "<injected context>", "synthetic": 1},
		userText("m1", "the real prompt"),
		coderText("m2", "done", "stop"),
	})
	activity, err := r.activity("ses_aaa", 0, coder.ActivityBudget)
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	if strings.Contains(activity.Text, "injected context") {
		t.Fatalf("the synthetic part is bookkeeping:\n%s", activity.Text)
	}
	if !strings.Contains(activity.Text, "user: the real prompt") {
		t.Fatalf("the real prompt has to stay:\n%s", activity.Text)
	}
}

// The record is found by the session id alone. A session opencode does not
// know answers no rows, which is an error a check can report, not a guess;
// an id outside the alphabet never reaches a query.
func TestASessionWithoutARecordIsAnError(t *testing.T) {
	r := activityRepository(t, []map[string]any{})
	if _, err := r.activity("ses_nothinghere", 0, coder.ActivityBudget); err == nil {
		t.Fatal("want an error for a session with no rows")
	}
	if _, err := r.activity("", 0, coder.ActivityBudget); err == nil {
		t.Fatal("want an error for an empty session id")
	}
	if _, err := r.activity("../escape", 0, coder.ActivityBudget); err == nil {
		t.Fatal("want an error for an id that is a path")
	}
	empty := &sessionRepository{dataRoot: t.TempDir(), query: func(string) ([]byte, error) {
		t.Fatal("no database, no query")
		return nil, nil
	}}
	if _, err := empty.activity("ses_aaa", 0, coder.ActivityBudget); err == nil {
		t.Fatal("want an error without a database")
	}
}

// A pre-created session that has not spoken yet answers its own row without
// a message: recorded, nothing said, not finished.
func TestAnEmptySessionReadsAsStarting(t *testing.T) {
	r := activityRepository(t, []map[string]any{sessionOnly()})
	activity, err := r.activity("ses_aaa", 0, coder.ActivityBudget)
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	if activity.Text != "" || activity.Finished {
		t.Fatalf("want an empty unfinished reading, got %+v", activity)
	}
}

// The newest message gets the whole budget first, the same rule the other
// readers apply: a 3000 rune final report arrives whole in the default
// reading, the older lines keep their per-line cut and the oldest fall off
// first, and only a newest message over the whole budget is cut.
func TestTheNewestMessageGetsTheBudgetFirst(t *testing.T) {
	oldLong := func(i int) string {
		return "old-" + string(rune('0'+i)) + "-" + strings.Repeat("y", 800)
	}
	report := "final-" + strings.Repeat("r", 2994)
	var rows []map[string]any
	for i := 0; i < 10; i++ {
		rows = append(rows, coderText("m"+string(rune('a'+i)), oldLong(i), "stop"))
	}
	rows = append(rows, coderText("mz", report, "stop"))
	r := activityRepository(t, rows)

	activity, err := r.activity("ses_aaa", 0, coder.ActivityBudget)
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

	over := activityRepository(t, []map[string]any{coderText("m1", "final-"+strings.Repeat("r", 4500), "stop")})
	activity, err = over.activity("ses_aaa", 0, coder.ActivityBudget)
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	if !strings.Contains(activity.Text, "… [cut: 4000 of ") || !strings.Contains(activity.Text, "runes shown, use --full for the whole message]") {
		t.Fatalf("a newest message over the whole budget has to say what was cut:\n%.200s", activity.Text)
	}
}

// --full lifts the cap here too: every message arrives whole, and it composes
// with --entries.
func TestAFullReadingIsUncut(t *testing.T) {
	older := "older-" + strings.Repeat("y", 1500)
	report := "final-" + strings.Repeat("r", 9000)
	r := activityRepository(t, []map[string]any{
		coderText("m1", older, "stop"),
		coderText("m2", report, "stop"),
	})
	full, err := r.activity("ses_aaa", 0, 0)
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

	one, err := r.activity("ses_aaa", 1, 0)
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	if !strings.Contains(one.Text, report) || strings.Contains(one.Text, "older-") {
		t.Fatalf("one entry full has to be exactly the whole last message:\n%.200s", one.Text)
	}
}
