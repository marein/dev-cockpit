const L = require("./lib");
const { assert, sleep, BASE } = L;

// The type search of the editor's quick open palette: Ctrl+O, Go to type in
// the menu, the Files and Types buttons beside the field, bare Shift twice in
// the palette, Ctrl+O and Ctrl+Shift+O switching between the two lists with
// the query kept, Ctrl+Alt+O opening no palette and switching no list; rows
// with kind, name, package and path, ranked by the files' rule;
// Enter, a click and a tap opening the declaration; the waiting states of a
// server that is built and indexes; the note of a project no server covers.
// A mixed project holds every language, and a file of each kind in the
// folders the languages install dependencies into: its own types come in
// one ranked list, each kind with its word and icon, a PHP class opening on
// its name below the doc comment, a query of two words narrowing by
// namespace and path. The types of the dependencies and the standard
// library follow them, each marked library: one in the standard library
// opens read only at its own path, one in vendor as the project file it is,
// as a definition there does. A PHP project whose vendor fills
// intelephense's limit keeps its own types: those of a namespace it
// declares, asked apart, and the one in the global namespace, read off its
// file's document symbols, while what the file's HTML declares stays out.
// Routes: GET /projects/:name/editor/lsp/types (internal/editorintelligence
// Types over workspace/symbol).
// Instance: the one editor-lsp.js runs on, the fake language server first on
// PATH as "gopls" and the fake docker beside it (tests/e2e/fake-lsp.py, see
// README.md). Its symbol contract: every symbol of types.go and inner/box.go
// for any query, with gopls' kinds and positions (a function, a field, an
// embedded interface and the standard library's io.Reader among them), none
// for "zzz", a hundred copies of Widget for "widg". The fake docker builds
// for three seconds on every start, which is the preparing phase, a
// `.fake-lsp-hold` file holds the indexing until it is gone, and the image
// reads answer from the fake's IMAGE_FILES. Over the mixed project the fake
// plays each of the four servers with the answers the real ones gave, tsgo
// and pyright answering only from an open document, tsgo naming a type in
// node_modules and one of its standard library once its library symbols are
// switched on. A `.fake-lsp-crowded` file makes intelephense fill its limit
// with vendor classes and name the project's own only behind its namespace
// and in an open file's document symbols. A `.fake-lsp-names` file makes
// intelephense, tsgo and pyright name only the symbols whose name holds the
// query, as the real ones match a name: the words of a query then find a
// type in any order, its folder, namespace or package before its name as
// after it, a word of two letters too, its name as the third of four words,
// and a type by its qualified name, tsgo naming a namespace's member with
// the namespace as its container. intelephense answers a query holding
// a backslash with the symbols whose qualified name holds its parts in
// their order, named by the qualified name, as the real one does.

const TYPES_GO = `package geo

import "io"

type Widget struct{ Name string }

type Shape interface{ Area() float64 }

type Handler func(int) error

func NewWidget() *Widget { return &Widget{} }

type RW interface {
	io.Reader
}
`;

const ORDER_PHP = `<?php

namespace Acme\\Shop\\Domain\\Order;

/**
 * An order, see class Order in the docs.
 */
#[Entity]
final class Order
{
    public function total(): int { return 0; }
}

interface Payable {}

trait Loggable {}

enum OrderState: string { case Open = 'open'; }

function order_helper(): void {}
`;

const ORDER_TS = `export interface OrderLike { id: number }
export class OrderModel implements OrderLike { id = 0 }
export enum OrderKind { A }
export type OrderId = string | number;
namespace OrderSpace {}
export const orderConst = 1;
`;

const ORDER_PY = `class OrderBase:
    class Nested:
        pass


OrderAlias = OrderBase


def order_func():
    pass
`;

const MIXED_DIRS = ["src/Shop", "vendor/acme/lib", "web/node_modules/dep", "py/third_party/lib/python3.12/site-packages/dep"];
const MIXED_FILES = [
  ["go.mod", "module example.com/mixed\n"],
  ["order.go", "package mixed\n\ntype OrderBook struct{}\n"],
  ["composer.json", "{}\n"],
  ["package.json", "{}\n"],
  ["src/Shop/Order.php", ORDER_PHP],
  ["vendor/acme/lib/VendorOrder.php", "<?php\n\nclass VendorOrder {}\n"],
  ["vendor/acme/lib/order.js", "export class VendorScriptOrder {}\n"],
  ["web/order.ts", ORDER_TS],
  ["web/widget.js", "export class OrderWidget {}\n"],
  ["web/space.ts", "export namespace Billing {\n  export class Ledger {}\n}\n"],
  ["web/node_modules/dep/order.js", "export class DepOrder {}\n"],
  ["web/node_modules/dep/order.d.ts", "export interface OrderDepTypes {}\n"],
  ["py/orders.py", ORDER_PY],
  ["py/third_party/lib/python3.12/site-packages/dep/__init__.py", "class VenvOrder:\n    pass\n"],
];

const CROWDED_DIRS = ["src/Shop", "legacy", "vendor/acme/lib"];
const CROWDED_FILES = [
  [".fake-lsp-crowded", "crowded\n"],
  ["composer.json", "{}\n"],
  ["src/Shop/Order.php", ORDER_PHP],
  ["legacy/OrderPrinter.php", "<?php\n\n/** Prints an order. */\nclass OrderPrinter {}\n"],
  ["legacy/page.php", "<?php $title = 'Orders'; ?>\n<style>.order-row { color: red; }</style>\n<script>class OrderScript {}</script>\n"],
  ["vendor/acme/lib/VendorOrder.php", "<?php\n\nclass VendorOrder {}\n"],
];

L.runFeature("EDITOR-LSP-TYPES", async ({ engine, page, run, mobilePage }) => {
  const tag = `types-${engine}-${Date.now().toString(36)}`;
  const project = `zzlspt-${tag}`;
  const bare = `zzlsptn-${tag}`;
  const mixed = `zzlsptm-${tag}`;
  const crowded = `zzlsptc-${tag}`;
  const base = `${BASE}/projects/${project}/editor`;
  const mixedBase = `${BASE}/projects/${mixed}/editor`;

  const postTo = (baseURL, path, body) => page.evaluate(async ({ url, body }) => {
    const token = document.querySelector('meta[name="csrf-token"]').content;
    const res = await fetch(url, {
      method: "POST",
      headers: { "X-CSRF-Token": token, "Content-Type": "application/x-www-form-urlencoded" },
      body: new URLSearchParams(body).toString(),
    });
    if (!res.ok) throw new Error(`${url} -> ${res.status}`);
  }, { url: `${baseURL}${path}`, body });

  const palette = (p = page) => p.evaluate(() => {
    const visible = (el) => !!el && el.getClientRects().length > 0 && getComputedStyle(el).visibility !== "hidden";
    return {
      open: visible(document.querySelector("[data-editor-quickopen]")),
      placeholder: document.querySelector("[data-editor-quickopen-input]").placeholder,
      value: document.querySelector("[data-editor-quickopen-input]").value,
      note: document.querySelector("[data-editor-quickopen-list] .editor-quickopen-empty")?.textContent || "",
      foot: [document.querySelector("[data-editor-quickopen-foot-left]").textContent, document.querySelector("[data-editor-quickopen-foot-right]").textContent],
      kinds: visible(document.querySelector("[data-editor-quickopen-kinds]")),
      pressed: [...document.querySelectorAll("[data-editor-quickopen-kind]")].filter((b) => b.getAttribute("aria-pressed") === "true").map((b) => b.dataset.editorQuickopenKind).join(","),
      rows: [...document.querySelectorAll("[data-editor-quickopen-list] .editor-quickopen-item")].map((row) => ({
        icon: [...(row.querySelector(".ti")?.classList || [])].find((c) => c !== "ti") || "",
        kind: row.querySelector(".badge")?.textContent || "",
        library: visible(row.querySelector("[data-editor-type-library]")),
        name: row.querySelector(".editor-quickopen-name")?.textContent || "",
        mark: row.querySelector(".editor-quickopen-name mark")?.textContent || "",
        pkg: row.querySelector(".editor-quickopen-dir")?.textContent || "",
        place: row.querySelector(".editor-quickopen-match-text")?.textContent || "",
        title: row.title,
      })),
    };
  });
  const typeRows = async (p = page) => (await palette(p)).rows.filter((row) => row.kind);
  const waitTypeRows = (p, n) => p.waitForFunction((count) => [...document.querySelectorAll("[data-editor-quickopen-list] .editor-quickopen-item")]
    .filter((row) => row.querySelector(".badge")).length === count, n, { timeout: 20000 });
  const waitNote = (p, text) => p.waitForFunction((t) => (document.querySelector("[data-editor-quickopen-list] .editor-quickopen-empty")?.textContent || "").includes(t), text, { timeout: 20000 });
  const editorAt = (p = page) => p.evaluate(() => ({
    path: document.querySelector('.editor-tab[aria-selected="true"]')?.dataset.path || "",
    pos: document.querySelector("[data-editor-pos]")?.textContent || "",
  }));
  const closePalette = async (p = page) => {
    await p.keyboard.press("Escape");
    await p.waitForFunction(() => document.querySelector("[data-editor-quickopen]").hidden, null, { timeout: 5000 });
  };
  const openTypes = async (query) => {
    await page.keyboard.press("Control+o");
    await page.waitForFunction(() => !document.querySelector("[data-editor-quickopen]").hidden, null, { timeout: 5000 });
    if (query) await page.keyboard.type(query);
  };
  const shiftTwice = async () => {
    await page.keyboard.press("Shift");
    await page.keyboard.press("Shift");
  };

  const saveLSPSettings = async () => {
    await Promise.all([
      page.waitForResponse((r) => r.url().includes("/settings/editor/lsp") && r.request().method() === "POST", { timeout: 8000 }),
      page.click('#settings-editor-lsp button[type="submit"]'),
    ]);
    await page.waitForFunction(() => document.body.textContent.includes("Settings saved."), null, { timeout: 8000 });
  };
  const servers = [["go", "gopls"], ["php", "intelephense"], ["typescript", "tsgo"], ["python", "pyright"]];

  await run("setup: every language pinned to Docker, the fake answering", async () => {
    await page.goto(`${BASE}/settings/editor/lsp`, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    const picked = await page.$$eval("#settings-editor-lsp select", (els) => els.map((sel) => `${sel.name}=${sel.value}`).join(","));
    const fresh = picked === servers.map(([id]) => `server_${id}=auto`).join(",");
    const pinned = picked === servers.map(([id, server]) => `server_${id}=${server}-docker`).join(",");
    assert(fresh || pinned, `Automatic or the Docker pin, got ${picked} (the runner needs the editor-lsp.js instance)`);
    if (fresh) {
      for (const [id, server] of servers) await page.selectOption(`select[name="server_${id}"]`, `${server}-docker`);
      await saveLSPSettings();
    }
  });

  await run("setup: a Go project held in its indexing, a project of notes", async () => {
    await L.createProject(page, project);
    await L.createProject(page, bare);
    await L.setEditorFiles(page, { autosave: false });
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await postTo(base, "/mkdir", { path: "inner" });
    for (const [path, content] of [
      [".fake-lsp-hold", "hold\n"],
      ["go.mod", "module example.com/geo\n"],
      ["types.go", TYPES_GO],
      ["inner/box.go", "package inner\n\ntype WidgetBox struct{}\n"],
    ]) {
      await postTo(base, "/create", { path });
      await postTo(base, "/file", { path, content });
    }
    await postTo(`${BASE}/projects/${bare}/editor`, "/create", { path: "notes.txt" });
    await postTo(`${BASE}/projects/${bare}/editor`, "/file", { path: "notes.txt", content: "Widget is a word here.\n" });
  });

  await run("setup: a project of every language with dependencies of each", async () => {
    await L.createProject(page, mixed);
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    for (const path of MIXED_DIRS) await postTo(mixedBase, "/mkdir", { path });
    for (const [path, content] of MIXED_FILES) {
      await postTo(mixedBase, "/create", { path });
      await postTo(mixedBase, "/file", { path, content });
    }
  });

  await run("setup: a PHP project whose vendor fills intelephense's limit", async () => {
    await L.createProject(page, crowded);
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    const crowdedBase = `${BASE}/projects/${crowded}/editor`;
    for (const path of CROWDED_DIRS) await postTo(crowdedBase, "/mkdir", { path });
    for (const [path, content] of CROWDED_FILES) {
      await postTo(crowdedBase, "/create", { path });
      await postTo(crowdedBase, "/file", { path, content });
    }
  });

  await run("a server being built and indexing says so in the list, the types come once it is ready", async () => {
    await page.goto(base, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    await page.waitForSelector('.editor-item[data-path="types.go"]', { timeout: 10000 });
    await page.evaluate(() => {
      window.__typeNotes = [];
      const list = document.querySelector("[data-editor-quickopen-list]");
      new MutationObserver(() => {
        const note = list.querySelector(".editor-quickopen-empty")?.textContent || "";
        if (note && window.__typeNotes[window.__typeNotes.length - 1] !== note) window.__typeNotes.push(note);
      }).observe(list, { childList: true, subtree: true, characterData: true });
    });
    await openTypes("widget");
    await waitNote(page, "Indexing Go…");
    await sleep(600);
    const held = await palette();
    assert(held.rows.length === 0, `rows while the index is held: ${JSON.stringify(held.rows)}`);
    assert(held.foot.join("") === "", `the foot counts nothing while waiting, got ${held.foot}`);
    await postTo(base, "/delete", { path: ".fake-lsp-hold" });
    await waitTypeRows(page, 2);
    const notes = await page.evaluate(() => window.__typeNotes);
    assert(notes.some((n) => n.startsWith("Preparing Go…")), `the build was not shown: ${JSON.stringify(notes)}`);
    assert(!notes.some((n) => n.startsWith("No matching")), `an empty answer showed while waiting: ${JSON.stringify(notes)}`);
    await closePalette();
    return notes.join(" | ");
  });

  await run("ctrl+o opens on types: kind, marked name, package and path, ranked like files, the standard library's behind, nothing that is no type", async () => {
    await page.click(".editor-tree");
    await openTypes();
    let state = await palette();
    assert(state.placeholder === "Go to type…", `placeholder ${state.placeholder}`);
    assert(state.kinds && state.pressed === "types", `the switch shows ${state.kinds} ${state.pressed}`);
    await waitNote(page, "Type the name of a type.");
    await page.keyboard.type("a");
    await waitTypeRows(page, 6);
    state = await palette();
    const got = state.rows.map((r) => `${r.kind} ${r.name} ${r.pkg} ${r.place}${r.library ? " library" : ""}`);
    const want = [
      "type Handler example.com/geo types.go:9",
      "interface Shape example.com/geo types.go:7",
      "interface RW example.com/geo types.go:13",
      "struct Widget example.com/geo types.go:5",
      "struct WidgetBox …/geo/inner inner/box.go:3",
      "interface Reader io …/src/io/io.go:86 library",
    ];
    assert(got.join("\n") === want.join("\n"), `rows:\n${got.join("\n")}`);
    assert(state.rows[0].mark === "a" && state.rows[3].mark === "", `marks ${state.rows.map((r) => r.mark)}`);
    assert(state.rows[4].title.includes("example.com/geo/inner"), `the title names the whole package: ${state.rows[4].title}`);
    assert(state.rows[5].title.endsWith("/usr/local/go/src/io/io.go:86, outside the project"), `the title names the whole path: ${state.rows[5].title}`);
    assert(state.foot[0] === "6 types" && state.foot[1] === "", `foot ${state.foot}`);
    await closePalette();
  });

  await run("a standard library type opens read only at its own path, like a definition there", async () => {
    await page.click(".editor-tree");
    await openTypes("reader");
    await waitTypeRows(page, 1);
    await page.keyboard.press("Enter");
    await page.waitForFunction(() => document.querySelector('.editor-tab[aria-selected="true"]')?.dataset.path === "/usr/local/go/src/io/io.go", null, { timeout: 20000 });
    const state = await page.evaluate(() => {
      const tab = document.querySelector('.editor-tab[aria-selected="true"]');
      return {
        locked: !!tab.querySelector(".ti-lock"),
        readOnly: document.querySelector("[data-editor-readonly]").getClientRects().length > 0,
        readOnlyPath: document.querySelector("[data-editor-readonly-path]").textContent,
        text: [...document.querySelectorAll(".cm-line")].map((l) => l.textContent).join("\n"),
      };
    });
    assert(state.locked && state.readOnly && state.readOnlyPath === "…/src/io/io.go", `read only ${JSON.stringify(state)}`);
    assert(state.text.includes("type Reader interface {"), `the image's source is in the buffer: ${state.text.slice(-200)}`);
    const at = await editorAt();
    assert(at.pos === "86:6", `Enter landed on ${JSON.stringify(at)}`);
  });

  await run("enter opens the declaration on its line, a click and ctrl+enter too", async () => {
    await openTypes("shape");
    await waitTypeRows(page, 1);
    await page.keyboard.press("Enter");
    await page.waitForFunction(() => document.querySelector("[data-editor-quickopen]").hidden, null, { timeout: 5000 });
    await page.waitForFunction(() => document.querySelector('.editor-tab[aria-selected="true"]')?.dataset.path === "types.go", null, { timeout: 10000 });
    let at = await editorAt();
    assert(at.pos === "7:6", `Enter landed on ${JSON.stringify(at)}`);

    await openTypes("box");
    await waitTypeRows(page, 1);
    await page.click("[data-editor-quickopen-list] .editor-quickopen-item");
    await page.waitForFunction(() => document.querySelector('.editor-tab[aria-selected="true"]')?.dataset.path === "inner/box.go", null, { timeout: 10000 });
    at = await editorAt();
    assert(at.pos === "3:6", `the click landed on ${JSON.stringify(at)}`);

    await openTypes("handler");
    await waitTypeRows(page, 1);
    await page.keyboard.press("Control+Enter");
    await page.waitForFunction(() => document.querySelector('.editor-tab[aria-selected="true"]')?.dataset.path === "types.go", null, { timeout: 10000 });
    const state = await palette();
    at = await editorAt();
    assert(state.open && at.pos === "9:6", `Ctrl+Enter left ${JSON.stringify({ open: state.open, at })}`);
    await closePalette();
  });

  await run("a query nothing matches says so, a cut answer says it is truncated", async () => {
    await openTypes("zzz");
    await waitNote(page, "No matching types.");
    assert((await palette()).foot[0] === "0 types", "the foot counts zero");
    await page.fill("[data-editor-quickopen-input]", "widg");
    await waitTypeRows(page, 1);
    const state = await palette();
    assert(state.foot[0] === "1 type" && state.foot[1] === "truncated, narrow the search", `foot ${state.foot}`);
    await closePalette();
  });

  await run("the keyboard switches between files and types and keeps the query, ctrl+alt+o opens no palette and switches no list", async () => {
    await page.click(".editor-tree");
    await page.keyboard.press("Control+Alt+o");
    await sleep(400);
    assert(await page.evaluate(() => document.querySelector("[data-editor-quickopen]").hidden), "Ctrl+Alt+O opened the palette");
    await page.keyboard.press("Control+Shift+o");
    await page.waitForFunction(() => !document.querySelector("[data-editor-quickopen]").hidden, null, { timeout: 5000 });
    await page.keyboard.type("box");
    await page.waitForSelector('[data-editor-quickopen-list] .editor-quickopen-item[data-path="inner/box.go"]', { timeout: 10000 });
    let state = await palette();
    assert(state.placeholder === "Go to file…" && state.kinds && state.pressed === "files", `files ${JSON.stringify(state)}`);
    await shiftTwice();
    await waitTypeRows(page, 1);
    state = await palette();
    assert(state.placeholder === "Go to type…" && state.value === "box" && state.pressed === "types", `double Shift ${JSON.stringify(state)}`);
    assert(state.rows[0].name === "WidgetBox", `types row ${state.rows[0].name}`);
    await shiftTwice();
    await page.waitForSelector('[data-editor-quickopen-list] .editor-quickopen-item[data-path="inner/box.go"]', { timeout: 10000 });
    state = await palette();
    assert(state.placeholder === "Go to file…" && state.value === "box", `back ${JSON.stringify(state)}`);
    await page.keyboard.press("Control+Alt+o");
    await sleep(400);
    state = await palette();
    assert(state.placeholder === "Go to file…" && state.value === "box" && state.pressed === "files", `Ctrl+Alt+O switched ${JSON.stringify(state)}`);
    assert(await page.evaluate(() => document.querySelector("[data-editor-sheet]").hidden), "Ctrl+Alt+O opened a sheet over the palette");
    await page.keyboard.press("Control+o");
    await waitTypeRows(page, 1);
    state = await palette();
    assert(state.placeholder === "Go to type…" && state.value === "box" && state.pressed === "types", `Ctrl+O ${JSON.stringify(state)}`);
    await page.keyboard.press("Control+Shift+o");
    await page.waitForSelector('[data-editor-quickopen-list] .editor-quickopen-item[data-path="inner/box.go"]', { timeout: 10000 });
    state = await palette();
    assert(state.placeholder === "Go to file…" && state.value === "box" && state.pressed === "files", `Ctrl+Shift+O ${JSON.stringify(state)}`);
    await closePalette();

    await page.keyboard.press("Control+Shift+f");
    await page.waitForFunction(() => !document.querySelector("[data-editor-quickopen]").hidden, null, { timeout: 5000 });
    state = await palette();
    assert(state.placeholder === "Find in files…" && !state.kinds, `find in files shows the switch: ${JSON.stringify(state)}`);
    await shiftTwice();
    await sleep(300);
    assert((await palette()).placeholder === "Find in files…", "double Shift left find in files");
    await closePalette();
  });

  await run("the menu's Go to type opens on types, the shortcuts, the hints and the docs name the keys", async () => {
    await page.click("[data-editor-menu]");
    await page.waitForSelector("[data-editor-menu-list].show", { timeout: 4000 });
    await page.click("[data-editor-type-open-item]");
    await page.waitForFunction(() => !document.querySelector("[data-editor-quickopen]").hidden, null, { timeout: 5000 });
    assert((await palette()).placeholder === "Go to type…", "the menu entry opened another list");
    await closePalette();

    await page.click("[data-editor-menu]");
    await page.waitForSelector("[data-editor-menu-list].show", { timeout: 4000 });
    await page.click('[data-editor-menu-list] [data-bs-target="#editor-shortcuts-modal"]');
    await L.modalShown(page, "editor-shortcuts-modal");
    const rows = await page.evaluate(() => [...document.querySelectorAll("#editor-shortcuts-modal .dc-shortcuts-row")]
      .map((r) => r.textContent.replace(/\s+/g, " ").trim()).filter((t) => /^Go to (file|type) /.test(t)).join(" | "));
    assert(rows === "Go to file Ctrl+Shift+O | Go to type Ctrl+O", `the dialog rows read "${rows}"`);
    const hints = await page.evaluate(() => ({
      buttons: [...document.querySelectorAll("[data-editor-quickopen-kind]")].map((b) => b.title).join(" | "),
      empty: document.querySelector("[data-editor-placeholder] .editor-placeholder-keys").textContent.replace(/\s+/g, " "),
      page: document.body.textContent.replace(/\s+/g, " ") + [...document.querySelectorAll("[title], [placeholder], [aria-label]")]
        .map((el) => `${el.title} ${el.placeholder || ""} ${el.getAttribute("aria-label")}`).join(" "),
    }));
    assert(hints.buttons === "Files (Ctrl+Shift+O) | Types (Ctrl+O)", `the buttons' titles read "${hints.buttons}"`);
    assert(hints.empty.trim().startsWith("Ctrl+Shift+O go to file ·"), `the empty editor's hint reads "${hints.empty}"`);
    assert(!/Ctrl\+Alt\+O (go to|types)|Types \(Ctrl\+Alt\+O\)|Ctrl\+O go to file|Files \(Ctrl\+O\)/i.test(hints.page), "the page names an old key");
    const note = await page.evaluate(() => [...document.querySelectorAll("#editor-shortcuts-modal .dc-shortcuts-note")]
      .map((n) => n.textContent.trim()).find((n) => n.includes("files and types")) || "");
    assert(note === "In the palette bare Shift twice switches between files and types.", `the dialog note reads "${note}"`);
    const examples = /HttpFoundation|http\.Request|models\.Item|Billing\.Ledger|Common.?Bus/;
    const dialogText = await page.evaluate(() => document.querySelector("#editor-shortcuts-modal").textContent);
    assert(!examples.test(dialogText), "the dialog names an example");
    await page.click('#editor-shortcuts-modal [data-bs-dismiss="modal"]');
    await page.waitForFunction(() => !document.querySelector("#editor-shortcuts-modal").classList.contains("show"), null, { timeout: 5000 });

    await page.goto(`${BASE}/docs`, { waitUntil: "domcontentloaded" });
    const item = await page.evaluate(() => {
      const row = [...document.querySelectorAll("#editor .list-group-item")].find((el) => el.querySelector(".fw-medium")?.textContent === "Go to type");
      return row ? { keys: [...row.querySelectorAll(".dc-docs-item-head kbd")].map((k) => k.textContent).join("+"), desc: row.querySelector(".mt-1").textContent } : null;
    });
    assert(item && item.keys === "Ctrl+O", `the docs item: ${JSON.stringify(item)}`);
    assert(item.desc === "Open a type by name. Files and Types beside the field, or bare Shift twice, switch the list and keep the query. Words match in any order, a qualified name too. Types of dependencies and the standard library follow, marked library. PHP's standard library, Python's libraries and JavaScript projects without TypeScript files list their own types only. While a language server indexes, the palette says so.", `the docs text: ${item.desc}`);
    const files = await page.evaluate(() => {
      const row = [...document.querySelectorAll("#editor .list-group-item")].find((el) => el.querySelector(".fw-medium")?.textContent === "Quick open");
      return row ? [...row.querySelectorAll(".dc-docs-item-head kbd")].map((k) => k.textContent).join("+") : "";
    });
    assert(files === "Ctrl+Shift+O", `the docs' quick open keys read "${files}"`);
    const docsText = await page.evaluate(() => document.body.textContent.replace(/\s+/g, " "));
    assert(!item.desc.includes("Alt") && !files.includes("Alt"), "the docs name Ctrl+Alt+O for types or files");
    assert(!examples.test(docsText), "the docs name an example");
  });

  await run("every language's own types in one ranked list, each kind with its word and icon, the dependencies' behind them marked library", async () => {
    await page.goto(mixedBase, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    await page.waitForSelector('.editor-item[data-path="order.go"]', { timeout: 10000 });
    await page.click(".editor-tree");
    assert(!(await page.$(".editor-tab")), "a file is open, the servers would not need their own");
    await openTypes("order");
    await waitTypeRows(page, 18);
    const state = await palette();
    const got = state.rows.map((r) => `${r.icon} ${r.kind} ${r.name} ${r.pkg} ${r.place}${r.library ? " library" : ""}`.replace(/ +/g, " "));
    const want = [
      "ti-letter-c class Order …\\Domain\\Order src/Shop/Order.php:9",
      "ti-letter-t type OrderId web/order.ts:4",
      "ti-letter-c class OrderBase py/orders.py:1",
      "ti-box struct OrderBook example.com/mixed order.go:3",
      "ti-list-numbers enum OrderKind web/order.ts:3",
      "ti-plug interface OrderLike web/order.ts:1",
      "ti-letter-c class OrderModel web/order.ts:2",
      "ti-list-numbers enum OrderState …\\Domain\\Order src/Shop/Order.php:18",
      "ti-letter-c class OrderWidget web/widget.js:1",
      "ti-letter-c class Nested OrderBase py/orders.py:2",
      "ti-plug interface Payable …\\Domain\\Order src/Shop/Order.php:14",
      "ti-puzzle trait Loggable …\\Domain\\Order src/Shop/Order.php:16",
      "ti-plug interface OrderDepTypes web/node_modules/dep/order.d.ts:1 library",
      "ti-letter-t type OrderLibAlias …/native-preview/lib/lib.es5.d.ts:30 library",
      "ti-letter-c class DepOrder web/node_modules/dep/order.js:1 library",
      "ti-letter-c class VenvOrder py/third_party/lib/python3.12/site-packages/dep/__init__.py:1 library",
      "ti-letter-c class VendorOrder Acme\\Lib vendor/acme/lib/VendorOrder.php:3 library",
      "ti-letter-c class VendorScriptOrder vendor/acme/lib/order.js:1 library",
    ];
    assert(got.join("\n") === want.join("\n"), `rows:\n${got.join("\n")}`);
    assert(state.rows[0].title.includes("Acme\\Shop\\Domain\\Order"), `the title names the whole namespace: ${state.rows[0].title}`);
    assert(state.foot[0] === "18 types" && state.foot[1] === "", `foot ${state.foot}`);
    const shown = await page.evaluate(() => [...document.querySelectorAll("[data-editor-quickopen-list] .editor-quickopen-item")].map((row) => {
      const icon = row.querySelector(".ti").getBoundingClientRect();
      const badge = row.querySelector(".badge").getBoundingClientRect();
      return icon.width > 0 && badge.width > 0 && getComputedStyle(row.querySelector(".badge")).visibility !== "hidden";
    }));
    assert(shown.every(Boolean), `a kind is not visible: ${shown}`);
  });

  await run("a PHP class opens on its name below the doc comment, two words narrow by namespace", async () => {
    await page.keyboard.press("Enter");
    await page.waitForFunction(() => document.querySelector('.editor-tab[aria-selected="true"]')?.dataset.path === "src/Shop/Order.php", null, { timeout: 10000 });
    const at = await editorAt();
    assert(at.pos === "9:13", `Enter landed on ${JSON.stringify(at)}`);

    await page.click(".editor-tree");
    await openTypes("Order shop");
    await waitTypeRows(page, 4);
    const got = (await typeRows()).map((r) => `${r.kind} ${r.name}`);
    assert(got.join(",") === "class Order,enum OrderState,interface Payable,trait Loggable", `rows ${got}`);
    await closePalette();
  });

  await run("a type in vendor opens as the project file it is, like a definition there", async () => {
    await page.click(".editor-tree");
    await openTypes("vendororder");
    await waitTypeRows(page, 1);
    assert((await typeRows())[0].library, "the vendor's type is marked library");
    await page.keyboard.press("Enter");
    await page.waitForFunction(() => document.querySelector('.editor-tab[aria-selected="true"]')?.dataset.path === "vendor/acme/lib/VendorOrder.php", null, { timeout: 10000 });
    const state = await page.evaluate(() => ({
      locked: !!document.querySelector('.editor-tab[aria-selected="true"] .ti-lock'),
      readOnly: document.querySelector("[data-editor-readonly]").getClientRects().length > 0,
    }));
    assert(!state.locked && !state.readOnly, `a project file carries no read only mark: ${JSON.stringify(state)}`);
    const at = await editorAt();
    assert(at.pos === "3:7", `Enter landed on ${JSON.stringify(at)}`);
  });

  await run("the words match in any order, a folder, a namespace or a package before the name as after it, a qualified name too", async () => {
    await page.goto(mixedBase, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    await page.waitForSelector('.editor-item[data-path="order.go"]', { timeout: 10000 });
    await postTo(mixedBase, "/create", { path: ".fake-lsp-names" });
    const rows = () => page.evaluate(() => [...document.querySelectorAll("[data-editor-quickopen-list] .editor-quickopen-item")]
      .filter((row) => row.querySelector(".badge")).map((row) => `${row.querySelector(".badge").textContent} ${row.querySelector(".editor-quickopen-name").textContent}`).join(","));
    for (const [query, want] of [
      ["shop order", "class Order,interface Payable,trait Loggable,enum OrderState"],
      ["order shop", "class Order,enum OrderState"],
      ["web id", "type OrderId,class OrderWidget"],
      ["id web", "type OrderId,class OrderWidget"],
      ["web ordermodel", "class OrderModel"],
      ["ordermodel web", "class OrderModel"],
      ["py orderbase", "class OrderBase"],
      ["orderbase py", "class OrderBase"],
      ["mixed orderbook", "struct OrderBook"],
      ["orderbook mixed", "struct OrderBook"],
      ["web order.ts OrderModel ts", "class OrderModel"],
      ["src Shop Payable Acme", "interface Payable"],
      ["Acme\\Shop\\Domain\\Order\\Payable", "interface Payable"],
      ["Order\\Pay", "interface Payable"],
      ["mixed.OrderBook", "struct OrderBook"],
      ["orders.OrderBase", "class OrderBase"],
      ["Billing.Ledger", "class Ledger"],
    ]) {
      await page.click(".editor-tree");
      await openTypes(query);
      await page.waitForFunction((w) => [...document.querySelectorAll("[data-editor-quickopen-list] .editor-quickopen-item")]
        .filter((row) => row.querySelector(".badge")).map((row) => `${row.querySelector(".badge").textContent} ${row.querySelector(".editor-quickopen-name").textContent}`).join(",") === w, want, { timeout: 20000 }).catch(() => {});
      const got = await rows();
      assert(got === want, `"${query}" lists "${got}", want "${want}"`);
      if (query === "Order\\Pay") {
        const first = (await palette()).rows[0];
        assert(first.mark === "Pay", `a qualified name marks its own name: ${JSON.stringify(first)}`);
      }
      if (query === "shop order") {
        const first = (await palette()).rows[0];
        assert(first.pkg === "…\\Domain\\Order" && first.title.includes("Acme\\Shop\\Domain\\Order"), `a type the namespace found shows its short name in its namespace: ${JSON.stringify(first)}`);
      }
      await closePalette();
    }
  });

  await run("a vendor filling intelephense's limit crowds none of the own types out, the global namespace's included", async () => {
    await page.goto(`${BASE}/projects/${crowded}/editor`, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    await page.waitForSelector('.editor-item[data-path="composer.json"]', { timeout: 10000 });
    await page.click(".editor-tree");
    await openTypes("order");
    await waitTypeRows(page, 100);
    const state = await palette();
    const got = state.rows.slice(0, 6).map((r) => `${r.kind} ${r.name} ${r.pkg} ${r.place}${r.library ? " library" : ""}`.replace(/ +/g, " "));
    const want = [
      "class Order …\\Domain\\Order src/Shop/Order.php:9",
      "enum OrderState …\\Domain\\Order src/Shop/Order.php:18",
      "class OrderPrinter legacy/OrderPrinter.php:4",
      "interface Payable …\\Domain\\Order src/Shop/Order.php:14",
      "trait Loggable …\\Domain\\Order src/Shop/Order.php:16",
      "class OrderVendor0000 Acme\\Lib vendor/acme/lib/VendorOrder.php:3 library",
    ];
    assert(got.join("\n") === want.join("\n"), `rows:\n${got.join("\n")}`);
    assert(state.rows.slice(5).every((r) => r.library), "an own type behind the vendor's");
    assert(!state.rows.some((r) => /OrderScript|order-row/.test(r.name)), "a script's class or a style's selector of the HTML part is listed");
    assert(state.foot[0] === "100 types" && state.foot[1] === "truncated, narrow the search", `foot ${state.foot}`);
    await page.keyboard.press("ArrowDown");
    await page.keyboard.press("ArrowDown");
    await page.keyboard.press("Enter");
    await page.waitForFunction(() => document.querySelector('.editor-tab[aria-selected="true"]')?.dataset.path === "legacy/OrderPrinter.php", null, { timeout: 10000 });
    const at = await editorAt();
    assert(at.pos === "4:7", `Enter landed on ${JSON.stringify(at)}`);
  });

  await run("a project no language server covers says so in the type mode", async () => {
    await page.goto(`${BASE}/projects/${bare}/editor`, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    await page.waitForSelector('.editor-item[data-path="notes.txt"]', { timeout: 10000 });
    await page.click(".editor-tree");
    await openTypes();
    await waitNote(page, "No language server covers this project.");
    await page.keyboard.type("Widget");
    await sleep(500);
    await waitNote(page, "No language server covers this project.");
    assert((await palette()).rows.length === 0, "rows in a project nothing covers");
    await shiftTwice();
    await page.waitForFunction(() => document.querySelector("[data-editor-quickopen-input]").placeholder === "Go to file…", null, { timeout: 5000 });
    await closePalette();
  });

  await run("phone: a tap switches between files and types, a tap on a row opens the declaration, a library's type marked within the width", async () => {
    const mp = await mobilePage();
    await mp.goto(base, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(mp);
    await mp.waitForSelector("[data-editor-drawer-toggle]", { timeout: 10000 });
    await mp.waitForSelector(".editor.editor-drawer-open", { timeout: 4000 }).catch(() => {});
    if (!(await mp.$(".editor.editor-drawer-open"))) await mp.tap("[data-editor-drawer-toggle]");
    await mp.tap('.editor-item[data-path="go.mod"]');
    await mp.waitForFunction(() => document.querySelector('.editor-tab[aria-selected="true"]')?.dataset.path === "go.mod", null, { timeout: 10000 });
    await sleep(500);
    await mp.tap("[data-editor-menu]");
    await mp.waitForSelector("[data-editor-menu-list].show", { timeout: 4000 });
    await mp.tap("[data-editor-quick-open-item]");
    await mp.waitForFunction(() => !document.querySelector("[data-editor-quickopen]").hidden, null, { timeout: 5000 });
    await mp.fill("[data-editor-quickopen-input]", "shape");
    await mp.tap('[data-editor-quickopen-kind="types"]');
    await waitTypeRows(mp, 1);
    let state = await palette(mp);
    assert(state.placeholder === "Go to type…" && state.value === "shape" && state.rows[0].name === "Shape", `types on a phone ${JSON.stringify(state)}`);
    const fit = await mp.evaluate(() => ({ page: document.documentElement.scrollWidth, width: innerWidth, kinds: document.querySelector("[data-editor-quickopen-kinds]").getBoundingClientRect().right }));
    assert(fit.page <= fit.width && fit.kinds <= fit.width, `the palette leaves the screen: ${JSON.stringify(fit)}`);
    await mp.tap('[data-editor-quickopen-kind="files"]');
    await mp.waitForFunction(() => document.querySelector("[data-editor-quickopen-input]").placeholder === "Go to file…", null, { timeout: 5000 });
    await mp.tap('[data-editor-quickopen-kind="types"]');
    await waitTypeRows(mp, 1);
    await mp.tap("[data-editor-quickopen-list] .editor-quickopen-item");
    await mp.waitForFunction(() => document.querySelector('.editor-tab[aria-selected="true"]')?.dataset.path === "types.go", null, { timeout: 10000 });
    state = await palette(mp);
    const at = await editorAt(mp);
    assert(!state.open && at.pos === "7:6", `the tap landed on ${JSON.stringify(at)}`);

    await mp.tap("[data-editor-menu]");
    await mp.waitForSelector("[data-editor-menu-list].show", { timeout: 4000 });
    await mp.tap("[data-editor-type-open-item]");
    await mp.waitForFunction(() => !document.querySelector("[data-editor-quickopen]").hidden, null, { timeout: 5000 });
    await mp.fill("[data-editor-quickopen-input]", "reader");
    await waitTypeRows(mp, 1);
    const mark = await mp.evaluate(() => {
      const badge = document.querySelector("[data-editor-quickopen-list] [data-editor-type-library]");
      const box = badge?.getBoundingClientRect();
      return badge ? { width: box.width, right: box.right, screen: innerWidth, visibility: getComputedStyle(badge).visibility, page: document.documentElement.scrollWidth } : null;
    });
    assert(mark && mark.width > 0 && mark.right <= mark.screen && mark.page <= mark.screen && mark.visibility !== "hidden", `the library mark on a phone: ${JSON.stringify(mark)}`);
    await mp.tap("[data-editor-quickopen-list] .editor-quickopen-item");
    await mp.waitForFunction(() => document.querySelector('.editor-tab[aria-selected="true"]')?.dataset.path === "/usr/local/go/src/io/io.go", null, { timeout: 20000 });
    assert((await editorAt(mp)).pos === "86:6", "the tap opens the standard library's declaration");
    await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
  });

  await run("cleanup: the runner's projects", async () => {
    await L.deleteProject(page, project);
    await L.deleteProject(page, bare);
    await L.deleteProject(page, mixed);
    await L.deleteProject(page, crowded);
  });
});
