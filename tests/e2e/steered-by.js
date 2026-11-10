const https = require("https");
const path = require("path");
const L = require("./lib");
const { assert, BASE, sleep, dismissUpdate } = L;
const { guestPage } = require("./project-share-guest");

// Who steers a coder: the purple steered mark names the assistant holding the
// job, wherever the cockpit draws it, for the owner alone. The icon's tooltip
// names the assistant after the coder, in the tab strip, the phone's sheet, the
// editor's picker and terminal tab, the project chips and the attach pages. The row
// menus of a steered coder (strip, pane head, chip, editor picker row and tab, the
// phone row's three dots) carry an entry of the sparkles and the assistant's
// name beside Release, opening the thread, its icon sized, aligned and
// coloured like the menu's other icons. No page shows the words "Steered by",
// only tooltips say them. From a tablet's width up the coder's head, the
// split's head and a pane head stand the sparkles and the assistant's name
// after the coder (data-steered-label, pulled from /coders/:id/steered-by), on
// a desktop the strip row too; a phone's head never does. The label gives way
// first: the coder's name is cut only once the label is down to its
// sparkles. On a phone, where no row of the coder stands on its page, a tap on
// the purple mark in the head (dc-steered-mark with `menu`) opens a menu
// holding the same entry. The name is the one the assistants list shows, cut
// by the menu, never breaking a phone's width. A steer from the page belongs
// to the assistant the dialog picked and shows the same way. A rename, a
// release and a deleted assistant move tooltip, label and menu entries live on
// the terminals event without a reload, and an unsteered coder shows none of
// it. A guest of a link (`/share/<token>/...`) sees the purple mark and never
// the name, the label or the thread, a tap on its head mark opens nothing.
//
// Instance: like assistant.js's (tests/e2e/fakes first on PATH for the fake
// claude, a scratch HOME, its own TMUX_TMPDIR, --projects-dir and
// --state-dir), served over https so the guest's raw requests can read it.
// SHOTS_DIR saves the phone's head and its menu per engine. About three
// minutes per engine.

const TAG = Date.now().toString(36).slice(-5);
const PROJECT = `zzsb${TAG}`;
const ORIGIN = new URL(BASE).origin;
const SHOTS = process.env.SHOTS_DIR || "";
const LONG = `Steering lead ${TAG} with a name long enough to be cut on a phone`;
const RENAMED = `Renamed lead ${TAG}`;
const OTHER = `Second lead ${TAG}`;
// The fake claude names its session after the first prompt, which the cockpit
// cuts to the longest name a head shows, so the prompt makes the name long.
const CODER = `Review the steered work ${TAG} under a coder name that fills the head`;

function fetchRaw(url) {
  return new Promise((resolve, reject) => {
    https.get(url, { rejectUnauthorized: false }, (res) => {
      let raw = "";
      res.on("data", (chunk) => { raw += chunk; });
      res.on("end", () => resolve({ status: res.statusCode, body: raw }));
    }).on("error", reject);
  });
}

async function post(page, url, body) {
  return page.evaluate(async ([to, values]) => {
    const res = await fetch(to, {
      method: "POST",
      headers: { "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]').content, Accept: "application/json" },
      body: new URLSearchParams(values),
    });
    return { status: res.status, text: await res.text() };
  }, [url, body]);
}

async function postJSON(page, url, body) {
  const res = await post(page, url, body);
  assert(res.status === 200, `POST ${url} answered ${res.status}: ${res.text.slice(0, 200)}`);
  return JSON.parse(res.text || "{}");
}

const tab = (id) => `terminal-tabs .terminal-tab[data-tab-id="${id}"]`;
const headMark = ".dc-work-head dc-steered-mark[menu]";
const headIcon = `${headMark} .dc-term-icon`;

// wordless reads whether the page shows the words "Steered by" anywhere, a
// tooltip may say them.
const wordless = (page) => page.evaluate(() => !/steered by/i.test(document.body.innerText));

// labelOf reads the label beside a coder's name in a scope: whether it shows,
// what it says, its thread and whether its name is cut.
const labelOf = (page, scope) => page.evaluate((s) => {
  const el = [...document.querySelectorAll(`${s} [data-steered-label]`)].find((l) => !l.closest("[hidden]"));
  if (!el) return { shown: false, text: "" };
  const box = el.getBoundingClientRect();
  const name = el.querySelector("span");
  return {
    shown: box.width > 0 && getComputedStyle(el).display !== "none", text: el.innerText.trim(), href: el.getAttribute("href") || "", title: el.title,
    width: Math.round(box.width), cut: name.scrollWidth > name.clientWidth, sparkles: !!el.querySelector(".ti-sparkles"),
  };
}, scope);

// giveWay reads a head's coder name and its label: the name is cut only
// once the label is down to its sparkles. A cut is measured against the
// text's natural width, since an ellipsis shows at a fraction of a pixel
// that scrollWidth rounds away.
const giveWay = (page, head, name) => page.evaluate(([h, n]) => {
  const cut = (el) => {
    const probe = el.cloneNode(true);
    probe.style.cssText = "position: absolute; visibility: hidden; width: auto; min-width: 0; max-width: none";
    el.parentNode.appendChild(probe);
    const natural = probe.getBoundingClientRect().width;
    probe.remove();
    return natural - el.getBoundingClientRect().width > 0.01;
  };
  const scope = document.querySelector(h);
  const title = [...scope.querySelectorAll(n)].find((el) => el.getBoundingClientRect().width > 0);
  const label = scope.querySelector("[data-steered-label]");
  const text = label?.querySelector("span");
  return {
    nameCut: cut(title), labelCut: !!text && cut(text),
    label: label ? Math.round(label.getBoundingClientRect().width) : 0, icon: label ? Math.round(label.querySelector(".ti").getBoundingClientRect().width) : 0,
  };
}, [head, name]);

// headOf measures a phone's head: the coder's name stands right after the mark
// and, at the longest name the cockpit gives a coder, uncut.
const headOf = (page) => page.evaluate(() => {
  const head = document.querySelector(".dc-work-head");
  const mark = [...head.querySelectorAll("dc-steered-mark")].find((m) => getComputedStyle(m).display !== "none" && !m.hidden).getBoundingClientRect();
  const holder = [...head.querySelectorAll("dc-live-name, .dc-coarse-only")].find((n) => getComputedStyle(n).display !== "none");
  const name = holder.getBoundingClientRect();
  const h1 = holder.querySelector("h1");
  return {
    before: Math.round(name.left - mark.right),
    cut: h1.scrollWidth > h1.clientWidth, width: Math.round(h1.getBoundingClientRect().width), name: h1.textContent.trim(),
    scroll: document.documentElement.scrollWidth, inner: innerWidth,
  };
});

const titleOf = (page, selector) => page.evaluate((s) => {
  const el = document.querySelector(s);
  return el ? { title: el.getAttribute("title") || "", by: el.dataset.steeredBy || "", url: el.dataset.steeredUrl || "", steered: el.classList.contains("steered") } : null;
}, selector);

const menuLabels = (page) => page.$$eval(".dc-context-menu .dropdown-item", (els) => els.map((el) => el.textContent.trim()));

// entryOf reads the menu's entry of the steering assistant beside a plain
// entry of the same menu: icon, colour, size and how its icon centres on its
// text.
const entryOf = (page) => page.evaluate(() => {
  const read = (el) => {
    const icon = el.querySelector("i");
    const ib = icon.getBoundingClientRect();
    const tb = el.querySelector("span").getBoundingClientRect();
    return {
      text: el.textContent.trim(), href: el.getAttribute("href") || "", title: el.title, purple: el.classList.contains("text-purple"),
      color: getComputedStyle(icon).color, size: getComputedStyle(icon).fontSize, width: Math.round(ib.width), off: Math.abs((ib.top + ib.height / 2) - (tb.top + tb.height / 2)),
    };
  };
  const items = [...document.querySelectorAll(".dc-context-menu .dropdown-item")];
  const entry = items.find((el) => el.querySelector(".ti-sparkles"));
  const plain = items.find((el) => /^(Rename|Open project)$/.test(el.textContent.trim()));
  return { entry: entry ? read(entry) : null, plain: plain ? read(plain) : null };
});

// entryFits proves the entry is the bare name with the sparkles, an icon like
// the menu's other ones, and a link to the thread.
async function entryFits(page, title, id) {
  const { entry, plain } = await entryOf(page);
  assert(entry && entry.text === title && entry.href === `/assistants/${id}` && entry.title === `Steered by ${title}` && !entry.purple,
    `the entry reads ${JSON.stringify(entry)}`);
  assert(entry.off <= 1.5, `the entry's icon stands ${entry.off}px off its text`);
  if (plain) {
    assert(entry.color === plain.color && entry.size === plain.size && Math.abs(entry.width - plain.width) <= 1,
      `the entry's icon reads ${JSON.stringify(entry)} beside ${JSON.stringify(plain)}`);
  }
}

async function menuAt(page, selector, open = "right") {
  if (open === "tap") await page.tap(selector);
  else if (open === "click") await page.click(selector);
  else await page.click(selector, { button: "right" });
  await page.waitForSelector(".dc-context-menu", { timeout: 5000 });
  return menuLabels(page);
}

async function closeMenu(page) {
  await page.keyboard.press("Escape");
  await page.waitForSelector(".dc-context-menu", { state: "detached", timeout: 4000 }).catch(() => {});
}

// tapOpensNothing taps a head's mark and proves no menu came up and the mark
// offers none.
async function tapOpensNothing(page, mark = headMark) {
  await page.tap(`${mark} .dc-term-icon`);
  await sleep(400);
  const role = await page.$eval(mark, (el) => el.getAttribute("role"));
  assert(!(await page.$(".dc-context-menu")) && role === null, `a tap on the head's mark opened a menu or the mark reads role ${role}`);
}

// WebKit's network process can crash when a navigation cancels loads a page
// still has in flight (README, Notes): a goto then fails with an internal
// error or the context loses its cookies. Every navigation here waits for the
// page's own loads first; the event stream and a terminal's stream never end.
const inflight = new WeakMap();
function track(page) {
  if (inflight.has(page)) return page;
  const open = new Set();
  inflight.set(page, open);
  page.on("request", (r) => { if (!/\/(events|stream)$/.test(new URL(r.url()).pathname)) open.add(r); });
  page.on("requestfinished", (r) => open.delete(r));
  page.on("requestfailed", (r) => open.delete(r));
  return page;
}

async function settle(page) {
  await page.waitForLoadState("load", { timeout: 8000 }).catch(() => {});
  const open = inflight.get(track(page));
  const deadline = Date.now() + 8000;
  let quiet = 0;
  while (Date.now() < deadline) {
    quiet = open.size ? 0 : quiet || Date.now();
    if (quiet && Date.now() - quiet >= 300) return;
    await sleep(50);
  }
}

async function visit(page, url) {
  await settle(page);
  return page.goto(url, { waitUntil: "domcontentloaded" });
}

const stay = (page) => page.evaluate(() => { window.dcStay = Math.random(); return window.dcStay; });
const stayed = async (page, marker) => assert(await page.evaluate(() => window.dcStay) === marker, "the page reloaded");

L.runFeature("steered-by", async ({ engine, browser, page, run, mobilePage, bag }) => {
  const fx = { requests: [] };
  const phonePage = async () => track(await mobilePage());
  track(page);
  try {
    await run("setup: a project, two assistants, a coder and a shell", async () => {
      await L.createProject(page, PROJECT);
      fx.dir = await L.projectPath(page, PROJECT);
      await visit(page, `${BASE}/projects`);
      await dismissUpdate(page);
      fx.a = (await postJSON(page, "/assistants/new", { form: "new", coder: "claude" })).id;
      await postJSON(page, `/assistants/${fx.a}`, { form: "rename", title: LONG });
      fx.b = (await postJSON(page, "/assistants/new", { form: "new", coder: "claude" })).id;
      await postJSON(page, `/assistants/${fx.b}`, { form: "rename", title: OTHER });
      fx.coder = (await postJSON(page, "/coders/new", { name: `sb-${TAG}`, project: fx.dir, coder: "claude", automatic_approval: "on", prompt: CODER })).id;
      fx.shell = new URL(await L.createShell(page, fx.dir)).pathname.split("/").pop();
      assert(fx.a && fx.b && fx.coder && fx.shell, `the fixture reads ${JSON.stringify(fx)}`);
    });

    await run("an unsteered coder shows no name in its tooltip, no label, no menu entry and its head mark opens nothing", async () => {
      await visit(page, `${BASE}/coders/${fx.coder}`);
      await page.waitForSelector(`${tab(fx.coder)} [data-tab-icon]`, { timeout: 10000 });
      const icon = await titleOf(page, `${tab(fx.coder)} [data-tab-icon]`);
      assert(!icon.steered && !icon.title && !icon.by && !icon.url, `the tab's icon reads ${JSON.stringify(icon)}`);
      const labels = { head: await labelOf(page, ".dc-work-head"), row: await labelOf(page, tab(fx.coder)) };
      assert(!labels.head.shown && !labels.row.shown && !labels.head.text && !labels.row.text, `the labels read ${JSON.stringify(labels)}`);
      await menuAt(page, tab(fx.coder));
      const { entry } = await entryOf(page);
      const steer = await menuLabels(page);
      await closeMenu(page);
      assert(steer.includes("Steer") && !entry, `the menu reads ${steer}`);
      const mp = await phonePage();
      await visit(mp, `${BASE}/coders/${fx.coder}`);
      await dismissUpdate(mp);
      await mp.waitForSelector(headIcon, { state: "visible", timeout: 10000 });
      await tapOpensNothing(mp);
    });

    await run("a coder steered from the page names the assistant the dialog picked, live", async () => {
      await visit(page, `${BASE}/coders/${fx.coder}`);
      await page.waitForSelector(`${tab(fx.coder)} [data-tab-icon]`, { timeout: 10000 });
      const marker = await stay(page);
      await menuAt(page, tab(fx.coder));
      await page.locator(".dc-context-menu .dropdown-item", { hasText: /^Steer$/ }).click();
      await page.waitForSelector("#dc-steer-assistant", { timeout: 5000 });
      await page.selectOption("#dc-steer-assistant", fx.a);
      await page.click(".swal2-confirm");
      await page.waitForFunction(([s, want]) => document.querySelector(s)?.title === want, [`${tab(fx.coder)} [data-tab-icon]`, `Claude, steered by ${LONG}`], { timeout: 15000 });
      const icon = await titleOf(page, `${tab(fx.coder)} [data-tab-icon]`);
      assert(icon.steered && icon.by === LONG && icon.url === `/assistants/${fx.a}`, `the tab's icon reads ${JSON.stringify(icon)}`);
      await page.waitForFunction((want) => document.querySelector(".dc-work-head [data-steered-label]")?.innerText.trim() === want, LONG, { timeout: 15000 });
      const head = await labelOf(page, ".dc-work-head");
      const row = await labelOf(page, tab(fx.coder));
      assert(head.shown && head.sparkles && head.href === `/assistants/${fx.a}` && head.title === `Steered by ${LONG}`, `the head's label reads ${JSON.stringify(head)}`);
      assert(row.shown && row.sparkles && row.text === LONG && !row.href, `the row's label reads ${JSON.stringify(row)}`);
      assert(await wordless(page), "the page shows Steered by");
      await stayed(page, marker);
    });

    await run("the strip's menu names the assistant and opens its thread", async () => {
      const labels = await menuAt(page, tab(fx.coder));
      assert(labels.includes(LONG) && labels.includes("Release") && !labels.includes("Steer") && await wordless(page), `the menu reads ${labels}`);
      await entryFits(page, LONG, fx.a);
      const width = await page.evaluate(() => document.querySelector(".dc-context-menu").getBoundingClientRect().width);
      assert(width <= 26 * 16 + 1, `the menu grew to ${width}px with the name`);
      await settle(page);
      await Promise.all([
        page.waitForURL((u) => u.pathname === `/assistants/${fx.a}`, { timeout: 10000 }),
        page.locator(".dc-context-menu .dropdown-item", { hasText: LONG }).click(),
      ]);
    });

    await run("the coder's head stands the label after the coder, which gives way before the name and opens the thread", async () => {
      await visit(page, `${BASE}/coders/${fx.coder}`);
      await page.waitForSelector(".dc-work-head [data-steered-label]", { state: "visible", timeout: 10000 });
      const order = await page.evaluate(() => {
        const head = document.querySelector(".dc-work-head");
        return head.querySelector("h1").getBoundingClientRect().right <= head.querySelector("[data-steered-label]").getBoundingClientRect().left;
      });
      assert(order, "the label stands before the coder's name");
      const route = await page.evaluate((id) => fetch(`/coders/${id}/steered-by`).then((r) => r.text()), fx.coder);
      assert(route.includes("data-steered-label") && route.includes(`/assistants/${fx.a}`), `the label's route answered ${route.slice(0, 200)}`);
      let gave = false;
      for (const width of [1360, 1200, 1100, 1040, 992]) {
        await page.setViewportSize({ width, height: 900 });
        await sleep(150);
        const head = await giveWay(page, ".dc-work-head", "h1");
        const row = await giveWay(page, tab(fx.coder), ".terminal-tab-project > .text-truncate");
        assert(!head.nameCut || head.label <= head.icon + 1, `at ${width}px the head reads ${JSON.stringify(head)}`);
        assert(!row.nameCut || row.label <= row.icon + 1, `at ${width}px the row's project reads ${JSON.stringify(row)}`);
        gave ||= head.labelCut && !head.nameCut;
      }
      await page.setViewportSize({ width: 1360, height: 900 });
      assert(gave, "the label never gave way before the name");
      await settle(page);
      await Promise.all([
        page.waitForURL((u) => u.pathname === `/assistants/${fx.a}`, { timeout: 10000 }),
        page.click(".dc-work-head [data-steered-label]"),
      ]);
    });

    await run("the project chip, the editor's picker row and terminal tab name it in tooltip and menu", async () => {
      await visit(page, `${BASE}/projects`);
      const chipIcon = `#project-${PROJECT} [data-chip-id="${fx.coder}"] .dc-term-icon`;
      await page.waitForSelector(chipIcon, { timeout: 10000 });
      const onChip = await titleOf(page, chipIcon);
      assert(onChip.steered && onChip.title === `Claude, steered by ${LONG}` && onChip.url === `/assistants/${fx.a}`, `the chip's icon reads ${JSON.stringify(onChip)}`);
      const chipMenu = await menuAt(page, `#project-${PROJECT} [data-chip-id="${fx.coder}"] [data-chip-main]`);
      await entryFits(page, LONG, fx.a);
      assert(await wordless(page), "the projects page shows Steered by");
      await closeMenu(page);
      assert(chipMenu.includes(LONG) && chipMenu.includes("Release"), `the chip's menu reads ${chipMenu}`);

      await visit(page, `${BASE}/projects/${PROJECT}/editor`);
      await page.waitForSelector(".cm-editor, .editor-textarea", { state: "attached", timeout: 15000 });
      const pickRow = `[data-editor-sheet-body] [data-term-pick="${fx.coder}"]`;
      const read = page.waitForResponse((r) => new URL(r.url()).pathname.endsWith("/editor/terminals"), { timeout: 10000 }).catch(() => null);
      await page.click("[data-editor-term-status]");
      await read;
      await sleep(150);
      await page.waitForSelector(pickRow, { state: "visible", timeout: 10000 });
      const onRowIcon = await titleOf(page, `${pickRow} .dc-term-icon`);
      assert(onRowIcon.steered && onRowIcon.title === `Claude, steered by ${LONG}` && onRowIcon.url === `/assistants/${fx.a}`, `the picker row's icon reads ${JSON.stringify(onRowIcon)}`);
      const rowMenu = await menuAt(page, pickRow, "click");
      await entryFits(page, LONG, fx.a);
      assert(await wordless(page), "the editor shows Steered by");
      await closeMenu(page);
      assert(rowMenu.includes(LONG) && rowMenu.includes("Release"), `the picker row's menu reads ${rowMenu}`);
      await sleep(400);
      const editorTab = `.editor-tab[data-path="//terminal/${fx.coder}"]`;
      await L.openPicked(page, pickRow);
      await page.waitForSelector(`${editorTab}.active`, { timeout: 10000 });
      const onTab = await titleOf(page, `${editorTab} .dc-term-icon`);
      assert(onTab.steered && onTab.title === `Claude, steered by ${LONG}` && onTab.url === `/assistants/${fx.a}`, `the terminal tab's icon reads ${JSON.stringify(onTab)}`);
      const tabMenu = await menuAt(page, editorTab);
      await entryFits(page, LONG, fx.a);
      await closeMenu(page);
      assert(tabMenu.includes(LONG) && tabMenu.includes("Release"), `the terminal tab's menu reads ${tabMenu}`);
      await page.click(`${editorTab} .editor-tab-state`);
      await page.waitForSelector(editorTab, { state: "detached", timeout: 6000 });
    });

    await run("on a phone the head gives the coder's name its width, and a tap on the mark opens the entry", async () => {
      const mp = await phonePage();
      await visit(mp, `${BASE}/coders/${fx.coder}`);
      await dismissUpdate(mp);
      await mp.waitForSelector(`${headMark}[role="button"] .dc-term-icon.steered`, { state: "visible", timeout: 10000 });
      const label = await labelOf(mp, ".dc-work-head");
      assert(!label.shown && await wordless(mp), `the phone's head shows the label ${JSON.stringify(label)}`);
      const icon = await titleOf(mp, headIcon);
      assert(icon.title === `Claude, steered by ${LONG}`, `the head's icon reads ${JSON.stringify(icon)}`);
      const head = await headOf(mp);
      assert(!head.cut && head.name.endsWith("…") && head.before <= 12 && head.scroll <= head.inner, `the head reads ${JSON.stringify(head)}`);
      if (SHOTS) await mp.screenshot({ path: path.join(SHOTS, `steered-by-phone-head-${engine}.png`) });
      const labels = await menuAt(mp, `${headIcon}`, "tap");
      assert(labels.length === 1 && labels[0] === LONG, `the head's menu reads ${labels}`);
      await entryFits(mp, LONG, fx.a);
      const box = await mp.evaluate(() => document.querySelector(".dc-context-menu").getBoundingClientRect().right);
      assert(box <= 390, `the menu runs out to ${box}px`);
      if (SHOTS) await mp.screenshot({ path: path.join(SHOTS, `steered-by-phone-menu-${engine}.png`) });
      await settle(mp);
      await Promise.all([
        mp.waitForURL((u) => u.pathname === `/assistants/${fx.a}`, { timeout: 10000 }),
        mp.tap(".dc-context-menu .dropdown-item"),
      ]);
    });

    await run("on a phone the sheet's row menu names it too", async () => {
      const mp = await phonePage();
      await visit(mp, `${BASE}/coders/${fx.coder}`);
      await mp.tap('.dc-tabbar [data-ctx-area="terminals"]');
      const row = `[data-ctx-sheet]:not([hidden]) .terminal-tab[data-tab-id="${fx.coder}"]`;
      await mp.waitForSelector(`${row} [data-tab-menu]`, { state: "visible", timeout: 10000 });
      const icon = await titleOf(mp, `${row} [data-tab-icon]`);
      assert(icon.title === `Claude, steered by ${LONG}`, `the sheet's icon reads ${JSON.stringify(icon)}`);
      const label = await labelOf(mp, row);
      assert(!label.shown, `the sheet's row shows the label ${JSON.stringify(label)}`);
      const labels = await menuAt(mp, `${row} [data-tab-menu]`, "tap");
      assert(labels.includes(LONG) && await wordless(mp), `the sheet's menu reads ${labels}`);
      await entryFits(mp, LONG, fx.a);
      const box = await mp.evaluate(() => document.querySelector(".dc-context-menu").getBoundingClientRect().right);
      assert(box <= 390, `the menu runs out to ${box}px`);
      await closeMenu(mp);
    });

    await run("on a tablet the head stands the label after the coder's name, uncut, and the mark opens the entry", async () => {
      const tp = await phonePage();
      await tp.setViewportSize({ width: 834, height: 1194 });
      try {
        await visit(tp, `${BASE}/coders/${fx.coder}`);
        await tp.waitForSelector(".dc-work-head [data-steered-label]", { state: "visible", timeout: 10000 });
        const label = await labelOf(tp, ".dc-work-head");
        const head = await headOf(tp);
        assert(label.text === LONG && !head.cut && head.scroll <= head.inner && await wordless(tp), `the tablet's head reads ${JSON.stringify({ label, head })}`);
        const labels = await menuAt(tp, headIcon, "tap");
        assert(labels.length === 1 && labels[0] === LONG, `the tablet head's menu reads ${labels}`);
        await entryFits(tp, LONG, fx.a);
        await closeMenu(tp);
      } finally {
        await tp.setViewportSize({ width: 390, height: 844 });
      }
    });

    await run("a rename moves tooltip, label and menu entries live on desktop and phone", async () => {
      const mp = await phonePage();
      await visit(page, `${BASE}/coders/${fx.coder}`);
      await visit(mp, `${BASE}/coders/${fx.coder}`);
      await mp.waitForSelector(`${headMark}[role="button"]`, { timeout: 10000 });
      const desk = await stay(page);
      const phone = await stay(mp);
      await postJSON(page, `/assistants/${fx.a}`, { form: "rename", title: RENAMED });
      await page.waitForFunction(([s, w]) => document.querySelector(s)?.title === w, [`${tab(fx.coder)} [data-tab-icon]`, `Claude, steered by ${RENAMED}`], { timeout: 15000 });
      await page.waitForFunction((w) => document.querySelector(".dc-work-head [data-steered-label]")?.innerText.trim() === w, RENAMED, { timeout: 15000 });
      await mp.waitForFunction(([s, w]) => document.querySelector(s)?.title === w, [headIcon, `Claude, steered by ${RENAMED}`], { timeout: 15000 });
      const row = await labelOf(page, tab(fx.coder));
      assert(row.text === RENAMED, `the row's label reads ${JSON.stringify(row)}`);
      const labels = await menuAt(page, tab(fx.coder));
      await closeMenu(page);
      assert(labels.includes(RENAMED) && !labels.includes(LONG), `the menu reads ${labels}`);
      const onPhone = await menuAt(mp, headIcon, "tap");
      await closeMenu(mp);
      assert(onPhone.length === 1 && onPhone[0] === RENAMED, `the phone head's menu reads ${onPhone}`);
      assert(await wordless(page) && await wordless(mp), "a page shows Steered by");
      await stayed(page, desk);
      await stayed(mp, phone);
    });

    await run("a guest of the project sees the purple mark and never the assistant's name or thread", async () => {
      const res = await post(page, "/settings/sharing/new", [["amount", "1"], ["unit", "hours"], ["name", `sb-${TAG}`], ["project", PROJECT]]);
      fx.link = (res.text.match(/value="([^"]*\/share\/[^"]+)"/) || [])[1];
      assert(fx.link, `the link answered ${res.status}`);
      fx.base = new URL(fx.link).pathname;
      const leaks = (html) => [RENAMED, LONG, `/assistants/${fx.a}`, "data-steered", "steered-by", "Steered by", "steered by", "ti-sparkles"].filter((w) => html.includes(w));
      for (const p of [`/coders/${fx.coder}`, "/terminal-tabs", `/coders/${fx.coder}/steered`, `/projects/${PROJECT}/editor/terminals`, "/ctx/terminals"]) {
        const got = await fetchRaw(`${ORIGIN}${fx.base}${p}${p === "/terminal-tabs" ? `?path=${encodeURIComponent(`${fx.base}/coders/${fx.coder}`)}` : ""}`);
        assert(got.status === 200 && !leaks(got.body).length, `the guest's ${p} answered ${got.status} with ${leaks(got.body)}`);
      }
      const steered = await fetchRaw(`${ORIGIN}${fx.base}/coders/${fx.coder}/steered`);
      assert(/class="dc-term-icon running[^"]* steered/.test(steered.body), "the guest's mark lost its purple");
      const label = await fetchRaw(`${ORIGIN}${fx.base}/coders/${fx.coder}/steered-by`);
      assert(label.status === 404 && !leaks(label.body).length, `the guest's label route answered ${label.status}`);
      for (const options of [{}, { hasTouch: true, isMobile: true, viewport: { width: 390, height: 844 } }]) {
        const guest = await guestPage(browser, bag, fx, ORIGIN, options);
        try {
          const gp = track(guest.page);
          await visit(gp, `${fx.link}/coders/${fx.coder}`);
          const mark = options.isMobile ? headIcon : `terminal-tabs .terminal-tab[href="${fx.base}/coders/${fx.coder}"] [data-tab-icon]`;
          await gp.waitForSelector(mark, { state: "visible", timeout: 10000 });
          const icon = await titleOf(gp, mark);
          assert(icon.steered && !icon.by && !icon.url && !/steered by/i.test(icon.title), `the guest's icon reads ${JSON.stringify(icon)}`);
          assert(!(await gp.$("[data-steered-url], [data-steered-label], .dc-steered-slot")), "the guest's page stands the label or the thread");
          const html = await gp.content();
          assert(!leaks(html).length, `the guest's page carries ${leaks(html)}`);
          if (options.isMobile) {
            await tapOpensNothing(gp);
            continue;
          }
          const menu = await gp.click(mark, { button: "right" }).then(() => gp.waitForSelector(".dc-context-menu", { timeout: 1500 })).catch(() => null);
          if (menu) {
            const labels = await menuLabels(gp);
            await closeMenu(gp);
            assert(!labels.some((l) => /steer/i.test(l)), `the guest's menu reads ${labels}`);
          }
        } finally {
          await guest.ctx.close().catch(() => {});
        }
      }
      assert(fx.requests.every((r) => r.includes(fx.base) || /^GET \/(js|css)\//.test(r) || /^GET \/[\w.-]+\.(svg|png|json)$/.test(r)), `a guest request left the link: ${fx.requests.filter((r) => !r.includes(fx.base)).slice(0, 5)}`);
    });

    await run("a split view's pane head stands the tooltip, the label giving way first and the menu entry, and its phone head opens the entry", async () => {
      await visit(page, `${BASE}/coders/${fx.coder}`);
      const made = await page.evaluate(async (ids) => {
        const res = await fetch("/terminal-tabs/group", {
          method: "POST",
          headers: { "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]').content, "Content-Type": "application/json" },
          body: JSON.stringify({ ids }),
        });
        return res.json();
      }, [fx.coder, fx.shell]);
      fx.split = made.id || made.group || (made.url || "").split("/").pop().split("?")[0];
      assert(fx.split, `the group answered ${JSON.stringify(made)}`);
      await visit(page, `${BASE}/splits/${fx.split}`);
      const head = `.attach-split-pane[data-pane-id="${fx.coder}"] [data-pane-head]`;
      await page.waitForSelector(`${head} .dc-term-icon.steered`, { state: "visible", timeout: 10000 });
      assert(await wordless(page), "the split shows Steered by");
      const icon = await titleOf(page, `${head} dc-steered-mark .dc-term-icon`);
      assert(icon.title === `Claude, steered by ${RENAMED}`, `the pane's icon reads ${JSON.stringify(icon)}`);
      const pane = await page.evaluate((s) => {
        const h = document.querySelector(s);
        const name = h.querySelector("[data-pane-label]");
        return { mark: h.querySelector("dc-steered-mark").getBoundingClientRect().right, name: name.getBoundingClientRect().left, end: name.getBoundingClientRect().right };
      }, head);
      assert(pane.name - pane.mark <= 12, `the pane's name stands ${pane.name - pane.mark}px off its mark`);
      const label = await labelOf(page, head);
      assert(label.shown && label.text === RENAMED && label.href === `/assistants/${fx.a}`, `the pane's label reads ${JSON.stringify(label)}`);
      let gave = false;
      for (const width of [1360, 1200, 1100, 1040, 992]) {
        await page.setViewportSize({ width, height: 900 });
        await sleep(150);
        const read = await giveWay(page, head, "[data-pane-label]");
        assert(!read.nameCut || read.label <= read.icon + 1, `at ${width}px the pane head reads ${JSON.stringify(read)}`);
        gave ||= read.labelCut && !read.nameCut;
      }
      await page.setViewportSize({ width: 1360, height: 900 });
      assert(gave, "the pane's label never gave way before the name");
      const labels = await menuAt(page, head);
      assert(labels.includes(RENAMED) && labels.includes("Release"), `the pane's menu reads ${labels}`);
      await entryFits(page, RENAMED, fx.a);
      await closeMenu(page);
      await settle(page);
      await Promise.all([
        page.waitForURL((u) => u.pathname === `/assistants/${fx.a}`, { timeout: 10000 }),
        page.click(`${head} [data-steered-label]`),
      ]);
      const mp = await phonePage();
      await visit(mp, `${BASE}/splits/${fx.split}?focus=${fx.coder}`);
      const badge = `.dc-work-head dc-steered-mark[data-terminal-badge="${fx.coder}"]`;
      await mp.waitForSelector(`${badge}[role="button"] .dc-term-icon`, { state: "visible", timeout: 10000 });
      const phoneLabel = await labelOf(mp, ".dc-work-head");
      assert(!phoneLabel.shown && await wordless(mp), `the split's phone head shows the label ${JSON.stringify(phoneLabel)}`);
      const onBadge = await titleOf(mp, `${badge} .dc-term-icon`);
      const split = await headOf(mp);
      assert(onBadge.title === `Claude, steered by ${RENAMED}` && !split.cut && split.before <= 12 && split.scroll <= split.inner,
        `the split's phone head reads ${JSON.stringify({ onBadge, split })}`);
      const onPhone = await menuAt(mp, `${badge} .dc-term-icon`, "tap");
      assert(onPhone.length === 1 && onPhone[0] === RENAMED, `the split's phone menu reads ${onPhone}`);
      await settle(mp);
      await Promise.all([
        mp.waitForURL((u) => u.pathname === `/assistants/${fx.a}`, { timeout: 10000 }),
        mp.tap(".dc-context-menu .dropdown-item"),
      ]);
      const ungrouped = await post(page, "/terminal-tabs/ungroup", [["id", fx.coder], ["id", fx.shell]]);
      assert(ungrouped.status < 400, `the ungroup answered ${ungrouped.status}`);
    });

    await run("a release takes tooltip, label and menu entries away live", async () => {
      const mp = await phonePage();
      await visit(page, `${BASE}/coders/${fx.coder}`);
      await visit(mp, `${BASE}/coders/${fx.coder}`);
      await mp.waitForSelector(`${headMark}[role="button"]`, { timeout: 10000 });
      const desk = await stay(page);
      const phone = await stay(mp);
      await postJSON(page, "/assistants/jobs", { form: "release", terminal: fx.coder });
      await page.waitForFunction((s) => !document.querySelector(s)?.classList.contains("steered"), `${tab(fx.coder)} [data-tab-icon]`, { timeout: 15000 });
      await mp.waitForFunction((s) => !document.querySelector(s)?.classList.contains("steered"), headIcon, { timeout: 15000 });
      await page.waitForFunction(() => !document.querySelector(".dc-work-head [data-steered-label]"), null, { timeout: 15000 });
      const icon = await titleOf(page, `${tab(fx.coder)} [data-tab-icon]`);
      const head = await titleOf(mp, headIcon);
      assert(!icon.title && !icon.by && head.title === "Claude" && !head.steered && !head.by, `the icons read ${JSON.stringify({ icon, head })}`);
      const row = await labelOf(page, tab(fx.coder));
      const slot = await page.evaluate(() => { const s = document.querySelector(".dc-work-head .dc-steered-slot"); return { html: s.innerHTML, width: s.getBoundingClientRect().width }; });
      assert(!row.text && !slot.html && !slot.width, `the labels read ${JSON.stringify({ row, slot })}`);
      await menuAt(page, tab(fx.coder));
      const { entry } = await entryOf(page);
      const labels = await menuLabels(page);
      await closeMenu(page);
      assert(labels.includes("Steer") && !entry, `the menu reads ${labels}`);
      await tapOpensNothing(mp);
      await stayed(page, desk);
      await stayed(mp, phone);
    });

    await run("deleting the steering assistant takes its name away live", async () => {
      const mp = await phonePage();
      await visit(page, `${BASE}/coders/${fx.coder}`);
      await visit(mp, `${BASE}/coders/${fx.coder}`);
      await mp.waitForSelector(headIcon, { state: "visible", timeout: 10000 });
      const marker = await stay(page);
      const phone = await stay(mp);
      await postJSON(page, "/assistants/jobs", { form: "steer", assistant: fx.b, terminal: fx.coder, task: "Steered by the second", done_when: "" });
      await page.waitForFunction(([s, w]) => document.querySelector(s)?.title === w, [`${tab(fx.coder)} [data-tab-icon]`, `Claude, steered by ${OTHER}`], { timeout: 15000 });
      await mp.waitForFunction(([s, w]) => document.querySelector(s)?.title === w, [headIcon, `Claude, steered by ${OTHER}`], { timeout: 15000 });
      await page.waitForFunction((w) => document.querySelector(".dc-work-head [data-steered-label]")?.innerText.trim() === w, OTHER, { timeout: 15000 });
      await postJSON(page, `/assistants/${fx.b}`, { form: "delete" });
      await page.waitForFunction((s) => !document.querySelector(s)?.title, `${tab(fx.coder)} [data-tab-icon]`, { timeout: 15000 });
      await page.waitForFunction(() => !document.querySelector(".dc-work-head [data-steered-label]"), null, { timeout: 15000 });
      await mp.waitForFunction((s) => document.querySelector(s)?.title === "Claude", headIcon, { timeout: 15000 });
      const row = await labelOf(page, tab(fx.coder));
      assert(!row.text, `the row's label reads ${JSON.stringify(row)}`);
      await menuAt(page, tab(fx.coder));
      const { entry } = await entryOf(page);
      await closeMenu(page);
      assert(!entry, `the menu holds ${JSON.stringify(entry)}`);
      await tapOpensNothing(mp);
      await stayed(page, marker);
      await stayed(mp, phone);
      fx.b = "";
    });
  } finally {
    await run("cleanup", async () => {
      await visit(page, `${BASE}/projects`).catch(() => {});
      for (const id of [fx.coder]) if (id) await post(page, `/coders/${id}/delete`, {}).catch(() => {});
      if (fx.shell) await post(page, `/shells/${fx.shell}/delete`, {}).catch(() => {});
      for (const id of [fx.a, fx.b]) if (id) await post(page, `/assistants/${id}`, { form: "delete" }).catch(() => {});
      await L.deleteProject(page, PROJECT).catch(() => {});
      await sleep(300);
    });
  }
});
