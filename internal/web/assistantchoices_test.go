package web

import (
	"strings"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/web/render"
)

const choicesInstance = "11111111-1111-4111-8111-111111111111"

func choicesServer(t *testing.T) *Server {
	t.Helper()
	stateDir := t.TempDir()
	conversations, workspace, err := assistant.New(stateDir, oneCoder{}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatalf("assistant: %v", err)
	}
	return &Server{assistants: conversations, workspace: workspace}
}

func choicesThread(rest ...assistant.Message) []assistant.Message {
	asked := assistant.Message{ID: "a1", Role: assistant.RoleAssistant, State: assistant.StateComplete,
		Content: "Before <b>the</b> questions.\n\n```ask\nWhich database?\n- PostgreSQL\n- SQLite <x>\nDeploy now?\n- Yes\n- No\n```\n\nAfter them."}
	return append([]assistant.Message{asked}, rest...)
}

func renderChoices(t *testing.T, s *Server, messages []assistant.Message, postURL string) string {
	t.Helper()
	view := s.assistantMessageView(choicesInstance, messages[0], false, true, "claude")
	answerChoices(&view, messages, 0, postURL)
	return renderTemplate(t, testTemplates(t), "chat_message.gohtml", render.ChatMessageData{Message: view})
}

func TestAnOpenBlockStandsBetweenItsTextAsAForm(t *testing.T) {
	html := renderChoices(t, choicesServer(t), choicesThread(), ownerChoicesURL(choicesInstance, true))
	before := strings.Index(html, "Before")
	block := strings.Index(html, "<dc-assistant-choices")
	after := strings.Index(html, "After them.")
	if before < 0 || block < before || after < block {
		t.Fatalf("want the text, the block and the rest in order: %s", html)
	}
	for _, want := range []string{
		`data-choices-state="open"`,
		`post-url="/assistants/` + choicesInstance + `"`,
		`fragment-url="/assistants/` + choicesInstance + `/messages/a1"`,
		`name="pick-1" value="1"`,
		`name="note-0"`,
		`SQLite &lt;x&gt;`,
		`data-choices-send>`,
		"0 of 2 answered",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("want %q in %s", want, html)
		}
	}
	if strings.Contains(html, "language-ask") || strings.Contains(html, "<b>") || strings.Count(html, "disabled") != 1 {
		t.Fatalf("want the block out of the markdown and nothing disabled but Send: %s", html)
	}
}

func TestAnAnsweredBlockShowsThePicksReadOnly(t *testing.T) {
	reply := assistant.Message{ID: "u1", Role: assistant.RoleUser, State: assistant.StateComplete, Content: "…",
		Answers: &assistant.Answers{Message: "a1", Picks: []assistant.Pick{{Option: 1, Text: "line one\nline <two>"}, {Option: -1, Text: "after Friday"}}}}
	html := renderChoices(t, choicesServer(t), choicesThread(reply), ownerChoicesURL(choicesInstance, true))
	for _, want := range []string{
		`data-choices-state="answered"`,
		`name="pick-0" value="1" id="choices-a1-0-1" autocomplete="off" checked`,
		"line one<br>line &lt;two&gt;",
		"after Friday",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("want %q in %s", want, html)
		}
	}
	for _, unwanted := range []string{"post-url", "data-choices-send", "<textarea", `value="0" id="choices-a1-1-0" autocomplete="off" checked`, "Answered"} {
		if strings.Contains(html, unwanted) {
			t.Fatalf("want no %q in %s", unwanted, html)
		}
	}
	if !strings.Contains(html, "<fieldset") || !strings.Contains(html, "min-w-0\" disabled>") {
		t.Fatalf("want the fieldset disabled: %s", html)
	}
}

func TestTheAnswerStandsAsASlimRowUnderItsBlock(t *testing.T) {
	reply := assistant.Message{ID: "u1", Role: assistant.RoleUser, State: assistant.StateComplete, Content: "Answer: PostgreSQL",
		CreatedAt: time.Date(2026, 10, 20, 21, 3, 0, 0, time.UTC),
		Answers:   &assistant.Answers{Message: "a1", Picks: []assistant.Pick{{Option: 0}, {Option: 1}}}}
	next := assistant.Message{ID: "a2", Role: assistant.RoleAssistant, State: assistant.StateComplete, Content: "Done."}
	views := choicesServer(t).assistantMessageViews(assistant.Instance{Summary: assistant.Summary{ID: choicesInstance, CoderID: "claude"}, Messages: choicesThread(reply, next)}, false)
	var ids []string
	for _, view := range views {
		ids = append(ids, view.ID)
	}
	if strings.Join(ids, ",") != "a1,u1,a2" {
		t.Fatalf("want the asked message, the answer and the next answer, got %v", ids)
	}
	html := renderTemplate(t, testTemplates(t), "chat_message.gohtml", render.ChatMessageData{Message: views[1]})
	for _, want := range []string{`data-role="user"`, "data-assistant-answered", "You", `<dc-time datetime="2026-10-20T21:03:00Z"`, "answered</span>"} {
		if !strings.Contains(html, want) {
			t.Fatalf("want %q in the row: %s", want, html)
		}
	}
	if strings.Contains(html, "PostgreSQL") || strings.Contains(html, "data-assistant-text") {
		t.Fatalf("want nothing of the answer's text in the row: %s", html)
	}
}

func TestAThreadThatMovedOnLeavesTheBlockBehind(t *testing.T) {
	other := assistant.Message{ID: "u1", Role: assistant.RoleUser, State: assistant.StateQueued, Content: "something else"}
	html := renderChoices(t, choicesServer(t), choicesThread(other), ownerChoicesURL(choicesInstance, true))
	if !strings.Contains(html, `data-choices-state="passed"`) || !strings.Contains(html, "Not answered") || strings.Contains(html, " checked") || strings.Contains(html, "post-url") {
		t.Fatalf("want the block over without a pick: %s", html)
	}
	views := choicesServer(t).assistantMessageViews(assistant.Instance{Summary: assistant.Summary{ID: choicesInstance, CoderID: "claude"}, Messages: choicesThread(other)}, false)
	row := renderTemplate(t, testTemplates(t), "chat_message.gohtml", render.ChatMessageData{Message: views[1]})
	if strings.Contains(row, "data-assistant-answered") || !strings.Contains(row, "something else") {
		t.Fatalf("want the other message as it always stood, no answered row: %s", row)
	}
}

func TestABlockThePageCannotAnswerIsReadOnly(t *testing.T) {
	for name, postURL := range map[string]string{"guest": "", "blocked": ownerChoicesURL(choicesInstance, false)} {
		html := renderChoices(t, choicesServer(t), choicesThread(), postURL)
		if !strings.Contains(html, `data-choices-state="readonly"`) || strings.Contains(html, "post-url") || strings.Contains(html, "data-choices-send") {
			t.Fatalf("%s: want the block read only: %s", name, html)
		}
	}
}

func TestAMalformedBlockStaysTheCodeItWasWrittenAs(t *testing.T) {
	s := choicesServer(t)
	messages := []assistant.Message{{ID: "a1", Role: assistant.RoleAssistant, State: assistant.StateComplete, Content: "Pick:\n\n```ask\nWhich?\n- only one\n```\n"}}
	html := renderChoices(t, s, messages, ownerChoicesURL(choicesInstance, true))
	if strings.Contains(html, "dc-assistant-choices") || !strings.Contains(html, `class="language-ask"`) || !strings.Contains(html, "- only one") {
		t.Fatalf("want the block as code: %s", html)
	}
}

func TestAChoicesBlockStaysCode(t *testing.T) {
	s := choicesServer(t)
	messages := []assistant.Message{{ID: "a1", Role: assistant.RoleAssistant, State: assistant.StateComplete, Content: "Pick:\n\n```choices\nWhich?\n- one\n- other\n```\n"}}
	html := renderChoices(t, s, messages, ownerChoicesURL(choicesInstance, true))
	if strings.Contains(html, "dc-assistant-choices") || !strings.Contains(html, `class="language-choices"`) || !strings.Contains(html, "- other") {
		t.Fatalf("want the block as code: %s", html)
	}
}
