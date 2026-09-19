package assistant

import (
	"errors"
	"fmt"
	"log"
	"regexp"
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
	// Target is what the event is about: the terminal of a job or a coder,
	// the subscription of a cron tick.
	Target string    `json:"target,omitempty"`
	Time   time.Time `json:"time"`
	// Headline is the one line a reaction is read by, Body the rest of it.
	Headline string `json:"headline"`
	Body     string `json:"body,omitempty"`
	// Owner is the assistant the event belongs to, a job's owner, so only
	// that assistant's subscriptions see it. Empty is everybody's, a coder's
	// signal is.
	Owner string `json:"owner,omitempty"`
}

// maxEventBodyRunes bounds what one event carries into a prompt. A job's
// report is a few sentences; the bound is for the day a source hands over a
// screen.
const maxEventBodyRunes = 4000

// Reactor turns events into reactions. There is one for the whole cockpit,
// built with the service: it holds every assistant's subscriptions, matches
// an event against them, collects what arrives inside a batch window,
// enforces the bounds, and spends the reaction. A reaction runs the way a
// check runs, in a provider session of its own with the owner's instruction
// file, the memory and the workspace files, on the wake slot and through the
// run register, so a restart recovers it; nothing is written into the owner's
// chat session for it. Its answer is pushed into the owner's thread as an
// answer started without the user, and an answer of NOTHING pushes nothing.
// Everything the reactor decides is on disk in the subscription itself, so a
// restart picks up an open batch window, a due cron tick and the cap where
// they stood, see Recover.
type Reactor struct {
	service *Service
	subs    *Subscriptions
	jobs    *Jobs
	now     func() time.Time
	// coderName answers what a terminal is called and which project it works
	// in, for the headline of a coder's event. Wired in main, where the coder
	// managers are; nil names the terminal by its id.
	coderName func(terminal string) (name, project string)

	// mu serializes a fire with the tick: both read a subscription, decide
	// and write it back, and two of them at once could spend two reactions
	// for one event.
	mu sync.Mutex
}

func newReactor(service *Service, subs *Subscriptions, jobs *Jobs) *Reactor {
	r := &Reactor{service: service, subs: subs, jobs: jobs, now: time.Now}
	service.events = r
	return r
}

// Events is the reactor of this service: where the subscriptions live and
// where an event goes.
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
func (s *Service) terminalGone(terminal, name, project string) []Subscription {
	if s.events == nil {
		return nil
	}
	return s.events.TerminalGone(terminal, name, project)
}

// SetCoderNamer installs the lookup a coder event's headline is worded with.
func (r *Reactor) SetCoderNamer(fn func(terminal string) (name, project string)) { r.coderName = fn }

// Subscribe stores a new subscription for its owner. A terminal target of a
// job subscription has to carry a job of that owner: any other job's end never
// reaches this assistant, and a subscription that can never fire would stand
// there as a promise. Open is what one target needs, because its end is still
// to come; a barrier takes a job that already closed, see seedArrived.
func (r *Reactor) Subscribe(spec SubscriptionSpec) (Subscription, error) {
	sub, err := newSubscription(spec, r.now().UTC())
	if err != nil {
		return Subscription{}, err
	}
	if _, err := r.service.Get(sub.Owner); err != nil {
		return Subscription{}, err
	}
	if err := r.nameJobTargets(&sub, nil); err != nil {
		return Subscription{}, err
	}
	store := r.subs.Of(sub.Owner)
	open := 0
	for _, existing := range store.List() {
		if existing.Open() {
			open++
		}
	}
	if open >= maxSubscriptionsPerOwner {
		return Subscription{}, fmt.Errorf("This assistant already holds %d standing subscriptions. Remove one first.", open)
	}
	store.Save(sub)
	r.seedArrived(sub)
	r.service.changed()
	return sub, nil
}

// nameJobTargets checks the terminals of a job subscription and fills the name
// its row shows. A target has to carry a job of this assistant: any other
// job's end never reaches it, and a subscription that can never fire would
// stand there as a promise. Open is what one target needs, because its end is
// still to come; a barrier takes a job that already closed, see seedArrived.
// A terminal in stood was named before and is left alone: it was checked when
// it was named, and its job may well have closed since.
func (r *Reactor) nameJobTargets(sub *Subscription, stood []SubscriptionTarget) error {
	if sub.Source != EventJob {
		return nil
	}
	for i := range sub.Targets {
		if slices.ContainsFunc(stood, func(t SubscriptionTarget) bool { return t.Terminal == sub.Targets[i].Terminal }) {
			continue
		}
		job, ok := r.jobs.Find(sub.Targets[i].Terminal)
		if !ok || job.Owner != sub.Owner || (!job.State.Open() && !sub.All) {
			return fmt.Errorf("Terminal %s carries no open job of yours. Steer it first, or leave the terminal out to react to any job of yours.", sub.Targets[i].Terminal)
		}
		if sub.Targets[i].Name == "" {
			sub.Targets[i].Name = job.Name
		}
	}
	return nil
}

// Edit changes a subscription that still fires: its task, the terminals it
// waits for with their mode, a schedule and the bounds. by is the assistant
// asking, empty for the user, the rule Unsubscribe follows. What the spec does
// not name stands, the event never moves, and neither does what the
// subscription already did: the count, when it was made, the events waiting in
// its window and a reaction that runs right now, which keeps the task it was
// given because it has it. It runs under the reactor's lock, the one a fire
// and the tick take, so a change and an arrival cannot write over each other.
// It answers the stored subscription and the line naming what moved, which is
// what both surfaces say afterwards: neither works that line out for itself.
func (r *Reactor) Edit(id, by string, spec SubscriptionSpec) (Subscription, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sub, ok := r.subs.Find(strings.TrimSpace(id))
	if !ok {
		return Subscription{}, "", errors.New("No subscription has that id.")
	}
	if by != "" && sub.Owner != by {
		return Subscription{}, "", errors.New("That subscription belongs to another assistant. Only the user or that assistant changes it.")
	}
	next, err := editSubscription(sub, spec, r.now().UTC())
	if err != nil {
		return Subscription{}, "", err
	}
	if err := r.nameJobTargets(&next, sub.Targets); err != nil {
		return Subscription{}, "", err
	}
	r.subs.Of(sub.Owner).Save(next)
	// A target named for the first time is caught up the way a fresh barrier
	// is: its job may have closed before it was ever waited for.
	r.seedArrivedLocked(next)
	r.service.changed()
	return next, subscriptionChanges(sub, next), nil
}

// seedArrived takes the ends a barrier already missed. Three coders are
// started one after another, and the first can be finished before the third
// exists, so a barrier made afterwards would wait for a report nobody will
// send again. A job that is closed at this moment therefore counts as arrived
// right away, with its own report in the window, so the one turn reads about
// every target. The filter still decides: a job that closed blocked is no
// arrival for a subscription on job-done, which is why a barrier belongs on
// job-closed.
func (r *Reactor) seedArrived(sub Subscription) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seedArrivedLocked(sub)
}

// seedArrivedLocked is that pass under the reactor's lock, which an edit
// already holds.
func (r *Reactor) seedArrivedLocked(sub Subscription) {
	if !sub.All || sub.Source != EventJob {
		return
	}
	for _, target := range sub.Targets {
		// A target that arrived is not taken twice: an edit keeps what a
		// target reached, and seeding it again would put its end into the
		// window a second time.
		if target.Met || target.Gone {
			continue
		}
		job, ok := r.jobs.Of(sub.Owner).Get(target.Terminal)
		if !ok || job.State.Open() {
			continue
		}
		if ev, ok := jobEvent(job); ok && sub.matches(ev) {
			r.take(sub, ev)
		}
	}
}

// Unsubscribe removes one subscription. by is the assistant asking, empty for
// the user: the user removes any, an assistant only its own. A reaction of it
// that runs right now runs to its end, its answer still lands.
func (r *Reactor) Unsubscribe(id, by string) error {
	sub, ok := r.subs.Find(strings.TrimSpace(id))
	if !ok {
		return errors.New("No subscription has that id.")
	}
	if by != "" && sub.Owner != by {
		return errors.New("That subscription belongs to another assistant. Only the user or that assistant removes it.")
	}
	r.subs.Of(sub.Owner).Delete(sub.ID)
	r.service.changed()
	return nil
}

// List returns every assistant's subscriptions, the standing ones first;
// ListOf narrows that to one assistant. Get is one by id, whoever owns it.
func (r *Reactor) List() []Subscription {
	out := r.subs.All()
	SortSubscriptions(out)
	return out
}

func (r *Reactor) ListOf(owner string) []Subscription {
	if !ValidID(owner) {
		return nil
	}
	return r.subs.Of(owner).List()
}

func (r *Reactor) Get(id string) (Subscription, bool) { return r.subs.Find(id) }

// Dropped is what happens to the subscriptions of an assistant that was
// deleted: the entries went with the directory, the store is forgotten, and
// a reaction of that assistant that still runs is killed, its answer would
// land nowhere.
func (r *Reactor) Dropped(owner string) {
	r.subs.Forget(owner)
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

// TerminalGone is what a deleted terminal does to the subscriptions that name
// it. A deletion is the last thing that can ever come from that terminal, so a
// coder subscription on it fires once for the deletion: a task that waits for
// that coder hears about it instead of standing until its expiry, and a barrier
// counts the target as arrived, which is why the event says it was deleted and
// not that it was finished. A subscription every target of which is gone can
// never fire again, so the entry goes, and its window is spent on the way out
// rather than waiting for something that cannot come. Answers what was dropped,
// which is the sentence the user reads.
//
// The job of that terminal is closed before this runs, see Watcher.TerminalDeleted,
// so a job subscription already has that report in its window. Only a deletion
// comes here: a stopped coder keeps its identifier and can be resumed under it,
// so its arrangements stand.
func (r *Reactor) TerminalGone(terminal, name, project string) []Subscription {
	terminal = strings.TrimSpace(terminal)
	if terminal == "" {
		return nil
	}
	now := r.now().UTC()
	r.mu.Lock()
	defer r.mu.Unlock()
	var dropped []Subscription
	for _, sub := range r.subs.All() {
		if !sub.holds(terminal) {
			continue
		}
		store := r.subs.Of(sub.Owner)
		fresh, ok := store.Update(sub.ID, func(s *Subscription) bool {
			return s.mark(terminal, func(t *SubscriptionTarget) { t.Gone = true })
		})
		if !ok {
			fresh = sub
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
		if fresh, ok = store.Get(sub.ID); !ok || !fresh.Vanished() {
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

// DroppedNote is what the user reads about the subscriptions a deleted terminal
// took with it, empty when it took none. One source for the page's flash, the
// JSON answer and what the CLI prints.
func DroppedNote(dropped []Subscription) string {
	if len(dropped) == 0 {
		return ""
	}
	return fmt.Sprintf("%d subscription%s dropped", len(dropped), plural(len(dropped)))
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

// Publish matches one event against the subscriptions it may fire and takes
// it into each of them: at once when the subscription has no batch window,
// into the open window otherwise, where the tick fires it when the window
// closes. A job event is looked up in its owner's store alone.
func (r *Reactor) Publish(ev CockpitEvent) {
	if ev.Time.IsZero() {
		ev.Time = r.now().UTC()
	}
	ev.Body = truncateRunes(strings.TrimSpace(ev.Body), maxEventBodyRunes)
	owners := r.subs.Owners()
	if ev.Owner != "" {
		owners = []string{ev.Owner}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, owner := range owners {
		for _, sub := range r.subs.Of(owner).List() {
			if !sub.matches(ev) {
				continue
			}
			r.take(sub, ev)
		}
	}
}

// take puts one event into a subscription. Under the reactor's lock.
func (r *Reactor) take(sub Subscription, ev CockpitEvent) {
	now := r.now().UTC()
	fresh, ok := r.subs.Of(sub.Owner).Update(sub.ID, func(s *Subscription) bool {
		if !s.Open() {
			return false
		}
		if len(s.Pending) >= maxSubscriptionPendingNotes {
			// A window that fills up fires with what it holds; the rest is
			// one more turn's worth, which the cap decides about.
			return false
		}
		s.Pending = append(s.Pending, ev)
		s.mark(ev.Target, func(t *SubscriptionTarget) { t.Met = true })
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
	// fires here instead of on the next tick: a subscription without a batch
	// window, and a barrier whose last target arrived long after the window of
	// its first one closed. A barrier that still misses one is refused by fire.
	if !fresh.BatchDue.After(now) {
		r.fire(fresh.Owner, fresh.ID)
	}
}

// fire spends what a subscription's window holds: within the bounds, one
// reaction. Every bound is decided inside the write, and a turn refused by
// one is written on the subscription's line, where the page and
// `subscription-list` show it, never into the thread. Under the reactor's
// lock; the reaction itself runs off it.
//
// A barrier holds its window until every target it names arrived, and the
// arrivals are taken back with the events it spends, so a standing one waits
// for all of them again.
//
// One subscription reacts once at a time. While its reaction runs the events
// stay in the window, whatever the window says, and nothing is refused and
// nothing is dropped: the end of that reaction fires them, and the batch fold
// makes them one turn. Two reactions of one subscription would answer the
// same thread about the same thing twice, out of order, each without knowing
// of the other, and the cap per hour is a bound on what is bought, not an
// order. What holds the window open is cleared where the reaction ends
// (conclude, adopt), so a held window always has an end that reaches it, and
// the tick is its backstop.
func (r *Reactor) fire(owner, id string) {
	now := r.now().UTC()
	var events []CockpitEvent
	refused := ""
	sub, ok := r.subs.Of(owner).Update(id, func(s *Subscription) bool {
		if len(s.Pending) == 0 || s.Reacting() || s.Waiting() {
			return false
		}
		events = s.Pending
		s.Pending = nil
		s.clearMet()
		s.BatchDue = time.Time{}
		s.UpdatedAt = now
		s.Turns = withinHour(s.Turns, now)
		switch {
		case !s.Open():
			refused = "the subscription is " + string(s.State)
		case !s.ExpiresAt.IsZero() && now.After(s.ExpiresAt):
			s.State = SubscriptionExpired
			refused = "the subscription expired"
		case len(s.Turns) >= s.MaxPerHour:
			refused = fmt.Sprintf("the cap of %d turn%s per hour is spent", s.MaxPerHour, plural(s.MaxPerHour))
		default:
			s.Turns = append(s.Turns, now)
			s.Fired++
			s.LastFiredAt = now
			s.ReactingSince = now
			if s.Once {
				s.State = SubscriptionDone
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
		Source:       NoteEvent,
		Headline:     eventsHeadline(events),
		Subscription: sub.ID,
		Task:         sub.Task,
		Count:        len(events),
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
		r.conclude(RunRecord{Instance: owner, Origin: &origin}, wakeOutcome{}, err)
		return
	}
	outcome, err := r.service.awaitWake(run)
	r.conclude(run.rec, outcome, err)
}

// adopt takes over the reactions that outlived the server: each is on the
// machine already, so the slot is taken when it is free and skipped when it
// is not, and the answer is concluded exactly as it would have been without
// the restart. A subscription that says a reaction runs while no run is left
// is cleared, the reaction died with the process.
func (r *Reactor) adopt(runs []*activeRun) {
	live := map[string]bool{}
	for _, a := range runs {
		if a.rec.Origin != nil {
			live[a.rec.Origin.Subscription] = true
		}
		go func(a *activeRun) {
			held := r.service.slots.tryTake()
			outcome, err := r.service.awaitWake(a)
			r.conclude(a.rec, outcome, err)
			if held {
				r.service.slots.release()
			}
		}(a)
	}
	now := r.now().UTC()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, sub := range r.subs.All() {
		if !sub.Reacting() || live[sub.ID] {
			continue
		}
		r.subs.Of(sub.Owner).Update(sub.ID, func(s *Subscription) bool {
			s.ReactingSince = time.Time{}
			s.Note = "The reaction was lost in a restart."
			s.NoteAt = now
			return true
		})
		// A window this reaction held open outlived it on disk, and the run
		// it waited for is not coming back: it fires now.
		r.fire(sub.Owner, sub.ID)
	}
}

// conclude turns what a reaction came back with into what the thread and the
// subscription show. An answer is pushed into the owner's thread as one
// started without the user; NOTHING on the first line pushes nothing and
// notifies nobody; an end without an answer is written on the subscription's
// line, so a reaction never disappears without a trace. The reaction counted
// as fired when it fired, whatever it answered.
func (r *Reactor) conclude(rec RunRecord, outcome wakeOutcome, err error) {
	origin := rec.Origin
	if origin == nil {
		return
	}
	now := r.now().UTC()
	line := "Answered for " + origin.Headline + "."
	switch {
	case err != nil:
		log.Printf("assistant: reaction of %s: %v", rec.Instance, err)
		line = "The reaction for " + origin.Headline + " came back without an answer: " + oneLine(err.Error())
	case quietAnswer(outcome.Text):
		line = "Fired for " + origin.Headline + ", nothing to do."
	default:
		if _, pushErr := r.service.pushReaction(rec.Instance, rec.MessageID, *origin, outcome.Text); pushErr != nil {
			log.Printf("assistant: the answer of a reaction of %s could not be pushed: %v", rec.Instance, pushErr)
		}
	}
	r.subs.Of(rec.Instance).Update(origin.Subscription, func(s *Subscription) bool {
		s.ReactingSince = time.Time{}
		s.Note = truncateRunes(line, maxDoneWhenRunes)
		s.NoteAt = now
		s.UpdatedAt = now
		return true
	})
	r.service.changed()
	// What arrived while this reaction ran waited for it. Now that nothing of
	// this subscription runs, it is one window and one turn.
	r.mu.Lock()
	r.fire(rec.Instance, origin.Subscription)
	r.mu.Unlock()
}

// pushReaction writes a reaction's answer into the owner's thread: an
// assistant message marked as started without the user, carrying the event
// and the task it came from as its origin. It is announced on a frame of its
// own, so a page holds it while a chat answer streams and lands it after,
// and it is news like any answer. The id is the register's, so a reaction
// concluded twice, once before and once after a restart, pushes one message.
func (s *Service) pushReaction(owner, messageID string, origin Note, text string) (Message, error) {
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
		State:     StateComplete,
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
func reactionPrompt(origin Note, body string) string {
	var b strings.Builder
	b.WriteString("This turn was started by the cockpit, not by the user: an event you subscribed to fired. You run in a session of your own with your instruction file, the memory and your workspace files, and nothing of the conversation; your answer is pushed into your thread, where the user reads it as one started without them.\n\n")
	if origin.Count > 1 {
		fmt.Fprintf(&b, "%d events arrived inside one window, here they are:\n\n", origin.Count)
	} else {
		fmt.Fprintf(&b, "Event: %s\n\n", oneLine(origin.Headline))
	}
	if body = strings.TrimSpace(body); body != "" {
		b.WriteString(body + "\n\n")
	}
	if task := strings.TrimSpace(origin.Task); task != "" {
		fmt.Fprintf(&b, "Your task for this event: %s\n\n", task)
	}
	b.WriteString("If there is nothing to do or say, answer NOTHING on the first line and nothing else: then nothing is pushed and nobody is notified. Otherwise answer as you would answer the user, they read it.")
	return b.String()
}

// quietLine is NOTHING standing on the first line, the way a check spells its
// verdict: with or without markdown around it, with or without a colon.
var quietLine = regexp.MustCompile(`(?i)^[*_#>\s]*nothing[*_]*\s*[:.]?\s*$`)

// quietAnswer reports whether an answer's first line is NOTHING, the
// contract a reaction answers with when there is nothing to do or say.
func quietAnswer(text string) bool {
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		return quietLine.MatchString(line)
	}
	return false
}

// reactorInterval is how often the reactor looks at its subscriptions: a
// batch window that closed, a cron tick that is due, an expiry that passed.
// Ten seconds keeps a half minute window honest and costs a few file stats.
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

// Tick is one pass over every subscription: the cron ticks that are due
// become events, the batch windows that closed fire, and the standing
// subscriptions whose expiry passed end, which their state and their line
// say.
func (r *Reactor) Tick() {
	now := r.now().UTC()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, sub := range r.subs.All() {
		switch {
		case sub.Open() && !sub.ExpiresAt.IsZero() && now.After(sub.ExpiresAt):
			r.expire(sub, now)
		case sub.Open() && sub.Source == EventCron && !sub.NextAt.IsZero() && !now.Before(sub.NextAt):
			r.cronTick(sub, now)
		}
		if len(sub.Pending) > 0 && !sub.BatchDue.IsZero() && !now.Before(sub.BatchDue) {
			r.fire(sub.Owner, sub.ID)
		}
	}
}

// cronTick fires one due tick and moves the schedule on past now: a tick
// that was due while the cockpit was down is due once, not once per minute
// it missed.
func (r *Reactor) cronTick(sub Subscription, now time.Time) {
	due := sub.NextAt
	next, err := NextCron(sub.Spec, now)
	fresh, ok := r.subs.Of(sub.Owner).Update(sub.ID, func(s *Subscription) bool {
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
		Headline: "Schedule " + fresh.Spec + " ticked at " + due.In(time.Local).Format("15:04"),
	})
}

// expire ends a subscription whose time is over. Its state and its line say
// so on the page and in `subscription-list`; the thread hears nothing, an
// expiry is no event.
func (r *Reactor) expire(sub Subscription, now time.Time) {
	if _, ok := r.subs.Of(sub.Owner).Update(sub.ID, func(s *Subscription) bool {
		if !s.Open() {
			return false
		}
		s.State = SubscriptionExpired
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

// Recover picks up what a restart left behind. The subscriptions are on
// disk, so nothing has to be rebuilt; what has to be caught up is what
// happened while nobody was listening. A job that closed in that window is
// in jobs.json: every job subscription is walked against the closed jobs of
// its owner that ended after the watermark it last took, and those are
// published now. A cron tick that fell due and a batch window that closed
// are the tick's business, so one runs right away. A coder's signal from
// that window is in the notify inbox, which the inbox poller drains once it
// runs, and reaches here through it like any other. The reactions that were
// running are adopted by Service.Recover, which finds them in the register.
//
// It must run after Service.Recover, and after the local API is bound: a
// reaction it starts acts through it.
func (r *Reactor) Recover() {
	for _, sub := range r.subs.All() {
		if !sub.Open() || sub.Source != EventJob {
			continue
		}
		for _, job := range r.jobs.Of(sub.Owner).List() {
			if job.State.Open() || !job.UpdatedAt.After(sub.SeenAt) {
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
