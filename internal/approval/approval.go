// Package approval holds the actions an assistant may only take once the user
// said yes: a compose command that asks first, a project delete. An approval
// is one such action waiting: who asked, what kind of action it is, the rows
// the dialog shows, and the run that does it once approved.
//
// The question travels through the askpass broker, so every signed-in page
// shows it and the push channels carry it to a phone. Whatever the user
// answers, the owner hears how it ended through one hook, Config.Ended. A
// kind hands in a Request and nothing else: its key, its line, its rows and
// its run; this package knows no kind by name. The run lives in this process
// alone, the entry on disk exists for one reason, so the next process can tell
// the owners whose question a restart took.
package approval

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

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

// The verdicts an approval ends in: approved and the run went through,
// approved and the run failed, or never run.
const (
	Done     = "done"
	Failed   = "failed"
	Declined = "declined"
)

// Run is what an approved action does. It answers the outcome the owner
// reads, or the error that says why it did not go through. It runs long after
// the ask, so it resolves what it acts on again rather than trusting what
// stood when it was asked.
type Run func() (string, error)

// Request is one action an assistant asks for, as its kind hands it in. Key
// is the action's stable identity within its kind, the same Kind and Key wait
// once whoever asks; What is the one line it is read by ("Delete project
// shop"), the notification and the note name it; Details are the rows the
// dialog shows; Command and Dir, where the action runs a program, are the
// command line and the directory it runs in, shown the way a proxied git
// call's are; Projects, where the action touches any, name the projects
// whose deletion ends the approval; Run is what happens once the user
// approves.
type Request struct {
	Owner    string
	Kind     string
	Key      string
	What     string
	Details  []askpass.Detail
	Command  string
	Dir      string
	Projects []string
	Run      Run
}

// Approval is one action waiting for the user, as it stands on disk: the
// request without its run.
type Approval struct {
	ID        string           `json:"id"`
	Owner     string           `json:"owner"`
	Kind      string           `json:"kind"`
	Key       string           `json:"key"`
	What      string           `json:"what"`
	Details   []askpass.Detail `json:"details,omitempty"`
	Command   string           `json:"command,omitempty"`
	Dir       string           `json:"dir,omitempty"`
	Projects  []string         `json:"projects,omitempty"`
	CreatedAt time.Time        `json:"createdAt"`
}

// Outcome is how an approval ended: the verdict, the run's outcome or the
// reason it never ran, and Quiet where the user's own click decided it and
// the end is no news to them.
type Outcome struct {
	Verdict string
	Text    string
	Quiet   bool
}

// Config wires the service to the rest of the cockpit. Asks reads a kind's
// switch, StopAsking turns it off when the user approves with "don't ask
// again", Asker names the assistant the way the dialog and the notification
// call it, and Ended hears every end of an approval that waited.
type Config struct {
	Path       string
	Asks       func(kind string) bool
	StopAsking func(kind string)
	Asker      func(owner string) string
	Ended      func(Approval, Outcome)
}

// Service is the one place approvals wait. Every move out of the register
// goes through take, under one lock, so a decision, a declined owner and a
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
	a := Approval{
		ID:        statefile.NewID(),
		Owner:     req.Owner,
		Kind:      req.Kind,
		Key:       req.Key,
		What:      req.What,
		Details:   req.Details,
		Command:   req.Command,
		Dir:       req.Dir,
		Projects:  req.Projects,
		CreatedAt: time.Now().UTC(),
	}
	bridge, err := s.park(a)
	if err != nil {
		return false, err
	}
	go s.await(a, bridge, req.Run)
	return true, nil
}

// park registers the approval and puts its bridge up under one lock, so a
// decline that comes after finds both and one that came before finds neither.
// What the last process left is no action waiting any more, Recover declines
// it, so it neither blocks the same action nor counts for its owner.
func (s *Service) park(a Approval) (*askpass.Action, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.broker == nil {
		return nil, errors.New("The approval could not be asked.")
	}
	list := s.load()
	standing := 0
	for id, other := range list {
		if slices.Contains(s.leftover, id) {
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
	bridge := s.broker.BeginApproval(askpass.ApprovalKey(a.ID), askpass.Question{
		Assistant: s.cfg.Asker(a.Owner),
		Action:    a.What,
		Details:   a.Details,
		Command:   a.Command,
		Dir:       a.Dir,
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
func (s *Service) await(a Approval, bridge *askpass.Action, run Run) {
	// The end of the bridge is what a timeout looks like: the question leaves
	// the list and nobody decided.
	expiry := time.AfterFunc(Timeout, bridge.End)
	decision, decided := bridge.AskApproval()
	expiry.Stop()
	bridge.End()
	// Don't ask again is about the kind, not about this approval: it holds
	// even where a decline took the approval away while the box was ticked.
	if decided && decision.Approved && decision.Remember {
		s.cfg.StopAsking(a.Kind)
	}
	if _, ok := s.take(a.ID); !ok {
		return
	}
	switch {
	case decided && decision.Approved:
		s.cfg.Ended(a, execute(run))
	case decided:
		s.cfg.Ended(a, Outcome{Verdict: Declined, Text: "declined by the user", Quiet: true})
	default:
		s.cfg.Ended(a, Outcome{Verdict: Declined, Text: "the approval expired unanswered"})
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
		if a, ok := s.take(id); ok {
			s.cfg.Ended(a, Outcome{Verdict: Declined, Text: "the project was deleted"})
		}
	}
}

// Recover declines the approvals the last process left waiting: their
// question and their run lived in that process, and nobody can answer them
// any more. Each owner hears why. An approval asked since the service was
// built is this process's own and keeps waiting, however late this runs.
func (s *Service) Recover() {
	s.mu.Lock()
	leftover := s.leftover
	s.leftover = nil
	s.mu.Unlock()
	for _, id := range leftover {
		if a, ok := s.take(id); ok {
			s.cfg.Ended(a, Outcome{Verdict: Declined, Text: "the approval was lost in a restart"})
		}
	}
}

// take moves one approval out of the register and answers it, false when it
// is no longer there: somebody else settled it first. Its question goes with
// it, so no question ever stands without an approval behind it.
func (s *Service) take(id string) (Approval, bool) {
	s.mu.Lock()
	list := s.load()
	a, ok := list[id]
	if ok {
		delete(list, id)
		s.save(list)
	}
	broker := s.broker
	s.mu.Unlock()
	if broker != nil {
		if bridge := broker.Find(askpass.ApprovalKey(id)); bridge != nil {
			bridge.End()
		}
	}
	return a, ok
}

func (s *Service) load() map[string]Approval {
	list := map[string]Approval{}
	statefile.Load(s.cfg.Path, &list)
	return list
}

func (s *Service) save(list map[string]Approval) {
	statefile.Save(s.cfg.Path, 0o600, list)
}
