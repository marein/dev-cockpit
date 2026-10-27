const { execFileSync } = require("child_process");
const fs = require("fs");
const path = require("path");
const L = require("./lib");
const fileMentionLinks = require("./file-mention-links");
const { assert, BASE, sleep } = L;

// The @ in an assistant's message box (dc-file-mention, file_mention.gohtml):
// typed at the start of a word it opens a popup over the box that lists the
// projects, worktrees included (GET /mention/projects, the editor palette's own
// rows as options, ranked by @dc/filter through @dc/project-palette, pulled
// again on every @ so a project made behind the page's back shows), and a
// picked project's folders and files (GET /mention/files?project=&q=, the quick
// open index with its ranking and exclusions, a folder ranked among the files
// and shown with its trailing slash, every hit placed under the project again
// and named by its absolute path, at most 50). The typed part walks into the
// deepest folder it names: with nothing after its slash the popup lists what
// stands in it, folders first, the folder itself on top, and what is typed
// after the slash is matched below it. Picking a folder walks into it. Arrows
// move, Enter and Tab pick, Escape closes, Backspace right after the slash of
// `@<project>/` or of a folder, or the back arrow, goes up one level with the
// filter that was typed before the pick, or with the slash taken off when
// nothing was picked, so a folder jumped to by a search hit goes up one folder
// at a time and the search comes back where it was typed. A file lands as its absolute path plus a blank, a folder
// with a trailing slash, quoted when it holds a blank. The box's own keys
// (Enter sends on a desktop) never fire while the popup takes them.
//
// The absolute paths of the projects' files link in the chat, the user's
// words and the answers alike (file-mention-links.js).
//
// The instance needs tests/e2e/fakes first on PATH and a scratch HOME like
// assistant.js (the run makes an assistant and deletes it, one turn runs on
// the fake claude), and its --projects-dir and --state-dir inside a scratch
// directory mounted into the runner at its own host path (AUX_DIR, default
// /tmp/dcfm): the runner builds its projects there with the container's git,
// a repository with a linked worktree, a plain folder and a tree of 24,000
// files, and deletes them at the end. SHOTS_DIR, when set and mounted,
// receives the phone screenshots of the projects, a project's folders and
// files and the inside of a folder, in both themes.
//
// Gotchas:
// - the composer is JS owned, wait for dc-assistant[ready] and the element,
// - a popup that is hidden still has its box in the DOM, checks read the
//   computed display and the rectangle,
// - the phone has no keyboard of its own here, a short viewport stands in for
//   the on-screen keyboard taking half the screen.

const AUX = process.env.AUX_DIR || "/tmp/dcfm";
const PROJECTS = path.join(AUX, "projects");
const SHOTS = process.env.SHOTS_DIR || "";
const S = Date.now().toString(36).slice(-3);
const SHOP = `shop-${S}`;
const CHECKOUT = `${SHOP}-checkout`;
const MONO = `mono-${S}`;
const NOTES = `notes-${S}`;
const LATE = `late-${S}`;
const BUILT = [SHOP, CHECKOUT, MONO, NOTES];
const OURS = [...BUILT, LATE];
const BIG_FILES = 24000;
const NEEDLE = "zz/deep/needle/TargetWidget.ts";
const BOX = "dc-assistant [data-assistant-input]";
const READY = "dc-assistant[ready]";

const SHOP_FILES = [
  "README.md", "package.json", "Dockerfile", "docker-compose.yml",
  "src/components/CartItem.tsx", "src/components/CartSummary.tsx", "src/components/CheckoutForm.tsx",
  "src/components/ProductCard.tsx", "src/components/UserMenu.tsx", "src/pages/cart.tsx", "src/pages/checkout.tsx",
  "src/api/orders.go", "src/api/payments.go", "src/api/cart_test.go", "src/styles/cart.css",
  "docs/architecture.md", "docs/user guide.md", "design notes/palette.md",
];

function writeFiles(root, files) {
  for (const rel of files) {
    const full = path.join(root, rel);
    fs.mkdirSync(path.dirname(full), { recursive: true });
    fs.writeFileSync(full, `${rel}\n`);
  }
}

function git(cwd, args) {
  return execFileSync("git", ["-c", "user.name=e2e", "-c", "user.email=e2e@example.com", "-c", "commit.gpgsign=false", ...args], { cwd, encoding: "utf8" });
}

function buildFixture() {
  for (const name of OURS) assert(!fs.existsSync(path.join(PROJECTS, name)), `${name} is already there`);
  const shop = path.join(PROJECTS, SHOP);
  writeFiles(shop, SHOP_FILES);
  git(shop, ["init", "-q", "-b", "main"]);
  git(shop, ["add", "-A"]);
  git(shop, ["commit", "-qm", "init"]);
  git(shop, ["worktree", "add", "-q", "-b", "checkout", path.join(PROJECTS, CHECKOUT)]);
  writeFiles(path.join(PROJECTS, NOTES), ["todo.md", "ideas/cart-redesign.md", "meetings/2026-10.md"]);
  const mono = path.join(PROJECTS, MONO);
  const big = [];
  for (let m = 0; big.length < BIG_FILES - 1; m += 1) {
    for (let s = 0; s < 20 && big.length < BIG_FILES - 1; s += 1) {
      for (let f = 0; f < 10 && big.length < BIG_FILES - 1; f += 1) big.push(`pkg/m${m}/sub${s}/file${f}.txt`);
    }
  }
  big.push(NEEDLE);
  for (const rel of big) {
    const full = path.join(mono, rel);
    fs.mkdirSync(path.dirname(full), { recursive: true });
    fs.writeFileSync(full, "");
  }
}

function removeFixture() {
  for (const name of OURS) fs.rmSync(path.join(PROJECTS, name), { recursive: true, force: true });
}

async function csrf(page) {
  return page.evaluate(() => document.querySelector('meta[name="csrf-token"]').content);
}

async function newAssistant(page) {
  await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
  await L.dismissUpdate(page);
  const made = await page.request.post(`${BASE}/assistants/new`, { form: { csrf_token: await csrf(page), form: "new", coder: "claude" }, headers: { Accept: "application/json" } });
  const id = (await made.json().catch(() => ({}))).id || "";
  assert(id, `no assistant was made: ${made.status()}`);
  return id;
}

async function deleteAssistant(page, id) {
  await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
  await page.request.post(`${BASE}/assistants/${id}`, { form: { csrf_token: await csrf(page), form: "delete" }, headers: { Accept: "application/json" } });
}

async function openAssistant(page, id) {
  await page.goto(`${BASE}/assistants/${id}`, { waitUntil: "domcontentloaded" });
  await L.dismissUpdate(page);
  await page.waitForSelector(READY, { timeout: 15000 });
  const missing = await L.waitUpgraded(page, ["dc-file-mention"]);
  assert(!missing.length, "dc-file-mention never upgraded");
}

function state(page) {
  return page.evaluate(() => {
    const panel = document.querySelector("[data-mention-panel]");
    const input = document.querySelector("dc-assistant [data-assistant-input]");
    const rect = panel.getBoundingClientRect();
    const rows = [...panel.querySelectorAll('[data-mention-list] [role="option"]')];
    return {
      shown: getComputedStyle(panel).display !== "none" && rect.height > 0,
      title: panel.querySelector("[data-mention-title]").textContent,
      trail: (() => { const el = panel.querySelector("[data-mention-trail]"); return getComputedStyle(el).display !== "none" ? el.textContent : ""; })(),
      back: getComputedStyle(panel.querySelector("[data-mention-back]")).display !== "none",
      count: panel.querySelector("[data-mention-count]").textContent,
      rows: rows.map((row) => row.dataset.projectName ? `@${row.dataset.projectName}` : row.title),
      refs: rows.map((row) => row.dataset.mentionRef || ""),
      walks: rows.map((row) => row.dataset.mentionWalk || ""),
      active: rows.findIndex((row) => row.classList.contains("active")),
      described: input.getAttribute("aria-activedescendant") || "",
      activeID: rows.find((row) => row.classList.contains("active"))?.id || "",
      note: panel.querySelector("[data-mention-note]")?.textContent || "",
      busy: panel.querySelector("[data-mention-list]").getAttribute("aria-busy") === "true",
      marks: [...panel.querySelectorAll("[data-mention-list] mark")].map((mark) => mark.textContent),
      value: input.value,
      caret: input.selectionStart,
      focused: document.activeElement === input,
    };
  });
}

async function waitState(page, test, what, timeout = 8000) {
  const deadline = Date.now() + timeout;
  let last = null;
  while (Date.now() < deadline) {
    last = await state(page);
    if (test(last)) return last;
    await sleep(40);
  }
  throw new Error(`${what}: ${JSON.stringify(last)}`);
}

async function reset(page) {
  await page.focus(BOX);
  await page.keyboard.press("Escape");
  await page.fill(BOX, "");
  await waitState(page, (s) => !s.shown && s.value === "", "the box did not empty");
}

async function messages(page) {
  return page.locator("[data-assistant-message]").count();
}

async function editorFiles(page, project, query) {
  const response = await page.request.get(`${BASE}/projects/${encodeURIComponent(project)}/editor/files?q=${encodeURIComponent(query)}`);
  return (await response.json()).files || [];
}

async function mentionFiles(page, project, query) {
  const response = await page.request.get(`${BASE}/mention/files?project=${encodeURIComponent(project)}&q=${encodeURIComponent(query)}`);
  return { status: response.status(), body: await response.json().catch(() => ({})) };
}

async function filterOrder(page, query) {
  return page.evaluate(async (q) => {
    const map = JSON.parse(document.querySelector('script[type="importmap"]').textContent).imports;
    const { rankTokens } = await import(map["@dc/filter"]);
    const rows = [...document.querySelectorAll("[data-mention-projects] [data-project-name]")];
    return rows
      .map((row) => ({ name: row.dataset.projectName, rank: rankTokens([row.dataset.projectName, row.querySelector(".editor-palette-repo").textContent], row.dataset.projectSearch, q) }))
      .filter((hit) => hit.rank >= 0)
      .sort((a, b) => a.rank - b.rank)
      .map((hit) => `@${hit.name}`);
  }, query);
}

async function geometry(page) {
  await page.waitForFunction(() => document.querySelector("[data-mention-panel]").getAnimations({ subtree: true }).every((a) => a.playState !== "running"), null, { timeout: 5000 });
  return page.evaluate(() => {
    const box = (el) => {
      const r = el.getBoundingClientRect();
      return { top: r.top, bottom: r.bottom, left: r.left, right: r.right, height: r.height };
    };
    const panel = document.querySelector("[data-mention-panel]");
    const rows = [...panel.querySelectorAll('[data-mention-list] [role="option"]')];
    return {
      panel: box(panel),
      input: box(document.querySelector("dc-assistant [data-assistant-input]")),
      bound: box(document.querySelector(".dc-assistant-main")),
      view: { width: window.innerWidth, height: window.innerHeight, top: window.visualViewport ? window.visualViewport.offsetTop : 0 },
      overflow: document.scrollingElement.scrollWidth - window.innerWidth,
      rowHeights: rows.map((row) => row.getBoundingClientRect().height),
      foot: getComputedStyle(panel.querySelector(".dc-mention-foot")).display,
      list: (() => { const list = panel.querySelector("[data-mention-list]"); return { scroll: list.scrollHeight, client: list.clientHeight }; })(),
    };
  });
}

async function shoot(browser, id, theme) {
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true, hasTouch: true, isMobile: true, viewport: { width: 390, height: 844 }, deviceScaleFactor: 2, colorScheme: theme });
  await ctx.addInitScript((t) => { try { window.localStorage.setItem("dc-theme", t); } catch {} }, theme);
  const page = await ctx.newPage();
  try {
    await L.login(page);
    await openAssistant(page, id);
    await page.fill(BOX, "");
    await page.tap(BOX);
    await page.keyboard.type("Fix the rounding in @");
    await waitState(page, (s) => s.shown && s.rows.includes(`@${SHOP}`), "the projects never came for the picture");
    await sleep(400);
    await page.screenshot({ path: path.join(SHOTS, `mention-projects-${theme}.png`) });
    await page.keyboard.type(S);
    await page.tap(`[data-mention-list] [role="option"][data-project-name="${SHOP}"]`);
    await waitState(page, (s) => s.title === SHOP && !s.busy && s.rows.includes("src/"), "the folders and files never came for the picture");
    await sleep(400);
    await page.screenshot({ path: path.join(SHOTS, `mention-files-${theme}.png`) });
    await page.tap('[data-mention-list] [role="option"][data-mention-walk="src"]');
    await waitState(page, (s) => s.title === "src/" && !s.busy && s.rows.includes("src/components/"), "src was not walked into for the picture");
    await page.tap('[data-mention-list] [role="option"][data-mention-walk="src/components"]');
    await waitState(page, (s) => s.title === "components/" && !s.busy && s.rows.includes("src/components/CartItem.tsx"), "components was not walked into for the picture");
    await sleep(400);
    await page.screenshot({ path: path.join(SHOTS, `mention-walk-${theme}.png`) });
  } finally {
    await ctx.close();
  }
}

buildFixture();
process.on("exit", removeFixture);

L.runFeature("FILE MENTION", async ({ engine, browser, page, run, mobilePage }) => {
  const id = await newAssistant(page);
  const shopDir = path.join(PROJECTS, SHOP);
  try {
    await openAssistant(page, id);

    await run("@ opens only at the start of a word: not in an email, not inside a word, after a blank or a bracket", async () => {
      await reset(page);
      await page.keyboard.type("write to me@home and foo@");
      let s = await state(page);
      assert(!s.shown, `an @ inside a word opened the popup: ${JSON.stringify(s)}`);
      await page.keyboard.type(" @");
      s = await waitState(page, (x) => x.shown && x.rows.length > 0, "an @ after a blank did not open");
      assert(s.title === "Projects" && !s.back, `the popup did not start on the projects: ${JSON.stringify(s)}`);
      await page.keyboard.press("Escape");
      s = await waitState(page, (x) => !x.shown, "Escape did not close");
      assert(s.value === "write to me@home and foo@ @" && s.focused, `Escape changed the text or took the focus: ${JSON.stringify(s)}`);
      await reset(page);
      await page.keyboard.type("(@");
      await waitState(page, (x) => x.shown, "an @ after a bracket did not open");
      await reset(page);
      await page.keyboard.type("@");
      await waitState(page, (x) => x.shown, "an @ at the very start did not open");
      return "email, mid word and Escape stay shut";
    });

    await run("a tree of 24,000 files is searched whole and fast, the deepest file found by a fragment", async () => {
      const started = Date.now();
      const first = await mentionFiles(page, MONO, "targetwid");
      const firstMs = Date.now() - started;
      assert(first.status === 200 && first.body.under === BIG_FILES && first.body.files[0].path === NEEDLE, `the first query answered ${JSON.stringify(first.body).slice(0, 300)}`);
      const warm = await page.evaluate(async (url) => {
        const out = [];
        for (let i = 0; i < 10; i += 1) {
          const t = performance.now();
          await (await fetch(`${url}&q=${encodeURIComponent(["file3", "sub1 file", "m7 sub", "needle", "txt"][i % 5])}`)).json();
          out.push(Math.round(performance.now() - t));
        }
        return out;
      }, `${BASE}/mention/files?project=${MONO}`);
      assert(Math.max(...warm) < 250, `a warm query took ${warm}ms`);
      const walked = await page.evaluate(async (url) => {
        const out = [];
        for (const q of ["pkg/", "pkg/m7/", "pkg/m7/sub1", "pkg/m7/sub1/", "zz/deep/", "pkg/m11/file3"]) {
          const t = performance.now();
          const body = await (await fetch(`${url}&q=${encodeURIComponent(q)}`)).json();
          out.push({ q, ms: Math.round(performance.now() - t), folder: body.folder, files: body.files.length, under: body.under, outside: body.files.filter((f) => body.folder && !f.path.startsWith(`${body.folder}/`)).length });
        }
        return out;
      }, `${BASE}/mention/files?project=${MONO}`);
      assert(walked.every((w) => w.ms < 250 && w.outside === 0), `a walk was slow or left its folder: ${JSON.stringify(walked)}`);
      assert(walked[0].folder === "pkg" && walked[0].files === 50 && walked[0].under === BIG_FILES - 1 && walked[1].folder === "pkg/m7" && walked[1].files === 20 && walked[2].folder === "pkg/m7" && walked[3].folder === "pkg/m7/sub1" && walked[3].files === 10 && walked[5].files === 20, `the walks answered ${JSON.stringify(walked)}`);
      await reset(page);
      await page.keyboard.type(`@${MONO}/`);
      await waitState(page, (x) => x.title === MONO && !x.busy, "the big project never answered");
      const typed = Date.now();
      await page.keyboard.type("targetwid");
      const s = await waitState(page, (x) => x.rows[0] === NEEDLE && !x.busy, "the needle never came", 3000);
      const uiMs = Date.now() - typed;
      assert(s.count === "1 match", `the count reads ${s.count}`);
      await page.keyboard.press("Escape");
      await page.keyboard.type(` @${MONO}/pkg/m7/`);
      const walkTyped = Date.now();
      const w = await waitState(page, (x) => x.title === "m7/" && !x.busy && x.rows.length === 21, "the walk into m7 never came", 3000);
      const walkMs = Date.now() - walkTyped;
      assert(w.trail === `${MONO}/pkg/` && w.count === "200 files" && w.rows[1] === "pkg/m7/sub0/", `the walk into m7 reads ${JSON.stringify(w)}`);
      return `first query ${firstMs}ms, warm max ${Math.max(...warm)}ms, walks max ${Math.max(...walked.map((x) => x.ms))}ms, typed to row ${uiMs}ms, walk to rows ${walkMs}ms`;
    });

    await run("the project step lists every project, the worktree included, filtered live by @dc/filter's token match", async () => {
      await reset(page);
      await page.keyboard.type("@");
      let s = await waitState(page, (x) => x.shown && x.rows.includes(`@${SHOP}`), "the projects never came");
      const fragment = await (await page.request.get(`${BASE}/mention/projects`)).text();
      const all = [...fragment.matchAll(/data-project-name="([^"]+)"/g)].map((m) => `@${m[1]}`);
      for (const name of BUILT) assert(s.rows.includes(`@${name}`), `${name} is not listed: ${s.rows}`);
      assert(new Set(s.rows).size === all.length && all.every((name) => s.rows.includes(name)), `the list is not every project: ${s.rows} vs ${all}`);
      assert(s.count === `${all.length} project${all.length === 1 ? "" : "s"}`, `the head counts ${s.count}`);
      assert(s.active === 0 && s.described === s.activeID && s.activeID, `the first row is not marked for the keys: ${JSON.stringify(s)}`);
      // The full list stands last, a recent shortlist above it lists the
      // worktree again as a row of its own once an earlier engine opened it.
      const worktree = await page.evaluate((name) => {
        const row = [...document.querySelectorAll(`[data-mention-list] [data-project-name="${name}"]`)].at(-1);
        return { icon: row.querySelector(".ti").className, repo: row.querySelector(".editor-palette-repo").textContent, sub: row.querySelector(".editor-palette-sub")?.textContent || "", member: row.classList.contains("editor-palette-member") };
      }, CHECKOUT);
      assert(worktree.icon.includes("ti-git-fork") && worktree.repo === SHOP && worktree.sub === CHECKOUT && worktree.member, `the worktree does not read as one: ${JSON.stringify(worktree)}`);
      for (const query of [S, `${S} check`, `CHECKOUT ${S}`, `${S} notes`]) {
        await reset(page);
        await page.keyboard.type(`@${query}`);
        const want = await filterOrder(page, query);
        s = await waitState(page, (x) => JSON.stringify(x.rows) === JSON.stringify(want), `"${query}" does not list what @dc/filter ranks`);
        assert(s.active === 0 && s.marks.length > 0, `"${query}" marks nothing: ${JSON.stringify(s)}`);
      }
      assert(JSON.stringify(s.rows) === JSON.stringify([`@${NOTES}`]), `two tokens did not narrow to one project: ${s.rows}`);
      await reset(page);
      await page.keyboard.type(`@zzz${S}`);
      s = await waitState(page, (x) => x.note.includes("No project matches"), "a filter nobody matches says nothing");
      await page.keyboard.type(" ");
      await waitState(page, (x) => !x.shown, "a blank after a filter nobody matches did not close");
      return `${all.length} projects, filtered like @dc/filter`;
    });

    await run("arrows move the mark with wrap, Enter takes a project without sending, the files open with the folder first", async () => {
      await reset(page);
      const before = await messages(page);
      await page.keyboard.type(`@${S}`);
      let s = await waitState(page, (x) => x.rows.length === 4, "the four projects never came");
      await page.keyboard.press("ArrowDown");
      s = await state(page);
      assert(s.active === 1 && s.described === s.activeID, `ArrowDown did not step: ${JSON.stringify(s)}`);
      await page.keyboard.press("ArrowUp");
      await page.keyboard.press("ArrowUp");
      s = await state(page);
      assert(s.active === s.rows.length - 1, `ArrowUp did not wrap: ${s.active}`);
      await page.keyboard.press("ArrowDown");
      s = await state(page);
      assert(s.active === 0 && s.value === `@${S}`, `the arrows moved the caret or did not wrap back: ${JSON.stringify(s)}`);
      const target = s.rows[0].slice(1);
      await page.keyboard.press("Enter");
      s = await waitState(page, (x) => x.title === target && !x.busy && x.rows.length > 0, "Enter did not open the files");
      assert(s.value === `@${target}/` && s.caret === s.value.length && s.back, `the project part is not in the box: ${JSON.stringify(s)}`);
      assert(s.refs[0] === `${path.join(PROJECTS, target)}/` && s.active === 0, `the folder is not the first row: ${JSON.stringify(s)}`);
      assert(/ files?$/.test(s.count), `the head does not count the files: ${s.count}`);
      assert(await messages(page) === before, "Enter sent the message");
      return `${target}: ${s.count}`;
    });

    await run("the file step ranks like the editor's quick open, with the same exclusions, and marks the hits", async () => {
      for (const query of ["cart", "comp cart", "api go", "ReadMe", "HEAD", "user", "comp", "hooks", "refs"]) {
        await reset(page);
        await page.keyboard.type(`@${SHOP}/${query}`);
        const want = (await editorFiles(page, SHOP, query)).slice(0, 50);
        const files = (x) => x.rows.filter((row) => !row.endsWith("/"));
        const s = await waitState(page, (x) => x.title === SHOP && !x.busy && (x.rows.length ? JSON.stringify(files(x)) === JSON.stringify(want) : !want.length && x.note.includes("No folder or file matches")), `"${query}" does not answer what the quick open answers (${want})`);
        assert(s.rows.every((row, i) => row.endsWith("/") ? s.walks[i] === row.slice(0, -1) && !s.refs[i] : s.refs[i] === path.join(shopDir, row) || s.refs[i] === JSON.stringify(path.join(shopDir, row))), `a ref is not the absolute path: ${s.rows} ${s.refs}`);
        assert(!s.rows.some((row) => row.startsWith(".git")), `.git leaked into "${query}": ${s.rows}`);
        if (s.rows.length) assert(s.marks.length > 0 && s.active === 0, `"${query}" marks no hit: ${JSON.stringify(s)}`);
      }
      return "cart, tokens, case, folders, exclusions";
    });

    await run("folders stand among the files, ranked by the same rule, with a folder icon, a trailing slash and a chevron", async () => {
      await reset(page);
      await page.keyboard.type(`@${SHOP}/comp`);
      let s = await waitState(page, (x) => x.title === SHOP && !x.busy && x.rows[0] === "src/components/", "the components folder is not the first hit");
      const api = await mentionFiles(page, SHOP, "comp");
      assert(JSON.stringify(s.rows) === JSON.stringify(api.body.files.map((f) => f.folder ? `${f.path}/` : f.path)), `the rows are not the answer's order: ${s.rows}`);
      assert(api.body.files[0].ref === `${path.join(shopDir, "src/components")}/` && api.body.files[0].folder === true, `the folder's ref is ${JSON.stringify(api.body.files[0])}`);
      const look = await page.evaluate(() => {
        const rows = [...document.querySelectorAll('[data-mention-list] [role="option"]')];
        const shown = (el) => getComputedStyle(el).display !== "none" && el.getBoundingClientRect().width > 0;
        return rows.map((row) => ({ icon: row.querySelector("[data-mention-icon]").className, name: row.querySelector("[data-mention-name]").textContent, chevron: shown(row.querySelector("[data-mention-walk]")) }));
      });
      assert(look[0].icon.includes("ti-folder") && look[0].name === "components/" && look[0].chevron, `the folder row does not read as one: ${JSON.stringify(look[0])}`);
      assert(look.slice(1).every((row) => !row.chevron && !row.icon.includes("ti-folder") && !row.name.endsWith("/")), `a file row reads as a folder: ${JSON.stringify(look)}`);
      assert(s.count === `${s.rows.length} matches`, `the head counts ${s.count}`);
      await reset(page);
      await page.keyboard.type(`@${SHOP}/`);
      s = await waitState(page, (x) => x.title === SHOP && !x.busy && x.rows.length === 8, "the project's own entries never came");
      assert(JSON.stringify(s.rows.slice(1)) === JSON.stringify(["design notes/", "docs/", "src/", "docker-compose.yml", "Dockerfile", "package.json", "README.md"]), `the project does not list folders first by name: ${s.rows}`);
      assert(s.refs[0] === `${shopDir}/` && s.count === `${SHOP_FILES.length} files` && s.trail === "", `the head or the folder row is off: ${JSON.stringify(s)}`);
      return `${look.length} rows for comp`;
    });

    await run("picking a folder walks into it: the head names it, its own entries show, filtering goes on inside it", async () => {
      await reset(page);
      const before = await messages(page);
      await page.keyboard.type(`@${SHOP}/`);
      await waitState(page, (x) => x.title === SHOP && !x.busy && x.rows[3] === "src/", "src never came");
      for (let i = 0; i < 3; i += 1) await page.keyboard.press("ArrowDown");
      await page.keyboard.press("Enter");
      let s = await waitState(page, (x) => x.title === "src/" && !x.busy, "Enter did not walk into src");
      assert(s.value === `@${SHOP}/src/` && s.caret === s.value.length && s.back && s.trail === `${SHOP}/`, `the walk did not complete the folder: ${JSON.stringify(s)}`);
      assert(JSON.stringify(s.rows) === JSON.stringify([s.rows[0], "src/api/", "src/components/", "src/pages/", "src/styles/"]) && s.refs[0] === `${path.join(shopDir, "src")}/` && s.active === 0, `src does not list its own folders under itself: ${JSON.stringify(s)}`);
      assert(s.count === "11 files" && await messages(page) === before, `the head counts ${s.count} or Enter sent`);
      await page.keyboard.type("cart");
      s = await waitState(page, (x) => x.title === "src/" && !x.busy && x.rows.includes("src/pages/cart.tsx"), "cart inside src never came");
      const below = await page.evaluate(() => Object.fromEntries([...document.querySelectorAll('[data-mention-list] [role="option"]')].map((row) => [row.title, row.querySelector("[data-mention-dir]").textContent])));
      assert(below["src/pages/cart.tsx"] === "pages" && below["src/components/CartItem.tsx"] === "components", `the rows do not name their place below src: ${JSON.stringify(below)}`);
      for (let i = 0; i < 4; i += 1) await page.keyboard.press("Backspace");
      await waitState(page, (x) => x.value === `@${SHOP}/src/` && !x.busy && x.rows.length === 5, "the filter inside src did not empty");
      await page.keyboard.type("comp");
      s = await waitState(page, (x) => x.rows[0] === "src/components/" && !x.busy, "the filter inside src found no components");
      await page.keyboard.press("Tab");
      s = await waitState(page, (x) => x.title === "components/" && !x.busy, "Tab did not walk into components");
      assert(s.value === `@${SHOP}/src/components/` && s.trail === `${SHOP}/src/`, `Tab did not complete the folder: ${JSON.stringify(s)}`);
      await page.keyboard.type("cart");
      s = await waitState(page, (x) => !x.busy && x.title === "components/" && x.rows.length === 2, "the filter did not stay inside components");
      assert(JSON.stringify(s.rows) === JSON.stringify(["src/components/CartItem.tsx", "src/components/CartSummary.tsx"]), `cart left the folder: ${s.rows}`);
      const subs = await page.evaluate(() => [...document.querySelectorAll('[data-mention-list] [role="option"] [data-mention-dir]')].map((el) => getComputedStyle(el).display === "none" ? "" : el.textContent));
      assert(subs.every((sub) => sub === ""), `a row inside the folder repeats its path: ${subs}`);
      await page.keyboard.press("Enter");
      s = await waitState(page, (x) => !x.shown, "Enter did not pick the file inside the folder");
      assert(s.value === `${path.join(shopDir, "src/components/CartItem.tsx")} ` && await messages(page) === before, `the box holds ${JSON.stringify(s)}`);
      return "src, components, cart";
    });

    await run("a folder lands as its absolute path with a trailing slash plus a blank, quoted when it holds a blank", async () => {
      await reset(page);
      await page.keyboard.type(`see @${SHOP}/src/components/`);
      let s = await waitState(page, (x) => x.title === "components/" && !x.busy, "the typed folder was not walked into");
      assert(s.rows[0] === `${path.join(shopDir, "src/components")}/` && s.active === 0, `the folder is not the first row: ${JSON.stringify(s)}`);
      await page.keyboard.press("Enter");
      s = await waitState(page, (x) => !x.shown, "Enter did not take the folder");
      const folder = `${path.join(shopDir, "src/components")}/`;
      assert(s.value === `see ${folder} ` && s.caret === s.value.length && s.focused, `the box holds ${JSON.stringify(s)}`);
      await page.keyboard.type("and @");
      await page.keyboard.type(`${SHOP}/design`);
      await waitState(page, (x) => x.rows[0] === "design notes/" && !x.busy, "the spaced folder never came");
      await page.keyboard.press("Enter");
      await waitState(page, (x) => x.title === "design notes/" && !x.busy, "Enter did not walk into the spaced folder");
      await page.keyboard.press("Tab");
      s = await waitState(page, (x) => !x.shown, "Tab did not take the spaced folder");
      const quoted = JSON.stringify(`${path.join(shopDir, "design notes")}/`);
      assert(s.value === `see ${folder} and ${quoted} `, `a folder with a blank is not quoted: ${s.value}`);
      await page.keyboard.type("or @");
      await page.keyboard.type(`${NOTES}/`);
      await waitState(page, (x) => x.title === NOTES && !x.busy, "the plain project never answered");
      await page.keyboard.press("Enter");
      s = await waitState(page, (x) => !x.shown, "Enter did not take the project's folder");
      assert(s.value === `see ${folder} and ${quoted} or ${path.join(PROJECTS, NOTES)}/ `, `the project's folder lands as ${s.value}`);
      return await waitDraft(page, id, s.value);
    });

    await run("Backspace after a folder's slash and the back arrow go up one level, with the filter typed before the pick or the slash taken off", async () => {
      await reset(page);
      await page.keyboard.type(`@${SHOP}/src`);
      await waitState(page, (x) => x.rows[0] === "src/" && !x.busy, "src never came first");
      await page.keyboard.press("Enter");
      await waitState(page, (x) => x.title === "src/" && !x.busy, "src was not walked into");
      await page.keyboard.type("comp");
      await waitState(page, (x) => x.rows[0] === "src/components/" && !x.busy, "components never came first");
      await page.keyboard.press("Enter");
      await waitState(page, (x) => x.title === "components/" && !x.busy, "components was not walked into");
      await page.keyboard.press("Backspace");
      let s = await waitState(page, (x) => x.title === "src/" && !x.busy && x.value === `@${SHOP}/src/comp`, "Backspace did not go up to src with the filter");
      assert(s.rows[s.active] === "src/components/", `the folder left is not marked: ${JSON.stringify(s)}`);
      await page.keyboard.press("Backspace");
      s = await state(page);
      assert(s.value === `@${SHOP}/src/com` && s.shown, `a second Backspace is not plain editing: ${JSON.stringify(s)}`);
      for (let i = 0; i < 3; i += 1) await page.keyboard.press("Backspace");
      s = await waitState(page, (x) => x.value === `@${SHOP}/src/` && x.title === "src/" && !x.busy, "the filter did not empty");
      await page.keyboard.press("Backspace");
      s = await waitState(page, (x) => x.title === SHOP && !x.busy && x.value === `@${SHOP}/src`, "Backspace did not go up to the project with its filter");
      assert(s.rows[s.active] === "src/" && s.trail === "", `src is not marked after going up: ${JSON.stringify(s)}`);
      await reset(page);
      await page.keyboard.type(`@${SHOP}/src/api/`);
      await waitState(page, (x) => x.title === "api/" && !x.busy, "the typed path was not walked into");
      await page.keyboard.press("Backspace");
      s = await waitState(page, (x) => x.title === "src/" && !x.busy && x.value === `@${SHOP}/src/api`, "Backspace after a typed folder did not take its slash off");
      assert(s.rows[s.active] === "src/api/", `the typed folder is not marked: ${JSON.stringify(s)}`);
      await page.keyboard.type("/");
      await waitState(page, (x) => x.title === "api/" && !x.busy, "the slash did not walk back in");
      await page.keyboard.type("pay");
      await waitState(page, (x) => x.rows[0] === "src/api/payments.go" && !x.busy, "payments never came");
      await page.click("[data-mention-back]");
      s = await waitState(page, (x) => x.title === "src/" && !x.busy, "the back arrow did not go up");
      assert(s.value === `@${SHOP}/src/api` && s.focused && s.rows[s.active] === "src/api/", `the back arrow lost the place, the focus or the mark: ${JSON.stringify(s)}`);
      const label = await page.getAttribute("[data-mention-back]", "aria-label");
      assert(label === `Back to ${SHOP}`, `the back arrow says ${label}`);
      return "picked, typed and the arrow";
    });

    await run("a folder jumped to by a search hit goes up one folder at a time, the search back once it stands where it was typed", async () => {
      await reset(page);
      await page.keyboard.type(`@${SHOP}/api`);
      let s = await waitState(page, (x) => x.title === SHOP && !x.busy && x.rows.includes("src/api/"), "src/api never came for api");
      for (let i = s.active; i < s.rows.indexOf("src/api/"); i += 1) await page.keyboard.press("ArrowDown");
      await page.keyboard.press("Enter");
      s = await waitState(page, (x) => x.title === "api/" && !x.busy, "the hit did not walk into src/api");
      assert(s.value === `@${SHOP}/src/api/` && s.trail === `${SHOP}/src/`, `the hit did not complete the folder: ${JSON.stringify(s)}`);
      const label = await page.getAttribute("[data-mention-back]", "aria-label");
      assert(label === "Back to src/", `the back arrow says ${label}`);
      await page.keyboard.press("Backspace");
      s = await waitState(page, (x) => x.title !== "api/" && !x.busy, "Backspace did not leave the hit");
      assert(s.title === "src/" && s.value === `@${SHOP}/src/api` && s.rows[s.active] === "src/api/", `Backspace did not go up one folder from the hit: ${JSON.stringify(s)}`);
      await page.keyboard.type("/");
      await waitState(page, (x) => x.title === "api/" && !x.busy, "the slash did not walk back in");
      await page.click("[data-mention-back]");
      s = await waitState(page, (x) => x.title !== "api/" && !x.busy, "the back arrow did not leave the hit");
      assert(s.title === "src/" && s.value === `@${SHOP}/src/api` && s.focused && s.rows[s.active] === "src/api/", `the back arrow did not go up one folder from the hit: ${JSON.stringify(s)}`);
      await page.click("[data-mention-back]");
      s = await waitState(page, (x) => x.title !== "src/" && !x.busy, "the back arrow did not leave src");
      assert(s.title === SHOP && s.value === `@${SHOP}/api` && s.trail === "" && s.rows.includes("src/api/"), `the search did not come back in the project: ${JSON.stringify(s)}`);
      await page.click("[data-mention-back]");
      s = await waitState(page, (x) => x.title === "Projects", "the back arrow did not go back to the projects");
      assert(s.value === `@${SHOP}` && s.rows[s.active] === `@${SHOP}`, `the projects lost the typed name or the mark: ${JSON.stringify(s)}`);
      return "hit, Backspace, arrow, search, projects";
    });

    await run("Tab picks a file: its absolute path lands with a blank, the caret after it, the draft carries it", async () => {
      await reset(page);
      await page.keyboard.type(`look at @${SHOP}/cartitem`);
      await waitState(page, (x) => x.rows[0] === "src/components/CartItem.tsx" && !x.busy, "CartItem never came first");
      await page.keyboard.press("Tab");
      const abs = path.join(shopDir, "src/components/CartItem.tsx");
      let s = await waitState(page, (x) => !x.shown, "Tab did not close the popup");
      assert(s.value === `look at ${abs} ` && s.caret === s.value.length && s.focused, `the box holds ${JSON.stringify(s)}`);
      assert(fs.existsSync(abs), `${abs} names no file`);
      await page.keyboard.type("and @");
      await waitState(page, (x) => x.shown, "a second mention did not open");
      await page.keyboard.type(`${SHOP}/user guide`);
      await waitState(page, (x) => x.rows[0] === "docs/user guide.md" && !x.busy, "the spaced file never came");
      await page.keyboard.press("Enter");
      s = await waitState(page, (x) => !x.shown, "Enter did not pick the file");
      const quoted = JSON.stringify(path.join(shopDir, "docs/user guide.md"));
      assert(s.value === `look at ${abs} and ${quoted} `, `a path with a blank is not quoted: ${s.value}`);
      const draft = await waitDraft(page, id, s.value);
      return draft;
    });

    await run("Backspace right after the project and the back arrow return to the projects with the typed filter and the project marked", async () => {
      await reset(page);
      await page.keyboard.type(`@${S} check`);
      await waitState(page, (x) => x.rows.length === 1 && x.rows[0] === `@${CHECKOUT}`, "the worktree was not the one hit");
      await page.keyboard.press("Enter");
      let s = await waitState(page, (x) => x.title === CHECKOUT && !x.busy, "the worktree's files never came");
      assert(s.refs[0] === `${path.join(PROJECTS, CHECKOUT)}/`, `the worktree's folder is ${s.refs[0]}`);
      await page.keyboard.press("Backspace");
      s = await waitState(page, (x) => x.title === "Projects", "Backspace did not go back");
      assert(s.value === `@${S} check` && s.rows[s.active] === `@${CHECKOUT}`, `the typed filter or the mark is lost: ${JSON.stringify(s)}`);
      await page.keyboard.press("Backspace");
      s = await state(page);
      assert(s.value === `@${S} chec` && s.title === "Projects" && s.shown, `a second Backspace is not plain editing: ${JSON.stringify(s)}`);
      await reset(page);
      await page.keyboard.type("@");
      await waitState(page, (x) => x.rows.includes(`@${NOTES}`), "the projects never came");
      await page.click(`[data-mention-list] [role="option"][data-project-name="${NOTES}"]`);
      s = await waitState(page, (x) => x.title === NOTES && !x.busy, "a click did not take the project");
      assert(s.focused && s.value === `@${NOTES}/`, `the click took the focus: ${JSON.stringify(s)}`);
      await page.keyboard.type("todo");
      await waitState(page, (x) => x.rows[0] === "todo.md" && !x.busy, "todo.md never came");
      await page.click("[data-mention-back]");
      s = await waitState(page, (x) => x.title === "Projects", "the back arrow did not go back");
      assert(s.value === "@" && s.focused && s.rows[s.active] === `@${NOTES}`, `the back arrow lost the filter, the mark or the focus: ${JSON.stringify(s)}`);
      return "Backspace and back arrow";
    });

    await run("the element tears down when removed and sets up once when put back", async () => {
      await reset(page);
      const detached = await page.evaluate(() => {
        const el = document.querySelector("dc-file-mention");
        const at = el.nextSibling;
        const parent = el.parentNode;
        el.remove();
        window.dcMentionParked = { el, at, parent };
        const box = document.querySelector("dc-assistant [data-assistant-input]");
        box.focus();
        document.execCommand("insertText", false, "@");
        return { panel: !!document.querySelector("[data-mention-panel]"), value: box.value };
      });
      assert(!detached.panel && detached.value === "@", `a removed element still acts: ${JSON.stringify(detached)}`);
      await page.evaluate(() => {
        const { el, at, parent } = window.dcMentionParked;
        parent.insertBefore(el, at);
        el.remove();
        parent.insertBefore(el, at);
        delete window.dcMentionParked;
      });
      await reset(page);
      await page.keyboard.type(`@${S} notes`);
      await waitState(page, (x) => x.rows.length === 1 && x.rows[0] === `@${NOTES}`, "the put back element does not filter");
      await page.keyboard.press("Enter");
      await waitState(page, (x) => x.title === NOTES && !x.busy && x.rows.length > 0, "the put back element did not take the project");
      await sleep(300);
      const s = await state(page);
      assert(s.shown && s.value === `@${NOTES}/`, `one Enter acted twice: ${JSON.stringify(s)}`);
      return "one setup, one Enter";
    });

    await run("Enter while the answer is on its way takes the first row of that answer, never a stale one", async () => {
      await reset(page);
      await page.keyboard.type(`@${SHOP}/`);
      await waitState(page, (x) => x.title === SHOP && !x.busy, "the files never came");
      await page.route("**/mention/files?**", async (route) => {
        await sleep(700);
        await route.continue();
      });
      try {
        await page.keyboard.type("checkoutform");
        await page.keyboard.press("Enter");
        const s = await waitState(page, (x) => !x.shown, "the pick waiting for the answer never happened");
        assert(s.value === `${path.join(shopDir, "src/components/CheckoutForm.tsx")} `, `a stale row was taken: ${s.value}`);
      } finally {
        await page.unroute("**/mention/files?**");
      }
      return "picked from the fresh answer";
    });

    await run("a file query nobody matches says so, a blank after it closes, the caret leaving the token or the box closes", async () => {
      await reset(page);
      await page.keyboard.type(`@${SHOP}/qqqzzz`);
      await waitState(page, (x) => x.note.includes("No folder or file matches") && !x.busy, "no note for a query nobody matches");
      await page.keyboard.type(" ");
      await waitState(page, (x) => !x.shown, "a blank after a dead query did not close");
      await reset(page);
      await page.keyboard.type(`@${S}`);
      await waitState(page, (x) => x.shown, "did not open");
      for (let i = 0; i <= S.length; i += 1) await page.keyboard.press("ArrowLeft");
      await waitState(page, (x) => !x.shown, "the caret before the @ did not close");
      await page.keyboard.press("End");
      await page.keyboard.type(" @");
      await waitState(page, (x) => x.shown, "did not open again");
      await page.click("[data-assistant-scroll]");
      await waitState(page, (x) => !x.shown, "leaving the box did not close");
      return "dead query, caret and blur";
    });

    await run("the server checks every input: known projects only, a bounded query, at most 50 results, every ref under its project", async () => {
      for (const name of ["", "nope", "..", `${SHOP}/..`, `../${path.basename(AUX)}`, `${SHOP}/src`, "x".repeat(300)]) {
        const r = await mentionFiles(page, name, "");
        assert(r.status === 404 && r.body.error === "Project not found.", `${name.slice(0, 40)} answered ${r.status} ${JSON.stringify(r.body)}`);
      }
      const long = await mentionFiles(page, SHOP, "q".repeat(257));
      assert(long.status === 400 && long.body.error === "The search is too long.", `a long query answered ${long.status}`);
      const many = await mentionFiles(page, MONO, "file");
      assert(many.status === 200 && many.body.files.length === 50 && many.body.truncated && many.body.total > 50, `the answer is not bounded: ${many.body.files.length} of ${many.body.total}`);
      assert(many.body.files.every((f) => f.ref === path.join(PROJECTS, MONO, f.path) + (f.folder ? "/" : "")), "a ref does not name its file under the project");
      const folders = await mentionFiles(page, MONO, "sub1");
      assert(folders.body.files.some((f) => f.folder) && folders.body.files.every((f) => f.ref === path.join(PROJECTS, MONO, f.path) + (f.folder ? "/" : "")), `a folder's ref does not name it under the project: ${JSON.stringify(folders.body.files.slice(0, 3))}`);
      const mono = `${path.join(PROJECTS, MONO)}/`;
      for (const q of ["../", `../${SHOP}/`, "/etc/", ".git/", "pkg/../../"]) {
        const r = await mentionFiles(page, MONO, q);
        assert(r.status === 200 && !r.body.folder.includes("..") && r.body.ref.startsWith(mono) && r.body.files.every((f) => f.ref.startsWith(mono)), `${q} walked out: ${JSON.stringify(r.body).slice(0, 300)}`);
      }
      return `${many.body.files.length} of ${many.body.total}`;
    });

    await run("a project that shows up on the disk is listed at the next @, no reload and no event needed", async () => {
      await reset(page);
      await page.keyboard.type("@");
      await waitState(page, (x) => x.rows.includes(`@${SHOP}`) && !x.rows.includes(`@${LATE}`), "the projects never came");
      await page.keyboard.press("Escape");
      fs.mkdirSync(path.join(PROJECTS, LATE));
      try {
        await page.keyboard.type(" @");
        await waitState(page, (x) => x.rows.includes(`@${LATE}`), "the new project never showed");
      } finally {
        fs.rmSync(path.join(PROJECTS, LATE), { recursive: true, force: true });
      }
      await page.keyboard.press("Escape");
      await page.keyboard.type(" @");
      await waitState(page, (x) => x.rows.includes(`@${SHOP}`) && !x.rows.includes(`@${LATE}`), "the removed project stayed listed");
      return "listed and gone again";
    });

    await run("phone: taps pick without taking the keyboard, rows are thumb sized, the popup sits over the box inside the screen", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      const mp = await mobilePage();
      await openAssistant(mp, id);
      await mp.fill(BOX, "");
      await mp.tap(BOX);
      await mp.keyboard.type("@");
      let s = await waitState(mp, (x) => x.shown && x.rows.includes(`@${SHOP}`), "the phone did not open");
      let g = await geometry(mp);
      assert(g.foot === "none", `the key hints show on a phone: ${g.foot}`);
      assert(Math.min(...g.rowHeights) >= 43.5, `a row is smaller than a thumb: ${g.rowHeights}`);
      assert(g.panel.left >= 0 && g.panel.right <= g.view.width && g.overflow <= 0, `the popup leaves the screen sideways: ${JSON.stringify(g)}`);
      assert(g.panel.bottom <= g.input.top && g.panel.top >= g.bound.top - 1, `the popup does not sit over the box: ${JSON.stringify(g)}`);
      await mp.keyboard.type(S);
      await mp.tap(`[data-mention-list] [role="option"][data-project-name="${SHOP}"]`);
      s = await waitState(mp, (x) => x.title === SHOP && !x.busy, "a tap did not take the project");
      assert(s.focused, "the tap took the focus off the box, the keyboard would close");
      await mp.tap("[data-mention-back]");
      s = await waitState(mp, (x) => x.title === "Projects", "the back arrow did not go back on a phone");
      assert(s.focused && s.rows[s.active] === `@${SHOP}`, `the back tap lost the focus or the mark: ${JSON.stringify(s)}`);
      await mp.keyboard.press("Enter");
      await waitState(mp, (x) => x.title === SHOP && !x.busy, "Enter on a phone did not take the project");
      await mp.keyboard.type("payments");
      await waitState(mp, (x) => x.rows[0] === "src/api/payments.go" && !x.busy, "payments.go never came on the phone");
      await mp.tap('[data-mention-list] [role="option"]');
      s = await waitState(mp, (x) => !x.shown, "a tap did not pick the file");
      assert(s.focused && s.value === `${path.join(shopDir, "src/api/payments.go")} `, `the phone box holds ${JSON.stringify(s)}`);
      return `rows ${Math.min(...g.rowHeights)}px`;
    });

    await run("phone with the keyboard up: the popup shrinks into the room above the box and scrolls", async () => {
      const mp = await mobilePage();
      await mp.setViewportSize({ width: 390, height: 460 });
      try {
        await reset(mp);
        await mp.keyboard.type(`@${MONO}/pkg/`);
        await waitState(mp, (x) => x.title === "pkg/" && !x.busy && x.rows.length > 10, "the short phone did not open the big project's folder");
        await sleep(200);
        const g = await geometry(mp);
        assert(g.panel.top >= Math.max(g.bound.top, g.view.top) - 1 && g.panel.bottom <= g.input.top, `the popup runs out of the room: ${JSON.stringify(g)}`);
        assert(g.list.scroll > g.list.client, `the rows do not scroll in the short popup: ${JSON.stringify(g.list)}`);
        for (let i = 0; i < 5; i += 1) await mp.keyboard.press("ArrowUp");
        const s = await state(mp);
        const seen = await mp.evaluate(() => {
          const list = document.querySelector("[data-mention-list]");
          const row = list.querySelector('[role="option"].active');
          const a = row.getBoundingClientRect();
          const b = list.getBoundingClientRect();
          return a.top >= b.top - 1 && a.bottom <= b.bottom + 1;
        });
        assert(seen, `the marked row ${s.active} is scrolled out of the popup`);
        return `popup ${Math.round(g.panel.height)}px in ${Math.round(g.input.top - g.bound.top)}px`;
      } finally {
        await mp.setViewportSize({ width: 390, height: 844 });
      }
    });

    await run("phone: a tap on a folder walks into it, the back arrow goes up, a tap on the folder itself puts it in", async () => {
      const mp = await mobilePage();
      await reset(mp);
      await mp.keyboard.type(`@${SHOP}/`);
      await waitState(mp, (x) => x.title === SHOP && !x.busy && x.rows.includes("src/"), "the phone did not list the project");
      await mp.tap('[data-mention-list] [role="option"][data-mention-walk="src"]');
      let s = await waitState(mp, (x) => x.title === "src/" && !x.busy, "a tap did not walk into src");
      assert(s.focused && s.value === `@${SHOP}/src/` && s.trail === `${SHOP}/`, `the tap took the focus or did not complete the folder: ${JSON.stringify(s)}`);
      const g = await geometry(mp);
      assert(Math.min(...g.rowHeights) >= 43.5 && g.panel.left >= 0 && g.panel.right <= g.view.width && g.overflow <= 0 && g.panel.bottom <= g.input.top, `the folder's rows do not fit the phone: ${JSON.stringify(g)}`);
      await mp.tap('[data-mention-list] [role="option"][data-mention-walk="src/api"]');
      await waitState(mp, (x) => x.title === "api/" && !x.busy, "a tap did not walk into api");
      await mp.tap("[data-mention-back]");
      s = await waitState(mp, (x) => x.title === "src/" && !x.busy && x.value === `@${SHOP}/src/`, "the back tap did not go up");
      assert(s.focused && s.rows[s.active] === "src/api/", `the back tap lost the focus or the mark: ${JSON.stringify(s)}`);
      await mp.tap('[data-mention-list] [role="option"][data-mention-walk="src/api"]');
      await waitState(mp, (x) => x.title === "api/" && !x.busy && x.value === `@${SHOP}/src/api/`, "a second tap did not walk into api");
      await mp.tap('[data-mention-list] [role="option"]');
      s = await waitState(mp, (x) => !x.shown, "a tap on the folder did not put it in");
      assert(s.focused && s.value === `${path.join(shopDir, "src/api")}/ `, `the phone box holds ${JSON.stringify(s)}`);
      return `rows ${Math.min(...g.rowHeights)}px`;
    });

    await fileMentionLinks({ page, run, id, AUX, PROJECTS, SHOP, CHECKOUT, NOTES, BOX, openAssistant, reset, waitState });

    if (SHOTS && engine === "chromium") {
      await run("screenshots of the projects, a project's folders and files and the inside of a folder in both themes on a phone", async () => {
        fs.mkdirSync(SHOTS, { recursive: true });
        for (const theme of ["light", "dark"]) await shoot(browser, id, theme);
        return SHOTS;
      });
    }
  } finally {
    await deleteAssistant(page, id).catch(() => {});
  }
});

async function waitDraft(page, id, want) {
  const deadline = Date.now() + 5000;
  let text = "";
  while (Date.now() < deadline) {
    const response = await page.request.get(`${BASE}/assistants/${id}/draft`, { headers: { Accept: "application/json" } });
    text = (await response.json().catch(() => ({}))).text || "";
    if (text === want) return "the draft holds both paths";
    await sleep(100);
  }
  throw new Error(`the draft holds ${JSON.stringify(text)}`);
}
