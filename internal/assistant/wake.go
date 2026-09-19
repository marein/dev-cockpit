package assistant

import (
	"errors"
	"log"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/statefile"
	"github.com/marein/dev-cockpit/internal/terminal"
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
	// Owner is the assistant whose job this is: the one the check acts as and
	// the one its report is written into.
	Owner    string
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
//   - it runs in a provider session of its own, so the instance's session
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
	if strings.TrimSpace(spec.Prompt) == "" {
		return nil, errors.New("A check needs a prompt.")
	}
	return s.startOwnSession(spec.Owner, spec.Prompt, wakeSessionName, RunRecord{
		Kind: RunCheck,
		// The report this check may write carries its id from here, so a check
		// that is concluded twice still writes exactly one message.
		MessageID: statefile.NewID(),
		Terminal:  spec.Terminal,
		Context:   spec.Context,
	})
}

// startReaction spends one turn on an event a subscription fired, the way a
// check is spent: a provider session of its own, the wake slot, the register.
// The reaction sees the owner's instruction file, the memory and the
// workspace files, and nothing of the conversation; what its answer means
// for the thread is the reactor's decision, and the id that answer is pushed
// under is reserved here so a restart pushes it once.
func (s *Service) startReaction(owner string, origin Note, prompt string) (*activeRun, error) {
	if strings.TrimSpace(prompt) == "" {
		return nil, errors.New("A reaction needs a prompt.")
	}
	return s.startOwnSession(owner, prompt, reactionSessionName, RunRecord{
		Kind:      RunReaction,
		MessageID: statefile.NewID(),
		Origin:    &origin,
	})
}

// startOwnSession runs one turn for an assistant in a provider session of its
// own, deliberately not a chat turn:
//
//   - it runs in a session of its own, so the instance's session stays free
//     for the user and the turn does not drag the whole chat history along,
//   - it holds a wake slot, never a chat slot,
//   - it writes nothing anywhere. What the answer means is the caller's
//     decision, the watcher's for a check and the reactor's for a reaction.
//
// Like a chat turn it is a detached process with an output file of its own,
// so a restart in the middle costs nothing: the turn keeps running and whoever
// comes back reads its answer out of the file. The session is kept out of the
// coder lists while it exists and removed when the turn is over, so it leaves
// no resumable ghost behind. It runs in the workspace of the assistant it
// belongs to, so its instructions say who it acts as.
func (s *Service) startOwnSession(owner, prompt string, name func(string) string, rec RunRecord) (*activeRun, error) {
	c, err := s.Get(owner)
	if err != nil {
		return nil, err
	}
	co, ok := s.coder(c.CoderID)
	if !ok {
		return nil, errors.New("The coder of this assistant is not available right now.")
	}
	workdir, err := s.workdirs.Workdir(c.ID)
	if err != nil {
		return nil, err
	}
	sessionID, err := terminal.NewKey()
	if err != nil {
		return nil, errors.New("The turn could not be started.")
	}
	s.reserve(c.CoderID, sessionID)

	rec.ID = statefile.NewID()
	rec.Instance = c.ID
	rec.CoderID = c.CoderID
	rec.SessionID = sessionID
	rec.Deadline = s.now().UTC().Add(wakeTimeout)
	a := &activeRun{rec: rec, done: make(chan struct{})}
	p, err := s.launch(&a.rec, co.Runner, TurnRequest{
		Instance:  c.ID,
		SessionID: sessionID,
		Title:     name(c.Title),
		Workdir:   workdir,
		Prompt:    prompt,
	})
	if err != nil {
		s.mu.Lock()
		s.dropSessionLocked(Instance{Summary: Summary{CoderID: c.CoderID}, NativeSessionID: sessionID})
		s.mu.Unlock()
		return nil, err
	}

	s.mu.Lock()
	a.proc = p
	a.launched = true
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

// recordWake writes the report of a check into the transcript of the assistant
// that steers the job, and announces it: one message that is marked as a check,
// never a user message, plus the cockpit's usual news so the phone rings.
// Returns the message id.
//
// The report goes to the owner, and only to the owner. A job is a standing
// arrangement one assistant made, so the answer belongs in that conversation
// and nowhere else: written into whichever assistant somebody happened to be
// looking at, a report would arrive in a thread that never asked for it, with
// nothing in that thread to make sense of it.
//
// The id comes from the check's register entry, so concluding the same check
// twice writes one message and not two. A report that belongs to no check (a job
// that ran out) gets a fresh one.
func (s *Service) recordWake(job Job, messageID string, verdict Verdict, text string) string {
	s.mu.Lock()
	c, ok := s.store.Load(job.Owner)
	if !ok {
		s.mu.Unlock()
		log.Printf("assistant: the assistant of the job on %s is gone, dropping its report", job.Terminal)
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
	name := strings.TrimSpace(job.Name)
	project := strings.TrimSpace(job.Project)
	message := Message{
		ID:        messageID,
		Role:      RoleCockpit,
		Content:   text,
		CreatedAt: now,
		State:     StateComplete,
		// A report is a note, the first source there was: the cockpit says
		// what a check concluded. The job's name and project travel with it,
		// so whoever reads it later says which job this was without asking
		// the store, see Note.
		Note: &Note{
			Source:   NoteCheck,
			Headline: checkHeadline(string(verdict), name, job.Terminal),
			Verdict:  string(verdict),
			Terminal: job.Terminal,
			Name:     name,
			Project:  project,
		},
		// The shape the release before notes read a report in, written
		// alongside for one more release so a binary rolled back to it still
		// shows the report as a check's. TODO(v2.0.0): drop with WakeNote.
		Wake: &WakeNote{Terminal: job.Terminal, Name: name, Project: project, Verdict: string(verdict)},
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
	// The report is also an event: a subscription on this job's end fires
	// on it, in the owner's own thread.
	s.publishEvent(CockpitEvent{
		Source:   EventJob,
		Kind:     string(verdict),
		Target:   job.Terminal,
		Owner:    job.Owner,
		Time:     now,
		Headline: "Job " + strings.ToLower(string(verdict)) + ": " + noteName(name, job.Terminal) + inProject(project),
		Body:     text,
	})
	return message.ID
}

// checkSessionPrefix marks the provider session of a check, and
// reactionSessionPrefix that of a reaction. Such a turn drops its session when
// it is over, but a process that is killed mid turn never gets there, and the
// session it reserved becomes a resumable ghost as soon as the reservation
// dies with the process. The name is what survives, so it is the one thing
// that can identify such a leftover afterwards.
const (
	checkSessionPrefix    = "cockpit check: "
	reactionSessionPrefix = "cockpit reaction: "
)

// wakeSessionName names the provider session of a check, so a stray session is
// recognizable in a provider's own list; reactionSessionName that of a
// reaction.
func wakeSessionName(title string) string {
	return SessionName(checkSessionPrefix + strings.TrimSpace(title))
}

func reactionSessionName(title string) string {
	return SessionName(reactionSessionPrefix + strings.TrimSpace(title))
}

// IsCheckSession reports whether a stored provider session was a check's or a
// reaction's own. Used at startup to sweep what a hard restart left behind:
// nothing running answers to these names, because a live one keeps its
// session reserved and invisible for as long as it runs.
func IsCheckSession(name string) bool {
	name = strings.TrimSpace(name)
	return strings.HasPrefix(name, strings.TrimSpace(checkSessionPrefix)) || strings.HasPrefix(name, strings.TrimSpace(reactionSessionPrefix))
}
