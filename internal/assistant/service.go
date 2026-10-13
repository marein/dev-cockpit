package assistant

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

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
	// StartedAt is when the turn started, the moment the page pins its
	// working mark to; zero while Queued.
	StartedAt time.Time
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
	agent    []string

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

// SetAgent hands every turn started from now on, a chat turn, a check or a
// reaction, the environment of the assistants' socket of the cockpit's ssh
// agent. It is set at wiring time, before the first turn starts.
func (s *Service) SetAgent(env []string) {
	s.agent = env
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

// RunningSince answers when the generation in flight for this instance
// started, zero while none is. Every surface that marks the assistant as
// working pins the mark to this moment, so it stands where it stood when it
// is drawn again and two assistants run apart the way their turns started.
func (s *Service) RunningSince(id string) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a := s.chatRunLocked(id); a != nil {
		return a.rec.StartedAt
	}
	return time.Time{}
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
	// The three defaults of the Models tab are creation defaults, copied onto
	// the new assistant's chat, check and trigger model where one is set and
	// left empty otherwise, the coder's start default for the chat and Same
	// as chat for the other two. Nothing on that tab is read at run time, and
	// a trigger takes nothing at all, it carries a model only where somebody
	// set one on it.
	defaults := co.ModelDefaults()
	c := Instance{
		Summary: Summary{
			ID:            id,
			Title:         DefaultTitle,
			CoderID:       co.ID,
			Model:         defaults.Chat,
			CheckModel:    defaults.Check,
			TriggerModel:  defaults.Trigger,
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

// ModelChoice is what a save of the ring's three model selects names: the
// chat model, the check model and the trigger model, each with whether the
// request carried the field at all, so a field nobody named leaves what
// stands and an empty one clears it, the way a trigger's spec reads its
// fields.
type ModelChoice struct {
	Chat, Check, Trigger          string
	ChatSet, CheckSet, TriggerSet bool
}

// SetModels stores which models an assistant's own turns run on. Every name
// goes through CleanModel, so what is stored is what a runner may put behind
// its flag, and a refused name changes nothing. It answers the instance as it
// stands afterwards, which is what the surfaces word their confirmation from
// and what the save's answer carries as the picks with their stamp. The
// fresh picks go out on the assistant's own stream as one models frame, so
// every open page of it moves its ring's selects without a reload, whether
// the pick was set on that page, on another tab or from the CLI. A save that
// moves nothing writes, stamps and publishes nothing. One that moves a pick
// stamps UpdatedAt, because that stamp is what orders the models frames.
func (s *Service) SetModels(id string, choice ModelChoice) (Instance, error) {
	chat, err := CleanModel(choice.Chat)
	if err != nil {
		return Instance{}, err
	}
	check, err := CleanModel(choice.Check)
	if err != nil {
		return Instance{}, err
	}
	trigger, err := CleanModel(choice.Trigger)
	if err != nil {
		return Instance{}, err
	}

	s.mu.Lock()
	c, ok := s.store.Load(id)
	if !ok {
		s.mu.Unlock()
		return Instance{}, errors.New("Assistant not found.")
	}
	was := [3]string{c.Model, c.CheckModel, c.TriggerModel}
	if choice.ChatSet {
		c.Model = chat
	}
	if choice.CheckSet {
		c.CheckModel = check
	}
	if choice.TriggerSet {
		c.TriggerModel = trigger
	}
	if [3]string{c.Model, c.CheckModel, c.TriggerModel} == was {
		s.mu.Unlock()
		return c, nil
	}
	c.UpdatedAt = s.now().UTC()
	s.store.Save(c)
	s.mu.Unlock()

	picks := c.ModelPicks()
	s.hub.publish(c.ID, StreamEvent{Kind: FrameModels, Models: &picks})
	s.changed()
	return c, nil
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
	d, changed := s.drafts.Of(id).SaveText(text, attachments, s.now().UTC())
	return d, changed, nil
}

// Draft is what the composer and the open blocks of an assistant hold right
// now. It is its own read so a device catching up pulls the draft, not the
// transcript.
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

// sanitizeError keeps a provider failure short and one line. Runners already
// return curated sentences, this is the backstop that keeps a stray path or a
// stack trace out of the browser. A failure that quotes the coder is two
// bounded parts, the sentence and the line behind it, each kept under the same
// bound, so the quote is never what cuts the sentence off. The redaction
// reaches the quoted line alone (quoteLine, before it got here): a sentence
// the cockpit wrote carries nothing to redact, and the one it writes for a
// refused model carries the name, which the redaction would take for a token.
func sanitizeError(err error) string {
	var said *Said
	if errors.As(err, &said) {
		return sanitizeError(said.Err) + " The coder said: " + said.Line
	}
	msg := truncateRunes(oneLine(err.Error()), quoteRunes)
	if msg == "" {
		return "The coder could not answer this message."
	}
	return msg
}
