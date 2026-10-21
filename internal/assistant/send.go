package assistant

import (
	"errors"
	"fmt"
	"strings"

	"github.com/marein/dev-cockpit/internal/statefile"
)

// Send appends a prompt and starts a generation. Attachments are already on
// disk when they arrive here, the prompt only points the coder at them.
//
// While this assistant's turn runs the message queues instead: it goes into the
// transcript as a waiting entry, and the end of the turn flushes everything
// waiting as one new turn. The queue belongs to the one assistant, so nobody
// else waits for it and a second thought does not have to be held in the head
// until an answer arrives. A send that finds older messages still waiting
// queues behind them even when nothing runs, so the order of what was typed is
// the order of what goes out.
//
// The decision falls under the service lock, the same one the turn's end takes
// to stop counting as running, so a send racing that end either sees the run
// and queues, or sees the freed assistant and starts.
func (s *Service) Send(id, prompt string, attachments []Attachment) (Run, error) {
	text, err := validatePrompt(prompt, len(attachments))
	if err != nil {
		return Run{}, err
	}
	return s.send(id, true, func(Instance) (Message, error) {
		return Message{Content: text, Attachments: attachments}, nil
	})
}

// Answer sends the user's picks for the questions an answer asked as their
// next message, the same way Send does. Whether the questions are still open
// is decided under the lock that appends the message, so two pages answering
// at once send one answer. The composer is not what sent it, its words stay.
func (s *Service) Answer(id, messageID string, picks []Pick) (Run, error) {
	return s.send(id, false, func(c Instance) (Message, error) {
		return answerMessage(c, messageID, picks)
	})
}

// send writes the message compose makes out of the transcript and starts the
// turn that answers it, or queues it behind the one that runs. composer says
// the message came out of the composer, whose words it then took along.
func (s *Service) send(id string, composer bool, compose func(Instance) (Message, error)) (Run, error) {
	s.mu.Lock()
	c, co, err := s.prepareLocked(id)
	if err != nil {
		s.mu.Unlock()
		return Run{}, err
	}
	user, err := compose(c)
	if err != nil {
		s.mu.Unlock()
		return Run{}, err
	}
	now := s.now().UTC()
	user.ID = statefile.NewID()
	user.Role = RoleUser
	user.CreatedAt = now
	if s.chatRunLocked(id) != nil || len(queuedMessages(c)) > 0 {
		if len(queuedMessages(c)) >= MaxQueuedMessages {
			s.mu.Unlock()
			return Run{}, errors.New("Too many messages are waiting already. Let the assistant catch up first.")
		}
		user.State = StateQueued
		c.Messages = append(c.Messages, user)
		c.UpdatedAt = now
		s.store.Save(c)
		s.mu.Unlock()

		ended := Ends(c.Messages, len(c.Messages)-1)
		s.spendDraft(id, composer, now, ended)
		for _, shown := range append(ended, user.ID) {
			s.hub.publish(c.ID, StreamEvent{Kind: FrameMessage, MessageID: shown})
		}
		s.changed()
		return Run{MessageID: user.ID, UserMessageID: user.ID, Title: c.Title, Queued: true}, nil
	}
	user.State = StateComplete
	// What the cockpit wrote since the last answer is read before the prompt
	// joins the transcript, and its headlines ride along in front of it.
	since := notesSince(c)
	c.Messages = append(c.Messages, user)
	if c.Title == "" || c.Title == DefaultTitle {
		c.Title = deriveTitle(user.Content, user.Attachments)
	}
	ended := Ends(c.Messages, len(c.Messages)-1)
	r, err := s.startLocked(&c, co, withNotes(since, withAttachments(user.Content, user.Attachments)), append(ended, user.ID)...)
	s.mu.Unlock()
	if err != nil {
		return Run{}, err
	}
	s.spendDraft(id, composer, now, ended)
	r.UserMessageID = user.ID
	s.changed()
	return r, nil
}

// Retry runs the last prompt again after a failed, cancelled or interrupted
// turn. It is always explicit: a turn that may already have been charged is
// never resent on its own.
func (s *Service) Retry(id string) (Run, error) {
	s.mu.Lock()
	c, co, err := s.prepareLocked(id)
	if err != nil {
		s.mu.Unlock()
		return Run{}, err
	}
	if s.chatRunLocked(id) != nil {
		s.mu.Unlock()
		return Run{}, errors.New("This assistant is still working on the previous message.")
	}
	last, ok := c.Last()
	if !ok || last.Role != RoleAssistant || !last.State.Retryable() {
		s.mu.Unlock()
		return Run{}, errors.New("There is nothing to retry in this assistant.")
	}
	replaced := last.ID
	c.Messages = c.Messages[:len(c.Messages)-1]
	prompt := ""
	if prev, ok := c.Last(); ok && prev.Role == RoleUser {
		prompt = withAttachments(prev.Content, prev.Attachments)
	}
	if prompt == "" {
		s.mu.Unlock()
		return Run{}, errors.New("There is nothing to retry in this assistant.")
	}
	r, err := s.startLocked(&c, co, prompt)
	r.ReplacedID = replaced
	s.mu.Unlock()
	if err != nil {
		return Run{}, err
	}
	// The page that pressed retry drops the replaced bubble itself, the others
	// hear about it here, so no page keeps the failed turn next to its retry.
	s.hub.publish(c.ID, StreamEvent{Kind: FrameGone, MessageID: replaced})
	s.changed()
	return r, nil
}

// prepareLocked resolves an assistant that may accept a new message. Whether a
// turn is already running is the caller's question, and the two callers answer
// it differently: a send queues behind it, a retry refuses.
func (s *Service) prepareLocked(id string) (Instance, CoderInfo, error) {
	c, ok := s.store.Load(id)
	if !ok {
		return Instance{}, CoderInfo{}, errors.New("Assistant not found.")
	}
	co, ok := s.coder(c.CoderID)
	if !ok {
		return Instance{}, CoderInfo{}, errors.New("The coder of this assistant is not available right now.")
	}
	if _, err := s.workdirs.Workdir(c.ID); err != nil {
		return Instance{}, CoderInfo{}, err
	}
	return c, co, nil
}

// startLocked persists the pending turn and registers it as running; the
// launch itself happens off the lock, in launchAndFollow. The assistant
// placeholder is written before anything starts, so a crash leaves a visible
// interrupted turn instead of a lost prompt (the recovery sweep settles a
// streaming message no register entry covers), and the run is registered
// before the launch begins, so a second send queues behind it and a stop
// finds it, however long the runner's own preparation takes: opencode's
// first turn boots a server to create its provider session, and held under
// the lock that boot stalled every assistant surface for seconds.
//
// announce names the user messages this turn takes with it and the answers
// whose questions they end (Ends). They go out on the stream after the save
// and before the start frame, so a page that never saw them pulls them first
// and the answer's bubble lands under them.
func (s *Service) startLocked(c *Instance, co CoderInfo, prompt string, announce ...string) (Run, error) {
	workdir, err := s.workdirs.Workdir(c.ID)
	if err != nil {
		return Run{}, err
	}
	runID := statefile.NewID()
	model := ModelFor(RunChat, c.Summary, "", co.ModelDefaults())
	msg := Message{
		ID:        statefile.NewID(),
		Role:      RoleAssistant,
		CreatedAt: s.now().UTC(),
		RunID:     runID,
		Model:     model,
		State:     StateStreaming,
	}
	c.Messages = append(c.Messages, msg)
	c.UpdatedAt = s.now().UTC()
	s.store.Save(*c)

	req := TurnRequest{
		Instance:  c.ID,
		SessionID: c.NativeSessionID,
		Resume:    co.Runner.SessionExists(c.NativeSessionID),
		Title:     c.Title,
		Workdir:   workdir,
		Prompt:    prompt,
		Model:     model,
	}
	rec := RunRecord{
		ID:        runID,
		Kind:      RunChat,
		Instance:  c.ID,
		MessageID: msg.ID,
		CoderID:   co.ID,
		SessionID: c.NativeSessionID,
		Model:     req.Model,
		StartedAt: s.now().UTC(),
	}
	for _, id := range announce {
		s.hub.publish(c.ID, StreamEvent{Kind: FrameMessage, MessageID: id})
	}
	s.hub.publish(c.ID, StreamEvent{Kind: FrameStart, RunID: runID, MessageID: msg.ID, Model: model, State: string(StateStreaming), Since: rec.StartedAt.UnixMilli()})

	a := &activeRun{rec: rec, done: make(chan struct{})}
	s.running[runID] = a
	go s.launchAndFollow(a, co.Runner, req)
	return Run{RunID: runID, MessageID: msg.ID, Title: c.Title, StartedAt: rec.StartedAt}, nil
}

// Cancel stops the running generation, keeping the partial answer. The stop is
// written down before the process is killed, so a restart that happens in
// between still reads it as a stop.
func (s *Service) Cancel(id string) error {
	s.mu.Lock()
	a := s.chatRunLocked(id)
	if a == nil {
		s.mu.Unlock()
		return errors.New("This instance is not working on anything.")
	}
	a.cancelled.Store(true)
	id, proc, launched := a.rec.ID, a.proc, a.launched
	s.mu.Unlock()

	s.runs.Update(id, func(rec *RunRecord) { rec.Cancelled = true })
	// A turn still launching has no process yet: the cancelled mark is what
	// launchAndFollow reads, and it kills the process the moment it begins.
	if launched {
		proc.Kill()
	}
	return nil
}

// Discard removes one waiting message before the queue flushed it. The decision
// falls under the service lock, the same one the flush holds: a message is
// either still waiting and comes out, or it already went and the caller hears so.
func (s *Service) Discard(instanceID, messageID string) error {
	s.mu.Lock()
	c, ok := s.store.Load(instanceID)
	if !ok {
		s.mu.Unlock()
		return errors.New("Assistant not found.")
	}
	for i, m := range c.Messages {
		if m.ID != messageID {
			continue
		}
		if m.State != StateQueued {
			s.mu.Unlock()
			return errors.New("This message already went out.")
		}
		ended := Ends(c.Messages, i)
		c.Messages = append(c.Messages[:i], c.Messages[i+1:]...)
		c.UpdatedAt = s.now().UTC()
		s.store.Save(c)
		s.mu.Unlock()

		s.hub.publish(instanceID, StreamEvent{Kind: FrameGone, MessageID: messageID})
		for _, shown := range ended {
			s.hub.publish(instanceID, StreamEvent{Kind: FrameMessage, MessageID: shown})
		}
		s.changed()
		return nil
	}
	s.mu.Unlock()
	return errors.New("Message not found.")
}

// flushReady sends what queued up in one assistant while its turn ran, as
// exactly one new turn. The queue is that assistant's, so this looks at that
// one and at nothing else, and an assistant with something waiting holds up
// nobody. It runs whenever a chat turn settled, and once per assistant after
// Recover, which is what makes a queued message survive a restart.
func (s *Service) flushReady(instanceID string) {
	s.mu.Lock()
	if s.recovering || instanceID == "" {
		s.mu.Unlock()
		return
	}
	c, ok := s.store.Load(instanceID)
	if !ok || s.chatRunLocked(instanceID) != nil {
		s.mu.Unlock()
		return
	}
	queued := queuedMessages(c)
	if len(queued) == 0 {
		s.mu.Unlock()
		return
	}
	co, ok := s.coder(c.CoderID)
	if !ok {
		s.mu.Unlock()
		return
	}
	if _, err := s.workdirs.Workdir(c.ID); err != nil {
		s.mu.Unlock()
		return
	}
	flushed := make([]string, 0, len(queued))
	for i := range c.Messages {
		if c.Messages[i].State == StateQueued {
			c.Messages[i].State = StateComplete
			flushed = append(flushed, c.Messages[i].ID)
		}
	}
	// A queued first message is what names the assistant, the same way a sent
	// one does: the turn it goes out in is the first anybody sees.
	if c.Title == "" || c.Title == DefaultTitle {
		c.Title = deriveTitle(queued[0].Content, queued[0].Attachments)
	}
	// The waiting entries go out with the new turn; every open page pulls them
	// fresh so their bubbles stop saying so. The one line about what the
	// cockpit wrote since the last answer rides along the way it does on a
	// send.
	_, err := s.startLocked(&c, co, withNotes(notesSince(c), queuedPrompt(queued)), flushed...)
	s.mu.Unlock()
	if err != nil {
		// Nothing was written, the messages stay waiting, and the next end of a
		// turn tries again.
		return
	}
	s.changed()
}

// flushAllReady lets every assistant that has something waiting go. Only the
// restart needs it: a queue that outlived the server belongs to whichever
// assistant wrote it, and there can be several of them.
func (s *Service) flushAllReady() {
	for _, entry := range s.store.List() {
		s.flushReady(entry.ID)
	}
}

func validatePrompt(raw string, attachments int) (string, error) {
	text := strings.TrimSpace(raw)
	if text == "" && attachments == 0 {
		return "", errors.New("Type a message first.")
	}
	if len(text) > MaxPromptBytes {
		return "", fmt.Errorf("That message is too long. Keep it under %d KB.", MaxPromptBytes/1024)
	}
	return text, nil
}

// queuedMessages are the transcript entries still waiting to go out, in the
// order they were sent. Only user messages ever carry the queued state.
func queuedMessages(c Instance) []Message {
	var out []Message
	for _, m := range c.Messages {
		if m.State == StateQueued {
			out = append(out, m)
		}
	}
	return out
}

// queuedPrompt is the one turn a flushed queue becomes. A single waiting
// message goes out as itself; several stay recognizable as separate messages,
// in the order they were sent.
func queuedPrompt(msgs []Message) string {
	if len(msgs) == 1 {
		return withAttachments(msgs[0].Content, msgs[0].Attachments)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "The user sent %d messages while the previous answer was still on its way. They follow in order, answer them all.\n", len(msgs))
	for i, m := range msgs {
		fmt.Fprintf(&b, "\n--- Message %d ---\n%s\n", i+1, withAttachments(m.Content, m.Attachments))
	}
	return b.String()
}

// noteLine is one line of the summary in front of a chat prompt: the headline
// the cockpit already wrote for that message, how many messages stand behind
// it and whether the turn that produced it broke off. Nothing is formulated
// here, the headline is read off the message.
type noteLine struct {
	headline string
	brokeOff bool
	count    int
}

// noteLineOf reads the line a message the cockpit wrote is summarized by: a
// note's own headline, or the headline of the event a pushed answer reacted
// to. Anything else is not the cockpit speaking and answers false.
func noteLineOf(m Message) (noteLine, bool) {
	switch {
	case m.IsNote():
		return noteLine{headline: oneLine(m.Note.Headline), count: 1}, true
	case m.Role == RoleAssistant && m.Auto:
		line := noteLine{count: 1, brokeOff: m.State == StateFailed || m.State == StateInterrupted}
		if m.Origin != nil {
			line.headline = oneLine(m.Origin.Headline)
		}
		return line, true
	}
	return noteLine{}, false
}

// notesSince is what the cockpit wrote into the thread since the assistant's
// last chat answer, newest first: the notes, a check's reports, and the
// answers a reaction pushed, which are assistant messages too. A chat turn's
// prompt used to be the user's text and nothing else, so an assistant learned
// about its own jobs only when the user told it. The walk stops only at a
// chat answer, never at a pushed one, because a pushed answer came out of a
// session that saw nothing of the thread. Equal headlines fold into one line
// with a count, which is what keeps a schedule firing every half hour from
// writing the same line twenty times; a turn that broke off folds only with
// another one that broke off, because that is the line an assistant must not
// read past.
func notesSince(c Instance) []noteLine {
	type key struct {
		headline string
		brokeOff bool
	}
	var lines []noteLine
	at := map[key]int{}
	for i := len(c.Messages) - 1; i >= 0; i-- {
		m := c.Messages[i]
		if m.Role == RoleAssistant && !m.Auto {
			break
		}
		line, ok := noteLineOf(m)
		if !ok {
			continue
		}
		k := key{line.headline, line.brokeOff}
		if pos, seen := at[k]; seen {
			lines[pos].count++
			continue
		}
		at[k] = len(lines)
		lines = append(lines, line)
	}
	return lines
}

// maxNoteLines bounds that summary. Equal headlines fold, so fifty lines is
// the guard against an outlier and never the everyday case; what is cut is
// the oldest, and the last line says how much.
const maxNoteLines = 50

// withNotes puts the summary of what the cockpit wrote since the last answer
// in front of a user's prompt: the count, where the whole of it stands, and
// one headline per line. The headline is what lets an assistant decide
// whether any of it touches the message it is answering. A bare count could
// not, so it had to read every job to find out, or guess.
func withNotes(lines []noteLine, prompt string) string {
	if len(lines) == 0 {
		return prompt
	}
	notes := 0
	for _, l := range lines {
		notes += l.count
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Since your last answer the cockpit wrote %d note%s into this thread, newest first. job-list and assistant-show on yourself say what, look only when the user's message needs it.\n", notes, plural(notes))
	shown := lines
	if len(shown) > maxNoteLines {
		shown = shown[:maxNoteLines]
	}
	for _, l := range shown {
		b.WriteString("- ")
		if l.count > 1 {
			fmt.Fprintf(&b, "%dx ", l.count)
		}
		b.WriteString(l.headline)
		if l.brokeOff {
			b.WriteString(" (broke off)")
		}
		b.WriteString("\n")
	}
	if rest := len(lines) - len(shown); rest > 0 {
		fmt.Fprintf(&b, "  and %d older\n", rest)
	}
	b.WriteString("\n")
	b.WriteString(prompt)
	return b.String()
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// withAttachments is the prompt the coder receives. The files are named by
// their absolute path, which is what a coder needs to open one, and the note
// stays out of the transcript so the bubble shows what was typed.
func withAttachments(text string, attachments []Attachment) string {
	if len(attachments) == 0 {
		return text
	}
	var b strings.Builder
	b.WriteString(text)
	if text != "" {
		b.WriteString("\n\n")
	}
	b.WriteString("Attached files:\n")
	for _, a := range attachments {
		fmt.Fprintf(&b, "- %s\n", a.Path)
	}
	return b.String()
}

// deriveTitle names an assistant after its first prompt, or after the file it
// carried when the prompt was only an attachment.
func deriveTitle(prompt string, attachments []Attachment) string {
	title := truncateRunes(oneLine(prompt), MaxTitleRunes)
	if title == "" && len(attachments) > 0 {
		title = truncateRunes(oneLine(attachments[0].Name), MaxTitleRunes)
	}
	if title == "" {
		return DefaultTitle
	}
	return title
}

func oneLine(s string) string {
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}
