const L = require("./lib");
const { assert, sleep, BASE } = L;

// Not a runner of its own: editor.js runs these checks of the project palette
// in its scratch project, with a second project and two worktrees of the
// scratch project made through the git proxy, one on a branch and one on a
// detached HEAD. The palette clones the server's rows into its groups, so a
// worktree folded behind its main is only known in the browser and reads
// differently there than in the Recent shortlist or under a query.

function readRow(el) {
  const sub = el.querySelector(".editor-palette-sub");
  const head = el.querySelector(".editor-palette-repo");
  const firstBox = (node) => {
    const range = document.createRange();
    range.selectNodeContents(node);
    return [...range.getClientRects()].find((box) => box.width > 0);
  };
  return {
    indent: sub ? Math.round((firstBox(sub).left - firstBox(head).left) * 100) / 100 : null,
    member: el.classList.contains("editor-palette-member"),
    fork: Boolean(el.querySelector(".editor-palette-line > .ti-git-fork")),
    head: head?.textContent.trim(),
    headShown: Boolean(head?.checkVisibility()),
    line: el.querySelector(".editor-palette-line").textContent.replace(/\s+/g, " ").trim(),
    branch: el.querySelector(".editor-palette-branch")?.textContent.trim(),
    sub: sub?.textContent.trim(),
    subBranch: Boolean(sub?.querySelector(".ti-git-branch")?.checkVisibility()),
    check: Boolean(el.querySelector(".editor-palette-check")?.checkVisibility()),
  };
}

async function projectPaletteChecks({ browser, ctx, page, run, bag, project, projectB, projectW, projectD, tag, editorURL }) {
  await run("project palette: the switcher opens a palette whose rows carry repository and branch, sorted the shared way", async () => {
    await L.createProject(page, projectB);
    // One visit makes the other project a recent one, so the shortlist has
    // a row on a pristine instance too.
    await page.goto(`${BASE}/projects/${encodeURIComponent(projectB)}/editor`, { waitUntil: "domcontentloaded" });
    await page.waitForSelector(".cm-editor", { state: "attached", timeout: 12000 });
    // The last used times are whole seconds; the next visit has to land in
    // a later one, or the recent sort falls back to the names.
    await sleep(1100);
    await page.goto(editorURL, { waitUntil: "domcontentloaded" });
    await page.waitForSelector(".cm-editor", { state: "attached", timeout: 12000 });
    // The source rows are the server's fragment, sorted by @dc/project-sort
    // like every other list; the palette clones them into its groups.
    const order = () => page.$$eval("[data-editor-project-list] [data-project-name]", (els) => els.map((e) => e.dataset.projectName));
    let names = await order();
    assert(names.indexOf(projectB) >= 0 && names.indexOf(projectB) < names.indexOf(project),
      `alpha sort order wrong: ${names.join(", ")}`);
    await page.evaluate(() => localStorage.setItem("dc-project-sort", "recent"));
    await page.reload({ waitUntil: "domcontentloaded" });
    await page.waitForSelector(".cm-editor", { state: "attached", timeout: 12000 });
    names = await order();
    assert(names.indexOf(project) >= 0 && names.indexOf(project) < names.indexOf(projectB),
      `recent sort order wrong: ${names.join(", ")}`);
    await page.evaluate(() => localStorage.removeItem("dc-project-sort"));
    // The menu leads with the switch, ahead of the open files.
    const firstItem = await page.$eval("[data-editor-menu-list] .dropdown-item", (e) => e.textContent.trim());
    assert(firstItem === "Switch project", `the menu's first entry is ${firstItem}`);
    await page.click("[data-editor-project-switch]");
    await page.waitForSelector("[data-editor-palette]", { state: "visible", timeout: 4000 });
    const current = await page.$eval("[data-editor-palette-list] .editor-palette-current", (e) => e.dataset.projectName);
    assert(current === project, `the palette marks ${current} as the current project`);
    const groups = await page.$$eval("[data-editor-palette-list] .editor-palette-group-head", (els) => els.map((e) => e.textContent.trim()));
    assert(groups[0] === "Recent" && groups[1] === "All projects", `the palette's groups are ${groups.join(", ")}`);
    const recent = await page.$$eval("[data-editor-palette-list] .editor-palette-group:first-child [data-project-name]", (els) => els.map((e) => e.dataset.projectName));
    assert(recent[0] === project && recent.includes(projectB), `the shortlist does not lead with the current project: ${recent.join(", ")}`);
    assert(await page.$(`[data-editor-palette-list] .editor-palette-group:first-child [data-project-name="${project}"].editor-palette-current .editor-palette-check`),
      "the current project's row in the shortlist carries no check");
    await page.locator(`[data-editor-palette-list] [data-project-name="${projectB}"]`).first().click();
    await page.waitForFunction((p) => decodeURIComponent(location.pathname) === `/projects/${p}/editor`, projectB, { timeout: 8000 });
    await page.waitForFunction((p) => {
      const el = document.querySelector("[data-editor-project-switch]");
      return el && el.textContent.includes(p);
    }, projectB, { timeout: 8000 });
  });

  await run("project palette: shortcut, search, arrows, Enter, and a project set that moves live", async () => {
    const projectC = `zztc-c-${tag}`;
    await page.goto(editorURL, { waitUntil: "domcontentloaded" });
    await page.waitForSelector(".cm-editor", { state: "attached", timeout: 12000 });
    const shown = () => page.$$eval("[data-editor-palette-list] [data-project-name]", (els) => els.map((el) => el.dataset.projectName));
    const marked = () => page.evaluate(() =>
      document.querySelector("[data-editor-palette-list] [data-project-name].active")?.dataset.projectName || "");
    const inField = () => page.evaluate(() => document.activeElement.matches("[data-editor-project-filter]"));

    await page.keyboard.press("Control+Shift+P");
    await page.waitForSelector("[data-editor-palette]", { state: "visible", timeout: 4000 });
    assert(await inField(), "the search field did not take the focus");
    // Enter on a fresh palette is the way back: the mark stands on the last
    // used other project, which the check before left as the previous one.
    assert(await marked() === projectB, `the open palette marks ${await marked()} instead of the last used project`);

    await page.keyboard.type(projectB);
    await sleep(200);
    let names = await shown();
    assert(names.length === 1 && names[0] === projectB, `typing did not narrow the list: ${names.join(", ")}`);
    assert(await marked() === projectB, `the first hit is not marked: ${await marked()}`);
    assert(await page.isVisible("[data-editor-project-empty]") === false, "the empty note shows while something matches");

    await page.keyboard.type("zzz");
    await sleep(200);
    assert((await shown()).length === 0 && await page.isVisible("[data-editor-project-empty]"),
      "a query nothing matches must say so");
    assert(await page.evaluate(() => {
      const note = document.querySelector("[data-editor-project-empty]").getBoundingClientRect();
      const field = document.querySelector("[data-editor-project-filter]").getBoundingClientRect();
      return note.top >= field.bottom - 1 && note.top < field.bottom + 40;
    }), "the note does not stand right under the field, where the quick open puts its own");
    assert(await marked() === "", "a list with no hits must mark nothing");
    for (let i = 0; i < 3; i += 1) await page.keyboard.press("Backspace");
    await sleep(200);

    await page.keyboard.press("Enter");
    await page.waitForFunction((p) => decodeURIComponent(location.pathname) === `/projects/${p}/editor`, projectB, { timeout: 8000 });
    await page.waitForSelector(".cm-editor", { state: "attached", timeout: 12000 });

    await page.keyboard.press("Control+Shift+P");
    await page.waitForSelector("[data-editor-palette]", { state: "visible", timeout: 4000 });
    const before = await marked();
    await page.keyboard.press("ArrowDown");
    await sleep(150);
    const after = await marked();
    assert(before === project && after !== "" && after !== project, `the arrow did not move the mark: ${before} -> ${after}`);
    assert(await inField(), "an arrow moved the focus out of the search field");
    await page.keyboard.press("Tab");
    assert(await inField(), "Tab walked out of the palette");
    await page.keyboard.press("Escape");
    await page.waitForSelector("[data-editor-palette]", { state: "hidden", timeout: 4000 });
    assert(await page.evaluate(() => !document.querySelector("[data-editor-palette]").contains(document.activeElement)),
      "closing the palette left the focus inside it");

    const other = await ctx.newPage();
    L.wirePage(other, bag);
    try {
      await L.createProject(other, projectC);
      await page.waitForFunction((p) => [...document.querySelectorAll("[data-editor-project-list] [data-project-name]")]
        .some((el) => el.dataset.projectName === p), projectC, { timeout: 8000 });
      await L.deleteProject(other, projectC);
      await page.waitForFunction((p) => ![...document.querySelectorAll("[data-editor-project-list] [data-project-name]")]
        .some((el) => el.dataset.projectName === p), projectC, { timeout: 8000 });
    } finally {
      await other.close().catch(() => {});
    }

    // A mouse open marks the best row too: the field takes the focus on a
    // fine pointer, so Enter has to mean something.
    await page.click("[data-editor-project-switch]");
    await page.waitForSelector("[data-editor-palette]", { state: "visible", timeout: 4000 });
    assert(await marked() === project, `a mouse open marked ${await marked()}`);
    // A click on the backdrop closes: the overlay's bottom left corner is
    // outside the panel, which sits centred on the top edge.
    const overlay = await page.locator("[data-editor-palette]").boundingBox();
    await page.mouse.click(overlay.x + 10, overlay.y + overlay.height - 10);
    await page.waitForSelector("[data-editor-palette]", { state: "hidden", timeout: 4000 });
  });

  await run("project palette: a worktree is found by repository and branch together and reads as both, under its main as its name over its branch, each second line under its headline's text", async () => {
    const path = await L.projectPath(page, project);
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
        return { code: d.exitCode, err: atob(d.stderr || "") };
      }, { cwd, args });
      if (res.failed || res.code !== 0) throw new Error(`git ${args.join(" ")}: ${res.failed || res.err}`);
    };
    await git(path, ["init", "-q", "-b", "master"]);
    await git(path, ["config", "user.email", "e2e@example.com"]);
    await git(path, ["config", "user.name", "e2e"]);
    await git(path, ["config", "commit.gpgsign", "false"]);
    await git(path, ["commit", "-q", "--allow-empty", "-m", "init"]);
    await git(path, ["worktree", "add", "-q", "-b", "topic/htmx", `../${projectW}`]);
    await git(path, ["worktree", "add", "-q", "--detach", `../${projectD}`]);

    await page.goto(editorURL, { waitUntil: "domcontentloaded" });
    await page.waitForSelector(".cm-editor", { state: "attached", timeout: 12000 });
    await page.waitForFunction((ps) => ps.every((p) => [...document.querySelectorAll("[data-editor-project-list] [data-project-name]")]
      .some((el) => el.dataset.projectName === p)), [projectW, projectD], { timeout: 8000 });
    const shown = () => page.$$eval("[data-editor-palette-list] [data-project-name]", (els) => els.map((el) => el.dataset.projectName));

    await page.keyboard.press("Control+Shift+P");
    await page.waitForSelector("[data-editor-palette]", { state: "visible", timeout: 4000 });
    // Repository and branch as two tokens: the main is not on that branch,
    // the sibling scratch projects have no branch at all.
    await page.keyboard.type(`${project} htmx`);
    await sleep(200);
    let names = await shown();
    assert(names.length === 1 && names[0] === projectW, `repository plus branch did not land on the worktree: ${names.join(", ")}`);
    const row = await page.locator(`[data-editor-palette-list] [data-project-name="${projectW}"]`).evaluate(readRow);
    assert(!row.member && row.head === project && row.branch === "topic/htmx" && row.sub === projectW && row.fork,
      `the worktree row does not read as repository, branch and directory: ${JSON.stringify(row)}`);
    assert(Math.abs(row.indent) <= 1, `the hit's second line starts ${row.indent}px off its headline's text`);
    // The branch alone finds it as well, the name of the directory need not
    // be known.
    await page.fill("[data-editor-project-filter]", "topic/htmx");
    await page.evaluate(() => document.querySelector("[data-editor-project-filter]").dispatchEvent(new Event("input", { bubbles: true })));
    await sleep(200);
    names = await shown();
    assert(names.length === 1 && names[0] === projectW, `the branch alone did not find the worktree: ${names.join(", ")}`);
    // Without a query the worktree stands behind its main as its member.
    await page.fill("[data-editor-project-filter]", "");
    await page.evaluate(() => document.querySelector("[data-editor-project-filter]").dispatchEvent(new Event("input", { bubbles: true })));
    await sleep(200);
    const grouped = await page.$$eval("[data-editor-palette-list] .editor-palette-group:last-child [data-project-name]", (els) => els.map((el) => `${el.dataset.projectName}${el.classList.contains("editor-palette-member") ? "*" : ""}`));
    const main = grouped.indexOf(project);
    assert(main >= 0 && grouped[main + 1] === `${projectW}*` && grouped[main + 2] === `${projectD}*`, `the worktrees do not stand behind their main: ${grouped.join(", ")}`);
    // The main above names the repository, so a member heads with its own
    // name and carries its branch below, a detached one its short hash.
    const all = "[data-editor-palette-list] .editor-palette-group:last-child";
    const member = await page.locator(`${all} [data-project-name="${projectW}"]`).evaluate(readRow);
    assert(member.head === projectW && member.headShown && member.line === projectW && !member.branch && member.sub === "topic/htmx" && member.subBranch && member.fork,
      `the member does not read as its name over its branch: ${JSON.stringify(member)}`);
    const detached = await page.locator(`${all} [data-project-name="${projectD}"]`).evaluate(readRow);
    assert(detached.head === projectD && detached.headShown && detached.line === projectD && /^[0-9a-f]{7}$/.test(detached.sub) && detached.subBranch && detached.fork,
      `the detached member does not read as its name over its short hash: ${JSON.stringify(detached)}`);
    for (const [what, r] of [["member", member], ["detached member", detached]]) {
      assert(Math.abs(r.indent) <= 1, `the ${what}'s second line starts ${r.indent}px off its headline's text`);
    }
    await page.keyboard.press("Escape");
    await page.waitForSelector("[data-editor-palette]", { state: "hidden", timeout: 4000 });
  });

  await run("project palette on a phone: the quick open's shape with the field on top, no keyboard, the desktop's order, Go takes the first hit, a worktree reads as itself in Recent and as a member below, each second line under its headline's text", async () => {
    const ctx = await browser.newContext({ ignoreHTTPSErrors: true, hasTouch: true, isMobile: true, viewport: { width: 390, height: 844 } });
    const tp = await ctx.newPage();
    L.wirePage(tp, bag);
    try {
      await L.login(tp);
      await tp.goto(editorURL, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(tp);
      await tp.waitForSelector(".cm-editor", { state: "attached", timeout: 12000 });
      if (!(await tp.isVisible("[data-editor-project-switch]"))) await tp.tap("[data-editor-drawer-toggle]");
      await tp.tap("[data-editor-project-switch]");
      await tp.waitForSelector("[data-editor-palette]", { state: "visible", timeout: 4000 });
      const state = await tp.evaluate(() => {
        const field = document.querySelector("[data-editor-project-filter]").getBoundingClientRect();
        const list = document.querySelector("[data-editor-palette-list]").getBoundingClientRect();
        const rows = [...document.querySelectorAll("[data-editor-palette-list] [data-project-name]")];
        return {
          focused: document.activeElement.matches("[data-editor-project-filter]"),
          marked: rows.some((el) => el.classList.contains("active")),
          fieldAboveList: field.bottom <= list.top + 1,
          shortest: Math.min(...rows.map((el) => el.getBoundingClientRect().height)),
          first: rows[0]?.dataset.projectName,
          firstCurrent: Boolean(rows[0]?.classList.contains("editor-palette-current") && rows[0]?.querySelector(".editor-palette-check")),
          second: rows[1]?.dataset.projectName,
          heads: [...document.querySelectorAll("[data-editor-palette-list] .editor-palette-group-head")].map((el) => el.textContent.trim()),
          scrolledTop: document.querySelector("[data-editor-palette-list]").scrollTop === 0,
          drawer: document.querySelector(".editor").classList.contains("editor-drawer-open"),
        };
      });
      assert(!state.focused, "the field took the focus on a touch screen, which raises the keyboard");
      assert(!state.marked, "a row is marked on a touch screen");
      assert(state.fieldAboveList, "the field does not stand above the list, the way the quick open's does");
      // The rows keep the floor the quick open's chooser rows keep, 2.25rem.
      assert(state.shortest >= 36, `a row is only ${state.shortest}px tall`);
      // The same order as on the desktop: the groups top down, the best row
      // on top, and the list standing at its top.
      assert(state.heads[0] === "Recent" && state.heads[1] === "All projects", `the phone's groups are ${state.heads.join(", ")}`);
      assert(state.first === project && state.firstCurrent, `the first row is ${state.first}, not the current project with its check`);
      assert(state.second === projectB, `the row under it is ${state.second}, not the last used other project`);
      assert(state.scrolledTop, "the list opened scrolled away from its top");
      assert(!state.drawer, "the drawer stayed open under the palette");
      await tp.tap("[data-editor-project-filter]");
      await tp.keyboard.type(projectW);
      await sleep(200);
      await tp.keyboard.press("Enter");
      await tp.waitForFunction((p) => decodeURIComponent(location.pathname) === `/projects/${p}/editor`, projectW, { timeout: 8000 });
      // Now the worktree is the current project: Recent leads with it as
      // itself, the full list carries it as its main's member, both checked.
      await tp.waitForSelector(".cm-editor", { state: "attached", timeout: 12000 });
      if (!(await tp.isVisible("[data-editor-project-switch]"))) await tp.tap("[data-editor-drawer-toggle]");
      await tp.tap("[data-editor-project-switch]");
      await tp.waitForSelector("[data-editor-palette]", { state: "visible", timeout: 4000 });
      const recent = await tp.locator(`[data-editor-palette-list] .editor-palette-group:first-child [data-project-name="${projectW}"]`).evaluate(readRow);
      assert(!recent.member && recent.head === project && recent.headShown && recent.branch === "topic/htmx" && recent.sub === projectW && recent.check,
        `the worktree's Recent row does not read as repository, branch and directory: ${JSON.stringify(recent)}`);
      const all = "[data-editor-palette-list] .editor-palette-group:last-child";
      const member = await tp.locator(`${all} [data-project-name="${projectW}"]`).evaluate(readRow);
      assert(member.member && member.head === projectW && member.line === projectW && member.sub === "topic/htmx" && member.subBranch && member.check,
        `the current member does not read as its name over its branch with the check: ${JSON.stringify(member)}`);
      for (const [what, r] of [["Recent row", recent], ["member", member]]) {
        assert(Math.abs(r.indent) <= 1, `the phone's ${what} has its second line ${r.indent}px off its headline's text`);
      }
      const detached = tp.locator(`${all} [data-project-name="${projectD}"]`);
      await detached.scrollIntoViewIfNeeded();
      await detached.tap();
      await tp.waitForFunction((p) => decodeURIComponent(location.pathname) === `/projects/${p}/editor`, projectD, { timeout: 8000 });
    } finally {
      await ctx.close();
    }
  });
}

module.exports = { projectPaletteChecks };
