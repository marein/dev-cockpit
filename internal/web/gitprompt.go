package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/askpass"
	"github.com/marein/dev-cockpit/internal/git"
	"github.com/marein/dev-cockpit/internal/sshagent"
)

// promptAction opens the askpass bridge for one user-triggered action, named
// by the project it runs in and the action's own name, which is what the
// dialog shows as this server's truth above ssh's or git's line. Whether a
// call may ask at all is the route's decision alone: only the handlers of the
// actions somebody started call this, a status poll or the quiet fetch never
// does, and everything without a bridge keeps failing prompts fast.
//
// The third value is false only when the project already runs a bridged
// action. The write lock in front of every caller makes that unreachable, so
// it is an invariant guard, but it may not be worked past: running the action
// anyway would leave it without a bridge, and every question would come back
// as an authentication failure with nothing saying that it was never asked.
// It opens the bridge through Begin and not BeginCommand, and that is the
// whole difference to the proxy's helper below: somebody started this on a
// page and is looking at it, so the question stays inside the app. Routing
// both through one constructor with empty strings would tie that decision to
// what the dialog renders again, which is exactly what askpass.Question.
// External exists to keep apart.
//
// The ssh agent signs for it through the UI's socket for a browser alone. The
// same route called over the local socket runs without the agent, so a local
// process never borrows the UI's policy.
func (s *Server) promptAction(c *gin.Context, project, name string) (*askpass.Action, *git.Prompt, bool) {
	kind := sshagent.KindUI
	if s.localCall(c) {
		kind = ""
	}
	return s.beginPrompt(func() *askpass.Action { return s.askpassBroker.Begin(project, name) }, kind)
}

// promptActionCommand is promptAction for the git proxy, which asks on behalf
// of somebody who is not in the app: the command line and the working copy it
// runs in travel with the question, so the dialog can show what is about to
// run and where, and the question is the kind that leaves the app as news.
// See askpass.Question.External. kind is the caller's kind of the ssh agent,
// empty where it has none, and the call then runs without the agent.
func (s *Server) promptActionCommand(project, name, command, dir string, kind sshagent.Kind) (*askpass.Action, *git.Prompt, bool) {
	return s.beginPrompt(func() *askpass.Action { return s.askpassBroker.BeginCommand(project, name, command, dir) }, kind)
}

// beginPrompt is what the two share: no bridge wired means every prompt keeps
// failing fast and the call runs anyway, an open action carries the helper
// environment into the call, and the ssh agent's socket of the caller's kind.
func (s *Server) beginPrompt(begin func() *askpass.Action, kind sshagent.Kind) (*askpass.Action, *git.Prompt, bool) {
	if s.askpassBroker == nil {
		return nil, nil, true
	}
	action := begin()
	if action == nil {
		return nil, nil, false
	}
	env := append(action.Env(),
		"SSH_ASKPASS="+s.askpassScript,
		"GIT_ASKPASS="+s.askpassScript,
	)
	if s.sshAgent != nil && kind != "" {
		if sock := s.sshAgent.Socket(kind); sock != "" {
			env = append(env, "SSH_AUTH_SOCK="+sock)
		}
	}
	return action, &git.Prompt{Env: env, Asked: action.Asked(), Answered: action.Answered()}, true
}

// promptRefusal is the error a bridged action answers with: git's words, and
// when the person pressed cancel, the honest reason the words alone would
// hide behind an authentication failure.
func promptRefusal(action *askpass.Action, err error) string {
	if action != nil && action.Cancelled() {
		return err.Error() + " — the question was cancelled."
	}
	return err.Error()
}

// handleGitPromptList answers the standing questions of every running git
// action, oldest first. It sits at the app level and not under a project,
// because the dialog does too: the page that started an action may be
// reloaded, updated away, or lying on a desk while another device answers, so
// every signed-in page shows what is being asked. The session is the whole
// authorization, single user by design.
func (s *Server) handleGitPromptList(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"questions": s.gitPromptViews()})
}

// gitPromptView is one standing question the way the dialog needs it: the
// broker's question, plus the notification entry it holds while it stands, or
// nothing when it holds none.
//
// The target is worked out here and not in the browser. Which questions become
// news is this server's rule (reconcileGitPromptNews) and the prefix is this
// server's spelling (notify.GitPromptTarget); a client deciding it again would
// be the same rule written a second time in another language, and a rename
// here would quietly stop the dialog from ever reading its entry.
type gitPromptView struct {
	askpass.Question
	Target string `json:"target,omitempty"`
}

func (s *Server) gitPromptViews() []gitPromptView {
	if s.askpassBroker == nil {
		return []gitPromptView{}
	}
	questions := s.askpassBroker.Questions()
	views := make([]gitPromptView, 0, len(questions))
	for _, q := range questions {
		views = append(views, gitPromptView{Question: q, Target: questionTarget(q)})
	}
	return views
}

// gitPromptAnswer is what the dialog posts back. Key names the action the
// way the question carried it; Project is the older spelling of the same
// thing, a page from before approvals existed still posts it. Answer and
// Cancel are a git question's, Approve and Remember an approval's: Remember
// turns that kind of approval off for every assistant, and means nothing on a
// denial.
type gitPromptAnswer struct {
	Key      string `json:"key"`
	Project  string `json:"project"`
	ID       string `json:"id"`
	Answer   string `json:"answer"`
	Cancel   bool   `json:"cancel"`
	Approve  bool   `json:"approve"`
	Remember bool   `json:"remember"`
}

// decision is the approval the answer carries. A denial remembers nothing.
func (a gitPromptAnswer) decision() askpass.Decision {
	return askpass.Decision{Approved: a.Approve, Remember: a.Approve && a.Remember}
}

// handleGitPromptAnswer carries the typed answer, or the cancel, back to the
// helper that asked. The answer lives in this request and the broker's
// channel and nowhere else: not in a log line, not in a state file. ok says
// whether the broker took it; false is a question that was already answered
// on another device or whose action already ended, which the dialog swallows,
// its close travels on the gitprompt event.
func (s *Server) handleGitPromptAnswer(c *gin.Context) {
	var req gitPromptAnswer
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The request could not be read."})
		return
	}
	if s.askpassBroker == nil {
		c.JSON(http.StatusOK, gin.H{"ok": false})
		return
	}
	key := req.Key
	if key == "" {
		key = req.Project
	}
	action := s.askpassBroker.Find(key)
	if action == nil {
		c.JSON(http.StatusOK, gin.H{"ok": false})
		return
	}
	// A key of the ssh agent opens for whoever answers, so like an approval
	// it is answered in a browser, never by the caller over the local socket.
	if q := action.Question(); q != nil && q.Kind == askpass.KindSSHKey && s.localCall(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": sshKeyLocalRefusal})
		return
	}
	if action.Approval() {
		// An approval means the user said yes, so it is answered in a
		// browser and never over the local socket, where the assistant that
		// asked could answer itself.
		if s.localCall(c) {
			c.JSON(http.StatusForbidden, gin.H{"error": approvalLocalRefusal})
			return
		}
		// A decision and never a line: the answer route reads which of the
		// two it was handed off the action, not off the request.
		c.JSON(http.StatusOK, gin.H{"ok": action.Decide(req.ID, req.decision())})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": action.Answer(req.ID, req.Answer, req.Cancel)})
}
