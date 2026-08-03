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
//     The same zone axis-locks horizontal gestures into terminal-swipe events,
//     terminal-swipe-nav turns them into switching between the open terminals
//     in tab order, with a target pill that doubles as the pending indicator.
// Terminal paints to <canvas> (CanvasAddon, several stacked layers, no .xterm-rows);
// read text from the .attach-selection mirror. Uses a throwaway shell (safe target).
// Routes: /shells/:id, /shells/:id/input, /shells/:id/resize, /shells/:id/stream.

// The fullscreen switch is an entry of the tab strip's terminal settings, like
// the editor's is an entry of its menu, so reaching it opens the gear first.
async function fullscreenFromMenu(page) {
  await page.click("terminal-tabs .terminal-tabs-settings .terminal-tabs-new-btn");
  await page.waitForSelector("terminal-tabs .terminal-settings-menu.show [data-terminal-fullscreen]", { timeout: 4000 });
  await page.click("terminal-tabs [data-terminal-fullscreen]");
}

L.runFeature("TERMINAL", async ({ engine, page, run, mobilePage, bag }) => {
  const tag = `term-${engine}-${Date.now().toString(36)}`;
  const project = `zztc-${tag}`;
  let shellUrl = null;
  let shellUrl2 = null;
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

    await run("desktop: a multi line paste posts one paste payload, no Enter bytes", async () => {
      await page.click("#terminal .xterm-screen");
      const reqP = page.waitForRequest((r) => /\/input$/.test(r.url()) && r.method() === "POST", { timeout: 8000 });
      await page.evaluate(() => {
        const area = document.querySelector(".xterm-helper-textarea");
        const data = new DataTransfer();
        data.setData("text/plain", "dcpaste one\ndcpaste two");
        area.dispatchEvent(new ClipboardEvent("paste", { clipboardData: data, bubbles: true, cancelable: true }));
      });
      const body = (await reqP).postData() || "";
      // xterm would turn the newline into \r and submit the first line in a
      // coder; the paste payload goes through tmux's buffer instead.
      assert(body.includes('"paste"'), `paste posted as ${body}`);
      assert(!body.includes("\\r"), `paste carries Enter bytes: ${body}`);
      // bash keeps the pasted text on the prompt, clear it for the next check.
      await page.keyboard.press("Control+C");
      await sleep(600);
    });

    // A paste starting with a dash used to end in tmux's own flag parsing
    // ("command set-buffer: unknown flag -d"), the buffer command separates the
    // text with -- now.
    await run("desktop: a paste starting with a dash lands on the prompt", async () => {
      const marker = `-dxdebug.idekey=DCP${tag.slice(-4)}`;
      await page.click("#terminal .xterm-screen");
      const reqP = page.waitForRequest((r) => /\/input$/.test(r.url()) && r.method() === "POST", { timeout: 8000 });
      await page.evaluate((text) => {
        const area = document.querySelector(".xterm-helper-textarea");
        const data = new DataTransfer();
        data.setData("text/plain", text);
        area.dispatchEvent(new ClipboardEvent("paste", { clipboardData: data, bubbles: true, cancelable: true }));
      }, marker);
      await reqP;
      let text = "";
      for (let i = 0; i < 12; i++) { text = await page.evaluate(() => { const m = document.querySelector(".attach-selection"); return m ? m.textContent || "" : ""; }); if (text.includes(marker)) break; await sleep(400); }
      assert(text.includes(marker), `dash paste never reached the pane (len ${text.length})`);
      await page.keyboard.press("Control+C");
      await sleep(600);
    });

    await run("desktop: Shift+Enter posts the shift-enter control, shells run the command like Enter", async () => {
      const marker = `TSE${tag.slice(-4)}`;
      await page.click("#terminal .xterm-screen");
      await page.keyboard.type(`echo ${marker.slice(0, 3)}'X'${marker.slice(3)}`, { delay: 20 });
      const reqP = page.waitForRequest((r) => /\/input$/.test(r.url()) && r.method() === "POST" && /shift-enter/.test(r.postData() || ""), { timeout: 8000 });
      await page.keyboard.press("Shift+Enter");
      await reqP;
      const executed = `${marker.slice(0, 3)}X${marker.slice(3)}`;
      let text = "";
      for (let i = 0; i < 12; i++) { text = await page.evaluate(() => { const m = document.querySelector(".attach-selection"); return m ? m.textContent || "" : ""; }); if (text.includes(executed)) break; await sleep(400); }
      assert(text.includes(executed), `command did not run on Shift+Enter (len ${text.length})`);
      assert(!text.includes("S-Enter"), "literal S-Enter text leaked into the shell");
    });

    await run("desktop: pixel wheel deltas (trackpad) post proportional history steps", async () => {
      const wheelBurst = async (dy, count) => {
        await page.evaluate(async ({ dy, count }) => {
          const screen = document.querySelector("#terminal .xterm-screen");
          const rect = screen.getBoundingClientRect();
          const tick = () => new Promise((resolve) => setTimeout(resolve, 16));
          for (let i = 0; i < count; i++) {
            screen.dispatchEvent(new WheelEvent("wheel", { bubbles: true, cancelable: true, deltaMode: 0, deltaY: dy, clientX: rect.left + rect.width / 2, clientY: rect.top + rect.height / 2 }));
            await tick();
          }
        }, { dy, count });
      };
      const upP = page.waitForRequest((r) => /\/input$/.test(r.url()) && r.method() === "POST" && /scroll-line-up/.test(r.postData() || ""), { timeout: 8000 });
      await wheelBurst(-40, 6);
      await upP;
      const downP = page.waitForRequest((r) => /\/input$/.test(r.url()) && r.method() === "POST" && /scroll-line-down/.test(r.postData() || ""), { timeout: 8000 });
      await wheelBurst(40, 8);
      await downP;
    });

    await run("desktop: the strip settings menu persists font-size, the settings row stays mobile only", async () => {
      const rowHidden = await page.$eval(".attach-settings", (el) => getComputedStyle(el).display === "none");
      assert(rowHidden, "old settings row still visible on desktop");
      await page.click(".terminal-tabs-settings > button");
      await page.waitForSelector(".terminal-tabs-settings .dropdown-menu.show", { state: "visible", timeout: 4000 });
      const sel = '.terminal-tabs-settings terminal-setting-select[setting="font-size"] select';
      const before = await page.$eval(sel, (el) => el.value);
      const value = await page.$eval(sel, (el) => [...el.options].map((o) => o.value).find((v) => v !== el.value));
      await page.selectOption(sel, value);
      await sleep(300);
      assert(await page.evaluate(() => localStorage.getItem("dc-terminal-font-size")) === value, "font-size not persisted");
      assert((await page.locator(".terminal-tabs-settings .dropdown-menu.show").count()) === 1, "settings menu closed on select");
      await page.selectOption(sel, before);
      await sleep(200);
      await page.keyboard.press("Escape");
      await sleep(200);
    });

    await run("desktop: legacy storage key migrates to dc- on read", async () => {
      await page.evaluate(() => { localStorage.removeItem("dc-terminal-font-size"); localStorage.setItem("session-terminal-font-size", "18"); });
      await page.reload({ waitUntil: "domcontentloaded" });
      await sleep(800);
      const st = await page.evaluate(() => ({ fresh: localStorage.getItem("dc-terminal-font-size"), legacy: localStorage.getItem("session-terminal-font-size") }));
      assert(st.fresh === "18" && st.legacy === null, `migration failed: ${JSON.stringify(st)}`);
      await page.evaluate(() => localStorage.removeItem("dc-terminal-font-size"));
    });

    await run("desktop: theme select applies a palette to the frame, persists, posts to the server", async () => {
      const frameBg = () => page.$eval("#terminal", (el) => getComputedStyle(el).backgroundColor);
      await page.emulateMedia({ colorScheme: "dark" });
      await page.click(".terminal-tabs-settings > button");
      await page.waitForSelector(".terminal-tabs-settings .dropdown-menu.show", { state: "visible", timeout: 4000 });
      const sel = '.terminal-tabs-settings terminal-setting-select[setting="theme"] select';
      const postP = page.waitForRequest((r) => /\/terminal-theme$/.test(r.url()) && r.method() === "POST", { timeout: 6000 });
      await page.selectOption(sel, "solarized-auto");
      const post = await postP;
      assert(/"bg":"#002b36"/.test(post.postData() || ""), `theme post body wrong: ${post.postData()}`);
      await sleep(300);
      assert((await frameBg()) === "rgb(0, 43, 54)", `frame not solarized dark: ${await frameBg()}`);
      assert(await page.evaluate(() => localStorage.getItem("dc-terminal-theme")) === "solarized-auto", "theme not persisted");
      await page.selectOption(sel, "auto");
      await sleep(200);
      await page.keyboard.press("Escape");
      await page.emulateMedia({ colorScheme: null });
      await page.evaluate(() => localStorage.removeItem("dc-terminal-theme"));
    });

    await run("desktop: auto and solarized-auto themes follow the OS scheme", async () => {
      const frameBg = () => page.$eval("#terminal", (el) => getComputedStyle(el).backgroundColor);
      const setTheme = async (value) => {
        await page.click(".terminal-tabs-settings > button");
        await page.waitForSelector(".terminal-tabs-settings .dropdown-menu.show", { state: "visible", timeout: 4000 });
        await page.selectOption('.terminal-tabs-settings terminal-setting-select[setting="theme"] select', value);
        await page.keyboard.press("Escape");
        await sleep(200);
      };
      await setTheme("auto");
      await page.emulateMedia({ colorScheme: "dark" });
      await sleep(300);
      assert((await frameBg()) === "rgb(11, 15, 25)", `auto+dark frame wrong: ${await frameBg()}`);
      await page.emulateMedia({ colorScheme: "light" });
      await sleep(300);
      assert((await frameBg()) === "rgb(255, 255, 255)", `auto+light frame wrong: ${await frameBg()}`);
      await setTheme("solarized-auto");
      await page.emulateMedia({ colorScheme: "dark" });
      await sleep(300);
      assert((await frameBg()) === "rgb(0, 43, 54)", `solarized-auto+dark frame wrong: ${await frameBg()}`);
      await page.emulateMedia({ colorScheme: "light" });
      await sleep(300);
      assert((await frameBg()) === "rgb(253, 246, 227)", `solarized-auto+light frame wrong: ${await frameBg()}`);
      await page.emulateMedia({ colorScheme: null });
      await page.evaluate(() => localStorage.removeItem("dc-terminal-theme"));
    });

    await run("desktop: theme colors ride the stream connect and the resize post", async () => {
      // The scheme travels with every server contact, not only the dedicated
      // /terminal-theme POST, so a reconnect or resize on a differently themed
      // device recovers on its own.
      const streamReqP = page.waitForRequest((r) => /\/stream\?/.test(r.url()) && /[?&]bg=%23/.test(r.url()), { timeout: 8000 });
      await page.click(".attach-desktop [data-terminal-refresh]");
      const streamReq = await streamReqP;
      assert(/[?&]fg=%23/.test(streamReq.url()), `stream connect missing fg: ${streamReq.url()}`);
      const resizeReqP = page.waitForRequest((r) => /\/resize$/.test(r.url()) && r.method() === "POST", { timeout: 8000 });
      await page.setViewportSize({ width: 1200, height: 860 });
      const resizeReq = await resizeReqP;
      const body = resizeReq.postData() || "";
      assert(/bg=%23/.test(body) && /fg=%23/.test(body), `resize post missing theme: ${body}`);
      await page.setViewportSize({ width: 1360, height: 900 });
      await sleep(300);
    });

    // Fullscreen (desktop only): the attach page takes over the viewport, the
    // tab strip stays on top, the control footer stays at the bottom, page
    // chrome disappears, and rows follow the viewport height instead of the
    // rows setting. Toggles: strip button, Ctrl+Shift+F or Cmd+Shift+F,
    // double-click on empty strip space. Persists per device.
    await run("desktop: fullscreen via the settings menu + Ctrl/Cmd+Shift+F + Ctrl+Shift+Enter alias + strip double-click, rows follow the viewport", async () => {
      const state = () => page.evaluate(() => {
        const footer = document.querySelector(".attach-footer");
        const footerBox = footer.getBoundingClientRect();
        return {
          on: document.documentElement.classList.contains("dc-terminal-fullscreen"),
          pagePos: getComputedStyle(document.querySelector(".attach-page")).position,
          footerVisible: getComputedStyle(footer).display !== "none" && footerBox.height > 0,
          footerAtBottom: Math.abs(footerBox.bottom - window.innerHeight) < 2,
          stored: localStorage.getItem("dc-terminal-fullscreen"),
          pressed: document.querySelector("[data-terminal-fullscreen]").getAttribute("aria-pressed"),
          icon: document.querySelector("[data-terminal-fullscreen] i").className,
          label: document.querySelector("[data-terminal-fullscreen-label]").textContent.trim(),
          menuOpen: Boolean(document.querySelector("terminal-tabs .terminal-settings-menu.show")),
        };
      });
      const resizeRows = (r) => { const m = /(?:^|&)rows=(\d+)/.exec(r.postData() || ""); return m ? Number(m[1]) : 0; };
      const enterP = page.waitForRequest((r) => /\/resize$/.test(r.url()) && r.method() === "POST" && resizeRows(r) > 30, { timeout: 8000 });
      await fullscreenFromMenu(page);
      await enterP;
      let st = await state();
      assert(st.on && st.pagePos === "fixed" && st.footerVisible && st.footerAtBottom, `enter state wrong: ${JSON.stringify(st)}`);
      assert(st.stored === "1" && st.pressed === "true" && /ti-minimize/.test(st.icon) && st.label === "Exit fullscreen",
        `enter button state wrong: ${JSON.stringify(st)}`);
      assert(!st.menuOpen, "the settings menu stayed open behind the fullscreen page");
      const exitP = page.waitForRequest((r) => /\/resize$/.test(r.url()) && r.method() === "POST" && resizeRows(r) === 30, { timeout: 8000 });
      await page.keyboard.press("Control+Shift+F");
      await exitP;
      st = await state();
      assert(!st.on && st.pagePos !== "fixed" && st.footerVisible && st.pressed === "false" && /ti-maximize/.test(st.icon) && st.label === "Fullscreen",
        `exit state wrong: ${JSON.stringify(st)}`);
      await page.keyboard.press("Meta+Shift+F");
      await page.waitForFunction(() => document.documentElement.classList.contains("dc-terminal-fullscreen"), null, { timeout: 4000 });
      await page.reload({ waitUntil: "domcontentloaded" });
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 10000 });
      await page.waitForFunction(() => document.documentElement.classList.contains("dc-terminal-fullscreen"), null, { timeout: 6000 });
      const strip = await page.locator(".terminal-tabs-strip").boundingBox();
      await page.mouse.dblclick(strip.x + strip.width - 8, strip.y + strip.height / 2);
      await page.waitForFunction(() => !document.documentElement.classList.contains("dc-terminal-fullscreen"), null, { timeout: 4000 });
      await page.keyboard.press("Control+Shift+Enter");
      await page.waitForFunction(() => document.documentElement.classList.contains("dc-terminal-fullscreen"), null, { timeout: 4000 });
      await page.keyboard.press("Control+Shift+Enter");
      await page.waitForFunction(() => !document.documentElement.classList.contains("dc-terminal-fullscreen"), null, { timeout: 4000 });
      await page.evaluate(() => localStorage.removeItem("dc-terminal-fullscreen"));
    });

    // A docked assistant keeps its column in fullscreen. The panel sits above
    // the attach page, so a page that took the whole viewport would run under
    // it and lose its right edge. The page ends at the panel's left edge
    // instead, and it follows the resize handle because both sides read
    // --dc-assistant-w. Closing the panel gives the viewport back.
    await run("desktop: fullscreen leaves the docked assistant its column and follows its resize", async () => {
      const edges = () => page.evaluate(() => {
        const box = document.querySelector(".attach-page").getBoundingClientRect();
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
      await fullscreenFromMenu(page);
      await page.waitForFunction(() => document.documentElement.classList.contains("dc-terminal-fullscreen"), null, { timeout: 5000 });
      await sleep(300);
      const side = await edges();
      assert(side.docked, "the panel did not dock");
      assert(Math.abs(side.right - side.panelLeft) <= 1 && side.right < side.width - 100,
        `the terminal does not stop at the panel: ${JSON.stringify(side)}`);

      const handle = await page.locator("[data-assistant-resize]").boundingBox();
      await page.mouse.move(handle.x + handle.width / 2, handle.y + handle.height / 2);
      await page.mouse.down();
      await page.mouse.move(handle.x - 120, handle.y + handle.height / 2, { steps: 10 });
      await page.mouse.up();
      await sleep(400);
      const wider = await edges();
      assert(wider.right < side.right - 30 && Math.abs(wider.right - wider.panelLeft) <= 1,
        `the terminal did not follow the panel's resize: ${JSON.stringify(wider)}`);

      await page.click("[data-assistant-panel-close]");
      await page.waitForSelector(".dc-assistant-panel-card[hidden]", { state: "attached", timeout: 8000 });
      await sleep(300);
      const full = await page.evaluate(() => {
        const box = document.querySelector(".attach-page").getBoundingClientRect();
        return { right: Math.round(box.right), width: window.innerWidth };
      });
      assert(Math.abs(full.right - full.width) <= 1, `the closed panel left a gap: ${JSON.stringify(full)}`);
      await page.keyboard.press("Control+Shift+F");
      await page.waitForFunction(() => !document.documentElement.classList.contains("dc-terminal-fullscreen"), null, { timeout: 4000 });
      await page.evaluate(() => localStorage.removeItem("dc-terminal-fullscreen"));
      return "terminal left, panel right, width follows the handle";
    });

    await run("desktop: refresh stream reconnects (no error)", async () => {
      const before = bag.consoleErrors.length + bag.pageErrors.length;
      await page.click(".attach-desktop [data-terminal-refresh]");
      await sleep(1000);
      assert(bag.consoleErrors.length + bag.pageErrors.length === before, "refresh errored");
    });

    await run("desktop: the page header carries no stop or delete", async () => {
      const action = page.locator('.page-header form[action$="/delete"] button').first();
      assert(await action.count() === 1, "the delete form left the shell attach header");
      assert(!(await action.isVisible()), "the header delete shows on a fine pointer");
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
    // The mobile context has its own localStorage, so an instance that found a
    // newer release shows the update dialog here again; its backdrop swallows
    // every tap on the toolbar below.
    await L.dismissUpdate(mp);
    await mp.waitForSelector("#terminal .xterm-screen canvas", { timeout: 12000 });
    await sleep(1200);

    await run("mobile: the page header carries the delete", async () => {
      assert(await mp.locator('.page-header form[action$="/delete"] button').first().isVisible(),
        "the header delete is missing on a coarse pointer");
    });

    await run("mobile: coarse pointer -> mirror + cursor input + mobile toolbar", async () => {
      assert(await mp.evaluate(() => matchMedia("(pointer: coarse)").matches), "pointer not coarse");
      await mp.waitForSelector("#terminal-cursor-input", { timeout: 8000 });
      assert(await mp.$(".attach-cursor"), "no .attach-cursor overlay");
      assert(await mp.locator("[data-terminal-copy]").first().isVisible(), "mobile copy button not visible");
      assert(await mp.locator('[data-terminal-control="enter"]').first().isVisible(), "control buttons not visible");
    });

    // Typing binds through delegated document listeners in terminal-input
    // because #terminal-cursor-input is created by terminal-attach after its
    // async setup; a node lookup at init would race it and typing would die.
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

    // Same tmux flag parsing trap on the typing path: send-keys -l used to hand
    // the text over as the first operand. insertText posts the whole string in
    // one text payload (per keystroke posts would each carry a single dash,
    // which tmux still takes as text).
    await run("mobile: typed text starting with a dash reaches the pane", async () => {
      const marker = `-dxdebug.idekey=MOD${tag.slice(-4)}`;
      await mp.evaluate(() => document.getElementById("terminal-cursor-input").focus());
      const reqP = mp.waitForRequest((r) => /\/input$/.test(r.url()) && r.method() === "POST", { timeout: 8000 });
      await mp.keyboard.insertText(marker);
      await reqP;
      let text = "";
      for (let i = 0; i < 12; i++) { text = await mp.evaluate(() => { const m = document.querySelector(".attach-selection"); return m ? m.textContent || "" : ""; }); if (text.includes(marker)) break; await sleep(400); }
      assert(text.includes(marker), `dash text never reached the pane (len ${text.length})`);
      // Runs into a command-not-found, which clears the prompt for the next check.
      await mp.keyboard.press("Enter");
      await sleep(600);
    });

    await run("mobile: control button (enter) posts a control", async () => {
      const reqP = mp.waitForRequest((r) => /\/input$/.test(r.url()) && r.method() === "POST", { timeout: 8000 });
      await mp.locator('[data-terminal-control="enter"]').first().click();
      assert(/enter/.test((await reqP).postData() || ""), "control body missing enter");
    });

    await run("mobile: Ctrl modifier arms, focuses the input (keyboard opens), sends ctrl-<letter>", async () => {
      const ctrlBtn = mp.locator("[data-shell-ctrl]").first();
      await ctrlBtn.click();
      assert((await ctrlBtn.getAttribute("aria-pressed")) === "true", "ctrl did not arm");
      // Arming focuses the cursor input in the tap handler, so the on-screen
      // keyboard comes up without an extra tap on the terminal.
      assert(await mp.evaluate(() => document.activeElement?.id === "terminal-cursor-input"), "cursor input not focused after arming ctrl");
      const reqP = mp.waitForRequest((r) => /\/input$/.test(r.url()) && r.method() === "POST", { timeout: 8000 });
      await mp.keyboard.type("c", { delay: 40 });
      assert(/ctrl-c/.test((await reqP).postData() || ""), "expected ctrl-c");
      assert((await ctrlBtn.getAttribute("aria-pressed")) === "false", "ctrl did not disarm");
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

    // Horizontal axis on the same zone: swipe left or right rotates through the
    // open terminals in tab order, wrapping at both ends (terminal-swipe-nav).
    // A pill names the target while the finger is down and stays as the pending
    // indicator until the new page arrives, the terminal frame follows the
    // finger a damped distance.
    await run("mobile: horizontal swipe switches to the neighbor terminal with a target pill", async () => {
      shellUrl2 = await L.createShell(mp, project);
      const firstId = new URL(shellUrl).pathname.split("/").pop();
      const secondId = new URL(shellUrl2).pathname.split("/").pop();
      await mp.goto(shellUrl, { waitUntil: "domcontentloaded" });
      await mp.waitForSelector("#terminal .xterm-screen canvas", { timeout: 10000 });
      await sleep(800);
      // Precondition: our newest shell is the right neighbor, so the swipe stays
      // on this runner's own sessions.
      const order = await mp.$$eval("terminal-tabs .terminal-tab", (els) => els.map((e) => e.dataset.tabId));
      assert(order[order.indexOf(firstId) + 1] === secondId, `right neighbor is not ours: ${order}`);
      const swipe = (dir) => mp.evaluate(async (dir) => {
        const zone = document.querySelector("terminal-scroll-zone").shadowRoot.querySelector(".zone");
        const rect = zone.getBoundingClientRect();
        let x = rect.left + rect.width * (dir < 0 ? 0.8 : 0.2);
        const y = rect.top + rect.height / 2;
        const ev = (type, opts) => zone.dispatchEvent(new PointerEvent(type, Object.assign({ bubbles: true, composed: true, pointerId: 9, pointerType: "touch", isPrimary: true, button: 0, buttons: 1, clientX: x, clientY: y }, opts)));
        const tick = () => new Promise((resolve) => setTimeout(resolve, 16));
        ev("pointerdown", {});
        for (let i = 0; i < 10; i++) { x += dir * 12; ev("pointermove", { clientX: x }); await tick(); }
        const pill = document.querySelector(".terminal-swipe-pill");
        const midGesture = {
          pill: pill ? pill.textContent.trim() : "",
          // The pill is one thing app wide: the editor's file swipe shows the
          // same class in the same place, see editor.js.
          shared: Boolean(pill && pill.classList.contains("dc-swipe-pill")),
          top: pill ? Math.round(pill.getBoundingClientRect().top) : -1,
          frameMoved: Boolean(document.getElementById("terminal").style.transform),
        };
        ev("pointerup", { buttons: 0, clientX: x });
        return midGesture;
      }, dir);
      const left = await swipe(-1);
      assert(left.pill.length > 0, "no target pill during the swipe");
      assert(left.shared, "the target pill does not use the shared dc-swipe-pill class");
      assert(left.top >= 0 && left.top < 200, `the target pill is not at the top: ${left.top}px`);
      assert(left.frameMoved, "terminal frame did not follow the finger");
      await mp.waitForURL(new RegExp(secondId), { timeout: 8000 });
      await mp.waitForSelector("#terminal .xterm-screen canvas", { timeout: 10000 });
      await sleep(800);
      // And back: swipe right returns to the previous terminal.
      await swipe(1);
      await mp.waitForURL(new RegExp(firstId), { timeout: 8000 });
      await mp.waitForSelector("#terminal .xterm-screen canvas", { timeout: 10000 });
      await sleep(500);
      // Wrap-around: swiping right on the leftmost tab rotates to the last one.
      const order2 = await mp.$$eval("terminal-tabs .terminal-tab", (els) => els.map((e) => e.dataset.tabId));
      assert(order2[0] === firstId && order2[order2.length - 1] === secondId, `strip must start and end with ours for the wrap check: ${order2}`);
      await swipe(1);
      await mp.waitForURL(new RegExp(secondId), { timeout: 8000 });
      await mp.waitForSelector("#terminal .xterm-screen canvas", { timeout: 10000 });
      await sleep(500);
    });

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
    if (shellUrl2) await L.deleteShell(page, shellUrl2).catch(() => {});
    if (shellUrl) await L.deleteShell(page, shellUrl).catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
  }
});
