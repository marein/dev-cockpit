package assistant

import (
	"log"
	"strings"

	"github.com/marein/dev-cockpit/internal/approval"
	"github.com/marein/dev-cockpit/internal/plaintext"
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

// ProjectDeleted is the outcome of a project delete: gone, or still going
// where its containers go down first, with the worktree projects that went
// along.
func ProjectDeleted(name string, worktrees []string, deleting bool) string {
	return strings.TrimSuffix(render("approval_project_deleted.md.tmpl", struct {
		Name      string
		Worktrees []string
		Deleting  bool
	}{name, worktrees, deleting}), "\n")
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

// PostScriptSaved is the outcome of an approved write of the worktree post
// script: saved, or deleted where the content was empty.
func PostScriptSaved(deleted bool) string {
	return strings.TrimSuffix(render("approval_post_script_saved.md.tmpl", struct {
		Deleted bool
	}{deleted}), "\n")
}

// ForgeRetry is a retry that went through on a forge: the forge's label and
// the repository's address there, the pipeline by its name, the branch and
// the project it ran for, the job by its name, "" for the jobs that failed
// the pipeline, the page of what runs again, and why the jobs a retry of the
// pipeline left as they are cannot run again.
type ForgeRetry struct {
	Forge    string
	Address  string
	Pipeline string
	Branch   string
	Project  string
	Job      string
	URL      string
	LeftOut  string
}

// ForgeOutcome is the outcome of a write on a forge in the two forms it is
// read in: Markdown in the note of an approval, plain text in an answer the
// CLI prints at once.
type ForgeOutcome struct {
	Note  string
	Plain string
}

func forgeOutcome(name string, data any) ForgeOutcome {
	return ForgeOutcome{
		Note:  strings.TrimSuffix(render(name, data), "\n"),
		Plain: strings.TrimSuffix(fill(plainTemplates, name, data), "\n"),
	}
}

// ForgeRetried is the outcome of a retry. What the forge wrote is written
// visible, so none of it breaks a line of the thread it stands in.
func ForgeRetried(r ForgeRetry) ForgeOutcome {
	for _, field := range []*string{&r.Forge, &r.Address, &r.Pipeline, &r.Branch, &r.Project, &r.Job, &r.URL, &r.LeftOut} {
		*field = plaintext.Visible(*field)
	}
	return forgeOutcome("forge_retried.md.tmpl", r)
}

// ForgeChange is a change request of a branch on a forge: the forge's label
// and the repository's address there, its noun and number in the forge's
// words, its title, whether it is a draft, the branch and the project it is
// of, the branch it merges into, its page, and whether it stood open already
// instead of opening.
type ForgeChange struct {
	Forge   string
	Address string
	Noun    string
	Ref     string
	Title   string
	Draft   bool
	Branch  string
	Project string
	Target  string
	URL     string
	Existed bool
}

// ForgeChanged is the outcome of an open of a change request. What the forge
// wrote is written visible, so none of it breaks a line of the thread it
// stands in.
func ForgeChanged(c ForgeChange) ForgeOutcome {
	for _, field := range []*string{&c.Forge, &c.Address, &c.Noun, &c.Ref, &c.Title, &c.Branch, &c.Project, &c.Target, &c.URL} {
		*field = plaintext.Visible(*field)
	}
	return forgeOutcome("forge_changed.md.tmpl", c)
}

// ForgePublish is a publish of line comments on a forge that went through:
// the forge's label and the repository's address there, the change request's
// noun and number in the forge's words and its title, the branch and the
// project the comments are of, and each note it posted.
type ForgePublish struct {
	Forge   string
	Address string
	Noun    string
	Ref     string
	Title   string
	Branch  string
	Project string
	Notes   []ForgePublishedNote
}

// ForgePublishedNote is one comment a publish posted: its file, its line in
// the pushed head and the page of its thread on the forge.
type ForgePublishedNote struct {
	Path string
	Line int
	URL  string
}

// ForgePublished is the outcome of a publish of line comments. What the forge
// or a file name holds is written visible, so none of it breaks a line of the
// thread it stands in.
func ForgePublished(p ForgePublish) ForgeOutcome {
	for _, field := range []*string{&p.Forge, &p.Address, &p.Noun, &p.Ref, &p.Title, &p.Branch, &p.Project} {
		*field = plaintext.Visible(*field)
	}
	for i := range p.Notes {
		p.Notes[i].Path, p.Notes[i].URL = plaintext.Visible(p.Notes[i].Path), plaintext.Visible(p.Notes[i].URL)
	}
	return forgeOutcome("forge_published.md.tmpl", p)
}

// ForgeThread is a write on a review thread on a forge that went through:
// the forge's label and the repository's address there, the change request's
// noun and number in the forge's words and its title, the branch and the
// project, the file and the line the thread stands on, whether a reply was
// posted, the page of that reply, and whether the thread is resolved now.
type ForgeThread struct {
	Forge    string
	Address  string
	Noun     string
	Ref      string
	Title    string
	Branch   string
	Project  string
	Path     string
	Line     int
	Replied  bool
	URL      string
	Resolved bool
}

// ForgeThreadWritten is the outcome of a write on a review thread. What the
// forge or a file name holds is written visible, so none of it breaks a line
// of the thread it stands in.
func ForgeThreadWritten(t ForgeThread) ForgeOutcome {
	for _, field := range []*string{&t.Forge, &t.Address, &t.Noun, &t.Ref, &t.Title, &t.Branch, &t.Project, &t.Path, &t.URL} {
		*field = plaintext.Visible(*field)
	}
	return forgeOutcome("forge_thread.md.tmpl", t)
}
