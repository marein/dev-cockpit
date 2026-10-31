const L = require("./lib");
const { assert, sleep, BASE } = L;

// The structure of the open file in the editor's structure sheet: Ctrl+Alt+O
// opening it at the editor's right edge with its filter focused, Escape
// closing it; the answer of a server that answers while it indexes and of
// gopls, which answers once its index stands; rows with the kind's icon and
// word, the name, the server's detail and the line, nested ones under their
// parent's name, in file order; typing filtering and ranking by the quick
// open rule, a filtered row naming its parent; the innermost symbol the
// cursor stands in marked and in view, the arrows walking from it, Enter and
// a click jumping; in a git diff and a diff against a revision, side by
// side and inline, the working copy's cursor marked and a jump opening the
// fold around its line; with the markdown preview shown, a heading
// scrolling the preview to it; a flat list and a mixed answer giving the
// tree; the editor's unsaved text; a markdown file's headings, also with every
// language server switched off; the notes of a file no language server
// covers and of no open file; Structure in the tab menu and in a file's tree
// menu; the phone's sheet from the bottom; the shortcuts dialog and /docs.
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

// Sixty functions, more than the sheet shows at once.
const LONG_GO = `package shop
${Array.from({ length: 60 }, (_, i) => `\nfunc F${String(i + 1).padStart(2, "0")}() int {\n\treturn ${i + 1}\n}\n`).join("")}`;

// Two sections of one name far down, so only the preview's own id finds the
// second one.
const LONG_MD = `# Top\n\n${"Words.\n\n".repeat(60)}## Middle\n\n${"Words.\n\n".repeat(60)}## Middle\n\n${"Words.\n\n".repeat(60)}## End\n`;
const SECOND_MIDDLE = LONG_MD.split("\n").lastIndexOf("## Middle") + 1;

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

  const sheet = (p = page) => p.evaluate(() => {
    const visible = (el) => !!el && el.getClientRects().length > 0 && getComputedStyle(el).visibility !== "hidden";
    const body = document.querySelector("[data-editor-sheet-body]");
    const empty = document.querySelector("[data-editor-sheet-empty]");
    return {
      open: visible(document.querySelector("[data-editor-sheet-panel]")),
      title: document.querySelector("[data-editor-sheet-title]").textContent,
      focused: document.activeElement === document.querySelector("[data-editor-sheet-filter]"),
      note: body.querySelector("[data-editor-structure-note]")?.textContent || "",
      noteDanger: !!body.querySelector("[data-editor-structure-note].text-danger"),
      empty: visible(empty) ? empty.textContent : "",
      count: document.querySelector("[data-editor-sheet-filter-count]").textContent,
      rows: [...body.querySelectorAll(".editor-sheet-row")].map((row) => {
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
          selected: row.classList.contains("selected"),
          focused: row.contains(document.activeElement),
          title: row.querySelector(".editor-sheet-open").title,
        };
      }),
    };
  });
  const tree = (state) => state.rows.map((r) => `${"  ".repeat(r.depth)}${r.kind} ${r.name} [${r.detail}] ${r.line} ${r.icon}`);
  const marked = (state) => state.rows.filter((r) => r.selected).map((r) => r.name).join(",");
  const waitRows = (p, n) => p.waitForFunction((count) => document.querySelectorAll("[data-editor-sheet-body] .editor-sheet-row").length === count, n, { timeout: 30000 });
  const waitNote = (p, text) => p.waitForFunction((t) => (document.querySelector("[data-editor-sheet-body] [data-editor-structure-note]")?.textContent || "") === t, text, { timeout: 20000 });
  const waitShut = (p = page) => p.waitForFunction(() => document.querySelector("[data-editor-sheet]").getClientRects().length === 0, null, { timeout: 5000 });
  const editorAt = (p = page) => p.evaluate(() => ({
    path: document.querySelector('.editor-tab[aria-selected="true"]')?.dataset.path || "",
    pos: document.querySelector("[data-editor-pos]")?.textContent || "",
  }));
  const openStructure = async () => {
    await page.keyboard.press("Control+Alt+o");
    await page.waitForFunction(() => document.querySelector("[data-editor-sheet-title]").textContent.startsWith("Structure"), null, { timeout: 5000 });
  };
  const closeSheet = async () => {
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
    ]) {
      await postTo(base, "/create", { path });
      await postTo(base, "/file", { path, content });
    }
  });

  await run("ctrl+alt+o opens the structure in a sheet at the right edge with its filter focused, escape closes it; intelephense answers while the index is held, its mixed answer in file order, gopls once its index stands, never an empty or failed answer meanwhile", async () => {
    await page.goto(base, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    await page.waitForSelector('.editor-item[data-path="order.go"]', { timeout: 10000 });
    await page.evaluate(() => {
      window.__structureNotes = [];
      const body = document.querySelector("[data-editor-sheet-body]");
      new MutationObserver(() => {
        const el = body.querySelector("[data-editor-structure-note]");
        const note = el ? `${el.textContent}${el.classList.contains("text-danger") ? " (danger)" : ""}` : "";
        if (note && window.__structureNotes.at(-1) !== note) window.__structureNotes.push(note);
      }).observe(body, { childList: true, subtree: true, characterData: true });
    });
    const indexing = () => page.evaluate(() => {
      const el = document.querySelector("[data-editor-lsp-index]");
      return el.getClientRects().length > 0 ? el.textContent.replace(/\s+/g, " ").trim() : "";
    });
    await openFile("page.php");
    await openStructure();
    await waitRows(page, 2);
    const state = await sheet();
    const box = await page.evaluate(() => {
      const outer = document.querySelector("[data-editor-sheet]").getBoundingClientRect();
      const panel = document.querySelector("[data-editor-sheet-panel]").getBoundingClientRect();
      return { right: outer.right - panel.right, bottom: outer.bottom - panel.bottom, share: panel.width / outer.width };
    });
    assert(state.open && state.title === "Structure of page.php" && state.focused, `the sheet ${JSON.stringify({ ...state, rows: undefined })}`);
    assert(tree(state).join("\n") === "class Invoice [] 3 ti-letter-c\nclass InvoiceScript [] 5 ti-letter-c", `rows:\n${tree(state).join("\n")}`);
    assert(Math.abs(box.right) < 1 && Math.abs(box.bottom) < 1 && Math.abs(box.share - 0.75) < 0.01, `the sheet does not stand at the right edge: ${JSON.stringify(box)}`);
    const held = await indexing();
    assert(held.startsWith("Indexing"), `the index was not held while the PHP structure came: "${held}"`);
    await closeSheet();
    assert(!(await page.evaluate(() => !!document.activeElement.closest("[data-editor-sheet]"))), "the focus stayed in the closed sheet");

    await openFile("order.go");
    await openStructure();
    await sleep(1500);
    const before = await sheet();
    const goHeld = await indexing();
    assert(before.rows.length === 0 && goHeld.startsWith("Indexing Go…"), `gopls answered before its index stood: ${before.rows.length} rows, "${goHeld}"`);
    await postTo(base, "/delete", { path: ".fake-lsp-hold" });
    await waitRows(page, ORDER_TREE.length);
    const notes = await page.evaluate(() => window.__structureNotes);
    assert(!notes.some((n) => /declares nothing|danger/.test(n)), `an empty or failed answer showed while waiting: ${JSON.stringify(notes)}`);
    await closeSheet();
    return notes.join(" | ");
  });

  await run("each row shows the kind's icon and word, the name, the detail and the line, nested ones under their parent's name, in file order", async () => {
    await openStructure();
    await waitRows(page, ORDER_TREE.length);
    const state = await sheet();
    assert(tree(state).join("\n") === ORDER_TREE.join("\n"), `rows:\n${tree(state).join("\n")}`);
    assert(state.rows.every((r) => r.lineShown), "a line number is not shown");
    for (const [child, parent] of [[2, 1], [3, 1], [5, 4]]) {
      const offset = Math.abs(state.rows[child].iconLeft - state.rows[parent].nameLeft);
      assert(offset < 1.5, `row ${child} is not under its parent's name (${offset}px off)`);
    }
    assert(state.rows[0].title === "constant MaxItems, line 4" && state.rows[2].title === "field ID in Order string, line 7", `titles ${state.rows[0].title} | ${state.rows[2].title}`);
    await closeSheet();
  });

  await run("typing filters and ranks by the quick open rule, a filtered row names its parent, a query nothing matches says so, an emptied query gives the tree back, a click jumps", async () => {
    await openStructure();
    await waitRows(page, ORDER_TREE.length);
    await page.keyboard.type("or");
    await waitRows(page, 5);
    let state = await sheet();
    const got = state.rows.map((r) => `${r.kind} ${r.name}/${r.mark}/${r.parent}/${r.depth}`);
    const want = ["struct Order/Or//0", "function NewOrder/Or//0", "method (*Order).Add/Or//0", "field ID//Order/0", "field Total//Order/0"];
    assert(got.join(" | ") === want.join(" | ") && state.count === "5 of 8", `ranked rows ${got.join(" | ")}, count ${state.count}`);
    await page.fill("[data-editor-sheet-filter]", "zzz");
    await waitRows(page, 0);
    assert((await sheet()).empty === 'Nothing matches "zzz".', "a query nothing matches says nothing");
    await page.fill("[data-editor-sheet-filter]", "");
    await waitRows(page, ORDER_TREE.length);
    assert(tree(await sheet()).join("\n") === ORDER_TREE.join("\n"), "the emptied query did not give the tree back");
    await page.fill("[data-editor-sheet-filter]", "or total");
    await waitRows(page, 1);
    state = await sheet();
    assert(state.rows[0].name === "Total" && state.rows[0].parent === "Order", `two words ${JSON.stringify(state.rows)}`);
    await page.click("[data-editor-sheet-body] .editor-sheet-open");
    await waitShut();
    const at = await editorAt();
    assert(at.path === "order.go" && at.pos === "8:2", `the click landed on ${JSON.stringify(at)}`);
  });

  await run("the innermost symbol the cursor stands in is the marked row and in view, enter jumps to it, the arrows walk from it, outside every symbol the first row is marked, a query marks its first row", async () => {
    await cursorOn("Total int", 8);
    await openStructure();
    await waitRows(page, ORDER_TREE.length);
    assert(marked(await sheet()) === "Total", `a struct's field marked "${marked(await sheet())}"`);
    await jumped("Enter", "8:2");

    await cursorOn("runner's Go file", 3);
    await openStructure();
    await waitRows(page, ORDER_TREE.length);
    assert(marked(await sheet()) === "MaxItems", `outside every symbol "${marked(await sheet())}" was marked`);
    await page.keyboard.type("pric");
    await waitRows(page, 2);
    assert(marked(await sheet()) === "Pricer", `the query marked "${marked(await sheet())}"`);
    await page.fill("[data-editor-sheet-filter]", "");
    await waitRows(page, ORDER_TREE.length);
    await page.keyboard.press("ArrowDown");
    const walked = (await sheet()).rows.findIndex((r) => r.focused);
    assert(walked === 1, `ArrowDown from the first row reached row ${walked}`);
    await jumped("Enter", "6:6");

    await openFile("long.go");
    await page.click(".cm-content");
    await page.keyboard.press("Control+End");
    await page.keyboard.press("ArrowUp");
    await page.keyboard.press("ArrowUp");
    await page.waitForFunction(() => document.querySelector("[data-editor-pos]")?.textContent === "240:1", null, { timeout: 5000 });
    await openStructure();
    await waitRows(page, 60);
    assert(marked(await sheet()) === "F60", `the last function's body marked "${marked(await sheet())}"`);
    const view = await page.evaluate(() => {
      const body = document.querySelector("[data-editor-sheet-body]");
      const box = body.getBoundingClientRect();
      const row = body.querySelector(".editor-sheet-row.selected").getBoundingClientRect();
      let around = 0;
      for (let el = body.parentElement; el; el = el.parentElement) around += el.scrollTop;
      return { top: row.top - box.top, bottom: box.bottom - row.bottom, scrolled: body.scrollTop, around };
    });
    assert(view.scrolled > 0 && view.top >= 0 && view.bottom >= 0 && view.around === 0, `the marked row is not in view: ${JSON.stringify(view)}`);
    await page.keyboard.press("ArrowUp");
    await jumped("Enter", "235:6");
  });

  await run("tsgo's flat list nests by what encloses it, a method's local stays out", async () => {
    await openFile("billing.ts");
    await openStructure();
    await waitRows(page, 6);
    const got = tree(await sheet());
    const want = [
      "variable Order [] 1 ti-variable",
      "variable Money [] 1 ti-variable",
      "variable VAT_RATE [] 3 ti-variable",
      "class Billing [] 5 ti-letter-c",
      "  property total [] 6 ti-point",
      "  method add [] 8 ti-math-function",
    ];
    assert(got.join("\n") === want.join("\n"), `rows:\n${got.join("\n")}`);
    await closeSheet();
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
    const last = tree(await sheet()).at(-1);
    assert(last === "constant Unsaved [] 23 ti-lock", `the unsaved symbol: ${last}`);
    const disk = await page.evaluate(async (url) => (await (await fetch(url, { headers: { Accept: "application/json" } })).json()).content, `${base}/file?path=order.go`);
    assert(!disk.includes("Unsaved"), "the text was saved, the check proves nothing");
    await closeSheet();
    for (let i = 0; i < 20 && (await dirty()); i++) await page.keyboard.press("Control+z");
    assert(!(await dirty()), "the typed line is still there");
  });

  await run("in the diff, side by side and inline, against HEAD and a revision, the sheet marks the symbol at the working copy's cursor, a click and enter open the fold around the row's line and jump there on the working copy", async () => {
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
      assert(marked(await sheet()) === "F02", `${round}: marked "${marked(await sheet())}"`);
      if (key === "click") {
        await page.click("[data-editor-sheet-body] .editor-sheet-row:nth-child(50) .editor-sheet-open");
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
    await page.click("[data-editor-sheet-body] .editor-sheet-row:nth-child(3) .editor-sheet-open");
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
    const state = await sheet();
    assert(tree(state).join("\n") === NOTES_TREE.join("\n"), `rows:\n${tree(state).join("\n")}`);
    assert(marked(state) === "Install" && state.rows[3].title === "h3 Linux apt in Setup, line 18", `marked "${marked(state)}", title ${state.rows[3].title}`);
    const glyphs = await page.$$eval("[data-editor-sheet-body] [data-editor-structure-icon]", (els) => els.map((el) => getComputedStyle(el, "::before").content));
    assert(glyphs.every((g) => g && g !== "none" && g !== '""'), `an icon has no glyph: ${JSON.stringify(glyphs)}`);
    await jumped("Enter", "7:4");
  });

  await run("a file no language server covers says so, no open file says so", async () => {
    await openFile("notes.txt");
    await openStructure();
    await waitNote(page, "No language server covers this file.");
    const state = await sheet();
    assert(state.rows.length === 0 && !state.noteDanger && state.title === "Structure of notes.txt", `notes.txt ${JSON.stringify(state)}`);
    await closeSheet();

    await page.goto(`${BASE}/projects/${bare}/editor`, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    await page.waitForSelector(".editor-tree", { timeout: 10000 });
    await page.click(".editor-tree");
    await openStructure();
    await waitNote(page, "Open a file to see its structure.");
    assert((await sheet()).title === "Structure", "the sheet names a file");
    await closeSheet();
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
    assert((await editorAt()).path === "order.go" && (await sheet()).title === "Structure of order.go", "the tab menu opened another file's structure");
    await closeSheet();

    assert(!(await page.$('.editor-tab[data-path="tax.go"]')), "tax.go is open already, the check proves nothing");
    await page.click('.editor-item[data-path="tax.go"]', { button: "right" });
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
    await structureEntry().click();
    await waitRows(page, 1);
    const state = await sheet();
    assert((await editorAt()).path === "tax.go" && state.title === "Structure of tax.go" && state.rows[0].name === "Tax", `the tree menu ${JSON.stringify(state)}`);
    await closeSheet();

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
      assert(tree(await sheet()).join("\n") === NOTES_TREE.join("\n"), "the headings differ without a language server");
      await closeSheet();
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
    assert(dialog.note === "It opens in a sheet on the symbol the cursor stands in. A markdown file lists its headings.", `the note reads "${dialog.note}"`);
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
    assert(item.desc === "Opens a sheet with the types, functions, methods, properties and constants of the open file with its unsaved changes, nested in file order, each with its kind, the signature or type the language server gives and its line. A markdown file lists its headings, nested by level. Structure in the menu of a tab or of a file in the tree opens it too, for a closed file after opening it. It opens on the innermost symbol the cursor stands in. Typing narrows the list, Enter jumps to the line, in a diff on the working copy, and scrolls a shown preview to the heading. PHP answers while it still indexes, Go once its index stands.", `the docs text: ${item.desc}`);
    const sheets = items.find((i) => i.title === "The sheets take the keyboard");
    assert(sheets.desc.startsWith("The git sheet, the docker sheet, the open files, a file's history and its structure open with the first row marked, the structure with the symbol at the cursor:"), `the sheets' text: ${sheets.desc}`);
    assert(items.filter((i) => `${i.keys} ${i.desc}`.includes("Alt+O")).length === 1, "another docs item names Ctrl+Alt+O");
  });

  await run("phone: the tab menu's structure comes up from the bottom over the full width, a tap on a row jumps", async () => {
    const mp = await mobilePage();
    await mp.goto(base, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(mp);
    await mp.waitForSelector("[data-editor-drawer-toggle]", { timeout: 10000 });
    await mp.waitForSelector(".editor.editor-drawer-open", { timeout: 4000 }).catch(() => {});
    if (!(await mp.$(".editor.editor-drawer-open"))) await mp.tap("[data-editor-drawer-toggle]");
    await mp.tap('.editor-item[data-path="order.go"]');
    await mp.waitForFunction(() => document.querySelector('.editor-tab[aria-selected="true"]')?.dataset.path === "order.go", null, { timeout: 10000 });
    await sleep(500);
    await mp.tap('.editor-tab[data-path="order.go"] .editor-tab-name');
    await mp.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
    await structureEntry(mp).click();
    await waitRows(mp, ORDER_TREE.length);
    const fit = await mp.evaluate(() => {
      const outer = document.querySelector("[data-editor-sheet]").getBoundingClientRect();
      const panel = document.querySelector("[data-editor-sheet-panel]").getBoundingClientRect();
      return {
        page: document.documentElement.scrollWidth,
        width: innerWidth,
        panel: [panel.left - outer.left, outer.right - panel.right, outer.bottom - panel.bottom],
        lines: [...document.querySelectorAll("[data-editor-structure-line]")].map((el) => el.getBoundingClientRect().right),
      };
    });
    assert(fit.page <= fit.width && fit.panel.every((d) => Math.abs(d) < 1) && fit.lines.every((r) => r <= fit.width), `the sheet does not stand full width at the bottom: ${JSON.stringify(fit)}`);
    await mp.tap("[data-editor-sheet-body] .editor-sheet-row:nth-child(7) .editor-sheet-open");
    await waitShut(mp);
    assert((await editorAt(mp)).pos === "15:6", "the tap did not land on NewOrder");
    await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
  });

  await run("cleanup: the runner's projects", async () => {
    await L.deleteProject(page, project);
    await L.deleteProject(page, bare);
  });
});
