const L = require("./lib");
const { assert, BASE, sleep } = L;
const fs = require("fs");
const path = require("path");
const { execFileSync } = require("child_process");
const { randomUUID } = require("crypto");

// The aside of an assistant's page coming back where the reader left it, per
// assistant and per device (@dc/assistant-place, localStorage
// dc-assistant-aside:<id>), chromium and webkit: the open tab, the project
// opened in Projects on its section with an unfolded rest of a list and a
// drawn diff, the scroll of the aside and of the cards behind the project,
// which Projects leads back to, the unfolded Details of a job and a trigger,
// the older coders of a card and the older projects. A project open behind
// Coders leaves the scroll of Coders alone over a reload. A reload paints no
// frame of another place: a sampler reads every frame the aside shows, also
// under a slowed project pull. A wait past about 300 ms shows a spinner in
// the aside's place, which leaves in the frame the aside shows, a fast
// restore and a load without a place show none. A blocked module and a pull
// that hangs still show the aside within about 2 seconds, the spinner then
// without a box. A second assistant opened over the list column keeps its
// own place and leaves the first one's alone. A project, a section and a
// fold that are gone fall back to the cards and the plain tab without a
// toast, and the next write drops them. A pull of a list,
// a pull of the open project and a surface swapped in keep the place on the
// screen and in the store. The phone's sheet comes back the same way. A place
// leaves with its assistant: one deleted elsewhere goes on the next load of
// the list column, one deleted in the editor's picker at once, a page without
// the whole list (the projects page, a share page) removes none. A storage
// that throws on every call loads the aside and lets every tab, fold and
// project work. At most 50 places stay on a device, the ones looked at
// longest ago go first.
//
// Instance like assistant-projects.js's: tests/e2e/fakes first on PATH (the
// coders and the answer run on the fake claude), a scratch HOME, its own
// --projects-dir, mounted into the runner at the same path, because the
// runner builds a repository there with the container's git. About two
// minutes per engine.
const TAG = Date.now().toString(36).slice(-5);
const PREFIX = "dc-assistant-aside:";
const BODY = "#assistant-aside .dc-assistant-aside-body";
const VIEW = (u) => u.pathname === "/assistants/projects/view";

// sampler records, on every frame, what the aside shows: whether its body is
// visible, whether the spinner of its wait is, the open tab, the project and
// section of the view, whether the cards stand and the scroll. It runs from
// the first frame of every load.
function sampler() {
  window.__frames = [];
  let ticks = 0;
  const tick = (t) => {
    const body = document.querySelector("#assistant-aside .dc-assistant-aside-body");
    const active = body?.querySelector("dc-assistant-list.tab-pane.active");
    const spin = document.querySelector("#assistant-aside .dc-aside-wait .spinner-border");
    if (body && active) {
      const view = active.querySelector(":scope > [data-assistant-project-view]");
      const cards = active.querySelector(":scope > [data-assistant-body]");
      window.__frames.push({
        t: Math.round(t),
        shown: getComputedStyle(body).opacity !== "0",
        wait: !!spin && spin.getClientRects().length > 0 && spin.checkVisibility({ opacityProperty: true, visibilityProperty: true }),
        tab: active.getAttribute("pane") || "",
        view: view && !view.hidden ? `${view.dataset.project}/${view.dataset.section}` : "",
        cards: !!cards && !cards.hidden,
        top: Math.round(body.scrollTop),
      });
    }
    if (++ticks < 900) requestAnimationFrame(tick);
  };
  requestAnimationFrame(tick);
}

const frames = (p) => p.evaluate(() => window.__frames || []);

// waiting reads the spinner off the frames of a load: how many showed it,
// when it first did after the first frame, and whether it stood in one run
// that ended in the frame before the aside showed.
function waiting(all) {
  const first = all.findIndex((f) => f.shown);
  const at = all.flatMap((f, i) => (f.wait ? [i] : []));
  const run = at.length && at[at.length - 1] - at[0] + 1 === at.length;
  return { count: at.length, after: at.length ? all[at[0]].t - all[0].t : -1, handover: !!run && first > 0 && at[at.length - 1] === first - 1 };
}

function git(dir, ...args) {
  return execFileSync("git", ["-c", "user.name=e2e", "-c", "user.email=e2e@localhost", "-c", "commit.gpgsign=false", "-C", dir, ...args], { encoding: "utf8" });
}

const post = (page, url, body) => page.evaluate(async ({ url, body }) => {
  const token = document.querySelector('meta[name="csrf-token"]')?.content || "";
  const r = await fetch(url, { method: "POST", headers: { "X-CSRF-Token": token, Accept: "application/json" }, body: new URLSearchParams(body) });
  return { ok: r.ok, status: r.status, text: await r.text() };
}, { url, body });

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

// open loads an assistant's page and waits until its aside landed.
async function open(p, id) {
  await p.goto(`${BASE}/assistants/${id}`, { waitUntil: "domcontentloaded" });
  await L.dismissUpdate(p);
  await landed(p, id);
}

// landed waits for the aside's lists and, where the assistant kept a place,
// for the write that ends its landing, dated after since (the page's start
// unless given). A storage that cannot be read keeps none.
async function landed(p, id, since = 0) {
  await p.waitForFunction((body) => !!document.querySelector(body) && customElements.get("dc-assistant-list"), BODY, { timeout: 15000 });
  await p.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
  await p.waitForFunction(({ key, since }) => {
    try {
      const saved = JSON.parse(localStorage.getItem(key) || "null");
      return !saved || saved.at > (since || performance.timeOrigin);
    } catch {
      return true;
    }
  }, { key: PREFIX + id, since }, { timeout: 15000 });
}

// aside reads the aside as it stands on the screen.
const aside = (p) => p.evaluate((body) => {
  const el = document.querySelector(body);
  const active = el.querySelector("dc-assistant-list.tab-pane.active");
  const projects = el.querySelector('dc-assistant-list[pane="projects"]');
  const view = projects.querySelector(":scope > [data-assistant-project-view]");
  const shown = (node) => !!node && node.getClientRects().length > 0;
  return {
    tab: active?.getAttribute("pane") || "",
    tabs: [...el.querySelectorAll('[role="tablist"] > [data-bs-toggle="tab"]')].filter((b) => b.classList.contains("active")).map((b) => b.getAttribute("aria-controls")),
    top: Math.round(el.scrollTop),
    max: el.scrollHeight - el.clientHeight,
    view: view && !view.hidden ? `${view.dataset.project}/${view.dataset.section}` : "",
    more: view?.dataset.more || "",
    cards: shown(projects.querySelector(":scope > [data-assistant-body]")),
    folds: [...el.querySelectorAll("[data-assistant-fold].show")].map((f) => f.id),
    diffs: [...el.querySelectorAll("[data-assistant-diff-view]")].filter((v) => !v.hidden && v.querySelector(".cm-editor")).length,
    waits: [...el.querySelectorAll("[data-assistant-rest] [data-assistant-rest-wait]")].filter(shown).length,
    toasts: [...document.querySelectorAll(".toast.show")].map((t) => t.textContent.trim()),
  };
}, BODY);

// places lists the stored places of the owner and of a guest.
const places = (p) => p.evaluate(() => Object.keys(localStorage).filter((k) => /^(dc-guest:)?dc-assistant-aside:/.test(k)));

const stored = (p, id) => p.evaluate((key) => {
  try {
    return JSON.parse(localStorage.getItem(key) || "null");
  } catch {
    return "unreadable";
  }
}, PREFIX + id);

// scrollTo moves the aside the way a wheel does and waits for the place to
// write it.
async function scrollTo(p, top) {
  const at = await p.evaluate(({ body, top }) => {
    const el = document.querySelector(body);
    el.scrollTop = top;
    return Math.round(el.scrollTop);
  }, { body: BODY, top });
  await sleep(400);
  return at;
}

async function tab(p, pane, tap = false) {
  const sel = `[role="tablist"] > [aria-controls="assistant-${pane}"]`;
  if (tap) await p.tap(sel);
  else await p.click(sel);
  await p.waitForFunction((pane) => document.querySelector(`#assistant-${pane}`)?.classList.contains("active"), pane, { timeout: 5000 });
}

async function unfoldBy(p, control, fold, tap = false) {
  if (tap) await p.tap(control);
  else await p.click(control);
  await p.waitForSelector(`#${fold}.show`, { state: "attached", timeout: 8000 });
  await sleep(450);
}

const seedPlace = (p, keys, at) => p.evaluate(({ keys, at }) => keys.forEach((k, i) => localStorage.setItem(k, JSON.stringify({ tab: "coders", scroll: 0, folds: [], older: [], project: null, at: at + i }))), { keys, at });

L.runFeature("assistant-place", async ({ page, run, mobilePage, browser, engine }) => {
  const REPO = `aplace${TAG}${engine[0]}`;
  const PLAIN = `aplaceplain${TAG}${engine[0]}`;
  const fx = { coders: [], triggers: [], assistants: [], olds: [] };
  await page.addInitScript(sampler);
  try {
    await run("setup: two assistants, a repository with changes, jobs and triggers", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      fx.a = (await postJSON(page, "/assistants/new", { form: "new", coder: "claude" })).id;
      fx.b = (await postJSON(page, "/assistants/new", { form: "new", coder: "claude" })).id;
      fx.assistants.push(fx.a, fx.b);
      await L.createProject(page, REPO);
      await L.createProject(page, PLAIN);
      fx.repoDir = await L.projectPath(page, REPO);
      fx.plainDir = await L.projectPath(page, PLAIN);
      assert(fs.existsSync(fx.repoDir), `the projects dir is not mounted at ${fx.repoDir}`);
      git(fx.repoDir, "init", "-q", "-b", "main");
      fs.writeFileSync(path.join(fx.repoDir, "README.md"), "base\n");
      git(fx.repoDir, "add", "README.md");
      git(fx.repoDir, "commit", "-q", "-m", "base");
      for (let i = 1; i <= 13; i++) {
        fs.mkdirSync(path.join(fx.repoDir, "src"), { recursive: true });
        fs.writeFileSync(path.join(fx.repoDir, "src", `f${String(i).padStart(2, "0")}.txt`), Array.from({ length: 4 + i * 3 }, (_, n) => `line ${n + 1} of ${i}\n`).join(""));
      }
      git(fx.repoDir, "add", "src");
      git(fx.repoDir, "commit", "-q", "-m", "Add the sources");
      for (let i = 0; i < 4; i++) {
        const id = await newCoder(page, fx.plainDir, `aplace-old-${i}-${TAG}`);
        fx.coders.push(id);
        await postJSON(page, "/assistants/jobs", { form: "steer", assistant: fx.a, terminal: id, task: `Place check ${i}`, done_when: "It is done" });
        await postJSON(page, "/assistants/jobs", { form: "release", terminal: id });
        await sleep(30);
      }
      for (let i = 0; i < 2; i++) {
        const id = await newCoder(page, fx.repoDir, `aplace-work-${i}-${TAG}`);
        fx.coders.push(id);
        await postJSON(page, "/assistants/jobs", { form: "steer", assistant: fx.a, terminal: id, task: `Place work ${i}`, done_when: "It is done" });
      }
      fx.job = fx.coders[fx.coders.length - 1];
      for (let i = 0; i < 3; i++) {
        fx.triggers.push((await postJSON(page, "/assistants/triggers", { form: "new", assistant: fx.a, event: "cron", spec: `0 ${3 + i} * * *`, task: `EVENT_NOTHING place ${i}`, batch: "0" })).id);
      }
      fx.triggers.push((await postJSON(page, "/assistants/triggers", { form: "new", assistant: fx.b, event: "cron", spec: "0 6 * * *", task: "EVENT_NOTHING theirs", batch: "0" })).id);
      fx.bTrigger = fx.triggers[fx.triggers.length - 1];
      await page.setViewportSize({ width: 1360, height: 420 });
      await open(page, fx.a);
      const first = await aside(page);
      assert(first.tab === "projects" && !first.view && first.top === 0, `a fresh assistant's aside does not open on the cards at the top: ${JSON.stringify(first)}`);
      const fresh = await frames(page);
      assert(fresh.length && fresh.every((f) => f.shown && !f.wait), `without a place a frame waited or showed the spinner: ${JSON.stringify(fresh.filter((f) => !f.shown || f.wait).slice(0, 3))}`);
      return `${fx.a} and ${fx.b}, ${fx.coders.length} jobs, ${fx.triggers.length} triggers`;
    });

    await run("a reload brings back the tab, its unfolded rows and its scroll", async () => {
      await tab(page, "coders");
      fx.jobFold = `assistant-job-${fx.a}-${fx.job}-details`;
      await unfoldBy(page, `[data-assistant-job="${fx.job}"] [data-assistant-job-fold]`, fx.jobFold);
      const before = await aside(page);
      assert(before.max > 80, `the Coders tab does not scroll at this height: ${JSON.stringify(before)}`);
      const top = await scrollTo(page, Math.round(before.max * 0.6));
      const kept = await stored(page, fx.a);
      assert(kept && kept.tab === "coders" && kept.folds.includes(fx.jobFold) && Math.abs(kept.scroll - top) <= 1 && kept.project === null,
        `the store does not hold the place: ${JSON.stringify(kept)}`);
      await open(page, fx.a);
      const back = await aside(page);
      assert(back.tab === "coders" && back.tabs.join() === "assistant-coders", `the reload opened ${JSON.stringify(back)}`);
      assert(back.folds.includes(fx.jobFold), `the job's Details did not come back unfolded: ${back.folds}`);
      assert(Math.abs(back.top - top) <= 2, `the scroll came back at ${back.top}, left at ${top}`);
      const all = await frames(page);
      const landedAt = all.find((f) => f.shown).t - all[0].t;
      assert(!waiting(all).count, `the restore took ${landedAt}ms, ${waiting(all).count} frames showed the spinner`);
      return `coders, ${fx.jobFold} unfolded, scroll ${top}, shown after ${landedAt}ms without a spinner`;
    });

    await run("a project comes back open on its section, rest and diff, at its scroll, Back finds the cards where they were", async () => {
      await tab(page, "projects");
      fx.cardFold = await page.evaluate((plain) => document.querySelector(`[data-assistant-project="${plain}"] [data-assistant-project-older]`)?.getAttribute("aria-controls"), PLAIN);
      assert(fx.cardFold, "the plain project's card has no older coders");
      await unfoldBy(page, `[data-assistant-project="${PLAIN}"] [data-assistant-project-older]`, fx.cardFold);
      const cards = await aside(page);
      assert(cards.max > 40, `the cards do not scroll at this height: ${JSON.stringify(cards)}`);
      const listTop = await scrollTo(page, Math.min(cards.max, 60));
      await page.click(`[data-assistant-project="${REPO}"] [data-assistant-project-changes]`);
      await page.waitForSelector("#assistant-projects [data-assistant-project-changes-section]", { state: "visible", timeout: 10000 });
      await page.click('#assistant-projects [data-assistant-changed-more="files"]');
      await page.waitForFunction(() => {
        const rest = document.querySelector('#assistant-projects [data-assistant-rest="files"]');
        return rest?.classList.contains("show") && !rest.querySelector("[data-assistant-rest-wait]");
      }, null, { timeout: 10000 });
      fx.fileFold = await page.evaluate(() => document.querySelector('#assistant-projects [data-assistant-changed-file="src/f13.txt"] [data-assistant-changed-toggle]')?.getAttribute("aria-controls"));
      assert(fx.fileFold, "src/f13.txt has no row in Changes");
      await unfoldBy(page, '#assistant-projects [data-assistant-changed-file="src/f13.txt"] [data-assistant-changed-toggle]', fx.fileFold);
      await page.waitForSelector(`#${fx.fileFold} [data-assistant-diff-view] .cm-editor`, { state: "visible", timeout: 15000 });
      const opened = await aside(page);
      assert(opened.view === `${REPO}/changes` && opened.more === "files" && opened.max > 100, `the view does not stand to scroll: ${JSON.stringify(opened)}`);
      const viewTop = await scrollTo(page, Math.round(opened.max * 0.7));
      const kept = await stored(page, fx.a);
      assert(kept.project && kept.project.name === REPO && kept.project.section === "changes" && kept.project.more === "files" && Math.abs(kept.project.list - listTop) <= 1,
        `the store does not hold the project: ${JSON.stringify(kept)}`);
      assert(kept.folds.includes(fx.fileFold) && kept.folds.includes(fx.cardFold) && kept.folds.includes(fx.jobFold), `the store lost a fold: ${kept.folds}`);

      await open(page, fx.a);
      await page.waitForSelector(`#${fx.fileFold} [data-assistant-diff-view] .cm-editor`, { state: "visible", timeout: 15000 });
      const back = await aside(page);
      assert(back.tab === "projects" && back.view === `${REPO}/changes` && !back.cards, `the reload did not open the project on Changes: ${JSON.stringify(back)}`);
      assert(back.more === "files" && !back.waits, `the rest of the files did not come back: ${JSON.stringify(back)}`);
      assert(back.folds.includes(fx.fileFold) && back.diffs === 1, `the file's diff did not come back drawn: ${JSON.stringify(back)}`);
      assert(Math.abs(back.top - viewTop) <= 2, `the view came back at ${back.top}, left at ${viewTop}`);
      assert(!back.toasts.length, `a toast showed: ${back.toasts}`);

      await page.click("#assistant-projects [data-assistant-project-back]");
      await page.waitForSelector(`#assistant-projects [data-assistant-project="${REPO}"]`, { state: "visible", timeout: 5000 });
      const list = await aside(page);
      assert(list.cards && !list.view && Math.abs(list.top - listTop) <= 2, `Back did not find the cards at ${listTop}: ${JSON.stringify(list)}`);
      assert(list.folds.includes(fx.cardFold), "the card's older coders folded on the way");
      await sleep(300);
      assert((await stored(page, fx.a)).project === null, "Back left the project in the store");
      return `${REPO}/changes, more=files, diff drawn, view ${viewTop}, cards ${listTop}`;
    });

    await run("a project open behind Coders keeps the scroll of Coders over a reload", async () => {
      await page.click(`[data-assistant-project="${REPO}"] [data-assistant-project-changes]`);
      await page.waitForSelector("#assistant-projects [data-assistant-project-changes-section]", { state: "visible", timeout: 10000 });
      await tab(page, "coders");
      const before = await aside(page);
      const top = await scrollTo(page, Math.round(before.max * 0.6));
      const kept = await stored(page, fx.a);
      assert(top > 40 && kept.tab === "coders" && kept.project?.name === REPO && Math.abs(kept.scroll - top) <= 1, `the store does not hold Coders over the project: ${JSON.stringify({ top, kept })}`);
      await open(page, fx.a);
      await sleep(500);
      const back = await aside(page);
      const again = await stored(page, fx.a);
      assert(back.tab === "coders" && Math.abs(back.top - top) <= 2, `Coders came back at ${back.top}, left at ${top}`);
      assert(back.view === `${REPO}/changes`, `the project behind Coders did not come back: ${JSON.stringify(back)}`);
      assert(again.tab === "coders" && Math.abs(again.scroll - top) <= 2 && again.project?.name === REPO, `the store lost the scroll of Coders: ${JSON.stringify(again)}`);
      const shown = (await frames(page)).filter((f) => f.shown);
      const off = shown.filter((f) => f.tab !== "coders" || Math.abs(f.top - top) > 2);
      assert(shown.length && !off.length, `a frame showed another place: ${JSON.stringify(off.slice(0, 3))} of ${shown.length}`);
      await tab(page, "projects");
      await page.click("#assistant-projects [data-assistant-project-back]");
      await page.waitForSelector(`#assistant-projects [data-assistant-project="${REPO}"]`, { state: "visible", timeout: 5000 });
      return `coders at ${top} over ${REPO}/changes, ${shown.length} frames`;
    });

    await run("under a slowed project pull no frame shows the cards before the project", async () => {
      await page.click(`[data-assistant-project="${REPO}"] [data-assistant-project-changes]`);
      await page.waitForSelector("#assistant-projects [data-assistant-project-changes-section]", { state: "visible", timeout: 10000 });
      const opened = await aside(page);
      assert(opened.max > 40, `the project does not scroll at this height: ${JSON.stringify(opened)}`);
      const top = await scrollTo(page, Math.round(opened.max / 2));
      await page.route(VIEW, async (route) => {
        await sleep(800);
        await route.continue().catch(() => {});
      });
      try {
        await open(page, fx.a);
      } finally {
        await page.unroute(VIEW);
      }
      const back = await aside(page);
      assert(back.tab === "projects" && back.view === `${REPO}/changes` && Math.abs(back.top - top) <= 2, `the slowed reload did not land on the project: ${JSON.stringify(back)}, left at ${top}`);
      const all = await frames(page);
      const shown = all.filter((f) => f.shown);
      const waited = all.length - shown.length;
      const off = shown.filter((f) => f.tab !== "projects" || f.view !== `${REPO}/changes` || f.cards || Math.abs(f.top - top) > 2);
      assert(waited > 0, `no frame waited for the slowed pull: ${all.length} frames`);
      assert(shown.length && !off.length, `a frame showed another place: ${JSON.stringify(off.slice(0, 3))} of ${shown.length}`);
      const spun = waiting(all);
      assert(spun.count && spun.after >= 200 && spun.after < 800, `the spinner showed ${spun.count} frames from ${spun.after}ms`);
      assert(spun.handover, `the spinner did not stand until the frame before the aside showed: ${JSON.stringify(all.filter((f) => f.wait || f.shown).slice(0, 4))}`);
      await page.click("#assistant-projects [data-assistant-project-back]");
      await page.waitForSelector(`#assistant-projects [data-assistant-project="${REPO}"]`, { state: "visible", timeout: 5000 });
      return `${waited} frames waited, the spinner ${spun.count} from ${spun.after}ms, ${shown.length} showed ${REPO}/changes at ${top}`;
    });

    await run("a second assistant keeps its own place and leaves the first one's alone", async () => {
      await page.click(`[data-assistant-project="${REPO}"] [data-assistant-project-changes]`);
      await page.waitForSelector('#assistant-projects [data-assistant-project-switch="reviews"]', { state: "visible", timeout: 10000 });
      await page.click('#assistant-projects [data-assistant-project-switch="reviews"]');
      await page.waitForFunction((repo) => document.querySelector("#assistant-projects > [data-assistant-project-view]")?.dataset.section === "reviews"
        && document.querySelector("#assistant-projects > [data-assistant-project-view]")?.dataset.project === repo, REPO, { timeout: 10000 });
      await sleep(300);
      const left = await aside(page);
      let since = await page.evaluate(() => Date.now());
      await page.click(`.dc-app > .dc-ctx [data-assistant-instance="${fx.b}"]`);
      await page.waitForFunction((b) => document.querySelector("dc-assistant")?.getAttribute("assistant-id") === b, fx.b, { timeout: 10000 });
      await landed(page, fx.b, since);
      const b = await aside(page);
      assert(b.tab === "projects" && !b.view && b.top === 0 && !b.folds.length, `B opened on A's place: ${JSON.stringify(b)}`);
      await tab(page, "triggers");
      const bFold = `assistant-trigger-${fx.bTrigger}-details`;
      await unfoldBy(page, `[data-assistant-trigger="${fx.bTrigger}"] [data-assistant-trigger-fold]`, bFold);
      since = await page.evaluate(() => Date.now());
      await page.click(`.dc-app > .dc-ctx [data-assistant-instance="${fx.a}"]`);
      await page.waitForFunction((a) => document.querySelector("dc-assistant")?.getAttribute("assistant-id") === a, fx.a, { timeout: 10000 });
      await landed(page, fx.a, since);
      const a = await aside(page);
      assert(a.tab === "projects" && a.view === `${REPO}/reviews` && Math.abs(a.top - left.top) <= 2, `A did not come back as it was left: ${JSON.stringify({ left, a })}`);
      await open(page, fx.b);
      const again = await aside(page);
      assert(again.tab === "triggers" && again.folds.includes(bFold) && !again.view, `B's reload lost its place: ${JSON.stringify(again)}`);
      const sa = await stored(page, fx.a);
      const sb = await stored(page, fx.b);
      assert(sa.project?.section === "reviews" && sb.tab === "triggers" && !sb.project, `the stores mixed: ${JSON.stringify({ sa, sb })}`);
      return `A on ${a.view}, B on triggers with ${bFold}`;
    });

    await run("a project, a section and a fold that are gone fall back quietly and are dropped", async () => {
      const gone = `gone${TAG}`;
      await page.evaluate(({ key, gone, job }) => localStorage.setItem(key, JSON.stringify({
        tab: "projects", scroll: 99999, folds: ["assistant-job-nothing-details", job], older: ["nonsense"],
        project: { name: gone, section: "changes", more: "files", list: 30 }, at: 1,
      })), { key: PREFIX + fx.a, gone, job: fx.jobFold });
      await open(page, fx.a);
      const fell = await aside(page);
      assert(fell.tab === "projects" && fell.cards && !fell.view && !fell.toasts.length, `the gone project did not fall back to the cards quietly: ${JSON.stringify(fell)}`);
      await sleep(400);
      const dropped = await stored(page, fx.a);
      assert(dropped.project === null && !dropped.folds.includes("assistant-job-nothing-details") && dropped.folds.includes(fx.jobFold) && !dropped.older.length,
        `the gone parts stayed in the store: ${JSON.stringify(dropped)}`);
      await page.evaluate(({ key, repo }) => localStorage.setItem(key, JSON.stringify({ tab: "nowhere", scroll: 0, folds: [], older: [], project: { name: repo, section: "nowhere", more: "", list: 0 }, at: 1 })), { key: PREFIX + fx.a, repo: REPO });
      await open(page, fx.a);
      const section = await aside(page);
      assert(section.tab === "projects" && section.view === `${REPO}/reviews` && !section.toasts.length, `a gone section did not fall back to Reviews: ${JSON.stringify(section)}`);
      await sleep(400);
      const fixed = await stored(page, fx.a);
      assert(fixed.tab === "projects" && fixed.project?.section === "reviews", `the store kept the gone section: ${JSON.stringify(fixed)}`);
      return "gone project to the cards, gone section to Reviews, gone fold and tab dropped";
    });

    await run("pulls of the lists and the project and a swapped surface keep the place", async () => {
      await page.click("#assistant-projects [data-assistant-project-back]");
      await tab(page, "coders");
      if (!(await page.evaluate((fold) => document.getElementById(fold)?.classList.contains("show"), fx.jobFold))) {
        await unfoldBy(page, `[data-assistant-job="${fx.job}"] [data-assistant-job-fold]`, fx.jobFold);
      }
      const top = await scrollTo(page, 100);
      const before = await stored(page, fx.a);
      await page.evaluate(() => {
        window.__noReload = 1;
        document.querySelector("dc-assistant").dcBefore = 1;
      });
      const extra = (await postJSON(page, "/assistants/triggers", { form: "new", assistant: fx.a, event: "cron", spec: "0 9 * * *", task: "EVENT_NOTHING moves the lists", batch: "0" })).id;
      fx.triggers.push(extra);
      await page.waitForSelector(`#assistant-triggers [data-assistant-trigger="${extra}"]`, { state: "attached", timeout: 10000 });
      fs.writeFileSync(path.join(fx.repoDir, "README.md"), "base\nmoved\n");
      await sleep(1500);
      const pulled = await aside(page);
      assert(pulled.tab === "coders" && pulled.folds.includes(fx.jobFold) && Math.abs(pulled.top - top) <= 2, `a pull moved the place: ${JSON.stringify(pulled)}`);
      const answers = await page.locator('[data-role="assistant"]').count();
      await page.fill("[data-assistant-input]", "MAGIC keep the aside");
      await page.click("[data-assistant-send]");
      await page.waitForFunction((n) => document.querySelectorAll('[data-role="assistant"]').length > n && !document.querySelector("dc-assistant[running]"), answers, { timeout: 20000 });
      await sleep(500);
      // A page that missed the answer pulls itself and swaps the fresh
      // surface in on the next assistant event.
      await page.evaluate(() => {
        const messages = document.querySelectorAll("[data-assistant-message][data-message-id]");
        messages[messages.length - 1].remove();
      });
      fx.triggers.push((await postJSON(page, "/assistants/triggers", { form: "new", assistant: fx.a, event: "cron", spec: "0 10 * * *", task: "EVENT_NOTHING swaps the surface", batch: "0" })).id);
      await page.waitForFunction(() => !document.querySelector("dc-assistant").dcBefore, null, { timeout: 20000 });
      await sleep(500);
      const swapped = await aside(page);
      const after = await stored(page, fx.a);
      assert(await page.evaluate(() => window.__noReload === 1), "the page reloaded");
      assert(swapped.tab === "coders" && swapped.folds.includes(fx.jobFold) && Math.abs(swapped.top - top) <= 2, `the swap moved the place: ${JSON.stringify(swapped)}, left at ${top}`);
      assert(after.tab === before.tab && after.project === before.project && after.folds.includes(fx.jobFold) && Math.abs(after.scroll - before.scroll) <= 2,
        `a pull or the swap reset the store: ${JSON.stringify({ before, after })}`);
      return `coders, fold and scroll ${swapped.top} kept over a trigger, a git change and a swapped surface`;
    });

    await run("older projects come back unfolded and fold away again", async () => {
      const root = path.dirname(fx.repoDir);
      for (let i = 0; i < 5; i++) {
        const name = `aplaceold${i}${TAG}${engine[0]}`;
        await L.createProject(page, name);
        fx.olds.push(name);
        const id = await newCoder(page, path.join(root, name), `aplace-older-${i}-${TAG}`);
        fx.coders.push(id);
        await postJSON(page, "/assistants/jobs", { form: "steer", assistant: fx.a, terminal: id, task: "Place older", done_when: "" });
        await postJSON(page, "/assistants/jobs", { form: "release", terminal: id });
      }
      const control = "#assistant-projects [data-assistant-older]";
      const read = () => page.evaluate((control) => ({
        open: document.querySelector(control)?.getAttribute("aria-expanded") || "",
        cards: document.querySelectorAll("#assistant-projects [data-assistant-project]").length,
        folded: document.querySelectorAll("#assistant-projects [data-assistant-project-folded]").length,
      }), control);
      await open(page, fx.a);
      await tab(page, "projects");
      await page.waitForSelector(control, { state: "visible", timeout: 10000 });
      const shut = await read();
      assert(shut.open === "false" && shut.folded === 1, `the older projects do not stand folded: ${JSON.stringify(shut)}`);
      await page.click(control);
      await page.waitForFunction((n) => document.querySelectorAll("#assistant-projects [data-assistant-project]").length > n, shut.cards, { timeout: 10000 });
      await sleep(300);
      assert((await stored(page, fx.a)).older.join() === "projects", `the store does not keep the older projects: ${JSON.stringify(await stored(page, fx.a))}`);
      await open(page, fx.a);
      const back = await read();
      assert(back.open === "true" && back.cards === shut.cards + 1 && !back.folded, `the older projects did not come back unfolded: ${JSON.stringify(back)}`);
      await page.click(control);
      await page.waitForFunction((n) => document.querySelectorAll("#assistant-projects [data-assistant-project]").length === n, shut.cards, { timeout: 10000 });
      await sleep(300);
      await open(page, fx.a);
      const again = await read();
      assert(again.open === "false" && again.folded === 1 && !(await stored(page, fx.a)).older.length, `the folded older projects came back: ${JSON.stringify(again)}`);
      return `${shut.cards} cards, ${back.cards} unfolded over a reload, folded again`;
    });

    await run("the phone's sheet comes back where it was left", async () => {
      const desk = await stored(page, fx.a);
      const mp = await mobilePage();
      await open(mp, fx.a);
      await mp.tap("[data-assistant-watching-button]");
      await mp.waitForSelector("#assistant-aside.show", { state: "visible", timeout: 10000 });
      await sleep(400);
      await tab(mp, "triggers", true);
      const fold = `assistant-trigger-${fx.triggers[1]}-details`;
      await unfoldBy(mp, `#assistant-aside [data-assistant-trigger="${fx.triggers[1]}"] [data-assistant-trigger-fold]`, fold, true);
      await open(mp, fx.a);
      await mp.tap("[data-assistant-watching-button]");
      await mp.waitForSelector("#assistant-aside.show", { state: "visible", timeout: 10000 });
      await sleep(400);
      const sheet = await aside(mp);
      assert(sheet.tab === "triggers" && sheet.folds.includes(fold), `the sheet did not come back on the trigger: ${JSON.stringify(sheet)}`);
      await tab(mp, "projects", true);
      await mp.tap(`#assistant-aside [data-assistant-project="${REPO}"] [data-assistant-project-changes]`);
      await mp.waitForSelector("#assistant-projects [data-assistant-project-changes-section]", { state: "visible", timeout: 10000 });
      const opened = await aside(mp);
      const top = opened.max > 40 ? await scrollTo(mp, Math.round(opened.max / 2)) : 0;
      await open(mp, fx.a);
      await mp.tap("[data-assistant-watching-button]");
      await mp.waitForSelector("#assistant-aside.show", { state: "visible", timeout: 10000 });
      await sleep(400);
      const view = await aside(mp);
      assert(view.tab === "projects" && view.view === `${REPO}/changes` && Math.abs(view.top - top) <= 2, `the sheet did not come back on the project: ${JSON.stringify(view)}`);
      const still = await stored(page, fx.a);
      assert(still.tab === desk.tab && JSON.stringify(still.project) === JSON.stringify(desk.project) && still.scroll === desk.scroll, `the phone moved the desktop's place: ${JSON.stringify({ desk, still })}`);
      return `triggers with ${fold}, then ${view.view} at ${view.top}`;
    });

    await run("a place whose assistant is gone leaves on the next load of the list column, the living ones stay", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      const away = (await postJSON(page, "/assistants/new", { form: "new", coder: "claude" })).id;
      const never = randomUUID();
      await seedPlace(page, [PREFIX + away, PREFIX + never], 5);
      await postJSON(page, `/assistants/${away}`, { form: "delete" });
      await sleep(500);
      const kept = await places(page);
      assert(kept.includes(PREFIX + away) && kept.includes(PREFIX + never), `a page without the list column removed a place: ${kept}`);
      await open(page, fx.a);
      const left = await places(page);
      assert(!left.includes(PREFIX + away) && !left.includes(PREFIX + never), `the gone assistants' places stayed: ${left}`);
      assert(left.includes(PREFIX + fx.a) && left.includes(PREFIX + fx.b), `a living assistant's place went: ${left}`);
      return `${left.length} places left, the deleted and the unknown one gone`;
    });

    await run("a delete in the editor's picker drops the place at once", async () => {
      const doomed = (await postJSON(page, "/assistants/new", { form: "new", coder: "claude" })).id;
      fx.assistants.push(doomed);
      // Up to 500px high the editor stands in its drawer layout.
      await page.setViewportSize({ width: 1360, height: 900 });
      let left;
      try {
        await page.goto(`${BASE}/projects/${REPO}/editor`, { waitUntil: "domcontentloaded" });
        await L.dismissUpdate(page);
        await L.editorReady(page);
        await seedPlace(page, [PREFIX + doomed], 7);
        const row = `[data-editor-sheet-body] [data-asst-pick="${doomed}"]`;
        await page.click("[data-editor-asst-status]");
        await page.waitForSelector(row, { timeout: 10000 });
        await sleep(150);
        assert(!(await page.$("[data-assistant-living]")), "the editor carries the whole list of assistants");
        await L.openPicked(page, row, { item: "Delete" });
        await L.confirmSwal(page);
        await page.waitForSelector(row, { state: "detached", timeout: 10000 });
        left = await places(page);
      } finally {
        await page.setViewportSize({ width: 1360, height: 420 });
      }
      assert(!left.includes(PREFIX + doomed), `the deleted assistant's place stayed: ${left}`);
      assert(left.includes(PREFIX + fx.a) && left.includes(PREFIX + fx.b), `the delete took another place: ${left}`);
      return `${doomed} deleted, its place gone`;
    });

    await run("a share page removes no place, the guest's and the owner's alike", async () => {
      const made = await post(page, "/settings/sharing/new", [["amount", "1"], ["unit", "hours"], ["name", `place-${TAG}`], ["assistant", fx.a]]);
      const link = (made.text.match(/value="([^"]*\/share\/[^"]+)"/) || [])[1];
      assert(made.ok && link, `the link of A answered ${made.status}`);
      const base = new URL(link).pathname;
      const never = randomUUID();
      const seeded = [PREFIX + never, `dc-guest:${PREFIX}${never}`, `dc-guest:${PREFIX}${fx.b}`];
      await seedPlace(page, seeded, 9);
      await page.goto(`${BASE}${base}/assistants/${fx.a}`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(`.dc-app > .dc-ctx [data-assistant-instance="${fx.a}"]`, { timeout: 10000 });
      await page.waitForFunction((body) => !!document.querySelector(body) && customElements.get("dc-assistant-list"), BODY, { timeout: 15000 });
      assert(!(await page.$("[data-assistant-living]")), "the guest's column says it holds every assistant");
      await tab(page, "triggers");
      await sleep(500);
      const left = await places(page);
      await page.evaluate((keys) => keys.forEach((k) => localStorage.removeItem(k)), [...seeded, `dc-guest:${PREFIX}${fx.a}`]);
      assert(seeded.every((k) => left.includes(k)), `the share page removed a place: ${left}`);
      assert(left.includes(`dc-guest:${PREFIX}${fx.a}`), `the guest's aside wrote no place of its own: ${left}`);
      return `${seeded.length} seeded places kept, the guest's own written`;
    });

    await run("a storage that throws on every call breaks nothing", async () => {
      const broken = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1360, height: 420 } });
      const bag = { consoleErrors: [], pageErrors: [], cdnNoise: [] };
      try {
        await broken.addInitScript(() => {
          const no = () => { throw new DOMException("The quota has been exceeded.", "QuotaExceededError"); };
          for (const name of ["getItem", "setItem", "removeItem", "key", "clear"]) Storage.prototype[name] = no;
          Object.defineProperty(Storage.prototype, "length", { get: no });
        });
        const bp = await broken.newPage();
        L.wirePage(bp, bag);
        await L.login(bp);
        await open(bp, fx.a);
        const first = await aside(bp);
        assert(first.tab === "projects" && first.cards, `the aside did not stand: ${JSON.stringify(first)}`);
        await tab(bp, "coders");
        await unfoldBy(bp, `[data-assistant-job="${fx.job}"] [data-assistant-job-fold]`, fx.jobFold);
        await tab(bp, "projects");
        await bp.click(`[data-assistant-project="${REPO}"] [data-assistant-project-changes]`);
        await bp.waitForSelector("#assistant-projects [data-assistant-project-changes-section]", { state: "visible", timeout: 10000 });
        await bp.click("#assistant-projects [data-assistant-project-back]");
        await bp.waitForSelector(`#assistant-projects [data-assistant-project="${REPO}"]`, { state: "visible", timeout: 5000 });
        await scrollTo(bp, 30);
        const errors = [...bag.pageErrors, ...bag.consoleErrors];
        assert(!errors.length, `the page threw: ${errors.join(" | ")}`);
        return "the aside, a tab, a fold, a project and Back work without storage";
      } finally {
        await broken.close().catch(() => {});
      }
    });

    await run("a blocked module and a hanging pull still show the aside within about 2 seconds", async () => {
      const own = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1360, height: 420 } });
      const bag = { consoleErrors: [], pageErrors: [], cdnNoise: [] };
      const held = /\/js\/dc\/assistant-place\.[0-9a-f]+\.js$/;
      try {
        const bp = await own.newPage();
        L.wirePage(bp, bag);
        await bp.addInitScript(sampler);
        await L.login(bp);
        await bp.evaluate(({ key, repo }) => localStorage.setItem(key, JSON.stringify({
          tab: "projects", scroll: 0, folds: [], older: [], project: { name: repo, section: "changes", more: "", list: 0 }, at: Date.now(),
        })), { key: PREFIX + fx.a, repo: REPO });
        const showing = async () => {
          await bp.waitForFunction(() => (window.__frames || []).some((f) => f.shown), null, { timeout: 6000 });
          const all = await frames(bp);
          const spin = await bp.evaluate(() => [...document.querySelectorAll("#assistant-aside .dc-aside-wait .spinner-border")].map((s) => s.getClientRects().length > 0 || s.checkVisibility()));
          const spun = waiting(all);
          assert(spun.count && spun.after >= 200 && spun.handover, `the spinner did not stand from about 300ms until the aside showed: ${JSON.stringify(spun)}`);
          assert(spin.length === 1 && !spin[0], `the spinner still has a box once the aside showed: ${spin}`);
          return { waited: all.filter((f) => !f.shown).length, after: all.find((f) => f.shown).t - all[0].t, spun };
        };
        await bp.route(held, (route) => route.abort());
        await bp.goto(`${BASE}/assistants/${fx.a}`, { waitUntil: "domcontentloaded" });
        const blocked = await showing();
        assert(!(await bp.evaluate(() => !!customElements.get("dc-assistant-list"))), "the lists came up without their module");
        assert(blocked.waited > 0 && blocked.after <= 2500, `without its module the aside showed after ${blocked.after}ms, ${blocked.waited} frames waited`);
        await bp.unroute(held);
        await bp.route(VIEW, () => {});
        await bp.goto(`${BASE}/assistants/${fx.a}`, { waitUntil: "domcontentloaded" });
        await L.dismissUpdate(bp);
        const hung = await showing();
        assert(hung.waited > 0 && hung.after <= 2500, `under a hanging pull the aside showed after ${hung.after}ms, ${hung.waited} frames waited`);
        await bp.waitForFunction((body) => !document.querySelector(body).classList.contains("dc-aside-landing"), BODY, { timeout: 3000 });
        const fell = await aside(bp);
        assert(fell.tab === "projects" && fell.cards && !fell.view, `the aside does not stand on the cards: ${JSON.stringify(fell)}`);
        await tab(bp, "coders");
        await sleep(400);
        const kept = await stored(bp, fx.a);
        assert(kept?.tab === "coders", `a tab picked while the pull hangs was not kept: ${JSON.stringify(kept)}`);
        return `without the module after ${blocked.after}ms, spinner from ${blocked.spun.after}ms, under the hanging pull after ${hung.after}ms, spinner from ${hung.spun.after}ms, then Coders kept`;
      } finally {
        await own.close().catch(() => {});
      }
    });

    await run("at most 50 places stay, the ones looked at longest ago go first", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      const more = [];
      for (let i = 0; i < 55; i++) more.push((await postJSON(page, "/assistants/new", { form: "new", coder: "claude" })).id);
      fx.assistants.push(...more);
      const seeded = [fx.b, ...more].map((id) => PREFIX + id);
      await page.evaluate((prefix) => Object.keys(localStorage).filter((k) => k.startsWith(prefix)).forEach((k) => localStorage.removeItem(k)), PREFIX);
      await seedPlace(page, seeded, 1000);
      await open(page, fx.a);
      const loaded = await places(page);
      assert(loaded.length === seeded.length, `the load of living assistants' places left ${loaded.length} of ${seeded.length}`);
      await tab(page, "triggers");
      await sleep(300);
      const left = await places(page);
      const went = seeded.filter((k) => !left.includes(k));
      const oldest = seeded.slice(0, seeded.length + 1 - 50);
      assert(left.length === 50 && left.includes(PREFIX + fx.a), `${left.length} places stayed: ${left.slice(0, 5)}`);
      assert(went.join() === oldest.join(), `not the oldest went: ${went.length} gone, ${went.slice(0, 3)}`);
      return `${left.length} kept, the ${went.length} oldest of ${seeded.length + 1} living assistants' places gone`;
    });
  } finally {
    for (const id of fx.triggers) await postJSON(page, "/assistants/triggers", { form: "remove", id }).catch(() => {});
    for (const id of fx.coders) await postJSON(page, `/coders/${id}/delete`, {}).catch(() => {});
    for (const id of fx.assistants) await postJSON(page, `/assistants/${id}`, { form: "delete" }).catch(() => {});
    for (const name of [REPO, PLAIN, ...fx.olds]) await L.deleteProject(page, name).catch(() => {});
  }
});
