const L = require("./lib");
const { assert, sleep, confirmSwal, BASE } = L;

// Editor terminal panel: the project's live coders and shells inside the editor
// page, desktop only (a fine or no pointer, wide viewport). The panel sits below the
// editor surface in the pane column, opens through the kebab menu's Terminal
// entry or Ctrl+J, remembers its open state and height per project
// (dc-editor-term-open:<project> / dc-editor-term-height:<project>), and hides completely on mobile
// widths and touch only devices. Content comes from the fragment
// GET /projects/:name/editor/terminals (tabs plus empty pane divs); the client
// mounts a terminal-attach/terminal-input island pair into a pane on first
// activation, so hidden panes hold no stream. Islands carry the `embedded`
// attribute: rows fit the panel height, and a hidden pane does not
// connect. The panel refreshes over the app wide `terminals` event and marks a
// pane's news read when it is activated. The + button POSTs /shells/new with
// the project path and activates the new shell's tab. Tab context menu: open
// terminal page, rename, stop/delete. Editor keyboard shortcuts stay
// away while focus is inside the panel; Ctrl+J toggles from both sides.
// The + button is a dropdown (New coder link with project preselect, New shell
// direct create), Cmd+T opens it while focus is inside the panel and the
// arrows walk it (bootstrap's own dropdown keys). Inside the panel Ctrl+Tab
// steps through the terminal tabs and Ctrl/Cmd+Shift+X asks to close the
// active one, mirroring the attach pages. Tabs drag-reorder with the mouse,
// whatever the pointer media report, never with a touch pointer, and
// persist through POST /terminal-tabs/order. The panel posts the project's ids
// only, and the server reads such a subset as a permutation of the places
// those sessions already hold: the slots stay, only who sits in which changes,
// so a drag here never moves a terminal of another project. The header
// refresh button reconnects the active pane only, the open state key is per
// project (dc-editor-term-open:<project>), and both editor tab strips hide
// their scrollbars. The shell tab context menu mirrors the strip: open
// terminal page, rename, open project, delete.
// The last active tab is remembered per project (dc-editor-term-active) and
// restored on load, closing the active tab hands focus to the neighbor's
// terminal, and the panel owns the terminal keys once it was clicked anywhere
// (a focus-owner flag, because a click on the bare strip focuses nothing).
// A panel opened without any terminal takes the focus itself, so Cmd+T
// opens the + menu right after Ctrl+J or a click opened it.
// A coder created through the + menu comes back to the editor: the create
// form's action carries the return target plus the panel=1 marker, the server
// redirects to .../editor?terminal=<id> and the panel activates that tab
// (without the marker, e.g. from the sheet, a create lands on the coder's
// own page). Coder panes get
// the attach page's files modal (fragment-rendered per coder, kept alive
// across refreshes like the panes) behind a [data-terminal-footer] button the
// active island unhides, so drop and paste uploads run through
// coder-file-upload itself. The panel stands wherever some input points
// finely or none is coarse, read off any pointer and never the primary one,
// and an embedded terminal always types straight into xterm; the probes launch
// Chromium with blink settings for each pointer setup. The copy button in the
// panel head opens the copy view in the pane, a shell's history or a coder's
// conversation with its Screen face.
// Gotchas: headless renders xterm on canvas, output is read from the
// .attach-selection mirror scoped to the panel; the swal prompt of a rename is
// filled via its input field; the second shell arrives over SSE, so the check
// polls for the tab instead of reloading; the coder round trip needs the
// claude CLI on the host like coder-claude.js.

L.runFeature("EDITOR-TERMINAL", async ({ engine, page, run, mobilePage }) => {
  const tag = `edt-${engine}-${Date.now().toString(36)}`;
  const project = `zzet-${tag}`;
  const panel = "[data-editor-term-panel]";
  let secondShellUrl = null;
  const panelText = () => page.evaluate(() => {
    const m = document.querySelector("[data-editor-term-panel] .editor-term-pane.active .attach-selection");
    return m ? m.textContent || "" : "";
  });
  const panelVisible = () => page.evaluate(() => {
    const p = document.querySelector("[data-editor-term-panel]");
    if (!p) return false;
    const r = p.getBoundingClientRect();
    return getComputedStyle(p).display !== "none" && !p.hidden && r.height > 0;
  });
  const tabCount = () => page.locator(`${panel} [data-term-tab]`).count();
  const openEditor = async () => {
    await page.goto(`${BASE}/projects/${project}/editor`, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    await page.waitForSelector(".cm-editor, .editor-textarea", { state: "attached", timeout: 15000 });
  };

  try {
    await L.createProject(page, project);
    await openEditor();

    await run("desktop: the menu carries a Terminal entry that opens the panel with its empty state", async () => {
      const itemVisible = await page.evaluate(() => !document.querySelector("[data-editor-term-item]").hidden);
      assert(itemVisible, "the Terminal menu entry is hidden on desktop");
      await page.evaluate(() => document.querySelector("[data-editor-term-item]").click());
      await sleep(400);
      assert(await panelVisible(), "the panel did not open");
      const emptyVisible = await page.evaluate(() => {
        const e = document.querySelector("[data-editor-term-empty]");
        return e && !e.hidden && e.getBoundingClientRect().height > 0;
      });
      assert(emptyVisible, "no empty state for a project without terminals");
      assert((await tabCount()) === 0, "tabs rendered for a project without terminals");
    });

    await run("desktop: Cmd+T opens the + menu at once after Ctrl+J or a click opened a panel without terminals", async () => {
      const menuOpen = () => page.evaluate(() => !!document.querySelector("[data-editor-term-panel] .dropdown-menu.show"));
      const openers = [
        ["Ctrl+J", () => page.keyboard.press("Control+j")],
        ["a click on the statusbar button", () => page.click("[data-editor-term-status]")],
      ];
      for (const [label, open] of openers) {
        await page.keyboard.press("Control+j");
        await page.waitForFunction(() => document.querySelector("[data-editor-term-panel]").hidden, null, { timeout: 5000 });
        await open();
        await page.waitForFunction(() => !document.querySelector("[data-editor-term-panel]").hidden, null, { timeout: 5000 });
        assert(await panelVisible(), `${label} did not open the panel`);
        assert((await tabCount()) === 0, "the project already has terminals");
        await page.keyboard.press("Meta+t");
        await page.waitForSelector(`${panel} .dropdown-menu.show`, { timeout: 3000 }).catch(() => {});
        assert(await menuOpen(), `Cmd+T right after ${label} did not open the + menu`);
        await page.keyboard.press("Escape");
        await page.waitForSelector(`${panel} .dropdown-menu.show`, { state: "detached", timeout: 5000 }).catch(() => {});
        assert(!(await menuOpen()), `Escape did not close the + menu after ${label}`);
      }
    });

    await run("desktop: the + dropdown offers New coder and creates a shell whose island mounts", async () => {
      await page.click(`${panel} [data-editor-term-plus]`);
      await page.waitForSelector(`${panel} .dropdown-menu.show`, { timeout: 5000 });
      const coderHref = await page.evaluate(() => {
        const menu = document.querySelector("[data-editor-term-panel] .dropdown-menu.show");
        const link = [...menu.querySelectorAll("a.dropdown-item")].find((a) => /new coder/i.test(a.textContent));
        return link ? link.getAttribute("href") : "";
      });
      assert(coderHref.includes("/coders/new?project="), `the + menu misses the New coder link (${coderHref})`);
      const scroll = await page.evaluate(() => {
        const menu = document.querySelector("[data-editor-term-panel] .editor-term-new-menu");
        const style = getComputedStyle(menu);
        return { overflow: style.overflowY, capped: style.maxHeight !== "none" };
      });
      assert(scroll.overflow === "auto" && scroll.capped, `the + menu does not scroll (${JSON.stringify(scroll)})`);
      await page.click(`${panel} [data-editor-term-new]`);
      await page.waitForSelector(`${panel} [data-term-tab]`, { timeout: 15000 });
      await page.waitForSelector(`${panel} .editor-term-pane.active terminal-attach[embedded] .xterm-screen canvas`, { timeout: 15000 });
      const active = await page.evaluate(() => document.querySelector("[data-editor-term-panel] [data-term-tab]").classList.contains("active"));
      assert(active, "the new shell's tab is not active");
      const emptyGone = await page.evaluate(() => document.querySelector("[data-editor-term-empty]").hidden);
      assert(emptyGone, "the empty state stayed visible next to a terminal");
    });

    await run("desktop: typing reaches the shell and the output echoes into the panel", async () => {
      const marker = `ETP${tag.slice(-4)}`;
      await sleep(1400);
      await page.click(`${panel} .editor-term-pane.active .xterm-screen`);
      const reqP = page.waitForRequest((r) => /\/input$/.test(r.url()) && r.method() === "POST", { timeout: 8000 });
      await page.keyboard.type(`echo ${marker}`);
      await reqP;
      await page.keyboard.press("Enter");
      let text = "";
      for (let i = 0; i < 12; i++) { text = await panelText(); if (text.includes(marker)) break; await sleep(400); }
      assert(text.includes(marker), `marker not mirrored (len ${text.length})`);
    });

    // The panel's panes are stages like the attach pages', so the copy button
    // in the panel head opens the same copy view in the terminal's place.
    await run("desktop: the copy button shows a shell's history in the pane and Escape brings the terminal back", async () => {
      const id = await page.evaluate(() => document.querySelector("[data-editor-term-panel] .editor-term-pane.active").getAttribute("data-term-pane"));
      const pane = `${panel} [data-term-pane="${id}"]`;
      const button = `${panel} [data-terminal-footer="${id}"] [data-terminal-copy]`;
      await page.waitForSelector(button, { state: "visible", timeout: 6000 });
      assert(await page.locator(`${button} .ti-copy`).count() === 1, "a shell's copy button does not wear the copy icon");
      await page.click(button);
      await page.waitForSelector(`${pane} terminal-copy:not([hidden])`, { timeout: 6000 });
      await page.waitForFunction(([sel, want]) => (document.querySelector(sel)?.textContent || "").includes(want),
        [`${pane} [data-copy-text]`, `ETP${tag.slice(-4)}`], { timeout: 8000 });
      const state = await page.evaluate(([p, b]) => ({
        behind: document.querySelector(`${p} terminal-attach`).classList.contains("attach-terminal-behind"),
        pressed: document.querySelector(b).getAttribute("aria-pressed"),
        copyBox: Math.round(document.querySelector(`${p} terminal-copy`).getBoundingClientRect().height),
        paneBox: Math.round(document.querySelector(p).getBoundingClientRect().height),
        title: document.querySelector(`${p} [data-copy-title]`).textContent,
      }), [pane, button]);
      assert(state.behind, "the terminal did not step behind the copy view");
      assert(state.pressed === "true", `the copy button does not read pressed: ${state.pressed}`);
      assert(state.copyBox === state.paneBox && state.copyBox > 0, `the copy view does not fill the pane: ${state.copyBox} of ${state.paneBox}px`);
      assert(state.title === "History", `a shell's copy view reads ${state.title}`);
      await page.keyboard.press("Escape");
      await page.waitForSelector(`${pane} terminal-copy`, { state: "hidden", timeout: 4000 });
      const back = await page.evaluate((p) => document.querySelector(`${p} terminal-attach`).classList.contains("attach-terminal-behind"), pane);
      assert(!back, "the terminal stayed behind after Escape");
    });

    await run("desktop: the terminal fits the panel instead of scrolling to a fixed row count", async () => {
      const fits = await page.evaluate(() => {
        const t = document.querySelector("[data-editor-term-panel] .editor-term-pane.active terminal-attach");
        return t && t.scrollHeight <= t.clientHeight + 8;
      });
      assert(fits, "the embedded terminal overflows its pane");
    });

    await run("desktop: editor shortcuts stay away while focus is inside the terminal", async () => {
      await page.click(`${panel} .editor-term-pane.active .xterm-screen`);
      await page.keyboard.press("Control+o");
      await sleep(400);
      const quickOpenHidden = await page.evaluate(() => document.querySelector("[data-editor-quickopen]").hidden);
      assert(quickOpenHidden, "Ctrl+O opened the quick open palette from inside the terminal");
      await page.keyboard.press("Control+c");
      await sleep(300);
    });

    await run("desktop: Ctrl+J closes and reopens the panel", async () => {
      await page.keyboard.press("Control+j");
      await sleep(300);
      assert(!(await panelVisible()), "Ctrl+J did not close the panel");
      await page.keyboard.press("Control+j");
      await sleep(500);
      assert(await panelVisible(), "Ctrl+J did not reopen the panel");
    });

    await run("desktop: the splitter resizes the panel and the height persists", async () => {
      const before = await page.evaluate(() => document.querySelector("[data-editor-term-panel]").getBoundingClientRect().height);
      const splitter = page.locator("[data-editor-term-splitter]");
      const box = await splitter.boundingBox();
      assert(box, "no splitter box");
      await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
      await page.mouse.down();
      await page.mouse.move(box.x + box.width / 2, box.y - 120, { steps: 8 });
      await page.mouse.up();
      await sleep(300);
      const after = await page.evaluate(() => document.querySelector("[data-editor-term-panel]").getBoundingClientRect().height);
      assert(after > before + 80, `panel did not grow (${before} -> ${after})`);
      const stored = await page.evaluate((p) => ({
        project: parseInt(localStorage.getItem(`dc-editor-term-height:${p}`) || "0", 10),
        device: parseInt(localStorage.getItem("dc-editor-term-height") || "0", 10),
      }), project);
      assert(Math.abs(stored.project - after) < 8, `height not persisted for the project (${stored.project} vs ${after})`);
      assert(stored.device === stored.project, `the device default did not follow (${stored.device} vs ${stored.project})`);
    });

    await run("desktop: a shell started elsewhere appears live over the terminals event", async () => {
      const page2 = await page.context().newPage();
      try {
        secondShellUrl = await L.createShell(page2, project);
      } finally {
        await page2.close().catch(() => {});
      }
      let count = 0;
      for (let i = 0; i < 20; i++) { count = await tabCount(); if (count >= 2) break; await sleep(400); }
      assert(count >= 2, `second shell never reached the panel (${count} tabs)`);
    });

    await run("desktop: switching tabs switches the visible pane and mounts lazily", async () => {
      const ids = await page.evaluate(() => [...document.querySelectorAll("[data-editor-term-panel] [data-term-tab]")].map((t) => t.getAttribute("data-term-tab")));
      const secondId = new URL(secondShellUrl).pathname.split("/").pop();
      assert(ids.includes(secondId), "the second shell has no tab");
      const mountedBefore = await page.evaluate((id) => !!document.querySelector(`[data-term-pane="${id}"] terminal-attach`), secondId);
      assert(!mountedBefore, "an inactive pane mounted its island eagerly");
      await page.click(`${panel} [data-term-tab="${secondId}"]`);
      await page.waitForSelector(`${panel} [data-term-pane="${secondId}"].active terminal-attach[embedded]`, { timeout: 10000 });
      const visiblePanes = await page.evaluate(() => [...document.querySelectorAll("[data-editor-term-panel] .editor-term-pane")].filter((p) => p.getBoundingClientRect().height > 0).length);
      assert(visiblePanes === 1, `${visiblePanes} panes visible at once`);
    });

    await run("desktop: an overflowing strip scrolls the active tab into view on switch and after a refresh", async () => {
      const tabState = (id) => page.evaluate((tid) => {
        const strip = document.querySelector("[data-editor-term-panel] [data-editor-term-tabs]");
        const tab = strip && strip.querySelector(`[data-term-tab="${tid}"]`);
        if (!tab) return null;
        const s = strip.getBoundingClientRect();
        const t = tab.getBoundingClientRect();
        return { inside: t.left >= s.left - 1 && t.right <= s.right + 1, scrollLeft: strip.scrollLeft };
      }, id);
      const switchTo = async (id) => {
        await page.evaluate((tid) => {
          document.querySelector(`[data-editor-term-panel] [data-term-tab="${tid}"]`).click();
        }, id);
        await sleep(300);
      };
      const ids = await page.evaluate(() => [...document.querySelectorAll("[data-editor-term-panel] [data-term-tab]")].map((t) => t.getAttribute("data-term-tab")));
      assert(ids.length >= 2, `need two tabs, have ${ids.length}`);
      const first = ids[0];
      const last = ids[ids.length - 1];
      let extraShellUrl = null;
      try {
        const overflows = await page.evaluate(() => {
          const strip = document.querySelector("[data-editor-term-panel] [data-editor-term-tabs]");
          const widest = Math.max(...[...strip.querySelectorAll("[data-term-tab]")].map((t) => t.getBoundingClientRect().width));
          const host = document.querySelector("[data-editor-term-panel] [data-editor-term-tabs-host]");
          host.style.maxWidth = `${Math.ceil(widest) + 40}px`;
          return strip.scrollWidth > strip.clientWidth;
        });
        assert(overflows, "the squeezed strip does not overflow");
        await switchTo(first);
        let state = await tabState(first);
        assert(state && state.inside, "the first tab is not in view after switching to it");
        await switchTo(last);
        state = await tabState(last);
        assert(state && state.inside, "the last tab did not scroll into view on switch");
        assert(state.scrollLeft > 0, "the strip did not scroll at all");
        const before = await tabCount();
        const page2 = await page.context().newPage();
        try {
          extraShellUrl = await L.createShell(page2, project);
        } finally {
          await page2.close().catch(() => {});
        }
        let count = before;
        for (let i = 0; i < 20; i++) { count = await tabCount(); if (count === before + 1) break; await sleep(400); }
        assert(count === before + 1, `the extra shell never reached the panel (${count} tabs)`);
        await sleep(300);
        state = await tabState(last);
        assert(state && state.inside, "the refresh dropped the active tab out of view");
      } finally {
        await page.evaluate(() => {
          document.querySelector("[data-editor-term-panel] [data-editor-term-tabs-host]").style.maxWidth = "";
        }).catch(() => {});
        if (extraShellUrl) await L.deleteShell(page, extraShellUrl).catch(() => {});
        await openEditor().catch(() => {});
        await page.waitForSelector(`${panel} [data-term-tab]`, { timeout: 10000 }).catch(() => {});
        await page.evaluate(() => document.querySelector("[data-editor-term-panel] [data-term-tab]")?.click()).catch(() => {});
      }
    });

    await run("desktop: both editor tab strips hide their scrollbars", async () => {
      const widths = await page.evaluate(() => ({
        files: getComputedStyle(document.querySelector("[data-editor-tabs]")).scrollbarWidth,
        terms: getComputedStyle(document.querySelector("[data-editor-term-tabs]")).scrollbarWidth,
      }));
      assert(widths.files === "none", `file tabs scrollbar-width is ${widths.files}`);
      assert(widths.terms === "none", `terminal tabs scrollbar-width is ${widths.terms}`);
    });

    await run("desktop: Ctrl+Tab steps through the terminal tabs from inside the panel", async () => {
      const before = await page.evaluate(() => document.querySelector("[data-editor-term-panel] .editor-term-pane.active")?.getAttribute("data-term-pane"));
      await page.click(`${panel} .editor-term-pane.active .xterm-screen`);
      await page.keyboard.press("Control+Tab");
      await sleep(500);
      const after = await page.evaluate(() => document.querySelector("[data-editor-term-panel] .editor-term-pane.active")?.getAttribute("data-term-pane"));
      assert(before && after && before !== after, `Ctrl+Tab did not switch (${before} -> ${after})`);
      await page.keyboard.press("Control+Shift+Tab");
      await sleep(500);
      const back = await page.evaluate(() => document.querySelector("[data-editor-term-panel] .editor-term-pane.active")?.getAttribute("data-term-pane"));
      assert(back === before, `Ctrl+Shift+Tab did not step back (${back})`);
      const quickOpenHidden = await page.evaluate(() => document.querySelector("[data-editor-quickopen]").hidden);
      assert(quickOpenHidden, "the editor reacted to the panel's Ctrl+Tab");
    });

    await run("desktop: terminal shortcuts work after clicking the bare strip", async () => {
      const before = await page.evaluate(() => document.querySelector("[data-editor-term-panel] .editor-term-pane.active")?.getAttribute("data-term-pane"));
      const box = await page.locator(`${panel} [data-editor-term-tabs]`).boundingBox();
      await page.mouse.click(box.x + box.width - 5, box.y + box.height / 2);
      await page.keyboard.press("Control+Tab");
      await sleep(500);
      const after = await page.evaluate(() => document.querySelector("[data-editor-term-panel] .editor-term-pane.active")?.getAttribute("data-term-pane"));
      assert(before && after && before !== after, `Ctrl+Tab dead after a strip click (${before} -> ${after})`);
      await page.keyboard.press("Control+Shift+Tab");
      await sleep(500);
    });

    await run("desktop: Cmd+T opens the + menu, selection walks it like the strip's menu", async () => {
      await page.click(`${panel} .editor-term-pane.active .xterm-screen`);
      await page.keyboard.press("Meta+t");
      await page.waitForSelector(`${panel} .dropdown-menu.show`, { timeout: 5000 });
      const selectedText = () => page.evaluate(() => document.querySelector("[data-editor-term-panel] .editor-term-new-menu .dropdown-item.selected")?.textContent?.trim() || "");
      const first = await selectedText();
      assert(/new coder/i.test(first), `the first entry is not selected (${first})`);
      const focusStayed = await page.evaluate(() => !document.activeElement || !document.activeElement.closest(".editor-term-new-menu"));
      assert(focusStayed, "the selection moved the focus into the menu");
      await page.keyboard.press("ArrowDown");
      await sleep(200);
      const second = await selectedText();
      assert(/new shell/i.test(second), `ArrowDown did not select New shell (${second})`);
      await page.keyboard.press("ArrowUp");
      await sleep(200);
      assert(/new coder/i.test(await selectedText()), "ArrowUp did not step back");
      await page.keyboard.press("Escape");
      await page.waitForSelector(`${panel} .dropdown-menu.show`, { state: "detached", timeout: 5000 }).catch(() => {});
      const open = await page.evaluate(() => !!document.querySelector("[data-editor-term-panel] .dropdown-menu.show"));
      assert(!open, "Escape did not close the + menu");
      const cleared = await page.evaluate(() => !document.querySelector("[data-editor-term-panel] .dropdown-item.selected"));
      assert(cleared, "the selection survived the close");
    });

    await run("desktop: Ctrl+Shift+X asks to close the active terminal", async () => {
      await page.click(`${panel} .editor-term-pane.active .xterm-screen`);
      await page.keyboard.press("Control+Shift+X");
      await page.waitForSelector(".swal2-title", { state: "visible", timeout: 5000 });
      const title = await page.evaluate(() => document.querySelector(".swal2-title")?.textContent || "");
      assert(/delete shell/i.test(title), `unexpected confirm: ${title}`);
      await page.click(".swal2-cancel");
      await page.waitForSelector(".swal2-container", { state: "detached", timeout: 5000 }).catch(() => {});
    });

    await run("desktop: dragging a tab reorders and persists through /terminal-tabs/order", async () => {
      const before = await page.evaluate(() => [...document.querySelectorAll("[data-editor-term-panel] [data-term-tab]")].map((t) => t.getAttribute("data-term-tab")));
      assert(before.length >= 2, `need two tabs, have ${before.length}`);
      const boxA = await page.locator(`${panel} [data-term-tab="${before[0]}"]`).boundingBox();
      const boxB = await page.locator(`${panel} [data-term-tab="${before[1]}"]`).boundingBox();
      const reqP = page.waitForRequest((r) => r.url().includes("/terminal-tabs/order") && r.method() === "POST", { timeout: 8000 });
      await page.mouse.move(boxA.x + boxA.width / 2, boxA.y + boxA.height / 2);
      await page.mouse.down();
      await page.mouse.move(boxB.x + boxB.width - 4, boxB.y + boxB.height / 2, { steps: 10 });
      await page.mouse.up();
      const body = JSON.parse((await reqP).postData() || "{}");
      assert(Array.isArray(body.ids) && body.ids[0] === before[1] && body.ids[1] === before[0],
        `order posted as ${JSON.stringify(body.ids)}`);
      let after = before;
      for (let i = 0; i < 15; i++) {
        after = await page.evaluate(() => [...document.querySelectorAll("[data-editor-term-panel] [data-term-tab]")].map((t) => t.getAttribute("data-term-tab")));
        if (after[0] === before[1]) break;
        await sleep(400);
      }
      assert(after[0] === before[1], `the server order did not follow (${after.join(", ")})`);
    });

    // The panel only ever shows one project, so its drag posts a subset. The
    // check builds the shape where that matters: a foreign terminal sits
    // between two of the project's own, and after a drag in the panel it has
    // to hold the exact seat it held. It also pins the other half, that the
    // project's own terminals stay on the same set of seats instead of being
    // pulled to the front of the strip.
    await run("desktop: a drag in the panel leaves another project's terminals in their seats", async () => {
      const other = `${project}-c`;
      const stripOrder = () => page.$$eval("terminal-tabs .terminal-tab", (els) => els.map((e) => e.dataset.tabId));
      let foreignUrl = null;
      let ownUrl = null;
      try {
        await L.createProject(page, other);
        foreignUrl = await L.createShell(page, other);
        // Both shells arrive without a @dc_tab_pos, so their place comes from
        // the start time, and that one is second granular (tmux
        // session_created): created inside the same second the strip falls back
        // to the id, which is a uuid and would decide this at random. The wait
        // is what makes the foreign shell land between the project's own.
        await sleep(1200);
        ownUrl = await L.createShell(page, project);
        const foreignId = new URL(foreignUrl).pathname.split("/").pop();

        await page.goto(foreignUrl, { waitUntil: "domcontentloaded" });
        await L.dismissUpdate(page);
        await page.waitForSelector("terminal-tabs .terminal-tab", { timeout: 10000 });
        const before = await stripOrder();
        const foreignAt = before.indexOf(foreignId);
        assert(foreignAt > 0 && foreignAt < before.length - 1,
          `the foreign shell has to sit between the project's own, strip is ${before.join(", ")}`);

        await openEditor();
        await page.waitForSelector(`${panel} [data-term-tab]`, { timeout: 10000 });
        const tabs = await page.evaluate(() => [...document.querySelectorAll("[data-editor-term-panel] [data-term-tab]")].map((t) => t.getAttribute("data-term-tab")));
        assert(tabs.length >= 2, `need two tabs in the panel, have ${tabs.length}`);
        assert(!tabs.includes(foreignId), "the panel lists a terminal of another project");
        const last = await page.locator(`${panel} [data-term-tab="${tabs[tabs.length - 1]}"]`).boundingBox();
        const first = await page.locator(`${panel} [data-term-tab="${tabs[0]}"]`).boundingBox();
        const posted = page.waitForRequest((r) => r.url().includes("/terminal-tabs/order") && r.method() === "POST", { timeout: 8000 });
        await page.mouse.move(last.x + last.width / 2, last.y + last.height / 2);
        await page.mouse.down();
        await page.mouse.move(first.x + 4, first.y + first.height / 2, { steps: 12 });
        await page.mouse.up();
        const body = JSON.parse((await posted).postData() || "{}");
        assert(!body.ids.includes(foreignId), `the panel posted a foreign id: ${JSON.stringify(body.ids)}`);
        await sleep(900);

        await page.goto(foreignUrl, { waitUntil: "domcontentloaded" });
        await L.dismissUpdate(page);
        await page.waitForSelector("terminal-tabs .terminal-tab", { timeout: 10000 });
        const after = await stripOrder();
        assert(after.indexOf(foreignId) === foreignAt,
          `the foreign shell moved from seat ${foreignAt} to ${after.indexOf(foreignId)}: ${after.join(", ")}`);
        const seatsBefore = before.map((id, i) => (tabs.includes(id) ? i : -1)).filter((i) => i >= 0);
        const seatsAfter = after.map((id, i) => (tabs.includes(id) ? i : -1)).filter((i) => i >= 0);
        assert(JSON.stringify(seatsBefore) === JSON.stringify(seatsAfter),
          `the project's terminals changed seats ${seatsBefore} -> ${seatsAfter}`);
        assert(after[foreignAt - 1] !== before[foreignAt - 1] || after[foreignAt + 1] !== before[foreignAt + 1],
          `nothing was reordered at all: ${after.join(", ")}`);
      } finally {
        // Whatever happened above, the checks after this one expect the editor
        // with its panel, so the way back belongs here and not after the try.
        if (ownUrl) await L.deleteShell(page, ownUrl).catch(() => {});
        if (foreignUrl) await L.deleteShell(page, foreignUrl).catch(() => {});
        await L.deleteProject(page, other).catch(() => {});
        await openEditor().catch(() => {});
        await page.waitForSelector(`${panel} [data-term-tab]`, { timeout: 10000 }).catch(() => {});
        await page.click(`${panel} [data-term-tab]`).catch(() => {});
      }
    });

    await run("desktop: the refresh button reconnects the active pane's stream", async () => {
      const activeId = await page.evaluate(() => document.querySelector("[data-editor-term-panel] .editor-term-pane.active")?.getAttribute("data-term-pane"));
      const reqP = page.waitForRequest((r) => r.url().includes(`/${activeId}/stream`), { timeout: 8000 });
      await page.click(`${panel} [data-terminal-refresh]`);
      await reqP;
    });

    await run("desktop: the shell tab context menu mirrors the strip entries", async () => {
      const firstTab = await page.evaluate(() => document.querySelector("[data-editor-term-panel] [data-term-tab]")?.getAttribute("data-term-tab"));
      await page.click(`${panel} [data-term-tab="${firstTab}"]`, { button: "right" });
      await page.waitForSelector(".dc-context-menu", { timeout: 5000 });
      const labels = await page.evaluate(() => [...document.querySelectorAll(".dc-context-menu .dropdown-item")].map((b) => b.textContent.trim()).filter(Boolean));
      await page.keyboard.press("Escape");
      for (const want of ["Open terminal page", "Rename", "Open project", "Delete"]) {
        assert(labels.includes(want), `menu misses "${want}": ${labels.join(", ")}`);
      }
    });

    await run("desktop: the open state is per project", async () => {
      const other = `${project}-b`;
      await L.createProject(page, other);
      try {
        await page.goto(`${BASE}/projects/${other}/editor`, { waitUntil: "domcontentloaded" });
        await L.dismissUpdate(page);
        await page.waitForSelector(".cm-editor, .editor-textarea", { state: "attached", timeout: 15000 });
        assert(!(await panelVisible()), "the panel opened in a project that never opened it");
        const keys = await page.evaluate((p) => ({
          own: localStorage.getItem(`dc-editor-term-open:${p}-b`),
          main: localStorage.getItem(`dc-editor-term-open:${p}`),
        }), project);
        assert(keys.main === "1" && !keys.own, `open keys wrong: ${JSON.stringify(keys)}`);
      } finally {
        await L.deleteProject(page, other);
      }
      await openEditor();
      await page.waitForSelector(`${panel} [data-term-tab]`, { timeout: 10000 });
      assert(await panelVisible(), "the panel did not come back in the project that had it open");
    });

    await run("desktop: the tab menu renames a shell", async () => {
      const secondId = new URL(secondShellUrl).pathname.split("/").pop();
      await page.click(`${panel} [data-term-tab="${secondId}"]`, { button: "right" });
      await page.waitForSelector(".dc-context-menu", { timeout: 5000 });
      await page.evaluate(() => {
        const item = [...document.querySelectorAll(".dc-context-menu .dropdown-item")].find((b) => /rename/i.test(b.textContent));
        item.click();
      });
      await page.waitForSelector(".swal2-input", { state: "visible", timeout: 5000 });
      await page.fill(".swal2-input", `renamed-${tag.slice(-4)}`);
      await page.click(".swal2-confirm");
      let renamed = false;
      for (let i = 0; i < 15; i++) {
        renamed = await page.evaluate((id) => {
          const tab = document.querySelector(`[data-editor-term-panel] [data-term-tab="${id}"]`);
          return !!tab && /renamed-/.test(tab.getAttribute("data-term-name") || "");
        }, secondId);
        if (renamed) break;
        await sleep(400);
      }
      assert(renamed, "the rename never reached the tab");
    });

    await run("desktop: the tab menu opens the terminal's own page", async () => {
      const secondId = new URL(secondShellUrl).pathname.split("/").pop();
      await page.click(`${panel} [data-term-tab="${secondId}"]`, { button: "right" });
      await page.waitForSelector(".dc-context-menu", { timeout: 5000 });
      await Promise.all([
        page.waitForURL(new RegExp(`/shells/${secondId}`), { timeout: 10000 }),
        page.evaluate(() => {
          const item = [...document.querySelectorAll(".dc-context-menu .dropdown-item")].find((b) => /open terminal page/i.test(b.textContent));
          item.click();
        }),
      ]);
      await openEditor();
      await page.waitForSelector(`${panel} [data-term-tab]`, { timeout: 10000 });
    });

    await run("desktop: the panel comes back open after a reload and reconnects", async () => {
      assert(await panelVisible(), "the panel did not restore its open state");
      await page.waitForSelector(`${panel} .editor-term-pane.active terminal-attach[embedded] .xterm-screen canvas`, { timeout: 15000 });
    });

    await run("desktop: the last active tab comes back after a reload", async () => {
      const ids = await page.evaluate(() => [...document.querySelectorAll("[data-editor-term-panel] [data-term-tab]")].map((t) => t.getAttribute("data-term-tab")));
      const current = await page.evaluate(() => document.querySelector("[data-editor-term-panel] .editor-term-pane.active")?.getAttribute("data-term-pane"));
      const target = ids.find((id) => id !== current);
      assert(target, "no second tab to switch to");
      await page.click(`${panel} [data-term-tab="${target}"]`);
      await sleep(500);
      await openEditor();
      await page.waitForSelector(`${panel} [data-term-tab]`, { timeout: 10000 });
      let active = null;
      for (let i = 0; i < 15; i++) {
        active = await page.evaluate(() => document.querySelector("[data-editor-term-panel] .editor-term-pane.active")?.getAttribute("data-term-pane"));
        if (active === target) break;
        await sleep(400);
      }
      assert(active === target, `the reload lost the last tab (${active} instead of ${target})`);
    });

    // The id in a ?terminal= may name a session that is gone by the time the
    // URL is opened again: a coder created from the panel is stopped, its tab
    // goes, and the address in the tab bar keeps the id. What the panel must
    // never do is end up with nothing marked at all, which is what a half
    // applied activation left behind.
    await run("desktop: a ?terminal= naming a session that is gone leaves a live tab active", async () => {
      const dead = "00000000-0000-4000-8000-000000000000";
      await page.goto(`${BASE}/projects/${project}/editor?terminal=${dead}`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      await page.waitForSelector(`${panel} [data-term-tab]`, { timeout: 15000 });
      await sleep(1200);
      const state = await page.evaluate(() => {
        const tabs = [...document.querySelectorAll("[data-editor-term-panel] [data-term-tab]")];
        return {
          tabs: tabs.length,
          activeTabs: tabs.filter((t) => t.classList.contains("active")).length,
          activePanes: document.querySelectorAll("[data-editor-term-panel] .editor-term-pane.active").length,
        };
      });
      assert(state.tabs > 0, "no tab left to fall back to");
      assert(state.activeTabs === 1, `${state.activeTabs} tabs marked active instead of one`);
      assert(state.activePanes === 1, `${state.activePanes} panes active instead of one`);
    });

    await run("desktop: closing the active tab hands focus to the neighbor terminal", async () => {
      const secondId = new URL(secondShellUrl).pathname.split("/").pop();
      await page.click(`${panel} [data-term-tab="${secondId}"]`);
      await sleep(500);
      const before = await tabCount();
      await page.hover(`${panel} [data-term-tab="${secondId}"]`);
      await page.click(`${panel} [data-term-tab="${secondId}"] [data-term-close]`);
      await confirmSwal(page);
      let count = before;
      for (let i = 0; i < 15; i++) { count = await tabCount(); if (count === before - 1) break; await sleep(400); }
      assert(count === before - 1, `the tab did not go (${count} of ${before})`);
      secondShellUrl = null;
      const paneGone = await page.evaluate((id) => !document.querySelector(`[data-term-pane="${id}"]`), secondId);
      assert(paneGone, "the pane of the deleted shell is still mounted");
      let focused = false;
      for (let i = 0; i < 12; i++) {
        focused = await page.evaluate(() => {
          const active = document.querySelector("[data-editor-term-panel] .editor-term-pane.active");
          return !!active && !!document.activeElement && active.contains(document.activeElement);
        });
        if (focused) break;
        await sleep(400);
      }
      assert(focused, "the neighbor terminal did not take focus");
    });

    // The close asks for a refresh that focuses the neighbour, and the delete
    // publishes the terminals event, whose refresh focuses nothing. Whichever
    // of the two lands last wins, so the event's refresh is made to start
    // after the close's own here, the order that used to drop the focus.
    await run("desktop: a terminals refresh overtaking the close's own keeps the neighbor's focus", async () => {
      const page2 = await page.context().newPage();
      let thirdUrl = null;
      try {
        thirdUrl = await L.createShell(page2, project);
      } finally {
        await page2.close().catch(() => {});
      }
      const thirdId = new URL(thirdUrl).pathname.split("/").pop();
      await page.waitForSelector(`${panel} [data-term-tab="${thirdId}"]`, { timeout: 8000 });
      await page.click(`${panel} [data-term-tab="${thirdId}"]`);
      await page.waitForSelector(`${panel} [data-term-pane="${thirdId}"].active terminal-attach[embedded]`, { timeout: 10000 });
      await sleep(500);
      let answered = false;
      let overtaken = false;
      const deletes = new RegExp(`/shells/${thirdId}/delete$`);
      const refreshes = /\/editor\/terminals(\?|$)/;
      await page.route(deletes, async (route) => {
        const res = await route.fetch();
        await sleep(1500);
        answered = true;
        await route.fulfill({ response: res });
      });
      await page.route(refreshes, async (route) => {
        if (answered && !overtaken) {
          overtaken = true;
          await page.evaluate(() => document.dispatchEvent(new CustomEvent("dc:terminals", { detail: null })));
        }
        await route.continue();
      });
      try {
        await page.hover(`${panel} [data-term-tab="${thirdId}"]`);
        await page.click(`${panel} [data-term-tab="${thirdId}"] [data-term-close]`);
        await confirmSwal(page);
        await page.waitForFunction((id) => !document.querySelector(`[data-term-tab="${id}"]`), thirdId, { timeout: 10000 });
        thirdUrl = null;
        let focused = false;
        for (let i = 0; i < 12; i++) {
          focused = await page.evaluate(() => {
            const active = document.querySelector("[data-editor-term-panel] .editor-term-pane.active");
            return !!active && !!document.activeElement && active.contains(document.activeElement);
          });
          if (focused) break;
          await sleep(400);
        }
        assert(overtaken, "no refresh started after the close's own, so this proves nothing");
        assert(focused, "the neighbor terminal did not take focus");
      } finally {
        await page.unroute(deletes);
        await page.unroute(refreshes);
        if (thirdUrl) await L.deleteShell(page, thirdUrl).catch(() => {});
      }
    });

    // A session that vanishes from somewhere else leaves the panel on a pane
    // activated without focus. Its buttons must still show at once, while the
    // keyboard stays wherever it was.
    await run("desktop: the active session vanishing elsewhere shows the next pane's buttons without taking focus", async () => {
      const page2 = await page.context().newPage();
      let goneUrl = null;
      try {
        goneUrl = await L.createShell(page2, project);
        const goneId = new URL(goneUrl).pathname.split("/").pop();
        await page.waitForSelector(`${panel} [data-term-tab="${goneId}"]`, { timeout: 8000 });
        const firstId = await page.evaluate((gone) => [...document.querySelectorAll("[data-editor-term-panel] [data-term-tab]")]
          .map((t) => t.getAttribute("data-term-tab")).find((id) => id !== gone), goneId);
        assert(firstId, "no other tab to fall back to, so this proves nothing");
        await page.click(`${panel} [data-term-tab="${firstId}"]`);
        await page.waitForSelector(`${panel} [data-term-pane="${firstId}"].active terminal-attach[embedded]`, { timeout: 10000 });
        await page.click(`${panel} [data-term-tab="${goneId}"]`);
        await page.waitForSelector(`${panel} [data-term-pane="${goneId}"].active terminal-attach[embedded]`, { timeout: 10000 });
        await sleep(500);
        const copy = `${panel} [data-terminal-footer="${firstId}"] [data-terminal-copy]`;
        assert(!(await page.locator(copy).isVisible()), "the first pane's copy button shows while another pane is active");
        await page.evaluate(() => document.activeElement?.blur());
        await L.deleteShell(page2, goneUrl);
        goneUrl = null;
        await page.waitForFunction((id) => !document.querySelector(`[data-term-tab="${id}"]`), goneId, { timeout: 10000 });
        await page.waitForSelector(`${panel} [data-term-pane="${firstId}"].active`, { timeout: 8000 });
        await page.waitForSelector(copy, { state: "visible", timeout: 4000 });
        await sleep(400);
        const focus = await page.evaluate(() => {
          const el = document.activeElement;
          return { inTerminal: !!el?.closest("terminal-attach"), onBody: el === document.body };
        });
        assert(!focus.inTerminal, "the activation moved the focus into the terminal");
        assert(focus.onBody, "the activation moved the focus");
      } finally {
        if (goneUrl) await L.deleteShell(page2, goneUrl).catch(() => {});
        await page2.close().catch(() => {});
      }
    });

    let coderId = null;
    await run("desktop: a coder created from the + menu returns to the editor with its tab active", async () => {
      await page.click(`${panel} [data-editor-term-plus]`);
      await page.waitForSelector(`${panel} .dropdown-menu.show`, { timeout: 5000 });
      // The entry opens the create dialog (see coders.js) over the editor; the
      // panel marker rides the form's action through the POST, so the create
      // still comes back to this editor with the new tab named.
      await page.click(`${panel} .dropdown-menu.show a.dropdown-item[href*="/coders/new"]`);
      await page.waitForSelector("[data-form-modal].show form", { timeout: 10000 });
      const f = page.locator('[data-form-modal] form:has(input[name="name"])').first();
      await f.locator('input[name="name"]').fill(`edt-${tag.slice(-6)}`);
      // The editor page stays under the dialog and already carries a ?terminal=
      // from an earlier check, so the landing is the move to a different one.
      const from = page.url();
      await f.locator('button[type="submit"]').first().click();
      await page.waitForURL((u) => /\/projects\/[^/]+\/editor\?terminal=/.test(u.href) && u.href !== from, { timeout: 30000 });
      coderId = new URL(page.url()).searchParams.get("terminal");
      assert(coderId, "the redirect carries no terminal parameter");
      await page.waitForSelector(`${panel} [data-term-pane="${coderId}"].active terminal-attach[embedded] .xterm-screen canvas`, { timeout: 20000 });
      const tabActive = await page.evaluate((id) => document.querySelector(`[data-editor-term-panel] [data-term-tab="${id}"]`)?.classList.contains("active"), coderId);
      assert(tabActive, "the new coder's tab is not active");
      let focused = false;
      for (let i = 0; i < 15; i++) {
        focused = await page.evaluate((id) => {
          const pane = document.querySelector(`[data-editor-term-panel] [data-term-pane="${id}"]`);
          return !!pane && !!document.activeElement && pane.contains(document.activeElement);
        }, coderId);
        if (focused) break;
        await sleep(400);
      }
      assert(focused, "the new coder's terminal is not focused");
    });

    await run("desktop: the coder pane carries the files button and the attach page's modal", async () => {
      assert(coderId, "no coder from the previous check");
      const btn = page.locator(`${panel} [data-term-foot="${coderId}"] .coder-files-button`);
      let visible = false;
      for (let i = 0; i < 10; i++) { visible = await btn.isVisible(); if (visible) break; await sleep(300); }
      assert(visible, "the files button is not visible for the active coder");
      await btn.click();
      await page.waitForFunction((id) => document.getElementById(`coder-files-modal-${id}`)?.classList.contains("show"), coderId, { timeout: 8000 });
      const action = await page.evaluate((id) => document.querySelector(`#coder-files-modal-${id} form[data-coder-file-upload-form]`)?.getAttribute("action"), coderId);
      assert(action === `/coders/${coderId}/files`, `upload form action is ${action}`);
      await page.click(`#coder-files-modal-${coderId} .btn-close`);
      await page.waitForFunction((id) => !document.getElementById(`coder-files-modal-${id}`)?.classList.contains("show"), coderId, { timeout: 8000 });
    });

    await run("desktop: a coder's copy button opens its conversation, Screen turns it to the screen and back", async () => {
      assert(coderId, "no coder from the previous check");
      const pane = `${panel} [data-term-pane="${coderId}"]`;
      const view = `${pane} terminal-copy`;
      const button = `${panel} [data-terminal-footer="${coderId}"] [data-terminal-copy]`;
      await page.waitForSelector(button, { state: "visible", timeout: 6000 });
      assert(await page.locator(`${button} .ti-messages`).count() === 1, "a coder's copy button does not wear the messages icon");
      const conversation = page.waitForResponse((r) => r.url().endsWith(`/coders/${coderId}/conversation`), { timeout: 10000 });
      await page.click(button);
      await page.waitForSelector(`${view}:not([hidden])`, { timeout: 6000 });
      assert((await conversation).ok(), "the conversation answered an error");
      await page.waitForSelector(`${view} [data-copy-waiting]`, { state: "hidden", timeout: 10000 });
      const face = (sel) => page.evaluate((v) => {
        const el = document.querySelector(v);
        return {
          title: el.querySelector("[data-copy-title]").textContent,
          log: !el.querySelector("[data-copy-log]").hidden,
          text: !el.querySelector("[data-copy-text]").hidden,
          screenBtn: !el.querySelector('[data-copy-face="text"]').hidden,
          convBtn: !el.querySelector('[data-copy-face="conversation"]').hidden,
          behind: document.querySelector(v.replace(" terminal-copy", " terminal-attach")).classList.contains("attach-terminal-behind"),
        };
      }, sel);
      const conv = await face(view);
      assert(conv.title === "Conversation" && conv.log && !conv.text && conv.screenBtn && !conv.convBtn && conv.behind,
        `the coder's view did not open on its conversation: ${JSON.stringify(conv)}`);
      const screen = page.waitForResponse((r) => r.url().includes(`/coders/${coderId}/copy`), { timeout: 10000 });
      await page.click(`${view} [data-copy-face="text"]`);
      assert((await screen).ok(), "the screen answered an error");
      await page.waitForFunction((v) => (document.querySelector(`${v} [data-copy-text]`)?.textContent || "").trim().length > 0, view, { timeout: 8000 });
      const scr = await face(view);
      assert(scr.title === "Screen" && !scr.log && scr.text && !scr.screenBtn && scr.convBtn, `Screen did not turn the frame: ${JSON.stringify(scr)}`);
      await page.click(`${view} [data-copy-face="conversation"]`);
      await page.waitForFunction((v) => document.querySelector(`${v} [data-copy-title]`)?.textContent === "Conversation", view, { timeout: 8000 });
      const again = await face(view);
      assert(again.log && !again.text && again.screenBtn, `Conversation did not turn the frame back: ${JSON.stringify(again)}`);
      await page.click(button);
      await page.waitForSelector(view, { state: "hidden", timeout: 4000 });
      const left = await face(view);
      assert(!left.behind, "the terminal stayed behind after the copy button closed the view");
    });

    await run("desktop: the coder tab menu mirrors the strip entries", async () => {
      assert(coderId, "no coder from the previous check");
      await page.click(`${panel} [data-term-tab="${coderId}"]`, { button: "right" });
      await page.waitForSelector(".dc-context-menu", { timeout: 5000 });
      const labels = await page.evaluate(() => [...document.querySelectorAll(".dc-context-menu .dropdown-item")].map((b) => b.textContent.trim()).filter(Boolean));
      await page.keyboard.press("Escape");
      for (const want of ["Open terminal page", "Steer", "Open project", "Stop", "Delete"]) {
        assert(labels.includes(want), `menu misses "${want}": ${labels.join(", ")}`);
      }
    });

    await run("desktop: a stopped coder shows up in the + menu and resumes into its tab", async () => {
      assert(coderId, "no coder from the previous check");
      await page.click(`${panel} [data-term-tab="${coderId}"]`, { button: "right" });
      await page.waitForSelector(".dc-context-menu", { timeout: 5000 });
      await page.evaluate(() => {
        [...document.querySelectorAll(".dc-context-menu .dropdown-item")].find((b) => b.textContent.trim() === "Stop").click();
      });
      await confirmSwal(page);
      let stopped = false;
      for (let i = 0; i < 20; i++) {
        stopped = await page.evaluate((id) => !document.querySelector(`[data-editor-term-panel] [data-term-tab="${id}"]`), coderId);
        if (stopped) break;
        await sleep(400);
      }
      assert(stopped, "the coder tab did not go after the stop");
      let listed = false;
      for (let i = 0; i < 15; i++) {
        listed = await page.evaluate((id) => !!document.querySelector(`[data-editor-term-resume-host] [data-resume-id="${id}"]`), coderId);
        if (listed) break;
        await sleep(400);
      }
      assert(listed, "the stopped coder is not offered in the + menu");
      await page.click(`${panel} [data-editor-term-plus]`);
      await page.waitForSelector(`${panel} .dropdown-menu.show`, { timeout: 5000 });
      const header = await page.evaluate(() => [...document.querySelectorAll("[data-editor-term-resume-host] .dropdown-header")].map((h) => h.textContent.trim()));
      assert(header.includes("Inactive coders"), `no Inactive coders header (${header.join(", ")})`);
      await page.click(`[data-editor-term-resume-host] [data-resume-id="${coderId}"]`);
      await page.waitForSelector(`${panel} [data-term-tab="${coderId}"]`, { timeout: 30000 });
      await page.waitForSelector(`${panel} [data-term-pane="${coderId}"].active terminal-attach[embedded]`, { timeout: 20000 });
    });

    await run("desktop: the coder purge delete cleans the session up", async () => {
      assert(coderId, "no coder from the previous check");
      await page.click(`${panel} [data-term-tab="${coderId}"]`, { button: "right" });
      await page.waitForSelector(".dc-context-menu", { timeout: 5000 });
      await page.evaluate(() => {
        const items = [...document.querySelectorAll(".dc-context-menu .dropdown-item")];
        items.reverse().find((b) => b.textContent.trim() === "Delete").click();
      });
      await confirmSwal(page);
      let gone = false;
      for (let i = 0; i < 20; i++) {
        gone = await page.evaluate((id) => !document.querySelector(`[data-editor-term-panel] [data-term-tab="${id}"]`)
          && !document.querySelector(`[data-editor-term-resume-host] [data-resume-id="${id}"]`), coderId);
        if (gone) break;
        await sleep(400);
      }
      assert(gone, "the coder did not go away after the delete");
      coderId = null;
    });

    // The panel asks whether some input points finely or none is coarse, never
    // the primary pointer: a Windows Edge seen over a remote desktop reports no
    // pointer and no hover at all, and an iPad with a trackpad a coarse primary
    // pointer beside a fine one. In both the panel stands and its terminal takes
    // the keyboard straight into xterm, the phone's cursor input would be one
    // nothing in the panel ever focuses. No emulation reaches the media
    // features apart, Chromium's blink settings do: a browser of its own per
    // pointer setup, logged into the same instance.
    const pointerProbe = async (blink, viewport, typeMarker, drag = false) => {
      const { chromium } = require("playwright-core");
      const b = await chromium.launch({ args: ["--no-sandbox", `--blink-settings=${blink}`] });
      let shellId = null;
      let dragShellId = null;
      try {
        const p = await (await b.newContext({ ignoreHTTPSErrors: true, viewport })).newPage();
        await L.login(p);
        await p.goto(`${BASE}/projects/${project}/editor`, { waitUntil: "domcontentloaded" });
        await L.dismissUpdate(p);
        await p.waitForSelector(".cm-editor, .editor-textarea", { state: "attached", timeout: 15000 });
        await sleep(300);
        const got = await p.evaluate(() => {
          const media = {};
          for (const q of ["(hover: hover)", "(pointer: fine)", "(pointer: coarse)", "(any-hover: hover)", "(any-pointer: fine)", "(any-pointer: coarse)"]) media[q] = matchMedia(q).matches;
          const shown = (el) => !!el && !el.hidden && el.getBoundingClientRect().width > 0;
          return {
            media,
            button: shown(document.querySelector("[data-editor-term-status]")),
            item: !document.querySelector("[data-editor-term-item]").hidden,
            panelDisplay: getComputedStyle(document.querySelector("[data-editor-term-panel]")).display,
            cssDisplay: (() => {
              const el = document.querySelector("[data-editor-term-panel]");
              const hidden = el.hidden;
              el.hidden = false;
              const display = getComputedStyle(el).display;
              el.hidden = hidden;
              return display;
            })(),
          };
        });
        if (!got.button || !typeMarker) return got;
        await p.click("[data-editor-term-status]");
        await p.waitForFunction(() => document.querySelector("[data-editor-term-panel]").getBoundingClientRect().height > 0, null, { timeout: 6000 });
        got.panel = true;
        const known = await p.$$eval(`${panel} [data-term-tab]`, (els) => els.map((el) => el.getAttribute("data-term-tab")));
        await p.click(`${panel} [data-editor-term-plus]`);
        await p.waitForSelector(`${panel} .dropdown-menu.show`, { timeout: 5000 });
        await p.click(`${panel} [data-editor-term-new]`);
        shellId = await p.waitForFunction((k) => {
          const tab = [...document.querySelectorAll("[data-editor-term-panel] [data-term-tab]")].find((el) => !k.includes(el.getAttribute("data-term-tab")));
          return tab ? tab.getAttribute("data-term-tab") : null;
        }, known, { timeout: 15000 }).then((h) => h.jsonValue());
        const pane = `${panel} [data-term-pane="${shellId}"]`;
        await p.waitForSelector(`${pane}.active terminal-attach[embedded] .xterm-screen canvas`, { timeout: 15000 });
        await sleep(1400);
        got.stayedActive = !!(await p.$(`${pane}.active`));
        if (!got.stayedActive) {
          await p.click(`${panel} [data-term-tab="${shellId}"]`);
          await p.waitForSelector(`${pane}.active terminal-attach[embedded] .xterm-screen canvas`, { timeout: 15000 });
        }
        got.interactive = await p.evaluate((sel) => ({
          classed: document.querySelector(`${sel} terminal-attach`).classList.contains("attach-terminal-interactive"),
          cursorInput: !!document.querySelector(`${sel} #terminal-cursor-input`),
        }), pane);
        await p.click(`${pane} .xterm-screen`);
        await p.keyboard.type(`echo ${typeMarker}`);
        await p.keyboard.press("Enter");
        got.echoed = await p.waitForFunction(([sel, want]) => (document.querySelector(`${sel} .attach-selection`)?.textContent || "").includes(want),
          [pane, typeMarker], { timeout: 8000 }).then(() => true, () => false);
        if (drag) {
          const order = () => p.$$eval(`${panel} [data-term-tab]`, (els) => els.map((el) => el.getAttribute("data-term-tab")));
          const mid = (box) => ({ x: box.x + box.width / 2, y: box.y + box.height / 2 });
          if ((await order()).length < 2) {
            const known2 = await order();
            await p.click(`${panel} [data-editor-term-plus]`);
            await p.waitForSelector(`${panel} .dropdown-menu.show`, { timeout: 5000 });
            await p.click(`${panel} [data-editor-term-new]`);
            dragShellId = await p.waitForFunction((k) => {
              const tab = [...document.querySelectorAll("[data-editor-term-panel] [data-term-tab]")].find((el) => !k.includes(el.getAttribute("data-term-tab")));
              return tab ? tab.getAttribute("data-term-tab") : null;
            }, known2, { timeout: 15000 }).then((h) => h.jsonValue());
            await sleep(900);
          }
          const posts = [];
          p.on("request", (r) => {
            if (r.url().includes("/terminal-tabs/order") && r.method() === "POST") posts.push(JSON.parse(r.postData() || "{}").ids);
          });

          const before = await order();
          const a = await p.locator(`${panel} [data-term-tab="${before[0]}"]`).boundingBox();
          const bb = await p.locator(`${panel} [data-term-tab="${before[1]}"]`).boundingBox();
          await p.mouse.move(mid(a).x, mid(a).y);
          await p.mouse.down();
          await p.mouse.move(bb.x + bb.width - 4, mid(bb).y, { steps: 10 });
          await p.mouse.up();
          let mouseAfter = before;
          for (let i = 0; i < 15 && mouseAfter[0] !== before[1]; i++) {
            await sleep(400);
            mouseAfter = await order();
          }
          got.mouseDrag = { before, posted: posts.slice(), after: mouseAfter };
          await sleep(900);

          // A real finger on the strip is taken by the browser as a pan and
          // cancelled before any gate is asked, so the touch drag is dispatched
          // as touch typed pointer events straight onto the tab, which is the
          // one path where only the handler's own refusal keeps it still.
          const touchBefore = await order();
          const postsBefore = posts.length;
          await p.evaluate(([sel, from, to]) => {
            const tab = document.querySelector(`${sel} [data-term-tab="${from}"]`);
            const target = document.querySelector(`${sel} [data-term-tab="${to}"]`).getBoundingClientRect();
            const start = tab.getBoundingClientRect();
            const y = start.top + start.height / 2;
            const x0 = start.left + start.width / 2;
            const x1 = target.right - 4;
            const init = (x, buttons) => ({ bubbles: true, cancelable: true, composed: true, pointerId: 91, pointerType: "touch", isPrimary: true, button: 0, buttons, clientX: x, clientY: y });
            tab.dispatchEvent(new PointerEvent("pointerdown", init(x0, 1)));
            for (let i = 1; i <= 10; i++) tab.dispatchEvent(new PointerEvent("pointermove", init(x0 + ((x1 - x0) * i) / 10, 1)));
            tab.dispatchEvent(new PointerEvent("pointerup", init(x1, 0)));
          }, [panel, touchBefore[0], touchBefore[1]]);
          await sleep(1500);
          got.touchDrag = { before: touchBefore, posted: posts.slice(postsBefore), after: await order() };
        }
        return got;
      } finally {
        if (dragShellId) await L.deleteShell(page, `${BASE}/shells/${dragShellId}`).catch(() => {});
        if (shellId) await L.deleteShell(page, `${BASE}/shells/${shellId}`).catch(() => {});
        await b.close();
      }
    };
    const desk = { width: 1360, height: 900 };
    const allFalse = (m) => Object.values(m).every((v) => !v);

    await run("desktop: a browser reporting no pointer and no hover gets the panel and types into its terminal", async () => {
      if (engine !== "chromium") return "skipped, the pointer setup is a chromium launch flag";
      const marker = `ENP${tag.slice(-4)}`;
      const got = await pointerProbe("primaryPointerType=1,availablePointerTypes=1,primaryHoverType=1,availableHoverTypes=1", desk, marker);
      assert(allFalse(got.media), `the pointer setup did not take: ${JSON.stringify(got.media)}`);
      assert(got.button && got.item && got.panel, `no pointer at all hides the terminal panel: ${JSON.stringify(got)}`);
      assert(got.interactive.classed && !got.interactive.cursorInput, `the panel terminal runs in phone mode: ${JSON.stringify(got.interactive)}`);
      assert(got.echoed, `the typed ${marker} never reached the terminal`);
      return `${marker} typed and echoed${got.stayedActive ? "" : ", the new tab had lost its activation to a refresh"}`;
    });

    await run("desktop: a coarse primary pointer beside a fine one gets the panel and types into its terminal", async () => {
      if (engine !== "chromium") return "skipped, the pointer setup is a chromium launch flag";
      const marker = `ECP${tag.slice(-4)}`;
      const got = await pointerProbe("primaryPointerType=2,availablePointerTypes=6,primaryHoverType=1,availableHoverTypes=3", desk, marker);
      assert(got.media["(pointer: coarse)"] && got.media["(any-pointer: fine)"], `the pointer setup did not take: ${JSON.stringify(got.media)}`);
      assert(got.button && got.item && got.panel, `a fine any-pointer behind a coarse primary one hides the terminal panel: ${JSON.stringify(got)}`);
      assert(got.interactive.classed && !got.interactive.cursorInput, `the panel terminal runs in phone mode: ${JSON.stringify(got.interactive)}`);
      assert(got.echoed, `the typed ${marker} never reached the terminal`);
      return `${marker} typed and echoed${got.stayedActive ? "" : ", the new tab had lost its activation to a refresh"}`;
    });

    // The tab drag is refused for a touch pointer and for nothing else: a mouse
    // on a machine whose media answer no pointer, or a coarse primary one, still
    // sorts the tabs, while a finger on the strip must not start a drag.
    const assertDrags = (got) => {
      const m = got.mouseDrag;
      assert(m.posted.length === 1 && m.posted[0][0] === m.before[1] && m.posted[0][1] === m.before[0],
        `the mouse drag posted ${JSON.stringify(m.posted)} for ${m.before.join(", ")}`);
      assert(m.after[0] === m.before[1] && m.after[1] === m.before[0], `the mouse drag did not reorder the tabs (${m.after.join(", ")})`);
      const t = got.touchDrag;
      assert(t.posted.length === 0, `the touch drag posted an order: ${JSON.stringify(t.posted)}`);
      assert(JSON.stringify(t.after) === JSON.stringify(t.before), `the touch drag reordered the tabs (${t.before.join(", ")} to ${t.after.join(", ")})`);
    };

    await run("desktop: with no pointer and no hover reported a mouse drags panel tabs, a touch does not", async () => {
      if (engine !== "chromium") return "skipped, the pointer setup is a chromium launch flag";
      const got = await pointerProbe("primaryPointerType=1,availablePointerTypes=1,primaryHoverType=1,availableHoverTypes=1", desk, `EDG${tag.slice(-4)}`, true);
      assert(allFalse(got.media), `the pointer setup did not take: ${JSON.stringify(got.media)}`);
      assertDrags(got);
      return "mouse reordered, touch left the order alone";
    });

    await run("desktop: with a coarse primary pointer beside a fine one a mouse drags panel tabs, a touch does not", async () => {
      if (engine !== "chromium") return "skipped, the pointer setup is a chromium launch flag";
      const got = await pointerProbe("primaryPointerType=2,availablePointerTypes=6,primaryHoverType=1,availableHoverTypes=3", desk, `EDG${tag.slice(-4)}`, true);
      assert(got.media["(pointer: coarse)"] && got.media["(any-pointer: fine)"], `the pointer setup did not take: ${JSON.stringify(got.media)}`);
      assertDrags(got);
      return "mouse reordered, touch left the order alone";
    });

    await run("desktop: a touch only device and a phone width keep the panel away", async () => {
      if (engine !== "chromium") return "skipped, the pointer setup is a chromium launch flag";
      const touch = await pointerProbe("primaryPointerType=2,availablePointerTypes=2,primaryHoverType=1,availableHoverTypes=1", desk, null);
      assert(touch.media["(any-pointer: coarse)"] && !touch.media["(any-pointer: fine)"], `the pointer setup did not take: ${JSON.stringify(touch.media)}`);
      assert(!touch.button && !touch.item && touch.panelDisplay === "none", `a touch only device offers the terminal panel: ${JSON.stringify(touch)}`);
      assert(touch.cssDisplay === "none", `the stylesheet alone shows the panel on a touch only device: ${JSON.stringify(touch)}`);
      const phone = await pointerProbe("primaryPointerType=1,availablePointerTypes=1,primaryHoverType=1,availableHoverTypes=1", { width: 390, height: 844 }, null);
      assert(!phone.button && !phone.item && phone.panelDisplay === "none", `a phone width offers the terminal panel: ${JSON.stringify(phone)}`);
      assert(phone.cssDisplay === "none", `the stylesheet alone shows the panel at a phone width: ${JSON.stringify(phone)}`);
      const fine = await pointerProbe("primaryPointerType=4,availablePointerTypes=4,primaryHoverType=2,availableHoverTypes=2", desk, null);
      assert(fine.cssDisplay !== "none", `the stylesheet hides the panel for a fine pointer on a desktop width: ${JSON.stringify(fine)}`);
      return "hidden for touch only and at 390, by the stylesheet alone too";
    });

    await run("mobile: the panel and its menu entry stay away", async () => {
      const mp = await mobilePage();
      await mp.goto(`${BASE}/projects/${project}/editor`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(mp);
      await mp.waitForSelector(".cm-editor, .editor-textarea", { state: "attached", timeout: 15000 });
      const state = await mp.evaluate(() => ({
        panelDisplay: getComputedStyle(document.querySelector("[data-editor-term-panel]")).display,
        itemHidden: document.querySelector("[data-editor-term-item]").hidden,
      }));
      assert(state.panelDisplay === "none", "the panel renders on mobile");
      assert(state.itemHidden, "the Terminal menu entry shows on mobile");
    });
  } finally {
    try {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      const links = await page.evaluate((p) => {
        const scope = document.querySelector(`[data-project-name="${p}"]`)?.closest('[id^="project-"]');
        return scope ? [...scope.querySelectorAll('a[href^="/shells/"]')].map((a) => a.getAttribute("href")) : [];
      }, project);
      for (const href of new Set(links)) {
        await L.deleteShell(page, `${BASE}${href}`).catch(() => {});
      }
      await L.deleteProject(page, project);
    } catch (e) {
      console.log("cleanup: " + e.message);
    }
  }
});
