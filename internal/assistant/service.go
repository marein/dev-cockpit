package assistant

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/local/dev-cockpit/internal/statefile"
	"github.com/local/dev-cockpit/internal/terminal"
)

// maxConcurrentRuns bounds the chat turns across every conversation, the ones a
// user asked for; a check has slots of its own and never takes one of these. A
// second request beyond it is refused as busy, never silently queued: a queued
// prompt looks like a hung page and can charge the user minutes after they gave up.
const maxConcurrentRuns = 2

// ErrBusy is returned when the global generation limit is reached.
var ErrBusy = errors.New("The coders are busy with other conversations right now. Try again in a moment.")

// Run identifies a started generation for the browser. ReplacedID names the
// message a retry dropped, so the page can remove that bubble instead of
// showing the failed turn next to its replacement.
type Run struct {
	RunID      string
	MessageID  string
	ReplacedID string
	// UserMessageID names the prompt that was just written into the transcript.
	// The page that sent it already shows it, and the same message is announced
	// on the stream for the other pages, so this is how the sender recognizes
	// the announcement of its own message instead of showing it twice.
	UserMessageID string
	// Title is the conversation title after this turn, so a page that derived its
	// title from the first prompt can show it without a reload.
	Title string
	// Queued says no generation started: the message waits in the transcript
	// until the running turn ends and the server flushes the queue.
	Queued bool
}

// Service owns conversation state, provider processes and the per conversation streams.
type Service struct {
	store *Store
	// runs is the register of turns that are on the machine right now. It is on
	// disk, because a turn is not a child of this process and the next server
	// has to be able to find it.
	runs     *RunStore
	coders   Coders
	projects Projects
	now      func() time.Time
	hub      *hub
	// chatSlots bound the generations the user's own messages run in. A check
	// never takes one of these, which is what keeps the chat free while it runs.
	chatSlots *slots

	// mu guards running and every state transition. Nothing under this lock may
	// call back into coder.Manager snapshots, and the change hooks fire after
	// it is released.
	mu sync.Mutex
	// running are the turns this server is following, by run id.
	running map[string]*activeRun

	// recovering holds the queue flush back while Recover walks the register:
	// an orphaned turn settles during that walk, and a flush started there
	// could run next to a still unregistered turn of the same conversation.
	// Guarded by mu.
	recovering bool

	// reserved has a lock of its own because it sits on a different hot path:
	// every coder snapshot asks it, and it must not queue behind the transcript
	// writes of a streaming answer. Its two writers (create, and the transfer
	// releasing a session) both hold mu as well, so a reader only ever observes
	// the state before or after such a change, never a half applied one: the
	// reservation is dropped after the terminal is up and its status persisted,
	// so a session is either hidden as a conversation or owned by a live terminal.
	reservedMu sync.RWMutex
	reserved   map[string]bool

	onChange func()
	onDone   func(conversationID string)
	render   func(string) (string, error)
}

// renderFloor is the shortest gap between two rendered prefixes while an answer
// streams. It grows with the answer, because every frame carries the whole
// prefix and a long answer would otherwise resend megabytes to a phone.
const renderFloor = 300 * time.Millisecond

// maxRenderBytes stops the live rendering for answers past this size. The final
// message is rendered once, as always.
const maxRenderBytes = 128 << 10

func renderInterval(size int) time.Duration {
	return renderFloor + time.Duration(size/64)*time.Millisecond
}

// newService wires the conversation service and reconciles the conversation
// state a previous process left behind. The turns that process left running are
// picked up separately by Recover, which needs the rest of the cockpit to exist
// first. The package entry point is New, which binds it to the assistant.
func newService(store *Store, runs *RunStore, coders Coders, projects Projects) *Service {
	s := &Service{
		store:     store,
		runs:      runs,
		coders:    coders,
		projects:  projects,
		now:       time.Now,
		hub:       newHub(),
		chatSlots: newSlots(maxConcurrentRuns),
		running:   map[string]*activeRun{},
		reserved:  map[string]bool{},
	}
	s.reconcile()
	return s
}

// SetHooks registers the coarse change publisher and the completion
// notification. Both run outside the service lock.
func (s *Service) SetHooks(onChange func(), onDone func(conversationID string)) {
	s.onChange = onChange
	s.onDone = onDone
}

// SetRenderer installs the Markdown renderer used while an answer streams. The
// browser never parses model output itself, it only shows what this produced.
func (s *Service) SetRenderer(render func(string) (string, error)) {
	s.render = render
}

// reconcile seeds the reservation set and enforces the one live conversation
// rule, which a crash between archiving and creating could otherwise leave
// broken. What became of the turns that were running is not decided here: their
// processes may still be writing, and Recover asks them.
func (s *Service) reconcile() {
	live := false
	for _, entry := range s.store.List() {
		if entry.Status != StatusActive {
			continue
		}
		if !live {
			live = true
			s.reserved[reservationKey(entry.CoderID, entry.ID)] = true
			continue
		}
		c, ok := s.store.Load(entry.ID)
		if !ok {
			log.Printf("assistant: transcript for %s is missing, keeping the index entry", entry.ID)
			continue
		}
		c.Status = StatusArchived
		c.UpdatedAt = s.now().UTC()
		s.store.Save(c)
		// A turn of that conversation may still be writing. Its session is
		// dropped when the turn ends, the same way a conversation archived
		// during a running turn is treated.
		if !s.registered(c.ID) {
			s.dropSessionLocked(c)
		}
	}
}

// registered reports whether a turn of this conversation is in the register, so
// nothing takes a provider session away from a process that is still using it.
func (s *Service) registered(conversationID string) bool {
	for _, rec := range s.runs.List() {
		if rec.Conversation == conversationID && rec.Kind == RunChat {
			return true
		}
	}
	return false
}

// Coders returns the coders that can answer a turn.
func (s *Service) Coders() []CoderInfo { return s.coders.Available() }

func (s *Service) coder(id string) (CoderInfo, bool) {
	for _, c := range s.coders.Available() {
		if c.ID == id {
			return c, true
		}
	}
	return CoderInfo{}, false
}

// List returns the conversation index, newest activity first.
func (s *Service) List() []Summary { return s.store.List() }

// Get loads one conversation.
func (s *Service) Get(id string) (Conversation, error) {
	c, ok := s.store.Load(id)
	if !ok {
		return Conversation{}, errors.New("Conversation not found.")
	}
	return c, nil
}

// Search returns the conversation index, newest activity first, narrowed to
// the conversations where a word appears in the title or in a message,
// compared case insensitively. An empty word returns the whole index. The
// title is answered from the index alone, the message match loads the
// transcript; nothing here writes.
func (s *Service) Search(word string) []Summary {
	entries := s.store.List()
	word = strings.ToLower(strings.TrimSpace(word))
	if word == "" {
		return entries
	}
	out := make([]Summary, 0, len(entries))
	for _, entry := range entries {
		if strings.Contains(strings.ToLower(entry.Title), word) {
			out = append(out, entry)
			continue
		}
		c, ok := s.store.Load(entry.ID)
		if !ok {
			continue
		}
		for _, m := range c.Messages {
			if strings.Contains(strings.ToLower(m.Content), word) {
				out = append(out, entry)
				break
			}
		}
	}
	return out
}

// TranscriptEntriesShown is how many messages a transcript reading keeps by
// default: enough to see where a conversation stands, small enough to stay a
// fraction of the answer that carries it.
const TranscriptEntriesShown = 6

// TranscriptMessageRunes is how many runes one message may cost in a capped
// transcript reading. Callers pass it as the budget; zero lifts the cap, for
// the one answer that needs a message whole.
const TranscriptMessageRunes = 600

// Transcript returns one conversation windowed to its last entries, each
// message cut to budget runes with a note saying how much of it is shown.
// entries zero or less means the default window, budget zero or less keeps
// every message whole. The second return is how many older messages the
// window dropped. It only reads, the stored transcript stays as it is.
func (s *Service) Transcript(id string, entries, budget int) (Conversation, int, error) {
	c, ok := s.store.Load(id)
	if !ok {
		return Conversation{}, 0, errors.New("Conversation not found.")
	}
	if entries <= 0 {
		entries = TranscriptEntriesShown
	}
	dropped := 0
	if len(c.Messages) > entries {
		dropped = len(c.Messages) - entries
		c.Messages = c.Messages[dropped:]
	}
	if budget > 0 {
		for i := range c.Messages {
			c.Messages[i].Content = cutMessage(c.Messages[i].Content, budget)
		}
	}
	return c, dropped, nil
}

// cutMessage cuts one message to max runes, visibly: a cut message says how
// much of it is shown and how the rest is reached, never a bare ellipsis.
func cutMessage(text string, max int) string {
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return fmt.Sprintf("%s… [cut: %d of %d runes shown, use --full for the whole message]", string(runes[:max]), max, len(runes))
}

// LastAnswer returns the newest assistant message of a conversation. The
// notification for a finished turn is raised right after that turn ended, so
// this is the message it is about: it names the answer in the entry and lets
// the entry link straight at it.
func (s *Service) LastAnswer(id string) (Message, bool) {
	c, ok := s.store.Load(id)
	if !ok {
		return Message{}, false
	}
	for i := len(c.Messages) - 1; i >= 0; i-- {
		if c.Messages[i].Role == RoleAssistant {
			return c.Messages[i], true
		}
	}
	return Message{}, false
}

// UploadDir is the directory holding the uploads of one conversation.
func (s *Service) UploadDir(id string) (string, error) { return s.store.UploadDir(id) }

// Running reports whether a generation is in flight for this conversation.
func (s *Service) Running(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.chatRunLocked(id) != nil
}

// chatRunLocked is the user's own turn of one conversation, if it is running. A
// check of that conversation is not one: it has a session of its own and never
// blocks the chat.
func (s *Service) chatRunLocked(conversationID string) *activeRun {
	for _, a := range s.running {
		if a.rec.Kind == RunChat && a.rec.Conversation == conversationID {
			return a
		}
	}
	return nil
}

// Current is the conversation the assistant is in right now. One is live at a
// time, so nothing that acts has to carry an id: a check reports here, a job is
// listed here, and the page opens here.
func (s *Service) Current() (Conversation, bool) {
	for _, entry := range s.store.List() {
		if entry.Status != StatusActive {
			continue
		}
		if c, ok := s.store.Load(entry.ID); ok {
			return c, true
		}
	}
	return Conversation{}, false
}

// Open is the live conversation, started when there is none. coderID picks the
// coder for a conversation that has to be created and is empty for "whichever
// one is live".
//
// An untouched conversation is reused instead of leaving a trail of empty ones
// behind: pressing new twice is one conversation, and a check that arrives
// before the user ever opened the page writes into the one they will find.
func (s *Service) Open(coderID string) (Conversation, error) {
	coders := s.coders.Available()
	if len(coders) == 0 {
		return Conversation{}, errors.New("No installed coder can run the assistant right now.")
	}
	current, live := s.Current()
	if coderID == "" {
		if live {
			return current, nil
		}
		coderID = coders[0].ID
	}
	if live && len(current.Messages) == 0 && current.CoderID == coderID {
		return current, nil
	}
	return s.create(coderID, "")
}

// Reserved reports whether a provider session belongs to an active conversation. The
// coder managers ask before listing a session, so a conversation never shows up as a
// ghost resumable coder. A transferred conversation releases its reservation, and its
// session then appears exactly once, as the coder terminal that owns it.
func (s *Service) Reserved(coderID, sessionID string) bool {
	s.reservedMu.RLock()
	defer s.reservedMu.RUnlock()
	return s.reserved[reservationKey(coderID, sessionID)]
}

func (s *Service) reserve(coderID, sessionID string) {
	s.reservedMu.Lock()
	defer s.reservedMu.Unlock()
	s.reserved[reservationKey(coderID, sessionID)] = true
}

func (s *Service) release(coderID, sessionID string) {
	s.reservedMu.Lock()
	defer s.reservedMu.Unlock()
	delete(s.reserved, reservationKey(coderID, sessionID))
}

func reservationKey(coderID, sessionID string) string { return coderID + "\x00" + sessionID }

// create opens an empty conversation bound to one coder and one project. The
// first prompt names it. Unexported on purpose: Open is the one way in, so
// nothing outside can start a second live conversation.
func (s *Service) create(coderID, rawProject string) (Conversation, error) {
	co, ok := s.coder(coderID)
	if !ok {
		return Conversation{}, errors.New("This coder cannot run conversations.")
	}
	workdir, err := s.projects.ValidatePath(rawProject)
	if err != nil {
		return Conversation{}, err
	}
	id, err := terminal.NewKey()
	if err != nil {
		return Conversation{}, errors.New("The conversation could not be created.")
	}
	now := s.now().UTC()
	c := Conversation{
		Summary: Summary{
			ID:            id,
			Title:         DefaultTitle,
			CoderID:       co.ID,
			ProjectPath:   workdir,
			Status:        StatusActive,
			CreatedAt:     now,
			LastMessageAt: now,
		},
		NativeSessionID: id,
		UpdatedAt:       now,
	}

	s.mu.Lock()
	// One conversation is live at a time. The others become what they are going
	// to stay, a transcript, and give their provider session back.
	s.archiveActiveLocked()
	s.reserve(co.ID, id)
	s.store.Save(c)
	s.mu.Unlock()

	s.changed()
	return c, nil
}

// archiveActiveLocked marks every active conversation archived and drops the
// provider session behind it. A conversation that is still generating keeps its
// session until the turn ends, finish drops it then: killing the session under
// a running process would leave the provider writing into something that is
// already gone.
func (s *Service) archiveActiveLocked() {
	for _, entry := range s.store.List() {
		if entry.Status != StatusActive {
			continue
		}
		c, ok := s.store.Load(entry.ID)
		if !ok {
			continue
		}
		c.Status = StatusArchived
		c.UpdatedAt = s.now().UTC()
		s.store.Save(c)
		if s.chatRunLocked(c.ID) == nil {
			s.dropSessionLocked(c)
		}
	}
}

// dropSessionLocked removes the provider session of a conversation nothing can
// continue. A session that refuses to go keeps its reservation, so it cannot
// turn up as a resumable coder that resumes a conversation the cockpit no
// longer drives.
func (s *Service) dropSessionLocked(c Conversation) {
	co, ok := s.coder(c.CoderID)
	if !ok || !co.Runner.SessionExists(c.NativeSessionID) {
		s.release(c.CoderID, c.NativeSessionID)
		return
	}
	if err := co.Runner.DeleteSession(c.NativeSessionID); err != nil {
		log.Printf("assistant: drop provider session %s: %v", c.NativeSessionID, err)
		return
	}
	s.release(c.CoderID, c.NativeSessionID)
}

// Send appends a prompt and starts a generation. Attachments are already on
// disk when they arrive here, the prompt only points the coder at them.
//
// While a turn runs the message queues instead: it goes into the transcript as
// a waiting entry, and the end of the turn flushes everything waiting as one
// new turn. The decision falls under the service lock, the same one the turn's
// end takes to stop counting as running, so a send racing that end either sees
// the run and queues, or sees the freed conversation and starts. A send that
// finds older messages still waiting queues behind them even when nothing
// runs, so the order of what was typed is the order of what goes out.
func (s *Service) Send(id, prompt string, attachments []Attachment) (Run, error) {
	return s.SendFrom(id, prompt, attachments, "")
}

// SendFrom is Send with the origin of the message. It is its own method and not
// a fourth parameter on Send so every existing caller keeps meaning what it
// meant: the browser is the empty source, and only a channel that knows it is
// one names itself here.
func (s *Service) SendFrom(id, prompt string, attachments []Attachment, source string) (Run, error) {
	source = Source(source)
	text, err := validatePrompt(prompt, len(attachments))
	if err != nil {
		return Run{}, err
	}

	s.mu.Lock()
	c, co, err := s.prepareLocked(id)
	if err != nil {
		s.mu.Unlock()
		return Run{}, err
	}
	now := s.now().UTC()
	if s.chatRunLocked(id) != nil || len(queuedMessages(c)) > 0 {
		if len(queuedMessages(c)) >= MaxQueuedMessages {
			s.mu.Unlock()
			return Run{}, errors.New("Too many messages are waiting already. Let the assistant catch up first.")
		}
		msg := Message{
			ID:          statefile.NewID(),
			Role:        RoleUser,
			Content:     text,
			Attachments: attachments,
			CreatedAt:   now,
			State:       StateQueued,
			Source:      source,
		}
		c.Messages = append(c.Messages, msg)
		c.Draft = Draft{UpdatedAt: now}
		c.UpdatedAt = now
		s.store.Save(c)
		s.mu.Unlock()

		s.hub.publish(c.ID, StreamEvent{Kind: FrameMessage, MessageID: msg.ID})
		s.changed()
		return Run{MessageID: msg.ID, UserMessageID: msg.ID, Title: c.Title, Queued: true}, nil
	}
	user := Message{
		ID:          statefile.NewID(),
		Role:        RoleUser,
		Content:     text,
		Attachments: attachments,
		CreatedAt:   now,
		State:       StateComplete,
		Source:      source,
	}
	c.Messages = append(c.Messages, user)
	if c.Title == "" || c.Title == DefaultTitle {
		c.Title = deriveTitle(text, attachments)
	}
	// The composer just emptied itself, so the draft it held is spent. The
	// empty draft keeps a timestamp: the other devices decide by it, and a
	// cleared draft without one would look older than what they still hold.
	c.Draft = Draft{UpdatedAt: now}
	r, err := s.startLocked(&c, co, withAttachments(text, attachments), user.ID)
	s.mu.Unlock()
	if err != nil {
		return Run{}, err
	}
	r.UserMessageID = user.ID
	s.changed()
	return r, nil
}

// Retry runs the last prompt again after a failed, cancelled or interrupted
// turn. It is always explicit: a turn that may already have been charged is
// never resent on its own.
func (s *Service) Retry(id string) (Run, error) {
	s.mu.Lock()
	c, co, err := s.prepareLocked(id)
	if err != nil {
		s.mu.Unlock()
		return Run{}, err
	}
	if s.chatRunLocked(id) != nil {
		s.mu.Unlock()
		return Run{}, errors.New("This conversation is still working on the previous message.")
	}
	last, ok := c.Last()
	if !ok || last.Role != RoleAssistant || !last.State.Retryable() {
		s.mu.Unlock()
		return Run{}, errors.New("There is nothing to retry in this conversation.")
	}
	replaced := last.ID
	c.Messages = c.Messages[:len(c.Messages)-1]
	prompt := ""
	if prev, ok := c.Last(); ok && prev.Role == RoleUser {
		prompt = withAttachments(prev.Content, prev.Attachments)
	}
	if prompt == "" {
		s.mu.Unlock()
		return Run{}, errors.New("There is nothing to retry in this conversation.")
	}
	r, err := s.startLocked(&c, co, prompt)
	r.ReplacedID = replaced
	s.mu.Unlock()
	if err != nil {
		return Run{}, err
	}
	// The page that pressed retry drops the replaced bubble itself, the others
	// hear about it here, so no page keeps the failed turn next to its retry.
	s.hub.publish(c.ID, StreamEvent{Kind: FrameGone, MessageID: replaced})
	s.changed()
	return r, nil
}

// prepareLocked resolves a conversation that may accept a new message. Whether
// a turn is already running is the caller's question: a send queues behind one,
// a retry refuses.
func (s *Service) prepareLocked(id string) (Conversation, CoderInfo, error) {
	c, ok := s.store.Load(id)
	if !ok {
		return Conversation{}, CoderInfo{}, errors.New("Conversation not found.")
	}
	switch c.Status {
	case StatusTransferred:
		return Conversation{}, CoderInfo{}, errors.New("This conversation moved to a coder terminal. Continue it there.")
	case StatusArchived:
		return Conversation{}, CoderInfo{}, errors.New("This is an earlier conversation. Continue in the current one.")
	}
	co, ok := s.coder(c.CoderID)
	if !ok {
		return Conversation{}, CoderInfo{}, errors.New("The coder of this conversation is not available right now.")
	}
	if _, err := s.projects.ValidatePath(c.ProjectPath); err != nil {
		return Conversation{}, CoderInfo{}, errors.New("The project of this conversation is not available anymore.")
	}
	return c, co, nil
}

// startLocked persists the pending turn and launches the provider process.
// The assistant placeholder is written before the process starts, so a crash
// leaves a visible interrupted turn instead of a lost prompt, and the register
// entry is written before the process exists, so a turn is never running
// unregistered.
//
// announce names the user messages this turn takes with it. They go out on the
// stream after the save and before the start frame, so a page that never saw
// them pulls the question first and the answer's bubble lands under it.
func (s *Service) startLocked(c *Conversation, co CoderInfo, prompt string, announce ...string) (Run, error) {
	if !s.chatSlots.take() {
		return Run{}, ErrBusy
	}

	runID := statefile.NewID()
	// The answer inherits the origin of the question it answers, which is the
	// last user message of the conversation whichever way this turn started: a
	// send, a retry, or a queue that flushed several at once.
	source := lastUserSource(*c)
	msg := Message{
		ID:        statefile.NewID(),
		Role:      RoleAssistant,
		CreatedAt: s.now().UTC(),
		RunID:     runID,
		State:     StateStreaming,
		Source:    source,
	}
	c.Messages = append(c.Messages, msg)
	c.UpdatedAt = s.now().UTC()
	s.store.Save(*c)

	req := TurnRequest{
		SessionID: c.NativeSessionID,
		Resume:    co.Runner.SessionExists(c.NativeSessionID),
		Title:     c.Title,
		Workdir:   c.ProjectPath,
		Prompt:    prompt,
		Source:    source,
	}
	rec := RunRecord{
		ID:           runID,
		Kind:         RunChat,
		Conversation: c.ID,
		MessageID:    msg.ID,
		CoderID:      co.ID,
		SessionID:    c.NativeSessionID,
	}
	for _, id := range announce {
		s.hub.publish(c.ID, StreamEvent{Kind: FrameMessage, MessageID: id})
	}
	s.hub.publish(c.ID, StreamEvent{Kind: FrameStart, RunID: runID, MessageID: msg.ID, State: string(StateStreaming)})

	a, err := s.launch(co.Runner, req, rec)
	if err != nil {
		// The placeholder becomes the failed turn, in the same shape as any
		// other failure, so the page offers the same retry. It runs on its own
		// goroutine because this holds the service lock.
		failed := &activeRun{rec: rec, done: make(chan struct{})}
		close(failed.done)
		go s.settleChat(failed, "", nil, err)
		return Run{RunID: runID, MessageID: msg.ID, Title: c.Title}, nil
	}
	s.running[runID] = a
	go s.follow(a, co.Runner)
	return Run{RunID: runID, MessageID: msg.ID, Title: c.Title}, nil
}

// Cancel stops the running generation, keeping the partial answer. The stop is
// written down before the process is killed, so a restart that happens in
// between still reads it as a stop.
func (s *Service) Cancel(id string) error {
	s.mu.Lock()
	a := s.chatRunLocked(id)
	if a == nil {
		s.mu.Unlock()
		return errors.New("This conversation is not working on anything.")
	}
	a.cancelled.Store(true)
	id, proc := a.rec.ID, a.proc
	s.mu.Unlock()

	s.runs.Update(id, func(rec *RunRecord) { rec.Cancelled = true })
	proc.kill()
	return nil
}

// Discard removes one waiting message before the queue flushed it. The decision
// falls under the service lock, the same one the flush holds: a message is
// either still waiting and comes out, or it already went and the caller hears so.
func (s *Service) Discard(conversationID, messageID string) error {
	s.mu.Lock()
	c, ok := s.store.Load(conversationID)
	if !ok {
		s.mu.Unlock()
		return errors.New("Conversation not found.")
	}
	for i, m := range c.Messages {
		if m.ID != messageID {
			continue
		}
		if m.State != StateQueued {
			s.mu.Unlock()
			return errors.New("This message already went out.")
		}
		c.Messages = append(c.Messages[:i], c.Messages[i+1:]...)
		c.UpdatedAt = s.now().UTC()
		s.store.Save(c)
		s.mu.Unlock()

		s.hub.publish(conversationID, StreamEvent{Kind: FrameGone, MessageID: messageID})
		s.changed()
		return nil
	}
	s.mu.Unlock()
	return errors.New("Message not found.")
}

// flushReady sends what queued up while a turn ran, as exactly one new turn.
// Only the live conversation can hold waiting messages that may still go out,
// so this looks at nothing else. It runs whenever a chat turn settled (the
// freed slot is what the flush takes) and once after Recover, which is what
// makes a queued message survive a restart.
func (s *Service) flushReady() {
	s.mu.Lock()
	if s.recovering {
		s.mu.Unlock()
		return
	}
	c, live := s.Current()
	if !live || s.chatRunLocked(c.ID) != nil {
		s.mu.Unlock()
		return
	}
	queued := queuedMessages(c)
	if len(queued) == 0 {
		s.mu.Unlock()
		return
	}
	co, ok := s.coder(c.CoderID)
	if !ok {
		s.mu.Unlock()
		return
	}
	if _, err := s.projects.ValidatePath(c.ProjectPath); err != nil {
		s.mu.Unlock()
		return
	}
	flushed := make([]string, 0, len(queued))
	for i := range c.Messages {
		if c.Messages[i].State == StateQueued {
			c.Messages[i].State = StateComplete
			flushed = append(flushed, c.Messages[i].ID)
		}
	}
	// The waiting entries go out with the new turn; every open page pulls them
	// fresh so their bubbles stop saying so.
	_, err := s.startLocked(&c, co, queuedPrompt(queued), flushed...)
	s.mu.Unlock()
	if err != nil {
		// The slots are full. Nothing was written, the messages stay waiting,
		// and the next turn that settles frees a slot and tries again.
		return
	}
	s.changed()
}

// Rename sets a new title.
func (s *Service) Rename(id, rawTitle string) error {
	title := strings.TrimSpace(rawTitle)
	if title == "" {
		return errors.New("A conversation needs a title.")
	}
	title = truncateRunes(oneLine(title), MaxTitleRunes)

	s.mu.Lock()
	c, ok := s.store.Load(id)
	if !ok {
		s.mu.Unlock()
		return errors.New("Conversation not found.")
	}
	c.Title = title
	c.UpdatedAt = s.now().UTC()
	s.store.Save(c)
	s.mu.Unlock()

	s.changed()
	return nil
}

// SaveDraft stores what the composer holds without sending it and says whether
// that changed anything. A repeated save writes nothing and announces nothing,
// so a long transcript is not rewritten for a keystroke and the other devices
// are not woken for a draft that already is what they hold.
func (s *Service) SaveDraft(id, text string, attachments []Attachment) (Draft, bool, error) {
	if len(text) > MaxPromptBytes {
		return Draft{}, false, errors.New("That message is too long.")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.store.Load(id)
	if !ok {
		return Draft{}, false, errors.New("Conversation not found.")
	}
	if c.Draft.Same(text, attachments) {
		return c.Draft, false, nil
	}
	c.Draft = Draft{Text: text, Attachments: attachments, UpdatedAt: s.now().UTC()}
	s.store.Save(c)
	return c.Draft, true, nil
}

// Draft is what the composer of a conversation holds right now. It is its own
// read so a device catching up pulls the draft, not the transcript.
func (s *Service) Draft(id string) (Draft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.store.Load(id)
	if !ok {
		return Draft{}, errors.New("Conversation not found.")
	}
	return c.Draft, nil
}

// Delete removes a conversation. An active conversation takes its provider session with it, a
// transferred one does not: the coder terminal owns that session now.
func (s *Service) Delete(id string) error {
	s.mu.Lock()
	// A loop, not an if: settling the killed turn flushes the queue, and the
	// flush may start the next turn right there. Each pass drains what waited
	// into a turn that is stopped in the next one, until nothing runs.
	for a := s.chatRunLocked(id); a != nil; a = s.chatRunLocked(id) {
		a.cancelled.Store(true)
		runID, proc, done := a.rec.ID, a.proc, a.done
		s.mu.Unlock()
		s.runs.Update(runID, func(rec *RunRecord) { rec.Cancelled = true })
		proc.kill()
		<-done
		s.mu.Lock()
	}

	c, ok := s.store.Load(id)
	if !ok {
		s.mu.Unlock()
		return errors.New("Conversation not found.")
	}
	if c.Status == StatusActive {
		if co, ok := s.coder(c.CoderID); ok && co.Runner.SessionExists(c.NativeSessionID) {
			if err := co.Runner.DeleteSession(c.NativeSessionID); err != nil {
				log.Printf("assistant: delete provider session %s: %v", c.NativeSessionID, err)
				s.mu.Unlock()
				return errors.New("The conversation could not be removed from the coder, so the conversation was kept.")
			}
		}
	}
	if err := s.store.Delete(id); err != nil {
		s.mu.Unlock()
		return err
	}
	s.release(c.CoderID, c.NativeSessionID)
	s.mu.Unlock()

	s.changed()
	return nil
}

// Transfer promotes the conversation's provider session into a coder terminal and
// returns the terminal id. It is one way: from here the terminal owns the
// conversation and the conversation keeps a readable transcript.
func (s *Service) Transfer(id string, terminals Terminals) (string, error) {
	terminalID, err := s.transfer(id, terminals)
	if err != nil {
		return "", err
	}
	s.changed()
	return terminalID, nil
}

func (s *Service) transfer(id string, terminals Terminals) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	c, ok := s.store.Load(id)
	if !ok {
		return "", errors.New("Conversation not found.")
	}
	if c.Status != StatusActive {
		return "", errors.New("This conversation already moved to a coder terminal.")
	}
	if s.chatRunLocked(id) != nil {
		return "", errors.New("Wait for the current answer before moving this conversation to a terminal.")
	}
	if len(queuedMessages(c)) > 0 {
		return "", errors.New("Messages are still waiting to be sent. Let them go out or remove them first.")
	}
	if !c.Idle() {
		return "", errors.New("The last answer did not finish. Retry or stop it before moving this conversation.")
	}
	if len(c.Messages) == 0 {
		return "", errors.New("This conversation has no conversation to move yet.")
	}
	co, ok := s.coder(c.CoderID)
	if !ok {
		return "", errors.New("The coder of this conversation is not available right now.")
	}
	workdir, err := s.projects.ValidatePath(c.ProjectPath)
	if err != nil {
		return "", errors.New("The project of this conversation is not available anymore.")
	}
	if !co.Runner.SessionExists(c.NativeSessionID) {
		return "", errors.New("This conversation has no conversation to move yet.")
	}

	// The reservation is still in place here, so the session cannot be listed
	// as a coder while the terminal comes up. It is released below, together
	// with the persisted status, under the same lock.
	terminalID, err := terminals.ResumeReserved(c.CoderID, c.NativeSessionID, workdir, c.Title)
	if err != nil {
		return "", err
	}

	c.Status = StatusTransferred
	c.TransferredSessionID = terminalID
	c.UpdatedAt = s.now().UTC()
	s.store.Save(c)

	// statefile writes are atomic but silent on failure, and a conversation that stays
	// active while its terminal runs would own the same session twice. Read it
	// back, and roll the terminal away if the state did not land.
	if saved, ok := s.store.Load(id); !ok || saved.Status != StatusTransferred {
		if stopErr := terminals.Stop(c.CoderID, terminalID); stopErr != nil {
			log.Printf("assistant: rollback of transfer %s failed: %v", id, stopErr)
		}
		return "", errors.New("The conversation could not be moved. Nothing was changed.")
	}
	s.release(c.CoderID, c.NativeSessionID)
	return terminalID, nil
}

// Subscribe attaches to the conversation's stream and returns the in-flight state.
func (s *Service) Subscribe(id string) (StreamEvent, bool, <-chan StreamEvent, func()) {
	return s.hub.subscribe(id)
}

func (s *Service) changed() {
	if s.onChange != nil {
		s.onChange()
	}
}

func validatePrompt(raw string, attachments int) (string, error) {
	text := strings.TrimSpace(raw)
	if text == "" && attachments == 0 {
		return "", errors.New("Type a message first.")
	}
	if len(text) > MaxPromptBytes {
		return "", fmt.Errorf("That message is too long. Keep it under %d KB.", MaxPromptBytes/1024)
	}
	return text, nil
}

// lastUserSource is where the question of this turn came from. A conversation
// mixes origins, so it is the newest user message that decides, the one this
// turn is about to answer.
func lastUserSource(c Conversation) string {
	for i := len(c.Messages) - 1; i >= 0; i-- {
		if c.Messages[i].Role == RoleUser {
			return c.Messages[i].Source
		}
	}
	return ""
}

// queuedMessages are the transcript entries still waiting to go out, in the
// order they were sent. Only user messages ever carry the queued state.
func queuedMessages(c Conversation) []Message {
	var out []Message
	for _, m := range c.Messages {
		if m.State == StateQueued {
			out = append(out, m)
		}
	}
	return out
}

// queuedPrompt is the one turn a flushed queue becomes. A single waiting
// message goes out as itself; several stay recognizable as separate messages,
// in the order they were sent.
func queuedPrompt(msgs []Message) string {
	if len(msgs) == 1 {
		return withAttachments(msgs[0].Content, msgs[0].Attachments)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "The user sent %d messages while the previous answer was still on its way. They follow in order, answer them all.\n", len(msgs))
	for i, m := range msgs {
		fmt.Fprintf(&b, "\n--- Message %d ---\n%s\n", i+1, withAttachments(m.Content, m.Attachments))
	}
	return b.String()
}

// withAttachments is the prompt the coder receives. The files are named by
// their absolute path, which is what a coder needs to open one, and the note
// stays out of the transcript so the bubble shows what was typed.
func withAttachments(text string, attachments []Attachment) string {
	if len(attachments) == 0 {
		return text
	}
	var b strings.Builder
	b.WriteString(text)
	if text != "" {
		b.WriteString("\n\n")
	}
	b.WriteString("Attached files:\n")
	for _, a := range attachments {
		fmt.Fprintf(&b, "- %s\n", a.Path)
	}
	return b.String()
}

// deriveTitle names a conversation after its first prompt, or after the file it
// carried when the prompt was only an attachment.
func deriveTitle(prompt string, attachments []Attachment) string {
	title := truncateRunes(oneLine(prompt), MaxTitleRunes)
	if title == "" && len(attachments) > 0 {
		title = truncateRunes(oneLine(attachments[0].Name), MaxTitleRunes)
	}
	if title == "" {
		return DefaultTitle
	}
	return title
}

func oneLine(s string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

// sanitizeError keeps a provider failure short and one line. Runners already
// return curated sentences, this is the backstop that keeps a stray path or a
// stack trace out of the browser.
func sanitizeError(err error) string {
	msg := oneLine(err.Error())
	if msg == "" {
		return "The coder could not answer this message."
	}
	return truncateRunes(msg, 200)
}
