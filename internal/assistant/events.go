package assistant

import (
	"errors"
	"fmt"
	"log"
	"maps"
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
// are a job's end, a coder's signal out of the notify inbox, a cron tick, the
// end of a compose run, and on a forge the end of a pipeline and a note on a
// change request.
type CockpitEvent struct {
	// ID names the event at its source, apart from every other event there
	// ever is: the entry of the notify inbox a coder's signal came in, a
	// compose run and how it ended, a pipeline and its final status, a review
	// note, a schedule and the tick that fell due. A source that cannot know
	// whether the reactor got an event hands it again under the same name, and
	// a trigger takes it once, see Trigger.hold. The time an event arrived is
	// no name. A job's end is named by its terminal and when it closed, and a
	// signal without a record behind it, a bell, by nothing: it is never handed
	// twice.
	ID     string `json:"id,omitempty"`
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
	// Project and Branch are what a forge event is about.
	Project string `json:"project,omitempty"`
	Branch  string `json:"branch,omitempty"`
	// Folded makes this entry a fold: the events a full window, or the one
	// turn of a one shot, had no room for, counted by their headline, see
	// fold. Headline and Body then say the same in words.
	Folded []FoldedEvents `json:"folded,omitempty"`
	// was is the key a job's terminal was steered under where it took another
	// id since, see Job.MovedFrom. It is read off the job with the event and
	// never stored.
	was string
}

// FoldedEvents counts the events of one headline that a fold took without
// their text.
type FoldedEvents struct {
	Headline string `json:"headline"`
	Count    int    `json:"count"`
}

// folds reports whether this entry is the fold of a window.
func (ev CockpitEvent) folds() bool { return len(ev.Folded) > 0 }

// count is how many events this entry stands for: itself, or what it folds.
func (ev CockpitEvent) count() int {
	if !ev.folds() {
		return 1
	}
	n := 0
	for _, f := range ev.Folded {
		n += f.Count
	}
	return n
}

// countEvents is how many events a window or a turn holds, the folded ones
// counted.
func countEvents(events []CockpitEvent) int {
	n := 0
	for _, ev := range events {
		n += ev.count()
	}
	return n
}

// fold counts one more event on a fold, under its headline: a coder that
// reports forty times is one line that says so. The lines are
// bounded like everything a trigger keeps, so past maxTriggerPendingNotes
// headlines an event is counted under what its kind is called, of which a
// trigger takes a handful at most. The fold is written out in words on every
// change, as an event like any other with a headline and a text: that is what
// a turn reads of it, and what a binary of the release before reads, which
// knows no folds and spends the entry as the event it looks like.
func (ev *CockpitEvent) fold(taken CockpitEvent) {
	line := func(name string) int {
		return slices.IndexFunc(ev.Folded, func(f FoldedEvents) bool { return f.Headline == name })
	}
	name := truncateRunes(oneLine(taken.Headline), maxFoldedHeadlineRunes)
	at := line(name)
	if at < 0 && len(ev.Folded) >= maxTriggerPendingNotes {
		name = EventLabel(taken.Source, taken.Kind)
		at = line(name)
	}
	if at < 0 {
		ev.Folded = append(ev.Folded, FoldedEvents{Headline: name})
		at = len(ev.Folded) - 1
	}
	ev.Folded[at].Count++

	total := ev.count()
	ev.Headline = fmt.Sprintf("%d more event%s arrived than there was room for, counted here without their text", total, plural(total))
	var b strings.Builder
	for i, f := range ev.Folded {
		if i > 0 {
			b.WriteString("\n")
		}
		if f.Count == 1 {
			b.WriteString("- " + f.Headline)
		} else {
			fmt.Fprintf(&b, "- %d times: %s", f.Count, f.Headline)
		}
	}
	ev.Body = b.String()
}

// foldInto counts one more event on the fold of a window or a turn. The fold
// is the last entry, and is made here where there is none yet.
func foldInto(events []CockpitEvent, ev CockpitEvent) []CockpitEvent {
	if n := len(events); n == 0 || !events[n-1].folds() {
		events = append(events, CockpitEvent{Source: ev.Source, Time: ev.Time})
	}
	events[len(events)-1].fold(ev)
	return events
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
	// published sees every event before any trigger does. Wired in main
	// before the first event, nil for none.
	published func(CockpitEvent)

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
// takes a job that already closed, see seedArrived. A trigger carries a model
// only where somebody set one on it, the form's select or `--model`: nothing
// is copied onto one made without, so it follows the owner's Triggers pick at
// the ring, else the owner's chat, as they stand when it fires, see ModelFor.
//
// It runs under the reactor's lock from the moment the trigger is dated: a job
// that closes after that date finds the trigger stored when its report gets
// here, so no end falls between the two.
func (r *Reactor) Add(spec TriggerSpec) (Trigger, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
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
	r.seedArrivedLocked(trigger)
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

// seedArrivedLocked takes the ends a barrier already missed. Three coders are
// started one after another, and the first can be finished before the third
// exists, so a barrier made afterwards would wait for a report nobody will
// send again. A job that is closed at this moment therefore counts as arrived
// right away, with its own report in the window, so the one turn reads about
// every target. The filter still decides: a job that closed blocked is no
// arrival for a trigger on job-done, which is why a barrier belongs on
// job-closed. An end the trigger already took is not taken again, see take.
// Under the reactor's lock.
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
// runs right now runs to its end, its answer still lands. Under the reactor's
// lock like every other change of a trigger: an edit reads the entry and
// stores it whole, and one removed in between would stand again.
func (r *Reactor) Remove(id, by string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
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
// waiting for something that cannot come; where a reaction of it runs, the
// entry goes when that ends, see retire. Answers what was dropped, which is
// the sentence the user reads. That is only ever a trigger that still stood:
// a spent one, a fired sequel or an expired wait, keeps its entry with its
// outcome in the spent list, its target merely marked gone, because it is
// history and nothing was lost with the terminal.
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
	moved := false
	for _, trigger := range r.triggers.All() {
		if !trigger.holds(terminal) {
			continue
		}
		store := r.triggers.Of(trigger.Owner)
		fresh, ok := store.Update(trigger.ID, func(s *Trigger) bool {
			return s.mark(terminal, func(t *TriggerTarget) { t.Gone = true })
		})
		if ok {
			moved = true
		} else {
			fresh = trigger
		}
		if !trigger.Open() {
			continue
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
		r.retire(fresh.Owner, fresh.ID)
		moved = true
		dropped = append(dropped, fresh)
	}
	if moved {
		r.service.changed()
	}
	return dropped
}

// retire takes away a trigger every target of which is gone, once nothing of
// it is on its way any more: a reaction that runs holds the events it reads
// on the entry, and a window that waits behind it is still to be spent, so
// the end of that reaction comes back here. Under the reactor's lock.
func (r *Reactor) retire(owner, id string) {
	store := r.triggers.Of(owner)
	if trigger, ok := store.Get(id); ok && trigger.Vanished() && !trigger.Reacting() && len(trigger.Pending) == 0 {
		store.Delete(id)
	}
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
// the way a notification does. entry is the entry of the notify inbox the
// signal came in, which names the event, empty for a signal that came in
// none, a bell.
func (r *Reactor) Coder(terminal, kind, entry string) {
	terminal = strings.TrimSpace(terminal)
	if terminal == "" {
		return
	}
	id := ""
	if entry != "" {
		id = "coder " + entry
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
		ID:       id,
		Source:   EventCoder,
		Kind:     kind,
		Target:   terminal,
		Time:     r.now().UTC(),
		Headline: "Coder " + what + ": " + noteName(name, terminal) + inProject(project),
	})
}

// OnPublish installs fn to see every published event, matched by a trigger
// or not. fn must not block.
func (r *Reactor) OnPublish(fn func(CockpitEvent)) { r.published = fn }

// Publish matches one event against the triggers it may fire and takes it into
// each of them: at once when the trigger has no batch window, into the open
// window otherwise, where the tick fires it when the window closes. A job
// event is looked up in its owner's store alone.
func (r *Reactor) Publish(ev CockpitEvent) {
	if ev.Time.IsZero() {
		ev.Time = r.now().UTC()
	}
	ev.Body = truncateRunes(strings.TrimSpace(ev.Body), maxEventBodyRunes)
	if r.published != nil {
		r.published(ev)
	}
	owners := r.triggers.Owners()
	if ev.Owner != "" {
		owners = []string{ev.Owner}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, owner := range owners {
		for _, trigger := range r.triggers.Of(owner).List() {
			if trigger.matches(ev) {
				r.take(trigger, ev)
			}
		}
	}
	if ev.Source == EventJob && ev.Owner != "" {
		r.settle(ev.Owner, ev.Time)
	}
}

// take puts one event into a trigger, see Trigger.hold, and fires the window
// where it has nothing left to wait for. Under the reactor's lock.
func (r *Reactor) take(trigger Trigger, ev CockpitEvent) {
	now := r.now().UTC()
	fresh, ok := r.triggers.Of(trigger.Owner).Update(trigger.ID, func(s *Trigger) bool { return s.hold(ev, now) })
	if ok {
		r.fireDue(fresh, now)
	}
}

// fireDue fires a window that is not in the future, right after a take
// instead of on the next tick: a trigger without a batch window, and a
// barrier whose last target arrived long after the window of its first one
// closed. A barrier that still misses one is refused by fire. Under the
// reactor's lock.
func (r *Reactor) fireDue(trigger Trigger, now time.Time) {
	if !trigger.BatchDue.After(now) {
		r.fire(trigger.Owner, trigger.ID)
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
// One turn reads at most maxTriggerPendingNotes events whole, and with them
// the fold of what the window had no room for, see turnOf. The events that
// waited behind those stay and are the next window, due at once, with their
// targets arrived: the end of this reaction fires them. A one shot has no
// next window, so its turn counts them on its fold: whatever the trigger took
// is in the one turn it gets.
//
// One trigger reacts once at a time. While its reaction runs the events stay
// in the window, whatever the window says, and nothing is refused and nothing
// is dropped: the end of that reaction fires them, and the batch fold makes
// them one turn. Two reactions of one trigger would answer the same thread
// about the same thing twice, out of order, each without knowing of the other.
// What holds the window open is cleared where the reaction ends (conclude),
// so a held window always has an end that reaches it, and the tick is its
// backstop.
//
// The events a fire spends stay on the trigger, as its turn, until the
// reaction is concluded: the turn waits for a wake slot before it starts, and
// a restart in that wait would otherwise leave them nowhere but in the memory
// of a process that is gone, see adopt.
func (r *Reactor) fire(owner, id string) {
	now := r.now().UTC()
	var events []CockpitEvent
	refused := ""
	trigger, ok := r.triggers.Of(owner).Update(id, func(s *Trigger) bool {
		if len(s.Pending) == 0 || s.Reacting() || s.Waiting() {
			return false
		}
		events = s.spend()
		s.BatchDue = time.Time{}
		s.UpdatedAt = now
		switch {
		case !s.Open():
			refused = "the trigger is " + string(s.State)
		case !s.ExpiresAt.IsZero() && now.After(s.ExpiresAt):
			s.State = TriggerExpired
			refused = "the trigger expired"
		default:
			var rest []CockpitEvent
			events, rest = turnOf(events, s.Once)
			s.Fired++
			s.LastFiredAt = now
			s.ReactingSince = now
			s.Turn = &TriggerTurn{MessageID: statefile.NewID(), Events: events}
			if s.Once {
				s.State = TriggerDone
			} else if len(rest) > 0 {
				s.Pending = slices.Clone(rest)
				s.BatchDue = now
				for _, ev := range rest {
					s.mark(ev.Target, func(t *TriggerTarget) { t.Met = true })
				}
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
	go r.react(trigger)
}

// turnOf splits a window into what one turn reads and what waits for the
// next: the oldest maxTriggerPendingNotes events whole, and the fold, which
// is only names and a count and so rides with the first turn there is rather
// than behind everything the window still holds. A last turn, a one shot's,
// leaves nothing to a next one: what it has no room for is counted on its
// fold, so the turn stays as bounded as any other and no event the trigger
// took is left out of it.
func turnOf(window []CockpitEvent, last bool) (turn, rest []CockpitEvent) {
	whole := window
	if n := len(window); n > 0 && window[n-1].folds() {
		whole = window[:n-1]
	}
	fold := window[len(whole):]
	if len(whole) > maxTriggerPendingNotes {
		whole, rest = whole[:maxTriggerPendingNotes], whole[maxTriggerPendingNotes:]
	}
	turn = slices.Concat(whole, fold)
	if !last {
		return turn, rest
	}
	for _, ev := range rest {
		turn = foldInto(turn, ev)
	}
	return turn, nil
}

// turnOrigin is the note a reaction of this trigger carries into the thread:
// what fired it, read off the turn the fire wrote.
func turnOrigin(trigger Trigger) Note {
	var events []CockpitEvent
	if trigger.Turn != nil {
		events = trigger.Turn.Events
	}
	origin := Note{
		Source:   NoteEvent,
		Headline: eventsHeadline(events),
		Trigger:  trigger.ID,
		Task:     trigger.Task,
		Count:    countEvents(events),
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
		// The target is a terminal for a job's and a coder's event and the
		// run for a compose event, and only a terminal belongs in Terminal:
		// every reader of it opens a coder.
		if events[0].Source == EventJob || events[0].Source == EventCoder {
			origin.Terminal = events[0].Target
		}
		if events[0].Source == EventCompose {
			origin.Run = events[0].Target
		}
	}
	return origin
}

// react spends the reaction a fire wrote on the trigger: a wake slot, a turn
// in a session of its own, and what came back concluded. The model is the
// trigger's own as it stood at the fire, empty for the owner's, read when the
// turn starts. Once the turn is in the register the trigger says so: from
// then on the register answers for it, and a restart never starts it again.
func (r *Reactor) react(trigger Trigger) {
	r.service.slots.take()
	defer r.service.slots.release()
	owner, turn, origin := trigger.Owner, trigger.Turn, turnOrigin(trigger)
	run, err := r.service.startReaction(owner, origin, reactionPrompt(origin, eventsBody(turn.Events)), trigger.Model, turn.MessageID)
	if err != nil {
		// A turn that never started is a turn that failed, and it is concluded
		// like any other: the run it would have been carries the record.
		r.conclude(&activeRun{rec: RunRecord{Instance: owner, MessageID: turn.MessageID, Origin: &origin}}, wakeOutcome{}, err)
		return
	}
	r.mu.Lock()
	r.triggers.Of(owner).Update(trigger.ID, func(s *Trigger) bool {
		if s.Turn == nil || s.Turn.MessageID != turn.MessageID {
			return false
		}
		s.Turn.Started = true
		return true
	})
	r.mu.Unlock()
	outcome, err := r.service.awaitWake(run)
	r.conclude(run, outcome, err)
}

// adopt takes over the reactions that outlived the server: each is on the
// machine already, so the slot is taken when it is free and skipped when it is
// not, and the answer is concluded exactly as it would have been without the
// restart.
//
// A trigger that says a reaction runs while no run is left holds the events of
// that reaction on its turn, and none of them is dropped. A turn that never
// got into the register never ran, the process died while it waited for a
// slot: it starts now, the one turn those events get. One that was in the
// register and is gone from it ran and was lost before its answer landed: its
// events go into the thread as an answer the restart interrupted, under the
// id that answer was to be pushed under, so one that did land is not written
// twice.
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
	r.mu.Lock()
	var lost []Trigger
	for _, trigger := range r.triggers.All() {
		switch {
		case !trigger.Reacting() || live[trigger.ID]:
		case trigger.Turn != nil && !trigger.Turn.Started:
			go r.react(trigger)
		default:
			lost = append(lost, trigger)
		}
	}
	r.mu.Unlock()
	for _, trigger := range lost {
		origin := turnOrigin(trigger)
		rec := RunRecord{Instance: trigger.Owner, Origin: &origin}
		if trigger.Turn != nil {
			rec.MessageID = trigger.Turn.MessageID
		} else {
			// The release before kept no turn on the trigger, so what that
			// reaction read is not known any more. TODO(v2.0.0)
			origin.Headline, origin.Count = noteName(trigger.Name, "A reaction"), 0
		}
		r.conclude(&activeRun{rec: rec, orphaned: true}, wakeOutcome{}, errors.New("The reaction was lost in a restart."))
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
	// Under the reactor's lock like every change of a trigger: an edit reads
	// the entry and stores it whole, and would put a reaction that ended in
	// between back on it.
	r.mu.Lock()
	defer r.mu.Unlock()
	r.triggers.Of(rec.Instance).Update(origin.Trigger, func(s *Trigger) bool {
		if s.Turn != nil && rec.MessageID != "" && s.Turn.MessageID != rec.MessageID {
			return false
		}
		s.ReactingSince = time.Time{}
		s.Turn = nil
		s.Note = truncateRunes(line, maxDoneWhenRunes)
		s.NoteAt = now
		s.Broke = err != nil
		s.UpdatedAt = now
		return true
	})
	r.service.changed()
	// What arrived while this reaction ran waited for it. Now that nothing of
	// this trigger runs, it is one window and one turn.
	r.fire(rec.Instance, origin.Trigger)
	r.retire(rec.Instance, origin.Trigger)
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
		return Message{}, ErrNotFound
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
		Model:     rec.Model,
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
	if len(events) == 1 && !events[0].folds() {
		return oneLine(events[0].Headline)
	}
	return fmt.Sprintf("%d events arrived", countEvents(events))
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
//
// It has a question asked in plain words and never with an ask block, which
// the instruction file teaches every turn: the page draws no buttons on a
// pushed answer (Message.Choices), because a click lands in the chat
// session, which knows only the headline of this answer. The block would
// stand as raw text.
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
	b.WriteString("If there is nothing to do or say, answer NOTHING on the first line and nothing else: then nothing is pushed and nobody is notified. Otherwise answer as you would answer the user, they read it. When the user has to decide something, ask in plain words and never with an `ask` block: the page shows no buttons on an answer pushed into your thread, and the user answers in the chat.")
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
// it missed. The schedule moves in the write that takes the tick, so a
// process that dies here leaves either a tick that is still due or one that
// was taken, never a schedule that moved on without it.
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
		s.hold(CockpitEvent{
			ID:       "cron " + s.ID + " " + due.UTC().Format(time.RFC3339),
			Source:   EventCron,
			Kind:     CronKindTick,
			Target:   s.ID,
			Owner:    s.Owner,
			Time:     due.UTC(),
			Headline: "Schedule " + s.Spec + " ticked at " + due.In(s.Zone()).Format("15:04") + " " + s.Timezone,
		}, now)
		return true
	})
	if ok {
		r.fireDue(fresh, now)
	}
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
// every job trigger is walked against the closed jobs of its owner, and takes
// the ends it missed. Only that trigger takes them. Nothing is published: an
// end is published once, by the report that closed the job, to every trigger
// and to the plugins, and publishing it again here would hand it a second time
// to everybody who took it then. Where the last process died before that
// report landed, the watcher's recovery wrote it a moment ago, and published
// the end with it for the first time, see Watcher.Recover. A cron tick that
// fell due and a batch window that closed are the tick's business, so one
// runs right away. A coder's signal from that window is in the notify inbox,
// which the inbox poller drains once it runs, and reaches here through it
// like any other; so does one the last process was handing over when it died,
// a second time under the same name. The end of a compose run and of an
// approval come the same way out of their registers, see
// docker.Service.Recover and approval.Service.Recover. The reactions that
// were running are adopted by Service.Recover, which finds them in the
// register.
//
// It must run after Service.Recover, and after the local API is bound: a
// reaction it starts acts through it.
func (r *Reactor) Recover() {
	r.mu.Lock()
	for _, owner := range r.triggers.Owners() {
		ends, written, _ := r.jobEnds(owner)
		for _, trigger := range r.triggers.Of(owner).List() {
			for _, ev := range ends {
				if trigger.missed(ev) {
					r.take(trigger, ev)
				}
			}
		}
		r.settle(owner, written)
	}
	r.mu.Unlock()
	r.Tick()
}

// PruneTerminals drops the closed jobs of terminals that are not in keep, the
// startup's sweep of entries whose terminal is gone, see JobStore.PruneTerminals.
// It runs before Recover, and a job that closed while the process died is only
// in the store: the job of an end a trigger still misses stays for one more
// start, whatever its terminal does, so Recover finds it. Returns how many
// entries were removed.
func (r *Reactor) PruneTerminals(keep map[string]bool) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	keep = maps.Clone(keep)
	if keep == nil {
		keep = map[string]bool{}
	}
	for _, owner := range r.triggers.Owners() {
		ends, _, _ := r.jobEnds(owner)
		for _, trigger := range r.triggers.Of(owner).List() {
			for _, ev := range ends {
				if trigger.missed(ev) {
					keep[ev.Target] = true
				}
			}
		}
	}
	return r.jobs.PruneTerminals(keep)
}

// jobEnds are the ends of an owner's closed jobs as the store holds them, the
// oldest first, and when the newest of those entries was written. read says
// whether the store answered with any job at all: an empty read is a file
// that is gone or could not be read as well as a store without jobs, and
// nothing is dropped on it.
func (r *Reactor) jobEnds(owner string) (ends []CockpitEvent, written time.Time, read bool) {
	jobs := r.jobs.Of(owner).List()
	for _, job := range jobs {
		ev, ok := jobEvent(job)
		if !ok {
			continue
		}
		ends = append(ends, ev)
		if job.UpdatedAt.After(written) {
			written = job.UpdatedAt
		}
	}
	slices.SortStableFunc(ends, func(a, b CockpitEvent) int { return a.Time.Compare(b.Time) })
	return ends, written, len(jobs) > 0
}

// settle closes the books of an owner's job triggers after its job ends were
// handed out, one live or the missed ones of a restart. The ends a trigger
// took are kept only as long as the store holds their job, so the list is
// bounded by the store and never by what a trigger lived through; a store
// that reads empty lets none go, it grows by no end either. And the
// watermark of the release before is raised to seen on every trigger that
// still fires, also on one the ends were not for: that release publishes
// every job written to after any trigger's watermark to all of them, which is
// how a trigger on job-blocked brought every finished job back on every
// restart. Under the reactor's lock. TODO(v2.0.0): drop the watermark.
func (r *Reactor) settle(owner string, seen time.Time) {
	held, _, read := r.jobEnds(owner)
	store := r.triggers.Of(owner)
	for _, trigger := range store.List() {
		if trigger.Source != EventJob {
			continue
		}
		store.Update(trigger.ID, func(s *Trigger) bool {
			kept := slices.DeleteFunc(slices.Clone(s.Ends), func(e JobEnd) bool {
				return read && !slices.ContainsFunc(held, e.is)
			})
			raise := s.Open() && seen.After(s.SeenAt)
			if len(kept) == len(s.Ends) && !raise {
				return false
			}
			s.Ends = kept
			if raise {
				s.Since = s.since()
				s.SeenAt = seen
			}
			return true
		})
	}
}

// jobEvent is the end of a closed job read out of the entry alone, the event a
// check's report published when it closed, see recordWake. Its time is when
// the job closed, which with the terminal names the end. Besides the report it
// is for the two passes that look at the store: the recovery after a restart,
// and a barrier made after one of its jobs had already closed.
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
		Time:     job.closedAt(),
		Headline: "Job " + strings.ToLower(string(verdict)) + ": " + noteName(job.Name, job.Terminal) + inProject(job.Project),
		Body:     job.Note,
		was:      job.MovedFrom,
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
