const L = require("./lib");
const { assert, sleep, BASE } = L;

// Live updates over the shared server event stream. One EventSource per page to
// GET /events carries a {type,data} envelope under the SSE event
// name "dc"; the @dc/events client re-dispatches each as a dc:<type> DOM event.
// The server publishes a "terminals" event whenever the live coder/shell set or
// its order changes (create, stop, resume, delete, shell rename, reorder), and
// pushes a snapshot (notifications + terminals) on every connect. terminal-tabs
// and the sheet subscribe and pull their own fresh fragment, so a change in one
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

    await run("an open terminals sheet refreshes when a session starts in another client", async () => {
      // The tab bar and its sheet stand below lg only, a wide screen has the
      // rail and the list columns instead.
      await page.setViewportSize({ width: 750, height: 900 });
      await page.click('.dc-tabbar button[data-ctx-area="terminals"]');
      await page.waitForSelector("dc-ctx-sheet:not([hidden]) .terminal-tabs-strip", { state: "visible", timeout: 6000 });
      const shellC = await L.createShell(pageB, project);
      shells.push(shellC);
      const idC = shellId(shellC);
      await page.waitForSelector(
        `dc-ctx-sheet .terminal-tab[data-tab-id="${idC}"]`,
        { timeout: 8000 },
      );
      await page.keyboard.press("Escape");
      await page.setViewportSize({ width: 1360, height: 900 });
    });

    await run("the projects page adds/removes sessions live and keeps unfold in other projects", async () => {
      // A second project with 9 shells so its chip list folds (>8).
      const other = `zzlive2-${tag}`;
      await L.createProject(pageB, other);
      const otherShells = [];
      for (let i = 0; i < 9; i += 1) otherShells.push(await L.createShell(pageB, other));
      shells.push(...otherShells);

      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      assert((await L.waitUpgraded(page, ["dc-project-list"], 8000)).length === 0, "elements not upgraded");
      // Unfold the other project's chips.
      const otherBody = `#project-${other} [data-sessions-body]`;
      const toggle = page.locator(`${otherBody} [data-chips-toggle]`);
      await toggle.waitFor({ state: "visible", timeout: 6000 });
      await toggle.click();
      const visibleChips = async (sel) => page.locator(`${sel} [data-chip]:not(.d-none)`).count();
      assert((await visibleChips(otherBody)) === 9, "unfold did not reveal all 9 shells");

      // Start a shell in the FIRST project from client B: appears live here...
      const sX = await L.createShell(pageB, project);
      shells.push(sX);
      const idX = shellId(sX);
      await page.waitForSelector(`#project-${project} [data-notify-target="${idX}"]`, { timeout: 8000 });
      assert(page.url().endsWith("/projects"), `client A navigated away: ${page.url()}`);
      // ...and the other project's unfold survived the live update.
      assert((await visibleChips(otherBody)) === 9, "unfolded other project refolded on a live change elsewhere");

      // Delete it again from client B: chip disappears live, unfold still intact.
      await L.deleteShell(pageB, sX);
      shells.splice(shells.indexOf(sX), 1);
      await page.waitForSelector(`#project-${project} [data-notify-target="${idX}"]`, { state: "detached", timeout: 8000 });
      assert((await visibleChips(otherBody)) === 9, "unfold lost after a live delete elsewhere");

      for (const url of otherShells) await L.deleteShell(pageB, url).catch(() => {});
      await L.deleteProject(pageB, other).catch(() => {});
    });

    await run("the projects page adds and removes projects live", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      assert((await L.waitUpgraded(page, ["dc-project-list"], 8000)).length === 0, "project list not upgraded");
      const remote = `zzlive-project-${tag}`;
      const create = await postAs(pageB, "/projects", { project_name: remote });
      assert(create.ok, `project create POST failed: ${create.status}`);
      await page.waitForSelector(`#project-${remote}`, { timeout: 8000 });
      assert(page.url().endsWith("/projects"), `client A navigated away: ${page.url()}`);

      const remove = await postAs(pageB, "/projects/delete", { project: remote });
      assert(remove.ok, `project delete POST failed: ${remove.status}`);
      await page.waitForSelector(`#project-${remote}`, { state: "detached", timeout: 8000 });
    });

    await run("a column refresh started before a navigation never paints the page it was left on", async () => {
      // The hazard in order: an event on the projects page starts the column's
      // own pull of /projects, the page navigates to a terminal while it is in
      // flight, and the answer lands afterwards. It describes the page that was
      // left, so painting it puts the project index next to the terminal. A
      // slow /projects is what the live host has and what makes the order
      // reproducible here.
      await page.unroute("**/projects").catch(() => {});
      let slow = false;
      await page.route("**/projects", async (route) => {
        if (route.request().method() !== "GET" || !slow) return route.continue();
        await new Promise((resolve) => setTimeout(resolve, 3000));
        return route.continue();
      });
      try {
        await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
        assert((await L.waitUpgraded(page, ["dc-project-list"], 8000)).length === 0, "project list not upgraded");
        await sleep(1200);
        const column = () => page.evaluate(() => {
          const ctx = document.querySelector(".dc-ctx");
          return {
            title: ctx?.querySelector(".dc-ctx-title")?.textContent.trim() || "",
            list: ctx?.querySelector("[data-ctx-list]")?.getAttribute("data-ctx-list") || "",
            projectRows: ctx?.querySelectorAll("[data-project-index] a").length || 0,
            tabs: ctx?.querySelectorAll(".terminal-tab").length || 0,
          };
        });
        const start = await column();
        assert(start.title === "Projects" && start.list === "projects", `the projects column did not render: ${JSON.stringify(start)}`);
        slow = true;
        // Client B starts a shell: that publishes the terminals event which
        // makes A's column pull /projects, now answered slowly.
        const shellB = await L.createShell(pageB, project);
        shells.push(shellB);
        const idB2 = shellId(shellB);
        await sleep(400);
        // The boosted navigation every JS opened terminal takes (the docker
        // actions' `@dc/docker` navigate is this call): the page swaps while
        // the column's pull is still running, where a full load would have
        // thrown the pending answer away with the document.
        const target = shellId(shells[0]);
        await page.evaluate((id) => window.app.navigate(`/shells/${id}`), target);
        await page.waitForURL(new RegExp(`/shells/${target}`), { timeout: 15000 });
        await page.waitForSelector(`.terminal-tab[data-tab-id="${idB2}"]`, { timeout: 15000 });
        const samples = [];
        for (let i = 0; i < 30; i += 1) {
          await sleep(250);
          samples.push(await column());
        }
        const stale = samples.filter((s) => s.title !== "Terminals" || s.projectRows > 0);
        assert(!stale.length, `the terminal page showed the project index in its column: ${JSON.stringify(stale[0])}`);
        const last = samples[samples.length - 1];
        assert(last.tabs > 0, `the terminals column lost its rows: ${JSON.stringify(last)}`);
        return `${samples.length} samples stayed on "${last.title}" with ${last.tabs} rows`;
      } finally {
        slow = false;
        await page.unroute("**/projects").catch(() => {});
      }
    });
  } finally {
    if (pageB) await pageB.close().catch(() => {});
    if (ctxB) await ctxB.close().catch(() => {});
    for (const url of shells) await L.deleteShell(page, url).catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
  }
});
