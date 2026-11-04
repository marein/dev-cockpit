const L = require("./lib");
const { assert, BASE, sleep } = L;
const fs = require("fs");
const path = require("path");
const { execFileSync } = require("child_process");

// The Projects tab of an assistant's aside (GET /assistants/projects, the
// watch POST /assistants/projects/watch), chromium: the first of three tabs
// and the one that opens, on the desktop aside and in the phone's sheet, the
// three in equal thirds on one line down to a 360px phone. One sentence while
// the assistant has no job. A card per project a job of the assistant names,
// the ones with an open job first: the name leading to the project, the
// checked out branch, ahead and behind its upstream, the uncommitted files or
// "no git repository", and under it the assistant's jobs there, each row
// opening its coder, a stopped one resumed first through the Coders tab's
// resume. A row's icon is the coder's terminal tab icon, the same on the card
// and in the Coders tab, in both schemes at rest and hovered, on the desktop
// and a phone, live: purple while the job is open, a check's stroke kept over
// an activity event, red where the job closed blocked also while the coder
// runs, else green while it runs and grey once it stopped, the stroke of its
// turn, the news dot, read by opening the coder. Closed rows past the newest
// three stand behind "Show N older coders", which reads "Show fewer coders"
// while they stand.
// The tab's count stays hidden while no project needs the user. A project
// opened in the aside leads back with "Projects", never underlined, its
// keyboard focus ringed. A changed file, a commit and a checkout made on disk
// outside the cockpit, a job closing and opening, all move the cards without
// a reload, an unfolded card and the scroll position staying. From xl up
// the aside drags at its left edge with the list column's handle (@dc/ctx),
// the thread and the composer following live, the width kept per area, the
// thread keeping its floor; the phone's sheet stays as it is. The guest's
// face of the tab is assistant-share.js, the forge on the cards and what
// needs the user assistant-projects-forge.js.
//
// Instance like assistant.js's: tests/e2e/fakes first on PATH (the coders run
// on the fake claude), a scratch HOME, its own --projects-dir, mounted into
// the runner at the same path, because the runner changes a working copy with
// the container's git the way a coder would, and the claude notify inbox,
// which starts and ends a coder's turn and buys a check
// (-v <state-dir>/notification-inbox/claude:/inbox -e NOTIFY_DIR=/inbox).
// SHOTS_DIR with a mount takes the desktop and phone screenshots the review
// reads.
const TAG = Date.now().toString(36).slice(-5);
const SHOTS_DIR = process.env.SHOTS_DIR || "";
const NOTIFY_DIR = process.env.NOTIFY_DIR || "";
const REPO = `aproj${TAG}`;
const PLAIN = `aprojplain${TAG}`;

function git(dir, ...args) {
  return execFileSync("git", ["-c", "user.name=e2e", "-c", "user.email=e2e@localhost", "-C", dir, ...args], { encoding: "utf8" });
}

async function postJSON(page, url, body) {
  const res = await page.evaluate(async ({ url, body }) => {
    const token = document.querySelector('meta[name="csrf-token"]')?.content || "";
    const r = await fetch(url, { method: "POST", headers: { "X-CSRF-Token": token, Accept: "application/json" }, body: new URLSearchParams(body) });
    return { ok: r.ok, status: r.status, text: await r.text() };
  }, { url, body });
  assert(res.ok, `POST ${url} answered ${res.status}: ${res.text.slice(0, 200)}`);
  return JSON.parse(res.text || "{}");
}

async function newCoder(page, dir, name) {
  const made = await postJSON(page, "/coders/new", { name, project: dir, coder: "claude", automatic_approval: "on", prompt: `job-${name}` });
  assert(made.id, `no coder made: ${JSON.stringify(made)}`);
  return made.id;
}

const steer = (page, owner, terminal, doneWhen = "") => postJSON(page, "/assistants/jobs", { form: "steer", assistant: owner, terminal, task: "Projects check", done_when: doneWhen });
const release = (page, terminal) => postJSON(page, "/assistants/jobs", { form: "release", terminal });

async function openPage(p, id) {
  await p.goto(`${BASE}/assistants/${id}`, { waitUntil: "domcontentloaded" });
  await L.dismissUpdate(p);
  await p.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
}

// cards reads the tab as it stands on the screen.
const cards = (p) => p.evaluate(() => [...document.querySelectorAll("#assistant-projects [data-assistant-project]")].map((card) => {
  const data = (sel, key) => card.querySelector(sel)?.dataset[key] ?? null;
  return {
    name: card.dataset.assistantProject,
    shown: card.getClientRects().length > 0,
    href: card.querySelector("[data-assistant-project-link]")?.getAttribute("href") || "",
    branch: card.querySelector("[data-assistant-project-branch]")?.textContent.trim() || "",
    ahead: data("[data-assistant-project-ahead]", "assistantProjectAhead"),
    behind: data("[data-assistant-project-behind]", "assistantProjectBehind"),
    changed: data("[data-assistant-project-changed]", "assistantProjectChanged"),
    norepo: !!card.querySelector("[data-assistant-project-norepo]"),
    noUpstream: !!card.querySelector("[data-assistant-project-no-upstream]"),
    older: card.querySelector("[data-assistant-project-older]")?.innerText.trim() || "",
    olderOpen: card.querySelector("[data-assistant-project-older]")?.getAttribute("aria-expanded") || "",
    jobs: [...card.querySelectorAll("[data-assistant-project-job]")].map((row) => {
      const icon = row.querySelector("[data-assistant-job-state]");
      return {
        id: row.dataset.assistantProjectJob,
        state: icon?.dataset.assistantJobState || "",
        label: icon?.getAttribute("aria-label") || "",
        href: row.matches("[data-assistant-project-job-link]") ? row.getAttribute("href") : "",
        resume: row.matches(`form[action="/coders/${row.dataset.assistantProjectJob}/resume"]`) && !!row.querySelector("button[data-assistant-job-resume]"),
        shown: row.getClientRects().length > 0,
      };
    }),
  };
}));

const card = async (p, name) => (await cards(p)).find((c) => c.name === name);

// until polls the tab until want answers true for the card of name.
async function until(p, name, want, what) {
  const end = Date.now() + 20000;
  let last = null;
  while (Date.now() < end) {
    last = await card(p, name);
    if (last && want(last)) return last;
    await sleep(250);
  }
  throw new Error(`${what}: ${JSON.stringify(last)}`);
}

// hook drops a claude hook of a coder into the notify inbox, the way claude
// reports the start and the end of a turn.
function hook(session, name) {
  const file = path.join(NOTIFY_DIR, `${Date.now()}-${Math.floor(Math.random() * 1e6)}`);
  fs.writeFileSync(`${file}.tmp`, JSON.stringify({ session_id: session, hook_event_name: name }));
  fs.renameSync(`${file}.tmp`, `${file}.json`);
}

// icons reads a coder's icon on its card and in the Coders tab: the marks it
// wears, its word, whether it draws that coder, a check it holds, what it
// paints, whether it stands on the screen and whether its news dot shows.
const icons = (p, id) => p.evaluate((id) => {
  const read = (el) => {
    if (!el) return null;
    const s = getComputedStyle(el);
    const dot = el.querySelector(".dc-news-dot");
    return {
      marks: ["running", "working", "steered", "text-danger", "news"].filter((c) => el.classList.contains(c)).join(" "),
      label: el.getAttribute("aria-label") || "",
      coder: !!el.querySelector("svg.coder-icon") && el.dataset.notifyTarget === id,
      held: !!el.dataset.workingHeld,
      paint: `${s.color} on ${s.backgroundColor}`,
      ring: s.boxShadow,
      shown: el.getClientRects().length > 0,
      dot: !!dot && getComputedStyle(dot).display !== "none" && (el.getClientRects().length === 0 || dot.getClientRects().length > 0),
    };
  };
  return {
    card: read(document.querySelector(`#assistant-projects [data-assistant-project-job="${id}"] [data-assistant-job-state]`)),
    tab: read(document.querySelector(`#assistant-coders [data-assistant-job="${id}"] [data-assistant-job-state]`)),
  };
}, id);

// alike says the two rows draw one icon, the coder's.
const alike = (g) => !!(g.card && g.tab && g.card.coder && g.tab.coder && g.card.marks === g.tab.marks && g.card.label === g.tab.label && g.card.paint === g.tab.paint);

async function untilIcons(p, id, want, what, timeout = 20000) {
  const end = Date.now() + timeout;
  let last = null;
  while (Date.now() < end) {
    last = await icons(p, id);
    if (want(last)) return last;
    await sleep(250);
  }
  throw new Error(`${what}: ${JSON.stringify(last)}`);
}

const badge = (p) => p.evaluate(() => {
  const el = document.querySelector("[data-assistant-projects-tab] dc-steer-badge");
  return el && el.getClientRects().length ? Number(el.textContent) : 0;
});

// strip measures the three tabs: each a third of the row, one line, its
// icon, words and count inside it.
const strip = (p) => p.evaluate(() => {
  const tabs = ["projects", "coders", "triggers"].map((name) => document.querySelector(`[data-assistant-${name}-tab]`));
  const row = tabs[0].parentElement.getBoundingClientRect();
  return {
    row: row.width,
    tabs: tabs.map((tab) => {
      const rect = tab.getBoundingClientRect();
      const label = tab.querySelector("[data-assistant-tab-label]").getBoundingClientRect();
      const icon = tab.querySelector(".ti");
      const iconWidth = getComputedStyle(icon).display === "none" ? 0 : icon.getBoundingClientRect().width + parseFloat(getComputedStyle(icon).marginRight);
      const style = getComputedStyle(tab);
      return {
        width: rect.width,
        content: iconWidth + label.width,
        lines: Math.round((tab.scrollHeight - parseFloat(style.paddingTop) - parseFloat(style.paddingBottom)) / parseFloat(style.lineHeight)),
        active: tab.classList.contains("active"),
      };
    }),
  };
});

function fits(where, m) {
  for (const [i, tab] of m.tabs.entries()) {
    assert(Math.abs(tab.width - m.row / 3) <= 1, `${where}: tab ${i} is not a third: ${JSON.stringify(m)}`);
    assert(tab.lines === 1, `${where}: tab ${i} wrapped: ${JSON.stringify(m)}`);
    assert(tab.content <= tab.width, `${where}: tab ${i} does not fit: ${JSON.stringify(m)}`);
  }
  assert(m.tabs[0].active && !m.tabs[1].active && !m.tabs[2].active, `${where}: Projects is not the open tab: ${JSON.stringify(m)}`);
}

L.runFeature("assistant-projects", async ({ page, run, mobilePage }) => {
  let owner = "";
  let repoDir = "";
  let plainDir = "";
  let worker = "";
  let stopped = "";
  const closed = [];
  try {
    await run("a fresh assistant opens on Projects with one sentence and no count", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      owner = (await postJSON(page, "/assistants/new", { form: "new", coder: "claude" })).id;
      assert(owner, "no assistant made");
      await openPage(page, owner);
      const pane = page.locator("#assistant-projects");
      assert(await pane.isVisible(), "the Projects pane is not on screen");
      const empty = page.locator("#assistant-projects [data-assistant-projects-empty]");
      assert(await empty.isVisible(), "no sentence for an assistant without a job");
      const text = (await empty.textContent()).trim();
      assert(text.split(/[.!?]\s/).length === 1 && text.length < 80, `the empty state is more than one short sentence: ${text}`);
      assert(!(await page.locator("#assistant-coders").isVisible()), "the coders pane shows beside Projects");
      assert((await badge(page)) === 0, "the count shows at zero");
      return text;
    });

    await run("a card per project: open work first, the git state, the jobs as compact rows", async () => {
      await L.createProject(page, REPO);
      await L.createProject(page, PLAIN);
      repoDir = await L.projectPath(page, REPO);
      plainDir = await L.projectPath(page, PLAIN);
      assert(fs.existsSync(repoDir), `the projects dir is not mounted at ${repoDir}`);
      git(repoDir, "init", "-q", "-b", "work");
      fs.writeFileSync(path.join(repoDir, "README.md"), "one\n");
      git(repoDir, "add", "README.md");
      git(repoDir, "commit", "-q", "-m", "one");
      const base = git(repoDir, "rev-parse", "HEAD").trim();
      fs.writeFileSync(path.join(repoDir, "README.md"), "two\n");
      git(repoDir, "commit", "-q", "-am", "two");
      git(repoDir, "remote", "add", "origin", "/nonexistent");
      git(repoDir, "update-ref", "refs/remotes/origin/work", base);
      git(repoDir, "branch", "-q", "--set-upstream-to=origin/work", "work");

      for (let i = 0; i < 4; i++) {
        const id = await newCoder(page, plainDir, `aproj-old-${i}-${TAG}`);
        await steer(page, owner, id);
        await release(page, id);
        closed.push(id);
        await sleep(30);
      }
      worker = await newCoder(page, repoDir, `aproj-work-${TAG}`);
      await steer(page, owner, worker);

      await page.setViewportSize({ width: 1360, height: 900 });
      await openPage(page, owner);
      const repo = await until(page, REPO, (c) => c.branch === "work", "the repository card never read its branch");
      const all = await cards(page);
      assert(all.map((c) => c.name).join(" ") === `${REPO} ${PLAIN}`, `the cards stand as ${all.map((c) => c.name)}`);
      assert(repo.href === `/projects#project-${REPO}`, `the card leads to ${repo.href}`);
      assert(repo.ahead === "1" && repo.behind === "0" && repo.changed === "0", `the git state reads ${JSON.stringify(repo)}`);
      assert(repo.jobs.length === 1 && repo.jobs[0].state === "steering" && repo.jobs[0].label === "Steering" && repo.jobs[0].href === `/coders/${worker}`,
        `the open job's row reads ${JSON.stringify(repo.jobs)}`);
      const plain = all[1];
      assert(plain.norepo && plain.ahead === null, `the plain project reads ${JSON.stringify(plain)}`);
      assert(plain.jobs.length === 4 && plain.jobs.filter((j) => j.shown).length === 3 && plain.older === "Show 1 older coder" && plain.olderOpen === "false",
        `the closed rows do not fold past three: ${JSON.stringify(plain)}`);
      assert(plain.jobs.every((j) => j.state === "stopped" && j.label === "Released, running"), `the closed rows read ${JSON.stringify(plain.jobs)}`);
      assert((await badge(page)) === 0, `the count reads ${await badge(page)} while nothing needs you`);
      // The row's icon is the jobs tab's own.
      const same = await page.evaluate((id) => {
        const a = document.querySelector(`#assistant-projects [data-assistant-project-job="${id}"] [data-assistant-job-state]`);
        const b = document.querySelector(`#assistant-coders [data-assistant-job="${id}"] [data-assistant-job-state]`);
        return a && b && a.outerHTML === b.outerHTML;
      }, worker);
      assert(same, "the card's state icon is not the jobs tab's");
      return `${REPO} on work +1 -0, ${PLAIN} without git, 3 of 4 closed rows shown`;
    });

    await run("a changed file, a commit and a checkout move the card live, fold and scroll kept", async () => {
      await page.setViewportSize({ width: 1360, height: 420 });
      await openPage(page, owner);
      await until(page, REPO, (c) => c.branch === "work", "the card is not there");
      await page.evaluate(() => { window.__noReload = 1; });
      await page.click(`[data-assistant-project="${PLAIN}"] [data-assistant-project-older]`);
      await page.waitForSelector(`[data-assistant-project="${PLAIN}"] [data-assistant-fold].show`, { timeout: 8000 });
      const unfolded = await card(page, PLAIN);
      assert(unfolded.older === "Show fewer coders" && unfolded.olderOpen === "true" && unfolded.jobs.every((j) => j.shown), `the unfolded card reads ${JSON.stringify(unfolded)}`);
      const scrolled = await page.evaluate(() => {
        let el = document.querySelector("#assistant-projects");
        while (el && !(el.scrollHeight > el.clientHeight && /auto|scroll/.test(getComputedStyle(el).overflowY))) el = el.parentElement;
        if (!el) return null;
        el.dataset.runnerScroller = "1";
        el.scrollTop = el.scrollHeight;
        return el.scrollTop;
      });
      assert(scrolled > 0, `the aside does not scroll at this height: ${scrolled}`);

      fs.writeFileSync(path.join(repoDir, "notes.txt"), "new\n");
      await until(page, REPO, (c) => c.changed === "1", "a new file did not reach the card");
      git(repoDir, "add", "notes.txt");
      git(repoDir, "commit", "-q", "-m", "three");
      await until(page, REPO, (c) => c.ahead === "2" && c.changed === "0", "the commit did not reach the card");
      git(repoDir, "checkout", "-q", "-b", "other");
      await until(page, REPO, (c) => c.branch === "other" && c.noUpstream, "the checkout did not reach the card");

      const kept = await page.evaluate((plain) => ({
        reload: window.__noReload !== 1,
        fold: !!document.querySelector(`[data-assistant-project="${plain}"] [data-assistant-fold].show`),
        scroll: document.querySelector("[data-runner-scroller]")?.scrollTop || 0,
      }), PLAIN);
      assert(!kept.reload, "the page reloaded");
      assert(kept.fold, "a refresh closed the unfolded card");
      const still = await card(page, PLAIN);
      assert(still.older === "Show fewer coders" && still.olderOpen === "true", `the kept fold's control reads ${still.older} ${still.olderOpen}`);
      await page.click(`[data-assistant-project="${PLAIN}"] [data-assistant-project-older]`);
      const shut = await until(page, PLAIN, (c) => c.jobs.filter((j) => j.shown).length === 3, "the older rows did not fold again");
      assert(shut.older === "Show 1 older coder" && shut.olderOpen === "false", `the folded card reads ${shut.older} ${shut.olderOpen}`);
      assert(Math.abs(kept.scroll - scrolled) <= 2, `the scroll moved from ${scrolled} to ${kept.scroll}`);
      await page.setViewportSize({ width: 1360, height: 900 });
      return "new file, commit and checkout read live";
    });

    await run("a job closing and opening moves the card live", async () => {
      await openPage(page, owner);
      await page.evaluate(() => { window.__noReload = 1; });
      await release(page, worker);
      await until(page, REPO, (c) => c.jobs[0]?.state === "stopped", "the closed job did not reach the card");
      await steer(page, owner, worker);
      await until(page, REPO, (c) => c.jobs[0]?.state === "steering", "the job opened again did not reach the card");
      assert((await cards(page))[0].name === REPO, "the open project is not first");
      assert((await badge(page)) === 0, "the count shows while nothing needs you");
      assert(await page.evaluate(() => window.__noReload === 1), "the page reloaded");
      return "stopped, steering";
    });

    await run("a stopped coder's row on a card resumes it like the Coders tab and lands on it, then opens it", async () => {
      await openPage(page, owner);
      stopped = await newCoder(page, repoDir, `aproj-stop-${TAG}`);
      await steer(page, owner, stopped);
      // Only a coder that wrote its session can be resumed, the fake writes
      // it a moment after the start.
      const written = () => page.evaluate(async (id) => {
        const res = await fetch(`/coders/${id}/conversation`);
        return res.ok && (await res.text()).includes("fake claude worked on it");
      }, stopped);
      for (let waited = 0; !(await written()); waited += 250) {
        assert(waited < 15000, "the coder never wrote its session");
        await sleep(250);
      }
      await release(page, stopped);
      await postJSON(page, `/coders/${stopped}/stop`, {});
      await openPage(page, owner);
      const before = await until(page, REPO, (c) => c.jobs.find((j) => j.id === stopped)?.resume, "the stopped coder's row does not resume it");
      const row = before.jobs.find((j) => j.id === stopped);
      assert(row.shown && !row.href && before.jobs.find((j) => j.id === worker)?.href === `/coders/${worker}`, `the card's rows read ${JSON.stringify(before.jobs)}`);
      const same = await page.evaluate((id) => {
        const card = document.querySelector(`#assistant-projects [data-assistant-project-job="${id}"]`);
        const tab = document.querySelector(`#assistant-coders [data-assistant-job="${id}"] [data-assistant-job-resume]`)?.form;
        const token = (form) => form?.querySelector('input[name="csrf_token"]')?.value || "";
        return {
          action: card?.getAttribute("action") === tab?.getAttribute("action") && card?.getAttribute("method") === "post",
          token: !!token(card) && token(card) === token(tab),
          title: card?.querySelector("[data-assistant-job-resume]")?.getAttribute("title") || "",
        };
      }, stopped);
      assert(same.action && same.token && same.title === "Resume coder", `the card's resume is not the Coders tab's: ${JSON.stringify(same)}`);
      await page.click(`#assistant-projects [data-assistant-project-job="${stopped}"] [data-assistant-job-resume]`);
      await page.waitForURL((u) => u.pathname === `/coders/${stopped}`, { timeout: 20000 });
      await openPage(page, owner);
      const after = await until(page, REPO, (c) => c.jobs.find((j) => j.id === stopped)?.href === `/coders/${stopped}`, "the resumed coder's row does not open it");
      assert(!after.jobs.find((j) => j.id === stopped).resume, "the running coder's row still resumes it");
      return "resumed through the Coders tab's form, landed on the coder, the row opens it now";
    });

    await run("a card's state icon wears the Coders tab's colours in both schemes, at rest and hovered", async () => {
      const look = (id) => page.evaluate((id) => {
        const paint = (el) => {
          const s = el && getComputedStyle(el);
          return s ? `${s.color} on ${s.backgroundColor}, glyph ${getComputedStyle(el.querySelector(".coder-icon")).color}` : "";
        };
        return {
          card: paint(document.querySelector(`#assistant-projects [data-assistant-project-job="${id}"] [data-assistant-job-state]`)),
          tab: paint(document.querySelector(`#assistant-coders [data-assistant-job="${id}"] [data-assistant-job-state]`)),
        };
      }, id);
      const seen = [];
      try {
        for (const scheme of ["dark", "light"]) {
          await page.emulateMedia({ colorScheme: scheme });
          await openPage(page, owner);
          await until(page, REPO, (c) => c.jobs.some((j) => j.id === worker && j.state === "steering"), "the steered row is not there");
          assert(await page.evaluate((s) => document.documentElement.getAttribute("data-bs-theme") === s, scheme), `the page does not paint ${scheme}`);
          for (const id of [worker, stopped]) {
            await page.mouse.move(1, 1);
            await sleep(200);
            const rest = await look(id);
            assert(rest.card && rest.card === rest.tab, `the ${scheme} icon at rest reads ${JSON.stringify(rest)}`);
            const box = await page.locator(`#assistant-projects [data-assistant-project-job="${id}"]`).boundingBox();
            await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
            await sleep(300);
            const hovered = await look(id);
            assert(hovered.card === rest.tab, `the ${scheme} icon hovered reads ${JSON.stringify(hovered)}`);
            seen.push(`${scheme} ${rest.card}`);
          }
        }
      } finally {
        await page.mouse.move(1, 1);
        await page.emulateMedia({ colorScheme: "light" });
      }
      return seen.join("; ");
    });

    await run("a coder's icon on a card and in the Coders tab is its terminal tab's, with its turn, news and job, live", async () => {
      assert(NOTIFY_DIR, "NOTIFY_DIR is not set: run with -v <state-dir>/notification-inbox/claude:/inbox -e NOTIFY_DIR=/inbox");
      const seen = [];
      const stripIcon = `terminal-tabs .terminal-tab[href="/coders/${worker}"] .dc-term-icon`;
      await page.goto(`${BASE}/coders/${worker}`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(stripIcon, { state: "visible", timeout: 10000 });
      const strip = await page.$eval(stripIcon, (el) => {
        const s = getComputedStyle(el);
        return { steered: el.classList.contains("steered") && el.classList.contains("running"), paint: `${s.color} on ${s.backgroundColor}` };
      });
      assert(strip.steered, "the steered coder's tab is not purple");
      await openPage(page, owner);
      let got = await untilIcons(page, worker, (g) => alike(g) && g.card.marks === "running steered", "the steered coder's rows do not wear its tab's icon");
      assert(got.card.paint === strip.paint && got.card.label === "Steering", `the steered rows read ${JSON.stringify(got)}, its tab ${strip.paint}`);
      seen.push(`steering ${got.card.paint}`);
      got = await untilIcons(page, stopped, (g) => alike(g) && g.card.marks === "running" && g.card.label === "Released, running", "a released coder that runs is not green");

      hook(stopped, "UserPromptSubmit");
      got = await untilIcons(page, stopped, (g) => alike(g) && g.card.marks === "running working" && g.card.ring !== "none", "the coder's turn did not reach the rows");
      hook(stopped, "Stop");
      got = await untilIcons(page, stopped, (g) => alike(g) && g.card.marks === "running news" && g.card.shown && g.card.dot, "the coder's news did not reach the rows");
      await page.click("#assistant-coders-tab");
      await untilIcons(page, stopped, (g) => g.tab.shown && g.tab.dot, "the Coders tab shows no news dot");
      await page.click("#assistant-projects-tab");
      await page.bringToFront();
      await page.click(`#assistant-projects a[data-assistant-project-job="${stopped}"]`);
      await page.waitForURL((u) => u.pathname === `/coders/${stopped}`, { timeout: 15000 });
      await openPage(page, owner);
      await untilIcons(page, stopped, (g) => alike(g) && g.card.marks === "running" && !g.card.dot, "opening the coder did not read its news");
      seen.push("turn, news, read");

      await page.evaluate(() => {
        window.__noReload = 1;
        window.__acts = 0;
        document.addEventListener("dc:activity", () => { window.__acts++; });
      });
      await steer(page, owner, worker, "SLOW WAKE_NOTHING: never true");
      hook(worker, "Stop");
      got = await untilIcons(page, worker, (g) => alike(g) && g.card.marks === "running working steered" && g.card.held && g.card.label === "Checking",
        "a check on the job did not reach the rows", 30000);
      const acts = await page.evaluate(() => window.__acts);
      hook(closed[closed.length - 1], "UserPromptSubmit");
      await page.waitForFunction((n) => window.__acts > n, acts, { timeout: 15000 });
      got = await icons(page, worker);
      assert(alike(got) && got.card.marks === "running working steered" && got.card.held, `an activity event took the check's stroke: ${JSON.stringify(got)}`);
      hook(closed[closed.length - 1], "Stop");
      await release(page, worker);
      got = await untilIcons(page, worker, (g) => alike(g) && g.card.marks === "running" && !g.card.held && g.card.label === "Released, running", "the released coder that runs is not green");
      seen.push(`check held, released ${got.card.paint}`);

      await steer(page, owner, worker, "WAKE_BLOCKED");
      hook(worker, "Stop");
      got = await untilIcons(page, worker, (g) => alike(g) && g.card.marks === "running text-danger" && g.card.label === "Blocked", "the blocked job's running coder is not red", 45000);
      const bad = await page.evaluate(() => getComputedStyle(document.documentElement).getPropertyValue("--dc-bad-rgb").trim());
      assert(got.card.paint.startsWith(`rgb(${bad}) on `), `the blocked rows paint ${got.card.paint}`);
      hook(worker, "UserPromptSubmit");
      got = await untilIcons(page, worker, (g) => alike(g) && g.card.marks.startsWith("running working text-danger") && g.card.ring.includes(bad), "the blocked coder's turn is not a red stroke");
      hook(worker, "Stop");
      await untilIcons(page, worker, (g) => alike(g) && !g.card.marks.includes("working"), "the blocked coder's turn did not end");
      seen.push(`blocked ${got.card.paint}`);

      await postJSON(page, `/coders/${stopped}/stop`, {});
      got = await untilIcons(page, stopped, (g) => alike(g) && g.card.marks === "" && g.card.label === "Released, stopped", "the stopped coder is not grey");
      assert(await page.$(`#assistant-projects form[data-assistant-project-job="${stopped}"] [data-assistant-job-resume]`), "the stopped coder's row does not resume it");
      seen.push(`stopped ${got.card.paint}`);
      await steer(page, owner, worker);
      await untilIcons(page, worker, (g) => alike(g) && g.card.marks.startsWith("running steered"), "the job opened again is not purple");
      assert(await page.evaluate(() => window.__noReload === 1), "the page reloaded");
      if (SHOTS_DIR) await page.screenshot({ path: path.join(SHOTS_DIR, "assistant-projects-icons-desktop.png") });

      const quiet = closed[closed.length - 1];
      const mp = await mobilePage();
      await openPage(mp, owner);
      await mp.tap("[data-assistant-watching-button]");
      await mp.waitForSelector(`#assistant-aside.show [data-assistant-project="${REPO}"]`, { state: "visible", timeout: 10000 });
      got = await untilIcons(mp, worker, (g) => alike(g) && g.card.marks.startsWith("running steered") && g.card.shown, "the phone's rows do not wear the steered coder's icon");
      got = await untilIcons(mp, quiet, (g) => alike(g) && g.card.marks === "running news" && g.card.shown && g.card.dot, "the phone's card shows no news dot");
      if (SHOTS_DIR) await mp.screenshot({ path: path.join(SHOTS_DIR, "assistant-projects-icons-phone.png") });
      await mp.tap("#assistant-coders-tab");
      await untilIcons(mp, quiet, (g) => g.tab.shown && g.tab.dot, "the phone's Coders tab shows no news dot");
      await mp.bringToFront();
      await mp.tap(`#assistant-coders [data-assistant-job="${quiet}"] [data-assistant-job-open]`);
      await mp.waitForURL((u) => u.pathname === `/coders/${quiet}`, { timeout: 15000 });
      await openPage(mp, owner);
      await untilIcons(mp, quiet, (g) => alike(g) && g.card.marks === "running" && !g.card.dot, "opening the coder on the phone did not read its news");
      await page.bringToFront();
      seen.push("phone alike, news read");
      return seen.join("; ");
    });

    await run("Projects leads back from a project without an underline, its keyboard focus ringed", async () => {
      await openPage(page, owner);
      const back = "#assistant-projects [data-assistant-project-back]";
      await page.click(`[data-assistant-project="${REPO}"] [data-assistant-project-changes]`);
      await page.waitForSelector(back, { state: "visible", timeout: 15000 });
      const read = () => page.evaluate((sel) => {
        const el = document.querySelector(sel);
        const s = getComputedStyle(el);
        return { line: s.textDecorationLine, ring: s.boxShadow !== "none" || (s.outlineStyle !== "none" && parseFloat(s.outlineWidth) > 0), visible: el.matches(":focus-visible") };
      }, back);
      const box = await page.locator(back).boundingBox();
      await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
      await sleep(300);
      const hovered = await read();
      assert(hovered.line === "none", `the hovered back link is underlined: ${JSON.stringify(hovered)}`);
      await page.mouse.move(1, 1);
      const rest = await read();
      await page.keyboard.press("Shift");
      await page.evaluate((sel) => document.querySelector(sel).focus(), back);
      await sleep(300);
      const focused = await read();
      assert(!rest.ring && focused.visible && focused.ring && focused.line === "none", `the back link's focus reads ${JSON.stringify({ rest, focused })}`);
      await page.click(back);
      await page.waitForSelector(`[data-assistant-project="${REPO}"]`, { state: "visible", timeout: 10000 });
      return "no underline hovered, a ring on keyboard focus";
    });

    await run("three tabs share a desktop aside and a phone's sheet, Projects open", async () => {
      await openPage(page, owner);
      fits("desktop 1360", await strip(page));
      if (SHOTS_DIR) await page.screenshot({ path: path.join(SHOTS_DIR, "assistant-projects-desktop.png") });
      const mp = await mobilePage();
      await openPage(mp, owner);
      assert(!(await mp.locator(`[data-assistant-project="${REPO}"]`).isVisible()), "the phone shows the aside before it is opened");
      await mp.tap("[data-assistant-watching-button]");
      await mp.waitForSelector(`#assistant-aside.show [data-assistant-project="${REPO}"]`, { state: "visible", timeout: 10000 });
      await sleep(400);
      fits("phone 390", await strip(mp));
      if (SHOTS_DIR) await mp.screenshot({ path: path.join(SHOTS_DIR, "assistant-projects-phone.png") });
      await mp.tap(`#assistant-aside.show [data-assistant-project="${PLAIN}"] [data-assistant-project-older]`);
      const unfolded = await until(mp, PLAIN, (c) => c.olderOpen === "true" && c.jobs.every((j) => j.shown), "the phone's card did not unfold its older rows");
      assert(unfolded.older === "Show fewer coders", `the phone's unfolded card reads ${unfolded.older}`);
      await mp.tap(`#assistant-aside.show [data-assistant-project="${PLAIN}"] [data-assistant-project-older]`);
      const folded = await until(mp, PLAIN, (c) => c.olderOpen === "false" && c.jobs.filter((j) => j.shown).length === 3, "the phone's card did not fold its older rows");
      assert(folded.older === "Show 1 older coder", `the phone's folded card reads ${folded.older}`);
      await mp.setViewportSize({ width: 360, height: 780 });
      await sleep(300);
      fits("phone 360", await strip(mp));
      const inside = await mp.evaluate((name) => {
        const r = document.querySelector(`[data-assistant-project="${name}"]`).getBoundingClientRect();
        return r.left >= 0 && r.right <= window.innerWidth;
      }, REPO);
      assert(inside, "a card sticks out of the phone's sheet");
      return "thirds on one line at 1360, 390 and 360";
    });

    await run("the aside drags at its left edge like the list column, live and kept, the phone's sheet stays", async () => {
      const key = "dc-ctx-aside-width:assistants";
      const handle = "#assistant-aside > [data-ctx-resize=aside]";
      const read = (p) => p.evaluate((sel) => {
        const box = (el) => {
          const r = el.getBoundingClientRect();
          return { left: Math.round(r.left), right: Math.round(r.right), width: Math.round(r.width), top: Math.round(r.top), bottom: Math.round(r.bottom) };
        };
        const grip = document.querySelector(sel);
        return {
          aside: box(document.querySelector("#assistant-aside")),
          thread: box(document.querySelector("[data-assistant-scroll]")),
          composer: box(document.querySelector("[data-assistant-footer]")),
          column: box(document.querySelector(".dc-app > .dc-ctx")),
          grip: box(grip),
          display: getComputedStyle(grip).display,
          cursor: getComputedStyle(grip).cursor,
          inner: window.innerWidth,
        };
      }, handle);
      const drag = async (from, to) => {
        const y = from.aside.top + 200;
        await page.mouse.move(from.grip.left + from.grip.width / 2, y);
        await page.mouse.down();
        await page.mouse.move(to, y, { steps: 6 });
        const live = await read(page);
        await page.mouse.up();
        return live;
      };
      const stored = (p) => p.evaluate((k) => localStorage.getItem(k), key);
      const mp = await mobilePage();
      try {
        await page.setViewportSize({ width: 1440, height: 900 });
        await openPage(page, owner);
        const before = await read(page);
        assert(before.display !== "none" && before.cursor === "col-resize" && before.grip.width === 6, `the handle is not the list column's: ${JSON.stringify(before)}`);
        assert(before.grip.left === before.aside.left && before.grip.top === before.aside.top && before.grip.bottom === before.aside.bottom,
          `the handle does not run along the aside's left edge: ${JSON.stringify(before)}`);
        const live = await drag(before, before.grip.left - 120);
        assert(live.aside.width >= before.aside.width + 100, `the drag did not widen the aside: ${before.aside.width} -> ${live.aside.width}`);
        assert(live.thread.width <= before.thread.width - 100 && live.thread.right <= live.aside.left && live.composer.right <= live.aside.left && live.composer.width <= before.composer.width - 100,
          `the thread and the composer did not follow during the drag: ${JSON.stringify({ before, live })}`);
        const wider = (await read(page)).aside.width;
        assert((await stored(page)) === String(wider), `the width is not kept like the column's: ${await stored(page)} for ${wider}`);
        assert((await read(page)).column.width === before.column.width, "the list column moved with the aside");

        const far = await drag(await read(page), 80);
        const half = Math.round(far.inner * 0.5);
        assert(far.aside.width === half && (await stored(page)) === String(half), `the aside is not held at half the window: ${far.aside.width}, kept ${await stored(page)}`);
        await openPage(page, owner);
        assert((await read(page)).aside.width === half, `a reload did not keep the width: ${(await read(page)).aside.width}`);

        await page.setViewportSize({ width: 1200, height: 900 });
        await sleep(300);
        const narrow = await read(page);
        assert(narrow.thread.width >= 319 && narrow.aside.right <= narrow.inner && narrow.aside.width >= 200,
          `the thread is crushed beside a wide aside: ${JSON.stringify(narrow)}`);
        await page.setViewportSize({ width: 1440, height: 900 });
        await sleep(300);

        await page.dblclick(handle);
        assert((await read(page)).aside.width === before.aside.width && (await stored(page)) === null, "a double click did not put the default width back");

        await mp.evaluate((k) => localStorage.setItem(k, "700"), key);
        await openPage(mp, owner);
        await mp.tap("[data-assistant-watching-button]");
        await mp.waitForSelector("#assistant-aside.show", { state: "visible", timeout: 10000 });
        await sleep(400);
        const sheet = await read(mp);
        assert(sheet.display === "none", "the phone's sheet shows the handle");
        assert(sheet.aside.width === Math.min(420, sheet.inner) && sheet.aside.right === sheet.inner, `the phone's sheet took the dragged width: ${JSON.stringify(sheet.aside)}`);
        return `${before.aside.width} -> ${wider}, held at ${half}, thread ${narrow.thread.width} at 1200, sheet ${sheet.aside.width}`;
      } finally {
        await page.evaluate((k) => localStorage.removeItem(k), key).catch(() => {});
        await mp.evaluate((k) => localStorage.removeItem(k), key).catch(() => {});
        await page.setViewportSize({ width: 1360, height: 900 });
      }
    });
  } finally {
    for (const id of [worker, stopped, ...closed]) {
      if (id) await postJSON(page, `/coders/${id}/delete`, {}).catch(() => {});
    }
    if (owner) await postJSON(page, `/assistants/${owner}`, { form: "delete" }).catch(() => {});
    for (const name of [REPO, PLAIN]) await L.deleteProject(page, name).catch(() => {});
  }
});
