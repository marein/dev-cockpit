const L = require("./lib");
const { assert, BASE } = L;

// Forge editor: the editor's forge sheet shows what the forge poller read of
// the branch HEAD is on (GET /projects/:name/forge/branch, forge_branch.gohtml),
// opened from its button in the statusbar, Forge in the editor menu and
// Ctrl+Shift+E, the git sheet keeping no row of it:
// the project row's badge (forge_badge.gohtml, the one template of the
// status, a running pipeline's or job's icon the cockpit's spinner) with the
// change request's title, a Jobs row that drills into the pipeline's jobs,
// and a row per job that failed the pipeline and has a log,
// which drills into the end of that log (GET /projects/:name/forge/log,
// forge_log.gohtml), the one read here that asks the forge. The log of a
// running job on GitLab is read again every 5 seconds until the job ends or
// the log closes, on GitHub it says the log comes once the job ended and
// shows it then. The sheet follows
// the SSE signal forge without a reload, a branch switch and a push to an
// upstream of another name too, and a guest of a shared link sees none of it.
// The forge is the runner's own fake (forge-fake.js) on FORGE_PORT (default
// 3157), published on the host's loopback, where the instance reaches it at
// FORGE_ADDR (default 127.0.0.1); its pipeline stands where state.status
// says. The repository is made with git through the cockpit's own proxy
// (POST /git), a push is an update-ref of the tracking ref. The host this
// runner adds carries its stamp in an alias and goes at the end with its
// project and its link. A run waits for poll intervals of a running pipeline
// and for the live log's reads, about two and a half minutes per engine.

const { startFakeForge, addHost } = require("./forge-fake");

const FAKE_PORT = Number(process.env.FORGE_PORT || 3157);
const FAKE_TLS_PORT = Number(process.env.FORGE_TLS_PORT || 3158);
const FAKE_ADDR = process.env.FORGE_ADDR || "127.0.0.1";
const TOKEN = `glpat-e2e${Date.now().toString(36)}Ed8wR2sT9uV4`;
let fakeForge = null;

L.runFeature("FORGE_EDITOR", async ({ browser, page, run, mobilePage, bag }) => {
  const stamp = Date.now().toString(36);
  const project = `zzforgeeditor-${stamp}`;
  const alias = `fe-${stamp}`;
  const linkName = `forge editor ${stamp}`;
  const fakeWeb = `http://${FAKE_ADDR}:${FAKE_PORT}`;
  const web = "https://gitlab.example.com/team/sub/app";
  const glProject = "/gl/api/v4/projects/team%2Fsub%2Fapp";
  const editor = `${BASE}/projects/${project}/editor`;
  fakeForge ||= await startFakeForge({ port: FAKE_PORT, tlsPort: FAKE_TLS_PORT, token: TOKEN });
  let path = "";
  let hostEdit = "";
  let pushed = "";

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

  const post = (target, fields) => page.evaluate(async ({ target, fields }) => {
    const body = new URLSearchParams([...fields, ["csrf_token", document.querySelector('meta[name="csrf-token"]').content]]);
    const r = await fetch(target, { method: "POST", body });
    return { status: r.status, url: r.url, text: await r.text() };
  }, { target, fields: Array.isArray(fields) ? fields : Object.entries(fields) });

  const asked = () => fakeForge.log.splice(0);
  const traces = () => asked().filter((r) => r.path === `${glProject}/jobs/31/trace`).length;
  const refresh = () => page.evaluate(async (p) => (await fetch(`/projects/${p}/forge/status?fresh=1`, { headers: { Accept: "application/json" } })).status, project);

  const sheet = (p) => p.evaluate(() => {
    const shown = (el) => {
      if (!el) return false;
      const box = el.getBoundingClientRect();
      const style = getComputedStyle(el);
      return box.width > 0 && box.height > 0 && style.visibility !== "hidden" && style.display !== "none";
    };
    const body = document.querySelector("[data-editor-sheet-body]");
    const head = body.querySelector("[data-forge-head]");
    const badge = head?.querySelector("[data-forge-badge]");
    const link = (a) => a && { text: a.textContent.trim(), href: a.getAttribute("href") || "", target: a.getAttribute("target") || "" };
    const rows = [...body.querySelectorAll(".dropdown-item, .editor-sheet-open")].filter(shown).map((row) => ({
      text: (row.querySelector(":scope > .d-flex > span:first-child") || row).textContent.trim(),
      sub: row.querySelector(":scope > .d-flex > .small")?.textContent.trim() || "",
      job: row.dataset.forgeJob || "",
      jobs: row.hasAttribute("data-forge-jobs"),
      href: row.getAttribute("href") || "",
      target: row.getAttribute("target") || "",
    }));
    return {
      title: document.querySelector("[data-editor-sheet-title]")?.textContent.trim() || "",
      head: shown(head) ? {
        title: head.querySelector("[data-forge-title]")?.textContent.trim() || "",
        problem: head.querySelector("[data-forge-problem-text]")?.textContent.trim() || "",
        badge: badge && {
          html: badge.outerHTML,
          pipeline: badge.dataset.forgePipeline || "",
          change: link(badge.querySelector("[data-forge-change]")),
          status: link(badge.querySelector("[data-forge-status]")),
        },
      } : null,
      rows,
    };
  });
  const rowTexts = (state) => state.rows.map((r) => r.text);
  const logView = (p) => p.evaluate(() => {
    const view = document.querySelector("[data-editor-sheet-body] [data-forge-log-view]");
    if (!view) return null;
    const pre = view.querySelector("[data-forge-log-text]");
    const later = view.querySelector("[data-forge-log-later]");
    const box = later?.getBoundingClientRect();
    return {
      status: view.dataset.forgeJobStatus || "",
      live: view.dataset.forgeLogLive || "",
      head: view.querySelector("[data-forge-log-head]")?.textContent.trim() || "",
      later: later && box.width > 0 && box.height > 0 && getComputedStyle(later).visibility !== "hidden" ? later.textContent.trim() : "",
      text: pre?.textContent || "",
      end: !!pre && pre.scrollTop + pre.clientHeight >= pre.scrollHeight - 1,
      top: pre ? pre.scrollTop : -1,
      overflows: !!pre && pre.scrollHeight > pre.clientHeight + 1,
      retry: !!view.querySelector("[data-forge-retry]"),
      fills: (() => {
        if (!pre) return null;
        const body = document.querySelector("[data-editor-sheet-body]");
        const b = body.getBoundingClientRect();
        const back = body.querySelector(".editor-sheet-open").getBoundingClientRect();
        const bottom = pre.getBoundingClientRect().bottom;
        return {
          ok: body.scrollHeight <= body.clientHeight + 1 && body.scrollTop === 0 && Math.abs(b.bottom - parseFloat(getComputedStyle(body).paddingBottom) - bottom) <= 2 && back.top >= b.top - 1,
          body: [Math.round(b.top), Math.round(b.bottom), body.scrollHeight, body.clientHeight, body.scrollTop], pre: Math.round(bottom), back: Math.round(back.top),
        };
      })(),
    };
  });
  const logHas = (p, text, timeout) => p.waitForFunction((text) => document.querySelector("[data-editor-sheet-body] [data-forge-log-text]")?.textContent.includes(text), text, { timeout });
  const openLog = async (p, job, title, level) => {
    await p.click("[data-editor-sheet-body] [data-forge-jobs]");
    await titled(p, level);
    await p.click(`[data-editor-sheet-body] [data-forge-job="${job}"]`);
    await titled(p, title);
  };
  const sheetPipeline = (p, want, timeout) => p.waitForFunction((want) => {
    const head = document.querySelector("[data-editor-sheet-body] [data-forge-head]");
    if (want === "") return !head;
    return head?.querySelector("[data-forge-badge]")?.dataset.forgePipeline === want;
  }, want, { timeout });
  const openEditor = async (p) => {
    await p.goto(editor, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(p);
    await p.waitForSelector("[data-editor-git-status]:not([hidden])", { timeout: 20000 });
    const backdrop = p.locator("[data-editor-backdrop]");
    if (await backdrop.isVisible()) {
      await backdrop.click({ position: { x: 370, y: 300 } });
      await p.waitForSelector("[data-editor-backdrop]", { state: "hidden", timeout: 5000 });
    }
    await p.evaluate(() => { window.__forgeEditorMark = 1; });
  };
  const openGit = async (p) => {
    await p.click("[data-editor-git-status]");
    await p.waitForFunction(() => document.querySelector("[data-editor-sheet-title]")?.textContent.trim() === "Git"
      && !document.querySelector("[data-editor-sheet]").hidden, null, { timeout: 8000 });
  };
  const openForge = async (p, title = "GitLab") => {
    await p.waitForSelector("[data-editor-forge-status]:not([hidden])", { timeout: 20000 });
    await p.click("[data-editor-forge-status]");
    await p.waitForFunction((title) => document.querySelector("[data-editor-sheet-title]")?.textContent.trim() === title
      && !document.querySelector("[data-editor-sheet]").hidden, title, { timeout: 8000 });
  };
  const emptied = async (p) => {
    await p.waitForSelector("[data-editor-sheet-body] [data-forge-empty]", { timeout: 15000 });
    return (await p.textContent("[data-editor-sheet-body] [data-forge-empty]")).trim();
  };
  const sheetClosed = (p) => p.waitForFunction(() => document.querySelector("[data-editor-sheet]").hidden, null, { timeout: 8000 });
  const forgeButton = (p) => p.evaluate(() => {
    const btn = document.querySelector("[data-editor-forge-status]");
    const item = document.querySelector("[data-editor-forge-item]");
    const box = btn?.getBoundingClientRect();
    return {
      shown: !!btn && box.width > 0 && box.height > 0 && getComputedStyle(btn).visibility !== "hidden",
      item: !!item && !item.hidden,
      title: btn?.title || "",
      icon: !!btn?.querySelector(":scope > i.ti-brand-gitlab, :scope > i.ti-brand-github"),
      ref: btn?.querySelector(":scope > span")?.textContent.trim() || "",
      pipeline: btn?.querySelector("[data-forge-bar-pipeline]")?.dataset.forgeBarPipeline || "",
      left: box?.left ?? -1,
      right: box?.right ?? -1,
      inner: window.innerWidth,
    };
  });
  const titled = (p, title) => p.waitForFunction((title) => document.querySelector("[data-editor-sheet-title]")?.textContent.trim() === title, title, { timeout: 10000 });
  const notReloaded = async (p) => {
    assert(await p.evaluate(() => window.__forgeEditorMark === 1), "the editor reloaded");
  };
  const rowBadge = async () => {
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    return page.evaluate((sel) => document.querySelector(`${sel} [data-project-meta] [data-forge-badge]`)?.outerHTML || "", `#project-${project}`);
  };

  try {
    await run("setup: a repository on the fake GitLab whose pushed branch feature runs a pipeline", async () => {
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
      fakeForge.state.head = pushed;
      fakeForge.state.status = "running";
      await page.goto(`${BASE}/settings/forges`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      const added = await addHost(page, { kind: "gitlab", web_url: `${fakeWeb}/gl`, aliases: alias, token: TOKEN });
      assert(!added.failed, added.failed);
      await page.goto(`${BASE}/settings/forges`, { waitUntil: "domcontentloaded" });
      hostEdit = await page.evaluate((mark) => [...document.querySelectorAll("[data-forge-host]")]
        .find((r) => r.textContent.includes(mark))?.querySelector("[data-forge-edit]")?.getAttribute("href") || "", alias);
      assert(hostEdit, "the added host has no Edit on the list");
    });

    await run("the forge sheet opens from the statusbar with the row's very badge, its running pipeline a spinner, the title and the jobs, no log while none failed with one", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await page.waitForFunction((sel) => document.querySelector(`${sel} [data-forge-badge]`)?.dataset.forgePipeline === "running", `#project-${project}`, { timeout: 20000 });
      const row = await rowBadge();
      await openEditor(page);
      await page.waitForSelector("[data-editor-forge-status]:not([hidden])", { timeout: 20000 });
      const button = await forgeButton(page);
      assert(button.shown && button.item && button.icon && button.ref === "!4" && button.pipeline === "running" && button.title === "GitLab: !4 draft · Pipeline #12 running", `the statusbar's forge button reads ${JSON.stringify(button)}`);
      const barSpin = await page.locator("[data-editor-forge-status] [data-forge-bar-pipeline]").evaluate(L.iconSpin);
      assert(L.spinning(barSpin), `the running pipeline's icon in the statusbar is no turning spinner: ${JSON.stringify(barSpin)}`);
      await openForge(page);
      await sheetPipeline(page, "running", 10000);
      const got = await sheet(page);
      const b = got.head.badge;
      assert(b.html === row, `the sheet's badge is not the row's:\n${b.html}\n${row}`);
      assert(b.change?.text === "!4" && b.change.href === `${web}/-/merge_requests/4` && b.change.target === "_blank", `the change reads ${JSON.stringify(b.change)}`);
      assert(b.status?.text === "running" && b.status.href === `${web}/-/pipelines/12`, `the pipeline reads ${JSON.stringify(b.status)}`);
      const spin = await page.locator("[data-editor-sheet-body] [data-forge-head] [data-forge-status] i").evaluate(L.iconSpin);
      assert(L.spinning(spin), `the running pipeline's icon in the sheet is no turning spinner: ${JSON.stringify(spin)}`);
      assert(got.head.title === "Fix the login" && got.head.problem === "", `the head reads ${JSON.stringify(got.head)}`);
      const jobs = got.rows.find((r) => r.jobs);
      assert(jobs && jobs.text === "Jobs" && jobs.sub === "4 jobs, 2 failed, 1 running, 1 success", `the jobs row reads ${JSON.stringify(jobs)}`);
      assert(!rowTexts(got).some((t) => t.startsWith("Log of")), `a log is offered of a job that has none: ${rowTexts(got)}`);
      assert(!rowTexts(got).includes("Switch branch"), `the forge sheet carries git's actions: ${rowTexts(got)}`);
      assert(!(await page.content()).includes(TOKEN), "the page shows the token");
    });

    await run("the git sheet holds no row of the forge, its button, Forge in the menu and Ctrl+Shift+E open the forge sheet", async () => {
      await page.keyboard.press("Escape");
      await sheetClosed(page);
      await openGit(page);
      let got = await sheet(page);
      assert(got.rows[0]?.text === "Switch branch", `the git sheet's first row reads ${JSON.stringify(got.rows[0])}`);
      assert(got.head === null && !got.rows.some((r) => r.jobs || r.job || r.text === "GitLab" || r.text.startsWith("Retry") || r.text.startsWith("New merge")), `the git sheet reads ${rowTexts(got)}`);
      assert(await page.locator("[data-editor-sheet-body] .ti-brand-gitlab").count() === 0, "the git sheet carries the forge's brand");
      await page.keyboard.press("Escape");
      await sheetClosed(page);
      await openForge(page);
      await sheetPipeline(page, "running", 5000);
      await page.keyboard.press("Escape");
      await sheetClosed(page);
      await page.click("[data-editor-menu]");
      await page.click("[data-editor-forge-item]");
      await titled(page, "GitLab");
      await sheetPipeline(page, "running", 5000);
      await page.keyboard.press("Control+Shift+E");
      await sheetClosed(page);
      await page.keyboard.press("Control+Shift+E");
      await titled(page, "GitLab");
      await sheetPipeline(page, "running", 5000);
      got = await sheet(page);
      assert(got.rows.some((r) => r.jobs), `Ctrl+Shift+E opened ${rowTexts(got)}`);
      await notReloaded(page);
    });

    await run("Jobs of the running pipeline turn the running job's icon into the spinner, the others stand still", async () => {
      await page.click("[data-editor-sheet-body] [data-forge-jobs]");
      await titled(page, "Pipeline #12 running");
      const icon = (job) => page.locator(`[data-editor-sheet-body] [data-forge-job="${job}"] i`).evaluate(L.iconSpin);
      const unit = await icon("31");
      assert(L.spinning(unit), `the running job's icon is no turning spinner: ${JSON.stringify(unit)}`);
      const build = await icon("30");
      assert(L.still(build), `the job that passed spins: ${JSON.stringify(build)}`);
      await page.keyboard.press("Escape");
      await titled(page, "GitLab");
      await sheetPipeline(page, "running", 5000);
      await notReloaded(page);
    });

    await run("the open sheet goes from running to failed without a reload and offers the failed job's log", async () => {
      fakeForge.state.status = "failed";
      await sheetPipeline(page, "failed", 30000);
      await page.waitForFunction(() => [...document.querySelectorAll("[data-editor-sheet-body] [data-forge-job]")].some((r) => r.textContent.includes("Log of unit")), null, { timeout: 5000 });
      const got = await sheet(page);
      assert(got.title === "GitLab" && got.head.badge.status.text === "failed", `the sheet reads ${JSON.stringify(got.head)}`);
      const stood = await page.locator("[data-editor-sheet-body] [data-forge-head] [data-forge-status] i").evaluate(L.iconSpin);
      assert(L.still(stood), `the failed pipeline's icon in the sheet still spins: ${JSON.stringify(stood)}`);
      assert(got.rows.find((r) => r.jobs)?.sub === "4 jobs, 3 failed, 1 success", `the jobs row reads ${JSON.stringify(got.rows.find((r) => r.jobs))}`);
      const log = got.rows.find((r) => r.text === "Log of unit");
      assert(log && log.job === "31" && log.sub === "test · failed", `the log row reads ${JSON.stringify(log)}`);
      assert(got.rows.filter((r) => r.text.startsWith("Log of")).length === 1, `logs offered: ${rowTexts(got)}`);
      await notReloaded(page);
    });

    await run("Jobs lists the pipeline on the forge and every job, filters them, Back and Escape lead to the forge sheet", async () => {
      await page.click("[data-editor-sheet-body] [data-forge-jobs]");
      await titled(page, "Pipeline #12 failed");
      let got = await sheet(page);
      assert(got.head === null, "the jobs level shows the forge's head");
      const by = Object.fromEntries(got.rows.map((r) => [r.text, r]));
      assert(by.Back && by["Open on GitLab"]?.href === `${web}/-/pipelines/12` && by["Open on GitLab"].target === "_blank", `the level's top reads ${JSON.stringify(got.rows.slice(0, 2))}`);
      assert(by.build?.sub === "build · success" && by.build.job === "30", `build reads ${JSON.stringify(by.build)}`);
      assert(by.unit?.sub === "test · failed" && by.unit.job === "31", `unit reads ${JSON.stringify(by.unit)}`);
      assert(by.child?.href === `${web}/-/pipelines/13` && by.child.target === "_blank" && by.child.sub === "deploy · failed · no log, opens on GitLab" && !by.child.job, `child reads ${JSON.stringify(by.child)}`);
      assert(by["ci/external"]?.href === "https://ci.example.com/33" && by["ci/external"].sub === "failed · no log, opens on GitLab", `ci/external reads ${JSON.stringify(by["ci/external"])}`);
      await page.fill("[data-editor-sheet-filter]", "unit");
      await page.waitForFunction(() => document.querySelector("[data-editor-sheet-filter-count]")?.textContent.trim() === "1 of 4", null, { timeout: 5000 });
      got = await sheet(page);
      assert(rowTexts(got).join(",") === "Back,Open on GitLab,Retry failed jobs,unit", `filtered on unit the level shows ${rowTexts(got)}`);
      await page.click("[data-editor-sheet-body] .editor-sheet-open");
      await titled(page, "GitLab");
      assert((await sheet(page)).head?.badge?.pipeline === "failed", "Back lost the forge's head");
      await page.click("[data-editor-sheet-body] [data-forge-jobs]");
      await titled(page, "Pipeline #12 failed");
      await page.keyboard.press("Escape");
      await titled(page, "GitLab");
      await notReloaded(page);
    });

    await run("the failed job's log is read from the forge at the tap, as plain text, scrolled to its end", async () => {
      asked();
      await page.click('[data-editor-sheet-body] [data-forge-job="31"]');
      await titled(page, "Log of unit");
      await page.waitForSelector("[data-editor-sheet-body] [data-forge-log-text]", { timeout: 10000 });
      const log = await page.evaluate(() => {
        const pre = document.querySelector("[data-editor-sheet-body] [data-forge-log-text]");
        return {
          text: pre.textContent,
          end: pre.scrollTop + pre.clientHeight >= pre.scrollHeight - 1,
          head: document.querySelector("[data-editor-sheet-body] [data-forge-log-head]")?.textContent.trim(),
        };
      });
      log.open = (await sheet(page)).rows.find((r) => r.text === "Open on GitLab");
      assert(log.text.includes("$ go test ./...") && log.text.includes("--- FAIL: TestLogin (0.00s)") && log.text.endsWith("ERROR: Job failed: exit code 1"), `the log reads ${JSON.stringify(log.text)}`);
      assert(!/\x1b|section_start|\r/.test(log.text) && log.text.includes("downloading 100%") && !log.text.includes("downloading 10%"), `the log keeps control sequences: ${JSON.stringify(log.text)}`);
      assert(log.end, "the log does not stand at its end");
      assert(log.head === "The whole log, 6 lines.", `the log's head reads ${log.head}`);
      assert(log.open?.href === `${web}/-/jobs/31` && log.open.target === "_blank" && log.open.sub === "unit · test · failed", `the job's page reads ${JSON.stringify(log.open)}`);
      const traces = asked().filter((r) => r.path === `${glProject}/jobs/31/trace`);
      assert(traces.length === 1 && traces[0].auth === `Bearer ${TOKEN}`, `the trace was asked ${traces.length} times`);
      await page.click("[data-editor-sheet-body] .editor-sheet-open");
      await titled(page, "GitLab");
      await notReloaded(page);
    });

    await run("a job's log from the jobs leads back to the jobs at its row", async () => {
      await page.click("[data-editor-sheet-body] [data-forge-jobs]");
      await titled(page, "Pipeline #12 failed");
      await page.click('[data-editor-sheet-body] [data-forge-job="30"]');
      await titled(page, "Log of build");
      await page.waitForSelector("[data-editor-sheet-body] [data-forge-log-error]", { timeout: 10000 });
      const said = (await page.textContent("[data-editor-sheet-body] [data-forge-log-error]")).trim();
      assert(said.startsWith('GitLab has no log of "build" in '), `a job whose trace the forge lacks reads ${said}`);
      await page.keyboard.press("Escape");
      await titled(page, "Pipeline #12 failed");
      const focused = await page.evaluate(() => document.activeElement?.dataset.forgeJob || "");
      assert(focused === "30", `back at the jobs the focus stands on ${JSON.stringify(focused)}`);
      await page.keyboard.press("Escape");
      await titled(page, "GitLab");
    });

    await run("a job's log from the jobs leads back to the forge sheet saying it holds nothing once the pipeline left the cache, the forge's button gone", async () => {
      const leaves = async (args) => {
        const read = page.waitForResponse(async (r) => r.url().includes(`/projects/${project}/forge/branch`)
          && !(await r.text()).includes("data-forge-jobs-level"), { timeout: 15000 });
        await git(args);
        await read;
        await L.sleep(500);
      };
      for (const [args, back] of [[["switch", "-q", "main"], "Back"], [["switch", "-q", "--detach"], "Escape"]]) {
        await page.click("[data-editor-sheet-body] [data-forge-jobs]");
        await titled(page, "Pipeline #12 failed");
        await page.click('[data-editor-sheet-body] [data-forge-job="31"]');
        await titled(page, "Log of unit");
        await page.waitForSelector("[data-editor-sheet-body] [data-forge-log-text]", { timeout: 10000 });
        await leaves(args);
        if (back === "Back") await page.click("[data-editor-sheet-body] .editor-sheet-open");
        else await page.keyboard.press("Escape");
        await titled(page, "GitLab");
        const said = await emptied(page);
        const got = await sheet(page);
        assert(said === "No change request or pipeline for this branch." && got.head === null && !got.rows.some((r) => r.jobs || r.job), `${back} after ${args.join(" ")} leads to ${said} ${rowTexts(got)}`);
        const gone = await forgeButton(page);
        assert(!gone.shown && !gone.item, `${back} after ${args.join(" ")} leaves the forge's button ${JSON.stringify(gone)}`);
        await git(["switch", "-q", "feature"]);
        await sheetPipeline(page, "failed", 15000);
      }
      await notReloaded(page);
    });

    await run("the forge sheet and its jobs ask the forge nothing, also after a reload", async () => {
      asked();
      await page.keyboard.press("Escape");
      await openEditor(page);
      await openForge(page);
      await sheetPipeline(page, "failed", 10000);
      await page.click("[data-editor-sheet-body] [data-forge-jobs]");
      await titled(page, "Pipeline #12 failed");
      await page.keyboard.press("Escape");
      await titled(page, "GitLab");
      await L.sleep(1500);
      const log = asked();
      assert(log.length === 0, `the sheet asked the forge for ${JSON.stringify(log.map((r) => r.path))}`);
    });

    await run("the log of a running job follows it without a reload, at its end or where it was scrolled to, the focus kept, until the job ended, filling the sheet as its one scroller", async () => {
      fakeForge.state.status = "running";
      await refresh();
      await sheetPipeline(page, "running", 15000);
      try {
        await openLog(page, "31", "Log of unit", "Pipeline #12 running");
        await page.waitForSelector("[data-editor-sheet-body] [data-forge-log-text]", { timeout: 10000 });
        let got = await logView(page);
        assert(got.status === "running" && got.live === "5000" && got.head === "The whole log, 6 lines. Live while the job runs." && !got.retry, `the running job's log reads ${JSON.stringify(got)}`);
        await page.focus("[data-editor-sheet-body] [data-forge-job-page]");
        asked();
        fakeForge.state.traceFill = 2;
        await logHas(page, "team/app/pkg1", 8000);
        got = await logView(page);
        assert(got.head === "The whole log, 8 lines. Live while the job runs." && got.end, `the grown log reads ${JSON.stringify(got)}`);
        assert(await page.evaluate(() => document.activeElement?.hasAttribute("data-forge-job-page") || false), "the read again took the focus off the job's page");
        traces();
        await L.sleep(10500);
        const reads = traces();
        assert(reads >= 1 && reads <= 3, `in 10.5 s the running job's trace was read ${reads} times`);
        fakeForge.state.traceFill = 80;
        await logHas(page, "team/app/pkg79", 8000);
        got = await logView(page);
        assert(got.overflows && got.end && got.head === "The whole log, 86 lines. Live while the job runs.", `the log grown past its box reads ${JSON.stringify({ ...got, text: undefined })}`);
        assert(got.fills.ok, `the grown log does not fill the sheet as its one scroller with Back on top: ${JSON.stringify(got.fills)}`);
        await page.evaluate(() => { document.querySelector("[data-editor-sheet-body] [data-forge-log-text]").scrollTop = 0; });
        fakeForge.state.traceFill = 81;
        await logHas(page, "team/app/pkg80", 8000);
        got = await logView(page);
        assert(got.top === 0 && !got.end, `a log scrolled back left its place: ${JSON.stringify({ ...got, text: undefined })}`);
        fakeForge.state.status = "failed";
        await page.waitForFunction(() => document.querySelector("[data-editor-sheet-body] [data-forge-log-view]")?.dataset.forgeJobStatus === "failed", null, { timeout: 30000 });
        got = await logView(page);
        assert(!got.live && got.head === "The whole log, 87 lines." && got.retry, `the ended job's log reads ${JSON.stringify({ ...got, text: undefined })}`);
        asked();
        await L.sleep(6000);
        const after = traces();
        assert(after === 0, `the ended job's trace was read ${after} more times`);
      } finally {
        fakeForge.state.traceFill = 0;
      }
      await page.keyboard.press("Escape");
      await titled(page, "Pipeline #12 failed");
      await page.keyboard.press("Escape");
      await titled(page, "GitLab");
      await notReloaded(page);
    });

    await run("leaving the log of a running job, for the jobs or by closing the sheet, stops its reads", async () => {
      fakeForge.state.status = "running";
      await refresh();
      await sheetPipeline(page, "running", 15000);
      for (const leave of ["Escape", "Close"]) {
        await openLog(page, "31", "Log of unit", "Pipeline #12 running");
        await page.waitForFunction(() => document.querySelector("[data-editor-sheet-body] [data-forge-log-view]")?.dataset.forgeLogLive === "5000", null, { timeout: 10000 });
        if (leave === "Escape") {
          await page.keyboard.press("Escape");
          await titled(page, "Pipeline #12 running");
        } else {
          await page.click("[data-editor-sheet-close]");
          await page.waitForFunction(() => document.querySelector("[data-editor-sheet]").hidden, null, { timeout: 5000 });
        }
        asked();
        await L.sleep(6000);
        const reads = traces();
        assert(reads === 0, `after ${leave} the trace was read ${reads} more times`);
        if (leave === "Escape") await page.keyboard.press("Escape");
        else await openForge(page);
        await titled(page, "GitLab");
      }
      fakeForge.state.status = "failed";
      await refresh();
      await sheetPipeline(page, "failed", 15000);
      await notReloaded(page);
    });

    await run("a branch switch to one the forge holds nothing of says so in the open sheet and takes its button, the way back shows the old one, without a reload", async () => {
      await git(["switch", "-q", "main"]);
      await page.waitForFunction(() => document.querySelector("[data-editor-git-branch]")?.textContent.trim() === "main", null, { timeout: 10000 });
      const said = await emptied(page);
      let got = await sheet(page);
      assert(said === "No change request or pipeline for this branch." && got.title === "GitLab" && got.head === null && !got.rows.length, `on main the sheet reads ${said} ${rowTexts(got)}`);
      const gone = await forgeButton(page);
      assert(!gone.shown && !gone.item, `on main the forge's button reads ${JSON.stringify(gone)}`);
      await page.keyboard.press("Control+Shift+E");
      await sheetClosed(page);
      await page.keyboard.press("Control+Shift+E");
      await L.sleep(500);
      assert(await page.evaluate(() => document.querySelector("[data-editor-sheet]").hidden), "on main Ctrl+Shift+E opened the forge sheet");
      await git(["switch", "-q", "feature"]);
      await openForge(page);
      await sheetPipeline(page, "failed", 15000);
      got = await sheet(page);
      assert(got.head.title === "Fix the login" && got.rows.some((r) => r.text === "Log of unit"), `back on feature the sheet reads ${JSON.stringify(got.head)} ${rowTexts(got)}`);
      await notReloaded(page);
    });

    await run("a push to an upstream of another name is read at once", async () => {
      await openEditor(page);
      await openForge(page);
      fakeForge.state.status = "success";
      await git(["switch", "-q", "-c", "local-fix", "--track", "origin/feature"]);
      await page.waitForFunction(() => document.querySelector("[data-editor-git-branch]")?.textContent.trim() === "local-fix", null, { timeout: 10000 });
      await sheetPipeline(page, "success", 15000);
      const got = await sheet(page);
      assert(got.head.badge.change?.text === "!4", `on local-fix tracking feature the sheet reads ${JSON.stringify(got.head)}`);
      await git(["commit", "-q", "--allow-empty", "-m", "pushed from local-fix"]);
      const next = await git(["rev-parse", "HEAD"]);
      fakeForge.state.head = next;
      fakeForge.state.status = "running";
      asked();
      const at = Date.now();
      await git(["update-ref", "refs/remotes/origin/feature", next]);
      await sheetPipeline(page, "running", 15000);
      const log = asked();
      assert(log.some((r) => r.path === `${glProject}/pipelines` && r.query.includes(`sha=${next}`)), `the pushed head was not asked for: ${JSON.stringify(log)}`);
      await notReloaded(page);
      return `read after ${Date.now() - at} ms`;
    });

    await run("phone: the forge's button, the forge sheet, the jobs and the log stand within 390px, the log's last line in view, the log filling the sheet as its one scroller", async () => {
      fakeForge.state.status = "failed";
      const phone = await mobilePage();
      await openEditor(phone);
      await phone.waitForSelector("[data-editor-forge-status]:not([hidden])", { timeout: 20000 });
      const button = await forgeButton(phone);
      assert(button.shown && button.left >= 0 && button.right <= button.inner, `the phone's forge button stands at ${JSON.stringify(button)}`);
      await openGit(phone);
      const git = await sheet(phone);
      assert(git.rows[0]?.text === "Switch branch" && !git.rows.some((r) => r.text === "GitLab"), `the phone's git sheet reads ${rowTexts(git)}`);
      await phone.keyboard.press("Escape");
      await sheetClosed(phone);
      await phone.tap("[data-editor-menu]");
      await phone.tap("[data-editor-forge-item]");
      await titled(phone, "GitLab");
      await phone.keyboard.press("Escape");
      await sheetClosed(phone);
      await openForge(phone);
      await sheetPipeline(phone, "failed", 30000);
      const fits = (p) => p.evaluate(() => {
        const body = document.querySelector("[data-editor-sheet-body]");
        const panel = document.querySelector("[data-editor-sheet-panel]").getBoundingClientRect();
        const badge = body.querySelector("[data-forge-badge]")?.getBoundingClientRect();
        const pre = body.querySelector("[data-forge-log-text]")?.getBoundingClientRect();
        return {
          inner: window.innerWidth, scroll: document.documentElement.scrollWidth,
          body: { scroll: body.scrollWidth, client: body.clientWidth },
          panel: { left: panel.left, right: panel.right },
          badge: badge && { left: badge.left, right: badge.right },
          pre: pre && { left: pre.left, right: pre.right },
        };
      });
      const inside = (w, what) => {
        assert(w.scroll <= w.inner && w.body.scroll <= w.body.client, `${what}: something scrolls sideways ${JSON.stringify(w)}`);
        assert(w.panel.left >= 0 && w.panel.right <= w.inner, `${what}: the sheet stands at ${JSON.stringify(w.panel)}`);
        for (const box of [w.badge, w.pre].filter(Boolean)) assert(box.left >= 0 && box.right <= w.inner, `${what}: a part stands at ${JSON.stringify(box)} in ${w.inner}`);
      };
      let w = await fits(phone);
      assert(w.badge, "the phone's sheet shows no badge");
      inside(w, "the forge sheet");
      await phone.tap("[data-editor-sheet-body] [data-forge-jobs]");
      await titled(phone, "Pipeline #12 failed");
      inside(await fits(phone), "the jobs");
      fakeForge.state.traceFill = 80;
      try {
        await phone.tap('[data-editor-sheet-body] [data-forge-job="31"]');
        await phone.waitForSelector("[data-editor-sheet-body] [data-forge-log-text]", { timeout: 10000 });
      } finally {
        fakeForge.state.traceFill = 0;
      }
      w = await fits(phone);
      assert(w.pre, "the phone shows no log");
      inside(w, "the log");
      const end = await phone.evaluate(() => {
        const pre = document.querySelector("[data-editor-sheet-body] [data-forge-log-text]");
        const range = document.createRange();
        range.selectNodeContents(pre);
        const rects = range.getClientRects();
        return { last: rects[rects.length - 1].bottom, pre: pre.getBoundingClientRect().bottom, body: document.querySelector("[data-editor-sheet-body]").getBoundingClientRect().bottom, text: pre.textContent.trim().split("\n").pop() };
      });
      assert(end.text === "ERROR: Job failed: exit code 1" && end.last <= end.body + 1 && end.pre <= end.body + 1, `the log's last line stands below the sheet: ${JSON.stringify(end)}`);
      const filled = (await logView(phone)).fills;
      assert(filled.ok, `the phone's log does not fill the sheet as its one scroller with Back on top: ${JSON.stringify(filled)}`);
      const focusRing = await phone.evaluate(() => document.activeElement?.matches("input, textarea") || false);
      assert(!focusRing, "a field took the focus on touch");
    });

    await run("a guest of a shared link sees no forge in the editor and cannot read it", async () => {
      await page.goto(`${BASE}/settings/sharing`, { waitUntil: "domcontentloaded" });
      const made = await post("/settings/sharing/new", [["amount", "1"], ["unit", "hours"], ["name", linkName], ["project", project]]);
      const link = (made.text.match(/value="([^"]*\/share\/[^"]+)"/) || [])[1];
      assert(link, `making a link answered ${made.status}`);
      const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1360, height: 900 } });
      try {
        const guest = await ctx.newPage();
        L.wirePage(guest, bag);
        const forgeAsked = [];
        guest.on("request", (r) => { if (new URL(r.url()).pathname.includes("/forge")) forgeAsked.push(r.url()); });
        const base = new URL(link).pathname;
        await guest.goto(`${new URL(link).origin}${base}/projects/${project}/editor`, { waitUntil: "domcontentloaded" });
        await guest.waitForSelector("[data-editor-git-status]:not([hidden])", { timeout: 20000 });
        await openGit(guest);
        await L.sleep(1500);
        const got = await sheet(guest);
        assert(got.head === null && !got.rows.some((r) => r.jobs || r.job || r.text === "GitLab"), `the guest's sheet reads ${rowTexts(got)}`);
        assert(await guest.locator("[data-editor-forge-status], [data-editor-forge-item]").count() === 0, "the guest's editor carries the forge's button or menu entry");
        await guest.keyboard.press("Escape");
        await guest.keyboard.press("Control+Shift+E");
        await L.sleep(500);
        assert(await guest.evaluate(() => document.querySelector("[data-editor-sheet]").hidden), "Ctrl+Shift+E opened a sheet for the guest");
        const reads = await guest.evaluate(async (paths) => Promise.all(paths.map(async (p) => (await fetch(p)).status)), [`${base}/projects/${project}/forge/branch`, `${base}/projects/${project}/forge/log?job=31`]);
        assert(reads.every((code) => code === 404), `a guest's forge reads answered ${reads}`);
        assert(forgeAsked.length === 2, `the guest's page asked ${JSON.stringify(forgeAsked)}`);
        assert(!(await guest.content()).includes("Fix the login"), "the guest's page names the change request");
      } finally {
        await ctx.close();
      }
    });

    await run("GitHub: the log of a running Actions job says it comes once the job ended, asks GitHub nothing, and shows when it did", async () => {
      fakeForge.state.ghTest = "in_progress";
      const saved = await post(hostEdit, { form: "save", kind: "github", web_url: fakeWeb, aliases: alias });
      assert(saved.status === 200, `turning the host into GitHub answered ${saved.status}`);
      await git(["remote", "set-url", "origin", `${fakeWeb}/octo/app.git`]);
      await openEditor(page);
      await refresh();
      await openForge(page, "GitHub");
      await sheetPipeline(page, "running", 15000);
      asked();
      await openLog(page, "6", "Log of test", "Pipeline running");
      await page.waitForSelector("[data-editor-sheet-body] [data-forge-log-later]", { timeout: 10000 });
      let got = await logView(page);
      assert(got.later === "GitHub hands out a job's log only once the job ended." && got.status === "running" && !got.live && !got.head && !got.text, `the running Actions job's log reads ${JSON.stringify(got)}`);
      await L.sleep(1000);
      const logs = asked().filter((r) => r.path.includes("/actions/jobs/") || r.path.startsWith("/blob/"));
      assert(logs.length === 0, `GitHub was asked for the log of a running job: ${JSON.stringify(logs)}`);
      fakeForge.state.ghTest = "failure";
      await refresh();
      await page.waitForSelector("[data-editor-sheet-body] [data-forge-log-text]", { timeout: 15000 });
      got = await logView(page);
      assert(got.status === "failed" && !got.later && !got.live && got.text.endsWith("##[error]Process completed with exit code 1."), `the ended Actions job's log reads ${JSON.stringify(got)}`);
      await notReloaded(page);
    });

    await run("docs: the Forges topic explains the editor's forge sheet, the editor's git sheet does not lead there", async () => {
      await page.goto(`${BASE}/docs`, { waitUntil: "domcontentloaded" });
      const item = page.locator("section#forges .list-group-item", { hasText: "Status in the editor" });
      assert(await item.count() === 1, "no docs entry for the status in the editor");
      const text = await item.innerText();
      assert(!text.includes("git sheet"), `the entry still leads from the git sheet: ${text}`);
      for (const said of ["The forge sheet opens from the forge's button in the statusbar and from Forge in the editor menu", "the row's badge", "Jobs lists the pipeline's jobs", "the last 100 lines of its log", "read again every 5 seconds while the job runs", "GitHub hands out a log only once the job ended", "guest of a shared link sees none of it"]) {
        assert(text.includes(said), `the entry lacks "${said}": ${text}`);
      }
      const git = await page.locator("section#editor .list-group-item", { hasText: "Branch, push and pull" }).innerText();
      assert(git.includes("opens the git sheet") && !git.includes("forge"), `the git sheet's entry reads ${git}`);
    });
  } finally {
    await page.goto(`${BASE}/settings/sharing`, { waitUntil: "domcontentloaded" }).catch(() => {});
    const linkId = await page.evaluate((name) => [...document.querySelectorAll("[data-share-row]")]
      .find((r) => r.querySelector("[data-share-name]")?.textContent.trim() === name)?.dataset.shareRow || "", linkName).catch(() => "");
    if (linkId) await post(`/settings/sharing/${linkId}`, { form: "revoke" }).catch(() => {});
    if (hostEdit) await post(hostEdit, { form: "delete" }).catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
  }
});
