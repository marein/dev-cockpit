package web

import (
	"errors"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/askpass"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/docker"
	"github.com/marein/dev-cockpit/internal/eventbus"
	"github.com/marein/dev-cockpit/internal/notify"
	"github.com/marein/dev-cockpit/internal/project"
)

// An assistant runs the compose commands a person runs, over the same route,
// and a command that asks first asks the person first there too: the run is
// parked, the command answers the run id at once, and the question stands in
// the askpass broker under the run's own key, where every signed-in page
// shows it and the push channels carry it to a phone. The person approves or
// denies; no answer within the bound, or a restart that takes the question
// with it, ends the run declined. The assistant is told either way, in its
// own thread, see composeDone.

// composeApprovalTimeout bounds how long a parked run waits for the decision.
// Long enough for a phone in a pocket, short enough that a "down with
// volumes" approved much later still means what the assistant asked for.
const composeApprovalTimeout = 30 * time.Minute

// assistantAsksForCompose says whether a confirm action an assistant starts
// waits for the user: yes unless the user turned the Compose actions approval
// off, in the modal or in the Approvals settings. It is one switch for every
// assistant, so the owner decides nothing.
func (s *Server) assistantAsksForCompose() bool {
	return s.assistants != nil && ApprovalAsks(s.settings, approvalComposeActions)
}

// assistantLabel is what a question calls an assistant: its title cut to a
// label, or the surface's own word for one nobody named.
func (s *Server) assistantLabel(id string) string {
	if s.assistants == nil {
		return assistant.Name
	}
	entry, err := s.assistants.Get(id)
	if err != nil {
		return assistant.Name
	}
	if title := strings.TrimSpace(entry.Title); title != "" && title != assistant.DefaultTitle {
		return coder.ShortTitle(title)
	}
	return assistant.Name
}

// askComposeApproval registers the bridge of one parked run before the
// request that parked it is answered, so a cancel that comes after finds the
// bridge and takes it down. The question itself stands once
// awaitComposeApproval parks it on that bridge. A run that stopped being parked meanwhile, a cancel from the
// page in between, gets no question at all. A question that cannot be put up
// declines the run without a note, the error is the caller's whole answer.
func (s *Server) askComposeApproval(owner string, p project.Project, action docker.Action, run docker.RunView) (*askpass.Action, error) {
	var bridge *askpass.Action
	if s.askpassBroker != nil {
		bridge = s.askpassBroker.BeginApproval(askpass.ApprovalKey(run.ID), askpass.Question{
			Project:   p.Name,
			Action:    action.Label,
			Command:   run.Command,
			Dir:       run.Dir,
			Assistant: s.assistantLabel(owner),
			Stack:     stackLabel(p.Path, run.Dir),
			URL:       dockerRunPath(p.Name, run.ID),
		})
	}
	if bridge == nil {
		const reason = "the approval could not be asked"
		if err := s.docker.WithdrawCompose(run.ID, reason); err != nil {
			log.Printf("docker: withdraw run %s: %v", run.ID, err)
		}
		return nil, errors.New(reason)
	}
	if current, ok := s.docker.ComposeRunByID(run.ID); !ok || !current.Pending {
		bridge.End()
		return nil, nil
	}
	return bridge, nil
}

// declineParked ends a parked run, quietly where it is over already: a run
// called off while it waited had its end reported by the cancel.
func (s *Server) declineParked(id, reason string, byUser bool) {
	if current, ok := s.docker.ComposeRunByID(id); ok && !current.Pending {
		return
	}
	if err := s.docker.DeclineCompose(id, reason, byUser); err != nil {
		log.Printf("docker: decline run %s: %v", id, err)
	}
}

// awaitComposeApproval waits out the question askComposeApproval put up and
// starts or ends the run on what comes back. It runs off the request, like
// the run itself would: the command that asked is long answered when the
// person decides. Which of a decision and a cancel wins is the docker
// service's to settle, see ApproveCompose.
func (s *Server) awaitComposeApproval(bridge *askpass.Action, runID string) {
	// The end of the action is what a timeout looks like: the question
	// leaves the list and the asker reads no decision.
	expiry := time.AfterFunc(composeApprovalTimeout, bridge.End)
	decision, decided := bridge.AskApproval()
	expiry.Stop()
	bridge.End()
	switch {
	case decided && decision.Approved:
		// Don't ask again is about the kind, not about this run: it holds
		// even where a cancel took the run away while the box was ticked.
		if decision.Remember {
			setApprovalAsks(s.settings, approvalComposeActions, false)
		}
		if current, ok := s.docker.ComposeRunByID(runID); ok && !current.Pending {
			break
		}
		if err := s.docker.ApproveCompose(runID); err != nil {
			log.Printf("docker: approve run %s: %v", runID, err)
		}
	case decided:
		// Only the user answers an approval, the local socket is refused on
		// it, so a Deny is the user's own click and rings nobody.
		s.declineParked(runID, "declined by the user", true)
	default:
		s.declineParked(runID, "the approval expired unanswered", false)
	}
	s.bus.Publish(eventbus.Event{Type: "docker"})
}

// declineParkedRuns ends the parked runs match picks and takes their
// questions down with them, for an owner or a directory that is going away.
func (s *Server) declineParkedRuns(match func(docker.RunView) bool, reason string) {
	ids := s.docker.DeclinePending(match, reason)
	if len(ids) == 0 {
		return
	}
	if s.askpassBroker != nil {
		for _, id := range ids {
			if bridge := s.askpassBroker.Find(askpass.ApprovalKey(id)); bridge != nil {
				bridge.End()
			}
		}
	}
	s.bus.Publish(eventbus.Event{Type: "docker"})
}

// declineOwnerApprovals is what a deleted assistant leaves: nobody is there
// any more to hear how a run it asked for went.
func (s *Server) declineOwnerApprovals(owner string) {
	s.declineParkedRuns(func(run docker.RunView) bool { return run.Owner == owner }, "the assistant was deleted")
}

// declineProjectApprovals is what a deleted project leaves: a run approved
// later would start in a directory that is gone.
func (s *Server) declineProjectApprovals(root string) {
	root = filepath.Clean(root)
	s.declineParkedRuns(func(run docker.RunView) bool {
		return run.Dir == root || strings.HasPrefix(run.Dir, root+string(filepath.Separator))
	}, "the project was deleted")
}

// questionTarget is the notification entry a standing question holds while it
// stands, empty for one that holds none: a proxied git question under its
// project, an approval under its run. It is decided here and handed to the
// dialog with the question, never worked out again in the browser.
func questionTarget(q askpass.Question) string {
	if run, ok := askpass.ApprovalRun(q.Key); ok && q.Kind == askpass.KindApproval {
		return notify.ApprovalTarget(run)
	}
	if q.External {
		return notify.GitPromptTarget(q.Project)
	}
	return ""
}

// composeReport is what the assistant's thread is told about an owned run
// that ended: the run, where it ran, how it went, the tail of what it wrote
// and the page that shows the rest. The stack is named the way the compose
// menu names it; a project that is gone by then leaves the directory.
func (s *Server) composeReport(run docker.ComposeRun, err error, output string) assistant.ComposeReport {
	report := assistant.ComposeReport{
		Owner:    run.Owner,
		Run:      run.ID,
		Project:  run.Label,
		Stack:    run.Dir,
		Action:   run.Action,
		URL:      dockerRunPath(run.Label, run.ID),
		Failed:   run.Failed,
		Declined: run.Declined,
		Exited:   run.Exited,
		Exit:     run.Exit,
		Output:   output,
		ByUser:   run.ByUser,
	}
	if err != nil {
		report.Reason = err.Error()
	}
	if p, perr := s.projects.FindByName(run.Label); perr == nil {
		report.Stack = stackLabel(p.Path, run.Dir)
	}
	return report
}
