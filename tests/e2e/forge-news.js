const L = require("./lib");
const { assert, BASE, sleep } = L;

// Forge news: a failed pipeline of the branch a bound project has checked out
// is news (internal/cli forgeevents.go, the target pipeline:<project> of
// internal/notify), once per pipeline, one entry per project, and goes out
// through the push channels like other news. Its title is the fixed sentence
// "Pipeline failed.", the line below names branch, project and change request
// and then the jobs that failed it. The entry links the editor opened on the
// pipeline (/projects/:name/editor?view=pipeline), which opens the forge sheet
// with the pipeline and reads the news on the server, so a push or a webhook
// link reads it as well as the bell does; the plain editor reads nothing. A
// pipeline that runs or passes is no news, and the same end read again is no
// second one. The forge is the runner's own fake (forge-fake.js) on FORGE_PORT
// (default 3157), published on the host's loopback, where the instance
// reaches it at FORGE_ADDR (default 127.0.0.1); the webhook the runner
// registers posts to the fake too. Two projects bind the same repository, so
// one failure is news twice. A pipeline ends on the poller's next read, up to
// 20 seconds after the fake moved; a run takes about a minute and a half per
// engine.

const { startFakeForge, addHost } = require("./forge-fake");

const FAKE_PORT = Number(process.env.FORGE_PORT || 3157);
const FAKE_TLS_PORT = Number(process.env.FORGE_TLS_PORT || 3158);
const FAKE_ADDR = process.env.FORGE_ADDR || "127.0.0.1";
const TOKEN = `glpat-e2e${Date.now().toString(36)}Nw5wR2sT9uV4`;
// Past the poller's fast interval of 15 seconds and its look every 5.
const QUIET_MS = 17000;
let fakeForge = null;

L.runFeature("FORGE_NEWS", async ({ page, run, mobilePage }) => {
  const stamp = Date.now().toString(36);
  const first = `zzforgenews-${stamp}`;
  const second = `zzforgenewz-${stamp}`;
  const alias = `fn-${stamp}`;
  const fakeWeb = `http://${FAKE_ADDR}:${FAKE_PORT}`;
  const hookUrl = `${fakeWeb}/hook`;
  fakeForge ||= await startFakeForge({ port: FAKE_PORT, tlsPort: FAKE_TLS_PORT, token: TOKEN });
  const paths = {};
  let hostEdit = "";
  let hookAdded = false;
  let phone = null;
  const detail = (project) => `feature in ${project}, merge request !4: unit, child, ci/external`;
  const link = (project) => `/projects/${project}/editor?view=pipeline`;

  const git = async (cwd, args) => {
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
    }, { cwd, args });
    if (res.failed || res.code !== 0) throw new Error(`git ${args.join(" ")}: ${res.failed || res.err}`);
    return res.out.trim();
  };

  const post = (target, fields) => page.evaluate(async ({ target, fields }) => {
    const body = new URLSearchParams({ ...fields, csrf_token: document.querySelector('meta[name="csrf-token"]').content });
    const r = await fetch(target, { method: "POST", body });
    return { status: r.status, url: r.url };
  }, { target, fields });

  const readJSON = (target) => page.evaluate(async (t) => {
    const r = await fetch(t, { headers: { Accept: "application/json" } });
    return { code: r.status, body: await r.json().catch(() => ({})) };
  }, target);
  const status = (project, query = "") => readJSON(`/projects/${project}/forge/status${query}`);

  // news answers the entries of the project's pipelines.
  const news = async (project) => ((await readJSON("/notifications")).body.notifications || [])
    .filter((n) => n.targetId === `pipeline:${project}`);
  const waitNews = async (project, want, what, timeout = 30000) => {
    const deadline = Date.now() + timeout;
    let seen = [];
    while (Date.now() < deadline) {
      seen = await news(project);
      if (want(seen)) return seen;
      await sleep(400);
    }
    assert(false, `${what}: the news of ${project} reads ${JSON.stringify(seen)}`);
    return seen;
  };
  const hooksOf = (project) => fakeForge.hooks.map((h) => JSON.parse(h)).filter((h) => (h.body || "").includes(` in ${project},`));

  const sheet = (p) => p.evaluate(() => {
    const el = document.querySelector("[data-editor-sheet]");
    const panel = document.querySelector("[data-editor-sheet-panel]");
    const box = panel.getBoundingClientRect();
    const top = document.elementFromPoint(box.left + box.width / 2, box.top + Math.min(box.height / 2, 40));
    return {
      open: !el.hidden && getComputedStyle(el).display !== "none" && box.width > 0 && box.height > 0,
      onTop: !!top && panel.contains(top),
      title: document.querySelector("[data-editor-sheet-title]")?.textContent.trim() || "",
      pipeline: document.querySelector("[data-editor-sheet-body] [data-forge-head] [data-forge-badge]")?.dataset.forgePipeline || "",
      logs: [...document.querySelectorAll("[data-editor-sheet-body] [data-forge-job]")].map((r) => (r.querySelector(":scope > .d-flex > span:first-child") || r).textContent.trim()),
      left: box.left, right: box.right, inner: window.innerWidth, scroll: document.documentElement.scrollWidth,
    };
  });
  const waitSheet = async (p) => {
    await p.waitForFunction(() => {
      const el = document.querySelector("[data-editor-sheet]");
      return el && !el.hidden && document.querySelector("[data-editor-sheet-title]")?.textContent.trim() === "GitLab"
        && document.querySelector("[data-editor-sheet-body] [data-forge-head] [data-forge-badge]")?.dataset.forgePipeline === "failed";
    }, null, { timeout: 15000 });
    return sheet(p);
  };

  try {
    await run("setup: two projects of one repository on the fake GitLab, their pushed branch feature running a pipeline, and a webhook", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      fakeForge.state.status = "running";
      for (const project of [first, second]) {
        await L.createProject(page, project);
        paths[project] = await L.projectPath(page, project);
        const cwd = paths[project];
        await git(cwd, ["init", "-q", "-b", "main"]);
        if (project === first) {
          for (const [key, value] of [["user.email", "e2e@example.com"], ["user.name", "e2e"], ["commit.gpgsign", "false"]]) await git(cwd, ["config", key, value]);
          await git(cwd, ["commit", "-q", "--allow-empty", "-m", "first"]);
          await git(cwd, ["switch", "-q", "-c", "feature"]);
          await git(cwd, ["commit", "-q", "--allow-empty", "-m", "pushed"]);
        } else {
          await git(cwd, ["fetch", "-q", "--", paths[first], "feature"]);
          await git(cwd, ["switch", "-q", "-c", "feature", "FETCH_HEAD"]);
        }
        await git(cwd, ["update-ref", "refs/remotes/origin/feature", "HEAD"]);
        await git(cwd, ["remote", "add", "origin", `${fakeWeb}/gl/team/sub/app.git`]);
      }
      const heads = [await git(paths[first], ["rev-parse", "HEAD"]), await git(paths[second], ["rev-parse", "HEAD"])];
      assert(heads[0] === heads[1], `the two pushed heads differ: ${heads}`);
      fakeForge.state.head = heads[0];
      await page.goto(`${BASE}/settings/notifications`, { waitUntil: "domcontentloaded" });
      const hooked = await post("/settings/notifications", { form: "webhook-add", url: hookUrl });
      assert(hooked.status === 200, `adding the webhook answered ${JSON.stringify(hooked)}`);
      hookAdded = true;
      await page.goto(`${BASE}/settings/forges`, { waitUntil: "domcontentloaded" });
      const added = await addHost(page, { kind: "gitlab", web_url: `${fakeWeb}/gl`, aliases: alias, token: TOKEN });
      assert(!added.failed, added.failed);
      await page.goto(`${BASE}/settings/forges`, { waitUntil: "domcontentloaded" });
      hostEdit = await page.evaluate((mark) => [...document.querySelectorAll("[data-forge-host]")]
        .find((r) => r.textContent.includes(mark))?.querySelector("[data-forge-edit]")?.getAttribute("href") || "", alias);
      assert(hostEdit, "the added host has no Edit on the list");
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      for (const project of [first, second]) {
        await page.waitForFunction((sel) => document.querySelector(`${sel} [data-forge-badge]`)?.dataset.forgePipeline === "running", `#project-${project}`, { timeout: 20000 });
      }
    });

    await run("a pipeline that runs is no news", async () => {
      for (const project of [first, second]) {
        const seen = await news(project);
        assert(seen.length === 0, `${project} has news: ${JSON.stringify(seen)}`);
      }
    });

    await run("the failed pipeline is news of each project, toasted on the open page and posted to the webhook", async () => {
      fakeForge.hooks.length = 0;
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await L.waitUpgraded(page, ["dc-notifications"], 8000);
      fakeForge.state.status = "failed";
      for (const project of [first, second]) {
        const [n] = await waitNews(project, (seen) => seen.length === 1, "the failure");
        assert(n.title === "Pipeline failed." && n.detail === detail(project) && n.url === link(project) && n.project === project && !n.read, `the news reads ${JSON.stringify(n)}`);
      }
      await page.waitForFunction(() => [...document.querySelectorAll(".dc-toast")].some((t) => t.textContent.includes("Pipeline failed.")), null, { timeout: 10000 });
      const toast = await page.evaluate(() => [...document.querySelectorAll(".dc-toast")].find((t) => t.textContent.includes("Pipeline failed."))?.innerText || "");
      assert([first, second].some((project) => toast.includes(detail(project))), `the toast reads ${JSON.stringify(toast)}`);
      const deadline = Date.now() + 10000;
      while (Date.now() < deadline && (hooksOf(first).length === 0 || hooksOf(second).length === 0)) await sleep(250);
      for (const project of [first, second]) {
        const hooks = hooksOf(project);
        assert(hooks.length === 1 && hooks[0].title === "Pipeline failed." && hooks[0].body === detail(project) && hooks[0].url === link(project), `the webhook got ${JSON.stringify(hooks)}`);
      }
      assert(!(await page.content()).includes(TOKEN), "the page shows the token");
    });

    await run("the same end read again is no news again", async () => {
      const before = {};
      for (const project of [first, second]) before[project] = (await news(project)).map((n) => n.id).join();
      for (let i = 0; i < 2; i++) {
        for (const project of [first, second]) {
          const { code, body } = await status(project, "?fresh=1");
          assert(code === 200 && body.pipeline?.status === "failed", `a fresh read answered ${code} ${JSON.stringify(body)}`);
        }
      }
      await sleep(QUIET_MS);
      for (const project of [first, second]) {
        const ids = (await news(project)).map((n) => n.id).join();
        assert(ids === before[project], `${project} has news again: ${ids} after ${before[project]}`);
        assert(hooksOf(project).length === 1, `the webhook got ${project} again: ${JSON.stringify(hooksOf(project))}`);
      }
    });

    await run("the bell's entry opens the editor on the forge sheet with the failed pipeline and reads it, the other project's stays", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await L.waitUpgraded(page, ["dc-notifications"], 8000);
      await page.locator(".dc-notify-bell:visible").first().click();
      const entry = page.locator(`.dc-notify-menu.show a[data-notify-target="pipeline:${first}"]`);
      await entry.waitFor({ state: "visible", timeout: 8000 });
      const said = await entry.innerText();
      assert(said.includes("Pipeline failed.") && said.includes(detail(first)) && said.includes("unread"), `the entry reads ${JSON.stringify(said)}`);
      await entry.click();
      await page.waitForURL((u) => u.pathname === `/projects/${first}/editor` && u.search === "?view=pipeline", { timeout: 15000 });
      const got = await waitSheet(page);
      assert(got.open && got.onTop && got.logs.includes("Log of unit"), `the sheet reads ${JSON.stringify(got)}`);
      const [n] = await waitNews(first, (seen) => seen.length === 1 && seen[0].read, "the open");
      assert(n.title === "Pipeline failed.", `the read entry reads ${JSON.stringify(n)}`);
      const other = await news(second);
      assert(other.length === 1 && !other[0].read, `the other project's news reads ${JSON.stringify(other)}`);
    });

    await run("phone: the plain editor reads nothing, the link a push opens shows the forge sheet on top within 390px and reads it", async () => {
      phone = await mobilePage();
      await phone.goto(`${BASE}/projects/${second}/editor`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(phone);
      await phone.waitForSelector("[data-editor-git-status]:not([hidden])", { timeout: 20000 });
      await sleep(1500);
      let seen = await news(second);
      assert(seen.length === 1 && !seen[0].read, `the plain editor read it: ${JSON.stringify(seen)}`);
      assert(await phone.evaluate(() => document.querySelector("[data-editor-sheet]").hidden), "the plain editor opened a sheet");
      await phone.goto(`${BASE}${link(second)}`, { waitUntil: "domcontentloaded" });
      const got = await waitSheet(phone);
      assert(got.open && got.onTop && got.logs.includes("Log of unit"), `the sheet reads ${JSON.stringify(got)}`);
      assert(got.left >= 0 && got.right <= got.inner && got.scroll <= got.inner, `the sheet stands at ${JSON.stringify(got)}`);
      seen = await news(second);
      assert(seen.length === 1 && seen[0].read, `the link did not read it: ${JSON.stringify(seen)}`);
    });

    await run("a retried pipeline that passes is no news", async () => {
      fakeForge.hooks.length = 0;
      const before = {};
      for (const project of [first, second]) before[project] = (await news(project)).map((n) => `${n.id}:${n.read}`).join();
      fakeForge.state.status = "running";
      for (const project of [first, second]) {
        const { code, body } = await status(project, "?fresh=1");
        assert(code === 200 && body.pipeline?.status === "running", `the retry reads ${code} ${JSON.stringify(body)}`);
      }
      fakeForge.state.status = "success";
      for (const project of [first, second]) {
        const { code, body } = await status(project, "?fresh=1");
        assert(code === 200 && body.pipeline?.status === "success", `the pass reads ${code} ${JSON.stringify(body)}`);
      }
      await sleep(4000);
      for (const project of [first, second]) {
        const now = (await news(project)).map((n) => `${n.id}:${n.read}`).join();
        assert(now === before[project], `${project}'s news moved: ${now} after ${before[project]}`);
      }
      assert(fakeForge.hooks.length === 0, `the webhook got ${JSON.stringify(fakeForge.hooks)}`);
    });

    await run("docs: Notifications explains the failed pipeline", async () => {
      await page.goto(`${BASE}/docs`, { waitUntil: "domcontentloaded" });
      const item = page.locator("section#notifications .list-group-item", { hasText: "A failed pipeline" });
      assert(await item.count() === 1, "no docs entry for the failed pipeline");
      const text = await item.innerText();
      for (const said of ["One entry per project bound to a forge", "the pipeline of its checked out branch fails", "once per pipeline, also across a restart", "opens the editor's forge sheet", "deleting the project reads it too"]) {
        assert(text.includes(said), `the entry lacks "${said}": ${text}`);
      }
    });
  } finally {
    if (phone) await phone.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" }).catch(() => {});
    if (hostEdit) await post(hostEdit, { form: "delete" }).catch(() => {});
    if (hookAdded) {
      await page.goto(`${BASE}/settings/notifications`, { waitUntil: "domcontentloaded" }).catch(() => {});
      const id = await page.evaluate((url) => {
        const row = [...document.querySelectorAll("dc-push-settings .list-group-item")].find((r) => r.textContent.includes(url));
        return row?.querySelector('form input[name="form"][value="webhook-remove"]')?.parentElement.querySelector('input[name="id"]')?.value || "";
      }, hookUrl).catch(() => "");
      if (id) await post("/settings/notifications", { form: "webhook-remove", id }).catch(() => {});
    }
    for (const project of [first, second]) await L.deleteProject(page, project).catch(() => {});
  }
});
