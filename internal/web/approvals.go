package web

import (
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/approval"
	"github.com/marein/dev-cockpit/internal/askpass"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/notify"
)

// An assistant takes some actions only once the user said yes: a compose
// command that asks first, a project delete. Each of them reaches the same
// route a click takes, and there it goes through internal/approval: with the
// kind's switch off the action runs the way it runs for the user, otherwise
// the question stands in the askpass broker, where every signed-in page shows
// it and the push channels carry it to a phone, the command answers that it
// waits, and the owner reads how it ended in its own thread, see
// approvalEnded. The run a kind hands in resolves what it acts on again when
// the user approves, by the names the request carried, and refuses where one
// of them is gone: nothing is trusted across the wait.

// newApprovals builds the approval service over the per kind switches of the
// Approvals settings. Its state file is only there so the next start can tell
// the owners whose question a restart took, and belongs in no backup.
func (s *Server) newApprovals(stateDir string) *approval.Service {
	return approval.New(approval.Config{
		Path:       filepath.Join(stateDir, "approvals.json"),
		Asks:       func(kind string) bool { return ApprovalAsks(s.settings, kind) },
		StopAsking: func(kind string) { setApprovalAsks(s.settings, kind, false) },
		Asker:      func(owner string) string { return withShortID(s.assistantLabel(owner), owner) },
		Ended:      s.approvalEnded,
	})
}

// RecoverApprovals declines the approvals the last process left waiting and
// tells their owners why, the ones on disk when the server was built and none
// asked since.
func (s *Server) RecoverApprovals() {
	s.approvals.Recover()
}

// askApproval puts an assistant's action before the user and answers the
// request where it has to: a refusal, or that the action waits. It answers
// false, having written nothing, where the kind's switch is off, and the
// caller then takes the action the way it takes it for the user.
func (s *Server) askApproval(c *gin.Context, req approval.Request) bool {
	waiting, err := s.approvals.Ask(req)
	switch {
	case err != nil:
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case waiting:
		c.JSON(http.StatusOK, gin.H{"pending": true, "what": req.What})
	}
	return err != nil || waiting
}

// approvalEnded writes how an approval ended into its owner's thread, with
// the event its kind publishes for that end.
func (s *Server) approvalEnded(a approval.Approval, outcome approval.Outcome) {
	if s.assistants == nil {
		return
	}
	report := assistant.ApprovalReport{Owner: a.Owner, What: a.What, Outcome: outcome}
	for _, kind := range approvalKinds {
		if kind.ID == a.Kind {
			report.Event = kind.Event
			break
		}
	}
	s.assistants.RecordApproval(report)
}

// withShortID is how an approval names an assistant or a coder: names repeat,
// two assistants may share a title and every untitled one reads the same, so
// the short id stands behind the name and tells them apart.
func withShortID(name, id string) string {
	return name + " · " + coder.ShortID(id)
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

// questionTarget is the notification entry a standing question holds while it
// stands, empty for one that holds none: a proxied git question under its
// project, an approval under its id. It is decided here and handed to the
// dialog with the question, never worked out again in the browser.
func questionTarget(q askpass.Question) string {
	if id, ok := askpass.ApprovalID(q.Key); ok && q.Kind == askpass.KindApproval {
		return notify.ApprovalTarget(id)
	}
	if q.External {
		return notify.GitPromptTarget(q.Project)
	}
	return ""
}
