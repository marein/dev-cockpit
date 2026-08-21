package editorintelligence

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
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
// document versions and open documents, and its navigation answers encode
// that state in the line numbers, so assertions read the protocol effects
// from the results.
func runFakeLSP(mode string) {
	if mode == "fail-init" {
		os.Exit(3)
	}
	r := bufio.NewReader(os.Stdin)
	var writeMu sync.Mutex
	docs := map[string]int{}
	rootURI := ""
	respond := func(id *json.RawMessage, result any) {
		msg := rpcMessage{JSONRPC: "2.0", ID: id, Result: mustMarshal(result)}
		payload, _ := json.Marshal(msg)
		writeMu.Lock()
		_ = writeFrame(os.Stdout, payload)
		writeMu.Unlock()
	}
	progress := func(kind string, pct int) {
		value := map[string]any{"kind": kind}
		if pct >= 0 {
			value["percentage"] = pct
		}
		msg := rpcMessage{JSONRPC: "2.0", Method: "$/progress",
			Params: mustMarshal(map[string]any{"token": "work", "value": value})}
		payload, _ := json.Marshal(msg)
		writeMu.Lock()
		_ = writeFrame(os.Stdout, payload)
		writeMu.Unlock()
	}
	// Whether the fake's index is complete. Every mode announces its
	// indexing through the standard progress pair; the indexing mode keeps
	// it open for a while and answers partial references meanwhile, the way
	// a real server does.
	var indexed atomic.Bool
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
			//
			// The answers are built from the working directory, not from the
			// announced workspace: the workspace is the project for most
			// servers and the directory above it for one the cockpit hands a
			// configuration, while the working directory is the project for
			// every one of them, which is what an answer names.
			if wd, err := os.Getwd(); err == nil {
				rootURI = "file://" + wd
			}
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
		case "textDocument/definition":
			if mode == "crash-on-request" {
				os.Exit(4)
			}
			if mode == "exit-restart-on-request" {
				os.Exit(watcherRestartCode)
			}
			if mode == "hang" {
				continue
			}
			if mode == "empty" {
				respond(msg.ID, nil)
				continue
			}
			var params struct {
				TextDocument struct {
					URI string `json:"uri"`
				} `json:"textDocument"`
			}
			_ = json.Unmarshal(msg.Params, &params)
			// A document outside the workspace is answered with its own
			// URI, so a test can read back which file the request really
			// named; everything inside answers the one definition file.
			answer := rootURI + "/def.go"
			if !strings.HasPrefix(params.TextDocument.URI, rootURI+"/") {
				answer = params.TextDocument.URI
			}
			// The document version travels as the start line, the open count
			// as the start character; the range spans a word's width so a
			// request at a covered position reads as the declaration.
			respond(msg.ID, []map[string]any{{
				"uri": answer,
				"range": map[string]any{
					"start": map[string]any{"line": docs[params.TextDocument.URI], "character": len(docs)},
					"end":   map[string]any{"line": docs[params.TextDocument.URI], "character": len(docs) + 11},
				},
			}})
		case "textDocument/references":
			if mode == "hang" {
				continue
			}
			var params struct {
				Context struct {
					IncludeDeclaration bool `json:"includeDeclaration"`
				} `json:"context"`
			}
			_ = json.Unmarshal(msg.Params, &params)
			locs := []map[string]any{
				{"uri": rootURI + "/use.go", "range": map[string]any{"start": map[string]any{"line": 4, "character": 2}, "end": map[string]any{"line": 4, "character": 5}}},
			}
			// While the index is announced as running, the answer is real
			// but partial, which is exactly what the service must wait out.
			if indexed.Load() {
				locs = append(locs, map[string]any{"uri": "file:///outside/stub.go", "range": map[string]any{"start": map[string]any{"line": 0, "character": 0}, "end": map[string]any{"line": 0, "character": 0}}})
				if params.Context.IncludeDeclaration {
					locs = append(locs, map[string]any{"uri": rootURI + "/def.go", "range": map[string]any{"start": map[string]any{"line": 1, "character": 0}, "end": map[string]any{"line": 1, "character": 3}}})
				}
			}
			respond(msg.ID, locs)
		case "shutdown":
			respond(msg.ID, nil)
		case "exit":
			os.Exit(0)
		default:
			if msg.ID != nil && msg.Method == "" && initID != nil {
				// The configuration response arrived; finish the handshake
				// and announce the indexing.
				respond(initID, map[string]any{"capabilities": map[string]any{}})
				initID = nil
				switch mode {
				case "indexing":
					progress("begin", 0)
					go func() {
						time.Sleep(350 * time.Millisecond)
						progress("report", 50)
						time.Sleep(350 * time.Millisecond)
						indexed.Store(true)
						progress("end", -1)
					}()
				case "mute":
					// A server that never announces its indexing; the
					// warming grace is all that holds its requests back.
					indexed.Store(true)
				default:
					indexed.Store(true)
					progress("begin", -1)
					progress("end", -1)
				}
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

func newTestService(t *testing.T) *Service {
	t.Helper()
	s, _ := newTestServiceWithCache(t)
	return s
}

// newTestServiceWithCache answers the service and the cache root it runs
// with, which is what a test needs to build a path inside a source root.
func newTestServiceWithCache(t *testing.T) (*Service, string) {
	t.Helper()
	work := t.TempDir()
	marker := filepath.Join(work, "image-built")
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	installFakeDocker(t, filepath.Join(work, "docker-args"), marker, "", "")
	cacheRoot := t.TempDir()
	s := New(t.TempDir(), cacheRoot, nil)
	t.Cleanup(s.Close)
	return s, cacheRoot
}

func goRequest(t *testing.T) Request {
	t.Helper()
	return Request{
		Client:      "client-a",
		ProjectName: "proj",
		ProjectRoot: t.TempDir(),
		Path:        "main.go",
		Content:     "package main\n",
		Line:        1,
		Character:   0,
	}
}

func TestServiceUnknownLanguage(t *testing.T) {
	s := newTestService(t)
	req := goRequest(t)
	req.Path = "readme.txt"
	res, _ := s.Definition(context.Background(), req)
	if res.Available || res.Status != StatusNoLanguage {
		t.Fatalf("status %+v", res)
	}
}

func TestServiceNotInstalled(t *testing.T) {
	s := newTestService(t)
	// An empty PATH: the Docker way's own detection, the docker client,
	// finds nothing.
	t.Setenv("PATH", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	res, _ := s.Definition(context.Background(), goRequest(t))
	if res.Status != StatusNotInstalled {
		t.Fatalf("status %q", res.Status)
	}
}

func TestServiceInvalidPosition(t *testing.T) {
	s := newTestService(t)
	req := goRequest(t)
	req.Line = 99
	if _, err := s.Definition(context.Background(), req); err == nil {
		t.Fatal("expected position error")
	}
}

func TestServiceDefinitionFlow(t *testing.T) {
	installFakeLSP(t, "normal", "gopls")
	s := newTestService(t)

	req := goRequest(t)
	res, err := s.Definition(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available || len(res.Locations) != 1 {
		t.Fatalf("unavailable: %+v", res)
	}
	// The fake answers 0-based line = the server assigned document version,
	// 1 after didOpen, and the service maps to 1-based.
	if res.Locations[0] != (Location{Path: "def.go", Line: 2, Character: 1}) {
		t.Fatalf("location %+v", res.Locations[0])
	}

	// Changed text syncs via didChange with the next server version; the
	// same text again costs nothing and keeps the version.
	req.Content = "package main\nx"
	res, _ = s.Definition(context.Background(), req)
	if !res.Available || res.Locations[0].Line != 3 {
		t.Fatalf("after change: %+v", res)
	}
	res, _ = s.Definition(context.Background(), req)
	if !res.Available || res.Locations[0].Line != 3 {
		t.Fatalf("unchanged text must keep the version: %+v", res)
	}
	if s.ConnectionCount() != 1 {
		t.Fatalf("connections %d", s.ConnectionCount())
	}

	// A second document on the same connection, then closing it again is
	// visible in the open count the fake encodes as the character.
	req2 := goRequest(t)
	req2.ProjectRoot = req.ProjectRoot
	req2.Path = "other.go"
	res, _ = s.Definition(context.Background(), req2)
	if !res.Available || res.Locations[0].Character != 2 {
		t.Fatalf("second doc: %+v", res)
	}
	s.CloseDocument(req.Client, req.ProjectName, "other.go")
	res, _ = s.Definition(context.Background(), req)
	if !res.Available || res.Locations[0].Character != 1 {
		t.Fatalf("after close: %+v", res)
	}
}

func TestServiceSharedAcrossClients(t *testing.T) {
	installFakeLSP(t, "normal", "gopls")
	s := newTestService(t)

	// Two editor instances of one project share one connection and one open
	// document; the fake's open count stays at one for both.
	a := goRequest(t)
	b := a
	b.Client = "client-b"
	resA, _ := s.Definition(context.Background(), a)
	resB, _ := s.Definition(context.Background(), b)
	if !resA.Available || !resB.Available {
		t.Fatalf("answers: %+v %+v", resA, resB)
	}
	if s.ConnectionCount() != 1 {
		t.Fatalf("connections %d", s.ConnectionCount())
	}
	if resB.Locations[0].Character != 1 {
		t.Fatalf("the shared document must be open once: %+v", resB.Locations[0])
	}

	// One client letting go does not close the document the other still
	// holds; the last one does.
	s.CloseDocument(a.Client, a.ProjectName, a.Path)
	res, _ := s.Definition(context.Background(), b)
	if !res.Available || res.Locations[0].Character != 1 || res.Locations[0].Line != 2 {
		t.Fatalf("document must survive the first close: %+v", res.Locations)
	}
	s.CloseDocument(b.Client, b.ProjectName, b.Path)
	s.CloseDocument(a.Client, a.ProjectName, a.Path)
	// Reopened after everybody let go: the server version starts over.
	res, _ = s.Definition(context.Background(), a)
	if !res.Available || res.Locations[0].Line != 2 {
		t.Fatalf("document must reopen fresh: %+v", res.Locations)
	}
}

func TestServiceTouchKeepsProjectAlive(t *testing.T) {
	installFakeLSP(t, "normal", "gopls")
	s := newTestService(t)

	req := goRequest(t)
	if res, _ := s.Definition(context.Background(), req); !res.Available {
		t.Fatal("setup request failed")
	}
	base := time.Now()
	// Just short of the idle timeout an editor action lands: the connection
	// counts as used from that moment.
	s.now = func() time.Time { return base.Add(connIdleTimeout - time.Minute) }
	s.Touch(req.ProjectName)
	s.now = func() time.Time { return base.Add(2*connIdleTimeout - 2*time.Minute) }
	s.expireIdle()
	if s.ConnectionCount() != 1 {
		t.Fatal("a touched connection must survive the sweep")
	}
	s.now = func() time.Time { return base.Add(3 * connIdleTimeout) }
	s.expireIdle()
	if s.ConnectionCount() != 0 {
		t.Fatal("an untouched connection must expire")
	}
}

func TestServiceWarmStartsWithoutARequest(t *testing.T) {
	installFakeLSP(t, "normal", "gopls")
	s := newTestService(t)

	req := goRequest(t)
	s.Warm(req.ProjectName, req.ProjectRoot, []WarmMode{{ProfileID: "go"}, {ProfileID: "unknown"}})
	// Only the known profile starts; the first request reuses it.
	if s.ConnectionCount() != 1 {
		t.Fatalf("connections after warm: %d", s.ConnectionCount())
	}
	if res, _ := s.Definition(context.Background(), req); !res.Available {
		t.Fatal("request after warm failed")
	}
	if s.ConnectionCount() != 1 {
		t.Fatalf("the request must reuse the warm connection: %d", s.ConnectionCount())
	}
}

// The change events feed the editor's indicator over SSE: a slot appearing,
// the handshake ending and the announced progress each fire one, all naming
// the project.
func TestServiceChangeEvents(t *testing.T) {
	installFakeLSP(t, "indexing", "gopls")
	s := newTestService(t)
	var events atomic.Int64
	var wrongProject atomic.Bool
	s.OnChange(func(project string) {
		if project != "proj" {
			wrongProject.Store(true)
		}
		events.Add(1)
	})

	req := goRequest(t)
	if res, _ := s.Definition(context.Background(), req); !res.Available {
		t.Fatal("setup request failed")
	}
	if wrongProject.Load() {
		t.Fatal("an event named a foreign project")
	}
	// At least the appearing slot, the finished handshake and the progress
	// moves of the announced indexing.
	if n := events.Load(); n < 4 {
		t.Fatalf("expected the indexing moves to fire events, got %d", n)
	}
}

// A server that announces no startup work has no warming stretch to sit
// out: its silence is readiness, not a late announcement. Waiting it out
// cost 45 seconds of spinner for an answer that stood at once. The servers
// that index at the handshake keep that wait, their complete answers hang
// on it, which is TestServiceWarmingGraceForSilentServer further down.
func TestSilentStartServerIsNotWaitedOut(t *testing.T) {
	installFakeLSP(t, "mute", "tsgo")
	s := newTestService(t)
	s.warmupGrace = 3 * time.Second

	req := goRequest(t)
	req.Path = "src/index.ts"
	start := time.Now()
	res, err := s.Definition(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available || len(res.Locations) == 0 {
		t.Fatalf("the answer must come back: %+v", res)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("a silent-start server must not be waited out, took %v", took)
	}
}

// And an empty answer from such a server is the truth rather than a warming
// artifact, so it comes back instead of being retried into a longer
// spinner. The handshake servers keep their retries, see
// TestServiceEmptyAnswerRetriesThenReturns.
func TestEmptyAnswerFromSilentStartServerIsNotRetried(t *testing.T) {
	installFakeLSP(t, "empty", "tsgo")
	s := newTestService(t)

	req := goRequest(t)
	req.Path = "src/index.ts"
	start := time.Now()
	res, err := s.Definition(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available || len(res.Locations) != 0 {
		t.Fatalf("an empty answer is still an answer: %+v", res)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("an empty answer must not be retried into a long wait, took %v", took)
	}
}

// The warming window is a clock nobody looks at twice, so its end has to
// travel as an event: without it the indicator of a server that announces
// nothing keeps a bar up that only an unrelated move would ever take down.
func TestSilentWarmingWindowPublishesItsEnd(t *testing.T) {
	installFakeLSP(t, "mute", "gopls")
	s := newTestService(t)
	s.warmupGrace = 700 * time.Millisecond
	moves := make(chan struct{}, 32)
	s.OnChange(func(string) {
		select {
		case moves <- struct{}{}:
		default:
		}
	})

	req := goRequest(t)
	s.Warm(req.ProjectName, req.ProjectRoot, []WarmMode{{ProfileID: "go"}})
	// Wait the window out through the events alone, the way the browser
	// does: no status is pulled until one arrives.
	deadline := time.After(6 * time.Second)
	for {
		select {
		case <-moves:
			if states := s.IndexStatus(req.ProjectName); len(states) == 1 && !states[0].Indexing {
				return
			}
		case <-deadline:
			t.Fatal("the end of the warming window was never published")
		}
	}
}

func TestServiceIndexStatus(t *testing.T) {
	installFakeLSP(t, "indexing", "gopls")
	s := newTestService(t)

	req := goRequest(t)
	s.Warm(req.ProjectName, req.ProjectRoot, []WarmMode{{ProfileID: "go"}})
	sawIndexing := false
	sawPct := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		states := s.IndexStatus(req.ProjectName)
		if len(states) == 1 && states[0].Indexing {
			sawIndexing = true
			if states[0].Percentage >= 0 {
				sawPct = true
			}
		}
		if len(states) == 1 && !states[0].Indexing && sawIndexing {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !sawIndexing || !sawPct {
		t.Fatalf("indexing %v with percentage %v must be visible while it runs", sawIndexing, sawPct)
	}
	states := s.IndexStatus(req.ProjectName)
	if len(states) != 1 || states[0].Indexing {
		t.Fatalf("indexing must end: %+v", states)
	}
}

func TestServiceReferences(t *testing.T) {
	installFakeLSP(t, "normal", "gopls")
	s := newTestService(t)

	req := goRequest(t)
	req.Path = "use.go"
	res, err := s.References(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available || res.Outside != 1 {
		t.Fatalf("references: %+v", res)
	}
	// The asked file sorts first, the declaration follows; the outside
	// stub is dropped.
	want := []Location{
		{Path: "use.go", Line: 5, Character: 2},
		{Path: "def.go", Line: 2, Character: 0},
	}
	if len(res.Locations) != len(want) || res.Locations[0] != want[0] || res.Locations[1] != want[1] {
		t.Fatalf("locations %+v", res.Locations)
	}
}

func TestServiceEmptyAnswerRetriesThenReturns(t *testing.T) {
	installFakeLSP(t, "empty", "gopls")
	s := newTestService(t)

	res, err := s.Definition(context.Background(), goRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available || len(res.Locations) != 0 {
		t.Fatalf("empty: %+v", res)
	}
}

func TestServiceCrashIsolationAndBackoff(t *testing.T) {
	installFakeLSP(t, "crash-on-request", "gopls")
	s := newTestService(t)

	res, _ := s.Definition(context.Background(), goRequest(t))
	if res.Available || res.Status != StatusError {
		t.Fatalf("crash: %+v", res)
	}
	if s.ConnectionCount() != 0 {
		t.Fatalf("dead connection kept: %d", s.ConnectionCount())
	}
	res, _ = s.Definition(context.Background(), goRequest(t))
	if res.Status != StatusUnavailable {
		t.Fatalf("backoff: %+v", res)
	}
	// The backoff is keyed per project: another project of the same
	// language starts its own server instead of reading unavailable.
	other := goRequest(t)
	other.ProjectName = "proj-other"
	other.ProjectRoot = t.TempDir()
	res, _ = s.Definition(context.Background(), other)
	if res.Status == StatusUnavailable {
		t.Fatalf("one project's backoff must not silence another: %+v", res)
	}
}

func TestServiceRestartExitSkipsBackoff(t *testing.T) {
	installFakeLSP(t, "exit-restart-on-request", "gopls")
	s := newTestService(t)

	// The server dies with the watcher's agreed restart code while the
	// lookup is in flight: an error for this request, but no backoff, the
	// restart is routine.
	req := goRequest(t)
	res, _ := s.Definition(context.Background(), req)
	if res.Available || res.Status != StatusError {
		t.Fatalf("restart exit: %+v", res)
	}
	res, _ = s.Definition(context.Background(), req)
	if res.Status == StatusUnavailable {
		t.Fatalf("a restart wish must not put the project into backoff: %+v", res)
	}
}

func TestServiceCancellation(t *testing.T) {
	installFakeLSP(t, "hang", "gopls")
	s := newTestService(t)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	res, _ := s.Definition(ctx, goRequest(t))
	if res.Status != StatusCanceled {
		t.Fatalf("canceled: %+v", res)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("cancellation hung")
	}
}

func TestServiceIdleExpiry(t *testing.T) {
	installFakeLSP(t, "normal", "gopls")
	s := newTestService(t)

	if res, _ := s.Definition(context.Background(), goRequest(t)); !res.Available {
		t.Fatalf("setup request failed: %+v", res)
	}
	s.mu.Lock()
	var conn *lspConn
	for _, mc := range s.conns {
		conn = mc.conn
	}
	s.mu.Unlock()

	s.now = func() time.Time { return time.Now().Add(connIdleTimeout + time.Hour) }
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
	work := t.TempDir()
	marker := filepath.Join(work, "image-built")
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	installFakeDocker(t, filepath.Join(work, "docker-args"), marker, "", "")
	installFakeLSP(t, "normal", "gopls")
	s := New(t.TempDir(), t.TempDir(), nil)

	if res, _ := s.Definition(context.Background(), goRequest(t)); !res.Available {
		t.Fatal("setup request failed")
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

func TestServiceLimitEvictsIdle(t *testing.T) {
	installFakeLSP(t, "normal", "gopls")
	s := newTestService(t)

	// One project past the table size: the extra one evicts the least
	// recently used idle connection instead of answering busy, so the count
	// never passes the limit.
	var last Result
	for i := 0; i <= maxConnections; i++ {
		req := goRequest(t)
		req.ProjectName = fmt.Sprintf("proj%d", i)
		req.ProjectRoot = t.TempDir()
		last, _ = s.Definition(context.Background(), req)
	}
	if !last.Available {
		t.Fatalf("the extra project must evict an idle connection: %+v", last)
	}
	if s.ConnectionCount() != maxConnections {
		t.Fatalf("connections %d", s.ConnectionCount())
	}
}

func TestServiceBusyWhenEverySlotWorks(t *testing.T) {
	installFakeLSP(t, "hang", "gopls")
	s := newTestService(t)

	// Every slot hangs in flight, so nothing may be evicted.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	for i := 0; i < maxConnections; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := goRequest(t)
			req.ProjectName = fmt.Sprintf("proj%d", i)
			req.ProjectRoot = t.TempDir()
			_, _ = s.Definition(ctx, req)
		}(i)
	}
	deadline := time.Now().Add(10 * time.Second)
	for s.ConnectionCount() < maxConnections && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	// The hanging calls register their in flight token right after the
	// handshake; a short settle keeps the check off that race.
	time.Sleep(300 * time.Millisecond)

	req := goRequest(t)
	req.ProjectName = "proj-more"
	req.ProjectRoot = t.TempDir()
	res, _ := s.Definition(context.Background(), req)
	if res.Status != StatusBusy {
		t.Fatalf("every slot in flight must answer busy: %+v", res)
	}
	cancel()
	wg.Wait()
}

func TestServiceWaitsOutAnnouncedIndexing(t *testing.T) {
	installFakeLSP(t, "indexing", "gopls")
	s := newTestService(t)

	req := goRequest(t)
	req.Path = "use.go"
	start := time.Now()
	res, err := s.References(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	// The fake answers one location while its announced indexing runs and
	// three once it ended; a complete answer proves the wait.
	if !res.Available || len(res.Locations) != 2 || res.Outside != 1 {
		t.Fatalf("must wait for the announced indexing: %+v", res)
	}
	if time.Since(start) < 500*time.Millisecond {
		t.Fatal("answered before the indexing ended")
	}
}

func TestServiceWarmingGraceForSilentServer(t *testing.T) {
	installFakeLSP(t, "mute", "gopls")
	s := newTestService(t)
	s.warmupGrace = 400 * time.Millisecond

	req := goRequest(t)
	req.Path = "use.go"
	start := time.Now()
	res, err := s.References(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	// A server that never announces is held for the warming grace, then
	// asked anyway.
	if !res.Available || len(res.Locations) == 0 {
		t.Fatalf("silent server must still answer after the grace: %+v", res)
	}
	if time.Since(start) < 350*time.Millisecond {
		t.Fatal("answered before the warming grace passed")
	}
}

// installFakeDocker puts a fake docker client on PATH: it records every
// call into argsFile, answers `image inspect` by whether markerFile
// exists, creates it on `build`, lists psFile's lines on `ps`, volsFile's
// on `volume ls` (none when empty) and the current profile refs on
// `images` once markerFile exists, and on `run` execs the server command
// that follows the image token, which resolves to the fake language server
// wrapper on PATH, mode included. A run with an overridden entrypoint
// execs that program with everything behind the image token instead, so
// the cache removal fallback really empties its directory. The Docker way
// runs end to end without a daemon.
func installFakeDocker(t *testing.T, argsFile, markerFile, psFile, volsFile string) {
	t.Helper()
	dir := t.TempDir()
	var refs []string
	for _, p := range profiles {
		refs = append(refs, imageRef(p))
	}
	script := fmt.Sprintf(`#!/bin/sh
echo "$@" >> %q
case "$1" in
  image) [ -f %q ] && exit 0 || exit 1 ;;
  build) touch %q; exit 0 ;;
  ps) [ -n %q ] && cat %q 2>/dev/null; exit 0 ;;
  volume)
    case "$2" in
      ls) [ -n %q ] && cat %q 2>/dev/null; exit 0 ;;
      *) exit 0 ;;
    esac ;;
  rm) exit 0 ;;
  images) [ -f %q ] && printf '%%s\n' %s; exit 0 ;;
  rmi) exit 0 ;;
  run)
    shift
    ep=""
    while [ $# -gt 0 ]; do
      case "$1" in
        --entrypoint) ep="$2"; shift ;;
        dev-cockpit-gopls:*|dev-cockpit-intelephense:*|dev-cockpit-tsgo:*)
          shift
          if [ -n "$ep" ]; then exec "$ep" "$@"; else exec "$1"; fi ;;
      esac
      shift
    done
    exit 1 ;;
  *) exit 1 ;;
esac
`, argsFile, markerFile, markerFile, psFile, psFile, volsFile, volsFile, markerFile, strings.Join(refs, " "))
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// The boot sweep takes exactly the containers wearing the cockpit's LSP
// scheme, leaves every other name alone, coder containers included,
// removes the cache directories whose project no longer exists on disk,
// and clears out the named cache volumes of the previous scheme, which
// nothing creates anymore.
// Deleting a project takes its caches with it, both servers, and only
// its own; the named volume an older release may still hold goes too.
func TestRemoveProjectCachesTakesTheProjectAndNothingElse(t *testing.T) {
	work := t.TempDir()
	argsFile := filepath.Join(work, "args")
	installFakeDocker(t, argsFile, filepath.Join(work, "image-built"), "", "")
	cacheRoot := t.TempDir()
	for _, name := range []string{
		"dev-cockpit-gopls-gone",
		"dev-cockpit-intelephense-gone",
		"dev-cockpit-gopls-kept",
	} {
		if err := os.MkdirAll(filepath.Join(cacheRoot, name, "mod"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	RemoveProjectCaches("gone", cacheRoot, "")
	for name, want := range map[string]bool{
		"dev-cockpit-gopls-gone":        false,
		"dev-cockpit-intelephense-gone": false,
		"dev-cockpit-gopls-kept":        true,
	} {
		if _, err := os.Stat(filepath.Join(cacheRoot, name)); (err == nil) != want {
			t.Errorf("%s: still there = %v, want %v", name, err == nil, want)
		}
	}
	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "volume rm dev-cockpit-gopls-gone") {
		t.Fatalf("the legacy volume of the project was not removed: %s", raw)
	}
}

// A local removal that will not go through, the files of a server that ran
// as the image's root, falls back to a container of the matching profile's
// own image: never pulled, no network, the directory as its only mount,
// find emptying it from inside, and the empty user owned top level goes
// with os.Remove. Without a docker client the local error stands the way
// it does today.
func TestRemoveCacheDirFallsBackToAContainer(t *testing.T) {
	work := t.TempDir()
	argsFile := filepath.Join(work, "args")
	marker := filepath.Join(work, "image-built")
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	installFakeDocker(t, argsFile, marker, "", "")
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		t.Fatal(err)
	}
	// Files another user owns cannot be staged by a suite that may run as
	// root, root deletes anything, so the seam stands in for them.
	removeAll = func(string) error { return errors.New("operation not permitted") }
	t.Cleanup(func() { removeAll = os.RemoveAll })

	cacheRoot := t.TempDir()
	dir := filepath.Join(cacheRoot, "dev-cockpit-gopls-app")
	if err := os.MkdirAll(filepath.Join(dir, "mod", "example.com"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mod", "example.com", "dep.go"), []byte("package dep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := removeCacheDir(dir, dockerPath, nil); err != nil {
		t.Fatalf("fallback removal: %v", err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Fatal("the cache directory is still there")
	}
	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	goProfile, _, _ := ProfileForPath("main.go")
	for _, want := range []string{
		"run --rm --pull=never --network none --entrypoint find",
		"-v " + dir + ":" + dir,
		imageRef(goProfile) + " " + dir + " -mindepth 1 -delete",
	} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("container call misses %q: %s", want, raw)
		}
	}

	other := filepath.Join(cacheRoot, "dev-cockpit-tsgo-app")
	if err := os.MkdirAll(filepath.Join(other, "cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := removeCacheDir(other, "", nil); err == nil {
		t.Fatal("no docker client must keep the local error")
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatal("the directory must stand until the next attempt")
	}
}

// What the fallback logs when the container run fails is docker's own
// reason, which stands first in its output; the trailing help hint line
// must never reach the log.
func TestRemoveCacheDirLogsDockersReasonNotTheHelpHint(t *testing.T) {
	dir := t.TempDir()
	goProfile, _, _ := ProfileForPath("main.go")
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
  images) echo %q; exit 0 ;;
  run)
    echo "docker: Error response from daemon: something broke." >&2
    echo "" >&2
    echo "Run 'docker run --help' for more information" >&2
    exit 125 ;;
esac
exit 0
`, imageRef(goProfile))
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	removeAll = func(string) error { return errors.New("operation not permitted") }
	t.Cleanup(func() { removeAll = os.RemoveAll })
	var logBuf strings.Builder
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	cache := filepath.Join(t.TempDir(), "dev-cockpit-gopls-app")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := removeCacheDir(cache, filepath.Join(dir, "docker"), nil); err == nil {
		t.Fatal("the removal must keep failing")
	}
	logged := logBuf.String()
	if !strings.Contains(logged, "Error response from daemon: something broke.") {
		t.Fatalf("the log misses docker's reason: %s", logged)
	}
	if strings.Contains(logged, "--help") {
		t.Fatalf("the help hint must never reach the log: %s", logged)
	}
}

// A host without a single cockpit built image is a state of its own: the
// first start builds one, so the sweep says so calmly, leaves the
// directory for a later run, and never puts an exit status into the log
// or starts a container.
func TestRemoveCacheDirMissingImageIsLoggedCalmly(t *testing.T) {
	work := t.TempDir()
	argsFile := filepath.Join(work, "args")
	installFakeDocker(t, argsFile, filepath.Join(work, "never-built"), "", "")
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		t.Fatal(err)
	}
	removeAll = func(string) error { return errors.New("operation not permitted") }
	t.Cleanup(func() { removeAll = os.RemoveAll })
	var logBuf strings.Builder
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	cache := filepath.Join(t.TempDir(), "dev-cockpit-gopls-app")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := removeCacheDir(cache, dockerPath, nil); err == nil {
		t.Fatal("the removal must report the local error")
	}
	if _, err := os.Stat(cache); err != nil {
		t.Fatal("the directory must stay for a later sweep")
	}
	logged := logBuf.String()
	if !strings.Contains(logged, "stays for a later sweep") || !strings.Contains(logged, "no cockpit built image exists on this host yet") {
		t.Fatalf("the empty candidate list must be its own calm case: %s", logged)
	}
	if strings.Contains(logged, "exit status") {
		t.Fatalf("no exit status for an expected state: %s", logged)
	}
	if raw, _ := os.ReadFile(argsFile); strings.Contains(string(raw), "run ") {
		t.Fatalf("no container may run without an image: %s", raw)
	}
}

// The removal image is picked with preference: the matching profile's
// current tag first, then any tag of its repository, then another
// profile's image, and images outside the cockpit's three repositories
// are never candidates.
func TestRemovalImagePrefersTheMatchingProfile(t *testing.T) {
	dir := t.TempDir()
	listFile := filepath.Join(dir, "list")
	script := fmt.Sprintf(`#!/bin/sh
[ "$1" = images ] && { cat %q 2>/dev/null; exit 0; }
exit 1
`, listFile)
	dockerBin := filepath.Join(dir, "docker")
	if err := os.WriteFile(dockerBin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	list := func(refs ...string) {
		if err := os.WriteFile(listFile, []byte(strings.Join(refs, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	goProfile, _, _ := ProfileForPath("main.go")
	ts, _, _ := ProfileForPath("index.ts")
	current, tsRef := imageRef(goProfile), imageRef(ts)
	oldTag := "dev-cockpit-gopls:000000000000"

	list("golang:1.26", tsRef, oldTag, current)
	if ref, err := removalImage(context.Background(), dockerBin, nil, goProfile); err != nil || ref != current {
		t.Fatalf("the current tag must win: %q %v", ref, err)
	}
	list("golang:1.26", tsRef, oldTag)
	if ref, err := removalImage(context.Background(), dockerBin, nil, goProfile); err != nil || ref != oldTag {
		t.Fatalf("the own repository must beat another profile's: %q %v", ref, err)
	}
	list("golang:1.26", tsRef)
	if ref, err := removalImage(context.Background(), dockerBin, nil, goProfile); err != nil || ref != tsRef {
		t.Fatalf("any cockpit built image beats none: %q %v", ref, err)
	}
	list("golang:1.26", "gopls-standalone:latest")
	if _, err := removalImage(context.Background(), dockerBin, nil, goProfile); !errors.Is(err, errImageNotBuilt) {
		t.Fatalf("a stranger's image is no candidate: %v", err)
	}
}

// The migration before a start: a cache whose entries another user wrote,
// the releases that ran the server as root, is taken away and the server
// starts cold once, while the cockpit's own cache stays. The check reads
// one level below the top, the top level is ensureCacheDir's and always
// the cockpit's own.
func TestPrepareRemovesAForeignOwnedCache(t *testing.T) {
	work := t.TempDir()
	argsFile := filepath.Join(work, "args")
	marker := filepath.Join(work, "image-built")
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	installFakeDocker(t, argsFile, marker, "", "")
	cacheRoot := t.TempDir()
	launcher := DockerLauncher(cacheRoot, nil)
	goProfile, _, _ := ProfileForPath("main.go")

	own := filepath.Join(cacheRoot, "dev-cockpit-gopls-own")
	if err := os.MkdirAll(filepath.Join(own, "mod"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(own, "mod", "keep.go"), []byte("package dep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := launcher.Prepare(context.Background(), t.TempDir(), "own", goProfile); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(own, "mod", "keep.go")); err != nil {
		t.Fatal("the cockpit's own cache must stay")
	}

	// Creating another user's files needs root, so the shifted uid makes
	// the same entries read as somebody else's.
	foreign := filepath.Join(cacheRoot, "dev-cockpit-gopls-taken")
	if err := os.MkdirAll(filepath.Join(foreign, "mod"), 0o755); err != nil {
		t.Fatal(err)
	}
	processUID = os.Getuid() + 1
	t.Cleanup(func() { processUID = os.Getuid() })
	if err := launcher.Prepare(context.Background(), t.TempDir(), "taken", goProfile); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(foreign, "mod")); err == nil {
		t.Fatal("the foreign cache must be gone")
	}
	if info, err := os.Stat(filepath.Join(foreign, "home")); err != nil || !info.IsDir() {
		t.Fatalf("the cold start still gets its cache directory: %v", err)
	}
}

func TestSweepStaleMatchesOnlyTheScheme(t *testing.T) {
	work := t.TempDir()
	argsFile := filepath.Join(work, "args")
	psFile := filepath.Join(work, "names")
	volsFile := filepath.Join(work, "vols")
	projectsRoot := t.TempDir()
	cacheRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(projectsRoot, "alive"), 0o755); err != nil {
		t.Fatal(err)
	}
	// One cache directory of a living project, one of a project that left
	// the disk, and one directory that is none of ours. The orphan is
	// written the way a module cache is, read only, which is what a plain
	// removal stumbles over.
	orphan := filepath.Join(cacheRoot, "dev-cockpit-gopls-gone-project")
	if err := os.MkdirAll(filepath.Join(orphan, "mod", "example.com"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphan, "mod", "example.com", "dep.go"), []byte("package dep\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(orphan, "mod", "example.com"), 0o555); err != nil {
		t.Fatal(err)
	}
	for _, keep := range []string{"dev-cockpit-gopls-alive", "somebody-elses-folder"} {
		if err := os.Mkdir(filepath.Join(cacheRoot, keep), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(psFile, []byte(strings.Join([]string{
		"dev-cockpit-gopls-old-project",
		"dev-cockpit-intelephense-x-abc123",
		"dev-cockpit-copilot",
		"dev-cockpit-gopls",
		"dc-ollama",
		"gopls-standalone",
	}, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(volsFile, []byte(strings.Join([]string{
		"dev-cockpit-gopls-alive",
		"dev-cockpit-gopls-gone-project",
		"dev-cockpit-intelephense-alive",
		"dev-cockpit-gomod",
		"dc-ollama",
	}, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	installFakeDocker(t, argsFile, filepath.Join(work, "image-built"), psFile, volsFile)
	sweepStale(context.Background(), projectsRoot, cacheRoot, nil)
	for dir, want := range map[string]bool{
		"dev-cockpit-gopls-gone-project": false,
		"dev-cockpit-gopls-alive":        true,
		"somebody-elses-folder":          true,
	} {
		if _, err := os.Stat(filepath.Join(cacheRoot, dir)); (err == nil) != want {
			t.Fatalf("cache directory %s: still there = %v, want %v", dir, err == nil, want)
		}
	}
	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	var rms, volRms []string
	for _, call := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.HasPrefix(call, "rm -f ") {
			rms = append(rms, strings.TrimPrefix(call, "rm -f "))
		}
		if strings.HasPrefix(call, "volume rm ") {
			volRms = append(volRms, strings.TrimPrefix(call, "volume rm "))
		}
	}
	want := "dev-cockpit-gopls-old-project,dev-cockpit-intelephense-x-abc123"
	if strings.Join(rms, ",") != want {
		t.Fatalf("swept %v, want %s", rms, want)
	}
	// Every volume of the scheme goes, whether its project lives or not:
	// the caches are directories now and no volume of that name will ever
	// be started against again.
	wantVols := "dev-cockpit-gopls-alive,dev-cockpit-gopls-gone-project,dev-cockpit-intelephense-alive"
	if strings.Join(volRms, ",") != wantVols {
		t.Fatalf("volume sweep took %v, want %s", volRms, wantVols)
	}
}

func TestServiceDockerModeBuildsOnceAndMountsTheContract(t *testing.T) {
	work := t.TempDir()
	argsFile := filepath.Join(work, "args")
	installFakeDocker(t, argsFile, filepath.Join(work, "image-built"), "", "")
	installFakeLSP(t, "normal", "gopls")
	projectsRoot := t.TempDir()
	cacheRoot := t.TempDir()
	s := New(projectsRoot, cacheRoot, nil)
	t.Cleanup(s.Close)

	req := goRequest(t)
	req.Launcher = DockerLauncher(cacheRoot, nil)
	res, err := s.Definition(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available || len(res.Locations) != 1 {
		t.Fatalf("docker mode answer: %+v", res)
	}

	// A second project reuses the built image: one build call, two runs.
	goProfile, _, _ := ProfileForPath("main.go")
	goImageRef := imageRef(goProfile)
	req2 := goRequest(t)
	req2.ProjectName = "proj2"
	req2.Launcher = DockerLauncher(cacheRoot, nil)
	if res, _ := s.Definition(context.Background(), req2); !res.Available {
		t.Fatalf("second docker project failed: %+v", res)
	}
	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	calls := strings.Split(strings.TrimSpace(string(raw)), "\n")
	builds, runs, rms := 0, 0, 0
	var runLine string
	lastWasRm := false
	for _, call := range calls {
		if strings.HasPrefix(call, "build ") {
			builds++
		}
		if strings.HasPrefix(call, "rm -f dev-cockpit-gopls-") {
			rms++
			lastWasRm = true
			continue
		}
		if strings.HasPrefix(call, "run ") {
			runs++
			runLine = call
			// The stale name is cleared right before each start.
			if !lastWasRm {
				t.Fatalf("run without a preceding rm: %v", calls)
			}
		}
		lastWasRm = false
	}
	if builds != 1 || runs != 2 || rms != 2 {
		t.Fatalf("builds %d runs %d rms %d: %v", builds, runs, rms, calls)
	}
	// The run carries the wrapper contract: reaped, interactive, a speaking
	// name with the project in it, projects dir at its own path, the cache
	// directory at the very path it has outside, the module downloads and
	// the file cache pointed into it, the workspace as workdir, the built
	// image, the server command.
	cache := filepath.Join(cacheRoot, "dev-cockpit-gopls-"+req2.ProjectName)
	for _, want := range []string{
		"--rm", "-i", "--init",
		"--name dev-cockpit-gopls-" + req2.ProjectName,
		"--label " + lspRootLabel + "=" + projectsRoot,
		fmt.Sprintf("--user %d:%d", os.Getuid(), os.Getgid()),
		"-v " + projectsRoot + ":" + projectsRoot,
		"-v " + cache + ":" + cache,
		"-w " + req2.ProjectRoot,
		"-e HOME=" + cache + "/home",
		"-e GOMODCACHE=" + cache + "/mod",
		"-e GOFLAGS=-modcacherw",
		"-e XDG_CACHE_HOME=" + cache + "/cache",
		goImageRef + " gopls",
	} {
		if !strings.Contains(runLine, want) {
			t.Fatalf("run line misses %q: %s", want, runLine)
		}
	}
	// And it stands there before the server starts, or docker would make
	// it itself, as root and with a mode nobody chose.
	if info, err := os.Stat(cache); err != nil || !info.IsDir() {
		t.Fatalf("cache directory not prepared: %v", err)
	}
}

// What the cockpit still owns for a server whose image writes a default
// configuration: the project is mounted alone, so the directory above it
// belongs to the container and nothing of ours can reach the working copy,
// and that directory travels to the image as the environment it writes into.
// It is the very directory the handshake announces, or the file would land
// where the server does not look. Which file, and whether the project
// already brought one, is the image's, and the container start proves it.
func TestDefaultConfigMountsTheProjectAloneAndNamesTheWorkspace(t *testing.T) {
	ts, _, _ := ProfileForPath("index.ts")
	goProfile, _, _ := ProfileForPath("main.go")
	projectsRoot := t.TempDir()
	root := filepath.Join(projectsRoot, "app")
	launcher := DockerLauncher(t.TempDir(), nil)

	line := strings.Join(launcher.Argv(projectsRoot, "app", root, ts), " ")
	if !strings.Contains(line, "-v "+root+":"+root) || strings.Contains(line, "-v "+projectsRoot+":"+projectsRoot) {
		t.Fatalf("the project is mounted alone: %s", line)
	}
	if !strings.Contains(line, "-e DC_WORKSPACE="+projectsRoot) {
		t.Fatalf("the image is told where to write: %s", line)
	}
	if got := workspaceURI(ts, root); got != fileURI(projectsRoot) {
		t.Fatalf("the handshake announces the same directory, got %s", got)
	}
	// The working directory stays the project, so the container's watcher
	// keeps watching it and a configuration added there restarts the server,
	// which is what makes the image's check run again.
	if !strings.Contains(line, "-w "+root) {
		t.Fatalf("the workspace stays the working directory: %s", line)
	}

	// Go and PHP know none of this and keep the projects directory.
	goLine := strings.Join(launcher.Argv(projectsRoot, "app", root, goProfile), " ")
	if !strings.Contains(goLine, "-v "+projectsRoot+":"+projectsRoot) || strings.Contains(goLine, "DC_WORKSPACE") {
		t.Fatalf("go is untouched: %s", goLine)
	}
	if got := workspaceURI(goProfile, root); got != fileURI(root) {
		t.Fatalf("go announces its project, got %s", got)
	}
}

// The handshake announces the directory the configuration lies in, while
// everything the editor is answered with stays relative to the project.
func TestWorkspaceURIFollowsTheConfiguration(t *testing.T) {
	ts, _, _ := ProfileForPath("index.ts")
	goProfile, _, _ := ProfileForPath("main.go")
	if got := workspaceURI(ts, "/projects/app"); got != "file:///projects" {
		t.Fatalf("typescript announces the directory above, got %s", got)
	}
	if got := workspaceURI(goProfile, "/projects/app"); got != "file:///projects/app" {
		t.Fatalf("go announces its project, got %s", got)
	}
}

// The launchers' initializationOptions: the container way points the PHP
// server's index storage into the project's own cache directory, which is
// the same path inside and outside, and everything else stays nil.
func TestLauncherInitOptions(t *testing.T) {
	php, _, _ := ProfileForPath("index.php")
	goProfile, _, _ := ProfileForPath("main.go")
	cacheRoot := t.TempDir()
	cache := filepath.Join(cacheRoot, "dev-cockpit-intelephense-my-app")
	opts, ok := DockerLauncher(cacheRoot, nil).InitOptions("my-app", php).(map[string]any)
	if !ok || opts["storagePath"] != cache+"/storage" || opts["globalStoragePath"] != cache+"/global" {
		t.Fatalf("php docker init options: %#v", opts)
	}
	if DockerLauncher(cacheRoot, nil).InitOptions("proj", goProfile) != nil {
		t.Fatalf("go docker init options must be nil")
	}
	ts, _, _ := ProfileForPath("index.ts")
	if DockerLauncher(cacheRoot, nil).InitOptions("proj", ts) != nil {
		t.Fatalf("typescript docker init options must be nil")
	}
}

// The container run of the typescript way: the cache directory carries what
// the server downloads itself, so its environment points there and at the
// very path the directory has outside, which is what lets the read route
// answer a definition that lands in it.
func TestTypescriptDockerArgvPointsTheCacheIn(t *testing.T) {
	ts, _, _ := ProfileForPath("index.ts")
	cacheRoot := t.TempDir()
	cache := filepath.Join(cacheRoot, "dev-cockpit-tsgo-my-app")
	argv := DockerLauncher(cacheRoot, nil).Argv("/projects", "my-app", "/projects/my-app", ts)
	line := strings.Join(argv, " ")
	for _, want := range []string{
		"-v " + cache + ":" + cache,
		"-e XDG_CACHE_HOME=" + cache + "/cache",
		imageRef(ts) + " tsgo --lsp -stdio",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("run line misses %q: %s", want, line)
		}
	}
}

// The server runs as the cockpit's own user, with HOME pointed into the
// cache mount, whose home directory ensureCacheDir stands up before the
// bind; the profile whose image writes a default configuration gets a
// tmpfs on the workspace directory, or that directory, container
// filesystem and root's, would refuse the config file.
func TestDockerArgvRunsAsTheCockpitUser(t *testing.T) {
	goProfile, _, _ := ProfileForPath("main.go")
	ts, _, _ := ProfileForPath("index.ts")
	cacheRoot := t.TempDir()
	launcher := DockerLauncher(cacheRoot, nil)
	user := fmt.Sprintf("--user %d:%d", os.Getuid(), os.Getgid())

	goCache := filepath.Join(cacheRoot, "dev-cockpit-gopls-my-app")
	goLine := strings.Join(launcher.Argv("/projects", "my-app", "/projects/my-app", goProfile), " ")
	for _, want := range []string{user, "-e HOME=" + goCache + "/home"} {
		if !strings.Contains(goLine, want) {
			t.Fatalf("run line misses %q: %s", want, goLine)
		}
	}
	if strings.Contains(goLine, "--tmpfs") {
		t.Fatalf("only the default config profile needs a tmpfs: %s", goLine)
	}

	tsCache := filepath.Join(cacheRoot, "dev-cockpit-tsgo-my-app")
	tsLine := strings.Join(launcher.Argv("/projects", "my-app", "/projects/my-app", ts), " ")
	for _, want := range []string{user, "-e HOME=" + tsCache + "/home", "--tmpfs /projects "} {
		if !strings.Contains(tsLine, want) {
			t.Fatalf("run line misses %q: %s", want, tsLine)
		}
	}

	if err := ensureCacheDir(goCache); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(goCache, "home")); err != nil || !info.IsDir() {
		t.Fatalf("the home directory must stand before the bind: %v", err)
	}
}

// The restart flag lives under /tmp: the server runs as the cockpit's
// user, and /run inside the images is root's, so a flag there would be a
// restart that never happens.
func TestEntrypointRestartFlagLivesInTmp(t *testing.T) {
	if !strings.Contains(entrypointDockerfile, "flag=/tmp/dev-cockpit-restart") {
		t.Fatalf("the restart flag must live under /tmp:\n%s", entrypointDockerfile)
	}
	if strings.Contains(entrypointDockerfile, "/run") {
		t.Fatalf("no /run in the entrypoint:\n%s", entrypointDockerfile)
	}
}

// The image tag is a stable hash of the shipped build file: same content,
// same tag; the tag never collides across the two images and stays a
// valid docker tag.
func TestImageRefFollowsTheBuildFile(t *testing.T) {
	goProfile, _, _ := ProfileForPath("main.go")
	php, _, _ := ProfileForPath("index.php")
	ts, _, _ := ProfileForPath("index.ts")
	goRef, phpRef, tsRef := imageRef(goProfile), imageRef(php), imageRef(ts)
	if goRef != imageRef(goProfile) {
		t.Fatal("the tag is not deterministic")
	}
	if !strings.HasPrefix(goRef, "dev-cockpit-gopls:") || !strings.HasPrefix(phpRef, "dev-cockpit-intelephense:") ||
		!strings.HasPrefix(tsRef, "dev-cockpit-tsgo:") {
		t.Fatalf("refs: %s / %s / %s", goRef, phpRef, tsRef)
	}
	for _, ref := range []string{goRef, phpRef, tsRef} {
		if !regexp.MustCompile(`^[a-z0-9-]+:[0-9a-f]{12}$`).MatchString(ref) {
			t.Fatalf("tag shape: %s", ref)
		}
	}
}

// The container names: speaking for a plain project, sanitized plus a short
// hash once anything was rewritten, so no two projects share a name.
func TestContainerNames(t *testing.T) {
	if got := containerName("gopls", "dev-cockpit"); got != "dev-cockpit-gopls-dev-cockpit" {
		t.Fatalf("plain name: %s", got)
	}
	spaced := containerName("gopls", "my app")
	if !strings.HasPrefix(spaced, "dev-cockpit-gopls-my-app-") || len(spaced) != len("dev-cockpit-gopls-my-app-")+6 {
		t.Fatalf("sanitized name misses its hash: %s", spaced)
	}
	if dashed := containerName("gopls", "my-app"); dashed != "dev-cockpit-gopls-my-app" || spaced == dashed {
		t.Fatalf("sanitizing collided: %q vs %q", spaced, dashed)
	}
	if spaced != containerName("gopls", "my app") {
		t.Fatal("the name is not deterministic")
	}
	long := strings.Repeat("a", 60)
	longer := long + "b"
	if containerName("gopls", long) == containerName("gopls", longer) {
		t.Fatal("capping collided")
	}
	if got := containerName("gopls", ""); got != containerName("gopls", "") || !strings.HasPrefix(got, "dev-cockpit-gopls-") {
		t.Fatalf("empty project name: %s", got)
	}
	// The longest server name of the registry leaves the least room for the
	// project, which is where a cap that forgot the prefix would show.
	tsPlain := containerName("tsgo", "my-app")
	if tsPlain != "dev-cockpit-tsgo-my-app" {
		t.Fatalf("typescript name: %s", tsPlain)
	}
	if containerName("tsgo", long) == containerName("tsgo", longer) {
		t.Fatal("capping collided behind the short server name")
	}
	for _, name := range []string{spaced, containerName("gopls", "über app"), containerName("gopls", long), containerName("intelephense", longer),
		tsPlain, containerName("tsgo", long), containerName("tsgo", "my app")} {
		if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`).MatchString(name) {
			t.Fatalf("name %q is not a valid container name", name)
		}
		if len(name) > 63 {
			t.Fatalf("name %q is longer than 63", name)
		}
	}
}

// A lookup asked from inside a file outside the project: the request
// travels as the absolute path and the server sees exactly that document
// (the fake answers a file outside the workspace with its own URI), the
// answer comes back marked external with that path, and the declaration
// rule holds out there like it does in the project.
func TestServiceDefinitionFromOutsideTheProject(t *testing.T) {
	installFakeLSP(t, "normal", "gopls")
	s, cacheRoot := newTestServiceWithCache(t)

	req := goRequest(t)
	req.Launcher = DockerLauncher(cacheRoot, nil)
	goProfile, _, _ := ProfileForPath("main.go")
	roots := dockerSourceRoots(cacheRoot, req.ProjectName, goProfile)
	req.Path = roots[0].Path + "/example.com/dep@v1.0.0/dep.go"
	req.Content = "package dep\nfunc Target() {}\n"
	req.Line = 1
	req.Character = 5

	res, err := s.Definition(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available || len(res.Locations) != 1 {
		t.Fatalf("answer: %+v", res)
	}
	if got := res.Locations[0]; got.Path != req.Path || !got.External {
		t.Fatalf("the target must be the asked file, external and absolute: %+v", got)
	}
	if res.Outside != 0 {
		t.Fatalf("a file under a source root is opened, never counted: %+v", res)
	}
	if !res.Declaration {
		t.Fatalf("a covered position reads as the declaration outside the project too: %+v", res)
	}
}

func TestServiceDefinitionAtDeclaration(t *testing.T) {
	installFakeLSP(t, "normal", "gopls")
	s := newTestService(t)

	// The fake's definition range starts at (version, open count) and spans
	// a word: a request inside that range in the answered file reads as the
	// declaration, one from another file does not. The server assigns
	// version 1 on open, so the range sits on line 1.
	req := goRequest(t)
	req.Path = "def.go"
	req.Content = "package main\nfunc IntelTarget() {}\n"
	req.Line = 1
	req.Character = 5
	res, err := s.Definition(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Available || !res.Declaration {
		t.Fatalf("a covered position must read as the declaration: %+v", res)
	}

	other := goRequest(t)
	other.ProjectRoot = req.ProjectRoot
	res, _ = s.Definition(context.Background(), other)
	if !res.Available || res.Declaration {
		t.Fatalf("another file must not read as the declaration: %+v", res)
	}
}
