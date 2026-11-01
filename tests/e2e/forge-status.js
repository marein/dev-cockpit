const L = require("./lib");
const { assert, BASE } = L;

// Forge status: what the forge a project is bound to says about one of its
// branches (internal/forge, StatusReader), read at
// /projects/:name/forge/status, the local API's read behind the assistant's
// forge-show. The branch is HEAD's unless the query names one, its pushed head
// the commit origin's remote tracking ref names, never the local commit ahead
// of it. A GitLab under a path reads the merge request whose source is the
// branch in the project itself, not a fork's of the same name, and the
// pipeline of the pushed head with its jobs, the project path one encoded
// segment, its trigger jobs and external statuses among the jobs; a GitHub
// Enterprise Server the pull request of owner:branch held against the head's
// repository and the check runs and commit statuses of the pushed head
// combined. Asked for with log, the answer carries the end of the failed jobs'
// logs as plain text, GitLab's trace and GitHub's Actions log, which GitHub
// hands out at a download address read without the token; a job without a
// log says why. Asked for with threads, it carries the review threads of the
// change request, the ones on the diff apart from the general ones: GitLab's
// discussions without its system notes, outdated where they stand on an
// earlier version of the diff, and GitHub's review threads over GraphQL
// with the conversation's comments and review bodies over REST, a pending
// comment and an empty review left out. A branch never pushed is an answer
// without pipeline and without review threads, a host
// without its token and a project bound to no forge are refused in a
// sentence. The forge is the runner's own fake (forge-fake.js) on FORGE_PORT
// (default 3157), published on the host's loopback, where the instance
// reaches it at FORGE_ADDR (default 127.0.0.1). The repository is made with
// git through the cockpit's own proxy (POST /git), the pushed head set with
// update-ref, nothing is pushed or fetched. The host this runner adds carries
// its stamp in an alias and goes again at the end with its project. Every
// read asks for a fresh answer, what the poller read answers forge-poll.js;
// the poller reads the project on its own too, so a check waits for its read
// before it counts requests, or counts the paths asked.

const { startFakeForge, addHost } = require("./forge-fake");

const FAKE_PORT = Number(process.env.FORGE_PORT || 3157);
const FAKE_TLS_PORT = Number(process.env.FORGE_TLS_PORT || 3158);
const FAKE_ADDR = process.env.FORGE_ADDR || "127.0.0.1";
const TOKEN = `glpat-e2e${Date.now().toString(36)}Xq3vR8sT2uW5`;
let fakeForge = null;

L.runFeature("FORGE_STATUS", async ({ page, run }) => {
  const stamp = Date.now().toString(36);
  const project = `zzforgestatus-${stamp}`;
  const alias = `fs-${stamp}`;
  const fakeWeb = `http://${FAKE_ADDR}:${FAKE_PORT}`;
  fakeForge ||= await startFakeForge({ port: FAKE_PORT, tlsPort: FAKE_TLS_PORT, token: TOKEN });
  let path = "";
  let hostEdit = "";
  let pushed = "";
  let local = "";

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

  // post sends a form the way its page does, with the page's token, and
  // answers where it led.
  const post = (target, fields) => page.evaluate(async ({ target, fields }) => {
    const body = new URLSearchParams({ ...fields, csrf_token: document.querySelector('meta[name="csrf-token"]').content });
    const r = await fetch(target, { method: "POST", body });
    return { status: r.status, url: r.url };
  }, { target, fields });

  const status = (query = "") => page.evaluate(async (target) => {
    const r = await fetch(target, { headers: { Accept: "application/json" } });
    return { code: r.status, body: await r.json() };
  }, `/projects/${project}/forge/status${query ? `${query}&` : "?"}fresh=1`);

  const asked = () => fakeForge.log.splice(0);
  // polled waits until the poller asked the forge for path, the last
  // request of its read.
  const polled = async (path) => {
    for (let i = 0; i < 100 && !fakeForge.log.some((r) => r.path === path); i++) await L.sleep(100);
    assert(fakeForge.log.some((r) => r.path === path), `the poller never asked for ${path}`);
  };
  // badged reports whether the project's row wears what the poller read,
  // which goes once the poller stopped reading it.
  const badged = async () => {
    const html = await (await page.request.get(`${BASE}/projects`)).text();
    const at = html.indexOf(`id="project-${project}"`);
    const next = html.indexOf('id="project-', at + 1);
    return at >= 0 && html.slice(at, next < 0 ? undefined : next).includes("data-forge-badge");
  };
  // byPath answers the first request of each path, the poller's and the
  // read's being the same.
  const byPath = (log) => Object.fromEntries([...log].reverse().map((r) => [r.path, r]));
  const glProject = "/gl/api/v4/projects/team%2Fsub%2Fapp";
  // threads reads a list of review threads in one line each, a note's link
  // shortened to what follows page.
  const threads = (list, page) => list.map((t) => `${t.id} ${t.path}:${t.line}:${t.side}${t.outdated ? " outdated" : ""} ` +
    `${t.resolvable ? (t.resolved ? "resolved" : "unresolved") : "comment"} ${t.notes.map((n) => `${n.author}: ${n.body} ${n.url.replace(page, "")}`).join(" | ")}`).join(", ");

  try {
    await run("setup: a repository whose branch feature is pushed and has one commit more", async () => {
      await L.createProject(page, project);
      path = await L.projectPath(page, project);
      await git(["init", "-q", "-b", "main"]);
      for (const [key, value] of [["user.email", "e2e@example.com"], ["user.name", "e2e"], ["commit.gpgsign", "false"]]) await git(["config", key, value]);
      await git(["commit", "-q", "--allow-empty", "-m", "first"]);
      await git(["switch", "-q", "-c", "feature"]);
      await git(["commit", "-q", "--allow-empty", "-m", "pushed"]);
      pushed = await git(["rev-parse", "HEAD"]);
      await git(["update-ref", "refs/remotes/origin/feature", pushed]);
      await git(["commit", "-q", "--allow-empty", "-m", "not pushed"]);
      local = await git(["rev-parse", "HEAD"]);
      await git(["remote", "add", "origin", `${fakeWeb}/gl/team/sub/app.git`]);
      fakeForge.state.head = pushed;
      await page.goto(`${BASE}/settings/forges`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      const added = await addHost(page, { kind: "gitlab", web_url: `${fakeWeb}/gl`, aliases: alias, token: TOKEN });
      assert(!added.failed, added.failed);
      await page.goto(`${BASE}/settings/forges`, { waitUntil: "domcontentloaded" });
      hostEdit = await page.evaluate((mark) => [...document.querySelectorAll("[data-forge-host]")]
        .find((row) => row.textContent.includes(mark))?.querySelector("[data-forge-edit]")?.getAttribute("href") || "", alias);
      assert(hostEdit, "the added host has no Edit on the list");
      await polled(`${glProject}/repository/commits/${pushed}/statuses`);
    });

    await run("GitLab: the project's own merge request and the pipeline of the pushed head, over one encoded segment", async () => {
      asked();
      const { code, body } = await status();
      assert(code === 200, `answered ${code} ${JSON.stringify(body)}`);
      assert(body.branch === "feature" && body.remoteBranch === "feature" && body.head === pushed && body.local === local, `the branch reads ${JSON.stringify(body)}`);
      assert(body.forge.label === "GitLab" && body.forge.changeNoun === "merge request" && body.forge.url === `${fakeWeb}/gl/team/sub/app`, `the forge reads ${JSON.stringify(body.forge)}`);
      const c = body.change;
      assert(c && c.ref === "!4" && c.title === "Fix the login" && c.draft === true && c.state === "open" && c.target === "main" && c.head === pushed, `the change reads ${JSON.stringify(c)}`);
      const p = body.pipeline;
      assert(p && p.id === "12" && p.status === "failed" && !p.partial, `the pipeline reads ${JSON.stringify(p)}`);
      const jobs = p.jobs.map((j) => `${j.id} ${j.name} ${j.stage} ${j.status}${j.noLog ? " no log" : ""}`).join(", ");
      assert(jobs === "30 build build success, 31 unit test failed, 32 child deploy failed no log, 33 ci/external  failed no log", `the jobs read ${jobs}`);
      assert(body.logs === null, `logs nobody asked for: ${JSON.stringify(body.logs)}`);
      assert(!("review" in body), `review threads nobody asked for: ${JSON.stringify(body.review)}`);
      const log = asked();
      const at = byPath(log);
      const paths = ["merge_requests", "pipelines", "pipelines/12/jobs", "pipelines/12/bridges", `repository/commits/${pushed}/statuses`].map((p) => `${glProject}/${p}`);
      assert(log.length >= 5 && log.every((r) => r.auth === `Bearer ${TOKEN}` && paths.includes(r.path)) && paths.every((p) => at[p]), `the forge was asked ${JSON.stringify(log)}`);
      assert(at[paths[0]].query.includes("source_branch=feature"), `the merge requests asked ${JSON.stringify(at[paths[0]])}`);
      assert(at[paths[1]].query.includes(`sha=${pushed}`), `the pipelines asked ${JSON.stringify(at[paths[1]])}`);
      assert(at[paths[4]].query.includes("pipeline_id=12") && at[paths[4]].query.includes("ref=feature"), `the statuses asked ${JSON.stringify(at[paths[4]])}`);
      assert(!JSON.stringify(log).includes(local), "the forge was asked for the local commit");
    });

    await run("GitLab: log=failed reads the failed job's trace as plain text, a trigger job and an external status say they have none", async () => {
      asked();
      const { code, body } = await status("?log=failed");
      assert(code === 200 && body.logs.length === 3 && body.logsLeft === 0, `answered ${code} ${JSON.stringify(body.logs)}`);
      const [unit, child, external] = body.logs;
      const lines = 'Running with gitlab-runner 17.5.0|Executing "step_script" stage of the job script|$ go test ./...|downloading 100%|--- FAIL: TestLogin (0.00s)|ERROR: Job failed: exit code 1';
      assert(unit.id === "31" && unit.name === "unit" && unit.error === "" && !unit.cut && unit.lines.join("|") === lines, `the trace reads ${JSON.stringify(unit)}`);
      assert(!JSON.stringify(unit.lines).includes("\\u001b") && !unit.lines.some((l) => l.includes("section_")), `the trace kept its sequences ${JSON.stringify(unit.lines)}`);
      assert(child.id === "32" && child.lines === null && child.error === "A trigger job has no log, the jobs of its downstream pipeline do.", `the trigger job reads ${JSON.stringify(child)}`);
      assert(external.id === "33" && external.lines === null && external.error === "An external commit status has no log on GitLab.", `the external status reads ${JSON.stringify(external)}`);
      const traces = asked().filter((r) => r.path.includes("/jobs/") && r.path.endsWith("/trace"));
      assert(traces.length === 1 && traces[0].path === `${glProject}/jobs/31/trace` && traces[0].auth === `Bearer ${TOKEN}`, `the traces asked ${JSON.stringify(traces)}`);
    });

    await run("GitLab: one job's log by its id cut to the lines asked for, a job the pipeline lacks and a count past the bound refused", async () => {
      let { code, body } = await status("?log=31&lines=2");
      assert(code === 200 && body.logs.length === 1 && body.logs[0].cut && body.logs[0].lines.join("|") === "--- FAIL: TestLogin (0.00s)|ERROR: Job failed: exit code 1", `answered ${code} ${JSON.stringify(body.logs)}`);
      ({ code, body } = await status("?log=99"));
      assert(code === 404 && body.error === 'The pipeline of "feature" has no job "99".', `a job the pipeline lacks answered ${code} ${JSON.stringify(body)}`);
      ({ code, body } = await status("?log=failed&lines=1001"));
      assert(code === 400 && body.error === "The lines of a log are a number from 1 to 1000.", `too many lines answered ${code} ${JSON.stringify(body)}`);
    });

    await run("GitLab: threads=1 reads the merge request's discussions, the ones on the diff apart from the general ones, without GitLab's own notes", async () => {
      asked();
      const { code, body } = await status("?threads=1");
      assert(code === 200 && body.change?.ref === "!4" && body.review && body.review.error === "" && body.review.partial === false, `answered ${code} ${JSON.stringify(body.review)}`);
      const page = "https://gitlab.example.com/team/sub/app/-/merge_requests/4";
      const diff = threads(body.review.diff, page);
      assert(diff === "d1e2e src/login.go:4:new unresolved root: New name bb? #note_29 | alice: Reply: yes, bb. #note_33, " +
        "d2e2e src/login.go:7:new outdated unresolved root: Added e #note_30, d3e2e src/old.go:4:old resolved root: Why drop b? #note_31", `the diff threads read ${diff}`);
      const general = threads(body.review.general, page);
      assert(general === "d4e2e :0: unresolved root: General thread #note_34 | alice: General reply #note_35, d5e2e :0: comment root: Individual note #note_36", `the general threads read ${general}`);
      assert(body.review.diff[0].notes[0].created === "2026-10-28T23:03:29.551Z", `a note was written at ${body.review.diff[0].notes[0].created}`);
      const reads = asked().filter((r) => r.path.includes("/merge_requests/4"));
      assert(reads.length === 2 && reads[0].path === `${glProject}/merge_requests/4` && reads[1].path === `${glProject}/merge_requests/4/discussions` &&
        reads[1].query.includes("per_page=100") && reads.every((r) => r.auth === `Bearer ${TOKEN}`), `the forge was asked ${JSON.stringify(reads)}`);
    });

    await run("a branch never pushed is an answer without pipeline and without review threads, and the forge is not asked for either", async () => {
      asked();
      const { code, body } = await status("?branch=main&threads=1");
      assert(code === 200 && body.branch === "main" && body.head === "" && body.local && body.change === null && body.pipeline === null && body.review === null, `answered ${code} ${JSON.stringify(body)}`);
      const log = asked();
      assert(log.length === 1 && log[0].path.endsWith("/merge_requests"), `the forge was asked ${JSON.stringify(log)}`);
    });

    await run("GitHub Enterprise Server: the pull request of the own repository and the checks combined", async () => {
      await page.goto(`${BASE}/settings/forges`, { waitUntil: "domcontentloaded" });
      const saved = await post(hostEdit, { form: "save", kind: "github", web_url: fakeWeb, aliases: alias });
      assert(saved.status === 200 && saved.url.endsWith("/settings/forges"), `changing the host led to ${JSON.stringify(saved)}`);
      await git(["remote", "set-url", "origin", `${fakeWeb}/octo/app.git`]);
      await polled(`/api/v3/repos/octo/app/commits/${pushed}/status`);
      asked();
      const { code, body } = await status();
      assert(code === 200 && body.head === pushed && body.forge.label === "GitHub" && body.forge.changeNoun === "pull request", `answered ${code} ${JSON.stringify(body)}`);
      const c = body.change;
      assert(c && c.ref === "#7" && c.title === "Fix the login" && c.state === "open" && c.target === "main", `the change reads ${JSON.stringify(c)}`);
      const p = body.pipeline;
      assert(p && p.id === "" && p.status === "pending" && p.url === `${fakeWeb}/octo/app/commit/${pushed}`, `the pipeline reads ${JSON.stringify(p)}`);
      const jobs = p.jobs.map((j) => `${j.id} ${j.name} ${j.status}${j.noLog ? " no log" : ""}`).join(", ");
      assert(jobs === "5 build success, 6 test failed, 7 scan failed no log, 9 ci/jenkins pending no log", `the jobs read ${jobs}`);
      const log = asked();
      const at = byPath(log);
      const paths = ["/api/v3/repos/octo/app/pulls", `/api/v3/repos/octo/app/commits/${pushed}/check-runs`, `/api/v3/repos/octo/app/commits/${pushed}/status`];
      assert(log.length >= 3 && log.every((r) => paths.includes(r.path)) && paths.every((p) => at[p]), `the forge was asked ${JSON.stringify(log)}`);
      assert(at[paths[0]].query.includes("head=octo%3Afeature"), `the pull requests asked ${JSON.stringify(at[paths[0]])}`);
    });

    await run("GitHub: log=failed downloads the failed Actions job's log without the token, a CodeQL check run says it has none", async () => {
      asked();
      const { code, body } = await status("?log=failed");
      assert(code === 200 && body.logs.length === 2 && body.logsLeft === 0, `answered ${code} ${JSON.stringify(body.logs)}`);
      const [test, scan] = body.logs;
      const lines = "2026-10-28T09:00:00.1000000Z ##[group]Run go test ./...|2026-10-28T09:00:01.2000000Z go test ./...|2026-10-28T09:00:02.3000000Z --- FAIL: TestLogin (0.00s)|2026-10-28T09:00:03.4000000Z ##[error]Process completed with exit code 1.";
      assert(test.id === "6" && test.name === "test" && test.error === "" && !test.cut && test.lines.join("|") === lines, `the Actions log reads ${JSON.stringify(test)}`);
      assert(scan.id === "7" && scan.lines === null && scan.error === 'The check run comes from "CodeQL", GitHub keeps no log of it.', `the CodeQL run reads ${JSON.stringify(scan)}`);
      const log = asked();
      const logs = log.filter((r) => r.path.endsWith("/logs"));
      const blob = log.filter((r) => r.path.startsWith("/blob/"));
      assert(logs.length === 1 && logs[0].path === "/api/v3/repos/octo/app/actions/jobs/6/logs" && logs[0].auth === `Bearer ${TOKEN}`, `the logs were asked ${JSON.stringify(logs)}`);
      assert(blob.length === 1 && blob[0].auth === "" && blob[0].query.includes("sig=e2e-sig"), `the download was asked ${JSON.stringify(blob)}`);
      assert(!JSON.stringify(body).includes("e2e-sig"), "the download address reached the answer");
    });

    await run("GitHub Enterprise Server: threads=1 reads the review threads over GraphQL and the conversation over REST", async () => {
      asked();
      const { code, body } = await status("?threads=1");
      assert(code === 200 && body.change?.ref === "#7" && body.review && body.review.error === "" && body.review.partial === false, `answered ${code} ${JSON.stringify(body.review)}`);
      const page = "https://github.example.com/octo/app/pull/7";
      const diff = threads(body.review.diff, page);
      assert(diff === "PRRT_kwDOe2e1 src/login.go:4:new unresolved octocat: New name bb? #discussion_r1, " +
        "PRRT_kwDOe2e2 src/login.go:7:new outdated unresolved octocat: Added e #discussion_r3, PRRT_kwDOe2e3 src/login.go:4:old resolved octocat: Why drop b? #discussion_r4", `the diff threads read ${diff}`);
      const general = threads(body.review.general, page);
      assert(general === "PRR_kwDOe2e80 :0: comment octocat: Please rename #pullrequestreview-80, IC_kwDOe2e11 :0: comment hubot: Looks good\r\nbut see the threads #issuecomment-11", `the general threads read ${general}`);
      const log = asked();
      const queries = log.filter((r) => r.path === "/api/graphql");
      assert(queries.length === 2 && queries.every((r) => r.auth === `Bearer ${TOKEN}`), `the GraphQL API was asked ${JSON.stringify(queries)}`);
      const [first, second] = queries.map((r) => JSON.stringify(r.variables));
      assert(first === '{"after":null,"first":50,"name":"app","notes":50,"number":7,"owner":"octo"}', `the first query asked ${first}`);
      assert(JSON.parse(second).after === "Y3Vyc29yOnYyOpK0", `the second query asked ${second}`);
      const rest = log.filter((r) => r.path.includes("/issues/7/") || r.path.includes("/pulls/7/"));
      assert(rest.length === 2 && rest[0].path === "/api/v3/repos/octo/app/issues/7/comments" && rest[1].path === "/api/v3/repos/octo/app/pulls/7/reviews" && rest.every((r) => r.auth === `Bearer ${TOKEN}`), `the REST API was asked ${JSON.stringify(rest)}`);
    });

    await run("refused in a sentence: a host without its token asks nothing, a project bound to no forge", async () => {
      await page.goto(`${BASE}/settings/forges`, { waitUntil: "domcontentloaded" });
      await post(`${hostEdit}/token`, { token_remove: "1" });
      for (let i = 0; i < 100 && await badged(); i++) await L.sleep(100);
      asked();
      let { code, body } = await status();
      assert(code === 409 && body.error === "The host has no token: add one on its row under Settings › Forges.", `without a token answered ${code} ${JSON.stringify(body)}`);
      assert(asked().length === 0, "a host without a token was asked");
      await post(`/projects/${project}/forge`, { how: "off" });
      ({ code, body } = await status());
      assert(code === 409 && body.error.startsWith(`"${project}" is bound to no forge`), `bound to no forge answered ${code} ${JSON.stringify(body)}`);
    });

    await run("docs: the Forges topic explains the change request, the pipeline, the failed job logs and the review threads", async () => {
      await page.goto(`${BASE}/docs`, { waitUntil: "domcontentloaded" });
      const item = page.locator("section#forges .list-group-item", { hasText: "Change request and pipeline" });
      assert(await item.count() === 1, "no docs entry for the change request and the pipeline");
      const text = await item.innerText();
      assert(text.includes("never the local HEAD") && text.includes("check runs and commit statuses combined"), `the entry reads ${text}`);
      const reads = page.locator("section#forges .list-group-item", { hasText: "For the assistants" });
      assert(await reads.count() === 1 && await reads.isVisible(), "no visible docs entry for what the assistants read");
      const said = await reads.innerText();
      for (const part of ["forge-show", "--log the end of the failed jobs' logs as plain text", "--threads the review threads", "the id a reply takes", "GitLab's own system notes are left out", "the conversation's comments and the review bodies"]) {
        assert(said.includes(part), `the entry lacks "${part}": ${said}`);
      }
    });
  } finally {
    if (hostEdit) await post(hostEdit, { form: "delete" }).catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
  }
});
