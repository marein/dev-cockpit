const L = require("./lib");
const { assert, sleep, confirmSwal, BASE } = L;

// Editor: the per-project file editor. Custom element dc-editor; CodeMirror 6 loads
// through the layout import map (jsDelivr CDN), language packs are dynamic-imported
// by extension. A bare .editor-textarea means the CDN import map failed (highlight
// failure). Open files are tabs (.editor-tab, per-tab undo history, dirty dot,
// persisted per project in localStorage and restored on load); switching tabs never
// asks to discard, only closing a dirty tab does. The tree is a drawer on small
// screens (auto-open when no tab restores) and a drag-resizable column on wide
// ones. Routes: GET /projects/:name/editor(/list|/file|/files), POST .../file
// (save), .../create, .../mkdir, .../delete, .../rename, .../upload, .../preview.
// The toolbar buttons are wired only after init() awaits the CDN, so wait for
// .cm-editor before driving them; kebab menu items are clicked via evaluate so the
// bootstrap dropdown does not need to be opened first.

L.runFeature("EDITOR", async ({ page, run, mobilePage }) => {
  const tag = `edit-${Date.now().toString(36)}`;
  const project = `zztc-${tag}`;
  const editorURL = `${BASE}/projects/${encodeURIComponent(project)}/editor`;
  const noteFile = `note_${tag}.md`;
  const qoFile = `qo_${tag}.txt`;
  let lastDialog = null;
  page.on("dialog", async (d) => { try { if (d.type() !== "beforeunload") lastDialog = d.message(); await d.accept(); } catch {} });

  const tabSel = (path) => `.editor-tab[data-path="${path}"]`;
  const clickItem = (sel) => page.evaluate((s) => document.querySelector(s).click(), sel);
  const newFile = async (name) => {
    await page.click("[data-editor-new-file]");
    await page.fill(".swal2-input", name); await page.click(".swal2-confirm");
    await page.waitForSelector(tabSel(name), { timeout: 8000 });
  };
  const waitDirty = (path, on) =>
    page.waitForFunction(([sel, want]) => {
      const el = document.querySelector(sel);
      return !!el && el.classList.contains("dirty") === want;
    }, [tabSel(path), on], { timeout: 6000 });

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
      await page.click("[data-editor-new-folder]");
      await page.fill(".swal2-input", `keep_${tag}`);
      await page.click(".swal2-confirm");
      await page.waitForSelector(`.editor-dir[data-path="keep_${tag}"]`, { timeout: 8000 });
      await page.click(`.editor-dir[data-path="keep_${tag}"]`);
      await sleep(300);
      await page.click("[data-editor-new-file]");
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

    await run("rename from the tree updates the row and the open tab", async () => {
      const renamed = `renamed_${tag}.go`;
      await page.click(`.editor-file[data-path="main.go"] .editor-item-ren`);
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
      await page.click("[data-editor-new-folder]"); await page.fill(".swal2-input", "sub"); await page.click(".swal2-confirm");
      await page.waitForFunction(() => [...document.querySelectorAll(".editor-item-name")].some((e) => e.textContent === "sub"), null, { timeout: 8000 });
      await page.locator(".editor-dir", { has: page.locator(".editor-item-name", { hasText: /^sub$/ }) }).first().click(); await sleep(300);
      await page.click("[data-editor-new-file]"); await page.fill(".swal2-input", "inner.txt"); await page.click(".swal2-confirm");
      await page.waitForSelector('.editor-file[data-path="sub/inner.txt"]', { timeout: 8000 });
      await page.waitForSelector(tabSel("sub/inner.txt"), { timeout: 6000 });
      await page.locator(".editor-dir", { has: page.locator(".editor-item-name", { hasText: /^sub$/ }) }).first().locator(".editor-item-del").click();
      await confirmSwal(page);
      await page.waitForFunction(() => ![...document.querySelectorAll(".editor-item-name")].some((e) => e.textContent === "sub") && !document.querySelector('.editor-file[data-path="sub/inner.txt"]'), null, { timeout: 8000 });
      assert(!(await page.$(tabSel("sub/inner.txt"))), "tab of the deleted folder's file still open");
    });

    await run("delete a file drops it from the tree and closes its tab", async () => {
      const renamed = `renamed_${tag}.go`;
      await page.click(`.editor-file[data-path="${renamed}"] .editor-item-del`);
      await confirmSwal(page);
      await page.waitForFunction((f) => !document.querySelector(`.editor-file[data-path="${f}"]`), renamed, { timeout: 8000 });
      assert(!(await page.$(tabSel(renamed))), "tab of the deleted file still open");
    });

    await run("mobile: tree is a drawer, auto-open without tabs, closes on open", async () => {
      const mp = await mobilePage();
      await mp.goto(editorURL, { waitUntil: "domcontentloaded" });
      await mp.waitForSelector(".cm-editor", { state: "attached", timeout: 12000 });
      await mp.waitForSelector(".editor.editor-drawer-open", { timeout: 8000 });
      assert(await mp.isVisible("[data-editor-drawer-toggle]"), "drawer toggle not visible on mobile");
      await mp.click("[data-editor-new-file]");
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

    await run("lifecycle teardown (remove dc-editor, no new errors)", async ({} = {}) => {
      await page.evaluate(() => document.querySelector("dc-editor").remove());
      await sleep(600);
    });
  } finally {
    await L.deleteProject(page, project).catch(() => {});
  }
});
