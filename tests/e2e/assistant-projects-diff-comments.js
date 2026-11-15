const L = require("./lib");
const { assert, BASE, sleep } = L;
const fs = require("fs");
const path = require("path");
const { execFileSync } = require("child_process");
const { guestPage } = require("./project-share-guest");
const { startFakeForge, addHost, gitlabNote, gitlabDiscussion } = require("./forge-fake");
const { editChecks } = require("./assistant-projects-edit");

// The line comments in the inline diff of a file in the Changes of a project
// opened in an assistant's aside (GET /assistants/projects/view, GET
// /assistants/projects/diff, @dc/inline-diff over @dc/comment-gutter, the
// editor's own gutter marks), chromium, against the fake GitLab: the diff's
// line numbers mark the file's threads open, outdated and resolved as the
// editor marks them (.cm-comment-line via gutterLineClass, the tint in every
// gutter but the change gutter of @codemirror/merge, which stays plain beside a
// commented unchanged line and keeps its colour beside a changed one, in both
// schemes and on a phone, the editor's diff view of the file alike with its
// fold gutter tinted), a fold of unchanged lines says how many threads it
// hides, a press on a marked line unfolds the copies of its cards in Reviews
// under the line (notes with author and age, Reply, Hand off, Resolve), in the
// colours of the cards in both schemes, and a second one folds them, with the
// gutter following the height; Reply and Resolve of a thread of the forge go
// through the forge thread route, of a local one through the editor's line
// comment routes, live, the reply growing with its text up to 35% of the
// window, then scrolling; a press on an unmarked line of the working copy opens
// a field under it, growing the same way, an empty Add marked invalid, a
// refused one standing at the field with the server's sentence and the words
// kept, no toast, Enter a new line, Ctrl+Enter and Cmd+Enter adding the comment
// once through the editor's route (POST .../editor/comments with the line's
// text as its quote), which then stands unfolded under its line in view, marked
// briefly, also on a phone, Cancel and a second press closing an empty field; a
// removed line takes none; a comment made elsewhere marks its line in the open
// diff while a field keeps its words and its focus and an open reply keeps its
// words; the Uncommitted list's diff against HEAD marks the same threads on the
// file's lines; project comments and threads of other files stay in Reviews
// alone; a guest of a link to the assistant sees the marks and the threads and
// writes nothing; on a phone's sheet a tap on a number unfolds a thread and
// opens a field inside the width, Add making the comment; a reply past its cap
// keeps the caret's line in view while typing, Enter at the end and in the
// middle, a paste, Backspace and lines set by code, desktop and phone; a thread
// of long unbroken text with its reply open and a field keep the diff's width
// on desktop and phone in both schemes, the quoted line wrapping; the bin of a
// local thread in its head beside the badge and of each of its replies in the
// diff, as far in from the right edge as the file icon from the left, none on a
// thread of the forge, deletes them behind the editor's confirm, the diff, its
// marks and Reviews following while a field on another line keeps its words,
// and on a phone Reply, Hand off and Resolve on one line and a tap on the bin
// and its confirm. A note's edit in place stands in assistant-projects-edit.js.
//
// Instance like assistant-projects-reviews.js's: tests/e2e/fakes first on PATH,
// a scratch HOME, its --projects-dir mounted into the runner at the same path,
// the fake forge's ports FORGE_PORT and FORGE_TLS_PORT (default 3157 and 3158)
// published on the host's loopback, where the instance reaches them at
// FORGE_ADDR (default 127.0.0.1). SHOTS_DIR with a mount takes desktop, dark
// desktop and phone screenshots, and a desktop and a phone one of the long
// thread. About two minutes.
const TAG = Date.now().toString(36).slice(-5);
const SHOTS_DIR = process.env.SHOTS_DIR || "";
const FAKE_PORT = Number(process.env.FORGE_PORT || 3157);
const FAKE_TLS_PORT = Number(process.env.FORGE_TLS_PORT || 3158);
const FAKE_ADDR = process.env.FORGE_ADDR || "127.0.0.1";
const TOKEN = `glpat-e2e${Date.now().toString(36)}Dc7pM4wQ`;
const ORIGIN = new URL(BASE).origin;
const REPO = `apdcm${TAG}`;
const FILE = "src/app.txt";
const OTHER = "src/other.txt";
const PANE = "#assistant-projects";
const lines = (n, word) => Array.from({ length: n }, (_, i) => `${word} ${i + 1}\n`).join("");
const PATCH = "@@ -17,7 +17,7 @@\n app 17\n app 18\n app 19\n-app 20\n+app 20 changed\n app 21\n app 22\n app 23";

function git(dir, ...args) {
  return execFileSync("git", ["-c", "user.name=e2e", "-c", "user.email=e2e@localhost", "-c", "commit.gpgsign=false", "-C", dir, ...args], { encoding: "utf8" });
}

async function post(page, url, body, { json = false } = {}) {
  return page.evaluate(async ({ url, body, json }) => {
    const token = document.querySelector('meta[name="csrf-token"]')?.content || "";
    const r = await fetch(url, {
      method: "POST",
      headers: { "X-CSRF-Token": token, Accept: "application/json", ...(json ? { "Content-Type": "application/json" } : {}) },
      body: json ? JSON.stringify(body) : new URLSearchParams(body),
    });
    return { ok: r.ok, status: r.status, text: await r.text() };
  }, { url, body, json });
}

async function postJSON(page, url, body, opts) {
  const res = await post(page, url, body, opts);
  assert(res.ok, `POST ${url} answered ${res.status}: ${res.text.slice(0, 200)}`);
  return JSON.parse(res.text || "{}");
}

const comment = async (page, line, text, lineText) => (await postJSON(page, `/projects/${REPO}/editor/comments`, { path: FILE, line, text, ...(lineText === undefined ? {} : { lineText }) }, { json: true })).comment?.id;
const stored = (page) => page.evaluate(async (name) => (await (await fetch(`/projects/${name}/editor/comments`)).json()).comments || [], REPO);

async function openPage(p, url) {
  await p.goto(url, { waitUntil: "domcontentloaded" });
  await L.dismissUpdate(p);
  await p.waitForSelector(`${PANE} [data-assistant-body]`, { state: "attached", timeout: 15000 });
}

const rowOf = (file, list = "files") => `${PANE} [data-assistant-changed-files="${list}"] [data-assistant-changed-file="${file}"]`;

// diff reads a file's unfolded diff as it stands on the screen: the marked
// line numbers with their kind (o open, x outdated, r resolved), the folds,
// what stands under a line, threads and field, where it stands against the
// numbers around it, and whether the number after a block sits on its line.
const diff = (p, file, list = "files") => p.evaluate(({ pane, file, list }) => {
  const shown = (el) => !!el && el.getClientRects().length > 0 && getComputedStyle(el).visibility !== "hidden";
  const text = (el) => (shown(el) ? el.textContent.replace(/\s+/g, " ").trim() : "");
  const row = document.querySelector(`${pane} [data-assistant-changed-files="${list}"] [data-assistant-changed-file="${CSS.escape(file)}"]`);
  const cm = row?.querySelector("[data-assistant-fold] .cm-editor");
  const cells = [...(cm?.querySelectorAll(".cm-lineNumbers .cm-gutterElement") || [])].filter(shown);
  const cell = (n) => cells.find((el) => el.textContent.trim() === String(n));
  const codeLines = [...(cm?.querySelectorAll(".cm-content > .cm-line") || [])].filter(shown);
  const kind = (el) => (el.classList.contains("cm-comment-line-outdated") ? "x" : el.classList.contains("cm-comment-line-resolved") ? "r" : "o");
  const blocks = [...(cm?.querySelectorAll("[data-line-block]") || [])].filter(shown).map((block) => {
    const n = Number(block.dataset.lineBlock);
    const above = cell(n)?.getBoundingClientRect();
    const below = cell(n + 1)?.getBoundingClientRect();
    const box = block.getBoundingClientRect();
    const next = cell(n + 1);
    const nextLine = next ? codeLines[cells.indexOf(next)] : null;
    const form = block.querySelector("[data-line-comment-form]");
    return {
      line: n,
      under: !!above && box.top >= above.bottom - 2 && (!below || below.top >= box.bottom - 2),
      aligned: !next || !nextLine || Math.abs(next.getBoundingClientRect().top - nextLine.getBoundingClientRect().top) <= 2,
      threads: [...block.querySelectorAll(":scope > [data-line-thread]")].filter(shown).map((t) => ({
        id: t.dataset.thread || "",
        cid: t.dataset.lineThread,
        notes: [...t.querySelectorAll("[data-comment-note]")].filter(shown).map((n) => `${text(n.querySelector("[data-comment-note-author]"))}: ${text(n.querySelector("[data-comment-note-text]"))}`),
        when: text(t.querySelector("[data-comment-note] dc-time")),
        actions: [...t.querySelectorAll("[data-line-thread-actions] button")].filter(shown).map((b) => text(b) || b.getAttribute("aria-label")),
        bin: (() => { const b = t.querySelector("[data-line-thread-head] [data-line-thread-delete]"); const o = t.querySelector("[data-line-thread-head] [data-line-thread-origin]"); return shown(b) && !!o && b.getBoundingClientRect().left >= o.getBoundingClientRect().right; })(),
        oneLine: new Set([...t.querySelectorAll("[data-line-thread-actions] button")].filter(shown).map((b) => Math.round(b.getBoundingClientRect().top))).size <= 1,
        drops: [...t.querySelectorAll("[data-comment-note]")].filter((n) => shown(n.querySelector("[data-comment-note-drop]"))).map((n) => text(n.querySelector("[data-comment-note-text]"))),
        reply: shown(t.querySelector("[data-line-thread-reply]")),
        inset: (() => {
          const card = t.getBoundingClientRect();
          const icon = t.querySelector("[data-line-thread-head] > .ti");
          const bins = [...t.querySelectorAll("[data-line-thread-delete] .ti, [data-comment-note-drop] .ti")].filter(shown);
          return { start: icon ? Math.round(icon.getBoundingClientRect().left - card.left) : null, ends: bins.map((b) => Math.round(card.right - b.getBoundingClientRect().right)) };
        })(),
        flash: t.classList.contains("dc-flash"),
        inView: (() => { const s = t.closest(".dc-assistant-aside-body")?.getBoundingClientRect(); const b = t.getBoundingClientRect(); return !!s && b.top >= s.top - 1 && b.bottom <= s.bottom + 1; })(),
      })),
      field: shown(form),
      value: form?.querySelector("textarea")?.value ?? null,
      invalid: !!form?.querySelector("textarea.is-invalid"),
      feedback: form?.querySelector("textarea.is-invalid") ? text(form.querySelector(".invalid-feedback")) : "",
      focused: !!form && document.activeElement === form.querySelector("textarea"),
      right: box.right,
    };
  });
  return {
    drawn: shown(cm),
    marks: cells.filter((el) => el.classList.contains("cm-comment-line")).map((el) => `${el.textContent.trim()}${kind(el)}`).join(","),
    folds: [...(cm?.querySelectorAll(".cm-collapsedLines") || [])].filter(shown).map((el) => el.textContent.trim()),
    blocks,
    removed: [...(cm?.querySelectorAll(".cm-deletedChunk .cm-deletedLine") || [])].filter(shown).map((el) => el.textContent),
    tint: { open: cell(22) ? getComputedStyle(cell(22)).color : "", plain: cell(23) ? getComputedStyle(cell(23)).color : "" },
    width: window.innerWidth,
  };
}, { pane: PANE, file, list });

async function untilDiff(p, want, what, file = FILE, list = "files", timeout = 30000) {
  const end = Date.now() + timeout;
  let last = null;
  while (Date.now() < end) {
    last = await diff(p, file, list);
    if (want(last)) return last;
    await sleep(200);
  }
  throw new Error(`${what}: ${JSON.stringify(last)}`);
}

const block = (d, line) => d.blocks.find((b) => b.line === line);

// even says every bin of a thread stands as far in from its right edge as its
// file icon from the left.
const even = (t) => !!t && t.inset.start !== null && t.inset.ends.length > 0 && t.inset.ends.every((end) => Math.abs(end - t.inset.start) <= 1);

// grown reads a field's box against the cap a growing field stops at.
const grown = (p, sel) => p.evaluate((sel) => {
  const el = document.querySelector(sel);
  const cs = getComputedStyle(el);
  return { height: Math.round(el.getBoundingClientRect().height), cap: Math.round(window.innerHeight * 0.35), overflow: cs.overflowY, resize: cs.resize, scroll: el.scrollHeight, client: el.clientHeight };
}, sel);
const rows = (n) => Array.from({ length: n }, (_, i) => `Row ${i + 1}`).join("\n");
const toasted = (p, words) => p.evaluate((w) => [...document.querySelectorAll(".dc-toast")].some((el) => el.textContent.includes(w)), words);

// gutterRow reads the gutter cells beside a line's number in a diff, the
// aside's of FILE unless an editor is named, each found by its box: the paint
// of the number, of the change gutter of @codemirror/merge, whether that marks
// the line changed, and the paint of every other gutter.
const gutterRow = (p, line, editor = `${rowOf(FILE)} [data-assistant-fold] .cm-editor`) => p.evaluate(({ editor, line }) => {
  const cm = document.querySelector(editor);
  const number = [...(cm?.querySelectorAll(".cm-lineNumbers .cm-gutterElement") || [])].find((el) => el.textContent.trim() === String(line) && el.getClientRects().length);
  const lane = cm?.querySelector(".cm-changeGutter");
  if (!number || !lane) return null;
  const row = number.getBoundingClientRect();
  const y = row.top + row.height / 2;
  const at = (gutter) => [...gutter.querySelectorAll(".cm-gutterElement")].find((el) => {
    const box = el.getBoundingClientRect();
    return box.height > 0 && box.top <= y && box.bottom >= y;
  });
  const paint = (el) => getComputedStyle(el).backgroundColor;
  const change = at(lane);
  return {
    number: paint(number),
    change: paint(change || lane),
    changed: !!change?.classList.contains("cm-changedLineGutter"),
    beside: [...cm.querySelectorAll(".cm-gutter:not(.cm-lineNumbers):not(.cm-changeGutter)")].map((g) => paint(at(g) || g)),
  };
}, { editor, line });

// tinted says a commented line's row wears the tint in every gutter but the
// change gutter, which reads as the change gutter of the plain row beside it.
const tinted = (g, plain, change = plain.change) => !!g && g.number !== plain.number && g.beside.every((c) => c === g.number) && g.change === change;

// press presses a line's number in a file's diff, by mouse or by touch, found by its number once it stood still for five frames: the view hands its cells to other lines.
async function press(p, line, { list = "files", touch = false, file = FILE } = {}) {
  const still = await p.evaluate(async ({ sel, line }) => {
    const find = () => [...document.querySelectorAll(`${sel} .cm-lineNumbers .cm-gutterElement`)].find((el) => el.textContent.trim() === String(line) && el.getClientRects().length);
    find()?.scrollIntoView({ block: "center" });
    for (let i = 0, last, now, n = 0; i < 90; i += 1, last = now) {
      await new Promise((r) => requestAnimationFrame(r));
      now = find()?.getBoundingClientRect().y;
      n = now !== undefined && now === last ? n + 1 : 0;
      if (n === 5) return true;
    }
  }, { sel: rowOf(file, list), line });
  assert(still, `line ${line} of ${file} shows no number to press`);
  const cell = p.locator(`${rowOf(file, list)} .cm-lineNumbers .cm-gutterElement`).filter({ hasText: new RegExp(`^${line}$`) }).first();
  await (touch ? cell.tap() : cell.click());
}

async function openChanges(p) {
  await p.click(`${PANE} [data-assistant-project="${REPO}"] [data-assistant-project-changes]`);
  await p.waitForSelector(`${PANE} [data-assistant-project-view-body="${REPO}"] [data-assistant-project-changes-section]`, { state: "visible", timeout: 30000 });
  await p.waitForSelector(`${rowOf(FILE)} [data-assistant-changed-toggle]`, { state: "visible", timeout: 30000 });
}

async function unfold(p, list = "files", touch = false) {
  const toggle = `${rowOf(FILE, list)} [data-assistant-changed-toggle]`;
  if (touch) await p.tap(toggle);
  else await p.click(toggle);
  return untilDiff(p, (d) => d.drawn && d.marks !== "", `the diff of ${list} was not drawn with its marks`, FILE, list);
}

L.runFeature("assistant-projects-diff-comments", async ({ browser, page, run, mobilePage, bag }) => {
  const fake = await startFakeForge({ port: FAKE_PORT, tlsPort: FAKE_TLS_PORT, token: TOKEN });
  const state = fake.state;
  const fakeWeb = `http://${FAKE_ADDR}:${FAKE_PORT}`;
  const coders = [];
  const ids = {};
  let owner = "";
  let repoDir = "";
  let hostEdit = "";
  let linkId = "";
  let guest = null;
  try {
    await run("setup: a branch on the fake GitLab changing a file, a review thread and local threads on its lines", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      owner = (await postJSON(page, "/assistants/new", { form: "new", coder: "claude" })).id;
      assert(owner, "no assistant made");
      await L.createProject(page, REPO);
      repoDir = await L.projectPath(page, REPO);
      assert(fs.existsSync(repoDir), `the projects dir is not mounted at ${repoDir}`);
      git(repoDir, "init", "-q", "-b", "main");
      fs.mkdirSync(path.join(repoDir, "src"));
      fs.writeFileSync(path.join(repoDir, FILE), lines(40, "app"));
      fs.writeFileSync(path.join(repoDir, OTHER), lines(3, "other"));
      git(repoDir, "add", "-A");
      git(repoDir, "commit", "-q", "-m", "base");
      git(repoDir, "switch", "-q", "-c", "feature");
      fs.writeFileSync(path.join(repoDir, FILE), lines(40, "app").replace("app 20\n", "app 20 changed\n"));
      git(repoDir, "commit", "-q", "-am", "Change line 20");
      const pushed = git(repoDir, "rev-parse", "HEAD").trim();
      git(repoDir, "update-ref", "refs/remotes/origin/feature", pushed);
      git(repoDir, "remote", "add", "origin", `${fakeWeb}/gl/team/sub/app.git`);
      fs.writeFileSync(path.join(repoDir, FILE), lines(40, "app").replace("app 20\n", "app 20 changed\n").replace("app 35\n", "app 35 local\n"));
      Object.assign(state, { head: pushed, status: "success", diffFiles: [{ path: FILE, patch: PATCH }], published: [], replies: [], resolved: [] });
      state.discussions = (s) => [gitlabDiscussion("d1dc", false, [gitlabNote(57, "root", "Why 22?", { resolvable: true, at: { head: s.head, path: FILE, newLine: 22 } })])];
      const added = await addHost(page, { kind: "gitlab", web_url: `${fakeWeb}/gl`, aliases: `apdc-${TAG}`, token: TOKEN });
      assert(!added.failed, added.failed);
      hostEdit = await page.evaluate(async (mark) => {
        const html = await (await fetch("/settings/forges")).text();
        const doc = new DOMParser().parseFromString(html, "text/html");
        return [...doc.querySelectorAll("[data-forge-host]")].find((r) => r.textContent.includes(mark))?.querySelector("[data-forge-edit]")?.getAttribute("href") || "";
      }, `apdc-${TAG}`);
      assert(hostEdit, "the added host has no Edit on the list");
      ids.hidden = await comment(page, 5, "Hidden in the fold");
      ids.done = await comment(page, 18, "Done here");
      ids.open = await comment(page, 20, "Why changed?");
      ids.gone = await comment(page, 21, "Lost its line", "app 21 old");
      ids.tail = await comment(page, 36, "Near the end");
      assert(Object.values(ids).every(Boolean), `the line comments were not made: ${JSON.stringify(ids)}`);
      await postJSON(page, `/projects/${REPO}/editor/comments/resolve`, { id: ids.done, resolved: true }, { json: true });
      ids.project = (await postJSON(page, `/projects/${REPO}/editor/comments`, { project: true, text: "On the whole project" }, { json: true })).comment?.id;
      ids.other = (await postJSON(page, `/projects/${REPO}/editor/comments`, { path: OTHER, line: 2, text: "Elsewhere" }, { json: true })).comment?.id;
      assert(ids.project && ids.other, "the project comment or the other file's comment was not made");
      const coder = (await postJSON(page, "/coders/new", { name: `apdc-${TAG}`, project: repoDir, coder: "claude", automatic_approval: "on", prompt: `job-${TAG}` })).id;
      assert(coder, "no coder made");
      coders.push(coder);
      await postJSON(page, "/assistants/jobs", { form: "steer", assistant: owner, terminal: coder, task: "Diff comments check", done_when: "" });
    });

    await run("the diff marks the file's threads as the editor does, a fold names the thread it hides", async () => {
      await page.setViewportSize({ width: 1360, height: 900 });
      await openPage(page, `${BASE}/assistants/${owner}`);
      await page.waitForFunction(({ pane, name }) => /open threads/.test(document.querySelector(`${pane} [data-assistant-project="${name}"] [data-assistant-project-review]`)?.textContent || ""), { pane: PANE, name: REPO }, { timeout: 30000 });
      await page.evaluate(() => { window.__noReload = 1; });
      await openChanges(page);
      const d = await unfold(page);
      const settled = await untilDiff(page, (x) => x.marks === "18r,20o,21x,22o,36o", "the diff's marks").catch(() => d);
      assert(settled.marks === "18r,20o,21x,22o,36o", `the diff marks ${settled.marks}`);
      assert(JSON.stringify(settled.folds) === JSON.stringify(["16 unchanged lines · 1 thread", "8 unchanged lines"]), `the folds read ${JSON.stringify(settled.folds)}`);
      assert(settled.blocks.length === 0 && JSON.stringify(settled.removed) === JSON.stringify(["app 20", "app 35"]), `the diff stands with ${JSON.stringify(settled.blocks)}, removed ${JSON.stringify(settled.removed)}`);
      assert(settled.tint.open && settled.tint.open !== settled.tint.plain, `the open mark wears ${JSON.stringify(settled.tint)}`);
      await page.click(`${rowOf(FILE)} .cm-collapsedLines >> nth=0`);
      const open = await untilDiff(page, (x) => x.folds.length === 1 && x.marks.startsWith("5o,"), "the fold did not unfold onto the hidden thread");
      assert(open.folds[0] === "8 unchanged lines", `the folds read ${JSON.stringify(open.folds)}`);
      return settled.marks;
    });

    await run("the mark tints every gutter but the change gutter, a commented unchanged line keeps it plain, a changed one its colour, both schemes", async () => {
      for (const scheme of ["light", "dark"]) {
        await page.emulateMedia({ colorScheme: scheme });
        await page.waitForFunction(({ sel, dark }) => (getComputedStyle(document.querySelector(`${sel} .cm-editor`)).backgroundColor === "rgb(40, 44, 52)") === dark, { sel: rowOf(FILE), dark: scheme === "dark" }, { timeout: 15000 });
        const plain = await gutterRow(page, 23);
        assert(plain && !plain.changed, `the plain line 23 in ${scheme} reads ${JSON.stringify(plain)}`);
        for (const line of [5, 18, 21, 22, 36]) {
          const g = await gutterRow(page, line);
          assert(tinted(g, plain) && !g.changed, `line ${line} in ${scheme} reads ${JSON.stringify(g)} against ${JSON.stringify(plain)}`);
        }
        const bare = await gutterRow(page, 35);
        assert(bare && bare.changed && bare.change !== plain.change && bare.number === plain.number, `the changed line 35 in ${scheme} reads ${JSON.stringify(bare)}`);
        const changed = await gutterRow(page, 20);
        assert(changed && changed.changed && tinted(changed, plain, bare.change), `the commented changed line 20 in ${scheme} reads ${JSON.stringify(changed)} against ${JSON.stringify(bare)}`);
      }
      await page.emulateMedia({ colorScheme: "light" });
    });

    await run("the editor's diff view of the file tints the same gutters, desktop and phone, both schemes", async () => {
      const editor = ".editor-surface .cm-editor:not(.cm-merge-a):has(.cm-changeGutter)";
      const phone = await L.newMobile(browser);
      try {
        for (const p of [await page.context().newPage(), await phone.newPage()]) {
          if (p.context() === phone) await L.login(p);
          for (const scheme of ["light", "dark"]) {
            await p.emulateMedia({ colorScheme: scheme });
            await p.goto(`${BASE}/projects/${REPO}/editor?file=${encodeURIComponent(FILE)}&line=1`, { waitUntil: "domcontentloaded" });
            await L.dismissUpdate(p);
            await p.waitForFunction(() => document.querySelector("dc-editor")?.dataset.gitRepo === "1" && document.querySelectorAll(".cm-lineNumbers .cm-comment-line").length > 0, null, { timeout: 20000 });
            await p.click(`.editor-tab[data-path="${FILE}"]`, { button: "right" });
            await p.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
            await p.locator(".dc-context-menu .dropdown-item", { hasText: "Show git diff" }).first().click();
            await p.waitForSelector(`${editor} .cm-changedLineGutter`, { timeout: 20000 });
            const plain = await gutterRow(p, 33, editor);
            const marked = await gutterRow(p, 36, editor);
            const bare = await gutterRow(p, 35, editor);
            const where = `${p.context() === phone ? "phone" : "desktop"} ${scheme}`;
            assert(plain && !plain.changed && tinted(marked, plain) && marked.beside.length > 0 && !marked.changed, `the commented line 36 on the ${where} reads ${JSON.stringify(marked)} against ${JSON.stringify(plain)}`);
            assert(bare && bare.changed && bare.change !== plain.change && bare.number === plain.number, `the changed line 35 on the ${where} reads ${JSON.stringify(bare)}`);
            await p.click(`.editor-tab[data-path="${FILE}"]`, { button: "right" });
            await p.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
            await p.locator(".dc-context-menu .dropdown-item", { hasText: "Hide git diff" }).first().click();
            await p.waitForSelector(".cm-changeGutter", { state: "detached", timeout: 20000 });
          }
          await p.close();
        }
      } finally {
        await phone.close();
      }
    });

    await run("a press on a marked line unfolds its threads under it, the gutter follows, a second press folds them", async () => {
      await press(page, 22);
      const d = await untilDiff(page, (x) => block(x, 22)?.threads.length === 1 && block(x, 22).under && block(x, 22).aligned, "the thread on line 22 did not unfold under its line");
      const b = block(d, 22);
      const t = b.threads[0];
      assert(b.under && b.aligned, `the thread does not stand under its line: ${JSON.stringify(b)}`);
      assert(t.id === "d1dc" && JSON.stringify(t.notes) === JSON.stringify(["root: Why 22?"]) && /ago$/.test(t.when), `the forge thread reads ${JSON.stringify(t)}`);
      assert(JSON.stringify(t.actions) === JSON.stringify(["Reply", "Hand off", "Resolve"]), `the forge thread offers ${JSON.stringify(t.actions)}`);
      await press(page, 20);
      const both = await untilDiff(page, (x) => block(x, 20)?.threads.length === 1 && block(x, 22)?.threads.length === 1 && x.blocks.every((b) => b.under && b.aligned), "the thread on line 20 did not unfold under its line");
      const local = block(both, 20).threads[0];
      assert(local.cid === ids.open && JSON.stringify(local.notes) === JSON.stringify(["You: Why changed?"]) && JSON.stringify(local.actions) === JSON.stringify(["Reply", "Hand off", "Resolve"]) && local.bin, `the local thread reads ${JSON.stringify(local)}`);
      assert(block(both, 20).under && block(both, 22).under && block(both, 20).aligned && block(both, 22).aligned, `the blocks stand off their lines: ${JSON.stringify(both.blocks)}`);
      await press(page, 18);
      const resolved = await untilDiff(page, (x) => block(x, 18)?.threads.length === 1, "the resolved thread did not unfold");
      assert(JSON.stringify(block(resolved, 18).threads[0].actions) === JSON.stringify(["Reply", "Hand off", "Reopen"]) && block(resolved, 18).threads[0].bin, `the resolved thread offers ${JSON.stringify(block(resolved, 18).threads[0].actions)}`);
      if (SHOTS_DIR) await page.screenshot({ path: path.join(SHOTS_DIR, "assistant-projects-diff-comments-desktop.png") });
      for (const scheme of ["dark", "light"]) {
        await page.emulateMedia({ colorScheme: scheme });
        await page.waitForFunction(({ sel, dark }) => (getComputedStyle(document.querySelector(`${sel} .cm-editor`)).backgroundColor === "rgb(40, 44, 52)") === dark, { sel: rowOf(FILE), dark: scheme === "dark" }, { timeout: 15000 });
        const read = () => page.evaluate(({ sel, id }) => {
          const look = (card) => [card, card.querySelector("[data-comment-note-text]"), card.querySelector("[data-comment-note-author]"), card.querySelector("[data-line-thread-actions] button")]
            .map((el) => `${getComputedStyle(el).color}/${getComputedStyle(el).backgroundColor}/${getComputedStyle(el).fontFamily}`);
          return { copy: look(document.querySelector(`${sel} [data-line-block] [data-line-thread="${id}"]`)), card: look(document.querySelector(`[data-assistant-project-reviews] [data-line-thread="${id}"]`)) };
        }, { sel: rowOf(FILE), id: ids.open });
        let looks = await read();
        for (let i = 0; i < 20 && JSON.stringify(looks.copy) !== JSON.stringify(looks.card); i += 1) {
          await sleep(150);
          looks = await read();
        }
        assert(JSON.stringify(looks.copy) === JSON.stringify(looks.card), `the copy in ${scheme} reads ${JSON.stringify(looks)}`);
        if (SHOTS_DIR && scheme === "dark") await page.screenshot({ path: path.join(SHOTS_DIR, "assistant-projects-diff-comments-dark.png") });
      }
      await press(page, 18);
      await press(page, 20);
      await untilDiff(page, (x) => !block(x, 18) && !block(x, 20) && block(x, 22)?.aligned && block(x, 22).under, "a second press did not fold the threads with the gutter following");
      return `${both.blocks.length} unfolded`;
    });

    await run("Reply and Resolve in the diff write a forge thread on the forge and a local one through the line comments, live", async () => {
      const thread = `${rowOf(FILE)} [data-line-block="22"] [data-line-thread]`;
      await page.click(`${thread} [data-line-thread-reply-open]`);
      await page.fill(`${thread} [data-line-thread-reply-text]`, "Because 22.");
      await page.keyboard.press("Control+Enter");
      await untilDiff(page, (x) => JSON.stringify(block(x, 22)?.threads[0]?.notes) === JSON.stringify(["root: Why 22?", "root: Because 22."]) && !block(x, 22).threads[0].reply, "the forge reply did not come back into the diff");
      assert(state.replies.some((r) => r.thread === "d1dc" && r.body === "Because 22."), `the forge holds ${JSON.stringify(state.replies)}`);
      await press(page, 20);
      const local = `${rowOf(FILE)} [data-line-block="20"] [data-line-thread]`;
      await page.waitForSelector(`${local} [data-line-thread-reply-open]`, { state: "visible" });
      await page.click(`${local} [data-line-thread-reply-open]`);
      const rest = await grown(page, `${local} [data-line-thread-reply-text]`);
      await page.fill(`${local} [data-line-thread-reply-text]`, rows(80));
      const g = await grown(page, `${local} [data-line-thread-reply-text]`);
      assert(rest.resize === "none" && Math.abs(g.height - g.cap) <= 1 && g.overflow === "auto" && g.scroll > g.client, `eighty rows of the reply in the diff read ${JSON.stringify(g)} from ${JSON.stringify(rest)}`);
      await page.fill(`${local} [data-line-thread-reply-text]`, "It had to.");
      assert(Math.abs((await grown(page, `${local} [data-line-thread-reply-text]`)).height - rest.height) <= 1, "the reply in the diff did not shrink back");
      await page.keyboard.press("Meta+Enter");
      await untilDiff(page, (x) => JSON.stringify(block(x, 20)?.threads[0]?.notes) === JSON.stringify(["You: Why changed?", "You: It had to."]), "the local reply did not show in the diff");
      assert((await stored(page)).find((c) => c.id === ids.open)?.replies?.some((r) => r.text === "It had to."), "the store does not hold the local reply");
      await page.click(`${local} [data-line-thread-resolve]`);
      await untilDiff(page, (x) => x.marks.includes("20r") && JSON.stringify(block(x, 20)?.threads[0]?.actions) === JSON.stringify(["Reply", "Hand off", "Reopen"]) && block(x, 20).threads[0].bin, "the local resolve did not mark the line resolved");
      assert((await stored(page)).find((c) => c.id === ids.open)?.resolved, "the store does not hold the resolve");
      await page.click(`${local} [data-line-thread-reopen]`);
      await untilDiff(page, (x) => x.marks.includes("20o"), "the reopen did not mark the line open again");
      await page.click(`${thread} [data-line-thread-resolve]`);
      await untilDiff(page, (x) => x.marks.includes("22r"), "the forge resolve did not mark the line resolved");
      assert(state.resolved.includes("d1dc"), `the forge resolved ${JSON.stringify(state.resolved)}`);
      await press(page, 20);
      await press(page, 22);
      await untilDiff(page, (x) => x.blocks.length === 0, "the threads did not fold again");
    });

    await run("a press on an unmarked line opens a field that makes a line comment anchored like the editor's", async () => {
      await press(page, 23);
      let d = await untilDiff(page, (x) => block(x, 23)?.field && block(x, 23).aligned, "no field opened under line 23");
      assert(block(d, 23).focused && block(d, 23).under && block(d, 23).aligned, `the field reads ${JSON.stringify(block(d, 23))}`);
      await page.click(`${rowOf(FILE)} [data-line-block="23"] [data-line-comment-add]`);
      d = await untilDiff(page, (x) => block(x, 23)?.invalid, "an empty Add was not marked invalid");
      assert((await stored(page)).every((c) => c.line !== 23), "an empty Add made a comment");
      const field = `${rowOf(FILE)} [data-line-block="23"] [data-line-comment-text]`;
      const rest = await grown(page, field);
      await page.fill(field, rows(8));
      let g = await grown(page, field);
      assert(rest.resize === "none" && g.height > rest.height + 40 && g.height < g.cap && g.overflow === "hidden", `eight rows read ${JSON.stringify(g)} from ${JSON.stringify(rest)}`);
      await page.fill(field, rows(80));
      g = await grown(page, field);
      assert(Math.abs(g.height - g.cap) <= 1 && g.overflow === "auto" && g.scroll > g.client, `eighty rows read ${JSON.stringify(g)}`);
      d = await untilDiff(page, (x) => block(x, 23)?.aligned, "the grown field does not keep the numbers on their lines");
      const tooLong = "\u00e4".repeat(9000);
      await page.fill(field, tooLong);
      await page.click(`${rowOf(FILE)} [data-line-block="23"] [data-line-comment-add]`);
      d = await untilDiff(page, (x) => block(x, 23)?.feedback === "That comment is too long.", "the refusal does not stand at the field");
      assert(block(d, 23).value === tooLong && block(d, 23).field && !(await toasted(page, "too long")) && (await stored(page)).every((c) => c.line !== 23), `the refused field reads ${JSON.stringify({ ...block(d, 23), value: block(d, 23).value === tooLong })} or a toast came`);
      await page.fill(field, "");
      assert(Math.abs((await grown(page, field)).height - rest.height) <= 1, "the emptied field did not shrink back");
      await page.click(field);
      await page.keyboard.type("Look");
      await page.keyboard.press("Enter");
      await page.keyboard.type("here");
      d = await diff(page, FILE);
      assert(block(d, 23).value === "Look\nhere" && !block(d, 23).invalid, `Enter did not start a line: ${JSON.stringify(block(d, 23))}`);
      await page.keyboard.press("Control+Enter");
      await page.keyboard.press("Control+Enter");
      d = await untilDiff(page, (x) => x.marks.includes("23o") && block(x, 23)?.threads.length === 1 && !block(x, 23).field && block(x, 23).threads[0].flash, "the new comment did not stand unfolded and marked under its line");
      assert(JSON.stringify(block(d, 23).threads[0].notes) === JSON.stringify(["You: Look here"]) && block(d, 23).threads[0].inView, `the new thread reads ${JSON.stringify(block(d, 23))}`);
      await untilDiff(page, (x) => block(x, 23)?.threads.length === 1 && !block(x, 23).threads[0].flash, "the mark did not go", FILE, "files", 5000);
      const made = (await stored(page)).filter((c) => c.path === FILE && c.line === 23);
      assert(made.length === 1 && made[0].lineText === "app 23" && made[0].text === "Look\nhere" && !made[0].outdated, `the store holds ${JSON.stringify(made)}`);
      ids.added = made[0].id;
      await press(page, 23);
      await untilDiff(page, (x) => !block(x, 23), "a second press did not fold the new thread");
      await press(page, 19);
      await untilDiff(page, (x) => block(x, 19)?.field, "no field opened under line 19");
      await page.click(`${rowOf(FILE)} [data-line-block="19"] [data-line-comment-cancel]`);
      await untilDiff(page, (x) => !block(x, 19), "Cancel did not close the field");
      await press(page, 19);
      await untilDiff(page, (x) => block(x, 19)?.field, "no field opened again under line 19");
      await press(page, 19);
      await untilDiff(page, (x) => !block(x, 19), "a second press did not close the empty field");
    });

    await run("a removed line takes no comment", async () => {
      const spot = await page.evaluate((sel) => {
        const chunk = document.querySelector(`${sel} .cm-deletedChunk`);
        const gutters = document.querySelector(`${sel} .cm-gutters`);
        if (!chunk || !gutters) return null;
        const c = chunk.getBoundingClientRect();
        const g = gutters.getBoundingClientRect();
        return { gutter: [g.left + g.width / 2, c.top + c.height / 2], line: [c.left + 20, c.top + c.height / 2] };
      }, rowOf(FILE));
      assert(spot, "the diff shows no removed line");
      await page.mouse.click(...spot.gutter);
      await page.mouse.click(...spot.line);
      await sleep(400);
      const d = await diff(page, FILE);
      assert(d.blocks.length === 0, `a removed line opened ${JSON.stringify(d.blocks)}`);
    });

    await run("a comment made elsewhere marks its line in the open diff, a field and an open reply keep their words", async () => {
      await press(page, 19);
      await untilDiff(page, (x) => block(x, 19)?.field, "no field opened under line 19");
      await page.click(`${rowOf(FILE)} [data-line-block="19"] [data-line-comment-text]`);
      await page.keyboard.type("half typed");
      await press(page, 20);
      const local = `${rowOf(FILE)} [data-line-block="20"] [data-line-thread]`;
      await page.waitForSelector(`${local} [data-line-thread-reply-open]`, { state: "visible" });
      await page.click(`${local} [data-line-thread-reply-open]`);
      await page.fill(`${local} [data-line-thread-reply-text]`, "kept reply");
      await page.click(`${rowOf(FILE)} [data-line-block="19"] [data-line-comment-text]`);
      await page.evaluate((sel) => { document.querySelector(`${sel} .cm-editor`).dataset.e2eSame = "1"; }, rowOf(FILE));
      const other = await browser.newContext({ ignoreHTTPSErrors: true });
      try {
        const op = await other.newPage();
        await L.login(op);
        await op.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
        ids.elsewhere = (await postJSON(op, `/projects/${REPO}/editor/comments`, { path: FILE, line: 17, text: "From elsewhere" }, { json: true })).comment?.id;
        await postJSON(op, `/projects/${REPO}/editor/comments/reply`, { thread: ids.open, text: "Meanwhile" }, { json: true });
      } finally {
        await other.close();
      }
      const d = await untilDiff(page, (x) => x.marks.includes("17o"), "the comment made elsewhere did not mark its line");
      const same = await page.evaluate((sel) => document.querySelector(`${sel} .cm-editor`)?.dataset.e2eSame === "1", rowOf(FILE));
      assert(same, "the open diff was drawn anew");
      assert(block(d, 19)?.value === "half typed" && block(d, 19).focused, `the field did not keep its words and focus: ${JSON.stringify(block(d, 19))}`);
      const kept = await page.evaluate((sel) => document.querySelector(`${sel} [data-line-thread-reply-text]`)?.value, local);
      assert(kept === "kept reply" && block(d, 20).threads[0].reply, `the open reply reads ${kept}`);
      await page.click(`${local} [data-line-thread-reply-cancel]`);
      await untilDiff(page, (x) => (block(x, 20)?.threads[0]?.notes || []).includes("You: Meanwhile"), "the thread did not follow once its reply closed");
      await page.click(`${rowOf(FILE)} [data-line-block="19"] [data-line-comment-cancel]`);
      await press(page, 20);
      await untilDiff(page, (x) => x.blocks.length === 0, "the field and the thread did not go");
    });

    await run("Uncommitted draws its diff against HEAD with the threads on the file's lines", async () => {
      await page.click(`${rowOf(FILE)} [data-assistant-changed-toggle]`);
      const d = await unfold(page, "uncommitted");
      assert(d.marks === "36o", `the diff against HEAD marks ${d.marks}`);
      assert(JSON.stringify(d.folds) === JSON.stringify(["31 unchanged lines · 7 threads"]), `the folds against HEAD read ${JSON.stringify(d.folds)}`);
      await press(page, 36, { list: "uncommitted" });
      const open = await untilDiff(page, (x) => block(x, 36)?.threads.length === 1, "the thread on line 36 did not unfold against HEAD", FILE, "uncommitted");
      assert(open.blocks[0].threads[0].cid === ids.tail && JSON.stringify(open.blocks[0].threads[0].notes) === JSON.stringify(["You: Near the end"]), `the thread against HEAD reads ${JSON.stringify(open.blocks)}`);
      await page.click(`${rowOf(FILE, "uncommitted")} [data-assistant-changed-toggle]`);
    });

    await run("project comments and threads on other files stay in Reviews alone", async () => {
      const where = await page.evaluate(({ pane, project, other }) => {
        const root = document.querySelector(`${pane} [data-assistant-project-view-body]`);
        const inReviews = (id) => !!root.querySelector(`[data-assistant-project-reviews] [data-line-thread="${id}"]`);
        const inDiffs = (id) => !!root.querySelector(`[data-assistant-diff] [data-line-thread="${id}"]`);
        return { project: inReviews(project), other: inReviews(other), inDiffs: inDiffs(project) || inDiffs(other) };
      }, { pane: PANE, project: ids.project, other: ids.other });
      assert(where.project && where.other && !where.inDiffs, `the project and other file's threads stand ${JSON.stringify(where)}`);
    });

    await run("a guest sees the marks and the threads and writes nothing", async () => {
      const made = await post(page, "/settings/sharing/new", [["amount", "1"], ["unit", "hours"], ["name", `apdc-${TAG}`], ["assistant", owner]]);
      const link = (made.text.match(/value="([^"]*\/share\/[^"]+)"/) || [])[1];
      assert(made.ok && link, `making a link answered ${made.status}`);
      linkId = await page.evaluate(async (name) => {
        const doc = new DOMParser().parseFromString(await (await fetch("/settings/sharing")).text(), "text/html");
        return [...doc.querySelectorAll("[data-share-row]")].find((r) => r.querySelector("[data-share-name]")?.textContent.trim() === name)?.dataset.shareRow || "";
      }, `apdc-${TAG}`);
      assert(linkId, "the link is not listed");
      const base = new URL(link).pathname;
      guest = await guestPage(browser, bag, { requests: [] }, ORIGIN);
      const gp = guest.page;
      await gp.setViewportSize({ width: 1360, height: 900 });
      await openPage(gp, `${BASE}${base}/assistants/${owner}`);
      await openChanges(gp);
      const d = await unfold(gp);
      assert(d.marks === "17o,18r,20o,21x,22r,23o,36o" && d.folds[0] === "16 unchanged lines · 1 thread", `the guest's diff marks ${d.marks} ${JSON.stringify(d.folds)}`);
      await press(gp, 20);
      const open = await untilDiff(gp, (x) => block(x, 20)?.threads.length === 1 && block(x, 20).under, "the guest's thread did not unfold under its line");
      assert(JSON.stringify(open.blocks[0].threads[0].notes) === JSON.stringify(["Owner: Why changed?", "Owner: It had to.", "Owner: Meanwhile"]) && open.blocks[0].threads[0].actions.length === 0, `the guest's thread reads ${JSON.stringify(open.blocks)}`);
      await press(gp, 19);
      await sleep(400);
      const after = await diff(gp, FILE);
      assert(!block(after, 19), `the guest got a field: ${JSON.stringify(after.blocks)}`);
      const controls = await gp.evaluate((pane) => [...document.querySelectorAll(`${pane} [data-assistant-diff] button, ${pane} [data-assistant-diff] textarea, ${pane} [data-assistant-diff] form, ${pane} template`)].length, PANE);
      assert(controls === 0, `the guest's diff carries ${controls} controls`);
    });

    await run("on a phone a tap on a number unfolds a thread and opens a field inside the width, Add makes the comment", async () => {
      const mp = await mobilePage();
      await openPage(mp, `${BASE}/assistants/${owner}`);
      await mp.tap("[data-assistant-watching-button]");
      await mp.waitForSelector(`#assistant-aside.show [data-assistant-project="${REPO}"] [data-assistant-project-changes]`, { state: "visible", timeout: 10000 });
      await mp.tap(`${PANE} [data-assistant-project="${REPO}"] [data-assistant-project-changes]`);
      await mp.waitForSelector(`${rowOf(FILE)} [data-assistant-changed-toggle]`, { state: "visible", timeout: 30000 });
      await unfold(mp, "files", true);
      const plain = await gutterRow(mp, 19);
      const marked = await gutterRow(mp, 22);
      assert(plain && marked && !marked.changed && tinted(marked, plain), `the phone's gutters of line 22 read ${JSON.stringify(marked)} against ${JSON.stringify(plain)}`);
      await press(mp, 22, { touch: true });
      let d = await untilDiff(mp, (x) => block(x, 22)?.threads.length === 1 && block(x, 22).under, "the tap did not unfold the thread under its line");
      assert(block(d, 22).right <= d.width && block(d, 22).under, `the phone's thread reads ${JSON.stringify(block(d, 22))} at ${d.width}`);
      await press(mp, 19, { touch: true });
      d = await untilDiff(mp, (x) => block(x, 19)?.field, "the tap did not open a field");
      assert(!block(d, 19).focused && block(d, 19).right <= d.width, `the phone's field reads ${JSON.stringify(block(d, 19))}`);
      const field = `${rowOf(FILE)} [data-line-block="19"]`;
      await mp.tap(`${field} [data-line-comment-text]`);
      await mp.fill(`${field} [data-line-comment-text]`, "From the phone");
      if (SHOTS_DIR) await mp.screenshot({ path: path.join(SHOTS_DIR, "assistant-projects-diff-comments-phone.png") });
      await mp.tap(`${field} [data-line-comment-add]`);
      d = await untilDiff(mp, (x) => x.marks.includes("19o") && block(x, 19)?.threads.length === 1 && block(x, 19).threads[0].flash, "the phone's comment did not stand marked under its line");
      assert(block(d, 19).threads[0].inView && block(d, 19).right <= d.width, `the phone's new thread reads ${JSON.stringify(block(d, 19))}`);
      const made = (await stored(page)).filter((c) => c.path === FILE && c.line === 19);
      assert(made.length === 1 && made[0].lineText === "app 19" && made[0].text === "From the phone", `the store holds ${JSON.stringify(made)}`);
    });

    await run("a reply in the diff past its cap keeps the caret's line in view, typed, Enter at the end and in the middle, pasted, deleted and set by code, desktop and phone", async () => {
      const thread = `${rowOf(FILE)} [data-line-block="20"] [data-line-thread]`;
      const seen = {};
      for (const touch of [false, true]) {
        const where = touch ? "phone" : "desktop";
        const p = touch ? await mobilePage() : page;
        if (!touch) await page.setViewportSize({ width: 1360, height: 900 });
        await openPage(p, `${BASE}/assistants/${owner}`);
        if (touch) {
          await p.tap("[data-assistant-watching-button]");
          await p.waitForSelector(`#assistant-aside.show [data-assistant-project="${REPO}"] [data-assistant-project-changes]`, { state: "visible", timeout: 10000 });
          await p.tap(`${PANE} [data-assistant-project="${REPO}"] [data-assistant-project-changes]`);
          await p.waitForSelector(`${rowOf(FILE)} [data-assistant-changed-toggle]`, { state: "visible", timeout: 30000 });
        } else {
          await openChanges(p);
        }
        await unfold(p, "files", touch);
        await press(p, 20, { touch });
        await p.waitForSelector(`${thread} [data-line-thread-reply-open]`, { state: "visible", timeout: 10000 });
        if (touch) await p.tap(`${thread} [data-line-thread-reply-open]`);
        else await p.click(`${thread} [data-line-thread-reply-open]`);
        const walk = await L.caretWalk(p, `${thread} [data-line-thread-reply-text]`);
        assert(walk.hidden.length === 0, `${where}: ${walk.hidden.length} of ${walk.steps} steps hid the caret: ${walk.hidden.slice(0, 4).join("; ")}`);
        await p.click(`${thread} [data-line-thread-reply-cancel]`);
        await untilDiff(p, (x) => block(x, 20)?.threads[0] && !block(x, 20).threads[0].reply, `${where}: Cancel did not close the reply`);
        await press(p, 20, { touch });
        await untilDiff(p, (x) => !block(x, 20), `${where}: a second press did not fold the thread`);
        seen[where] = `${walk.steps} steps, ${walk.lines} lines typed, pasted by ${walk.pasted}`;
      }
      return JSON.stringify(seen);
    });

    await run("an open thread of long unbroken text, its reply and a field keep the diff's width, desktop and phone, both schemes", async () => {
      const long = `app 30 ${"w".repeat(320)}`;
      fs.writeFileSync(path.join(repoDir, FILE), fs.readFileSync(path.join(repoDir, FILE), "utf8").replace("app 30\n", `${long}\n`));
      ids.wide = await comment(page, 30, `${"n".repeat(400)} https://example.test/${"segment/".repeat(60)}`, long);
      assert(ids.wide, "the long comment was not made");
      // fits reads the diff's width with the thread's end and the field in
      // view, the diff draws only the lines near what shows.
      const fits = (p) => p.evaluate(async (sel) => {
        const row = document.querySelector(sel);
        row.querySelector('[data-line-block="30"]').scrollIntoView({ block: "end" });
        for (let i = 0; i < 20 && !row.querySelector('[data-line-block="31"] textarea'); i += 1) await new Promise((r) => requestAnimationFrame(r));
        const scroller = row.querySelector(".cm-scroller");
        const box = scroller.getBoundingClientRect();
        const parts = [...row.querySelectorAll("[data-line-block] :is([data-line-thread-quote], [data-comment-note-text], textarea, [data-line-thread-actions])")];
        const page = document.scrollingElement;
        let aside = scroller.parentElement;
        while (aside && !["auto", "scroll"].includes(getComputedStyle(aside).overflowY)) aside = aside.parentElement;
        return {
          scroll: scroller.scrollWidth,
          width: scroller.clientWidth,
          parts: parts.length,
          field: !!row.querySelector('[data-line-block="31"] [data-line-comment-text]'),
          outside: parts.filter((el) => el.getBoundingClientRect().right > box.right + 1 || el.scrollWidth > el.clientWidth + 1).map((el) => el.outerHTML.slice(0, 80)),
          aside: aside ? aside.scrollWidth - aside.clientWidth : 0,
          page: page.scrollWidth - page.clientWidth,
        };
      }, rowOf(FILE));
      const open = async (p, touch) => {
        await openPage(p, `${BASE}/assistants/${owner}`);
        if (touch) {
          await p.tap("[data-assistant-watching-button]");
          await p.waitForSelector(`#assistant-aside.show [data-assistant-project="${REPO}"] [data-assistant-project-changes]`, { state: "visible", timeout: 10000 });
          await p.tap(`${PANE} [data-assistant-project="${REPO}"] [data-assistant-project-changes]`);
          await p.waitForSelector(`${rowOf(FILE)} [data-assistant-changed-toggle]`, { state: "visible", timeout: 30000 });
        } else {
          await openChanges(p);
        }
        await unfold(p, "files", touch);
        await untilDiff(p, (x) => x.marks.includes("30o"), "the long comment did not mark its line", FILE, "files");
        await press(p, 31, { touch });
        await untilDiff(p, (x) => block(x, 31)?.field, "no field opened under line 31");
        await press(p, 30, { touch });
        await untilDiff(p, (x) => block(x, 30)?.threads.length === 1, "the long thread did not unfold under its line");
        const reply = `${rowOf(FILE)} [data-line-block="30"] [data-line-thread-reply-open]`;
        if (touch) await p.tap(reply);
        else await p.click(reply);
        await untilDiff(p, (x) => block(x, 30)?.threads[0]?.reply, "the reply did not open");
      };
      await page.setViewportSize({ width: 1360, height: 900 });
      await open(page, false);
      for (const scheme of ["light", "dark"]) {
        await page.emulateMedia({ colorScheme: scheme });
        await page.waitForFunction(({ sel, dark }) => (getComputedStyle(document.querySelector(`${sel} .cm-editor`)).backgroundColor === "rgb(40, 44, 52)") === dark, { sel: rowOf(FILE), dark: scheme === "dark" }, { timeout: 15000 });
        const w = await fits(page);
        assert(w.parts >= 5 && w.field && w.scroll <= w.width && !w.outside.length && w.aside <= 0 && w.page <= 0, `the desktop diff in ${scheme} reads ${JSON.stringify(w)}`);
      }
      if (SHOTS_DIR) await page.screenshot({ path: path.join(SHOTS_DIR, "assistant-projects-diff-comments-wide.png") });
      await page.emulateMedia({ colorScheme: "light" });
      const mp = await mobilePage();
      for (const scheme of ["dark", "light"]) {
        await mp.emulateMedia({ colorScheme: scheme });
        await open(mp, true);
        const w = await fits(mp);
        assert(w.parts >= 5 && w.field && w.scroll <= w.width && !w.outside.length && w.aside <= 0 && w.page <= 0, `the phone's diff in ${scheme} reads ${JSON.stringify(w)}`);
        if (SHOTS_DIR && scheme === "dark") await mp.screenshot({ path: path.join(SHOTS_DIR, "assistant-projects-diff-comments-wide-phone.png") });
      }
      return `${(await fits(page)).width}px`;
    });

    await run("the bin in the diff deletes a reply and a thread as the editor does, the diff and Reviews follow, a field elsewhere keeps its words", async () => {
      const DIALOG = ".swal2-popup:not(.swal2-toast)";
      await page.setViewportSize({ width: 1360, height: 900 });
      await page.emulateMedia({ colorScheme: "light" });
      await openPage(page, `${BASE}/assistants/${owner}`);
      await page.evaluate(() => { window.__noReload = 1; });
      await openChanges(page);
      await unfold(page);
      await press(page, 24);
      await untilDiff(page, (x) => block(x, 24)?.field, "no field opened under line 24");
      await page.fill(`${rowOf(FILE)} [data-line-block="24"] [data-line-comment-text]`, "stays");
      await press(page, 22);
      await press(page, 20);
      let d = await untilDiff(page, (x) => block(x, 20)?.threads.length === 1 && block(x, 22)?.threads.length === 1, "the threads on 20 and 22 did not unfold");
      const local = block(d, 20).threads[0];
      const forge = block(d, 22).threads[0];
      assert(JSON.stringify(local.actions) === JSON.stringify(["Reply", "Hand off", "Resolve"]) && local.bin && JSON.stringify(local.drops) === JSON.stringify(["It had to.", "Meanwhile"]), `the local thread in the diff offers ${JSON.stringify(local)}`);
      assert(!forge.bin && !forge.drops.length, `the forge thread in the diff offers ${JSON.stringify(forge)}`);
      assert(even(local) && local.inset.ends.length === 3, `the bins in the diff stand in from the right unlike the file icon from the left: ${JSON.stringify(local.inset)}`);
      const copy = `${rowOf(FILE)} [data-line-block="20"] [data-line-thread]`;
      const stands = (id) => page.evaluate((id) => !!document.querySelector(`[data-assistant-project-reviews] [data-line-thread="${id}"]`), ids.open);
      await page.locator(`${copy} [data-comment-note]`).filter({ hasText: "Meanwhile" }).locator("[data-comment-note-drop]").click();
      await page.waitForSelector(DIALOG, { state: "visible", timeout: 5000 });
      let asked = (await page.locator(DIALOG).textContent()).replace(/\s+/g, " ");
      assert(asked.includes("Delete this reply?") && asked.includes(`${FILE}:20`) && asked.includes("Meanwhile"), `the reply's confirm reads ${asked}`);
      await page.click(`${DIALOG} .swal2-confirm`);
      d = await untilDiff(page, (x) => JSON.stringify(block(x, 20)?.threads[0]?.notes) === JSON.stringify(["You: Why changed?", "You: It had to."]), "the reply did not go from the diff");
      const card = await page.evaluate((id) => [...document.querySelectorAll(`[data-assistant-project-reviews] [data-line-thread="${id}"] [data-comment-note-text]`)].map((n) => n.textContent), ids.open);
      assert(JSON.stringify(card) === JSON.stringify(["Why changed?", "It had to."]), `the card in Reviews holds ${JSON.stringify(card)}`);
      assert(JSON.stringify((await stored(page)).find((c) => c.id === ids.open)?.replies?.map((r) => r.text)) === JSON.stringify(["It had to."]), "the store still holds the deleted reply");
      assert(block(d, 24)?.value === "stays", `the field lost its words: ${JSON.stringify(block(d, 24))}`);
      await page.click(`${copy} [data-line-thread-delete]`);
      await page.waitForSelector(DIALOG, { state: "visible", timeout: 5000 });
      asked = (await page.locator(DIALOG).textContent()).replace(/\s+/g, " ");
      assert(asked.includes("Delete this comment?") && asked.includes("Why changed?") && asked.includes("It takes its reply along."), `the thread's confirm reads ${asked}`);
      await page.click(`${DIALOG} .swal2-confirm`);
      d = await untilDiff(page, (x) => !x.marks.split(",").includes("20o") && !block(x, 20), "the thread did not go from the diff");
      assert(!(await stands(ids.open)) && !(await stored(page)).some((c) => c.id === ids.open), "the thread stands in Reviews or the store");
      assert(block(d, 24)?.value === "stays" && block(d, 22)?.threads.length === 1, `the field or the forge thread went: ${JSON.stringify(d.blocks)}`);
      assert(await page.evaluate(() => window.__noReload === 1), "the page reloaded");
      const mp = await mobilePage();
      await openPage(mp, `${BASE}/assistants/${owner}`);
      await mp.tap("[data-assistant-watching-button]");
      await mp.waitForSelector(`#assistant-aside.show [data-assistant-project="${REPO}"] [data-assistant-project-changes]`, { state: "visible", timeout: 10000 });
      await mp.tap(`${PANE} [data-assistant-project="${REPO}"] [data-assistant-project-changes]`);
      await mp.waitForSelector(`${rowOf(FILE)} [data-assistant-changed-toggle]`, { state: "visible", timeout: 30000 });
      await unfold(mp, "files", true);
      await press(mp, 23, { touch: true });
      d = await untilDiff(mp, (x) => block(x, 23)?.threads.length === 1, "the tap did not unfold the thread on 23");
      assert(block(d, 23).threads[0].bin && JSON.stringify(block(d, 23).threads[0].actions) === JSON.stringify(["Reply", "Hand off", "Resolve"]) && block(d, 23).threads[0].oneLine && even(block(d, 23).threads[0]) && block(d, 23).right <= d.width, `the phone's thread reads ${JSON.stringify(block(d, 23))}`);
      const bin = `${rowOf(FILE)} [data-line-block="23"] [data-line-thread-delete]`;
      const fits = await mp.evaluate((sel) => { const r = document.querySelector(sel).getBoundingClientRect(); return { right: r.right, width: r.width, inner: window.innerWidth }; }, bin);
      assert(fits.right <= fits.inner && fits.width >= 24, `the phone's bin reads ${JSON.stringify(fits)}`);
      await mp.tap(bin);
      await mp.waitForSelector(DIALOG, { state: "visible", timeout: 5000 });
      await mp.tap(`${DIALOG} .swal2-confirm`);
      await untilDiff(mp, (x) => !x.marks.split(",").includes("23o") && !block(x, 23), "the phone's thread did not go from the diff");
      assert(!(await stored(page)).some((c) => c.id === ids.added), "the store still holds the phone's thread");
    });
    await editChecks({ page, run, mobilePage, owner, REPO, FILE, PANE, openPage, openChanges, unfold, press, untilDiff, block, rowOf, stored, comment, postJSON });
  } finally {
    if (guest) await guest.ctx.close().catch(() => {});
    if (linkId) await post(page, `/settings/sharing/${linkId}`, { form: "revoke" }).catch(() => {});
    for (const id of coders) await post(page, `/coders/${id}/delete`, {}).catch(() => {});
    if (owner) await post(page, `/assistants/${owner}`, { form: "delete" }).catch(() => {});
    if (hostEdit) await post(page, hostEdit, { form: "delete" }).catch(() => {});
    await L.deleteProject(page, REPO).catch(() => {});
  }
});
