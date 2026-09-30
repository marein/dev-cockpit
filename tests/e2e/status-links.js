#!/usr/bin/env node
const L = require("./lib");
const { assert, sleep, BASE, dismissUpdate } = L;

// The links chip of the status line (dc-status-links, GET /docker/links): while
// a stack runs, the line carries "N links · M projects" behind a green light
// right next to the server meters, behind a separator and before the gap that
// pushes the version to the right. The chip is the button of a dropup listing
// every address the running stacks answer on, by project and stack, the routed
// hosts first and the published ports ascending, each in a new tab. The groups
// stand in the app's project order (@dc/project-sort, the pick on the projects
// page) and re-sort the moment the pick changes, with no reload. The project of
// the page starts unfolded, on a page without one the project used last; the
// folds a person sets are the browser's (localStorage dc-status-links-open),
// they survive opens, swaps, reloads and page changes, and a name whose
// project lost its links is dropped from the stored set. The chip follows the
// daemon over the SSE "docker" event, the same way the projects page does, and
// is gone entirely while no stack runs. A coarse pointer never sees it
// (dc-fine-only), the host meters beside it stay.
//
// The runner brings its own stacks: two scratch projects with a compose file
// each, one publishing a port and carrying a traefik router label the default
// link rule reads, one with the label alone, composed up through the same
// POST the compose menu uses. It needs an instance that reaches the daemon
// plus the docker CLI and whose projects dir runs no stack of its own at the
// start, because the first check reads the chip's absence. Both projects are
// deleted at the end, which brings their stacks down.
//
// Gotchas:
// - the element hides through the hidden attribute, which style.css makes
//   win over any display rule, so checks read computed display.
// - the fold a person set has to survive the swap the docker event causes,
//   which is why one check dispatches the DOM event the stream client would.
// - the menu's rows stand in the DOM while it is closed, so the order is read
//   without opening it; what a fold shows is read with it open.
// - a wide viewport with touch is what makes "(pointer: coarse)" true beside
//   a visible status line; the phone's viewport hides the whole line.
// - a routed address that does not fit loses its middle and never its end,
//   the split the container menus use (splitAddress in @dc/docker, the tail
//   from the last dot of the host, capped at 24): the second scratch project
//   carries a route longer than the menu for that, under a router name that
//   sorts after its short one, because a rule reads its labels in key order.

const CHIP = ".dc-status dc-status-links";
const TOGGLE = `${CHIP} [data-links-toggle]`;
const MENU = `${CHIP} .dropdown-menu`;
const HOST = new URL(BASE).hostname;
const FOLDS_KEY = "dc-status-links-open";

const chipShown = (page) => page.$eval(CHIP, (el) => getComputedStyle(el).display !== "none");
const summary = (page) => page.$eval(`${CHIP} [data-links-summary]`, (el) => el.textContent.trim());
const waitChip = (page, shown, timeout) => page.waitForFunction(({ sel, shown }) => {
  const el = document.querySelector(sel);
  return Boolean(el) && (getComputedStyle(el).display !== "none") === shown;
}, { sel: CHIP, shown }, { timeout });
const waitSummary = (page, text, timeout) => page.waitForFunction(({ sel, text }) =>
  document.querySelector(sel)?.textContent.trim() === text, { sel: `${CHIP} [data-links-summary]`, text }, { timeout });
const folds = (page) => page.evaluate((key) => JSON.parse(localStorage.getItem(key) || "null"), FOLDS_KEY);

// The menu's rows as the runner reads them: the project, whether it stands
// unfolded, its count, and the addresses in the order they are listed.
const rows = (page) => page.$$eval(`${MENU} [data-links-project]`, (els) => els.map((el) => ({
  name: el.dataset.linksProject,
  open: el.querySelector("[data-links-fold]").getAttribute("aria-expanded") === "true",
  chevron: el.querySelector("[data-links-chevron]").className,
  count: el.querySelector("[data-links-count]").textContent.trim(),
  stacksShown: getComputedStyle(el.querySelector("[data-links-stacks]")).display !== "none",
  links: [...el.querySelectorAll("a")].map((a) => ({
    text: a.textContent.trim(), href: a.href, raw: a.getAttribute("href"), target: a.getAttribute("target"),
  })),
})));
const order = async (page) => (await rows(page)).map((r) => r.name).join(",");

async function openMenu(page) {
  await page.click(TOGGLE);
  await page.waitForSelector(`${MENU}.show`, { timeout: 4000 });
}

async function closeMenu(page) {
  await page.keyboard.press("Escape");
  await page.waitForFunction((sel) => !document.querySelector(`${sel}.show`), MENU, { timeout: 4000 });
}

async function pickSort(page, mode) {
  await page.click("[data-project-sort-toggle]");
  await page.waitForSelector(`[data-project-sort-option="${mode}"]`, { state: "visible", timeout: 4000 });
  await page.click(`[data-project-sort-option="${mode}"]`);
  await sleep(200);
}

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

const composeFile = (host, port, long) => [
  "services:",
  "  web:",
  "    image: nginx:alpine",
  ...(port ? [`    ports: ["${port}:80"]`] : []),
  "    labels:",
  '      traefik.enable: "true"',
  `      traefik.http.routers.${host.replace(/\W/g, "")}.rule: "Host(\`${host}\`)"`,
  ...(long ? [`      traefik.http.routers.zlong.rule: "Host(\`${long.host}\`) && PathPrefix(\`${long.path}\`)"`] : []),
  "",
].join("\n");

// prepare creates the project and its compose file, composeUp brings it up
// through the same POST the compose menu sends. The create helper navigates,
// so the two halves stand apart: a check that reads the chip following the
// event marks the page in between.
async function prepare(page, name, host, port, long) {
  await L.createProject(page, name);
  const wrote = await postAs(page, `/projects/${name}/editor/file`, { path: "compose.yaml", content: composeFile(host, port, long) });
  assert(wrote.ok, `writing the compose file of ${name} answered ${wrote.status}`);
}

async function composeUp(page, name) {
  const up = await postAs(page, `/projects/${name}/docker/compose`, { stack: "", action: "up" });
  assert(up.ok, `compose up on ${name} answered ${up.status}`);
}

async function takeDown(page, name) {
  await L.deleteProject(page, name);
  await page.waitForSelector(`#project-${name}`, { state: "detached", timeout: 180000 });
}

const stamp = Date.now().toString(36);
const A = `dclinks-a-${stamp}`;
const B = `dclinks-b-${stamp}`;
const HOST_A = `${A}.test`;
const HOST_B = `${B}.test`;
const PORT_A = 18089;
const LONG = { host: `api.staging.a-subdomain-name-far-longer-than-the-menu-can-show.${B}.test`, path: "/some/long/path/for/the/menu" };
const LONG_ADDR = LONG.host + LONG.path;

L.runFeature("STATUS-LINKS", async ({ browser, page, run }) => {
  let upA = false;
  let upB = false;
  try {
    await run("the chip is absent while no stack runs", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await dismissUpdate(page);
      assert((await L.waitUpgraded(page, ["dc-status-links", "dc-host-status"], 8000)).length === 0, "dc-status-links not upgraded");
      assert(!(await chipShown(page)), "the chip shows with nothing running");
      const line = await page.$eval(".dc-status", (el) => ({
        shown: getComputedStyle(el).display !== "none",
        height: el.getBoundingClientRect().height,
        order: [...el.children].map((c) => c.localName + (c.className ? "." + String(c.className).split(" ")[0] : "")).join(" "),
      }));
      assert(line.shown && line.height === 26, `the status line is ${line.height}px high`);
      assert(/^dc-host-status\.dropdown dc-status-links\.dropdown span\.dc-status-gap span\.dc-status-item$/.test(line.order),
        `the line does not read meters, links, gap, version: ${line.order}`);
      return line.order;
    });

    await run("a stack coming up brings the chip in next to the meters over the docker event", async () => {
      await prepare(page, A, HOST_A, PORT_A);
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await dismissUpdate(page);
      assert(!(await chipShown(page)), "the chip shows for a stack that is not up yet");
      await page.evaluate(() => { window.__statusLinksPage = true; });
      await composeUp(page, A);
      upA = true;
      await waitChip(page, true, 90000);
      await waitSummary(page, "2 links · 1 project", 15000);
      assert(await page.evaluate(() => window.__statusLinksPage === true), "the page reloaded instead of following the event");
      const chip = await page.$eval(TOGGLE, (el) => ({
        height: el.getBoundingClientRect().height,
        led: !!el.querySelector(".status-dot"),
        green: el.classList.contains("status-green"),
        label: el.getAttribute("aria-label"),
      }));
      assert(chip.led && chip.green, `the chip has no green light: ${JSON.stringify(chip)}`);
      assert(chip.height <= 22, `the chip is ${chip.height}px high in a 26px line`);
      assert(chip.label === "Links of running stacks", `chip label "${chip.label}"`);
      const line = await page.evaluate(() => {
        const box = (sel) => document.querySelector(sel).getBoundingClientRect();
        const sep = document.querySelector(".dc-status dc-status-links .dc-status-sep");
        return {
          height: box(".dc-status").height,
          meters: box(".dc-status [data-host-toggle]"),
          sep: sep && { ...sep.getBoundingClientRect().toJSON(), shown: getComputedStyle(sep).display !== "none" },
          chip: box(".dc-status [data-links-toggle]"),
          version: box(".dc-status > .dc-status-item"),
          width: window.innerWidth,
        };
      });
      assert(line.height === 26, `the status line grew to ${line.height}px`);
      assert(line.sep && line.sep.shown && line.sep.width === 1 && line.sep.height >= 10 && line.sep.height <= 14,
        `no separator between meters and chip: ${JSON.stringify(line.sep)}`);
      assert(line.meters.right < line.sep.left && line.sep.right < line.chip.left,
        `the separator does not stand between meters and chip: ${JSON.stringify(line)}`);
      assert(line.chip.left - line.meters.right < 40, `the chip stands ${Math.round(line.chip.left - line.meters.right)}px away from the meters`);
      assert(line.chip.right < line.width / 2 && line.version.left > line.width / 2, `the chip is not left of the gap: ${JSON.stringify(line)}`);
    });

    await run("the menu lists the stack's addresses, host first, in new tabs", async () => {
      await openMenu(page);
      const list = await rows(page);
      assert(list.length === 1 && list[0].name === A, `rows ${JSON.stringify(list.map((r) => r.name))}`);
      assert(list[0].open && list[0].stacksShown && list[0].count === "2", `the only project is folded: ${JSON.stringify(list[0])}`);
      assert(/ti-chevron-down/.test(list[0].chevron), `an open row wears ${list[0].chevron}`);
      const links = list[0].links;
      assert(links.map((l) => l.text).join(",") === `${HOST_A},:${PORT_A}`, `addresses ${links.map((l) => l.text)}`);
      assert(links.every((l) => l.target === "_blank"), "a link opens in this tab");
      assert(links[0].raw === `//${HOST_A}`, `the routed host is written ${links[0].raw}`);
      // A published port keeps http by the container's port, only the host is
      // the page's own.
      assert(links[1].href === `http://${HOST}:${PORT_A}/`, `the port resolves to ${links[1].href}`);
      const inside = await page.$eval(MENU, (el) => {
        const box = el.getBoundingClientRect();
        return box.top >= 0 && box.bottom <= window.innerHeight && box.left >= 0 && box.right <= window.innerWidth && box.width >= 200;
      });
      assert(inside, "the menu hangs outside the window");
      await closeMenu(page);
    });

    await run("a second project groups under its own row, the page's project is the open one", async () => {
      await prepare(page, B, HOST_B, 0, LONG);
      await composeUp(page, B);
      upB = true;
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await dismissUpdate(page);
      await waitSummary(page, "4 links · 2 projects", 90000);
      assert((await order(page)) === `${A},${B}`, `rows ${await order(page)}`);
      await openMenu(page);
      let list = await rows(page);
      assert(list[0].open && !list[1].open && !list[1].stacksShown && list[1].count === "2", `fold state ${JSON.stringify(list.map((r) => [r.open, r.count]))}`);
      await closeMenu(page);

      await page.goto(`${BASE}/projects/${B}/editor`, { waitUntil: "domcontentloaded" });
      await dismissUpdate(page);
      await page.waitForSelector(TOGGLE, { timeout: 15000 });
      assert((await order(page)) === `${A},${B}`, `on the editor page the rows read ${await order(page)}`);
      await openMenu(page);
      list = await rows(page);
      assert(!list[0].open && list[1].open, `the editor's project is not the open one: ${JSON.stringify(list.map((r) => r.open))}`);
      assert(list[1].links.map((l) => l.text).join(",") === `${HOST_B},${LONG_ADDR}`, `the second project's addresses ${list[1].links.map((l) => l.text)}`);
      await closeMenu(page);

      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await dismissUpdate(page);
      await page.waitForSelector(TOGGLE, { timeout: 15000 });
      await openMenu(page);
      list = await rows(page);
      assert(list.map((r) => r.name).join(",") === `${A},${B}` && !list[0].open && list[1].open,
        `the project used last is not the open one: ${JSON.stringify(list.map((r) => [r.name, r.open]))}`);
      await closeMenu(page);
    });

    await run("a long route keeps its end, stays inside the menu and links whole", async () => {
      await openMenu(page);
      const chip = await page.$eval(`${MENU} a[data-links-route="${LONG_ADDR}"]`, (a) => {
        const menu = a.closest(".dropdown-menu").getBoundingClientRect();
        const box = a.getBoundingClientRect();
        const head = a.querySelector(".dc-menu-label-head");
        const tail = a.querySelector(".dc-menu-label-tail");
        return {
          href: a.getAttribute("href"), title: a.title, text: a.textContent.trim(), lines: box.height,
          inside: box.left >= menu.left && box.right <= menu.right,
          headText: head?.textContent, headCut: head ? head.scrollWidth > head.clientWidth + 1 : null,
          tailText: tail?.textContent, tailWhole: tail ? tail.scrollWidth <= tail.clientWidth + 1 && tail.getBoundingClientRect().right <= box.right : null,
        };
      });
      assert(chip.href === `//${LONG_ADDR}`, `the long route links to ${chip.href}`);
      assert(chip.title === `Open ${LONG_ADDR}` && chip.text === LONG_ADDR, `the chip lost the address: ${JSON.stringify(chip)}`);
      assert(chip.inside && chip.lines <= 24, `the chip spills out of the menu: ${JSON.stringify(chip)}`);
      assert(chip.headText + chip.tailText === LONG_ADDR && chip.tailText.length === 24 && LONG_ADDR.endsWith(chip.tailText),
        `the split is not head plus a 24 character tail: ${JSON.stringify(chip)}`);
      assert(chip.headCut === true, `the head does not ellipsize: ${JSON.stringify(chip)}`);
      assert(chip.tailWhole === true, `the tail is not fully visible: ${JSON.stringify(chip)}`);
      const short = await page.$eval(`${MENU} a[data-links-route="${HOST_B}"]`, (a) => ({
        head: a.querySelector(".dc-menu-label-head")?.textContent, tail: a.querySelector(".dc-menu-label-tail")?.textContent,
        cut: a.querySelector(".dc-menu-label-head").scrollWidth > a.querySelector(".dc-menu-label-head").clientWidth + 1,
      }));
      assert(short.head + short.tail === HOST_B && short.tail === ".test" && !short.cut, `the short route is split wrong: ${JSON.stringify(short)}`);
      const port = await page.$eval(`${MENU} a[href$=":${PORT_A}/"], ${MENU} a[href$=":${PORT_A}"]`, (a) => ({
        route: a.hasAttribute("data-links-route"), spans: a.querySelectorAll(".dc-menu-label-head, .dc-menu-label-tail").length, text: a.textContent.trim(),
      }));
      assert(!port.route && port.spans === 0 && port.text === `:${PORT_A}`, `the port chip changed: ${JSON.stringify(port)}`);
      await closeMenu(page);
    });

    await run("the order follows the project sort and changes live", async () => {
      assert((await order(page)) === `${A},${B}`, `under the name sort the rows read ${await order(page)}`);
      await pickSort(page, "recent");
      assert((await order(page)) === `${B},${A}`, `under the recent sort the rows read ${await order(page)}`);
      const board = await page.$$eval(".projects-card [id^='project-dclinks-']", (els) => els.map((el) => el.id.replace("project-", "")).join(","));
      assert(board === `${B},${A}`, `the board reads ${board} while the chip reads ${await order(page)}`);
      await pickSort(page, "active");
      assert((await order(page)) === `${A},${B}`, `under the active sort the rows read ${await order(page)}`);
      await pickSort(page, "alpha");
      assert((await order(page)) === `${A},${B}`, `back under the name sort the rows read ${await order(page)}`);
      assert(await page.evaluate(() => window.__statusLinksPage === undefined), "a sort change reloaded the page");
    });

    await run("a click unfolds a folded project, the fold is stored and survives the next swap", async () => {
      await openMenu(page);
      await page.click(`${MENU} [data-links-project="${A}"] [data-links-fold]`);
      await sleep(150);
      assert(await page.$eval(`${MENU}`, (el) => el.classList.contains("show")), "the click closed the menu");
      let list = await rows(page);
      assert(list[0].name === A && list[0].open && list[0].stacksShown && list[1].open, `unfold ${JSON.stringify(list.map((r) => [r.name, r.open]))}`);
      assert(list[0].links.map((l) => l.text).join(",") === `${HOST_A},:${PORT_A}`, `unfolded addresses ${list[0].links.map((l) => l.text)}`);
      assert(JSON.stringify(await folds(page)) === JSON.stringify([A, B]), `stored folds ${JSON.stringify(await folds(page))}`);
      await page.click(`${MENU} [data-links-project="${B}"] [data-links-fold]`);
      await sleep(150);
      list = await rows(page);
      assert(!list[1].open && !list[1].stacksShown && /ti-chevron-right/.test(list[1].chevron), `fold ${JSON.stringify(list[1])}`);
      assert(JSON.stringify(await folds(page)) === JSON.stringify([A]), `stored folds ${JSON.stringify(await folds(page))}`);
      await page.evaluate(() => document.dispatchEvent(new CustomEvent("dc:docker")));
      await sleep(1500);
      list = await rows(page);
      assert(await page.$eval(`${MENU}`, (el) => el.classList.contains("show")), "the swap closed the menu");
      assert(list[0].open && list[0].stacksShown && !list[1].open, `the folds did not survive the swap: ${JSON.stringify(list.map((r) => [r.name, r.open]))}`);
      await closeMenu(page);
    });

    await run("the folds survive a reload and a page change", async () => {
      await page.reload({ waitUntil: "domcontentloaded" });
      await dismissUpdate(page);
      await page.waitForSelector(TOGGLE, { timeout: 15000 });
      await openMenu(page);
      let list = await rows(page);
      assert(list.map((r) => [r.name, r.open]).join(";") === `${A},true;${B},false`,
        `after a reload the folds read ${JSON.stringify(list.map((r) => [r.name, r.open]))}`);
      assert(list[0].stacksShown && !list[1].stacksShown, "the stacks do not follow the stored folds");
      await closeMenu(page);
      await page.goto(`${BASE}/projects/${B}/editor`, { waitUntil: "domcontentloaded" });
      await dismissUpdate(page);
      await page.waitForSelector(TOGGLE, { timeout: 15000 });
      list = await rows(page);
      assert(list.map((r) => [r.name, r.open]).join(";") === `${A},true;${B},false`,
        `on the editor page the stored folds lost to the page's default: ${JSON.stringify(list.map((r) => [r.name, r.open]))}`);
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await dismissUpdate(page);
      await page.waitForSelector(TOGGLE, { timeout: 15000 });
    });

    await run("a coarse pointer on a wide screen keeps the meters and drops the chip", async () => {
      const ctx = await browser.newContext({ ignoreHTTPSErrors: true, hasTouch: true, isMobile: true, viewport: { width: 1360, height: 900 } });
      try {
        const wide = await ctx.newPage();
        await L.login(wide);
        await wide.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
        await dismissUpdate(wide);
        await wide.waitForSelector(".dc-status", { timeout: 8000 });
        const seen = await wide.evaluate(() => ({
          coarse: matchMedia("(pointer: coarse)").matches,
          line: getComputedStyle(document.querySelector(".dc-status")).display !== "none",
          meters: getComputedStyle(document.querySelector(".dc-status dc-host-status")).display !== "none",
          chip: getComputedStyle(document.querySelector(".dc-status dc-status-links")).display,
        }));
        assert(seen.coarse, "the wide touch context is not a coarse pointer");
        assert(seen.line && seen.meters, `the status line or its meters are gone: ${JSON.stringify(seen)}`);
        assert(seen.chip === "none", `the chip shows on a coarse pointer: ${JSON.stringify(seen)}`);
      } finally {
        await ctx.close().catch(() => {});
      }
    });

    await run("the stacks going down take the chip away and drop their folds", async () => {
      await openMenu(page);
      await page.click(`${MENU} [data-links-project="${B}"] [data-links-fold]`);
      await sleep(150);
      await closeMenu(page);
      assert(JSON.stringify(await folds(page)) === JSON.stringify([A, B]), `stored folds ${JSON.stringify(await folds(page))}`);
      await takeDown(page, B);
      upB = false;
      await waitSummary(page, "2 links · 1 project", 30000);
      assert(JSON.stringify(await folds(page)) === JSON.stringify([A]), `a gone project stays stored: ${JSON.stringify(await folds(page))}`);
      await takeDown(page, A);
      upA = false;
      await waitChip(page, false, 30000);
      assert(JSON.stringify(await folds(page)) === "[]", `a gone project stays stored: ${JSON.stringify(await folds(page))}`);
    });
  } finally {
    if (upB) await takeDown(page, B).catch(() => {});
    if (upA) await takeDown(page, A).catch(() => {});
    for (const name of [A, B]) await L.deleteProject(page, name).catch(() => {});
    await page.evaluate((key) => localStorage.removeItem(key), FOLDS_KEY).catch(() => {});
  }
});
