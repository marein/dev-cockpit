const L = require("./lib");
const { assert, BASE, sleep } = L;
const fs = require("fs");
const path = require("path");
const { execFileSync } = require("child_process");
const { guestPage } = require("./project-share-guest");
const { startFakeForge, addHost, gitlabNote, gitlabDiscussion } = require("./forge-fake");

// The forge on the cards of an assistant's Projects tab (GET
// /assistants/projects), chromium: a card of a project bound to the fake
// GitLab shows its merge request (mark, number, title, draft, state, its
// page), the pipeline with a bar per stage, the job that runs or the first
// that failed it, the open threads, the forge's and the local ones, and the
// line comments a publish would post, read from the forge poller's cache and
// moved by the forge and linecomments signals without a reload. A failed
// pipeline and a job closed blocked put a card first with a red edge, Needs
// you and the reason, the tab counts them; Retry runs the failed jobs again through
// the project row's route and the card follows. "Show N older projects"
// unfolds the quiet cards past the newest five and, then reading "Show fewer
// projects", folds them again; a failed pipeline brings a card up from behind
// it too. A read that failed says why, a project without a forge shows git
// alone. On a phone's sheet the lines fit; a guest of a link to the
// assistant reads them with the mark and the count, live, and no Retry.
//
// Instance like assistant-projects.js's (tests/e2e/fakes first on PATH, a
// scratch HOME, its --projects-dir mounted into the runner at the same path)
// with the claude notify inbox mounted (-v
// <state-dir>/notification-inbox/claude:/inbox -e NOTIFY_DIR=/inbox), which
// starts the check that closes a job blocked. The fake forge runs inside the
// runner on FORGE_PORT and FORGE_TLS_PORT (default 3157 and 3158), published
// on the host's loopback, where the instance reaches it at FORGE_ADDR
// (default 127.0.0.1). SHOTS_DIR with a mount takes the screenshots. About a
// minute.
const TAG = Date.now().toString(36).slice(-5);
const SHOTS_DIR = process.env.SHOTS_DIR || "";
const NOTIFY_DIR = process.env.NOTIFY_DIR || "";
const FAKE_PORT = Number(process.env.FORGE_PORT || 3157);
const FAKE_TLS_PORT = Number(process.env.FORGE_TLS_PORT || 3158);
const FAKE_ADDR = process.env.FORGE_ADDR || "127.0.0.1";
const TOKEN = `glpat-e2e${Date.now().toString(36)}Ap2fQ7rZ`;
const ORIGIN = new URL(BASE).origin;
const REPO = `apforge${TAG}`;
const LOST = `apforgelost${TAG}`;
const PLAIN = `apforgeplain${TAG}`;
const QUIET = [1, 2, 3].map((n) => `apforgequiet${n}${TAG}`);
const FILE = "src/login.go";
const CONTENT = "package login\n\nfunc login() {\n\tname := bb\n\tc := 3\n\td := 4\n\treturn\n}\n";
const PANE = "#assistant-projects";

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

const steer = (page, owner, terminal, doneWhen = "") => postJSON(page, "/assistants/jobs", { form: "steer", assistant: owner, terminal, task: "Forge check", done_when: doneWhen });
const release = (page, terminal) => postJSON(page, "/assistants/jobs", { form: "release", terminal });

// ring drops a coder's Stop hook into the notify inbox, the way claude
// reports a turn, which starts the check of its job.
function ring(session) {
  const file = path.join(NOTIFY_DIR, `${Date.now()}-${Math.floor(Math.random() * 1e6)}`);
  fs.writeFileSync(`${file}.tmp`, JSON.stringify({ session_id: session, hook_event_name: "Stop" }));
  fs.renameSync(`${file}.tmp`, `${file}.json`);
}

async function openPage(p, url) {
  await p.goto(url, { waitUntil: "domcontentloaded" });
  await L.dismissUpdate(p);
  await p.waitForSelector(`${PANE} [data-assistant-body]`, { state: "attached", timeout: 15000 });
}

// cards reads the tab as it stands on the screen, each forge line by what it
// shows.
const cards = (p) => p.evaluate((pane) => {
  const shown = (el) => !!el && el.getClientRects().length > 0 && getComputedStyle(el).visibility !== "hidden";
  const probe = document.body.appendChild(document.createElement("div"));
  probe.className = "border border-danger";
  const danger = getComputedStyle(probe).borderTopColor;
  probe.remove();
  const text = (el) => (shown(el) ? el.textContent.replace(/\s+/g, " ").trim() : "");
  return [...document.querySelectorAll(`${pane} [data-assistant-project]`)].map((card) => {
    const q = (sel) => card.querySelector(sel);
    const box = card.getBoundingClientRect();
    return {
      name: card.dataset.assistantProject,
      left: box.left,
      right: box.right,
      red: getComputedStyle(card).borderTopColor === danger,
      needs: text(q("[data-assistant-project-needs]")),
      needsWhy: text(q("[data-assistant-project-needs-why]")),
      change: text(q("[data-assistant-project-change]")),
      changeHref: q("[data-assistant-project-change-link]")?.getAttribute("href") || "",
      noChange: text(q("[data-assistant-project-no-change]")),
      pipeline: q("[data-assistant-project-pipeline]")?.dataset.assistantProjectPipeline || "",
      pipelineShown: shown(q("[data-assistant-project-pipeline]")),
      note: text(q("[data-assistant-project-pipeline-note]")),
      stages: [...card.querySelectorAll("[data-assistant-project-stage]")].filter(shown).map((bar) => bar.dataset.assistantProjectStage),
      retry: text(q("[data-assistant-project-retry]")),
      retryInside: shown(q("[data-assistant-project-retry]")) && q("[data-assistant-project-retry]").getBoundingClientRect().right <= box.right,
      problem: text(q("[data-assistant-project-problem]")),
      review: text(q("[data-assistant-project-review]")),
      lines: [...card.querySelectorAll("[data-assistant-project-change], [data-assistant-project-no-change], [data-assistant-project-pipeline], [data-assistant-project-problem], [data-assistant-project-review]")].filter(shown).length,
      buttons: [...card.querySelectorAll("button, form, input")].filter((el) => !el.matches("[data-assistant-project-older], [data-assistant-project-open]")).length,
    };
  });
}, PANE);

async function until(p, want, what, timeout = 30000) {
  const end = Date.now() + timeout;
  let last = null;
  while (Date.now() < end) {
    last = await cards(p);
    if (want(last, (name) => last.find((c) => c.name === name))) return last;
    await sleep(250);
  }
  throw new Error(`${what}: ${JSON.stringify(last)}`);
}

const badge = (p) => p.evaluate(() => {
  const el = document.querySelector("[data-assistant-projects-tab] dc-steer-badge");
  return el && el.getClientRects().length ? Number(el.textContent) : 0;
});

const waitBadge = (p, n) => p.waitForFunction((n) => {
  const el = document.querySelector("[data-assistant-projects-tab] dc-steer-badge");
  return (el && el.getClientRects().length ? Number(el.textContent) : 0) === n;
}, n, { timeout: 20000 });

L.runFeature("assistant-projects-forge", async ({ browser, page, run, mobilePage, bag }) => {
  assert(NOTIFY_DIR, "NOTIFY_DIR is not set: run with -v <state-dir>/notification-inbox/claude:/inbox -e NOTIFY_DIR=/inbox");
  const fake = await startFakeForge({ port: FAKE_PORT, tlsPort: FAKE_TLS_PORT, token: TOKEN });
  const state = fake.state;
  const fakeWeb = `http://${FAKE_ADDR}:${FAKE_PORT}`;
  const coders = [];
  let owner = "";
  let repoDir = "";
  let plainCoder = "";
  let lostCoder = "";
  let workCoder = "";
  let hostEdit = "";
  let linkId = "";
  let guest = null;
  const refresh = (name) => page.evaluate(async (p) => (await fetch(`/projects/${p}/forge/status?fresh=1`, { headers: { Accept: "application/json" } })).status, name);
  try {
    await run("setup: a repository on the fake GitLab with a merge request, review threads and a running pipeline", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      owner = (await postJSON(page, "/assistants/new", { form: "new", coder: "claude" })).id;
      assert(owner, "no assistant made");
      for (const name of [REPO, LOST, PLAIN]) await L.createProject(page, name);
      repoDir = await L.projectPath(page, REPO);
      const lostDir = await L.projectPath(page, LOST);
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
      git(lostDir, "init", "-q", "-b", "main");
      git(lostDir, "commit", "-q", "--allow-empty", "-m", "first");
      git(lostDir, "update-ref", "refs/remotes/origin/main", "HEAD");
      git(lostDir, "remote", "add", "origin", `${fakeWeb}/gl/team/sub/missing.git`);
      state.head = pushed;
      state.status = "running";
      state.discussions = (s) => [
        gitlabDiscussion("d1ap", false, [gitlabNote(41, "root", "Rename it?", { resolvable: true, at: { head: s.head, path: FILE, newLine: 4 } })]),
        gitlabDiscussion("d2ap", false, [gitlabNote(42, "alice", "Why 3?", { resolvable: true, at: { head: s.head, path: FILE, newLine: 5 } })]),
        gitlabDiscussion("d3ap", false, [gitlabNote(43, "root", "Done.", { resolvable: true, resolved: true, at: { head: s.head, path: FILE, newLine: 6 } })]),
      ];
      const added = await addHost(page, { kind: "gitlab", web_url: `${fakeWeb}/gl`, aliases: `apf-${TAG}`, token: TOKEN });
      assert(!added.failed, added.failed);
      hostEdit = await page.evaluate(async (mark) => {
        const html = await (await fetch("/settings/forges")).text();
        const doc = new DOMParser().parseFromString(html, "text/html");
        return [...doc.querySelectorAll("[data-forge-host]")].find((r) => r.textContent.includes(mark))?.querySelector("[data-forge-edit]")?.getAttribute("href") || "";
      }, `apf-${TAG}`);
      assert(hostEdit, "the added host has no Edit on the list");
      await postJSON(page, `/projects/${REPO}/editor/comments`, { path: FILE, line: 3, text: "Mine to publish" }, { json: true });

      lostCoder = await newCoder(page, lostDir, `apf-lost-${TAG}`);
      coders.push(lostCoder);
      await steer(page, owner, lostCoder);
      await release(page, lostCoder);
      workCoder = await newCoder(page, repoDir, `apf-work-${TAG}`);
      coders.push(workCoder);
      await steer(page, owner, workCoder);
      await sleep(30);
      plainCoder = await newCoder(page, await L.projectPath(page, PLAIN), `apf-plain-${TAG}`);
      coders.push(plainCoder);
      await steer(page, owner, plainCoder);
    });

    await run("a bound card shows the merge request, the running pipeline with its stages and the review counts", async () => {
      await page.setViewportSize({ width: 1360, height: 900 });
      await openPage(page, `${BASE}/assistants/${owner}`);
      const all = await until(page, (_, c) => c(REPO)?.pipeline === "running" && /3 open threads/.test(c(REPO)?.review) && c(LOST)?.problem, "the forge never reached the cards");
      const repo = all.find((c) => c.name === REPO);
      assert(all.length === 3 && all[2].name === LOST, `the cards stand as ${all.map((c) => c.name)}`);
      assert(repo.change === "!4 Fix the login draft · open" && repo.changeHref === "https://gitlab.example.com/team/sub/app/-/merge_requests/4", `the merge request reads ${JSON.stringify(repo)}`);
      assert(repo.pipelineShown && repo.note === "unit" && [...repo.stages].sort().join(",") === "failed,failed,running,success", `the pipeline reads ${JSON.stringify(repo)}`);
      assert(repo.review === "3 open threads · 1 unpublished comment", `the review line reads ${repo.review}`);
      assert(!repo.retry && !repo.needs && repo.buttons === 0, `a running pipeline offers a retry or needs you: ${JSON.stringify(repo)}`);
      const lost = all.find((c) => c.name === LOST);
      assert(lost.problem.startsWith("The last read of the forge failed") && lost.lines === 1, `the failed read reads ${JSON.stringify(lost)}`);
      const plain = all.find((c) => c.name === PLAIN);
      assert(plain.lines === 0 && !plain.needs, `the project without a forge shows ${JSON.stringify(plain)}`);
      assert((await badge(page)) === 0, "the count shows while nothing needs you");
      return `${repo.change}; ${repo.note}; ${repo.review}`;
    });

    await run("a failed pipeline puts the card first with its mark, the failed job and Retry, and counts it, live", async () => {
      await page.evaluate(() => { window.__noReload = 1; });
      state.status = "failed";
      assert(await refresh(REPO) === 200, "the fresh read failed");
      const all = await until(page, (cs) => cs[0]?.name === REPO && cs[0].needs, "the failed pipeline did not move the card");
      const repo = all[0];
      assert(repo.needs === "Needs you" && repo.needsWhy === "The pipeline failed.", `the mark reads ${JSON.stringify(repo)}`);
      assert(repo.red && !all.slice(1).some((c) => c.red), `the red edge stands on ${all.filter((c) => c.red).map((c) => c.name)}`);
      assert(repo.note === "unit +2" && repo.stages.includes("failed") && repo.retry === "Retry" && repo.retryInside, `the failed pipeline reads ${JSON.stringify(repo)}`);
      await waitBadge(page, 1);
      assert(await page.evaluate(() => window.__noReload === 1), "the page reloaded");
      if (SHOTS_DIR) await page.screenshot({ path: path.join(SHOTS_DIR, "assistant-projects-forge-desktop.png") });
      return repo.note;
    });

    await run("a phone's sheet shows the forge lines inside the card", async () => {
      const mp = await mobilePage();
      await openPage(mp, `${BASE}/assistants/${owner}`);
      await mp.tap("[data-assistant-watching-button]");
      await mp.waitForSelector(`#assistant-aside.show [data-assistant-project="${REPO}"] [data-assistant-project-pipeline]`, { state: "visible", timeout: 10000 });
      await sleep(400);
      const repo = (await cards(mp)).find((c) => c.name === REPO);
      const width = await mp.evaluate(() => window.innerWidth);
      assert(repo.left >= 0 && repo.right <= width && repo.retryInside && repo.lines === 3, `the phone's card reads ${JSON.stringify(repo)} at ${width}px`);
      const overflow = await mp.evaluate((name) => [...document.querySelectorAll(`[data-assistant-project="${name}"] [data-assistant-project-pipeline] > *`)]
        .filter((el) => el.getBoundingClientRect().right > el.closest("[data-assistant-project]").getBoundingClientRect().right + 0.5).length, REPO);
      assert(overflow === 0, "a part of the pipeline line sticks out of the phone's card");
      if (SHOTS_DIR) await mp.screenshot({ path: path.join(SHOTS_DIR, "assistant-projects-forge-phone.png") });
    });

    await run("a guest of a link reads the forge lines, the mark and the count, without Retry", async () => {
      const made = await post(page, "/settings/sharing/new", [["amount", "1"], ["unit", "hours"], ["name", `apf-${TAG}`], ["assistant", owner]]);
      const link = (made.text.match(/value="([^"]*\/share\/[^"]+)"/) || [])[1];
      assert(made.ok && link, `making a link answered ${made.status}`);
      linkId = await page.evaluate(async (name) => {
        const doc = new DOMParser().parseFromString(await (await fetch("/settings/sharing")).text(), "text/html");
        return [...doc.querySelectorAll("[data-share-row]")].find((r) => r.querySelector("[data-share-name]")?.textContent.trim() === name)?.dataset.shareRow || "";
      }, `apf-${TAG}`);
      assert(linkId, "the link is not listed");
      const base = new URL(link).pathname;
      guest = await guestPage(browser, bag, { requests: [] }, ORIGIN);
      const gp = guest.page;
      await openPage(gp, `${BASE}${base}/assistants/${owner}`);
      const repo = (await until(gp, (cs) => cs[0]?.name === REPO && cs[0].needs, "the guest's cards lack the mark"))[0];
      assert(repo.change === "!4 Fix the login draft · open" && repo.pipeline === "failed" && repo.note === "unit +2" && repo.needsWhy === "The pipeline failed." && /3 open threads/.test(repo.review), `the guest's card reads ${JSON.stringify(repo)}`);
      assert(!repo.retry && repo.buttons === 0, `the guest's card offers ${JSON.stringify(repo)}`);
      await waitBadge(gp, 1);
      return base.replace(/\/share\/.*/, "/share/…");
    });

    await run("Retry runs the failed jobs again, the card and the guest's follow live", async () => {
      await openPage(page, `${BASE}/assistants/${owner}`);
      await until(page, (cs) => cs[0]?.name === REPO && cs[0].retry, "the card offers no Retry");
      await page.evaluate(() => { window.__noReload = 1; });
      state.retried.splice(0);
      await page.click(`${PANE} [data-assistant-project="${REPO}"] [data-assistant-project-retry]`);
      await page.waitForFunction(() => [...document.querySelectorAll(".dc-toast")].some((el) => el.textContent.includes("The failed jobs of pipeline #12 run again.")), null, { timeout: 15000 });
      assert(state.retried.includes("/pipelines/12/retry"), `the forge ran ${JSON.stringify(state.retried)}`);
      const all = await until(page, (_, c) => c(REPO)?.pipeline === "running" && !c(REPO).needs && !c(REPO).needsWhy && !c(REPO).retry, "the card did not follow the retry");
      assert(!all.some((c) => c.red), `a red edge stays on ${all.filter((c) => c.red).map((c) => c.name)}`);
      await waitBadge(page, 0);
      await until(guest.page, (_, c) => c(REPO)?.pipeline === "running" && !c(REPO).needs, "the guest's card did not follow the retry");
      await waitBadge(guest.page, 0);
      assert(await page.evaluate(() => window.__noReload === 1), "the page reloaded");
    });

    await run("a line comment and a job closed blocked move the cards live", async () => {
      await page.evaluate(() => { window.__noReload = 1; });
      await postJSON(page, `/projects/${REPO}/editor/comments`, { path: FILE, line: 4, text: "Another one" }, { json: true });
      await until(page, (_, c) => c(REPO)?.review === "4 open threads · 2 unpublished comments", "the new comment did not reach the card");
      await release(page, plainCoder);
      await steer(page, owner, plainCoder, "WAKE_BLOCKED");
      ring(plainCoder);
      const all = await until(page, (cs) => cs[0]?.name === PLAIN && cs[0].needs, "the blocked job did not move its card", 45000);
      assert(all[0].needsWhy === "Its newest job is blocked.", `the blocked card says ${all[0].needsWhy}`);
      await waitBadge(page, 1);
      await steer(page, owner, plainCoder);
      await until(page, (_, c) => c(PLAIN) && !c(PLAIN).needs && !c(PLAIN).needsWhy, "the job opened again did not take the mark back");
      await waitBadge(page, 0);
      assert(await page.evaluate(() => window.__noReload === 1), "the page reloaded");
    });

    await run("Show N older projects unfolds the quiet cards and folds them again, a failed pipeline brings one up, live", async () => {
      await release(page, workCoder);
      await sleep(30);
      await steer(page, owner, lostCoder);
      await release(page, lostCoder);
      await release(page, plainCoder);
      for (const name of QUIET) {
        await L.createProject(page, name);
        const coder = await newCoder(page, await L.projectPath(page, name), `apf-${name}`);
        coders.push(coder);
        await steer(page, owner, coder);
        await release(page, coder);
      }
      await openPage(page, `${BASE}/assistants/${owner}`);
      await page.evaluate(() => { window.__noReload = 1; });
      const older = () => page.evaluate((pane) => {
        const el = document.querySelector(`${pane} [data-assistant-older]`);
        return el && el.getClientRects().length ? `${el.innerText.trim()} ${el.getAttribute("aria-expanded")}` : "";
      }, PANE);
      await until(page, (cs, c) => cs.length === 5 && !c(REPO) && cs.every((card) => !card.needs), "the card did not fold");
      assert(await older() === "Show 1 older project false", `the fold row reads ${await older()}`);
      await page.click(`${PANE} [data-assistant-older]`);
      await until(page, (cs, c) => cs.length === 6 && c(REPO), "the older card did not unfold");
      assert(await older() === "Show fewer projects true", `the unfolded fold row reads ${await older()}`);
      await page.click(`${PANE} [data-assistant-older]`);
      await until(page, (cs, c) => cs.length === 5 && !c(REPO), "the older card did not fold again");
      assert(await older() === "Show 1 older project false", `the folded again row reads ${await older()}`);
      await waitBadge(page, 0);
      if (guest) await until(guest.page, (cs, c) => cs.length === 5 && !c(REPO), "the guest's card did not fold");
      state.status = "failed";
      assert(await refresh(REPO) === 200, "the fresh read failed");
      const all = await until(page, (cs) => cs[0]?.name === REPO && cs[0].needs === "Needs you", "the failed pipeline did not bring the folded card up");
      assert(all.length === 6 && all[0].red && all[0].retry === "Retry", `the cards read ${JSON.stringify(all)}`);
      assert(await older() === "", "the fold row stays");
      await waitBadge(page, 1);
      if (guest) {
        await until(guest.page, (cs) => cs[0]?.name === REPO && cs[0].needs === "Needs you" && !cs[0].retry, "the guest's folded card did not come up");
        await waitBadge(guest.page, 1);
      }
      assert(await page.evaluate(() => window.__noReload === 1), "the page reloaded");
    });
  } finally {
    if (guest) await guest.ctx.close().catch(() => {});
    if (linkId) await post(page, `/settings/sharing/${linkId}`, { form: "revoke" }).catch(() => {});
    for (const id of coders) await post(page, `/coders/${id}/delete`, {}).catch(() => {});
    if (owner) await post(page, `/assistants/${owner}`, { form: "delete" }).catch(() => {});
    if (hostEdit) await post(page, hostEdit, { form: "delete" }).catch(() => {});
    for (const name of [REPO, LOST, PLAIN, ...QUIET]) await L.deleteProject(page, name).catch(() => {});
  }
});
