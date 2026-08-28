package web

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/git"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// worktreeBranchLimit bounds each branch list the create form offers, per
// kind, the way the editor's branch picker is bounded. A repository with more
// branches than this shows the most recently moved ones, which are the ones a
// new worktree is made for.
const worktreeBranchLimit = 500

// newProjectPath is where the create form stands, carrying everything that
// was filled in. Every refusal goes back through here, so a create that was
// refused on one field does not cost the person the whole form, and the way
// back is a plain address a reload may repeat.
func newProjectPath(form projectCreateForm) string {
	query := url.Values{}
	for key, value := range map[string]string{
		"create":       form.Create,
		"clone_url":    form.CloneURL,
		"project_name": form.Name.String(),
		"branch_mode":  form.BranchMode,
		"branch":       form.Branch,
		"new_branch":   form.NewBranch,
		"start":        form.Start,
	} {
		if strings.TrimSpace(value) != "" {
			query.Set(key, value)
		}
	}
	if len(query) == 0 {
		return "/projects/new"
	}
	return "/projects/new?" + query.Encode()
}

// projectNewFill reads back what a refused create had filled in. It is the
// same vocabulary the form posts, so one address describes one form state.
func projectNewFill(c *gin.Context) render.ProjectNewFill {
	return render.ProjectNewFill{
		Name:      c.Query("project_name"),
		CloneURL:  c.Query("clone_url"),
		Mode:      c.Query("branch_mode"),
		Branch:    c.Query("branch"),
		NewBranch: c.Query("new_branch"),
		Start:     c.Query("start"),
	}
}

// projectNewData builds the create form. Without a source it is the plain
// create it has always been, with one it also carries that repository's
// branches and who holds them. The lists are read here and not in the
// template, because both come from git and a template cannot ask it.
//
// Only a main repository is offered as a source. git would take a worktree
// as well and register the new one in its main all the same, but the result
// is the same sibling under a name that suggests a chain, so the list stays
// the projects a fork actually comes from.
func (s *Server) projectNewData(c *gin.Context, choice string) render.ProjectNewData {
	data := render.ProjectNewData{Page: s.page(c, "New Project", "projects"), Fill: projectNewFill(c)}
	data.Sources = worktreeSources(s.projects.List(), render.ProjectOptions(data.QuickNav.AllProjects))
	if choice == render.CreateClone {
		data.Create = choice
		return data
	}
	src, err := s.projects.FindByName(render.WorktreeSource(choice))
	if err != nil || !src.GitRepo || src.GitWorktree {
		return data
	}
	data.Create = choice
	data.Source = src.Name
	data.SourceBranch = src.GitBranch
	data.Branches = worktreeBranches(c.Request.Context(), s.projects, src)
	return data
}

// worktreeSources is what the source select offers: every main repository,
// and no worktree. The marks come from the quick nav's own project list, the
// same ones the other create forms hand their select, so this one is sorted
// in the browser by the mode the whole app shares.
func worktreeSources(projects []project.Project, options []render.ProjectOption) []render.ProjectOption {
	mains := map[string]bool{}
	for _, p := range projects {
		if p.GitRepo && !p.GitWorktree {
			mains[p.Name] = true
		}
	}
	out := make([]render.ProjectOption, 0, len(mains))
	for _, opt := range options {
		if mains[opt.Name] {
			out = append(out, opt)
		}
	}
	return out
}

// worktreeBranches is what the form offers: the repository's own branches
// first, then the ones that so far only exist on a remote, which a worktree
// takes by creating the local branch that follows them. A remote branch whose
// local branch exists is left out, it is already in the first list and the
// same working copy would come out of both.
//
// Every branch that stands in a working copy is marked with the project that
// holds it. git refuses a second checkout of it, and a form that says so
// before the button is pressed is the difference between a choice and a
// failed attempt.
func worktreeBranches(ctx context.Context, projects *project.Repository, src project.Project) []render.BranchChoice {
	repo := git.New(src.Path)
	refs, err := repo.Refs(ctx, git.RefSearch{Kinds: []string{git.KindBranch, git.KindRemote}, Limit: worktreeBranchLimit})
	if err != nil {
		log.Printf("worktree branches %s: %v", src.Path, err)
		return nil
	}
	taken := map[string]string{}
	worktrees, err := repo.Worktrees(ctx)
	if err != nil {
		log.Printf("worktree list %s: %v", src.Path, err)
	}
	for _, w := range worktrees {
		if w.Branch == "" {
			continue
		}
		if name := projects.ProjectNameFor(w.Path); name != "" {
			taken[w.Branch] = name
			continue
		}
		taken[w.Branch] = w.Path
	}
	local := map[string]bool{}
	out := make([]render.BranchChoice, 0, len(refs.Refs))
	for _, ref := range refs.Refs {
		if ref.Kind != git.KindBranch {
			continue
		}
		local[ref.Name] = true
		out = append(out, render.BranchChoice{Ref: ref.Name, Name: ref.Name, Taken: taken[ref.Name], Head: ref.Head})
	}
	for _, ref := range refs.Refs {
		if ref.Kind != git.KindRemote || local[ref.Branch] {
			continue
		}
		out = append(out, render.BranchChoice{Ref: ref.Name, Name: ref.Branch, Remote: true})
	}
	return out
}

// createWorktreeProject makes a project that is a linked worktree of another
// one: the directory through the same repository call every project is made
// with, and the working copy in it through git.
//
// The order is the careful one. The branch is resolved before anything is
// created, so a form that names something gone leaves no directory behind;
// the repository's write lock is held around both steps, so this cannot run
// beside a checkout or a commit of the same working copy; and a git that
// refuses takes the directory with it, which os.Remove does only while it is
// empty, so nobody's data is ever removed by a failed create.
func (s *Server) createWorktreeProject(c *gin.Context, form projectCreateForm) (project.Project, git.NewWorktree, error) {
	src, err := s.projects.FindByName(render.WorktreeSource(form.Create))
	if err != nil {
		return project.Project{}, git.NewWorktree{}, err
	}
	if !src.GitRepo {
		return project.Project{}, git.NewWorktree{}, fmt.Errorf("The project %q is no git repository.", src.Name)
	}
	if src.GitWorktree {
		return src, git.NewWorktree{}, worktreeSourceRefusal(src)
	}
	plan, err := worktreePlan(c.Request.Context(), src, form)
	if err != nil {
		return src, plan, err
	}
	keys, ok := gitWriteKeys(c, src)
	if !ok {
		log.Printf("worktree create %s: the working copy could not be named", src.Path)
		return src, plan, errors.New(gitUnknownCopy)
	}
	if !s.gitWrites.try(keys...) {
		return src, plan, errors.New(gitInUse)
	}
	defer s.gitWrites.release(keys...)
	path, err := s.projects.Create(form.Name.String())
	if err != nil {
		return src, plan, err
	}
	plan.Dir = path
	if err := git.New(src.Path).AddWorktree(gitWriteContext(c), plan); err != nil {
		log.Printf("worktree create %s in %s: %v", plan.Branch, path, err)
		_ = os.Remove(path)
		return src, plan, err
	}
	s.publishProjects()
	return src, plan, nil
}

// worktreePlan turns the form's branch choice into the one git call behind
// it, and answers what the repository cannot do before anything is made. The
// names are checked against the repository's own refs and not taken from the
// form as given: the form was rendered from that list a moment ago, and a
// branch that is gone since must not travel into a git argument as if it
// stood.
//
// A remote branch is not checked out as itself. It becomes the local branch
// that follows it, created at the remote ref, which is what checking one out
// does everywhere else in the cockpit.
func worktreePlan(ctx context.Context, src project.Project, form projectCreateForm) (git.NewWorktree, error) {
	refs, err := git.New(src.Path).Refs(ctx, git.RefSearch{Kinds: []string{git.KindBranch, git.KindRemote}, Limit: worktreeBranchLimit})
	if err != nil {
		return git.NewWorktree{}, errors.New("The repository's branches could not be read, so nothing was created. Try again.")
	}
	if form.BranchMode == "new" {
		name := strings.TrimSpace(form.NewBranch)
		if name == "" {
			return git.NewWorktree{}, errors.New("A name for the new branch is required.")
		}
		start := strings.TrimSpace(form.Start)
		if !hasRef(refs, start) {
			return git.NewWorktree{}, errors.New("Choose where the new branch starts.")
		}
		return git.NewWorktree{Branch: name, Start: start}, nil
	}
	pick := strings.TrimSpace(form.Branch)
	for _, ref := range refs.Refs {
		if ref.Kind == git.KindBranch && ref.Name == pick {
			return git.NewWorktree{Branch: pick}, nil
		}
	}
	for _, ref := range refs.Refs {
		if ref.Kind == git.KindRemote && ref.Name == pick {
			return git.NewWorktree{Branch: ref.Branch, Start: ref.Name}, nil
		}
	}
	return git.NewWorktree{}, errors.New("Choose the branch the worktree stands on.")
}

// hasRef reports whether the repository still knows this name, branch or
// remote branch alike.
func hasRef(refs git.RefMatches, name string) bool {
	if name == "" {
		return false
	}
	for _, ref := range refs.Refs {
		if ref.Name == name {
			return true
		}
	}
	return false
}

// createCloneProject makes a project directory and fills it from a
// repository that already exists somewhere else.
//
// Unlike the worktree create this one reaches the network, so it runs
// through runGitWrite, the same order the editor's own clone uses: the
// working copy is held, and the askpass bridge is open, so a remote that
// wants a passphrase or credentials asks the person in the browser instead
// of failing. A clone that does not come home leaves the directory empty,
// git cleans up after itself, and the empty directory goes with os.Remove.
func (s *Server) createCloneProject(c *gin.Context, name, remote string) (string, error) {
	if strings.TrimSpace(remote) == "" {
		return "", errors.New("A repository URL is required.")
	}
	path, err := s.projects.Create(name)
	if err != nil {
		return "", err
	}
	p, err := s.projects.FindByName(filepath.Base(path))
	if err != nil {
		_ = os.Remove(path)
		return "", err
	}
	if err := s.runGitWrite(c, p, "clone", func(repo *git.Repo) error {
		return repo.Clone(gitWriteContext(c), remote)
	}); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	s.publishProjects()
	return path, nil
}

// worktreeSourceRefusal answers a source that is itself a worktree. The form
// never offers one, so this is the hand written request, and it says where
// the fork belongs instead of only refusing.
func worktreeSourceRefusal(src project.Project) error {
	main := src.GitWorktreeOf
	if main == "" {
		main = src.GitWorktreeMain
	}
	if main == "" {
		return fmt.Errorf("%q is itself a worktree. Make the new one of the project it belongs to.", src.Name)
	}
	return fmt.Errorf("%q is itself a worktree of %q. Make the new one of that project.", src.Name, main)
}

// worktreeCreatedMessage is the flash of a created worktree: which project it
// forked, and the branch its working copy stands on, because that pair is the
// whole reason the project exists.
func worktreeCreatedMessage(name string, src project.Project, plan git.NewWorktree) string {
	return fmt.Sprintf("Project %q created as a worktree of %q on branch %q.", name, src.Name, plan.Branch)
}
