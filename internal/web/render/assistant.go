package render

import "html/template"

// AssistantCoderOption is one selectable coder on the new conversation control.
type AssistantCoderOption struct {
	ID    string
	Label string
}

// AssistantAttachmentView is one file a message carries, ready to embed.
type AssistantAttachmentView struct {
	Name     string
	URL      string
	Media    string
	SizeText string
	// Width and Height are the pixel size of a picture, zero when it could not
	// be read. They render as the image's attributes, so its space stands
	// before the browser has the file: without them a transcript full of
	// screenshots rearranges itself under the reader while they arrive.
	Width  int
	Height int
}

// AssistantJobView is one steered job on the conversation page: what the
// assistant keeps an eye on, what it costs, and where it stands.
type AssistantJobView struct {
	Terminal string
	Name     string
	Project  string
	Task     string
	DoneWhen string
	State    string
	Open     bool
	// Checking says a check is running on this job right now, so the page can
	// show it instead of leaving the user to read a counter.
	Checking bool
	Note     string
	Wakes    int
	MaxWakes int
	// Expires is a machine stamp (RFC3339); the dc-time element renders it in
	// the browser locale like the conversation timestamps.
	Expires string
	// URL opens the coder the job steers.
	URL string
}

// AssistantData is the model for the assistant conversation surface, rendered
// as the full page and as the interior of the docked panel.
type AssistantData struct {
	Page
	ID string
	// ConversationTitle names the conversation in the work head, the same
	// title the list column shows; empty while nobody has spoken in it.
	ConversationTitle string
	// Path is the page's own address: the list column marks its row, and the
	// page pulls it again to catch up after an assistant event.
	Path string
	// Ctx is the list column beside the conversation.
	Ctx        *AssistantCtxData
	CoderID    string
	CoderLabel string
	// Coders is only used when more than one is installed: the new
	// conversation control then asks which one answers.
	Coders   []AssistantCoderOption
	Messages []AssistantMessageView
	Running  bool
	// Blocked carries the reason the composer is off. Empty means the
	// assistant accepts messages.
	Blocked string
	// NewCoderID is the coder a new conversation started from a blocked one
	// runs on. It is empty when this conversation's coder is gone, then the
	// new one picks whichever coder is installed.
	NewCoderID string
	// CurrentURL points at the conversation that still takes messages, set
	// only while looking at a different one. An earlier conversation then
	// sends the reader on instead of offering to start another one.
	CurrentURL string
	// Jobs are the coders the assistant steers. Empty when nothing is
	// steered, which is the normal state.
	Jobs []AssistantJobView
	// JobsOpen is how many of them still wake the assistant. It is what the
	// button that opens the list shows without being opened: the icon carries
	// it the way every other status in the cockpit does, through its colour.
	JobsOpen int
	// JobsOlder is how many closed jobs the list holds back, so nothing is
	// dropped silently. The open jobs always all render.
	JobsOlder int
	// JobsURL is the jobs path: it serves the list on its own, so the page can
	// pull it when a check changed something, and it takes the two actions on a
	// job. One path, so the forms post where the fragment came from.
	JobsURL string
	// EarlierCount is how many messages are held back above the rendered
	// window, and AllURL renders the whole transcript, anchored at the oldest
	// message that was already on the page.
	EarlierCount int
	AllURL       string
	StreamURL    string
	PostURL      string
	MessageURL   string
	UploadURL    string
	// SttURL takes a recorded clip and answers its transcript. Empty while
	// speech to text is off, which is what takes the talk button away.
	SttURL string
	// TTS says the spoken answers are on: the messages carry their audio
	// routes and the composer offers the voice mode toggle.
	TTS            bool
	MaxPromptBytes int
	MaxUploadBytes int64
	HistoryCount   int
	// Draft is the unsent message this conversation holds, rendered straight
	// into the message box, and DraftFiles are the files that were uploaded
	// for it, as the JSON the composer rebuilds its chips from. Both come from
	// the conversation, so the draft is there on the next device too.
	Draft      string
	DraftFiles string
	// DraftURL serves the stored draft on its own, which is how a second device
	// catches up after a save somewhere else and after a reconnect.
	DraftURL string
	// ContextPercent is how full the coder's context window stood at the end of
	// the last turn, drawn as the ring around the new conversation button. Zero
	// means there is nothing to show, either because no turn reported a reading
	// yet or because that model's window is unknown, and the ring then stays
	// empty: a wrong percentage is worse than none. It is rendered here as well
	// as pushed with the end frame, so opening the panel shows the number
	// without waiting for a turn.
	ContextPercent int
}

// AssistantCard is one row on the assistant history. The title names the
// conversation, the same way `dev-cockpit assistant conversation-list` prints it,
// and the preview of the last answer sits under it.
type AssistantCard struct {
	ID         string
	Title      string
	Preview    string
	CoderLabel string
	URL        string
	Messages   int
	// Running says a turn is being written right now, and Unfinished that the
	// last one stopped before it was done. They come apart at the source, so a
	// row shows the run as work and never as a fault.
	Running    bool
	Unfinished bool
	Current    bool
	// Updated is a machine stamp (RFC3339), the dc-time element renders it in
	// the browser locale.
	Updated string
}

// AssistantHistoryData is the model for the earlier conversations.
type AssistantHistoryData struct {
	Page
	Conversations []AssistantCard
	Available     bool
	// CurrentURL is the conversation that still takes messages. While there is
	// one, the page sends the reader there instead of offering to start
	// another one next to it.
	CurrentURL string
}

// AssistantCtxData is the list column of the assistant page, and what
// /ctx/assistant answers for the phone's sheet: the conversation that still
// takes messages under its own head, the rest under Earlier, and the new
// conversation control in the head.
type AssistantCtxData struct {
	Page
	// Path is the page the column stands beside: its row is active, and the
	// column refreshes itself from /ctx/assistant with the same path.
	Path string
	// Current is the conversation that still takes messages, nil while none
	// exists; Answering says a turn runs in it right now, read off that card so
	// the head and the row say the same thing.
	Current   *AssistantCard
	Answering bool
	Earlier   []AssistantCard
	ActiveID  string
	Available bool
	// Coders and NewCoderID drive the new conversation control, PostURL is
	// where its forms post, and IDPrefix keeps those forms' ids apart from
	// the composer's and between the page's column and the sheet's.
	Coders     []AssistantCoderOption
	NewCoderID string
	PostURL    string
	IDPrefix   string
}

// AssistantMemoryEntry is one thing the assistant knows about the user.
type AssistantMemoryEntry struct {
	Slug  string
	Title string
	Body  string
	// Updated is a machine stamp (RFC3339), the dc-time element renders it in
	// the browser locale.
	Updated string
}

// AssistantMemoryData is the model for the memory page. Editing happens in
// place, each row carries its own prefilled form.
type AssistantMemoryData struct {
	Page
	Entries []AssistantMemoryEntry
	// Prefix names the add form's ids, so a second rendering of the list in
	// one document could keep its own.
	Prefix string
}

// AssistantMessageView is one rendered message. User text stays plain, assistant
// answers are rendered from Markdown server-side with raw HTML disabled.
type AssistantMessageView struct {
	ID    string
	RunID string
	User  bool
	// Wake is set on a message a check wrote: which coder it was about and what
	// it concluded. It never renders as something the user said.
	Wake        *AssistantWakeView
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
	// read-only conversation renders as never sent instead.
	Queued     bool
	CanDiscard bool
	Time       string
	// AudioURL serves this answer spoken, set only while text to speech is on
	// and the answer is complete; the speaker button renders from it.
	AudioURL string
}

// AssistantWakeView describes the check a message came from.
type AssistantWakeView struct {
	Terminal string
	Name     string
	Verdict  string
	Done     bool
	Blocked  bool
	// Expired marks the one message a job writes when it ran out of checks or
	// out of time, so the reader knows nobody is looking any more.
	Expired bool
	URL     string
}

// AssistantMessageData is the model for the single-message fragment the browser
// pulls when a streamed answer finished.
type AssistantMessageData struct {
	Message AssistantMessageView
}
