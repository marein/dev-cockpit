const L = require("./lib");
const { assert, sleep, BASE } = L;

// Editor code navigation over LSP: Ctrl/Cmd+click and Ctrl+B jump to the
// definition, on the declaration the usages open instead, Shift+F12 lists
// them (bottom sheet on a phone), touch gets the cursor pill
// ([data-editor-lsp-pill]) with one Look up action; no context menu claim.
// Routes: POST /projects/:name/editor/lsp/{definition,references,close}
// (internal/editorintelligence).
// Instance: fake language server first on PATH as "gopls"
// (tests/e2e/fake-lsp.py, see README.md). Its contract: indexing announced
// finished right after the handshake; definitions answer lib.go 0-based
// line 2 chars 5-16 (covers IntelTarget's declaration, so requests inside
// read as declaration); line 0 has no definition; references answer use.go
// lines 3 and 4, the declaration, and one outside location.
// Gotchas: word positions are measured through a DOM Range over the text
// node (.cm-line is full width, highlighting splits identifiers); a press
// waits until its point resolves to .cm-content (closing drawer covers
// it); a phone with no tabs auto-opens the drawer.

L.runFeature("EDITOR-LSP", async ({ engine, page, run, mobilePage }) => {
  const tag = `lsp-${engine}-${Date.now().toString(36)}`;
  const project = `zzlsp-${tag}`;
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
  const post = (path, body) => postTo(base, path, body);

  const editorPath = (p = page) => p.evaluate(() => document.querySelector('.editor-tab[aria-selected=\"true\"]')?.dataset.path || "");
  const editorPos = (p = page) => p.evaluate(() => document.querySelector("[data-editor-pos]")?.textContent || "");
  const statusText = () => page.evaluate(() => document.querySelector("[data-editor-status]")?.textContent || "");
  const panelShowing = (p = page) => p.evaluate(() => !document.querySelector("[data-editor-quickopen]").hidden);

  // The center of a word, measured through a Range over its text node; see
  // the header.
  const wordPointOn = (p, lineNeedle, word) => p.evaluate(({ lineNeedle, word }) => {
    const el = [...document.querySelectorAll(".cm-line")].find((l) => l.textContent.includes(lineNeedle));
    if (!el) return null;
    const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
    let node;
    while ((node = walker.nextNode())) {
      const i = node.textContent.indexOf(word);
      if (i >= 0) {
        const r = document.createRange();
        r.setStart(node, i);
        r.setEnd(node, i + word.length);
        const rect = r.getBoundingClientRect();
        return { x: rect.x + rect.width / 2, y: rect.y + rect.height / 2 };
      }
    }
    return null;
  }, { lineNeedle, word });
  const wordPoint = (lineNeedle, word) => wordPointOn(page, lineNeedle, word);

  const tapAt = (p, pt) => p.touchscreen.tap(pt.x, pt.y);
  const pillShowing = (p) => p.evaluate(() => !!document.querySelector("[data-editor-lsp-pill]"));

  const ctrlClick = async (lineNeedle, word) => {
    const pt = await wordPoint(lineNeedle, word);
    assert(pt, `word ${word} on the surface`);
    await page.keyboard.down("Control");
    await page.mouse.click(pt.x, pt.y);
    await page.keyboard.up("Control");
  };

  const rightClick = async (lineNeedle, word) => {
    const pt = await wordPoint(lineNeedle, word);
    assert(pt, `word ${word} on the surface`);
    await page.mouse.click(pt.x, pt.y, { button: "right" });
  };

  const openFile = async (path) => {
    await page.click(`.editor-item[data-path="${path}"]`);
    await page.waitForFunction((p) => document.querySelector('.editor-tab[aria-selected=\"true\"]')?.dataset.path === p, path, { timeout: 10000 });
  };

  // Boosted form: only the swapped-in flash says the save is done, and
  // every caller starts from a fresh page so no stale flash satisfies it.
  const saveLSPSettings = async () => {
    await Promise.all([
      page.waitForResponse((r) => r.url().includes("/settings/editor/lsp") && r.request().method() === "POST", { timeout: 8000 }),
      page.click('#settings-editor-lsp button[type="submit"]'),
    ]);
    await page.waitForFunction(() => document.body.textContent.includes("Settings saved."), null, { timeout: 8000 });
  };

  // The pin comes before any editor page: the explicit Docker pick runs
  // deterministically over the fake docker, a fresh instance is asserted.
  await run("setup: a fresh instance defaults to Automatic, pinned to Docker for the run", async () => {
    await page.goto(`${BASE}/settings/editor/lsp`, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    const picked = await page.$$eval("#settings-editor-lsp select", (els) => els.map((sel) => `${sel.name}=${sel.value}`).join(","));
    const fresh = picked === "server_go=auto,server_php=auto,server_typescript=auto";
    const pinned = picked === "server_go=gopls-docker,server_php=intelephense-docker,server_typescript=tsgo-docker";
    assert(fresh || pinned, `Automatic on a fresh instance or the earlier engine's pin, got ${picked} (the runner needs a fresh throwaway)`);
    await page.selectOption('select[name="server_go"]', "gopls-docker");
    await page.selectOption('select[name="server_php"]', "intelephense-docker");
    await page.selectOption('select[name="server_typescript"]', "tsgo-docker");
    await saveLSPSettings();
    return fresh ? "fresh instance, Automatic was the default" : "pin from the earlier engine pass held";
  });

  await run("setup: project with lib.go and use.go", async () => {
    await L.createProject(page, project);
    for (const [path, content] of [
      ["lib.go", "package lib\n\nfunc IntelTarget() {}\n"],
      ["use.go", "package lib\n\nfunc use() {\n\tIntelTarget()\n\tIntelTarget()\n}\n"],
      ["notes.txt", "IntelTarget is documented here.\n"],
      // The two files whose lookups land outside the project, see the
      // fake's contract: one in the bound module cache, one in the image.
      ["deps.go", "package lib\n\nfunc useDep() {\n\tTarget()\n}\n"],
      ["stdlib.go", "package lib\n\nfunc useStd() {\n\tPrintln()\n}\n"],
    ]) {
      await post("/create", { path });
      await post("/file", { path, content });
    }
    await page.goto(base, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    await page.waitForSelector('.editor-item[data-path="use.go"]', { timeout: 10000 });
    await openFile("use.go");
    await page.waitForFunction(() => [...document.querySelectorAll(".cm-line")].some((l) => l.textContent.includes("IntelTarget(")), null, { timeout: 10000 });
  });

  await run("holding ctrl underlines the word under the mouse", async () => {
    const pt = await wordPoint("IntelTarget(", "IntelTarget");
    await page.keyboard.down("Control");
    await page.mouse.move(pt.x, pt.y);
    await sleep(250);
    const marked = await page.evaluate(() => document.querySelector(".cm-dc-lsp-target")?.textContent || "");
    await page.keyboard.up("Control");
    assert(marked === "IntelTarget", `underline marks the word, got "${marked}"`);
    await sleep(400);
    const gone = await page.evaluate(() => !document.querySelector(".cm-dc-lsp-target"));
    assert(gone, "the underline goes with the released modifier");
  });

  await run("ctrl+click on a usage jumps to the definition", async () => {
    await ctrlClick("IntelTarget(", "IntelTarget");
    await page.waitForFunction(() => document.querySelector('.editor-tab[aria-selected=\"true\"]')?.dataset.path === "lib.go", null, { timeout: 15000 });
    const pos = await editorPos();
    assert(/^3:6$/.test(pos), `cursor on the declaration, got "${pos}"`);
  });

  await run("ctrl+click on the declaration lists the usages instead", async () => {
    await ctrlClick("func IntelTarget", "IntelTarget");
    await page.waitForFunction(() => !document.querySelector("[data-editor-quickopen]").hidden
      && document.querySelectorAll(".editor-quickopen-item").length >= 3, null, { timeout: 20000 });
    const state = await page.evaluate(() => ({
      placeholder: document.querySelector("[data-editor-quickopen-input]").placeholder,
      titles: [...document.querySelectorAll(".editor-quickopen-item")].map((r) => r.title),
    }));
    assert(state.placeholder === '3 usages of "IntelTarget"', `placeholder, got "${state.placeholder}"`);
    assert(state.titles.join(",") === "lib.go:3,use.go:4,use.go:5", `asked file first, got ${state.titles.join(",")}`);
    assert((await editorPath()) === "lib.go", "no jump happened");
    await page.keyboard.press("Escape");
    await page.waitForFunction(() => document.querySelector("[data-editor-quickopen]").hidden, null, { timeout: 5000 });
  });

  await run("two quick ctrl+clicks stay in the editor, no switcher", async () => {
    await ctrlClick("func IntelTarget", "IntelTarget");
    await ctrlClick("func IntelTarget", "IntelTarget");
    await sleep(400);
    const switcher = await page.evaluate(() => !!document.querySelector(".terminal-switcher"));
    assert(!switcher, "the double tap machine must reset on pointerdown");
    assert((await editorPath()) === "lib.go", "still on the declaration");
    await page.keyboard.press("Escape");
    await page.waitForFunction(() => document.querySelector("[data-editor-quickopen]").hidden, null, { timeout: 5000 });
  });

  await run("ctrl+b jumps from the keyboard and keeps the declaration rule", async () => {
    // The cursor on a usage: the key jumps to the declaration; the cursor
    // then sits on the declaration, so the same key lists the usages.
    await openFile("use.go");
    const pt = await wordPoint("IntelTarget(", "IntelTarget");
    await page.mouse.click(pt.x, pt.y);
    await page.keyboard.press("Control+b");
    await page.waitForFunction(() => document.querySelector('.editor-tab[aria-selected=\"true\"]')?.dataset.path === "lib.go", null, { timeout: 15000 });
    const pos = await editorPos();
    assert(/^3:6$/.test(pos), `cursor on the declaration, got "${pos}"`);
    await page.keyboard.press("Control+b");
    await page.waitForFunction(() => !document.querySelector("[data-editor-quickopen]").hidden
      && document.querySelectorAll(".editor-quickopen-item").length >= 3, null, { timeout: 20000 });
    assert((await editorPath()) === "lib.go", "the declaration lists usages instead of jumping");
    await page.keyboard.press("Escape");
    await page.waitForFunction(() => document.querySelector("[data-editor-quickopen]").hidden, null, { timeout: 5000 });
    await openFile("use.go");
  });

  await run("shift+f12 lists the references with the outside note", async () => {
    const pt = await wordPoint("IntelTarget(", "IntelTarget");
    await page.mouse.click(pt.x, pt.y);
    await page.keyboard.press("Shift+F12");
    await page.waitForFunction(() => !document.querySelector("[data-editor-quickopen]").hidden
      && document.querySelectorAll(".editor-quickopen-item").length >= 3, null, { timeout: 20000 });
    const state = await page.evaluate(() => ({
      placeholder: document.querySelector("[data-editor-quickopen-input]").placeholder,
      titles: [...document.querySelectorAll(".editor-quickopen-item")].map((r) => r.title),
      marked: !!document.querySelector(".editor-quickopen-item mark"),
      note: [...document.querySelectorAll(".editor-quickopen-empty")].map((n) => n.textContent).join(" "),
    }));
    assert(state.placeholder === '3 usages of "IntelTarget"', `placeholder, got "${state.placeholder}"`);
    assert(state.titles.join(",") === "use.go:4,use.go:5,lib.go:3", `rows sorted asked-file first, got ${state.titles.join(",")}`);
    assert(state.marked, "the symbol is highlighted in the preview");
    assert(state.note.includes("1 more outside the project."), `outside note, got "${state.note}"`);
  });

  await run("typing in the usages panel narrows the rows live", async () => {
    // The panel from the previous check is still open.
    await page.fill("[data-editor-quickopen-input]", "lib");
    let titles = await page.$$eval(".editor-quickopen-item", (els) => els.map((r) => r.title));
    assert(titles.join(",") === "lib.go:3", `path filter, got ${titles.join(",")}`);
    await page.fill("[data-editor-quickopen-input]", "func");
    titles = await page.$$eval(".editor-quickopen-item", (els) => els.map((r) => r.title));
    assert(titles.join(",") === "lib.go:3", `preview filter, got ${titles.join(",")}`);
    await page.fill("[data-editor-quickopen-input]", "zzz");
    const empty = await page.evaluate(() => document.querySelector(".editor-quickopen-empty")?.textContent || "");
    assert(empty.includes("No matches."), `an unmatched filter says so, got "${empty}"`);
    await page.fill("[data-editor-quickopen-input]", "");
    const state = await page.evaluate(() => ({
      rows: document.querySelectorAll(".editor-quickopen-item").length,
      note: [...document.querySelectorAll(".editor-quickopen-empty")].map((n) => n.textContent).join(" "),
    }));
    assert(state.rows === 3, `an emptied box shows everything, got ${state.rows}`);
    assert(state.note.includes("1 more outside the project."), `the note returns with the whole answer, got "${state.note}"`);
  });

  await run("a usage row jumps to its line and column", async () => {
    await page.click('.editor-quickopen-item[title="use.go:5"]');
    await page.waitForFunction(() => document.querySelector("[data-editor-quickopen]").hidden, null, { timeout: 5000 });
    assert((await editorPath()) === "use.go", "landed in use.go");
    const pos = await editorPos();
    assert(/^5:2$/.test(pos), `cursor on the usage, got "${pos}"`);
    assert(await page.evaluate(() => !!document.activeElement?.closest?.(".cm-content")), "the pick hands the focus to the editor");
  });

  await run("escape closes the panel and hands the focus back to the editor", async () => {
    await page.keyboard.press("Shift+F12");
    await page.waitForFunction(() => !document.querySelector("[data-editor-quickopen]").hidden, null, { timeout: 20000 });
    await page.keyboard.press("Escape");
    await page.waitForFunction(() => document.querySelector("[data-editor-quickopen]").hidden, null, { timeout: 5000 });
    const state = await page.evaluate(() => ({
      focused: !!document.activeElement?.closest?.(".cm-content"),
      pos: document.querySelector("[data-editor-pos]")?.textContent || "",
    }));
    assert(state.focused, "the editor holds the focus again");
    assert(/^5:2$/.test(state.pos), `the cursor stands where it stood, got "${state.pos}"`);
  });

  await run("a right click on a symbol stays the browser's own menu", async () => {
    await rightClick("IntelTarget(", "IntelTarget");
    await sleep(600);
    assert(!(await page.$(".dc-context-menu")), "the surface claims no context menu");
  });

  // ---- targets outside the project ------------------------------------------

  const tabState = () => page.evaluate(() => {
    const tab = document.querySelector('.editor-tab[aria-selected="true"]');
    return {
      path: tab?.dataset.path || "",
      title: tab?.title || "",
      locked: !!tab?.querySelector(".ti-lock"),
      // Real visibility, never the attribute: a display utility and the
      // attribute on one element is exactly where that lies.
      readOnly: document.querySelector("[data-editor-readonly]").getClientRects().length > 0,
      readOnlyTitle: document.querySelector("[data-editor-readonly]").title,
      readOnlyPath: document.querySelector("[data-editor-readonly-path]").textContent,
      text: [...document.querySelectorAll(".cm-line")].map((l) => l.textContent).join("\n"),
    };
  });

  await run("a definition in a dependency opens read only, at its own path", async () => {
    await openFile("deps.go");
    await page.waitForFunction(() => [...document.querySelectorAll(".cm-line")].some((l) => l.textContent.includes("Target(")), null, { timeout: 10000 });
    await ctrlClick("Target(", "Target");
    await page.waitForFunction(() => (document.querySelector('.editor-tab[aria-selected="true"]')?.dataset.path || "").endsWith("/dep.go"), null, { timeout: 20000 });
    const state = await tabState();
    // The path is the module cache the cockpit binds, which is the whole
    // point: the same path inside the container and out here.
    assert(/\/editor-lsp\/dev-cockpit-gopls-[^/]+\/mod\/example\.com\/dep@v1\.0\.0\/dep\.go$/.test(state.path), `module cache path, got "${state.path}"`);
    assert(state.locked, "the tab carries the lock");
    assert(state.title.endsWith("· read only"), `the tab tooltip says read only, got "${state.title}"`);
    assert(state.readOnly, "the statusbar says read only");
    assert(state.readOnlyPath === "…/example.com/dep@v1.0.0/dep.go", `the statusbar says where it comes from, got "${state.readOnlyPath}"`);
    assert(state.readOnlyTitle === state.path, `and carries the whole path, got "${state.readOnlyTitle}"`);
    assert(state.text.includes("func Target() {}"), `the dependency's source is in the buffer, got "${state.text}"`);
    const pos = await editorPos();
    assert(/^4:6$/.test(pos), `cursor on the definition, got "${pos}"`);
  });

  await run("the read only buffer refuses typing and offers no save", async () => {
    await page.click(".cm-content");
    await page.keyboard.type("xxx");
    await sleep(200);
    const state = await page.evaluate(() => ({
      text: [...document.querySelectorAll(".cm-line")].map((l) => l.textContent).join("\n"),
      save: !document.querySelector("[data-editor-save]").hidden,
      dirty: !!document.querySelector('.editor-tab[aria-selected="true"]')?.classList.contains("dirty"),
    }));
    assert(!state.text.includes("xxx"), `typing must not reach the buffer, got "${state.text}"`);
    assert(!state.save, "no save button on a file that cannot be saved");
    assert(!state.dirty, "a read only tab never goes dirty");
  });

  await run("its tab menu keeps the close entries and the path, nothing that writes", async () => {
    await page.click('.editor-tab[aria-selected="true"]', { button: "right" });
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
    const labels = await page.$$eval(".dc-context-menu .dropdown-item", (els) => els.map((el) => el.textContent.trim()));
    await page.keyboard.press("Escape");
    assert(labels.join(",") === "Close,Close others,Close to the right,Close all,Copy path", `menu entries, got ${labels.join(",")}`);
  });

  await run("the read only tab survives a reload", async () => {
    const before = await editorPath();
    await page.reload({ waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    await page.waitForFunction((p) => [...document.querySelectorAll(".editor-tab")].some((t) => t.dataset.path === p), before, { timeout: 15000 });
    await page.click(`.editor-tab[data-path="${before.replace(/["\\]/g, "\\$&")}"]`);
    await page.waitForFunction((p) => document.querySelector('.editor-tab[aria-selected="true"]')?.dataset.path === p, before, { timeout: 10000 });
    const state = await tabState();
    assert(state.locked && state.readOnly, "it comes back read only");
    assert(state.text.includes("func Target() {}"), "and with its content");
  });

  // The chain: the cursor stands in a file outside the project, and the
  // lookup goes on from there, into the next file outside and back in.
  await run("chain: a lookup inside a read only tab leads to the next file outside", async () => {
    const from = await editorPath();
    await ctrlClick("func Target", "Target");
    await page.waitForFunction(() => document.querySelector('.editor-tab[aria-selected="true"]')?.dataset.path === "/usr/local/go/src/fmt/print.go", null, { timeout: 20000 });
    const state = await tabState();
    assert(state.locked && state.readOnly, `the second file outside opens read only, got ${JSON.stringify(state)}`);
    assert(state.text.includes("func Println(a ...any) {}"), `its content is the image's, got "${state.text}"`);
    assert(await page.evaluate((p) => [...document.querySelectorAll(".editor-tab")].some((t) => t.dataset.path === p), from), "the tab it came from stays open");
    return `${from} → /usr/local/go/src/fmt/print.go`;
  });

  await run("chain: find usages from outside the project lists the project's own", async () => {
    const pt = await wordPoint("func Println", "Println");
    await page.mouse.click(pt.x, pt.y);
    await page.keyboard.press("Shift+F12");
    await page.waitForFunction(() => !document.querySelector("[data-editor-quickopen]").hidden
      && document.querySelectorAll(".editor-quickopen-item").length >= 3, null, { timeout: 20000 });
    const panel = await page.evaluate(() => ({
      titles: [...document.querySelectorAll(".editor-quickopen-item")].map((r) => r.title),
      note: [...document.querySelectorAll(".editor-quickopen-empty")].map((n) => n.textContent).join(" "),
    }));
    assert(panel.titles.join(",") === "lib.go:3,use.go:4,use.go:5", `rows of the project, got ${panel.titles.join(",")}`);
    assert(panel.note.includes("1 more outside the project."), `the outside note stands, got "${panel.note}"`);
  });

  await run("chain: a row of the project jumps back into it, and the marks go", async () => {
    await page.click('.editor-quickopen-item[title="lib.go:3"]');
    await page.waitForFunction(() => document.querySelector('.editor-tab[aria-selected="true"]')?.dataset.path === "lib.go", null, { timeout: 15000 });
    const state = await tabState();
    assert(!state.locked && !state.readOnly, `a file of the project carries neither mark, got ${JSON.stringify(state)}`);
    assert(/^3:6$/.test(await editorPos()), `the cursor lands on the declaration, got "${await editorPos()}"`);
  });

  await run("a definition in the standard library is read out of the image", async () => {
    await openFile("stdlib.go");
    await page.waitForFunction(() => [...document.querySelectorAll(".cm-line")].some((l) => l.textContent.includes("Println(")), null, { timeout: 10000 });
    await ctrlClick("Println(", "Println");
    await page.waitForFunction(() => document.querySelector('.editor-tab[aria-selected="true"]')?.dataset.path === "/usr/local/go/src/fmt/print.go", null, { timeout: 20000 });
    const state = await tabState();
    assert(state.locked && state.readOnly, "the standard library opens read only too");
    assert(state.text.includes("func Println(a ...any) {}"), `the image's source is in the buffer, got "${state.text}"`);
    // And the mark goes with the tab: back on a file of the project the
    // statusbar says nothing about reading.
    await page.evaluate(() => document.querySelector('.editor-tab[aria-selected="true"] .editor-tab-close').click());
    await openFile("use.go");
    const back = await tabState();
    assert(!back.readOnly && !back.locked, `a project file carries neither mark, got ${JSON.stringify(back)}`);
  });

  await run("the source route serves the source roots and nothing else", async () => {
    const answers = await page.evaluate(async (url) => {
      const ask = async (path) => {
        const res = await fetch(`${url}?path=${encodeURIComponent(path)}`, { credentials: "same-origin", headers: { Accept: "application/json" } });
        return res.status;
      };
      return {
        stdlib: await ask("/usr/local/go/src/fmt/print.go"),
        passwd: await ask("/etc/passwd"),
        traversal: await ask("/usr/local/go/src/../../../../etc/passwd"),
        relative: await ask("lib.go"),
      };
    }, `${base}/lsp/source`);
    assert(answers.stdlib === 200, `a source root answers, got ${answers.stdlib}`);
    assert(answers.passwd === 400 && answers.traversal === 400 && answers.relative === 400,
      `everything else is refused, got ${JSON.stringify(answers)}`);
  });

  await run("a word without a definition says so in the statusbar", async () => {
    await openFile("use.go");
    await ctrlClick("package lib", "package");
    await page.waitForFunction(() => (document.querySelector("[data-editor-status]")?.textContent || "") === "No definition found.", null, { timeout: 20000 });
    return await statusText();
  });

  await run("the editor menu carries no usages entry", async () => {
    await page.click("[data-editor-menu]");
    const entry = await page.evaluate(() => !!document.querySelector("[data-editor-usages-item]"));
    await page.keyboard.press("Escape");
    assert(!entry, "the overflow menu must not carry a usages entry");
  });

  // ---- the touch path, on the phone context ---------------------------------

  // A phone with no open tabs opens the drawer itself.
  const mOpenFile = async (mp, path) => {
    const open = await mp.evaluate(() => !!document.querySelector(".editor.editor-drawer-open"));
    if (!open) {
      await mp.tap("[data-editor-drawer-toggle]");
      await mp.waitForSelector(".editor.editor-drawer-open", { timeout: 6000 });
    }
    await mp.tap(`.editor-item[data-path="${path}"]`);
    await mp.waitForFunction((p) => document.querySelector('.editor-tab[aria-selected=\"true\"]')?.dataset.path === p, path, { timeout: 10000 });
  };
  // The closing drawer still covers the point while its transition runs.
  const pressPoint = async (mp, lineNeedle, word) => {
    const pt = await wordPointOn(mp, lineNeedle, word);
    assert(pt, `word ${word} on the phone surface`);
    await mp.waitForFunction(({ x, y }) => {
      const el = document.elementFromPoint(x, y);
      return !!(el && el.closest(".cm-content"));
    }, pt, { timeout: 5000 });
    return pt;
  };
  const sheetShowing = (mp) => mp.evaluate(() => {
    const el = document.querySelector("[data-editor-sheet]");
    return !!el && el.getClientRects().length > 0;
  });

  await run("mobile: a tap on a symbol raises the cursor pill", async () => {
    const mp = await mobilePage();
    await mp.goto(base, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(mp);
    await mp.waitForSelector("[data-editor-drawer-toggle]", { timeout: 10000 });
    await mp.waitForSelector(".editor.editor-drawer-open", { timeout: 4000 }).catch(() => {});
    await mOpenFile(mp, "use.go");
    await mp.waitForFunction(() => [...document.querySelectorAll(".cm-line")].some((l) => l.textContent.includes("IntelTarget(")), null, { timeout: 10000 });
    const pt = await pressPoint(mp, "IntelTarget(", "IntelTarget");
    await tapAt(mp, pt);
    await mp.waitForSelector("[data-editor-lsp-pill]", { state: "visible", timeout: 8000 });
    const labels = await mp.$$eval("[data-editor-lsp-pill] button", (els) => els.map((b) => b.textContent.trim()));
    assert(labels.join(",") === "Look up", `one honest pill action: ${labels.join(",")}`);
  });

  await run("mobile: the pill's action jumps to the declaration", async () => {
    const mp = await mobilePage();
    await mp.tap("[data-pill-action]");
    await mp.waitForFunction(() => document.querySelector('.editor-tab[aria-selected=\"true\"]')?.dataset.path === "lib.go", null, { timeout: 15000 });
    const pos = await editorPos(mp);
    assert(/^3:6$/.test(pos), `cursor on the declaration, got "${pos}"`);
  });

  await run("mobile: on the declaration the pill's action lists the usages", async () => {
    const mp = await mobilePage();
    await mOpenFile(mp, "lib.go");
    const pt = await pressPoint(mp, "func IntelTarget", "IntelTarget");
    await tapAt(mp, pt);
    await mp.waitForSelector("[data-editor-lsp-pill]", { state: "visible", timeout: 8000 });
    await mp.tap("[data-pill-action]");
    await mp.waitForFunction(() => {
      const el = document.querySelector("[data-editor-sheet]");
      return !!el && el.getClientRects().length > 0
        && document.querySelectorAll("[data-editor-sheet-body] .editor-sheet-open").length >= 3;
    }, null, { timeout: 20000 });
    const sheet = await mp.evaluate(() => ({
      title: document.querySelector("[data-editor-sheet-title]").textContent,
      rows: [...document.querySelectorAll("[data-editor-sheet-body] .editor-sheet-open")].map((r) => r.title),
      marked: !!document.querySelector("[data-editor-sheet-body] mark"),
    }));
    assert(sheet.title === '3 usages of "IntelTarget"', `sheet title, got "${sheet.title}"`);
    assert(sheet.rows.join(",") === "lib.go:3,use.go:4,use.go:5", `sheet rows, got ${sheet.rows.join(",")}`);
    assert(sheet.marked, "the symbol is highlighted in the preview");
    await mp.locator('[data-editor-sheet-body] .editor-sheet-open[title="use.go:4"]').click();
    await mp.waitForFunction(() => document.querySelector('.editor-tab[aria-selected=\"true\"]')?.dataset.path === "use.go", null, { timeout: 15000 });
    assert(!(await sheetShowing(mp)), "the sheet closes with the jump");
    const pos = await editorPos(mp);
    assert(/^4:/.test(pos), `the row jump lands on its line, got "${pos}"`);
  });

  await run("mobile: typing in the sheet's filter narrows the rows live", async () => {
    const mp = await mobilePage();
    await mOpenFile(mp, "lib.go");
    const pt = await pressPoint(mp, "func IntelTarget", "IntelTarget");
    await tapAt(mp, pt);
    await mp.waitForSelector("[data-editor-lsp-pill]", { state: "visible", timeout: 8000 });
    await mp.tap("[data-pill-action]");
    await mp.waitForFunction(() => {
      const el = document.querySelector("[data-editor-sheet]");
      return !!el && el.getClientRects().length > 0
        && document.querySelectorAll("[data-editor-sheet-body] .editor-sheet-open").length >= 3;
    }, null, { timeout: 20000 });
    const input = mp.locator("[data-editor-sheet-body] input");
    assert(!(await mp.evaluate(() => document.activeElement?.tagName === "INPUT")), "the filter does not steal the focus on open");
    await input.fill("use");
    let rows = await mp.$$eval("[data-editor-sheet-body] .editor-sheet-open", (els) => els.map((r) => r.title));
    assert(rows.join(",") === "use.go:4,use.go:5", `path filter, got ${rows.join(",")}`);
    let note = await mp.evaluate(() => [...document.querySelectorAll("[data-editor-sheet-body] .text-secondary")].map((n) => n.textContent).join(" "));
    assert(!note.includes("outside the project"), `the note stands only unfiltered, got "${note}"`);
    await input.fill("func");
    rows = await mp.$$eval("[data-editor-sheet-body] .editor-sheet-open", (els) => els.map((r) => r.title));
    assert(rows.join(",") === "lib.go:3", `preview filter, got ${rows.join(",")}`);
    await input.fill("zzz");
    rows = await mp.$$eval("[data-editor-sheet-body] .editor-sheet-open", (els) => els.map((r) => r.title));
    assert(rows.length === 0, `an unmatched filter empties the list, got ${rows.join(",")}`);
    assert(await sheetShowing(mp), "filtering never closes the sheet");
    await input.fill("");
    const state = await mp.evaluate(() => ({
      rows: document.querySelectorAll("[data-editor-sheet-body] .editor-sheet-open").length,
      note: [...document.querySelectorAll("[data-editor-sheet-body] .text-secondary")].map((n) => n.textContent).join(" "),
    }));
    assert(state.rows === 3, `an emptied box shows everything, got ${state.rows}`);
    assert(state.note.includes("1 more outside the project."), `the note returns with the whole answer, got "${state.note}"`);
    await mp.tap("[data-editor-sheet-close]");
    await mp.waitForFunction(() => {
      const el = document.querySelector("[data-editor-sheet]");
      return !el || el.getClientRects().length === 0;
    }, null, { timeout: 5000 });
    await mOpenFile(mp, "use.go");
  });

  await run("mobile: a tap off a word raises no pill and leaves the selection alone", async () => {
    const mp = await mobilePage();
    const pt = await mp.evaluate(() => {
      const el = [...document.querySelectorAll(".cm-line")].find((l) => l.textContent.trim() === "");
      if (!el) return null;
      const r = el.getBoundingClientRect();
      return { x: r.x + 40, y: r.y + r.height / 2 };
    });
    assert(pt, "an empty line on the surface");
    await tapAt(mp, pt);
    await sleep(500);
    assert(!(await pillShowing(mp)), "no pill off a word");
    assert(await mp.evaluate(() => document.getSelection().isCollapsed), "no selection from a tap");
  });

  await run("mobile: the pill leaves with typing and when the cursor moves off", async () => {
    const mp = await mobilePage();
    const pt = await pressPoint(mp, "IntelTarget(", "IntelTarget");
    await tapAt(mp, pt);
    await mp.waitForSelector("[data-editor-lsp-pill]", { state: "visible", timeout: 8000 });
    await mp.keyboard.type("x");
    await mp.waitForSelector("[data-editor-lsp-pill]", { state: "detached", timeout: 4000 });
    await mp.keyboard.press("Backspace");
    await sleep(300);
    assert(!(await pillShowing(mp)), "typing keeps the pill away");
    await tapAt(mp, pt);
    await mp.waitForSelector("[data-editor-lsp-pill]", { state: "visible", timeout: 8000 });
    const off = await mp.evaluate(() => {
      const el = [...document.querySelectorAll(".cm-line")].find((l) => l.textContent.trim() === "");
      const r = el.getBoundingClientRect();
      return { x: r.x + 40, y: r.y + r.height / 2 };
    });
    await tapAt(mp, off);
    await mp.waitForSelector("[data-editor-lsp-pill]", { state: "detached", timeout: 4000 });
  });

  await run("mobile: the pill never covers the tapped line, first line included", async () => {
    const mp = await mobilePage();
    const pt = await pressPoint(mp, "package lib", "package");
    await tapAt(mp, pt);
    await mp.waitForSelector("[data-editor-lsp-pill]", { state: "visible", timeout: 8000 });
    const boxes = await mp.evaluate(() => {
      const pill = document.querySelector("[data-editor-lsp-pill]").getBoundingClientRect();
      const line = [...document.querySelectorAll(".cm-line")].find((l) => l.textContent.includes("package lib")).getBoundingClientRect();
      return { pillTop: pill.top, pillBottom: pill.bottom, lineTop: line.top, lineBottom: line.bottom };
    });
    const overlap = boxes.pillTop < boxes.lineBottom && boxes.pillBottom > boxes.lineTop;
    assert(!overlap, `the pill covers the line: ${JSON.stringify(boxes)}`);
  });

  await run("desktop: a mouse click on a symbol raises no pill", async () => {
    await openFile("use.go");
    const pt = await wordPoint("IntelTarget(", "IntelTarget");
    assert(pt, "word on the desktop surface");
    await page.mouse.click(pt.x, pt.y);
    await sleep(500);
    assert(!(await page.$("[data-editor-lsp-pill]")), "no pill for the mouse");
  });

  // The settings round toggles instance-wide state, so it runs after every
  // other check and puts the switch back before the cleanup.
  await run("settings: the select offers Automatic, Docker and Off, and the pick persists", async () => {
    await page.goto(`${BASE}/settings/editor/lsp`, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    const selects = await page.$$eval("#settings-editor-lsp select", (els) => els.map((sel) => ({
      name: sel.name,
      options: [...sel.options].map((o) => o.value).join(","),
    })));
    const byName = Object.fromEntries(selects.map((sel) => [sel.name, sel.options]));
    assert(byName.server_go === "auto,gopls-docker,off", `go select options, got ${byName.server_go}`);
    assert(byName.server_php === "auto,intelephense-docker,off", `php select options, got ${byName.server_php}`);
    assert(byName.server_typescript === "auto,tsgo-docker,off", `typescript select options, got ${byName.server_typescript}`);
    await page.selectOption('select[name="server_go"]', "auto");
    await saveLSPSettings();
    await page.reload({ waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    const auto = await page.$eval('select[name="server_go"]', (el) => el.value);
    assert(auto === "auto", `the Automatic pick survives the save, got ${auto}`);
    await page.selectOption('select[name="server_go"]', "gopls-docker");
    await saveLSPSettings();
    await page.reload({ waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    const picked = await page.$eval('select[name="server_go"]', (el) => el.value);
    assert(picked === "gopls-docker", `the Docker pick survives the save, got ${picked}`);
  });

  await run("settings: a language switched off loses its whole surface", async () => {
    await page.goto(`${BASE}/settings/editor/lsp`, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    await page.selectOption('select[name="server_go"]', "off");
    await saveLSPSettings();

    await page.goto(base, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    await page.waitForSelector('.editor-item[data-path="use.go"]', { timeout: 10000 });
    await openFile("use.go");
    await page.waitForFunction(() => [...document.querySelectorAll(".cm-line")].some((l) => l.textContent.includes("IntelTarget(")), null, { timeout: 10000 });
    const attr = await page.evaluate(() => document.querySelector("dc-editor").dataset.editorLsp || "");
    assert(!attr.includes("go:"), `go absent from the surface, got "${attr}"`);
    assert(attr.includes("php:PHP"), `php still offered, got "${attr}"`);

    await page.goto(`${BASE}/settings/editor/lsp`, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    await page.selectOption('select[name="server_go"]', "gopls-docker");
    await saveLSPSettings();
    await page.goto(base, { waitUntil: "domcontentloaded" });
    await page.waitForFunction(() => (document.querySelector("dc-editor")?.dataset.editorLsp || "").includes("go:Go"), null, { timeout: 8000 });
  });

  // The fake docker never finds an image and sleeps in its build, so the
  // preparing phase is visible without a daemon.
  await run("the image build shows as preparing and flows into the indexing", async () => {
    const p3 = `zzlsp3-${tag}`;
    const base3 = `${BASE}/projects/${p3}/editor`;
    await L.createProject(page, p3);
    for (const [path, content] of [
      ["go.mod", "module zzlsp3\n"],
      [".fake-lsp-slow", "pct\n"],
      ["lib.go", "package lib\n\nfunc IntelTarget() {}\n"],
    ]) {
      await postTo(base3, "/create", { path });
      await postTo(base3, "/file", { path, content });
    }
    await page.goto(base3, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    await page.waitForFunction(() => {
      const el = document.querySelector("[data-editor-lsp-index]");
      return el && !el.hidden && el.textContent.includes("Preparing Go");
    }, null, { timeout: 15000 });
    const bar = await page.evaluate(() => document.querySelector("[data-editor-lsp-index-bar]").classList.contains("progress-bar-indeterminate"));
    assert(bar, "the build phase draws the indeterminate bar");
    await page.waitForFunction(() => {
      const el = document.querySelector("[data-editor-lsp-index]");
      return el && !el.hidden && el.textContent.includes("Indexing Go");
    }, null, { timeout: 20000 });
    await page.waitForFunction(() => document.querySelector("[data-editor-lsp-index]").hidden, null, { timeout: 20000 });
    await L.deleteProject(page, p3);
  });

  // ---- lifetime and the indexing indicator ----------------------------------

  // .fake-lsp-slow makes the fake index slowly; the .fake-lsp-starts
  // ledger proves how many servers ever started.
  await run("indexing starts with the page, shows progress, and a reload reconnects warm", async () => {
    const p2 = `zzlsp2-${tag}`;
    const base2 = `${BASE}/projects/${p2}/editor`;
    await L.createProject(page, p2);
    for (const [path, content] of [
      [".fake-lsp-slow", "pct\n"],
      ["lib.go", "package lib\n\nfunc IntelTarget() {}\n"],
      ["use.go", "package lib\n\nfunc use() {\n\tIntelTarget()\n\tIntelTarget()\n}\n"],
    ]) {
      await postTo(base2, "/create", { path });
      await postTo(base2, "/file", { path, content });
    }
    await page.goto(base2, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    await page.waitForFunction(() => {
      const el = document.querySelector("[data-editor-lsp-index]");
      return el && !el.hidden && el.textContent.includes("Indexing Go");
    }, null, { timeout: 20000 });
    const state = await page.evaluate(() => ({
      indeterminate: document.querySelector("[data-editor-lsp-index-bar]").classList.contains("progress-bar-indeterminate"),
    }));
    assert(!state.indeterminate, "percentage reports draw a determinate bar");
    await page.waitForFunction(() => {
      const bar = document.querySelector("[data-editor-lsp-index-bar]");
      return parseFloat(bar.style.width || "0") >= 20;
    }, null, { timeout: 10000 });
    await page.waitForFunction(() => document.querySelector("[data-editor-lsp-index]").hidden, null, { timeout: 15000 });

    // Complete usages right after the announced indexing ended.
    await page.waitForSelector('.editor-item[data-path="use.go"]', { timeout: 10000 });
    await openFile("use.go");
    await page.waitForFunction(() => [...document.querySelectorAll(".cm-line")].some((l) => l.textContent.includes("IntelTarget(")), null, { timeout: 10000 });
    let pt = await wordPoint("IntelTarget(", "IntelTarget");
    await page.mouse.click(pt.x, pt.y);
    await page.keyboard.press("Shift+F12");
    await page.waitForFunction(() => !document.querySelector("[data-editor-quickopen]").hidden
      && document.querySelectorAll(".editor-quickopen-item").length >= 3, null, { timeout: 20000 });
    await page.keyboard.press("Escape");

    // The reload reconnects: the ledger still holds one start.
    await page.goto(base2, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    await sleep(2500);
    assert(await page.evaluate(() => document.querySelector("[data-editor-lsp-index]").hidden), "no indexing indicator after a reload onto the warm server");
    await openFile("use.go");
    await page.waitForFunction(() => [...document.querySelectorAll(".cm-line")].some((l) => l.textContent.includes("IntelTarget(")), null, { timeout: 10000 });
    pt = await wordPoint("IntelTarget(", "IntelTarget");
    await page.mouse.click(pt.x, pt.y);
    await page.keyboard.press("Shift+F12");
    await page.waitForFunction(() => !document.querySelector("[data-editor-quickopen]").hidden
      && document.querySelectorAll(".editor-quickopen-item").length >= 3, null, { timeout: 8000 });
    await page.keyboard.press("Escape");
    const ledger = await page.evaluate(async (url) => (await fetch(url, { credentials: "same-origin" })).json(), `${base2}/file?path=${encodeURIComponent(".fake-lsp-starts")}`);
    assert(ledger.content === "start\n", `one server start across the reload, got ${JSON.stringify(ledger.content)}`);
  });

  // Reindex stops the project's servers gracefully and warms them fresh.
  await run("reindex from the menu restarts the server and shows the indexing", async () => {
    const p2 = `zzlsp2-${tag}`;
    const base2 = `${BASE}/projects/${p2}/editor`;
    await page.click("[data-editor-menu]");
    await page.waitForSelector("[data-editor-reindex-item]:not([hidden])", { state: "visible", timeout: 4000 });
    await page.click("[data-editor-reindex-item]");
    await page.waitForFunction(() => {
      const el = document.querySelector("[data-editor-lsp-index]");
      return el && !el.hidden && el.textContent.includes("Indexing Go");
    }, null, { timeout: 15000 });
    await page.waitForFunction(() => document.querySelector("[data-editor-lsp-index]").hidden, null, { timeout: 20000 });
    const pt = await wordPoint("IntelTarget(", "IntelTarget");
    assert(pt, "the symbol still on the surface");
    await page.mouse.click(pt.x, pt.y);
    await page.keyboard.press("Shift+F12");
    await page.waitForFunction(() => !document.querySelector("[data-editor-quickopen]").hidden
      && document.querySelectorAll(".editor-quickopen-item").length >= 3, null, { timeout: 20000 });
    await page.keyboard.press("Escape");
    await page.waitForFunction(() => document.querySelector("[data-editor-quickopen]").hidden, null, { timeout: 5000 });
    const ledger = await page.evaluate(async (url) => (await fetch(url, { credentials: "same-origin" })).json(), `${base2}/file?path=${encodeURIComponent(".fake-lsp-starts")}`);
    assert(ledger.content === "start\nstart\n", `the reindex started a fresh server, got ${JSON.stringify(ledger.content)}`);
  });

  // Exit code contract: .fake-lsp-restart makes the fake exit 64, the
  // cockpit restarts the server without error or backoff.
  await run("a workspace change restarts the server over the exit code contract", async () => {
    const p2 = `zzlsp2-${tag}`;
    const base2 = `${BASE}/projects/${p2}/editor`;
    await postTo(base2, "/create", { path: ".fake-lsp-restart" });
    await postTo(base2, "/file", { path: ".fake-lsp-restart", content: "go\n" });
    await page.waitForFunction(() => {
      const el = document.querySelector("[data-editor-lsp-index]");
      return el && !el.hidden;
    }, null, { timeout: 25000 });
    await page.waitForFunction(() => document.querySelector("[data-editor-lsp-index]").hidden, null, { timeout: 30000 });
    const pt = await wordPoint("IntelTarget(", "IntelTarget");
    assert(pt, "the symbol still on the surface");
    await page.mouse.click(pt.x, pt.y);
    await page.keyboard.press("Shift+F12");
    await page.waitForFunction(() => !document.querySelector("[data-editor-quickopen]").hidden
      && document.querySelectorAll(".editor-quickopen-item").length >= 3, null, { timeout: 20000 });
    await page.keyboard.press("Escape");
    await page.waitForFunction(() => document.querySelector("[data-editor-quickopen]").hidden, null, { timeout: 5000 });
    const ledger = await page.evaluate(async (url) => (await fetch(url, { credentials: "same-origin" })).json(), `${base2}/file?path=${encodeURIComponent(".fake-lsp-starts")}`);
    assert(ledger.content === "start\nstart\nstart\n", `the change started a fresh server, got ${JSON.stringify(ledger.content)}`);
    await L.deleteProject(page, p2);
  });

  await run("a server without percentage reports draws the indeterminate indicator", async () => {
    // Its own name: the build phase check deletes zzlsp3 asynchronously.
    const p3 = `zzlsp4-${tag}`;
    const base3 = `${BASE}/projects/${p3}/editor`;
    await L.createProject(page, p3);
    for (const [path, content] of [
      [".fake-lsp-slow", "nopct\n"],
      ["lib.go", "package lib\n\nfunc IntelTarget() {}\n"],
    ]) {
      await postTo(base3, "/create", { path });
      await postTo(base3, "/file", { path, content });
    }
    await page.goto(base3, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    await page.waitForFunction(() => {
      const el = document.querySelector("[data-editor-lsp-index]");
      return el && !el.hidden;
    }, null, { timeout: 10000 });
    const indeterminate = await page.evaluate(() => document.querySelector("[data-editor-lsp-index-bar]").classList.contains("progress-bar-indeterminate"));
    assert(indeterminate, "no percentage means the indeterminate bar");
    await page.waitForFunction(() => document.querySelector("[data-editor-lsp-index]").hidden, null, { timeout: 15000 });
    await L.deleteProject(page, p3);
  });

  await run("cleanup", async () => {
    await L.deleteProject(page, project);
  });
});
