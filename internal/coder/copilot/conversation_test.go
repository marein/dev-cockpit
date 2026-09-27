package copilot

import (
	"testing"

	"github.com/marein/dev-cockpit/internal/coder"
)

func build(lines ...string) []coder.Message {
	var b conversationBuilder
	for _, line := range lines {
		b.Feed([]byte(line))
	}
	return b.Messages()
}

func TestATurnIsOneMessageWithTheToolsItRan(t *testing.T) {
	all := build(sessionStartEvent, userEvent, turnStartEvent, toolEvent, toolDoneEvent, answerEvent, turnEndEvent)
	if len(all) != 2 {
		t.Fatalf("got %d messages, want 2: %+v", len(all), all)
	}
	user, turn := all[0], all[1]
	if user.Role != coder.RoleUser || user.ID != "e1" || user.Text() != "write the README" {
		t.Fatalf("user message: %+v", user)
	}
	if turn.Role != coder.RoleCoder || turn.ID != "e3" || len(turn.Parts) != 2 {
		t.Fatalf("coder turn: %+v", turn)
	}
	if turn.Parts[0].Kind != coder.PartTool || turn.Parts[0].Tool != "write" {
		t.Fatalf("the tool call: %+v", turn.Parts[0])
	}
	if turn.Parts[1].Text != "The README is written." {
		t.Fatalf("the answer: %+v", turn.Parts[1])
	}
}

// The user's next words end a turn, and so do an abort and the session's end:
// the parts after them open a turn of their own.
func TestATurnEndsWithTheNextWordsAnAbortOrTheEnd(t *testing.T) {
	for _, end := range []string{abortEvent, nextUserEvent, shutdownEvent} {
		all := build(sessionStartEvent, userEvent, turnStartEvent, toolEvent, turnEndEvent, end, answerEvent)
		last := all[len(all)-1]
		if last.Role != coder.RoleCoder || len(last.Parts) != 1 || last.Parts[0].Kind != coder.PartText {
			t.Fatalf("after %s the answer joined the ended turn: %+v", end, all)
		}
	}
}

// The shape copilot writes: a tool call is a model round of its own,
// turn_end and turn_start with the next turnId stand between the call and the
// answer inside one interaction. Both rounds are one turn and one bubble, the
// call names what it ran, and a failed call leaves nothing but its line.
const (
	roundUserEvent     = `{"type":"user.message","data":{"content":"list the files","interactionId":"i1"},"id":"r1","timestamp":"2026-09-26T08:00:00.000Z"}`
	roundStartEvent    = `{"type":"assistant.turn_start","data":{"turnId":"0","interactionId":"i1"},"id":"r2","timestamp":"2026-09-26T08:00:01.000Z"}`
	roundRequestEvent  = `{"type":"assistant.message","data":{"messageId":"x1","content":"","toolRequests":[{"toolCallId":"call_1","name":"bash","arguments":{"command":"ls /tmp","description":"List"}}],"interactionId":"i1"},"id":"r3","timestamp":"2026-09-26T08:00:02.000Z"}`
	roundToolEvent     = `{"type":"tool.execution_start","data":{"toolCallId":"call_1","toolName":"bash","arguments":{"command":"ls /tmp","description":"List","initial_wait":30},"turnId":"0"},"id":"r4","timestamp":"2026-09-26T08:00:02.100Z"}`
	roundToolDoneEvent = `{"type":"tool.execution_complete","data":{"toolCallId":"call_1","interactionId":"i1","turnId":"0","success":true,"result":{"content":"a\nb"}},"id":"r5","timestamp":"2026-09-26T08:00:03.000Z"}`
	roundViewEvent     = `{"type":"tool.execution_start","data":{"toolCallId":"call_2","toolName":"view","arguments":{"path":"/tmp/missing.txt"},"turnId":"0"},"id":"r6","timestamp":"2026-09-26T08:00:03.100Z"}`
	roundViewFailEvent = `{"type":"tool.execution_complete","data":{"toolCallId":"call_2","interactionId":"i1","turnId":"0","success":false,"error":{"message":"Path does not exist","code":"failure"}},"id":"r7","timestamp":"2026-09-26T08:00:03.200Z"}`
	roundPatchEvent    = `{"type":"tool.execution_start","data":{"toolCallId":"call_3","toolName":"apply_patch","arguments":"*** Begin Patch\n*** Update File: /tmp/app.py\n@@\n-a\n+b\n*** End Patch","turnId":"0"},"id":"r8","timestamp":"2026-09-26T08:00:03.300Z"}`
	roundEndEvent      = `{"type":"assistant.turn_end","data":{"turnId":"0"},"id":"r9","timestamp":"2026-09-26T08:00:04.000Z"}`
	roundNextEvent     = `{"type":"assistant.turn_start","data":{"turnId":"1","interactionId":"i1"},"id":"r10","timestamp":"2026-09-26T08:00:04.100Z"}`
	roundAnswerEvent   = `{"type":"assistant.message","data":{"messageId":"x2","content":"Two files.","interactionId":"i1"},"id":"r11","timestamp":"2026-09-26T08:00:05.000Z"}`
	roundLastEndEvent  = `{"type":"assistant.turn_end","data":{"turnId":"1"},"id":"r12","timestamp":"2026-09-26T08:00:05.100Z"}`

	abortEvent    = `{"type":"abort","data":{"reason":"user_initiated"},"id":"a1"}`
	nextUserEvent = `{"type":"user.message","data":{"content":"next"},"id":"u9"}`
	shutdownEvent = `{"type":"session.shutdown","data":{"shutdownType":"routine"},"id":"s9"}`
)

func TestTheRoundsOfOneInteractionAreOneBubble(t *testing.T) {
	all := build(sessionStartEvent, roundUserEvent, roundStartEvent, roundRequestEvent, roundToolEvent, roundToolDoneEvent, roundViewEvent, roundViewFailEvent, roundPatchEvent, roundEndEvent, roundNextEvent, roundAnswerEvent, roundLastEndEvent)
	if len(all) != 2 {
		t.Fatalf("got %d messages, want the user's and one coder turn: %+v", len(all), all)
	}
	turn := all[1]
	if turn.Role != coder.RoleCoder || turn.ID != "r4" || len(turn.Parts) != 4 {
		t.Fatalf("coder turn: %+v", turn)
	}
	bash, view, patch, answer := turn.Parts[0], turn.Parts[1], turn.Parts[2], turn.Parts[3]
	if bash.Tool != "bash" || bash.Text != "ls /tmp" {
		t.Fatalf("the bash call: %+v", bash)
	}
	if view.Tool != "view" || view.Text != "/tmp/missing.txt" {
		t.Fatalf("the failed view call: %+v", view)
	}
	if patch.Tool != "apply_patch" || patch.Text != "/tmp/app.py" {
		t.Fatalf("the patch call: %+v", patch)
	}
	if answer.Kind != coder.PartText || answer.Text != "Two files." {
		t.Fatalf("the answer: %+v", answer)
	}
}

// The reading goes through the event log from the top on every call.
func TestTheSessionIsReadFromItsLog(t *testing.T) {
	root := t.TempDir()
	c := &Coder{sessions: &sessionRepository{stateRoot: root}}
	writeEvents(t, root, "s1", sessionStartEvent, userEvent, turnStartEvent, toolEvent, toolDoneEvent, answerEvent, turnEndEvent)
	all, err := c.SessionConversation("s1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(all) != 2 || all[1].ID != "e3" || len(all[1].Parts) != 2 {
		t.Fatalf("messages: %+v", all)
	}
}

// A `!` shell escape is recorded as a tool the user requested, with the
// command in its arguments (captured line): it stands as the user's command
// chip and closes the turn before it, and its completion adds nothing.
func TestAShellEscapeIsTheUsersCommand(t *testing.T) {
	escape := `{"type":"tool.user_requested","data":{"toolCallId":"681f0478-ffd3-44fe-965f-b33da6c24e6c","toolName":"local_shell","arguments":{"command":"sleep 10","description":"Execute shell command: sleep 10"}},"id":"5fb62644-3a37-4989-8656-f9c248f2916e","timestamp":"2026-08-28T23:29:20.911Z","parentId":"106c644f-82df-4105-9b91-6c3c7fa008e0"}`
	done := `{"type":"tool.execution_complete","data":{"toolCallId":"681f0478-ffd3-44fe-965f-b33da6c24e6c","isUserRequested":true,"success":true,"result":{"content":""}},"id":"4858bcaa-8f1f-4e76-80ec-b25957f8b045","timestamp":"2026-08-28T23:29:30.925Z","parentId":"5fb62644-3a37-4989-8656-f9c248f2916e"}`
	all := build(userEvent, turnStartEvent, answerEvent, escape, done, toolEvent)
	if len(all) != 4 {
		t.Fatalf("got %d messages, want 4: %+v", len(all), all)
	}
	chip := all[2]
	if chip.Role != coder.RoleUser || chip.Kind != coder.KindCommand || chip.Text() != "! sleep 10" || chip.ID != "5fb62644-3a37-4989-8656-f9c248f2916e" || chip.Time.IsZero() {
		t.Fatalf("the escape: %+v", chip)
	}
	if all[3].Role != coder.RoleCoder || all[3].Parts[0].Tool != "write" {
		t.Fatalf("the tool after the escape opens a turn of its own: %+v", all[3])
	}
}
