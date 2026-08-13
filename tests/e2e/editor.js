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
// asks to discard, only closing a dirty tab does, and Ctrl/Cmd+Shift+X closes the
// active tab through that same path, the way it closes a terminal on the attach
// pages. The strip stands on every
// width, and so does one menu: outside it the header carries only the folder
// toggle (where the tree is a drawer), the strip, a Save that shows up when the
// file is unsaved, and the menu itself. Everything else is an entry in that
// menu, git included, and the entries are the same at 390 and at 1440. The list
// of open files is a sheet the menu opens; on touch its grip handle is the only
// way to reorder, the strip drag stays with the mouse. The tree is a drawer on
// small screens (auto-open when no tab restores) and a drag-resizable column on
// wide ones. Routes: GET /projects/:name/editor(/list|/file|/files), POST .../file
// (save), .../create, .../mkdir, .../delete, .../rename, .../upload, .../preview.
// The read answers a version of the file and the save carries it back, so a
// buffer never lands on a file a coder or git wrote in the meantime: the write
// is refused with a 409 whose `conflict` says `changed` or `deleted`, and each
// of the two gets its own dialog with exactly two ways out (Reload / Cancel,
// Create again / Cancel). There is no force save. A save with no version at all
// is the create path, which is what a file created in the editor takes on its
// first save, and what the checks here write behind the editor's back with.
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
  const tabOrder = () => page.$$eval("[data-editor-tabs] .editor-tab", (els) => els.map((el) => el.dataset.path));
  const dragLastTabToFront = async (order) => {
    const from = await page.locator(tabSel(order[order.length - 1])).boundingBox();
    const to = await page.locator(tabSel(order[0])).boundingBox();
    await page.mouse.move(from.x + from.width / 2, from.y + from.height / 2);
    await page.mouse.down();
    const targetX = to.x + to.width * 0.2;
    for (let i = 1; i <= 12; i++) {
      await page.mouse.move(from.x + from.width / 2 + (targetX - from.x - from.width / 2) * (i / 12), from.y + from.height / 2, { steps: 2 });
      await sleep(30);
    }
    await page.mouse.up();
    await sleep(300);
  };
  // What the browser really lays out, never the hidden attribute: a display
  // utility outranks it, so a control can carry hidden and still stand there.
  const boxes = (p, map) => p.evaluate((sels) => {
    const out = {};
    for (const [key, sel] of Object.entries(sels)) {
      const el = document.querySelector(sel);
      if (!el) { out[key] = null; continue; }
      const r = el.getBoundingClientRect();
      out[key] = { display: getComputedStyle(el).display, w: Math.round(r.width), h: Math.round(r.height) };
    }
    return out;
  }, map);
  const activeName = (p) => p.$eval("[data-editor-tabs] .editor-tab.active .editor-tab-name", (el) => el.textContent);
  // A menu entry carries its state in the hidden attribute; inside a closed
  // dropdown it has no box, so this is the one place the attribute is the
  // honest measurement. What it is worth on screen is checked with the menu
  // open, in the checks that count the entries.
  const waitItemShown = (p, sel) => p.waitForFunction((x) => {
    const el = document.querySelector(x);
    return el && !el.hidden;
  }, sel, { timeout: 6000 });
  // The one menu: what a person really sees in it, with it open.
  const openMenu = async (p) => {
    // With nothing open the drawer opens by itself on a phone, and its backdrop
    // lies over the header until it is closed.
    if (await p.$(".editor.editor-drawer-open")) {
      await p.evaluate(() => document.querySelector("[data-editor-backdrop]").click());
      await p.waitForFunction(() => !document.querySelector(".editor.editor-drawer-open"), null, { timeout: 6000 });
    }
    await p.click("[data-editor-menu]");
    await p.waitForSelector("[data-editor-menu-list].show", { timeout: 4000 });
    await sleep(250);
  };
  const closeMenu = async (p) => {
    await p.keyboard.press("Escape");
    await p.waitForSelector("[data-editor-menu-list].show", { state: "detached", timeout: 4000 }).catch(() => {});
    await sleep(150);
  };
  const menuEntries = async (p) => {
    await openMenu(p);
    const rows = await p.$$eval("[data-editor-menu-list] .dropdown-item", (els) => els
      .filter((el) => el.getBoundingClientRect().height > 0)
      .map((el) => el.getAttributeNames().find((n) => n.startsWith("data-editor")) || el.className));
    await closeMenu(p);
    return rows;
  };
  // Everything a person can hit in the header, the strip counted as one. There
  // are two pane headers, one over the tree and one over the editor: this is the
  // one the strip sits in, hence the .editor-pane-col scope.
  const headerControls = (p) => p.$$eval(
    ".editor-pane-col > .editor-pane-header > button, .editor-pane-col > .editor-pane-header > .editor-tabs, .editor-pane-col > .editor-pane-header .editor-actions > button, .editor-pane-col > .editor-pane-header .editor-actions > a, .editor-pane-col > .editor-pane-header .editor-actions > .dropdown > button",
    (els) => els
      .filter((el) => {
        const r = el.getBoundingClientRect();
        return r.width > 0 && r.height > 0 && getComputedStyle(el).display !== "none";
      })
      .map((el) => el.getAttributeNames().find((n) => n.startsWith("data-editor")) || el.className.split(" ")[0]),
  );
  const openFilesSheet = async (p) => {
    await openMenu(p);
    await p.click("[data-editor-files-item]");
    await p.waitForSelector(".editor-sheet-row", { timeout: 5000 });
    await sleep(250);
  };
  // Opens a file on the phone through the tree drawer, the only way there.
  const openOnPhone = async (mp, path) => {
    if (!(await mp.$(".editor.editor-drawer-open"))) {
      await mp.click("[data-editor-drawer-toggle]");
      await mp.waitForSelector(".editor.editor-drawer-open", { timeout: 6000 });
    }
    await mp.waitForSelector(`.editor-file[data-path="${path}"]`, { timeout: 8000 });
    await mp.click(`.editor-file[data-path="${path}"]`);
    await mp.waitForSelector(`${tabSel(path)}.active`, { state: "attached", timeout: 10000 });
    await sleep(400);
  };
  const sheetRows = (mp) => mp.$$eval(".editor-sheet-row", (els) => els.map((el) => el.dataset.path));
  // Opens a file through the palette and leaves nothing of it standing: when the
  // file is already the active one the tab wait passes at once, and a palette
  // still open then covers the whole editor, the menu button included.
  const openViaPalette = async (target, name) => {
    await target.keyboard.press("Control+o");
    await target.waitForSelector("[data-editor-quickopen]:not([hidden])", { timeout: 4000 });
    await target.fill("[data-editor-quickopen-input]", name);
    await sleep(300);
    await target.keyboard.press("Enter");
    await target.waitForSelector(`${tabSel(name)}.active`, { state: "attached", timeout: 8000 });
    if (await target.$("[data-editor-quickopen]:not([hidden])")) {
      await target.keyboard.press("Escape");
    }
    await target.waitForSelector("[data-editor-quickopen]", { state: "hidden", timeout: 4000 });
    await sleep(300);
  };
  // A finger on the grip handle, the gesture quicknav uses for the same job.
  const dragSheetRow = async (mp, path, toIndex) => {
    await mp.evaluate(async ([sel, want]) => {
      const rows = [...document.querySelectorAll(".editor-sheet-row")];
      const row = document.querySelector(sel);
      const grip = row.querySelector("[data-editor-sheet-handle]");
      const r = grip.getBoundingClientRect();
      const x = Math.round(r.left + r.width / 2);
      const y0 = Math.round(r.top + r.height / 2);
      const raw = rows[want].getBoundingClientRect().top - row.getBoundingClientRect().top;
      // The drag starts measuring where it crossed the threshold, so the first
      // step is spent on that and the rest has to carry the whole distance.
      const lift = Math.sign(raw) * 8;
      const dy = Math.round(raw + lift + Math.sign(raw) * 10);
      const send = (type, y) => grip.dispatchEvent(new PointerEvent(type, {
        bubbles: true, cancelable: true, pointerId: 41, pointerType: "touch", isPrimary: true,
        clientX: x, clientY: y, buttons: type === "pointerup" ? 0 : 1,
      }));
      send("pointerdown", y0);
      send("pointermove", Math.round(y0 + lift));
      await new Promise((done) => setTimeout(done, 16));
      for (let i = 1; i <= 10; i++) {
        send("pointermove", Math.round(y0 + lift + ((dy - lift) * i) / 10));
        await new Promise((done) => setTimeout(done, 16));
      }
      send("pointerup", y0 + dy);
    }, [`.editor-sheet-row[data-path="${path}"]`, toIndex]);
    await sleep(400);
  };
  // A horizontal drag over the editor surface. Returns whether the editor took
  // the gesture (it cancels the move it acts on), which is what tells a swipe
  // that switched files from one the surface kept for its own scrolling.
  const swipeSurface = (mp, dx) => mp.evaluate(async (travel) => {
    const el = document.querySelector("[data-editor-surface]");
    const r = el.getBoundingClientRect();
    const y = Math.round(r.top + r.height / 2);
    const x0 = Math.round(r.left + r.width / 2);
    let taken = false;
    const send = (type, x) => {
      const event = new PointerEvent(type, {
        bubbles: true, cancelable: true, pointerId: 31, pointerType: "touch", isPrimary: true,
        clientX: x, clientY: y, buttons: type === "pointerup" ? 0 : 1,
      });
      if (!el.dispatchEvent(event)) taken = true;
    };
    send("pointerdown", x0);
    for (let i = 1; i <= 10; i++) {
      send("pointermove", Math.round(x0 + (travel * i) / 10));
      await new Promise((done) => setTimeout(done, 16));
    }
    send("pointerup", Math.round(x0 + travel));
    return taken;
  }, dx).then(async (taken) => {
    await sleep(500);
    return taken;
  });
  const setWrap = async (mp, on) => {
    await mp.evaluate((want) => {
      const box = document.querySelector('[data-editor-setting="line_wrap"]');
      if (box.checked !== want) box.click();
    }, on);
    await sleep(400);
  };
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

    await run("statusbar shows the cursor position as line colon column", async () => {
      const pos = await page.textContent("[data-editor-pos]");
      assert(/^\d+:\d+$/.test(pos || ""), `unexpected position readout: ${pos}`);
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
      // Double Shift opens the palette like Ctrl+O; a Shift chord must not,
      // and a held Shift is not a tap.
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
      await sleep(350);
      assert(await page.$("[data-editor-quickopen][hidden]"), "two slow Shift taps outside the window opened the palette");
      await page.keyboard.down("Shift");
      await sleep(450);
      await page.keyboard.up("Shift");
      await page.keyboard.press("Shift");
      await sleep(350);
      assert(await page.$("[data-editor-quickopen][hidden]"), "a held Shift then a tap opened the palette");
      await page.keyboard.press("Shift");
      await sleep(80);
      await page.keyboard.down("Shift");
      await sleep(450);
      assert(await page.$("[data-editor-quickopen][hidden]"), "a held second press opened the palette");
      await page.keyboard.up("Shift");
      await sleep(200);
      assert(await page.$("[data-editor-quickopen][hidden]"), "a late second keyup opened the palette");
      await page.keyboard.press("Shift");
      await sleep(80);
      await page.keyboard.down("Shift");
      await sleep(80);
      assert(await page.$("[data-editor-quickopen][hidden]"), "the palette opened on the second keydown already");
      await page.keyboard.up("Shift");
      await page.waitForSelector("[data-editor-quickopen]:not([hidden])", { timeout: 6000 });
      await page.keyboard.press("Escape");
      await page.waitForSelector("[data-editor-quickopen][hidden]", { state: "attached", timeout: 6000 });
    });

    await run("find in files searches contents and jumps to the match", async () => {
      await clickItem("[data-editor-search-project-item]");
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
      assert(/^1:1$/.test(pos || ""), `cursor not on the match line: ${pos}`);
    });

    await run("a hit far down the file scrolls it into view, from the content search and from name:line", async () => {
      // The check above matches on line 1, where there is nothing to scroll to.
      // This one puts the hit at line 300 of 400, which is what caught the
      // viewport staying put while the cursor moved.
      const longFile = `jump_${tag}.txt`;
      const needle = `deepneedle${tag}`;
      const hitLine = 300;
      await page.evaluate(async ([project, path, n, marker]) => {
        const token = document.querySelector('meta[name="csrf-token"]').content;
        const body = Array.from({ length: 400 }, (_, i) => (i + 1 === n ? marker : `filler ${i + 1}`)).join("\n");
        await fetch(`/projects/${project}/editor/create`, {
          method: "POST",
          headers: { "X-CSRF-Token": token, "Content-Type": "application/x-www-form-urlencoded" },
          body: "path=" + encodeURIComponent(path),
        });
        await fetch(`/projects/${project}/editor/file`, {
          method: "POST",
          headers: { "X-CSRF-Token": token, "Content-Type": "application/x-www-form-urlencoded" },
          body: "path=" + encodeURIComponent(path) + "&content=" + encodeURIComponent(body),
        });
      }, [project, longFile, hitLine, needle]);

      const onLine = async (n) => page.waitForFunction(
        (want) => /^(\d+):/.exec(document.querySelector("[data-editor-pos]")?.textContent || "")?.[1] === String(want),
        n, { timeout: 8000 });
      // A viewport that never moved sits at zero; the cursor alone proves nothing.
      const scrolled = async () => page.waitForFunction(
        () => (document.querySelector(".cm-editor .cm-scroller")?.scrollTop ?? 0) > 0,
        null, { timeout: 8000 });
      const closeTab = async (name) => {
        await page.evaluate((sel) => document.querySelector(`${sel} .editor-tab-state`)?.click(), tabSel(name));
        await page.waitForFunction((sel) => !document.querySelector(sel), tabSel(name), { timeout: 6000 });
      };

      // Content search, opening the file fresh: the case that was broken.
      await clickItem("[data-editor-search-project-item]");
      await page.waitForSelector("[data-editor-quickopen]:not([hidden])", { timeout: 6000 });
      await page.fill("[data-editor-quickopen-input]", needle);
      await page.waitForSelector(".editor-quickopen-match", { timeout: 8000 });
      await page.keyboard.press("Enter");
      await page.waitForSelector(`${tabSel(longFile)}.active`, { timeout: 8000 });
      await onLine(hitLine);
      await scrolled();

      // Same file, same jump, through the file palette's :line suffix. Closed
      // first so the editor has to open it from nothing again.
      await closeTab(longFile);
      const askLine = 120;
      await page.keyboard.press("Control+O");
      await page.waitForSelector("[data-editor-quickopen]:not([hidden])", { timeout: 6000 });
      await page.fill("[data-editor-quickopen-input]", `${longFile}:${askLine}`);
      await page.waitForSelector(".editor-quickopen-item", { timeout: 6000 });
      // The suffix is not part of the path, so the file still has to be found.
      const first = await page.textContent(".editor-quickopen-item .editor-quickopen-name");
      assert(first === longFile, `the :line suffix leaked into the match: ${first}`);
      await page.keyboard.press("Enter");
      await page.waitForSelector(`${tabSel(longFile)}.active`, { timeout: 8000 });
      await onLine(askLine);
      await scrolled();

      // Without a suffix the file opens at the top, the jump is opt-in.
      await closeTab(longFile);
      await page.keyboard.press("Control+O");
      await page.waitForSelector("[data-editor-quickopen]:not([hidden])", { timeout: 6000 });
      await page.fill("[data-editor-quickopen-input]", longFile);
      await page.waitForSelector(".editor-quickopen-item", { timeout: 6000 });
      await page.keyboard.press("Enter");
      await page.waitForSelector(`${tabSel(longFile)}.active`, { timeout: 8000 });
      await onLine(1);

      // Leave the strip as it was found: the checks after this one count tabs.
      await closeTab(longFile);
      await page.click(tabSel(noteFile));
      await page.waitForSelector(`${tabSel(noteFile)}.active`, { timeout: 6000 });
      return `content search to line ${hitLine}, palette to line ${askLine}, both scrolled`;
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

    // The preview is a per file switch in the file's own context menu, on its
    // tab and on its tree row, like the git ones; the editor menu has no entry
    // for it.
    await run("markdown preview renders server side and toggles off from the file's menu", async () => {
      await page.click(tabSel(noteFile));
      await page.click(".cm-content");
      await page.keyboard.press("Control+End").catch(() => {});
      await page.keyboard.type("\n\n# PreviewTitle" + tag);
      assert(!(await page.$("[data-editor-preview-item]")), "the editor menu still carries a preview entry");
      await openRowMenu(page, tabSel(noteFile));
      await menuItem(page, "Show preview").click();
      await page.waitForFunction((t) => {
        const pane = document.querySelector("[data-editor-preview-pane]");
        return pane && !pane.hidden && /PreviewTitle/.test(pane.textContent) && pane.querySelector("h1");
      }, tag, { timeout: 8000 });
      // It rides on the tab, so the stored state carries it and the menu of a
      // file without a preview does not offer it at all.
      const stored = await page.evaluate((key) => JSON.parse(localStorage.getItem(key) || "{}"), `dc-editor-tabs:${project}`);
      const entry = (stored.open || []).find((e) => e && e.path === noteFile);
      assert(entry && entry.preview === true, `the preview is not on the tab entry: ${JSON.stringify(stored.open)}`);
      await openRowMenu(page, tabSel(noteFile));
      await menuItem(page, "Hide preview").click();
      await page.waitForFunction(() => document.querySelector("[data-editor-preview-pane]").hidden, null, { timeout: 6000 });
      await clickItem("[data-editor-save-all]");
      await waitDirty(noteFile, false);
    });

    await run("find in file opens the styled search panel", async () => {
      await page.click(tabSel(noteFile));
      await clickItem("[data-editor-find-item]");
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
        // Its own context, so its own localStorage: the update dialog is
        // unseen here and would swallow every click behind its backdrop.
        await L.dismissUpdate(p2);
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
      // What a single file can do lives in the tab's menu, not in the editor
      // menu: an image tab is a file like any other and can be downloaded.
      await openRowMenu(page, tabSel(`pix_${tag}.png`));
      const download = page.locator(".dc-context-menu .dropdown-item", { hasText: /^Download$/ });
      assert(await download.count() === 1 && !(await download.isDisabled()), "the tab menu does not offer Download for an image");
      await page.keyboard.press("Escape");
      await page.waitForSelector(".dc-context-menu", { state: "detached", timeout: 4000 });
      const raw = await page.evaluate(async (p) => {
        const res = await fetch(location.pathname.replace(/\/editor$/, "/editor/raw") + `?path=${encodeURIComponent(p)}&download=1`);
        return { status: res.status, disposition: res.headers.get("Content-Disposition") || "" };
      }, `pix_${tag}.png`);
      assert(raw.status === 200 && raw.disposition.includes("attachment"), `raw download wrong: ${JSON.stringify(raw)}`);
      await page.click(`.editor-file[data-path="pic_${tag}.svg"] .editor-item-name`);
      await page.waitForSelector(tabSel(`pic_${tag}.svg`), { timeout: 6000 });
      await openRowMenu(page, tabSel(`pic_${tag}.svg`));
      await menuItem(page, "Show preview").click();
      await page.waitForSelector("[data-editor-preview-pane]:not([hidden]) img", { state: "attached", timeout: 6000 });
      await openRowMenu(page, tabSel(`pic_${tag}.svg`));
      await menuItem(page, "Hide preview").click();
      await openRowMenu(page, tabSel(`pic_${tag}.svg`));
      await menuItem(page, "Delete").click();
      await confirmSwal(page);
      await page.waitForFunction((p) => !document.querySelector(`.editor-file[data-path="${p}"]`), `pic_${tag}.svg`, { timeout: 8000 });
      await page.click(tabSel(`pix_${tag}.png`) + " .editor-tab-state");
      await page.waitForFunction((p) => !document.querySelector(`.editor-tab[data-path="${p}"]`), `pix_${tag}.png`, { timeout: 6000 });
    });

    await run("search settings: the Search tab holds the folder exclusions and they reach the palette", async () => {
      // The exclusions are install-wide, so this leaves the setting the way it
      // found it before it returns.
      const before = await page.evaluate(async () => {
        const html = await fetch("/settings/editor/search", { headers: { Accept: "text/html" } }).then((r) => r.text());
        return new DOMParser().parseFromString(html, "text/html").querySelector('[name="exclusions"]').value;
      });

      // The bare path and the sidebar's Editor row lead to the leftmost tab,
      // which is Search; Git is the tab next to it.
      await page.goto(`${BASE}/settings/editor`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      assert(/\/settings\/editor\/search$/.test(page.url()), `the bare path landed on ${page.url()}`);
      const tabs = await page.locator("[data-editor-sections] .nav-link").evaluateAll((els) => els.map((e) => e.getAttribute("href")));
      assert(tabs.join() === "/settings/editor/search,/settings/editor/git,/settings/editor/lsp", `the tabs are ${tabs.join(", ")}`);
      const active = await page.locator("[data-editor-sections] .nav-link.active").getAttribute("href");
      assert(active === "/settings/editor/search", `the marked tab is ${active}`);
      assert(await page.$('[data-settings-nav] a[href="/settings/editor/search"].active'), "the Editor row is not marked in the settings nav");
      assert(await page.locator("#settings-editor-search").count() === 1, "there is not exactly one form");

      // A folder excluded here disappears from the palette without a restart:
      // the index notices the setting changed and rebuilds itself.
      const excluded = "zz_excluded";
      // create makes the folders on the way, file would need them to be there.
      const created = await page.evaluate(async ([project, dir]) => {
        const token = document.querySelector('meta[name="csrf-token"]').content;
        const res = await fetch(`/projects/${project}/editor/create`, {
          method: "POST",
          headers: { "X-CSRF-Token": token, "Content-Type": "application/x-www-form-urlencoded" },
          body: "path=" + encodeURIComponent(`${dir}/buried.txt`),
        });
        return res.status;
      }, [project, excluded]);
      assert(created === 200, `creating the file answered ${created}`);

      const paletteHas = async (needle) => page.evaluate(async ([project, q]) => {
        const data = await fetch(`/projects/${project}/editor/files?q=${encodeURIComponent(q)}`, { headers: { Accept: "application/json" } }).then((r) => r.json());
        return (data.files || []).some((f) => f.includes(q));
      }, [project, needle]);

      assert(await paletteHas("buried.txt"), "the file is not findable before it is excluded");

      await page.fill('#settings-editor-search [name="exclusions"]', `.git\n${excluded}`);
      await Promise.all([
        page.waitForResponse((r) => r.url().includes("/settings/editor/search") && r.request().method() === "POST", { timeout: 15000 }),
        page.click('#settings-editor-search button[type="submit"]'),
      ]);
      await page.waitForLoadState("domcontentloaded");
      const stored = await page.inputValue('#settings-editor-search [name="exclusions"]');
      assert(stored === `.git\n${excluded}`, `the form came back with ${JSON.stringify(stored)}`);

      await page.goto(editorURL, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      assert(!(await paletteHas("buried.txt")), "the excluded folder still shows up in the palette");

      // Put the setting back so the rest of the run sees what it expected.
      await page.goto(`${BASE}/settings/editor/search`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      await page.fill('#settings-editor-search [name="exclusions"]', before);
      await Promise.all([
        page.waitForResponse((r) => r.url().includes("/settings/editor/search") && r.request().method() === "POST", { timeout: 15000 }),
        page.click('#settings-editor-search button[type="submit"]'),
      ]);
      await page.goto(editorURL, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      assert(await paletteHas("buried.txt"), "the folder did not come back after the setting was restored");
      return "search tab first, exclusions round-trip, index follows without a restart";
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

    // A save carries the version the file was read with, so a buffer can never
    // land on a file somebody else wrote in the meantime. Both out of band
    // writes below go through the save route with no version, which is exactly
    // what the create path is and what a coder's write looks like from here:
    // the editor's own state is untouched by them.
    const outOfBand = (path, content) => page.evaluate(async ([base, p, c]) => {
      const token = document.querySelector('meta[name="csrf-token"]').getAttribute("content");
      const res = await fetch(`${base}/file`, {
        method: "POST",
        headers: { "Content-Type": "application/x-www-form-urlencoded", "X-CSRF-Token": token, Accept: "application/json" },
        body: new URLSearchParams({ path: p, content: c }).toString(),
      });
      return res.status;
    }, [`/projects/${encodeURIComponent(project)}/editor`, path, content]);
    const conflictFile = `conflict_${tag}.txt`;
    // Swal hides the buttons a dialog does not use with an inline display, so
    // the count of exactly two ways out is read off what is really shown.
    const dialogButtons = () => page.$$eval(".swal2-actions button", (els) => els
      .filter((el) => getComputedStyle(el).display !== "none")
      .map((el) => el.textContent.trim()));
    const diskText = (path) => page.evaluate(async ([base, p]) => {
      const res = await fetch(`${base}/file?path=${encodeURIComponent(p)}`, { headers: { Accept: "application/json" } });
      return (await res.json()).content;
    }, [`/projects/${encodeURIComponent(project)}/editor`, path]);

    await run("a file written behind the editor's back refuses the save and Cancel keeps the buffer", async () => {
      // The check before this one confirmed a dialog, and a raw right click
      // lands on the backdrop while it closes: treeRootMenu drives the mouse
      // by coordinates, so it cannot wait the way a locator click does.
      await page.waitForSelector(".swal2-container", { state: "detached", timeout: 4000 }).catch(() => {});
      await newFile(conflictFile);
      await page.click(".cm-content"); await page.keyboard.type("mine");
      await waitDirty(conflictFile, true);
      assert((await outOfBand(conflictFile, "theirs")) === 200, "the out of band write did not land");
      await page.keyboard.press("Control+S");
      await page.waitForSelector(".swal2-confirm", { state: "visible", timeout: 8000 });
      const title = await page.textContent(".swal2-title");
      assert(/changed on disk/.test(title || ""), `the conflict dialog says ${title}`);
      const buttons = await dialogButtons();
      assert(buttons.length === 2 && buttons.includes("Reload"), `the dialog offers ${buttons.join(", ")}`);
      await page.click(".swal2-cancel");
      await page.waitForSelector(".swal2-container", { state: "detached", timeout: 4000 }).catch(() => {});
      // Nothing written, and the buffer is still the one that was typed.
      await waitDirty(conflictFile, true);
      assert((await page.textContent(".cm-content")).includes("mine"), "the cancelled save changed the buffer");
      const disk = await diskText(conflictFile);
      assert(disk === "theirs", `the cancelled save wrote ${JSON.stringify(disk)}`);
    });

    await run("Reload replaces the buffer with the server state and the next save writes", async () => {
      await page.click(".cm-content");
      await page.keyboard.press("Control+S");
      await confirmSwal(page);
      await page.waitForFunction((sel) => document.querySelector(sel).textContent.includes("theirs"), ".cm-content", { timeout: 8000 });
      await waitDirty(conflictFile, false);
      // The reload brought the fresh version with it, so this one goes through
      // without a second dialog.
      await page.click(".cm-content");
      await page.keyboard.press("Control+End");
      await page.keyboard.type(" plus mine");
      await waitDirty(conflictFile, true);
      await page.keyboard.press("Control+S");
      await waitDirty(conflictFile, false);
      assert(!(await page.$(".swal2-confirm")), "the save after the reload asked again");
    });

    await run("a deleted file gets its own dialog and Create again writes it back", async () => {
      await page.click(".cm-content"); await page.keyboard.type(" and more");
      await waitDirty(conflictFile, true);
      const gone = await page.evaluate(async ([base, p]) => {
        const token = document.querySelector('meta[name="csrf-token"]').getAttribute("content");
        const res = await fetch(`${base}/delete`, {
          method: "POST",
          headers: { "Content-Type": "application/x-www-form-urlencoded", "X-CSRF-Token": token, Accept: "application/json" },
          body: new URLSearchParams({ path: p }).toString(),
        });
        return res.status;
      }, [`/projects/${encodeURIComponent(project)}/editor`, conflictFile]);
      assert(gone === 200, `the out of band delete answered ${gone}`);
      await page.click(".cm-content");
      await page.keyboard.press("Control+S");
      await page.waitForSelector(".swal2-confirm", { state: "visible", timeout: 8000 });
      const title = await page.textContent(".swal2-title");
      assert(/no longer exists/.test(title || ""), `the deleted dialog says ${title}`);
      const buttons = await dialogButtons();
      assert(buttons.length === 2 && buttons.includes("Create again"), `the deleted dialog offers ${buttons.join(", ")}`);
      assert(!buttons.includes("Reload"), "the deleted dialog offers a reload of a file that is gone");
      await page.click(".swal2-confirm");
      await waitDirty(conflictFile, false);
      const disk = await diskText(conflictFile);
      assert(/and more$/.test(disk || ""), `the recreated file holds ${JSON.stringify(disk)}`);
      // Its version came back with it, so the next save asks nothing either.
      await page.click(".cm-content");
      await page.keyboard.press("Control+End");
      await page.keyboard.type("!");
      await waitDirty(conflictFile, true);
      await page.keyboard.press("Control+S");
      await waitDirty(conflictFile, false);
      assert(!(await page.$(".swal2-confirm")), "the save after the recreate asked again");
      // The tree never heard about the out of band delete, so its row is the
      // one the recreate made valid again.
      await openRowMenu(page, `.editor-file[data-path="${conflictFile}"]`);
      await menuItem(page, "Delete").click();
      await confirmSwal(page);
      await page.waitForFunction((f) => !document.querySelector(`.editor-file[data-path="${f}"]`), conflictFile, { timeout: 8000 });
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

    // A paste has no row under a pointer, so it takes the folder the tree has
    // selected and runs the drop's path from there: same walk, same dialog.
    await run("pasting files uploads them into the selected folder", async () => {
      const name = `pasted-${tag.slice(-4)}.txt`;
      await page.click('.editor-dir[data-path="spring"]');
      await sleep(300);
      const carried = await page.evaluate((n) => {
        const data = new DataTransfer();
        data.items.add(new File(["pasted into the tree\n"], n, { type: "text/plain" }));
        if (!data.files.length) return false;
        document.querySelector("dc-editor [data-editor-tree]")
          .dispatchEvent(new ClipboardEvent("paste", { clipboardData: data, bubbles: true, cancelable: true }));
        return true;
      }, name);
      assert(carried, "the engine builds no file clipboard");
      await page.waitForSelector(`.editor-file[data-path="spring/${name}"]`, { timeout: 15000 });
      const content = await page.evaluate(async ([p, path]) => {
        const r = await fetch(`/projects/${p}/editor/raw?path=${encodeURIComponent(path)}&download=1`);
        return r.text();
      }, [project, `spring/${name}`]);
      assert(content.trim() === "pasted into the tree", `pasted content: ${content}`);

      // Text keeps its own way through: the editor must not swallow a plain
      // paste and must not upload anything for it.
      const before = await page.$$eval(".editor-file", (els) => els.length);
      const swallowed = await page.evaluate(() => {
        const data = new DataTransfer();
        data.setData("text/plain", "just text");
        const event = new ClipboardEvent("paste", { clipboardData: data, bubbles: true, cancelable: true });
        document.querySelector("dc-editor [data-editor-tree]").dispatchEvent(event);
        return event.defaultPrevented;
      });
      await sleep(600);
      assert(!swallowed, "a text paste was taken by the upload path");
      assert((await page.$$eval(".editor-file", (els) => els.length)) === before, "a text paste changed the tree");
      return "into the selected folder, text untouched";
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

    await run("Ctrl+Shift+X closes the active tab, a dirty one asks first", async () => {
      await newFile(`cx_${tag}.txt`);
      await page.click(".cm-content");
      await page.keyboard.type("unsaved");
      await waitDirty(`cx_${tag}.txt`, true);
      await page.keyboard.down("Control");
      await page.keyboard.down("Shift");
      await page.keyboard.press("x");
      await page.keyboard.up("Shift");
      await page.keyboard.up("Control");
      await page.waitForSelector(".swal2-popup", { timeout: 6000 });
      assert(/discard/i.test(await page.textContent(".swal2-popup")), "no discard wording in the shortcut close confirm");
      await confirmSwal(page);
      await page.waitForFunction((s) => !document.querySelector(s), tabSel(`cx_${tag}.txt`), { timeout: 6000 });
      await page.waitForSelector(".swal2-container", { state: "detached", timeout: 4000 }).catch(() => {});
      await sleep(400);
      await page.click(tabSel(`ct2_${tag}.txt`));
      await page.waitForSelector(`${tabSel(`ct2_${tag}.txt`)}.active`, { timeout: 6000 });
    });

    await run("mouse drag reorders the tabs and the order survives a reload", async () => {
      const before = await tabOrder();
      assert(before.length >= 2, `need two tabs to drag: ${before}`);
      await dragLastTabToFront(before);
      const after = await tabOrder();
      const expected = [before[before.length - 1], ...before.slice(0, -1)];
      assert(JSON.stringify(after) === JSON.stringify(expected), `drag did not reorder: ${after} != ${expected}`);
      // The drag release must not switch the active tab.
      assert(await page.$(`${tabSel(`ct2_${tag}.txt`)}.active`), "drag changed the active tab");
      await page.reload({ waitUntil: "domcontentloaded" });
      await page.waitForSelector(".cm-editor", { state: "attached", timeout: 12000 });
      await page.waitForSelector("[data-editor-tabs] .editor-tab", { timeout: 8000 });
      assert(JSON.stringify(await tabOrder()) === JSON.stringify(expected), "reordered tabs did not survive the reload");
    });

    // One editor on both widths: the strip stands, and the options that used to
    // be seven icons next to it are in the one menu.
    await run("at 1440 the strip stands and the header carries only the strip, the menu and a due Save", async () => {
      await page.setViewportSize({ width: 1440, height: 900 });
      // Its own page and its own two tabs: this is about the width, so it must
      // not inherit whatever the checks before it left open. What it opens it
      // closes again, the strip is shared with the checks that follow and one
      // of them needs empty space in it.
      await page.goto(editorURL, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(".cm-editor", { state: "attached", timeout: 12000 });
      const opened = [];
      for (const name of [noteFile, qoFile]) {
        if (!(await page.$(tabSel(name)))) opened.push(name);
        await openViaPalette(page, name);
      }
      const strip = await boxes(page, { strip: "[data-editor-tabs]" });
      assert(strip.strip.display !== "none" && strip.strip.w > 0, `the strip is not laid out at 1440: ${JSON.stringify(strip.strip)}`);
      assert(JSON.stringify(await headerControls(page)) === JSON.stringify(["data-editor-drawer-toggle", "data-editor-tabs", "data-editor-menu"]),
        `the header at 1440 shows ${(await headerControls(page)).join(", ")}`);

      const before = await tabOrder();
      await dragLastTabToFront(before);
      const expected = [before[before.length - 1], ...before.slice(0, -1)];
      assert(JSON.stringify(await tabOrder()) === JSON.stringify(expected), "the mouse drag stopped reordering at 1440");
      // Through a reload, not straight after the drag: the strip swallows the
      // one click after a drag (that is what keeps a drop from switching tabs),
      // and with the pointer moved that far the browser sends no click of its
      // own to consume it, so the close control would be ignored once.
      if (opened.length) {
        await page.reload({ waitUntil: "domcontentloaded" });
        await page.waitForSelector(".cm-editor", { state: "attached", timeout: 12000 });
        await sleep(600);
        for (const name of opened) {
          await page.evaluate((sel) => document.querySelector(`${sel} .editor-tab-state`).click(), tabSel(name));
          await page.waitForFunction((sel) => !document.querySelector(sel), tabSel(name), { timeout: 6000 });
        }
      }
      await page.setViewportSize({ width: 1360, height: 900 });
      await sleep(400);
      return `strip ${strip.strip.w}px, three controls in the header`;
    });

    await run("a due Save is the only control the header gains, and it goes with the save", async () => {
      await page.goto(editorURL, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(".cm-editor", { state: "attached", timeout: 12000 });
      await openViaPalette(page, noteFile);
      await page.click(".cm-content", { force: true });
      await page.keyboard.type("save me");
      await page.waitForFunction(() => document.querySelector("[data-editor-save]").getBoundingClientRect().width > 0, null, { timeout: 6000 });
      assert(JSON.stringify(await headerControls(page)) === JSON.stringify(["data-editor-drawer-toggle", "data-editor-tabs", "data-editor-save", "data-editor-menu"]),
        `the header with an unsaved file shows ${(await headerControls(page)).join(", ")}`);
      await page.click("[data-editor-save]");
      await page.waitForFunction(() => document.querySelector("[data-editor-save]").getBoundingClientRect().width === 0, null, { timeout: 8000 });
      assert(JSON.stringify(await headerControls(page)) === JSON.stringify(["data-editor-drawer-toggle", "data-editor-tabs", "data-editor-menu"]),
        `Save stayed after the save: ${(await headerControls(page)).join(", ")}`);
    });

    // The gesture is touch only. This drives a real mouse, not a synthetic
    // PointerEvent that could claim any pointerType it likes: press on the
    // surface, cross it far past the distance that commits a swipe, release.
    // Wrapping is on, so nothing but the pointer type stands between the drag
    // and a file switch.
    await run("a mouse drag across the editor surface never switches the file", async () => {
      const wrapWas = await page.$eval('[data-editor-setting="line_wrap"]', (el) => el.checked);
      const setWrapHere = async (on) => {
        if (await page.$eval('[data-editor-setting="line_wrap"]', (el) => el.checked) === on) return;
        await page.evaluate(() => document.querySelector('[data-editor-setting="line_wrap"]').click());
        await sleep(400);
      };
      await setWrapHere(true);
      // A second file, so a swipe would have somewhere to go: this check is
      // about the pointer type, not about how many tabs the checks before it
      // happened to leave open.
      if ((await tabOrder()).length < 2) await openViaPalette(page, qoFile);
      const open = await tabOrder();
      assert(open.length >= 2, `the drag needs a file it could switch to: ${open.join(", ")}`);
      const before = await page.$eval("[data-editor-tabs] .editor-tab.active", (el) => el.dataset.path);
      const box = await page.locator("[data-editor-surface]").boundingBox();
      const y = Math.round(box.y + box.height / 2);
      const from = Math.round(box.x + box.width - 40);
      const travel = Math.round(box.width - 80);
      await page.mouse.move(from, y);
      await page.mouse.down();
      for (let i = 1; i <= 12; i++) {
        await page.mouse.move(Math.round(from - (travel * i) / 12), y, { steps: 2 });
        await sleep(20);
      }
      await page.mouse.up();
      await sleep(600);
      const after = await page.$eval("[data-editor-tabs] .editor-tab.active", (el) => el.dataset.path);
      assert(after === before, `a mouse drag switched the file: ${before} -> ${after}`);
      // It must not even have started: a running gesture pushes the surface
      // along under the pointer and puts the target's name in a pill.
      const trace = await page.evaluate(() => ({
        transform: document.querySelector("[data-editor-surface]").style.transform,
        pill: !!document.querySelector("[data-editor-swipe-pill]"),
      }));
      assert(!trace.transform && !trace.pill, `the mouse drag armed the swipe: ${JSON.stringify(trace)}`);
      // Leave no selection behind for the checks that follow.
      await page.mouse.click(Math.round(box.x + 40), y);
      await sleep(200);
      await setWrapHere(wrapWas);
      return `${travel}px across, still on ${before.split("/").pop()}`;
    });

    await run("fullscreen: button toggles and persists, Ctrl+Shift+Enter and strip double-click toggle too", async () => {
      const waitFullscreen = (want) => page.waitForFunction(
        (w) => document.documentElement.classList.contains("dc-editor-fullscreen") === w, want, { timeout: 6000 });
      await clickItem("[data-editor-fullscreen]");
      await waitFullscreen(true);
      assert(await page.$eval("[data-editor-fullscreen]", (e) => e.getAttribute("aria-pressed") === "true"), "button not pressed after enabling");
      assert(await page.$eval("[data-editor-fullscreen] i", (e) => e.className.includes("ti-minimize")), "entry icon did not switch to minimize");
      assert(await page.isVisible(".editor-back"), "back button not visible in fullscreen");
      await page.reload({ waitUntil: "domcontentloaded" });
      await page.waitForSelector(".cm-editor", { state: "attached", timeout: 12000 });
      await waitFullscreen(true);
      await page.keyboard.press("Control+Shift+Enter");
      await waitFullscreen(false);
      const box = await page.locator("[data-editor-tabs]").boundingBox();
      await page.mouse.dblclick(box.x + box.width - 8, box.y + box.height / 2);
      await waitFullscreen(true);
      await clickItem("[data-editor-fullscreen]");
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
      await clickItem("[data-editor-fullscreen]");
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
      await clickItem("[data-editor-fullscreen]");
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
      await mp.waitForSelector(`${tabSel(noteFile)}.active`, { state: "attached", timeout: 8000 });
      await mp.waitForFunction(() => !document.querySelector(".editor.editor-drawer-open"), null, { timeout: 6000 });
      await mp.click("[data-editor-drawer-toggle]");
      await mp.waitForSelector(".editor.editor-drawer-open", { timeout: 6000 });
      await mp.click("[data-editor-backdrop]");
      await mp.waitForFunction(() => !document.querySelector(".editor.editor-drawer-open"), null, { timeout: 6000 });
    });

    // The strip's touch gestures are checked where a coarse pointer and a
    // drawer meet, a tablet: the same strip stands on the phone, and its own
    // checks are further down.
    await run("touch tablet: tapping the active tab and long-pressing any tab open the context menu", async () => {
      const ctx = await browser.newContext({ ignoreHTTPSErrors: true, hasTouch: true, isMobile: true, viewport: { width: 700, height: 900 } });
      const tp = await ctx.newPage();
      L.wirePage(tp, bag);
      try {
        await L.login(tp);
        await tp.goto(editorURL, { waitUntil: "domcontentloaded" });
        await L.dismissUpdate(tp);
        await tp.waitForSelector(".cm-editor", { state: "attached", timeout: 12000 });
        await openOnPhone(tp, qoFile);
        await openOnPhone(tp, noteFile);
        assert(await tp.isVisible("[data-editor-tabs]"), "the tab strip is folded away on a tablet");
        await tp.tap(`${tabSel(noteFile)} .editor-tab-name`);
        await tp.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
        await menuItem(tp, "Close").click();
        await tp.waitForFunction((s) => !document.querySelector(s), tabSel(noteFile), { timeout: 6000 });
        await tp.waitForSelector(`${tabSel(qoFile)}.active`, { timeout: 6000 });
        await tp.evaluate((sel) => {
          const el = document.querySelector(sel);
          const rect = el.getBoundingClientRect();
          el.dispatchEvent(new PointerEvent("pointerdown", {
            bubbles: true, cancelable: true, pointerId: 7, pointerType: "touch",
            clientX: rect.left + 12, clientY: rect.top + 12, buttons: 1,
          }));
        }, tabSel(qoFile));
        await tp.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
        await tp.evaluate((sel) => {
          const el = document.querySelector(sel);
          const rect = el.getBoundingClientRect();
          el.dispatchEvent(new PointerEvent("pointerup", {
            bubbles: true, cancelable: true, pointerId: 7, pointerType: "touch",
            clientX: rect.left + 12, clientY: rect.top + 12,
          }));
        }, tabSel(qoFile));
        const labels = await tp.$$eval(".dc-context-menu .dropdown-item", (els) => els.map((e) => e.textContent.trim()));
        assert(labels.includes("Close all"), `long-press menu misses entries: ${labels.join(", ")}`);
        await tp.keyboard.press("Escape");
        await tp.waitForSelector(".dc-context-menu", { state: "detached", timeout: 4000 });
      } finally {
        await ctx.close();
      }
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

    const mobFile = `mob_${tag}.txt`;

    await run("mobile: at 390 the strip stands and the header carries the folder, the strip and the menu", async () => {
      const mp = await mobilePage();
      await openOnPhone(mp, noteFile);
      await openOnPhone(mp, qoFile);
      const strip = await boxes(mp, { strip: "[data-editor-tabs]" });
      assert(strip.strip.display !== "none" && strip.strip.w > 0 && strip.strip.h > 0,
        `the strip is not laid out at 390: ${JSON.stringify(strip.strip)}`);
      const tabs = await mp.$$eval("[data-editor-tabs] .editor-tab", (els) => els.length);
      assert(tabs === 3, `the phone strip holds ${tabs} tabs`);
      assert(JSON.stringify(await headerControls(mp)) === JSON.stringify(["data-editor-drawer-toggle", "data-editor-tabs", "data-editor-menu"]),
        `the header at 390 shows ${(await headerControls(mp)).join(", ")}`);
      return `strip ${strip.strip.w}px, three controls in the header`;
    });

    // Fullscreen is the one entry the two widths do not share: a phone has no
    // window around the page to grow out of, so the switch stays away there.
    await run("mobile: the menu carries the same entries as the wide screen, minus fullscreen", async () => {
      const mp = await mobilePage();
      // The same file open on both, so a file bound entry cannot make the two
      // lists differ for a reason that has nothing to do with the width. The
      // desktop page may stand on another project by now, the switcher check
      // takes it there.
      await openOnPhone(mp, noteFile);
      await page.goto(editorURL, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(".cm-editor", { state: "attached", timeout: 12000 });
      await openViaPalette(page, noteFile);
      const phone = await menuEntries(mp);
      const desktop = await menuEntries(page);
      assert(!phone.includes("data-editor-fullscreen"), `the phone menu offers fullscreen: ${phone.join(", ")}`);
      assert(desktop.includes("data-editor-fullscreen"), `the wide menu lost fullscreen: ${desktop.join(", ")}`);
      // Fullscreen and the terminal panel are the two deliberate desktop-only
      // entries: a phone has no window to grow out of and no panel either.
      assert(!phone.includes("data-editor-term-item"), `the phone menu offers the terminal panel: ${phone.join(", ")}`);
      assert(desktop.includes("data-editor-term-item"), `the wide menu lost the terminal panel: ${desktop.join(", ")}`);
      const want = desktop.filter((entry) => entry !== "data-editor-fullscreen" && entry !== "data-editor-term-item");
      assert(JSON.stringify(phone) === JSON.stringify(want),
        `the menus differ beyond fullscreen and terminal:\n  390:  ${phone.join(", ")}\n  1360: ${desktop.join(", ")}`);
      for (const entry of ["data-editor-files-item", "data-editor-quick-open-item", "data-editor-find-item",
        "data-editor-search-project-item", "data-editor-settings-item"]) {
        assert(phone.includes(entry), `the menu misses ${entry}: ${phone.join(", ")}`);
      }
      // The file actions belong to the file: the tab menu and the tree row
      // carry them, the editor menu keeps only what acts on the editor.
      for (const gone of ["data-editor-copy-path", "data-editor-download", "data-editor-rename", "data-editor-delete"]) {
        assert(!desktop.includes(gone), `the editor menu still carries ${gone}: ${desktop.join(", ")}`);
      }
      assert(desktop.includes("data-editor-save-all"), `the editor menu lost save all: ${desktop.join(", ")}`);
      return `${phone.length} entries on the phone, ${desktop.length} wide`;
    });

    await run("mobile: the sheet comes from the menu, switches, closes and reorders, and the order survives a reload", async () => {
      const mp = await mobilePage();
      await openFilesSheet(mp);
      assert(JSON.stringify(await sheetRows(mp)) === JSON.stringify([mobFile, noteFile, qoFile]),
        `the sheet lists ${(await sheetRows(mp)).join(", ")}`);
      await mp.click(`.editor-sheet-row[data-path="${mobFile}"] .editor-sheet-open`);
      await mp.waitForSelector(`${tabSel(mobFile)}.active`, { state: "attached", timeout: 6000 });
      const closed = await boxes(mp, { sheet: "[data-editor-sheet]" });
      assert(closed.sheet.display === "none" && closed.sheet.h === 0, `the sheet stayed open after the switch: ${JSON.stringify(closed.sheet)}`);

      await openFilesSheet(mp);
      await dragSheetRow(mp, qoFile, 0);
      const reordered = [qoFile, mobFile, noteFile];
      assert(JSON.stringify(await sheetRows(mp)) === JSON.stringify(reordered),
        `the grip did not reorder: ${(await sheetRows(mp)).join(", ")}`);
      assert(JSON.stringify(await mp.$$eval("[data-editor-tabs] .editor-tab", (els) => els.map((el) => el.dataset.path))) === JSON.stringify(reordered),
        "the strip behind the sheet kept the old order");

      await mp.click(`.editor-sheet-row[data-path="${noteFile}"] [data-editor-sheet-close-tab]`);
      await mp.waitForFunction((sel) => !document.querySelector(`.editor-sheet-row[data-path="${sel}"]`), noteFile, { timeout: 6000 });
      assert(!(await mp.$(`[data-editor-tabs] .editor-tab[data-path="${noteFile}"]`)), "closing in the sheet left the tab open");

      await mp.reload({ waitUntil: "domcontentloaded" });
      await mp.waitForSelector(".cm-editor", { state: "attached", timeout: 15000 });
      await sleep(800);
      await openFilesSheet(mp);
      assert(JSON.stringify(await sheetRows(mp)) === JSON.stringify([qoFile, mobFile]),
        `the order did not survive the reload: ${(await sheetRows(mp)).join(", ")}`);
      await mp.click("[data-editor-sheet-close]");
      await mp.waitForFunction(() => document.querySelector("[data-editor-sheet]").getBoundingClientRect().height === 0, null, { timeout: 4000 });
    });

    await run("mobile: an unsaved file brings Save into the header and takes it away again", async () => {
      const mp = await mobilePage();
      let header = await headerControls(mp);
      assert(!header.includes("data-editor-save"), `Save stands with nothing to save: ${header.join(", ")}`);
      await mp.click(".cm-content", { force: true });
      await mp.keyboard.type("phone edit");
      await mp.waitForFunction(() => document.querySelector("[data-editor-save]").getBoundingClientRect().width > 0, null, { timeout: 6000 });
      header = await headerControls(mp);
      assert(JSON.stringify(header) === JSON.stringify(["data-editor-drawer-toggle", "data-editor-tabs", "data-editor-save", "data-editor-menu"]),
        `the header with an unsaved file shows ${header.join(", ")}`);
      // The sheet says the same about that file.
      await openFilesSheet(mp);
      const open = await mp.$eval("[data-editor-tabs] .editor-tab.active", (el) => el.dataset.path);
      const row = await boxes(mp, { dot: `.editor-sheet-row[data-path="${open}"] [data-editor-sheet-dirty]` });
      assert(row.dot && row.dot.w > 0, "the row of the unsaved file carries no dot");
      await mp.click("[data-editor-sheet-close]");
      await sleep(300);
      await mp.click("[data-editor-save]");
      await mp.waitForFunction(() => document.querySelector("[data-editor-save]").getBoundingClientRect().width === 0, null, { timeout: 8000 });
    });

    await run("mobile: a swipe changes the file and wraps at both ends, but only while lines wrap", async () => {
      const mp = await mobilePage();
      const order = await mp.$$eval("[data-editor-tabs] .editor-tab", (els) => els.map((el) => el.dataset.path));
      assert(order.length === 2, `the phone should hold two files here: ${order.join(", ")}`);
      const names = order.map((path) => path.split("/").pop());
      await openFilesSheet(mp);
      await mp.click(`.editor-sheet-row[data-path="${order[0]}"] .editor-sheet-open`);
      await mp.waitForSelector(`${tabSel(order[0])}.active`, { state: "attached", timeout: 6000 });
      // A line the surface has to scroll for, so "the gesture belongs to the
      // code" is measurable and not just claimed.
      await mp.click(".cm-content", { force: true });
      await mp.keyboard.type(`${"scroll-me ".repeat(30)}`);
      await mp.waitForFunction(() => document.querySelector("[data-editor-save]").getBoundingClientRect().width > 0, null, { timeout: 6000 });
      await mp.click("[data-editor-save]");
      await sleep(800);

      // Who owns which gesture. The value has to sit on the scroller: the touch
      // hits a .cm-line, and a pan reads the value from there up to the element
      // that scrolls, so anything above .cm-scroller is never consulted. No pan
      // is left to the browser, which decides the axis at the first pixels and
      // answered every swipe with a downward drift with pointercancel; two
      // fingers stay its own, so pinching still zooms.
      await setWrap(mp, true);
      const owned = await mp.$eval(".cm-scroller", (el) => getComputedStyle(el).touchAction);
      assert(!/pan|auto/.test(owned), `the scroller still leaves a pan to the browser: ${owned}`);
      assert(/pinch-zoom/.test(owned), `pinching no longer zooms: ${owned}`);
      await setWrap(mp, false);
      assert(await mp.$eval(".cm-scroller", (el) => getComputedStyle(el).touchAction) === "auto",
        "the scroller keeps the swipe's axis rule with wrapping off, where the code needs it");

      const before = await activeName(mp);
      const takenOff = await swipeSurface(mp, -180);
      assert(await activeName(mp) === before, `the swipe switched to ${await activeName(mp)} with wrapping off`);
      assert(!takenOff, "the editor took the horizontal gesture away from the surface with wrapping off");
      const scroll = await mp.evaluate(() => {
        const s2 = document.querySelector(".cm-scroller");
        s2.scrollLeft = 160;
        return { left: Math.round(s2.scrollLeft), over: s2.scrollWidth > s2.clientWidth + 8 };
      });
      assert(scroll.over && scroll.left > 0, `the surface cannot scroll sideways there: ${JSON.stringify(scroll)}`);
      await mp.evaluate(() => { document.querySelector(".cm-scroller").scrollLeft = 0; });

      await setWrap(mp, true);
      const takenOn = await swipeSurface(mp, -180);
      assert(takenOn, "the editor did not take the gesture with wrapping on");
      assert(await activeName(mp) === names[1], `the swipe did not move on: ${await activeName(mp)}`);
      // Around at both ends, the way Ctrl+Tab and the terminal swipe do it.
      await swipeSurface(mp, -180);
      assert(await activeName(mp) === names[0], `the swipe past the last file did not wrap: ${await activeName(mp)}`);
      await swipeSurface(mp, 180);
      assert(await activeName(mp) === names[1], `the swipe past the first file did not wrap: ${await activeName(mp)}`);
      await swipeSurface(mp, 180);
      assert(await activeName(mp) === names[0], `the swipe back did not return: ${await activeName(mp)}`);
      await swipeSurface(mp, -40);
      assert(await activeName(mp) === names[0], "a swipe under the threshold switched the file");
      return `${names.join(" / ")}, wrapping at both ends`;
    });

    // One pill for both swipes: same class, same place. The editor's sits where
    // the terminal's sits, fixed near the top of the viewport, not at the
    // bottom edge of the surface it belongs to.
    await run("mobile: the swipe pill is the terminal's pill, at the top of the viewport", async () => {
      const mp = await mobilePage();
      await setWrap(mp, true);
      const shown = await mp.evaluate(async () => {
        const el = document.querySelector("[data-editor-surface]");
        const r = el.getBoundingClientRect();
        const y = Math.round(r.top + r.height / 2);
        const x0 = Math.round(r.left + r.width / 2);
        const send = (type, x) => el.dispatchEvent(new PointerEvent(type, {
          bubbles: true, cancelable: true, pointerId: 61, pointerType: "touch", isPrimary: true,
          clientX: x, clientY: y, buttons: type === "pointerup" ? 0 : 1,
        }));
        send("pointerdown", x0);
        for (let i = 1; i <= 6; i++) {
          send("pointermove", x0 - i * 14);
          await new Promise((done) => setTimeout(done, 16));
        }
        const pill = document.querySelector("[data-editor-swipe-pill]");
        const rect = pill ? pill.getBoundingClientRect() : null;
        const out = {
          shared: Boolean(pill && pill.classList.contains("dc-swipe-pill")),
          own: Boolean(pill && pill.classList.contains("editor-swipe-pill")),
          position: pill ? getComputedStyle(pill).position : "",
          top: rect ? Math.round(rect.top) : -1,
          half: Math.round(window.innerHeight / 2),
          name: pill ? pill.textContent.trim() : "",
        };
        send("pointerup", x0 - 84);
        return out;
      });
      await sleep(500);
      assert(shown.shared, `the pill does not carry the shared class: ${JSON.stringify(shown)}`);
      assert(!shown.own, "the pill still carries an editor-only class of its own");
      assert(shown.position === "fixed", `the pill is ${shown.position}, the terminal's is fixed`);
      assert(shown.top >= 0 && shown.top < shown.half, `the pill sits at ${shown.top}px, below the middle at ${shown.half}px`);
      assert(shown.name.length > 0, "the pill does not name the file it would go to");
      return `dc-swipe-pill at ${shown.top}px, "${shown.name}"`;
    });

    // The check above drives the gesture with synthetic pointer events, which
    // skip the browser's own scroll arbitration, and that arbitration is
    // exactly what broke this outside fullscreen. Chromium can be given a real
    // finger through CDP, so there the gesture is the real one.
    await run("mobile: a real finger swipes the file outside fullscreen (chromium)", async () => {
      if (engine !== "chromium") return "skipped, CDP is chromium only";
      const mp = await mobilePage();
      const cdp = await mp.context().newCDPSession(mp);
      await setWrap(mp, true);
      assert(!(await mp.evaluate(() => document.documentElement.classList.contains("dc-editor-fullscreen"))),
        "this has to run outside fullscreen, that is the case it is about");
      const drag = async (dx) => {
        const box = await mp.$eval("[data-editor-surface]", (el) => {
          const r = el.getBoundingClientRect();
          return { x: Math.round(r.left + r.width / 2), y: Math.round(r.top + r.height / 2) };
        });
        await mp.evaluate(() => {
          window.__cancelled = false;
          document.querySelector("[data-editor-surface]")
            .addEventListener("pointercancel", () => { window.__cancelled = true; }, { once: true });
        });
        await cdp.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ x: box.x, y: box.y, id: 1 }] });
        for (let i = 1; i <= 12; i++) {
          await cdp.send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ x: Math.round(box.x + (dx * i) / 12), y: box.y, id: 1 }] });
          await sleep(20);
        }
        await cdp.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
        await sleep(600);
        return mp.evaluate(() => window.__cancelled);
      };
      const order = await mp.$$eval("[data-editor-tabs] .editor-tab", (els) => els.map((el) => el.dataset.path.split("/").pop()));
      const first = await activeName(mp);
      const cancelled = await drag(-200);
      assert(!cancelled, "the browser took the gesture: pointercancel instead of the swipe");
      assert(await activeName(mp) !== first, `a real finger changed nothing: still ${await activeName(mp)}`);
      // Past the end it lands at the other end.
      await drag(-200);
      assert(await activeName(mp) === first, `the real swipe did not wrap around: ${await activeName(mp)}`);
      // And a vertical drag scrolls the text, not the page around it, and a
      // fast release keeps it moving: the browser hands both axes over now, so
      // this scroll is the editor's own. That needs a file long enough to
      // scroll at all, which a scratch file is not.
      if (!(await mp.evaluate(() => {
        const s2 = document.querySelector(".cm-scroller");
        return s2.scrollHeight > s2.clientHeight + 20;
      }))) {
        await mp.click(".cm-content", { force: true });
        await mp.keyboard.press("Control+End");
        for (let i = 0; i < 60; i++) await mp.keyboard.press("Enter");
        await mp.keyboard.type("bottom");
        await mp.waitForFunction(() => document.querySelector("[data-editor-save]").getBoundingClientRect().width > 0, null, { timeout: 6000 });
        await mp.click("[data-editor-save]");
        await sleep(800);
        await mp.evaluate(() => { document.querySelector(".cm-scroller").scrollTop = 0; });
        await sleep(300);
      }
      const box = await mp.$eval("[data-editor-surface]", (el) => {
        const r = el.getBoundingClientRect();
        return { x: Math.round(r.left + r.width / 2), y: Math.round(r.top + r.height / 2) };
      });
      const start = await mp.evaluate(() => ({ scroll: document.querySelector(".cm-scroller").scrollTop, page: window.scrollY }));
      await cdp.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ x: box.x, y: box.y, id: 2 }] });
      for (let i = 1; i <= 12; i++) {
        await cdp.send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ x: box.x, y: box.y - i * 15, id: 2 }] });
        await sleep(20);
      }
      await cdp.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
      const atRelease = await mp.evaluate(() => document.querySelector(".cm-scroller").scrollTop);
      await sleep(700);
      const after = await mp.evaluate(() => ({
        scroll: document.querySelector(".cm-scroller").scrollTop,
        page: window.scrollY,
        file: document.querySelector("[data-editor-tabs] .editor-tab.active").dataset.path.split("/").pop(),
      }));
      assert(after.scroll > start.scroll, `a vertical drag did not scroll the code: ${start.scroll} -> ${after.scroll}`);
      assert(after.page === start.page, `the page scrolled under the gesture: ${start.page} -> ${after.page}`);
      assert(after.scroll > atRelease, `the release did not keep the text moving: ${atRelease} -> ${after.scroll}`);
      assert(after.file === first, `a vertical drag switched the file to ${after.file}`);
      return `${order.join(" / ")}, real finger, no fullscreen, fling ${Math.round(after.scroll - atRelease)}px`;
    });

    // One header on both widths, measured the same way on both: what a person
    // can hit outside the menu, read off computed display and a real box. The
    // page has two pane headers, one over the tree and one over the editor;
    // this reads the one the tab strip sits in.
    await run("the header outside the menu is folder, strip and menu at 390 and at 1440, Save only when due", async () => {
      const mp = await mobilePage();
      const want = ["data-editor-drawer-toggle", "data-editor-tabs", "data-editor-menu"];
      const wantDirty = ["data-editor-drawer-toggle", "data-editor-tabs", "data-editor-save", "data-editor-menu"];
      const seen = {};
      for (const [label, target] of [["390", mp], ["1440", page]]) {
        if (label === "1440") {
          await page.setViewportSize({ width: 1440, height: 900 });
          await sleep(400);
        }
        await target.goto(editorURL, { waitUntil: "domcontentloaded" });
        await target.waitForSelector(".cm-editor", { state: "attached", timeout: 15000 });
        await sleep(800);
        if (await target.$(".editor.editor-drawer-open")) {
          await target.evaluate(() => document.querySelector("[data-editor-backdrop]").click());
          await sleep(300);
        }
        // A file open, and saved: Save has no business in the header then.
        await openViaPalette(target, noteFile);
        const clean = await headerControls(target);
        assert(JSON.stringify(clean) === JSON.stringify(want), `${label}: the header shows ${clean.join(", ")}`);

        await target.click(".cm-content", { force: true });
        await target.keyboard.type("x");
        await target.waitForFunction(() => document.querySelector("[data-editor-save]").getBoundingClientRect().width > 0, null, { timeout: 6000 });
        const dirty = await headerControls(target);
        assert(JSON.stringify(dirty) === JSON.stringify(wantDirty), `${label}: with an unsaved file the header shows ${dirty.join(", ")}`);
        await target.click("[data-editor-save]");
        await target.waitForFunction(() => document.querySelector("[data-editor-save]").getBoundingClientRect().width === 0, null, { timeout: 8000 });
        seen[label] = `${clean.length} / ${dirty.length}`;
      }
      await page.setViewportSize({ width: 1360, height: 900 });
      await sleep(400);
      return `390: ${seen["390"]}, 1440: ${seen["1440"]} controls`;
    });

    // Where the tree is a column the same button folds it away, and the fold
    // stays until it is folded back, a reload included.
    await run("the folder button folds the tree column away at 1440 and the fold survives a reload", async () => {
      await page.setViewportSize({ width: 1440, height: 900 });
      await sleep(400);
      try {
      const treeBox = () => boxes(page, { col: ".editor-tree-col", splitter: "[data-editor-splitter]", surface: "[data-editor-surface]" });
      const before = await treeBox();
      assert(before.col.display !== "none" && before.col.w > 0, `the tree column is not there to fold: ${JSON.stringify(before.col)}`);
      assert(before.splitter.w > 0, "the splitter is not there while the column stands");

      await page.click("[data-editor-drawer-toggle]");
      await sleep(500);
      const folded = await treeBox();
      assert(folded.col.display === "none" && folded.col.w === 0, `the column did not fold away: ${JSON.stringify(folded.col)}`);
      assert(folded.splitter.w === 0, "the splitter stayed without a column to drag");
      assert(folded.surface.w > before.surface.w, `the editor did not take the room: ${before.surface.w} -> ${folded.surface.w}`);

      await page.reload({ waitUntil: "domcontentloaded" });
      await page.waitForSelector(".cm-editor", { state: "attached", timeout: 15000 });
      await sleep(700);
      assert((await treeBox()).col.w === 0, "the fold did not survive the reload");

      await page.click("[data-editor-drawer-toggle]");
      await sleep(500);
      const back = await treeBox();
      assert(back.col.w > 0 && back.splitter.w > 0, `the column did not come back: ${JSON.stringify(back)}`);
      // The splitter still drags the column it brought back. Grabbed at its left
      // edge and moved in one step: the handle is six pixels wide with negative
      // margins, the pane beside it paints over its right half, and a pointer
      // that leaves it loses the capture, so a long multi-step drag measures the
      // handle's reach rather than whether resizing works.
      const handle = await page.locator("[data-editor-splitter]").boundingBox();
      const onHandle = await page.evaluate(([x, y]) => {
        const el = document.elementFromPoint(x, y);
        return !!el && el.hasAttribute("data-editor-splitter");
      }, [Math.round(handle.x + 1), Math.round(handle.y + handle.height / 2)]);
      assert(onHandle, "the splitter is not the element under its own left edge");
      await page.mouse.move(handle.x + 1, handle.y + handle.height / 2);
      await page.mouse.down();
      await page.mouse.move(handle.x + 41, handle.y + handle.height / 2);
      await sleep(100);
      await page.mouse.up();
      await sleep(400);
      const wider = await treeBox();
      assert(wider.col.w > back.col.w + 20, `the splitter stopped working: ${back.col.w} -> ${wider.col.w}`);
      return `${before.col.w}px column, folded to 0, back at ${back.col.w}px, splitter to ${wider.col.w}px`;
      } finally {
        // Whatever failed, the checks after this one get the page they expect.
        await page.evaluate(() => localStorage.removeItem("dc-editor-tree-width"));
        await page.setViewportSize({ width: 1360, height: 900 });
        await sleep(400);
      }
    });

    // A tree row carries draggable for the mouse drag that moves a file. On a
    // coarse pointer that is what hands the long press to the browser's own
    // drag lift, and iOS then never lets the press become the row's menu, so
    // the rows are draggable on a fine pointer only.
    await run("mobile: a long press on a tree row opens the same menu as a right click", async () => {
      const mp = await mobilePage();
      if (!(await mp.$(".editor.editor-drawer-open"))) {
        await mp.click("[data-editor-drawer-toggle]");
        await mp.waitForSelector(".editor.editor-drawer-open", { timeout: 6000 });
      }
      await mp.waitForSelector(`.editor-file[data-path="${noteFile}"]`, { timeout: 8000 });
      assert(await mp.$eval(`.editor-file[data-path="${noteFile}"]`, (el) => el.draggable === false),
        "a tree row is draggable on touch, the long press goes to the drag lift there");
      // The press the way a finger delivers it. WebKit has no Touch
      // constructor, so there it arrives through the pointer family, which is
      // the other path wireRowMenus arms.
      const family = await mp.evaluate((sel) => {
        const el = document.querySelector(sel);
        const r = el.getBoundingClientRect();
        const x = r.left + 20;
        const y = r.top + r.height / 2;
        try {
          // WebKit has the interface object but no constructor behind it, so
          // this throws there rather than reporting itself as missing.
          const touch = new Touch({ identifier: 71, target: el, clientX: x, clientY: y });
          el.dispatchEvent(new TouchEvent("touchstart", {
            bubbles: true, cancelable: true, touches: [touch], targetTouches: [touch], changedTouches: [touch],
          }));
          return "touch";
        } catch (error) {
          void error;
        }
        el.dispatchEvent(new PointerEvent("pointerdown", {
          bubbles: true, cancelable: true, pointerId: 71, pointerType: "touch", isPrimary: true,
          clientX: x, clientY: y, buttons: 1,
        }));
        return "pointer";
      }, `.editor-file[data-path="${noteFile}"]`);
      await mp.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
      const touchLabels = await mp.$$eval(".dc-context-menu .dropdown-item", (els) => els.map((e) => e.textContent.trim()));
      await mp.keyboard.press("Escape");
      await mp.waitForSelector(".dc-context-menu", { state: "detached", timeout: 4000 });
      // The same entries the mouse gets on the wide screen, from a page of this
      // check's own: the checks before it resize and reload that page, and a
      // tree rebuilt under the pointer takes the press with it.
      await page.goto(editorURL, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(".cm-editor", { state: "attached", timeout: 15000 });
      await page.waitForSelector(`.editor-file[data-path="${noteFile}"]`, { timeout: 10000 });
      await sleep(800);
      await openRowMenu(page, `.editor-file[data-path="${noteFile}"]`);
      const mouseLabels = await page.$$eval(".dc-context-menu .dropdown-item", (els) => els.map((e) => e.textContent.trim()));
      await page.keyboard.press("Escape");
      await page.waitForSelector(".dc-context-menu", { state: "detached", timeout: 4000 });
      assert(JSON.stringify(touchLabels) === JSON.stringify(mouseLabels),
        `the tree menu differs:\n  touch: ${touchLabels.join(", ")}\n  mouse: ${mouseLabels.join(", ")}`);
      await mp.evaluate(() => document.querySelector("[data-editor-backdrop]").click());
      await mp.waitForFunction(() => !document.querySelector(".editor.editor-drawer-open"), null, { timeout: 6000 });
      return `${touchLabels.length} entries, the same on both (${family} press)`;
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
