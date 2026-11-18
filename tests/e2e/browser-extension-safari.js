const fs = require("fs");
const L = require("./lib");
const X = require("./browser-extension-fixture");
const { assert, BASE, sleep } = L;

// The browser extension's Safari zip (internal/browserextension addon.go
// writes its manifest: the panel as the button's popup, no side panel), loaded
// unpacked into a Chromium of its own, which then takes Safari's code paths.
// Safari does not run on Linux: its prompt per website and the popup closing
// on a click into the page are left to a look by hand on a Mac. Instance and
// sites as for browser-extension.js. EXT_SHOTS: screenshots.

process.env.NODE_TLS_REJECT_UNAUTHORIZED = "0";
const { TAG, ORIGIN, SITE_A, SHOTS } = X;
const TOKEN_NAME = `Safari ${TAG}`;
const NOTE = `Safari keeps this note ${TAG}.`;
const SAFARI = "The extension cannot pick or draw on this page. Safari's own pages stay closed to it, a website opens once you allow it under Safari › Settings › Websites.";

const stored = (ext, key) => ext.worker.evaluate(async (k) => (await chrome.storage[/^(tray|log):/.test(k) ? "local" : "session"].get(k))[k] ?? null, key);
const tabs = (ext) => ext.worker.evaluate(async () => (await chrome.tabs.query({})).map((t) => ({ id: t.id, url: t.url || t.pendingUrl || "" })));
const badge = (ext, tabId) => ext.worker.evaluate((id) => chrome.action.getBadgeText({ tabId: id }), tabId);
const tabOf = async (ext, url) => (await tabs(ext)).find((t) => t.url === url)?.id ?? -1;

const popupTargets = async (ext) => (await ext.session.send("Target.getTargets")).targetInfos.filter((t) => t.url.startsWith(`chrome-extension://${ext.id}/panel.html`));

// Closing the popup stands in for Safari's click into the page.
async function openPopup(ext, site) {
  await X.press(ext, site);
  return X.Panel.open(ext.session, `chrome-extension://${ext.id}/panel.html`);
}

async function closePopup(ext, panel) {
  if (!panel) return;
  await panel.close();
  await ext.session.send("Target.closeTarget", { targetId: panel.targetId }).catch(() => {});
  await X.until(() => popupTargets(ext), (list) => list.length === 0, "the popup stayed");
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
  const s = { made: [], tokens: [TOKEN_NAME] };
  try {
    await L.login(page);
    await L.dismissUpdate(page);
    const made = await X.postAs(page, "/assistants/new", { coder: "claude" });
    s.assistant = JSON.parse(made.text).id;
    s.made.push(s.assistant);
    s.title = `Safari ${TAG}`;
    await X.postAs(page, `/assistants/${s.assistant}`, { form: "rename", title: s.title });
    s.token = await X.makeToken(page, TOKEN_NAME);
    s.zip = await X.download(page, "safari");
    s.files = X.unzip(s.zip.bytes);
    s.version = await page.textContent("#settings-browser-extension [data-browser-extension-version]");

    await run("Settings › Browser offers the extension for Safari beside Chrome and Firefox, a zip of the extension's version whose button opens the panel as a popup", async () => {
      const buttons = await page.evaluate(() => [...document.querySelectorAll("[data-browser-extension-zip]")].map((a) => { const b = a.getBoundingClientRect(); return { zip: a.dataset.browserExtensionZip, text: a.textContent.trim(), seen: getComputedStyle(a).display !== "none" && b.width > 0 && b.right <= innerWidth }; }));
      assert(JSON.stringify(buttons.map((b) => b.text)) === JSON.stringify(["For Chrome and Edge", "For Firefox", "For Safari"]) && buttons.every((b) => b.seen), JSON.stringify(buttons));
      const manifest = JSON.parse(s.files["manifest.json"]);
      const version = String(s.files["version.js"]);
      assert(/^\d+(\.\d+){0,3}$/.test(s.version) && s.zip.name === `dev-cockpit-browser-extension-safari-${s.version}.zip` && manifest.version === s.version && !manifest.version_name && /^export const API = \d+;$/m.test(version) && version.includes('BROWSER = "safari"'), `${s.zip.name} ${manifest.version} ${version}`);
      assert(manifest.action.default_popup === "panel.html" && manifest.background.service_worker === "background.js" && manifest.background.type === "module" && manifest.browser_specific_settings.safari.strict_min_version === "26.0", s.files["manifest.json"]);
      assert(!manifest.side_panel && !manifest.sidebar_action && !manifest.permissions.includes("sidePanel") && !manifest.host_permissions && !manifest.content_scripts && !manifest.web_accessible_resources && !manifest.externally_connectable, s.files["manifest.json"]);
      assert(s.files["pick.js"] && s.files["draw.js"] && s.files["console.js"] && s.files["browser-context.js"], JSON.stringify(Object.keys(s.files)));
    });

    await run("on a 390x844 phone Settings › Browser shows the three downloads within the width, and a tap on For Safari downloads its zip", async () => {
      const phone = await L.newMobile(browser);
      const tap = await phone.newPage();
      try {
        await L.login(tap);
        await L.dismissUpdate(tap);
        await tap.goto(`${BASE}/settings/browser#settings-browser-extension`, { waitUntil: "domcontentloaded" });
        const fits = await tap.evaluate(() => [...document.querySelectorAll("[data-browser-extension-zip]")].map((a) => { const b = a.getBoundingClientRect(); return getComputedStyle(a).display !== "none" && b.width > 0 && b.left >= 0 && b.right <= innerWidth; }));
        assert(fits.length === 3 && fits.every(Boolean) && (await tap.evaluate(() => innerWidth)) === 390, JSON.stringify(fits));
        const [file] = await Promise.all([tap.waitForEvent("download"), tap.tap("[data-browser-extension-zip=safari]")]);
        assert(file.suggestedFilename() === s.zip.name, file.suggestedFilename());
        if (SHOTS) await tap.screenshot({ path: `${SHOTS}/safari-settings-phone.png` });
      } finally {
        await phone.close();
      }
    });

    s.dir = X.unpack(s.files, X.EVERY_PAGE);
    s.ext = await X.launchWith(s.dir, { scale: 2 });

    await run("the setup names Safari's question on each website and the popup, connects, and names a newer version with the Safari zip", async () => {
      const ok = await X.setUp(s.ext, ORIGIN, s.token);
      const texts = await ok.page.evaluate(() => Object.fromEntries([...document.querySelectorAll("[data-every-safari], [data-use]")].map((n) => [n.dataset.use || "safari", getComputedStyle(n).display !== "none" && n.getBoundingClientRect().height > 0 ? n.textContent : ""])));
      assert(!ok.error && /^Connected\. The cockpit lists \d+ assistants? and \d+ running coders?\.$/.test(ok.said), ok.said);
      assert(/^Safari also asks per website, through the extension's button: allow it on every website/.test(texts.safari) && /to open the panel over the page/.test(texts.popup) && texts.panel === "", JSON.stringify(texts));
      await ok.page.close();
      const oldDir = X.unpack({ ...s.files, "manifest.json": JSON.stringify({ ...JSON.parse(s.files["manifest.json"]), version: "0.0.1" }) }, X.EVERY_PAGE);
      const old = await X.launchWith(oldDir);
      try {
        const said = await X.setUp(old, ORIGIN, s.token);
        const link = await said.page.evaluate(() => document.querySelector("[data-result] a")?.href || "");
        assert(!said.error && said.said.endsWith(`. Version ${s.version} is available. Download`) && link === `${ORIGIN}/settings/browser/extension/safari.zip`, `${said.said} ${link}`);
      } finally {
        await old.close();
        fs.rmSync(oldDir, { recursive: true, force: true });
      }
    });

    s.site = await s.ext.context.newPage();
    await s.site.goto(`${SITE_A}/plain`);

    await run("the button opens the tray of the tab as a popup without picking; Pick there picks, and the page sees no click", async () => {
      s.panel = await openPopup(s.ext, s.site);
      const first = await X.until(() => X.panelState(s.panel), (p) => p.hint, "the popup shows no hint");
      const size = await s.panel.eval(() => ({ popup: document.documentElement.classList.contains("popup"), width: document.body.getBoundingClientRect().width, pick: document.querySelector("[data-pick]").title }));
      assert(!first.picking && first.hint === "Click Pick, then click an element in the page." && first.access === "" && size.popup && size.width === 380, JSON.stringify({ first, size }));
      assert(size.pick === "Pick elements in this tab, Escape or this button stops", `the Pick button says ${size.pick}`);
      await sleep(500);
      assert(!(await X.panelState(s.panel)).picking, "the button started picking");
      if (SHOTS) for (const dark of [false, true]) { await s.panel.scheme(dark); await s.panel.shot(`${SHOTS}/safari-popup-${dark ? "dark" : "light"}.png`); }
      await s.panel.scheme(false);
      await s.panel.click("[data-pick]");
      await X.until(() => X.panelState(s.panel), (p) => p.picking, "Pick did not start picking");
      await X.pickAt(s.site, "#buy");
      const chips = await X.until(() => X.chips(s.panel), (c) => c.length === 1, "no chip");
      const clicks = await s.site.evaluate(() => [window.clicks, window.seenClicks]);
      assert(/#buy/.test(chips[0].label) && clicks[0] === 0 && clicks[1] === 0, JSON.stringify({ chips, clicks }));
      await s.panel.click("[data-pick]");
      await X.until(() => X.panelState(s.panel), (p) => !p.picking, "picking never ended");
    });

    await run("a popup closed while picking leaves picking on: a click into the page still picks and reaches no page, the popup opened again says it picks", async () => {
      await s.panel.click("[data-pick]");
      await X.until(() => X.panelState(s.panel), (p) => p.picking, "Pick did not start picking");
      await closePopup(s.ext, s.panel);
      s.panel = null;
      await sleep(500);
      const tab = await tabOf(s.ext, s.site.url());
      const before = (await stored(s.ext, `tray:${tab}`)).items[0].rev;
      assert((await stored(s.ext, `pick:${tab}`)).on, "picking ended with the popup");
      await X.pickAt(s.site, "#buy");
      await X.until(() => stored(s.ext, `tray:${tab}`), (t) => t.items.length === 1 && t.items[0].rev > before, "a click into the page after the popup closed picked nothing");
      const clicks = await s.site.evaluate(() => [window.clicks, window.seenClicks]);
      assert(clicks[0] === 0 && clicks[1] === 0, `the page saw the click: ${JSON.stringify(clicks)}`);
      s.panel = await openPopup(s.ext, s.site);
      await X.until(() => X.panelState(s.panel), (p) => p.picking, "the popup opened again does not say it picks");
      await s.panel.click("[data-pick]");
      await X.until(() => X.panelState(s.panel), (p) => !p.picking, "picking never ended");
    });

    await run("a popup closed right after typing keeps the note with its tab, and over another tab shows that tab's own tray", async () => {
      await s.panel.click("[data-note]");
      await s.panel.type(NOTE);
      await sleep(150);
      await closePopup(s.ext, s.panel);
      s.other = await s.ext.context.newPage();
      await s.other.goto(`${SITE_A}/second`);
      s.panel = await openPopup(s.ext, s.other);
      const there = await X.until(() => X.panelState(s.panel), (p) => p.hint, "the popup over the other tab shows nothing");
      assert(there.note === "" && (await X.chips(s.panel)).length === 0, JSON.stringify(there));
      await closePopup(s.ext, s.panel);
      await s.other.close();
      s.panel = await openPopup(s.ext, s.site);
      const back = await X.until(() => X.panelState(s.panel), (p) => p.note === NOTE, "the note was lost with the popup");
      const chips = await X.chips(s.panel);
      assert(chips.length === 1 && /#buy/.test(chips[0].label), JSON.stringify({ back, chips }));
    });

    await run("Send from the popup reaches the assistant, the thread shows the note with the folded card from the extension and the token's name", async () => {
      await s.panel.click("[data-send]");
      await s.panel.waitFor("document.querySelectorAll('[data-menu] [role=menuitem]').length > 1", 8000, "the send menu");
      await s.panel.clickText("[data-menu] [role=menuitem]", s.title);
      const done = await X.until(() => X.panelState(s.panel), (p) => p.status.startsWith("Sent to"), "the send never ended", 15000);
      assert(done.status === `Sent to ${s.title}. Open the assistant` && done.note === "" && (await X.chips(s.panel)).length === 0, JSON.stringify(done));
      await page.goto(`${BASE}/assistants/${s.assistant}`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
      const card = await X.until(() => page.evaluate((note) => {
        const el = [...document.querySelectorAll("[data-picked]")].find((one) => one.closest("[data-assistant-message]").querySelector("[data-assistant-text]").innerText.trim() === note);
        if (!el) return null;
        const b = el.getBoundingClientRect();
        return { head: el.querySelector("[data-picked-fold] .fw-medium")?.innerText.trim() || "", via: el.querySelector("[data-picked-via]")?.textContent.trim() || "", seen: getComputedStyle(el).display !== "none" && b.height > 0 };
      }, NOTE), (c) => c && c.seen, "the thread shows no card", 15000);
      assert(card.head.startsWith("From the extension") && card.via === `via ${TOKEN_NAME}`, JSON.stringify(card));
      await closePopup(s.ext, s.panel);
      s.panel = null;
    });

    await run("the popup lists the console of the tab from its first script on, and the badge counts it", async () => {
      const errors = await s.ext.context.newPage();
      await errors.goto(`${SITE_A}/errors`);
      const tab = await tabOf(s.ext, `${SITE_A}/errors`);
      s.panel = await openPopup(s.ext, errors);
      const list = await X.until(() => s.panel.eval(() => {
        const box = document.querySelector("[data-console-list]");
        const b = box.getBoundingClientRect();
        return { seen: getComputedStyle(box).display !== "none" && b.height > 0, rows: [...box.querySelectorAll(".log")].map((row) => row.querySelector(".log-text")?.textContent || ""), summary: document.querySelector("[data-console-summary]").textContent };
      }), (l) => l.rows.length >= 7, "the popup listed too little");
      assert(list.seen && list.rows[0] === "Uncaught Error: Head boom before anything" && list.summary === "6 errors, 1 warning" && (await badge(s.ext, tab)) === "7", JSON.stringify({ list, badge: await badge(s.ext, tab) }));
      await closePopup(s.ext, s.panel);
      s.panel = null;
      await errors.close();
    });

    await run("Draw from the popup draws on the page, and Add to the tray takes the picture into the tab's tray", async () => {
      const drawn = await s.ext.context.newPage();
      await drawn.goto(`${SITE_A}/draw`);
      await drawn.bringToFront();
      const tab = await tabOf(s.ext, `${SITE_A}/draw`);
      s.panel = await openPopup(s.ext, drawn);
      await s.panel.click("[data-draw]");
      await X.until(() => stored(s.ext, `draw:${tab}`), (d) => d && d.on, "Draw did not start");
      const fix = await X.boxOf(drawn, "#fix");
      const y = fix.y + fix.height / 2;
      await drawn.mouse.move(fix.x - 20, y);
      await drawn.mouse.down();
      await drawn.mouse.move(fix.x + fix.width + 20, y, { steps: 8 });
      await drawn.mouse.up();
      await X.until(() => stored(s.ext, `draw:${tab}`), (d) => d.count === 1, "no mark");
      await s.panel.click("[data-picture-add]");
      const said = await X.until(() => X.panelState(s.panel), (p) => /tray|picture|page/i.test(p.status), "Add to the tray said nothing", 15000);
      const tray = await stored(s.ext, `tray:${tab}`);
      const image = tray.items.find((one) => one.item.kind === "image");
      assert(said.status === "The drawing is in the tray." && image && image.item.marks.length === 1 && /#fix/.test(JSON.stringify(image.item.marks)), JSON.stringify({ said, tray: JSON.stringify(tray).slice(0, 400) }));
      await closePopup(s.ext, s.panel);
      s.panel = null;
      await drawn.close();
    });

    await run("Pick on a page closed to the extension says Safari's sentence", async () => {
      const closedPage = await s.ext.context.newPage();
      await closedPage.goto("chrome://version");
      s.panel = await openPopup(s.ext, closedPage);
      await s.panel.click("[data-pick]");
      const said = await X.until(() => X.panelState(s.panel), (p) => p.alert, "Pick refused nothing");
      assert(said.alert === SAFARI && !said.picking, JSON.stringify(said));
      await closePopup(s.ext, s.panel);
      s.panel = null;
      await closedPage.close();
    });
  } catch (e) {
    results.push({ name: "[chromium] FATAL", status: "FAIL", detail: e.stack || e.message });
  } finally {
    try {
      await s.panel?.close();
      await s.ext?.close();
      if (s.dir) fs.rmSync(s.dir, { recursive: true, force: true });
      for (const name of s.tokens) await X.revokeToken(page, name);
      for (const one of s.made) await X.postAs(page, `/assistants/${one}`, { form: "delete" });
    } catch (e) {
      results.push({ name: "[chromium] cleanup", status: "FAIL", detail: e.message });
    }
    sites.close();
    await ctx.close().catch(() => {});
    await browser.close().catch(() => {});
  }
  process.exit(L.report("BROWSER EXTENSION IN SAFARI'S SHAPE", results, bag) ? 1 : 0);
}

main();
