const L = require("./lib");
const { assert, sleep, confirmSwal } = L;

// Split view: several live terminals grouped into one tab, rendered in columns
// of stacked panes on GET /splits/:id. Group membership lives in tmux user
// options (@dc_tab_group/@dc_tab_gpos/@dc_tab_gname/@dc_tab_gcol), so it is
// cross-device and dies with the sessions. Routes: GET /splits/:id (?focus=<id>
// renders that pane active; member solo URLs 303-redirect here), POST
// /terminal-tabs/group (also persists the pane order and, optionally, a `cols`
// array parallel to `ids`), POST /terminal-tabs/ungroup, POST
// /terminal-tabs/group/name (empty name removes it).
// The columns: @dc_tab_gcol says which members share one, a member without it
// is a column of its own (which is what every group was before), and a column
// stands where its first member stands in the flat @dc_tab_gpos order. The
// panes are flat siblings of one CSS grid, so a column change is a style change
// and never a DOM move: the streams stay connected. The pane head drag is
// two-dimensional here — sideways joins another column, up and down sorts
// inside one, and only a drop on an outer edge opens a column, which is also
// how two side by side panes swap places. The rows setting is the height of the
// page from here on: a column shows about that many lines in total, stacked
// panes share them minus their heads, and stacking never changes the page
// height. Creating into a split rides the create routes themselves (`group`
// plus `column`, a member of the target column, through the query and the
// form): the pane head's menu creates into that pane's column, the group tab's
// menu into a column of its own at the right edge, and the strip's + menu stays
// a standalone create everywhere. Custom elements: one
// terminal-attach/terminal-input pair per member paired via terminal-id; the
// island touched last carries `active` and receives every untargeted input
// (contextual per-member footer, prompt dialog); typing into a pane's xterm
// stays scoped through the bubbled event's origin island. terminal-split
// owns the pane heads (context menu, drag reorder, Ctrl+Shift+Arrow pane
// switch) and mirrors the strip's live refresh into the page (members,
// order, names — a group change from anywhere re-renders the open split).
// The group name is derived from the member names when the split carries none
// of its own, so that mirror also feeds the page heading ([data-split-title])
// and the browser title; the touch header keeps the focused member's name but
// leaves the title alone.
// Every close control kills for real after a confirm (strip tab X, pane head
// X). The keyboard has both targets and never mixes them up: Ctrl/Cmd+Shift+X
// belongs to the strip tab, which in a split is the whole group, and
// Ctrl+Shift+Backspace takes the active pane alone (Ctrl only, no Cmd variant:
// Cmd+Shift+Backspace clears the browsing data in the mac browsers).
// Ungrouping without killing lives in the context
// menus and the quick nav swipes. On mobile the settings row above the
// terminal carries the active member's type badge (data-terminal-badge,
// coder icon only with several coders, shells always), toggled with the
// active pane by the same sync that flips the footers. The quick nav renders groups as blocks
// (member sort, remove, group with the same dwell drag as the strip).
// Gotchas: drag-to-group dwell must be waited out with the pointer still
// down; the quick nav refreshes its list shortly after opening (settle
// ~800ms before measuring); shells need a moment before bash echoes input;
// the floating quick nav only exists below lg (the assistant's corner button
// replaces it from 992px up), so its checks run in a 900px window; a member
// of a split has no tab of its own in the strip, so the shared delete helper
// finds nothing to click and a member is closed through its pane head.

L.runFeature("SPLIT VIEW", async ({ page, run, mobilePage, engine }) => {
  const tag = `split-${Date.now().toString(36)}`;
  const project = `zztc-${tag}`;
  const shellUrls = [];
  const ids = [];
  let gid = null;

  const tabSel = (id) => `terminal-tabs .terminal-tab[data-tab-id="${id}"]`;
  const groupTabSel = "terminal-tabs .terminal-tab-split";
  const paneSel = (id) => `.attach-split-pane:has(terminal-attach[terminal-id="${id}"])`;
  const paneText = (id) => page.locator(`${paneSel(id)} .attach-selection`).textContent();

  const renameShell = async (url, name) => {
    await page.goto(url, { waitUntil: "domcontentloaded" });
    await page.click("[data-rename-label]");
    await page.waitForSelector("[data-rename-input]:not(.d-none)", { timeout: 4000 });
    await page.fill("[data-rename-input]", name);
    await page.keyboard.press("Enter");
    await sleep(600);
  };

  const typeInto = async (id, text) => {
    await page.click(`terminal-attach[terminal-id="${id}"]`);
    await sleep(200);
    await page.keyboard.type(text, { delay: 15 });
    await page.keyboard.press("Enter");
    await sleep(1200);
  };

  const contextItem = async (sel, label) => {
    await page.click(sel, { button: "right" });
    const item = page.locator(".dc-context-menu .dropdown-item", { hasText: label }).first();
    await item.waitFor({ state: "visible", timeout: 4000 });
    return item;
  };

  const groupVia = (memberIds) => page.evaluate(async (list) => {
    const token = document.querySelector('meta[name="csrf-token"]').content;
    const r = await fetch("/terminal-tabs/group", { method: "POST", headers: { "X-CSRF-Token": token, "Content-Type": "application/json" }, body: JSON.stringify({ ids: list }) });
    return r.json();
  }, memberIds);

  const steadyBox = async (p, selector) => {
    for (let i = 0; i < 15; i += 1) {
      const box = await p.locator(selector).first().boundingBox().catch(() => null);
      if (box) return box;
      await sleep(200);
    }
    throw new Error(`no stable box for ${selector}`);
  };

  // The floating quick nav is the primary navigation below lg only: from 992px
  // up the assistant's corner button replaces it, so its checks need a window
  // that still carries it. Fine pointer either way, the drags stay mouse driven.
  const withQuickNav = async (fn) => {
    await page.setViewportSize({ width: 900, height: 900 });
    await sleep(500);
    try {
      return await fn();
    } finally {
      await page.setViewportSize({ width: 1360, height: 900 });
      await sleep(500);
    }
  };

  try {
    await L.createProject(page, project);

    await run("setup: two shells created and named", async () => {
      shellUrls.push(await L.createShell(page, project));
      shellUrls.push(await L.createShell(page, project));
      ids.push(...shellUrls.map((u) => new URL(u).pathname.split("/").pop()));
      await renameShell(shellUrls[0], "alpha");
      await renameShell(shellUrls[1], "bravo");
      await page.goto(shellUrls[1], { waitUntil: "domcontentloaded" });
      await page.waitForSelector(tabSel(ids[0]), { state: "attached", timeout: 8000 });
    });

    await run("dragging a tab onto another (with dwell) creates the split and navigates to it", async () => {
      await sleep(800);
      const src = await steadyBox(page, tabSel(ids[1]));
      const dst = await steadyBox(page, tabSel(ids[0]));
      await page.mouse.move(src.x + src.width / 2, src.y + src.height / 2);
      await page.mouse.down();
      for (let i = 1; i <= 8; i++) {
        await page.mouse.move(
          src.x + src.width / 2 + (dst.x + dst.width / 2 - src.x - src.width / 2) * (i / 8),
          dst.y + dst.height / 2,
          { steps: 2 },
        );
        await sleep(20);
      }
      const highlighted = await page.waitForSelector(".terminal-tab-group-target", { timeout: 3000 }).catch(() => null);
      await sleep(400);
      await page.mouse.up();
      assert(highlighted, "group target never highlighted during dwell");
      await page.waitForURL(/\/splits\/[^/]+$/, { timeout: 10000 });
      gid = new URL(page.url()).pathname.split("/").pop();
    });

    await run("split page renders one island per member with its own stream", async () => {
      await page.waitForSelector(".attach-split-pane .xterm-screen canvas", { timeout: 15000 });
      const canvases = await page.locator(".attach-split-pane .xterm-screen canvas").count();
      assert(canvases >= 2, `expected 2 pane canvases, got ${canvases}`);
      const islands = await page.$$eval("terminal-attach[terminal-id]", (els) => els.map((el) => el.getAttribute("terminal-id")));
      assert(islands.length === 2 && islands.includes(ids[0]) && islands.includes(ids[1]), `islands: ${islands}`);
      const heads = (await page.locator(".attach-split-head").allTextContents()).join(" ");
      assert(heads.includes("alpha") && heads.includes("bravo"), `pane heads: ${heads}`);
    });

    await run("strip folds the members into one split tab with both ids and a single icon", async () => {
      await page.waitForSelector(groupTabSel, { state: "attached", timeout: 8000 });
      const members = await page.getAttribute(groupTabSel, "data-tab-members");
      assert(members === `${ids[0]} ${ids[1]}`, `members: ${members}`);
      assert(!(await page.$(tabSel(ids[0]))), "member still rendered as its own tab");
      const label = await page.getAttribute(groupTabSel, "data-tab-name");
      assert(label.includes("alpha") && label.includes("bravo"), `label: ${label}`);
      assert(await page.$(`${groupTabSel} [data-tab-icon] .ti-layout`), "group tab misses the single split icon");
      const targets = await page.getAttribute(`${groupTabSel} [data-tab-icon]`, "data-notify-targets");
      assert(targets === `${ids[0]} ${ids[1]}`, `aggregated notify targets: ${targets}`);
    });

    await run("a member link redirects to the split with that pane focused", async () => {
      await page.goto(shellUrls[0], { waitUntil: "domcontentloaded" });
      await page.waitForURL(new RegExp(`/splits/${gid}\\?focus=${ids[0]}`), { timeout: 8000 });
      await page.waitForSelector(".attach-split-pane .xterm-screen canvas", { timeout: 15000 });
      const active = await page.getAttribute("terminal-attach[active]", "terminal-id");
      assert(active === ids[0], `focused pane: ${active}`);
      assert(!(await page.$eval(`[data-terminal-footer="${ids[0]}"]`, (el) => el.hidden)), "focused pane's footer hidden");
      await page.waitForSelector(`${groupTabSel}.active`, { state: "attached", timeout: 8000 });
    });

    await run("mobile: a member link shows only that pane, like the old solo page", async () => {
      const mp = await mobilePage();
      await mp.goto(shellUrls[1], { waitUntil: "domcontentloaded" });
      await mp.waitForURL(new RegExp(`/splits/${gid}\\?focus=${ids[1]}`), { timeout: 8000 });
      await mp.waitForSelector(`terminal-attach[terminal-id="${ids[1]}"] .xterm-screen canvas`, { timeout: 15000 });
      const visible = await mp.$$eval(".attach-split-pane", (panes) => panes.filter((p) => p.offsetParent !== null).length);
      assert(visible === 1, `visible panes on mobile: ${visible}`);
      const active = await mp.getAttribute("terminal-attach[active]", "terminal-id");
      assert(active === ids[1], `active pane on mobile: ${active}`);
      assert(!(await mp.$(`terminal-attach[terminal-id="${ids[0]}"] canvas`)), "hidden pane booted its terminal");
    });

    await run("mobile: member header, no pager chips, quick nav shows the group block and switches", async () => {
      const mp = await mobilePage();
      const headerName = (await mp.textContent(".dc-coarse-only [data-rename-label]")).trim();
      assert(headerName === "bravo", `mobile header shows: ${headerName}`);
      const badge = await mp.$eval(`.attach-settings [data-terminal-badge="${ids[1]}"]`, (e) => ({ hidden: e.hidden, shown: e.offsetParent !== null }));
      assert(!badge.hidden && badge.shown, "settings row badge for the active pane not visible");
      const otherBadgesHidden = await mp.$$eval(`.attach-settings [data-terminal-badge]:not([data-terminal-badge="${ids[1]}"])`, (els) => els.every((e) => e.hidden));
      assert(otherBadgesHidden, "inactive pane badges visible in the settings row");
      assert(!(await mp.$(".attach-split-pager")), "pager chips still rendered");
      const swipeStops = await mp.$$eval("terminal-tabs .terminal-tab-split [data-member-url]", (els) => els.map((el) => el.getAttribute("data-member-url")));
      assert(
        JSON.stringify(swipeStops) === JSON.stringify([`/splits/${gid}?focus=${ids[0]}`, `/splits/${gid}?focus=${ids[1]}`]),
        `swipe member stops: ${swipeStops}`,
      );
      await mp.click(".quicknav-toggle");
      await mp.waitForSelector(`[data-qn-block="${gid}"]`, { state: "visible", timeout: 8000 });
      await sleep(800);
      const memberRows = await mp.$$eval(
        `[data-qn-block="${gid}"] [data-qn-group-member] .quicknav-active-item`,
        (els) => els.map((el) => el.dataset.tabId),
      );
      assert(JSON.stringify(memberRows) === JSON.stringify([ids[0], ids[1]]), `group members in quick nav: ${memberRows}`);
      assert(
        await mp.$(`[data-qn-block="${gid}"] [data-qn-group-member] .quicknav-active-item.active[data-tab-id="${ids[1]}"]`),
        "focused member row not marked active in the quick nav",
      );
      const context = await mp.$eval(".quicknav-context", (el) => el.textContent).catch(() => "");
      assert(context.includes(project), `quick nav project context: ${context}`);
      assert(await mp.$(`[data-qn-block="${gid}"] [data-qn-ungroup]`), "group row misses the ungroup swipe action");
      assert(await mp.$(`[data-qn-block="${gid}"] [data-qn-group] [data-qn-delete]`), "group row misses the close swipe action");
      assert(await mp.$(`[data-qn-block="${gid}"] [data-qn-group] [data-qn-rename]`), "group row misses the rename swipe action");
      assert(await mp.$(`[data-qn-block="${gid}"] [data-qn-group-member] [data-qn-remove]`), "member row misses the remove swipe action");
      assert(await mp.$(`[data-qn-block="${gid}"] [data-qn-group-member] [data-qn-rename]`), "member shell row misses the rename swipe action");
      const memberProjects = await mp.$$eval(
        `[data-qn-block="${gid}"] [data-qn-group-member] .quicknav-active-item .text-secondary`,
        (els) => els.map((el) => el.textContent.trim()),
      );
      assert(memberProjects.length === 2 && memberProjects.every((p) => p === project), `member project labels: ${memberProjects}`);
      await mp.click(`[data-qn-block="${gid}"] [data-qn-group-member] .quicknav-active-item[data-tab-id="${ids[0]}"]`);
      await mp.waitForURL(new RegExp(`/splits/${gid}\\?focus=${ids[0]}`), { timeout: 10000 });
      await mp.waitForSelector(`terminal-attach[terminal-id="${ids[0]}"] .xterm-screen canvas`, { timeout: 15000 });
      const headerAfter = (await mp.textContent(".dc-coarse-only [data-rename-label]")).trim();
      assert(headerAfter === "alpha", `mobile header after quick nav switch: ${headerAfter}`);
      await mp.goto(`${L.BASE}/projects`, { waitUntil: "domcontentloaded" });
    });

    await run("mobile: cancelling a create form returns to the focused pane, not the first member", async () => {
      const mp = await mobilePage();
      await mp.goto(shellUrls[1], { waitUntil: "domcontentloaded" });
      await mp.waitForURL(new RegExp(`/splits/${gid}\\?focus=${ids[1]}`), { timeout: 8000 });
      await mp.waitForSelector(`terminal-attach[terminal-id="${ids[1]}"] .xterm-screen canvas`, { timeout: 15000 });
      await mp.click(".quicknav-toggle");
      await mp.waitForSelector('[data-quicknav-pane="active"]', { state: "visible", timeout: 8000 });
      await sleep(800);
      await mp.click('[data-quicknav-pane="active"] a[href^="/shells/new"]');
      await mp.waitForURL(/\/shells\/new/, { timeout: 10000 });
      await mp.locator("a", { hasText: "Cancel" }).first().click();
      await mp.waitForURL(new RegExp(`/splits/${gid}\\?focus=${ids[1]}`), { timeout: 10000 });
      await mp.waitForSelector(`terminal-attach[terminal-id="${ids[1]}"] .xterm-screen canvas`, { timeout: 15000 });
      const active = await mp.getAttribute("terminal-attach[active]", "terminal-id");
      assert(active === ids[1], `active pane after cancel: ${active}`);
      await mp.goto(`${L.BASE}/projects`, { waitUntil: "domcontentloaded" });
    });

    await run("typing into a pane reaches only that pane's shell", async () => {
      await sleep(1500);
      await typeInto(ids[0], `echo A_${tag}`);
      await typeInto(ids[1], `echo B_${tag}`);
      const textA = await paneText(ids[0]);
      const textB = await paneText(ids[1]);
      assert(textA.includes(`A_${tag}`), "pane A missing its own output");
      assert(textB.includes(`B_${tag}`), "pane B missing its own output");
      assert(!textA.includes(`B_${tag}`), "pane A leaked pane B input");
      assert(!textB.includes(`A_${tag}`), "pane B leaked pane A input");
    });

    await run("the last touched island carries the active attribute", async () => {
      await page.click(`terminal-attach[terminal-id="${ids[0]}"]`);
      let active = await page.getAttribute("terminal-attach[active]", "terminal-id");
      assert(active === ids[0], `active after clicking A: ${active}`);
      await page.click(`terminal-attach[terminal-id="${ids[1]}"]`);
      active = await page.getAttribute("terminal-attach[active]", "terminal-id");
      assert(active === ids[1], `active after clicking B: ${active}`);
    });

    await run("Ctrl+Shift+Arrow switches the active pane", async () => {
      await page.keyboard.press("Control+Shift+ArrowRight");
      await page.waitForFunction(
        (id) => document.querySelector("terminal-attach[active]")?.getAttribute("terminal-id") === id,
        ids[0],
        { timeout: 4000 },
      );
      await page.keyboard.press("Control+Shift+ArrowLeft");
      await page.waitForFunction(
        (id) => document.querySelector("terminal-attach[active]")?.getAttribute("terminal-id") === id,
        ids[1],
        { timeout: 4000 },
      );
    });

    await run("holding Ctrl+Shift+Arrow keeps stepping through the panes", async () => {
      if (engine !== "chromium") return "skipped (autoRepeat needs CDP)";
      await page.evaluate(() => {
        window.__paneSwitches = 0;
        document.addEventListener("dc:activate-pane", () => { window.__paneSwitches += 1; });
      });
      const cdp = await page.context().newCDPSession(page);
      const ctrlShift = 10;
      const key = { key: "ArrowRight", code: "ArrowRight", windowsVirtualKeyCode: 39, nativeVirtualKeyCode: 39, modifiers: ctrlShift };
      await cdp.send("Input.dispatchKeyEvent", { type: "rawKeyDown", ...key });
      for (let i = 0; i < 4; i++) {
        await sleep(250);
        await cdp.send("Input.dispatchKeyEvent", { type: "rawKeyDown", autoRepeat: true, ...key });
      }
      await cdp.send("Input.dispatchKeyEvent", { type: "keyUp", ...key });
      await cdp.detach();
      const switches = await page.evaluate(() => window.__paneSwitches);
      assert(switches >= 3, `held key produced only ${switches} pane switches`);
      const active = await page.evaluate(() => document.querySelector("terminal-attach[active]")?.getAttribute("terminal-id"));
      if (active !== ids[1]) {
        await page.keyboard.press("Control+Shift+ArrowRight");
        await page.waitForFunction(
          (id) => document.querySelector("terminal-attach[active]")?.getAttribute("terminal-id") === id,
          ids[1],
          { timeout: 4000 },
        );
      }
      return `${switches} switches while held`;
    });

    await run("shell panes get the shell footer: scroll pad, no prompt or files buttons", async () => {
      const footer = `[data-terminal-footer="${ids[1]}"]`;
      await page.waitForFunction((sel) => {
        const el = document.querySelector(sel);
        return el && !el.hidden;
      }, footer, { timeout: 4000 });
      assert(!(await page.$(`${footer} [data-terminal-prompt-modal-open]`)), "shell footer offers the prompt dialog");
      assert(!(await page.$(`${footer} .coder-files-button`)), "shell footer offers the files modal");
      assert(await page.$(`${footer} terminal-direction-pad[up-control="scroll-up"]`), "shell footer misses the scroll pad");
      assert(await page.$eval(`[data-terminal-footer="${ids[0]}"]`, (el) => el.hidden), "inactive pane's footer is visible");
    });

    await run("news in a visible inactive pane is auto-read, the local changed dot stays until activation", async () => {
      await page.click(`terminal-attach[terminal-id="${ids[1]}"]`);
      await sleep(200);
      await page.keyboard.type("sleep 2", { delay: 15 });
      await page.keyboard.press("Enter");
      await page.click(`.attach-split-pane[data-pane-id="${ids[0]}"] [data-pane-head]`);
      await page.waitForFunction(
        (id) => document.querySelector(`.attach-split-pane[data-pane-id="${id}"] [data-notify-target].changed`),
        ids[1],
        { timeout: 20000 },
      );
      await sleep(1500);
      assert(!(await page.$(`.attach-split-pane[data-pane-id="${ids[1]}"] [data-notify-target].news`)), "visible pane kept global news");
      assert(!(await page.$("terminal-tabs .terminal-tab-split .dc-term-icon.news")), "group tab shows news for a visible pane");
      assert(await page.$(`.attach-split-pane[data-pane-id="${ids[1]}"] [data-notify-target].changed`), "changed dot missing");
      await page.click(`.attach-split-pane[data-pane-id="${ids[1]}"] [data-pane-head]`);
      await page.waitForFunction(
        (id) => !document.querySelector(`.attach-split-pane[data-pane-id="${id}"] [data-notify-target].changed`),
        ids[1],
        { timeout: 8000 },
      );
    });

    await run("the last active pane is remembered and restored on the next plain open", async () => {
      await page.goto(`${L.BASE}/splits/${gid}`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(".attach-split-pane .xterm-screen canvas", { timeout: 15000 });
      await page.waitForFunction(
        (id) => document.querySelector("terminal-attach[active]")?.getAttribute("terminal-id") === id,
        ids[1],
        { timeout: 4000 },
      );
      assert(!(await page.$eval(`[data-terminal-footer="${ids[1]}"]`, (el) => el.hidden)), "remembered pane's footer hidden");
    });

    await run("the remembered pane also survives a boosted strip navigation", async () => {
      await page.click(`terminal-attach[terminal-id="${ids[0]}"]`);
      await sleep(300);
      const extraUrl = await L.createShell(page, project);
      await page.waitForSelector(groupTabSel, { state: "attached", timeout: 8000 });
      await page.click(groupTabSel);
      await page.waitForURL(new RegExp(`/splits/${gid}$`), { timeout: 10000 });
      await page.waitForFunction(
        (id) => document.querySelector("terminal-attach[active]")?.getAttribute("terminal-id") === id,
        ids[0],
        { timeout: 4000 },
      );
      assert(!(await page.$eval(`[data-terminal-footer="${ids[0]}"]`, (el) => el.hidden)), "remembered pane's footer hidden after boosted nav");
      await L.deleteShell(page, extraUrl);
      await page.goto(`${L.BASE}/splits/${gid}`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(".attach-split-pane .xterm-screen canvas", { timeout: 15000 });
    });

    // The pane head drag is two-dimensional since the grid layout: sideways
    // into another column joins it, up and down sorts inside it, and only a
    // drop on an outer edge opens a column of its own — which is also how two
    // side by side panes swap places.
    const dragPane = async (fromId, xRatio, yRatio = null) => {
      const head = await steadyBox(page, `.attach-split-pane[data-pane-id="${fromId}"] [data-pane-head]`);
      const container = await steadyBox(page, "terminal-split");
      const startX = head.x + head.width / 2;
      const startY = head.y + head.height / 2;
      const endX = container.x + container.width * xRatio;
      const endY = yRatio === null ? startY : container.y + container.height * yRatio;
      await page.mouse.move(startX, startY);
      await page.mouse.down();
      for (let i = 1; i <= 10; i++) {
        await page.mouse.move(startX + (endX - startX) * (i / 10), startY + (endY - startY) * (i / 10), { steps: 2 });
        await sleep(25);
      }
      await page.mouse.up();
    };
    const membersAre = (expected) => page.waitForFunction(
      (want) => document.querySelector("terminal-tabs .terminal-tab-split")?.getAttribute("data-tab-members") === want,
      expected,
      { timeout: 8000 },
    );
    // The rendered columns, left to right and top to bottom. The flat order
    // (@dc_tab_gpos) is a different reading of the same panes, so both are
    // measured where they matter.
    const columnsView = () => page.$$eval(".attach-split-pane", (panes) => {
      const byCol = new Map();
      for (const pane of panes) {
        const col = Number(pane.dataset.paneCol) || 0;
        const row = parseInt(pane.style.gridRow, 10) || 1;
        if (!byCol.has(col)) byCol.set(col, []);
        byCol.get(col).push({ id: pane.dataset.paneId, row });
      }
      return [...byCol.entries()]
        .sort((a, b) => a[0] - b[0])
        .map(([, items]) => items.sort((a, b) => a.row - b.row).map((item) => item.id));
    });
    // The layout as the page shows it: the panes in their flat order with the
    // column each one renders in.
    const layout = () => page.$$eval(".attach-split-pane", (panes) => panes
      .map((p) => ({ id: p.dataset.paneId, col: Number(p.dataset.paneCol) || 0, order: Number(p.style.order) || 0 }))
      .sort((a, b) => a.order - b.order)
      .map((p) => `${p.id}:${p.col}`));

    await run("dragging a pane head onto an outer edge reorders the columns and persists via gpos", async () => {
      await dragPane(ids[0], 0.98);
      await membersAre(`${ids[1]} ${ids[0]}`);
      const firstVisual = await page.$$eval(".attach-split-pane", (panes) => panes
        .map((p) => ({ id: p.dataset.paneId, order: Number(p.style.order) || 0 }))
        .sort((a, b) => a.order - b.order)[0].id);
      assert(firstVisual === ids[1], `left pane after reorder: ${firstVisual}`);
      await dragPane(ids[0], 0.02);
      await membersAre(`${ids[0]} ${ids[1]}`);
    });

    // ---- The column grid ----------------------------------------------------
    // A split arranges its panes in columns of stacked rows: @dc_tab_gcol says
    // which members share a column, @dc_tab_gpos stays the one global order and
    // stacks them inside it. The panes stay flat siblings of one CSS grid, so a
    // column change is a style change and never a DOM move.
    let thirdUrl = null;
    let thirdId = null;
    let stackedId = null;
    const splitHeight = () => page.$eval("terminal-split", (el) => Math.round(el.getBoundingClientRect().height));
    const screenHeights = () => page.$$eval(".attach-split-pane", (panes) => Object.fromEntries(panes.map((p) => [
      p.dataset.paneId,
      Math.round(p.querySelector(".xterm-screen")?.getBoundingClientRect().height || 0),
    ])));

    await run("a pane dragged into another column stacks there, and the rows budget keeps the page height", async () => {
      thirdUrl = await L.createShell(page, project);
      shellUrls.push(thirdUrl);
      thirdId = new URL(thirdUrl).pathname.split("/").pop();
      await page.goto(`${L.BASE}/splits/${gid}`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(tabSel(thirdId), { state: "attached", timeout: 8000 });
      await groupVia([ids[0], ids[1], thirdId]);
      await page.waitForFunction(
        () => document.querySelectorAll("terminal-attach[terminal-id]").length === 3,
        undefined,
        { timeout: 15000 },
      );
      await page.waitForSelector(".attach-split-pane .xterm-screen canvas", { timeout: 20000 });
      await sleep(2000);
      const before = await layout();
      assert(
        JSON.stringify(before) === JSON.stringify([`${ids[0]}:1`, `${ids[1]}:2`, `${thirdId}:3`]),
        `three panes are three columns: ${before}`,
      );
      const heightBefore = await splitHeight();
      const tallBefore = (await screenHeights())[ids[0]];
      // Sideways into the first column, below its pane: joins that column.
      await dragPane(thirdId, 0.15, 0.75);
      await membersAre(`${ids[0]} ${thirdId} ${ids[1]}`);
      const after = await layout();
      assert(
        JSON.stringify(after) === JSON.stringify([`${ids[0]}:1`, `${thirdId}:1`, `${ids[1]}:2`]),
        `two stacked left, one right: ${after}`,
      );
      const cols = await page.$eval("terminal-split", (el) => el.style.getPropertyValue("--dc-split-cols").trim());
      assert(cols === "2", `grid columns after stacking: ${cols}`);
      await sleep(1500);
      const heightAfter = await splitHeight();
      assert(Math.abs(heightAfter - heightBefore) <= 2, `page height moved from ${heightBefore} to ${heightAfter}`);
      // The column's budget is shared: the two stacked terminals fit into the
      // height the single one had, minus the second pane head.
      const screens = await screenHeights();
      assert(
        screens[thirdId] > 0 && screens[thirdId] < tallBefore * 0.75 && screens[ids[1]] > tallBefore * 0.9,
        `stacked ${screens[thirdId]} / ${screens[ids[0]]} against the full column ${screens[ids[1]]} (was ${tallBefore})`,
      );
      return `${heightBefore}px stays ${heightAfter}px`;
    });

    await run("the column layout is group state: the strip carries it and a reload renders the same grid", async () => {
      const stripCols = await page.getAttribute(groupTabSel, "data-tab-member-cols");
      assert(stripCols === "1 1 2", `strip member columns: ${stripCols}`);
      await page.goto(`${L.BASE}/splits/${gid}`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(".attach-split-pane .xterm-screen canvas", { timeout: 20000 });
      const reloaded = await layout();
      assert(
        JSON.stringify(reloaded) === JSON.stringify([`${ids[0]}:1`, `${thirdId}:1`, `${ids[1]}:2`]),
        `layout after reload: ${reloaded}`,
      );
      const spans = await page.$$eval(".attach-split-pane", (panes) => Object.fromEntries(panes.map((p) => [p.dataset.paneId, p.style.gridRow])));
      assert(spans[ids[1]] === "1 / span 2", `the single pane column spans its whole height: ${spans[ids[1]]}`);
      assert(spans[ids[0]] === "1 / span 1" && spans[thirdId] === "2 / span 1", `stacked rows: ${JSON.stringify(spans)}`);
    });

    await run("mobile keeps one pane per page whatever the columns are", async () => {
      const mp = await mobilePage();
      await mp.goto(`${L.BASE}/splits/${gid}?focus=${thirdId}`, { waitUntil: "domcontentloaded" });
      await mp.waitForSelector(`terminal-attach[terminal-id="${thirdId}"] .xterm-screen canvas`, { timeout: 20000 });
      const visible = await mp.$$eval(".attach-split-pane", (panes) => panes.filter((p) => p.offsetParent !== null).length);
      assert(visible === 1, `visible panes on mobile with a stacked column: ${visible}`);
      const flat = await mp.$$eval("terminal-tabs .terminal-tab-split [data-member-url]", (els) => els.map((el) => el.getAttribute("data-member-url")));
      assert(
        JSON.stringify(flat) === JSON.stringify([ids[0], thirdId, ids[1]].map((id) => `/splits/${gid}?focus=${id}`)),
        `the swipe order stays flat and follows the columns: ${flat}`,
      );
      await mp.goto(`${L.BASE}/projects`, { waitUntil: "domcontentloaded" });
    });

    await run("a drop on the outer edge opens a column of its own again", async () => {
      await page.goto(`${L.BASE}/splits/${gid}`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(".attach-split-pane .xterm-screen canvas", { timeout: 20000 });
      await sleep(1200);
      await dragPane(thirdId, 0.99);
      await membersAre(`${ids[0]} ${ids[1]} ${thirdId}`);
      const after = await layout();
      assert(
        JSON.stringify(after) === JSON.stringify([`${ids[0]}:1`, `${ids[1]}:2`, `${thirdId}:3`]),
        `three columns again: ${after}`,
      );
      // Close it through its own pane head: a member has no tab of its own in
      // the strip, so the shared delete helper would find nothing to click.
      await page.click(`${paneSel(thirdId)} .attach-split-remove`);
      await confirmSwal(page);
      await page.waitForFunction(
        () => document.querySelectorAll("terminal-attach[terminal-id]").length === 2,
        undefined,
        { timeout: 15000 },
      );
      shellUrls.splice(shellUrls.indexOf(thirdUrl), 1);
      thirdUrl = null;
      await sleep(800);
      await page.waitForSelector(".attach-split-pane .xterm-screen canvas", { timeout: 20000 });
    });

    await run("quick nav: the group is a block, dragging a member reorders the panes", async () => withQuickNav(async () => {
      const memberRowSel = (index) => `[data-qn-block="${gid}"] [data-qn-group-member]:nth-of-type(${index + 2})`;
      const membersNow = () => page.getAttribute(groupTabSel, "data-tab-members");
      const membersAre = async (expected) => {
        await page.waitForFunction(
          (want) => document.querySelector("terminal-tabs .terminal-tab-split")?.getAttribute("data-tab-members") === want,
          expected,
          { timeout: 8000 },
        ).catch(async () => {
          throw new Error(`members are "${await membersNow()}", want "${expected}"`);
        });
        await sleep(900);
      };
      const dragMember = async (fromIndex, toIndex) => {
        const from = await steadyBox(page, `${memberRowSel(fromIndex)} [data-qn-drag-handle]`);
        const to = await steadyBox(page, memberRowSel(toIndex));
        const startY = from.y + from.height / 2;
        const endY = to.y + to.height * (toIndex > fromIndex ? 0.8 : 0.2);
        await page.mouse.move(from.x + from.width / 2, startY);
        await page.mouse.down();
        for (let i = 1; i <= 8; i++) {
          await page.mouse.move(from.x + from.width / 2, startY + (endY - startY) * (i / 8), { steps: 2 });
          await sleep(30);
        }
        await page.mouse.up();
      };
      await page.click(".quicknav-toggle");
      await page.waitForSelector(`[data-qn-block="${gid}"]`, { state: "visible", timeout: 8000 });
      await sleep(800);
      const start = (await membersNow()).split(" ");
      await dragMember(0, 1);
      await membersAre(`${start[1]} ${start[0]}`);
      await dragMember(1, 0);
      await membersAre(start.join(" "));
      await page.click(".quicknav-toggle");
      await sleep(400);
    }));

    await run("a pane head context menu offers pane actions and renames the shell in place", async () => {
      await page.click(`.attach-split-pane[data-pane-id="${ids[1]}"] [data-pane-head]`, { button: "right" });
      const item = page.locator(".dc-context-menu .dropdown-item", { hasText: "Rename" }).first();
      await item.waitFor({ state: "visible", timeout: 4000 });
      const labels = await page.$$eval(".dc-context-menu .dropdown-item", (els) => els.map((el) => el.textContent.trim()));
      assert(labels.some((l) => l.includes("Remove from split view")), `menu misses remove: ${labels}`);
      assert(labels.includes("Delete"), `menu misses delete: ${labels}`);
      await item.click();
      await page.waitForSelector(".swal2-input", { timeout: 4000 });
      await page.fill(".swal2-input", "bravo2");
      await page.click(".swal2-confirm");
      await page.waitForFunction(
        (id) => document.querySelector(`.attach-split-pane[data-pane-id="${id}"] [data-pane-label]`)?.textContent === "bravo2",
        ids[1],
        { timeout: 6000 },
      );
      // The group name is derived from the member names, so the page heading and
      // the browser title have to follow a member rename too.
      await page.waitForFunction(
        () => document.querySelector("[data-split-title]")?.textContent.includes("bravo2")
          && document.title.includes("bravo2"),
        null,
        { timeout: 6000 },
      );
    });

    await run("rename split view via the tab context menu, empty name restores the derived label", async () => {
      const item = await contextItem(groupTabSel, "Rename split view");
      await item.click();
      await page.waitForSelector(".swal2-input", { timeout: 4000 });
      await page.fill(".swal2-input", `duo-${tag.slice(-4)}`);
      await page.click(".swal2-confirm");
      await sleep(1000);
      let label = await page.getAttribute(groupTabSel, "data-tab-name");
      assert(label === `duo-${tag.slice(-4)}`, `label after rename: ${label}`);
      await page.waitForFunction(
        (name) => document.querySelector("[data-split-title]")?.textContent.trim() === name
          && document.title.startsWith(name),
        `duo-${tag.slice(-4)}`,
        { timeout: 6000 },
      );
      const again = await contextItem(groupTabSel, "Rename split view");
      await again.click();
      await page.waitForSelector(".swal2-input", { timeout: 4000 });
      await page.fill(".swal2-input", "");
      await page.click(".swal2-confirm");
      await page.waitForFunction(
        () => {
          const name = document.querySelector("terminal-tabs .terminal-tab-split")?.getAttribute("data-tab-name") || "";
          const heading = document.querySelector("[data-split-title]")?.textContent || "";
          return name.includes("alpha") && name.includes("bravo2")
            && heading === name && document.title.startsWith(name);
        },
        undefined,
        { timeout: 8000 },
      );
    });

    // ---- Creating a terminal into a split -----------------------------------
    // The pane head's menu creates into that pane's column, the strip's group
    // tab menu into a column of its own at the right edge. Both ride the
    // existing create routes: `group` and `column` (a member of the target
    // column) travel through the query and the form like `return` does, so one
    // request creates the terminal and puts it into the split.
    await run("'New shell here' from a pane head opens the form prefilled and lands in that pane's column", async () => {
      await page.goto(`${L.BASE}/splits/${gid}`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(".attach-split-pane .xterm-screen canvas", { timeout: 20000 });
      await sleep(1200);
      const item = await contextItem(`.attach-split-pane[data-pane-id="${ids[0]}"] [data-pane-head]`, "New shell here");
      await item.click();
      await page.waitForURL(/\/shells\/new/, { timeout: 10000 });
      const query = new URL(page.url()).searchParams;
      assert(query.get("group") === gid && query.get("column") === ids[0], `the form carries the target: ${page.url()}`);
      const fields = await page.$$eval("form input[type=hidden]", (els) => Object.fromEntries(els.map((e) => [e.name, e.value])));
      assert(fields.group === gid && fields.column === ids[0], `hidden fields: ${JSON.stringify(fields)}`);
      const selected = await page.$eval('select[name="project"]', (el) => el.value.split("/").pop());
      assert(selected === project, `the project select stands on the pane's project: ${selected}`);
      // The field stays editable, which project the new pane works in is the
      // person's decision.
      const editable = await page.$eval('select[name="project"]', (el) => !el.disabled && el.offsetParent !== null);
      assert(editable, "the project select is not editable");
      await page.locator('form:has(select[name="project"]) button[type="submit"]').first().click();
      await page.waitForURL(new RegExp(`/splits/${gid}\\?focus=`), { timeout: 20000 });
      const fresh = new URL(page.url()).searchParams.get("focus");
      stackedId = fresh;
      shellUrls.push(`${L.BASE}/shells/${fresh}`);
      await page.waitForFunction(
        () => document.querySelectorAll("terminal-attach[terminal-id]").length === 3,
        undefined,
        { timeout: 20000 },
      );
      await page.waitForSelector(".attach-split-pane .xterm-screen canvas", { timeout: 20000 });
      await sleep(1200);
      const columns = await columnsView();
      assert(
        JSON.stringify(columns) === JSON.stringify([[ids[0], fresh], [ids[1]]]),
        `the new pane stacks at the bottom of the pane's column: ${JSON.stringify(columns)}`,
      );
      // Its @dc_tab_gpos is the group's highest plus one, so it is last in the
      // flat order the strip, the quick nav and the mobile swipe walk — which
      // can differ from reading the columns left to right.
      const members = await page.getAttribute(groupTabSel, "data-tab-members");
      assert(members === `${ids[0]} ${ids[1]} ${fresh}`, `members after the create: ${members}`);
      const active = await page.getAttribute("terminal-attach[active]", "terminal-id");
      assert(active === fresh, `the new pane is the focused one: ${active}`);
      // The keyboard steps the visual order, columns left to right and top to
      // bottom, straight from the server render: the new pane sits between
      // its column neighbour and the next column even though the flat member
      // order lists it last.
      const stepping = await layout();
      assert(
        JSON.stringify(stepping) === JSON.stringify([`${ids[0]}:1`, `${fresh}:1`, `${ids[1]}:2`]),
        `pane order for the keyboard: ${stepping}`,
      );
      await page.keyboard.press("Control+Shift+ArrowLeft");
      await page.waitForFunction(
        (id) => document.querySelector("terminal-attach[active]")?.getAttribute("terminal-id") === id,
        ids[0],
        { timeout: 4000 },
      );
      await page.keyboard.press("Control+Shift+ArrowRight");
      await page.waitForFunction(
        (id) => document.querySelector("terminal-attach[active]")?.getAttribute("terminal-id") === id,
        fresh,
        { timeout: 4000 },
      );
    });

    await run("'New coder here' from the group tab opens the form prefilled and carries the target through it", async () => {
      const item = await contextItem(groupTabSel, "New coder here");
      await item.click();
      await page.waitForURL(/\/coders\/new/, { timeout: 10000 });
      const query = new URL(page.url()).searchParams;
      assert(query.get("group") === gid, `the form carries the split: ${page.url()}`);
      assert(!query.get("column"), `a split wide create names no column: ${page.url()}`);
      assert(query.get("project") === project, `the project is prefilled: ${query.get("project")}`);
      const fields = await page.$$eval("form input[type=hidden]", (els) => Object.fromEntries(els.map((e) => [e.name, e.value])));
      assert(fields.group === gid, `hidden group field: ${JSON.stringify(fields)}`);
      assert(fields.column === "", `hidden column field: ${JSON.stringify(fields)}`);
      const selected = await page.$eval('select[name="project"]', (el) => el.value.split("/").pop());
      assert(selected === project, `the project select stands on the source project: ${selected}`);
      // The field stays editable: which project the new pane works in is the
      // person's decision, so nothing here is disabled or hidden.
      const editable = await page.$eval('select[name="project"]', (el) => !el.disabled && el.offsetParent !== null);
      assert(editable, "the project select is not editable");
      // Send the form: the hidden fields carry the target through the POST, so
      // the coder starts and joins the split in one request.
      const form = page.locator('form:has(select[name="agent"])').first();
      await form.locator('input[name="name"]').fill(`sc-${tag.slice(-5)}`);
      await form.locator('button[type="submit"]').first().click();
      await page.waitForURL(new RegExp(`/splits/${gid}\\?focus=`), { timeout: 25000 });
      await page.waitForFunction(
        () => document.querySelectorAll("terminal-attach[terminal-id]").length === 4,
        undefined,
        { timeout: 20000 },
      );
      await sleep(1500);
      const coderId = new URL(page.url()).searchParams.get("focus");
      const columns = await columnsView();
      assert(
        JSON.stringify(columns) === JSON.stringify([[ids[0], stackedId], [ids[1]], [coderId]]),
        `the coder joined as a column of its own at the right edge: ${JSON.stringify(columns)}`,
      );
      await page.click(`.attach-split-pane[data-pane-id="${coderId}"] .attach-split-remove`);
      await confirmSwal(page);
      await page.waitForFunction(
        () => document.querySelectorAll("terminal-attach[terminal-id]").length === 3,
        undefined,
        { timeout: 20000 },
      );
      // The coder was stopped, not dropped: take its conversation with it.
      await page.evaluate(async (id) => {
        const token = document.querySelector('meta[name="csrf-token"]').content;
        await fetch(`/coders/${id}/delete`, { method: "POST", headers: { "X-CSRF-Token": token, Accept: "application/json" } });
      }, coderId);
      await sleep(800);
    });

    // A layout wish must never fail a create: the split may be gone by the
    // time the form comes back, and the terminal is what was asked for.
    await run("a create for a split that is gone still starts the terminal and lands on its own page", async () => {
      const projectDir = await L.projectPath(page, project);
      const path = await page.evaluate(async ({ projectDir, group }) => {
        const token = document.querySelector('meta[name="csrf-token"]').content;
        const response = await fetch("/shells/new", {
          method: "POST",
          headers: { "X-CSRF-Token": token, "Content-Type": "application/x-www-form-urlencoded" },
          body: new URLSearchParams({ project: projectDir, group }).toString(),
        });
        return new URL(response.url).pathname;
      }, { projectDir, group: "11111111-2222-3333-4444-555555555555" });
      assert(/^\/shells\/[0-9a-f-]+$/.test(path), `landed on ${path}`);
      const orphan = path.split("/").pop();
      await page.evaluate(async (id) => {
        const token = document.querySelector('meta[name="csrf-token"]').content;
        await fetch(`/shells/${id}/delete`, { method: "POST", headers: { "X-CSRF-Token": token, Accept: "application/json" } });
      }, orphan);
      await sleep(800);
      await page.goto(`${L.BASE}/splits/${gid}`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(".attach-split-pane .xterm-screen canvas", { timeout: 20000 });
    });

    await run("a split wide create opens a column of its own at the right edge", async () => {
      const item = await contextItem(groupTabSel, "New shell here");
      await item.click();
      await page.waitForURL(/\/shells\/new/, { timeout: 10000 });
      const query = new URL(page.url()).searchParams;
      assert(query.get("group") === gid && !query.get("column"), `a split wide create names no column: ${page.url()}`);
      await page.locator('form:has(select[name="project"]) button[type="submit"]').first().click();
      await page.waitForURL(new RegExp(`/splits/${gid}\\?focus=`), { timeout: 20000 });
      const fresh = new URL(page.url()).searchParams.get("focus");
      shellUrls.push(`${L.BASE}/shells/${fresh}`);
      await page.waitForFunction(
        () => document.querySelectorAll("terminal-attach[terminal-id]").length === 4,
        undefined,
        { timeout: 20000 },
      );
      await page.waitForSelector(".attach-split-pane .xterm-screen canvas", { timeout: 20000 });
      await sleep(1200);
      const columns = await columnsView();
      assert(
        JSON.stringify(columns) === JSON.stringify([[ids[0], stackedId], [ids[1]], [fresh]]),
        `the new pane is a column of its own at the right edge: ${JSON.stringify(columns)}`,
      );
      // Clean both extra panes out again through their own pane heads.
      for (const id of [stackedId, fresh]) {
        await page.click(`.attach-split-pane[data-pane-id="${id}"] .attach-split-remove`);
        await confirmSwal(page);
        await sleep(1500);
        shellUrls.splice(shellUrls.indexOf(`${L.BASE}/shells/${id}`), 1);
      }
      await page.waitForFunction(
        () => document.querySelectorAll("terminal-attach[terminal-id]").length === 2,
        undefined,
        { timeout: 20000 },
      );
      stackedId = null;
    });

    await run("the strip's + menu stays a standalone create, also on a split page", async () => {
      const entries = await page.$$eval("terminal-tabs [data-tabs-new]", (els) => els.map((el) => ({
        kind: el.dataset.tabsNew,
        href: el.getAttribute("href"),
      })));
      assert(entries.length === 2, `the + menu has its two create entries: ${JSON.stringify(entries)}`);
      assert(
        entries.every((e) => !/[?&]group=/.test(e.href)),
        `the + menu never carries a split target: ${JSON.stringify(entries)}`,
      );
    });

    await run("adding a terminal from the split page renders the new pane", async () => {
      const extraUrl = await L.createShell(page, project);
      const extraId = new URL(extraUrl).pathname.split("/").pop();
      await page.goto(`${L.BASE}/splits/${gid}`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(tabSel(extraId), { state: "attached", timeout: 8000 });
      await groupVia([ids[0], ids[1], extraId]);
      await page.waitForFunction(
        () => document.querySelectorAll("terminal-attach[terminal-id]").length === 3,
        undefined,
        { timeout: 10000 },
      );
      const members = await page.getAttribute(groupTabSel, "data-tab-members");
      assert(members === `${ids[0]} ${ids[1]} ${extraId}`, `members after add: ${members}`);
      await page.click(`${paneSel(extraId)} .attach-split-remove`);
      await confirmSwal(page);
      await page.waitForFunction(
        () => document.querySelectorAll("terminal-attach[terminal-id]").length === 2,
        undefined,
        { timeout: 15000 },
      );
      await sleep(800);
      assert(!(await page.$(tabSel(extraId))), "the pane close control did not delete the terminal");
      await page.waitForSelector(groupTabSel, { state: "attached", timeout: 8000 });
    });

    await run("the open split page follows group changes live: add, rename, remove", async () => {
      const extraUrl = await L.createShell(page, project);
      const extraId = new URL(extraUrl).pathname.split("/").pop();
      await page.goto(`${L.BASE}/splits/${gid}`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(".attach-split-pane .xterm-screen canvas", { timeout: 15000 });
      const api = async (url, jsonBody, formBody) => page.evaluate(async ({ url, jsonBody, formBody }) => {
        const token = document.querySelector('meta[name="csrf-token"]').content;
        const init = { method: "POST", headers: { "X-CSRF-Token": token } };
        if (jsonBody) {
          init.headers["Content-Type"] = "application/json";
          init.body = JSON.stringify(jsonBody);
        } else {
          init.headers["Content-Type"] = "application/x-www-form-urlencoded";
          init.body = new URLSearchParams(formBody).toString();
        }
        const response = await fetch(url, init);
        return response.status;
      }, { url, jsonBody, formBody });
      await api("/terminal-tabs/group", { ids: [ids[0], ids[1], extraId] });
      await page.waitForFunction(
        () => document.querySelectorAll("terminal-attach[terminal-id]").length === 3,
        undefined,
        { timeout: 15000 },
      );
      await api(`/shells/${extraId}/rename`, null, { name: "livename" });
      await page.waitForFunction(
        (id) => document.querySelector(`.attach-split-pane[data-pane-id="${id}"] [data-pane-label]`)?.textContent === "livename",
        extraId,
        { timeout: 10000 },
      );
      await api("/terminal-tabs/ungroup", { ids: [extraId] });
      await page.waitForFunction(
        () => document.querySelectorAll("terminal-attach[terminal-id]").length === 2,
        undefined,
        { timeout: 15000 },
      );
      await api(`/shells/${extraId}/delete`, null, {});
      await sleep(800);
      await page.waitForSelector(".attach-split-pane .xterm-screen canvas", { timeout: 15000 });
    });

    await run("the pane context menu removes one terminal, dissolving a two pane split onto the survivor", async () => {
      await page.click(`.attach-split-pane[data-pane-id="${ids[1]}"] [data-pane-head]`, { button: "right" });
      const item = page.locator(".dc-context-menu .dropdown-item", { hasText: "Remove from split view" }).first();
      await item.waitFor({ state: "visible", timeout: 4000 });
      await item.click();
      await page.waitForURL(new RegExp(`/shells/${ids[0]}$`), { timeout: 10000 });
      await sleep(600);
      assert(!(await page.$(groupTabSel)), "split tab survived the dissolve");
      await page.waitForSelector(tabSel(ids[1]), { state: "attached", timeout: 8000 });
    });

    await run("regrouping via the group route folds the strip and the solo URL redirects again", async () => {
      await page.goto(shellUrls[0], { waitUntil: "domcontentloaded" });
      await page.waitForSelector(tabSel(ids[1]), { state: "attached", timeout: 8000 });
      const group = await groupVia([ids[0], ids[1]]);
      gid = group.id;
      await page.goto(shellUrls[0], { waitUntil: "domcontentloaded" });
      await page.waitForURL(new RegExp(`/splits/${gid}\\?focus=${ids[0]}$`), { timeout: 10000 });
      await page.waitForSelector(groupTabSel, { state: "attached", timeout: 8000 });
      const members = await page.getAttribute(groupTabSel, "data-tab-members");
      assert(members === `${ids[0]} ${ids[1]}`, `members after regroup: ${members}`);
    });

    await run("context menu 'Ungroup split view' dissolves without killing members", async () => {
      const item = await contextItem(groupTabSel, "Ungroup split view");
      await item.click();
      await page.waitForURL(new RegExp(`/shells/(${ids[0]}|${ids[1]})$`), { timeout: 10000 });
      await sleep(800);
      assert(!(await page.$(groupTabSel)), "split tab still present after ungroup");
      assert(await page.$(tabSel(ids[0])), "member A tab missing after ungroup");
      assert(await page.$(tabSel(ids[1])), "member B tab missing after ungroup");
    });

    await run("quick nav: dragging a row onto another (with dwell) groups them", async () => withQuickNav(async () => {
      const rowSel = (id) => `.quicknav-active-list .quicknav-swipe-row:has(.quicknav-active-item[data-tab-id="${id}"])`;
      await page.click(".quicknav-toggle");
      await page.waitForSelector(rowSel(ids[1]), { state: "visible", timeout: 8000 });
      await sleep(800);
      const from = await steadyBox(page, `${rowSel(ids[1])} [data-qn-drag-handle]`);
      const to = await steadyBox(page, rowSel(ids[0]));
      const startY = from.y + from.height / 2;
      const endY = to.y + to.height / 2;
      await page.mouse.move(from.x + from.width / 2, startY);
      await page.mouse.down();
      for (let i = 1; i <= 8; i++) {
        await page.mouse.move(from.x + from.width / 2, startY + (endY - startY) * (i / 8), { steps: 2 });
        await sleep(25);
      }
      const highlighted = await page.waitForSelector(".quicknav-group-target", { timeout: 3000 }).catch(() => null);
      await sleep(400);
      await page.mouse.up();
      assert(highlighted, "group target never highlighted during the quick nav dwell");
      await page.waitForURL(/\/splits\/[^/]+$/, { timeout: 10000 });
      gid = new URL(page.url()).pathname.split("/").pop();
      await page.waitForSelector(".attach-split-pane .xterm-screen canvas", { timeout: 15000 });
      const members = await page.getAttribute(groupTabSel, "data-tab-members");
      assert(members === `${ids[0]} ${ids[1]}`, `members after quick nav grouping: ${members}`);
    }));

    await run("quick nav: the member swipe action removes one terminal from the split", async () => withQuickNav(async () => {
      await page.click(".quicknav-toggle");
      await page.waitForSelector(`[data-qn-block="${gid}"]`, { state: "visible", timeout: 8000 });
      await sleep(800);
      await page.$eval(
        `[data-qn-block="${gid}"] [data-qn-group-member]:last-child [data-qn-remove]`,
        (el) => el.click(),
      );
      await page.waitForURL(new RegExp(`/shells/${ids[0]}$`), { timeout: 10000 });
      await sleep(800);
      assert(!(await page.$(groupTabSel)), "split tab survived the member removal");
      assert(await page.$(tabSel(ids[1])), "removed member lost its tab");
      const group = await groupVia([ids[0], ids[1]]);
      gid = group.id;
      await page.goto(`${L.BASE}${group.url}`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(groupTabSel, { state: "attached", timeout: 8000 });
    }));

    await run("the group tab's close control stops every member after a confirm", async () => {
      await page.click("terminal-tabs .terminal-tab-split [data-tab-close]");
      await confirmSwal(page);
      await page.waitForURL((u) => !/\/splits\//.test(u.toString()), { timeout: 15000 });
      await sleep(800);
      assert(!(await page.$(tabSel(ids[0]))), "member A tab survived close");
      assert(!(await page.$(tabSel(ids[1]))), "member B tab survived close");
      shellUrls.length = 0;
    });

    // The two keyboard closes are deliberately different targets: the strip
    // shortcut belongs to the tab, which in a split is the whole group, and the
    // pane shortcut is the only one that takes a single member.
    let keptId = null;

    await run("Ctrl+Shift+Backspace closes the active pane and leaves the rest of the split", async () => {
      const urls = [await L.createShell(page, project), await L.createShell(page, project)];
      shellUrls.push(...urls);
      const pair = urls.map((u) => new URL(u).pathname.split("/").pop());
      keptId = pair[0];
      const group = await groupVia(pair);
      await page.goto(`${L.BASE}${group.url}`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(".attach-split-pane .xterm-screen canvas", { timeout: 15000 });
      await page.click(`terminal-attach[terminal-id="${pair[1]}"]`);
      await page.waitForFunction(
        (id) => document.querySelector("terminal-attach[active]")?.getAttribute("terminal-id") === id,
        pair[1],
        { timeout: 4000 },
      );
      await page.keyboard.down("Control");
      await page.keyboard.down("Shift");
      await page.keyboard.press("Backspace");
      await page.keyboard.up("Shift");
      await page.keyboard.up("Control");
      await page.waitForSelector(".swal2-popup", { timeout: 6000 });
      const asked = await page.textContent(".swal2-popup");
      assert(/Delete shell/i.test(asked) && !/Close all/i.test(asked), `the pane close asked for the group: ${asked.replace(/\s+/g, " ").slice(0, 80)}`);
      await confirmSwal(page);
      // The close re-renders the page, so wait for the survivor's own tab to
      // stand again before reading the strip; the closed pane's tab is gone
      // during the swap either way.
      await page.waitForSelector(tabSel(keptId), { state: "attached", timeout: 15000 });
      await sleep(800);
      assert(await page.$(tabSel(keptId)), "the other member went with the active pane");
      assert(!(await page.$(tabSel(pair[1]))), "the closed pane kept its tab");
    });

    await run("Ctrl+Shift+X on a split page closes the whole split, never one pane", async () => {
      const extraUrl = await L.createShell(page, project);
      shellUrls.push(extraUrl);
      const extraId = new URL(extraUrl).pathname.split("/").pop();
      const group = await groupVia([keptId, extraId]);
      await page.goto(`${L.BASE}${group.url}`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(".attach-split-pane .xterm-screen canvas", { timeout: 15000 });
      await page.click(`terminal-attach[terminal-id="${extraId}"]`);
      await sleep(200);
      await page.keyboard.down("Control");
      await page.keyboard.down("Shift");
      await page.keyboard.press("x");
      await page.keyboard.up("Shift");
      await page.keyboard.up("Control");
      await page.waitForSelector(".swal2-popup", { timeout: 6000 });
      const asked = await page.textContent(".swal2-popup");
      assert(/Close all 2 terminals/i.test(asked), `the strip close did not ask for the whole split: ${asked.replace(/\s+/g, " ").slice(0, 80)}`);
      await confirmSwal(page);
      await page.waitForURL((u) => !/\/splits\//.test(u.toString()), { timeout: 15000 });
      await sleep(800);
      assert(!(await page.$(tabSel(keptId))), "the split survived the shortcut");
      assert(!(await page.$(tabSel(extraId))), "the active pane survived the shortcut");
      shellUrls.length = 0;
    });

    await run("mixed split: the footer follows the active pane, prompt and files belong to the coder", async () => {
      const coderUrl = await L.createSession(page, project, `cdr-${tag.slice(-5)}`);
      const coderId = new URL(coderUrl).pathname.split("/").pop();
      const shellUrl = await L.createShell(page, project);
      shellUrls.push(shellUrl);
      const shellId = new URL(shellUrl).pathname.split("/").pop();
      await page.waitForSelector(tabSel(coderId), { state: "attached", timeout: 8000 });
      const group = await groupVia([shellId, coderId]);
      await page.goto(`${L.BASE}${group.url}`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(".attach-split-pane .xterm-screen canvas", { timeout: 15000 });
      const coderFooter = `[data-terminal-footer="${coderId}"]`;
      const shellFooter = `[data-terminal-footer="${shellId}"]`;
      assert(await page.$eval(coderFooter, (el) => el.hidden), "coder footer visible while the shell pane is active");
      await page.click(`terminal-attach[terminal-id="${coderId}"]`);
      await page.waitForFunction((sel) => {
        const el = document.querySelector(sel);
        return el && !el.hidden;
      }, coderFooter, { timeout: 4000 });
      assert(await page.$eval(shellFooter, (el) => el.hidden), "shell footer still visible");
      assert(await page.$(`${coderFooter} .attach-desktop [data-terminal-prompt-modal-open]`), "coder footer misses the prompt button");
      assert(await page.$(`${coderFooter} terminal-direction-pad[up-control="page-up"]`), "coder footer misses the page pad");
      await page.click(`${coderFooter} .attach-desktop .coder-files-button`);
      await page.waitForFunction((id) => {
        const modal = document.getElementById(`coder-files-modal-${id}`);
        return modal && modal.classList.contains("show");
      }, coderId, { timeout: 6000 });
      assert(await page.$(`#coder-files-modal-${coderId} [data-coder-file-upload-form][action="/coders/${coderId}/files"]`), "files modal posts to the wrong coder");
      await page.click(`#coder-files-modal-${coderId} .btn-close`);
      await sleep(700);
      await page.click(`${coderFooter} .attach-desktop [data-terminal-prompt-modal-open]`);
      await page.waitForSelector("#terminal-prompt-modal.show", { timeout: 4000 });
      await page.fill("#terminal-prompt-modal-text", `SPLIT_PROMPT_${tag}`);
      const reqP = page.waitForRequest((r) => /\/input$/.test(r.url()) && r.method() === "POST", { timeout: 8000 });
      await page.click('#terminal-prompt-modal-form button[type="submit"]');
      const req = await reqP;
      assert(new URL(req.url()).pathname === `/coders/${coderId}/input`, `prompt posted to ${new URL(req.url()).pathname}`);
      assert((req.postData() || "").includes(`SPLIT_PROMPT_${tag}`), "prompt payload missing from the input POST");
      await sleep(1000);
      assert(!(await paneText(shellId)).includes(`SPLIT_PROMPT_${tag}`), "prompt leaked into the shell pane");
      await page.click(`terminal-tabs .terminal-tab-split [data-tab-close]`);
      await confirmSwal(page);
      await page.waitForURL((u) => !/\/splits\//.test(u.toString()), { timeout: 15000 });
      await sleep(600);
    });

    // Close all leaves the split behind: only a coder member answers its POST
    // with JSON, so a naive follow would land on the POST url.
    await run("close all on a split holding a coder lands on a real page, never on the POST url", async () => {
      const coderUrl = await L.createSession(page, project, `spl-${tag.slice(-5)}`);
      const coderId = new URL(coderUrl).pathname.split("/").pop();
      const shellUrl = await L.createShell(page, project);
      shellUrls.push(shellUrl);
      const shellId = new URL(shellUrl).pathname.split("/").pop();
      await page.waitForSelector(tabSel(coderId), { state: "attached", timeout: 8000 });
      const group = await groupVia([shellId, coderId]);
      await page.goto(`${L.BASE}${group.url}`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(".attach-split-pane .xterm-screen canvas", { timeout: 15000 });
      await page.click("terminal-tabs .terminal-tab-split [data-tab-close]");
      await confirmSwal(page);
      await page.waitForURL((u) => !/\/splits\//.test(u.toString()), { timeout: 15000 });
      assert(!/\/(stop|delete)$/.test(page.url()), `landed on the POST url: ${page.url()}`);
      // The strip's close-all goes to the first tab that is left, or to the
      // projects page when the strip runs empty. Both are real pages.
      const landed = new URL(page.url()).pathname;
      assert(/^\/(projects|coders|shells)/.test(landed), `close all landed on ${landed}`);
      const body = await page.textContent("body");
      assert(!/Method not allowed/i.test(body), "close all landed on an error page");
      assert(!(await page.$(tabSel(coderId))), "the coder tab survived close all");
      assert(!(await page.$(tabSel(shellId))), "the shell tab survived close all");
      shellUrls.pop();
      // The coder was stopped, not dropped: take its conversation with it.
      await page.evaluate(async (id) => {
        const token = document.querySelector('meta[name="csrf-token"]').content;
        await fetch(`/coders/${id}/delete`, { method: "POST", headers: { "X-CSRF-Token": token, Accept: "application/json" } });
      }, coderId);
    });
  } finally {
    for (const url of shellUrls) await L.deleteShell(page, url).catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
  }
});
