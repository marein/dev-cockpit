const fs = require("fs");
const http = require("http");
const https = require("https");
const path = require("path");
const L = require("./lib");
const { assert, BASE, sleep, dismissUpdate } = L;
const { guestPage } = require("./project-share-guest");

// Assistants shared read only beside projects on one link (internal/share,
// /share/<token>/assistants/...), chromium. The owner picks a project and an
// assistant in the New link dialog of Settings › Sharing from one searchable
// dropdown (dc-picker) holding each kind under its head, the list shows both
// without a Comment mark, the link's Edit offers the other assistant in its
// Add. A guest that never logs in
// moves in one tab between the project's editor, the assistant's whole
// conversation (dc-share-chat over the conversation's own stream) and the
// running coders the assistant steers or once steered, whatever project they
// work in, live with the copy view and its conversation face, one it steers
// later joining the open strip without a reload. A stopped job coder, a coder
// another assistant steers, one it took over after the shared assistant let
// it go, and that assistant itself answer the plain 404. The guest's coders
// wear the owner's steered mark and a working ring that comes and goes live,
// in the list, the phone's sheet and the head, and the guest hears the
// working state of nothing else. Beside the chat stand the assistant's jobs
// and triggers, read only, one made later joining live, the other
// assistant's nowhere. The assistant at work wears the owner's ring for the
// guest, live: a turn on its row, in the switcher, on the phone's head and as
// the tool line in the answer, a check on its job's row and a reaction on its
// trigger's row. Share in the owner's row menu of an assistant opens
// Settings › Sharing with the New link dialog on it and the assistant picked,
// listed read only.
// The answer streams to the open guest while it is
// written, and the finished one links inside the share: the coders it names
// by their short id, one it steers and one of the project on the link, a
// file of that project; a file of a project off it, a stopped coder, another
// assistant's coder and every other path of the cockpit stay text, a link
// naming the cockpit's host stays a link. Of the workspace
// the guest reads the file a message links and nothing else. No write reaches
// the assistant from the guest, no request of a guest page leaves the link,
// and the local API socket can make and grow no link. Deleting the assistant
// ends a link of it alone, takes it off the link it shares with the project
// and ends the open guest's stream.
//
// Instance: like project-share.js's (tests/e2e/fakes first on PATH for the
// fake claude, a scratch HOME, --projects-dir and --state-dir of its own),
// its local API socket mounted (APISOCK, default /apisock/s) and the claude
// notify inbox, which starts and ends a coder's turn
// (-v <state-dir>/notification-inbox/claude:/inbox -e NOTIFY_DIR=/inbox).
// About a minute.

const APISOCK = process.env.APISOCK || "/apisock/s";
const NOTIFY_DIR = process.env.NOTIFY_DIR || "";
const TAG = Date.now().toString(36).slice(-5);
const PROJECT = `ashare${TAG}`;
const OFF = `ashareoff${TAG}`;
const ORIGIN = new URL(BASE).origin;
const short = (id) => id.slice(0, 8);

async function postAs(page, path, body, { json = false } = {}) {
  return page.evaluate(async ({ path, body, json }) => {
    const token = document.querySelector('meta[name="csrf-token"]')?.content || "";
    const res = await fetch(path, {
      method: "POST",
      headers: { "X-CSRF-Token": token, Accept: "application/json", ...(json ? { "Content-Type": "application/json" } : {}) },
      body: json ? JSON.stringify(body) : new URLSearchParams(body),
    });
    return { ok: res.ok, status: res.status, text: await res.text() };
  }, { path, body, json });
}

async function postJSON(page, path, body) {
  const res = await postAs(page, path, body);
  assert(res.ok, `POST ${path} answered ${res.status}: ${res.text.slice(0, 200)}`);
  return JSON.parse(res.text || "{}");
}

// fetchRaw asks the instance with nothing of a browser: no cookie, no
// redirect followed, so a refusal shows as what it is.
function fetchRaw(url, { method = "GET", body = "", headers = {} } = {}) {
  return new Promise((resolve, reject) => {
    const req = https.request(url, { method, rejectUnauthorized: false, headers: { ...headers, ...(body ? { "Content-Length": Buffer.byteLength(body) } : {}) } }, (res) => {
      let raw = "";
      res.on("data", (chunk) => { raw += chunk; });
      res.on("end", () => resolve({ status: res.statusCode, body: raw, headers: res.headers }));
    });
    req.on("error", reject);
    req.end(body);
  });
}

function overSocket(path, form) {
  return new Promise((resolve) => {
    const data = new URLSearchParams(form).toString();
    const req = http.request({
      socketPath: APISOCK, path, method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded", "Content-Length": Buffer.byteLength(data), Accept: "application/json" },
    }, (res) => {
      res.resume();
      res.on("end", () => resolve(res.statusCode));
    });
    req.on("error", () => resolve(0));
    req.end(data);
  });
}

// hook drops a coder's hook event into the notify inbox, the way claude's
// hooks report a turn.
function hook(session, name) {
  const file = path.join(NOTIFY_DIR, `${Date.now()}-${Math.floor(Math.random() * 1e6)}`);
  fs.writeFileSync(`${file}.tmp`, JSON.stringify({ session_id: session, hook_event_name: name }));
  fs.renameSync(`${file}.tmp`, `${file}.json`);
}

// iconOf reads a terminal icon that stands on the screen: its marks and what
// they paint.
const iconOf = (p, selector) => p.evaluate((s) => {
  const el = document.querySelector(s);
  if (!el || !el.getClientRects().length) return null;
  const style = getComputedStyle(el);
  return { steered: el.classList.contains("steered"), working: el.classList.contains("working"), color: style.color, ring: style.boxShadow };
}, selector);

// ringed waits until the icon wears the working ring, or until it lost it.
const ringed = (p, selector, on) => p.waitForFunction(([s, want]) => {
  const el = document.querySelector(s);
  if (!el || !el.getClientRects().length) return false;
  return el.classList.contains("working") === want && (getComputedStyle(el).boxShadow !== "none") === want;
}, [selector, on], { timeout: 15000 });

async function newAssistant(page, title) {
  const made = await postJSON(page, "/assistants/new", { form: "new", coder: "claude" });
  assert(made.id, `no assistant made: ${JSON.stringify(made)}`);
  await postJSON(page, `/assistants/${made.id}`, { form: "rename", title });
  return made.id;
}

async function newCoder(page, dir, name) {
  const made = await postJSON(page, "/coders/new", { name, project: dir, coder: "claude", automatic_approval: "on", prompt: `job-${name}` });
  assert(made.id, `no coder made: ${JSON.stringify(made)}`);
  return made.id;
}

const steer = (page, owner, terminal) => postJSON(page, "/assistants/jobs", { form: "steer", assistant: owner, terminal, task: "Share check", done_when: "" });

// thread reads the owner's transcript of an assistant as the CLI reads it.
async function thread(page, id) {
  return page.evaluate(async (p) => (await (await fetch(p, { headers: { Accept: "application/json" } })).json()).messages || [], `/assistants/instances/${id}?entries=200`);
}

// waitIdle waits until the owner's page of the assistant writes no answer.
async function waitIdle(page, id) {
  const until = Date.now() + 30000;
  while (Date.now() < until) {
    const html = await page.evaluate(async (p) => (await fetch(p)).text(), `/assistants/${id}`);
    if (!/<dc-assistant\b[^>]*\srunning[\s>]/.test(html)) return;
    await sleep(300);
  }
  throw new Error(`the assistant ${id} never finished its turn`);
}

// jobState reads the state of a job from the owner's list of an assistant's
// jobs.
async function jobState(page, owner, terminal) {
  const html = await page.evaluate(async (p) => (await fetch(p)).text(), `/assistants/jobs?assistant=${owner}`);
  return (html.match(new RegExp(`data-assistant-job="${terminal}"[\\s\\S]*?data-assistant-job-state="([a-z]+)"`)) || [])[1] || "";
}

// pick searches the picker of a form and picks the hit with Enter, and
// answers what it sends of a kind, what it says and the heads it showed.
async function pick(form, kind, query) {
  const picker = form.locator("dc-picker");
  await picker.locator("[data-picker-toggle]").click();
  await picker.locator("[data-picker-filter]").fill(query);
  const heads = await picker.locator(".editor-palette-group-head").allTextContents();
  await form.page().keyboard.press("Enter");
  await form.page().keyboard.press("Escape");
  return picker.evaluate((el, k) => ({
    sends: [...el.querySelectorAll(`[name="${k}"]:checked`)].map((box) => box.value),
    says: el.querySelector("[data-picker-summary]").textContent.trim(),
    offers: [...el.querySelectorAll("[data-picker-source] [data-picker-key]")].map((row) => row.dataset.pickerKey),
  }), kind).then((read) => ({ ...read, heads }));
}

async function linkOver(page, fields) {
  const form = [["amount", "1"], ["unit", "hours"], ["name", `api-${TAG}`], ...fields];
  const res = await postAs(page, "/settings/sharing/new", form);
  const link = (res.text.match(/value="([^"]*\/share\/[^"]+)"/) || [])[1];
  assert(res.ok && link, `making a link of ${JSON.stringify(fields)} answered ${res.status}`);
  return link;
}

const bubble = (gp, selector) => gp.evaluate((s) => {
  const nodes = [...document.querySelectorAll(`[data-share-log] ${s}`)];
  const el = nodes[nodes.length - 1];
  if (!el) return null;
  return {
    id: el.dataset.messageId,
    state: el.dataset.state,
    text: el.querySelector("[data-assistant-text]")?.textContent || "",
    links: [...el.querySelectorAll("a[href]")].map((a) => ({ href: a.getAttribute("href"), text: a.textContent.trim() })),
  };
}, selector);

L.runFeature("assistant-share", async ({ browser, page, run, mobilePage, bag }) => {
  assert(NOTIFY_DIR, "NOTIFY_DIR is not set: run with -v <state-dir>/notification-inbox/claude:/inbox -e NOTIFY_DIR=/inbox, "
    + "without the notify inbox no coder starts a turn and the working ring goes unchecked");
  const fx = { requests: [] };
  const missing = (await fetchRaw(`${BASE}/no/such/address`)).body;
  const notFound = async (url, what) => {
    const res = await fetchRaw(url);
    assert(res.status === 404 && res.body === missing, `${what} answered ${res.status}, want the plain 404`);
  };
  let guest = null;
  try {
    await run("the owner shares a project and an assistant from the one searchable dropdown", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await dismissUpdate(page);
      for (const name of [PROJECT, OFF]) {
        await L.createProject(page, name);
        await postJSON(page, `/projects/${name}/editor/create`, { path: "README.md" });
        fx[name] = await L.projectPath(page, name);
      }
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      fx.a = await newAssistant(page, `helper ${TAG}`);
      fx.b = await newAssistant(page, `other ${TAG}`);
      fx.steering = await newCoder(page, fx[OFF], `steering-${TAG}`);
      fx.once = await newCoder(page, fx[OFF], `once-${TAG}`);
      fx.stopped = await newCoder(page, fx[OFF], `stopped-${TAG}`);
      fx.foreign = await newCoder(page, fx[OFF], `foreign-${TAG}`);
      fx.mine = await newCoder(page, fx[PROJECT], `mine-${TAG}`);
      for (const id of [fx.steering, fx.once, fx.stopped]) await steer(page, fx.a, id);
      await steer(page, fx.b, fx.foreign);
      await postJSON(page, "/assistants/jobs", { form: "release", terminal: fx.once });
      assert(await jobState(page, fx.a, fx.once) === "stopped", "the released coder's job is still open");
      await postJSON(page, `/coders/${fx.stopped}/stop`, {});
      fx.trigA = (await postJSON(page, "/assistants/triggers", { form: "new", assistant: fx.a, event: "job-done", terminal: fx.steering, task: "EVENT_NOTHING share check", batch: "0" })).id;
      fx.trigB = (await postJSON(page, "/assistants/triggers", { form: "new", assistant: fx.b, event: "cron", spec: "0 3 * * *", task: "EVENT_NOTHING theirs", batch: "0" })).id;
      assert(fx.trigA && fx.trigB, "a trigger was not made");

      await page.goto(`${BASE}/settings/sharing`, { waitUntil: "domcontentloaded" });
      await dismissUpdate(page);
      await page.click("[data-share-new-link]");
      const form = page.locator("[data-form-modal].show [data-share-new]");
      await form.waitFor({ state: "visible", timeout: 10000 });
      await form.locator('[name="name"]').fill(`team-${TAG}`);
      const projects = await pick(form, "project", PROJECT);
      assert(projects.sends.join(",") === PROJECT && projects.says === PROJECT && projects.heads.join(",") === "Projects", `the picker reads ${JSON.stringify(projects)}`);
      const helpers = await pick(form, "assistant", `helper ${TAG}`);
      assert(helpers.sends.join(",") === fx.a && helpers.says === `${PROJECT}, helper ${TAG}` && helpers.heads.join(",") === "Assistants", `the picker reads ${JSON.stringify(helpers)}`);
      assert(helpers.offers.includes(`assistant:${fx.b}`) && helpers.offers.includes(`project:${OFF}`), "the picker does not offer the other assistant and project");
      await form.locator('button[type="submit"]').click();
      const field = page.locator("[data-form-modal].show [data-share-created] [data-share-url]");
      await field.waitFor({ state: "visible", timeout: 10000 });
      fx.link = await field.inputValue();
      fx.base = new URL(fx.link).pathname;
      const card = await page.textContent("[data-share-created]");
      assert(card.includes(PROJECT) && card.includes(`helper ${TAG}`), `the new link reads ${JSON.stringify(card.trim())}`);
      await page.click("[data-form-modal].show [data-share-created] .btn-primary");
      const row = page.locator("[data-share-row]", { has: page.locator(`[data-share-assistant="${fx.a}"]`) });
      await row.waitFor({ state: "visible", timeout: 10000 });
      fx.linkId = await row.getAttribute("data-share-row");
      for (const sel of [`[data-share-project="${PROJECT}"]`, `[data-share-assistant="${fx.a}"]`]) {
        assert(await row.locator(sel).count() === 1 && await row.locator(`${sel} [data-share-grant]`).count() === 0, `${sel} is not listed read only`);
      }
      await row.locator("[data-share-edit]").click();
      const edit = page.locator("[data-form-modal].show [data-share-edit-form]");
      await edit.waitFor({ state: "visible", timeout: 10000 });
      assert(await edit.locator(`[data-picker-value="assistant:${fx.b}"]`).count() === 1 && await edit.locator(`[data-picker-value="assistant:${fx.a}"]`).count() === 0, "the link's Add does not offer the other assistant alone");
      assert((await edit.locator(`[data-share-assistant="${fx.a}"]`).textContent()).includes("Assistant, read only"), "the Edit does not list the assistant read only");
      await page.click("[data-form-modal].show .btn-close");
    });

    await run("the local API socket can make and grow no link of an assistant", async () => {
      const made = await overSocket("/settings/sharing/new", { name: "sock", assistant: fx.b, amount: "1", unit: "hours" });
      const grown = await overSocket(`/settings/sharing/${fx.linkId}`, { form: "save", assistant: fx.b });
      assert(made === 403 && grown === 403, `the socket answered ${made} and ${grown}`);
      await notFound(`${fx.link}/assistants/${fx.b}`, "the assistant the socket tried to add");
    });

    await run("the assistant's row menu offers Share, which opens New link with that assistant picked", async () => {
      await page.goto(`${BASE}/assistants/${fx.b}`, { waitUntil: "domcontentloaded" });
      await dismissUpdate(page);
      await page.click(`[data-assistant-rows] [data-assistant-instance="${fx.b}"]`, { button: "right" });
      await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 6000 });
      const labels = await page.$$eval(".dc-context-menu .dropdown-item", (els) => els.map((el) => el.textContent.trim()));
      assert(labels.join(",") === "Open,Rename,Share,Delete", `the row menu reads ${labels}`);
      await page.locator(".dc-context-menu .dropdown-item", { hasText: "Share" }).click();
      await page.waitForSelector("[data-form-modal].show [data-share-new]", { timeout: 10000 });
      const picked = await page.$$eval("[data-share-new] [data-picker-value]:checked", (els) => els.map((el) => `${el.name}=${el.value}`));
      const at = new URL(page.url());
      assert(picked.join(",") === `assistant=${fx.b}` && at.pathname === "/settings/sharing" && !at.search, `Sharing opened at ${page.url()} with ${picked}`);
      const entry = page.locator(`[data-form-modal].show [data-share-entries] [data-share-picked="assistant:${fx.b}"]`);
      assert(await entry.isVisible() && (await entry.textContent()).includes("Assistant, read only") && await entry.locator("[data-share-grant]").count() === 0, "the assistant does not stand in New link's list read only");
      await page.click("[data-form-modal].show .btn-close");
    });

    await run("the guest moves in one tab between the project, the conversation and the assistant's coders", async () => {
      guest = await guestPage(browser, bag, fx, ORIGIN);
      const gp = guest.page;
      await gp.goto(fx.link, { waitUntil: "domcontentloaded" });
      assert(new URL(gp.url()).pathname === `${fx.base}/projects/${PROJECT}/editor`, `landed on ${gp.url()}`);
      await gp.waitForSelector(".editor-file", { timeout: 15000 });
      const rail = await gp.$$eval(".dc-rail a", (els) => els.map((el) => `${el.textContent.trim()} ${el.getAttribute("href")}`));
      assert(rail.length === 3 && rail.every((r) => r.includes(fx.base)) && rail[2].startsWith("Assistants"), `the rail holds ${JSON.stringify(rail)}`);
      await gp.click(`.dc-rail a[href="${fx.base}/assistants"]`);
      await gp.waitForURL((u) => u.pathname === `${fx.base}/assistants/${fx.a}`, { timeout: 10000 });
      await gp.waitForSelector("dc-share-chat [data-share-log]", { state: "attached", timeout: 10000 });
      assert((await gp.textContent("[data-share-title]")).trim() === `helper ${TAG}`, "the conversation's head names another assistant");
      assert(!(await gp.$("[data-assistant-input], [data-assistant-send], [data-assistant-speak], [data-assistant-voice], [data-assistant-menu], [data-assistant-memory-button], [data-assistant-trigger-new], dc-model-pick")), "an owner control stands on the guest's conversation");
      const column = await gp.$$eval("[data-assistant-rows] [data-assistant-instance]", (els) => els.map((el) => el.getAttribute("href")));
      assert(column.join(",") === `${fx.base}/assistants/${fx.a}`, `the list column holds ${column}`);
      await gp.click(`.dc-rail a[href="${fx.base}/terminals"]`);
      await gp.waitForSelector("terminal-tabs .terminal-tab", { timeout: 10000 });
      const tabs = await gp.$$eval("terminal-tabs .terminal-tab", (els) => els.map((el) => el.getAttribute("href")));
      for (const id of [fx.steering, fx.once]) assert(tabs.includes(`${fx.base}/coders/${id}`), `the strip misses the job coder ${id}: ${tabs}`);
      for (const id of [fx.stopped, fx.foreign]) assert(!tabs.some((h) => h.endsWith(id)), `the strip lists ${id}: ${tabs}`);
      await gp.click(`terminal-tabs .terminal-tab[href="${fx.base}/coders/${fx.steering}"]`);
      await gp.waitForURL((u) => u.pathname === `${fx.base}/coders/${fx.steering}`, { timeout: 10000 });
      await gp.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
      assert(!(await gp.$("terminal-input, [data-terminal-control], [data-tab-close], [data-tab-menu], dc-inline-rename")), "the coder's page offers a write");
      assert(!(await gp.$(`a[href*="/projects/${OFF}"]`)), "the coder's page links the project off the link");
      await gp.click("[data-terminal-copy]:visible");
      await gp.waitForSelector("terminal-copy:not([hidden]) [data-copy-log]:not([hidden]) [data-message-id]", { timeout: 15000 });
      assert((await gp.textContent("terminal-copy [data-copy-log]")).includes(`job-steering-${TAG}`), "the conversation face misses the coder's task");
      assert(!(await gp.isVisible("[data-copy-hint]")), "the copy view tells the guest to type");
    });

    await run("the guest's coders wear the steered mark and a live working ring, in the list, the sheet and the head", async () => {
      const gp = guest.page;
      const tab = (base, id) => `terminal-tabs .terminal-tab[href="${base}/coders/${id}"] .dc-term-icon`;
      const head = "dc-steered-mark .dc-term-icon";
      await gp.goto(`${BASE}${fx.base}/coders/${fx.mine}`, { waitUntil: "domcontentloaded" });
      await gp.waitForSelector(tab(fx.base, fx.steering), { timeout: 10000 });
      await page.goto(`${BASE}/coders/${fx.mine}`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(tab("", fx.foreign), { timeout: 10000 });
      const steered = await iconOf(gp, tab(fx.base, fx.steering));
      const plain = await iconOf(gp, tab(fx.base, fx.mine));
      const owners = await iconOf(page, tab("", fx.steering));
      assert(steered?.steered && !plain?.steered && steered.color === owners?.color && steered.color !== plain.color, `the strip's marks read ${JSON.stringify({ steered, plain, owners })}`);
      const phone = await guestPage(browser, bag, fx, ORIGIN, { hasTouch: true, isMobile: true, viewport: { width: 390, height: 844 } });
      try {
        const mp = phone.page;
        await mp.goto(`${fx.link}/coders/${fx.steering}`, { waitUntil: "domcontentloaded" });
        await mp.waitForSelector(head, { state: "visible", timeout: 10000 });
        const own = await iconOf(mp, head);
        assert(own?.steered && own.color === owners.color, `the phone's head reads ${JSON.stringify(own)}`);
        await mp.tap('.dc-tabbar [data-ctx-area="terminals"]');
        const row = `[data-ctx-sheet]:not([hidden]) a[href="${fx.base}/coders/${fx.steering}"] .dc-term-icon`;
        await mp.waitForSelector(row, { state: "visible", timeout: 10000 });
        assert((await iconOf(mp, row))?.steered, "the phone's sheet drops the steered mark");
        await mp.goto(`${fx.link}/coders/${fx.mine}`, { waitUntil: "domcontentloaded" });
        await mp.waitForSelector(head, { state: "visible", timeout: 10000 });
        await sleep(800);
        await gp.evaluate(() => {
          window.dcHeard = [];
          window.dcStay = Math.random();
          document.addEventListener("dc:activity", (event) => window.dcHeard.push(JSON.stringify(event.detail)));
        });
        const marker = await gp.evaluate(() => window.dcStay);
        hook(fx.mine, "UserPromptSubmit");
        hook(fx.foreign, "UserPromptSubmit");
        await Promise.all([ringed(gp, tab(fx.base, fx.mine), true), ringed(mp, head, true), ringed(page, tab("", fx.foreign), true)]);
        hook(fx.mine, "Stop");
        await Promise.all([ringed(gp, tab(fx.base, fx.mine), false), ringed(mp, head, false)]);
        assert(await gp.evaluate(() => window.dcStay) === marker, "the guest's page reloaded");
        const heard = await gp.evaluate(() => window.dcHeard);
        assert(heard.some((d) => d.includes(fx.mine)) && !heard.some((d) => d.includes(fx.foreign)), `the guest heard ${JSON.stringify(heard)}`);
      } finally {
        await phone.ctx.close().catch(() => {});
      }
    });

    await run("a coder the assistant steers later joins the open guest's strip without a reload", async () => {
      const gp = guest.page;
      const marker = await gp.evaluate(() => { window.dcStay = Math.random(); return window.dcStay; });
      fx.later = await newCoder(page, fx[OFF], `later-${TAG}`);
      await sleep(1500);
      assert(!(await gp.$(`terminal-tabs .terminal-tab[href="${fx.base}/coders/${fx.later}"]`)), "an unsteered coder of a project off the link joined the strip");
      await steer(page, fx.a, fx.later);
      await gp.waitForSelector(`terminal-tabs .terminal-tab[href="${fx.base}/coders/${fx.later}"]`, { state: "attached", timeout: 15000 });
      assert(await gp.evaluate(() => window.dcStay) === marker, "the guest's page reloaded");
    });

    await run("a stopped job coder, another assistant's coder and that assistant answer the plain 404", async () => {
      for (const [path, what] of [
        [`/coders/${fx.stopped}`, "the stopped job coder"],
        [`/coders/${fx.stopped}/stream`, "the stopped job coder's stream"],
        [`/coders/${fx.foreign}`, "the other assistant's coder"],
        [`/coders/${fx.foreign}/stream`, "the other assistant's coder's stream"],
        [`/coders/${fx.foreign}/conversation`, "the other assistant's coder's conversation"],
        [`/assistants/${fx.b}`, "the other assistant"],
        [`/assistants/${fx.b}/stream`, "the other assistant's stream"],
        [`/assistants/${fx.b}/media/CLAUDE.md`, "the other assistant's workspace"],
      ]) await notFound(`${fx.link}${path}`, what);
      assert((await fetchRaw(`${fx.link}/coders/${fx.once}`)).status === 200, "the once steered coder does not open");
    });

    await run("a coder another assistant steers now leaves the link that showed it", async () => {
      const gp = guest.page;
      await gp.goto(`${BASE}${fx.base}/coders/${fx.steering}`, { waitUntil: "domcontentloaded" });
      await gp.waitForSelector(`terminal-tabs .terminal-tab[href="${fx.base}/coders/${fx.once}"]`, { state: "attached", timeout: 10000 });
      await steer(page, fx.b, fx.once);
      await gp.waitForSelector(`terminal-tabs .terminal-tab[href="${fx.base}/coders/${fx.once}"]`, { state: "detached", timeout: 15000 });
      await notFound(`${fx.link}/coders/${fx.once}`, "the coder the other assistant took over");
    });

    await run("beside the chat the guest reads the assistant's jobs and triggers, read only, and nothing of the other's", async () => {
      const gp = guest.page;
      const aside = "#assistant-aside";
      const controls = `[data-assistant-job-stop], [data-assistant-job-again], [data-assistant-trigger-new], [data-assistant-trigger-edit], [data-assistant-trigger-remove], ${aside} form`;
      await gp.goto(`${BASE}${fx.base}/assistants/${fx.a}`, { waitUntil: "domcontentloaded" });
      await gp.waitForSelector(`${aside} [data-assistant-job="${fx.steering}"]`, { state: "visible", timeout: 10000 });
      const jobs = await gp.$$eval(`${aside} [data-assistant-job]`, (els) => els.map((el) => el.dataset.assistantJob));
      for (const id of [fx.steering, fx.once, fx.stopped, fx.later]) assert(jobs.includes(id), `the jobs miss ${id}: ${jobs}`);
      assert(!jobs.includes(fx.foreign), "the other assistant's job is listed");
      const links = (id) => gp.$$eval(`${aside} [data-assistant-job="${id}"] a[href]`, (els) => els.map((el) => el.getAttribute("href")));
      const steering = await links(fx.steering);
      assert(steering.length === 2 && steering.every((h) => h === `${fx.base}/coders/${fx.steering}`), `the steered coder's row leads to ${steering}`);
      for (const id of [fx.once, fx.stopped]) assert(!(await links(id)).length, `the row of ${id} leads somewhere`);
      assert(!(await gp.$(controls)), "the guest's aside offers a change");
      await gp.click("#assistant-triggers-tab");
      const trigger = `${aside} [data-assistant-trigger="${fx.trigA}"]`;
      await gp.waitForSelector(trigger, { state: "visible", timeout: 5000 });
      assert(!(await gp.$(`[data-assistant-trigger="${fx.trigB}"]`)), "the other assistant's trigger is listed");
      assert(await gp.getAttribute(`${trigger} [data-assistant-trigger-open]`, "href") === `${fx.base}/coders/${fx.steering}`, "the trigger's coder opens outside the share");
      const marker = await gp.evaluate(() => { window.dcStay = Math.random(); return window.dcStay; });
      const later = await postJSON(page, "/assistants/triggers", { form: "new", assistant: fx.a, event: "cron", spec: "0 4 * * *", task: "EVENT_NOTHING later", batch: "0" });
      await gp.waitForSelector(`${aside} [data-assistant-trigger="${later.id}"]`, { state: "visible", timeout: 15000 });
      assert(await gp.evaluate(() => window.dcStay) === marker, "the guest's page reloaded");
      assert(!(await gp.$(controls)), "the refreshed aside offers a change");
      const hrefs = await gp.$$eval(`${aside} a[href]`, (els) => els.map((el) => el.getAttribute("href")));
      assert(hrefs.length && hrefs.every((h) => h.startsWith(`${fx.base}/`)), `the aside links ${hrefs}`);
      await notFound(`${fx.link}/assistants/jobs?assistant=${fx.b}`, "the other assistant's jobs");
      await notFound(`${fx.link}/assistants/triggers?assistant=${fx.b}`, "the other assistant's triggers");
      const form = await fetchRaw(`${fx.link}/assistants/triggers?form=new&assistant=${fx.a}`);
      assert(form.status === 200 && !form.body.includes("<form") && form.body.includes(`data-assistant-trigger="${fx.trigA}"`), `the trigger form answered ${form.status}`);
    });

    await run("the answer streams to the open guest while it is written and links inside the share when done", async () => {
      const gp = guest.page;
      await gp.goto(`${BASE}${fx.base}/assistants/${fx.a}`, { waitUntil: "domcontentloaded" });
      await gp.waitForSelector("dc-share-chat [data-share-log]", { state: "attached", timeout: 10000 });
      await sleep(500);
      const lines = [1, 2, 3, 4, 5, 6].map((n) => `line ${n} of ${TAG}`);
      const message = ["ECHO_ANSWER", ...lines,
        `Coder \`steering\` (${short(fx.steering)}), mine (${short(fx.mine)}), stopped (${short(fx.stopped)}), foreign (${short(fx.foreign)}).`,
        `Files ${fx[PROJECT]}/README.md and ${fx[OFF]}/README.md, the [run](/projects/${PROJECT}/docker/runs/1), the [settings](/settings), [home](${ORIGIN}/settings), [far](https://example.com).`].join("\n");
      await postJSON(page, `/assistants/${fx.a}`, { form: "message", message });
      let seen = null;
      const until = Date.now() + 20000;
      while (Date.now() < until && !seen) {
        const b = await bubble(gp, '[data-role="assistant"]');
        if (b && b.state === "streaming" && b.text.includes(lines[0]) && !b.text.includes("Files ")) seen = b.text;
        await sleep(100);
      }
      assert(seen, "the guest never saw the answer while it was written");
      await gp.waitForFunction(() => {
        const nodes = document.querySelectorAll('[data-share-log] [data-role="assistant"]');
        return nodes.length && nodes[nodes.length - 1].dataset.state === "complete";
      }, null, { timeout: 30000 });
      const done = await bubble(gp, '[data-role="assistant"]');
      const hrefs = done.links.map((l) => l.href);
      assert(done.text.includes("Files ") && done.text.includes("the run, the settings"), `the answer reads ${done.text}`);
      for (const id of [fx.steering, fx.mine]) {
        assert(done.links.some((l) => l.href === `${fx.base}/coders/${id}` && l.text === short(id)), `the short id of ${id} links ${JSON.stringify(done.links)}`);
      }
      assert(hrefs.some((h) => h.startsWith(`${fx.base}/projects/${PROJECT}/editor?file=README.md`)), `the project's file does not link inside the share: ${hrefs}`);
      assert(hrefs.includes("https://example.com") && hrefs.includes(`${ORIGIN}/settings`), `a link naming a host got lost: ${hrefs}`);
      const leaving = hrefs.filter((h) => h.startsWith("/") && !h.startsWith(`${fx.base}/`));
      assert(!leaving.length && !hrefs.some((h) => h.includes(fx.stopped) || h.includes(fx.foreign) || h.includes(`/projects/${OFF}`)), `links leaving the share: ${hrefs}`);
      assert(done.text.includes(`${fx[OFF]}/README.md`) && done.text.includes(short(fx.foreign)), "a name that stays text got lost");
      await gp.click(`[data-share-log] a[href="${fx.base}/coders/${fx.steering}"]`);
      await gp.waitForURL((u) => u.pathname === `${fx.base}/coders/${fx.steering}`, { timeout: 10000 });
      await gp.waitForSelector("#terminal .xterm-screen", { timeout: 15000 });
    });

    await run("of the workspace the guest reads the file a message links and nothing else", async () => {
      await postJSON(page, `/assistants/${fx.a}`, { form: "message", message: "FILELINK please" });
      await waitIdle(page, fx.a);
      const page2 = await fetchRaw(`${fx.link}/assistants/${fx.a}`);
      const href = (page2.body.match(/href="([^"]*\/media\/assistant-files\/proof\.txt)[^"]*"/) || [])[1];
      assert(href && href.startsWith(`${fx.base}/assistants/${fx.a}/media/`), `the linked file is ${href}`);
      const file = await fetchRaw(`${ORIGIN}${href}`);
      assert(file.status === 200 && file.body.startsWith("proof from") && file.headers["content-security-policy"] === "sandbox", `the linked file answered ${file.status}`);
      for (const rel of ["CLAUDE.md", "AGENTS.md", "cockpit", "assistant-files/other.txt", "user-upload/x.png"]) {
        await notFound(`${fx.link}/assistants/${fx.a}/media/${rel}`, rel);
      }
    });

    await run("nothing the guest sends writes into the assistant or spends a turn", async () => {
      const asked = (messages) => messages.filter((m) => m.role === "user").length;
      const before = asked(await thread(page, fx.a));
      const csrf = await guest.page.evaluate(() => document.querySelector('meta[name="csrf-token"]')?.content || "");
      const headers = { "Content-Type": "application/x-www-form-urlencoded", "X-CSRF-Token": csrf, Accept: "application/json" };
      for (const [path, form] of [
        [`/assistants/${fx.a}`, "form=message&message=MAGIC"],
        [`/assistants/${fx.a}`, "form=retry"],
        [`/assistants/${fx.a}`, "form=delete"],
        [`/assistants/${fx.a}/user-upload`, ""],
        [`/assistants/${fx.a}/stt`, ""],
        ["/assistants/jobs", `form=release&terminal=${fx.steering}`],
        ["/assistants/jobs", `form=steer&assistant=${fx.a}&terminal=${fx.mine}&task=x&done_when=x`],
        ["/assistants/triggers", `form=remove&id=${fx.trigA}`],
        ["/assistants/triggers", `form=new&assistant=${fx.a}&event=cron&spec=0+5+*+*+*&task=MAGIC`],
        ["/assistants/new", "form=new&coder=claude"],
        ["/assistants/memory", "form=save"],
        [`/coders/${fx.steering}/input`, "{}"],
      ]) {
        const res = await fetchRaw(`${fx.link}${path}`, { method: "POST", body: form, headers });
        assert(res.status === 404 || res.status === 405, `POST ${path} answered ${res.status}`);
      }
      for (const path of [`/assistants/${fx.a}/draft`, "/assistants/memory", "/assistants/triggers", "/assistants/models", `/assistants/instances/${fx.a}`, "/ctx/assistant"]) {
        await notFound(`${fx.link}${path}`, `GET ${path}`);
      }
      await sleep(1500);
      const after = await thread(page, fx.a);
      assert(asked(after) === before && !after.some((m) => (m.content || "").includes("MAGIC")), `the guest asked ${asked(after) - before} times`);
      const state = await jobState(page, fx.a, fx.steering);
      assert(state === "steering" && await jobState(page, fx.a, fx.mine) === "", `the guest moved the jobs, steering reads ${state}`);
      const triggers = await page.evaluate(async (p) => (await (await fetch(p, { headers: { Accept: "application/json" } })).json()).triggers || [], `/assistants/triggers?assistant=${fx.a}`);
      assert(triggers.some((t) => t.id === fx.trigA) && !triggers.some((t) => (t.task || "").includes("MAGIC")), `the guest moved the triggers: ${JSON.stringify(triggers.map((t) => t.id))}`);
      const outside = fx.requests.filter((r) => { const p = r.split(" ")[1]; return !p.startsWith(`${fx.base}/`) && p !== fx.base && !/^\/(js|css)\//.test(p) && !/^\/[\w.-]+\.(svg|png|json)$/.test(p); });
      assert(!outside.length, `guest requests left the link: ${JSON.stringify(outside.slice(0, 5))}`);
    });

    await run("the guest sees a turn as the owner does: on the assistant's row, in the switcher, the phone's head and the chat", async () => {
      const gp = guest.page;
      const row = `[data-assistant-rows] [data-assistant-instance="${fx.a}"] .dc-term-icon.assistant`;
      const head = "[data-share-head-icon]";
      await page.goto(`${BASE}/assistants/${fx.a}`, { waitUntil: "domcontentloaded" });
      await gp.goto(`${BASE}${fx.base}/assistants/${fx.a}`, { waitUntil: "domcontentloaded" });
      await gp.waitForSelector("dc-share-chat [data-share-log]", { state: "attached", timeout: 10000 });
      const phone = await guestPage(browser, bag, fx, ORIGIN, { hasTouch: true, isMobile: true, viewport: { width: 390, height: 844 } });
      try {
        const mp = phone.page;
        await mp.goto(`${fx.link}/assistants/${fx.a}`, { waitUntil: "domcontentloaded" });
        await mp.waitForSelector("dc-share-chat [data-share-log]", { state: "attached", timeout: 10000 });
        await sleep(800);
        const marker = await gp.evaluate(() => { window.dcStay = Math.random(); return window.dcStay; });
        await postJSON(page, `/assistants/${fx.a}`, { form: "message", message: "SLOW TOOL hold the turn" });
        await Promise.all([ringed(gp, row, true), ringed(page, row, true), ringed(mp, head, true)]);
        const [mine, owners] = [await iconOf(gp, row), await iconOf(page, row)];
        assert(mine.ring === owners.ring && mine.color === owners.color, `the rows ring apart: ${JSON.stringify({ mine, owners })}`);
        const tool = await gp.waitForFunction(() => {
          const line = [...document.querySelectorAll("[data-share-log] [data-assistant-tool]")].pop();
          return line && line.getClientRects().length ? `${line.firstChild?.nextSibling?.textContent} ${line.querySelector("[data-assistant-tool-name]").textContent}` : "";
        }, null, { timeout: 15000 });
        assert(await tool.jsonValue() === "Working Edit", `the guest's tool line reads ${await tool.jsonValue()}`);
        assert(await gp.evaluate(() => window.dcStay) === marker, "the guest's page reloaded");
        await gp.goto(`${BASE}${fx.base}/coders/${fx.steering}`, { waitUntil: "domcontentloaded" });
        await gp.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
        await gp.click(".attach-terminal");
        await sleep(200);
        await gp.keyboard.press("Control");
        await gp.keyboard.press("Control");
        await gp.waitForSelector(".terminal-switcher", { state: "visible", timeout: 4000 });
        const item = `.terminal-switcher-item[data-switcher-url="${fx.base}/assistants/${fx.a}"] .dc-term-icon`;
        await ringed(gp, item, true);
        const urls = await gp.$$eval(".terminal-switcher-item[data-switcher-url]", (els) => els.map((el) => el.dataset.switcherUrl));
        assert(urls.every((u) => u.startsWith(`${fx.base}/`)) && !urls.some((u) => u.includes(fx.b)), `the guest's switcher lists ${urls}`);
        await postJSON(page, `/assistants/${fx.a}`, { form: "cancel" });
        await Promise.all([ringed(gp, item, false), ringed(mp, head, false), ringed(page, row, false)]);
        await gp.keyboard.press("Escape");
        await waitIdle(page, fx.a);
      } finally {
        await phone.ctx.close().catch(() => {});
      }
    });

    await run("a check and a trigger's reaction wear the owner's working ring in the guest's Watching, live", async () => {
      const gp = guest.page;
      fx.slow = await newCoder(page, fx[OFF], `slow-${TAG}`);
      fx.done = await newCoder(page, fx[OFF], `done-${TAG}`);
      for (const [terminal, doneWhen] of [[fx.slow, "SLOW WAKE_NOTHING: never true"], [fx.done, "WAKE_DONE: the file is there"]]) {
        await postJSON(page, "/assistants/jobs", { form: "steer", assistant: fx.a, terminal, task: "Share check", done_when: doneWhen });
      }
      fx.trigSlow = (await postJSON(page, "/assistants/triggers", { form: "new", assistant: fx.a, event: "job-done", terminal: fx.done, task: "SLOW keep working on it", batch: "0", once: "on" })).id;
      const check = `#assistant-aside [data-assistant-job="${fx.slow}"] [data-assistant-working="check"]`;
      const react = `#assistant-aside [data-assistant-trigger="${fx.trigSlow}"] [data-assistant-working="react"]`;
      for (const [p, base] of [[page, ""], [gp, fx.base]]) {
        await p.goto(`${BASE}${base}/assistants/${fx.a}`, { waitUntil: "domcontentloaded" });
        await p.waitForSelector(`#assistant-aside [data-assistant-job="${fx.slow}"]`, { state: "visible", timeout: 10000 });
      }
      const marker = await gp.evaluate(() => { window.dcStay = Math.random(); return window.dcStay; });
      hook(fx.slow, "Stop");
      hook(fx.done, "Stop");
      await Promise.all([ringed(gp, check, true), ringed(page, check, true)]);
      const checks = [await iconOf(gp, check), await iconOf(page, check)];
      for (const p of [page, gp]) await p.click("#assistant-triggers-tab");
      await Promise.all([ringed(gp, react, true), ringed(page, react, true)]);
      const reactions = [await iconOf(gp, react), await iconOf(page, react)];
      for (const [mine, owners] of [checks, reactions]) {
        assert(mine.ring === owners.ring && mine.color === owners.color, `the guest's ring differs: ${JSON.stringify({ mine, owners })}`);
      }
      assert(await gp.evaluate(() => window.dcStay) === marker, "the guest's page reloaded");
    });

    await run("on a phone the tab bar opens the assistants' sheet and the conversation", async () => {
      const phone = await guestPage(browser, bag, fx, ORIGIN, { hasTouch: true, isMobile: true, viewport: { width: 390, height: 844 } });
      try {
        const mp = phone.page;
        await mp.goto(`${fx.link}/assistants/${fx.a}`, { waitUntil: "domcontentloaded" });
        assert(!(await mp.isVisible(`#assistant-aside [data-assistant-job="${fx.steering}"]`)), "the phone shows the aside before it is opened");
        await mp.tap("[data-assistant-watching-button]");
        await mp.waitForSelector(`#assistant-aside.show [data-assistant-job="${fx.steering}"]`, { state: "visible", timeout: 10000 });
        await mp.tap("#assistant-aside [data-bs-dismiss=\"offcanvas\"]");
        await mp.waitForSelector("#assistant-aside", { state: "hidden", timeout: 10000 });
        const bar = await mp.$$eval(".dc-tabbar .dc-tabbar-btn", (els) => els.map((el) => el.textContent.trim()));
        assert(bar.join(",") === "Editor,Terminals,Assistants", `the tab bar holds ${bar}`);
        await mp.tap('.dc-tabbar [data-ctx-area="assistants"]');
        await mp.waitForSelector(`[data-ctx-sheet]:not([hidden]) [data-assistant-instance="${fx.a}"]`, { timeout: 10000 });
        assert(!(await mp.$("[data-ctx-sheet] [data-assistant-menu], [data-ctx-sheet] [data-assistant-grip]")), "the sheet's row carries a menu or a grip");
        const width = await mp.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1);
        assert(width, "the guest's conversation is wider than the phone");
      } finally {
        await phone.ctx.close().catch(() => {});
      }
    });

    await run("deleting the assistant ends a link of it alone and takes it off the shared one", async () => {
      fx.alone = await linkOver(page, [["assistant", fx.a]]);
      const gp = guest.page;
      await gp.goto(`${fx.alone}/assistants/${fx.a}`, { waitUntil: "domcontentloaded" });
      await gp.waitForSelector("dc-share-chat [data-share-log]", { state: "attached", timeout: 10000 });
      await sleep(800);
      await postJSON(page, `/assistants/${fx.a}`, { form: "delete" });
      fx.deleted = fx.a;
      await gp.waitForSelector("[data-share-ended]:not([hidden])", { timeout: 15000 });
      assert(await gp.isVisible("[data-share-ended]"), "the ended notice does not show");
      await notFound(fx.alone, "the link of the deleted assistant alone");
      await notFound(`${fx.link}/assistants/${fx.a}`, "the deleted assistant under the shared link");
      await notFound(`${fx.link}/assistants`, "the Assistants entry of the shared link");
      await notFound(`${fx.link}/coders/${fx.steering}`, "the deleted assistant's coder");
      assert((await fetchRaw(`${fx.link}/projects/${PROJECT}/editor`)).status === 200, "the project left with the assistant");
      await page.goto(`${BASE}/settings/sharing`, { waitUntil: "domcontentloaded" });
      const row = page.locator(`[data-share-row="${fx.linkId}"]`);
      assert(await row.locator(`[data-share-project="${PROJECT}"]`).count() === 1 && await row.locator("[data-share-assistant]").count() === 0, "the shared link still lists the assistant");
      assert(!(await page.content()).includes(fx.alone.split("/share/")[1].slice(0, 12)), "the ended link is still listed");
      const rail = await fetchRaw(`${fx.link}/projects/${PROJECT}/editor`);
      assert(!rail.body.includes(`${fx.base}/assistants"`), "the guest's rail still offers the assistants");
    });
  } finally {
    if (guest) await guest.ctx.close().catch(() => {});
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" }).catch(() => {});
    for (const id of [fx.steering, fx.once, fx.stopped, fx.foreign, fx.later, fx.mine, fx.slow, fx.done].filter(Boolean)) {
      await postAs(page, `/coders/${id}/delete`, {}).catch(() => {});
    }
    for (const id of [fx.a, fx.b].filter((id) => id && id !== fx.deleted)) await postAs(page, `/assistants/${id}`, { form: "delete" }).catch(() => {});
    if (fx.linkId) await postAs(page, `/settings/sharing/${fx.linkId}`, { form: "revoke" }).catch(() => {});
    for (const name of [PROJECT, OFF]) await L.deleteProject(page, name).catch(() => {});
  }
});
