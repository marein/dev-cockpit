const fs = require("fs");
const os = require("os");
const path = require("path");
const L = require("./lib");
const X = require("./browser-extension-fixture");
const { assert, sleep } = L;

// Not a runner of its own: browser-extension-console-draw.js runs these checks
// with its zip, each on a Chromium of its own with every page granted. The
// panel is closed through chrome.sidePanel.close, the way its X closes it.
// Chrome tells the worker from version 142 on; the copy whose api.js says
// otherwise takes the path of Firefox and older Chrome, the panel's port.

const { SITE_A, SITE_A_AGAIN } = X;
const NOTE = `Keep this note ${X.TAG}.`;

const stored = (ext, key) => ext.worker.evaluate(async (k) => (await chrome.storage.session.get(k))[k] ?? null, key);
const trayOf = (ext, tabId) => ext.worker.evaluate(async (k) => (await chrome.storage.local.get(k))[k] ?? null, `tray:${tabId}`);
const tabOf = (ext, url) => ext.worker.evaluate(async (u) => (await chrome.tabs.query({})).find((t) => t.url === u) ?? null, url);
const closePanel = (ext, windowId) => ext.worker.evaluate((id) => chrome.sidePanel.close({ windowId: id }), windowId);
const panelTargets = async (ext) => (await ext.session.send("Target.getTargets")).targetInfos.filter((t) => t.url.startsWith(`chrome-extension://${ext.id}/panel.html`));

async function openTab(ext, url) {
  const tab = await ext.context.newPage();
  await tab.goto(url);
  return { page: tab, ...(await tabOf(ext, url)) };
}

// The page counts a click and its pointerdown.
async function takesClicks(page, selector) {
  const before = await page.evaluate(() => window.clicks);
  await page.click(selector);
  return (await page.evaluate(() => window.clicks)) > before;
}

async function pickWithNote(ext, panel, pick) {
  await X.pickAt(pick.page, "#buy");
  await panel.click("[data-note]");
  await panel.type(NOTE);
  await X.until(() => trayOf(ext, pick.id), (t) => t && t.note === NOTE && t.items.length === 1, "the pick and the note did not reach the tray");
}

async function startDrawing(ext, panel, draw, { drawBar, stroke }) {
  await draw.page.bringToFront();
  await X.until(() => X.panelState(panel), (p) => !p.picking, "the panel did not follow the drawing tab");
  await panel.click("[data-draw]");
  await X.until(() => drawBar(panel), (d) => d.on, "Draw did not start");
  const fix = await X.boxOf(draw.page, "#fix");
  await stroke(draw.page, { x: fix.x - 10, y: fix.y + fix.height / 2 }, { x: fix.x + fix.width + 10, y: fix.y + fix.height / 2 });
  await X.until(() => stored(ext, `draw:${draw.id}`), (d) => d && d.on && d.count === 1, "the mark did not land");
}

async function endedInWindow(ext, pick, draw, { sheet }) {
  await X.until(() => stored(ext, `pick:${pick.id}`), (p) => p && !p.on && !p.resume, "picking went on after the panel closed");
  await X.until(() => stored(ext, `draw:${draw.id}`), (d) => d && !d.on && d.count === 1, "drawing went on after the panel closed, or its mark went");
  const tray = await trayOf(ext, pick.id);
  assert(tray && tray.note === NOTE && tray.items.length === 1 && tray.items[0].item.selector === "#buy", `the tray or the note went with the panel: ${JSON.stringify(tray)?.slice(0, 300)}`);
  const outline = await X.overlay(pick.page);
  assert(!outline.host || (!outline.glass.shown && !outline.hover.shown), `the picker stays over the page: ${JSON.stringify(outline)}`);
  assert(await takesClicks(pick.page, "#buy"), "the picking tab takes no click after the panel closed");
  const kept = await sheet(draw.page);
  assert(kept.host && kept.canvas && !kept.glass, `the drawing did not stay, or its glass did: ${JSON.stringify(kept)}`);
  assert(await takesClicks(draw.page, "#fix"), "the drawing tab takes no click after the panel closed");
}

function otherPanel() {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), `dc-ext-other-${X.TAG}-`));
  fs.writeFileSync(path.join(dir, "manifest.json"), JSON.stringify({ manifest_version: 3, name: "Another panel", version: "1.0", permissions: ["sidePanel"], action: {}, side_panel: { default_path: "other.html" }, background: { service_worker: "other.js" } }));
  fs.writeFileSync(path.join(dir, "other.html"), "<!doctype html><title>Another panel</title><p>Another panel</p>");
  fs.writeFileSync(path.join(dir, "other.js"), "chrome.sidePanel.setPanelBehavior({ openPanelOnActionClick: true });\n");
  return dir;
}

async function closeChecks({ run, s, drawBar, stroke, sheet }) {
  await run("closing the side panel ends picking and drawing in every tab of its window: the pages take clicks again, the drawing, the tray and the note stay, and the panel opened again shows neither mode but the pick and the note", async () => {
    const ext = await X.launchWith(s.dir);
    let panel = null;
    try {
      const pick = await openTab(ext, `${SITE_A}/plain`);
      const draw = await openTab(ext, `${SITE_A}/draw`);
      await pick.page.bringToFront();
      await X.press(ext, pick.page);
      panel = await X.Panel.open(ext.session, `chrome-extension://${ext.id}/panel.html`);
      await X.until(() => X.panelState(panel), (p) => p.picking, "the button did not start picking");
      await pickWithNote(ext, panel, pick);
      await startDrawing(ext, panel, draw, { drawBar, stroke });
      assert((await stored(ext, `pick:${pick.id}`)).on, "picking ended in the other tab when drawing began");
      await panel.close();
      panel = null;
      await closePanel(ext, pick.windowId);
      await X.until(() => panelTargets(ext), (list) => list.length === 0, "the side panel stayed open");
      await endedInWindow(ext, pick, draw, { sheet });
      await ext.worker.evaluate(() => chrome.sidePanel.setPanelBehavior({ openPanelOnActionClick: true }));
      await X.press(ext, draw.page);
      await ext.worker.evaluate(() => chrome.sidePanel.setPanelBehavior({ openPanelOnActionClick: false }));
      panel = await X.Panel.open(ext.session, `chrome-extension://${ext.id}/panel.html`);
      const bar = await X.until(() => drawBar(panel), (d) => d.shown && d.clear, "the panel opened again shows no drawing bar for the kept mark");
      assert(!bar.on, `the panel opened again says it draws: ${JSON.stringify(bar)}`);
      await pick.page.bringToFront();
      await X.until(() => drawBar(panel), (d) => !d.shown, "the panel did not follow the picking tab");
      const back = await X.panelState(panel);
      const chips = await X.chips(panel);
      assert(!back.picking && back.note === NOTE && chips.length === 1 && chips[0].title === "#buy", `the panel opened again says it picks, or lost the pick or the note: ${JSON.stringify({ back, chips })}`);
    } finally {
      await panel?.close();
      await ext.close();
    }
  });

  await run("another side panel taking the place of the extension's ends picking, and the page takes clicks again", async () => {
    const other = otherPanel();
    const ext = await X.launchWith(s.dir, { beside: [other] });
    let panel = null;
    try {
      const pick = await openTab(ext, `${SITE_A}/plain`);
      await X.press(ext, pick.page);
      panel = await X.Panel.open(ext.session, `chrome-extension://${ext.id}/panel.html`);
      await X.until(() => X.panelState(panel), (p) => p.picking, "the button did not start picking");
      const otherId = new URL((await X.until(() => ext.context.serviceWorkers().find((one) => one.url().endsWith("/other.js")), Boolean, "the other extension has no worker")).url()).host;
      const { targetInfos } = await ext.session.send("Target.getTargets", { filter: [{ type: "tab" }] });
      await ext.session.send("Extensions.triggerAction", { id: otherId, targetId: targetInfos.find((one) => one.url === `${SITE_A}/plain`).targetId });
      await X.until(() => stored(ext, `pick:${pick.id}`), (p) => p && !p.on, "picking went on behind the other panel");
      assert((await panel.eval("document.visibilityState")) === "hidden", "the extension's panel is not the one hidden");
      assert(await takesClicks(pick.page, "#buy"), "the page takes no click behind the other panel");
      await X.until(() => X.panelState(panel), (p) => !p.picking, "the hidden panel still says it picks");
    } finally {
      await panel?.close();
      await ext.close();
      fs.rmSync(other, { recursive: true, force: true });
    }
  });

  await run("as in Firefox and Chrome before 142, the panel's port ends picking and drawing in its window once it closes, the tray and the note stay, while the panel of another window picks on", async () => {
    const source = String(s.files["api.js"]);
    const told = "export const PANEL_CLOSED = Boolean(api.sidePanel?.onClosed);";
    assert(source.includes(told), "api.js no longer asks for Chrome's event");
    const dir = X.unpack(s.files, X.EVERY_PAGE, { "api.js": source.replace(told, "export const PANEL_CLOSED = false;") });
    const ext = await X.launchWith(dir);
    let panel = null;
    try {
      const pick = await openTab(ext, `${SITE_A}/plain`);
      const draw = await openTab(ext, `${SITE_A}/draw`);
      await pick.page.bringToFront();
      await X.press(ext, pick.page);
      panel = await X.Panel.open(ext.session, `chrome-extension://${ext.id}/panel.html`);
      await X.until(() => X.panelState(panel), (p) => p.picking, "the button did not start picking");
      await pickWithNote(ext, panel, pick);
      await startDrawing(ext, panel, draw, { drawBar, stroke });
      await panel.close();
      panel = null;
      await ext.session.send("Target.createTarget", { url: `${SITE_A_AGAIN}/plain`, newWindow: true });
      const awayPage = await X.until(() => ext.context.pages().find((one) => one.url() === `${SITE_A_AGAIN}/plain`), Boolean, "no second window");
      await awayPage.waitForLoadState("load");
      const away = { page: awayPage, ...(await tabOf(ext, `${SITE_A_AGAIN}/plain`)) };
      assert(away.windowId !== pick.windowId, "the second tab opened in the same window");
      await X.press(ext, away.page);
      await X.until(() => panelTargets(ext), (list) => list.length === 2, "the second window's panel did not open");
      await X.until(() => stored(ext, `pick:${away.id}`), (p) => p && p.on, "the button did not pick in the second window");
      await sleep(300);
      await closePanel(ext, pick.windowId);
      await X.until(() => panelTargets(ext), (list) => list.length === 1, "the first window's panel stayed open");
      await endedInWindow(ext, pick, draw, { sheet });
      assert((await stored(ext, `pick:${away.id}`)).on && (await X.overlay(away.page)).glass.shown, "picking ended in the window whose panel stayed open");
      await closePanel(ext, away.windowId);
      await X.until(() => stored(ext, `pick:${away.id}`), (p) => p && !p.on, "picking went on after the second panel closed");
    } finally {
      await panel?.close();
      await ext.close();
      fs.rmSync(dir, { recursive: true, force: true });
    }
  });
}

module.exports = { closeChecks };
