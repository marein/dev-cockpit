package assistant

import "errors"

var (
	errCompactUnsupported = errors.New("This coder cannot compact a conversation.")
	errNotCompacted       = errors.New("The coder reported no compaction.")
)

const compactInterrupted = "The server restarted while it ran."

func (s *Service) Compacts(coderID string) bool {
	co, ok := s.coder(coderID)
	if !ok {
		return false
	}
	_, ok = co.Runner.(Compactor)
	return ok
}

// Compact compacts the coder's session of one assistant. It runs detached and
// in the register like a chat turn, so a restart picks it up, and it holds the
// chat's place, so a message sent meanwhile queues behind it. It never goes
// through withNotes or the queue: a CLI reads its compact command only as the
// whole prompt, anything in front of it reaches the model as plain words.
func (s *Service) Compact(id string) (Run, error) {
	s.mu.Lock()
	c, co, err := s.prepareLocked(id)
	if err != nil {
		s.mu.Unlock()
		return Run{}, err
	}
	if _, ok := co.Runner.(Compactor); !ok {
		s.mu.Unlock()
		return Run{}, errCompactUnsupported
	}
	if s.chatRunLocked(id) != nil {
		s.mu.Unlock()
		return Run{}, errors.New("This assistant is still working. Compact once it is done.")
	}
	if len(queuedMessages(c)) > 0 {
		s.mu.Unlock()
		return Run{}, errors.New("Messages are still waiting to go out. Compact once they are answered.")
	}
	if !c.HasTurn() || !co.Runner.SessionExists(c.NativeSessionID) {
		s.mu.Unlock()
		return Run{}, errors.New("There is nothing to compact before the first answer.")
	}
	r, err := s.beginLocked(&c, co, RunCompact, "")
	s.mu.Unlock()
	if err != nil {
		return Run{}, err
	}
	s.changed()
	return r, nil
}

func commandFor(kind RunKind, runner Runner, req TurnRequest) (Command, error) {
	if kind != RunCompact {
		return runner.Command(req)
	}
	compactor, ok := runner.(Compactor)
	if !ok {
		return Command{}, errCompactUnsupported
	}
	return compactor.CompactCommand(req)
}

// settleCompact writes how a compaction went into its line, the way
// settleChat writes an answer, and lets the queue go. A compaction that
// applied without a reading leaves the old one pending: its number is no
// longer the context's, and the next turn reads the real one. One that did
// not apply changed nothing, the reading stays as it was.
func (s *Service) settleCompact(a *activeRun, text string, report *CompactReport, usage *ContextUsage, turnErr error) {
	state, message, outcome := a.compactState(text, report, turnErr)
	pending := false

	s.mu.Lock()
	c, ok := s.store.Load(a.rec.Instance)
	if ok {
		for i := range c.Messages {
			if c.Messages[i].ID != a.rec.MessageID || c.Messages[i].RunID != a.rec.ID {
				continue
			}
			c.Messages[i].State = state
			c.Messages[i].Error = message
			c.Messages[i].Compact = &Compaction{Outcome: outcome}
			switch {
			case outcome != CompactApplied:
			case usage != nil:
				reading := *usage
				c.Context = &reading
			case c.Context != nil:
				reading := *c.Context
				reading.Tokens, reading.Pending = 0, true
				c.Context = &reading
			}
			pending = c.Context != nil && c.Context.Pending
			c.UpdatedAt = s.now().UTC()
			s.store.Save(c)
			break
		}
	}
	s.mu.Unlock()

	context := 0
	if usage != nil && outcome == CompactApplied {
		context = usage.Percent()
	}
	s.hub.publish(a.rec.Instance, StreamEvent{
		Kind:           FrameEnd,
		RunID:          a.rec.ID,
		MessageID:      a.rec.MessageID,
		State:          string(state),
		Error:          message,
		Context:        context,
		ContextPending: pending,
		Compact:        true,
	})

	s.retire(a)

	s.mu.Lock()
	delete(s.running, a.rec.ID)
	s.mu.Unlock()

	s.changed()
	s.flushReady(a.rec.Instance)
}

// compactState decides what a finished compaction is. The coder's record wins
// over everything, a stop included: a session it compacted stays compacted.
// copilot exits 1 when there is nothing to compact, so the record also wins
// over a failed run.
func (a *activeRun) compactState(text string, report *CompactReport, turnErr error) (State, string, string) {
	if report != nil && (report.Outcome == CompactApplied || report.Outcome == CompactNothing) {
		return StateComplete, "", report.Outcome
	}
	switch {
	case a.cancelled.Load():
		return StateCancelled, "", ""
	case report != nil && report.Reason != "":
		return StateFailed, sanitizeError(Quote(errNotCompacted, report.Reason)), ""
	case turnErr != nil && a.orphaned:
		return StateInterrupted, compactInterrupted, ""
	case turnErr != nil:
		return StateFailed, sanitizeError(turnErr), ""
	}
	return StateFailed, sanitizeError(Quote(errNotCompacted, text)), ""
}
