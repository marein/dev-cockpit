const fs = require("fs");
const path = require("path");
const L = require("./lib");
const X = require("./browser-extension-fixture");
const { assert, sleep } = L;

// Not a runner of its own: browser-extension-console-draw.js runs these checks
// with its zip and token, on a Chromium of their own. An update the way the
// docs tell it, the new zip into the same folder and Reload on
// chrome://extensions, the extension's errors listed there, then a start of
// the browser on the same profile and tabs open before access to every page.

const { ORIGIN, SITE_A } = X;
const UPDATED = "The extension was updated, reload the page to catch its console again.";
const NOT_CAUGHT = "Reload the page to catch its console.";
const NOT_WEB = "The console is caught on http and https pages.";

const consoleOf = (panel) => panel.eval(() => {
  const empty = document.querySelector("[data-console-empty]");
  const b = empty.getBoundingClientRect();
  return {
    sentence: getComputedStyle(empty).display !== "none" && b.height > 0 ? empty.textContent : "",
    rows: [...document.querySelectorAll("[data-console-list] .log:not(.mark) .log-text")].map((one) => one.textContent),
  };
});

const tabOf = (ext, url) => ext.worker.evaluate(async (u) => (await chrome.tabs.query({})).find((t) => t.url === u)?.id ?? -1, url);
const badge = (ext, tabId) => ext.worker.evaluate((id) => chrome.action.getBadgeText({ tabId: id }), tabId);
const held = (page, tag) => page.evaluate((name) => document.querySelectorAll(name).length, tag);
const keptKeys = (ext) => ext.worker.evaluate(async () => Object.keys(await chrome.storage.local.get(null)).filter((key) => /^(tray|log):/.test(key)).sort());
const pictureKeys = (ext) => ext.worker.evaluate(async () => {
  const db = await new Promise((resolve, reject) => {
    const r = indexedDB.open("dc-extension-pictures", 1);
    r.onupgradeneeded = () => r.result.createObjectStore("pictures");
    r.onsuccess = () => resolve(r.result);
    r.onerror = () => reject(r.error);
  });
  const keys = await new Promise((resolve) => {
    const r = db.transaction("pictures").objectStore("pictures").getAllKeys();
    r.onsuccess = () => resolve(r.result);
    r.onerror = () => resolve([]);
  });
  db.close();
  return keys;
});

async function manager(ext) {
  const page = await ext.context.newPage();
  await page.goto(`chrome://extensions/?id=${ext.id}`);
  return page;
}

// What Reload on chrome://extensions does when the folder holds a new
// version. Developer mode is on there, Load unpacked needs it, and Chrome
// collects an extension's errors only from its next load on.
async function reloadFolder(ext, dir) {
  const file = path.join(dir, "manifest.json");
  const manifest = JSON.parse(fs.readFileSync(file, "utf8"));
  manifest.version = `${manifest.version.split(".").slice(0, 3).join(".")}.1`;
  fs.writeFileSync(file, JSON.stringify(manifest, null, 2));
  const page = await manager(ext);
  await page.evaluate(() => chrome.developerPrivate.updateProfileConfiguration({ inDeveloperMode: true }));
  const fresh = ext.context.waitForEvent("serviceworker", { timeout: 15000 });
  await page.evaluate((id) => chrome.developerPrivate.reload(id, { failQuietly: true }), ext.id);
  ext.worker = await fresh;
  await page.close();
}

async function openPanel(ext, tab) {
  await X.press(ext, tab);
  const panel = await X.Panel.open(ext.session, `chrome-extension://${ext.id}/panel.html`);
  await X.until(() => X.panelState(panel), (p) => p.picking, "the button did not pick");
  await tab.keyboard.press("Escape");
  await X.until(() => X.panelState(panel), (p) => !p.picking, "Escape did not end picking");
  return panel;
}

async function updateChecks({ run, s, drawBar, stroke, picture, near, RED, GREEN }) {
  const u = { dir: X.unpack(s.files, X.EVERY_PAGE), ext: null, panel: null };
  const note = (tab) => `Note of the ${tab} tab ${X.TAG}`;
  try {
    await run("an update the way the docs say, the new zip into the same folder and Reload, keeps each tab's tray, note, drawing and console list with its badge, the list says the page needs a reload and catches again after it, and a closed tab's tray goes; a tab that was picking or drawing takes clicks again, picks and draws anew with no trace of the old drawing", async () => {
      u.ext = await X.launchWith(u.dir, { scale: 2 });
      const set = await X.setUp(u.ext, ORIGIN, s.token);
      assert(!set.error, set.said);
      await set.page.close();
      await X.until(() => u.ext.worker.evaluate(async () => (await chrome.scripting.getRegisteredContentScripts()).length), (n) => n === 2, "the console catch was never put into the pages");
      const errors = await u.ext.context.newPage();
      await errors.goto(`${SITE_A}/errors`);
      u.panel = await openPanel(u.ext, errors);
      await X.until(() => consoleOf(u.panel), (c) => c.rows.length === 7, "the errors page's console was not listed");
      await X.press(u.ext, errors);
      await X.until(() => X.panelState(u.panel), (p) => p.picking, "no picking");
      await X.pickAt(errors, "#again");
      await X.until(() => X.chips(u.panel), (c) => c.length === 1, "no pick");
      await u.panel.click("[data-note]");
      await u.panel.type(note("errors"));
      const plain = await u.ext.context.newPage();
      await plain.goto(`${SITE_A}/plain`);
      await plain.bringToFront();
      await X.until(() => X.chips(u.panel), (c) => c.length === 0, "the panel did not follow to the plain tab");
      await u.panel.click("[data-draw]");
      await X.until(() => drawBar(u.panel), (d) => d.on, "Draw did not start");
      const buy = await X.boxOf(plain, "#buy");
      await stroke(plain, { x: buy.x - 10, y: buy.y + buy.height / 2 }, { x: buy.x + buy.width + 10, y: buy.y + buy.height / 2 });
      await u.panel.click("[data-picture-add]");
      await X.until(() => X.chips(u.panel), (c) => c.length === 1 && c[0].label === "Drawing, one mark", "no drawing in the tray");
      await u.panel.click("[data-note]");
      await u.panel.type(note("plain"));
      await sleep(600);
      const errorsTab = await tabOf(u.ext, `${SITE_A}/errors`);
      const plainTab = await tabOf(u.ext, `${SITE_A}/plain`);
      const busy = await u.ext.worker.evaluate(async (keys) => chrome.storage.session.get(keys), [`pick:${errorsTab}`, `draw:${plainTab}`]);
      assert(busy[`pick:${errorsTab}`]?.on && busy[`draw:${plainTab}`]?.on && busy[`draw:${plainTab}`]?.count === 1, `before the update: ${JSON.stringify(busy)}`);
      const closed = Math.max(errorsTab, plainTab) + 999;
      await u.ext.worker.evaluate((key) => chrome.storage.local.set({ [key]: { note: "a tab closed while the extension was off", items: [], current: "", full: false, problem: "" } }), `tray:${closed}`);
      const pictures = await pictureKeys(u.ext);
      assert(pictures.length === 1 && (await badge(u.ext, errorsTab)) === "7", JSON.stringify({ pictures, badge: await badge(u.ext, errorsTab) }));
      await u.panel.close();
      u.panel = null;

      await reloadFolder(u.ext, u.dir);
      await X.until(() => keptKeys(u.ext), (keys) => !keys.includes(`tray:${closed}`), "the tray of a closed tab stayed");
      assert(JSON.stringify(await keptKeys(u.ext)) === JSON.stringify([`log:${errorsTab}`, `log:${plainTab}`, `tray:${errorsTab}`, `tray:${plainTab}`].sort()), JSON.stringify(await keptKeys(u.ext)));
      assert(JSON.stringify(await pictureKeys(u.ext)) === JSON.stringify(pictures), "the drawing's picture went with the update");
      assert((await badge(u.ext, errorsTab)) === "7", `the badge says ${await badge(u.ext, errorsTab)} after the update`);
      await errors.evaluate(() => console.error("Logged after the update"));

      await errors.bringToFront();
      await X.until(() => held(errors, "dc-extension-pick"), (n) => n === 0, "the old picker stayed in the page after the update");
      await errors.evaluate(() => {
        window.hits = [];
        document.addEventListener("click", (event) => window.hits.push(`${event.target.localName} ${event.defaultPrevented}`));
      });
      await X.pickAt(errors, "h1");
      await X.until(() => errors.evaluate(() => window.hits), (hits) => hits.length === 1, "a click after the update did not reach the page that was picking");
      assert(JSON.stringify(await errors.evaluate(() => window.hits)) === JSON.stringify(["h1 false"]), JSON.stringify(await errors.evaluate(() => window.hits)));
      await plain.bringToFront();
      await X.until(() => held(plain, "dc-extension-draw"), (n) => n === 0, "the old drawing stayed on the page after the update");
      const clicks = await plain.evaluate(() => window.clicks);
      await X.pickAt(plain, "#buy");
      await X.until(() => plain.evaluate(() => window.clicks), (n) => n === clicks + 1, "a click after the update did not reach the page that was drawing");

      u.panel = await openPanel(u.ext, errors);
      const kept = await X.until(async () => ({ chips: await X.chips(u.panel), state: await X.panelState(u.panel), console: await consoleOf(u.panel) }), (k) => k.chips.length === 1 && k.chips[0].label.startsWith("button#again") && k.state.note === note("errors") && k.console.rows.length === 7, "the errors tab lost its tray, note or console");
      assert(kept.chips[0].label.startsWith("button#again") && kept.state.note === note("errors") && kept.console.sentence === UPDATED, JSON.stringify(kept));
      await sleep(500);
      const later = await consoleOf(u.panel);
      assert(!later.rows.includes("Logged after the update") && later.sentence === UPDATED, JSON.stringify(later));
      await plain.bringToFront();
      const drawn = await X.until(async () => ({ chips: await X.chips(u.panel), state: await X.panelState(u.panel), console: await consoleOf(u.panel) }), (k) => k.chips.length === 1 && k.chips[0].label === "Drawing, one mark" && k.state.note === note("plain"), "the plain tab lost its tray or its note");
      assert(drawn.chips[0].label === "Drawing, one mark" && drawn.console.sentence === UPDATED, JSON.stringify(drawn));
      await u.panel.click("[data-chips] [data-chip-show]");
      await X.until(() => u.panel.eval(() => { const img = document.querySelector("[data-preview-image]"); return img && img.complete ? img.naturalWidth : 0; }), (w) => w > 0, "the drawing kept no picture");
      await u.panel.click("[data-preview-close]");
      await u.panel.click("[data-draw]");
      await X.until(() => drawBar(u.panel), (d) => d.on, "Draw did not start after the update");
      const card = await X.boxOf(plain, ".card");
      await stroke(plain, { x: card.x + 20, y: card.y + 8 }, { x: card.x + 220, y: card.y + 8 });
      await u.panel.click("[data-picture-add]");
      const two = await X.until(() => X.chips(u.panel), (c) => c.length === 2 && c[1].label === "Drawing, one mark", "the drawing after the update did not reach the tray");
      const button = await X.boxOf(plain, "#buy");
      const shot = await picture(u.ext, two[1].key, [{ x: card.x + 120, y: card.y + 8 }, { x: button.x + 4, y: button.y + button.height / 2 }]);
      assert(shot && near(shot.colors[0], RED) && near(shot.colors[1], GREEN), `the picture after the update: ${JSON.stringify(shot)}`);
      await u.panel.click("[data-draw-clear]");
      await X.until(() => drawBar(u.panel), (d) => d.on && !d.clear, "Clear left the drawing");
      await plain.keyboard.press("Escape");
      await X.until(() => held(plain, "dc-extension-draw"), (n) => n === 0, "the drawing stayed after Clear and Escape");

      await errors.bringToFront();
      await u.panel.click("[data-pick]");
      await X.until(() => X.panelState(u.panel), (p) => p.picking, "Pick in the panel did not pick after the update");
      await X.pickAt(errors, "h1");
      const picked = await X.until(() => X.chips(u.panel), (c) => c.length === 2, "Pick in the panel picked nothing after the update");
      assert(picked[1].label.startsWith("h1") && (await held(errors, "dc-extension-pick")) === 1, JSON.stringify(picked));
      await errors.keyboard.press("Escape");
      await X.until(() => X.panelState(u.panel), (p) => !p.picking, "Escape did not end picking after the update");

      await errors.reload();
      const caught = await X.until(() => consoleOf(u.panel), (c) => c.rows.length === 7 && c.sentence === "", "a reload did not catch the console again");
      assert(!caught.rows.includes("Logged after the update"), JSON.stringify(caught));
      await errors.evaluate(() => console.error("Logged after the reload"));
      await X.until(() => consoleOf(u.panel), (c) => c.rows.includes("Logged after the reload"), "the console was not caught after the reload");
      await X.until(() => badge(u.ext, errorsTab), (b) => b === "8", "the badge did not count on");
    });

    await run("with Developer mode on, chrome://extensions lists no console message of a page among the extension's errors, while the badge counts them", async () => {
      assert(u.ext, "the update check did not set up");
      const page = await manager(u.ext);
      const errors = await u.ext.context.newPage();
      await errors.goto(`${SITE_A}/errors?developer=1`);
      await errors.evaluate(() => console.error("Logged with Developer mode on"));
      const tab = await tabOf(u.ext, `${SITE_A}/errors?developer=1`);
      await X.until(() => badge(u.ext, tab), (b) => b === "8", "the page's console was not caught");
      await sleep(500);
      const listed = (await page.evaluate((id) => chrome.developerPrivate.getExtensionInfo(id), u.ext.id)).runtimeErrors.map((one) => `${one.message} (${one.source})`);
      assert(listed.length === 0, `chrome://extensions lists ${JSON.stringify(listed)}`);
      await page.close();
    });

    await run("a start of the browser on the same profile keeps no tray, note, picture or console list, a tab opened then shows none", async () => {
      assert(u.ext, "the update check did not set up");
      await u.panel?.close();
      u.panel = null;
      await u.ext.quit();
      u.ext = await X.launchWith(u.dir, { profile: u.ext.profile });
      await X.until(async () => ({ keys: await keptKeys(u.ext), pictures: await pictureKeys(u.ext) }), (k) => k.keys.length === 0 && k.pictures.length === 0, "trays, pictures or consoles outlived the start of the browser");
      const errors = await u.ext.context.newPage();
      await errors.goto(`${SITE_A}/errors`);
      u.panel = await openPanel(u.ext, errors);
      const fresh = await X.until(async () => ({ chips: await X.chips(u.panel), state: await X.panelState(u.panel), console: await consoleOf(u.panel) }), (k) => k.console.rows.length === 7, "the errors page was not caught after the start");
      assert(fresh.chips.length === 0 && fresh.state.note === "" && fresh.console.sentence === "", JSON.stringify(fresh));
    });

    await run("tabs open before access to every page is granted: the console list says the page needs a reload, a browser page says what is caught, a reload catches", async () => {
      assert(u.ext && u.panel, "the start check did not set up");
      const page = await manager(u.ext);
      await page.evaluate((id) => chrome.developerPrivate.updateExtensionConfiguration({ extensionId: id, hostAccess: "ON_CLICK" }), u.ext.id);
      await X.until(() => u.ext.worker.evaluate(() => chrome.permissions.contains({ origins: ["<all_urls>"] })), (every) => every === false, "Chrome kept the access to every page");
      const early = await u.ext.context.newPage();
      await early.goto(`${SITE_A}/errors?early=1`);
      const version = await u.ext.context.newPage();
      await version.goto("chrome://version");
      await page.evaluate((id) => chrome.developerPrivate.updateExtensionConfiguration({ extensionId: id, hostAccess: "ON_ALL_SITES" }), u.ext.id);
      await X.until(() => u.ext.worker.evaluate(() => chrome.permissions.contains({ origins: ["<all_urls>"] })), (every) => every === true, "Chrome did not grant every page");
      await page.close();
      await early.bringToFront();
      const before = await X.until(() => consoleOf(u.panel), (c) => c.sentence === NOT_CAUGHT, "the page open before the access did not say it needs a reload");
      assert(before.rows.length === 0, JSON.stringify(before));
      await version.bringToFront();
      await X.until(() => consoleOf(u.panel), (c) => c.sentence === NOT_WEB && c.rows.length === 0, "a browser page did not say what is caught");
      await early.bringToFront();
      await early.reload();
      await X.until(() => consoleOf(u.panel), (c) => c.rows.length === 7 && c.sentence === "", "a reload did not catch the page");
    });

  } finally {
    await u.panel?.close();
    await u.ext?.close();
    fs.rmSync(u.dir, { recursive: true, force: true });
  }
}

module.exports = { updateChecks };
