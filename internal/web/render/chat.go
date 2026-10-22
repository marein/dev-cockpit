package render

import "html/template"

// ChatMessageView is one bubble, rendered by chat_message.gohtml. The
// assistant's thread and a coder's copy view share it: same markup, same
// stripes, same head with the author and the time. What only one of them has
// (the assistant's attachments, retry and speaker; the coder's parts, tool
// lines and copy button) stands in the struct behind a field the other
// surface leaves empty, so one template renders both.
type ChatMessageView struct {
	ID    string
	RunID string
	User  bool
	// Command marks a user message that is a command the user ran, a slash
	// command or a shell escape: it renders as one chip carrying the line.
	Command bool
	// Answered marks a user message sent from the questions of an answer. It
	// stands as one slim row, what was picked shows in the block above.
	Answered bool
	// Coder marks a coder's own message in the copy view, which wears the
	// coder's stripe rather than the assistant's.
	Coder bool
	// Note is set on a message the cockpit wrote, the third role: a check's
	// report. It never renders as something the user said.
	Note *AssistantNoteView
	// Auto marks an answer started without the user: a reaction to an event
	// pushed it, and Origin is the one line it is read by, the header over the
	// answer. What the trigger was given is not rendered: the thread holds the
	// result, the way a check's report does.
	Auto        bool
	Origin      *AssistantNoteView
	Author      string
	Text        string
	HTML        template.HTML
	Attachments []AssistantAttachmentView
	State       string
	Error       string
	Streaming   bool
	Failed      bool
	CanRetry    bool
	// Queued marks a message still waiting for the running turn to end, and
	// CanDiscard says the page may still take it back. A waiting entry in a
	// blocked assistant renders without the button.
	Queued     bool
	CanDiscard bool
	Time       string
	Model      string
	ModelName  string
	// AudioURL serves this message spoken, set only while text to speech is on
	// and the message can be read aloud at all, a check's report as much as an
	// answer; the speaker button hangs on it alone, in both headers.
	AudioURL string
	// Parts are a message's pieces in order: a coder message's texts and tool
	// calls, an answer's text around the questions it asks. A message with
	// parts renders them instead of HTML.
	Parts []ChatPartView
	// Copy is what the message's copy button puts on the clipboard, the words
	// as recorded; empty renders no button.
	Copy string
}

// ChatMessageData is the model of chat_message.gohtml, one bubble.
type ChatMessageData struct {
	Message ChatMessageView
}

// ChatPartView is one piece of a message: a rendered text, a tool call as one
// compact line, or the questions of an answer.
type ChatPartView struct {
	// HTML is the piece as markup: the text rendered, or a tool call's line
	// with its file links.
	HTML template.HTML
	Tool string
	// Line says what the call was about.
	Line    string
	Choices *ChatChoicesView
}

// ChatChoicesView is the questions an answer asks, rendered by
// chat_choices.gohtml. PostURL is set while the page may still answer them,
// FragmentURL is the message the block reloads itself from once it did,
// DraftURL is where the picks and notes of the draft of AssistantID are read,
// Done how many questions they answer. Answered and Passed say how a block
// that is over ended: answered, or left behind by a message of the user that
// answered nothing.
type ChatChoicesView struct {
	MessageID   string
	AssistantID string
	Questions   []ChatQuestionView
	NoteMax     int
	PostURL     string
	FragmentURL string
	DraftURL    string
	Done        int
	Answered    bool
	Passed      bool
}

// State names the block's state for the page and its runners.
func (v ChatChoicesView) State() string {
	switch {
	case v.Answered:
		return "answered"
	case v.Passed:
		return "passed"
	case v.PostURL != "":
		return "open"
	}
	return "readonly"
}

// ChatQuestionView is one question with its options, Multi where it takes
// several, the words the user wrote beside it once answered, and the words of
// the draft while it is open.
type ChatQuestionView struct {
	Text    string
	Options []ChatOptionView
	Multi   bool
	Note    template.HTML
	Draft   string
}

// ChatOptionView is one option, Picked once the user's answer or the draft
// picked it.
type ChatOptionView struct {
	Text   string
	Picked bool
}

// CoderCopyData is the model of coder_copy_page.gohtml, one page of a
// coder's conversation in the copy view.
type CoderCopyData struct {
	Messages []ChatMessageView
	// Dropped is how many older messages stand before the page, and Before
	// the id the page that fetches them asks with.
	Dropped int
	Before  string
	// Note says why the page is empty.
	Note string
}
