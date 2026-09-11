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

// The theme button is the one control that belongs to no feature: it stands in
// the rail's foot, in the phone's head menu and on the login page, and it walks
// one fixed ring, light, dark, follow the OS, whatever the OS itself says.
const RING = ["light", "dark", "auto"];
const RING_LABELS = { light: "Light", dark: "Dark", auto: "Follow the OS" };
const RAIL_CYCLE = ".dc-rail-foot dc-theme-cycle";
// The bell next to it is a dropdown too, so the menu is named by what only it
// carries, not by its place among the head tools.
const HEAD_MENU = ".dc-head-tools .dropdown:has([data-theme-cycle])";

// What one theme button shows and what the page made of it. The mark is read off
// the computed display, style.css is what turns the hidden attribute into one.
async function themeState(page, root) {
  return page.evaluate((sel) => {
    const host = document.querySelector(sel);
    if (!host) return { missing: true, shown: [] };
    const button = host.querySelector("[data-theme-cycle]");
    const label = host.querySelector("[data-theme-label]");
    let stored = null;
    try { stored = localStorage.getItem("dc-theme"); } catch (e) { /* blocked storage */ }
    return {
      shown: [...host.querySelectorAll("[data-theme-mark]")]
        .filter((m) => getComputedStyle(m).display !== "none")
        .map((m) => m.getAttribute("data-theme-mark")),
      stored,
      label: label ? label.textContent.trim() : null,
      title: button ? button.getAttribute("title") : "",
      aria: button ? button.getAttribute("aria-label") : "",
      dark: document.documentElement.getAttribute("data-bs-theme") === "dark",
    };
  }, root);
}

// One step of the ring, with the whole walk in the message: a mode that drops
// out of the round is only readable from the sequence that led there.
function assertMode(state, mode, walked, where) {
  const shown = state.shown.join("+") || "nothing";
  assert(state.shown.length === 1 && state.shown[0] === mode, `${where}: the button shows ${shown}, expected ${mode} (walked ${walked.join(" > ")})`);
  assert(state.stored === (mode === "auto" ? null : mode), `${where} ${mode}: dc-theme holds ${state.stored}`);
  assert(state.title.startsWith(`Theme: ${RING_LABELS[mode]}.`), `${where} ${mode}: the title reads ${state.title}`);
  assert(state.title === state.aria, `${where} ${mode}: title and aria-label differ (${state.title} / ${state.aria})`);
}

// A user toggle rides the 350ms dc-theme-flip, so a read waits it out.
const FLIP = 450;

L.runFeature("FRONTEND", async ({ page, run, mobilePage, bag }) => {
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
      // dc-form-modal sits in the layout of every signed in page, next to the
      // swapped region, so it upgrades here like the sheet does.
      assert((await L.waitUpgraded(page, ["dc-ctx-sheet", "dc-update-check", "dc-project-list", "dc-form-modal"], 8000)).length === 0, "not upgraded");
    });

    await run("marked text is opaque and carries 4.5:1, light and dark", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector("[data-project-filter]", { timeout: 8000 });
      let noted = false;
      for (const scheme of ["light", "dark"]) {
        await page.emulateMedia({ colorScheme: scheme });
        await sleep(300);
        for (const [what, sel] of [["page text", ".dc-project-name"], ["form field", "[data-project-filter]"]]) {
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

    await run("the theme button walks all three modes, on an OS in light and in dark", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(`${RAIL_CYCLE} [data-theme-cycle]`, { timeout: 8000 });
      for (const scheme of ["light", "dark"]) {
        await page.emulateMedia({ colorScheme: scheme });
        await page.evaluate(() => { try { localStorage.removeItem("dc-theme"); } catch (e) { /* blocked storage */ } });
        await page.reload({ waitUntil: "domcontentloaded" });
        await page.waitForSelector(`${RAIL_CYCLE} [data-theme-cycle]`, { timeout: 8000 });
        await sleep(300);
        // Two rounds from auto: a mode the ring never reaches falls out on the
        // first one, a ring that turns with the OS instead of on its own on the
        // second (it ran auto, dark, auto, dark under an OS in light mode).
        const walked = [];
        for (const mode of [...RING, ...RING]) {
          await page.click(`${RAIL_CYCLE} [data-theme-cycle]`);
          await sleep(FLIP);
          const state = await themeState(page, RAIL_CYCLE);
          walked.push(state.shown.join("+") || "nothing");
          assertMode(state, mode, walked, `OS ${scheme}`);
          const wantDark = mode === "dark" || (mode === "auto" && scheme === "dark");
          assert(state.dark === wantDark, `OS ${scheme} ${mode}: data-bs-theme dark is ${state.dark}, expected ${wantDark}`);
        }
      }
      await page.evaluate(() => { try { localStorage.removeItem("dc-theme"); } catch (e) { /* blocked storage */ } });
      await page.emulateMedia({ colorScheme: null });
      await sleep(200);
    });

    await run("a forced mode survives a reload, auto goes on following the OS", async () => {
      await page.emulateMedia({ colorScheme: "dark" });
      await page.evaluate(() => { try { localStorage.setItem("dc-theme", "light"); } catch (e) { /* blocked storage */ } });
      await page.reload({ waitUntil: "domcontentloaded" });
      await page.waitForSelector(`${RAIL_CYCLE} [data-theme-cycle]`, { timeout: 8000 });
      await sleep(300);
      let state = await themeState(page, RAIL_CYCLE);
      assertMode(state, "light", ["light"], "reload under an OS in dark");
      assert(!state.dark, "the reload took the OS scheme over the stored light");
      // Two clicks from light stand on auto, which takes its scheme from the OS
      // alone, in both directions and without another click.
      for (let i = 0; i < 2; i++) { await page.click(`${RAIL_CYCLE} [data-theme-cycle]`); await sleep(FLIP); }
      state = await themeState(page, RAIL_CYCLE);
      assertMode(state, "auto", ["dark", "auto"], "two clicks on from light");
      assert(state.dark, "auto did not take the OS dark scheme");
      await page.emulateMedia({ colorScheme: "light" });
      await sleep(300);
      state = await themeState(page, RAIL_CYCLE);
      assert(!state.dark, "auto did not follow the OS back to light");
      assertMode(state, "auto", ["auto"], "after the OS flip");
      await page.emulateMedia({ colorScheme: null });
      await sleep(200);
    });

    await run("the phone's menu row walks the same ring and names the mode", async () => {
      const mp = await mobilePage();
      await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await dismissUpdate(mp);
      await mp.emulateMedia({ colorScheme: "light" });
      await mp.evaluate(() => { try { localStorage.removeItem("dc-theme"); } catch (e) { /* blocked storage */ } });
      await mp.reload({ waitUntil: "domcontentloaded" });
      await mp.click(`${HEAD_MENU} > [data-bs-toggle="dropdown"]`);
      await mp.waitForSelector(`${HEAD_MENU} .dropdown-menu.show`, { timeout: 8000 });
      // The menu carries data-bs-auto-close="outside", so the row stays under
      // the finger for the whole round.
      const walked = [];
      for (const mode of RING) {
        await mp.click(`${HEAD_MENU} [data-theme-cycle]`);
        await sleep(FLIP);
        const state = await themeState(mp, `${HEAD_MENU} dc-theme-cycle`);
        walked.push(state.shown.join("+") || "nothing");
        assertMode(state, mode, walked, "phone menu");
        assert(state.label === RING_LABELS[mode], `phone menu ${mode}: the row reads ${state.label}`);
      }
      await mp.evaluate(() => { try { localStorage.removeItem("dc-theme"); } catch (e) { /* blocked storage */ } });
      await mp.emulateMedia({ colorScheme: null });
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
