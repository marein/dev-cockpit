package web

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// handleProjectForgeBranch answers the editor's forge sheet and its button:
// what the forge poller holds of the branch HEAD of the project is on, the
// badge of its row with the pipeline's jobs, the change request it offers to
// open, the one the line comments publish on and the one whose review threads
// they answer and resolve, nothing while it holds nothing. It reads the
// poller's cache alone.
func (s *Server) handleProjectForgeBranch(c *gin.Context) {
	p, err := s.projects.FindByName(c.Param("name"))
	if err != nil {
		c.String(http.StatusNotFound, "Unknown project.")
		return
	}
	var data render.ForgeBranchData
	if b, polled, ok := s.forgePolledOf(p); ok {
		replies, resolves := s.forge.AnswersThreads(b)
		data = render.ForgeBranchData{
			Kind:    b.Host.Kind,
			Badge:   render.NewForgeBadge(b, polled),
			Offer:   render.NewForgeChangeOffer(p.Name, b, polled),
			Publish: render.NewForgePublishOffer(b, polled, s.forge.Publishes(b)),
			Threads: render.NewForgeThreadsOffer(b, polled, replies, resolves),
		}
	}
	c.HTML(http.StatusOK, "forge_branch.gohtml", data)
}

// handleProjectForgeLog answers the end of the log of one job, named by its
// id in the query, of the pipeline the forge poller holds of the branch HEAD
// of the project is on, with its run again where it failed. The job comes
// from the poller's cache, its log from the forge, read when somebody asks
// for it, and while the job runs the answer asks to be read again.
func (s *Server) handleProjectForgeLog(c *gin.Context) {
	p, err := s.projects.FindByName(c.Param("name"))
	if err != nil {
		c.String(http.StatusNotFound, "Unknown project.")
		return
	}
	id := strings.TrimSpace(c.Query("job"))
	if utf8.RuneCountInString(id) > maxForgeJobID {
		c.String(http.StatusBadRequest, fmt.Sprintf("A job id has at most %d characters.", maxForgeJobID))
		return
	}
	b, polled, ok := s.forgePolledOf(p)
	var job forge.Job
	if ok && polled.Status.Pipeline != nil {
		job, ok = polled.Status.Pipeline.Job(id)
	} else {
		ok = false
	}
	if !ok {
		c.String(http.StatusNotFound, fmt.Sprintf("The pipeline of %q holds no job %q any more.", p.GitBranch, id))
		return
	}
	read := s.forge.JobLogs(c.Request.Context(), b, []forge.Job{job}, forge.DefaultLogLines)[0]
	data := render.ForgeLogData{Job: render.ForgeJob{Job: job, Forge: b.Host.Kind.Label}, Lines: read.Lines, Cut: read.Cut, Retry: render.NewForgeBadge(b, polled).JobRetry(job)}
	var later *forge.LogLater
	switch {
	case errors.As(read.Err, &later):
		data.Later = later.Error()
	case read.Err != nil:
		data.Err = read.Err.Error()
	case read.Live:
		data.Live = forge.LiveLogPace
	}
	c.HTML(http.StatusOK, "forge_log.gohtml", data)
}

// forgePolledOf answers where p is bound and what the forge poller holds of
// the branch HEAD of p is on there, false when it holds nothing.
func (s *Server) forgePolledOf(p project.Project) (forge.Binding, forge.Polled, bool) {
	projects := []project.Project{p}
	bound := s.forgeByProject(projects)
	polled, ok := s.forgePolled(projects, bound)[p.Name]
	return bound[p.Name], polled, ok
}
