const L = require("./lib");
const { assert, sleep } = L;

// Terminal: the shared attach interaction for sessions and shells. Both attach pages
// run the same terminal-attach + terminal-input, so typing, controls, copy, paste,
// scroll and refresh behave the same; the per surface files (shells.js, sessions.js)
// cover what differs. Two divergent paths, both checked here:
//   - Desktop (fine pointer): types straight into the xterm <canvas>; onData -> a
//     terminal-input raw event -> @dc/http POST /input, output echoes back. The only
//     on-screen control is refresh (.attach-desktop); the control/copy/paste toolbar
//     is .attach-mobile, display:none on desktop.
//   - Mobile (coarse pointer, matchMedia): read-only mirror, a hidden
//     #terminal-cursor-input at the cursor cell sends text, an .attach-cursor overlay
//     mirrors the cursor, and the .attach-mobile toolbar is the interaction surface
//     (control buttons + auto-repeat, ctrl modifier, copy mode, paste). Swipe
//     scrolling on the terminal-scroll-zone overlay: proportional drag + fling.
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
      const missing = await L.waitUpgraded(page, ["terminal-attach", "terminal-input", "terminal-scroll-zone", "terminal-direction-pad", "terminal-setting-select"], 12000);
      assert(missing.length === 0, `not upgraded: ${missing}`);
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 10000 });
    });

    await run("desktop: canvas typing posts /input and output echoes into mirror", async () => {
      const marker = `TCP${tag.slice(-4)}`;
      await sleep(1400);
      await page.click("#terminal .xterm-screen");
      const reqP = page.waitForRequest((r) => /\/input$/.test(r.url()) && r.method() === "POST", { timeout: 8000 });
      await page.keyboard.type(`echo ${marker}`);
      await reqP;
      await page.keyboard.press("Enter");
      let text = "";
      for (let i = 0; i < 12; i++) { text = await page.evaluate(() => { const m = document.querySelector(".attach-selection"); return m ? m.textContent || "" : ""; }); if (text.includes(marker)) break; await sleep(400); }
      assert(text.includes(marker), `marker not mirrored (len ${text.length})`);
    });

    await run("desktop: terminal setting select persists font-size", async () => {
      const root = 'terminal-setting-select[setting="font-size"]';
      const toggle = await page.$(`${root} .dropdown-toggle`);
      assert(toggle, "no setting dropdown");
      await toggle.click();
      const item = await page.waitForSelector(`${root} .dropdown-menu.show .dropdown-item:not(.active)`, { timeout: 4000 });
      const value = await item.evaluate((el) => el.dataset.value);
      await item.click();
      await sleep(300);
      assert(await page.evaluate(() => localStorage.getItem("dc-terminal-font-size")) === value, "font-size not persisted");
      assert((await page.$eval(`${root} .dropdown-toggle span`, (el) => el.textContent)) === value, "toggle label not updated");
    });

    await run("desktop: legacy storage key migrates to dc- on read", async () => {
      await page.evaluate(() => { localStorage.removeItem("dc-terminal-font-size"); localStorage.setItem("session-terminal-font-size", "18"); });
      await page.reload({ waitUntil: "domcontentloaded" });
      await sleep(800);
      const st = await page.evaluate(() => ({ fresh: localStorage.getItem("dc-terminal-font-size"), legacy: localStorage.getItem("session-terminal-font-size") }));
      assert(st.fresh === "18" && st.legacy === null, `migration failed: ${JSON.stringify(st)}`);
      await page.evaluate(() => localStorage.removeItem("dc-terminal-font-size"));
    });

    await run("desktop: refresh stream reconnects (no error)", async () => {
      const before = bag.consoleErrors.length + bag.pageErrors.length;
      await page.click(".attach-desktop [data-terminal-refresh]");
      await sleep(1000);
      assert(bag.consoleErrors.length + bag.pageErrors.length === before, "refresh errored");
    });

    await run("desktop: context buttons follow the foreground command", async () => {
      const slot = ".attach-desktop [data-terminal-context]";
      await page.click("#terminal .xterm-screen");
      await page.keyboard.type("less /etc/hosts");
      await page.keyboard.press("Enter");
      await page.waitForSelector(`${slot} button[title="Quit"]`, { timeout: 10000 });
      const labels = await page.$$eval(`${slot} button`, (els) => els.map((el) => el.textContent.trim()));
      assert(labels.includes("q") && labels.includes("Space"), `expected less keys, got: ${labels.join("|")}`);
      const program = await page.$eval(`${slot} .terminal-context-name`, (el) => el.textContent.trim());
      assert(program === "less", `expected program name less, got: ${program}`);
      await page.click(`${slot} button[title="Quit"]`);
      await page.waitForFunction((sel) => !document.querySelector(sel + " button"), slot, { timeout: 10000 });
    });

    await run("desktop: lifecycle teardown (remove terminal + input, no new errors)", async () => {
      const before = bag.consoleErrors.length + bag.pageErrors.length;
      await page.evaluate(() => { document.getElementById("terminal")?.remove(); document.querySelector("terminal-input")?.remove(); });
      await sleep(700);
      assert(bag.consoleErrors.length + bag.pageErrors.length === before, "teardown errored");
    });

    // ---------------- mobile (coarse pointer) ----------------
    const mp = await mobilePage();
    await mp.goto(shellUrl, { waitUntil: "domcontentloaded" });
    await mp.waitForSelector("#terminal .xterm-screen canvas", { timeout: 12000 });
    await sleep(1200);

    await run("mobile: coarse pointer -> mirror + cursor input + mobile toolbar", async () => {
      assert(await mp.evaluate(() => matchMedia("(pointer: coarse)").matches), "pointer not coarse");
      await mp.waitForSelector("#terminal-cursor-input", { timeout: 8000 });
      assert(await mp.$(".attach-cursor"), "no .attach-cursor overlay");
      assert(await mp.locator("[data-terminal-copy]").first().isVisible(), "mobile copy button not visible");
      assert(await mp.locator('[data-terminal-control="enter"]').first().isVisible(), "control buttons not visible");
    });

    await run("mobile: cursor-input typing sends text + mirror echoes", async () => {
      const marker = `MOB${tag.slice(-4)}`;
      await mp.evaluate(() => document.getElementById("terminal-cursor-input").focus());
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
      await mp.locator('[data-terminal-control="enter"]').first().click();
      assert(/enter/.test((await reqP).postData() || ""), "control body missing enter");
    });

    await run("mobile: Ctrl modifier arms then sends ctrl-<letter>", async () => {
      const ctrlBtn = mp.locator("[data-shell-ctrl]").first();
      await ctrlBtn.click();
      assert((await ctrlBtn.getAttribute("aria-pressed")) === "true", "ctrl did not arm");
      await mp.evaluate(() => document.getElementById("terminal-cursor-input").focus());
      const reqP = mp.waitForRequest((r) => /\/input$/.test(r.url()) && r.method() === "POST", { timeout: 8000 });
      await mp.keyboard.type("c", { delay: 40 });
      assert(/ctrl-c/.test((await reqP).postData() || ""), "expected ctrl-c");
      assert((await ctrlBtn.getAttribute("aria-pressed")) === "false", "ctrl did not disarm");
    });

    await run("mobile: arming ctrl focuses the cursor input, disarming does not", async () => {
      await mp.evaluate(() => document.getElementById("terminal-cursor-input").blur());
      await mp.locator("[data-shell-ctrl]").first().click();
      assert((await mp.evaluate(() => document.activeElement && document.activeElement.id)) === "terminal-cursor-input", "cursor input not focused on arm");
      await mp.evaluate(() => document.getElementById("terminal-cursor-input").blur());
      await mp.locator("[data-shell-ctrl]").first().click();
      assert((await mp.evaluate(() => document.activeElement && document.activeElement.id)) !== "terminal-cursor-input", "disarm must not refocus");
    });

    await run("mobile: context buttons render and send their sequence", async () => {
      const slot = ".attach-mobile [data-terminal-context]";
      await mp.evaluate(() => document.getElementById("terminal-cursor-input").focus());
      await mp.keyboard.type("less /etc/hosts", { delay: 40 });
      await mp.keyboard.press("Enter");
      await mp.waitForSelector(`${slot} button[title="Quit"]`, { timeout: 10000 });
      await mp.evaluate(() => document.getElementById("terminal-cursor-input").blur());
      await mp.locator(`${slot} button[title="Search"]`).click();
      assert((await mp.evaluate(() => document.activeElement && document.activeElement.id)) === "terminal-cursor-input", "search key did not focus cursor input");
      await mp.keyboard.press("Backspace");
      await sleep(400);
      const reqP = mp.waitForRequest((r) => /\/input$/.test(r.url()) && r.method() === "POST", { timeout: 8000 });
      await mp.locator(`${slot} button[title="Quit"]`).click();
      await reqP;
      await mp.waitForFunction((sel) => !document.querySelector(sel + " button"), slot, { timeout: 10000 });
    });

    // Swipe scrolling (terminal-scroll-zone): finger travel streams px deltas that
    // terminal-attach converts into per-program steps (here: tmux history controls,
    // scroll-line-up/down). Synthetic PointerEvents on the shadow .zone stand in
    // for a touch drag; a fast release starts a decaying fling that keeps posting
    // steps after pointerup (velocity capped by the measured input round trip).
    await run("mobile: swipe drag posts proportional history steps", async () => {
      const reqP = mp.waitForRequest((r) => /\/input$/.test(r.url()) && r.method() === "POST" && /scroll-line-up/.test(r.postData() || ""), { timeout: 8000 });
      await mp.evaluate(async () => {
        const zone = document.querySelector("terminal-scroll-zone").shadowRoot.querySelector(".zone");
        const rect = zone.getBoundingClientRect();
        const x = rect.left + rect.width / 2;
        let y = rect.top + rect.height / 3;
        const ev = (type, opts) => zone.dispatchEvent(new PointerEvent(type, Object.assign({ bubbles: true, composed: true, pointerId: 7, pointerType: "touch", isPrimary: true, button: 0, buttons: 1, clientX: x, clientY: y }, opts)));
        const tick = () => new Promise((resolve) => setTimeout(resolve, 16));
        ev("pointerdown", {});
        for (let i = 0; i < 10; i++) { y += 18; ev("pointermove", { clientY: y }); await tick(); }
        ev("pointerup", { buttons: 0, clientY: y });
      });
      await reqP;
    });

    await run("mobile: fling keeps scrolling after release", async () => {
      const posts = [];
      const onReq = (r) => { if (/\/input$/.test(r.url()) && r.method() === "POST" && /scroll-line-down/.test(r.postData() || "")) posts.push(Date.now()); };
      mp.on("request", onReq);
      const released = await mp.evaluate(async () => {
        const zone = document.querySelector("terminal-scroll-zone").shadowRoot.querySelector(".zone");
        const rect = zone.getBoundingClientRect();
        const x = rect.left + rect.width / 2;
        let y = rect.top + rect.height * 0.7;
        const ev = (type, opts) => zone.dispatchEvent(new PointerEvent(type, Object.assign({ bubbles: true, composed: true, pointerId: 8, pointerType: "touch", isPrimary: true, button: 0, buttons: 1, clientX: x, clientY: y }, opts)));
        const tick = () => new Promise((resolve) => setTimeout(resolve, 16));
        ev("pointerdown", {});
        for (let i = 0; i < 6; i++) { y -= 30; ev("pointermove", { clientY: y }); await tick(); }
        ev("pointerup", { buttons: 0, clientY: y });
        return Date.now();
      });
      await sleep(800);
      mp.off("request", onReq);
      assert(posts.some((t) => t > released + 120), `no scroll posts after release (${posts.length} total)`);
    }, { soft: true });

    await run("mobile: copy mode toggles the selection mirror", async () => {
      const copyBtn = mp.locator("[data-terminal-copy]").first();
      await copyBtn.click();
      await mp.waitForSelector("#terminal.attach-terminal-copy-mode", { timeout: 4000 });
      assert((await mp.evaluate(() => (document.querySelector(".attach-selection") || {}).textContent || "")).trim().length > 0, "mirror empty in copy mode");
      await copyBtn.click();
    });

    await run("mobile: paste without clipboard shows the fallback toast", async () => {
      await mp.locator("[data-terminal-paste]").first().click();
      await mp.waitForFunction(() => /not available|clipboard/i.test(document.body.innerText), null, { timeout: 5000 });
    }, { soft: true });
  } finally {
    if (shellUrl) await L.deleteShell(page, shellUrl).catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
  }
});
