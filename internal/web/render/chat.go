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
	// AudioURL serves this message spoken, set only while text to speech is on
	// and the message can be read aloud at all, a check's report as much as an
	// answer; the speaker button hangs on it alone, in both headers.
	AudioURL string
	// Parts are a coder message's pieces in record order, text and tool
	// calls. A message with parts renders them instead of HTML.
	Parts []ChatPartView
	// Copy is what the message's copy button puts on the clipboard, the words
	// as recorded; empty renders no button.
	Copy string
}

// ChatMessageData is the model of chat_message.gohtml, one bubble.
type ChatMessageData struct {
	Message ChatMessageView
}

// ChatPartView is one piece of a coder message: a rendered text, or a tool
// call as one compact line.
type ChatPartView struct {
	HTML template.HTML
	Tool string
	// Line says what the call was about.
	Line string
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
