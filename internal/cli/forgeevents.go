package cli

import (
	"net/url"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/notify"
)

// pipelineKinds are the event kinds a final status of a pipeline is
// published under.
var pipelineKinds = map[forge.Status]string{
	forge.Success:  assistant.PipelineKindSucceeded,
	forge.Failed:   assistant.PipelineKindFailed,
	forge.Canceled: assistant.PipelineKindCanceled,
	forge.Skipped:  assistant.PipelineKindSkipped,
}

// pipelineReport is the end of a pipeline the way the assistants read it,
// in the words of the forge it ran on: they know no forge. False for an end
// of no final status.
func pipelineReport(end forge.PipelineEnd) (assistant.PipelineReport, bool) {
	pl := end.Status.Pipeline
	if pl == nil {
		return assistant.PipelineReport{}, false
	}
	kind, ok := pipelineKinds[pl.Status]
	if !ok {
		return assistant.PipelineReport{}, false
	}
	w := end.Watch
	k := w.Binding.Host.Kind
	report := assistant.PipelineReport{
		Kind:    kind,
		Project: w.Project,
		Branch:  w.Branch,
		Head:    w.Head,
		Forge:   k.Label,
		Address: w.Binding.Address(),
		ID:      pl.ID,
		URL:     pl.URL,
	}
	if c := end.Status.Change; c != nil {
		change := changeRequest(k, *c)
		report.Change = &change
	}
	for _, j := range pl.Failing() {
		report.Failing = append(report.Failing, j.Name)
	}
	return report, true
}

// changeRequest is a change request in the words of the forge it is on.
func changeRequest(k forge.Kind, c forge.Change) assistant.ChangeRequest {
	return assistant.ChangeRequest{Noun: k.ChangeNoun, Ref: k.ChangeRef(c.Number), Title: c.Title, State: string(c.State), Draft: c.Draft, Target: c.Target, URL: c.URL}
}

// reviewReport is a new note on a change request the way the assistants
// read it, in the words of the forge it is on.
func reviewReport(n forge.ReviewNote) assistant.ReviewReport {
	w, t := n.Watch, n.Thread
	report := assistant.ReviewReport{
		Project:  w.Project,
		Branch:   w.Branch,
		Forge:    w.Binding.Host.Kind.Label,
		Address:  w.Binding.Address(),
		Change:   changeRequest(w.Binding.Host.Kind, n.Change),
		Author:   n.Note.Author,
		Body:     n.Note.Body,
		URL:      n.Note.URL,
		Path:     t.Path,
		Line:     t.Line,
		Side:     string(t.Side),
		Outdated: t.Outdated,
		Resolved: t.Resolved,
	}
	if len(t.Notes) > 0 && t.Notes[0].ID != n.Note.ID {
		report.Reply, report.Opener = true, t.Notes[0].Author
	}
	if reply, resolve := t.Takes(); reply {
		report.Thread, report.Resolvable = t.ID, resolve
	}
	return report
}

// pipelineEnded hands every end of a pipeline to the assistants and a failed
// one to notifier as the project's news, which leads to the editor's git
// sheet: that shows the pipeline with its jobs and the logs of the failed ones.
func pipelineEnded(publish func(assistant.PipelineReport), notifier *notify.Service) func(forge.PipelineEnd) {
	return func(end forge.PipelineEnd) {
		report, ok := pipelineReport(end)
		if !ok {
			return
		}
		publish(report)
		if report.Kind != assistant.PipelineKindFailed {
			return
		}
		info := notify.TargetInfo{Name: report.Project, Project: report.Project, URL: "/projects/" + url.PathEscape(report.Project) + "/editor?view=pipeline", Failed: true}
		info.Title, info.Detail = pipelineNews(report)
		notifier.AddResolved(notify.PipelineTarget(report.Project), info)
	}
}
