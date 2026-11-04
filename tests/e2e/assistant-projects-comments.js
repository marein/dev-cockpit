const L = require("./lib");
const { assert, BASE, sleep } = L;
const fs = require("fs");
const path = require("path");
const { execFileSync } = require("child_process");
const { guestPage } = require("./project-share-guest");
const { startFakeForge, addHost, gitlabNote, gitlabDiscussion } = require("./forge-fake");

// The project comments in the Reviews of a project opened in the aside of an
// assistant (GET /assistants/projects/view), chromium, against the fake
// GitLab and then the fake GitHub: the card's review line counts them with
// the threads on lines, the view lists them first, titled Project comment
// without a file, a quote or outdated, the local ones marked local and the
// forge's general threads marked with its name, each with its notes from
// line_comment_notes.gohtml. A general thread of GitLab takes Reply with its
// resolve and Resolve through the forge thread route, an individual note
// GitLab does not resolve takes Reply alone, a comment of GitHub's
// conversation, which is no thread, takes neither. A local project comment
// resolves, reopens and takes replies through the editor's line comment
// routes, Publish asks in the words of a general comment and posts it as one.
// Hand off names a project comment by the forge's thread id or the comment's
// id, the project and the branch, no place. On a phone the sheet holds them
// within its width. A guest of a link to the assistant reads them without a
// control.
//
// Instance like assistant-projects-reviews.js's. SHOTS_DIR with a mount takes
// a desktop and a phone screenshot. About a minute.
const TAG = Date.now().toString(36).slice(-5);
const SHOTS_DIR = process.env.SHOTS_DIR || "";
const FAKE_PORT = Number(process.env.FORGE_PORT || 3157);
const FAKE_TLS_PORT = Number(process.env.FORGE_TLS_PORT || 3158);
const FAKE_ADDR = process.env.FORGE_ADDR || "127.0.0.1";
const TOKEN = `glpat-e2e${Date.now().toString(36)}Pc5mW2xN`;
const ORIGIN = new URL(BASE).origin;
const REPO = `apcom${TAG}`;
const FILE = "src/login.go";
const CONTENT = "package login\n\nfunc login() {\n\tname := bb\n\tc := 3\n\treturn\n}\n";
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

async function openPage(p, url) {
  await p.goto(url, { waitUntil: "domcontentloaded" });
  await L.dismissUpdate(p);
  await p.waitForSelector(`${PANE} [data-assistant-body]`, { state: "attached", timeout: 15000 });
}

// view reads the open project as it stands on the screen, each thread by
// what it shows.
const view = (p) => p.evaluate((pane) => {
  const shown = (el) => !!el && el.getClientRects().length > 0 && getComputedStyle(el).visibility !== "hidden";
  const text = (el) => (shown(el) ? el.textContent.replace(/\s+/g, " ").trim() : "");
  const root = document.querySelector(`${pane} [data-assistant-project-view-body]`);
  const thread = (el) => ({
    id: el.dataset.thread || "",
    cid: el.dataset.assistantThread || "",
    local: el.hasAttribute("data-assistant-thread-local"),
    head: text(el.querySelector("[data-assistant-thread-project]")),
    origin: text(el.querySelector("[data-assistant-thread-origin]")),
    file: !!el.querySelector("[data-assistant-thread-file], [data-assistant-thread-quote], [data-assistant-thread-outdated]"),
    shown: shown(el),
    notes: [...el.querySelectorAll("[data-comment-note]")].filter(shown).map((n) => `${text(n.querySelector("[data-comment-note-author]"))}: ${text(n.querySelector("[data-comment-note-text]"))}`),
    when: text(el.querySelector("[data-comment-note] dc-time")),
    actions: [...el.querySelectorAll("[data-assistant-thread-actions] button")].filter(shown).map(text),
    right: el.getBoundingClientRect().right,
  });
  return {
    open: shown(root),
    cards: shown(document.querySelector(`${pane} > [data-assistant-body]`)),
    name: root?.dataset.assistantProjectViewBody || "",
    count: text(root?.querySelector("[data-assistant-project-reviews-count]")),
    unpublished: text(root?.querySelector("[data-assistant-project-unpublished-box]")),
    threads: [...(root?.querySelectorAll("[data-assistant-project-threads-open] [data-assistant-thread]") || [])].map(thread),
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

const all = (v) => [...v.threads, ...v.resolved];
const byId = (v, id) => all(v).find((t) => t.id === id);
const byCid = (v, cid) => all(v).find((t) => t.cid === cid);
const byNote = (v, words) => all(v).find((t) => t.notes.some((n) => n.includes(words)));
const notesOf = (t) => JSON.stringify(t?.notes || []);
const threadSel = (id) => `${PANE} [data-assistant-thread][data-thread="${id}"]`;
const localSel = (cid) => `${PANE} [data-assistant-thread="${cid}"][data-assistant-thread-local]`;
const toast = (p, words) => p.waitForFunction((w) => [...document.querySelectorAll(".dc-toast")].some((el) => el.textContent.includes(w)), words, { timeout: 15000 });
const reviewLine = (p, want) => p.waitForFunction(({ pane, name, want }) => document.querySelector(`${pane} [data-assistant-project="${name}"] [data-assistant-project-review]`)?.textContent.replace(/\s+/g, " ").trim() === want, { pane: PANE, name: REPO, want }, { timeout: 30000 });

async function openReviews(p) {
  await p.click(`${PANE} [data-assistant-project="${REPO}"] [data-assistant-project-review]`);
  return until(p, (v) => v.open && !v.cards && v.name === REPO, "the project did not open");
}

L.runFeature("assistant-projects-comments", async ({ browser, page, run, mobilePage, bag }) => {
  const fake = await startFakeForge({ port: FAKE_PORT, tlsPort: FAKE_TLS_PORT, token: TOKEN });
  const state = fake.state;
  const fakeWeb = `http://${FAKE_ADDR}:${FAKE_PORT}`;
  const alias = `apcm-${TAG}`;
  let coder = "";
  let owner = "";
  let repoDir = "";
  let hostEdit = "";
  let linkId = "";
  let guest = null;
  let pubId = "";
  let keepId = "";
  try {
    await run("setup: a repository on the fake GitLab with a thread on a line, two general ones and two local project comments", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      owner = (await postJSON(page, "/assistants/new", { form: "new", coder: "claude" })).id;
      assert(owner, "no assistant made");
      await L.createProject(page, REPO);
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
      Object.assign(state, { head: pushed, status: "success", readOnly: false, ghPending: false, diffFiles: [], published: [], replies: [], resolved: [] });
      state.discussions = (s) => [
        gitlabDiscussion("d1pc", false, [gitlabNote(41, "root", "Rename it?", { resolvable: true, at: { head: s.head, path: FILE, newLine: 4 } })]),
        gitlabDiscussion("g1pc", false, [gitlabNote(42, "alice", "Ship it?", { resolvable: true })]),
        gitlabDiscussion("g2pc", true, [gitlabNote(43, "root", "Nice.")]),
      ];
      const added = await addHost(page, { kind: "gitlab", web_url: `${fakeWeb}/gl`, aliases: alias, token: TOKEN });
      assert(!added.failed, added.failed);
      hostEdit = await page.evaluate(async (mark) => {
        const html = await (await fetch("/settings/forges")).text();
        const doc = new DOMParser().parseFromString(html, "text/html");
        return [...doc.querySelectorAll("[data-forge-host]")].find((r) => r.textContent.includes(mark))?.querySelector("[data-forge-edit]")?.getAttribute("href") || "";
      }, alias);
      assert(hostEdit, "the added host has no Edit on the list");
      pubId = (await postJSON(page, `/projects/${REPO}/editor/comments`, { project: true, text: "Publish me" }, { json: true })).comment?.id;
      keepId = (await postJSON(page, `/projects/${REPO}/editor/comments`, { project: true, text: "Keep in mind" }, { json: true })).comment?.id;
      assert(pubId && keepId, "the project comments were not made");
      const made = await postJSON(page, "/coders/new", { name: `apcm-work-${TAG}`, project: repoDir, coder: "claude", automatic_approval: "on", prompt: "job" });
      coder = made.id;
      assert(coder, `no coder made: ${JSON.stringify(made)}`);
      await postJSON(page, "/assistants/jobs", { form: "steer", assistant: owner, terminal: coder, task: "Review check", done_when: "" });
      await postJSON(page, "/assistants/jobs", { form: "release", terminal: coder });
    });

    await run("the card counts the project comments, the view lists them first, titled without a place", async () => {
      await page.setViewportSize({ width: 1360, height: 900 });
      await openPage(page, `${BASE}/assistants/${owner}`);
      await reviewLine(page, "5 open threads · 2 unpublished comments");
      const v = await openReviews(page);
      assert(v.count === "5" && v.threads.length === 5 && !v.resolved.length, `the view reads ${JSON.stringify(v)}`);
      assert(v.threads.slice(0, 4).every((t) => t.head === "Project comment" && !t.file) && v.threads[4].id === "d1pc" && !v.threads[4].head && v.threads[4].file, `the project comments do not stand first without a place: ${JSON.stringify(v.threads)}`);
      const g1 = byId(v, "g1pc");
      const g2 = byId(v, "g2pc");
      const pub = byCid(v, pubId);
      const keep = byCid(v, keepId);
      assert(g1 && !g1.local && g1.origin === "GitLab" && notesOf(g1) === JSON.stringify(["alice: Ship it?"]) && /ago$/.test(g1.when), `the general thread reads ${JSON.stringify(g1)}`);
      assert(JSON.stringify(g1.actions) === JSON.stringify(["Reply", "Hand off", "Resolve"]), `the general thread offers ${JSON.stringify(g1.actions)}`);
      assert(g2 && g2.origin === "GitLab" && JSON.stringify(g2.actions) === JSON.stringify(["Reply", "Hand off"]), `the note GitLab does not resolve reads ${JSON.stringify(g2)}`);
      assert(pub && pub.local && pub.origin === "local" && notesOf(pub) === JSON.stringify(["You: Publish me"]), `the local project comment reads ${JSON.stringify(pub)}`);
      assert(keep && JSON.stringify(keep.actions) === JSON.stringify(["Reply", "Hand off", "Resolve"]), `the local project comment offers ${JSON.stringify(keep)}`);
      assert(v.unpublished === "2 project comments not published Publish", `the unpublished line reads ${v.unpublished}`);
      if (SHOTS_DIR) await page.screenshot({ path: path.join(SHOTS_DIR, "assistant-projects-comments-desktop.png") });
    });

    await run("a note GitLab does not resolve takes a reply alone, a general thread a reply with its resolve, live", async () => {
      await page.evaluate(() => { window.__noReload = 1; });
      await page.click(`${threadSel("g2pc")} [data-assistant-thread-reply-open]`);
      const lone = `${threadSel("g2pc")} [data-assistant-thread-reply]`;
      await page.waitForSelector(`${lone} textarea`, { state: "visible", timeout: 5000 });
      assert(!(await page.locator(`${lone} [data-assistant-thread-reply-resolve]`).count()), "the note GitLab does not resolve offers a resolve");
      await page.fill(`${lone} textarea`, "Thanks.");
      await page.click(`${lone} [data-assistant-thread-reply-send]`);
      await toast(page, "The reply is posted on merge request !4.");
      assert(state.replies.some((r) => r.thread === "g2pc" && r.body === "Thanks."), `the forge holds ${JSON.stringify(state.replies)}`);
      await until(page, (w) => notesOf(byId(w, "g2pc")).includes("Thanks."), "the reply to the note did not reach the view");
      await page.click(`${threadSel("g1pc")} [data-assistant-thread-reply-open]`);
      const form = `${threadSel("g1pc")} [data-assistant-thread-reply]`;
      await page.fill(`${form} textarea`, "Shipped.");
      await page.check(`${form} [data-assistant-thread-reply-resolve]`);
      await page.click(`${form} [data-assistant-thread-reply-send]`);
      await toast(page, "The reply is posted on merge request !4, the thread is resolved.");
      assert(state.resolved.includes("g1pc") && state.replies.some((r) => r.thread === "g1pc" && r.body === "Shipped."), `the forge holds ${JSON.stringify(state)}`);
      const v = await until(page, (w) => w.count === "4" && w.resolved.some((t) => t.id === "g1pc") && !w.threads.some((t) => t.id === "g1pc"), "the resolved general thread did not fold away");
      assert(v.threads.slice(0, 3).every((t) => t.head === "Project comment"), `the open project comments do not stand first: ${JSON.stringify(v.threads)}`);
      assert(await page.evaluate(() => window.__noReload === 1), "the page reloaded");
    });

    await run("a local project comment resolves, reopens and takes a reply through the editor's line comments, live", async () => {
      await page.evaluate(() => { window.__noReload = 1; });
      const sel = localSel(keepId);
      await page.click(`${sel} [data-assistant-thread-resolve]`);
      let v = await until(page, (w) => w.count === "3" && w.resolved.some((t) => t.cid === keepId) && !w.threads.some((t) => t.cid === keepId), "the resolved project comment did not fold away");
      await page.click(`${PANE} [data-assistant-project-resolved-toggle]`);
      v = await until(page, (w) => byCid(w, keepId)?.shown && JSON.stringify(byCid(w, keepId).actions) === JSON.stringify(["Reply", "Hand off", "Reopen"]), "the resolved project comment offers no reopen");
      assert(byCid(v, keepId).head === "Project comment" && !byCid(v, keepId).file, `the resolved project comment reads ${JSON.stringify(byCid(v, keepId))}`);
      await page.click(`${sel} [data-assistant-thread-reopen]`);
      await until(page, (w) => w.count === "4" && w.threads.some((t) => t.cid === keepId), "the reopened project comment did not come back");
      await page.click(`${sel} [data-assistant-thread-reply-open]`);
      await page.fill(`${sel} [data-assistant-thread-reply] textarea`, "Noted.");
      await page.click(`${sel} [data-assistant-thread-reply-send]`);
      v = await until(page, (w) => notesOf(byCid(w, keepId)) === JSON.stringify(["You: Keep in mind", "You: Noted."]) && w.unpublished === "1 project comment not published Publish", "the reply did not reach the view");
      const stored = await page.evaluate(async ({ project, id }) => (await (await fetch(`/projects/${project}/editor/comments`)).json()).comments.find((c) => c.id === id), { project: REPO, id: keepId });
      assert(stored && stored.project && !stored.resolved && stored.replies.length === 1, `the editor's comments hold ${JSON.stringify(stored)}`);
      await reviewLine(page, "4 open threads · 1 unpublished comment");
      assert(v.open && await page.evaluate(() => window.__noReload === 1), "the view closed or the page reloaded");
    });

    await run("Publish asks in the words of a general comment and posts the project comment as one", async () => {
      await page.evaluate(() => { window.__noReload = 1; });
      await page.click(`${PANE} [data-assistant-project-publish]`);
      await page.waitForSelector(DIALOG, { state: "visible", timeout: 5000 });
      const asked = (await page.locator(DIALOG).textContent()).replace(/\s+/g, " ");
      assert(asked.includes("Publish the comment?") && asked.includes("It becomes a general comment on merge request !4 on GitLab, for everybody who can read it there."), `the confirm reads ${asked}`);
      await page.click(`${DIALOG} .swal2-confirm`);
      await toast(page, "The comment is published on merge request !4.");
      assert(state.published.length === 1 && state.published[0].general && state.published[0].body === "Publish me", `the forge holds ${JSON.stringify(state.published)}`);
      const v = await until(page, (w) => !w.unpublished && w.threads.some((t) => !t.local && t.head === "Project comment" && notesOf(t) === JSON.stringify(["root: Publish me"])), "the published project comment did not become a general thread");
      assert(v.count === "4" && !v.threads.some((t) => t.local && t.notes.includes("You: Publish me")), `the view reads ${JSON.stringify(v.threads)}`);
      assert(await page.evaluate(() => window.__noReload === 1), "the page reloaded");
    });

    await run("Hand off names a project comment by its thread id or its id, the project and the branch, no place, and sends nothing", async () => {
      const input = "[data-assistant-input]";
      const messages = () => page.locator("[data-message-id]").count();
      await page.fill(input, "");
      await sleep(700);
      const sent = await messages();
      await page.click(`${localSel(keepId)} [data-assistant-thread-handoff]`);
      const local = `Project comment ${keepId} in project ${REPO} on branch feature.\n`;
      await page.waitForFunction(([sel, w]) => document.querySelector(sel)?.value === w, [input, local], { timeout: 5000 });
      await page.fill(input, "");
      await page.click(`${threadSel("g2pc")} [data-assistant-thread-handoff]`);
      const general = `General thread g2pc in project ${REPO} on branch feature.\n`;
      await page.waitForFunction(([sel, w]) => document.querySelector(sel)?.value === w, [input, general], { timeout: 5000 });
      await sleep(700);
      assert(await messages() === sent, "the hand off sent a message");
      await page.fill(input, "");
      await sleep(700);
    });

    await run("on a phone the sheet holds the project comments within its width", async () => {
      const mp = await mobilePage();
      await openPage(mp, `${BASE}/assistants/${owner}`);
      await mp.tap("[data-assistant-watching-button]");
      await mp.waitForSelector(`#assistant-aside.show [data-assistant-project="${REPO}"] [data-assistant-project-review]`, { state: "visible", timeout: 10000 });
      await mp.tap(`${PANE} [data-assistant-project="${REPO}"] [data-assistant-project-review]`);
      const v = await until(mp, (w) => w.open && w.name === REPO && w.threads.length === 4, "the phone's sheet did not open the project");
      const project = v.threads.filter((t) => t.head === "Project comment");
      assert(project.length === 3 && project.every((t) => t.shown && t.right <= v.width), `a project comment sticks out of the phone: ${JSON.stringify(project)} at ${v.width}`);
      if (SHOTS_DIR) await mp.screenshot({ path: path.join(SHOTS_DIR, "assistant-projects-comments-phone.png") });
    });

    await run("on GitHub a comment of the conversation stands as a project comment taking no reply and no resolve", async () => {
      await page.evaluate(() => { window.__noReload = 1; });
      const saved = await post(page, hostEdit, { form: "save", kind: "github", web_url: fakeWeb, aliases: alias });
      assert(saved.status === 200, `turning the host into a GitHub answered ${saved.status}`);
      git(repoDir, "remote", "set-url", "origin", `${fakeWeb}/octo/app.git`);
      const v = await until(page, (w) => byNote(w, "Looks good")?.origin === "GitHub", "the conversation comment did not reach the view", 45000);
      const lone = byNote(v, "Looks good");
      assert(lone.head === "Project comment" && !lone.file && !lone.id && notesOf(lone) === JSON.stringify(["hubot: Looks good but see the threads"]), `the conversation comment reads ${JSON.stringify(lone)}`);
      assert(JSON.stringify(lone.actions) === JSON.stringify(["Hand off"]), `the conversation comment offers ${JSON.stringify(lone.actions)}`);
      assert(!(await page.locator(`${PANE} [data-assistant-thread="${lone.cid}"] [data-assistant-thread-reply]`).count()), "the conversation comment has a reply form");
      await page.click(`${PANE} [data-assistant-thread="${lone.cid}"] [data-assistant-thread-handoff]`);
      await page.waitForFunction(([sel, w]) => document.querySelector(sel)?.value === w, ["[data-assistant-input]", `Project comment ${lone.cid} in project ${REPO} on branch feature.\n`], { timeout: 5000 });
      await page.fill("[data-assistant-input]", "");
      await sleep(700);
      assert(byCid(v, keepId)?.local, "the local project comment went with the forge");
      assert(await page.evaluate(() => window.__noReload === 1), "the page reloaded");
    });

    await run("a guest of a link reads the project comments without a control", async () => {
      const made = await post(page, "/settings/sharing/new", [["amount", "1"], ["unit", "hours"], ["name", `apcm-${TAG}`], ["assistant", owner]]);
      const link = (made.text.match(/value="([^"]*\/share\/[^"]+)"/) || [])[1];
      assert(made.ok && link, `making a link answered ${made.status}`);
      linkId = await page.evaluate(async (name) => {
        const doc = new DOMParser().parseFromString(await (await fetch("/settings/sharing")).text(), "text/html");
        return [...doc.querySelectorAll("[data-share-row]")].find((r) => r.querySelector("[data-share-name]")?.textContent.trim() === name)?.dataset.shareRow || "";
      }, `apcm-${TAG}`);
      assert(linkId, "the link is not listed");
      guest = await guestPage(browser, bag, { requests: [] }, ORIGIN);
      const gp = guest.page;
      await openPage(gp, `${BASE}${new URL(link).pathname}/assistants/${owner}`);
      const v = await openReviews(gp);
      const project = v.threads.filter((t) => t.head === "Project comment");
      assert(v.controls === 0 && project.length >= 2 && project.every((t) => !t.id && !t.file && t.actions.length === 0), `the guest's view offers ${JSON.stringify(v)}`);
      assert(project.some((t) => t.local && notesOf(t) === JSON.stringify(["Owner: Keep in mind", "Owner: Noted."])) && project.some((t) => t.origin === "GitHub" && t.notes[0]?.startsWith("hubot: Looks good")), `the guest's project comments read ${JSON.stringify(project)}`);
    });
  } finally {
    if (guest) await guest.ctx.close().catch(() => {});
    if (linkId) await post(page, `/settings/sharing/${linkId}`, { form: "revoke" }).catch(() => {});
    if (coder) await post(page, `/coders/${coder}/delete`, {}).catch(() => {});
    if (owner) await post(page, `/assistants/${owner}`, { form: "delete" }).catch(() => {});
    if (hostEdit) await post(page, hostEdit, { form: "delete" }).catch(() => {});
    await L.deleteProject(page, REPO).catch(() => {});
  }
});
