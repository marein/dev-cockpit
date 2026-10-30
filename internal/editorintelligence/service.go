package editorintelligence

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Connection limits. A connection is one language server process per
// project and profile, shared by every editor instance of the project, so a
// page refresh reconnects to the warm index instead of building a new one.
const (
	// The limit counts projects, not connections: an admitted project runs
	// a server for every language it needs, so a low limit never leaves one
	// of its languages without one. The limit is a setting read live, see
	// SetMaxProjects, and DefaultMaxProjects stands in while none is wired
	// or stored.
	DefaultMaxProjects = 3
	// MinMaxProjects is the floor: the project somebody works in has to run.
	MinMaxProjects = 1
	// MaxMaxProjects bounds the setting; every project may run a server per
	// language, each with a full index in memory.
	MaxMaxProjects = 32
	// A connection lives until the project saw no editor action for the idle
	// timeout; every editor route of the project counts as action, see
	// Touch. The timeout is a setting read live, see SetIdleTimeout, and
	// DefaultIdleTimeout stands in while none is wired or stored. Short on
	// purpose: the warm cache makes a fresh start cheap.
	DefaultIdleTimeout = 5 * time.Minute
	// MinIdleTimeout is the floor: an open editor renews its watches every
	// 15 seconds, and a timeout near that would stop a server between two
	// renewals of a project somebody is working in.
	MinIdleTimeout = time.Minute
	// MaxIdleTimeout bounds the setting to a day, which is longer than any
	// reason to keep an unused server around.
	MaxIdleTimeout = 24 * time.Hour
	// The janitor looks a tenth of the timeout apart, within these bounds,
	// so the real shutdown stays close to the configured value.
	minJanitorInterval = 5 * time.Second
	maxJanitorInterval = 30 * time.Second
	lspErrorBackoff    = 30 * time.Second
	// How long a request may wait for the server to be indexed, measured
	// from the connection's start. Under load an index run takes far longer
	// than idle, and a partial answer after a short wait is exactly the
	// missed-usages bug this bound exists for.
	indexWaitBudget = 90 * time.Second
	// callBudget bounds one lookup as a whole: the longest index wait plus
	// time for the answer. A server that hangs on a call would otherwise
	// hold the call open for as long as the client waits, and a slot with a
	// call in flight never idles out.
	callBudget = indexWaitBudget + 30*time.Second
	// maxLocations caps a usages answer; a symbol with more locations
	// answers the first ones and says so.
	maxLocations = 200
)

// Statuses reported to the client when the language server cannot answer.
// The set only ever grows.
const (
	StatusNoLanguage   = "no-language"
	StatusNotInstalled = "not-installed"
	StatusBusy         = "busy"
	StatusCanceled     = "canceled"
	StatusError        = "error"
	StatusUnavailable  = "unavailable"
	// StatusDisabled is the handler's answer for a profile switched off in
	// the settings; the service itself knows no settings.
	StatusDisabled = "disabled"
)

// Service owns every language server connection. One Service belongs to one
// serve process. projectsRoot is what the Docker option mounts into a
// container, at its own path, so file URIs match inside and outside.
type Service struct {
	projectsRoot string
	// cacheRoot is where the per project cache directories live, the binds
	// that carry a server's index and the sources it downloaded. It is
	// this process's own state directory, see CacheRoot.
	cacheRoot string
	// dockerHost answers the daemon the cockpit is configured for, the
	// same one the availability gate reads; nil or empty means the ambient
	// one. It reaches every docker CLI call of the feature as DOCKER_HOST.
	dockerHost func() string

	ctx    context.Context
	cancel context.CancelFunc
	// prepCtx bounds launcher preparation and the boot sweep. It is
	// cancelled first on Close, so a shutdown never waits an image build
	// out; aborting one is safe, the next start simply builds again.
	prepCtx    context.Context
	prepCancel context.CancelFunc
	wg         sync.WaitGroup

	mu      sync.Mutex
	conns   map[connKey]*managedConn
	backoff map[string]time.Time
	// sweepDone gates the first server starts behind the boot sweep, see
	// SweepStale; without a sweep it starts closed.
	sweepDone chan struct{}

	// onChange is told the project of every move of an indexing picture: a
	// slot appearing, a handshake ending, announced progress, a death, a
	// removal. Set once at startup before the first connection, like the
	// seams below; the web layer publishes it as the `lsp` event.
	onChange func(project string)

	// idleTimeout answers the configured idle timeout, read again on every
	// janitor round so a changed setting applies without a restart. Held
	// atomically because the janitor runs from New on, before the web
	// layer wires the setting in.
	idleTimeout atomic.Pointer[func() time.Duration]
	// maxProjects answers the configured limit of projects with running
	// servers, read again on every admission and janitor round.
	maxProjects atomic.Pointer[func() int]

	// images keeps the files type searches and the types below a type read
	// out of the servers' images, ownFiles what type searches read off the
	// projects' own files.
	images       imageSources
	ownFiles     ownFiles
	typeReuse    typeReuse
	libraryFiles libraryFiles
	// holds numbers the documents lookups open for a request at a position
	// in them, see lookupHolder.
	holds atomic.Int64

	// Seams for tests: process start and time.
	startConn func(ctx context.Context, profile *Profile, argv, env []string, root string, initOptions any, notify func()) (*lspConn, error)
	now       func() time.Time
	// callBudget is the deadline of one lookup, callBudget outside tests.
	callBudget time.Duration
	// walkBudget is how long a walk to the indirect types below a type may
	// take, walkBudget outside tests.
	walkBudget time.Duration
	// kick runs one janitor round at once, so a test drives the real loop
	// without waiting out its interval.
	kick chan struct{}
}

// SetIdleTimeout wires the idle timeout the janitor reads before every
// round. Nil puts DefaultIdleTimeout back. Nil-receiver-safe like the other
// web-facing entry points.
func (s *Service) SetIdleTimeout(fn func() time.Duration) {
	if s == nil {
		return
	}
	if fn == nil {
		s.idleTimeout.Store(nil)
		return
	}
	s.idleTimeout.Store(&fn)
}

// currentIdleTimeout is the timeout in force, clamped into the range the
// setting accepts, so a stored value that slipped past the form cannot stop
// servers between two watch renewals.
func (s *Service) currentIdleTimeout() time.Duration {
	timeout := DefaultIdleTimeout
	if fn := s.idleTimeout.Load(); fn != nil {
		timeout = (*fn)()
	}
	return ClampIdleTimeout(timeout)
}

// SetMaxProjects wires the limit of projects with running servers, read on
// every admission of a new project and every janitor round. Nil puts
// DefaultMaxProjects back. Nil-receiver-safe like the other web-facing
// entry points.
func (s *Service) SetMaxProjects(fn func() int) {
	if s == nil {
		return
	}
	if fn == nil {
		s.maxProjects.Store(nil)
		return
	}
	s.maxProjects.Store(&fn)
}

// currentMaxProjects is the limit in force, clamped into the range the
// setting accepts.
func (s *Service) currentMaxProjects() int {
	limit := DefaultMaxProjects
	if fn := s.maxProjects.Load(); fn != nil {
		limit = (*fn)()
	}
	return ClampMaxProjects(limit)
}

// ClampMaxProjects holds a limit between MinMaxProjects and MaxMaxProjects,
// the rule the web layer reads a stored value with: a value under the floor
// reads as the floor, never as the default.
func ClampMaxProjects(limit int) int {
	return min(max(limit, MinMaxProjects), MaxMaxProjects)
}

// ClampIdleTimeout holds a timeout between MinIdleTimeout and
// MaxIdleTimeout, the same rule as ClampMaxProjects.
func ClampIdleTimeout(timeout time.Duration) time.Duration {
	return min(max(timeout, MinIdleTimeout), MaxIdleTimeout)
}

// janitorInterval is how far apart the janitor looks for a timeout: a tenth
// of it, between minJanitorInterval and maxJanitorInterval, so a server
// stops at most that much after its timeout passed.
func janitorInterval(timeout time.Duration) time.Duration {
	interval := timeout / 10
	if interval < minJanitorInterval {
		return minJanitorInterval
	}
	if interval > maxJanitorInterval {
		return maxJanitorInterval
	}
	return interval
}

// OnChange registers the one listener for indexing moves; call before the
// service serves. Nil-receiver-safe like the other web-facing entry points.
func (s *Service) OnChange(fn func(project string)) {
	if s == nil {
		return
	}
	s.onChange = fn
}

func (s *Service) notifyChange(project string) {
	if s.onChange != nil {
		s.onChange(project)
	}
}

// New returns a running service. cacheRoot is where the per project cache
// directories live, CacheRoot of the serve process's state directory.
// dockerHost names the configured daemon, nil for the ambient one.
func New(projectsRoot, cacheRoot string, dockerHost func() string) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	prepCtx, prepCancel := context.WithCancel(context.Background())
	swept := make(chan struct{})
	close(swept)
	s := &Service{
		projectsRoot: projectsRoot,
		cacheRoot:    cacheRoot,
		dockerHost:   dockerHost,
		ctx:          ctx,
		cancel:       cancel,
		prepCtx:      prepCtx,
		prepCancel:   prepCancel,
		conns:        map[connKey]*managedConn{},
		backoff:      map[string]time.Time{},
		sweepDone:    swept,
		startConn:    startLSPConn,
		now:          time.Now,
		callBudget:   callBudget,
		walkBudget:   walkBudget,
		kick:         make(chan struct{}),
	}
	s.wg.Add(1)
	go s.runJanitor()
	return s
}

// Close shuts every language server down and stops the janitor. The
// preparation context falls first, so a shutdown never waits an in flight
// image build out; the graceful shutdown runs before the process contexts
// are cancelled, so servers get their shutdown request instead of a bare
// kill.
func (s *Service) Close() {
	s.prepCancel()
	s.mu.Lock()
	conns := make([]*managedConn, 0, len(s.conns))
	for _, mc := range s.conns {
		mc.retired = true
		conns = append(conns, mc)
	}
	s.conns = map[connKey]*managedConn{}
	s.mu.Unlock()
	s.closeAll(conns)
	s.cancel()
	s.wg.Wait()
}

// CloseProject shuts the project's language servers down the graceful way
// and forgets their slots, so the next warm starts fresh servers over a
// fresh scan; the manual reindex and a project delete are the callers.
// Nil-receiver-safe like the other web-facing entry points.
func (s *Service) CloseProject(project string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	var closing []*managedConn
	for key, mc := range s.conns {
		if key.project == project {
			mc.retired = true
			delete(s.conns, key)
			closing = append(closing, mc)
		}
	}
	s.mu.Unlock()
	s.closeAll(closing)
	if len(closing) > 0 {
		s.notifyChange(project)
	}
}

// closeAll closes the given connections in parallel and waits them out.
func (s *Service) closeAll(conns []*managedConn) {
	var wg sync.WaitGroup
	for _, mc := range conns {
		wg.Add(1)
		go func(mc *managedConn) {
			defer wg.Done()
			s.closeManaged(mc)
		}(mc)
	}
	wg.Wait()
}

func (s *Service) closeManaged(mc *managedConn) {
	<-mc.ready
	if mc.conn != nil {
		mc.conn.close()
	}
	if mc.cancel != nil {
		mc.cancel()
	}
}

func (s *Service) runJanitor() {
	defer s.wg.Done()
	// A timer set anew every round rather than a ticker: the interval
	// follows the timeout, which may change between two rounds.
	timer := time.NewTimer(janitorInterval(s.currentIdleTimeout()))
	defer timer.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-timer.C:
			s.expireIdle()
			s.enforceMaxProjects()
			timer.Reset(janitorInterval(s.currentIdleTimeout()))
		case <-s.kick:
			s.expireIdle()
			s.enforceMaxProjects()
		}
	}
}

// expireIdle closes connections idle past the timeout and drops dead ones.
// A slot in use is not idle whatever its clock says: a lookup routes no
// Touch, and one waiting out the indexing may stand longer than the
// timeout, so retiring the slot under it would fail a call nobody closed.
func (s *Service) expireIdle() {
	now := s.now()
	timeout := s.currentIdleTimeout()
	s.mu.Lock()
	var expired []*managedConn
	var projects []string
	for key, mc := range s.conns {
		select {
		case <-mc.ready:
		default:
			continue
		}
		dead := s.slotDead(mc)
		if dead && s.restartPending(mc) {
			continue
		}
		if !dead && s.inUse(mc) {
			continue
		}
		if dead || now.Sub(mc.lastUsed) > timeout {
			// A dead slot is not retired: whether its death was a restart
			// wish stays watchRestart's call, the janitor only drops it.
			if !dead {
				mc.retired = true
			}
			delete(s.conns, key)
			expired = append(expired, mc)
			projects = append(projects, key.project)
		}
	}
	s.mu.Unlock()
	for _, mc := range expired {
		go s.closeManaged(mc)
	}
	for _, project := range projects {
		s.notifyChange(project)
	}
}

// WarmMode names one profile to warm and the way its server runs.
type WarmMode struct {
	ProfileID string
	Launcher  Launcher
}

// Warm makes sure the project's server for each given profile runs, so the
// indexing starts when the editor page opens instead of with the first
// lookup. It answers nothing: a profile that cannot start (not installed,
// every running project busy) simply stays cold and the first lookup says
// why.
func (s *Service) Warm(project, root string, modes []WarmMode) {
	if s == nil {
		return
	}
	for _, mode := range modes {
		for _, p := range profiles {
			if p.ID == mode.ProfileID {
				if mc, status := s.connFor(context.Background(), project, root, p, mode.Launcher, s.now()); status == "" {
					s.release(mc)
				}
			}
		}
	}
}

// Touch marks editor action for the project: every connection of it counts
// as used now, which is what the idle shutdown measures. Safe on a nil
// service, which is what the web tests build their server without.
func (s *Service) Touch(project string) {
	if s == nil {
		return
	}
	now := s.now()
	s.mu.Lock()
	for key, mc := range s.conns {
		if key.project == project {
			mc.lastUsed = now
		}
	}
	s.mu.Unlock()
}

// IndexState is one profile's indexing picture for the editor's statusbar
// indicator.
type IndexState struct {
	ProfileID string `json:"id"`
	Label     string `json:"label"`
	Indexing  bool   `json:"indexing"`
	// Preparing marks the stretch before the server process answers: the
	// launcher stands up what the start needs, which for the Docker way is
	// the image build on first use. The client words that phase apart from
	// the indexing, so a first activation is never a silent minute.
	Preparing bool `json:"preparing,omitempty"`
	// Percentage is the server's reported progress, -1 while it reports
	// none, which the client shows as an indeterminate indicator.
	Percentage int `json:"percentage"`
}

// IndexStatus answers which of the project's servers are indexing right
// now, which is when a lookup waits: a starting connection counts as
// indexing, a ready one while its announced work runs, and one that indexes
// at the handshake until it announced that run (lspConn.starting).
func (s *Service) IndexStatus(project string) []IndexState {
	if s == nil {
		return []IndexState{}
	}
	s.mu.Lock()
	type snap struct {
		profile string
		mc      *managedConn
	}
	snaps := make([]snap, 0)
	for key, mc := range s.conns {
		if key.project == project {
			snaps = append(snaps, snap{profile: key.profile, mc: mc})
		}
	}
	s.mu.Unlock()
	states := make([]IndexState, 0, len(snaps))
	for _, p := range profiles {
		for _, sn := range snaps {
			if sn.profile != p.ID {
				continue
			}
			state := IndexState{ProfileID: p.ID, Label: p.Label, Percentage: -1}
			select {
			case <-sn.mc.ready:
				if sn.mc.conn != nil && sn.mc.conn.alive() {
					state.Indexing, _, state.Percentage = sn.mc.conn.progress()
					state.Indexing = state.Indexing || sn.mc.conn.starting()
					if !state.Indexing {
						state.Percentage = -1
					}
				}
			default:
				state.Indexing = true
				state.Preparing = true
			}
			states = append(states, state)
		}
	}
	return states
}

// CloseDocument lets the client go of the document on the project's shared
// connections, sent when a tab closes. The document really closes only when
// no other editor instance holds it, see closeDocument. Nil-receiver-safe
// like the other web-facing entry points.
func (s *Service) CloseDocument(client, project, path string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	var matching []*managedConn
	for key, mc := range s.conns {
		if key.project == project {
			matching = append(matching, mc)
		}
	}
	s.mu.Unlock()
	for _, mc := range matching {
		select {
		case <-mc.ready:
		default:
			continue
		}
		if mc.conn == nil || !mc.conn.alive() {
			continue
		}
		mc.conn.docMu.Lock()
		mc.conn.closeDocument(client, path)
		mc.conn.docMu.Unlock()
	}
}

// ConnectionCount reports the live and starting language server
// connections. Nil-receiver-safe like the other web-facing entry points.
func (s *Service) ConnectionCount() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}
