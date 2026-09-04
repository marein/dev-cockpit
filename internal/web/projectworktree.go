package web

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/git"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// worktreeBranchLimit bounds the listing the form is rendered from and the
// search a picked name is resolved against, per kind. A repository with more
// branches than this renders its defaults out of the most recently moved
// ones, which are the ones a new worktree is made for; nothing is ever cut
// off from being found, the pickers ask git with what was typed.
const worktreeBranchLimit = 500

// worktreeBranchPage bounds one round of a picker's autocomplete, per kind
// like the editor's own (editorRefsCap). With the search on the server this is
// the size of a list a person reads, and no longer the whole world a browser
// had to filter: a name past it is reached by typing more of it, not by
// scrolling.
const worktreeBranchPage = 50

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
	data.Branches = worktreeBranches(c.Request.Context(), s.projects, src, "", worktreeBranchLimit, false)
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

// worktreeBranches is one round of what the form offers: the repository's own
// branches first, then the ones that so far only exist on a remote, which a
// worktree takes by creating the local branch that follows them. text is what
// somebody typed into a picker, empty for the listing the page is rendered
// from and for a picker that just opened.
//
// A remote branch whose local branch exists is left out, it is already in the
// first list and the same working copy would come out of both. The local names
// that decides on come from the whole namespace (git.BranchNames) and not from
// the hits: under a search the local branch a remote hit collides with need
// not have matched, and offering it would offer a create git refuses.
//
// starts turns that rule off, because the starting point of a new branch is
// the one list where the two are not the same thing: a local branch and the
// remote branch it follows can have drifted apart, and beginning at one is not
// beginning at the other. Nothing is created under either name there, the new
// branch carries a name of its own, so nothing can collide.
//
// Every branch that stands in a working copy is marked with the project that
// holds it. git refuses a second checkout of it, and a form that says so
// before the button is pressed is the difference between a choice and a
// failed attempt.
func worktreeBranches(ctx context.Context, projects *project.Repository, src project.Project, text string, limit int, starts bool) []render.BranchChoice {
	repo := git.New(src.Path)
	refs, err := repo.Refs(ctx, git.RefSearch{Text: text, Kinds: []string{git.KindBranch, git.KindRemote}, Limit: limit})
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
	names, err := repo.BranchNames(ctx)
	if err != nil {
		log.Printf("worktree branch names %s: %v", src.Path, err)
	}
	local := map[string]bool{}
	for _, name := range names {
		local[name] = true
	}
	out := make([]render.BranchChoice, 0, len(refs.Refs))
	for _, ref := range refs.Refs {
		if ref.Kind != git.KindBranch {
			continue
		}
		out = append(out, render.BranchChoice{
			Ref:      ref.Name,
			Name:     ref.Name,
			Taken:    taken[ref.Name],
			Head:     ref.Head,
			Upstream: ref.Upstream,
			Ahead:    ref.Ahead,
			Behind:   ref.Behind,
		})
	}
	for _, ref := range refs.Refs {
		if ref.Kind != git.KindRemote || (!starts && local[ref.Branch]) {
			continue
		}
		out = append(out, render.BranchChoice{Ref: ref.Name, Name: ref.Branch, Remote: true})
	}
	return out
}

// handleProjectBranches answers one round of the create form's two pickers:
// the names a new worktree of this project could stand on, matched against
// `?q=`, capped per kind.
//
// The search runs here and not in the browser for the reason the editor's
// picker moved off the client (`git/refs`): a page that filters the list it
// was rendered with can only find what fitted in that list, and a repository
// is not obliged to keep the branch somebody is looking for among its five
// hundred most recent. It answers the rows the form already knows, marks and
// all, because the two pickers and the rendered defaults have to say the same
// thing about the same branch.
//
// `?pick=start` is the starting point picker asking, the one difference
// between the two lists (see worktreeBranches). Anything else is the checkout
// picker, which is also what an old page sending no parameter at all gets.
//
// The answer carries when this repository last heard from a remote
// (`fetchedAt`, empty for one that never has), because the resync sits in
// these lists now and somebody deciding whether to press it wants to know how
// old the names in front of them are. It is a file stat and no process.
//
// It only reads. A client that wants the remote branches to exist here first
// asks the fetch for it, which is the one route of the pair that touches the
// network.
func (s *Server) handleProjectBranches(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	branches := worktreeBranches(c.Request.Context(), s.projects, p, c.Query("q"), worktreeBranchPage, c.Query("pick") == "start")
	if branches == nil {
		branches = []render.BranchChoice{}
	}
	fetched := ""
	if at, ok := git.New(p.Path).LastFetch(c.Request.Context()); ok {
		fetched = at.UTC().Format(time.RFC3339)
	}
	c.JSON(http.StatusOK, gin.H{"branches": branches, "fetchedAt": fetched})
}

// handleProjectFetch brings a project's remotes down for the surfaces outside
// the editor: the create form's resync, which needs the remote branches to
// exist here before its pickers can offer them, because a branch nobody
// fetched is a branch this repository does not know, and the projects page's
// git menu, where a fetch is the one action that runs in place.
//
// It is the editor's own explicit fetch (handleEditorGitFetch) on a path of
// its own, so the passphrase question travels through the one askpass bridge
// and lands in the app-wide dialog, and a second write on that working copy
// reads the same refusal it always does.
//
// The answer carries the sentence a person reads beside the flag a client
// acts on: the resync repaints its list off `fetched`, the menu toasts
// `message`, which says what was fetched and where the checked out branch now
// stands. Worded here and not in the browser, because the distance comes out
// of git and one wording serves every surface.
func (s *Server) handleProjectFetch(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	fetched, ok := s.fetchRemotes(c, p)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"fetched": fetched, "message": fetchedMessage(c.Request.Context(), p, fetched)})
}

// fetchedMessage is the sentence a fetch answers with: whether anything was
// fetched, and how far the checked out branch stands from its upstream now,
// because right after a fetch is the moment that distance answers a question.
// The distance is one for-each-ref after an action a person started; the
// projects list itself never pays it, a process per row per render is what
// gitfacts exists to avoid. A detached HEAD, an unborn branch and a branch that
// follows nothing have no distance and get the first sentence alone.
func fetchedMessage(ctx context.Context, p project.Project, fetched bool) string {
	if !fetched {
		return fmt.Sprintf("Nothing to fetch, %q has no remote.", p.Name)
	}
	message := fmt.Sprintf("Fetched %q.", p.Name)
	ref, err := worktreeRef(ctx, p, p.GitBranch)
	if err != nil || ref.Kind != git.KindBranch || ref.Upstream == "" {
		return message
	}
	return message + " " + upstreamDistance(ref)
}

// upstreamDistance words where a branch stands against the branch it follows.
func upstreamDistance(ref git.Ref) string {
	switch {
	case ref.Ahead == 0 && ref.Behind == 0:
		return fmt.Sprintf("%q is up to date with %q.", ref.Name, ref.Upstream)
	case ref.Behind == 0:
		return fmt.Sprintf("%q is %s ahead of %q.", ref.Name, commitCount(ref.Ahead), ref.Upstream)
	case ref.Ahead == 0:
		return fmt.Sprintf("%q is %s behind %q.", ref.Name, commitCount(ref.Behind), ref.Upstream)
	}
	return fmt.Sprintf("%q has diverged from %q: %s ahead, %s behind.", ref.Name, ref.Upstream, commitCount(ref.Ahead), commitCount(ref.Behind))
}

func commitCount(n int) string {
	if n == 1 {
		return "1 commit"
	}
	return fmt.Sprintf("%d commits", n)
}

// worktreeCreate is what a created worktree project answers: the project it
// forked, the git call behind it, and how the catch up went. The last two
// fields are apart on purpose, a fast forward that did not run is not a failed
// create: the working copy stands either way and the flash says so.
type worktreeCreate struct {
	Source project.Project
	Plan   git.NewWorktree
	// CaughtUp names the ref the new working copy was moved onto, empty when
	// nothing was asked for or there was nothing to move onto.
	CaughtUp string
	// CatchUpErr is git's word on a catch up that was asked for and did not
	// run.
	CatchUpErr string
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
func (s *Server) createWorktreeProject(c *gin.Context, form projectCreateForm) (worktreeCreate, error) {
	made := worktreeCreate{}
	src, err := s.projects.FindByName(render.WorktreeSource(form.Create))
	if err != nil {
		return made, err
	}
	made.Source = src
	if !src.GitRepo {
		return made, fmt.Errorf("The project %q is no git repository.", src.Name)
	}
	if src.GitWorktree {
		return made, worktreeSourceRefusal(src)
	}
	plan, err := worktreePlan(c.Request.Context(), src, form)
	made.Plan = plan
	if err != nil {
		return made, err
	}
	keys, ok := gitWriteKeys(c, src)
	if !ok {
		log.Printf("worktree create %s: the working copy could not be named", src.Path)
		return made, errors.New(gitUnknownCopy)
	}
	if !s.gitWrites.try(keys...) {
		return made, errors.New(gitInUse)
	}
	defer s.gitWrites.release(keys...)
	path, err := s.projects.Create(form.Name.String())
	if err != nil {
		return made, err
	}
	made.Plan.Dir = path
	if err := git.New(src.Path).AddWorktree(gitWriteContext(c), made.Plan); err != nil {
		log.Printf("worktree create %s in %s: %v", made.Plan.Branch, path, err)
		_ = os.Remove(path)
		return made, err
	}
	if form.FastForward.Bool() {
		onto, err := catchUpWorktree(gitWriteContext(c), made.Plan.Dir, made.Plan.Branch)
		if err != nil {
			log.Printf("worktree catch up %s in %s: %v", made.Plan.Branch, path, err)
			made.CatchUpErr = err.Error()
		}
		made.CaughtUp = onto
	}
	s.publishProjects()
	return made, nil
}

// catchUpWorktree brings the new working copy up to the branch it follows,
// when that is all it takes, and answers the ref it moved onto.
//
// The form's checkbox is a wish and never an instruction. What was true while
// somebody looked at the form is read again here, in the working copy that now
// exists: only a branch that is still **purely** behind is moved, and an ahead
// or diverged one is left exactly where it is, because catching it up would be
// a merge and nobody decides a merge by ticking a box on a create form. A
// branch that follows nothing has nothing to catch up to.
//
// It runs where the new working copy is, against a ref that is already here,
// so it reaches no network and can be asked for no passphrase. That is the
// whole reason a create may do it at all: the form deliberately never fetches
// on its own.
func catchUpWorktree(ctx context.Context, dir, branch string) (string, error) {
	repo := git.New(dir)
	refs, err := repo.Refs(ctx, git.RefSearch{Text: branch, Kinds: []string{git.KindBranch}, Limit: worktreeBranchLimit})
	if err != nil {
		return "", err
	}
	for _, ref := range refs.Refs {
		if ref.Name != branch {
			continue
		}
		if ref.Upstream == "" || ref.Behind == 0 || ref.Ahead != 0 {
			return "", nil
		}
		if err := repo.FastForward(ctx, ref.Upstream); err != nil {
			return "", err
		}
		return ref.Upstream, nil
	}
	return "", nil
}

// worktreePlan turns the form's branch choice into the one git call behind
// it, and answers what the repository cannot do before anything is made. The
// names are checked against the repository's own refs and not taken from the
// form as given: the picker answered from that repository a moment ago, and a
// branch that is gone since must not travel into a git argument as if it
// stood.
//
// A remote branch is not checked out as itself. It becomes the local branch
// that follows it, created at the remote ref, which is what checking one out
// does everywhere else in the cockpit.
func worktreePlan(ctx context.Context, src project.Project, form projectCreateForm) (git.NewWorktree, error) {
	if form.BranchMode == "new" {
		name := strings.TrimSpace(form.NewBranch)
		if name == "" {
			return git.NewWorktree{}, errors.New("A name for the new branch is required.")
		}
		start := strings.TrimSpace(form.Start)
		ref, err := worktreeRef(ctx, src, start)
		if err != nil {
			return git.NewWorktree{}, err
		}
		if ref.Name == "" {
			return git.NewWorktree{}, errors.New("Choose where the new branch starts.")
		}
		return git.NewWorktree{Branch: name, Start: ref.Name}, nil
	}
	pick := strings.TrimSpace(form.Branch)
	ref, err := worktreeRef(ctx, src, pick)
	if err != nil {
		return git.NewWorktree{}, err
	}
	switch ref.Kind {
	case git.KindBranch:
		return git.NewWorktree{Branch: ref.Name}, nil
	case git.KindRemote:
		return git.NewWorktree{Branch: ref.Branch, Start: ref.Name}, nil
	}
	return git.NewWorktree{}, errors.New("Choose the branch the worktree stands on.")
}

// worktreeRef looks one picked name up in the repository, a branch before a
// remote one, the way the pickers list them. An empty Ref is the name the
// repository does not know; the caller words that, the two fields it can be
// missing from mean two different things to a person.
//
// It asks with the name as the search text rather than walking a listing,
// because a listing is capped and the pickers are not: a branch found by
// typing it must be creatable, and one below the cap of a plain listing would
// be offered and then refused. The search's own cap cannot lose it either, an
// exact name is one of at most a page of names carrying it.
func worktreeRef(ctx context.Context, src project.Project, name string) (git.Ref, error) {
	if name == "" {
		return git.Ref{}, nil
	}
	refs, err := git.New(src.Path).Refs(ctx, git.RefSearch{
		Text:  name,
		Kinds: []string{git.KindBranch, git.KindRemote},
		Limit: worktreeBranchLimit,
	})
	if err != nil {
		return git.Ref{}, errors.New("The repository's branches could not be read, so nothing was created. Try again.")
	}
	for _, kind := range []string{git.KindBranch, git.KindRemote} {
		for _, ref := range refs.Refs {
			if ref.Kind == kind && ref.Name == name {
				return ref, nil
			}
		}
	}
	return git.Ref{}, nil
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
// whole reason the project exists. A catch up that ran, or was asked for and
// did not, is the second sentence: the project is there either way, so this
// reports and never refuses.
func worktreeCreatedMessage(name string, made worktreeCreate) string {
	message := fmt.Sprintf("Project %q created as a worktree of %q on branch %q.", name, made.Source.Name, made.Plan.Branch)
	switch {
	case made.CatchUpErr != "":
		message += " It could not be fast-forwarded: " + made.CatchUpErr
	case made.CaughtUp != "":
		message += fmt.Sprintf(" Fast-forwarded to %q.", made.CaughtUp)
	}
	return message
}
