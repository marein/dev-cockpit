package editorintelligence

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"
)

// Connection and provider limits. A connection is one language server
// process for one browser editor instance, project and profile, so two tabs
// with different unsaved content never share a document.
const (
	maxConnections          = 8
	maxConnectionsPerClient = 3
	connIdleTimeout         = 3 * time.Minute
	janitorInterval         = 30 * time.Second
	lspErrorBackoff         = 30 * time.Second
	aiErrorBackoff          = time.Minute
	// Empty completions on a young connection retry a few times with a
	// short delay, covering servers that are still parsing the just opened
	// document or indexing the workspace. A newer request cancels the wait.
	warmupWindow     = 45 * time.Second
	warmupRetryDelay = 700 * time.Millisecond
	warmupRetries    = 5
)

// Statuses reported to the client when a source is unavailable. The set only
// ever grows.
const (
	StatusDisabled     = "disabled"
	StatusNoLanguage   = "no-language"
	StatusNotInstalled = "not-installed"
	StatusBusy         = "busy"
	StatusStale        = "stale"
	StatusCanceled     = "canceled"
	StatusError        = "error"
	StatusUnavailable  = "unavailable"
	StatusWithheld     = "withheld"
	StatusEmpty        = "empty"
)

// Request is one completion request against the active document snapshot.
type Request struct {
	Client      string
	ProjectName string
	ProjectRoot string
	// Path is the project relative file path, already validated by the
	// caller.
	Path      string
	Version   int
	Content   string
	Line      int
	Character int
	WantLSP   bool
	WantAI    bool
}

// LSPResult is the language server half of a completion response.
type LSPResult struct {
	Available  bool   `json:"available"`
	Status     string `json:"status,omitempty"`
	From       int    `json:"from"`
	Incomplete bool   `json:"incomplete,omitempty"`
	Items      []Item `json:"items"`
}

// AIResult is the AI half of a completion response, at most one insertion.
type AIResult struct {
	Available bool   `json:"available"`
	Status    string `json:"status,omitempty"`
	Insert    string `json:"insert,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

// Response bundles the requested sources.
type Response struct {
	LSP *LSPResult `json:"lsp,omitempty"`
	AI  *AIResult  `json:"ai,omitempty"`
}

type connKey struct {
	client  string
	project string
	profile string
}

// inflightToken identifies one in flight completion per document, so a newer
// request can cancel the older one.
type inflightToken struct {
	cancel context.CancelFunc
}

// managedConn is a connection slot. It enters the table before the process
// starts, so the limits count starting connections and concurrent requests
// for the same key wait for one handshake instead of racing a second
// process.
type managedConn struct {
	ready  chan struct{}
	conn   *lspConn
	err    error
	cancel context.CancelFunc

	lastUsed time.Time

	inflightMu sync.Mutex
	inflight   map[string]*inflightToken
}

// Service owns every language server connection and the AI provider access.
// One Service belongs to one serve process.
type Service struct {
	Config  *ConfigStore
	Secrets *SecretStore

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	conns   map[connKey]*managedConn
	backoff map[string]time.Time

	// Seams for tests: process start, provider construction and time.
	startConn   func(ctx context.Context, profile *Profile, argv []string, root string) (*lspConn, error)
	newProvider func(model string) CompletionProvider
	now         func() time.Time
	idleTimeout time.Duration
}

// New returns a running service backed by the state dir stores.
func New(stateDir string) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{
		Config:      NewConfigStore(stateDir),
		Secrets:     NewSecretStore(stateDir),
		ctx:         ctx,
		cancel:      cancel,
		conns:       map[connKey]*managedConn{},
		backoff:     map[string]time.Time{},
		startConn:   startLSPConn,
		newProvider: func(model string) CompletionProvider { return NewOllama(model) },
		now:         time.Now,
		idleTimeout: connIdleTimeout,
	}
	s.wg.Add(1)
	go s.runJanitor()
	return s
}

// Close shuts every language server down and stops the janitor. The
// graceful shutdown runs before the process contexts are cancelled, so
// servers get their shutdown request instead of a bare kill.
func (s *Service) Close() {
	s.mu.Lock()
	conns := make([]*managedConn, 0, len(s.conns))
	for _, mc := range s.conns {
		conns = append(conns, mc)
	}
	s.conns = map[connKey]*managedConn{}
	s.mu.Unlock()
	var wg sync.WaitGroup
	for _, mc := range conns {
		wg.Add(1)
		go func(mc *managedConn) {
			defer wg.Done()
			s.closeManaged(mc)
		}(mc)
	}
	wg.Wait()
	s.cancel()
	s.wg.Wait()
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
	for key, mc := range s.conns {
		select {
		case <-mc.ready:
		default:
			continue
		}
		dead := mc.conn == nil || !mc.conn.alive()
		if dead || now.Sub(mc.lastUsed) > s.idleTimeout {
			delete(s.conns, key)
			expired = append(expired, mc)
		}
	}
	s.mu.Unlock()
	for _, mc := range expired {
		go s.closeManaged(mc)
	}
}

// Complete answers one completion request. It returns an error only for
// invalid input the handler should reject; source failures travel in the
// response statuses.
func (s *Service) Complete(ctx context.Context, req Request) (Response, error) {
	doc := newDocText(req.Content)
	if !doc.validPosition(req.Line, req.Character) {
		return Response{}, errors.New("position is outside the document")
	}
	settings := s.Config.Load()
	resp := Response{}
	if req.WantLSP {
		result := s.lspComplete(ctx, settings, req, doc)
		resp.LSP = &result
	}
	if req.WantAI {
		result := s.aiComplete(ctx, settings, req, doc)
		resp.AI = &result
	}
	return resp, nil
}

func lspUnavailable(status string) LSPResult {
	return LSPResult{Status: status, Items: []Item{}}
}

func (s *Service) lspComplete(ctx context.Context, settings Settings, req Request, doc *docText) LSPResult {
	if settings.Mode == ModeOff {
		return lspUnavailable(StatusDisabled)
	}
	profile, langID, ok := ProfileForPath(req.Path)
	if !ok {
		return lspUnavailable(StatusNoLanguage)
	}
	if !settings.ProfileEnabled(profile.ID) {
		return lspUnavailable(StatusDisabled)
	}
	if s.inBackoff("lsp:" + profile.ID) {
		return lspUnavailable(StatusUnavailable)
	}
	mc, status := s.connFor(ctx, req, profile)
	if status != "" {
		return lspUnavailable(status)
	}

	callCtx, token := mc.beginCall(ctx, req.Path)
	defer mc.endCall(req.Path, token)

	mc.conn.docMu.Lock()
	err := mc.conn.ensureDocument(req.Path, langID, req.Version, req.Content)
	if err == nil {
		var items []lspCompletionItem
		var incomplete bool
		items, incomplete, err = mc.conn.completion(callCtx, req.Path, req.Line, req.Character)
		for attempt := 0; err == nil && len(items) == 0 && attempt < warmupRetries; attempt++ {
			if callCtx.Err() != nil || time.Since(mc.conn.startedAt) >= warmupWindow {
				break
			}
			select {
			case <-callCtx.Done():
			case <-time.After(warmupRetryDelay):
				items, incomplete, err = mc.conn.completion(callCtx, req.Path, req.Line, req.Character)
			}
		}
		if err == nil {
			mc.conn.docMu.Unlock()
			s.touch(mc)
			from, normalized := normalizeCompletions(items, doc, req.Line, req.Character)
			return LSPResult{Available: true, From: from, Incomplete: incomplete, Items: normalized}
		}
	}
	mc.conn.docMu.Unlock()

	switch {
	case errors.Is(err, errStale):
		return lspUnavailable(StatusStale)
	case callCtx.Err() != nil:
		return lspUnavailable(StatusCanceled)
	}
	log.Printf("editor intelligence: %s completion failed: %v", profile.ID, err)
	if !mc.conn.alive() {
		s.dropConn(req, profile, mc)
		s.setBackoff("lsp:"+profile.ID, lspErrorBackoff)
	}
	return lspUnavailable(StatusError)
}

// beginCall cancels the previous in flight completion for the document and
// registers the new one.
func (mc *managedConn) beginCall(ctx context.Context, path string) (context.Context, *inflightToken) {
	callCtx, cancel := context.WithCancel(ctx)
	token := &inflightToken{cancel: cancel}
	mc.inflightMu.Lock()
	if previous := mc.inflight[path]; previous != nil {
		previous.cancel()
	}
	mc.inflight[path] = token
	mc.inflightMu.Unlock()
	return callCtx, token
}

func (mc *managedConn) endCall(path string, token *inflightToken) {
	mc.inflightMu.Lock()
	if mc.inflight[path] == token {
		delete(mc.inflight, path)
	}
	mc.inflightMu.Unlock()
	token.cancel()
}

// connFor returns the live connection for the request, starting one when
// needed. A non empty status tells the caller why no connection is
// available.
func (s *Service) connFor(ctx context.Context, req Request, profile *Profile) (*managedConn, string) {
	key := connKey{client: req.Client, project: req.ProjectName, profile: profile.ID}
	s.mu.Lock()
	if mc, ok := s.conns[key]; ok {
		if !s.slotDead(mc) {
			mc.lastUsed = s.now()
			s.mu.Unlock()
			return s.awaitConn(ctx, key, mc)
		}
		delete(s.conns, key)
	}
	if len(s.conns) >= maxConnections || s.clientConns(req.Client) >= maxConnectionsPerClient {
		s.mu.Unlock()
		return nil, StatusBusy
	}
	detection := profile.Detect()
	if !detection.Found {
		s.mu.Unlock()
		return nil, StatusNotInstalled
	}
	mc := &managedConn{
		ready:    make(chan struct{}),
		lastUsed: s.now(),
		inflight: map[string]*inflightToken{},
	}
	s.conns[key] = mc
	s.mu.Unlock()

	argv := append([]string{detection.Path}, profile.Command[1:]...)
	procCtx, cancel := context.WithCancel(s.ctx)
	conn, err := s.startConn(procCtx, profile, argv, req.ProjectRoot)
	mc.conn = conn
	mc.err = err
	mc.cancel = cancel
	close(mc.ready)
	if err != nil {
		cancel()
		s.removeConn(key, mc)
		s.setBackoff("lsp:"+profile.ID, lspErrorBackoff)
		log.Printf("editor intelligence: %v", err)
		return nil, StatusError
	}
	return mc, ""
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

// clientConns counts table slots per client. Caller holds s.mu.
func (s *Service) clientConns(client string) int {
	n := 0
	for key := range s.conns {
		if key.client == client {
			n++
		}
	}
	return n
}

func (s *Service) removeConn(key connKey, mc *managedConn) {
	s.mu.Lock()
	if s.conns[key] == mc {
		delete(s.conns, key)
	}
	s.mu.Unlock()
}

func (s *Service) dropConn(req Request, profile *Profile, mc *managedConn) {
	key := connKey{client: req.Client, project: req.ProjectName, profile: profile.ID}
	s.removeConn(key, mc)
	go s.closeManaged(mc)
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

func aiUnavailable(status string) AIResult {
	return AIResult{Status: status}
}

func (s *Service) aiComplete(ctx context.Context, settings Settings, req Request, doc *docText) AIResult {
	if !settings.AIConfigured() {
		return aiUnavailable(StatusDisabled)
	}
	if sensitivePath(req.Path) {
		return aiUnavailable(StatusWithheld)
	}
	if s.inBackoff("ai") {
		return aiUnavailable(StatusUnavailable)
	}
	offset, ok := doc.byteOffset(req.Line, req.Character)
	if !ok {
		return aiUnavailable(StatusError)
	}
	prefix, suffix := buildFIMContext(req.Content, offset)
	_, langID, _ := ProfileForPath(req.Path)
	provider := s.newProvider(settings.Ollama.Model)
	started := s.now()
	insert, err := provider.Complete(ctx, FIMRequest{
		Language: langID,
		Path:     req.Path,
		Prefix:   prefix,
		Suffix:   suffix,
	})
	if err != nil {
		if ctx.Err() != nil {
			return aiUnavailable(StatusCanceled)
		}
		s.setBackoff("ai", aiErrorBackoff)
		log.Printf("editor intelligence: ai completion failed after %s: %v", s.now().Sub(started).Round(time.Millisecond), err)
		return aiUnavailable(StatusUnavailable)
	}
	if insert == "" {
		return aiUnavailable(StatusEmpty)
	}
	return AIResult{Available: true, Insert: insert, Detail: "Ollama"}
}

// CloseDocument releases the document on every connection the client holds
// for the project, sent when a tab closes.
func (s *Service) CloseDocument(client, project, path string) {
	s.mu.Lock()
	var matching []*managedConn
	for key, mc := range s.conns {
		if key.client == client && key.project == project {
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
		mc.conn.closeDocument(path)
		mc.conn.docMu.Unlock()
	}
}

// TestProvider checks the configured AI provider without sending source
// code, feeding the settings page test action.
func (s *Service) TestProvider(ctx context.Context, model string) (bool, string) {
	provider := s.newProvider(model)
	available, reason := provider.Available(ctx)
	if available {
		return true, ""
	}
	if reason == "" {
		reason = "The provider is not available."
	}
	return false, reason
}

// ConnectionCount reports the live and starting language server
// connections, for the settings page.
func (s *Service) ConnectionCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}
