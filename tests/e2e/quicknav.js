const L = require("./lib");
const { assert, sleep, BASE } = L;

// Quick nav: the floating menu present on every page. Custom element dc-quicknav
// (Bootstrap dropdown). The toggle opens it and lazy loads /quicknav into
// [data-quicknav-tabs]; two segments Active and Projects; [data-pb-drill] opens a
// project detail, [data-pb-back] returns; a project scoped page sets
// data-quicknav-current-project; a group over 5 entries folds with
// [data-qn-fold-toggle]; the project order is shared with the projects page.
// The Projects tab is a palette built like the editor's project switcher: a
// search field ([data-pb-filter]) in the sticky header over the rows
// ([data-pb-rows]), the server's rows kept out of sight in [data-pb-source] and
// cloned into groups (Recent over All projects without a query, one ranked list
// with one), the query matching name, repository and branch together over
// data-project-search, the arrows walking the rows (.quicknav-pb-active) and
// Enter drilling into the marked one. The field takes the focus and a row is
// marked only where a keyboard is around (hover/fine pointer), so opening the
// tab on a phone throws no keyboard up; the head steps aside for the Active tab
// and while a project is drilled open.
// The Active pane is one flat list of every live coder and shell, no per-kind
// grouping, sorted and drag reorderable through the same cross-device @dc_tab_pos
// state the attach page tab strip uses (POST /terminal-tabs/order); the New coder
// and New shell buttons sit at the end of that list. Entering that list (opening
// the menu on it, or switching to it) scrolls the terminal the user stands on
// into the middle of the menu, once the menu is on screen and has a height to
// centre against (bootstrap's show fires before that, shown after it). It is a
// one shot that survives the background refresh behind the open, which restores
// the menu's scroll offset instead of dropping the reader back to the top, and
// off a terminal page nothing is current and the list stays where it was. That
// refresh reconciles the fresh markup against the standing menu instead of
// replacing it, so a node that did not change is never detached and keeps its
// running animation (the compose spinner would otherwise restart on every
// container that moves). What is hidden in the menu stays the client's: the
// server renders every pane and project detail hidden and applyState decides,
// so the refresh must not carry that attribute over, or the open detail goes
// display:none for as long as it takes to re-apply the view and every
// animation under it is thrown away. On touch a swipe left on a
// row reveals a delete action (row wrapper .quicknav-swipe-row, button
// [data-qn-delete]) that behaves exactly like the desktop tab strip's close
// control: confirm dialog, POST /coders/:id/stop or /shells/:id/delete, and
// deleting the terminal you are attached to navigates to its neighbor. Shell
// rows also reveal a rename action ([data-qn-rename], prompt dialog, POST
// /shells/:id/rename). Coder rows reveal a second action ([data-qn-purge], POST
// /coders/:id/delete) that deletes the coder outright, the server stops it
// first. The Projects tab detail mirrors the projects page chip
// row: no per-kind headlines, one merged list of the live coders and shells in
// tab strip order, its containers under them, inactive coders after them. The
// project's actions stand on one line under the title, right aligned and icons
// only ([data-pb-actions], the shape the context bar above already uses):
// editor, new coder and new shell always, the git menu ahead of them where the
// project is a repository and the compose menu where it runs containers, both
// the projects page's own menus out of @dc/project-actions. A container row
// answers a plain click with its own menu, running or not, the way the
// editor's container cells do, and reveals its logs on a swipe. Detail rows
// swipe too, reorder stays active-list only: active coders
// reveal stop plus delete, inactive coders reveal delete (POST
// /coders/:id/delete, projects page confirm wording), shells reveal rename plus
// delete. A coder created through the quick nav lands on the coder's own page
// even while standing on the editor page: the editor comeback belongs to the
// panel + menu's panel=1 marker alone, the quick nav's editor return serves
// only the form's Cancel.

L.runFeature("QUICKNAV", async ({ page, run, mobilePage }) => {
  const tag = `qn-${Date.now().toString(36)}`;
  const project = `zztc-${tag}`;
  const shellUrls = [];
  let dragIds = [];
  let dragUrls = [];
  let retCoderPath = null;
  // Synthetic touch swipe left on a row's anchor, shared by the swipe checks in
  // both quick nav tabs. Long enough to fling past the widest action reveal.
  const swipeRow = (pg, sel) => pg.evaluate(async (sel) => {
    const item = document.querySelector(sel);
    item.scrollIntoView({ block: "center" });
    const r = item.getBoundingClientRect();
    let x = r.left + r.width * 0.6;
    const y = r.top + r.height / 2;
    const ev = (type, opts) => item.dispatchEvent(new PointerEvent(type, Object.assign({ bubbles: true, composed: true, pointerId: 41, pointerType: "touch", isPrimary: true, button: 0, buttons: 1, clientX: x, clientY: y }, opts)));
    const tick = () => new Promise((res) => setTimeout(res, 16));
    ev("pointerdown", {}); await tick();
    for (let i = 0; i < 12; i++) { x -= 16; ev("pointermove", { clientX: x }); await tick(); }
    ev("pointerup", { buttons: 0, clientX: x });
  }, sel);
  const fillPrompt = async (pg, value) => {
    await pg.waitForSelector(".swal2-input", { state: "visible", timeout: 8000 });
    await pg.fill(".swal2-input", value);
    await L.confirmSwal(pg);
  };
  try {
    // The quick nav shows exactly where the editor's terminal panel does not
    // (below md, low windows, coarse pointers); the assistant's corner button
    // replaces it everywhere else, so this whole runner drives it in a window
    // below md.
    await page.setViewportSize({ width: 750, height: 900 });
    await L.createProject(page, project);

    await run("toggle opens, lazy loads tabs, drill + back", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      assert((await L.waitUpgraded(page, ["dc-quicknav"], 8000)).length === 0, "dc-quicknav not upgraded");
      await page.click(".quicknav-toggle");
      await page.waitForSelector("[data-quicknav-tabs]", { state: "visible", timeout: 6000 });
      assert((await page.$$("[data-quicknav-tab]")).length >= 2, "expected >=2 tabs");
      await page.locator('[data-quicknav-tab="projects"]:visible').first().click(); await sleep(500);
      const drill = page.locator(`[data-pb-drill="${project}"]`).first();
      await drill.waitFor({ state: "visible", timeout: 6000 });
      await drill.click();
      await page.locator(`[data-pb-detail="${project}"]`).first().waitFor({ state: "visible", timeout: 6000 });
      await page.locator("[data-pb-back]:visible").first().click(); await sleep(200);
    });

    await run("projects tab is a palette: field on top, query over name/repo/branch, arrows and Enter drill", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await page.click(".quicknav-toggle");
      await page.waitForSelector("[data-quicknav-tabs]", { state: "visible", timeout: 6000 });
      await page.locator('[data-quicknav-tab="projects"]:visible').first().click(); await sleep(400);
      await page.waitForSelector("[data-pb-rows] [data-pb-drill]", { state: "visible", timeout: 6000 });
      const head = await page.evaluate(() => {
        const field = document.querySelector("[data-pb-filter]");
        const list = document.querySelector("[data-pb-rows]");
        return {
          above: field.getBoundingClientRect().bottom <= list.getBoundingClientRect().top + 2,
          focused: document.activeElement === field,
          marked: Boolean(document.querySelector(".quicknav-pb-active")),
          source: document.querySelector("[data-pb-source]").hidden,
        };
      });
      assert(head.above, "the search field does not stand over the rows");
      assert(head.focused, "the field did not take the focus where a keyboard is around");
      assert(head.marked, "no row is marked for Enter");
      assert(head.source, "the server's rows are not kept out of sight");
      // The scratch project's branch is what a fresh git init leaves, so the
      // query goes over the name; the search line carries all three fields.
      const line = await page.$eval(`[data-pb-rows] [data-pb-drill="${project}"]`, (el) => el.getAttribute("data-project-search"));
      assert(line && line.includes(project), `search line ${line} does not carry the name`);
      await page.fill("[data-pb-filter]", project);
      await sleep(300);
      const hits = await page.$$eval("[data-pb-rows] [data-pb-drill]", (els) => els.map((el) => el.getAttribute("data-pb-drill")));
      assert(hits.length && hits.every((name) => name.includes(project)), `query left foreign rows: ${hits.join()}`);
      // A query nobody can match says so in the list, under the field.
      await page.fill("[data-pb-filter]", `${project}-nothing-matches-this`);
      await sleep(300);
      assert(await page.$eval("[data-pb-empty]", (el) => !el.hidden), "no matches note stayed hidden");
      await page.fill("[data-pb-filter]", project);
      await sleep(300);
      await page.keyboard.press("ArrowDown");
      await page.keyboard.press("ArrowUp");
      await page.keyboard.press("Enter");
      await page.locator(`[data-pb-detail="${project}"]`).first().waitFor({ state: "visible", timeout: 6000 });
      assert(await page.$eval("[data-pb-head]", (el) => el.hidden), "the field stayed up over a drilled project");
      await page.locator("[data-pb-back]:visible").first().click(); await sleep(300);
      assert(await page.$eval("[data-pb-head]", (el) => !el.hidden), "the field did not come back with the list");
      await page.locator('[data-quicknav-tab="active"]:visible').first().click(); await sleep(200);
      assert(await page.$eval("[data-pb-head]", (el) => el.hidden), "the field stayed up on the Active tab");
    });

    await run("mobile: the projects palette waits for a tap, no keyboard on open", async () => {
      const mp = await mobilePage();
      await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await mp.click(".quicknav-toggle");
      await mp.waitForSelector("[data-quicknav-tabs]", { state: "visible", timeout: 6000 });
      await mp.locator('[data-quicknav-tab="projects"]:visible').first().click(); await sleep(400);
      await mp.waitForSelector("[data-pb-rows] [data-pb-drill]", { state: "visible", timeout: 6000 });
      const idle = await mp.evaluate(() => ({
        focused: document.activeElement === document.querySelector("[data-pb-filter]"),
        marked: Boolean(document.querySelector(".quicknav-pb-active")),
      }));
      assert(!idle.focused, "the field grabbed the focus on a touch screen");
      assert(!idle.marked, "a row was marked on a touch screen");
      await mp.click("[data-pb-filter]");
      await mp.fill("[data-pb-filter]", project);
      await sleep(300);
      assert(await mp.evaluate(() => document.activeElement === document.querySelector("[data-pb-filter]")), "the tap did not sharpen the field");
      const hits = await mp.$$eval("[data-pb-rows] [data-pb-drill]", (els) => els.map((el) => el.getAttribute("data-pb-drill")));
      assert(hits.length && hits.every((name) => name.includes(project)), `query left foreign rows: ${hits.join()}`);
      await mp.locator(`[data-pb-rows] [data-pb-drill="${project}"]`).first().click();
      await mp.locator(`[data-pb-detail="${project}"]`).first().waitFor({ state: "visible", timeout: 6000 });
    });

    await run("active pane is one @dc_tab_pos-sorted list, drag reorders it like the tab strip, new buttons last", async () => {
      for (let i = 0; i < 3; i++) { dragUrls.push(await L.createShell(page, project)); await sleep(1100); }
      shellUrls.push(...dragUrls);
      const dids = dragUrls.map((u) => new URL(u).pathname.split("/").pop());
      dragIds = dids;
      const activeSel = (id) => `[data-quicknav-active-list] .quicknav-active-item[data-tab-id="${id}"]`;
      const activeOrder = async () => (await page.$$eval("[data-quicknav-active-list] .quicknav-active-item", (els) => els.map((e) => e.dataset.tabId))).filter((id) => dids.includes(id));
      const openNav = async () => {
        await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
        await page.click(".quicknav-toggle");
        await page.waitForSelector("[data-quicknav-tabs]", { state: "visible", timeout: 6000 });
        // The menu remembers the last segment (a prior run left it on Projects).
        await page.locator('[data-quicknav-tab="active"]:visible').first().click();
        await page.waitForSelector("[data-quicknav-active-list]", { state: "visible", timeout: 6000 });
        for (const id of dids) await page.waitForSelector(activeSel(id), { state: "visible", timeout: 6000 });
        // The list also holds the host's own live sessions, so scroll the newest
        // (this run's) rows into the menu's interactable region before a drag.
        await page.$eval(".quicknav-menu.show", (m) => { m.scrollTop = m.scrollHeight; });
        await sleep(300);
      };

      await openNav();
      assert(JSON.stringify(await activeOrder()) === JSON.stringify(dids), "active list is not in @dc_tab_pos (creation) order");

      // New coder / New shell sit after the last item, not grouped per kind.
      const layout = await page.evaluate(() => {
        const pane = document.querySelector('[data-quicknav-pane="active"]');
        const nodes = [...pane.querySelectorAll('.quicknav-active-item, a[href^="/coders/new"], a[href^="/shells/new"]')];
        const lastItem = nodes.reduce((acc, n, i) => (n.classList.contains("quicknav-active-item") ? i : acc), -1);
        return { lastItem, newCoder: nodes.findIndex((n) => (n.getAttribute("href") || "").startsWith("/coders/new")), newShell: nodes.findIndex((n) => (n.getAttribute("href") || "").startsWith("/shells/new")) };
      });
      assert(layout.newCoder > layout.lastItem && layout.newShell > layout.lastItem, `new buttons not after the items: ${JSON.stringify(layout)}`);

      // Mouse-drag the last of this run's rows above the first, expect [d2, d0, d1].
      const s = await page.locator(activeSel(dids[2])).boundingBox();
      const d = await page.locator(activeSel(dids[0])).boundingBox();
      assert(s && d, "active item boxes unavailable");
      const from = { x: s.x + s.width / 2, y: s.y + s.height / 2 };
      const to = { x: from.x, y: d.y + d.height * 0.2 };
      await page.mouse.move(from.x, from.y);
      await page.mouse.down();
      for (let i = 1; i <= 12; i++) { await page.mouse.move(to.x, from.y + (to.y - from.y) * (i / 12), { steps: 2 }); await sleep(30); }
      await page.mouse.up();
      await sleep(800);
      assert(JSON.stringify(await activeOrder()) === JSON.stringify([dids[2], dids[0], dids[1]]), "active list did not reorder on drag");

      await openNav();
      assert(JSON.stringify(await activeOrder()) === JSON.stringify([dids[2], dids[0], dids[1]]), "dragged order did not persist across a reopen");

      // Same cross-device @dc_tab_pos state the tab strip reads, so both agree.
      await page.goto(dragUrls[0], { waitUntil: "domcontentloaded" });
      await page.waitForSelector(`terminal-tabs .terminal-tab[data-tab-id="${dids[2]}"]`, { state: "attached", timeout: 8000 });
      const strip = (await page.$$eval("terminal-tabs .terminal-tab", (els) => els.map((e) => e.dataset.tabId))).filter((id) => dids.includes(id));
      assert(JSON.stringify(strip) === JSON.stringify([dids[2], dids[0], dids[1]]), `tab strip order ${strip} disagrees with the quick nav`);
    });

    // A mouse press starts a reorder candidate on the whole row; it must not
    // capture the pointer before the drag begins, otherwise the click retargets
    // to the wrapper div and pe.js never sees the anchor (desktop items dead).
    await run("desktop: clicking an item navigates to its terminal", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await page.click(".quicknav-toggle");
      await page.waitForSelector("[data-quicknav-tabs]", { state: "visible", timeout: 6000 });
      await page.locator('[data-quicknav-tab="active"]:visible').first().click();
      const sel = `[data-quicknav-active-list] .quicknav-active-item[data-tab-id="${dragIds[1]}"]`;
      await page.waitForSelector(sel, { state: "visible", timeout: 6000 });
      await page.$eval(".quicknav-menu.show", (m) => { m.scrollTop = m.scrollHeight; });
      await sleep(200);
      await page.click(sel);
      await page.waitForURL(new RegExp(dragIds[1]), { timeout: 8000 });
    });

    await run("active tab: opening the nav puts the terminal you stand on in the middle of the menu", async () => {
      // A short window makes the menu short (height: min(60vh, 28rem)), so the
      // list is longer than the box and the centring has something to do.
      await page.setViewportSize({ width: 750, height: 380 });
      // Enough rows to overflow the short menu. They go again at the end of the
      // check: past five entries the project detail folds, and the swipe checks
      // below reach for rows that would then be hidden.
      const extra = [];
      while (shellUrls.length < 7) {
        const url = await L.createShell(page, project);
        shellUrls.push(url);
        extra.push(url);
        await sleep(900);
      }
      const openNav = async () => {
        await page.click(".quicknav-toggle");
        await page.waitForSelector("[data-quicknav-active-list]", { state: "visible", timeout: 8000 });
        await sleep(700);
      };
      const seen = () => page.evaluate(() => {
        const menu = document.querySelector(".quicknav-menu.show");
        const rows = [...menu.querySelectorAll('[data-quicknav-pane="active"] .quicknav-active-item[data-tab-kind="shell"]')];
        const row = menu.querySelector('[data-quicknav-pane="active"] [aria-current="true"]');
        const mr = menu.getBoundingClientRect();
        const r = row && row.getBoundingClientRect();
        return {
          ids: rows.map((e) => e.dataset.tabId),
          current: row ? row.dataset.tabId : null,
          scrolls: menu.scrollHeight > menu.clientHeight + 1,
          scrollTop: Math.round(menu.scrollTop),
          inside: r ? r.top >= mr.top - 0.5 && r.bottom <= mr.bottom + 0.5 : false,
          off: r ? Math.round(Math.abs((r.top + r.height / 2) - (mr.top + mr.height / 2))) : -1,
        };
      });

      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await openNav();
      const list = await seen();
      assert(list.scrolls, "the active list does not overflow the short menu, the check would prove nothing");
      // A row from the middle of the list: the top rows are reachable without
      // scrolling, so they would pass on an untouched menu too.
      const mid = list.ids[Math.floor(list.ids.length / 2)];
      assert(mid, `no shell rows in the active pane: ${JSON.stringify(list.ids)}`);

      await page.goto(`${BASE}/shells/${mid}`, { waitUntil: "domcontentloaded" });
      await sleep(900);
      await openNav();
      const on = await seen();
      assert(on.current === mid, `the current row is ${on.current}, expected ${mid}`);
      assert(on.scrollTop > 0, "the menu never scrolled, the current row was left wherever it fell");
      assert(on.inside, "the current row is not inside the menu");
      assert(on.off <= 30, `the current row sits ${on.off}px off the menu's middle`);

      // Off a terminal page nothing is current, and the list stays at its top.
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await openNav();
      const off = await seen();
      assert(off.current === null, `a row claims to be current off a terminal page: ${off.current}`);
      assert(off.scrollTop === 0, `the menu scrolled to ${off.scrollTop} with nothing to centre`);
      await page.setViewportSize({ width: 750, height: 900 });
      for (const u of extra) {
        await L.deleteShell(page, u);
        shellUrls.splice(shellUrls.indexOf(u), 1);
      }
    });

    await run("mobile: the grip handle reorders the active list (whole-row touch still scrolls)", async () => {
      const mp = await mobilePage();
      const activeSel = (id) => `[data-quicknav-active-list] .quicknav-active-item[data-tab-id="${id}"]`;
      const mineOrder = async () => (await mp.$$eval("[data-quicknav-active-list] .quicknav-active-item", (els) => els.map((e) => e.dataset.tabId))).filter((id) => dragIds.includes(id));
      await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await mp.waitForSelector(".quicknav-toggle", { state: "visible", timeout: 8000 });
      await mp.click(".quicknav-toggle");
      await mp.waitForSelector("[data-quicknav-tabs]", { state: "visible", timeout: 6000 });
      await mp.locator('[data-quicknav-tab="active"]:visible').first().click();
      await mp.waitForSelector("[data-quicknav-active-list]", { state: "visible", timeout: 6000 });
      for (const id of dragIds) await mp.waitForSelector(activeSel(id), { timeout: 6000 });
      await mp.$eval(".quicknav-menu.show", (m) => { m.scrollTop = m.scrollHeight; });
      await sleep(300);
      const before = await mineOrder();
      const dragId = before[before.length - 1];
      const targetId = before[0];
      // Synthetic touch stream on the grip handle: a whole-row touch would scroll,
      // only the handle (touch-action: none) drives a reorder.
      await mp.evaluate(async ({ dragId, targetId }) => {
        const list = document.querySelector("[data-quicknav-active-list]");
        const handle = list.querySelector(`.quicknav-active-item[data-tab-id="${dragId}"] [data-qn-drag-handle]`);
        const target = list.querySelector(`.quicknav-active-item[data-tab-id="${targetId}"]`);
        const hr = handle.getBoundingClientRect();
        const x = hr.left + hr.width / 2;
        let y = hr.top + hr.height / 2;
        const goalY = target.getBoundingClientRect().top - 6;
        const ev = (type, opts) => handle.dispatchEvent(new PointerEvent(type, Object.assign({ bubbles: true, composed: true, pointerId: 21, pointerType: "touch", isPrimary: true, button: 0, buttons: 1, clientX: x, clientY: y }, opts)));
        const tick = () => new Promise((r) => setTimeout(r, 16));
        ev("pointerdown", {}); await tick();
        while (y > goalY) { y -= 12; ev("pointermove", { clientY: y }); await tick(); }
        ev("pointermove", { clientY: goalY }); await tick();
        ev("pointerup", { buttons: 0, clientY: goalY });
      }, { dragId, targetId });
      await sleep(800);
      const after = await mineOrder();
      assert(JSON.stringify(after) !== JSON.stringify(before), `touch handle drag did not reorder: ${after}`);
      assert(after.indexOf(dragId) < before.indexOf(dragId), `handle-dragged row did not move up: ${after}`);
    });

    await run("mobile: swipe left reveals delete, tap closes it, delete works like the desktop tab close", async () => {
      const mp = await mobilePage();
      const activeSel = (id) => `[data-quicknav-active-list] .quicknav-active-item[data-tab-id="${id}"]`;
      const rowSel = (id) => `[data-quicknav-active-list] .quicknav-swipe-row:has(.quicknav-active-item[data-tab-id="${id}"])`;
      await mp.goto(dragUrls[0], { waitUntil: "domcontentloaded" });
      await mp.click(".quicknav-toggle");
      await mp.waitForSelector("[data-quicknav-tabs]", { state: "visible", timeout: 6000 });
      await mp.locator('[data-quicknav-tab="active"]:visible').first().click();
      await mp.waitForSelector(activeSel(dragIds[0]), { state: "visible", timeout: 6000 });
      await mp.$eval(".quicknav-menu.show", (m) => { m.scrollTop = m.scrollHeight; });
      await sleep(300);
      const swipeOpen = (id) => mp.evaluate(async (id) => {
        const item = document.querySelector(`[data-quicknav-active-list] .quicknav-active-item[data-tab-id="${id}"]`);
        const r = item.getBoundingClientRect();
        let x = r.left + r.width * 0.35;
        const y = r.top + r.height / 2;
        const ev = (type, opts) => item.dispatchEvent(new PointerEvent(type, Object.assign({ bubbles: true, composed: true, pointerId: 31, pointerType: "touch", isPrimary: true, button: 0, buttons: 1, clientX: x, clientY: y }, opts)));
        const tick = () => new Promise((res) => setTimeout(res, 16));
        ev("pointerdown", {}); await tick();
        for (let i = 0; i < 8; i++) { x -= 14; ev("pointermove", { clientX: x }); await tick(); }
        ev("pointerup", { buttons: 0, clientX: x });
      }, id);
      await swipeOpen(dragIds[0]);
      await sleep(400);
      assert(await mp.$eval(rowSel(dragIds[0]), (r) => r.classList.contains("quicknav-swipe-open")), "swipe did not reveal the delete");
      assert(await mp.$eval(`${rowSel(dragIds[0])} [data-qn-delete]`, (b) => getComputedStyle(b).visibility === "visible"), "delete button not visible");
      // A tap on the row while revealed only closes the reveal, no navigation.
      // Click the row center, the left edge is clipped away while translated.
      await mp.click(activeSel(dragIds[0]));
      await sleep(400);
      assert(!(await mp.$eval(rowSel(dragIds[0]), (r) => r.classList.contains("quicknav-swipe-open"))), "tap did not close the reveal");
      assert(mp.url().includes(dragIds[0]), `tap on the revealed row navigated: ${mp.url()}`);
      // Swipe again and delete: same confirm flow as the desktop tab close, and
      // deleting the current terminal navigates to its neighbor (right, else left),
      // computed from the live list order because earlier runs reorder it.
      const order = await mp.$$eval("[data-quicknav-active-list] .quicknav-active-item", (els) => els.map((e) => e.dataset.tabId));
      const at = order.indexOf(dragIds[0]);
      const neighborId = order[at + 1] || order[at - 1];
      assert(neighborId, "no neighbor row found");
      await swipeOpen(dragIds[0]);
      await sleep(400);
      await mp.click(`${rowSel(dragIds[0])} [data-qn-delete]`);
      await L.confirmSwal(mp);
      await mp.waitForURL(new RegExp(neighborId), { timeout: 8000 });
      await mp.click(".quicknav-toggle");
      await mp.waitForSelector("[data-quicknav-active-list]", { state: "visible", timeout: 6000 });
      await sleep(600);
      assert(!(await mp.$(activeSel(dragIds[0]))), "deleted shell still listed in the quick nav");
    });

    await run("mobile: swipe rename on an active tab shell row", async () => {
      const mp = await mobilePage();
      const id = dragIds[1];
      const itemSel = `[data-quicknav-active-list] .quicknav-active-item[data-tab-id="${id}"]`;
      const rowSel = `[data-quicknav-active-list] .quicknav-swipe-row:has(.quicknav-active-item[data-tab-id="${id}"])`;
      const newName = `qnren-${tag.slice(-4)}`;
      await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await mp.click(".quicknav-toggle");
      await mp.waitForSelector("[data-quicknav-tabs]", { state: "visible", timeout: 6000 });
      await mp.locator('[data-quicknav-tab="active"]:visible').first().click();
      await mp.waitForSelector(itemSel, { state: "visible", timeout: 6000 });
      await sleep(300);
      await swipeRow(mp, itemSel);
      await sleep(400);
      assert(await mp.$eval(`${rowSel} [data-qn-rename]`, (b) => getComputedStyle(b).visibility === "visible"), "rename button not revealed");
      await mp.click(`${rowSel} [data-qn-rename]`);
      await fillPrompt(mp, newName);
      let renamed = false;
      for (let i = 0; i < 15; i++) { await sleep(600); if (((await mp.$eval(itemSel, (el) => el.textContent).catch(() => "")) || "").includes(newName)) { renamed = true; break; } }
      assert(renamed, "active tab shell row does not show the new name");
    });

    await run("mobile: projects tab shell row swipes rename and delete", async () => {
      const mp = await mobilePage();
      const shellUrl = await L.createShell(page, project);
      shellUrls.push(shellUrl);
      const id = new URL(shellUrl).pathname.split("/").pop();
      const itemSel = `[data-pb-detail="${project}"] .quicknav-active-item[data-tab-id="${id}"]`;
      const rowSel = `[data-pb-detail="${project}"] .quicknav-swipe-row:has(.quicknav-active-item[data-tab-id="${id}"])`;
      const newName = `qnpr-${tag.slice(-4)}`;
      await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await mp.click(".quicknav-toggle");
      await mp.waitForSelector("[data-quicknav-tabs]", { state: "visible", timeout: 6000 });
      await mp.locator('[data-quicknav-tab="projects"]:visible').first().click(); await sleep(400);
      await mp.locator(`[data-pb-drill="${project}"]`).first().click();
      await mp.waitForSelector(itemSel, { state: "visible", timeout: 6000 });
      await swipeRow(mp, itemSel);
      await sleep(400);
      assert(await mp.$eval(`${rowSel} [data-qn-rename]`, (b) => getComputedStyle(b).visibility === "visible"), "projects tab rename not revealed");
      assert(await mp.$eval(`${rowSel} [data-qn-delete]`, (b) => getComputedStyle(b).visibility === "visible"), "projects tab delete not revealed");
      await mp.click(`${rowSel} [data-qn-rename]`);
      await fillPrompt(mp, newName);
      let renamed = false;
      for (let i = 0; i < 15; i++) { await sleep(600); if (((await mp.$eval(itemSel, (el) => el.textContent).catch(() => "")) || "").includes(newName)) { renamed = true; break; } }
      assert(renamed, "projects tab shell row does not show the new name");
      assert(await mp.$eval(`[data-pb-detail="${project}"]`, (el) => !el.hidden), "drilled project view lost after the rename refresh");
      await swipeRow(mp, itemSel);
      await sleep(400);
      await mp.click(`${rowSel} [data-qn-delete]`);
      await L.confirmSwal(mp);
      let gone = false;
      for (let i = 0; i < 15; i++) { await sleep(600); if (!(await mp.$(itemSel))) { gone = true; break; } }
      assert(gone, "projects tab shell still listed after the swipe delete");
    });

    await run("mobile: projects tab coder rows swipe: stop the active, delete the inactive", async () => {
      const mp = await mobilePage();
      const name = `qncod-${tag.slice(-4)}`;
      await L.createSession(page, project, name);
      const coderRow = `[data-pb-detail="${project}"] .quicknav-swipe-row:has([data-tab-kind="coder"])`;
      const coderItem = `${coderRow} .quicknav-active-item`;
      const inactiveRow = `[data-pb-detail="${project}"] .quicknav-swipe-row:has([data-tab-kind="inactive"])`;
      const inactiveItem = `${inactiveRow} .quicknav-active-item`;
      await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await mp.click(".quicknav-toggle");
      await mp.waitForSelector("[data-quicknav-tabs]", { state: "visible", timeout: 6000 });
      await mp.locator('[data-quicknav-tab="projects"]:visible').first().click(); await sleep(400);
      await mp.locator(`[data-pb-drill="${project}"]`).first().click();
      await mp.waitForSelector(coderItem, { state: "visible", timeout: 10000 });
      await swipeRow(mp, coderItem);
      await sleep(400);
      assert(await mp.$eval(`${coderRow} [data-qn-delete]`, (b) => getComputedStyle(b).visibility === "visible"), "coder stop action not revealed");
      await mp.click(`${coderRow} [data-qn-delete]`);
      await L.confirmSwal(mp);
      let inactive = false;
      for (let i = 0; i < 30; i++) { await sleep(700); if (await mp.$(inactiveItem)) { inactive = true; break; } }
      assert(inactive, "stopped coder did not appear as inactive in the projects tab");
      assert(!(await mp.$(coderItem)), "stopped coder still listed as active");
      await swipeRow(mp, inactiveItem);
      await sleep(400);
      await mp.click(`${inactiveRow} [data-qn-delete]`);
      await L.confirmSwal(mp);
      let gone = false;
      for (let i = 0; i < 15; i++) { await sleep(700); if (!(await mp.$(inactiveItem))) { gone = true; break; } }
      assert(gone, "inactive coder still listed after the swipe delete");
    });

    await run("mobile: the coder row's second swipe action deletes the running coder outright", async () => {
      const mp = await mobilePage();
      const name = `qnpur-${tag.slice(-4)}`;
      await L.createSession(page, project, name);
      const coderRow = `[data-pb-detail="${project}"] .quicknav-swipe-row:has([data-tab-kind="coder"])`;
      const coderItem = `${coderRow} .quicknav-active-item`;
      const inactiveItem = `[data-pb-detail="${project}"] .quicknav-swipe-row:has([data-tab-kind="inactive"]) .quicknav-active-item`;
      await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await mp.click(".quicknav-toggle");
      await mp.waitForSelector("[data-quicknav-tabs]", { state: "visible", timeout: 6000 });
      await mp.locator('[data-quicknav-tab="projects"]:visible').first().click(); await sleep(400);
      await mp.locator(`[data-pb-drill="${project}"]`).first().click();
      await mp.waitForSelector(coderItem, { state: "visible", timeout: 10000 });
      await swipeRow(mp, coderItem);
      await sleep(400);
      assert(await mp.$eval(`${coderRow} [data-qn-purge]`, (b) => getComputedStyle(b).visibility === "visible"), "coder delete action not revealed");
      await mp.click(`${coderRow} [data-qn-purge]`);
      await L.confirmSwal(mp);
      let gone = false;
      for (let i = 0; i < 30; i++) { await sleep(700); if (!(await mp.$(coderItem))) { gone = true; break; } }
      assert(gone, "deleted coder still listed as active");
      assert(!(await mp.$(inactiveItem)), "the deleted coder came back as an inactive row");
    });

    await run("mobile: quicknav coder create from the editor lands on the coder page", async () => {
      const mp = await mobilePage();
      const name = `qnret-${tag.slice(-4)}`;
      await mp.goto(`${BASE}/projects/${encodeURIComponent(project)}/editor`, { waitUntil: "domcontentloaded" });
      await mp.click(".quicknav-toggle");
      await mp.waitForSelector("[data-quicknav-tabs]", { state: "visible", timeout: 6000 });
      const newCoder = mp.locator('.quicknav-context a[href^="/coders/new"]').first();
      await newCoder.waitFor({ state: "visible", timeout: 6000 });
      // The entry opens the create dialog (see coders.js), the editor page
      // stays under it, and the create lands on the coder's own page.
      await newCoder.click();
      await mp.waitForSelector("[data-form-modal].show form", { timeout: 10000 });
      const f = mp.locator('[data-form-modal] form:has(select[name="agent"])').first();
      await f.locator('input[name="name"]').fill(name);
      await Promise.all([mp.waitForURL(/\/coders\/(?!new)[^/]+$/, { timeout: 30000 }), f.locator('button[type="submit"]').first().click()]);
      retCoderPath = new URL(mp.url()).pathname;
    });

    await run("context bar reflects the current project on a scoped page", async () => {
      await page.goto(`${BASE}/projects/${encodeURIComponent(project)}/editor`, { waitUntil: "domcontentloaded" });
      await page.click(".quicknav-toggle");
      await page.waitForSelector("[data-quicknav-tabs]", { timeout: 6000 });
      let cur = null; for (let i = 0; i < 10; i++) { cur = await page.$eval("[data-quicknav-tabs]", (e) => e.getAttribute("data-quicknav-current-project")).catch(() => null); if (cur === project) break; await sleep(300); }
      assert(cur === project, `current-project='${cur}'`);
    });

    await run("a group over 5 entries folds with 'Show N more'", async () => {
      for (let i = 0; i < 6; i++) shellUrls.push(await L.createShell(page, project));
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await page.click(".quicknav-toggle");
      await page.waitForSelector("[data-quicknav-tabs]", { state: "visible", timeout: 6000 });
      await page.locator('[data-quicknav-tab="projects"]:visible').first().click(); await sleep(500);
      await page.locator(`[data-pb-drill="${project}"]`).first().click();
      await page.waitForSelector(`[data-pb-detail="${project}"]`, { state: "visible", timeout: 6000 });
      const fold = page.locator(`[data-pb-detail="${project}"] [data-qn-fold-toggle]`).first();
      await fold.waitFor({ state: "visible", timeout: 6000 });
      const before = await page.locator(`[data-pb-detail="${project}"] [data-qn-fold] > *`).count();
      await fold.click(); await sleep(400);
      assert(await page.locator(`[data-pb-detail="${project}"] [data-qn-fold] > *`).count() >= before, "fold did not expand");
    });

    await run("projects tab detail is one merged list in projects-page chip order, its actions one icon row", async () => {
      const detail = `[data-pb-detail="${project}"]`;
      assert((await page.$$(`${detail} h6.dropdown-header`)).length === 0, "per-kind headlines still present");
      assert((await page.$$(`${detail} [data-qn-fold]`)).length === 1, "expected one merged terminal group");
      const navIds = await page.$$eval(`${detail} [data-qn-fold] [data-tab-id]`, (els) => els.map((el) => el.getAttribute("data-tab-id")));
      const chipIds = await page.$$eval(`#project-${project} [data-chip] [data-notify-target]`, (els) => els.map((el) => el.getAttribute("data-notify-target")));
      assert(navIds.length > 0 && navIds.join() === chipIds.join(), `detail order ${navIds.join()} != chip order ${chipIds.join()}`);
      // The three project actions: one row, icons only, and nothing of them left
      // in the list below.
      const acts = await page.evaluate((sel) => {
        const bar = document.querySelector(`${sel} [data-pb-actions]`);
        if (!bar) return null;
        const items = [...bar.querySelectorAll("a, button")];
        const tops = items.map((e) => Math.round(e.getBoundingClientRect().top));
        return {
          count: items.length,
          oneLine: tops.every((t) => t === tops[0]),
          labels: items.map((e) => e.textContent.trim()),
          titles: items.map((e) => e.getAttribute("title")),
          hrefs: items.map((e) => e.getAttribute("href") || e.closest("form")?.getAttribute("action") || ""),
          icons: items.map((e) => [...e.querySelectorAll("i.ti")].map((i) => [...i.classList].find((c) => c.startsWith("ti-"))).join("+")),
          heights: items.map((e) => Math.round(e.getBoundingClientRect().height)),
          git: bar.querySelectorAll("[data-git-project-menu]").length,
          docker: bar.querySelectorAll("[data-docker-project-menu]").length,
          belowTitle: bar.getBoundingClientRect().top >= document.querySelector(`${sel} [data-pb-back]`).getBoundingClientRect().bottom - 0.5,
          rightAligned: (() => {
            const title = document.querySelector(`${sel} [data-pb-back]`).getBoundingClientRect();
            const last = items[items.length - 1].getBoundingClientRect();
            const first = items[0].getBoundingClientRect();
            return last.right <= title.right + 1 && last.right >= title.right - 20 && first.left > title.left + 40;
          })(),
        };
      }, detail);
      assert(acts, "the detail has no action row");
      // A plain directory has no repository and no containers, so it carries
      // the three that always stand.
      assert(acts.count === 3, `expected three actions on a plain directory, got ${acts.count}`);
      assert(acts.docker === 0, "a project without containers carries a compose button");
      assert(acts.oneLine, `the actions are not on one line: ${acts.titles.join(", ")}`);
      assert(acts.labels.every((t) => t === ""), `the actions carry text, not icons only: ${acts.labels.join("|")}`);
      assert(acts.belowTitle, "the action row does not sit under the title");
      // Icons only must not mean a target a finger misses.
      assert(acts.heights.every((h) => h >= 28), `an action is too small to tap: ${acts.heights.join()}`);
      // The row is right aligned: it ends where the rows below end, and starts
      // well past their left edge.
      assert(acts.rightAligned, "the action row is not flushed to the right edge");
      assert(acts.icons.join() === "ti-code,ti-robot+ti-plus,ti-terminal-2+ti-plus", `unexpected icons: ${acts.icons.join()}`);
      assert(acts.hrefs[0].includes("/editor") && acts.hrefs[1].includes("/coders/new") && acts.hrefs[2] === "/shells/new", `unexpected targets: ${acts.hrefs.join()}`);
      acts.titles.forEach((t) => assert(t && t.includes(project), `an action names no project: ${t}`));
      const items = await page.$$eval(`${detail} .dropdown-item`, (els) => els.map((el) => el.textContent.trim()));
      assert(!items.includes("New coder") && !items.includes("New shell") && !items.includes("Editor"), `the old rows are still in the list: ${items.join("|")}`);
    });

    // The order of the row is the projects page's: docker, git, editor, new
    // coder, new shell. This project carries no stack, so the docker half of
    // that rule is where the stack is, in docker.js.
    await run("a repository grows the git action, left of the editor, opening the projects page's own menu", async () => {
      // The /git route is the runner's way to make a real repository out of the
      // scratch directory, the same one the projects runner seeds with.
      const path = await L.projectPath(page, project);
      const git = async (args) => {
        const res = await page.evaluate(async ({ cwd, args }) => {
          const token = document.querySelector('meta[name="csrf-token"]')?.content || "";
          const r = await fetch("/git", { method: "POST", headers: { "X-CSRF-Token": token, "Content-Type": "application/json" }, body: JSON.stringify({ cwd, args }) });
          if (!r.ok) return { failed: `${r.status}` };
          return { code: (await r.json()).exitCode };
        }, { cwd: path, args });
        if (res.failed || res.code !== 0) throw new Error(`git ${args.join(" ")} failed: ${res.failed || res.code}`);
      };
      await git(["init", "-q", "-b", "master"]);
      await git(["config", "user.email", "e2e@example.com"]);
      await git(["config", "user.name", "e2e"]);
      await git(["config", "commit.gpgsign", "false"]);
      await git(["commit", "-q", "--allow-empty", "-m", "init"]);

      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await page.click(".quicknav-toggle");
      await page.waitForSelector("[data-quicknav-tabs]", { state: "visible", timeout: 8000 });
      await page.locator('[data-quicknav-tab="projects"]:visible').first().click();
      await page.waitForSelector("[data-pb-rows] [data-pb-drill]", { state: "visible", timeout: 8000 });
      await page.locator(`[data-pb-rows] [data-pb-drill="${project}"]`).first().click();
      await page.locator(`[data-pb-detail="${project}"]`).first().waitFor({ state: "visible", timeout: 8000 });
      await sleep(300);
      const row = await page.evaluate((name) => {
        const bar = document.querySelector(`[data-pb-detail="${name}"] [data-pb-actions]`);
        const items = [...bar.querySelectorAll("a, button")];
        const gitBtn = bar.querySelector("[data-git-project-menu]");
        return {
          count: items.length,
          firstIsGit: items[0] === gitBtn,
          afterDocker: !bar.querySelector("[data-docker-project-menu]")
            || items.indexOf(bar.querySelector("[data-docker-project-menu]")) < items.indexOf(gitBtn),
          beforeEditor: items.indexOf(gitBtn) < items.findIndex((e) => (e.getAttribute("href") || "").includes("/editor")),
          fetch: gitBtn?.dataset.gitFetch,
          commit: gitBtn?.dataset.gitCommit,
          compare: gitBtn?.dataset.gitCompare,
          worktree: Boolean(gitBtn?.dataset.gitWorktree),
        };
      }, project);
      assert(row.count === 4, `the repository's row has ${row.count} actions, expected four`);
      assert(row.firstIsGit && row.beforeEditor, "the git action does not stand left of the editor");
      assert(row.afterDocker, "the git action does not stand right of the compose action");
      assert(row.fetch === `/projects/${project}/fetch`, `git fetch target is ${row.fetch}`);
      assert(row.commit?.includes("view=commit") && row.compare?.includes("view=compare"), "the git action misses the editor views");
      assert(row.worktree, "a main repository offers no worktree entry");

      await page.locator(`[data-pb-detail="${project}"] [data-git-project-menu]`).click();
      await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 6000 });
      const labels = await page.$$eval(".dc-context-menu .dropdown-item", (els) => els.map((e) => e.textContent.trim()));
      assert(
        JSON.stringify(labels) === JSON.stringify(["New worktree", "Fetch", "Commit changes", "Compare revisions"]),
        `the quick nav's git menu reads ${JSON.stringify(labels)}`,
      );
      await page.keyboard.press("Escape");
    });
  } finally {
    if (retCoderPath) {
      await L.stopSession(page, `${BASE}${retCoderPath}`).catch(() => {});
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" }).catch(() => {});
      const d = page.locator(`#project-${project} form[action^="/coders/"][action$="/delete"]`).first();
      if (await d.count().catch(() => 0)) {
        await d.locator("button").first().click().catch(() => {});
        await L.confirmSwal(page).catch(() => {});
        await sleep(500);
      }
    }
    for (const u of shellUrls) await L.deleteShell(page, u).catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
  }
});
