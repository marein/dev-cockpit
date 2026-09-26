package assistant

import (
	"log"
	"strings"
	"unicode/utf8"

	"github.com/marein/dev-cockpit/internal/statefile"
)

// ComposeReport is the end of a compose run an assistant started, as the
// docker layer reports it and the web layer hands it here: which run, what
// it ran where, how it ended, and the page that shows the whole of it. The
// assistant knows no routes, so the URL is the caller's.
type ComposeReport struct {
	Owner string
	Run   string
	// Project is the project the stack belongs to, Stack the stack's label
	// inside it, empty for the project root, Action the command's label.
	Project string
	Stack   string
	Action  string
	URL     string
	// Failed covers every way the run did not go through; Declined narrows it
	// to a run that never started. Reason is the sentence that says why.
	Failed   bool
	Declined bool
	Reason   string
	Exited   bool
	Exit     int
	// Output is the tail of what the run wrote, already bounded by the
	// docker layer; the note cuts it once more to what a thread carries.
	Output string
	// ByUser says the user ended the run themselves, a Deny, or a Cancel of a
	// parked or a running run: the note and the event stand, the ring does
	// not.
	ByUser bool
}

// composeOutputRunes bounds what a compose note quotes of the run's output.
// The docker layer hands over two kilobytes, which is a wall in a thread;
// the last lines are where a failure says its reason, and the run page has
// the rest.
const composeOutputRunes = 600

// Kind is the event kind this end publishes under, the same words the trigger
// options list.
func (r ComposeReport) Kind() string {
	switch {
	case r.Declined:
		return ComposeKindDeclined
	case r.Failed:
		return ComposeKindFailed
	}
	return ComposeKindDone
}

// composeNoteData is what the note template is handed.
type composeNoteData struct {
	Action   string
	Where    string
	Failed   bool
	Declined bool
	Reason   string
	Exited   bool
	Exit     int
	Output   string
	URL      string
}

// composeWhere is what the note calls the place a run ran: the stack inside
// its project, or the project alone for a stack at its root.
func composeWhere(project, stack string) string {
	project = strings.TrimSpace(project)
	stack = strings.TrimSpace(stack)
	switch {
	case project == "" && stack == "":
		return "the project"
	case stack == "":
		return project
	case project == "":
		return stack
	}
	return stack + " in " + project
}

// composeHeadline is the one line a compose note is read by: what happened,
// the command and where, "Compose done: Compose up on shop". The note's text
// says where with the same preposition and leaves the command out, the
// headline and the notification's identifier already name it.
func composeHeadline(report ComposeReport) string {
	word := "done"
	switch report.Kind() {
	case ComposeKindFailed:
		word = "failed"
	case ComposeKindDeclined:
		word = "declined"
	}
	return "Compose " + word + ": " + strings.TrimSpace(report.Action) + " on " + composeWhere(report.Project, report.Stack)
}

// composeNote is the text of the note, rendered like the reports a check
// ends in.
func composeNote(report ComposeReport) string {
	output := strings.TrimSpace(report.Output)
	if utf8.RuneCountInString(output) > composeOutputRunes {
		runes := []rune(output)
		output = "…" + string(runes[len(runes)-composeOutputRunes:])
	}
	// A fence inside the quoted output would end the block early; the
	// output is a quote and never markup, so its fences are blunted.
	output = strings.ReplaceAll(output, "```", "` ` `")
	return strings.TrimSuffix(render("compose_note.md.tmpl", composeNoteData{
		Action:   strings.TrimSpace(report.Action),
		Where:    composeWhere(report.Project, report.Stack),
		Failed:   report.Failed,
		Declined: report.Declined,
		Reason:   report.Reason,
		Exited:   report.Exited,
		Exit:     report.Exit,
		Output:   output,
		URL:      report.URL,
	}), "\n")
}

// RecordCompose writes the end of a compose run into the thread of the
// assistant that started it, the way recordWake writes a check's report: one
// note, announced on the thread's own frame so an open page appends it, and
// published as an event so a trigger on it fires, in the owner's thread and
// nowhere else. It rings the user through onDone like a check's report does,
// as news of the assistant's thread: the run went on in the background and
// its end is what the user waits for. An end the user made themselves
// (ByUser) rings nobody, it is news only to the thread. The project's docker
// notification stays off for it, the thread's is the one. Returns the message
// id, empty when the owner is gone.
func (s *Service) RecordCompose(report ComposeReport) string {
	s.mu.Lock()
	c, ok := s.store.Load(report.Owner)
	if !ok {
		s.mu.Unlock()
		log.Printf("assistant: the assistant of compose run %s is gone, dropping its report", report.Run)
		return ""
	}
	now := s.now().UTC()
	text := composeNote(report)
	message := Message{
		ID:        statefile.NewID(),
		Role:      RoleCockpit,
		Content:   text,
		CreatedAt: now,
		State:     StateComplete,
		Note: &Note{
			Source:   NoteCompose,
			Headline: composeHeadline(report),
			Verdict:  report.Kind(),
			Name:     strings.TrimSpace(report.Action),
			Project:  strings.TrimSpace(report.Project),
			Run:      report.Run,
		},
	}
	c.Messages = append(c.Messages, message)
	c.UpdatedAt = now
	s.store.Save(c)
	s.mu.Unlock()

	s.hub.publish(c.ID, StreamEvent{Kind: FrameMessage, MessageID: message.ID})
	s.changed()
	if s.onDone != nil && !report.ByUser {
		s.onDone(c.ID)
	}
	s.publishEvent(CockpitEvent{
		Source:   EventCompose,
		Kind:     report.Kind(),
		Target:   report.Run,
		Owner:    report.Owner,
		Time:     now,
		Headline: message.Note.Headline,
		Body:     text,
	})
	return message.ID
}
