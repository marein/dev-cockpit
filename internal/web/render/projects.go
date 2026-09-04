package render

import (
	"fmt"
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
	Ref    string `json:"ref"`
	Name   string `json:"name"`
	Remote bool   `json:"remote,omitempty"`
	Taken  string `json:"taken,omitempty"`
	Head   bool   `json:"head,omitempty"`
	// Upstream, Ahead and Behind are how far this branch stands from the one
	// it follows, straight out of git.Ref. A row wearing them says how old a
	// starting point is, which is the one thing a branch name never shows and
	// the reason a worktree can begin its life already out of date. They are
	// empty for a remote branch and for a local one that follows nothing.
	Upstream string `json:"upstream,omitempty"`
	Ahead    int    `json:"ahead,omitempty"`
	Behind   int    `json:"behind,omitempty"`
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

// DefaultBranchName is the local branch the default choice ends up on: its
// own name, and for a branch that so far only exists on a remote the name the
// checkout creates here. The form suggests the project name from it, so it
// travels beside the value the picker posts.
func (d ProjectNewData) DefaultBranchName() string {
	ref := d.DefaultBranch()
	for _, b := range d.Branches {
		if b.Ref == ref {
			return b.Name
		}
	}
	return ref
}

// DefaultBranchUpstream names what the default checkout could be caught up to:
// the branch it follows, and only while it is purely behind that branch. Every
// other state answers empty, so the form offers a catch up exactly where a
// catch up is a fast forward and nothing else. A branch from a remote is
// created here and is behind nothing.
func (d ProjectNewData) DefaultBranchUpstream() string {
	ref := d.DefaultBranch()
	for _, b := range d.Branches {
		if b.Ref != ref {
			continue
		}
		if b.Remote || b.Upstream == "" || b.Behind == 0 || b.Ahead != 0 {
			return ""
		}
		return b.Upstream
	}
	return ""
}

// DefaultStart is where a new branch begins by default: the branch the source
// project stands on, which is the state somebody looking at that project has
// in mind — or that branch's upstream, when the local one has only fallen
// behind it.
//
// The exchange is deliberate and one sided. A head that is purely behind
// (behind above zero, ahead zero) holds nothing the remote does not, so
// starting there only means starting older, by however long nobody pulled;
// starting at the upstream is the same history, further along. The moment the
// head is ahead or has diverged that stops being true: those commits exist
// nowhere else, and quietly branching off the upstream instead would leave
// them behind without a word. So ahead and diverged keep the local branch, and
// so does a head with no upstream, which has nothing to compare against.
func (d ProjectNewData) DefaultStart() string {
	for _, b := range d.Branches {
		if b.Ref == d.Fill.Start {
			return b.Ref
		}
	}
	for _, b := range d.Branches {
		if b.Head {
			if b.Upstream != "" && b.Behind > 0 && b.Ahead == 0 {
				return b.Upstream
			}
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
	// Nothing to check out outranks what was filled in: the select disables
	// that half in this state, and a form that came back standing on a
	// disabled option would be standing on a choice it cannot make.
	if d.DefaultBranch() == "" {
		return "new"
	}
	if d.Fill.Mode == "new" || d.Fill.Mode == "existing" {
		return d.Fill.Mode
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

// ProjectRow is one line of the list. A main repository whose worktrees are
// projects of this list folds them beneath itself: Worktrees names them in
// list order and every one of those rows is Grouped. A worktree whose main is
// no project here has nothing to fold under and stands on a line of its own,
// its chip naming the main's path, and a main whose worktrees all lie outside
// the list carries no count either, there is nothing to unfold. WorktreeNews
// and WorktreeActive say whether one of the folded worktrees has news or is at
// work: their rows are out of sight while the group is closed, so the badge
// carries their dot and their green for them.
type ProjectRow struct {
	project.Project
	Worktrees      []string
	WorktreeNews   bool
	WorktreeActive bool
	Grouped        bool
}

// WorktreeIDs is the space separated list of row ids the fold button
// controls, what aria-controls wants.
func (r ProjectRow) WorktreeIDs() string {
	ids := make([]string, 0, len(r.Worktrees))
	for _, name := range r.Worktrees {
		ids = append(ids, "project-"+name)
	}
	return strings.Join(ids, " ")
}

// WorktreeCount words the badge's name: "1 worktree", "5 worktrees". The
// badge itself shows the bare number, the words go to its title and label.
func (r ProjectRow) WorktreeCount() string {
	if len(r.Worktrees) == 1 {
		return "1 worktree"
	}
	return fmt.Sprintf("%d worktrees", len(r.Worktrees))
}

// Rows is the order the list renders: the projects as they came, with every
// main repository followed at once by the worktree projects grouped under
// it. A worktree is grouped when its main is a project of this list and is a
// main itself, which is the same rule the client's sort groups by; the
// client then puts the groups into its own order and this is what stands
// before it runs. Building it is one pass over what is already in memory,
// the list costs no extra read for it.
func (d ProjectsListData) Rows() []ProjectRow {
	index := make(map[string]int, len(d.Projects))
	for i, p := range d.Projects {
		index[p.Name] = i
	}
	mainOf := func(p project.Project) string {
		if !p.GitWorktree || p.GitWorktreeOf == "" || p.GitWorktreeOf == p.Name {
			return ""
		}
		i, ok := index[p.GitWorktreeOf]
		if !ok || d.Projects[i].GitWorktree {
			return ""
		}
		return p.GitWorktreeOf
	}
	children := map[string][]string{}
	for _, p := range d.Projects {
		if main := mainOf(p); main != "" {
			children[main] = append(children[main], p.Name)
		}
	}
	rows := make([]ProjectRow, 0, len(d.Projects))
	for _, p := range d.Projects {
		if mainOf(p) != "" {
			continue
		}
		main := ProjectRow{Project: p, Worktrees: children[p.Name]}
		for _, name := range children[p.Name] {
			child := d.Projects[index[name]]
			main.WorktreeNews = main.WorktreeNews || child.HasNews
			main.WorktreeActive = main.WorktreeActive || child.Active()
		}
		rows = append(rows, main)
		for _, name := range children[p.Name] {
			rows = append(rows, ProjectRow{Project: d.Projects[index[name]], Grouped: true})
		}
	}
	return rows
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
