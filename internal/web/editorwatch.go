package web

import (
	"sort"
	"sync"
	"time"

	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/project"
)

// fileWatchWindow is how long one client's watch keeps a project's file tick
// alive, the same window the git poller runs on: an open editor renews it well
// inside, a closed one lets it lapse, and nothing is stat'ed for a project
// nobody is looking at.
const fileWatchWindow = 45 * time.Second

// The caps on one client's scope. Twenty tabs and thirty unfolded folders is
// what a wide screen holds, so these are far above the real thing and are here
// for the case the numbers never come from a screen at all.
const (
	maxWatchedFiles = 250
	maxWatchedDirs  = 250
)

// watchedPath is one path a client says it is showing, with the token it holds
// for it: a file's version out of the read, a folder's listing signature out of
// the listing. The token is what lets the first round answer "the disk is not
// what you are showing" instead of "nothing moved since I started looking",
// which is the difference between following the disk and following it from the
// next change onwards. A client that has no token for a path sends none, and
// then the first round is a baseline like any other.
type watchedPath struct {
	Path  string `json:"path"`
	Token string `json:"token"`
}

// fileWatchers is who is watching what, and what the disk last said about it.
// The scope is the client's to say, because it is exactly what is on that
// screen: the open tabs and the unfolded folders. The server holds the union
// over every client of a project, so two browsers on one project are one tick
// over both their screens and never two, and refcounts it the way gitWatchers
// refcounts the git poller: the tick runs while somebody renews, and the round
// after the last window lapsed is the round that ends it.
//
// The stamps live here and not in the tick, because a watch has to be able to
// seed them: the path a client just opened is stamped from what that client
// read, at the moment it says so, and not whenever the tick next comes round.
type fileWatchers struct {
	mu       sync.Mutex
	projects map[string]*fileWatchProject
}

type fileWatchProject struct {
	clients map[string]fileWatchClient
	stamps  map[string]filesystem.Stamp
	running bool
}

// fileWatchClient is one screen: what it shows and how long that still counts.
type fileWatchClient struct {
	files []string
	dirs  []string
	until time.Time
}

func newFileWatchers() *fileWatchers {
	return &fileWatchers{projects: map[string]*fileWatchProject{}}
}

// fileKey and dirKey keep the two kinds of path apart in the one stamp map: the
// same name can be a file today and a folder tomorrow, and those are two
// different things to watch.
func fileKey(rel string) string { return "f:" + rel }
func dirKey(rel string) string  { return "d:" + rel }

// watch records what one client is showing and reports whether a tick has to be
// started for the project. A client that renews replaces its own scope entirely
// and never merges with what it said before: a closed tab has to leave the
// scope, and the only report of that is the next scope this client sends.
//
// Every path this client names that the project has no stamp for yet is seeded
// from the token the client sent. That is the whole reason the tokens travel:
// the client read that file or listed that folder a moment ago, so its token is
// the honest starting point, while a stamp taken by the tick's next round would
// already carry whatever happened in between and would hide it forever.
func (f *fileWatchers) watch(projectName, clientID string, files, dirs []watchedPath, now time.Time) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	entry := f.projects[projectName]
	if entry == nil {
		entry = &fileWatchProject{
			clients: map[string]fileWatchClient{},
			stamps:  map[string]filesystem.Stamp{},
		}
		f.projects[projectName] = entry
	}
	filePaths := capPaths(files, maxWatchedFiles)
	dirPaths := capPaths(dirs, maxWatchedDirs)
	entry.seed(files, fileKey)
	entry.seed(dirs, dirKey)
	entry.clients[clientID] = fileWatchClient{files: filePaths, dirs: dirPaths, until: now.Add(fileWatchWindow)}
	if entry.running {
		return false
	}
	entry.running = true
	return true
}

// seed writes a stamp for every named path the project holds none for. A path
// that is already stamped keeps what it has: the tick has been watching it, and
// what the tick knows is newer than what a client says about it.
func (p *fileWatchProject) seed(paths []watchedPath, key func(string) string) {
	for _, path := range paths {
		if path.Token == "" {
			continue
		}
		if _, held := p.stamps[key(path.Path)]; held {
			continue
		}
		p.stamps[key(path.Path)] = filesystem.SeedStamp(path.Token)
	}
}

// scope answers the union of every client whose window is still open, files and
// directories apart, and drops the stamps of everything that has left it. A
// lapsed client is dropped here, and a project without a client left is dropped
// with it: ok false is what ends the tick, so the next watch starts a fresh one.
// Forgetting the stamps of what left is what makes a tab reopened later start
// from a fresh baseline instead of a stale one.
func (f *fileWatchers) scope(projectName string, now time.Time) (files, dirs []string, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	entry := f.projects[projectName]
	if entry == nil {
		return nil, nil, false
	}
	fileSet := map[string]bool{}
	dirSet := map[string]bool{}
	for id, client := range entry.clients {
		if !now.Before(client.until) {
			delete(entry.clients, id)
			continue
		}
		for _, p := range client.files {
			fileSet[p] = true
		}
		for _, p := range client.dirs {
			dirSet[p] = true
		}
	}
	if len(entry.clients) == 0 {
		delete(f.projects, projectName)
		return nil, nil, false
	}
	for key := range entry.stamps {
		rel := key[2:]
		if (key[0] == 'f' && !fileSet[rel]) || (key[0] == 'd' && !dirSet[rel]) {
			delete(entry.stamps, key)
		}
	}
	return sortedKeys(fileSet), sortedKeys(dirSet), true
}

// stamps hands the tick a copy to compare against, and merge takes the fresh
// ones back. A copy rather than the map itself, because the probing in between
// reads the disk and holds no lock while it does.
func (f *fileWatchers) stamps(projectName string) map[string]filesystem.Stamp {
	f.mu.Lock()
	defer f.mu.Unlock()
	entry := f.projects[projectName]
	if entry == nil {
		return map[string]filesystem.Stamp{}
	}
	out := make(map[string]filesystem.Stamp, len(entry.stamps))
	for key, stamp := range entry.stamps {
		out[key] = stamp
	}
	return out
}

// merge writes back what one round probed, and only that: a seed that arrived
// while the round was running belongs to a path this round did not touch, and
// replacing the whole map would take it with it.
func (f *fileWatchers) merge(projectName string, next map[string]filesystem.Stamp) {
	f.mu.Lock()
	defer f.mu.Unlock()
	entry := f.projects[projectName]
	if entry == nil {
		return
	}
	for key, stamp := range next {
		entry.stamps[key] = stamp
	}
}

// release drops a project's tick without waiting for the windows to lapse. It
// is what the tick calls when it stops on its own, so the next watch can start
// one again.
func (f *fileWatchers) release(projectName string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.projects, projectName)
}

// capPaths takes what a client sent, drops what repeats and keeps the order it
// arrived in. The cap is not a correctness rule, it is a ceiling on what one
// request may make the tick do.
func capPaths(in []watchedPath, max int) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, path := range in {
		if seen[path.Path] || len(out) >= max {
			continue
		}
		seen[path.Path] = true
		out = append(out, path.Path)
	}
	return out
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// watchProjectFiles takes one client's scope and starts the project's tick when
// it is the one that opened the window. A poll turned off starts nothing; the
// answer says so and the client stops renewing.
func (s *Server) watchProjectFiles(p project.Project, clientID string, files, dirs []watchedPath, set editorSettings) bool {
	if set.FilePollSeconds <= 0 || clientID == "" {
		return false
	}
	if s.fileWatchers.watch(p.Name, clientID, files, dirs, time.Now()) {
		go s.pollProjectFiles(p)
	}
	return true
}

// pollProjectFiles is the tick, built like the git poller and answering a
// question the git poller cannot. A git status moves when a file is created,
// deleted or modified for the first time, and it stands still when an already
// modified file is written again, which is the everyday case here: a coder
// writes into the same file for an hour and the status line stays "M path" the
// whole time. Ignored files and projects without a repository never move it at
// all. So the disk is asked directly, but only about what is on a screen.
//
// One round is one stat per watched path. A file is read and a folder is listed
// only where its stat moved, and the token decides whether anything happened
// (filesystem.Stamp). What goes out is a signal in the house style, the project
// and the paths that moved, never any content: every client pulls the directory
// listing or the file itself with its own session, and answers it with what its
// own buffer is.
//
// The interval is read again before every round instead of being cast into a
// ticker once, so a changed setting reaches a running tick and zero ends it.
func (s *Server) pollProjectFiles(p project.Project) {
	for {
		seconds := s.editorSettings().FilePollSeconds
		if seconds <= 0 {
			s.fileWatchers.release(p.Name)
			return
		}
		time.Sleep(time.Duration(seconds) * time.Second)
		files, dirs, ok := s.fileWatchers.scope(p.Name, time.Now())
		if !ok {
			return
		}
		movedFiles, movedDirs := s.roundProjectFiles(p, files, dirs)
		if len(movedFiles) == 0 && len(movedDirs) == 0 {
			continue
		}
		// The quick open index knows paths and nothing about content, so only a
		// directory that moved can have made it wrong. More hangs on that index
		// than the file palette by now, the content search and the tree's own
		// filter choices among them, which is why it is dropped here instead of
		// being left to its staleness bound.
		if len(movedDirs) > 0 {
			s.quickOpen.Invalidate(p.Path)
		}
		s.publishFiles(p.Name, movedFiles, movedDirs)
	}
}

// roundProjectFiles probes one round's scope and answers what moved. A path the
// project has no stamp for at all is only recorded, never reported: nobody has
// said what they hold for it, so there is nothing to compare against and a
// first round without a baseline would tell everybody about a move that never
// happened. A path a watch seeded is not that case, it carries the token of
// what a client is showing, and a disk that says something else is a move.
func (s *Server) roundProjectFiles(p project.Project, files, dirs []string) (movedFiles, movedDirs []string) {
	last := s.fileWatchers.stamps(p.Name)
	next := make(map[string]filesystem.Stamp, len(files)+len(dirs))
	for _, rel := range files {
		key := fileKey(rel)
		held, known := last[key]
		stamp := filesystem.StampFile(p.Path, rel, held)
		next[key] = stamp
		if known && !stamp.Same(held) {
			movedFiles = append(movedFiles, rel)
		}
	}
	for _, rel := range dirs {
		key := dirKey(rel)
		held, known := last[key]
		stamp := filesystem.StampDir(p.Path, rel, held)
		next[key] = stamp
		if known && !stamp.Same(held) {
			movedDirs = append(movedDirs, rel)
		}
	}
	s.fileWatchers.merge(p.Name, next)
	return movedFiles, movedDirs
}
