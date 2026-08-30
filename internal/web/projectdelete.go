package web

import (
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"sync"
	"time"

	"github.com/marein/dev-cockpit/internal/docker"
	"github.com/marein/dev-cockpit/internal/notify"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/statefile"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// composeIdlePoll is how often a wait on a running compose run looks again.
const composeIdlePoll = 250 * time.Millisecond

// composeIdleFallback bounds a wait whose directory reads busy while the
// register names no run there, a state that should not exist; it keeps a
// confused claim from holding a deletion forever. The ordinary bound is the
// run's own, see waitComposeIdle.
const composeIdleFallback = 2 * time.Minute

// resumeDockerWait is how long a resumed deletion waits for the daemon
// connection of a server that has just come up. Long enough for a socket that
// is there, short enough not to hold a deletion on a host that has no docker
// at all.
const resumeDockerWait = 15 * time.Second

// projectDeleteEntry is one running deletion as it stands on disk: where the
// project is, so it can be taken up again without the project having to still
// be listable. The file carries the deletions that are going on and nothing
// else, never history: an entry is an intent, and every one of them is taken up
// again unconditionally at the next start.
type projectDeleteEntry struct {
	Path string `json:"path"`
}

// projectDeletes are the deletions under way, plus the reason a finished one
// failed. A deletion that brings compose stacks down first outlives its
// request, so the row has to say so on every render until the project is gone,
// and a failure has to keep standing there: the request that would have
// flashed it is long answered. Every deletion enters itself here before the
// first directory goes, the in-request ones included: for those the entry is
// the crash marker alone, spent within the request, and their failure travels
// inline as the flash it always was.
//
// The intent outlives the process too, through one flat state file. The
// goroutine doing the work does not: it dies with the server, and what is left
// is the entry, which is what the next process reads to put the row back and to
// take the deletion up again. That is deliberately all there is, no lock file
// and no held process like a compose run has: nothing here is expensive to do
// twice, so remembering the intent is enough.
//
// The failure does not outlive it, and that is the same decision from the other
// side: it is not an intent, it is what one attempt ran into. It stands on the
// row for as long as this server does and goes with it, so a name never carries
// an old error into a project that is created under it later.
type projectDeletes struct {
	path    string
	mu      sync.Mutex
	running map[string]bool
	failed  map[string]string
}

// newProjectDeletes reads the running deletions before anything can ask about
// them, so a row whose deletion the last process was in the middle of never
// renders as an ordinary project with its buttons back. Taking the work up
// again comes later and may take its time, see ResumeProjectDeletes.
func newProjectDeletes(stateDir string) *projectDeletes {
	d := &projectDeletes{
		path:    filepath.Join(stateDir, "project-deletes.json"),
		running: map[string]bool{},
		failed:  map[string]string{},
	}
	for name := range d.load() {
		d.running[name] = true
	}
	return d
}

// start claims a project's deletion and reports whether the caller got it. A
// second request while one runs changes nothing. A retry clears what the
// previous attempt failed with.
func (d *projectDeletes) start(name, path string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.running[name] {
		return false
	}
	d.running[name] = true
	delete(d.failed, name)
	d.write(name, projectDeleteEntry{Path: path})
	return true
}

// finish releases the claim, and the entry goes either way: the deletion is not
// going on any more, and that is the only thing the file says. A failure stays
// behind in this process alone, where the row reads it until somebody asks
// again or this server ends.
func (d *projectDeletes) finish(name, failure string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.running, name)
	if failure == "" {
		delete(d.failed, name)
	} else {
		d.failed[name] = failure
	}
	d.forget(name)
}

// pending answers the deletions that were going on when the last process
// ended, keyed by project name with the path they were removing.
func (d *projectDeletes) pending() map[string]string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := map[string]string{}
	for name, entry := range d.load() {
		out[name] = entry.Path
	}
	return out
}

// load, write and forget are the file. It is read through on every call like
// every other state file, so the entry a dying process wrote is the entry the
// next one finds.
func (d *projectDeletes) load() map[string]projectDeleteEntry {
	out := map[string]projectDeleteEntry{}
	statefile.Load(d.path, &out)
	return out
}

func (d *projectDeletes) write(name string, entry projectDeleteEntry) {
	list := d.load()
	list[name] = entry
	statefile.Save(d.path, 0o600, list)
}

func (d *projectDeletes) forget(name string) {
	list := d.load()
	if _, ok := list[name]; !ok {
		return
	}
	delete(list, name)
	statefile.Save(d.path, 0o600, list)
}

// view answers what the list renders per project, nil while nothing is going
// on, which is the ordinary case.
func (d *projectDeletes) view(projects []project.Project) map[string]render.ProjectDelete {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.running) == 0 && len(d.failed) == 0 {
		return nil
	}
	out := map[string]render.ProjectDelete{}
	for i := range projects {
		name := projects[i].Name
		if d.running[name] || d.failed[name] != "" {
			out[name] = render.ProjectDelete{Running: d.running[name], Failed: d.failed[name]}
		}
	}
	return out
}

// composeStacksToStop answers the compose directories of a project a deletion
// has to bring down before the directory goes. Without a reachable daemon and
// without the CLI compose needs it answers none, which is what makes a host
// with no docker delete exactly the way it always did.
func (s *Server) composeStacksToStop(path string) []docker.Stack {
	if !s.docker.CLI() {
		return nil
	}
	state := s.docker.State()
	if !state.Available {
		return nil
	}
	return stacksToStop(state, path)
}

// stacksToStop is that choice itself: a stack the daemon shows containers for,
// running or stopped, because both outlive the directory and both are what
// compose down clears. A directory that only carries a compose file has
// nothing to bring down. The stack carries its compose project name, which the
// down has to name explicitly, see composeDown.
func stacksToStop(state docker.State, path string) []docker.Stack {
	var stacks []docker.Stack
	for _, stack := range state.StacksForDir(path) {
		if stack.Total > 0 {
			stacks = append(stacks, stack)
		}
	}
	return stacks
}

// deleteProjectWithCompose is the deletion of a project that runs containers,
// off the request: the runners go first, then every stack is brought down and
// waited for, and the directory goes last, because compose reads its file out
// of it. The row says it is working the whole time and disappears when the
// projects event lands.
//
// Every step of it can be done twice, which is what lets a deletion the last
// process was in the middle of simply run again from the top: the purge finds
// nothing left to stop, the stacks are asked of the daemon here and now rather
// than handed in (the ones already down are not among them any more), a down
// somebody else is still running is waited out, and removing a directory that
// is a torso or gone at all is what removing it means.
func (s *Server) deleteProjectWithCompose(p project.Project) {
	s.purgeProjectRunners(p.Path)
	s.publishTerminals("") // the purge removed this project's coders and shells everywhere
	for _, stack := range s.composeStacksToStop(p.Path) {
		if err := s.composeDown(stack, p); err != nil {
			s.abortProjectDelete(p, err)
			return
		}
	}
	// Nothing may still be running compose in there when the directory goes.
	// The stacks above are the ones the daemon shows containers for, and a run
	// is past that point while it still removes the network: after a restart
	// that run belongs to nobody here, it was only adopted, and pulling its
	// directory away mid-run is the one thing this deletion must not do.
	if !s.waitComposeIdle(p.Path, func() bool { return s.docker.ComposeBusyUnder(p.Path) }) {
		s.abortProjectDelete(p, errors.New("A compose run in the project would not end, nothing was removed."))
		return
	}
	s.closeProjectLSP(p.Name)
	failure := ""
	if err := s.projects.Remove(p); err != nil {
		log.Printf("delete project %s: %v", p.Name, err)
		failure = err.Error()
	} else {
		s.commitDrafts.Delete(p.Name)
		s.searchDrafts.Delete(p.Name)
		s.lineComments.Clear(p.Name)
		s.quickOpen.Forget(p.Path)
		// The project's compose and askpass news read themselves with it, and
		// deliberately only on success: an aborted deletion keeps its compose
		// failure notification unread, that is the one word about why nothing
		// was removed.
		s.notifier.MarkTargetRead(notify.DockerTarget(p.Name))
		s.notifier.MarkTargetRead(notify.GitPromptTarget(p.Name))
	}
	s.deletes.finish(p.Name, failure)
	s.publishProjects()
}

// claimDeleteCascade enters a deletion in the deletes state before any work
// starts: the main and every worktree project registered in its repository,
// the cascade the delete confirm announced. The entry is the crash marker, a
// server that dies mid-deletion takes every claimed project up again at the
// next start, and the one publish here is what puts the working rows on every
// open list before the first directory goes. ok is false when the main is
// already being deleted, nothing was claimed then. names is what the answer
// reports as going: it includes a worktree project some other request is
// already deleting, that one is just not this request's to work on and is not
// among the claimed children.
func (s *Server) claimDeleteCascade(p project.Project) (children []project.Project, names []string, ok bool) {
	if !s.deletes.start(p.Name, p.Path) {
		return nil, nil, false
	}
	for _, wt := range p.GitWorktrees {
		if wt.Project == "" || wt.Project == p.Name {
			continue
		}
		child, err := s.projects.FindByName(wt.Project)
		if err != nil {
			continue
		}
		names = append(names, child.Name)
		if s.deletes.start(child.Name, child.Path) {
			children = append(children, child)
		}
	}
	s.publishProjects()
	return children, names, true
}

// runDeleteCascade works the claimed worktree projects off: one with compose
// involved goes off the request in a goroutine of its own, which finishes its
// entry itself, the rest go in place. A child that cannot be removed ends the
// cascade, the main is more use alive than gone under a worktree that would
// not go; the remaining claims are released then, the main's too, because no
// work stands behind them any more, and the failure travels inline like every
// sync delete error, so the entries carry none.
func (s *Server) runDeleteCascade(p project.Project, children []project.Project) error {
	for i, child := range children {
		if len(s.composeStacksToStop(child.Path)) > 0 || s.docker.ComposeBusyUnder(child.Path) {
			go s.deleteProjectWithCompose(child)
			continue
		}
		if err := s.removeProjectNow(child); err != nil {
			s.deletes.finish(child.Name, "")
			for _, rest := range children[i+1:] {
				s.deletes.finish(rest.Name, "")
			}
			s.deletes.finish(p.Name, "")
			return fmt.Errorf("Deleting worktree project %q: %v", child.Name, err)
		}
		s.deletes.finish(child.Name, "")
	}
	return nil
}

// removeProjectNow is the in-request deletion: runners purged, language
// servers closed, the directory removed, per project state cleared.
// Publishing stays with the caller.
func (s *Server) removeProjectNow(p project.Project) error {
	s.purgeProjectRunners(p.Path)
	s.closeProjectLSP(p.Name)
	if err := s.projects.Remove(p); err != nil {
		return err
	}
	s.commitDrafts.Delete(p.Name)
	s.searchDrafts.Delete(p.Name)
	s.lineComments.Clear(p.Name)
	s.quickOpen.Forget(p.Path)
	return nil
}

// abortProjectDelete ends a deletion that could not bring the containers down.
// The directory stays: removing it over a stack that is still up would leave
// containers behind with no compose file left to reach them from. The reason
// stands on the row, and the retry the row then offers starts over from the
// top, which every step of the deletion is built for.
func (s *Server) abortProjectDelete(p project.Project, err error) {
	log.Printf("delete project %s: %v", p.Name, err)
	s.deletes.finish(p.Name, err.Error())
	s.publishProjects()
}

// ResumeProjectDeletes takes up the deletions the last process was in the
// middle of. Their rows already say they are working, that was read before this
// server answered anything; this is the work behind them starting again.
//
// It waits for the docker connection first: the deletion's whole point is that
// the containers go before the directory does, and a cache that has not
// answered yet would look exactly like a host without docker. A host that
// really has none is not waited out for long. An in-request deletion the last
// process died over resumes the same road and simply finds nothing to bring
// down.
func (s *Server) ResumeProjectDeletes() {
	pending := s.deletes.pending()
	if len(pending) == 0 {
		return
	}
	s.waitForDocker()
	for name, path := range pending {
		p, err := s.projects.FindByName(name)
		if err != nil {
			// The directory is gone already, the last process got that far.
			// Nothing is left to take up, and the row goes with the entry.
			log.Printf("resuming the deletion of project %s: nothing left at %s", name, path)
			s.deletes.finish(name, "")
			s.publishProjects()
			continue
		}
		log.Printf("resuming the deletion of project %s", name)
		go s.deleteProjectWithCompose(p)
	}
}

// waitForDocker gives the daemon connection a moment to come up, the one thing
// a resumed deletion has to have before it decides there is nothing to bring
// down. A cockpit without the docker CLI has nothing to wait for.
func (s *Server) waitForDocker() {
	if !s.docker.CLI() {
		return
	}
	deadline := time.Now().Add(resumeDockerWait)
	for !s.docker.State().Available && time.Now().Before(deadline) {
		time.Sleep(composeIdlePoll)
	}
}

// composeDown runs one down and waits for it, the one place that turns the
// background run into a step. A run somebody else started in the same
// directory is waited out first, and a down that could not run or that failed
// comes back as an error that ends the deletion: a deletion that worked past
// its down would leave the containers behind with no directory left to reach
// them from. The run is quiet, only a failure has anything to say, and it is
// the docker service that says it, here and after a restart alike (see
// composeDone).
//
// This one command is not the configured list, and deliberately so: a deletion
// takes everything with it, so it is the fixed down with volumes, and it names
// the compose project the daemon reports for these containers. Without that
// name compose derives one from the directory it stands in, which is a
// normalised folder name: a stack started under any other name would be left
// running while the run reports success.
//
// The wait is this process's. A restart during it ends the deletion, not the
// down: the run keeps going, reports when it is over, and the project is still
// there to be deleted again.
func (s *Server) composeDown(stack docker.Stack, p project.Project) error {
	name := stack.Label
	if name == "" {
		name = p.Name
	}
	if !s.waitComposeIdle(stack.Dir, func() bool { return s.docker.ComposeBusy(stack.Dir) }) {
		return fmt.Errorf("A compose run in %q would not end, the containers were not brought down.", name)
	}
	id, err := s.docker.RunCompose(docker.ComposeOptions{
		Dir:    stack.Dir,
		Root:   p.Path,
		Label:  p.Name,
		Action: docker.PurgeAction(stack.Project),
		Quiet:  true,
	})
	if err != nil {
		return fmt.Errorf("Compose down in %q did not start: %s.", name, err)
	}
	s.docker.AwaitCompose(id)
	if run, ok := s.docker.ComposeRunByID(id); ok && run.Failure != "" {
		return fmt.Errorf("Compose down in %q failed: %s.", name, run.Failure)
	}
	return nil
}

// waitComposeIdle waits the compose runs busy reports out and reports whether
// dir is idle now. The bound is the runs' own: every run carries a timeout the
// hold process enforces, so its start plus that timeout plus a grace is the
// latest it can end (docker.ComposeDeadline), and an up that legitimately
// pulls for twenty minutes is waited for. Only a run still going past its own
// deadline, a stuck one, is given up on, and the caller then refuses instead
// of working over it.
func (s *Server) waitComposeIdle(dir string, busy func() bool) bool {
	fallback := time.Now().Add(composeIdleFallback)
	for busy() {
		deadline, ok := s.docker.ComposeDeadline(dir)
		if !ok {
			deadline = fallback
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(composeIdlePoll)
	}
	return true
}
