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
// first. Tabs reorder by pointer drag. The switcher popup opens via Ctrl+Tab
// (browsers often reserve it, so headless and PWAs only) and via double tapping
// Ctrl or Meta within 400ms (collision free: bare modifiers never reach the
// shell, a chord like Ctrl+C cancels the tap); cycle via Tab, Ctrl+Tab or
// arrows, Enter or click switches, Esc closes, typing filters the list by name
// and project through the search input, and it all
// works while the xterm terminal has focus. Opening anchors on the active
// tabs: the initial selection moves one step from the current session and
// wraps within the actives (first tab + backward = last active, last tab +
// forward = first active, never an inactive row); after opening, cycling
// rotates over the full list including the inactive section. Every tab carries a close control
// (confirm dialog, then coder stop or shell delete; closing the current session
// switches to the right neighbor tab like Terminal.app, left as fallback,
// projects page when the strip is empty; the page header stop button keeps its
// own redirect+flash flow). Switcher rows deliberately carry no close control
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
// desktop attach footer (Ctrl+Ctrl in the browser, Ctrl+Tab in standalone/PWA
// via display-mode media query): always visible, no strip space. The + menu
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
      const dotHidden = await page.$eval(`${tabSel(ids[0])} .status-dot`, (e) => getComputedStyle(e).display === "none");
      assert(dotHidden, "idle status dot visible in a tab");
      const hintVisible = await page.$eval(".attach-desktop .switcher-hint-browser", (e) => getComputedStyle(e).display !== "none" && e.textContent.includes("Ctrl"));
      assert(hintVisible, "switcher shortcut hint not visible in the desktop footer");
      const sticky = await page.$eval("terminal-tabs", (e) => getComputedStyle(e).position === "sticky");
      assert(sticky, "tab strip is not sticky");
      const appHidden = await page.$eval(".attach-desktop .switcher-hint-app", (e) => getComputedStyle(e).display === "none");
      assert(appHidden, "web app hint variant visible outside standalone mode");
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

    await run("Ctrl+Tab opens the switcher while the terminal is focused, cycles, Enter switches", async () => {
      await page.click(".attach-terminal");
      await sleep(200);
      await page.keyboard.down("Control");
      await page.keyboard.press("Tab");
      await page.waitForSelector(".terminal-switcher", { state: "visible", timeout: 4000 });
      const count = await page.locator(".terminal-switcher-item").count();
      assert(count >= 3, `switcher lists ${count} items`);
      let selected = "";
      for (let i = 0; i < count + 1; i++) {
        selected = await page.$eval(".terminal-switcher-item.selected", (e) => e.dataset.switcherId);
        if (selected === ids[1]) break;
        await page.keyboard.press("Tab");
        await sleep(80);
      }
      await page.keyboard.up("Control");
      assert(selected === ids[1], "could not cycle to the target shell");
      await page.keyboard.press("Enter");
      await page.waitForURL(new RegExp(ids[1]), { timeout: 8000 });
      assert((await page.locator(".terminal-switcher").count()) === 0, "switcher still open after Enter");
    });

    await run("arrow keys move the switcher selection, Esc closes without navigating", async () => {
      const before = page.url();
      await page.keyboard.down("Control");
      await page.keyboard.press("Tab");
      await page.keyboard.up("Control");
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
      await page.keyboard.down("Control");
      await page.keyboard.press("Tab");
      await page.keyboard.up("Control");
      await page.waitForSelector(".terminal-switcher", { state: "visible", timeout: 4000 });
      await page.click(`.terminal-switcher-item[data-switcher-id="${ids[2]}"]`);
      await page.waitForURL(new RegExp(ids[2]), { timeout: 8000 });
    });

    await run("the strip settings menu adjusts rows and stays open while selecting", async () => {
      await page.click(".terminal-tabs-settings > button");
      await page.waitForSelector(".terminal-tabs-settings .dropdown-menu.show", { state: "visible", timeout: 4000 });
      const selects = await page.locator(".terminal-tabs-settings terminal-setting-select select").count();
      assert(selects === 2, `settings menu carries ${selects} selects, expected font size and rows`);
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
      const hrefs = await page.$$eval("terminal-tabs .dropdown-menu.show a", (as) => as.map((a) => a.getAttribute("href")));
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

    await run("typing in the switcher filters the list, Enter opens the first match", async () => {
      await page.keyboard.press("Control");
      await page.keyboard.press("Control");
      await page.waitForSelector(".terminal-switcher", { state: "visible", timeout: 4000 });
      await page.keyboard.type(project, { delay: 40 });
      await sleep(200);
      const visible = await page.$$eval(".terminal-switcher-item:not([hidden])", (els) => els.map((e) => e.dataset.switcherId));
      assert(visible.length === 4, `filter shows ${visible.length} rows, expected the 4 own shells`);
      assert(visible.every((id) => [...ids, ownId(shellUrls[3])].includes(id)), `filter leaked foreign rows: ${visible}`);
      const hiddenCount = await page.locator(".terminal-switcher-item[hidden]").count();
      assert(hiddenCount > 0, "no rows were filtered out");
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

    await run("opening the + menu and the switcher refreshes the strip in the background", async () => {
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
      assert(!(await page.$(tabSel(ghostId))), "strip shows the new shell before any refresh");
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
    });

    await run("a stopped coder is resumable from the + menu (grouped by project) and from the switcher", async () => {
      const coderName = `tabres-${tag.slice(-4)}`;
      coderUrl = await L.createSession(page, project, coderName);
      const coderId = ownId(coderUrl);
      await L.stopSession(page, coderUrl);
      coderUrl = null;
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(`#project-${project}-coders form[action^="/coders/"][action$="/resume"]`, { timeout: 8000 });

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
      await page.keyboard.down("Control");
      await page.keyboard.down("Shift");
      await page.keyboard.press("Tab");
      await page.keyboard.up("Shift");
      await page.keyboard.up("Control");
      await page.waitForSelector(".terminal-switcher", { state: "visible", timeout: 4000 });
      await page.waitForSelector(`.terminal-switcher-item[data-switcher-resume="${coderId}"]`, { state: "attached", timeout: 4000 });
      const backId = await page.$eval(".terminal-switcher-item.selected", (e) => e.dataset.switcherId || "");
      const activeIds = await page.$$eval(".terminal-switcher-item[data-switcher-id]", (els) => els.map((e) => e.dataset.switcherId));
      const currentRow = await page.$eval(".terminal-switcher-item.current", (e) => e.dataset.switcherId);
      const expectedBack = activeIds[(activeIds.indexOf(currentRow) - 1 + activeIds.length) % activeIds.length];
      assert(backId === expectedBack, `backward open selected '${backId}', expected the previous active tab ${expectedBack} (never an inactive row)`);
      await page.keyboard.press("Escape");
      await sleep(300);

      await page.keyboard.press("Control");
      await page.keyboard.press("Control");
      await page.waitForSelector(".terminal-switcher", { state: "visible", timeout: 4000 });
      const rowSel = `.terminal-switcher-item[data-switcher-resume="${coderId}"]`;
      await page.waitForSelector(rowSel, { state: "attached", timeout: 4000 });
      await page.locator(".terminal-switcher-section").waitFor({ state: "visible", timeout: 2000 });
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
      assert(mobileSelects === 2, `mobile settings menu carries ${mobileSelects} selects`);
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
    for (const u of shellUrls) await L.deleteShell(page, u).catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
  }
});
