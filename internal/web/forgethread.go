package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/approval"
	"github.com/marein/dev-cockpit/internal/askpass"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/linecomment"
	"github.com/marein/dev-cockpit/internal/plaintext"
)

// maxThreadOpening bounds what an approval quotes of the note a review
// thread opens with.
const maxThreadOpening = 200

// handleProjectForgeThread answers or resolves a review thread on the diff of
// the change request of a project's branch, the one HEAD is on unless the
// form names another: the thread by the forge's id, answered with the form's
// body where it has one, then resolved where the form asks. The forge is read
// again first, and the line comments follow with the poller's read right
// after. A click in the browser writes at once. An assistant's call over the
// local socket waits for the user's approval, whose run resolves the project,
// its binding, the branch and the thread again by name and writes what the
// user saw. A refusal says whether the reply was posted before it, so the
// reply dialog does not offer to post it a second time.
func (s *Server) handleProjectForgeThread(c *gin.Context) {
	p, err := s.projects.FindByName(c.Param("name"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Unknown project."})
		return
	}
	owner := ""
	if s.localCall(c) {
		if owner, err = s.assistantCaller(c); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
	}
	write := forge.ThreadWrite{Thread: strings.TrimSpace(c.PostForm("thread")), Body: strings.TrimSpace(c.PostForm("body")), Resolve: c.PostForm("resolve") == "1"}
	if refusal := forgeThreadAsked(write); refusal != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": refusal})
		return
	}
	ctx := c.Request.Context()
	w, _, code, err := s.forgeWatch(ctx, p, c.PostForm("branch"))
	if err != nil {
		c.JSON(code, gin.H{"error": err.Error()})
		return
	}
	if owner != "" {
		target, err := s.forge.ThreadTarget(ctx, w, write)
		if err != nil {
			c.JSON(forgeThreadCode(err), gin.H{"error": err.Error()})
			return
		}
		if s.askApproval(c, s.forgeThreadApproval(owner, w, target, write)) {
			return
		}
	}
	target, note, err := s.forge.WriteThread(ctx, w, write)
	if err != nil {
		c.JSON(forgeThreadCode(err), gin.H{"error": err.Error(), "replied": note != nil})
		return
	}
	if owner != "" {
		c.JSON(http.StatusOK, gin.H{"ok": true, "written": forgeThreadText(w, target, note).Plain})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "message": forgeThreadMessage(w.Binding.Host.Kind, write, target, note)})
}

// forgeThreadAsked says why a write is no request to write on a thread, ""
// when it is one.
func forgeThreadAsked(t forge.ThreadWrite) string {
	switch {
	case forge.ThreadIDRefusal(t.Thread) != "":
		return forge.ThreadIDRefusal(t.Thread)
	case t.Body == "" && !t.Resolve:
		return "Write a reply, or resolve the thread."
	case len(t.Body) > linecomment.MaxText:
		return fmt.Sprintf("A reply has at most %d KiB.", linecomment.MaxText>>10)
	}
	return ""
}

// forgeThreadCode is the status a failed write on a thread answers: a
// conflict where the forge was not asked to write anything, a bad gateway
// where it refused.
func forgeThreadCode(err error) int {
	var refused *forge.ThreadRefusal
	var off *forge.BranchOff
	switch {
	case errors.As(err, &refused), errors.As(err, &off), errors.Is(err, forge.ErrNoToken), errors.Is(err, forge.ErrNoStatus),
		errors.Is(err, forge.ErrNoThreads), errors.Is(err, forge.ErrNoReply), errors.Is(err, forge.ErrNoResolve):
		return http.StatusConflict
	}
	return http.StatusBadGateway
}

// forgeThreadApproval is the approval an assistant's write on a thread waits
// for, once per thread. Its run resolves the project, its binding, the branch
// and the thread again by name and writes the reply the user saw: a project
// bound elsewhere, a branch without the thread and a resolve of a thread
// resolved meanwhile refuse it.
func (s *Server) forgeThreadApproval(owner string, w forge.Watch, target forge.ThreadTarget, write forge.ThreadWrite) approval.Request {
	kind := w.Binding.Host.Kind
	project, branch, address := w.Project, w.Branch, w.Binding.Address()
	ref := kind.ChangeRef(target.Change.Number)
	action := "Reply"
	switch {
	case write.Body == "":
		action = "Resolve the thread"
	case write.Resolve && !target.Thread.Resolved:
		action = "Reply and resolve the thread"
	}
	details := []askpass.Detail{{Label: "Action", Value: action}, {Label: "Thread", Value: forgeThreadOpening(target.Thread)}}
	if write.Body != "" {
		details = append(details, askpass.Detail{Label: "Reply", Value: write.Body})
	}
	on := fmt.Sprintf("%s %s · %s", kind.ChangeNoun, ref, target.Change.Title)
	if target.Change.URL != "" {
		on += " · " + target.Change.URL
	}
	details = append(details,
		askpass.Detail{Label: "On", Value: on},
		askpass.Detail{Label: "Branch", Value: branch},
		askpass.Detail{Label: "Forge", Value: kind.Label + " · " + address},
		askpass.Detail{Label: "Project", Value: project},
	)
	return approval.Request{
		Owner:    owner,
		Kind:     approvalForgeThread,
		Key:      strings.Join([]string{project, branch, write.Thread}, "\n"),
		What:     fmt.Sprintf("%s at %s:%d on %s %s of %s in %s", action, plaintext.Visible(target.Thread.Path), target.Thread.Line, kind.ChangeNoun, ref, plaintext.Visible(branch), project),
		Details:  details,
		Projects: []string{project},
		Run: func() (string, error) {
			ctx, cancel := context.WithTimeout(context.Background(), forgeWriteTimeout)
			defer cancel()
			p, err := s.projects.FindByName(project)
			if err != nil {
				return "", errors.New("the project no longer exists")
			}
			w, _, _, err := s.forgeWatch(ctx, p, branch)
			if err != nil {
				return "", err
			}
			if now := w.Binding.Address(); now != address {
				return "", fmt.Errorf("%q is bound to %s now, not to %s", project, now, address)
			}
			done, note, err := s.forge.WriteThread(ctx, w, write)
			if err != nil {
				return "", err
			}
			return forgeThreadText(w, done, note).Note, nil
		},
	}
}

// forgeThreadOpening is where a thread stands and the start of the note it
// opens with, the way an approval names the thread.
func forgeThreadOpening(t forge.Thread) string {
	at := fmt.Sprintf("%s:%d", t.Path, t.Line)
	if len(t.Notes) == 0 {
		return at
	}
	first := t.Notes[0]
	text, _, _ := strings.Cut(strings.TrimSpace(first.Body), "\n")
	if runes := []rune(text); len(runes) > maxThreadOpening {
		text = string(runes[:maxThreadOpening]) + "…"
	}
	return fmt.Sprintf("%s · %s: %s", at, first.Author, text)
}

// forgeThreadText is what an assistant reads of a write on a thread that went
// through, in the answer and in the note of an approval.
func forgeThreadText(w forge.Watch, done forge.ThreadTarget, note *forge.Note) assistant.ForgeOutcome {
	kind := w.Binding.Host.Kind
	data := assistant.ForgeThread{
		Forge:    kind.Label,
		Address:  w.Binding.Address(),
		Noun:     kind.ChangeNoun,
		Ref:      kind.ChangeRef(done.Change.Number),
		Title:    done.Change.Title,
		Branch:   w.Branch,
		Project:  w.Project,
		Path:     done.Thread.Path,
		Line:     done.Thread.Line,
		Replied:  note != nil,
		Resolved: done.Thread.Resolved,
	}
	if note != nil {
		data.URL = note.URL
	}
	return assistant.ForgeThreadWritten(data)
}

// forgeThreadMessage is what the user reads of a write on a thread a click
// ran.
func forgeThreadMessage(kind forge.Kind, write forge.ThreadWrite, done forge.ThreadTarget, note *forge.Note) string {
	on := fmt.Sprintf("%s %s", kind.ChangeNoun, kind.ChangeRef(done.Change.Number))
	switch {
	case note != nil && write.Resolve:
		return fmt.Sprintf("The reply is posted on %s, the thread is resolved.", on)
	case note != nil:
		return fmt.Sprintf("The reply is posted on %s.", on)
	}
	return fmt.Sprintf("The thread is resolved on %s.", on)
}
