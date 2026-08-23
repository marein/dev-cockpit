const L = require("./lib");
const { assert, sleep, confirmSwal, BASE } = L;

// Editor terminal panel: the project's live coders and shells inside the editor
// page, desktop only (fine pointer, wide viewport). The panel sits below the
// editor surface in the pane column, opens through the kebab menu's Terminal
// entry or Ctrl+J, remembers its open state and height per device
// (dc-editor-term-open / dc-editor-term-height), and hides completely on mobile
// widths and coarse pointers. Content comes from the fragment
// GET /projects/:name/editor/terminals (tabs plus empty pane divs); the client
// mounts a terminal-attach/terminal-input island pair into a pane on first
// activation, so hidden panes hold no stream. Islands carry the `embedded`
// attribute: rows fit the panel height like fullscreen fits the viewport, the
// terminal fullscreen shortcuts stay off the page, and a hidden pane does not
// connect. The panel refreshes over the app wide `terminals` event and marks a
// pane's news read when it is activated. The + button POSTs /shells/new with
// the project path and activates the new shell's tab. Tab context menu: open
// terminal page, rename (shells), stop/delete. Editor keyboard shortcuts stay
// away while focus is inside the panel; Ctrl+J toggles from both sides.
// The + button is a dropdown (New coder link with project preselect, New shell
// direct create), Cmd+T opens it while focus is inside the panel and the
// arrows walk it (bootstrap's own dropdown keys). Inside the panel Ctrl+Tab
// steps through the terminal tabs and Ctrl/Cmd+Shift+X asks to close the
// active one, mirroring the attach pages. Tabs drag-reorder with the mouse and
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
// A coder created through the + menu comes back to the editor: the create
// form's action carries the return target plus the panel=1 marker, the server
// redirects to .../editor?terminal=<id> and the panel activates that tab
// (without the marker, e.g. from the quick nav, a create lands on the coder's
// own page). Coder panes get
// the attach page's files modal (fragment-rendered per coder, kept alive
// across refreshes like the panes) behind a [data-terminal-footer] button the
// active island unhides, so drop and paste uploads run through
// coder-file-upload itself.
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
      const stored = await page.evaluate(() => parseInt(localStorage.getItem("dc-editor-term-height") || "0", 10));
      assert(Math.abs(stored - after) < 8, `height not persisted (${stored} vs ${after})`);
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
      const labels = await page.evaluate(() => [...document.querySelectorAll(".dc-context-menu button")].map((b) => b.textContent.trim()).filter(Boolean));
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
        const item = [...document.querySelectorAll(".dc-context-menu button")].find((b) => /rename/i.test(b.textContent));
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
          const item = [...document.querySelectorAll(".dc-context-menu button")].find((b) => /open terminal page/i.test(b.textContent));
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

    let coderId = null;
    await run("desktop: a coder created from the + menu returns to the editor with its tab active", async () => {
      await page.click(`${panel} [data-editor-term-plus]`);
      await page.waitForSelector(`${panel} .dropdown-menu.show`, { timeout: 5000 });
      await Promise.all([
        page.waitForURL(/\/coders\/new/, { timeout: 10000 }),
        page.click(`${panel} .dropdown-menu.show a.dropdown-item[href*="/coders/new"]`),
      ]);
      const f = page.locator('form:has(input[name="name"])').first();
      await f.locator('input[name="name"]').fill(`edt-${tag.slice(-6)}`);
      await Promise.all([
        page.waitForURL(/\/projects\/[^/]+\/editor\?terminal=/, { timeout: 30000 }),
        f.locator('button[type="submit"]').first().click(),
      ]);
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

    await run("desktop: Ctrl+Shift+Enter in the terminal reaches the editor fullscreen, the files modal stays usable there", async () => {
      assert(coderId, "no coder from the previous check");
      await page.click(`${panel} [data-term-pane="${coderId}"] .xterm-screen`);
      await page.keyboard.press("Control+Shift+Enter");
      await sleep(400);
      let fullscreen = await page.evaluate(() => document.documentElement.classList.contains("dc-editor-fullscreen"));
      assert(fullscreen, "the fullscreen toggle did not reach the editor");
      await page.click(`${panel} [data-term-foot="${coderId}"] .coder-files-button`);
      await page.waitForFunction((id) => document.getElementById(`coder-files-modal-${id}`)?.classList.contains("show"), coderId, { timeout: 8000 });
      await page.click(`#coder-files-modal-${coderId} .btn-close`, { timeout: 5000 });
      await page.waitForFunction((id) => !document.getElementById(`coder-files-modal-${id}`)?.classList.contains("show"), coderId, { timeout: 8000 });
      await page.click(`${panel} [data-term-pane="${coderId}"] .xterm-screen`);
      await page.keyboard.press("Control+Shift+Enter");
      await sleep(400);
      fullscreen = await page.evaluate(() => document.documentElement.classList.contains("dc-editor-fullscreen"));
      assert(!fullscreen, "the second toggle did not leave fullscreen");
    });

    // The float never outranks something that asks for interaction, and the
    // fullscreen editor is no exception to that: it ducks to the same 5 there,
    // which sinks it behind the fullscreen surface for as long as the popup
    // stands. Deliberate, a float that stayed above the surface could never be
    // covered by a dropdown inside it. An exception here would be the bug.
    await run("desktop: the host float ducks under everything, in fullscreen too", async () => {
      const zs = await page.evaluate(() => {
        const float = document.createElement("div");
        float.className = "dc-host-float";
        document.body.appendChild(float);
        const resting = getComputedStyle(float).zIndex;
        const menu = document.createElement("div");
        menu.className = "dropdown-menu show";
        document.body.appendChild(menu);
        const ducked = getComputedStyle(float).zIndex;
        document.documentElement.classList.add("dc-editor-fullscreen");
        const fullscreen = getComputedStyle(float).zIndex;
        document.documentElement.classList.remove("dc-editor-fullscreen");
        float.remove();
        menu.remove();
        return { resting, ducked, fullscreen };
      });
      assert(zs.resting === "1045", `resting z-index is ${zs.resting}`);
      assert(zs.ducked === "5", `ducked z-index is ${zs.ducked}`);
      assert(zs.fullscreen === "5", `the fullscreen duck was lifted to ${zs.fullscreen}`);
    });

    await run("desktop: the coder tab menu mirrors the strip entries", async () => {
      assert(coderId, "no coder from the previous check");
      await page.click(`${panel} [data-term-tab="${coderId}"]`, { button: "right" });
      await page.waitForSelector(".dc-context-menu", { timeout: 5000 });
      const labels = await page.evaluate(() => [...document.querySelectorAll(".dc-context-menu button")].map((b) => b.textContent.trim()).filter(Boolean));
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
        [...document.querySelectorAll(".dc-context-menu button")].find((b) => b.textContent.trim() === "Stop").click();
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
        const items = [...document.querySelectorAll(".dc-context-menu button")];
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
