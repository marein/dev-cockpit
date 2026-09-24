package render

import "html/template"

// AssistantCoderOption is one selectable coder on the new assistant control.
type AssistantCoderOption struct {
	ID    string
	Label string
}

// AssistantModelOption is one entry of a model select.
type AssistantModelOption struct {
	Value    string
	Label    string
	Selected bool
}

// AssistantModelPick is one model select as the page renders it: the field it
// posts under, the entries with the stored value selected, a stored value the
// list does not hold standing as an entry of its own, and Current, the stored
// value itself, which the Other… entry carries so that picking it without JS
// changes nothing. MaxRunes is what the typed field's maxlength says before
// the server has to refuse a name.
type AssistantModelPick struct {
	Field    string
	Current  string
	MaxRunes int
	Options  []AssistantModelOption
}

// AssistantModels is what the ring button's menu offers: the chat model, the
// check model and the trigger model of this assistant, each over the list its
// coder offers, and the line that says where that list comes from.
type AssistantModels struct {
	Chat    AssistantModelPick
	Check   AssistantModelPick
	Trigger AssistantModelPick
	Note    string
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
	// Triggers are the events this assistant reacts to; TriggersListURL is
	// what the page pulls when one of them moved and TriggersNewURL what the
	// plus in the aside's strip opens.
	Triggers        AssistantTriggersData
	TriggersListURL string
	TriggersNewURL  string
	// JobsOpen is how many of them still wake the assistant, TriggersOpen how
	// many triggers still fire, and WatchingOpen the two together. The button
	// that opens the aside shows the sum, because the aside holds both kinds
	// and one number has to stand for both: which of them it is stands a touch
	// later, on the two section heads inside.
	JobsOpen     int
	WatchingOpen int
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
	// Models is what that same button's menu offers: which models this
	// assistant's chat turns and its checks run on. Nil where the composer is
	// off, a blocked assistant has no coder to list models for.
	Models *AssistantModels
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

// AssistantTriggerView is one trigger on the assistant page: what fires it,
// what it asks for, and where it stands.
type AssistantTriggerView struct {
	ID string
	// Short is the id cut for a row, the whole id stays in the title.
	Short string
	// Owner is what the assistant holding it is called, rendered only in a
	// list that spans several; OwnerID is the same one as an id.
	Owner   string
	OwnerID string
	Source  string
	Kind    string
	// Name is what the user called it, empty for a trigger nobody named. With
	// one it is the row's heading and the event moves to the line under it,
	// without one the event is the heading, the way it always was.
	Name string
	// Label is what the event is called, Where narrows it: the coder's name,
	// "any job of mine", the schedule.
	Label string
	Where string
	// Heading is what the row reads by and what an answer calls it: the name
	// where there is one, the event and where it listens otherwise. One
	// reading, so the row, the confirm and the flash cannot say three things.
	Heading string
	// TargetURL opens the coder a terminal target names, empty without one.
	TargetURL string
	Spec      string
	// Timezone is the IANA name a schedule is read in, empty on every other
	// event. It stands on the same line as the cron fields, in Where.
	Timezone string
	Task     string
	Once     bool
	State    string
	Open     bool
	Fired    int
	// Next is the next tick of a schedule, already written out as the wall
	// clock of the schedule's own zone with that zone named, because a
	// schedule is a statement about wall clock time and dc-time would answer
	// in the browser's zone. Until is the expiry, which is a moment and not a
	// wall clock, so it stays a machine stamp (RFC3339) for dc-time. Both are
	// empty where there is none.
	Next  string
	Until string
	// Note is the last thing that happened to it, one line.
	Note string
	// Model is the model its reactions run on, empty for one that follows the
	// owner's chat model, which is what most triggers do.
	Model string
	// EditURL opens the form that changes it, empty on one that is over: a
	// spent trigger cannot be changed, and the row carrying nothing is what
	// keeps the button off it.
	EditURL string
	// Pending says events wait in the open batch window right now, Reacting
	// that the reaction it bought runs right now, in a session of its own, and
	// Broke that the last one ended without arriving.
	Pending  int
	Reacting bool
	Broke    bool
}

// AssistantTriggerTarget is one terminal the form offers as a target, for the
// job or the coder events.
type AssistantTriggerTarget struct {
	Terminal string
	Name     string
	Project  string
	// Picked says this terminal is one the form opens on, which is what an
	// edit and a trigger begun on a coder's row carry.
	Picked bool
}

// AssistantTriggersData is the model of the triggers fragment: the rows of one
// assistant and what its form offers.
type AssistantTriggersData struct {
	Page
	Owner    string
	Triggers []AssistantTriggerView
	// Open is how many still fire.
	Open int
	// Older is how many spent triggers the list holds back, so nothing is
	// dropped in silence, the way the closed jobs say it.
	Older int
	// URL is the path a row's own action posts to.
	URL string
	// Owners says the list spans several assistants, so every row names
	// whose it is.
	Owners bool
}

// AssistantTriggerFormData is the one form that makes a trigger and the one
// that changes it: the page renders it in a card, the create dialog asks for
// the same GET with modal=1 and gets it alone, the way the create forms do.
// Every field starts on the stand the server rendered into it, so an edit is
// the same markup filled and nothing fills a form in the browser.
type AssistantTriggerFormData struct {
	Page
	Modal bool
	// Edit is the trigger this form changes, empty for a new one. It is what
	// the POST dispatches on and what locks the event: another event is
	// another trigger.
	Edit string
	// Owner is the assistant the trigger belongs to, URL the path it posts to
	// and Return where the page's way out leads.
	Owner  string
	URL    string
	Return string
	// Events are the kinds the form offers, every one the CLI offers too.
	Events []AssistantEventOption
	// Jobs and Coders are the terminals it offers as targets: the owner's open
	// jobs for a job event, the running coders for a coder event. A target the
	// stand names that neither list holds any more rides along picked, so
	// saving does not drop it.
	Jobs   []AssistantTriggerTarget
	Coders []AssistantTriggerTarget
	// What every other field stands on.
	Event string
	// Name is the optional heading, and MaxName how long it may be, which is
	// what the field's maxlength says before the server has to refuse it.
	Name    string
	MaxName int
	Task    string
	Spec    string
	// Timezone is the zone the schedule is read in: what the trigger carries
	// on a change, and the default a new one starts on. It is always filled,
	// so the field never stands empty and nobody has to guess what a schedule
	// with nothing in it would mean.
	Timezone string
	// Model is the select for the model the reaction runs on, over the list
	// the owner's coder offers with the empty entry reading Same as the
	// assistant, and ModelNote the line under it saying where that list comes
	// from.
	Model     AssistantModelPick
	ModelNote string
	Mode      string
	Once      bool
	Batch     int
	// The expiry as the form asks for it: a number with the unit beside it,
	// and the box that says there is none. UntilCount is zero where nothing
	// expires, which is where a new trigger starts, and the field then stands
	// empty with the box ticked. Until is the moment that stands, a machine
	// stamp for dc-time, shown as the hint beside the two fields so an edit
	// reads the date as well as the span. Empty is no expiry.
	UntilCount int
	UntilUnit  string
	Until      string
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
}

// AssistantNoteView describes a note: where it came from and the line it is
// read by. A check's report carries its verdict and the coder it was about.
// What the note holds beyond that line stays out of it: the event, the task
// and the events one reaction bundles are read on the trigger and in the
// headline, never in the thread.
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
	// URL opens the coder the note is about, empty without one.
	URL string
	// Preview is the first words of the message this note stands over, cut to
	// what a push carries; Rest says the message holds more than that, which
	// unfolds under the preview on a tap.
	Preview string
	Rest    bool
}

// AssistantMessageData is the model for the single-message fragment the browser
// pulls when a streamed answer finished.
type AssistantMessageData struct {
	Message AssistantMessageView
}
