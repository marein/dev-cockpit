const { spawn, execFileSync } = require("child_process");
const fs = require("fs");
const path = require("path");
const L = require("./lib");
const { assert, BASE, sleep } = L;

// Worktree projects made by an assistant and the one worktree post script.
// `project-worktree-new` and `project-worktree-list` (internal/cli/worktrees.go)
// post the create form's own fields to `POST /projects`, one create path and
// one naming rule, <project>-<branch>. The script is edited on
// `/settings/projects/worktrees`, stored as `<state-dir>/worktree-post-script`
// (0700, LF), executed without a shell in every new worktree with DC_*
// variables. The page lands on the highlighted row without a flash, the CLI
// prints the output. A failure exits the CLI 1 and adds one notification,
// which opening and deleting the project read. An assistant reads the script
// with `project-worktree-script-show`, which ends with one line naming the
// directory, time bound and DC_* variables, and replaces it with
// `project-worktree-script-set`, which waits for the user's approval (the
// dialog shows the new content) unless the Worktree post script approval is
// off.
//
// The runner starts the real CLI as a child process, like git-proxy.js, so the
// instance needs a scratch directory mounted at its own host path (AUX_DIR)
// holding bin/dev-cockpit (a copy), the instance's --state-dir (state/) and
// --projects-dir (projects/), plus home/.
const AUX = process.env.AUX_DIR || "/tmp/dcwt";
const BIN = path.join(AUX, "bin", "dev-cockpit");
const STATE = path.join(AUX, "state");
const PROJECTS = path.join(AUX, "projects");
const MAIN = `wtm${Date.now().toString(36).slice(-5)}`;
const REPO = path.join(PROJECTS, MAIN);
const SCRIPT_FILE = path.join(STATE, "worktree-post-script");

const SUMMARY = "Runs in the new worktree, max 15s, env: DC_SOURCE_PROJECT DC_SOURCE_DIR DC_WORKTREE_DIR DC_WORKTREE_PROJECT DC_BRANCH.\n";
const BASH = `#!/usr/bin/env bash
echo "setup in $(basename "$PWD") on $(git branch --show-current)"
echo "vars $DC_SOURCE_PROJECT|$DC_PROJECT|$DC_SOURCE_DIR|$DC_WORKTREE_PROJECT|$DC_WORKTREE_DIR|$DC_BRANCH" > .setup-ran
`;
const SLOW = "#!/bin/sh\necho slow start\nsleep 20\necho slow end\n";
const FAIL = "#!/bin/sh\necho \"setup in $(basename \"$PWD\")\"\necho \"setup broke\" >&2\nexit 3\n";
const APPROVED = "#!/bin/sh\necho approved\n";
const DIALOG = ".swal2-popup:not(.swal2-toast)";
const SCRIPT_SWITCH = '[data-assistant-approval="worktree-post-script"] input[type="checkbox"]';
const PYTHON = `#!/usr/bin/env python3
import os
print("python in", os.path.basename(os.getcwd()), "for", os.environ["DC_WORKTREE_PROJECT"])
`;

function git(args, cwd = REPO) {
  return execFileSync("git", ["-c", "user.name=e2e", "-c", "user.email=e2e@example.com", "-c", "commit.gpgsign=false", ...args], { cwd, encoding: "utf8" });
}

function buildFixture() {
  fs.mkdirSync(REPO, { recursive: true });
  git(["init", "-q", "-b", "master"]);
  fs.writeFileSync(path.join(REPO, ".gitignore"), ".setup-ran\n");
  git(["add", "-A"]);
  git(["commit", "-qm", "init"]);
  git(["branch", "feature"]);
}

function cockpit(args, input = "") {
  return new Promise((resolve) => {
    const child = spawn(BIN, ["assistant", "--state-dir", STATE, "--projects-dir", PROJECTS, ...args], {
      env: { ...process.env, HOME: path.join(AUX, "home") },
    });
    child.stdin.end(input);
    let stdout = "", stderr = "";
    child.stdout.on("data", (d) => { stdout += d; });
    child.stderr.on("data", (d) => { stderr += d; });
    child.on("close", (code) => resolve({ code, stdout, stderr }));
  });
}

function storedScript() {
  try { return fs.readFileSync(SCRIPT_FILE, "utf8"); } catch { return null; }
}

// worktreeNews lists the notifications of one worktree project.
function worktreeNews(name) {
  let list = [];
  try { list = JSON.parse(fs.readFileSync(path.join(STATE, "notifications.json"), "utf8")); } catch {}
  return list.filter((n) => n.targetId === `worktree:${name}`);
}

L.runFeature("PROJECT WORKTREES", async ({ page, run }) => {
  buildFixture();
  let assistantID = "";
  const approvalNote = async (id, verdict, count) => {
    await page.goto(`${BASE}/assistants/${id}`, { waitUntil: "domcontentloaded" });
    const notes = page.locator(`[data-assistant-note="approval"][data-assistant-approval-note="${verdict}"]`);
    for (let i = 0; i < 60 && (await notes.count()) < count; i += 1) await sleep(250);
    assert((await notes.count()) >= count, `want ${count} ${verdict} approval notes`);
  };
  const SETTINGS = `${BASE}/settings/projects/worktrees`;
  const saveScript = async (content) => {
    await page.goto(SETTINGS, { waitUntil: "domcontentloaded" });
    await page.fill("#post-script", content);
    await Promise.all([
      page.waitForResponse((r) => r.url().endsWith("/settings/projects/worktrees") && r.request().method() === "POST", { timeout: 15000 }),
      page.click('#settings-projects-worktrees button[type="submit"]'),
    ]);
    await page.waitForSelector(".alert", { state: "visible", timeout: 8000 });
    return (await page.textContent(".alert")).trim();
  };
  const createInForm = async (branch) => {
    await page.goto(`${BASE}/projects/new?create=worktree%3A${MAIN}`, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    if (branch !== "feature") {
      await page.waitForSelector('select[name="branch_mode"]', { timeout: 8000 });
      await page.selectOption('select[name="branch_mode"]', "new");
      await page.fill('input[name="new_branch"]', branch);
    }
    await page.waitForFunction((n) => document.querySelector('input[name="project_name"]')?.value === n, `${MAIN}-${branch}`, { timeout: 8000 });
    const name = `${MAIN}-${branch}`;
    await Promise.all([
      page.waitForURL(new RegExp(`/projects#project-${name}$`), { timeout: 30000 }),
      page.click('form[action^="/projects"] button[type="submit"]'),
    ]);
    await page.waitForFunction((n) => document.getElementById(`project-${n}`)?.classList.contains("project-row-flash"), name, { timeout: 15000, polling: "raf" });
    const alerts = await page.evaluate(() => [...document.querySelectorAll(".alert")].filter((a) => a.getClientRects().length > 0).map((a) => a.innerText));
    assert(alerts.length === 0, `a flash came with the create: ${JSON.stringify(alerts)}`);
  };
  const setupRan = (branch) => {
    try { return fs.readFileSync(path.join(PROJECTS, `${MAIN}-${branch}`, ".setup-ran"), "utf8"); } catch { return null; }
  };

  await run("settings: Projects in the nav leads to the Worktrees tab, empty on a fresh instance", async () => {
    await page.goto(`${BASE}/settings/general`, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    await page.click('[data-settings-nav] a:has-text("Projects")');
    await page.waitForURL(/\/settings\/projects\/worktrees$/, { timeout: 10000 });
    assert(await page.isVisible("#post-script"), "the script field is not visible");
    assert((await page.inputValue("#post-script")) === "", "a fresh instance already carries a script");
    assert(storedScript() === null, "a fresh instance already has the file");
    assert((await page.innerText("#settings-projects-worktrees")).includes("for up to 15s."), "the page does not name the 15s bound");
  });

  await run("settings: a bash script saves as 0700 with LF endings and survives a reload", async () => {
    const saved = await saveScript(BASH);
    assert(saved.includes("Settings saved."), `the save answered ${saved}`);
    assert(storedScript() === BASH, `the file holds ${JSON.stringify(storedScript())}`);
    assert((fs.statSync(SCRIPT_FILE).mode & 0o777) === 0o700, `mode ${(fs.statSync(SCRIPT_FILE).mode & 0o777).toString(8)}`);
    await page.reload({ waitUntil: "domcontentloaded" });
    assert((await page.inputValue("#post-script")) === BASH, "the script did not survive a reload");
  });

  await run("settings: a script without #! is refused and keeps what was typed", async () => {
    const refused = await saveScript("echo no interpreter");
    assert(refused.includes("not saved") && refused.includes("#!/bin/bash"), `the refusal reads ${refused}`);
    assert((await page.inputValue("#post-script")) === "echo no interpreter", "the typed script was lost");
    assert(storedScript() === BASH, "a refused script replaced the stored one");
  });

  await run("assistant: show prints the script, set waits for the approval with the new content, approve saves it", async () => {
    const created = await page.request.post(`${BASE}/assistants/new`, {
      form: { csrf_token: await page.evaluate(() => document.querySelector('meta[name="csrf-token"]').content), form: "new", coder: "claude" },
      headers: { Accept: "application/json" },
    });
    assistantID = (await created.json().catch(() => ({}))).id || "";
    assert(assistantID, `no assistant for the checks: ${created.status()}`);
    const shown = await cockpit(["--as", assistantID, "project-worktree-script-show"]);
    assert(shown.code === 0 && shown.stdout === BASH + SUMMARY, `show printed ${shown.code} ${JSON.stringify(shown.stdout)}`);
    const file = path.join(AUX, "approved.sh");
    fs.writeFileSync(file, APPROVED.replace(/\n/g, "\r\n"));
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    const set = await cockpit(["--as", assistantID, "project-worktree-script-set", file]);
    assert(set.code === 0 && set.stdout === "Set the worktree post script waits for the user's approval. A note lands in your thread once they decide; do not run it again.\n", `set printed ${set.code} ${set.stdout}${set.stderr}`);
    assert(storedScript() === BASH, "a waiting write changed the script");
    await page.waitForSelector(`${DIALOG} [data-approval-details]`, { state: "visible", timeout: 15000 });
    const shownScript = await page.locator(`${DIALOG} [data-approval-details] dd`).last().innerText();
    assert(shownScript.trim() === APPROVED.trim(), `the dialog shows ${JSON.stringify(shownScript)}`);
    await page.click(`${DIALOG} .swal2-confirm`);
    for (let i = 0; i < 40 && storedScript() !== APPROVED; i += 1) await sleep(250);
    assert(storedScript() === APPROVED, `the approved write left ${JSON.stringify(storedScript())}`);
    assert((fs.statSync(SCRIPT_FILE).mode & 0o777) === 0o700, "the approved script is not 0700");
    await approvalNote(assistantID, "done", 1);
  });

  await run("assistant: a denied empty set deletes nothing, a script without #! is refused at once", async () => {
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    const set = await cockpit(["--as", assistantID, "project-worktree-script-set"], "");
    assert(set.code === 0 && set.stdout.startsWith("Delete the worktree post script waits"), `set printed ${set.stdout}${set.stderr}`);
    await page.waitForSelector(`${DIALOG} .swal2-deny`, { state: "visible", timeout: 15000 });
    await page.click(`${DIALOG} .swal2-deny`);
    await approvalNote(assistantID, "declined", 1);
    assert(storedScript() === APPROVED, "a denied delete changed the script");
    const refused = await cockpit(["--as", assistantID, "project-worktree-script-set"], "echo no interpreter\n");
    assert(refused.code !== 0 && refused.stderr.includes("#!/bin/bash"), `a script without #! answered ${refused.code} ${refused.stderr}`);
  });

  await run("assistant: with the approval off set writes at once and empty deletes", async () => {
    const toggle = async (on) => {
      await page.goto(`${BASE}/settings/assistant/approvals`, { waitUntil: "domcontentloaded" });
      await page.locator(SCRIPT_SWITCH).setChecked(on);
      await Promise.all([page.waitForNavigation({ waitUntil: "domcontentloaded" }), page.click('#settings-assistant-approvals button[type="submit"]')]);
      assert((await page.locator(SCRIPT_SWITCH).isChecked()) === on, "the switch did not move");
    };
    await page.goto(`${BASE}/settings/assistant/approvals`, { waitUntil: "domcontentloaded" });
    assert(await page.locator(SCRIPT_SWITCH).isChecked(), "the approval is not on by default");
    await toggle(false);
    const deleted = await cockpit(["--as", assistantID, "project-worktree-script-set"], "");
    assert(deleted.code === 0 && deleted.stdout.includes("deleted") && storedScript() === null, `the empty set printed ${deleted.stdout}${deleted.stderr}, left ${JSON.stringify(storedScript())}`);
    const none = await cockpit(["--as", assistantID, "project-worktree-script-show"]);
    assert(none.stdout === "there is no worktree post script\n" + SUMMARY, `show printed ${none.stdout}`);
    const saved = await cockpit(["--as", assistantID, "project-worktree-script-set"], BASH);
    assert(saved.code === 0 && saved.stdout.includes("saved") && storedScript() === BASH, `the set printed ${saved.stdout}${saved.stderr}`);
    await toggle(true);
  });

  await run("form: <project>-<branch>, the script runs inside with its variables, no notification", async () => {
    await createInForm("feature");
    const wt = path.join(PROJECTS, `${MAIN}-feature`);
    const ran = setupRan("feature");
    assert(ran === `vars ${MAIN}|${MAIN}|${REPO}|${MAIN}-feature|${wt}|feature\n`, `the script did not run inside the worktree with its variables: ${JSON.stringify(ran)}`);
    assert(!fs.existsSync(path.join(REPO, ".setup-ran")), "the script ran in the main repository");
    assert(worktreeNews(`${MAIN}-feature`).length === 0, "a passing script notified");
  });

  await run("form: a CRLF script posted raw still runs", async () => {
    const status = await page.evaluate(async (script) => {
      const body = new URLSearchParams({ csrf_token: document.querySelector('meta[name="csrf-token"]').content, post_script: script });
      return (await fetch("/settings/projects/worktrees", { method: "POST", body })).status;
    }, BASH.replace(/\n/g, "\r\n"));
    assert(status === 200, `the save answered ${status}`);
    assert(storedScript() === BASH, `CRLF was not normalized: ${JSON.stringify(storedScript())}`);
    await createInForm("crlf");
    assert(setupRan("crlf")?.includes(`|${MAIN}-crlf|`), `the CRLF script did not run: ${JSON.stringify(setupRan("crlf"))}`);
  });

  await run("form: a failing script leaves the worktree and notifies once, opening the project reads it", async () => {
    await saveScript(FAIL);
    await createInForm("uifail");
    assert(fs.existsSync(path.join(PROJECTS, `${MAIN}-uifail`, ".git")), "the failed script took the worktree with it");
    const news = worktreeNews(`${MAIN}-uifail`);
    assert(news.length === 1, `want one notification, got ${JSON.stringify(news)}`);
    assert(news[0].title === "Post script failed." && news[0].detail.startsWith(`${MAIN}-uifail: exit status 3`) && news[0].detail.includes("setup broke"), `the notification reads ${JSON.stringify(news[0])}`);
    assert(!news[0].read, "the notification was born read");
    await page.goto(`${BASE}/projects/${MAIN}-uifail/editor`, { waitUntil: "domcontentloaded" });
    assert(worktreeNews(`${MAIN}-uifail`)[0]?.read === true, "opening the project left its notification unread");
  });

  await run("form: a failing script from the page and the dialog, deleting the project reads its notification", async () => {
    await createInForm("pagefail");
    const modal = await page.evaluate(async (m) => {
      const body = new URLSearchParams({ csrf_token: document.querySelector('meta[name="csrf-token"]').content, create: `worktree:${m}`, branch_mode: "new", new_branch: "modalfail", start: "master", project_name: `${m}-modalfail` });
      const r = await fetch("/projects?modal=1", { method: "POST", body, headers: { Accept: "application/json" } });
      return { status: r.status, body: await r.text() };
    }, MAIN);
    assert(modal.status === 200 && modal.body.includes(`#project-${MAIN}-modalfail`), `the dialog's create answered ${modal.status}: ${modal.body}`);
    for (const name of ["pagefail", "modalfail", "crlf"]) {
      await L.deleteProject(page, `${MAIN}-${name}`);
      for (let i = 0; i < 40 && fs.existsSync(path.join(PROJECTS, `${MAIN}-${name}`)); i += 1) await sleep(250);
      assert(!fs.existsSync(path.join(PROJECTS, `${MAIN}-${name}`)), `${MAIN}-${name} could not be deleted`);
    }
    for (const name of ["pagefail", "modalfail"]) {
      let news = worktreeNews(`${MAIN}-${name}`);
      for (let i = 0; i < 20 && !news[0]?.read; i += 1) { await sleep(250); news = worktreeNews(`${MAIN}-${name}`); }
      assert(news.length === 1 && news[0].read, `deleting ${MAIN}-${name} left ${JSON.stringify(news)}`);
    }
  });

  await run("cli: project-worktree-new prints the failing output, exits 1 and notifies once", async () => {
    await saveScript(FAIL);
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    const failed = await cockpit(["project-worktree-new", MAIN, "clifail", "--from", "master"]);
    assert(failed.code === 1, `a failed script exited ${failed.code}: ${failed.stdout}${failed.stderr}`);
    assert(failed.stdout.includes(`project ${MAIN}-clifail created at ${PROJECTS}/${MAIN}-clifail, a worktree of ${MAIN} on branch clifail\n`), `stdout: ${failed.stdout}`);
    assert(failed.stdout.includes("post script failed: exit status 3\n"), `stdout: ${failed.stdout}`);
    assert(failed.stdout.includes(`--- output\nsetup in ${MAIN}-clifail\nsetup broke\n`), `stdout: ${failed.stdout}`);
    assert(failed.stderr.includes("the post script failed, the worktree project stands"), `stderr: ${failed.stderr}`);
    assert(worktreeNews(`${MAIN}-clifail`).length === 1, "the CLI's failure did not notify once");
    await page.waitForSelector(`#project-${MAIN}-clifail`, { state: "attached", timeout: 10000 });
  });

  await run("cli: a script sleeping 20s is stopped at about 15s, leaves the worktree and notifies once", async () => {
    await saveScript(SLOW);
    const started = Date.now();
    const slow = await cockpit(["project-worktree-new", MAIN, "clislow", "--from", "master"]);
    const took = (Date.now() - started) / 1000;
    assert(took >= 15 && took < 19, `the create took ${took}s`);
    assert(slow.code === 1, `a stopped script exited ${slow.code}: ${slow.stdout}${slow.stderr}`);
    assert(slow.stdout.includes("post script failed: stopped after 15s\n") && slow.stdout.includes("slow start\n") && !slow.stdout.includes("slow end"), `stdout: ${slow.stdout}`);
    assert(fs.existsSync(path.join(PROJECTS, `${MAIN}-clislow`, ".git")), "the stopped script took the worktree with it");
    const news = worktreeNews(`${MAIN}-clislow`);
    assert(news.length === 1 && news[0].detail.startsWith(`${MAIN}-clislow: stopped after 15s`), `want one notification, got ${JSON.stringify(news)}`);
    return `${took.toFixed(1)}s`;
  });

  await run("cli: a python script runs through its own interpreter", async () => {
    await saveScript(PYTHON);
    const made = await cockpit(["project-worktree-new", MAIN, "cli", "--from", "master"]);
    assert(made.code === 0, `exit ${made.code}: ${made.stdout}${made.stderr}`);
    assert(made.stdout.includes("post script ran\n") && made.stdout.includes(`python in ${MAIN}-cli for ${MAIN}-cli\n`), `stdout: ${made.stdout}`);
    assert(worktreeNews(`${MAIN}-cli`).length === 0, "a passing script notified");
  });

  await run("settings: an empty save deletes the script, a new worktree runs nothing", async () => {
    const saved = await saveScript("");
    assert(saved.includes("Settings saved."), `the save answered ${saved}`);
    assert(storedScript() === null, "an empty save left the file");
    const made = await cockpit(["project-worktree-new", MAIN, "plain", "--from", "master"]);
    assert(made.code === 0 && !made.stdout.includes("post script"), `stdout: ${made.stdout}${made.stderr}`);
  });

  await run("cli: a taken branch is refused in git's words and nothing is made", async () => {
    const taken = await cockpit(["project-worktree-new", MAIN, "feature", "--name", `${MAIN}-twice`]);
    assert(taken.code !== 0, `a taken branch exited 0: ${taken.stdout}`);
    assert(!fs.existsSync(path.join(PROJECTS, `${MAIN}-twice`)), "a refused create left a directory");
    return taken.stderr.trim();
  });

  await run("cli: project-worktree-list and status name the worktree projects", async () => {
    const list = await cockpit(["project-worktree-list", MAIN]);
    assert(list.code === 0, `exit ${list.code}: ${list.stderr}`);
    assert(list.stdout.startsWith(`Worktrees of ${MAIN} (6)\n`), `list: ${list.stdout}`);
    for (const name of ["feature", "uifail", "clifail", "clislow", "cli", "plain"]) {
      assert(list.stdout.includes(`  ${MAIN}-${name} (${name}) ${PROJECTS}/${MAIN}-${name}\n`), `list misses ${name}: ${list.stdout}`);
    }
    const fromWorktree = await cockpit(["project-worktree-list", `${MAIN}-cli`]);
    assert(fromWorktree.stdout.startsWith(`${MAIN}-cli is a worktree of ${MAIN}\nWorktrees of ${MAIN} (6)\n`), `list from a worktree: ${fromWorktree.stdout}`);
    const status = await cockpit(["status"]);
    assert(status.code === 0, `status exit ${status.code}: ${status.stderr}`);
    assert(status.stdout.includes(`  ${MAIN} (master)\n`), `status lists the main as a worktree: ${status.stdout}`);
    assert(status.stdout.includes(`  ${MAIN}-cli (cli) worktree of ${MAIN}\n`), `status: ${status.stdout}`);
  });

  await run("delete: the main takes its worktrees with it", async () => {
    await L.deleteProject(page, MAIN);
    for (let i = 0; i < 40 && fs.existsSync(REPO); i += 1) await sleep(250);
    assert(!fs.existsSync(REPO), "the main repository is still there");
    for (const name of ["feature", "uifail", "clifail", "clislow", "cli", "plain"]) {
      assert(!fs.existsSync(path.join(PROJECTS, `${MAIN}-${name}`)), `${MAIN}-${name} survived the cascade`);
    }
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    assert((await page.locator(`[data-project-name^="${MAIN}"]`).count()) === 0, "a row of the deleted projects is still listed");
  });
});
