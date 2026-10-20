package assistant

import (
	"reflect"
	"strings"
	"testing"
)

const choicesAnswer = "Two things first.\n\n```ask\nWhich database should the app use?\n- PostgreSQL\n- SQLite\nDeploy right away?\n- Yes\n- No, after the review\n```\n"

func answered(content string) Message {
	return Message{ID: "a1", Role: RoleAssistant, State: StateComplete, Content: content}
}

func TestAnAskBlockGivesItsQuestionsAndOptions(t *testing.T) {
	choices, ok := answered("```go\nx := 1\n```\n\n" + choicesAnswer).Choices()
	if !ok {
		t.Fatal("want the block read")
	}
	want := []Question{
		{Text: "Which database should the app use?", Options: []string{"PostgreSQL", "SQLite"}},
		{Text: "Deploy right away?", Options: []string{"Yes", "No, after the review"}},
	}
	if choices.Fence != 1 || !reflect.DeepEqual(choices.Questions, want) {
		t.Fatalf("want the second fence with both questions, got %+v", choices)
	}
}

func TestAMalformedAskBlockAsksNothing(t *testing.T) {
	for name, body := range map[string]string{
		"one option":          "Pick?\n- only\n",
		"option first":        "- yes\nPick?\n- no\n",
		"question without":    "First?\n- a\n- b\nSecond?\n",
		"empty":               "\n",
		"empty option":        "Pick?\n- a\n-  \n",
		"too many options":    "Pick?\n" + strings.Repeat("- o\n", maxChoiceOptions+1),
		"too many questions":  strings.Repeat("Q?\n- a\n- b\n", MaxChoiceQuestions+1),
		"question too long":   strings.Repeat("x", maxChoiceRunes+1) + "\n- a\n- b\n",
		"option too long":     "Pick?\n- a\n- " + strings.Repeat("x", maxChoiceRunes+1) + "\n",
		"no options as lines": "Pick?\n-a\n-b\n",
	} {
		if _, ok := answered("```ask\n" + body + "```\n").Choices(); ok {
			t.Errorf("%s: want no questions", name)
		}
	}
}

func TestAChoicesBlockIsPlainCode(t *testing.T) {
	if _, ok := answered(strings.Replace(choicesAnswer, "```ask", "```choices", 1)).Choices(); ok {
		t.Fatal("want a choices block left as code")
	}
}

func TestOnlyAFinishedChatAnswerAsksTheUser(t *testing.T) {
	for name, m := range map[string]Message{
		"streaming": {Role: RoleAssistant, State: StateStreaming, Content: choicesAnswer},
		"cancelled": {Role: RoleAssistant, State: StateCancelled, Content: choicesAnswer},
		"user":      {Role: RoleUser, State: StateComplete, Content: choicesAnswer},
		"note":      {Role: RoleCockpit, State: StateComplete, Content: choicesAnswer, Note: &Note{Source: NoteCheck}},
		"pushed":    {Role: RoleAssistant, State: StateComplete, Content: choicesAnswer, Auto: true},
		"in a list": {Role: RoleAssistant, State: StateComplete, Content: "- item\n\n  ```ask\n  Pick?\n  - a\n  - b\n  ```\n"},
	} {
		if _, ok := m.Choices(); ok {
			t.Errorf("%s: want no questions", name)
		}
	}
}

func TestTheFirstWellFormedBlockIsTheOneAsked(t *testing.T) {
	choices, ok := answered("```ask\nPick?\n- a\n```\n\n```ask\nReally?\n- yes\n- no\n```\n").Choices()
	if !ok || choices.Fence != 1 || choices.Questions[0].Text != "Really?" {
		t.Fatalf("want the second block, got %+v %v", choices, ok)
	}
}

func TestTheAnswerReadsEveryQuestionWithWhatWasPicked(t *testing.T) {
	c := Instance{Messages: []Message{answered(choicesAnswer)}}
	m, err := answerMessage(c, "a1", []Pick{{Option: 0, Text: " keep version 16 "}, {Option: -1, Text: "after Friday"}, {Option: -1}})
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	want := "Which database should the app use?\nAnswer: PostgreSQL\nNote: keep version 16\n\nDeploy right away?\nAnswer in my own words: after Friday"
	if m.Content != want {
		t.Fatalf("want %q, got %q", want, m.Content)
	}
	if m.Answers == nil || m.Answers.Message != "a1" || !reflect.DeepEqual(m.Answers.Picks, []Pick{{Option: 0, Text: "keep version 16"}, {Option: -1, Text: "after Friday"}}) {
		t.Fatalf("want the picks kept, got %+v", m.Answers)
	}
	plain, err := answerMessage(c, "a1", []Pick{{Option: 1}, {Option: 0}})
	if err != nil || plain.Content != "Which database should the app use?\nAnswer: SQLite\n\nDeploy right away?\nAnswer: Yes" {
		t.Fatalf("want the options alone, got %q %v", plain.Content, err)
	}
}

func TestAnAnswerIsRefusedWhereItDoesNotFit(t *testing.T) {
	asked := answered(choicesAnswer)
	open := Instance{Messages: []Message{asked}}
	over := Instance{Messages: []Message{asked, {ID: "u1", Role: RoleUser, State: StateQueued, Content: "never mind"}}}
	for name, tc := range map[string]struct {
		c     Instance
		id    string
		picks []Pick
	}{
		"unknown answer":      {open, "nope", []Pick{{Option: 0}, {Option: 0}}},
		"answer asks none":    {Instance{Messages: []Message{answered("plain")}}, "a1", []Pick{{Option: 0}}},
		"thread moved on":     {over, "a1", []Pick{{Option: 0}, {Option: 0}}},
		"question left open":  {open, "a1", []Pick{{Option: 0}, {Option: -1, Text: "  "}}},
		"option out of range": {open, "a1", []Pick{{Option: 2}, {Option: 0}}},
		"option below none":   {open, "a1", []Pick{{Option: -2}, {Option: 0}}},
		"more than asked":     {open, "a1", []Pick{{Option: 0}, {Option: 0}, {Option: 0}}},
		"note too long":       {open, "a1", []Pick{{Option: 0, Text: strings.Repeat("x", MaxChoiceNoteRunes+1)}, {Option: 0}}},
	} {
		if _, err := answerMessage(tc.c, tc.id, tc.picks); err == nil {
			t.Errorf("%s: want a refusal", name)
		}
	}
}

func TestAnsweringSendsOnceAndKeepsTheDraft(t *testing.T) {
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: choicesAnswer}}}
	svc, _, _ := newTestService(t, runner)
	created, _ := svc.create("claude")
	if _, err := svc.Send(created.ID, "Set the app up", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	asked := waitIdle(t, svc, created.ID)
	if _, _, err := svc.SaveDraft(created.ID, "half a thought", nil); err != nil {
		t.Fatalf("draft: %v", err)
	}

	frames := collectFrames(svc, created.ID)
	run, err := svc.Answer(created.ID, asked.ID, []Pick{{Option: 1}, {Option: 0, Text: "today"}})
	if err != nil || run.UserMessageID == "" {
		t.Fatalf("answer: %+v %v", run, err)
	}
	waitIdle(t, svc, created.ID)
	collected := frames.stop()
	if len(collected) < 3 || collected[0].Kind != FrameMessage || collected[0].MessageID != asked.ID || collected[1].Kind != FrameMessage || collected[1].MessageID != run.UserMessageID || collected[2].Kind != FrameStart {
		t.Fatalf("want the asked message and the answer announced before the turn, got %+v", collected)
	}
	if _, err := svc.Answer(created.ID, asked.ID, []Pick{{Option: 0}, {Option: 0}}); err == nil {
		t.Fatal("want a second answer refused")
	}

	instance, _ := svc.Get(created.ID)
	reply, ok := ReplyTo(instance.Messages, 1)
	if !ok || reply.ID != run.UserMessageID || reply.Answers == nil || reply.Answers.Message != asked.ID {
		t.Fatalf("want the answer as the user's next message, got %+v", reply)
	}
	turns := runner.turns()
	if !strings.Contains(turns[len(turns)-1].Prompt, "Deploy right away?\nAnswer: Yes\nNote: today") {
		t.Fatalf("want the answer as the prompt, got %q", turns[len(turns)-1].Prompt)
	}
	if draft, _ := svc.Draft(created.ID); draft.Text != "half a thought" {
		t.Fatalf("want the composer's draft kept, got %q", draft.Text)
	}
}

func TestAUserMessageEndsTheQuestionsSinceTheUserLastWrote(t *testing.T) {
	asks := func(id string) Message {
		m := answered(choicesAnswer)
		m.ID = id
		return m
	}
	user := func(id string) Message { return Message{ID: id, Role: RoleUser, State: StateComplete, Content: "x"} }
	plain := Message{ID: "p", Role: RoleAssistant, State: StateComplete, Content: "No questions."}
	messages := []Message{asks("old"), user("u1"), asks("a1"), plain, asks("a2"), user("u2"), user("u3")}
	if got := Ends(messages, 5); !reflect.DeepEqual(got, []string{"a2", "a1"}) {
		t.Fatalf("want the asking answers since u1, got %v", got)
	}
	if got := Ends(messages, 6); got != nil {
		t.Fatalf("want nothing ended by a message right after another, got %v", got)
	}
	if got := Ends(messages, 1); !reflect.DeepEqual(got, []string{"old"}) {
		t.Fatalf("want the first answer ended by the first message, got %v", got)
	}
}

// The example the instructions teach is the format the page reads.
func TestTheInstructionsTeachTheAskBlock(t *testing.T) {
	text := instructionsText(t)
	start := strings.Index(text, "```ask\n")
	if start < 0 {
		t.Fatal("the instructions carry no ask block")
	}
	end := strings.Index(text[start+3:], "```") + start + 6
	choices, ok := answered(text[start:end]).Choices()
	if !ok || len(choices.Questions) != 2 {
		t.Fatalf("want the example read as two questions, got %+v %v", choices, ok)
	}
	pinned(t, "end your answer with one `ask` block", "the answers come back as the user's next message")
}
