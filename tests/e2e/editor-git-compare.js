const L = require("./lib");
const { assert, sleep } = L;

// Not a runner of its own: editor-git.js runs these inside its revision
// comparison section, over its scratch repository and the fixture branches
// that section fetched in. compareHelpers is that section's toolkit.
// worktreeChecks covers the working copy as the right side of a comparison
// (GET git/compare?worktree=1, a parameter of its own because whatever is
// typed into `to` is a revision: the merge base of from and HEAD against the
// working tree for `...`, from itself for `..`, untracked files listed with
// "?" and their lines counted), the default it is when a device stored no
// pair, and "Open file" in the row menus of the commit view and of the
// comparison: the file as the right side has it, the working copy as its own
// editable tab (an open one comes to the front out of its diff, the next row
// click puts the diff back), a revision as a read only `//rev/<rev>/<path>`
// tab that comes back after a reload, and a file the right side deleted as
// the left side has it. A pick in the file's "Diff against revision" picker
// reads the revision where HEAD split off from it (git/file?split=1), stored
// on the tab as the name plus `diffSplit`, while a Compare row click diffs
// against exactly the commit the panel names. The compare icon beside the
// commit icon toggles the panel the way the commit icon toggles the commit
// view, and neither stands without a repository. On a touch screen those two
// and the group by folder toggles of both panels lose the hover look, so a
// tap that switches one off leaves it plain, while the pressed look stays.
// Gotchas:
//   - the fixture commits its files under wcmp/ with a pathspec commit, so
//     what earlier checks left staged stays out of it, and the cleanup puts
//     only wcmp/ back; the two commits stay in the history.
//   - the list follows a save over the git event, which fires only when the
//     status moves, so the save check writes a file that is clean against
//     HEAD. A second save into an already modified file publishes nothing,
//     in the commit view as much as here.
//   - shellText leaves count.txt behind as an untracked file, which the
//     working copy side lists, so the totals check reads the API and the
//     panel until both say the same instead of counting by hand.
//   - a folder row of the comparison has no menu at all, so that check waits
//     a moment and reads that none stands.
//   - WebKit's touch emulation does not hit the toggles at all, so the touch
//     check runs in Chromium, where a tap leaves :hover on the button and
//     holds :active a moment after the click.
//   - the split check needs a branch that moved on after it left HEAD's
//     history, built in a clone under /tmp and fetched in like the revcmp
//     branches, so the working copy never moves.

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

async function worktreeChecks({ page, run, tag, author, editorBase, project, runInShell, shellText, openEditor, diffReady, openCommitView, setEditorSwitch, toggleDiff, diffPressed, h }) {
  const wc = "wcmp";
  const ask = (query) => page.evaluate(([b, q]) =>
    fetch(`${b}/git/compare?${q}`, { headers: { Accept: "application/json" } })
      .then((r) => r.json().then((data) => ({ status: r.status, data }))), [editorBase, query]);
  const revisionTab = (rev, path) => `//rev/${encodeURIComponent(rev)}/${encodeURIComponent(path)}`;
  const listRow = (list, path) => `[data-editor-${list}-list] .editor-commit-row[data-path="${path}"]`;
  // menuOf right-clicks a row and answers the labels of the menu that opens,
  // or null when none does. The menu is left open for the caller.
  const menuOf = async (selector) => {
    await page.click(selector, { button: "right" });
    const shown = await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 2000 }).then(() => true).catch(() => false);
    if (!shown) return null;
    return page.$$eval(".dc-context-menu .dropdown-item .dc-menu-label-head", (els) => els.map((e) => e.textContent.trim()));
  };
  const closeMenu = async () => {
    await page.keyboard.press("Escape");
    await page.waitForSelector(".dc-context-menu", { state: "hidden", timeout: 4000 }).catch(() => {});
  };
  const openFileFrom = async (selector) => {
    const labels = await menuOf(selector);
    assert(labels && labels.includes("Open file"), `the menu of ${selector} offers ${JSON.stringify(labels)}`);
    await page.locator(".dc-context-menu .dropdown-item", { hasText: /^Open file/ }).first().click();
    return labels;
  };
  const plainEditor = () => page.evaluate(() => {
    const content = document.querySelector(".editor-surface > .cm-editor .cm-content");
    return {
      merge: document.querySelectorAll(".cm-mergeView").length,
      text: [...document.querySelectorAll(".editor-surface > .cm-editor .cm-line")].map((l) => l.textContent).join("\n").trim(),
      readOnly: (() => {
        const el = document.querySelector("[data-editor-readonly]");
        return { shown: getComputedStyle(el).display !== "none", path: document.querySelector("[data-editor-readonly-path]").textContent.trim() };
      })(),
      title: (document.querySelector(".editor-tab.active") || {}).title || "",
    };
  });
  // A read only tab keeps its cursor like a file from outside the project,
  // so what proves it is typing into it and finding nothing changed.
  const refusesTyping = async () => {
    const before = (await plainEditor()).text;
    await page.locator(".editor-surface > .cm-editor .cm-content").click({ force: true });
    await page.keyboard.press("Control+End");
    await page.keyboard.type("zzz");
    await sleep(300);
    return (await plainEditor()).text === before && await page.locator(".editor-tab.active.dirty").count() === 0;
  };
  const activeTab = (path) => page.waitForSelector(`.editor-tab.active[data-path="${path}"]`, { timeout: 15000 });
  const closeTab = async (path) => {
    const tab = page.locator(`.editor-tab[data-path="${path}"]`);
    if (await tab.count() === 0) return;
    await tab.locator(".editor-tab-state").click();
    await page.waitForSelector(`.editor-tab[data-path="${path}"]`, { state: "detached", timeout: 6000 });
  };
  const closeRevisionTabs = async () => {
    while (await page.locator(".editor-tab[data-path^='//rev/']").count() > 0) {
      await page.locator(".editor-tab[data-path^='//rev/'] .editor-tab-state").first().click();
      await sleep(200);
    }
  };
  const rowNumbers = (path) => page.evaluate((p) => {
    const row = document.querySelector(`[data-editor-revdiff-list] .editor-commit-row[data-path="${p}"]`);
    if (!row) return null;
    const plus = row.querySelector(".editor-commit-line .text-green");
    const minus = row.querySelector(".editor-commit-line .text-red.ms-1");
    return { mark: row.querySelector(".editor-commit-line > span").textContent.trim(), plus: plus ? plus.textContent : "", minus: minus ? minus.textContent : "", disabled: row.querySelector(".editor-commit-open").disabled };
  }, path);
  const grouped = async (list, on) => {
    const button = `[data-editor-${list === "revdiff" ? "revdiff" : "commit"}-group]`;
    const pressed = await page.evaluate((b) => document.querySelector(b).getAttribute("aria-pressed") === "true", button);
    if (pressed !== on) await page.click(button);
    return pressed;
  };
  const storedTab = (path) => page.evaluate(([key, p]) => (JSON.parse(localStorage.getItem(key)).open || []).find((e) => e && e.path === p), [`dc-editor-tabs:${project}`, path]);
  const leftSide = async (want) => {
    const ok = await page.waitForFunction((w) => {
      const content = document.querySelector(".cm-mergeView .cm-content");
      return !!content && [...content.querySelectorAll(".cm-line")].map((l) => l.textContent).join("\n").trim() === w;
    }, want, { timeout: 15000 }).then(() => true).catch(() => false);
    if (!ok) throw new Error(`the left side reads ${JSON.stringify(((await h.mergeSides(page))[0] || {}).text)}, not ${JSON.stringify(want)}`);
  };
  const side = "wcmp-side";
  const sideClone = `/tmp/zzgit-wcmp-${tag}`;
  let base = "";

  await run("compare: the working copy is the right side a device starts on, by a parameter of its own, while a stored pair stays", async () => {
    const fixture = [
      `mkdir -p ${wc}`,
      `printf 'one\\ntwo\\nthree\\n' > ${wc}/mod.txt`,
      `printf 'gone\\n' > ${wc}/del.txt`,
      `printf 'a\\nb\\nc\\nd\\ne\\nf\\n' > ${wc}/old.txt`,
      `printf 'keep\\n' > ${wc}/save.txt`,
      `git add ${wc} && git ${author} commit -q -m "wcmp base" -- ${wc}`,
      `printf 'one\\ntwo\\nthree\\nfour\\n' > ${wc}/mod.txt`,
      `printf 'keep\\nbase moved\\n' > ${wc}/save.txt`,
      `git add ${wc} && git ${author} commit -q -m "wcmp head" -- ${wc}`,
      `printf 'one\\ntwo changed\\nthree\\nfour\\n' > ${wc}/mod.txt`,
      `rm ${wc}/del.txt && git mv ${wc}/old.txt ${wc}/new.txt`,
      `printf 'x\\ny\\nz\\n' > ${wc}/loose.txt`,
    ].join(" && ");
    assert(await runInShell(`${fixture}\r`) === 200, "the shell refused the fixture");
    const deadline = Date.now() + 60000;
    let answer = null;
    while (Date.now() < deadline) {
      answer = await ask("from=HEAD~1&worktree=1&mode=since");
      const files = answer.status === 200 ? answer.data.files || [] : [];
      if (files.some((f) => f.path === `${wc}/loose.txt`) && files.some((f) => f.path === `${wc}/new.txt`)) break;
      await sleep(500);
    }
    const byPath = Object.fromEntries((answer.data.files || []).map((f) => [f.path, f]));
    assert(answer.status === 200 && answer.data.worktree === true && answer.data.to.name === "" && answer.data.to.sha === "",
      `the working copy answered ${JSON.stringify({ status: answer.status, worktree: answer.data.worktree, to: answer.data.to })}`);
    base = answer.data.base;
    assert(base === answer.data.from.sha, "HEAD~1 is behind HEAD, so its split with HEAD is itself");
    const want = { "mod.txt": ["M", 2, 1], "del.txt": ["D", 0, 1], "save.txt": ["M", 1, 0], "loose.txt": ["?", 3, 0] };
    for (const [name, [status, added, removed]] of Object.entries(want)) {
      const f = byPath[`${wc}/${name}`];
      assert(f && f.status === status && f.added === added && f.removed === removed, `${name} reads ${JSON.stringify(f)}`);
    }
    assert(byPath[`${wc}/new.txt`].status === "R" && byPath[`${wc}/new.txt`].from === `${wc}/old.txt`, `the staged rename reads ${JSON.stringify(byPath[`${wc}/new.txt`])}`);

    const split = (await ask("from=revcmp-a&to=HEAD&mode=since")).data.base;
    const since = await ask("from=revcmp-a&worktree=1&mode=since");
    const direct = await ask("from=revcmp-a&worktree=1&mode=direct");
    assert(since.data.base === split, `... measures from the merge base with HEAD ${split}, not ${since.data.base}`);
    assert(direct.data.base === direct.data.from.sha && direct.data.base !== split, `.. measures from revcmp-a itself, not ${direct.data.base}`);
    for (const query of ["from=HEAD~1&to=Working%20copy", "from=HEAD~1&worktree=true", "from=HEAD~1&worktree=1&to=master"]) {
      const refused = await ask(query);
      assert(refused.status === 400 && refused.data.side === "to", `${query} answered ${refused.status} ${JSON.stringify(refused.data)}`);
    }

    await openEditor(page);
    await diffReady(page);
    await page.evaluate((key) => localStorage.setItem(key, JSON.stringify({ from: "revcmp-a", to: "revcmp-b", mode: "since" })), h.revdiffKey);
    await openEditor(page);
    await h.openRevdiff(page);
    await h.revdiffSettled(page);
    const stored = await h.revdiffNames(page);
    assert(stored.from === "revcmp-a" && stored.to === "revcmp-b", `the pair this device stored reads ${JSON.stringify(stored)}`);
    await page.evaluate((key) => localStorage.removeItem(key), h.revdiffKey);
    await openEditor(page);
    await h.openRevdiff(page);
    await h.revdiffSettled(page);
    const names = await h.revdiffNames(page);
    const option = await h.revdiffOption(page);
    assert(names.to === "Working copy" && option.text === `${names.from}...working copy`,
      `without a stored pair the panel reads ${JSON.stringify({ names, option })}`);
    const kept = await page.evaluate((key) => JSON.parse(localStorage.getItem(key) || "null"), h.revdiffKey);
    assert(kept && kept.worktree === true && kept.to === "", `the device keeps ${JSON.stringify(kept)}`);
    await page.selectOption("[data-editor-revdiff-mode]", "direct");
    await h.revdiffSettled(page);
    assert((await h.revdiffOption(page)).text === `${names.from}..working copy`, "the direct question does not name the working copy");
    await page.selectOption("[data-editor-revdiff-mode]", "since");
    await h.revdiffSettled(page);

    await h.openSidePicker(page, "to");
    await page.waitForSelector("[data-picker-loading]", { state: "hidden", timeout: 20000 });
    const first = await page.evaluate(() => (document.querySelector("[data-editor-sheet-body] .editor-sheet-row .editor-sheet-name") || {}).textContent);
    assert(first === "Working copy", `the to picker opens on ${JSON.stringify(first)}`);
    await page.fill("[data-editor-sheet-body] input", "work");
    await page.waitForSelector("[data-picker-loading]", { state: "hidden", timeout: 20000 });
    assert(!(await page.locator('[data-editor-sheet-body] .editor-sheet-name:text-is("Working copy")').count()), "the working copy stands under a typed name");
    await page.click("[data-editor-sheet-close]");
    await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 6000 });
    await h.openSidePicker(page, "from");
    await page.waitForSelector("[data-picker-loading]", { state: "hidden", timeout: 20000 });
    assert(!(await page.locator('[data-editor-sheet-body] .editor-sheet-name:text-is("Working copy")').count()), "the from picker offers the working copy");
    await page.click("[data-editor-sheet-close]");
    await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 6000 });
    return "worktree=1 lists M D R and the untracked file counted, ... from the split and .. from revcmp-a, typed names refused, a stored pair stands, the bare device starts on Working copy";
  });

  await run("compare: against the working copy the list carries uncommitted and untracked files and the totals count them", async () => {
    await openEditor(page);
    await h.openRevdiff(page);
    await h.revdiffSettled(page);
    await h.typeSide(page, "from", "HEAD~1");
    await page.waitForSelector(listRow("revdiff", `${wc}/loose.txt`), { timeout: 15000 });
    const rows = Object.fromEntries((await h.revdiffRows(page)).map((r) => [r.path, r]));
    assert(rows[`${wc}/loose.txt`].mark === "U", `the untracked file is marked ${rows[`${wc}/loose.txt`].mark}`);
    assert(rows[`${wc}/mod.txt`].mark === "M" && rows[`${wc}/del.txt`].mark === "D" && rows[`${wc}/save.txt`].mark === "M", `the rows read ${JSON.stringify(rows)}`);
    assert(rows[`${wc}/new.txt`].mark === "R" && rows[`${wc}/new.txt`].sub === `${wc}/old.txt → ${wc}/new.txt`, `the rename reads ${JSON.stringify(rows[`${wc}/new.txt`])}`);
    const loose = await rowNumbers(`${wc}/loose.txt`);
    assert(loose.plus === "+3" && loose.minus === "−0", `the untracked row counts ${JSON.stringify(loose)}`);
    assert((await rowNumbers(`${wc}/del.txt`)).disabled, "a file the working copy deleted opens a diff of nothing");
    const deadline = Date.now() + 30000;
    let seen = "";
    let want = "";
    while (Date.now() < deadline) {
      const files = (await ask("from=HEAD~1&worktree=1&mode=since")).data.files || [];
      const added = files.reduce((n, f) => n + (f.added || 0), 0);
      const removed = files.reduce((n, f) => n + (f.removed || 0), 0);
      want = `${files.length} changes · +${added} −${removed}`;
      seen = await h.revdiffSummary(page);
      if (seen === want) break;
      await sleep(500);
    }
    assert(seen === want, `the summary reads "${seen}", the answer counts "${want}"`);
    return "M, D, R with its source, the untracked U with +3, a deleted row not openable, the totals equal to the answer";
  });

  await run("compare: a row against the working copy opens the file itself, editable and diffed against the left side, and the list follows its save", async () => {
    await setEditorSwitch(page, "diff_collapse", false);
    try {
      await openEditor(page);
      await h.openRevdiff(page);
      await h.revdiffSettled(page);
      await page.click(`${listRow("revdiff", `${wc}/mod.txt`)} .editor-commit-open`);
      await activeTab(`${wc}/mod.txt`);
      await page.waitForSelector(".cm-mergeView .cm-content", { timeout: 15000 });
      await sleep(400);
      let sides = await h.mergeSides(page);
      assert(sides.length === 2 && sides[0].editable === "false" && sides[1].editable === "true", `the sides are ${JSON.stringify(sides.map((s) => s.editable))}`);
      assert(sides[0].text === "one\ntwo\nthree" && sides[1].text === "one\ntwo changed\nthree\nfour",
        `the left side is not HEAD~1 or the right not the disk: ${JSON.stringify(sides.map((s) => s.text))}`);
      const stored = await page.evaluate(([key, p]) => (JSON.parse(localStorage.getItem(key)).open || []).find((e) => e && e.path === p), [`dc-editor-tabs:${project}`, `${wc}/mod.txt`]);
      assert(stored && stored.diff === base, `the tab stores ${JSON.stringify(stored)}, not the base ${base}`);

      await page.click(`${listRow("revdiff", `${wc}/new.txt`)} .editor-commit-open`);
      await activeTab(`${wc}/new.txt`);
      await page.waitForSelector(".cm-mergeView .cm-content", { timeout: 15000 });
      await sleep(400);
      sides = await h.mergeSides(page);
      assert(sides[0].text === "a\nb\nc\nd\ne\nf" && sides[1].text === "a\nb\nc\nd\ne\nf" && sides[0].changed.length === 0,
        `the rename is not diffed against its old path: ${JSON.stringify(sides.map((s) => s.text))}`);
      await openEditor(page);
      await activeTab(`${wc}/new.txt`);
      await page.waitForSelector(".cm-mergeView .cm-content", { timeout: 15000 });
      await sleep(400);
      sides = await h.mergeSides(page);
      assert(sides[0].text === "a\nb\nc\nd\ne\nf", `after a reload the rename's left side reads ${JSON.stringify(sides[0].text)}`);

      await h.openRevdiff(page);
      await h.revdiffSettled(page);
      const before = await rowNumbers(`${wc}/save.txt`);
      assert(before.plus === "+1" && before.minus === "−0", `save.txt starts at ${JSON.stringify(before)}`);
      await page.click(`${listRow("revdiff", `${wc}/save.txt`)} .editor-commit-open`);
      await activeTab(`${wc}/save.txt`);
      await page.waitForSelector(".cm-mergeView .cm-content", { timeout: 15000 });
      await sleep(400);
      await page.locator(".cm-mergeView .cm-content").nth(1).click({ force: true });
      await page.keyboard.press("Control+End");
      await page.keyboard.type("added");
      await page.waitForSelector(`.editor-tab[data-path="${wc}/save.txt"].dirty`, { timeout: 6000 });
      await page.click("[data-editor-save]");
      await page.waitForSelector(`.editor-tab[data-path="${wc}/save.txt"]:not(.dirty)`, { timeout: 10000 });
      await page.waitForFunction((p) => {
        const row = document.querySelector(`[data-editor-revdiff-list] .editor-commit-row[data-path="${p}"]`);
        const plus = row && row.querySelector(".editor-commit-line .text-green");
        return !!plus && plus.textContent === "+2";
      }, `${wc}/save.txt`, { timeout: 30000 });
      return "left side at HEAD~1 read only, right side the editable file, the base stored on the tab, a rename diffed against its old path before and after a reload, a save moving its row from +1 to +2";
    } finally {
      await setEditorSwitch(page, "diff_collapse", true).catch(() => {});
    }
  });

  await run("diff against revision: a picked branch that moved on diffs from where HEAD split off, a rename by its old path, while Compare's .. row diffs from the tip", async () => {
    const build = [
      `git clone -q . ${sideClone}`,
      `cd ${sideClone}`,
      `git checkout -q -b ${side} HEAD~1`,
      `printf 'one\\ntwo\\nthree\\nside\\n' > ${wc}/mod.txt`,
      `git ${author} commit -qam "wcmp side"`,
      "cd - > /dev/null",
      `git fetch -q ${sideClone} refs/heads/${side}:refs/heads/${side}`,
    ].join(" && ");
    assert(await runInShell(`${build}\r`) === 200, "the shell refused the side branch");
    const deadline = Date.now() + 60000;
    let tip = null;
    while (Date.now() < deadline) {
      tip = await ask(`from=${side}&to=${side}&mode=direct`);
      if (tip.status === 200) break;
      await sleep(500);
    }
    assert(tip.status === 200, `the side branch never arrived: ${JSON.stringify(tip.data)}`);
    const sideSha = tip.data.from.sha;
    const pickRev = async (name) => {
      await page.click(".editor-tab.active", { button: "right" });
      await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
      await page.locator(".dc-context-menu .dropdown-item", { hasText: /^Diff against revision/ }).first().click();
      await page.waitForSelector("[data-editor-sheet-body] input", { timeout: 8000 });
      const said = (await page.textContent("[data-editor-sheet-body] [data-picker-hint]")).trim();
      await page.fill("[data-editor-sheet-body] input", name);
      await page.waitForSelector("[data-picker-loading]", { state: "hidden", timeout: 20000 });
      await page.click(`[data-editor-sheet] .editor-sheet-row:has(.editor-sheet-name:text-is("${name}")) .editor-sheet-open`);
      await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 6000 });
      await page.waitForFunction((want) => document.querySelector("[data-editor-status]").textContent.includes(want),
        `Diff against ${name} at the split (${base.slice(0, 7)})`, { timeout: 15000 });
      return said;
    };
    await setEditorSwitch(page, "diff_collapse", false);
    try {
      await openEditor(page);
      await diffReady(page);
      await page.click(`.editor-tab[data-path="${wc}/mod.txt"]`);
      await activeTab(`${wc}/mod.txt`);
      const said = await pickRev(side);
      assert(said === "A branch compares from where yours split off.", `the picker says ${JSON.stringify(said)}`);
      await leftSide("one\ntwo\nthree");
      let stored = await storedTab(`${wc}/mod.txt`);
      assert(stored && stored.diff === side && stored.diffSplit === true, `the tab stores ${JSON.stringify(stored)}, not the name and the split`);
      await openEditor(page);
      await activeTab(`${wc}/mod.txt`);
      await leftSide("one\ntwo\nthree");

      await page.click(`.editor-tab[data-path="${wc}/new.txt"]`);
      await activeTab(`${wc}/new.txt`);
      await pickRev(side);
      await leftSide("a\nb\nc\nd\ne\nf");
      stored = await storedTab(`${wc}/new.txt`);
      assert(stored && stored.diffFrom === `${wc}/old.txt` && stored.diffSplit === true, `the renamed tab stores ${JSON.stringify(stored)}`);

      await h.openRevdiff(page);
      await h.revdiffSettled(page);
      await h.typeSide(page, "from", side);
      await page.selectOption("[data-editor-revdiff-mode]", "direct");
      await h.revdiffSettled(page);
      assert((await h.revdiffOption(page)).text === `${side}..working copy`, "the question is not side..working copy");
      await page.click(`${listRow("revdiff", `${wc}/mod.txt`)} .editor-commit-open`);
      await activeTab(`${wc}/mod.txt`);
      await leftSide("one\ntwo\nthree\nside");
      stored = await storedTab(`${wc}/mod.txt`);
      assert(stored && stored.diff === sideSha && !stored.diffSplit, `the .. row stores ${JSON.stringify(stored)}, not the tip ${sideSha}`);
      await page.selectOption("[data-editor-revdiff-mode]", "since");
      await h.revdiffSettled(page);
      await page.click(`${listRow("revdiff", `${wc}/mod.txt`)} .editor-commit-open`);
      await leftSide("one\ntwo\nthree");
      stored = await storedTab(`${wc}/mod.txt`);
      assert(stored && stored.diff === base && !stored.diffSplit, `the ... row stores ${JSON.stringify(stored)}, not the merge base ${base}`);
      await h.typeSide(page, "from", "HEAD~1");
      return "the picker names the split and diffs from it, the name and the split stored and rebuilt after a reload, a rename by its old path, .. against the working copy from the tip, ... from the merge base";
    } finally {
      await setEditorSwitch(page, "diff_collapse", true).catch(() => {});
    }
  });

  await run("compare: Open file in a row's menu opens the file as the right side has it, out of its diff, a deleted one as the left side had it, and a folder offers nothing", async () => {
    await openEditor(page);
    await diffReady(page);
    await h.openRevdiff(page);
    await h.revdiffSettled(page);
    await page.click(`${listRow("revdiff", `${wc}/mod.txt`)} .editor-commit-open`);
    await activeTab(`${wc}/mod.txt`);
    await page.waitForSelector(".cm-mergeView .cm-content", { timeout: 15000 });
    const labels = await openFileFrom(listRow("revdiff", `${wc}/mod.txt`));
    assert(JSON.stringify(labels) === JSON.stringify(["Open file"]), `the row's menu offers ${JSON.stringify(labels)}`);
    await page.waitForSelector(".cm-mergeView", { state: "detached", timeout: 10000 });
    await activeTab(`${wc}/mod.txt`);
    await sleep(400);
    let seen = await plainEditor();
    assert(seen.merge === 0 && seen.text === "one\ntwo changed\nthree\nfour" && !seen.readOnly.shown,
      `the working copy opened as ${JSON.stringify(seen)}`);
    assert(!(await diffPressed(page)), "Open file left the tab in its diff");
    const stored = await storedTab(`${wc}/mod.txt`);
    assert(!stored || !stored.diff, `the tab still stores a diff: ${JSON.stringify(stored)}`);

    await openFileFrom(listRow("revdiff", `${wc}/del.txt`));
    await activeTab(revisionTab(base, `${wc}/del.txt`));
    await sleep(400);
    seen = await plainEditor();
    assert(seen.text === "gone" && seen.readOnly.shown && seen.readOnly.path === `${wc}/del.txt at HEAD~1` && await refusesTyping(),
      `the deleted file opened as ${JSON.stringify(seen)}`);

    const was = await grouped("revdiff", true);
    try {
      await page.waitForSelector(`[data-editor-revdiff-list] .editor-commit-grouprow[data-dir="${wc}"]`, { timeout: 10000 });
      assert(await menuOf(`[data-editor-revdiff-list] .editor-commit-grouprow[data-dir="${wc}"]`) === null, "a folder row opened a menu");
    } finally {
      await closeMenu();
      await grouped("revdiff", was);
    }
    return "the working copy file editable without a diff, the deleted file read only at the base, no menu on a folder";
  });

  await run("commit view: Open file stands next to Revert, takes a tab out of its diff until the next row click, opens a deleted file as HEAD has it, and a folder keeps only the revert", async () => {
    await openEditor(page);
    await diffReady(page);
    await page.click(`.editor-tab[data-path="${wc}/mod.txt"]`);
    await activeTab(`${wc}/mod.txt`);
    if (!(await diffPressed(page))) await toggleDiff(page);
    await page.waitForSelector(".cm-mergeView .cm-content", { timeout: 15000 });
    await page.click(`.editor-tab[data-path="${wc}/save.txt"]`);
    await activeTab(`${wc}/save.txt`);
    await openCommitView(page);
    await page.waitForSelector(listRow("commit", `${wc}/mod.txt`), { timeout: 15000 });
    const labels = await openFileFrom(listRow("commit", `${wc}/mod.txt`));
    assert(labels.indexOf("Revert changes") === labels.indexOf("Open file") + 1, `Open file does not stand next to Revert: ${JSON.stringify(labels)}`);
    await activeTab(`${wc}/mod.txt`);
    await sleep(400);
    assert((await plainEditor()).merge === 0 && !(await diffPressed(page)), "Open file brought the tab to the front in its diff");
    await page.click(`${listRow("commit", `${wc}/mod.txt`)} .editor-commit-open`);
    await page.waitForSelector(".cm-mergeView .cm-content", { timeout: 15000 });
    assert(await diffPressed(page), "the row click after Open file did not put the diff back");

    await closeTab(`${wc}/new.txt`);
    await openFileFrom(listRow("commit", `${wc}/new.txt`));
    await activeTab(`${wc}/new.txt`);
    await sleep(400);
    let seen = await plainEditor();
    assert(seen.merge === 0 && !seen.readOnly.shown && seen.text === "a\nb\nc\nd\ne\nf", `the renamed file opened as ${JSON.stringify(seen)}`);
    await openFileFrom(listRow("commit", `${wc}/loose.txt`));
    await activeTab(`${wc}/loose.txt`);
    await sleep(400);
    seen = await plainEditor();
    assert(!seen.readOnly.shown && seen.text === "x\ny\nz", `the untracked file opened as ${JSON.stringify(seen)}`);

    await openFileFrom(listRow("commit", `${wc}/del.txt`));
    await activeTab(revisionTab("HEAD", `${wc}/del.txt`));
    await sleep(400);
    seen = await plainEditor();
    assert(seen.text === "gone" && seen.readOnly.path === `${wc}/del.txt at HEAD` && seen.title === `${wc}/del.txt at HEAD · read only` && await refusesTyping(),
      `the deleted file opened as ${JSON.stringify(seen)}`);

    const was = await grouped("commit", true);
    try {
      await page.waitForSelector(`[data-editor-commit-list] .editor-commit-grouprow[data-dir="${wc}"]`, { timeout: 10000 });
      const folder = await menuOf(`[data-editor-commit-list] .editor-commit-grouprow[data-dir="${wc}"]`);
      assert(folder && folder.includes("Revert changes") && !folder.includes("Open file"), `the folder row offers ${JSON.stringify(folder)}`);
    } finally {
      await closeMenu();
      await grouped("commit", was);
    }
    await page.click("[data-editor-commit-close]");
    return "Open file then Revert, the background tab front without its diff and the row click putting it back, the rename by its new path, the untracked file, the deleted file at HEAD, the folder only Revert";
  });

  await run("compare: with a revision on the right the row stays a read only diff and Open file opens read only revisions that survive a reload", async () => {
    await openEditor(page);
    await h.openRevdiff(page);
    await h.revdiffSettled(page);
    await h.typeSide(page, "to", "revcmp-b");
    await h.typeSide(page, "from", "revcmp-a");
    const answer = (await h.compareJSON("revcmp-a", "revcmp-b", "since")).data;
    await page.click(`${listRow("revdiff", "revcmp/keep/mod.txt")} .editor-commit-open`);
    await page.waitForSelector(".editor-tab.active[data-path^='//revdiff/']", { timeout: 15000 });
    await page.waitForSelector(".cm-mergeView .cm-content", { timeout: 15000 });
    const sides = await h.mergeSides(page);
    assert(sides.length === 2 && sides.every((s) => s.editable === "false"), `the revision diff is editable: ${JSON.stringify(sides.map((s) => s.editable))}`);
    await h.closeRevdiffTabs(page);

    const opened = [];
    for (const [path, rev, label] of [
      ["revcmp/keep/mod.txt", answer.to.sha, "revcmp-b"],
      ["revcmp/moved/edit.txt", answer.to.sha, "revcmp-b"],
      ["revcmp/keep/del.txt", answer.base, `revcmp-a at the split (${answer.base.slice(0, 7)})`],
    ]) {
      await openFileFrom(listRow("revdiff", path));
      await activeTab(revisionTab(rev, path));
      await sleep(400);
      const seen = await plainEditor();
      const git = await shellText(`git show ${rev}:${path}`);
      assert(seen.text === git && seen.title === `${path} at ${label} · read only` && await refusesTyping(),
        `${path} opened as ${JSON.stringify(seen)}, git says ${JSON.stringify(git)}`);
      opened.push(seen.text);
    }
    const count = await page.locator(".editor-tab[data-path^='//rev/']").count();
    await openEditor(page);
    await activeTab(revisionTab(answer.base, "revcmp/keep/del.txt"));
    await sleep(400);
    const back = await plainEditor();
    assert(await page.locator(".editor-tab[data-path^='//rev/']").count() === count && back.text === opened[2] && back.readOnly.shown && await refusesTyping(),
      `after a reload ${await page.locator(".editor-tab[data-path^='//rev/']").count()} of ${count} revision tabs stand, the active one ${JSON.stringify(back)}`);
    await closeRevisionTabs();
    return "the row a read only diff, Open file the file at revcmp-b, the rename by its new path, the deletion at the split, all matching git show and back after a reload";
  });

  for (const path of ["mod.txt", "new.txt", "save.txt", "loose.txt"]) await closeTab(`${wc}/${path}`).catch(() => {});
  assert(await runInShell(`git reset -q -- ${wc} && git checkout -q -- ${wc} && git clean -fdq -- ${wc} && git branch -qD ${side}; rm -rf ${sideClone}\r`) === 200, "the shell refused the cleanup");

  await run("compare: its icon beside the commit icon toggles the panel, and neither stands without a repository", async () => {
    const shown = (sel) => page.evaluate((s) => {
      const el = document.querySelector(s);
      if (!el) return false;
      const box = el.getBoundingClientRect();
      return getComputedStyle(el).display !== "none" && box.width > 0 && box.height > 0;
    }, sel);
    const state = () => page.evaluate(() => {
      const visible = (el) => !!el && getComputedStyle(el).display !== "none" && el.getBoundingClientRect().height > 0;
      const commit = document.querySelector("[data-editor-commit-toggle]");
      const compare = document.querySelector("[data-editor-revdiff-toggle]");
      return {
        compare: visible(document.querySelector("[data-editor-revdiff]")),
        commit: visible(document.querySelector("[data-editor-commit]")),
        tree: visible(document.querySelector("[data-editor-tree]")),
        comparePressed: compare.getAttribute("aria-pressed") === "true" && compare.classList.contains("active"),
        commitPressed: commit.getAttribute("aria-pressed") === "true" && commit.classList.contains("active"),
      };
    });
    const settle = async (want) => {
      const deadline = Date.now() + 8000;
      let last = null;
      while (Date.now() < deadline) {
        last = await state();
        if (Object.entries(want).every(([k, v]) => last[k] === v)) return last;
        await sleep(150);
      }
      throw new Error(`the panels read ${JSON.stringify(last)}, not ${JSON.stringify(want)}`);
    };
    const closed = { compare: false, commit: false, tree: true, comparePressed: false, commitPressed: false };
    const comparing = { compare: true, commit: false, tree: false, comparePressed: true, commitPressed: false };
    const committing = { compare: false, commit: true, tree: false, comparePressed: false, commitPressed: true };

    await openEditor(page);
    await diffReady(page);
    assert(await shown("[data-editor-revdiff-toggle]"), "the compare icon does not stand in a repository");
    const icon = await page.evaluate(() => {
      const el = document.querySelector("[data-editor-revdiff-toggle]");
      return {
        title: el.title,
        label: el.getAttribute("aria-label"),
        glyph: !!el.querySelector(".ti-git-compare"),
        afterCommit: el.previousElementSibling === document.querySelector("[data-editor-commit-toggle]"),
      };
    });
    assert(icon.title === "Compare" && icon.label === "Compare" && icon.glyph && icon.afterCommit, `the compare icon reads ${JSON.stringify(icon)}`);
    await settle(closed);
    await page.click("[data-editor-revdiff-toggle]");
    await settle(comparing);
    const title = await page.evaluate(() => document.querySelector("[data-editor-revdiff] .fw-medium").textContent.trim());
    assert(title === "Compare", `the panel is titled ${JSON.stringify(title)}`);
    await page.click("[data-editor-revdiff-toggle]");
    await settle(closed);

    await page.click("[data-editor-commit-toggle]");
    await settle(committing);
    await page.click("[data-editor-revdiff-toggle]");
    await settle(comparing);
    await page.click("[data-editor-commit-toggle]");
    await settle(committing);
    await page.click("[data-editor-commit-toggle]");
    await settle(closed);

    await h.openRevdiff(page);
    await settle(comparing);
    await page.click("[data-editor-revdiff-close]");
    await settle(closed);
    await page.click("[data-editor-git-status]");
    await page.waitForSelector("[data-editor-sheet]:not([hidden])", { timeout: 6000 });
    const entry = await page.locator("[data-editor-sheet-body] .dropdown-item", { hasText: /^Compare/ }).first().evaluate((row) =>
      [...row.querySelectorAll(":scope > span > span")].map((el) => el.textContent.trim()));
    assert(entry[0] === "Compare" && entry[1] === "what your branch changed, or any two revisions", `the git sheet's entry reads ${JSON.stringify(entry)}`);
    await page.keyboard.press("Escape");
    await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 6000 });

    const plain = `zzgit-cmpicon-${tag}`;
    await L.createProject(page, plain);
    try {
      await page.goto(`${L.BASE}/projects/${encodeURIComponent(plain)}/editor`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      await L.waitUpgraded(page, ["dc-editor"]);
      await diffReady(page, false);
      assert(!(await shown("[data-editor-revdiff-toggle]")) && !(await shown("[data-editor-commit-toggle]")), "an icon stands in a project without a repository");
    } finally {
      await L.deleteProject(page, plain).catch(() => {});
    }
    return "icon titled Compare after the commit icon, a click toggles it, the commit icon and it take turns, the sheet names it with its new line, no icon without a repository";
  });

  await run("compare: on a touch screen the commit, compare and group toggles drop the hover look and keep the pressed one, switched off they read untouched", async () => {
    const toggles = ["[data-editor-commit-toggle]", "[data-editor-revdiff-toggle]", "[data-editor-commit-group]", "[data-editor-revdiff-group]"];
    // A tapped button keeps :active a moment and then eases into its new
    // colors, so the look is read once both are over.
    const read = (target) => target.evaluate(async (sels) => {
      await new Promise((done) => {
        const tick = () => (sels.some((s) => document.querySelector(s).matches(":active")) ? requestAnimationFrame(tick) : done());
        tick();
      });
      await Promise.all(sels.flatMap((s) => document.querySelector(s).getAnimations().map((a) => a.finished.catch(() => {}))));
      return {
        hoverNone: matchMedia("(hover: none)").matches,
        buttons: Object.fromEntries(sels.map((s) => {
          const cs = getComputedStyle(document.querySelector(s));
          const vars = (prefix) => ["color", "bg", "border-color"].map((k) => cs.getPropertyValue(`--tblr-btn-${prefix}${k}`).trim()).join(" | ");
          return [s, { hover: vars("hover-"), plain: vars(""), look: [cs.color, cs.backgroundColor, cs.borderColor, cs.boxShadow, cs.outlineStyle].join(" ") }];
        })),
      };
    }, toggles);
    await openEditor(page);
    const desk = await read(page);
    assert(!desk.hoverNone && toggles.every((s) => desk.buttons[s].hover !== desk.buttons[s].plain),
      `a fine pointer lost the hover look: ${JSON.stringify(desk)}`);
    await page.hover("[data-editor-commit-toggle]");
    const hovered = (await read(page)).buttons["[data-editor-commit-toggle]"].look;
    await page.mouse.move(0, 0);
    assert(hovered !== desk.buttons["[data-editor-commit-toggle]"].look, `a mouse over the commit toggle shows no hover: ${hovered}`);

    const ctx = await page.context().browser().newContext({ ignoreHTTPSErrors: true, hasTouch: true, isMobile: true, viewport: { width: 1360, height: 900 } });
    try {
      const tp = await ctx.newPage();
      await L.login(tp);
      await openEditor(tp);
      const touch = await read(tp);
      assert(touch.hoverNone && toggles.every((s) => touch.buttons[s].hover === touch.buttons[s].plain),
        `a touch screen keeps the hover look: ${JSON.stringify(touch)}`);
      const look = async (s) => (await read(tp)).buttons[s].look;
      const plainLook = await look("[data-editor-commit-toggle]");
      const comparePlain = await look("[data-editor-revdiff-toggle]");
      await tp.tap("[data-editor-commit-toggle]");
      await tp.waitForSelector("[data-editor-commit]:not([hidden])", { timeout: 10000 });
      const commitOn = await look("[data-editor-commit-toggle]");
      await tp.tap("[data-editor-commit-toggle]");
      await tp.waitForSelector("[data-editor-commit]", { state: "hidden", timeout: 8000 });
      const commitOff = await look("[data-editor-commit-toggle]");
      assert(commitOn !== plainLook && commitOff === plainLook, `the commit toggle reads ${JSON.stringify({ plainLook, commitOn, commitOff })}`);
      await tp.tap("[data-editor-revdiff-toggle]");
      await tp.waitForSelector("[data-editor-revdiff]:not([hidden])", { timeout: 10000 });
      const pressed = await look("[data-editor-revdiff-toggle]");
      assert(pressed !== plainLook, `the pressed compare toggle looks plain: ${pressed}`);
      const tapGroup = async () => {
        const was = await tp.getAttribute("[data-editor-revdiff-group]", "aria-pressed");
        await tp.tap("[data-editor-revdiff-group]");
        await tp.waitForFunction((w) => document.querySelector("[data-editor-revdiff-group]").getAttribute("aria-pressed") !== w, was, { timeout: 5000 });
        return look("[data-editor-revdiff-group]");
      };
      const groupBefore = await look("[data-editor-revdiff-group]");
      const groupFlipped = await tapGroup();
      const groupBack = await tapGroup();
      assert(groupFlipped !== groupBefore && groupBack === groupBefore,
        `the group toggle reads ${JSON.stringify({ groupBefore, groupFlipped, groupBack })}`);
      await tp.tap("[data-editor-revdiff-toggle]");
      await tp.waitForSelector("[data-editor-revdiff]", { state: "hidden", timeout: 8000 });
      const off = await look("[data-editor-revdiff-toggle]");
      assert(off === comparePlain, `the compare toggle switched off reads ${off}, untouched ${comparePlain}`);
    } finally {
      await ctx.close();
    }
    return "hover variables equal the plain ones under (hover: none) on all four toggles and stay distinct on a fine pointer, a mouse hover shows, pressed looks differ, commit and compare switched off by a tap read like untouched";
  });
}

module.exports = { compareHelpers, worktreeChecks };
