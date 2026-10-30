package editorintelligence

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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
	texts := map[string]string{}
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
	// Whether a server that leaves library symbols out by default was
	// switched to name them, the setting a type search sends tsgo.
	libraries := false
	for {
		payload, err := readFrame(r)
		if err != nil {
			return
		}
		var msg rpcMessage
		if err := json.Unmarshal(payload, &msg); err != nil {
			return
		}
		logFakeRequest(msg)
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
		case "workspace/didChangeConfiguration":
			var params struct {
				Settings struct {
					JSTS struct {
						WorkspaceSymbols struct {
							ExcludeLibrarySymbols bool `json:"excludeLibrarySymbols"`
						} `json:"workspaceSymbols"`
					} `json:"js/ts"`
				} `json:"settings"`
			}
			_ = json.Unmarshal(msg.Params, &params)
			libraries = !params.Settings.JSTS.WorkspaceSymbols.ExcludeLibrarySymbols
		case "textDocument/didOpen":
			var params struct {
				TextDocument struct {
					URI     string `json:"uri"`
					Version int    `json:"version"`
					Text    string `json:"text"`
				} `json:"textDocument"`
			}
			_ = json.Unmarshal(msg.Params, &params)
			docs[params.TextDocument.URI] = params.TextDocument.Version
			texts[params.TextDocument.URI] = params.TextDocument.Text
			if mode == "lists-late" && !indexed.Load() {
				// The listing ends a while after the first document opens,
				// and the diagnostics of that document are the only sign.
				uri := params.TextDocument.URI
				go func() {
					time.Sleep(700 * time.Millisecond)
					indexed.Store(true)
					note := rpcMessage{JSONRPC: "2.0", Method: "textDocument/publishDiagnostics",
						Params: mustMarshal(map[string]any{"uri": uri, "diagnostics": []any{}})}
					payload, _ := json.Marshal(note)
					writeMu.Lock()
					_ = writeFrame(os.Stdout, payload)
					writeMu.Unlock()
				}()
			}
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
			delete(texts, params.TextDocument.URI)
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
				TextDocument struct {
					URI string `json:"uri"`
				} `json:"textDocument"`
				Position lspPosition `json:"position"`
				Context  struct {
					IncludeDeclaration bool `json:"includeDeclaration"`
				} `json:"context"`
			}
			_ = json.Unmarshal(msg.Params, &params)
			// A TypeScript document is answered off its text, every place
			// the asked word stands, the way tsgo answers a type's usages.
			if uri := params.TextDocument.URI; strings.HasSuffix(uri, ".ts") {
				respond(msg.ID, fakeWordReferences(uri, texts[uri], params.Position))
				continue
			}
			if uri := params.TextDocument.URI; strings.HasSuffix(uri, ".php") {
				respond(msg.ID, fakeFilesReferences(fakeFiles(rootURI, texts, ".php"), texts[uri], params.Position))
				continue
			}
			// pyright answers a position in any file of the workspace.
			if uri := params.TextDocument.URI; fakePythonImplementations(uri) {
				files := fakeFiles(rootURI, texts, ".py")
				respond(msg.ID, fakeFilesReferences(files, files[uri], params.Position))
				continue
			}
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
		case "textDocument/prepareTypeHierarchy":
			var params struct {
				TextDocument struct {
					URI string `json:"uri"`
				} `json:"textDocument"`
				Position lspPosition `json:"position"`
			}
			_ = json.Unmarshal(msg.Params, &params)
			// The asked line says what stands there, the way gopls answers:
			// on line 3 an interface declared right there, on line 4 a
			// struct, on line 5 an interface declared in def.go, on line 6
			// no type at all, which gopls answers with an error.
			item := func(uri string, kind int, line int) []map[string]any {
				span := lspRange{Start: lspPosition{Line: line, Character: 5}, End: lspPosition{Line: line, Character: 10}}
				return []map[string]any{{"name": "Shape", "kind": kind, "uri": uri, "range": span, "selectionRange": span, "data": "asked"}}
			}
			switch params.Position.Line {
			case 3:
				respond(msg.ID, item(params.TextDocument.URI, symbolInterface, 3))
			case 4:
				respond(msg.ID, item(params.TextDocument.URI, symbolStruct, 4))
			case 5:
				respond(msg.ID, item(rootURI+"/def.go", symbolInterface, 1))
			case 6:
				refusal := rpcMessage{JSONRPC: "2.0", ID: msg.ID, Error: &rpcError{Code: 0, Message: "not a type name"}}
				payload, _ := json.Marshal(refusal)
				writeMu.Lock()
				_ = writeFrame(os.Stdout, payload)
				writeMu.Unlock()
			default:
				respond(msg.ID, nil)
			}
		case "typeHierarchy/subtypes":
			var params struct {
				Item struct {
					Data string `json:"data"`
				} `json:"item"`
			}
			_ = json.Unmarshal(msg.Params, &params)
			// Only the item the preparation answered, carried back whole,
			// has subtypes; and it has them whatever its kind, so a test
			// sees the service ask for an interface's alone. One of them
			// stands on the line of the declaration's usage in def.go, one
			// outside the project.
			if params.Item.Data != "asked" {
				respond(msg.ID, nil)
				continue
			}
			sub := func(uri string, line, character int) map[string]any {
				span := lspRange{Start: lspPosition{Line: line, Character: character}, End: lspPosition{Line: line, Character: character + 4}}
				return map[string]any{"name": "Impl", "kind": symbolStruct, "uri": uri, "range": span, "selectionRange": span}
			}
			respond(msg.ID, []map[string]any{
				sub(rootURI+"/impl.go", 2, 5),
				sub(rootURI+"/def.go", 1, 5),
				sub("file:///outside/impl.go", 0, 5),
			})
		case "textDocument/implementation":
			var params struct {
				TextDocument struct {
					URI string `json:"uri"`
				} `json:"textDocument"`
				Position lspPosition `json:"position"`
			}
			_ = json.Unmarshal(msg.Params, &params)
			uri := params.TextDocument.URI
			respond(msg.ID, fakeImplementations(uri, fakeWordAt(texts[uri], params.Position)))
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
		case "workspace/symbol":
			var params struct {
				Query string          `json:"query"`
				Token json.RawMessage `json:"partialResultToken"`
			}
			_ = json.Unmarshal(msg.Params, &params)
			server := os.Getenv("FAKE_LSP_SERVER")
			if server == "gopls" {
				respond(msg.ID, fakeSymbols(rootURI, params.Query, indexed.Load(), mode == "crowded"))
				continue
			}
			// The other servers answer per file, the way pyright streams
			// its parts when the request carries a token.
			files := fakeLanguageSymbols(server, rootURI, params.Query, fakeSymbolState{
				open: len(docs) > 0, indexed: indexed.Load(), libraries: libraries, crowded: mode == "crowded",
			})
			all := []map[string]any{}
			for _, file := range files {
				if server == "pyright-langserver" && params.Token != nil {
					note := rpcMessage{JSONRPC: "2.0", Method: "$/progress",
						Params: mustMarshal(map[string]any{"token": params.Token, "value": file})}
					payload, _ := json.Marshal(note)
					writeMu.Lock()
					_ = writeFrame(os.Stdout, payload)
					writeMu.Unlock()
					continue
				}
				all = append(all, file...)
			}
			respond(msg.ID, all)
		case "textDocument/documentSymbol":
			var params struct {
				TextDocument struct {
					URI string `json:"uri"`
				} `json:"textDocument"`
			}
			_ = json.Unmarshal(msg.Params, &params)
			uri := params.TextDocument.URI
			if _, open := docs[uri]; !open {
				respond(msg.ID, []any{})
				continue
			}
			respond(msg.ID, fakeDocumentSymbols(uri, texts[uri]))
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
					// A server that never announces its indexing.
					indexed.Store(true)
				case "late":
					// The announcement comes a while after the handshake,
					// as intelephense's does on a starved CPU, and the
					// answers stay partial until the run it names ended.
					go func() {
						time.Sleep(1200 * time.Millisecond)
						progress("begin", -1)
						time.Sleep(200 * time.Millisecond)
						indexed.Store(true)
						progress("end", -1)
					}()
				case "lists-late":
					// Silent as well, and partial until the didOpen above
					// publishes its diagnostics.
				default:
					indexed.Store(true)
					progress("begin", -1)
					progress("end", -1)
				}
			}
		}
	}
}

// fakeWordAt answers the word at the position of the text.
func fakeWordAt(text string, at lspPosition) string {
	rows := strings.Split(text, "\n")
	if at.Line >= len(rows) {
		return ""
	}
	row := rows[at.Line]
	start, end := at.Character, at.Character
	for start > 0 && isIdentifierRune(rune(row[start-1])) {
		start--
	}
	for end < len(row) && isIdentifierRune(rune(row[end])) {
		end++
	}
	return row[start:end]
}

// fakeWordReferences answers every place the word at the position stands in
// the text, on a word's bounds.
func fakeWordReferences(uri, text string, at lspPosition) []lspLocation {
	word := fakeWordAt(text, at)
	if word == "" {
		return nil
	}
	var locs []lspLocation
	for n, row := range strings.Split(text, "\n") {
		for i := 0; i+len(word) <= len(row); i++ {
			if row[i:i+len(word)] == word && (i == 0 || !isIdentifierRune(rune(row[i-1]))) && (i+len(word) == len(row) || !isIdentifierRune(rune(row[i+len(word)]))) {
				locs = append(locs, lspLocation{URI: uri, Range: lspRange{Start: lspPosition{Line: n, Character: i}, End: lspPosition{Line: n, Character: i + len(word)}}})
			}
		}
	}
	return locs
}

// fakeFiles answers the files of the workspace with the extension by their
// URIs, the open documents' texts over the ones on disk.
func fakeFiles(rootURI string, texts map[string]string, ext string) map[string]string {
	files := map[string]string{}
	root := strings.TrimPrefix(rootURI, "file://")
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ext) {
			if data, err := os.ReadFile(p); err == nil {
				files["file://"+p] = string(data)
			}
		}
		return nil
	})
	for uri, text := range texts {
		if strings.HasSuffix(uri, ext) {
			files[uri] = text
		}
	}
	return files
}

// fakePythonImplementations reports whether a Python document is one of
// the implementation tests, which the fake answers off the files.
func fakePythonImplementations(uri string) bool {
	return strings.HasSuffix(uri, ".py") && strings.Contains(uri, "/shapes/")
}

// fakeFilesReferences answers every place the word at the position of the
// text stands in the files, on a word's bounds, the way intelephense and
// pyright answer a type's: a name an import renames it to is a word of its
// own. intelephense finds nothing at a position in a file it has not opened
// since its start, and its caller hands an empty text for one.
func fakeFilesReferences(files map[string]string, text string, at lspPosition) []lspLocation {
	word := fakeWordAt(text, at)
	if word == "" {
		return nil
	}
	var locs []lspLocation
	for file, text := range files {
		for n, row := range strings.Split(text, "\n") {
			for i := 0; i+len(word) <= len(row); i++ {
				if row[i:i+len(word)] == word && (i == 0 || !isIdentifierRune(rune(row[i-1]))) && (i+len(word) == len(row) || !isIdentifierRune(rune(row[i+len(word)]))) {
					locs = append(locs, lspLocation{URI: file, Range: lspRange{Start: lspPosition{Line: n, Character: i}, End: lspPosition{Line: n, Character: i + len(word)}}})
				}
			}
		}
	}
	return locs
}

// fakeImplementations answers tsgo's implementation request over the text of
// implementationsTS by the asked word, in its shapes: a class or an
// interface by its name, a value typed with the type by the value. On the
// class Square the class itself and the two classes extending it, on the
// class Tile the class alone; on every other word what the interface Shape
// has, its classes, the one extending Square too, the object literal typed
// with it, one answer outside the project and a class of the standard
// library in the image.
func fakeImplementations(asked, word string) []lspLocation {
	at := func(uri string, line, character, length int) lspLocation {
		return lspLocation{URI: uri, Range: lspRange{Start: lspPosition{Line: line, Character: character}, End: lspPosition{Line: line, Character: character + length}}}
	}
	switch word {
	case "Square":
		return []lspLocation{at(asked, 1, 13, 6), at(asked, 2, 13, 4), at(asked, 3, 13, 4)}
	case "Tile":
		return []lspLocation{at(asked, 2, 13, 4)}
	}
	return []lspLocation{
		at(asked, 1, 13, 6),
		at(asked, 2, 13, 4),
		at(asked, 3, 13, 4),
		at(asked, 6, 27, 17),
		at("file:///outside/impl.ts", 0, 13, 4),
		at("file://"+fakeTSLib, 39, 23, 13),
	}
}

// logFakeRequest appends the request to the file FAKE_LSP_LOG names, one
// line each: the server the fake plays, the method, then the query of a
// workspace symbol request or the file of a document's open, close and
// symbols.
func logFakeRequest(msg rpcMessage) {
	file := os.Getenv("FAKE_LSP_LOG")
	if file == "" || msg.Method == "" {
		return
	}
	var params struct {
		Query        string `json:"query"`
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	_ = json.Unmarshal(msg.Params, &params)
	line := os.Getenv("FAKE_LSP_SERVER") + " " + msg.Method
	switch msg.Method {
	case "workspace/symbol":
		line += " " + params.Query
	case "textDocument/didOpen", "textDocument/didClose", "textDocument/documentSymbol":
		line += " " + filepath.Base(params.TextDocument.URI)
	default:
		return
	}
	out, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer out.Close()
	fmt.Fprintln(out, line)
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
		script := fmt.Sprintf("#!/bin/sh\nexec env GO_WANT_FAKE_LSP=1 FAKE_LSP_MODE=%s FAKE_LSP_SERVER=%s %q\n", mode, name, exe)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// fakeImageFiles are what the servers' images hold for the tests: the
// standard library files whose lines a type search reads, the lines in
// between empty.
var fakeImageFiles = map[string]string{
	"/usr/local/go/src/io/io.go":           fakeLines(map[int]string{86: "type Reader interface {", 87: "\tRead(p []byte) (n int, err error)", 88: "}"}),
	"/usr/local/go/src/net/http/server.go": fakeLines(map[int]string{2282: "type HandlerFunc func(ResponseWriter, *Request)"}),
	"/usr/local/go/src/bytes/reader.go":    fakeLines(map[int]string{160: "func NewReader(b []byte) *Reader { return &Reader{b, 0, -1} }"}),
	fakeTSLib: fakeLines(map[int]string{
		30: "type OrderLibAlias = string | number;",
		40: "declare abstract class OrderLibClass {}",
	}),
}

// fakeTSLib is the standard library file of tsgo's image in the tests.
const fakeTSLib = "/usr/local/lib/node_modules/@typescript/native-preview/lib/lib.es5.d.ts"

// fakeLines is a file holding the given 1-based lines, as long as the last
// of them.
func fakeLines(lines map[int]string) string {
	last := 0
	for n := range lines {
		last = max(last, n)
	}
	all := make([]string, last)
	for n, text := range lines {
		all[n-1] = text
	}
	return strings.Join(all, "\n") + "\n"
}

// installFakeImage puts the files under a directory standing in for every
// server's image and a tar on PATH that archives them from there, which is
// what the fake docker runs for a read out of an image. A file the image
// does not hold is left out of the archive, as the real tar leaves it.
func installFakeImage(t *testing.T, files map[string]string) {
	t.Helper()
	image, bin := t.TempDir(), t.TempDir()
	for p, content := range files {
		target := filepath.Join(image, p)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	realTar, err := exec.LookPath("tar")
	if err != nil {
		t.Skip("no tar on this host")
	}
	script := fmt.Sprintf(`#!/bin/sh
shift 3
n=$#
while [ $n -gt 0 ]; do set -- "$@" "${1#/}"; shift; n=$((n-1)); done
cd %q && exec %q -cf - -- "$@" 2>/dev/null
`, image, realTar)
	if err := os.WriteFile(filepath.Join(bin, "tar"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
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
	var refs, images []string
	for _, p := range profiles {
		refs, images = append(refs, imageRef(p)), append(images, p.container.Image+":*")
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
        %s)
          shift
          if [ -n "$ep" ]; then exec "$ep" "$@"; else exec "$1"; fi ;;
      esac
      shift
    done
    exit 1 ;;
  *) exit 1 ;;
esac
`, argsFile, markerFile, markerFile, psFile, psFile, volsFile, volsFile, markerFile, strings.Join(refs, " "), strings.Join(images, "|"))
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
