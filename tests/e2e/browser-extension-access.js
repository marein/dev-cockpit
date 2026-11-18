const fs = require("fs");
const L = require("./lib");
const X = require("./browser-extension-fixture");
const { assert, sleep } = L;

// Not a runner of its own: browser-extension.js runs these checks with its
// zip and its spare token, each on a Chromium of its own. A panel open before
// the setup follows it, and Chrome's Site access "On click", which withholds
// every page, set the way its settings of the extension set it.

const { ORIGIN, SITE_A } = X;
const NO_ACCESS = "The extension cannot pick or draw on this page. Browser pages and the extension stores stay closed to it, on other pages click its button in the toolbar or press its key.";

async function accessChecks({ run, s }) {
  await run("a panel open before the setup follows the saved setup at once, with no click into it", async () => {
    const dir = X.unpack(s.files, [ORIGIN]);
    const blank = await X.launchWith(dir);
    let panel = null;
    try {
      const tab = await blank.context.newPage();
      await tab.goto(`${SITE_A}/plain`);
      await X.press(blank, tab);
      panel = await X.Panel.open(blank.session, `chrome-extension://${blank.id}/panel.html`);
      const before = await X.until(() => X.panelState(panel), (p) => p.alert.includes("Set up the cockpit's address and a token first"), "the panel did not ask for the setup");
      assert(before.picking, JSON.stringify(before));
      const set = await X.setUp(blank, ORIGIN, s.spare, { every: false });
      assert(!set.error, set.said);
      await X.until(() => X.panelState(panel), (p) => !p.alert.includes("Set up"), "the open panel kept asking for the setup", 3000);
      await set.page.close();
      await tab.bringToFront();
      await X.until(() => X.panelState(panel), (p) => p.picking, "the panel did not come back to the picking tab");
      await X.pickAt(tab, "#buy");
      await X.until(async () => ({ chips: (await X.chips(panel)).length, send: await panel.eval(() => !document.querySelector("[data-send]").disabled) }), (p) => p.chips === 1 && p.send, "Send stayed off after the setup");
    } finally {
      await panel?.close();
      await blank.close();
      fs.rmSync(dir, { recursive: true, force: true });
    }
  });
  await run("with Site access On click, Pick in the panel says at once that the tab needs the button, one press of the button picks, a chip keeps its state where the address cannot be read and an open setup follows the access", async () => {
    const dir = X.unpack(s.files, X.EVERY_PAGE);
    const ext = await X.launchWith(dir);
    let panel = null;
    try {
      const first = await ext.context.newPage();
      await first.goto(`${SITE_A}/plain`);
      await X.press(ext, first);
      panel = await X.Panel.open(ext.session, `chrome-extension://${ext.id}/panel.html`);
      await X.until(() => X.panelState(panel), (p) => p.picking, "the button did not pick");
      await first.keyboard.press("Escape");
      const picked = await ext.context.newPage();
      await picked.goto(`${SITE_A}/second`);
      await picked.bringToFront();
      await X.until(() => X.chips(panel), (c) => c.length === 0, "the panel did not follow to the second tab");
      await panel.click("[data-pick]");
      await X.until(() => X.panelState(panel), (p) => p.picking, "Pick in the panel did not pick");
      await X.pickAt(picked, "#other");
      await X.until(() => X.chips(panel), (c) => c.length === 1, "no pick through Pick in the panel");
      await picked.keyboard.press("Escape");
      const set = await X.setUp(ext, ORIGIN, s.spare);
      assert(!set.error, set.said);
      const manager = await ext.context.newPage();
      await manager.goto(`chrome://extensions/?id=${ext.id}`);
      await manager.evaluate((id) => chrome.developerPrivate.updateExtensionConfiguration({ extensionId: id, hostAccess: "ON_CLICK" }), ext.id);
      await manager.close();
      await X.until(() => ext.worker.evaluate(() => chrome.permissions.contains({ origins: ["<all_urls>"] })), (every) => every === false, "Chrome kept the access to every page");
      await X.until(() => set.page.evaluate(() => ({ checked: document.querySelector("[data-every-page]").checked, disabled: document.querySelector("[data-every-page]").disabled })), (box) => !box.checked && !box.disabled, "the open setup did not follow the access");
      await set.page.close();
      const fresh = await ext.context.newPage();
      await fresh.goto(`${SITE_A}/plain?b=1`);
      await fresh.bringToFront();
      await X.until(() => X.panelState(panel), (p) => p.access && !p.picking, "the panel did not follow to the tab never clicked");
      await panel.click("[data-pick]");
      const refused = await X.until(() => X.panelState(panel), (p) => p.alert.includes("cannot pick"), "Pick in a tab never clicked said nothing", 3000);
      assert(!refused.picking && refused.alert.startsWith(NO_ACCESS), JSON.stringify(refused));
      await X.press(ext, fresh);
      await X.until(() => X.panelState(panel), (p) => p.picking, "one press of the button did not pick", 4000);
      await X.pickAt(fresh, "#buy");
      await X.until(() => X.chips(panel), (c) => c.length === 1, "no pick after one press");
      await fresh.keyboard.press("Escape");
      await picked.bringToFront();
      await X.until(() => X.chips(panel), (c) => c.length === 1 && c[0].label.startsWith("p#other"), "the panel did not come back to the tab picked through Pick");
      await panel.hover("[data-chips] .chip [data-chip-show]");
      await sleep(600);
      const chip = (await X.chips(panel))[0];
      assert(!chip.gone, `the chip turned dashed where its element stands: ${JSON.stringify(chip)}`);
    } finally {
      await panel?.close();
      await ext.close();
      fs.rmSync(dir, { recursive: true, force: true });
    }
  });
}

module.exports = { accessChecks };
