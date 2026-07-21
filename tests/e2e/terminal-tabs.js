const L = require("./lib");
const { assert, sleep, BASE } = L;

// Terminal tabs: the desktop tab strip attached to the terminal on the attach
// pages (custom element terminal-tabs, partial terminal_tabs.gohtml). Every live
// coder and shell is a tab. The strip is sticky at the viewport top (the
// counterpart of the sticky attach footer), so it stays reachable while a tall
// terminal scrolls. The order is cross-device state in tmux: each
// session carries a @dc_tab_pos user option, written through POST
// /terminal-tabs/order on drag, read back by the pane scan, so the server
// renders the strip fully sorted and the order dies with its sessions.
// Unpositioned sessions (freshly started, resumed) join on the right, oldest
// first. Tabs reorder by pointer drag. Ctrl+Tab switches straight to the next
// active tab and Ctrl+Shift+Tab to the previous, rotating over the active tabs
// only (never an inactive row) and wrapping at the ends. Each press aborts the
// switch in flight: pe.js aborts the prior navigation and re-checks before it
// renders, so mashing the shortcut chains through the tabs (tracked on
// pendingIndex, independent of the still-current active tab) and lands on the
// last target without ever painting the ones in between. While a switch is in
// flight the tab you are heading to carries a pending highlight (the switcher's
// blue selected-row tint, class terminal-tab-pending, scrolled into view), so
// you see where you are landing before the page renders; it clears when the tab
// loads (becomes active) or when a wrap brings you back to the current tab.
// Browsers reserve
// Ctrl+Tab, so the direct switch only reaches headless and PWAs. The switcher
// popup opens only via double tapping Ctrl or Meta within 400ms (collision
// free: bare modifiers never reach the shell, a chord like Ctrl+C cancels the
// tap); the double tap anchors one step forward from the current session and
// wraps within the actives, then cycling via Tab, Ctrl+Tab or arrows rotates
// over the full list including the inactive section, Enter or click switches,
// Esc closes, typing filters the list by name and project through the search
// input, and it all works while the xterm terminal has focus. The switcher is
// app wide: every page without an inline strip (projects, editor, settings)
// mounts the same element hidden from the layout (terminal_tabs_switcher
// partial, strip plus menu only), so double Ctrl/Meta opens the switcher
// anywhere. The hidden instance leaves direct Ctrl+Tab to the page (the editor
// binds it for its own tabs) and skips the live fragment pull while its
// switcher is closed, flushing the deferred refresh when it opens. The
// switcher is a full quick-access palette: below the active terminals and the
// inactive coders sit an Editors section (one row per project, sorted like the
// resume groups through @dc/project-sort, URLs from ProjectNav.EditorURL with
// the ?return target, fed by a hidden [data-tabs-editors] link list in the +
// menu) and a New section (New coder / New shell rows reusing the + menu
// links, so the current project arrives preselected on the create form). All
// of it filters through the search input; with no sessions at all the
// switcher still opens on the editor and New rows.
// Every tab carries a close control
// (confirm dialog, then coder stop or shell delete; closing the current session
// switches to the right neighbor tab like Terminal.app, left as fallback,
// projects page when the strip is empty; the page header stop button keeps its
// own redirect+flash flow). Right click on a tab opens a context menu
// (@dc/contextmenu, body-mounted .dc-context-menu): Rename for shells (prompt,
// POST /shells/:id/rename, tab updates over the event stream), Mark read only
// while the tab carries news, Open project (projects page card fragment), Open
// editor (with ?return to the attach page), and the same stop/delete action as
// the close control. Switcher rows deliberately carry no close control
// and no badges; the current session's row is marked by the current class
// (accent bar) without extra row content. The + button at the strip's end
// opens New coder / New shell with the current project preselected, followed by
// a resume section listing every inactive coder grouped by project (same source
// as the quick nav project browser, plain form POST to
// /coders/:id/resume, folded to 3 per project with a "Show N more" toggle via
// @dc/fold). The project groups are wrapped in [data-project-name/-active/-used]
// elements and reordered client side through @dc/project-sort, so the sort mode
// chosen on the projects page (shared "dc-project-sort" key) applies here and,
// via DOM order, to the switcher's inactive rows too. The switcher lists those
// inactive coders in a dimmed section below the active rows, grouped per
// project with the same fold: three rows per project plus a selectable
// "Show N more" row that Tab reaches like any other; Enter or click on it
// expands the group and selects the first revealed row, which then all cycle
// normally. Typing a filter suspends the folding (toggles hide, every match
// shows), so all inactive coders stay findable. Enter or click on a row
// resumes and navigates to the attach page. Opening the + menu or the switcher
// refreshes the strip and menu content in the background from the
// GET /terminal-tabs fragment (same pattern and 2px progress indicator as the
// quick nav's /quicknav); an open switcher rebuilds its rows in place,
// preserving filter text, selection and expanded groups.
// Tabs hide their idle (green) status
// dot via CSS and show only the blue news dot; the notify wiring stays on the
// element. A settings button (gear) sits right of the + button and opens a
// menu with the font-size and rows selects (terminal-setting-select renders as
// label plus native select, auto-close outside so selecting keeps the menu
// open); the old settings row above the terminal is hidden on desktop
// (.attach-settings) and on mobile holds the same gear menu instead of bare
// selects, both instances sync through the bubbling terminal-setting-change
// event. Navigating between attach pages (tab click, switcher, resume) keeps
// the scroll position: app.js restores it in the pe:navigate/pe:form succeed
// hook, which runs before the browser paints, so pe.js' scroll-to-top after the
// swap never becomes visible and only the terminal's own cursor scrolling moves
// the view. The switcher shortcut hint sits next to the refresh button in the
// desktop attach footer (always Ctrl+Ctrl to open the switcher; standalone/PWA
// also shows Ctrl+Tab to step between tabs, via a display-mode media query,
// since browsers reserve Ctrl+Tab): always visible, no strip space. The + menu
// also links to the current project's editor (quick nav parity). On coarse
// pointer clients the strip stays hidden, the quick nav covers mobile. The strip
// lists every live session on the host, so all interactions here MUST stay on
// tabs of sessions this runner created (scoped via data-tab-id). Known side
// effect: the drag posts the full strip order, so on a shared host tmux the
// run writes the cosmetic @dc_tab_pos option onto real sessions too; unset
// with `tmux set-option -u -t <session> @dc_tab_pos` if that matters.

L.runFeature("TERMINAL-TABS", async ({ browser, page, run, mobilePage }) => {
  const tag = `tt-${Date.now().toString(36)}`;
  const project = `zztc-${tag}`;
  const shellUrls = [];
  let coderUrl = null;
  let foreignProject = null;
  let foreignShellUrl = null;
  const ownId = (url) => new URL(url).pathname.split("/").pop();
  const tabSel = (id) => `terminal-tabs .terminal-tab[data-tab-id="${id}"]`;
  const tabOrder = () => page.$$eval("terminal-tabs .terminal-tab", (els) => els.map((e) => e.dataset.tabId));
  try {
    await L.createProject(page, project);
    for (let i = 0; i < 3; i++) {
      shellUrls.push(await L.createShell(page, project));
      await sleep(1100);
    }
    const ids = shellUrls.map(ownId);

    await run("strip renders every own shell, active tab is the current one, newest sits rightmost", async () => {
      await page.goto(shellUrls[2], { waitUntil: "domcontentloaded" });
      assert((await L.waitUpgraded(page, ["terminal-tabs"], 8000)).length === 0, "terminal-tabs not upgraded");
      for (const id of ids) await page.waitForSelector(tabSel(id), { state: "attached", timeout: 8000 });
      const order = await tabOrder();
      const ownOrder = order.filter((id) => ids.includes(id));
      assert(JSON.stringify(ownOrder) === JSON.stringify(ids), `own tab order ${ownOrder} != creation order ${ids}`);
      assert(order[order.length - 1] === ids[2], "newest own shell is not the rightmost tab");
      const active = await page.$eval(tabSel(ids[2]), (e) => e.classList.contains("active") && e.getAttribute("aria-selected") === "true");
      assert(active, "current shell tab not marked active");
      const project2 = await page.$eval(tabSel(ids[0]), (e) => e.dataset.tabProject);
      assert(project2 === project, `tab project label '${project2}'`);
      const idleClean = await page.$eval(`${tabSel(ids[0])} .dc-term-icon`, (e) => !e.classList.contains("news"));
      assert(idleClean, "idle tab icon still carries the news mark");
      const sticky = await page.$eval("terminal-tabs", (e) => getComputedStyle(e).position === "sticky");
      assert(sticky, "tab strip is not sticky");
    });

    // The shortcut description lives in a modal behind a help button in the
    // desktop footer, so long key combos never overflow the footer row.
    await run("the footer help button opens the keyboard shortcuts modal", async () => {
      const help = page.locator('.attach-desktop [data-bs-target="#terminal-shortcuts-modal"]');
      assert(await help.isVisible(), "help button not visible in the desktop footer");
      await help.click();
      await L.modalShown(page, "terminal-shortcuts-modal");
      const text = await page.$eval("#terminal-shortcuts-modal", (e) => e.textContent);
      assert(/Ctrl/.test(text) && /Fullscreen/i.test(text) && /switcher/i.test(text), `modal text incomplete: ${text.replace(/\s+/g, " ").slice(0, 120)}`);
      const kbdCount = await page.locator("#terminal-shortcuts-modal kbd").count();
      assert(kbdCount >= 6, `expected kbd elements in the modal, got ${kbdCount}`);
      await page.click('#terminal-shortcuts-modal [data-bs-dismiss="modal"]');
      await page.waitForFunction(() => !document.querySelector("#terminal-shortcuts-modal.show"), null, { timeout: 4000 });
      await sleep(300);
    });

    await run("clicking a tab switches to that session (boosted navigation)", async () => {
      await page.click(tabSel(ids[0]));
      await page.waitForURL(new RegExp(ids[0]), { timeout: 8000 });
      await page.waitForSelector(`${tabSel(ids[0])}.active`, { state: "attached", timeout: 8000 });
    });

    await run("dragging a tab reorders the strip and the order survives a reload", async () => {
      await page.$eval(".terminal-tabs-strip", (s) => { s.scrollLeft = s.scrollWidth; });
      await sleep(150);
      const src = page.locator(tabSel(ids[2]));
      const dst = page.locator(tabSel(ids[0]));
      const s = await src.boundingBox();
      const d = await dst.boundingBox();
      assert(s && d, "tab bounding boxes unavailable");
      const from = { x: s.x + s.width / 2, y: s.y + s.height / 2 };
      const to = { x: d.x + d.width * 0.1, y: d.y + d.height / 2 };
      await page.mouse.move(from.x, from.y);
      await page.mouse.down();
      for (let i = 1; i <= 12; i++) {
        await page.mouse.move(from.x + (to.x - from.x) * (i / 12), from.y, { steps: 2 });
        await sleep(30);
      }
      await page.mouse.up();
      await sleep(800);
      let ownOrder = (await tabOrder()).filter((id) => ids.includes(id));
      assert(JSON.stringify(ownOrder) === JSON.stringify([ids[2], ids[0], ids[1]]), `order after drag: ${ownOrder}`);
      await page.reload({ waitUntil: "domcontentloaded" });
      await page.waitForSelector(tabSel(ids[2]), { state: "attached", timeout: 8000 });
      await sleep(500);
      ownOrder = (await tabOrder()).filter((id) => ids.includes(id));
      assert(JSON.stringify(ownOrder) === JSON.stringify([ids[2], ids[0], ids[1]]), `order after reload: ${ownOrder}`);
    });

    await run("a drag does not trigger the tab's navigation", async () => {
      assert(page.url().includes(ids[0]), `unexpected page ${page.url()}`);
    });

    await run("the dragged order is cross device, a fresh browser context sees it", async () => {
      const ctx2 = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1360, height: 900 } });
      try {
        const page2 = await ctx2.newPage();
        await L.login(page2);
        await page2.goto(shellUrls[1], { waitUntil: "domcontentloaded" });
        await page2.waitForSelector(tabSel(ids[2]), { state: "attached", timeout: 8000 });
        const order2 = await page2.$$eval("terminal-tabs .terminal-tab", (els) => els.map((e) => e.dataset.tabId));
        const ownOrder2 = order2.filter((id) => ids.includes(id));
        assert(JSON.stringify(ownOrder2) === JSON.stringify([ids[2], ids[0], ids[1]]), `order in second context: ${ownOrder2}`);
      } finally {
        await ctx2.close();
      }
    });

    await run("Ctrl+Tab switches straight to the next active tab, Ctrl+Shift+Tab to the previous, no switcher", async () => {
      // own block order after the drag is [ids2, ids0, ids1], contiguous in the strip.
      await page.goto(shellUrls[0], { waitUntil: "domcontentloaded" });
      await page.waitForSelector(`${tabSel(ids[0])}.active`, { state: "attached", timeout: 8000 });
      await page.click(".attach-terminal");
      await sleep(200);
      await page.keyboard.down("Control");
      await page.keyboard.press("Tab");
      await page.keyboard.up("Control");
      await page.waitForURL(new RegExp(ids[1]), { timeout: 8000 });
      await page.waitForSelector(`${tabSel(ids[1])}.active`, { state: "attached", timeout: 8000 });
      assert((await page.locator(".terminal-switcher").count()) === 0, "Ctrl+Tab opened the switcher instead of switching");
      await page.click(".attach-terminal");
      await sleep(200);
      await page.keyboard.down("Control");
      await page.keyboard.down("Shift");
      await page.keyboard.press("Tab");
      await page.keyboard.up("Shift");
      await page.keyboard.up("Control");
      await page.waitForURL(new RegExp(ids[0]), { timeout: 8000 });
      await page.waitForSelector(`${tabSel(ids[0])}.active`, { state: "attached", timeout: 8000 });
    });

    await run("mashing Ctrl+Tab chains across active tabs, aborts the intermediates, lands on the last", async () => {
      await page.goto(shellUrls[2], { waitUntil: "domcontentloaded" });
      await page.waitForSelector(`${tabSel(ids[2])}.active`, { state: "attached", timeout: 8000 });
      await page.click(".attach-terminal");
      await sleep(200);
      await page.keyboard.down("Control");
      await page.keyboard.press("Tab"); // ids2 -> ids0
      await page.keyboard.press("Tab"); // ids0 -> ids1, aborting the ids0 load mid flight
      await page.keyboard.up("Control");
      await page.waitForURL(new RegExp(ids[1]), { timeout: 8000 });
      await page.waitForSelector(`${tabSel(ids[1])}.active`, { state: "attached", timeout: 8000 });
      assert((await page.locator(".terminal-switcher").count()) === 0, "switcher opened during rapid Ctrl+Tab");
    });

    await run("the tab you are heading to gets a pending highlight while the switch is in flight", async () => {
      await page.goto(`${BASE}/shells/${ids[0]}`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(`${tabSel(ids[0])}.active`, { state: "attached", timeout: 8000 });
      // Hold the target's attach navigation so the in-flight (pending) state is observable.
      await page.route(`**/shells/${ids[1]}`, async (route) => { await sleep(2000); await route.continue(); });
      try {
        await page.click(".attach-terminal");
        await sleep(200);
        await page.keyboard.down("Control");
        await page.keyboard.press("Tab"); // ids0 -> ids1, held by the route
        await page.keyboard.up("Control");
        const pending = await page.waitForSelector(`${tabSel(ids[1])}.terminal-tab-pending`, { state: "attached", timeout: 1500 });
        const bg = await pending.evaluate((e) => getComputedStyle(e).backgroundColor);
        assert(bg !== "rgba(0, 0, 0, 0)" && bg !== "transparent", `pending tab carries no highlight background (${bg})`);
        // The page has not switched yet: the origin tab stays active until the nav lands.
        assert(await page.$(`${tabSel(ids[0])}.active`), "origin tab lost active before the switch landed");
        assert(page.url().includes(ids[0]), `navigated away before the switch landed (${page.url()})`);
        await page.waitForURL(new RegExp(ids[1]), { timeout: 8000 });
        await page.waitForSelector(`${tabSel(ids[1])}.active`, { state: "attached", timeout: 8000 });
        assert((await page.locator(".terminal-tab-pending").count()) === 0, "pending highlight lingered after the switch landed");
      } finally {
        await page.unroute(`**/shells/${ids[1]}`);
      }
    });

    await run("arrow keys move the switcher selection, Esc closes without navigating", async () => {
      const before = page.url();
      await page.click(".attach-terminal");
      await sleep(200);
      await page.keyboard.press("Control");
      await page.keyboard.press("Control");
      await page.waitForSelector(".terminal-switcher", { state: "visible", timeout: 4000 });
      const first = await page.$eval(".terminal-switcher-item.selected", (e) => e.dataset.switcherId);
      await page.keyboard.press("ArrowDown");
      await sleep(100);
      const second = await page.$eval(".terminal-switcher-item.selected", (e) => e.dataset.switcherId);
      assert(first !== second, "ArrowDown did not move the selection");
      await page.keyboard.press("ArrowUp");
      await sleep(100);
      assert(first === (await page.$eval(".terminal-switcher-item.selected", (e) => e.dataset.switcherId)), "ArrowUp did not move back");
      await page.keyboard.press("Escape");
      await sleep(200);
      assert((await page.locator(".terminal-switcher").count()) === 0, "Esc did not close the switcher");
      assert(page.url() === before, "Esc navigated away");
    });

    await run("the open switcher overlays the attach footer, its buttons are not hit-testable", async () => {
      await page.click(".attach-terminal");
      await sleep(200);
      await page.keyboard.press("Control");
      await page.keyboard.press("Control");
      await page.waitForSelector(".terminal-switcher", { state: "visible", timeout: 4000 });
      const portaled = await page.$eval(".terminal-switcher", (e) => e.parentElement === document.body);
      assert(portaled, "switcher overlay is not portaled to <body> (trapped in the sticky terminal-tabs stacking context)");
      const box = await page.locator(".attach-desktop [data-terminal-refresh]").boundingBox();
      assert(box, "desktop footer refresh button has no box");
      const hit = await page.evaluate(({ x, y }) => {
        const el = document.elementFromPoint(x, y);
        return { switcher: !!el?.closest(".terminal-switcher"), footer: !!el?.closest(".attach-footer") };
      }, { x: box.x + box.width / 2, y: box.y + box.height / 2 });
      assert(hit.switcher && !hit.footer, "attach footer sits above the open switcher (buttons stay clickable)");
      await page.keyboard.press("Escape");
      await sleep(200);
    });

    await run("double tapping Ctrl opens the switcher, plain Tab cycles, Esc closes", async () => {
      await page.click(".attach-terminal");
      await sleep(200);
      await page.keyboard.press("Control");
      await page.keyboard.press("Control");
      await page.waitForSelector(".terminal-switcher", { state: "visible", timeout: 4000 });
      const first = await page.$eval(".terminal-switcher-item.selected", (e) => e.dataset.switcherId);
      await page.keyboard.press("Tab");
      await sleep(100);
      assert(first !== (await page.$eval(".terminal-switcher-item.selected", (e) => e.dataset.switcherId)), "plain Tab did not cycle");
      await page.keyboard.press("Escape");
      await sleep(200);
      assert((await page.locator(".terminal-switcher").count()) === 0, "Esc did not close after double Ctrl");
    });

    await run("double tapping Meta opens the switcher too", async () => {
      await page.keyboard.press("Meta");
      await page.keyboard.press("Meta");
      await page.waitForSelector(".terminal-switcher", { state: "visible", timeout: 4000 });
      await page.keyboard.press("Escape");
      await sleep(200);
      assert((await page.locator(".terminal-switcher").count()) === 0, "Esc did not close after double Meta");
    });

    await run("a Ctrl chord does not count toward the double tap", async () => {
      await page.keyboard.down("Control");
      await page.keyboard.press("c");
      await page.keyboard.up("Control");
      await page.keyboard.press("Control");
      await sleep(250);
      assert((await page.locator(".terminal-switcher").count()) === 0, "switcher opened after Ctrl+C plus one tap");
      await sleep(400);
    });

    await run("clicking a switcher item switches to that session", async () => {
      await page.click(".attach-terminal");
      await sleep(200);
      await page.keyboard.press("Control");
      await page.keyboard.press("Control");
      await page.waitForSelector(".terminal-switcher", { state: "visible", timeout: 4000 });
      await page.click(`.terminal-switcher-item[data-switcher-id="${ids[2]}"]`);
      await page.waitForURL(new RegExp(ids[2]), { timeout: 8000 });
    });

    await run("the strip settings menu adjusts rows and stays open while selecting", async () => {
      await page.click(".terminal-tabs-settings > button");
      await page.waitForSelector(".terminal-tabs-settings .dropdown-menu.show", { state: "visible", timeout: 4000 });
      const selects = await page.locator(".terminal-tabs-settings terminal-setting-select select").count();
      assert(selects === 3, `settings menu carries ${selects} selects, expected font size, rows and theme`);
      const rowsSel = '.terminal-tabs-settings terminal-setting-select[setting="rows"] select';
      const before = await page.$eval(rowsSel, (el) => el.value);
      await page.selectOption(rowsSel, "40");
      await sleep(300);
      assert(await page.evaluate(() => localStorage.getItem("dc-terminal-rows")) === "40", "rows not persisted from the strip settings menu");
      assert((await page.locator(".terminal-tabs-settings .dropdown-menu.show").count()) === 1, "settings menu closed on select");
      await page.selectOption(rowsSel, before);
      await sleep(200);
      await page.keyboard.press("Escape");
      await sleep(200);
    });

    await run("the + menu links to the editor, new coder and new shell with the current project preselected", async () => {
      await page.click(".terminal-tabs-new-btn");
      await page.waitForSelector("terminal-tabs .dropdown-menu.show", { state: "visible", timeout: 4000 });
      const hrefs = await page.$$eval("terminal-tabs .dropdown-menu.show > a", (as) => as.map((a) => a.getAttribute("href")));
      assert(hrefs.length === 3, `expected 3 links, got ${hrefs.length}`);
      assert(hrefs[0].startsWith(`/projects/${project}/editor?return=`), `editor link ${hrefs[0]}`);
      assert(hrefs[1].startsWith("/coders/new?") && hrefs[1].includes(`project=${project}`), `coder link ${hrefs[1]}`);
      assert(hrefs[2].startsWith("/shells/new?") && hrefs[2].includes(`project=${project}`), `shell link ${hrefs[2]}`);
      assert(hrefs[2].includes(`return=%2Fshells%2F${ids[2]}`), `shell link return target ${hrefs[2]}`);
      await page.click('terminal-tabs .dropdown-menu.show a[href^="/shells/new"]');
      await page.waitForURL(/\/shells\/new/, { timeout: 8000 });
      const selectedPath = await page.locator('select[name="project"]').inputValue();
      assert(selectedPath.endsWith(`/${project}`), `preselected project path ${selectedPath}`);
    });

    await run("a shell created through the + menu joins the strip on the right", async () => {
      await Promise.all([
        page.waitForURL(/\/shells\/(?!new)[^/]+$/, { timeout: 15000 }),
        page.locator('form:has(select[name="project"]) button[type="submit"]').first().click(),
      ]);
      shellUrls.push(page.url());
      const newId = ownId(page.url());
      await page.waitForSelector(tabSel(newId), { state: "attached", timeout: 8000 });
      await sleep(400);
      const order = await tabOrder();
      assert(order[order.length - 1] === newId, "new shell is not the rightmost tab");
      const ownOrder = order.filter((id) => [...ids, newId].includes(id));
      assert(JSON.stringify(ownOrder) === JSON.stringify([ids[2], ids[0], ids[1], newId]), `own order with new shell: ${ownOrder}`);
    });

    await run("typing in the switcher filters out a foreign-project shell, Enter opens the first match", async () => {
      foreignProject = `zzfo-${tag.slice(-6)}`;
      await L.createProject(page, foreignProject);
      foreignShellUrl = await L.createShell(page, foreignProject);
      const foreignId = ownId(foreignShellUrl);
      await page.waitForSelector(tabSel(foreignId), { state: "attached", timeout: 8000 });
      // The strip may still be replaying terminals events from the create;
      // retry the double tap until the switcher actually opens.
      let opened = null;
      for (let attempt = 0; attempt < 3 && !opened; attempt += 1) {
        await page.keyboard.press("Control");
        await sleep(120);
        await page.keyboard.press("Control");
        opened = await page.waitForSelector(".terminal-switcher", { state: "visible", timeout: 2500 }).catch(() => null);
        if (!opened) await sleep(600);
      }
      assert(opened, "switcher did not open after retries");
      await page.keyboard.type(project, { delay: 40 });
      await sleep(200);
      const visible = await page.$$eval(".terminal-switcher-item[data-switcher-id]:not([hidden])", (els) => els.map((e) => e.dataset.switcherId));
      assert(visible.length === 4, `filter shows ${visible.length} rows, expected the 4 own shells`);
      assert(visible.every((id) => [...ids, ownId(shellUrls[3])].includes(id)), `filter leaked foreign rows: ${visible}`);
      const editorRows = await page.$$eval('.terminal-switcher-item[data-switcher-section="editors"]:not([hidden])', (els) => els.map((e) => e.dataset.switcherUrl));
      assert(editorRows.length === 1 && editorRows[0].startsWith(`/projects/${project}/editor`), `project filter editor rows ${JSON.stringify(editorRows)}`);
      const foreignHidden = await page.$eval(`.terminal-switcher-item[data-switcher-id="${foreignId}"]`, (e) => e.hidden);
      assert(foreignHidden, "the foreign-project row is not hidden by the filter");
      await page.keyboard.press("Enter");
      await page.waitForURL(new RegExp(visible[0]), { timeout: 8000 });
      assert((await page.locator(".terminal-switcher").count()) === 0, "switcher still open after Enter");
    });

    await run("a filter without matches shows the empty state and Enter does nothing", async () => {
      const before = page.url();
      await page.keyboard.press("Control");
      await page.keyboard.press("Control");
      await page.waitForSelector(".terminal-switcher", { state: "visible", timeout: 4000 });
      await page.keyboard.type("zz-no-match-zz");
      await sleep(200);
      assert((await page.locator(".terminal-switcher-item:not([hidden])").count()) === 0, "rows left despite no match");
      await page.locator(".terminal-switcher-empty").waitFor({ state: "visible", timeout: 2000 });
      await page.keyboard.press("Enter");
      await sleep(300);
      assert(page.url() === before, "Enter navigated despite no match");
      await page.keyboard.press("Escape");
      await sleep(200);
    });

    await run("renaming the shell inline updates its tab immediately", async () => {
      assert(page.url().includes(ids[2]), `unexpected page ${page.url()}`);
      const newName = `tabs-renamed-${tag}`;
      await page.click("[data-rename-label]");
      await page.fill("[data-rename-input]", newName);
      await page.keyboard.press("Enter");
      await page.waitForFunction(
        (id) => document.querySelector(`terminal-tabs .terminal-tab[data-tab-id="${id}"]`)?.dataset.tabName?.startsWith("tabs-renamed-"),
        ids[2],
        { timeout: 8000 },
      );
      const label = await page.$eval(`${tabSel(ids[2])} .terminal-tab-name`, (e) => e.textContent);
      assert(label === newName, `tab label '${label}' after rename`);
      const title = await page.$eval(tabSel(ids[2]), (e) => e.getAttribute("title"));
      assert(title.includes(newName) && title.includes(project), `tab title '${title}' after rename`);
    });

    const menuItem = (label) => page.locator(".dc-context-menu .dropdown-item", { hasText: new RegExp(`^${label}$`) });
    const openTabMenu = async (sel) => {
      await page.click(sel, { button: "right" });
      await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
    };
    const closeTabMenu = async () => {
      await page.keyboard.press("Escape");
      await page.waitForSelector(".dc-context-menu", { state: "detached", timeout: 4000 });
    };

    await run("right click on a shell tab opens its context menu, Escape closes it", async () => {
      await page.goto(shellUrls[2], { waitUntil: "domcontentloaded" });
      await page.waitForSelector(tabSel(ids[2]), { state: "attached", timeout: 8000 });
      await openTabMenu(tabSel(ids[2]));
      const labels = await page.$$eval(".dc-context-menu .dropdown-item", (els) => els.map((e) => e.textContent.trim()));
      for (const want of ["Rename", "Open project", "Open editor", "Delete shell"]) {
        assert(labels.includes(want), `shell menu misses '${want}': ${labels.join(", ")}`);
      }
      assert(!labels.includes("Stop coder"), "shell menu offers Stop coder");
      assert(!labels.includes("Mark read"), "menu offers Mark read without news");
      await closeTabMenu();
    });

    await run("context menu rename posts and the tab updates over the event stream", async () => {
      const newName = `ctx-renamed-${tag}`;
      await openTabMenu(tabSel(ids[2]));
      await menuItem("Rename").click();
      await page.waitForSelector(".swal2-input", { state: "visible", timeout: 4000 });
      await page.fill(".swal2-input", newName);
      await page.click(".swal2-confirm");
      await page.waitForFunction(
        ([id, name]) => document.querySelector(`terminal-tabs .terminal-tab[data-tab-id="${id}"]`)?.dataset.tabName === name,
        [ids[2], newName],
        { timeout: 8000 },
      );
    });

    await run("context menu navigates to the project card and the project editor", async () => {
      await page.goto(shellUrls[2], { waitUntil: "domcontentloaded" });
      await page.waitForSelector(tabSel(ids[2]), { state: "attached", timeout: 8000 });
      const attachPath = new URL(page.url()).pathname;
      await openTabMenu(tabSel(ids[2]));
      await menuItem("Open project").click();
      await page.waitForFunction((p) => window.location.pathname === "/projects" && window.location.hash === `#project-${p}`, project, { timeout: 8000 });
      await page.waitForSelector(`#project-${project}`, { state: "attached", timeout: 8000 });
      await page.goto(shellUrls[2], { waitUntil: "domcontentloaded" });
      await page.waitForSelector(tabSel(ids[2]), { state: "attached", timeout: 8000 });
      await openTabMenu(tabSel(ids[2]));
      await menuItem("Open editor").click();
      await page.waitForURL(new RegExp(`/projects/${project}/editor`), { timeout: 10000 });
      assert(decodeURIComponent(page.url()).includes(`return=${attachPath}`), `editor url misses the return target: ${page.url()}`);
      await page.waitForSelector("[data-editor-tree]", { state: "attached", timeout: 8000 });
      await page.waitForFunction(() => {
        const t = document.querySelector("[data-editor-tree]");
        return t && !/Loading/.test(t.textContent);
      }, null, { timeout: 8000 });
      await page.goto(shellUrls[2], { waitUntil: "domcontentloaded" });
      await page.waitForSelector(tabSel(ids[2]), { state: "attached", timeout: 8000 });
    });

    await run("context menu delete removes a shell tab in place", async () => {
      const ghostUrl = await L.createShell(page, project);
      const ghostId = ownId(ghostUrl);
      await page.goto(shellUrls[2], { waitUntil: "domcontentloaded" });
      await page.waitForSelector(tabSel(ghostId), { state: "attached", timeout: 8000 });
      await openTabMenu(tabSel(ghostId));
      await menuItem("Delete shell").click();
      await L.confirmSwal(page);
      await page.waitForSelector(tabSel(ghostId), { state: "detached", timeout: 8000 });
      assert(page.url().includes(ids[2]), `deleting a background tab must not navigate: ${page.url()}`);
    });

    await run("a background session change reaches the strip, + menu and switcher live", async () => {
      await page.goto(`${BASE}/shells/new?project=${encodeURIComponent(project)}`, { waitUntil: "domcontentloaded" });
      const projPath = await page.locator('select[name="project"]').inputValue();
      await page.goto(shellUrls[2], { waitUntil: "domcontentloaded" });
      await page.waitForSelector(".terminal-tabs-new-btn", { state: "attached", timeout: 8000 });
      const ghostUrl = await page.evaluate(async (p) => {
        const token = document.querySelector('meta[name="csrf-token"]').content;
        const res = await fetch("/shells/new", { method: "POST", headers: { "X-CSRF-Token": token, "Content-Type": "application/x-www-form-urlencoded" }, body: "project=" + encodeURIComponent(p) });
        return res.url;
      }, projPath);
      const ghostId = ownId(ghostUrl);
      assert(ghostId && !ghostUrl.includes("/new"), `background shell create failed: ${ghostUrl}`);
      // The strip refreshes live over the server event stream (see live-updates.js),
      // which also keeps the + menu and switcher current, so a shell created in the
      // background shows up in all three without any open-time refetch.
      await page.waitForSelector(tabSel(ghostId), { state: "attached", timeout: 8000 });
      await page.click(".terminal-tabs-new-btn");
      await page.waitForSelector(tabSel(ghostId), { state: "attached", timeout: 8000 });
      await page.keyboard.press("Escape");
      await sleep(300);
      await page.evaluate(async (id) => {
        const token = document.querySelector('meta[name="csrf-token"]').content;
        await fetch("/shells/" + id + "/delete", { method: "POST", headers: { "X-CSRF-Token": token } });
      }, ghostId);
      await page.click(".attach-terminal");
      await sleep(200);
      await page.keyboard.press("Control");
      await page.keyboard.press("Control");
      await page.waitForSelector(".terminal-switcher", { state: "visible", timeout: 4000 });
      await page.waitForSelector(tabSel(ghostId), { state: "detached", timeout: 8000 });
      assert((await page.locator(`.terminal-switcher-item[data-switcher-id="${ghostId}"]`).count()) === 0, "switcher still lists the deleted shell after the refresh");
      await page.keyboard.press("Escape");
      await sleep(200);
    });

    await run("switcher rows carry no close control and no badge, the current row is class-marked", async () => {
      await page.keyboard.press("Control");
      await page.keyboard.press("Control");
      await page.waitForSelector(".terminal-switcher", { state: "visible", timeout: 4000 });
      assert((await page.locator(".terminal-switcher-close").count()) === 0, "switcher rows still carry close buttons");
      assert((await page.locator(".terminal-switcher-badge").count()) === 0, "switcher rows still carry badges");
      const currentId = ownId(page.url());
      const marked = await page.$eval(`.terminal-switcher-item[data-switcher-id="${currentId}"]`, (e) => e.classList.contains("current"));
      assert(marked, "current session row not marked with the current class");
      await page.keyboard.press("Escape");
      await sleep(200);
    });

    await run("closing the current tab switches to its right neighbor like Terminal.app", async () => {
      assert(page.url().includes(ids[2]), `unexpected page ${page.url()}`);
      const order = (await tabOrder()).filter((id) => ids.includes(id));
      assert(order[0] === ids[2] && order[1] === ids[0], `own order before close: ${order}`);
      await page.click(`${tabSel(ids[2])} [data-tab-close]`);
      await L.confirmSwal(page);
      await page.waitForURL(new RegExp(ids[0]), { timeout: 10000 });
      await page.waitForSelector(tabSel(ids[2]), { state: "detached", timeout: 8000 });
      await page.waitForSelector(`${tabSel(ids[0])}.active`, { state: "attached", timeout: 8000 });
      await sleep(800);
      assert((await page.locator(".swal2-toast .swal2-error").count()) === 0, "error toast after tab close");
      const toasts = (await page.locator(".swal2-toast").allTextContents()).join(" ");
      assert(!toasts.includes("Terminal has ended"), "ended toast not suppressed on tab close");
    });

    await run("a stopped coder is resumable from the + menu (grouped by project) and from the switcher", async () => {
      const coderName = `tabres-${tag.slice(-4)}`;
      coderUrl = await L.createSession(page, project, coderName);
      const coderId = ownId(coderUrl);
      await L.stopSession(page, coderUrl);
      coderUrl = null;
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(`#project-${project} form[action^="/coders/"][action$="/resume"]`, { timeout: 8000 });

      await page.goto(shellUrls[0], { waitUntil: "domcontentloaded" });
      await page.waitForSelector(".terminal-tabs-new-btn", { state: "attached", timeout: 8000 });
      await page.click(".terminal-tabs-new-btn");
      await page.waitForSelector("terminal-tabs .dropdown-menu.show", { state: "visible", timeout: 4000 });
      const resumeBtn = `terminal-tabs .dropdown-menu.show [data-tabs-resume-fold="${project}"] [data-resume-id="${coderId}"]`;
      await page.waitForSelector(resumeBtn, { state: "visible", timeout: 4000 });
      const grouped = await page.$eval(resumeBtn, (e) => e.dataset.resumeProject);
      assert(grouped === project, `resume item grouped under '${grouped}'`);
      const headers = await page.$$eval("terminal-tabs .dropdown-menu.show .terminal-tabs-resume-project", (els) => els.map((e) => e.textContent.trim()));
      assert(headers.includes(project), `project headers ${JSON.stringify(headers)} miss ${project}`);
      const wrapper = await page.$eval(`terminal-tabs .dropdown-menu.show [data-tabs-resume-projects] [data-project-name="${project}"]`, (e) => ({ active: e.dataset.projectActive, used: Number(e.dataset.projectUsed) }));
      assert(wrapper.active === "true" && wrapper.used > 0, `resume project wrapper sort attrs ${JSON.stringify(wrapper)}`);
      const form = await page.$eval(resumeBtn, (e) => e.closest("form").getAttribute("action"));
      assert(form === `/coders/${coderId}/resume`, `resume form action ${form}`);
      await page.keyboard.press("Escape");
      await sleep(300);

      await page.click(".attach-terminal");
      await sleep(200);
      await page.keyboard.press("Control");
      await page.keyboard.press("Control");
      await page.waitForSelector(".terminal-switcher", { state: "visible", timeout: 4000 });
      await page.waitForSelector(`.terminal-switcher-item[data-switcher-resume="${coderId}"]`, { state: "attached", timeout: 4000 });
      const openSel = await page.$eval(".terminal-switcher-item.selected", (e) => e.dataset.switcherId || "");
      const activeIds = await page.$$eval(".terminal-switcher-item[data-switcher-id]", (els) => els.map((e) => e.dataset.switcherId));
      const currentRow = await page.$eval(".terminal-switcher-item.current", (e) => e.dataset.switcherId);
      const expectedFwd = activeIds[(activeIds.indexOf(currentRow) + 1) % activeIds.length];
      assert(openSel === expectedFwd, `double tap open selected '${openSel}', expected the next active tab ${expectedFwd} (never an inactive row)`);
      await page.keyboard.press("Escape");
      await sleep(300);

      await page.keyboard.press("Control");
      await page.keyboard.press("Control");
      await page.waitForSelector(".terminal-switcher", { state: "visible", timeout: 4000 });
      const rowSel = `.terminal-switcher-item[data-switcher-resume="${coderId}"]`;
      await page.waitForSelector(rowSel, { state: "attached", timeout: 4000 });
      await page.locator('.terminal-switcher-section[data-switcher-section="inactive"]').waitFor({ state: "visible", timeout: 2000 });
      const groupLabels = await page.$$eval(".terminal-switcher-group:not([hidden])", (els) => els.map((e) => e.textContent.trim()));
      assert(groupLabels.includes(project), `switcher group labels ${JSON.stringify(groupLabels)} miss ${project}`);
      await page.keyboard.type(coderName, { delay: 40 });
      await sleep(200);
      const visible = await page.$$eval(".terminal-switcher-item:not([hidden])", (els) => els.map((e) => e.dataset.switcherResume || e.dataset.switcherId));
      assert(visible.length === 1 && visible[0] === coderId, `filter narrowed to ${visible}`);
      await page.keyboard.press("Enter");
      await page.waitForURL(/\/coders\/(?!new)[^/]+$/, { timeout: 20000 });
      coderUrl = page.url();
      const resumedId = ownId(coderUrl);
      await page.waitForSelector(`${tabSel(resumedId)}.active`, { state: "attached", timeout: 8000 });
      assert((await page.locator(".terminal-switcher").count()) === 0, "switcher still open after resume");
    });

    await run("a coder tab's context menu offers stop but no rename", async () => {
      assert(coderUrl, "no live coder to inspect");
      const resumedId = ownId(coderUrl);
      await openTabMenu(tabSel(resumedId));
      const labels = await page.$$eval(".dc-context-menu .dropdown-item", (els) => els.map((e) => e.textContent.trim()));
      assert(labels.includes("Stop coder"), `coder menu misses Stop coder: ${labels.join(", ")}`);
      assert(!labels.includes("Rename"), "coder menu offers Rename");
      assert(!labels.includes("Delete shell"), "coder menu offers Delete shell");
      await closeTabMenu();
    });

    await run("switching tabs keeps the scroll position instead of jumping to the top", async () => {
      await page.evaluate(() => localStorage.setItem("dc-terminal-rows", "100"));
      await page.evaluate(async (id) => {
        const token = document.querySelector('meta[name="csrf-token"]').content;
        await fetch(`/shells/${id}/input`, { method: "POST", headers: { "X-CSRF-Token": token, "Content-Type": "application/json" }, body: JSON.stringify({ items: [{ prompt: "seq 1 300" }] }) });
      }, ids[0]);
      await sleep(800);
      await page.goto(shellUrls[1], { waitUntil: "domcontentloaded" });
      await page.waitForSelector(tabSel(ids[0]), { state: "attached", timeout: 8000 });
      await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight));
      await sleep(300);
      const before = await page.evaluate(() => window.scrollY);
      assert(before > 200, `page did not scroll (scrollY ${before}), rows setting ineffective?`);
      await page.click(tabSel(ids[0]));
      await page.waitForURL(new RegExp(ids[0]), { timeout: 8000 });
      await sleep(500);
      const after = await page.evaluate(() => window.scrollY);
      assert(after > 200, `view jumped to the top on tab switch (scrollY ${after})`);
      await page.evaluate(() => localStorage.removeItem("dc-terminal-rows"));
    });

    await run("pages without the strip mount a hidden switcher-only instance, double Ctrl works app wide", async () => {
      await page.goto(`${BASE}/shells/new?project=${encodeURIComponent(project)}`, { waitUntil: "domcontentloaded" });
      const projPath = await page.locator('select[name="project"]').inputValue();
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      assert((await L.waitUpgraded(page, ["terminal-tabs"], 8000)).length === 0, "terminal-tabs not upgraded on /projects");
      const mode = await page.$eval("terminal-tabs", (e) => ({
        hidden: e.hasAttribute("hidden"),
        display: getComputedStyle(e).display,
        tabs: e.querySelectorAll(".terminal-tab").length,
      }));
      assert(mode.hidden && mode.display === "none", `layout instance not hidden: ${JSON.stringify(mode)}`);
      assert(mode.tabs > 0, "hidden strip carries no tabs");
      // Ctrl+Tab stays with the page while the switcher is closed (the editor
      // binds it for its own tabs), the switcher-only instance must not act.
      await page.keyboard.down("Control");
      await page.keyboard.press("Tab");
      await page.keyboard.up("Control");
      await sleep(400);
      assert(page.url().endsWith("/projects"), `Ctrl+Tab navigated away to ${page.url()}`);
      assert((await page.locator(".terminal-switcher").count()) === 0, "Ctrl+Tab opened the switcher on /projects");
      // A background change while the switcher is closed does not pull the
      // fragment eagerly; opening the switcher flushes the deferred refresh.
      const ghostUrl = await page.evaluate(async (p) => {
        const token = document.querySelector('meta[name="csrf-token"]').content;
        const res = await fetch("/shells/new", { method: "POST", headers: { "X-CSRF-Token": token, "Content-Type": "application/x-www-form-urlencoded" }, body: "project=" + encodeURIComponent(p) });
        return res.url;
      }, projPath);
      const ghostId = ownId(ghostUrl);
      assert(ghostId && !ghostUrl.includes("/new"), `background shell create failed: ${ghostUrl}`);
      let opened = null;
      for (let attempt = 0; attempt < 3 && !opened; attempt += 1) {
        await page.keyboard.press("Control");
        await sleep(120);
        await page.keyboard.press("Control");
        opened = await page.waitForSelector(".terminal-switcher", { state: "visible", timeout: 2500 }).catch(() => null);
        if (!opened) await sleep(600);
      }
      assert(opened, "switcher did not open on /projects");
      await page.waitForSelector(`.terminal-switcher-item[data-switcher-id="${ghostId}"]`, { state: "attached", timeout: 8000 });
      await page.evaluate(async (id) => {
        const token = document.querySelector('meta[name="csrf-token"]').content;
        await fetch("/shells/" + id + "/delete", { method: "POST", headers: { "X-CSRF-Token": token } });
      }, ghostId);
      await page.waitForSelector(`.terminal-switcher-item[data-switcher-id="${ghostId}"]`, { state: "detached", timeout: 8000 });
      // The switcher is a full quick-access palette: editor rows for every
      // project and New coder / New shell rows with the page's context.
      const editorUrls = await page.$$eval('.terminal-switcher-item[data-switcher-section="editors"]', (els) => els.map((e) => e.dataset.switcherUrl));
      assert(editorUrls.some((u) => u.startsWith(`/projects/${project}/editor`)), `editors section misses ${project}: ${JSON.stringify(editorUrls)}`);
      const actionUrls = await page.$$eval('.terminal-switcher-item[data-switcher-section="new"]', (els) => els.map((e) => e.dataset.switcherUrl));
      assert(actionUrls.length === 2 && actionUrls[0].startsWith("/coders/new?") && actionUrls[1].startsWith("/shells/new?"), `action rows ${JSON.stringify(actionUrls)}`);
      assert(actionUrls.every((u) => u.includes("return=")), `action rows carry no return context: ${JSON.stringify(actionUrls)}`);
      // Click switches to the session from the strip-less page.
      await page.click(`.terminal-switcher-item[data-switcher-id="${ids[0]}"]`);
      await page.waitForURL(new RegExp(ids[0]), { timeout: 8000 });
      assert((await page.locator(".terminal-switcher").count()) === 0, "switcher still open after the switch");
    });

    await run("double Ctrl opens the switcher on the editor page too, Esc returns to editing", async () => {
      await page.goto(`${BASE}/projects/${project}/editor`, { waitUntil: "domcontentloaded" });
      assert((await L.waitUpgraded(page, ["terminal-tabs"], 8000)).length === 0, "terminal-tabs not upgraded on the editor page");
      await page.keyboard.down("Control");
      await page.keyboard.press("Tab");
      await page.keyboard.up("Control");
      await sleep(400);
      assert((await page.locator(".terminal-switcher").count()) === 0, "Ctrl+Tab opened the switcher on the editor page");
      assert(page.url().includes("/editor"), `Ctrl+Tab navigated away to ${page.url()}`);
      await page.keyboard.press("Control");
      await page.keyboard.press("Control");
      await page.waitForSelector(".terminal-switcher", { state: "visible", timeout: 4000 });
      await page.keyboard.press("Escape");
      await sleep(200);
      assert((await page.locator(".terminal-switcher").count()) === 0, "Esc did not close the switcher on the editor page");
      assert(page.url().includes("/editor"), "closing the switcher navigated away from the editor");
    });

    await run("switcher editor rows open the project editor, the New shell row preselects the project", async () => {
      await page.keyboard.press("Control");
      await page.keyboard.press("Control");
      await page.waitForSelector(".terminal-switcher", { state: "visible", timeout: 4000 });
      await page.keyboard.type("editor", { delay: 30 });
      await sleep(200);
      const sections = await page.$$eval(".terminal-switcher-item:not([hidden])", (els) => els.map((e) => e.dataset.switcherSection));
      assert(sections.length && sections.every((s) => s === "editors"), `filter 'editor' shows ${JSON.stringify(sections)}`);
      await page.click(`.terminal-switcher-item[data-switcher-url^="/projects/${foreignProject}/editor"]`);
      await page.waitForURL(new RegExp(`/projects/${foreignProject}/editor`), { timeout: 8000 });
      await page.keyboard.press("Control");
      await page.keyboard.press("Control");
      await page.waitForSelector(".terminal-switcher", { state: "visible", timeout: 4000 });
      await page.click('.terminal-switcher-item[data-switcher-section="new"][data-switcher-url^="/shells/new"]');
      await page.waitForURL(/\/shells\/new/, { timeout: 8000 });
      const selectedPath = await page.locator('select[name="project"]').inputValue();
      assert(selectedPath.endsWith(`/${foreignProject}`), `preselected project path '${selectedPath}'`);
    });

    await run("the strip stays hidden on coarse pointer (mobile) clients", async () => {
      const mp = await mobilePage();
      await mp.goto(shellUrls[0], { waitUntil: "domcontentloaded" });
      await mp.waitForSelector("terminal-tabs", { state: "attached", timeout: 8000 });
      const display = await mp.$eval("terminal-tabs", (e) => getComputedStyle(e).display);
      assert(display === "none", `terminal-tabs display '${display}' on mobile`);
      const settingsVisible = await mp.$eval(".attach-settings", (e) => getComputedStyle(e).display !== "none");
      assert(settingsVisible, "settings row hidden on mobile");
      assert(await mp.$(".attach-settings .ti-settings"), "mobile settings gear button missing");
      assert(await mp.$(".attach-settings .ti-terminal-2"), "mobile shell icon badge missing on the shell page");
      await mp.click(".attach-settings [data-bs-toggle=dropdown]");
      await mp.waitForSelector(".attach-settings .dropdown-menu.show", { state: "visible", timeout: 4000 });
      const mobileSelects = await mp.locator(".attach-settings terminal-setting-select select").count();
      assert(mobileSelects === 3, `mobile settings menu carries ${mobileSelects} selects`);
      const rowsSel = '.attach-settings terminal-setting-select[setting="rows"] select';
      const before = await mp.$eval(rowsSel, (el) => el.value);
      await mp.selectOption(rowsSel, "40");
      await sleep(200);
      assert(await mp.evaluate(() => localStorage.getItem("dc-terminal-rows")) === "40", "rows not persisted from the mobile settings menu");
      await mp.selectOption(rowsSel, before);
      await sleep(200);
    });
  } finally {
    if (coderUrl) await L.stopSession(page, coderUrl).catch(() => {});
    if (foreignShellUrl) await L.deleteShell(page, foreignShellUrl).catch(() => {});
    for (const u of shellUrls) await L.deleteShell(page, u).catch(() => {});
    if (foreignProject) await L.deleteProject(page, foreignProject).catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
  }
});
