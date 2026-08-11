package telegram

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/local/dev-cockpit/internal/assistant"
)

func TestAFinishedAnswerGoesOut(t *testing.T) {
	tc := newTestChannel(t)
	tc.pair(42)
	tc.convs.setAnswer(assistant.Message{
		ID:      "a1",
		Role:    assistant.RoleAssistant,
		Content: "The release coder is done.",
		State:   assistant.StateComplete,
	})

	tc.Notify("conv-1")

	sent := tc.api.messages()
	if len(sent) != 1 || sent[0] != "The release coder is done." {
		t.Fatalf("the answer did not go out as it stands: %q", sent)
	}
}

func TestAFailedAnswerIsReplacedByOneLine(t *testing.T) {
	for _, tt := range []struct {
		state assistant.State
		says  string
	}{
		{assistant.StateFailed, "failed"},
		{assistant.StateCancelled, "stopped"},
		{assistant.StateInterrupted, "did not finish"},
	} {
		tc := newTestChannel(t)
		tc.pair(42)
		tc.convs.setAnswer(assistant.Message{Role: assistant.RoleAssistant, Content: "half an ans", State: tt.state})

		tc.Notify("conv-1")

		sent := tc.api.messages()
		if len(sent) != 1 {
			t.Fatalf("%s: got %d messages, want the one line", tt.state, len(sent))
		}
		if strings.Contains(sent[0], "half an ans") {
			t.Fatalf("%s: the half written answer went out: %q", tt.state, sent[0])
		}
		if !strings.Contains(sent[0], tt.says) {
			t.Fatalf("%s: the line does not say what happened: %q", tt.state, sent[0])
		}
	}
}

func TestAJobReportCarriesAHeadline(t *testing.T) {
	tc := newTestChannel(t)
	tc.pair(42)
	tc.convs.setAnswer(assistant.Message{
		Role:    assistant.RoleAssistant,
		Content: "The tests pass and the branch is pushed.",
		State:   assistant.StateComplete,
		Wake: &assistant.WakeNote{
			Terminal: "abc",
			Name:     "release coder",
			Project:  "dev-cockpit",
			Verdict:  string(assistant.VerdictDone),
		},
	})

	tc.Notify("conv-1")

	sent := tc.api.messages()
	if len(sent) != 1 {
		t.Fatalf("got %d messages, want one", len(sent))
	}
	head := strings.SplitN(sent[0], "\n", 2)[0]
	if !strings.Contains(head, "Job done.") || !strings.Contains(head, "release coder") || !strings.Contains(head, "dev-cockpit") {
		t.Fatalf("the headline does not name the job: %q", head)
	}
	if !strings.Contains(sent[0], "The tests pass") {
		t.Fatalf("the report itself is missing: %q", sent[0])
	}
}

func TestNothingGoesOutWithoutAConnectedChat(t *testing.T) {
	tc := newTestChannel(t)
	tc.store.Update(func(s *State) {
		s.BotToken = testToken
		s.Enabled = true
	})
	tc.convs.setAnswer(assistant.Message{Role: assistant.RoleAssistant, Content: "hello", State: assistant.StateComplete})

	tc.Notify("conv-1")

	if sent := tc.api.messages(); len(sent) != 0 {
		t.Fatalf("something went out without a connected chat: %q", sent)
	}
}

func TestNothingGoesOutWhileTheChannelIsOff(t *testing.T) {
	tc := newTestChannel(t)
	tc.pair(42)
	tc.store.Update(func(s *State) { s.Enabled = false })
	tc.convs.setAnswer(assistant.Message{Role: assistant.RoleAssistant, Content: "hello", State: assistant.StateComplete})

	tc.Notify("conv-1")

	if sent := tc.api.messages(); len(sent) != 0 {
		t.Fatalf("the switched off channel sent %q", sent)
	}
}

func TestANamedFileIsSentAndItsLineLeavesTheText(t *testing.T) {
	tc := newTestChannel(t)
	tc.pair(42)
	tc.files.write(t, "assistant-files/shot.png", 12)
	tc.convs.setAnswer(assistant.Message{
		Role:    assistant.RoleAssistant,
		Content: "Here is the panel:\n\n[[datei: assistant-files/shot.png]]\n\nIt shows the new header.",
		State:   assistant.StateComplete,
	})

	tc.Notify("conv-1")

	sent := tc.api.messages()
	if len(sent) != 1 {
		t.Fatalf("got %d messages, want one", len(sent))
	}
	if strings.Contains(sent[0], "[[datei:") {
		t.Fatalf("the file line stayed in the text: %q", sent[0])
	}
	if !strings.Contains(sent[0], "Here is the panel:") || !strings.Contains(sent[0], "It shows the new header.") {
		t.Fatalf("the text around the line was lost: %q", sent[0])
	}
	uploads := tc.api.uploads()
	if len(uploads) != 1 || uploads[0].Method != "sendPhoto" || uploads[0].Name != "shot.png" {
		t.Fatalf("the file was not sent as a photo: %+v", uploads)
	}
}

func TestSomethingThatIsNotAPictureGoesAsADocument(t *testing.T) {
	tc := newTestChannel(t)
	tc.pair(42)
	tc.files.write(t, "assistant-files/report.md", 20)
	tc.convs.setAnswer(assistant.Message{
		Role:    assistant.RoleAssistant,
		Content: "[[datei: assistant-files/report.md]]",
		State:   assistant.StateComplete,
	})

	tc.Notify("conv-1")

	uploads := tc.api.uploads()
	if len(uploads) != 1 || uploads[0].Method != "sendDocument" {
		t.Fatalf("the file did not go as a document: %+v", uploads)
	}
}

// The three that matter most: a file line may never reach a path outside the
// assistant's workspace. Without the boundary one line in one coder report
// would carry any file of this machine out of the house.
func TestAFileLineNeverLeavesTheWorkspace(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "id_rsa")
	if err := os.WriteFile(secret, []byte("very secret"), 0o600); err != nil {
		t.Fatalf("write the bait: %v", err)
	}

	for _, tt := range []struct {
		name string
		ref  string
		link string
	}{
		{name: "an absolute path", ref: secret},
		{name: "a path with ..", ref: "../../" + filepath.Base(secret)},
		{name: "a symlink out of the workspace", ref: "assistant-files/escape", link: secret},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tc := newTestChannel(t)
			tc.pair(42)
			if tt.link != "" {
				target := filepath.Join(tc.files.root, filepath.FromSlash(tt.ref))
				if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
					t.Fatalf("create dir: %v", err)
				}
				if err := os.Symlink(tt.link, target); err != nil {
					t.Fatalf("create the symlink: %v", err)
				}
			}
			tc.convs.setAnswer(assistant.Message{
				Role:    assistant.RoleAssistant,
				Content: "Look at this:\n[[datei: " + tt.ref + "]]",
				State:   assistant.StateComplete,
			})

			tc.Notify("conv-1")

			if uploads := tc.api.uploads(); len(uploads) != 0 {
				t.Fatalf("a file left the machine: %+v", uploads)
			}
			sent := tc.api.messages()
			if len(sent) != 1 {
				t.Fatalf("got %d messages, want the text with a line about the refusal", len(sent))
			}
			if !strings.Contains(sent[0], "was not sent") {
				t.Fatalf("the refusal is silent: %q", sent[0])
			}
			if strings.Contains(sent[0], "very secret") {
				t.Fatalf("the file's content leaked into the message: %q", sent[0])
			}
		})
	}
}

func TestAMissingFileBecomesALineInsteadOfSilence(t *testing.T) {
	tc := newTestChannel(t)
	tc.pair(42)
	tc.convs.setAnswer(assistant.Message{
		Role:    assistant.RoleAssistant,
		Content: "[[datei: assistant-files/gone.png]]",
		State:   assistant.StateComplete,
	})

	tc.Notify("conv-1")

	sent := tc.api.messages()
	if len(sent) != 1 || !strings.Contains(sent[0], "gone.png") || !strings.Contains(sent[0], "was not sent") {
		t.Fatalf("a missing file did not become a line: %q", sent)
	}
	if uploads := tc.api.uploads(); len(uploads) != 0 {
		t.Fatalf("something was uploaded: %+v", uploads)
	}
}

func TestAFileOverTheLimitBecomesALine(t *testing.T) {
	tc := newTestChannel(t)
	tc.pair(42)
	tc.files.write(t, "assistant-files/huge.bin", maxDocumentBytes+1)
	tc.convs.setAnswer(assistant.Message{
		Role:    assistant.RoleAssistant,
		Content: "[[datei: assistant-files/huge.bin]]",
		State:   assistant.StateComplete,
	})

	tc.Notify("conv-1")

	sent := tc.api.messages()
	if len(sent) != 1 || !strings.Contains(sent[0], "larger than") {
		t.Fatalf("an oversized file did not become a line: %q", sent)
	}
	if uploads := tc.api.uploads(); len(uploads) != 0 {
		t.Fatalf("an oversized file was uploaded: %+v", uploads)
	}
}

func TestAtMostFiveFilesPerAnswer(t *testing.T) {
	tc := newTestChannel(t)
	tc.pair(42)
	var content strings.Builder
	for i := 0; i < 7; i++ {
		name := "assistant-files/shot" + string(rune('1'+i)) + ".png"
		tc.files.write(t, name, 4)
		content.WriteString("[[datei: " + name + "]]\n")
	}
	tc.convs.setAnswer(assistant.Message{Role: assistant.RoleAssistant, Content: content.String(), State: assistant.StateComplete})

	tc.Notify("conv-1")

	if uploads := tc.api.uploads(); len(uploads) != maxOutgoingFiles {
		t.Fatalf("%d files were sent, want at most %d", len(uploads), maxOutgoingFiles)
	}
	if sent := tc.api.messages(); len(sent) != 1 || !strings.Contains(sent[0], "first 5 files") {
		t.Fatalf("the dropped files were not mentioned: %q", sent)
	}
}

func TestALongAnswerGoesOutInParts(t *testing.T) {
	tc := newTestChannel(t)
	tc.pair(42)
	tc.convs.setAnswer(assistant.Message{
		Role:    assistant.RoleAssistant,
		Content: strings.Repeat("a", 2000) + "\n\n" + strings.Repeat("b", 2000),
		State:   assistant.StateComplete,
	})

	tc.Notify("conv-1")

	if sent := tc.api.messages(); len(sent) != 2 {
		t.Fatalf("a long answer went out in %d messages", len(sent))
	}
}

func TestAnAnswerGoesOutFormatted(t *testing.T) {
	tc := newTestChannel(t)
	tc.pair(42)
	tc.convs.setAnswer(assistant.Message{
		Role:    assistant.RoleAssistant,
		Content: "The **release coder** is done, see `go test ./...`.",
		State:   assistant.StateComplete,
	})

	tc.Notify("conv-1")

	posts := tc.api.posts()
	if len(posts) != 1 {
		t.Fatalf("got %d messages, want one", len(posts))
	}
	if posts[0].ParseMode != "HTML" {
		t.Fatalf("the answer went out with parse_mode %q", posts[0].ParseMode)
	}
	if !strings.Contains(posts[0].Text, "<b>release coder</b>") || !strings.Contains(posts[0].Text, "<code>go test ./...</code>") {
		t.Fatalf("the markup was not translated: %q", posts[0].Text)
	}
}

// The fallback is the point of the whole formatting: a bug in the translator
// may cost the markup, never the answer.
func TestARefusedFormattedMessageGoesOutAsPlainText(t *testing.T) {
	captureLog(t)
	tc := newTestChannel(t)
	tc.pair(42)
	tc.api.refuseFormatted = true
	tc.convs.setAnswer(assistant.Message{
		Role:    assistant.RoleAssistant,
		Content: "The **release coder** is done.",
		State:   assistant.StateComplete,
	})

	tc.Notify("conv-1")

	posts := tc.api.posts()
	if len(posts) != 1 {
		t.Fatalf("got %d messages, want the plain one", len(posts))
	}
	if posts[0].ParseMode != "" {
		t.Fatalf("the fallback carried a parse_mode: %q", posts[0].ParseMode)
	}
	if posts[0].Text != "The **release coder** is done." {
		t.Fatalf("the fallback is not the answer itself: %q", posts[0].Text)
	}
}

// The two settings narrow the sending and nothing else. What the browser shows
// is the conversation, and the conversation is untouched by them.
func TestOnlyAnswersFromTheChatWhenTheChoiceSaysSo(t *testing.T) {
	for _, tt := range []struct {
		name   string
		source string
		sent   bool
	}{
		{"an answer to a question from the browser stays in the browser", "", false},
		{"an answer to a question from the chat goes out", assistant.SourceTelegram, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tc := newTestChannel(t)
			tc.pair(42)
			tc.store.Update(func(s *State) { s.Answers = DeliveryTelegram })
			answer := assistant.Message{
				ID:      "a1",
				Role:    assistant.RoleAssistant,
				Content: "the answer",
				State:   assistant.StateComplete,
				Source:  tt.source,
			}
			tc.convs.setAnswer(answer)
			tc.convs.setLast(answer)

			tc.Notify("conv-1")

			if sent := len(tc.api.messages()) > 0; sent != tt.sent {
				t.Fatalf("sent=%v, want %v: %q", sent, tt.sent, tc.api.messages())
			}
			// Whatever the switch says, the conversation keeps the answer: the
			// filter sits in front of the sending, not in front of the transcript.
			if _, ok := tc.convs.LastAnswer("conv-1"); !ok {
				t.Fatal("the answer left the conversation")
			}
			conversation, _ := tc.convs.Current()
			found := false
			for _, message := range conversation.Messages {
				if message.ID == "a1" {
					found = true
				}
			}
			if !found {
				t.Fatalf("the answer is gone from the transcript: %+v", conversation.Messages)
			}
		})
	}
}

func TestOnlyReportsOfJobsFromTheChatWhenTheChoiceSaysSo(t *testing.T) {
	for _, tt := range []struct {
		name   string
		source string
		sent   bool
	}{
		{"a report of a job steered from the page stays there", "", false},
		{"a report of a job started from the chat goes out", assistant.SourceTelegram, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tc := newTestChannel(t)
			tc.pair(42)
			tc.store.Update(func(s *State) { s.Reports = DeliveryTelegram })
			report := assistant.Message{
				ID:      "r1",
				Role:    assistant.RoleAssistant,
				Content: "the branch is pushed",
				State:   assistant.StateComplete,
				Wake: &assistant.WakeNote{
					Terminal: "abc",
					Name:     "release coder",
					Verdict:  string(assistant.VerdictDone),
					Source:   tt.source,
				},
			}
			tc.convs.setAnswer(report)
			tc.convs.setLast(report)

			tc.Notify("conv-1")

			if sent := len(tc.api.messages()) > 0; sent != tt.sent {
				t.Fatalf("sent=%v, want %v: %q", sent, tt.sent, tc.api.messages())
			}
			conversation, _ := tc.convs.Current()
			if len(conversation.Messages) == 0 {
				t.Fatal("the report is gone from the transcript")
			}
		})
	}
}

func TestTheWideChoiceCarriesEverything(t *testing.T) {
	tc := newTestChannel(t)
	tc.pair(42)
	// The default of a fresh channel: no origin anywhere, everything goes out.
	tc.convs.setAnswer(assistant.Message{Role: assistant.RoleAssistant, Content: "from the browser", State: assistant.StateComplete})

	tc.Notify("conv-1")

	if sent := tc.api.messages(); len(sent) != 1 {
		t.Fatalf("the default channel sent %d messages", len(sent))
	}
}
