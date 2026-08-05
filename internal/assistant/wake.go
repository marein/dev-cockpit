package assistant

import (
	"errors"
	"log"
	"strings"
	"time"

	"github.com/local/dev-cockpit/internal/statefile"
	"github.com/local/dev-cockpit/internal/terminal"
)

// wakeTimeout bounds one check. Ten minutes was the first guess, from a check
// that reads a terminal and answers in a few sentences. A criterion worth having
// is often not answerable that way: "the tests pass" means running them, and a
// suite plus an e2e pass takes longer than ten minutes, so the limit quietly won
// and the job stood still. It is two hours now, room for a real build and test
// pass, and the prompt tells a check to report what it knows instead of working
// up to the limit. Whatever happens, the limit is never silent: a turn that
// runs into it says so (see Service.read), and the watcher writes that on the
// job. The limit is an absolute point in time in the register, so a restart
// does not hand a check another two hours.
const wakeTimeout = 2 * time.Hour

// wakeSpec is one check the watcher asks for.
type wakeSpec struct {
	Terminal string
	Prompt   string
	// Context is what the watcher saw before the check started. It travels into
	// the register with the turn, so the answer is judged the same way whether
	// or not the cockpit restarted while the check ran.
	Context checkContext
}

// checkContext is the state a verdict is read against: the message the report
// will be written as, which job the check was started for, whether the coder
// stood still, and when the assistant last wrote to that terminal.
type checkContext struct {
	MessageID string `json:"messageId,omitempty"`
	// JobCreatedAt identifies the job this check belongs to, the way MessageID
	// identifies its report. A terminal can be steered again while a check
	// runs; the store keys jobs by terminal, so without this a late answer
	// would land on the successor: close it, spend one of its checks, write
	// the old job's note on it. A context without it carries no identity and
	// counts as it always did.
	JobCreatedAt time.Time `json:"jobCreatedAt,omitempty"`
	Idle         bool      `json:"idle,omitempty"`
	SteeredAt    time.Time `json:"steeredAt,omitempty"`
}

// forJob reports whether a stored entry is still the job this check was
// started for.
func (c checkContext) forJob(job Job) bool {
	return c.JobCreatedAt.IsZero() || job.CreatedAt.Equal(c.JobCreatedAt)
}

// wakeOutcome is what the check concluded. Whether the user hears about it is
// the watcher's decision, not this one's.
type wakeOutcome struct {
	Verdict Verdict
	Text    string
}

// startWake spends one turn checking on a steered coder. It is deliberately not
// a chat turn:
//
//   - it runs in a provider session of its own, so the conversation's session
//     stays free for the user and the check does not drag the whole chat history
//     along, which is what a check would cost otherwise,
//   - it holds a wake slot, never a chat slot,
//   - it writes nothing anywhere. What the answer means for the user is decided
//     by the watcher, which knows the job.
//
// Like a chat turn it is a detached process with an output file of its own, so a
// restart in the middle costs nothing: the check keeps running and whoever comes
// back reads its verdict out of the file.
func (s *Service) startWake(spec wakeSpec) (*activeRun, error) {
	c, err := s.Open("")
	if err != nil {
		return nil, err
	}
	co, ok := s.coder(c.CoderID)
	if !ok {
		return nil, errors.New("The coder of this conversation is not available right now.")
	}
	if strings.TrimSpace(spec.Prompt) == "" {
		return nil, errors.New("A check needs a prompt.")
	}

	// A session of its own, kept out of the coder lists while it exists and
	// removed when the check is over: a wake leaves no resumable ghost behind.
	sessionID, err := terminal.NewKey()
	if err != nil {
		return nil, errors.New("The check could not be started.")
	}
	s.reserve(c.CoderID, sessionID)

	a, err := s.launch(co.Runner, TurnRequest{
		SessionID: sessionID,
		Title:     wakeSessionName(c.Title),
		Workdir:   c.ProjectPath,
		Prompt:    spec.Prompt,
	}, RunRecord{
		ID:   statefile.NewID(),
		Kind: RunCheck,
		// The report this check may write carries its id from here, so a check
		// that is concluded twice still writes exactly one message.
		MessageID: statefile.NewID(),
		CoderID:   c.CoderID,
		SessionID: sessionID,

		Terminal: spec.Terminal,
		Context:  spec.Context,
		Deadline: s.now().UTC().Add(wakeTimeout),
	})
	if err != nil {
		s.mu.Lock()
		s.dropSessionLocked(Conversation{Summary: Summary{CoderID: c.CoderID}, NativeSessionID: sessionID})
		s.mu.Unlock()
		return nil, err
	}

	s.mu.Lock()
	s.running[a.rec.ID] = a
	s.mu.Unlock()

	go s.follow(a, co.Runner)
	return a, nil
}

// awaitWake blocks until a check ended and reports what it concluded.
func (s *Service) awaitWake(a *activeRun) (wakeOutcome, error) {
	<-a.done
	return a.outcome, a.err
}

// killChecks ends every running check on one terminal, started here or
// adopted after a restart: both stand in the running map. A release and a job
// running out take the actor away, so the process dies the way the deadline
// kills it, a dead check writes nothing any more, and an answer nobody wants
// is not paid to its end. The stop is written down before the kill, exactly
// like Cancel, so a restart in between still reads it as a stop; whatever a
// killed check still delivers is dropped by conclude, the second belt.
func (s *Service) killChecks(terminal string) {
	s.mu.Lock()
	var doomed []*activeRun
	for _, a := range s.running {
		if a.rec.Kind == RunCheck && a.rec.Terminal == terminal {
			doomed = append(doomed, a)
		}
	}
	s.mu.Unlock()
	for _, a := range doomed {
		a.cancelled.Store(true)
		s.runs.Update(a.rec.ID, func(rec *RunRecord) { rec.Cancelled = true })
		a.proc.Kill()
	}
}

// recordWake writes the report of a check into the live conversation and
// announces it: one message that is marked as a check, never a user message,
// plus the cockpit's usual news so the phone rings. Returns the message id.
//
// The report goes where the user is, not where the job started. A job outlives
// the conversation it was asked for, and writing into that one would put the
// answer into a transcript the user has already left behind.
//
// The id comes from the check's register entry, so concluding the same check
// twice writes one message and not two. A report that belongs to no check (a job
// that ran out) gets a fresh one.
func (s *Service) recordWake(job Job, messageID string, verdict Verdict, text string) string {
	// Resolved before the lock: opening a conversation takes it.
	target, err := s.Open("")
	if err != nil {
		log.Printf("assistant: no conversation for the report on %s: %v", job.Terminal, err)
		return ""
	}

	s.mu.Lock()
	c, ok := s.store.Load(target.ID)
	if !ok {
		s.mu.Unlock()
		return ""
	}
	if messageID == "" {
		messageID = statefile.NewID()
	}
	for _, existing := range c.Messages {
		if existing.ID == messageID {
			s.mu.Unlock()
			return messageID
		}
	}
	now := s.now().UTC()
	message := Message{
		ID:        messageID,
		Role:      RoleAssistant,
		Content:   text,
		CreatedAt: now,
		State:     StateComplete,
		Wake: &WakeNote{
			Terminal: job.Terminal,
			// The job's name and project travel with the report, so whoever
			// reads it later says which job this was without asking the store,
			// see WakeNote.
			Name:    strings.TrimSpace(job.Name),
			Project: strings.TrimSpace(job.Project),
			Verdict: string(verdict),
		},
	}
	c.Messages = append(c.Messages, message)
	c.UpdatedAt = now
	s.store.Save(c)
	s.mu.Unlock()

	// A frame of its own: the page pulls this one message and appends it,
	// without touching a chat answer that may be streaming at the same time.
	s.hub.publish(c.ID, StreamEvent{Kind: FrameMessage, MessageID: message.ID})
	s.changed()
	if s.onDone != nil {
		s.onDone(c.ID)
	}
	return message.ID
}

// checkSessionPrefix marks the provider session of a check. A check drops its
// session when it is over, but a process that is killed mid check never gets
// there, and the session it reserved becomes a resumable ghost as soon as the
// reservation dies with the process. The name is what survives, so it is the
// one thing that can identify such a leftover afterwards.
const checkSessionPrefix = "cockpit check: "

// wakeSessionName names the provider session of a check, so a stray session is
// recognizable in a provider's own list.
func wakeSessionName(title string) string {
	return SessionName(checkSessionPrefix + strings.TrimSpace(title))
}

// IsCheckSession reports whether a stored provider session was a check's own.
// Used at startup to sweep what a hard restart left behind: nothing running
// answers to these names, because a live check keeps its session reserved and
// invisible for as long as it runs.
func IsCheckSession(name string) bool {
	return strings.HasPrefix(strings.TrimSpace(name), strings.TrimSpace(checkSessionPrefix))
}
