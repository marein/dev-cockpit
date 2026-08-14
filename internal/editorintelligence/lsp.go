package editorintelligence

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var errConnClosed = errors.New("language server connection closed")

const (
	initializeTimeout = 15 * time.Second
	shutdownTimeout   = 3 * time.Second
	// How waitIndexed polls the progress state.
	progressPoll = 200 * time.Millisecond
)

// lspConn is one running language server process with its stdio JSON-RPC
// session and the documents opened on it.
type lspConn struct {
	profile  *Profile
	rootPath string
	rootURI  string
	// workspaceURI is what the handshake announces. It is the project for
	// every server, and the directory above it for one the cockpit hands a
	// configuration file: that file lies there, and a server only looks for
	// a configuration inside its workspace. Everything the editor is
	// answered with still goes through rootPath and rootURI, which stay the
	// project, or a location would come back relative to the wrong root.
	workspaceURI string
	initOptions  any
	// exit is the process exit code, written by the Wait owner before
	// exited closes, read only after.
	exit      int
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	startedAt time.Time

	writeMu sync.Mutex
	seq     atomic.Int64

	pendingMu sync.Mutex
	pending   map[int64]chan rpcMessage

	// docMu serializes the document lifecycle and the send of a navigation
	// request per connection: the snapshot sync and the request that
	// describes it stay adjacent on the wire, so no other client's
	// didChange slips between them, while response waits and retry sleeps
	// run outside the lock. The connection
	// is shared by every editor instance of the project, so a document
	// carries the server's own version counter and the set of clients that
	// hold it open: versions from different pages cannot be compared, text
	// can, and the document closes only when its last holder lets go.
	docMu      sync.Mutex
	docVersion map[string]int
	docText    map[string]string
	docClients map[string]map[string]bool

	// The server's announced work, standard progress notifications: which
	// tokens are between begin and end right now, whether any begin was
	// ever seen, and the last reported percentage while work is active
	// (-1 while the server reports none). All of it feeds waitIndexed and
	// the editor's indexing indicator.
	progressMu     sync.Mutex
	progressActive map[string]bool
	progressSeen   bool
	progressPct    int

	// onChange fires on every announced progress move and on exit; the
	// service turns it into the project's change event. Nil for none.
	onChange func()

	done      chan struct{}
	exited    chan struct{}
	closeOnce sync.Once
	failure   atomic.Value
}

// startLSPConn launches argv in the workspace root and completes the LSP
// initialize handshake. ctx bounds the process lifetime, not just the
// handshake; env is the launcher's extra process environment, notify the
// service's change signal.
func startLSPConn(ctx context.Context, profile *Profile, argv, env []string, root string, initOptions any, notify func()) (*lspConn, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = root
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	setChildProcAttr(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", argv[0], err)
	}
	c := &lspConn{
		profile:        profile,
		rootPath:       root,
		rootURI:        fileURI(root),
		workspaceURI:   workspaceURI(profile, root),
		initOptions:    initOptions,
		onChange:       notify,
		cmd:            cmd,
		stdin:          stdin,
		startedAt:      time.Now(),
		pending:        map[int64]chan rpcMessage{},
		docVersion:     map[string]int{},
		docText:        map[string]string{},
		docClients:     map[string]map[string]bool{},
		progressActive: map[string]bool{},
		progressPct:    -1,
		done:           make(chan struct{}),
		exited:         make(chan struct{}),
	}
	go c.logStderr(stderr)
	go c.readLoop(bufio.NewReaderSize(stdout, 64<<10))
	// The single Wait owner: it reaps the process whenever it ends and
	// flips the connection to failed, so pending calls never hang on a
	// dead server.
	go func() {
		err := cmd.Wait()
		if ee, ok := err.(*exec.ExitError); ok {
			c.exit = ee.ExitCode()
		}
		c.fail(fmt.Errorf("language server %s exited: %v", profile.ID, err))
		close(c.exited)
		// A death moves the indexing picture too.
		if c.onChange != nil {
			c.onChange()
		}
	}()

	initCtx, cancel := context.WithTimeout(ctx, initializeTimeout)
	defer cancel()
	if err := c.initialize(initCtx); err != nil {
		c.close()
		return nil, fmt.Errorf("initialize %s: %w", profile.ID, err)
	}
	return c, nil
}

// workspaceURI answers what the handshake announces as the workspace root,
// see lspConn.workspaceURI.
func workspaceURI(p *Profile, root string) string {
	if !p.container.DefaultConfig {
		return fileURI(root)
	}
	return fileURI(workspaceDir(root))
}

// fileURI builds a file:// URI with the path percent encoded, so a name
// carrying '#', '?', '%' or a space stays one path on the server's side;
// uriPath is the decoding counterpart.
func fileURI(path string) string {
	return "file://" + (&url.URL{Path: path}).EscapedPath()
}

// exitStatus is the dead process's exit code; call only after exited
// closed.
func (c *lspConn) exitStatus() int { return c.exit }

func (c *lspConn) initialize(ctx context.Context) error {
	// processId stays null on purpose: servers exit when the announced
	// parent PID does not exist, and a server wrapped into a container
	// lives in another PID namespace where our PID never exists
	// (intelephense exits within seconds then). Lifetime is owned by the
	// shutdown/exit protocol and the stdin pipe instead.
	//
	// linkSupport stays unset (false): servers then answer definition with
	// plain Locations; LocationLinks are still decoded defensively.
	params := map[string]any{
		"processId": nil,
		"rootUri":   c.workspaceURI,
		"workspaceFolders": []map[string]any{
			{"uri": c.workspaceURI, "name": "workspace"},
		},
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"synchronization": map[string]any{
					"didSave": false,
				},
				"definition": map[string]any{},
				"references": map[string]any{},
			},
			"workspace": map[string]any{
				"configuration":    true,
				"workspaceFolders": true,
			},
			// The progress capability is what makes the server announce its
			// indexing, which is what waitIndexed holds requests back on:
			// answers during that phase are real but partial.
			"window": map[string]any{
				"workDoneProgress": true,
			},
		},
	}
	if c.initOptions != nil {
		params["initializationOptions"] = c.initOptions
	}
	var result json.RawMessage
	if err := c.call(ctx, "initialize", params, &result); err != nil {
		return err
	}
	return c.notify("initialized", map[string]any{})
}

// startCall registers a pending response slot and sends the request. The
// caller owns the wait through awaitCall, which also releases the slot;
// splitting the two is what lets a navigation send happen under docMu while
// the wait runs outside it.
func (c *lspConn) startCall(method string, params any) (int64, chan rpcMessage, error) {
	id := c.seq.Add(1)
	ch := make(chan rpcMessage, 1)
	c.pendingMu.Lock()
	c.pending[id] = ch
	c.pendingMu.Unlock()
	if err := c.send(rpcMessage{JSONRPC: "2.0", ID: rawID(id), Method: method, Params: mustMarshal(params)}); err != nil {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return 0, nil, err
	}
	return id, ch, nil
}

// awaitCall waits for the response of a startCall. A cancelled ctx sends
// $/cancelRequest and returns immediately.
func (c *lspConn) awaitCall(ctx context.Context, id int64, ch chan rpcMessage, result any) error {
	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
	}()
	select {
	case <-ctx.Done():
		_ = c.notify("$/cancelRequest", map[string]any{"id": id})
		return ctx.Err()
	case <-c.done:
		return c.closeErr()
	case resp := <-ch:
		if resp.Error != nil {
			return resp.Error
		}
		if result != nil && len(resp.Result) > 0 {
			return json.Unmarshal(resp.Result, result)
		}
		return nil
	}
}

// call sends a request and waits for its response.
func (c *lspConn) call(ctx context.Context, method string, params, result any) error {
	id, ch, err := c.startCall(method, params)
	if err != nil {
		return err
	}
	return c.awaitCall(ctx, id, ch, result)
}

func (c *lspConn) notify(method string, params any) error {
	return c.send(rpcMessage{JSONRPC: "2.0", Method: method, Params: mustMarshal(params)})
}

func (c *lspConn) send(msg rpcMessage) error {
	payload, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if len(payload) > maxFrameBytes {
		return errors.New("outgoing frame exceeds the limit")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	select {
	case <-c.done:
		return c.closeErr()
	default:
	}
	return writeFrame(c.stdin, payload)
}

func mustMarshal(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return raw
}

func (c *lspConn) readLoop(r *bufio.Reader) {
	for {
		payload, err := readFrame(r)
		if err != nil {
			c.fail(fmt.Errorf("read from %s: %w", c.profile.ID, err))
			return
		}
		var msg rpcMessage
		if err := json.Unmarshal(payload, &msg); err != nil {
			c.fail(fmt.Errorf("malformed frame from %s: %w", c.profile.ID, err))
			return
		}
		switch {
		case msg.ID != nil && msg.Method == "":
			c.dispatchResponse(msg)
		case msg.ID != nil:
			c.answerServerRequest(msg)
		case msg.Method == "$/progress":
			c.trackProgress(msg.Params)
		default:
			// Other server notifications (diagnostics, logs) are not
			// surfaced.
		}
	}
}

func (c *lspConn) dispatchResponse(msg rpcMessage) {
	var id int64
	if err := json.Unmarshal(*msg.ID, &id); err != nil {
		return
	}
	c.pendingMu.Lock()
	ch := c.pending[id]
	c.pendingMu.Unlock()
	if ch != nil {
		// Non blocking: a peer answering the same id twice must not wedge
		// the read loop, the duplicate is dropped.
		select {
		case ch <- msg:
		default:
		}
	}
}

// answerServerRequest keeps servers happy that call back into the client.
// Every answer is the neutral default; unknown methods get MethodNotFound.
func (c *lspConn) answerServerRequest(msg rpcMessage) {
	resp := rpcMessage{JSONRPC: "2.0", ID: msg.ID}
	switch msg.Method {
	case "workspace/configuration":
		var params struct {
			Items []json.RawMessage `json:"items"`
		}
		_ = json.Unmarshal(msg.Params, &params)
		nulls := make([]json.RawMessage, len(params.Items))
		for i := range nulls {
			nulls[i] = json.RawMessage("null")
		}
		resp.Result = mustMarshal(nulls)
	case "client/registerCapability", "client/unregisterCapability",
		"window/workDoneProgress/create", "window/showMessageRequest":
		resp.Result = json.RawMessage("null")
	case "workspace/applyEdit":
		resp.Result = mustMarshal(map[string]any{"applied": false})
	default:
		resp.Error = &rpcError{Code: -32601, Message: "method not supported"}
	}
	if err := c.send(resp); err != nil {
		log.Printf("editor intelligence: answer %s to %s: %v", msg.Method, c.profile.ID, err)
	}
}

// trackProgress follows the server's announced work: a begin marks its
// token active, the end takes it down again, and the percentage riding on
// begin and report frames is remembered for the indexing indicator. The
// token may be a string or a number on the wire, so its raw bytes are the
// key.
func (c *lspConn) trackProgress(params json.RawMessage) {
	var p struct {
		Token json.RawMessage `json:"token"`
		Value struct {
			Kind       string `json:"kind"`
			Percentage *int   `json:"percentage"`
		} `json:"value"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return
	}
	token := string(p.Token)
	changed := false
	c.progressMu.Lock()
	switch p.Value.Kind {
	case "begin", "report":
		if p.Value.Kind == "begin" {
			c.progressActive[token] = true
			c.progressSeen = true
			changed = true
		}
		if p.Value.Percentage != nil && *p.Value.Percentage != c.progressPct {
			c.progressPct = *p.Value.Percentage
			changed = true
		}
	case "end":
		delete(c.progressActive, token)
		if len(c.progressActive) == 0 {
			c.progressPct = -1
		}
		changed = true
	}
	c.progressMu.Unlock()
	if changed && c.onChange != nil {
		c.onChange()
	}
}

// warming reports whether the connection is still in the stretch where an
// answer may be short because the server has not got going yet: it indexes
// the workspace at the handshake and announces that run seconds late, so
// until the announcement arrives its silence cannot be told from a server
// already deep in a run and is waited out.
//
// A server that announces no startup work (Profile.SilentStart) has no such
// stretch: it is ready when it answers, and waiting its silence out would
// hold every lookup for the whole window against an announcement that is
// never coming.
func (c *lspConn) warming(grace time.Duration) bool {
	if c.profile.SilentStart {
		return false
	}
	return time.Since(c.startedAt) < grace
}

// progress answers whether announced work is running, whether any begin was
// ever seen, and the last percentage it reported, -1 while it reports none.
func (c *lspConn) progress() (busy, seen bool, pct int) {
	c.progressMu.Lock()
	defer c.progressMu.Unlock()
	return len(c.progressActive) > 0, c.progressSeen, c.progressPct
}

// waitIndexed holds a navigation request back while the server indexes the
// workspace. Answers during that phase are real but partial, references
// most of all, and a partial answer is not empty, so the empty-answer retry
// never catches it. The indexing travels as standard progress notifications
// (the initialize declares the capability), but the announcement itself
// arrives only seconds after the handshake, and on a loaded host later
// still, so the whole warming window doubles as the wait for it: a
// connection that has announced nothing counts as warming for `grace` after
// process start. Every supported server announces, and the test fakes
// announce an already finished run right after the handshake, so only a
// truly silent server ever sits this wait out. Past the budget the request
// goes out and answers what is indexed by then.
//
// Announced work is waited out whoever announces it: a server that reports
// none at startup can still announce a run of its own later, and a lookup
// during it would be answered out of half a picture.
func (c *lspConn) waitIndexed(ctx context.Context, grace, budget time.Duration) error {
	deadline := c.startedAt.Add(budget)
	for {
		busy, seen, _ := c.progress()
		if !busy && (seen || !c.warming(grace)) {
			return nil
		}
		if time.Now().After(deadline) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.done:
			return c.closeErr()
		case <-time.After(progressPoll):
		}
	}
}

func (c *lspConn) logStderr(stderr io.Reader) {
	scanner := bufio.NewScanner(stderr)
	scanner.Buffer(make([]byte, 0, 64<<10), 64<<10)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			log.Printf("lsp %s: %s", c.profile.ID, line)
		}
	}
}

// fail marks the connection broken and unblocks every pending call. The
// closed stdin makes a well behaved server exit on its own.
func (c *lspConn) fail(err error) {
	c.closeOnce.Do(func() {
		c.failure.Store(err)
		close(c.done)
		_ = c.stdin.Close()
	})
}

func (c *lspConn) closeErr() error {
	if err, ok := c.failure.Load().(error); ok && err != nil {
		return fmt.Errorf("%w: %w", errConnClosed, err)
	}
	return errConnClosed
}

func (c *lspConn) alive() bool {
	select {
	case <-c.done:
		return false
	default:
		return true
	}
}

// close shuts the server down along the protocol (shutdown request, exit
// notification) and kills the process when it does not comply in time.
func (c *lspConn) close() {
	if c.alive() {
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		_ = c.call(ctx, "shutdown", nil, nil)
		_ = c.notify("exit", nil)
		cancel()
	}
	c.fail(errConnClosed)
	select {
	case <-c.exited:
	case <-time.After(shutdownTimeout):
		_ = c.cmd.Process.Kill()
		<-c.exited
	}
}

// ensureDocument opens or updates the document on the shared server and
// remembers that this client holds it. The version is the server side
// counter, because clients cannot be compared against each other; text can,
// so an unchanged snapshot costs nothing and a changed one is one didChange.
// The caller holds docMu.
func (c *lspConn) ensureDocument(client, rel, langID, text string) error {
	uri := c.docURI(rel)
	_, open := c.docVersion[rel]
	switch {
	case !open:
		if err := c.notify("textDocument/didOpen", map[string]any{
			"textDocument": map[string]any{
				"uri":        uri,
				"languageId": langID,
				"version":    1,
				"text":       text,
			},
		}); err != nil {
			return err
		}
		c.docVersion[rel] = 1
		c.docText[rel] = text
		c.docClients[rel] = map[string]bool{}
	case text != c.docText[rel]:
		version := c.docVersion[rel] + 1
		if err := c.notify("textDocument/didChange", map[string]any{
			"textDocument":   map[string]any{"uri": uri, "version": version},
			"contentChanges": []map[string]any{{"text": text}},
		}); err != nil {
			return err
		}
		c.docVersion[rel] = version
		c.docText[rel] = text
	}
	c.docClients[rel][client] = true
	return nil
}

// closeDocument lets one client go of the document and sends didClose only
// when nobody holds it any more, so the server reads the disk again instead
// of a buffer someone else discarded. The caller holds docMu.
func (c *lspConn) closeDocument(client, rel string) {
	holders, open := c.docClients[rel]
	if !open {
		return
	}
	delete(holders, client)
	if len(holders) > 0 {
		return
	}
	delete(c.docVersion, rel)
	delete(c.docText, rel)
	delete(c.docClients, rel)
	_ = c.notify("textDocument/didClose", map[string]any{
		"textDocument": map[string]any{"uri": c.docURI(rel)},
	})
}

// docURI is the URI of one document the editor addresses, project relative
// or a source outside the project, see documentPath. A file the workspace
// does not hold is one the server knows from its own index, and the paths
// agree inside the container and out, which is what the cache bind is for.
func (c *lspConn) docURI(p string) string {
	return fileURI(documentPath(c.rootPath, p))
}

// startLocations sends one navigation request. method is
// "textDocument/definition" or "textDocument/references"; the references
// call includes the declaration, so the panel shows where the symbol lives
// along with where it is used. The caller holds docMu, so the snapshot the
// request describes and the request itself stay adjacent on the wire.
func (c *lspConn) startLocations(method, rel string, line, char int) (int64, chan rpcMessage, error) {
	params := map[string]any{
		"textDocument": map[string]any{"uri": c.docURI(rel)},
		"position":     map[string]any{"line": line, "character": char},
	}
	if method == methodReferences {
		params["context"] = map[string]any{"includeDeclaration": true}
	}
	return c.startCall(method, params)
}

// awaitLocations waits a navigation answer out, outside any lock.
func (c *lspConn) awaitLocations(ctx context.Context, id int64, ch chan rpcMessage) ([]lspLocation, error) {
	var raw json.RawMessage
	if err := c.awaitCall(ctx, id, ch, &raw); err != nil {
		return nil, err
	}
	return decodeLocations(raw)
}
