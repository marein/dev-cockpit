package editorintelligence

import (
	"context"
	"encoding/json"
	"errors"
	"log"
)

// The methods a request can run: the two navigations and the hover a class
// name is read off.
const (
	methodDefinition = "textDocument/definition"
	methodReferences = "textDocument/references"
	methodHover      = "textDocument/hover"
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
	// Implementations are the types implementing or extending the interface
	// a usages lookup stands on the declaration of, a group of their own: no
	// line of theirs stands among the usages too. ImplementationsTruncated
	// says the group was cut like Truncated says it of the usages.
	Implementations          []Location `json:"implementations,omitempty"`
	ImplementationsTruncated bool       `json:"implementationsTruncated,omitempty"`
	// Subclasses are the classes extending the class a usages lookup stands
	// on the declaration of, a group of their own like the implementations.
	Subclasses          []Location `json:"subclasses,omitempty"`
	SubclassesTruncated bool       `json:"subclassesTruncated,omitempty"`
	// Indirect are the types below the asked one through another one of
	// them, in the group Below names and apart from it: a class extending
	// an implementation, a subclass's subclass. IndirectTruncated says the
	// walk to them or the list was cut.
	Indirect          []Location `json:"indirect,omitempty"`
	IndirectTruncated bool       `json:"indirectTruncated,omitempty"`
	// Below names the group the types below the asked one form, empty
	// when the asked symbol is no type with such a group: an answer whose
	// direct ones are empty still names the group its indirect ones stand
	// beneath.
	Below string `json:"below,omitempty"`
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

// NameResult is one class name answer: the fully qualified name of the
// class the symbol at the position names, empty when it names none, and the
// symbol's range as the server gave it, 0-based like the request's
// position, so one answer covers every position of the name. An
// unavailable server travels as a status like a navigation's does.
type NameResult struct {
	Available bool      `json:"available"`
	Status    string    `json:"status,omitempty"`
	Name      string    `json:"name"`
	Range     *lspRange `json:"range,omitempty"`
}

// ClassName answers the fully qualified name of the class the symbol at the
// position names, read off the server's hover by the language's own rule. A
// language without such a rule answers like one without a server, before
// any server starts. Safe on a nil receiver, like every entry point the web
// layer calls.
func (s *Service) ClassName(ctx context.Context, req Request) (NameResult, error) {
	if s == nil {
		return NameResult{Status: StatusUnavailable}, nil
	}
	profile, _, ok := ProfileForPath(req.Path)
	if !ok || profile.ClassName == nil {
		return NameResult{Status: StatusNoLanguage}, nil
	}
	res := NameResult{Available: true}
	_, _, status, err := s.ask(ctx, req, methodHover, func(answer json.RawMessage) error {
		text, span, err := hoverText(answer)
		res.Name, res.Range = profile.ClassName(text), span
		return err
	})
	if err != nil || status != "" {
		return NameResult{Status: status}, err
	}
	return res, nil
}

// navigate runs one navigation request. It returns an error only for
// invalid input the handler should reject; server failures travel in the
// result status.
func (s *Service) navigate(ctx context.Context, req Request, method string) (Result, error) {
	var raw []lspLocation
	mc, profile, status, err := s.ask(ctx, req, method, func(answer json.RawMessage) error {
		locs, err := decodeLocations(answer)
		raw = locs
		return err
	})
	if err != nil || status != "" {
		return unavailable(status), err
	}
	roots := mc.launcher.SourceRoots(req.ProjectName, profile)
	locs, outside := mapLocations(mc.conn.rootURI, raw, roots)
	res := Result{Available: true, Outside: outside}
	switch method {
	case methodDefinition:
		res.Declaration = atRequestPosition(mc.conn.rootURI, raw, req)
	case methodReferences:
		below, status := s.typesBelow(ctx, req, profile, mc, roots, raw)
		if status != "" {
			return unavailable(status), nil
		}
		locs = withoutLines(locs, below.lines)
		below.fill(&res, req.Path)
		res.Outside += below.outside
		sortReferences(locs, req.Path)
	}
	res.Locations, res.Truncated = capLocations(locs)
	return res, nil
}

// capLocations cuts a list to maxLocations and says whether it did.
func capLocations(locs []Location) ([]Location, bool) {
	if len(locs) > maxLocations {
		return locs[:maxLocations], true
	}
	return locs, false
}

// ask runs one request about the position on the project's server and hands
// the answer to read. The document the position describes goes out before
// the wait for the index and again with the request. It answers the
// connection and profile the request ran on, or the status that stood in
// the way; an error is invalid input the handler should reject.
func (s *Service) ask(ctx context.Context, req Request, method string, read func(json.RawMessage) error) (*managedConn, *Profile, string, error) {
	doc := newDocText(req.Content)
	if !doc.validPosition(req.Line, req.Character) {
		return nil, nil, "", errors.New("position is outside the document")
	}
	profile, langID, ok := ProfileForPath(req.Path)
	if !ok {
		return nil, nil, StatusNoLanguage, nil
	}
	mc, status := s.exchange(ctx, positionExchange(req, profile, langID, method, read))
	return mc, profile, status, nil
}

// positionExchange is the exchange of one request about the request's
// position, its document synced before the wait and with the request.
func positionExchange(req Request, profile *Profile, langID, method string, read func(json.RawMessage) error) exchange {
	open := func(c *lspConn) error {
		return c.ensureDocument(req.Client, req.Path, langID, req.Content)
	}
	return exchange{
		client:   req.Client,
		project:  req.ProjectName,
		root:     req.ProjectRoot,
		launcher: req.Launcher,
		profile:  profile,
		scope:    req.Path,
		method:   method,
		open:     open,
		send: func(c *lspConn) (int64, chan rpcMessage, error) {
			if err := open(c); err != nil {
				return 0, nil, err
			}
			return c.startAt(method, req.Path, req.Line, req.Character)
		},
		read: read,
	}
}

// exchange is one request on a project's server: whom it is for, what it
// is about and how it goes out. open runs once before the wait for the
// index and send after it, both under docMu; open may be nil. scope keys the
// in flight call beside the client and the method, the document for a lookup
// at a position, a type search's request otherwise. refusable marks a
// request a server answers with an error for a position that holds nothing
// of what it asks about, which then reads as an empty answer.
type exchange struct {
	client    string
	project   string
	root      string
	launcher  Launcher
	profile   *Profile
	scope     string
	method    string
	refusable bool
	open      func(c *lspConn) error
	send      func(c *lspConn) (int64, chan rpcMessage, error)
	read      func(json.RawMessage) error
}

// exchange runs one request and hands the answer to read. What surrounds the
// request is the same for every lookup: the connection and the wait for the
// index, after which the answer is final, an empty one too. It answers the
// connection the request ran on, or the status that stood in the way.
func (s *Service) exchange(ctx context.Context, x exchange) (*managedConn, string) {
	profile := x.profile
	backoff := backoffKey(x.project, profile)
	if s.inBackoff(backoff) {
		return nil, StatusUnavailable
	}
	mc, status := s.connFor(ctx, x.project, x.root, profile, x.launcher, s.now())
	if status != "" {
		return nil, status
	}

	budgetCtx, cancelBudget := context.WithTimeout(ctx, s.callBudget)
	defer cancelBudget()
	callCtx, token := mc.beginCall(budgetCtx, x.client, x.scope, x.method)
	defer mc.endCall(x.client, x.scope, x.method, token)
	s.release(mc)

	var err error
	if x.open != nil {
		mc.conn.docMu.Lock()
		err = x.open(mc.conn)
		mc.conn.docMu.Unlock()
	}
	// The wait runs outside docMu: it may stand for a long time, and a tab
	// closing meanwhile must not queue behind it.
	if err == nil {
		err = mc.conn.waitIndexed(callCtx, indexWaitBudget)
	}
	if err == nil {
		err = x.run(callCtx, mc.conn)
	}

	if err == nil {
		s.touch(mc)
		return mc, ""
	}
	if budgetCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
		log.Printf("editor intelligence: %s %s got no answer within %s", profile.ID, x.method, s.callBudget)
		return nil, StatusError
	}
	if callCtx.Err() != nil {
		return nil, StatusCanceled
	}
	log.Printf("editor intelligence: %s %s failed: %v", profile.ID, x.method, err)
	// A death that is the launcher's restart wish is routine, the workspace
	// watcher's doing: watchRestart swaps the replacement into the slot, so
	// the slot stays for it and the project stays out of the error backoff.
	if !mc.conn.alive() && !s.exitWasRestartWish(mc) {
		s.dropConn(x.project, profile, mc)
		s.setBackoff(backoff, lspErrorBackoff)
	}
	return nil, StatusError
}

// run sends the request and hands the answer to read. The send re-syncs the
// shared document under docMu, so another client's didChange during the wait
// for the index can never make the position describe somebody else's text;
// the response wait runs outside the lock, a tab closing meanwhile never
// queues behind it.
func (x exchange) run(ctx context.Context, c *lspConn) error {
	c.docMu.Lock()
	id, ch, err := x.send(c)
	c.docMu.Unlock()
	if err != nil {
		return err
	}
	var answer json.RawMessage
	var refusal *rpcError
	if err := c.awaitCall(ctx, id, ch, &answer); err != nil {
		if x.refusable && errors.As(err, &refusal) {
			return nil
		}
		return err
	}
	return x.read(answer)
}
