#!/usr/bin/env node
const fs = require("fs");
const path = require("path");
const L = require("./lib");
const { Feed, releaseChecks } = require("./external-plugins-releases");
const { optionalChecks } = require("./external-plugins-optional");
const { assert, sleep, BASE } = L;

// External plugins through the example plugin examples/plugins/hello
// (Python) on a throwaway instance. external-plugins-releases.js adds the
// installs from a repository, external-plugins-optional.js with DISTRO=1 the
// optional compiled plugins. Routes /settings/plugins, /settings/plugins/new
// and /plugins/<id>/, elements dc-plugin-list and dc-plugin-region.
//
// Needs the plugins folder read write, the example read only, the host path
// of the example and the notify inbox, which takes a coder signal the plugin
// reads off its event stream:
//   -v <state-dir>/external-plugins:/plugins -e PLUGINS_DIR=/plugins
//   -v $PWD/examples/plugins/hello:/example:ro -e EXAMPLE=/example
//   -e EXAMPLE_HOST=$PWD/examples/plugins/hello
//   -v <state-dir>/notification-inbox:/inbox -e INBOX=/inbox
// The fake forge listens on PLUGIN_FEED_PORT (3187), published on the host's
// loopback, where the instance reaches it at PLUGIN_FEED_ADDR (127.0.0.1):
// -p 127.0.0.1:3187:3187, the instance started with
// DEV_COCKPIT_PLUGIN_API_URL=http://127.0.0.1:3187 and
// DEV_COCKPIT_PLUGIN_CHECK_EVERY=3s.
// With DISTRO=1 the instance runs examples/distro and its state directory is
// mounted read only: -e DISTRO=1 -v <state-dir>:/state:ro -e STATE=/state.
// The instance must hold no plugin named hello, hello-drop, broken-* or
// silent before the run, the runner removes them at its end.
//
// Gotchas:
// - a hung plugin is found by two missed probes ten seconds apart, that
//   check waits up to 40 seconds per engine.
// - WebKit on a phone has no touch scroll and no wheel in Playwright, the
//   scroll there goes through the keyboard on the focused list.

const PLUGINS_DIR = process.env.PLUGINS_DIR || "/plugins";
const EXAMPLE = process.env.EXAMPLE || "/example";
const EXAMPLE_HOST = process.env.EXAMPLE_HOST || "";
const INBOX = process.env.INBOX || "";
const DISTRO = process.env.DISTRO === "1";
const ROW = (id) => `[data-plugin-row="${id}"]`;

const shown = (page, selector) => page.evaluate((sel) => {
  const el = document.querySelector(sel);
  if (!el) return false;
  const box = el.getBoundingClientRect();
  if (box.width === 0 || box.height === 0) return false;
  for (let n = el; n instanceof Element; n = n.parentElement) {
    const style = getComputedStyle(n);
    if (style.visibility === "hidden" || style.display === "none" || style.opacity === "0") return false;
  }
  return true;
}, selector);

async function waitShown(page, selector, want = true, timeout = 15000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    if (await shown(page, selector).catch(() => false) === want) return;
    await sleep(100);
  }
  throw new Error(`${selector} ${want ? "never showed" : "never went away"}`);
}

async function rowState(page, id) {
  return page.evaluate((sel) => document.querySelector(sel)?.dataset.pluginState || "", ROW(id));
}

async function waitRow(page, id, state, timeout = 20000) {
  const deadline = Date.now() + timeout;
  let last = "";
  while (Date.now() < deadline) {
    last = await rowState(page, id).catch(() => "");
    if (last === state) return;
    await sleep(150);
  }
  throw new Error(`the row of ${id} stands ${last || "nowhere"}, want ${state}`);
}

async function plugins(page) {
  await page.goto(`${BASE}/settings/plugins`, { waitUntil: "domcontentloaded" });
  await L.dismissUpdate(page);
  await page.waitForSelector("dc-plugin-list [data-plugin-list]");
  await L.waitUpgraded(page, ["dc-plugin-list"]);
}

// act runs a row action and answers the ms until its flash.
async function act(page, id, action, { tap = false } = {}) {
  const button = page.locator(`${ROW(id)} [data-plugin-action="${action}"]`);
  await button.scrollIntoViewIfNeeded();
  const began = Date.now();
  await Promise.all([
    page.waitForFunction(() => /Plugin \w+\./.test(document.querySelector(".dc-notice-text")?.textContent || ""), null, { timeout: 15000 }),
    tap ? button.tap() : button.click(),
  ]);
  return Date.now() - began;
}

// A stop waits for the plugin to end, not for the 3 second grace.
const QUICK_STOP = 2500;

async function removePlugin(page, id, { tap = false } = {}) {
  await plugins(page);
  if (!(await page.locator(ROW(id)).count())) return 0;
  const button = page.locator(`${ROW(id)} [data-plugin-action="remove"]`);
  await button.scrollIntoViewIfNeeded();
  if (tap) await button.tap(); else await button.click();
  const began = Date.now();
  await L.confirmSwal(page);
  await page.waitForFunction((sel) => !document.querySelector(sel), ROW(id), { timeout: 15000 });
  const took = Date.now() - began;
  assert(/Plugin removed\./.test(await page.locator(".dc-notice-text").first().innerText()), "the removal flashed nothing");
  assert(!fs.existsSync(path.join(PLUGINS_DIR, id)), `Remove left the directory of ${id}`);
  return took;
}

async function pageNotice(page, id) {
  const sel = `dc-plugin-region[data-region="page-${id}"] [data-plugin-notice]`;
  return await shown(page, sel) ? (await page.locator(sel).innerText()).trim() : "";
}

async function waitNotice(page, id, pattern, timeout = 15000) {
  const deadline = Date.now() + timeout;
  let last = "";
  while (Date.now() < deadline) {
    last = await pageNotice(page, id).catch(() => "");
    if (pattern.test(last)) return;
    await sleep(100);
  }
  throw new Error(`the page of ${id} says "${last}", want ${pattern}`);
}

// copyExample installs the example as id. Element tags are <id>-<name>, so
// the markup follows the id.
function copyExample(id) {
  const dest = path.join(PLUGINS_DIR, id);
  fs.cpSync(EXAMPLE, dest, { recursive: true });
  fs.chmodSync(path.join(dest, "hello.py"), 0o755);
  const manifest = JSON.parse(fs.readFileSync(path.join(dest, "plugin.json"), "utf8"));
  manifest.id = id;
  fs.writeFileSync(path.join(dest, "plugin.json"), JSON.stringify(manifest));
  for (const name of ["page.html", "slot.html"]) {
    const file = path.join(dest, name);
    fs.writeFileSync(file, fs.readFileSync(file, "utf8").replace(/(<\/?)hello-/g, `$1${id}-`));
  }
  return dest;
}

function writeBroken(id, manifest, files = {}) {
  const dir = path.join(PLUGINS_DIR, id);
  fs.mkdirSync(dir, { recursive: true });
  fs.writeFileSync(path.join(dir, "plugin.json"), typeof manifest === "string" ? manifest : JSON.stringify(manifest));
  for (const [name, content] of Object.entries(files)) {
    fs.mkdirSync(path.dirname(path.join(dir, name)), { recursive: true });
    if (content.link) fs.symlinkSync(content.link, path.join(dir, name));
    else fs.writeFileSync(path.join(dir, name), content.text, { mode: content.mode || 0o644 });
  }
}

const minimal = (id, extra = {}) => ({ protocol: 1, id, name: id, version: "0.1.0", run: "run.sh", ...extra });

const call = (page, url, init = {}) => page.evaluate(async ({ url, init }) => {
  const began = performance.now();
  const res = await fetch(url, { credentials: "same-origin", ...init });
  return { status: res.status, text: await res.text(), ms: performance.now() - began };
}, { url, init });

const csrf = (page) => page.evaluate(() => document.querySelector('meta[name="csrf-token"]')?.content || "");

async function fail(page, how) {
  const token = await csrf(page);
  return call(page, "/plugins/hello/fail", { method: "POST", headers: { "Content-Type": "application/json", "X-CSRF-Token": token }, body: JSON.stringify({ how }) });
}

async function scrollDown(page, engine, scroller) {
  const before = await page.evaluate((sel) => document.querySelector(sel).scrollTop, scroller);
  const box = await page.locator(scroller).boundingBox();
  const x = box.x + box.width / 2;
  if (engine === "chromium") {
    const cdp = await page.context().newCDPSession(page);
    const from = box.y + box.height * 0.8;
    const to = box.y + box.height * 0.2;
    await cdp.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ x, y: from }] });
    for (let i = 1; i <= 8; i += 1) {
      await cdp.send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ x, y: from + ((to - from) * i) / 8 }] });
      await sleep(30);
    }
    await sleep(250);
    await cdp.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
    await cdp.detach();
  } else {
    await page.evaluate((sel) => { const s = document.querySelector(sel); s.tabIndex = -1; s.focus(); }, scroller);
    await page.keyboard.press("PageDown");
  }
  await sleep(400);
  return (await page.evaluate((sel) => document.querySelector(sel).scrollTop, scroller)) - before;
}

const feed = new Feed();

L.runFeature("external-plugins", async ({ engine, browser, page, run, mobilePage }) => {
  if (!feed.server.listening) await feed.listen();
  assert(fs.existsSync(path.join(EXAMPLE, "plugin.json")), `no example at ${EXAMPLE}`);
  assert(fs.existsSync(PLUGINS_DIR), `no plugins folder at ${PLUGINS_DIR}`);
  const projects = [];
  const second = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1360, height: 900 } });
  const watcher = await second.newPage();
  L.wirePage(watcher, { consoleErrors: [], pageErrors: [], cdnNoise: [] });

  await run("the page warns once, names the folder and refuses a relative path at its field", async () => {
    await plugins(page);
    const text = await page.locator(".dc-work-body").innerText();
    assert((text.match(/runs as your user/g) || []).length === 1, "the warning is not there exactly once");
    assert(/frontend code runs in the cockpit page/.test(text), "the warning does not name the frontend code");
    assert(/external-plugins/.test(await page.locator("[data-plugin-folder]").innerText()), "the folder is not named");
    await page.fill("#plugin-path", "relative/hello");
    await Promise.all([page.waitForSelector("[data-plugin-path-error]"), page.click("[data-plugin-install-button]")]);
    assert(await page.inputValue("#plugin-path") === "relative/hello", "the refused path was not kept");
    assert(/absolute path/.test(await page.locator("[data-plugin-path-error]").innerText()), "the refusal does not say why");
    await waitShown(page, "[data-plugin-path-error]");
  });

  await run("the example installs from its host path, runs, and a second page follows live", async () => {
    assert(EXAMPLE_HOST, "EXAMPLE_HOST is not set");
    await L.login(watcher);
    await watcher.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(watcher);
    await watcher.evaluate(() => { window.__stayed = true; });
    await plugins(page);
    await page.fill("#plugin-path", EXAMPLE_HOST);
    await Promise.all([page.waitForURL(/\/settings\/plugins/), page.click("[data-plugin-install-button]")]);
    await waitRow(page, "hello", "running");
    await waitShown(page, `${ROW("hello")} [data-plugin-badge]`);
    assert(/Hello\s+1\.0\.0/.test(await page.locator(`${ROW("hello")} [data-plugin-name]`).locator("..").innerText()), "the row names no version");
    await waitShown(watcher, ".dc-ctx-tools hello-button button[data-hello-button]");
    await waitShown(watcher, '.dc-rail a[href="/plugins/hello/"]');
    assert(await watcher.evaluate(() => window.__stayed === true), "the second page reloaded");
  });

  await run("disable takes the button and the entry off the other page, enable brings them back", async () => {
    await plugins(page);
    const took = await act(page, "hello", "disable");
    assert(took < QUICK_STOP, `disable took ${took}ms`);
    await waitRow(page, "hello", "disabled");
    await waitShown(watcher, ".dc-ctx-tools hello-button", false);
    await waitShown(watcher, '.dc-rail a[href="/plugins/hello/"]', false);
    const gone = await call(page, "/plugins/hello/notes");
    assert(gone.status === 404, `a disabled plugin's route answered ${gone.status}`);
    await act(page, "hello", "enable");
    await waitRow(page, "hello", "running");
    await waitShown(watcher, ".dc-ctx-tools hello-button button");
    await waitShown(watcher, '.dc-rail a[href="/plugins/hello/"]');
    assert(await watcher.evaluate(() => window.__stayed === true), "the second page reloaded");
  });

  await run("restart and the log", async () => {
    await plugins(page);
    const took = await act(page, "hello", "restart");
    assert(took < QUICK_STOP, `restart took ${took}ms`);
    await waitRow(page, "hello", "running");
    await Promise.all([page.waitForURL(/\/settings\/plugins\/hello\/log/), page.click(`${ROW("hello")} [data-plugin-action="log"]`)]);
    await waitShown(page, "[data-plugin-log]");
    assert(/hello is listening/.test(await page.locator("[data-plugin-log]").innerText()), "the log misses the plugin's output");
  });

  await run("the page and the slot arrive filled with their markup before any plugin module runs", async () => {
    const blocked = /\/plugins\/hello\/(elements|assets\/).*\.js(\?|$)/;
    const aborted = [];
    await page.route(blocked, (route) => { aborted.push(route.request().url()); return route.abort(); });
    try {
      const res = await page.goto(`${BASE}/plugins/hello/`, { waitUntil: "domcontentloaded" });
      const html = await res.text();
      assert(/<hello-board class="d-block dc-narrow" data-notes-url="\/plugins\/hello\/notes"[^>]*>\s*<form /.test(html), "the response carries no board markup with its base filled in");
      await waitShown(page, "hello-board input[name=text]");
      await waitShown(page, "hello-board button[type=submit]");
      assert(await page.evaluate(() => !customElements.get("hello-board")), "the board was defined although its module was blocked");
      assert(/Write a note/.test(await page.getAttribute("hello-board input[name=text]", "placeholder")), "the field is not the markup's");
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await waitShown(page, ".dc-ctx-tools hello-button button[data-hello-button]");
      assert(await page.evaluate(() => !customElements.get("hello-button")), "the button was defined although its module was blocked");
      assert(aborted.length > 0, "no plugin module was asked for, the block proves nothing");
    } finally {
      await page.unroute(blocked);
    }
  });

  await run("the plugin's own page on a second page says it is off, live and after a reload", async () => {
    const own = await second.newPage();
    L.wirePage(own, { consoleErrors: [], pageErrors: [], cdnNoise: [] });
    try {
      await own.goto(`${BASE}/plugins/hello/`, { waitUntil: "domcontentloaded" });
      await waitShown(own, "hello-board input[name=text]");
      await own.evaluate(() => { window.__stayed = true; });
      await plugins(page);
      await act(page, "hello", "disable");
      await waitNotice(own, "hello", /^Hello is not running\.$/);
      await waitShown(own, "hello-board", false);
      assert(await own.evaluate(() => window.__stayed === true), "the plugin's page reloaded");
      const res = await own.reload({ waitUntil: "domcontentloaded" });
      assert(res.status() === 200, `the disabled plugin's page answered ${res.status()}`);
      await waitNotice(own, "hello", /^Hello is not running\.$/);
      assert(/Hello notes/.test(await own.locator(".dc-work-title").innerText()), "the reloaded page lost its title");
      await act(page, "hello", "enable");
      await waitShown(own, "hello-board input[name=text]");
      assert(await pageNotice(own, "hello") === "", "the notice stayed beside the board");
    } finally {
      await own.close();
    }
  });

  await run("the page mounts its board and a typed note lands in the list", async () => {
    await page.goto(`${BASE}/plugins/hello/`, { waitUntil: "domcontentloaded" });
    await waitShown(page, "hello-board input");
    const note = `desktop note ${engine}`;
    await page.click("hello-board input");
    await page.keyboard.type(note);
    await page.click("hello-board button[type=submit]");
    await page.waitForFunction((n) => [...document.querySelectorAll("[data-hello-note]")].some((r) => r.textContent === n), note, { timeout: 8000 });
    assert(await page.locator('.dc-rail a.active[href="/plugins/hello/"]').count() === 1, "the rail does not mark the page");
  });

  await run("a coder's signal reaches the plugin's event stream and its page", async () => {
    assert(INBOX, "INBOX is not set");
    const target = `xpl-event-${engine}-${Date.now().toString(36)}`;
    const dir = path.join(INBOX, "copilot");
    fs.mkdirSync(dir, { recursive: true });
    fs.writeFileSync(path.join(dir, `${target}.tmp`), JSON.stringify({ session_id: target, hook_event_name: "Stop" }));
    fs.renameSync(path.join(dir, `${target}.tmp`), path.join(dir, `${target}.json`));
    const deadline = Date.now() + 15000;
    for (;;) {
      await page.goto(`${BASE}/plugins/hello/`, { waitUntil: "domcontentloaded" });
      await waitShown(page, "hello-board input");
      const listed = await page.waitForFunction((t) => [...document.querySelectorAll("[data-hello-events] .list-group-item")].some((r) => r.textContent.includes(t)), target, { timeout: 2000 }).then(() => true, () => false);
      if (listed) break;
      assert(Date.now() < deadline, "the coder event never reached the plugin's page");
    }
    assert(/coder: Coder ended its turn/.test(await page.locator("[data-hello-events]").innerText()), "the event is not worded as the cockpit words it");
  });

  await run("a proxied request carries no cookie and no CSRF token, a POST without one never arrives", async () => {
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    const token = await csrf(page);
    const got = await call(page, "/plugins/hello/echo", { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded", "X-CSRF-Token": token }, body: `csrf_token=${encodeURIComponent(token)}&note=kept` });
    const seen = JSON.parse(got.text);
    assert(got.status === 200 && seen.cookie === null && seen.csrf === null && seen.body === "note=kept", `the plugin saw ${got.text}`);
    const refused = await call(page, "/plugins/hello/notes", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ text: "no token" }) });
    assert(refused.status === 403, `a POST without the token answered ${refused.status}`);
    const notes = JSON.parse((await call(page, "/plugins/hello/notes")).text).notes;
    assert(!notes.includes("no token"), "the refused POST reached the plugin");
  });

  await run("the slot's button creates a project through the cockpit", async () => {
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    const name = `xpl-${engine}-${Date.now() % 100000}`;
    await page.click(".dc-ctx-tools hello-button button");
    await page.waitForSelector(".swal2-input", { state: "visible" });
    await page.click(".swal2-input");
    await page.keyboard.type(name);
    assert(await page.inputValue(".swal2-input") === name, "the name did not land in the prompt");
    await page.click(".swal2-confirm");
    projects.push(name);
    await page.waitForSelector(`[data-index-project="${name}"]`, { timeout: 15000 });
  });

  await run("a crash shows why, empties the slot, answers 503 at once and comes back", async () => {
    await plugins(page);
    const asked = await fail(page, "exit");
    assert(asked.status === 200, `the crash request answered ${asked.status}`);
    await waitRow(page, "hello", "crashed", 5000);
    const reason = await page.locator(`${ROW("hello")} [data-plugin-reason]`).innerText();
    assert(/status 3/.test(reason), `the crash says ${reason}`);
    await waitShown(page, `${ROW("hello")} [data-plugin-tail]`);
    const down = await call(page, "/plugins/hello/notes");
    assert(down.status === 503 && /Hello is not running/.test(down.text) && down.ms < 1500, `a crashed route answered ${down.status} after ${Math.round(down.ms)}ms`);
    await waitShown(watcher, ".dc-ctx-tools hello-button", false, 5000);
    await waitRow(page, "hello", "running");
    await waitShown(watcher, ".dc-ctx-tools hello-button button");
  });

  await run("an answer that is not HTTP is a clear 502", async () => {
    const got = await fail(page, "garbage");
    assert(got.status === 502 && /not HTTP/.test(got.text), `garbage answered ${got.status} ${got.text}`);
    assert(await rowState(page, "hello") === "running", "one garbage answer took the plugin down");
  });

  await run("a plugin that stops answering is ended and started again", async () => {
    await plugins(page);
    await fail(page, "hang");
    await waitRow(page, "hello", "crashed", 40000);
    assert(/stopped answering/.test(await page.locator(`${ROW("hello")} [data-plugin-reason]`).innerText()), "the hang is not named");
    await waitRow(page, "hello", "running", 20000);
  });

  await run("a broken manifest and a plugin that never answers hold up no page", async () => {
    writeBroken("broken-json", "{ not json");
    writeBroken("silent", minimal("silent"), { "run.sh": { text: "#!/bin/sh\nexec sleep 300\n", mode: 0o755 } });
    await plugins(page);
    await waitRow(page, "broken-json", "broken");
    assert(/is invalid/.test(await page.locator(`${ROW("broken-json")} [data-plugin-reason]`).innerText()), "the broken manifest does not say why");
    await waitRow(page, "silent", "starting");
    const began = Date.now();
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await waitShown(page, ".dc-ctx-tools hello-button button");
    assert(Date.now() - began < 5000, `the projects page took ${Date.now() - began}ms beside a silent plugin`);
    await removePlugin(page, "broken-json");
    await removePlugin(page, "silent");
  });

  await run("a clashing id and an asset that leaves the directory mark the plugin broken", async () => {
    writeBroken("broken-id", minimal("hello"), { "run.sh": { text: "#!/bin/sh\nexit 0\n", mode: 0o755 } });
    writeBroken("broken-asset", minimal("broken-asset", { assets: "assets" }), { "run.sh": { text: "#!/bin/sh\nexit 0\n", mode: 0o755 }, "assets/passwd": { link: "/etc/passwd" } });
    await plugins(page);
    await waitRow(page, "broken-id", "broken");
    assert(/but the manifest id is hello/.test(await page.locator(`${ROW("broken-id")} [data-plugin-reason]`).innerText()), "the clash is not named");
    await waitRow(page, "broken-asset", "broken");
    assert(/leaves the plugin directory/.test(await page.locator(`${ROW("broken-asset")} [data-plugin-reason]`).innerText()), "the escaping asset is not named");
    assert(await rowState(page, "hello") === "running", "a clash took the real plugin down");
    await removePlugin(page, "broken-id");
    await removePlugin(page, "broken-asset");
  });

  await run("a directory dropped into the folder shows up live and leaves with Remove", async () => {
    await plugins(page);
    await page.evaluate(() => { window.__stayed = true; });
    copyExample("hello-drop");
    await waitRow(page, "hello-drop", "running");
    await waitShown(watcher, ".dc-ctx-tools hello-drop-button button");
    assert(await page.evaluate(() => window.__stayed === true), "the page reloaded");
    await removePlugin(page, "hello-drop");
    assert(!fs.existsSync(path.join(PLUGINS_DIR, "hello-drop")), "Remove left the directory");
    await waitShown(watcher, ".dc-ctx-tools hello-drop-button", false);
  });

  await run("phone: the Plugins page with taps, typing and a scroll", async () => {
    const m = await mobilePage();
    for (const id of ["broken-phone-a", "broken-phone-b"]) writeBroken(id, "{ not json");
    await plugins(m);
    await waitRow(m, "broken-phone-b", "broken");
    await m.tap("#plugin-path");
    await m.keyboard.type("not/absolute");
    await m.tap("[data-plugin-install-button]");
    await waitShown(m, "[data-plugin-path-error]");
    const moved = await scrollDown(m, engine, ".dc-work-body");
    assert(moved > 20, `the page scrolled ${moved}px`);
    await act(m, "hello", "disable", { tap: true });
    await waitRow(m, "hello", "disabled");
    await waitShown(watcher, ".dc-ctx-tools hello-button", false);
    await act(m, "hello", "enable", { tap: true });
    await waitRow(m, "hello", "running");
    await waitShown(m, `${ROW("hello")} [data-plugin-badge]`);
    await waitShown(watcher, ".dc-ctx-tools hello-button button");
    await removePlugin(m, "broken-phone-a", { tap: true });
    await removePlugin(m, "broken-phone-b", { tap: true });
  });

  await run("phone: the slot's button in the projects sheet creates a project", async () => {
    const m = await mobilePage();
    await m.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(m);
    await m.tap('.dc-tabbar button[data-ctx-area="projects"]');
    await waitShown(m, "dc-ctx-sheet hello-button button");
    await m.tap("dc-ctx-sheet hello-button button");
    await m.waitForSelector(".swal2-input", { state: "visible" });
    const name = `xplm-${engine}-${Date.now() % 100000}`;
    await m.tap(".swal2-input");
    await m.keyboard.type(name);
    await m.tap(".swal2-confirm");
    projects.push(name);
    await waitShown(m, `dc-ctx-sheet [data-index-project="${name}"]`);
  });

  await run("phone: the page through the Cockpit sheet, a typed note and a scrolled list", async () => {
    const m = await mobilePage();
    await m.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await m.tap('.dc-tabbar button[data-ctx-area="cockpit"]');
    await waitShown(m, 'dc-ctx-sheet a[href="/plugins/hello/"]');
    await Promise.all([m.waitForURL(/\/plugins\/hello\/$/), m.tap('dc-ctx-sheet a[href="/plugins/hello/"]')]);
    await waitShown(m, "hello-board input");
    for (let i = 0; i < 14; i += 1) {
      await m.tap("hello-board input");
      await m.keyboard.type(`phone note ${engine} ${i}`);
      await m.tap("hello-board button[type=submit]");
      await m.waitForFunction((n) => [...document.querySelectorAll("[data-hello-note]")].some((r) => r.textContent === n), `phone note ${engine} ${i}`, { timeout: 8000 });
    }
    const moved = await scrollDown(m, engine, ".dc-work-body");
    assert(moved > 20, `the notes scrolled ${moved}px`);
  });

  await releaseChecks({ L, engine, page, run, mobilePage, second, feed, EXAMPLE, EXAMPLE_HOST, PLUGINS_DIR, ROW, shown, waitShown, waitRow, plugins, removePlugin, call, csrf, scrollDown });

  if (DISTRO) await optionalChecks({ L, engine, page, run, mobilePage, second, INBOX, PLUGINS_DIR, waitShown, plugins, act, call, csrf, scrollDown, waitNotice, pageNotice, writeBroken, minimal });

  await run("cleanup: the projects and the plugin go, its open page says it is not installed anymore", async () => {
    for (const name of projects) await L.deleteProject(page, name);
    const own = await second.newPage();
    L.wirePage(own, { consoleErrors: [], pageErrors: [], cdnNoise: [] });
    try {
      await own.goto(`${BASE}/plugins/hello/`, { waitUntil: "domcontentloaded" });
      await waitShown(own, "hello-board input[name=text]");
      const took = await removePlugin(page, "hello");
      assert(took < QUICK_STOP, `remove took ${took}ms`);
      assert(!fs.existsSync(path.join(PLUGINS_DIR, "hello")), "the plugin's directory stayed");
      await waitShown(watcher, ".dc-ctx-tools hello-button", false);
      await waitNotice(own, "hello", /^The plugin is not installed anymore\.$/);
      await waitShown(own, "hello-board", false);
      const res = await own.reload({ waitUntil: "domcontentloaded" });
      assert(res.status() === 404, `the removed plugin's page answered ${res.status()}`);
      await waitNotice(own, "hello", /^The plugin is not installed anymore\.$/);
    } finally {
      await own.close();
    }
  });
  await second.close();
});
