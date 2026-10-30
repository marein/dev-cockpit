package editorintelligence

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/filesystem"
)

// The project the language tests write: a PHP, a TypeScript and a Python
// file of the project's own, one file of each in the folder its language
// installs dependencies into, and a script in the PHP one.
var langTypesFiles = map[string]string{
	"src/Shop/Order.php": `<?php

namespace Shop;

/**
 * An order.
 */
#[Entity]
final class Order extends BaseOrder
{
    public function total(): int { return 0; }
}

interface Payable {}

trait Loggable {}

enum OrderState: string { case Open = 'open'; }

function order_helper(): void {}

const ORDER_MAX = 5;
`,
	"composer.json":               `{"autoload": {"psr-4": {"Shop\\": "src/Shop/"}}}`,
	"ideas/Draft.php":             "<?php\n\nnamespace Draft;\n\nclass OrderDraft {}\n",
	"legacy/OrderPrinter.php":     "<?php\n\n/** Prints an order. */\nclass OrderPrinter {}\n",
	"legacy/page.php":             "<?php $title = 'Orders'; ?>\n<style>.order-row { color: red; }</style>\n<script>class OrderScript {}</script>\n",
	"legacy/helpers.php":          "<?php\n\nfunction order_total(): int { return 0; }\n",
	"vendor/acme/VendorOrder.php": "<?php\n\nclass VendorOrder {}\n",
	"web/order.ts": `export interface OrderLike { id: number }
export class OrderModel implements OrderLike { id = 0 }
export enum OrderKind { A }
export type OrderId = string | number;
namespace OrderSpace {}
export const orderConst = 1;
`,
	"web/node_modules/dep/order.js":   "export class DepOrder {}\n",
	"web/node_modules/dep/order.d.ts": "export interface OrderDepTypes {}\n",
	"vendor/acme/order.js":            "export class VendorScriptOrder {}\n",
	"py/orders.py": `class OrderBase:
    class Nested:
        pass


OrderAlias = OrderBase


def order_func():
    pass
`,
	"py/venv/lib/python3.12/site-packages/dep/__init__.py": "class VenvOrder:\n    pass\n",
}

// fakeSymbolState is what a fake server's workspace symbols depend on:
// whether a document is open, its workspace indexed, its library symbols
// switched on, and whether a library crowds its limit.
type fakeSymbolState struct {
	open, indexed, libraries, crowded bool
}

// fakeLanguageSymbols answers workspace/symbol for the servers of the other
// languages over the project above, file by file, with the kinds, names,
// containers and ranges each real server answered: intelephense's ranges
// start at the doc comment and a namespace asked for names the types under
// it qualified, tsgo answers only once a document is open and names the
// declaration files, its standard library's among them, only once switched
// to, and pyright answers only once its workspace is listed. None of them
// matches a space or an upper case letter, which is how a query they must
// not see shows. Crowded, the vendor folder fills intelephense's limit and
// node_modules tsgo's.
func fakeLanguageSymbols(server, rootURI, query string, state fakeSymbolState) [][]map[string]any {
	if strings.ContainsAny(query, " ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
		return nil
	}
	sym := func(name string, kind int, container, path string, line, char, endLine int) map[string]any {
		uri := rootURI + "/" + path
		if filepath.IsAbs(path) {
			uri = "file://" + path
		}
		s := map[string]any{
			"name": name, "kind": kind,
			"location": map[string]any{"uri": uri, "range": map[string]any{
				"start": map[string]any{"line": line, "character": char},
				"end":   map[string]any{"line": endLine, "character": 1},
			}},
		}
		if container != "" {
			s["containerName"] = container
		}
		return s
	}
	crowd := func(n, kind int, path string) []map[string]any {
		many := make([]map[string]any, 0, n)
		for i := range n {
			many = append(many, sym(fmt.Sprintf("OrderLib%04d", i), kind, "", path, 0, 0, 0))
		}
		return many
	}
	switch server {
	case "intelephense":
		if strings.HasPrefix(query, `draft\`) {
			return [][]map[string]any{{sym(`Draft\OrderDraft`, symbolClass, "", "ideas/Draft.php", 4, 0, 4)}}
		}
		if strings.Contains(query, `\`) {
			if !strings.HasPrefix(query, `shop\`) {
				return nil
			}
			return [][]map[string]any{{
				sym(`Shop\Order`, symbolClass, "", "src/Shop/Order.php", 4, 0, 11),
				sym(`Shop\Payable`, symbolInterface, "", "src/Shop/Order.php", 13, 0, 13),
				sym(`Shop\Loggable`, symbolModule, "", "src/Shop/Order.php", 15, 0, 15),
				sym(`Shop\OrderState`, symbolEnum, "", "src/Shop/Order.php", 17, 0, 17),
				sym(`Shop\order_helper`, symbolFunction, "", "src/Shop/Order.php", 19, 0, 19),
				sym(`Shop\ORDER_MAX`, 14, "", "src/Shop/Order.php", 21, 6, 21),
			}}
		}
		if state.crowded {
			return [][]map[string]any{crowd(1000, symbolClass, "vendor/acme/VendorOrder.php")}
		}
		return [][]map[string]any{
			{
				sym("Order", symbolClass, "Shop", "src/Shop/Order.php", 4, 0, 11),
				sym("total", 6, `Shop\Order`, "src/Shop/Order.php", 10, 4, 10),
				sym("Payable", symbolInterface, "Shop", "src/Shop/Order.php", 13, 0, 13),
				sym("Loggable", symbolModule, "Shop", "src/Shop/Order.php", 15, 0, 15),
				sym("OrderState", symbolEnum, "Shop", "src/Shop/Order.php", 17, 0, 17),
				sym("Open", 22, `Shop\OrderState`, "src/Shop/Order.php", 17, 26, 17),
				sym("order_helper", symbolFunction, "Shop", "src/Shop/Order.php", 19, 0, 19),
				sym("ORDER_MAX", 14, "Shop", "src/Shop/Order.php", 21, 6, 21),
			},
			{sym("VendorOrder", symbolClass, "", "vendor/acme/VendorOrder.php", 2, 0, 2)},
		}
	case "tsgo":
		if !state.open {
			return nil
		}
		own := [][]map[string]any{
			{
				sym("OrderLike", symbolInterface, "", "web/order.ts", 0, 17, 0),
				sym("OrderModel", symbolClass, "", "web/order.ts", 1, 13, 1),
				sym("OrderKind", symbolEnum, "", "web/order.ts", 2, 12, 2),
				sym("OrderId", symbolClass, "", "web/order.ts", 3, 12, 3),
				sym("OrderSpace", 3, "", "web/order.ts", 4, 10, 4),
				sym("orderConst", 13, "", "web/order.ts", 5, 13, 5),
			},
			{sym("DepOrder", symbolClass, "", "web/node_modules/dep/order.js", 0, 13, 0)},
			{sym("VendorScriptOrder", symbolClass, "", "vendor/acme/order.js", 0, 13, 0)},
		}
		switch {
		case !state.libraries:
			return own
		case state.crowded:
			return [][]map[string]any{crowd(256, symbolInterface, "web/node_modules/dep/order.d.ts")}
		}
		return append(own,
			[]map[string]any{sym("OrderDepTypes", symbolInterface, "", "web/node_modules/dep/order.d.ts", 0, 17, 0)},
			[]map[string]any{
				sym("OrderLibAlias", symbolClass, "", fakeTSLib, 29, 5, 29),
				sym("OrderLibClass", symbolClass, "", fakeTSLib, 39, 23, 39),
			})
	case "pyright-langserver":
		if !state.indexed {
			return nil
		}
		return [][]map[string]any{
			{
				sym("OrderBase", symbolClass, "", "py/orders.py", 0, 6, 2),
				sym("Nested", symbolClass, "OrderBase", "py/orders.py", 1, 10, 2),
				sym("OrderAlias", 13, "", "py/orders.py", 5, 0, 5),
				sym("order_func", symbolFunction, "", "py/orders.py", 8, 4, 9),
			},
			{sym("VenvOrder", symbolClass, "", "py/venv/lib/python3.12/site-packages/dep/__init__.py", 0, 6, 1)},
		}
	}
	return nil
}

// fakeDocumentSymbols answers intelephense's document symbols of an open
// document the way the real one does: a class of the PHP code as a document
// symbol on its name, a script's class and a style's selector of an HTML
// part in the plain shape with a location.
func fakeDocumentSymbols(uri, text string) []map[string]any {
	symbols := []map[string]any{}
	point := func(line, char int) map[string]any { return map[string]any{"line": line, "character": char} }
	for n, line := range strings.Split(text, "\n") {
		if name, ok := strings.CutPrefix(line, "class "); ok {
			name = strings.Fields(name)[0]
			symbols = append(symbols, map[string]any{
				"name": name, "kind": symbolClass,
				"range":          map[string]any{"start": point(n, 0), "end": point(n, len(line))},
				"selectionRange": map[string]any{"start": point(n, 6), "end": point(n, 6+len(name))},
			})
		}
		for _, html := range []struct{ open, name string }{{"<script>class ", ""}, {"<style>", ""}} {
			at := strings.Index(line, html.open)
			if at < 0 {
				continue
			}
			name := strings.Fields(line[at+len(html.open):])[0]
			symbols = append(symbols, map[string]any{
				"name": name, "kind": symbolClass,
				"location": map[string]any{"uri": uri, "range": map[string]any{"start": point(n, at), "end": point(n, len(line))}},
			})
		}
	}
	return symbols
}

func writeLangTypesProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range langTypesFiles {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func installLangTypesServers(t *testing.T) {
	t.Helper()
	installFakeLSP(t, "normal", "gopls", "intelephense", "tsgo")
	installFakeLSP(t, "lists-late", "pyright-langserver")
}

func langTypesRequest(root, query string) TypesRequest {
	return TypesRequest{
		Client:      "client-a",
		ProjectName: "proj",
		ProjectRoot: root,
		Query:       query,
		Profiles:    []WarmMode{{ProfileID: "php"}, {ProfileID: "typescript"}, {ProfileID: "python"}},
	}
}

// Every language's own types come in one list ranked by the quick open
// rule, each with the word of its kind and the place of its name: a PHP
// type on its name below the doc comment and the attribute. What is no
// type stays out. Whatever lies where a package manager installs, and
// tsgo's standard library, read off the image, follow as the libraries'.
// tsgo answers through a file the search opens and closes again, pyright
// through one it opens until its workspace is listed, streaming its answer.
func TestServiceTypesListsTheTypesOfEveryLanguage(t *testing.T) {
	installLangTypesServers(t)
	s := newTestService(t)
	root := writeLangTypesProject(t)

	res := s.Types(context.Background(), langTypesRequest(root, "order"))
	want := strings.Join([]string{
		"class Order Shop src/Shop/Order.php:9:12",
		"type OrderId  web/order.ts:4:12",
		"class OrderBase  py/orders.py:1:6",
		"enum OrderKind  web/order.ts:3:12",
		"interface OrderLike  web/order.ts:1:17",
		"class OrderModel  web/order.ts:2:13",
		"enum OrderState Shop src/Shop/Order.php:18:5",
		"class Nested OrderBase py/orders.py:2:10",
		"interface Payable Shop src/Shop/Order.php:14:10",
		"trait Loggable Shop src/Shop/Order.php:16:6",
		"interface OrderDepTypes  web/node_modules/dep/order.d.ts:1:17 library",
		"type OrderLibAlias  " + fakeTSLib + ":30:5 library external",
		"class OrderLibClass  " + fakeTSLib + ":40:23 library external",
		"class DepOrder  web/node_modules/dep/order.js:1:13 library",
		"class VenvOrder  py/venv/lib/python3.12/site-packages/dep/__init__.py:1:6 library",
		"class VendorOrder  vendor/acme/VendorOrder.php:3:6 library",
		"class VendorScriptOrder  vendor/acme/order.js:1:13 library",
	}, "\n")
	if got := typeRows(res); got != want {
		t.Fatalf("types:\n%s\nwant:\n%s", got, want)
	}
	if res.Truncated || len(res.Statuses) != 0 {
		t.Fatalf("a whole answer: %+v", res)
	}
	languages := map[string]string{}
	for _, ty := range res.Types {
		languages[ty.Name] = ty.Language
	}
	if languages["Order"] != "PHP" || languages["OrderId"] != "JS / TS" || languages["OrderBase"] != "Python" {
		t.Fatalf("languages: %v", languages)
	}
	for _, ext := range []string{"x.ts", "x.py"} {
		mc := slotOf(s, "proj", ext)
		mc.conn.docMu.Lock()
		open := len(mc.conn.docVersion)
		mc.conn.docMu.Unlock()
		if open != 0 {
			t.Fatalf("%s: the search left %d documents open", ext, open)
		}
	}
}

// A query of several words asks a server that reads one word with each of
// them apart, in lower case, and the rule weighs the others against the
// name, the namespace and the path, so the words match in any order. However
// many words are typed, the requests stay bounded.
func TestServiceTypesAsksAOneWordServerEveryWord(t *testing.T) {
	installLangTypesServers(t)
	requests := filepath.Join(t.TempDir(), "requests")
	t.Setenv("FAKE_LSP_LOG", requests)
	s := newTestService(t)
	root := writeLangTypesProject(t)

	for query, want := range map[string][]string{
		"Order shop":       {"Order", "OrderState", "Payable", "Loggable"},
		"shop Order":       {"Order", "Payable", "Loggable", "OrderState"},
		"orders.OrderBase": {"OrderBase", "Nested"},
	} {
		var got []string
		for _, ty := range s.Types(context.Background(), langTypesRequest(root, query)).Types {
			got = append(got, ty.Name)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("%s: %v, want %v ranked by the first word", query, got, want)
		}
	}
	for _, server := range []string{"intelephense", "pyright-langserver"} {
		asked := fakeRequests(t, requests, server)
		if !strings.Contains(asked, "workspace/symbol order\n") || !strings.Contains(asked, "workspace/symbol shop\n") || strings.Contains(asked, " shop order") || strings.Contains(asked, " order shop") {
			t.Fatalf("%s's requests:\n%s", server, asked)
		}
	}

	if err := os.WriteFile(requests, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s.Types(context.Background(), langTypesRequest(root, "aaa bbb ccc ddd eee fff ggg"))
	for server, want := range map[string]int{"intelephense": 1 + maxQueryWords, "tsgo": 2 * maxQueryWords, "pyright-langserver": maxQueryWords} {
		if n := strings.Count(fakeRequests(t, requests, server), "workspace/symbol "); n != want {
			t.Fatalf("%s was asked %d times", server, n)
		}
	}
}

// intelephense is asked the words as one namespace and the first word
// alone: a namespace typed in front of the name finds the types under it
// in that one answer, named qualified and listed by their short name in
// their namespace. Where the namespace's answer holds no type the query
// matches, each other word is asked apart.
func TestServiceTypesAsksIntelephenseTheWordsAsANamespace(t *testing.T) {
	installLangTypesServers(t)
	requests := filepath.Join(t.TempDir(), "requests")
	t.Setenv("FAKE_LSP_LOG", requests)
	s := newTestService(t)
	root := writeLangTypesProject(t)
	search := func(client, query string) ([]TypeSymbol, string) {
		t.Helper()
		if err := os.WriteFile(requests, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		req := langTypesRequest(root, query)
		req.Client, req.Profiles = client, []WarmMode{{ProfileID: "php"}}
		return s.Types(context.Background(), req).Types, fakeRequests(t, requests, "intelephense")
	}

	types, asked := search("a", "Shop Order")
	if asked != "workspace/symbol shop\nworkspace/symbol shop\\order\n" {
		t.Fatalf("intelephense's requests:\n%s", asked)
	}
	var rows []string
	for _, ty := range types {
		rows = append(rows, ty.Kind+" "+ty.Name+" "+ty.Container)
	}
	if strings.Join(rows, ",") != "class Order Shop,interface Payable Shop,trait Loggable Shop,enum OrderState Shop" {
		t.Fatalf("rows %v", rows)
	}

	if _, asked := search("b", "Order Shop"); !strings.Contains(asked, "workspace/symbol order\\shop\n") || !strings.Contains(asked, "workspace/symbol order\n") || !strings.Contains(asked, "workspace/symbol shop\n") {
		t.Fatalf("no fall back to the words apart:\n%s", asked)
	}

	// A qualified name is that namespace as typed, its own name asked alone,
	// and a part of it finds the same.
	for client, query := range map[string]string{"c": `\Shop\Order`, "d": `Shop\Ord`} {
		types, asked := search(client, query)
		lower := strings.ToLower(strings.TrimPrefix(query, `\`))
		if asked != "workspace/symbol "+lower[strings.LastIndexByte(lower, '\\')+1:]+"\nworkspace/symbol "+lower+"\n" {
			t.Fatalf("%s: intelephense's requests:\n%s", query, asked)
		}
		if len(types) == 0 || types[0].Kind+" "+types[0].Name+" "+types[0].Container != "class Order Shop" {
			t.Fatalf("%s: %+v", query, types)
		}
	}
}

// tsgo names a library's symbols only in a project with TS files, so a
// project of JavaScript alone asks it once per word, a declaration file in
// node_modules notwithstanding, and twice once a TS file of its own came,
// which a search notices after libraryFilesFor.
func TestServiceTypesAsksTsgoOnceAWordWithoutTSFiles(t *testing.T) {
	installFakeLSP(t, "normal", "tsgo")
	requests := filepath.Join(t.TempDir(), "requests")
	t.Setenv("FAKE_LSP_LOG", requests)
	s := newTestService(t)
	root := t.TempDir()
	for name, content := range map[string]string{
		"package.json":                    "{}",
		"web/widget.js":                   "export class OrderWidget {}\n",
		"web/node_modules/dep/order.d.ts": "export interface OrderDepTypes {}\n",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	asked := func(client, query string) int {
		t.Helper()
		if err := os.WriteFile(requests, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		req := langTypesRequest(root, query)
		req.Client, req.Profiles = client, []WarmMode{{ProfileID: "typescript"}}
		s.Types(context.Background(), req)
		return strings.Count(fakeRequests(t, requests, "tsgo"), "workspace/symbol ")
	}

	if n := asked("a", "order widget"); n != 2 {
		t.Fatalf("tsgo was asked %d times for two words of a project without TS files", n)
	}
	if err := os.WriteFile(filepath.Join(root, "web/order.ts"), []byte("export type OrderId = string;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n := asked("b", "order widget"); n != 2 {
		t.Fatalf("the walk's answer stood %d asks short of libraryFilesFor", n)
	}
	s.now = func() time.Time { return time.Now().Add(libraryFilesFor) }
	if n := asked("c", "order widget"); n != 4 {
		t.Fatalf("tsgo was asked %d times for two words of a project with a TS file", n)
	}
}

// A client's next search asks only what its last one did not: a word that
// stays as it was typed keeps its answer for typeReuseFor, then it is asked
// again, and another client asks it itself.
func TestServiceTypesReusesTheWordsOfTheLastSearch(t *testing.T) {
	installLangTypesServers(t)
	requests := filepath.Join(t.TempDir(), "requests")
	t.Setenv("FAKE_LSP_LOG", requests)
	s := newTestService(t)
	root := writeLangTypesProject(t)
	asked := func(req TypesRequest) (string, string) {
		t.Helper()
		if err := os.WriteFile(requests, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, ty := range s.Types(context.Background(), req).Types {
			names = append(names, ty.Name)
		}
		return strings.Join(names, ","), fakeRequests(t, requests, "intelephense") + fakeRequests(t, requests, "tsgo") + fakeRequests(t, requests, "pyright-langserver")
	}

	asked(langTypesRequest(root, "order"))
	names, got := asked(langTypesRequest(root, "order shop"))
	if names != "Order,OrderState,Payable,Loggable" {
		t.Fatalf("types %s", names)
	}
	if strings.Contains(got, "workspace/symbol order\n") || strings.Count(got, "workspace/symbol shop\n") != 4 {
		t.Fatalf("the unchanged word was asked again:\n%s", got)
	}
	other := langTypesRequest(root, "order shop")
	other.Client = "client-b"
	if _, got := asked(other); !strings.Contains(got, "workspace/symbol order\n") {
		t.Fatalf("another client's search was answered from this one's:\n%s", got)
	}
	s.now = func() time.Time { return time.Now().Add(typeReuseFor) }
	if _, got := asked(langTypesRequest(root, "order shop")); !strings.Contains(got, "workspace/symbol order\n") || !strings.Contains(got, "workspace/symbol shop\n") {
		t.Fatalf("an answer stood longer than typeReuseFor:\n%s", got)
	}
}

// A library whose symbols fill a server's limit takes none of the project's
// own types, which the other requests bring and which stand first: in PHP
// those of the autoloaded namespace and of one only a file declares, asked
// apart, and the one of the global namespace, read off its file's document
// symbols, while a script's class and a style's selector of an HTML part
// stay out. The files read are kept until they change.
func TestServiceTypesKeepsTheOwnTypesWhenALibraryFillsTheLimit(t *testing.T) {
	installFakeLSP(t, "crowded", "intelephense", "tsgo")
	installFakeLSP(t, "lists-late", "pyright-langserver")
	requests := filepath.Join(t.TempDir(), "requests")
	t.Setenv("FAKE_LSP_LOG", requests)
	s := newTestService(t)
	root := writeLangTypesProject(t)

	res := s.Types(context.Background(), langTypesRequest(root, "order"))
	if len(res.Types) != maxTypes || !res.Truncated {
		t.Fatalf("a cut list of %d: %v", len(res.Types), res.Truncated)
	}
	want := strings.Join([]string{
		"class Order Shop src/Shop/Order.php:9:12",
		"type OrderId  web/order.ts:4:12",
		"class OrderBase  py/orders.py:1:6",
		"enum OrderKind  web/order.ts:3:12",
		"interface OrderLike  web/order.ts:1:17",
		"class OrderDraft Draft ideas/Draft.php:5:6",
		"class OrderModel  web/order.ts:2:13",
		"enum OrderState Shop src/Shop/Order.php:18:5",
		"class OrderPrinter  legacy/OrderPrinter.php:4:6",
		"class Nested OrderBase py/orders.py:2:10",
		"interface Payable Shop src/Shop/Order.php:14:10",
		"trait Loggable Shop src/Shop/Order.php:16:6",
	}, "\n")
	if got := typeRows(TypesResult{Types: res.Types[:12]}); got != want {
		t.Fatalf("own types:\n%s\nwant:\n%s", got, want)
	}
	for _, ty := range res.Types[12:] {
		if !ty.Library {
			t.Fatalf("an own type behind the libraries': %+v", ty)
		}
	}
	php := fakeRequests(t, requests, "intelephense")
	for _, line := range []string{`workspace/symbol order`, `workspace/symbol shop\order`, `workspace/symbol draft\order`,
		"textDocument/didOpen OrderPrinter.php", "textDocument/documentSymbol OrderPrinter.php", "textDocument/didClose OrderPrinter.php",
		"textDocument/didOpen page.php"} {
		if !strings.Contains(php, line+"\n") {
			t.Fatalf("no %q among intelephense's requests:\n%s", line, php)
		}
	}
	if strings.Contains(php, "helpers.php") || strings.Contains(php, "Order.php") || strings.Contains(php, "Draft.php") {
		t.Fatalf("a file without a type of the global namespace was opened:\n%s", php)
	}

	if err := os.WriteFile(requests, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if res := s.Types(context.Background(), langTypesRequest(root, "order")); typeRows(TypesResult{Types: res.Types[:12]}) != want {
		t.Fatalf("the second search:\n%s", typeRows(res))
	}
	if php := fakeRequests(t, requests, "intelephense"); strings.Contains(php, "didOpen") {
		t.Fatalf("the second search opened a file again:\n%s", php)
	}
	printer := filepath.Join(root, "legacy/OrderPrinter.php")
	if err := os.WriteFile(printer, []byte("<?php\n\nclass OrderPrinter {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res = s.Types(context.Background(), langTypesRequest(root, "order printer"))
	if got := typeRows(TypesResult{Types: res.Types[:1]}); got != "class OrderPrinter  legacy/OrderPrinter.php:3:6" {
		t.Fatalf("a changed file is read again: %s", got)
	}
}

// Below the server's limit its answer holds every match, and nothing more
// is asked.
func TestServiceTypesAsksNothingMoreBelowTheLimit(t *testing.T) {
	installLangTypesServers(t)
	requests := filepath.Join(t.TempDir(), "requests")
	t.Setenv("FAKE_LSP_LOG", requests)
	s := newTestService(t)
	root := writeLangTypesProject(t)

	s.Types(context.Background(), langTypesRequest(root, "order"))
	if php := fakeRequests(t, requests, "intelephense"); php != "workspace/symbol order\n" {
		t.Fatalf("intelephense's requests:\n%s", php)
	}
}

// A search opens at most typeDocumentsPerSearch files of the global
// namespace, those named like the query first, says that more may match,
// and the next search reads the rest.
func TestServiceTypesOpensABoundOfGlobalFilesPerSearch(t *testing.T) {
	installFakeLSP(t, "crowded", "intelephense")
	requests := filepath.Join(t.TempDir(), "requests")
	t.Setenv("FAKE_LSP_LOG", requests)
	s := newTestService(t)
	root := writeLangTypesProject(t)
	for i := range typeDocumentsPerSearch + 4 {
		name := fmt.Sprintf("OrderMany%02d", i)
		if err := os.WriteFile(filepath.Join(root, "legacy", fmt.Sprintf("many%02d.php", i)), []byte("<?php\n\nclass "+name+" {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	req := langTypesRequest(root, "order")
	req.Profiles = []WarmMode{{ProfileID: "php"}}
	own := func(res TypesResult) []string {
		var names []string
		for _, ty := range res.Types {
			if !ty.Library && strings.HasPrefix(ty.Path, "legacy/") {
				names = append(names, ty.Name)
			}
		}
		return names
	}
	res := s.Types(context.Background(), req)
	if got := own(res); len(got) != typeDocumentsPerSearch || !slices.Contains(got, "OrderPrinter") || !res.Truncated {
		t.Fatalf("the first search: %v %v", got, res.Truncated)
	}
	if opened := strings.Count(fakeRequests(t, requests, "intelephense"), "didOpen"); opened != typeDocumentsPerSearch {
		t.Fatalf("%d files opened", opened)
	}
	res = s.Types(context.Background(), req)
	if got := own(res); len(got) != typeDocumentsPerSearch+5 {
		t.Fatalf("the second search reads the rest: %v", got)
	}
}

// fakeRequests reads the requests the fake servers logged, those of the
// server alone.
func fakeRequests(t *testing.T, file, server string) string {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, server+" "); ok {
			lines = append(lines, rest)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// The folders the search settings exclude hold none of the types, a
// dependency's among them, and a server that answers from an open document
// is not asked at all when no file of the language is left to open, which
// it would wait for in vain.
func TestServiceTypesStaysOutOfExcludedFolders(t *testing.T) {
	installLangTypesServers(t)
	s := newTestService(t)
	root := writeLangTypesProject(t)

	req := langTypesRequest(root, "order")
	req.Exclusions = filesystem.ParseExclusions("src/Shop\npy\nweb\nvendor")
	start := time.Now()
	if res := s.Types(context.Background(), req); len(res.Types) != 0 || len(res.Statuses) != 0 {
		t.Fatalf("types: %s %+v", typeRows(res), res.Statuses)
	}
	if took := time.Since(start); took > 20*time.Second {
		t.Fatalf("the search waited %s", took)
	}
	if slotOf(s, "proj", "x.ts") != nil || slotOf(s, "proj", "x.py") != nil {
		t.Fatal("a server was started for a language without own files")
	}
}

// The search opens a file only where the server needs one and none is open
// yet: pyright never again once its workspace is listed, tsgo whenever no
// document of it is open, then the shallowest of the project's own files.
func TestOpenTypeAnchor(t *testing.T) {
	root := writeLangTypesProject(t)
	for name, content := range map[string]string{"web/a.ts": "", "deep/x/y.ts": "", "z.js": "", "node_modules/a.ts": "", "bundle.js": strings.Repeat("x", anchorMaxBytes+1)} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ts, py := profileByID("typescript"), profileByID("python")
	open := func(c *lspConn, p *Profile) string {
		rel, ok := typeAnchor(root, p, skipDir(filesystem.Exclusions{}))
		if !ok {
			t.Fatalf("%s: no anchor", p.ID)
		}
		if opened, err := openTypeAnchor(c, root, p, rel); err != nil || !opened {
			return ""
		}
		return rel
	}
	if rel, ok := typeAnchor(root, ts, skipDir(filesystem.Exclusions{})); !ok || rel != "z.js" {
		t.Fatalf("ts anchor %q %v, a bundle past the size is none", rel, ok)
	}
	if rel, ok := typeAnchor(root, ts, skipDir(filesystem.ParseExclusions("z.js\nweb"))); !ok || rel != "z.js" {
		t.Fatalf("a file is no folder to exclude: %q %v", rel, ok)
	}
	if rel, ok := typeAnchor(root, py, skipDir(filesystem.Exclusions{})); !ok || rel != "py/orders.py" {
		t.Fatalf("python anchor %q %v", rel, ok)
	}
	if _, ok := typeAnchor(root, py, skipDir(filesystem.ParseExclusions("py"))); ok {
		t.Fatal("an anchor in an excluded folder")
	}

	sent := 0
	conn := &lspConn{profile: ts, rootPath: root, rootURI: fileURI(root), docVersion: map[string]int{}, docText: map[string]string{},
		docClients: map[string]map[string]bool{}, diagnosed: make(chan struct{}), stdin: nopWriter{&sent}}
	if rel := open(conn, ts); rel != "z.js" || !conn.docClients["z.js"][typesHolder] {
		t.Fatalf("tsgo without a document: %q %v", rel, conn.docClients)
	}
	if rel := open(conn, ts); rel != "" {
		t.Fatalf("a document is open already, yet %q", rel)
	}
	conn.profile = py
	conn.docVersion, conn.docClients = map[string]int{}, map[string]map[string]bool{}
	if rel := open(conn, py); rel != "py/orders.py" {
		t.Fatalf("pyright before its listing: %q", rel)
	}
	conn.docVersion, conn.docClients = map[string]int{}, map[string]map[string]bool{}
	close(conn.diagnosed)
	if rel := open(conn, py); rel != "" {
		t.Fatalf("pyright listed, yet %q", rel)
	}
}

type nopWriter struct{ n *int }

func (w nopWriter) Write(p []byte) (int, error) { *w.n++; return len(p), nil }
func (w nopWriter) Close() error                { return nil }

// An answer past the bound keeps the first symbols and counts as cut,
// however the parts of it arrive.
func TestSymbolStreamCutsAtItsBound(t *testing.T) {
	part := func(n int) json.RawMessage {
		syms := make([]map[string]any, n)
		for i := range syms {
			syms[i] = map[string]any{"name": "A", "kind": symbolClass}
		}
		return mustMarshal(syms)
	}
	st := &symbolStream{}
	if err := st.add(part(maxSymbolCandidates - 1)); err != nil {
		t.Fatal(err)
	}
	if syms, cut := st.result(); cut || len(syms) != maxSymbolCandidates-1 {
		t.Fatalf("below the bound: %d %v", len(syms), cut)
	}
	_ = st.add(part(5))
	_ = st.add(json.RawMessage("[]"))
	if syms, cut := st.result(); !cut || len(syms) != maxSymbolCandidates {
		t.Fatalf("past the bound: %d %v", len(syms), cut)
	}
	if err := st.add(json.RawMessage(`{"no":"list"}`)); err == nil {
		t.Fatal("a malformed answer")
	}
}

// gopls is asked the whole query, a server that reads one word each word
// once, however short, at most maxQueryWords of them, the last among them.
func TestSymbolWords(t *testing.T) {
	gopls, php := profileByID("go"), profileByID("php")
	for _, tc := range []struct {
		profile     *Profile
		query, want string
	}{
		{gopls, "Inner Box", "inner box"},
		{php, "Order Shop", "order|shop"},
		{php, "Shop Order shop", "shop|order"},
		{php, "  Thing ", "thing"},
		{php, "Order io", "order|io"},
		{php, "io Order", "io|order"},
		{php, "src billing Invoice ts", "src|billing|invoice|ts"},
		{php, "aaa bbb ccc ddd eee fff ggg", "aaa|bbb|ccc|ddd|eee|ggg"},
		{php, `\Shop\Order`, "order|shop"},
		{php, "inventory.models.Item", "item|inventory|models"},
		{gopls, "inner.Box", "inner.box"},
		{php, "", ""},
	} {
		if got := strings.Join(symbolWords(tc.profile, tc.query), "|"); got != tc.want {
			t.Errorf("%s %q: %q, want %q", tc.profile.ID, tc.query, got, tc.want)
		}
	}
}

// A qualified name counts as the words it is made of, its own name first,
// whichever separator its language writes it with, and only the first word
// typed decides which word leads.
func TestMatchQuery(t *testing.T) {
	for query, want := range map[string]string{
		`Symfony\Component\HttpFoundation\Response`: "response symfony component httpfoundation",
		`HttpFoundation\Resp`:                       "resp httpfoundation",
		`\Shop\Order Draft`:                         "order shop draft",
		`Draft Shop\Order`:                          "draft shop order",
		"inventory.models.Item":                     "item inventory models",
		"Bus Common":                                "bus common",
		`Shop\`:                                     "shop",
		"":                                          "",
	} {
		if got := matchQuery(query); got != want {
			t.Errorf("%q: %q, want %q", query, got, want)
		}
	}
}

// Each server's own types and its libraries' come from requests of their
// own: gopls is asked fuzzily and exactly, tsgo with its library switch off
// and on. pyright names no library, and intelephense the libraries' types
// beside the project's own.
func TestTypeAsks(t *testing.T) {
	asks := func(p *Profile, query string) string {
		var all []string
		for _, ask := range p.TypeAsks(t.TempDir(), query) {
			settings, _ := json.Marshal(ask.settings)
			all = append(all, ask.query+" "+string(settings))
		}
		return strings.Join(all, " | ")
	}
	for _, tc := range []struct {
		profile     *Profile
		query, want string
	}{
		{profileByID("go"), "inner box", "inner box null | 'inner 'box null"},
		{profileByID("typescript"), "order", `order {"js/ts":{"workspaceSymbols":{"excludeLibrarySymbols":true}}} | order {"js/ts":{"workspaceSymbols":{"excludeLibrarySymbols":false}}}`},
	} {
		if got := asks(tc.profile, tc.query); got != tc.want {
			t.Errorf("%s: %s\nwant %s", tc.profile.ID, got, tc.want)
		}
	}
	if profileByID("python").TypeAsks != nil || profileByID("php").TypeAsks != nil {
		t.Error("pyright and intelephense are asked the query alone")
	}
}

// A crowded PHP search asks the namespaces composer.json autoloads, those
// the project's files declare outside them, and those a level below the
// autoloaded ones, in that order and within the bound, each for every
// crowded word, and opens the files that may declare a type in the global
// namespace, those named like one of the words first. Beside complete words
// it asks the deepest namespace of each file whose namespace and path hold
// them all, and opens only such files.
func TestPHPCrowded(t *testing.T) {
	root := t.TempDir()
	composer := `{"autoload": {"psr-4": {"App\\": "src/", "App\\Domain\\": ["domain/", "../outside/"], "": "lib/"}, "psr-0": {"Legacy_": "old/", "Old\\": "old/"}},
		"autoload-dev": {"psr-4": {"\\Fixtures\\": "fixtures/"}}}`
	files := map[string]string{
		"composer.json":         composer,
		"src/Kernel.php":        "<?php\nnamespace App;\nclass Kernel {}\n",
		"src/Chat/Room.php":     "<?php\n\nnamespace App\\Chat;\n\nclass Room {}\n",
		"src/Common/x.txt":      "",
		"src/9lives/x.txt":      "",
		"src/resources-x/x.txt": "",
		"domain/Order/x.txt":    "",
		"old/Old/Billing/x.txt": "",
		"outside/Secret/x.txt":  "",
		"ideas/Draft.php":       "<?php namespace Some\\Space;\nclass Draft {}\n",
		"ideas/Deeper.php":      "<?php\nnamespace Some\\Space\\Deeper;\n",
		"lib/Thing.php":         "<?php\n\nnamespace Legacy\\Things {\n    class Thing {}\n}\n\nnamespace {\n    class GlobalThing {}\n}\n",
		"tests/OrderCest.php":   "<?php\n\nfinal class OrderCest {}\n",
		"tests/script.php":      "<?php\n\necho 'class names';\n",
		"vendor/acme/Lib.php":   "<?php\n\nclass VendorThing {}\n",
	}
	for name, content := range files {
		target := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	walk := ownWalk{root: root, skip: skipDir(filesystem.Exclusions{}), owns: func(rel string) bool { return strings.HasSuffix(rel, ".php") }}
	plan := phpCrowded(&ownFiles{}, walk, []string{"cest"}, nil)
	var asks, documents []string
	for _, ask := range plan.asks {
		asks = append(asks, ask.query)
	}
	for _, doc := range plan.documents {
		documents = append(documents, doc.rel)
	}
	want := []string{`app\cest`, `old\cest`, `fixtures\cest`, `app\domain\cest`, `some\space\cest`, `legacy\things\cest`,
		`app\chat\cest`, `app\common\cest`, `old\billing\cest`, `app\domain\order\cest`}
	if strings.Join(asks, " ") != strings.Join(want, " ") || plan.cut {
		t.Fatalf("asks %v %v\nwant %v", asks, plan.cut, want)
	}
	if strings.Join(documents, " ") != "tests/OrderCest.php lib/Thing.php tests/script.php" {
		t.Fatalf("documents %v, the one named like the query first", documents)
	}
	plan = phpCrowded(&ownFiles{}, walk, []string{"thing", "cest"}, nil)
	if len(plan.asks) != 2*len(want) || plan.asks[0].query != `app\thing` || plan.asks[1].query != `app\cest` {
		t.Fatalf("two words: %v", plan.asks)
	}
	documents = nil
	for _, doc := range plan.documents {
		documents = append(documents, doc.rel)
	}
	if strings.Join(documents, " ") != "lib/Thing.php tests/OrderCest.php tests/script.php" {
		t.Fatalf("documents %v, those named like a word first", documents)
	}

	plan = phpCrowded(&ownFiles{}, walk, []string{"cest"}, []string{"chat"})
	if len(plan.asks) != 1 || plan.asks[0].query != `app\chat\cest` || len(plan.documents) != 0 {
		t.Fatalf("beside a complete word only its places: %v %v", plan.asks, plan.documents)
	}
	plan = phpCrowded(&ownFiles{}, walk, []string{"thing"}, []string{"tests"})
	if len(plan.asks) != 0 || len(plan.documents) != 2 {
		t.Fatalf("a complete word in the paths of global files only: %v %v", plan.asks, plan.documents)
	}
	if plan := phpCrowded(&ownFiles{}, walk, []string{"cest"}, []string{"nowhere"}); len(plan.asks) != 0 || len(plan.documents) != 0 {
		t.Fatalf("a complete word standing nowhere: %v %v", plan.asks, plan.documents)
	}

	many := map[string]string{}
	for i := range 40 {
		many[fmt.Sprintf("Ns%02d\\", i)] = "src/"
	}
	manifest, _ := json.Marshal(map[string]any{"autoload": map[string]any{"psr-4": many}})
	if err := os.WriteFile(filepath.Join(root, "composer.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	if plan := phpCrowded(&ownFiles{}, walk, []string{"cest"}, nil); len(plan.asks) != phpNamespaceAsks || !plan.cut {
		t.Fatalf("asks %d %v, bounded at %d", len(plan.asks), plan.cut, phpNamespaceAsks)
	}
}

// The types of a file's document symbols are its PHP declarations, inside
// a bracketed namespace with it, and nothing an HTML part names.
func TestPHPDocumentTypes(t *testing.T) {
	raw := json.RawMessage(`[
		{"name": "Legacy\\Things", "kind": 3, "range": {"start": {"line": 2, "character": 0}, "end": {"line": 4, "character": 1}}, "selectionRange": {"start": {"line": 2, "character": 10}, "end": {"line": 2, "character": 23}},
		 "children": [{"name": "Thing", "kind": 5, "range": {"start": {"line": 3, "character": 4}, "end": {"line": 3, "character": 18}}, "selectionRange": {"start": {"line": 3, "character": 10}, "end": {"line": 3, "character": 15}},
		   "children": [{"name": "run", "kind": 6, "range": {"start": {"line": 3, "character": 4}, "end": {"line": 3, "character": 18}}, "selectionRange": {"start": {"line": 3, "character": 10}, "end": {"line": 3, "character": 13}}}]}]},
		{"name": "Loggable", "kind": 2, "range": {"start": {"line": 6, "character": 0}, "end": {"line": 6, "character": 18}}, "selectionRange": {"start": {"line": 6, "character": 6}, "end": {"line": 6, "character": 14}}},
		{"name": "Game", "kind": 5, "location": {"uri": "file:///p/page.php", "range": {"start": {"line": 9, "character": 12}, "end": {"line": 9, "character": 30}}}},
		{"name": ".flipped", "kind": 5, "location": {"uri": "file:///p/page.php", "range": {"start": {"line": 10, "character": 8}, "end": {"line": 10, "character": 20}}}}
	]`)
	types, err := phpDocumentTypes(raw, "lib/Thing.php", "PHP")
	if err != nil {
		t.Fatal(err)
	}
	if got := typeRows(TypesResult{Types: types}); got != "class Thing Legacy\\Things lib/Thing.php:4:10\ntrait Loggable  lib/Thing.php:7:6" {
		t.Fatalf("types:\n%s", got)
	}
	if types, err := phpDocumentTypes(json.RawMessage("null"), "x.php", "PHP"); err != nil || len(types) != 0 {
		t.Fatalf("an empty answer: %v %v", types, err)
	}
}

// The lines are the ones intelephense 1.18.5 answered workspace symbols on:
// a type's range starts at its doc comment or attribute, and the name is
// found below, the keyword in any case.
func TestPHPTypeOf(t *testing.T) {
	file := []string{
		"/**",
		" * Doc with class Order in it.",
		" */",
		"#[Attr]",
		"final class Order extends OrderBase",
		"{",
		"}",
		"Abstract CLASS  Ünicode {}",
		"readonly class OrderOrder {}",
	}
	line := func(n int) (string, bool) {
		if n < 1 || n > len(file) {
			return "", false
		}
		return file[n-1], true
	}
	for _, tc := range []struct {
		sym  Symbol
		want string
	}{
		{Symbol{Name: "Order", Kind: symbolClass, Line: 1, EndLine: 7}, "class Order 5:12"},
		{Symbol{Name: `Shop\Domain\Order`, Kind: symbolClass, Line: 1, EndLine: 7}, `class Order 5:12 Shop\Domain`},
		{Symbol{Name: "Ünicode", Kind: symbolClass, Line: 8, EndLine: 8}, "class Ünicode 8:16"},
		{Symbol{Name: "OrderOrder", Kind: symbolClass, Line: 9, EndLine: 9}, "class OrderOrder 9:15"},
		{Symbol{Name: "Payable", Kind: symbolInterface, Line: 2, EndLine: 3}, "interface Payable 2:0"},
		{Symbol{Name: "Loggable", Kind: symbolModule, Line: 6, EndLine: 6}, "trait Loggable 6:0"},
		{Symbol{Name: "State", Kind: symbolEnum, Line: 7, EndLine: 7}, "enum State 7:0"},
		{Symbol{Name: "Gone", Kind: symbolClass, Line: 40, EndLine: 41}, "class Gone 40:0"},
		{Symbol{Name: "order_helper", Kind: symbolFunction, Line: 1, EndLine: 1}, ""},
		{Symbol{Name: "total", Kind: 6, Line: 1, EndLine: 1}, ""},
		{Symbol{Name: "ORDER_MAX", Kind: 14, Line: 1, EndLine: 1}, ""},
	} {
		at, ok := phpTypeOf(tc.sym, line)
		got := ""
		if ok {
			got = strings.TrimSpace(fmt.Sprintf("%s %s %d:%d %s", at.Kind, at.Name, at.Line, at.Character, at.Container))
		}
		if got != tc.want {
			t.Errorf("%s: %q, want %q", tc.sym.Name, got, tc.want)
		}
	}
}

// The lines are the ones tsgo 7.0.0-dev answered workspace symbols on, the
// column on the name.
func TestTSTypeOf(t *testing.T) {
	for _, tc := range []struct {
		line string
		sym  Symbol
		want string
	}{
		{"export class OrderModel {}", Symbol{Name: "OrderModel", Kind: symbolClass, Character: 13}, "class"},
		{"export default class EdgeDefault {}", Symbol{Name: "EdgeDefault", Kind: symbolClass, Character: 21}, "class"},
		{"export const EdgeNamed = class EdgeInnerName {};", Symbol{Name: "EdgeInnerName", Kind: symbolClass, Character: 31}, "class"},
		{"export type OrderId = string | number;", Symbol{Name: "OrderId", Kind: symbolClass, Character: 12}, "type"},
		{"export declare type EdgeDeclaredType = string;", Symbol{Name: "EdgeDeclaredType", Kind: symbolClass, Character: 20}, "type"},
		{"  type EdgeLocalType = string;", Symbol{Name: "EdgeLocalType", Kind: symbolClass, Character: 7}, "type"},
		{"export interface OrderLike {}", Symbol{Name: "OrderLike", Kind: symbolInterface, Character: 17}, "interface"},
		{"export const enum OrderFlag {}", Symbol{Name: "OrderFlag", Kind: symbolEnum, Character: 18}, "enum"},
		{"namespace OrderSpace {}", Symbol{Name: "OrderSpace", Kind: 3, Character: 10}, ""},
		{"export const EdgeExpr = class {};", Symbol{Name: "EdgeExpr", Kind: 13, Character: 13}, ""},
		{"export function orderFn() {}", Symbol{Name: "orderFn", Kind: symbolFunction, Character: 16}, ""},
	} {
		tc.sym.Line, tc.sym.EndLine = 4, 4
		at, ok := tsTypeOf(tc.sym, func(n int) (string, bool) { return tc.line, n == 4 })
		if at.Kind != tc.want || ok != (tc.want != "") || ok && (at.Name != tc.sym.Name || at.Line != 4 || at.Character != tc.sym.Character) {
			t.Errorf("%q: %+v %v, want %q", tc.line, at, ok, tc.want)
		}
	}
}

func TestPythonTypeOf(t *testing.T) {
	if at, ok := pythonTypeOf(Symbol{Name: "Nested", Kind: symbolClass, Line: 2, Character: 10}, nil); !ok || at != (Declared{Name: "Nested", Kind: "class", Line: 2, Character: 10}) {
		t.Fatalf("a class: %+v %v", at, ok)
	}
	for _, kind := range []int{13, symbolFunction, 14} {
		if _, ok := pythonTypeOf(Symbol{Name: "OrderAlias", Kind: kind, Line: 6}, nil); ok {
			t.Fatalf("kind %d", kind)
		}
	}
}
