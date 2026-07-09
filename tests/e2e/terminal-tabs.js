const L = require("./lib");
const { assert, sleep, BASE } = L;

// Terminal tabs: the desktop tab strip attached to the terminal on the attach
// pages (custom element terminal-tabs, partial terminal_tabs.gohtml). Every live
// coder and shell is a tab, ordered oldest first server side, then reordered per
// device from localStorage dc-terminal-tab-order; a new session always lands on
// the right. Tabs reorder by pointer drag. The switcher popup opens via Ctrl+Tab
// (browsers often reserve it, so headless and PWAs only) and via double tapping
// Ctrl or Meta within 400ms (collision free: bare modifiers never reach the
// shell, a chord like Ctrl+C cancels the tap); cycle via Tab, Ctrl+Tab or
// arrows, Enter or click switches, Esc closes, typing filters the list by name
// and project through the search input, and it all
// works while the xterm terminal has focus. Every tab and every switcher row
// carries a close control (confirm dialog, then coder stop or shell delete;
// closing the current session switches to the right neighbor tab like
// Terminal.app, left as fallback, projects page when the strip is empty; the
// page header stop button keeps its own redirect+flash flow). The + button at the strip's end
// opens New coder / New shell with the current project preselected. On coarse
// pointer clients the strip stays hidden, the quick nav covers mobile. The strip
// lists every live session on the host, so all interactions here MUST stay on
// tabs of sessions this runner created (scoped via data-tab-id).

L.runFeature("TERMINAL-TABS", async ({ page, run, mobilePage }) => {
  const tag = `tt-${Date.now().toString(36)}`;
  const project = `zztc-${tag}`;
  const shellUrls = [];
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
      await sleep(300);
      let ownOrder = (await tabOrder()).filter((id) => ids.includes(id));
      assert(JSON.stringify(ownOrder) === JSON.stringify([ids[2], ids[0], ids[1]]), `order after drag: ${ownOrder}`);
      const stored = await page.evaluate(() => JSON.parse(localStorage.getItem("dc-terminal-tab-order") || "[]"));
      assert(stored.indexOf(ids[2]) < stored.indexOf(ids[0]), "dragged order not persisted");
      await page.reload({ waitUntil: "domcontentloaded" });
      await page.waitForSelector(tabSel(ids[2]), { state: "attached", timeout: 8000 });
      await sleep(500);
      ownOrder = (await tabOrder()).filter((id) => ids.includes(id));
      assert(JSON.stringify(ownOrder) === JSON.stringify([ids[2], ids[0], ids[1]]), `order after reload: ${ownOrder}`);
    });

    await run("a drag does not trigger the tab's navigation", async () => {
      assert(page.url().includes(ids[0]), `unexpected page ${page.url()}`);
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

    await run("the + menu links to new coder and new shell with the current project preselected", async () => {
      await page.click(".terminal-tabs-new-btn");
      await page.waitForSelector("terminal-tabs .dropdown-menu.show", { state: "visible", timeout: 4000 });
      const hrefs = await page.$$eval("terminal-tabs .dropdown-menu.show a", (as) => as.map((a) => a.getAttribute("href")));
      assert(hrefs.length === 2, `expected 2 create links, got ${hrefs.length}`);
      assert(hrefs[0].startsWith("/coders/new?") && hrefs[0].includes(`project=${project}`), `coder link ${hrefs[0]}`);
      assert(hrefs[1].startsWith("/shells/new?") && hrefs[1].includes(`project=${project}`), `shell link ${hrefs[1]}`);
      assert(hrefs[1].includes(`return=%2Fshells%2F${ids[2]}`), `shell link return target ${hrefs[1]}`);
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

    await run("the row close button in the switcher deletes that shell after confirm", async () => {
      const target = ownId(shellUrls[3]);
      await page.keyboard.press("Control");
      await page.keyboard.press("Control");
      await page.waitForSelector(".terminal-switcher", { state: "visible", timeout: 4000 });
      await page.click(`.terminal-switcher-item[data-switcher-id="${target}"] .terminal-switcher-close`);
      await L.confirmSwal(page);
      await page.waitForSelector(`.terminal-switcher-item[data-switcher-id="${target}"]`, { state: "detached", timeout: 8000 });
      await page.waitForSelector(tabSel(target), { state: "detached", timeout: 8000 });
      assert((await page.locator(".terminal-switcher").count()) === 1, "switcher closed by the row close action");
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

    await run("the strip stays hidden on coarse pointer (mobile) clients", async () => {
      const mp = await mobilePage();
      await mp.goto(shellUrls[0], { waitUntil: "domcontentloaded" });
      await mp.waitForSelector("terminal-tabs", { state: "attached", timeout: 8000 });
      const display = await mp.$eval("terminal-tabs", (e) => getComputedStyle(e).display);
      assert(display === "none", `terminal-tabs display '${display}' on mobile`);
    });
  } finally {
    for (const u of shellUrls) await L.deleteShell(page, u).catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
  }
});
