const L = require("./lib");
const { assert, sleep, BASE } = L;

// Live updates over the shared server event stream. One EventSource per page to
// GET /events carries a {type,data} envelope under the SSE event
// name "dc"; the @dc/events client re-dispatches each as a dc:<type> DOM event.
// The server publishes a "terminals" event whenever the live coder/shell set or
// its order changes (create, stop, resume, delete, shell rename, reorder), and
// pushes a snapshot (notifications + terminals) on every connect. terminal-tabs
// and dc-quicknav subscribe and pull their own fresh fragment, so a change in one
// client shows up in every other client without a navigation.
//
// This runner drives two independent desktop clients against the same instance:
// client A stays on a shell attach page (desktop tab strip visible) while client
// B mutates sessions, and A must reflect it live. The tab strip is hidden on a
// coarse pointer, so both clients are desktop contexts.

function shellId(url) {
  return new URL(url).pathname.split("/").pop();
}

// postAs runs an authenticated POST from inside a page, threading the CSRF token
// the way @dc/http does, so it exercises the real server path a component would.
async function postAs(page, path, body, json) {
  return page.evaluate(async ({ path, body, json }) => {
    const token = document.querySelector('meta[name="csrf-token"]')?.content || "";
    const headers = { "X-CSRF-Token": token };
    let payload;
    if (json) {
      headers["Content-Type"] = "application/json";
      payload = JSON.stringify(body);
    } else {
      headers["Content-Type"] = "application/x-www-form-urlencoded";
      payload = new URLSearchParams(body).toString();
    }
    const res = await fetch(path, { method: "POST", headers, body: payload });
    return { ok: res.ok, status: res.status };
  }, { path, body, json });
}

L.runFeature("LIVE-UPDATES", async ({ engine, browser, page, run, bag }) => {
  const tag = `live-${Date.now().toString(36)}`;
  const project = `zzlive-${tag}`;
  const shells = [];
  let ctxB = null;
  let pageB = null;
  try {
    await L.createProject(page, project);

    // Client A parks on its own scratch shell; its desktop tab strip is the live
    // surface the other client's changes must reach.
    const shellA = await L.createShell(page, project);
    shells.push(shellA);
    const idA = shellId(shellA);
    assert((await L.waitUpgraded(page, ["terminal-tabs"], 8000)).length === 0, "terminal-tabs not upgraded");
    await page.waitForSelector(`.terminal-tab[data-tab-id="${idA}"]`, { timeout: 8000 });

    // A second, independent client on the same instance.
    ctxB = await L.newDesktop(browser, engine);
    pageB = await ctxB.newPage();
    L.wirePage(pageB, bag);
    await L.login(pageB);

    let idB = null;
    await run("a shell started in another client appears in the tab strip live", async () => {
      const shellB = await L.createShell(pageB, project);
      shells.push(shellB);
      idB = shellId(shellB);
      // A stays on shellA; the new tab must arrive over the stream, not a nav.
      await page.waitForSelector(`.terminal-tab[data-tab-id="${idB}"]`, { timeout: 8000 });
      assert(page.url().includes(idA), `client A navigated away: ${page.url()}`);
      return `A=${idA} B=${idB}`;
    });

    await run("renaming a shell in another client updates its tab label live", async () => {
      const name = `renamed-${tag.slice(-4)}`;
      const res = await postAs(pageB, `/shells/${idB}/rename`, { name });
      assert(res.ok, `rename POST failed: ${res.status}`);
      await page.waitForFunction(
        ({ id, name }) => {
          const tab = document.querySelector(`.terminal-tab[data-tab-id="${id}"] .terminal-tab-name`);
          return tab && tab.textContent.trim() === name;
        },
        { id: idB, name },
        { timeout: 8000 },
      );
    });

    await run("reordering in another client reorders the strip live", async () => {
      const order = async (p) =>
        p.$$eval(".terminal-tab", (els) => els.map((e) => e.dataset.tabId));
      const current = (await order(page)).filter((id) => id === idA || id === idB);
      assert(current.length === 2, `expected both scratch tabs, got ${JSON.stringify(current)}`);
      const swapped = [current[1], current[0]];
      // Post only this run's two ids; unrelated ids keep their position.
      const res = await postAs(pageB, "/terminal-tabs/order", { ids: swapped }, true);
      assert(res.ok, `order POST failed: ${res.status}`);
      await page.waitForFunction(
        (want) => {
          const ids = [...document.querySelectorAll(".terminal-tab")]
            .map((e) => e.dataset.tabId)
            .filter((id) => want.includes(id));
          return JSON.stringify(ids) === JSON.stringify(want);
        },
        swapped,
        { timeout: 8000 },
      );
    });

    await run("stopping a session in another client removes it from the strip live", async () => {
      await L.deleteShell(pageB, shells[1]);
      await page.waitForSelector(`.terminal-tab[data-tab-id="${idB}"]`, { state: "detached", timeout: 8000 });
      shells.splice(1, 1);
    });

    await run("an open quick nav refreshes when a session starts in another client", async () => {
      await page.click(".quicknav-toggle");
      await page.waitForSelector("[data-quicknav-active-list]", { state: "visible", timeout: 6000 });
      const shellC = await L.createShell(pageB, project);
      shells.push(shellC);
      const idC = shellId(shellC);
      await page.waitForSelector(
        `[data-quicknav-active-list] .quicknav-active-item[data-tab-id="${idC}"]`,
        { timeout: 8000 },
      );
      await page.keyboard.press("Escape");
    });

    await run("the projects page adds/removes sessions live and keeps unfold in other projects", async () => {
      // A second project with 6 shells so its shell list folds (>5).
      const other = `zzlive2-${tag}`;
      await L.createProject(pageB, other);
      const otherShells = [];
      for (let i = 0; i < 6; i += 1) otherShells.push(await L.createShell(pageB, other));
      shells.push(...otherShells);

      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      assert((await L.waitUpgraded(page, ["dc-project-list", "dc-collapse-list"], 8000)).length === 0, "elements not upgraded");
      // Unfold the other project's shells.
      const otherBody = `#project-${other}-shells [data-shells-body]`;
      const toggle = page.locator(`${otherBody} [data-collapse-toggle]`);
      await toggle.waitFor({ state: "visible", timeout: 6000 });
      await toggle.click();
      const visibleRows = async (sel) => page.locator(`${sel} .list-group-item:not([data-collapse-toggle])`).count();
      assert((await visibleRows(otherBody)) === 6, "unfold did not reveal all 6 shells");

      // Start a shell in the FIRST project from client B: appears live here...
      const sX = await L.createShell(pageB, project);
      shells.push(sX);
      const idX = shellId(sX);
      await page.waitForSelector(`#project-${project}-shells [data-notify-target="${idX}"]`, { timeout: 8000 });
      assert(page.url().endsWith("/projects"), `client A navigated away: ${page.url()}`);
      // ...and the other project's unfold survived the live update.
      assert((await visibleRows(otherBody)) === 6, "unfolded other project refolded on a live change elsewhere");

      // Delete it again from client B: row disappears live, unfold still intact.
      await L.deleteShell(pageB, sX);
      shells.splice(shells.indexOf(sX), 1);
      await page.waitForSelector(`#project-${project}-shells [data-notify-target="${idX}"]`, { state: "detached", timeout: 8000 });
      assert((await visibleRows(otherBody)) === 6, "unfold lost after a live delete elsewhere");

      for (const url of otherShells) await L.deleteShell(pageB, url).catch(() => {});
      await L.deleteProject(pageB, other).catch(() => {});
    });
  } finally {
    if (pageB) await pageB.close().catch(() => {});
    if (ctxB) await ctxB.close().catch(() => {});
    for (const url of shells) await L.deleteShell(page, url).catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
  }
});
