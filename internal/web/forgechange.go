package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/approval"
	"github.com/marein/dev-cockpit/internal/askpass"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/git"
	"github.com/marein/dev-cockpit/internal/plaintext"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// forgeChangeSent is what a request to open a change request sent: the
// branch, "" for the one HEAD is on, and each field of the change request,
// prefilled where a request without it leaves it out.
type forgeChangeSent struct {
	branch string
	change forge.NewChange
	titled bool
	bodied bool
}

// readForgeChange reads the body with its line breaks as the text box counts
// them against its maxlength, one character each, though a browser sends
// them as two.
func readForgeChange(c *gin.Context) forgeChangeSent {
	title, titled := c.GetPostForm("title")
	body, bodied := c.GetPostForm("body")
	return forgeChangeSent{
		branch: c.PostForm("branch"),
		change: forge.NewChange{Target: strings.TrimSpace(c.PostForm("target")), Title: strings.TrimSpace(title), Body: strings.ReplaceAll(body, "\r\n", "\n"), Draft: c.PostForm("draft") == "1"},
		titled: titled,
		bodied: bodied,
	}
}

// refused says why sent cannot be opened as it stands, "" when it can.
func (f forgeChangeSent) refused() string {
	n := f.change
	switch {
	case utf8.RuneCountInString(n.Title) > forge.MaxChangeTitle:
		return fmt.Sprintf("A title has at most %d characters.", forge.MaxChangeTitle)
	case utf8.RuneCountInString(n.Body) > forge.MaxChangeBody:
		return fmt.Sprintf("A description has at most %d characters.", forge.MaxChangeBody)
	}
	return forge.BranchRefusal(n.Target)
}

// handleProjectForgeChange renders the form that opens a change request of
// a pushed branch of a project, the one HEAD is on unless the query names
// another, as a page and alone for the dialog: into the repository's default
// branch, its title and body made of the commits the branch carries since
// then, a draft. A branch with an open change request shows that one
// instead. The forge is read where the poller's read is not fresh.
func (s *Server) handleProjectForgeChange(c *gin.Context) {
	p, err := s.projects.FindByName(c.Param("name"))
	if err != nil {
		s.renderError(c, http.StatusNotFound, "Project not found", "There is no such project.")
		return
	}
	data := s.forgeChangeData(c, p, c.Query("branch"), nil)
	s.renderForgeChange(c, data)
}

// handleProjectForgeChangeOpen opens a change request of a pushed branch of
// a project on the forge it is bound to, as the form names it; the forge is
// read again first, and a branch with an open change request answers that
// one. A click in the browser opens it at once. An assistant's call over the
// local socket fills what it leaves out the way the form is prefilled and
// waits for the user's approval, whose run resolves the project, its binding
// and the branch again by name.
func (s *Server) handleProjectForgeChangeOpen(c *gin.Context) {
	p, err := s.projects.FindByName(c.Param("name"))
	if err != nil {
		if s.localCall(c) || wantsJSON(c.Request) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Unknown project."})
			return
		}
		s.renderError(c, http.StatusNotFound, "Project not found", "There is no such project.")
		return
	}
	owner := ""
	if s.localCall(c) {
		if owner, err = s.assistantCaller(c); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
	}
	if err := readForm(c); err != nil {
		s.refuseForgeChange(c, p, nil, http.StatusBadRequest, err.Error())
		return
	}
	sent := readForgeChange(c)
	if refusal := sent.refused(); refusal != "" {
		s.refuseForgeChange(c, p, &sent, http.StatusBadRequest, refusal)
		return
	}
	w, _, code, err := s.forgeWatch(c.Request.Context(), p, sent.branch)
	if err != nil {
		s.refuseForgeChange(c, p, &sent, code, err.Error())
		return
	}
	if owner != "" {
		s.openForgeChangeLocal(c, owner, p, w, sent)
		return
	}
	opened, err := s.forge.OpenChange(c.Request.Context(), w, sent.change)
	var exists *forge.ChangeExists
	switch {
	case errors.As(err, &exists) && (inFormModal(c) || !wantsJSON(c.Request)):
		data := s.forgeChangeData(c, p, w.Branch, &sent)
		data.Exists, data.Problem = &exists.Change, ""
		s.renderForgeChange(c, data)
		return
	case err != nil:
		s.refuseForgeChange(c, p, &sent, forgeChangeCode(err), err.Error())
		return
	}
	message := forgeOpenedMessage(w.Binding.Host.Kind)
	if s.formStayed(c, message) {
		return
	}
	if wantsJSON(c.Request) {
		c.JSON(http.StatusOK, gin.H{"ok": true, "message": message, "url": opened.URL})
		return
	}
	s.redirectWithProjectFlash(c, p.Name, message, "")
}

// openForgeChangeLocal is an assistant's open of a change request: what it
// left out is made the way the form is prefilled, a branch with an open one
// answers that one, and the rest waits for the user's approval unless the
// user turned the question off.
func (s *Server) openForgeChangeLocal(c *gin.Context, owner string, p project.Project, w forge.Watch, sent forgeChangeSent) {
	ctx := c.Request.Context()
	n := sent.change
	fresh := true
	if !sent.titled || !sent.bodied {
		target := n.Target
		if target == "" {
			base, err := s.forge.ChangeBase(ctx, w, true)
			if s.forgeChangeExisting(c, w, err) {
				return
			}
			target, fresh = base, false
		}
		title, body, _, _ := forgeCarried(ctx, p, w, target)
		if !sent.titled {
			n.Title = title
		}
		if !sent.bodied {
			n.Body = body
		}
	}
	n, err := s.forge.ChangeTarget(ctx, w, n, fresh)
	if s.forgeChangeExisting(c, w, err) {
		return
	}
	if s.askApproval(c, s.forgeChangeApproval(owner, w, n)) {
		return
	}
	done, err := s.openForgeChangeNamed(ctx, w.Project, w.Branch, w.Binding.Address(), n)
	if err != nil {
		c.JSON(forgeChangeCode(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "opened": done.Plain})
}

// forgeChangeExisting answers an assistant's open whose branch has an open
// change request with that one and any other refusal with its sentence, and
// reports whether it answered.
func (s *Server) forgeChangeExisting(c *gin.Context, w forge.Watch, err error) bool {
	var exists *forge.ChangeExists
	switch {
	case err == nil:
		return false
	case errors.As(err, &exists):
		c.JSON(http.StatusOK, gin.H{"ok": true, "existing": forgeChanged(w, exists.Change, true).Plain})
	default:
		c.JSON(forgeChangeCode(err), gin.H{"error": err.Error()})
	}
	return true
}

// forgeChangeApproval is the approval an assistant's open of a change request
// waits for, once per branch. Its run resolves the project, its binding and
// the branch again by name and opens the change request the user saw, where
// the branch still has none open: one opened meanwhile answers instead.
func (s *Server) forgeChangeApproval(owner string, w forge.Watch, n forge.NewChange) approval.Request {
	kind := w.Binding.Host.Kind
	project, branch, address := w.Project, w.Branch, w.Binding.Address()
	action := "Open " + kind.ChangeNoun
	if n.Draft {
		action = "Open draft " + kind.ChangeNoun
	}
	body := n.Body
	if body == "" {
		body = "(none)"
	}
	return approval.Request{
		Owner: owner,
		Kind:  approvalForgeChange,
		Key:   project + "\n" + branch,
		What:  fmt.Sprintf("%s of %s into %s in %s", action, plaintext.Visible(branch), plaintext.Visible(n.Target), project),
		Details: []askpass.Detail{
			{Label: "Action", Value: action},
			{Label: "Title", Value: n.Title},
			{Label: "Description", Value: body},
			{Label: "Branch", Value: fmt.Sprintf("%s at %s", branch, forge.ShortHead(w.Head))},
			{Label: "Into", Value: n.Target},
			{Label: "Forge", Value: kind.Label + " · " + address},
			{Label: "Project", Value: project},
		},
		Projects: []string{project},
		Run: func() (string, error) {
			ctx, cancel := context.WithTimeout(context.Background(), forgeWriteTimeout)
			defer cancel()
			done, err := s.openForgeChangeNamed(ctx, project, branch, address, n)
			return done.Note, err
		},
	}
}

// openForgeChangeNamed resolves project, its binding and branch by name and
// opens n there, refused where the project is bound elsewhere than address
// now. A branch with an open change request answers that one.
func (s *Server) openForgeChangeNamed(ctx context.Context, project, branch, address string, n forge.NewChange) (assistant.ForgeOutcome, error) {
	p, err := s.projects.FindByName(project)
	if err != nil {
		return assistant.ForgeOutcome{}, errors.New("the project no longer exists")
	}
	w, _, _, err := s.forgeWatch(ctx, p, branch)
	if err != nil {
		return assistant.ForgeOutcome{}, err
	}
	if now := w.Binding.Address(); now != address {
		return assistant.ForgeOutcome{}, fmt.Errorf("%q is bound to %s now, not to %s", project, now, address)
	}
	opened, err := s.forge.OpenChange(ctx, w, n)
	var exists *forge.ChangeExists
	switch {
	case errors.As(err, &exists):
		return forgeChanged(w, exists.Change, true), nil
	case err != nil:
		return assistant.ForgeOutcome{}, err
	}
	return forgeChanged(w, opened, false), nil
}

// forgeChangeCode is the status a failed open answers: a conflict where the
// forge was not asked to open anything, a bad gateway where it refused.
func forgeChangeCode(err error) int {
	var refused *forge.ChangeRefusal
	var exists *forge.ChangeExists
	var off *forge.BranchOff
	switch {
	case errors.As(err, &refused), errors.As(err, &exists), errors.As(err, &off), errors.Is(err, forge.ErrNoToken), errors.Is(err, forge.ErrNoStatus), errors.Is(err, forge.ErrNoChange):
		return http.StatusConflict
	}
	return http.StatusBadGateway
}

// forgeChanged is what an assistant reads of a change request of w's branch,
// one an open opened or the open one the branch had already.
func forgeChanged(w forge.Watch, ch forge.Change, existed bool) assistant.ForgeOutcome {
	kind := w.Binding.Host.Kind
	return assistant.ForgeChanged(assistant.ForgeChange{
		Forge:   kind.Label,
		Address: w.Binding.Address(),
		Noun:    kind.ChangeNoun,
		Ref:     kind.ChangeRef(ch.Number),
		Title:   ch.Title,
		Draft:   ch.Draft,
		Branch:  w.Branch,
		Project: w.Project,
		Target:  ch.Target,
		URL:     ch.URL,
		Existed: existed,
	})
}

// forgeOpenedMessage is what the user reads of a change request a click
// opened, the same words each time: its number and title stand on the
// project row it leads to.
func forgeOpenedMessage(kind forge.Kind) string {
	return fmt.Sprintf("The %s is open.", kind.ChangeNoun)
}

// forgeCarried is the title and the body a change request of w's branch into
// target is prefilled with, made of the commits the pushed head carries since
// target as p's repository last fetched it, how many those are, and whether
// target was known to count them against.
func forgeCarried(ctx context.Context, p project.Project, w forge.Watch, target string) (string, string, int, bool) {
	messages, carried, measured, err := git.New(p.Path).Carried(ctx, forgeRemote, target, w.Head, forge.MaxChangeCommits)
	if err != nil {
		return "", "", 0, false
	}
	commits := make([]forge.Commit, 0, len(messages))
	for _, m := range messages {
		commits = append(commits, forge.Commit{Subject: m.Subject, Body: m.Body})
	}
	title, body := forge.ChangeText(commits)
	return title, body, carried, measured
}

// forgeChangeData reads what the form opening a change request of p's branch
// shows: the branch's open change request in its place, or why none opens,
// else the form filled with what was sent, or prefilled.
func (s *Server) forgeChangeData(c *gin.Context, p project.Project, branch string, sent *forgeChangeSent) render.ForgeChangeData {
	ctx := c.Request.Context()
	data := render.ForgeChangeData{
		Page:      s.page(c, "New change request", "projects"),
		Modal:     inFormModal(c),
		Project:   p.Name,
		Return:    s.formReturn(c),
		Draft:     true,
		TitleMax:  forge.MaxChangeTitle,
		BodyMax:   forge.MaxChangeBody,
		TargetMax: forge.MaxBranch,
	}
	w, local, _, err := s.forgeWatch(ctx, p, branch)
	if err != nil {
		data.Problem = err.Error()
		return data
	}
	data.Kind, data.Address, data.Branch, data.Head = w.Binding.Host.Kind, w.Binding.Address(), w.Branch, w.Head
	data.Page.Title = data.Heading()
	if local != "" && local != w.Head {
		data.Local = local
		data.Ahead, data.Behind, _ = git.New(p.Path).Apart(ctx, local, w.Head)
	}
	base, err := s.forge.ChangeBase(ctx, w, false)
	var exists *forge.ChangeExists
	switch {
	case errors.As(err, &exists):
		data.Exists = &exists.Change
		return data
	case err != nil:
		data.Problem = err.Error()
		return data
	}
	data.Target = base
	if sent != nil {
		data.Target, data.ChangeTitle, data.Body, data.Draft = sent.change.Target, sent.change.Title, sent.change.Body, sent.change.Draft
	}
	title, body, carried, measured := forgeCarried(ctx, p, w, data.Target)
	data.Carried, data.Measured = carried, measured
	if sent == nil {
		data.ChangeTitle, data.Body = title, body
	}
	return data
}

func (s *Server) renderForgeChange(c *gin.Context, data render.ForgeChangeData) {
	if data.Modal {
		c.HTML(http.StatusOK, "forge_change_form.gohtml", data)
		return
	}
	c.HTML(http.StatusOK, "forge_change.gohtml", data)
}

// refuseForgeChange answers a refused open with code: the dialog, a local
// caller and a script hear why as JSON, the page shows the form again under
// the reason with what was sent, so nothing a request sent rides a URL or the
// session.
func (s *Server) refuseForgeChange(c *gin.Context, p project.Project, sent *forgeChangeSent, code int, message string) {
	if inFormModal(c) || wantsJSON(c.Request) || s.localCall(c) {
		c.JSON(code, gin.H{"error": message})
		return
	}
	branch := ""
	if sent != nil {
		branch = sent.branch
	}
	data := s.forgeChangeData(c, p, branch, sent)
	data.Page.Flash = render.Flash{Message: message, Level: "error"}
	c.HTML(code, "forge_change.gohtml", data)
}
