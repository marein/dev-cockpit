const L = require("./lib");
const { assert, sleep, BASE } = L;

// Frontend (cross cutting): the custom element layer. All browser behavior lives in
// custom elements and @dc/* modules. Checks that each page upgrades its elements,
// that a heavy element tears down clean on disconnect (AbortController aborted,
// EventSource closed, xterm/CodeMirror disposed, no leaks), and that re-inserting a
// removed element sets it up exactly once. The CanvasAddon stacks several <canvas>
// layers per terminal, so re-init is checked against the baseline layer count.

L.runFeature("FRONTEND", async ({ page, run, bag }) => {
  const tag = `fe-${Date.now().toString(36)}`;
  const project = `zztc-${tag}`;
  let shellUrl = null;
  try {
    await L.createProject(page, project);
    // dc-collapse-list renders only for a project with coders or shells, so the
    // scratch shell must exist before the /projects check (self-contained run).
    shellUrl = await L.createShell(page, project);

    await run("custom elements upgraded on /projects", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      assert((await L.waitUpgraded(page, ["dc-quicknav", "dc-update-check", "dc-project-list", "dc-collapse-list"], 8000)).length === 0, "not upgraded");
    });

    await run("custom elements upgraded on the editor", async () => {
      await page.goto(`${BASE}/projects/${encodeURIComponent(project)}/editor`, { waitUntil: "domcontentloaded" });
      assert((await L.waitUpgraded(page, ["dc-editor"], 10000)).length === 0, "dc-editor not upgraded");
    });

    await run("editor teardown on disconnect leaves no new errors", async () => {
      await page.waitForSelector(".cm-editor", { state: "attached", timeout: 12000 });
      const before = bag.consoleErrors.length + bag.pageErrors.length;
      await page.evaluate(() => document.querySelector("dc-editor").remove());
      await sleep(600);
      assert(bag.consoleErrors.length + bag.pageErrors.length === before, "teardown errored");
    });

    await run("attach elements upgraded on a shell", async () => {
      await page.goto(shellUrl, { waitUntil: "domcontentloaded" });
      assert((await L.waitUpgraded(page, ["terminal-attach", "terminal-input", "terminal-scroll-zone", "terminal-direction-pad", "terminal-setting-select"], 12000)).length === 0, "not upgraded");
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 10000 });
    });

    await run("re-init guard: remove + re-insert keeps one terminal, no leak", async () => {
      await sleep(800);
      const baseline = await page.locator("#terminal .xterm-screen canvas").count();
      const before = bag.consoleErrors.length + bag.pageErrors.length;
      await page.evaluate(() => { const el = document.getElementById("terminal"); const p = el.parentElement; el.remove(); p.appendChild(el); });
      await sleep(1200);
      assert(await page.locator("#terminal .xterm-screen canvas").count() === baseline, "canvas layer count changed (double setup or no re-init)");
      assert(bag.consoleErrors.length + bag.pageErrors.length === before, "re-insert errored");
      // functional: typing still reaches /input after the re-init
      await page.click("#terminal .xterm-screen");
      const reqP = page.waitForRequest((r) => /\/input$/.test(r.url()) && r.method() === "POST", { timeout: 8000 });
      await page.keyboard.type("echo reinit");
      await reqP;
    });

    await run("shell teardown on disconnect leaves no new errors", async () => {
      const before = bag.consoleErrors.length + bag.pageErrors.length;
      await page.evaluate(() => { document.getElementById("terminal")?.remove(); document.querySelector("terminal-input")?.remove(); });
      await sleep(700);
      assert(bag.consoleErrors.length + bag.pageErrors.length === before, "teardown errored");
    });
  } finally {
    if (shellUrl) await L.deleteShell(page, shellUrl).catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
  }
});
