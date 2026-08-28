package render

import (
	"strings"

	"github.com/marein/dev-cockpit/internal/docker"
	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/project"
)

// DeleteWorktreeNote is the extra sentence the delete confirm carries for a
// project whose repository has linked worktrees. Worktrees that are projects
// of their own are deleted with the main and named; one lying elsewhere on the
// disk is left alone but its repository dies here, so it is named too. A
// worktree inside the project directory goes with the directory anyway and
// needs no word. Empty when none of that applies.
func DeleteWorktreeNote(p project.Project) string {
	var names, elsewhere []string
	for _, wt := range p.GitWorktrees {
		switch {
		case wt.Project != "" && wt.Project != p.Name:
			names = append(names, "\""+wt.Project+"\"")
		case wt.Project == "" && !filesystem.IsUnder(wt.Path, p.Path):
			elsewhere = append(elsewhere, wt.Path)
		}
	}
	var parts []string
	if len(names) == 1 {
		parts = append(parts, "The project "+names[0]+" is deleted too, because it is a worktree of this one.")
	} else if len(names) > 1 {
		parts = append(parts, "The projects "+joinAnd(names)+" are deleted too, because they are worktrees of this one.")
	}
	if len(elsewhere) == 1 {
		parts = append(parts, "The worktree at "+elsewhere[0]+" loses its repository.")
	} else if len(elsewhere) > 1 {
		parts = append(parts, "The worktrees at "+joinAnd(elsewhere)+" lose their repository.")
	}
	return strings.Join(parts, " ")
}

// joinAnd joins items the way a sentence lists them: commas, "and" before the
// last.
func joinAnd(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// OriginIcon names the tabler icon a project's origin is shown with. The
// forges the row can recognize get their own brand, everything else gets a
// globe: the origin is the one thing on the row that lives somewhere else, and
// that reads better than a third git glyph beside the branch and the worktree.
func OriginIcon(origin string) string {
	host, _, _ := strings.Cut(strings.ToLower(origin), "/")
	switch {
	case strings.Contains(host, "github"):
		return "ti-brand-github"
	case strings.Contains(host, "gitlab"):
		return "ti-brand-gitlab"
	case strings.Contains(host, "bitbucket"):
		return "ti-brand-bitbucket"
	}
	return "ti-world"
}

// BranchChoice is one branch a new worktree can stand on. Ref is what the
// form submits and the server resolves again, the branch's own name or, for
// one that so far only exists on a remote, the remote ref; Name is the local
// branch the working copy ends up on in both cases. Taken names the project
// or the directory whose working copy already holds this branch, which is the
// one thing that makes a branch unavailable, and is empty while it is free.
type BranchChoice struct {
	Ref    string
	Name   string
	Remote bool
	Taken  string
	Head   bool
}

// The values the create form's one select carries. An empty value is the
// plain project, CreateClone a directory a repository is cloned into, and a
// worktree carries the project it forks behind CreateWorktree, because the
// prefix is what keeps a project that is called "clone" apart from the kind
// of the same name.
const (
	CreateClone    = "clone"
	CreateWorktree = "worktree:"
)

// WorktreeChoice is the select value that forks the given project.
func WorktreeChoice(name string) string { return CreateWorktree + name }

// WorktreeSource names the project a choice forks, empty for every choice
// that forks none.
func WorktreeSource(choice string) string {
	if !strings.HasPrefix(choice, CreateWorktree) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(choice, CreateWorktree))
}

// ProjectNewFill is what the person had already typed when a create was
// refused. The form comes back carrying it, because a refusal on the last
// field must not cost somebody the whole form. Every value is what was
// submitted, and the defaults below take it only where it still makes sense:
// a branch that is gone or taken since is not preselected.
type ProjectNewFill struct {
	Name      string
	CloneURL  string
	Mode      string
	Branch    string
	NewBranch string
	Start     string
}

// ProjectNewData is the model for the create form. Source empty is the plain
// create the page has always been; with a source the form makes a linked
// worktree of that project, and everything below Source describes that one
// repository. Sources lists the projects that can be forked, as the same
// options every other create form's project select carries, so the browser
// puts them into the order the projects page stands in.
type ProjectNewData struct {
	Page
	Sources []ProjectOption
	// Create is the choice the select stands on, one of the values above.
	Create       string
	Source       string
	SourceBranch string
	Branches     []BranchChoice
	// Fill carries a refused create's own values back into the form.
	Fill ProjectNewFill
}

// AnyTaken reports whether one of the offered branches stands in a working
// copy already. The form says why those cannot be picked, and only says it
// when there is such a branch to explain.
func (d ProjectNewData) AnyTaken() bool {
	for _, b := range d.Branches {
		if b.Taken != "" {
			return true
		}
	}
	return false
}

// Locals are the repository's own branches, Remotes the ones that so far
// only exist on a remote. The form keeps them apart because they mean two
// different things when picked, and a template cannot split a list.
func (d ProjectNewData) Locals() []BranchChoice {
	var out []BranchChoice
	for _, b := range d.Branches {
		if !b.Remote {
			out = append(out, b)
		}
	}
	return out
}

func (d ProjectNewData) Remotes() []BranchChoice {
	var out []BranchChoice
	for _, b := range d.Branches {
		if b.Remote {
			out = append(out, b)
		}
	}
	return out
}

// DefaultBranch names the branch the form opens on: the first free one, never
// a taken one, because a browser preselects the first option and a taken
// branch is exactly the choice git would refuse.
func (d ProjectNewData) DefaultBranch() string {
	for _, b := range d.Branches {
		if b.Ref == d.Fill.Branch && b.Taken == "" {
			return b.Ref
		}
	}
	for _, b := range d.Branches {
		if !b.Remote && b.Taken == "" {
			return b.Ref
		}
	}
	for _, b := range d.Branches {
		if b.Taken == "" {
			return b.Ref
		}
	}
	return ""
}

// DefaultStart is where a new branch begins by default: the branch the source
// project stands on, which is the state somebody looking at that project has
// in mind.
func (d ProjectNewData) DefaultStart() string {
	for _, b := range d.Branches {
		if b.Ref == d.Fill.Start {
			return b.Ref
		}
	}
	for _, b := range d.Branches {
		if b.Head {
			return b.Ref
		}
	}
	if len(d.Branches) > 0 {
		return d.Branches[0].Ref
	}
	return ""
}

// DefaultMode is which half of the branch choice the form opens on. A
// repository whose branches all stand in a working copy already, the ordinary
// state of a project with one branch, has nothing to check out, and the form
// opens on the new branch instead of on a list where everything is refused.
func (d ProjectNewData) DefaultMode() string {
	if d.Fill.Mode == "new" || d.Fill.Mode == "existing" {
		return d.Fill.Mode
	}
	if d.DefaultBranch() == "" {
		return "new"
	}
	return "existing"
}

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

// Working reports whether a compose command of this project is in flight,
// which is what the row's docker icon rides the wave for. Both halves count:
// a run this process started holds the stack busy, and a run adopted after a
// restart is going without anybody holding anything.
func (d ProjectDocker) Working() bool {
	for _, s := range d.Stacks {
		if s.Busy || s.RunGoing {
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
