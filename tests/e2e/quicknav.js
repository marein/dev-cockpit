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
// and New shell buttons sit at the end of that list.

L.runFeature("QUICKNAV", async ({ page, run, mobilePage }) => {
  const tag = `qn-${Date.now().toString(36)}`;
  const project = `zztc-${tag}`;
  const shellUrls = [];
  let dragIds = [];
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
      const dragUrls = [];
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
  } finally {
    for (const u of shellUrls) await L.deleteShell(page, u).catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
  }
});
