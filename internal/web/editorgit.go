package web

import (
	"context"
	"sync"
	"time"

	"github.com/marein/dev-cockpit/internal/git"
	"github.com/marein/dev-cockpit/internal/project"
)

// gitWatchWindow is how long one client's watch keeps a project's poller alive.
// An open editor renews it well inside the window, a closed one lets it lapse,
// and nothing polls a project nobody is looking at.
const gitWatchWindow = 45 * time.Second

// gitWatchers is who is currently watching which project, plus which projects
// already have a poller running. One poller per project serves every client of
// it, so ten open editor tabs are still one git call per interval.
type gitWatchers struct {
	mu      sync.Mutex
	until   map[string]time.Time
	running map[string]bool
}

func newGitWatchers() *gitWatchers {
	return &gitWatchers{until: map[string]time.Time{}, running: map[string]bool{}}
}

// watch records interest in a project and reports whether a poller has to be
// started for it.
func (g *gitWatchers) watch(projectName string, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.until[projectName] = now.Add(gitWatchWindow)
	if g.running[projectName] {
		return false
	}
	g.running[projectName] = true
	return true
}

// watched reports whether the window is still open. A lapsed window is cleared
// here, so the poller that reads it can return and the next watch starts a
// fresh one.
func (g *gitWatchers) watched(projectName string, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if now.Before(g.until[projectName]) {
		return true
	}
	delete(g.until, projectName)
	delete(g.running, projectName)
	return false
}

// release drops a project's poller without waiting for its window to lapse. It
// is what a poller calls when it stops on its own, so the next watch can start
// one again.
func (g *gitWatchers) release(projectName string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.until, projectName)
	delete(g.running, projectName)
}

// gitWrites is which working copies are being written right now. The editor's
// own lock lives in one page and can only speak for that page, while a working
// copy has as many pages on it as somebody opened: a phone and a desktop, two
// tabs, the same project in a second window. Two writes that pass each other
// there are not two failures, they are one commit that records half of the old
// branch, because git serializes the index and not the files on disk.
//
// It is a try, never a wait: a request that queued behind a checkout would sit
// there for minutes with nothing to show, so the second one is refused in the
// same words the bridge uses and the person tries again when the first is
// through. The quiet fetch is the exception, it asks and simply does nothing.
//
// One write holds more than one name for what it is writing (`gitWriteKeys`),
// and they are taken together or not at all. The working copy is the name two
// projects in one checkout meet under, a project below the repository root
// included. The project path is the name that exists before any repository
// does: a clone starts in an empty directory, and git creates the `.git` there
// within the first moments, so a working copy name taken at the start and one
// resolved a second later are two different answers for one running write.
// Holding the project path across the whole clone is what keeps the second
// write out for as long as the clone runs.
type gitWrites struct {
	mu   sync.Mutex
	held map[string]bool
}

func newGitWrites() *gitWrites {
	return &gitWrites{held: map[string]bool{}}
}

// try takes every name of one write at once and reports whether it got them.
// All or nothing: a write that took one name and found the second one held
// would have to give the first back, and between those two moments it is a
// lock that lets somebody else in.
func (g *gitWrites) try(keys ...string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, key := range keys {
		if g.held[key] {
			return false
		}
	}
	for _, key := range keys {
		g.held[key] = true
	}
	return true
}

func (g *gitWrites) release(keys ...string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, key := range keys {
		delete(g.held, key)
	}
}

// watchProjectGit takes a client's watch and starts the project's poller when
// it is the one that opened the window. Polling turned off starts nothing; the
// answer says so and the client stops renewing.
func (s *Server) watchProjectGit(p project.Project, set editorSettings) bool {
	if set.GitPollSeconds <= 0 {
		return false
	}
	if s.gitWatchers.watch(p.Name, time.Now()) {
		go s.pollProjectGit(p)
	}
	return true
}

// pollProjectGit watches one project for as long as somebody is looking at it.
// It compares fingerprints and publishes only when one moves, so an idle
// repository costs the browsers nothing. The event is a bare signal naming the
// project, like the terminals event: every client pulls the status itself, with
// its own session and its own path. It carries one flag on top of that, whether
// the base moved, because that is the one case in which an open diff has to
// fetch its revision again; a save in the working copy must not cost that.
//
// The interval is read again before every round instead of being cast into a
// ticker once: a changed setting has to reach a poller that is already running,
// without waiting for the last watcher to close the page. Zero stops the poller
// here as well, and the next watch answers that there is nothing to renew.
func (s *Server) pollProjectGit(p project.Project) {
	repo := git.New(p.Path)
	// The opening read is a round like any other, so whether it could ask git
	// counts here too: its zero value is indistinguishable from a directory
	// that is no repository, and taking it as the baseline would make the
	// first healthy round report a move that never happened, base included.
	// Until one round has answered, there is nothing to compare against.
	last, haveBase := repo.Fingerprint(context.Background())
	for {
		seconds := s.editorSettings().GitPollSeconds
		if seconds <= 0 {
			s.gitWatchers.release(p.Name)
			return
		}
		time.Sleep(time.Duration(seconds) * time.Second)
		if !s.gitWatchers.watched(p.Name, time.Now()) {
			return
		}
		fingerprint, ok := repo.Fingerprint(context.Background())
		// A round that could not ask git knows nothing, so it keeps the last
		// answer and waits: publishing on it would send every open editor after
		// a status that did not move, and again when the next round recovers.
		if !ok {
			continue
		}
		if !haveBase {
			last, haveBase = fingerprint, true
			continue
		}
		if !fingerprint.Moved(last) {
			continue
		}
		baseMoved := fingerprint.Base != last.Base
		last = fingerprint
		s.publishGit(p.Name, baseMoved)
	}
}
