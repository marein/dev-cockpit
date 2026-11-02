package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/git"
	"github.com/marein/dev-cockpit/internal/project"
)

// maxForgeJobID bounds the job id a status read names for its log.
const maxForgeJobID = 64

// forgeFailedLogs is the log query that asks for the logs of the jobs that
// failed the pipeline instead of one job's.
const forgeFailedLogs = "failed"

// forgeRemote is the remote a binding is read off, so its tracking refs
// are where a branch was last pushed.
const forgeRemote = "origin"

// forgeUnbound is the refusal of a project that is bound to no forge.
func forgeUnbound(p project.Project, bindings forge.Bindings) error {
	if bindings.Override(forgeOwner(p)).How == forge.Off {
		return fmt.Errorf("%q is bound to no forge: its Forge is switched off.", p.Name)
	}
	return fmt.Errorf("%q is bound to no forge: its origin names no forge host and none is set under its Forge.", p.Name)
}

// handleProjectForgeStatus answers what the forge a project is bound to says
// about one of its branches, the one HEAD is on unless the query names
// another: the change request and the pipeline of the commit origin's
// tracking ref names. The query's log asks for the end of job logs too, of
// the first jobs that failed the pipeline or of the one job it names, lines
// for how many lines of each, its threads for the review threads of the
// change request, which are always read from the forge. It is the local API's
// read behind forge-show and reads what the forge poller read of the branch
// where that is fresh, else asks the forge, which the query's fresh always
// does.
func (s *Server) handleProjectForgeStatus(c *gin.Context) {
	p, err := s.projects.FindByName(c.Param("name"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Unknown project."})
		return
	}
	logOf := strings.TrimSpace(c.Query("log"))
	if utf8.RuneCountInString(logOf) > maxForgeJobID {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("A job id has at most %d characters.", maxForgeJobID)})
		return
	}
	lines := forge.DefaultLogLines
	if raw := strings.TrimSpace(c.Query("lines")); raw != "" {
		if lines, err = strconv.Atoi(raw); err != nil || lines < 1 || lines > forge.MaxLogLines {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("The lines of a log are a number from 1 to %d.", forge.MaxLogLines)})
			return
		}
	}
	watch, local, code, err := s.forgeWatch(c.Request.Context(), p, c.Query("branch"))
	if err != nil {
		c.JSON(code, gin.H{"error": err.Error()})
		return
	}
	b := watch.Binding
	status, err := s.forge.Status(c.Request.Context(), watch, c.Query("fresh") == "1")
	var off *forge.BranchOff
	switch {
	case errors.Is(err, forge.ErrNoToken), errors.Is(err, forge.ErrNoStatus), errors.As(err, &off):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	case err != nil:
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	var logJobs []forge.Job
	logsLeft := 0
	switch {
	case logOf == forgeFailedLogs && status.Pipeline != nil:
		logJobs = status.Pipeline.Failing()
		if len(logJobs) > forge.MaxFailedLogs {
			logJobs, logsLeft = logJobs[:forge.MaxFailedLogs], len(logJobs)-forge.MaxFailedLogs
		}
	case logOf != "" && logOf != forgeFailedLogs:
		if status.Pipeline == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("%q has no pipeline at its pushed head, so no job %q.", watch.Branch, logOf)})
			return
		}
		job, ok := status.Pipeline.Job(logOf)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("The pipeline of %q has no job %q.", watch.Branch, logOf)})
			return
		}
		logJobs = []forge.Job{job}
	}
	kind := b.Host.Kind
	answer := gin.H{
		"project":      p.Name,
		"forge":        gin.H{"label": kind.Label, "address": b.Address(), "url": b.WebURL(), "changeNoun": kind.ChangeNoun},
		"branch":       watch.Branch,
		"remoteBranch": watch.Remote,
		"head":         watch.Head,
		"local":        local,
		"change":       nil,
		"pipeline":     nil,
		"logs":         nil,
	}
	if ch := status.Change; ch != nil {
		answer["change"] = gin.H{"ref": kind.ChangeRef(ch.Number), "title": ch.Title, "draft": ch.Draft, "state": ch.State, "url": ch.URL, "target": ch.Target, "head": ch.Head}
	}
	if pl := status.Pipeline; pl != nil {
		jobs := make([]gin.H, 0, len(pl.Jobs))
		for _, j := range pl.Jobs {
			jobs = append(jobs, gin.H{"id": j.ID, "name": j.Name, "stage": j.Stage, "status": j.Status, "url": j.URL, "allowFailure": j.AllowFailure, "noLog": j.NoLog, "noRetry": j.NoRetry})
		}
		answer["pipeline"] = gin.H{"id": pl.ID, "status": pl.Status, "url": pl.URL, "jobs": jobs, "partial": pl.Partial}
	}
	if logOf != "" {
		logs := make([]gin.H, 0, len(logJobs))
		for _, l := range s.forge.JobLogs(c.Request.Context(), b, logJobs, lines) {
			entry := gin.H{"id": l.Job.ID, "name": l.Job.Name, "lines": l.Lines, "cut": l.Cut, "error": ""}
			if l.Err != nil {
				entry["error"] = l.Err.Error()
			}
			logs = append(logs, entry)
		}
		answer["logs"] = logs
		answer["logsLeft"] = logsLeft
	}
	if c.Query("threads") == "1" {
		answer["review"] = s.forgeReview(c.Request.Context(), b, status.Change)
	}
	c.JSON(http.StatusOK, answer)
}

// forgeWatch resolves the branch of p a forge read or write names, the one
// HEAD is on when branch is "", to where it was last pushed on the forge p is
// bound to: the commit origin's tracking ref names, and the local branch's
// own commit. A branch the forge features are off for is refused before any
// answer names it. A refusal comes with the status it answers.
func (s *Server) forgeWatch(ctx context.Context, p project.Project, branch string) (forge.Watch, string, int, error) {
	if !p.GitRepo {
		return forge.Watch{}, "", http.StatusBadRequest, errors.New(forgeRefused(p))
	}
	bindings, err := s.forge.Bindings()
	if err != nil {
		return forge.Watch{}, "", http.StatusServiceUnavailable, err
	}
	b, ok := bindings.Bind(forgeOwner(p), p.GitOriginRaw)
	if !ok {
		return forge.Watch{}, "", http.StatusConflict, forgeUnbound(p, bindings)
	}
	branch = strings.TrimSpace(branch)
	if refusal := forge.BranchRefusal(branch); refusal != "" {
		return forge.Watch{}, "", http.StatusBadRequest, errors.New(refusal)
	}
	pushed, err := git.New(p.Path).PushedHead(ctx, forgeRemote, branch)
	switch {
	case errors.Is(err, git.ErrDetached):
		return forge.Watch{}, "", http.StatusConflict, fmt.Errorf("HEAD of %q is detached, name the branch.", p.Name)
	case errors.Is(err, git.ErrNoAnswer):
		return forge.Watch{}, "", http.StatusServiceUnavailable, err
	case err != nil:
		return forge.Watch{}, "", http.StatusBadRequest, err
	}
	w := forge.Watch{Project: p.Name, Binding: b, Branch: pushed.Branch, Remote: pushed.Remote, Head: pushed.Head}
	if err := w.Refused(); err != nil {
		return forge.Watch{}, "", http.StatusConflict, err
	}
	return w, pushed.Local, http.StatusOK, nil
}

// forgeReview is the answer's review threads of change, nil without a change
// request, the reason in their place where the forge could not be read.
func (s *Server) forgeReview(ctx context.Context, b forge.Binding, change *forge.Change) gin.H {
	if change == nil {
		return nil
	}
	review, err := s.forge.Threads(ctx, b, change.Number)
	if err != nil {
		return gin.H{"error": err.Error()}
	}
	return gin.H{"diff": forgeThreads(review.Diff), "general": forgeThreads(review.General), "partial": review.Partial, "error": ""}
}

// forgeThreads are threads the way forge-show reads them, each with the
// forge's id, reply where it takes a reply and resolvable where it takes a
// resolve.
func forgeThreads(threads []forge.Thread) []gin.H {
	out := make([]gin.H, 0, len(threads))
	for _, t := range threads {
		notes := make([]gin.H, 0, len(t.Notes))
		for _, n := range t.Notes {
			notes = append(notes, gin.H{"id": n.ID, "author": n.Author, "body": n.Body, "created": n.Created, "url": n.URL})
		}
		reply, resolve := t.Takes()
		out = append(out, gin.H{"id": t.ID, "path": t.Path, "line": t.Line, "side": t.Side, "outdated": t.Outdated, "reply": reply, "resolvable": resolve, "resolved": t.Resolved, "notes": notes})
	}
	return out
}
