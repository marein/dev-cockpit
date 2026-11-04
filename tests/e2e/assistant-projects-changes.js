const L = require("./lib");
const { assert, BASE, sleep } = L;
const fs = require("fs");
const path = require("path");
const { execFileSync } = require("child_process");
const { guestPage } = require("./project-share-guest");
const { startFakeForge, addHost, gitlabNote, gitlabDiscussion } = require("./forge-fake");

// The Changes of a project opened in an assistant's aside (GET
// /assistants/projects/view?section=changes), chromium, against the fake
// GitLab, in the editor's terms: on top Uncommitted, the files that differ
// between HEAD and the working copy, then one list, the pair the editor's
// compare starts on, from main to the Working copy since the split, committed
// and uncommitted changes and untracked files together. A card's changes line
// counts the commits since the split and the files and lines of that list
// with the uncommitted files, and opens the project on Changes, where a
// project on a forge has a switch to Reviews. Changes names both sides with
// the split, counts commits, files and lines, lists the files with the
// largest change first cut to ten and the commits newest first cut to ten,
// each with a `Show N more files` or `Show N older commits` that unfolds the
// rest in place (GET .../view?more=files&more=commits) and, then reading
// `Show fewer files` or `Show fewer commits`, folds it again, kept over a
// pull, and unfolding while a reply stays open in Reviews. The working
// copy and a commit on disk move it live, the picked section staying. A file unfolds its diff against the base (GET
// /assistants/projects/diff, drawn by @dc/inline-diff): the editor's inline
// diff with three unchanged lines around a change and the rest folded, its
// marks in the editor's tints, the dark scheme taken live, read again on the
// next pull of the view and folded away again; a deleted, a binary and a
// link show a line instead, so does a file over the editor's diff limits
// (Settings › Editor › Git, set to 300 lines and 200 KiB for the check),
// naming the limit it hit. The git watch reads
// git's status only, so the live check moves the status beside the edit.
// Open in the editor in the fold opens the editor's compare on the file on
// the same pair, the pair stored on the device going back to the suggestion,
// a deleted one as the base had it, and `?view=compare&pair=suggested` opens
// the compare alone on that pair. Uncommitted stands above the pair while the
// working copy differs from HEAD, an uncommitted file in both lists: it counts
// its files, lists them with the largest change first and their lines against
// HEAD, unfolds its rest past ten in place (more=uncommitted) apart from the
// pair's, unfolds a file's diff against HEAD (list=uncommitted), opens the
// editor's commit view on the file's diff against HEAD (?view=commit&file=),
// a deleted one read only as HEAD has it, a staged rename against its old
// path, and is gone once everything is committed. A project without a forge shows Changes
// alone, compared on the main branch with the commit before, and a branch
// made there without a commit of its own names main as its base. On a
// phone's sheet the rows of both lists and a diff stand inside the width. A
// guest of a link reads Changes and its diffs with no control, Open in the
// editor leading into the share only on a project the link grants.
//
// Instance like assistant-projects-reviews.js's: tests/e2e/fakes first on
// PATH, a scratch HOME, its --projects-dir mounted into the runner at the
// same path, the fake forge's ports FORGE_PORT and FORGE_TLS_PORT (default
// 3157 and 3158) published on the host's loopback, where the instance
// reaches them at FORGE_ADDR (default 127.0.0.1). SHOTS_DIR with a mount
// takes desktop, dark desktop and phone screenshots. About three minutes.
const TAG = Date.now().toString(36).slice(-5);
const SHOTS_DIR = process.env.SHOTS_DIR || "";
const FAKE_PORT = Number(process.env.FORGE_PORT || 3157);
const FAKE_TLS_PORT = Number(process.env.FORGE_TLS_PORT || 3158);
const FAKE_ADDR = process.env.FORGE_ADDR || "127.0.0.1";
const TOKEN = `glpat-e2e${Date.now().toString(36)}Cg5mT2pX`;
const ORIGIN = new URL(BASE).origin;
const REPO = `apchg${TAG}`;
const PLAIN = `apchgplain${TAG}`;
const PANE = "#assistant-projects";
const lines = (n, word) => Array.from({ length: n }, (_, i) => `${word} ${i + 1}\n`).join("");

function git(dir, ...args) {
  return execFileSync("git", ["-c", "user.name=e2e", "-c", "user.email=e2e@localhost", "-c", "commit.gpgsign=false", "-C", dir, ...args], { encoding: "utf8" });
}

function commitFile(dir, file, content, message) {
  fs.mkdirSync(path.dirname(path.join(dir, file)), { recursive: true });
  fs.writeFileSync(path.join(dir, file), content);
  git(dir, "add", "--", file);
  git(dir, "commit", "-q", "-m", message);
}

async function post(page, url, body) {
  return page.evaluate(async ({ url, body }) => {
    const token = document.querySelector('meta[name="csrf-token"]')?.content || "";
    const r = await fetch(url, { method: "POST", headers: { "X-CSRF-Token": token, Accept: "application/json" }, body: new URLSearchParams(body) });
    return { ok: r.ok, status: r.status, text: await r.text() };
  }, { url, body });
}

async function postJSON(page, url, body) {
  const res = await post(page, url, body);
  assert(res.ok, `POST ${url} answered ${res.status}: ${res.text.slice(0, 200)}`);
  return JSON.parse(res.text || "{}");
}

async function newCoder(page, dir, name) {
  const made = await postJSON(page, "/coders/new", { name, project: dir, coder: "claude", automatic_approval: "on", prompt: `job-${name}` });
  assert(made.id, `no coder made: ${JSON.stringify(made)}`);
  return made.id;
}

async function openPage(p, url) {
  await p.goto(url, { waitUntil: "domcontentloaded" });
  await L.dismissUpdate(p);
  await p.waitForSelector(`${PANE} [data-assistant-body]`, { state: "attached", timeout: 15000 });
}

// line reads a card's changes line as it stands on the screen.
const line = (p, name) => p.evaluate(({ pane, name }) => {
  const el = document.querySelector(`${pane} [data-assistant-project="${name}"] [data-assistant-project-changes]`);
  return el && el.getClientRects().length ? el.textContent.replace(/\s+/g, " ").trim() : "";
}, { pane: PANE, name });

// view reads the open project: which section shows, the switch, the counts,
// the pair, the headings and the rows of the lists as they stand on the
// screen, the files of the pair and of Uncommitted apart.
const view = (p) => p.evaluate((pane) => {
  const shown = (el) => !!el && el.getClientRects().length > 0 && getComputedStyle(el).visibility !== "hidden";
  const text = (el) => (shown(el) ? el.textContent.replace(/\s+/g, " ").trim() : "");
  const label = (el) => (shown(el) ? el.innerText.replace(/\s+/g, " ").trim() : "");
  const root = document.querySelector(`${pane} [data-assistant-project-view-body]`);
  const changes = root?.querySelector("[data-assistant-project-changes-section]");
  const rows = (list) => [...(changes?.querySelectorAll(`[data-assistant-changed-files="${list}"] [data-assistant-changed-file]`) || [])].filter(shown).map((row) => ({
    path: row.dataset.assistantChangedFile,
    mark: text(row.querySelector("[data-assistant-changed-mark]")),
    lines: text(row.querySelector("[data-assistant-changed-lines]")),
    dir: text(row.querySelector("[data-assistant-changed-dir]")),
    href: row.querySelector("[data-assistant-changed-open]")?.getAttribute("href") || "",
    right: row.getBoundingClientRect().right,
  }));
  const top = changes?.querySelector("[data-assistant-uncommitted]");
  const base = changes?.querySelector("[data-assistant-changes-base]");
  return {
    open: shown(root),
    cards: shown(document.querySelector(`${pane} > [data-assistant-body]`)),
    name: root?.dataset.assistantProjectViewBody || "",
    switch: [...(root?.querySelectorAll("[data-assistant-project-switch]") || [])].filter(shown).map((b) => `${b.dataset.assistantProjectSwitch}${b.classList.contains("active") ? "*" : ""}`).join(","),
    reviews: shown(root?.querySelector("[data-assistant-project-reviews]")),
    changes: shown(changes),
    counts: [...(changes?.querySelector("[data-assistant-changes-counts]")?.children || [])].filter(shown).map((stat) => [...stat.children].map((part) => part.textContent.trim()).join(" ")).join(", "),
    filesCount: changes?.querySelector("[data-assistant-changes-files]")?.textContent.trim() || "",
    base: text(changes?.querySelector("[data-assistant-changes-base]")),
    heads: [...(changes?.querySelectorAll("h3, h4") || [])].filter(shown).map((h) => [...h.childNodes].map((n) => n.textContent.trim()).filter(Boolean).join(" ")),
    files: rows("files"),
    more: label(changes?.querySelector('[data-assistant-changed-more="files"]')),
    moreOpen: changes?.querySelector('[data-assistant-changed-more="files"]')?.getAttribute("aria-expanded") || "",
    uncommittedShown: shown(top),
    above: shown(top) && shown(base) && top.getBoundingClientRect().bottom <= base.getBoundingClientRect().top,
    uncommitted: rows("uncommitted"),
    moreUncommitted: label(changes?.querySelector('[data-assistant-changed-more="uncommitted"]')),
    moreUncommittedOpen: changes?.querySelector('[data-assistant-changed-more="uncommitted"]')?.getAttribute("aria-expanded") || "",
    commits: [...(changes?.querySelectorAll("[data-assistant-changes-commit]") || [])].filter(shown).map((row) => text(row)),
    older: label(changes?.querySelector("[data-assistant-changes-older]")),
    olderOpen: changes?.querySelector("[data-assistant-changes-older]")?.getAttribute("aria-expanded") || "",
    none: text(changes?.querySelector("[data-assistant-changed-none]")),
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

// diff reads a file's fold in a list as it stands on the screen: whether it
// is open, what its toggle says, the drawn diff's lines that arrived and
// went, the folded runs of unchanged lines, the note in its place and the
// editor link.
const diff = (p, file, list = "files") => p.evaluate(({ pane, file, list }) => {
  const shown = (el) => !!el && el.getClientRects().length > 0 && getComputedStyle(el).visibility !== "hidden";
  const row = document.querySelector(`${pane} [data-assistant-changed-files="${list}"] [data-assistant-changed-file="${CSS.escape(file)}"]`);
  const fold = row?.querySelector("[data-assistant-fold]");
  const cm = fold?.querySelector(".cm-editor");
  const lines = (sel) => [...(cm?.querySelectorAll(sel) || [])].filter(shown).map((el) => el.textContent);
  const note = [...(fold?.querySelectorAll("[data-assistant-diff-note]") || [])].find(shown);
  const open = fold?.querySelector("[data-assistant-changed-open]");
  const added = cm?.querySelector(".cm-changedLine");
  return {
    row: shown(row),
    open: shown(fold),
    expanded: row?.querySelector("[data-assistant-changed-toggle]")?.getAttribute("aria-expanded") || "",
    drawn: shown(cm),
    wait: shown(fold?.querySelector("[data-assistant-diff-wait]")),
    lines: lines(".cm-line"),
    added: lines(".cm-changedLine"),
    removed: lines(".cm-deletedChunk .cm-deletedLine"),
    folded: lines(".cm-collapsedLines").length,
    tint: added ? getComputedStyle(added).backgroundColor : "",
    surface: cm ? getComputedStyle(cm).backgroundColor : "",
    green: getComputedStyle(document.body).getPropertyValue("--tblr-green-rgb").split(",").map((part) => part.trim()).join(", "),
    note: note ? note.textContent.trim() : "",
    href: shown(open) ? open.getAttribute("href") : "",
    right: fold ? fold.getBoundingClientRect().right : 0,
    width: window.innerWidth,
  };
}, { pane: PANE, file, list });

async function untilDiff(p, file, want, what, timeout = 30000, list = "files") {
  const end = Date.now() + timeout;
  let last = null;
  while (Date.now() < end) {
    last = await diff(p, file, list);
    if (want(last)) return last;
    await sleep(250);
  }
  throw new Error(`${what}: ${JSON.stringify(last)}`);
}

const rowOf = (file, list = "files") => `${PANE} [data-assistant-changed-files="${list}"] [data-assistant-changed-file="${file}"]`;

async function unfold(p, file, list = "files") {
  await p.click(`${rowOf(file, list)} [data-assistant-changed-toggle]`);
}

// moreFiles unfolds or folds the files of a list past the first ten and
// waits until the rows stand as asked.
async function moreFiles(p, open, count, list = "files") {
  await p.click(`${PANE} [data-assistant-changed-more="${list}"]`);
  const rest = list === "files" ? (w) => [w.moreOpen, w.files.length] : (w) => [w.moreUncommittedOpen, w.uncommitted.length];
  return until(p, (w) => rest(w)[0] === String(open) && rest(w)[1] === count, `the rest of ${list} did not ${open ? "unfold" : "fold"} to ${count} rows`);
}

async function openChanges(p, name) {
  await p.click(`${PANE} [data-assistant-project="${name}"] [data-assistant-project-changes]`);
  return until(p, (v) => v.open && !v.cards && v.name === name && v.changes, `${name} did not open on its changes`);
}

L.runFeature("assistant-projects-changes", async ({ browser, page, run, mobilePage, bag }) => {
  const fake = await startFakeForge({ port: FAKE_PORT, tlsPort: FAKE_TLS_PORT, token: TOKEN });
  const state = fake.state;
  const fakeWeb = `http://${FAKE_ADDR}:${FAKE_PORT}`;
  const coders = [];
  let owner = "";
  let repoDir = "";
  let plainDir = "";
  let split = "";
  let hostEdit = "";
  let linkId = "";
  let guest = null;
  const editorURL = (file) => `/projects/${REPO}/editor?file=${encodeURIComponent(file)}&view=compare`;
  const commitURL = (file) => `/projects/${REPO}/editor?file=${encodeURIComponent(file)}&view=commit`;
  try {
    await run("setup: a branch split from main with a dozen commits, uncommitted work on top, a project without a forge beside", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      owner = (await postJSON(page, "/assistants/new", { form: "new", coder: "claude" })).id;
      assert(owner, "no assistant made");
      for (const name of [REPO, PLAIN]) await L.createProject(page, name);
      repoDir = await L.projectPath(page, REPO);
      plainDir = await L.projectPath(page, PLAIN);
      assert(fs.existsSync(repoDir), `the projects dir is not mounted at ${repoDir}`);
      git(repoDir, "init", "-q", "-b", "main");
      fs.mkdirSync(path.join(repoDir, "src"));
      fs.writeFileSync(path.join(repoDir, "src/keep.txt"), lines(3, "keep"));
      fs.writeFileSync(path.join(repoDir, "src/old.txt"), lines(2, "old"));
      fs.writeFileSync(path.join(repoDir, "src/drop.txt"), lines(30, "drop"));
      fs.writeFileSync(path.join(repoDir, "src/long.txt"), lines(40, "long"));
      git(repoDir, "add", "-A");
      git(repoDir, "commit", "-q", "-m", "base");
      git(repoDir, "switch", "-q", "-c", "feature");
      commitFile(repoDir, "src/big.txt", lines(40, "big"), "Add the big file");
      for (let i = 1; i <= 9; i += 1) commitFile(repoDir, `src/f${i}.txt`, lines(i, `f${i}`), `Add f${i}`);
      git(repoDir, "rm", "-q", "src/drop.txt");
      git(repoDir, "commit", "-q", "-m", "Drop the dropped file");
      git(repoDir, "mv", "src/old.txt", "src/moved.txt");
      git(repoDir, "commit", "-q", "-m", "Move the old file");
      git(repoDir, "switch", "-q", "main");
      commitFile(repoDir, "MAIN.md", "main\n", "Main moves on");
      git(repoDir, "switch", "-q", "feature");
      split = git(repoDir, "merge-base", "main", "feature").trim();
      fs.appendFileSync(path.join(repoDir, "src/keep.txt"), "keep 4\n");
      fs.writeFileSync(path.join(repoDir, "src/new.txt"), lines(2, "new"));
      const pushed = git(repoDir, "rev-parse", "HEAD").trim();
      git(repoDir, "update-ref", "refs/remotes/origin/feature", pushed);
      git(repoDir, "remote", "add", "origin", `${fakeWeb}/gl/team/sub/app.git`);
      Object.assign(state, { head: pushed, status: "success", diffFiles: [{ path: "src/big.txt", patch: "@@ -0,0 +1,1 @@\n+big 1" }], published: [], replies: [], resolved: [] });
      state.discussions = (s) => [gitlabDiscussion("d1cg", false, [gitlabNote(51, "root", "Smaller?", { resolvable: true, at: { head: s.head, path: "src/big.txt", newLine: 1 } })])];
      const added = await addHost(page, { kind: "gitlab", web_url: `${fakeWeb}/gl`, aliases: `apcg-${TAG}`, token: TOKEN });
      assert(!added.failed, added.failed);
      hostEdit = await page.evaluate(async (mark) => {
        const html = await (await fetch("/settings/forges")).text();
        const doc = new DOMParser().parseFromString(html, "text/html");
        return [...doc.querySelectorAll("[data-forge-host]")].find((r) => r.textContent.includes(mark))?.querySelector("[data-forge-edit]")?.getAttribute("href") || "";
      }, `apcg-${TAG}`);
      assert(hostEdit, "the added host has no Edit on the list");
      git(plainDir, "init", "-q", "-b", "main");
      commitFile(plainDir, "a.txt", "a\n", "first");
      commitFile(plainDir, "a.txt", "a\nb\n", "second");
      for (const dir of [repoDir, plainDir]) {
        const coder = await newCoder(page, dir, `apcg-${path.basename(dir)}`);
        coders.push(coder);
        await postJSON(page, "/assistants/jobs", { form: "steer", assistant: owner, terminal: coder, task: "Changes check", done_when: "" });
      }
    });

    await run("the changes line counts the list, opens Changes on the pair with its counts, one list of files and the commits", async () => {
      await page.setViewportSize({ width: 1360, height: 900 });
      await openPage(page, `${BASE}/assistants/${owner}`);
      const want = "12 commits · 14 files · +88 −30 · 2 uncommitted";
      await page.waitForFunction(({ pane, name, want }) => document.querySelector(`${pane} [data-assistant-project="${name}"] [data-assistant-project-changes]`)?.textContent.replace(/\s+/g, " ").trim() === want, { pane: PANE, name: REPO, want }, { timeout: 30000 }).catch(() => {});
      assert(await line(page, REPO) === want, `the card's changes line reads ${await line(page, REPO)}`);
      assert(await line(page, PLAIN) === "1 commit · 1 file · +1 −0 · clean", `the plain card's changes line reads ${await line(page, PLAIN)}`);
      const title = await page.getAttribute(`${PANE} [data-assistant-project="${REPO}"] [data-assistant-project-changes]`, "title");
      assert(title === `Working copy since the split from main at ${split.slice(0, 7)}`, `the changes line's title reads ${title}`);
      await page.evaluate(() => { window.__noReload = 1; });
      const v = await openChanges(page, REPO);
      assert(v.switch === "reviews,changes*" && !v.reviews, `the switch reads ${v.switch}, Reviews shown ${v.reviews}`);
      assert(v.base === `from main to Working copy since the split at ${split.slice(0, 7)}`, `the pair reads ${v.base}`);
      assert(v.counts === "12 commits, 14 files, +88 added, −30 removed", `the counts read ${v.counts}`);
      assert(JSON.stringify(v.heads) === JSON.stringify(["Uncommitted 2 against HEAD", "Commits 12"]), `the headings read ${JSON.stringify(v.heads)}`);
      const order = ["src/big.txt", "src/drop.txt", "src/f9.txt", "src/f8.txt", "src/f7.txt", "src/f6.txt", "src/f5.txt", "src/f4.txt", "src/f3.txt", "src/f2.txt"];
      assert(JSON.stringify(v.files.map((f) => f.path)) === JSON.stringify(order), `the files read ${JSON.stringify(v.files.map((f) => f.path))}`);
      assert(v.files[0].mark === "A" && v.files[0].lines === "+40 −0" && v.files[1].mark === "D" && v.files[1].lines === "+0 −30", `the largest rows read ${JSON.stringify(v.files.slice(0, 2))}`);
      assert(v.files.every((f) => f.dir === "src" && f.href === editorURL(f.path)), `the rows ${JSON.stringify(v.files)}`);
      assert(v.more === "Show 4 more files" && v.moreOpen === "false", `the rest of the files reads ${v.more} ${v.moreOpen}`);
      if (SHOTS_DIR) await page.screenshot({ path: path.join(SHOTS_DIR, "assistant-projects-changes-desktop.png") });
      const all = await moreFiles(page, true, 14);
      assert(JSON.stringify(all.files.slice(10).map((f) => `${f.mark} ${f.path} ${f.lines}`)) === JSON.stringify(["U src/new.txt +2 −0", "A src/f1.txt +1 −0", "M src/keep.txt +1 −0", "R src/moved.txt +0 −0"]) && all.more === "Show fewer files",
        `the unfolded rest reads ${JSON.stringify(all.files.slice(10))}`);
      assert(all.files.find((f) => f.path === "src/moved.txt").dir === "src/old.txt → src/moved.txt", `the rename reads ${JSON.stringify(all.files.slice(-1))}`);
      const folded = await moreFiles(page, false, 10);
      assert(folded.more === "Show 4 more files", `the folded rest of the files reads ${folded.more}`);
      assert(v.commits.length === 10 && /^\w{7} Move the old file e2e · (just now|\d+m ago)$/.test(v.commits[0]) && v.commits[9].includes("Add f2") && v.older === "Show 2 older commits" && v.olderOpen === "false", `the commits read ${JSON.stringify(v.commits)} ${v.older}`);
      await page.click(`${PANE} [data-assistant-changes-older]`);
      const older = await until(page, (w) => w.commits.length === 12, "the older commits did not unfold");
      assert(older.commits[10].includes("Add f1") && older.commits[11].includes("Add the big file"), `the older commits read ${JSON.stringify(older.commits.slice(10))}`);
      assert(older.older === "Show fewer commits" && older.olderOpen === "true", `the unfolded commits' control reads ${older.older} ${older.olderOpen}`);
      await page.click(`${PANE} [data-assistant-changes-older]`);
      const shut = await until(page, (w) => w.commits.length === 10, "the older commits did not fold again");
      assert(shut.older === "Show 2 older commits" && shut.olderOpen === "false", `the folded commits' control reads ${shut.older} ${shut.olderOpen}`);
      await page.click(`${PANE} [data-assistant-project-switch="reviews"]`);
      await until(page, (w) => w.reviews && !w.changes && w.switch === "reviews*,changes", "the switch did not show Reviews");
      await page.click(`${PANE} [data-assistant-project-switch="changes"]`);
      await until(page, (w) => !w.reviews && w.changes, "the switch did not show Changes again");
      await page.click(`${PANE} [data-assistant-project-back]`);
      await until(page, (w) => !w.open && w.cards, "Back did not return to the cards");
      await page.click(`${PANE} [data-assistant-project="${REPO}"] [data-assistant-project-review]`);
      await until(page, (w) => w.open && w.reviews && !w.changes && w.switch === "reviews*,changes", "the review line did not open Reviews");
      await page.click(`${PANE} [data-assistant-project-back]`);
      assert(await page.evaluate(() => window.__noReload === 1), "the page reloaded");
    });

    await run("the working copy and a commit on disk move Changes live, the unfolded rest and the picked section stay", async () => {
      await page.evaluate(() => { window.__noReload = 1; });
      await openChanges(page, REPO);
      await moreFiles(page, true, 14);
      fs.writeFileSync(path.join(repoDir, "src/live.txt"), "live\n");
      let v = await until(page, (w) => w.changes && w.counts === "12 commits, 15 files, +89 added, −30 removed", "the new file did not reach Changes", 45000);
      assert(v.moreOpen === "true" && v.files.length === 15 && v.files.some((f) => f.path === "src/live.txt" && f.mark === "U"), `the unfolded rest after a pull reads ${JSON.stringify(v.files.map((f) => f.path))}`);
      git(repoDir, "add", "--", "src/live.txt");
      git(repoDir, "commit", "-q", "-m", "Commit the live file");
      v = await until(page, (w) => w.changes && w.counts === "13 commits, 15 files, +89 added, −30 removed", "the commit did not reach Changes", 45000);
      assert(v.files.some((f) => f.path === "src/live.txt" && f.mark === "A") && v.commits[0].includes("Commit the live file") && v.older === "Show 3 older commits", `the commit reads ${JSON.stringify(v.files.map((f) => f.mark + f.path))} ${JSON.stringify(v.commits)} ${v.older}`);
      await page.click(`${PANE} [data-assistant-project-switch="reviews"]`);
      await until(page, (w) => w.reviews && !w.changes, "the switch did not show Reviews");
      fs.writeFileSync(path.join(repoDir, "src/live2.txt"), "live\n");
      await until(page, (w) => w.filesCount === "16", "the hidden Changes did not follow the disk", 45000);
      const after = await view(page);
      assert(after.reviews && !after.changes && after.switch === "reviews*,changes", `a live pull moved the section: ${JSON.stringify(after)}`);
      fs.unlinkSync(path.join(repoDir, "src/live2.txt"));
      await page.click(`${PANE} [data-assistant-project-back]`);
      const card = "13 commits · 15 files · +89 −30 · 2 uncommitted";
      await page.waitForFunction(({ pane, name, card }) => document.querySelector(`${pane} [data-assistant-project="${name}"] [data-assistant-project-changes]`)?.textContent.replace(/\s+/g, " ").trim() === card, { pane: PANE, name: REPO, card }, { timeout: 45000 }).catch(() => {});
      assert(await line(page, REPO) === card, `the card reads ${await line(page, REPO)}`);
      assert(await page.evaluate(() => window.__noReload === 1), "the page reloaded");
    });

    await run("a file unfolds its diff against the base: hunks with three lines around, live, folded away again, a line in place of what it does not draw", async () => {
      await page.setViewportSize({ width: 1360, height: 900 });
      await openPage(page, `${BASE}/assistants/${owner}`);
      await page.evaluate(() => { window.__noReload = 1; });
      await openChanges(page, REPO);
      await moreFiles(page, true, 15);
      const long = path.join(repoDir, "src/long.txt");
      const before = fs.readFileSync(long, "utf8");
      const extra = ["src/blob.bin", "src/many.txt", "src/large.txt", "src/live3.txt"].map((f) => path.join(repoDir, f));
      try {
        fs.writeFileSync(long, before.replace("long 20\n", "long twenty\n"));
        await until(page, (w) => w.files.some((f) => f.path === "src/long.txt" && f.mark === "M" && f.lines === "+1 −1"), "the edit did not reach the list", 45000);
        await unfold(page, "src/long.txt");
        let d = await untilDiff(page, "src/long.txt", (x) => x.drawn, "the edited file's diff was not drawn");
        assert(d.open && d.expanded === "true" && !d.wait && !d.note, `the fold reads ${JSON.stringify(d)}`);
        assert(JSON.stringify(d.removed) === JSON.stringify(["long 20"]) && JSON.stringify(d.added) === JSON.stringify(["long twenty"]), `the diff marks ${JSON.stringify(d)}`);
        assert(d.folded === 2 && JSON.stringify(d.lines) === JSON.stringify(["long 17", "long 18", "long 19", "long twenty", "long 21", "long 22", "long 23"]), `the hunk reads ${JSON.stringify(d.lines)} with ${d.folded} folds`);
        assert(d.tint === `rgba(${d.green}, 0.1)`, `the added line is not tinted like the editor's: ${d.tint}, green ${d.green}`);
        assert(d.href === editorURL("src/long.txt"), `the fold's editor link reads ${d.href}`);
        if (SHOTS_DIR) {
          await page.waitForFunction(() => !document.querySelector(".collapsing"));
          await page.screenshot({ path: path.join(SHOTS_DIR, "assistant-projects-diff-desktop.png") });
        }
        await page.emulateMedia({ colorScheme: "dark" });
        d = await untilDiff(page, "src/long.txt", (x) => x.surface === "rgb(40, 44, 52)", "the diff did not take the dark scheme");
        assert(d.tint === `rgba(${d.green}, 0.16)`, `the added line is not tinted like the editor's dark one: ${d.tint}`);
        if (SHOTS_DIR) await page.screenshot({ path: path.join(SHOTS_DIR, "assistant-projects-diff-desktop-dark.png") });
        await page.emulateMedia({ colorScheme: "light" });
        await untilDiff(page, "src/long.txt", (x) => x.surface !== "rgb(40, 44, 52)", "the diff did not take the light scheme back");
        fs.appendFileSync(long, "long 41\n");
        fs.writeFileSync(extra[3], "live\n");
        d = await untilDiff(page, "src/long.txt", (x) => x.drawn && x.added.includes("long 41"), "the open diff did not follow the disk", 45000);
        assert(d.open && d.added.includes("long twenty") && d.removed.includes("long 20"), `the followed diff reads ${JSON.stringify(d)}`);
        await unfold(page, "src/long.txt");
        d = await untilDiff(page, "src/long.txt", (x) => !x.open && !x.drawn, "the diff did not fold away");
        assert(d.expanded === "false", `the folded toggle reads ${d.expanded}`);
        await unfold(page, "src/f4.txt");
        d = await untilDiff(page, "src/f4.txt", (x) => x.drawn, "the added file's diff was not drawn");
        assert(JSON.stringify(d.added) === JSON.stringify(["f4 1", "f4 2", "f4 3", "f4 4"]) && d.removed.length === 0 && d.folded === 0, `the added file reads ${JSON.stringify(d)}`);
        await unfold(page, "src/drop.txt");
        d = await untilDiff(page, "src/drop.txt", (x) => x.open && x.note, "the deleted file shows no line");
        assert(d.note === "A deleted file, no diff." && !d.drawn && d.href === editorURL("src/drop.txt"), `the deleted file reads ${JSON.stringify(d)}`);
        await unfold(page, "src/keep.txt");
        d = await untilDiff(page, "src/keep.txt", (x) => x.drawn, "the uncommitted file's diff in the rest was not drawn");
        assert(JSON.stringify(d.added) === JSON.stringify(["keep 4"]), `the uncommitted file reads ${JSON.stringify(d)}`);
        await moreFiles(page, false, 10);
        d = await diff(page, "src/keep.txt");
        assert(!d.row && !d.drawn, `the folded rest still shows its diff: ${JSON.stringify(d)}`);
        await moreFiles(page, true, 17);
        d = await untilDiff(page, "src/keep.txt", (x) => x.row, "the rest did not unfold again");
        assert(!d.open && !d.drawn && d.expanded === "false", `a diff in the rest stayed open over its fold: ${JSON.stringify(d)}`);
        const limits = await post(page, "/settings/editor/git", { diff_max_lines: "300", diff_max_kib: "200" });
        assert(limits.ok, `the diff limits answered ${limits.status}`);
        fs.writeFileSync(extra[0], Buffer.from([0x62, 0, 0x6c, 0x6f, 0x62, 10]));
        fs.writeFileSync(extra[1], lines(301, "many"));
        fs.writeFileSync(extra[2], `${"l".repeat(210 * 1024)}\n`);
        await until(page, (w) => ["src/blob.bin", "src/many.txt", "src/large.txt"].every((f) => w.files.some((r) => r.path === f)), "the new files did not reach the list", 45000);
        for (const [file, want] of [["src/blob.bin", "A binary file, no diff."], ["src/many.txt", "More than 300 lines, not shown here."], ["src/large.txt", "Larger than 200 KiB, not shown here."]]) {
          await unfold(page, file);
          d = await untilDiff(page, file, (x) => x.open && x.note, `${file} shows no line`);
          assert(d.note === want && !d.drawn && !d.wait, `${file} reads ${JSON.stringify(d)}`);
        }
        assert(await page.evaluate(() => window.__noReload === 1), "the page reloaded");
      } finally {
        await post(page, "/settings/editor/git", { diff_max_lines: "50000", diff_max_kib: "4096" });
        fs.writeFileSync(long, before);
        for (const f of extra) fs.rmSync(f, { force: true });
      }
      await page.click(`${PANE} [data-assistant-project-back]`);
    });

    await run("a file opens the editor's compare on the same pair, a deleted one as the base had it, the pair link the compare alone", async () => {
      const stored = JSON.stringify({ from: "main", to: "", mode: "direct", worktree: false });
      await page.evaluate(({ key, stored }) => localStorage.setItem(key, stored), { key: `dc-editor-revdiff:${REPO}`, stored });
      await openChanges(page, REPO);
      await unfold(page, "src/big.txt");
      await page.click(`${rowOf("src/big.txt")} [data-assistant-changed-open]`);
      await page.waitForURL((u) => u.pathname === `/projects/${REPO}/editor` && u.searchParams.get("view") === "compare" && u.searchParams.get("file") === "src/big.txt", { timeout: 15000 });
      await page.waitForSelector('.editor-tab.active[data-path="src/big.txt"]', { timeout: 15000 });
      await page.waitForSelector(".cm-mergeView .cm-content", { timeout: 15000 });
      await page.waitForSelector("[data-editor-revdiff]", { state: "visible", timeout: 15000 });
      const panel = () => page.evaluate((key) => ({ stored: localStorage.getItem(key), from: document.querySelector('[data-editor-revdiff-name="from"]')?.textContent.trim(), to: document.querySelector('[data-editor-revdiff-name="to"]')?.textContent.trim(), mode: document.querySelector("[data-editor-revdiff-mode]")?.value }), `dc-editor-revdiff:${REPO}`);
      let got = await panel();
      assert(got.stored === null && got.from === "main" && got.to === "Working copy" && got.mode === "since", `the compare stands on ${JSON.stringify(got)}`);
      await openPage(page, `${BASE}/assistants/${owner}`);
      await openChanges(page, REPO);
      await unfold(page, "src/drop.txt");
      await page.click(`${rowOf("src/drop.txt")} [data-assistant-changed-open]`);
      const deleted = `//rev/${encodeURIComponent(split)}/${encodeURIComponent("src/drop.txt")}`;
      await page.waitForSelector(`.editor-tab.active[data-path="${deleted}"]`, { timeout: 15000 });
      await openPage(page, `${BASE}/assistants/${owner}`);
      await openChanges(page, REPO);
      await moreFiles(page, true, 15);
      await unfold(page, "src/new.txt");
      await page.click(`${rowOf("src/new.txt")} [data-assistant-changed-open]`);
      await page.waitForSelector('.editor-tab.active[data-path="src/new.txt"]', { timeout: 15000 });
      await page.waitForSelector(".cm-mergeView .cm-content", { timeout: 15000 });
      await page.evaluate(({ key, stored }) => localStorage.setItem(key, stored), { key: `dc-editor-revdiff:${REPO}`, stored });
      await page.goto(`${BASE}/projects/${REPO}/editor?view=compare&pair=suggested`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      await page.waitForSelector("[data-editor-revdiff]", { state: "visible", timeout: 15000 });
      await page.waitForFunction(() => document.querySelector('[data-editor-revdiff-name="from"]')?.textContent.trim() === "main", null, { timeout: 15000 }).catch(() => {});
      got = await panel();
      assert(got.stored === null && got.from === "main" && got.to === "Working copy" && got.mode === "since", `the pair link stands on ${JSON.stringify(got)}`);
    });

    await run("Uncommitted stands over the pair against HEAD, unfolds its diff and its rest, opens the commit view and goes once committed", async () => {
      await page.setViewportSize({ width: 1360, height: 900 });
      await openPage(page, `${BASE}/assistants/${owner}`);
      await page.evaluate(() => { window.__noReload = 1; });
      let v = await openChanges(page, REPO);
      assert(v.uncommittedShown && v.above, `Uncommitted does not stand above the pair: ${JSON.stringify(v)}`);
      assert(JSON.stringify(v.uncommitted.map((f) => `${f.mark} ${f.path} ${f.lines} ${f.href}`)) === JSON.stringify([`U src/new.txt +2 −0 ${commitURL("src/new.txt")}`, `M src/keep.txt +1 −0 ${commitURL("src/keep.txt")}`]), `Uncommitted reads ${JSON.stringify(v.uncommitted)}`);
      const f9 = path.join(repoDir, "src/f9.txt");
      const f9Before = fs.readFileSync(f9, "utf8");
      const extra = Array.from({ length: 10 }, (_, i) => path.join(repoDir, `src/u${String(i + 1).padStart(2, "0")}.txt`));
      try {
        fs.appendFileSync(f9, "f9 10\n");
        fs.unlinkSync(path.join(repoDir, "src/f8.txt"));
        v = await until(page, (w) => w.uncommitted.length === 4, "the edits did not reach Uncommitted", 45000);
        assert(JSON.stringify(v.uncommitted.map((f) => `${f.mark} ${f.path} ${f.lines}`)) === JSON.stringify(["D src/f8.txt +0 −8", "U src/new.txt +2 −0", "M src/f9.txt +1 −0", "M src/keep.txt +1 −0"]), `Uncommitted reads ${JSON.stringify(v.uncommitted)}`);
        assert(v.heads[0] === "Uncommitted 4 against HEAD", `the heading reads ${v.heads[0]}`);
        await unfold(page, "src/f9.txt", "uncommitted");
        let d = await untilDiff(page, "src/f9.txt", (x) => x.drawn, "the uncommitted diff was not drawn", 30000, "uncommitted");
        assert(JSON.stringify(d.added) === JSON.stringify(["f9 10"]) && d.removed.length === 0 && d.href === commitURL("src/f9.txt"), `the diff against HEAD reads ${JSON.stringify(d)}`);
        await unfold(page, "src/f9.txt");
        d = await untilDiff(page, "src/f9.txt", (x) => x.drawn, "the pair's diff was not drawn");
        assert(d.added.length === 10 && d.href === editorURL("src/f9.txt"), `the pair's diff of the same file reads ${JSON.stringify(d)}`);
        d = await diff(page, "src/f9.txt", "uncommitted");
        assert(d.open && d.drawn && JSON.stringify(d.added) === JSON.stringify(["f9 10"]), `the pair's fold took the uncommitted one: ${JSON.stringify(d)}`);
        await unfold(page, "src/f8.txt", "uncommitted");
        d = await untilDiff(page, "src/f8.txt", (x) => x.open && x.note, "the deleted file shows no line", 30000, "uncommitted");
        assert(d.note === "A deleted file, no diff." && d.href === commitURL("src/f8.txt"), `the deleted file reads ${JSON.stringify(d)}`);
        if (SHOTS_DIR) {
          await page.waitForFunction(() => !document.querySelector(".collapsing"));
          await page.screenshot({ path: path.join(SHOTS_DIR, "assistant-projects-uncommitted-desktop.png") });
        }
        await page.click(`${rowOf("src/f9.txt", "uncommitted")} [data-assistant-changed-open]`);
        await page.waitForURL((u) => u.pathname === `/projects/${REPO}/editor` && u.searchParams.get("view") === "commit" && u.searchParams.get("file") === "src/f9.txt", { timeout: 15000 });
        await page.waitForSelector('.editor-tab.active[data-path="src/f9.txt"]', { timeout: 15000 });
        await page.waitForSelector(".cm-mergeView .cm-content", { timeout: 15000 });
        await page.waitForSelector("[data-editor-commit]", { state: "visible", timeout: 15000 });
        const sides = await page.evaluate(() => [...document.querySelectorAll(".cm-mergeView .cm-content")].map((el) => el.textContent));
        assert(sides.length === 2 && !sides[0].includes("f9 10") && sides[1].includes("f9 10"), `the editor's diff against HEAD reads ${JSON.stringify(sides)}`);
        await openPage(page, `${BASE}/assistants/${owner}`);
        await openChanges(page, REPO);
        await unfold(page, "src/f8.txt", "uncommitted");
        await page.click(`${rowOf("src/f8.txt", "uncommitted")} [data-assistant-changed-open]`);
        await page.waitForSelector(`.editor-tab.active[data-path="//rev/HEAD/${encodeURIComponent("src/f8.txt")}"]`, { timeout: 15000 });
        await openPage(page, `${BASE}/assistants/${owner}`);
        await page.evaluate(() => { window.__noReload = 1; });
        await openChanges(page, REPO);
        for (const [i, file] of extra.entries()) fs.writeFileSync(file, lines(i + 3, "u"));
        v = await until(page, (w) => w.uncommitted.length === 10 && w.moreUncommitted === "Show 4 more files", "Uncommitted did not fold its rest", 45000);
        assert(v.moreUncommittedOpen === "false" && v.moreOpen === "false", `the folds read ${v.moreUncommittedOpen} ${v.moreOpen}`);
        v = await moreFiles(page, true, 14, "uncommitted");
        assert(v.moreOpen === "false" && v.files.length === 10 && v.uncommitted.some((f) => f.path === "src/keep.txt"), `unfolding Uncommitted unfolded the pair: ${JSON.stringify(v)}`);
        await moreFiles(page, false, 10, "uncommitted");
        assert(await page.evaluate(() => window.__noReload === 1), "the page reloaded");
      } finally {
        fs.writeFileSync(f9, f9Before);
        git(repoDir, "checkout", "-q", "--", "src/f8.txt");
        for (const f of extra) fs.rmSync(f, { force: true });
      }
      await openPage(page, `${BASE}/assistants/${owner}`);
      await page.evaluate(() => { window.__noReload = 1; });
      v = await openChanges(page, REPO);
      assert(v.uncommitted.length === 2, `the restored working copy reads ${JSON.stringify(v.uncommitted)}`);
      git(repoDir, "add", "-A");
      git(repoDir, "commit", "-q", "-m", "Commit the working copy");
      try {
        v = await until(page, (w) => w.changes && !w.uncommittedShown && w.commits[0]?.includes("Commit the working copy"), "Uncommitted did not go once committed", 45000);
        assert(v.heads.length === 1 && v.heads[0].startsWith("Commits"), `the headings read ${JSON.stringify(v.heads)}`);
      } finally {
        git(repoDir, "reset", "-q", "HEAD~1");
      }
      v = await until(page, (w) => w.uncommitted.length === 2, "Uncommitted did not come back", 45000);
      await page.click(`${PANE} [data-assistant-project-back]`);
      assert(await page.evaluate(() => window.__noReload === 1), "the page reloaded");
    });

    await run("a reply open in Reviews leaves a rest unfolding, a link draws no diff, a staged rename opens the editor on its old path", async () => {
      await page.setViewportSize({ width: 1360, height: 900 });
      await openPage(page, `${BASE}/assistants/${owner}`);
      await page.evaluate(() => { window.__noReload = 1; });
      await openChanges(page, REPO);
      await page.click(`${PANE} [data-assistant-project-switch="reviews"]`);
      await until(page, (w) => w.reviews && !w.changes, "the switch did not show Reviews");
      const thread = `${PANE} [data-assistant-thread][data-thread="d1cg"]`;
      await page.click(`${thread} [data-assistant-thread-reply-open]`);
      await page.fill(`${thread} [data-assistant-thread-reply-text]`, "Held words");
      await page.click(`${PANE} [data-assistant-project-switch="changes"]`);
      await until(page, (w) => !w.reviews && w.changes, "the switch did not show Changes");
      await moreFiles(page, true, 15);
      await page.click(`${PANE} [data-assistant-project-switch="reviews"]`);
      assert(await page.inputValue(`${thread} [data-assistant-thread-reply-text]`) === "Held words", "the reply lost its words");
      await page.click(`${thread} [data-assistant-thread-reply-cancel]`);
      await page.click(`${PANE} [data-assistant-project-switch="changes"]`);
      await moreFiles(page, false, 10);
      const link = path.join(repoDir, "src/link");
      const f7 = path.join(repoDir, "src/f7.txt");
      try {
        fs.symlinkSync("/etc/hostname", link);
        await until(page, (w) => w.uncommitted.some((f) => f.path === "src/link"), "the link did not reach Uncommitted", 45000);
        await unfold(page, "src/link", "uncommitted");
        let d = await untilDiff(page, "src/link", (x) => x.open && x.note, "the link shows no line", 30000, "uncommitted");
        assert(d.note === "Not a regular file, no diff." && !d.drawn, `the link reads ${JSON.stringify(d)}`);
        await moreFiles(page, true, 16);
        await unfold(page, "src/link");
        d = await untilDiff(page, "src/link", (x) => x.open && x.note, "the link in the pair shows no line");
        assert(d.note === "Not a regular file, no diff." && !d.drawn, `the link in the pair reads ${JSON.stringify(d)}`);
        fs.rmSync(link);
        git(repoDir, "mv", "src/f7.txt", "src/f7moved.txt");
        await until(page, (w) => w.uncommitted.some((f) => f.path === "src/f7moved.txt" && f.mark === "R"), "the staged rename did not reach Uncommitted", 45000);
        await unfold(page, "src/f7moved.txt", "uncommitted");
        d = await untilDiff(page, "src/f7moved.txt", (x) => x.drawn, "the rename's diff was not drawn", 30000, "uncommitted");
        assert(!d.added.length && !d.removed.length && d.href === commitURL("src/f7moved.txt"), `the rename's diff reads ${JSON.stringify(d)}`);
        await page.click(`${rowOf("src/f7moved.txt", "uncommitted")} [data-assistant-changed-open]`);
        await page.waitForSelector('.editor-tab.active[data-path="src/f7moved.txt"]', { timeout: 15000 });
        await page.waitForSelector(".cm-mergeView .cm-content", { timeout: 15000 });
        await page.waitForFunction(() => [...document.querySelectorAll(".cm-mergeView .cm-content")].every((el) => /unchanged lines/.test(el.textContent)), null, { timeout: 15000 }).catch(() => {});
        const sides = await page.evaluate(() => [...document.querySelectorAll(".cm-mergeView .cm-content")].map((el) => el.textContent));
        assert(sides.length === 2 && sides[0] === sides[1] && /^8 unchanged lines$/.test(sides[0]), `the editor's diff of the rename reads ${JSON.stringify(sides)}`);
      } finally {
        fs.rmSync(link, { force: true });
        if (!fs.existsSync(f7)) git(repoDir, "mv", "src/f7moved.txt", "src/f7.txt");
      }
      await openPage(page, `${BASE}/assistants/${owner}`);
      await openChanges(page, REPO);
      await until(page, (w) => w.uncommitted.length === 2, "the working copy did not come back", 45000);
      await page.click(`${PANE} [data-assistant-project-back]`);
    });

    await run("a project without a forge shows Changes alone, measured against the commit before", async () => {
      await openPage(page, `${BASE}/assistants/${owner}`);
      const v = await openChanges(page, PLAIN);
      assert(v.switch === "" && !v.reviews, `the plain project offers ${v.switch}, Reviews shown ${v.reviews}`);
      assert(v.base === "from main~1 to Working copy" && v.counts === "1 commit, 1 file, +1 added, −0 removed", `the plain project reads ${v.base} ${v.counts}`);
      assert(v.files.length === 1 && v.files[0].path === "a.txt" && v.files[0].dir === "" && !v.more && v.commits.length === 1 && !v.older, `the plain lists read ${JSON.stringify(v)}`);
      await page.click(`${PANE} [data-assistant-project-back]`);
      git(plainDir, "switch", "-q", "-c", "start");
      try {
        await openPage(page, `${BASE}/assistants/${owner}`);
        const title = await page.getAttribute(`${PANE} [data-assistant-project="${PLAIN}"] [data-assistant-project-changes]`, "title");
        const fresh = await openChanges(page, PLAIN);
        assert(title === "Working copy since main" && fresh.base === "from main to Working copy" && fresh.counts === "0 commits, 0 files, +0 added, −0 removed" && fresh.none === "No file differs.",
          `a branch at main's commit reads ${title}, ${fresh.base}, ${fresh.counts}, ${fresh.none}`);
        await page.click(`${PANE} [data-assistant-project-back]`);
      } finally {
        git(plainDir, "switch", "-q", "main");
        git(plainDir, "branch", "-q", "-D", "start");
      }
    });

    await run("on a phone the sheet opens Changes inside its width", async () => {
      const mp = await mobilePage();
      await openPage(mp, `${BASE}/assistants/${owner}`);
      await mp.tap("[data-assistant-watching-button]");
      await mp.waitForSelector(`#assistant-aside.show [data-assistant-project="${REPO}"] [data-assistant-project-changes]`, { state: "visible", timeout: 10000 });
      await mp.tap(`${PANE} [data-assistant-project="${REPO}"] [data-assistant-project-changes]`);
      await until(mp, (w) => w.open && w.changes && w.files.length === 10, "the phone's sheet did not open Changes");
      await mp.tap(`${PANE} [data-assistant-changed-more="files"]`);
      const v = await until(mp, (w) => w.moreOpen === "true" && w.files.length === 15, "the phone's sheet did not unfold the rest");
      assert(v.uncommittedShown && v.above && v.uncommitted.length === 2, `the phone's Uncommitted reads ${JSON.stringify(v)}`);
      assert([...v.files, ...v.uncommitted].every((f) => f.right <= v.width), `a row sticks out of the phone: ${JSON.stringify([...v.files, ...v.uncommitted].map((f) => f.right))} at ${v.width}`);
      if (SHOTS_DIR) await mp.screenshot({ path: path.join(SHOTS_DIR, "assistant-projects-changes-phone.png") });
      await mp.tap(`${rowOf("src/keep.txt")} [data-assistant-changed-toggle]`);
      const d = await untilDiff(mp, "src/keep.txt", (x) => x.drawn, "the phone's sheet did not draw the diff");
      assert(JSON.stringify(d.added) === JSON.stringify(["keep 4"]) && d.right <= d.width, `the phone's diff reads ${JSON.stringify(d)}`);
      if (SHOTS_DIR) {
        await mp.waitForFunction(() => !document.querySelector(".collapsing"));
        await mp.screenshot({ path: path.join(SHOTS_DIR, "assistant-projects-diff-phone.png") });
      }
      await mp.tap(`${PANE} [data-assistant-project-back]`);
      await until(mp, (w) => !w.open && w.cards, "Back did not return to the phone's cards");
    });

    await run("a guest reads Changes without a control, a file leading into the share only where the link grants the project", async () => {
      const made = await post(page, "/settings/sharing/new", [["amount", "1"], ["unit", "hours"], ["name", `apcg-${TAG}`], ["assistant", owner], ["project", REPO]]);
      const link = (made.text.match(/value="([^"]*\/share\/[^"]+)"/) || [])[1];
      assert(made.ok && link, `making a link answered ${made.status}`);
      linkId = await page.evaluate(async (name) => {
        const doc = new DOMParser().parseFromString(await (await fetch("/settings/sharing")).text(), "text/html");
        return [...doc.querySelectorAll("[data-share-row]")].find((r) => r.querySelector("[data-share-name]")?.textContent.trim() === name)?.dataset.shareRow || "";
      }, `apcg-${TAG}`);
      assert(linkId, "the link is not listed");
      const base = new URL(link).pathname;
      guest = await guestPage(browser, bag, { requests: [] }, ORIGIN);
      const gp = guest.page;
      await openPage(gp, `${BASE}${base}/assistants/${owner}`);
      const granted = await openChanges(gp, REPO);
      assert(granted.controls === 0 && granted.files.length === 10, `the guest's view offers ${JSON.stringify(granted)}`);
      assert(granted.uncommitted.length === 2 && granted.uncommitted.every((f) => f.href === `${base}${commitURL(f.path)}`), `the guest's uncommitted rows lead to ${JSON.stringify(granted.uncommitted)}`);
      await unfold(gp, "src/keep.txt", "uncommitted");
      const head = await untilDiff(gp, "src/keep.txt", (x) => x.drawn, "the guest's uncommitted diff was not drawn", 30000, "uncommitted");
      assert(JSON.stringify(head.added) === JSON.stringify(["keep 4"]) && head.href === `${base}${commitURL("src/keep.txt")}`, `the guest's uncommitted diff reads ${JSON.stringify(head)}`);
      const unfolded = await moreFiles(gp, true, 15);
      assert(unfolded.files.every((f) => f.href === `${base}${editorURL(f.path)}`), `the guest's rows lead to ${JSON.stringify(unfolded.files.map((f) => f.href))}`);
      await unfold(gp, "src/keep.txt");
      const mine = await untilDiff(gp, "src/keep.txt", (x) => x.drawn, "the guest's diff was not drawn");
      assert(JSON.stringify(mine.added) === JSON.stringify(["keep 4"]) && mine.href === `${base}${editorURL("src/keep.txt")}`, `the guest's diff reads ${JSON.stringify(mine)}`);
      await gp.click(`${PANE} [data-assistant-project-back]`);
      const off = await openChanges(gp, PLAIN);
      assert(off.controls === 0 && off.files.length === 1 && off.files.every((f) => !f.href) && !off.uncommittedShown, `the guest's view off the link reads ${JSON.stringify(off)}`);
      await unfold(gp, "a.txt");
      const theirs = await untilDiff(gp, "a.txt", (x) => x.drawn, "the guest's diff off the link was not drawn");
      assert(JSON.stringify(theirs.added) === JSON.stringify(["b"]) && !theirs.href, `the guest's diff off the link reads ${JSON.stringify(theirs)}`);
      await gp.click(`${PANE} [data-assistant-project-back]`);
      await until(gp, (w) => !w.open && w.cards, "the guest's Back did not return to the cards");
    });
  } finally {
    if (guest) await guest.ctx.close().catch(() => {});
    if (linkId) await post(page, `/settings/sharing/${linkId}`, { form: "revoke" }).catch(() => {});
    for (const id of coders) await post(page, `/coders/${id}/delete`, {}).catch(() => {});
    if (owner) await post(page, `/assistants/${owner}`, { form: "delete" }).catch(() => {});
    if (hostEdit) await post(page, hostEdit, { form: "delete" }).catch(() => {});
    for (const name of [REPO, PLAIN]) await L.deleteProject(page, name).catch(() => {});
  }
});
