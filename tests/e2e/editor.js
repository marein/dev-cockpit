const L = require("./lib");
const { assert, sleep, confirmSwal, BASE } = L;

// Editor: the per-project file editor. Custom element dc-editor; CodeMirror 6 loads
// through the layout import map (jsDelivr CDN), language packs are dynamic-imported
// by extension. A bare .editor-textarea means the CDN import map failed (highlight
// failure). Routes: GET /projects/:name/editor(/list|/file), POST .../file (save),
// .../create, .../mkdir, .../delete. The new-file/-folder buttons are wired only
// after init() awaits the CDN, so wait for .cm-editor before driving the toolbar.

L.runFeature("EDITOR", async ({ page, run }) => {
  const tag = `edit-${Date.now().toString(36)}`;
  const project = `zztc-${tag}`;
  const editorURL = `${BASE}/projects/${encodeURIComponent(project)}/editor`;
  const file = `note_${tag}.md`;
  let lastDialog = null;
  page.on("dialog", async (d) => { try { if (d.type() !== "beforeunload") lastDialog = d.message(); await d.accept(); } catch {} });

  try {
    await L.createProject(page, project);

    await run("mounts dc-editor + tree loads + CodeMirror ready", async () => {
      await page.goto(editorURL, { waitUntil: "domcontentloaded" });
      assert((await L.waitUpgraded(page, ["dc-editor"], 8000)).length === 0, "dc-editor not upgraded");
      await page.waitForSelector("[data-editor-tree]", { timeout: 8000 });
      await page.waitForSelector(".cm-editor", { timeout: 12000 });
      await page.waitForFunction(() => { const t = document.querySelector("[data-editor-tree]"); return t && !/Loading/.test(t.textContent); }, null, { timeout: 8000 });
    });

    await run("new file -> appears -> CodeMirror mounts (not textarea fallback)", async () => {
      await page.click("[data-editor-new-file]");
      await page.fill(".swal2-input", file); await page.click(".swal2-confirm");
      await page.waitForSelector(`.editor-file[data-path="${file}"]`, { timeout: 8000 });
      await page.waitForSelector(".cm-editor, .editor-textarea", { timeout: 10000 });
      assert(!(await page.$(".editor-textarea")), "CodeMirror fell back to textarea (CDN import map failed)");
    });

    await run("edit -> dirty badge -> save clears dirty", async () => {
      await page.click(".cm-content"); await page.keyboard.type("hello " + tag);
      await page.waitForFunction(() => { const d = document.querySelector("[data-editor-dirty]"); return d && !d.hidden; }, null, { timeout: 5000 });
      await page.click("[data-editor-save]");
      await page.waitForFunction(() => { const d = document.querySelector("[data-editor-dirty]"); return d && d.hidden; }, null, { timeout: 8000 });
    });

    await run("Ctrl+S saves the dirty buffer", async () => {
      await page.click(".cm-content"); await page.keyboard.type("\n// more");
      await page.waitForFunction(() => { const d = document.querySelector("[data-editor-dirty]"); return d && !d.hidden; }, null, { timeout: 5000 });
      await page.keyboard.press("Control+S");
      await page.waitForFunction(() => { const d = document.querySelector("[data-editor-dirty]"); return d && d.hidden; }, null, { timeout: 8000 });
    });

    await run("real language highlighting for a code file", async () => {
      await page.click("[data-editor-new-file]"); await page.fill(".swal2-input", "main.go"); await page.click(".swal2-confirm");
      await page.waitForSelector('.editor-file[data-path="main.go"]', { timeout: 8000 });
      await page.click(".cm-content"); await page.keyboard.type('package main\n\nfunc main() {}\n');
      let spans = 0; for (let i = 0; i < 30; i++) { spans = await page.locator(".cm-editor .cm-content span").count(); if (spans > 0) break; await sleep(300); }
      assert(spans > 0, "no highlight spans (Go language pack did not load from the CDN)");
      return `${spans} token spans`;
    }, { soft: true });

    await run("switching a dirty buffer fires the discard confirm then switches", async () => {
      await page.click(".cm-content"); await page.keyboard.type("\n// dirty");
      await page.waitForFunction(() => { const d = document.querySelector("[data-editor-dirty]"); return d && !d.hidden; }, null, { timeout: 5000 });
      lastDialog = null;
      await page.click(`.editor-file[data-path="${file}"]`);
      await page.waitForFunction((f) => new RegExp(f).test((document.querySelector("[data-editor-current]") || {}).textContent || ""), file, { timeout: 6000 });
      assert(lastDialog && /discard/i.test(lastDialog), `no discard confirm (saw: ${lastDialog})`);
    });

    await run("settings persist to localStorage", async () => {
      assert(await page.evaluate(() => { const el = document.querySelector('[data-editor-setting="font_size"]'); if (!el) return false; el.value = "18"; el.dispatchEvent(new Event("change", { bubbles: true })); return true; }), "no font_size control");
      await sleep(250);
      assert(String((await page.evaluate(() => JSON.parse(localStorage.getItem("dc-editor-settings") || "{}"))).font_size) === "18", "font_size not persisted");
    });

    await run("new folder + recursive delete drops it and its descendants", async () => {
      await page.click("[data-editor-new-folder]"); await page.fill(".swal2-input", "sub"); await page.click(".swal2-confirm");
      await page.waitForFunction(() => [...document.querySelectorAll(".editor-item-name")].some((e) => e.textContent === "sub"), null, { timeout: 8000 });
      await page.locator(".editor-dir", { has: page.locator(".editor-item-name", { hasText: /^sub$/ }) }).first().click(); await sleep(300);
      await page.click("[data-editor-new-file]"); await page.fill(".swal2-input", "inner.txt"); await page.click(".swal2-confirm");
      await page.waitForSelector('.editor-file[data-path="sub/inner.txt"]', { timeout: 8000 });
      await page.locator(".editor-dir", { has: page.locator(".editor-item-name", { hasText: /^sub$/ }) }).first().locator(".editor-item-del").click();
      await confirmSwal(page);
      await page.waitForFunction(() => ![...document.querySelectorAll(".editor-item-name")].some((e) => e.textContent === "sub") && !document.querySelector('.editor-file[data-path="sub/inner.txt"]'), null, { timeout: 8000 });
    });

    await run("delete a file drops it from the tree", async () => {
      await page.click(`.editor-file[data-path="${file}"] .editor-item-del`);
      await confirmSwal(page);
      await page.waitForFunction((f) => !document.querySelector(`.editor-file[data-path="${f}"]`), file, { timeout: 8000 });
    });

    await run("lifecycle teardown (remove dc-editor, no new errors)", async ({} = {}) => {
      await page.evaluate(() => document.querySelector("dc-editor").remove());
      await sleep(600);
    });
  } finally {
    await L.deleteProject(page, project).catch(() => {});
  }
});
