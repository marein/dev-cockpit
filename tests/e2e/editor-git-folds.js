const L = require("./lib");
const { assert, sleep } = L;

// Not a runner of its own: editor-git.js runs these over its scratch
// repository after the compare checks of editor-git-compare-worktree.js.
// foldChecks covers folding the folder rows of the two lists that group
// changes by folder, the commit view's and the compare view's
// (@dc/editor-changes over @dc/editor-change-tree): a click on a folder row
// folds its subtree away and back, the chevron and aria-expanded say which,
// the count stays, and the row's checkbox picks or drops the whole folder
// without folding it, its box apart from the fold button's. In the list
// Enter, the right and the left arrow fold the way a tree does, left on an
// open subfolder folds it and on a folded one steps to the folder above,
// Space stays the checkbox. A filter opens every folded folder that holds a
// match, folds made under it last until the query changes, and clearing it
// brings back the folds made without it. The folds come back with a reload
// while the view stays open and go once it closes, like its filter. On a
// phone a tap does all of it, in Chromium and WebKit.
// Gotchas:
//   - the fixture is untracked files under 0fold, which sorts before every
//     other folder, so its rows are built whatever earlier checks left in the
//     list; they are removed again at the end, picks included, because the
//     picks are a draft the server keeps for every device.

async function foldChecks({ page, run, mobile, editorBase, openEditor, openCommitView, runInShell, h }) {
  const top = "0fold";
  const files = [`${top}/a/one.txt`, `${top}/a/two.txt`, `${top}/b/three.txt`, `${top}/top.txt`];
  const below = [`g:${top}/a`, `f:${top}/a/one.txt`, `f:${top}/a/two.txt`, `g:${top}/b`, `f:${top}/b/three.txt`, `f:${top}/top.txt`];
  const listOf = (list) => `[data-editor-${list}-list]`;
  const rowOf = (list, dir) => `${listOf(list)} .editor-commit-grouprow[data-dir="${dir}"]`;
  // Every row the list shows, by key, read from its box, so a row that is
  // gone and a row that stands without a box both count as not shown.
  const shown = (target, list) => target.evaluate((sel) => [...document.querySelectorAll(`${sel} .editor-commit-row`)]
    .filter((row) => row.getBoundingClientRect().height > 0)
    .map((row) => (row.dataset.dir != null ? `g:${row.dataset.dir}` : `f:${row.dataset.path}`)), listOf(list));
  const folder = (target, list, dir) => target.evaluate((sel) => {
    const row = document.querySelector(sel);
    if (!row) return null;
    const btn = row.querySelector("[data-editor-commit-fold]");
    const check = row.querySelector("input[type=checkbox]");
    const chevron = btn.querySelector(".ti-chevron-right, .ti-chevron-down");
    return {
      expanded: btn.getAttribute("aria-expanded"),
      chevron: chevron ? (chevron.classList.contains("ti-chevron-right") ? "right" : "down") : "",
      count: row.querySelector(".ms-auto").textContent,
      active: row.classList.contains("active"),
      focused: document.activeElement === row,
      checked: check ? check.checked : null,
      indeterminate: check ? check.indeterminate : null,
    };
  }, rowOf(list, dir));
  const waitShown = (target, list, want, why) => target.waitForFunction(([sel, keys, on]) => {
    const rows = [...document.querySelectorAll(`${sel} .editor-commit-row`)].filter((row) => row.getBoundingClientRect().height > 0)
      .map((row) => (row.dataset.dir != null ? `g:${row.dataset.dir}` : `f:${row.dataset.path}`));
    return keys.every((k) => rows.includes(k) === on);
  }, [listOf(list), below, want], { timeout: 8000 }).catch(() => { throw new Error(why); });
  const fileChecks = (target) => target.evaluate((paths) => paths.map((p) =>
    document.querySelector(`[data-editor-commit-list] .editor-commit-row[data-path="${p}"] input`)?.checked), files);
  // The shell answers before it ran, so the working copy is asked until it
  // says what the command did.
  const untilChanges = async (done, why) => {
    const deadline = Date.now() + 30000;
    for (;;) {
      const json = await page.evaluate((b) => fetch(`${b}/git/changes`, { headers: { Accept: "application/json" } }).then((r) => r.text()), editorBase);
      if (done(json)) return;
      assert(Date.now() < deadline, why);
      await sleep(500);
    }
  };
  const summary = async (target) => Number((await target.locator("[data-editor-commit-summary]").textContent()).trim().split(" ")[0]);
  const grouped = async (target) => {
    if (await target.getAttribute("[data-editor-commit-group]", "aria-pressed") !== "true") await target.click("[data-editor-commit-group]");
    await target.waitForSelector(rowOf("commit", top), { timeout: 15000 });
  };
  // The checkbox and the fold button are two boxes that do not touch, and
  // the middle of each is that element and no other.
  const targetsApart = (target, list) => target.evaluate((sel) => {
    const row = document.querySelector(sel);
    const check = row.querySelector("input[type=checkbox]").getBoundingClientRect();
    const btn = row.querySelector("[data-editor-commit-fold]").getBoundingClientRect();
    const at = (r) => document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
    return {
      gap: btn.left - check.right,
      checkHit: at(check) === row.querySelector("input[type=checkbox]"),
      buttonHit: !!at(btn)?.closest("[data-editor-commit-fold]"),
    };
  }, rowOf(list, top));

  await run("folds: a click on a folder row of the commit list folds its subtree away and back, the count stays, its checkbox picks the whole folder without folding it", async () => {
    await runInShell(`mkdir -p -- ${top}/a ${top}/b && for f in ${files.join(" ")}; do echo "$f" > "$f"; done\r`);
    await untilChanges((json) => files.every((p) => json.includes(`"${p}"`)), "the fixture never reached the working copy");
    await openEditor(page);
    await openCommitView(page);
    await grouped(page);
    await waitShown(page, "commit", true, "the fixture's rows never showed");
    const open = await folder(page, "commit", top);
    assert(open.expanded === "true" && open.chevron === "down" && open.count === "4", `the folder starts as ${JSON.stringify(open)}`);

    await page.click(`${rowOf("commit", top)} [data-editor-commit-fold]`);
    await waitShown(page, "commit", false, "a click left the folder's subtree on screen");
    const shut = await folder(page, "commit", top);
    assert(shut.expanded === "false" && shut.chevron === "right" && shut.count === "4" && shut.active,
      `the folded folder reads ${JSON.stringify(shut)}`);
    assert((await shown(page, "commit")).includes(`g:${top}`), "folding took the folder's own row away");

    const apart = await targetsApart(page, "commit");
    assert(apart.gap >= 4 && apart.checkHit && apart.buttonHit, `the checkbox and the fold button collide: ${JSON.stringify(apart)}`);
    const before = await summary(page);
    await page.setChecked(`${rowOf("commit", top)} input`, true);
    await sleep(300);
    const picked = await folder(page, "commit", top);
    assert(picked.checked && picked.expanded === "false", `the checkbox of the folded folder reads ${JSON.stringify(picked)}`);
    assert(await summary(page) === before + 4, `picking the folded folder counts ${await summary(page)}, not ${before + 4}`);
    assert(!(await shown(page, "commit")).some((k) => below.includes(k)), "picking the folder unfolded it");

    await page.click(`${rowOf("commit", top)} [data-editor-commit-fold]`);
    await waitShown(page, "commit", true, "a second click left the folder folded");
    assert((await fileChecks(page)).every(Boolean), `the files under the folder came back unpicked: ${JSON.stringify(await fileChecks(page))}`);
    await page.setChecked(`${rowOf("commit", top)} input`, false);
    await sleep(300);
    assert((await fileChecks(page)).every((on) => on === false), "dropping the folder left a file picked");
    assert((await folder(page, "commit", top)).expanded === "true", "dropping the folder folded it");
    return `folded and back with the count at ${shut.count}, the box picked ${before} -> ${before + 4} without folding, ${apart.gap.toFixed(1)}px between the two targets`;
  });

  await run("folds: Enter and the arrows fold in the list, a filter opens the folded folders that hold a match until it is cleared, a reload keeps the folds and closing the view drops them", async () => {
    await page.click(`${rowOf("commit", `${top}/a`)} [data-editor-commit-fold]`);
    await page.waitForSelector(`${listOf("commit")} .editor-commit-row[data-path="${top}/a/one.txt"]`, { state: "detached", timeout: 5000 });

    await page.fill("[data-editor-commit-filter]", top);
    await waitShown(page, "commit", true, "the filter did not open the folded subfolder that holds a match");
    assert((await folder(page, "commit", `${top}/a`)).expanded === "true", "the filter shows the matches under a folder that reads folded");

    await page.focus("[data-editor-commit-filter]");
    await page.keyboard.press("ArrowDown");
    const first = await folder(page, "commit", top);
    assert(first.active && first.focused, `arrow down did not land on the folder row: ${JSON.stringify(first)}`);
    const keySteps = [];
    for (const [key, want] of [["ArrowLeft", false], ["ArrowRight", true], ["Enter", false], ["Enter", true]]) {
      await page.keyboard.press(key);
      await waitShown(page, "commit", want, `${key} did not ${want ? "unfold" : "fold"} the folder`);
      const now = await folder(page, "commit", top);
      assert(now.focused && now.expanded === String(want), `after ${key} the folder reads ${JSON.stringify(now)}`);
      keySteps.push(`${key} ${want ? "open" : "folded"}`);
    }
    await page.keyboard.press("ArrowDown");
    assert((await folder(page, "commit", `${top}/a`)).focused, "arrow down did not step onto the subfolder");
    await page.keyboard.press("ArrowLeft");
    await page.waitForSelector(`${listOf("commit")} .editor-commit-row[data-path="${top}/a/one.txt"]`, { state: "detached", timeout: 5000 });
    await page.keyboard.press("ArrowLeft");
    const parent = await folder(page, "commit", top);
    assert(parent.focused && parent.expanded === "true", `left on a folded subfolder did not step to its parent: ${JSON.stringify(parent)}`);
    await page.keyboard.press(" ");
    await sleep(300);
    const spaced = await folder(page, "commit", top);
    assert(spaced.checked && spaced.expanded === "true", `Space on the folder row reads ${JSON.stringify(spaced)}`);
    await page.keyboard.press(" ");
    await sleep(300);
    assert(!(await folder(page, "commit", top)).checked, "a second Space left the folder picked");

    // A changed query starts with every folder open again.
    await page.fill("[data-editor-commit-filter]", `${top}/`);
    await waitShown(page, "commit", true, "a changed query kept the fold made under the old one");

    await page.fill("[data-editor-commit-filter]", "");
    await page.waitForSelector(`${listOf("commit")} .editor-commit-row[data-path="${top}/a/one.txt"]`, { state: "detached", timeout: 5000 });
    const cleared = await shown(page, "commit");
    assert(cleared.includes(`g:${top}/a`) && cleared.includes(`f:${top}/top.txt`), `clearing the filter shows ${JSON.stringify(cleared.filter((k) => k.includes(top)))}`);

    await sleep(600);
    await page.reload({ waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    await page.waitForSelector(rowOf("commit", `${top}/a`), { timeout: 20000 });
    await sleep(500);
    const reloaded = await folder(page, "commit", `${top}/a`);
    assert(reloaded.expanded === "false" && !(await shown(page, "commit")).includes(`f:${top}/a/one.txt`), `after a reload the subfolder reads ${JSON.stringify(reloaded)}`);

    await page.click("[data-editor-commit-close]");
    await page.waitForSelector("[data-editor-commit]", { state: "hidden", timeout: 8000 });
    await openCommitView(page);
    await waitShown(page, "commit", true, "closing the view kept the fold");
    return `${keySteps.join(", ")}; the filter opened the folded subfolder, clearing it folded it again, a reload kept it, closing the view dropped it`;
  });

  await run("folds: the compare list folds its folder rows the same way", async () => {
    await page.evaluate((k) => localStorage.removeItem(k), h.revdiffKey);
    await openEditor(page);
    await h.openRevdiff(page);
    await h.revdiffSettled(page);
    await page.waitForSelector(rowOf("revdiff", top), { timeout: 20000 });
    await waitShown(page, "revdiff", true, "the compare list does not show the fixture");
    const open = await folder(page, "revdiff", top);
    assert(open.checked === null && open.expanded === "true" && open.count === "4", `the compare folder reads ${JSON.stringify(open)}`);
    await page.click(`${rowOf("revdiff", top)} [data-editor-commit-fold]`);
    await waitShown(page, "revdiff", false, "a click left the compare folder's subtree on screen");
    const shut = await folder(page, "revdiff", top);
    assert(shut.expanded === "false" && shut.chevron === "right" && shut.count === "4", `the folded compare folder reads ${JSON.stringify(shut)}`);
    await page.keyboard.press("Enter");
    await waitShown(page, "revdiff", true, "Enter on the folded compare folder left it folded");
    await page.click("[data-editor-revdiff-close]");
    await page.waitForSelector("[data-editor-revdiff]", { state: "hidden", timeout: 8000 });
    return `folded by a click with the count at ${shut.count}, unfolded by Enter`;
  });

  await run("folds: mobile, a tap on a folder row folds it and a tap on its checkbox picks the whole folder without folding it", async () => {
    const mp = await mobile();
    await openEditor(mp);
    if (!(await mp.$(".editor.editor-drawer-open"))) {
      await mp.locator("[data-editor-drawer-toggle]").first().click();
      await mp.waitForSelector(".editor.editor-drawer-open", { timeout: 10000 });
    }
    await openCommitView(mp);
    await grouped(mp);
    await waitShown(mp, "commit", true, "the phone does not show the fixture's rows");
    const apart = await targetsApart(mp, "commit");
    assert(apart.gap >= 4 && apart.checkHit && apart.buttonHit, `on the phone the checkbox and the fold button collide: ${JSON.stringify(apart)}`);

    await mp.tap(`${rowOf("commit", top)} [data-editor-commit-fold]`);
    await waitShown(mp, "commit", false, "a tap left the folder's subtree on the phone");
    const before = await summary(mp);
    await mp.tap(`${rowOf("commit", top)} input`);
    await sleep(400);
    const picked = await folder(mp, "commit", top);
    assert(picked.checked && picked.expanded === "false" && picked.count === "4", `the tapped checkbox reads ${JSON.stringify(picked)}`);
    assert(await summary(mp) === before + 4, `the tap picked ${await summary(mp) - before} changes, not 4`);
    await mp.tap(`${rowOf("commit", top)} [data-editor-commit-fold]`);
    await waitShown(mp, "commit", true, "a second tap left the folder folded on the phone");
    await mp.tap(`${rowOf("commit", top)} input`);
    await sleep(400);
    const dropped = await folder(mp, "commit", top);
    assert(!dropped.checked && dropped.expanded === "true", `the second tap on the checkbox reads ${JSON.stringify(dropped)}`);

    await runInShell(`rm -r -- ${top}\r`);
    await untilChanges((json) => !json.includes(`${top}/`), "the fixture never left the working copy");
    return `tapped folded and back, the checkbox picked ${before} -> ${before + 4} without folding, ${apart.gap.toFixed(1)}px between the targets`;
  });
}

module.exports = { foldChecks };
