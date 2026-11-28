// Package approval holds the actions an assistant may only take once the user
// said yes: a compose command that asks first, a project delete. An approval
// is one such action waiting: who asked, what kind of action it is, the rows
// the dialog shows, and the run that does it once approved.
//
// The question travels through the askpass broker, so every signed-in page
// shows it and the push channels carry it to a phone. Whatever the user
// answers, the owner hears how it ended through one hook, Config.Ended, unless
// somebody waits for the answer in a request (Await) and hears it there. A
// kind hands in a Request and nothing else: its key, its line, its rows and
// its run; this package knows no kind by name. The run lives in this process
// alone, the entry on disk exists so the next process can tell the owners what
// a restart took: a question that still waited, and an end that was settled
// and that its owner had not heard yet.
package approval

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/marein/dev-cockpit/internal/askpass"
	"github.com/marein/dev-cockpit/internal/statefile"
)

// Timeout bounds how long an approval waits for the decision. Long enough for
// a phone in a pocket, short enough that a "down with volumes" or a project
// delete approved much later still means what the assistant asked for.
const Timeout = 30 * time.Minute

// MaxPerOwner bounds how many approvals one assistant may have waiting at
// once. Every one of them is a question on every page and on the phone, and
// the bound is what keeps a turn in a loop from burying the user in them.
const MaxPerOwner = 100

// MaxDetailBytes bounds what the dialog shows of one value. Below it the
// user approves what they read, whole. Every page and the phone carry each
// question, up to MaxPerOwner of them per assistant, and this bound is what
// keeps that sane; the run takes the whole value all the same.
const MaxDetailBytes = 64 << 10

// The verdicts an approval ends in: approved and the run went through,
// approved and the run failed, or never run.
const (
	Done     = "done"
	Failed   = "failed"
	Declined = "declined"
)

// The answers an awaited approval ends in, see Await: the user approved or
// declined it, nobody answered in time, or it went away unanswered because the
// caller stopped waiting or its owner was deleted.
const (
	Approved  = "approved"
	Expired   = "expired"
	Withdrawn = "withdrawn"
)

// Run is what an approved action does. It answers the outcome the owner
// reads, or the error that says why it did not go through. It runs long after
// the ask, so it resolves what it acts on again rather than trusting what
// stood when it was asked.
type Run func() (string, error)

// Request is one action an assistant asks for, as its kind hands it in. Key
// is the action's stable identity within its kind, the same Kind and Key wait
// once whoever asks; What is the one line it is read by ("Delete project
// shop"), the notification and the note name it; Title is the dialog's head
// line where it says less than What, whose rest the dialog's chips show; Icon
// and Tone are what its dialog wears, its kind's where empty; Details are
// what the dialog shows;
// Command and Dir, where the action runs a program, are the
// command line and the directory it runs in, shown the way a proxied git
// call's are; Projects, where the action touches any, name the projects
// whose deletion ends the approval; Run is what happens once the user
// approves. Remember is what the box under the question does, under the
// words of RememberLabel; without it the box turns the kind's switch off. ID
// is the id the approval stands under, a fresh one where empty: a kind whose
// run starts something that keeps a record of its own names that by it, so
// the next process finds it from the approval, see Config.Started.
type Request struct {
	ID            string
	Owner         string
	Kind          string
	Key           string
	What          string
	Title         string
	Icon          string
	Tone          string
	Details       []askpass.Detail
	Command       string
	Dir           string
	Projects      []string
	Run           Run
	RememberLabel string
	Remember      func()
}

// Approval is one action waiting for the user, as it stands on disk: the
// request without its run.
type Approval struct {
	ID        string           `json:"id"`
	Owner     string           `json:"owner"`
	Kind      string           `json:"kind"`
	Key       string           `json:"key"`
	What      string           `json:"what"`
	Title     string           `json:"title,omitempty"`
	Icon      string           `json:"icon,omitempty"`
	Tone      string           `json:"tone,omitempty"`
	Details   []askpass.Detail `json:"details,omitempty"`
	Command   string           `json:"command,omitempty"`
	Dir       string           `json:"dir,omitempty"`
	Projects  []string         `json:"projects,omitempty"`
	CreatedAt time.Time        `json:"createdAt"`
	// Awaited marks an approval somebody waits for in a request, see Await:
	// its end is the caller's answer and never a report.
	Awaited bool `json:"awaited,omitempty"`
	// Outcome is how the approval ended, written onto the entry before its
	// owner hears of it. The entry leaves the register only once the owner
	// heard, so an end the process still owed when it died stands here for
	// the next one to tell again, in the same words, see settle and report.
	Outcome *Outcome `json:"outcome,omitempty"`
}

// Outcome is how an approval ended: the verdict, the run's outcome or the
// reason it never ran, and Quiet where the user's own click decided it and
// the end is no news to them.
type Outcome struct {
	Verdict string `json:"verdict"`
	Text    string `json:"text,omitempty"`
	Quiet   bool   `json:"quiet,omitempty"`
}

// Config wires the service to the rest of the cockpit. Asks reads a kind's
// switch, StopAsking turns it off when the user approves with "don't ask
// again", Asker names the assistant the way the dialog and the notification
// call it, its name and its short id, and Ended hears every end of an
// approval that waited. Started is asked by Recover about an approved action
// the last process died over: where its run started something that keeps a
// record of its own and reports its own end, it answers the outcome the run
// would have written and true. Without it, and where it answers false, the
// end stays the one that says how far the run got is not known.
type Config struct {
	Path       string
	Asks       func(kind string) bool
	StopAsking func(kind string)
	Asker      func(owner string) (name, id string)
	Ended      func(Approval, Outcome)
	Started    func(Approval) (Outcome, bool)
}

// Service is the one place approvals wait. Every end of one goes through
// settle or take, under one lock, so a decision, a declined owner and a
// restart racing each other end in one outcome.
type Service struct {
	cfg    Config
	broker *askpass.Broker
	// leftover are the approvals the last process left on disk, read when
	// the service is built, before anything can ask: Recover declines these
	// and never one asked since.
	leftover []string
	mu       sync.Mutex
}

func New(cfg Config) *Service {
	s := &Service{cfg: cfg}
	for id := range s.load() {
		s.leftover = append(s.leftover, id)
	}
	return s
}

// SetBroker hands over the broker the questions stand in. Without one an
// approval cannot be asked.
func (s *Service) SetBroker(broker *askpass.Broker) {
	s.mu.Lock()
	s.broker = broker
	s.mu.Unlock()
}

// Ask puts an action of the request's kind before the user. With the kind's
// switch off nothing is asked and waiting is false: the caller goes on as if
// no approval existed. Otherwise the approval is parked and the question put
// up; the run executes when the user approves, and Ended hears the end either
// way. The same action already waiting refuses a second, whoever asks, and so
// does an owner with MaxPerOwner approvals waiting.
func (s *Service) Ask(req Request) (waiting bool, err error) {
	if !s.cfg.Asks(req.Kind) {
		return false, nil
	}
	a := entry(req)
	bridge, err := s.park(a, req, Timeout)
	if err != nil {
		return false, err
	}
	go s.await(a, bridge, req)
	return true, nil
}

// Await puts an action before the user like Ask, and waits for the answer in
// the caller's request instead of running anything: the caller acts on it
// itself. With the kind's switch off it answers Approved at once. Nobody
// answering within wait ends it Expired; ctx ending or the owner going away
// ends it Withdrawn, and the question goes with it either way. Nothing of it is
// reported through Ended, the caller is the one who hears how it ended.
func (s *Service) Await(ctx context.Context, req Request, wait time.Duration) (string, error) {
	if !s.cfg.Asks(req.Kind) {
		return Approved, nil
	}
	a := entry(req)
	a.Awaited = true
	bridge, err := s.park(a, req, wait)
	if err != nil {
		return "", err
	}
	expiry := time.AfterFunc(wait, bridge.End)
	stop := context.AfterFunc(ctx, bridge.End)
	decision, decided := bridge.AskApproval()
	expiry.Stop()
	stop()
	bridge.End()
	if decided && decision.Approved && decision.Remember {
		s.remember(req)
	}
	if _, ok := s.take(a.ID); !ok {
		return Withdrawn, nil
	}
	switch {
	case decided && decision.Approved:
		return Approved, nil
	case decided:
		return Declined, nil
	case ctx.Err() != nil:
		return Withdrawn, nil
	default:
		return Expired, nil
	}
}

// entry is the approval a request stands on disk as.
func entry(req Request) Approval {
	return Approval{
		ID:        cmp.Or(req.ID, statefile.NewID()),
		Owner:     req.Owner,
		Kind:      req.Kind,
		Key:       req.Key,
		What:      req.What,
		Title:     req.Title,
		Icon:      req.Icon,
		Tone:      req.Tone,
		Details:   shown(req.Details),
		Command:   req.Command,
		Dir:       req.Dir,
		Projects:  req.Projects,
		CreatedAt: time.Now().UTC(),
	}
}

// remember is what the box under an approved question does.
func (s *Service) remember(req Request) {
	if req.Remember != nil {
		req.Remember()
		return
	}
	s.cfg.StopAsking(req.Kind)
}

// shown are the rows the way the dialog shows them, see ShownValue.
func shown(details []askpass.Detail) []askpass.Detail {
	out := make([]askpass.Detail, 0, len(details))
	for _, d := range details {
		var cut bool
		d.Value, cut = ShownValue(d.Value)
		d.Cut = d.Cut || cut
		out = append(out, d)
	}
	return out
}

// ShownValue is a value the way the dialog shows it: whole, or one beyond
// MaxDetailBytes cut on a character's start, and whether it was cut.
func ShownValue(value string) (string, bool) {
	if len(value) <= MaxDetailBytes {
		return value, false
	}
	end := MaxDetailBytes
	for end > MaxDetailBytes-utf8.UTFMax && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end], true
}

// park registers the approval and puts its bridge up under one lock, so a
// decline that comes after finds both and one that came before finds neither.
// What the last process left is no action waiting any more, Recover declines
// it, so it neither blocks the same action nor counts for its owner. Neither
// does one that is settled and only stands until its owner heard.
func (s *Service) park(a Approval, req Request, wait time.Duration) (*askpass.Action, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.broker == nil {
		return nil, errors.New("The approval could not be asked.")
	}
	list := s.load()
	standing := 0
	for id, other := range list {
		if slices.Contains(s.leftover, id) || other.Outcome != nil {
			continue
		}
		if other.Kind == a.Kind && other.Key == a.Key {
			return nil, fmt.Errorf("%s already waits for the user's approval.", other.What)
		}
		if other.Owner == a.Owner {
			standing++
		}
	}
	if standing >= MaxPerOwner {
		return nil, fmt.Errorf("%d actions already wait for the user's approval.", standing)
	}
	name, id := s.cfg.Asker(a.Owner)
	bridge := s.broker.BeginApproval(askpass.ApprovalKey(a.ID), askpass.Question{
		Assistant:   name,
		AssistantID: id,
		Action:      a.What,
		Title:       a.Title,
		Icon:        a.Icon,
		Tone:        a.Tone,
		Details:     a.Details,
		Command:     a.Command,
		Dir:         a.Dir,
		Remember:    req.RememberLabel,
		WaitMinutes: int(wait.Round(time.Minute) / time.Minute),
	})
	if bridge == nil {
		return nil, errors.New("The approval could not be asked.")
	}
	list[a.ID] = a
	s.save(list)
	return bridge, nil
}

// await waits out the question and settles the approval on what comes back.
// It runs off the request: the command that asked is long answered when the
// user decides.
func (s *Service) await(a Approval, bridge *askpass.Action, req Request) {
	// The end of the bridge is what a timeout looks like: the question leaves
	// the list and nobody decided.
	expiry := time.AfterFunc(Timeout, bridge.End)
	decision, decided := bridge.AskApproval()
	expiry.Stop()
	bridge.End()
	// Don't ask again is about the kind, or whatever the request remembers,
	// not about this approval: it holds even where a decline took the
	// approval away while the box was ticked.
	if decided && decision.Approved && decision.Remember {
		s.remember(req)
	}
	outcome := Outcome{Verdict: Declined, Text: "the approval expired unanswered"}
	switch {
	case decided && decision.Approved:
		// What a process that dies in the middle of the run leaves behind:
		// the action was approved and started, so declined would be wrong,
		// and nobody is left who knows how far it got.
		outcome = Outcome{Verdict: Failed, Text: "the cockpit restarted while it ran, so how far it got is not known"}
	case decided:
		outcome = Outcome{Verdict: Declined, Text: "declined by the user", Quiet: true}
	}
	settled, ok := s.settle(a.ID, outcome, true)
	if ok && decided && decision.Approved {
		settled, ok = s.settle(a.ID, execute(req.Run), false)
	}
	if ok {
		s.report(settled)
	}
}

// execute runs an approved action. The user just approved it, so an outcome
// that went through is no news to them, one that failed is.
func execute(run Run) Outcome {
	text, err := run()
	if err != nil {
		return Outcome{Verdict: Failed, Text: err.Error()}
	}
	return Outcome{Verdict: Done, Text: text, Quiet: true}
}

// DeclineOwner takes down the approvals of an assistant that is going away,
// questions included. Nobody is left to hear how they ended.
func (s *Service) DeclineOwner(owner string) {
	s.mu.Lock()
	var ids []string
	for id, a := range s.load() {
		if a.Owner == owner {
			ids = append(ids, id)
		}
	}
	s.mu.Unlock()
	for _, id := range ids {
		s.take(id)
	}
}

// DeclineProject takes down the approvals touching a project that was
// deleted, questions included. Nothing is left for them to act on, and each owner
// hears why.
func (s *Service) DeclineProject(name string) {
	if name == "" {
		return
	}
	s.mu.Lock()
	var ids []string
	for id, a := range s.load() {
		if slices.Contains(a.Projects, name) {
			ids = append(ids, id)
		}
	}
	s.mu.Unlock()
	for _, id := range ids {
		if a, ok := s.settle(id, Outcome{Verdict: Declined, Text: "the project was deleted"}, true); ok {
			s.report(a)
		}
	}
}

// Recover declines the approvals the last process left waiting: their
// question and their run lived in that process, and nobody can answer them
// any more. Each owner hears why, except of an awaited one: its caller heard
// it when the request broke off. One that process had settled is reported
// with the end it wrote down: whether its owner heard it is not known, and
// that is the owner's to tell apart. An approval asked since the service was
// built is this process's own and keeps waiting, however late this runs.
//
// An end that says the action failed may be the one written before its run,
// for a process that dies in the middle of it, see await. The run may have
// started all the same, and one that reports its own end would then end
// twice, once here as failed and once in its own words. So the kind is asked
// first, see Config.Started, and where the run started the approval ends the
// way it would have, and the run's own end is the only one.
func (s *Service) Recover() {
	s.mu.Lock()
	leftover := s.leftover
	s.leftover = nil
	list := s.load()
	s.mu.Unlock()
	for _, id := range leftover {
		a, ok := list[id]
		switch {
		case !ok || a.Awaited:
		case a.Outcome == nil:
			a, ok = s.settle(id, Outcome{Verdict: Declined, Text: "the approval was lost in a restart"}, true)
		case a.Outcome.Verdict == Failed && s.cfg.Started != nil:
			if outcome, started := s.cfg.Started(a); started {
				a, ok = s.settle(id, outcome, false)
			}
		}
		switch {
		case !ok:
		case a.Awaited:
			s.take(id)
		default:
			s.report(a)
		}
	}
}

// settle writes how an approval ended onto its entry and answers it, false
// when the entry is no longer there. waiting asks for one that still waits,
// which is what makes the first end the only one: an approval somebody else
// settled is refused. An approved run settles twice, before it starts and
// with its own outcome after, see await. The question goes with the first
// end, so none ever stands without an approval waiting behind it.
func (s *Service) settle(id string, outcome Outcome, waiting bool) (Approval, bool) {
	s.mu.Lock()
	list := s.load()
	a, ok := list[id]
	if ok = ok && (a.Outcome == nil || !waiting); ok {
		a.Outcome = &outcome
		list[id] = a
		s.save(list)
	}
	s.mu.Unlock()
	s.unask(id)
	return a, ok
}

// report tells the owner how a settled approval ended and only then lets the
// entry go. A process that dies between the two leaves the entry with its
// end, and Recover reports it again: whoever hears it took it by then or
// takes it now, and knows the one from the other by the approval's id.
func (s *Service) report(a Approval) {
	s.cfg.Ended(a, *a.Outcome)
	s.take(a.ID)
}

// take moves one approval out of the register and answers it, false when it
// is no longer there: somebody else took it first. Its question goes with
// it, so no question ever stands without an approval behind it.
func (s *Service) take(id string) (Approval, bool) {
	s.mu.Lock()
	list := s.load()
	a, ok := list[id]
	if ok {
		delete(list, id)
		s.save(list)
	}
	s.mu.Unlock()
	s.unask(id)
	return a, ok
}

// unask takes down the question of an approval, if one still stands.
func (s *Service) unask(id string) {
	s.mu.Lock()
	broker := s.broker
	s.mu.Unlock()
	if broker != nil {
		if bridge := broker.Find(askpass.ApprovalKey(id)); bridge != nil {
			bridge.End()
		}
	}
}

func (s *Service) load() map[string]Approval {
	list := map[string]Approval{}
	statefile.Load(s.cfg.Path, &list)
	return list
}

func (s *Service) save(list map[string]Approval) {
	statefile.Save(s.cfg.Path, 0o600, list)
}
