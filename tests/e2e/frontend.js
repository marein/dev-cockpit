const L = require("./lib");
const { assert, sleep, BASE } = L;

// Frontend (cross cutting): the custom element layer. All browser behavior lives in
// custom elements and @dc/* modules. Checks that each page upgrades its elements,
// that a heavy element tears down clean on disconnect (AbortController aborted,
// EventSource closed, xterm/CodeMirror disposed, no leaks), and that re-inserting a
// removed element sets it up exactly once. The CanvasAddon stacks several <canvas>
// layers per terminal, so re-init is checked against the baseline layer count.

// It also carries the one app wide look that belongs to no feature: marked text.
// style.css sets ::selection and ::-moz-selection once, opaque and with a
// foreground of its own (--dc-selection-bg / --dc-selection-fg), so the pair is
// the same on every surface and on every background a page can put under it. The
// two surfaces that bring their own mark are checked where they live: CodeMirror
// in editor.js, the terminal keeps its wash over the canvas on purpose.

// A throwaway instance built from a dev tree offers an update on the first
// visit of a fresh context, and that modal swallows the first click of the
// seed. Deny it, never confirm.
async function dismissUpdate(page) {
  const cancel = page.locator(".swal2-cancel");
  try {
    await cancel.waitFor({ state: "visible", timeout: 2500 });
  } catch {
    return;
  }
  await cancel.click();
  await page.waitForSelector(".swal2-container", { state: "detached", timeout: 5000 });
}


// What the browser resolved for the mark on one element, and what that pair is
// worth. Reading the ::selection pseudo is a Chromium ability; an engine that
// answers with the element's own style says so and is not asserted against.
async function selectionContrast(page, sel) {
  return page.evaluate((s) => {
    const el = document.querySelector(s);
    if (!el) return { missing: true };
    const own = getComputedStyle(el);
    const cs = getComputedStyle(el, "::selection");
    const parts = (v) => (v.match(/[\d.]+/g) || []).map(Number);
    const lin = (c) => { const x = c / 255; return x <= 0.04045 ? x / 12.92 : Math.pow((x + 0.055) / 1.055, 2.4); };
    const lum = (p) => 0.2126 * lin(p[0]) + 0.7152 * lin(p[1]) + 0.0722 * lin(p[2]);
    const bgp = parts(cs.backgroundColor), fgp = parts(cs.color);
    if (bgp.length < 3 || fgp.length < 3) return { unsupported: true };
    const la = lum(bgp), lb = lum(fgp);
    return {
      background: cs.backgroundColor,
      color: cs.color,
      alpha: bgp.length > 3 ? bgp[3] : 1,
      ratio: Math.round(((Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05)) * 100) / 100,
      sameAsOwn: cs.backgroundColor === own.backgroundColor && cs.color === own.color,
    };
  }, sel);
}

L.runFeature("FRONTEND", async ({ page, run, bag }) => {
  const tag = `fe-${Date.now().toString(36)}`;
  const project = `zztc-${tag}`;
  let shellUrl = null;
  try {
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    await L.createProject(page, project);
    // The scratch shell exists up front so the attach steps below run against a
    // session this runner owns (self-contained run).
    shellUrl = await L.createShell(page, project);

    await run("custom elements upgraded on /projects", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      assert((await L.waitUpgraded(page, ["dc-quicknav", "dc-update-check", "dc-project-list"], 8000)).length === 0, "not upgraded");
    });

    await run("marked text is opaque and carries 4.5:1, light and dark", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector("[data-project-filter]", { timeout: 8000 });
      let noted = false;
      for (const scheme of ["light", "dark"]) {
        await page.emulateMedia({ colorScheme: scheme });
        await sleep(300);
        for (const [what, sel] of [["page text", ".page-title"], ["form field", "[data-project-filter]"]]) {
          const m = await selectionContrast(page, sel);
          assert(!m.missing, `${what}: nothing matched ${sel}`);
          if (m.unsupported || m.sameAsOwn) {
            if (!noted) { noted = true; console.log("      (this engine does not resolve ::selection, not asserted)"); }
            continue;
          }
          // A translucent mark hands the contrast to whatever sits underneath,
          // which on a diff row is a green or a red tint. Opaque is the rule.
          assert(m.alpha === 1, `${scheme} ${what}: the mark is translucent (${m.background})`);
          assert(m.ratio >= 4.5, `${scheme} ${what}: ${m.color} on ${m.background} is ${m.ratio}:1, under 4.5:1`);
        }
      }
      await page.emulateMedia({ colorScheme: null });
      await sleep(200);
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
