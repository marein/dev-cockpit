package editorintelligence

import (
	"context"
	"log"
	"sort"
	"sync"
	"time"
)

type connKey struct {
	project string
	profile string
}

// inflightToken identifies one in flight call per client and document, so a
// client's newer request cancels its older one and never somebody else's.
type inflightToken struct {
	cancel context.CancelFunc
}

// managedConn is a connection slot. It enters the table before the process
// starts, so the limits count starting connections and concurrent requests
// for the same key wait for one handshake instead of racing a second
// process. root and launcher are what the slot was started with: a project
// recreated under the same name, or a language whose way to run changed in
// the settings, gets a fresh server instead of the old one's answers.
type managedConn struct {
	ready    chan struct{}
	conn     *lspConn
	err      error
	cancel   context.CancelFunc
	root     string
	launcher Launcher

	// lastUsed, retired and holds are guarded by Service.mu. retired marks
	// a deliberate close (shutdown, project delete, idle expiry, eviction):
	// watchRestart never resurrects a retired slot. holds counts the callers
	// connFor handed the slot to that have not registered their call yet,
	// so an eviction between the two cannot close it under them.
	lastUsed time.Time
	retired  bool
	holds    int

	inflightMu sync.Mutex
	inflight   map[string]*inflightToken
}

// beginCall cancels the client's previous in flight call of the same method
// for the document and registers the new one; another client's call is left
// alone, the connection is shared. The method keeps a class name lookup that
// follows the pointer from cancelling a jump that waits for the index.
func (mc *managedConn) beginCall(ctx context.Context, client, path, method string) (context.Context, *inflightToken) {
	callCtx, cancel := context.WithCancel(ctx)
	token := &inflightToken{cancel: cancel}
	key := client + "\x00" + path + "\x00" + method
	mc.inflightMu.Lock()
	if previous := mc.inflight[key]; previous != nil {
		previous.cancel()
	}
	mc.inflight[key] = token
	mc.inflightMu.Unlock()
	return callCtx, token
}

func (mc *managedConn) endCall(client, path, method string, token *inflightToken) {
	key := client + "\x00" + path + "\x00" + method
	mc.inflightMu.Lock()
	if mc.inflight[key] == token {
		delete(mc.inflight, key)
	}
	mc.inflightMu.Unlock()
	token.cancel()
}

// connFor returns the live connection of the project and profile, starting
// one when needed. A non empty status tells the caller why no connection is
// available. A returned connection is held, see managedConn.holds: the
// caller registers its call and then calls release, so no eviction can
// close the connection between the two. used is the moment the slot's idle
// clock is set to: a lookup passes now, a restart the old clock it carries
// over. It only ever moves a running slot's clock forward.
func (s *Service) connFor(ctx context.Context, project, root string, profile *Profile, launcher Launcher, used time.Time) (*managedConn, string) {
	if launcher == nil {
		launcher = DockerLauncher(s.cacheRoot, s.dockerHost)
	}
	key := connKey{project: project, profile: profile.ID}
	// The setting is a file read and the detection a probe: both are asked
	// before the lock, never under it. A server that is not installed is
	// answered before anything is evicted for it.
	limit := s.currentMaxProjects()
	found := launcher.Detect(profile).Found
	s.mu.Lock()
	sweepDone := s.sweepDone
	// A server that asked for a restart keeps its project's place: its
	// replacement is watchRestart's, and the lookup starting it early
	// takes that place over instead of competing for a new one.
	admitted := false
	if mc, ok := s.conns[key]; ok {
		// A slot whose root or launcher no longer matches belongs to a
		// project recreated under its name, or to a server whose way to
		// run the settings moved: either way its answers describe
		// something that is gone.
		dead := s.slotDead(mc)
		if !dead && mc.root == root && mc.launcher.ID() == launcher.ID() {
			if used.After(mc.lastUsed) {
				mc.lastUsed = used
			}
			mc.holds++
			s.mu.Unlock()
			return s.awaitConn(ctx, key, mc)
		}
		admitted = dead && s.restartPending(mc)
		delete(s.conns, key)
		go s.closeManaged(mc)
	}
	// The limit counts projects. A project that already runs a server is
	// admitted, its other languages start without asking; a lowered limit
	// only takes other projects down to it. A new project needs a free
	// place: the least recently used projects with nothing in flight make
	// room, and busy is the answer only when too few of them idle.
	if !found {
		s.mu.Unlock()
		return nil, StatusNotInstalled
	}
	admitted = admitted || s.projectAdmitted(project)
	if !admitted {
		limit--
	}
	evicted, ok := s.evictProjects(project, limit, !admitted)
	if !ok {
		s.mu.Unlock()
		s.closeEvicted(evicted)
		return nil, StatusBusy
	}
	mc := &managedConn{
		ready:    make(chan struct{}),
		root:     root,
		launcher: launcher,
		lastUsed: used,
		holds:    1,
		inflight: map[string]*inflightToken{},
	}
	s.conns[key] = mc
	s.mu.Unlock()
	s.closeEvicted(evicted)
	if status := s.launch(ctx, key, mc, profile, sweepDone); status != "" {
		s.release(mc)
		return nil, status
	}
	return mc, ""
}

// launch starts the server of a slot that already stands in the table,
// starting, and closes its ready channel either way. A failed start takes
// the slot out again. A non empty status says why no server runs.
func (s *Service) launch(ctx context.Context, key connKey, mc *managedConn, profile *Profile, sweepDone chan struct{}) string {
	project, root, launcher := key.project, mc.root, mc.launcher
	// The slot shows as preparing from here on.
	s.notifyChange(project)

	// The boot sweep owns every container of the scheme until it finished:
	// starting under it would hand the sweep a fresh server to remove.
	select {
	case <-sweepDone:
	case <-ctx.Done():
		s.removeConn(key, mc)
		close(mc.ready)
		s.notifyChange(project)
		return StatusCanceled
	case <-s.prepCtx.Done():
		s.removeConn(key, mc)
		close(mc.ready)
		s.notifyChange(project)
		return StatusError
	}
	// Preparation is bounded by the service's preparation context, not the
	// lookup's: a canceled lookup must not abort an image build another one
	// waits on.
	if err := launcher.Prepare(s.prepCtx, s.projectsRoot, project, profile); err != nil {
		log.Printf("editor intelligence: %v", err)
		s.removeConn(key, mc)
		close(mc.ready)
		s.setBackoff(backoffKey(project, profile), lspErrorBackoff)
		s.notifyChange(project)
		return StatusError
	}
	argv := launcher.Argv(s.projectsRoot, project, root, profile)
	procCtx, cancel := context.WithCancel(s.ctx)
	conn, err := s.startConn(procCtx, profile, argv, launcher.ProcEnv(), root, launcher.InitOptions(project, profile), func() { s.notifyChange(project) })
	mc.conn = conn
	mc.err = err
	mc.cancel = cancel
	close(mc.ready)
	if err != nil {
		cancel()
		s.removeConn(key, mc)
		s.setBackoff(backoffKey(project, profile), lspErrorBackoff)
		log.Printf("editor intelligence: %v", err)
		s.notifyChange(project)
		return StatusError
	}
	s.wg.Add(1)
	go s.watchRestart(key, mc, profile)
	if !profile.SilentStart {
		s.wg.Add(1)
		go s.endSilentWindow(project, mc)
	}
	// The handshake ended: preparing hands over to the announced indexing.
	s.notifyChange(project)
	return ""
}

// release gives back the hold connFor handed out with a connection.
func (s *Service) release(mc *managedConn) {
	s.mu.Lock()
	mc.holds--
	s.mu.Unlock()
}

// endSilentWindow publishes the one moment a connection stops counting as
// indexing merely because its announcement had not arrived yet. Nothing
// else ever looks at that clock again: the indicator is event driven, so
// without this the bar of a server that announces late, or never announces
// at all, stands until some unrelated move of the picture happens to take
// it down, which for an idle project is never. One timer per connection,
// bounded by the warming window, and the death of the server is the other
// way out, which reports itself.
func (s *Service) endSilentWindow(project string, mc *managedConn) {
	defer s.wg.Done()
	left := s.warmupGrace - time.Since(mc.conn.startedAt)
	if left <= 0 {
		return
	}
	timer := time.NewTimer(left)
	defer timer.Stop()
	select {
	case <-s.ctx.Done():
	case <-mc.conn.exited:
	case <-timer.C:
		s.notifyChange(project)
	}
}

// watchRestart waits a running server out. A death whose exit code the
// launcher reads as a restart wish, the container's workspace watcher saw
// a relevant change, starts a fresh server for the same slot right away:
// no backoff, no error, the indicator simply shows the new indexing, and
// with the warm cache the restart is cheap. The replacement is swapped
// into the slot the old server still holds, so the project keeps its place
// among the running ones. The eager restart only happens while the project
// still sees editor action: an idle project's wish is honored lazily by the
// next editor open, or background churn alone would keep a container
// reindexing forever with no reader. Every other death stays what it was,
// an error the next lookup reports.
func (s *Service) watchRestart(key connKey, mc *managedConn, profile *Profile) {
	defer s.wg.Done()
	select {
	case <-s.ctx.Done():
		return
	case <-mc.conn.exited:
	}
	project, launcher := key.project, mc.launcher
	if !launcher.WantsRestart(mc.conn.exitStatus()) {
		return
	}
	// Both are settings or probes: asked before the lock, never under it.
	timeout := s.currentIdleTimeout()
	found := launcher.Detect(profile).Found
	s.mu.Lock()
	cur, held := s.conns[key]
	if (held && cur != mc) || mc.retired {
		// A newer replacement or a deliberate close ends the wish.
		s.mu.Unlock()
		return
	}
	// A restart is not editor action: the fresh slot keeps the old clock,
	// so the idle timeout keeps measuring from the last real use.
	lastUsed := mc.lastUsed
	idle := s.now().Sub(lastUsed) > timeout
	if !held || !found || idle {
		delete(s.conns, key)
		s.mu.Unlock()
		go s.closeManaged(mc)
		s.notifyChange(project)
		switch {
		case idle:
			// The next editor open starts it.
		case !found:
			log.Printf("editor intelligence: %s server for %s asked for a restart, but it is not installed any more", profile.ID, project)
		default:
			s.restartOutsideSlot(project, mc.root, profile, launcher, lastUsed)
		}
		return
	}
	next := &managedConn{
		ready:    make(chan struct{}),
		root:     mc.root,
		launcher: launcher,
		lastUsed: lastUsed,
		inflight: map[string]*inflightToken{},
	}
	s.conns[key] = next
	sweepDone := s.sweepDone
	s.mu.Unlock()
	go s.closeManaged(mc)
	log.Printf("editor intelligence: %s server for %s asked for a restart, reindexing", profile.ID, project)
	if status := s.launch(s.ctx, key, next, profile, sweepDone); status != "" {
		log.Printf("editor intelligence: %s server for %s did not come back after its restart: %s", profile.ID, project, status)
	}
}

// restartOutsideSlot is the restart of a server whose slot somebody took out
// of the table before watchRestart saw the wish, a lookup that met the dead
// server first. Without the slot the project has to be admitted again like
// any other, and a refusal is said, not swallowed. Like the restart inside
// the slot it is no editor action: a fresh slot starts on the old clock, and
// a replacement a lookup started first keeps that lookup's newer one.
func (s *Service) restartOutsideSlot(project, root string, profile *Profile, launcher Launcher, lastUsed time.Time) {
	log.Printf("editor intelligence: %s server for %s asked for a restart, reindexing", profile.ID, project)
	mc, status := s.connFor(s.ctx, project, root, profile, launcher, lastUsed)
	if status != "" {
		log.Printf("editor intelligence: %s server for %s was not restarted: %s, the next lookup starts it", profile.ID, project, status)
		return
	}
	s.release(mc)
}

// inUse reports whether a lookup holds the slot or has a call registered on
// it. Caller holds s.mu.
func (s *Service) inUse(mc *managedConn) bool {
	if mc.holds > 0 {
		return true
	}
	mc.inflightMu.Lock()
	defer mc.inflightMu.Unlock()
	return len(mc.inflight) > 0
}

// projectAdmitted reports whether the project holds a live slot in the
// table, starting, running or restarting. Caller holds s.mu.
func (s *Service) projectAdmitted(project string) bool {
	for key, mc := range s.conns {
		if key.project == project && s.slotLive(mc) {
			return true
		}
	}
	return false
}

// evictProjects takes other projects out of the table until at most limit
// projects hold slots, the least recently used first. Dead slots go first
// and count for nobody: a crashed server is no running project, and its
// recent use must not protect it over a live one. A project is taken only
// as a whole and only while none of its slots is starting, restarting,
// held or has a call in flight; keep is never taken. With strict set the
// eviction happens only when it reaches the limit, so a refused admission
// takes down nothing but the dead. It answers the removed slots per
// project, for closeEvicted after the caller unlocked, and whether the
// limit was reached. Caller holds s.mu.
func (s *Service) evictProjects(keep string, limit int, strict bool) (map[string][]*managedConn, bool) {
	type candidate struct {
		project  string
		lastUsed time.Time
		busy     bool
	}
	evicted := map[string][]*managedConn{}
	byProject := map[string]*candidate{}
	for key, mc := range s.conns {
		dead := s.slotDead(mc)
		pending := dead && s.restartPending(mc)
		if dead && !pending {
			// Not retired: whether its death was a restart wish stays
			// watchRestart's call, like the janitor's own drop.
			delete(s.conns, key)
			evicted[key.project] = append(evicted[key.project], mc)
			continue
		}
		c := byProject[key.project]
		if c == nil {
			c = &candidate{project: key.project}
			byProject[key.project] = c
		}
		if mc.lastUsed.After(c.lastUsed) {
			c.lastUsed = mc.lastUsed
		}
		if pending {
			c.busy = true
			continue
		}
		select {
		case <-mc.ready:
		default:
			c.busy = true
			continue
		}
		if s.inUse(mc) {
			c.busy = true
		}
	}
	excess := len(byProject) - limit
	if excess <= 0 {
		return evicted, true
	}
	idle := make([]*candidate, 0, len(byProject))
	for _, c := range byProject {
		if c.project != keep && !c.busy {
			idle = append(idle, c)
		}
	}
	if strict && len(idle) < excess {
		return evicted, false
	}
	sort.Slice(idle, func(i, j int) bool { return idle[i].lastUsed.Before(idle[j].lastUsed) })
	if len(idle) > excess {
		idle = idle[:excess]
	}
	for _, c := range idle {
		for key, mc := range s.conns {
			if key.project == c.project {
				mc.retired = true
				delete(s.conns, key)
				evicted[c.project] = append(evicted[c.project], mc)
			}
		}
	}
	return evicted, len(idle) >= excess
}

// closeEvicted closes what evictProjects took out and reports each project.
// Caller does not hold s.mu.
func (s *Service) closeEvicted(evicted map[string][]*managedConn) {
	for project, conns := range evicted {
		for _, mc := range conns {
			go s.closeManaged(mc)
		}
		s.notifyChange(project)
	}
}

// enforceMaxProjects takes projects down to a lowered limit on a janitor
// round, the least recently used idle ones first. The most recently used
// project is never taken, whether or not it is busy right now: it is the one
// somebody works in. What cannot go yet waits for a later round.
func (s *Service) enforceMaxProjects() {
	limit := s.currentMaxProjects()
	s.mu.Lock()
	evicted, _ := s.evictProjects(s.mostRecentProject(), limit, false)
	s.mu.Unlock()
	s.closeEvicted(evicted)
}

// mostRecentProject answers the project of the most recently used live
// slot, empty when there is none. Caller holds s.mu.
func (s *Service) mostRecentProject() string {
	var project string
	var last time.Time
	for key, mc := range s.conns {
		if !s.slotLive(mc) {
			continue
		}
		if project == "" || mc.lastUsed.After(last) {
			project, last = key.project, mc.lastUsed
		}
	}
	return project
}

// slotDead reports whether a table entry is finished and unusable. Starting
// entries count as live so their slot stays reserved. Caller holds s.mu.
func (s *Service) slotDead(mc *managedConn) bool {
	select {
	case <-mc.ready:
		return mc.err != nil || mc.conn == nil || !mc.conn.alive()
	default:
		return false
	}
}

// slotLive reports whether an entry holds its project's place: starting,
// running, or dead with a restart pending. The server is asked once, so a
// death between two reads cannot make one entry both. Caller holds s.mu.
func (s *Service) slotLive(mc *managedConn) bool {
	return !s.slotDead(mc) || s.restartPending(mc)
}

// restartPending reports whether an entry slotDead already found dead is a
// restart watchRestart is about to swap in: its server ended with the
// launcher's restart wish, or has not been reaped yet, so the wish cannot
// be ruled out. Such an entry keeps its project's place. It never asks
// whether the server is alive: the caller read that once and passes only a
// dead entry. Caller holds s.mu.
func (s *Service) restartPending(mc *managedConn) bool {
	if mc.retired || mc.err != nil || mc.conn == nil {
		return false
	}
	select {
	case <-mc.conn.exited:
		return mc.launcher.WantsRestart(mc.conn.exitStatus())
	default:
		return true
	}
}

// awaitConn waits for a starting connection to finish its handshake. The
// slot comes held; a failed wait gives the hold back.
func (s *Service) awaitConn(ctx context.Context, key connKey, mc *managedConn) (*managedConn, string) {
	select {
	case <-ctx.Done():
		s.release(mc)
		return nil, StatusCanceled
	case <-mc.ready:
	}
	if mc.err != nil || mc.conn == nil || !mc.conn.alive() {
		s.release(mc)
		s.removeConn(key, mc)
		return nil, StatusError
	}
	return mc, ""
}

func (s *Service) removeConn(key connKey, mc *managedConn) {
	s.mu.Lock()
	if s.conns[key] == mc {
		delete(s.conns, key)
	}
	s.mu.Unlock()
}

func (s *Service) dropConn(project string, profile *Profile, mc *managedConn) {
	s.removeConn(connKey{project: project, profile: profile.ID}, mc)
	go s.closeManaged(mc)
	s.notifyChange(project)
}

func (s *Service) touch(mc *managedConn) {
	s.mu.Lock()
	mc.lastUsed = s.now()
	s.mu.Unlock()
}

func (s *Service) inBackoff(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	until, ok := s.backoff[key]
	return ok && s.now().Before(until)
}

func (s *Service) setBackoff(key string, d time.Duration) {
	s.mu.Lock()
	s.backoff[key] = s.now().Add(d)
	s.mu.Unlock()
}

// backoffKey scopes the error backoff to one project and profile: one
// project's broken server must not silence the language everywhere.
func backoffKey(project string, profile *Profile) string {
	return "lsp:" + project + "\x00" + profile.ID
}

// exitWasRestartWish reports whether the dead connection ended with the
// launcher's agreed restart code. The exit code lands moments after the
// pipes close, so a short bounded wait covers the gap between the failed
// call and the reaped process.
func (s *Service) exitWasRestartWish(mc *managedConn) bool {
	select {
	case <-mc.conn.exited:
	case <-time.After(2 * time.Second):
		return false
	}
	return mc.launcher.WantsRestart(mc.conn.exitStatus())
}
