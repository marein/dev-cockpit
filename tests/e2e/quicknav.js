const L = require("./lib");
const { assert, sleep, BASE } = L;

// Quick nav: the floating menu present on every page. Custom element dc-quicknav
// (Bootstrap dropdown). The toggle opens it and lazy loads /quicknav into
// [data-quicknav-tabs]; two segments Active and Projects; [data-pb-drill] opens a
// project detail, [data-pb-back] returns; a project scoped page sets
// data-quicknav-current-project; a group over 5 entries folds with
// [data-qn-fold-toggle]; the project order is shared with the projects page.
// The Active pane is one flat list of every live coder and shell, no per-kind
// grouping, sorted and drag reorderable through the same cross-device @dc_tab_pos
// state the attach page tab strip uses (POST /terminal-tabs/order); the New coder
// and New shell buttons sit at the end of that list. On touch a swipe left on a
// row reveals a delete action (row wrapper .quicknav-swipe-row, button
// [data-qn-delete]) that behaves exactly like the desktop tab strip's close
// control: confirm dialog, POST /coders/:id/stop or /shells/:id/delete, and
// deleting the terminal you are attached to navigates to its neighbor. Shell
// rows also reveal a rename action ([data-qn-rename], prompt dialog, POST
// /shells/:id/rename). The Projects tab detail mirrors the projects page chip
// row: no per-kind headlines, one merged list of the live coders and shells in
// tab strip order, inactive coders after them, New coder and New shell at the
// end. Detail rows swipe too, reorder stays active-list only: active coders
// reveal stop, inactive coders reveal delete (POST /coders/:id/delete,
// projects page confirm wording), shells reveal rename plus delete.

L.runFeature("QUICKNAV", async ({ page, run, mobilePage }) => {
  const tag = `qn-${Date.now().toString(36)}`;
  const project = `zztc-${tag}`;
  const shellUrls = [];
  let dragIds = [];
  let dragUrls = [];
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

    await run("projects tab detail is one merged list in projects-page chip order, new buttons last", async () => {
      const detail = `[data-pb-detail="${project}"]`;
      assert((await page.$$(`${detail} h6.dropdown-header`)).length === 0, "per-kind headlines still present");
      assert((await page.$$(`${detail} [data-qn-fold]`)).length === 1, "expected one merged terminal group");
      const navIds = await page.$$eval(`${detail} [data-qn-fold] [data-tab-id]`, (els) => els.map((el) => el.getAttribute("data-tab-id")));
      const chipIds = await page.$$eval(`#project-${project} [data-chip] [data-notify-target]`, (els) => els.map((el) => el.getAttribute("data-notify-target")));
      assert(navIds.length > 0 && navIds.join() === chipIds.join(), `detail order ${navIds.join()} != chip order ${chipIds.join()}`);
      const tail = await page.$$eval(`${detail} .dropdown-item`, (els) => els.slice(-2).map((el) => el.textContent.trim()));
      assert(tail[0] === "New coder" && tail[1] === "New shell", `expected New coder/New shell last, got ${tail.join(", ")}`);
    });
  } finally {
    for (const u of shellUrls) await L.deleteShell(page, u).catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
  }
});
