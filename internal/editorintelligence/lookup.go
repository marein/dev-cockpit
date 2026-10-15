package editorintelligence

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"
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
	_, _, status, err := s.ask(ctx, req, methodHover, func(answer json.RawMessage) (bool, error) {
		text, span, err := hoverText(answer)
		res.Name, res.Range = profile.ClassName(text), span
		return text == "", err
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
	mc, profile, status, err := s.ask(ctx, req, method, func(answer json.RawMessage) (bool, error) {
		locs, err := decodeLocations(answer)
		raw = locs
		return len(locs) == 0, err
	})
	if err != nil || status != "" {
		return unavailable(status), err
	}
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

// ask runs one request about the position on the project's server and hands
// the answer to read, which reports whether it was empty. What surrounds the
// request is the same for every lookup: the connection, the document the
// position describes, the wait for the index and the retries of an empty
// answer while the server is still getting going. It answers the connection
// and profile the request ran on, or the status that stood in the way; an
// error is invalid input the handler should reject.
func (s *Service) ask(ctx context.Context, req Request, method string, read func(json.RawMessage) (bool, error)) (*managedConn, *Profile, string, error) {
	doc := newDocText(req.Content)
	if !doc.validPosition(req.Line, req.Character) {
		return nil, nil, "", errors.New("position is outside the document")
	}
	profile, langID, ok := ProfileForPath(req.Path)
	if !ok {
		return nil, nil, StatusNoLanguage, nil
	}
	backoff := backoffKey(req.ProjectName, profile)
	if s.inBackoff(backoff) {
		return nil, nil, StatusUnavailable, nil
	}
	mc, status := s.connFor(ctx, req.ProjectName, req.ProjectRoot, profile, req.Launcher, s.now())
	if status != "" {
		return nil, nil, status, nil
	}

	budgetCtx, cancelBudget := context.WithTimeout(ctx, s.callBudget)
	defer cancelBudget()
	callCtx, token := mc.beginCall(budgetCtx, req.Client, req.Path, method)
	defer mc.endCall(req.Client, req.Path, method, token)
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
	for attempt := 0; err == nil; attempt++ {
		var id int64
		var ch chan rpcMessage
		mc.conn.docMu.Lock()
		err = mc.conn.ensureDocument(req.Client, req.Path, langID, req.Content)
		if err == nil {
			id, ch, err = mc.conn.startAt(method, req.Path, req.Line, req.Character)
		}
		mc.conn.docMu.Unlock()
		if err != nil {
			break
		}
		var answer json.RawMessage
		empty := false
		if err = mc.conn.awaitCall(callCtx, id, ch, &answer); err == nil {
			empty, err = read(answer)
		}
		if err != nil || !empty {
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
		return mc, profile, "", nil
	}
	if budgetCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
		log.Printf("editor intelligence: %s %s got no answer within %s", profile.ID, method, s.callBudget)
		return nil, nil, StatusError, nil
	}
	if callCtx.Err() != nil {
		return nil, nil, StatusCanceled, nil
	}
	log.Printf("editor intelligence: %s %s failed: %v", profile.ID, method, err)
	// A death that is the launcher's restart wish is routine, the workspace
	// watcher's doing: watchRestart swaps the replacement into the slot, so
	// the slot stays for it and the project stays out of the error backoff.
	if !mc.conn.alive() && !s.exitWasRestartWish(mc) {
		s.dropConn(req.ProjectName, profile, mc)
		s.setBackoff(backoff, lspErrorBackoff)
	}
	return nil, nil, StatusError, nil
}
