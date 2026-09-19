package assistant

import (
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/marein/dev-cockpit/internal/statefile"
)

// An event is one thing that happened in the cockpit that an assistant may
// react to. The type is small on purpose and open on purpose: a source that
// wants to be reacted to publishes one line, and the reactor does the rest,
// the matching, the bounds, the reaction and its answer. The sources so far
// are a job's end, a coder's signal out of the notify inbox, and a cron tick.
type CockpitEvent struct {
	Source string `json:"source"`
	Kind   string `json:"kind"`
	// Target is what the event is about: the terminal of a job or a coder, the
	// trigger of a cron tick.
	Target string    `json:"target,omitempty"`
	Time   time.Time `json:"time"`
	// Headline is the one line a reaction is read by, Body the rest of it.
	Headline string `json:"headline"`
	Body     string `json:"body,omitempty"`
	// Owner is the assistant the event belongs to, a job's owner, so only that
	// assistant's triggers see it. Empty is everybody's, a coder's signal is.
	Owner string `json:"owner,omitempty"`
}

// maxEventBodyRunes bounds what one event carries into a prompt. A job's
// report is a few sentences; the bound is for the day a source hands over a
// screen.
const maxEventBodyRunes = 4000

// Reactor turns events into reactions. There is one for the whole cockpit,
// built with the service: it holds every assistant's triggers, matches an
// event against them, collects what arrives inside a batch window, enforces
// the bounds, and spends the reaction. A reaction runs the way a check runs,
// in a provider session of its own with the owner's instruction file, the
// memory and the workspace files, on the wake slot and through the run
// register, so a restart recovers it; nothing is written into the owner's chat
// session for it. Its answer is pushed into the owner's thread as an answer
// started without the user, and an answer of NOTHING pushes nothing.
// Everything the reactor decides is on disk in the trigger itself, so a
// restart picks up an open batch window, a due cron tick and the cap where
// they stood, see Recover.
type Reactor struct {
	service  *Service
	triggers *Triggers
	jobs     *Jobs
	now      func() time.Time
	// coderName answers what a terminal is called and which project it works
	// in, for the headline of a coder's event. Wired in main, where the coder
	// managers are; nil names the terminal by its id.
	coderName func(terminal string) (name, project string)

	// mu serializes a fire with the tick: both read a trigger, decide and
	// write it back, and two of them at once could spend two reactions for one
	// event.
	mu sync.Mutex
}

func newReactor(service *Service, triggers *Triggers, jobs *Jobs) *Reactor {
	r := &Reactor{service: service, triggers: triggers, jobs: jobs, now: time.Now}
	service.events = r
	return r
}

// Events is the reactor of this service: where the triggers live and where an
// event goes.
func (s *Service) Events() *Reactor { return s.events }

// publishEvent hands an event to the reactor, and is a no-op on a service
// without one.
func (s *Service) publishEvent(ev CockpitEvent) {
	if s.events != nil {
		s.events.Publish(ev)
	}
}

// terminalGone hands a deleted terminal to the reactor the same way, and
// answers what that dropped.
func (s *Service) terminalGone(terminal, name, project string) []Trigger {
	if s.events == nil {
		return nil
	}
	return s.events.TerminalGone(terminal, name, project)
}

// SetCoderNamer installs the lookup a coder event's headline is worded with.
func (r *Reactor) SetCoderNamer(fn func(terminal string) (name, project string)) { r.coderName = fn }

// Add stores a new trigger for its owner. A terminal target of a job trigger
// has to carry a job of that owner: any other job's end never reaches this
// assistant, and a trigger that can never fire would stand there as a promise.
// Open is what one target needs, because its end is still to come; a barrier
// takes a job that already closed, see seedArrived.
func (r *Reactor) Add(spec TriggerSpec) (Trigger, error) {
	trigger, err := newTrigger(spec, r.now().UTC())
	if err != nil {
		return Trigger{}, err
	}
	if _, err := r.service.Get(trigger.Owner); err != nil {
		return Trigger{}, err
	}
	if err := r.nameJobTargets(&trigger, nil); err != nil {
		return Trigger{}, err
	}
	store := r.triggers.Of(trigger.Owner)
	open := 0
	for _, existing := range store.List() {
		if existing.Open() {
			open++
		}
	}
	if open >= maxTriggersPerOwner {
		return Trigger{}, fmt.Errorf("This assistant already holds %d standing triggers. Remove one first.", open)
	}
	store.Save(trigger)
	r.seedArrived(trigger)
	r.service.changed()
	return trigger, nil
}

// nameJobTargets checks the terminals of a job trigger and fills the name its
// row shows. A target has to carry a job of this assistant: any other job's
// end never reaches it, and a trigger that can never fire would stand there as
// a promise. Open is what one target needs, because its end is still to come;
// a barrier takes a job that already closed, see seedArrived. A terminal in
// stood was named before and is left alone: it was checked when it was named,
// and its job may well have closed since.
func (r *Reactor) nameJobTargets(trigger *Trigger, stood []TriggerTarget) error {
	if trigger.Source != EventJob {
		return nil
	}
	for i := range trigger.Targets {
		if slices.ContainsFunc(stood, func(t TriggerTarget) bool { return t.Terminal == trigger.Targets[i].Terminal }) {
			continue
		}
		job, ok := r.jobs.Find(trigger.Targets[i].Terminal)
		if !ok || job.Owner != trigger.Owner || (!job.State.Open() && !trigger.All) {
			return fmt.Errorf("Terminal %s carries no open job of yours. Steer it first, or leave the terminal out to react to any job of yours.", trigger.Targets[i].Terminal)
		}
		if trigger.Targets[i].Name == "" {
			trigger.Targets[i].Name = job.Name
		}
	}
	return nil
}

// Edit changes a trigger that still fires: its task, the terminals it waits
// for with their mode, a schedule and the bounds. by is the assistant asking,
// empty for the user, the rule Remove follows. What the spec does not name
// stands, the event never moves, and neither does what the trigger already
// did: the count, when it was made, the events waiting in its window and a
// reaction that runs right now, which keeps the task it was given because it
// has it. It runs under the reactor's lock, the one a fire and the tick take,
// so a change and an arrival cannot write over each other. It answers the
// stored trigger and the line naming what moved, which is what both surfaces
// say afterwards: neither works that line out for itself.
func (r *Reactor) Edit(id, by string, spec TriggerSpec) (Trigger, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	trigger, ok := r.triggers.Find(strings.TrimSpace(id))
	if !ok {
		return Trigger{}, "", errors.New("No trigger has that id.")
	}
	if by != "" && trigger.Owner != by {
		return Trigger{}, "", errors.New("That trigger belongs to another assistant. Only the user or that assistant changes it.")
	}
	next, err := editTrigger(trigger, spec, r.now().UTC())
	if err != nil {
		return Trigger{}, "", err
	}
	if err := r.nameJobTargets(&next, trigger.Targets); err != nil {
		return Trigger{}, "", err
	}
	r.triggers.Of(trigger.Owner).Save(next)
	// A target named for the first time is caught up the way a fresh barrier
	// is: its job may have closed before it was ever waited for.
	r.seedArrivedLocked(next)
	r.service.changed()
	return next, triggerChanges(trigger, next), nil
}

// seedArrived takes the ends a barrier already missed. Three coders are
// started one after another, and the first can be finished before the third
// exists, so a barrier made afterwards would wait for a report nobody will
// send again. A job that is closed at this moment therefore counts as arrived
// right away, with its own report in the window, so the one turn reads about
// every target. The filter still decides: a job that closed blocked is no
// arrival for a trigger on job-done, which is why a barrier belongs on
// job-closed.
func (r *Reactor) seedArrived(trigger Trigger) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seedArrivedLocked(trigger)
}

// seedArrivedLocked is that pass under the reactor's lock, which an edit
// already holds.
func (r *Reactor) seedArrivedLocked(trigger Trigger) {
	if !trigger.All || trigger.Source != EventJob {
		return
	}
	for _, target := range trigger.Targets {
		// A target that arrived is not taken twice: an edit keeps what a
		// target reached, and seeding it again would put its end into the
		// window a second time.
		if target.Met || target.Gone {
			continue
		}
		job, ok := r.jobs.Of(trigger.Owner).Get(target.Terminal)
		if !ok || job.State.Open() {
			continue
		}
		if ev, ok := jobEvent(job); ok && trigger.matches(ev) {
			r.take(trigger, ev)
		}
	}
}

// Remove takes one trigger away. by is the assistant asking, empty for the
// user: the user removes any, an assistant only its own. A reaction of it that
// runs right now runs to its end, its answer still lands.
func (r *Reactor) Remove(id, by string) error {
	trigger, ok := r.triggers.Find(strings.TrimSpace(id))
	if !ok {
		return errors.New("No trigger has that id.")
	}
	if by != "" && trigger.Owner != by {
		return errors.New("That trigger belongs to another assistant. Only the user or that assistant removes it.")
	}
	r.triggers.Of(trigger.Owner).Delete(trigger.ID)
	r.service.changed()
	return nil
}

// List returns every assistant's triggers, the standing ones first; ListOf
// narrows that to one assistant. Get is one by id, whoever owns it.
func (r *Reactor) List() []Trigger {
	out := r.triggers.All()
	SortTriggers(out)
	return out
}

func (r *Reactor) ListOf(owner string) []Trigger {
	if !ValidID(owner) {
		return nil
	}
	return r.triggers.Of(owner).List()
}

func (r *Reactor) Get(id string) (Trigger, bool) { return r.triggers.Find(id) }

// Dropped is what happens to the triggers of an assistant that was deleted:
// the entries went with the directory, the store is forgotten, and a reaction
// of that assistant that still runs is killed, its answer would land nowhere.
func (r *Reactor) Dropped(owner string) {
	r.triggers.Forget(owner)
	s := r.service
	var doomed []*activeRun
	for _, a := range s.running {
		if a.rec.Kind == RunReaction && a.rec.Instance == owner {
			doomed = append(doomed, a)
		}
	}
	for _, a := range doomed {
		a.cancelled.Store(true)
		s.runs.Update(a.rec.ID, func(rec *RunRecord) { rec.Cancelled = true })
		if a.launched {
			a.proc.Kill()
		}
	}
}

// TerminalGone is what a deleted terminal does to the triggers that name it. A
// deletion is the last thing that can ever come from that terminal, so a coder
// trigger on it fires once for the deletion: a task that waits for that coder
// hears about it instead of standing until its expiry, and a barrier counts
// the target as arrived, which is why the event says it was deleted and not
// that it was finished. A trigger every target of which is gone can never fire
// again, so the entry goes, and its window is spent on the way out rather than
// waiting for something that cannot come. Answers what was dropped, which is
// the sentence the user reads.
//
// The job of that terminal is closed before this runs, see
// Watcher.TerminalDeleted, so a job trigger already has that report in its
// window. Only a deletion comes here: a stopped coder keeps its identifier and
// can be resumed under it, so its arrangements stand.
func (r *Reactor) TerminalGone(terminal, name, project string) []Trigger {
	terminal = strings.TrimSpace(terminal)
	if terminal == "" {
		return nil
	}
	now := r.now().UTC()
	r.mu.Lock()
	defer r.mu.Unlock()
	var dropped []Trigger
	for _, trigger := range r.triggers.All() {
		if !trigger.holds(terminal) {
			continue
		}
		store := r.triggers.Of(trigger.Owner)
		fresh, ok := store.Update(trigger.ID, func(s *Trigger) bool {
			return s.mark(terminal, func(t *TriggerTarget) { t.Gone = true })
		})
		if !ok {
			fresh = trigger
		}
		if fresh.Source == EventCoder {
			r.take(fresh, CockpitEvent{
				Source:   EventCoder,
				Kind:     fresh.Kind,
				Target:   terminal,
				Time:     now,
				Headline: "Coder deleted: " + noteName(name, terminal) + inProject(project),
				Body:     "The coder was deleted, so nothing will ever come from this terminal again.",
			})
		}
		if fresh, ok = store.Get(trigger.ID); !ok || !fresh.Vanished() {
			continue
		}
		r.fire(fresh.Owner, fresh.ID)
		store.Delete(fresh.ID)
		dropped = append(dropped, fresh)
	}
	if len(dropped) > 0 {
		r.service.changed()
	}
	return dropped
}

// DroppedNote is what the user reads about the triggers a deleted terminal
// took with it, empty when it took none. One source for the page's flash, the
// JSON answer and what the CLI prints.
func DroppedNote(dropped []Trigger) string {
	if len(dropped) == 0 {
		return ""
	}
	return fmt.Sprintf("%d trigger%s dropped", len(dropped), plural(len(dropped)))
}

// Coder publishes a coder's signal as an event: ended for a turn that is
// over, asks for a question or a permission. The headline names the coder
// the way a notification does.
func (r *Reactor) Coder(terminal, kind string) {
	terminal = strings.TrimSpace(terminal)
	if terminal == "" {
		return
	}
	name, project := "", ""
	if r.coderName != nil {
		name, project = r.coderName(terminal)
	}
	what := "ended its turn"
	if kind == CoderKindAsks {
		what = "asks a question"
	}
	r.Publish(CockpitEvent{
		Source:   EventCoder,
		Kind:     kind,
		Target:   terminal,
		Time:     r.now().UTC(),
		Headline: "Coder " + what + ": " + noteName(name, terminal) + inProject(project),
	})
}

// Publish matches one event against the triggers it may fire and takes it into
// each of them: at once when the trigger has no batch window, into the open
// window otherwise, where the tick fires it when the window closes. A job
// event is looked up in its owner's store alone.
func (r *Reactor) Publish(ev CockpitEvent) {
	if ev.Time.IsZero() {
		ev.Time = r.now().UTC()
	}
	ev.Body = truncateRunes(strings.TrimSpace(ev.Body), maxEventBodyRunes)
	owners := r.triggers.Owners()
	if ev.Owner != "" {
		owners = []string{ev.Owner}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, owner := range owners {
		for _, trigger := range r.triggers.Of(owner).List() {
			if !trigger.matches(ev) {
				continue
			}
			r.take(trigger, ev)
		}
	}
}

// take puts one event into a trigger. Under the reactor's lock.
func (r *Reactor) take(trigger Trigger, ev CockpitEvent) {
	now := r.now().UTC()
	fresh, ok := r.triggers.Of(trigger.Owner).Update(trigger.ID, func(s *Trigger) bool {
		if !s.Open() {
			return false
		}
		if len(s.Pending) >= maxTriggerPendingNotes {
			// A window that fills up fires with what it holds, and what
			// arrives after that opens the next one.
			return false
		}
		s.Pending = append(s.Pending, ev)
		s.mark(ev.Target, func(t *TriggerTarget) { t.Met = true })
		if ev.Time.After(s.SeenAt) {
			s.SeenAt = ev.Time
		}
		if s.BatchDue.IsZero() {
			s.BatchDue = now.Add(s.Batch())
		}
		s.UpdatedAt = now
		return true
	})
	if !ok {
		return
	}
	// A window that is not in the future has nothing left to wait for, so it
	// fires here instead of on the next tick: a trigger without a batch
	// window, and a barrier whose last target arrived long after the window of
	// its first one closed. A barrier that still misses one is refused by
	// fire.
	if !fresh.BatchDue.After(now) {
		r.fire(fresh.Owner, fresh.ID)
	}
}

// fire spends what a trigger's window holds: within the bounds, one reaction.
// Every bound is decided inside the write, and a turn refused by one is
// written on the trigger's line, where the page and `trigger-list` show it,
// never into the thread. Under the reactor's lock; the reaction itself runs
// off it.
//
// A barrier holds its window until every target it names arrived, and the
// arrivals are taken back with the events it spends, so a standing one waits
// for all of them again.
//
// One trigger reacts once at a time. While its reaction runs the events stay
// in the window, whatever the window says, and nothing is refused and nothing
// is dropped: the end of that reaction fires them, and the batch fold makes
// them one turn. Two reactions of one trigger would answer the same thread
// about the same thing twice, out of order, each without knowing of the other.
// What holds the window open is cleared where the reaction ends (conclude,
// adopt), so a held window always has an end that reaches it, and the tick is
// its backstop.
func (r *Reactor) fire(owner, id string) {
	now := r.now().UTC()
	var events []CockpitEvent
	refused := ""
	trigger, ok := r.triggers.Of(owner).Update(id, func(s *Trigger) bool {
		if len(s.Pending) == 0 || s.Reacting() || s.Waiting() {
			return false
		}
		events = s.Pending
		s.Pending = nil
		s.clearMet()
		s.BatchDue = time.Time{}
		s.UpdatedAt = now
		switch {
		case !s.Open():
			refused = "the trigger is " + string(s.State)
		case !s.ExpiresAt.IsZero() && now.After(s.ExpiresAt):
			s.State = TriggerExpired
			refused = "the trigger expired"
		default:
			s.Fired++
			s.LastFiredAt = now
			s.ReactingSince = now
			if s.Once {
				s.State = TriggerDone
			}
		}
		s.NoteAt = now
		if refused != "" {
			s.Note = "No turn for " + eventsHeadline(events) + ": " + refused + "."
		} else {
			s.Note = "Fired for " + eventsHeadline(events) + "."
		}
		return true
	})
	if !ok {
		return
	}
	r.service.changed()
	if refused != "" {
		return
	}
	origin := Note{
		Source:   NoteEvent,
		Headline: eventsHeadline(events),
		Trigger:  trigger.ID,
		Task:     trigger.Task,
		Count:    len(events),
	}
	// The name the user gave the trigger is the line it is read by, and this
	// is the one place that decides it: the notification, the header over the
	// pushed answer and the line in front of the next chat prompt all read the
	// note's headline, so they take the name without knowing that names exist.
	// What fired it moves into Event, where the readers with room for both
	// still find it, and without a name nothing moves at all.
	if trigger.Name != "" {
		origin.Event, origin.Headline = origin.Headline, trigger.Name
	}
	if len(events) == 1 {
		origin.Verdict = events[0].Kind
		origin.Terminal = events[0].Target
	}
	go r.react(owner, origin, eventsBody(events))
}

// react spends one reaction: a wake slot, a turn in a session of its own,
// and what came back concluded.
func (r *Reactor) react(owner string, origin Note, body string) {
	r.service.slots.take()
	defer r.service.slots.release()
	run, err := r.service.startReaction(owner, origin, reactionPrompt(origin, body))
	if err != nil {
		// A turn that never started is a turn that failed, and it is concluded
		// like any other: the run it would have been carries the record.
		r.conclude(&activeRun{rec: RunRecord{Instance: owner, Origin: &origin}}, wakeOutcome{}, err)
		return
	}
	outcome, err := r.service.awaitWake(run)
	r.conclude(run, outcome, err)
}

// adopt takes over the reactions that outlived the server: each is on the
// machine already, so the slot is taken when it is free and skipped when it is
// not, and the answer is concluded exactly as it would have been without the
// restart. A trigger that says a reaction runs while no run is left is
// cleared, the reaction died with the process.
func (r *Reactor) adopt(runs []*activeRun) {
	live := map[string]bool{}
	for _, a := range runs {
		if a.rec.Origin != nil {
			live[a.rec.Origin.Trigger] = true
		}
		go func(a *activeRun) {
			held := r.service.slots.tryTake()
			outcome, err := r.service.awaitWake(a)
			r.conclude(a, outcome, err)
			if held {
				r.service.slots.release()
			}
		}(a)
	}
	now := r.now().UTC()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, trigger := range r.triggers.All() {
		if !trigger.Reacting() || live[trigger.ID] {
			continue
		}
		r.triggers.Of(trigger.Owner).Update(trigger.ID, func(s *Trigger) bool {
			s.ReactingSince = time.Time{}
			s.Note = "The reaction was lost in a restart."
			s.NoteAt = now
			s.Broke = true
			return true
		})
		// A window this reaction held open outlived it on disk, and the run
		// it waited for is not coming back: it fires now.
		r.fire(trigger.Owner, trigger.ID)
	}
}

// conclude turns what a reaction came back with into what the thread and the
// trigger show. An answer is pushed into the owner's thread as one started
// without the user, and a turn that broke off goes exactly that way too, only
// marked as what it is: the same message, the same notification, the same
// push, carrying what the turn had written when it stopped and the sentence
// that says why, so a failure never disappears without a trace. The state is
// the reading a chat turn gets, see turnState. NOTHING on the first line of a
// finished answer pushes nothing and notifies nobody. The reaction counted as
// fired when it fired, whatever it answered.
func (r *Reactor) conclude(a *activeRun, outcome wakeOutcome, err error) {
	rec := a.rec
	origin := rec.Origin
	if origin == nil {
		return
	}
	now := r.now().UTC()
	state, sentence := a.turnState(err)
	// A failed turn is never read as the quiet contract: what stands in it is
	// half an answer, not the one word that means there is nothing to say.
	quiet := err == nil && quietAnswer(outcome.Text)
	// The trigger's own note says what happened and not what the trigger is
	// called: it stands under the row's heading, which is the name already,
	// and it is the line that has to agree with the one fire wrote a moment
	// earlier.
	line := "Answered for " + origin.Occasion() + "."
	switch {
	case err != nil:
		log.Printf("assistant: reaction of %s: %v", rec.Instance, err)
		line = "The reaction for " + origin.Occasion() + " broke off: " + sentence
	case quiet:
		line = "Fired for " + origin.Occasion() + ", nothing to do."
	}
	if !quiet {
		if _, pushErr := r.service.pushReaction(rec, outcome.Text, state, sentence); pushErr != nil {
			log.Printf("assistant: the answer of a reaction of %s could not be pushed: %v", rec.Instance, pushErr)
		}
	}
	r.triggers.Of(rec.Instance).Update(origin.Trigger, func(s *Trigger) bool {
		s.ReactingSince = time.Time{}
		s.Note = truncateRunes(line, maxDoneWhenRunes)
		s.NoteAt = now
		s.Broke = err != nil
		s.UpdatedAt = now
		return true
	})
	r.service.changed()
	// What arrived while this reaction ran waited for it. Now that nothing of
	// this trigger runs, it is one window and one turn.
	r.mu.Lock()
	r.fire(rec.Instance, origin.Trigger)
	r.mu.Unlock()
}

// pushReaction writes a reaction's answer into the owner's thread: an
// assistant message marked as started without the user, carrying the event
// and the task it came from as its origin. It is announced on a frame of its
// own, so a page holds it while a chat answer streams and lands it after,
// and it is news like any answer. The id is the register's, so a reaction
// concluded twice, once before and once after a restart, pushes one message.
//
// A turn that broke off is written the same way, under the state it came back
// as and with the sentence the reader sees under it, the way a chat turn that
// failed is written: the text is then what the turn had said before it
// stopped, which is the best account of what it was doing.
func (s *Service) pushReaction(rec RunRecord, text string, state State, errText string) (Message, error) {
	owner, messageID, origin := rec.Instance, rec.MessageID, *rec.Origin
	s.mu.Lock()
	c, ok := s.store.Load(owner)
	if !ok {
		s.mu.Unlock()
		return Message{}, errors.New("Assistant not found.")
	}
	if messageID == "" {
		messageID = statefile.NewID()
	}
	for _, existing := range c.Messages {
		if existing.ID == messageID {
			s.mu.Unlock()
			return existing, nil
		}
	}
	now := s.now().UTC()
	msg := Message{
		ID:        messageID,
		Role:      RoleAssistant,
		Content:   text,
		CreatedAt: now,
		State:     state,
		Error:     errText,
		Auto:      true,
		Origin:    &origin,
	}
	c.Messages = append(c.Messages, msg)
	c.UpdatedAt = now
	s.store.Save(c)
	s.mu.Unlock()

	s.hub.publish(c.ID, StreamEvent{Kind: FrameMessage, MessageID: msg.ID})
	s.changed()
	if s.onDone != nil {
		s.onDone(c.ID)
	}
	return msg, nil
}

// eventsHeadline is the line a reaction of one or several events is read by.
func eventsHeadline(events []CockpitEvent) string {
	if len(events) == 1 {
		return oneLine(events[0].Headline)
	}
	return fmt.Sprintf("%d events arrived", len(events))
}

// eventsBody is what the reaction reads about the events: the one event's
// body, or every event with its headline in front.
func eventsBody(events []CockpitEvent) string {
	if len(events) == 1 {
		return strings.TrimSpace(events[0].Body)
	}
	var b strings.Builder
	for i, ev := range events {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "**%s**", oneLine(ev.Headline))
		if body := strings.TrimSpace(ev.Body); body != "" {
			b.WriteString("\n\n" + body)
		}
	}
	return b.String()
}

// noteName is what a headline calls a coder: its name, else its terminal.
func noteName(name, terminal string) string {
	if strings.TrimSpace(name) == "" {
		return terminal
	}
	return strings.TrimSpace(name)
}

// inProject is the " in <project>" tail of a headline, empty without one.
func inProject(project string) string {
	if strings.TrimSpace(project) == "" {
		return ""
	}
	return " in " + strings.TrimSpace(project)
}

// reactionPrompt is what a reaction reads: the cockpit speaking, the event
// and the task. The session is the reaction's own, so the prompt says what
// it can see and where its answer lands, and it carries the one word that
// means nothing to say.
//
// It also says what a check's prompt says about its first line, and for the
// same reason: this answer travels to a phone, where a sentence or two of it
// is all there is (answerExcerpt in the cli package cuts it). A check may
// think out loud because parseVerdict throws the preamble away; here nothing
// is thrown away, so an announcement in front of the result eats the message
// itself. And a reaction has no verdict: it reads the same instruction file a
// check reads, which is where a DONE in an answer nobody asked for comes
// from.
//
// It says what a check's prompt says about the two hours too, and for a
// sharper reason: a check that runs into the deadline has a next check to
// carry on, a reaction has nothing and lands in the thread as one that broke
// off.
func reactionPrompt(origin Note, body string) string {
	var b strings.Builder
	b.WriteString("This turn was started by the cockpit, not by the user: an event you set a trigger on fired. You run in a session of your own and see nothing of the conversation; your answer is pushed into your thread, where the user reads it as one started without them.\n\n")
	if origin.Count > 1 {
		fmt.Fprintf(&b, "%d events arrived inside one window, here they are:\n\n", origin.Count)
	} else {
		fmt.Fprintf(&b, "Event: %s\n\n", oneLine(origin.Occasion()))
	}
	if body = strings.TrimSpace(body); body != "" {
		b.WriteString(body + "\n\n")
	}
	if task := strings.TrimSpace(origin.Task); task != "" {
		fmt.Fprintf(&b, "Your task for this event: %s\n\n", task)
	}
	b.WriteString("This turn has two hours and is killed when that runs out, and nothing carries on after it: if you cannot finish inside that, answer with what you have and what is still open.\n\n")
	b.WriteString("Your whole answer starts with the result. Do not announce what you are about to do: the user reads its first line on their phone, where a sentence or two is all that fits. You have no verdict here: DONE, BLOCKED and WORKING belong to a check, not to a reaction. Write in the language the task is written in.\n\n")
	b.WriteString("If there is nothing to do or say, answer NOTHING on the first line and nothing else: then nothing is pushed and nobody is notified. Otherwise answer as you would answer the user, they read it.")
	return b.String()
}

// quietAnswer reports whether a reaction had nothing to say. The contract is
// NOTHING and nothing else, and the word is found by the check's own reading
// (parseVerdict) rather than by a second one: a model that talks first and
// puts its NOTHING behind the preamble means what a model answering it bare
// means, and two readings of one habit drift apart, which is how a reaction
// that had decided right rang the user anyway. That reading's guards against
// prose come with it, upper case and no other verdict beside it, and what is
// left of the answer is the last of them: a reaction that carries the word
// into something it wrote for the user leaves that text standing behind it,
// and text left over is an answer to push. A turn that wrote nothing at all
// said nothing, which is not the same as saying NOTHING, so it is pushed like
// any other.
func quietAnswer(text string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	verdict, rest := parseVerdict(text)
	return verdict == VerdictNothing && rest == ""
}

// reactorInterval is how often the reactor looks at its triggers: a batch
// window that closed, a cron tick that is due, an expiry that passed. Ten
// seconds keeps a half minute window honest and costs a few file stats.
const reactorInterval = 10 * time.Second

// Run ticks for as long as the process lives. Blocks; run it in a goroutine.
func (r *Reactor) Run(interval time.Duration) {
	if interval <= 0 {
		interval = reactorInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		r.Tick()
	}
}

// Tick is one pass over every trigger: the cron ticks that are due become
// events, the batch windows that closed fire, and the standing triggers whose
// expiry passed end, which their state and their line say.
func (r *Reactor) Tick() {
	now := r.now().UTC()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, trigger := range r.triggers.All() {
		switch {
		case trigger.Open() && !trigger.ExpiresAt.IsZero() && now.After(trigger.ExpiresAt):
			r.expire(trigger, now)
		case trigger.Open() && trigger.Source == EventCron && !trigger.NextAt.IsZero() && !now.Before(trigger.NextAt):
			r.cronTick(trigger, now)
		}
		if len(trigger.Pending) > 0 && !trigger.BatchDue.IsZero() && !now.Before(trigger.BatchDue) {
			r.fire(trigger.Owner, trigger.ID)
		}
	}
}

// cronTick fires one due tick and moves the schedule on past now: a tick
// that was due while the cockpit was down is due once, not once per minute
// it missed.
func (r *Reactor) cronTick(trigger Trigger, now time.Time) {
	due := trigger.NextAt
	next, err := NextCron(trigger.Spec, now, trigger.Zone())
	fresh, ok := r.triggers.Of(trigger.Owner).Update(trigger.ID, func(s *Trigger) bool {
		if !s.Open() || !s.NextAt.Equal(due) {
			return false
		}
		if err != nil {
			s.NextAt = time.Time{}
		} else {
			s.NextAt = next
		}
		s.UpdatedAt = now
		return true
	})
	if !ok {
		return
	}
	r.take(fresh, CockpitEvent{
		Source:   EventCron,
		Kind:     CronKindTick,
		Target:   fresh.ID,
		Owner:    fresh.Owner,
		Time:     due.UTC(),
		Headline: "Schedule " + fresh.Spec + " ticked at " + due.In(fresh.Zone()).Format("15:04") + " " + fresh.Timezone,
	})
}

// expire ends a trigger whose time is over. Its state and its line say so on
// the page and in `trigger-list`; the thread hears nothing, an expiry is no
// event.
func (r *Reactor) expire(trigger Trigger, now time.Time) {
	if _, ok := r.triggers.Of(trigger.Owner).Update(trigger.ID, func(s *Trigger) bool {
		if !s.Open() {
			return false
		}
		s.State = TriggerExpired
		s.Pending = nil
		s.BatchDue = time.Time{}
		s.Note = "Expired, nothing fires it any more."
		s.NoteAt = now
		s.UpdatedAt = now
		return true
	}); ok {
		r.service.changed()
	}
}

// Recover picks up what a restart left behind. The triggers are on disk, so
// nothing has to be rebuilt; what has to be caught up is what happened while
// nobody was listening. A job that closed in that window is in jobs.json:
// every job trigger is walked against the closed jobs of its owner that ended
// after the watermark it last took, and those are published now. A cron tick
// that fell due and a batch window that closed are the tick's business, so one
// runs right away. A coder's signal from that window is in the notify inbox,
// which the inbox poller drains once it runs, and reaches here through it like
// any other. The reactions that were running are adopted by Service.Recover,
// which finds them in the register.
//
// It must run after Service.Recover, and after the local API is bound: a
// reaction it starts acts through it.
func (r *Reactor) Recover() {
	for _, trigger := range r.triggers.All() {
		if !trigger.Open() || trigger.Source != EventJob {
			continue
		}
		for _, job := range r.jobs.Of(trigger.Owner).List() {
			if job.State.Open() || !job.UpdatedAt.After(trigger.SeenAt) {
				continue
			}
			if ev, ok := jobEvent(job); ok {
				r.Publish(ev)
			}
		}
	}
	r.Tick()
}

// jobEvent is the end of a closed job read out of the entry alone, the event a
// check's report published when it closed. It is for the two passes that look
// at the store instead of at a report: the recovery after a restart, and a
// barrier made after one of its jobs had already closed.
func jobEvent(job Job) (CockpitEvent, bool) {
	verdict, ok := jobEndVerdict(job.State)
	if !ok {
		return CockpitEvent{}, false
	}
	return CockpitEvent{
		Source:   EventJob,
		Kind:     string(verdict),
		Target:   job.Terminal,
		Owner:    job.Owner,
		Time:     job.UpdatedAt,
		Headline: "Job " + strings.ToLower(string(verdict)) + ": " + noteName(job.Name, job.Terminal) + inProject(job.Project),
		Body:     job.Note,
	}, true
}

// jobEndVerdict is the verdict a closed job's state was written with; a job
// the user called off is no event.
func jobEndVerdict(state JobState) (Verdict, bool) {
	switch state {
	case JobDone:
		return VerdictDone, true
	case JobBlocked:
		return VerdictBlocked, true
	case JobExpired:
		return VerdictExpired, true
	}
	return "", false
}
