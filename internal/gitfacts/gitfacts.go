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
	"fmt"
	"net"
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

// Remote is a remote url taken apart into what names a repository. Scheme is
// the url's own, "ssh" for the scp form; Host is lower case, without user and
// port; Path has no leading or trailing slash and no .git.
type Remote struct {
	Scheme string
	Host   string
	Port   string
	Path   string
}

// ParseRemote reads a remote url in every form git takes for a server:
// scheme://[user@]host[:port]/path and the scp form [user@]host:path, each
// with or without .git and a trailing slash. A local path, a file:// url and
// anything without both a host and a path answer false.
func ParseRemote(raw string) (Remote, bool) {
	s := strings.TrimSpace(raw)
	var r Remote
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil || u.Scheme == "file" {
			return Remote{}, false
		}
		r = Remote{Scheme: strings.ToLower(u.Scheme), Host: u.Hostname(), Port: u.Port(), Path: u.Path}
	} else {
		// git reads a colon as the scp form only when no slash comes before
		// it, which is what keeps ./a:b a local path.
		hostPart, path, ok := strings.Cut(s, ":")
		if !ok || strings.Contains(hostPart, "/") {
			return Remote{}, false
		}
		if at := strings.LastIndex(hostPart, "@"); at >= 0 {
			hostPart = hostPart[at+1:]
		}
		r = Remote{Scheme: "ssh", Host: hostPart, Path: path}
	}
	r.Host = strings.ToLower(r.Host)
	r.Path = strings.Trim(strings.TrimSuffix(strings.TrimRight(r.Path, "/"), ".git"), "/")
	if r.Host == "" || r.Path == "" {
		return Remote{}, false
	}
	return r, true
}

// OriginName is the short display form of a remote url: host and path, no
// scheme, no user, no port, no .git suffix. A local path stays the path.
func OriginName(raw string) string {
	if r, ok := ParseRemote(raw); ok {
		return r.Host + "/" + r.Path
	}
	return strings.TrimSuffix(strings.TrimRight(strings.TrimSpace(raw), "/"), ".git")
}

// OriginWebURL turns a remote url into the https address a browser can open,
// or "" when the url has no such form. An http or https remote keeps its
// scheme and port, every other transport's port is not the web server's.
func OriginWebURL(raw string) string {
	r, ok := ParseRemote(raw)
	if !ok {
		return ""
	}
	u := url.URL{Scheme: "https", Host: r.Host, Path: "/" + r.Path}
	if strings.Contains(r.Host, ":") {
		u.Host = "[" + r.Host + "]"
	}
	if r.Scheme == "http" || r.Scheme == "https" {
		u.Scheme = r.Scheme
		if r.Port != "" {
			u.Host = net.JoinHostPort(r.Host, r.Port)
		}
	}
	return u.String()
}

// RefStamp marks how the files stand that move when the commit a branch was
// last pushed at moves: HEAD, the config with the upstreams, the packed refs,
// the ref table and the remote's loose refs of HEAD's branch and of the
// upstream it tracks there. A fetch, a push, a checkout or a pack that writes
// one of them changes it, for a few stats. A directory without a repository
// answers "".
func RefStamp(dir, remote string) string {
	gitPath := filepath.Join(dir, ".git")
	gitDir, err := resolveGitDir(gitPath)
	if err != nil {
		return ""
	}
	common := commonDir(gitDir)
	files := []string{
		filepath.Join(gitDir, "HEAD"),
		filepath.Join(common, "config"),
		filepath.Join(common, "packed-refs"),
		filepath.Join(common, "reftable", "tables.list"),
	}
	if branch := readBranch(gitPath); branch != "" {
		files = append(files, filepath.Join(common, "refs", "remotes", remote, filepath.FromSlash(branch)))
		if upstream := readUpstream(common, branch, remote); upstream != "" && upstream != branch {
			files = append(files, filepath.Join(common, "refs", "remotes", remote, filepath.FromSlash(upstream)))
		}
	}
	var b strings.Builder
	for _, f := range files {
		if fi, err := os.Stat(f); err == nil {
			fmt.Fprintf(&b, "%d:%d;", fi.ModTime().UnixNano(), fi.Size())
		} else {
			b.WriteString("-;")
		}
	}
	return b.String()
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
	return configSection(commonDir(gitDir), `[remote "origin"]`)["url"]
}

// readUpstream names the branch on remote that branch tracks, read off its
// merge in the config of the common git directory, "" when it tracks none
// there.
func readUpstream(common, branch, remote string) string {
	quoted := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(branch)
	keys := configSection(common, `[branch "`+quoted+`"]`)
	if keys["remote"] != remote {
		return ""
	}
	upstream, _ := strings.CutPrefix(keys["merge"], "refs/heads/")
	return upstream
}

// configSection reads the keys of one section of the config in the common
// git directory, the section named by its header the way git writes it, each
// key lower cased with its first value.
func configSection(common, header string) map[string]string {
	keys := map[string]string{}
	f, err := os.Open(filepath.Join(common, "config"))
	if err != nil {
		return keys
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	inside := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inside = line == header
			continue
		}
		if !inside {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		if _, seen := keys[key]; !seen {
			keys[key] = strings.TrimSpace(value)
		}
	}
	return keys
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
