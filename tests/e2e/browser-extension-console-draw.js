const fs = require("fs");
const os = require("os");
const path = require("path");
const L = require("./lib");
const X = require("./browser-extension-fixture");
const { assert, BASE, sleep } = L;

// The browser extension's console and drawing (internal/browser console.js and
// draw.js, the addon's catch.js, relay.js and content.js), loaded unpacked
// like browser-extension.js on a screen of pixel ratio 2, so a picture's
// pixels can be held against the page's. Instance and sites as for
// browser-extension.js. EXT_SHOTS: screenshots.

process.env.NODE_TLS_REJECT_UNAUTHORIZED = "0";
const { TAG, PROJECT, ORIGIN, SHOTS, SITE_A } = X;
const TOKEN_NAME = `Draw ${TAG}`;
const SPARE_NAME = `Draw again ${TAG}`;
const NOTE = `The head script breaks ${TAG}.`;
const RED = [214, 57, 57];
const GREEN = [0, 120, 80];

const tabOf = (ext, url) => ext.worker.evaluate(async (u) => (await chrome.tabs.query({})).find((t) => t.url === u)?.id ?? -1, url);
const badge = (ext, tabId) => ext.worker.evaluate((id) => chrome.action.getBadgeText({ tabId: id }), tabId);
const stored = (ext, key) => ext.worker.evaluate(async (k) => (await chrome.storage[/^(tray|log):/.test(k) ? "local" : "session"].get(k))[k] ?? null, key);

const logRows = (panel) => panel.eval(() => {
  const list = document.querySelector("[data-console-list]");
  const box = list.getBoundingClientRect();
  const seen = getComputedStyle(list).display !== "none" && box.height > 0;
  return {
    seen,
    summary: document.querySelector("[data-console-summary]").textContent,
    empty: document.querySelector("[data-console-empty]").hidden ? "" : document.querySelector("[data-console-empty]").textContent,
    rows: [...list.querySelectorAll(".log")].map((row) => ({ mark: row.classList.contains("mark"), kind: row.querySelector(".log-kind")?.textContent || "", text: row.querySelector(".log-text")?.textContent || "", id: row.dataset.logId || "" })),
  };
});

const drawBar = (panel) => panel.eval(() => {
  const bar = document.querySelector("[data-draw-bar]");
  const b = bar.getBoundingClientRect();
  const pressed = (sel) => document.querySelector(sel)?.getAttribute("aria-pressed") === "true";
  return {
    shown: getComputedStyle(bar).display !== "none" && b.height > 0,
    on: pressed("[data-draw]"),
    tool: bar.querySelector("[data-tool][aria-pressed=true]")?.dataset.tool || "",
    color: bar.querySelector("[data-color][aria-pressed=true]")?.dataset.color || "",
    undo: !bar.querySelector("[data-undo]").disabled,
    redo: !bar.querySelector("[data-redo]").disabled,
    clear: !bar.querySelector("[data-draw-clear]").disabled,
  };
});

// The sheet's shadow root is closed, only the DevTools protocol reads it.
async function sheet(page) {
  return X.readClosed(page, "DC-EXTENSION-DRAW", async (host, { send, model }) => {
    const shadow = host.shadowRoots[0];
    const shown = async (node) => {
      const { computedStyle } = await send("CSS.getComputedStyleForNode", { nodeId: node.nodeId });
      const display = computedStyle.find((p) => p.name === "display")?.value;
      const box = await model(node);
      return display !== "none" && Boolean(box) && box.width > 0 && box.height > 0;
    };
    const [canvas, glass] = shadow.children;
    return { host: true, mode: shadow.shadowRootType, canvas: await shown(canvas), glass: await shown(glass), parent: host.parentId };
  });
}

// The panel shows a choice once the page has it.
async function choose(panel, what, value) {
  await panel.click(`[data-${what}="${value}"]`);
  await X.until(() => drawBar(panel), (d) => d[what] === value, `the ${what} ${value} was not taken`);
}

async function stroke(page, from, to) {
  await page.mouse.move(from.x, from.y);
  await page.mouse.down();
  await page.mouse.move(to.x, to.y, { steps: 8 });
  await page.mouse.up();
  await sleep(120);
}

// Points are CSS pixels of the page, the picture is at the pixel ratio.
const picture = (ext, key, points) => ext.worker.evaluate(async ({ key, points }) => {
  const db = await new Promise((resolve, reject) => {
    const r = indexedDB.open("dc-extension-pictures", 1);
    r.onsuccess = () => resolve(r.result);
    r.onerror = () => reject(r.error);
  });
  const kept = await new Promise((resolve) => {
    const r = db.transaction("pictures").objectStore("pictures").get(key);
    r.onsuccess = () => resolve(r.result);
    r.onerror = () => resolve(null);
  });
  db.close();
  if (!kept) return null;
  const bitmap = await createImageBitmap(new Blob([kept.bytes], { type: kept.type }));
  const canvas = new OffscreenCanvas(bitmap.width, bitmap.height);
  const ctx = canvas.getContext("2d");
  ctx.drawImage(bitmap, 0, 0);
  const ratio = 2;
  const colors = points.map((p) => [...ctx.getImageData(Math.round(p.x * ratio), Math.round(p.y * ratio), 1, 1).data.slice(0, 3)]);
  return { width: bitmap.width, height: bitmap.height, colors };
}, { key, points });

const near = (got, want) => got && got.every((v, i) => Math.abs(v - want[i]) <= 24);

async function lastUser(page, id) {
  return page.evaluate(async (one) => (await (await fetch(`/assistants/instances/${one}?full=true`, { headers: { Accept: "application/json" } })).json()).messages.filter((m) => m.role === "user").pop(), id);
}

async function main() {
  const results = [];
  const bag = { consoleErrors: [], pageErrors: [], cdnNoise: [] };
  const run = async (name, fn) => {
    if (!(process.env.CHECK ? process.env.CHECK.split(",").some((t) => name.toLowerCase().includes(t.trim().toLowerCase())) : true)) return;
    const t0 = Date.now();
    try { const d = await fn(); results.push({ name: `[chromium] ${name}`, status: "PASS", detail: d || "", ms: Date.now() - t0 }); }
    catch (e) { results.push({ name: `[chromium] ${name}`, status: "FAIL", detail: e.message.split("\n")[0], ms: Date.now() - t0 }); }
  };
  const sites = await X.start(ORIGIN);
  const browser = await L.launch("chromium");
  const ctx = await L.newDesktop(browser, "chromium");
  const page = await ctx.newPage();
  L.wirePage(page, bag);
  const s = { made: [], ext: null, panel: null, downloads: fs.mkdtempSync(path.join(os.tmpdir(), `dc-ext-dl-${TAG}-`)) };
  try {
    await L.login(page);
    await L.dismissUpdate(page);
    await L.createProject(page, PROJECT);
    s.coder = new URL(await L.createSession(page, PROJECT, `extd-${TAG}`, "claude")).pathname.split("/").pop();
    const made = await X.postAs(page, "/assistants/new", { coder: "claude" });
    s.assistant = JSON.parse(made.text).id;
    s.made.push(s.assistant);
    s.title = `Draw ${TAG}`;
    await X.postAs(page, `/assistants/${s.assistant}`, { form: "rename", title: s.title });
    s.token = await X.makeToken(page, TOKEN_NAME);
    s.coderName = (await X.request("GET", "/api/browser-extension/targets", { token: s.token })).json.coders.find((one) => one.id === s.coder).name;
    s.files = X.unzip((await X.download(page, "chrome")).bytes);
    s.dir = X.unpack(s.files, X.EVERY_PAGE);
    s.ext = await X.launchWith(s.dir, { scale: 2 });
    const set = await X.setUp(s.ext, ORIGIN, s.token);
    assert(!set.error, set.said);
    await set.page.close();
    await X.until(() => s.ext.worker.evaluate(async () => (await chrome.scripting.getRegisteredContentScripts()).map((one) => one.id).sort().join()), (ids) => ids === "dc-console-catch,dc-console-relay", "the console catch was never put into the pages");

    s.site = await s.ext.context.newPage();
    await s.site.goto(`${SITE_A}/errors`);
    await X.press(s.ext, s.site);
    s.panel = await X.Panel.open(s.ext.session, `chrome-extension://${s.ext.id}/panel.html`);
    await X.until(() => X.panelState(s.panel), (p) => p.picking, "the panel never said it picks");
    await s.site.keyboard.press("Escape");
    await X.until(() => X.panelState(s.panel), (p) => !p.picking, "Escape did not end picking");
    s.tab = await tabOf(s.ext, `${SITE_A}/errors`);

    await run("the console lists what goes wrong from the first script on: an error thrown in the head, a warning, a logged error, a rejection and failed requests, and the badge counts them", async () => {
      const got = await X.until(() => logRows(s.panel), (l) => l.rows.length >= 7, "the console listed too little");
      const texts = got.rows.map((row) => `${row.kind} ${row.text}`);
      const want = ["error Uncaught Error: Head boom before anything", "warning Careful, the cache is cold", 'error Cart total is {"total":-1}', "error Uncaught (in promise) Error: The price service never answered", `request GET ${SITE_A}/missing.json 404 Not Found`, `request GET ${X.QUIET}/never failed Failed to fetch`, `request GET ${SITE_A}/gone.png 404 (img)`];
      for (const one of want) assert(texts.includes(one), `missing ${one} in ${JSON.stringify(texts)}`);
      assert(texts[0] === want[0] && got.rows.length === 7 && got.seen && got.summary === "6 errors, 1 warning" && !got.empty, JSON.stringify(got));
      assert((await badge(s.ext, s.tab)) === "7", `the badge says ${await badge(s.ext, s.tab)}`);
      await s.panel.click('[data-log-id="1"] [data-log-toggle]');
      const stack = await s.panel.eval(() => { const pre = document.querySelector('[data-log-id="1"] .log-stack'); return pre && getComputedStyle(pre).display !== "none" ? pre.textContent : ""; });
      assert(stack.includes(`${SITE_A}/errors`) && !stack.includes("extension:"), `the stack: ${stack}`);
    });

    await run("the badge starts at zero after another page, a view the page loads itself and a reload, the console keeps the earlier page under a mark", async () => {
      await s.site.goto(`${SITE_A}/second`);
      await X.until(() => badge(s.ext, s.tab), (b) => b === "", "the badge kept counting on another page");
      let got = await X.until(() => logRows(s.panel), (l) => l.rows.at(-1)?.mark, "no mark for the other page");
      assert(got.rows.at(-1).text === `Navigated to ${SITE_A}/second` && got.rows.length === 8, JSON.stringify(got.rows.at(-1)));
      await s.site.goto(`${SITE_A}/errors`);
      await X.until(() => badge(s.ext, s.tab), (b) => b === "7", "the badge did not count the errors page again");
      await s.site.evaluate(() => window.route("/errors/view"));
      await X.until(() => badge(s.ext, s.tab), (b) => b === "", "the badge kept counting after a view the page loaded itself");
      got = await logRows(s.panel);
      assert(got.rows.at(-1).mark && got.rows.at(-1).text === `Navigated to ${SITE_A}/errors/view`, JSON.stringify(got.rows.at(-1)));
      await s.site.click("#again");
      await X.until(() => badge(s.ext, s.tab), (b) => b === "1", "a click's error did not count on the new view");
      const viewKeeps = async (view) => {
        got = await X.until(() => logRows(s.panel), (l) => l.rows.at(-1)?.text === "Uncaught Error: Clicked into a bug", "the click's error was not listed");
        const log = await stored(s.ext, `log:${s.tab}`);
        assert(got.rows.at(-2).mark && got.rows.at(-2).text === `Navigated to ${SITE_A}${view}` && got.rows.at(-1).text === "Uncaught Error: Clicked into a bug" && log.entries.at(-1).page === `${SITE_A}${view}`, JSON.stringify({ rows: got.rows.slice(-2), page: log.entries.at(-1).page }));
        await s.site.evaluate(() => { location.hash = "spot"; });
        await X.until(() => s.ext.worker.evaluate(async (id) => (await chrome.tabs.get(id)).url, s.tab), (u) => u === `${SITE_A}${view}#spot`, "the jump never reached the tab");
        await sleep(400);
        got = await logRows(s.panel);
        assert((await badge(s.ext, s.tab)) === "1" && !got.rows.at(-1).mark, `a jump to a spot on ${view} emptied the badge: ${JSON.stringify(got.rows.at(-1))}`);
      };
      await viewKeeps("/errors/view");
      await s.site.evaluate(() => window.route("/errors/two"));
      await X.until(() => badge(s.ext, s.tab), (b) => b === "", "the badge kept counting after the next view");
      await s.site.click("#again");
      await X.until(() => badge(s.ext, s.tab), (b) => b === "1", "a click's error did not count on the next view");
      await viewKeeps("/errors/two");
      await s.site.goto(`${SITE_A}/errors`);
      await s.site.reload();
      await X.until(() => logRows(s.panel), (l) => l.rows.length === 7 && !l.rows.some((row) => row.mark), "a reload did not start the console again");
      assert((await badge(s.ext, s.tab)) === "7", "the badge after the reload");
    });

    await run("an entry and every entry go into the tray as chips and into Copy in the Browser's format", async () => {
      await s.panel.click('[data-log-id] [data-log-add]');
      await X.until(() => X.chips(s.panel), (c) => c.length === 1, "the entry did not reach the tray");
      await s.panel.click("[data-console-all]");
      const chips = await X.until(() => X.chips(s.panel), (c) => c.length === 2, "Add all did not reach the tray");
      assert(chips[0].label === 'error "Uncaught Error: Head boom before anythi…"' && chips[1].label === "Console, 7 entries", JSON.stringify(chips));
      assert((await X.panelState(s.panel)).hint === "7 console entries" || (await X.panelState(s.panel)).hint === "8 console entries", (await X.panelState(s.panel)).hint);
      await s.panel.eval(() => {
        window.copied = "";
        Object.defineProperty(navigator.clipboard, "writeText", { configurable: true, value: async (text) => { window.copied = text; } });
      });
      await s.panel.click("[data-copy]");
      const text = await X.until(() => s.panel.eval(() => window.copied), (t) => t, "Copy wrote nothing");
      assert(text.includes("## Picked with the browser extension") && text.includes("### 1. Uncaught error") && text.includes("Head boom before anything") && text.includes("### 2. The page's console, 7 entries") && text.includes(`request GET ${SITE_A}/missing.json 404 Not Found`), text.slice(0, 600));
    });

    await run("a page that logs more than the catch keeps says how many more were not listed, in the console and in Copy; a row unfolded in one tab stays folded in another", async () => {
      const unfolded = await s.panel.eval(() => [...document.querySelectorAll("[data-console-list] .log[data-log-id]")].find((row) => row.querySelector(".log-stack"))?.dataset.logId || "");
      await s.panel.click(`[data-log-id="${unfolded}"] [data-log-toggle]`);
      await s.panel.waitFor(`document.querySelector('[data-log-id="${unfolded}"]').classList.contains("open")`, 3000, "the row unfolded");
      const flood = await s.ext.context.newPage();
      try {
        await flood.goto(`${SITE_A}/flood`);
        await flood.bringToFront();
        const got = await X.until(() => logRows(s.panel), (l) => l.rows.at(-1)?.text === "Flood 200", "the console did not list the flood");
        const open = await s.panel.eval(() => [...document.querySelectorAll("[data-console-list] .log.open")].map((row) => row.dataset.logId));
        assert(Number(unfolded) <= 200 && open.length === 0, `the other tab shows rows unfolded: ${JSON.stringify(open)}`);
        const left = await s.panel.eval(() => {
          const row = document.querySelector("[data-console-list] .log-left");
          return row && getComputedStyle(row).display !== "none" && row.getBoundingClientRect().height > 0 ? row.textContent : "";
        });
        assert(left === "40 more entries were not listed." && got.rows.length === 200 && got.rows[0].text === "Flood 1", JSON.stringify({ left, rows: got.rows.length, first: got.rows[0] }));
        await s.panel.click("[data-console-all]");
        await X.until(() => X.chips(s.panel), (c) => c.length === 1, "Add all did not reach the tray");
        await s.panel.eval(() => {
          window.copied = "";
          Object.defineProperty(navigator.clipboard, "writeText", { configurable: true, value: async (text) => { window.copied = text; } });
        });
        await s.panel.click("[data-copy]");
        const text = await X.until(() => s.panel.eval(() => window.copied), (t) => t, "Copy wrote nothing");
        assert(/\n- \d+ more entries are not listed\.\n/.test(text) && !text.includes("earlier") && text.includes("Flood 200") && !text.includes("Flood 201"), text.slice(0, 600));
      } finally {
        await flood.close();
      }
      await s.site.bringToFront();
      await s.panel.waitFor(`document.querySelector('[data-log-id="${unfolded}"]')?.classList.contains("open")`, 3000, "the row unfolded in its own tab again");
    });

    await run("DevTools names the page's own line as the source of a logged error or warning, not the extension's console.js", async () => {
      const dir = X.unpack(s.files, X.EVERY_PAGE);
      const port = 9333;
      const tooled = await X.launchWith(dir, { args: ["--remote-allow-origins=*", `--remote-debugging-port=${port}`] });
      try {
        await X.until(() => tooled.worker.evaluate(async () => (await chrome.scripting.getRegisteredContentScripts()).length), (n) => n === 2, "the console catch was never put into the pages");
        const errors = await tooled.context.newPage();
        await errors.goto(`${SITE_A}/errors`);
        const target = (await (await fetch(`http://127.0.0.1:${port}/json/list`)).json()).find((one) => one.url === `${SITE_A}/errors`);
        const tools = await tooled.context.newPage();
        await tools.goto(`http://127.0.0.1:${port}/devtools/inspector.html?ws=127.0.0.1:${port}/devtools/page/${target.id}&panel=console`);
        const logged = () => tools.locator(".console-message-wrapper").evaluateAll((els) => els.map((el) => el.innerText.replace(/\s+/g, " ").trim()).filter((one) => /Careful, the cache is cold|Cart total is/.test(one)));
        await X.until(logged, (list) => list.length === 2, "DevTools did not list the page's console", 20000);
        await X.until(logged, (list) => list.every((one) => /^errors:\d+ /.test(one)), "DevTools links the extension as the source", 15000);
      } finally {
        await tooled.close();
        fs.rmSync(dir, { recursive: true, force: true });
      }
    });

    await run("the console lists the files a style sheet failed to load, a background and a font, beside a failed image", async () => {
      const styled = await s.ext.context.newPage();
      try {
        await styled.goto(`${SITE_A}/styled`);
        await styled.bringToFront();
        const want = [`request GET ${SITE_A}/missing.png 404 (img)`, `request GET ${SITE_A}/missing-bg.png 404 (css)`, `request GET ${SITE_A}/missing-font.woff2 404 (css)`];
        const line = (row) => `${row.kind} ${row.text}`;
        await X.until(() => logRows(s.panel), (l) => want.every((one) => l.rows.some((row) => line(row) === one)), "the console missed a failed file");
        await sleep(500);
        const got = await logRows(s.panel);
        const texts = got.rows.map(line);
        assert(want.every((one) => texts.includes(one)) && texts.length === 3 && got.summary === "3 errors", JSON.stringify({ texts, summary: got.summary }));
      } finally {
        await styled.close();
      }
      await s.site.bringToFront();
    });

    s.draw = await s.ext.context.newPage();
    await s.draw.goto(`${SITE_A}/draw`);
    await s.draw.bringToFront();
    s.drawTab = await tabOf(s.ext, `${SITE_A}/draw`);
    await X.until(() => X.chips(s.panel), (c) => c.length === 0, "the panel did not follow to the draw tab");

    await run("Draw draws with pen, arrow, box and text in a color, and the page takes no click and no scroll while it draws", async () => {
      await s.panel.click("[data-draw]");
      await X.until(() => drawBar(s.panel), (d) => d.on && d.shown, "Draw did not start");
      const glass = await X.until(() => sheet(s.draw), (o) => o.host && o.glass, "no glass over the page");
      assert(glass.mode === "closed", JSON.stringify(glass));
      const fix = await X.boxOf(s.draw, "#fix");
      const spot = await X.boxOf(s.draw, "#spot");
      await stroke(s.draw, { x: fix.x - 10, y: fix.y + fix.height / 2 }, { x: fix.x + fix.width + 10, y: fix.y + fix.height / 2 });
      await choose(s.panel, "tool", "arrow");
      await choose(s.panel, "color", "blue");
      await stroke(s.draw, { x: spot.x + spot.width + 120, y: spot.y + 80 }, { x: spot.x + spot.width - 20, y: spot.y + 30 });
      await choose(s.panel, "tool", "rect");
      await stroke(s.draw, { x: spot.x - 8, y: spot.y - 8 }, { x: spot.x + spot.width + 8, y: spot.y + spot.height + 8 });
      await choose(s.panel, "tool", "text");
      await s.draw.mouse.click(spot.x + 40, spot.y + spot.height + 60);
      await s.draw.keyboard.type("too green");
      await s.draw.keyboard.press("Enter");
      const state = await X.until(() => stored(s.ext, `draw:${s.drawTab}`), (d) => d && d.count === 4, "four marks did not land");
      assert(state.on && state.undo && !state.redo, JSON.stringify(state));
      await s.draw.mouse.move(spot.x + 10, spot.y + 10);
      await s.draw.mouse.wheel(0, 600);
      await s.draw.keyboard.press("PageDown");
      await s.draw.keyboard.press("Space");
      await sleep(300);
      const quiet = await s.draw.evaluate(() => ({ clicks: window.clicks, scrolls: window.scrolls, y: scrollY }));
      assert(quiet.clicks === 0 && quiet.scrolls === 0 && quiet.y === 0, `the page moved or took a click: ${JSON.stringify(quiet)}`);
    });

    await run("undo and redo step through the marks, Escape leaves drawing with the drawing kept and clicks reach the page again, Clear takes the marks off", async () => {
      await s.panel.click("[data-undo]");
      await X.until(() => stored(s.ext, `draw:${s.drawTab}`), (d) => d.count === 3 && d.redo, "Undo did not take the text off");
      await s.draw.keyboard.press("Control+Shift+Z");
      await X.until(() => stored(s.ext, `draw:${s.drawTab}`), (d) => d.count === 4 && !d.redo, "Redo did not bring it back");
      await s.draw.keyboard.press("Escape");
      const off = await X.until(() => drawBar(s.panel), (d) => !d.on, "Escape did not end drawing");
      assert(off.shown && off.clear, `the drawing bar went with the drawing: ${JSON.stringify(off)}`);
      const kept = await sheet(s.draw);
      assert(kept.host && kept.canvas && !kept.glass, `the drawing did not stay: ${JSON.stringify(kept)}`);
      await s.draw.click("#fix");
      assert((await s.draw.evaluate(() => window.clicks)) === 2, "a click after drawing does not reach the page");
      await s.panel.click("[data-draw-clear]");
      await X.until(() => stored(s.ext, `draw:${s.drawTab}`), (d) => d.count === 0, "Clear left marks");
      assert(!(await sheet(s.draw)).host && !(await drawBar(s.panel)).shown, "the sheet or the bar stayed after Clear");
      await s.draw.evaluate(() => { window.clicks = 0; });
    });

    await run("Escape in the panel ends drawing as it does in the page", async () => {
      await s.panel.click("[data-draw]");
      await X.until(() => drawBar(s.panel), (d) => d.on, "Draw did not start");
      await s.panel.key("Escape", "Escape", 27);
      await X.until(() => drawBar(s.panel), (d) => !d.on, "Escape in the panel did not end drawing");
      assert(!(await sheet(s.draw)).glass, "the glass stayed over the page");
    });

    await run("marks move with the area of the page they were drawn on when it scrolls, in the page and in the picture", async () => {
      const inner = await s.ext.context.newPage();
      try {
        await inner.goto(`${SITE_A}/inner`);
        await inner.bringToFront();
        const innerTab = await tabOf(s.ext, `${SITE_A}/inner`);
        await s.panel.click("[data-draw]");
        await X.until(() => drawBar(s.panel), (d) => d.on, "Draw did not start");
        await choose(s.panel, "tool", "rect");
        await choose(s.panel, "color", "red");
        const row = await X.boxOf(inner, "#row8");
        await stroke(inner, { x: row.x - 10, y: row.y - 10 }, { x: row.x + row.width + 10, y: row.y + row.height + 10 });
        await X.until(() => stored(s.ext, `draw:${innerTab}`), (d) => d && d.count === 1, "no mark");
        await inner.keyboard.press("Escape");
        await X.until(() => drawBar(s.panel), (d) => !d.on, "drawing did not end");
        await inner.mouse.move(row.x + 20, row.y + 20);
        await inner.mouse.wheel(0, 240);
        const moved = await X.until(() => X.boxOf(inner, "#row8"), (b) => Math.abs(row.y - b.y - 240) < 1, "the main area did not scroll");
        await sleep(200);
        await s.panel.click("[data-picture-add]");
        await X.until(() => X.chips(s.panel), (c) => c.length === 1, "no drawing in the tray");
        const tray = await stored(s.ext, `tray:${innerTab}`);
        const item = tray.items[0].item;
        const left = row.x - 10;
        const shot = await picture(s.ext, tray.items[0].key, [{ x: left, y: moved.y + moved.height / 2 }, { x: left, y: row.y + row.height / 2 }]);
        assert(near(shot.colors[0], RED) && !near(shot.colors[1], RED) && item.marks[0].target?.selector === "#row8" && item.marks[0].seen, JSON.stringify({ colors: shot.colors, mark: item.marks[0] }));
      } finally {
        await inner.close();
      }
      await s.draw.bringToFront();
      await X.until(() => X.chips(s.panel), (c) => c.length === 0, "the panel did not come back to the draw tab");
    });

    await run("the picture shows the page's own pixels with the drawing over it at the screen's pixel ratio, downloads as a PNG and goes into the tray as a drawing", async () => {
      await s.panel.click("[data-draw]");
      await X.until(() => drawBar(s.panel), (d) => d.on, "Draw did not start");
      await choose(s.panel, "tool", "pen");
      await choose(s.panel, "color", "red");
      const fix = await X.boxOf(s.draw, "#fix");
      const spot = await X.boxOf(s.draw, "#spot");
      const y = fix.y + fix.height / 2;
      await stroke(s.draw, { x: fix.x - 20, y }, { x: fix.x + fix.width + 20, y });
      await X.until(() => stored(s.ext, `draw:${s.drawTab}`), (d) => d.count === 1, "no mark");
      await s.ext.session.send("Browser.setDownloadBehavior", { behavior: "allowAndName", downloadPath: s.downloads, eventsEnabled: true });
      const began = new Promise((resolve) => s.ext.session.once("Browser.downloadWillBegin", resolve));
      const done = new Promise((resolve) => {
        const on = (event) => { if (event.state === "completed") { s.ext.session.off("Browser.downloadProgress", on); resolve(event); } };
        s.ext.session.on("Browser.downloadProgress", on);
      });
      await s.panel.click("[data-picture-save]");
      const started = await Promise.race([began, sleep(10000).then(() => null)]);
      assert(started && /^drawing-localhost-\d+-\d{8}-\d{6}\.png$/.test(started.suggestedFilename), `the download: ${JSON.stringify(started)}`);
      const finished = await Promise.race([done, sleep(10000).then(() => null)]);
      const file = finished && fs.readFileSync(path.join(s.downloads, finished.guid));
      const view = await s.draw.evaluate(() => ({ w: innerWidth, h: innerHeight, dpr: devicePixelRatio }));
      assert(view.dpr === 2 && file && file.readUInt32BE(0) === 0x89504e47 && file.readUInt32BE(16) === view.w * 2 && file.readUInt32BE(20) === view.h * 2, `the PNG: ${file ? `${file.readUInt32BE(16)}x${file.readUInt32BE(20)}` : "none"} for ${JSON.stringify(view)}`);
      assert((await X.panelState(s.panel)).status === "Downloaded the picture.", (await X.panelState(s.panel)).status);
      await s.panel.click("[data-picture-add]");
      const chips = await X.until(() => X.chips(s.panel), (c) => c.length === 1, "no drawing in the tray");
      assert(chips[0].label === "Drawing, one mark", JSON.stringify(chips));
      const tray = await stored(s.ext, `tray:${s.drawTab}`);
      const item = tray.items[0].item;
      assert(item.kind === "image" && item.pixels && item.width === view.w && item.height === view.h && item.ratio === 2 && item.marks.length === 1 && item.marks[0].target?.label === 'button#fix.btn "Fix me"' && item.marks[0].target.selector === "#fix", JSON.stringify(item));
      const shot = await picture(s.ext, tray.items[0].key, [{ x: fix.x + fix.width / 2, y }, { x: spot.x + spot.width / 2, y: spot.y + spot.height - 6 }]);
      assert(shot && shot.width === view.w * 2 && shot.height === view.h * 2 && near(shot.colors[0], RED) && near(shot.colors[1], GREEN), `the picture: ${JSON.stringify(shot)}`);
      await s.panel.click("[data-chips] [data-chip-show]");
      const preview = await X.until(() => s.panel.eval(() => { const img = document.querySelector("[data-preview-image]"); return img && img.complete ? { w: img.naturalWidth, seen: img.getBoundingClientRect().width > 0 } : null; }), (p) => p && p.w === view.w * 2 && p.seen, "the chip shows no picture");
      assert(preview.seen, JSON.stringify(preview));
      await s.panel.click("[data-preview-close]");
      await s.draw.keyboard.press("Escape");
    });

    await run("a picture and console entries go to the assistant: the arrow leaves the page with the send, the thread shows the card with the picture from its media, and the assistant reads the element under every mark", async () => {
      await s.site.bringToFront();
      await X.until(() => X.chips(s.panel), (c) => c.length === 2, "the errors tab's tray is gone");
      await s.panel.click("[data-draw]");
      await X.until(() => drawBar(s.panel), (d) => d.on, "Draw did not start");
      await choose(s.panel, "tool", "arrow");
      const again = await X.boxOf(s.site, "#again");
      await stroke(s.site, { x: again.x + again.width + 150, y: again.y + 90 }, { x: again.x + again.width - 6, y: again.y + again.height / 2 });
      await X.until(() => stored(s.ext, `draw:${s.tab}`), (d) => d && d.count === 1, "no arrow");
      await s.panel.click("[data-picture-add]");
      await X.until(() => X.chips(s.panel), (c) => c.length === 3, "the picture did not join the console chips");
      await s.site.keyboard.press("Escape");
      const before = (await lastUser(page, s.assistant))?.content || "";
      await s.panel.click("[data-note]");
      await s.panel.type(NOTE);
      await s.panel.click("[data-send]");
      await s.panel.waitFor("document.querySelectorAll('[data-menu] [role=menuitem]').length > 1", 8000, "the send menu");
      await s.panel.clickText("[data-menu] [role=menuitem]", s.title);
      const done = await X.until(() => X.panelState(s.panel), (p) => p.status.startsWith("Sent to") || /refuse|not|could/.test(p.status), "the send never ended", 20000);
      assert(done.status === `Sent to ${s.title}. Open the assistant` && (await X.chips(s.panel)).length === 0, JSON.stringify(done));
      await X.until(async () => ({ sheet: await sheet(s.site), state: await stored(s.ext, `draw:${s.tab}`) }), (l) => !l.sheet.host && l.state?.count === 0, "the arrow stayed on the page after the send");
      const got = await X.until(() => lastUser(page, s.assistant), (m) => m && m.content !== before, "the assistant got nothing");
      const view = await s.site.evaluate(() => `${innerWidth}×${innerHeight}`);
      const file = new RegExp(`- Picture: \`(drawing-localhost-\\d+-\\d{8}-\\d{6}(?:-\\d+)?\\.png)\`, what the page showed with the drawing over it, ${view} at 2x`).exec(got.content)?.[1];
      assert(got.content.startsWith(`${NOTE}\n\n## Picked with the browser extension\n`) && got.content.includes("### 1. Uncaught error") && got.content.includes("### 2. The page's console, 7 entries") && got.content.includes("### 3. A drawing on the page") && file, JSON.stringify(got.content.slice(got.content.indexOf("### 3."))));
      assert(got.content.includes('1. Arrow, red, pointing at `button#again.btn "Fail again"`, selector `#again`'), JSON.stringify(got.content.slice(got.content.indexOf("### 3."), got.content.indexOf("### 3.") + 500)));
      await page.goto(`${BASE}/assistants/${s.assistant}`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
      const card = await X.until(() => page.evaluate((note) => document.querySelector("[data-picked]")?.closest("[data-assistant-message]")?.querySelector("[data-assistant-text]")?.innerText.trim() === note, NOTE), (v) => v, "the thread shows no card under the note");
      assert(card, "card");
      await page.click("[data-picked] [data-bs-toggle=collapse]");
      const body = await X.until(() => page.evaluate(() => {
        const el = document.querySelector("[data-picked]");
        const img = el.querySelector("[data-picked-picture]");
        const vis = (n) => { if (!n) return false; const st = getComputedStyle(n); const b = n.getBoundingClientRect(); return st.display !== "none" && st.visibility !== "hidden" && b.width > 0 && b.height > 0; };
        return { open: vis(el.querySelector("[data-picked-body]")), chips: [...el.querySelectorAll("[data-picked-chip]")].map((c) => c.dataset.pickedChip), picture: img && img.complete ? img.naturalWidth : 0, pictureSeen: vis(img), src: img?.getAttribute("src") || "", lines: [...el.querySelectorAll("[data-picked-line]")].map((l) => l.innerText.trim()) };
      }), (b) => b.open && b.picture > 0, "the card did not unfold with the picture");
      assert(body.src.startsWith(`/assistants/${s.assistant}/media/`) && body.src.endsWith(encodeURIComponent(file)), `the picture comes from ${body.src}, not the assistant's media`);
      assert(body.chips.join() === "error,image" && body.pictureSeen && body.picture > 2000 && body.lines.some((l) => l.includes("Head boom before anything")) && body.lines.some((l) => l.includes('Arrow pointing at button#again.btn "Fail again"')), JSON.stringify(body));
    });

    await run("a picture sent to a running coder lands in its files and the Markdown names its path", async () => {
      await s.draw.bringToFront();
      await X.until(() => X.chips(s.panel), (c) => c.length === 1, "the draw tab's tray is gone");
      await s.panel.click("[data-note]");
      await s.panel.type(`Coder ${TAG} picture`);
      await s.panel.click("[data-send]");
      await s.panel.waitFor("document.querySelectorAll('[data-menu] [role=menuitem]').length > 1", 8000, "the send menu");
      await s.panel.clickText("[data-menu] [role=menuitem]", s.coderName);
      const done = await X.until(() => X.panelState(s.panel), (p) => p.status.startsWith("Sent to") || /refuse|not|could/.test(p.status), "the send never ended", 20000);
      assert(done.status === `Sent to ${s.coderName}. Open the coder`, done.status);
      const text = await X.until(() => page.evaluate(async (id) => (await (await fetch(`/coders/${id}/copy?lines=400`)).json()).text || "", s.coder), (t) => t.includes(`Coder ${TAG} picture`) && /Picture: `[^`]*drawing-localhost-[^`]*\.png`/.test(t.replace(/\s*\n\s*/g, "")), "the coder never showed the picture's path");
      assert(!text.includes("not part of this text"), text.slice(-400));
    });

    await run("a send with the drawing tools open takes the marks off its page and ends drawing there, a failed send keeps the marks, drawing and the tray, another tab keeps its drawing", async () => {
      await s.draw.bringToFront();
      await X.until(() => X.chips(s.panel), (c) => c.length === 0, "the draw tab's tray is not empty");
      await s.panel.click("[data-draw]");
      await X.until(() => drawBar(s.panel), (d) => d.on, "Draw did not start in the other tab");
      await choose(s.panel, "tool", "pen");
      const fix = await X.boxOf(s.draw, "#fix");
      await stroke(s.draw, { x: fix.x - 10, y: fix.y + fix.height / 2 }, { x: fix.x + fix.width + 10, y: fix.y + fix.height / 2 });
      await X.until(() => stored(s.ext, `draw:${s.drawTab}`), (d) => d && d.on && d.count === 1, "no mark in the other tab");
      await s.site.bringToFront();
      await X.until(() => drawBar(s.panel), (d) => !d.on, "the panel shows the other tab drawing");
      await s.panel.click("[data-draw]");
      await X.until(() => drawBar(s.panel), (d) => d.on, "Draw did not start");
      await choose(s.panel, "tool", "arrow");
      const again = await X.boxOf(s.site, "#again");
      await stroke(s.site, { x: again.x + again.width + 150, y: again.y + 90 }, { x: again.x + again.width - 6, y: again.y + again.height / 2 });
      await choose(s.panel, "tool", "rect");
      await stroke(s.site, { x: again.x + 2, y: again.y - 8 }, { x: again.x + again.width + 8, y: again.y + again.height + 8 });
      await X.until(() => stored(s.ext, `draw:${s.tab}`), (d) => d && d.on && d.count === 2, "no arrow and box");
      await s.panel.click("[data-picture-add]");
      await X.until(() => X.chips(s.panel), (c) => c.length === 1, "the picture did not go into the tray");
      await s.panel.click("[data-note]");
      await s.panel.type(`Send ends drawing ${TAG}`);
      const sendNew = async () => {
        await s.panel.click("[data-send]");
        await s.panel.waitFor("document.querySelectorAll('[data-menu] [role=menuitem][data-target=new]').length > 0", 8000, "the send menu");
      };
      await sendNew();
      await X.revokeToken(page, TOKEN_NAME);
      await s.panel.click("[data-menu] [role=menuitem][data-target=new]");
      await X.until(() => X.panelState(s.panel), (p) => /refuses the token/.test(p.status), "the send with the revoked token did not fail");
      const kept = { state: await stored(s.ext, `draw:${s.tab}`), bar: await drawBar(s.panel), sheet: await sheet(s.site), chips: (await X.chips(s.panel)).length };
      assert(kept.state.on && kept.state.count === 2 && kept.bar.on && kept.bar.shown && kept.sheet.canvas && kept.sheet.glass && kept.chips === 1, `the failed send changed the drawing or the tray: ${JSON.stringify(kept)}`);
      s.token = await X.makeToken(page, SPARE_NAME);
      const set = await X.setUp(s.ext, ORIGIN, s.token);
      assert(!set.error, set.said);
      await set.page.close();
      await s.site.bringToFront();
      await X.until(async () => ({ chips: (await X.chips(s.panel)).length, send: await s.panel.eval(() => !document.querySelector("[data-send]").disabled), bar: await drawBar(s.panel) }), (p) => p.chips === 1 && p.send && p.bar.on, "the panel did not come back to the page's tray and drawing");
      await sendNew();
      await s.panel.click("[data-menu] [role=menuitem][data-target=new]");
      const done = await X.until(() => X.panelState(s.panel), (p) => p.status.startsWith("Sent to") || /refuse|not|could/.test(p.status), "the send never ended", 20000);
      const id = (await s.panel.eval(() => document.querySelector("[data-status] [data-open]")?.href || "")).split("/assistants/")[1] || "";
      if (id) s.made.push(id);
      assert(done.status === `Sent to Send ends drawing ${TAG}. Open the assistant` && id, done.status);
      const gone = await X.until(async () => ({ state: await stored(s.ext, `draw:${s.tab}`), bar: await drawBar(s.panel), sheet: await sheet(s.site) }), (g) => !g.sheet.host && g.state && !g.state.on && g.state.count === 0, "the marks or drawing stayed on the page after the send");
      assert(!gone.state.undo && !gone.state.redo && !gone.bar.on && !gone.bar.shown && (await X.chips(s.panel)).length === 0, JSON.stringify(gone));
      const other = { state: await stored(s.ext, `draw:${s.drawTab}`), sheet: await sheet(s.draw) };
      assert(other.state.on && other.state.count === 1 && other.sheet.canvas && other.sheet.glass, `the other tab lost its drawing: ${JSON.stringify(other)}`);
      await s.draw.bringToFront();
      await X.until(() => drawBar(s.panel), (d) => d.on && d.shown, "the panel does not show the other tab's drawing");
      await s.draw.keyboard.press("Escape");
      await X.until(() => drawBar(s.panel), (d) => !d.on, "Escape did not end drawing in the other tab");
    });

    await run("drawing over a modal dialog and over an open popover menu: the marks land, the dialog takes no click and the menu stays open", async () => {
      await s.draw.goto(`${SITE_A}/modal`);
      await s.panel.click("[data-draw]");
      await X.until(() => drawBar(s.panel), (d) => d.on, "Draw did not start over the dialog");
      const ok = await X.boxOf(s.draw, "#ok");
      await stroke(s.draw, { x: ok.x - 30, y: ok.y + ok.height / 2 }, { x: ok.x + ok.width + 10, y: ok.y + ok.height / 2 });
      await X.until(() => stored(s.ext, `draw:${s.drawTab}`), (d) => d && d.count === 1, "no mark over the dialog");
      const modal = await s.draw.evaluate(() => ({ open: document.getElementById("confirm").open, clicks: window.clicks, seen: window.seenClicks, title: document.title }));
      assert(modal.open && modal.clicks === 0 && modal.seen === 0 && modal.title === "Modal page", JSON.stringify(modal));
      await s.draw.keyboard.press("Escape");
      await X.until(() => drawBar(s.panel), (d) => !d.on, "Escape did not end drawing");
      await s.draw.goto(`${SITE_A}/menu`);
      await s.draw.click("#open-menu");
      await s.panel.click("[data-draw]");
      await X.until(() => drawBar(s.panel), (d) => d.on, "Draw did not start over the menu");
      await choose(s.panel, "tool", "pen");
      const item = await X.boxOf(s.draw, "#item2");
      await stroke(s.draw, { x: item.x - 10, y: item.y + item.height / 2 }, { x: item.x + item.width + 10, y: item.y + item.height / 2 });
      await s.panel.click("[data-picture-add]");
      await X.until(() => X.chips(s.panel), (c) => c.length === 1, "no picture of the menu");
      const marks = (await stored(s.ext, `tray:${s.drawTab}`)).items[0].item.marks;
      const menu = await s.draw.evaluate(() => ({ open: document.getElementById("menu").matches(":popover-open"), toggles: window.toggles.join(), clicks: window.clicks }));
      assert(menu.open && menu.toggles === "menu open" && menu.clicks === 0 && marks[0]?.target?.label === 'button#item2.btn "Item two"', JSON.stringify({ menu, marks }));
      await s.draw.keyboard.press("Escape");
      await s.panel.click("[data-clear]");
      await X.until(() => X.chips(s.panel), (c) => c.length === 0, "Clear left chips");
    });

    await run("a page can neither draw, take a picture nor fill the tray, what it forges into its console stays in the console", async () => {
      await s.draw.goto(`${SITE_A}/draw`);
      await s.panel.click("[data-draw]");
      await X.until(() => drawBar(s.panel), (d) => d.on, "Draw did not start");
      const log = await s.draw.evaluate((id) => window.attack(id), s.ext.id);
      await sleep(600);
      assert(log.every((one) => one.endsWith("refused")), JSON.stringify(log));
      const state = await stored(s.ext, `draw:${s.drawTab}`);
      const chips = await X.chips(s.panel);
      const rows = await logRows(s.panel);
      const pictures = await s.ext.worker.evaluate(async () => {
        const db = await new Promise((resolve) => { const r = indexedDB.open("dc-extension-pictures", 1); r.onsuccess = () => resolve(r.result); });
        const keys = await new Promise((resolve) => { const r = db.transaction("pictures").objectStore("pictures").getAllKeys(); r.onsuccess = () => resolve(r.result); });
        db.close();
        return keys.length;
      });
      const errorsTray = await stored(s.ext, `tray:${s.tab}`);
      assert(state.count === 0 && chips.length === 0 && pictures === 0 && (errorsTray?.items || []).length === 0, JSON.stringify({ state, chips, pictures }));
      assert(rows.rows.some((row) => row.text === "Forged by the page") && (await badge(s.ext, s.drawTab)) === "1", JSON.stringify(rows));
      await s.draw.keyboard.press("Escape");
    });

    await run("without access to every page the panel lists no console and says so, Draw and its picture work in the tab after a click on the button", async () => {
      const dir = X.unpack(s.files, [ORIGIN]);
      const narrow = await X.launchWith(dir);
      let panel = null;
      try {
        const set = await X.setUp(narrow, ORIGIN, s.token, { every: false });
        assert(!set.error, set.said);
        await set.page.close();
        assert((await narrow.worker.evaluate(async () => (await chrome.scripting.getRegisteredContentScripts()).length)) === 0, "the catch went into the pages without the access");
        const tab = await narrow.context.newPage();
        await tab.goto(`${SITE_A}/errors`);
        await X.press(narrow, tab);
        panel = await X.Panel.open(narrow.session, `chrome-extension://${narrow.id}/panel.html`);
        await X.until(() => X.panelState(panel), (p) => p.picking, "no picking");
        const said = await X.until(() => logRows(panel), (l) => l.empty, "the console said nothing");
        assert(said.empty === "The console needs access to every page, Setup gives it." && !said.seen && said.rows.length === 0, JSON.stringify(said));
        await panel.click("[data-draw]");
        await X.until(() => drawBar(panel), (d) => d.on && d.shown, "Draw did not start in the clicked tab");
        assert(!(await X.panelState(panel)).picking, "picking went on while drawing");
        const again = await X.boxOf(tab, "#again");
        await stroke(tab, { x: again.x + 2, y: again.y + 10 }, { x: again.x + again.width + 20, y: again.y + 10 });
        await panel.click("[data-picture-add]");
        await X.until(() => X.chips(panel), (c) => c.length === 1 && c[0].label === "Drawing, one mark", "no picture in the clicked tab");
      } finally {
        await panel?.close();
        await narrow.close();
        fs.rmSync(dir, { recursive: true, force: true });
      }
    });

    await require("./browser-extension-close").closeChecks({ run, s, drawBar, stroke, sheet });

    await require("./browser-extension-update").updateChecks({ run, s, drawBar, stroke, picture, near, RED, GREEN });

    await run("the card with console entries and the picture on a 390x844 phone: a tap unfolds it within the width, the picture and the entries shown", async () => {
      const phone = await L.newMobile(browser);
      try {
        const tap = await phone.newPage();
        L.wirePage(tap, bag);
        await L.login(tap);
        await L.dismissUpdate(tap);
        for (const dark of [false, true]) {
          await tap.emulateMedia({ colorScheme: dark ? "dark" : "light" });
          await tap.goto(`${BASE}/assistants/${s.assistant}`, { waitUntil: "domcontentloaded" });
          await tap.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
          await tap.locator("[data-picked] [data-bs-toggle=collapse]").last().scrollIntoViewIfNeeded();
          await tap.locator("[data-picked] [data-bs-toggle=collapse]").last().tap();
          const seen = await X.until(() => tap.evaluate(() => {
            const el = [...document.querySelectorAll("[data-picked]")].pop();
            const img = el.querySelector("[data-picked-picture]");
            const body = el.querySelector("[data-picked-body]");
            const b = el.getBoundingClientRect();
            const ib = img?.getBoundingClientRect();
            return { open: getComputedStyle(body).display !== "none" && body.getBoundingClientRect().height > 0, inside: b.left >= 0 && b.right <= innerWidth + 0.5 && document.documentElement.scrollWidth <= innerWidth, picture: img && img.complete ? img.naturalWidth : 0, pictureInside: ib ? ib.width > 0 && ib.right <= b.right + 0.5 : false, lines: el.querySelectorAll("[data-picked-line]").length };
          }), (v) => v.open && v.picture > 0, "the card did not unfold on the phone");
          assert(seen.inside && seen.pictureInside && seen.lines >= 8, JSON.stringify(seen));
          if (SHOTS) await tap.screenshot({ path: `${SHOTS}/card-phone-${dark ? "dark" : "light"}.png`, fullPage: false });
        }
      } finally {
        await phone.close();
      }
    });

    await run("the panel with the console and the drawing in light and dark, read by hand from the screenshots", async () => {
      if (!SHOTS) return "EXT_SHOTS not set";
      await s.site.bringToFront();
      await s.panel.click("[data-draw]");
      await X.until(() => drawBar(s.panel), (d) => d.on, "Draw did not start");
      const again = await X.boxOf(s.site, "#again");
      await stroke(s.site, { x: again.x + 2, y: again.y + 40 }, { x: again.x + again.width + 40, y: again.y + 40 });
      for (const dark of [false, true]) {
        await s.panel.scheme(dark);
        await s.panel.shot(`${SHOTS}/panel-console-draw-${dark ? "dark" : "light"}.png`);
      }
      await s.panel.scheme(false);
      await s.site.screenshot({ path: `${SHOTS}/page-drawing.png` });
      await s.site.keyboard.press("Escape");
      return "screenshots written";
    });
  } catch (e) {
    results.push({ name: "[chromium] FATAL", status: "FAIL", detail: e.stack || e.message });
  } finally {
    try {
      await s.panel?.close();
      await s.ext?.close();
      if (s.dir) fs.rmSync(s.dir, { recursive: true, force: true });
      fs.rmSync(s.downloads, { recursive: true, force: true });
      await X.revokeToken(page, TOKEN_NAME);
      await X.revokeToken(page, SPARE_NAME);
      for (const one of s.made) await X.postAs(page, `/assistants/${one}`, { form: "delete" });
      if (s.coder) await X.postAs(page, `/coders/${s.coder}/delete`, {});
      await L.deleteProject(page, PROJECT);
    } catch (e) {
      results.push({ name: "[chromium] cleanup", status: "FAIL", detail: e.message });
    }
    sites.close();
    await ctx.close().catch(() => {});
    await browser.close().catch(() => {});
  }
  process.exit(L.report("BROWSER EXTENSION CONSOLE AND DRAWING", results, bag) ? 1 : 0);
}

main();
