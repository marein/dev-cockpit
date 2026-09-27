package assistant

import (
	"log"
	"strings"

	"github.com/marein/dev-cockpit/internal/approval"
)

// ApprovalReport is the end of an action the assistant asked for and the user
// was asked about, as the web layer hands it here: the action's one line, how
// it ended, and the event its kind publishes for that end, if any.
type ApprovalReport struct {
	Owner   string
	What    string
	Outcome approval.Outcome
	Event   ApprovalEvent
}

// ApprovalEvent is what a kind of approval publishes when one of its
// approvals ends: the event source and, per verdict, the event kind. A verdict
// the map leaves out publishes nothing, which is how a kind whose run
// publishes its own end (a compose run that started) stays silent here.
type ApprovalEvent struct {
	Source string
	Kinds  map[string]string
}

// approvalHeadline is the one line the note is read by: the verdict and the
// action, "Declined: Delete project shop".
func approvalHeadline(report ApprovalReport) string {
	word := "Approved"
	switch report.Outcome.Verdict {
	case approval.Failed:
		word = "Failed"
	case approval.Declined:
		word = "Declined"
	}
	return word + ": " + strings.TrimSpace(report.What)
}

type approvalNoteData struct {
	Verdict string
	Text    string
}

// RecordApproval writes the end of an approval into the thread of the
// assistant that asked, one note whatever the kind, the way RecordCompose
// writes a run's end, and publishes the event the report names for the
// verdict. It rings the user unless the outcome is quiet, the user's own
// click that went as decided. Returns the message id, empty when the owner is
// gone.
func (s *Service) RecordApproval(report ApprovalReport) string {
	text := strings.TrimSuffix(render("approval_note.md.tmpl", approvalNoteData{
		Verdict: report.Outcome.Verdict,
		Text:    strings.TrimSpace(report.Outcome.Text),
	}), "\n")
	note := Note{
		Source:   NoteApproval,
		Headline: approvalHeadline(report),
		Verdict:  report.Outcome.Verdict,
		Name:     strings.TrimSpace(report.What),
	}
	id, now := s.recordNote(report.Owner, note, text, report.Outcome.Quiet)
	if id == "" {
		log.Printf("assistant: the assistant that asked for %q is gone, dropping its report", report.What)
		return ""
	}
	if kind, ok := report.Event.Kinds[report.Outcome.Verdict]; ok {
		s.publishEvent(CockpitEvent{
			Source:   report.Event.Source,
			Kind:     kind,
			Owner:    report.Owner,
			Time:     now,
			Headline: note.Headline,
			Body:     text,
		})
	}
	return id
}

// ProjectDeleted is the outcome an approved project delete reads in the
// thread: gone, with the worktree projects that went along.
func ProjectDeleted(name string, worktrees []string) string {
	return strings.TrimSuffix(render("approval_project_deleted.md.tmpl", struct {
		Name      string
		Worktrees []string
	}{name, worktrees}), "\n")
}

// CoderDeleted is the outcome an approved coder delete reads in the thread:
// gone, with what the deletion dropped.
func CoderDeleted(name, dropped string) string {
	return strings.TrimSuffix(render("approval_coder_deleted.md.tmpl", struct {
		Name    string
		Dropped string
	}{name, dropped}), "\n")
}

// AssistantDeleted is the outcome an approved assistant delete reads in the
// thread: gone, with the coders it steered handed back to the user.
func AssistantDeleted(name string, released []Job) string {
	return strings.TrimSuffix(render("approval_assistant_deleted.md.tmpl", struct {
		Name     string
		Released []string
	}{name, jobNames(released)}), "\n")
}
