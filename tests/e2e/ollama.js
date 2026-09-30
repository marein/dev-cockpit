const L = require("./lib");
const { assert, BASE, sleep } = L;
const fs = require("fs");
const http = require("http");
const path = require("path");

// Ollama: claude sessions and assistant turns run through Ollama's launcher
// when the pick is ollama/<name>. Routes: GET/POST /settings/coders/ollama
// (the host and the model list), POST .../models/add and
// .../models/delete (the names added there, a recommended one refused), GET
// /coders/new (the model select, the note under it and the warning line, on
// the page and in the dialog), POST /coders/new (a start on an ollama/ pick),
// POST /coders/:id/stop and /coders/:id/resume (the mark and the launch
// survive both), POST /assistants/new and /assistants/:id with form=model
// (the ring's mark), POST /assistants/jobs with form=steer (a check's note
// naming its model in the head on the desktop and inside the fold on the
// phone). Elements: dc-coder-select, dc-model-pick, dc-form-modal,
// terminal-tabs, dc-steered-mark, dc-assistant.
//
// The instance MUST run with tests/e2e/fakes ahead of the real CLIs on PATH
// and a scratch HOME, see the README: the fake ollama there appends every
// argv to the file FAKE_OLLAMA_LOG names and runs the claude fake for
// `launch claude`, and this runner reads that file through OLLAMA_LOG, so
// the instance's log has to be mounted into the container. The Ollama server
// is the runner's own, an http server in this process on OLLAMA_PORT (11439)
// that the instance reaches over 127.0.0.1 because the container shares the
// host's network: it answers /api/version, lists one remote and one local
// model on /api/tags, answers /api/show with a context window and serves
// Ollama's public catalog on /catalog, where the instance's
// DEV_COCKPIT_OLLAMA_CATALOG_URL points. The host setting is pointed at it
// through the settings page and put back at the end. The check's note is
// bought by ringing the steered coder the way wake.js does, a Stop hook file
// written into NOTIFY_DIR, so the instance's notification-inbox/claude has to
// be mounted into the container as well.
//
// Gotchas:
// - the model list and the server's reachability are fetched in the
//   background on the first read after the host changed and again after the
//   list's ten minute TTL, a failed probe after thirty seconds, so the checks
//   reload until the list or the warning stands, and the warning check saves
//   the host in another spelling to force that refresh: a stopped fake keeps
//   the listed names and moves only the warning,
// - a catalog name has no remove control, so its refusal is posted through a
//   form the check builds itself,
// - the host may run an ollama of its own on the default port, so nothing is
//   asserted about the server lines before the host is saved,
// - the claude fake sleeps forever in interactive mode, which is what a
//   started coder looks like, and the stop goes through the strip's close
//   control because the desktop head carries none.

const PORT = Number(process.env.OLLAMA_PORT || 11439);
const LOG = process.env.OLLAMA_LOG || "";
const NOTIFY_DIR = process.env.NOTIFY_DIR || "";
const HOST = `http://127.0.0.1:${PORT}`;
const REMOTE = "gpt-oss:120b-cloud";
const LOCAL = "llama3.2:latest";
const VERSION = "0.20.1";
const CATALOG = "nemotron-3-ultra";
const CATALOG_CLOUD = `${CATALOG}:cloud`;
const NO_ANSWER = "The Ollama server does not answer.";
const NOTE = "ollama/ names run through Ollama, cloud models only.";
const SETTINGS = `${BASE}/settings/coders/ollama`;

function fakeServer() {
  const state = { hits: [], server: null };
  const answer = (res, code, body) => {
    res.writeHead(code, { "Content-Type": "application/json" });
    res.end(JSON.stringify(body));
  };
  const handle = (req, res) => {
    state.hits.push(`${req.method} ${req.url}`);
    if (req.method === "GET" && req.url === "/api/version") return answer(res, 200, { version: VERSION });
    if (req.method === "GET" && req.url === "/api/tags") {
      return answer(res, 200, { models: [
        { name: REMOTE, model: REMOTE, remote_host: "https://ollama.com", size: 0 },
        { name: LOCAL, model: LOCAL, size: 2019393189 },
      ] });
    }
    if (req.method === "POST" && req.url === "/api/show") {
      let body = "";
      req.on("data", (chunk) => { body += chunk; });
      req.on("end", () => {
        let model = "";
        try { model = JSON.parse(body).model; } catch {}
        if (model === REMOTE) return answer(res, 200, { model_info: { ".context_length": 131072, "general.architecture": "cloud" } });
        answer(res, 404, { error: "not found" });
      });
      return;
    }
    if (req.method === "GET" && req.url === "/catalog") {
      return answer(res, 200, { models: [
        { name: CATALOG, model: CATALOG, size: 0, digest: "x", details: {} },
        { name: "gpt-oss:120b", model: "gpt-oss:120b", size: 0, digest: "y", details: {} },
      ] });
    }
    return answer(res, 404, { error: "not found" });
  };
  state.start = () => new Promise((resolve, reject) => {
    const server = http.createServer(handle);
    server.once("error", reject);
    server.listen(PORT, "127.0.0.1", () => { state.server = server; resolve(); });
  });
  state.stop = () => new Promise((resolve) => {
    const server = state.server;
    state.server = null;
    if (!server) return resolve();
    server.closeAllConnections();
    server.close(() => resolve());
  });
  return state;
}

function ring(sessionID) {
  assert(NOTIFY_DIR, "NOTIFY_DIR is not set, mount the instance's notification-inbox/claude into the container");
  const name = `${Date.now()}-${Math.floor(Math.random() * 1e6)}`;
  const payload = JSON.stringify({ session_id: sessionID, hook_event_name: "Stop" });
  fs.writeFileSync(path.join(NOTIFY_DIR, `${name}.tmp`), payload);
  fs.renameSync(path.join(NOTIFY_DIR, `${name}.tmp`), path.join(NOTIFY_DIR, `${name}.json`));
}

function launches(id) {
  assert(LOG, "OLLAMA_LOG is not set, mount the instance's FAKE_OLLAMA_LOG into the container");
  let text = "";
  try { text = fs.readFileSync(LOG, "utf8"); } catch { return []; }
  return text.split("\n").filter((line) => line.startsWith("launch ") && line.includes(id));
}

async function waitLaunches(id, count) {
  let lines = [];
  for (let i = 0; i < 40; i += 1) {
    lines = launches(id);
    if (lines.length >= count) return lines;
    await sleep(500);
  }
  throw new Error(`the fake was asked ${lines.length} times for ${id}, wanted ${count}: ${JSON.stringify(lines)}`);
}

async function shown(page, selector) {
  const el = page.locator(selector).first();
  if (!(await el.count())) return false;
  if (!(await el.isVisible())) return false;
  const box = await el.boundingBox();
  return Boolean(box && box.width > 0 && box.height > 0);
}

const noticeSays = (page, text, level = "success") => page.waitForFunction(([want, lvl]) =>
  [...document.querySelectorAll(`.dc-notice.alert-${lvl}`)].some((n) => n.textContent.includes(want)), [text, level], { timeout: 8000 });

L.runFeature("OLLAMA", async ({ page, run, mobilePage }) => {
  const tag = Date.now().toString(36);
  const project = `zzol-${tag}`;
  const addedName = `zztc-${tag}:cloud`;
  const fake = fakeServer();
  let projectDir = "";
  let hostBefore = null;
  let coderID = "";
  let plainID = "";
  let assistantID = "";

  const token = (p) => p.locator('meta[name="csrf-token"]').getAttribute("content");
  const openSettings = async () => {
    await page.goto(SETTINGS, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
  };
  const saveHost = async (value) => {
    await openSettings();
    await page.fill("#settings-ollama-address", value);
    await Promise.all([
      page.waitForResponse((r) => r.url().includes("/settings/coders/ollama") && r.request().method() === "POST", { timeout: 15000 }),
      page.click('#settings-ollama button[type="submit"]'),
    ]);
    await noticeSays(page, "Settings saved.");
  };
  const modelBlock = (scope) => `${scope} [data-coder-models="claude"]`;
  const openNewCoder = async () => {
    await page.goto(`${BASE}/coders/new?project=${encodeURIComponent(projectDir)}`, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    assert((await L.waitUpgraded(page, ["dc-coder-select", "dc-model-pick"])).length === 0, "the form's elements did not upgrade");
    await page.selectOption('select[name="coder"]', "claude");
    await page.waitForSelector(`${modelBlock("")}:not([hidden])`, { timeout: 8000 });
  };
  const readModels = (scope = "") => page.evaluate((block) => {
    const root = document.querySelector(block);
    return {
      options: [...root.querySelectorAll('select[name="model"] option')].map((o) => o.value),
      note: [...root.querySelectorAll(".form-hint:not([data-ollama-warning])")].map((n) => n.textContent.trim()).join(" "),
      warning: root.querySelector("[data-ollama-warning]")?.textContent.trim() ?? null,
    };
  }, modelBlock(scope));
  const post = async (path, form) => page.request.post(`${BASE}${path}`, { form: { csrf_token: await token(page), ...form }, headers: { Accept: "application/json" } });

  try {
    await fake.start();
    await L.createProject(page, project);
    projectDir = await L.projectPath(page, project);

    await run("settings nav: Ollama stands nested under Coder with its icon and opens its page", async () => {
      await page.goto(`${BASE}/settings/general`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      const row = '[data-settings-nav] a[href="/settings/coders/ollama"]';
      assert(await shown(page, row), "no Ollama row in the settings column");
      const placed = await page.evaluate((sel) => {
        const link = document.querySelector(sel);
        let head = link.previousElementSibling;
        while (head && !head.classList.contains("dc-section-head")) head = head.previousElementSibling;
        return { head: head ? head.textContent.trim() : "", nested: link.classList.contains("is-nested"), text: link.textContent.trim(), note: link.querySelector(".dc-row-note")?.textContent.trim() ?? "" };
      }, row);
      assert(placed.head === "Coder" && placed.nested, `the row is not nested under Coder: ${JSON.stringify(placed)}`);
      assert(/^Ollama\s*Launcher$/.test(placed.text) && placed.note === "Launcher", `the row reads ${JSON.stringify(placed)}`);
      assert(await shown(page, `${row} svg[data-launcher="ollama"]`), "the row carries no Ollama icon");
      await Promise.all([page.waitForURL(/\/settings\/coders\/ollama$/, { timeout: 10000 }), page.click(row)]);
      await page.waitForSelector("#settings-ollama", { timeout: 8000 });
      assert((await page.locator(`${row}.active`).count()) === 1, "the row is not marked active on its page");
      assert((await page.locator("h1.dc-work-title").textContent()).trim() === "Ollama", "the page title is not Ollama");
    });

    await run("settings nav on the phone: the cockpit sheet marks the Ollama row and scrolls it into view", async () => {
      const mp = await mobilePage();
      await mp.goto(SETTINGS, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(mp);
      await mp.tap('.dc-tabbar button[data-ctx-area="cockpit"]');
      const row = 'dc-ctx-sheet:not([hidden]) [data-settings-nav] a[href="/settings/coders/ollama"]';
      await mp.waitForSelector(row, { state: "visible", timeout: 8000 });
      await sleep(400);
      const placed = await mp.evaluate((sel) => {
        const link = document.querySelector(sel);
        const sheet = link.closest("dc-ctx-sheet");
        const r = link.getBoundingClientRect();
        const s = sheet.getBoundingClientRect();
        return { active: link.classList.contains("active"), inside: r.top >= s.top && r.bottom <= s.bottom && r.height > 0, row: [r.top, r.bottom], sheet: [s.top, s.bottom] };
      }, row);
      assert(placed.active, `the Ollama row is not active in the sheet: ${JSON.stringify(placed)}`);
      assert(placed.inside, `the Ollama row is not inside the sheet's viewport: ${JSON.stringify(placed)}`);
      await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    });

    await run("settings page: the intro is two sentences, the host field and the model list stand, no status line", async () => {
      await openSettings();
      assert(await shown(page, "[data-ollama-intro]"), "no intro");
      const intro = (await page.locator("[data-ollama-intro]").textContent()).trim();
      const sentences = (intro.match(/[.!?](\s|$)/g) || []).length;
      assert(sentences === 2, `the intro has ${sentences} sentences: ${intro}`);
      assert(await shown(page, "#settings-ollama-address"), "no host field");
      hostBefore = await page.inputValue("#settings-ollama-address");
      assert(await shown(page, "h1.dc-work-title"), "the head carries no title");
      assert(await shown(page, '[data-coder-head] svg[data-launcher="ollama"]'), "the head carries no Ollama icon on the right");
      assert((await page.locator(".dc-work-tabs").count()) === 0, "a section tab strip stands in the head");
      assert(await shown(page, "[data-ollama-models]"), "the model list is not visible");
      assert((await page.locator("[data-ollama-status]").count()) === 0, "a status block stands on the page");
      const text = await page.textContent("#settings-ollama");
      assert(!/found on PATH|The server answers|igned in|version/.test(text), `a status line stands on the page: ${text}`);
      return intro;
    });

    await run("host: the fake's address saves with a notice and survives a reload", async () => {
      await saveHost(HOST);
      assert(await shown(page, ".dc-notice.alert-success"), "the saved notice is not visible");
      await page.reload({ waitUntil: "domcontentloaded" });
      assert((await page.inputValue("#settings-ollama-address")) === HOST, "the host did not survive the reload");
    });

    await run("models: the server's remote entry is listed as server, the local one is not, the catalog stands beside it spelled for the cloud", async () => {
      let listed = false;
      for (let i = 0; i < 20 && !listed; i += 1) {
        await openSettings();
        listed = (await page.locator(`[data-ollama-model="${REMOTE}"][data-ollama-source="server"]`).count()) === 1;
        if (!listed) await sleep(500);
      }
      assert(listed, "the remote entry never showed up");
      const rows = await page.$$eval("[data-ollama-model]", (els) => els.map((el) => ({
        name: el.dataset.ollamaModel,
        source: el.dataset.ollamaSource,
        badge: el.querySelector(".badge")?.textContent.trim(),
        remove: Boolean(el.querySelector('form[action$="/models/delete"]')),
      })));
      const catalog = rows.filter((r) => r.source === "catalog");
      assert(catalog.length === 1 && catalog[0].name === CATALOG_CLOUD && catalog[0].badge === "catalog" && !catalog[0].remove, `catalog rows: ${JSON.stringify(catalog)}`);
      assert(rows.filter((r) => r.name === REMOTE).length === 1, `the server's name stands once: ${JSON.stringify(rows)}`);
      const server = rows.find((r) => r.name === REMOTE);
      assert(server.badge === "server" && !server.remove, `the server row: ${JSON.stringify(server)}`);
      assert(fake.hits.includes("GET /catalog") && fake.hits.includes("POST /api/show"), `the fake was not asked for the catalog and the window: ${fake.hits}`);
      assert(await shown(page, `[data-ollama-model="${REMOTE}"]`), "the remote row is not visible");
      assert((await page.locator(`[data-ollama-model="${LOCAL}"]`).count()) === 0, "the local entry is listed");
      assert(fake.hits.includes("GET /api/tags"), "the fake's tags were never asked");
      assert((await page.locator("[data-ollama-models-empty]").count()) === 0, "the empty line stands beside a list");
      return `${rows.length} rows, catalog ${CATALOG_CLOUD}`;
    });

    await run("models: a name added here is listed as added with a remove control, and goes with it", async () => {
      await openSettings();
      const add = '[data-ollama-models] form[action$="/models/add"]';
      await page.fill(`${add} input[name="name"]`, addedName);
      await Promise.all([
        page.waitForResponse((r) => r.url().includes("/models/add") && r.request().method() === "POST", { timeout: 15000 }),
        page.click(`${add} button[type="submit"]`),
      ]);
      await noticeSays(page, `${addedName} added.`);
      const row = `[data-ollama-model="${addedName}"]`;
      assert(await shown(page, row), "the added row is not shown");
      assert((await page.locator(`${row}[data-ollama-source="added"] .badge`).textContent()).trim() === "added", "the row is not marked added");
      const remove = `${row} form[action$="/models/delete"] button[type="submit"]`;
      assert(await shown(page, remove), "no remove control on the added row");
      await Promise.all([
        page.waitForResponse((r) => r.url().includes("/models/delete") && r.request().method() === "POST", { timeout: 15000 }),
        page.click(remove),
      ]);
      await noticeSays(page, `${addedName} removed.`);
      assert((await page.locator(row).count()) === 0, "the removed row still stands");
    });

    await run("models: a catalog name carries no remove control and its removal is refused with its sentence", async () => {
      await openSettings();
      const row = `[data-ollama-model="${CATALOG_CLOUD}"][data-ollama-source="catalog"]`;
      assert(await shown(page, row), "the catalog row is not shown");
      assert((await page.locator(`${row} form`).count()) === 0, "a catalog row carries a form");
      await Promise.all([
        page.waitForEvent("load", { timeout: 10000 }),
        page.evaluate((name) => {
          const form = document.createElement("form");
          form.method = "post";
          form.action = "/settings/coders/ollama/models/delete";
          for (const [key, value] of [["csrf_token", document.querySelector('meta[name="csrf-token"]').content], ["name", name]]) {
            const input = document.createElement("input");
            input.type = "hidden";
            input.name = key;
            input.value = value;
            form.appendChild(input);
          }
          document.body.appendChild(form);
          form.submit();
        }, CATALOG_CLOUD).catch(() => {}),
      ]);
      await noticeSays(page, `${CATALOG_CLOUD} comes from Ollama and cannot be removed.`, "danger");
      assert(await shown(page, ".dc-notice.alert-danger"), "the refusal is not visible");
      assert(await shown(page, row), "the catalog row went");
    });

    await run("new coder: the model select lists the ollama/ names, the remote entry and not the local one, the note under it and no warning", async () => {
      await openNewCoder();
      const seen = await readModels();
      assert(seen.options.includes(`ollama/${REMOTE}`), `no remote entry: ${seen.options}`);
      assert(seen.options.includes(`ollama/${CATALOG_CLOUD}`), `no catalog entry: ${seen.options}`);
      assert(!seen.options.includes(`ollama/${LOCAL}`), `the local entry is offered: ${seen.options}`);
      assert(seen.note.includes(NOTE), `note: ${seen.note}`);
      assert(await shown(page, `${modelBlock("")} .form-hint`), "the note is not visible");
      assert(seen.warning === null, `a warning stands while everything is up: ${seen.warning}`);
    });

    await run("new coder: the dialog from the projects page carries the same list, the note and no warning", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      assert((await L.waitUpgraded(page, ["dc-form-modal"], 8000)).length === 0, "dc-form-modal not upgraded");
      await page.click(`#project-${project} a[href^="/coders/new"]`);
      await page.waitForSelector("[data-form-modal].show form", { timeout: 8000 });
      await sleep(600);
      await page.selectOption('[data-form-modal] select[name="coder"]', "claude");
      await page.waitForSelector(`${modelBlock("[data-form-modal]")}:not([hidden])`, { timeout: 8000 });
      const seen = await readModels("[data-form-modal]");
      assert(seen.options.includes(`ollama/${REMOTE}`) && !seen.options.includes(`ollama/${LOCAL}`), `dialog options: ${seen.options}`);
      assert(seen.note.includes(NOTE), `dialog note: ${seen.note}`);
      assert(seen.warning === null, `dialog warning: ${seen.warning}`);
      assert(await shown(page, `${modelBlock("[data-form-modal]")} select`), "the dialog's select is not visible");
      await page.click('[data-form-modal] [data-bs-dismiss="modal"]');
      await page.waitForFunction(() => !document.querySelector(".modal-backdrop") && !document.querySelector("[data-form-modal].show"), null, { timeout: 8000 });
    });

    await run("new coder: the warning says the server does not answer while the fake is stopped, and goes once it is back, each after the refresh a saved host forces", async () => {
      const warningBecomes = async (want) => {
        let seen = null;
        for (let i = 0; i < 60; i += 1) {
          await openNewCoder();
          seen = await readModels();
          if (want(seen.warning)) return seen;
          await sleep(500);
        }
        throw new Error(`the warning never moved, last ${JSON.stringify(seen.warning)}`);
      };
      await fake.stop();
      try {
        await saveHost(`${HOST}/`);
        const seen = await warningBecomes((warning) => warning === NO_ANSWER);
        assert(await shown(page, `${modelBlock("")} [data-ollama-warning]`), "the warning is not visible");
        assert(seen.options.includes(`ollama/${REMOTE}`) && seen.options.includes(`ollama/${CATALOG_CLOUD}`), "the listed names went with the server");
      } finally {
        await fake.start();
      }
      await warningBecomes((warning) => warning === null);
      assert((await page.locator(`${modelBlock("")} [data-ollama-warning]`).count()) === 0, "the warning element stays");
    });

    await run("coder: a start on an ollama/ pick wears the mark in the strip and on the projects row, and the fake launched claude on that model", async () => {
      await openNewCoder();
      const form = page.locator('form:has(select[name="agent"])').first();
      await form.locator('input[name="name"]').fill(`ol-${tag.slice(-4)}`);
      await page.selectOption(`${modelBlock("")} select[name="model"]`, `ollama/${REMOTE}`);
      const before = page.url();
      await Promise.all([
        page.waitForURL((u) => /\/coders\/(?!new)[^/]+$/.test(u.pathname) && u.href !== before, { timeout: 20000 }),
        form.locator('button[type="submit"]').first().click(),
      ]);
      coderID = new URL(page.url()).pathname.split("/").pop();
      const icon = `terminal-tabs .terminal-tab[data-tab-id="${coderID}"] .terminal-tab-icon[data-launcher="ollama"]`;
      await page.waitForSelector(icon, { timeout: 15000 });
      assert(await shown(page, icon), "the strip icon is not visible");
      assert(await shown(page, `${icon} svg[data-launcher="ollama"]`), "the strip icon holds no Ollama svg");
      assert((await page.locator(`terminal-tabs .terminal-tab[data-tab-id="${coderID}"] .terminal-tab-icon svg.coder-icon`).count()) === 1, "the strip icon is not one svg");
      const badged = `${icon} .dc-icon-badged`;
      assert(await shown(page, `${badged} > svg.coder-icon`) && await shown(page, `${badged} > svg.dc-icon-badge[data-launcher="ollama"]`), "the coder icon is not badged with the llama");
      assert((await page.getAttribute(badged, "title")) === "Claude via Ollama", `the badge title reads ${await page.getAttribute(badged, "title")}`);
      const lines = await waitLaunches(coderID, 1);
      assert(lines[0].startsWith(`launch claude --model ${REMOTE} --yes -- `), `first launch: ${lines[0]}`);
      assert(lines[0].includes(`--session-id ${coderID}`), `the launch does not carry the session: ${lines[0]}`);
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      const chip = `#project-${project} [data-chip-id="${coderID}"] .dc-term-icon[data-launcher="ollama"]`;
      await page.waitForSelector(chip, { timeout: 8000 });
      assert(await shown(page, `${chip} svg[data-launcher="ollama"]`), "the projects row chip holds no Ollama svg");
      return lines[0].slice(0, 60);
    });

    await run("coder: a plain claude start beside it wears no mark and asks the fake nothing", async () => {
      const url = await L.createSession(page, projectDir, `pl-${tag.slice(-4)}`, "claude");
      plainID = new URL(url).pathname.split("/").pop();
      const tab = `terminal-tabs .terminal-tab[data-tab-id="${plainID}"]`;
      await page.waitForSelector(`${tab} .terminal-tab-icon svg.coder-icon`, { timeout: 15000 });
      assert((await page.locator(`${tab} [data-launcher]`).count()) === 0, "a plain start carries the launcher mark");
      assert((await page.locator(`terminal-tabs .terminal-tab[data-tab-id="${coderID}"] [data-launcher="ollama"]`).count()) >= 1, "the ollama tab lost its mark beside a plain one");
      assert(launches(plainID).length === 0, `the fake was asked for a plain start: ${launches(plainID)}`);
    });

    await run("coder: the attach head on a phone wears the mark", async () => {
      const mp = await mobilePage();
      await mp.goto(`${BASE}/coders/${coderID}`, { waitUntil: "domcontentloaded" });
      const mark = 'dc-steered-mark .dc-term-icon[data-launcher="ollama"] svg[data-launcher="ollama"]';
      await mp.waitForSelector(mark, { timeout: 10000 });
      assert(await shown(mp, mark), "the head mark is not visible on the phone");
      await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    });

    await run("coder: a stop keeps the mark on the resumable row and a resume launches through ollama again on the same model", async () => {
      await L.stopSession(page, `${BASE}/coders/${coderID}`);
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      const resume = `#project-${project} form[action="/coders/${coderID}/resume"]`;
      await page.waitForSelector(resume, { timeout: 10000 });
      assert((await page.locator(`#project-${project} [data-chip-id="${coderID}"]`).count()) === 0, "the stopped coder still stands as running");
      assert(await shown(page, `${resume} .dc-term-icon[data-launcher="ollama"] svg[data-launcher="ollama"]`), "the resumable row lost the mark");
      await Promise.all([
        page.waitForURL(/\/coders\/(?!new)[^/]+$/, { timeout: 20000 }),
        page.locator(`${resume} button[type="submit"]`).first().click(),
      ]);
      const resumedID = new URL(page.url()).pathname.split("/").pop();
      const icon = `terminal-tabs .terminal-tab[data-tab-id="${resumedID}"] .terminal-tab-icon[data-launcher="ollama"] svg[data-launcher="ollama"]`;
      await page.waitForSelector(icon, { timeout: 15000 });
      assert(await shown(page, icon), "the resumed tab lost the mark");
      const lines = await waitLaunches(coderID, 2);
      assert(lines[1].startsWith(`launch claude --model ${REMOTE} --yes -- `), `second launch: ${lines[1]}`);
      assert(lines[1].includes(`--resume ${coderID}`), `the second launch is not a resume: ${lines[1]}`);
      coderID = resumedID;
      return lines[1].slice(0, 60);
    });

    await run("assistant: a chat pick on an ollama/ model badges the ring's coder icon with the Ollama mark, live and after a reload", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      const made = await post("/assistants/new", { form: "new", coder: "claude" });
      assistantID = (await made.json().catch(() => ({}))).id || "";
      assert(assistantID, `no assistant: ${made.status()}`);
      await page.goto(`${BASE}/assistants/${assistantID}`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
      const ring = "dc-assistant [data-assistant-new-label]";
      const launcher = `${ring} [data-assistant-ring-launcher]`;
      const icon = `${ring} [data-assistant-ring-icon]`;
      assert(await shown(page, icon), "a fresh assistant shows no coder icon on the ring");
      assert(!(await shown(page, launcher)), "a fresh assistant wears the launcher already");
      await page.evaluate(() => { document.querySelector(".dc-app").dataset.runnerLive = "1"; });
      const res = await post(`/assistants/${assistantID}`, { form: "model", model: `ollama/${REMOTE}` });
      assert(res.status() === 200, `the pick was refused: ${res.status()} ${await res.text()}`);
      await page.waitForFunction((sel) => { const el = document.querySelector(sel); return el && !el.hidden; }, launcher, { timeout: 10000 });
      assert(await shown(page, launcher), "the launcher mark is not visible");
      assert(!(await shown(page, icon)), "the bare coder icon still shows beside the badged one");
      assert(await shown(page, `${launcher} .dc-icon-badged > svg.coder-icon`) && await shown(page, `${launcher} .dc-icon-badged > svg.dc-icon-badge[data-launcher="ollama"]`), "the ring does not show the coder icon badged with the llama");
      assert(await page.evaluate(() => document.querySelector(".dc-app").dataset.runnerLive === "1"), "the page reloaded to show the mark");
      await page.reload({ waitUntil: "domcontentloaded" });
      await page.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
      assert(await shown(page, launcher), "the mark is not rendered on a load");
      assert(!(await shown(page, icon)), "the bare coder icon is rendered beside the badged one on a load");
      assert(await shown(page, `${launcher} .dc-icon-badged > svg.dc-icon-badge[data-launcher="ollama"]`), "the badge is not rendered on a load");
      assert(await page.locator("dc-assistant [data-assistant-models] [data-ollama-warning]").evaluate((el) => el.hidden), "the ring's warning stands while the server is up");
    });

    await run("assistant: a check's note keeps the model out of its head on a phone and names it first inside the fold, the desktop head names it before the speaker", async () => {
      const steered = await post("/assistants/jobs", { form: "steer", assistant: assistantID, terminal: coderID, task: "Write the file", done_when: "WAKE_LONG: the file is there" });
      assert(steered.status() === 200, `steer answered ${steered.status()}: ${await steered.text()}`);
      ring(coderID);
      const mp = await mobilePage();
      await mp.goto(`${BASE}/assistants/${assistantID}`, { waitUntil: "domcontentloaded" });
      await mp.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
      const note = 'dc-assistant [data-assistant-message][data-role="cockpit"]';
      const head = `${note} [data-assistant-note="check"]`;
      const headModel = `${head} [data-assistant-model]`;
      const foldModel = `${note} [data-assistant-note-rest] [data-assistant-model]`;
      await mp.waitForSelector(head, { timeout: 90000 });
      assert((await mp.locator(headModel).count()) === 1 && !(await shown(mp, headModel)), "the phone's note head shows the model");
      const headText = await mp.locator(head).innerText();
      assert(!headText.includes(REMOTE), `the phone's note head reads the model: ${headText}`);
      assert((await mp.locator(foldModel).count()) === 1 && !(await shown(mp, foldModel)), "the fold's model line shows before the fold opens");
      await mp.click(`${note} [data-assistant-note-fold]`);
      await mp.waitForSelector(`${note} [data-assistant-note-rest].collapse.show`, { timeout: 8000 });
      assert(await shown(mp, foldModel), "the opened fold does not show the model line");
      const placed = await mp.evaluate((sel) => {
        const line = document.querySelector(sel);
        const text = line.parentElement.querySelector("[data-assistant-text]");
        return { text: line.textContent.trim(), title: line.title, lineBottom: line.getBoundingClientRect().bottom, textTop: text.getBoundingClientRect().top };
      }, foldModel);
      assert(placed.text === REMOTE && placed.title === REMOTE, `the fold's line reads ${JSON.stringify(placed)}`);
      assert(placed.lineBottom <= placed.textTop, `the model line does not stand above the text: ${JSON.stringify(placed)}`);
      await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await page.goto(`${BASE}/assistants/${assistantID}`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
      await page.waitForSelector(headModel, { state: "attached", timeout: 15000 });
      assert(await shown(page, headModel), "the desktop's note head does not show the model");
      assert(!(await shown(page, foldModel)), "the desktop shows the fold's model line");
      const desk = await page.evaluate((sel) => {
        const name = document.querySelector(sel);
        const box = name.getBoundingClientRect();
        const headBox = name.parentElement.getBoundingClientRect();
        const stamp = name.parentElement.querySelector("dc-time").getBoundingClientRect();
        const next = name.nextElementSibling;
        return { text: name.textContent.trim(), afterTime: box.left >= stamp.right, inside: box.right <= headBox.right + 1, next: next ? next.getAttribute("data-assistant-speak") !== null || next.tagName : "" };
      }, headModel);
      assert(desk.text === REMOTE && desk.afterTime && desk.inside, `the desktop head's model is off: ${JSON.stringify(desk)}`);
      return `${REMOTE} inside the fold on the phone, in the head on the desktop`;
    });
  } finally {
    try {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      if (assistantID) await post(`/assistants/${assistantID}`, { form: "delete" });
      for (const id of [coderID, plainID].filter(Boolean)) await post(`/coders/${id}/delete`, {});
      if (hostBefore !== null) await saveHost(hostBefore);
      await post("/settings/coders/ollama/models/delete", { name: addedName });
      await L.deleteProject(page, project);
    } catch (e) { console.log("cleanup note:", e.message); }
    await fake.stop();
  }
});
