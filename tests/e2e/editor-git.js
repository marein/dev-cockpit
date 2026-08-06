const L = require("./lib");
const { assert, sleep, BASE } = L;

// Editor git: what the editor shows about the repository a project sits in,
// and the two git switches of a file tab, Git diff and Git blame. Routes:
// GET /projects/:name/editor/git/changes (what the working copy carries, one
// entry per changed path with the line counts, plus the repo flag), GET
// .../git/file?path= (the file at HEAD; no route ever answers a diff,
// @codemirror/merge computes it in the browser), GET .../git/blame,
// POST .../git/watch. The watch is what starts and stops the
// server's per-project poller: it only runs while a client says it is
// watching, it compares a fingerprint (HEAD, the status output) and publishes a
// bare "git" event over the shared /events stream when that moves. The event
// carries no state, dc-editor pulls the status itself, exactly like the tab
// strip pulls its fragment on "terminals".
// Marks are computed in the browser out of the flat status list: a changed file
// carries a letter at the end of its row, a folder carries a dot for the most
// pressing thing under it (conflict > deleted > modified > renamed > added >
// untracked; untracked carries its own cyan U, so it never reads as a staged
// file), and both take their color from Tabler's text utilities so they
// follow the OS theme. Gotchas:
//   - a scratch project is not a repository, so this runner makes one through a
//     shell in that project (git init plus one commit). The one route that
//     writes git is the commit (GET/POST .../git/commit, the GET carries the
//     branch and the last message): a pathspec commit of exactly the checked
//     rows, untracked paths through intent-to-add, staged work on other paths
//     untouched. Init, add, merge and everything else stay with the shell.
//   - the "git" event refreshes the status, not the file tree. A file created
//     elsewhere has no row yet, so the live check changes a file that is
//     already rendered, and the new-file case goes through the refresh button,
//     which pulls both.
//   - the tree is lazy: a file inside a folder only has a row once that folder
//     has been opened.
//   - the git editor settings are one form behind the Git tab
//     (/settings/editor/git). The bare path and the sidebar's Editor row lead to
//     the leftmost tab, which is Search; that tab is covered in editor.js. They are shared state on the instance, so
//     the defaults are read before anything is saved and the poll interval is
//     put back at the end. There is no switch for git itself: it shows where a
//     repository is and nothing where there is none.
//   - the diff always compares against HEAD and is one switch in the file's
//     context menu (tab and tree row), reading "Show git diff" / "Hide git
//     diff"; it is not in the editor menu, and without a repository the menu
//     carries no git entries at all. `dc-editor` carries `data-git-repo` once
//     the status answered, which is what diffReady waits on. The tab stores
//     the revision it compares against (`diff: "HEAD"`), not a boolean, so a
//     revision picker later fills the same field. How a comparison looks, side
//     by side against inline and whether unchanged parts are folded, is the
//     editor's own setting, per device in
//     `dc-editor-settings` and never on the server, picked in the sheet behind
//     the menu's Editor settings entry; automatic decides by the width and
//     keeps deciding while a comparison is open, so resizing the window across
//     the lg breakpoint rebuilds it in the other view (a picked view stands,
//     the window never overrules it). Either
//     applies to an open comparison on the spot, so a check that needs one view
//     waits for the comparison to stand and then switches, no reload. The
//     settings page keeps only what describes the install: the poll interval
//     and the two size limits, and it has no checkbox left at all.
//   - side by side is a MergeView with two editors (.cm-mergeView, the revision
//     first and read only), inline is unifiedMergeView on the one editor, which
//     renders the revision as decorations (.cm-deletedChunk / .cm-changedLine).
//     The working copy keeps its buffer across every switch, which is what the
//     save check proves.
//   - asking the DOM whether the merge view exists is not enough, and that is
//     exactly how the side by side view shipped broken once: the plain editor
//     sat on top of it, because CodeMirror's base theme carries
//     `display: flex !important` and an inline `display: none` cannot hide it.
//     So one check asks elementFromPoint what a person actually looks at.
//   - the settings form posts to the page it is rendered on, so waiting for the
//     URL returns before the request goes out and the next navigation cancels
//     it. Wait for the POST response (saveEditorSettings).
//   - the fingerprint the poller compares carries two parts: HEAD and the
//     working copy (the status output). The event says which of them moved, and
//     only a moved HEAD makes an open diff fetch its revision again, so a save
//     costs no git show at all; a commit updates the revision side in place.
//     One check counts the requests to prove both directions.
//   - the checks that count requests write through the editor page's own fetch
//     instead of a second page. The editor catches up when it comes back to the
//     front, so a page opening and closing beside it could hand it the change
//     the check is trying to prove nobody handed it.
//   - the poll interval is read again before every round, so zero stops a
//     poller that is already running. Proving that needs a window in which
//     nothing may arrive, which is why that check is slow on purpose.
//   - every git command here carries its author flags, the merge included: it
//     writes MERGE_HEAD and needs a committer for that, and an instance
//     started with a scratch HOME has no global identity to fall back on.
//   - the blame gutter shows what git has, so it disappears while the buffer is
//     dirty and comes back on the save. The switch belongs to the file: it is
//     an entry of the file's context menu (tab and tree row), rides on the tab,
//     and persists in the saved tab entry as `blame: true`, never in a key of
//     its own.
//   - a comparison of two files is a tab keyed by a synthetic path that starts
//     with a double slash, so it can never collide with a file. It is persisted
//     as its two paths and rebuilt from the disk on restore, so unsaved changes
//     fall away like they do for every other tab.
//   - the saved tab set carries typed entries; a bare string is an older state
//     and reads as a file, and the legacy diff map still switches the diff on.
//     One check seeds exactly that legacy shape.
//   - the change bars (.cm-changes) are always on where a repository is: no
//     switch, no menu entry, computed against HEAD in the browser and updated
//     by the keystroke. Green is a new line, blue a changed one, a grey tick
//     sits on the boundary deleted lines vanished from. They rest while a
//     diff or a comparison is up and are absent for a file HEAD does not
//     hold. The gutter keeps a hidden spacer cell for its width, checks must
//     skip it.
//   - never wait for "[x][hidden]" and never assert on the attribute when the
//     claim is that something is gone. Tabler's display utilities are important
//     and sit below its own [hidden] rule, so an element with a d-* class was
//     laid out while the attribute said hidden; style.css now settles that, and
//     the checks at the end read computed display and the box instead.
//     An entry inside a closed dropdown has no box, so "is it there" is read
//     with the menu open (menuBoxes) and only "does it apply" from `hidden`.
//     The phone checks also open the drawer themselves: it only opens by itself
//     while nothing is open, and an earlier check may have left a file open.

L.runFeature("EDITOR GIT", async ({ ctx, page, run, bag, mobilePage }) => {
  const tag = `git-${Date.now().toString(36)}`;
  const project = `zzgit-${tag}`;
  const plain = `zzgit-plain-${tag}`;
  const editorBase = `/projects/${encodeURIComponent(project)}/editor`;
  const editorURL = `${BASE}${editorBase}`;
  const tracked = "sub/tracked.txt";
  // A file whose middle word changes, so a line carries both marks at once: the
  // band over the whole line and the word diff inside it.
  const worded = "sub/word.txt";
  const wordedBefore = "alpha\nbeta gamma delta\nepsilon\n";
  const wordedAfter = "alpha\nbeta omega delta\nepsilon\n";
  const author = "-c user.email=e2e@example.com -c user.name=e2e -c commit.gpgsign=false";
  let shellUrl = "";

  // post drives one of the editor's own JSON routes from an open app page, the
  // way the browser does, CSRF header included.
  const post = (target, path, fields) => target.evaluate(([p, f]) => {
    const token = document.querySelector('meta[name="csrf-token"]').content;
    return fetch(p, {
      method: "POST",
      headers: {
        "Content-Type": "application/x-www-form-urlencoded",
        "X-CSRF-Token": token,
        Accept: "application/json",
      },
      body: new URLSearchParams(f).toString(),
    }).then((r) => r.status);
  }, [path, fields]);

  // The editor settings are one form behind one tab. It posts to the page it is
  // rendered on, so waiting for the URL would return before the request even
  // goes out and the next navigation would cancel it. WebKit drops the save
  // that way, chromium happened to get it through.
  const openEditorSettings = async () => {
    await page.goto(`${BASE}/settings/editor/git`, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    await page.waitForSelector("#settings-editor-git", { timeout: 10000 });
  };
  const saveEditorSettings = async () => {
    const [response] = await Promise.all([
      page.waitForResponse((r) => r.url().includes("/settings/editor/git") && r.request().method() === "POST", { timeout: 15000 }),
      page.locator('#settings-editor-git button[type="submit"]').click(),
    ]);
    assert(response.status() < 400, `saving the editor settings answered ${response.status()}`);
    await sleep(300);
  };
  // Every stored editor setting, read off the freshly loaded page.
  const editorValues = async () => {
    await openEditorSettings();
    return page.evaluate(() => {
      const form = document.getElementById("settings-editor-git");
      const read = (el) => (el.type === "checkbox" ? el.checked : el.value);
      return Object.fromEntries([...form.querySelectorAll("[name]")]
        .filter((el) => el.name !== "csrf_token")
        .map((el) => [el.name, read(el)]));
    });
  };
  // How a comparison looks is not a server setting: the view and the folding
  // live in the editor's own settings, per device, reached through the sheet
  // the menu's Editor settings entry opens, on every width.
  const openEditorSettingsSheet = async (target, key) => {
    await target.click("[data-editor-menu]");
    await target.waitForSelector("[data-editor-menu-list].show", { timeout: 4000 });
    await target.click("[data-editor-settings-item]");
    await target.waitForSelector(`[data-editor-sheet] [data-editor-setting="${key}"]`, { timeout: 6000 });
  };
  const closeEditorSettingsSheet = async (target) => {
    await sleep(300);
    await target.click("[data-editor-sheet-close]");
    await target.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 6000 });
    await sleep(200);
  };
  const setDiffView = async (target, view) => {
    await openEditorSettingsSheet(target, "diff_view");
    await target.selectOption('[data-editor-setting="diff_view"]', view);
    await closeEditorSettingsSheet(target);
  };
  const setEditorSwitch = async (target, key, on) => {
    await openEditorSettingsSheet(target, key);
    await target.setChecked(`[data-editor-setting="${key}"]`, on);
    await closeEditorSettingsSheet(target);
  };

  const gitChanges = (target, name) => target.evaluate((n) =>
    fetch(`/projects/${encodeURIComponent(n)}/editor/git/changes`, { headers: { Accept: "application/json" } })
      .then((r) => r.json()), name);

  // The two git switches live in the file's context menu, on the tab and on
  // the tree row. diffReady waits for the status answer the entries render on
  // (the root says whether there is a repository), toggleDiff opens the active
  // tab's menu and clicks the diff entry the way a person does, diffPressed
  // reads whether the entry offers to hide, which is what "on" looks like in a
  // menu that is rebuilt on every open.
  const diffReady = (target, applies = true) => target.waitForFunction((want) => {
    const root = document.querySelector("dc-editor");
    return !!root && root.dataset.gitRepo != null && (root.dataset.gitRepo === "1") === want;
  }, applies, { timeout: 20000 });
  const diffEntry = async (target, what) => {
    await target.click(".editor-tab.active", { button: "right" });
    await target.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
    const item = target.locator(".dc-context-menu .dropdown-item", { hasText: /git diff/ });
    if (what === "click") {
      await item.first().click();
      await sleep(400);
      return null;
    }
    const label = (await item.count()) ? (await item.first().textContent()).trim() : null;
    await target.keyboard.press("Escape");
    await sleep(200);
    return label;
  };
  const toggleDiff = (target) => diffEntry(target, "click");
  const diffPressed = async (target) => await diffEntry(target, "label") === "Hide git diff";
  // The context menus of a file, on its tab and on its tree row, carry the
  // per-file git actions. menuItem finds an entry in the one open menu, pick
  // opens a row's menu and clicks one, menuLabel only reads and closes again.
  const menuItem = (label) => page.locator(".dc-context-menu .dropdown-item", { hasText: new RegExp(label) });
  const pick = async (row, label) => {
    await page.click(row, { button: "right" });
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
    await menuItem(label).first().click();
    await sleep(500);
  };
  const menuLabel = async (row, label) => {
    await page.click(row, { button: "right" });
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
    const item = menuItem(label);
    const value = (await item.count()) ? (await item.first().textContent()).trim() : null;
    await page.keyboard.press("Escape");
    await sleep(200);
    return value;
  };

  const markOf = (target, path) => target.evaluate((p) => {
    const row = document.querySelector(`.editor-item[data-path="${p}"]`);
    if (!row) return null;
    const mark = row.querySelector("[data-git-mark]");
    return { status: row.dataset.gitStatus || "", mark: mark ? mark.textContent : "" };
  }, path);

  try {
    await L.createProject(page, project);
    await L.createProject(page, plain);

    await run("settings: the git tab, one form, and only what describes the install", async () => {
      // The bare path leads to the leftmost tab, like a coder's base path leads
      // to its first section. That is Search; git is the tab next to it.
      await page.goto(`${BASE}/settings/editor`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      assert(/\/settings\/editor\/search$/.test(page.url()), `the bare path landed on ${page.url()}`);
      await page.goto(`${BASE}/settings/editor/git`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      const tabs = await page.locator("[data-editor-sections] .nav-link").evaluateAll((els) => els.map((e) => e.getAttribute("href")));
      assert(tabs.join() === "/settings/editor/search,/settings/editor/git", `the tabs are ${tabs.join(", ")}`);
      const active = await page.locator("[data-editor-sections] .nav-link.active").getAttribute("href");
      assert(active === "/settings/editor/git", `the marked tab is ${active}`);
      assert(await page.$('[data-settings-nav] a[href="/settings/editor/search"].active'), "the Editor row is not marked in the settings nav");
      assert(await page.locator("#settings-editor-git").count() === 1, "there is not exactly one form");

      const values = await editorValues();
      assert(values.git_poll_seconds === "2", `poll seconds: ${values.git_poll_seconds}`);
      assert(values.diff_max_lines === "5000", `diff max lines: ${values.diff_max_lines}`);
      assert(values.diff_max_kib === "512", `diff max KiB: ${values.diff_max_kib}`);
      // How a comparison looks belongs to the screen, not to the install:
      // neither the view nor the folding is on this page, and the server stores
      // nothing about them. The limits stay, they are a house rule.
      assert(Object.keys(values).sort().join() === "diff_max_kib,diff_max_lines,git_poll_seconds",
        `the form carries more than the install's own settings: ${Object.keys(values).join(", ")}`);

      // One save writes every field of the page.
      await page.fill('#settings-editor-git [name="git_poll_seconds"]', "1");
      await page.fill('#settings-editor-git [name="diff_max_lines"]', "4000");
      await saveEditorSettings();
      const stored = await editorValues();
      assert(stored.git_poll_seconds === "1", `poll seconds after save: ${stored.git_poll_seconds}`);
      assert(stored.diff_max_lines === "4000", `diff max lines after save: ${stored.diff_max_lines}`);

      // Back to the defaults, the diff checks further down read these.
      await page.fill('#settings-editor-git [name="diff_max_lines"]', "5000");
      await saveEditorSettings();
      return "search tab first, git next to it, one POST, three values";
    });

    await run("a project without a repository answers no repo and marks nothing", async () => {
      const changes = await gitChanges(page, plain);
      assert(changes.repo === false, "a plain directory is reported as a repository");
      assert(Array.isArray(changes.worktree) && changes.worktree.length === 0, `worktree: ${JSON.stringify(changes.worktree)}`);
      await page.goto(`${BASE}/projects/${encodeURIComponent(plain)}/editor`, { waitUntil: "domcontentloaded" });
      await L.waitUpgraded(page, ["dc-editor"]);
      await sleep(1500);
      assert(!(await page.$("[data-git-status]")), "a project without a repository shows marks");
      assert(!(await page.$("[data-git-mark]")), "a project without a repository shows marks");
      return "no repo, no surface";
    });

    // The scratch project becomes a repository through a shell in it: the app
    // itself never writes git.
    await page.goto(editorURL, { waitUntil: "domcontentloaded" });
    await L.waitUpgraded(page, ["dc-editor"]);
    assert(await post(page, `${editorBase}/mkdir`, { path: "sub" }) === 200, "mkdir sub failed");
    assert(await post(page, `${editorBase}/file`, { path: tracked, content: "one\n" }) === 200, `write ${tracked} failed`);
    assert(await post(page, `${editorBase}/file`, { path: "root.txt", content: "root\n" }) === 200, "write root.txt failed");
    assert(await post(page, `${editorBase}/file`, { path: worded, content: wordedBefore }) === 200, `write ${worded} failed`);

    shellUrl = await L.createShell(page, project);
    await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
    await sleep(2000);
    await page.evaluate((href) => {
      const token = document.querySelector('meta[name="csrf-token"]').content;
      const command = "git init -q && git add -A && "
        + "git -c user.email=e2e@example.com -c user.name=e2e -c commit.gpgsign=false commit -qm init\r";
      return fetch(href + "/input", {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-CSRF-Token": token },
        body: JSON.stringify({ items: [{ raw: command }] }),
      });
    }, new URL(shellUrl).pathname);

    await run("a repository with a commit answers repo with a clean tree", async () => {
      const deadline = Date.now() + 45000;
      let changes = null;
      while (Date.now() < deadline) {
        changes = await gitChanges(page, project);
        if (changes.repo && changes.worktree.length === 0) break;
        await sleep(1000);
      }
      assert(changes && changes.repo, "the project never became a repository");
      assert(changes.worktree.length === 0, `a fresh commit left changes: ${JSON.stringify(changes.worktree)}`);
      return "repo true, nothing changed";
    });

    await run("a change made elsewhere reaches an open editor without a reload", async () => {
      await page.goto(editorURL, { waitUntil: "domcontentloaded" });
      await L.waitUpgraded(page, ["dc-editor"]);
      await page.waitForSelector('.editor-item[data-path="sub"]', { timeout: 10000 });
      await page.click('.editor-item[data-path="sub"]');
      await page.waitForSelector(`.editor-item[data-path="${tracked}"]`, { timeout: 10000 });
      await sleep(1500);
      assert(!(await page.$("[data-git-status]")), "a clean repository marks rows");

      // A second page writes the file, the open one is never touched again.
      const other = await ctx.newPage();
      L.wirePage(other, bag);
      try {
        await other.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
        await L.dismissUpdate(other);
        assert(await post(other, `${editorBase}/file`, { path: tracked, content: "one\ntwo\n" }) === 200, "the second page could not save");
      } finally {
        await other.close().catch(() => {});
      }

      await page.waitForSelector(`.editor-item[data-path="${tracked}"][data-git-status="modified"]`, { timeout: 20000 });
      await page.waitForSelector('.editor-item[data-path="sub"][data-git-status="modified"]', { timeout: 5000 });
      const file = await markOf(page, tracked);
      const folder = await markOf(page, "sub");
      assert(file.mark === "M", `file mark: ${file.mark}`);
      assert(folder.mark === "•", `folder mark: ${folder.mark}`);
      const colored = await page.$(`.editor-item[data-path="${tracked}"] .editor-item-name.text-yellow`);
      assert(colored, "the changed file's name is not colored");
      return "modified file and its folder marked live";
    });

    await run("the refresh button pulls the status with the files", async () => {
      const other = await ctx.newPage();
      L.wirePage(other, bag);
      try {
        await other.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
        await L.dismissUpdate(other);
        assert(await post(other, `${editorBase}/file`, { path: "fresh.txt", content: "new\n" }) === 200, "the second page could not create the file");
      } finally {
        await other.close().catch(() => {});
      }
      await page.click("[data-editor-refresh]");
      await page.waitForSelector('.editor-item[data-path="fresh.txt"][data-git-status="untracked"]', { timeout: 15000 });
      const fresh = await markOf(page, "fresh.txt");
      assert(fresh.mark === "U", `untracked mark: ${fresh.mark}`);
      assert(await page.$('.editor-item[data-path="fresh.txt"] .editor-item-name.text-cyan'), "an untracked file's name is not cyan");
      return "a new file arrives marked untracked";
    });

    // openTracked leaves the page on the editor with sub/tracked.txt as the
    // active tab and the diff entry applying.
    const openTracked = async () => {
      await page.goto(editorURL, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      await L.waitUpgraded(page, ["dc-editor"]);
      await page.waitForSelector(".cm-editor", { state: "attached", timeout: 15000 });
      if (!(await page.$(`.editor-item[data-path="${tracked}"]`))) {
        await page.click('.editor-item[data-path="sub"]');
      }
      await page.waitForSelector(`.editor-item[data-path="${tracked}"]`, { timeout: 10000 });
      await page.click(`.editor-item[data-path="${tracked}"]`);
      await page.waitForSelector(`.editor-tab[data-path="${tracked}"]`, { timeout: 10000 });
      await diffReady(page);
    };
    // CodeMirror covers its content with its own layers, so playwright never
    // finds a "clickable" spot; force dispatches the click at the element the
    // way a person's pointer lands on it. Clicking is what decides where the
    // keystrokes go, and the read only side must not take them.
    const clickPane = (index) => page.locator(".cm-mergeView .cm-content").nth(index).click({ force: true });
    const dismissSwal = async () => {
      const cancel = page.locator(".swal2-cancel");
      if (await cancel.count() && await cancel.first().isVisible()) {
        await cancel.first().click();
        await page.waitForSelector(".swal2-container", { state: "detached", timeout: 6000 }).catch(() => {});
      }
    };

    // The change bars have no switch: with a repository they ride in their own
    // gutter (.cm-changes), computed against HEAD in the browser and following
    // the buffer live. readBars classifies by the inline style the markers
    // carry (the Tabler variable names, so no computed colors), and skips the
    // spacer cell CodeMirror hides to keep the gutter's width.
    const readBars = (target) => target.evaluate(() => {
      const out = [];
      for (const cell of document.querySelectorAll(".cm-changes .cm-gutterElement")) {
        if (cell.style.visibility === "hidden") continue;
        for (const el of cell.children) {
          const bg = el.style.background || "";
          const ticks = [...el.children].map((c) => (c.style.top ? "top" : "bottom"));
          if (bg.includes("azure")) out.push(ticks.length ? "mod+tick" : "mod");
          else if (bg.includes("green")) out.push("add");
          else if (ticks.length) out.push(`del:${ticks.join("+")}`);
        }
      }
      return out;
    });
    const waitBars = async (target, want, label) => {
      const deadline = Date.now() + 15000;
      let bars = [];
      while (Date.now() < deadline) {
        bars = await readBars(target);
        if (JSON.stringify(bars) === JSON.stringify(want)) return;
        await sleep(250);
      }
      assert(false, `${label}: the gutter carries ${JSON.stringify(bars)}, not ${JSON.stringify(want)}`);
    };

    await run("change bars: always on, following the buffer live, resting under a comparison", async () => {
      await openTracked();
      // HEAD holds one line, the working copy a second one: one green bar.
      await waitBars(page, ["add"], "after opening");
      await page.locator(".cm-content").first().click({ force: true });
      await page.keyboard.press("Control+Home");
      await page.keyboard.press("End");
      await page.keyboard.type("X");
      // The edit joins the new line into one changed region: both lines read
      // as modified now, live, without a save and without a request.
      await waitBars(page, ["mod", "mod"], "after typing");
      await page.keyboard.press("Control+z");
      await waitBars(page, ["add"], "after undo");
      // A comparison shows the changes itself, so the bars rest under it, in
      // both views, and come back when it goes away.
      await toggleDiff(page);
      await page.waitForSelector(".cm-mergeView", { timeout: 20000 });
      assert((await page.locator(".cm-mergeView .cm-changes").count()) === 0, "the merge view carries the change bars");
      await toggleDiff(page);
      await page.waitForSelector(".cm-mergeView", { state: "detached", timeout: 10000 });
      await waitBars(page, ["add"], "after the diff went away");
      return "green for the new line, live to the keystroke, resting under the diff";
    });

    await run("change bars: a clean file shows none, a deleted line leaves a tick on the boundary", async () => {
      await page.click(`.editor-item[data-path="${worded}"]`);
      await page.waitForSelector(`.editor-tab[data-path="${worded}"].active`, { timeout: 10000 });
      await waitBars(page, [], "on a committed unchanged file");
      await page.locator(".cm-content").first().click({ force: true });
      await page.keyboard.press("Control+Home");
      await page.keyboard.press("ArrowDown");
      await page.keyboard.press("Home");
      await page.keyboard.down("Shift");
      await page.keyboard.press("ArrowDown");
      await page.keyboard.up("Shift");
      await page.keyboard.press("Delete");
      await waitBars(page, ["del:top"], "after deleting the middle line");
      await page.keyboard.press("Control+z");
      await waitBars(page, [], "after undo");
      await page.click(`.editor-tab[data-path="${worded}"] .editor-tab-close`);
      await sleep(400);
      return "nothing on a clean file, a grey tick where the line vanished";
    });

    await run("change bars: a file HEAD does not hold shows no gutter at all", async () => {
      await page.click('.editor-item[data-path="fresh.txt"]');
      await page.waitForSelector('.editor-tab[data-path="fresh.txt"].active', { timeout: 10000 });
      await sleep(1500);
      assert((await page.locator(".cm-changes").count()) === 0, "an untracked file carries a changes gutter");
      await page.click('.editor-tab[data-path="fresh.txt"] .editor-tab-close');
      await sleep(400);
      return "untracked means nothing to compare, no gutter";
    });

    await run("diff: the switch puts HEAD on a read only side", async () => {
      await openTracked();
      await toggleDiff(page);
      await page.waitForSelector(".cm-mergeView", { timeout: 20000 });
      const panes = await page.locator(".cm-mergeView .cm-content").count();
      assert(panes === 2, `panes: ${panes}`);
      const editable = await page.evaluate(() =>
        [...document.querySelectorAll(".cm-mergeView .cm-content")].map((el) => el.getAttribute("contenteditable")));
      assert(editable[0] === "false", `the revision side is editable: ${editable[0]}`);
      assert(editable[1] === "true", `the working copy side is not editable: ${editable[1]}`);
      const revisionText = await page.locator(".cm-mergeView .cm-content").nth(0).textContent();
      assert(revisionText.includes("one"), "the revision side does not hold the committed line");
      assert(!revisionText.includes("two"), "the revision side holds the working copy's change");
      assert(await diffPressed(page), "the diff entry does not read as comparing");
      return "two panes, HEAD on the left, read only";
    });

    // The check that would have caught the plain editor painting over the merge
    // view: everything above only asks what is in the DOM, this asks what a
    // person actually looks at and clicks on.
    await run("diff: the side by side view is the layer you see and can scroll", async () => {
      const seen = await page.evaluate(() => {
        const surface = document.querySelector("[data-editor-surface]");
        const rect = surface.getBoundingClientRect();
        const at = (fx, fy) => {
          const el = document.elementFromPoint(rect.left + rect.width * fx, rect.top + rect.height * fy);
          if (!el) return "nothing";
          if (el.closest(".cm-mergeView")) return "merge";
          if (el.closest("[data-editor-surface] > .cm-editor")) return "plain editor";
          return el.className || el.tagName;
        };
        const plain = document.querySelector("[data-editor-surface] > .cm-editor");
        const merge = document.querySelector(".cm-mergeView");
        merge.scrollTop = merge.scrollHeight; // a diff longer than the box must reach its end
        return {
          left: at(0.25, 0.5),
          right: at(0.75, 0.5),
          plainVisibility: getComputedStyle(plain).visibility,
          scrollable: merge.scrollHeight >= merge.clientHeight,
          scrolled: merge.scrollTop,
          scrollHeight: merge.scrollHeight,
          clientHeight: merge.clientHeight,
        };
      });
      assert(seen.left === "merge", `the left half of the surface shows ${seen.left}`);
      assert(seen.right === "merge", `the right half of the surface shows ${seen.right}`);
      assert(seen.plainVisibility === "hidden", `the plain editor is still on screen: ${seen.plainVisibility}`);
      // A file this small fits the box, so scrolling is only checked when there
      // is something to scroll; the point is that the panes are not clipped.
      if (seen.scrollHeight > seen.clientHeight) {
        assert(seen.scrolled > 0, `the diff is clipped at one screenful: ${JSON.stringify(seen)}`);
      }
      return `both halves show the diff, the plain editor is ${seen.plainVisibility}`;
    });

    await run("diff: the working copy side edits and saves, the revision side does not", async () => {
      await clickPane(1);
      await page.keyboard.press("Control+End");
      await page.keyboard.type("three");
      await page.waitForSelector(`.editor-tab[data-path="${tracked}"].dirty`, { timeout: 6000 });
      await page.click("[data-editor-save]");
      await page.waitForSelector(`.editor-tab[data-path="${tracked}"]:not(.dirty)`, { timeout: 10000 });
      const onDisk = await page.evaluate(([b, p]) =>
        fetch(`${b}/file?path=${encodeURIComponent(p)}`, { headers: { Accept: "application/json" } })
          .then((r) => r.json()), [editorBase, tracked]);
      assert(onDisk.content.includes("three"), `the save did not reach the disk: ${JSON.stringify(onDisk.content)}`);

      const before = await page.locator(".cm-mergeView .cm-content").nth(0).textContent();
      await clickPane(0);
      await page.keyboard.press("Control+End");
      await page.keyboard.type("zzz");
      await sleep(300);
      const after = await page.locator(".cm-mergeView .cm-content").nth(0).textContent();
      assert(after === before, "the revision side took an edit");
      // The keystrokes must not have gone anywhere else either.
      assert(!(await page.$(`.editor-tab[data-path="${tracked}"].dirty`)), "typing on the revision side reached the buffer");
      return "saved through the normal route, revision side untouched";
    });

    // The marking itself: a changed line has to read as changed, and the words
    // that actually differ have to read stronger than the line they sit in.
    // Both are CSS over what @codemirror/merge marks, so both are read back as
    // computed colors. The view comes from the setting, so each round sets it
    // and comes back to the editor, where the switch rides the reload.
    await run("diff: a changed line is marked and the changed words stronger, in both views", async () => {
      const other = await ctx.newPage();
      L.wirePage(other, bag);
      try {
        await other.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
        await L.dismissUpdate(other);
        assert(await post(other, `${editorBase}/file`, { path: worded, content: wordedAfter }) === 200, "the word change could not be written");
      } finally {
        await other.close().catch(() => {});
      }

      const openWorded = async () => {
        await page.goto(editorURL, { waitUntil: "domcontentloaded" });
        await L.dismissUpdate(page);
        await L.waitUpgraded(page, ["dc-editor"]);
        await page.waitForSelector(".cm-editor", { state: "attached", timeout: 20000 });
        if (!(await page.$(`.editor-item[data-path="${worded}"]`))) await page.click('.editor-item[data-path="sub"]');
        await page.waitForSelector(`.editor-item[data-path="${worded}"]`, { timeout: 10000 });
        await page.click(`.editor-item[data-path="${worded}"]`);
        await page.waitForSelector(`.editor-tab[data-path="${worded}"].active`, { state: "attached", timeout: 10000 });
        await diffReady(page);
      };

      // The colors of one changed line, its changed words, and an untouched
      // line next to it. Read from whichever view is up.
      const colors = () => page.evaluate(() => {
        const scope = document.querySelector(".cm-mergeView") || document.querySelector("[data-editor-surface] > .cm-editor");
        const changed = (l) => l.classList.contains("cm-changedLine") || l.classList.contains("cm-inlineChangedLine");
        const lines = [...scope.querySelectorAll(".cm-line")];
        const bg = (el) => (el ? getComputedStyle(el).backgroundColor : null);
        return {
          line: bg(lines.find(changed)),
          // Not the active line: CodeMirror tints the one the cursor sits in,
          // which says nothing about the diff.
          plain: bg(lines.find((l) => !changed(l) && !l.classList.contains("cm-activeLine"))),
          word: bg(scope.querySelector(".cm-changedText")),
          // The removed block carries the band, its lines sit inside it.
          deleted: bg(scope.querySelector(".cm-deletedChunk")),
          deletedWord: bg(scope.querySelector(".cm-deletedText")),
        };
      });
      const opaque = (color) => color && color !== "rgba(0, 0, 0, 0)" && color !== "transparent";
      const results = {};
      let diffOn = false;
      for (const mode of ["side", "inline"]) {
        await openWorded();
        if (!diffOn) {
          await toggleDiff(page);
          diffOn = true; // from here on the switch rides the tab through reloads
        }
        // The view is picked in the editor's own settings and applies to the
        // open comparison right there, so the comparison has to stand before
        // the switch: a build still in flight would carry the old view.
        await page.waitForSelector(".cm-mergeView, .cm-deletedChunk, .cm-changedLine", { state: "attached", timeout: 20000 });
        await setDiffView(page, mode);
        if (mode === "side") await page.waitForSelector(".cm-mergeView", { timeout: 20000 });
        else await page.waitForSelector(".cm-deletedChunk, .cm-changedLine", { state: "attached", timeout: 20000 });
        await page.waitForSelector(".cm-changedText", { state: "attached", timeout: 20000 });
        if (mode === "inline") {
          assert(await page.locator(".cm-mergeView").count() === 0, "inline still shows the two pane view");
          assert(await page.locator(".cm-content").count() === 1, "inline left more than one editor");
          await page.waitForSelector(".cm-deletedText", { state: "attached", timeout: 20000 });
        }
        await sleep(800);
        const seen = await colors();
        assert(opaque(seen.line), `${mode}: the changed line carries no mark (${seen.line})`);
        assert(!opaque(seen.plain), `${mode}: an untouched line is marked too (${seen.plain})`);
        assert(seen.line !== seen.plain, `${mode}: changed and untouched line look the same (${seen.line})`);
        assert(opaque(seen.word), `${mode}: the changed words carry no mark (${seen.word})`);
        assert(seen.word !== seen.line, `${mode}: the changed words look like the line around them (${seen.word})`);
        if (mode === "inline") {
          assert(opaque(seen.deleted), `inline: the removed lines carry no mark (${seen.deleted})`);
          assert(opaque(seen.deletedWord), `inline: the removed words carry no mark (${seen.deletedWord})`);
          assert(seen.deletedWord !== seen.deleted, "inline: the removed words look like the block around them");
        }
        results[mode] = seen;
      }
      assert(await diffPressed(page), "the switch did not ride the reload");
      await toggleDiff(page); // off again
      await sleep(400);
      await setDiffView(page, "auto");
      await openTracked();
      return `side ${results.side.line} / ${results.side.word}, inline ${results.inline.line} / ${results.inline.word}`;
    });

    await run("how a comparison looks is this device's setting, not the install's", async () => {
      await openTracked();
      await setDiffView(page, "inline");
      const readSettings = (target) => target.evaluate(() => JSON.parse(localStorage.getItem("dc-editor-settings") || "{}"));
      assert((await readSettings(page)).diff_view === "inline",
        `the view is not in this device's editor settings: ${JSON.stringify(await readSettings(page))}`);

      // The folding sits next to it and rebuilds the open comparison, which has
      // to survive that: same buffer, still a comparison.
      await setDiffView(page, "side");
      await page.waitForSelector(".cm-mergeView", { timeout: 20000 });
      const before = await page.evaluate(() => document.querySelectorAll(".cm-mergeView .cm-content")[1].textContent);
      await setEditorSwitch(page, "diff_collapse", false);
      await page.waitForSelector(".cm-mergeView", { timeout: 20000 });
      const after = await page.evaluate(() => document.querySelectorAll(".cm-mergeView .cm-content")[1].textContent);
      assert((await readSettings(page)).diff_collapse === false,
        `the folding is not in this device's editor settings: ${JSON.stringify(await readSettings(page))}`);
      assert(before === after, `the folding switch changed the buffer: ${JSON.stringify([before, after])}`);
      await setEditorSwitch(page, "diff_collapse", true);

      // Nothing about either reaches the server: the settings page does not
      // offer them and the editor page carries no attribute for them.
      const settings = await page.evaluate(() =>
        fetch("/settings/editor/git", { headers: { Accept: "text/html" } }).then((r) => r.text()));
      assert(!/diff_view|diff_collapse/.test(settings), "the settings page still carries how a comparison looks");
      assert(!(await page.evaluate(() => {
        const d = document.querySelector("dc-editor").dataset;
        return "editorDiffView" in d || "editorDiffCollapse" in d;
      })), "the editor page still renders it as page data");

      // A second device is unaffected, it keeps deciding by its own width.
      const mp = await mobilePage();
      await mp.goto(editorURL, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(mp);
      await L.waitUpgraded(mp, ["dc-editor"]);
      const onPhone = (await readSettings(mp)).diff_view;
      assert(onPhone === undefined || onPhone === "auto", `the phone inherited the desktop's view: ${JSON.stringify(onPhone)}`);

      await setDiffView(page, "auto");
      return "view and folding stored per device, nothing on the server";
    });

    // Automatic reads the room, and the room changes while a diff stands open.
    // The open comparison follows the window instead of waiting for the next
    // time the file is opened.
    await run("diff: on automatic the open comparison follows the window width", async () => {
      await openTracked();
      await page.waitForSelector(".cm-mergeView", { timeout: 20000 });
      await page.setViewportSize({ width: 900, height: 900 });
      // The two pane view going away is the claim, and it is the only reliable
      // signal: a changed line carries the same class on both views, so waiting
      // for one of those matches the comparison that still stands.
      await page.waitForSelector(".cm-mergeView", { state: "detached", timeout: 20000 })
        .catch(() => { throw new Error("the narrow window kept the two pane view"); });
      await page.waitForSelector(".cm-deletedChunk, .cm-changedLine", { state: "attached", timeout: 20000 });
      await page.setViewportSize({ width: 1360, height: 900 });
      await page.waitForSelector(".cm-mergeView", { timeout: 20000 });
      assert(await page.locator(".cm-mergeView .cm-content").count() === 2,
        "the wide window did not bring the two panes back");
      // A picked view is a decision, the window does not overrule it.
      await setDiffView(page, "side");
      await page.setViewportSize({ width: 900, height: 900 });
      await sleep(600);
      assert(await page.locator(".cm-mergeView").count() === 1,
        "the narrow window overruled the picked side by side view");
      await page.setViewportSize({ width: 1360, height: 900 });
      await setDiffView(page, "auto");
      await page.waitForSelector(".cm-mergeView", { timeout: 20000 });
      return "narrow goes inline, wide comes back, a picked view stands";
    });

    await run("diff: the switch survives a reload and is stored as the revision", async () => {
      // tracked's switch has been on since the first diff check and rode every
      // navigation since: openTracked landed in the built comparison already,
      // on the automatic view, which is side by side on this width.
      await page.waitForSelector(".cm-mergeView", { timeout: 20000 });
      assert(await diffPressed(page), "the entry does not read as on before the reload");
      // The tab carries the revision it is compared against, not a boolean, so
      // a revision picker later fills the same field and the stored shape does
      // not change. Today the one revision is HEAD.
      const stored = await page.evaluate(([key, p]) => {
        const saved = JSON.parse(localStorage.getItem(key) || "{}");
        return (saved.open || []).find((e) => e && e.path === p) || null;
      }, [`dc-editor-tabs:${project}`, tracked]);
      assert(stored && stored.diff === "HEAD", `the diff is not stored as its revision: ${JSON.stringify(stored)}`);
      await page.reload({ waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      await L.waitUpgraded(page, ["dc-editor"]);
      await page.waitForSelector(`.editor-tab[data-path="${tracked}"].active`, { timeout: 15000 });
      await page.waitForSelector(".cm-mergeView", { timeout: 20000 });
      assert(await diffPressed(page), "the diff entry came back off");
      await toggleDiff(page);
      await page.waitForSelector(".cm-mergeView", { state: "detached", timeout: 20000 });
      assert(!(await diffPressed(page)), "switching it off did not read back");
      return "back in the diff without a click, off with one";
    });

    await run("diff: a file over the limit asks before it is built", async () => {
      await openEditorSettings();
      await page.fill('#settings-editor-git [name="diff_max_lines"]', "1");
      await saveEditorSettings();
      await openTracked();
      await dismissSwal(); // a restored diff would ask on its own
      await toggleDiff(page);
      await page.waitForSelector(".swal2-container", { timeout: 8000 });
      await page.click(".swal2-cancel");
      await page.waitForSelector(".swal2-container", { state: "detached", timeout: 6000 });
      await sleep(400);
      assert(await page.locator(".cm-mergeView").count() === 0, "a declined diff was built anyway");
      assert(!(await diffPressed(page)), "a declined diff left the diff entry pressed");

      await toggleDiff(page);
      await page.waitForSelector(".swal2-container", { timeout: 8000 });
      await L.confirmSwal(page);
      await page.waitForSelector(".cm-mergeView", { timeout: 20000 });
      await toggleDiff(page);
      await page.waitForSelector(".cm-mergeView", { state: "detached", timeout: 20000 });

      await openEditorSettings();
      await page.fill('#settings-editor-git [name="diff_max_lines"]', "5000");
      await saveEditorSettings();
      return "asked, declined, asked again, confirmed";
    });

    // The tab strip says what the tree says: the same letter, the same color,
    // and nothing at all for a file git has no entry for.
    await run("the open tabs carry the git mark, live", async () => {
      await openTracked(); // sub/tracked.txt is modified
      await page.click('.editor-item[data-path="root.txt"]'); // committed and clean
      await page.waitForSelector('.editor-tab[data-path="root.txt"]', { timeout: 10000 });
      await page.waitForSelector(`.editor-tab[data-path="${tracked}"][data-git-status="modified"]`, { timeout: 15000 });
      const changedTab = await page.evaluate((p) => {
        const btn = document.querySelector(`.editor-tab[data-path="${p}"]`);
        const mark = btn.querySelector("[data-git-mark]");
        const name = btn.querySelector(".editor-tab-name");
        return {
          mark: mark ? mark.textContent : "",
          markFirst: mark ? btn.firstElementChild === mark : false,
          colored: name.className,
          title: btn.title,
          // The close control must keep its full hit area next to the letter.
          stateWidth: Math.round(btn.querySelector(".editor-tab-state").getBoundingClientRect().width),
          nameClipped: getComputedStyle(name).textOverflow,
        };
      }, tracked);
      assert(changedTab.mark === "M", `tab mark: ${changedTab.mark}`);
      assert(changedTab.markFirst, "the letter does not sit in front of the name");
      assert(/text-yellow/.test(changedTab.colored), `the tab name is not colored: ${changedTab.colored}`);
      assert(/Modified/.test(changedTab.title), `the tab title says nothing: ${changedTab.title}`);
      assert(changedTab.stateWidth >= 19, `the close control shrank to ${changedTab.stateWidth}px`);
      assert(changedTab.nameClipped === "ellipsis", "a long name would no longer truncate");

      const cleanTab = await page.evaluate(() => {
        const btn = document.querySelector('.editor-tab[data-path="root.txt"]');
        return { mark: !!btn.querySelector("[data-git-mark]"), status: btn.dataset.gitStatus || "", cls: btn.querySelector(".editor-tab-name").className };
      });
      assert(!cleanTab.mark && !cleanTab.status, "an unchanged file carries a mark");
      assert(!/text-/.test(cleanTab.cls), `an unchanged tab name is colored: ${cleanTab.cls}`);

      // A change from outside, the way a coder makes one: the strip follows
      // without anybody reloading the page.
      const other = await ctx.newPage();
      L.wirePage(other, bag);
      try {
        await other.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
        await L.dismissUpdate(other);
        assert(await post(other, `${editorBase}/file`, { path: "root.txt", content: "root\nchanged from outside\n" }) === 200, "the second page could not save");
      } finally {
        await other.close().catch(() => {});
      }
      await page.waitForSelector('.editor-tab[data-path="root.txt"][data-git-status="modified"]', { timeout: 20000 });
      const arrived = await page.evaluate(() => {
        const btn = document.querySelector('.editor-tab[data-path="root.txt"]');
        return { mark: btn.querySelector("[data-git-mark]").textContent, cls: btn.querySelector(".editor-tab-name").className };
      });
      assert(arrived.mark === "M", `mark after the outside change: ${arrived.mark}`);
      assert(/text-yellow/.test(arrived.cls), `name color after the outside change: ${arrived.cls}`);
      return "marked, unmarked, and updated without a reload";
    });

    const openEditor = async (target) => {
      await target.goto(editorURL, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(target);
      await L.waitUpgraded(target, ["dc-editor"]);
      await target.waitForSelector(".editor-item[data-path]", { timeout: 15000 });
    };

    // The checks from here on write through the editor page itself rather than
    // a second one: a page opening and closing next to it could take the
    // editor's tab out of the foreground and back in, and coming back to the
    // front is a thing this editor reacts to.
    const writeHere = async (path, content) => {
      assert(await post(page, `${editorBase}/file`, { path, content }) === 200, `writing ${path} failed`);
    };
    // The interval is instance-wide state, so it is set from a page of its own:
    // these checks need the editor page to stay open while it changes.
    const setPollSeconds = async (seconds) => {
      const other = await ctx.newPage();
      L.wirePage(other, bag);
      try {
        await other.goto(`${BASE}/settings/editor/git`, { waitUntil: "domcontentloaded" });
        await L.dismissUpdate(other);
        await other.waitForSelector("#settings-editor-git", { timeout: 10000 });
        await other.fill('#settings-editor-git [name="git_poll_seconds"]', String(seconds));
        const [response] = await Promise.all([
          other.waitForResponse((r) => r.url().includes("/settings/editor/git") && r.request().method() === "POST", { timeout: 15000 }),
          other.locator('#settings-editor-git button[type="submit"]').click(),
        ]);
        assert(response.status() < 400, `saving the poll interval answered ${response.status()}`);
      } finally {
        await other.close().catch(() => {});
      }
    };
    // The shell this runner created is how the repository is driven: init,
    // add and merge stay with the shell, the editor's one write is the commit
    // route and it is covered by its own checks below.
    const runInShell = (command) => page.evaluate(([href, cmd]) => {
      const token = document.querySelector('meta[name="csrf-token"]').content;
      return fetch(`${href}/input`, {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-CSRF-Token": token },
        body: JSON.stringify({ items: [{ raw: cmd }] }),
      }).then((r) => r.status);
    }, [new URL(shellUrl).pathname, command]);

    // A tab remembers the switch it was in, and a project without a repository
    // has nothing to compare against: HEAD would answer "not in there", the
    // file would read as one long addition, and the entry that could turn it
    // off is hidden exactly then. The switch stays on the tab and is taken up
    // if a repository turns up, it is just never built without. The stored
    // state seeded here is the legacy shape on purpose: bare paths plus the old
    // diff map, which still has to read as a file with the diff on.
    await run("a restored diff is not built in a project without a repository", async () => {
      const plainBase = `/projects/${encodeURIComponent(plain)}/editor`;
      await page.goto(`${BASE}${plainBase}`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      await L.waitUpgraded(page, ["dc-editor"]);
      assert(await post(page, `${plainBase}/file`, { path: "note.txt", content: "one\ntwo\n" }) === 200, "writing the file failed");
      await page.evaluate((key) => localStorage.setItem(key, JSON.stringify({
        open: ["note.txt"],
        active: "note.txt",
        diff: { "note.txt": { mode: "side", rev: "HEAD" } },
      })), `dc-editor-tabs:${plain}`);
      await page.reload({ waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      await L.waitUpgraded(page, ["dc-editor"]);
      await page.waitForSelector('.editor-tab[data-path="note.txt"]', { timeout: 15000 });
      await page.waitForSelector(".cm-editor", { state: "attached", timeout: 15000 });
      await sleep(2500);
      assert(await page.locator(".cm-mergeView").count() === 0, "a diff was built without a repository");
      assert(await page.locator(".cm-deletedChunk, .cm-changedLine").count() === 0, "the file reads as one long addition");
      // Without a repository the file's menu has no git entries at all, which
      // is the shape "the switch that could turn it off is gone" takes now.
      await diffReady(page, false);
      assert(await diffEntry(page, "label") === null, "the tab menu offers a git diff without a repository");
      const text = await page.evaluate(() => document.querySelector(".cm-content").textContent);
      assert(text.includes("one") && text.includes("two"), `the file did not open normally: ${JSON.stringify(text)}`);
      return "the switch is kept, nothing is built, the file is just open";
    });

    // The interval is a setting, so changing it has to reach a poller that is
    // already running. Zero is the sharpest case of that: it means "stop", and
    // an interval cast into a ticker at start would keep publishing until the
    // last watcher closes the page.
    await run("a poll interval of zero stops a poller that is already running", async () => {
      await setPollSeconds(1);
      await openEditor(page);
      // Written through this page's own fetch, which tells it nothing: the mark
      // can only leave over the poller's event. root.txt goes back to its
      // committed content, which also proves the poller is running.
      await writeHere("root.txt", "root\n");
      await page.waitForSelector('.editor-item[data-path="root.txt"][data-git-status]', { state: "detached", timeout: 20000 });

      await setPollSeconds(0);
      await sleep(3000); // one round of the old interval is all it may take
      await writeHere("root.txt", "root\nwhile the poll is off\n");
      await sleep(12000);
      assert(!(await page.$('.editor-item[data-path="root.txt"][data-git-status]')), "the poller kept publishing after the interval was set to zero");
      return "the running poller took the new interval";
    });

    // A phone that was locked has its timers throttled: the watch window lapses,
    // the poller ends, and what happened in the meantime is never published to
    // anybody. The page asks for it itself when it is in front again.
    await run("a page that comes back to the front catches up on its own", async () => {
      // Still with the poll off, so nothing is published: whatever arrives now,
      // the page went and got it.
      assert(!(await page.$('.editor-item[data-path="root.txt"][data-git-status]')), "the change is marked before the page came back");
      await page.evaluate(() => document.dispatchEvent(new Event("visibilitychange")));
      await page.waitForSelector('.editor-item[data-path="root.txt"][data-git-status="modified"]', { timeout: 15000 });
      return "the missed change arrived without a click on refresh";
    });

    // Two status requests can be in flight at once. The answer to the older
    // question describes a repository that no longer exists, and applying it
    // undoes the newer one, which is what a person then sees: a mark that comes
    // back after it was gone.
    await run("a slow status answer never overwrites a newer one", async () => {
      let asked = 0;
      await page.route("**/editor/git/changes*", async (route) => {
        asked += 1;
        const response = await route.fetch();
        const body = await response.text();
        if (asked === 1) await sleep(5000); // the older question, answered late
        await route.fulfill({ response, body });
      });
      try {
        await page.click("[data-editor-refresh]"); // asks while root.txt is modified
        await sleep(1500); // its answer is fetched by now, only not delivered
        await writeHere("root.txt", "root\n"); // back to the committed content
        await page.click("[data-editor-refresh]"); // asks again, answered at once
        await page.waitForSelector('.editor-item[data-path="root.txt"][data-git-status]', { state: "detached", timeout: 15000 });
        await sleep(6000); // the first answer lands in here
        assert(!(await page.$('.editor-item[data-path="root.txt"][data-git-status]')), "the older answer painted the tree again");
      } finally {
        await page.unroute("**/editor/git/changes*");
      }
      return `${asked} answers, the newer one stands`;
    });

    // Saving a file moves the working copy and nothing else, so an open diff
    // must not fetch its revision again. A commit moves HEAD, and then the
    // revision side follows on its own: fetched once and replaced in place,
    // the buffer untouched.
    await run("a save costs no revision request, a commit moves the diff along", async () => {
      await setPollSeconds(1);
      await openTracked();
      await toggleDiff(page);
      await page.waitForSelector(".cm-mergeView", { timeout: 20000 });
      await sleep(1000);

      let revisionCalls = 0;
      const count = (request) => {
        if (request.url().includes("/editor/git/file")) revisionCalls += 1;
      };
      page.on("request", count);
      try {
        await writeHere("root.txt", "root\nsaved elsewhere\n");
        await page.waitForSelector('.editor-item[data-path="root.txt"][data-git-status="modified"]', { timeout: 20000 });
        await sleep(1500); // a request the save caused would be out by now
        assert(revisionCalls === 0, `a save cost ${revisionCalls} revision requests`);

        const working = await page.locator(".cm-mergeView .cm-content").nth(1).textContent();
        assert(await runInShell(`git add -A && git ${author} commit -qm follow\r`) === 200, "the shell refused the commit");
        // The commit moved HEAD, so the revision side catches up on its own:
        // the two sides read the same and the diff has nothing left to mark.
        await page.waitForFunction((want) => {
          const revision = document.querySelector(".cm-mergeView .cm-content");
          return revision && revision.textContent === want;
        }, working, { timeout: 30000 });
        assert(revisionCalls > 0, "the moved HEAD cost no request at all");
      } finally {
        page.off("request", count);
      }
      assert(await diffPressed(page), "following HEAD switched the diff off");
      await toggleDiff(page);
      await page.waitForSelector(".cm-mergeView", { state: "detached", timeout: 20000 });
      return `no request on a save, ${revisionCalls} on a commit, the revision side followed`;
    });

    // The red ! is the one mark that comes from an unmerged path. The editor
    // only shows it, resolving stays with the shell, so the merge is aborted
    // right after.
    await run("a file git could not merge carries the red ! in the tree", async () => {
      assert(await runInShell(
        "git checkout -q -b clash && printf 'root\\nclash line\\n' > root.txt && "
        + `git add -A && git ${author} commit -qm clash && `
        + "git checkout -q master && printf 'root\\nmaster line\\n' > root.txt && "
        + `git add -A && git ${author} commit -qm master && `
        + `git ${author} merge clash; true\r`,
      ) === 200, "the shell refused the merge setup");
      const deadline = Date.now() + 60000;
      let unmerged = false;
      while (Date.now() < deadline) {
        const changes = await gitChanges(page, project);
        unmerged = !!changes.repo && changes.worktree.some((e) => e.index === "U" || e.worktree === "U");
        if (unmerged) break;
        await sleep(1000);
      }
      assert(unmerged, "the merge left no unmerged path");

      await openEditor(page);
      await page.waitForSelector('.editor-item[data-path="root.txt"][data-git-status="conflict"]', { timeout: 20000 });
      const mark = await markOf(page, "root.txt");
      assert(mark.mark === "!", `the conflict mark is ${mark.mark}`);
      assert(await page.$('.editor-item[data-path="root.txt"] .editor-item-name.text-red'), "a conflicted file is not marked red");

      assert(await runInShell("git merge --abort || git reset -q --hard\r") === 200, "the shell refused to end the merge");
      await page.waitForSelector('.editor-item[data-path="root.txt"][data-git-status]', { state: "detached", timeout: 30000 });
      return "unmerged reads as !, aborted in the shell";
    });

    // Untracked and staged are two different answers about a new file: git has
    // never heard of the one and already holds the other. The letter and the
    // color say which one a row is, on the phone too, where no tooltip exists.
    await run("staging a new file turns the cyan U into the green A", async () => {
      await openEditor(page);
      await writeHere("staged.txt", "staged\n");
      await page.click("[data-editor-refresh]");
      await page.waitForSelector('.editor-item[data-path="staged.txt"][data-git-status="untracked"]', { timeout: 15000 });
      const before = await markOf(page, "staged.txt");
      assert(before.mark === "U", `untracked mark: ${before.mark}`);
      assert(await page.$('.editor-item[data-path="staged.txt"] .editor-item-name.text-cyan'), "an untracked file's name is not cyan");

      assert(await runInShell("git add staged.txt\r") === 200, "the shell refused to stage");
      await page.waitForSelector('.editor-item[data-path="staged.txt"][data-git-status="added"]', { timeout: 30000 });
      const after = await markOf(page, "staged.txt");
      assert(after.mark === "A", `added mark: ${after.mark}`);
      assert(await page.$('.editor-item[data-path="staged.txt"] .editor-item-name.text-green'), "a staged file's name is not green");

      assert(await runInShell(`git ${author} commit -qm staged\r`) === 200, "the shell refused the commit");
      await page.waitForSelector('.editor-item[data-path="staged.txt"][data-git-status]', { state: "detached", timeout: 30000 });
      return "U in cyan while unknown, A in green once staged, gone with the commit";
    });

    // ---- committing from the editor ------------------------------------------
    //
    // The commit view takes the tree's place in the column. Everything here
    // starts by taking every row out and picking back exactly what the check
    // is about, because earlier checks leave changes of their own behind and
    // committing those would pull the ground from under them.
    const openCommitView = async (target) => {
      await target.waitForSelector("[data-editor-commit-toggle]:not([hidden])", { timeout: 15000 });
      await target.click("[data-editor-commit-toggle]");
      await target.waitForSelector("[data-editor-commit]:not([hidden])", { timeout: 10000 });
    };
    const pickOnly = async (path) => {
      await page.waitForSelector(`.editor-commit-row[data-path="${path}"]`, { timeout: 15000 });
      await page.setChecked("[data-editor-commit-all]", false);
      await page.setChecked(`.editor-commit-row[data-path="${path}"] input[type="checkbox"]`, true);
    };
    // A count comes out of the shell into a file the editor can read; the
    // file is a worktree change while it exists, which is exactly why every
    // commit here picks its rows instead of taking everything. The old file
    // goes first, or a second count could read the first one's number.
    const shellCount = async (command) => {
      await post(page, `${editorBase}/delete`, { path: "count.txt" }).catch(() => {});
      assert(await runInShell(`${command} > count.txt\r`) === 200, "the shell refused the count");
      const deadline = Date.now() + 20000;
      while (Date.now() < deadline) {
        const data = await page.evaluate((b) =>
          fetch(`${b}/file?path=count.txt`, { headers: { Accept: "application/json" } })
            .then((r) => (r.ok ? r.json() : null)).catch(() => null), editorBase);
        const count = data && parseInt(data.content, 10);
        if (count > 0) return count;
        await sleep(500);
      }
      throw new Error("the count never arrived");
    };
    const revCount = () => shellCount("git rev-list --count HEAD");

    await run("commit: the panel lists the changes and a partial commit takes only the checked rows", async () => {
      await openEditor(page);
      await writeHere("commit-a.txt", "aaa\n");
      await writeHere("commit-b.txt", "bbb\n");
      assert(await post(page, `${editorBase}/mkdir`, { path: "deep" }) === 200, "mkdir deep failed");
      assert(await post(page, `${editorBase}/mkdir`, { path: "deep/nested" }) === 200, "mkdir deep/nested failed");
      assert(await post(page, `${editorBase}/mkdir`, { path: "chain" }) === 200, "mkdir chain failed");
      assert(await post(page, `${editorBase}/mkdir`, { path: "chain/one" }) === 200, "mkdir chain/one failed");
      await writeHere("deep/one.txt", "one\n");
      await writeHere("deep/nested/two.txt", "two\n");
      await writeHere("chain/one/two.txt", "chained\n");
      await page.click("[data-editor-refresh]");
      await page.waitForSelector('.editor-item[data-path="commit-a.txt"][data-git-status="untracked"]', { timeout: 15000 });

      // The editor menu carries the entry with the count of changes.
      await page.click("[data-editor-menu]");
      await page.waitForSelector("[data-editor-menu-list].show", { timeout: 4000 });
      assert(await page.locator("[data-editor-commit-item]").isVisible(), "the menu does not offer the commit");
      assert(/\d/.test(await page.locator("[data-editor-commit-item-count]").textContent()), "the menu entry carries no count");
      await page.keyboard.press("Escape");
      await sleep(300);

      await openCommitView(page);
      await page.waitForSelector("[data-editor-tree]", { state: "hidden", timeout: 5000 });
      assert(await page.$("[data-editor-commit-toggle].active"), "the toggle does not read as active while the view is open");
      // The branch arrives with the info request, after the panel is up.
      await page.waitForFunction(() =>
        (document.querySelector("[data-editor-commit-branch]").textContent || "").trim() !== "", null, { timeout: 10000 });
      const branch = (await page.locator("[data-editor-commit-branch]").textContent()).trim();
      assert(branch === "master", `the panel commits to "${branch}"`);
      const marks = await page.evaluate(() => Object.fromEntries(
        [...document.querySelectorAll(".editor-commit-row")]
          .filter((row) => row.dataset.path)
          .map((row) => [row.dataset.path, row.querySelector(".editor-commit-open span").textContent])));
      assert(marks["commit-a.txt"] === "U" && marks["commit-b.txt"] === "U", `the rows carry ${JSON.stringify(marks)}`);
      // A whole new folder is not one collapsed line: its files list one by
      // one, so a single one of them can be picked.
      assert(!("deep/" in marks), "an untracked folder is one collapsed row");
      assert(marks["deep/one.txt"] === "U" && marks["deep/nested/two.txt"] === "U",
        `the folder's files are not listed one by one: ${JSON.stringify(marks)}`);

      // Grouped is the default and reads the way an IDE's directory tree
      // does: folders first and files after them on every level, a subfolder
      // nested under its parent labelled with only what its path adds, a
      // folder chain that only hands down to one subfolder merged into one
      // row, a folder's checkbox covering its whole subtree and reading
      // mixed while only part of it is picked, its count the subtree's. The
      // folders button switches to the flat list the rest of this check
      // works in.
      await page.waitForSelector('.editor-commit-grouprow[data-dir="deep/nested"]', { timeout: 5000 });
      const grouped = await page.evaluate(() => {
        const rows = [...document.querySelectorAll(".editor-commit-row")];
        return {
          firstIsGroup: rows.length > 0 && rows[0].classList.contains("editor-commit-grouprow"),
          groups: rows.filter((r) => r.dataset.dir != null).map((r) => r.dataset.dir),
          order: rows.map((r) => r.dataset.dir || r.dataset.path),
          nestedHasPath: !!document.querySelector('.editor-commit-row[data-path="deep/nested/two.txt"] .editor-commit-path'),
          nestedLabel: document.querySelector('.editor-commit-grouprow[data-dir="deep/nested"] .text-truncate').textContent,
          chainLabel: (document.querySelector('.editor-commit-grouprow[data-dir="chain/one"] .text-truncate') || {}).textContent,
          deepCount: document.querySelector('.editor-commit-grouprow[data-dir="deep"] .ms-auto').textContent,
        };
      });
      assert(grouped.firstIsGroup, `the list does not lead with a folder: ${JSON.stringify(grouped.order)}`);
      assert(grouped.groups.includes("deep") && grouped.groups.includes("deep/nested"),
        `the groups are ${JSON.stringify(grouped.groups)}`);
      assert(!grouped.groups.includes("chain"), "a folder that only hands down got a row of its own");
      assert(grouped.chainLabel === "chain/one", `the chain reads as "${grouped.chainLabel}"`);
      assert(grouped.nestedLabel === "nested", `the nested group is labelled "${grouped.nestedLabel}"`);
      assert(grouped.deepCount === "2", `the parent's count speaks for ${grouped.deepCount} instead of its subtree`);
      assert(!grouped.nestedHasPath, "a grouped file still carries its own path line");
      assert(grouped.order.indexOf("deep/one.txt") > grouped.order.indexOf("deep/nested/two.txt"),
        `a folder's files do not follow its subfolders: ${JSON.stringify(grouped.order)}`);
      await page.setChecked('.editor-commit-grouprow[data-dir="deep/nested"] input', false);
      assert(!(await page.locator('.editor-commit-row[data-path="deep/nested/two.txt"] input').isChecked()),
        "dropping the group left its file picked");
      assert(await page.evaluate(() =>
        document.querySelector('.editor-commit-grouprow[data-dir="deep"] input').indeterminate),
      "a partly picked subtree does not read as mixed on the folder above");
      await page.setChecked('.editor-commit-row[data-path="deep/nested/two.txt"] input', true);
      assert(await page.locator('.editor-commit-grouprow[data-dir="deep/nested"] input').isChecked(),
        "picking the file back did not reach its group row");
      await page.setChecked('.editor-commit-grouprow[data-dir="deep"] input', false);
      const subtree = await page.evaluate(() => [
        document.querySelector('.editor-commit-row[data-path="deep/one.txt"] input').checked,
        document.querySelector('.editor-commit-row[data-path="deep/nested/two.txt"] input').checked,
      ]);
      assert(subtree.join() === "false,false", `dropping the parent left its subtree at ${subtree.join()}`);
      await page.setChecked('.editor-commit-grouprow[data-dir="deep"] input', true);
      await page.click("[data-editor-commit-group]");
      await page.waitForSelector(".editor-commit-grouprow", { state: "detached", timeout: 5000 });
      assert(await page.$('.editor-commit-row[data-path="deep/nested/two.txt"] .editor-commit-path'),
        "the flat list does not carry the full path under the name");

      await pickOnly("commit-a.txt");
      const summary = await page.locator("[data-editor-commit-summary]").textContent();
      assert(/^1 of \d+ changes$/.test(summary.trim()), `the summary says "${summary}"`);
      assert(await page.locator("[data-editor-commit-button]").isDisabled(), "the button is offered without a message");
      await page.fill("[data-editor-commit-message]", "editor commit one");
      await page.click("[data-editor-commit-button]");
      await page.waitForSelector('.editor-commit-row[data-path="commit-a.txt"]', { state: "detached", timeout: 20000 });
      assert(await page.locator('.editor-commit-row[data-path="commit-b.txt"]').count() === 1, "the unchecked row went into the commit");
      assert((await page.inputValue("[data-editor-commit-message]")) === "", "the message box did not empty");

      const changes = await gitChanges(page, project);
      assert(!changes.worktree.some((e) => e.path === "commit-a.txt"), "the picked file is still a change");
      assert(changes.worktree.some((e) => e.path === "commit-b.txt" && e.worktree === "?"), "the unchecked file is no longer untracked");
      const atHead = await page.evaluate((b) =>
        fetch(`${b}/git/file?path=commit-a.txt`, { headers: { Accept: "application/json" } }).then((r) => r.json()), editorBase);
      assert(atHead.exists && /aaa/.test(atHead.content), `HEAD does not hold the committed file: ${JSON.stringify(atHead)}`);

      await page.click("[data-editor-commit-close]");
      await page.waitForSelector("[data-editor-tree]", { state: "visible", timeout: 5000 });
      await page.waitForSelector("[data-editor-commit]", { state: "hidden", timeout: 5000 });
      assert(!(await page.$("[data-editor-commit-toggle].active")), "the toggle still reads as active after the close");
      assert(await post(page, `${editorBase}/delete`, { path: "deep" }) === 200, "the folder could not be removed");
      assert(await post(page, `${editorBase}/delete`, { path: "chain" }) === 200, "the chain folder could not be removed");
      return "grouped tree by default, flat behind the switch, one picked, one left standing";
    });

    await run("commit: unsaved work on a picked file is saved into the commit, Ctrl+K and Ctrl+Enter drive it", async () => {
      await openEditor(page);
      await page.click("[data-editor-refresh]");
      await page.waitForSelector('.editor-item[data-path="commit-b.txt"]', { timeout: 15000 });
      await page.click('.editor-item[data-path="commit-b.txt"]');
      await page.waitForSelector('.editor-tab[data-path="commit-b.txt"]', { timeout: 10000 });
      await diffReady(page);
      await page.locator("[data-editor-surface] .cm-content").first().click({ force: true });
      await page.keyboard.press("Control+End");
      await page.keyboard.type("TYPED");
      await page.waitForSelector(".editor-tab.dirty", { timeout: 8000 });

      await page.keyboard.press("Control+k");
      await page.waitForSelector("[data-editor-commit]:not([hidden])", { timeout: 10000 });
      await pickOnly("commit-b.txt");
      await page.fill("[data-editor-commit-message]", "editor commit two");
      await page.keyboard.press("Control+Enter");
      await page.waitForSelector('.editor-commit-row[data-path="commit-b.txt"]', { state: "detached", timeout: 20000 });
      assert(!(await page.$(".editor-tab.dirty")), "the buffer still reads as unsaved");
      const atHead = await page.evaluate((b) =>
        fetch(`${b}/git/file?path=commit-b.txt`, { headers: { Accept: "application/json" } }).then((r) => r.json()), editorBase);
      assert(/TYPED/.test(atHead.content), `the unsaved words never reached the commit: ${JSON.stringify(atHead.content)}`);
      await page.keyboard.press("Control+k");
      await page.waitForSelector("[data-editor-commit]", { state: "hidden", timeout: 5000 });
      return "typed, never saved by hand, and in HEAD";
    });

    await run("commit: an amend borrows the last message and rewrites the tip instead of adding one", async () => {
      await openEditor(page);
      const before = await revCount();
      await writeHere("commit-b.txt", "bbb\nTYPED\namended\n");
      await page.click("[data-editor-refresh]");
      await page.waitForSelector('.editor-item[data-path="commit-b.txt"][data-git-status="modified"]', { timeout: 15000 });

      await openCommitView(page);
      await pickOnly("commit-b.txt");
      await page.waitForFunction(() => !document.querySelector("[data-editor-commit-amend]").disabled, null, { timeout: 10000 });
      await page.setChecked("[data-editor-commit-amend]", true);
      await page.waitForFunction(() => document.querySelector("[data-editor-commit-message]").value.length > 0, null, { timeout: 8000 });
      const borrowed = await page.inputValue("[data-editor-commit-message]");
      assert(borrowed === "editor commit two", `the amend starts from "${borrowed}"`);
      await page.fill("[data-editor-commit-message]", "editor commit two, amended");
      await page.click("[data-editor-commit-button]");
      await page.waitForSelector('.editor-commit-row[data-path="commit-b.txt"]', { state: "detached", timeout: 20000 });

      const info = await page.evaluate((b) =>
        fetch(`${b}/git/commit`, { headers: { Accept: "application/json" } }).then((r) => r.json()), editorBase);
      assert(info.lastMessage === "editor commit two, amended", `the tip says "${info.lastMessage}"`);
      const after = await revCount();
      assert(after === before, `the amend went from ${before} to ${after} commits`);
      assert(await post(page, `${editorBase}/delete`, { path: "count.txt" }) === 200, "the count file could not be removed");
      await page.click("[data-editor-commit-close]");
      return `still ${after} commits, the tip reworded`;
    });

    await run("commit: push rides behind the arrow, a refused push leaves the commit standing", async () => {
      await openEditor(page);
      await writeHere("push-a.txt", "pa\n");
      await page.click("[data-editor-refresh]");
      await page.waitForSelector('.editor-item[data-path="push-a.txt"]', { timeout: 15000 });
      // The arrow toggles: a click while the menu is somehow still up closes
      // it again, so this keeps clicking until the entry really shows.
      const pickPush = async () => {
        const deadline = Date.now() + 10000;
        while (!(await page.locator("[data-editor-commit-push]").isVisible())) {
          assert(Date.now() < deadline, "the push entry never came up");
          await page.click("[data-editor-commit-more]");
          await sleep(400);
        }
        await page.click("[data-editor-commit-push]");
      };
      await openCommitView(page);
      await pickOnly("push-a.txt");
      await page.fill("[data-editor-commit-message]", "push without a destination");
      await pickPush();
      // No remote: the commit lands, the push refusal stands beside it.
      await page.waitForSelector('.editor-commit-row[data-path="push-a.txt"]', { state: "detached", timeout: 20000 });
      await page.waitForSelector("[data-editor-commit-error]:not([hidden])", { timeout: 15000 });
      const said = await page.locator("[data-editor-commit-error]").textContent();
      assert(/push/.test(said) && /destination|upstream/i.test(said), `the refusal says "${said}"`);
      assert((await page.inputValue("[data-editor-commit-message]")) === "", "the commit took the message, the box must be empty");
      // The arrow gets disabled by the very click, which used to keep
      // bootstrap from closing the menu behind it.
      assert(await page.evaluate(() =>
        !document.querySelector("[data-editor-commit-push]").closest(".dropdown-menu").classList.contains("show")),
      "the menu stayed open after Commit and push");

      const remote = `/tmp/zzgit-remote-${tag}.git`;
      assert(await runInShell(
        `git init -q --bare ${remote} && git remote add origin ${remote} && git push -q -u origin master\r`,
      ) === 200, "the shell refused the remote setup");
      const before = await shellCount(`git --git-dir ${remote} rev-list --count --all`);
      await writeHere("push-a.txt", "pa2\n");
      await page.click("[data-editor-refresh]");
      await page.waitForSelector('.editor-commit-row[data-path="push-a.txt"]', { timeout: 15000 });
      await pickOnly("push-a.txt");
      await page.fill("[data-editor-commit-message]", "push with a destination");
      await pickPush();
      await page.waitForSelector('.editor-commit-row[data-path="push-a.txt"]', { state: "detached", timeout: 30000 });
      const after = await shellCount(`git --git-dir ${remote} rev-list --count --all`);
      assert(after === before + 1, `the upstream went from ${before} to ${after}`);
      assert(await page.evaluate(() => document.querySelector("[data-editor-commit-error]").hidden),
        "a delivered push left a refusal standing");

      assert(await post(page, `${editorBase}/delete`, { path: "count.txt" }) === 200, "the count file could not be removed");
      assert(await runInShell(`git remote remove origin && rm -rf ${remote}\r`) === 200, "the shell refused the cleanup");
      await page.click("[data-editor-commit-close]");
      return "refused without a destination, delivered with one";
    });

    await run("commit: git's refusal stands in the panel and a conflicted row cannot be picked", async () => {
      await openEditor(page);
      assert(await runInShell(
        "git checkout -q -b clash2 && printf 'root\\nclash two\\n' > root.txt && "
        + `git add -A && git ${author} commit -qm clash2 && `
        + "git checkout -q master && printf 'root\\nmaster two\\n' > root.txt && "
        + `git add -A && git ${author} commit -qm master2 && `
        + `git ${author} merge clash2; true\r`,
      ) === 200, "the shell refused the merge setup");
      const deadline = Date.now() + 60000;
      let unmerged = false;
      while (Date.now() < deadline) {
        const changes = await gitChanges(page, project);
        unmerged = !!changes.repo && changes.worktree.some((e) => e.index === "U" || e.worktree === "U");
        if (unmerged) break;
        await sleep(1000);
      }
      assert(unmerged, "the merge left no unmerged path");
      // The probe goes onto the disk only now: written before the shell's
      // branch dance it would ride into the clash2 commit, and the abort at
      // the end would take it away with the merge.
      await writeHere("refused.txt", "probe\n");
      await page.click("[data-editor-refresh]");
      await openCommitView(page);

      await page.waitForSelector('.editor-commit-row[data-path="root.txt"]', { timeout: 15000 });
      assert(await page.locator('.editor-commit-row[data-path="root.txt"] input[type="checkbox"]').isDisabled(),
        "a conflicted row offers its checkbox");
      await pickOnly("refused.txt");
      await page.fill("[data-editor-commit-message]", "must not land");
      await page.click("[data-editor-commit-button]");
      await page.waitForSelector("[data-editor-commit-error]:not([hidden])", { timeout: 20000 });
      const said = await page.locator("[data-editor-commit-error]").textContent();
      assert(/partial commit/i.test(said), `the panel says "${said}" instead of git's own words`);
      assert((await page.inputValue("[data-editor-commit-message]")) === "must not land", "a refused commit ate the message");

      assert(await runInShell("git merge --abort || git reset -q --hard\r") === 200, "the shell refused to end the merge");
      assert(await post(page, `${editorBase}/delete`, { path: "refused.txt" }) === 200, "the probe could not be removed");
      await page.click("[data-editor-commit-close]");
      return "refused in git's words, the message kept, the conflict unpickable";
    });

    await run("commit: on the phone the view opens inside the drawer", async () => {
      const mp = await mobilePage();
      await openEditor(mp);
      await diffReady(mp);
      // A phone with no tab open opens the drawer by itself, and its backdrop
      // would stand in front of the menu; what this check is about is the menu
      // entry opening the drawer, so it starts closed.
      await mp.keyboard.press("Escape");
      await mp.waitForSelector("[data-editor-backdrop]", { state: "hidden", timeout: 5000 });
      await mp.click("[data-editor-menu]");
      await mp.waitForSelector("[data-editor-menu-list].show", { timeout: 4000 });
      await mp.click("[data-editor-commit-item]");
      await mp.waitForSelector(".editor-drawer-open [data-editor-commit]:not([hidden])", { timeout: 10000 });
      assert(await mp.locator("[data-editor-commit-button]").isVisible(), "the commit button is not on screen");
      await mp.click("[data-editor-commit-close]");
      await mp.waitForSelector("[data-editor-commit]", { state: "hidden", timeout: 5000 });
      assert(await mp.locator(".editor-drawer-open").count() === 1, "closing the view closed the drawer with it");
      return "the drawer carries the view, closing it keeps the drawer";
    });

    // ---- comparing two files -------------------------------------------------

    await run("two files on disk compare side by side, both writable, each with its own save", async () => {
      const leftFile = "compare-left.txt";
      const rightFile = "sub/compare-right.txt";
      await openEditor(page);
      await writeHere(leftFile, "one\nleft\nthree\n");
      await writeHere(rightFile, "one\nright\nthree\n");
      await page.click("[data-editor-refresh]");
      await page.waitForSelector(`.editor-item[data-path="${leftFile}"]`, { timeout: 15000 });
      if (!(await page.$(`.editor-item[data-path="${rightFile}"]`))) await page.click('.editor-item[data-path="sub"]');
      await page.waitForSelector(`.editor-item[data-path="${rightFile}"]`, { timeout: 10000 });

      // Before anything is selected there is nothing to compare with.
      await page.click(`.editor-item[data-path="${leftFile}"]`, { button: "right" });
      await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
      assert(await menuItem("Select for compare").count() === 1, "the tree menu does not offer a selection");
      assert(await menuItem("^Compare with").count() === 0, "the menu offers a comparison without a selection");
      await page.keyboard.press("Escape");
      await sleep(300);

      // The first step goes through the tab's menu, the second through the tree
      // row's: both surfaces carry the same picking.
      await page.click(`.editor-item[data-path="${leftFile}"]`);
      await page.waitForSelector(`.editor-tab[data-path="${leftFile}"]`, { timeout: 10000 });
      await pick(`.editor-tab[data-path="${leftFile}"]`, "Select for compare");
      await pick(`.editor-item[data-path="${rightFile}"]`, "^Compare with");
      await page.waitForSelector("[data-editor-compare]:not([hidden])", { timeout: 20000 });
      await page.waitForSelector(".cm-mergeView", { timeout: 20000 });

      const built = await page.evaluate(() => {
        const contents = [...document.querySelectorAll(".cm-mergeView .cm-content")];
        return {
          panes: contents.length,
          editable: contents.map((el) => el.getAttribute("contenteditable")),
          text: contents.map((el) => el.textContent),
          names: [...document.querySelectorAll("[data-editor-compare-name]")].map((el) => el.textContent),
          saves: [...document.querySelectorAll("[data-editor-compare-save]")].map((el) => el.disabled),
          toolbarSave: document.querySelector("[data-editor-save]").disabled,
        };
      });
      assert(built.panes === 2, `panes: ${built.panes}`);
      assert(built.editable.join() === "true,true", `both sides must be writable: ${built.editable.join()}`);
      assert(/left/.test(built.text[0]) && /right/.test(built.text[1]), `the sides hold the wrong files: ${JSON.stringify(built.text)}`);
      assert(built.names[0] === leftFile && built.names[1] === rightFile, `the bar names ${JSON.stringify(built.names)}`);
      assert(built.saves.join() === "true,true", "a save is offered before anything was typed");
      assert(built.toolbarSave, "the toolbar save is offered next to two of its own");
      assert(await diffEntry(page, "label") === null, "the comparison's menu offers a git diff");

      // Each side dirties and saves on its own.
      await page.locator(".cm-mergeView .cm-content").nth(0).click({ force: true });
      await page.keyboard.press("Control+End");
      await page.keyboard.type("LEFTEDIT");
      await page.waitForFunction(() => document.querySelector('[data-editor-compare-save="left"]').disabled === false, null, { timeout: 8000 });
      assert(await page.locator('[data-editor-compare-save="right"]').isDisabled(), "typing on one side offered to save the other");
      await page.waitForSelector(".editor-tab.dirty", { timeout: 6000 });

      await page.click('[data-editor-compare-save="left"]');
      await page.waitForFunction(() => document.querySelector('[data-editor-compare-save="left"]').disabled === true, null, { timeout: 10000 });
      const files = await page.evaluate(([b, l, r]) => Promise.all([l, r].map((p) =>
        fetch(`${b}/file?path=${encodeURIComponent(p)}`, { headers: { Accept: "application/json" } }).then((x) => x.json()))), [editorBase, leftFile, rightFile]);
      assert(/LEFTEDIT/.test(files[0].content), `the left save did not reach the disk: ${JSON.stringify(files[0].content)}`);
      assert(!/LEFTEDIT/.test(files[1].content), "the left save reached the right file");

      // And the other side, so the two saves are really two.
      await page.locator(".cm-mergeView .cm-content").nth(1).click({ force: true });
      await page.keyboard.press("Control+End");
      await page.keyboard.type("RIGHTEDIT");
      await page.waitForFunction(() => document.querySelector('[data-editor-compare-save="right"]').disabled === false, null, { timeout: 8000 });
      await page.click('[data-editor-compare-save="right"]');
      await page.waitForFunction(() => document.querySelector('[data-editor-compare-save="right"]').disabled === true, null, { timeout: 10000 });
      const after = await page.evaluate(([b, r]) =>
        fetch(`${b}/file?path=${encodeURIComponent(r)}`, { headers: { Accept: "application/json" } }).then((x) => x.json()), [editorBase, rightFile]);
      assert(/RIGHTEDIT/.test(after.content), `the right save did not reach the disk: ${JSON.stringify(after.content)}`);
      assert(!(await page.$(".editor-tab.dirty")), "the comparison still reads as unsaved");
      return "two panes, two names, two saves, two files on disk";
    });

    await run("a comparison keeps both buffers across a tab switch and comes back after a reload", async () => {
      const compareTab = await page.evaluate(() => document.querySelector(".editor-tab.active").dataset.path);
      assert(/^\/\/compare\//.test(compareTab), `the comparison tab is keyed as a file: ${compareTab}`);
      await page.locator(".cm-mergeView .cm-content").nth(0).click({ force: true });
      await page.keyboard.press("Control+End");
      await page.keyboard.type("UNSAVED");
      await page.waitForSelector(".editor-tab.dirty", { timeout: 8000 });

      // Away to another tab and back: the text stays, which is the whole point
      // of carrying both documents on the tab.
      await page.click('.editor-item[data-path="root.txt"]');
      await page.waitForSelector('.editor-tab[data-path="root.txt"].active', { timeout: 10000 });
      // Waiting for the attribute would have passed while the bar was still
      // on screen, which is how this shipped. "hidden" is real visibility.
      await page.waitForSelector("[data-editor-compare]", { state: "hidden", timeout: 8000 });
      // The synthetic path is url encoded ASCII, so it needs no escaping here.
      await page.click(`.editor-tab[data-path="${compareTab}"]`);
      await page.waitForSelector("[data-editor-compare]:not([hidden])", { timeout: 15000 });
      await page.waitForSelector(".cm-mergeView", { timeout: 15000 });
      await sleep(800);
      const kept = await page.evaluate(() => ({
        left: document.querySelectorAll(".cm-mergeView .cm-content")[0].textContent,
        dirty: !!document.querySelector(".editor-tab.dirty"),
        leftSave: document.querySelector('[data-editor-compare-save="left"]').disabled,
      }));
      assert(/UNSAVED/.test(kept.left), `the unsaved text did not survive the switch: ${JSON.stringify(kept.left)}`);
      assert(kept.dirty && kept.leftSave === false, "the comparison came back reading as saved");

      // It is persisted as its two paths, so a reload brings it back, read from
      // the disk: the unsaved text falls away like it does for every open tab.
      const stored = await page.evaluate((key) => JSON.parse(localStorage.getItem(key) || "{}"), `dc-editor-tabs:${project}`);
      const entry = (stored.open || []).find((e) => e && e.type === "compare");
      assert(entry && entry.left === "compare-left.txt" && entry.right === "sub/compare-right.txt",
        `the comparison is not persisted as its two paths: ${JSON.stringify(stored.open)}`);
      await page.reload({ waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      await L.waitUpgraded(page, ["dc-editor"]);
      await page.waitForSelector(`.editor-tab[data-path="${compareTab}"]`, { state: "attached", timeout: 20000 });
      await page.click(`.editor-tab[data-path="${compareTab}"]`);
      await page.waitForSelector(".cm-mergeView", { timeout: 20000 });
      await sleep(800);
      const restored = await page.evaluate(() => ({
        left: document.querySelectorAll(".cm-mergeView .cm-content")[0].textContent,
        dirty: !!document.querySelector(".editor-tab.dirty"),
      }));
      assert(!/UNSAVED/.test(restored.left), "unsaved text survived the reload");
      assert(/LEFTEDIT/.test(restored.left), `the left side did not come back from the disk: ${JSON.stringify(restored.left)}`);
      assert(!restored.dirty, "a comparison fresh from the disk reads as unsaved");

      // Its menu carries the close entries of any tab, close to the right
      // included, and nothing of a file: two files are not one to rename.
      await page.click(`.editor-tab[data-path="${compareTab}"]`, { button: "right" });
      await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
      assert(await menuItem("Close to the right").count() === 1, "the comparison's menu lacks close to the right");
      assert(await menuItem("Rename").count() === 0, "the comparison's menu offers a rename");
      assert(await menuItem("Select for compare").count() === 0, "the comparison offers itself for a comparison");
      await page.keyboard.press("Escape");
      await sleep(300);

      // It closes like any tab.
      await page.click(`.editor-tab[data-path="${compareTab}"] .editor-tab-state`);
      await page.waitForSelector("[data-editor-compare]", { state: "hidden", timeout: 8000 });
      return "both buffers survived the switch, the reload rebuilt it from the disk";
    });

    await run("Ctrl+S and Save all save what a comparison carries unsaved", async () => {
      const leftFile = "compare-left.txt";
      const rightFile = "sub/compare-right.txt";
      await pick(`.editor-item[data-path="${leftFile}"]`, "Select for compare");
      await pick(`.editor-item[data-path="${rightFile}"]`, "^Compare with");
      await page.waitForSelector("[data-editor-compare]:not([hidden])", { timeout: 20000 });
      await page.waitForSelector(".cm-mergeView", { timeout: 20000 });

      // Ctrl+S on the comparison saves its dirty side to the real file, not to
      // the synthetic tab path.
      await page.locator(".cm-mergeView .cm-content").nth(0).click({ force: true });
      await page.keyboard.press("Control+End");
      await page.keyboard.type("KEYSAVE");
      await page.waitForFunction(() => document.querySelector('[data-editor-compare-save="left"]').disabled === false, null, { timeout: 8000 });
      await page.keyboard.press("Control+s");
      await page.waitForFunction(() => document.querySelector('[data-editor-compare-save="left"]').disabled === true, null, { timeout: 10000 });
      assert(!(await page.$(".editor-tab.active.dirty")), "the comparison still reads as unsaved after Ctrl+S");
      const left = await page.evaluate(([b, p]) =>
        fetch(`${b}/file?path=${encodeURIComponent(p)}`, { headers: { Accept: "application/json" } }).then((x) => x.json()), [editorBase, leftFile]);
      assert(/KEYSAVE/.test(left.content), `Ctrl+S did not reach the disk: ${JSON.stringify(left.content).slice(0, 120)}`);

      // Save all takes the other side with it.
      await page.locator(".cm-mergeView .cm-content").nth(1).click({ force: true });
      await page.keyboard.press("Control+End");
      await page.keyboard.type("ALLSAVE");
      await page.waitForFunction(() => document.querySelector('[data-editor-save-all]').disabled === false, null, { timeout: 8000 });
      await page.click("[data-editor-menu]");
      await page.waitForSelector("[data-editor-menu-list].show", { timeout: 4000 });
      await page.click("[data-editor-save-all]");
      await page.waitForFunction(() => document.querySelector('[data-editor-compare-save="right"]').disabled === true, null, { timeout: 10000 });
      const right = await page.evaluate(([b, p]) =>
        fetch(`${b}/file?path=${encodeURIComponent(p)}`, { headers: { Accept: "application/json" } }).then((x) => x.json()), [editorBase, rightFile]);
      assert(/ALLSAVE/.test(right.content), `Save all did not reach the disk: ${JSON.stringify(right.content).slice(0, 120)}`);

      await page.click(".editor-tab.active .editor-tab-state");
      await page.waitForSelector("[data-editor-compare]", { state: "hidden", timeout: 8000 });
      return "Ctrl+S saved the left file, Save all the right one";
    });

    // ---- blame ---------------------------------------------------------------

    await run("blame answers per line and refuses a path outside the project", async () => {
      const blame = await page.evaluate(([b]) =>
        fetch(`${b}/git/blame?path=root.txt`, { headers: { Accept: "application/json" } }).then((r) => r.json()), [editorBase]);
      assert(blame.repo === true, "blame does not see a repository");
      assert(blame.lines.length >= 2, `lines: ${JSON.stringify(blame.lines)}`);
      assert(blame.commits.length >= 1, `commits: ${JSON.stringify(blame.commits)}`);
      // Every line points at a commit that is really in the list, and the list
      // carries each commit once.
      assert(blame.lines.every((i) => i >= 0 && i < blame.commits.length), `a line points nowhere: ${JSON.stringify(blame.lines)}`);
      assert(new Set(blame.commits.map((c) => c.sha)).size === blame.commits.length, "a commit is listed twice");
      const first = blame.commits[0];
      assert(first.short && first.author && first.summary && first.time > 0, `commit: ${JSON.stringify(first)}`);
      const refused = await page.evaluate(([b]) =>
        fetch(`${b}/git/blame?path=${encodeURIComponent("../../etc/passwd")}`).then((r) => r.status), [editorBase]);
      assert(refused === 400, `a path out of the project answered ${refused}`);
      return `${blame.commits.length} commits over ${blame.lines.length} lines`;
    });

    await run("the blame gutter is a per-file switch in the file's menus and rides with the tab", async () => {
      await openEditor(page);
      await page.click('.editor-item[data-path="root.txt"]');
      await page.waitForSelector('.editor-tab[data-path="root.txt"]', { timeout: 10000 });
      await diffReady(page);
      // The switch left the editor menu: it belongs to one file, so only the
      // file's own context menu carries it, on the tab and on the tree row.
      assert(!(await page.$("[data-editor-blame-item]")), "the editor menu still carries a blame entry");
      assert(await menuLabel('.editor-tab[data-path="root.txt"]', "git blame") === "Show git blame",
        "the tab menu does not offer the blame switch");
      assert(await menuLabel('.editor-item[data-path="root.txt"]', "git blame") === "Show git blame",
        "the tree menu does not offer the blame switch");
      assert(await page.locator(".cm-blame").count() === 0, "the gutter is there before it was asked for");

      await pick('.editor-tab[data-path="root.txt"]', "Show git blame");
      await page.waitForSelector(".cm-blame", { timeout: 20000 });
      const shown = await page.evaluate(() => {
        const marks = [...document.querySelectorAll(".cm-blame .cm-gutterElement span")].filter((el) => el.textContent.trim());
        return {
          count: marks.length,
          text: marks[0] ? marks[0].textContent : "",
          title: marks[0] ? marks[0].title : "",
        };
      });
      assert(shown.count > 0, "the gutter is empty");
      assert(/^[0-9a-f]{7} \S/.test(shown.text), `the gutter says ${JSON.stringify(shown.text)}`);
      assert(/·/.test(shown.title) && shown.title.split("\n").length === 2, `the tooltip says ${JSON.stringify(shown.title)}`);
      assert(await menuLabel('.editor-tab[data-path="root.txt"]', "git blame") === "Hide git blame",
        "the entry does not read as on");

      // Per file: another file stays without the gutter and its own menu still
      // says Show, the file that asked keeps it across the switch back.
      if (!(await page.$(`.editor-item[data-path="${tracked}"]`))) await page.click('.editor-item[data-path="sub"]');
      await page.click(`.editor-item[data-path="${tracked}"]`);
      await page.waitForSelector(`.editor-tab[data-path="${tracked}"].active`, { timeout: 10000 });
      await sleep(800);
      assert(await page.locator(".cm-blame").count() === 0, "the gutter followed to a file that never asked for it");
      assert(await menuLabel(`.editor-tab[data-path="${tracked}"]`, "git blame") === "Show git blame",
        "the switch reads as on for a file that never asked");
      await page.click('.editor-tab[data-path="root.txt"]');
      await page.waitForSelector(".cm-blame", { timeout: 20000 });

      // It rides in the saved tab entry, not in a key of its own, and a reload
      // comes back with the gutter on that file.
      const stored = await page.evaluate((key) => JSON.parse(localStorage.getItem(key) || "{}"), `dc-editor-tabs:${project}`);
      const entry = (stored.open || []).find((e) => e && e.path === "root.txt");
      assert(entry && entry.blame === true, `the switch is not on the tab entry: ${JSON.stringify(stored.open)}`);
      assert(await page.evaluate(() => localStorage.getItem("dc-editor-blame")) === null, "a global blame key is written");
      await page.reload({ waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      await L.waitUpgraded(page, ["dc-editor"]);
      await page.waitForSelector('.editor-tab[data-path="root.txt"]', { timeout: 20000 });
      await page.click('.editor-tab[data-path="root.txt"]');
      await page.waitForSelector(".cm-blame", { timeout: 25000 });
      const settings = await page.evaluate(() =>
        fetch("/settings/editor/git", { headers: { Accept: "text/html" } }).then((r) => r.text()));
      assert(!/blame/i.test(settings), "the settings page carries a blame setting");

      // The gutter is what git has, so an unsaved buffer takes it away and the
      // save brings it back: a line that moved would otherwise point at the
      // wrong commit.
      await page.locator(".cm-content").first().click({ force: true });
      await page.keyboard.press("Control+Home");
      await page.keyboard.type("moved\n");
      await page.waitForSelector('.editor-tab[data-path="root.txt"].dirty', { timeout: 8000 });
      await page.waitForSelector(".cm-blame", { state: "detached", timeout: 8000 });
      await page.click("[data-editor-save]");
      await page.waitForSelector('.editor-tab[data-path="root.txt"]:not(.dirty)', { timeout: 10000 });
      await page.waitForSelector(".cm-blame", { timeout: 25000 });

      // Off from the tree row's menu, the other door to the same switch.
      await pick('.editor-item[data-path="root.txt"]', "Hide git blame");
      await page.waitForSelector(".cm-blame", { state: "detached", timeout: 8000 });
      const after = await page.evaluate((key) => JSON.parse(localStorage.getItem(key) || "{}"), `dc-editor-tabs:${project}`);
      const off = (after.open || []).find((e) => e && e.path === "root.txt");
      assert(!off || off.blame !== true, `switching it off was not remembered: ${JSON.stringify(after.open)}`);
      return `${shown.count} lines attributed, "${shown.text}"`;
    });

    // ---- what happens when git cannot answer -------------------------------

    await run("blame on a file git has never seen answers empty and the editor says so", async () => {
      assert(await post(page, `${editorBase}/file`, { path: "unseen.txt", content: "never committed\n" }) === 200, "write unseen.txt failed");
      const answer = await page.evaluate(([b]) =>
        fetch(`${b}/git/blame?path=unseen.txt`, { headers: { Accept: "application/json" } })
          .then((r) => r.json().then((d) => ({ status: r.status, d }))), [editorBase]);
      assert(answer.status === 200, `blame on an untracked file answers ${answer.status}`);
      assert(answer.d.repo === true && answer.d.lines.length === 0, `blame answered ${JSON.stringify(answer.d).slice(0, 120)}`);

      await openEditor(page);
      await page.waitForSelector('.editor-item[data-path="unseen.txt"]', { timeout: 15000 });
      await page.click('.editor-item[data-path="unseen.txt"]');
      await page.waitForSelector('.editor-tab[data-path="unseen.txt"].active', { timeout: 10000 });
      await pick('.editor-tab[data-path="unseen.txt"]', "Show git blame");
      await sleep(2000);
      assert(!(await page.$(".cm-blame")), "a file git does not know shows a blame gutter");
      const said = await page.evaluate(() => document.querySelector("[data-editor-status]").textContent);
      assert(/nothing to blame/i.test(said), `the status line says ${JSON.stringify(said)}`);
      await page.click('.editor-tab[data-path="unseen.txt"] .editor-tab-state');
      await sleep(300);
      return "empty blame, and a status line that admits it";
    });

    await run("a revision that is too large says that, not that it is binary", async () => {
      // Large in the commit, small on the disk: a file over the edit limit has
      // no tab at all, so the only way to meet this message is a revision that
      // outgrew what the editor reads.
      assert(await runInShell(
        "yes abcdefghijklmnopqrstuvwxyz | head -c 2200000 > heavy.txt && "
        + `git add -A && git ${author} commit -qm heavy && printf 'small now\\n' > heavy.txt\r`,
      ) === 200, "the shell refused to write the large file");
      const deadline = Date.now() + 45000;
      let answer = null;
      while (Date.now() < deadline) {
        answer = await page.evaluate(([b]) =>
          fetch(`${b}/git/file?path=heavy.txt`, { headers: { Accept: "application/json" } })
            .then((r) => r.json()), [editorBase]);
        if (answer && answer.exists) break;
        await sleep(1000);
      }
      assert(answer && answer.binary === true, `the large revision answered ${JSON.stringify(answer).slice(0, 120)}`);
      assert(answer.reason === "large", `the reason is ${JSON.stringify(answer.reason)}`);

      await openEditor(page);
      await page.waitForSelector('.editor-item[data-path="heavy.txt"]', { timeout: 15000 });
      await page.click('.editor-item[data-path="heavy.txt"]');
      await page.waitForSelector('.editor-tab[data-path="heavy.txt"]', { timeout: 10000 });
      await diffReady(page);
      await toggleDiff(page);
      await sleep(2000);
      const toast = await page.evaluate(() => document.body.innerText.includes("too large to diff"));
      assert(toast, "the message does not say the revision is too large");
      await dismissSwal();
      assert(!(await page.$(".cm-mergeView")), "a revision the editor cannot read was diffed anyway");
      await page.click('.editor-tab[data-path="heavy.txt"] .editor-tab-state');
      await sleep(300);
      return "too large is its own sentence";
    });

    // One question per round: everything a round needs rides in the one
    // changes answer, so nothing else may be asked beside it.
    await run("a status round asks exactly one route", async () => {
      await openEditor(page);
      await sleep(1500);
      const seen = [];
      const watch = (request) => {
        const url = request.url();
        if (url.includes("/editor/git/")) seen.push(url.replace(/^.*\/editor\/git\//, ""));
      };
      page.on("request", watch);
      try {
        const round = page.waitForResponse((r) => r.url().includes("/editor/git/changes"), { timeout: 30000 });
        const other = await ctx.newPage();
        L.wirePage(other, bag);
        try {
          await other.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
          await L.dismissUpdate(other);
          assert(await post(other, `${editorBase}/file`, { path: "round.txt", content: "one round\n" }) === 200, "the second page could not create the file");
        } finally {
          await other.close().catch(() => {});
        }
        // The event refreshes the status, not the tree, so a new file has no row
        // to wait for: the round itself is what this counts.
        await round;
        await sleep(1500);
      } finally {
        page.off("request", watch);
      }
      const rounds = seen.filter((u) => u.startsWith("changes")).length;
      const beside = seen.filter((u) => !u.startsWith("changes") && !u.startsWith("watch"));
      assert(rounds >= 1, `no changes request at all: ${JSON.stringify(seen)}`);
      assert(beside.length === 0, `a round asked more than the changes route: ${JSON.stringify(beside)}`);
      return `${rounds} changes request, nothing beside it`;
    });

    await run("the phone keeps the file name in the strip beside one menu", async () => {
      const mp = await mobilePage();
      await mp.goto(editorURL, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(mp);
      await L.waitUpgraded(mp, ["dc-editor"]);
      if (!(await mp.locator("dc-editor.editor-drawer-open").count())) {
        await mp.click("[data-editor-drawer-toggle]");
        await mp.waitForSelector(".editor.editor-drawer-open", { timeout: 6000 });
      }
      await mp.waitForSelector('.editor-item[data-path="root.txt"]', { timeout: 15000 });
      await mp.click('.editor-item[data-path="root.txt"]');
      await mp.waitForSelector('.editor-tab[data-path="root.txt"]', { timeout: 10000 });
      await diffReady(mp);
      await sleep(1200);
      // The git controls were flex-shrink-0 and the tab strip was not, so the
      // strip was what paid for them: it stood at four pixels with a conflicted
      // file open, no name, no tab switch, no close control. They are entries
      // of the one menu now, and what has to hold is that the strip keeps the
      // room and none of them is laid out beside it.
      const row = await mp.evaluate(() => {
        const strip = document.querySelector("[data-editor-tabs]");
        const name = document.querySelector(".editor-tab.active .editor-tab-name");
        const stripRect = strip.getBoundingClientRect();
        const nameRect = name.getBoundingClientRect();
        const inside = Math.max(0, Math.min(nameRect.right, stripRect.right) - Math.max(nameRect.left, stripRect.left));
        const controls = [...document.querySelectorAll(".editor-pane-col > .editor-pane-header .editor-actions > *")]
          .filter((el) => el.getBoundingClientRect().width > 0)
          .map((el) => el.getAttributeNames().find((n) => n.startsWith("data-editor")) || el.className.split(" ")[0]);
        return {
          strip: Math.round(stripRect.width),
          name: name.textContent,
          shown: nameRect.width > 0 ? inside / nameRect.width : 0,
          controls,
          overflow: document.body.scrollWidth > document.documentElement.clientWidth,
        };
      });
      assert(row.strip >= 100, `the tab strip is ${row.strip}px wide next to the menu`);
      assert(row.name === "root.txt" && row.shown > 0.95, `the strip does not carry the file name: ${JSON.stringify(row)}`);
      assert(JSON.stringify(row.controls) === JSON.stringify(["dropdown"]),
        `the actions beside the strip are ${row.controls.join(", ")}`);
      assert(!row.overflow, "the page scrolls sideways on a phone");
      return `strip ${row.strip}px, name fully inside, one menu beside it`;
    });

    // The diff entry is a switch in the file's context menu, not a sheet:
    // clicking it builds the comparison right there, on the phone in the
    // inline view because the automatic setting picks by the width. The open
    // file has to differ from HEAD, or the inline view would have no chunk to
    // wait for. The editor menu carries no git entry at all anymore.
    await run("the diff toggles from the file's menu and the editor menu carries no git, on both widths", async () => {
      await writeHere("root.txt", "root\ndiff me\n");
      for (const [where, target] of [["phone", await mobilePage()], ["desktop", page]]) {
        if (target === page) {
          await openEditor(target);
        } else {
          await target.goto(editorURL, { waitUntil: "domcontentloaded" });
          await L.dismissUpdate(target);
          await L.waitUpgraded(target, ["dc-editor"]);
        }
        // The phone's tree renders its rows off screen while the drawer is
        // closed, so existence proves nothing there: the drawer opens first.
        if (where === "phone" && !(await target.$(".editor.editor-drawer-open"))) {
          await target.click("[data-editor-drawer-toggle]");
          await target.waitForSelector(".editor.editor-drawer-open", { timeout: 6000 });
        }
        await target.waitForSelector('.editor-item[data-path="root.txt"]', { timeout: 15000 });
        await target.click('.editor-item[data-path="root.txt"]');
        await target.waitForSelector('.editor-tab[data-path="root.txt"].active', { state: "attached", timeout: 10000 });
        await diffReady(target);
        await target.click("[data-editor-menu]");
        await target.waitForSelector("[data-editor-menu-list].show", { timeout: 4000 });
        const labels = await target.$$eval("[data-editor-menu-list] .dropdown-item", (els) => els
          .filter((el) => !el.hidden)
          .map((el) => el.textContent.trim()));
        assert(!labels.some((l) => /git diff|blame/i.test(l)), `${where}: the editor menu still carries git: ${labels.join(", ")}`);
        await target.keyboard.press("Escape");
        await sleep(200);
        assert(await diffEntry(target, "label") === "Show git diff",
          `${where}: the file's menu does not offer the diff`);

        await toggleDiff(target);
        await target.waitForSelector(".cm-mergeView, .cm-deletedChunk, .cm-changedLine", { state: "attached", timeout: 20000 });
        assert(await diffPressed(target), `${where}: the entry does not read as on`);
        assert(await target.evaluate(() => document.querySelector("[data-editor-sheet]").getBoundingClientRect().height === 0),
          `${where}: the diff opened a sheet`);
        await toggleDiff(target);
        await target.waitForFunction(() => !document.querySelector(".cm-mergeView")
          && !document.querySelector(".cm-deletedChunk") && !document.querySelector(".cm-changedLine"), null, { timeout: 20000 });
        assert(!(await diffPressed(target)), `${where}: the entry does not read as off`);
      }
      return "one switch in the file's menu, no sheet, no git in the editor menu";
    });

    const boxOf = (target, selector) => target.evaluate((sel) => {
      const el = document.querySelector(sel);
      if (!el) return null;
      const rect = el.getBoundingClientRect();
      const style = getComputedStyle(el);
      return {
        attribute: el.hasAttribute("hidden"),
        display: style.display,
        visibility: style.visibility,
        width: Math.round(rect.width),
        height: Math.round(rect.height),
      };
    }, selector);

    // ---- nothing open ------------------------------------------------------
    //
    // Everything above checks that a control turns up. Nothing checked that it
    // goes away, and that is exactly where this shipped broken: the comparison
    // bar carries `hidden` next to a d-flex, Tabler's display
    // utilities are important and sit below its own [hidden] rule in the same
    // file, so the utility won on source order and they stood on an empty
    // editor. The attribute was right the whole time, which is why nothing that
    // reads the attribute would have caught it. These read what the browser
    // actually lays out. Everything a file's own menu carries (the diff, the
    // blame, the preview) needs no measuring here: without a tab there is no
    // menu to open, and the entries are built per call from the tab.
    const FILE_CONTROLS = {
      "the comparison bar": "[data-editor-compare]",
      "the indentation readout": "[data-editor-indent-info]",
      "the cursor position": "[data-editor-pos]",
    };
    const assertGone = (where, name, box) => {
      assert(box, `${where}: ${name} is not on the page at all`);
      assert(box.attribute, `${where}: ${name} does not even carry the hidden attribute: ${JSON.stringify(box)}`);
      assert(box.display === "none", `${where}: ${name} carries hidden and is laid out as ${box.display}`);
      assert(box.width === 0 && box.height === 0, `${where}: ${name} takes ${box.width}x${box.height} on screen`);
    };
    const emptyEditor = async (target) => {
      await target.goto(editorURL, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(target);
      await L.waitUpgraded(target, ["dc-editor"]);
      await target.evaluate((key) => localStorage.removeItem(key), `dc-editor-tabs:${project}`);
      await target.reload({ waitUntil: "domcontentloaded" });
      await L.dismissUpdate(target);
      await L.waitUpgraded(target, ["dc-editor"]);
      await target.waitForSelector("[data-editor-placeholder]:not([hidden])", { timeout: 20000 });
      await sleep(2000); // the status has landed by now, and every sync with it
    };

    await run("an editor with no file open shows none of a file's controls, on both widths", async () => {
      await emptyEditor(page);
      assert(await page.locator(".editor-tab").count() === 0, "a tab is still open");
      const desktop = {};
      for (const [name, selector] of Object.entries(FILE_CONTROLS)) {
        desktop[name] = await boxOf(page, selector);
        assertGone("desktop", name, desktop[name]);
      }
      // The placeholder is what should be there instead, and it has to be a box
      // somebody can actually read.
      const placeholder = await boxOf(page, "[data-editor-placeholder]");
      assert(!placeholder.attribute && placeholder.height > 0, `the placeholder is not on screen: ${JSON.stringify(placeholder)}`);

      // The phone is where this was noticed, and the width matters: the
      // indentation readout hides itself through d-none below the sm
      // breakpoint, so only the wide measurement above proves anything about it.
      const mp = await mobilePage();
      await emptyEditor(mp);
      for (const [name, selector] of Object.entries(FILE_CONTROLS)) {
        assertGone("mobile", name, await boxOf(mp, selector));
      }
      return `${Object.keys(FILE_CONTROLS).length} controls gone on both widths`;
    });

    await run("closing the last tab takes the file's controls away again", async () => {
      await page.click('.editor-item[data-path="root.txt"]');
      await page.waitForSelector('.editor-tab[data-path="root.txt"]', { timeout: 10000 });
      await diffReady(page);
      // With a file open its menu carries the diff switch; the cursor readout
      // stands in the page. Both are what has to go with the last tab.
      assert(await diffEntry(page, "label") === "Show git diff", "the file's menu does not offer the diff");
      const pos = await boxOf(page, "[data-editor-pos]");
      assert(!pos.attribute && pos.width > 0, `the cursor position is not on screen with a file open: ${JSON.stringify(pos)}`);

      await page.click('.editor-tab[data-path="root.txt"] .editor-tab-state');
      await page.waitForSelector("[data-editor-placeholder]:not([hidden])", { timeout: 10000 });
      await sleep(600);
      for (const [name, selector] of Object.entries(FILE_CONTROLS)) {
        assertGone("after the close", name, await boxOf(page, selector));
      }
      assert(await page.locator(".editor-tab").count() === 0, "a tab survived the close");
      return "shown with a file, gone without one";
    });
  } finally {
    // The poll interval is instance-wide, put it back for the next runner.
    await page.goto(`${BASE}/settings/editor/git`, { waitUntil: "domcontentloaded" }).catch(() => {});
    await page.evaluate(() => {
      const form = document.getElementById("settings-editor-git");
      if (!form) return;
      form.querySelector('[name="git_poll_seconds"]').value = "2";
      form.submit();
    }).catch(() => {});
    await sleep(500);
    if (shellUrl) await L.deleteShell(page, shellUrl).catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
    await L.deleteProject(page, plain).catch(() => {});
  }
});
