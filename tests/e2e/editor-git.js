const L = require("./lib");
const { assert, sleep, BASE } = L;

// Editor git: what the editor shows about the repository a project sits in,
// and the two git switches of a file tab, Git diff and Git blame. Routes:
// GET /projects/:name/editor/git/changes (what the working copy carries, one
// entry per changed path with the line counts, plus the repo flag and the
// branch with ahead/behind), GET .../git/file?path=&rev= (the file at a
// revision, HEAD without one; no route ever answers a diff,
// @codemirror/merge computes it in the browser), GET .../git/blame, GET
// .../git/log, GET .../git/refs, POST .../git/watch, the write routes of
// the git surface (push, fetch, pull, checkout, branch, clone) and the app
// level askpass pair (GET/POST /git/prompt, standing questions as server
// state), covered in their
// own section below. The watch is what starts and stops the
// server's per-project poller: it only runs while a client says it is
// watching, it compares a fingerprint (HEAD, the status output) and publishes a
// bare "git" event over the shared /events stream when that moves. The event
// carries no state, dc-editor pulls the status itself, exactly like the tab
// strip pulls its fragment on "terminals". The stream's connect snapshot
// carries a git signal without a project name, which an editor answers with
// the full catch-up: a move published into a connection gap comes never
// again, the same file changing further does not move the status list.
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
//   - nothing starts picked in the commit panel, and the panel's draft (the
//     message, the picked paths and an amend in progress with its borrowed
//     message) is server state per project
//     (GET/POST .../git/commit-draft, saved debounced, published as the
//     commitdraft event, spent by a successful commit). Picks therefore stick
//     across navigations: every commit here clears the pick first (clearPicks
//     goes through the all checkbox's checked state, an indeterminate box
//     reads as unchecked and setChecked(false) would do nothing) and picks
//     back exactly what the check is about.
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
//   - the diff switch compares against HEAD and lives in the file's
//     context menu (tab and tree row), reading "Show git diff" / "Hide git
//     diff"; the file history and the revision picker fill the same field
//     with another revision. The editor menu carries git only as the Git
//     entry that opens the sheet; without a repository the entry stays, the
//     sheet then offers the clone and nothing else. `dc-editor` carries `data-git-repo` once
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
//   - the two axes of a merge view are two different scrollers, and the scroll
//     checks need long lines with wrapping off and the folding turned off to
//     have anything to scroll at all. The outer .cm-mergeView carries both
//     editors vertically, they cannot scroll on their own there, while
//     sideways every editor has its own .cm-scroller and the app ties the two
//     together. A wheel is aimed near the top edge of the view: a pane is as
//     tall as its whole document, so its own middle can sit far below the box.
//   - switching the revision under an open diff needs a file whose revisions
//     say different things, or a switch that never happened passes as one.
//     sub/rev.txt is that file: two commits ("one", "two") and a working copy
//     ("work"), so the revision side names which revision is up. The inline
//     view is where this broke, and it broke silently: it is an extension on
//     the open editor, and reconfiguring a compartment that already holds one
//     keeps the StateField values it had, so the new revision never arrived
//     while the status line already named it.
//   - a diff against a commit hash is immutable, so a commit cannot move it.
//     The check for the moved base has to put the tab back on HEAD first
//     (switch off, switch on), else it proves nothing.
//   - the pickers search on the server (`git/refs?q=&kinds=`), so a row is
//     only there once the round it belongs to came back: type, then wait for
//     `[data-picker-loading]` to go hidden. Typing is debounced at 200ms, so
//     a `keyboard.type` with a delay under that costs one round and not one
//     per character. The revision picker asks for commits as well and the
//     branch picker does not.
//   - proving that a slow answer never overwrites a newer one needs a slow
//     answer: `page.route` holds one query back, and the delayed
//     `route.continue()` has to swallow its own error, because the check
//     unroutes before the round comes home and playwright handles what is
//     still parked. An uncaught rejection there takes the whole runner down.
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
//   - the askpass bridge needs something that asks, and nothing real may be
//     contacted. `core.sshCommand` of this one repository points at a script
//     written through the editor's own file route (no quoting through the
//     shell), which talks to nothing: it runs `$SSH_ASKPASS` with the line a
//     passphrased key would ask, writes down what came back and exits 255, so
//     the push always ends in git's words and only the answer is the claim.
//     It has to stay quiet on `ssh -G <host>`, the configuration probe git
//     runs before it opens the connection, or one push asks twice and the
//     second question stands there unanswered until the action times out.
//     That is also the regression signal: without the bridge `SSH_ASKPASS` is
//     `/bin/false`, no dialog ever appears and the check fails on the wait.
//     The field is masked only for a secret, decided on the prompt line
//     itself, which is why the stand-in asks for a passphrase: the same
//     helper also carries an https user name and ssh's host key yes, and
//     those are typed in the open. A check that asserts `type="password"`
//     has to ask a question that names one.
//     The dialog is the one moment in which a write is provably still running,
//     which is where the single lock is read (statusbar spinner, every sheet
//     row disabled, the commit button disabled), and the commit message is
//     filled first, else that button is dead over the empty field and the
//     check would prove nothing.
//   - never wait for "[x][hidden]" and never assert on the attribute when the
//     claim is that something is gone. Tabler's display utilities are important
//     and sit below its own [hidden] rule, so an element with a d-* class was
//     laid out while the attribute said hidden; style.css now settles that, and
//     the checks at the end read computed display and the box instead.
//     An entry inside a closed dropdown has no box, so "is it there" is read
//     with the menu open (menuBoxes) and only "does it apply" from `hidden`.
//     The phone checks also open the drawer themselves: it only opens by itself
//     while nothing is open, and an earlier check may have left a file open.

L.runFeature("EDITOR GIT", async ({ engine, ctx, page, run, bag, mobilePage }) => {
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

    // The poller publishes only moves, and a move that fell into a gap comes
    // never again: the same file changing further does not move the status
    // list. Two healers close that gap. A stream that really died comes back
    // through the connect snapshot's bare git signal, which the editor
    // answers with the full catch-up. And a pull that failed retries on its
    // own, for the connection that half-died: offline emulation builds
    // exactly that half, an established SSE keeps delivering while every new
    // request fails, so the git event arrives here and its status pull dies —
    // which is also what a phone waking before its radio does.
    await run("a reconnect catches up on the git change the gap swallowed", async () => {
      const mp = await mobilePage();
      try {
        await ctx.setOffline(true);
        try {
          await sleep(1000);
          assert(await post(mp, `${editorBase}/file`, { path: worded, content: wordedAfter }) === 200,
            "the other device could not save");
          await sleep(2500);
          assert(!(await page.$(`.editor-item[data-path="${worded}"][data-git-status]`)),
            "the mark arrived while the page was offline");
        } finally {
          await ctx.setOffline(false);
        }
        // No refresh, no visibility change, no further git move: the mark has
        // to arrive on its own, here through the retry of the pull that
        // failed while the requests were down.
        await page.waitForSelector(`.editor-item[data-path="${worded}"][data-git-status="modified"]`, { timeout: 30000 });
      } finally {
        // The later checks need this file clean, a failed wait included.
        await post(page, `${editorBase}/file`, { path: worded, content: wordedBefore }).catch(() => {});
      }
      await page.waitForSelector(`.editor-item[data-path="${worded}"][data-git-status]`, { state: "detached", timeout: 20000 });
      return "the mark arrived with the reconnect, and live events flow again after it";
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

    await run("diff: the switch on a background tab brings the tab to the front", async () => {
      await openTracked();
      await page.click('.editor-item[data-path="root.txt"]');
      await page.waitForSelector('.editor-tab[data-path="root.txt"].active', { timeout: 10000 });

      await pick(`.editor-tab[data-path="${tracked}"]`, "Show git diff");
      await page.waitForSelector(`.editor-tab[data-path="${tracked}"].active`, { timeout: 10000 });
      await page.waitForSelector(".cm-mergeView", { timeout: 20000 });
      await toggleDiff(page);
      await page.waitForSelector(".cm-mergeView", { state: "detached", timeout: 20000 });

      await page.click('.editor-tab[data-path="root.txt"]');
      await page.waitForSelector('.editor-tab[data-path="root.txt"].active', { timeout: 10000 });
      await pick(`.editor-item[data-path="${tracked}"]`, "Show git diff");
      await page.waitForSelector(`.editor-tab[data-path="${tracked}"].active`, { timeout: 10000 });
      await page.waitForSelector(".cm-mergeView", { timeout: 20000 });

      await page.click('.editor-tab[data-path="root.txt"]');
      await page.waitForSelector('.editor-tab[data-path="root.txt"].active', { timeout: 10000 });
      await pick(`.editor-item[data-path="${tracked}"]`, "Hide git diff");
      await sleep(600);
      assert(await page.locator('.editor-tab[data-path="root.txt"].active').count() === 1,
        "hiding from the back stole the front tab");
      await page.click(`.editor-tab[data-path="${tracked}"]`);
      await page.waitForSelector(`.editor-tab[data-path="${tracked}"].active`, { timeout: 10000 });
      await sleep(800);
      assert(await page.locator(".cm-mergeView").count() === 0, "a hidden diff was built anyway");
      assert(!(await diffPressed(page)), "the entry still reads as on");
      return "show surfaces the tab with the diff, hide only clears the wish";
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

    // The revert is the editor's one deliberate discard (POST git/revert, no
    // askpass bridge): a marked row's context menu takes the path back to
    // HEAD, and the confirm says the one thing that cannot be restored before
    // anything runs, a file without a state in HEAD is deleted. The entry only
    // exists on a marked row, which is why every wait here goes through the
    // row's git status first.
    await run("revert: a tracked file goes back to HEAD, an untracked one is deleted after the confirm says so", async () => {
      await openEditor(page);
      await writeHere("revertme.txt", "head\n");
      assert(await runInShell(`git add revertme.txt && git ${author} commit -qm revertme\r`) === 200, "the shell refused the baseline");
      await page.click("[data-editor-refresh]");
      await page.waitForSelector('.editor-item[data-path="revertme.txt"]:not([data-git-status])', { timeout: 30000 });
      await writeHere("revertme.txt", "changed\n");
      // The open buffer is part of the claim: after the revert it has to read
      // the disk again instead of showing the discarded text.
      await page.click('.editor-item[data-path="revertme.txt"]');
      await page.waitForFunction(() => (document.querySelector(".cm-content")?.textContent || "").includes("changed"), null, { timeout: 15000 });
      await page.waitForSelector('.editor-item[data-path="revertme.txt"][data-git-status="modified"]', { timeout: 20000 });
      await pick('.editor-item[data-path="revertme.txt"]', "Revert changes");
      await page.waitForSelector(".swal2-popup:not(.swal2-toast)", { state: "visible", timeout: 8000 });
      const asked = await page.textContent(".swal2-html-container");
      assert(/state in HEAD/.test(asked), `the confirm does not name HEAD: ${asked}`);
      await page.click(".swal2-confirm");
      await page.waitForSelector('.editor-item[data-path="revertme.txt"][data-git-status]', { state: "detached", timeout: 30000 });
      const reverted = await page.evaluate((b) =>
        fetch(`${b}/file?path=revertme.txt`, { headers: { Accept: "application/json" } })
          .then((r) => (r.ok ? r.json() : null)).catch(() => null), editorBase);
      assert(reverted && reverted.content === "head\n", `the disk reads ${JSON.stringify(reverted && reverted.content)}`);
      await page.waitForFunction(() => {
        const text = document.querySelector(".cm-content")?.textContent || "";
        return text.includes("head") && !text.includes("changed");
      }, null, { timeout: 15000 });

      await writeHere("loose.txt", "no HEAD state\n");
      await page.click("[data-editor-refresh]");
      await page.waitForSelector('.editor-item[data-path="loose.txt"][data-git-status="untracked"]', { timeout: 20000 });
      await pick('.editor-item[data-path="loose.txt"]', "Revert changes");
      await page.waitForSelector(".swal2-popup:not(.swal2-toast)", { state: "visible", timeout: 8000 });
      const question = await page.textContent(".swal2-html-container");
      assert(/deletes the file/.test(question), `the confirm does not say the deletion: ${question}`);
      const button = (await page.textContent(".swal2-confirm")).trim();
      assert(button === "Delete file", `the confirm button says ${button}`);
      await page.click(".swal2-confirm");
      await page.waitForSelector('.editor-item[data-path="loose.txt"]', { state: "detached", timeout: 30000 });
      const status = await page.evaluate((b) =>
        fetch(`${b}/file?path=loose.txt`, { headers: { Accept: "application/json" } }).then((r) => r.status), editorBase);
      assert(status >= 400, `the deleted file still answers ${status}`);
      return "restored to HEAD with the buffer following, deletion said and done";
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
    // clearPicks empties the pick through the all checkbox. The box can stand
    // indeterminate, where setChecked(false) reads it as unchecked and does
    // nothing, so it goes through checked first.
    const clearPicks = async () => {
      const enabled = await page.evaluate(() => {
        const all = document.querySelector("[data-editor-commit-all]");
        return !!all && !all.disabled;
      });
      if (!enabled) return;
      await page.setChecked("[data-editor-commit-all]", true);
      await page.setChecked("[data-editor-commit-all]", false);
    };
    const pickOnly = async (path) => {
      await page.waitForSelector(`.editor-commit-row[data-path="${path}"]`, { timeout: 15000 });
      await clearPicks();
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
    // The same trip for a question whose answer is a line and not a number,
    // a commit's subject being the one that says whether a rewrite arrived.
    const shellText = async (command) => {
      await post(page, `${editorBase}/delete`, { path: "count.txt" }).catch(() => {});
      assert(await runInShell(`${command} > count.txt\r`) === 200, "the shell refused the read");
      const deadline = Date.now() + 20000;
      while (Date.now() < deadline) {
        const data = await page.evaluate((b) =>
          fetch(`${b}/file?path=count.txt`, { headers: { Accept: "application/json" } })
            .then((r) => (r.ok ? r.json() : null)).catch(() => null), editorBase);
        const text = data && (data.content || "").trim();
        if (text) return text;
        await sleep(500);
      }
      throw new Error("the answer never arrived");
    };
    // Reads a file of the project straight through the editor's own route,
    // which is how the checks below see what a process outside the browser
    // wrote down.
    const readHere = async (path) => {
      const deadline = Date.now() + 15000;
      while (Date.now() < deadline) {
        const data = await page.evaluate(([b, p]) =>
          fetch(`${b}/file?path=${encodeURIComponent(p)}`, { headers: { Accept: "application/json" } })
            .then((r) => (r.ok ? r.json() : null)).catch(() => null), [editorBase, path]);
        const text = data && (data.content || "").trim();
        if (text) return text;
        await sleep(400);
      }
      return "";
    };

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

      // The editor menu carries the Git entry with the count of changes.
      await page.click("[data-editor-menu]");
      await page.waitForSelector("[data-editor-menu-list].show", { timeout: 4000 });
      assert(await page.locator("[data-editor-git-item]").isVisible(), "the menu does not offer git");
      assert(/\d/.test(await page.locator("[data-editor-git-item-count]").textContent()), "the git entry carries no count");
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
      // Nothing starts picked: a change goes into the commit because somebody
      // picked it, never because it appeared.
      assert(await page.evaluate(() =>
        [...document.querySelectorAll(".editor-commit-row input")].every((el) => !el.checked)),
      "a fresh panel starts with rows picked");
      const startSummary = await page.locator("[data-editor-commit-summary]").textContent();
      assert(/^0 of \d+ changes$/.test(startSummary.trim()), `the summary starts at "${startSummary}"`);
      await page.setChecked('.editor-commit-grouprow[data-dir="deep"] input', true);
      await page.setChecked('.editor-commit-grouprow[data-dir="deep/nested"] input', false);
      assert(!(await page.locator('.editor-commit-row[data-path="deep/nested/two.txt"] input').isChecked()),
        "dropping the group left its file picked");
      assert(await page.locator('.editor-commit-row[data-path="deep/one.txt"] input').isChecked(),
        "dropping the subfolder took the parent's own file with it");
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

    await run("commit: an amend with nothing picked rewrites only the message", async () => {
      await openEditor(page);
      await openCommitView(page);
      await page.waitForFunction(() => !document.querySelector("[data-editor-commit-amend]").disabled, null, { timeout: 10000 });
      // Nothing picked and no amend: the button stands disabled however hard
      // the message tries.
      await clearPicks();
      await page.fill("[data-editor-commit-message]", "will not land");
      assert(await page.locator("[data-editor-commit-button]").isDisabled(), "an empty pick enables the commit");
      // Amend on: the borrowed message commits alone, the everyday typo fix.
      await page.setChecked("[data-editor-commit-amend]", true);
      await page.waitForFunction(() => {
        const value = document.querySelector("[data-editor-commit-message]").value;
        return value.length > 0 && value !== "will not land";
      }, null, { timeout: 8000 });
      const borrowed = await page.inputValue("[data-editor-commit-message]");
      await page.fill("[data-editor-commit-message]", `${borrowed}, message only`);
      await page.waitForFunction(() => !document.querySelector("[data-editor-commit-button]").disabled, null, { timeout: 8000 });
      await page.click("[data-editor-commit-button]");
      await page.waitForFunction((b) =>
        fetch(`${b}/git/commit`, { headers: { Accept: "application/json" } })
          .then((r) => r.json()).then((i) => /message only$/.test(i.lastMessage || "")), editorBase, { timeout: 20000 });
      await page.click("[data-editor-commit-close]");
      return "no pick needed with amend on, the tip reworded in place";
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
      await mp.click("[data-editor-git-item]");
      await mp.waitForSelector("[data-editor-sheet]:not([hidden])", { timeout: 6000 });
      await mp.locator("[data-editor-sheet-body] .dropdown-item", { hasText: /^Commit/ }).first().click();
      await mp.waitForSelector(".editor-drawer-open [data-editor-commit]:not([hidden])", { timeout: 10000 });
      assert(await mp.locator("[data-editor-commit-button]").isVisible(), "the commit button is not on screen");
      await mp.click("[data-editor-commit-close]");
      await mp.waitForSelector("[data-editor-commit]", { state: "hidden", timeout: 5000 });
      assert(await mp.locator(".editor-drawer-open").count() === 1, "closing the view closed the drawer with it");

      // A row of the list surfaces the file's diff, and the drawer goes with
      // every way out: a fresh open went through openPath, which closes it,
      // but a file that was already open behind another one used to keep the
      // drawer standing over the comparison it just built.
      await writeHere("drawer-a.txt", "da\n");
      await writeHere("drawer-b.txt", "db\n");
      await mp.click("[data-editor-refresh]");
      await mp.waitForSelector('.editor-item[data-path="drawer-a.txt"]', { timeout: 15000 });
      await mp.click('.editor-item[data-path="drawer-a.txt"]');
      await mp.waitForSelector('.editor-tab[data-path="drawer-a.txt"].active', { state: "attached", timeout: 10000 });
      await mp.waitForSelector(".editor.editor-drawer-open", { state: "detached", timeout: 5000 });
      await mp.click("[data-editor-drawer-toggle]");
      await mp.waitForSelector(".editor.editor-drawer-open", { timeout: 5000 });
      await mp.click('.editor-item[data-path="drawer-b.txt"]');
      await mp.waitForSelector('.editor-tab[data-path="drawer-b.txt"].active', { state: "attached", timeout: 10000 });
      await mp.waitForSelector(".editor.editor-drawer-open", { state: "detached", timeout: 5000 });
      await mp.click("[data-editor-drawer-toggle]");
      await mp.waitForSelector(".editor.editor-drawer-open", { timeout: 5000 });
      await mp.click("[data-editor-commit-toggle]");
      await mp.waitForSelector("[data-editor-commit]:not([hidden])", { timeout: 10000 });
      await mp.waitForSelector('.editor-commit-row[data-path="drawer-a.txt"]', { timeout: 15000 });
      await mp.click('.editor-commit-row[data-path="drawer-a.txt"] .editor-commit-open');
      await mp.waitForSelector(".editor.editor-drawer-open", { state: "detached", timeout: 8000 });
      await mp.waitForSelector('.editor-tab[data-path="drawer-a.txt"].active', { state: "attached", timeout: 10000 });
      await mp.waitForSelector(".cm-mergeView, .cm-deletedChunk, .cm-changedLine", { state: "attached", timeout: 20000 });
      return "the drawer carries the view, keeps standing when it closes, and goes when a row surfaces an open file's diff";
    });

    await run("commit: the draft lives with the project, hands off to a second session, and a commit spends it", async () => {
      await openEditor(page);
      await writeHere("draft-a.txt", "da\n");
      await writeHere("draft-b.txt", "db\n");
      await page.click("[data-editor-refresh]");
      await page.waitForSelector('.editor-item[data-path="draft-a.txt"]', { timeout: 15000 });
      await openCommitView(page);
      await pickOnly("draft-a.txt");
      await page.fill("[data-editor-commit-message]", "carried draft");
      const storedDraft = () => page.evaluate((b) =>
        fetch(`${b}/git/commit-draft`, { headers: { Accept: "application/json" } })
          .then((r) => r.json()), editorBase);
      const waitStored = async (want, label) => {
        const deadline = Date.now() + 15000;
        let d = null;
        while (Date.now() < deadline) {
          d = await storedDraft();
          if (want(d)) return;
          await sleep(400);
        }
        assert(false, `${label}: the server holds ${JSON.stringify(d)}`);
      };
      await waitStored((d) => d.message === "carried draft" && (d.paths || []).includes("draft-a.txt")
        && !(d.paths || []).includes("draft-b.txt"), "after typing and picking");

      await openEditor(page);
      await openCommitView(page);
      await page.waitForFunction(() =>
        document.querySelector("[data-editor-commit-message]").value === "carried draft", null, { timeout: 10000 });
      await page.waitForSelector('.editor-commit-row[data-path="draft-a.txt"]', { timeout: 15000 });
      assert(await page.locator('.editor-commit-row[data-path="draft-a.txt"] input').isChecked(),
        "the pick did not survive the reload");
      assert(!(await page.locator('.editor-commit-row[data-path="draft-b.txt"] input').isChecked()),
        "an unpicked row came back picked");

      const other = await ctx.newPage();
      L.wirePage(other, bag);
      try {
        await openEditor(other);
        await openCommitView(other);
        await other.waitForFunction(() =>
          document.querySelector("[data-editor-commit-message]").value === "carried draft", null, { timeout: 10000 });
        assert(await other.locator('.editor-commit-row[data-path="draft-a.txt"] input').isChecked(),
          "the handed-off pick is not checked");
        await other.fill("[data-editor-commit-message]", "carried draft, grown");
        await page.waitForFunction(() =>
          document.querySelector("[data-editor-commit-message]").value === "carried draft, grown", null, { timeout: 15000 });

        // An amend in progress travels too: the flag, the borrowed message as
        // it was edited, and the stash the amend displaced, which unchecking
        // gives back on whichever device does it.
        await other.waitForFunction(() => !document.querySelector("[data-editor-commit-amend]").disabled, null, { timeout: 10000 });
        await other.setChecked("[data-editor-commit-amend]", true);
        await other.waitForFunction(() => {
          const value = document.querySelector("[data-editor-commit-message]").value;
          return value.length > 0 && value !== "carried draft, grown";
        }, null, { timeout: 8000 });
        await other.fill("[data-editor-commit-message]", "amend carried over");
        await page.waitForFunction(() => document.querySelector("[data-editor-commit-amend]").checked
          && document.querySelector("[data-editor-commit-message]").value === "amend carried over", null, { timeout: 15000 });
        await page.setChecked("[data-editor-commit-amend]", false);
        await page.waitForFunction(() =>
          document.querySelector("[data-editor-commit-message]").value === "carried draft, grown", null, { timeout: 8000 });
        await other.waitForFunction(() => !document.querySelector("[data-editor-commit-amend]").checked
          && document.querySelector("[data-editor-commit-message]").value === "carried draft, grown", null, { timeout: 15000 });
      } finally {
        await other.close().catch(() => {});
      }

      await page.click("[data-editor-commit-button]");
      await page.waitForSelector('.editor-commit-row[data-path="draft-a.txt"]', { state: "detached", timeout: 20000 });
      assert((await page.inputValue("[data-editor-commit-message]")) === "", "the commit left the message standing");
      await waitStored((d) => (d.message || "") === "" && (d.paths || []).length === 0, "after the commit");

      await pickOnly("draft-b.txt");
      await waitStored((d) => (d.paths || []).includes("draft-b.txt"), "after picking the second file");
      assert(await post(page, `${editorBase}/delete`, { path: "draft-b.txt" }) === 200, "draft-b could not be removed");
      await page.waitForSelector('.editor-commit-row[data-path="draft-b.txt"]', { state: "detached", timeout: 15000 });
      await page.fill("[data-editor-commit-message]", "prune probe");
      await waitStored((d) => d.message === "prune probe" && (d.paths || []).length === 0,
        "after the picked file left the changes");

      await page.fill("[data-editor-commit-message]", "");
      await waitStored((d) => (d.message || "") === "", "after emptying the box");
      await page.click("[data-editor-commit-close]");
      return "stored on the server, handed off live with the amend, spent by the commit, pruned with the changes";
    });

    // ---- the git surface -----------------------------------------------------
    //
    // The branch stands in the statusbar with the ahead/behind arrows and is
    // the way into the git sheet, which carries every repository wide action:
    // switch branch, new branch, commit, push, pull (fast forward only),
    // fetch, force push, and the recent commits. Push, fetch and pull run
    // against a local bare repository under /tmp, never against anything
    // real. Routes: GET git/log (paged history, ?path= for one file's), GET
    // git/refs (a plain read; the branch list asks POST git/fetch with auto
    // first, so no GET of ours ever touches the network), POST git/push,
    // .../fetch, .../pull, .../checkout, .../branch.
    const waitBranch = (target, want) => target.waitForFunction((name) => {
      const el = document.querySelector("[data-editor-git-branch]");
      const btn = document.querySelector("[data-editor-git-status]");
      return !!btn && !btn.hidden && !!el && el.textContent === name;
    }, want, { timeout: 30000 });
    const waitArrow = (target, pattern) => target.waitForFunction((p) => {
      const ab = document.querySelector("[data-editor-git-ab]");
      if (p === "") return !!ab && ab.hidden;
      return !!ab && !ab.hidden && new RegExp(p).test(ab.textContent);
    }, pattern, { timeout: 30000 });
    const openGitSheet = async (target) => {
      await target.click("[data-editor-git-status]");
      await target.waitForSelector("[data-editor-sheet]:not([hidden])", { timeout: 6000 });
      await target.waitForSelector("[data-editor-sheet-body] .dropdown-item", { timeout: 6000 });
    };
    const sheetAction = (target, pattern) => target.locator("[data-editor-sheet-body] .dropdown-item", { hasText: pattern });
    const surfaceRemote = `/tmp/zzgit-surface-${tag}.git`;
    const surfacePeer = `/tmp/zzgit-peer-${tag}`;

    await run("the statusbar names the branch, counts ahead, and the sheet's push clears it", async () => {
      await openEditor(page);
      await diffReady(page);
      await waitBranch(page, "master");
      assert(await runInShell(
        `git init -q --bare ${surfaceRemote} && git remote add origin ${surfaceRemote} && git push -q -u origin master\r`,
      ) === 200, "the shell refused the remote setup");
      await writeHere("surface.txt", "away\n");
      assert(await runInShell(`git add surface.txt && git ${author} commit -qm surface\r`) === 200, "the shell refused the commit");
      await waitArrow(page, "↑1");
      await openGitSheet(page);
      const pushRow = sheetAction(page, /^Push/);
      assert(/1 commit to push/.test(await pushRow.first().textContent()), "the push row does not count the commit");
      await pushRow.first().click();
      await waitArrow(page, "");
      const pushed = await shellCount(`git --git-dir ${surfaceRemote} rev-list --count HEAD`);
      const local = await revCount();
      assert(pushed === local, `the remote holds ${pushed} of ${local} commits`);
      assert(await post(page, `${editorBase}/delete`, { path: "count.txt" }) === 200, "the count file could not be removed");
      return "↑1 shown, push delivered, arrows gone";
    });

    await run("the git sheet fetches on open, and pull only fast forwards", async () => {
      await openEditor(page);
      await diffReady(page);
      assert(await runInShell(
        `git clone -q ${surfaceRemote} ${surfacePeer} && git -C ${surfacePeer} ${author} commit -qm peer --allow-empty && git -C ${surfacePeer} push -q\r`,
      ) === 200, "the shell refused the peer");
      // The shell runs on its own time: the remote's count is what says the
      // peer's push has landed, and clearing FETCH_HEAD makes the sheet's
      // quiet fetch deterministically stale (a fresh one skips on purpose).
      assert(await runInShell("rm -f .git/FETCH_HEAD\r") === 200, "the shell refused the marker cleanup");
      const remoteCount = await shellCount(`git --git-dir ${surfaceRemote} rev-list --count HEAD`);
      assert(remoteCount >= 2, `the peer's push has not landed: ${remoteCount}`);
      assert(await post(page, `${editorBase}/delete`, { path: "count.txt" }) === 200, "the count file could not be removed");
      // Nothing here has fetched it, so nothing counts behind yet; opening
      // the sheet runs the quiet fetch, and the arrow arrives over the git
      // event.
      await openGitSheet(page);
      await waitArrow(page, "↓1");
      await sheetAction(page, /^Pull/).first().click();
      await waitArrow(page, "");
      return "↓1 after the sheet's own fetch, gone after the pull";
    });

    await run("a running action's spinner keeps the row's label in place", async () => {
      await openEditor(page);
      await diffReady(page);
      await openGitSheet(page);
      const fetchRow = () => page.evaluate(() => {
        const row = [...document.querySelectorAll("[data-editor-sheet-body] .dropdown-item")]
          .find((el) => /^Fetch/.test(el.textContent.trim()));
        if (!row) return null;
        const label = row.querySelector("span.d-flex.flex-column") || row;
        return { x: label.getBoundingClientRect().x, busy: !!row.querySelector(".spinner-border") };
      });
      const before = await fetchRow();
      assert(before && !before.busy, "the fetch row is not idle before the click");
      let releaseFetch = null;
      const heldFetch = new Promise((resolve) => { releaseFetch = resolve; });
      await page.route("**/git/fetch", async (route) => {
        await heldFetch;
        await route.continue().catch(() => {});
      });
      let during = null;
      try {
        await sheetAction(page, /^Fetch/).first().click();
        const deadline = Date.now() + 5000;
        while (Date.now() < deadline) {
          during = await fetchRow();
          if (during && during.busy) break;
          await sleep(50);
        }
      } finally {
        releaseFetch();
        await page.unroute("**/git/fetch");
      }
      assert(during && during.busy, "the spinner never showed on the tapped row");
      assert(Math.abs(during.x - before.x) < 0.5,
        `the label moved from ${before.x} to ${during.x} while the spinner stood`);
      await page.waitForFunction(() => {
        const row = [...document.querySelectorAll("[data-editor-sheet-body] .dropdown-item")]
          .find((el) => /^Fetch/.test(el.textContent.trim()));
        return row && !row.querySelector(".spinner-border");
      }, null, { timeout: 20000 });
      await page.keyboard.press("Escape");
      await sleep(300);
      return "the spinner takes the icon's box, the label stands still";
    });

    // The sheet is a list of rows, and the keyboard walks it: the first row
    // takes the focus when it opens, the arrows step and wrap over the enabled
    // rows, Enter runs the focused one, and Escape goes one level back before
    // it closes. The repaint is where this gets hard: a write disables every
    // row while it runs, so the focus has nowhere to sit, and the repaint that
    // ends the write has to put it back where it stood.
    const sheetRows = (target) => target.evaluate(() =>
      [...document.querySelectorAll("[data-editor-sheet-body] .dropdown-item, [data-editor-sheet-body] .editor-sheet-open")]
        .filter((row) => !row.disabled)
        .map((row) => row.textContent.replace(/\s+/g, " ").trim()));
    const sheetFocus = (target) => target.evaluate(() => {
      const el = document.activeElement;
      const body = document.querySelector("[data-editor-sheet-body]");
      const box = body ? body.getBoundingClientRect() : null;
      const rect = el.getBoundingClientRect();
      const style = getComputedStyle(el);
      // The surface may sit on the row around a list row's button, which is
      // what marks such a row over its full width.
      const line = getComputedStyle(el.closest(".editor-sheet-row") || el);
      const transparent = (value) => !value || value === "rgba(0, 0, 0, 0)" || value === "transparent";
      return {
        text: (el.textContent || "").replace(/\s+/g, " ").trim(),
        tag: el.tagName.toLowerCase(),
        inSheet: !!el.closest("[data-editor-sheet-body]"),
        inside: !!box && rect.top >= box.top - 1 && rect.bottom <= box.bottom + 1,
        paints: !transparent(style.backgroundColor) || !transparent(line.backgroundColor),
        surface: transparent(style.backgroundColor) ? line.backgroundColor : style.backgroundColor,
        outline: `${style.outlineWidth} ${style.outlineStyle}`,
        scrollTop: body ? body.scrollTop : 0,
        pageY: window.scrollY,
      };
    });

    await run("the git sheet takes the keyboard: the arrows walk it, Enter runs a row, Escape steps back", async () => {
      await openEditor(page);
      await diffReady(page);
      await openGitSheet(page);
      await sleep(300);
      const rows = await sheetRows(page);
      assert(rows.length > 2, `the sheet carries ${rows.length} rows`);
      const opened = await sheetFocus(page);
      assert(opened.inSheet && opened.text === rows[0], `the sheet opened with the focus on "${opened.text}"`);
      // The row the keyboard stands on is marked by a surface and never by a
      // ring, the same surface the mouse paints, and it is dragged into view as
      // the focus walks past the fold without the page moving under it.
      assert(opened.paints, "the row the sheet opened on paints nothing");
      assert(opened.outline === "0px none", `the focused row draws ${opened.outline}`);
      // A short window is what makes the list longer than the sheet, which is
      // the case the scrolling is for: every step keeps the focused row inside
      // the body, and the page behind it never moves.
      await page.setViewportSize({ width: 1200, height: 520 });
      await sleep(300);
      let scrolled = false;
      for (let i = 1; i < rows.length; i += 1) {
        await page.keyboard.press("ArrowDown");
        const step = await sheetFocus(page);
        assert(step.inside, `the focused row "${step.text}" stands outside the sheet body`);
        assert(step.pageY === 0, `the page scrolled to ${step.pageY} while the focus walked`);
        if (step.scrollTop > 0) scrolled = true;
      }
      assert(scrolled, "the sheet never scrolled while the focus walked past its edge");
      assert((await sheetFocus(page)).text === rows[rows.length - 1], "ArrowDown did not walk to the last row");
      await page.keyboard.press("ArrowDown");
      assert((await sheetFocus(page)).text === rows[0], "ArrowDown did not wrap to the first row");
      await page.keyboard.press("ArrowUp");
      assert((await sheetFocus(page)).text === rows[rows.length - 1], "ArrowUp did not wrap to the last row");

      // Enter on a held fetch: the rows go disabled under the focus, and the
      // answer brings it back to the row that was pressed.
      await page.evaluate(() => [...document.querySelectorAll("[data-editor-sheet-body] .dropdown-item")]
        .find((el) => /^Fetch/.test(el.textContent.trim())).focus());
      let releaseFetch = null;
      const heldFetch = new Promise((resolve) => { releaseFetch = resolve; });
      await page.route("**/git/fetch", async (route) => {
        await heldFetch;
        await route.continue().catch(() => {});
      });
      let during = null;
      try {
        await page.keyboard.press("Enter");
        const deadline = Date.now() + 5000;
        while (Date.now() < deadline) {
          during = await page.evaluate(() => ({
            rows: [...document.querySelectorAll("[data-editor-sheet-body] .dropdown-item")].map((el) => el.disabled),
            onRow: !!document.activeElement.closest("[data-editor-sheet-body] .dropdown-item"),
          }));
          if (during.rows.length && during.rows.every(Boolean)) break;
          await sleep(50);
        }
      } finally {
        releaseFetch();
        await page.unroute("**/git/fetch");
      }
      assert(during && during.rows.every(Boolean), "the rows never went disabled under the running fetch");
      assert(!during.onRow, "a disabled row kept the focus");
      await page.waitForFunction(() => {
        const el = document.activeElement;
        return !!el && /^Fetch/.test(el.textContent.trim());
      }, null, { timeout: 20000 });

      // A drilled level is a level of its own: it opens on its own first stop,
      // and Escape leads back to the row it was reached from, not out.
      await page.evaluate(() => [...document.querySelectorAll("[data-editor-sheet-body] .dropdown-item")]
        .find((el) => /^Switch branch/.test(el.textContent.trim())).focus());
      await page.keyboard.press("Enter");
      await page.waitForSelector("[data-editor-sheet-body] input", { timeout: 10000 });
      await sleep(400);
      assert((await sheetFocus(page)).tag === "input", "the branch picker did not start on its filter");
      await page.keyboard.press("Escape");
      await sleep(400);
      assert(await page.isVisible("[data-editor-sheet]:not([hidden])"), "Escape out of the picker closed the whole sheet");
      const back = await sheetFocus(page);
      assert(back.inSheet && /^Switch branch/.test(back.text), `back on the git sheet the focus stands on "${back.text}"`);
      await page.keyboard.press("Escape");
      await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 6000 });
      await page.setViewportSize({ width: 1360, height: 900 });
      await sleep(300);
      return `${rows.length} rows walked on ${opened.surface}, focus survived the write, picker level stepped back`;
    });

    // The sheet is the editor's own panel: it docks to the right on a screen
    // that has the room and stays the full width bottom sheet on a phone, and
    // it never leaves the editor, which is what its click-away close hangs on.
    await run("the sheet docks right where there is room and stays a bottom sheet on a phone", async () => {
      await openEditor(page);
      await diffReady(page);
      await openGitSheet(page);
      await sleep(300);
      const shape = () => page.evaluate(() => {
        const sheet = document.querySelector("[data-editor-sheet]");
        const panel = document.querySelector("[data-editor-sheet-panel]");
        const s = sheet.getBoundingClientRect();
        const p = panel.getBoundingClientRect();
        return {
          share: p.width / s.width,
          heightShare: p.height / s.height,
          rightGap: Math.round(s.right - p.right),
          bottomGap: Math.round(s.bottom - p.bottom),
          radius: getComputedStyle(panel).borderRadius,
        };
      });
      const wide = await shape();
      assert(Math.abs(wide.share - 0.75) < 0.02, `the panel takes ${(wide.share * 100).toFixed(1)}% of the width`);
      assert(wide.rightGap === 0 && wide.bottomGap === 0, `the panel sits ${wide.rightGap}px from the right and ${wide.bottomGap}px from the bottom`);
      assert(wide.heightShare <= 0.86, `the panel is ${(wide.heightShare * 100).toFixed(1)}% tall, the height was to stay`);
      assert(/^12px 0px 0px/.test(wide.radius), `the rounding reads ${wide.radius}, it belongs on the left edge`);
      await page.setViewportSize({ width: 390, height: 844 });
      await sleep(400);
      const phone = await shape();
      assert(Math.abs(phone.share - 1) < 0.02, `on a phone the panel takes ${(phone.share * 100).toFixed(1)}%`);
      assert(phone.bottomGap === 0, "on a phone the panel left the bottom edge");
      // The backdrop is what a click outside closes on, and it is still there:
      // the strip of the sheet above the panel is that backdrop.
      const spot = await page.evaluate(() => {
        const s = document.querySelector("[data-editor-sheet]").getBoundingClientRect();
        const p = document.querySelector("[data-editor-sheet-panel]").getBoundingClientRect();
        return { x: Math.round(s.left + s.width / 2), y: Math.round(s.top + Math.max(4, (p.top - s.top) / 2)) };
      });
      await page.mouse.click(spot.x, spot.y);
      await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 6000 });
      await page.setViewportSize({ width: 1360, height: 900 });
      await sleep(300);
      return `wide ${(wide.share * 100).toFixed(0)}% docked right, ${(wide.heightShare * 100).toFixed(0)}% tall; phone full width`;
    });

    await run("force push asks first, and a cancelled question leaves the remote alone", async () => {
      await openEditor(page);
      await diffReady(page);
      // A rewritten tip is no fast forward: the plain push has nothing to do
      // here, the force one is the only way, and it asks before it goes. The
      // tip the pull left standing is the peer's empty commit, and amending
      // one of those is what --allow-empty is for.
      assert(await runInShell(`git ${author} commit -q --amend --allow-empty -m FORCED\r`) === 200, "the shell refused the amend");
      await waitArrow(page, "↑1");
      const before = await shellText(`git --git-dir ${surfaceRemote} log -1 --format=%s`);
      assert(!/FORCED/.test(before), `the remote already carries the rewrite: ${before}`);

      await openGitSheet(page);
      await sheetAction(page, /^Force push/).first().click();
      await page.waitForSelector(".swal2-popup:not(.swal2-toast)", { state: "visible", timeout: 8000 });
      const asked = await page.textContent(".swal2-html-container");
      assert(/force-with-lease/.test(asked), `the question does not name the lease: ${asked}`);
      // Cancelled means nothing ran, not "ran and was undone".
      await page.click(".swal2-cancel");
      await page.waitForSelector(".swal2-popup:not(.swal2-toast)", { state: "hidden", timeout: 8000 });
      await sleep(1500);
      const stillThere = await shellText(`git --git-dir ${surfaceRemote} log -1 --format=%s`);
      assert(stillThere === before, `the cancelled question moved the remote to ${stillThere}`);

      // The sheet is excepted from the body's close-on-action-click, so it
      // stands through a cancelled question and the next try starts at the row.
      assert(await page.isVisible("[data-editor-sheet]:not([hidden])"), "the sheet closed under the cancelled question");
      await sheetAction(page, /^Force push/).first().click();
      await page.waitForSelector(".swal2-popup:not(.swal2-toast)", { state: "visible", timeout: 8000 });
      await page.click(".swal2-confirm");
      await waitArrow(page, "");
      const after = await shellText(`git --git-dir ${surfaceRemote} log -1 --format=%s`);
      assert(/FORCED/.test(after), `the remote did not follow the rewrite: ${after}`);
      assert(await post(page, `${editorBase}/delete`, { path: "count.txt" }) === 200, "the count file could not be removed");
      return "asked, cancelled without effect, then the rewrite landed";
    });

    // What ssh and git ask when they cannot get in reaches the browser through
    // the askpass bridge: the server points SSH_ASKPASS and GIT_ASKPASS of one
    // user-triggered action at a stub, the stub reports the prompt line over
    // the broker's unix socket and blocks, the standing question is server
    // state announced on the gitprompt event, the app-wide dialog in
    // @dc/gitprompt pulls GET /git/prompt and shows it naming the project and
    // the action, and the masked answer posts back to the waiting helper.
    // Nothing real is contacted here: core.sshCommand of this one repository
    // points at a script that only asks the question a passphrased key would
    // ask and writes down what came back. Which is also the regression signal,
    // because without the bridge SSH_ASKPASS is /bin/false and no dialog ever
    // appears.
    const fakeSsh = "zz-fake-ssh";
    const askAnswer = "zz-ask-answer.txt";
    await run("the askpass bridge carries the question, the answer and the cancel", async () => {
      await openEditor(page);
      await diffReady(page);
      assert(await post(page, `${editorBase}/file`, {
        path: fakeSsh,
        content: [
          "#!/bin/sh",
          "# Stands in for ssh: talks to nothing, asks the one question a key",
          "# with a passphrase asks, and writes down what came back.",
          "# git probes the configuration first (ssh -G <host>) and only then",
          "# opens the connection. Real ssh never asks anything on the probe,",
          "# so neither does this, or every push would ask twice.",
          'case "$1" in -G) exit 0 ;; esac',
          'here=$(dirname "$0")',
          'if answer=$("$SSH_ASKPASS" "Enter passphrase for key \'/zz/fake_ed25519\':"); then',
          '  printf %s "$answer" > "$here/' + askAnswer + '"',
          "else",
          '  printf denied > "$here/' + askAnswer + '"',
          "fi",
          'echo "zz-fake-ssh: no such identity" >&2',
          "exit 255",
          "",
        ].join("\n"),
      }) === 200, "writing the ssh stand-in failed");
      assert(await runInShell(
        `chmod +x ${fakeSsh} && git config core.sshCommand "$PWD/${fakeSsh}" && git remote set-url origin ssh://zz-fake.invalid/repo.git\r`,
      ) === 200, "the shell refused the ssh wiring");
      // The push has to have something to send, or git answers before it ever
      // reaches the transport.
      await writeHere("ask-probe.txt", "probe\n");
      assert(await runInShell(`git add ask-probe.txt && git ${author} commit -qm "ask probe"\r`) === 200, "the shell refused the commit");

      // A commit that is ready to go is what makes the lock check below say
      // anything: a message plus the picked rows is the one state in which the
      // commit button is live, so seeing it dead during the push is the claim
      // and not the empty field's doing.
      await page.click("[data-editor-commit-toggle]");
      await page.waitForSelector(".editor-commit-row", { timeout: 20000 });
      await page.setChecked("[data-editor-commit-all]", true);
      await page.fill("[data-editor-commit-message]", "would commit");
      // The rows arrive with a status round, so this waits for the button to
      // be live rather than assuming it already is.
      await page.waitForFunction(() => !document.querySelector("[data-editor-commit-button]").disabled,
        null, { timeout: 30000 });

      await openGitSheet(page);
      await sheetAction(page, /^Push/).first().click();
      await page.waitForSelector(".swal2-popup:not(.swal2-toast) input.swal2-input", { timeout: 20000 });
      const question = await page.textContent(".swal2-html-container");
      assert(/Enter passphrase for key '\/zz\/fake_ed25519':/.test(question),
        `the dialog does not carry ssh's own line: ${question}`);
      // Above ssh's line the dialog carries this server's own truth, the
      // project and the action, kept apart from whatever the helper reported.
      assert(question.includes(`${project} · push`),
        `the dialog does not name project and action: ${question}`);
      assert(await page.getAttribute(".swal2-popup input.swal2-input", "type") === "password",
        "the answer field is not masked");

      // One write at a time, and the running one holds the whole surface: the
      // statusbar carries the spinner in the branch's place, every sheet row
      // is out, and the commit button is out with them. Two locks side by side
      // is what would let a checkout and a commit run at each other.
      const busy = await page.evaluate(() => ({
        spinner: !document.querySelector("[data-editor-git-spin]").hidden,
        icon: document.querySelector("[data-editor-git-icon]").hidden,
        rows: [...document.querySelectorAll("[data-editor-sheet-body] .dropdown-item")].map((el) => el.disabled),
        commit: document.querySelector("[data-editor-commit-button]").disabled,
      }));
      assert(busy.spinner && busy.icon, "the statusbar does not show the running action");
      assert(busy.rows.length > 0 && busy.rows.every(Boolean), `a sheet row stayed live while a write ran: ${JSON.stringify(busy.rows)}`);
      assert(busy.commit, "the commit button stayed live while a sheet action ran");

      // The question is the cockpit's and not this page's: any signed-in page
      // shows it, the projects page included, and the answer given on one
      // device takes the dialog down on every other. A fresh page catches up
      // through the connect snapshot's bare gitprompt signal, so nothing has
      // to happen for the dialog to arrive.
      const second = await page.context().newPage();
      await second.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await second.waitForSelector(".swal2-popup:not(.swal2-toast) input.swal2-input", { timeout: 20000 });

      await page.fill(".swal2-popup input.swal2-input", "opensesame");
      await page.click(".swal2-confirm");
      await second.waitForSelector(".swal2-popup:not(.swal2-toast)", { state: "hidden", timeout: 20000 });
      await second.close();
      // The stand-in refuses whatever it gets, so the push ends in git's
      // words; what matters is that the typed answer reached the helper.
      assert(await readHere(askAnswer) === "opensesame", "the answer never reached the helper");
      const cleared = await page.waitForFunction(() => !document.querySelector("[data-editor-git-spin]") ||
        document.querySelector("[data-editor-git-spin]").hidden, null, { timeout: 30000 }).then(() => true).catch(() => false);
      if (!cleared) {
        const state = await page.evaluate(() => ({
          dialog: !!document.querySelector(".swal2-popup:not(.swal2-toast)"),
          dialogText: (document.querySelector(".swal2-html-container") || {}).textContent || "",
          toasts: [...document.querySelectorAll(".dc-toast")].map((t) => t.textContent),
        }));
        throw new Error(`the write never released: ${JSON.stringify(state)}`);
      }

      // Cancel denies the helper, and the refusal says so instead of hiding
      // behind an authentication failure.
      assert(await post(page, `${editorBase}/delete`, { path: askAnswer }) === 200, "the answer file could not be removed");
      assert(await page.isVisible("[data-editor-sheet]:not([hidden])"), "the sheet closed under the running action");
      await sheetAction(page, /^Push/).first().click();
      await page.waitForSelector(".swal2-popup:not(.swal2-toast) input.swal2-input", { timeout: 20000 });
      await page.click(".swal2-cancel");
      assert(await readHere(askAnswer) === "denied", "the cancel never reached the helper");
      await page.waitForFunction(() => [...document.querySelectorAll(".dc-toast")]
        .some((t) => /the question was cancelled/.test(t.textContent)), null, { timeout: 30000 });

      assert(await runInShell(
        `git config --unset core.sshCommand && git remote set-url origin ${surfaceRemote} && git reset -q --hard HEAD~1\r`,
      ) === 200, "the shell refused the cleanup");
      await post(page, `${editorBase}/delete`, { path: fakeSsh }).catch(() => {});
      await post(page, `${editorBase}/delete`, { path: askAnswer }).catch(() => {});
      // The draft is per project and survives a reload, so this one goes back
      // out instead of standing in the next check's panel.
      await page.fill("[data-editor-commit-message]", "");
      await page.click("[data-editor-sheet-close]");
      await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 6000 });
      await page.click("[data-editor-commit-close]");
      await waitArrow(page, "");
      return "prompt shown on both pages, answer delivered and closed everywhere, surface locked, cancel named";
    });

    await run("a new branch and the switch back, both from the sheet", async () => {
      const probe = `probe-${tag}`;
      await openEditor(page);
      await diffReady(page);
      await openGitSheet(page);
      await sheetAction(page, /^New branch/).first().click();
      await page.waitForSelector(".swal2-popup input.swal2-input", { timeout: 8000 });
      await page.fill(".swal2-popup input.swal2-input", probe);
      await page.click(".swal2-confirm");
      await waitBranch(page, probe);
      // The sheet stays open after the creation, its head row repainted; the
      // switch drills from it into the branch list, whose filter narrows and
      // whose exact name tells master from origin/master.
      await page.click('[data-editor-sheet-body] .dropdown-item[title="Switch branch"]');
      await page.waitForSelector("[data-editor-sheet-body] input", { timeout: 8000 });
      await page.fill("[data-editor-sheet-body] input", "master");
      await page.click('[data-editor-sheet] .editor-sheet-row:has(.editor-sheet-name:text-is("master")) .editor-sheet-open');
      await waitBranch(page, "master");
      assert(await runInShell(
        `git branch -qd ${probe} && git remote remove origin && rm -rf ${surfaceRemote} ${surfacePeer}\r`,
      ) === 200, "the shell refused the cleanup");
      return "created, switched, back on master";
    });

    await run("file history opens the diff against the picked commit, a typed revision works too", async () => {
      await setDiffView(page, "side");
      await openTracked();
      await page.click(".editor-tab.active", { button: "right" });
      await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
      await menuItem("File history").first().click();
      await page.waitForSelector("[data-editor-sheet]:not([hidden])", { timeout: 8000 });
      await page.waitForFunction(() => document.querySelectorAll("[data-editor-sheet-body] .editor-sheet-row").length > 0, null, { timeout: 15000 });
      await page.locator("[data-editor-sheet-body] .editor-sheet-row .editor-sheet-open").first().click();
      await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 8000 });
      await page.waitForSelector(".cm-mergeView", { state: "attached", timeout: 20000 });
      // The tab stores the picked commit in the same field the HEAD switch
      // fills, so a reload would come back into this very comparison.
      const storedRev = await page.evaluate((key) => {
        const saved = JSON.parse(localStorage.getItem(key) || "null");
        const entry = saved && saved.open.find((e) => e && typeof e === "object" && e.path === "sub/tracked.txt");
        return entry ? entry.diff : null;
      }, `dc-editor-tabs:${project}`);
      assert(/^[0-9a-f]{40}$/.test(storedRev || ""), `the tab stores "${storedRev}" as the revision`);

      await page.click(".editor-tab.active", { button: "right" });
      await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
      await menuItem("Diff against revision").first().click();
      await page.waitForSelector("[data-editor-sheet-body] input", { timeout: 8000 });
      await page.fill("[data-editor-sheet-body] input", "HEAD~1");
      await page.keyboard.press("Enter");
      await page.waitForSelector(".cm-mergeView", { state: "attached", timeout: 20000 });
      await page.waitForFunction(() => /Diff against HEAD~1/.test(document.querySelector("[data-editor-status]").textContent), null, { timeout: 10000 });
      await toggleDiff(page);
      await page.waitForFunction(() => !document.querySelector(".cm-mergeView"), null, { timeout: 20000 });
      await setDiffView(page, "auto");
      return "history row and typed revision both land in the comparison";
    });

    // ---- switching the revision under an open diff ---------------------------
    //
    // One file in three states: two commits and the working copy, so the
    // revision side has something different to say for each of them and a
    // switch that did not happen cannot pass as one. The inline view is where
    // this broke: it is an extension on the open editor, and reconfiguring a
    // compartment that already holds one keeps the StateField values it had,
    // so the new revision never arrived and the old comparison stayed on
    // screen while the status line already named the new one.
    const revFile = "sub/rev.txt";
    const revAlpha = `rev alpha ${tag}`;
    const revBeta = `rev beta ${tag}`;
    let revOne = null;
    let revTwo = null;

    const logOf = (path) => page.evaluate(([b, p]) =>
      fetch(`${b}/git/log?path=${encodeURIComponent(p)}`, { headers: { Accept: "application/json" } })
        .then((r) => r.json()), [editorBase, path]);

    const openRevFile = async () => {
      await page.goto(editorURL, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      await L.waitUpgraded(page, ["dc-editor"]);
      await page.waitForSelector(".cm-editor", { state: "attached", timeout: 15000 });
      if (!(await page.$(`.editor-item[data-path="${revFile}"]`))) {
        if (!(await page.$(`.editor-item[data-path="${tracked}"]`))) await page.click('.editor-item[data-path="sub"]');
        await page.click("[data-editor-refresh]");
      }
      await page.waitForSelector(`.editor-item[data-path="${revFile}"]`, { timeout: 15000 });
      await page.click(`.editor-item[data-path="${revFile}"]`);
      await page.waitForSelector(`.editor-tab[data-path="${revFile}"].active`, { timeout: 10000 });
      await diffReady(page);
    };

    // What the revision side of the open comparison reads, whichever view is
    // up: the first editor of the two pane view, the removed blocks inline.
    const revisionText = () => page.evaluate(() => {
      const merge = document.querySelector(".cm-mergeView");
      if (merge) return merge.querySelectorAll(".cm-content")[0].textContent;
      return [...document.querySelectorAll(".cm-deletedChunk")].map((n) => n.textContent).join(" ");
    });
    const waitRevision = (want, missing) => page.waitForFunction(([w, m]) => {
      const merge = document.querySelector(".cm-mergeView");
      const text = merge
        ? merge.querySelectorAll(".cm-content")[0].textContent
        : [...document.querySelectorAll(".cm-deletedChunk")].map((n) => n.textContent).join(" ");
      return text.includes(w) && !text.includes(m);
    }, [want, missing], { timeout: 20000 });

    const openPickerFromTab = async (label) => {
      await page.click(".editor-tab.active", { button: "right" });
      await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
      await menuItem(label).first().click();
      await page.waitForSelector("[data-editor-sheet-body] input", { timeout: 8000 });
      await page.waitForSelector("[data-picker-loading]", { state: "hidden", timeout: 20000 });
    };
    // Typing is debounced, so a row is only asked for once the round it
    // belongs to has come back.
    const searchPicker = async (text) => {
      await page.fill("[data-editor-sheet-body] input", text);
      await page.waitForSelector("[data-picker-loading]", { state: "hidden", timeout: 20000 });
    };
    const pickerRow = (text) => page.locator("[data-editor-sheet-body] .editor-sheet-row", { hasText: text });

    await run("a diff switches to another revision in place, in both views and from both ways in", async () => {
      assert(await runInShell(
        `printf 'one\\n' > ${revFile} && git add ${revFile} && git ${author} commit -qm "${revAlpha}" && `
        + `printf 'two\\n' > ${revFile} && git add ${revFile} && git ${author} commit -qm "${revBeta}" && `
        + `printf 'work\\n' > ${revFile}\r`,
      ) === 200, "the shell refused the two revisions");
      const deadline = Date.now() + 45000;
      while (Date.now() < deadline) {
        const page1 = await logOf(revFile);
        if (page1.commits && page1.commits.length === 2) {
          [revTwo, revOne] = page1.commits;
          break;
        }
        await sleep(1000);
      }
      assert(revOne && revTwo, "the two revisions never landed in the history");
      assert(revOne.summary === revAlpha && revTwo.summary === revBeta, `the history reads ${revTwo && revTwo.summary} / ${revOne && revOne.summary}`);

      for (const view of ["inline", "side"]) {
        await openRevFile();
        await setDiffView(page, view);
        // Start where a person starts, at HEAD, which is the second commit.
        if (!(await diffPressed(page))) await toggleDiff(page);
        await waitRevision("two", "one");

        // The revision picker: type a piece of the older commit's subject and
        // take that row. The status line names it, and the revision side has
        // to say what that commit said.
        await openPickerFromTab("Diff against revision");
        await searchPicker(revAlpha);
        await pickerRow(revAlpha).first().locator(".editor-sheet-open").click();
        await page.waitForFunction((sha) => new RegExp(`Diff against ${sha}`).test(document.querySelector("[data-editor-status]").textContent), revOne.sha, { timeout: 15000 });
        await waitRevision("one", "two").catch(async () => {
          throw new Error(`${view}: the revision picker did not move the comparison, it still reads ${await revisionText()}`);
        });
        assert(view === "side" ? await page.locator(".cm-mergeView").count() === 1 : await page.locator(".cm-mergeView").count() === 0,
          `${view}: the comparison is not in the view that was picked`);

        // And the same the other way in, the file history, back to the newer
        // commit. A switch that does nothing would leave "one" standing.
        await page.click(".editor-tab.active", { button: "right" });
        await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
        await menuItem("File history").first().click();
        await page.waitForSelector("[data-editor-sheet-body] .editor-sheet-row", { timeout: 15000 });
        await page.locator("[data-editor-sheet-body] .editor-sheet-row", { hasText: revBeta }).first().locator(".editor-sheet-open").first().click();
        await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 8000 });
        await waitRevision("two", "one").catch(async () => {
          throw new Error(`${view}: the file history did not move the comparison, it still reads ${await revisionText()}`);
        });
      }

      await setDiffView(page, "auto");
      return "revision picker and file history both switch, inline and side by side";
    });

    await run("a commit under an open inline diff moves the revision side along", async () => {
      await openRevFile();
      await setDiffView(page, "inline");
      // Against HEAD and not against the hash the last check left on the tab:
      // a commit does not move an immutable revision, so a diff against one
      // would have nothing to follow and the check would prove nothing.
      if (await diffPressed(page)) await toggleDiff(page);
      await toggleDiff(page);
      await waitRevision("two", "one");
      // The base moves, which is the one thing that makes an open comparison
      // stale. Nothing is asked of the person and the buffer is not touched:
      // only the revision side follows, through setOriginal, which is the
      // inline path with the same reconfigure trap under it.
      assert(await runInShell(`git add ${revFile} && git ${author} commit -qm "rev gamma ${tag}"\r`) === 200, "the shell refused the commit");
      await page.waitForFunction(() => !document.querySelector(".cm-deletedChunk"), null, { timeout: 40000 })
        .catch(() => { throw new Error("the inline diff never followed the moved base"); });
      const buffer = await page.evaluate(() => document.querySelector("[data-editor-surface] .cm-content").textContent);
      assert(/work/.test(buffer), `the buffer was rewritten by the follow: ${buffer}`);
      await toggleDiff(page);
      await setDiffView(page, "auto");
      return "the inline revision side followed the commit, the buffer stood";
    });

    await run("the revision picker finds commits, the branch picker stays with names", async () => {
      await openRevFile();
      await openPickerFromTab("Diff against revision");

      // By a piece of the subject: the row names the short hash and the
      // subject, with the author and the date under it.
      await searchPicker(revAlpha);
      const row = pickerRow(revAlpha).first();
      assert(await row.count() === 1, `the subject search found no commit row for "${revAlpha}"`);
      const name = (await row.locator(".editor-sheet-name").textContent()).trim();
      const sub = (await row.locator(".editor-sheet-dir").textContent()).trim();
      assert(name.startsWith(revOne.sha.slice(0, 7)), `the row does not lead with the short hash: ${name}`);
      assert(/e2e/.test(sub) && /\d/.test(sub), `the row does not carry author and date: ${sub}`);

      // And by a hash prefix, which is not a subject and only rev-parse can
      // resolve.
      await searchPicker(revOne.sha.slice(0, 8));
      const byHash = pickerRow(revOne.sha.slice(0, 7)).first();
      assert(await byHash.count() === 1, `the hash prefix found nothing: ${revOne.sha.slice(0, 8)}`);
      await byHash.locator(".editor-sheet-open").click();
      await page.waitForFunction((sha) => new RegExp(`Diff against ${sha}`).test(document.querySelector("[data-editor-status]").textContent), revOne.sha, { timeout: 15000 });
      // The value the row carries is the whole hash, so the tab compares
      // against exactly that commit and not against what a prefix might grow
      // into.
      const storedRev = await page.evaluate((key) => {
        const saved = JSON.parse(localStorage.getItem(key) || "null");
        const entry = saved && saved.open.find((e) => e && typeof e === "object" && e.path === "sub/rev.txt");
        return entry ? entry.diff : null;
      }, `dc-editor-tabs:${project}`);
      assert(storedRev === revOne.sha, `the tab stores "${storedRev}" and not the full hash`);

      // A checkout of a hash is a detached HEAD, so the branch picker never
      // offers one.
      await openGitSheet(page);
      await page.click('[data-editor-sheet-body] .dropdown-item[title="Switch branch"]');
      await page.waitForSelector("[data-editor-sheet-body] input", { timeout: 15000 });
      await searchPicker(revAlpha);
      assert(await pickerRow(revOne.sha.slice(0, 7)).count() === 0, "the branch picker offered a commit");
      await page.click("[data-editor-sheet-close]");
      await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 6000 });
      await toggleDiff(page);
      return "subject and hash both find the commit, the branch picker does not";
    });

    await run("the picker's search is the server's, debounced, and a slow answer never wins", async () => {
      await openRevFile();
      const asked = [];
      const count = (request) => {
        if (request.url().includes("/editor/git/refs")) asked.push(new URL(request.url()).search);
      };
      // One query is held back long enough to come home after a newer one.
      await page.route("**/editor/git/refs*", async (route) => {
        const q = new URL(route.request().url()).searchParams.get("q") || "";
        if (q === "slow") await new Promise((done) => setTimeout(done, 2500));
        // The held back round may outlive the interception itself: the check
        // unroutes as soon as it is done, and playwright then handles what is
        // still parked. Continuing it twice is an uncaught rejection that
        // takes the whole runner down, so this one is allowed to be late.
        await route.continue().catch(() => {});
      });
      page.on("request", count);
      try {
        await openPickerFromTab("Diff against revision");
        assert(asked.length === 1, `opening the picker asked ${asked.length} times`);
        assert(/kinds=/.test(asked[0]) && !/q=/.test(asked[0]), `the opening round is not the plain list: ${asked[0]}`);

        // Typing goes out once, not once per character, and it carries the
        // text: nothing is filtered here any more.
        asked.length = 0;
        await page.click("[data-editor-sheet-body] input");
        await page.keyboard.type(revAlpha, { delay: 20 });
        await page.waitForSelector("[data-picker-loading]", { state: "hidden", timeout: 20000 });
        assert(asked.length === 1, `${revAlpha.length} characters cost ${asked.length} rounds`);
        assert(/[?&]q=/.test(asked[0]) && /kinds=.*commit/.test(decodeURIComponent(asked[0])),
          `the round does not carry the search: ${asked[0]}`);
        assert(await pickerRow(revAlpha).count() === 1, "the searched round shows no hit");

        // While a round is out the sheet says so.
        await page.fill("[data-editor-sheet-body] input", "slow");
        await page.waitForSelector("[data-picker-loading]", { state: "visible", timeout: 5000 });
        // Past the debounce, so the held back round is really out on the wire:
        // typing again inside it would only cancel a timer.
        await sleep(600);
        // A newer query lands first; the slow one comes home two seconds later
        // and must not paint its own list over it.
        await searchPicker(revAlpha);
        assert(await pickerRow(revAlpha).count() === 1, "the newer round did not paint");
        await sleep(3000);
        assert(await pickerRow(revAlpha).count() === 1, "the slow answer painted over the newer one");
        assert(await page.locator("[data-picker-loading]").isVisible() === false, "the loading state stayed up");

        // A raw revision typed past the list still goes through on Enter while
        // a round is running, which is the one path that may never wait.
        await page.fill("[data-editor-sheet-body] input", "slow");
        await page.waitForSelector("[data-picker-loading]", { state: "visible", timeout: 5000 });
        await sleep(600);
        await page.fill("[data-editor-sheet-body] input", "HEAD~1");
        await page.keyboard.press("Enter");
        await page.waitForFunction(() => /Diff against HEAD~1/.test(document.querySelector("[data-editor-status]").textContent), null, { timeout: 20000 });
      } finally {
        page.off("request", count);
        await page.unroute("**/editor/git/refs*");
      }
      await toggleDiff(page);
      await page.click(`.editor-tab[data-path="${revFile}"] .editor-tab-close`).catch(() => {});
      await sleep(400);
      return "one round per pause, the text goes to git, the slow answer is dropped";
    });

    await run("the git surface fits the phone", async () => {
      const mp = await mobilePage();
      await openEditor(mp);
      await diffReady(mp);
      // A phone with no tab open opens the drawer by itself, and its backdrop
      // would swallow the tap on the statusbar.
      await mp.keyboard.press("Escape");
      await mp.waitForSelector("[data-editor-backdrop]", { state: "hidden", timeout: 5000 });
      const btn = mp.locator("[data-editor-git-status]");
      assert(await btn.isVisible(), "the statusbar branch is not on screen at phone width");
      await btn.click();
      await mp.waitForSelector("[data-editor-sheet]:not([hidden])", { timeout: 6000 });
      await mp.waitForSelector("[data-editor-sheet-body] .dropdown-item", { timeout: 6000 });
      // Every action row is a real touch target inside the viewport.
      const rows = await mp.evaluate(() => [...document.querySelectorAll("[data-editor-sheet-body] .dropdown-item")]
        .map((el) => {
          const rect = el.getBoundingClientRect();
          return { text: el.textContent.trim().slice(0, 14), h: rect.height, left: rect.left, right: rect.right, vw: window.innerWidth };
        }));
      assert(rows.length >= 7, `the sheet carries ${rows.length} actions`);
      for (const row of rows) {
        assert(row.h >= 36, `a row is ${Math.round(row.h)}px tall: ${row.text}`);
        assert(row.left >= 0 && row.right <= row.vw + 1, `a row leaves the viewport: ${JSON.stringify(row)}`);
      }
      // The picker opens, filters and closes without leaving the sheet world.
      await mp.click('[data-editor-sheet-body] .dropdown-item[title="Switch branch"]');
      await mp.waitForSelector("[data-editor-sheet-body] input", { timeout: 6000 });
      await mp.waitForSelector("[data-editor-sheet] .editor-sheet-row", { timeout: 10000 });
      await mp.click("[data-editor-sheet-close]");
      await mp.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 6000 });
      return "branch button, sheet rows and picker usable at phone width";
    });

    await run("a project without a repository offers the clone, and the clone fills it", async () => {
      const cloneSrc = `/tmp/zzgit-clone-${tag}.git`;
      assert(await runInShell(`git clone -q --bare . ${cloneSrc}\r`) === 200, "the shell refused the bare copy");
      const bared = await shellCount(`git --git-dir ${cloneSrc} rev-list --count HEAD`);
      assert(bared >= 1, `the bare copy is empty: ${bared}`);
      assert(await post(page, `${editorBase}/delete`, { path: "count.txt" }) === 200, "the count file could not be removed");

      // The clone wants an empty directory and git refuses anything else, so
      // the check gets a fresh project; the plain one carries a probe file by
      // now.
      const cloneProject = `zzgit-empty-${tag}`;
      await L.createProject(page, cloneProject);
      await page.goto(`${BASE}/projects/${encodeURIComponent(cloneProject)}/editor`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      await L.waitUpgraded(page, ["dc-editor"]);
      await diffReady(page, false);
      // The statusbar says what is missing and is the way in.
      await page.waitForFunction(() => {
        const btn = document.querySelector("[data-editor-git-status]");
        return btn && !btn.hidden && /No repository/.test(btn.textContent);
      }, null, { timeout: 15000 });
      await openGitSheet(page);
      const cloneRow = sheetAction(page, /^Clone repository/);
      assert(await cloneRow.first().isVisible(), "the sheet does not offer the clone");
      await cloneRow.first().click();
      await page.waitForSelector(".swal2-popup input.swal2-input", { timeout: 8000 });
      await page.fill(".swal2-popup input.swal2-input", cloneSrc);
      await page.click(".swal2-confirm");
      // Tree, statusbar and marks follow the clone on their own.
      await waitBranch(page, "master");
      await page.waitForSelector('.editor-item[data-path="root.txt"]', { timeout: 20000 });
      assert(await runInShell(`rm -rf ${cloneSrc}\r`) === 200, "the shell refused the cleanup");
      await L.deleteProject(page, cloneProject).catch(() => {});
      return "no repository named in the statusbar, one clone later the branch is";
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

    // ---- the two sides scroll together ---------------------------------------

    // Vertically the outer .cm-mergeView is the one scroller of both editors,
    // the package's own shape: the editors grow to their full height inside it
    // and neither can scroll on its own. Sideways each editor keeps its own
    // .cm-scroller, and that is where a comparison used to fall apart: with
    // wrapping off the left column stood at one place and the right one at
    // another, so the two halves of a line were no longer next to each other.
    // Both merge views are checked, the git diff and the comparison of two
    // files on disk, and long lines are what makes the axis exist at all.
    // The folding is turned off for these: with it on a comparison shrinks to
    // a few lines around each change and the vertical axis has nothing left to
    // prove.
    const wideDoc = (lines, mark, width) => Array.from({ length: lines }, (_, row) =>
      `${mark} line ${row + 1} ` + Array.from({ length: width }, (_, i) => `${mark}_${row + 1}_${i}`).join(" ")).join("\n") + "\n";
    // What both sides stand at, plus what each of them could stand at: a side
    // whose longest line is shorter has a smaller end of its own, which is the
    // case the guard is built for.
    const scrollState = (target) => target.evaluate(() => {
      const outer = document.querySelector(".cm-mergeView");
      const sides = [...outer.querySelectorAll(".cm-scroller")];
      return {
        left: sides.map((el) => Math.round(el.scrollLeft)),
        maxLeft: sides.map((el) => Math.round(el.scrollWidth - el.clientWidth)),
        sideTop: sides.map((el) => Math.round(el.scrollTop)),
        sideScrollable: sides.map((el) => el.scrollHeight > el.clientHeight),
        outerTop: Math.round(outer.scrollTop),
        outerMax: outer.scrollHeight - outer.clientHeight,
      };
    });
    const scrollSideTo = async (target, index, x) => {
      await target.evaluate(([i, to]) => {
        document.querySelectorAll(".cm-mergeView .cm-scroller")[i].scrollLeft = to;
      }, [index, x]);
      await sleep(500);
    };
    // A wheel over one pane, near the top edge so the point is really on
    // screen: the panes are as tall as their document, so their own middle can
    // sit far below the visible box.
    const wheelOverSide = async (target, index, dx, dy) => {
      const at = await target.evaluate((i) => {
        const outer = document.querySelector(".cm-mergeView");
        const box = outer.querySelectorAll(".cm-scroller")[i].getBoundingClientRect();
        const frame = outer.getBoundingClientRect();
        return { x: Math.round(box.left + box.width / 2), y: Math.round(frame.top + 60) };
      }, index);
      await target.mouse.move(at.x, at.y);
      await target.mouse.wheel(dx, dy);
      await sleep(500);
    };

    await run("the two sides of a git diff scroll together sideways, the vertical axis stays the outer scroller's", async () => {
      const wide = "wide.txt";
      await openEditor(page);
      await writeHere(wide, wideDoc(40, "a", 40));
      assert(await runInShell(`git add ${wide} && git ${author} commit -qm wide\r`) === 200, "the shell refused the commit");
      // The shell answers before git has run, and waitForFunction cannot await
      // a fetch (see the README note), so the poll runs from here.
      const committed = async () => {
        const deadline = Date.now() + 30000;
        while (Date.now() < deadline) {
          const answer = await page.evaluate(([b, p]) =>
            fetch(`${b}/git/file?path=${encodeURIComponent(p)}`, { headers: { Accept: "application/json" } }).then((r) => r.json()), [editorBase, wide]);
          if (typeof answer.content === "string" && answer.content.includes("a line 1")) return true;
          await sleep(1000);
        }
        return false;
      };
      assert(await committed(), `${wide} never reached the last commit`);
      // One changed line, so this is a real diff and not two identical sides.
      await writeHere(wide, wideDoc(40, "a", 40).replace("a line 12", "a CHANGED 12"));

      await setDiffView(page, "side");
      await setEditorSwitch(page, "diff_collapse", false);
      await setEditorSwitch(page, "line_wrap", false);
      await openEditor(page);
      await page.click(`.editor-item[data-path="${wide}"]`);
      await page.waitForSelector(`.editor-tab[data-path="${wide}"].active`, { timeout: 10000 });
      await diffReady(page);
      await toggleDiff(page);
      await page.waitForSelector(".cm-mergeView", { timeout: 20000 });
      await sleep(800);

      const start = await scrollState(page);
      // Both ends have to lie past the figures below, or a side stopping short
      // would read as the sync failing.
      assert(start.maxLeft.every((max) => max > 900), `the sides have nothing to scroll sideways: ${JSON.stringify(start.maxLeft)}`);
      assert(start.sideScrollable.every((can) => !can), "a side scrolls vertically on its own, the outer view is supposed to");
      assert(start.outerMax > 0, "the comparison fits the box, so the vertical axis proves nothing");

      // A real wheel over the left pane, the way a mouse drives it.
      await wheelOverSide(page, 0, 260, 0);
      const wheeled = await scrollState(page);
      assert(wheeled.left[0] > 0, `the wheel moved nothing: ${JSON.stringify(wheeled.left)}`);
      assert(wheeled.left[1] === wheeled.left[0], `the right side stayed behind: ${JSON.stringify(wheeled.left)}`);

      // And the other way round.
      await scrollSideTo(page, 1, 700);
      const back = await scrollState(page);
      assert(back.left[0] === 700 && back.left[1] === 700, `scrolling the right side left the left one at ${JSON.stringify(back.left)}`);
      await scrollSideTo(page, 0, 0);
      const home = await scrollState(page);
      assert(home.left.join() === "0,0", `back at the start: ${JSON.stringify(home.left)}`);
      // Nothing keeps moving afterwards: an echo answered as a scroll of its
      // own is what would make the two sides push each other.
      await sleep(600);
      assert((await scrollState(page)).left.join() === "0,0", "the sides kept moving after the last scroll");

      // The vertical axis is untouched by all of it: the wheel scrolls the
      // outer view and neither side moves on its own.
      await wheelOverSide(page, 1, 0, 300);
      const down = await scrollState(page);
      assert(down.outerTop > 0, `the vertical wheel moved nothing: ${JSON.stringify(down)}`);
      assert(down.sideTop.join() === "0,0", `a side took the vertical scroll: ${JSON.stringify(down.sideTop)}`);

      await toggleDiff(page);
      await page.waitForSelector(".cm-mergeView", { state: "detached", timeout: 10000 });
      return `both sides at ${back.left[1]}px, the outer view at ${down.outerTop}px`;
    });

    await run("a comparison of two files scrolls together and never pulls the wider side back", async () => {
      const leftWide = "compare-wide-left.txt";
      const rightNarrow = "compare-wide-right.txt";
      await openEditor(page);
      await writeHere(leftWide, wideDoc(30, "l", 40));
      await writeHere(rightNarrow, wideDoc(30, "r", 12));
      await page.click("[data-editor-refresh]");
      await page.waitForSelector(`.editor-item[data-path="${rightNarrow}"]`, { timeout: 15000 });
      await page.click(`.editor-item[data-path="${leftWide}"]`);
      await page.waitForSelector(`.editor-tab[data-path="${leftWide}"]`, { timeout: 10000 });
      await pick(`.editor-tab[data-path="${leftWide}"]`, "Select for compare");
      await pick(`.editor-item[data-path="${rightNarrow}"]`, "^Compare with");
      await page.waitForSelector("[data-editor-compare]:not([hidden])", { timeout: 20000 });
      await page.waitForSelector(".cm-mergeView", { timeout: 20000 });
      await sleep(800);

      const start = await scrollState(page);
      assert(start.maxLeft[0] > start.maxLeft[1] + 200,
        `the two files are not far enough apart in width: ${JSON.stringify(start.maxLeft)}`);

      // Inside what both sides can reach, so this says they follow each other
      // and nothing about the clamp yet. A fixed pixel figure would be the
      // font's, not the app's.
      const shared = Math.round(start.maxLeft[1] / 2);
      assert(shared > 40, `the narrow side has nothing to scroll: ${JSON.stringify(start.maxLeft)}`);
      await scrollSideTo(page, 0, shared);
      const together = await scrollState(page);
      assert(together.left.join() === `${shared},${shared}`, `the sides parted: ${JSON.stringify(together.left)}`);

      // Past the narrow side's own end: it stops where its longest line ends,
      // and the wide one stays where it was put. Answering that clamp would
      // pull the wide side back and make everything past this point
      // unreachable.
      await scrollSideTo(page, 0, start.maxLeft[0]);
      const far = await scrollState(page);
      assert(Math.abs(far.left[0] - start.maxLeft[0]) <= 1, `the wide side was pulled back to ${far.left[0]}`);
      assert(Math.abs(far.left[1] - start.maxLeft[1]) <= 1, `the narrow side did not follow to its own end: ${JSON.stringify(far.left)}`);
      await sleep(600);
      const settled = await scrollState(page);
      assert(settled.left.join() === far.left.join(), `the two sides kept pushing each other: ${JSON.stringify(settled.left)}`);

      // And it finds its way back together.
      const back = Math.round(shared / 2);
      await scrollSideTo(page, 1, back);
      const home = await scrollState(page);
      assert(home.left.join() === `${back},${back}`, `coming back left them apart: ${JSON.stringify(home.left)}`);

      await page.click(".editor-tab.active .editor-tab-state");
      await page.waitForSelector("[data-editor-compare]", { state: "hidden", timeout: 8000 });
      return `wide side to ${far.left[0]}px, narrow side resting at its own end ${far.left[1]}px`;
    });

    // The sync hangs on the scroll event, which every pointer that moves a
    // scroller raises, so this is the same code path as the wheel above. What
    // it proves is that a finger really reaches the scroller: in a comparison
    // the editor's swipe zone is off, so the browser owns the pan, and that is
    // the arrangement the phone depends on.
    await run("mobile: a finger panning one side takes the other with it (chromium)", async () => {
      if (engine !== "chromium") return "skipped, CDP is chromium only";
      const mp = await mobilePage();
      const cdp = await mp.context().newCDPSession(mp);
      await openEditor(mp);
      await diffReady(mp);
      // The file goes up first: opening one closes the drawer, and the drawer's
      // backdrop would stand in front of the menu the settings sheet hangs in.
      if (!(await mp.$(".editor.editor-drawer-open"))) {
        await mp.click("[data-editor-drawer-toggle]");
        await mp.waitForSelector(".editor.editor-drawer-open", { timeout: 6000 });
      }
      await mp.waitForSelector('.editor-item[data-path="wide.txt"]', { timeout: 15000 });
      await mp.click('.editor-item[data-path="wide.txt"]');
      await mp.waitForSelector('.editor-tab[data-path="wide.txt"].active', { state: "attached", timeout: 10000 });
      await mp.waitForSelector(".editor.editor-drawer-open", { state: "detached", timeout: 6000 });
      // Every setting is per device, so the phone picks its own.
      await setDiffView(mp, "side");
      await setEditorSwitch(mp, "diff_collapse", false);
      await setEditorSwitch(mp, "line_wrap", false);
      if (!(await diffPressed(mp))) await toggleDiff(mp);
      await mp.waitForSelector(".cm-mergeView", { timeout: 20000 });
      await sleep(800);

      const at = await mp.evaluate(() => {
        const outer = document.querySelector(".cm-mergeView");
        const box = outer.querySelectorAll(".cm-scroller")[0].getBoundingClientRect();
        const frame = outer.getBoundingClientRect();
        return { x: Math.round(box.left + box.width / 2), y: Math.round(frame.top + 40) };
      });
      await cdp.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ x: at.x, y: at.y, id: 7 }] });
      for (let i = 1; i <= 12; i += 1) {
        await cdp.send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ x: at.x - i * 10, y: at.y, id: 7 }] });
        await sleep(20);
      }
      await cdp.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
      await sleep(900);

      const panned = await scrollState(mp);
      assert(panned.left[0] > 0, `the finger moved nothing: ${JSON.stringify(panned)}`);
      assert(panned.left[1] === panned.left[0], `the other side stayed behind: ${JSON.stringify(panned.left)}`);
      await toggleDiff(mp);
      await mp.waitForSelector(".cm-mergeView", { state: "detached", timeout: 10000 });
      await setEditorSwitch(mp, "diff_collapse", true);
      await setDiffView(mp, "auto");
      return `a finger moved both sides to ${panned.left[0]}px`;
    });

    // Back to what the checks below expect: the folding is on by default and
    // the view follows the width.
    await setEditorSwitch(page, "diff_collapse", true);
    await setDiffView(page, "auto");

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
    // wait for. The editor menu carries git only as the one Git entry.
    await run("the diff toggles from the file's menu and the editor menu carries git only as the sheet entry, on both widths", async () => {
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
        assert(!labels.some((l) => /git diff|blame/i.test(l)), `${where}: the editor menu still carries per-file git: ${labels.join(", ")}`);
        assert(labels.some((l) => /^Git\d*$/.test(l)), `${where}: the menu misses the Git entry: ${labels.join(", ")}`);
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
      return "one switch in the file's menu, no sheet, the editor menu keeps only the Git entry";
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
