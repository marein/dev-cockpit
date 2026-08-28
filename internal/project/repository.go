// Package project manages project directories under a configured root.
package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/gitfacts"
	"github.com/marein/dev-cockpit/internal/recent"
)

// Project is one project directory.
type Project struct {
	Name              string
	Root              string
	Path              string
	Label             string
	GitBranch         string
	GitOrigin         string
	GitOriginURL      string
	GitRepo           bool
	GitWorktree       bool          // .git is a gitdir pointer file of a linked worktree
	GitWorktreeOf     string        // main repository's project name, "" when it is no project under the root
	GitWorktreeMain   string        // main repository's path on disk, the fallback label when GitWorktreeOf is empty
	GitWorktrees      []WorktreeRef // linked worktrees registered in this repository, empty for a worktree itself
	ActiveCoders      int
	InactiveCoders    int
	ActiveCoderRefs   []CoderRef
	InactiveCoderRefs []CoderRef
	ShellRefs         []ShellRef
	ActiveRefs        []TerminalRef // coders and shells merged in tab strip order
	LastUsedUnix      int64         // last time the project was opened; 0 = never
	HasNews           bool          // any coder or shell below has an unread notification
}

type CoderRef struct {
	ID       string
	Name     string
	Coder    string    // owning coder id, for the badge when several coders run
	At       time.Time // started (active) or last updated (inactive); for date sorting
	TabPos   int       // tab strip position from @dc_tab_pos, 0 when unset (active only)
	Group    string    // split view group id from @dc_tab_group, empty when ungrouped
	GroupPos int       // position inside the group from @dc_tab_gpos, 0 when unset
	HasNews  bool      // an unread notification points at this coder
}

type ShellRef struct {
	ID       string
	Name     string
	At       time.Time // started; tiebreak for the tab order sort
	TabPos   int       // tab strip position from @dc_tab_pos, 0 when unset
	Group    string    // split view group id from @dc_tab_group, empty when ungrouped
	GroupPos int       // position inside the group from @dc_tab_gpos, 0 when unset
	HasNews  bool      // an unread notification points at this shell
}

// WorktreeRef is one linked worktree registered in a project's repository.
type WorktreeRef struct {
	Path    string // worktree directory on disk
	Project string // its project name, "" when it does not lie directly under the root
}

// TerminalRef is one live coder or shell of a project, merged into the tab
// strip order for the projects page chip row.
type TerminalRef struct {
	ID      string
	Name    string
	Kind    string // "coder" or "shell"
	Coder   string // owning coder id when Kind is "coder"
	HasNews bool
}

// Repository wraps the on-disk projects root.
type Repository struct {
	Root   string
	recent *recent.Store
}

// NewRepository creates a Repository for the given root directory. The store
// records and supplies per-project last-used timestamps.
func NewRepository(root string, store *recent.Store) *Repository {
	return &Repository{Root: root, recent: store}
}

// Touch records that the named project was just opened.
func (r *Repository) Touch(name string) {
	if r.recent != nil {
		r.recent.Touch(name)
	}
}

// EnsureRoot creates the root if missing and returns its resolved path.
func (r *Repository) EnsureRoot() (string, error) {
	info, err := os.Lstat(r.Root)
	if err == nil && !info.IsDir() {
		return "", fmt.Errorf("Configured projects directory is not a directory: %s", r.Root)
	}
	if err := os.MkdirAll(r.Root, 0o755); err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(r.Root)
}

func (r *Repository) resolvedRoot() string {
	p, err := r.EnsureRoot()
	if err != nil {
		return ""
	}
	return p
}

// SelectablePaths returns absolute project directory paths suitable as session
// working directories.
func (r *Repository) SelectablePaths() []string {
	root := r.resolvedRoot()
	if root == "" {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		full := filepath.Join(root, e.Name())
		info, err := os.Lstat(full)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			continue
		}
		resolved, err := filepath.EvalSymlinks(full)
		if err != nil {
			continue
		}
		out = append(out, resolved)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

// List returns all selectable projects as Project records.
func (r *Repository) List() []Project {
	root := r.resolvedRoot()
	if root == "" {
		return nil
	}
	paths := r.SelectablePaths()
	out := make([]Project, len(paths))
	for i, p := range paths {
		out[i] = Project{
			Name:  filepath.Base(p),
			Root:  root,
			Path:  p,
			Label: r.Label(p),
		}
	}
	r.enrichProjects(out)
	if r.recent != nil {
		used := r.recent.Times()
		for i := range out {
			out[i].LastUsedUnix = used[out[i].Name]
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name)
		if a != b {
			return a < b
		}
		return strings.ToLower(out[i].Path) < strings.ToLower(out[j].Path)
	})
	return out
}

// enrichProjects fills in lightweight git metadata. It only reads a couple of
// files inside .git (HEAD and config) — no `git status`, no directory walk — so
// it is cheap enough to run inline for every project on each request.
func (r *Repository) enrichProjects(projects []Project) {
	for i := range projects {
		meta := gitfacts.Read(projects[i].Path)
		projects[i].GitRepo = meta.Repo
		projects[i].GitBranch = meta.Branch
		projects[i].GitOrigin = gitfacts.OriginName(meta.Origin)
		projects[i].GitOriginURL = gitfacts.OriginWebURL(meta.Origin)
		projects[i].GitWorktree = meta.Worktree
		projects[i].GitWorktreeOf = r.rootProjectName(meta.WorktreeMain)
		projects[i].GitWorktreeMain = meta.WorktreeMain
		projects[i].GitWorktrees = r.worktreeRefs(meta.WorktreePaths)
	}
}

// ValidatePath checks that raw is a non-symlink project directly under the root.
func (r *Repository) ValidatePath(raw string) (string, error) {
	workdir := strings.TrimSpace(raw)
	if workdir == "" {
		return "", errors.New("Project is required.")
	}
	root, err := r.EnsureRoot()
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(workdir)
	if err != nil {
		return "", fmt.Errorf("Selected project does not exist: %s", workdir)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("Selected project must not be a symlink: %s", workdir)
	}
	resolved, err := filepath.EvalSymlinks(workdir)
	if err != nil {
		return "", fmt.Errorf("Selected project does not exist: %s", workdir)
	}
	info, err = os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("Selected project is not a directory: %s", resolved)
	}
	if filepath.Dir(resolved) != root {
		return "", fmt.Errorf("Selected project is not allowed: %s", resolved)
	}
	return resolved, nil
}

// Find resolves a project from a raw path.
func (r *Repository) Find(raw string) (Project, error) {
	p, err := r.ValidatePath(raw)
	if err != nil {
		return Project{}, err
	}
	root, _ := r.EnsureRoot()
	meta := gitfacts.Read(p)
	return Project{
		Name:            filepath.Base(p),
		Root:            root,
		Path:            p,
		Label:           r.Label(p),
		GitBranch:       meta.Branch,
		GitOrigin:       gitfacts.OriginName(meta.Origin),
		GitOriginURL:    gitfacts.OriginWebURL(meta.Origin),
		GitRepo:         meta.Repo,
		GitWorktree:     meta.Worktree,
		GitWorktreeOf:   r.rootProjectName(meta.WorktreeMain),
		GitWorktreeMain: meta.WorktreeMain,
		GitWorktrees:    r.worktreeRefs(meta.WorktreePaths),
	}, nil
}

// ProjectNameFor returns the name of the top-level project that cwd lives under,
// or "" when cwd is outside the projects root. Unlike Find it accepts arbitrary
// subdirectories, so a session or shell working deep inside a project still maps
// back to it. It is cheap: it only inspects the path and reads no git metadata,
// so it is safe to call per entry in list/quick-nav views.
func (r *Repository) ProjectNameFor(cwd string) string {
	dir := strings.TrimSpace(cwd)
	if dir == "" {
		return ""
	}
	root := r.resolvedRoot()
	if root == "" {
		return ""
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return ""
	}
	if i := strings.IndexRune(rel, filepath.Separator); i >= 0 {
		return rel[:i]
	}
	return rel
}

// FindByName resolves a project from its directory name under the configured root.
func (r *Repository) FindByName(raw string) (Project, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return Project{}, errors.New("Project is required.")
	}
	root, err := r.EnsureRoot()
	if err != nil {
		return Project{}, err
	}
	return r.Find(filepath.Join(root, name))
}

// Label returns a short user-facing label (path relative to root when possible).
func (r *Repository) Label(path string) string {
	root := r.resolvedRoot()
	if root != "" {
		if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return path
}

// ErrExists is what Create answers for a name whose directory already holds
// content. It is a sentinel so a caller can tell the taken name apart from a
// name that cannot be used at all.
var ErrExists = errors.New("A project with that name already exists.")

// Create makes a new project directory under the root. A directory that
// already exists but is empty is adopted and answered like a fresh one, a
// leftover of an earlier attempt is a place to fill and not a refusal;
// anything with content in it, and anything that is not a plain directory,
// is somebody's data and answers ErrExists.
func (r *Repository) Create(rawName string) (string, error) {
	root, err := r.EnsureRoot()
	if err != nil {
		return "", err
	}
	name, err := sanitizeDirName(rawName)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, name)
	if info, err := os.Lstat(dir); err == nil {
		if info.IsDir() {
			entries, err := os.ReadDir(dir)
			if err != nil {
				return "", err
			}
			if len(entries) == 0 {
				return filepath.EvalSymlinks(dir)
			}
		}
		return "", ErrExists
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.Mkdir(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(dir)
}

// Remove deletes a project directory. The caller is responsible for checking
// session conflicts first. A project that is a linked worktree takes its
// registration in the main repository with it, what a later `git worktree
// prune` would do; without that the main keeps the worktree's branch checked
// out and refuses it.
func (r *Repository) Remove(p Project) error {
	if !filesystem.IsUnder(p.Path, p.Root) {
		return fmt.Errorf("Refusing to delete a project outside %s.", p.Root)
	}
	gitfacts.PruneWorktreeRegistration(p.Path)
	return os.RemoveAll(p.Path)
}

func sanitizeDirName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", errors.New("Project directory name is required.")
	}
	out := filesystem.ToDirectoryName(name)
	if out == "" {
		return "", errors.New("Project directory name must include at least one letter or number.")
	}
	return out, nil
}

// rootProjectName maps a directory onto the name of the project it is, or ""
// when it is not a directory directly under the projects root.
func (r *Repository) rootProjectName(path string) string {
	if path == "" {
		return ""
	}
	root := r.resolvedRoot()
	if root == "" {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ""
	}
	if filepath.Dir(resolved) != root {
		return ""
	}
	return filepath.Base(resolved)
}

// worktreeRefs turns registered worktree paths into refs the delete confirm
// and the delete handler read: the path, and the project name when the
// worktree is a project of its own.
func (r *Repository) worktreeRefs(paths []string) []WorktreeRef {
	var out []WorktreeRef
	for _, p := range paths {
		out = append(out, WorktreeRef{Path: p, Project: r.rootProjectName(p)})
	}
	return out
}
