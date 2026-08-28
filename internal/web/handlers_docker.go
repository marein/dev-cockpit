package web

import (
	"context"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/docker"
	"github.com/marein/dev-cockpit/internal/eventbus"
	"github.com/marein/dev-cockpit/internal/notify"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// dockerIDPattern is the shape of the container ids the cockpit renders: the
// daemon's full hex id. Nothing else ever reaches these routes from our own
// pages, so anything else is refused before it travels to the daemon.
var dockerIDPattern = regexp.MustCompile(`^[0-9a-f]{12,64}$`)

// dockerLogsName is what a whole stack's logs terminal is called. A container's
// carries its own name, it is about that one thing; a stack's followed the
// project's name for no reason, it is every service of a compose directory and
// says so in its first line.
const dockerLogsName = "docker logs"

// dockerActionTimeout bounds one lifecycle call. A stop waits for the
// daemon's grace period, so this is generous rather than snappy.
const dockerActionTimeout = 60 * time.Second

func (s *Server) handleDockerStart(c *gin.Context)   { s.dockerLifecycle(c, "start") }
func (s *Server) handleDockerStop(c *gin.Context)    { s.dockerLifecycle(c, "stop") }
func (s *Server) handleDockerRestart(c *gin.Context) { s.dockerLifecycle(c, "restart") }

// dockerLifecycle runs one container action against the connected daemon.
// The event stream refreshes the chips, so the answer carries no state, only
// success or the daemon's message.
func (s *Server) dockerLifecycle(c *gin.Context, action string) {
	id := c.Param("id")
	if !dockerIDPattern.MatchString(id) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unknown container."})
		return
	}
	client, err := s.docker.Client()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), dockerActionTimeout)
	defer cancel()
	switch action {
	case "start":
		err = client.Start(ctx, id)
	case "stop":
		err = client.Stop(ctx, id)
	case "restart":
		err = client.Restart(ctx, id)
	}
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// cachedContainer finds a container in the watcher's cache by its full id,
// the only shape our own pages ever send.
func (s *Server) cachedContainer(id string) (docker.Container, bool) {
	if !dockerIDPattern.MatchString(id) {
		return docker.Container{}, false
	}
	for _, container := range s.docker.State().Containers {
		if container.ID == id {
			return container, true
		}
	}
	return docker.Container{}, false
}

// handleDockerShell starts a cockpit shell that immediately steps into the
// container; when the exec ends, the pane falls back to a plain shell in the
// compose directory. handleDockerLogsShell is the same shell following the
// container's output instead.
func (s *Server) handleDockerShell(c *gin.Context) {
	s.dockerShell(c, false)
}

func (s *Server) handleDockerLogsShell(c *gin.Context) {
	s.dockerShell(c, true)
}

func (s *Server) dockerShell(c *gin.Context, logs bool) {
	container, ok := s.cachedContainer(c.Param("id"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "Unknown container."})
		return
	}
	if !s.docker.CLI() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "The docker CLI is not installed."})
		return
	}
	dir := container.WorkingDir
	projectName := s.projects.ProjectNameFor(dir)
	if dir == "" || projectName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The container belongs to no project."})
		return
	}
	host := s.docker.State().Host
	name := container.DisplayName()
	command := docker.ExecCommand(host, container.Name)
	if logs {
		filter, ok := s.logFilter(c)
		if !ok {
			return
		}
		name += " logs"
		if filter != "" {
			name += ": " + filter
		}
		command = docker.LogsCommand(host, container.Name, filter)
	}
	id, err := s.shells.StartCommand(dir, name, command+"; exec bash -il")
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	s.styleSessionPane(id)
	s.publishTerminals(projectName)
	c.JSON(http.StatusOK, gin.H{"id": id, "url": "/shells/" + id})
}

// handleDockerComposeLogs opens a cockpit shell following a whole stack's
// output, the project wide counterpart of a container's logs: one stream for
// every service, so nobody has to find the container that is talking first.
// It is a normal shell like the container ones, so it lives in the tab strip
// and the terminal panel like any other.
func (s *Server) handleDockerComposeLogs(c *gin.Context) {
	p, stack, ok := s.composeStack(c)
	if !ok {
		return
	}
	dir := stack.Dir
	if !s.docker.CLI() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "The docker CLI is not installed."})
		return
	}
	filter, ok := s.logFilter(c)
	if !ok {
		return
	}
	name := dockerLogsName
	if filter != "" {
		name += ": " + filter
	}
	id, err := s.shells.StartCommand(dir, name, docker.ComposeLogsCommand(s.docker.State().Host, filter)+"; exec bash -il")
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	s.styleSessionPane(id)
	s.publishTerminals(p.Name)
	c.JSON(http.StatusOK, gin.H{"id": id, "url": "/shells/" + id})
}

// logFilter reads the optional pattern a log shell is started with. A pattern
// the formatter could not compile is refused here, where it was typed, instead
// of failing inside the spawned pipeline where nobody reads the error.
func (s *Server) logFilter(c *gin.Context) (string, bool) {
	filter := strings.TrimSpace(c.PostForm("filter"))
	if _, err := docker.CompileLogPattern(filter); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The filter is not a valid regular expression."})
		return "", false
	}
	return filter, true
}

// composeStack resolves the project and the compose stack a request names,
// the two things every project scoped docker action starts from.
func (s *Server) composeStack(c *gin.Context) (project.Project, docker.Stack, bool) {
	p, err := s.projects.FindByName(c.Param("name"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Unknown project."})
		return project.Project{}, docker.Stack{}, false
	}
	label := c.PostForm("stack")
	for _, stack := range s.docker.State().StacksForDir(p.Path) {
		if stack.Label == label {
			return p, stack, true
		}
	}
	c.JSON(http.StatusBadRequest, gin.H{"error": "Unknown compose stack."})
	return project.Project{}, docker.Stack{}, false
}

// composeActions answers the configured compose commands. The setting is
// asked with Lookup and never with Get: never set means the default list,
// while set and empty means somebody took every button away, and Get says the
// same empty string to both.
func (s *Server) composeActions() []docker.Action {
	return docker.Actions(s.settings.Lookup(docker.ActionsSettingKey))
}

// linkRules answers the configured link rules, the same three states as the
// commands above and read the same way.
func (s *Server) linkRules() []docker.LinkRule {
	return docker.LinkRules(s.settings.Lookup(docker.LinkRulesSettingKey))
}

// linkMatcher prepares them for one read: a page asks it per container, and a
// rule is a regular expression that is compiled once here instead of once per
// chip.
func (s *Server) linkMatcher() docker.LinkMatcher {
	return docker.NewLinkMatcher(s.linkRules())
}

// handleDockerCompose starts one configured compose command for one of the
// project's stacks in the background. The event stream shows the containers
// move; the word at the end is a notification, like a backup's, and it comes
// from the service's one completion callback (see composeDone), because a run
// outlives this request and may well outlive this whole process.
func (s *Server) handleDockerCompose(c *gin.Context) {
	p, stack, ok := s.composeStack(c)
	if !ok {
		return
	}
	action, found := docker.ActionByID(s.composeActions(), c.PostForm("action"))
	if !found {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unknown compose action."})
		return
	}
	id, err := s.docker.RunCompose(docker.ComposeOptions{
		Dir:    stack.Dir,
		Root:   p.Path,
		Label:  p.Name,
		Action: action,
	})
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	s.bus.Publish(eventbus.Event{Type: "docker"})
	c.JSON(http.StatusOK, gin.H{"ok": true, "run": id, "url": dockerRunPath(p.Name, id)})
}

// handleDockerActionsRestore is the one way back to the default commands, the
// route the docker menu and the settings page both take. An empty list is a
// real answer, so nothing restores itself, but the way back is one click and
// does not send anybody off to retype four lines.
//
// It removes the key rather than writing the defaults into it. Writing them
// would leave the setting reading as answered, and this install would then keep
// today's list forever, which is exactly what the absent state exists to
// prevent: a default nobody stored is a default a later version may improve.
func (s *Server) handleDockerActionsRestore(c *gin.Context) {
	s.settings.Delete(docker.ActionsSettingKey)
	s.bus.Publish(eventbus.Event{Type: "docker"})
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// handleDockerLinkRulesRestore is the same way back for the link rules, and
// it is the same decision: the key goes rather than today's defaults being
// written into it.
func (s *Server) handleDockerLinkRulesRestore(c *gin.Context) {
	s.settings.Delete(docker.LinkRulesSettingKey)
	s.bus.Publish(eventbus.Event{Type: "docker"})
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// dockerRunPath is where one run's output is read.
func dockerRunPath(project, id string) string {
	return "/projects/" + url.PathEscape(project) + "/docker/runs/" + id
}

// composeRun resolves the run a request names and refuses one that belongs to
// another project, so a run is only ever reachable through the project it ran
// for. It serves the fetch endpoints under the run page, output and stop,
// whose caller is the page's own script, so a refusal is JSON. The page route
// checks for itself, see handleDockerRun.
func (s *Server) composeRun(c *gin.Context) (project.Project, docker.RunView, bool) {
	p, err := s.projects.FindByName(c.Param("name"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Unknown project."})
		return project.Project{}, docker.RunView{}, false
	}
	run, ok := s.docker.ComposeRunByID(c.Param("id"))
	if !ok || run.Project != p.Name {
		c.JSON(http.StatusNotFound, gin.H{"error": "Unknown compose run."})
		return project.Project{}, docker.RunView{}, false
	}
	return p, run, true
}

// handleDockerRun renders the output of one run, the page a notification
// links at. The run is detached, so this page is not watching a process: it
// reads the file the run writes into, while it runs and after it ended.
//
// It is a page route, so it checks project and run itself and refuses the way
// the pages do, a redirect with a flash: a JSON refusal here reaches pe.js,
// which treats every answer as a page, finds none in it, and the person who
// clicked the notification of a deleted project reads the literal word null.
func (s *Server) handleDockerRun(c *gin.Context) {
	p, err := s.projects.FindByName(c.Param("name"))
	if err != nil {
		s.redirectWithFlash(c, "/projects", "", "Unknown project.")
		return
	}
	run, ok := s.docker.ComposeRunByID(c.Param("id"))
	if !ok || run.Project != p.Name {
		s.redirectWithFlash(c, "/projects", "", "Unknown compose run.")
		return
	}
	// Being here is seeing a run's outcome, so the project's compose
	// notification reads itself, like opening an attach page does for a
	// terminal.
	s.notifier.MarkTargetRead(notify.DockerTarget(p.Name))
	c.HTML(http.StatusOK, "docker_run.gohtml", render.DockerRunData{
		Page:      s.page(c, run.Action, "projects"),
		Project:   p.Name,
		Stack:     stackLabel(p.Path, run.Dir),
		Run:       run,
		Status:    render.DockerRunStatus(run),
		Output:    s.docker.ComposeRunOutput(run.ID),
		OutputURL: dockerRunPath(p.Name, run.ID) + "/output",
		StopURL:   dockerRunPath(p.Name, run.ID) + "/stop",
	})
}

// handleDockerRunOutput answers what the page repaints from while a run goes.
func (s *Server) handleDockerRunOutput(c *gin.Context) {
	_, run, ok := s.composeRun(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"running": run.Running,
		"status":  render.DockerRunStatus(run),
		"failed":  run.Failure != "",
		"output":  s.docker.ComposeRunOutput(run.ID),
	})
}

// handleDockerRunStop calls a running command off. The kill goes at the hold
// process, never at this server: the run is detached and the server that
// started it may be long gone.
func (s *Server) handleDockerRunStop(c *gin.Context) {
	_, run, ok := s.composeRun(c)
	if !ok {
		return
	}
	if err := s.docker.CancelCompose(run.ID); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// stackLabel is what a stack directory is called inside its project, empty for
// the project root, the same label the compose menu uses.
func stackLabel(root, dir string) string {
	if rel, err := filepath.Rel(root, dir); err == nil && rel != "." {
		return rel
	}
	return ""
}

// composeDone is what every finished compose run reports through, the ones
// this process started and the ones it found still running after a restart.
// That is why it is one callback on the service and not the caller's own
// closure: the closure of the process that asked for a run is gone by the time
// a run that outlived it ends, and the user is owed the word either way. The
// busy flag moved and a failed run raises no container event at all, so the
// surfaces are told directly.
func (s *Server) composeDone(run docker.ComposeRun, err error, output string) {
	if err != nil {
		log.Printf("docker %q in %s: %v: %s", run.Action, run.Dir, err, output)
	}
	// A quiet run only speaks when it failed: the project deletion runs its
	// compose down as a step of itself, and the row disappearing is the word.
	// The target is the project, so two projects finishing at the same moment
	// are two pieces of news, while one project's down and up seconds apart
	// still collapse into one.
	if err != nil || !run.Quiet {
		s.notifier.Add(notify.DockerTarget(run.Label))
	}
	s.bus.Publish(eventbus.Event{Type: "docker"})
}

// handleEditorDocker answers the editor's docker view of one project as
// JSON: the compose stacks it can drive and the containers that exist, all
// from the cache. The editor paints its statusbar segment and the docker
// sheet from it.
func (s *Server) handleEditorDocker(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	state := s.docker.State()
	stacks := make([]gin.H, 0)
	containers := make([]gin.H, 0)
	if state.Available {
		for _, stack := range state.StacksForDir(p.Path) {
			entry := gin.H{
				"label":   stack.Label,
				"running": stack.Running,
				"total":   stack.Total,
				"busy":    s.docker.ComposeBusy(stack.Dir),
			}
			if runs := s.docker.ComposeRunsForDir(stack.Dir); len(runs) > 0 {
				entry["run"] = gin.H{
					"id":      runs[0].ID,
					"action":  runs[0].Action,
					"running": runs[0].Running,
					"url":     dockerRunPath(p.Name, runs[0].ID),
				}
			}
			stacks = append(stacks, entry)
		}
		matcher := s.linkMatcher()
		for _, container := range state.ForDir(p.Path) {
			// Both kinds of address in one list, in the order the container
			// answers them: an empty host is this page's own, an empty scheme
			// is the page's own, and a route carries no port at all.
			links := make([]gin.H, 0)
			for _, link := range matcher.Links(container) {
				links = append(links, gin.H{
					"scheme": link.Scheme,
					"host":   link.Host,
					"port":   link.Port,
					"path":   link.Path,
				})
			}
			containers = append(containers, gin.H{
				"id":         container.ID,
				"name":       container.DisplayName(),
				"running":    container.Running(),
				"unwell":     container.Unwell(),
				"portsLabel": container.PortsLabel(),
				"links":      links,
			})
		}
	}
	// The icon is resolved here, not in the browser: the name to picture table
	// is one table in the render layer, and a client that carried its own copy
	// would be a second one.
	actions := make([]gin.H, 0)
	for _, action := range render.DockerButtons(s.composeActions()) {
		actions = append(actions, gin.H{
			"id":      action.ID,
			"icon":    action.Icon,
			"label":   action.Label,
			"command": action.Command,
			"confirm": action.Confirm,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"available":  state.Available,
		"cli":        s.docker.CLI(),
		"stacks":     stacks,
		"containers": containers,
		"actions":    actions,
	})
}
