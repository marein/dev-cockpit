// Optional compiled plugin checks of external-plugins.js with DISTRO=1,
// never run on their own. The instance runs examples/distro: stamp is always
// on, pulse is optional and ships off. Pulse beats into its state directory
// from a goroutine and adds a setting and a memory file, the runner reads
// them in the instance's state directory, mounted read only at STATE.
const fs = require("fs");
const path = require("path");

const STATE = process.env.STATE || "";

const BUILTIN = (id) => `[data-plugin-builtin] [data-plugin-row="${id}"]`;

function readText(file) {
  try {
    return fs.readFileSync(file, "utf8");
  } catch {
    return null;
  }
}

function setting(key) {
  const raw = readText(path.join(STATE, "settings.json"));
  if (raw === null) return undefined;
  return JSON.parse(raw)[key];
}

const heartbeat = () => readText(path.join(STATE, "plugins", "pulse", "heartbeat"));
const memory = () => readText(path.join(STATE, "assistant", "memory", "pulse.md"));

async function optionalChecks({ L, engine, page, run, mobilePage, second, INBOX, PLUGINS_DIR, waitShown, plugins, act, call, csrf, scrollDown, waitNotice, pageNotice, writeBroken, minimal }) {
  const { assert, sleep, BASE } = L;
  const watcher = await second.newPage();
  const own = await second.newPage();
  for (const p of [watcher, own]) L.wirePage(p, { consoleErrors: [], pageErrors: [], cdnNoise: [] });
  const fresh = STATE && !fs.existsSync(path.join(STATE, "plugins", "pulse"));

  const builtInState = (p, id) => p.evaluate((sel) => document.querySelector(sel)?.dataset.pluginState || "", BUILTIN(id));
  async function waitBuiltIn(p, id, want, timeout = 15000) {
    const deadline = Date.now() + timeout;
    let last = "";
    while (Date.now() < deadline) {
      last = await builtInState(p, id).catch(() => "");
      if (last === want) return;
      await sleep(150);
    }
    throw new Error(`the built in row of ${id} stands ${last || "nowhere"}, want ${want}`);
  }

  // signal drops a copilot signal into the inbox, plugins get it as a coder
  // event.
  function signal(target) {
    const dir = path.join(INBOX, "copilot");
    fs.mkdirSync(dir, { recursive: true });
    fs.writeFileSync(path.join(dir, `${target}.tmp`), JSON.stringify({ session_id: target, hook_event_name: "Stop" }));
    fs.renameSync(path.join(dir, `${target}.tmp`), path.join(dir, `${target}.json`));
  }

  async function signedIn(p) {
    await p.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    if (/\/login/.test(new URL(p.url()).pathname)) await L.login(p);
  }

  async function heard(target) {
    const got = await call(page, "/plugins/pulse/events");
    return got.status === 200 && JSON.parse(got.text).events.some((line) => line.includes(target));
  }

  async function stillHeartbeat(ms) {
    const before = heartbeat();
    await sleep(ms);
    return before === heartbeat();
  }

  const stamp = `xopt-${engine}-${Date.now().toString(36)}`;
  const onTarget = `${stamp}-on`;
  const offTarget = `${stamp}-off`;

  await run("built in: stamp is always on, pulse is listed off, and nothing of it runs or is written", async () => {
    assert(STATE, "STATE is not set");
    await plugins(page);
    await waitShown(page, BUILTIN("stamp"));
    const stampRow = page.locator(BUILTIN("stamp"));
    assert(/Always on/.test(await stampRow.locator("[data-plugin-badge]").innerText()), "stamp's badge does not say Always on");
    assert(/Built in, always on/.test(await stampRow.innerText()), "stamp's row is not marked built in");
    assert(await stampRow.locator("[data-plugin-action]").count() === 0, "stamp offers an action");
    await waitBuiltIn(page, "pulse", "disabled");
    const pulseRow = page.locator(BUILTIN("pulse"));
    assert(/Built in, optional/.test(await pulseRow.innerText()), "pulse's row is not marked built in and optional");
    const actions = await pulseRow.locator("[data-plugin-action]").evaluateAll((els) => els.map((el) => el.dataset.pluginAction));
    assert(actions.join() === "enable", `pulse offers ${actions.join()}, want enable alone`);
    assert(await page.locator(".dc-rail a[href=\"/plugins/pulse/\"]").count() === 0, "the rail holds pulse");
    const route = await call(page, "/plugins/pulse/beats");
    assert(route.status === 404, `pulse's route answered ${route.status} while off`);
    const ping = await call(page, "/plugins/stamp/ping");
    assert(ping.status === 200 && /^stamp \//.test(ping.text), `stamp answered ${ping.status} ${ping.text}`);
    if (fresh) {
      assert(!fs.existsSync(path.join(STATE, "plugins", "pulse")), "pulse made its state directory while off");
      assert(memory() === null, "pulse wrote its memory while off");
      assert(setting("pulse-greeting") === undefined, "pulse wrote its setting while off");
    } else {
      assert(await stillHeartbeat(1200), "pulse beats while off");
    }
  });

  await run("built in: Enable brings pulse's entry, button and page to the other pages live, it beats and writes", async () => {
    await signedIn(watcher);
    await L.dismissUpdate(watcher);
    const answered = await own.goto(`${BASE}/plugins/pulse/`, { waitUntil: "domcontentloaded" });
    assert(answered.status() === 200, `pulse's page answered ${answered.status()} while off`);
    await waitNotice(own, "pulse", /^pulse is not running\.$/);
    for (const p of [watcher, own]) await p.evaluate(() => { window.__stayed = true; });
    await plugins(page);
    await act(page, "pulse", "enable");
    await waitBuiltIn(page, "pulse", "running");
    assert(/Running/.test(await page.locator(`${BUILTIN("pulse")} [data-plugin-badge]`).innerText()), "pulse's badge does not say Running");
    await waitShown(watcher, ".dc-ctx-tools pulse-button a[data-pulse-button][data-pulse-ready]");
    await waitShown(watcher, '.dc-rail a[href="/plugins/pulse/"]');
    await waitShown(own, "pulse-board input[name=text]");
    assert(await pageNotice(own, "pulse") === "", "the notice stayed beside the board");
    await own.waitForFunction(() => /\d+ beats so far\./.test(document.querySelector("[data-pulse-beats]")?.textContent || ""), null, { timeout: 8000 });
    for (const p of [watcher, own]) assert(await p.evaluate(() => window.__stayed === true), "a page reloaded");
    const first = Number(heartbeat());
    await sleep(800);
    assert(Number(heartbeat()) > first, "pulse does not beat while on");
    assert(/pulse plugin/.test(memory() || ""), "pulse wrote no memory");
    assert(setting("pulse-greeting") === "Hello from pulse", `the setting reads ${setting("pulse-greeting")}`);
  });

  await run("built in: a line typed on pulse's page lands and an event while it is on reaches it", async () => {
    const line = `desktop line ${engine}`;
    await own.click("pulse-board input[name=text]");
    await own.keyboard.type(line);
    await own.click("pulse-board button[type=submit]");
    await own.waitForFunction((n) => [...document.querySelectorAll("[data-pulse-line]")].some((r) => r.textContent === n), line, { timeout: 8000 });
    assert(INBOX, "INBOX is not set");
    signal(onTarget);
    const deadline = Date.now() + 15000;
    while (!(await heard(onTarget))) {
      assert(Date.now() < deadline, "the coder event never reached pulse");
      await sleep(300);
    }
  });

  await run("built in: Disable takes pulse off every page live, its page says so, nothing beats and what it wrote stays", async () => {
    await plugins(page);
    await act(page, "pulse", "disable");
    await waitBuiltIn(page, "pulse", "disabled");
    await waitShown(watcher, ".dc-ctx-tools pulse-button", false);
    await waitShown(watcher, '.dc-rail a[href="/plugins/pulse/"]', false);
    await waitNotice(own, "pulse", /^pulse is not running\.$/);
    await waitShown(own, "pulse-board", false);
    for (const p of [watcher, own]) assert(await p.evaluate(() => window.__stayed === true), "a page reloaded");
    const route = await call(page, "/plugins/pulse/beats");
    assert(route.status === 404, `pulse's route answered ${route.status} once off`);
    assert(await stillHeartbeat(1200), "pulse beats on after its switch off");
    assert(/pulse plugin/.test(memory() || ""), "the switch off took pulse's memory");
    assert(setting("pulse-greeting") === "Hello from pulse", "the switch off took pulse's setting");
    signal(offTarget);
    const res = await own.reload({ waitUntil: "domcontentloaded" });
    assert(res.status() === 200, `pulse's page answered ${res.status()} after a reload while off`);
    await waitNotice(own, "pulse", /^pulse is not running\.$/);
  });

  await run("built in: switched on again pulse heard nothing while it was off", async () => {
    await sleep(1500);
    await plugins(page);
    await act(page, "pulse", "enable");
    await waitBuiltIn(page, "pulse", "running");
    await waitShown(own, "pulse-board input[name=text]");
    assert(await heard(onTarget), "the event heard while on is gone");
    assert(!(await heard(offTarget)), "pulse heard an event while it was off");
    await act(page, "pulse", "disable");
    await waitBuiltIn(page, "pulse", "disabled");
  });

  await run("built in: neither is restarted or removed, stamp does not switch, and no external plugin takes pulse's id", async () => {
    await plugins(page);
    const token = await csrf(page);
    const post = (form) => call(page, "/settings/plugins", { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: `csrf_token=${encodeURIComponent(token)}&${form}` });
    for (const [form, want] of [["form=remove&id=pulse", /pulse is built in/], ["form=restart&id=pulse", /pulse is built in/], ["form=disable&id=stamp", /stamp is built in and always on\./], ["form=remove&id=stamp", /stamp is built in and always on\./]]) {
      const got = await post(form);
      assert(got.status === 409 && want.test(got.text), `${form} answered ${got.status}`);
    }
    writeBroken("pulse", minimal("pulse"), { "run.sh": { text: "#!/bin/sh\nexit 0\n", mode: 0o755 } });
    const clash = '[data-plugin-installed] [data-plugin-row="pulse"]';
    await waitShown(page, clash);
    await page.waitForFunction((sel) => document.querySelector(sel)?.dataset.pluginState === "broken", clash, { timeout: 15000 });
    assert(/A built in plugin has the id pulse\./.test(await page.locator(`${clash} [data-plugin-reason]`).innerText()), "the clash is not named");
    assert(await builtInState(page, "pulse") === "disabled", "the clash moved the built in pulse");
    await page.locator(`${clash} [data-plugin-action="remove"]`).click();
    await L.confirmSwal(page);
    await waitShown(page, clash, false);
    assert(!fs.existsSync(path.join(PLUGINS_DIR, "pulse")), "Remove left the clashing directory");
    await waitShown(page, BUILTIN("pulse"));
  });

  await run("built in phone: Enable and Disable tapped with a second page following, the page through the Cockpit sheet, a typed line and a scroll", async () => {
    const m = await mobilePage();
    await plugins(m);
    await watcher.evaluate(() => { window.__stayed = true; });
    const enable = m.locator(`${BUILTIN("pulse")} [data-plugin-action="enable"]`);
    await enable.scrollIntoViewIfNeeded();
    await Promise.all([
      m.waitForFunction(() => /Plugin enabled\./.test(document.querySelector(".dc-notice-text")?.textContent || ""), null, { timeout: 15000 }),
      enable.tap(),
    ]);
    await waitBuiltIn(m, "pulse", "running");
    await waitShown(watcher, ".dc-ctx-tools pulse-button a[data-pulse-button]");
    await waitShown(watcher, '.dc-rail a[href="/plugins/pulse/"]');
    await m.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await m.tap('.dc-tabbar button[data-ctx-area="cockpit"]');
    await waitShown(m, 'dc-ctx-sheet a[href="/plugins/pulse/"]');
    await Promise.all([m.waitForURL(/\/plugins\/pulse\/$/), m.tap('dc-ctx-sheet a[href="/plugins/pulse/"]')]);
    await waitShown(m, "pulse-board input[name=text]");
    for (let i = 0; i < 12; i += 1) {
      const line = `phone line ${engine} ${i}`;
      await m.tap("pulse-board input[name=text]");
      await m.keyboard.type(line);
      await m.tap("pulse-board button[type=submit]");
      await m.waitForFunction((n) => [...document.querySelectorAll("[data-pulse-line]")].some((r) => r.textContent === n), line, { timeout: 8000 });
    }
    const moved = await scrollDown(m, engine, ".dc-work-body");
    assert(moved > 20, `the lines scrolled ${moved}px`);
    await plugins(m);
    const disable = m.locator(`${BUILTIN("pulse")} [data-plugin-action="disable"]`);
    await disable.scrollIntoViewIfNeeded();
    await Promise.all([
      m.waitForFunction(() => /Plugin disabled\./.test(document.querySelector(".dc-notice-text")?.textContent || ""), null, { timeout: 15000 }),
      disable.tap(),
    ]);
    await waitBuiltIn(m, "pulse", "disabled");
    await waitShown(watcher, ".dc-ctx-tools pulse-button", false);
    await waitShown(watcher, '.dc-rail a[href="/plugins/pulse/"]', false);
    assert(await watcher.evaluate(() => window.__stayed === true), "the second page reloaded");
  });

  await watcher.close();
  await own.close();
}

module.exports = { optionalChecks };
