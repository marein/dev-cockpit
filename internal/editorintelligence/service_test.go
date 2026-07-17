package editorintelligence

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestMain doubles as the fake language server: tests install a wrapper
// script named like a real server that re-executes this binary with
// GO_WANT_FAKE_LSP set, so no test depends on locally installed language
// servers.
func TestMain(m *testing.M) {
	if os.Getenv("GO_WANT_FAKE_LSP") == "1" {
		runFakeLSP(os.Getenv("FAKE_LSP_MODE"))
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runFakeLSP speaks just enough framed LSP for the tests: it tracks
// document versions and open documents, and its completion items encode
// that state, so assertions read the protocol effects from the results.
func runFakeLSP(mode string) {
	if mode == "fail-init" {
		os.Exit(3)
	}
	r := bufio.NewReader(os.Stdin)
	var writeMu sync.Mutex
	docs := map[string]int{}
	respond := func(id *json.RawMessage, result any) {
		msg := rpcMessage{JSONRPC: "2.0", ID: id, Result: mustMarshal(result)}
		payload, _ := json.Marshal(msg)
		writeMu.Lock()
		_ = writeFrame(os.Stdout, payload)
		writeMu.Unlock()
	}
	configOK := false
	var initID *json.RawMessage
	for {
		payload, err := readFrame(r)
		if err != nil {
			return
		}
		var msg rpcMessage
		if err := json.Unmarshal(payload, &msg); err != nil {
			return
		}
		switch msg.Method {
		case "initialize":
			// Exercise the client's server request answering before the
			// handshake finishes: the initialize response only goes out
			// once the client answered workspace/configuration, so the
			// order is deterministic for the assertions.
			initID = msg.ID
			id := json.RawMessage("9001")
			req := rpcMessage{JSONRPC: "2.0", ID: &id, Method: "workspace/configuration",
				Params: mustMarshal(map[string]any{"items": []map[string]any{{"section": "test"}}})}
			raw, _ := json.Marshal(req)
			writeMu.Lock()
			_ = writeFrame(os.Stdout, raw)
			writeMu.Unlock()
		case "initialized":
		case "textDocument/didOpen":
			var params struct {
				TextDocument struct {
					URI     string `json:"uri"`
					Version int    `json:"version"`
				} `json:"textDocument"`
			}
			_ = json.Unmarshal(msg.Params, &params)
			docs[params.TextDocument.URI] = params.TextDocument.Version
		case "textDocument/didChange":
			var params struct {
				TextDocument struct {
					URI     string `json:"uri"`
					Version int    `json:"version"`
				} `json:"textDocument"`
			}
			_ = json.Unmarshal(msg.Params, &params)
			docs[params.TextDocument.URI] = params.TextDocument.Version
		case "textDocument/didClose":
			var params struct {
				TextDocument struct {
					URI string `json:"uri"`
				} `json:"textDocument"`
			}
			_ = json.Unmarshal(msg.Params, &params)
			delete(docs, params.TextDocument.URI)
		case "textDocument/completion":
			if mode == "crash-on-completion" {
				os.Exit(4)
			}
			if mode == "hang" {
				continue
			}
			var params struct {
				TextDocument struct {
					URI string `json:"uri"`
				} `json:"textDocument"`
			}
			_ = json.Unmarshal(msg.Params, &params)
			items := []map[string]any{
				{"label": fmt.Sprintf("docv%d", docs[params.TextDocument.URI])},
				{"label": fmt.Sprintf("open%d", len(docs))},
				{"label": fmt.Sprintf("config%v", configOK)},
			}
			respond(msg.ID, map[string]any{"isIncomplete": false, "items": items})
		case "shutdown":
			respond(msg.ID, nil)
		case "exit":
			os.Exit(0)
		default:
			if msg.ID != nil && msg.Method == "" && initID != nil {
				// The configuration response: one null per requested item.
				configOK = strings.TrimSpace(string(msg.Result)) == "[null]"
				respond(initID, map[string]any{"capabilities": map[string]any{}})
				initID = nil
			}
		}
	}
}

// installFakeLSP puts wrapper scripts for the given command names on PATH,
// each re-executing the test binary as the fake server.
func installFakeLSP(t *testing.T, mode string, commands ...string) {
	t.Helper()
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range commands {
		script := fmt.Sprintf("#!/bin/sh\nexec env GO_WANT_FAKE_LSP=1 FAKE_LSP_MODE=%s %q\n", mode, exe)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func newTestService(t *testing.T, settings Settings) *Service {
	t.Helper()
	s := New(t.TempDir())
	t.Cleanup(s.Close)
	s.Config.Save(settings)
	return s
}

// goRequest builds a request whose cursor sits at the start of the second
// line, so the empty typed prefix keeps every fake completion item.
func goRequest(t *testing.T, version int) Request {
	t.Helper()
	return Request{
		Client:      "client-a",
		ProjectName: "proj",
		ProjectRoot: t.TempDir(),
		Path:        "main.go",
		Version:     version,
		Content:     "package main\n",
		Line:        1,
		Character:   0,
		WantLSP:     true,
	}
}

func labels(items []Item) string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Label)
	}
	return strings.Join(out, ",")
}

func TestServiceModeOff(t *testing.T) {
	s := newTestService(t, defaultSettings())
	resp, err := s.Complete(context.Background(), goRequest(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	if resp.LSP.Available || resp.LSP.Status != StatusDisabled {
		t.Fatalf("mode off: %+v", resp.LSP)
	}
}

func TestServiceUnknownLanguage(t *testing.T) {
	settings := defaultSettings()
	settings.Mode = ModeLSP
	s := newTestService(t, settings)
	req := goRequest(t, 1)
	req.Path = "readme.txt"
	resp, _ := s.Complete(context.Background(), req)
	if resp.LSP.Status != StatusNoLanguage {
		t.Fatalf("status %q", resp.LSP.Status)
	}
}

func TestServiceProfileDisabled(t *testing.T) {
	settings := defaultSettings()
	settings.Mode = ModeLSP
	settings.DisabledProfiles = []string{"go"}
	s := newTestService(t, settings)
	resp, _ := s.Complete(context.Background(), goRequest(t, 1))
	if resp.LSP.Status != StatusDisabled {
		t.Fatalf("status %q", resp.LSP.Status)
	}
}

func TestServiceNotInstalled(t *testing.T) {
	settings := defaultSettings()
	settings.Mode = ModeLSP
	s := newTestService(t, settings)
	t.Setenv("PATH", t.TempDir())
	resp, _ := s.Complete(context.Background(), goRequest(t, 1))
	if resp.LSP.Status != StatusNotInstalled {
		t.Fatalf("status %q", resp.LSP.Status)
	}
}

func TestServiceInvalidPosition(t *testing.T) {
	s := newTestService(t, defaultSettings())
	req := goRequest(t, 1)
	req.Line = 99
	if _, err := s.Complete(context.Background(), req); err == nil {
		t.Fatal("expected position error")
	}
}

func TestServiceCompletionFlow(t *testing.T) {
	installFakeLSP(t, "normal", "gopls")
	settings := defaultSettings()
	settings.Mode = ModeLSP
	s := newTestService(t, settings)

	req := goRequest(t, 1)
	resp, err := s.Complete(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.LSP.Available {
		t.Fatalf("unavailable: %+v", resp.LSP)
	}
	if got := labels(resp.LSP.Items); got != "configtrue,docv1,open1" {
		t.Fatalf("items %q", got)
	}
	if resp.LSP.From != 13 {
		t.Fatalf("from %d", resp.LSP.From)
	}

	// A newer version syncs via didChange on the same connection. The
	// cursor stays at the line start, so no typed prefix filters the fake
	// items away.
	req.Version = 5
	req.Content = "package main\nx"
	resp, _ = s.Complete(context.Background(), req)
	if !resp.LSP.Available || !strings.Contains(labels(resp.LSP.Items), "docv5") {
		t.Fatalf("after change: %+v", resp.LSP)
	}
	if s.ConnectionCount() != 1 {
		t.Fatalf("connections %d", s.ConnectionCount())
	}

	// An older snapshot must never overwrite newer text.
	req.Version = 2
	resp, _ = s.Complete(context.Background(), req)
	if resp.LSP.Available || resp.LSP.Status != StatusStale {
		t.Fatalf("stale: %+v", resp.LSP)
	}

	// A second document on the same connection, then closing it again is
	// visible in the open count.
	req2 := goRequest(t, 1)
	req2.ProjectRoot = req.ProjectRoot
	req2.Path = "other.go"
	resp, _ = s.Complete(context.Background(), req2)
	if !strings.Contains(labels(resp.LSP.Items), "open2") {
		t.Fatalf("second doc: %+v", resp.LSP)
	}
	s.CloseDocument(req.Client, req.ProjectName, "other.go")
	req.Version = 6
	resp, _ = s.Complete(context.Background(), req)
	if !strings.Contains(labels(resp.LSP.Items), "open1") {
		t.Fatalf("after close: %+v", resp.LSP)
	}
}

func TestServiceCrashIsolationAndBackoff(t *testing.T) {
	installFakeLSP(t, "crash-on-completion", "gopls")
	settings := defaultSettings()
	settings.Mode = ModeLSP
	s := newTestService(t, settings)

	resp, _ := s.Complete(context.Background(), goRequest(t, 1))
	if resp.LSP.Available || resp.LSP.Status != StatusError {
		t.Fatalf("crash: %+v", resp.LSP)
	}
	if s.ConnectionCount() != 0 {
		t.Fatalf("dead connection kept: %d", s.ConnectionCount())
	}
	resp, _ = s.Complete(context.Background(), goRequest(t, 2))
	if resp.LSP.Status != StatusUnavailable {
		t.Fatalf("backoff: %+v", resp.LSP)
	}
}

func TestServiceCancellation(t *testing.T) {
	installFakeLSP(t, "hang", "gopls")
	settings := defaultSettings()
	settings.Mode = ModeLSP
	s := newTestService(t, settings)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	resp, _ := s.Complete(ctx, goRequest(t, 1))
	if resp.LSP.Status != StatusCanceled {
		t.Fatalf("canceled: %+v", resp.LSP)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("cancellation hung")
	}
}

func TestServiceIdleExpiry(t *testing.T) {
	installFakeLSP(t, "normal", "gopls")
	settings := defaultSettings()
	settings.Mode = ModeLSP
	s := newTestService(t, settings)

	if resp, _ := s.Complete(context.Background(), goRequest(t, 1)); !resp.LSP.Available {
		t.Fatalf("setup completion failed: %+v", resp.LSP)
	}
	s.mu.Lock()
	var conn *lspConn
	for _, mc := range s.conns {
		conn = mc.conn
	}
	s.mu.Unlock()

	s.now = func() time.Time { return time.Now().Add(10 * time.Minute) }
	s.expireIdle()
	if s.ConnectionCount() != 0 {
		t.Fatalf("idle connection kept: %d", s.ConnectionCount())
	}
	deadline := time.Now().Add(5 * time.Second)
	for conn.alive() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if conn.alive() {
		t.Fatal("expired connection still alive")
	}
	<-conn.exited
}

func TestServiceCloseShutsProcessesDown(t *testing.T) {
	installFakeLSP(t, "normal", "gopls")
	settings := defaultSettings()
	settings.Mode = ModeLSP
	s := New(t.TempDir())
	s.Config.Save(settings)

	if resp, _ := s.Complete(context.Background(), goRequest(t, 1)); !resp.LSP.Available {
		t.Fatal("setup completion failed")
	}
	s.mu.Lock()
	var conn *lspConn
	for _, mc := range s.conns {
		conn = mc.conn
	}
	s.mu.Unlock()

	s.Close()
	select {
	case <-conn.exited:
	default:
		t.Fatal("process not reaped after Close")
	}
	if s.ConnectionCount() != 0 {
		t.Fatalf("connections after Close: %d", s.ConnectionCount())
	}
}

// fakeProvider records the FIM request and returns canned output.
type fakeProvider struct {
	mu       sync.Mutex
	requests []FIMRequest
	insert   string
	err      error
}

func (f *fakeProvider) Available(context.Context) (bool, string) { return true, "" }

func (f *fakeProvider) Complete(ctx context.Context, req FIMRequest) (string, error) {
	f.mu.Lock()
	f.requests = append(f.requests, req)
	f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return f.insert, f.err
}

func aiSettings() Settings {
	settings := defaultSettings()
	settings.Mode = ModeLSPAI
	settings.Ollama = OllamaSettings{Enabled: true, Model: "qwen2.5-coder"}
	return settings
}

func aiRequest(t *testing.T) Request {
	t.Helper()
	req := goRequest(t, 1)
	req.WantLSP = false
	req.WantAI = true
	return req
}

func TestServiceAIComplete(t *testing.T) {
	s := newTestService(t, aiSettings())
	provider := &fakeProvider{insert: "fmt.Println(\"hi\")"}
	s.newProvider = func(model string) CompletionProvider { return provider }

	resp, err := s.Complete(context.Background(), aiRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	if !resp.AI.Available || resp.AI.Insert != "fmt.Println(\"hi\")" || resp.AI.Detail != "Ollama" {
		t.Fatalf("ai: %+v", resp.AI)
	}
	if len(provider.requests) != 1 || provider.requests[0].Prefix != "package main\n" || provider.requests[0].Language != "go" {
		t.Fatalf("fim request: %+v", provider.requests)
	}
}

func TestServiceAIDisabledModes(t *testing.T) {
	settings := aiSettings()
	settings.Mode = ModeLSP
	s := newTestService(t, settings)
	s.newProvider = func(string) CompletionProvider {
		t.Fatal("provider must not be called")
		return nil
	}
	resp, _ := s.Complete(context.Background(), aiRequest(t))
	if resp.AI.Status != StatusDisabled {
		t.Fatalf("status %q", resp.AI.Status)
	}
}

func TestServiceAISensitivePathWithheld(t *testing.T) {
	s := newTestService(t, aiSettings())
	s.newProvider = func(string) CompletionProvider {
		t.Fatal("provider must not see sensitive content")
		return nil
	}
	req := aiRequest(t)
	req.Path = ".env"
	req.Content = "SECRET=1\n"
	resp, _ := s.Complete(context.Background(), req)
	if resp.AI.Available || resp.AI.Status != StatusWithheld {
		t.Fatalf("withheld: %+v", resp.AI)
	}
}

func TestServiceAIErrorBackoff(t *testing.T) {
	s := newTestService(t, aiSettings())
	provider := &fakeProvider{err: fmt.Errorf("boom")}
	s.newProvider = func(string) CompletionProvider { return provider }

	resp, _ := s.Complete(context.Background(), aiRequest(t))
	if resp.AI.Status != StatusUnavailable {
		t.Fatalf("error: %+v", resp.AI)
	}
	resp, _ = s.Complete(context.Background(), aiRequest(t))
	if resp.AI.Status != StatusUnavailable || len(provider.requests) != 1 {
		t.Fatalf("backoff must skip the provider: %+v, %d calls", resp.AI, len(provider.requests))
	}
}

func TestServiceAIEmptyResult(t *testing.T) {
	s := newTestService(t, aiSettings())
	s.newProvider = func(string) CompletionProvider { return &fakeProvider{insert: ""} }
	resp, _ := s.Complete(context.Background(), aiRequest(t))
	if resp.AI.Available || resp.AI.Status != StatusEmpty {
		t.Fatalf("empty: %+v", resp.AI)
	}
}

func TestServicePerClientLimit(t *testing.T) {
	installFakeLSP(t, "normal", "gopls", "intelephense", "pyright-langserver", "vscode-html-language-server")
	settings := defaultSettings()
	settings.Mode = ModeLSP
	s := newTestService(t, settings)

	root := t.TempDir()
	paths := []string{"a.go", "b.php", "c.py", "d.html"}
	var last Response
	for _, p := range paths {
		req := goRequest(t, 1)
		req.ProjectRoot = root
		req.Path = p
		last, _ = s.Complete(context.Background(), req)
	}
	if last.LSP.Status != StatusBusy {
		t.Fatalf("fourth profile must hit the per client limit: %+v", last.LSP)
	}
	if s.ConnectionCount() != maxConnectionsPerClient {
		t.Fatalf("connections %d", s.ConnectionCount())
	}
}
