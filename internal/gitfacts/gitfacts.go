// Package gitfacts reads cheap git facts straight from the files of a
// repository: HEAD, config, the gitdir pointer of a linked worktree and the
// registrations under worktrees/. It never runs git and never walks a tree,
// which is what makes it safe to call per project on every request; the
// package that runs the binary is internal/git, and the two deliberately do
// not meet.
package gitfacts

import (
	"bufio"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Info is what the file reads answer about one directory.
type Info struct {
	Repo          bool
	Branch        string   // checked-out branch, or a short hash on a detached HEAD
	Origin        string   // the origin remote's url as written in the config
	Worktree      bool     // dir is a linked worktree (.git is a gitdir pointer file)
	WorktreeMain  string   // main repository path, "" when it cannot be derived
	WorktreePaths []string // worktree directories registered in this repository
}

// Read collects the facts about dir. A directory without a .git answers the
// zero Info, and every fact that cannot be read stays empty instead of
// becoming an error: these are chips on a list, not a diagnosis.
func Read(dir string) Info {
	gitPath := filepath.Join(dir, ".git")
	fi, err := os.Stat(gitPath)
	if err != nil {
		return Info{}
	}
	out := Info{
		Repo:   true,
		Branch: readBranch(gitPath),
		Origin: readOriginURL(gitPath),
	}
	if fi.IsDir() {
		out.WorktreePaths = registeredWorktrees(gitPath)
	} else {
		out.Worktree, out.WorktreeMain = worktreeTarget(gitPath)
	}
	return out
}

// OriginName is the short display form of a remote url: host and path, no
// scheme, no user, no .git suffix.
func OriginName(raw string) string {
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

// OriginWebURL turns a remote url into the https address a browser can open,
// or "" when the url has no such form.
func OriginWebURL(raw string) string {
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

// PruneWorktreeRegistration removes the metadata directory a linked worktree
// stands registered under in its main repository, what a later `git worktree
// prune` would do; without it the main keeps the worktree's branch checked out
// and refuses it. The registration's gitdir file is read back first, only an
// entry that names this very worktree is taken; any doubt, a dangling pointer
// included, leaves the main untouched. The one write of this package, and a
// file operation like everything else in it.
func PruneWorktreeRegistration(dir string) {
	gitPath := filepath.Join(dir, ".git")
	fi, err := os.Stat(gitPath)
	if err != nil || fi.IsDir() {
		return
	}
	gitDir, err := resolveGitDir(gitPath)
	if err != nil {
		return
	}
	if filepath.Base(filepath.Dir(gitDir)) != "worktrees" {
		return
	}
	data, err := os.ReadFile(filepath.Join(gitDir, "gitdir"))
	if err != nil {
		return
	}
	registered := strings.TrimSpace(string(data))
	if registered == "" {
		return
	}
	if !filepath.IsAbs(registered) {
		registered = filepath.Join(gitDir, registered)
	}
	if !samePath(registered, gitPath) {
		return
	}
	os.RemoveAll(gitDir)
}

// registeredWorktrees lists the worktree directories registered under
// <gitPath>/worktrees. Only entries whose gitdir file names a worktree whose
// own .git file points back at the entry are answered, that double link is
// what makes the path trustworthy; a deleted or half pruned worktree drops
// out on one of the two reads.
func registeredWorktrees(gitPath string) []string {
	entries, err := os.ReadDir(filepath.Join(gitPath, "worktrees"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		metaDir := filepath.Join(gitPath, "worktrees", e.Name())
		data, err := os.ReadFile(filepath.Join(metaDir, "gitdir"))
		if err != nil {
			continue
		}
		wtGit := strings.TrimSpace(string(data))
		if wtGit == "" {
			continue
		}
		if !filepath.IsAbs(wtGit) {
			wtGit = filepath.Join(metaDir, wtGit)
		}
		target, err := resolveGitDir(wtGit)
		if err != nil || !samePath(target, metaDir) {
			continue
		}
		out = append(out, filepath.Dir(wtGit))
	}
	return out
}

// worktreeTarget classifies a .git that is a regular file. It is a linked
// worktree when the gitdir pointer resolves to an existing directory under a
// "worktrees" parent, which keeps submodules (gitdir under "modules") out. The
// main repository path follows git's standard <main>/.git/worktrees/<name>
// layout; a bare main repository has no ".git" component there, so its own
// directory is the main path. A dangling pointer answers no worktree at all.
func worktreeTarget(gitPath string) (bool, string) {
	gitDir, err := resolveGitDir(gitPath)
	if err != nil {
		return false, ""
	}
	fi, err := os.Stat(gitDir)
	if err != nil || !fi.IsDir() {
		return false, ""
	}
	worktrees := filepath.Dir(gitDir)
	if filepath.Base(worktrees) != "worktrees" {
		return false, ""
	}
	mainGit := filepath.Dir(worktrees)
	if filepath.Base(mainGit) != ".git" {
		return true, mainGit
	}
	return true, filepath.Dir(mainGit)
}

// readBranch returns the checked-out branch from HEAD, or a short commit hash
// when HEAD is detached.
func readBranch(gitPath string) string {
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

// readOriginURL parses the "url" of [remote "origin"] from the config. gitPath
// may be a regular file (linked worktree); then the gitdir: pointer and the
// worktree's commondir are followed, since config and remotes live in the
// shared (main) git directory, not the per-worktree one.
func readOriginURL(gitPath string) string {
	gitDir, err := resolveGitDir(gitPath)
	if err != nil {
		return ""
	}
	f, err := os.Open(filepath.Join(commonDir(gitDir), "config"))
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
	fi, err := os.Stat(gitPath)
	if err != nil {
		return "", err
	}
	if fi.IsDir() {
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

// commonDir returns the shared git directory for gitDir. For a linked
// worktree, gitDir holds only HEAD/index/etc.; the config and remotes live in
// the main git directory pointed to by the "commondir" file. For a normal repo
// there is no commondir file and gitDir is returned unchanged (one cheap,
// failed file read, no extra cost worth worrying about).
func commonDir(gitDir string) string {
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

// samePath reports whether two paths name the same place, resolved when both
// resolve, cleaned otherwise.
func samePath(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA == nil && errB == nil {
		return ra == rb
	}
	return filepath.Clean(a) == filepath.Clean(b)
}
