package assistant

import (
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/local/dev-cockpit/internal/statefile"
)

// A job is a coder the assistant keeps an eye on. The signal a coder produces
// when it finishes or asks something already reaches the cockpit as news; a job
// is what turns that news into a check by the assistant instead of a badge the
// user has to notice.
//
// Every job is explicit. It carries the task and the criterion that decides
// whether the task is done, because "done" is not a feeling: the check has to be
// able to fail. It also carries what it may cost, because every wake is a turn
// the user pays for.

// JobState is where a job stands.
type JobState string

const (
	// JobSteering is a job that still wakes the assistant.
	JobSteering JobState = "steering"
	// JobDone is a job whose criterion was met.
	JobDone JobState = "done"
	// JobBlocked is a job that cannot go on without the user.
	JobBlocked JobState = "blocked"
	// JobExpired is a job that ran out of wakes or out of time.
	JobExpired JobState = "expired"
	// JobStopped is a job the user or the assistant called off.
	JobStopped JobState = "stopped"
)

// Open reports whether this state still wakes the assistant.
func (s JobState) Open() bool { return s == JobSteering }

// A job's budget is what it may cost before it stops on its own: ten wakes
// and eight hours. Both are hard, because a coder that keeps reporting would
// otherwise keep paying for turns nobody asked for.
const (
	defaultMaxWakes = 10
	defaultJobTTL   = 8 * time.Hour
)

// maxTaskRunes and maxDoneWhenRunes bound what a job carries into every wake
// prompt, and they differ on purpose. The task only describes, it may be a
// whole briefing, so it gets room and is cut at its bound. The criterion is
// what a check judges against, it must stay tight, and one over its bound is
// refused whole instead of being stored as half a sentence.
const (
	maxTaskRunes     = 16000
	maxDoneWhenRunes = 4000
)

// Job is one steered job.
type Job struct {
	// Terminal is the coder session, which is also the id its news arrives
	// under, so a signal resolves to a job with one lookup.
	Terminal string `json:"terminal"`
	Name     string `json:"name"`
	Project  string `json:"project"`
	CoderID  string `json:"coderId"`
	// Task and DoneWhen are the job itself: one thing to do and the one
	// criterion that decides it is done. A job that needs a second step gets it
	// the same way the first one arrived, from the assistant a DONE woke.
	Task     string   `json:"task"`
	DoneWhen string   `json:"doneWhen"`
	State    JobState `json:"state"`
	// Source is where this job was asked for, empty for the browser and for
	// every job stored before this existed. It comes from the turn that called
	// coder-new or coder-steer, and it is what a channel filters its reports
	// by: a job steered from the settings page is nobody's chat business.
	Source    string    `json:"source,omitempty"`
	Wakes     int       `json:"wakes"`
	MaxWakes  int       `json:"maxWakes"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	// Note is the one line the last check left, and NoteAt when that was. A
	// check without news writes nothing else, so this is where the user sees
	// that the job is alive.
	Note   string    `json:"note,omitempty"`
	NoteAt time.Time `json:"noteAt,omitempty"`
	// LastAssistantInputAt is when this terminal last got input from an
	// assistant turn. The standstill rule reads it: a check that reports
	// WORKING on a coder that stood still must really have sent it something.
	LastAssistantInputAt time.Time `json:"lastAssistantInputAt,omitempty"`
	// LastWakeAt is when the last check ran. The heartbeat reads it: its own
	// quiet window keeps a paid check from being followed by another look
	// right away.
	LastWakeAt time.Time `json:"lastWakeAt,omitempty"`
	// CheckingSince is set while a check is running, so the page can say so and
	// nobody has to read a counter to know. A restart leaves it standing, and
	// the watcher picks those jobs up when it comes back.
	CheckingSince time.Time `json:"checkingSince,omitempty"`
	// Silent counts the checks in a row that came back without a verdict: a
	// crash, a time limit, an answer with no words. Those cost no wake, so
	// something has to stop a job from retrying them forever.
	Silent int `json:"silent,omitempty"`
	// ActivityDigest and ActivityAt are what the coder last looked like and when
	// that changed. A picture that stops changing is how the heartbeat tells a
	// coder that is working from one that is stuck without being able to say so.
	ActivityDigest string    `json:"activityDigest,omitempty"`
	ActivityAt     time.Time `json:"activityAt,omitempty"`
}

// Checking reports whether a check is running on this job right now.
func (j Job) Checking() bool { return !j.CheckingSince.IsZero() }

// Spent reports whether the job used up its budget.
func (j Job) Spent() bool { return j.MaxWakes > 0 && j.Wakes >= j.MaxWakes }

// maxClosedJobs is how many jobs that are over the store keeps. One entry per
// terminal ever steered grows forever otherwise, and what that costs is not the
// disk: the file is parsed whole on every read, and a read happens on the input
// route for every send to a steered terminal. So the bound is a count and not an
// age, because the parse is what is paid for. Open jobs are outside it whatever
// their number: they are what still wakes the assistant, and a job that
// disappears while it steers takes its terminal's ownership with it. It stays
// generous because this tail is also the horizon `jobs --contains` searches.
const maxClosedJobs = 150

// JobStore persists the jobs. One file, read through on every call like every
// other state file, so a job survives a restart and two processes cannot hold
// different ideas about it.
type JobStore struct {
	path string
	mu   sync.Mutex
}

// JobsPath is where the jobs live for a state directory.
func JobsPath(stateDir string) string {
	return filepath.Join(stateDir, "assistant", "jobs.json")
}

// NewJobStore returns the store for a state directory.
func NewJobStore(stateDir string) *JobStore {
	return &JobStore{path: JobsPath(stateDir)}
}

// List returns every job, newest first.
func (s *JobStore) List() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

func (s *JobStore) load() []Job {
	var out []Job
	statefile.Load(s.path, &out)
	return out
}

// Get returns the job of one terminal.
func (s *JobStore) Get(terminal string) (Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, w := range s.load() {
		if w.Terminal == terminal {
			return w, true
		}
	}
	return Job{}, false
}

// Save writes one job, replacing the entry of the same terminal.
func (s *JobStore) Save(w Job) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.load()
	replaced := false
	for i := range list {
		if list[i].Terminal == w.Terminal {
			list[i] = w
			replaced = true
			break
		}
	}
	if !replaced {
		list = append([]Job{w}, list...)
	}
	statefile.Save(s.path, 0o600, capClosed(list))
}

// capClosed drops the oldest jobs that are over once there are more than
// maxClosedJobs of them, and leaves the open ones alone. What decides which
// ones go is when they were made, not where they sit in the file: an entry a
// re-steered terminal replaced keeps the position of the job before it, so the
// file's order is only roughly the age.
func capClosed(list []Job) []Job {
	closed := make([]int, 0, len(list))
	for i := range list {
		if !list[i].State.Open() {
			closed = append(closed, i)
		}
	}
	if len(closed) <= maxClosedJobs {
		return list
	}
	sort.SliceStable(closed, func(a, b int) bool {
		return list[closed[a]].CreatedAt.After(list[closed[b]].CreatedAt)
	})
	drop := make(map[int]bool, len(closed)-maxClosedJobs)
	for _, i := range closed[maxClosedJobs:] {
		drop[i] = true
	}
	kept := list[:0]
	for i := range list {
		if !drop[i] {
			kept = append(kept, list[i])
		}
	}
	return kept
}

// PruneTerminals drops the jobs of terminals that are not in keep. The startup
// restore calls it with what its pass found, the same place and the same reason
// the notifications are pruned there: a session that was deleted leaves an entry
// that resolves to nothing forever, and every read of the store pays for parsing
// it. Open jobs stay whatever their terminal does. A job that ends has to say so
// to the user, and ending a job whose terminal is gone is the heartbeat's, with
// a report; dropping it here would be the one ending nobody hears. Returns how
// many entries were removed.
func (s *JobStore) PruneTerminals(keep map[string]bool) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.load()
	kept := list[:0]
	for _, w := range list {
		if w.State.Open() || keep[w.Terminal] {
			kept = append(kept, w)
		}
	}
	removed := len(list) - len(kept)
	if removed > 0 {
		statefile.Save(s.path, 0o600, kept)
	}
	return removed
}

// Update changes one job in place, under the same lock that reads it. Both the
// watcher and the input route write to the same record while a check runs, and
// each of them owns different fields: the assistant's last input belongs to the
// input route, the counters and the state to the check. Read, change and write
// in two separate lock sections would let the later writer put back what it read
// before the other one wrote, and what disappears that way is either a paid
// check or the send that was meant to get the coder going again.
//
// change decides whether its change stands: false leaves the file alone, which
// is what a caller wants once it sees, under this lock, that the job is not the
// one it meant to write to any more. The reported bool is whether the job was
// written, so a caller that has to decide something after the write asks that
// one question instead of two.
func (s *JobStore) Update(terminal string, change func(*Job) bool) (Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.load()
	for i := range list {
		if list[i].Terminal != terminal {
			continue
		}
		if !change(&list[i]) {
			return list[i], false
		}
		statefile.Save(s.path, 0o600, list)
		return list[i], true
	}
	return Job{}, false
}

// Delete removes the job of one terminal.
func (s *JobStore) Delete(terminal string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.load()
	kept := list[:0]
	for _, w := range list {
		if w.Terminal != terminal {
			kept = append(kept, w)
		}
	}
	statefile.Save(s.path, 0o600, kept)
}

// Sessions is how a check reaches the coder of the job it checks. It asks the
// coder, not the terminal: a screen carries the coder's input line with
// whatever draft stands in it, and a draft is not a message from anybody. What
// a session really did is the coder's own knowledge, so the coder answers, and
// how it answers depends on the provider. Implemented on the coder side, wired
// in main.
type Sessions interface {
	Activity(coderID, terminal string) (Activity, error)
	// Running reports whether the job's terminal still exists at all. A job
	// whose terminal is gone can never be met, and asking a coder that is not
	// there costs a paid turn to learn what the cockpit already knows.
	Running(coderID, terminal string) bool
}

// Activity is what the coder answered about a steered session: what it last did
// and whether it is still doing it. Why it stopped is deliberately absent. A
// dialog waiting for an answer, a context window with no room left and a turn
// that is simply over are the same picture from here, and the difference is not
// a flag anybody can set reliably: it is a screen that has to be read. The check
// reads it.
type Activity struct {
	// Text is what the session last did, newest last.
	Text string
	// Finished says its turn is over: it is waiting, not working.
	Finished bool
	// Screen says Text is the terminal picture rather than a recorded
	// conversation. Then the reading carries the coder's input line, and the
	// prompt has to say so.
	Screen bool
}

// maxSilentChecks is how many checks in a row may come back without a verdict
// before the user hears about it. A check without a verdict costs no wake, which
// is right, and would let a broken one retry on every signal forever, which is
// not: the second one says so and closes the job.
const maxSilentChecks = 2

// maxConcurrentChecks is how many checks may run at once, across every job.
// Five: jobs are independent, and one slow check must not make every other
// job's check stand in line behind it, a real build and test pass holds its
// slot for a long time. Each job still runs at most one check at a time
// (running and pending above), so this cap only decides how many different
// jobs may be checked at the same moment. It stays global and small because
// every check is a paid turn nobody asked for interactively: a fleet of noisy
// coders fans out to five turns, not to one per coder.
const maxConcurrentChecks = 5

// Watcher turns a coder's news into a check by the assistant. It owns the gates
// that decide whether a wake may happen at all, so the cost of the feature is
// visible in one place.
type Watcher struct {
	service  *Service
	store    *JobStore
	sessions Sessions
	now      func() time.Time

	// announce is who hears that ownership changed: a job began, was called
	// off, took itself up again or reached its end. The web layer publishes a
	// terminals event on it, so every surface pulls the fragments that carry
	// the steered mark. Notes and counters stay quiet here, they change no
	// ownership and would turn every check into a fleet of refetches.
	announce func(project string)

	// mu guards the in flight bookkeeping. running keeps a second signal from
	// starting a second turn for the same job, pending remembers that one
	// arrived so the check is repeated once instead of dropped.
	mu      sync.Mutex
	running map[string]bool
	pending map[string]bool

	// quiet and stall are the heartbeat's own windows, vanishAfter how long a
	// job is given before a terminal nobody can find counts as gone.
	quiet       time.Duration
	stall       time.Duration
	vanishAfter time.Duration

	// slots is the one global cap on checks: maxConcurrentChecks at a time,
	// whatever happens in the cockpit. It is a queue and not a limit that drops:
	// a second job whose coder reports while a check runs waits its turn instead
	// of going silent. A chat turn has its own slots and never waits for this.
	slots chan struct{}
}

// NewWatcher wires the watcher. There is no global switch: steering is
// explicit per job, so a job that exists is a job that wakes.
func NewWatcher(service *Service, store *JobStore, sessions Sessions) *Watcher {
	return &Watcher{
		service:     service,
		store:       store,
		sessions:    sessions,
		now:         time.Now,
		quiet:       heartbeatQuiet,
		stall:       stallAfter,
		vanishAfter: vanishGrace,
		running:     map[string]bool{},
		pending:     map[string]bool{},
		slots:       make(chan struct{}, maxConcurrentChecks),
	}
}

// OnStateChange registers who hears about a change of ownership: steer,
// release, reopen, and every verdict that closes a job. Called with the job's
// project so a page can refresh just that project's fragments.
func (w *Watcher) OnStateChange(fn func(project string)) { w.announce = fn }

func (w *Watcher) announceChange(project string) {
	if w.announce != nil {
		w.announce(project)
	}
}

// Marks is what the pages render about the jobs: which terminals an open job
// holds right now, and the stored criterion of the closed ones, which is what
// the steer dialog offers as its prefill when a terminal is steered again.
func (w *Watcher) Marks() (steered map[string]bool, doneWhens map[string]string) {
	steered = map[string]bool{}
	doneWhens = map[string]string{}
	for _, job := range w.store.List() {
		if job.State.Open() {
			steered[job.Terminal] = true
			continue
		}
		if job.DoneWhen != "" {
			doneWhens[job.Terminal] = job.DoneWhen
		}
	}
	return steered, doneWhens
}

// ValidateDoneWhen normalizes a done-when and refuses what a job cannot
// store: an empty one, and one over the bound. Lines survive, a done-when
// may be a list with one check per line, so only the line endings and the
// edges are normalized; folding to one line is a display concern of the
// places that need one. It is the one rule set: Steer applies it, and whoever
// wants to check a done-when before anything else exists (the coder create
// route, so a refused done-when cannot leave a running coder without its
// job) calls the same function instead of copying the bound and the
// message.
func ValidateDoneWhen(raw string) (string, error) {
	doneWhen := strings.TrimSpace(strings.ReplaceAll(raw, "\r\n", "\n"))
	if doneWhen == "" {
		return "", errors.New("A job needs a done-when that can be checked.")
	}
	if runes := len([]rune(doneWhen)); runes > maxDoneWhenRunes {
		return "", fmt.Errorf("That done-when is too long to store whole: %d runes, at most %d. Shorten it, a cut criterion would be judged as half a sentence.", runes, maxDoneWhenRunes)
	}
	return doneWhen, nil
}

// TruncateTask cuts a task to its bound and says so. The task only
// describes, it may be a whole briefing, so a long one is stored cut instead
// of refused, but the cut must not stay silent. The second return is the
// notice for the caller, empty when nothing was cut. Like ValidateDoneWhen
// it is the one rule set: Steer applies it, and the routes that answer a
// caller take the notice from the same function instead of copying the
// bound and the sentence.
func TruncateTask(raw string) (string, string) {
	task := strings.TrimSpace(raw)
	if len([]rune(task)) <= maxTaskRunes {
		return task, ""
	}
	return truncateRunes(task, maxTaskRunes), fmt.Sprintf("task was cut at %d runes", maxTaskRunes)
}

// DoneWhenLine is what a report or a list shows as a job's done-when. A job
// from the page may carry none: then the session's own task decides, and
// every surface says exactly that instead of an empty line.
func DoneWhenLine(doneWhen string) string {
	if strings.TrimSpace(doneWhen) == "" {
		return "what the session's own task says"
	}
	return doneWhen
}

// Steer starts a job on a terminal. The criterion may be empty: the check then
// judges against the task the session itself was given, and every place that
// shows the criterion says so (DoneWhenLine). Whether empty is allowed is the
// caller's rule, not this one's: the page may leave it out, the assistant's
// own coder-steer command may not, and the jobs handler enforces that at the door.
// A criterion over its bound is refused, never cut: it is what decides that a
// job is done, and a check against a criterion that ends mid-sentence judges
// against half a sentence without anybody knowing.
func (w *Watcher) Steer(spec Job) (Job, error) {
	terminal := strings.TrimSpace(spec.Terminal)
	if terminal == "" {
		return Job{}, errors.New("A job needs the terminal it steers.")
	}
	task, _ := TruncateTask(spec.Task)
	doneWhen := ""
	if strings.TrimSpace(spec.DoneWhen) != "" {
		var err error
		doneWhen, err = ValidateDoneWhen(spec.DoneWhen)
		if err != nil {
			return Job{}, err
		}
	}
	now := w.now().UTC()
	job := Job{
		Terminal:  terminal,
		Name:      strings.TrimSpace(spec.Name),
		Project:   strings.TrimSpace(spec.Project),
		CoderID:   strings.TrimSpace(spec.CoderID),
		Task:      task,
		DoneWhen:  doneWhen,
		Source:    Source(spec.Source),
		State:     JobSteering,
		MaxWakes:  defaultMaxWakes,
		CreatedAt: now,
		ExpiresAt: now.Add(defaultJobTTL),
		UpdatedAt: now,
	}
	w.store.Save(job)
	w.service.changed()
	w.announceChange(job.Project)
	return job, nil
}

// Release calls a job off. It stays visible in that state, so the user can see
// what happened to it instead of finding it gone. A check that is running on
// the job right now is killed: release takes the actor away, and a dead check
// writes nothing and costs nothing more.
func (w *Watcher) Release(terminal string) error {
	fresh, ok := w.store.Update(strings.TrimSpace(terminal), func(job *Job) bool {
		job.State = JobStopped
		job.UpdatedAt = w.now().UTC()
		return true
	})
	if !ok {
		return errors.New("No job steers that terminal.")
	}
	w.service.killChecks(fresh.Terminal)
	w.service.changed()
	w.announceChange(fresh.Project)
	return nil
}

// Forget removes the job of a terminal that is gone for good. Release is the
// end of a job whose terminal stays: it leaves the entry standing, so the user
// can see what happened to it. A deleted session has nothing left to show it
// next to, its entry would be read on every look at the store forever, and
// nothing can ever move it again, so it goes. A check that is still running is
// killed like on a release, for the same reason: nobody pays for its answer.
func (w *Watcher) Forget(terminal string) {
	terminal = strings.TrimSpace(terminal)
	job, ok := w.store.Get(terminal)
	if !ok {
		return
	}
	w.store.Delete(terminal)
	w.service.killChecks(terminal)
	w.service.changed()
	w.announceChange(job.Project)
}

// Get returns one job by the terminal it steers.
func (w *Watcher) Get(terminal string) (Job, bool) { return w.store.Get(terminal) }

// NoteAssistantInput records that an assistant turn wrote into this terminal.
// The user's inputs are none of the job's business: steering is ownership, and
// only steer and release change it. What is recorded here serves the checks
// themselves. A terminal nobody steers is ignored, so this costs one lookup on
// the input path.
func (w *Watcher) NoteAssistantInput(terminal string) {
	reopened := false
	now := w.now().UTC()
	fresh, ok := w.store.Update(strings.TrimSpace(terminal), func(job *Job) bool {
		job.LastAssistantInputAt = now
		if job.State == JobBlocked {
			// A blocked job is the one state the cockpit created itself, while
			// the job was still wanted: a check asked for a decision and stopped
			// there. Sending to that coder is the decision, so the job is on
			// again, with its budget from the start, because a new leg begins.
			// The other closed states are decisions or limits of their own, and
			// a send must not quietly undo them: done, stopped and expired stay
			// closed and the page offers one tap to steer again.
			job.State = JobSteering
			job.Wakes = 0
			job.Silent = 0
			job.LastWakeAt = time.Time{}
			job.ExpiresAt = now.Add(defaultJobTTL)
			job.Note = "Steering again, the coder was sent something."
			job.NoteAt = now
			reopened = true
		}
		job.UpdatedAt = now
		return true
	})
	if ok && reopened {
		w.service.changed()
		w.announceChange(fresh.Project)
	}
}

// List returns every job, open ones first.
func (w *Watcher) List() []Job {
	out := w.store.List()
	SortJobs(out)
	return out
}

// Handle is one signal from a terminal. Every reason not to wake is checked in
// one place, so the cost of this feature never depends on what a prompt decides,
// and the repeat run of a check asks the same questions again.
func (w *Watcher) Handle(terminal string) {
	job, ok := w.gate(terminal)
	if !ok {
		return
	}

	w.mu.Lock()
	if w.running[terminal] {
		// The coder reported again while the last check was still running. One
		// more check after this one is enough, a second turn now would pay
		// twice for the same picture.
		w.pending[terminal] = true
		w.mu.Unlock()
		return
	}
	w.running[terminal] = true
	w.mu.Unlock()

	go w.check(job)
}

// gate answers whether this job may be checked right now, and returns the job
// as it stands. Every reason not to wake lives here: the job's own state, its
// budget and its time. A signal buys its check at once, there is no quiet
// window behind it any more: it swallowed the done signals of short jobs,
// which then waited for the heartbeat to notice them. Paying twice for one
// picture is what running and pending prevent, and the budget caps what a
// noisy coder can spend either way.
func (w *Watcher) gate(terminal string) (Job, bool) {
	job, ok := w.store.Get(terminal)
	if !ok || !job.State.Open() {
		return Job{}, false
	}
	now := w.now().UTC()
	switch {
	case job.Spent():
		w.expire(job, "the checks it was given are used up")
		return Job{}, false
	case !job.ExpiresAt.IsZero() && now.After(job.ExpiresAt):
		w.expire(job, "the time it was given is over")
		return Job{}, false
	}
	return job, true
}

// check runs one wake and, when a signal arrived while it ran, exactly one more.
// The repeat run goes through the same gate as the first: a job whose budget ran
// out while the check was running stops it.
func (w *Watcher) check(job Job) {
	for {
		w.slots <- struct{}{}
		w.wake(job)
		<-w.slots

		w.mu.Lock()
		again := w.pending[job.Terminal]
		delete(w.pending, job.Terminal)
		if !again {
			delete(w.running, job.Terminal)
			w.mu.Unlock()
			return
		}
		// Still marked as running, so no third signal starts a parallel check
		// while this one asks whether it may go again.
		w.mu.Unlock()

		fresh, ok := w.gate(job.Terminal)
		if !ok {
			w.mu.Lock()
			delete(w.running, job.Terminal)
			w.mu.Unlock()
			return
		}
		job = fresh
	}
}

// observe asks the coder what the steered session did and whether its turn is
// over. The check never looks at the screen itself: that is the coder's job,
// and for a coder that keeps a transcript the screen is the wrong source
// entirely, because it shows a draft for the next prompt that nobody typed.
func (w *Watcher) observe(job Job) Activity {
	if w.sessions == nil {
		return Activity{}
	}
	activity, err := w.sessions.Activity(job.CoderID, job.Terminal)
	if err != nil {
		// The coder cannot be asked any more. That is worth one last check,
		// because it may have finished and exited, and it is certainly not
		// working.
		return Activity{Text: "The coder is not there any more: " + oneLine(err.Error()), Finished: true}
	}
	if strings.TrimSpace(activity.Text) == "" {
		activity.Text = "This session has not said anything yet."
	}
	return activity
}

// wake spends one turn on a job: what the coder says about the session plus the
// job go into a prompt, the answer decides whether the user hears about it.
func (w *Watcher) wake(job Job) {
	activity := w.observe(job)

	// The job says that a check is running, which is what the page shows. The
	// counter moves when a verdict comes back, not now: a check its time limit
	// killed and a check that answered nothing never produced a judgement, and
	// neither may cost one of the ten a job has.
	//
	// The record that comes back from that write is also where the standstill
	// rule's baseline comes from. Minutes can pass between the signal and this
	// line: the check waits for the one slot, and reading the coder takes its
	// own time, so the baseline has to be the job as it stands now, not the
	// copy the signal was gated on.
	job = w.mark(job.Terminal, func(fresh *Job) { fresh.CheckingSince = w.now().UTC() })
	if job.Terminal == "" {
		return
	}

	// What this check found before it started is what the verdict is judged
	// against afterwards. It travels into the register with the turn, so a
	// restart in the middle changes nothing about how the answer is read. The
	// job's identity travels with it: the answer belongs to this job, not to
	// whatever is steered under the terminal by the time it arrives.
	seen := checkContext{
		JobCreatedAt: job.CreatedAt,
		Idle:         activity.Finished,
		SteeredAt:    job.LastAssistantInputAt,
	}

	run, err := w.service.startWake(wakeSpec{
		Terminal: job.Terminal,
		Prompt:   w.wakePrompt(job, activity),
		Context:  seen,
		// A check acts for the job, so whatever it starts belongs where the job
		// belongs: the origin travels on into its turn.
		Source: job.Source,
	})
	if err != nil {
		w.conclude(job, seen, wakeOutcome{}, err)
		return
	}
	seen.MessageID = run.rec.MessageID
	outcome, err := w.service.awaitWake(run)
	w.conclude(job, seen, outcome, err)
}

// adopt takes over a check that outlived the server. It is the second half of
// wake and nothing else: the turn is already running, what it found before it
// started came back out of the register, and the verdict is judged exactly as it
// would have been without the restart.
func (w *Watcher) adopt(check AdoptedCheck) {
	// The turn is already on the machine, so the slot is taken if it is free and
	// skipped if it is not. Waiting for it would be waiting for a check that is
	// running, which is what this one is.
	held := false
	select {
	case w.slots <- struct{}{}:
		held = true
	default:
	}
	defer func() {
		if held {
			<-w.slots
		}
	}()

	job, ok := w.store.Get(check.Terminal)
	if !ok {
		// The job is gone, so there is nobody to report to. The check is still
		// waited on, otherwise its provider session would stay behind.
		_, _ = w.service.awaitWake(check.run)
		return
	}
	log.Printf("assistant: a check on %s outlived the restart, waiting for its verdict", check.Terminal)
	w.mu.Lock()
	w.running[check.Terminal] = true
	w.mu.Unlock()

	outcome, err := w.service.awaitWake(check.run)
	w.conclude(job, check.Context, outcome, err)

	w.mu.Lock()
	again := w.pending[check.Terminal]
	delete(w.pending, check.Terminal)
	delete(w.running, check.Terminal)
	w.mu.Unlock()
	if again {
		go w.Handle(check.Terminal)
	}
}

// conclude turns what a check came back with into what the job and the user see.
// It is the one place that reads a verdict, whether the check ran start to
// finish in this process or was picked up from the register halfway through.
// Every write goes through markJob: a verdict belongs to the job the check was
// started for, and when the terminal has been steered again since, the answer
// is dropped whole, the successor pays for none of it.
func (w *Watcher) conclude(job Job, seen checkContext, outcome wakeOutcome, err error) {
	terminal := job.Terminal
	if err != nil {
		// No verdict: the turn crashed, ran into its time limit, was killed by
		// a release or answered nothing. It costs no wake, and on an open job
		// it is never silent, the job says what happened; a job whose checks
		// keep coming back empty would otherwise retry forever, so the second
		// one in a row goes to the user. A closed job only gets its spinner
		// cleared: the ending that closed it wrote its own note, and the kill
		// that follows such an ending must not overwrite it with noise.
		log.Printf("assistant: wake for %s: %v", terminal, err)
		silent := 0
		open := false
		if w.markJob(terminal, seen, func(fresh *Job) {
			fresh.CheckingSince = time.Time{}
			fresh.LastWakeAt = w.now().UTC()
			open = fresh.State.Open()
			if !open {
				return
			}
			fresh.Silent++
			silent = fresh.Silent
			fresh.Note = truncateRunes("The check came back without a verdict: "+oneLine(err.Error()), maxDoneWhenRunes)
			fresh.NoteAt = w.now().UTC()
		}).Terminal == "" {
			log.Printf("assistant: dropped a late check answer for %s, the job it was started for is gone", terminal)
			return
		}
		if open && silent >= maxSilentChecks {
			w.report(job, seen, wakeOutcome{Verdict: VerdictBlocked, Text: silentReport(job, err)})
		}
		return
	}
	// The counted record is the one everything after it is decided from: the
	// input route writes to the same job while a check runs, and the copy this
	// call started with is minutes old by now.
	job = w.markJob(terminal, seen, func(fresh *Job) {
		fresh.CheckingSince = time.Time{}
		fresh.Wakes++
		fresh.LastWakeAt = w.now().UTC()
		fresh.Silent = 0
	})
	if job.Terminal == "" {
		// The job was deleted while the check ran, or the terminal is steered
		// again by now: either way there is nobody this verdict belongs to.
		log.Printf("assistant: dropped a late check answer for %s, the job it was started for is gone", terminal)
		return
	}
	outcome = w.resolveStandstill(job, outcome, seen.Idle, seen.SteeredAt)

	if !outcome.Verdict.News() {
		w.note(job, outcome.Text)
		return
	}
	w.report(job, seen, outcome)
}

// mark changes the stored job in place, on the copy that is on disk right now.
// A check runs for minutes, and the input route writes to the same record while
// it does, so the fields a check owns are written back one at a time instead of
// saving a copy that went stale.
func (w *Watcher) mark(terminal string, change func(*Job)) Job {
	fresh, ok := w.store.Update(terminal, func(job *Job) bool {
		change(job)
		job.UpdatedAt = w.now().UTC()
		return true
	})
	if !ok {
		return Job{}
	}
	w.service.changed()
	return fresh
}

// markJob is mark for what a check writes back: it writes only while the entry
// is still the job the check was started for. The store keys jobs by terminal,
// and steering the terminal again replaces the entry, so everything a check
// charges to its job, the spent wake, the note, the silent counter, has to
// check the identity inside the write, not before it.
func (w *Watcher) markJob(terminal string, seen checkContext, change func(*Job)) Job {
	fresh, ok := w.store.Update(terminal, func(job *Job) bool {
		if !seen.forJob(*job) {
			return false
		}
		change(job)
		job.UpdatedAt = w.now().UTC()
		return true
	})
	if !ok {
		return Job{}
	}
	w.service.changed()
	return fresh
}

// Recover picks up what a restart left behind. A check whose process outlived
// the restart is handed over by the service and simply carried on, and only the
// jobs whose check really died are checked again: the signal that started that
// check is spent, its inbox file is gone, and nothing will ever repeat it, so
// the job is the only one who knows. That is why a running check is written on
// the job at all.
//
// The provider session such a dead check left behind is swept by the caller,
// which owns the coders, see IsCheckSession.
func (w *Watcher) Recover(adopted []AdoptedCheck) {
	carried := map[string]bool{}
	for _, check := range adopted {
		carried[check.Terminal] = true
		go w.adopt(check)
	}
	for _, job := range w.store.List() {
		if !job.Checking() || carried[job.Terminal] {
			continue
		}
		w.mark(job.Terminal, func(fresh *Job) {
			fresh.CheckingSince = time.Time{}
			fresh.LastWakeAt = time.Time{}
			fresh.Note = "The check was interrupted by a restart, looking again."
			fresh.NoteAt = w.now().UTC()
		})
		log.Printf("assistant: a check on %s was interrupted by a restart, checking again", job.Terminal)
		if job.State.Open() {
			go w.Handle(job.Terminal)
		}
	}
}

// report closes a job and tells the user, which is the same pair of steps for a
// finished job, a blocked one and a standstill.
func (w *Watcher) report(job Job, seen checkContext, outcome wakeOutcome) {
	fresh, ok := w.store.Update(job.Terminal, func(entry *Job) bool {
		if !entry.State.Open() {
			// The user called the job off, or it ran out, while this check was
			// running. Then it is off: a late answer must not reopen a job the
			// user already decided about.
			return false
		}
		if !seen.forJob(*entry) {
			// The terminal is steered again, this entry is the successor. A
			// late answer must not close a job it was never asked about.
			return false
		}
		entry.State = JobDone
		if outcome.Verdict == VerdictBlocked {
			entry.State = JobBlocked
		}
		entry.Note = oneLine(outcome.Text)
		entry.NoteAt = w.now().UTC()
		entry.UpdatedAt = entry.NoteAt
		return true
	})
	if !ok {
		return
	}
	w.service.recordWake(fresh, seen.MessageID, outcome.Verdict, outcome.Text)
	w.service.changed()
	w.announceChange(fresh.Project)
}

// resolveStandstill enforces the one thing a check may not do: leave a job
// standing. WORKING is only true while the coder works. If it stands still and
// the criterion is not met, the check either sent it the next step, which the
// input path records as assistant input, or it has nothing to move it with, and
// then the user has to hear about it.
func (w *Watcher) resolveStandstill(job Job, outcome wakeOutcome, idle bool, steeredBefore time.Time) wakeOutcome {
	if !idle || outcome.Verdict != VerdictWorking {
		return outcome
	}
	if fresh, ok := w.store.Get(job.Terminal); ok && fresh.LastAssistantInputAt.After(steeredBefore) {
		// It steered the coder, so something is moving again.
		return outcome
	}
	return wakeOutcome{Verdict: VerdictBlocked, Text: standstillReport(job, outcome.Text)}
}

// silentReport is what the user reads when the checks themselves stop working.
// The job is closed, because nobody is checking it any more and pretending
// otherwise is the failure this whole feature exists to prevent.
func silentReport(job Job, err error) string {
	name := job.Name
	if name == "" {
		name = job.Terminal
	}
	var b strings.Builder
	fmt.Fprintf(&b, "I cannot check on **%s** any more: %s\n\n", name, oneLine(err.Error()))
	fmt.Fprintf(&b, "Done when: %s\n\n", DoneWhenLine(job.DoneWhen))
	b.WriteString("Nothing is steering this coder now. Tell me to look again, or take it from here yourself.")
	return b.String()
}

// standstillReport says what nobody else would: the coder is idle, the job is
// not done, and the check did not move it.
func standstillReport(job Job, said string) string {
	name := job.Name
	if name == "" {
		name = job.Terminal
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**%s** is idle and the job is not done, and the check did not send it anything.\n\n", name)
	fmt.Fprintf(&b, "Done when: %s\n\n", DoneWhenLine(job.DoneWhen))
	if line := oneLine(said); line != "" {
		fmt.Fprintf(&b, "The check said: %s\n\n", line)
	}
	b.WriteString("It needs a decision or a next step from you.")
	return b.String()
}

// note records what a check without news found. No transcript entry, no push:
// the job's own line on the page is where a "still working" belongs.
func (w *Watcher) note(job Job, text string) {
	fresh, ok := w.store.Update(job.Terminal, func(entry *Job) bool {
		if !entry.State.Open() {
			return false
		}
		if !entry.CreatedAt.Equal(job.CreatedAt) {
			// The terminal is steered again, the line belongs to the old job.
			return false
		}
		if note := oneLine(text); note != "" {
			entry.Note = truncateRunes(note, maxDoneWhenRunes)
			entry.NoteAt = w.now().UTC()
		}
		entry.UpdatedAt = w.now().UTC()
		return true
	})
	if !ok {
		return
	}
	w.service.changed()
	if fresh.Spent() {
		w.expire(fresh, "the checks it was given are used up")
	}
}

// SweepExpired ends every job whose time or budget is used up. It is the same
// report a signal would have produced, written without one: a job ends most
// often by going quiet, and every gate used to sit behind a signal, so the one
// ending nobody can hear was the one that mattered. The heartbeat runs this
// first on every pass, which is what makes that report happen without a signal.
func (w *Watcher) SweepExpired() {
	now := w.now().UTC()
	for _, job := range w.store.List() {
		if !job.State.Open() {
			continue
		}
		switch {
		case job.Spent():
			w.expire(job, "the checks it was given are used up")
		case !job.ExpiresAt.IsZero() && now.After(job.ExpiresAt):
			w.expire(job, "the time it was given is over")
		}
	}
}

// expire ends a job that nobody will check again and says so once. A job that
// stops quietly breaks the promise the whole feature makes: the user hears about
// a job without having to look. The report goes the same way a finished one
// does, so there is no second channel and no second kind of message.
func (w *Watcher) expire(job Job, reason string) {
	report := ""
	fresh, ok := w.store.Update(job.Terminal, func(entry *Job) bool {
		if !entry.State.Open() {
			// Already closed, by another signal or by the user. One report per job.
			return false
		}
		// The report is written from the job as the last check left it, so what
		// that check found is carried into the last word the user gets.
		report = expiryReport(*entry, reason)
		entry.State = JobExpired
		entry.Note = "Stopped steering: " + reason + "."
		entry.NoteAt = w.now().UTC()
		entry.UpdatedAt = entry.NoteAt
		return true
	})
	if !ok {
		return
	}
	// A check that is still running on the expired job is killed like on a
	// release: nobody is steering any more, so nobody pays for its answer.
	w.service.killChecks(fresh.Terminal)
	w.service.recordWake(fresh, "", VerdictExpired, report)
	w.service.changed()
	w.announceChange(fresh.Project)
}

// expiryReport is what the user reads: what stopped, why, and that nobody is
// looking at it any more.
func expiryReport(job Job, reason string) string {
	name := job.Name
	if name == "" {
		name = job.Terminal
	}
	var b strings.Builder
	fmt.Fprintf(&b, "I stopped steering **%s**", name)
	if job.Project != "" {
		fmt.Fprintf(&b, " in %s", job.Project)
	}
	fmt.Fprintf(&b, ": %s, so nothing checks this job any more.\n\n", reason)
	fmt.Fprintf(&b, "Done when: %s\n\n", DoneWhenLine(job.DoneWhen))
	if job.Note != "" {
		fmt.Fprintf(&b, "The last check said: %s\n\n", job.Note)
	}
	b.WriteString("Tell me if you want another look at it.")
	return b.String()
}

// SortJobs puts the jobs that still wake the assistant first, then the
// newest. Exported because every list of jobs is read in this order, the page,
// the conversation and `dev-cockpit assistant job-list`, and one order means one function.
func SortJobs(list []Job) {
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].State.Open() != list[j].State.Open() {
			return list[i].State.Open()
		}
		return list[i].CreatedAt.After(list[j].CreatedAt)
	})
}

// cockpitCommands is how a check is told to call the cockpit's own commands.
// Implemented by the assistant workspace, which already builds the same strings
// for the generated instructions.
type cockpitCommands interface {
	CockpitCommand(name string) string
}

// command names one of the cockpit's commands for the prompt, with the path and
// the directories of this instance when they are known.
func (w *Watcher) command(name string) string {
	if w.service != nil {
		if namer, ok := w.service.projects.(cockpitCommands); ok {
			return namer.CockpitCommand(name)
		}
	}
	return "dev-cockpit " + name
}

// wakePrompt is what the assistant reads when a coder reported. It carries the
// job, what the terminal shows, what this turn may change, and the contract for
// the answer. The contract is what keeps a check from becoming noise: a wake
// without news must be able to say so in one word.
func (w *Watcher) wakePrompt(job Job, activity Activity) string {
	return wakePromptWith(job, activity,
		w.command("terminal-screen"), w.command("coder-send-prompt"), w.command("coder-send-control-keys"))
}

// wakePrompt is the same prompt with the plain command names, for a caller
// without an instance to ask.
func wakePrompt(job Job, activity Activity) string {
	return wakePromptWith(job, activity, "dev-cockpit assistant terminal-screen",
		"dev-cockpit assistant coder-send-prompt", "dev-cockpit assistant coder-send-control-keys")
}

func wakePromptWith(job Job, activity Activity, outputCommand, sendCommand, keysCommand string) string {
	var b strings.Builder
	// The opening matches what bought the check. Most checks are bought by a
	// report, the coder finished or asked something, and greeting their judge
	// with a stall story frames every job as stuck. Only a session that stands
	// still mid work gets the not moving opening.
	if activity.Finished {
		b.WriteString("A coder you are steering reported. The job stands to be judged against its criterion, ")
		b.WriteString("and when it is not met, find out what is in the way and get the coder going again.\n\n")
	} else {
		b.WriteString("A coder you are steering is not moving. Find out what is in the way and get it going again.\n\n")
	}
	b.WriteString("## The job\n\n")
	fmt.Fprintf(&b, "- terminal: %s\n", job.Terminal)
	if job.Name != "" {
		fmt.Fprintf(&b, "- coder: %s\n", job.Name)
	}
	if job.Project != "" {
		fmt.Fprintf(&b, "- project: %s\n", job.Project)
	}
	if job.Task != "" {
		fmt.Fprintf(&b, "- task: %s\n", job.Task)
	}
	// A criterion may be absent: a job from the page carries none, and then
	// the session's own task is the criterion. It may also be a list, one
	// check per line. The lines have to reach the check as lines: folded into
	// one they read as one sentence, and a check that skims it treats the
	// tail as elaboration instead of as its own condition.
	if strings.TrimSpace(job.DoneWhen) == "" {
		b.WriteString("- done when: nothing was written down for this job. It is done when the task this ")
		b.WriteString("session was given is complete: read the task and the session's own record, judge ")
		b.WriteString("against that, and name in your report what you checked.\n\n")
	} else if lines := strings.Split(job.DoneWhen, "\n"); len(lines) > 1 {
		b.WriteString("- done when, every line of it:\n")
		for _, line := range lines {
			fmt.Fprintf(&b, "    %s\n", strings.TrimSpace(line))
		}
		b.WriteString("\n")
	} else {
		fmt.Fprintf(&b, "- done when: %s\n\n", job.DoneWhen)
	}

	if activity.Screen {
		b.WriteString("## What its terminal showed\n\n```\n")
	} else {
		b.WriteString("## What the session last recorded\n\n```\n")
	}
	b.WriteString(strings.TrimSpace(activity.Text))
	b.WriteString("\n```\n\n")
	if activity.Screen {
		b.WriteString("The bottom of a screen is the coder's input line, and whatever stands there is the coder's own ")
		b.WriteString("suggestion for a next prompt. Nobody typed it, it belongs to nobody, and it says nothing ")
		b.WriteString("about whether anything is moving.\n\n")
	}

	b.WriteString("## Moving it\n\n")
	b.WriteString("This terminal is steered, so it is yours to keep moving.\n\n")
	fmt.Fprintf(&b, "Read what you need: the project files, the cockpit's read only commands, and `%s %s` ", outputCommand, job.Terminal)
	b.WriteString("for the screen as it stands right now, which is where you see what stopped it. ")
	fmt.Fprintf(&b, "Then move it: `%s %s \"<text>\"` sends a prompt, ", sendCommand, job.Terminal)
	fmt.Fprintf(&b, "`%s %s <key>...` presses keys, in one call, in the order you would press them ", keysCommand, job.Terminal)
	b.WriteString("(`arrow-down`, `arrow-up`, `enter`, `escape`).\n\n")
	b.WriteString("Examples, not a list to work through: a tool call that ran into an API error is often worth ")
	b.WriteString("another go; a coder that says it has no room left to think in takes `/compact`; an open dialog ")
	b.WriteString("takes keys and never text, sent as text the answer lands in the chooser as text and the question ")
	b.WriteString("stays open. What is really in the way is on the screen, and what to do about it is your judgement.\n\n")
	b.WriteString("A question the task already answers is yours to answer. One that costs money, deletes something, ")
	b.WriteString("is hard to undo or that the task does not imply belongs to the user: report BLOCKED and touch nothing. ")
	b.WriteString("Starting a coder is allowed when the task explicitly calls for that next step, ")
	b.WriteString("for example a case decision the user wrote into it, and refused otherwise. ")
	b.WriteString("Creating or deleting projects is refused for this turn.\n\n")

	b.WriteString("## Your answer\n\n")
	b.WriteString("Answer while you still can. This turn has two hours and is killed when that runs out, ")
	b.WriteString("which reaches the user as a job whose check came back with nothing. If checking properly ")
	b.WriteString("would take longer than that, say what you know now and what is still open, as WORKING or ")
	b.WriteString("BLOCKED, and let the next check carry on.\n\n")
	b.WriteString("Your whole answer starts with the verdict. Do not think out loud first: everything before the verdict ")
	b.WriteString("is thrown away, and the rest is what the user reads. Exactly one of these:\n\n")
	b.WriteString("- `DONE: <what the user needs to know, one or two sentences>`\n")
	b.WriteString("- `BLOCKED: <what is in the way and what you need from the user>`\n")
	b.WriteString("- `WORKING: <one short line about what you sent it and what it is doing now>`\n")
	b.WriteString("- `NOTHING`\n\n")
	b.WriteString("Use DONE only when the done-when is met and you checked it, not when it looks likely. ")
	b.WriteString("Use WORKING only when the coder is going again, which for a coder that had stopped means you sent it ")
	b.WriteString("something: a WORKING without that is turned into BLOCKED anyway, because a job nobody moves is stuck. ")
	b.WriteString("DONE and BLOCKED reach the user on their phone; WORKING and NOTHING stay quiet, ")
	b.WriteString("so a report that is not one of those two costs a turn and says nothing. ")
	b.WriteString("While this job is open the coder's own news rings nowhere, so your report is the one thing ")
	b.WriteString("the user hears about it.\n")
	return b.String()
}

// Verdict is what a wake concluded.
type Verdict string

const (
	VerdictDone    Verdict = "done"
	VerdictBlocked Verdict = "blocked"
	VerdictWorking Verdict = "working"
	VerdictNothing Verdict = "nothing"
	// VerdictExpired is not something a check answers. It is what the watcher
	// records when a job runs out of checks or out of time, so the message that
	// says nobody is steering any more looks like the other reports.
	VerdictExpired Verdict = "expired"
)

// News reports whether this verdict is worth the user's attention.
func (v Verdict) News() bool { return v == VerdictDone || v == VerdictBlocked }

// verdictPattern reads the verdict at the front of an answer. The separator is
// optional and may be a colon or a dash, because that is the formatting a model
// drifts into; the word itself is the contract.
var verdictPattern = regexp.MustCompile(`(?i)^\s*[*_#>\s]*(done|blocked|working|nothing)\b[*_]*\s*[:\-–]?\s*[*_]*\s*`)

// verdictInText finds the contract form of a verdict after a model talked first:
// upper case and followed by a colon, which prose never is. Everything before it
// is thinking out loud and belongs in no note. The colon is required here, unlike
// at the front of an answer: a sentence may well contain the word in capitals,
// and taking that for a verdict would cut a real report in half.
var verdictInText = regexp.MustCompile(`(DONE|BLOCKED|WORKING|NOTHING)\b[*_]*\s*:\s*[*_]*\s*`)

// bareNothing finds the one verdict the contract spells without a colon after a
// model talked first: NOTHING, upper case, standing as its own word. The case
// is the guard against prose, a sentence that needs the word writes it small.
var bareNothing = regexp.MustCompile(`\bNOTHING\b`)

// otherVerdict is any other verdict word in capitals. An answer that carries
// one may be a real DONE or BLOCKED report whose form just failed to parse,
// and cutting it at a stray NOTHING would cut that report, so bare NOTHING
// only counts in an answer without them.
var otherVerdict = regexp.MustCompile(`\b(DONE|BLOCKED|WORKING)\b`)

// parseVerdict splits an answer into its verdict and the text that belongs to
// the user. The text starts at the verdict: a model that reasons first would
// otherwise put its planning into the job's line and into the report the user
// reads. NOTHING is the one verdict the contract spells bare, so after the
// colon forms failed it is recovered without one. An answer with no verdict at
// all counts as working, because dropping a real report would be worse than a
// line nobody asked for.
func parseVerdict(answer string) (Verdict, string) {
	text := strings.TrimSpace(answer)
	if text == "" {
		return VerdictNothing, ""
	}
	if match := verdictPattern.FindStringSubmatch(text); match != nil {
		return verdictOf(match[1]), strings.TrimSpace(text[len(match[0]):])
	}
	if match := verdictInText.FindStringSubmatchIndex(text); match != nil {
		word := text[match[2]:match[3]]
		return verdictOf(word), strings.TrimSpace(text[match[1]:])
	}
	if match := bareNothing.FindStringIndex(text); match != nil && !otherVerdict.MatchString(text) {
		return VerdictNothing, strings.TrimSpace(text[match[1]:])
	}
	return VerdictWorking, text
}

func verdictOf(word string) Verdict {
	switch strings.ToLower(word) {
	case "done":
		return VerdictDone
	case "blocked":
		return VerdictBlocked
	case "nothing":
		return VerdictNothing
	default:
		return VerdictWorking
	}
}
