const L = require("./lib");
const { assert, BASE, sleep } = L;

// Forge events: an assistant's trigger on the end of a pipeline
// (internal/forge ends.go, the forge source of internal/assistant), made in
// the trigger form (`Pipeline succeeded`, `Pipeline failed`, `Pipeline ended`
// with a project select and a branch field, data-trigger-target="forge") or
// with the fields `trigger-new forge-pipeline-failed --project` posts. The forge
// poller reads the pipeline of the branch a bound project has checked out,
// and its end fires the triggers on that project once per pipeline and final
// status: the reaction reads the headline naming branch, project, change
// request and status, and runs the `forge-show` line its text names. A read
// of the same end again fires nothing, a retried pipeline that passes fires
// the trigger on succeeded, a trigger on another branch stays quiet, and a change
// of the trigger moves its branch. A trigger on `Review comment` fires on a
// note somebody else wrote on the merge request of that branch, once per
// note, read with the review threads after a push (an update-ref of the
// tracking ref): the reaction reads the headline naming author, branch,
// project and change request and runs the `forge-show --threads` line its
// text names, while a note of the token's own account (root on the fake),
// the notes that stood at the first read and the same notes read again fire
// nothing, and neither does a trigger on the end of a pipeline. The forge is
// the runner's own fake (forge-fake.js) on FORGE_PORT (default 3157),
// published on the host's loopback, where the instance reaches it at
// FORGE_ADDR (default 127.0.0.1).
//
// The instance MUST be the assistant one (tests/e2e/fakes ahead of the real
// CLIs on PATH, a scratch HOME). FORGE_EVENT in a task makes the fake claude
// answer with the event's line and what the forge-show line of its text
// printed. A pipeline ends on the poller's next read, up to 20 seconds after
// the fake moved; a run takes about three minutes per engine.

const { startFakeForge, addHost, gitlabNote, gitlabDiscussion, gitlabDiscussions } = require("./forge-fake");

const FAKE_PORT = Number(process.env.FORGE_PORT || 3157);
const FAKE_TLS_PORT = Number(process.env.FORGE_TLS_PORT || 3158);
const FAKE_ADDR = process.env.FORGE_ADDR || "127.0.0.1";
const TOKEN = `glpat-e2e${Date.now().toString(36)}Ev5wR2sT9uV4`;
// Past the poller's fast interval of 15 seconds and its look every 5.
const QUIET_MS = 17000;
let fakeForge = null;

L.runFeature("FORGE_EVENTS", async ({ page, run, mobilePage }) => {
  const stamp = Date.now().toString(36);
  const project = `zzforgeev-${stamp}`;
  const alias = `fe-${stamp}`;
  const fakeWeb = `http://${FAKE_ADDR}:${FAKE_PORT}`;
  fakeForge ||= await startFakeForge({ port: FAKE_PORT, tlsPort: FAKE_TLS_PORT, token: TOKEN });
  let path = "";
  let hostEdit = "";
  let assistant = "";
  let failedTrigger = "";
  let doneTrigger = "";
  let mainTrigger = "";
  let reviewTrigger = "";
  let reviewMainTrigger = "";

  const git = async (args) => {
    const res = await page.evaluate(async ({ cwd, args }) => {
      const token = document.querySelector('meta[name="csrf-token"]')?.content || "";
      const r = await fetch("/git", {
        method: "POST",
        headers: { "X-CSRF-Token": token, "Content-Type": "application/json" },
        body: JSON.stringify({ cwd, args }),
      });
      if (!r.ok) return { failed: `${r.status} ${await r.text()}` };
      const d = await r.json();
      return { code: d.exitCode, out: atob(d.stdout || ""), err: atob(d.stderr || "") };
    }, { cwd: path, args });
    if (res.failed || res.code !== 0) throw new Error(`git ${args.join(" ")}: ${res.failed || res.err}`);
    return res.out.trim();
  };

  // post sends a form the way a page does, JSON back where the route
  // answers it.
  const post = (target, fields) => page.evaluate(async ({ target, fields }) => {
    const body = new URLSearchParams({ ...fields, csrf_token: document.querySelector('meta[name="csrf-token"]').content });
    const r = await fetch(target, { method: "POST", body, headers: { Accept: "application/json" } });
    return { status: r.status, url: r.url, body: await r.json().catch(() => ({})) };
  }, { target, fields });

  const readJSON = (target) => page.evaluate(async (t) => {
    const r = await fetch(t, { headers: { Accept: "application/json" } });
    return { code: r.status, body: await r.json().catch(() => ({})) };
  }, target);
  const status = (query = "") => readJSON(`/projects/${project}/forge/status${query}`);

  const openAssistant = async (p) => {
    await p.goto(`${BASE}/assistants/${assistant}`, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(p);
    await p.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
  };

  // rows reads the fragment the aside's list is fed from.
  const rows = () => page.evaluate(async (id) => {
    const res = await fetch(`/assistants/triggers?assistant=${id}`, { headers: { Accept: "text/html" } });
    const holder = document.createElement("div");
    holder.innerHTML = await res.text();
    return [...holder.querySelectorAll("[data-assistant-trigger]")].map((row) => ({
      id: row.getAttribute("data-assistant-trigger"),
      event: row.querySelector("[data-assistant-trigger-event]")?.textContent.trim().replace(/\s+/g, " ") || "",
      facts: row.querySelector("[data-assistant-trigger-facts]")?.textContent.trim() || "",
      note: row.querySelector("[data-assistant-trigger-note]")?.textContent.trim() || "",
      reacting: !!row.querySelector('[data-assistant-working="react"]'),
      edit: row.querySelector("[data-assistant-trigger-edit]")?.getAttribute("href") || "",
    }));
  }, assistant);
  const row = async (id) => (await rows()).find((r) => r.id === id) || null;
  const waitRow = async (id, want, timeout = 30000) => {
    const deadline = Date.now() + timeout;
    let seen = null;
    while (Date.now() < deadline) {
      seen = await row(id);
      if (seen && want(seen)) return seen;
      await sleep(400);
    }
    assert(false, `trigger ${id} is ${JSON.stringify(seen)}`);
    return seen;
  };

  // pushed reads the answers the reactions pushed into the thread, the
  // header they are read by and their text, from the page itself.
  const pushed = () => page.evaluate(() => [...document.querySelectorAll("[data-assistant-message][data-message-id][data-assistant-auto]")].map((m) => ({
    id: m.getAttribute("data-message-id"),
    state: m.getAttribute("data-state"),
    headline: m.querySelector("[data-assistant-origin] [data-assistant-note-headline]")?.textContent.trim() || "",
    text: (m.querySelector("[data-assistant-text]")?.textContent || "").trim(),
  })));
  const newPushed = async (before, timeout = 60000) => {
    const deadline = Date.now() + timeout;
    let seen = [];
    while (Date.now() < deadline) {
      seen = await pushed();
      const fresh = seen.find((m) => m.state === "complete" && !before.has(m.id));
      if (fresh) return fresh;
      await sleep(500);
    }
    assert(false, `no answer was pushed, the thread holds ${JSON.stringify(seen)}`);
    return null;
  };
  const pushedIDs = async () => new Set((await pushed()).map((m) => m.id));

  const formShown = async (p) => {
    await p.waitForSelector(".modal.show [data-assistant-trigger-form]", { timeout: 8000 });
    await p.waitForFunction(() => {
      const modal = document.querySelector(".modal.show");
      const dialog = modal && window.bootstrap?.Modal.getInstance(modal);
      return !!dialog && !dialog._isTransitioning;
    }, null, { timeout: 8000 });
  };
  // boxes reads which of the form's fields show, by their real box.
  const boxes = (p, scope) => p.evaluate((scope) => {
    const form = document.querySelector(`${scope} [data-assistant-trigger-form]`);
    const shown = (el) => {
      if (!el) return false;
      const box = el.getBoundingClientRect();
      const style = getComputedStyle(el);
      return box.width > 0 && box.height > 0 && style.display !== "none" && style.visibility !== "hidden";
    };
    const project = form.querySelector("[data-trigger-project]");
    const branch = form.querySelector("[data-trigger-branch]");
    const pbox = project.getBoundingClientRect();
    return {
      forge: shown(form.querySelector('[data-trigger-target="forge"]')),
      job: shown(form.querySelector('[data-trigger-target="job"]')),
      coder: shown(form.querySelector('[data-trigger-target="coder"]')),
      cron: shown(form.querySelector('[data-trigger-target="cron"]')),
      mode: shown(form.querySelector("[data-trigger-mode]")),
      batch: shown(form.querySelector("[data-trigger-batch]")),
      projectDisabled: project.disabled,
      branchDisabled: branch.disabled,
      projects: [...project.options].map((o) => o.value),
      picked: project.value,
      branch: branch.value,
      placeholder: branch.placeholder,
      left: pbox.left, right: pbox.right,
      inner: window.innerWidth, scroll: document.documentElement.scrollWidth,
    };
  }, scope);

  // push moves the branch one commit on, the fake first, so the poller reads
  // the branch and its review threads at once.
  const push = async (message) => {
    await git(["commit", "-q", "--allow-empty", "-m", message]);
    const head = await git(["rev-parse", "HEAD"]);
    fakeForge.state.head = head;
    await git(["update-ref", "refs/remotes/origin/feature", head]);
    return head;
  };
  const discussionsAsked = () => fakeForge.log.filter((r) => r.path.endsWith("/merge_requests/4/discussions")).length;
  const discussionsRead = async (before) => {
    for (let i = 0; i < 300 && discussionsAsked() <= before; i++) await sleep(100);
    assert(discussionsAsked() > before, "the poller never read the discussions again");
  };
  // extra are the discussions the merge request holds beyond the fake's own.
  const extra = [];
  const atLine = (state) => ({ resolvable: true, at: { head: state.head, newLine: 4 } });

  try {
    await run("setup: an assistant and a repository on the fake GitLab whose pushed branch feature runs a pipeline", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      const made = await post("/assistants/new", { form: "new", coder: "claude" });
      assistant = made.body.id || "";
      assert(assistant, `no assistant of its own: ${JSON.stringify(made)}`);
      await L.createProject(page, project);
      path = await L.projectPath(page, project);
      await git(["init", "-q", "-b", "main"]);
      for (const [key, value] of [["user.email", "e2e@example.com"], ["user.name", "e2e"], ["commit.gpgsign", "false"]]) await git(["config", key, value]);
      await git(["commit", "-q", "--allow-empty", "-m", "first"]);
      await git(["switch", "-q", "-c", "feature"]);
      await git(["commit", "-q", "--allow-empty", "-m", "pushed"]);
      const pushedHead = await git(["rev-parse", "HEAD"]);
      await git(["update-ref", "refs/remotes/origin/feature", pushedHead]);
      await git(["remote", "add", "origin", `${fakeWeb}/gl/team/sub/app.git`]);
      fakeForge.state.head = pushedHead;
      fakeForge.state.status = "running";
      fakeForge.state.discussions = null;
      await page.goto(`${BASE}/settings/forges`, { waitUntil: "domcontentloaded" });
      const added = await addHost(page, { kind: "gitlab", web_url: `${fakeWeb}/gl`, aliases: alias, token: TOKEN });
      assert(!added.failed, added.failed);
      await page.goto(`${BASE}/settings/forges`, { waitUntil: "domcontentloaded" });
      hostEdit = await page.evaluate((mark) => [...document.querySelectorAll("[data-forge-host]")]
        .find((r) => r.textContent.includes(mark))?.querySelector("[data-forge-edit]")?.getAttribute("href") || "", alias);
      assert(hostEdit, "the added host has no Edit on the list");
      const { code, body } = await status();
      assert(code === 200 && body.pipeline?.status === "running" && body.change?.ref === "!4", `the branch reads ${code} ${JSON.stringify(body)}`);
    });

    await run("the form offers the pipeline events with the bound project and a branch, and nothing of the other events", async () => {
      await openAssistant(page);
      await page.setViewportSize({ width: 1280, height: 800 });
      await page.click("[data-assistant-triggers-tab]");
      await page.click("#assistant-triggers [data-assistant-trigger-new]");
      await formShown(page);
      const labels = await page.evaluate(() => [...document.querySelectorAll(".modal.show [data-trigger-event] option")]
        .filter((o) => o.dataset.source === "forge").map((o) => `${o.value}=${o.textContent.trim()}`));
      assert(JSON.stringify(labels) === JSON.stringify(["forge-pipeline-succeeded=Pipeline succeeded", "forge-pipeline-failed=Pipeline failed", "forge-pipeline-ended=Pipeline ended", "forge-review-comment=Review comment"]), `the forge events read ${JSON.stringify(labels)}`);
      let got = await boxes(page, ".modal.show");
      assert(!got.forge && got.projectDisabled && got.branchDisabled, `a job event shows the project: ${JSON.stringify(got)}`);
      await page.selectOption(".modal.show [data-trigger-event]", "forge-pipeline-failed");
      got = await boxes(page, ".modal.show");
      assert(got.forge && !got.job && !got.coder && !got.cron && !got.mode && got.batch, `a pipeline event shows ${JSON.stringify(got)}`);
      assert(!got.projectDisabled && !got.branchDisabled && got.projects.includes(project) && got.branch === "" && got.placeholder === "Any, the one checked out", `the project and the branch read ${JSON.stringify(got)}`);
      await page.selectOption(".modal.show [data-trigger-project]", project);
      await page.fill(".modal.show #assistant-trigger-task", "FORGE_EVENT the failed one");
      await page.fill(".modal.show #assistant-trigger-batch", "0");
      await page.click('.modal.show button[type="submit"]');
      await page.waitForSelector(".modal.show", { state: "hidden", timeout: 8000 });
      const made = await (async () => {
        const deadline = Date.now() + 15000;
        while (Date.now() < deadline) {
          const hit = (await rows()).find((r) => r.event === `Pipeline failed · ${project}`);
          if (hit) return hit;
          await sleep(400);
        }
        return null;
      })();
      assert(made, `no row reads the trigger: ${JSON.stringify(await rows())}`);
      assert(/fired 0/.test(made.facts), `the new trigger reads ${JSON.stringify(made)}`);
      failedTrigger = made.id;
    });

    await run("a failed pipeline fires it, the reaction reads the headline and the forge-show its text names", async () => {
      await openAssistant(page);
      const before = await pushedIDs();
      fakeForge.state.status = "failed";
      const answer = await newPushed(before, 60000);
      const headline = `Pipeline failed: feature in ${project}, merge request !4`;
      assert(answer.headline === headline, `the answer is read by ${JSON.stringify(answer)}`);
      for (const said of [`Event: ${headline}`, `${project} is on GitLab`, `Branch "feature", pushed at`, `Merge request !4 "Fix the login"`, "Pipeline #12: failed", `Log of "unit"`, "--- FAIL: TestLogin"]) {
        assert(answer.text.includes(said), `the reaction did not read "${said}": ${answer.text}`);
      }
      const fired = await waitRow(failedTrigger, (r) => /fired 1/.test(r.facts) && !r.reacting);
      assert(fired.note === `Answered for ${headline}.`, `the row says ${JSON.stringify(fired)}`);
      const news = await readJSON("/notifications");
      const ours = (news.body.notifications || []).filter((n) => n.targetId === assistant);
      assert(ours[0]?.title === "Trigger fired." && (ours[0].detail || "").startsWith(headline), `the news reads ${JSON.stringify(ours[0])}`);
      assert(!(await page.content()).includes(TOKEN), "the page shows the token");
    });

    await run("the same end read again fires nothing", async () => {
      const before = await pushedIDs();
      for (let i = 0; i < 2; i++) {
        const { code, body } = await status("?fresh=1");
        assert(code === 200 && body.pipeline?.status === "failed", `a fresh read answered ${code} ${JSON.stringify(body)}`);
      }
      await sleep(QUIET_MS);
      const still = await row(failedTrigger);
      assert(/fired 1/.test(still.facts), `the end fired again: ${JSON.stringify(still)}`);
      const fresh = (await pushed()).filter((m) => !before.has(m.id));
      assert(fresh.length === 0, `answers were pushed again: ${JSON.stringify(fresh)}`);
    });

    await run("a trigger names a bound project: what trigger-new posts is taken, a project of no forge and none at all are refused", async () => {
      const fields = { form: "new", assistant, task: "FORGE_EVENT the passed one", batch: "0" };
      const done = await post("/assistants/triggers", { ...fields, event: "forge-pipeline-succeeded", project });
      assert(done.status === 200 && done.body.summary === `Pipeline succeeded, ${project}`, `forge-pipeline-succeeded answered ${done.status} ${JSON.stringify(done.body)}`);
      doneTrigger = done.body.id;
      const main = await post("/assistants/triggers", { ...fields, event: "forge-pipeline-ended", project, branch: "main" });
      assert(main.status === 200 && main.body.summary === `Pipeline ended, main in ${project}`, `forge-pipeline-ended on main answered ${main.status} ${JSON.stringify(main.body)}`);
      mainTrigger = main.body.id;
      for (const [extra, said] of [
        [{ project: `${project}-nope` }, `No project is called "${project}-nope".`],
        [{}, "A pipeline or review event needs --project"],
        [{ project, branch: "a b" }, "A branch name holds no spaces, no control characters and no invisible characters, its character 2 is U+0020."],
        [{ project, branch: "x\u202egnp" }, "A branch name holds no spaces, no control characters and no invisible characters, its character 2 is U+202E."],
        [{ project, branch: "b".repeat(256) }, "A branch name has at most 255 characters."],
      ]) {
        const refused = await post("/assistants/triggers", { ...fields, event: "forge-pipeline-failed", ...extra });
        assert(refused.status === 400 && (refused.body.error || "").includes(said), `${JSON.stringify(extra)} answered ${refused.status} ${JSON.stringify(refused.body)}`);
      }
      const listed = await readJSON(`/assistants/triggers?assistant=${assistant}`);
      const byID = Object.fromEntries((listed.body.triggers || []).map((t) => [t.id, t]));
      const m = byID[mainTrigger];
      assert(m && m.event === "forge-pipeline-ended" && m.project === project && m.branch === "main" && m.where === `main in ${project}` && m.label === "Pipeline ended", `the list reads ${JSON.stringify(m)}`);
    });

    await run("a retried pipeline that passes fires the trigger on succeeded, the one on another branch stays", async () => {
      await openAssistant(page);
      const before = await pushedIDs();
      fakeForge.state.status = "running";
      const { code, body } = await status("?fresh=1");
      assert(code === 200 && body.pipeline?.status === "running", `the retry reads ${code} ${JSON.stringify(body)}`);
      fakeForge.state.status = "success";
      const answer = await newPushed(before, 60000);
      const headline = `Pipeline succeeded: feature in ${project}, merge request !4`;
      assert(answer.headline === headline && answer.text.includes(`Event: ${headline}`) && answer.text.includes("Pipeline #12: success"), `the answer reads ${JSON.stringify(answer)}`);
      await waitRow(doneTrigger, (r) => /fired 1/.test(r.facts) && !r.reacting);
      await sleep(3000);
      const main = await row(mainTrigger);
      const failed = await row(failedTrigger);
      assert(/fired 0/.test(main.facts) && /fired 1/.test(failed.facts), `the others read ${JSON.stringify([main, failed])}`);
    });

    await run("Change opens on the project and the branch, and an emptied branch is whichever is checked out", async () => {
      await openAssistant(page);
      await page.click("[data-assistant-triggers-tab]");
      await page.click(`[data-assistant-trigger="${mainTrigger}"] [data-assistant-trigger-edit]`);
      await formShown(page);
      const got = await boxes(page, ".modal.show");
      assert(got.forge && got.picked === project && got.branch === "main", `the change opens on ${JSON.stringify(got)}`);
      await page.fill(".modal.show [data-trigger-branch]", "");
      await page.click('.modal.show button[type="submit"]');
      await page.waitForSelector(".modal.show", { state: "hidden", timeout: 8000 });
      const moved = await waitRow(mainTrigger, (r) => r.event === `Pipeline ended · ${project}`, 15000);
      assert(/fired 0/.test(moved.facts), `the change moved the count: ${JSON.stringify(moved)}`);
    });

    await run("the form offers Review comment with the project and the branch, and makes a trigger on it", async () => {
      fakeForge.state.discussions = (state) => [...gitlabDiscussions(state), ...extra.map((d) => d(state))];
      await openAssistant(page);
      await page.click("[data-assistant-triggers-tab]");
      await page.click("#assistant-triggers [data-assistant-trigger-new]");
      await formShown(page);
      await page.selectOption(".modal.show [data-trigger-event]", "forge-review-comment");
      const got = await boxes(page, ".modal.show");
      assert(got.forge && !got.job && !got.coder && !got.cron && !got.mode && got.batch && !got.projectDisabled && got.projects.includes(project), `a review event shows ${JSON.stringify(got)}`);
      await page.selectOption(".modal.show [data-trigger-project]", project);
      await page.fill(".modal.show #assistant-trigger-task", "FORGE_EVENT the review");
      await page.fill(".modal.show #assistant-trigger-batch", "0");
      await page.click('.modal.show button[type="submit"]');
      await page.waitForSelector(".modal.show", { state: "hidden", timeout: 8000 });
      const deadline = Date.now() + 15000;
      let made = null;
      while (!made && Date.now() < deadline) {
        made = (await rows()).find((r) => r.event === `Review comment · ${project}`) || null;
        if (!made) await sleep(400);
      }
      assert(made && /fired 0/.test(made.facts), `no row reads the trigger: ${JSON.stringify(await rows())}`);
      reviewTrigger = made.id;
      const main = await post("/assistants/triggers", { form: "new", assistant, task: "FORGE_EVENT the review on main", batch: "0", event: "forge-review-comment", project, branch: "main" });
      assert(main.status === 200 && main.body.summary === `Review comment, main in ${project}`, `forge-review-comment on main answered ${main.status} ${JSON.stringify(main.body)}`);
      reviewMainTrigger = main.body.id;
    });

    await run("a note somebody else wrote fires it once, the reaction reads it and every thread, no pipeline trigger and no other branch takes it", async () => {
      await openAssistant(page);
      const before = await pushedIDs();
      extra.push((state) => gitlabDiscussion("d7e2e", false, [
        gitlabNote(42, "root", "Split this function?", atLine(state)),
        gitlabNote(43, "alice", "Agreed, split it\ninto two.", atLine(state)),
      ]));
      await push("review");
      const answer = await newPushed(before, 60000);
      const headline = `Review comment: alice on feature in ${project}, merge request !4`;
      assert(answer.headline === headline, `the answer is read by ${JSON.stringify(answer)}`);
      for (const said of [`Event: ${headline}`, "Threads on the diff", "Split this function?", "Agreed, split it", "into two.", "Reply: yes, bb."]) {
        assert(answer.text.includes(said), `the reaction did not read "${said}": ${answer.text}`);
      }
      const fired = await waitRow(reviewTrigger, (r) => /fired 1/.test(r.facts) && !r.reacting);
      assert(fired.note === `Answered for ${headline}.`, `the row says ${JSON.stringify(fired)}`);
      await sleep(3000);
      const fresh = (await pushed()).filter((m) => !before.has(m.id));
      assert(fresh.length === 1, `the notes pushed ${JSON.stringify(fresh)}`);
      const others = await Promise.all([reviewMainTrigger, mainTrigger, failedTrigger, doneTrigger].map(row));
      assert(/fired 0/.test(others[0].facts) && /fired 0/.test(others[1].facts) && /fired 1/.test(others[2].facts) && /fired 1/.test(others[3].facts), `the other triggers read ${JSON.stringify(others)}`);
      assert(!(await page.content()).includes(TOKEN), "the page shows the token");
    });

    await run("a note of the token's own account and the same notes read again fire nothing", async () => {
      const before = await pushedIDs();
      extra.push(() => gitlabDiscussion("d8e2e", true, [gitlabNote(44, "root", "Merging after lunch.")]));
      let asked = discussionsAsked();
      await push("own note");
      await discussionsRead(asked);
      asked = discussionsAsked();
      await push("again");
      await discussionsRead(asked);
      await sleep(3000);
      const still = await row(reviewTrigger);
      assert(/fired 1/.test(still.facts), `the trigger fired again: ${JSON.stringify(still)}`);
      const fresh = (await pushed()).filter((m) => !before.has(m.id));
      assert(fresh.length === 0, `answers were pushed: ${JSON.stringify(fresh)}`);
    });

    await run("phone: the pipeline fields stand in the form within 390px", async () => {
      const phone = await mobilePage();
      await phone.goto(`${BASE}/assistants/triggers?form=new&assistant=${assistant}`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(phone);
      await phone.waitForSelector("[data-assistant-trigger-form]", { timeout: 8000 });
      await phone.selectOption("[data-trigger-event]", "forge-pipeline-ended");
      const got = await boxes(phone, "body");
      assert(got.forge && got.left >= 0 && got.right <= got.inner && got.scroll <= got.inner, `the field stands at ${JSON.stringify(got)}`);
    });

    await run("docs: the Forges topic explains the pipeline and the review comment events", async () => {
      await page.goto(`${BASE}/docs`, { waitUntil: "domcontentloaded" });
      const item = page.locator("section#forges .list-group-item", { hasText: "Pipeline events" });
      assert(await item.count() === 1, "no docs entry for the pipeline events");
      const text = await item.innerText();
      for (const said of ["Pipeline failed", "the pipeline of the checked out branch", "once per pipeline and end, also across a restart", "ended before the cockpit first read the branch fires nothing"]) {
        assert(text.includes(said), `the entry lacks "${said}": ${text}`);
      }
      const review = page.locator("section#forges .list-group-item", { hasText: "Review comment events" });
      assert(await review.count() === 1, "no docs entry for the review comment events");
      const reviewText = await review.innerText();
      for (const said of ["Review comment", "once per note somebody else wrote", "also across a restart", "Notes of the token's account and those standing at the first read fire nothing"]) {
        assert(reviewText.includes(said), `the entry lacks "${said}": ${reviewText}`);
      }
      const triggers = await page.locator("section .list-group-item", { hasText: "It reacts to events" }).innerText();
      assert(triggers.includes("the pipeline of a project on a forge passing, failing or ending, a review comment on its change request"), `the triggers entry reads ${triggers}`);
    });
  } finally {
    if (hostEdit) await post(hostEdit, { form: "delete" }).catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
    if (assistant) await post(`/assistants/${assistant}`, { form: "delete" }).catch(() => {});
  }
});
