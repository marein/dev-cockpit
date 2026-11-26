package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/coder"
)

// The records below are the shapes codex 0.172.0 writes, cut to the fields
// a reading takes plus enough of the rest to prove it is skipped.

func event(at, kind, turn string, rest map[string]any) string {
	payload := map[string]any{"type": kind, "turn_id": turn}
	for k, v := range rest {
		payload[k] = v
	}
	line, _ := json.Marshal(map[string]any{"timestamp": at, "type": "event_msg", "payload": payload})
	return string(line)
}

func item(at, turn string, body map[string]any) string {
	return event(at, "item_completed", turn, map[string]any{"item": body})
}

func response(at string, payload map[string]any) string {
	line, _ := json.Marshal(map[string]any{"timestamp": at, "type": "response_item", "payload": payload})
	return string(line)
}

func userItem(at, turn, text string) string {
	return item(at, turn, map[string]any{"type": "UserMessage", "id": "u-" + at, "content": []map[string]any{{"type": "text", "text": text}, {"type": "image", "url": "data:..."}}})
}

func coderItem(at, turn, text string) string {
	return item(at, turn, map[string]any{"type": "AgentMessage", "id": "a-" + at, "content": []map[string]any{{"type": "Text", "text": text}}, "phase": "final_answer"})
}

func commandItem(at, turn, command string) string {
	return item(at, turn, map[string]any{"type": "CommandExecution", "id": "c-" + at, "command": []string{"/bin/bash", "-lc", command}, "aggregated_output": "the output is no part of a reading"})
}

func toolCallLine(at, id string) string {
	return response(at, map[string]any{"type": "custom_tool_call", "call_id": id, "name": "exec", "input": "text(await tools.exec_command({cmd:\"make\"}))"})
}

func toolOutputLine(at, id string) string {
	return response(at, map[string]any{"type": "custom_tool_call_output", "call_id": id, "output": []map[string]string{{"type": "input_text", "text": "Script completed"}}})
}

// instructions is what codex sends the model beside the user's words: a
// message of the user's role that nobody typed.
func instructions(at string) string {
	return response(at, map[string]any{"type": "message", "role": "user", "content": []map[string]string{{"type": "input_text", "text": "# AGENTS.md instructions"}}})
}

const (
	turnA = "01a21027-85d5-7f83-bfaf-2164ffe736ed"
	turnB = "01a21028-0a17-7a23-84fc-4a725578a263"
)

func appendRollout(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
		t.Fatal(err)
	}
}

func rolloutWith(t *testing.T, lines ...string) (*Coder, string) {
	t.Helper()
	r, home, project := testRepository(t)
	path := writeRollout(t, home, firstID, map[string]any{"cwd": project}, 0)
	appendRollout(t, path, lines...)
	return &Coder{sessions: r}, path
}

func reading(t *testing.T, c *Coder) coder.Activity {
	t.Helper()
	activity, err := c.SessionActivity(firstID, 0, coder.ActivityBudget)
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	return activity
}

// A finished turn is codex's own record: task_complete stands in the
// rollout, and the reading carries what was said and what ran.
func TestARolloutSaysWhenTheTurnIsOver(t *testing.T) {
	c, _ := rolloutWith(t,
		event("2026-11-25T00:42:35.365Z", "task_started", turnA, nil),
		instructions("2026-11-25T00:42:36.100Z"),
		userItem("2026-11-25T00:42:36.152Z", turnA, "write the\nREADME"),
		toolCallLine("2026-11-25T00:42:39.825Z", "call_1"),
		commandItem("2026-11-25T00:43:00.201Z", turnA, "make readme"),
		toolOutputLine("2026-11-25T00:43:00.207Z", "call_1"),
		item("2026-11-25T00:43:00.300Z", turnA, map[string]any{"type": "FileChange", "id": "f1", "changes": map[string]any{"/p/README.md": map[string]string{"type": "add"}}}),
		item("2026-11-25T00:43:00.400Z", turnA, map[string]any{"type": "Reasoning", "id": "r1"}),
		coderItem("2026-11-25T00:43:01.777Z", turnA, "The README is written."),
		event("2026-11-25T00:43:01.787Z", "task_complete", turnA, nil),
		event("2026-11-25T00:43:09.209Z", "thread_settings_applied", "", nil),
	)
	activity := reading(t, c)
	if !activity.Finished || activity.InToolCall || activity.Empty || activity.Screen {
		t.Fatalf("want a finished turn read from the record: %+v", activity)
	}
	want := "user: write the README\ncoder ran shell, apply_patch\ncoder: The README is written."
	if activity.Text != want {
		t.Fatalf("text = %q\nwant   %q", activity.Text, want)
	}
	if at := time.Date(2026, 11, 25, 0, 43, 1, 787e6, time.UTC); !activity.LastMessageAt.Equal(at) {
		t.Fatalf("the newest word is dated %s, want the turn's end %s", activity.LastMessageAt, at)
	}
}

// A turn is open from its start, and inside a tool call from the call to
// its output: that is where a question to the person stands.
func TestAnOpenTurnAndItsToolCallAreRead(t *testing.T) {
	c, path := rolloutWith(t,
		event("2026-11-25T00:42:35.365Z", "task_started", turnA, nil),
		userItem("2026-11-25T00:42:36.152Z", turnA, "run it"),
	)
	if a := reading(t, c); a.Finished || a.InToolCall {
		t.Fatalf("a started turn reads %+v", a)
	}
	if got := c.BellSignal(firstID); got != coder.TurnSignalOver {
		t.Fatalf("a bell outside a tool call is the turn's end, its record not written yet, got %v", got)
	}
	appendRollout(t, path, toolCallLine("2026-11-25T00:42:39.825Z", "call_1"))
	if a := reading(t, c); a.Finished || !a.InToolCall {
		t.Fatalf("a call without its output reads %+v", a)
	}
	if got := c.BellSignal(firstID); got != coder.TurnSignalWaiting {
		t.Fatalf("a bell inside a tool call is a question, got %v", got)
	}
	appendRollout(t, path, toolOutputLine("2026-11-25T00:43:00.207Z", "call_1"))
	if a := reading(t, c); a.Finished || a.InToolCall {
		t.Fatalf("a call with its output reads %+v", a)
	}
	appendRollout(t, path, coderItem("2026-11-25T00:43:01.777Z", turnA, "done"), event("2026-11-25T00:43:01.787Z", "task_complete", turnA, nil))
	if got := c.BellSignal(firstID); got != coder.TurnSignalOver {
		t.Fatalf("a bell behind the turn's end is the end, got %v", got)
	}
}

// Escape writes turn_aborted, which ends the turn like its completion does.
// The command the turn left running reports its end later, under the turn
// that is over, and opens nothing.
func TestAnInterruptedTurnIsOverAndStaysOver(t *testing.T) {
	c, _ := rolloutWith(t,
		event("2026-11-25T00:43:09.213Z", "task_started", turnB, nil),
		userItem("2026-11-25T00:43:09.468Z", turnB, "sleep"),
		toolCallLine("2026-11-25T00:43:13.100Z", "call_2"),
		event("2026-11-25T00:43:16.241Z", "turn_aborted", turnB, map[string]any{"reason": "interrupted"}),
		commandItem("2026-11-25T00:43:58.000Z", turnB, "sleep 45"),
	)
	activity := reading(t, c)
	if !activity.Finished || activity.InToolCall {
		t.Fatalf("an interrupted turn reads %+v", activity)
	}
	if strings.Contains(activity.Text, "coder ran") {
		t.Fatalf("the late end of a command stands in the reading: %q", activity.Text)
	}
}

// Where the tail no longer holds a turn's start, what happens inside the
// turn proves it open.
func TestATurnWhoseStartFellOffTheTailIsStillOpen(t *testing.T) {
	c, _ := rolloutWith(t, coderItem("2026-11-25T00:43:01.777Z", turnA, "still at it"))
	if a := reading(t, c); a.Finished || a.Empty {
		t.Fatalf("a word inside a turn reads %+v", a)
	}
}

// A rollout nobody spoke in holds bookkeeping alone: no account of a turn.
func TestARolloutWithoutATurnIsEmpty(t *testing.T) {
	c, _ := rolloutWith(t, event("2026-11-25T00:42:35.000Z", "thread_settings_applied", "", nil), instructions("2026-11-25T00:42:36.100Z"))
	if a := reading(t, c); !a.Empty || !a.Finished || a.Text != "" {
		t.Fatalf("bookkeeping reads %+v", a)
	}
}

// A long session costs a reading its tail alone, and a line no reading can
// hold, or one codex is still writing, is skipped without losing the rest.
func TestOnlyTheTailOfALongRolloutIsRead(t *testing.T) {
	c, path := rolloutWith(t,
		event("2026-11-25T00:42:35.365Z", "task_started", turnA, nil),
		userItem("2026-11-25T00:42:36.152Z", turnA, "the old words"),
		event("2026-11-25T00:42:37.000Z", "task_complete", turnA, nil),
	)
	huge, _ := json.Marshal(map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "image": strings.Repeat("A", recordLineLimit+activityTailBytes)}})
	appendRollout(t, path, string(huge),
		event("2026-11-25T00:43:09.213Z", "task_started", turnB, nil),
		userItem("2026-11-25T00:43:09.468Z", turnB, "the new words"),
	)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"timestamp":"2026-11-25T00:43:10.000Z","type":"event_msg","payload":{"type":"task_comp`)
	_ = f.Close()
	activity := reading(t, c)
	if activity.Text != "user: the new words" || activity.Finished {
		t.Fatalf("the tail reads %+v", activity)
	}
}

// The stamp is the rollout's own, a session codex has not written has none,
// and an id that is not codex's names no file.
func TestTheStampIsTheRolloutsAndUnknownSessionsHaveNone(t *testing.T) {
	c, path := rolloutWith(t)
	at := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
	stamp, err := c.SessionActivityStamp(firstID)
	if err != nil || !stamp.Equal(at) {
		t.Fatalf("stamp = %s, %v, want %s", stamp, err, at)
	}
	if _, err := c.SessionActivityStamp(secondID); err == nil {
		t.Fatal("a session without a rollout has a stamp")
	}
	for _, id := range []string{"../" + firstID, "*", ""} {
		if _, err := c.SessionActivity(id, 0, 0); err == nil {
			t.Fatalf("the id %q is read", id)
		}
		if _, err := c.SessionConversation(id); err == nil {
			t.Fatalf("the conversation of the id %q is read", id)
		}
	}
	if err := os.Rename(path, filepath.Join(filepath.Dir(path), "gone")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SessionActivityStamp(firstID); err == nil {
		t.Fatal("a rollout that is gone keeps its stamp")
	}
}
