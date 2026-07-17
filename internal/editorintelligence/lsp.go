package editorintelligence

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	errConnClosed = errors.New("language server connection closed")
	errStale      = errors.New("document version is stale")
)

const (
	initializeTimeout = 15 * time.Second
	shutdownTimeout   = 3 * time.Second
)

// lspConn is one running language server process with its stdio JSON-RPC
// session and the documents opened on it.
type lspConn struct {
	profile   *Profile
	rootURI   string
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	startedAt time.Time

	writeMu sync.Mutex
	seq     atomic.Int64

	pendingMu sync.Mutex
	pending   map[int64]chan rpcMessage

	// docMu serializes document lifecycle and completion calls per
	// connection, keeping didOpen/didChange ordering intact.
	docMu sync.Mutex
	docs  map[string]int

	done      chan struct{}
	exited    chan struct{}
	closeOnce sync.Once
	failure   atomic.Value
}

// startLSPConn launches argv in the workspace root and completes the LSP
// initialize handshake. ctx bounds the process lifetime, not just the
// handshake.
func startLSPConn(ctx context.Context, profile *Profile, argv []string, root string) (*lspConn, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = root
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
		profile:   profile,
		rootURI:   fileURI(root),
		cmd:       cmd,
		stdin:     stdin,
		startedAt: time.Now(),
		pending:   map[int64]chan rpcMessage{},
		docs:      map[string]int{},
		done:      make(chan struct{}),
		exited:    make(chan struct{}),
	}
	go c.logStderr(stderr)
	go c.readLoop(bufio.NewReaderSize(stdout, 64<<10))
	// The single Wait owner: it reaps the process whenever it ends and
	// flips the connection to failed, so pending calls never hang on a
	// dead server.
	go func() {
		err := cmd.Wait()
		c.fail(fmt.Errorf("language server %s exited: %v", profile.ID, err))
		close(c.exited)
	}()

	initCtx, cancel := context.WithTimeout(ctx, initializeTimeout)
	defer cancel()
	if err := c.initialize(initCtx); err != nil {
		c.close()
		return nil, fmt.Errorf("initialize %s: %w", profile.ID, err)
	}
	return c, nil
}

func fileURI(path string) string {
	return "file://" + path
}

func (c *lspConn) initialize(ctx context.Context) error {
	// processId stays null on purpose: servers exit when the announced
	// parent PID does not exist, and a server wrapped into a container
	// lives in another PID namespace where our PID never exists
	// (intelephense exits within seconds then). Lifetime is owned by the
	// shutdown/exit protocol and the stdin pipe instead.
	params := map[string]any{
		"processId": nil,
		"rootUri":   c.rootURI,
		"workspaceFolders": []map[string]any{
			{"uri": c.rootURI, "name": "workspace"},
		},
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"synchronization": map[string]any{
					"didSave": false,
				},
				"completion": map[string]any{
					"contextSupport": true,
					"completionItem": map[string]any{
						"snippetSupport":      false,
						"documentationFormat": []string{"plaintext", "markdown"},
					},
				},
			},
			"workspace": map[string]any{
				"configuration":    true,
				"workspaceFolders": true,
			},
		},
	}
	var result json.RawMessage
	if err := c.call(ctx, "initialize", params, &result); err != nil {
		return err
	}
	return c.notify("initialized", map[string]any{})
}

// call sends a request and waits for its response. A cancelled ctx sends
// $/cancelRequest and returns immediately.
func (c *lspConn) call(ctx context.Context, method string, params, result any) error {
	id := c.seq.Add(1)
	ch := make(chan rpcMessage, 1)
	c.pendingMu.Lock()
	c.pending[id] = ch
	c.pendingMu.Unlock()
	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
	}()

	if err := c.send(rpcMessage{JSONRPC: "2.0", ID: rawID(id), Method: method, Params: mustMarshal(params)}); err != nil {
		return err
	}
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
		default:
			// Server notifications (diagnostics, progress, logs) are not
			// surfaced yet.
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
		ch <- msg
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

// ensureDocument opens or updates the document on the server. Versions only
// move forward; a stale version is rejected so an old snapshot can never
// overwrite newer text. The caller holds docMu.
func (c *lspConn) ensureDocument(rel, langID string, version int, text string) error {
	uri := c.docURI(rel)
	current, open := c.docs[rel]
	switch {
	case !open:
		if err := c.notify("textDocument/didOpen", map[string]any{
			"textDocument": map[string]any{
				"uri":        uri,
				"languageId": langID,
				"version":    version,
				"text":       text,
			},
		}); err != nil {
			return err
		}
	case version < current:
		return errStale
	case version > current:
		if err := c.notify("textDocument/didChange", map[string]any{
			"textDocument":   map[string]any{"uri": uri, "version": version},
			"contentChanges": []map[string]any{{"text": text}},
		}); err != nil {
			return err
		}
	default:
		return nil
	}
	c.docs[rel] = version
	return nil
}

// closeDocument sends didClose when the document is open. The caller holds
// docMu.
func (c *lspConn) closeDocument(rel string) {
	if _, open := c.docs[rel]; !open {
		return
	}
	delete(c.docs, rel)
	_ = c.notify("textDocument/didClose", map[string]any{
		"textDocument": map[string]any{"uri": c.docURI(rel)},
	})
}

func (c *lspConn) docURI(rel string) string {
	return c.rootURI + "/" + rel
}

// completion issues textDocument/completion and returns the raw items.
// Trigger kind 1 (invoked) covers both typing and the explicit shortcut,
// kind 2 is reserved for server declared trigger characters the editor does
// not use.
func (c *lspConn) completion(ctx context.Context, rel string, line, char int) ([]lspCompletionItem, bool, error) {
	var raw json.RawMessage
	err := c.call(ctx, "textDocument/completion", map[string]any{
		"textDocument": map[string]any{"uri": c.docURI(rel)},
		"position":     map[string]any{"line": line, "character": char},
		"context":      map[string]any{"triggerKind": 1},
	}, &raw)
	if err != nil {
		return nil, false, err
	}
	return decodeCompletionResult(raw)
}

func decodeCompletionResult(raw json.RawMessage) ([]lspCompletionItem, bool, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, false, nil
	}
	if strings.HasPrefix(trimmed, "[") {
		var items []lspCompletionItem
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, false, err
		}
		return items, false, nil
	}
	var list struct {
		IsIncomplete bool                `json:"isIncomplete"`
		Items        []lspCompletionItem `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, false, err
	}
	return list.Items, list.IsIncomplete, nil
}
