package web

import (
	"time"

	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/terminal"
)

// sendCoderInput sends the person's input into a coder and tells the working
// mark what it was. Every input starts the echo window: the repaint that
// follows a keystroke is the keystroke coming back, not the coder working. An
// interrupt key is a hint that the turn was aborted, because an abort before
// the first answer leaves no trace at all, see Tracker.Interrupt; whether the
// keys mean that here is the coder's own choice. Enter or a numbered option
// is the person answering an ask the coder stands at, see Tracker.Answered,
// and text is the person writing, which a digit typed after it belongs to,
// see Tracker.Wrote. The keys are dated before they go out, floored to the
// millisecond the record dates its messages in: what the coder writes after
// taking the key then never sorts before it.
func (s *Server) sendCoderInput(co *coder.Manager, id string, items []terminal.Input) error {
	at := time.Now().Truncate(time.Millisecond)
	if err := co.Send(id, items); err != nil {
		return err
	}
	s.activity.Input(id)
	if co.Coder().ActivityProfile().InterruptKeys && terminal.InputInterrupts(items) {
		s.activity.Interrupt(id, at)
		return nil
	}
	if terminal.InputWrites(items) {
		s.activity.Wrote(id, at)
	}
	if terminal.InputAnswers(items) {
		s.activity.Answered(id, at, terminal.InputSubmits(items))
	}
	return nil
}
