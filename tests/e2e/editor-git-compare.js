const L = require("./lib");
const { assert, sleep } = L;

// Not a runner of its own: editor-git.js runs these over its scratch
// repository. compareHelpers is the toolkit of its revision comparison
// section, whose working copy checks live in editor-git-compare-worktree.js.
// commitChecks covers the two entries a commit's menu carries in the git
// sheet's history, in a file's history and in the blame gutter's line menu:
// "Compare with its parent" (dead on a root commit) and "Compare with the
// working copy" close the sheet and open the panel on that pair, the side
// buttons naming it, the device storing it, and a standing panel asking
// git/compare again for it. resetChecks covers the way back: the reset
// button beside the select stands only off the suggested pair, its click
// brings the suggestion back with the working copy and the split question,
// and an unpicked left side asks for the suggestion on every load instead of
// storing the name it resolved to, so a new tag moves it. remoteChecks picks a
// remote only branch as the left side: the comparison asks for origin/<branch>,
// never the local name a checkout would create, and the row says remote with
// no checkout. remoteOnly is that fixture, an origin holding one branch the
// working copy does not, which editor-git-switch.js borrows. Gotchas:
//   - it runs after the blame checks and leaves their state as it found it:
//     the blamed file the active tab with its gutter on and the panel closed,
//     on the phone too, whose later checks expect the tree in the drawer.
//   - a standing panel loads again on the pick, so the wait for the answer
//     is the request carrying the new pair, armed before the click; the
//     summary alone could still be the old answer.
//   - a new tag moves no git event, HEAD and the status stay, so the check
//     waits for the server's suggestion to name it and reloads the page.

function compareHelpers({ page, editorBase, project, tag, openGitSheet, sheetAction }) {
  const cmpClone = `/tmp/zzgit-revcmp-${tag}`;
  const revdiffKey = `dc-editor-revdiff:${project}`;
  const compareJSON = (from, to, mode) => page.evaluate(([b, f, t, m]) =>
    fetch(`${b}/git/compare?from=${encodeURIComponent(f)}&to=${encodeURIComponent(t)}&mode=${m}`, { headers: { Accept: "application/json" } })
      .then((r) => r.json().then((data) => ({ status: r.status, data }))), [editorBase, from, to, mode]);
  const waitCompare = async (from, to, mode, want) => {
    const deadline = Date.now() + 180000;
    let last = null;
    while (Date.now() < deadline) {
      last = await compareJSON(from, to, mode).catch(() => null);
      if (last && last.status === 200 && (last.data.files || []).length === want) return last.data;
      await sleep(1000);
    }
    throw new Error(`the comparison ${from}..${to} never answered ${want} files: ${JSON.stringify(last && { status: last.status, files: (last.data.files || []).length, error: last.data.error })}`);
  };
  const openRevdiff = async (target) => {
    await openGitSheet(target);
    await sheetAction(target, /^Compare/).first().click();
    await target.waitForSelector("[data-editor-revdiff]:not([hidden])", { timeout: 10000 });
  };
  const revdiffSettled = (target) => target.waitForFunction(() => {
    const el = document.querySelector("[data-editor-revdiff-summary]");
    const error = document.querySelector("[data-editor-revdiff-error]");
    const text = el ? el.textContent.trim() : "";
    return (text !== "" && !/^Comparing/.test(text)) || (error && !error.hidden);
  }, null, { timeout: 90000 });
  const revdiffSummary = (target) => target.evaluate(() => document.querySelector("[data-editor-revdiff-summary]").textContent.trim());
  const revdiffNames = (target) => target.evaluate(() => ({
    from: document.querySelector('[data-editor-revdiff-name="from"]').textContent.trim(),
    to: document.querySelector('[data-editor-revdiff-name="to"]').textContent.trim(),
  }));
  const revdiffOption = (target) => target.evaluate(() => {
    const el = document.querySelector("[data-editor-revdiff-mode]");
    return { value: el.value, text: el.options[el.selectedIndex].textContent };
  });
  const revdiffRows = (target) => target.evaluate(() => [...document.querySelectorAll("[data-editor-revdiff-list] .editor-commit-row[data-path]")]
    .map((row) => ({
      path: row.dataset.path,
      mark: row.querySelector(".editor-commit-line > span").textContent.trim(),
      sub: (row.querySelector(".editor-commit-path") || { textContent: "" }).textContent.trim(),
    })));
  const openSidePicker = async (target, side) => {
    await target.click(`[data-editor-revdiff-pick="${side}"]`);
    await target.waitForSelector("[data-editor-sheet-body] input", { timeout: 8000 });
  };
  const pickSide = async (target, side, name) => {
    await openSidePicker(target, side);
    await target.fill("[data-editor-sheet-body] input", name);
    await target.waitForSelector("[data-picker-loading]", { state: "hidden", timeout: 20000 });
    await target.click(`[data-editor-sheet] .editor-sheet-row:has(.editor-sheet-name:text-is("${name}")) .editor-sheet-open`);
    await target.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 6000 });
    await revdiffSettled(target);
  };
  const typeSide = async (target, side, name) => {
    await openSidePicker(target, side);
    await target.fill("[data-editor-sheet-body] input", name);
    await target.keyboard.press("Enter");
    await target.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 6000 });
    await revdiffSettled(target);
  };
  const mergeSides = (target) => target.evaluate(() => [...document.querySelectorAll(".cm-mergeView .cm-content")].map((content) => ({
    editable: content.getAttribute("contenteditable"),
    text: [...content.querySelectorAll(".cm-line")].map((line) => line.textContent).join("\n").trim(),
    changed: [...content.querySelectorAll(".cm-changedLine")].map((line) => line.textContent),
  })));
  const compareBar = (target) => target.evaluate(() => {
    const box = (el) => {
      const rect = el.getBoundingClientRect();
      return { display: getComputedStyle(el).display, width: rect.width };
    };
    return {
      left: document.querySelector('[data-editor-compare-name="left"]').textContent.trim(),
      right: document.querySelector('[data-editor-compare-name="right"]').textContent.trim(),
      saveLeft: box(document.querySelector('[data-editor-compare-save="left"]')),
      saveRight: box(document.querySelector('[data-editor-compare-save="right"]')),
    };
  });
  const closeRevdiffTabs = async (target) => {
    while (await target.locator(".editor-tab[data-path^='//revdiff/']").count() > 0) {
      await target.locator(".editor-tab[data-path^='//revdiff/'] .editor-tab-state").first().click();
      await sleep(200);
    }
  };
  return { cmpClone, revdiffKey, compareJSON, waitCompare, openRevdiff, revdiffSettled, revdiffSummary, revdiffNames, revdiffOption, revdiffRows, openSidePicker, pickSide, typeSide, mergeSides, compareBar, closeRevdiffTabs };
}

async function commitChecks({ page, run, mobile, editorBase, blamed, openEditor, diffReady, openGitSheet, openLineMenu, h }) {
  const logOf = (path) => page.evaluate(([b, p]) =>
    fetch(`${b}/git/log${p ? `?path=${encodeURIComponent(p)}` : ""}`, { headers: { Accept: "application/json" } }).then((r) => r.json()), [editorBase, path]);
  const compareEntries = (target) => target.$$eval(".dc-context-menu .dropdown-item", (els) => Object.fromEntries(els
    .map((el) => [el.textContent.trim().replace(/\s+/g, " "), el.disabled])
    .filter(([text]) => text.startsWith("Compare with "))));
  const openCellMenu = async (target, sha) => {
    const cell = target.locator(`[data-editor-sheet-body] [data-git-commit="${sha}"]`);
    await cell.waitFor({ timeout: 15000 });
    await cell.click();
    await target.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
    return compareEntries(target);
  };
  const comparedOn = async (target, label, from, to) => {
    const worktree = to === "";
    const asked = target.waitForRequest((r) => {
      const q = new URL(r.url()).searchParams;
      return r.url().includes("/git/compare?") && q.get("from") === from && (worktree ? q.get("worktree") === "1" : q.get("to") === to);
    }, { timeout: 10000 });
    await target.locator(".dc-context-menu .dropdown-item", { hasText: label }).first().click();
    await asked;
    await target.waitForSelector("[data-editor-revdiff]", { state: "visible", timeout: 10000 });
    await h.revdiffSettled(target);
    const names = await h.revdiffNames(target);
    const want = { from: from.slice(0, 7), to: worktree ? "Working copy" : to.slice(0, 7) };
    assert(names.from === want.from && names.to === want.to, `${label} names ${JSON.stringify(names)}, not ${JSON.stringify(want)}`);
    assert(!(await target.locator("[data-editor-sheet]").isVisible()), `${label} left the sheet standing`);
    const stored = await target.evaluate((key) => JSON.parse(localStorage.getItem(key) || "null"), h.revdiffKey);
    assert(stored && stored.from === from && stored.to === to && stored.worktree === worktree, `${label} stored ${JSON.stringify(stored)}`);
  };
  const closePanel = async (target) => {
    await target.click("[data-editor-revdiff-close]");
    await target.waitForSelector("[data-editor-revdiff]", { state: "hidden", timeout: 8000 });
  };

  await run("compare: a commit in a file's history and in the git sheet's history compares with its parent or the working copy, the parent dead on a root commit", async () => {
    const file = (await logOf("root.txt")).commits;
    const root = file[file.length - 1];
    const head = (await logOf("")).commits[0];
    assert(root && !root.parent && head.parent, `root.txt's history ${JSON.stringify(file.map((c) => [c.short, c.parent]))}, the newest commit's parent ${head.parent}`);
    await page.click('.editor-item[data-path="root.txt"]');
    await page.click('.editor-tab[data-path="root.txt"]', { button: "right" });
    await page.locator(".dc-context-menu .dropdown-item", { hasText: "File history" }).first().click();
    let entries = await openCellMenu(page, root.sha);
    assert(entries["Compare with its parent"] === true && entries["Compare with the working copy"] === false, `a root commit offers ${JSON.stringify(entries)}`);
    await comparedOn(page, "Compare with the working copy", root.sha, "");

    await openGitSheet(page);
    entries = await openCellMenu(page, head.sha);
    assert(entries["Compare with its parent"] === false && entries["Compare with the working copy"] === false, `the history offers ${JSON.stringify(entries)}`);
    await comparedOn(page, "Compare with its parent", head.parent, head.sha);
    await openGitSheet(page);
    await openCellMenu(page, head.sha);
    await comparedOn(page, "Compare with the working copy", head.sha, "");
    await closePanel(page);

    const mp = await mobile();
    await openEditor(mp);
    await diffReady(mp);
    await mp.keyboard.press("Escape");
    await mp.waitForSelector("[data-editor-backdrop]", { state: "hidden", timeout: 5000 });
    await openGitSheet(mp);
    await openCellMenu(mp, head.sha);
    await comparedOn(mp, "Compare with the working copy", head.sha, "");
    assert(await mp.locator(".editor-drawer-open [data-editor-revdiff]").isVisible(), "the phone's panel does not stand in the drawer");
    await closePanel(mp);
    return "file history and git sheet history, root commit dead, a standing panel asking again, the phone's drawer";
  });

  await run("compare: a line's commit in the blame gutter compares with its parent or the working copy", async () => {
    const blame = await page.evaluate(([b, f]) =>
      fetch(`${b}/git/blame?path=${f}`, { headers: { Accept: "application/json" } }).then((r) => r.json()), [editorBase, blamed]);
    const al = blame.commits.find((c) => c.author === "Al");
    assert(al && al.parent, `Al's commit carries no parent: ${JSON.stringify(al)}`);
    await page.click(`.editor-tab[data-path="${blamed}"]`);
    await page.waitForSelector(".cm-blame", { timeout: 20000 });
    const labels = await openLineMenu(1);
    const live = labels.filter((l) => /^Compare with /.test(l.text) && !l.disabled).map((l) => l.text);
    assert(live.length === 2 && !labels.some((l) => l.text === "Compare"), `the line menu offers ${JSON.stringify(labels.map((l) => l.text))}`);
    await comparedOn(page, "Compare with its parent", al.parent, al.sha);
    const changed = (await h.compareJSON(al.parent, al.sha, "direct")).data.files.map((f) => f.path).sort();
    const rows = (await h.revdiffRows(page)).map((r) => r.path).sort();
    assert(changed.includes(blamed) && JSON.stringify(rows) === JSON.stringify(changed), `the panel lists ${JSON.stringify(rows)}, the commit changed ${JSON.stringify(changed)}`);
    await openLineMenu(1);
    await comparedOn(page, "Compare with the working copy", al.sha, "");
    await closePanel(page);
    return `${al.short}^ against ${al.short} listing ${rows.length} ${rows.length === 1 ? "file" : "files"}, then ${al.short} against the working copy`;
  });
}

async function resetChecks({ page, run, runInShell, openEditor, diffReady, h }) {
  const resetShown = () => page.evaluate(() => {
    const el = document.querySelector("[data-editor-revdiff-reset]");
    const rect = el.getBoundingClientRect();
    return getComputedStyle(el).display !== "none" && rect.width > 0 && rect.height > 0;
  });
  const stored = () => page.evaluate((key) => JSON.parse(localStorage.getItem(key) || "null"), h.revdiffKey);
  const suggestion = async () => (await h.compareJSON("", "", "since")).data.from.name;
  const suggests = async (want) => {
    const deadline = Date.now() + 20000;
    let got = "";
    while (Date.now() < deadline) {
      got = await suggestion();
      if (got === want) return;
      await sleep(300);
    }
    throw new Error(`the repository suggests ${got}, not ${want}`);
  };
  const reads = async (from, why) => {
    const names = await h.revdiffNames(page);
    const option = await h.revdiffOption(page);
    assert(names.from === from && names.to === "Working copy" && option.value === "since" && option.text === `${from}...working copy`,
      `${why} reads ${JSON.stringify({ names, option })}, not ${from} to the working copy`);
  };
  const auto = "revcmp-auto";

  await run("compare: the reset stands only off the suggested pair and brings it back, and an unpicked left side follows a new suggestion", async () => {
    await page.evaluate((key) => localStorage.removeItem(key), h.revdiffKey);
    await openEditor(page);
    await diffReady(page);
    await h.openRevdiff(page);
    await h.revdiffSettled(page);
    const suggested = await suggestion();
    await reads(suggested, "the suggested pair");
    assert(!(await resetShown()), "the reset stands on the suggested pair");
    assert(await stored() === null, `opening stored ${JSON.stringify(await stored())}, which nobody picked`);

    await page.selectOption("[data-editor-revdiff-mode]", "direct");
    await h.revdiffSettled(page);
    let pair = await stored();
    assert(await resetShown() && pair && pair.from === "" && pair.mode === "direct", `the other question shows the reset ${await resetShown()} and stores ${JSON.stringify(pair)}`);
    await page.selectOption("[data-editor-revdiff-mode]", "since");
    await h.revdiffSettled(page);
    assert(!(await resetShown()), "the reset stands after the question went back");

    await h.typeSide(page, "from", "HEAD~2");
    pair = await stored();
    assert(await resetShown() && pair && pair.from === "HEAD~2", `a pick shows the reset ${await resetShown()} and stores ${JSON.stringify(pair)}`);
    const asked = page.waitForRequest((r) => {
      const q = new URL(r.url()).searchParams;
      return r.url().includes("/git/compare?") && !q.has("from") && !q.has("to") && q.get("worktree") === "1" && q.get("mode") === "since";
    }, { timeout: 10000 });
    await page.click("[data-editor-revdiff-reset]");
    await asked;
    await h.revdiffSettled(page);
    await reads(suggested, "the reset");
    assert(!(await resetShown()), "the reset stands after its click");
    assert(await stored() === null, `the reset left ${JSON.stringify(await stored())} stored`);

    assert(await runInShell(`git tag ${auto} HEAD~1\r`) === 200, "the shell refused the tag");
    await suggests(auto);
    await openEditor(page);
    await diffReady(page);
    await h.openRevdiff(page);
    await h.revdiffSettled(page);
    await reads(auto, "after a new tag the unpicked pair");
    assert(!(await resetShown()) && await stored() === null, `following the suggestion shows the reset ${await resetShown()} and stores ${JSON.stringify(await stored())}`);

    assert(await runInShell(`git tag -d ${auto} > /dev/null\r`) === 200, "the shell refused the cleanup");
    await suggests(suggested);
    await page.click("[data-editor-revdiff-close]");
    await page.waitForSelector("[data-editor-revdiff]", { state: "hidden", timeout: 8000 });
    return `${suggested} to the working copy without a reset, the reset after the other question and after HEAD~2, its click back on ${suggested}, then ${auto} after a reload without a pick`;
  });
}

async function remoteOnly({ runInShell, h }, branch) {
  const upstream = `${h.cmpClone}-upstream.git`;
  const name = `origin/${branch}`;
  assert(await runInShell(
    `git init -q --bare ${upstream} && git push -q ${upstream} HEAD:refs/heads/${branch} && git remote add origin ${upstream} && git fetch -q origin\r`,
  ) === 200, "the shell refused the remote");
  const deadline = Date.now() + 20000;
  let remote = await h.compareJSON(name, "HEAD", "direct");
  while (remote.status !== 200 && Date.now() < deadline) {
    await sleep(300);
    remote = await h.compareJSON(name, "HEAD", "direct");
  }
  assert(remote.status === 200, `${name} never resolved: ${JSON.stringify(remote.data)}`);
  const local = await h.compareJSON(branch, "HEAD", "direct");
  assert(local.status === 400 && local.data.side === "from", `the local name ${branch} answers ${local.status}, so it is not remote only`);
  return { name, drop: async () => assert(await runInShell(`git remote remove origin && rm -rf ${upstream}\r`) === 200, "the shell refused the cleanup") };
}

async function remoteChecks({ page, run, runInShell, openEditor, diffReady, h }) {
  await run("compare: a remote only branch picked as the left side reaches the comparison under its full name, its row naming no checkout", async () => {
    const { name, drop } = await remoteOnly({ runInShell, h }, "revcmp-remote-only");
    await openEditor(page);
    await diffReady(page);
    await h.openRevdiff(page);
    await h.revdiffSettled(page);
    await h.openSidePicker(page, "from");
    await page.fill("[data-editor-sheet-body] input", name);
    await page.waitForSelector("[data-picker-loading]", { state: "hidden", timeout: 20000 });
    const row = page.locator(`[data-editor-sheet] .editor-sheet-row:has(.editor-sheet-name:text-is("${name}"))`);
    const sub = (await row.locator(".editor-sheet-dir").textContent()).trim();
    assert(sub === "remote", `the row reads "${sub}", a comparison checks nothing out`);
    const asked = page.waitForRequest((r) => r.url().includes("/git/compare?") && new URL(r.url()).searchParams.get("from") === name, { timeout: 10000 });
    await row.locator(".editor-sheet-open").click();
    await asked;
    await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 6000 });
    await h.revdiffSettled(page);
    const error = await page.evaluate(() => {
      const el = document.querySelector("[data-editor-revdiff-error]");
      return el.hidden ? "" : el.textContent.trim();
    });
    assert(error === "", `the comparison refused ${name}: ${error}`);
    const names = await h.revdiffNames(page);
    const stored = await page.evaluate((key) => JSON.parse(localStorage.getItem(key) || "null"), h.revdiffKey);
    assert(names.from === name && stored && stored.from === name, `the side reads ${JSON.stringify(names)} and the device keeps ${JSON.stringify(stored)}`);
    const summary = await h.revdiffSummary(page);

    await page.click("[data-editor-revdiff-reset]");
    await h.revdiffSettled(page);
    await page.click("[data-editor-revdiff-close]");
    await page.waitForSelector("[data-editor-revdiff]", { state: "hidden", timeout: 8000 });
    await drop();
    return `${name} picked from the list compares as ${name}, "${summary}", the row reading remote`;
  });
}

module.exports = { compareHelpers, commitChecks, resetChecks, remoteOnly, remoteChecks };
