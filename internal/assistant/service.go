package assistant

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/marein/dev-cockpit/internal/statefile"
	"github.com/marein/dev-cockpit/internal/terminal"
)

// One chat turn per assistant, and no cap over all of them. A chat needs no
// more than one turn at a time, and a second prompt into an assistant that is
// answering waits in that assistant's own queue; the reasoning sits at the
// queue, in Send. The queue is one assistant's, so a thread that is thinking
// holds up nothing but itself and every other assistant answers at the same
// time. A number over all assistants would be a different thing entirely, a
// cap on how many conversations may be alive at once, and what it would buy is
// not worth what it costs: a second assistant that answers "busy" because a
// first one is thinking is an assistant nobody can rely on. What is capped
// globally is the checks, the turns nobody asked for interactively, see
// DefaultConcurrentChecks.

// SelfDeleteRefusal is what an assistant asking to delete itself reads. It
// would be deleting the transcript its own answer is being written into, and
// there would be nobody left to tell the user what happened. Two places refuse
// it, the command before it asks anything and the server at the door, and they
// say the same sentence because it is the same rule.
const SelfDeleteRefusal = "An assistant cannot delete itself. Ask the user, or another assistant."

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
	// Title is the instance title after this turn, so a page that derived its
	// title from the first prompt can show it without a reload.
	Title string
	// Queued says no generation started: the message waits in the transcript
	// until the running turn ends.
	Queued bool
}

// Service owns instance state, provider processes and the per instance streams.
type Service struct {
	store *Store
	// drafts are the unsent messages, one file per assistant next to its
	// transcript. They are their own store because they are written while
	// somebody types and the transcript must not be rewritten for that.
	drafts *Drafts
	// runs is the register of turns that are on the machine right now. It is on
	// disk, because a turn is not a child of this process and the next server
	// has to be able to find it.
	runs     *RunStore
	coders   Coders
	workdirs Workdirs
	now      func() time.Time
	hub      *hub

	// mu guards running and every state transition. Nothing under this lock may
	// call back into coder.Manager snapshots, and the change hooks fire after
	// it is released.
	mu sync.Mutex
	// running are the turns this server is following, by run id.
	running map[string]*activeRun

	// recovering holds the queue flush back while Recover walks the register:
	// an orphaned turn settles during that walk, and a flush started there
	// could run next to a still unregistered turn of the same assistant.
	// Guarded by mu.
	recovering bool

	// reserved has a lock of its own because it sits on a different hot path:
	// every coder snapshot asks it, and it must not queue behind the transcript
	// writes of a streaming answer. It changes when an assistant is created or
	// deleted and when a check starts or ends, one key at a time, so a reader
	// never sees a half applied change: a session is either reserved, and
	// hidden from the coder lists, or it is not.
	reservedMu sync.RWMutex
	reserved   map[string]bool

	onChange func()
	onDone   func(instanceID string)
	render   func(string) (string, error)

	// events is the reactor: where an event a source publishes goes, and where
	// the triggers live. Built with the service, see newReactor.
	events *Reactor
	// slots is the one cap on the turns nobody asked for interactively, the
	// checks and the reactions. The watcher sets its limit from the setting.
	slots *checkSlots
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

// newService wires the instance service and reconciles the instance
// state a previous process left behind. The turns that process left running are
// picked up separately by Recover, which needs the rest of the cockpit to exist
// first. The package entry point is New, which binds it to the assistant.
func newService(store *Store, runs *RunStore, coders Coders, workdirs Workdirs) *Service {
	s := &Service{
		store:    store,
		drafts:   NewDrafts(store),
		runs:     runs,
		coders:   coders,
		workdirs: workdirs,
		now:      time.Now,
		hub:      newHub(),
		running:  map[string]*activeRun{},
		reserved: map[string]bool{},
		slots:    newCheckSlots(nil),
	}
	s.reconcile()
	newReactor(s, NewTriggers(store), NewJobs(store))
	return s
}

// SetHooks registers the coarse change publisher and the completion
// notification. Both run outside the service lock.
func (s *Service) SetHooks(onChange func(), onDone func(instanceID string)) {
	s.onChange = onChange
	s.onDone = onDone
}

// SetRenderer installs the Markdown renderer used while an answer streams. The
// browser never parses model output itself, it only shows what this produced.
func (s *Service) SetRenderer(render func(string) (string, error)) {
	s.render = render
}

// reconcile seeds the reservation set: every assistant's provider session
// stays out of the coder lists, so none of them turns up as a ghost resumable
// coder. What became of the turns that were running is not decided here: their
// processes may still be writing, and Recover asks them.
func (s *Service) reconcile() {
	for _, entry := range s.store.List() {
		s.reserved[reservationKey(entry.CoderID, entry.ID)] = true
	}
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

// List returns the instance index in the order the user sorted it into.
func (s *Service) List() []Summary { return s.store.List() }

// Reorder puts the list in the order the ids name. The order is the user's,
// not the clock's: an assistant sits where it was dragged, and an answer
// arriving in another one moves nothing.
func (s *Service) Reorder(ids []string) { s.store.Reorder(ids) }

// Get loads one instance.
func (s *Service) Get(id string) (Instance, error) {
	c, ok := s.store.Load(id)
	if !ok {
		return Instance{}, errors.New("Assistant not found.")
	}
	return c, nil
}

// Search returns the instance index in list order, narrowed to the
// instances where a word appears in the title or in a message,
// compared case insensitively. An empty word returns the whole index. The
// title is answered from the index alone, the message match loads the
// transcript; nothing here writes. A message is matched by Message.carries,
// the same rule Transcript narrows a thread with, so the list that says an
// assistant holds the word and the reading that shows where cannot disagree.
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
			if m.carries(word) {
				out = append(out, entry)
				break
			}
		}
	}
	return out
}

// TranscriptEntriesShown is how many messages a transcript reading keeps by
// default: enough to see where an assistant stands, small enough to stay a
// fraction of the answer that carries it.
const TranscriptEntriesShown = 6

// TranscriptMessageRunes is how many runes one message may cost in a capped
// transcript reading. Callers pass it as the budget; zero lifts the cap, for
// the one answer that needs a message whole.
const TranscriptMessageRunes = 600

// Transcript returns one instance windowed to its last entries, each
// message cut to budget runes with a note saying how much of it is shown.
// entries zero or less means the default window, budget zero or less keeps
// every message whole. The second return is how many older messages the
// window dropped. It only reads, the stored transcript stays as it is.
//
// A contains word narrows the thread to the messages carrying it, and it
// narrows before the window the way `job-list --contains` filters before its
// cap: a match older than the last entries is exactly what somebody searching
// a long thread is after, and a filter behind the window would never reach
// it. MessageCount stays the whole thread's, so the reading can say how much
// of it the matches are.
func (s *Service) Transcript(id, contains string, entries, budget int) (Instance, int, error) {
	c, ok := s.store.Load(id)
	if !ok {
		return Instance{}, 0, errors.New("Assistant not found.")
	}
	if word := strings.ToLower(strings.TrimSpace(contains)); word != "" {
		kept := make([]Message, 0, len(c.Messages))
		for _, m := range c.Messages {
			if m.carries(word) {
				kept = append(kept, m)
			}
		}
		c.Messages = kept
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

// LastAnswer returns the newest message that can ring: an assistant's answer
// or a note the cockpit wrote, a check's report. The notification for a
// finished turn is raised right after that turn ended, so this is the message
// it is about: it names the answer in the entry and lets the entry link
// straight at it.
func (s *Service) LastAnswer(id string) (Message, bool) {
	c, ok := s.store.Load(id)
	if !ok {
		return Message{}, false
	}
	for i := len(c.Messages) - 1; i >= 0; i-- {
		if m := c.Messages[i]; m.Role == RoleAssistant || m.IsNote() {
			return m, true
		}
	}
	return Message{}, false
}

// UploadDir is the directory holding the uploads of one instance.
func (s *Service) UploadDir(id string) (string, error) { return s.store.UploadDir(id) }

// Running reports whether a generation is in flight for this instance.
func (s *Service) Running(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.chatRunLocked(id) != nil
}

// chatRunLocked is the user's own turn of one instance, if it is running. A
// check of that instance is not one: it has a session of its own and never
// blocks the chat.
func (s *Service) chatRunLocked(instanceID string) *activeRun {
	for _, a := range s.running {
		if a.rec.Kind == RunChat && a.rec.Instance == instanceID {
			return a
		}
	}
	return nil
}

// Create starts a new assistant. coderID picks the coder that answers for it
// and is empty for the first installed one. It is the one way in, and it is
// always somebody asking: nothing in the cockpit creates an assistant by
// itself, so the list only ever holds what the user made.
func (s *Service) Create(coderID string) (Instance, error) {
	coders := s.coders.Available()
	if len(coders) == 0 {
		return Instance{}, errors.New("No installed coder can run the assistant right now.")
	}
	if strings.TrimSpace(coderID) == "" {
		coderID = coders[0].ID
	}
	return s.create(coderID)
}

// Reserved reports whether a provider session belongs to an assistant, or to a
// check that is running. The coder managers ask before listing a session, so
// neither ever shows up as a ghost resumable coder.
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

// create opens an empty assistant bound to one coder, in a workspace of its
// own. The first prompt names it, and the user may rename it at any time.
func (s *Service) create(coderID string) (Instance, error) {
	co, ok := s.coder(coderID)
	if !ok {
		return Instance{}, errors.New("This coder cannot run an assistant.")
	}
	id, err := terminal.NewKey()
	if err != nil {
		return Instance{}, errors.New("The assistant could not be created.")
	}
	if _, err := s.workdirs.Workdir(id); err != nil {
		return Instance{}, err
	}
	now := s.now().UTC()
	c := Instance{
		Summary: Summary{
			ID:            id,
			Title:         DefaultTitle,
			CoderID:       co.ID,
			CreatedAt:     now,
			LastMessageAt: now,
		},
		NativeSessionID: id,
		UpdatedAt:       now,
	}

	s.mu.Lock()
	s.reserve(co.ID, id)
	s.store.Save(c)
	s.mu.Unlock()

	s.changed()
	return c, nil
}

// dropSessionLocked removes the provider session of an assistant nothing can
// continue, and the session of a check that is over. A session that refuses to
// go keeps its reservation, so it cannot turn up as a resumable coder that
// resumes a conversation the cockpit no longer drives. The removal itself runs
// off this goroutine: it goes through the provider's own CLI, which for
// opencode boots a JavaScript runtime first, and this is called under the
// service lock, so a blocking delete would stall every assistant surface for
// that boot. The reservation stands until the delete succeeded, which is what
// keeps the session invisible in the window, and a refused delete keeps it
// reserved exactly as before; the reservations carry their own lock, so the
// release needs nothing this caller holds.
func (s *Service) dropSessionLocked(c Instance) {
	co, ok := s.coder(c.CoderID)
	if !ok || !co.Runner.SessionExists(c.NativeSessionID) {
		s.release(c.CoderID, c.NativeSessionID)
		return
	}
	go func() {
		if err := co.Runner.DeleteSession(c.NativeSessionID); err != nil {
			log.Printf("assistant: drop provider session %s: %v", c.NativeSessionID, err)
			return
		}
		s.release(c.CoderID, c.NativeSessionID)
	}()
}

// Send appends a prompt and starts a generation. Attachments are already on
// disk when they arrive here, the prompt only points the coder at them.
//
// While this assistant's turn runs the message queues instead: it goes into the
// transcript as a waiting entry, and the end of the turn flushes everything
// waiting as one new turn. The queue belongs to the one assistant, so nobody
// else waits for it and a second thought does not have to be held in the head
// until an answer arrives. A send that finds older messages still waiting
// queues behind them even when nothing runs, so the order of what was typed is
// the order of what goes out.
//
// The decision falls under the service lock, the same one the turn's end takes
// to stop counting as running, so a send racing that end either sees the run
// and queues, or sees the freed assistant and starts.
func (s *Service) Send(id, prompt string, attachments []Attachment) (Run, error) {
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
		}
		c.Messages = append(c.Messages, msg)
		c.UpdatedAt = now
		s.store.Save(c)
		s.mu.Unlock()

		s.clearDraft(id, now)
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
	}
	// What the cockpit wrote since the last answer is read before the prompt
	// joins the transcript, and its headlines ride along in front of it.
	since := notesSince(c)
	c.Messages = append(c.Messages, user)
	if c.Title == "" || c.Title == DefaultTitle {
		c.Title = deriveTitle(text, attachments)
	}
	r, err := s.startLocked(&c, co, withNotes(since, withAttachments(text, attachments)), user.ID)
	s.mu.Unlock()
	if err != nil {
		return Run{}, err
	}
	// The composer just emptied itself, so the draft it held is spent.
	s.clearDraft(id, now)
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
		return Run{}, errors.New("This assistant is still working on the previous message.")
	}
	last, ok := c.Last()
	if !ok || last.Role != RoleAssistant || !last.State.Retryable() {
		s.mu.Unlock()
		return Run{}, errors.New("There is nothing to retry in this assistant.")
	}
	replaced := last.ID
	c.Messages = c.Messages[:len(c.Messages)-1]
	prompt := ""
	if prev, ok := c.Last(); ok && prev.Role == RoleUser {
		prompt = withAttachments(prev.Content, prev.Attachments)
	}
	if prompt == "" {
		s.mu.Unlock()
		return Run{}, errors.New("There is nothing to retry in this assistant.")
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

// prepareLocked resolves an assistant that may accept a new message. Whether a
// turn is already running is the caller's question, and the two callers answer
// it differently: a send queues behind it, a retry refuses.
func (s *Service) prepareLocked(id string) (Instance, CoderInfo, error) {
	c, ok := s.store.Load(id)
	if !ok {
		return Instance{}, CoderInfo{}, errors.New("Assistant not found.")
	}
	co, ok := s.coder(c.CoderID)
	if !ok {
		return Instance{}, CoderInfo{}, errors.New("The coder of this assistant is not available right now.")
	}
	if _, err := s.workdirs.Workdir(c.ID); err != nil {
		return Instance{}, CoderInfo{}, err
	}
	return c, co, nil
}

// startLocked persists the pending turn and registers it as running; the
// launch itself happens off the lock, in launchAndFollow. The assistant
// placeholder is written before anything starts, so a crash leaves a visible
// interrupted turn instead of a lost prompt (the recovery sweep settles a
// streaming message no register entry covers), and the run is registered
// before the launch begins, so a second send queues behind it and a stop
// finds it, however long the runner's own preparation takes: opencode's
// first turn boots a server to create its provider session, and held under
// the lock that boot stalled every assistant surface for seconds.
//
// announce names the user message this turn takes with it. It goes out on the
// stream after the save and before the start frame, so a page that never saw
// it pulls the question first and the answer's bubble lands under it.
func (s *Service) startLocked(c *Instance, co CoderInfo, prompt string, announce ...string) (Run, error) {
	workdir, err := s.workdirs.Workdir(c.ID)
	if err != nil {
		return Run{}, err
	}
	runID := statefile.NewID()
	msg := Message{
		ID:        statefile.NewID(),
		Role:      RoleAssistant,
		CreatedAt: s.now().UTC(),
		RunID:     runID,
		State:     StateStreaming,
	}
	c.Messages = append(c.Messages, msg)
	c.UpdatedAt = s.now().UTC()
	s.store.Save(*c)

	req := TurnRequest{
		Instance:  c.ID,
		SessionID: c.NativeSessionID,
		Resume:    co.Runner.SessionExists(c.NativeSessionID),
		Title:     c.Title,
		Workdir:   workdir,
		Prompt:    prompt,
	}
	rec := RunRecord{
		ID:        runID,
		Kind:      RunChat,
		Instance:  c.ID,
		MessageID: msg.ID,
		CoderID:   co.ID,
		SessionID: c.NativeSessionID,
	}
	for _, id := range announce {
		s.hub.publish(c.ID, StreamEvent{Kind: FrameMessage, MessageID: id})
	}
	s.hub.publish(c.ID, StreamEvent{Kind: FrameStart, RunID: runID, MessageID: msg.ID, State: string(StateStreaming)})

	a := &activeRun{rec: rec, done: make(chan struct{})}
	s.running[runID] = a
	go s.launchAndFollow(a, co.Runner, req)
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
		return errors.New("This instance is not working on anything.")
	}
	a.cancelled.Store(true)
	id, proc, launched := a.rec.ID, a.proc, a.launched
	s.mu.Unlock()

	s.runs.Update(id, func(rec *RunRecord) { rec.Cancelled = true })
	// A turn still launching has no process yet: the cancelled mark is what
	// launchAndFollow reads, and it kills the process the moment it begins.
	if launched {
		proc.Kill()
	}
	return nil
}

// Discard removes one waiting message before the queue flushed it. The decision
// falls under the service lock, the same one the flush holds: a message is
// either still waiting and comes out, or it already went and the caller hears so.
func (s *Service) Discard(instanceID, messageID string) error {
	s.mu.Lock()
	c, ok := s.store.Load(instanceID)
	if !ok {
		s.mu.Unlock()
		return errors.New("Assistant not found.")
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

		s.hub.publish(instanceID, StreamEvent{Kind: FrameGone, MessageID: messageID})
		s.changed()
		return nil
	}
	s.mu.Unlock()
	return errors.New("Message not found.")
}

// flushReady sends what queued up in one assistant while its turn ran, as
// exactly one new turn. The queue is that assistant's, so this looks at that
// one and at nothing else, and an assistant with something waiting holds up
// nobody. It runs whenever a chat turn settled, and once per assistant after
// Recover, which is what makes a queued message survive a restart.
func (s *Service) flushReady(instanceID string) {
	s.mu.Lock()
	if s.recovering || instanceID == "" {
		s.mu.Unlock()
		return
	}
	c, ok := s.store.Load(instanceID)
	if !ok || s.chatRunLocked(instanceID) != nil {
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
	if _, err := s.workdirs.Workdir(c.ID); err != nil {
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
	// A queued first message is what names the assistant, the same way a sent
	// one does: the turn it goes out in is the first anybody sees.
	if c.Title == "" || c.Title == DefaultTitle {
		c.Title = deriveTitle(queued[0].Content, queued[0].Attachments)
	}
	// The waiting entries go out with the new turn; every open page pulls them
	// fresh so their bubbles stop saying so. The one line about what the
	// cockpit wrote since the last answer rides along the way it does on a
	// send.
	_, err := s.startLocked(&c, co, withNotes(notesSince(c), queuedPrompt(queued)), flushed...)
	s.mu.Unlock()
	if err != nil {
		// Nothing was written, the messages stay waiting, and the next end of a
		// turn tries again.
		return
	}
	s.changed()
}

// flushAllReady lets every assistant that has something waiting go. Only the
// restart needs it: a queue that outlived the server belongs to whichever
// assistant wrote it, and there can be several of them.
func (s *Service) flushAllReady() {
	for _, entry := range s.store.List() {
		s.flushReady(entry.ID)
	}
}

// Rename sets a new name. An assistant is named after its first prompt and the
// user renames it from there, which is what makes a list of several of them
// readable.
func (s *Service) Rename(id, rawTitle string) error {
	title := strings.TrimSpace(rawTitle)
	if title == "" {
		return errors.New("An assistant needs a name.")
	}
	title = truncateRunes(oneLine(title), MaxTitleRunes)

	s.mu.Lock()
	c, ok := s.store.Load(id)
	if !ok {
		s.mu.Unlock()
		return errors.New("Assistant not found.")
	}
	c.Title = title
	c.UpdatedAt = s.now().UTC()
	s.store.Save(c)
	s.mu.Unlock()

	s.changed()
	return nil
}

// SaveDraft stores what the composer holds without sending it and says whether
// that changed anything. It writes one small file of its own and neither the
// transcript nor the index: a draft is saved every time the typing pauses, and
// a thread that has been going for a week is not rewritten for a keystroke. A
// repeated save writes nothing and announces nothing, so the other devices are
// not woken for a draft that already is what they hold.
func (s *Service) SaveDraft(id, text string, attachments []Attachment) (Draft, bool, error) {
	if len(text) > MaxPromptBytes {
		return Draft{}, false, errors.New("That message is too long.")
	}
	if _, ok := s.store.Load(id); !ok {
		return Draft{}, false, errors.New("Assistant not found.")
	}
	d := Draft{Text: text, Attachments: attachments, UpdatedAt: s.now().UTC()}
	if !s.drafts.Of(id).Save(d) {
		return s.drafts.Of(id).Get(), false, nil
	}
	return d, true, nil
}

// Draft is what the composer of an assistant holds right now. It is its own
// read so a device catching up pulls the draft, not the transcript.
func (s *Service) Draft(id string) (Draft, error) {
	if !ValidID(id) {
		return Draft{}, errors.New("Assistant not found.")
	}
	return s.drafts.Of(id).Get(), nil
}

// Delete removes an assistant and takes its provider session with it.
func (s *Service) Delete(id string) error {
	s.mu.Lock()
	// A loop, not an if. The turn that is running goes first, and waiting for
	// it to settle is what makes the delete safe: settling gives the provider
	// session back and takes the register entry away, and both have to be over
	// before the transcript underneath them is removed. But settling is also
	// what lets this assistant's queue go (settleChat, flushReady): a message
	// that waited goes out as a new turn right there, registered before done
	// closes. So every pass stops the turn it finds, and the next pass finds
	// the one the flush started, until nothing runs. Only then can nothing
	// start another one: a send into an assistant whose transcript is gone is
	// refused.
	for a := s.chatRunLocked(id); a != nil; a = s.chatRunLocked(id) {
		a.cancelled.Store(true)
		runID, proc, done, launched := a.rec.ID, a.proc, a.done, a.launched
		s.mu.Unlock()
		s.runs.Update(runID, func(rec *RunRecord) { rec.Cancelled = true })
		// A turn the flush just started may have no process yet. Then the
		// cancelled mark is what launchAndFollow reads, exactly like on a
		// stop, and it kills the process the moment it begins.
		if launched {
			proc.Kill()
		}
		<-done
		s.mu.Lock()
	}

	c, ok := s.store.Load(id)
	if !ok {
		s.mu.Unlock()
		return errors.New("Assistant not found.")
	}
	if co, ok := s.coder(c.CoderID); ok && co.Runner.SessionExists(c.NativeSessionID) {
		if err := co.Runner.DeleteSession(c.NativeSessionID); err != nil {
			log.Printf("assistant: delete provider session %s: %v", c.NativeSessionID, err)
			s.mu.Unlock()
			return errors.New("The assistant could not be removed from the coder, so it was kept.")
		}
	}
	if err := s.store.Delete(id); err != nil {
		s.mu.Unlock()
		return err
	}
	s.drafts.Forget(id)
	s.events.Dropped(id)
	s.release(c.CoderID, c.NativeSessionID)
	s.mu.Unlock()

	s.changed()
	return nil
}

// Subscribe attaches to the instance's stream and returns the in-flight state.
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

// queuedMessages are the transcript entries still waiting to go out, in the
// order they were sent. Only user messages ever carry the queued state.
func queuedMessages(c Instance) []Message {
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

// noteLine is one line of the summary in front of a chat prompt: the headline
// the cockpit already wrote for that message, how many messages stand behind
// it and whether the turn that produced it broke off. Nothing is formulated
// here, the headline is read off the message.
type noteLine struct {
	headline string
	brokeOff bool
	count    int
}

// noteLineOf reads the line a message the cockpit wrote is summarized by: a
// note's own headline, or the headline of the event a pushed answer reacted
// to. Anything else is not the cockpit speaking and answers false.
func noteLineOf(m Message) (noteLine, bool) {
	switch {
	case m.IsNote():
		return noteLine{headline: oneLine(m.Note.Headline), count: 1}, true
	case m.Role == RoleAssistant && m.Auto:
		line := noteLine{count: 1, brokeOff: m.State == StateFailed || m.State == StateInterrupted}
		if m.Origin != nil {
			line.headline = oneLine(m.Origin.Headline)
		}
		return line, true
	}
	return noteLine{}, false
}

// notesSince is what the cockpit wrote into the thread since the assistant's
// last chat answer, newest first: the notes, a check's reports, and the
// answers a reaction pushed, which are assistant messages too. A chat turn's
// prompt used to be the user's text and nothing else, so an assistant learned
// about its own jobs only when the user told it. The walk stops only at a
// chat answer, never at a pushed one, because a pushed answer came out of a
// session that saw nothing of the thread. Equal headlines fold into one line
// with a count, which is what keeps a schedule firing every half hour from
// writing the same line twenty times; a turn that broke off folds only with
// another one that broke off, because that is the line an assistant must not
// read past.
func notesSince(c Instance) []noteLine {
	type key struct {
		headline string
		brokeOff bool
	}
	var lines []noteLine
	at := map[key]int{}
	for i := len(c.Messages) - 1; i >= 0; i-- {
		m := c.Messages[i]
		if m.Role == RoleAssistant && !m.Auto {
			break
		}
		line, ok := noteLineOf(m)
		if !ok {
			continue
		}
		k := key{line.headline, line.brokeOff}
		if pos, seen := at[k]; seen {
			lines[pos].count++
			continue
		}
		at[k] = len(lines)
		lines = append(lines, line)
	}
	return lines
}

// maxNoteLines bounds that summary. Equal headlines fold, so fifty lines is
// the guard against an outlier and never the everyday case; what is cut is
// the oldest, and the last line says how much.
const maxNoteLines = 50

// withNotes puts the summary of what the cockpit wrote since the last answer
// in front of a user's prompt: the count, where the whole of it stands, and
// one headline per line. The headline is what lets an assistant decide
// whether any of it touches the message it is answering. A bare count could
// not, so it had to read every job to find out, or guess.
func withNotes(lines []noteLine, prompt string) string {
	if len(lines) == 0 {
		return prompt
	}
	notes := 0
	for _, l := range lines {
		notes += l.count
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Since your last answer the cockpit wrote %d note%s into this thread, newest first. job-list and assistant-show on yourself say what, look only when the user's message needs it.\n", notes, plural(notes))
	shown := lines
	if len(shown) > maxNoteLines {
		shown = shown[:maxNoteLines]
	}
	for _, l := range shown {
		b.WriteString("- ")
		if l.count > 1 {
			fmt.Fprintf(&b, "%dx ", l.count)
		}
		b.WriteString(l.headline)
		if l.brokeOff {
			b.WriteString(" (broke off)")
		}
		b.WriteString("\n")
	}
	if rest := len(lines) - len(shown); rest > 0 {
		fmt.Fprintf(&b, "  and %d older\n", rest)
	}
	b.WriteString("\n")
	b.WriteString(prompt)
	return b.String()
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
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

// deriveTitle names an assistant after its first prompt, or after the file it
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
