const http = require("http");
const L = require("./lib");
const { assert, BASE, sleep } = L;

// Forge retry: the failed jobs of a failed pipeline run again on the forge
// (POST /projects/:name/forge/retry, internal/forge retry.go): from the git
// menu of the project's row, from Retry failed jobs in the editor's forge
// sheet and its Jobs, and one failed job from Retry job in its log, each a click of
// the user that runs at once, the forge read again first and a click on a
// pipeline that moved on refused. A token without the right to write is
// refused in a sentence, and a refusal in the forge's own words reads its
// control characters as escapes. An assistant's retry over the local socket waits
// for the user's approval (internal/approval, the Pipeline retry approval
// under Settings › Assistants › Approvals): the app-wide dialog names the
// pipeline, its failed jobs, what cannot run again, the branch, the forge and
// the project; Deny runs nothing, Approve reads the forge again and runs the
// jobs, a branch pushed again meanwhile fails it, and the note in the
// assistant's thread says how it ended; with the switch off the retry runs at
// once. On a GitHub Enterprise Server the failed jobs of each failed workflow
// run re-run, one job re-runs on its own, and a check run of another app and
// a commit status are named as unable to run again, the row standing off
// with the reason where nothing else failed. A guest of a shared link
// reaches no retry.
//
// The forge is the runner's own fake (forge-fake.js) on FORGE_PORT (default
// 3157), published on the host's loopback, where the instance reaches it at
// FORGE_ADDR (default 127.0.0.1). The instance MUST be the assistant one
// (tests/e2e/fakes ahead of the real CLIs on PATH, a scratch HOME), with its
// local API socket mounted (APISOCK, default /apisock/s), because nothing but
// the socket may act as an assistant. A run takes about two minutes.

const { startFakeForge, addHost } = require("./forge-fake");

const FAKE_PORT = Number(process.env.FORGE_PORT || 3157);
const FAKE_TLS_PORT = Number(process.env.FORGE_TLS_PORT || 3158);
const FAKE_ADDR = process.env.FORGE_ADDR || "127.0.0.1";
const APISOCK = process.env.APISOCK || "/apisock/s";
const TOKEN = `glpat-e2e${Date.now().toString(36)}Rt4wR2sT9uV4`;
const DIALOG = ".swal2-popup:not(.swal2-toast)";
let fakeForge = null;

// asAssistant posts one form over the local socket as the assistant, which
// is what the generated `cockpit` wrapper does for a turn.
function asAssistant(id, path, body) {
  return new Promise((resolve, reject) => {
    const data = new URLSearchParams(body).toString();
    const req = http.request({
      socketPath: APISOCK,
      path,
      method: "POST",
      headers: {
        "X-Dev-Cockpit-Assistant": id,
        "Content-Type": "application/x-www-form-urlencoded",
        "Content-Length": Buffer.byteLength(data),
        Accept: "application/json",
      },
    }, (res) => {
      let raw = "";
      res.on("data", (chunk) => { raw += chunk; });
      res.on("end", () => {
        let json = {};
        try { json = JSON.parse(raw); } catch {}
        resolve({ status: res.statusCode, json, raw });
      });
    });
    req.on("error", reject);
    req.end(data);
  });
}

async function waitFor(fn, what, timeout = 20000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    if (await fn()) return;
    await sleep(250);
  }
  throw new Error(`timed out waiting for ${what}`);
}

L.runFeature("FORGE_RETRY", async ({ browser, page, run, mobilePage, bag }) => {
  const stamp = Date.now().toString(36);
  const project = `zzforgeretry-${stamp}`;
  const alias = `fr-${stamp}`;
  const linkName = `forge retry ${stamp}`;
  const fakeWeb = `http://${FAKE_ADDR}:${FAKE_PORT}`;
  const editor = `${BASE}/projects/${project}/editor`;
  fakeForge ||= await startFakeForge({ port: FAKE_PORT, tlsPort: FAKE_TLS_PORT, token: TOKEN });
  const state = fakeForge.state;
  let path = "";
  let hostEdit = "";
  let pushed = "";
  let assistant = "";
  let linkId = "";

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

  // post sends a form the way a page does, the JSON or the page back.
  const post = (target, fields) => page.evaluate(async ({ target, fields }) => {
    const body = new URLSearchParams([...fields, ["csrf_token", document.querySelector('meta[name="csrf-token"]').content]]);
    const r = await fetch(target, { method: "POST", body, headers: { Accept: "application/json" } });
    const text = await r.text();
    let json = {};
    try { json = JSON.parse(text); } catch {}
    return { status: r.status, url: r.url, text, json };
  }, { target, fields: Array.isArray(fields) ? fields : Object.entries(fields) });

  // refresh has the forge read now, which the poller keeps and signals.
  const refresh = () => page.evaluate(async (p) => (await fetch(`/projects/${p}/forge/status?fresh=1`, { headers: { Accept: "application/json" } })).status, project);
  const writes = () => state.retried.splice(0);
  const asked = () => fakeForge.log.splice(0);

  const shown = (p, selector) => p.evaluate((sel) => [...document.querySelectorAll(sel)].filter((el) => {
    const box = el.getBoundingClientRect();
    const style = getComputedStyle(el);
    return box.width > 0 && box.height > 0 && style.visibility !== "hidden" && style.display !== "none";
  }).length, selector);
  // retryRow reads the retry row the sheet shows, null for none.
  const retryRow = (p) => p.evaluate(() => {
    const row = [...document.querySelectorAll("[data-editor-sheet-body] [data-forge-retry]")].find((r) => r.getBoundingClientRect().height > 0);
    if (!row) return null;
    const box = row.getBoundingClientRect();
    return {
      label: row.querySelector(":scope > .d-flex > span:first-child").textContent.trim(),
      sub: row.querySelector(":scope > .d-flex > .small").textContent.trim(),
      title: row.getAttribute("title") || "",
      disabled: row.disabled,
      opacity: Number(getComputedStyle(row).opacity),
      color: getComputedStyle(row).color,
      branch: row.dataset.forgeRetryBranch, head: row.dataset.forgeRetryHead, pipeline: row.dataset.forgeRetryPipeline, job: row.dataset.forgeRetryJob,
      pinned: row.hasAttribute("data-editor-sheet-pin"),
      left: box.left, right: box.right, inner: window.innerWidth,
    };
  });
  const toast = async (p, text) => {
    await p.waitForFunction((t) => [...document.querySelectorAll(".dc-toast")].some((el) => el.textContent.includes(t)), text, { timeout: 10000 });
  };
  const sheetPipeline = (p, want, timeout = 30000) => p.waitForFunction((want) => document.querySelector("[data-editor-sheet-body] [data-forge-head] [data-forge-badge]")?.dataset.forgePipeline === want, want, { timeout });
  const titled = async (p, title) => {
    try {
      await p.waitForFunction((title) => document.querySelector("[data-editor-sheet-title]")?.textContent.trim() === title, title, { timeout: 10000 });
    } catch {
      throw new Error(`the sheet is titled ${JSON.stringify(await p.evaluate(() => document.querySelector("[data-editor-sheet-title]")?.textContent.trim()))}, not ${JSON.stringify(title)}`);
    }
  };
  const openEditor = async (p) => {
    await p.goto(editor, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(p);
    await p.waitForSelector("[data-editor-git-status]:not([hidden])", { timeout: 20000 });
    const backdrop = p.locator("[data-editor-backdrop]");
    if (await backdrop.isVisible()) {
      await backdrop.click({ position: { x: 370, y: 300 } });
      await p.waitForSelector("[data-editor-backdrop]", { state: "hidden", timeout: 5000 });
    }
  };
  const openGit = async (p) => {
    await p.click("[data-editor-git-status]");
    await titled(p, "Git");
  };
  const openForge = async (p, title = "GitLab") => {
    await p.waitForSelector("[data-editor-forge-status]:not([hidden])", { timeout: 20000 });
    await p.click("[data-editor-forge-status]");
    await titled(p, title);
  };
  const failedAgain = async (p) => {
    state.status = "failed";
    await refresh();
    await sheetPipeline(p, "failed");
  };
  const dialogRows = () => page.locator(`${DIALOG} dl[data-approval-details]`).evaluate((list) => {
    const labels = [...list.querySelectorAll("dt")].map((dt) => dt.textContent.trim());
    return [...list.querySelectorAll("dd")].map((dd, i) => [labels[i], dd.textContent.trim()]);
  });
  // approvalNote reads the assistant's page with a fetch, not a visit: a
  // visit leaves the page's conversation stream connecting, and the next
  // navigation that cuts it is a page error in WebKit.
  const approvalNote = async (verdict, count) => {
    let note = null;
    await waitFor(async () => {
      note = await page.evaluate(async ({ target, verdict, count }) => {
        const html = await (await fetch(target)).text();
        const notes = new DOMParser().parseFromString(html, "text/html").querySelectorAll(`[data-assistant-note="approval"][data-assistant-approval-note="${verdict}"]`);
        if (notes.length < count) return null;
        const last = notes[count - 1];
        return { headline: last.querySelector("[data-assistant-note-headline]").textContent.trim(), text: last.parentElement.textContent };
      }, { target: `/assistants/${assistant}`, verdict, count });
      return note !== null;
    }, `${count} ${verdict} approval notes`, 30000);
    return note;
  };

  try {
    await run("setup: a repository on the fake GitLab whose pushed branch feature has a failed pipeline", async () => {
      await L.createProject(page, project);
      path = await L.projectPath(page, project);
      await git(["init", "-q", "-b", "main"]);
      for (const [key, value] of [["user.email", "e2e@example.com"], ["user.name", "e2e"], ["commit.gpgsign", "false"]]) await git(["config", key, value]);
      await git(["commit", "-q", "--allow-empty", "-m", "first"]);
      await git(["switch", "-q", "-c", "feature"]);
      await git(["commit", "-q", "--allow-empty", "-m", "pushed"]);
      pushed = await git(["rev-parse", "HEAD"]);
      await git(["update-ref", "refs/remotes/origin/feature", pushed]);
      await git(["remote", "add", "origin", `${fakeWeb}/gl/team/sub/app.git`]);
      Object.assign(state, { head: pushed, status: "failed", readOnly: false });
      await page.goto(`${BASE}/settings/forges`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      const added = await addHost(page, { kind: "gitlab", web_url: `${fakeWeb}/gl`, aliases: alias, token: TOKEN });
      assert(!added.failed, added.failed);
      await page.goto(`${BASE}/settings/forges`, { waitUntil: "domcontentloaded" });
      hostEdit = await page.evaluate((mark) => [...document.querySelectorAll("[data-forge-host]")]
        .find((r) => r.textContent.includes(mark))?.querySelector("[data-forge-edit]")?.getAttribute("href") || "", alias);
      assert(hostEdit, "the added host has no Edit on the list");
    });

    await run("the project row's git menu runs the failed jobs again, its trigger job on its own, and the badge follows without a reload", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      const row = `#project-${project}`;
      await page.waitForFunction((sel) => document.querySelector(`${sel} [data-forge-badge]`)?.dataset.forgePipeline === "failed", row, { timeout: 20000 });
      const button = page.locator(`${row} [data-git-project-menu]`);
      const facts = await button.evaluate((b) => ({ retry: b.dataset.gitRetry, label: b.dataset.gitRetryLabel, branch: b.dataset.gitRetryBranch, head: b.dataset.gitRetryHead, pipeline: b.dataset.gitRetryPipeline }));
      assert(JSON.stringify(facts) === JSON.stringify({ retry: `/projects/${project}/forge/retry`, label: "Retry failed jobs", branch: "feature", head: pushed, pipeline: "12" }), `the git button carries ${JSON.stringify(facts)}`);
      await page.evaluate(() => { window.__forgeRetryMark = 1; });
      await button.click();
      await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
      const labels = await page.$$eval(".dc-context-menu .dropdown-item", (els) => els.map((e) => e.textContent.trim()));
      assert(JSON.stringify(labels) === JSON.stringify(["New worktree", "Fetch", "Commit changes", "Compare", "Retry failed jobs", "Forge"]), `the git menu reads ${JSON.stringify(labels)}`);
      writes();
      asked();
      await page.click('.dc-context-menu .dropdown-item:has-text("Retry failed jobs")');
      await toast(page, "The failed jobs of pipeline #12 run again.");
      const done = writes();
      assert(JSON.stringify(done) === JSON.stringify(["/pipelines/12/retry", "/jobs/32/retry"]), `the forge ran ${JSON.stringify(done)}`);
      const posts = asked().filter((r) => r.method === "POST");
      assert(posts.length === 2 && posts.every((r) => r.auth === `Bearer ${TOKEN}` && r.query === ""), `the writes went as ${JSON.stringify(posts)}`);
      await page.waitForFunction((sel) => document.querySelector(`${sel} [data-forge-badge]`)?.dataset.forgePipeline === "running", row, { timeout: 15000 });
      assert(await page.evaluate(() => window.__forgeRetryMark === 1), "the page reloaded");
      assert(await page.locator(`${row} [data-git-project-menu]`).evaluate((b) => !b.dataset.gitRetry), "a running pipeline still offers its run again");
      assert(!(await page.content()).includes(TOKEN), "the page shows the token");
    });

    await run("the editor's forge sheet offers Retry failed jobs on a failed pipeline, naming what cannot run again, and runs them", async () => {
      await openEditor(page);
      await openForge(page);
      await sheetPipeline(page, "running");
      assert(await retryRow(page) === null, "a running pipeline offers its run again");
      await failedAgain(page);
      await page.waitForFunction(() => !!document.querySelector("[data-editor-sheet-body] [data-forge-retry]"), null, { timeout: 5000 });
      const r = await retryRow(page);
      assert(r.label === "Retry failed jobs" && r.sub === "Pipeline #12 failed · ci/external cannot run again" && !r.disabled && !r.pinned, `the row reads ${JSON.stringify(r)}`);
      assert(r.branch === "feature" && r.head === pushed && r.pipeline === "12" && r.job === "", `the row names ${JSON.stringify(r)}`);
      const order = await page.evaluate(() => [...document.querySelectorAll("[data-editor-sheet-body] [data-forge-summary] .dropdown-item")].map((e) => e.querySelector(":scope > .d-flex > span:first-child")?.textContent.trim()));
      assert(JSON.stringify(order) === JSON.stringify(["Jobs", "Log of unit", "Retry failed jobs"]), `the forge's rows read ${JSON.stringify(order)}`);
      writes();
      await page.click("[data-editor-sheet-body] [data-forge-retry]");
      await toast(page, "The failed jobs of pipeline #12 run again.");
      assert(JSON.stringify(writes()) === JSON.stringify(["/pipelines/12/retry", "/jobs/32/retry"]), "the sheet's row ran something else");
      await sheetPipeline(page, "running", 15000);
      assert(await retryRow(page) === null, "the running pipeline still offers its run again");
    });

    await run("Jobs offers it above the jobs, pinned beside the filter", async () => {
      await failedAgain(page);
      await page.click("[data-editor-sheet-body] [data-forge-jobs]");
      await titled(page, "Pipeline #12 failed");
      const r = await retryRow(page);
      assert(r && r.label === "Retry failed jobs" && r.pinned, `the jobs' row reads ${JSON.stringify(r)}`);
      await page.fill("[data-editor-sheet-filter]", "unit");
      await page.waitForFunction(() => document.querySelector("[data-editor-sheet-filter-count]")?.textContent.trim() === "1 of 4", null, { timeout: 5000 });
      assert(await retryRow(page), "the filter hid the pipeline's run again");
      await page.click("[data-editor-sheet-body] .editor-sheet-open");
      await titled(page, "GitLab");
    });

    await run("a failed job's log offers Retry job, which runs that job again, and follows the job once it runs", async () => {
      await page.click('[data-editor-sheet-body] [data-forge-job="31"]');
      await titled(page, "Log of unit");
      await page.waitForSelector("[data-editor-sheet-body] [data-forge-log-text]", { timeout: 10000 });
      const r = await retryRow(page);
      assert(r && r.label === "Retry job" && r.sub === "unit" && r.job === "31" && r.pipeline === "12" && !r.disabled, `the log's row reads ${JSON.stringify(r)}`);
      writes();
      // The fake runs the job again under its old id, so the log follows it
      // as soon as the poller reads that: the row is read the moment the
      // toast stands, before that read can replace it.
      await page.evaluate(() => {
        window.__retryAtToast = new Promise((resolve) => {
          const seen = new MutationObserver(() => {
            if (![...document.querySelectorAll(".dc-toast")].some((el) => el.textContent.includes('The job "unit" runs again.'))) return;
            seen.disconnect();
            const row = document.querySelector("[data-editor-sheet-body] [data-forge-retry]");
            resolve(row && { disabled: row.disabled, opacity: Number(getComputedStyle(row).opacity) });
          });
          seen.observe(document.body, { childList: true, subtree: true, characterData: true });
        });
      });
      await page.click("[data-editor-sheet-body] [data-forge-retry]");
      await toast(page, 'The job "unit" runs again.');
      const ran = await page.evaluate(() => window.__retryAtToast);
      assert(JSON.stringify(writes()) === JSON.stringify(["/jobs/31/retry"]), "the job's row ran something else");
      assert(ran?.disabled && ran.opacity < 1, `the row of a job that runs again stays live: ${JSON.stringify(ran)}`);
      await page.waitForFunction(() => {
        const view = document.querySelector("[data-editor-sheet-body] [data-forge-log-view]");
        return view?.dataset.forgeJobStatus === "running" && view.dataset.forgeLogLive === "5000";
      }, null, { timeout: 15000 });
      assert(await retryRow(page) === null, "the log of the job running again still offers its run again");
      await page.keyboard.press("Escape");
      await titled(page, "GitLab");
    });

    await run("a click on a pipeline that moved on, or one without the right to write, runs nothing and says why", async () => {
      await failedAgain(page);
      writes();
      const stale = await post(`/projects/${project}/forge/retry`, { branch: "feature", head: pushed, pipeline: "11" });
      assert(stale.status === 409 && stale.json.error === 'The pipeline of "feature" is #12 now, not #11.', `a stale click answered ${stale.status} ${stale.text}`);
      const pushedAgain = await post(`/projects/${project}/forge/retry`, { branch: "feature", head: "0".repeat(40), pipeline: "12" });
      assert(pushedAgain.status === 409 && pushedAgain.json.error === `"feature" was pushed again and stands at ${pushed.slice(0, 12)} now, its pipeline is another one.`, `a click on an old head answered ${pushedAgain.status} ${pushedAgain.text}`);
      state.readOnly = true;
      await page.click("[data-editor-sheet-body] [data-forge-retry]");
      await toast(page, "The token may not write to GitLab: give it the scope api.");
      const r = await retryRow(page);
      assert(r && !r.disabled, "a refused row stays off");
      assert(writes().length === 0, "a refused retry ran something");
      state.readOnly = false;
    });

    await run("an assistant's retry waits for the approval, the dialog names it all, Deny runs nothing", async () => {
      const made = await post("/assistants/new", { form: "new", coder: "claude" });
      assistant = made.json.id || "";
      assert(assistant, `no assistant for the checks: ${made.status}`);
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      writes();
      const answer = await asAssistant(assistant, `/projects/${project}/forge/retry`, {});
      const what = `Retry failed jobs of pipeline #12 on feature in ${project}`;
      assert(answer.status === 200 && answer.json.pending === true && answer.json.what === what, `the socket answered ${answer.status} ${answer.raw}`);
      await page.waitForSelector(`${DIALOG} [data-approval-what]`, { timeout: 15000 });
      assert((await page.textContent(`${DIALOG} [data-approval-what]`)).trim() === what, "the dialog's first line is another");
      const rows = await dialogRows();
      const want = [
        ["Asked by", `Assistant · ${assistant.slice(0, 8)}`],
        ["Action", "Retry failed jobs"],
        ["Failed jobs", "unit, child, ci/external"],
        ["Left as they are", '"ci/external": GitLab cannot run an external commit status again.'],
        ["Pipeline", "#12 · https://gitlab.example.com/team/sub/app/-/pipelines/12"],
        ["Branch", "feature"],
        ["Forge", `GitLab · ${FAKE_ADDR}:${FAKE_PORT}/gl/team/sub/app`],
        ["Project", project],
      ];
      assert(JSON.stringify(rows) === JSON.stringify(want), `the dialog's rows read ${JSON.stringify(rows)}`);
      assert(writes().length === 0, "a waiting retry ran something");
      const again = await asAssistant(assistant, `/projects/${project}/forge/retry`, { branch: "feature" });
      assert(again.status === 409 && again.json.error === `${what} already waits for the user's approval.`, `the same retry again answered ${again.status} ${again.raw}`);
      await page.click(`${DIALOG} .swal2-deny`);
      await page.waitForSelector(DIALOG, { state: "detached", timeout: 8000 });
      const note = await approvalNote("declined", 1);
      assert(note.headline === `Declined: ${what}` && note.text.includes("Not done, declined by the user."), `the note reads ${JSON.stringify(note)}`);
      assert(writes().length === 0, "a denied retry ran something");
    });

    await run("Approve reads the forge again and runs the failed jobs, the note says so", async () => {
      const answer = await asAssistant(assistant, `/projects/${project}/forge/retry`, {});
      assert(answer.json.pending === true, `the retry did not wait: ${answer.raw}`);
      await page.waitForSelector(`${DIALOG} .swal2-confirm`, { timeout: 15000 });
      asked();
      await page.click(`${DIALOG} .swal2-confirm`);
      await page.waitForSelector(DIALOG, { state: "detached", timeout: 8000 });
      const note = await approvalNote("done", 1);
      assert(note.headline === `Approved: Retry failed jobs of pipeline #12 on feature in ${project}`, `the note reads ${note.headline}`);
      assert(note.text.includes(`On GitLab ${FAKE_ADDR}:${FAKE_PORT}/gl/team/sub/app the failed jobs of pipeline #12 of feature in ${project} run again`), `the note says ${note.text}`);
      assert(JSON.stringify(writes()) === JSON.stringify(["/pipelines/12/retry", "/jobs/32/retry"]), "the approval ran something else");
      assert(asked().some((r) => r.method === "GET" && r.path === "/gl/api/v4/projects/team%2Fsub%2Fapp/pipelines"), "the approval did not read the forge again");
    });

    await run("an approval whose branch was pushed again meanwhile fails and runs nothing", async () => {
      state.status = "failed";
      await refresh();
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      const answer = await asAssistant(assistant, `/projects/${project}/forge/retry`, { job: "31" });
      assert(answer.json.pending === true && answer.json.what === `Retry job "unit" of pipeline #12 on feature in ${project}`, `the job's retry answered ${answer.raw}`);
      await page.waitForSelector(`${DIALOG} .swal2-confirm`, { timeout: 15000 });
      const rows = await dialogRows();
      assert(JSON.stringify(rows.slice(1, 3)) === JSON.stringify([["Action", "Retry job"], ["Job", "unit"]]), `the job's dialog reads ${JSON.stringify(rows)}`);
      await git(["commit", "-q", "--allow-empty", "-m", "pushed again"]);
      const next = await git(["rev-parse", "HEAD"]);
      await git(["update-ref", "refs/remotes/origin/feature", next]);
      await page.click(`${DIALOG} .swal2-confirm`);
      await page.waitForSelector(DIALOG, { state: "detached", timeout: 8000 });
      const note = await approvalNote("failed", 1);
      assert(note.text.includes(`"feature" was pushed again and stands at ${next.slice(0, 12)} now, its pipeline is another one.`), `the note says ${note.text}`);
      assert(writes().length === 0, "the approval of a branch pushed again ran something");
      await git(["update-ref", "refs/remotes/origin/feature", pushed]);
      await git(["reset", "-q", "--hard", pushed]);
    });

    await run("a forge's refusal reads its control characters as escapes, in the answer to a click and in an approval's note", async () => {
      state.status = "failed";
      await refresh();
      writes();
      state.refusal = "\u001b]0;owned title\u0007\u001b[2J\u001b[1;31mFAKE PROMPT $ \u001b[0m\rline2\nline3";
      const said = String.raw`GitLab refused to retry pipeline #12: \x1b]0;owned title\a\x1b[2J\x1b[1;31mFAKE PROMPT $ \x1b[0m\rline2\nline3.`;
      try {
        const click = await post(`/projects/${project}/forge/retry`, { branch: "feature", head: pushed, pipeline: "12" });
        assert(click.status === 502 && click.json.error === said, `a refused click answered ${click.status} ${click.text}`);
        await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
        const answer = await asAssistant(assistant, `/projects/${project}/forge/retry`, {});
        assert(answer.json.pending === true, `the retry did not wait: ${answer.raw}`);
        await page.waitForSelector(`${DIALOG} .swal2-confirm`, { timeout: 15000 });
        await page.click(`${DIALOG} .swal2-confirm`);
        await page.waitForSelector(DIALOG, { state: "detached", timeout: 8000 });
        const note = await approvalNote("failed", 2);
        assert(note.text.includes(`Approved, and it failed: ${said}`) && !/[\u0000-\u0008\u000b-\u001f\u007f]/.test(note.text), `the note says ${JSON.stringify(note.text)}`);
      } finally {
        state.refusal = "";
      }
      assert(writes().length === 0, "a refused retry ran something");
    });

    await run("the Approvals tab lists the Pipeline retry approval, off runs an assistant's retry at once, the socket cannot move it", async () => {
      await page.goto(`${BASE}/settings/assistant/approvals`, { waitUntil: "domcontentloaded" });
      const kinds = await page.locator("[data-assistant-approval]").evaluateAll((rows) => rows.map((row) => row.dataset.assistantApproval));
      assert(JSON.stringify(kinds) === JSON.stringify(["compose-actions", "project-delete", "coder-delete", "assistant-delete", "worktree-post-script", "forge-retry", "forge-change", "forge-publish", "forge-thread"]), `the tab lists ${JSON.stringify(kinds)}`);
      const label = (await page.locator('[data-assistant-approval="forge-retry"]').innerText()).replace(/\s+/g, " ").trim();
      assert(label === "Pipeline retry approval Failed jobs an assistant wants to run again on a forge stay as they are until you approve.", `the switch reads ${label}`);
      const refused = await asAssistant(assistant, "/settings/assistant/approvals", { "approval-forge-retry": "0" });
      assert(refused.status === 403, `the socket moved the switch: ${refused.status}`);
      const off = await post("/settings/assistant/approvals", { "approval-forge-retry": "0" });
      assert(off.status === 200, `switching it off answered ${off.status}`);
      state.status = "failed";
      await refresh();
      writes();
      const answer = await asAssistant(assistant, `/projects/${project}/forge/retry`, { branch: "feature" });
      assert(answer.status === 200 && !answer.json.pending && answer.json.retried?.startsWith(`On GitLab ${FAKE_ADDR}:${FAKE_PORT}/gl/team/sub/app the failed jobs of pipeline #12 of feature in ${project} run again`), `with the switch off the socket answered ${answer.raw}`);
      assert(JSON.stringify(writes()) === JSON.stringify(["/pipelines/12/retry", "/jobs/32/retry"]), "the retry with the switch off ran something else");
      const on = await post("/settings/assistant/approvals", { "approval-forge-retry": "1" });
      assert(on.status === 200, `switching it on answered ${on.status}`);
    });

    await run("GitHub: Re-run failed jobs re-runs each failed workflow run, a check run of another app and a commit status cannot run again", async () => {
      Object.assign(state, { ghRun: "failure", ghTest: "failure", ghStatus: "failure" });
      const saved = await post(hostEdit, { form: "save", kind: "github", web_url: fakeWeb, aliases: alias });
      assert(saved.status === 200, `turning the host into GitHub answered ${saved.status}`);
      await git(["remote", "set-url", "origin", `${fakeWeb}/octo/app.git`]);
      await openEditor(page);
      await refresh();
      await openForge(page, "GitHub");
      await sheetPipeline(page, "failed");
      await page.waitForFunction(() => !!document.querySelector("[data-editor-sheet-body] [data-forge-retry]"), null, { timeout: 5000 });
      const r = await retryRow(page);
      assert(r.label === "Re-run failed jobs" && r.sub === "Pipeline failed · scan, ci/jenkins cannot run again" && r.pipeline === "" && r.head === pushed && !r.disabled, `the row reads ${JSON.stringify(r)}`);
      writes();
      asked();
      await page.click("[data-editor-sheet-body] [data-forge-retry]");
      await toast(page, `The failed jobs of the pipeline at ${pushed.slice(0, 12)} run again.`);
      assert(JSON.stringify(writes()) === JSON.stringify(["/actions/runs/501/rerun-failed-jobs"]), "the re-run ran something else");
      const runs = asked().find((r) => r.path === "/api/v3/repos/octo/app/actions/runs");
      assert(runs && runs.query.includes(`head_sha=${pushed}`) && runs.auth === `Bearer ${TOKEN}`, `the runs were read as ${JSON.stringify(runs)}`);
      await sheetPipeline(page, "running", 15000);
    });

    await run("GitHub: a failed job's log offers Re-run job, and with only other apps failed the row stands off with the reason", async () => {
      state.ghTest = "failure";
      await refresh();
      await sheetPipeline(page, "failed");
      await page.click('[data-editor-sheet-body] [data-forge-job="6"]');
      await titled(page, "Log of test");
      await page.waitForSelector("[data-editor-sheet-body] [data-forge-log-text]", { timeout: 10000 });
      const r = await retryRow(page);
      assert(r && r.label === "Re-run job" && r.sub === "test" && r.job === "6", `the log's row reads ${JSON.stringify(r)}`);
      writes();
      await page.click("[data-editor-sheet-body] [data-forge-retry]");
      await toast(page, 'The job "test" runs again.');
      assert(JSON.stringify(writes()) === JSON.stringify(["/actions/jobs/6/rerun"]), "the job's re-run ran something else");
      const scan = await post(`/projects/${project}/forge/retry`, { job: "7" });
      assert(scan.status === 409 && scan.json.error === 'The check run comes from "CodeQL", only that app runs it again.', `a re-run of CodeQL answered ${scan.status} ${scan.text}`);
      await page.keyboard.press("Escape");
      await titled(page, "GitHub");
      state.ghTest = "success";
      await refresh();
      await page.waitForFunction(() => document.querySelector("[data-editor-sheet-body] [data-forge-retry]")?.disabled === true, null, { timeout: 15000 });
      const off = await retryRow(page);
      assert(off.title === 'No job that failed the pipeline can run again. "scan": The check run comes from "CodeQL", only that app runs it again. "ci/jenkins": GitHub cannot run a commit status again.' && off.sub === off.title, `the row off reads ${JSON.stringify(off)}`);
      assert(off.opacity < 1, `the row off does not look off: ${JSON.stringify(off)}`);
      await page.click("[data-editor-sheet-body] [data-forge-retry]", { force: true });
      await sleep(500);
      assert(writes().length === 0, "a row off ran something");
    });

    await run("phone: the forge sheet's and the log's retry rows stand within 390px", async () => {
      Object.assign(state, { ghTest: "failure" });
      await refresh();
      const phone = await mobilePage();
      await openEditor(phone);
      await openForge(phone, "GitHub");
      await sheetPipeline(phone, "failed");
      const inside = async (what) => {
        const r = await retryRow(phone);
        const scroll = await phone.evaluate(() => ({ page: document.documentElement.scrollWidth, body: document.querySelector("[data-editor-sheet-body]").scrollWidth - document.querySelector("[data-editor-sheet-body]").clientWidth }));
        assert(r && r.left >= 0 && r.right <= r.inner && scroll.page <= r.inner && scroll.body <= 0, `${what}: the row stands at ${JSON.stringify(r)}, scrolls ${JSON.stringify(scroll)}`);
      };
      await inside("the forge sheet");
      await phone.tap('[data-editor-sheet-body] [data-forge-job="6"]');
      await phone.waitForSelector("[data-editor-sheet-body] [data-forge-log-text]", { timeout: 10000 });
      await inside("the log");
      assert(await shown(phone, "[data-editor-sheet-body] [data-forge-retry]") === 1, "the phone's log shows no single retry row");
    });

    await run("a guest of a shared link sees no retry and reaches none", async () => {
      await page.goto(`${BASE}/settings/sharing`, { waitUntil: "domcontentloaded" });
      const made = await post("/settings/sharing/new", [["amount", "1"], ["unit", "hours"], ["name", linkName], ["project", project]]);
      const link = (made.text.match(/value="([^"]*\/share\/[^"]+)"/) || [])[1];
      assert(link, `making a link answered ${made.status}`);
      await page.goto(`${BASE}/settings/sharing`, { waitUntil: "domcontentloaded" });
      linkId = await page.evaluate((name) => [...document.querySelectorAll("[data-share-row]")]
        .find((r) => r.querySelector("[data-share-name]")?.textContent.trim() === name)?.dataset.shareRow || "", linkName);
      const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1360, height: 900 } });
      try {
        const guest = await ctx.newPage();
        L.wirePage(guest, bag);
        const base = new URL(link).pathname;
        await guest.goto(`${new URL(link).origin}${base}/projects/${project}/editor`, { waitUntil: "domcontentloaded" });
        await guest.waitForSelector("[data-editor-git-status]:not([hidden])", { timeout: 20000 });
        await openGit(guest);
        await sleep(1500);
        assert(await guest.locator("[data-forge-retry]").count() === 0, "the guest's sheet offers a retry");
        writes();
        const code = await guest.evaluate(async (target) => (await fetch(target, { method: "POST", headers: { "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]')?.content || "", "Content-Type": "application/x-www-form-urlencoded" }, body: "job=6" })).status, `${base}/projects/${project}/forge/retry`);
        assert(code === 404, `a guest's retry answered ${code}`);
        assert(writes().length === 0, "a guest's retry ran something");
      } finally {
        await ctx.close();
      }
    });

    await run("docs: the Forges topic explains the run of failed jobs again, Approvals names it", async () => {
      await page.goto(`${BASE}/docs`, { waitUntil: "domcontentloaded" });
      const item = page.locator("section#forges .list-group-item", { hasText: /^\s*Run failed jobs again/ });
      assert(await item.count() === 1, "no docs entry for the run of failed jobs again");
      const text = await item.innerText();
      for (const said of ["Retry failed jobs", "Re-run failed jobs", "git menu of the project's row", "Retry job", "The forge is read again first", "A commit status and a check run of another app cannot run again", "only once you approve"]) {
        assert(text.includes(said), `the entry lacks "${said}": ${text}`);
      }
      const tokens = await page.locator("section#forges .list-group-item", { hasText: "Tokens and the connection test" }).innerText();
      assert(tokens.includes("writing api") && tokens.includes("Actions and Pull requests read and write"), `the tokens' entry reads ${tokens}`);
      const approvals = await page.locator("section#assistants .list-group-item", { hasText: /^\s*Approvals/ }).innerText();
      assert(approvals.includes("a run of failed jobs again") && approvals.includes("the pipeline, change request, comments or thread"), `the approvals' entry reads ${approvals}`);
    });
  } finally {
    if (linkId) await post(`/settings/sharing/${linkId}`, { form: "revoke" }).catch(() => {});
    await post("/settings/assistant/approvals", { "approval-forge-retry": "1" }).catch(() => {});
    if (hostEdit) await post(hostEdit, { form: "delete" }).catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
    if (assistant) await post(`/assistants/${assistant}`, { form: "delete" }).catch(() => {});
  }
});
