const L = require("./lib");
const { assert, sleep } = L;
const { remoteOnly } = require("./editor-git-compare");

// Not a runner of its own: editor-git.js runs it over its scratch repository
// after the comparison checks, on the origin remoteOnly sets up there.
// switchChecks covers Switch branch in the git sheet on a branch only origin
// holds: the row says which local branch the pick checks out, the pick posts
// that local name, and git creates it on the way as the branch tracking
// origin/<branch>. The way back to the starting branch is the same picker,
// never the shell: the poller still holds the starting state, so a switch
// back within one round reads as no move and the editor never follows. The
// shell then removes the branch, the remote and its bare repository. A name
// typed while the picker still fetches the remotes narrows the list the fetch
// ends in, a field emptied again gets the whole list; `page.route` holds the
// fetch while the field is typed into.

async function switchChecks({ page, run, editorBase, runInShell, openEditor, diffReady, openGitSheet, waitBranch, h }) {
  const branch = "switch-remote-only";
  const headOf = () => page.evaluate((b) =>
    fetch(`${b}/git/changes`, { headers: { Accept: "application/json" } }).then((r) => r.json()).then((c) => c.branch), editorBase);
  const refsOf = (q) => page.evaluate(([b, text]) =>
    fetch(`${b}/git/refs?kinds=branch,remote&q=${encodeURIComponent(text)}`, { headers: { Accept: "application/json" } })
      .then((r) => r.json()).then((data) => (data.refs || []).map((ref) => ref.name)), [editorBase, q]);
  const switchRow = async (q) => {
    await openGitSheet(page);
    await page.click('[data-editor-sheet-body] .dropdown-item[title="Switch branch"]');
    await page.waitForSelector("[data-editor-sheet-body] input", { timeout: 8000 });
    await page.waitForSelector("[data-picker-loading]", { state: "hidden", timeout: 20000 });
    await page.fill("[data-editor-sheet-body] input", q);
    await page.waitForSelector("[data-picker-loading]", { state: "hidden", timeout: 20000 });
    return page.locator(`[data-editor-sheet] .editor-sheet-row:has(.editor-sheet-name:text-is("${q}"))`);
  };

  await run("switch: a remote only branch picked in Switch branch checks out the local branch tracking it, its row naming that branch", async () => {
    const start = await headOf();
    assert(start.name && !start.detached, `HEAD stands on no branch: ${JSON.stringify(start)}`);
    const { name, drop } = await remoteOnly({ runInShell, h }, branch);

    await openEditor(page);
    await diffReady(page);
    const row = await switchRow(name);
    const sub = (await row.locator(".editor-sheet-dir").textContent()).trim();
    assert(sub === `remote, checks out ${branch}`, `the row reads "${sub}"`);
    const asked = page.waitForRequest((r) => r.method() === "POST" && r.url().endsWith("/git/checkout"), { timeout: 10000 });
    await row.locator(".editor-sheet-open").click();
    const sent = (await asked).postDataJSON();
    assert(sent.branch === branch, `the pick asked to check out ${JSON.stringify(sent)}`);
    await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 30000 });
    await waitBranch(page, branch);
    const head = await headOf();
    assert(head.name === branch && !head.detached && head.upstream === name, `HEAD stands ${JSON.stringify(head)}, not on ${branch} tracking ${name}`);

    await (await switchRow(start.name)).locator(".editor-sheet-open").click();
    await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 30000 });
    await waitBranch(page, start.name);
    assert(await runInShell(`git branch -qD ${branch}\r`) === 200, "the shell refused the cleanup");
    await drop();
    const deadline = Date.now() + 20000;
    let left = await refsOf(branch);
    while (left.length && Date.now() < deadline) {
      await sleep(300);
      left = await refsOf(branch);
    }
    assert(!left.length, `the cleanup left ${JSON.stringify(left)}`);
    return `${name} reads "${sub}", posts ${branch}, HEAD on ${branch} tracking ${head.upstream}, back on ${start.name}`;
  });

  await run("switch: a name typed while Switch branch still fetches narrows the list the fetch ends in, an emptied field gets the whole list", async () => {
    const typed = "switch-typed";
    const start = await headOf();
    assert(await runInShell(`git branch ${typed}\r`) === 200, "the shell refused the branch");
    const names = () => page.locator("[data-editor-sheet] .editor-sheet-row .editor-sheet-name").allTextContents();
    const refsRound = (q) => page.waitForResponse((r) => r.url().includes("/git/refs?")
      && (q === undefined || (new URL(r.url()).searchParams.get("q") || "") === q), { timeout: 20000 });
    const heldOpen = async (text) => {
      let release = null;
      const held = new Promise((resolve) => { release = resolve; });
      let caught = null;
      const asked = new Promise((resolve) => { caught = resolve; });
      await page.route("**/git/fetch", async (route) => {
        caught();
        await held;
        await route.continue().catch(() => {});
      });
      try {
        await openGitSheet(page);
        await page.click('[data-editor-sheet-body] .dropdown-item[title="Switch branch"]');
        await page.waitForSelector("[data-editor-sheet-body] input", { timeout: 8000 });
        await asked;
        for (const step of text) {
          const answered = refsRound(step);
          await page.fill("[data-editor-sheet-body] input", step);
          await answered;
        }
        const after = refsRound();
        release();
        await after;
      } finally {
        release();
        await page.unroute("**/git/fetch");
      }
      await page.waitForSelector("[data-picker-loading]", { state: "hidden", timeout: 20000 });
      const shown = await names();
      await page.keyboard.press("Escape");
      await page.keyboard.press("Escape");
      await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 6000 });
      return shown;
    };

    await openEditor(page);
    await diffReady(page);
    const narrowed = await heldOpen([typed]);
    const whole = await heldOpen([typed, ""]);
    assert(await runInShell(`git branch -qD ${typed}\r`) === 200, "the shell refused the cleanup");
    assert(narrowed.length && narrowed.every((n) => n.includes(typed)), `the field holds "${typed}" and the list reads ${JSON.stringify(narrowed)}`);
    assert(whole.includes(start.name) && whole.includes(typed), `the emptied field shows ${JSON.stringify(whole)}`);
    return `typed during the fetch: ${JSON.stringify(narrowed)}, emptied: ${whole.length} rows`;
  });
}

module.exports = { switchChecks };
