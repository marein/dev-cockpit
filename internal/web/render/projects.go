package render

import (
	"github.com/local/dev-cockpit/internal/docker"
	"github.com/local/dev-cockpit/internal/project"
)

// ProjectDocker is one project's docker presence: the compose stacks it can
// drive and the containers that exist. Either list may be empty, a project
// with only a compose file still gets its compose button on the row.
type ProjectDocker struct {
	Stacks     []DockerStack
	Containers []DockerContainer
}

// DockerContainer is one container as the row renders it: what the daemon
// says, plus the addresses it answers on. Those are resolved here and not in
// the template, because which label carries an address is configuration and
// applying it is the matcher's job, not a template's.
type DockerContainer struct {
	docker.Container
	Links []docker.Link
}

// AnyRunning reports whether one of the project's containers runs, the
// state the row button's icon colors on.
func (d ProjectDocker) AnyRunning() bool {
	for _, c := range d.Containers {
		if c.Running() {
			return true
		}
	}
	return false
}

// DockerStack is one compose control point rendered as a chip. Busy marks a
// compose run in flight, the menu then disables the actions. Run names the
// newest run of that stack, the one whose output the menu leads to, whether it
// is still going or over.
type DockerStack struct {
	docker.Stack
	Busy      bool
	RunID     string
	RunAction string
	RunGoing  bool
}

// ProjectDelete is what a row says about its own deletion: one that brings
// compose stacks down runs longer than its request, and a failure has nobody
// left to flash it, so both live on the row.
type ProjectDelete struct {
	Running bool
	Failed  string
}

// ProjectsListData is the model for the projects list page.
type ProjectsListData struct {
	Page
	Projects []project.Project
	// Docker holds each project's docker presence, keyed by project name,
	// joined through the compose working directory. Nil while no daemon
	// answers, the chip row then simply has no docker chips.
	Docker map[string]ProjectDocker
	// DockerActions is the configured compose commands, the same list for
	// every project because it describes the install and not one stack, with
	// their icons already resolved. Empty is a real answer: the menu then says
	// so and offers the defaults back.
	DockerActions []DockerButton
	// Deleting holds the rows whose project is being deleted or whose
	// deletion failed, keyed by project name. Nil while none is.
	Deleting map[string]ProjectDelete
}
