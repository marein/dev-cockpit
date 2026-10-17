package web

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/docker"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/share"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// handleEditorDocker answers the editor's docker view of one project as
// JSON: the compose stacks it can drive and the containers that exist, all
// from the cache. The editor paints its statusbar segment and the docker
// sheet from it. A guest's answer carries the compose actions and the runs
// only where the link grants them, and each container's read only logs.
func (s *Server) handleEditorDocker(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	acc := accessOf(c)
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
			if runs := s.docker.ComposeRunsForDir(stack.Dir); len(runs) > 0 && acc.Can(share.Compose) {
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
			entry := gin.H{
				"id":         container.ID,
				"name":       container.DisplayName(),
				"running":    container.Running(),
				"unwell":     container.Unwell(),
				"portsLabel": container.PortsLabel(),
				"links":      links,
			}
			if acc.Guest {
				entry["logs"] = acc.Base + dockerLogsPath(p.Name, container.ID)
			}
			containers = append(containers, entry)
		}
	}
	// The icon is resolved here, not in the browser: the name to picture table
	// is one table in the render layer, and a client that carried its own copy
	// would be a second one.
	actions := make([]gin.H, 0)
	for _, action := range render.DockerButtons(s.composeActions()) {
		if !acc.Can(share.Compose) {
			break
		}
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

// dockerLogsTail is how many of a container's last lines its logs page shows,
// and dockerLogsTimeout how long one read of them may take, the page asks
// again every second and a half while the container runs.
const (
	dockerLogsTail    = 500
	dockerLogsTimeout = 10 * time.Second
)

func dockerLogsPath(project, container string) string {
	return "/projects/" + url.PathEscape(project) + "/docker/logs?" + url.Values{"container": {container}}.Encode()
}

// projectContainer answers the container a request names, one of the
// project's and none of another's.
func (s *Server) projectContainer(c *gin.Context) (project.Project, docker.Container, bool) {
	p, err := s.projects.FindByName(c.Param("name"))
	if err != nil {
		return project.Project{}, docker.Container{}, false
	}
	id := c.Query("container")
	for _, container := range s.docker.State().ForDir(p.Path) {
		if container.ID == id {
			return p, container, true
		}
	}
	return project.Project{}, docker.Container{}, false
}

// containerLogs reads the container's last lines from the daemon, the one
// read of its logs that starts no terminal.
func (s *Server) containerLogs(c *gin.Context, container docker.Container) (string, error) {
	client, err := s.docker.Client()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), dockerLogsTimeout)
	defer cancel()
	return client.Logs(ctx, container.ID, dockerLogsTail)
}

func containerLogsStatus(container docker.Container) string {
	if container.Running() {
		return "Running"
	}
	return "Not running"
}

// handleDockerLogsPage shows a container's last lines, read only, which is
// how a guest of the shared project reads its logs: the owner's follow runs
// in a shell, and a guest starts none. It stays live through its output.
func (s *Server) handleDockerLogsPage(c *gin.Context) {
	p, container, ok := s.projectContainer(c)
	if !ok {
		s.handleNotFound(c)
		return
	}
	acc := accessOf(c)
	output, err := s.containerLogs(c, container)
	if err != nil {
		output = userFacingError(c, err)
	}
	c.HTML(http.StatusOK, "docker_logs.gohtml", render.DockerLogsData{
		Page:      s.page(c, container.DisplayName(), "editor"),
		Project:   p.Name,
		Container: container.DisplayName(),
		Running:   container.Running(),
		Status:    containerLogsStatus(container),
		Tail:      dockerLogsTail,
		Output:    output,
		OutputURL: acc.Base + "/projects/" + url.PathEscape(p.Name) + "/docker/logs/output?" + url.Values{"container": {container.ID}}.Encode(),
		Back:      acc.Base + "/projects/" + url.PathEscape(p.Name) + "/editor",
	})
}

// handleDockerLogsOutput answers what the logs page repaints from.
func (s *Server) handleDockerLogsOutput(c *gin.Context) {
	_, container, ok := s.projectContainer(c)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "No such container."})
		return
	}
	output, err := s.containerLogs(c, container)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": userFacingError(c, err)})
		return
	}
	c.JSON(http.StatusOK, gin.H{"output": output, "running": container.Running(), "status": containerLogsStatus(container)})
}
