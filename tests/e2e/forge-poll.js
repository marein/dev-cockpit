const L = require("./lib");
const { assert, BASE } = L;

// Forge poll: the forge service's poller keeps the status of the branch HEAD
// is on of every project bound to a host with a token (internal/forge,
// Service.Run), and the projects page row shows it beside the branch as a
// badge ([data-forge-badge] in [data-project-meta], forge_badge.gohtml): the
// change request by its number, the pipeline by its status, each linking to
// its page, in the pipeline's colour, a running one's icon the cockpit's
// spinner (.dc-icon-spinner). A move reaches an open page over the
// SSE signal forge, which dc-project-list answers with a pull of the row; the
// page never reloads. The poller reads a running pipeline every 15 seconds,
// an ended one every 5 minutes, a branch whose tracking ref moved at once,
// and a host without token never. A saved token is the one the next read
// takes, at once, a removed one is not sent again. The status read behind
// forge-show answers from the poller's read while it is no older than 30
// seconds, fresh=1 asks the forge. The forge is the runner's own fake (forge-fake.js) on
// FORGE_PORT (default 3157), published on the host's loopback, where the
// instance reaches it at FORGE_ADDR (default 127.0.0.1); its pipeline stands
// where state.status says. The repository is made with git through the
// cockpit's own proxy (POST /git), a push is an update-ref of the tracking
// ref. The host this runner adds carries its stamp in an alias and goes
// again at the end with its project. A branch whose name holds C1 controls or
// a direction mark is never read: the row's warning and the forge sheet say
// that the forge features are off for it and why. A run waits out two poll
// intervals, a little over half a minute per engine.

const { startFakeForge, addHost } = require("./forge-fake");

const FAKE_PORT = Number(process.env.FORGE_PORT || 3157);
const FAKE_TLS_PORT = Number(process.env.FORGE_TLS_PORT || 3158);
const FAKE_ADDR = process.env.FORGE_ADDR || "127.0.0.1";
const TOKEN = `glpat-e2e${Date.now().toString(36)}Pq7wR2sT9uV4`;
// Past the poller's fast interval of 15 seconds and its look every 5.
const QUIET_MS = 17000;
let fakeForge = null;

L.runFeature("FORGE_POLL", async ({ page, run, mobilePage }) => {
  const stamp = Date.now().toString(36);
  const project = `zzforgepoll-${stamp}`;
  const alias = `fp-${stamp}`;
  const fakeWeb = `http://${FAKE_ADDR}:${FAKE_PORT}`;
  const web = "https://gitlab.example.com/team/sub/app";
  const glProject = "/gl/api/v4/projects/team%2Fsub%2Fapp";
  const row = `#project-${project}`;
  fakeForge ||= await startFakeForge({ port: FAKE_PORT, tlsPort: FAKE_TLS_PORT, token: TOKEN });
  let path = "";
  let hostEdit = "";

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
    const body = new URLSearchParams({ ...fields, csrf_token: document.querySelector('meta[name="csrf-token"]').content });
    const r = await fetch(target, { method: "POST", body });
    return { status: r.status, url: r.url };
  }, { target, fields });

  const status = (query = "") => page.evaluate(async (target) => {
    const r = await fetch(target, { headers: { Accept: "application/json" } });
    return { code: r.status, body: await r.json() };
  }, `/projects/${project}/forge/status${query}`);

  const asked = () => fakeForge.log.splice(0);
  const projectAsked = () => asked().filter((r) => r.path.startsWith(glProject));

  // badge reads the row's badge as it stands on the page, null without one.
  const badge = (p) => p.evaluate((sel) => {
    const el = document.querySelector(`${sel} [data-project-meta] [data-forge-badge]`);
    if (!el) return null;
    const box = el.getBoundingClientRect();
    const style = getComputedStyle(el);
    const link = (a) => a && {
      text: a.textContent.trim(), href: a.getAttribute("href") || "", target: a.getAttribute("target") || "",
      title: a.getAttribute("title") || "", icon: a.querySelector("i")?.className || "",
    };
    return {
      shown: box.width > 0 && box.height > 0 && style.visibility !== "hidden" && style.display !== "none",
      pipeline: el.dataset.forgePipeline || "",
      tone: [...el.classList].find((c) => c.endsWith("-lt")) || "",
      change: link(el.querySelector("[data-forge-change]")),
      status: link(el.querySelector("[data-forge-status]")),
      problem: !!el.querySelector("[data-forge-problem]"),
      left: box.left, right: box.right,
    };
  }, row);
  const pipelineShown = (p, want, timeout) => p.waitForFunction(([sel, want]) => {
    const el = document.querySelector(`${sel} [data-forge-badge]`);
    return want === "" ? !el : el?.dataset.forgePipeline === want;
  }, [row, want], { timeout });
  const notReloaded = async () => {
    assert(await page.evaluate(() => window.__forgePollMark === 1), "the page reloaded");
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
      const pushed = await git(["rev-parse", "HEAD"]);
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

    await run("the row shows the merge request and the running pipeline as a badge, each linking to its page, the pipeline's icon a spinner", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await page.evaluate(() => { window.__forgePollMark = 1; });
      await pipelineShown(page, "running", 20000);
      const got = await badge(page);
      assert(got.shown && got.tone === "bg-blue-lt" && !got.problem, `the badge reads ${JSON.stringify(got)}`);
      const c = got.change;
      assert(c && c.text === "!4" && c.href === `${web}/-/merge_requests/4` && c.target === "_blank" && c.title === "Merge request !4, draft: Fix the login" && c.icon.includes("ti-git-pull-request-draft"), `the change reads ${JSON.stringify(c)}`);
      const s = got.status;
      assert(s && s.text === "running" && s.href === `${web}/-/pipelines/12` && s.target === "_blank" && s.title === "Pipeline #12 running" && s.icon.includes("ti-progress"), `the pipeline reads ${JSON.stringify(s)}`);
      const spin = await page.locator(`${row} [data-project-meta] [data-forge-status] i`).evaluate(L.iconSpin);
      assert(L.spinning(spin), `the running pipeline's icon is no turning spinner: ${JSON.stringify(spin)}`);
      assert(!(await page.content()).includes(TOKEN), "the page shows the token");
      await notReloaded();
    });

    await run("the badge goes from running to failed without a reload, its spinner gone", async () => {
      fakeForge.state.status = "failed";
      await pipelineShown(page, "failed", 30000);
      const got = await badge(page);
      assert(got.shown && got.tone === "bg-red-lt" && got.status.text === "failed" && got.status.title === "Pipeline #12 failed" && got.status.icon.includes("ti-circle-x"), `the badge reads ${JSON.stringify(got)}`);
      const stood = await page.locator(`${row} [data-project-meta] [data-forge-status] i`).evaluate(L.iconSpin);
      assert(L.still(stood), `the failed pipeline's icon still spins: ${JSON.stringify(stood)}`);
      assert(got.change && got.change.text === "!4", `the change went: ${JSON.stringify(got)}`);
      await notReloaded();
    });

    await run("the status read answers from the poll while it is fresh, an ended pipeline is not read again soon", async () => {
      asked();
      let { code, body } = await status();
      assert(code === 200 && body.change?.ref === "!4" && body.pipeline?.status === "failed" && body.pipeline.jobs.length === 4, `answered ${code} ${JSON.stringify(body)}`);
      assert(asked().length === 0, "a fresh poll was asked again");
      await L.sleep(QUIET_MS);
      const quiet = projectAsked();
      assert(quiet.length === 0, `an ended pipeline was read again within ${QUIET_MS} ms: ${JSON.stringify(quiet)}`);
      ({ code, body } = await status("?fresh=1"));
      assert(code === 200 && body.pipeline?.status === "failed", `asked fresh answered ${code} ${JSON.stringify(body)}`);
      assert(projectAsked().some((r) => r.path === `${glProject}/pipelines`), "asked for a fresh read, the forge was not asked");
    });

    await run("a saved token is the one the next read takes, at once: a wrong one warns, the right one clears it, a removed one is not sent", async () => {
      const save = async (fields, status = 200) => {
        asked();
        const saved = await post(`${hostEdit}/token`, fields);
        assert(saved.status === status && (status !== 200 || saved.url.endsWith("/settings/forges")), `saving the token led to ${JSON.stringify(saved)}`);
      };
      const warned = (want) => page.waitForFunction(([sel, want]) => {
        const el = document.querySelector(`${sel} [data-forge-badge]`);
        return el?.dataset.forgePipeline === "failed" && !!el.querySelector("[data-forge-problem]") === want;
      }, [row, want], { timeout: 10000 });
      const sentOnly = (token) => {
        const log = asked();
        const other = log.filter((r) => r.auth !== `Bearer ${token}`).map((r) => r.path);
        assert(log.length > 0 && other.length === 0, `after the save the forge was asked ${log.length} times, with another token for ${JSON.stringify(other)}`);
      };
      const wrong = `${TOKEN}x`;
      await save({ token: wrong }, 409);
      await warned(true);
      sentOnly(wrong);
      await save({ token: TOKEN });
      await warned(false);
      sentOnly(TOKEN);
      await save({ token_remove: "1" });
      await pipelineShown(page, "", 10000);
      await L.sleep(2000);
      const log = asked();
      assert(log.length === 0, `after the token went the forge was asked for ${JSON.stringify(log.map((r) => r.path))}`);
      await save({ token: TOKEN });
      await pipelineShown(page, "failed", 10000);
      sentOnly(TOKEN);
      await notReloaded();
    });

    await run("a push moves the badge at once, the moved head read from the forge", async () => {
      await git(["commit", "-q", "--allow-empty", "-m", "pushed again"]);
      const next = await git(["rev-parse", "HEAD"]);
      fakeForge.state.head = next;
      fakeForge.state.status = "running";
      asked();
      await git(["update-ref", "refs/remotes/origin/feature", next]);
      await pipelineShown(page, "running", 15000);
      const log = projectAsked();
      assert(log.some((r) => r.path === `${glProject}/pipelines` && r.query.includes(`sha=${next}`)), `the moved head was not asked for: ${JSON.stringify(log)}`);
      assert((await badge(page)).tone === "bg-blue-lt", "the badge kept its colour");
      await notReloaded();
    });

    await run("a branch named with C1 controls and a direction mark switches the forge off for it: the row and the forge sheet say why, the forge is never asked", async () => {
      const hidden = "x\u009b2J\u202egnp";
      const off = "The forge features are off for this branch, since its name breaks the cockpit's rule: a branch name holds no spaces, no control characters and no invisible characters, its character 2 is U+009B.";
      const head = await git(["rev-parse", "HEAD"]);
      await git(["update-ref", `refs/remotes/origin/${hidden}`, head]);
      asked();
      await git(["switch", "-q", "-c", hidden]);
      try {
        await page.waitForFunction((sel) => {
          const el = document.querySelector(`${sel} [data-forge-badge]`);
          return el && !el.dataset.forgePipeline && !!el.querySelector("[data-forge-problem]");
        }, row, { timeout: 15000 });
        const got = await badge(page);
        const title = await page.getAttribute(`${row} [data-forge-badge] [data-forge-problem]`, "title");
        assert(got.shown && !got.change && !got.status && title === off, `the badge reads ${JSON.stringify(got)} under "${title}"`);
        const sheet = await page.evaluate(async (target) => {
          const doc = new DOMParser().parseFromString(await (await fetch(target)).text(), "text/html");
          return {
            problem: doc.querySelector("[data-forge-problem-text]")?.textContent.trim() || "",
            offer: !!doc.querySelector("[data-forge-change-new]"),
            jobs: !!doc.querySelector("[data-forge-jobs]"),
          };
        }, `/projects/${project}/forge/branch`);
        assert(sheet.problem === off && !sheet.offer && !sheet.jobs, `the forge sheet reads ${JSON.stringify(sheet)}`);
        const { code, body } = await status("?fresh=1");
        assert(code === 409 && body.error === off, `the status read answered ${code} ${JSON.stringify(body)}`);
        await L.sleep(6000);
        const log = projectAsked();
        assert(log.length === 0, `the forge was asked of the refused branch: ${JSON.stringify(log)}`);
        await notReloaded();
      } finally {
        await git(["switch", "-q", "feature"]);
      }
      await page.waitForFunction((sel) => !!document.querySelector(`${sel} [data-forge-badge]`)?.dataset.forgePipeline, row, { timeout: 15000 });
      assert(!(await badge(page)).problem, "back on feature the badge still warns");
    });

    await run("a host without token: the badge goes without a reload and the forge is asked no more", async () => {
      const saved = await post(`${hostEdit}/token`, { token_remove: "1" });
      assert(saved.status === 200 && saved.url.endsWith("/settings/forges"), `removing the token led to ${JSON.stringify(saved)}`);
      await pipelineShown(page, "", 15000);
      asked();
      await L.sleep(QUIET_MS);
      const log = asked();
      assert(log.length === 0, `a host without token was asked: ${JSON.stringify(log)}`);
      await notReloaded();
    });

    await run("phone: the badge stands in the row within 390px", async () => {
      const saved = await post(`${hostEdit}/token`, { token: TOKEN });
      assert(saved.status === 200, `the token came not back: ${JSON.stringify(saved)}`);
      const phone = await mobilePage();
      await phone.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await pipelineShown(phone, "running", 20000);
      const got = await badge(phone);
      const width = await phone.evaluate(() => ({ inner: window.innerWidth, scroll: document.documentElement.scrollWidth }));
      assert(got.shown && got.left >= 0 && got.right <= width.inner, `the badge stands at ${JSON.stringify(got)} in ${JSON.stringify(width)}`);
      assert(width.scroll <= width.inner, `the page scrolls sideways: ${JSON.stringify(width)}`);
    });

    await run("docs: the Forges topic explains the badge, the poll and the status read", async () => {
      await page.goto(`${BASE}/docs`, { waitUntil: "domcontentloaded" });
      const item = page.locator("section#forges .list-group-item", { hasText: "Status on the project row" });
      assert(await item.count() === 1, "no docs entry for the status on the project row");
      const text = await item.innerText();
      for (const said of ["the change request by its number", "red failed", "a running one with a spinner", "Each project is checked on its own: every 15s while a pipeline runs or 2m after a push, every 5m when idle. Review threads are read along with these checks, at most every 1m. After a failed check it waits longer, up to 5m."]) {
        assert(text.includes(said), `the entry lacks "${said}": ${text}`);
      }
      const off = await page.locator("section#forges .list-group-item", { hasText: "Branches the forge stays off for" }).innerText();
      assert(off.includes("never read from the forge nor written to it") && off.includes("merges into such a branch shows its number alone") && off.includes("name the character"), `the gate's entry reads ${off}`);
    });
  } finally {
    if (hostEdit) await post(hostEdit, { form: "delete" }).catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
  }
});
