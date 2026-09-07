const L = require("./lib");
const { assert, sleep, BASE } = L;

// Editor layout per project: what a project's editor looks like is kept per
// project in localStorage and comes back the way it was left, after a switch
// through the project palette and after a reload. The pieces: the tree column's
// width (dc-editor-tree-width:<project>) and fold (dc-editor-tree-folded:<project>),
// the tree's scroll position (dc-editor-tree-scroll:<project>), which view the
// tree column shows (dc-editor-view:<project>, commit or compare), the terminal
// panel's height (dc-editor-term-height:<project>, open state and active tab were
// per project before), the filter and the scroll of the commit and compare
// lists while their view stands (dc-editor-commit-list:<project>,
// dc-editor-revdiff-list:<project>, dropped when the view closes), and on
// every tab the cursor and the scroll position
// (`view` on the entry in dc-editor-tabs:<project>, as line and column).
// Width, fold and height also write the bare key, which is what a project this
// device never opened starts with: the second project here inherits the first
// one's width and stands unfolded like it, while its own fold afterwards stays
// its own and becomes the start of a third project.
// Gotchas: the commit view needs a repository, built through a shell (the app
// never writes git); a folded tree hides the palette button, so the way back
// goes over Ctrl+Shift+P; the tree's scroll is put back by a ResizeObserver
// when its box appears again, which is what closing the commit view proves;
// the cursors are set last so no later click moves them. The diff checks scroll
// the outer .cm-mergeView for the vertical axis and one side for the sideways
// one, the sync carries the other side; the stored entry is read back before
// the switch, so a miss tells the capture from the restore.

L.runFeature("EDITOR-LAYOUT", async ({ engine, page, run }) => {
  const tag = `edl-${engine}-${Date.now().toString(36)}`;
  const a = `zzla-${tag}`;
  const b = `zzlb-${tag}`;
  const c = `zzlc-${tag}`;
  const longFile = "long.txt";
  const noteFile = "note.txt";
  const editorURL = (p) => `${BASE}/projects/${encodeURIComponent(p)}/editor`;
  const tabSel = (path) => `.editor-tab[data-path="${path}"]`;
  let shellUrl = null;
  const saved = {};

  const post = (url, form) => page.evaluate(async ([u, f]) => {
    const token = document.querySelector('meta[name="csrf-token"]').content;
    const res = await fetch(u, {
      method: "POST",
      headers: { "X-CSRF-Token": token, "Content-Type": "application/x-www-form-urlencoded" },
      body: new URLSearchParams(f).toString(),
    });
    return res.status;
  }, [url, form]);
  const gitChanges = () => page.evaluate(async (u) => (await fetch(u)).json(), `/projects/${encodeURIComponent(a)}/editor/git/changes`);

  const settle = async () => {
    await page.waitForSelector(".cm-editor", { state: "attached", timeout: 15000 });
    await sleep(1200);
  };
  const openEditor = async (p) => {
    await page.goto(editorURL(p), { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    await settle();
  };
  const landed = (p) => page.waitForFunction((n) => decodeURIComponent(location.pathname) === `/projects/${n}/editor`, p, { timeout: 10000 });
  const switchByButton = async (p) => {
    await page.click("[data-editor-project-switch]");
    await page.waitForSelector("[data-editor-palette]", { state: "visible", timeout: 4000 });
    await page.keyboard.type(p);
    await sleep(300);
    await page.keyboard.press("Enter");
    await landed(p);
    await settle();
  };
  const switchByKey = async (p) => {
    await page.keyboard.press("Control+Shift+P");
    await page.waitForSelector("[data-editor-palette]", { state: "visible", timeout: 4000 });
    await page.keyboard.type(p);
    await sleep(300);
    await page.keyboard.press("Enter");
    await landed(p);
    await settle();
  };
  const layout = () => page.evaluate(() => {
    const box = (sel) => {
      const el = document.querySelector(sel);
      if (!el) return null;
      const r = el.getBoundingClientRect();
      return { w: Math.round(r.width), h: Math.round(r.height), display: getComputedStyle(el).display };
    };
    const tree = document.querySelector("[data-editor-tree]");
    const panel = document.querySelector("[data-editor-term-panel]");
    const scroller = document.querySelector("[data-editor-surface] .cm-scroller");
    return {
      col: box(".editor-tree-col"),
      widthVar: document.querySelector(".editor-body").style.getPropertyValue("--editor-tree-width").trim(),
      folded: document.querySelector(".editor").classList.contains("editor-tree-folded"),
      treeScroll: Math.round(tree.scrollTop),
      treeHidden: tree.hidden,
      commitOn: !document.querySelector("[data-editor-commit]").hidden,
      termOpen: !panel.hidden && panel.getBoundingClientRect().height > 0,
      termHeight: Math.round(panel.getBoundingClientRect().height),
      tabs: [...document.querySelectorAll("[data-editor-tabs] .editor-tab")].map((el) => el.dataset.path),
      activeTab: document.querySelector("[data-editor-tabs] .editor-tab.active")?.dataset.path || "",
      pos: document.querySelector("[data-editor-pos]").textContent.trim(),
      cmScroll: scroller ? Math.round(scroller.scrollTop) : -1,
    };
  });
  const near = (x, y, d = 4) => Math.abs(x - y) <= d;

  try {
    await L.createProject(page, a);
    await L.createProject(page, b);
    await L.createProject(page, c);
    await openEditor(a);
    const files = {};
    for (let i = 0; i < 60; i++) files[`f${String(i).padStart(2, "0")}.txt`] = `filler ${i}\n`;
    files[longFile] = Array.from({ length: 300 }, (_, i) => `line ${i + 1}`).join("\n") + "\n";
    files[noteFile] = Array.from({ length: 20 }, (_, i) => `note ${i + 1}`).join("\n") + "\n";
    for (const [path, content] of Object.entries(files)) {
      assert(await post(`/projects/${encodeURIComponent(a)}/editor/file`, { path, content }) === 200, `write ${path} failed`);
    }

    shellUrl = await L.createShell(page, a);
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
    {
      const deadline = Date.now() + 45000;
      let changes = null;
      while (Date.now() < deadline) {
        changes = await gitChanges();
        if (changes.repo && changes.worktree.length === 0) break;
        await sleep(1000);
      }
      assert(changes && changes.repo, "the first project never became a repository");
    }

    await run("one project takes a layout: width, tree scroll, terminal panel with height, commit view, cursors", async () => {
      await openEditor(a);
      const before = await layout();
      assert(before.col && before.col.w > 0 && !before.folded, `no tree column to lay out: ${JSON.stringify(before.col)}`);

      const handle = await page.locator("[data-editor-splitter]").boundingBox();
      await page.mouse.move(handle.x + 1, handle.y + handle.height / 2);
      await page.mouse.down();
      await page.mouse.move(handle.x + 121, handle.y + handle.height / 2);
      await sleep(100);
      await page.mouse.up();
      await sleep(400);
      const widened = await layout();
      assert(widened.col.w > before.col.w + 80, `the splitter did not widen the column: ${before.col.w} -> ${widened.col.w}`);
      saved.width = widened.col.w;

      await page.click(`.editor-file[data-path="${longFile}"]`);
      await page.waitForSelector(`${tabSel(longFile)}.active`, { timeout: 8000 });
      await page.click(`.editor-file[data-path="${noteFile}"]`);
      await page.waitForSelector(`${tabSel(noteFile)}.active`, { timeout: 8000 });
      await sleep(300);

      await page.evaluate(() => { document.querySelector("[data-editor-tree]").scrollTop = 300; });
      await sleep(600);
      const scrolled = await layout();
      assert(scrolled.treeScroll > 100, `the tree did not scroll: ${scrolled.treeScroll}`);
      saved.treeScroll = scrolled.treeScroll;

      await page.click("[data-editor-surface] .cm-scroller");
      await page.keyboard.press("Control+j");
      await page.waitForSelector("[data-editor-term-panel] [data-term-tab]", { timeout: 15000 });
      await page.waitForSelector("[data-editor-term-panel] .editor-term-pane.active terminal-attach[embedded] .xterm-screen canvas", { timeout: 15000 });
      await sleep(500);
      const opened = await layout();
      assert(opened.termOpen, "Ctrl+J did not open the terminal panel");
      const splitter = await page.locator("[data-editor-term-splitter]").boundingBox();
      await page.mouse.move(splitter.x + splitter.width / 2, splitter.y + splitter.height / 2);
      await page.mouse.down();
      await page.mouse.move(splitter.x + splitter.width / 2, splitter.y - 120, { steps: 8 });
      await page.mouse.up();
      await sleep(400);
      const grown = await layout();
      assert(grown.termHeight > opened.termHeight + 80, `the panel did not grow: ${opened.termHeight} -> ${grown.termHeight}`);
      saved.termHeight = grown.termHeight;

      await page.click(tabSel(longFile));
      await page.waitForSelector(`${tabSel(longFile)}.active`, { timeout: 8000 });
      await page.click("[data-editor-surface] .cm-scroller");
      await page.keyboard.press("Control+End");
      for (let i = 0; i < 7; i++) await page.keyboard.press("ArrowUp");
      await page.keyboard.press("End");
      await sleep(200);
      await page.evaluate(() => { document.querySelector("[data-editor-surface] .cm-scroller").scrollBy(0, -300); });
      await sleep(600);
      const long = await layout();
      assert(/^29\d:9$/.test(long.pos), `the cursor is not where the keys put it: ${long.pos}`);
      assert(long.cmScroll > 0, `the long file did not scroll: ${long.cmScroll}`);
      saved.longPos = long.pos;
      saved.longScroll = long.cmScroll;

      await page.click(tabSel(noteFile));
      await page.waitForSelector(`${tabSel(noteFile)}.active`, { timeout: 8000 });
      await page.click("[data-editor-surface] .cm-scroller");
      await page.keyboard.press("Control+Home");
      for (let i = 0; i < 4; i++) await page.keyboard.press("ArrowDown");
      for (let i = 0; i < 3; i++) await page.keyboard.press("ArrowRight");
      await sleep(600);
      const note = await layout();
      assert(note.pos === "5:4", `the note's cursor is not 5:4: ${note.pos}`);

      for (let i = 0; i < 60; i++) {
        assert(await post(`/projects/${encodeURIComponent(a)}/editor/file`, { path: `f${String(i).padStart(2, "0")}.txt`, content: `changed ${i}\n` }) === 200, `changing f${i} failed`);
      }
      await page.click("[data-editor-refresh]");
      await page.click("[data-editor-commit-toggle]");
      await sleep(600);
      const committing = await layout();
      assert(committing.commitOn && committing.treeHidden, "the commit view did not take the tree's place");
      await page.waitForFunction(() => document.querySelectorAll("[data-editor-commit-list] .editor-commit-row").length >= 60, null, { timeout: 15000 });
      await page.fill("[data-editor-commit-filter]", "f");
      await page.waitForFunction(() => document.querySelector("[data-editor-commit-filter-count]").textContent.trim() === "60 of 60", null, { timeout: 8000 });
      await page.evaluate(() => { document.querySelector("[data-editor-commit-list]").scrollTop = 200; });
      await sleep(800);
      const listState = await page.evaluate((p) => JSON.parse(localStorage.getItem(`dc-editor-commit-list:${p}`) || "null"), a);
      assert(listState && listState.filter === "f" && listState.scroll === 200, `the commit list's filter and scroll are not stored: ${JSON.stringify(listState)}`);
      const stored = await page.evaluate((p) => ({
        width: localStorage.getItem(`dc-editor-tree-width:${p}`),
        device: localStorage.getItem("dc-editor-tree-width"),
        scroll: localStorage.getItem(`dc-editor-tree-scroll:${p}`),
        view: localStorage.getItem(`dc-editor-view:${p}`),
        height: localStorage.getItem(`dc-editor-term-height:${p}`),
        tabs: JSON.parse(localStorage.getItem(`dc-editor-tabs:${p}`) || "null"),
      }), a);
      assert(near(parseInt(stored.width, 10), saved.width) && stored.device === stored.width, `width not stored per project plus device: ${JSON.stringify(stored)}`);
      assert(parseInt(stored.scroll, 10) === saved.treeScroll, `tree scroll not stored: ${stored.scroll}`);
      assert(stored.view === "commit", `view not stored: ${stored.view}`);
      assert(near(parseInt(stored.height, 10), saved.termHeight), `panel height not stored: ${stored.height}`);
      const longEntry = (stored.tabs.open || []).find((e) => e && e.path === longFile);
      assert(longEntry && longEntry.view && longEntry.view.head.line === parseInt(saved.longPos, 10) && longEntry.view.scrollTop === saved.longScroll,
        `the long file's view is not on its tab entry: ${JSON.stringify(longEntry)}`);
      return `width ${saved.width}, tree at ${saved.treeScroll}, panel ${saved.termHeight}, ${saved.longPos} scrolled ${saved.longScroll}`;
    });

    await run("a second project inherits the width and the fold and nothing else", async () => {
      await switchByButton(b);
      const other = await layout();
      assert(near(other.col.w, saved.width), `the width did not carry over as the device's last: ${other.col.w} vs ${saved.width}`);
      assert(!other.folded, "the second project opened folded");
      assert(!other.commitOn && !other.treeHidden, "the commit view followed into a project without a repository");
      assert(!other.termOpen, "the terminal panel opened in a project that never had it");
      assert(other.tabs.length === 0, `tabs followed: ${other.tabs.join(", ")}`);
      assert(other.treeScroll === 0, `the tree scroll followed: ${other.treeScroll}`);
      await page.click("[data-editor-drawer-toggle]");
      await sleep(500);
      const folded = await layout();
      assert(folded.folded && folded.col.display === "none", "the fold did not take");
      return `width ${other.col.w}, then folded`;
    });

    await run("the first project comes back exactly as it was left", async () => {
      await switchByKey(a);
      const back = await layout();
      assert(!back.folded, "the second project's fold leaked into the first");
      assert(near(back.col.w, saved.width), `width: ${back.col.w} vs ${saved.width}`);
      assert(back.commitOn && back.treeHidden, "the commit view did not come back");
      await page.waitForFunction(() => document.querySelectorAll("[data-editor-commit-list] .editor-commit-row").length >= 60, null, { timeout: 15000 });
      await sleep(400);
      const list = await page.evaluate(() => ({
        filter: document.querySelector("[data-editor-commit-filter]").value,
        count: document.querySelector("[data-editor-commit-filter-count]").textContent.trim(),
        scroll: Math.round(document.querySelector("[data-editor-commit-list]").scrollTop),
      }));
      assert(list.filter === "f" && list.count === "60 of 60", `the commit list's filter did not come back: ${JSON.stringify(list)}`);
      assert(near(list.scroll, 200), `the commit list's scroll did not come back: ${JSON.stringify(list)}`);
      assert(back.termOpen, "the terminal panel did not come back open");
      assert(near(back.termHeight, saved.termHeight, 8), `panel height: ${back.termHeight} vs ${saved.termHeight}`);
      assert(back.activeTab === noteFile, `active tab: ${back.activeTab}`);
      assert(back.pos === "5:4", `the note's cursor: ${back.pos}`);
      await page.click(tabSel(longFile));
      await page.waitForSelector(`${tabSel(longFile)}.active`, { timeout: 8000 });
      await sleep(500);
      const long = await layout();
      assert(long.pos === saved.longPos, `the long file's cursor: ${long.pos} vs ${saved.longPos}`);
      assert(near(long.cmScroll, saved.longScroll), `the long file's scroll: ${long.cmScroll} vs ${saved.longScroll}`);
      await page.click("[data-editor-commit-close]");
      await sleep(500);
      const tree = await layout();
      assert(!tree.commitOn && !tree.treeHidden, "the commit view did not close");
      assert(tree.treeScroll === saved.treeScroll, `the tree scroll after the commit view: ${tree.treeScroll} vs ${saved.treeScroll}`);
      assert(await page.evaluate((p) => localStorage.getItem(`dc-editor-commit-list:${p}`), a) === null, "closing the commit view kept its list state");
      return `panel ${long.termHeight}, ${long.pos} scrolled ${long.cmScroll}, tree at ${tree.treeScroll}`;
    });

    await run("a reload brings the same layout back", async () => {
      await page.reload({ waitUntil: "domcontentloaded" });
      await settle();
      await sleep(800);
      const again = await layout();
      assert(near(again.col.w, saved.width), `width: ${again.col.w} vs ${saved.width}`);
      assert(!again.commitOn && !again.treeHidden, "the closed commit view came back open");
      assert(again.treeScroll === saved.treeScroll, `tree scroll: ${again.treeScroll} vs ${saved.treeScroll}`);
      assert(again.termOpen && near(again.termHeight, saved.termHeight, 8), `panel: open ${again.termOpen} at ${again.termHeight} vs ${saved.termHeight}`);
      assert(again.activeTab === longFile, `active tab: ${again.activeTab}`);
      assert(again.pos === saved.longPos, `cursor: ${again.pos} vs ${saved.longPos}`);
      assert(near(again.cmScroll, saved.longScroll), `scroll: ${again.cmScroll} vs ${saved.longScroll}`);
      return `${again.pos} scrolled ${again.cmScroll}, tree at ${again.treeScroll}, panel ${again.termHeight}`;
    });

    await run("a third project starts with the device's last fold, the second keeps its own", async () => {
      await switchByKey(c);
      const fresh = await layout();
      assert(fresh.folded, "the third project did not start folded like the last fold set on this device");
      assert(fresh.widthVar === `${saved.width}px`, `the third project's width: ${fresh.widthVar} vs ${saved.width}px`);
      await page.click("[data-editor-drawer-toggle]");
      await sleep(400);
      await switchByButton(b);
      const second = await layout();
      assert(second.folded, "the second project lost its fold");
      await switchByKey(c);
      const third = await layout();
      assert(!third.folded, "the third project lost its own unfold");
      return "third folded then unfolded, second still folded";
    });

    const mergeScroll = () => page.evaluate(() => {
      const outer = document.querySelector(".cm-mergeView");
      if (!outer) return null;
      const sides = [...outer.querySelectorAll(".cm-scroller")];
      return { top: Math.round(outer.scrollTop), left: sides.map((el) => Math.round(el.scrollLeft)) };
    });
    const setMergeScroll = (top, left) => page.evaluate(([t, l]) => {
      const outer = document.querySelector(".cm-mergeView");
      outer.scrollTop = t;
      outer.querySelectorAll(".cm-scroller")[1].scrollLeft = l;
    }, [top, left]);
    const plainScroll = () => page.evaluate(() => {
      const el = document.querySelector("[data-editor-surface] .cm-scroller");
      return { top: Math.round(el.scrollTop), left: Math.round(el.scrollLeft) };
    });
    const storedEntry = (path) => page.evaluate(([p, f]) => {
      const tabs = JSON.parse(localStorage.getItem(`dc-editor-tabs:${p}`) || "null");
      return (tabs && tabs.open || []).find((e) => e && (e.path === f || e.right === f)) || null;
    }, [a, path]);
    const waitMerge = async () => {
      await page.waitForSelector(".cm-mergeView", { timeout: 20000 });
      await sleep(900);
    };
    const pick = async (row, label) => {
      await page.click(row, { button: "right" });
      await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
      await page.locator(".dc-context-menu .dropdown-item", { hasText: new RegExp(label) }).first().click();
      await sleep(500);
    };
    const wideLines = (changed) => Array.from({ length: 300 }, (_, i) => `${i + 1 === changed ? "changed" : "line"} ${i + 1} ${"x".repeat(400)}`).join("\n") + "\n";
    const wideLong = wideLines(5);
    const twinFile = "long2.txt";
    const setDiffSettings = (patch) => page.evaluate((p) => {
      const s = JSON.parse(localStorage.getItem("dc-editor-settings") || "{}");
      localStorage.setItem("dc-editor-settings", JSON.stringify({ ...s, ...p }));
    }, patch);

    const blocksB = () => page.evaluate(() => [...document.querySelectorAll(".cm-merge-b .cm-collapsedLines")].map((el) => el.textContent.trim()));
    const outerHeight = () => page.evaluate(() => document.querySelector(".cm-mergeView").scrollHeight);
    const expandFirst = async () => {
      await page.click(".cm-merge-b .cm-collapsedLines");
      await sleep(800);
    };
    const reopenWithDiff = async (content) => {
      assert(await post(`/projects/${encodeURIComponent(a)}/editor/file`, { path: longFile, content }) === 200, "the change could not be written");
      await page.click(tabSel(longFile));
      await page.waitForSelector(`${tabSel(longFile)}.active`, { timeout: 8000 });
      await page.keyboard.press("Control+Shift+x");
      await page.waitForSelector(tabSel(longFile), { state: "detached", timeout: 8000 });
      await page.click(`.editor-file[data-path="${longFile}"]`);
      await page.waitForSelector(`${tabSel(longFile)}.active`, { timeout: 8000 });
      await sleep(400);
      await pick(".editor-tab.active", "git diff");
      await waitMerge();
    };
    const oneChange = Array.from({ length: 300 }, (_, i) => (i === 4 ? `changed 5 ${"x".repeat(400)}` : `line ${i + 1}`)).join("\n") + "\n";

    await run("a collapsed diff's opened block comes back with the tab", async () => {
      await switchByKey(a);
      await reopenWithDiff(oneChange);
      const before = await blocksB();
      assert(before.length === 1 && /^293 unchanged/.test(before[0]), `one block of 293 lines is expected after a change in line 5: ${JSON.stringify(before)}`);
      const folded = await outerHeight();
      await expandFirst();
      const opened = await outerHeight();
      assert((await blocksB()).length === 0 && opened > folded + 3000, `the click did not open the block: ${JSON.stringify(await blocksB())}, ${folded} -> ${opened}`);
      const entry = await storedEntry(longFile);
      assert(entry && entry.view && JSON.stringify(entry.view.expanded) === "[9]", `the opened block is not on the tab entry: ${JSON.stringify(entry)}`);

      await switchByKey(b);
      await switchByKey(a);
      await waitMerge();
      assert((await blocksB()).length === 0 && (await outerHeight()) > folded + 3000, `the block folded again after the switch: ${JSON.stringify(await blocksB())}, ${await outerHeight()}`);

      await page.reload({ waitUntil: "domcontentloaded" });
      await settle();
      await waitMerge();
      assert((await blocksB()).length === 0 && (await outerHeight()) > folded + 3000, `the block folded again after the reload: ${JSON.stringify(await blocksB())}, ${await outerHeight()}`);
      return `block at line 9 opened, ${folded} -> ${opened}, still open after the switch and the reload`;
    });

    await run("a side by side diff keeps its outer scroll and its sideways offset across the switch and a reload", async () => {
      await setDiffSettings({ diff_collapse: false });
      await page.reload({ waitUntil: "domcontentloaded" });
      await settle();
      await reopenWithDiff(wideLong);
      await setMergeScroll(600, 300);
      await sleep(800);
      const set = await mergeScroll();
      assert(set.top === 600 && set.left.join() === "0,300", `the diff did not scroll (HEAD's side has no wide line and stays at 0): ${JSON.stringify(set)}`);
      const entry = await storedEntry(longFile);
      assert(entry && entry.view && entry.view.scrollTop === 600 && entry.view.scrollLeft === 300, `the diff's scroll is not on the tab entry: ${JSON.stringify(entry)}`);

      await switchByKey(b);
      await switchByKey(a);
      await waitMerge();
      const back = await mergeScroll();
      assert(near(back.top, 600, 8) && back.left.join() === "0,300", `after the switch: ${JSON.stringify(back)}`);

      await page.reload({ waitUntil: "domcontentloaded" });
      await settle();
      await waitMerge();
      const again = await mergeScroll();
      assert(near(again.top, 600, 8) && again.left.join() === "0,300", `after the reload: ${JSON.stringify(again)}`);
      return `outer ${again.top}, sides ${again.left.join("/")}`;
    });

    await run("an inline diff keeps both axes the same way", async () => {
      await setDiffSettings({ diff_view: "inline" });
      await page.reload({ waitUntil: "domcontentloaded" });
      await settle();
      await page.waitForSelector(".cm-deletedChunk, .cm-changedLine", { state: "attached", timeout: 20000 });
      await sleep(600);
      assert(!(await page.$(".cm-mergeView")), "the inline setting still built a side by side view");
      await page.evaluate(() => {
        const el = document.querySelector("[data-editor-surface] .cm-scroller");
        el.scrollTop = 500;
        el.scrollLeft = 200;
      });
      await sleep(800);
      const set = await plainScroll();
      assert(set.top > 400 && set.left === 200, `the inline diff did not scroll: ${JSON.stringify(set)}`);

      await switchByKey(b);
      await switchByKey(a);
      await page.waitForSelector(".cm-deletedChunk, .cm-changedLine", { state: "attached", timeout: 20000 });
      await sleep(900);
      const back = await plainScroll();
      assert(near(back.top, set.top, 8) && back.left === 200, `after the switch: ${JSON.stringify(back)} vs ${JSON.stringify(set)}`);

      await page.reload({ waitUntil: "domcontentloaded" });
      await settle();
      await page.waitForSelector(".cm-deletedChunk, .cm-changedLine", { state: "attached", timeout: 20000 });
      await sleep(900);
      const again = await plainScroll();
      assert(near(again.top, set.top, 8) && again.left === 200, `after the reload: ${JSON.stringify(again)} vs ${JSON.stringify(set)}`);
      await setDiffSettings({ diff_view: "auto" });
      return `top ${again.top}, left ${again.left}`;
    });

    await run("a comparison of two files keeps both axes and its opened blocks too", async () => {
      await setDiffSettings({ diff_collapse: true });
      assert(await post(`/projects/${encodeURIComponent(a)}/editor/file`, { path: twinFile, content: wideLines(250) }) === 200, "the twin could not be written");
      await page.reload({ waitUntil: "domcontentloaded" });
      await settle();
      await page.waitForSelector(`.editor-file[data-path="${twinFile}"]`, { timeout: 15000 });
      await pick(`.editor-file[data-path="${longFile}"]`, "Select for compare");
      await pick(`.editor-file[data-path="${twinFile}"]`, "^Compare with");
      await waitMerge();
      const active = await page.evaluate(() => document.querySelector("[data-editor-tabs] .editor-tab.active")?.dataset.path || "");
      assert(active.includes(twinFile), `the comparison tab is not active: ${active}`);
      const before = await blocksB();
      assert(before.length === 2 && /^238 unchanged/.test(before[0]) && /^48 unchanged/.test(before[1]), `two blocks are expected on changes in lines 5 and 250: ${JSON.stringify(before)}`);
      const folded = await outerHeight();
      await expandFirst();
      const stillFolded = () => blocksB().then((b) => b.every((t) => !/^238 unchanged/.test(t)));
      assert(await stillFolded() && (await outerHeight()) > folded + 3000, `the click did not open the first block: ${JSON.stringify(await blocksB())}`);
      await setMergeScroll(400, 150);
      await sleep(800);
      const set = await mergeScroll();
      assert(set.top === 400 && set.left.join() === "150,150", `the comparison did not scroll: ${JSON.stringify(set)}`);
      const entry = await page.evaluate((p) => {
        const tabs = JSON.parse(localStorage.getItem(`dc-editor-tabs:${p}`) || "null");
        return (tabs && tabs.open || []).find((e) => e && e.type === "compare") || null;
      }, a);
      assert(entry && entry.scroll && entry.scroll.top === 400 && entry.scroll.left === 150, `the comparison's scroll is not on its entry: ${JSON.stringify(entry)}`);
      assert(JSON.stringify(entry.expanded) === "[9]", `the opened block is not on the comparison's entry: ${JSON.stringify(entry)}`);

      await switchByKey(b);
      await switchByKey(a);
      await waitMerge();
      assert(await stillFolded() && (await outerHeight()) > folded + 3000, `the opened block folded again after the switch: ${JSON.stringify(await blocksB())}`);
      const back = await mergeScroll();
      assert(near(back.top, set.top, 8) && back.left.join() === "150,150", `after the switch: ${JSON.stringify(back)} vs ${JSON.stringify(set)}`);

      await page.reload({ waitUntil: "domcontentloaded" });
      await settle();
      await waitMerge();
      assert(await stillFolded() && (await outerHeight()) > folded + 3000, `the opened block folded again after the reload: ${JSON.stringify(await blocksB())}`);
      const again = await mergeScroll();
      assert(near(again.top, set.top, 8) && again.left.join() === "150,150", `after the reload: ${JSON.stringify(again)} vs ${JSON.stringify(set)}`);
      return `outer ${again.top}, sides ${again.left.join("/")}, one block open`;
    });
  } finally {
    if (shellUrl) await L.deleteShell(page, shellUrl).catch(() => {});
    for (const p of [a, b, c]) await L.deleteProject(page, p).catch(() => {});
  }
});
