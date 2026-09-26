package assistant

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/marein/dev-cockpit/internal/statefile"
)

// A trigger is how an assistant reacts to something instead of waiting to be
// asked: an event filter, a task, and bounds. It belongs to one assistant like
// a job does, its note and its turn land in that thread, and deleting the
// assistant drops it. The bounds are a batch window, an optional expiry and
// the one shot, and what ends a standing trigger is somebody removing it.

// TriggerState is where a trigger stands.
type TriggerState string

const (
	// TriggerStanding still fires.
	TriggerStanding TriggerState = "standing"
	// TriggerDone is a one shot that fired.
	TriggerDone TriggerState = "done"
	// TriggerExpired ran past its expiry.
	TriggerExpired TriggerState = "expired"
)

// The batch window a trigger starts on: half a minute, long enough that two
// jobs closing on one signal become one turn and short enough that a single
// event does not read as ignored. A schedule has none, a tick is never seconds
// from the next one and a window would only delay it.
const (
	DefaultTriggerBatch    = 30 * time.Second
	maxTriggerTaskRunes    = maxTaskRunes
	maxTriggersPerOwner    = 50
	maxTriggerPendingNotes = 20
)

// MaxTriggerNameRunes is how long a trigger's name may be. It is the 32 a
// coder's label is cut to (coder.TitleRunes) and it fits the row it stands
// in: what the head line leaves the name, after the icon in front of it and
// the fold control behind it, is 330px in the inline aside at its full 420px,
// 290px in the 380px one a 1280 window gives it, and 275px in the phone's
// sheet at 390px, which at the row's own 14px is over 40 runes of ordinary
// prose even on the narrowest screen that matters. It had 71px less while a
// state badge and a working icon still stood behind the name, and exactly 32
// fitted then; the room the icon gave back is slack and not a new cap, one
// number for a name wherever the cockpit shows one. A name past it is refused
// rather than cut, because a name a person typed is theirs; a label the
// cockpit picks for itself goes through FitTriggerName.
const MaxTriggerNameRunes = 32

// FitTriggerName answers a label the cockpit picked itself as a name a
// trigger may carry, empty where it is longer than one may be. The cockpit
// names the trigger it wires in `coder-new --then` after the coder that
// trigger waits for, and a session name past the cap must cost that sequel
// nothing: no name is what every trigger falls back to anyway, and the row
// then reads by the event. What a person types is refused instead, see
// applyTrigger: that name is theirs and cutting it would put words in their
// mouth.
func FitTriggerName(label string) string {
	label = strings.Join(strings.Fields(label), " ")
	if len([]rune(label)) > MaxTriggerNameRunes {
		return ""
	}
	return label
}

// TriggerTarget is one terminal a trigger waits for. The id is the key; the
// name is what that terminal was called when the trigger was made, for the row
// on the page, because a deleted terminal has no name left to look up.
type TriggerTarget struct {
	Terminal string `json:"terminal"`
	Name     string `json:"name,omitempty"`
	// Met says this target produced an event inside the open window, Gone that
	// its terminal was deleted. A barrier counts both as arrived: nothing can
	// ever come from a deleted terminal again, so waiting for it is waiting
	// for nothing.
	Met  bool `json:"met,omitempty"`
	Gone bool `json:"gone,omitempty"`
}

// Trigger is one standing arrangement.
type Trigger struct {
	ID string `json:"id"`
	// Owner is the assistant this trigger belongs to. Like a job's it is the
	// directory the entry was read from, never stored.
	Owner string `json:"-"`
	// Name is what the user calls this trigger, optional and short. With one
	// the row in the aside reads by it, and the event, the target or the
	// schedule moves to the line under it; without one that line is the
	// heading, which is what every trigger looked like before names existed.
	// Nothing is ever derived from the task: a trigger nobody named has none.
	Name string `json:"name,omitempty"`
	// Source and Kind say which events fire it, see the event kinds; Targets
	// narrow a job or coder source to those terminals, none is any of them (a
	// job source: any job of this assistant). Spec is a cron source's
	// schedule.
	Source  string          `json:"source"`
	Kind    string          `json:"kind"`
	Targets []TriggerTarget `json:"targets,omitempty"`
	// All makes several targets a barrier: the trigger fires once every one of
	// them produced an event, and the batch window folds those events into
	// that one turn. Without it any of them fires it, which is what one target
	// always did.
	All  bool   `json:"all,omitempty"`
	Spec string `json:"spec,omitempty"`
	// Timezone is the IANA name a schedule's wall clock times are read in,
	// "Europe/Berlin". Every cron trigger carries one, resolved when it is
	// written and never left to whatever the host happens to be set to: a
	// stored default that moves later must not move the schedules that already
	// stand, and nine o'clock has to keep meaning nine o'clock.
	Timezone string `json:"timezone,omitempty"`
	// Target and TargetName are the one target of a file written before
	// several were possible. They are read once and folded into Targets, and
	// the next write drops them. TODO(v2.0.0)
	Target     string `json:"target,omitempty"`
	TargetName string `json:"targetName,omitempty"`
	// Task is what the bought turn is asked to do.
	Task string `json:"task"`
	// Model is the model the reaction runs on, empty for the owner's own at
	// fire time, its Triggers pick at the ring, else its chat: that one is read
	// when the reaction starts (ModelFor) and never copied here, so a pick
	// that moves later moves the reactions with it.
	Model string `json:"model,omitempty"`
	// The bounds: a one shot ends after its first turn, ExpiresAt ends it by
	// the clock (zero is no expiry, which is what a trigger nobody bounded
	// carries), and BatchSeconds is how long it waits after an event for more
	// of them before it buys one turn for all of them.
	Once         bool      `json:"once,omitempty"`
	ExpiresAt    time.Time `json:"expiresAt,omitempty"`
	BatchSeconds int       `json:"batchSeconds"`

	State     TriggerState `json:"state"`
	CreatedAt time.Time    `json:"createdAt"`
	UpdatedAt time.Time    `json:"updatedAt"`
	// Fired counts the turns this trigger bought, LastFiredAt when the last
	// one was.
	Fired       int       `json:"fired,omitempty"`
	LastFiredAt time.Time `json:"lastFiredAt,omitempty"`
	// NextAt is a cron trigger's next tick, on disk so a restart does not lose
	// it: a tick that was due while the cockpit was down is due when it comes
	// back, once.
	NextAt time.Time `json:"nextAt,omitempty"`
	// SeenAt is the watermark of a job trigger: the newest job end it took. A
	// job that closed while the cockpit was down is in jobs.json, and the
	// startup pass fires it from there, see Reactor.Recover.
	SeenAt time.Time `json:"seenAt,omitempty"`
	// Pending are the events waiting in the open batch window, BatchDue when
	// that window closes. Both on disk, so a window survives a restart.
	Pending  []CockpitEvent `json:"pending,omitempty"`
	BatchDue time.Time      `json:"batchDue,omitempty"`
	// Note is the last thing that happened to the trigger, one line the page
	// shows: what it fired for, what refused a turn.
	Note   string    `json:"note,omitempty"`
	NoteAt time.Time `json:"noteAt,omitempty"`
	// Broke says the last reaction ended without arriving, the turn that broke
	// off and the one a restart took. It is what the row reads red for, next
	// to the note that says it in words, and the next reaction that comes back
	// whole clears it.
	Broke bool `json:"broke,omitempty"`
	// ReactingSince is set while the reaction it bought runs, so the page can
	// say so: nothing streams in the thread for it, the reaction runs in a
	// session of its own. A restart leaves it standing; the recovery clears
	// it where no run is left.
	ReactingSince time.Time `json:"reactingSince,omitempty"`
}

// Reacting reports whether a reaction of this trigger runs right now.
func (s Trigger) Reacting() bool { return !s.ReactingSince.IsZero() }

// Zone is the location a schedule's times are read in. Every cron trigger is
// written with a name that loads, so the fallback is for a file somebody
// edited by hand: reading such a schedule in the server's zone is what it did
// before zones existed, and answering nil would be a panic.
func (s Trigger) Zone() *time.Location {
	if loc, err := LoadZone(s.Timezone); err == nil {
		return loc
	}
	return time.Local
}

// Batch is the batch window as a duration.
func (s Trigger) Batch() time.Duration { return time.Duration(s.BatchSeconds) * time.Second }

// Open reports whether the trigger still fires.
func (s Trigger) Open() bool { return s.State == TriggerStanding }

// Terminals are the terminals this trigger names, none for any of them.
func (s Trigger) Terminals() []string {
	out := make([]string, 0, len(s.Targets))
	for _, t := range s.Targets {
		out = append(out, t.Terminal)
	}
	return out
}

// takesTerminals reports whether a trigger of that source waits for
// terminals, a job's or a coder's, and so whether any and all mean anything.
func takesTerminals(source string) bool {
	return source == EventJob || source == EventCoder
}

// holds reports whether this trigger names that terminal.
func (s Trigger) holds(terminal string) bool {
	return slices.ContainsFunc(s.Targets, func(t TriggerTarget) bool { return t.Terminal == terminal })
}

// Waiting reports whether a barrier still misses a target: it fires once every
// terminal it names produced an event, and a deleted one counts as arrived
// because nothing can come from it any more.
func (s Trigger) Waiting() bool {
	if !s.All {
		return false
	}
	for _, t := range s.Targets {
		if !t.Met && !t.Gone {
			return true
		}
	}
	return false
}

// Vanished reports whether every terminal this trigger names is deleted, so
// nothing can ever fire it again. One without targets is never vanished: a job
// of the assistant, any coder or a schedule still reaches it.
func (s Trigger) Vanished() bool {
	if len(s.Targets) == 0 {
		return false
	}
	for _, t := range s.Targets {
		if !t.Gone {
			return false
		}
	}
	return true
}

// mark sets a flag on one target and reports whether that changed anything.
func (s *Trigger) mark(terminal string, set func(*TriggerTarget)) bool {
	for i := range s.Targets {
		if s.Targets[i].Terminal != terminal {
			continue
		}
		before := s.Targets[i]
		set(&s.Targets[i])
		return s.Targets[i] != before
	}
	return false
}

// clearMet takes the arrivals of a spent window back, so a standing barrier
// waits for every target again. A gone target stays gone.
func (s *Trigger) clearMet() {
	for i := range s.Targets {
		s.Targets[i].Met = false
	}
}

// matches reports whether an event fires this trigger. A job event belongs to
// the assistant whose job it is, so only that assistant's triggers see it; a
// cron tick names the trigger it is for. A trigger on its source's umbrella
// kind takes every kind of that source.
func (s Trigger) matches(ev CockpitEvent) bool {
	if !s.Open() || s.Source != ev.Source {
		return false
	}
	if ev.Owner != "" && ev.Owner != s.Owner {
		return false
	}
	if s.Source == EventCron {
		return ev.Target == s.ID
	}
	if s.Kind != ev.Kind && s.Kind != umbrellaKind(s.Source) {
		return false
	}
	return len(s.Targets) == 0 || s.holds(ev.Target)
}

// umbrellaKind is the kind of a source that stands for every kind of it:
// closed for every way a job ends, news for every signal a coder sends,
// however it was classified. A source without one answers empty, which no
// stored kind ever equals. For a coder it is the only kind a trigger may name,
// so every coder trigger goes through this rule, and the event it is fired by
// still carries the kind it was read as.
func umbrellaKind(source string) string {
	switch source {
	case EventJob:
		return JobKindClosed
	case EventCoder:
		return CoderKindNews
	case EventCompose:
		return ComposeKindEnded
	}
	return ""
}

// triggersFileName is what the triggers of one instance are stored as, inside
// that instance's own directory, next to its jobs.
const triggersFileName = "triggers.json"

// TriggerStore persists the triggers of one assistant, one file, read through
// on every call like every other state file.
type TriggerStore struct {
	owner string
	path  string
	mu    sync.Mutex
}

// NewTriggerStore returns the store for one instance's directory.
func NewTriggerStore(owner, dir string) *TriggerStore {
	return &TriggerStore{owner: owner, path: filepath.Join(dir, triggersFileName)}
}

func (s *TriggerStore) load() []Trigger {
	var out []Trigger
	statefile.Load(s.path, &out)
	for i := range out {
		out[i].Owner = s.owner
		// The one target of a file written before several were possible. It is
		// read once into the list and dropped by the next write of the entry,
		// so a trigger made then keeps firing on its terminal. TODO(v2.0.0)
		if len(out[i].Targets) == 0 && out[i].Target != "" {
			out[i].Targets = []TriggerTarget{{Terminal: out[i].Target, Name: out[i].TargetName}}
		}
		out[i].Target, out[i].TargetName = "", ""
	}
	return out
}

func (s *TriggerStore) write(list []Trigger) {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		log.Printf("assistant: create the trigger directory of %s: %v", s.owner, err)
		return
	}
	statefile.Save(s.path, 0o600, list)
}

// List returns every trigger of this assistant, newest first.
func (s *TriggerStore) List() []Trigger {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.load()
	SortTriggers(out)
	return out
}

// Get returns one trigger by id.
func (s *TriggerStore) Get(id string) (Trigger, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, trigger := range s.load() {
		if trigger.ID == id {
			return trigger, true
		}
	}
	return Trigger{}, false
}

// Save writes one trigger, replacing the entry of the same id.
func (s *TriggerStore) Save(trigger Trigger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	trigger.Owner = s.owner
	list := s.load()
	replaced := false
	for i := range list {
		if list[i].ID == trigger.ID {
			list[i] = trigger
			replaced = true
			break
		}
	}
	if !replaced {
		list = append([]Trigger{trigger}, list...)
	}
	s.write(list)
}

// Update changes one trigger in place, under the same lock that reads it: the
// reactor's tick and a fired event write to the same entry, and neither may
// put back what the other wrote. change decides whether its change stands; the
// bool says whether the entry was written.
func (s *TriggerStore) Update(id string, change func(*Trigger) bool) (Trigger, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.load()
	for i := range list {
		if list[i].ID != id {
			continue
		}
		if !change(&list[i]) {
			return list[i], false
		}
		s.write(list)
		return list[i], true
	}
	return Trigger{}, false
}

// Delete removes one trigger. Reports whether there was one.
func (s *TriggerStore) Delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.load()
	kept := list[:0]
	for _, trigger := range list {
		if trigger.ID != id {
			kept = append(kept, trigger)
		}
	}
	if len(kept) == len(list) {
		return false
	}
	s.write(kept)
	return true
}

// Triggers is every assistant's triggers at once, one store per instance, the
// way Jobs holds the jobs. It reads the index for the instances that exist, so
// a deleted instance takes its triggers out of every answer the moment its
// entry is gone.
type Triggers struct {
	store *Store

	mu     sync.Mutex
	stores map[string]*TriggerStore
}

// NewTriggers wires the registry over the index of instances.
func NewTriggers(store *Store) *Triggers {
	return &Triggers{store: store, stores: map[string]*TriggerStore{}}
}

// Of is the store of one assistant. The owner becomes a path component, so it
// has to be an id that is one, see Jobs.Of.
func (r *Triggers) Of(owner string) *TriggerStore {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.stores[owner]; ok {
		return s
	}
	s := NewTriggerStore(owner, r.store.InstanceDir(owner))
	r.stores[owner] = s
	return s
}

// Forget drops the store of an assistant that is gone; the file went with
// the instance directory.
func (r *Triggers) Forget(owner string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.stores, owner)
}

// Owners are the assistants that exist right now.
func (r *Triggers) Owners() []string {
	entries := r.store.List()
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.ID)
	}
	return out
}

// All returns every assistant's triggers, each carrying its owner.
func (r *Triggers) All() []Trigger {
	var out []Trigger
	for _, owner := range r.Owners() {
		out = append(out, r.Of(owner).List()...)
	}
	return out
}

// Find is one trigger by id, whoever owns it.
func (r *Triggers) Find(id string) (Trigger, bool) {
	for _, owner := range r.Owners() {
		if trigger, ok := r.Of(owner).Get(id); ok {
			return trigger, true
		}
	}
	return Trigger{}, false
}

// SortTriggers puts the ones that still fire first, then the newest.
func SortTriggers(list []Trigger) {
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Open() != list[j].Open() {
			return list[i].Open()
		}
		return list[i].CreatedAt.After(list[j].CreatedAt)
	})
}

// The event sources, and the kinds each one has. A trigger names one source
// and one kind of it, and two of those kinds are umbrellas, see umbrellaKind:
// JobKindClosed stands for every way a job ends, CoderKindNews for every
// signal a coder sends.
//
// CoderKindEnded and CoderKindAsks are the two readings of a coder's signal,
// and they stay exactly that: an event carries the one it was read as, its
// headline says which, and a reaction therefore still reads what happened.
// What no trigger may name is one of them alone, see EventOptions.
//
// The umbrella over them is called news and not signal, although a signal is
// what notify passes around: signal is the installation's word for the
// mechanism, and it earned its place while it meant "whichever of the two this
// was". With nothing under it left to choose from it no longer says that,
// while news is what the cockpit already calls this very event where the user
// reads it, "Coder has news". One thing, one word, in both places.
//
// A compose run an assistant started ends in one of three ways, and ended is
// the umbrella over them the way closed is over a job's: done is a command
// that went through, failed one that started and did not, whether it exited
// non zero, timed out or was cancelled while it ran, or one approved that
// could not start, and declined one that never started: denied by the user,
// cancelled while it waited, unanswered past the bound, lost in a restart, or
// ended with its project. The event's target is the run, so a terminal is no
// filter on it.
const (
	EventJob     = "job"
	EventCoder   = "coder"
	EventCron    = "cron"
	EventCompose = "compose"

	JobKindClosed       = "closed"
	CoderKindEnded      = "ended"
	CoderKindAsks       = "asks"
	CoderKindNews       = "news"
	CronKindTick        = "tick"
	ComposeKindDone     = "done"
	ComposeKindFailed   = "failed"
	ComposeKindDeclined = "declined"
	ComposeKindEnded    = "ended"
)

// EventOptions are the kinds a trigger may name, per source, in the order the
// surfaces list them. Every kind here is one the page and the CLI both offer,
// nothing is page only.
//
// A coder has one entry, the umbrella, and the two narrow kinds are
// deliberately not on this list. Which of the two a signal was is read off the
// coder's own hook name, and that reading does not hold everywhere: copilot
// has no hook path at all, only the terminal bell, and a bell is read as ended
// whatever it rang for. A trigger on ended alone would be a promise the
// cockpit cannot keep for every coder, so while that is so, one that fires on
// every signal is the honest answer. The classification is not dropped with
// the choice, it still decides the kind an event carries and the headline it
// is read by; it only stops deciding what a trigger may wait for. The way back
// to a choice is a bell that is classified from the coder's own record instead
// of from a hook name it never had.
var EventOptions = []EventOption{
	{Source: EventJob, Kind: string(JobDone), Label: "Job done", Help: "a job of yours closed done"},
	{Source: EventJob, Kind: string(JobBlocked), Label: "Job blocked", Help: "a job of yours closed blocked"},
	{Source: EventJob, Kind: string(JobExpired), Label: "Job expired", Help: "a job of yours ran out of checks or time"},
	{Source: EventJob, Kind: JobKindClosed, Label: "Job closed", Help: "a job of yours ended, done, blocked or expired"},
	{Source: EventCoder, Kind: CoderKindNews, Label: "Coder has news", Help: "a coder's turn ended or it asks, every signal either way"},
	{Source: EventCompose, Kind: ComposeKindDone, Label: "Compose done", Help: "a compose command you started went through"},
	{Source: EventCompose, Kind: ComposeKindFailed, Label: "Compose failed", Help: "a compose command you started ran and failed, timed out or was cancelled, or could not start once approved"},
	{Source: EventCompose, Kind: ComposeKindDeclined, Label: "Compose declined", Help: "a compose command you started never ran: denied, cancelled while waiting, unanswered, or lost in a restart"},
	{Source: EventCompose, Kind: ComposeKindEnded, Label: "Compose ended", Help: "a compose command you started ended, done, failed or declined"},
	{Source: EventCron, Kind: CronKindTick, Label: "Schedule", Help: "a cron schedule ticks"},
}

// EventOption is one entry of that list, with what a surface calls it.
type EventOption struct {
	Source string
	Kind   string
	Label  string
	Help   string
}

// Name is the one word the CLI and the page pass for a kind: source and kind
// joined by a dash, "job-done", and the bare "cron" for the schedule.
func (k EventOption) Name() string {
	if k.Source == EventCron {
		return EventCron
	}
	return k.Source + "-" + k.Kind
}

// ParseEventOption reads the word back. It takes the joined form and, for the
// page's selects, the source and kind apart.
func ParseEventOption(name string) (EventOption, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, k := range EventOptions {
		if k.Name() == name {
			return k, nil
		}
	}
	names := make([]string, 0, len(EventOptions))
	for _, k := range EventOptions {
		names = append(names, k.Name())
	}
	return EventOption{}, fmt.Errorf("No event is called %q. The events are %s.", name, strings.Join(names, ", "))
}

// EventLabel is what a surface calls a source and kind.
func EventLabel(source, kind string) string {
	for _, k := range EventOptions {
		if k.Source == source && k.Kind == kind {
			return k.Label
		}
	}
	return source + " " + kind
}

// TriggerSpec is what a caller asks for when it makes a trigger or changes
// one. A field left at zero is one the caller did not name: on a create it
// takes the default, on an edit it leaves what stands. Three of them have no
// zero that could say that, so they carry a companion that does; Until is the
// expiry as a span from now, zero for a caller that named none and Never for
// no expiry at all, which is what takes one off a trigger that carries it.
type TriggerSpec struct {
	Owner string
	Event string
	// Name is the optional heading. Empty is a name too, it clears one, so it
	// carries its own companion instead of reading an empty string as "not
	// named": the page posts the field on every save.
	Name    string
	NameSet bool
	// Targets are the terminals it waits for, each with the name the caller
	// knows it by; none is any of them. All makes them a barrier.
	Targets    []TriggerTarget
	TargetsSet bool
	All        bool
	AllSet     bool
	Spec       string
	// Timezone is the IANA name a schedule is read in. Empty is a caller that
	// named none, which on a create is filled in by the surface before it gets
	// here (what was asked for, else the stored default, else the server's
	// zone) and on an edit leaves the zone that stands.
	Timezone string
	Task     string
	// Model is the reaction's own model, with the companion that says whether
	// the request carried the field: an empty one clears it back to the
	// owner's chat model, a request without it leaves what stands.
	Model    string
	ModelSet bool
	Once     bool
	OnceSet  bool
	Until    time.Duration
	Never    bool
	Batch    time.Duration
	BatchSet bool
}

// newTrigger turns a spec into the entry that is stored, or refuses it. The
// entry starts at the defaults and the spec is applied onto it, so a field
// nobody named is the default and one nobody named on an edit is what stands:
// one function writes a trigger, whichever way in the caller took.
func newTrigger(spec TriggerSpec, now time.Time) (Trigger, error) {
	if !ValidID(spec.Owner) {
		return Trigger{}, errors.New("A trigger needs the assistant it belongs to.")
	}
	kind, err := ParseEventOption(spec.Event)
	if err != nil {
		return Trigger{}, err
	}
	trigger := Trigger{
		ID:           statefile.NewID(),
		Owner:        spec.Owner,
		Source:       kind.Source,
		Kind:         kind.Kind,
		BatchSeconds: int(DefaultTriggerBatch / time.Second),
		State:        TriggerStanding,
		CreatedAt:    now,
		UpdatedAt:    now,
		SeenAt:       now,
	}
	// A create names every field there is: what it left empty is the default
	// the entry was seeded with, which is the same answer an unnamed field
	// gets on an edit.
	spec.TargetsSet, spec.AllSet, spec.OnceSet = true, true, true
	if err := applyTrigger(&trigger, spec, now); err != nil {
		return Trigger{}, err
	}
	return trigger, nil
}

// editTrigger changes a trigger that still fires: what the spec names moves,
// everything else stands. Two things it never touches, the record of what
// happened (the count, when it was made, the events waiting in the window, a
// reaction that runs right now) and the event: another event is another
// trigger, and there are new and delete for that.
func editTrigger(trigger Trigger, spec TriggerSpec, now time.Time) (Trigger, error) {
	if !trigger.Open() {
		return Trigger{}, fmt.Errorf("That trigger is %s and cannot be changed. Make a new one.", trigger.State)
	}
	if strings.TrimSpace(spec.Event) != "" {
		kind, err := ParseEventOption(spec.Event)
		if err != nil {
			return Trigger{}, err
		}
		if kind.Source != trigger.Source || kind.Kind != trigger.Kind {
			return Trigger{}, fmt.Errorf("A trigger cannot change its event, it reacts to %s. Make one for %s and remove this.", EventLabel(trigger.Source, trigger.Kind), kind.Label)
		}
	}
	if err := applyTrigger(&trigger, spec, now); err != nil {
		return Trigger{}, err
	}
	return trigger, nil
}

// applyTrigger writes what a spec names onto a trigger and checks the result.
// It is the one validation both ways in share: a create applies a spec onto an
// entry seeded with the defaults, an edit onto the one that stands, and what
// the spec does not name is left exactly as it is.
func applyTrigger(trigger *Trigger, spec TriggerSpec, now time.Time) error {
	if spec.NameSet {
		// A heading is one line, so the whitespace of a pasted one is
		// collapsed the way every label of this app is read.
		trigger.Name = strings.Join(strings.Fields(spec.Name), " ")
	}
	if task := strings.TrimSpace(spec.Task); task != "" {
		trigger.Task = task
	}
	if spec.ModelSet {
		model, err := CleanModel(spec.Model)
		if err != nil {
			return err
		}
		trigger.Model = model
	}
	if spec.TargetsSet {
		trigger.Targets = mergedTargets(*trigger, spec.Targets)
	}
	if spec.AllSet {
		trigger.All = spec.All
	}
	if !takesTerminals(trigger.Source) {
		// Any or all is a question about terminals, and a schedule and a
		// compose run have none: a mode posted for one is no barrier.
		trigger.All = false
	}
	if spec.OnceSet {
		trigger.Once = spec.Once
	}
	if spec.BatchSet {
		if spec.Batch < 0 {
			return errors.New("A batch window cannot be negative.")
		}
		trigger.BatchSeconds = int(spec.Batch / time.Second)
	}
	switch {
	case spec.Never:
		trigger.ExpiresAt = time.Time{}
	case spec.Until < 0:
		return errors.New("An expiry looks backwards. Give a span from now, like 8h.")
	case spec.Until > 0:
		trigger.ExpiresAt = now.Add(spec.Until)
	}
	// A schedule that moved is a schedule whose next tick has to be worked out
	// again; one nobody touched keeps the tick it is waiting for. A zone moves
	// it the same way: the same five fields in another zone are other minutes.
	moved := false
	if fields := strings.Join(strings.Fields(spec.Spec), " "); fields != "" {
		moved = fields != trigger.Spec
		trigger.Spec = fields
	}
	if zone := strings.TrimSpace(spec.Timezone); zone != "" {
		if _, err := LoadZone(zone); err != nil {
			return err
		}
		moved = moved || zone != trigger.Timezone
		trigger.Timezone = zone
	}

	if len([]rune(trigger.Name)) > MaxTriggerNameRunes {
		return fmt.Errorf("That name is too long for the row it stands in: at most %d runes.", MaxTriggerNameRunes)
	}
	if strings.TrimSpace(trigger.Task) == "" {
		return errors.New("A trigger needs a task: what to do when the event fires.")
	}
	if len([]rune(trigger.Task)) > maxTriggerTaskRunes {
		return fmt.Errorf("That task is too long to store whole: at most %d runes.", maxTriggerTaskRunes)
	}
	if trigger.All && len(trigger.Targets) == 0 {
		return errors.New("A barrier needs the terminals it waits for: name them, or drop all and it fires on any event.")
	}
	if len(trigger.Targets) < 2 {
		// One target arrives on its own event either way, so a barrier of one
		// would be the ordinary trigger under a name that reads like it waits
		// for something else.
		trigger.All = false
	}
	switch trigger.Source {
	case EventCron:
		if trigger.Spec == "" {
			return errors.New("A schedule needs its five cron fields, like \"*/30 9-17 * * 1-5\".")
		}
		if trigger.Timezone == "" {
			return errors.New("A schedule needs the zone its times are read in, an IANA name like \"Europe/Berlin\".")
		}
		// A schedule has no batch window: a tick is never seconds from the
		// next one, so a window named for one is dropped rather than stored,
		// and the surfaces say so where somebody could have named it.
		trigger.Targets, trigger.All, trigger.BatchSeconds = nil, false, 0
		if moved || trigger.NextAt.IsZero() {
			next, err := NextCron(trigger.Spec, now, trigger.Zone())
			if err != nil {
				return err
			}
			trigger.NextAt = next
		}
	case EventCompose:
		if trigger.Spec != "" {
			return errors.New("Only a schedule takes cron fields.")
		}
		if trigger.Timezone != "" {
			return errors.New("Only a schedule takes a zone: the other events happen when they happen.")
		}
		if len(trigger.Targets) > 0 {
			// The event is about a run and no terminal; a terminal named on
			// it would filter every event out and the trigger would never
			// fire, which is worse than a refusal.
			return errors.New("A compose event is about no terminal, leave --terminal out: it fires on every compose command you start.")
		}
	default:
		if trigger.Spec != "" {
			return errors.New("Only a schedule takes cron fields.")
		}
		if trigger.Timezone != "" {
			return errors.New("Only a schedule takes a zone: the other events happen when they happen.")
		}
	}
	trigger.UpdatedAt = now
	return nil
}

// mergedTargets is the target list a spec names, read against the one that
// stands: a target that stays keeps what it reached, an arrival stays an
// arrival and a deleted terminal stays deleted, one that is named for the
// first time starts fresh, and one that is left out takes its state with it.
func mergedTargets(trigger Trigger, wanted []TriggerTarget) []TriggerTarget {
	var out []TriggerTarget
	for _, t := range wanted {
		terminal := strings.TrimSpace(t.Terminal)
		if terminal == "" || slices.ContainsFunc(out, func(o TriggerTarget) bool { return o.Terminal == terminal }) {
			continue
		}
		next := TriggerTarget{Terminal: terminal, Name: strings.TrimSpace(t.Name)}
		for _, stood := range trigger.Targets {
			if stood.Terminal != terminal {
				continue
			}
			next.Met, next.Gone = stood.Met, stood.Gone
			if next.Name == "" {
				next.Name = stood.Name
			}
			break
		}
		out = append(out, next)
	}
	return out
}

// triggerChanges names what an edit moved, in the order the form asks for the
// fields, empty when nothing moved. Reactor.Edit answers it, so the line
// `trigger-edit` prints and the toast the page shows are the one wording.
func triggerChanges(before, after Trigger) string {
	var out []string
	if before.Name != after.Name {
		if after.Name == "" {
			out = append(out, "no name")
		} else {
			out = append(out, "name "+after.Name)
		}
	}
	if before.Task != after.Task {
		out = append(out, "task")
	}
	if before.Model != after.Model {
		if after.Model == "" {
			out = append(out, "the assistant default")
		} else {
			out = append(out, "model "+after.Model)
		}
	}
	if !slices.Equal(before.Terminals(), after.Terminals()) {
		if len(after.Targets) == 0 {
			out = append(out, "no terminal")
		} else {
			out = append(out, fmt.Sprintf("%d terminal%s", len(after.Targets), plural(len(after.Targets))))
		}
	}
	if before.All != after.All {
		if after.All {
			out = append(out, "all of them")
		} else {
			out = append(out, "any of them")
		}
	}
	if before.Spec != after.Spec {
		out = append(out, "schedule "+after.Spec)
	}
	if before.Timezone != after.Timezone {
		out = append(out, "zone "+after.Timezone)
	}
	if before.Once != after.Once {
		if after.Once {
			out = append(out, "once")
		} else {
			out = append(out, "standing")
		}
	}
	if !before.ExpiresAt.Equal(after.ExpiresAt) {
		if after.ExpiresAt.IsZero() {
			out = append(out, "no expiry")
		} else {
			out = append(out, "until "+after.ExpiresAt.In(time.Local).Format("2006-01-02 15:04"))
		}
	}
	if before.BatchSeconds != after.BatchSeconds {
		out = append(out, fmt.Sprintf("batch %ds", after.BatchSeconds))
	}
	return strings.Join(out, ", ")
}
