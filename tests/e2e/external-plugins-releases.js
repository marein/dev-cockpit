// Release checks of external-plugins.js, never run on their own. A fake
// forge in the runner serves the release feeds of github.com, GitHub
// Enterprise and GitLab hosts, keyed by Host header and path. The instance
// sends every release request there through DEV_COCKPIT_PLUGIN_API_URL,
// keeping the Host header, and checks every DEV_COCKPIT_PLUGIN_CHECK_EVERY.
// PLUGIN_FEED_PORT and PLUGIN_FEED_ADDR, see external-plugins.js.
const crypto = require("crypto");
const fs = require("fs");
const http = require("http");
const path = require("path");
const zlib = require("zlib");

const FEED_PORT = Number(process.env.PLUGIN_FEED_PORT || 3187);
const FEED_ADDR = process.env.PLUGIN_FEED_ADDR || "127.0.0.1";

// tarball packs files, each { name, body, mode }, as a gzipped ustar.
function tarball(files) {
  const blocks = [];
  for (const file of files) {
    const header = Buffer.alloc(512);
    header.write(file.name, 0, 100, "utf8");
    header.write(`${(file.mode || 0o644).toString(8).padStart(7, "0")}\0`, 100);
    header.write("0000000\0", 108);
    header.write("0000000\0", 116);
    header.write(`${file.body.length.toString(8).padStart(11, "0")}\0`, 124);
    header.write(`${Math.floor(Date.now() / 1000).toString(8).padStart(11, "0")}\0`, 136);
    header.write("        ", 148);
    header.write("0", 156);
    header.write("ustar\0", 257);
    header.write("00", 263);
    let sum = 0;
    for (const byte of header) sum += byte;
    header.write(`${sum.toString(8).padStart(6, "0")}\0 `, 148);
    blocks.push(header, file.body, Buffer.alloc((512 - (file.body.length % 512)) % 512));
  }
  blocks.push(Buffer.alloc(1024));
  return zlib.gzipSync(Buffer.concat(blocks));
}

function walk(dir, base = "") {
  return fs.readdirSync(path.join(dir, base), { withFileTypes: true }).flatMap((entry) => {
    const rel = path.join(base, entry.name);
    return entry.isDirectory() ? walk(dir, rel) : [rel];
  });
}

// release packs the example as plugin id at version. crash swaps in a run
// file that exits with status 3, change edits the manifest.
function release(example, id, version, { crash = false, change } = {}) {
  const files = walk(example).map((rel) => {
    let body = fs.readFileSync(path.join(example, rel));
    if (rel === "plugin.json") {
      const manifest = JSON.parse(body.toString("utf8"));
      Object.assign(manifest, { id, version }, crash ? { run: "crash.sh" } : {});
      if (change) change(manifest);
      body = Buffer.from(JSON.stringify(manifest));
    }
    if (rel === "page.html" || rel === "slot.html") body = Buffer.from(body.toString("utf8").replace(/(<\/?)hello-/g, `$1${id}-`));
    return { name: `${id}-${version}/${rel}`, body, mode: rel === "hello.py" ? 0o755 : 0o644 };
  });
  if (crash) files.push({ name: `${id}-${version}/crash.sh`, body: Buffer.from("#!/bin/sh\necho this release breaks >&2\nexit 3\n"), mode: 0o755 });
  return tarball(files);
}

// Feed is the fake forge. tamper changes the files after their checksums.
class Feed {
  constructor() {
    this.reset();
    this.server = http.createServer((req, res) => this.answer(req, res));
  }

  reset() {
    this.repos = new Map();
    this.files = new Map();
    this.asked = [];
  }

  listen() {
    return new Promise((resolve, reject) => {
      this.server.once("error", reject);
      this.server.listen(FEED_PORT, "0.0.0.0", resolve);
    });
  }

  close() {
    this.server.close();
  }

  publish(repo, version, archives, { tamper, prerelease = false } = {}) {
    const files = { ...archives };
    const sums = Object.entries(files).map(([name, body]) => `${crypto.createHash("sha256").update(body).digest("hex")}  ${name}`);
    files[`plugin_${version}_checksums.txt`] = Buffer.from(`${sums.join("\n")}\n`);
    if (tamper) tamper(files);
    const assets = Object.entries(files).map(([name, body]) => {
      const at = `/files/${repo.replace(/[/:]/g, "-")}/${version}/${name}`;
      this.files.set(at, body);
      return { name, url: `http://${FEED_ADDR}:${FEED_PORT}${at}` };
    });
    const list = this.repos.get(repo) || [];
    list.unshift({ tag: `v${version}`, notes: `What ${version} brings: **faster notes**.`, prerelease, assets });
    this.repos.set(repo, list);
  }

  answer(req, res) {
    const url = req.url.split("?")[0];
    const host = req.headers.host || "";
    this.asked.push(host + url);
    const github = host === "api.github.com" ? url.match(/^\/repos\/(.+)\/releases$/) : url.match(/^\/api\/v3\/repos\/(.+)\/releases$/);
    const gitlab = url.match(/^\/api\/v4\/projects\/(.+)\/releases$/);
    const forgeHost = host === "api.github.com" ? "github.com" : host;
    const repo = github ? `${forgeHost}/${github[1]}` : gitlab ? `${forgeHost}/${decodeURIComponent(gitlab[1])}` : null;
    if (repo !== null) {
      const list = this.repos.get(repo);
      if (!list) {
        res.writeHead(404, { "Content-Type": "application/json" });
        res.end('{"message":"Not Found"}');
        return;
      }
      const body = github
        ? list.map((r) => ({ tag_name: r.tag, name: r.tag, body: r.notes, published_at: "2026-11-16T10:00:00Z", draft: false, prerelease: r.prerelease, assets: r.assets.map((a) => ({ name: a.name, browser_download_url: a.url })) }))
        : list.map((r) => ({ tag_name: r.tag, name: r.tag, description: r.notes, released_at: "2026-11-16T10:00:00Z", upcoming_release: r.prerelease, assets: { links: r.assets.map((a) => ({ name: a.name, direct_asset_url: a.url })) } }));
      res.writeHead(200, { "Content-Type": "application/json" });
      res.end(JSON.stringify(body));
      return;
    }
    const file = this.files.get(url);
    if (!file) {
      res.writeHead(404);
      res.end();
      return;
    }
    res.writeHead(200, { "Content-Type": "application/octet-stream" });
    res.end(file);
  }
}

// releaseChecks runs the checks of installs from a repository for one engine.
async function releaseChecks(t) {
  const { L, engine, page, run, mobilePage, second, feed, EXAMPLE, EXAMPLE_HOST, PLUGINS_DIR, ROW, shown, waitShown, waitRow, plugins, removePlugin, call, csrf, scrollDown } = t;
  const { assert, sleep, BASE } = L;
  const any = (id, version, opts) => ({ [`${id}_${version}_any.tar.gz`]: release(EXAMPLE, id, version, opts) });
  const versionOf = (p, id) => p.evaluate((sel) => document.querySelector(`${sel} [data-plugin-version]`)?.textContent.trim() || "", ROW(id));
  const rowCount = (p) => p.evaluate(() => document.querySelectorAll("[data-plugin-row]").length);
  const versionDirs = (id) => (fs.existsSync(path.join(PLUGINS_DIR, ".versions", id)) ? fs.readdirSync(path.join(PLUGINS_DIR, ".versions", id)) : []);
  const fetchLeft = () => fs.readdirSync(PLUGINS_DIR).filter((name) => name.startsWith(".fetch-"));
  async function waitVersion(p, id, version, state = "running", timeout = 30000) {
    const deadline = Date.now() + timeout;
    let last = "";
    while (Date.now() < deadline) {
      last = `${await versionOf(p, id).catch(() => "")} ${await p.evaluate((sel) => document.querySelector(sel)?.dataset.pluginState || "", ROW(id)).catch(() => "")}`;
      if (last === `${version} ${state}`) return;
      await sleep(150);
    }
    throw new Error(`the row of ${id} reads ${last}, want ${version} ${state}`);
  }
  async function openDialog(p, { tap = false } = {}) {
    await L.waitUpgraded(p, ["dc-form-modal", "dc-plugin-list"]);
    const opener = p.locator("[data-plugin-repository-open]");
    await opener.scrollIntoViewIfNeeded();
    if (tap) await opener.tap(); else await opener.click();
    await waitShown(p, ".modal.show #plugin-url");
    await sleep(600);
  }
  async function lookUp(p, url, forge, { tap = false } = {}) {
    const pick = `label[for="plugin-forge-${forge}"]`;
    if (tap) await p.tap(pick); else await p.click(pick);
    assert(await p.isChecked(`#plugin-forge-${forge}`), `${forge} is not picked`);
    if (tap) {
      await p.tap("#plugin-url");
      await p.keyboard.type(url);
      await p.tap("[data-plugin-look]");
    } else {
      await p.fill("#plugin-url", url);
      await p.click("[data-plugin-look]");
    }
  }
  // tapLive taps again when a live update of the list replaced the target.
  async function tapLive(p, selector) {
    for (let i = 0; ; i += 1) {
      try {
        await p.locator(selector).scrollIntoViewIfNeeded({ timeout: 5000 });
        await p.locator(selector).tap({ timeout: 5000 });
        return;
      } catch (e) {
        if (i === 4 || !/not attached|detached|not stable/i.test(e.message)) throw e;
        await sleep(300);
      }
    }
  }
  const watch = async () => {
    const w = await second.newPage();
    L.wirePage(w, { consoleErrors: [], pageErrors: [], cdnNoise: [] });
    await w.goto(`${BASE}/settings/plugins`, { waitUntil: "domcontentloaded" });
    if (/\/login/.test(w.url())) await L.login(w);
    await plugins(w);
    await w.evaluate(() => { window.__stayed = true; });
    return w;
  };
  const stayed = async (w) => assert(await w.evaluate(() => window.__stayed === true), "the second page reloaded");
  const addNote = async (id, text) => {
    const token = await csrf(page);
    const res = await call(page, `/plugins/${id}/notes`, { method: "POST", headers: { "Content-Type": "application/json", "X-CSRF-Token": token }, body: JSON.stringify({ text }) });
    assert(res.status < 300, `the note answered ${res.status}`);
  };
  const hasNote = async (id, text) => JSON.parse((await call(page, `/plugins/${id}/notes`)).text).notes.some((n) => (n.text || n) === text);
  feed.reset();
  const note = `kept through every release ${engine}`;

  await run("a repository installs after one question, a second page follows live", async () => {
    feed.publish("github.com/acme/hello-rel", "1.0.0", any("hello-rel", "1.0.0"));
    const w = await watch();
    try {
      await plugins(page);
      await openDialog(page);
      await lookUp(page, "https://github.com/acme/hello-rel", "github");
      await waitShown(page, '.modal.show [data-plugin-offer="hello-rel"]');
      const offer = await page.locator(".modal.show [data-plugin-offer-details]").innerText();
      for (const want of [/Name\s+Hello/, /Version\s+1\.0\.0/, /Id\s+hello-rel/, /Description\s+\S/, /Source\s+github\.com\/acme\/hello-rel/, /Archive\s+hello-rel_1\.0\.0_any\.tar\.gz/]) assert(want.test(offer), `the offer misses ${want}: ${offer}`);
      assert(/Install Hello\?/.test(await page.locator(".modal.show [data-form-modal-title]").innerText()), "the dialog does not ask");
      assert(await page.isChecked("[data-plugin-offer-auto]"), "keeping it updated is not offered on");
      assert(await rowCount(w) === await rowCount(page), "the second page shows another list");
      assert(!(await w.locator(ROW("hello-rel")).count()), "the plugin was installed before the question was answered");
      await page.click("[data-plugin-offer-auto]");
      await page.click("[data-plugin-offer-install]");
      await waitRow(page, "hello-rel", "running");
      await waitShown(page, `${ROW("hello-rel")} [data-plugin-repository]`);
      assert(/From github\.com\/acme\/hello-rel/.test(await page.locator(`${ROW("hello-rel")} [data-plugin-source]`).innerText()), "the row does not name its repository");
      assert(await page.getAttribute(`${ROW("hello-rel")} [data-plugin-action="auto"]`, "data-plugin-auto") === "off", "the automatic updates did not stay off");
      await waitShown(page, ".modal.show", false);
      await waitRow(w, "hello-rel", "running");
      await waitVersion(w, "hello-rel", "1.0.0");
      await stayed(w);
      assert(versionDirs("hello-rel").length === 1, `the versions hold ${versionDirs("hello-rel")}`);
      assert(fs.lstatSync(path.join(PLUGINS_DIR, "hello-rel")).isSymbolicLink(), "the plugin does not stand as a link into its versions");
      await addNote("hello-rel", note);
    } finally {
      await w.close();
    }
  });

  await run("a release without a fitting archive, a tampered one and a broken one are refused, nothing changes", async () => {
    feed.publish("gitlab.com/acme/no-archive", "1.0.0", { "x_1.0.0_plan9_mips.tar.gz": Buffer.from("x") });
    feed.publish("github.com/acme/tampered", "1.0.0", any("tampered", "1.0.0"), { tamper: (files) => { files["tampered_1.0.0_any.tar.gz"] = release(EXAMPLE, "tampered", "1.0.0", { crash: true }); } });
    feed.publish("github.com/acme/broken-rel", "1.0.0", any("broken-rel", "1.0.0", { change: (m) => { m.run = "../escape.sh"; } }));
    const wrongVersion = release(EXAMPLE, "wrong-version", "0.9.0");
    feed.publish("github.com/acme/wrong-version", "1.0.0", { "wrong-version_1.0.0_any.tar.gz": wrongVersion });
    await plugins(page);
    const rows = await rowCount(page);
    const staged = fetchLeft();
    await openDialog(page);
    for (const [repo, forge, want] of [
      ["https://github.com/acme/missing", "github", /No public repository at github\.com\/acme\/missing\./],
      ["http://github.com/acme/hello-rel", "github", /The address must use https\./],
      ["https://169.254.169.254/acme/hello-rel", "gitlab", /Link local addresses are refused\./],
      ["https://gitlab.com/acme/no-archive", "gitlab", /Release 1\.0\.0 has no archive for \S+ or any, or no checksums file\./],
      ["https://github.com/acme/tampered", "github", /Release 1\.0\.0 refused: tampered_1\.0\.0_any\.tar\.gz does not match its checksum\./],
      ["https://github.com/acme/wrong-version", "github", /its plugin\.json says version 0\.9\.0\./],
      ["https://github.com/acme/broken-rel", "github", /Release 1\.0\.0 refused\. The run path must be a relative path inside the plugin directory\./],
    ]) {
      await lookUp(page, repo, forge);
      await page.waitForFunction((w) => new RegExp(w).test(document.querySelector(".modal.show [data-form-modal-error]")?.textContent || ""), want.source, { timeout: 15000 });
      await waitShown(page, ".modal.show [data-form-modal-error]");
      assert(await page.inputValue("#plugin-url") === repo, `the refused address ${repo} was not kept`);
    }
    await page.click(".modal.show [data-bs-dismiss=modal]");
    await waitShown(page, ".modal.show", false);
    await plugins(page);
    assert(await rowCount(page) === rows, "a refused release added a row");
    const left = fetchLeft().filter((name) => !staged.includes(name));
    assert(left.length === 0, `a refused release left ${left}`);
    for (const id of ["tampered", "broken-rel", "wrong-version"]) assert(!fs.existsSync(path.join(PLUGINS_DIR, id)) && !versionDirs(id).length, `${id} left something in the folder`);
  });

  await run("the row shows the step with its notes, one tap updates, the version before stays", async () => {
    const w = await watch();
    try {
      feed.publish("github.com/acme/hello-rel", "1.1.0", any("hello-rel", "1.1.0"));
      feed.publish("github.com/acme/hello-rel", "9.0.0", any("hello-rel", "9.0.0"), { prerelease: true });
      await plugins(page);
      await Promise.all([page.waitForFunction(() => /Updates checked\./.test(document.querySelector(".dc-notice-text")?.textContent || ""), null, { timeout: 15000 }), page.click("[data-plugin-check]")]);
      await waitShown(page, `${ROW("hello-rel")} [data-plugin-step="1.1.0"]`);
      assert(/1\.0\.0 → 1\.1\.0/.test(await page.locator(`${ROW("hello-rel")} [data-plugin-step-label]`).innerText()), "the step is not named");
      await waitShown(w, `${ROW("hello-rel")} [data-plugin-step="1.1.0"]`);
      await page.click(`${ROW("hello-rel")} [data-plugin-notes-open]`);
      await page.waitForSelector(".swal2-popup", { state: "visible" });
      assert(/What 1\.1\.0 brings: faster notes\./.test(await page.locator(".swal2-popup").innerText()), "the notes are not shown");
      assert(!/9\.0\.0/.test(await page.locator(".swal2-popup").innerText()), "a prerelease is offered");
      assert(await page.locator(".swal2-popup strong", { hasText: "faster notes" }).count() === 1, "the notes' markdown was not rendered");
      await Promise.all([page.waitForFunction(() => /Plugin updated\./.test(document.querySelector(".dc-notice-text")?.textContent || ""), null, { timeout: 30000 }), page.click(".swal2-confirm")]);
      await waitVersion(page, "hello-rel", "1.1.0");
      await waitVersion(w, "hello-rel", "1.1.0");
      await waitShown(w, `${ROW("hello-rel")} [data-plugin-step]`, false);
      assert(versionDirs("hello-rel").length === 2, `the version before did not stay: ${versionDirs("hello-rel")}`);
      assert(await hasNote("hello-rel", note), "the plugin's note did not survive the update");
      await stayed(w);
    } finally {
      await w.close();
    }
  });

  await run("a release that does not keep running is gone back from, with why", async () => {
    const w = await watch();
    try {
      feed.publish("github.com/acme/hello-rel", "1.2.0", any("hello-rel", "1.2.0", { crash: true }));
      await plugins(page);
      await page.click("[data-plugin-check]");
      await waitShown(page, `${ROW("hello-rel")} [data-plugin-step="1.2.0"]`);
      await Promise.all([
        page.waitForFunction(() => /Plugin updated\./.test(document.querySelector(".dc-notice-text")?.textContent || ""), null, { timeout: 30000 }),
        page.click(`${ROW("hello-rel")} [data-plugin-action="update"]`),
      ]);
      await waitShown(page, `${ROW("hello-rel")} [data-plugin-failed]`, true, 30000);
      await waitVersion(page, "hello-rel", "1.1.0");
      const failed = await page.locator(`${ROW("hello-rel")} [data-plugin-failed]`).innerText();
      assert(/The update to 1\.2\.0 failed, rolled back to 1\.1\.0\. It exited with status 3\./.test(failed), `the row says ${failed}`);
      assert(/this release breaks/.test(await page.locator(`${ROW("hello-rel")} [data-plugin-failed-tail]`).innerText()), "the failed release's last lines are not shown");
      await waitShown(w, `${ROW("hello-rel")} [data-plugin-failed]`);
      await waitVersion(w, "hello-rel", "1.1.0");
      assert(await hasNote("hello-rel", note), "the plugin's note did not survive the rollback");
      assert(versionDirs("hello-rel").length === 1, `the failed release stayed: ${versionDirs("hello-rel")}`);
      await stayed(w);
    } finally {
      await w.close();
    }
  });

  await run("automatic updates never retry the failed release and install a newer one on their own", async () => {
    const w = await watch();
    try {
      await plugins(page);
      await Promise.all([
        page.waitForFunction(() => /Automatic updates on\./.test(document.querySelector(".dc-notice-text")?.textContent || ""), null, { timeout: 15000 }),
        page.click(`${ROW("hello-rel")} [data-plugin-action="auto"]`),
      ]);
      await page.waitForFunction((sel) => document.querySelector(sel)?.dataset.pluginAuto === "on", `${ROW("hello-rel")} [data-plugin-action="auto"]`, { timeout: 10000 });
      await w.waitForFunction((sel) => document.querySelector(sel)?.dataset.pluginAuto === "on", `${ROW("hello-rel")} [data-plugin-action="auto"]`, { timeout: 10000 });
      await sleep(7000);
      assert(await versionOf(page, "hello-rel") === "1.1.0", "an automatic update tried the failed release again");
      feed.publish("github.com/acme/hello-rel", "1.3.0", any("hello-rel", "1.3.0"));
      await waitVersion(w, "hello-rel", "1.3.0", "running", 40000);
      await waitVersion(page, "hello-rel", "1.3.0");
      await waitShown(page, `${ROW("hello-rel")} [data-plugin-failed]`, false, 45000);
      assert(await hasNote("hello-rel", note), "the plugin's note did not survive the automatic update");
      await stayed(w);
    } finally {
      await w.close();
    }
  });

  await run("a release that is not newer is no update, an offer used up leaves no button to tap", async () => {
    const w = await watch();
    const nothing = (version, newest) => new RegExp(`Nothing to update: Hello ${version.replace(/\./g, "\\.")} is installed, the newest release of github\\.com/acme/hello-rel is ${newest.replace(/\./g, "\\.")}\\.`);
    try {
      await plugins(page);
      await Promise.all([
        page.waitForFunction(() => /Automatic updates off\./.test(document.querySelector(".dc-notice-text")?.textContent || ""), null, { timeout: 15000 }),
        page.click(`${ROW("hello-rel")} [data-plugin-action="auto"]`),
      ]);
      await page.waitForFunction((sel) => document.querySelector(sel)?.dataset.pluginAuto === "off", `${ROW("hello-rel")} [data-plugin-action="auto"]`, { timeout: 10000 });
      const staged = fetchLeft();
      await openDialog(page);
      await lookUp(page, "https://github.com/acme/hello-rel", "github");
      await page.waitForFunction((w) => new RegExp(w).test(document.querySelector(".modal.show [data-form-modal-error]")?.textContent || ""), nothing("1.3.0", "1.3.0").source, { timeout: 15000 });
      await waitShown(page, ".modal.show [data-form-modal-error]");
      assert(!(await shown(page, ".modal.show [data-plugin-offer]")) && !(await page.locator(".modal.show [data-plugin-offer-install]").count()), "the release installed is offered");
      assert(!/Update/.test(await page.locator(".modal.show [data-form-modal-title]").innerText()), "the dialog says Update");
      assert(await page.inputValue("#plugin-url") === "https://github.com/acme/hello-rel", "the address was not kept");
      assert(fetchLeft().filter((name) => !staged.includes(name)).length === 0, "the refused look left its download");

      feed.publish("github.com/acme/hello-rel", "1.4.0", any("hello-rel", "1.4.0"));
      await lookUp(page, "https://github.com/acme/hello-rel", "github");
      await waitShown(page, '.modal.show [data-plugin-offer="hello-rel"]');
      assert(/Update Hello\?/.test(await page.locator(".modal.show [data-form-modal-title]").innerText()), "a newer release is not offered as an update");
      assert(/This updates the installed Hello 1\.3\.0,/.test(await page.locator(".modal.show [data-plugin-offer-updates]").innerText()), "the update is not named");
      await plugins(w);
      await w.click("[data-plugin-check]");
      await waitShown(w, `${ROW("hello-rel")} [data-plugin-step="1.4.0"]`);
      await w.click(`${ROW("hello-rel")} [data-plugin-action="update"]`);
      await waitVersion(w, "hello-rel", "1.4.0");
      await page.click("[data-plugin-offer-install]");
      await page.waitForFunction((w) => new RegExp(w).test(document.querySelector(".modal.show [data-form-modal-error]")?.textContent || ""), nothing("1.4.0", "1.4.0").source, { timeout: 15000 });
      await waitShown(page, ".modal.show [data-form-modal-error]");
      await waitShown(page, ".modal.show [data-plugin-offer-install]", false);
      assert(/Install from a repository/.test(await page.locator(".modal.show [data-form-modal-title]").innerText()), "the used offer's dialog kept its title");
      assert(await page.inputValue("#plugin-url") === "https://github.com/acme/hello-rel", "the address was not kept after the used offer");
      await page.evaluate(() => { document.querySelector(".modal.show [data-form-modal-error]").dataset.answered = "1"; });
      await page.click("[data-plugin-look]");
      await page.waitForFunction((w) => { const box = document.querySelector(".modal.show [data-form-modal-error]"); return box && !box.dataset.answered && new RegExp(w).test(box.textContent); }, nothing("1.4.0", "1.4.0").source, { timeout: 15000 });
      const said = await page.evaluate(() => [...document.querySelectorAll(".modal.show [data-form-modal-error], .modal.show [data-plugin-url-error]")].map((el) => el.textContent.trim()));
      assert(said.length === 1 && !/download is gone/.test(said[0]), `the second tap says ${JSON.stringify(said)}`);
      await page.click(".modal.show [data-bs-dismiss=modal]");
      await waitShown(page, ".modal.show", false);
      await plugins(page);
      await waitVersion(page, "hello-rel", "1.4.0");
      assert(await hasNote("hello-rel", note), "the plugin's note did not survive");
    } finally {
      await w.close();
    }
  });

  await run("a plugin of another source asks before a repository replaces it", async () => {
    feed.publish("github.com/acme/hello", "2.0.0", any("hello", "2.0.0"));
    await plugins(page);
    assert(/Copied from/.test(await page.locator(`${ROW("hello")} [data-plugin-source]`).innerText()), "the example does not say it was copied");
    await openDialog(page);
    await lookUp(page, "github.com/acme/hello", "github");
    await waitShown(page, ".modal.show [data-plugin-offer-replaces]");
    const ask = await page.locator(".modal.show [data-plugin-offer-replaces]").innerText();
    assert(ask.includes(EXAMPLE_HOST) && /Installing replaces it/.test(ask), `the question reads ${ask}`);
    assert(/Replace/.test(await page.locator("[data-plugin-offer-install]").innerText()), "the button does not say Replace");
    await page.click(".modal.show [data-bs-dismiss=modal]");
    await waitShown(page, ".modal.show", false);
    await plugins(page);
    assert(await versionOf(page, "hello") === "1.0.0" && /Copied from/.test(await page.locator(`${ROW("hello")} [data-plugin-source]`).innerText()), "a cancelled replace changed the plugin");
  });

  await run("a GitLab on its own host with a subgroup path and a GitHub Enterprise host install and update", async () => {
    const w = await watch();
    try {
      for (const [id, repo, url, forge, feedPath] of [
        ["hello-lab", "git.example.test/group/sub/hello-lab", "https://git.example.test/group/sub/hello-lab/-/releases", "gitlab", "git.example.test/api/v4/projects/group%2Fsub%2Fhello-lab/releases"],
        ["hello-ghe", "ghe.example.test/acme/hello-ghe", "ghe.example.test/acme/hello-ghe", "github", "ghe.example.test/api/v3/repos/acme/hello-ghe/releases"],
      ]) {
        const source = `${repo} on ${forge === "github" ? "GitHub" : "GitLab"}`;
        feed.publish(repo, "1.0.0", any(id, "1.0.0"));
        await plugins(page);
        await openDialog(page);
        await lookUp(page, url, forge);
        await waitShown(page, `.modal.show [data-plugin-offer="${id}"]`);
        assert((await page.locator(".modal.show [data-plugin-offer-source]").innerText()).trim() === source, `the offer of ${id} names another source`);
        await page.click("[data-plugin-offer-auto]");
        await page.click("[data-plugin-offer-install]");
        await waitRow(page, id, "running");
        assert((await page.locator(`${ROW(id)} [data-plugin-source]`).innerText()).trim() === `From ${source}`, `the row of ${id} names another source`);
        await waitRow(w, id, "running");
        feed.publish(repo, "1.1.0", any(id, "1.1.0"));
        await plugins(page);
        await page.click("[data-plugin-check]");
        await waitShown(page, `${ROW(id)} [data-plugin-step="1.1.0"]`);
        await page.click(`${ROW(id)} [data-plugin-action="update"]`);
        await waitVersion(page, id, "1.1.0");
        await waitVersion(w, id, "1.1.0");
        const asked = feed.asked.filter((at) => at === feedPath).length;
        assert(asked >= 3, `the feed ${feedPath} was asked ${asked} times: ${feed.asked.slice(-8)}`);
      }
      await stayed(w);
      for (const id of ["hello-lab", "hello-ghe"]) await removePlugin(page, id);
    } finally {
      await w.close();
    }
  });

  await run("phone: install from a repository and update it with taps, typing and a scroll", async () => {
    const m = await mobilePage();
    await removePlugin(m, "hello-ph");
    for (const id of ["broken-phone-c", "broken-phone-d"]) {
      fs.mkdirSync(path.join(PLUGINS_DIR, id), { recursive: true });
      fs.writeFileSync(path.join(PLUGINS_DIR, id, "plugin.json"), "{ not json");
    }
    const w = await watch();
    try {
      feed.publish("gitlab.com/acme/hello-ph", "1.0.0", any("hello-ph", "1.0.0"));
      await plugins(m);
      await openDialog(m, { tap: true });
      await lookUp(m, "gitlab.com/acme/hello-ph", "gitlab", { tap: true });
      await waitShown(m, '.modal.show [data-plugin-offer="hello-ph"]');
      assert(/gitlab\.com\/acme\/hello-ph/.test(await m.locator("[data-plugin-offer-source]").innerText()), "the phone's offer names no source");
      await m.tap("[data-plugin-offer-auto]");
      assert(!(await m.isChecked("[data-plugin-offer-auto]")), "the tap did not switch the automatic updates off");
      await m.tap("[data-plugin-offer-install]");
      await waitRow(m, "hello-ph", "running");
      assert(await m.getAttribute(`${ROW("hello-ph")} [data-plugin-action="auto"]`, "data-plugin-auto") === "off", "the phone's choice did not hold");
      await waitRow(w, "hello-ph", "running");
      await plugins(m);
      await m.evaluate(() => { document.querySelector(".dc-work-body").scrollTop = 0; });
      const moved = await scrollDown(m, engine, ".dc-work-body");
      assert(moved > 20, `the page scrolled ${moved}px`);
      feed.publish("gitlab.com/acme/hello-ph", "1.1.0", any("hello-ph", "1.1.0"));
      await plugins(m);
      await tapLive(m, "[data-plugin-check]");
      await waitShown(m, `${ROW("hello-ph")} [data-plugin-notes-open]`, true, 20000);
      await tapLive(m, `${ROW("hello-ph")} [data-plugin-notes-open]`);
      await m.waitForSelector(".swal2-popup", { state: "visible" });
      await waitShown(w, `${ROW("hello-ph")} [data-plugin-step="1.1.0"]`);
      await m.tap(".swal2-confirm");
      await waitVersion(m, "hello-ph", "1.1.0");
      await waitVersion(w, "hello-ph", "1.1.0");
      await stayed(w);
      await removePlugin(m, "hello-ph", { tap: true });
      await waitShown(w, ROW("hello-ph"), false);
      for (const id of ["broken-phone-c", "broken-phone-d"]) await removePlugin(m, id, { tap: true });
    } finally {
      await w.close();
    }
  });

  await run("removing a plugin of a repository takes its releases, its state stays", async () => {
    await removePlugin(page, "hello-ph");
    await removePlugin(page, "hello-rel");
    assert(!versionDirs("hello-rel").length && !fs.existsSync(path.join(PLUGINS_DIR, ".versions", "hello-rel")), "Remove left the releases");
    assert(!(await shown(page, ROW("hello-rel"))), "the row stayed");
  });
}

module.exports = { Feed, releaseChecks, FEED_PORT };
