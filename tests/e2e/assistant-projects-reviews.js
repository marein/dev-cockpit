const L = require("./lib");
const { assert, BASE, sleep } = L;
const fs = require("fs");
const path = require("path");
const { execFileSync } = require("child_process");
const { guestPage } = require("./project-share-guest");
const { startFakeForge, addHost, gitlabNote, gitlabDiscussion } = require("./forge-fake");

// A project opened in the aside of an assistant (GET
// /assistants/projects/view), chromium, against the fake GitLab: the review
// line of a card opens the project in place of the cards, once on a double
// tap and alone while the cards pull again, Back returns to them where they
// stood. Reviews lists the line comment threads of the
// project, the review threads of the branch's merge request marked GitLab and
// the local ones marked local, each with its file and line opening the editor
// there, the quoted code line, the notes with author and age (the one
// rendering of a thread's notes, line_comment_notes.gohtml), outdated once the
// file on disk lost the quoted line, the resolved ones folded under a count,
// and the line comments a publish would post with Publish as review. On a
// thread of the forge Reply (growing with its text up to 35% of the window,
// then scrolling, a refusal keeps the words, Enter starts a line,
// Ctrl+Enter sends and a second one while the first is on the wire sends
// nothing) and Resolve write through the forge thread route, on a local one
// Reply (sent with Cmd+Enter), Resolve and Reopen through the editor's line
// comment routes, Publish as review through the
// publish route, and the view follows the forge, the line comments and git
// live, keeping an open fold. Hand off puts a line naming the thread, the
// project, the branch, the file and the line under what the message box
// holds, sends nothing, and a reload keeps it. On a phone the sheet opens the
// view and Hand off closes the sheet. A project bound to no forge opens on its
// local threads from its card, and on its resolved ones once none is open. A
// guest of a link to the assistant reads the
// view without a control and without a file link into a project the link
// does not grant. A local thread and each of its replies carry the editor's
// bin (POST .../editor/comments/delete and .../comments/reply/delete behind
// the editor's confirm), a thread of the forge none: the thread's in its head
// beside the badge, a reply's on its line, each as far in from the right
// edge as the file icon from the left, quiet at rest, the danger colour
// hovered and focused from the keyboard in both schemes, a cancel deleting
// nothing, what went leaving Reviews and the card's count live while a reply
// typed in another thread keeps its words, and on a phone Reply, Hand off and
// Resolve on one line and a tap on the bin and its confirm deleting the
// thread.
//
// Instance like assistant-projects-forge.js's: tests/e2e/fakes first on PATH,
// a scratch HOME, its --projects-dir mounted into the runner at the same path,
// the fake forge's ports FORGE_PORT and FORGE_TLS_PORT (default 3157 and 3158)
// published on the host's loopback, where the instance reaches them at
// FORGE_ADDR (default 127.0.0.1). SHOTS_DIR with a mount takes a desktop and
// a phone screenshot. About a minute.
const TAG = Date.now().toString(36).slice(-5);
const SHOTS_DIR = process.env.SHOTS_DIR || "";
const FAKE_PORT = Number(process.env.FORGE_PORT || 3157);
const FAKE_TLS_PORT = Number(process.env.FORGE_TLS_PORT || 3158);
const FAKE_ADDR = process.env.FORGE_ADDR || "127.0.0.1";
const TOKEN = `glpat-e2e${Date.now().toString(36)}Rv3kQ8sW`;
const ORIGIN = new URL(BASE).origin;
const REPO = `aprev${TAG}`;
const OTHERS = [1, 2].map((n) => `aprevother${n}${TAG}`);
const PLAIN = OTHERS[0];
const SPARE = `aprevspare${TAG}`;
const NOTE = "notes.md";
const FILE = "src/login.go";
const CONTENT = "package login\n\nfunc login() {\n\tname := bb\n\tc := 3\n\td := 4\n\treturn\n}\n";
const PATCH = "@@ -1,8 +1,8 @@\n package login\n \n func login() {\n-\tname := b\n+\tname := bb\n \tc := 3\n \td := 4\n \treturn\n }";
const PANE = "#assistant-projects";
const DIALOG = ".swal2-popup:not(.swal2-toast)";

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

async function newCoder(page, dir, name) {
  const made = await postJSON(page, "/coders/new", { name, project: dir, coder: "claude", automatic_approval: "on", prompt: `job-${name}` });
  assert(made.id, `no coder made: ${JSON.stringify(made)}`);
  return made.id;
}

const steer = (page, owner, terminal) => postJSON(page, "/assistants/jobs", { form: "steer", assistant: owner, terminal, task: "Review check", done_when: "" });
const release = (page, terminal) => postJSON(page, "/assistants/jobs", { form: "release", terminal });

async function openPage(p, url) {
  await p.goto(url, { waitUntil: "domcontentloaded" });
  await L.dismissUpdate(p);
  await p.waitForSelector(`${PANE} [data-assistant-body]`, { state: "attached", timeout: 15000 });
}

// view reads the tab as it stands on the screen: whether the cards or the
// project show, and each thread of the project by what it shows.
const view = (p) => p.evaluate((pane) => {
  const shown = (el) => !!el && el.getClientRects().length > 0 && getComputedStyle(el).visibility !== "hidden";
  const text = (el) => (shown(el) ? el.textContent.replace(/\s+/g, " ").trim() : "");
  const root = document.querySelector(`${pane} [data-assistant-project-view-body]`);
  const thread = (el) => ({
    id: el.dataset.thread || "",
    cid: el.dataset.assistantThread || "",
    local: el.hasAttribute("data-assistant-thread-local"),
    origin: text(el.querySelector("[data-assistant-thread-origin]")),
    shown: shown(el),
    file: text(el.querySelector("[data-assistant-thread-file]")),
    href: el.querySelector("[data-assistant-thread-file]")?.getAttribute("href") || "",
    quote: text(el.querySelector("[data-assistant-thread-quote]")),
    outdated: shown(el.querySelector("[data-assistant-thread-outdated]")),
    notes: [...el.querySelectorAll("[data-comment-note]")].filter(shown).map((n) => `${text(n.querySelector("[data-comment-note-author]"))}: ${text(n.querySelector("[data-comment-note-text]"))}`),
    when: text(el.querySelector("[data-comment-note] dc-time")),
    actions: [...el.querySelectorAll("[data-assistant-thread-actions] button")].filter(shown).map((b) => text(b) || b.getAttribute("aria-label")),
    bin: (() => { const b = el.querySelector("[data-assistant-thread-head] [data-assistant-thread-delete]"); const o = el.querySelector("[data-assistant-thread-head] [data-assistant-thread-origin]"); return shown(b) && !!o && b.getBoundingClientRect().left >= o.getBoundingClientRect().right; })(),
    oneLine: new Set([...el.querySelectorAll("[data-assistant-thread-actions] button")].filter(shown).map((b) => Math.round(b.getBoundingClientRect().top))).size <= 1,
    drops: [...el.querySelectorAll("[data-comment-note]")].filter((n) => shown(n.querySelector("[data-comment-note-drop]"))).map((n) => text(n.querySelector("[data-comment-note-text]"))),
    inset: (() => {
      const card = el.getBoundingClientRect();
      const icon = el.querySelector("[data-assistant-thread-head] > .ti");
      const bins = [...el.querySelectorAll("[data-assistant-thread-delete] .ti, [data-comment-note-drop] .ti")].filter(shown);
      return { start: icon ? Math.round(icon.getBoundingClientRect().left - card.left) : null, ends: bins.map((b) => Math.round(card.right - b.getBoundingClientRect().right)) };
    })(),
    right: el.getBoundingClientRect().right,
  });
  return {
    open: shown(root),
    cards: shown(document.querySelector(`${pane} > [data-assistant-body]`)),
    name: root?.dataset.assistantProjectViewBody || "",
    head: text(root?.querySelector("[data-assistant-project-view-card]")),
    count: text(root?.querySelector("[data-assistant-project-reviews-count]")),
    unpublished: text(root?.querySelector("[data-assistant-project-unpublished-box]")),
    threads: [...(root?.querySelectorAll("[data-assistant-project-threads-open] [data-assistant-thread]") || [])].map(thread),
    resolvedToggle: shown(root?.querySelector("[data-assistant-project-resolved-toggle]")) ? root.querySelector("[data-assistant-project-resolved-toggle]").innerText.replace(/\s+/g, " ").trim() : "",
    resolvedOpen: root?.querySelector("[data-assistant-project-resolved-toggle]")?.getAttribute("aria-expanded") || "",
    resolved: [...(root?.querySelectorAll("[data-assistant-project-threads-resolved] [data-assistant-thread]") || [])].map(thread),
    controls: root ? [...root.querySelectorAll("button, form, textarea, input")].filter((el) => !el.matches("[data-assistant-project-back], [data-assistant-project-switch], [data-assistant-project-resolved-toggle], [data-assistant-changed-toggle], [data-assistant-changed-more], [data-assistant-changes-older]")).length : 0,
    width: window.innerWidth,
  };
}, PANE);

async function until(p, want, what, timeout = 30000) {
  const end = Date.now() + timeout;
  let last = null;
  while (Date.now() < end) {
    last = await view(p);
    if (want(last)) return last;
    await sleep(250);
  }
  throw new Error(`${what}: ${JSON.stringify(last)}`);
}

const byId = (v, id) => v.threads.find((t) => t.id === id) || v.resolved.find((t) => t.id === id);
const byCid = (v, cid) => v.threads.find((t) => t.cid === cid) || v.resolved.find((t) => t.cid === cid);
const threadSel = (id) => `${PANE} [data-assistant-thread][data-thread="${id}"]`;
const localSel = (cid) => `${PANE} [data-assistant-thread="${cid}"][data-assistant-thread-local]`;
const notesOf = (t) => JSON.stringify(t?.notes || []);
// even says every bin of a thread stands as far in from its right edge as its
// file icon from the left.
const even = (t) => !!t && t.inset.start !== null && t.inset.ends.length > 0 && t.inset.ends.every((end) => Math.abs(end - t.inset.start) <= 1);
const grown = (p, sel) => p.evaluate((sel) => {
  const el = document.querySelector(sel);
  const cs = getComputedStyle(el);
  return { height: Math.round(el.getBoundingClientRect().height), cap: Math.round(window.innerHeight * 0.35), overflow: cs.overflowY, resize: cs.resize, scroll: el.scrollHeight, client: el.clientHeight };
}, sel);
const toast = (p, words) => p.waitForFunction((w) => [...document.querySelectorAll(".dc-toast")].some((el) => el.textContent.includes(w)), words, { timeout: 15000 });

async function openReviews(p, name = REPO) {
  await p.click(`${PANE} [data-assistant-project="${name}"] [data-assistant-project-review]`);
  return until(p, (v) => v.open && !v.cards && v.name === name, "the project did not open");
}

L.runFeature("assistant-projects-reviews", async ({ browser, page, run, mobilePage, bag }) => {
  const fake = await startFakeForge({ port: FAKE_PORT, tlsPort: FAKE_TLS_PORT, token: TOKEN });
  const state = fake.state;
  const fakeWeb = `http://${FAKE_ADDR}:${FAKE_PORT}`;
  const coders = [];
  let owner = "";
  let repoDir = "";
  let hostEdit = "";
  let linkId = "";
  let guest = null;
  let mineId = "";
  let localId = "";
  let plainId = "";
  const handoff = (id, line) => `Review thread ${id} in project ${REPO} on branch feature at ${FILE}:${line}.`;
  const localHandoff = (id, line) => `Line comment ${id} in project ${REPO} on branch feature at ${FILE}:${line}.`;
  try {
    await run("setup: a repository on the fake GitLab with review threads, local threads and cards around it", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      owner = (await postJSON(page, "/assistants/new", { form: "new", coder: "claude" })).id;
      assert(owner, "no assistant made");
      for (const name of [REPO, ...OTHERS]) await L.createProject(page, name);
      repoDir = await L.projectPath(page, REPO);
      assert(fs.existsSync(repoDir), `the projects dir is not mounted at ${repoDir}`);
      git(repoDir, "init", "-q", "-b", "main");
      git(repoDir, "commit", "-q", "--allow-empty", "-m", "first");
      git(repoDir, "switch", "-q", "-c", "feature");
      fs.mkdirSync(path.join(repoDir, "src"));
      fs.writeFileSync(path.join(repoDir, FILE), CONTENT);
      git(repoDir, "add", "-A");
      git(repoDir, "commit", "-q", "-m", "login");
      const pushed = git(repoDir, "rev-parse", "HEAD").trim();
      git(repoDir, "update-ref", "refs/remotes/origin/feature", pushed);
      git(repoDir, "remote", "add", "origin", `${fakeWeb}/gl/team/sub/app.git`);
      Object.assign(state, { head: pushed, status: "success", diffFiles: [{ path: FILE, patch: PATCH }], published: [], replies: [], resolved: [] });
      state.discussions = (s) => [
        gitlabDiscussion("d1rv", false, [gitlabNote(41, "root", "Rename it?", { resolvable: true, at: { head: s.head, path: FILE, newLine: 4 } })]),
        gitlabDiscussion("d2rv", false, [gitlabNote(42, "alice", "Why 3?", { resolvable: true, at: { head: s.head, path: FILE, newLine: 5 } })]),
        gitlabDiscussion("d3rv", false, [gitlabNote(43, "root", "Done.", { resolvable: true, resolved: true, at: { head: s.head, path: FILE, newLine: 6 } })]),
      ];
      const added = await addHost(page, { kind: "gitlab", web_url: `${fakeWeb}/gl`, aliases: `aprv-${TAG}`, token: TOKEN });
      assert(!added.failed, added.failed);
      hostEdit = await page.evaluate(async (mark) => {
        const html = await (await fetch("/settings/forges")).text();
        const doc = new DOMParser().parseFromString(html, "text/html");
        return [...doc.querySelectorAll("[data-forge-host]")].find((r) => r.textContent.includes(mark))?.querySelector("[data-forge-edit]")?.getAttribute("href") || "";
      }, `aprv-${TAG}`);
      assert(hostEdit, "the added host has no Edit on the list");
      mineId = (await postJSON(page, `/projects/${REPO}/editor/comments`, { path: FILE, line: 4, text: "Mine to publish" }, { json: true })).comment?.id;
      localId = (await postJSON(page, `/projects/${REPO}/editor/comments`, { path: FILE, line: 6, text: "Keep d?" }, { json: true })).comment?.id;
      assert(mineId && localId, "the line comments were not made");
      await postJSON(page, `/projects/${REPO}/editor/comments/reply`, { thread: localId, text: "Yes." }, { json: true });
      fs.writeFileSync(path.join(await L.projectPath(page, PLAIN), NOTE), "x\n");
      plainId = (await postJSON(page, `/projects/${PLAIN}/editor/comments`, { path: NOTE, line: 1, text: "Check this" }, { json: true })).comment?.id;
      assert(plainId, "the line comment without a forge was not made");
      const work = await newCoder(page, repoDir, `aprv-work-${TAG}`);
      coders.push(work);
      await steer(page, owner, work);
      await release(page, work);
      for (const name of OTHERS) {
        const coder = await newCoder(page, await L.projectPath(page, name), `aprv-${name}`);
        coders.push(coder);
        await steer(page, owner, coder);
      }
    });

    await run("the review line opens the project with its threads once on a double tap, Back returns to the cards where they stood", async () => {
      await page.setViewportSize({ width: 1360, height: 400 });
      await openPage(page, `${BASE}/assistants/${owner}`);
      await page.waitForFunction(({ pane, name }) => /4 open threads · 1 unpublished comment/.test(document.querySelector(`${pane} [data-assistant-project="${name}"] [data-assistant-project-review]`)?.textContent || ""), { pane: PANE, name: REPO }, { timeout: 30000 });
      await page.evaluate(() => { window.__noReload = 1; });
      const scroller = () => page.evaluate(() => { const a = document.querySelector("#assistant-aside .dc-assistant-aside-body"); return { top: a.scrollTop, max: a.scrollHeight - a.clientHeight }; });
      await page.evaluate(() => { const a = document.querySelector("#assistant-aside .dc-assistant-aside-body"); a.scrollTop = a.scrollHeight; });
      const before = await scroller();
      assert(before.max > 40 && Math.abs(before.top - before.max) <= 1, `the aside does not scroll: ${JSON.stringify(before)}`);
      await page.dblclick(`${PANE} [data-assistant-project="${REPO}"] [data-assistant-project-review]`);
      const v = await until(page, (w) => w.open && !w.cards && w.name === REPO, "the project did not open");
      assert((await scroller()).top === 0, "the project does not open at its top");
      assert(v.count === "4" && v.head.includes(REPO) && v.head.includes("!4 Fix the login"), `the view's head reads ${JSON.stringify(v)}`);
      const d1 = byId(v, "d1rv");
      const d2 = byId(v, "d2rv");
      assert(d1 && d1.shown && d1.file === `${FILE}:4` && d1.href === `/projects/${REPO}/editor?file=${encodeURIComponent(FILE)}&line=4` && d1.quote === "name := bb" && !d1.local && d1.origin === "GitLab", `the first thread reads ${JSON.stringify(d1)}`);
      assert(JSON.stringify(d1.notes) === JSON.stringify(["root: Rename it?"]) && /ago$/.test(d1.when) && !d1.outdated, `the first thread's notes read ${JSON.stringify(d1)}`);
      assert(JSON.stringify(d1.actions) === JSON.stringify(["Reply", "Hand off", "Resolve"]), `the first thread offers ${JSON.stringify(d1.actions)}`);
      assert(d2 && d2.quote === "c := 3" && JSON.stringify(d2.notes) === JSON.stringify(["alice: Why 3?"]), `the second thread reads ${JSON.stringify(d2)}`);
      const mine = byCid(v, mineId);
      const local = byCid(v, localId);
      assert(mine && mine.shown && mine.local && mine.origin === "local" && !mine.id && mine.file === `${FILE}:4` && mine.quote === "name := bb" && notesOf(mine) === JSON.stringify(["You: Mine to publish"]), `the local comment reads ${JSON.stringify(mine)}`);
      assert(local && local.shown && local.local && local.file === `${FILE}:6` && local.quote === "d := 4" && notesOf(local) === JSON.stringify(["You: Keep d?", "You: Yes."]) && /^just now$|ago$/.test(local.when), `the local thread reads ${JSON.stringify(local)}`);
      assert(JSON.stringify(local.actions) === JSON.stringify(["Reply", "Hand off", "Resolve"]) && local.bin && JSON.stringify(local.drops) === JSON.stringify(["Yes."]), `the local thread offers ${JSON.stringify(local)}`);
      assert(v.threads.filter((t) => !t.local).every((t) => !t.bin && !t.drops.length), `a thread of the forge offers a delete: ${JSON.stringify(v.threads)}`);
      assert(v.resolvedToggle === "Show 1 resolved thread" && v.resolvedOpen === "false" && v.resolved.length === 1 && !v.resolved[0].shown, `the resolved thread does not fold: ${v.resolvedToggle} ${JSON.stringify(v.resolved)}`);
      assert(/^1 line comment not published Publish as review$/.test(v.unpublished), `the unpublished line reads ${v.unpublished}`);
      if (SHOTS_DIR) await page.screenshot({ path: path.join(SHOTS_DIR, "assistant-projects-reviews-desktop.png") });
      await page.click(`${PANE} [data-assistant-project-resolved-toggle]`);
      const unfolded = await until(page, (w) => w.resolved[0]?.shown && JSON.stringify(w.resolved[0].notes) === JSON.stringify(["root: Done."]) && w.resolved[0].actions.join() === "Reply,Hand off", "the resolved thread does not unfold");
      assert(unfolded.resolvedToggle === "Hide resolved threads" && unfolded.resolvedOpen === "true", `the unfolded resolved threads' control reads ${unfolded.resolvedToggle} ${unfolded.resolvedOpen}`);
      await page.click(`${PANE} [data-assistant-project-back]`);
      const back = await until(page, (w) => !w.open && w.cards, "Back did not return to the cards");
      const after = await scroller();
      assert(Math.abs(after.top - before.top) <= 1 && !back.name, `the cards stand at ${JSON.stringify(after)}, were at ${JSON.stringify(before)}`);
      assert(await page.evaluate(() => window.__noReload === 1), "the page reloaded");
      return `${v.threads.length} open, ${v.resolvedToggle}`;
    });

    await run("a card opened while the cards pull again stands alone, the cards stay hidden", async () => {
      await page.setViewportSize({ width: 1360, height: 900 });
      await openPage(page, `${BASE}/assistants/${owner}`);
      await page.waitForSelector(`${PANE} [data-assistant-project="${REPO}"] [data-assistant-project-review]`, { state: "visible", timeout: 15000 });
      await page.evaluate(() => { window.__noReload = 1; });
      let release = () => {};
      const held = new Promise((resolve) => { release = resolve; });
      const viewPull = (u) => u.pathname === "/assistants/projects/view";
      await page.route(viewPull, async (route) => {
        await held;
        await route.continue();
      });
      const other = await page.context().newPage();
      try {
        await page.click(`${PANE} [data-assistant-project="${REPO}"] [data-assistant-project-review]`);
        const pulled = page.waitForResponse((r) => new URL(r.url()).pathname === "/assistants/projects", { timeout: 20000 });
        await L.createProject(other, SPARE);
        await pulled;
        release();
        const v = await until(page, (w) => w.open && w.name === REPO && w.threads.length > 0, "the project did not open");
        assert(!v.cards, "the cards stand beside the open project");
      } finally {
        release();
        await page.unroute(viewPull);
        await L.deleteProject(other, SPARE).catch(() => {});
        await other.close();
      }
      await page.click(`${PANE} [data-assistant-project-back]`);
      await until(page, (w) => !w.open && w.cards, "Back did not return to the cards");
      assert(await page.evaluate(() => window.__noReload === 1), "the page reloaded");
    });

    await run("an empty or refused reply keeps the form and its words, Enter starts a line, Ctrl+Enter sends once, the reply shows up live", async () => {
      await page.setViewportSize({ width: 1360, height: 900 });
      await openReviews(page);
      await page.evaluate(() => { window.__noReload = 1; });
      await page.click(`${threadSel("d1rv")} [data-assistant-thread-reply-open]`);
      const form = `${threadSel("d1rv")} [data-assistant-thread-reply]`;
      await page.waitForSelector(`${form} textarea`, { state: "visible", timeout: 5000 });
      assert(await page.evaluate((sel) => document.activeElement === document.querySelector(`${sel} textarea`), form), "the reply field has no focus");
      await page.click(`${form} [data-assistant-thread-reply-send]`);
      await page.waitForSelector(`${form} .invalid-feedback`, { state: "visible", timeout: 5000 });
      assert(state.replies.length === 0, "an empty reply was posted");
      const rest = await grown(page, `${form} textarea`);
      await page.fill(`${form} textarea`, Array.from({ length: 8 }, (_, i) => `Row ${i + 1}`).join("\n"));
      let g = await grown(page, `${form} textarea`);
      assert(rest.resize === "none" && g.height > rest.height + 40 && g.height < g.cap && g.overflow === "hidden", `eight rows of the reply read ${JSON.stringify(g)} from ${JSON.stringify(rest)}`);
      await page.fill(`${form} textarea`, Array.from({ length: 80 }, (_, i) => `Row ${i + 1}`).join("\n"));
      g = await grown(page, `${form} textarea`);
      assert(Math.abs(g.height - g.cap) <= 1 && g.overflow === "auto" && g.scroll > g.client, `eighty rows of the reply read ${JSON.stringify(g)}`);
      state.readOnly = true;
      await page.fill(`${form} textarea`, "Renamed in abc.");
      await page.click(`${form} [data-assistant-thread-reply-send]`);
      await toast(page, "The token may not write to GitLab: give it the scope api.");
      assert(await page.locator(`${form} textarea`).inputValue() === "Renamed in abc." && await page.locator(`${form} textarea`).isVisible(), "a refused reply lost its words");
      state.readOnly = false;
      await page.press(`${form} textarea`, "End");
      await page.press(`${form} textarea`, "Enter");
      await sleep(300);
      assert(await page.locator(`${form} textarea`).inputValue() === "Renamed in abc.\n" && state.replies.length === 0, "Enter sent the reply instead of starting a line");
      const posts = [];
      let release = () => {};
      const held = new Promise((resolve) => { release = resolve; });
      await page.route("**/forge/thread", async (route) => {
        posts.push(route.request().postData() || "");
        await held;
        await route.continue();
      });
      await page.press(`${form} textarea`, "Control+Enter");
      await page.waitForFunction((sel) => document.querySelector(`${sel} [data-assistant-thread-reply-send]`)?.disabled, form, { timeout: 5000 });
      await page.press(`${form} textarea`, "Control+Enter");
      await sleep(500);
      release();
      await toast(page, "The reply is posted on merge request !4.");
      await page.unroute("**/forge/thread");
      assert(posts.length === 1, `Ctrl+Enter twice posted ${posts.length} replies`);
      assert(state.replies.filter((r) => r.thread === "d1rv").length === 1 && state.replies.some((r) => r.thread === "d1rv" && r.body === "Renamed in abc."), `the forge holds ${JSON.stringify(state.replies)}`);
      const v = await until(page, (w) => JSON.stringify(byId(w, "d1rv")?.notes) === JSON.stringify(["root: Rename it?", "root: Renamed in abc."]), "the reply did not reach the view");
      assert(v.open && !(await page.locator(form).isVisible()), "the view closed or the form stays open");
      assert(await page.evaluate(() => window.__noReload === 1), "the page reloaded");
    });

    await run("Resolve resolves the thread on the forge, it folds under the resolved count live", async () => {
      await page.evaluate(() => { window.__noReload = 1; });
      await page.click(`${threadSel("d1rv")} [data-assistant-thread-resolve]`);
      await toast(page, "The thread is resolved on merge request !4.");
      assert(state.resolved.includes("d1rv"), `the forge resolved ${JSON.stringify(state.resolved)}`);
      const v = await until(page, (w) => w.count === "3" && w.resolvedToggle === "Show 2 resolved threads" && !w.threads.some((t) => t.id === "d1rv"), "the resolved thread did not fold away");
      assert(v.open && v.resolved.some((t) => t.id === "d1rv"), `the view reads ${JSON.stringify(v)}`);
      assert(await page.evaluate(() => window.__noReload === 1), "the page reloaded");
    });

    await run("Publish as review publishes the line comment, which becomes a thread of the view", async () => {
      await page.evaluate(() => { window.__noReload = 1; });
      await page.click(`${PANE} [data-assistant-project-publish]`);
      await page.waitForSelector(DIALOG, { state: "visible", timeout: 5000 });
      const asked = (await page.locator(DIALOG).textContent()).replace(/\s+/g, " ");
      assert(asked.includes("Publish the comment?") && asked.includes("It becomes a review thread on merge request !4 on GitLab"), `the confirm reads ${asked}`);
      await page.click(`${DIALOG} .swal2-confirm`);
      await toast(page, "The comment is published on merge request !4.");
      assert(state.published.length === 1 && state.published[0].body === "Mine to publish" && state.published[0].line === 4, `the forge holds ${JSON.stringify(state.published)}`);
      const v = await until(page, (w) => !w.unpublished && w.threads.some((t) => !t.local && JSON.stringify(t.notes) === JSON.stringify(["root: Mine to publish"])), "the published comment did not become a thread");
      assert(v.count === "3" && !byCid(v, mineId)?.local && byCid(v, localId)?.local, `the count reads ${v.count}, the threads ${JSON.stringify(v.threads)}`);
      assert(await page.evaluate(() => window.__noReload === 1), "the page reloaded");
    });

    await run("a file changed on disk marks its thread outdated live, the view and its open fold stay", async () => {
      await page.evaluate(() => { window.__noReload = 1; });
      await page.click(`${PANE} [data-assistant-project-resolved-toggle]`);
      await until(page, (w) => w.resolved.every((t) => t.shown), "the fold did not open");
      fs.writeFileSync(path.join(repoDir, FILE), CONTENT.replace("\tc := 3\n", "\tc := 30\n"));
      const v = await until(page, (w) => byId(w, "d2rv")?.outdated, "the thread whose line went did not turn outdated", 45000);
      assert(v.open && v.resolved.every((t) => t.shown) && !byId(v, "d1rv").outdated, `the view reads ${JSON.stringify(v)}`);
      assert(await page.evaluate(() => window.__noReload === 1), "the page reloaded");
    });

    await run("a local thread resolves, reopens and takes replies through the editor's line comments, live", async () => {
      await page.evaluate(() => { window.__noReload = 1; });
      const sel = localSel(localId);
      await page.click(`${sel} [data-assistant-thread-resolve]`);
      let v = await until(page, (w) => w.count === "2" && w.resolvedToggle === "Hide resolved threads" && w.resolved.length === 3 && w.resolved.some((t) => t.cid === localId && t.shown), "the resolved local thread did not fold away");
      assert(JSON.stringify(byCid(v, localId).actions) === JSON.stringify(["Reply", "Hand off", "Reopen"]) && byCid(v, localId).bin, `the resolved local thread offers ${JSON.stringify(byCid(v, localId).actions)}`);
      await page.click(`${sel} [data-assistant-thread-reopen]`);
      await until(page, (w) => w.count === "3" && w.threads.some((t) => t.cid === localId), "the reopened local thread did not come back");
      await page.click(`${sel} [data-assistant-thread-reply-open]`);
      await page.fill(`${sel} [data-assistant-thread-reply] textarea`, "Keeping it.");
      assert((await page.locator(`${sel} [data-assistant-thread-reply] .form-check`).textContent()).trim() === "Resolve the thread", "the local reply offers no resolve");
      assert(!(await page.locator(`${sel} [data-assistant-thread-reply] .form-hint`).count()), "the local reply speaks of the forge");
      await page.check(`${sel} [data-assistant-thread-reply-resolve]`);
      await page.press(`${sel} [data-assistant-thread-reply] textarea`, "Meta+Enter");
      v = await until(page, (w) => w.count === "2" && notesOf(w.resolved.find((t) => t.cid === localId)) === JSON.stringify(["You: Keep d?", "You: Yes.", "You: Keeping it."]), "the reply that resolves did not reach the view");
      await page.click(`${sel} [data-assistant-thread-reply-open]`);
      await page.fill(`${sel} [data-assistant-thread-reply] textarea`, "Back.");
      assert((await page.locator(`${sel} [data-assistant-thread-reply] .form-check`).textContent()).trim() === "Reopen the thread", "the resolved local reply offers no reopen");
      await page.check(`${sel} [data-assistant-thread-reply-resolve]`);
      await page.click(`${sel} [data-assistant-thread-reply-send]`);
      v = await until(page, (w) => w.count === "3" && notesOf(w.threads.find((t) => t.cid === localId)) === JSON.stringify(["You: Keep d?", "You: Yes.", "You: Keeping it.", "You: Back."]), "the reply that reopens did not reach the view");
      const stored = await page.evaluate(async ({ project, id }) => (await (await fetch(`/projects/${project}/editor/comments`)).json()).comments.find((c) => c.id === id), { project: REPO, id: localId });
      assert(stored && !stored.resolved && stored.replies.length === 3 && !stored.forge, `the editor's line comments hold ${JSON.stringify(stored)}`);
      assert(v.open && await page.evaluate(() => window.__noReload === 1), "the view closed or the page reloaded");
    });

    await run("Hand off puts the thread under the message box's words, sends nothing, a reload keeps it", async () => {
      const input = "[data-assistant-input]";
      const messages = () => page.locator("[data-message-id]").count();
      await page.fill(input, "First words");
      await sleep(700);
      const sent = await messages();
      await page.click(`${threadSel("d2rv")} [data-assistant-thread-handoff]`);
      const want = `First words\n\n${handoff("d2rv", 5)}\n`;
      await page.waitForFunction(([sel, w]) => document.querySelector(sel)?.value === w, [input, want], { timeout: 5000 });
      assert(await page.evaluate((sel) => document.activeElement === document.querySelector(sel), input), "the message box has no focus");
      await sleep(700);
      assert(await messages() === sent, "the hand off sent a message");
      await openPage(page, `${BASE}/assistants/${owner}`);
      assert(await page.locator(input).inputValue() === want, `the reload shows ${JSON.stringify(await page.locator(input).inputValue())}`);
      await page.fill(input, "");
      await sleep(700);
    });

    await run("on a phone the sheet opens the project, Hand off closes the sheet onto the message box", async () => {
      const mp = await mobilePage();
      await openPage(mp, `${BASE}/assistants/${owner}`);
      await mp.tap("[data-assistant-watching-button]");
      await mp.waitForSelector(`#assistant-aside.show [data-assistant-project="${REPO}"] [data-assistant-project-review]`, { state: "visible", timeout: 10000 });
      await mp.tap(`${PANE} [data-assistant-project="${REPO}"] [data-assistant-project-review]`);
      const v = await until(mp, (w) => w.open && w.name === REPO && w.threads.length === 3, "the phone's sheet did not open the project");
      assert(v.threads.every((t) => t.right <= v.width), `a thread sticks out of the phone: ${JSON.stringify(v.threads.map((t) => t.right))} at ${v.width}`);
      if (SHOTS_DIR) await mp.screenshot({ path: path.join(SHOTS_DIR, "assistant-projects-reviews-phone.png") });
      await mp.tap(`${PANE} [data-assistant-project-back]`);
      await until(mp, (w) => !w.open && w.cards, "Back did not return to the phone's cards");
      await mp.tap(`${PANE} [data-assistant-project="${REPO}"] [data-assistant-project-review]`);
      await until(mp, (w) => w.open, "the project did not open again");
      await mp.tap(`${localSel(localId)} [data-assistant-thread-handoff]`);
      await mp.waitForFunction(() => !document.querySelector("#assistant-aside").classList.contains("show"), null, { timeout: 5000 });
      assert(await mp.locator("[data-assistant-input]").inputValue() === `${localHandoff(localId, 6)}\n`, "the phone's message box does not hold the hand off of the local thread");
      await mp.fill("[data-assistant-input]", "");
      await sleep(700);
    });

    await run("a project bound to no forge opens on its local threads from its card, live, and on its resolved ones once none is open", async () => {
      await page.setViewportSize({ width: 1360, height: 900 });
      await openPage(page, `${BASE}/assistants/${owner}`);
      await page.waitForFunction(({ pane, name }) => document.querySelector(`${pane} [data-assistant-project="${name}"] [data-assistant-project-review]`)?.textContent.trim() === "1 open thread", { pane: PANE, name: PLAIN }, { timeout: 15000 });
      await page.evaluate(() => { window.__noReload = 1; });
      const v = await openReviews(page, PLAIN);
      const t = v.threads[0];
      assert(v.count === "1" && v.threads.length === 1 && !v.unpublished && !v.resolvedToggle, `the view without a forge reads ${JSON.stringify(v)}`);
      assert(t.cid === plainId && t.local && t.origin === "local" && t.file === `${NOTE}:1` && t.quote === "x" && notesOf(t) === JSON.stringify(["You: Check this"]), `the thread without a forge reads ${JSON.stringify(t)}`);
      assert(JSON.stringify(t.actions) === JSON.stringify(["Reply", "Hand off", "Resolve"]) && t.bin, `the thread without a forge offers ${JSON.stringify(t.actions)}`);
      assert(!(await page.locator(`${PANE} [data-assistant-project-switch]`).count()), "a project without git offers a switch");
      await postJSON(page, `/projects/${PLAIN}/editor/comments`, { path: NOTE, line: 1, text: "And this" }, { json: true });
      await until(page, (w) => w.count === "2" && w.threads.length === 2, "a comment made elsewhere did not reach the view");
      await page.click(`${localSel(plainId)} [data-assistant-thread-handoff]`);
      await page.waitForFunction(([sel, w]) => document.querySelector(sel)?.value === w, ["[data-assistant-input]", `Line comment ${plainId} in project ${PLAIN} at ${NOTE}:1.\n`], { timeout: 5000 });
      await page.fill("[data-assistant-input]", "");
      await sleep(700);
      await page.click(`${PANE} [data-assistant-project-back]`);
      await until(page, (w) => !w.open && w.cards, "Back did not return to the cards");
      await page.waitForFunction(({ pane, name }) => document.querySelector(`${pane} [data-assistant-project="${name}"] [data-assistant-project-review]`)?.textContent.trim() === "2 open threads", { pane: PANE, name: PLAIN }, { timeout: 15000 });
      const ids = await page.evaluate(async (name) => (await (await fetch(`/projects/${name}/editor/comments`)).json()).comments.map((c) => c.id), PLAIN);
      for (const id of ids) await postJSON(page, `/projects/${PLAIN}/editor/comments/resolve`, { id, resolved: true }, { json: true });
      await page.waitForFunction(({ pane, name }) => document.querySelector(`${pane} [data-assistant-project="${name}"] [data-assistant-project-review]`)?.textContent.trim() === "2 resolved threads", { pane: PANE, name: PLAIN }, { timeout: 15000 });
      const done = await openReviews(page, PLAIN);
      assert(done.count === "0" && !done.threads.length && done.resolvedToggle === "Show 2 resolved threads" && done.resolved.length === 2, `the project with resolved threads alone reads ${JSON.stringify(done)}`);
      await page.click(`${PANE} [data-assistant-project-back]`);
      await until(page, (w) => !w.open && w.cards, "Back did not return to the cards");
      for (const id of ids) await postJSON(page, `/projects/${PLAIN}/editor/comments/resolve`, { id, resolved: false }, { json: true });
      await page.waitForFunction(({ pane, name }) => document.querySelector(`${pane} [data-assistant-project="${name}"] [data-assistant-project-review]`)?.textContent.trim() === "2 open threads", { pane: PANE, name: PLAIN }, { timeout: 15000 });
      assert(await page.evaluate(() => window.__noReload === 1), "the page reloaded");
    });

    await run("a guest of a link reads the project without a control and without a link into it", async () => {
      const made = await post(page, "/settings/sharing/new", [["amount", "1"], ["unit", "hours"], ["name", `aprv-${TAG}`], ["assistant", owner]]);
      const link = (made.text.match(/value="([^"]*\/share\/[^"]+)"/) || [])[1];
      assert(made.ok && link, `making a link answered ${made.status}`);
      linkId = await page.evaluate(async (name) => {
        const doc = new DOMParser().parseFromString(await (await fetch("/settings/sharing")).text(), "text/html");
        return [...doc.querySelectorAll("[data-share-row]")].find((r) => r.querySelector("[data-share-name]")?.textContent.trim() === name)?.dataset.shareRow || "";
      }, `aprv-${TAG}`);
      assert(linkId, "the link is not listed");
      const base = new URL(link).pathname;
      guest = await guestPage(browser, bag, { requests: [] }, ORIGIN);
      const gp = guest.page;
      await openPage(gp, `${BASE}${base}/assistants/${owner}`);
      await gp.click(`${PANE} [data-assistant-project="${REPO}"] [data-assistant-project-review]`);
      const v = await until(gp, (w) => w.open && w.threads.length === 3, "the guest's project did not open");
      assert(v.controls === 0 && v.threads.every((t) => !t.id && !t.href && t.actions.length === 0) && !v.unpublished, `the guest's view offers ${JSON.stringify(v)}`);
      assert(v.threads.some((t) => t.outdated && JSON.stringify(t.notes) === JSON.stringify(["alice: Why 3?"])), `the guest's threads read ${JSON.stringify(v.threads)}`);
      assert(v.threads.some((t) => t.local && t.origin === "local" && notesOf(t) === JSON.stringify(["Owner: Keep d?", "Owner: Yes.", "Owner: Keeping it.", "Owner: Back."])), `the guest's local thread reads ${JSON.stringify(v.threads)}`);
      await gp.click(`${PANE} [data-assistant-project-back]`);
      await until(gp, (w) => !w.open && w.cards, "the guest's Back did not return to the cards");
      const plain = await openReviews(gp, PLAIN);
      assert(plain.controls === 0 && plain.threads.length === 2 && plain.threads.every((t) => t.local && t.actions.length === 0 && !t.href), `the guest's project without a forge reads ${JSON.stringify(plain)}`);
    });

    await run("the bin deletes a local reply and thread as the editor does, Reviews and the card follow live, a reply typed elsewhere stays", async () => {
      await page.setViewportSize({ width: 1360, height: 900 });
      const made = (await postJSON(page, `/projects/${REPO}/editor/comments`, { path: FILE, line: 7, text: "Drop me" }, { json: true })).comment?.id;
      assert(made, "the thread to delete was not made");
      await postJSON(page, `/projects/${REPO}/editor/comments/reply`, { thread: made, text: "Drop this reply" }, { json: true });
      await postJSON(page, `/projects/${REPO}/editor/comments/reply`, { thread: made, text: "Stay." }, { json: true });
      const stored = async (id) => (await page.evaluate(async (name) => (await (await fetch(`/projects/${name}/editor/comments`)).json()).comments, REPO)).find((c) => c.id === id);
      await openPage(page, `${BASE}/assistants/${owner}`);
      const counted = () => page.evaluate(({ pane, name }) => Number((document.querySelector(`${pane} [data-assistant-project="${name}"] [data-assistant-project-review]`)?.textContent.match(/(\d+) open thread/) || [])[1] || 0), { pane: PANE, name: REPO });
      await page.waitForFunction(({ pane, name }) => /open threads/.test(document.querySelector(`${pane} [data-assistant-project="${name}"] [data-assistant-project-review]`)?.textContent || ""), { pane: PANE, name: REPO }, { timeout: 15000 });
      const before = await counted();
      await page.evaluate(() => { window.__noReload = 1; });
      let v = await openReviews(page);
      const mine = byCid(v, made);
      assert(JSON.stringify(mine?.actions) === JSON.stringify(["Reply", "Hand off", "Resolve"]) && mine.bin && JSON.stringify(mine.drops) === JSON.stringify(["Drop this reply", "Stay."]), `the thread to delete offers ${JSON.stringify(mine)}`);
      assert(even(mine) && mine.inset.ends.length === 3, `the bins stand in from the right unlike the file icon from the left: ${JSON.stringify(mine.inset)}`);
      assert(v.threads.filter((t) => !t.local).every((t) => !t.bin && !t.drops.length), `a thread of the forge offers a delete: ${JSON.stringify(v.threads)}`);
      const bin = `${localSel(made)} [data-assistant-thread-delete]`;
      const look = () => page.evaluate((sel) => {
        const box = (el) => el.getBoundingClientRect();
        const paint = (el) => `${getComputedStyle(el).color}/${getComputedStyle(el).backgroundColor}`;
        const bin = document.querySelector(`${sel} [data-assistant-thread-delete]`);
        const drop = document.querySelector(`${sel} [data-comment-note-drop]`);
        const badge = document.querySelector(`${sel} [data-assistant-thread-head] [data-assistant-thread-origin]`);
        return {
          bin: paint(bin),
          drop: paint(drop),
          icon: getComputedStyle(bin.querySelector(".ti")).color,
          label: `${bin.getAttribute("aria-label")}/${drop.getAttribute("aria-label")}`,
          middle: Math.abs((box(bin).top + box(bin).bottom) / 2 - (box(badge).top + box(badge).bottom) / 2),
          height: box(bin).height - box(drop).height,
          ends: Math.abs(box(bin).right - box(drop).right),
          visible: document.activeElement === bin && bin.matches(":focus-visible"),
        };
      }, localSel(made));
      const settle = async (want, what) => {
        let last = null;
        for (let i = 0; i < 40; i += 1) {
          last = await look();
          if (want(last)) return last;
          await sleep(50);
        }
        throw new Error(`${what}: ${JSON.stringify(last)}`);
      };
      const DANGER = "rgb(214, 57, 57)";
      for (const scheme of ["light", "dark"]) {
        await page.emulateMedia({ colorScheme: scheme });
        await page.waitForFunction((dark) => document.documentElement.getAttribute("data-bs-theme") === (dark ? "dark" : "light"), scheme === "dark", { timeout: 10000 });
        await page.mouse.move(0, 0);
        const rest = await settle((x) => !x.bin.startsWith(DANGER) && x.bin === x.drop, `the bins at rest in ${scheme}`);
        assert(rest.bin.endsWith("/rgba(0, 0, 0, 0)") && rest.label === "Delete comment/Delete reply" && rest.middle <= 1 && Math.abs(rest.height) <= 1 && rest.ends <= 1, `the bins at rest in ${scheme} read ${JSON.stringify(rest)}`);
        await page.hover(bin);
        await settle((x) => x.bin === `${DANGER}/rgba(214, 57, 57, 0.12)` && x.icon === DANGER, `the hovered bin in ${scheme}`);
        if (SHOTS_DIR && scheme === "light") await page.screenshot({ path: path.join(SHOTS_DIR, "assistant-projects-reviews-bin.png") });
        await page.mouse.move(0, 0);
        await page.focus(`${localSel(made)} [data-assistant-thread-file]`);
        await page.keyboard.press("Tab");
        await settle((x) => x.visible && x.bin === `${DANGER}/rgba(214, 57, 57, 0.12)`, `the bin focused from the keyboard in ${scheme}`);
        await page.evaluate(() => document.activeElement?.blur());
      }
      await page.emulateMedia({ colorScheme: "light" });
      const typing = `${localSel(localId)} [data-assistant-thread-reply] textarea`;
      await page.click(`${localSel(localId)} [data-assistant-thread-reply-open]`);
      await page.fill(typing, "Half typed");
      await page.click(`${localSel(made)} [data-comment-note-drop] >> nth=0`);
      await page.waitForSelector(DIALOG, { state: "visible", timeout: 5000 });
      let asked = (await page.locator(DIALOG).textContent()).replace(/\s+/g, " ");
      assert(asked.includes("Delete this reply?") && asked.includes(`${FILE}:7`) && asked.includes("Drop this reply") && !asked.includes("Stay."), `the reply's confirm reads ${asked}`);
      await page.click(`${DIALOG} .swal2-confirm`);
      v = await until(page, (w) => notesOf(byCid(w, made)) === JSON.stringify(["You: Drop me", "You: Stay."]), "the reply did not go from Reviews");
      assert(JSON.stringify((await stored(made))?.replies?.map((r) => r.text)) === JSON.stringify(["Stay."]), `the store holds ${JSON.stringify(await stored(made))}`);
      await page.click(bin);
      await page.waitForSelector(DIALOG, { state: "visible", timeout: 5000 });
      asked = (await page.locator(DIALOG).textContent()).replace(/\s+/g, " ");
      assert(asked.includes("Delete this comment?") && asked.includes("Drop me") && asked.includes("It takes its reply along."), `the thread's confirm reads ${asked}`);
      await page.click(`${DIALOG} .swal2-cancel`);
      await page.waitForSelector(DIALOG, { state: "detached", timeout: 5000 });
      await sleep(500);
      assert(byCid(await view(page), made) && await stored(made), "a cancelled delete took the thread");
      await page.click(bin);
      await page.waitForSelector(DIALOG, { state: "visible", timeout: 5000 });
      await page.click(`${DIALOG} .swal2-confirm`);
      v = await until(page, (w) => w.open && !byCid(w, made), "the thread did not go from Reviews");
      assert(!(await stored(made)), "the store still holds the deleted thread");
      assert(await page.locator(typing).inputValue() === "Half typed", "the reply typed elsewhere lost its words");
      await page.waitForFunction(({ pane, name, want }) => Number((document.querySelector(`${pane} [data-assistant-project="${name}"] [data-assistant-project-review]`)?.textContent.match(/(\d+) open thread/) || [])[1] || 0) === want, { pane: PANE, name: REPO, want: before - 1 }, { timeout: 15000 });
      await page.click(`${localSel(localId)} [data-assistant-thread-reply-cancel]`);
      await until(page, (w) => w.count === String(before - 1), "the view's count did not follow once the reply closed");
      await page.click(`${PANE} [data-assistant-project-back]`);
      await until(page, (w) => !w.open && w.cards, "Back did not return to the cards");
      assert(await counted() === before - 1 && await page.evaluate(() => window.__noReload === 1), `the card counts ${await counted()} open threads, was ${before}, or the page reloaded`);
      const onPhone = (await postJSON(page, `/projects/${REPO}/editor/comments`, { path: FILE, line: 7, text: "Drop on the phone" }, { json: true })).comment?.id;
      assert(onPhone, "the phone's thread was not made");
      const mp = await mobilePage();
      await openPage(mp, `${BASE}/assistants/${owner}`);
      await mp.tap("[data-assistant-watching-button]");
      await mp.waitForSelector(`#assistant-aside.show [data-assistant-project="${REPO}"] [data-assistant-project-review]`, { state: "visible", timeout: 10000 });
      await mp.tap(`${PANE} [data-assistant-project="${REPO}"] [data-assistant-project-review]`);
      const held = await until(mp, (w) => w.open && byCid(w, onPhone)?.shown, "the phone's view did not show the thread");
      assert(byCid(held, onPhone).bin && JSON.stringify(byCid(held, onPhone).actions) === JSON.stringify(["Reply", "Hand off", "Resolve"]) && byCid(held, onPhone).oneLine && even(byCid(held, onPhone)), `the phone's thread reads ${JSON.stringify(byCid(held, onPhone))}`);
      const phoneBin = `${localSel(onPhone)} [data-assistant-thread-delete]`;
      await mp.locator(phoneBin).scrollIntoViewIfNeeded();
      const fits = await mp.evaluate((sel) => { const r = document.querySelector(sel).getBoundingClientRect(); return { right: r.right, width: r.width, inner: window.innerWidth }; }, phoneBin);
      assert(fits.right <= fits.inner && fits.width >= 24, `the phone's bin reads ${JSON.stringify(fits)}`);
      await mp.tap(phoneBin);
      await mp.waitForSelector(DIALOG, { state: "visible", timeout: 5000 });
      await mp.tap(`${DIALOG} .swal2-confirm`);
      await until(mp, (w) => w.open && !byCid(w, onPhone), "the phone's thread did not go");
      assert(!(await stored(onPhone)), "the store still holds the phone's thread");
      return `${before} to ${before - 1} open threads`;
    });
  } finally {
    if (guest) await guest.ctx.close().catch(() => {});
    if (linkId) await post(page, `/settings/sharing/${linkId}`, { form: "revoke" }).catch(() => {});
    for (const id of coders) await post(page, `/coders/${id}/delete`, {}).catch(() => {});
    if (owner) await post(page, `/assistants/${owner}`, { form: "delete" }).catch(() => {});
    if (hostEdit) await post(page, hostEdit, { form: "delete" }).catch(() => {});
    for (const name of [REPO, ...OTHERS, SPARE]) await L.deleteProject(page, name).catch(() => {});
  }
});
