package editorintelligence

import (
	"context"
	"errors"
	"log"
	"sort"
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
	// A connection counts as warming for warmupWindow after process start.
	// Within it, empty answers retry a few times with a short delay per
	// request (a newer request cancels the wait), and a connection that has
	// not announced its indexing yet is still waited on, because on a loaded
	// host the announcement itself arrives late.
	warmupWindow     = 45 * time.Second
	warmupRetryDelay = 700 * time.Millisecond
	warmupRetries    = 5
	// How long a request may wait for the server's announced indexing to
	// end, measured from the connection's start. Under load an index run
	// takes far longer than idle, and a partial answer after a short wait is
	// exactly the missed-usages bug this bound exists for.
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

// The two navigation methods a request can run.
const (
	methodDefinition = "textDocument/definition"
	methodReferences = "textDocument/references"
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

// Request is one navigation request against the active document snapshot.
// Client names the asking editor instance: the connection is shared per
// project, the client only scopes document holds and in-flight
// cancellation. Launcher is the way the settings picked for the
// language's server; nil means the Docker way, the default.
type Request struct {
	Client      string
	ProjectName string
	ProjectRoot string
	Launcher    Launcher
	// Path is the file the cursor stands in, already validated by the
	// caller: project relative, or the absolute path of a source outside
	// the project the caller checked against the allowlist, which is what
	// a lookup from inside a read only tab asks with. Both travel the same
	// way from here, see documentPath.
	Path    string
	Content string
	// Line and Character are the 0-based LSP position, Character in UTF-16
	// units like the CodeMirror document.
	Line      int
	Character int
}

// Result is one navigation answer. An unavailable server travels as a
// status inside an available=false result, never as an error.
type Result struct {
	Available bool       `json:"available"`
	Status    string     `json:"status,omitempty"`
	Locations []Location `json:"locations"`
	// Outside counts targets dropped because they lie outside the project.
	Outside   int  `json:"outside,omitempty"`
	Truncated bool `json:"truncated,omitempty"`
	// Declaration reports that a definition answer covers the asked
	// position itself: the cursor already sits on the declaration, and a
	// jump would lead nowhere new.
	Declaration bool `json:"declaration,omitempty"`
}

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

	// Seams for tests: process start, time, and the warming grace a silent
	// connection is waited on, warmupWindow outside tests.
	startConn   func(ctx context.Context, profile *Profile, argv, env []string, root string, initOptions any, notify func()) (*lspConn, error)
	now         func() time.Time
	warmupGrace time.Duration
	// callBudget is the deadline of one lookup, callBudget outside tests.
	callBudget time.Duration
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
		warmupGrace:  warmupWindow,
		callBudget:   callBudget,
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

// Definition answers where the symbol at the position is defined.
// Nil-receiver-safe like the other web-facing entry points.
func (s *Service) Definition(ctx context.Context, req Request) (Result, error) {
	if s == nil {
		return unavailable(StatusUnavailable), nil
	}
	return s.navigate(ctx, req, methodDefinition)
}

// References answers every location the symbol at the position is used at,
// its declaration included. Nil-receiver-safe like the other web-facing
// entry points.
func (s *Service) References(ctx context.Context, req Request) (Result, error) {
	if s == nil {
		return unavailable(StatusUnavailable), nil
	}
	return s.navigate(ctx, req, methodReferences)
}

func unavailable(status string) Result {
	return Result{Status: status, Locations: []Location{}}
}

// navigate runs one navigation request. It returns an error only for
// invalid input the handler should reject; server failures travel in the
// result status.
func (s *Service) navigate(ctx context.Context, req Request, method string) (Result, error) {
	doc := newDocText(req.Content)
	if !doc.validPosition(req.Line, req.Character) {
		return Result{}, errors.New("position is outside the document")
	}
	profile, langID, ok := ProfileForPath(req.Path)
	if !ok {
		return unavailable(StatusNoLanguage), nil
	}
	backoff := backoffKey(req.ProjectName, profile)
	if s.inBackoff(backoff) {
		return unavailable(StatusUnavailable), nil
	}
	mc, status := s.connFor(ctx, req.ProjectName, req.ProjectRoot, profile, req.Launcher, s.now())
	if status != "" {
		return unavailable(status), nil
	}

	budgetCtx, cancelBudget := context.WithTimeout(ctx, s.callBudget)
	defer cancelBudget()
	callCtx, token := mc.beginCall(budgetCtx, req.Client, req.Path)
	defer mc.endCall(req.Client, req.Path, token)
	s.release(mc)

	mc.conn.docMu.Lock()
	err := mc.conn.ensureDocument(req.Client, req.Path, langID, req.Content)
	mc.conn.docMu.Unlock()
	// The wait runs outside docMu: it may stand for a long time, and a tab
	// closing meanwhile must not queue behind it.
	if err == nil {
		err = mc.conn.waitIndexed(callCtx, s.warmupGrace, indexWaitBudget)
	}
	// Each attempt re-syncs the shared document and sends the request under
	// docMu, so another client's didChange during the wait or a retry sleep
	// can never make the position describe somebody else's text; the
	// response wait and the sleeps run outside the lock, a tab closing
	// meanwhile never queues behind them.
	var raw []lspLocation
	for attempt := 0; err == nil; attempt++ {
		var id int64
		var ch chan rpcMessage
		mc.conn.docMu.Lock()
		err = mc.conn.ensureDocument(req.Client, req.Path, langID, req.Content)
		if err == nil {
			id, ch, err = mc.conn.startLocations(method, req.Path, req.Line, req.Character)
		}
		mc.conn.docMu.Unlock()
		if err != nil {
			break
		}
		raw, err = mc.conn.awaitLocations(callCtx, id, ch)
		if err != nil || len(raw) > 0 {
			break
		}
		// An empty answer is retried only while the server may still be
		// getting going, which is the same stretch the wait above sits out
		// and therefore the same question: past it an empty answer is the
		// truth, and retrying it only keeps the reader in front of a spinner.
		if attempt >= warmupRetries || callCtx.Err() != nil || !mc.conn.warming(s.warmupGrace) {
			break
		}
		select {
		case <-callCtx.Done():
			err = callCtx.Err()
		case <-time.After(warmupRetryDelay):
		}
	}

	if err == nil {
		s.touch(mc)
		locs, outside := mapLocations(mc.conn.rootURI, raw, mc.launcher.SourceRoots(req.ProjectName, profile))
		declaration := false
		if method == methodDefinition {
			declaration = atRequestPosition(mc.conn.rootURI, raw, req)
		}
		if method == methodReferences {
			sortReferences(locs, req.Path)
		}
		truncated := len(locs) > maxLocations
		if truncated {
			locs = locs[:maxLocations]
		}
		return Result{Available: true, Locations: locs, Outside: outside, Truncated: truncated, Declaration: declaration}, nil
	}

	if budgetCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
		log.Printf("editor intelligence: %s %s got no answer within %s", profile.ID, method, s.callBudget)
		return unavailable(StatusError), nil
	}
	if callCtx.Err() != nil {
		return unavailable(StatusCanceled), nil
	}
	log.Printf("editor intelligence: %s %s failed: %v", profile.ID, method, err)
	// A death that is the launcher's restart wish is routine, the workspace
	// watcher's doing: watchRestart swaps the replacement into the slot, so
	// the slot stays for it and the project stays out of the error backoff.
	if !mc.conn.alive() && !s.exitWasRestartWish(mc) {
		s.dropConn(req.ProjectName, profile, mc)
		s.setBackoff(backoff, lspErrorBackoff)
	}
	return unavailable(StatusError), nil
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
// now: a starting connection counts as indexing (its announcement has not
// arrived yet), a ready one by its announced work, and a ready one that has
// announced nothing counts as indexing for the warming window, because the
// announcement itself arrives seconds after the handshake and the indicator
// must not flicker off in that gap.
//
// That last rule is about the gap between a handshake and the work it
// started, so it holds only for the servers that work there. A
// `SilentStart` server has no such gap: it is ready when it answers, so
// counting its silence as indexing would put a bar on the screen for work
// that is not happening, waiting for an end that is not coming. Its silence
// is readiness and is reported as such; the work it does announce later,
// fetching types for an untyped dependency, shows like anybody else's.
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
					var seen bool
					state.Indexing, seen, state.Percentage = sn.mc.conn.progress()
					if !state.Indexing && !seen && sn.mc.conn.warming(s.warmupGrace) {
						state.Indexing = true
					}
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

// beginCall cancels the client's previous in flight call for the document
// and registers the new one; another client's call is left alone, the
// connection is shared.
func (mc *managedConn) beginCall(ctx context.Context, client, path string) (context.Context, *inflightToken) {
	callCtx, cancel := context.WithCancel(ctx)
	token := &inflightToken{cancel: cancel}
	key := client + "\x00" + path
	mc.inflightMu.Lock()
	if previous := mc.inflight[key]; previous != nil {
		previous.cancel()
	}
	mc.inflight[key] = token
	mc.inflightMu.Unlock()
	return callCtx, token
}

func (mc *managedConn) endCall(client, path string, token *inflightToken) {
	key := client + "\x00" + path
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
