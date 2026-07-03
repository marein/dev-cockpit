const L = require("./lib");
const { assert, sleep } = L;

// Terminal: the shared attach interaction for sessions and shells. Both attach pages
// run the same session-attach + session-input, so typing, controls, copy, paste,
// scroll and refresh behave the same; the per surface files (shells.js, sessions.js)
// cover what differs. Two divergent paths, both checked here:
//   - Desktop (fine pointer): types straight into the xterm <canvas>; onData -> a
//     session-input raw event -> @dc/http POST /input, output echoes back. The only
//     on-screen control is refresh (.attach-desktop); the control/copy/paste toolbar
//     is .attach-mobile, display:none on desktop.
//   - Mobile (coarse pointer, matchMedia): read-only mirror, a hidden
//     #session-cursor-input at the cursor cell sends text, an .attach-cursor overlay
//     mirrors the cursor, and the .attach-mobile toolbar is the interaction surface
//     (control buttons + auto-repeat, ctrl modifier, copy mode, paste).
// Terminal paints to <canvas> (CanvasAddon, several stacked layers, no .xterm-rows);
// read text from the .attach-selection mirror. Uses a throwaway shell (safe target).
// Routes: /shells/:id, /shells/:id/input, /shells/:id/resize, /shells/:id/stream.

L.runFeature("TERMINAL", async ({ engine, page, run, mobilePage, bag }) => {
  const tag = `term-${engine}-${Date.now().toString(36)}`;
  const project = `zztc-${tag}`;
  let shellUrl = null;
  try {
    await L.createProject(page, project);
    shellUrl = await L.createShell(page, project);

    // ---------------- desktop (fine pointer) ----------------
    await run("desktop: attach custom elements upgraded + canvas", async () => {
      const missing = await L.waitUpgraded(page, ["session-attach", "session-input", "session-scroll-zone", "session-direction-pad", "session-terminal-setting-select"], 12000);
      assert(missing.length === 0, `not upgraded: ${missing}`);
      await page.waitForSelector("#session-terminal .xterm-screen canvas", { timeout: 10000 });
    });

    await run("desktop: canvas typing posts /input and output echoes into mirror", async () => {
      const marker = `TCP${tag.slice(-4)}`;
      await sleep(1400);
      await page.click("#session-terminal .xterm-screen");
      const reqP = page.waitForRequest((r) => /\/input$/.test(r.url()) && r.method() === "POST", { timeout: 8000 });
      await page.keyboard.type(`echo ${marker}`);
      await reqP;
      await page.keyboard.press("Enter");
      let text = "";
      for (let i = 0; i < 12; i++) { text = await page.evaluate(() => { const m = document.querySelector(".attach-selection"); return m ? m.textContent || "" : ""; }); if (text.includes(marker)) break; await sleep(400); }
      assert(text.includes(marker), `marker not mirrored (len ${text.length})`);
    });

    await run("desktop: terminal setting select persists font-size", async () => {
      const sel = await page.$("session-terminal-setting-select select");
      assert(sel, "no setting select");
      const opts = await sel.$$eval("option", (os) => os.map((o) => o.value));
      await sel.selectOption(opts.find((v) => v) || opts[0]);
      await sleep(300);
      assert(await page.evaluate(() => localStorage.getItem("session-terminal-font-size")) !== null, "font-size not persisted");
    });

    await run("desktop: refresh stream reconnects (no error)", async () => {
      const before = bag.consoleErrors.length + bag.pageErrors.length;
      await page.click(".attach-desktop [data-session-refresh]");
      await sleep(1000);
      assert(bag.consoleErrors.length + bag.pageErrors.length === before, "refresh errored");
    });

    await run("desktop: lifecycle teardown (remove terminal + input, no new errors)", async () => {
      const before = bag.consoleErrors.length + bag.pageErrors.length;
      await page.evaluate(() => { document.getElementById("session-terminal")?.remove(); document.querySelector("session-input")?.remove(); });
      await sleep(700);
      assert(bag.consoleErrors.length + bag.pageErrors.length === before, "teardown errored");
    });

    // ---------------- mobile (coarse pointer) ----------------
    const mp = await mobilePage();
    await mp.goto(shellUrl, { waitUntil: "domcontentloaded" });
    await mp.waitForSelector("#session-terminal .xterm-screen canvas", { timeout: 12000 });
    await sleep(1200);

    await run("mobile: coarse pointer -> mirror + cursor input + mobile toolbar", async () => {
      assert(await mp.evaluate(() => matchMedia("(pointer: coarse)").matches), "pointer not coarse");
      await mp.waitForSelector("#session-cursor-input", { timeout: 8000 });
      assert(await mp.$(".attach-cursor"), "no .attach-cursor overlay");
      assert(await mp.locator("[data-session-copy]").first().isVisible(), "mobile copy button not visible");
      assert(await mp.locator('[data-session-control="enter"]').first().isVisible(), "control buttons not visible");
    });

    await run("mobile: cursor-input typing sends text + mirror echoes", async () => {
      const marker = `MOB${tag.slice(-4)}`;
      await mp.evaluate(() => document.getElementById("session-cursor-input").focus());
      const reqP = mp.waitForRequest((r) => /\/input$/.test(r.url()) && r.method() === "POST", { timeout: 8000 });
      await mp.keyboard.type(`echo ${marker}`, { delay: 40 });
      await reqP;
      await mp.keyboard.press("Enter");
      let text = "";
      for (let i = 0; i < 12; i++) { text = await mp.evaluate(() => { const m = document.querySelector(".attach-selection"); return m ? m.textContent || "" : ""; }); if (text.includes(marker)) break; await sleep(400); }
      assert(text.includes(marker), `mirror did not echo (len ${text.length})`);
    });

    await run("mobile: control button (enter) posts a control", async () => {
      const reqP = mp.waitForRequest((r) => /\/input$/.test(r.url()) && r.method() === "POST", { timeout: 8000 });
      await mp.locator('[data-session-control="enter"]').first().click();
      assert(/enter/.test((await reqP).postData() || ""), "control body missing enter");
    });

    await run("mobile: Ctrl modifier arms then sends ctrl-<letter>", async () => {
      const ctrlBtn = mp.locator("[data-shell-ctrl]").first();
      await ctrlBtn.click();
      assert((await ctrlBtn.getAttribute("aria-pressed")) === "true", "ctrl did not arm");
      await mp.evaluate(() => document.getElementById("session-cursor-input").focus());
      const reqP = mp.waitForRequest((r) => /\/input$/.test(r.url()) && r.method() === "POST", { timeout: 8000 });
      await mp.keyboard.type("c", { delay: 40 });
      assert(/ctrl-c/.test((await reqP).postData() || ""), "expected ctrl-c");
      assert((await ctrlBtn.getAttribute("aria-pressed")) === "false", "ctrl did not disarm");
    });

    await run("mobile: copy mode toggles the selection mirror", async () => {
      const copyBtn = mp.locator("[data-session-copy]").first();
      await copyBtn.click();
      await mp.waitForSelector("#session-terminal.attach-terminal-copy-mode", { timeout: 4000 });
      assert((await mp.evaluate(() => (document.querySelector(".attach-selection") || {}).textContent || "")).trim().length > 0, "mirror empty in copy mode");
      await copyBtn.click();
    });

    await run("mobile: paste without clipboard shows the fallback toast", async () => {
      await mp.locator("[data-session-paste]").first().click();
      await mp.waitForFunction(() => /not available|clipboard/i.test(document.body.innerText), null, { timeout: 5000 });
    }, { soft: true });
  } finally {
    if (shellUrl) await L.deleteShell(page, shellUrl).catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
  }
});
