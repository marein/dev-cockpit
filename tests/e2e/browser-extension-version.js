const fs = require("fs");
const L = require("./lib");
const X = require("./browser-extension-fixture");
const { assert, BASE } = L;

// The extension's own version and the handshake (GET
// /api/browser-extension/version, the addon's cockpit.js `fit`), Chromium
// only. Each case loads the cockpit's Chrome zip unpacked with another
// version in its manifest or another API in its version.js, into a Chromium
// of its own: the same version, an older one, one newer than the cockpit
// offers, one whose API the cockpit no longer speaks and one whose API is
// newer than the cockpit's. Instance and sites as for browser-extension.js.

process.env.NODE_TLS_REJECT_UNAUTHORIZED = "0";
const { TAG, ORIGIN, SITE_A } = X;
const TOKEN_NAME = `Version ${TAG}`;

const shown = (selector) => `((el) => { if (!el) return ""; const st = getComputedStyle(el); const b = el.getBoundingClientRect(); return st.display !== "none" && st.visibility !== "hidden" && b.width > 0 && b.height > 0 ? el.innerText.trim() : ""; })(document.querySelector(${JSON.stringify(selector)}))`;

const panelVersion = (panel) => panel.eval(`({
  head: document.querySelector("[data-version]").textContent,
  notice: ${shown("[data-notice]")},
  noticeLink: document.querySelector("[data-notice] a")?.href || "",
  alert: ${shown("[data-alert]")},
  alertLink: document.querySelector("[data-alert] a")?.href || "",
  sendOff: document.querySelector("[data-send]").disabled,
  menu: ${shown("[data-menu]")},
})`);

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
  const s = { made: [], open: null };

  // The extension as the cockpit hands it out, with its manifest's version
  // and the API of its version.js swapped.
  async function variant(version, apiVersion) {
    const manifest = { ...JSON.parse(s.files["manifest.json"]), version };
    const files = { ...s.files, "manifest.json": JSON.stringify(manifest), "version.js": String(s.files["version.js"]).replace(/API = \d+;/, `API = ${apiVersion};`) };
    const dir = X.unpack(files, X.EVERY_PAGE);
    const ext = await X.launchWith(dir);
    s.open = { ext, dir, panel: null };
    const setup = await X.setUp(ext, ORIGIN, s.token);
    const link = await setup.page.evaluate(() => document.querySelector("[data-result] a")?.href || "");
    const head = await setup.page.textContent("[data-version]");
    await setup.page.close();
    const site = await ext.context.newPage();
    await site.goto(`${SITE_A}/plain`);
    await X.press(ext, site);
    s.open.panel = await X.Panel.open(ext.session, `chrome-extension://${ext.id}/panel.html`);
    await X.until(() => X.panelState(s.open.panel), (p) => p.picking, "the button did not pick");
    await X.pickAt(site, "#buy");
    await X.until(() => X.chips(s.open.panel), (c) => c.length === 1, "the pick made no chip");
    await site.keyboard.press("Escape");
    await X.until(() => X.panelState(s.open.panel), (p) => !p.picking, "Escape did not end picking");
    return { setup: { said: setup.said, error: setup.error, link, head }, panel: s.open.panel, site };
  }

  async function done() {
    if (!s.open) return;
    await s.open.panel?.close();
    await s.open.ext.close();
    fs.rmSync(s.open.dir, { recursive: true, force: true });
    s.open = null;
  }

  async function sends(panel, note) {
    await panel.click("[data-note]");
    await panel.type(note);
    await panel.click("[data-send]");
    await panel.waitFor("document.querySelectorAll('[data-menu] [role=menuitem]').length > 1", 8000, "the send menu");
    await panel.clickText("[data-menu] [role=menuitem]", s.title);
    const sent = await X.until(() => X.panelState(panel), (p) => p.status && !p.sending, "the send never ended", 15000);
    assert(sent.status === `Sent to ${s.title}. Open the assistant`, sent.status);
  }

  try {
    await L.login(page);
    await L.dismissUpdate(page);
    const made = await X.postAs(page, "/assistants/new", { coder: "claude" });
    s.assistant = JSON.parse(made.text).id;
    s.made.push(s.assistant);
    s.title = `Versions ${TAG}`;
    await X.postAs(page, `/assistants/${s.assistant}`, { form: "rename", title: s.title });
    s.token = await X.makeToken(page, TOKEN_NAME);
    const zip = await X.download(page, "chrome");
    s.files = X.unzip(zip.bytes);
    s.version = await page.textContent("#settings-browser-extension [data-browser-extension-version]");
    s.api = Number(String(s.files["version.js"]).match(/API = (\d+);/)?.[1]);
    s.zip = `${ORIGIN}/settings/browser/extension/chrome.zip`;

    await run("the handshake answers the extension version the cockpit offers and the API versions it speaks, to the token alone", async () => {
      const answer = await X.request("GET", "/api/browser-extension/version", { token: s.token });
      assert(answer.status === 200 && answer.json.extension === s.version && Array.isArray(answer.json.api) && answer.json.api.includes(s.api) && answer.json.api.every(Number.isInteger), answer.text);
      assert(/^\d+(\.\d+){0,3}$/.test(s.version) && JSON.parse(s.files["manifest.json"]).version === s.version && zip.name === `dev-cockpit-browser-extension-chrome-${s.version}.zip`, `${s.version} ${zip.name}`);
      const none = await X.request("GET", "/api/browser-extension/version");
      assert(none.status === 401 && !none.text.includes(s.version), `without the token: ${none.status} ${none.text.slice(0, 120)}`);
    });

    await run("the same version: the setup connects without a notice, the panel shows none and Send reaches the assistant", async () => {
      try {
        const same = await variant(s.version, s.api);
        assert(!same.setup.error && /^Connected\. The cockpit lists \d+ assistants? and \d+ running coders?\.$/.test(same.setup.said) && !same.setup.link && same.setup.head === `Version ${s.version}`, JSON.stringify(same.setup));
        const state = await X.until(() => panelVersion(same.panel), (p) => !p.sendOff, "Send stayed off");
        assert(state.head === s.version && state.notice === "" && state.alert === "", JSON.stringify(state));
        await sends(same.panel, `Same version ${TAG}`);
      } finally {
        await done();
      }
    });

    await run("an older version the cockpit's API still speaks: a quiet notice with the download in the setup and the panel, and Send still works", async () => {
      try {
        const old = await variant("0.9.0", s.api);
        assert(!old.setup.error && old.setup.said.startsWith("Connected. The cockpit lists ") && old.setup.said.endsWith(`. Version ${s.version} is available. Download`) && old.setup.link === s.zip && old.setup.head === "Version 0.9.0", JSON.stringify(old.setup));
        const state = await X.until(() => panelVersion(old.panel), (p) => p.notice, "the panel names no newer version");
        assert(state.notice === `Version ${s.version} is available. Download` && state.noticeLink === s.zip && state.alert === "" && !state.sendOff && state.head === "0.9.0", JSON.stringify(state));
        const look = await old.panel.eval(`((el) => ({ notice: getComputedStyle(el).color, body: getComputedStyle(document.body).color }))(document.querySelector("[data-notice]"))`);
        assert(look.notice !== look.body, `the notice is not quiet: ${JSON.stringify(look)}`);
        await sends(old.panel, `Older version ${TAG}`);
        const after = await panelVersion(old.panel);
        assert(after.notice === state.notice, JSON.stringify(after));
      } finally {
        await done();
      }
    });

    await run("a version newer than the cockpit offers on the same API: no notice, Send works", async () => {
      try {
        const newer = await variant("9.0.0", s.api);
        assert(!newer.setup.error && /^Connected\. The cockpit lists \d+ assistants? and \d+ running coders?\.$/.test(newer.setup.said) && !newer.setup.link, JSON.stringify(newer.setup));
        const state = await X.until(() => panelVersion(newer.panel), (p) => !p.sendOff, "Send stayed off");
        assert(state.notice === "" && state.alert === "" && state.head === "9.0.0", JSON.stringify(state));
        await sends(newer.panel, `Newer version ${TAG}`);
      } finally {
        await done();
      }
    });

    for (const [kind, apiVersion, sentence] of [
      ["an API the cockpit no longer speaks", s.api - 1, `This extension is too old for the cockpit, install version ${s.version}.`],
      ["an API newer than the cockpit speaks", s.api + 1, `This extension is too new for the cockpit, update the cockpit or install version ${s.version}.`],
    ]) {
      await run(`${kind}: one sentence with the download, Send stays off and nothing reaches the cockpit`, async () => {
        try {
          const before = (await X.request("GET", "/api/browser-extension/targets", { token: s.token })).json.assistants.length;
          const off = await variant(kind.includes("newer") ? "9.0.0" : "0.9.0", apiVersion);
          assert(off.setup.error && off.setup.said === `Connected. ${sentence} Download` && off.setup.link === s.zip, JSON.stringify(off.setup));
          const state = await X.until(() => panelVersion(off.panel), (p) => p.alert, "the panel says nothing");
          assert(state.alert === `${sentence} Download` && state.alertLink === s.zip && state.notice === "" && state.sendOff, JSON.stringify(state));
          await off.panel.click("[data-note]");
          await off.panel.type(`Refused ${TAG}`);
          await off.panel.key("Enter", "Enter", 13, 2);
          await off.panel.click("[data-send]");
          const after = await panelVersion(off.panel);
          assert(after.menu === "" && after.sendOff && (await X.panelState(off.panel)).status === "", JSON.stringify(after));
          const now = (await X.request("GET", "/api/browser-extension/targets", { token: s.token })).json.assistants.length;
          assert(now === before, `the assistants went from ${before} to ${now}`);
        } finally {
          await done();
        }
      });
    }

    await run("the thread holds the sends of the fitting versions alone", async () => {
      await page.goto(`${BASE}/assistants/${s.assistant}`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
      const notes = await X.until(() => page.evaluate((tag) => [...document.querySelectorAll("[data-assistant-message] [data-assistant-text]")].map((el) => el.innerText.trim()).filter((one) => one.includes(tag)), TAG), (list) => list.length >= 3, "the thread misses a send", 15000);
      assert(notes.length === 3 && notes.every((one) => /^(Same|Older|Newer) version /.test(one)), JSON.stringify(notes));
    });
  } catch (e) {
    results.push({ name: "[chromium] FATAL", status: "FAIL", detail: e.stack || e.message });
  } finally {
    try {
      await done();
      await X.revokeToken(page, TOKEN_NAME);
      for (const one of s.made) await X.postAs(page, `/assistants/${one}`, { form: "delete" });
    } catch (e) {
      results.push({ name: "[chromium] cleanup", status: "FAIL", detail: e.message });
    }
    sites.close();
    await ctx.close().catch(() => {});
    await browser.close().catch(() => {});
  }
  process.exit(L.report("BROWSER EXTENSION VERSIONS", results, bag) ? 1 : 0);
}

main();
