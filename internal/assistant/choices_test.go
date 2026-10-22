package assistant

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const choicesAnswer = "Two things first.\n\n```ask\nWhich database should the app use?\n- PostgreSQL\n- SQLite\nDeploy right away?\n- Yes\n- No, after the review\n```\n"

// mixedAnswer asks a question that takes several picks before one that takes
// one.
const mixedAnswer = "```ask\nWhich checks run first?\n- [ ] Unit tests\n- [ ] Lint\n- [ ] e2e\nDeploy right away?\n- Yes\n- No\n```\n"

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
		"box after plain":     "Pick?\n- a\n- [ ] b\n",
		"plain after box":     "Pick?\n- [ ] a\n- b\n",
		"empty box":           "Pick?\n- [ ] a\n- [ ]\n",
		"one box":             "Pick?\n- [ ] only\n",
	} {
		if _, ok := answered("```ask\n" + body + "```\n").Choices(); ok {
			t.Errorf("%s: want no questions", name)
		}
	}
}

func TestOptionsWithBoxesTakeSeveralPicksBesideAQuestionThatTakesOne(t *testing.T) {
	choices, ok := answered(mixedAnswer).Choices()
	want := []Question{
		{Text: "Which checks run first?", Options: []string{"Unit tests", "Lint", "e2e"}, Multi: true},
		{Text: "Deploy right away?", Options: []string{"Yes", "No"}},
	}
	if !ok || !reflect.DeepEqual(choices.Questions, want) {
		t.Fatalf("want a question with several picks and one with one, got %+v %v", choices, ok)
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

func TestTheAnswerReadsEveryOptionPickedOnAQuestionThatTakesSeveral(t *testing.T) {
	c := Instance{Messages: []Message{answered(mixedAnswer)}}
	m, err := answerMessage(c, "a1", []Pick{{Option: -1, Options: []int{2, 0}, Text: " before Friday "}, {Option: 0}})
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	want := "Which checks run first?\n- [x] Unit tests\n- [ ] Lint\n- [x] e2e\nNote: before Friday\n\nDeploy right away?\nAnswer: Yes"
	if m.Content != want {
		t.Fatalf("want %q, got %q", want, m.Content)
	}
	if !reflect.DeepEqual(m.Answers.Picks, []Pick{{Option: -1, Options: []int{0, 2}, Text: "before Friday"}, {Option: 0}}) {
		t.Fatalf("want the picks kept in the question's order, got %+v", m.Answers.Picks)
	}
	words, err := answerMessage(c, "a1", []Pick{{Option: -1, Text: "only the fast ones"}, {Option: 1}})
	if err != nil || words.Content != "Which checks run first?\nAnswer in my own words: only the fast ones\n\nDeploy right away?\nAnswer: No" {
		t.Fatalf("want the words alone, got %q %v", words.Content, err)
	}
}

func TestAnOptionHoldingACommaStaysOneOption(t *testing.T) {
	c := Instance{Messages: []Message{answered("```ask\nWhich checks run first?\n- [ ] Unit, fast\n- [ ] Lint\n- [ ] e2e\n```\n")}}
	m, err := answerMessage(c, "a1", []Pick{{Option: -1, Options: []int{0, 1}}})
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	want := "Which checks run first?\n- [x] Unit, fast\n- [x] Lint\n- [ ] e2e"
	if m.Content != want {
		t.Fatalf("want %q, got %q", want, m.Content)
	}
}

func TestPicksOnAQuestionThatTakesSeveralAreRefusedWhereTheyDoNotFit(t *testing.T) {
	c := Instance{Messages: []Message{answered(mixedAnswer)}}
	for name, picks := range map[string][]Pick{
		"none and no words": {{Option: -1}, {Option: 0}},
		"picked twice":      {{Option: -1, Options: []int{1, 1}}, {Option: 0}},
		"out of range":      {{Option: -1, Options: []int{0, 3}}, {Option: 0}},
		"below zero":        {{Option: -1, Options: []int{-1}}, {Option: 0}},
		"more than offered": {{Option: -1, Options: []int{0, 1, 2, 0}}, {Option: 0}},
		"one option there":  {{Option: 0}, {Option: 0}},
		"several on one":    {{Option: -1, Options: []int{0}}, {Option: -1, Options: []int{0, 1}}},
		"beyond questions":  {{Option: -1, Options: []int{0}}, {Option: 0}, {Option: -1, Options: []int{0}}},
	} {
		if _, err := answerMessage(c, "a1", picks); err == nil {
			t.Errorf("%s: want a refusal", name)
		}
	}
}

// An answer stored before a question could take several picks reads as it
// always did, and a pick of one option is stored in that shape still.
func TestAPickOfOneOptionKeepsItsStoredShape(t *testing.T) {
	var old Answers
	if err := json.Unmarshal([]byte(`{"message":"a1","picks":[{"option":1,"text":"x"},{"option":-1,"text":"y"}]}`), &old); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(old.Picks, []Pick{{Option: 1, Text: "x"}, {Option: -1, Text: "y"}}) || !reflect.DeepEqual(old.Picks[0].Picked(), []int{1}) {
		t.Fatalf("want the stored picks as before, got %+v", old.Picks)
	}
	raw, _ := json.Marshal([]Pick{{Option: 1}, {Option: -1, Options: []int{0, 2}}})
	if string(raw) != `[{"option":1},{"option":-1,"options":[0,2]}]` {
		t.Fatalf("want one option stored as before and several beside it, got %s", raw)
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
	if !ok || len(choices.Questions) != 3 || choices.Questions[0].Multi || !choices.Questions[1].Multi || choices.Questions[2].Multi {
		t.Fatalf("want the example read as three questions, the second taking several picks, got %+v %v", choices, ok)
	}
	pinned(t, "end your answer with one `ask` block", "the answers come back as the user's next message")
}
