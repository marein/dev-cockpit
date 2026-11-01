package assistant

import (
	"strings"

	"github.com/marein/dev-cockpit/internal/plaintext"
)

// PipelineReport is the end of a pipeline of the branch the forge service
// polls of a bound project, as runServe hands it here once per pipeline and
// final status. The assistant knows no forge, so every word the forge has
// for it travels in the report: Kind is one of the pipeline kinds, Forge
// what the forge is called and Address the repository's page without its
// scheme, ID the forge's own number of the pipeline, empty where it numbers
// none, and Failing the names of the jobs that failed it.
type PipelineReport struct {
	Kind    string
	Project string
	Branch  string
	Head    string
	Forge   string
	Address string
	ID      string
	URL     string
	Change  *ChangeRequest
	Failing []string
}

// ChangeRequest is the change request of a branch, in the forge's words:
// what it calls one and how it writes its number, "merge request" and "!12".
type ChangeRequest struct {
	Noun   string
	Ref    string
	Title  string
	State  string
	Draft  bool
	Target string
	URL    string
}

// visible is the change request with every word of the forge written
// visible, so none of them breaks a line of the prompt it stands in.
func (c ChangeRequest) visible() ChangeRequest {
	return ChangeRequest{
		Noun:   plaintext.Visible(c.Noun),
		Ref:    plaintext.Visible(c.Ref),
		Title:  plaintext.Visible(c.Title),
		State:  plaintext.Visible(c.State),
		Draft:  c.Draft,
		Target: plaintext.Visible(c.Target),
		URL:    plaintext.Visible(c.URL),
	}
}

// pipelineFailingShown bounds the jobs an event names, forge-show lists them
// all.
const pipelineFailingShown = 10

// pipelineSaid is what the headline and the text say a pipeline of a kind
// did.
var pipelineSaid = map[string]struct{ word, text string }{
	PipelineKindSucceeded: {"succeeded", "passed"},
	PipelineKindFailed:    {"failed", "failed"},
	PipelineKindCanceled:  {"canceled", "was canceled"},
	PipelineKindSkipped:   {"skipped", "was skipped"},
}

// pipelineHeadline is the one line a pipeline event is read by: what
// happened, the branch, the project and the change request, "Pipeline
// failed: feature in shop, merge request !12".
func pipelineHeadline(report PipelineReport) string {
	headline := "Pipeline " + pipelineSaid[report.Kind].word + ": " + oneLine(report.Branch) + " in " + oneLine(report.Project)
	if c := report.Change; c != nil {
		headline += ", " + oneLine(c.Noun) + " " + oneLine(c.Ref)
	}
	return headline
}

// pipelineEventData is what the event's text is handed. Every word from the
// forge or the repository is written visible, so none of them breaks a line
// of the prompt it stands in.
type pipelineEventData struct {
	Project, Branch, Head, Forge, Address, ID, URL, Said string
	Change                                               *ChangeRequest
	Failing                                              []string
	More                                                 int
	Cockpit                                              string
}

// pipelineBody is the rest of the event, what a reaction reads under the
// headline: where the pipeline ran and its page, the change request with
// its title, the jobs that failed it, and the command that reads the rest.
func pipelineBody(report PipelineReport) string {
	data := pipelineEventData{
		Project: plaintext.Visible(report.Project),
		Branch:  plaintext.Visible(report.Branch),
		Head:    plaintext.Visible(report.Head),
		Forge:   plaintext.Visible(report.Forge),
		Address: plaintext.Visible(report.Address),
		ID:      plaintext.Visible(report.ID),
		URL:     plaintext.Visible(report.URL),
		Said:    pipelineSaid[report.Kind].text,
		Cockpit: shortCockpit,
	}
	if c := report.Change; c != nil {
		visible := c.visible()
		data.Change = &visible
	}
	for i, name := range report.Failing {
		if i == pipelineFailingShown {
			data.More = len(report.Failing) - i
			break
		}
		data.Failing = append(data.Failing, plaintext.Visible(name))
	}
	return strings.TrimSuffix(render("pipeline_event.md.tmpl", data), "\n")
}

// Pipeline publishes the end of a pipeline as an event. It is everybody's,
// a trigger of any assistant on the project takes it.
func (r *Reactor) Pipeline(report PipelineReport) {
	r.Publish(CockpitEvent{
		Source:   EventForge,
		Kind:     report.Kind,
		Project:  report.Project,
		Branch:   report.Branch,
		Time:     r.now().UTC(),
		Headline: pipelineHeadline(report),
		Body:     pipelineBody(report),
	})
}
