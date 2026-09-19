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

// A subscription is how an assistant reacts to something instead of waiting
// to be asked: an event filter, a task, and bounds. It belongs to one
// assistant like a job does, its note and its turn land in that thread, and
// deleting the assistant drops it. Without bounds an assistant would wake
// itself every five minutes at night, so every subscription carries an
// expiry, a cap on the turns it may buy per hour and a batch window, and
// may be a one shot.

// SubscriptionState is where a subscription stands.
type SubscriptionState string

const (
	// SubscriptionStanding still fires.
	SubscriptionStanding SubscriptionState = "standing"
	// SubscriptionDone is a one shot that fired.
	SubscriptionDone SubscriptionState = "done"
	// SubscriptionExpired ran past its expiry.
	SubscriptionExpired SubscriptionState = "expired"
)

// The defaults of the bounds. Eight hours and six turns an hour: a standing
// arrangement outlives a working day only when somebody said so, and a noisy
// source costs at most a turn every ten minutes. The batch window is half a
// minute, long enough that two jobs closing on one signal become one turn
// and short enough that a single event does not read as ignored.
const (
	DefaultSubscriptionTTL      = 8 * time.Hour
	DefaultSubscriptionPerHour  = 6
	DefaultSubscriptionBatch    = 30 * time.Second
	maxSubscriptionTaskRunes    = maxTaskRunes
	maxSubscriptionsPerOwner    = 50
	maxSubscriptionPendingNotes = 20
)

// SubscriptionTarget is one terminal a subscription waits for. The id is the
// key; the name is what that terminal was called when the subscription was
// made, for the row on the page, because a deleted terminal has no name left
// to look up.
type SubscriptionTarget struct {
	Terminal string `json:"terminal"`
	Name     string `json:"name,omitempty"`
	// Met says this target produced an event inside the open window, Gone that
	// its terminal was deleted. A barrier counts both as arrived: nothing can
	// ever come from a deleted terminal again, so waiting for it is waiting
	// for nothing.
	Met  bool `json:"met,omitempty"`
	Gone bool `json:"gone,omitempty"`
}

// Subscription is one standing arrangement.
type Subscription struct {
	ID string `json:"id"`
	// Owner is the assistant this subscription belongs to. Like a job's it is
	// the directory the entry was read from, never stored.
	Owner string `json:"-"`
	// Source and Kind say which events fire it, see the event kinds; Targets
	// narrow a job or coder source to those terminals, none is any of them (a
	// job source: any job of this assistant). Spec is a cron source's
	// schedule.
	Source  string               `json:"source"`
	Kind    string               `json:"kind"`
	Targets []SubscriptionTarget `json:"targets,omitempty"`
	// All makes several targets a barrier: the subscription fires once every
	// one of them produced an event, and the batch window folds those events
	// into that one turn. Without it any of them fires it, which is what one
	// target always did.
	All  bool   `json:"all,omitempty"`
	Spec string `json:"spec,omitempty"`
	// Target and TargetName are the one target of a file written before
	// several were possible. They are read once and folded into Targets, and
	// the next write drops them. TODO(v2.0.0)
	Target     string `json:"target,omitempty"`
	TargetName string `json:"targetName,omitempty"`
	// Task is what the bought turn is asked to do.
	Task string `json:"task"`
	// The bounds: a one shot ends after its first turn, ExpiresAt ends it by
	// the clock (zero is never), MaxPerHour caps the turns it buys, and
	// BatchSeconds is how long it waits after an event for more of them
	// before it buys one turn for all of them.
	Once         bool      `json:"once,omitempty"`
	ExpiresAt    time.Time `json:"expiresAt,omitempty"`
	MaxPerHour   int       `json:"maxPerHour"`
	BatchSeconds int       `json:"batchSeconds"`

	State     SubscriptionState `json:"state"`
	CreatedAt time.Time         `json:"createdAt"`
	UpdatedAt time.Time         `json:"updatedAt"`
	// Fired counts the turns this subscription bought, LastFiredAt when the
	// last one was.
	Fired       int       `json:"fired,omitempty"`
	LastFiredAt time.Time `json:"lastFiredAt,omitempty"`
	// NextAt is a cron subscription's next tick, on disk so a restart does
	// not lose it: a tick that was due while the cockpit was down is due
	// when it comes back, once.
	NextAt time.Time `json:"nextAt,omitempty"`
	// SeenAt is the watermark of a job subscription: the newest job end it
	// took. A job that closed while the cockpit was down is in jobs.json,
	// and the startup pass fires it from there, see Reactor.Recover.
	SeenAt time.Time `json:"seenAt,omitempty"`
	// Turns are the turns bought within the last hour, what the cap counts.
	Turns []time.Time `json:"turns,omitempty"`
	// Pending are the events waiting in the open batch window, BatchDue when
	// that window closes. Both on disk, so a window survives a restart.
	Pending  []CockpitEvent `json:"pending,omitempty"`
	BatchDue time.Time      `json:"batchDue,omitempty"`
	// Note is the last thing that happened to the subscription, one line the
	// page shows: what it fired for, what refused a turn.
	Note   string    `json:"note,omitempty"`
	NoteAt time.Time `json:"noteAt,omitempty"`
	// ReactingSince is set while the reaction it bought runs, so the page can
	// say so: nothing streams in the thread for it, the reaction runs in a
	// session of its own. A restart leaves it standing; the recovery clears
	// it where no run is left.
	ReactingSince time.Time `json:"reactingSince,omitempty"`
}

// Reacting reports whether a reaction of this subscription runs right now.
func (s Subscription) Reacting() bool { return !s.ReactingSince.IsZero() }

// Batch is the batch window as a duration.
func (s Subscription) Batch() time.Duration { return time.Duration(s.BatchSeconds) * time.Second }

// Open reports whether the subscription still fires.
func (s Subscription) Open() bool { return s.State == SubscriptionStanding }

// Terminals are the terminals this subscription names, none for any of them.
func (s Subscription) Terminals() []string {
	out := make([]string, 0, len(s.Targets))
	for _, t := range s.Targets {
		out = append(out, t.Terminal)
	}
	return out
}

// holds reports whether this subscription names that terminal.
func (s Subscription) holds(terminal string) bool {
	return slices.ContainsFunc(s.Targets, func(t SubscriptionTarget) bool { return t.Terminal == terminal })
}

// Waiting reports whether a barrier still misses a target: it fires once every
// terminal it names produced an event, and a deleted one counts as arrived
// because nothing can come from it any more.
func (s Subscription) Waiting() bool {
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

// Vanished reports whether every terminal this subscription names is deleted,
// so nothing can ever fire it again. One without targets is never vanished: a
// job of the assistant, any coder or a schedule still reaches it.
func (s Subscription) Vanished() bool {
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
func (s *Subscription) mark(terminal string, set func(*SubscriptionTarget)) bool {
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
func (s *Subscription) clearMet() {
	for i := range s.Targets {
		s.Targets[i].Met = false
	}
}

// matches reports whether an event fires this subscription. A job event
// belongs to the assistant whose job it is, so only that assistant's
// subscriptions see it; a cron tick names the subscription it is for.
func (s Subscription) matches(ev CockpitEvent) bool {
	if !s.Open() || s.Source != ev.Source {
		return false
	}
	if ev.Owner != "" && ev.Owner != s.Owner {
		return false
	}
	if s.Source == EventCron {
		return ev.Target == s.ID
	}
	if s.Kind != ev.Kind && !(s.Source == EventJob && s.Kind == JobKindClosed) {
		return false
	}
	return len(s.Targets) == 0 || s.holds(ev.Target)
}

// subscriptionsFileName is what the subscriptions of one instance are stored
// as, inside that instance's own directory, next to its jobs.
const subscriptionsFileName = "subscriptions.json"

// SubscriptionStore persists the subscriptions of one assistant, one file,
// read through on every call like every other state file.
type SubscriptionStore struct {
	owner string
	path  string
	mu    sync.Mutex
}

// NewSubscriptionStore returns the store for one instance's directory.
func NewSubscriptionStore(owner, dir string) *SubscriptionStore {
	return &SubscriptionStore{owner: owner, path: filepath.Join(dir, subscriptionsFileName)}
}

func (s *SubscriptionStore) load() []Subscription {
	var out []Subscription
	statefile.Load(s.path, &out)
	for i := range out {
		out[i].Owner = s.owner
		// The one target of a file written before several were possible. It is
		// read once into the list and dropped by the next write of the entry,
		// so a subscription made then keeps firing on its terminal.
		// TODO(v2.0.0)
		if len(out[i].Targets) == 0 && out[i].Target != "" {
			out[i].Targets = []SubscriptionTarget{{Terminal: out[i].Target, Name: out[i].TargetName}}
		}
		out[i].Target, out[i].TargetName = "", ""
	}
	return out
}

func (s *SubscriptionStore) write(list []Subscription) {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		log.Printf("assistant: create the subscription directory of %s: %v", s.owner, err)
		return
	}
	statefile.Save(s.path, 0o600, list)
}

// List returns every subscription of this assistant, newest first.
func (s *SubscriptionStore) List() []Subscription {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.load()
	SortSubscriptions(out)
	return out
}

// Get returns one subscription by id.
func (s *SubscriptionStore) Get(id string) (Subscription, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sub := range s.load() {
		if sub.ID == id {
			return sub, true
		}
	}
	return Subscription{}, false
}

// Save writes one subscription, replacing the entry of the same id.
func (s *SubscriptionStore) Save(sub Subscription) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub.Owner = s.owner
	list := s.load()
	replaced := false
	for i := range list {
		if list[i].ID == sub.ID {
			list[i] = sub
			replaced = true
			break
		}
	}
	if !replaced {
		list = append([]Subscription{sub}, list...)
	}
	s.write(list)
}

// Update changes one subscription in place, under the same lock that reads
// it: the reactor's tick and a fired event write to the same entry, and
// neither may put back what the other wrote. change decides whether its
// change stands; the bool says whether the entry was written.
func (s *SubscriptionStore) Update(id string, change func(*Subscription) bool) (Subscription, bool) {
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
	return Subscription{}, false
}

// Delete removes one subscription. Reports whether there was one.
func (s *SubscriptionStore) Delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.load()
	kept := list[:0]
	for _, sub := range list {
		if sub.ID != id {
			kept = append(kept, sub)
		}
	}
	if len(kept) == len(list) {
		return false
	}
	s.write(kept)
	return true
}

// Subscriptions is every assistant's subscriptions at once, one store per
// instance, the way Jobs holds the jobs. It reads the index for the instances
// that exist, so a deleted instance takes its subscriptions out of every
// answer the moment its entry is gone.
type Subscriptions struct {
	store *Store

	mu     sync.Mutex
	stores map[string]*SubscriptionStore
}

// NewSubscriptions wires the registry over the index of instances.
func NewSubscriptions(store *Store) *Subscriptions {
	return &Subscriptions{store: store, stores: map[string]*SubscriptionStore{}}
}

// Of is the store of one assistant. The owner becomes a path component, so it
// has to be an id that is one, see Jobs.Of.
func (r *Subscriptions) Of(owner string) *SubscriptionStore {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.stores[owner]; ok {
		return s
	}
	s := NewSubscriptionStore(owner, r.store.InstanceDir(owner))
	r.stores[owner] = s
	return s
}

// Forget drops the store of an assistant that is gone; the file went with
// the instance directory.
func (r *Subscriptions) Forget(owner string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.stores, owner)
}

// Owners are the assistants that exist right now.
func (r *Subscriptions) Owners() []string {
	entries := r.store.List()
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.ID)
	}
	return out
}

// All returns every assistant's subscriptions, each carrying its owner.
func (r *Subscriptions) All() []Subscription {
	var out []Subscription
	for _, owner := range r.Owners() {
		out = append(out, r.Of(owner).List()...)
	}
	return out
}

// Find is one subscription by id, whoever owns it.
func (r *Subscriptions) Find(id string) (Subscription, bool) {
	for _, owner := range r.Owners() {
		if sub, ok := r.Of(owner).Get(id); ok {
			return sub, true
		}
	}
	return Subscription{}, false
}

// SortSubscriptions puts the ones that still fire first, then the newest.
func SortSubscriptions(list []Subscription) {
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Open() != list[j].Open() {
			return list[i].Open()
		}
		return list[i].CreatedAt.After(list[j].CreatedAt)
	})
}

// The event sources, and the kinds each one has. A subscription names one
// source and one kind of it; JobKindClosed stands for every way a job ends.
const (
	EventJob   = "job"
	EventCoder = "coder"
	EventCron  = "cron"

	JobKindClosed  = "closed"
	CoderKindEnded = "ended"
	CoderKindAsks  = "asks"
	CronKindTick   = "tick"
)

// EventOptions are the kinds a subscription may name, per source, in the order
// the surfaces list them. Every kind here is one the page and the CLI both
// offer, nothing is page only.
var EventOptions = []EventOption{
	{Source: EventJob, Kind: string(JobDone), Label: "Job done", Help: "a job of yours closed done"},
	{Source: EventJob, Kind: string(JobBlocked), Label: "Job blocked", Help: "a job of yours closed blocked"},
	{Source: EventJob, Kind: string(JobExpired), Label: "Job expired", Help: "a job of yours ran out of checks or time"},
	{Source: EventJob, Kind: JobKindClosed, Label: "Job closed", Help: "a job of yours ended, done, blocked or expired"},
	{Source: EventCoder, Kind: CoderKindEnded, Label: "Coder ended its turn", Help: "a coder's turn ended"},
	{Source: EventCoder, Kind: CoderKindAsks, Label: "Coder asks", Help: "a coder asks a question or wants a permission"},
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

// SubscriptionSpec is what a caller asks for when it subscribes or changes
// one. A field left at zero is one the caller did not name: on a create it
// takes the default, on an edit it leaves what stands. Three of them have no
// zero that could say that, so they carry a companion that does; Until is the
// expiry as a span from now, zero for the default and Never for none.
type SubscriptionSpec struct {
	Owner string
	Event string
	// Targets are the terminals it waits for, each with the name the caller
	// knows it by; none is any of them. All makes them a barrier.
	Targets    []SubscriptionTarget
	TargetsSet bool
	All        bool
	AllSet     bool
	Spec       string
	Task       string
	Once       bool
	OnceSet    bool
	Until      time.Duration
	Never      bool
	MaxPerHour int
	Batch      time.Duration
	BatchSet   bool
}

// defaultBatchSeconds is the window a new subscription of a source starts
// with. A tick is never seconds from the next one, so a schedule has none: a
// window would only delay it.
func defaultBatchSeconds(source string) int {
	if source == EventCron {
		return 0
	}
	return int(DefaultSubscriptionBatch / time.Second)
}

// newSubscription turns a spec into the entry that is stored, or refuses it.
// The entry starts at the defaults and the spec is applied onto it, so a field
// nobody named is the default and one nobody named on an edit is what stands:
// one function writes a subscription, whichever way in the caller took.
func newSubscription(spec SubscriptionSpec, now time.Time) (Subscription, error) {
	if !ValidID(spec.Owner) {
		return Subscription{}, errors.New("A subscription needs the assistant it belongs to.")
	}
	kind, err := ParseEventOption(spec.Event)
	if err != nil {
		return Subscription{}, err
	}
	sub := Subscription{
		ID:           statefile.NewID(),
		Owner:        spec.Owner,
		Source:       kind.Source,
		Kind:         kind.Kind,
		MaxPerHour:   DefaultSubscriptionPerHour,
		BatchSeconds: defaultBatchSeconds(kind.Source),
		ExpiresAt:    now.Add(DefaultSubscriptionTTL),
		State:        SubscriptionStanding,
		CreatedAt:    now,
		UpdatedAt:    now,
		SeenAt:       now,
	}
	// A create names every field there is: what it left empty is the default
	// the entry was seeded with, which is the same answer an unnamed field
	// gets on an edit.
	spec.TargetsSet, spec.AllSet, spec.OnceSet = true, true, true
	if err := applySubscription(&sub, spec, now); err != nil {
		return Subscription{}, err
	}
	return sub, nil
}

// editSubscription changes a subscription that still fires: what the spec
// names moves, everything else stands. Two things it never touches, the record
// of what happened (the count, when it was made, the events waiting in the
// window, a reaction that runs right now) and the event: another event is
// another subscription, and there are new and delete for that.
func editSubscription(sub Subscription, spec SubscriptionSpec, now time.Time) (Subscription, error) {
	if !sub.Open() {
		return Subscription{}, fmt.Errorf("That subscription is %s and cannot be changed. Make a new one.", sub.State)
	}
	if strings.TrimSpace(spec.Event) != "" {
		kind, err := ParseEventOption(spec.Event)
		if err != nil {
			return Subscription{}, err
		}
		if kind.Source != sub.Source || kind.Kind != sub.Kind {
			return Subscription{}, fmt.Errorf("A subscription cannot change its event, it reacts to %s. Make one for %s and remove this.", EventLabel(sub.Source, sub.Kind), kind.Label)
		}
	}
	if err := applySubscription(&sub, spec, now); err != nil {
		return Subscription{}, err
	}
	return sub, nil
}

// applySubscription writes what a spec names onto a subscription and checks
// the result. It is the one validation both ways in share: a create applies a
// spec onto an entry seeded with the defaults, an edit onto the one that
// stands, and what the spec does not name is left exactly as it is.
func applySubscription(sub *Subscription, spec SubscriptionSpec, now time.Time) error {
	if task := strings.TrimSpace(spec.Task); task != "" {
		sub.Task = task
	}
	if spec.TargetsSet {
		sub.Targets = mergedTargets(*sub, spec.Targets)
	}
	if spec.AllSet {
		sub.All = spec.All
	}
	if spec.OnceSet {
		sub.Once = spec.Once
	}
	if spec.MaxPerHour > 0 {
		sub.MaxPerHour = spec.MaxPerHour
	}
	if spec.BatchSet {
		if spec.Batch < 0 {
			return errors.New("A batch window cannot be negative.")
		}
		sub.BatchSeconds = int(spec.Batch / time.Second)
	}
	switch {
	case spec.Never:
		sub.ExpiresAt = time.Time{}
	case spec.Until < 0:
		return errors.New("An expiry looks backwards. Give a span from now, like 8h.")
	case spec.Until > 0:
		sub.ExpiresAt = now.Add(spec.Until)
	}
	// A schedule that moved is a schedule whose next tick has to be worked out
	// again; one nobody touched keeps the tick it is waiting for.
	moved := false
	if fields := strings.Join(strings.Fields(spec.Spec), " "); fields != "" {
		moved = fields != sub.Spec
		sub.Spec = fields
	}

	if strings.TrimSpace(sub.Task) == "" {
		return errors.New("A subscription needs a task: what to do when the event fires.")
	}
	if len([]rune(sub.Task)) > maxSubscriptionTaskRunes {
		return fmt.Errorf("That task is too long to store whole: at most %d runes.", maxSubscriptionTaskRunes)
	}
	if sub.All && len(sub.Targets) == 0 {
		return errors.New("A barrier needs the terminals it waits for: name them, or drop all and it fires on any event.")
	}
	if len(sub.Targets) < 2 {
		// One target arrives on its own event either way, so a barrier of one
		// would be the ordinary subscription under a name that reads like it
		// waits for something else.
		sub.All = false
	}
	switch sub.Source {
	case EventCron:
		if sub.Spec == "" {
			return errors.New("A schedule needs its five cron fields, like \"*/30 9-17 * * 1-5\".")
		}
		sub.Targets, sub.All = nil, false
		if moved || sub.NextAt.IsZero() {
			next, err := NextCron(sub.Spec, now)
			if err != nil {
				return err
			}
			sub.NextAt = next
		}
	default:
		if sub.Spec != "" {
			return errors.New("Only a schedule takes cron fields.")
		}
	}
	sub.UpdatedAt = now
	return nil
}

// mergedTargets is the target list a spec names, read against the one that
// stands: a target that stays keeps what it reached, an arrival stays an
// arrival and a deleted terminal stays deleted, one that is named for the
// first time starts fresh, and one that is left out takes its state with it.
func mergedTargets(sub Subscription, wanted []SubscriptionTarget) []SubscriptionTarget {
	var out []SubscriptionTarget
	for _, t := range wanted {
		terminal := strings.TrimSpace(t.Terminal)
		if terminal == "" || slices.ContainsFunc(out, func(o SubscriptionTarget) bool { return o.Terminal == terminal }) {
			continue
		}
		next := SubscriptionTarget{Terminal: terminal, Name: strings.TrimSpace(t.Name)}
		for _, stood := range sub.Targets {
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

// subscriptionChanges names what an edit moved, in the order the form asks for
// the fields, empty when nothing moved. Reactor.Edit answers it, so the line
// `subscription-edit` prints and the toast the page shows are the one wording.
func subscriptionChanges(before, after Subscription) string {
	var out []string
	if before.Task != after.Task {
		out = append(out, "task")
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
	if before.MaxPerHour != after.MaxPerHour {
		out = append(out, fmt.Sprintf("%d turn%s per hour", after.MaxPerHour, plural(after.MaxPerHour)))
	}
	if before.BatchSeconds != after.BatchSeconds {
		out = append(out, fmt.Sprintf("batch %ds", after.BatchSeconds))
	}
	return strings.Join(out, ", ")
}

// withinHour keeps the moments of the last hour.
func withinHour(turns []time.Time, now time.Time) []time.Time {
	kept := turns[:0]
	for _, t := range turns {
		if now.Sub(t) < time.Hour {
			kept = append(kept, t)
		}
	}
	return slices.Clone(kept)
}
