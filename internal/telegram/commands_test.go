package telegram

import (
	"strings"
	"testing"

	"github.com/local/dev-cockpit/internal/assistant"
)

func TestStartSaysWhatThisIs(t *testing.T) {
	tc := newTestChannel(t)
	tc.pair(42)
	tc.api.push(tc.message(1, 42, "/start"))
	tc.Start()

	waitFor(t, "the answer to /start", func() bool { return len(tc.api.messages()) == 1 })
	if sent := tc.api.messages(); !strings.Contains(sent[0], "assistant") {
		t.Fatalf("/start does not say what this is: %q", sent[0])
	}
	if prompts := tc.convs.prompts(); len(prompts) != 0 {
		t.Fatalf("/start reached the conversation: %+v", prompts)
	}
}

func TestNewOffersTheCodersAndCreatesNothingYet(t *testing.T) {
	tc := newTestChannel(t)
	tc.pair(42)
	tc.convs.setLast(assistant.Message{ID: "m1", Role: assistant.RoleUser, Content: "older", State: assistant.StateComplete})
	tc.api.push(tc.message(1, 42, "/new"))
	tc.Start()

	waitFor(t, "the question", func() bool { return len(tc.api.buttons()) > 0 })
	buttons := tc.api.buttons()
	if len(buttons) != 3 {
		t.Fatalf("got %d buttons, want one per coder plus cancel: %+v", len(buttons), buttons)
	}
	if buttons[0].Data != "new:claude" || buttons[1].Data != "new:copilot" || buttons[2].Data != newCancel {
		t.Fatalf("the buttons do not carry the coders: %+v", buttons)
	}
	for _, button := range buttons {
		if len(button.Data) > 64 {
			t.Fatalf("the callback data is over what Telegram takes: %q", button.Data)
		}
	}
	if text := tc.api.messages()[0]; !strings.Contains(text, "Claude") {
		t.Fatalf("the question does not name the coder running now: %q", text)
	}
	if tc.convs.createdCount() != 0 {
		t.Fatal("the command created a conversation before anybody pressed anything")
	}
}

func TestTheePressCreatesTheConversation(t *testing.T) {
	tc := newTestChannel(t)
	tc.pair(42)
	tc.convs.setLast(assistant.Message{ID: "m1", Role: assistant.RoleUser, Content: "older", State: assistant.StateComplete})
	tc.api.push(tc.message(1, 42, "/new"), tc.press(2, 42, "new:copilot"))
	tc.Start()

	waitFor(t, "the new conversation", func() bool { return tc.convs.createdCount() == 1 })
	current, _ := tc.convs.Current()
	if current.CoderID != "copilot" {
		t.Fatalf("the new conversation runs on %q, want the coder that was pressed", current.CoderID)
	}
	waitFor(t, "the confirmation", func() bool {
		for _, text := range tc.api.messages() {
			if strings.Contains(text, "Copilot answers") {
				return true
			}
		}
		return false
	})
	if answered := tc.api.answered(); len(answered) != 1 {
		t.Fatalf("the press was answered %d times, want once so the button stops spinning", len(answered))
	}
}

func TestCancelChangesNothing(t *testing.T) {
	tc := newTestChannel(t)
	tc.pair(42)
	tc.convs.setLast(assistant.Message{ID: "m1", Role: assistant.RoleUser, Content: "older", State: assistant.StateComplete})
	tc.api.push(tc.message(1, 42, "/new"), tc.press(2, 42, newCancel))
	tc.Start()

	waitFor(t, "the answer to cancel", func() bool {
		for _, text := range tc.api.messages() {
			if strings.Contains(text, "Nothing changed") {
				return true
			}
		}
		return false
	})
	if tc.convs.createdCount() != 0 {
		t.Fatal("cancel created a conversation")
	}
}

func TestTextAfterNewIsNotAPrompt(t *testing.T) {
	tc := newTestChannel(t)
	tc.pair(42)
	tc.api.push(tc.message(1, 42, "/new what is the release coder doing"))
	tc.Start()

	waitFor(t, "the question", func() bool { return len(tc.api.buttons()) > 0 })
	if prompts := tc.convs.prompts(); len(prompts) != 0 {
		t.Fatalf("the text behind the command became a message: %+v", prompts)
	}
	if text := tc.api.messages()[0]; !strings.Contains(text, "takes no text") {
		t.Fatalf("the bot does not say that /new takes no text: %q", text)
	}
}

func TestTheePressIsRefusedWhileATurnIsRunning(t *testing.T) {
	tc := newTestChannel(t)
	tc.pair(42)
	tc.convs.setLast(assistant.Message{ID: "m1", Role: assistant.RoleUser, Content: "older", State: assistant.StateComplete})
	tc.api.push(tc.message(1, 42, "/new"))
	tc.Start()

	waitFor(t, "the question", func() bool { return len(tc.api.buttons()) > 0 })
	// The turn starts between the question and the press, which is exactly why
	// the check sits on the press and not on the command.
	tc.convs.setRunning(true)
	tc.api.push(tc.press(2, 42, "new:claude"))

	waitFor(t, "the refusal", func() bool {
		for _, text := range tc.api.messages() {
			if strings.Contains(text, "still writing") {
				return true
			}
		}
		return false
	})
	if tc.convs.createdCount() != 0 {
		t.Fatal("a conversation was pulled out from under a running turn")
	}
}

func TestAPressFromAnotherChatIsDropped(t *testing.T) {
	captureLog(t)
	tc := newTestChannel(t)
	tc.pair(42)
	tc.api.push(tc.press(1, 99, "new:claude"), tc.message(2, 42, "mine"))
	tc.Start()

	waitFor(t, "the message of the connected chat", func() bool { return len(tc.convs.prompts()) == 1 })
	if tc.convs.createdCount() != 0 {
		t.Fatal("a stranger's press created a conversation")
	}
	if sent := tc.api.messages(); len(sent) != 0 {
		t.Fatalf("a stranger's press was answered: %q", sent)
	}
	if answered := tc.api.answered(); len(answered) != 0 {
		t.Fatalf("a stranger's press got a callback answer: %q", answered)
	}
}

func TestContextSaysHowFullTheWindowStands(t *testing.T) {
	tc := newTestChannel(t)
	tc.pair(42)
	tc.convs.setContext(&assistant.ContextUsage{Model: "claude-opus-5", Tokens: 120_000, Window: 200_000})
	tc.api.push(tc.message(1, 42, "/context"))
	tc.Start()

	waitFor(t, "the reading", func() bool { return len(tc.api.messages()) == 1 })
	line := tc.api.messages()[0]
	for _, want := range []string{"60%", "120 000", "200 000", "claude-opus-5", "Claude"} {
		if !strings.Contains(line, want) {
			t.Fatalf("the reading is missing %q: %q", want, line)
		}
	}
}

func TestContextSaysWhenTheWindowIsUnknown(t *testing.T) {
	tc := newTestChannel(t)
	tc.pair(42)
	// A model nothing here knows: the tokens are real, the window is not.
	tc.convs.setContext(&assistant.ContextUsage{Model: "some-new-model", Tokens: 4200})
	tc.api.push(tc.message(1, 42, "/context"))
	tc.Start()

	waitFor(t, "the answer", func() bool { return len(tc.api.messages()) == 1 })
	line := tc.api.messages()[0]
	if !strings.Contains(line, "unknown") || !strings.Contains(line, "some-new-model") {
		t.Fatalf("the answer invents something instead of saying it does not know: %q", line)
	}
	if strings.Contains(line, "%") {
		t.Fatalf("a percentage was made up: %q", line)
	}
}

func TestContextOnAConversationWithoutATurn(t *testing.T) {
	tc := newTestChannel(t)
	tc.pair(42)
	tc.api.push(tc.message(1, 42, "/context"))
	tc.Start()

	waitFor(t, "the answer", func() bool { return len(tc.api.messages()) == 1 })
	if line := tc.api.messages()[0]; !strings.Contains(line, "no reading") {
		t.Fatalf("a conversation without a turn is not answered plainly: %q", line)
	}
}

func TestAMessageThatOnlyLooksLikeACommandIsAMessage(t *testing.T) {
	tc := newTestChannel(t)
	tc.pair(42)
	tc.api.push(tc.message(1, 42, "/root/projects/dev-cockpit is the working copy"))
	tc.Start()

	waitFor(t, "the message", func() bool { return len(tc.convs.prompts()) == 1 })
	if prompt := tc.convs.prompts()[0]; !strings.HasPrefix(prompt.Text, "/root/projects") {
		t.Fatalf("a path was swallowed as a command: %q", prompt.Text)
	}
}

func TestAMessageFromTheChatCarriesItsOrigin(t *testing.T) {
	tc := newTestChannel(t)
	tc.pair(42)
	tc.api.push(tc.message(1, 42, "what is running"))
	tc.Start()

	waitFor(t, "the message", func() bool { return len(tc.convs.prompts()) == 1 })
	if source := tc.convs.prompts()[0].Source; source != assistant.SourceTelegram {
		t.Fatalf("the message went in with source %q", source)
	}
}

func TestACommandCarryingTheBotNameStillWorks(t *testing.T) {
	command, rest := splitCommand("/new@dev_cockpit_bot look at the tests")
	if command != "/new" || rest != "look at the tests" {
		t.Fatalf("splitCommand gave %q and %q", command, rest)
	}
}

func TestCommandsOnlyAnswerTheConnectedChat(t *testing.T) {
	tc := newTestChannel(t)
	tc.pair(42)
	tc.api.push(tc.message(1, 99, "/start"))
	tc.Start()

	waitFor(t, "the message to be handled", func() bool { return tc.state().Offset == 2 })
	if sent := tc.api.messages(); len(sent) != 0 {
		t.Fatalf("a stranger got an answer to /start: %q", sent)
	}
}
