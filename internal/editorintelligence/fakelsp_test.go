package editorintelligence

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
		case "textDocument/hover":
			var params struct {
				Position lspPosition `json:"position"`
			}
			_ = json.Unmarshal(msg.Params, &params)
			// Line 1 is a class and line 2 a function, both shaped like
			// intelephense's hovers and naming the three characters from the
			// asked one as the symbol's range; every other line and an index
			// still running answer nothing.
			hovers := map[int]string{
				1: "__Foo\\\\Bar__\n\n```php\n<?php\nclass Bar { }\n```",
				2: "__Foo\\\\helper__\n\n```php\n<?php\nfunction helper(): void { }\n```",
			}
			value, ok := hovers[params.Position.Line]
			if !ok || !indexed.Load() {
				respond(msg.ID, nil)
				continue
			}
			end := params.Position
			end.Character += 3
			respond(msg.ID, map[string]any{
				"contents": map[string]any{"kind": "markdown", "value": value},
				"range":    lspRange{Start: params.Position, End: end},
			})
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
