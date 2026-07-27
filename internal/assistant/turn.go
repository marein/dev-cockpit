package assistant

import (
	"errors"
	"log"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// progressInterval is how often the register records how far the output file
// was read. It is a fixed cost per turn, not one that grows with the answer:
// the answer itself is already on disk, this only says how much of it was
// turned into events.
const progressInterval = 5 * time.Second

// activeRun is a turn this server is following. It is a view on the register
// entry, not a second copy of the truth: everything a later process would need
// is in the entry, and what lives here is only what following costs.
type activeRun struct {
	rec  RunRecord
	proc process
	done chan struct{}
	// cancelled is set by a stop and read when the turn ends, so a killed
	// process is reported as a stop and not as a coder that fell over.
	cancelled atomic.Bool
	// orphaned marks a turn whose process was already gone when this server
	// picked it up. Its answer is whatever the file holds, and an incomplete one
	// is an interrupted turn, not a failing coder.
	orphaned bool

	// outcome and err are what a check concluded, valid once done is closed.
	outcome wakeOutcome
	err     error
}

// slots bound how many turns run at once. It is a counter and not a channel
// because a restart has to be able to take slots for turns that were already
// running: the limit governs what may start, it can never refuse a turn that is
// already on the machine.
type slots struct {
	mu    sync.Mutex
	limit int
	held  int
}

func newSlots(limit int) *slots { return &slots{limit: limit} }

// take reserves a slot, or reports that the limit is reached.
func (s *slots) take() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.held >= s.limit {
		return false
	}
	s.held++
	return true
}

// adopt takes a slot for a turn that is already running, past the limit if it
// has to. More turns than the limit can only happen after a restart, and it
// settles by itself as they end.
func (s *slots) adopt() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.held++
}

func (s *slots) release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.held > 0 {
		s.held--
	}
}

// launch starts one turn and writes it into the register. From this moment the
// turn exists outside this process: the entry and the file it points at are
// enough for any later server to finish it.
func (s *Service) launch(runner Runner, req TurnRequest, rec RunRecord) (*activeRun, error) {
	cmd, err := runner.Command(req)
	if err != nil {
		return nil, err
	}
	out, errs, lock, err := s.runs.Files(rec.ID)
	if err != nil {
		log.Printf("assistant: no place for the output of turn %s: %v", rec.ID, err)
		return nil, errors.New("The coder could not be started.")
	}
	rec.Output, rec.Errors, rec.Lock = out, errs, lock
	rec.StartedAt = s.now().UTC()
	p, err := start(cmd, req.Workdir, out, errs, lock)
	if err != nil {
		remove(out)
		remove(errs)
		remove(lock)
		return nil, err
	}
	rec.PID = p.pid
	s.runs.Save(rec)
	return &activeRun{rec: rec, proc: p, done: make(chan struct{})}, nil
}

// follow reads one turn to its end and settles it. It is the same code for a
// turn this server started and for one it found in the register, which is the
// point: a turn has no idea which process is watching it.
func (s *Service) follow(a *activeRun, runner Runner) {
	defer close(a.done)
	text, usage, turnErr := s.read(a, runner)
	switch a.rec.Kind {
	case RunCheck:
		s.settleCheck(a, text, turnErr)
	default:
		// The context reading only travels with a chat turn. Every other kind
		// runs in a provider session of its own that nothing continues, a
		// check's above, so what it consumed says nothing about the
		// conversation it reports into and would overwrite the chat's own
		// number with a stranger's.
		if a.rec.Kind != RunChat {
			usage = nil
		}
		s.settleChat(a, text, usage, turnErr)
	}
}

// read turns the raw output of a turn into the conversation's stream and
// returns the answer, the context reading the turn reported, and whatever went
// wrong.
func (s *Service) read(a *activeRun, runner Runner) (string, *ContextUsage, error) {
	events := make(chan Event, eventBuffer)
	parser := runner.Parse(a.rec.SessionID, events)

	var readErr error
	go func() {
		defer close(events)
		err := tail(a.rec.Output, a.proc.alive, parser.Line, s.progress(a))
		if err == nil {
			err = parser.Finish()
		}
		readErr = err
	}()

	var (
		buf       strings.Builder
		turnErr   error
		usage     *ContextUsage
		renderAt  time.Time
		renderLen int
		expired   <-chan time.Time
	)
	// A dead process cannot run past its limit: its output is finite, and
	// reading it to the end is all that is left. An armed timer would race
	// that reading, and a check that concluded during a long downtime would
	// report a timeout instead of the verdict sitting in its file.
	if !a.rec.Deadline.IsZero() && !a.orphaned {
		timer := time.NewTimer(time.Until(a.rec.Deadline))
		defer timer.Stop()
		expired = timer.C
	}
	live := a.rec.Kind == RunChat

	// publishRender sends the answer so far as HTML. Markdown only survives
	// being cut mid document when something re-renders the whole prefix, so
	// this is where the formatting comes from while the text streams.
	publishRender := func() {
		if s.render == nil || !live {
			return
		}
		text := buf.String()
		if text == "" || len(text) == renderLen || len(text) > maxRenderBytes {
			return
		}
		if !renderAt.IsZero() && time.Since(renderAt) < renderInterval(len(text)) {
			return
		}
		html, err := s.render(text)
		if err != nil {
			return
		}
		renderAt = time.Now()
		renderLen = len(text)
		s.hub.publish(a.rec.Conversation, StreamEvent{Kind: FrameHTML, RunID: a.rec.ID, MessageID: a.rec.MessageID, HTML: html})
	}

	for events != nil {
		select {
		case ev, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			switch ev.Kind {
			case EventDelta:
				if buf.Len()+len(ev.Text) > MaxResponseBytes {
					if turnErr == nil {
						turnErr = errors.New("The answer grew past the size this conversation can hold. The part received so far is kept.")
						a.proc.kill()
					}
					continue
				}
				buf.WriteString(ev.Text)
				if live {
					s.hub.publish(a.rec.Conversation, StreamEvent{Kind: FrameDelta, RunID: a.rec.ID, MessageID: a.rec.MessageID, Text: ev.Text})
					publishRender()
				}
			case EventTool:
				if live {
					s.hub.publish(a.rec.Conversation, StreamEvent{Kind: FrameTool, RunID: a.rec.ID, MessageID: a.rec.MessageID, Text: ev.Text})
				}
			case EventUsage:
				// Nothing goes out while the answer streams: the number moves
				// once per turn, and the end frame is where it belongs.
				if ev.Usage != nil {
					reading := *ev.Usage
					usage = &reading
				}
			case EventError:
				if turnErr == nil && ev.Err != nil {
					turnErr = ev.Err
				}
			}
		case <-expired:
			// The turn ran into its own time limit. That is a result, not
			// silence: the process is killed, the answer is a fragment, and a
			// caller that hears nothing would read it as nothing to report.
			expired = nil
			if turnErr == nil {
				turnErr = errors.New("The turn hit its time limit before the coder answered.")
			}
			a.proc.kill()
		}
	}
	if turnErr == nil {
		turnErr = readErr
	}
	if turnErr != nil {
		tail := stderrTail(a.rec.Errors)
		if tail != "" {
			log.Printf("assistant: turn %s ended with %v (stderr tail: %s)", a.rec.ID, turnErr, tail)
		}
		// Naming the failure belongs to the parser: it is the one that saw the
		// output, and where a CLI says what went wrong differs per CLI, one puts
		// it in a record on standard output, the next only on standard error. So
		// it is asked on every failed turn and not only on one that wrote to
		// standard error, and the tail travels with the question.
		if named := parser.Diagnose(turnErr, tail); named != nil {
			turnErr = named
		}
	}
	return buf.String(), usage, turnErr
}

// progress records how far the output file was read. Every few seconds, because
// the value only has to be close enough to catch a file that was replaced under
// a running turn.
func (s *Service) progress(a *activeRun) func(int64) {
	last := time.Now()
	id := a.rec.ID
	return func(consumed int64) {
		if time.Since(last) < progressInterval {
			return
		}
		last = time.Now()
		s.runs.Update(id, func(rec *RunRecord) { rec.Processed = consumed })
	}
}

// turnState decides what a finished turn is. A stop wins over everything: the
// process was killed on purpose and whatever it said about that is noise. A turn
// whose process was gone before this server ever looked at it is interrupted,
// not failed, because nothing about it went wrong, it only lost its server.
func (a *activeRun) turnState(turnErr error) (State, string) {
	switch {
	case a.cancelled.Load():
		return StateCancelled, "You stopped this answer."
	case turnErr == nil:
		return StateComplete, ""
	case a.orphaned:
		return StateInterrupted, "This answer was interrupted when the server restarted."
	default:
		return StateFailed, sanitizeError(turnErr)
	}
}

// settleChat records the terminal state of a chat turn and lets everything go
// that belonged to it. Writing into the message is keyed on the run, so applying
// the same ending twice changes nothing.
func (s *Service) settleChat(a *activeRun, text string, usage *ContextUsage, turnErr error) {
	state, message := a.turnState(turnErr)

	s.mu.Lock()
	c, ok := s.store.Load(a.rec.Conversation)
	if ok {
		for i := range c.Messages {
			if c.Messages[i].ID != a.rec.MessageID || c.Messages[i].RunID != a.rec.ID {
				continue
			}
			c.Messages[i].Content = text
			c.Messages[i].State = state
			c.Messages[i].Error = message
			// The reading belongs to the conversation this turn ran in, so it is
			// written where the answer is written and only when the turn really
			// reported one: a failed turn leaves the last known number standing
			// rather than clearing the ring. Only a chat turn gets this far, and
			// that is what makes the number the chat's own: everything else runs
			// in a provider session of its own, so its context is not this
			// conversation's, see the kind filter in follow.
			if usage != nil {
				reading := *usage
				c.Context = &reading
			}
			c.UpdatedAt = s.now().UTC()
			s.store.Save(c)
			break
		}
		// A new conversation was started while this one was still answering:
		// the archive left its provider session alone for exactly this moment.
		if c.Status == StatusArchived {
			s.dropSessionLocked(c)
		}
	}
	s.mu.Unlock()

	// The end frame goes out before the turn stops counting as running, so
	// nothing can see an idle conversation while its stream still says an
	// answer is on the way.
	context := 0
	if usage != nil {
		context = usage.Percent()
	}
	s.hub.publish(a.rec.Conversation, StreamEvent{
		Kind:      FrameEnd,
		RunID:     a.rec.ID,
		MessageID: a.rec.MessageID,
		State:     string(state),
		Error:     message,
		Context:   context,
	})

	// The entry goes before the turn stops counting as running. A turn that is
	// over has to be over in every way at once: the other order says the answer
	// is settled while the register still holds the turn and its raw output,
	// and whoever looks in that moment, the recovery of a server starting right
	// then or a test that waits for the turn to end, sees a finished answer
	// with a running turn behind it.
	s.retire(a)

	s.mu.Lock()
	delete(s.running, a.rec.ID)
	s.mu.Unlock()

	s.chatSlots.release()
	s.changed()
	// A finished answer is news, and so is one that died on the way: the whole
	// point of asking from a phone is not to sit and watch. Only a turn the
	// user stopped stays silent, they were there when it happened.
	if s.onDone != nil && (state == StateComplete || state == StateFailed || state == StateInterrupted) {
		s.onDone(a.rec.Conversation)
	}
	// The end of a turn is what lets the queue go: whatever waited while this
	// one ran goes out as one new turn now. After the news above, so the entry
	// still points at the answer that just settled, not at the placeholder the
	// flush is about to write.
	s.flushReady()
}

// settleCheck ends a check. It writes nothing anywhere: what the answer means
// for the user is the watcher's decision, and it is waiting on done.
func (s *Service) settleCheck(a *activeRun, text string, turnErr error) {
	a.outcome, a.err = checkOutcome(text, turnErr, a.cancelled.Load())

	s.mu.Lock()
	// The session of a check is its own and nothing continues it, so it goes
	// when the check does. A session that refuses to go keeps its reservation
	// and cannot turn up as a resumable coder.
	s.dropSessionLocked(Conversation{
		Summary:         Summary{CoderID: a.rec.CoderID},
		NativeSessionID: a.rec.SessionID,
	})
	s.mu.Unlock()

	// The same order a chat turn ends in: the register first, the running mark
	// after it, so a check is never over and registered at the same time.
	s.retire(a)

	s.mu.Lock()
	delete(s.running, a.rec.ID)
	s.mu.Unlock()
}

// retire removes a turn from the register together with its raw output. The
// transcript holds the answer by then, and a file nobody reads any more is only
// disk.
func (s *Service) retire(a *activeRun) {
	s.runs.Delete(a.rec.ID)
}

// checkOutcome reads what a check concluded out of its answer.
func checkOutcome(text string, turnErr error, cancelled bool) (wakeOutcome, error) {
	switch {
	case cancelled:
		return wakeOutcome{}, errors.New("The check was stopped.")
	case turnErr != nil:
		return wakeOutcome{}, turnErr
	case strings.TrimSpace(text) == "":
		// No error, no words. Reading that as nothing to report is how a check
		// disappears without a trace, so it is an error like any other end
		// without a verdict.
		return wakeOutcome{}, errors.New("The check ended without saying anything.")
	}
	verdict, answer := parseVerdict(text)
	return wakeOutcome{Verdict: verdict, Text: answer}, nil
}

// AdoptedCheck is a check that outlived the server. The watcher takes it from
// here: it is the one that knows the job the check belongs to.
type AdoptedCheck struct {
	Terminal string
	// Context is what the check found before it started, carried through the
	// register so the job is judged the same way whether or not a restart
	// happened in between.
	Context checkContext
	run     *activeRun
}

// Recover picks up the turns a previous server left behind and is what makes a
// restart cost nothing. Every one of them is decided the same way: is the
// process still writing, and does its output still hold what was already read.
// A turn that is alive is followed on, a turn that ended in the meantime is read
// to its end and settled, and only a turn whose output is gone becomes an
// interrupted answer.
//
// It must run after everything the service publishes through is wired, and after
// the local API socket of this process is bound: an adopted check acts while it
// runs.
func (s *Service) Recover() []AdoptedCheck {
	var adopted []AdoptedCheck
	followed := map[string]bool{}

	// The queue flush waits until the whole register is walked: an orphaned
	// turn settles right here in the loop, and a flush started at that moment
	// could run next to a turn of the same conversation that is still a few
	// entries further down.
	s.mu.Lock()
	s.recovering = true
	s.mu.Unlock()

	for _, rec := range s.runs.List() {
		co, ok := s.coder(rec.CoderID)
		if !ok {
			log.Printf("assistant: turn %s ran on %s, which is not available any more", rec.ID, rec.CoderID)
			s.orphan(rec, errors.New("The coder of this answer is not available any more."))
			continue
		}
		alive := processAlive(rec.PID, rec.Lock)
		if info, err := os.Stat(rec.Output); err != nil || info.Size() < rec.Processed {
			// The answer this turn was writing is not in that file any more, so
			// reading it would put somebody else's words into this message.
			if alive {
				killProcess(rec.PID, rec.Lock)
			}
			s.orphan(rec, errTruncatedOutput)
			continue
		}

		a := &activeRun{rec: rec, proc: process{pid: rec.PID, lock: rec.Lock}, done: make(chan struct{}), orphaned: !alive}
		a.cancelled.Store(rec.Cancelled)

		s.mu.Lock()
		s.running[rec.ID] = a
		s.mu.Unlock()

		switch rec.Kind {
		case RunCheck:
			// The session of a running check stays out of the coder lists, the
			// same way it did before the restart.
			s.reserve(rec.CoderID, rec.SessionID)
			// The id the report will be written as lives on the entry, so a
			// check concluded after a restart writes the same one message.
			seen := rec.Context
			seen.MessageID = rec.MessageID
			adopted = append(adopted, AdoptedCheck{Terminal: rec.Terminal, Context: seen, run: a})
		default:
			s.chatSlots.adopt()
			followed[rec.MessageID] = true
			// The page gets the whole answer again: the file is read from its
			// first byte, so a browser that reconnects sees what was there
			// before the restart and everything after it, without a reload.
			s.hub.publish(rec.Conversation, StreamEvent{Kind: FrameStart, RunID: rec.ID, MessageID: rec.MessageID, State: string(StateStreaming)})
		}
		if alive {
			log.Printf("assistant: turn %s is still running as process %d, reading on", rec.ID, rec.PID)
		}
		go s.follow(a, co.Runner)
	}

	s.markLostTurns(followed)
	s.runs.Sweep()

	s.mu.Lock()
	s.recovering = false
	s.mu.Unlock()
	// A message that queued before the restart is still waiting in its
	// transcript. When no recovered turn runs for the live conversation it goes
	// out now; when one does, its own end flushes.
	s.flushReady()
	return adopted
}

// orphan settles a turn nothing can read any more, in the state the user sees
// as an interrupted answer they can send again.
func (s *Service) orphan(rec RunRecord, cause error) {
	a := &activeRun{rec: rec, done: make(chan struct{}), orphaned: true}
	close(a.done)
	if rec.Kind == RunCheck {
		a.err = cause
		s.runs.Delete(rec.ID)
		return
	}
	// The slot is taken first and given back by settling, so the books balance
	// for a turn that is only ever closed.
	s.chatSlots.adopt()
	s.settleChat(a, "", nil, cause)
}

// markLostTurns closes the answers no register entry accounts for. A turn is
// written into the register before its process starts and removed after it
// ended, so a message that is still streaming without one lost its process
// somewhere in between and will never be written again.
func (s *Service) markLostTurns(followed map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range s.store.List() {
		if !entry.Unfinished {
			continue
		}
		c, ok := s.store.Load(entry.ID)
		if !ok {
			continue
		}
		changed := false
		for i := range c.Messages {
			if c.Messages[i].State != StateStreaming || followed[c.Messages[i].ID] {
				continue
			}
			c.Messages[i].State = StateInterrupted
			c.Messages[i].Error = "This answer was interrupted when the server restarted."
			changed = true
		}
		if changed {
			c.UpdatedAt = s.now().UTC()
			s.store.Save(c)
		}
	}
}
