const L = require("./lib");
const { assert, sleep, confirmSwal, BASE } = L;

// Editor: the per-project file editor. Custom element dc-editor; CodeMirror 6 loads
// through the layout import map (jsDelivr CDN), language packs are dynamic-imported
// by extension; shell, Dockerfile and TOML have no lezer grammar and come from
// the legacy stream modes instead. Tree rows carry a per type icon, and a row
// dragged onto a folder row moves the file or folder there (POST /editor/move,
// the drop highlight always sits on the target folder, the tree box stands in
// for the project root, a pill names the destination, the tree scrolls while the
// pointer rests on its top or bottom edge); open tabs follow the new path. Video
// and audio files open in a player instead of a download button, fed by the raw
// endpoint, which serves those types inline so range requests (seeking) work.
// A move, a copy or an upload onto a name that is already taken answers 409 and
// the browser asks once before it replaces the file. The tree menu also copies
// and pastes (clipboard in this browser only, a paste into the source folder
// makes a "name copy"), packs a folder into a tar.gz (GET /editor/archive,
// excluded from the gzip middleware, it would compress the archive twice) and
// unpacks tar, tar.gz and zip files into a fresh folder next to them. A drag
// resting on a closed folder opens it after 600ms. Closing a folder folds its
// descendants and drops them from the saved set, so a reload keeps them closed.
// The tab menu carries the same file actions as a tree row (copy, extract).
// A menu opened by a long press swallows that finger's lift (and the click it
// may synthesize), so the entry that lands under the resting finger is not
// picked; the next tap works normally.
// Whole folders upload too: the
// picker input carries webkitdirectory, a drop walks the directory entries via
// webkitGetAsEntry (capped at 1000 files, an archive is the route beyond that),
// and each file carries its path inside the folder so the server makes the
// folders on the way (dirs=1). Every rebuild of the tree puts
// the scroll position back, waiting for the lazily loaded folders first. The
// drags in here are mouse driven on purpose: a synthetic DragEvent skips the
// browser's drag machinery and would have missed that the shared row-menu
// wiring cancelled every dragstart. A bare .editor-textarea means the CDN import map failed (highlight
// failure). Open files are tabs (.editor-tab, per-tab undo history, dirty dot,
// persisted per project in localStorage and restored on load); switching tabs never
// asks to discard, only closing a dirty tab does. The tree is a drawer on small
// screens (auto-open when no tab restores) and a drag-resizable column on wide
// ones. Routes: GET /projects/:name/editor(/list|/file|/files), POST .../file
// (save), .../create, .../mkdir, .../delete, .../rename, .../upload, .../preview.
// The toolbar buttons are wired only after init() awaits the CDN, so wait for
// .cm-editor before driving them; kebab menu items are clicked via evaluate so the
// bootstrap dropdown does not need to be opened first. Tabs and tree rows carry a
// context menu (@dc/contextmenu, body-mounted .dc-context-menu): right click on
// fine pointers, long-press on touch, and on tabs also a tap on the already active
// tab. Tab entries are the close variants, copy path, download, reveal in tree,
// rename and delete. Tree entries are new file/new folder/upload (right click
// selects the row, so they target the row's dir), copy path, download on files,
// rename and delete; the empty tree area clears the selection and targets the
// project root, plus a refresh entry. The menu is the only create/upload path:
// the per-row hover pencil/trash buttons are gone and the tree header keeps just
// the refresh button (drag-drop upload still works).

L.runFeature("EDITOR", async ({ engine, browser, page, run, mobilePage, bag }) => {
  const tag = `edit-${Date.now().toString(36)}`;
  const project = `zztc-${tag}`;
  const projectB = `zztc-a-${tag}`;
  const editorURL = `${BASE}/projects/${encodeURIComponent(project)}/editor`;
  const noteFile = `note_${tag}.md`;
  const qoFile = `qo_${tag}.txt`;
  let lastDialog = null;
  page.on("dialog", async (d) => { try { if (d.type() !== "beforeunload") lastDialog = d.message(); await d.accept(); } catch {} });

  const tabSel = (path) => `.editor-tab[data-path="${path}"]`;
  // Presses the source row, moves off it, hovers the target and releases, so the
  // browser runs its own drag machinery. Returns what the tree marked while the
  // pointer sat on the target.
  const dragRow = async (pg, fromSel, toSel, opts = {}) => {
    await pg.locator(fromSel).first().scrollIntoViewIfNeeded();
    await sleep(150);
    const from = await pg.locator(fromSel).first().boundingBox();
    const to = toSel ? await pg.locator(toSel).first().boundingBox() : opts.point;
    assert(from && to, `missing boxes for ${fromSel} -> ${toSel}`);
    await pg.mouse.move(from.x + 40, from.y + from.height / 2);
    await pg.mouse.down();
    await pg.mouse.move(from.x + 55, from.y + from.height / 2, { steps: 3 });
    await pg.mouse.move(toSel ? to.x + Math.min(60, to.width / 2) : to.x, toSel ? to.y + to.height / 2 : to.y, { steps: 14 });
    await sleep(250);
    const state = await pg.evaluate(() => ({
      highlight: document.querySelector(".editor-item.editor-drop")?.dataset.path
        ?? (document.querySelector(".editor-tree.editor-drop") ? "(tree)" : null),
      hint: document.querySelector("[data-editor-drop-hint]:not([hidden])")?.textContent ?? null,
    }));
    await pg.mouse.up();
    await sleep(800);
    return state;
  };
  const clickItem = (sel) => page.evaluate((s) => document.querySelector(s).click(), sel);
  // Wait out the Swal close: on WebKit the dialog restores focus to the
  // pre-dialog element asynchronously, even past the container detach, stealing
  // the focus the editor set on the new tab. Typing right away could then land
  // outside the buffer (with the old toolbar button as opener it reopened the
  // create dialog). The settle sleep outlives that restore, the caller's
  // .cm-content click after newFile then lands on a stable focus.
  const treeRootMenu = async () => {
    const box = await page.locator("[data-editor-tree]").boundingBox();
    await page.mouse.click(box.x + box.width / 2, box.y + box.height - 12, { button: "right" });
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
  };
  const newFile = async (name) => {
    await treeRootMenu();
    await menuItem(page, "New file").click();
    await page.waitForSelector(".swal2-input", { state: "visible", timeout: 4000 });
    await page.fill(".swal2-input", name); await page.click(".swal2-confirm");
    await page.waitForSelector(tabSel(name), { timeout: 8000 });
    await page.waitForSelector(".swal2-container", { state: "detached", timeout: 4000 }).catch(() => {});
    await sleep(800);
  };
  const waitDirty = (path, on) =>
    page.waitForFunction(([sel, want]) => {
      const el = document.querySelector(sel);
      return !!el && el.classList.contains("dirty") === want;
    }, [tabSel(path), on], { timeout: 6000 });
  const menuItem = (p, label) => p.locator(".dc-context-menu .dropdown-item", { hasText: new RegExp(`^${label}$`) });
  const openRowMenu = async (p, sel) => {
    await p.click(sel, { button: "right" });
    await p.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
  };

  try {
    await L.createProject(page, project);

    await run("mounts dc-editor + tree loads + CodeMirror ready", async () => {
      await page.goto(editorURL, { waitUntil: "domcontentloaded" });
      assert((await L.waitUpgraded(page, ["dc-editor"], 8000)).length === 0, "dc-editor not upgraded");
      await page.waitForSelector("[data-editor-tree]", { timeout: 8000 });
      await page.waitForSelector(".cm-editor", { state: "attached", timeout: 12000 });
      await page.waitForFunction(() => { const t = document.querySelector("[data-editor-tree]"); return t && !/Loading/.test(t.textContent); }, null, { timeout: 8000 });
    });

    await run("the header back button follows the ?return target like the create forms' Cancel", async () => {
      const backSel = '.page-header a[title="Back"]';
      let href = await page.getAttribute(backSel, "href");
      assert(href === "/projects", `default back href '${href}'`);
      const ret = `/projects#project-${project}`;
      await page.goto(`${editorURL}?return=${encodeURIComponent(ret)}`, { waitUntil: "domcontentloaded" });
      href = await page.getAttribute(backSel, "href");
      assert(href === ret, `back href '${href}' != '${ret}'`);
      await page.click(backSel);
      await page.waitForFunction((r) => window.location.pathname + window.location.hash === r, ret, { timeout: 8000 });
      await page.waitForFunction((p) => {
        const card = document.getElementById(`project-${p}`);
        if (!card) return false;
        const rect = card.getBoundingClientRect();
        return rect.top >= 0 && rect.top < window.innerHeight;
      }, project, { timeout: 4000 });
      await page.goto(editorURL, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(".cm-editor", { state: "attached", timeout: 12000 });
      await page.waitForFunction(() => { const t = document.querySelector("[data-editor-tree]"); return t && !/Loading/.test(t.textContent); }, null, { timeout: 8000 });
    });

    await run("new file -> tree row + tab open + CodeMirror (not textarea fallback)", async () => {
      await newFile(noteFile);
      await page.waitForSelector(`.editor-file[data-path="${noteFile}"]`, { timeout: 8000 });
      assert(!(await page.$(".editor-textarea")), "CodeMirror fell back to textarea (CDN import map failed)");
      assert(await page.$(`${tabSel(noteFile)}.active`), "new file did not open as the active tab");
    });

    await run("edit -> dirty dot on tab -> save clears it", async () => {
      await page.click(".cm-content"); await page.keyboard.type("hello " + tag);
      await waitDirty(noteFile, true);
      await page.click("[data-editor-save]");
      await waitDirty(noteFile, false);
    });

    await run("Ctrl+S saves the dirty buffer", async () => {
      await page.click(".cm-content"); await page.keyboard.type("\nmore");
      await waitDirty(noteFile, true);
      await page.keyboard.press("Control+S");
      await waitDirty(noteFile, false);
    });

    await run("reverting to the saved content clears the dirty flag", async () => {
      await page.click(".cm-content");
      await page.keyboard.press("Control+End");
      await page.keyboard.type("x");
      await waitDirty(noteFile, true);
      await page.keyboard.press("Backspace");
      await waitDirty(noteFile, false);
      await page.keyboard.type("y");
      await waitDirty(noteFile, true);
      await page.keyboard.press("Control+z");
      await waitDirty(noteFile, false);
    });

    await run("statusbar shows the cursor position", async () => {
      const pos = await page.textContent("[data-editor-pos]");
      assert(/Ln \d+, Col \d+/.test(pos || ""), `unexpected position readout: ${pos}`);
    });

    await run("real language highlighting for a code file", async () => {
      await newFile("main.go");
      await page.click(".cm-content"); await page.keyboard.type('package main\n\nfunc main() {}\n');
      let spans = 0; for (let i = 0; i < 30; i++) { spans = await page.locator(".cm-editor .cm-content span").count(); if (spans > 0) break; await sleep(300); }
      assert(spans > 0, "no highlight spans (Go language pack did not load from the CDN)");
      return `${spans} token spans`;
    }, { soft: true });

    await run("switching tabs keeps the dirty buffer, no discard confirm", async () => {
      await waitDirty("main.go", true);
      lastDialog = null;
      await page.click(tabSel(noteFile));
      await page.waitForSelector(`${tabSel(noteFile)}.active`, { timeout: 6000 });
      assert(lastDialog === null, `unexpected dialog: ${lastDialog}`);
      const noteText = await page.textContent(".cm-content");
      assert(noteText.includes("hello " + tag), "note buffer not shown after switch");
      await page.click(tabSel("main.go"));
      await waitDirty("main.go", true);
      const goText = await page.textContent(".cm-content");
      assert(goText.includes("package main"), "go buffer lost after switching back");
      await page.click("[data-editor-save]");
      await waitDirty("main.go", false);
    });

    await run("CodeMirror theme follows the OS scheme on every open tab", async () => {
      // noteFile and main.go are both open. A stored per-tab EditorState must be
      // re-themed on switch, not just the active one (the fix for stale tabs).
      const cmDark = async () => page.$eval(".cm-editor", (el) => {
        const m = getComputedStyle(el).backgroundColor.match(/[\d.]+/g).map(Number);
        return !(m.length === 4 && m[3] === 0) && m[0] + m[1] + m[2] < 250;
      });
      await page.emulateMedia({ colorScheme: "dark" });
      await sleep(400);
      assert(await cmDark(), "active tab not dark after OS flip");
      await page.click(tabSel(noteFile));
      await page.waitForSelector(`${tabSel(noteFile)}.active`, { timeout: 6000 });
      await sleep(300);
      assert(await cmDark(), "switched tab kept the old light theme");
      await page.emulateMedia({ colorScheme: "light" });
      await sleep(300);
      await page.click(tabSel("main.go"));
      await page.waitForSelector(`${tabSel("main.go")}.active`, { timeout: 6000 });
      await sleep(300);
      assert(!(await cmDark()), "switched tab kept the old dark theme");
      await page.emulateMedia({ colorScheme: null });
      await sleep(200);
    });

    await run("quick open palette opens a closed file", async () => {
      await newFile(qoFile);
      await page.evaluate((s) => document.querySelector(`${s} .editor-tab-state`).click(), tabSel(qoFile));
      await page.waitForFunction((s) => !document.querySelector(s), tabSel(qoFile), { timeout: 6000 });
      await page.click(".cm-content");
      await page.keyboard.press("Control+O");
      await page.waitForSelector("[data-editor-quickopen]:not([hidden])", { timeout: 6000 });
      await page.fill("[data-editor-quickopen-input]", qoFile.slice(0, 8));
      await page.waitForSelector(".editor-quickopen-item", { timeout: 6000 });
      await page.keyboard.press("Enter");
      await page.waitForSelector(`${tabSel(qoFile)}.active`, { timeout: 8000 });
      // Double Shift opens the palette like Ctrl+O; a Shift chord must not.
      await page.click(".cm-content");
      await page.keyboard.press("Shift+A");
      await page.keyboard.press("Shift");
      await sleep(80);
      await page.keyboard.press("Shift");
      await page.waitForSelector("[data-editor-quickopen]:not([hidden])", { timeout: 6000 });
      await page.keyboard.press("Escape");
      await page.waitForSelector("[data-editor-quickopen][hidden]", { state: "attached", timeout: 6000 });
      await page.click(".cm-content");
      await page.keyboard.press("Backspace");
      await waitDirty(qoFile, false);
      await page.keyboard.press("Shift");
      await sleep(600);
      await page.keyboard.press("Shift");
      await sleep(300);
      assert(await page.$("[data-editor-quickopen][hidden]"), "two slow Shift taps outside the window opened the palette");
    });

    await run("find in files searches contents and jumps to the match", async () => {
      await page.click("[data-editor-search-project]");
      await page.waitForSelector("[data-editor-quickopen]:not([hidden])", { timeout: 6000 });
      await page.fill("[data-editor-quickopen-input]", "hello " + tag);
      await page.waitForSelector(".editor-quickopen-match", { timeout: 8000 });
      assert(await page.$(".editor-quickopen-match mark"), "match text not highlighted");
      const head = await page.textContent(".editor-quickopen-match .editor-quickopen-name");
      assert(head === `${noteFile}:1`, `unexpected match head: ${head}`);
      await page.keyboard.press("Enter");
      await page.waitForFunction(() => document.querySelector("[data-editor-quickopen]").hidden, null, { timeout: 6000 });
      await page.waitForSelector(`${tabSel(noteFile)}.active`, { timeout: 8000 });
      const pos = await page.textContent("[data-editor-pos]");
      assert(/^Ln 1, Col 1/.test(pos || ""), `cursor not on the match line: ${pos}`);
    });

    await run("open tabs, active tab and expanded tree dirs survive a reload", async () => {
      await treeRootMenu();
      await menuItem(page, "New folder").click();
      await page.waitForSelector(".swal2-input", { state: "visible", timeout: 4000 });
      await page.fill(".swal2-input", `keep_${tag}`);
      await page.click(".swal2-confirm");
      await page.waitForSelector(`.editor-dir[data-path="keep_${tag}"]`, { timeout: 8000 });
      await page.waitForSelector(".swal2-container", { state: "detached", timeout: 4000 }).catch(() => {});
      await page.click(`.editor-dir[data-path="keep_${tag}"]`);
      await sleep(300);
      await openRowMenu(page, `.editor-dir[data-path="keep_${tag}"]`);
      await menuItem(page, "New file").click();
      await page.waitForSelector(".swal2-input", { state: "visible", timeout: 4000 });
      await page.fill(".swal2-input", "inside.txt");
      await page.click(".swal2-confirm");
      await page.waitForSelector(`.editor-file[data-path="keep_${tag}/inside.txt"]`, { timeout: 8000 });
      await page.click(tabSel(noteFile));
      await sleep(300);
      await page.goto(editorURL, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(".cm-editor", { timeout: 12000 });
      await page.waitForSelector(`${tabSel(noteFile)}.active`, { timeout: 8000 });
      assert(await page.$(tabSel("main.go")), "main.go tab not restored");
      assert(await page.$(tabSel(qoFile)), "qo tab not restored");
      const text = await page.textContent(".cm-content");
      assert(text.includes("hello " + tag), "restored active buffer content missing");
      await page.waitForSelector(`.editor-file[data-path="keep_${tag}/inside.txt"]`, { timeout: 8000 });
      await page.click(`${tabSel(`keep_${tag}/inside.txt`)} .editor-tab-state`);
      await page.waitForFunction((p) => !document.querySelector(`.editor-tab[data-path="${p}"]`), `keep_${tag}/inside.txt`, { timeout: 6000 });
      await page.click(tabSel(noteFile));
      await sleep(200);
    });

    await run("closing a dirty tab asks to discard", async () => {
      await page.click(tabSel(qoFile));
      await page.click(".cm-content"); await page.keyboard.type("throwaway");
      await waitDirty(qoFile, true);
      await page.evaluate((s) => document.querySelector(`${s} .editor-tab-state`).click(), tabSel(qoFile));
      await page.waitForSelector(".swal2-popup", { timeout: 6000 });
      assert(/discard/i.test(await page.textContent(".swal2-popup")), "no discard wording in the confirm");
      await confirmSwal(page);
      await page.waitForFunction((s) => !document.querySelector(s), tabSel(qoFile), { timeout: 6000 });
    });

    await run("rename via the tree row's context menu updates the row and the open tab", async () => {
      const renamed = `renamed_${tag}.go`;
      await openRowMenu(page, '.editor-file[data-path="main.go"]');
      await menuItem(page, "Rename").click();
      await page.waitForSelector(".swal2-input", { state: "visible", timeout: 4000 });
      await page.fill(".swal2-input", renamed); await page.click(".swal2-confirm");
      await page.waitForSelector(`.editor-file[data-path="${renamed}"]`, { timeout: 8000 });
      await page.waitForSelector(tabSel(renamed), { timeout: 6000 });
      assert(!(await page.$(tabSel("main.go"))), "old tab path still present after rename");
    });

    await run("save all clears every dirty tab", async () => {
      const renamed = `renamed_${tag}.go`;
      await page.click(tabSel(renamed));
      await page.click(".cm-content"); await page.keyboard.type("\n// one");
      await waitDirty(renamed, true);
      await page.click(tabSel(noteFile));
      await page.click(".cm-content"); await page.keyboard.type("\nmore note");
      await waitDirty(noteFile, true);
      await clickItem("[data-editor-save-all]");
      await waitDirty(noteFile, false);
      await waitDirty(renamed, false);
    });

    await run("markdown preview renders server side and toggles off", async () => {
      await page.click(tabSel(noteFile));
      await page.click(".cm-content");
      await page.keyboard.press("Control+End").catch(() => {});
      await page.keyboard.type("\n\n# PreviewTitle" + tag);
      await page.waitForSelector("[data-editor-preview-toggle]:not([hidden])", { timeout: 6000 });
      await page.click("[data-editor-preview-toggle]");
      await page.waitForFunction((t) => {
        const pane = document.querySelector("[data-editor-preview-pane]");
        return pane && !pane.hidden && /PreviewTitle/.test(pane.textContent) && pane.querySelector("h1");
      }, tag, { timeout: 8000 });
      await page.click("[data-editor-preview-toggle]");
      await page.waitForFunction(() => document.querySelector("[data-editor-preview-pane]").hidden, null, { timeout: 6000 });
      await clickItem("[data-editor-save-all]");
      await waitDirty(noteFile, false);
    });

    await run("find in file opens the styled search panel", async () => {
      await page.click(tabSel(noteFile));
      await page.click("[data-editor-find]");
      await page.waitForSelector(".cm-panel.cm-search", { timeout: 6000 });
      await page.keyboard.press("Escape");
      await page.waitForFunction(() => !document.querySelector(".cm-panel.cm-search"), null, { timeout: 6000 });
      // The explicit Ctrl-f binding, on this platform it doubles CodeMirror's own Mod-f.
      await page.click(".cm-content");
      await page.keyboard.press("Control+f");
      await page.waitForSelector(".cm-panel.cm-search", { timeout: 6000 });
      await page.keyboard.press("Escape");
      await page.waitForFunction(() => !document.querySelector(".cm-panel.cm-search"), null, { timeout: 6000 });
    });

    // On a mac CodeMirror's Mod-f is Cmd-f and Ctrl-f defaults to the emacs
    // cursor step, the editor binds Ctrl-f to the search panel explicitly so
    // both work. The mac platform is emulated via CDP, so chromium only.
    await run("Ctrl+F opens the find panel on a mac platform too", async () => {
      if (engine !== "chromium") return;
      const ctx = await browser.newContext({ ignoreHTTPSErrors: true });
      try {
        const p2 = await ctx.newPage();
        const cdp = await ctx.newCDPSession(p2);
        await cdp.send("Emulation.setUserAgentOverride", {
          userAgent: await p2.evaluate(() => navigator.userAgent),
          platform: "MacIntel",
        });
        await L.login(p2);
        await p2.goto(editorURL, { waitUntil: "domcontentloaded" });
        await p2.waitForSelector(".cm-editor", { state: "attached", timeout: 12000 });
        await p2.waitForFunction(() => { const t = document.querySelector("[data-editor-tree]"); return t && !/Loading/.test(t.textContent); }, null, { timeout: 8000 });
        assert((await p2.evaluate(() => navigator.platform)) === "MacIntel", "platform override did not stick");
        await p2.click(`.editor-file[data-path="${noteFile}"]`);
        await p2.waitForSelector(tabSel(noteFile), { timeout: 8000 });
        await p2.click(".cm-content");
        await p2.keyboard.press("Control+f");
        await p2.waitForSelector(".cm-panel.cm-search", { timeout: 6000 });
      } finally {
        await ctx.close();
      }
    });

    await run("upload via the file picker confirms the target dir and shows progress", async () => {
      const uploaded = `upload_${tag}.txt`;
      await page.setInputFiles("[data-editor-upload-input]", { name: uploaded, mimeType: "text/plain", buffer: Buffer.from("uploaded " + tag) });
      await page.waitForSelector(".editor-upload-list", { timeout: 6000 });
      assert((await page.textContent(".swal2-html-container")).includes("project root"), "upload dialog does not name the target dir");
      await page.waitForSelector(".editor-upload-item .progress", { timeout: 4000 });
      await page.click(".swal2-confirm");
      await page.waitForSelector(`.editor-file[data-path="${uploaded}"]`, { timeout: 8000 });
      await page.waitForSelector(".swal2-container", { state: "detached", timeout: 4000 });
    });

    await run("image opens in a viewer, svg gets a rendered preview, raw download works", async () => {
      const png = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==", "base64");
      const svg = Buffer.from('<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><rect width="10" height="10" fill="#f00"/></svg>');
      await page.setInputFiles("[data-editor-upload-input]", [
        { name: `pix_${tag}.png`, mimeType: "image/png", buffer: png },
        { name: `pic_${tag}.svg`, mimeType: "image/svg+xml", buffer: svg },
      ]);
      await page.waitForSelector(".editor-upload-list", { timeout: 6000 });
      await page.click(".swal2-confirm");
      await page.waitForSelector(".swal2-container", { state: "detached", timeout: 6000 });
      await page.click(`.editor-file[data-path="pix_${tag}.png"] .editor-item-name`);
      await page.waitForSelector("[data-editor-viewer] img.editor-viewer-image", { timeout: 8000 });
      assert(await page.evaluate(() => {
        const img = document.querySelector("[data-editor-viewer] img");
        return img && img.complete && img.naturalWidth === 1;
      }), "viewer image did not load");
      assert(await page.evaluate(() => !document.querySelector("[data-editor-download]").disabled), "download item disabled for image tab");
      const raw = await page.evaluate(async (p) => {
        const res = await fetch(location.pathname.replace(/\/editor$/, "/editor/raw") + `?path=${encodeURIComponent(p)}&download=1`);
        return { status: res.status, disposition: res.headers.get("Content-Disposition") || "" };
      }, `pix_${tag}.png`);
      assert(raw.status === 200 && raw.disposition.includes("attachment"), `raw download wrong: ${JSON.stringify(raw)}`);
      await page.click(`.editor-file[data-path="pic_${tag}.svg"] .editor-item-name`);
      await page.waitForSelector(tabSel(`pic_${tag}.svg`), { timeout: 6000 });
      await page.waitForSelector("[data-editor-preview-toggle]:not([hidden])", { timeout: 6000 });
      await page.click("[data-editor-preview-toggle]");
      await page.waitForSelector("[data-editor-preview-pane]:not([hidden]) img", { state: "attached", timeout: 6000 });
      await page.click("[data-editor-preview-toggle]");
      await clickItem("[data-editor-delete]");
      await confirmSwal(page);
      await page.waitForFunction((p) => !document.querySelector(`.editor-file[data-path="${p}"]`), `pic_${tag}.svg`, { timeout: 8000 });
      await page.click(tabSel(`pix_${tag}.png`) + " .editor-tab-state");
      await page.waitForFunction((p) => !document.querySelector(`.editor-tab[data-path="${p}"]`), `pix_${tag}.png`, { timeout: 6000 });
    });

    await run("settings persist to localStorage", async () => {
      assert(await page.evaluate(() => { const el = document.querySelector('[data-editor-setting="font_size"]'); if (!el) return false; el.value = "18"; el.dispatchEvent(new Event("change", { bubbles: true })); return true; }), "no font_size control");
      await sleep(250);
      assert(String((await page.evaluate(() => JSON.parse(localStorage.getItem("dc-editor-settings") || "{}"))).font_size) === "18", "font_size not persisted");
    });

    await run("new folder + recursive delete drops descendants and their tabs", async () => {
      await treeRootMenu();
      await menuItem(page, "New folder").click();
      await page.waitForSelector(".swal2-input", { state: "visible", timeout: 4000 });
      await page.fill(".swal2-input", "sub"); await page.click(".swal2-confirm");
      await page.waitForSelector('.editor-dir[data-path="sub"]', { timeout: 8000 });
      await page.waitForSelector(".swal2-container", { state: "detached", timeout: 4000 }).catch(() => {});
      await page.click('.editor-dir[data-path="sub"]'); await sleep(300);
      await openRowMenu(page, '.editor-dir[data-path="sub"]');
      await menuItem(page, "New file").click();
      await page.waitForSelector(".swal2-input", { state: "visible", timeout: 4000 });
      await page.fill(".swal2-input", "inner.txt"); await page.click(".swal2-confirm");
      await page.waitForSelector('.editor-file[data-path="sub/inner.txt"]', { timeout: 8000 });
      await page.waitForSelector(tabSel("sub/inner.txt"), { timeout: 6000 });
      await page.locator(".editor-dir", { has: page.locator(".editor-item-name", { hasText: /^sub$/ }) }).first().click({ button: "right" });
      await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
      await menuItem(page, "Delete").click();
      await confirmSwal(page);
      await page.waitForFunction(() => ![...document.querySelectorAll(".editor-item-name")].some((e) => e.textContent === "sub") && !document.querySelector('.editor-file[data-path="sub/inner.txt"]'), null, { timeout: 8000 });
      assert(!(await page.$(tabSel("sub/inner.txt"))), "tab of the deleted folder's file still open");
    });

    await run("delete via the tree row's context menu drops it from the tree and closes its tab", async () => {
      const renamed = `renamed_${tag}.go`;
      await openRowMenu(page, `.editor-file[data-path="${renamed}"]`);
      await menuItem(page, "Delete").click();
      await confirmSwal(page);
      await page.waitForFunction((f) => !document.querySelector(`.editor-file[data-path="${f}"]`), renamed, { timeout: 8000 });
      assert(!(await page.$(tabSel(renamed))), "tab of the deleted file still open");
    });

    await run("right click on a tab opens the context menu, Escape closes it", async () => {
      await openRowMenu(page, tabSel(noteFile));
      const labels = await page.$$eval(".dc-context-menu .dropdown-item", (els) => els.map((e) => e.textContent.trim()));
      for (const want of ["Close", "Close others", "Close to the right", "Close all", "Copy path", "Download", "Reveal in tree", "Rename", "Delete"]) {
        assert(labels.includes(want), `menu misses '${want}': ${labels.join(", ")}`);
      }
      const disabled = await page.$$eval(".dc-context-menu .dropdown-item:disabled", (els) => els.map((e) => e.textContent.trim()));
      assert(disabled.includes("Close others") && disabled.includes("Close to the right"), `single tab must disable close-others/right: ${disabled.join(", ")}`);
      await page.keyboard.press("Escape");
      await page.waitForSelector(".dc-context-menu", { state: "detached", timeout: 4000 });
    });

    await run("context menu Copy path copies the tab path", async () => {
      await openRowMenu(page, tabSel(noteFile));
      await menuItem(page, "Copy path").click();
      await page.waitForSelector(".dc-context-menu", { state: "detached", timeout: 4000 });
      await page.waitForFunction(() => /Copied |Clipboard is not available/.test(document.querySelector("[data-editor-status]").textContent), null, { timeout: 4000 });
      if (engine === "chromium") {
        const text = await page.evaluate(() => navigator.clipboard.readText());
        assert(text === noteFile, `clipboard '${text}' != '${noteFile}'`);
      }
    });

    await run("context menu Reveal in tree expands the folder and selects the file", async () => {
      const inside = `keep_${tag}/inside.txt`;
      const rowVisible = () => page.$eval(`.editor-file[data-path="${inside}"]`, (e) => e.offsetParent !== null).catch(() => false);
      if (!(await rowVisible())) {
        await page.click(`.editor-dir[data-path="keep_${tag}"]`);
        await page.waitForSelector(`.editor-file[data-path="${inside}"]`, { timeout: 8000 });
      }
      await page.click(`.editor-file[data-path="${inside}"]`);
      await page.waitForSelector(`${tabSel(inside)}.active`, { timeout: 8000 });
      await page.click(`.editor-dir[data-path="keep_${tag}"]`);
      await page.waitForFunction((p) => {
        const row = document.querySelector(`.editor-file[data-path="${p}"]`);
        return !row || row.offsetParent === null;
      }, inside, { timeout: 6000 });
      await openRowMenu(page, tabSel(inside));
      await menuItem(page, "Reveal in tree").click();
      await page.waitForFunction((p) => {
        const row = document.querySelector(`.editor-file[data-path="${p}"]`);
        return !!row && row.offsetParent !== null && row.classList.contains("selected");
      }, inside, { timeout: 8000 });
      await openRowMenu(page, tabSel(inside));
      await menuItem(page, "Close").click();
      await page.waitForFunction((s) => !document.querySelector(s), tabSel(inside), { timeout: 6000 });
    });

    await run("tree context menu: dir menu creates inside the dir, file menu deletes, empty area targets the root", async () => {
      const dirSel = `.editor-dir[data-path="keep_${tag}"]`;
      const inmenu = `keep_${tag}/inmenu.txt`;
      await openRowMenu(page, dirSel);
      let labels = await page.$$eval(".dc-context-menu .dropdown-item", (els) => els.map((e) => e.textContent.trim()));
      for (const want of ["New file", "New folder", "Upload files", "Copy path", "Rename", "Delete"]) {
        assert(labels.includes(want), `dir menu misses '${want}': ${labels.join(", ")}`);
      }
      assert(!labels.includes("Download"), "dir menu offers Download");
      assert(await page.$eval(dirSel, (e) => e.classList.contains("selected")), "right-click did not select the dir row");
      await menuItem(page, "New file").click();
      await page.waitForSelector(".swal2-input", { state: "visible", timeout: 4000 });
      await page.fill(".swal2-input", "inmenu.txt");
      await page.click(".swal2-confirm");
      await page.waitForSelector(tabSel(inmenu), { timeout: 8000 });
      await page.waitForSelector(".swal2-container", { state: "detached", timeout: 4000 }).catch(() => {});
      await sleep(800);
      await openRowMenu(page, `.editor-file[data-path="${inmenu}"]`);
      labels = await page.$$eval(".dc-context-menu .dropdown-item", (els) => els.map((e) => e.textContent.trim()));
      assert(labels.includes("Download"), `file menu misses Download: ${labels.join(", ")}`);
      await menuItem(page, "Delete").click();
      await confirmSwal(page);
      await page.waitForFunction((p) => !document.querySelector(`.editor-file[data-path="${p}"]`), inmenu, { timeout: 8000 });
      assert(!(await page.$(tabSel(inmenu))), "tab of the menu-deleted file still open");
      await page.waitForSelector(".swal2-container", { state: "detached", timeout: 4000 }).catch(() => {});
      const box = await page.locator("[data-editor-tree]").boundingBox();
      await page.mouse.click(box.x + box.width / 2, box.y + box.height - 12, { button: "right" });
      await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
      labels = await page.$$eval(".dc-context-menu .dropdown-item", (els) => els.map((e) => e.textContent.trim()));
      for (const want of ["New file", "New folder", "Upload files", "Refresh"]) {
        assert(labels.includes(want), `empty-area menu misses '${want}': ${labels.join(", ")}`);
      }
      assert(!labels.includes("Rename"), "empty-area menu offers Rename");
      await menuItem(page, "New file").click();
      await page.waitForSelector(".swal2-input", { state: "visible", timeout: 4000 });
      await page.fill(".swal2-input", "rootmenu.txt");
      await page.click(".swal2-confirm");
      await page.waitForSelector(tabSel("rootmenu.txt"), { timeout: 8000 });
      await page.waitForSelector(".swal2-container", { state: "detached", timeout: 4000 }).catch(() => {});
      await sleep(800);
      await openRowMenu(page, '.editor-file[data-path="rootmenu.txt"]');
      await menuItem(page, "Delete").click();
      await confirmSwal(page);
      await page.waitForFunction(() => !document.querySelector('.editor-file[data-path="rootmenu.txt"]'), null, { timeout: 8000 });
      await page.waitForSelector(".swal2-container", { state: "detached", timeout: 4000 }).catch(() => {});
    });

    await run("Close to the right and Close others close the expected tabs", async () => {
      await newFile(`cm1_${tag}.txt`);
      await newFile(`cm2_${tag}.txt`);
      await openRowMenu(page, tabSel(`cm1_${tag}.txt`));
      await menuItem(page, "Close to the right").click();
      await page.waitForFunction((s) => !document.querySelector(s), tabSel(`cm2_${tag}.txt`), { timeout: 6000 });
      assert(await page.$(tabSel(noteFile)), "close-to-the-right also closed a tab on the left");
      await openRowMenu(page, tabSel(`cm1_${tag}.txt`));
      await menuItem(page, "Close others").click();
      await page.waitForFunction((s) => !document.querySelector(s), tabSel(noteFile), { timeout: 6000 });
      const open = await page.$$eval(".editor-tab", (els) => els.map((e) => e.dataset.path));
      assert(open.length === 1 && open[0] === `cm1_${tag}.txt`, `unexpected open tabs: ${open.join(", ")}`);
    });

    await run("Close all asks to discard a dirty tab and empties the strip", async () => {
      await page.click(".cm-content");
      await page.keyboard.type("dirty");
      await waitDirty(`cm1_${tag}.txt`, true);
      await openRowMenu(page, tabSel(`cm1_${tag}.txt`));
      await menuItem(page, "Close all").click();
      await page.waitForSelector(".swal2-popup", { timeout: 6000 });
      assert(/discard/i.test(await page.textContent(".swal2-popup")), "no discard wording in the close-all confirm");
      await confirmSwal(page);
      await page.waitForFunction(() => !document.querySelector(".editor-tab"), null, { timeout: 6000 });
      assert(await page.$eval("[data-editor-placeholder]", (e) => !e.hidden), "placeholder hidden with no tabs open");
    });

    await run("tree rows carry a per type icon and a shell file gets highlighted", async () => {
      await page.waitForSelector(".swal2-container", { state: "detached", timeout: 4000 }).catch(() => {});
      await page.evaluate(async ([project, files]) => {
        const token = document.querySelector('meta[name="csrf-token"]').content;
        for (const [path, content] of files) {
          await fetch(`/projects/${project}/editor/file`, {
            method: "POST",
            headers: { "X-CSRF-Token": token, "Content-Type": "application/x-www-form-urlencoded" },
            body: "path=" + encodeURIComponent(path) + "&content=" + encodeURIComponent(content),
          });
        }
      }, [project, [["icons.sh", "#!/bin/bash\nfor f in *; do echo \"$f\"; done\n"], ["icons.json", "{}\n"]]]);
      await page.click("[data-editor-refresh]");
      await page.waitForSelector('.editor-file[data-path="icons.sh"]', { timeout: 8000 });
      const icons = await page.evaluate(() => ({
        sh: document.querySelector('.editor-file[data-path="icons.sh"] .editor-item-icon').className,
        json: document.querySelector('.editor-file[data-path="icons.json"] .editor-item-icon').className,
      }));
      assert(/ti-terminal-2/.test(icons.sh), `shell icon: ${icons.sh}`);
      assert(/ti-json/.test(icons.json), `json icon: ${icons.json}`);
      // The shell mode comes from the legacy stream modes, so a token check also
      // proves that import path still resolves.
      await page.click('.editor-file[data-path="icons.sh"]');
      await page.waitForSelector(`${tabSel("icons.sh")}.active`, { timeout: 8000 });
      await page.waitForFunction(() => document.querySelectorAll(".cm-line span[class]").length > 0, null, { timeout: 10000 });
    });

    await run("dragging a file onto a folder moves it, the open tab follows", async () => {
      await treeRootMenu();
      await menuItem(page, "New folder").click();
      await page.waitForSelector(".swal2-input", { state: "visible", timeout: 4000 });
      await page.fill(".swal2-input", `moved_${tag}`); await page.click(".swal2-confirm");
      await page.waitForSelector(`.editor-dir[data-path="moved_${tag}"]`, { timeout: 8000 });
      await page.waitForSelector(".swal2-container", { state: "detached", timeout: 4000 }).catch(() => {});
      // A real mouse driven drag: a synthetic DragEvent would not have caught
      // that the shared row-menu wiring cancelled every dragstart.
      const st = await dragRow(page, '.editor-file[data-path="icons.sh"]', `.editor-dir[data-path="moved_${tag}"]`);
      assert(st.highlight === `moved_${tag}`, `the drop highlight sat on ${st.highlight}`);
      assert(/^Target → moved_/.test(st.hint || ""), `drop hint: ${st.hint}`);
      await page.waitForSelector(`.editor-file[data-path="moved_${tag}/icons.sh"]`, { timeout: 8000 });
      assert(!(await page.$('.editor-file[data-path="icons.sh"]')), "the file stayed at the old path");
      const tabPath = await page.$eval(".editor-tab.active", (e) => e.dataset.path);
      assert(tabPath === `moved_${tag}/icons.sh`, `the tab kept the old path: ${tabPath}`);
    });

    await run("a folder refuses to land in its own child, empty space moves to the root", async () => {
      await openRowMenu(page, `.editor-dir[data-path="moved_${tag}"]`);
      await menuItem(page, "New folder").click();
      await page.waitForSelector(".swal2-input", { state: "visible", timeout: 4000 });
      await page.fill(".swal2-input", "inner"); await page.click(".swal2-confirm");
      await page.waitForSelector(`.editor-dir[data-path="moved_${tag}/inner"]`, { timeout: 8000 });
      await page.waitForSelector(".swal2-container", { state: "detached", timeout: 4000 }).catch(() => {});
      const blocked = await dragRow(page, `.editor-dir[data-path="moved_${tag}"]`, `.editor-dir[data-path="moved_${tag}/inner"]`);
      assert(blocked.highlight === null && blocked.hint === null, `a forbidden target was marked: ${JSON.stringify(blocked)}`);
      assert(await page.$(`.editor-dir[data-path="moved_${tag}"]`), "the folder vanished into its own child");
      // Empty tree space stands in for the project root.
      const tree = await page.locator(".editor-tree").boundingBox();
      const root = await dragRow(page, `.editor-file[data-path="moved_${tag}/icons.sh"]`, null, {
        point: { x: tree.x + tree.width / 2, y: tree.y + tree.height - 40 },
      });
      assert(/^Target → project root$/.test(root.hint || ""), `root hint: ${root.hint}`);
      await page.waitForSelector('.editor-file[data-path="icons.sh"]', { timeout: 8000 });
      for (const path of ["icons.sh", "icons.json"]) {
        const sel = tabSel(path);
        if (await page.$(sel)) {
          await page.evaluate((s) => document.querySelector(`${s} .editor-tab-state`).click(), sel);
          await page.waitForFunction((s) => !document.querySelector(s), sel, { timeout: 6000 });
        }
      }
    });

    await run("a video file plays in the viewer instead of offering a download", async () => {
      // A one second clip, small enough to live in the runner.
      const TINY_MP4 = "AAAAIGZ0eXBpc29tAAACAGlzb21pc28yYXZjMW1wNDEAAANKbW9vdgAAAGxtdmhkAAAAAAAAAAAAAAAAAAAD6AAAA+gAAQAAAQAAAAAAAAAAAAAAAAEAAAAAAAAAAAAAAAAAAAABAAAAAAAAAAAAAAAAAABAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAgAAAnR0cmFrAAAAXHRraGQAAAADAAAAAAAAAAAAAAABAAAAAAAAA+gAAAAAAAAAAAAAAAAAAAAAAAEAAAAAAAAAAAAAAAAAAAABAAAAAAAAAAAAAAAAAABAAAAAAEAAAABAAAAAAAAkZWR0cwAAABxlbHN0AAAAAAAAAAEAAAPoAAAAAAABAAAAAAHsbWRpYQAAACBtZGhkAAAAAAAAAAAAAAAAAAAoAAAAKABVxAAAAAAALWhkbHIAAAAAAAAAAHZpZGUAAAAAAAAAAAAAAABWaWRlb0hhbmRsZXIAAAABl21pbmYAAAAUdm1oZAAAAAEAAAAAAAAAAAAAACRkaW5mAAAAHGRyZWYAAAAAAAAAAQAAAAx1cmwgAAAAAQAAAVdzdGJsAAAAt3N0c2QAAAAAAAAAAQAAAKdhdmMxAAAAAAAAAAEAAAAAAAAAAAAAAAAAAAAAAEAAQABIAAAASAAAAAAAAAABAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAGP//AAAALWF2Y0MBQsAK/+EAFmdCwAraEJsBEAAAAwAQAAADAUDxImoBAARozg/IAAAAEHBhc3AAAAABAAAAAQAAABRidHJ0AAAAAAAAFmAAABZgAAAAGHN0dHMAAAAAAAAAAQAAAAoAAAQAAAAAFHN0c3MAAAAAAAAAAQAAAAEAAAAcc3RzYwAAAAAAAAABAAAAAQAAAAoAAAABAAAAPHN0c3oAAAAAAAAAAAAAAAoAAAJyAAAACgAAAAoAAAAKAAAACgAAAAoAAAAKAAAACgAAAAoAAAAKAAAAFHN0Y28AAAAAAAAAAQAAA3oAAABidWR0YQAAAFptZXRhAAAAAAAAACFoZGxyAAAAAAAAAABtZGlyYXBwbAAAAAAAAAAAAAAAAC1pbHN0AAAAJal0b28AAAAdZGF0YQAAAAEAAAAATGF2ZjU4Ljc2LjEwMAAAAAhmcmVlAAAC1G1kYXQAAAJUBgX//1DcRem95tlIt5Ys2CDZI+7veDI2NCAtIGNvcmUgMTYzIHIzMDYwIDVkYjZhYTYgLSBILjI2NC9NUEVHLTQgQVZDIGNvZGVjIC0gQ29weWxlZnQgMjAwMy0yMDIxIC0gaHR0cDovL3d3dy52aWRlb2xhbi5vcmcveDI2NC5odG1sIC0gb3B0aW9uczogY2FiYWM9MCByZWY9MSBkZWJsb2NrPTA6MDowIGFuYWx5c2U9MDowIG1lPWRpYSBzdWJtZT0wIHBzeT0xIHBzeV9yZD0xLjAwOjAuMDAgbWl4ZWRfcmVmPTAgbWVfcmFuZ2U9MTYgY2hyb21hX21lPTEgdHJlbGxpcz0wIDh4OGRjdD0wIGNxbT0wIGRlYWR6b25lPTIxLDExIGZhc3RfcHNraXA9MSBjaHJvbWFfcXBfb2Zmc2V0PTAgdGhyZWFkcz0yIGxvb2thaGVhZF90aHJlYWRzPTEgc2xpY2VkX3RocmVhZHM9MCBucj0wIGRlY2ltYXRlPTEgaW50ZXJsYWNlZD0wIGJsdXJheV9jb21wYXQ9MCBjb25zdHJhaW5lZF9pbnRyYT0wIGJmcmFtZXM9MCB3ZWlnaHRwPTAga2V5aW50PTI1MCBrZXlpbnRfbWluPTEwIHNjZW5lY3V0PTAgaW50cmFfcmVmcmVzaD0wIHJjPWNyZiBtYnRyZWU9MCBjcmY9MjMuMCBxY29tcD0wLjYwIHFwbWluPTAgcXBtYXg9NjkgcXBzdGVwPTQgaXBfcmF0aW89MS40MCBhcT0wAIAAAAAWZYiEOiYoAAkCycnJ1111111111114AAAAAZBmiARoIwAAAAGQZpAEqCMAAAABkGaYBKgjAAAAAZBmoASoIwAAAAGQZqgEqCMAAAABkGawBKgjAAAAAZBmuASoIwAAAAGQZsAEqCMAAAABkGbIBKgjA==";
      await page.setInputFiles("[data-editor-upload-input]", [{
        name: `clip_${tag}.mp4`,
        mimeType: "video/mp4",
        buffer: Buffer.from(TINY_MP4, "base64"),
      }]);
      await page.waitForSelector(".editor-upload-list", { timeout: 6000 });
      await page.click(".swal2-confirm");
      await page.waitForSelector(".swal2-container", { state: "detached", timeout: 6000 });
      await page.waitForSelector(`.editor-file[data-path="clip_${tag}.mp4"]`, { timeout: 10000 });
      await page.click(`.editor-file[data-path="clip_${tag}.mp4"] .editor-item-name`);
      await page.waitForSelector("video.editor-viewer-video", { timeout: 8000 });
      const state = await page.evaluate(async () => {
        const v = document.querySelector("video.editor-viewer-video");
        await new Promise((r) => (v.readyState > 0 ? r() : v.addEventListener("loadedmetadata", r, { once: true })));
        return { duration: v.duration, width: v.videoWidth, controls: v.controls };
      });
      assert(state.width > 0 && state.duration > 0, `the browser could not read the clip: ${JSON.stringify(state)}`);
      assert(state.controls, "the player carries no controls");
      // Seeking needs the raw endpoint to answer ranges inline, not as a download.
      const raw = await page.evaluate(async ([project, name]) => {
        const r = await fetch(`/projects/${project}/editor/raw?path=${name}`, { headers: { Range: "bytes=0-99" } });
        return { status: r.status, type: r.headers.get("content-type"), disposition: r.headers.get("content-disposition") };
      }, [project, `clip_${tag}.mp4`]);
      assert(raw.status === 206 && /video\/mp4/.test(raw.type || ""), `range request: ${JSON.stringify(raw)}`);
      assert(!raw.disposition, `the clip was served as an attachment: ${raw.disposition}`);
      const sel = tabSel(`clip_${tag}.mp4`);
      await page.evaluate((s) => document.querySelector(`${s} .editor-tab-state`).click(), sel);
      await page.waitForFunction((s) => !document.querySelector(s), sel, { timeout: 6000 });
    });

    await run("the tree keeps its scroll position across a file operation", async () => {
      await page.evaluate(async ([project]) => {
        const token = document.querySelector('meta[name="csrf-token"]').content;
        for (let i = 0; i < 40; i++) {
          await fetch(`/projects/${project}/editor/file`, {
            method: "POST",
            headers: { "X-CSRF-Token": token, "Content-Type": "application/x-www-form-urlencoded" },
            body: `path=zscroll_${String(i).padStart(2, "0")}.txt&content=x`,
          });
        }
      }, [project]);
      await page.click("[data-editor-refresh]");
      await page.waitForSelector('.editor-file[data-path="zscroll_39.txt"]', { timeout: 8000 });
      await sleep(400);
      await page.evaluate(() => { document.querySelector(".editor-tree").scrollTop = 420; });
      await sleep(300);
      const before = await page.evaluate(() => Math.round(document.querySelector(".editor-tree").scrollTop));
      assert(before > 300, `the tree did not scroll (${before})`);
      // Every action rebuilds the tree; the refresh is that rebuild in its
      // plainest form.
      await page.click("[data-editor-refresh]");
      await page.waitForSelector('.editor-file[data-path="zscroll_39.txt"]', { timeout: 8000 });
      await sleep(600);
      const after = await page.evaluate(() => Math.round(document.querySelector(".editor-tree").scrollTop));
      assert(Math.abs(after - before) < 20, `the tree jumped: ${before} -> ${after}`);
      // A delete rebuilds it too. Opening the row menu may scroll the row into
      // view first, so the reference is taken once the menu is up.
      await openRowMenu(page, '.editor-file[data-path="zscroll_39.txt"]');
      const beforeDelete = await page.evaluate(() => Math.round(document.querySelector(".editor-tree").scrollTop));
      await menuItem(page, "Delete").click();
      await confirmSwal(page);
      await page.waitForFunction(() => !document.querySelector('.editor-file[data-path="zscroll_39.txt"]'), null, { timeout: 8000 });
      await sleep(600);
      const afterDelete = await page.evaluate(() => Math.round(document.querySelector(".editor-tree").scrollTop));
      assert(Math.abs(afterDelete - beforeDelete) < 40, `the tree jumped on delete: ${beforeDelete} -> ${afterDelete}`);
      await page.waitForSelector(".swal2-container", { state: "detached", timeout: 4000 }).catch(() => {});
      await page.evaluate(() => { document.querySelector(".editor-tree").scrollTop = 0; });
    });

    await run("a drop onto a taken name asks before it replaces", async () => {
      await page.evaluate(async ([project]) => {
        const token = document.querySelector('meta[name="csrf-token"]').content;
        const post = (what, body) => fetch(`/projects/${project}/editor/${what}`, {
          method: "POST",
          headers: { "X-CSRF-Token": token, "Content-Type": "application/x-www-form-urlencoded" },
          body,
        });
        await post("mkdir", "path=clash");
        await post("file", "path=clash/dup.txt&content=old%20one");
        await post("file", "path=dup.txt&content=new%20one");
      }, [project]);
      await page.click("[data-editor-refresh]");
      await page.waitForSelector('.editor-file[data-path="dup.txt"]', { timeout: 8000 });
      await sleep(400);
      await dragRow(page, '.editor-file[data-path="dup.txt"]', '.editor-dir[data-path="clash"]');
      await page.waitForSelector(".swal2-popup", { timeout: 6000 });
      const text = await page.textContent(".swal2-popup");
      assert(/Replace/i.test(text) && /dup\.txt/.test(text), `unexpected confirm: ${text.slice(0, 120)}`);
      // Cancelling leaves both files where they are.
      await page.click(".swal2-cancel");
      await page.waitForSelector(".swal2-container", { state: "detached", timeout: 4000 });
      assert(await page.$('.editor-file[data-path="dup.txt"]'), "the file moved although the replace was cancelled");
      await dragRow(page, '.editor-file[data-path="dup.txt"]', '.editor-dir[data-path="clash"]');
      await page.waitForSelector(".swal2-popup", { timeout: 6000 });
      await confirmSwal(page);
      await page.waitForFunction(() => !document.querySelector('.editor-file[data-path="dup.txt"]'), null, { timeout: 8000 });
      const content = await page.evaluate(async ([project]) => {
        const r = await fetch(`/projects/${project}/editor/raw?path=clash/dup.txt&download=1`);
        return r.text();
      }, [project]);
      assert(content.trim() === "new one", `the dropped file did not replace the old one: ${content}`);
    });

    await run("an upload onto a taken name names it and offers to replace", async () => {
      await page.setInputFiles("[data-editor-upload-input]", [{
        name: "dup.txt", mimeType: "text/plain", buffer: Buffer.from("uploaded one"),
      }]);
      await page.waitForSelector(".editor-upload-list", { timeout: 6000 });
      assert(!(await page.$("[data-upload-clash]")), "the root has no dup.txt, yet a clash was reported");
      await page.click(".swal2-cancel");
      await page.waitForSelector(".swal2-container", { state: "detached", timeout: 4000 });
      await page.setInputFiles("[data-editor-upload-input]", [{
        name: "dup.txt", mimeType: "text/plain", buffer: Buffer.from("uploaded one"),
      }]);
      await page.waitForSelector(".editor-upload-list", { timeout: 6000 });
      await page.click(".swal2-confirm");
      await page.waitForSelector(".swal2-container", { state: "detached", timeout: 8000 });
      await page.waitForSelector('.editor-file[data-path="dup.txt"]', { timeout: 8000 });
      // Now the name is taken: the dialog says so and the button changes.
      await page.setInputFiles("[data-editor-upload-input]", [{
        name: "dup.txt", mimeType: "text/plain", buffer: Buffer.from("replaced by the upload"),
      }]);
      await page.waitForSelector("[data-upload-clash]", { timeout: 6000 });
      const warn = await page.textContent("[data-upload-clash]");
      assert(/dup\.txt/.test(warn) && /replaced/i.test(warn), `clash warning: ${warn}`);
      assert((await page.textContent(".swal2-confirm")).trim() === "Replace", "the confirm button still says Upload");
      await page.click(".swal2-confirm");
      await page.waitForSelector(".swal2-container", { state: "detached", timeout: 8000 });
      const content = await page.evaluate(async ([project]) => {
        const r = await fetch(`/projects/${project}/editor/raw?path=dup.txt&download=1`);
        return r.text();
      }, [project]);
      assert(content.trim() === "replaced by the upload", `the upload did not replace the file: ${content}`);
    });

    await run("copy and paste put a file and a folder into another folder", async () => {
      await page.evaluate(async ([project]) => {
        const token = document.querySelector('meta[name="csrf-token"]').content;
        const post = (what, body) => fetch(`/projects/${project}/editor/${what}`, {
          method: "POST",
          headers: { "X-CSRF-Token": token, "Content-Type": "application/x-www-form-urlencoded" },
          body,
        });
        await post("mkdir", "path=cpsrc");
        await post("mkdir", "path=cpdest");
        await post("file", "path=cpsrc/inner.txt&content=inner%20text");
        await post("file", "path=cpfile.txt&content=file%20text");
      }, [project]);
      await page.click("[data-editor-refresh]");
      await page.waitForSelector('.editor-file[data-path="cpfile.txt"]', { timeout: 8000 });
      await sleep(400);
      // A file into another folder.
      await openRowMenu(page, '.editor-file[data-path="cpfile.txt"]');
      await menuItem(page, "Copy file").click();
      await openRowMenu(page, '.editor-dir[data-path="cpdest"]');
      const labels = await page.$$eval(".dc-context-menu .dropdown-item", (els) => els.map((e) => e.textContent.trim()));
      assert(labels.some((l) => l === 'Paste "cpfile.txt"'), `paste entry missing: ${labels.join(", ")}`);
      await page.click('.dc-context-menu .dropdown-item:has-text("Paste")');
      await page.waitForSelector('.editor-file[data-path="cpdest/cpfile.txt"]', { timeout: 8000 });
      assert(await page.$('.editor-file[data-path="cpfile.txt"]'), "the source file disappeared, that is a move not a copy");
      // A folder with its content.
      await openRowMenu(page, '.editor-dir[data-path="cpsrc"]');
      await menuItem(page, "Copy folder").click();
      await openRowMenu(page, '.editor-dir[data-path="cpdest"]');
      await page.click('.dc-context-menu .dropdown-item:has-text("Paste")');
      await page.waitForSelector('.editor-dir[data-path="cpdest/cpsrc"]', { timeout: 8000 });
      // The copy carries the content, the folder just opens closed.
      await page.click('.editor-dir[data-path="cpdest/cpsrc"]');
      await page.waitForSelector('.editor-file[data-path="cpdest/cpsrc/inner.txt"]', { timeout: 8000 });
      // Pasting into the folder the source sits in duplicates it under a free name.
      await openRowMenu(page, '.editor-file[data-path="cpfile.txt"]');
      await menuItem(page, "Copy file").click();
      await treeRootMenu();
      await page.click('.dc-context-menu .dropdown-item:has-text("Paste")');
      await page.waitForSelector('.editor-file[data-path="cpfile copy.txt"]', { timeout: 8000 });
      // Pasting onto a name that is taken asks first.
      await openRowMenu(page, '.editor-file[data-path="cpfile.txt"]');
      await menuItem(page, "Copy file").click();
      await openRowMenu(page, '.editor-dir[data-path="cpdest"]');
      await page.click('.dc-context-menu .dropdown-item:has-text("Paste")');
      await page.waitForSelector(".swal2-popup", { timeout: 6000 });
      assert(/Replace/i.test(await page.textContent(".swal2-popup")), "no replace confirm on a taken name");
      await page.click(".swal2-cancel");
      await page.waitForSelector(".swal2-container", { state: "detached", timeout: 4000 });
    });

    await run("a folder downloads as a tar.gz that unpacks with its content", async () => {
      await page.evaluate(async ([project]) => {
        const token = document.querySelector('meta[name="csrf-token"]').content;
        const post = (what, body) => fetch(`/projects/${project}/editor/${what}`, {
          method: "POST",
          headers: { "X-CSRF-Token": token, "Content-Type": "application/x-www-form-urlencoded" },
          body,
        });
        await post("mkdir", "path=arch");
        await post("mkdir", "path=arch/deep");
        await post("file", "path=arch/top.txt&content=top%20file");
        await post("file", "path=arch/deep/inner.txt&content=inner%20file");
      }, [project]);
      await page.click("[data-editor-refresh]");
      await page.waitForSelector('.editor-dir[data-path="arch"]', { timeout: 8000 });
      await openRowMenu(page, '.editor-dir[data-path="arch"]');
      const labels = await page.$$eval(".dc-context-menu .dropdown-item", (els) => els.map((e) => e.textContent.trim()));
      assert(labels.includes("Download as tar.gz"), `folder menu: ${labels.join(", ")}`);
      const [download] = await Promise.all([
        page.waitForEvent("download", { timeout: 10000 }),
        page.click('.dc-context-menu .dropdown-item:has-text("Download as tar.gz")'),
      ]);
      assert(download.suggestedFilename() === "arch.tar.gz", `download named ${download.suggestedFilename()}`);
      // Unpack it here: the archive is only useful if tar can read it.
      const zlib = require("zlib");
      const fs = require("fs");
      const tar = zlib.gunzipSync(fs.readFileSync(await download.path()));
      const names = [];
      for (let off = 0; off + 512 <= tar.length; ) {
        const name = tar.toString("utf8", off, off + 100).replace(/\0.*$/, "");
        if (!name) break;
        const size = parseInt(tar.toString("utf8", off + 124, off + 135).trim() || "0", 8);
        names.push(name);
        off += 512 + Math.ceil(size / 512) * 512;
      }
      for (const want of ["arch/", "arch/top.txt", "arch/deep/", "arch/deep/inner.txt"]) {
        assert(names.includes(want), `archive misses ${want}: ${names.join(", ")}`);
      }
      // Round trip: upload the archive back and unpack it from the menu. The
      // upload follows the tree selection, so the root has to be the target.
      await treeRootMenu();
      await page.keyboard.press("Escape");
      await page.waitForSelector(".dc-context-menu", { state: "detached", timeout: 4000 });
      await page.setInputFiles("[data-editor-upload-input]", [{
        name: "arch.tar.gz",
        mimeType: "application/gzip",
        buffer: fs.readFileSync(await download.path()),
      }]);
      await page.waitForSelector(".editor-upload-list", { timeout: 6000 });
      await page.click(".swal2-confirm");
      await page.waitForSelector(".swal2-container", { state: "detached", timeout: 8000 });
      await page.waitForSelector('.editor-file[data-path="arch.tar.gz"]', { timeout: 8000 });
      await openRowMenu(page, '.editor-file[data-path="arch.tar.gz"]');
      const archiveLabels = await page.$$eval(".dc-context-menu .dropdown-item", (els) => els.map((e) => e.textContent.trim()));
      assert(archiveLabels.includes("Extract here"), `archive menu: ${archiveLabels.join(", ")}`);
      await page.click('.dc-context-menu .dropdown-item:has-text("Extract here")');
      // The folder name arch is taken, so the unpacked one gets a free name.
      await page.waitForSelector('.editor-dir[data-path="arch 2"]', { timeout: 10000 });
      await page.click('.editor-dir[data-path="arch 2"]');
      await page.waitForSelector('.editor-dir[data-path="arch 2/arch"]', { timeout: 8000 });
      // A plain file offers no extraction.
      await openRowMenu(page, '.editor-file[data-path="icons.json"]');
      const plainLabels = await page.$$eval(".dc-context-menu .dropdown-item", (els) => els.map((e) => e.textContent.trim()));
      assert(!plainLabels.includes("Extract here"), "a plain file offers Extract here");
      await page.keyboard.press("Escape");
      await page.waitForSelector(".dc-context-menu", { state: "detached", timeout: 4000 });
      // Opening the archive shows the same action next to the download, and its
      // tab menu carries the file actions of the tree row.
      await page.click('.editor-file[data-path="arch.tar.gz"] .editor-item-name');
      await page.waitForSelector("[data-editor-viewer] [data-editor-extract]", { timeout: 8000 });
      await openRowMenu(page, tabSel("arch.tar.gz"));
      const tabLabels = await page.$$eval(".dc-context-menu .dropdown-item", (els) => els.map((e) => e.textContent.trim()));
      for (const want of ["Copy file", "Extract here", "Download", "Reveal in tree"]) {
        assert(tabLabels.includes(want), `archive tab menu misses ${want}: ${tabLabels.join(", ")}`);
      }
      await page.keyboard.press("Escape");
      await page.waitForSelector(".dc-context-menu", { state: "detached", timeout: 4000 });
      await page.click("[data-editor-viewer] [data-editor-extract]");
      await page.waitForSelector('.editor-dir[data-path="arch 3"]', { timeout: 10000 });
      await page.click('.editor-file[data-path="icons.json"] .editor-item-name');
      await page.waitForSelector("[data-editor-viewer] [data-editor-extract]", { state: "detached", timeout: 6000 }).catch(() => {});
      assert(!(await page.$("[data-editor-viewer] [data-editor-extract]:visible")), "a plain file shows the extract button");
      // Leave the strip as it was, the tab checks below count on that.
      for (const path of ["arch.tar.gz", "icons.json"]) {
        const sel = tabSel(path);
        if (await page.$(sel)) {
          await page.evaluate((s) => document.querySelector(`${s} .editor-tab-state`).click(), sel);
          await page.waitForFunction((s) => !document.querySelector(s), sel, { timeout: 6000 });
        }
      }
    });

    await run("resting a drag on a closed folder opens it", async () => {
      await page.evaluate(async ([project]) => {
        const token = document.querySelector('meta[name="csrf-token"]').content;
        const post = (what, body) => fetch(`/projects/${project}/editor/${what}`, {
          method: "POST",
          headers: { "X-CSRF-Token": token, "Content-Type": "application/x-www-form-urlencoded" },
          body,
        });
        await post("mkdir", "path=spring");
        await post("file", "path=spring/inside.txt&content=inside");
      }, [project]);
      await page.click("[data-editor-refresh]");
      await page.waitForSelector('.editor-dir[data-path="spring"]', { timeout: 8000 });
      await sleep(400);
      // Both rows have to be on screen together and clear of the tree edges,
      // where the pointer would auto-scroll instead of opening the folder. The
      // file right below the folder block is always the closest candidate.
      const source = await page.evaluate(() => {
        const tree = document.querySelector(".editor-tree");
        const row = document.querySelector('.editor-dir[data-path="spring"]');
        row.scrollIntoView({ block: "center" });
        const rows = [...tree.querySelectorAll(".editor-file")];
        const below = rows.find((r) => r.getBoundingClientRect().top > row.getBoundingClientRect().bottom);
        return below?.dataset.path ?? null;
      });
      assert(source, "no file row below the folder to drag");
      await sleep(300);
      const closed = await page.evaluate(() => document.querySelector('.editor-dir[data-path="spring"]').nextElementSibling.hidden);
      assert(closed, "the folder was already open before the drag");
      const from = await page.locator(`.editor-file[data-path="${source}"]`).boundingBox();
      const to = await page.locator('.editor-dir[data-path="spring"]').boundingBox();
      await page.mouse.move(from.x + 40, from.y + from.height / 2);
      await page.mouse.down();
      await page.mouse.move(from.x + 55, from.y + from.height / 2, { steps: 3 });
      await page.mouse.move(to.x + 60, to.y + to.height / 2, { steps: 10 });
      await sleep(200);
      assert(await page.evaluate(() => document.querySelector('.editor-dir[data-path="spring"]').nextElementSibling.hidden),
        "the folder opened before the hold was over");
      await sleep(900);
      const opened = await page.evaluate(() => !document.querySelector('.editor-dir[data-path="spring"]').nextElementSibling.hidden);
      await page.mouse.up();
      await sleep(1000);
      assert(opened, "resting on the folder did not open it");
      await page.waitForSelector(`.editor-file[data-path="spring/${source}"]`, { timeout: 8000 });
    });

    await run("a folder upload keeps its structure", async () => {
      // The picker input carries webkitdirectory, so every file arrives with its
      // path inside the folder; the drop path walks the same tree through
      // webkitGetAsEntry and ends in the same upload.
      const fs = require("fs");
      const os = require("os");
      const path = require("path");
      const root = fs.mkdtempSync(path.join(os.tmpdir(), "upfolder-"));
      fs.mkdirSync(path.join(root, "nested"));
      fs.writeFileSync(path.join(root, "top.txt"), "top level");
      fs.writeFileSync(path.join(root, "nested", "deep.txt"), "deep");
      await treeRootMenu();
      await page.keyboard.press("Escape");
      await page.waitForSelector(".dc-context-menu", { state: "detached", timeout: 4000 });
      await page.setInputFiles("[data-editor-upload-dir-input]", root);
      await page.waitForSelector(".editor-upload-list", { timeout: 8000 });
      const rows = await page.$$eval(".editor-upload-item .text-truncate", (els) => els.map((e) => e.textContent.trim()));
      const folder = path.basename(root);
      assert(rows.some((r) => r === `${folder}/nested/deep.txt`), `upload list shows ${rows.join(", ")}`);
      await page.click(".swal2-confirm");
      await page.waitForSelector(".swal2-container", { state: "detached", timeout: 15000 });
      await page.waitForSelector(`.editor-dir[data-path="${folder}"]`, { timeout: 10000 });
      // The uploaded folder opens itself, its subfolders stay closed.
      await page.waitForSelector(`.editor-file[data-path="${folder}/top.txt"]`, { timeout: 8000 });
      await page.click(`.editor-dir[data-path="${folder}/nested"]`);
      await page.waitForSelector(`.editor-file[data-path="${folder}/nested/deep.txt"]`, { timeout: 8000 });
      const content = await page.evaluate(async ([project, p]) => {
        const r = await fetch(`/projects/${project}/editor/raw?path=${encodeURIComponent(p)}&download=1`);
        return r.text();
      }, [project, `${folder}/nested/deep.txt`]);
      assert(content.trim() === "deep", `uploaded content: ${content}`);
      fs.rmSync(root, { recursive: true, force: true });
    });

    await run("closing a folder folds its children too, before and after a reload", async () => {
      await page.evaluate(async ([project]) => {
        const token = document.querySelector('meta[name="csrf-token"]').content;
        const post = (what, body) => fetch(`/projects/${project}/editor/${what}`, {
          method: "POST",
          headers: { "X-CSRF-Token": token, "Content-Type": "application/x-www-form-urlencoded" },
          body,
        });
        await post("mkdir", "path=fold/deep/deeper");
        await post("file", "path=fold/deep/deeper/leaf.txt&content=leaf");
      }, [project]);
      await page.click("[data-editor-refresh]");
      await page.waitForSelector('.editor-dir[data-path="fold"]', { timeout: 8000 });
      await page.click('.editor-dir[data-path="fold"]');
      await page.waitForSelector('.editor-dir[data-path="fold/deep"]', { timeout: 8000 });
      await page.click('.editor-dir[data-path="fold/deep"]');
      await page.waitForSelector('.editor-dir[data-path="fold/deep/deeper"]', { timeout: 8000 });
      await page.click('.editor-dir[data-path="fold/deep/deeper"]');
      await page.waitForSelector('.editor-file[data-path="fold/deep/deeper/leaf.txt"]', { timeout: 8000 });
      // Closing the top folder folds everything below it.
      await page.click('.editor-dir[data-path="fold"]');
      await sleep(400);
      const state = await page.evaluate(() => ({
        deep: document.querySelector('.editor-dir[data-path="fold/deep"]')?.nextElementSibling.hidden,
        deeper: document.querySelector('.editor-dir[data-path="fold/deep/deeper"]')?.nextElementSibling.hidden,
        saved: Object.entries(localStorage)
          .filter(([key]) => key.includes("tree"))
          .flatMap(([, value]) => { try { return JSON.parse(value); } catch { return []; } })
          .filter((p) => typeof p === "string" && p.startsWith("fold")),
      }));
      assert(state.deep && state.deeper, `children stayed open: ${JSON.stringify(state)}`);
      assert(state.saved.length === 0, `saved dirs still hold ${state.saved.join(", ")}`);
      await page.click('.editor-dir[data-path="fold"]');
      await page.waitForSelector('.editor-dir[data-path="fold/deep"]', { timeout: 8000 });
      assert(await page.evaluate(() => document.querySelector('.editor-dir[data-path="fold/deep"]').nextElementSibling.hidden),
        "reopening the parent brought its children back open");
      // And the same after a reload, the saved set decides there.
      await page.click('.editor-dir[data-path="fold"]');
      await sleep(300);
      await page.reload({ waitUntil: "domcontentloaded" });
      await page.waitForSelector('.editor-dir[data-path="fold"]', { timeout: 10000 });
      await sleep(600);
      assert(!(await page.$('.editor-dir[data-path="fold/deep"]')), "the reload reopened the folder tree");
      // The reload restores a tree selection, and new files follow it. Point it
      // back at a root row so the checks below create their files in the root.
      await openRowMenu(page, '.editor-file[data-path="icons.json"]');
      await page.keyboard.press("Escape");
      await page.waitForSelector(".dc-context-menu", { state: "detached", timeout: 4000 });
    });

    await run("a tab switch keeps the scroll position and does not move the editor", async () => {
      await page.waitForSelector(".swal2-container", { state: "detached", timeout: 4000 }).catch(() => {});
      const longFile = `sc1_${tag}.txt`;
      const shortFile = `sc2_${tag}.txt`;
      await page.evaluate(async ([project, path, other]) => {
        const token = document.querySelector('meta[name="csrf-token"]').content;
        const write = (p, content) => fetch(`/projects/${project}/editor/file`, {
          method: "POST",
          headers: { "X-CSRF-Token": token, "Content-Type": "application/x-www-form-urlencoded" },
          body: "path=" + encodeURIComponent(p) + "&content=" + encodeURIComponent(content),
        });
        await write(path, Array.from({ length: 400 }, (_, i) => `line ${i}`).join("\n"));
        await write(other, "one line\n");
      }, [project, longFile, shortFile]);
      const openViaPalette = async (name) => {
        await page.keyboard.press("Control+O");
        await page.waitForSelector("[data-editor-quickopen]:not([hidden])", { timeout: 6000 });
        await page.fill("[data-editor-quickopen-input]", name);
        await page.waitForSelector(".editor-quickopen-item", { timeout: 6000 });
        await page.keyboard.press("Enter");
        await page.waitForSelector(`${tabSel(name)}.active`, { timeout: 8000 });
        await sleep(600);
      };
      await openViaPalette(shortFile);
      await openViaPalette(longFile);
      await page.click(tabSel(longFile));
      await sleep(600);
      await page.evaluate(() => { document.querySelector(".cm-scroller").scrollTop = 900; });
      await sleep(400);
      const before = await page.evaluate(() => ({
        top: Math.round(document.querySelector(".editor-tabs").getBoundingClientRect().top),
        scroll: Math.round(document.querySelector(".cm-scroller").scrollTop),
      }));
      assert(before.scroll > 500, `the long file did not scroll (${before.scroll})`);
      await page.click(tabSel(shortFile));
      await sleep(700);
      const middle = await page.evaluate(() => Math.round(document.querySelector(".editor-tabs").getBoundingClientRect().top));
      assert(middle === before.top, `the editor moved on the switch: ${before.top} -> ${middle}`);
      await page.click(tabSel(longFile));
      await sleep(700);
      const after = await page.evaluate(() => ({
        top: Math.round(document.querySelector(".editor-tabs").getBoundingClientRect().top),
        scroll: Math.round(document.querySelector(".cm-scroller").scrollTop),
      }));
      assert(after.top === before.top, `the editor moved coming back: ${before.top} -> ${after.top}`);
      assert(Math.abs(after.scroll - before.scroll) < 20, `scroll position lost: ${before.scroll} -> ${after.scroll}`);
      // Leave the strip as this check found it, the next ones count tabs.
      for (const name of [longFile, shortFile]) {
        await page.evaluate((s) => document.querySelector(`${s} .editor-tab-state`).click(), tabSel(name));
        await page.waitForFunction((s) => !document.querySelector(s), tabSel(name), { timeout: 6000 });
      }
    });

    await run("Ctrl+Tab and Ctrl+Shift+Tab step through the open tabs in strip order", async () => {
      await page.waitForSelector(".swal2-container", { state: "detached", timeout: 4000 }).catch(() => {});
      await newFile(`ct1_${tag}.txt`);
      await newFile(`ct2_${tag}.txt`);
      await page.waitForSelector(`${tabSel(`ct2_${tag}.txt`)}.active`, { timeout: 6000 });
      await page.keyboard.press("Control+Tab");
      await page.waitForSelector(`${tabSel(`ct1_${tag}.txt`)}.active`, { timeout: 6000 });
      await page.keyboard.press("Control+Shift+Tab");
      await page.waitForSelector(`${tabSel(`ct2_${tag}.txt`)}.active`, { timeout: 6000 });
    });

    await run("mouse drag reorders the tabs and the order survives a reload", async () => {
      const order = () => page.$$eval("[data-editor-tabs] .editor-tab", (els) => els.map((el) => el.dataset.path));
      const before = await order();
      assert(before.length >= 2, `need two tabs to drag: ${before}`);
      const from = await page.locator(tabSel(before[before.length - 1])).boundingBox();
      const to = await page.locator(tabSel(before[0])).boundingBox();
      await page.mouse.move(from.x + from.width / 2, from.y + from.height / 2);
      await page.mouse.down();
      const targetX = to.x + to.width * 0.2;
      for (let i = 1; i <= 12; i++) {
        await page.mouse.move(from.x + from.width / 2 + (targetX - from.x - from.width / 2) * (i / 12), from.y + from.height / 2, { steps: 2 });
        await sleep(30);
      }
      await page.mouse.up();
      await sleep(300);
      const after = await order();
      const expected = [before[before.length - 1], ...before.slice(0, -1)];
      assert(JSON.stringify(after) === JSON.stringify(expected), `drag did not reorder: ${after} != ${expected}`);
      // The drag release must not switch the active tab.
      assert(await page.$(`${tabSel(`ct2_${tag}.txt`)}.active`), "drag changed the active tab");
      await page.reload({ waitUntil: "domcontentloaded" });
      await page.waitForSelector(".cm-editor", { state: "attached", timeout: 12000 });
      await page.waitForSelector("[data-editor-tabs] .editor-tab", { timeout: 8000 });
      assert(JSON.stringify(await order()) === JSON.stringify(expected), "reordered tabs did not survive the reload");
    });

    await run("fullscreen: button toggles and persists, Ctrl+Shift+Enter and strip double-click toggle too", async () => {
      const waitFullscreen = (want) => page.waitForFunction(
        (w) => document.documentElement.classList.contains("dc-editor-fullscreen") === w, want, { timeout: 6000 });
      await page.click("[data-editor-fullscreen]");
      await waitFullscreen(true);
      assert(await page.$eval("[data-editor-fullscreen]", (e) => e.getAttribute("aria-pressed") === "true"), "button not pressed after enabling");
      assert(await page.$eval("[data-editor-fullscreen] i", (e) => e.className.includes("ti-minimize")), "icon did not switch to minimize");
      assert(await page.isVisible(".editor-back"), "back button not visible in fullscreen");
      await page.reload({ waitUntil: "domcontentloaded" });
      await page.waitForSelector(".cm-editor", { state: "attached", timeout: 12000 });
      await waitFullscreen(true);
      await page.keyboard.press("Control+Shift+Enter");
      await waitFullscreen(false);
      const box = await page.locator("[data-editor-tabs]").boundingBox();
      await page.mouse.dblclick(box.x + box.width - 8, box.y + box.height / 2);
      await waitFullscreen(true);
      await page.click("[data-editor-fullscreen]");
      await waitFullscreen(false);
      assert(!(await page.isVisible(".editor-back")), "back button visible outside fullscreen");
    });

    // A docked assistant keeps its column in fullscreen. The panel sits above
    // the editor, so an editor that took the whole viewport would run under it
    // and lose its right edge. The editor ends at the panel's left edge
    // instead, and it follows the resize handle because both sides read
    // --dc-assistant-w. Closing the panel gives the viewport back.
    await run("fullscreen: the docked assistant keeps its column and the editor follows its resize", async () => {
      const edges = () => page.evaluate(() => {
        const box = document.querySelector(".editor").getBoundingClientRect();
        const card = document.querySelector(".dc-assistant-panel-card").getBoundingClientRect();
        return {
          right: Math.round(box.right),
          panelLeft: Math.round(card.left),
          width: window.innerWidth,
          docked: document.body.classList.contains("dc-assistant-docked"),
        };
      });
      await page.click("[data-assistant-corner]");
      await page.waitForSelector(".dc-assistant-panel-card:not([hidden]) dc-assistant[ready]", { timeout: 20000 });
      await page.click("[data-editor-fullscreen]");
      await page.waitForFunction(() => document.documentElement.classList.contains("dc-editor-fullscreen"), null, { timeout: 6000 });
      await sleep(300);
      const side = await edges();
      assert(side.docked, "the panel did not dock");
      assert(Math.abs(side.right - side.panelLeft) <= 1 && side.right < side.width - 100,
        `the editor does not stop at the panel: ${JSON.stringify(side)}`);

      const handle = await page.locator("[data-assistant-resize]").boundingBox();
      await page.mouse.move(handle.x + handle.width / 2, handle.y + handle.height / 2);
      await page.mouse.down();
      await page.mouse.move(handle.x - 120, handle.y + handle.height / 2, { steps: 10 });
      await page.mouse.up();
      await sleep(400);
      const wider = await edges();
      assert(wider.right < side.right - 30 && Math.abs(wider.right - wider.panelLeft) <= 1,
        `the editor did not follow the panel's resize: ${JSON.stringify(wider)}`);

      await page.click("[data-assistant-panel-close]");
      await page.waitForSelector(".dc-assistant-panel-card[hidden]", { state: "attached", timeout: 8000 });
      await sleep(300);
      const full = await page.evaluate(() => {
        const box = document.querySelector(".editor").getBoundingClientRect();
        return { right: Math.round(box.right), width: window.innerWidth };
      });
      assert(Math.abs(full.right - full.width) <= 1, `the closed panel left a gap: ${JSON.stringify(full)}`);
      await page.click("[data-editor-fullscreen]");
      await page.waitForFunction(() => !document.documentElement.classList.contains("dc-editor-fullscreen"), null, { timeout: 6000 });
    });

    await run("project switcher in the tree header switches to another project's editor", async () => {
      await L.createProject(page, projectB);
      await page.goto(editorURL, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(".cm-editor", { state: "attached", timeout: 12000 });
      const order = () => page.$$eval(".editor-project-menu .dropdown-item", (els) => els.map((e) => e.dataset.projectName));
      let names = await order();
      assert(names.indexOf(projectB) >= 0 && names.indexOf(projectB) < names.indexOf(project),
        `alpha sort order wrong: ${names.join(", ")}`);
      await page.evaluate(() => localStorage.setItem("dc-project-sort", "recent"));
      await page.reload({ waitUntil: "domcontentloaded" });
      await page.waitForSelector(".cm-editor", { state: "attached", timeout: 12000 });
      names = await order();
      assert(names.indexOf(project) >= 0 && names.indexOf(project) < names.indexOf(projectB),
        `recent sort order wrong: ${names.join(", ")}`);
      await page.evaluate(() => localStorage.removeItem("dc-project-sort"));
      await page.click(".editor-project-switch");
      await page.waitForSelector(".editor-project-menu.show", { timeout: 4000 });
      const active = await page.$eval(".editor-project-menu .dropdown-item.active", (e) => e.textContent.trim());
      assert(active === project, `active switcher entry is ${active}`);
      await page.click(`.editor-project-menu .dropdown-item:has-text("${projectB}")`);
      await page.waitForFunction((p) => decodeURIComponent(location.pathname) === `/projects/${p}/editor`, projectB, { timeout: 8000 });
      await page.waitForFunction((p) => {
        const el = document.querySelector(".editor-project-switch");
        return el && el.textContent.includes(p);
      }, projectB, { timeout: 8000 });
    });

    await run("mobile: tree is a drawer, auto-open without tabs, closes on open", async () => {
      const mp = await mobilePage();
      await mp.goto(editorURL, { waitUntil: "domcontentloaded" });
      await mp.waitForSelector(".cm-editor", { state: "attached", timeout: 12000 });
      await mp.waitForSelector(".editor.editor-drawer-open", { timeout: 8000 });
      assert(await mp.isVisible("[data-editor-drawer-toggle]"), "drawer toggle not visible on mobile");
      await mp.evaluate(() => {
        const tree = document.querySelector("[data-editor-tree]");
        const rect = tree.getBoundingClientRect();
        tree.dispatchEvent(new PointerEvent("pointerdown", {
          bubbles: true, cancelable: true, pointerId: 13, pointerType: "touch",
          clientX: rect.left + rect.width / 2, clientY: rect.bottom - 16, buttons: 1,
        }));
      });
      await mp.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
      await mp.evaluate(() => {
        const tree = document.querySelector("[data-editor-tree]");
        const rect = tree.getBoundingClientRect();
        tree.dispatchEvent(new PointerEvent("pointerup", {
          bubbles: true, cancelable: true, pointerId: 13, pointerType: "touch",
          clientX: rect.left + rect.width / 2, clientY: rect.bottom - 16,
        }));
      });
      await menuItem(mp, "New file").click();
      await mp.waitForSelector(".swal2-input", { state: "visible", timeout: 4000 });
      await mp.fill(".swal2-input", `mob_${tag}.txt`); await mp.click(".swal2-confirm");
      await mp.waitForSelector(tabSel(`mob_${tag}.txt`), { timeout: 8000 });
      assert(await mp.$(".editor.editor-drawer-open"), "creating a file closed the drawer");
      await mp.click(`.editor-file[data-path="${noteFile}"]`);
      await mp.waitForSelector(`${tabSel(noteFile)}.active`, { timeout: 8000 });
      await mp.waitForFunction(() => !document.querySelector(".editor.editor-drawer-open"), null, { timeout: 6000 });
      await mp.click("[data-editor-drawer-toggle]");
      await mp.waitForSelector(".editor.editor-drawer-open", { timeout: 6000 });
      await mp.click("[data-editor-backdrop]");
      await mp.waitForFunction(() => !document.querySelector(".editor.editor-drawer-open"), null, { timeout: 6000 });
    });

    await run("mobile: tapping the active tab and long-pressing any tab open the context menu", async () => {
      const mp = await mobilePage();
      await mp.waitForSelector(`${tabSel(noteFile)}.active`, { timeout: 8000 });
      await mp.tap(`${tabSel(noteFile)} .editor-tab-name`);
      await mp.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
      await menuItem(mp, "Close").click();
      await mp.waitForFunction((s) => !document.querySelector(s), tabSel(noteFile), { timeout: 6000 });
      const mob = `mob_${tag}.txt`;
      await mp.waitForSelector(`${tabSel(mob)}.active`, { timeout: 6000 });
      await mp.evaluate((sel) => {
        const el = document.querySelector(sel);
        const rect = el.getBoundingClientRect();
        el.dispatchEvent(new PointerEvent("pointerdown", {
          bubbles: true, cancelable: true, pointerId: 7, pointerType: "touch",
          clientX: rect.left + 12, clientY: rect.top + 12, buttons: 1,
        }));
      }, tabSel(mob));
      await mp.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
      await mp.evaluate((sel) => {
        const el = document.querySelector(sel);
        const rect = el.getBoundingClientRect();
        el.dispatchEvent(new PointerEvent("pointerup", {
          bubbles: true, cancelable: true, pointerId: 7, pointerType: "touch",
          clientX: rect.left + 12, clientY: rect.top + 12,
        }));
      }, tabSel(mob));
      const labels = await mp.$$eval(".dc-context-menu .dropdown-item", (els) => els.map((e) => e.textContent.trim()));
      assert(labels.includes("Close all"), `long-press menu misses entries: ${labels.join(", ")}`);
      await mp.keyboard.press("Escape");
      await mp.waitForSelector(".dc-context-menu", { state: "detached", timeout: 4000 });
    });

    await run("mobile: long-pressing a tree row opens the file actions menu", async () => {
      const mp = await mobilePage();
      await mp.tap("[data-editor-drawer-toggle]");
      await mp.waitForSelector(".editor.editor-drawer-open", { timeout: 6000 });
      await mp.waitForSelector(`.editor-file[data-path="${noteFile}"]`, { timeout: 8000 });
      await mp.evaluate((sel) => {
        const el = document.querySelector(sel);
        const rect = el.getBoundingClientRect();
        el.dispatchEvent(new PointerEvent("pointerdown", {
          bubbles: true, cancelable: true, pointerId: 11, pointerType: "touch",
          clientX: rect.left + 20, clientY: rect.top + rect.height / 2, buttons: 1,
        }));
      }, `.editor-file[data-path="${noteFile}"]`);
      await mp.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
      await mp.evaluate((sel) => {
        const el = document.querySelector(sel);
        const rect = el.getBoundingClientRect();
        el.dispatchEvent(new PointerEvent("pointerup", {
          bubbles: true, cancelable: true, pointerId: 11, pointerType: "touch",
          clientX: rect.left + 20, clientY: rect.top + rect.height / 2,
        }));
      }, `.editor-file[data-path="${noteFile}"]`);
      const labels = await mp.$$eval(".dc-context-menu .dropdown-item", (els) => els.map((e) => e.textContent.trim()));
      for (const want of ["New file", "Upload files", "Rename", "Delete"]) {
        assert(labels.includes(want), `tree long-press menu misses '${want}': ${labels.join(", ")}`);
      }
      await mp.keyboard.press("Escape");
      await mp.waitForSelector(".dc-context-menu", { state: "detached", timeout: 4000 });
      await mp.evaluate(() => document.querySelector("[data-editor-backdrop]").click());
      await mp.waitForFunction(() => !document.querySelector(".editor.editor-drawer-open"), null, { timeout: 6000 });
    });

    await run("mobile: lifting the finger on the fresh menu picks nothing", async () => {
      const mp = await mobilePage();
      await mp.tap("[data-editor-drawer-toggle]");
      await mp.waitForSelector(".editor.editor-drawer-open", { timeout: 6000 });
      await mp.waitForSelector(`.editor-file[data-path="${noteFile}"]`, { timeout: 8000 });
      // The menu opens under the resting finger, so its lift lands on an entry.
      const point = await mp.evaluate((sel) => {
        const el = document.querySelector(sel);
        const rect = el.getBoundingClientRect();
        const x = Math.round(rect.left + 30);
        const y = Math.round(rect.top + rect.height / 2);
        const target = el;
        const touch = new Touch({ identifier: 21, target, clientX: x, clientY: y });
        target.dispatchEvent(new TouchEvent("touchstart", {
          bubbles: true, cancelable: true, touches: [touch], targetTouches: [touch], changedTouches: [touch],
        }));
        return { x, y };
      }, `.editor-file[data-path="${noteFile}"]`);
      await mp.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
      const entry = await mp.evaluate(() => {
        const first = document.querySelector(".dc-context-menu .dropdown-item");
        const r = first.getBoundingClientRect();
        return { label: first.textContent.trim(), x: Math.round(r.x + r.width / 2), y: Math.round(r.y + r.height / 2) };
      });
      const lift = ([x, y, id]) => {
        const target = document.elementFromPoint(x, y) || document.body;
        const touch = new Touch({ identifier: id, target, clientX: x, clientY: y });
        target.dispatchEvent(new TouchEvent("touchend", {
          bubbles: true, cancelable: true, touches: [], targetTouches: [], changedTouches: [touch],
        }));
        target.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true, clientX: x, clientY: y }));
      };
      await mp.evaluate(lift, [entry.x, entry.y, 21]);
      await sleep(500);
      assert(!(await mp.$(".swal2-container")), `the lift triggered '${entry.label}'`);
      assert(await mp.$(".dc-context-menu"), "the lift closed the menu");
      // The tap after it works as usual.
      await mp.evaluate(([x, y, id]) => {
        const target = document.elementFromPoint(x, y) || document.body;
        const touch = new Touch({ identifier: id, target, clientX: x, clientY: y });
        target.dispatchEvent(new TouchEvent("touchstart", {
          bubbles: true, cancelable: true, touches: [touch], targetTouches: [touch], changedTouches: [touch],
        }));
      }, [entry.x, entry.y, 22]);
      await mp.evaluate(lift, [entry.x, entry.y, 22]);
      await mp.waitForSelector(".swal2-container", { timeout: 6000 });
      await mp.click(".swal2-cancel");
      await mp.waitForSelector(".swal2-container", { state: "detached", timeout: 4000 });
      await mp.evaluate(() => document.querySelector("[data-editor-backdrop]").click());
      await mp.waitForFunction(() => !document.querySelector(".editor.editor-drawer-open"), null, { timeout: 6000 });
    });

    await run("mobile: fullscreen button hidden on coarse pointers", async () => {
      const mp = await mobilePage();
      assert(!(await mp.isVisible("[data-editor-fullscreen]")), "fullscreen button visible on mobile");
    });

    await run("mobile landscape: the closed drawer stays fully offscreen", async () => {
      const ctx = await browser.newContext({ ignoreHTTPSErrors: true, hasTouch: true, isMobile: true, viewport: { width: 1250, height: 390 } });
      const lp = await ctx.newPage();
      L.wirePage(lp, bag);
      try {
        await L.login(lp);
        await lp.goto(editorURL, { waitUntil: "domcontentloaded" });
        await lp.waitForSelector(".cm-editor", { state: "attached", timeout: 12000 });
        await lp.waitForSelector(".editor.editor-drawer-open", { timeout: 8000 }).catch(() => {});
        if (await lp.$(".editor.editor-drawer-open")) {
          await lp.evaluate(() => document.querySelector("[data-editor-backdrop]").click());
          await lp.waitForFunction(() => !document.querySelector(".editor.editor-drawer-open"), null, { timeout: 6000 });
        }
        await sleep(500);
        const right = await lp.$eval(".editor-tree-col", (el) => el.getBoundingClientRect().right);
        assert(right <= 0, `drawer leaks into the viewport, right edge at ${right}px`);
      } finally {
        await ctx.close();
      }
    });

    await run("lifecycle teardown (remove dc-editor, no new errors)", async ({} = {}) => {
      await page.evaluate(() => document.querySelector("dc-editor").remove());
      await sleep(600);
    });
  } finally {
    await L.deleteProject(page, project).catch(() => {});
    await L.deleteProject(page, projectB).catch(() => {});
  }
});
