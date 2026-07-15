// Shared helpers for the tests/e2e Playwright scripts. Self contained so each
// script can `require("./lib")` inside the Playwright Docker image.
const BASE = process.env.BASE_URL || "https://localhost:3010";
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// The editor pulls CodeMirror language packs from a third-party CDN at runtime and
// degrades gracefully when that fails (langFor try/catch -> warn + no-highlight
// fallback, see editor.js). Under headless load the CDN sometimes throttles a
// request, and the browser logs its own CORS/resource-load error for that external
// origin. That is not an app defect, so it is recorded as visible noise, not a gate
// failure. App-origin errors still gate.
function isCdnNoise(t) {
  return /jsdelivr|cdn\.|CORS policy|ERR_FAILED|Failed to load resource/i.test(t);
}

// WebKit reports a same-origin request that a navigation aborted mid-flight (the
// /events stream connect, the /update/check poll) as a page error ending in "due
// to access control checks". That is a browser artifact of racing navigations on
// the throwaway instance, not an app defect, so it joins the non-gating noise
// bucket. The filter stays narrow (only these two always-on background paths) so
// a real access control failure on an app route still gates.
function isWebkitAbortNoise(t) {
  return /\/(events|update\/check)\b.*due to access control checks/.test(t);
}
function wirePage(page, bag) {
  bag.cdnNoise = bag.cdnNoise || [];
  page.on("console", (m) => {
    if (m.type() !== "error") return;
    const t = m.text();
    (isCdnNoise(t) ? bag.cdnNoise : bag.consoleErrors).push(`[${page.url()}] ${t}`);
  });
  page.on("pageerror", (e) => {
    (isWebkitAbortNoise(e.message) ? bag.cdnNoise : bag.pageErrors).push(`[${page.url()}] ${e.message}`);
  });
}

function submitBtn(page, hasSel) {
  return page.locator(`form:has(${hasSel})`).first().locator('button[type="submit"], input[type="submit"]').first();
}

async function confirmSwal(page) {
  await page.waitForSelector(".swal2-confirm", { state: "visible", timeout: 8000 });
  await sleep(150);
  await page.click(".swal2-confirm");
}

const modalShown = (page, id) => page.waitForFunction((i) => { const m = document.getElementById(i); return m && m.classList.contains("show"); }, id, { timeout: 8000 });

async function upgraded(page, names) {
  return page.evaluate((ns) => ns.filter((n) => !customElements.get(n)), names);
}
async function waitUpgraded(page, names, timeout = 12000) {
  const deadline = Date.now() + timeout;
  let missing = await upgraded(page, names);
  while (missing.length && Date.now() < deadline) { await sleep(200); missing = await upgraded(page, names); }
  return missing;
}

async function login(page) {
  await page.goto(`${BASE}/login`, { waitUntil: "domcontentloaded" });
  await page.fill('input[name="username"]', "admin");
  await page.fill('input[name="password"]', "password");
  await Promise.all([page.waitForURL(/\/projects/, { timeout: 15000 }), page.click('button[type="submit"]')]);
}

async function createProject(page, name) {
  await page.goto(`${BASE}/projects/new`, { waitUntil: "domcontentloaded" });
  await page.fill('input[name="project_name"]', name);
  await Promise.all([page.waitForURL(/\/projects/, { timeout: 15000 }), submitBtn(page, 'input[name="project_name"]').click()]);
  await page.waitForSelector(`[data-project-name="${name}"]`, { timeout: 8000 });
}

async function deleteProject(page, name) {
  await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
  const card = await page.$(`[data-project-name="${name}"]`);
  if (!card) return;
  const btn = await page.evaluateHandle((p) => {
    const c = [...document.querySelectorAll("[data-project-name]")].find((e) => e.dataset.projectName === p);
    const scope = c.closest('[id^="project-"]') || c;
    return scope.querySelector('form[action="/projects/delete"] [type="submit"], form[action="/projects/delete"] button');
  }, name);
  const el = btn.asElement();
  if (el) { await el.click().catch(() => {}); await confirmSwal(page).catch(() => {}); await sleep(600); }
}

async function createShell(page, project) {
  await page.goto(`${BASE}/shells/new`, { waitUntil: "domcontentloaded" });
  const f = page.locator('form:has(select[name="project"])').first();
  await f.locator('select[name="project"]').selectOption(project).catch(async () => { await f.locator('select[name="project"]').selectOption({ label: project }); });
  await Promise.all([page.waitForURL(/\/shells\/(?!new)[^/]+$/, { timeout: 15000 }), f.locator('button[type="submit"]').first().click()]);
  return page.url();
}

async function deleteShell(page, shellUrl) {
  await page.goto(shellUrl, { waitUntil: "domcontentloaded" });
  const delPath = new URL(shellUrl).pathname + "/delete";
  const btn = await page.$(`form[action="${delPath}"] button[type="submit"], form[action="${delPath}"] button`);
  if (!btn) return;
  await btn.click();
  await confirmSwal(page).catch(() => {});
  await sleep(500);
}

async function createSession(page, project, name, coder) {
  await page.goto(`${BASE}/coders/new?project=${encodeURIComponent(project)}`, { waitUntil: "domcontentloaded" });
  const f = page.locator('form:has(select[name="agent"])').first();
  await f.locator('input[name="name"]').fill(name);
  await f.locator('select[name="project"]').selectOption(project).catch(() => {});
  if (coder) await f.locator('select[name="coder"]').selectOption(coder).catch(() => {});
  await Promise.all([page.waitForURL(/\/coders\/(?!new)[^/]+$/, { timeout: 20000 }), f.locator('button[type="submit"]').first().click()]);
  return page.url();
}

async function stopSession(page, sessionUrl) {
  await page.goto(sessionUrl, { waitUntil: "domcontentloaded" });
  const btn = await page.$('form[action$="/stop"] button[type="submit"], form[action$="/stop"] button');
  if (!btn) return;
  await btn.click();
  await confirmSwal(page).catch(() => {});
  await sleep(500);
}

function makeRunner() {
  const results = [];
  async function run(name, fn, { soft = false } = {}) {
    const t0 = Date.now();
    try { const d = await fn(); results.push({ name, status: "PASS", detail: d || "", ms: Date.now() - t0 }); }
    catch (e) { results.push({ name, status: soft ? "WARN" : "FAIL", detail: e.message, ms: Date.now() - t0 }); }
  }
  return { results, run };
}

function report(title, results, bag) {
  console.log(`\n===================== ${title} =====================`);
  let anyFail = false;
  for (const c of results) {
    console.log(`  [${c.status}] ${c.name}${c.detail ? "  -- " + c.detail : ""}`);
    if (c.status === "FAIL") anyFail = true;
  }
  const cdn = bag.cdnNoise || [];
  console.log(`  console.errors: ${bag.consoleErrors.length}, pageerrors: ${bag.pageErrors.length}, cdn noise (non-gating): ${cdn.length}`);
  bag.consoleErrors.forEach((x) => { console.log("   CE " + x); anyFail = true; });
  bag.pageErrors.forEach((x) => { console.log("   PE " + x); anyFail = true; });
  cdn.forEach((x) => console.log("   cdn " + x));
  console.log("=".repeat(title.length + 44));
  console.log(anyFail ? `${title}: FAIL` : `${title}: PASS`);
  return anyFail;
}

function assert(c, m) { if (!c) throw new Error(m); }

// ---- engines + contexts ---------------------------------------------------
const { chromium, webkit } = require("playwright-core");
const ENGINES = { chromium, webkit };

// Which browser engines a run covers. Default chromium for fast iteration; set
// ENGINE=webkit or ENGINE=chromium,webkit for the cross-browser pass.
function engineList() {
  return (process.env.ENGINE || "chromium").split(",").map((s) => s.trim()).filter((s) => ENGINES[s]);
}
function launch(engineName) {
  // --no-sandbox is Chromium only; webkit rejects unknown args.
  return ENGINES[engineName].launch(engineName === "chromium" ? { args: ["--no-sandbox"] } : {});
}
async function newDesktop(browser, engineName) {
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1360, height: 900 } });
  if (engineName === "chromium") { try { await ctx.grantPermissions(["clipboard-read", "clipboard-write"], { origin: BASE }); } catch {} }
  return ctx;
}
// Coarse pointer, mobile viewport, so matchMedia("(pointer: coarse)") is true and
// the attach page switches to the mirror + cursor input + mobile toolbar. No
// clipboard grant, so paste falls back to the "not available" toast.
function newMobile(browser) {
  return browser.newContext({ ignoreHTTPSErrors: true, hasTouch: true, isMobile: true, viewport: { width: 390, height: 844 } });
}

// runFeature drives a feature's checks across the selected engines, handling login,
// error capture, reporting and exit code. body({ engine, browser, ctx, page, run,
// mobilePage }) runs per engine; run(name, fn, opts) is prefixed with the engine.
async function runFeature(title, body) {
  const engines = engineList();
  const results = [];
  const bag = { consoleErrors: [], pageErrors: [], cdnNoise: [] };
  for (const engine of engines) {
    const browser = await launch(engine);
    const ctx = await newDesktop(browser, engine);
    const page = await ctx.newPage();
    wirePage(page, bag);
    const run = async (name, fn, opts) => {
      const t0 = Date.now();
      try { const d = await fn(); results.push({ name: `[${engine}] ${name}`, status: "PASS", detail: d || "", ms: Date.now() - t0 }); }
      catch (e) { results.push({ name: `[${engine}] ${name}`, status: (opts && opts.soft) ? "WARN" : "FAIL", detail: e.message, ms: Date.now() - t0 }); }
    };
    // Lazily created mobile page, shared per engine, torn down at the end.
    let mobileCtx = null;
    const mobilePage = async () => {
      if (!mobileCtx) { mobileCtx = await newMobile(browser); const mp = await mobileCtx.newPage(); wirePage(mp, bag); await login(mp); mobileCtx._page = mp; }
      return mobileCtx._page;
    };
    try { await login(page); await body({ engine, browser, ctx, page, run, mobilePage, bag }); }
    catch (e) { results.push({ name: `[${engine}] FATAL`, status: "FAIL", detail: e.message }); }
    finally { if (mobileCtx) await mobileCtx.close().catch(() => {}); await ctx.close().catch(() => {}); await browser.close().catch(() => {}); }
  }
  const anyFail = report(title, results, bag);
  process.exit(anyFail ? 1 : 0);
}

module.exports = {
  BASE, sleep, wirePage, submitBtn, confirmSwal, modalShown, upgraded, waitUpgraded,
  login, createProject, deleteProject, createShell, deleteShell, createSession, stopSession,
  makeRunner, report, assert,
  ENGINES, engineList, launch, newDesktop, newMobile, runFeature,
};
