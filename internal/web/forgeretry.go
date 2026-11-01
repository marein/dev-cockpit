package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/approval"
	"github.com/marein/dev-cockpit/internal/askpass"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/plaintext"
)

// maxForgeHead bounds the commit a retry names, a sha256 in hex at most.
const maxForgeHead = 64

// forgeWriteTimeout bounds an approved write on a forge, which reads the
// branch from the forge again before it asks the forge to write anything.
const forgeWriteTimeout = time.Minute

// handleProjectForgeRetry runs failed jobs again on the forge a project is
// bound to: the ones that failed the pipeline of a branch's pushed head, or
// the one job of it the form names. The branch is the one HEAD is on unless
// the form names another, and the head and the pipeline the form names, the
// ones a click saw, have to be the ones that stand: the forge is read again
// first. A click in the browser runs it at once. An assistant's call over
// the local socket waits for the user's approval, whose run resolves the
// project, its binding, the branch, the pipeline and the job again by name.
func (s *Server) handleProjectForgeRetry(c *gin.Context) {
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
	ask := forge.Retry{Head: strings.TrimSpace(c.PostForm("head")), Pipeline: strings.TrimSpace(c.PostForm("pipeline")), Job: strings.TrimSpace(c.PostForm("job"))}
	for _, bound := range []struct {
		value, refusal string
		max            int
	}{
		{ask.Head, "A commit has at most %d characters.", maxForgeHead},
		{ask.Pipeline, "A pipeline id has at most %d characters.", maxForgeJobID},
		{ask.Job, "A job id has at most %d characters.", maxForgeJobID},
	} {
		if utf8.RuneCountInString(bound.value) > bound.max {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf(bound.refusal, bound.max)})
			return
		}
	}
	w, _, code, err := s.forgeWatch(c.Request.Context(), p, c.PostForm("branch"))
	if err != nil {
		c.JSON(code, gin.H{"error": err.Error()})
		return
	}
	if owner != "" {
		target, err := s.forge.RetryTarget(c.Request.Context(), w, ask)
		if err != nil {
			c.JSON(forgeRetryCode(err), gin.H{"error": err.Error()})
			return
		}
		if s.askApproval(c, s.forgeRetryApproval(owner, w, target)) {
			return
		}
		ask = target.Retry()
	}
	done, err := s.forge.Retry(c.Request.Context(), w, ask)
	if err != nil {
		c.JSON(forgeRetryCode(err), gin.H{"error": err.Error()})
		return
	}
	if owner != "" {
		c.JSON(http.StatusOK, gin.H{"ok": true, "retried": forgeRetried(w, done).Plain})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "message": forgeRetriedMessage(done)})
}

// forgeRetryCode is the status a failed retry answers: a conflict where the
// forge was not asked to run anything, a bad gateway where it refused.
func forgeRetryCode(err error) int {
	var refused *forge.RetryRefusal
	var off *forge.BranchOff
	switch {
	case errors.As(err, &refused), errors.As(err, &off), errors.Is(err, forge.ErrNoToken), errors.Is(err, forge.ErrNoStatus), errors.Is(err, forge.ErrNoRetry):
		return http.StatusConflict
	}
	return http.StatusBadGateway
}

// forgeRetryApproval is the approval an assistant's retry waits for. The same
// retry of the same pipeline waits once. Its run resolves the project, its
// binding and the branch again by name and has the forge read the pipeline
// again: a branch pushed again, a pipeline that is another one or a job
// that ran again meanwhile refuses it, and so does a project bound elsewhere.
func (s *Server) forgeRetryApproval(owner string, w forge.Watch, target forge.Retried) approval.Request {
	kind := w.Binding.Host.Kind
	named := target.Retry()
	project, branch, address := w.Project, w.Branch, w.Binding.Address()
	pipeline := forgePipelineName(target)
	what := fmt.Sprintf("%s failed jobs of %s on %s in %s", kind.Retry, pipeline, plaintext.Visible(branch), project)
	details := []askpass.Detail{{Label: "Action", Value: kind.Retry + " failed jobs"}}
	if job := target.Job; job != nil {
		what = fmt.Sprintf("%s job %q of %s on %s in %s", kind.Retry, job.Name, pipeline, plaintext.Visible(branch), project)
		details = []askpass.Detail{{Label: "Action", Value: kind.Retry + " job"}, {Label: "Job", Value: job.Name}}
	} else {
		var names []string
		for _, j := range target.Pipeline.Jobs {
			if j.Status == forge.Failed {
				names = append(names, j.Name)
			}
		}
		details = append(details, askpass.Detail{Label: "Failed jobs", Value: strings.Join(names, ", ")})
		if left := target.Pipeline.LeftOut(); len(left) > 0 {
			details = append(details, askpass.Detail{Label: "Left as they are", Value: forge.Unretried(left)})
		}
	}
	details = append(details,
		askpass.Detail{Label: "Pipeline", Value: forgePipelineRow(target)},
		askpass.Detail{Label: "Branch", Value: branch},
		askpass.Detail{Label: "Forge", Value: kind.Label + " · " + address},
		askpass.Detail{Label: "Project", Value: project},
	)
	return approval.Request{
		Owner:    owner,
		Kind:     approvalForgeRetry,
		Key:      strings.Join([]string{project, branch, named.Head, named.Pipeline, named.Job}, "\n"),
		What:     what,
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
			done, err := s.forge.Retry(ctx, w, named)
			if err != nil {
				return "", err
			}
			return forgeRetried(w, done).Note, nil
		},
	}
}

// forgeRetried is what an assistant reads of a retry that went through, in
// the answer and in the note of an approval.
func forgeRetried(w forge.Watch, done forge.Retried) assistant.ForgeOutcome {
	data := assistant.ForgeRetry{
		Forge:    w.Binding.Host.Kind.Label,
		Address:  w.Binding.Address(),
		Pipeline: forgePipelineName(done),
		Branch:   w.Branch,
		Project:  w.Project,
		URL:      done.Pipeline.URL,
	}
	if done.Job != nil {
		data.Job, data.URL = done.Job.Name, done.Job.URL
	} else if left := done.Pipeline.LeftOut(); len(left) > 0 {
		data.LeftOut = forge.Unretried(left)
	}
	return assistant.ForgeRetried(data)
}

// forgeRetriedMessage is what the user reads of a retry a click ran.
func forgeRetriedMessage(done forge.Retried) string {
	if done.Job != nil {
		return fmt.Sprintf("The job %q runs again.", done.Job.Name)
	}
	return fmt.Sprintf("The failed jobs of %s run again.", forgePipelineName(done))
}

// forgePipelineName names the pipeline a retry runs again, by its number
// where the forge numbers pipelines, else by the commit it ran on.
func forgePipelineName(r forge.Retried) string {
	if r.Pipeline.ID != "" {
		return "pipeline #" + r.Pipeline.ID
	}
	return "the pipeline at " + forge.ShortHead(r.Head)
}

// forgePipelineRow is the pipeline's row in the approval dialog.
func forgePipelineRow(r forge.Retried) string {
	at := "#" + r.Pipeline.ID
	if r.Pipeline.ID == "" {
		at = forge.ShortHead(r.Head)
	}
	if r.Pipeline.URL != "" {
		return at + " · " + r.Pipeline.URL
	}
	return at
}
