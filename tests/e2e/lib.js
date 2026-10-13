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
// /events stream connect, the /update/check and /costs/status polls, the
// page's one /time measurement, the editor's terminals fragment pull and its
// renewal of what it watches on disk, an assistant's pull of its own answer,
// a terminal's lookup of the words it printed, which catches its own failure)
// as a page error ending in "due to access control checks".
// That is a browser artifact of racing navigations on the throwaway
// instance, not an app defect, so it joins the non-gating noise bucket. The
// filter stays narrow so a real access control failure on an app route still
// gates.
//
// The bare `/projects` pull is the same artifact and gets its own pattern
// rather than a path in the list above, because it has to be matched to the
// **end** of the URL: dc-project-list answers a projects event by pulling the
// page itself (`refreshProjectList`, a fetch that already carries its own
// catch), and a runner navigating on while that pull is out leaves this error
// behind. An assistant's page pulls itself the same way (`syncFromServer`),
// and so does a split page whose members moved (`refreshPage`) and the tab
// strip on a terminals event (`tryRefresh`, whose writes under
// `/terminal-tabs/` still gate). Naming it in the list would swallow every project scoped route under
// it, `/projects/<name>/editor/…` included, which is exactly where a real
// access control failure would have to gate.
function isWebkitAbortNoise(t) {
  return /\/(events|update\/check|costs\/status|git\/prompt|time|editor\/(terminals|docker|lsp\/(status|source)|list|file|watch|git\/changes))\b.*due to access control checks/.test(t)
    || /\/(projects|terminal-tabs|assistants\/[^/\s?]+|splits\/[^/\s?]+)(\?\S*)? due to access control checks/.test(t)
    || /\/assistants\/[^/\s]+\/messages\/[^/\s]+ due to access control checks/.test(t)
    || /\/(shells|coders)\/[^/\s]+\/file-links due to access control checks/.test(t);
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

// WebKit applies the login form's autofocus late, which can steal focus while
// the second fill inserts its text, leaving "passwordadmin" in the username
// field and an empty required password (the submit then silently no-ops).
// Filling is therefore verified and retried before submitting.
async function login(page) {
  await page.goto(`${BASE}/login`, { waitUntil: "domcontentloaded" });
  for (let i = 0; i < 5; i += 1) {
    await page.fill('input[name="username"]', "admin");
    await page.fill('input[name="password"]', "password");
    const ok = await page.evaluate(() => document.querySelector('input[name="username"]').value === "admin" && document.querySelector('input[name="password"]').value === "password");
    if (ok) break;
    await sleep(200);
  }
  await Promise.all([page.waitForURL(/\/projects/, { timeout: 15000 }), page.click('button[type="submit"]')]);
}

// dismissUpdate closes the update dialog if this instance found a newer release.
// A runner that only clicks would hit its backdrop, and the failure looks like
// the form itself is broken. Instances started for a run usually point at a
// stub feed, but the README start command does not, so every runner has to cope.
async function dismissUpdate(page) {
  const cancel = page.locator(".swal2-cancel");
  try {
    await cancel.waitFor({ state: "visible", timeout: 1500 });
  } catch {
    return;
  }
  await cancel.click();
  await page.waitForSelector(".swal2-container", { state: "detached", timeout: 5000 }).catch(() => {});
}

// Settings, Editor, Files: the autosave switch and the disk poll interval sit
// in one form, so both are posted. Answers the interval it found, to put back.
// Autosave is on by default, and a runner that types into the editor and reads
// the dirty state has to switch it off. The boosted form fetches the page the
// save leads to after the POST answered, and a runner navigating on before it
// arrived aborts it, a WebKit page error.
async function setEditorFiles(page, { autosave, poll }) {
  const form = "#settings-editor-files";
  await page.goto(`${BASE}/settings/editor/files`, { waitUntil: "domcontentloaded" });
  await dismissUpdate(page);
  const before = await page.inputValue(`${form} [name="file_poll_seconds"]`);
  await page.locator(`${form} [name="autosave"]`).setChecked(!!autosave);
  await page.fill(`${form} [name="file_poll_seconds"]`, poll === undefined ? before : String(poll));
  await Promise.all([
    page.waitForResponse((r) => r.url().includes("/settings/editor/files") && r.request().method() === "POST", { timeout: 15000 }),
    page.waitForEvent("requestfinished", { predicate: (r) => r.url().includes("/settings/editor/files") && r.method() === "GET", timeout: 15000 }),
    page.click(`${form} button[type="submit"]`),
  ]);
  await page.waitForLoadState("domcontentloaded");
  assert((await page.isChecked(`${form} [name="autosave"]`)) === !!autosave, `autosave came back as ${!autosave}`);
  return before;
}

async function createProject(page, name) {
  await page.goto(`${BASE}/projects/new`, { waitUntil: "domcontentloaded" });
  await dismissUpdate(page);
  await page.fill('input[name="project_name"]', name);
  await Promise.all([page.waitForURL(/\/projects/, { timeout: 15000 }), submitBtn(page, 'input[name="project_name"]').click()]);
  await page.waitForSelector(`[data-project-name="${name}"]`, { timeout: 8000 });
}

// projectPath is the absolute directory of a project, read from the new coder
// form, whose select carries the paths the server accepts. A runner must not
// assume where the projects root is: an instance started the way the README
// describes serves ~/projects, one started from the crontab line serves another
// root, and a hard coded path makes a runner pass on one and fail on the other.
async function projectPath(page, name) {
  await page.goto(`${BASE}/coders/new`, { waitUntil: "domcontentloaded" });
  await dismissUpdate(page);
  const path = await page.evaluate((project) => {
    const options = [...document.querySelectorAll('select[name="project"] option')];
    const match = options.find((option) => (option.value || "").split("/").pop() === project);
    return match ? match.value : "";
  }, name);
  assert(path, `the project ${name} is not offered by this instance`);
  return path;
}

async function deleteProject(page, name) {
  await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
  await dismissUpdate(page);
  const card = await page.$(`[data-project-name="${name}"]`);
  if (!card) return;
  // A worktree project folded under its main repository is hidden until the
  // main's count is clicked; the delete button is not clickable before that.
  await page.evaluate((p) => {
    const row = document.querySelector(`[data-project-name="${p}"]`);
    const main = row && row.dataset.projectWorktreeOf;
    const toggle = main && document.querySelector(`#project-${main} [data-worktrees-toggle][aria-expanded="false"]`);
    if (toggle) toggle.click();
  }, name);
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
  const delPath = new URL(shellUrl).pathname + "/delete";
  await closeTerminal(page, shellUrl, `form[action="${delPath}"] button[type="submit"], form[action="${delPath}"] button`, delPath);
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

// closeFromStrip closes a terminal through the tab strip's close control, the
// desktop way: the attach page header carries stop and delete on touch only.
// It asks the same confirm as the header button did.
// The strip repaints itself right after a page lands (its own fragment pull,
// then every terminals event), so the close control is clicked through a
// locator that re-resolves it, a handle taken before a repaint is detached.
async function closeFromStrip(page, id) {
  const sel = `terminal-tabs .terminal-tab[data-tab-id="${id}"] [data-tab-close]`;
  if (!(await page.$(sel))) return false;
  await page.locator(sel).first().click();
  await confirmSwal(page).catch(() => {});
  await sleep(600);
  return true;
}

// closeTerminal takes whichever way the page offers: a header button where
// one stands, else the tab strip's close control. A terminal inside a split
// has no row of its own in the strip, so that one goes straight to the route
// the controls post to.
async function closeTerminal(page, url, headerSelector, action) {
  await page.goto(url, { waitUntil: "domcontentloaded" });
  const btn = await page.$(headerSelector);
  if (btn && await btn.isVisible()) {
    await btn.click();
    await confirmSwal(page).catch(() => {});
    await sleep(500);
    return;
  }
  if (await closeFromStrip(page, new URL(url).pathname.split("/").pop())) return;
  await page.evaluate(async (path) => {
    const token = document.querySelector('meta[name="csrf-token"]')?.content || "";
    await fetch(path, { method: "POST", headers: { "X-CSRF-Token": token, Accept: "application/json" } });
  }, action || new URL(url).pathname + "/delete");
  await sleep(600);
}

async function stopSession(page, sessionUrl) {
  await closeTerminal(page, sessionUrl, 'form[action$="/stop"] button[type="submit"], form[action$="/stop"] button', new URL(sessionUrl).pathname + "/stop");
}

// saveTagSettings saves Settings › Projects › Tags the way another page
// would: it loads the page's fields, lets change edit them as [name, value]
// pairs, and posts them. A save the page refuses answers 200 and fails here.
async function saveTagSettings(page, change) {
  const html = await (await page.request.get(`${BASE}/settings/projects/tags`)).text();
  const token = html.match(/name="csrf-token" content="([^"]+)"/)[1];
  const text = (raw) => raw.replace(/&#34;/g, '"').replace(/&#39;/g, "'").replace(/&lt;/g, "<").replace(/&gt;/g, ">").replace(/&amp;/g, "&");
  const fields = [["csrf_token", token]];
  for (const m of html.replace(/<template[\s\S]*?<\/template>/g, "").matchAll(/<input type="(?:hidden|text)"(?: class="[^"]*")? name="([^"]*)" value="([^"]*)"/g)) {
    if (m[1] !== "csrf_token") fields.push([text(m[1]), text(m[2])]);
  }
  change(fields);
  const res = await page.request.post(`${BASE}/settings/projects/tags`, {
    data: new URLSearchParams(fields).toString(),
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    maxRedirects: 0,
  });
  assert(res.status() === 303, `the tags settings answered ${res.status()}`);
}

// setField sets the value of a form field among [name, value] pairs.
function setField(fields, name, value) {
  const field = fields.find((f) => f[0] === name);
  assert(field, `no field ${name}`);
  field[1] = value;
}

// reach reports whether a thumb at each [dx, dy] off the middle of an
// element still lands on it. It brings the element to the middle of its
// scroller first: a point outside the viewport hits nothing, and other
// projects on the board push a runner's row below the fold.
function reach(page, selector, offsets) {
  return page.$eval(selector, async (el, offs) => {
    el.scrollIntoView({ block: "center", inline: "center", behavior: "instant" });
    await new Promise((done) => requestAnimationFrame(() => requestAnimationFrame(done)));
    const box = el.getBoundingClientRect();
    const x = box.left + box.width / 2;
    const y = box.top + box.height / 2;
    return offs.every(([dx, dy]) => {
      const hit = document.elementFromPoint(x + dx, y + dy);
      return hit === el || el.contains(hit);
    });
  }, offsets);
}

// editorReady waits until the editor has wired its controls, which it does
// when its init resolves and leaves its teardown on the element.
function editorReady(page) {
  return page.waitForFunction(() => typeof document.querySelector("dc-editor")?.teardown === "function", null, { timeout: 15000 });
}

// editorCursor is the path of the editor's active tab and the line and column
// its cursor stands on, null while no editor shows.
function editorCursor(page) {
  return page.evaluate(async () => {
    const { EditorView } = await import("@codemirror/view");
    const dom = [...document.querySelectorAll(".cm-editor")].find((el) => el.getClientRects().length && getComputedStyle(el).visibility !== "hidden");
    const view = dom && EditorView.findFromDOM(dom);
    if (!view) return null;
    const head = view.state.selection.main.head;
    const line = view.state.doc.lineAt(head);
    return { path: document.querySelector(".editor-tab.active")?.dataset.path || "", line: line.number, col: head - line.from + 1 };
  });
}

// waitEditorCursor waits until the editor shows want.path, at want.line and
// want.col when want names a line.
async function waitEditorCursor(page, want) {
  let got = null;
  for (let i = 0; i < 60; i += 1) {
    got = await editorCursor(page).catch(() => null);
    if (got && got.path === want.path && (!want.line || (got.line === want.line && got.col === want.col))) return got;
    await sleep(250);
  }
  throw new Error(`the editor stands at ${JSON.stringify(got)}, want ${JSON.stringify(want)}`);
}

async function skewedClocks(context, bag, minutes = 10) {
  const clocks = [];
  for (const [name, skew] of [["behind", -minutes * 60 * 1000], ["ahead", minutes * 60 * 1000]]) {
    const page = await context.newPage();
    wirePage(page, bag);
    await page.addInitScript((ms) => {
      const RealDate = Date;
      const realNow = () => RealDate.now();
      function SkewedDate(...args) {
        if (!new.target) return new RealDate(realNow() + ms).toString();
        return args.length ? new RealDate(...args) : new RealDate(realNow() + ms);
      }
      SkewedDate.prototype = RealDate.prototype;
      SkewedDate.now = () => realNow() + ms;
      SkewedDate.parse = RealDate.parse;
      SkewedDate.UTC = RealDate.UTC;
      Object.defineProperty(window, "__dcRealNow", { value: realNow });
      window.Date = SkewedDate;
    }, skew);
    clocks.push([name, page]);
  }
  return clocks;
}

function markPhases(page, ...selectors) {
  return page.evaluate(async (sels) => {
    const realNow = window.__dcRealNow || (() => Date.now());
    const icons = sels.map((sel) => document.querySelector(sel));
    const spot = (icon) => getComputedStyle(icon, "::before").offsetDistance;
    const now = realNow();
    const marks = icons.map((icon) => {
      if (!icon) return null;
      const run = icon.getAnimations({ subtree: true }).find((a) => a.effect?.getTiming().iterations === Infinity);
      const since = Number(icon.dataset.workingSince) || 0;
      const phase = run ? run.currentTime : null;
      return { since, phase, drift: since && run ? Math.round(phase - (now - since)) : null, spot: spot(icon), working: icon.classList.contains("working") };
    });
    await new Promise((resolve) => setTimeout(resolve, 450));
    return marks.map((mark, i) => mark && { ...mark, moved: spot(icons[i]) !== mark.spot });
  }, selectors);
}

function markOff(mark) {
  return !mark || !mark.working || !(mark.since > 0) ? "carries no running mark with its start"
    : !mark.moved ? "stands still"
    : !(Math.abs(mark.drift) < 250) ? `stands ${mark.drift}ms off the start of its turn`
    : "";
}

async function clockMarks(clocks, when, selectors, problems) {
  const seen = {};
  for (const [name, page] of clocks) {
    const read = await markPhases(page, ...selectors);
    read.forEach((mark, i) => {
      const off = markOff(mark);
      if (off) problems.push(`${when}, clock ${name}: mark ${i + 1} ${off}`);
    });
    const [a, b] = read;
    if (read.length === 2 && a?.phase != null && b?.phase != null) {
      const gap = Math.round((a.phase - b.phase) - (b.since - a.since));
      if (!(Math.abs(gap) < 250)) problems.push(`${when}, clock ${name}: two marks started ${b.since - a.since}ms apart run ${Math.round(a.phase - b.phase)}ms apart`);
    }
    seen[name] = read;
  }
  return seen;
}

// removeTags removes, through Settings › Projects › Tags, every tag that
// match(name, id) picks by the name and id the page loaded it with. Tags
// outlive their projects, so deleting its projects leaves a runner's tags.
async function removeTags(page, match) {
  await saveTagSettings(page, (fields) => {
    for (const [name, value] of fields.filter(([n]) => n.startsWith("was:"))) {
      const id = name.slice("was:".length);
      if (match(value, id)) setField(fields, `remove:${id}`, "1");
    }
  });
}

function makeRunner() {
  const results = [];
  async function run(name, fn, { soft = false } = {}) {
    if (!matchesCheck(name)) return;
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

// wordPoint is the center of a word on the editor surface, measured through
// a Range over its text node: a .cm-line is full width, so a click on the
// element lands right of the text, and highlighting splits identifiers into
// spans of their own.
function wordPoint(page, lineNeedle, word) {
  return page.evaluate(({ lineNeedle, word }) => {
    const el = [...document.querySelectorAll(".cm-line")].find((l) => l.textContent.includes(lineNeedle));
    if (!el) return null;
    const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
    let node;
    while ((node = walker.nextNode())) {
      const i = node.textContent.indexOf(word);
      if (i >= 0) {
        const r = document.createRange();
        r.setStart(node, i);
        r.setEnd(node, i + word.length);
        const rect = r.getBoundingClientRect();
        return { x: rect.x + rect.width / 2, y: rect.y + rect.height / 2 };
      }
    }
    return null;
  }, { lineNeedle, word });
}

// splitLabel reads a split menu label off the screen rather than out of its
// text: the width the head's trailing space takes there, 0 when it has none
// or the browser dropped it, and the room between the head's box and the
// tail's.
function splitLabel(row) {
  return row.evaluate((el) => {
    const head = el.querySelector(".dc-menu-label-head");
    const tail = el.querySelector(".dc-menu-label-tail");
    if (!head || !tail) return null;
    const text = head.firstChild;
    let space = 0;
    if (text && text.textContent.endsWith(" ")) {
      const r = document.createRange();
      r.setStart(text, text.length - 1);
      r.setEnd(text, text.length);
      space = r.getBoundingClientRect().width;
    }
    return { head: head.textContent, tail: tail.textContent, space, gap: tail.getBoundingClientRect().left - head.getBoundingClientRect().right };
  });
}

// recordCopies is an init script that logs every copy the page makes, through
// the clipboard API or the execCommand fallback, in window.__copies. A page
// without a secure origin cannot read the clipboard back, and execCommand
// only copies inside a user gesture, so an ok entry of the fallback is the
// proof the copy ran in the click.
function recordCopies() {
  window.__copies = [];
  const clip = navigator.clipboard;
  if (clip && clip.writeText) {
    const write = clip.writeText.bind(clip);
    clip.writeText = (text) => write(text).then(
      () => { window.__copies.push({ via: "clipboard", text, ok: true }); },
      (err) => { window.__copies.push({ via: "clipboard", text, ok: false }); throw err; },
    );
  }
  const exec = document.execCommand.bind(document);
  document.execCommand = (command, ...rest) => {
    const field = document.activeElement;
    const text = field && typeof field.value === "string" ? field.value.slice(field.selectionStart, field.selectionEnd) : String(getSelection());
    const ok = exec(command, ...rest);
    if (command === "copy") window.__copies.push({ via: "execCommand", text, ok });
    return ok;
  };
}

function assert(c, m) { if (!c) throw new Error(m); }

// claude's first setup and its login menu mark their first entry with the
// same ❯ its prompt starts with, so a wait for the prompt reads them as
// ready. An instance whose HOME holds no claude login shows them, see the
// README.
function assertNoLogin(text) {
  assert(!/Choose the text style|Select login method/.test(text), `claude asks for a login, the instance's HOME holds none: ${JSON.stringify(text.slice(-200))}`);
}

// CHECK narrows a runner to the checks whose name contains one of a comma
// separated list of substrings (case insensitive), matched against the
// check's own name as passed to run(), before an engine prefix joins it in
// the report. Several terms are what a check that reads state an earlier one
// set up needs, that earlier one named alongside it. Unset runs everything,
// same as before it existed.
function matchesCheck(name) {
  const want = process.env.CHECK;
  if (!want) return true;
  const n = name.toLowerCase();
  return want.split(",").some((term) => n.includes(term.trim().toLowerCase()));
}

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
      if (!matchesCheck(name)) return;
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
  login, createProject, projectPath, deleteProject, dismissUpdate, setEditorFiles, createShell, deleteShell, createSession, stopSession, closeFromStrip,
  saveTagSettings, setField, removeTags, reach, editorReady, waitEditorCursor, wordPoint, splitLabel, skewedClocks, markPhases, markOff, clockMarks,
  makeRunner, report, assert, assertNoLogin, recordCopies,
  ENGINES, engineList, launch, newDesktop, newMobile, runFeature,
};
