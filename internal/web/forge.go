package web

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/git"
	"github.com/marein/dev-cockpit/internal/gitfacts"
	"github.com/marein/dev-cockpit/internal/project"
)

// forgeHeadsAge is how long a pushed head read stands while the files it
// was read from did not move.
const forgeHeadsAge = time.Minute

// SetForge hands the server the forge service. A server without it binds no
// origin to a forge.
func (s *Server) SetForge(f *forge.Service) {
	s.forge = f
	s.forgeHeads = &forgeHeads{heads: map[string]forgeHead{}}
}

// forgeByProject binds the repositories among the projects to their forges,
// keyed by project name. The binding is read off the overrides and the
// origins alone, nothing asks a forge here. A file the server cannot read
// leaves what it holds out, the settings page and a project's forge say what
// went wrong.
func (s *Server) forgeByProject(projects []project.Project) map[string]forge.Binding {
	if s.forge == nil {
		return nil
	}
	bindings, _ := s.forge.Bindings()
	out := map[string]forge.Binding{}
	for _, p := range projects {
		if !p.GitRepo {
			continue
		}
		if b, ok := bindings.Bind(forgeOwner(p), p.GitOriginRaw); ok {
			out[p.Name] = b
		}
	}
	return out
}

// forgePolled answers what the forge poller holds of the bound projects'
// branches, keyed by project name. It reads the poller's cache alone.
func (s *Server) forgePolled(projects []project.Project, bound map[string]forge.Binding) map[string]forge.Polled {
	out := map[string]forge.Polled{}
	for _, p := range projects {
		b, ok := bound[p.Name]
		if !ok {
			continue
		}
		if polled, ok := s.forge.Polled(p.Name, b, p.GitBranch); ok {
			out[p.Name] = polled
		}
	}
	return out
}

// ForgeWatches answers the branches the forge poller keeps: of every
// repository bound where polled says the poller reads, the branch HEAD is on
// at the commit origin's tracking ref names. A detached HEAD keeps nothing,
// a repository whose git never answered keeps what it holds. Bindings that
// cannot be read are an error, so a file read badly takes no project's
// status away.
func (s *Server) ForgeWatches(ctx context.Context, polled func(forge.Binding) bool) ([]forge.Watch, error) {
	bindings, err := s.forge.Bindings()
	if err != nil {
		return nil, err
	}
	var watches []forge.Watch
	dirs := map[string]bool{}
	for _, p := range s.projects.List() {
		if !p.GitRepo {
			continue
		}
		b, ok := bindings.Bind(forgeOwner(p), p.GitOriginRaw)
		if !ok || !polled(b) {
			continue
		}
		dirs[p.Path] = true
		pushed, ok, err := s.forgeHeads.pushed(ctx, p.Path)
		switch {
		case err != nil:
			watches = append(watches, forge.Watch{Project: p.Name, Binding: b})
		case ok:
			watches = append(watches, forge.Watch{Project: p.Name, Binding: b, Branch: pushed.Branch, Remote: pushed.Remote, Head: pushed.Head})
		}
	}
	s.forgeHeads.only(dirs)
	return watches, nil
}

// forgeHeads holds where the branch HEAD is on of each repository the forge
// poller keeps was last pushed. git reads it again only when the files a
// push, a fetch or a checkout writes moved, or forgeHeadsAge went by, so the
// poller's look every few seconds starts no git.
type forgeHeads struct {
	mu    sync.Mutex
	heads map[string]forgeHead
}

type forgeHead struct {
	stamp  string
	pushed git.Pushed
	ok     bool
	at     time.Time
}

// pushed answers where HEAD's branch of the repository in dir was last
// pushed, false for a detached HEAD or a branch git refuses. A git that did
// not answer tells nothing, the answer before it stands, and without one it
// answers git's error.
func (h *forgeHeads) pushed(ctx context.Context, dir string) (git.Pushed, bool, error) {
	stamp := gitfacts.RefStamp(dir, forgeRemote)
	now := time.Now()
	h.mu.Lock()
	known, seen := h.heads[dir]
	h.mu.Unlock()
	if seen && known.stamp == stamp && now.Sub(known.at) < forgeHeadsAge {
		return known.pushed, known.ok, nil
	}
	pushed, err := git.New(dir).PushedHead(ctx, forgeRemote, "")
	if errors.Is(err, git.ErrNoAnswer) {
		if !seen {
			return git.Pushed{}, false, err
		}
		return known.pushed, known.ok, nil
	}
	h.mu.Lock()
	h.heads[dir] = forgeHead{stamp: stamp, pushed: pushed, ok: err == nil, at: now}
	h.mu.Unlock()
	return pushed, err == nil, nil
}

// only forgets the repositories outside dirs.
func (h *forgeHeads) only(dirs map[string]bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for dir := range h.heads {
		if !dirs[dir] {
			delete(h.heads, dir)
		}
	}
}

// forgeOwner names the project whose override binds p: its own, a worktree's
// main project's, and none for a worktree whose main repository is no
// project here, which binds by its origin alone.
func forgeOwner(p project.Project) string {
	if p.GitWorktree {
		return p.GitWorktreeOf
	}
	return p.Name
}

// forgetForge takes the override of a project that is gone away.
func (s *Server) forgetForge(name string) {
	if s.forge != nil {
		s.forge.Forget(name)
	}
}
