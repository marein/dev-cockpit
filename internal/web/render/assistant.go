package render

import "html/template"

// AssistantCoderOption is one selectable coder on the new assistant control.
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

// AssistantJobView is one steered job on the assistant page: what an assistant
// keeps an eye on, what it costs, and where it stands.
type AssistantJobView struct {
	Terminal string
	// Owner is what the assistant steering this job is called. A list narrowed
	// to one assistant does not render it, a list of everybody's does: with
	// several of them the question "who holds this coder" is the whole point of
	// the list. OwnerID is the same assistant as an id, which steering the same
	// coder again posts back, so a job that comes up a second time comes up for
	// the one that had it.
	Owner    string
	OwnerID  string
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
	// the browser locale like the message timestamps.
	Expires string
	// URL opens the coder the job steers, EditorURL the editor on the project
	// it works in: the two ways to look at what a steered coder is doing, the
	// screen it types on and the files it writes. Empty where the job names no
	// project, which is what a coder outside one is.
	URL       string
	EditorURL string
}

// AssistantData is the model for one assistant's surface. An empty ID is the
// area with no assistant in it at all: the work column is then the empty state
// whose one control makes the first one.
type AssistantData struct {
	Page
	ID string
	// Name is what this assistant is called, in the work head and the same
	// name the list column shows; empty while nobody has spoken in it.
	Name string
	// Path is the page's own address: the list column marks its row, and the
	// page pulls it again to catch up after an assistant event.
	Path string
	// Ctx is the list column beside it.
	Ctx        *AssistantCtxData
	CoderID    string
	CoderLabel string
	// Coders is only used when more than one is installed: the new assistant
	// control then asks which one answers.
	Coders   []AssistantCoderOption
	Messages []AssistantMessageView
	Running  bool
	// Blocked carries the reason the composer is off. Empty means the
	// assistant accepts messages.
	Blocked string
	// NewCoderID is the coder a new assistant started from this one runs on. It
	// is empty when this assistant's coder is gone, then the new one picks
	// whichever coder is installed.
	NewCoderID string
	// Jobs are the coders this assistant steers. Empty when nothing is
	// steered, which is the normal state.
	Jobs []AssistantJobView
	// Subscriptions are the events this assistant reacts to, with what the
	// form to make one offers; SubscriptionsListURL is what the page pulls
	// when one of them moved.
	Subscriptions        AssistantSubscriptionsData
	SubscriptionsListURL string
	// JobsOpen is how many of them still wake the assistant. It is what the
	// button that opens the list shows without being opened: the icon carries
	// it the way every other status in the cockpit does, through its colour.
	JobsOpen int
	// JobsOlder is how many closed jobs the list holds back, so nothing is
	// dropped silently. The open jobs always all render.
	JobsOlder int
	// JobsURL is the one path the two actions on a job post to, whoever steers
	// it. JobsListURL is the same path narrowed to this assistant, which is
	// what the page pulls when a check changed something and what the badge
	// counts: the aside shows the coders this assistant holds, not everybody's.
	JobsURL     string
	JobsListURL string
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
	// Draft is the unsent message this assistant holds, rendered straight into
	// the message box, and DraftFiles are the files that were uploaded for it,
	// as the JSON the composer rebuilds its chips from. Both come from the
	// stored assistant, so the draft is there on the next device too.
	Draft      string
	DraftFiles string
	// DraftURL serves the stored draft on its own, which is how a second device
	// catches up after a save somewhere else and after a reconnect.
	DraftURL string
	// ContextPercent is how full the coder's context window stood at the end of
	// the last turn, drawn as the ring around the new assistant button. Zero
	// means there is nothing to show, either because no turn reported a reading
	// yet or because that model's window is unknown, and the ring then stays
	// empty: a wrong percentage is worse than none. It is rendered here as well
	// as pushed with the end frame, so opening the panel shows the number
	// without waiting for a turn.
	ContextPercent int
}

// AssistantCard is one row of the assistant list: what it is called, what it
// holds and what it is doing. The same name `dev-cockpit assistant
// assistant-list` prints. The preview of the last message is not on the row,
// the rows are read as a list of who is who; it stays in the summary and in
// what the CLI prints.
type AssistantCard struct {
	ID         string
	Title      string
	CoderLabel string
	URL        string
	Messages   int
	// Running says a turn is being written right now, and Unfinished that the
	// last one stopped before it was done. They come apart at the source, so a
	// row shows the run as work and never as a fault.
	Running    bool
	Unfinished bool
	// OpenJobs is how many coders this assistant steers right now. It is on the
	// row because with several assistants that is the one thing a glance has to
	// answer: who is holding what.
	OpenJobs int
	// News says this assistant has an unread answer. The rail carries one dot
	// for the whole area, so this is where the reader sees which one it is in.
	News bool
	// Updated is a machine stamp (RFC3339), the dc-time element renders it in
	// the browser locale.
	Updated string
}

// AssistantCtxData is the list column of the assistant page, and what
// /ctx/assistants answers for the phone's sheet: every assistant that lives, in
// the order the rows were dragged into, and the control that makes another one
// in the head.
type AssistantCtxData struct {
	Page
	// Path is the page the column stands beside: its row is active, and the
	// column refreshes itself from /ctx/assistants with the same path.
	Path string
	// Assistants are all of them. There is no current one and no earlier ones:
	// every row is a live assistant that takes messages.
	Assistants []AssistantCard
	ActiveID   string
	Available  bool
	// Coders and NewCoderID drive the new assistant control, PostURL is where
	// its forms post, and IDPrefix keeps those forms' ids apart from the
	// composer's and between the page's column and the sheet's.
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

// AssistantSubscriptionView is one subscription on the assistant page: what
// fires it, what it asks for, and where it stands.
type AssistantSubscriptionView struct {
	ID string
	// Short is the id cut for a row, the whole id stays in the title.
	Short string
	// Owner is what the assistant holding it is called, rendered only in a
	// list that spans several; OwnerID is the same one as an id.
	Owner   string
	OwnerID string
	Source  string
	Kind    string
	// Label is what the event is called, Where narrows it: the coder's name,
	// "any job of mine", the schedule.
	Label string
	Where string
	// TargetURL opens the coder a terminal target names, empty without one.
	TargetURL string
	Spec      string
	Task      string
	Once      bool
	State     string
	Open      bool
	Fired     int
	// Next is the next tick of a schedule, Until the expiry, both machine
	// stamps (RFC3339) for dc-time, empty where there is none.
	Next  string
	Until string
	// Note is the last thing that happened to it, one line.
	Note string
	// Edit is the stand of every field the form offers, as JSON, so the row's
	// Edit opens that same form filled without a request of its own. It stands
	// only on a subscription that still fires: one that is over cannot be
	// changed, and the row carrying nothing is what keeps the entry out of its
	// menu.
	Edit string
	// Pending says events wait in the open batch window right now, Reacting
	// that the reaction it bought runs right now, in a session of its own.
	Pending  int
	Reacting bool
}

// AssistantSubscriptionTarget is one terminal the form offers as a target,
// for the job or the coder events.
type AssistantSubscriptionTarget struct {
	Terminal string
	Name     string
	Project  string
}

// AssistantSubscriptionsData is the model of the subscriptions fragment: the
// rows of one assistant and what its form offers.
type AssistantSubscriptionsData struct {
	Page
	Owner         string
	Subscriptions []AssistantSubscriptionView
	// Open is how many still fire.
	Open int
	// Events are the kinds the form offers, every one the CLI offers too.
	Events []AssistantEventOption
	// Jobs and Coders are the terminals the form offers as targets: the
	// owner's open jobs for a job event, the running coders for a coder
	// event.
	Jobs   []AssistantSubscriptionTarget
	Coders []AssistantSubscriptionTarget
	URL    string
	// Owners says the list spans several assistants, so every row names
	// whose it is.
	Owners bool
}

// AssistantEventOption is one event the form's select offers.
type AssistantEventOption struct {
	Name   string
	Source string
	Label  string
	Help   string
}

// AssistantMessageView is one rendered message. User text stays plain,
// assistant answers are rendered from Markdown server-side with raw HTML
// disabled.
type AssistantMessageView struct {
	ID    string
	RunID string
	User  bool
	// Note is set on a message the cockpit wrote, the third role: a check's
	// report. It never renders as something the user said.
	Note *AssistantNoteView
	// Auto marks an answer started without the user: a reaction to an event
	// pushed it, and Origin says which event and which task, rendered as a
	// folded header over the answer.
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
	// AudioURL serves this answer spoken, set only while text to speech is on
	// and the answer is complete; the speaker button renders from it.
	AudioURL string
}

// AssistantNoteView describes a note: where it came from and the line it is
// read by. A check's report carries its verdict and the coder it was about;
// an event note the subscription and how many events it bundles.
type AssistantNoteView struct {
	Source   string
	Headline string
	Verdict  string
	Terminal string
	Name     string
	Done     bool
	Blocked  bool
	// Expired marks the one message a job writes when it ran out of checks or
	// out of time, so the reader knows nobody is looking any more.
	Expired bool
	Count   int
	Task    string
	// URL opens the coder the note is about, empty without one.
	URL string
	// First is the first line of the body, shown folded; Rest says there is
	// more behind it, which unfolds on tap.
	First string
	Rest  bool
}

// AssistantMessageData is the model for the single-message fragment the browser
// pulls when a streamed answer finished.
type AssistantMessageData struct {
	Message AssistantMessageView
}
