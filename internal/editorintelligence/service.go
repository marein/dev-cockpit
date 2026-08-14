package editorintelligence

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"
)

// Connection limits. A connection is one language server process per
// project and profile, shared by every editor instance of the project, so a
// page refresh reconnects to the warm index instead of building a new one.
const (
	maxConnections = 8
	// A connection lives until the project saw no editor action for this
	// long; every editor route of the project counts as action, see Touch.
	// Short on purpose: the warm cache volume makes a fresh start cheap.
	connIdleTimeout = 10 * time.Minute
	janitorInterval = 30 * time.Second
	lspErrorBackoff = 30 * time.Second
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

	// lastUsed and retired are guarded by Service.mu. retired marks a
	// deliberate close (shutdown, project delete, idle expiry, eviction):
	// watchRestart never resurrects a retired slot.
	lastUsed time.Time
	retired  bool

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

	// Seams for tests: process start, time, and the warming grace a silent
	// connection is waited on, warmupWindow outside tests.
	startConn   func(ctx context.Context, profile *Profile, argv, env []string, root string, initOptions any, notify func()) (*lspConn, error)
	now         func() time.Time
	idleTimeout time.Duration
	warmupGrace time.Duration
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
		idleTimeout:  connIdleTimeout,
		warmupGrace:  warmupWindow,
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
	ticker := time.NewTicker(janitorInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.expireIdle()
		}
	}
}

// expireIdle closes connections idle past the timeout and drops dead ones.
func (s *Service) expireIdle() {
	now := s.now()
	s.mu.Lock()
	var expired []*managedConn
	var projects []string
	for key, mc := range s.conns {
		select {
		case <-mc.ready:
		default:
			continue
		}
		dead := mc.conn == nil || !mc.conn.alive()
		if dead || now.Sub(mc.lastUsed) > s.idleTimeout {
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
	mc, status := s.connFor(ctx, req.ProjectName, req.ProjectRoot, profile, req.Launcher)
	if status != "" {
		return unavailable(status), nil
	}

	callCtx, token := mc.beginCall(ctx, req.Client, req.Path)
	defer mc.endCall(req.Client, req.Path, token)

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

	if callCtx.Err() != nil {
		return unavailable(StatusCanceled), nil
	}
	log.Printf("editor intelligence: %s %s failed: %v", profile.ID, method, err)
	if !mc.conn.alive() {
		s.dropConn(req.ProjectName, profile, mc)
		// A death that is the launcher's restart wish is routine, the
		// workspace watcher's doing, and watchRestart already starts the
		// replacement: it must not put the project into the error backoff.
		if !s.exitWasRestartWish(mc) {
			s.setBackoff(backoff, lspErrorBackoff)
		}
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
// table full of working slots) simply stays cold and the first lookup says
// why.
func (s *Service) Warm(project, root string, modes []WarmMode) {
	if s == nil {
		return
	}
	for _, mode := range modes {
		for _, p := range profiles {
			if p.ID == mode.ProfileID {
				s.connFor(context.Background(), project, root, p, mode.Launcher)
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
// available.
func (s *Service) connFor(ctx context.Context, project, root string, profile *Profile, launcher Launcher) (*managedConn, string) {
	if launcher == nil {
		launcher = DockerLauncher(s.cacheRoot, s.dockerHost)
	}
	key := connKey{project: project, profile: profile.ID}
	s.mu.Lock()
	sweepDone := s.sweepDone
	if mc, ok := s.conns[key]; ok {
		// A slot whose root or launcher no longer matches belongs to a
		// project recreated under its name, or to a server whose way to
		// run the settings moved: either way its answers describe
		// something that is gone.
		if !s.slotDead(mc) && mc.root == root && mc.launcher.ID() == launcher.ID() {
			mc.lastUsed = s.now()
			s.mu.Unlock()
			return s.awaitConn(ctx, key, mc)
		}
		delete(s.conns, key)
		go s.closeManaged(mc)
	}
	// A full table is not a refusal while something in it only idles: the
	// least recently used connection with nothing in flight makes room, and
	// busy is the answer only when every slot is actually working.
	evicted, freed := connKey{}, false
	if len(s.conns) >= maxConnections {
		if evicted, freed = s.evictIdleConn(); !freed {
			s.mu.Unlock()
			return nil, StatusBusy
		}
	}
	if !launcher.Detect(profile).Found {
		s.mu.Unlock()
		return nil, StatusNotInstalled
	}
	mc := &managedConn{
		ready:    make(chan struct{}),
		root:     root,
		launcher: launcher,
		lastUsed: s.now(),
		inflight: map[string]*inflightToken{},
	}
	s.conns[key] = mc
	s.mu.Unlock()
	if freed {
		s.notifyChange(evicted.project)
	}
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
		return nil, StatusCanceled
	case <-s.prepCtx.Done():
		s.removeConn(key, mc)
		close(mc.ready)
		s.notifyChange(project)
		return nil, StatusError
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
		return nil, StatusError
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
		return nil, StatusError
	}
	s.wg.Add(1)
	go s.watchRestart(key, mc, project, root, profile, launcher)
	if !profile.SilentStart {
		s.wg.Add(1)
		go s.endSilentWindow(project, mc)
	}
	// The handshake ended: preparing hands over to the announced indexing.
	s.notifyChange(project)
	return mc, ""
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
// with the warm cache volume the restart is cheap. The eager restart only
// happens while the project still sees editor action: an idle project's
// wish is honored lazily by the next editor open, or background churn
// alone would keep a container reindexing forever with no reader. Every
// other death stays what it was, an error the next lookup reports.
func (s *Service) watchRestart(key connKey, mc *managedConn, project, root string, profile *Profile, launcher Launcher) {
	defer s.wg.Done()
	select {
	case <-s.ctx.Done():
		return
	case <-mc.conn.exited:
	}
	if !launcher.WantsRestart(mc.conn.exitStatus()) {
		return
	}
	s.mu.Lock()
	// A missing table entry is fine: a lookup that died with the server
	// already dropped the slot, the restart wish still stands. Only a
	// newer replacement or a deliberate close ends it.
	if cur, ok := s.conns[key]; ok && cur != mc {
		s.mu.Unlock()
		return
	}
	if mc.retired {
		s.mu.Unlock()
		return
	}
	lastUsed := mc.lastUsed
	idle := s.now().Sub(lastUsed) > s.idleTimeout
	delete(s.conns, key)
	s.mu.Unlock()
	go s.closeManaged(mc)
	if idle {
		return
	}
	log.Printf("editor intelligence: %s server for %s asked for a restart, reindexing", profile.ID, project)
	s.connFor(context.Background(), project, root, profile, launcher)
	// A restart is not editor action: the fresh slot keeps the old clock,
	// so the idle timeout keeps measuring from the last real use.
	s.mu.Lock()
	if cur, ok := s.conns[key]; ok && cur.root == root {
		cur.lastUsed = lastUsed
	}
	s.mu.Unlock()
}

// evictIdleConn frees one slot by closing the least recently used
// connection that finished starting and has nothing in flight. It answers
// the evicted key and whether a slot was freed; the caller holds s.mu and
// notifies after unlocking.
func (s *Service) evictIdleConn() (connKey, bool) {
	var lruKey connKey
	var lru *managedConn
	for key, mc := range s.conns {
		select {
		case <-mc.ready:
		default:
			continue
		}
		mc.inflightMu.Lock()
		busy := len(mc.inflight) > 0
		mc.inflightMu.Unlock()
		if busy {
			continue
		}
		if lru == nil || mc.lastUsed.Before(lru.lastUsed) {
			lru, lruKey = mc, key
		}
	}
	if lru == nil {
		return connKey{}, false
	}
	lru.retired = true
	delete(s.conns, lruKey)
	go s.closeManaged(lru)
	return lruKey, true
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

// awaitConn waits for a starting connection to finish its handshake.
func (s *Service) awaitConn(ctx context.Context, key connKey, mc *managedConn) (*managedConn, string) {
	select {
	case <-ctx.Done():
		return nil, StatusCanceled
	case <-mc.ready:
	}
	if mc.err != nil || mc.conn == nil || !mc.conn.alive() {
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
