const L = require("./lib");
const { assert, BASE, sleep, dismissUpdate } = L;

// Docker integration end to end: compose containers as chips on the projects
// page ([data-chip-kind="docker"], joined via the compose working_dir label),
// the project's compose button ([data-docker-project-menu]) driving the
// configured commands for the whole project (POST /projects/:name/docker/compose
// with the action id, background run, busy state), the container chip menu
// (ports line, every address that container answers on as an Open entry, the
// routed host a link rule reads out of its labels before the published port,
// Shell and Logs via
// POST /docker/:id/shell|logs-shell landing in a normal cockpit shell,
// Start/Stop/Restart; no status line, the uptime the daemon reports is
// a snapshot of the last cache refresh and is not rendered anywhere),
// everything following the daemon live over the SSE "docker" event, the
// editor's docker surface (statusbar segment [data-editor-docker-status],
// docker sheet with compose rows and container rows, JSON from
// GET /projects/:name/editor/docker), the project deletion that brings the
// project's stacks down before the directory goes (background, the row shows
// [data-project-deleting] while it works), and docker's own settings section
// (/settings/docker: the host field with its connection line plus the compose
// commands under #settings-docker-actions, one row per entry with the argv it
// splits into, the icon picked from a fixed vocabulary, add and remove, a
// touch drag on the grip handle reordering the rows with the save keeping the
// new order and the compose menu following it, the
// empty list and the way back; the host field is gone from /settings/general,
// docker is unreleased and moves without a redirect).
//
// Wherever containers are listed they stand in one order, decided once on the
// server (docker.State.ForDir): what is unwell first, then what runs, then the
// rest, stable by name inside a group. The check brings up a stack of its own
// whose names contradict that order, so a list that came back in the cache's
// own order fails instead of passing by accident, and it reads both surfaces,
// the chips on the projects page and the editor's grid.
//
// What a running compose command looks like is motion: the docker icon rides a
// wave (.dc-docker-working) on the row and in the editor's statusbar, and the
// run's own menu entry carries a turning loader (.dc-spin), which used to be a
// picture of a spinner with no animation on it at all. Both are read as motion
// and not as a class name, animation plus play state plus a display that can be
// transformed, because an inline box ignores every transform and a rotation on
// one is silently nothing.
//
// The two menus answer two different questions, which is what keeps the
// project menu readable on a phone: the project menu says which container (one
// entry each, the address itself when there is exactly one, otherwise the
// container's name and how many it has, drilling into the same menu with a
// Back entry), the container's own menu says which address. A label that does
// not fit loses its middle and never its end, and the menu stays inside the
// viewport, both checked at 390x844.
//
// Where an address comes from is configuration too (settings key
// docker-link-rules, edited under #settings-docker-links): a published port is
// docker's own truth and always offered, a routed host is read out of a label
// of the container by a rule, and the default rule covers the traefik router
// labels. Such a link carries no port and no scheme of its own, it is opened
// over the scheme the page was reached over, so the runner reads the entry's
// label rather than a URL. The rows say what a pattern gets wrong and what it
// finds in the containers running right now, an emptied list leaves the ports
// alone, and one button puts the default rule back by clearing the key.
//
// The compose commands are configuration (settings key docker-compose-actions):
// the menu shows one entry per configured command in the stored order, each with
// the icon its stored name stands for (the name to class table is one table in
// Go, so the stored value never carries a class), an entry marked "ask first"
// confirms before it starts, and every run lands on the same output page
// (/projects/:name/docker/runs/:id) with its exit code and a Cancel while it
// goes. The runner edits that list on the settings page and puts the defaults
// back, so it needs its own throwaway instance like every other one.
//
// The run's news: opening a run's page marks the project's docker target read
// (server side on the GET, like the backup page), and a failed run is never
// swallowed by the notify dedupe window as a follow-up of a fresh success,
// it replaces it as the target's one unread entry. The output block is
// Tabler's own pre, a dark surface with light text in both themes, measured
// as a real luminance gap under both color schemes: a half override once
// kept the light text on a light ground and was unreadable in light mode.
//
// Fixture the host prepares before the run (see README): the instance's
// projects dir holds a project named DOCKER_PROJECT (default "dockere2e")
// whose directory carries a compose file with one service "web" publishing
// port 18088 and carrying the traefik router label the default link rule
// reads (Host(`dockere2e.test`), no traefik anywhere on the host, only the
// label is read), already up, and the instance reaches the Docker daemon plus
// the docker CLI. The runner stops, starts and composes only that fixture
// and leaves the stack up. Compose runs pull no images, the fixture image
// is local, so the up/down cycle stays in seconds. The delete check brings
// its own scratch project with a stack of its own (same image, no published
// port) and takes it away again, the fixture is never deleted.

const NAME = process.env.DOCKER_PROJECT || "dockere2e";
// The host the fixture's traefik label routes, which the default link rule
// reads out of it. Nothing serves it, the link is never followed.
const LINK_HOST = process.env.DOCKER_LINK_HOST || "dockere2e.test";

const row = (page) => page.locator(`#project-${NAME}`);
const chip = (page) => row(page).locator('[data-chip-kind="docker"]');
const composeBtn = (page) => row(page).locator("[data-docker-project-menu]");
// A label is matched whole and literally: an entry may carry brackets ("web
// (2 addresses)") and a dot is a dot in a host name.
const menuItem = (page, label) =>
  page.locator(".dc-context-menu button", { hasText: new RegExp(`^${label.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}$`) });

// A container chip answers a plain click with a shell, so its menu is where
// every other row's menu is: on the right click, and on a long press.
async function openMenuOn(page, locator) {
  await locator.locator(".project-chip-main").click({ button: "right" });
  await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
}

// closeMenu also waits out the shared just-closed window: a menu that has just
// gone swallows the next click on the control that opens it, which is what
// makes a second click on a toggle close it instead of reopening it. A runner
// clicking faster than a finger can would otherwise measure that window.
async function closeMenu(page) {
  await page.keyboard.press("Escape");
  await page.waitForSelector(".dc-context-menu", { state: "detached", timeout: 4000 });
  await sleep(400);
}

// postAs runs an authenticated POST from inside the page, the way @dc/http
// does. The delete check needs a compose stack of its own, and writing the
// file plus composing it is what the editor and the compose menu do anyway.
async function postAs(page, path, body) {
  return page.evaluate(async ({ path, body }) => {
    const res = await fetch(path, {
      method: "POST",
      headers: {
        "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]')?.content || "",
        "Content-Type": "application/x-www-form-urlencoded",
        Accept: "application/json",
      },
      body: new URLSearchParams(body).toString(),
    });
    return { ok: res.ok, status: res.status };
  }, { path, body });
}

// notifications reads the center's list from inside the page. The polling
// around it stays on the Node side: Playwright's waitForFunction does not
// await an async predicate, the returned promise is always truthy and the
// wait resolves at once, which is how a check that "waited" this way once
// raced the very run it had just started.
async function notifications(page) {
  return page.evaluate(async () => {
    const res = await fetch("/notifications", { headers: { Accept: "application/json" } });
    return (await res.json()).notifications || [];
  });
}

async function waitNotify(page, test, timeout, what) {
  const t0 = Date.now();
  for (;;) {
    if (test(await notifications(page))) return;
    if (Date.now() - t0 > timeout) throw new Error(`timed out waiting for ${what}`);
    await sleep(1000);
  }
}

// apiClient is a logged in request client of its own, outside every browser
// context: the reconnect check cuts the editor's context off the network, and
// what moves a container while it is off has to be reachable from somewhere
// else entirely.
async function apiClient() {
  const { request } = require("playwright-core");
  const ctx = await request.newContext({ baseURL: BASE, ignoreHTTPSErrors: true });
  const form = await (await ctx.get("/login")).text();
  const token = /name="csrf_token" value="([^"]+)"/.exec(form)?.[1] || "";
  await ctx.post("/login", { form: { username: "admin", password: "password", csrf_token: token } });
  const projects = await (await ctx.get("/projects")).text();
  const csrf = /name="csrf-token" content="([^"]+)"/.exec(projects)?.[1] || "";
  return {
    post: (path) => ctx.post(path, { headers: { "X-CSRF-Token": csrf, Accept: "application/json" } }),
    dispose: () => ctx.dispose(),
  };
}

// The scratch stack the delete check owns: the fixture image, so nothing is
// pulled, and no published port, so it cannot collide with the fixture.
const SCRATCH_COMPOSE = "services:\n  web:\n    image: nginx:alpine\n";
// Two services out of the same local image, one of which is over before the
// list is read: what the order check needs is a stopped container next to a
// running one, in a project of its own.
const SORT_COMPOSE = "services:\n  a-stopped:\n    image: nginx:alpine\n    entrypoint: [\"true\"]\n  z-running:\n    image: nginx:alpine\n";

// READ_MOTION answers whether an element really animates: which keyframes
// matched, whether one of them runs, and whether the box can be transformed at
// all. The last one is not pedantry, an inline element ignores every transform,
// so a rotation on a bare icon is silently nothing.
const READ_MOTION = (el) => ({
  name: getComputedStyle(el).animationName,
  display: getComputedStyle(el).display,
  running: el.getAnimations().filter((a) => a.playState === "running").length,
});

L.runFeature("DOCKER", async ({ engine, browser, page, run, mobilePage, bag }) => {
  await run("the fixture project shows its container chip and compose button", async () => {
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    await page.waitForSelector(`#project-${NAME} [data-chip-kind="docker"]`, { timeout: 8000 });
    assert(await chip(page).count() === 1, "expected exactly one docker chip");
    assert((await chip(page).getAttribute("data-chip-name")) === "web", "chip is not named by the compose service");
    assert(await chip(page).locator(".dc-term-icon.running").count() === 1, "running container chip is not green");
    assert(await chip(page).locator("[data-docker-logs]").count() === 1, "direct logs icon missing on the chip");
    assert(await composeBtn(page).count() === 1, "compose button missing next to the row actions");
    assert(await composeBtn(page).locator(".dc-term-icon.running").count() === 1, "compose button not green while running");
    assert(await row(page).locator('[data-chip-kind="docker-stack"]').count() === 0, "the old stack chip is still rendered");
  });

  await run("the container chips stand in a row of their own, folding on their own", async () => {
    const rows = row(page).locator("[data-sessions-body] .project-chips");
    assert(await rows.count() === 2, "expected a terminal row and a container row");
    assert(await rows.nth(0).locator('[data-chip-fold="terminals"]').count() === 1, "the first row is not the terminal row");
    assert(await rows.nth(0).locator('[data-chip-kind="docker"]').count() === 0, "a container chip is still among the terminals");
    assert(await rows.nth(1).locator('[data-chip-fold="docker"]').count() === 1, "the second row is not the container row");
    assert(await rows.nth(1).locator('[data-chip-kind="docker"]').count() === 1, "the container chip is not in its own row");
    // Its own row means below, not merely later in the markup.
    const terminals = await rows.nth(0).boundingBox();
    const containers = await rows.nth(1).boundingBox();
    assert(containers.y >= terminals.y + terminals.height - 1, `the container row does not sit below (${terminals.y} vs ${containers.y})`);
    // The + chips belong to the terminals, the container row carries none.
    assert(await rows.nth(0).locator(".project-chip-new").count() === 2, "the new coder and shell chips left the terminal row");
    assert(await rows.nth(1).locator(".project-chip-new").count() === 0, "the container row carries a new chip");
  });

  // The order is one decision on the server (docker.State.ForDir) and every
  // surface reads that one list, so the scratch stack proves both ends of it
  // at once: the chips on the projects page and the editor's own grid. The
  // service names are picked against the answer, "a-stopped" sorts before
  // "z-running" by name, so a list that came back in the cache's own order
  // fails here rather than passing by accident.
  await run("a stopped container stands behind the running ones, everywhere", async () => {
    const scratch = `dcsort-${Date.now().toString(36)}`;
    await L.createProject(page, scratch);
    try {
      const wrote = await postAs(page, `/projects/${scratch}/editor/file`, { path: "compose.yaml", content: SORT_COMPOSE });
      assert(wrote.ok, `writing the scratch compose file answered ${wrote.status}`);
      const up = await postAs(page, `/projects/${scratch}/docker/compose`, { stack: "", action: "up" });
      assert(up.ok, `compose up on the scratch project answered ${up.status}`);
      const chips = page.locator(`#project-${scratch} [data-chip-kind="docker"]`);
      await page.waitForFunction((id) => {
        const list = document.querySelectorAll(`#project-${id} [data-chip-kind="docker"]`);
        return list.length === 2 && Array.from(list).some((c) => c.classList.contains("is-idle"));
      }, scratch, { timeout: 90000 });
      const names = await chips.evaluateAll((list) => list.map((c) => c.dataset.chipName));
      assert(names.join(",") === "z-running,a-stopped", `the chips stand as ${names.join(", ")}`);

      await page.goto(`${BASE}/projects/${scratch}/editor`, { waitUntil: "domcontentloaded" });
      await dismissUpdate(page);
      await page.waitForSelector("[data-editor-docker-status]:not([hidden])", { timeout: 15000 });
      await page.click("[data-editor-docker-status]");
      await page.waitForSelector("[data-editor-sheet]:not([hidden])", { timeout: 4000 });
      const cells = await page.locator("[data-editor-docker-list] [data-docker-container]").allTextContents();
      const first = cells.findIndex((t) => /z-running/.test(t));
      const second = cells.findIndex((t) => /a-stopped/.test(t));
      assert(first === 0 && second === 1, `the editor lists them as ${cells.map((t) => t.trim()).join(" | ")}`);
      await page.keyboard.press("Escape");
    } finally {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await dismissUpdate(page);
      await page.locator(`#project-${scratch} form[action="/projects/delete"] button`).click();
      await page.waitForSelector(".swal2-confirm", { state: "visible", timeout: 8000 });
      await sleep(150);
      await page.click(".swal2-confirm");
      await page.waitForSelector(`#project-${scratch}`, { state: "detached", timeout: 180000 });
    }
  });

  await run("the container menu carries ports, shells, logs, and lifecycle", async () => {
    await openMenuOn(page, chip(page));
    assert(await menuItem(page, "Open :18088").count() === 1, "port link missing");
    // The address the container's own label declares, before the port: where a
    // route exists it is the address a person wants. It carries no port, the
    // proxy answers on the default one.
    assert(await menuItem(page, `Open ${LINK_HOST}`).count() === 1, "the routed host from the container's label is missing");
    const labels = (await page.locator(".dc-context-menu button").allTextContents()).map((t) => t.trim());
    assert(labels.indexOf(`Open ${LINK_HOST}`) < labels.indexOf("Open :18088"), "the routed host does not stand before the port");
    assert(await menuItem(page, "Shell").count() === 1, "Shell entry missing");
    assert(await menuItem(page, "Logs").count() === 1, "Logs entry missing");
    assert(await menuItem(page, "Log terminal").count() === 0, "the separate Log terminal entry is still there");
    assert(await menuItem(page, "Stop").count() === 1, "Stop entry missing");
    assert(await menuItem(page, "Restart").count() === 1, "Restart entry missing");
    assert(await menuItem(page, "Start").count() === 0, "Start offered for a running container");
    const head = page.locator(".dc-context-menu button[disabled]");
    assert(await head.count() === 1, "ports line missing");
    // The ports line stays what it is, the published mappings: an address a
    // label declares is a link, not a mapping the daemon knows about.
    assert(/18088:80/.test(await head.textContent()), "ports line misses the port mapping");
    assert(!(await head.textContent()).includes(LINK_HOST), "the routed host leaked into the ports line");
    assert(!/\bUp\b/.test(await page.locator(".dc-context-menu").textContent()), "the menu still reports an uptime");
    await closeMenu(page);
  });

  await run("a click on a running container's chip opens a shell in it", async () => {
    await Promise.all([
      page.waitForURL(/\/shells\/[^/]+$/, { timeout: 15000 }),
      chip(page).locator(".project-chip-main").click(),
    ]);
    await page.waitForSelector("terminal-attach", { timeout: 8000 });
    await L.deleteShell(page, page.url());
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
  });

  await run("a long press on a container chip still opens its menu", async () => {
    const mp = await mobilePage();
    await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(mp);
    await mp.waitForSelector(`#project-${NAME} [data-chip-kind="docker"]`, { timeout: 8000 });
    // The connect snapshot swaps the chip bodies right after the load, and the
    // press is dispatched on the element held here.
    await sleep(1200);
    await mp.locator(`#project-${NAME} [data-chip-kind="docker"]`).first().evaluate((el) => {
      const r = el.getBoundingClientRect();
      const x = r.x + 8;
      const y = r.y + 8;
      el.dispatchEvent(new PointerEvent("pointerdown", {
        bubbles: true, cancelable: true, pointerId: 4, pointerType: "touch", clientX: x, clientY: y,
      }));
      const touch = new Touch({ identifier: 1, target: el, clientX: x, clientY: y });
      el.dispatchEvent(new TouchEvent("touchstart", {
        bubbles: true, cancelable: true, touches: [touch], targetTouches: [touch], changedTouches: [touch],
      }));
    });
    await mp.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
    const items = mp.locator(".dc-context-menu button");
    assert(await items.filter({ hasText: /^Shell$/ }).count() === 1, "the long press menu lost its Shell entry");
    assert(await items.filter({ hasText: /^Stop$/ }).count() === 1, "the long press menu lost its lifecycle entries");
    await mp.keyboard.press("Escape");
    assert(new URL(mp.url()).pathname === "/projects", "the long press navigated away");
  });

  await run("the compose button closes its own menu on a second click", async () => {
    await composeBtn(page).click();
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
    await composeBtn(page).click();
    await page.waitForSelector(".dc-context-menu", { state: "detached", timeout: 4000 });
    // And it opens again, the toggle is not a one way door. Past the shared
    // just-closed window, which is what swallowed the closing click.
    await sleep(400);
    await composeBtn(page).click();
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
    await closeMenu(page);
  });

  await run("the logs icon on the chip opens a log terminal, never a dialog", async () => {
    await Promise.all([
      page.waitForURL(/\/shells\/[^/]+$/, { timeout: 15000 }),
      chip(page).locator("[data-docker-logs]").click(),
    ]);
    await page.waitForSelector("terminal-attach", { timeout: 8000 });
    assert(await page.locator(".swal2-popup").count() === 0, "a dialog opened after all");
    // A container's logs terminal carries that container's name.
    const named = (await page.locator("[data-rename-label]").textContent()).trim();
    assert(named === "web logs", `the container logs terminal is called "${named}"`);
    await L.deleteShell(page, page.url());
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
  });

  await run("the project menu carries the containers, the logs and every configured command", async () => {
    await composeBtn(page).click();
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
    // The project menu answers which container, not which address: the fixture
    // container has two, so it stands here once and drills in.
    assert(await menuItem(page, "web (2 addresses)").count() === 1, "the project menu does not name the container and its addresses");
    assert(await menuItem(page, "Open :18088").count() === 0, "the project menu spreads a container's addresses");
    assert(await menuItem(page, `Open ${LINK_HOST}`).count() === 0, "the project menu spreads a container's addresses");
    assert(await menuItem(page, "Logs").count() === 1, "the project's logs entry is missing");
    // The four default commands, in the order they are configured in.
    for (const label of ["Compose up", "Compose down", "Compose build", "Compose down with volumes"]) {
      assert(await menuItem(page, label).count() === 1, `configured command "${label}" missing`);
    }
    const order = (await page.locator(".dc-context-menu button").allTextContents()).map((t) => t.trim());
    const at = (label) => order.indexOf(label);
    assert(at("Compose up") >= 0 && at("Compose up") < at("Compose down"), "the commands do not stand in the configured order");
    assert(at("Compose down") < at("Compose build"), "the commands do not stand in the configured order");
    // The icon comes from the server as a class, the stored setting carries
    // only our own name for it.
    assert(await menuItem(page, "Compose up").locator("i.ti.ti-player-play").count() === 1, "compose up misses its icon");
    assert(await menuItem(page, "Compose down with volumes").locator("i.ti.ti-trash").count() === 1, "the destructive command misses its icon");
    await closeMenu(page);
  });

  await run("a container with several addresses drills in, and back out again", async () => {
    await composeBtn(page).click();
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
    await menuItem(page, "web (2 addresses)").click();
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
    // The same menu again, now that container's addresses, the route before
    // the port, and a way back as the first entry.
    const drilled = (await page.locator(".dc-context-menu button").allTextContents()).map((t) => t.trim());
    assert(drilled[0] === "Back", `the drilled menu starts with "${drilled[0]}"`);
    assert(drilled.indexOf(`Open ${LINK_HOST}`) > 0, "the drilled menu misses the routed host");
    assert(drilled.indexOf(`Open ${LINK_HOST}`) < drilled.indexOf("Open :18088"), "the drilled menu puts the port first");
    assert(drilled.length === 3, `the drilled menu carries ${drilled.length} entries: ${drilled.join(", ")}`);
    // An address never loses its end, so its label is a head that may shrink
    // and a tail that may not, and the whole address is the item's title.
    const address = menuItem(page, `Open ${LINK_HOST}`);
    assert(await address.locator(".dc-menu-label-tail").count() === 1, "the address label carries no tail of its own");
    assert((await address.getAttribute("title")) === `Open ${LINK_HOST}`, "the address item does not carry the whole address as its title");
    await menuItem(page, "Back").click();
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
    assert(await menuItem(page, "Compose up").count() === 1, "Back did not lead to the project menu");
    assert(await menuItem(page, "web (2 addresses)").count() === 1, "Back did not lead to the project menu");
    await closeMenu(page);
  });

  await run("the container's own menu keeps every address, and the menu fits a phone", async () => {
    const mp = await mobilePage();
    await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(mp);
    await mp.waitForSelector(`#project-${NAME} [data-chip-kind="docker"]`, { timeout: 8000 });
    // The connect snapshot swaps the chip bodies right after the load, and the
    // press is dispatched on the element held here. A host far longer than the
    // screen rides in as one more link span before the press, so the menu opens
    // with it through the real path: addressLabel splits it and place clamps
    // the menu, which is what the fit below measures.
    await sleep(1200);
    const longHost = `${"long-service-name-".repeat(6)}web.example.com`;
    await mp.locator(`#project-${NAME} [data-chip-kind="docker"]`).first().evaluate((el, host) => {
      const span = document.createElement("span");
      span.hidden = true;
      span.setAttribute("data-docker-link", "");
      span.dataset.linkHost = host;
      el.appendChild(span);
      const r = el.getBoundingClientRect();
      const x = r.x + 8;
      const y = r.y + 8;
      el.dispatchEvent(new PointerEvent("pointerdown", {
        bubbles: true, cancelable: true, pointerId: 6, pointerType: "touch", clientX: x, clientY: y,
      }));
      const touch = new Touch({ identifier: 2, target: el, clientX: x, clientY: y });
      el.dispatchEvent(new TouchEvent("touchstart", {
        bubbles: true, cancelable: true, touches: [touch], targetTouches: [touch], changedTouches: [touch],
      }));
    }, longHost);
    await mp.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
    // Whoever long presses this container asked about this container: every
    // address, no drill in.
    const labels = (await mp.locator(".dc-context-menu button").allTextContents()).map((t) => t.trim());
    assert(labels.includes(`Open ${LINK_HOST}`) && labels.includes("Open :18088"), `the chip menu lost an address: ${labels.join(", ")}`);
    assert(!labels.some((t) => /addresses\)$/.test(t)), "the chip menu drills in instead of listing");
    // The menu stays inside the 390 wide viewport, and a label longer than the
    // screen keeps its tail.
    const fits = await mp.evaluate((host) => {
      const menu = document.querySelector(".dc-context-menu");
      const item = Array.from(menu.querySelectorAll(".dropdown-item")).find((b) => b.title === `Open ${host}`);
      const head = item?.querySelector(".dc-menu-label-head");
      const tail = item?.querySelector(".dc-menu-label-tail");
      if (!head || !tail) return null;
      const box = menu.getBoundingClientRect();
      return {
        left: box.left,
        right: box.right,
        width: window.innerWidth,
        tailCut: tail.scrollWidth - tail.clientWidth,
        headCut: head.scrollWidth - head.clientWidth,
      };
    }, longHost);
    assert(fits, "the long host did not reach the menu");
    assert(fits.left >= 0 && fits.right <= fits.width, `the menu runs off the screen (${fits.left}..${fits.right} of ${fits.width})`);
    assert(fits.tailCut <= 1, "the tail of the label was cut off");
    assert(fits.headCut > 1, "the head did not have to shrink, so the check measured nothing");
    await mp.keyboard.press("Escape");
  });

  await run("the project menu's logs entry opens the stack's terminal", async () => {
    await composeBtn(page).click();
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
    await Promise.all([
      page.waitForURL(/\/shells\/[^/]+$/, { timeout: 15000 }),
      menuItem(page, "Logs").click(),
    ]);
    await page.waitForSelector("terminal-attach", { timeout: 8000 });
    // The stack's is not named after the project, it is every service of a
    // compose directory.
    const stackNamed = (await page.locator("[data-rename-label]").textContent()).trim();
    assert(stackNamed === "docker logs", `the stack logs terminal is called "${stackNamed}"`);
    await L.deleteShell(page, page.url());
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
  });

  await run("Shell opens a normal cockpit shell inside the container", async () => {
    await openMenuOn(page, chip(page));
    await Promise.all([
      page.waitForURL(/\/shells\/[^/]+$/, { timeout: 15000 }),
      menuItem(page, "Shell").click(),
    ]);
    await page.waitForSelector("terminal-attach", { timeout: 8000 });
    const shellUrl = page.url();
    await L.deleteShell(page, shellUrl);
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
  });

  await run("a command that asks first does not start until it is confirmed", async () => {
    await composeBtn(page).click();
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
    await menuItem(page, "Compose down with volumes").click();
    await page.waitForSelector(".swal2-popup", { state: "visible", timeout: 8000 });
    const dialog = await page.locator(".swal2-popup").textContent();
    assert(/docker compose down -v/.test(dialog), `the confirm does not name the command: "${dialog}"`);
    await page.click(".swal2-cancel");
    await page.waitForSelector(".swal2-popup", { state: "detached", timeout: 8000 });
    await sleep(800);
    assert(await chip(page).count() === 1, "the cancelled confirm brought the stack down anyway");
  });

  await run("compose down over the row button empties the project live, compose up refills it", async () => {
    await composeBtn(page).click();
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
    assert(/1 of 1 running/.test(await page.locator(".dc-context-menu button[disabled]").first().textContent()), "compose status wrong");
    await menuItem(page, "Compose down").click();
    await page.waitForFunction((project) => {
      const section = document.getElementById(`project-${project}`);
      return section && !section.querySelector('[data-chip-kind="docker"]');
    }, NAME, { timeout: 60000 });
    assert(new URL(page.url()).pathname === "/projects", "compose down navigated away");
    assert(await composeBtn(page).count() === 1, "compose button vanished with the containers");
    assert(await composeBtn(page).locator(".dc-term-icon.running").count() === 0, "compose button still green with nothing running");
    await composeBtn(page).click();
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
    await menuItem(page, "Compose up").click();
    await page.waitForSelector(`#project-${NAME} [data-chip-kind="docker"] .dc-term-icon.running`, { timeout: 90000 });
    assert(await composeBtn(page).locator(".dc-term-icon.running").count() === 1, "compose button not green after the up");
  });

  await run("the run of a configured command has its output page with the exit code", async () => {
    await composeBtn(page).click();
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
    const outputRow = menuItem(page, "Output of Compose up");
    assert(await outputRow.count() === 1, "the finished run is not offered in the menu");
    await Promise.all([
      page.waitForURL(/\/docker\/runs\/[^/]+$/, { timeout: 15000 }),
      outputRow.click(),
    ]);
    await page.waitForSelector("dc-docker-run", { timeout: 8000 });
    const status = await page.locator("[data-run-status]").textContent();
    assert(/Exit status 0/.test(status), `the finished run reads "${status}"`);
    assert(/docker compose up -d/.test(await page.locator("dc-docker-run").textContent()), "the page does not name the command line");
    assert(await page.locator("[data-run-stop]:not([hidden])").count() === 0, "a finished run still offers Cancel");
    // The output is readable in both themes: Tabler's own pre, a dark block
    // with light text, so the gap between the text and what it stands on is
    // large under either scheme. The broken state was a light background kept
    // under the light text, a gap of almost nothing in light mode.
    const contrastGap = async (scheme) => {
      await page.emulateMedia({ colorScheme: scheme });
      await page.waitForFunction((want) => {
        const theme = document.documentElement.getAttribute("data-bs-theme");
        return want === "dark" ? theme === "dark" : theme !== "dark";
      }, scheme, { timeout: 4000 });
      return page.evaluate(() => {
        const pre = document.querySelector("[data-run-output]");
        const lum = (v) => {
          const c = (v.match(/\d+(\.\d+)?/g) || []).slice(0, 3).map(Number);
          return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2];
        };
        let el = pre;
        let bg = getComputedStyle(el).backgroundColor;
        while (el && (bg === "rgba(0, 0, 0, 0)" || bg === "transparent")) {
          el = el.parentElement;
          bg = el ? getComputedStyle(el).backgroundColor : "rgb(255, 255, 255)";
        }
        return Math.abs(lum(getComputedStyle(pre).color) - lum(bg));
      });
    };
    const light = await contrastGap("light");
    assert(light > 60, `the output has no contrast in light mode (gap ${Math.round(light)})`);
    const dark = await contrastGap("dark");
    assert(dark > 60, `the output has no contrast in dark mode (gap ${Math.round(dark)})`);
    await page.emulateMedia({ colorScheme: null });
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
  });

  await run("deleting a project brings its stack down, the row works while it does", async () => {
    const scratch = `dcdel-${Date.now().toString(36)}`;
    await L.createProject(page, scratch);
    const wrote = await postAs(page, `/projects/${scratch}/editor/file`, { path: "compose.yaml", content: SCRATCH_COMPOSE });
    assert(wrote.ok, `writing the scratch compose file answered ${wrote.status}`);
    const up = await postAs(page, `/projects/${scratch}/docker/compose`, { stack: "", action: "up" });
    assert(up.ok, `compose up on the scratch project answered ${up.status}`);
    await page.waitForSelector(`#project-${scratch} [data-chip-kind="docker"] .dc-term-icon.running`, { timeout: 90000 });

    await page.locator(`#project-${scratch} form[action="/projects/delete"] button`).click();
    await page.waitForSelector(".swal2-confirm", { state: "visible", timeout: 8000 });
    const dialog = await page.locator(".swal2-popup").textContent();
    assert(/brought down/.test(dialog), `the confirm does not name the containers: "${dialog}"`);
    assert(/volumes/.test(dialog), `the confirm does not say the volumes go: "${dialog}"`);
    await sleep(150);
    await page.click(".swal2-confirm");

    // The request is answered at once: the row says it is working, offers no
    // second delete, and the rest of the page keeps answering.
    await page.waitForSelector(`#project-${scratch} [data-project-deleting]`, { timeout: 15000 });
    assert(new URL(page.url()).pathname === "/projects", "the delete navigated away");
    assert(await page.locator(`#project-${scratch} form[action="/projects/delete"]`).count() === 0, "the working row still offers delete");
    await composeBtn(page).click();
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
    await closeMenu(page);
    await page.waitForSelector(`#project-${scratch}`, { state: "detached", timeout: 180000 });

    // The stack really went down: the container joined the project through the
    // compose working directory, so the same directory back would show its chip
    // again if anything had survived.
    await L.createProject(page, scratch);
    await sleep(1500);
    await page.reload({ waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    await page.waitForSelector(`#project-${scratch}`, { timeout: 8000 });
    assert(await page.locator(`#project-${scratch} [data-chip-kind="docker"]`).count() === 0, "a container survived the deletion");
    await L.deleteProject(page, scratch);
    await page.waitForSelector(`#project-${scratch}`, { state: "detached", timeout: 15000 });
  });

  await run("the editor carries the docker statusbar segment and sheet", async () => {
    await page.goto(`${BASE}/projects/${NAME}/editor`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    await page.waitForSelector(".cm-editor, .editor-textarea", { state: "attached", timeout: 15000 });
    await page.waitForSelector("[data-editor-docker-status]:not([hidden])", { timeout: 8000 });
    const label = await page.locator("[data-editor-docker-status-text]").textContent();
    assert(label === "1/1", `statusbar segment reads "${label}"`);
    await page.click("[data-editor-docker-status]");
    await page.waitForSelector("[data-editor-sheet]:not([hidden])", { timeout: 4000 });
    const rows = page.locator("[data-editor-docker-list] .dropdown-item");
    assert(await rows.count() >= 3, "docker sheet misses rows");
    assert(await page.locator("[data-editor-docker-list] .dropdown-item", { hasText: /^Compose up$/ }).count() === 1, "sheet compose up missing");
    // The project entries the projects page has, here too: its ports and the
    // logs of the whole stack.
    // The same answer as the projects page: which container, and its addresses
    // one drill in away, the sheet showing that list with a way back.
    const drillRow = page.locator("[data-editor-docker-list] .dropdown-item", { hasText: /^web \(2 addresses\)$/ });
    assert(await drillRow.count() === 1, "the sheet does not name the container and its addresses");
    await drillRow.click();
    assert(await page.locator("[data-editor-docker-list] .dropdown-item", { hasText: /^Open :18088$/ }).count() === 1, "the drilled sheet misses the port");
    assert(await page.locator("[data-editor-docker-list] .dropdown-item", { hasText: new RegExp(`^Open ${LINK_HOST}$`) }).count() === 1, "the drilled sheet misses the routed host");
    await page.locator("[data-editor-docker-list] .dropdown-item", { hasText: /^Back$/ }).click();
    assert(await page.locator("[data-editor-docker-list] .dropdown-item", { hasText: /^web \(2 addresses\)$/ }).count() === 1, "Back did not lead to the project's own list");
    assert(await page.locator("[data-editor-docker-list] .dropdown-item", { hasText: /^Logs$/ }).count() === 1, "sheet stack logs missing");
    // The containers stand in a plain bootstrap row, one, two or three per
    // line by the width, and no cell carries a logs button of its own.
    const grid = page.locator("[data-editor-docker-list] .row");
    assert(await grid.count() === 1, "the container row is missing");
    assert(await grid.locator("> .col-12.col-sm-6.col-lg-4").count() >= 1, "the containers do not sit in columns");
    assert(await page.locator("[data-editor-docker-list] [data-docker-logs]").count() === 0, "the per row logs icon is still there");
    const cell = page.locator("[data-editor-docker-list] [data-docker-container]", { hasText: /web/ });
    assert(await cell.count() === 1, "sheet container cell missing");
    assert(!/\bUp\b/.test(await cell.textContent()), "the container cell still reports an uptime");
    await cell.click();
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
    assert(await menuItem(page, "Stop").count() === 1, "container menu from the sheet misses Stop");
    assert(await menuItem(page, "Logs").count() === 1, "container menu from the sheet misses Logs");
    await closeMenu(page);
    await page.click("[data-editor-sheet-close]");
    await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 4000 });
  });

  // The sheet is a list of rows and the keyboard walks it: it opens with the
  // first row focused, the arrows step over the rows, a drill in and the way
  // back both start over on the first row of the level they land on, and a row
  // pressed with Enter carries no click point, so a menu it opens takes the
  // row's own rect as its anchor instead of the screen corner.
  await run("the docker sheet takes the keyboard, into a drilled level and back out", async () => {
    const focus = () => page.evaluate(() => {
      const el = document.activeElement;
      return { text: (el.textContent || "").replace(/\s+/g, " ").trim(), inSheet: !!el.closest("[data-editor-sheet-body]") };
    });
    const rows = () => page.evaluate(() =>
      [...document.querySelectorAll("[data-editor-docker-list] .dropdown-item")]
        .filter((row) => !row.disabled)
        .map((row) => row.textContent.replace(/\s+/g, " ").trim()));
    await page.keyboard.press("Control+Shift+D");
    await page.waitForSelector("[data-editor-docker-list] .dropdown-item", { timeout: 8000 });
    await sleep(700);
    const list = await rows();
    const opened = await focus();
    assert(opened.inSheet && opened.text === list[0], `the sheet opened with the focus on "${opened.text}"`);
    await page.keyboard.press("ArrowDown");
    assert((await focus()).text === list[1], `ArrowDown landed on "${(await focus()).text}" instead of "${list[1]}"`);

    await page.evaluate(() => [...document.querySelectorAll("[data-editor-docker-list] .dropdown-item")]
      .find((row) => /^web \(2 addresses\)$/.test(row.textContent.trim())).focus());
    await page.keyboard.press("Enter");
    await sleep(400);
    const drilled = await rows();
    assert(/^Back$/.test(drilled[0]), `the drilled level starts with "${drilled[0]}"`);
    assert((await focus()).text === drilled[0], `the drilled level focused "${(await focus()).text}"`);
    await page.keyboard.press("Enter");
    await sleep(400);
    assert((await focus()).text === list[0], `the way back focused "${(await focus()).text}" instead of "${list[0]}"`);

    // The row the keyboard stands on carries a surface and no ring, and it is
    // the very surface the mouse paints: one state, whichever way a row is
    // reached. A container name is long, so the cells are also where the sheet
    // would scroll sideways if a row grew out of its column.
    const cell = page.locator("[data-editor-docker-list] [data-docker-container]").first();
    const painted = await cell.evaluate((el) => {
      el.focus();
      const cs = getComputedStyle(el);
      return { bg: cs.backgroundColor, outline: `${cs.outlineWidth} ${cs.outlineStyle}` };
    });
    assert(painted.bg !== "rgba(0, 0, 0, 0)", "the focused container cell paints nothing");
    assert(painted.outline === "0px none", `the focused container cell draws ${painted.outline}`);
    await cell.hover();
    await sleep(200);
    const hovered = await cell.evaluate((el) => getComputedStyle(el).backgroundColor);
    assert(hovered === painted.bg, `hover paints ${hovered} and the keyboard ${painted.bg}`);
    const sideways = await page.evaluate(() => {
      const body = document.querySelector("[data-editor-sheet-body]");
      return { client: body.clientWidth, scroll: body.scrollWidth };
    });
    assert(sideways.scroll <= sideways.client + 1,
      `the sheet scrolls sideways: ${sideways.client} wide, ${sideways.scroll} to scroll`);

    await page.evaluate(() => document.querySelector("[data-editor-docker-list] [data-docker-container]").focus());
    await page.keyboard.press("Enter");
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
    const box = await page.locator(".dc-context-menu").boundingBox();
    assert(box.x > 10 && box.y > 10, `the menu of a row pressed with Enter sits at ${box.x},${box.y}`);
    await closeMenu(page);
    assert((await focus()).inSheet, "the closed menu left the focus outside the sheet");
    await page.keyboard.press("Escape");
    await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 4000 });
  });

  await run("Ctrl+Shift+D toggles the docker view, the statusbar terminal icon the panel", async () => {
    await page.keyboard.press("Control+Shift+D");
    await page.waitForSelector("[data-editor-sheet]:not([hidden])", { timeout: 4000 });
    await page.keyboard.press("Control+Shift+D");
    await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 4000 });
    assert(await page.locator("[data-editor-term-status]:not([hidden])").count() === 1, "statusbar terminal icon missing");
    await page.click("[data-editor-term-status]");
    await page.waitForFunction(() => {
      const panel = document.querySelector("[data-editor-term-panel]");
      return panel && !panel.hidden;
    }, null, { timeout: 8000 });
    assert((await page.locator("[data-editor-term-status]").getAttribute("aria-pressed")) === "true", "terminal icon not pressed while open");
    await page.click("[data-editor-term-status]");
    await page.waitForFunction(() => {
      const panel = document.querySelector("[data-editor-term-panel]");
      return panel && panel.hidden;
    }, null, { timeout: 4000 });
  });

  await run("a reconnect pulls the docker state into the editor", async () => {
    await page.waitForSelector("[data-editor-docker-status]:not([hidden])", { timeout: 8000 });
    await page.waitForFunction(() => document.querySelector("[data-editor-docker-status-text]")?.textContent === "1/1", null, { timeout: 15000 });
    const containerId = await page.evaluate(async () => {
      const res = await fetch(`${location.pathname}/docker`, { credentials: "same-origin" });
      return (await res.json()).containers[0].id;
    });
    // The container is moved by a client of its own, outside the browser, so
    // the editor's own context can be cut off the network entirely.
    const api = await apiClient();
    try {
      await page.context().setOffline(true);
      await sleep(1500);
      const stopped = await api.post(`/docker/${containerId}/stop`);
      assert(stopped.ok(), `stopping the container answered ${stopped.status()}`);
      await sleep(2000);
      assert((await page.locator("[data-editor-docker-status-text]").textContent()) === "1/1", "the editor heard something while it was offline");
      await page.context().setOffline(false);
      await page.waitForFunction(() => document.querySelector("[data-editor-docker-status-text]")?.textContent === "0/1", null, { timeout: 40000 });
    } finally {
      await page.context().setOffline(false);
      await api.post(`/docker/${containerId}/start`).catch(() => {});
      await api.dispose().catch(() => {});
    }
    await page.waitForFunction(() => document.querySelector("[data-editor-docker-status-text]")?.textContent === "1/1", null, { timeout: 40000 });
  });

  await run("docker has its own settings section with the host and the commands", async () => {
    await page.goto(`${BASE}/settings/general`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    assert(await page.locator('#settings-general input[name="docker_host"]').count() === 0, "the docker host still sits in the general section");
    const nav = page.locator('[data-settings-nav] a[href="/settings/docker"]');
    assert(await nav.count() === 1, "the settings navigation has no docker entry");
    await Promise.all([
      page.waitForURL(/\/settings\/docker$/, { timeout: 8000 }),
      nav.click(),
    ]);
    assert(await page.locator('[data-settings-nav] a[href="/settings/docker"].active').count() === 1, "the docker entry is not marked");
    assert(await page.locator('#settings-docker input[name="docker_host"]').count() === 1, "docker host field missing");
    const statusLine = await page.locator("#settings-docker .form-text").first().textContent();
    assert(/Connected to/.test(statusLine), `status line reads "${statusLine}"`);
    await Promise.all([
      page.waitForNavigation({ waitUntil: "domcontentloaded" }),
      page.click('#settings-docker button[type="submit"]:not([name])'),
    ]);
    assert(/Settings saved/.test(await page.locator("[data-page-content]").textContent()), "the docker save shows no flash");
  });

  await run("the compose commands are a list on the settings page, with the argv under each one", async () => {
    const rows = page.locator("#settings-docker-actions [data-action-row]");
    assert(await rows.count() === 4, `expected the four default commands, got ${await rows.count()}`);
    assert((await rows.nth(0).locator('input[name="action_label"]').inputValue()) === "Compose up", "the first row is not compose up");
    assert((await rows.nth(0).locator('input[name="action_command"]').inputValue()) === "docker compose up -d", "the first row lost its command");
    const argv = await rows.nth(0).locator("code").allTextContents();
    assert(argv.join(" ") === "docker compose up -d", `the argv preview reads "${argv.join(" ")}"`);
    assert(await rows.nth(3).locator("[data-action-confirm-box]").isChecked(), "the destructive default does not ask first");
    assert((await rows.nth(3).locator("[data-action-confirm]").inputValue()) === "1", "the confirm does not travel with its row");
    // The icon is picked from a fixed list, never typed: what is stored is our
    // own name for it, what is shown is the class that name stands for.
    assert(await rows.nth(0).locator('input[name="action_icon"][type="hidden"]').count() === 1, "the icon is a free text field");
    assert((await rows.nth(0).locator('[data-action-icon-value]').inputValue()) === "start", "the first row stores no icon name");
    assert(await rows.nth(0).locator("[data-action-icon-preview].ti-player-play").count() === 1, "the icon preview misses its class");
    await rows.nth(0).locator("[data-action-icon-pick]").click();
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
    assert(await page.locator(".dc-context-menu button i.ti-download").count() === 1, "the icon list misses an entry");
    await page.locator(".dc-context-menu button", { hasText: /^restart$/ }).click();
    assert((await rows.nth(0).locator('[data-action-icon-value]').inputValue()) === "restart", "picking an icon did not stick");
    assert(await rows.nth(0).locator("[data-action-icon-preview].ti-refresh").count() === 1, "the preview did not follow the pick");
    await rows.nth(0).locator("[data-action-icon-pick]").click();
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
    await page.locator(".dc-context-menu button", { hasText: /^start$/ }).click();

    await page.click("[data-action-add]");
    const fresh = page.locator("#settings-docker-actions [data-action-row]").nth(4);
    await fresh.locator('input[name="action_label"]').fill("E2E follow");
    await fresh.locator('input[name="action_command"]').fill('docker compose logs -f --tail "5"');
    await fresh.locator('input[name="action_timeout"]').fill("5m");
    await Promise.all([
      page.waitForNavigation({ waitUntil: "domcontentloaded" }),
      page.click('#settings-docker button[type="submit"]:not([name])'),
    ]);
    const added = page.locator("#settings-docker-actions [data-action-row]").nth(4);
    const addedArgv = await added.locator("code").allTextContents();
    // The quotes group and disappear, which is what the preview is for.
    assert(addedArgv.join("|") === "docker|compose|logs|-f|--tail|5", `the added argv reads "${addedArgv.join("|")}"`);
  });

  await run("a grip drag reorders the commands and the save keeps the new order", async () => {
    await page.goto(`${BASE}/settings/docker`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    const labels = () => page.$$eval('#settings-docker-actions [data-action-row] input[name="action_label"]', (els) => els.map((el) => el.value));
    const before = await labels();
    assert(before.length >= 2, `expected at least two rows, got ${before.length}`);
    // The element upgrades lazily after the load, and a drag dispatched before
    // its listeners exist moves nothing.
    await page.waitForFunction(() => !!document.querySelector("dc-docker-actions")?.rows, null, { timeout: 4000 });
    // A finger on the grip handle, the way the quick nav and the editor sheet
    // reorder on touch: the first move spends the threshold, the rest carries
    // the whole distance past the next row's center.
    await page.evaluate(async () => {
      const rows = [...document.querySelectorAll("#settings-docker-actions [data-action-row]")];
      const grip = rows[0].querySelector("[data-action-grip]");
      const r = grip.getBoundingClientRect();
      const x = Math.round(r.left + r.width / 2);
      const y0 = Math.round(r.top + r.height / 2);
      const lift = 8;
      const raw = rows[1].getBoundingClientRect().top - rows[0].getBoundingClientRect().top + 10;
      const send = (type, y) => grip.dispatchEvent(new PointerEvent(type, {
        bubbles: true, cancelable: true, pointerId: 51, pointerType: "touch", isPrimary: true,
        clientX: x, clientY: y, buttons: type === "pointerup" ? 0 : 1,
      }));
      send("pointerdown", y0);
      send("pointermove", y0 + lift);
      await new Promise((done) => setTimeout(done, 16));
      for (let i = 1; i <= 10; i++) {
        send("pointermove", Math.round(y0 + lift + (raw * i) / 10));
        await new Promise((done) => setTimeout(done, 16));
      }
      send("pointerup", y0 + lift + raw);
    });
    const after = await labels();
    assert(after[0] === before[1] && after[1] === before[0], `the drag did not swap the first two rows: ${after.join(", ")}`);
    await Promise.all([
      page.waitForNavigation({ waitUntil: "domcontentloaded" }),
      page.click('#settings-docker button[type="submit"]:not([name])'),
    ]);
    const saved = await labels();
    assert(saved[0] === before[1] && saved[1] === before[0], `the saved order came back as ${saved.join(", ")}`);
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    await composeBtn(page).click();
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
    // Exact labels: the menu also carries "Output of <label>" for the newest
    // run, which a substring match would take for the command itself.
    const items = (await page.locator(".dc-context-menu button").allTextContents()).map((t) => t.trim());
    const first = items.indexOf(before[1]);
    const second = items.indexOf(before[0]);
    assert(first >= 0 && second >= 0 && first < second, `the menu does not follow the stored order: ${items.join(" | ")}`);
    await closeMenu(page);
  });

  await run("a configured command runs, shows its output live, and cancels", async () => {
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    await composeBtn(page).click();
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
    assert(await menuItem(page, "E2E follow").count() === 1, "the added command is not in the menu");
    await menuItem(page, "E2E follow").click();
    await page.waitForSelector(".dc-toast", { timeout: 8000 }).catch(() => {});
    await sleep(1500);
    // The icon says a command is going, and it says it by moving: the class
    // alone would pass with keyframes nobody wrote, and a transform on an
    // inline box renders nothing at all.
    const waving = composeBtn(page).locator(".dc-docker-working");
    await waving.waitFor({ state: "attached", timeout: 8000 });
    const wave = await waving.evaluate(READ_MOTION);
    assert(wave.name === "dc-docker-wave", `the docker icon carries no wave: ${wave.name}`);
    assert(wave.running === 1, `the wave is not running: ${JSON.stringify(wave)}`);
    assert(wave.display !== "inline", "the waving icon is inline, so nothing of it moves");
    await composeBtn(page).click();
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
    const running = menuItem(page, "E2E follow is running…");
    assert(await running.count() === 1, "the running command is not offered in the menu");
    assert(await menuItem(page, "Compose up").count() === 0, "the commands are offered while one is running");
    const spin = await running.locator("i.ti-loader-2").evaluate(READ_MOTION);
    assert(spin.name === "dc-spin", `the running command's spinner carries no animation: ${spin.name}`);
    assert(spin.running === 1, `the spinner stands still: ${JSON.stringify(spin)}`);
    assert(spin.display !== "inline", "the spinner is inline, so nothing of it turns");
    await Promise.all([
      page.waitForURL(/\/docker\/runs\/[^/]+$/, { timeout: 15000 }),
      running.click(),
    ]);
    await page.waitForSelector("dc-docker-run", { timeout: 8000 });
    assert(/Running/.test(await page.locator("[data-run-status]").textContent()), "the run does not read as running");
    await page.click("[data-run-stop]");
    await page.waitForFunction(() => {
      const badge = document.querySelector("[data-run-status]");
      return badge && /cancelled/i.test(badge.textContent);
    }, null, { timeout: 20000 });
    assert(await page.locator("[data-run-stop]:not([hidden])").count() === 0, "the cancelled run still offers Cancel");
  });

  // restoreDefaults is the runner's own way back, the same route the buttons
  // take. Both checks below empty the list, and a failure in the middle of one
  // would otherwise leave the instance without any compose command for every
  // check that follows.
  async function restoreDefaults() {
    await postAs(page, "/docker/actions/restore", {}).catch(() => {});
  }

  // emptyActions takes every row away and saves, the state an install has when
  // somebody decided they want no compose buttons at all.
  async function emptyActions() {
    await page.goto(`${BASE}/settings/docker`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    const rows = await page.locator("#settings-docker-actions [data-action-row]").count();
    for (let i = 0; i < rows; i++) await page.locator("[data-action-remove]").first().click();
    assert(await page.locator("#settings-docker-actions [data-action-row]").count() === 0, "the rows did not go");
    await Promise.all([
      page.waitForNavigation({ waitUntil: "domcontentloaded" }),
      page.click('#settings-docker button[type="submit"]'),
    ]);
    assert(await page.locator("#settings-docker-actions [data-action-row]").count() === 0, "the emptied list came back");
  }

  await run("two projects finishing at the same moment are two notifications", async () => {
    const scratch = `dcnews-${Date.now().toString(36)}`;
    await L.createProject(page, scratch);
    try {
      const wrote = await postAs(page, `/projects/${scratch}/editor/file`, { path: "compose.yaml", content: SCRATCH_COMPOSE });
      assert(wrote.ok, `writing the scratch compose file answered ${wrote.status}`);
      const up = await postAs(page, `/projects/${scratch}/docker/compose`, { stack: "", action: "up" });
      assert(up.ok, `compose up on the scratch project answered ${up.status}`);
      await page.waitForSelector(`#project-${scratch} [data-chip-kind="docker"] .dc-term-icon.running`, { timeout: 90000 });
      // Read whatever is in the center away first, so what is counted is what
      // these two runs wrote.
      await postAs(page, "/notifications/read", { all: "1" });

      // Both at once, the case a shared target collapsed into one entry.
      const both = await Promise.all([
        postAs(page, `/projects/${NAME}/docker/compose`, { stack: "", action: "build" }),
        postAs(page, `/projects/${scratch}/docker/compose`, { stack: "", action: "build" }),
      ]);
      assert(both.every((r) => r.ok), `the two runs answered ${both.map((r) => r.status).join(", ")}`);
      // Two unread targets, which is what a shared target could never show.
      await waitNotify(page, (list) =>
        list.filter((n) => !n.read && n.targetId.startsWith("docker:")).length >= 2,
        90000, "two unread compose entries");

      await page.locator(".dc-notify-bell:visible").first().click();
      await page.waitForSelector(".dc-notify-menu.show", { timeout: 6000 });
      // The client holds a fresh entry for a short grace period before it
      // shows, so the rows are waited for, never read right off the click.
      await page.waitForFunction(() => document.querySelectorAll(".dc-notify-menu.show .dc-notify-list a").length >= 2, null, { timeout: 8000 });
      const lines = await page.locator(".dc-notify-menu.show .dc-notify-list a").allTextContents();
      const named = (project) => lines.some((line) => line.includes(project));
      assert(named(NAME), `no entry for ${NAME}: ${JSON.stringify(lines)}`);
      assert(named(scratch), `no entry for ${scratch}: ${JSON.stringify(lines)}`);
      // Each one leads to its own run's output, not to the other project's.
      const hrefs = await page.locator(".dc-notify-menu.show .dc-notify-list a").evaluateAll((nodes) => nodes.map((n) => n.getAttribute("href")));
      assert(hrefs.some((h) => h.includes(`/projects/${NAME}/docker/runs/`)), `the fixture's entry links at ${JSON.stringify(hrefs)}`);
      assert(hrefs.some((h) => h.includes(`/projects/${scratch}/docker/runs/`)), `the scratch entry links at ${JSON.stringify(hrefs)}`);
      await page.keyboard.press("Escape");
      await postAs(page, "/notifications/read", { all: "1" });
    } finally {
      await L.deleteProject(page, scratch);
      await page.waitForSelector(`#project-${scratch}`, { state: "detached", timeout: 180000 }).catch(() => {});
    }
  });

  await run("a failed run rings through the window, and its page reads the news away", async () => {
    const scratch = `dcfail-${Date.now().toString(36)}`;
    await L.createProject(page, scratch);
    try {
      const target = `docker:${scratch}`;
      const wrote = await postAs(page, `/projects/${scratch}/editor/file`, { path: "compose.yaml", content: SCRATCH_COMPOSE });
      assert(wrote.ok, `writing the scratch compose file answered ${wrote.status}`);
      await postAs(page, "/notifications/read", { all: "1" });
      const up = await postAs(page, `/projects/${scratch}/docker/compose`, { stack: "", action: "up" });
      assert(up.ok, `compose up on the scratch project answered ${up.status}`);
      // The success entry first: it proves the run is over (the file is free
      // to break) and opens the 30s window the failure has to ring through.
      await waitNotify(page, (list) =>
        list.some((n) => !n.read && n.targetId === target && /finished/i.test(n.title || "")),
        90000, "the success entry");

      // A compose file nobody can parse fails the next run within seconds.
      const broke = await postAs(page, `/projects/${scratch}/editor/file`, { path: "compose.yaml", content: "services: {\n" });
      assert(broke.ok, `breaking the compose file answered ${broke.status}`);
      const fail = await postAs(page, `/projects/${scratch}/docker/compose`, { stack: "", action: "up" });
      assert(fail.ok, `the failing compose up answered ${fail.status}`);
      // The failure is not swallowed as a follow-up of the fresh success: it
      // replaces it as the project's one unread entry.
      await waitNotify(page, (list) => {
        const unread = list.filter((n) => !n.read && n.targetId === target);
        return unread.length === 1 && /failed/i.test(unread[0].title || "");
      }, 60000, "the failure entry");

      // Opening the run's page is seeing the outcome: the project's docker
      // news reads itself, like an attach page does for a terminal.
      const entry = (await notifications(page)).find((n) => !n.read && n.targetId === target);
      const href = (entry || {}).url || "";
      assert(href.includes(`/projects/${scratch}/docker/runs/`), `the failure links at "${href}"`);
      await page.goto(`${BASE}${href}`, { waitUntil: "domcontentloaded" });
      await dismissUpdate(page);
      await page.waitForSelector("dc-docker-run", { timeout: 8000 });
      assert(/exit status|failed/i.test(await page.locator("[data-run-status]").textContent()), "the failed run does not say how it ended");
      await waitNotify(page, (list) => !list.some((n) => !n.read && n.targetId === target), 8000, "the news to read itself");
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await dismissUpdate(page);
    } finally {
      await L.deleteProject(page, scratch);
      await page.waitForSelector(`#project-${scratch}`, { state: "detached", timeout: 180000 }).catch(() => {});
    }
  });

  await run("the settings page puts the defaults back with one button", async () => {
    try {
      await emptyActions();
      const restore = page.locator("#settings-docker-actions [data-actions-restore]");
      assert(await restore.count() === 1, "the emptied page offers no way back");
      await restore.click();
      await page.waitForFunction(() => document.querySelectorAll("#settings-docker-actions [data-action-row]").length === 4, null, { timeout: 15000 });
      assert(await page.locator("#settings-docker-actions [data-actions-restore]").count() === 0, "the way back still stands with a full list");
      // It says so, and the list it shows is the default one again.
      const said = (await page.locator(".dc-toast").allTextContents().catch(() => [])).join(" ");
      assert(/default compose actions/i.test(said), `the button said "${said}"`);
      assert((await page.locator('#settings-docker-actions input[name="action_label"]').first().inputValue()) === "Compose up", "the restored list is not the default one");
    } finally {
      await restoreDefaults();
    }
  });

  await run("an emptied list leaves no buttons and offers the defaults back", async () => {
    try {
      await emptyActions();
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await dismissUpdate(page);
      await composeBtn(page).click();
      await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
      assert(await menuItem(page, "Compose up").count() === 0, "a command is offered from an empty list");
      assert(await page.locator(".dc-context-menu button", { hasText: /No compose actions configured/ }).count() === 1, "the empty list says nothing");
      assert(await menuItem(page, "web (2 addresses)").count() === 1, "the container went with the commands");
      await menuItem(page, "Restore the default actions").click();
      await page.waitForFunction(() => !document.querySelector(".dc-context-menu"), null, { timeout: 8000 });
      await sleep(800);
      await page.reload({ waitUntil: "domcontentloaded" });
      await dismissUpdate(page);
      await composeBtn(page).click();
      await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
      assert(await menuItem(page, "Compose up").count() === 1, "the defaults did not come back");
      await closeMenu(page);
    } finally {
      await restoreDefaults();
    }
  });

  await run("the link rules are a list on the settings page, with what each one finds", async () => {
    await page.goto(`${BASE}/settings/docker`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    const rows = page.locator("#settings-docker-links [data-link-row]");
    assert(await rows.count() === 1, `expected the one default rule, got ${await rows.count()}`);
    const label = await rows.first().locator('input[name="link_label"]').inputValue();
    assert(label === "traefik.http.routers.*.rule", `the default rule reads the label "${label}"`);
    // The default rule pins no scheme: the link is opened over the one the
    // page was reached over, which is the right answer whenever the proxy in
    // front of the app is the proxy in front of the cockpit.
    assert((await rows.first().locator('select[name="link_scheme"]').inputValue()) === "", "the default rule pins a scheme");
    // The preview runs the same matcher over the same cache the pages read,
    // so it names the fixture's own routed host.
    const preview = (await rows.first().locator("[data-link-preview]").textContent()).trim();
    assert(preview.includes(LINK_HOST), `the rule preview reads "${preview}"`);
    // A pattern nobody can compile is refused where it is typed, not stored
    // and quietly skipped later.
    await rows.first().locator('input[name="link_pattern"]').fill("(?P<host>");
    await Promise.all([
      page.waitForNavigation({ waitUntil: "domcontentloaded" }),
      page.click('#settings-docker button[type="submit"]:not([name])'),
    ]);
    const flash = await page.locator("[data-page-content]").textContent();
    assert(/regular expression/.test(flash), `the broken pattern was answered with "${flash.replace(/\s+/g, " ").trim().slice(0, 200)}"`);
    const stored = await page.locator('#settings-docker-links [data-link-row] input[name="link_pattern"]').first().inputValue();
    assert(stored !== "(?P<host>", "the broken pattern was stored anyway");
  });

  await run("an emptied rule list leaves the published ports, and the way back is one button", async () => {
    try {
      await page.goto(`${BASE}/settings/docker`, { waitUntil: "domcontentloaded" });
      await dismissUpdate(page);
      const count = await page.locator("#settings-docker-links [data-link-row]").count();
      for (let i = 0; i < count; i++) await page.locator("#settings-docker-links [data-link-remove]").first().click();
      await Promise.all([
        page.waitForNavigation({ waitUntil: "domcontentloaded" }),
        page.click('#settings-docker button[type="submit"]:not([name])'),
      ]);
      assert(await page.locator("#settings-docker-links [data-link-row]").count() === 0, "the emptied rules came back");

      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await dismissUpdate(page);
      await page.waitForSelector(`#project-${NAME} [data-chip-kind="docker"]`, { timeout: 8000 });
      await openMenuOn(page, chip(page));
      assert(await menuItem(page, "Open :18088").count() === 1, "the published port went with the rules");
      assert(await menuItem(page, `Open ${LINK_HOST}`).count() === 0, "a routed host is offered from an empty rule list");
      await closeMenu(page);
      // One address left, so the project menu names it directly instead of
      // drilling into a list of one.
      await composeBtn(page).click();
      await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
      assert(await menuItem(page, "Open :18088").count() === 1, "a container with one address does not stand in the project menu");
      assert(await menuItem(page, "web (1 addresses)").count() === 0, "a container with one address drills in");
      await closeMenu(page);

      await page.goto(`${BASE}/settings/docker`, { waitUntil: "domcontentloaded" });
      await dismissUpdate(page);
      const restore = page.locator("#settings-docker-links [data-link-restore]");
      assert(await restore.count() === 1, "the emptied rules offer no way back");
      await restore.click();
      await page.waitForFunction(() => document.querySelectorAll("#settings-docker-links [data-link-row]").length === 1, null, { timeout: 15000 });
      const back = await page.locator('#settings-docker-links input[name="link_label"]').first().inputValue();
      assert(back === "traefik.http.routers.*.rule", `the restored rule reads "${back}"`);
    } finally {
      // The same route the button takes, so a failure in the middle leaves the
      // instance with its default rule like every other check found it.
      await postAs(page, "/docker/link-rules/restore", {}).catch(() => {});
    }
  });

  await sleep(200);
});
