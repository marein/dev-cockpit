package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/activity"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// assistantJobsPath is where the steered jobs live: the list, the two actions
// on them, and the path `dev-cockpit assistant coder-steer` posts to. One path
// for every assistant's jobs, because a terminal carries at most one job and
// who may steer it is a question about all of them at once. Which assistant a
// job belongs to travels in the request and in the answer, never in the URL.
const assistantJobsPath = "/assistants/jobs"

// handleAssistantJobs serves the job list on its own, so a check that changed a
// job reaches the open page without a reload. The `assistant` query narrows it
// to one assistant's jobs, which is what its own page asks for, and `all`
// lifts the cap on the closed tail, which is what a tap on the count asks for.
func (s *Server) handleAssistantJobs(c *gin.Context) {
	jobs, older := s.assistantJobViews(strings.TrimSpace(c.Query("assistant")), listsAll(c))
	c.HTML(http.StatusOK, "assistant_jobs_list.gohtml", render.AssistantData{
		Jobs:      jobs,
		JobsOpen:  openJobs(jobs),
		JobsOlder: older,
		JobsURL:   assistantJobsPath,
		Page:      render.Page{CSRFToken: s.csrfToken(c)},
	})
}

// handleAssistantJobsAction is the POST side of that path. It dispatches on the
// hidden form field like every other form of the cockpit, and serves the page's
// buttons and the assistant's own commands through the same handlers.
func (s *Server) handleAssistantJobsAction(c *gin.Context) {
	switch strings.TrimSpace(c.PostForm("form")) {
	case "steer":
		s.assistantSteer(c)
	case "release":
		s.assistantRelease(c)
	default:
		s.renderError(c, http.StatusBadRequest, "Unknown action", "That action isn't available on this page.")
	}
}

// openJobs is how many jobs still wake the assistant, which is what the button
// on the page carries without opening the list.
func openJobs(jobs []render.AssistantJobView) int {
	count := 0
	for _, job := range jobs {
		if job.Open {
			count++
		}
	}
	return count
}

// closedJobsShown bounds the closed tail of the job list, the same number the
// `job-list` command uses: the jobs are the assistant's now and a host collects
// them for weeks, while a closed job is only history. What is dropped is shown
// as a count, never silently.
const closedJobsShown = 5

// listsAll reads the `all` query a list of jobs or triggers is asked with: the
// whole history instead of its capped tail.
func listsAll(c *gin.Context) bool {
	all, _ := strconv.ParseBool(c.Query("all"))
	return all
}

// stoppedCoders are the coders with no session running that can be resumed,
// by id, which is what a job's and a trigger's row resume instead of opening.
func (s *Server) stoppedCoders() map[string]bool {
	stopped := map[string]bool{}
	for _, co := range s.coders {
		for _, session := range co.Snapshot().Inactive {
			stopped[session.SessionID] = true
		}
	}
	return stopped
}

// assistantJobViews are the coders an assistant steers, open jobs first, the
// closed tail capped unless all lifts it. An empty owner is every assistant's
// jobs, which is what a surface that is not one assistant's page shows.
// Returns the views and how many closed jobs were dropped.
func (s *Server) assistantJobViews(owner string, all bool) ([]render.AssistantJobView, int) {
	jobs := s.watcher.List()
	if owner != "" {
		jobs = s.watcher.ListOf(owner)
	}
	stopped := s.stoppedCoders()
	out := make([]render.AssistantJobView, 0, len(jobs))
	closed, older := 0, 0
	for _, w := range jobs {
		// The list is sorted open first and newest first, so everything past
		// the cap is the oldest closed history.
		if !w.State.Open() {
			closed++
			if !all && closed > closedJobsShown {
				older++
				continue
			}
		}
		view := render.AssistantJobView{
			Terminal: w.Terminal,
			OwnerID:  w.Owner,
			Name:     w.Name,
			Project:  w.Project,
			Task:     w.Task,
			DoneWhen: w.DoneWhen,
			State:    string(w.State),
			Open:     w.State.Open(),
			Checking: activity.Mark(w.CheckingSince),
			Note:     w.Note,
			Wakes:    w.Wakes,
			MaxWakes: w.MaxWakes,
			Expires:  machineTime(w.ExpiresAt),
			URL:      "/coders/" + w.Terminal,
			Stopped:  stopped[w.Terminal],
		}
		if w.Project != "" {
			view.EditorURL = "/projects/" + url.PathEscape(w.Project) + "/editor"
		}
		// Only a list that spans several assistants says whose job this is. On
		// one assistant's own page the answer is always the same and the line
		// would be noise on every row.
		if owner == "" {
			view.Owner = s.assistantName(w.Owner)
		}
		out = append(out, view)
	}
	return out, older
}

// assistantSteer starts steering a terminal. Both callers come through here:
// the page's button and an assistant's own `dev-cockpit assistant coder-steer`.
//
// A job needs an owner, because its checks wake exactly that assistant and its
// reports land in exactly that thread. An assistant steering names itself by
// being the caller; the user steering from the page says which assistant it is
// for, and the dialog only asks when there is more than one to choose from.
func (s *Server) assistantSteer(c *gin.Context) {
	// Who is steering comes before what is being steered: a caller that cannot
	// be attributed is refused over the call itself, not over the terminal it
	// happened to name, so the sentence it reads is the one it can act on.
	owner, err := s.steerOwner(c)
	if err != nil {
		s.assistantJobError(c, err)
		return
	}
	target, err := s.assistantSteerTarget(c.PostForm("terminal"))
	if err != nil {
		s.assistantJobError(c, err)
		return
	}
	// The page may leave the criterion empty, the check then judges against
	// the session's own task. An assistant's own command may not: it is the
	// one caller that can write a checkable criterion, so the requirement
	// stays at its door, decided by the surface like every ownership question.
	if s.localCall(c) {
		if _, err := assistant.ValidateDoneWhen(c.PostForm("done_when")); err != nil {
			s.assistantJobError(c, err)
			return
		}
	}
	job, err := s.watcher.Steer(assistant.Job{
		Owner:    owner,
		Terminal: target.Identifier,
		Name:     target.Name,
		Project:  target.Project,
		CoderID:  target.CoderID,
		Task:     c.PostForm("task"),
		DoneWhen: c.PostForm("done_when"),
	})
	if err != nil {
		s.assistantJobError(c, err)
		return
	}
	// A task over its bound was stored cut, and the caller has to hear that.
	// The notice comes from the rule Add applied, so every path says the same
	// sentence.
	_, taskNotice := assistant.TruncateTask(c.PostForm("task"))
	if wantsJSON(c.Request) {
		answer := gin.H{
			"terminal":  job.Terminal,
			"name":      job.Name,
			"maxWakes":  job.MaxWakes,
			"expiresAt": job.ExpiresAt,
		}
		if taskNotice != "" {
			answer["notice"] = taskNotice
		}
		c.JSON(http.StatusOK, answer)
		return
	}
	flash := "Steering " + job.Name + "."
	if taskNotice != "" {
		flash = "Steering " + job.Name + ", " + taskNotice + "."
	}
	s.redirectWithFlash(c, "/projects", flash, "")
}

// steerOwner is the assistant a new job belongs to. A turn steering is itself,
// and it has to be able to say which one it is: a call over the socket that
// names none is refused rather than charged to whoever the user last looked at.
// The user steering picks one, and the dialog only asks where there is a
// choice: with a single assistant the form carries none and the only one there
// is takes the job. With several and no pick the steer is refused rather than
// charged to one of them, and with none there is no job at all: a check has to
// be able to wake somebody, and a job nothing wakes is the promise this feature
// exists to keep.
func (s *Server) steerOwner(c *gin.Context) (string, error) {
	if s.localCall(c) {
		return s.assistantCaller(c)
	}
	if asked := strings.TrimSpace(c.PostForm("assistant")); asked != "" {
		if _, err := s.assistants.Get(asked); err != nil {
			return "", errors.New("That assistant does not exist any more.")
		}
		return asked, nil
	}
	switch live := s.assistants.List(); len(live) {
	case 0:
		return "", errors.New("There is no assistant to steer this coder. Start one first.")
	case 1:
		return live[0].ID, nil
	}
	return "", errors.New("Say which assistant this job reports to.")
}

// assistantRelease calls a job off, from the page's Stop button or from
// `dev-cockpit assistant coder-release`. The user may call any job off, an
// assistant only its own: taking somebody else's coder away is the user's
// decision, and the refusal names who holds it.
func (s *Server) assistantRelease(c *gin.Context) {
	terminal := strings.TrimSpace(c.PostForm("terminal"))
	// Who is calling decides what they may call off. A browser is the user and
	// may call off any job; a call over the socket is a turn and may call off
	// only its own, so one that cannot say which assistant it is gets no say at
	// all instead of the user's.
	by := ""
	if s.localCall(c) {
		from, err := s.assistantCaller(c)
		if err != nil {
			s.assistantJobError(c, err)
			return
		}
		by = from
	}
	if err := s.watcher.Release(terminal, by); err != nil {
		s.assistantJobError(c, err)
		return
	}
	if wantsJSON(c.Request) {
		c.JSON(http.StatusOK, gin.H{"terminal": terminal, "state": string(assistant.JobStopped)})
		return
	}
	s.redirectWithFlash(c, "/projects", "The coder is released.", "")
}

// assistantJobError answers a refused job action. The page's job list posts
// with fetch and shows the sentence in a toast, the command line prints it.
func (s *Server) assistantJobError(c *gin.Context, err error) {
	if wantsJSON(c.Request) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s.redirectWithFlash(c, "/projects", "", err.Error())
}

// steerTarget is the running coder a job steers, resolved to what the job
// stores about it.
type steerTarget struct {
	Identifier string
	Name       string
	Project    string
	CoderID    string
}

// assistantSteerTarget resolves the terminal a job steers. Only a running
// coder can be steered: a shell produces no report worth a turn, and a coder
// that is not running has nothing to say yet.
func (s *Server) assistantSteerTarget(raw string) (steerTarget, error) {
	id := strings.TrimSpace(raw)
	if id == "" {
		return steerTarget{}, errors.New("A job needs the terminal it steers.")
	}
	for _, m := range s.coders {
		for _, running := range m.Snapshot().Running {
			if running.Identifier != id {
				continue
			}
			return steerTarget{
				Identifier: running.Identifier,
				Name:       running.Name,
				Project:    s.projects.ProjectNameFor(running.CWD),
				CoderID:    m.ID(),
			}, nil
		}
	}
	return steerTarget{}, fmt.Errorf("No running coder with id %q.", id)
}
