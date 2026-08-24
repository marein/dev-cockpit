package web

import (
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/terminal"
	"github.com/marein/dev-cockpit/internal/web/render"
)

type coderCreateForm struct {
	Name              AlphaNumDashString `form:"name" binding:"required"`
	Project           string             `form:"project" binding:"required"`
	Coder             string             `form:"coder"`
	Agent             string             `form:"agent"`
	AutomaticApproval CheckboxBool       `form:"automatic_approval"`
	// Task is the first prompt the session starts with. It reaches the CLI in
	// its argv, so it arrives whether or not the CLI already reads stdin.
	Task string `form:"prompt"`
	// DoneWhen asks for the new coder to be steered, in the same request that
	// creates it. The criterion is validated before the session exists: a
	// criterion the watcher would refuse must not leave a running coder
	// without its job, and a repeated call would start a second session on
	// the same task.
	DoneWhen string `form:"done_when"`
}

type terminalInputItem struct {
	Prompt  string `json:"prompt"`
	Control string `json:"control"`
	Text    string `json:"text"`
	Paste   string `json:"paste"`
	Raw     string `json:"raw"`
}

type terminalInputBatch struct {
	Items []terminalInputItem `json:"items"`
}

// maxTerminalInputItems bounds one input flush so a single request can't pin the
// handler in a long run of tmux sends. Real typing bursts stay far below this.
const maxTerminalInputItems = 1024

type terminalResizeForm struct {
	Cols       string `form:"cols" binding:"required"`
	Rows       string `form:"rows" binding:"required"`
	Background string `form:"bg"`
	Foreground string `form:"fg"`
}

func (s *Server) handleCoderNew(c *gin.Context) {
	// Only the project the form was opened from preselects an option. Without
	// one the server marks none, so the browser takes the first one, which the
	// select ends up with in the order the projects page is in.
	defaultPath := ""
	if name := strings.TrimSpace(c.Query("project")); name != "" {
		if p, err := s.projects.FindByName(name); err == nil {
			defaultPath = p.Path
		}
	}
	selected, err := s.coderFromRequest(c)
	if err != nil {
		selected = s.coders[0]
	}
	coders := make([]render.CoderChoice, 0, len(s.coders))
	for i := range s.coders {
		co := s.coders[i]
		defaultAgent := ""
		if agentID, err := co.Coder().AgentRepository().ValidateSelected(c.Query("agent")); err == nil {
			defaultAgent = agentID
		}
		coders = append(coders, render.CoderChoice{
			ID:           co.ID(),
			Agents:       co.Coder().AgentRepository().Options(),
			DefaultAgent: defaultAgent,
		})
	}
	// A coder needs its whole form, so a split scoped create opens it
	// prefilled and carries the group target through as hidden fields.
	target := splitTargetFromRequest(c)
	page := s.page(c, "New Coder", "projects")
	c.HTML(http.StatusOK, "coders_new.gohtml", render.CoderNewData{
		Page:              page,
		Projects:          render.ProjectOptions(page.QuickNav.AllProjects),
		DefaultPath:       defaultPath,
		Coders:            coders,
		SelectedCoder:     selected.ID(),
		AutomaticApproval: true,
		Return:            s.formReturn(c),
		Panel:             c.Query("panel") == "1",
		SplitGroup:        target.Group,
		SplitColumn:       target.Column,
	})
}

func (s *Server) handleCoderAttach(c *gin.Context) {
	id := c.Param("id")
	co, running, err := s.resolveRunning(id)
	if err != nil {
		s.redirectWithFlash(c, "/projects", "", err.Error())
		return
	}
	// A grouped session lives on the split page; every link to its own page
	// lands there with its pane focused.
	if url, ok := s.splitPageURL(running.TabGroup, running.Identifier); ok {
		c.Redirect(http.StatusSeeOther, url)
		return
	}
	files, err := co.Coder().SessionRepository().ListFiles(running.Identifier)
	if err != nil {
		s.redirectWithFlash(c, "/projects", "", err.Error())
		return
	}
	projectName := s.projects.ProjectNameFor(running.CWD)
	s.projects.Touch(projectName)
	s.notifier.MarkTargetRead(running.Identifier)
	page := s.page(c, pageTitle(running.Name, projectName), "projects")
	page.HasTabStrip = true
	c.HTML(http.StatusOK, "coder_attach.gohtml", render.CoderAttachData{
		Page:            page,
		Running:         running,
		Identifier:      running.Identifier,
		Coder:           co.ID(),
		ProjectName:     projectName,
		Files:           files,
		MaxUploadSizeMB: maxRequestBodyMegabytes(s.cfg.MaxRequestBodySize),
		StreamURL:       "/coders/" + running.Identifier + "/stream",
		ResizeURL:       "/coders/" + running.Identifier + "/resize",
		InputURL:        "/coders/" + running.Identifier + "/input",
	})
}

func (s *Server) handleCoderCreate(c *gin.Context) {
	var form coderCreateForm
	if !s.decodeForm(c, &form, "/coders/new") {
		return
	}
	co, err := s.coderFromRequest(c)
	if err != nil {
		s.redirectWithFlash(c, "/coders/new", "", err.Error())
		return
	}
	// The criterion is checked before anything exists, with the watcher's own
	// rule: refused afterwards it would leave a running coder without its job,
	// and whoever repeats the command then has two sessions on the same task.
	doneWhen := ""
	if strings.TrimSpace(form.DoneWhen) != "" {
		doneWhen, err = assistant.ValidateDoneWhen(form.DoneWhen)
		if err != nil {
			if wantsJSON(c.Request) {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			s.redirectWithFlash(c, "/coders/new", "", err.Error())
			return
		}
	}
	res, err := co.Start(
		form.Name.String(),
		form.Project,
		form.Agent,
		coder.StartOptions{
			AutomaticApproval: form.AutomaticApproval.Bool(),
			Task:              form.Task,
		},
	)
	if err != nil {
		if wantsJSON(c.Request) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		s.redirectWithFlash(c, "/coders/new", "", err.Error())
		return
	}
	s.styleSessionPane(res.Identifier)
	joinErr := s.joinSplit(res.Identifier, splitTargetFromRequest(c))
	s.invalidateTerminals()
	s.publishTerminals(s.projects.ProjectNameFor(res.Workdir))
	answer := gin.H{
		"id":      res.Identifier,
		"project": s.projects.ProjectNameFor(res.Workdir),
		"url":     "/coders/" + res.Identifier,
	}
	if doneWhen != "" {
		job, err := s.watcher.Steer(assistant.Job{
			Terminal: res.Identifier,
			Name:     res.Name,
			Project:  s.projects.ProjectNameFor(res.Workdir),
			CoderID:  co.ID(),
			Task:     form.Task,
			DoneWhen: doneWhen,
		})
		if err != nil {
			// The coder is running either way, so the answer carries it; the
			// caller has to hear that nobody steers it.
			answer["steerError"] = err.Error()
		} else {
			answer["maxWakes"] = job.MaxWakes
			// The session got the whole prompt, only the job's stored copy is
			// bounded, and a cut copy must not stay silent.
			if _, notice := assistant.TruncateTask(form.Task); notice != "" {
				answer["notice"] = notice
			}
		}
	}
	// A local caller (the assistant, through dev-cockpit assistant coder-new) needs the
	// identifier it just created, not the page a browser would follow to.
	if wantsJSON(c.Request) {
		c.JSON(http.StatusOK, answer)
		return
	}
	// A create that came from the editor terminal panel's + menu goes back
	// there: the form action carries the return target and the panel marker
	// through the POST, and the editor page hands the id back to its client as
	// data-editor-terminal to activate that tab. Only the marker earns the
	// comeback; every other caller, the quick nav on an editor page included,
	// lands on the coder's own page like a created shell does, and its editor
	// return serves the form's Cancel alone.
	if ret := s.formReturn(c); c.Query("panel") == "1" && editorReturnPath.MatchString(ret) {
		c.Redirect(http.StatusSeeOther, ret+"?terminal="+url.QueryEscape(res.Identifier))
		return
	}
	if joinErr != nil {
		// The coder runs either way; only the split view did not happen.
		s.redirectWithFlash(c, "/coders/"+res.Identifier, "", "The coder was started but could not join the split view.")
		return
	}
	c.Redirect(http.StatusSeeOther, "/coders/"+res.Identifier)
}

// editorReturnPath matches a create form's return target that is a project
// editor page.
var editorReturnPath = regexp.MustCompile(`^/projects/[^/]+/editor$`)

func (s *Server) handleCoderStop(c *gin.Context) {
	id := c.Param("id")
	co, running, err := s.resolveRunning(id)
	if err != nil {
		if coderRefused(c, err) {
			return
		}
		s.redirectWithFlash(c, "/projects", "", err.Error())
		return
	}
	project := s.projects.ProjectNameFor(running.CWD)
	name, err := co.Stop(id)
	if err != nil {
		if coderRefused(c, err) {
			return
		}
		s.redirectWithFlash(c, "/projects", "", err.Error())
		return
	}
	s.notifier.MarkTargetRead(id)
	// A stopped coder cannot report again, so its job would wait for a signal
	// that never comes. Stopping is the same decision as deleting for the job.
	s.jobCalledOff(id)
	s.publishTerminals(project)
	if s.coderJSON(c, id, name, running.CWD, projectLanding(project)) {
		return
	}
	s.redirectWithProjectFlash(c, project, "Coder \""+name+"\" stopped.", "")
}

func (s *Server) handleCoderFiles(c *gin.Context) {
	data, err := s.coderFilesData(c, c.Param("id"), "", "")
	if err != nil {
		c.HTML(http.StatusBadRequest, "coder_files_content.gohtml", render.CoderFilesData{
			Page:  s.page(c, "Files", "projects"),
			Error: err.Error(),
		})
		return
	}
	c.HTML(http.StatusOK, "coder_files_content.gohtml", data)
}

func (s *Server) handleCoderFileUpload(c *gin.Context) {
	id := c.Param("id")
	_, err := s.uploadCoderFiles(c, id)
	if err != nil {
		s.renderCoderFiles(c, id, http.StatusBadRequest, err.Error(), "")
		return
	}
	s.renderCoderFiles(c, id, http.StatusOK, "", "")
}

func (s *Server) uploadCoderFiles(c *gin.Context, id string) (int, error) {
	form, err := c.MultipartForm()
	if err != nil {
		return 0, fmt.Errorf("Please choose a file to upload.")
	}
	files := form.File["files"]
	if len(files) == 0 {
		return 0, fmt.Errorf("Please choose a file to upload.")
	}

	co, err := s.coderForSession(id)
	if err != nil {
		return 0, err
	}
	for _, header := range files {
		src, err := header.Open()
		if err != nil {
			return 0, err
		}
		_, saveErr := co.Coder().SessionRepository().SaveFile(id, header.Filename, src)
		closeErr := src.Close()
		if saveErr != nil {
			return 0, saveErr
		}
		if closeErr != nil {
			return 0, closeErr
		}
	}

	return len(files), nil
}

func (s *Server) handleCoderFileDownload(c *gin.Context) {
	id := c.Param("id")
	co, err := s.coderForSession(id)
	if err != nil {
		s.redirectWithFlash(c, "/coders/"+id, "", err.Error())
		return
	}
	file, err := co.Coder().SessionRepository().OpenFile(id, c.Query("name"))
	if err != nil {
		s.redirectWithFlash(c, "/coders/"+id, "", err.Error())
		return
	}
	defer file.Close()

	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": file.Name})
	c.DataFromReader(http.StatusOK, file.Size, "application/octet-stream", file, map[string]string{
		"Content-Disposition": disposition,
	})
}

func (s *Server) handleCoderFileDelete(c *gin.Context) {
	id := c.Param("id")
	co, err := s.coderForSession(id)
	if err != nil {
		s.renderCoderFiles(c, id, http.StatusBadRequest, err.Error(), "")
		return
	}
	file, err := co.Coder().SessionRepository().DeleteFile(id, c.PostForm("name"))
	if err != nil {
		s.renderCoderFiles(c, id, http.StatusBadRequest, err.Error(), "")
		return
	}
	s.renderCoderFiles(c, id, http.StatusOK, "", "File \""+file.Name+"\" deleted.")
}

func (s *Server) renderCoderFiles(c *gin.Context, id string, status int, errorMessage, message string) {
	data, err := s.coderFilesData(c, id, errorMessage, message)
	if err != nil {
		data = render.CoderFilesData{
			Page:  s.page(c, "Files", "projects"),
			Error: err.Error(),
		}
		status = http.StatusBadRequest
	}
	c.HTML(status, "coder_files_content.gohtml", data)
}

func (s *Server) coderFilesData(c *gin.Context, id, errorMessage, message string) (render.CoderFilesData, error) {
	co, err := s.coderForSession(id)
	if err != nil {
		return render.CoderFilesData{}, err
	}
	files, err := co.Coder().SessionRepository().ListFiles(id)
	if err != nil {
		return render.CoderFilesData{}, err
	}
	return render.CoderFilesData{
		Page:            s.page(c, "Files", "projects"),
		Identifier:      id,
		Files:           files,
		MaxUploadSizeMB: maxRequestBodyMegabytes(s.cfg.MaxRequestBodySize),
		Error:           errorMessage,
		Message:         message,
	}, nil
}

// pageTitle composes the browser title for a terminal page as "name - project",
// falling back to just the name when the working directory sits outside any project.
func pageTitle(name, projectName string) string {
	if projectName == "" {
		return name
	}
	return name + " - " + projectName
}

func maxRequestBodyMegabytes(size int64) string {
	if size <= 0 {
		return ""
	}
	mb := size / (1024 * 1024)
	if mb <= 0 {
		return "1"
	}
	return fmt.Sprintf("%d", mb)
}

func (s *Server) handleCoderInput(c *gin.Context) {
	id := c.Param("id")
	var batch terminalInputBatch
	if err := c.ShouldBindJSON(&batch); err != nil {
		c.String(http.StatusBadRequest, "Invalid input.")
		return
	}
	if len(batch.Items) > maxTerminalInputItems {
		c.String(http.StatusRequestEntityTooLarge, "Too many input items.")
		return
	}
	items := make([]terminal.Input, len(batch.Items))
	for i, item := range batch.Items {
		items[i] = terminal.Input(item)
	}
	co, err := s.coderForInput(id)
	if err != nil {
		if errors.Is(err, coder.ErrNotRunning) {
			s.coderNotRunning(c, id)
			return
		}
		c.String(http.StatusBadRequest, err.Error())
		return
	}
	if err := co.Send(id, items); err != nil {
		if errors.Is(err, coder.ErrNotRunning) {
			s.coderNotRunning(c, id)
			return
		}
		c.String(http.StatusBadRequest, err.Error())
		return
	}
	// Steering is ownership and only steer and release change it, so the
	// user's inputs are none of the job's business. An assistant send still
	// lands on the job: the standstill rule reads it, and a blocked job takes
	// it as the decision it was waiting for.
	if s.localCall(c) {
		s.watcher.NoteAssistantInput(id)
	}
	c.JSON(http.StatusOK, CoderInputAnswer)
}

// CoderInputAnswer is what a successful input POST answers. The local API
// client requires a JSON object on every 2xx (internal/localapi), so a plain
// text answer here would make `coder-send-prompt` and `coder-send-control-keys`
// report a delivered input as an error. The CLI test replays exactly this value
// instead of inventing an answer, so the two cannot drift apart.
var CoderInputAnswer = map[string]string{"status": "ok"}

// handleCoderActivity answers what a session last did and whether its turn is
// over, from the coder's own record where there is one (coder.Manager.Activity).
// It exists for the assistant's `coder-activity` command: the browser has the
// terminal itself, but a turn that wants to know where a session stands must
// not read the screen, whose input line carries the coder's own draft. The
// session may be running or stopped, the record outlives the terminal. The
// reading is capped by default; `full` lifts the cap.
func (s *Server) handleCoderActivity(c *gin.Context) {
	id := c.Param("id")
	entries, err := strconv.Atoi(c.DefaultQuery("entries", "0"))
	if err != nil || entries < 0 {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "entries has to be a number."})
		return
	}
	budget := coder.ActivityBudget
	if full, _ := strconv.ParseBool(c.DefaultQuery("full", "false")); full {
		budget = 0
	}
	for _, m := range s.coders {
		if _, err := m.ResolveRunning(id); err != nil {
			if _, err := m.ResolveResumable(id); err != nil {
				continue
			}
		}
		activity, err := m.Activity(id, entries, budget)
		if err != nil {
			c.JSON(http.StatusBadRequest, map[string]string{"error": userFacingError(c, err)})
			return
		}
		c.JSON(http.StatusOK, map[string]any{
			"text":     activity.Text,
			"finished": activity.Finished,
			"screen":   activity.Screen,
		})
		return
	}
	c.JSON(http.StatusNotFound, map[string]string{"error": fmt.Sprintf("No coder session %q is running or resumable.", id)})
}

// handleCoderSteeredMark serves the coder icon of one terminal, which is how
// the attach pages follow the terminals event: their icons do not re-render
// on it, so a dc-steered-mark element pulls this fragment instead. The icon
// is rendered here exactly like everywhere else, purple while a job holds the
// terminal, and the client only swaps it in. A terminal that is gone answers
// empty: the page showing it is stale either way.
func (s *Server) handleCoderSteeredMark(c *gin.Context) {
	id := c.Param("id")
	coderID := ""
	for _, m := range s.coders {
		if _, err := m.ResolveRunning(id); err == nil {
			coderID = m.ID()
			break
		}
	}
	if coderID == "" {
		c.Status(http.StatusOK)
		return
	}
	steered, _ := s.watcher.Marks()
	c.HTML(http.StatusOK, "steered_icon.gohtml", map[string]any{
		"ID":      id,
		"Coder":   coderID,
		"Steered": steered[id],
	})
}

func (s *Server) handleCoderResize(c *gin.Context) {
	id := c.Param("id")
	var form terminalResizeForm
	if err := c.Bind(&form); err != nil {
		return
	}
	s.updateTerminalTheme(form.Background, form.Foreground)
	co, err := s.coderForInput(id)
	if err == nil {
		err = co.Resize(id, form.Cols, form.Rows)
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": userFacingError(c, err)})
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) handleCoderResume(c *gin.Context) {
	id := c.Param("id")
	// Already running (e.g. resumed in another tab): just go to its page.
	if _, running, err := s.resolveRunning(id); err == nil {
		if s.coderJSON(c, running.Identifier, running.Name, running.CWD, "/coders/"+running.Identifier) {
			return
		}
		c.Redirect(http.StatusSeeOther, "/coders/"+running.Identifier)
		return
	}
	co, _, err := s.resolveResumable(id)
	if err != nil {
		if coderRefused(c, err) {
			return
		}
		s.redirectWithFlash(c, "/projects", "", err.Error())
		return
	}
	stored, err := co.Resume(id)
	if err != nil {
		if coderRefused(c, err) {
			return
		}
		s.redirectWithFlash(c, "/projects", "", err.Error())
		return
	}
	s.styleSessionPane(stored.SessionID)
	s.publishTerminals(s.projects.ProjectNameFor(stored.CWD))
	if s.coderJSON(c, stored.SessionID, stored.Name, stored.CWD, "/coders/"+stored.SessionID) {
		return
	}
	c.Redirect(http.StatusSeeOther, "/coders/"+stored.SessionID)
}

// coderJSON answers a caller that asked for JSON, the way the create route
// does: the identifier is what every other command takes, so a command never
// has to read it out of a redirect. landing is where a browser goes next, the
// page this action would have redirected a form to: the fetch of a JSON caller
// never follows a redirect, and the action's own URL has no GET, so a client
// that navigates to the response URL lands on "Method not allowed". Reports
// whether it answered.
func (s *Server) coderJSON(c *gin.Context, id, name, cwd, landing string) bool {
	if !wantsJSON(c.Request) {
		return false
	}
	c.JSON(http.StatusOK, gin.H{
		"id":      id,
		"name":    name,
		"project": s.projects.ProjectNameFor(cwd),
		"url":     landing,
	})
	return true
}

// projectLanding is the page a stopped or deleted coder leaves the browser on,
// the same target redirectWithProjectFlash sends a form to.
func projectLanding(project string) string {
	if project == "" {
		return "/projects"
	}
	return "/projects#project-" + project
}

// coderRefused hands a refusal to a caller that asked for JSON. A redirect plus
// a flash says nothing to a command, and the sentence is the same one the page
// would have shown.
func coderRefused(c *gin.Context, err error) bool {
	if !wantsJSON(c.Request) {
		return false
	}
	c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	return true
}

// handleCoderDelete removes a coder for good. A running one is stopped first,
// DeleteResumable refuses to touch a live session and its tmux session would
// outlive the store otherwise, so the delete entries in the menus and the swipe
// actions need no second request.
func (s *Server) handleCoderDelete(c *gin.Context) {
	id := c.Param("id")
	stopped := ""
	project := ""
	cwd := ""
	if co, running, err := s.resolveRunning(id); err == nil {
		project = s.projects.ProjectNameFor(running.CWD)
		cwd = running.CWD
		name, err := co.Stop(id)
		if err != nil {
			if coderRefused(c, err) {
				return
			}
			s.redirectWithFlash(c, "/projects", "", err.Error())
			return
		}
		stopped = name
		s.notifier.MarkTargetRead(id)
	}
	co, _, err := s.resolveResumable(id)
	if err != nil {
		// A coder stopped before it wrote a session leaves nothing to delete.
		if stopped != "" {
			s.jobDeleted(id)
			s.publishTerminals(project)
			if s.coderJSON(c, id, stopped, cwd, projectLanding(project)) {
				return
			}
			s.redirectWithProjectFlash(c, project, "Coder \""+stopped+"\" deleted.", "")
			return
		}
		if coderRefused(c, err) {
			return
		}
		s.redirectWithFlash(c, "/projects", "", err.Error())
		return
	}
	stored, err := co.DeleteResumable(id)
	if err != nil {
		if coderRefused(c, err) {
			return
		}
		s.redirectWithFlash(c, "/projects", "", err.Error())
		return
	}
	project = s.projects.ProjectNameFor(stored.CWD)
	s.jobDeleted(id)
	s.publishTerminals(project)
	if s.coderJSON(c, id, stored.Name, stored.CWD, projectLanding(project)) {
		return
	}
	s.redirectWithProjectFlash(c, project, "Coder \""+stored.Name+"\" deleted.", "")
}

// jobCalledOff ends the job of a coder that is being stopped or deleted.
// Either way the terminal it steers will not report again, so nothing can ever
// move the job, and one left steering would sit in the conversation as a promise
// nobody can keep. A terminal nobody steers answers with an error, which is the
// normal case here.
func (s *Server) jobCalledOff(id string) {
	_ = s.watcher.Release(id)
}

// jobDeleted is jobCalledOff for a session that is removed for good. A stopped
// coder can be resumed, so its job stays readable next to it; a deleted one
// leaves nothing to read it next to, and the entry would be parsed on every
// look at the store from here on.
func (s *Server) jobDeleted(id string) {
	s.watcher.Forget(id)
}
