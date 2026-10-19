const L = require("./lib");
const { assert, sleep, confirmSwal } = L;

// The phone's sheet as the strip of split.js, run by it on its split and
// shells with its helpers: a split carried over the list lies above every
// row with all its member rows, and the sheet opens centered on the focused
// pane, drags a split as one with the list scrolling under a finger held at
// its edge, lands the tap that follows the drag and stops a member from its
// own menu. Both drags are touch through CDP, so WebKit skips them.

module.exports = async function splitSheet({ run, engine, mobilePage, page, project, shellUrls, ids, gid, groupTabSel, steadyBox, postIds, groupVia }) {
  await run("mobile: a carried split view lies above everything with all its rows", async () => {
    if (engine !== "chromium") return "skipped, the touch drag goes through CDP, which is chromium only";
    const mp = await mobilePage();
    const [extraUrl, tailUrl] = [await L.createShell(page, project), await L.createShell(page, project)];
    shellUrls.push(extraUrl, tailUrl);
    const [extraId, tailId] = [extraUrl, tailUrl].map((u) => new URL(u).pathname.split("/").pop());
    // The unit hangs a row below its target: a row of this runner's, not the sheet's clipped edge.
    assert(await postIds("/terminal-tabs/order", [extraId, tailId]) === 204, "the strip order was refused");
    await mp.goto(`${L.BASE}/shells/${extraId}`, { waitUntil: "domcontentloaded" });
    await mp.waitForSelector(`terminal-attach[terminal-id="${extraId}"] .xterm-screen canvas`, { timeout: 15000 });
    await mp.tap('.dc-tabbar button[data-ctx-area="terminals"]');
    await mp.waitForSelector(`dc-ctx-sheet:not([hidden]) .terminal-tab-split[data-tab-id="${gid}"]`, { state: "visible", timeout: 8000 });
    await sleep(900);
    const cdp = await mp.context().newCDPSession(mp);
    const grip = await steadyBox(mp, `dc-ctx-sheet .terminal-tab-split[data-tab-id="${gid}"] [data-tab-grip]`);
    const target = await steadyBox(mp, `dc-ctx-sheet .terminal-tab[data-tab-id="${extraId}"]`);
    const start = { x: grip.x + grip.width / 2, y: grip.y + grip.height / 2 };
    const unit = await mp.$eval(`dc-ctx-sheet .terminal-tab-split[data-tab-id="${gid}"]`, (row) => {
      const rows = [row, ...row.parentElement.querySelectorAll(`.terminal-tab-member[data-tab-group="${row.dataset.tabId}"]`)];
      return rows.reduce((sum, r) => sum + r.getBoundingClientRect().height, 0);
    });
    const down = target.y > start.y;
    const end = { x: start.x, y: down ? target.y + target.height / 2 + 4 - (unit - grip.height) / 2 : target.y + target.height / 2 + 4 };
    await cdp.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ x: start.x, y: start.y, id: 1 }] });
    for (let i = 1; i <= 10; i += 1) {
      await cdp.send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ x: start.x, y: Math.round(start.y + (end.y - start.y) * (i / 10)), id: 1 }] });
      await sleep(30);
    }
    await sleep(450);
    const state = await mp.evaluate((g) => {
      const rows = [...document.querySelectorAll("dc-ctx-sheet .terminal-tab-dragging")];
      const top = Math.max(...[...document.querySelectorAll("*")].map((e) => parseInt(getComputedStyle(e).zIndex, 10) || 0));
      const target = document.querySelector("dc-ctx-sheet .terminal-tab-group-target");
      return {
        rows: rows.map((row) => {
          const r = row.getBoundingClientRect();
          const hit = document.elementFromPoint(r.left + r.width * 0.4, r.top + r.height / 2);
          return { id: row.dataset.tabId.slice(0, 8), z: parseInt(getComputedStyle(row).zIndex, 10) || 0, covered: !row.contains(hit) };
        }),
        top,
        members: rows.filter((row) => row.dataset.tabGroup === g).length,
        targetFrame: target ? getComputedStyle(target).boxShadow : "",
      };
    }, gid);
    for (let i = 10; i >= 0; i -= 1) {
      await cdp.send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ x: start.x, y: Math.round(start.y + (end.y - start.y) * (i / 10)), id: 1 }] });
      await sleep(30);
    }
    await sleep(200);
    await cdp.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
    await sleep(600);
    assert(state.rows.length === 3 && state.members === 2, `the carried unit is not the split with its two members: ${JSON.stringify(state.rows)}`);
    for (const row of state.rows) {
      assert(!row.covered, `a carried row is covered: ${JSON.stringify(row)}`);
      assert(row.z >= state.top, `a carried row's z-index ${row.z} is below the page's highest ${state.top}`);
    }
    assert(/inset/.test(state.targetFrame) && /0px 0px 0px 2px/.test(state.targetFrame), `the row under the unit wears no frame: ${state.targetFrame}`);
    await mp.keyboard.press("Escape").catch(() => {});
    await mp.goto(`${L.BASE}/projects`, { waitUntil: "domcontentloaded" });
    for (const url of [extraUrl, tailUrl]) await L.deleteShell(page, url).then(() => shellUrls.splice(shellUrls.indexOf(url), 1));
    return `${state.rows.length} rows at z ${state.rows[0].z} over ${state.top}`;
  });

  // The sheet is the phone's strip: it opens on the focused pane, a split
  // travels with its member rows, a finger held at the edge scrolls the list,
  // the tap after a drag is a tap, and a member's own menu ends it.
  await run("mobile: the sheet centers the focused pane, drags a split as one, the next tap lands, a member stops from its menu", async () => {
    if (engine !== "chromium") return "skipped, the touch drag goes through CDP, which is chromium only";
    const mp = await mobilePage();
    const extraUrls = [];
    for (let i = 0; i < 3; i += 1) extraUrls.push(await L.createShell(page, project));
    const pairUrls = [await L.createShell(page, project), await L.createShell(page, project)];
    for (let i = 0; i < 3; i += 1) extraUrls.push(await L.createShell(page, project));
    const pair = pairUrls.map((u) => new URL(u).pathname.split("/").pop());
    const extras = extraUrls.map((u) => new URL(u).pathname.split("/").pop());
    const group = await groupVia(pair);
    // Terminals nobody placed stand in the order they started, which tmux
    // keeps in whole seconds, so shells made in one second fall in no given
    // order. The strip is put the way the check reads it, the pair between
    // three shells on either side, the way a drag would put it.
    const ordered = await postIds("/terminal-tabs/order", [...extras.slice(0, 3), ...pair, ...extras.slice(3)]);
    assert(ordered === 204, `the strip order was refused: ${ordered}`);
    try {
      await mp.goto(`${L.BASE}/splits/${group.id}?focus=${pair[1]}`, { waitUntil: "domcontentloaded" });
      await mp.waitForSelector(`terminal-attach[terminal-id="${pair[1]}"] .xterm-screen canvas`, { timeout: 15000 });
      await mp.tap('.dc-tabbar button[data-ctx-area="terminals"]');
      const memberSel = `dc-ctx-sheet .terminal-tab-member[data-tab-id="${pair[1]}"]`;
      await mp.waitForSelector(`${memberSel}.active`, { state: "visible", timeout: 8000 });
      await sleep(600);
      const centered = await mp.evaluate((sel) => {
        const row = document.querySelector(sel);
        const body = row.closest(".dc-ctx-body");
        const r = row.getBoundingClientRect();
        const b = body.getBoundingClientRect();
        return { off: Math.round(Math.abs((r.top + r.height / 2) - (b.top + b.height / 2))), bodyH: Math.round(b.height), scroll: body.scrollTop, max: body.scrollHeight - body.clientHeight };
      }, memberSel);
      assert(centered.max > 0, `the list does not scroll: ${JSON.stringify(centered)}`);
      assert(centered.off < centered.bodyH * 0.25, `the focused pane is not centered in the sheet: ${JSON.stringify(centered)}`);

      const cdp = await mp.context().newCDPSession(mp);
      const grip = await steadyBox(mp, `dc-ctx-sheet .terminal-tab-split[data-tab-id="${group.id}"] [data-tab-grip]`);
      const start = { x: grip.x + grip.width / 2, y: grip.y + grip.height / 2 };
      const body = await mp.$eval("dc-ctx-sheet .dc-ctx-body", (el) => ({ bottom: el.getBoundingClientRect().bottom, scrollTop: el.scrollTop }));
      await cdp.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ x: start.x, y: start.y, id: 1 }] });
      const edge = body.bottom - 6;
      for (let i = 1; i <= 12; i += 1) {
        await cdp.send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ x: start.x, y: Math.round(start.y + (edge - start.y) * (i / 12)), id: 1 }] });
        await sleep(30);
      }
      await sleep(800);
      const scrolled = await mp.$eval("dc-ctx-sheet .dc-ctx-body", (el) => el.scrollTop);
      assert(scrolled > body.scrollTop, `a finger held at the bottom edge did not scroll the list: ${body.scrollTop} -> ${scrolled}`);
      await cdp.send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ x: start.x, y: Math.round(body.bottom + 30), id: 1 }] });
      await sleep(150);
      await cdp.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
      await sleep(600);
      const order = await mp.$$eval("dc-ctx-sheet [data-tabs-strip] > a", (rows) => rows.map((r) => r.dataset.tabId));
      assert(JSON.stringify(order.slice(-3)) === JSON.stringify([group.id, pair[0], pair[1]]), `the split did not move to the end as one: ${order.slice(-4)}`);

      await mp.tap(`dc-ctx-sheet .terminal-tab[data-tab-id="${extras[0]}"]`);
      await mp.waitForURL(new RegExp(`/shells/${extras[0]}`), { timeout: 8000 });
      assert(await mp.evaluate(() => document.querySelector("dc-ctx-sheet").hidden), "the sheet stayed open after the tap that followed the drag");

      await page.reload({ waitUntil: "domcontentloaded" });
      await page.waitForSelector(`terminal-tabs .terminal-tab[data-tab-id="${group.id}"]`, { state: "attached", timeout: 8000 });
      const persisted = await page.$$eval("terminal-tabs .terminal-tab", (els) => els.map((e) => e.dataset.tabId));
      assert(persisted[persisted.length - 1] === group.id, `the moved split is not last after a reload: ${persisted.slice(-3)}`);

      await mp.waitForSelector(`terminal-attach[terminal-id="${extras[0]}"] .xterm-screen canvas`, { timeout: 15000 });
      await mp.tap('.dc-tabbar button[data-ctx-area="terminals"]');
      await mp.waitForSelector(`dc-ctx-sheet:not([hidden]) ${memberSel.replace("dc-ctx-sheet ", "")}`, { state: "visible", timeout: 8000 });
      await sleep(400);
      await mp.tap(`${memberSel} [data-tab-menu]`);
      await mp.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
      await mp.locator(".dc-context-menu .dropdown-item", { hasText: /^Delete$/ }).click();
      await mp.waitForSelector(".swal2-confirm", { state: "visible", timeout: 5000 });
      await confirmSwal(mp);
      await mp.waitForSelector(memberSel, { state: "detached", timeout: 10000 });
      await mp.keyboard.press("Escape").catch(() => {});
      await page.reload({ waitUntil: "domcontentloaded" });
      await sleep(800);
      assert(!(await page.$(`terminal-tabs [data-tab-id="${pair[1]}"]`)), "the deleted member is still in the strip");
    } finally {
      await mp.goto(`${L.BASE}/projects`, { waitUntil: "domcontentloaded" }).catch(() => {});
      for (const u of [...pairUrls, ...extraUrls]) await L.deleteShell(page, u).catch(() => {});
      await page.goto(shellUrls[0], { waitUntil: "domcontentloaded" });
      await page.waitForURL(new RegExp(`/splits/${gid}\\?focus=${ids[0]}`), { timeout: 8000 });
      await page.waitForSelector(`terminal-attach[terminal-id="${ids[1]}"] .xterm-screen canvas`, { timeout: 15000 });
      await page.waitForSelector(`${groupTabSel}.active`, { state: "attached", timeout: 8000 });
    }
  });
};
