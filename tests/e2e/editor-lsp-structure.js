const L = require("./lib");
const { assert, sleep, BASE } = L;

// The structure of the open file in the quick open palette: Ctrl+Alt+O
// opening it with the file named and the field focused, without the switch
// between files and types and without a sheet, pressed again closing it,
// switching to it from files and from find in files, Escape closing it with
// the focus back in the code; the answer of a server that answers while it
// indexes and of gopls, which answers once its index stands; rows with the
// kind's icon and word, the name, the server's detail and the line, nested
// ones under their parent's name, in file order; typing filtering and
// ranking by the quick open rule, a filtered row naming its parent; the
// innermost symbol the cursor stands in marked and in view, the arrows
// walking from it and wrapping, Enter and a click jumping; in a git diff and
// a diff against a revision, side by side and inline, the working copy's
// cursor marked and a jump opening the fold around its line; with the
// markdown preview shown, a heading scrolling the preview to it; a flat list
// and a mixed answer giving the tree; the editor's unsaved text; a markdown
// file's headings, also with every language server switched off, and the
// mark of an answer cut at its limit; the notes of a file no language server
// covers and of no open file; Structure in the tab menu and in a file's tree
// menu; the phone's palette with taps and typing; the shortcuts dialog and
// /docs.
// Routes: POST /projects/:name/editor/lsp/structure (internal/editorintelligence
// Structure over textDocument/documentSymbol), POST /projects/:name/editor/headings
// (internal/editorintelligence MarkdownStructure over internal/markdown Headings).
// Instance: the one editor-lsp.js runs on, the fake language servers and the
// fake docker first on PATH (tests/e2e/fake-lsp.py, see README.md). Its
// contract: gopls' tree off the Go file's text with gopls' details, tsgo's
// flat list off the TypeScript file (its imports, a class's members and a
// method's local, each naming its container), intelephense's class lines
// beside the plain entries of an HTML part, gopls only once its index stands;
// a `.fake-lsp-hold` file holds every server's indexing until it is gone,
// and every start builds for three seconds first.

const ORDER_GO = `package shop

// The structure runner's Go file.
const MaxItems = 10

type Order struct {
	ID    string
	Total int
}

type Pricer interface {
	Price(o Order) int
}

func NewOrder(id string) *Order {
	return &Order{ID: id}
}

func (o *Order) Add(amount int) int {
	o.Total += amount
	return o.Total
}
`;

const BILLING_TS = `import { Order, Money } from "./order";

export const VAT_RATE = 0.19;

export class Billing {
  total = 0;

  add(order: Order) {
    const amount = order.total;
    this.total += amount;
  }
}
`;

// Sixty functions, more than the palette shows at once.
const LONG_GO = `package shop
${Array.from({ length: 60 }, (_, i) => `\nfunc F${String(i + 1).padStart(2, "0")}() int {\n\treturn ${i + 1}\n}\n`).join("")}`;

// Two sections of one name far down, so only the preview's own id finds the
// second one.
const LONG_MD = `# Top\n\n${"Words.\n\n".repeat(60)}## Middle\n\n${"Words.\n\n".repeat(60)}## Middle\n\n${"Words.\n\n".repeat(60)}## End\n`;
const SECOND_MIDDLE = LONG_MD.split("\n").lastIndexOf("## Middle") + 1;

// One heading more than a structure answer carries.
const MANY_MD = Array.from({ length: 2001 }, (_, i) => `# H${i + 1}\n`).join("");

const PAGE_PHP = `<?php

class Invoice {}
?>
<script>class InvoiceScript {}</script>
`;

const NOTES_MD = `Intro before any heading.

# Guide

Some words.

## Install

\`\`\`sh
# not a heading
Title
---
\`\`\`

Setup
-----

### Linux \`apt\`

~~~
## not one either
~~~

Usage
=====

#### Deep

## Notes ##
`;

const NOTES_TREE = [
  "h1 Guide [] 3 ti-h-1",
  "  h2 Install [] 7 ti-h-2",
  "  h2 Setup [] 15 ti-h-2",
  "    h3 Linux apt [] 18 ti-h-3",
  "h1 Usage [] 24 ti-h-1",
  "  h4 Deep [] 27 ti-h-4",
  "  h2 Notes [] 29 ti-h-2",
];

const ORDER_TREE = [
  "constant MaxItems [] 4 ti-lock",
  "struct Order [struct{...}] 6 ti-box",
  "  field ID [string] 7 ti-point",
  "  field Total [int] 8 ti-point",
  "interface Pricer [interface{...}] 11 ti-plug",
  "  method Price [func(o Order) int] 12 ti-math-function",
  "function NewOrder [func(id string) *Order] 15 ti-math-function",
  "method (*Order).Add [func(amount int) int] 19 ti-math-function",
];

L.runFeature("EDITOR-LSP-STRUCTURE", async ({ engine, page, run, mobilePage }) => {
  const tag = `structure-${engine}-${Date.now().toString(36)}`;
  const project = `zzlsps-${tag}`;
  const bare = `zzlspsn-${tag}`;
  const base = `${BASE}/projects/${project}/editor`;

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
    const list = document.querySelector("[data-editor-quickopen-list]");
    const input = document.querySelector("[data-editor-quickopen-input]");
    const context = document.querySelector("[data-editor-quickopen-structure]");
    const note = list.querySelector(".editor-quickopen-empty");
    return {
      open: visible(document.querySelector("[data-editor-quickopen]")),
      sheet: visible(document.querySelector("[data-editor-sheet]")),
      context: visible(context) ? context.textContent : "",
      placeholder: input.placeholder,
      value: input.value,
      kinds: visible(document.querySelector("[data-editor-quickopen-kinds]")),
      focused: document.activeElement === input,
      note: note?.textContent || "",
      noteDanger: !!note?.classList.contains("text-danger"),
      foot: [document.querySelector("[data-editor-quickopen-foot-left]").textContent, document.querySelector("[data-editor-quickopen-foot-right]").textContent],
      rows: [...list.querySelectorAll(".editor-quickopen-item")].map((row) => {
        const icon = row.querySelector("[data-editor-structure-icon]");
        const name = row.querySelector(".editor-quickopen-name");
        return {
          depth: row.querySelectorAll(".ti.invisible").length,
          icon: [...icon.classList].find((c) => c.startsWith("ti-")),
          iconLeft: icon.getBoundingClientRect().left,
          nameLeft: name.getBoundingClientRect().left,
          kind: row.querySelector(".badge").textContent,
          name: name.textContent,
          mark: name.querySelector("mark")?.textContent || "",
          parent: row.querySelector("[data-editor-structure-parent]")?.textContent || "",
          detail: row.querySelector(".editor-quickopen-dir").textContent,
          line: row.querySelector("[data-editor-structure-line]").textContent,
          lineShown: visible(row.querySelector("[data-editor-structure-line]")),
          selected: row.classList.contains("active"),
          title: row.title,
        };
      }),
    };
  });
  const tree = (state) => state.rows.map((r) => `${"  ".repeat(r.depth)}${r.kind} ${r.name} [${r.detail}] ${r.line} ${r.icon}`);
  const marked = (state) => state.rows.filter((r) => r.selected).map((r) => r.name).join(",");
  const ROW = "[data-editor-quickopen-list] .editor-quickopen-item";
  const FIELD = "[data-editor-quickopen-input]";
  const waitRows = (p, n) => p.waitForFunction((count) => document.querySelectorAll("[data-editor-quickopen-list] .editor-quickopen-item").length === count, n, { timeout: 30000 });
  const waitNote = (p, text) => p.waitForFunction((t) => (document.querySelector("[data-editor-quickopen-list] .editor-quickopen-empty")?.textContent || "") === t, text, { timeout: 20000 });
  const waitShut = (p = page) => p.waitForFunction(() => document.querySelector("[data-editor-quickopen]").getClientRects().length === 0, null, { timeout: 5000 });
  const waitStructure = (p = page) => p.waitForFunction(() => {
    const el = document.querySelector("[data-editor-quickopen]");
    return el.getClientRects().length > 0 && document.querySelector("[data-editor-quickopen-input]").placeholder === "Go to symbol…";
  }, null, { timeout: 5000 });
  const editorAt = (p = page) => p.evaluate(() => ({
    path: document.querySelector('.editor-tab[aria-selected="true"]')?.dataset.path || "",
    pos: document.querySelector("[data-editor-pos]")?.textContent || "",
  }));
  const openStructure = async () => {
    await page.keyboard.press("Control+Alt+o");
    await waitStructure();
  };
  const closePalette = async () => {
    await page.keyboard.press("Escape");
    await waitShut();
  };
  const jumped = async (key, pos) => {
    await page.keyboard.press(key);
    await waitShut();
    const at = await editorAt();
    assert(at.pos === pos, `${key} landed on ${JSON.stringify(at)}, not ${pos}`);
  };
  const openFile = async (path, p = page) => {
    await p.click(`.editor-item[data-path="${path}"]`);
    await p.waitForFunction((f) => document.querySelector('.editor-tab[aria-selected="true"]')?.dataset.path === f, path, { timeout: 10000 });
    await p.waitForSelector(".cm-content", { timeout: 8000 });
  };
  const cursorOn = async (text, line) => {
    await page.click(`.cm-line:has-text(${JSON.stringify(text)})`);
    await page.waitForFunction((l) => document.querySelector("[data-editor-pos]")?.textContent.startsWith(`${l}:`), line, { timeout: 5000 });
  };
  const menuEntries = (p = page) => p.$$eval(".dc-context-menu .dropdown-item", (els) => els.map((el) => [...el.children].map((c) => c.textContent.trim()).filter(Boolean).join(" ")));
  const structureEntry = (p = page) => p.locator(".dc-context-menu .dropdown-item", { hasText: /^\s*Structure/ });

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

  await run("setup: a project of Go, TypeScript and PHP held in its indexing, a project without files", async () => {
    await L.createProject(page, project);
    await L.createProject(page, bare);
    await L.setEditorFiles(page, { autosave: false });
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    for (const [path, content] of [
      [".fake-lsp-hold", "hold\n"],
      ["go.mod", "module example.com/shop\n"],
      ["order.go", ORDER_GO],
      ["long.go", LONG_GO],
      ["tax.go", "package shop\n\nfunc Tax(n int) int {\n\treturn n\n}\n"],
      ["billing.ts", BILLING_TS],
      ["page.php", PAGE_PHP],
      ["notes.txt", "Order is a word here.\n"],
      ["notes.md", NOTES_MD],
      ["long.md", LONG_MD],
      ["many.md", MANY_MD],
    ]) {
      await postTo(base, "/create", { path });
      await postTo(base, "/file", { path, content });
    }
  });

  await run("ctrl+alt+o opens the structure in the quick open palette with the file named, the field focused, no switch and no sheet, pressed again it closes, escape closes it with the focus back in the code; intelephense answers while the index is held, its mixed answer in file order, gopls once its index stands, a wait note and never an empty or failed answer meanwhile", async () => {
    await page.goto(base, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    await page.waitForSelector('.editor-item[data-path="order.go"]', { timeout: 10000 });
    await page.evaluate(() => {
      window.__structureNotes = [];
      const list = document.querySelector("[data-editor-quickopen-list]");
      new MutationObserver(() => {
        const el = list.querySelector(".editor-quickopen-empty");
        const note = el ? `${el.textContent}${el.classList.contains("text-danger") ? " (danger)" : ""}` : "";
        if (note && window.__structureNotes.at(-1) !== note) window.__structureNotes.push(note);
      }).observe(list, { childList: true, subtree: true, characterData: true });
    });
    const indexing = () => page.evaluate(() => {
      const el = document.querySelector("[data-editor-lsp-index]");
      return el.getClientRects().length > 0 ? el.textContent.replace(/\s+/g, " ").trim() : "";
    });
    const inCode = () => page.evaluate(() => !!document.activeElement?.closest(".cm-content"));
    await openFile("page.php");
    await page.click(".cm-content");
    await openStructure();
    await waitRows(page, 2);
    const state = await palette();
    assert(state.open && !state.sheet && state.context === "Structure of page.php" && state.focused && !state.kinds && state.value === "", `the palette ${JSON.stringify({ ...state, rows: undefined })}`);
    assert(tree(state).join("\n") === "class Invoice [] 3 ti-letter-c\nclass InvoiceScript [] 5 ti-letter-c" && state.foot.join("|") === "2 symbols|", `rows:\n${tree(state).join("\n")}\nfoot ${state.foot}`);
    const held = await indexing();
    assert(held.startsWith("Indexing"), `the index was not held while the PHP structure came: "${held}"`);
    const phpNotes = await page.evaluate(() => window.__structureNotes.splice(0));
    assert(phpNotes.length > 0 && phpNotes.every((n) => n === "Loading…" || n === "Preparing PHP…"), `the wait said ${JSON.stringify(phpNotes)}`);
    await page.keyboard.press("Control+Alt+o");
    await waitShut();
    assert(await inCode(), "the second Ctrl+Alt+O left the focus outside the code");
    await openStructure();
    await waitRows(page, 2);
    await closePalette();
    assert(await inCode(), "Escape left the focus outside the code");

    await openFile("order.go");
    await openStructure();
    await sleep(1500);
    const before = await palette();
    const goHeld = await indexing();
    assert(before.rows.length === 0 && before.note === "Loading…" && goHeld.startsWith("Indexing Go…"), `gopls answered before its index stood: ${before.rows.length} rows, "${before.note}", "${goHeld}"`);
    await postTo(base, "/delete", { path: ".fake-lsp-hold" });
    await waitRows(page, ORDER_TREE.length);
    const notes = await page.evaluate(() => window.__structureNotes);
    assert(!notes.some((n) => /declares nothing|danger/.test(n)), `an empty or failed answer showed while waiting: ${JSON.stringify(notes)}`);
    await closePalette();
    return [...phpNotes, ...notes].join(" | ");
  });

  await run("from go to file and from find in files ctrl+alt+o switches to the structure with an empty field, ctrl+o leaves it for the types with the switch back", async () => {
    await page.keyboard.press("Control+Shift+o");
    await page.waitForFunction(() => document.querySelector("[data-editor-quickopen-input]").placeholder === "Go to file…", null, { timeout: 5000 });
    await page.keyboard.type("order");
    await page.waitForSelector('[data-editor-quickopen-list] .editor-quickopen-item[data-path="order.go"]', { timeout: 10000 });
    await openStructure();
    await waitRows(page, ORDER_TREE.length);
    let state = await palette();
    assert(state.value === "" && !state.kinds && state.context === "Structure of order.go" && !state.sheet, `from files ${JSON.stringify({ ...state, rows: undefined })}`);
    await page.keyboard.press("Control+o");
    await page.waitForFunction(() => document.querySelector("[data-editor-quickopen-input]").placeholder === "Go to type…", null, { timeout: 5000 });
    state = await palette();
    assert(state.kinds && !state.context, `Ctrl+O from the structure ${JSON.stringify({ ...state, rows: undefined })}`);
    await closePalette();
    await page.keyboard.press("Control+Shift+f");
    await page.waitForFunction(() => document.querySelector("[data-editor-quickopen-input]").placeholder === "Find in files…", null, { timeout: 5000 });
    await page.keyboard.type("Order");
    await openStructure();
    await waitRows(page, ORDER_TREE.length);
    state = await palette();
    assert(state.value === "" && state.context === "Structure of order.go", `from find in files ${JSON.stringify({ ...state, rows: undefined })}`);
    await closePalette();
  });

  await run("each row shows the kind's icon and word, the name, the detail and the line, nested ones under their parent's name, in file order", async () => {
    await openStructure();
    await waitRows(page, ORDER_TREE.length);
    const state = await palette();
    assert(tree(state).join("\n") === ORDER_TREE.join("\n"), `rows:\n${tree(state).join("\n")}`);
    assert(state.rows.every((r) => r.lineShown), "a line number is not shown");
    for (const [child, parent] of [[2, 1], [3, 1], [5, 4]]) {
      const offset = Math.abs(state.rows[child].iconLeft - state.rows[parent].nameLeft);
      assert(offset < 1.5, `row ${child} is not under its parent's name (${offset}px off)`);
    }
    assert(state.rows[0].title === "constant MaxItems, line 4" && state.rows[2].title === "field ID in Order string, line 7", `titles ${state.rows[0].title} | ${state.rows[2].title}`);
    await closePalette();
  });

  await run("typing filters and ranks by the quick open rule, a filtered row names its parent, a query nothing matches says so, an emptied query gives the tree back, a click jumps", async () => {
    await openStructure();
    await waitRows(page, ORDER_TREE.length);
    await page.keyboard.type("or");
    await waitRows(page, 5);
    let state = await palette();
    const got = state.rows.map((r) => `${r.kind} ${r.name}/${r.mark}/${r.parent}/${r.depth}`);
    const want = ["struct Order/Or//0", "function NewOrder/Or//0", "method (*Order).Add/Or//0", "field ID//Order/0", "field Total//Order/0"];
    assert(got.join(" | ") === want.join(" | ") && state.foot[0] === "5 of 8 symbols", `ranked rows ${got.join(" | ")}, count ${state.foot[0]}`);
    await page.fill(FIELD, "zzz");
    await waitRows(page, 0);
    state = await palette();
    assert(state.note === "No matching symbols." && state.foot[0] === "0 of 8 symbols", `a query nothing matches says "${state.note}", "${state.foot[0]}"`);
    await page.fill(FIELD, "");
    await waitRows(page, ORDER_TREE.length);
    assert(tree(await palette()).join("\n") === ORDER_TREE.join("\n"), "the emptied query did not give the tree back");
    await page.fill(FIELD, "or total");
    await waitRows(page, 1);
    state = await palette();
    assert(state.rows[0].name === "Total" && state.rows[0].parent === "Order", `two words ${JSON.stringify(state.rows)}`);
    await page.click(ROW);
    await waitShut();
    const at = await editorAt();
    assert(at.path === "order.go" && at.pos === "8:2", `the click landed on ${JSON.stringify(at)}`);
  });

  await run("the innermost symbol the cursor stands in is the marked row and in view, enter jumps to it, the arrows walk from it and wrap, outside every symbol the first row is marked, a query marks its first row", async () => {
    await cursorOn("Total int", 8);
    await openStructure();
    await waitRows(page, ORDER_TREE.length);
    assert(marked(await palette()) === "Total", `a struct's field marked "${marked(await palette())}"`);
    await jumped("Enter", "8:2");

    await cursorOn("runner's Go file", 3);
    await openStructure();
    await waitRows(page, ORDER_TREE.length);
    assert(marked(await palette()) === "MaxItems", `outside every symbol "${marked(await palette())}" was marked`);
    await page.keyboard.type("pric");
    await waitRows(page, 2);
    assert(marked(await palette()) === "Pricer", `the query marked "${marked(await palette())}"`);
    await page.fill(FIELD, "");
    await waitRows(page, ORDER_TREE.length);
    const at = async () => (await palette()).rows.findIndex((r) => r.selected);
    await page.keyboard.press("ArrowUp");
    const wrapped = await at();
    await page.keyboard.press("ArrowDown");
    const back = await at();
    await page.keyboard.press("ArrowDown");
    const walked = await at();
    assert(wrapped === ORDER_TREE.length - 1 && back === 0 && walked === 1, `ArrowUp from the first row reached row ${wrapped}, ArrowDown then ${back} and ${walked}`);
    assert((await palette()).focused, "the arrows took the focus off the field");
    await jumped("Enter", "6:6");

    await openFile("long.go");
    await page.click(".cm-content");
    await page.keyboard.press("Control+End");
    await page.keyboard.press("ArrowUp");
    await page.keyboard.press("ArrowUp");
    await page.waitForFunction(() => document.querySelector("[data-editor-pos]")?.textContent === "240:1", null, { timeout: 5000 });
    await openStructure();
    await waitRows(page, 60);
    assert(marked(await palette()) === "F60", `the last function's body marked "${marked(await palette())}"`);
    const view = await page.evaluate(() => {
      const body = document.querySelector("[data-editor-quickopen-list]");
      const box = body.getBoundingClientRect();
      const row = body.querySelector(".editor-quickopen-item.active").getBoundingClientRect();
      let around = 0;
      for (let el = body.parentElement; el; el = el.parentElement) around += el.scrollTop;
      return { top: row.top - box.top, bottom: box.bottom - row.bottom, scrolled: body.scrollTop, around };
    });
    assert(view.scrolled > 0 && view.top >= 0 && view.bottom >= -1 && view.around === 0, `the marked row is not in view: ${JSON.stringify(view)}`);
    await page.keyboard.press("ArrowUp");
    await jumped("Enter", "235:6");
  });

  await run("tsgo's flat list nests by what encloses it, a method's local stays out", async () => {
    await openFile("billing.ts");
    await openStructure();
    await waitRows(page, 6);
    const got = tree(await palette());
    const want = [
      "variable Order [] 1 ti-variable",
      "variable Money [] 1 ti-variable",
      "variable VAT_RATE [] 3 ti-variable",
      "class Billing [] 5 ti-letter-c",
      "  property total [] 6 ti-point",
      "  method add [] 8 ti-math-function",
    ];
    assert(got.join("\n") === want.join("\n"), `rows:\n${got.join("\n")}`);
    await closePalette();
  });

  await run("the structure shows the editor's unsaved text", async () => {
    await openFile("order.go");
    await page.click(".cm-content");
    await page.keyboard.press("Control+End");
    await page.keyboard.type("const Unsaved = 1\n");
    const dirty = () => page.evaluate(() => document.querySelector('.editor-tab[data-path="order.go"]').classList.contains("dirty"));
    assert(await dirty(), "the tab is not marked unsaved");
    await openStructure();
    await waitRows(page, ORDER_TREE.length + 1);
    const last = tree(await palette()).at(-1);
    assert(last === "constant Unsaved [] 23 ti-lock", `the unsaved symbol: ${last}`);
    const disk = await page.evaluate(async (url) => (await (await fetch(url, { headers: { Accept: "application/json" } })).json()).content, `${base}/file?path=order.go`);
    assert(!disk.includes("Unsaved"), "the text was saved, the check proves nothing");
    await closePalette();
    for (let i = 0; i < 20 && (await dirty()); i++) await page.keyboard.press("Control+z");
    assert(!(await dirty()), "the typed line is still there");
  });

  await run("in the diff, side by side and inline, against HEAD and a revision, the structure marks the symbol at the working copy's cursor, a click and enter open the fold around the row's line and jump there on the working copy", async () => {
    const shell = await L.createShell(page, project);
    await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
    await sleep(2000);
    const author = "-c user.email=e2e@example.com -c user.name=e2e -c commit.gpgsign=false";
    await page.evaluate(([href, raw]) => fetch(`${href}/input`, {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]').content },
      body: JSON.stringify({ items: [{ raw }] }),
    }), [new URL(shell).pathname, `git init -q && git add -A && git ${author} commit -qm one && git ${author} commit -q --allow-empty -m two\r`]);
    const deadline = Date.now() + 30000;
    let log = [];
    while (Date.now() < deadline && log.length < 2) {
      await sleep(500);
      log = await page.evaluate((url) => fetch(url, { headers: { Accept: "application/json" } }).then((r) => (r.ok ? r.json() : {})).then((d) => d.commits || []), `${base}/git/log`);
    }
    assert(log.length === 2, `the project has ${log.length} commits, not two`);
    await L.deleteShell(page, shell);
    await postTo(base, "/file", { path: "long.go", content: LONG_GO.replace("return 1\n", "return 100\n") });

    const workingCopy = (side) => (side ? ".cm-mergeView .cm-merge-b" : ".editor-surface > .cm-editor");
    const working = (side) => page.evaluate((sel) => {
      const view = document.querySelector(sel);
      const surface = view.closest("[data-editor-surface]").getBoundingClientRect();
      const line = [...view.querySelectorAll(".cm-line")].find((l) => l.textContent === "func F50() int {");
      const box = line?.getBoundingClientRect();
      return {
        merge: !!document.querySelector(".cm-mergeView"),
        folds: view.querySelectorAll(".cm-collapsedLines").length,
        inView: !!box && box.top >= surface.top && box.bottom <= surface.bottom,
      };
    }, workingCopy(side));
    for (const [view, rev, key] of [["side", "", "click"], ["inline", "", "Enter"], ["side", "HEAD~1", "Enter"], ["inline", "HEAD~1", "click"]]) {
      const round = `${view} ${rev || "HEAD"} ${key}`;
      const side = view === "side";
      await page.evaluate((v) => {
        const settings = JSON.parse(localStorage.getItem("dc-editor-settings") || "{}");
        localStorage.setItem("dc-editor-settings", JSON.stringify({ ...settings, diff_view: v, diff_collapse: true }));
      }, view);
      await page.goto(base, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      await page.waitForSelector('.editor-item[data-path="long.go"]', { timeout: 10000 });
      await page.waitForFunction(() => document.querySelector("dc-editor")?.dataset.gitRepo === "1", null, { timeout: 20000 });
      await openFile("long.go");
      if (rev) {
        await page.click('.editor-tab[data-path="long.go"]', { button: "right" });
        await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
        await page.locator(".dc-context-menu .dropdown-item", { hasText: /^Diff against revision/ }).first().click();
        await page.waitForSelector("[data-editor-sheet-body] input", { timeout: 8000 });
        await page.fill("[data-editor-sheet-body] input", rev);
        await page.keyboard.press("Enter");
        await page.waitForFunction((r) => document.querySelector("[data-editor-status]").textContent.includes(`Diff against ${r}`), rev, { timeout: 15000 });
      } else {
        await page.click(".cm-content");
        await page.keyboard.press("Control+Alt+d");
      }
      await page.waitForFunction(([merge, sel]) => !!document.querySelector(".cm-mergeView") === merge && document.querySelector(`${sel} .cm-collapsedLines`), [side, workingCopy(side)], { timeout: 20000 });
      const before = await working(side);
      assert(before.folds > 0 && !before.inView, `${round}: F50 is not folded away first: ${JSON.stringify(before)}`);
      await page.click(`${workingCopy(side)} .cm-line:has-text("func F02()")`);
      await page.waitForFunction(() => document.querySelector("[data-editor-pos]")?.textContent.startsWith("7:"), null, { timeout: 5000 });
      await openStructure();
      await waitRows(page, 60);
      assert(marked(await palette()) === "F02", `${round}: marked "${marked(await palette())}"`);
      if (key === "click") {
        await page.click(`${ROW}:nth-child(50)`);
        await waitShut();
      } else {
        await page.keyboard.type("F50");
        await waitRows(page, 1);
        await page.keyboard.press("Enter");
        await waitShut();
      }
      await page.waitForFunction(() => document.querySelector("[data-editor-pos]")?.textContent === "199:6", null, { timeout: 5000 });
      await sleep(300);
      const after = await working(side);
      assert(after.merge === side && after.inView && after.folds < before.folds, `${round}: F50 is not in view on the working copy: ${JSON.stringify(after)}`);
      await page.keyboard.press("Control+Alt+d");
      await page.waitForFunction(() => !document.querySelector(".cm-mergeView, .cm-collapsedLines"), null, { timeout: 20000 });
    }
  });

  await run("with the markdown preview shown, a heading puts the cursor on its line and scrolls the preview to that heading", async () => {
    await openFile("long.md");
    await page.click(".cm-content");
    await page.keyboard.press("Control+Alt+p");
    await page.waitForSelector("[data-editor-preview-pane] #user-content-middle-1", { timeout: 10000 });
    await openStructure();
    await waitRows(page, 4);
    await page.click(`${ROW}:nth-child(3)`);
    await waitShut();
    await page.waitForFunction((pos) => document.querySelector("[data-editor-pos]")?.textContent === pos, `${SECOND_MIDDLE}:4`, { timeout: 5000 });
    await page.waitForFunction(() => {
      const pane = document.querySelector("[data-editor-preview-pane]");
      const top = (id) => pane.querySelector(`#${id}`).getBoundingClientRect().top - pane.getBoundingClientRect().top;
      return pane.scrollTop > 0 && Math.abs(top("user-content-middle-1")) < 2 && top("user-content-middle") < 0;
    }, null, { timeout: 5000 });
    await page.keyboard.press("Control+Alt+p");
    await page.waitForFunction(() => document.querySelector("[data-editor-preview-pane]").hidden, null, { timeout: 5000 });
  });

  await run("a markdown file lists its headings, hash and underlined, nested by level in file order, none from a fenced block, the section at the cursor marked, enter jumping to its words", async () => {
    await openFile("notes.md");
    await cursorOn("# not a heading", 10);
    await openStructure();
    await waitRows(page, NOTES_TREE.length);
    const state = await palette();
    assert(tree(state).join("\n") === NOTES_TREE.join("\n"), `rows:\n${tree(state).join("\n")}`);
    assert(marked(state) === "Install" && state.rows[3].title === "h3 Linux apt in Setup, line 18", `marked "${marked(state)}", title ${state.rows[3].title}`);
    const glyphs = await page.$$eval("[data-editor-quickopen-list] [data-editor-structure-icon]", (els) => els.map((el) => getComputedStyle(el, "::before").content));
    assert(glyphs.every((g) => g && g !== "none" && g !== '""'), `an icon has no glyph: ${JSON.stringify(glyphs)}`);
    await jumped("Enter", "7:4");
  });

  await run("an answer cut at its limit says so under the unfiltered list", async () => {
    await openFile("many.md");
    await openStructure();
    await waitRows(page, 2000);
    let state = await palette();
    assert(state.foot.join("|") === "2000 symbols|Only the first 2000 symbols are shown." && state.rows.at(-1).name === "H2000", `the cut list ${state.foot}, last ${state.rows.at(-1).name}`);
    await page.keyboard.type("H1999");
    await waitRows(page, 1);
    state = await palette();
    assert(state.foot.join("|") === "1 of 2000 symbols|", `the filtered cut list ${state.foot}`);
    await closePalette();
  });

  await run("a file no language server covers says so, no open file says so", async () => {
    await openFile("notes.txt");
    await openStructure();
    await waitNote(page, "No language server covers this file.");
    const state = await palette();
    assert(state.rows.length === 0 && !state.noteDanger && state.context === "Structure of notes.txt", `notes.txt ${JSON.stringify(state)}`);
    await closePalette();

    await page.goto(`${BASE}/projects/${bare}/editor`, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    await page.waitForSelector(".editor-tree", { timeout: 10000 });
    await page.click(".editor-tree");
    await openStructure();
    await waitNote(page, "Open a file to see its structure.");
    assert((await palette()).context === "Structure", "the palette names a file");
    await closePalette();
  });

  await run("structure in the tab menu brings its file to the front with its structure, in the tree menu it opens a closed file and its structure, a file nothing structures and the editor menu carry no entry", async () => {
    await page.goto(base, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    await page.waitForSelector('.editor-tab[data-path="order.go"]', { timeout: 10000 });
    await openFile("notes.md");
    await page.click('.editor-tab[data-path="order.go"]', { button: "right" });
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
    const entry = (await menuEntries()).find((e) => e.startsWith("Structure"));
    assert(entry === "Structure Ctrl+Alt+O", `the tab menu's entry reads "${entry}"`);
    await structureEntry().click();
    await waitRows(page, ORDER_TREE.length);
    assert((await editorAt()).path === "order.go" && (await palette()).context === "Structure of order.go", "the tab menu opened another file's structure");
    await closePalette();

    assert(!(await page.$('.editor-tab[data-path="tax.go"]')), "tax.go is open already, the check proves nothing");
    await page.click('.editor-item[data-path="tax.go"]', { button: "right" });
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
    await structureEntry().click();
    await waitRows(page, 1);
    const state = await palette();
    assert((await editorAt()).path === "tax.go" && state.context === "Structure of tax.go" && state.rows[0].name === "Tax", `the tree menu ${JSON.stringify(state)}`);
    await closePalette();

    await page.click('.editor-item[data-path="notes.txt"]', { button: "right" });
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
    assert(!(await menuEntries()).some((e) => e.startsWith("Structure")), "notes.txt offers a structure");
    await page.keyboard.press("Escape");
    await page.click("[data-editor-menu]");
    await page.waitForSelector("[data-editor-menu-list].show", { timeout: 4000 });
    const items = await page.$$eval("[data-editor-menu-list] .dropdown-item", (els) => els.map((el) => el.textContent.trim()));
    assert(!items.some((t) => /structure/i.test(t)), `the editor menu carries ${items.join(" | ")}`);
    await page.keyboard.press("Escape");
  });

  await run("with every language server switched off, a markdown file still lists its headings", async () => {
    const pick = async (value) => {
      await page.goto(`${BASE}/settings/editor/lsp`, { waitUntil: "domcontentloaded" });
      for (const [id, server] of servers) await page.selectOption(`select[name="server_${id}"]`, value(server));
      await saveLSPSettings();
    };
    await pick(() => "off");
    try {
      await page.goto(base, { waitUntil: "domcontentloaded" });
      await page.waitForSelector('.editor-item[data-path="notes.md"]', { timeout: 10000 });
      const spec = await page.$eval("[data-editor-lsp]", (el) => el.dataset.editorLsp);
      assert(spec === "", `the page still names languages: "${spec}"`);
      await openFile("notes.md");
      await openStructure();
      await waitRows(page, NOTES_TREE.length);
      assert(tree(await palette()).join("\n") === NOTES_TREE.join("\n"), "the headings differ without a language server");
      await closePalette();
    } finally {
      await pick((server) => `${server}-docker`);
    }
  });

  await run("the shortcuts dialog and the docs say where the structure opens", async () => {
    await page.goto(base, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    await page.waitForSelector(".editor-tree", { timeout: 10000 });
    await page.click("[data-editor-menu]");
    await page.waitForSelector("[data-editor-menu-list].show", { timeout: 4000 });
    await page.click('[data-editor-menu-list] [data-bs-target="#editor-shortcuts-modal"]');
    await L.modalShown(page, "editor-shortcuts-modal");
    const dialog = await page.evaluate(() => {
      const rows = [...document.querySelectorAll("#editor-shortcuts-modal .dc-shortcuts-row")];
      const row = rows.find((r) => r.textContent.includes("File structure"));
      const next = row?.nextElementSibling;
      return {
        row: row?.textContent.replace(/\s+/g, " ").trim(),
        note: next?.classList.contains("dc-shortcuts-note") ? next.textContent.trim() : "",
        alt: rows.filter((r) => r.textContent.replace(/\s+/g, "").includes("Ctrl+Alt+O")).length,
      };
    });
    assert(dialog.row === "File structure Ctrl+Alt+O" && dialog.alt === 1, `the dialog ${JSON.stringify(dialog)}`);
    assert(dialog.note === "It opens in the quick open palette on the symbol the cursor stands in. A markdown file lists its headings.", `the note reads "${dialog.note}"`);
    await page.click('#editor-shortcuts-modal [data-bs-dismiss="modal"]');
    await page.waitForFunction(() => !document.querySelector("#editor-shortcuts-modal").classList.contains("show"), null, { timeout: 5000 });

    await page.goto(`${BASE}/docs`, { waitUntil: "domcontentloaded" });
    const items = await page.evaluate(() => [...document.querySelectorAll("#editor .list-group-item")].map((row) => ({
      title: row.querySelector(".fw-medium")?.textContent || "",
      keys: [...row.querySelectorAll(".dc-docs-item-head kbd")].map((k) => k.textContent).join("+"),
      desc: row.querySelector(".mt-1")?.textContent || "",
    })));
    const item = items.find((i) => i.title === "File structure");
    assert(item && item.keys === "Ctrl+Alt+O", `the docs item: ${JSON.stringify(item)}`);
    assert(item.desc === "Opens the quick open palette with the types, functions, methods, properties and constants of the open file with its unsaved changes, nested in file order, each with its kind, the signature or type the language server gives and its line. A markdown file lists its headings, nested by level. Structure in the menu of a tab or of a file in the tree opens it too, for a closed file after opening it. It opens on the innermost symbol the cursor stands in. Typing narrows the list, Enter jumps to the line, in a diff on the working copy, and scrolls a shown preview to the heading. PHP answers while it still indexes, Go once its index stands.", `the docs text: ${item.desc}`);
    const sheets = items.find((i) => i.title === "The sheets take the keyboard");
    assert(sheets.desc.startsWith("The git sheet, the forge sheet, the docker sheet, the open files and a file's history open with the first row marked:") && !/structure/i.test(sheets.desc), `the sheets' text: ${sheets.desc}`);
    assert(items.filter((i) => `${i.keys} ${i.desc}`.includes("Alt+O")).length === 1, "another docs item names Ctrl+Alt+O");
  });

  await run("phone: the tab menu's structure opens in the palette where go to type opens, inside the width, a tap on a row jumps, a tap on the field, typing and enter jump", async () => {
    const mp = await mobilePage();
    await mp.goto(base, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(mp);
    await mp.waitForSelector("[data-editor-drawer-toggle]", { timeout: 10000 });
    await mp.waitForSelector(".editor.editor-drawer-open", { timeout: 4000 }).catch(() => {});
    if (!(await mp.$(".editor.editor-drawer-open"))) await mp.tap("[data-editor-drawer-toggle]");
    await mp.tap('.editor-item[data-path="order.go"]');
    await mp.waitForFunction(() => document.querySelector('.editor-tab[aria-selected="true"]')?.dataset.path === "order.go", null, { timeout: 10000 });
    await sleep(500);
    const panelBox = () => mp.evaluate(() => {
      const box = document.querySelector(".editor-quickopen-panel").getBoundingClientRect();
      return [box.left, box.top, box.width, box.height].map(Math.round).join(",");
    });
    await mp.tap("[data-editor-menu]");
    await mp.waitForSelector("[data-editor-menu-list].show", { timeout: 4000 });
    await mp.tap("[data-editor-type-open-item]");
    await mp.waitForFunction(() => document.querySelector("[data-editor-quickopen-input]").placeholder === "Go to type…", null, { timeout: 5000 });
    const types = await panelBox();
    await mp.tap("[data-editor-quickopen]", { position: { x: 5, y: 5 } });
    await waitShut(mp);
    const openFromTab = async () => {
      await mp.tap('.editor-tab[data-path="order.go"] .editor-tab-name');
      await mp.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
      await structureEntry(mp).tap();
      await waitStructure(mp);
      await waitRows(mp, ORDER_TREE.length);
    };
    await openFromTab();
    const fit = await mp.evaluate(() => {
      const panel = document.querySelector(".editor-quickopen-panel").getBoundingClientRect();
      return {
        page: document.documentElement.scrollWidth,
        width: innerWidth,
        panel: [panel.left, panel.right],
        lines: [...document.querySelectorAll("[data-editor-structure-line]")].map((el) => el.getBoundingClientRect().right),
        sheet: document.querySelector("[data-editor-sheet]").getClientRects().length,
      };
    });
    const structure = await panelBox();
    assert(structure === types, `the structure's panel ${structure} is not where go to type's stands ${types}`);
    assert(fit.page <= fit.width && fit.panel[0] >= 0 && fit.panel[1] <= fit.width && fit.lines.every((r) => r <= fit.panel[1]) && fit.sheet === 0, `the palette does not fit the phone: ${JSON.stringify(fit)}`);
    await mp.tap(`${ROW}:nth-child(7)`);
    await waitShut(mp);
    assert((await editorAt(mp)).pos === "15:6", "the tap did not land on NewOrder");

    // A tap within 350 ms of a menu closing belongs to that menu (menuJustClosed).
    await sleep(400);
    await openFromTab();
    await mp.tap(FIELD);
    await mp.keyboard.type("total");
    await waitRows(mp, 1);
    await mp.keyboard.press("Enter");
    await waitShut(mp);
    assert((await editorAt(mp)).pos === "8:2", `typing and Enter landed on ${(await editorAt(mp)).pos}`);
    await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
  });

  await run("cleanup: the runner's projects", async () => {
    await L.deleteProject(page, project);
    await L.deleteProject(page, bare);
  });
});
