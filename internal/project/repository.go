// Package project manages project directories under a configured root.
package project

import (
	"bufio"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/local/dev-cockpit/internal/filesystem"
	"github.com/local/dev-cockpit/internal/recent"
)

// Project is one project directory.
type Project struct {
	Name                string
	Root                string
	Path                string
	Label               string
	GitBranch           string
	GitOrigin           string
	GitOriginURL        string
	GitRepo             bool
	ActiveSessions      int
	InactiveSessions    int
	ActiveSessionRefs   []SessionRef
	InactiveSessionRefs []SessionRef
	ShellRefs           []ShellRef
	LastUsedUnix        int64 // last time the project was opened; 0 = never
}

type SessionRef struct {
	ID   string
	Name string
	At   time.Time // started (active) or last updated (inactive); for date sorting
}

type ShellRef struct {
	ID   string
	Name string
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

// DefaultPath returns the first selectable project (for new-session defaults).
func (r *Repository) DefaultPath() string {
	p := r.SelectablePaths()
	if len(p) == 0 {
		return ""
	}
	return p[0]
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
		meta := gitMetadata(projects[i].Path)
		projects[i].GitRepo = meta.Repo
		projects[i].GitBranch = meta.Branch
		projects[i].GitOrigin = meta.Origin
		projects[i].GitOriginURL = meta.OriginURL
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
	meta := gitMetadata(p)
	return Project{
		Name:         filepath.Base(p),
		Root:         root,
		Path:         p,
		Label:        r.Label(p),
		GitBranch:    meta.Branch,
		GitOrigin:    meta.Origin,
		GitOriginURL: meta.OriginURL,
		GitRepo:      meta.Repo,
	}, nil
}

// ProjectNameFor returns the name of the top-level project that cwd lives under,
// or "" when cwd is outside the projects root. Unlike Find it accepts arbitrary
// subdirectories, so a session or shell working deep inside a project still maps
// back to it. It is cheap: it only inspects the path and reads no git metadata,
// so it is safe to call per entry in list/switcher views.
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

// Create makes a new project directory under the root.
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
	if _, err := os.Lstat(dir); err == nil {
		return "", fmt.Errorf("Project directory already exists: %s", dir)
	}
	if err := os.Mkdir(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(dir)
}

// Remove deletes a project directory. The caller is responsible for checking
// session conflicts first.
func (r *Repository) Remove(p Project) error {
	if !filesystem.IsUnder(p.Path, p.Root) {
		return fmt.Errorf("Refusing to delete a project outside %s.", p.Root)
	}
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

type gitInfo struct {
	Repo      bool
	Branch    string
	Origin    string
	OriginURL string
}

// gitMetadata returns only cheap, file-read git facts: whether dir is a repo,
// its current branch (from .git/HEAD) and its origin remote (from .git/config).
// It never shells out to git or walks the tree.
func gitMetadata(dir string) gitInfo {
	gitPath := filepath.Join(dir, ".git")
	if _, err := os.Stat(gitPath); err != nil {
		return gitInfo{}
	}
	rawOrigin := readOriginURL(gitPath)
	return gitInfo{
		Repo:      true,
		Branch:    readGitBranch(gitPath),
		Origin:    shortenGitOrigin(rawOrigin),
		OriginURL: gitOriginURL(rawOrigin),
	}
}

// readGitBranch returns the checked-out branch from .git/HEAD, or a short commit
// hash when HEAD is detached.
func readGitBranch(gitPath string) string {
	gitDir, err := resolveGitDir(gitPath)
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	head := strings.TrimSpace(string(data))
	if ref, ok := strings.CutPrefix(head, "ref: "); ok {
		return strings.TrimPrefix(strings.TrimSpace(ref), "refs/heads/")
	}
	if len(head) > 7 { // detached HEAD: raw commit hash
		return head[:7]
	}
	return head
}

// readOriginURL parses the "url" of [remote "origin"] from a .git/config file.
// gitPath may also be a regular file (linked worktree); in that case we follow
// the gitdir: pointer and then the worktree's commondir, since config/remotes
// live in the shared (main) git directory, not the per-worktree one.
func readOriginURL(gitPath string) string {
	gitDir, err := resolveGitDir(gitPath)
	if err != nil {
		return ""
	}
	f, err := os.Open(filepath.Join(gitCommonDir(gitDir), "config"))
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	inOrigin := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inOrigin = line == `[remote "origin"]`
			continue
		}
		if !inOrigin {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if strings.TrimSpace(key) == "url" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// resolveGitDir returns the real git directory for a .git path, following the
// "gitdir:" pointer when .git is a file (linked worktree).
func resolveGitDir(gitPath string) (string, error) {
	info, err := os.Stat(gitPath)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return gitPath, nil
	}
	data, err := os.ReadFile(gitPath)
	if err != nil {
		return "", err
	}
	const prefix = "gitdir:"
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		target, ok := strings.CutPrefix(line, prefix)
		if !ok {
			continue
		}
		target = strings.TrimSpace(target)
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(gitPath), target)
		}
		return target, nil
	}
	return "", errors.New("no gitdir pointer")
}

// gitCommonDir returns the shared git directory for gitDir. For a linked
// worktree, gitDir holds only HEAD/index/etc.; the config and remotes live in
// the main git directory pointed to by the "commondir" file. For a normal repo
// there is no commondir file and gitDir is returned unchanged (one cheap, failed
// file read — no extra cost worth worrying about).
func gitCommonDir(gitDir string) string {
	data, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return gitDir
	}
	common := strings.TrimSpace(string(data))
	if common == "" {
		return gitDir
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(gitDir, common)
	}
	return filepath.Clean(common)
}

func shortenGitOrigin(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	s = strings.TrimPrefix(s, "ssh://")
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "git@")
	s = strings.TrimSuffix(s, ".git")
	s = strings.ReplaceAll(s, ":", "/")
	s = strings.TrimPrefix(s, "/")
	return s
}

func gitOriginURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		u, err := url.Parse(s)
		if err != nil || u.Host == "" || u.Path == "" {
			return ""
		}
		u.User = nil
		u.RawQuery = ""
		u.Fragment = ""
		u.Path = strings.TrimSuffix(u.Path, ".git")
		return u.String()
	}
	if strings.HasPrefix(s, "ssh://") {
		u, err := url.Parse(s)
		if err != nil {
			return ""
		}
		host := u.Hostname()
		path := strings.TrimPrefix(strings.TrimSuffix(u.Path, ".git"), "/")
		if host == "" || path == "" {
			return ""
		}
		return "https://" + host + "/" + path
	}
	if strings.HasPrefix(s, "git@") {
		host, path, ok := strings.Cut(strings.TrimPrefix(s, "git@"), ":")
		path = strings.TrimPrefix(strings.TrimSuffix(path, ".git"), "/")
		if !ok || host == "" || path == "" {
			return ""
		}
		return "https://" + host + "/" + path
	}
	return ""
}
