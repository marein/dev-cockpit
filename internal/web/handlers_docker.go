package web

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/approval"
	"github.com/marein/dev-cockpit/internal/askpass"
	"github.com/marein/dev-cockpit/internal/assistant"
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
	if stack, ok := s.findStack(p, c.PostForm("stack")); ok {
		return p, stack, true
	}
	c.JSON(http.StatusBadRequest, gin.H{"error": "Unknown compose stack."})
	return project.Project{}, docker.Stack{}, false
}

// findStack answers the project's compose stack under its label.
func (s *Server) findStack(p project.Project, label string) (docker.Stack, bool) {
	for _, stack := range s.docker.State().StacksForDir(p.Path) {
		if stack.Label == label {
			return stack, true
		}
	}
	return docker.Stack{}, false
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
//
// An assistant reaches the same route over the local socket and its run is
// owned: the word at the end goes into its thread instead of the user's bell.
// A command that asks first asks the user first for an assistant too, the way
// the compose menu's confirm comes before any request: nothing is started
// until the user approves, and the run and its id come with the approval.
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
	// Only the browser runs a compose command as the user. A local call is an
	// assistant's, and one that names no live assistant is refused rather
	// than read as the user: the question a confirm action asks exists for
	// exactly the calls that would slip through here otherwise.
	owner := ""
	if s.localCall(c) {
		from, err := s.assistantCaller(c)
		if err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
		owner = from
	}
	if owner != "" && action.Confirm && s.askApproval(c, s.composeApproval(owner, p, stack, action)) {
		return
	}
	id, err := s.startCompose(p, stack, action, owner)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok": true, "action": action.Label, "stack": stack.Label, "project": p.Name,
		"run": id, "url": dockerRunPath(p.Name, id),
	})
}

// startCompose starts one compose command on one stack, owned by an
// assistant where owner names one.
func (s *Server) startCompose(p project.Project, stack docker.Stack, action docker.Action, owner string) (string, error) {
	id, err := s.docker.RunCompose(docker.ComposeOptions{
		Dir:    stack.Dir,
		Root:   p.Path,
		Label:  p.Name,
		Action: action,
		Owner:  owner,
	})
	if err != nil {
		return "", err
	}
	s.bus.Publish(eventbus.Event{Type: "docker"})
	return id, nil
}

// composeApproval is the approval a confirm compose command of an assistant
// waits for. The same command on the same stack waits once. Its run resolves
// the project, the stack and the command again by name, the settings may have
// moved in the half hour the question may stand.
func (s *Server) composeApproval(owner string, p project.Project, stack docker.Stack, action docker.Action) approval.Request {
	what := action.Label + " in " + p.Name
	where := "project root"
	if stack.Label != "" {
		what = action.Label + " on " + stack.Label + " in " + p.Name
		where = stack.Label
	}
	details := []askpass.Detail{
		{Label: "Action", Value: action.Label},
		{Label: "Stack", Value: where},
		{Label: "Project", Value: p.Name},
	}
	// The line the run will start, resolved the way the run resolves it; one
	// that does not resolve fails the run, and the dialog shows it as written.
	command := action.Command
	if argv, _, err := action.Resolve(stack.Dir, p.Path); err == nil {
		command = strings.Join(argv, " ")
	}
	name, label, actionID := p.Name, stack.Label, action.ID
	return approval.Request{
		Owner:    owner,
		Kind:     approvalComposeActions,
		Key:      strings.Join([]string{name, label, actionID}, "\n"),
		What:     what,
		Details:  details,
		Command:  command,
		Dir:      stack.Dir,
		Projects: []string{name},
		Run: func() (string, error) {
			p, err := s.projects.FindByName(name)
			if err != nil {
				return "", errors.New("the project no longer exists")
			}
			stack, ok := s.findStack(p, label)
			if !ok {
				return "", errors.New("the compose stack no longer exists")
			}
			action, ok := docker.ActionByID(s.composeActions(), actionID)
			if !ok {
				return "", errors.New("the compose command no longer exists")
			}
			run, err := s.startCompose(p, stack, action, owner)
			if err != nil {
				return "", err
			}
			return assistant.ComposeStarted(run, action.Label, p.Name, stack.Label), nil
		},
	}
}

// handleProjectDocker answers one project's docker picture as JSON for the
// assistant's `compose-list`: the stacks it can drive with where their newest
// run stands, the configured commands, and the containers. It is the editor's
// view without the editor: off the editor group, so a reading by an assistant
// never counts as somebody working in the project.
func (s *Server) handleProjectDocker(c *gin.Context) {
	p, err := s.projects.FindByName(c.Param("name"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Unknown project."})
		return
	}
	state := s.docker.State()
	stacks := make([]gin.H, 0)
	containers := make([]gin.H, 0)
	if state.Available {
		matcher := s.linkMatcher()
		for _, stack := range state.StacksForDir(p.Path) {
			entry := gin.H{
				"label":   stack.Label,
				"dir":     stack.Dir,
				"running": stack.Running,
				"total":   stack.Total,
				"busy":    s.docker.ComposeBusy(stack.Dir),
				"links":   linkAddresses(matcher.StackLinks(state, stack.Dir)),
			}
			if runs := s.docker.ComposeRunsForDir(stack.Dir); len(runs) > 0 {
				entry["run"] = composeRunJSON(p.Name, runs[0])
			}
			stacks = append(stacks, entry)
		}
		for _, container := range state.ForDir(p.Path) {
			containers = append(containers, gin.H{
				"name":    container.DisplayName(),
				"running": container.Running(),
				"unwell":  container.Unwell(),
			})
		}
	}
	actions := make([]gin.H, 0)
	for _, action := range s.composeActions() {
		actions = append(actions, gin.H{
			"id":      action.ID,
			"intent":  docker.NormalizeIcon(action.Icon),
			"label":   action.Label,
			"command": action.Command,
			"timeout": action.Duration().String(),
			"confirm": action.Confirm,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"project":    p.Name,
		"available":  state.Available,
		"cli":        s.docker.CLI(),
		"stacks":     stacks,
		"containers": containers,
		"actions":    actions,
	})
}

// linkAddresses is what the assistant's readings say about a stack's
// addresses: each one as a person reads it, the order kept.
func linkAddresses(links []docker.Link) []string {
	out := make([]string, 0, len(links))
	for _, link := range links {
		out = append(out, link.Address())
	}
	return out
}

// composeRunJSON is one run the way the JSON readings say it, the run page's
// poll and the assistant's `compose-show` alike, so both read the same facts.
func composeRunJSON(project string, run docker.RunView) gin.H {
	entry := gin.H{
		"id":        run.ID,
		"action":    run.Action,
		"command":   run.Command,
		"status":    render.DockerRunStatus(run),
		"running":   run.Running,
		"failed":    run.Failure != "",
		"failure":   run.Failure,
		"exited":    run.Exited,
		"exit":      run.Exit,
		"cancelled": run.Cancelled,
		"owner":     run.Owner,
		"startedAt": run.StartedAt,
		"url":       dockerRunPath(project, run.ID),
	}
	if !run.EndedAt.IsZero() {
		entry["endedAt"] = run.EndedAt
	}
	return entry
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
//
// A local call is refused like the settings save it stands beside.
func (s *Server) handleDockerActionsRestore(c *gin.Context) {
	if s.localCall(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": composeActionsLocalRefusal})
		return
	}
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
	s.readComposeNews(p.Name, run.ID)
	c.HTML(http.StatusOK, "docker_run.gohtml", render.DockerRunData{
		Page:      s.page(c, run.Action, "projects"),
		Project:   p.Name,
		Stack:     stackLabel(p.Path, run.Dir),
		Run:       run,
		Status:    render.DockerRunStatus(run),
		Output:    s.docker.ComposeRunOutput(run.ID),
		OutputURL: dockerRunPath(p.Name, run.ID) + "/output",
		StopURL:   dockerRunPath(p.Name, run.ID) + "/stop",
		Back:      dockerRunBack(c, p.Name),
	})
}

// dockerRunBack is the run page's way back, the `return` a link carried the
// way the create forms take it, cockpit paths only, and the project's row
// without one: a notification or a fresh tab has no page to go back to.
func dockerRunBack(c *gin.Context, project string) string {
	if back := safeRedirectPath(strings.TrimSpace(c.Query("return"))); back != "/" {
		return back
	}
	return "/projects#project-" + project
}

// readComposeNews marks the project's compose notification read when the run
// being looked at is the one it is about: being there is seeing that outcome,
// like opening an attach page is for a terminal. The target speaks for the
// newest finished run without an owner (LastComposeRun), so reading an
// assistant's run, or an older run of the user's, leaves news about another
// run standing.
func (s *Server) readComposeNews(project, runID string) {
	if last, ok := s.docker.LastComposeRun(project); ok && last.ID == runID {
		s.notifier.MarkTargetRead(notify.DockerTarget(project))
	}
}

// handleDockerRunOutput answers what the page repaints from while a run goes.
func (s *Server) handleDockerRunOutput(c *gin.Context) {
	p, run, ok := s.composeRun(c)
	if !ok {
		return
	}
	entry := composeRunJSON(p.Name, run)
	entry["stack"] = stackLabel(p.Path, run.Dir)
	entry["output"] = s.docker.ComposeRunOutput(run.ID)
	c.JSON(http.StatusOK, entry)
}

// handleDockerRunStop calls a running command off. The kill goes at the hold
// process, never at this server: the run is detached and the server that
// started it may be long gone.
//
// The user stops any run. An assistant stops only its own, the line a job
// draws for releasing.
func (s *Server) handleDockerRunStop(c *gin.Context) {
	_, run, ok := s.composeRun(c)
	if !ok {
		return
	}
	local := s.localCall(c)
	if local {
		from, err := s.assistantCaller(c)
		if err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
		if from != run.Owner {
			c.JSON(http.StatusForbidden, gin.H{"error": s.composeStopRefusal(run.Owner)})
			return
		}
	}
	// A cancel from the browser is the user's own click: the run still
	// reports into its owner's thread, but rings nobody.
	if err := s.docker.CancelCompose(run.ID, !local); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// composeStopRefusal is what an assistant reads when it tries to stop a run
// that is not its own, naming whose it is.
func (s *Server) composeStopRefusal(owner string) string {
	if owner == "" {
		return "That run belongs to the user. Only the user stops it."
	}
	return "That run belongs to " + s.assistantName(owner) + ". Only the user or that assistant stops it."
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
//
// Who hears the word is decided per run and never per project: a run an
// assistant started reports into that assistant's thread and rings nobody,
// while a run the user started on the same project still writes the
// project's notification.
//
// An owner that is gone by the time its run ends, a delete racing the end or
// a run registered right after its owner was deleted, has no thread to take
// the report. That run is handed to the user here, where the end lands, and
// rings the project like a run the user started: Disown at the delete closes
// the common case, this closes every window around it.
func (s *Server) composeDone(run docker.ComposeRun, err error, output string) {
	if err != nil {
		log.Printf("docker %q in %s: %v: %s", run.Action, run.Dir, err, output)
	}
	if run.Owner != "" {
		recorded := s.assistants != nil && s.assistants.RecordCompose(s.composeReport(run, err, output)) != ""
		if !recorded && s.docker.DisownRun(run.ID) {
			run.Owner = ""
		}
	}
	if run.Owner == "" && (err != nil || !run.Quiet) {
		// A quiet run only speaks when it failed: the project deletion runs
		// its compose down as a step of itself, and the row disappearing is
		// the word. The target is the project, so two projects finishing at
		// the same moment are two pieces of news, while one project's down
		// and up seconds apart still collapse into one.
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

// composeReportURL is the run page the note links, with the owner's thread as
// its way back.
func composeReportURL(run docker.ComposeRun) string {
	path := dockerRunPath(run.Label, run.ID)
	if run.Owner == "" {
		return path
	}
	return path + "?return=" + url.QueryEscape("/assistants/"+run.Owner)
}

// composeReport is what the assistant's thread is told about an owned run
// that ended: the run, where it ran, how it went, the tail of what it wrote
// and the page that shows the rest. The stack is named the way the compose
// menu names it; a project that is gone by then leaves the directory.
func (s *Server) composeReport(run docker.ComposeRun, err error, output string) assistant.ComposeReport {
	report := assistant.ComposeReport{
		Owner:   run.Owner,
		Run:     run.ID,
		Project: run.Label,
		Stack:   run.Dir,
		Action:  run.Action,
		URL:     composeReportURL(run),
		Failed:  run.Failed,
		Exited:  run.Exited,
		Exit:    run.Exit,
		Output:  output,
		ByUser:  run.ByUser,
	}
	if err != nil {
		report.Reason = err.Error()
	}
	if p, perr := s.projects.FindByName(run.Label); perr == nil {
		report.Stack = stackLabel(p.Path, run.Dir)
	}
	if !run.Failed && docker.BringsUp(run.Intent) {
		report.Links = s.composeLinks(run)
	}
	return report
}

// composeLinksWait bounds how long the note of a stack brought up waits for
// the cache to read the containers the run started. The cache reads the list
// again only once the event burst settled, a moment after an up -d returned.
const composeLinksWait = 2 * time.Second

// composeLinks are the addresses a stack brought up answers on, routes first.
// They are read once, off the first reading the cache took after the run
// ended, or off what it holds when none came within composeLinksWait; the
// stack's claim is already given back, only the note waits. Without a daemon
// no reading comes, and nothing is waited for.
func (s *Server) composeLinks(run docker.ComposeRun) []assistant.ComposeLink {
	if !s.docker.State().Available {
		return nil
	}
	s.docker.AwaitReload(run.Reloads, composeLinksWait)
	state := s.docker.State()
	if !state.Available {
		return nil
	}
	var out []assistant.ComposeLink
	for _, link := range s.linkMatcher().StackLinks(state, run.Dir) {
		out = append(out, assistant.ComposeLink{Address: link.Address(), URL: link.URL()})
	}
	return out
}
