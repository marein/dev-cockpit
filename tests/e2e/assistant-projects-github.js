const L = require("./lib");
const { assert, BASE, sleep } = L;
const fs = require("fs");
const path = require("path");
const { execFileSync } = require("child_process");
const { guestPage } = require("./project-share-guest");
const { startFakeForge, addHost } = require("./forge-fake");

// The whole Projects tab of an assistant's aside on the fake GitHub
// Enterprise Server, chromium, in the dark scheme where nothing says
// otherwise: the card of a project bound to GitHub with its pull request,
// the checks with the job that failed them, Needs you with its reason, the
// open threads and the comment a publish would post; Re-run runs the failed
// jobs of the workflow run again and the card follows live. The project view
// lists the comments of the conversation first as project comments taking
// Hand off alone, the review threads of the pull request marked GitHub with
// the outdated one marked, the resolved one folded; Reply and Resolve write
// over GraphQL and the view follows live, Hand off names a thread by its node
// id, Publish as review posts the local comment as a review comment that
// comes back as a thread. Every text of the cards, Reviews and Changes with
// an unfolded diff, the diff's own lines aside, holds 4.5:1 against its
// background and every icon 3:1 in the light and the dark scheme, and the
// card's changes and review lines and a file row of Changes keep their icons
// and their coloured text so hovered, pressed and focused. A phone's sheet
// holds the card and the view inside its width, a guest of a link to the
// assistant reads both without a control.
//
// Instance like assistant-projects-reviews.js's (tests/e2e/fakes first on
// PATH, a scratch HOME, its --projects-dir mounted into the runner at the
// same path). The fake forge runs inside the runner on FORGE_PORT and
// FORGE_TLS_PORT (default 3157 and 3158), published on the host's loopback,
// where the instance reaches it at FORGE_ADDR (default 127.0.0.1). SHOTS_DIR
// with a mount takes a dark desktop, a light desktop and a dark phone
// screenshot. About a minute.
const TAG = Date.now().toString(36).slice(-5);
const SHOTS_DIR = process.env.SHOTS_DIR || "";
const FAKE_PORT = Number(process.env.FORGE_PORT || 3157);
const FAKE_TLS_PORT = Number(process.env.FORGE_TLS_PORT || 3158);
const FAKE_ADDR = process.env.FORGE_ADDR || "127.0.0.1";
const TOKEN = `ghp_e2e${Date.now().toString(36)}Gh6tP2xQ`;
const ORIGIN = new URL(BASE).origin;
const REPO = `apgh${TAG}`;
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

async function openPage(p, url) {
  await p.goto(url, { waitUntil: "domcontentloaded" });
  await L.dismissUpdate(p);
  await p.waitForSelector(`${PANE} [data-assistant-body]`, { state: "attached", timeout: 15000 });
}

// tab reads the Projects tab as it stands on the screen: the card of the
// repository by its lines, or the open project with its threads.
const tab = (p, name) => p.evaluate(({ pane, name }) => {
  const shown = (el) => !!el && el.getClientRects().length > 0 && getComputedStyle(el).visibility !== "hidden";
  const text = (el) => (shown(el) ? el.textContent.replace(/\s+/g, " ").trim() : "");
  const card = document.querySelector(`${pane} [data-assistant-project="${name}"]`);
  const root = document.querySelector(`${pane} [data-assistant-project-view-body]`);
  const thread = (el) => ({
    id: el.dataset.thread || "",
    cid: el.dataset.lineThread || "",
    project: shown(el.querySelector("[data-line-thread-project]")),
    origin: text(el.querySelector("[data-line-thread-origin]")),
    file: text(el.querySelector("[data-line-thread-file]")),
    quote: text(el.querySelector("[data-line-thread-quote]")),
    outdated: shown(el.querySelector("[data-line-thread-outdated]")),
    notes: [...el.querySelectorAll("[data-comment-note]")].filter(shown).map((n) => `${text(n.querySelector("[data-comment-note-author]"))}: ${text(n.querySelector("[data-comment-note-text]"))}`),
    actions: [...el.querySelectorAll("[data-line-thread-actions] button")].filter(shown).map(text),
    right: el.getBoundingClientRect().right,
  });
  return {
    card: card && shown(card) ? {
      icon: card.querySelector(".dc-term-icon .ti")?.className || "",
      needs: text(card.querySelector("[data-assistant-project-needs]")),
      needsWhy: text(card.querySelector("[data-assistant-project-needs-why]")),
      change: text(card.querySelector("[data-assistant-project-change]")),
      changeHref: card.querySelector("[data-assistant-project-change-link]")?.getAttribute("href") || "",
      pipeline: card.querySelector("[data-assistant-project-pipeline]")?.dataset.assistantProjectPipeline || "",
      note: text(card.querySelector("[data-assistant-project-pipeline-note]")),
      stages: [...card.querySelectorAll("[data-assistant-project-stage]")].map((bar) => bar.dataset.assistantProjectStage),
      retry: text(card.querySelector("[data-assistant-project-retry]")),
      review: text(card.querySelector("[data-assistant-project-review]")),
      changes: text(card.querySelector("[data-assistant-project-changes]")),
      right: card.getBoundingClientRect().right,
    } : null,
    open: shown(root),
    count: text(root?.querySelector("[data-assistant-project-reviews-count]")),
    unpublished: text(root?.querySelector("[data-assistant-project-unpublished-box]")),
    threads: [...(root?.querySelectorAll("[data-assistant-project-threads-open] [data-line-thread]") || [])].map(thread),
    resolvedToggle: shown(root?.querySelector("[data-assistant-project-resolved-toggle]")) ? root.querySelector("[data-assistant-project-resolved-toggle]").innerText.replace(/\s+/g, " ").trim() : "",
    resolvedOpen: root?.querySelector("[data-assistant-project-resolved-toggle]")?.getAttribute("aria-expanded") || "",
    resolved: [...(root?.querySelectorAll("[data-assistant-project-threads-resolved] [data-line-thread]") || [])].map(thread),
    controls: root ? [...root.querySelectorAll("button, form, textarea, input")].filter((el) => !el.matches("[data-assistant-project-back], [data-assistant-project-switch], [data-assistant-project-resolved-toggle], [data-assistant-changed-toggle], [data-assistant-changed-more], [data-assistant-changes-older]")).length : 0,
    width: window.innerWidth,
  };
}, { pane: PANE, name });

async function until(p, want, what, timeout = 30000) {
  const end = Date.now() + timeout;
  let last = null;
  while (Date.now() < end) {
    last = await tab(p, REPO);
    if (want(last)) return last;
    await sleep(250);
  }
  throw new Error(`${what}: ${JSON.stringify(last)}`);
}

// unreadable lists every text under root that stands against the
// background behind it below 4.5:1 and every icon below 3:1, WCAG AA, the
// backgrounds of its ancestors laid over each other, with the scheme the
// page paints in. Colours are read through a canvas, which takes every
// colour syntax the browser computes. The lines of a drawn diff wear the
// editor's colours and stay out.
const unreadable = (p, root = PANE) => p.evaluate((root) => {
  const ctx = Object.assign(document.createElement("canvas"), { width: 1, height: 1 }).getContext("2d", { willReadFrequently: true });
  const parse = (value) => {
    ctx.clearRect(0, 0, 1, 1);
    ctx.fillStyle = "rgba(0, 0, 0, 0)";
    ctx.fillStyle = value;
    ctx.fillRect(0, 0, 1, 1);
    const [r, g, b, a] = ctx.getImageData(0, 0, 1, 1).data;
    return { r, g, b, a: a / 255 };
  };
  const over = (top, under) => ({ r: top.r * top.a + under.r * (1 - top.a), g: top.g * top.a + under.g * (1 - top.a), b: top.b * top.a + under.b * (1 - top.a), a: 1 });
  const behind = (el) => {
    const layers = [];
    for (let node = el; node; node = node.parentElement) {
      const c = parse(getComputedStyle(node).backgroundColor);
      if (c.a > 0) layers.push(c);
      if (c.a >= 1) break;
    }
    let base = parse(getComputedStyle(document.body).backgroundColor);
    for (const layer of layers.reverse()) base = over(layer, base);
    return base;
  };
  const opacity = (el) => {
    let o = 1;
    for (let node = el; node; node = node.parentElement) o *= Number(getComputedStyle(node).opacity);
    return o;
  };
  const lum = (c) => {
    const ch = (v) => {
      const s = v / 255;
      return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
    };
    return 0.2126 * ch(c.r) + 0.7152 * ch(c.g) + 0.0722 * ch(c.b);
  };
  const bad = [];
  let texts = 0;
  let icons = 0;
  const top = document.querySelector(root);
  for (const el of top ? [top, ...top.querySelectorAll("*")] : []) {
    if (!el.getClientRects().length || getComputedStyle(el).visibility === "hidden" || el.closest(".collapse:not(.show), .collapsing, [hidden], .cm-editor") || el.closest(":disabled")) continue;
    const icon = el.matches(".ti");
    if (!icon && ![...el.childNodes].some((n) => n.nodeType === 3 && n.textContent.trim())) continue;
    if (icon) icons += 1;
    else texts += 1;
    const back = behind(el);
    const color = parse(getComputedStyle(el).color);
    const fore = over({ ...color, a: color.a * opacity(el) }, back);
    const [hi, lo] = [lum(fore), lum(back)].sort((a, b) => b - a);
    const ratio = (hi + 0.05) / (lo + 0.05);
    if (ratio < (icon ? 3 : 4.5)) bad.push(`${icon ? (el.className.match(/ti-[\w-]+/) || ["icon"])[0] : el.textContent.trim().slice(0, 30)} ${ratio.toFixed(2)}`);
  }
  return { scheme: document.documentElement.getAttribute("data-bs-theme"), texts, icons, bad };
}, root);

// states reads a row hovered, focused from the keyboard and pressed: in each
// it keeps its icons and every text and icon in it stays readable.
async function states(p, sel) {
  const reads = [];
  const box = await p.locator(sel).boundingBox();
  await p.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await sleep(300);
  reads.push(["hovered", await unreadable(p, sel)]);
  await p.mouse.down();
  await sleep(150);
  reads.push(["pressed", await unreadable(p, sel)]);
  await p.mouse.move(1, 1);
  await p.mouse.up();
  await p.evaluate(() => window.getSelection()?.removeAllRanges());
  await p.keyboard.press("Shift");
  await p.evaluate((sel) => document.querySelector(sel).focus(), sel);
  await sleep(300);
  assert(await p.evaluate((sel) => document.querySelector(sel).matches(":focus-visible"), sel), `${sel} took no visible focus`);
  reads.push(["focused", await unreadable(p, sel)]);
  await p.evaluate(() => document.activeElement?.blur());
  await sleep(300);
  return reads;
}

L.runFeature("assistant-projects-github", async ({ browser, page, run, mobilePage, bag }) => {
  const fake = await startFakeForge({ port: FAKE_PORT, tlsPort: FAKE_TLS_PORT, token: TOKEN });
  const state = fake.state;
  const fakeWeb = `http://${FAKE_ADDR}:${FAKE_PORT}`;
  const coders = [];
  let owner = "";
  let repoDir = "";
  let hostEdit = "";
  let mineId = "";
  let linkId = "";
  let guest = null;
  const refresh = () => page.evaluate(async (p) => (await fetch(`/projects/${p}/forge/status?fresh=1`, { headers: { Accept: "application/json" } })).status, REPO);
  const threadSel = (id) => `${PANE} [data-line-thread][data-thread="${id}"]`;
  const toast = (p, words) => p.waitForFunction((w) => [...document.querySelectorAll(".dc-toast")].some((el) => el.textContent.includes(w)), words, { timeout: 15000 });
  const openReviews = async (p) => {
    await p.click(`${PANE} [data-assistant-project="${REPO}"] [data-assistant-project-review]`);
    return until(p, (v) => v.open && v.threads.length > 0, "the project did not open");
  };
  try {
    await run("setup: a repository on the fake GitHub with a pull request, failed checks, review threads and a comment to publish", async () => {
      await page.emulateMedia({ colorScheme: "dark" });
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
      git(repoDir, "remote", "add", "origin", `${fakeWeb}/octo/app.git`);
      fs.appendFileSync(path.join(repoDir, FILE), "var more = 1\n");
      Object.assign(state, { head: pushed, ghRun: "failure", ghTest: "failure", ghStatus: "success", diffFiles: [{ path: FILE, patch: PATCH }], published: [], replies: [], resolved: [] });
      const added = await addHost(page, { kind: "github", web_url: fakeWeb, aliases: `apgh-${TAG}`, token: TOKEN });
      assert(!added.failed, added.failed);
      hostEdit = await page.evaluate(async (mark) => {
        const html = await (await fetch("/settings/forges")).text();
        const doc = new DOMParser().parseFromString(html, "text/html");
        return [...doc.querySelectorAll("[data-forge-host]")].find((r) => r.textContent.includes(mark))?.querySelector("[data-forge-edit]")?.getAttribute("href") || "";
      }, `apgh-${TAG}`);
      assert(hostEdit, "the added host has no Edit on the list");
      mineId = (await postJSON(page, `/projects/${REPO}/editor/comments`, { path: FILE, line: 5, text: "Mine to publish" }, { json: true })).comment?.id;
      assert(mineId, "the line comment was not made");
      const made = await postJSON(page, "/coders/new", { name: `apgh-work-${TAG}`, project: repoDir, coder: "claude", automatic_approval: "on", prompt: `job-apgh-${TAG}` });
      assert(made.id, `no coder made: ${JSON.stringify(made)}`);
      coders.push(made.id);
      await postJSON(page, "/assistants/jobs", { form: "steer", assistant: owner, terminal: made.id, task: "GitHub check", done_when: "" });
    });

    await run("the card shows the pull request, the failed checks with Re-run, Needs you with its reason and the threads, in the dark scheme", async () => {
      await page.setViewportSize({ width: 1360, height: 900 });
      await openPage(page, `${BASE}/assistants/${owner}`);
      assert(await refresh() === 200, "the fresh read failed");
      const v = await until(page, (w) => w.card?.pipeline === "failed" && /open thread/.test(w.card.review), "the card did not take the forge's read", 45000);
      const c = v.card;
      assert(c.icon.includes("ti-brand-github") && c.change === "#7 Fix the login open" && c.changeHref === "https://github.example.com/octo/app/pull/7", `the pull request reads ${JSON.stringify(c)}`);
      assert(c.needs === "Needs you" && c.needsWhy === "The pipeline failed." && c.retry === "Re-run" && c.note === "test +1", `the checks read ${JSON.stringify(c)}`);
      assert(c.review === "5 open threads · 1 unpublished comment" && c.changes === "1 commit · 1 file · +9 −0 · 1 uncommitted", `the counts read ${JSON.stringify(c)}`);
      assert(await page.evaluate(() => document.documentElement.getAttribute("data-bs-theme")) === "dark", "the page does not paint dark");
      if (SHOTS_DIR) await page.screenshot({ path: path.join(SHOTS_DIR, "assistant-projects-github-desktop-dark.png") });
      return `${c.change}; ${c.note}; ${c.review}`;
    });

    await run("Re-run runs the failed jobs of the workflow run again, the card follows live", async () => {
      await page.evaluate(() => { window.__noReload = 1; });
      await page.click(`${PANE} [data-assistant-project="${REPO}"] [data-assistant-project-retry]`);
      await page.waitForFunction(() => !!document.querySelector(".dc-toast"), null, { timeout: 15000 });
      assert(state.retried.includes("/actions/runs/501/rerun-failed-jobs"), `the forge ran ${JSON.stringify(state.retried)}`);
      const v = await until(page, (w) => w.card && w.card.note !== "test +1", "the card did not follow the re-run", 45000);
      assert(!v.card.retry, `the re-run card reads ${JSON.stringify(v.card)}`);
      assert(await page.evaluate(() => window.__noReload === 1), "the page reloaded");
      return `${v.card.pipeline} ${v.card.note}`;
    });

    await run("Reviews lists the conversation first, the review threads marked GitHub, Hand off names the node id, Reply and Resolve go over GraphQL, live", async () => {
      await page.evaluate(() => { window.__noReload = 1; });
      const v = await openReviews(page);
      const general = v.threads.filter((t) => t.project);
      const lines = v.threads.filter((t) => !t.project);
      assert(v.threads.findIndex((t) => !t.project) === general.length && general.length === 2, `the project comments do not stand first: ${JSON.stringify(v.threads)}`);
      assert(general.every((t) => t.origin === "GitHub" && !t.id && JSON.stringify(t.actions) === JSON.stringify(["Hand off"])), `the conversation reads ${JSON.stringify(general)}`);
      const first = lines.find((t) => t.id === "PRRT_kwDOe2e1");
      const outdated = lines.find((t) => t.id === "PRRT_kwDOe2e2");
      const mine = lines.find((t) => t.cid === mineId);
      assert(first && first.origin === "GitHub" && first.file === `${FILE}:4` && first.quote === "name := bb" && !first.outdated && JSON.stringify(first.notes) === JSON.stringify(["octocat: New name bb?"]), `the review thread reads ${JSON.stringify(first)}`);
      assert(JSON.stringify(first.actions) === JSON.stringify(["Reply", "Hand off", "Resolve"]), `the review thread offers ${JSON.stringify(first.actions)}`);
      assert(outdated && outdated.outdated && JSON.stringify(outdated.notes) === JSON.stringify(["octocat: Added e"]), `the outdated thread reads ${JSON.stringify(outdated)}`);
      assert(mine && mine.origin === "local", `the local comment reads ${JSON.stringify(mine)}`);
      assert(v.count === "5" && v.resolvedToggle === "Show 1 resolved thread" && /^1 line comment not published Publish as review$/.test(v.unpublished), `the view reads ${JSON.stringify(v)}`);
      await page.click(`${threadSel("PRRT_kwDOe2e1")} [data-line-thread-handoff]`);
      const want = `Review thread PRRT_kwDOe2e1 in project ${REPO} on branch feature at ${FILE}:4.\n`;
      await page.waitForFunction(([sel, w]) => document.querySelector(sel)?.value === w, ["[data-assistant-input]", want], { timeout: 5000 });
      await page.fill("[data-assistant-input]", "");
      await sleep(700);
      await page.click(`${threadSel("PRRT_kwDOe2e1")} [data-line-thread-reply-open]`);
      await page.fill(`${threadSel("PRRT_kwDOe2e1")} [data-line-thread-reply-text]`, "Renamed in abc.");
      await page.click(`${threadSel("PRRT_kwDOe2e1")} [data-line-thread-reply-send]`);
      await toast(page, "The reply is posted on pull request #7.");
      assert(state.replies.some((r) => r.kind === "github" && r.thread === "PRRT_kwDOe2e1" && r.body === "Renamed in abc."), `the forge holds ${JSON.stringify(state.replies)}`);
      await until(page, (w) => JSON.stringify(w.threads.find((t) => t.id === "PRRT_kwDOe2e1")?.notes) === JSON.stringify(["octocat: New name bb?", "octocat: Renamed in abc."]), "the reply did not reach the view");
      await page.click(`${threadSel("PRRT_kwDOe2e1")} [data-line-thread-resolve]`);
      await toast(page, "The thread is resolved on pull request #7.");
      assert(state.resolved.includes("PRRT_kwDOe2e1"), `the forge resolved ${JSON.stringify(state.resolved)}`);
      await until(page, (w) => w.count === "4" && w.resolvedToggle === "Show 2 resolved threads" && !w.threads.some((t) => t.id === "PRRT_kwDOe2e1"), "the resolved thread did not fold away");
      assert(await page.evaluate(() => window.__noReload === 1), "the page reloaded");
    });

    await run("Publish as review posts the local comment on the pull request, it comes back as a thread of GitHub", async () => {
      await page.evaluate(() => { window.__noReload = 1; });
      await page.click(`${PANE} [data-assistant-project-publish]`);
      await page.waitForSelector(DIALOG, { state: "visible", timeout: 5000 });
      const asked = (await page.locator(DIALOG).textContent()).replace(/\s+/g, " ");
      assert(asked.includes("Publish the comment?") && asked.includes("It becomes a review thread on pull request #7 on GitHub"), `the confirm reads ${asked}`);
      await page.click(`${DIALOG} .swal2-confirm`);
      await toast(page, "The comment is published on pull request #7.");
      assert(state.published.some((n) => n.kind === "github" && n.body === "Mine to publish" && n.line === 5), `the forge holds ${JSON.stringify(state.published)}`);
      const v = await until(page, (w) => !w.unpublished && w.threads.some((t) => t.origin === "GitHub" && t.id && JSON.stringify(t.notes) === JSON.stringify(["octocat: Mine to publish"])), "the published comment did not come back as a thread", 45000);
      assert(v.count === "4" && !v.threads.some((t) => t.cid === mineId && t.origin === "local"), `the view reads ${JSON.stringify(v.threads)}`);
      await page.click(`${PANE} [data-assistant-project-back]`);
      await until(page, (w) => !w.open && w.card?.review === "4 open threads", "the card did not follow the publish");
      assert(await page.evaluate(() => window.__noReload === 1), "the page reloaded");
    });

    await run("every text of the cards, Reviews and Changes with a diff holds 4.5:1 and every icon 3:1, light and dark", async () => {
      const seen = [];
      for (const scheme of ["dark", "light"]) {
        await page.emulateMedia({ colorScheme: scheme });
        await openPage(page, `${BASE}/assistants/${owner}`);
        await until(page, (w) => !!w.card?.review, "the card did not stand");
        await page.mouse.move(1, 1);
        await sleep(300);
        const cards = await unreadable(page);
        await openReviews(page);
        await page.click(`${PANE} [data-assistant-project-resolved-toggle]`);
        await until(page, (w) => w.resolved.length > 0 && w.resolved.every((t) => t.notes.length > 0), "the resolved threads did not unfold");
        await page.mouse.move(1, 1);
        await sleep(300);
        const reviews = await unreadable(page);
        await page.click(`${PANE} [data-assistant-project-switch="changes"]`);
        await page.click(`${PANE} [data-assistant-changed-files="uncommitted"] [data-assistant-changed-toggle]`);
        await page.waitForSelector(`${PANE} [data-assistant-changed-files="uncommitted"] .cm-editor`, { state: "visible", timeout: 15000 });
        await page.waitForFunction(() => !document.querySelector(".collapsing"));
        await page.mouse.move(1, 1);
        await sleep(300);
        const changes = await unreadable(page);
        if (SHOTS_DIR && scheme === "light") await page.screenshot({ path: path.join(SHOTS_DIR, "assistant-projects-github-desktop-light.png") });
        for (const [where, read] of [["cards", cards], ["Reviews", reviews], ["Changes", changes]]) {
          assert(read.scheme === scheme && read.texts > 5 && read.icons > 3 && read.bad.length === 0, `${where} in ${scheme}: ${JSON.stringify(read)}`);
          seen.push(`${where} ${scheme} ${read.texts}+${read.icons}`);
        }
      }
      await page.emulateMedia({ colorScheme: "dark" });
      return seen.join(", ");
    });

    await run("a hovered, pressed and focused row of the cards and of Changes keeps its icon and its coloured text, light and dark", async () => {
      const card = `${PANE} [data-assistant-project="${REPO}"]`;
      const file = `${PANE} [data-assistant-changed-files="files"] [data-assistant-changed-toggle]`;
      const seen = [];
      for (const scheme of ["dark", "light"]) {
        await page.emulateMedia({ colorScheme: scheme });
        await openPage(page, `${BASE}/assistants/${owner}`);
        await until(page, (w) => !!w.card?.changes, "the card did not stand");
        const rows = [["changes line", `${card} [data-assistant-project-changes]`, 2, 3], ["review line", `${card} [data-assistant-project-review]`, 2, 0]];
        const read = async (name, sel, icons, coloured) => {
          const tones = await page.evaluate((sel) => document.querySelector(sel).querySelectorAll(".text-green, .text-red, .text-warning, .text-yellow").length, sel);
          assert(tones >= coloured, `the ${name} wears ${tones} coloured texts`);
          for (const [how, r] of await states(page, sel)) {
            assert(r.scheme === scheme && r.icons === icons && r.bad.length === 0, `the ${how} ${name} in ${scheme}: ${JSON.stringify(r)}`);
          }
          seen.push(`${name} ${scheme}`);
        };
        for (const row of rows) await read(...row);
        await page.click(`${card} [data-assistant-project-changes]`);
        await page.waitForSelector(file, { state: "visible", timeout: 15000 });
        await read("file row", file, 0, 3);
        await page.click(`${PANE} [data-assistant-project-back]`);
      }
      await page.emulateMedia({ colorScheme: "dark" });
      return seen.join(", ");
    });

    await run("on a phone the sheet holds the card and the project inside its width, dark", async () => {
      const mp = await mobilePage();
      await mp.emulateMedia({ colorScheme: "dark" });
      await openPage(mp, `${BASE}/assistants/${owner}`);
      await mp.tap("[data-assistant-watching-button]");
      await mp.waitForSelector(`#assistant-aside.show [data-assistant-project="${REPO}"] [data-assistant-project-review]`, { state: "visible", timeout: 10000 });
      await sleep(400);
      const c = (await tab(mp, REPO));
      assert(c.card && c.card.right <= c.width && c.card.change === "#7 Fix the login open", `the phone's card reads ${JSON.stringify(c)}`);
      await mp.tap(`${PANE} [data-assistant-project="${REPO}"] [data-assistant-project-review]`);
      const v = await until(mp, (w) => w.open && w.threads.length === 4, "the phone's sheet did not open the project");
      assert(v.threads.every((t) => t.right <= v.width), `a thread sticks out of the phone: ${JSON.stringify(v.threads.map((t) => t.right))} at ${v.width}`);
      if (SHOTS_DIR) await mp.screenshot({ path: path.join(SHOTS_DIR, "assistant-projects-github-phone-dark.png") });
      await mp.tap(`${PANE} [data-assistant-project-back]`);
    });

    await run("a guest of a link reads the card and the project without a control", async () => {
      const made = await post(page, "/settings/sharing/new", [["amount", "1"], ["unit", "hours"], ["name", `apgh-${TAG}`], ["assistant", owner]]);
      const link = (made.text.match(/value="([^"]*\/share\/[^"]+)"/) || [])[1];
      assert(made.ok && link, `making a link answered ${made.status}`);
      linkId = await page.evaluate(async (name) => {
        const doc = new DOMParser().parseFromString(await (await fetch("/settings/sharing")).text(), "text/html");
        return [...doc.querySelectorAll("[data-share-row]")].find((r) => r.querySelector("[data-share-name]")?.textContent.trim() === name)?.dataset.shareRow || "";
      }, `apgh-${TAG}`);
      assert(linkId, "the link is not listed");
      guest = await guestPage(browser, bag, { requests: [] }, ORIGIN);
      const gp = guest.page;
      await gp.emulateMedia({ colorScheme: "dark" });
      await openPage(gp, `${BASE}${new URL(link).pathname}/assistants/${owner}`);
      const c = await until(gp, (w) => w.card?.change === "#7 Fix the login open", "the guest's card lacks the pull request");
      assert(!c.card.retry, `the guest's card offers ${JSON.stringify(c.card)}`);
      const v = await openReviews(gp);
      assert(v.controls === 0 && v.threads.length === 4 && v.threads.every((t) => !t.id && t.actions.length === 0) && !v.unpublished, `the guest's view offers ${JSON.stringify(v)}`);
      assert(v.threads.filter((t) => t.project).length === 2 && v.threads.some((t) => t.outdated), `the guest's threads read ${JSON.stringify(v.threads)}`);
    });
  } finally {
    if (guest) await guest.ctx.close().catch(() => {});
    if (linkId) await post(page, `/settings/sharing/${linkId}`, { form: "revoke" }).catch(() => {});
    for (const id of coders) await post(page, `/coders/${id}/delete`, {}).catch(() => {});
    if (owner) await post(page, `/assistants/${owner}`, { form: "delete" }).catch(() => {});
    if (hostEdit) await post(page, hostEdit, { form: "delete" }).catch(() => {});
    await L.deleteProject(page, REPO).catch(() => {});
  }
});
