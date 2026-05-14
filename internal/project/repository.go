// Package project manages project directories under a configured root.
package project

import (
	"bufio"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/local/dev-cockpit/internal/filesystem"
	"golang.org/x/sync/errgroup"
)

// Project is one project directory.
type Project struct {
	Name                string
	Root                string
	Path                string
	Label               string
	Size                string
	GitBranch           string
	GitOrigin           string
	GitOriginURL        string
	GitRepo             bool
	GitChanges          int
	GitAhead            int
	GitBehind           int
	ActiveSessions      int
	InactiveSessions    int
	ActiveSessionRefs   []SessionRef
	InactiveSessionRefs []SessionRef
}

type SessionRef struct {
	ID   string
	Name string
}

// Repository wraps the on-disk projects root.
type Repository struct {
	Root                string
	MetadataConcurrency int
}

// NewRepository creates a Repository for the given root directory.
func NewRepository(root string, metadataConcurrency int) *Repository {
	if metadataConcurrency <= 0 {
		metadataConcurrency = 1
	}
	return &Repository{Root: root, MetadataConcurrency: metadataConcurrency}
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
	sort.Slice(out, func(i, j int) bool {
		a, b := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name)
		if a != b {
			return a < b
		}
		return strings.ToLower(out[i].Path) < strings.ToLower(out[j].Path)
	})
	return out
}

func (r *Repository) enrichProjects(projects []Project) {
	var g errgroup.Group
	g.SetLimit(r.MetadataConcurrency)
	for i := range projects {
		i := i
		g.Go(func() error {
			meta := gitMetadata(projects[i].Path)
			projects[i].GitBranch = meta.Branch
			projects[i].GitOrigin = meta.Origin
			projects[i].GitOriginURL = meta.OriginURL
			projects[i].GitRepo = meta.Repo
			projects[i].GitChanges = meta.Changes
			projects[i].GitAhead = meta.Ahead
			projects[i].GitBehind = meta.Behind
			return nil
		})
		g.Go(func() error {
			projects[i].Size = filesystem.PathSize(projects[i].Path)
			return nil
		})
	}
	_ = g.Wait()
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
		Size:         filesystem.PathSize(p),
		GitBranch:    meta.Branch,
		GitOrigin:    meta.Origin,
		GitOriginURL: meta.OriginURL,
		GitRepo:      meta.Repo,
		GitChanges:   meta.Changes,
		GitAhead:     meta.Ahead,
		GitBehind:    meta.Behind,
	}, nil
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
	Changes   int
	Ahead     int
	Behind    int
}

func gitMetadata(dir string) gitInfo {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return gitInfo{}
	}
	info := parseGitStatus(gitOutput(dir, "status", "--porcelain=v2", "--branch"))
	if !info.Repo {
		return gitInfo{}
	}
	rawOrigin := readOriginURL(filepath.Join(dir, ".git"))
	info.Origin = shortenGitOrigin(rawOrigin)
	info.OriginURL = gitOriginURL(rawOrigin)
	return info
}

// readOriginURL parses the "url" of [remote "origin"] from a .git/config file.
// gitDir may also be a regular file (linked worktree); in that case we follow
// the gitdir: pointer to the real git directory.
func readOriginURL(gitDir string) string {
	configPath, err := resolveGitConfigPath(gitDir)
	if err != nil {
		return ""
	}
	f, err := os.Open(configPath)
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

func resolveGitConfigPath(gitDir string) (string, error) {
	info, err := os.Stat(gitDir)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return filepath.Join(gitDir, "config"), nil
	}
	data, err := os.ReadFile(gitDir)
	if err != nil {
		return "", err
	}
	const prefix = "gitdir:"
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		target := strings.TrimSpace(strings.TrimPrefix(line, prefix))
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(gitDir), target)
		}
		return filepath.Join(target, "config"), nil
	}
	return "", errors.New("no gitdir pointer")
}

func gitOutput(dir string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func parseGitStatus(raw string) gitInfo {
	out := gitInfo{}
	if strings.TrimSpace(raw) == "" {
		return out
	}
	out.Repo = true
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "# branch.head "):
			out.Branch = strings.TrimPrefix(line, "# branch.head ")
		case strings.HasPrefix(line, "# branch.ab "):
			var ahead, behind int
			if _, err := fmt.Sscanf(strings.TrimPrefix(line, "# branch.ab "), "+%d -%d", &ahead, &behind); err == nil {
				out.Ahead = ahead
				out.Behind = behind
			}
		case strings.HasPrefix(line, "# "):
			continue
		default:
			out.Changes++
		}
	}
	if out.Branch == "HEAD" || out.Branch == "(detached)" {
		out.Branch = gitDetachedHead(raw)
	}
	return out
}

func gitDetachedHead(raw string) string {
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# branch.oid ") {
			oid := strings.TrimPrefix(line, "# branch.oid ")
			if oid != "" && oid != "(initial)" {
				if len(oid) > 7 {
					return oid[:7]
				}
				return oid
			}
		}
	}
	return ""
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
