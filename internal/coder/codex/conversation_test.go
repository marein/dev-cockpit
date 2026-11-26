package codex

import (
	"errors"
	"io/fs"
	"testing"

	"github.com/marein/dev-cockpit/internal/coder"
)

// The conversation is the user's words as typed, never what codex sent the
// model beside them, and one coder message per turn with its tool calls as
// lines where they ran.
func TestTheConversationIsReadFromTheRollout(t *testing.T) {
	c, path := rolloutWith(t)
	project := c.sessions.List()[0].CWD
	appendRollout(t, path,
		event("2026-11-25T00:42:35.365Z", "task_started", turnA, nil),
		instructions("2026-11-25T00:42:36.100Z"),
		userItem("2026-11-25T00:42:36.152Z", turnA, "write the README"),
		coderItem("2026-11-25T00:42:38.790Z", turnA, "I will write it."),
		toolCallLine("2026-11-25T00:42:39.825Z", "call_1"),
		commandItem("2026-11-25T00:43:00.201Z", turnA, "make readme\nmake check"),
		item("2026-11-25T00:43:00.300Z", turnA, map[string]any{"type": "FileChange", "id": "f1", "changes": map[string]any{project + "/docs/README.md": map[string]string{"type": "add"}}}),
		item("2026-11-25T00:43:00.350Z", turnA, map[string]any{"type": "McpToolCall", "id": "m1", "server": "docs", "tool": "lookup"}),
		item("2026-11-25T00:43:00.400Z", turnA, map[string]any{"type": "Reasoning", "id": "r1"}),
		coderItem("2026-11-25T00:43:01.777Z", turnA, "The README is written."),
		event("2026-11-25T00:43:01.787Z", "task_complete", turnA, nil),
		event("2026-11-25T00:43:09.213Z", "task_started", turnB, nil),
		userItem("2026-11-25T00:43:09.468Z", turnB, "thanks"),
		coderItem("2026-11-25T00:43:12.749Z", turnB, "Any time."),
		toolCallLine("2026-11-25T00:43:13.100Z", "call_2"),
		event("2026-11-25T00:43:16.241Z", "turn_aborted", turnB, map[string]any{"reason": "interrupted"}),
		commandItem("2026-11-25T00:43:58.000Z", turnB, "sleep 45"),
	)
	messages, err := c.SessionConversation(firstID)
	if err != nil {
		t.Fatalf("conversation: %v", err)
	}
	if len(messages) != 4 {
		t.Fatalf("want user, coder, user, coder, got %d: %+v", len(messages), messages)
	}
	if messages[0].Role != coder.RoleUser || messages[0].Text() != "write the README" || messages[0].Time.IsZero() || messages[0].ID == "" {
		t.Fatalf("the first message is %+v", messages[0])
	}
	turn := messages[1]
	want := []coder.Part{
		{Kind: coder.PartText, Text: "I will write it."},
		{Kind: coder.PartTool, Tool: "shell", Text: "make readme"},
		{Kind: coder.PartTool, Tool: "apply_patch", Text: "docs/README.md"},
		{Kind: coder.PartTool, Tool: "lookup"},
		{Kind: coder.PartText, Text: "The README is written."},
	}
	if turn.Role != coder.RoleCoder || len(turn.Parts) != len(want) {
		t.Fatalf("the turn is %+v", turn)
	}
	for i, part := range want {
		if turn.Parts[i] != part {
			t.Fatalf("part %d = %+v, want %+v", i, turn.Parts[i], part)
		}
	}
	if messages[2].Text() != "thanks" || messages[3].Text() != "Any time." {
		t.Fatalf("the second turn is %+v %+v", messages[2], messages[3])
	}
	if late := messages[3].Parts; len(late) != 2 || late[1] != (coder.Part{Kind: coder.PartTool, Tool: "shell", Text: "sleep 45"}) {
		t.Fatalf("the command an interrupted turn left running stands apart from its turn: %+v", late)
	}
	seen := map[string]bool{}
	for _, m := range messages {
		if seen[m.ID] {
			t.Fatalf("the id %q names two messages", m.ID)
		}
		seen[m.ID] = true
	}
}

// A session codex has not written yet has no conversation, which is not a
// record that could not be read.
func TestASessionWithoutARolloutHasNoConversationYet(t *testing.T) {
	c, _ := rolloutWith(t)
	if _, err := c.SessionConversation(secondID); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("want a record that is not there yet, got %v", err)
	}
}
