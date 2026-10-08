const L = require("./lib");
const { assert, sleep, BASE } = L;

// Editor tab groups: the editor's work surface splits into groups, each with
// its own tab strip, open tabs and active tab, one of them focused. A tab
// dragged out of its strip onto a group's edge opens a new group on that side,
// onto another group's strip or its middle it moves there; the tab menu does
// the same with Split right/left/down/up and Move to group N. The line between
// two groups drags their shares, a group keeps a minimum size. Closing the last
// tab of a group removes it and its room goes to the neighbour. A tab lives in
// one group, moving it keeps its undo history. Save and Ctrl+S act on the
// focused group, Ctrl+Tab and Ctrl+Shift+Tab walk every tab, the groups as
// they stand on the screen: top row first, each row from the left. The whole layout rides in dc-editor-tabs:<project>: open
// holds every tab group after group, groups lists their positions and active
// one, layout the split tree with the shares, focus the focused group; an old
// {open, active} value reads as one group. Making groups is desktop only, the
// same rule as the editor's terminal panel (a fine pointer and room), and the
// width is the view, never the data: a narrow window or a phone shows the
// focused group with every group's tabs in its strip, in screen order, keeps
// the stored layout with its sizes, and its tab menu offers no split.
// Markup: [data-editor-groups] holds .editor-split boxes (data-dir row or
// column) with .editor-split-handle between their children and .editor-group
// leaves, the focused one .editor-group-focused; the drop marker is
// [data-editor-drop-zone]. Module @dc/editor-groups, component dc-editor.
// Gotchas: groups are read in DOM order, which is the layout's reading order;
// every box is measured, never the hidden attribute; autosave is switched off
// for the run so the dirty marks stay until the save under test, posted from
// the page because WebKit reports the settings form's own submit as a page
// error ("Load failed") for a fetch the navigation cut.

L.runFeature("EDITOR-GROUPS", async ({ engine, page, run, mobilePage }) => {
  const tag = `edg-${engine}-${Date.now().toString(36)}`;
  const project = `zzeg-${tag}`;
  const editorURL = `${BASE}/projects/${encodeURIComponent(project)}/editor`;
  const tabsKey = `dc-editor-tabs:${project}`;
  const files = {
    "a.js": "export const a = 1;\n",
    "b.go": "package main\n\nfunc main() {}\n",
    "c.css": ".c { color: red; }\n",
    "d.md": "# d\n\ntext\n",
    "e.txt": "e one\ne two\n",
  };
  const tabSel = (path) => `.editor-tab[data-path="${path}"]`;
  const near = (x, y, d = 3) => Math.abs(x - y) <= d;

  const post = (url, form) => page.evaluate(async ([u, f]) => {
    const token = document.querySelector('meta[name="csrf-token"]').content;
    const res = await fetch(u, {
      method: "POST",
      headers: { "X-CSRF-Token": token, "Content-Type": "application/x-www-form-urlencoded" },
      body: new URLSearchParams(f).toString(),
    });
    return res.status;
  }, [url, form]);
  const readFile = (p, path) => p.evaluate(async (u) => (await (await fetch(u)).json()).content, `/projects/${encodeURIComponent(project)}/editor/file?path=${encodeURIComponent(path)}`);

  const groups = (p = page) => p.evaluate(() => [...document.querySelectorAll(".editor-group")].map((g) => {
    const r = g.getBoundingClientRect();
    const head = g.querySelector(".editor-group-head").getBoundingClientRect();
    return {
      x: Math.round(r.x),
      y: Math.round(r.y),
      w: Math.round(r.width),
      h: Math.round(r.height),
      headBottom: Math.round(head.bottom),
      display: getComputedStyle(g).display,
      focused: g.classList.contains("editor-group-focused"),
      tabs: [...g.querySelectorAll(".editor-tab")].map((t) => t.dataset.path),
      active: g.querySelector(".editor-tab.active")?.dataset.path || "",
    };
  }));
  const groupOf = (list, path) => list.findIndex((g) => g.tabs.includes(path));
  const waitGroups = (n, p = page) => p.waitForFunction((count) => document.querySelectorAll(".editor-group").length === count, n, { timeout: 8000 });
  const openEditor = async (p = page) => {
    await p.goto(editorURL, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(p);
    await p.waitForSelector(".cm-editor", { state: "attached", timeout: 20000 });
    await sleep(600);
  };
  const openFile = async (path) => {
    await page.click(`.editor-file[data-path="${path}"]`);
    await page.waitForSelector(`${tabSel(path)}.active`, { timeout: 8000 });
  };
  // A mouse drag from a tab to a point, with the drop marker read while the
  // button is still down.
  const dragTab = async (path, x, y) => {
    const from = await page.locator(tabSel(path)).boundingBox();
    assert(from, `no tab ${path} to drag`);
    await page.mouse.move(from.x + from.width / 2, from.y + from.height / 2);
    await page.mouse.down();
    await page.mouse.move(from.x + from.width / 2 + 8, from.y + from.height / 2 + 8, { steps: 3 });
    await page.mouse.move(x, y, { steps: 16 });
    await sleep(200);
    const zone = await page.evaluate(() => {
      const el = document.querySelector("[data-editor-drop-zone]");
      const r = el.getBoundingClientRect();
      return { display: getComputedStyle(el).display, x: Math.round(r.x), y: Math.round(r.y), w: Math.round(r.width), h: Math.round(r.height) };
    });
    await page.mouse.up();
    await sleep(700);
    return zone;
  };
  // Grabbed a quarter along its length: where a nested handle meets it, the
  // nested one is on top.
  const dragHandle = async (index, dx, dy) => {
    const box = await page.evaluate((i) => {
      const r = document.querySelectorAll(".editor-split-handle")[i].getBoundingClientRect();
      return r.width > r.height ? { x: r.x + r.width / 4, y: r.y + r.height / 2 } : { x: r.x + r.width / 2, y: r.y + r.height / 4 };
    }, index);
    await page.mouse.move(box.x, box.y);
    await page.mouse.down();
    await page.mouse.move(box.x + dx / 2, box.y + dy / 2, { steps: 5 });
    await page.mouse.move(box.x + dx, box.y + dy, { steps: 5 });
    await page.mouse.up();
    await sleep(400);
  };
  const sameLayout = (x, y) => {
    if (typeof x === "number" || typeof y === "number") return x === y;
    if (!x || !y || x.split !== y.split || x.children.length !== y.children.length) return false;
    return x.sizes.every((v, i) => Math.abs(v - y.sizes[i]) < 0.002) && x.children.every((c, i) => sameLayout(c, y.children[i]));
  };
  const handleDirs = () => page.$$eval(".editor-split-handle", (els) => els.map((el) => el.dataset.editorSplitHandle));
  const menuItem = (label) => page.locator(".dc-context-menu .dropdown-item", { has: page.locator(".dc-menu-label-head", { hasText: new RegExp(`^${label}$`) }) });
  const tabMenu = async (path) => {
    await page.click(tabSel(path), { button: "right" });
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
  };
  const typeInto = async (path, text) => {
    await page.click(tabSel(path));
    const g = await groups();
    const at = g[groupOf(g, path)];
    await page.mouse.click(at.x + at.w / 2, at.headBottom + 40);
    await page.keyboard.press("Control+End");
    await page.keyboard.type(text);
    await page.waitForSelector(`${tabSel(path)}.dirty`, { timeout: 6000 });
  };

  try {
    const settings = await page.evaluate(async () => (await fetch("/settings/editor/files")).text());
    const poll = (/name="file_poll_seconds"[^>]*value="(\d+)"/.exec(settings) || [])[1];
    assert(poll !== undefined && await post("/settings/editor/files", { file_poll_seconds: poll }) === 200, "switching autosave off failed");
    await L.createProject(page, project);
    await openEditor();
    for (const [path, content] of Object.entries(files)) {
      assert(await post(`/projects/${encodeURIComponent(project)}/editor/file`, { path, content }) === 200, `writing ${path} failed`);
    }
    await page.evaluate((k) => localStorage.removeItem(k), tabsKey);
    await openEditor();
    await page.waitForSelector('.editor-file[data-path="e.txt"]', { timeout: 15000 });
    for (const path of ["a.js", "b.go", "c.css", "d.md"]) await openFile(path);

    await run("a tab dragged onto a group's right edge opens a group on the right", async () => {
      const [one] = await groups();
      const zone = await dragTab("d.md", one.x + one.w - 20, one.headBottom + (one.y + one.h - one.headBottom) / 2);
      assert(zone.display !== "none" && near(zone.x, one.x + one.w / 2, 4) && near(zone.w, one.w / 2, 4),
        `the drop marker did not cover the right half while dragging: ${JSON.stringify(zone)}`);
      await waitGroups(2);
      const [left, right] = await groups();
      assert(right.x >= left.x + left.w - 2 && near(left.y, right.y) && near(left.h, right.h), `the groups do not stand side by side: ${JSON.stringify([left, right])}`);
      assert(near(left.w, right.w, 4), `the room was not halved: ${left.w} and ${right.w}`);
      assert(JSON.stringify(right.tabs) === '["d.md"]' && right.active === "d.md" && right.focused, `the right group does not hold the dragged tab focused: ${JSON.stringify(right)}`);
      assert(!left.tabs.includes("d.md") && left.active === "c.css", `the tab stayed in its old group: ${JSON.stringify(left)}`);
      const after = await page.$eval("[data-editor-drop-zone]", (el) => getComputedStyle(el).display);
      assert(after === "none", `the drop marker stayed after the drop: ${after}`);
      return `${left.w}px | ${right.w}px`;
    });

    await run("a tab dragged onto the right group's bottom edge opens a group below it", async () => {
      const [, right] = await groups();
      const zone = await dragTab("c.css", right.x + right.w / 2, right.y + right.h - 20);
      assert(zone.display !== "none" && near(zone.x, right.x, 4) && near(zone.w, right.w, 4),
        `the drop marker did not cover the bottom of the right group: ${JSON.stringify(zone)}`);
      await waitGroups(3);
      const [left, top, bottom] = await groups();
      assert(JSON.stringify(top.tabs) === '["d.md"]' && JSON.stringify(bottom.tabs) === '["c.css"]', `wrong tabs: ${JSON.stringify([top, bottom])}`);
      assert(near(top.x, bottom.x) && near(top.w, bottom.w) && bottom.y >= top.y + top.h - 2, `the new group is not below the right one: ${JSON.stringify([top, bottom])}`);
      assert(near(left.h, top.h + bottom.h, 3) && top.x >= left.x + left.w - 2, `the left group lost its full height: ${JSON.stringify([left, top, bottom])}`);
      assert(JSON.stringify(left.tabs) === '["a.js","b.go"]' && left.active === "b.go", `the left group: ${JSON.stringify(left)}`);
      assert(JSON.stringify(await handleDirs()) === '["row","column"]', `handles: ${JSON.stringify(await handleDirs())}`);
      return `${left.w}px | ${top.h}px over ${bottom.h}px`;
    });

    await run("dragging a handle resizes the groups beside it, down to a minimum", async () => {
      const before = await groups();
      await dragHandle(0, -120, 0);
      const wide = await groups();
      assert(near(wide[0].w, before[0].w - 120, 3), `the left group did not shrink by 120: ${before[0].w} -> ${wide[0].w}`);
      assert(near(wide[1].w, before[1].w + 120, 3) && near(wide[2].w, before[2].w + 120, 3), `the right column did not grow by 120: ${JSON.stringify(wide)}`);
      await dragHandle(1, 0, 90);
      const tall = await groups();
      assert(near(tall[1].h, wide[1].h + 90, 3) && near(tall[2].h, wide[2].h - 90, 3), `the column handle did not move 90: ${JSON.stringify([wide[1].h, wide[2].h, tall[1].h, tall[2].h])}`);
      await dragHandle(0, -2000, 0);
      const squeezed = await groups();
      assert(squeezed[0].w >= 159 && squeezed[0].w < 175, `the left group is not held at its minimum: ${squeezed[0].w}`);
      const handle = await page.evaluate(() => {
        const r = document.querySelector(".editor-split-handle").getBoundingClientRect();
        return { x: r.x + r.width / 2, y: r.y + r.height / 4 };
      });
      await page.mouse.dblclick(handle.x, handle.y);
      await sleep(400);
      const even = await groups();
      assert(near(even[0].w, even[1].w, 3) && near(even[1].h, tall[1].h, 3), `a double click did not even out the row alone: ${JSON.stringify(even)}`);
      return `left ${before[0].w} -> ${wide[0].w}, min ${squeezed[0].w}, even ${even[0].w}; top ${wide[1].h} -> ${tall[1].h}`;
    });

    await run("a tab dragged onto another group's strip moves there with its undo history", async () => {
      await typeInto("b.go", "// typed in the left group\n");
      const target = (await groups())[2];
      await dragTab("b.go", target.x + target.w - 30, target.y + (target.headBottom - target.y) / 2);
      const list = await groups();
      assert(list.length === 3, `the group count changed: ${list.length}`);
      assert(!list[0].tabs.includes("b.go") && JSON.stringify(list[2].tabs) === '["c.css","b.go"]', `b.go did not land at the end of the bottom group: ${JSON.stringify(list)}`);
      assert(list[2].active === "b.go" && list[2].focused && list[0].active === "a.js", `active or focus wrong: ${JSON.stringify(list)}`);
      assert(await page.$eval(tabSel("b.go"), (el) => el.classList.contains("dirty")), "the moved tab lost its unsaved mark");
      await page.mouse.click(list[2].x + list[2].w / 2, list[2].headBottom + 40);
      for (let i = 0; i < 6 && await page.$eval(tabSel("b.go"), (el) => el.classList.contains("dirty")); i++) {
        await page.keyboard.press("Control+z");
        await sleep(150);
      }
      await page.waitForSelector(`${tabSel("b.go")}:not(.dirty)`, { timeout: 6000 });
      return "undo in the new group took the typing back";
    });

    await run("the tab menu moves a tab and splits a group", async () => {
      await tabMenu("b.go");
      const labels = await page.$$eval(".dc-context-menu .dropdown-item", (els) => els.map((el) => (el.disabled ? "-" : "") + el.querySelector(".dc-menu-label-head").textContent));
      for (const want of ["Split right", "Split left", "Split down", "Split up", "Move to group 1", "Move to group 2"]) {
        assert(labels.includes(want), `the menu lacks ${want}: ${labels.join(", ")}`);
      }
      assert(!labels.some((l) => /Move to group 3$/.test(l)), `the menu offers the tab's own group: ${labels.join(", ")}`);
      await menuItem("Move to group 1").click();
      await sleep(500);
      let list = await groups();
      assert(JSON.stringify(list[0].tabs) === '["a.js","b.go"]' && list[0].focused && list[0].active === "b.go", `b.go did not move to group 1: ${JSON.stringify(list)}`);
      await tabMenu("b.go");
      await menuItem("Split down").click();
      await waitGroups(4);
      list = await groups();
      assert(JSON.stringify(list[1].tabs) === '["b.go"]' && list[1].focused, `Split down did not open a group under group 1: ${JSON.stringify(list)}`);
      assert(near(list[0].x, list[1].x) && list[1].y >= list[0].y + list[0].h - 2, `the split group is not below: ${JSON.stringify(list.slice(0, 2))}`);
      // The new group's top sits above the right column's lower one, so the
      // screen puts that one after it, where the tree would go to the upper.
      await page.keyboard.press("Control+Tab");
      await sleep(300);
      let front = (await groups()).find((g) => g.focused);
      assert(front && front.active === "c.css", `Ctrl+Tab did not follow the screen: ${JSON.stringify(front)}`);
      await page.keyboard.press("Control+Shift+Tab");
      await sleep(300);
      front = (await groups()).find((g) => g.focused);
      assert(front && front.active === "b.go", `Ctrl+Shift+Tab did not come back: ${JSON.stringify(front)}`);
      return labels.filter((l) => /Split|Move/.test(l)).join(", ");
    });

    await run("closing a group's last tab removes the group and its neighbour takes the room", async () => {
      const before = await groups();
      await page.click(`${tabSel("b.go")} .editor-tab-state`);
      await waitGroups(3);
      const after = await groups();
      assert(!after.some((g) => g.tabs.includes("b.go")), "b.go is still open");
      assert(near(after[0].h, before[0].h + before[1].h, 3) && near(after[0].y, before[0].y), `the room did not go to the group above: ${before[0].h}+${before[1].h} -> ${after[0].h}`);
      assert(after[0].focused, `the neighbour did not take the focus: ${JSON.stringify(after)}`);
      assert(JSON.stringify(await handleDirs()) === '["row","column"]', `a handle stayed behind: ${JSON.stringify(await handleDirs())}`);
      const gone = await page.evaluate(() => document.querySelectorAll(".editor-group").length);
      assert(gone === 3, `${gone} groups in the page`);
      await openFile("b.go");
      return `${after[0].h}px`;
    });

    await run("save and Ctrl+S act on the focused group, Ctrl+Tab walks every group", async () => {
      await typeInto("d.md", "\nsaved from the top right\n");
      let list = await groups();
      assert(list[1].focused && list[1].active === "d.md", `the clicked group is not focused: ${JSON.stringify(list)}`);
      const saveShown = await page.$eval("[data-editor-save]", (el) => getComputedStyle(el).display !== "none" && el.getBoundingClientRect().width > 0);
      assert(saveShown, "Save does not show for the focused group's unsaved file");
      await page.keyboard.press("Control+s");
      await page.waitForSelector(`${tabSel("d.md")}:not(.dirty)`, { timeout: 8000 });
      assert(/saved from the top right/.test(await readFile(page, "d.md")), "Ctrl+S did not write the focused group's file");
      await page.click(tabSel("a.js"));
      await page.mouse.click(list[0].x + list[0].w / 2, list[0].headBottom + 40);
      const walk = [];
      for (const key of ["Control+Tab", "Control+Tab", "Control+Tab", "Control+Tab", "Control+Shift+Tab"]) {
        await page.keyboard.press(key);
        await sleep(300);
        list = await groups();
        walk.push(list.find((g) => g.focused).active);
      }
      assert(JSON.stringify(walk) === '["b.go","d.md","c.css","a.js","c.css"]', `Ctrl+Tab did not walk the groups in order and wrap: ${JSON.stringify(walk)}`);
      assert(list[0].active === "a.js" && list[1].active === "d.md" && list[2].active === "c.css" && list[2].focused, `the groups' fronts after the walk: ${JSON.stringify(list.map((g) => g.active))}`);
      return list.map((g) => g.active).join(" | ");
    });

    await run("a reload restores the groups, their tabs, sizes and the focus", async () => {
      const before = await groups();
      const stored = await page.evaluate((k) => JSON.parse(localStorage.getItem(k)), tabsKey);
      assert(stored && Array.isArray(stored.groups) && stored.groups.length === 3 && stored.layout && stored.layout.split === "row",
        `the stored layout is not three groups in a row: ${JSON.stringify(stored)}`);
      assert(stored.open.length === 4 && stored.focus === 2, `stored open/focus: ${JSON.stringify(stored)}`);
      await page.reload({ waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      await waitGroups(3);
      await page.waitForSelector(`${tabSel("c.css")}.active`, { timeout: 10000 });
      await sleep(800);
      const after = await groups();
      for (let i = 0; i < 3; i++) {
        const a = before[i];
        const b = after[i];
        assert(JSON.stringify(a.tabs) === JSON.stringify(b.tabs) && a.active === b.active && a.focused === b.focused, `group ${i + 1} came back different: ${JSON.stringify([a, b])}`);
        assert(near(a.x, b.x, 2) && near(a.y, b.y, 2) && near(a.w, b.w, 2) && near(a.h, b.h, 2), `group ${i + 1} changed size: ${JSON.stringify([a, b])}`);
      }
      return after.map((g) => `${g.w}x${g.h}`).join(" | ");
    });

    await run("a load in a narrow window keeps the layout and its sizes", async () => {
      const before = await groups();
      const want = await page.evaluate((k) => JSON.parse(localStorage.getItem(k)), tabsKey);
      for (const size of [{ width: 700, height: 900 }, { width: 1440, height: 480 }]) {
        await page.setViewportSize(size);
        await page.reload({ waitUntil: "domcontentloaded" });
        await L.dismissUpdate(page);
        await page.waitForSelector(`${tabSel("c.css")}.active`, { state: "attached", timeout: 10000 });
        await sleep(800);
        const list = await groups();
        const shown = list.filter((g) => g.display !== "none" && g.w > 0);
        assert(list.length === 3 && shown.length === 1 && shown[0].focused, `${size.width}x${size.height} does not show the focused group alone: ${JSON.stringify(list)}`);
        assert(JSON.stringify(shown[0].tabs) === '["a.js","b.go","d.md","c.css"]' && shown[0].active === "c.css",
          `${size.width}x${size.height}: the strip does not carry every tab in screen order: ${JSON.stringify(shown[0])}`);
        const now = await page.evaluate((k) => JSON.parse(localStorage.getItem(k)), tabsKey);
        assert(JSON.stringify(now.groups) === JSON.stringify(want.groups) && sameLayout(now.layout, want.layout) && now.focus === want.focus,
          `${size.width}x${size.height} rewrote the layout: ${JSON.stringify(now)} vs ${JSON.stringify(want)}`);
      }
      const from = await page.locator(tabSel("d.md")).boundingBox();
      const to = await page.locator(tabSel("c.css")).boundingBox();
      await page.mouse.move(from.x + from.width / 2, from.y + from.height / 2);
      await page.mouse.down();
      await page.mouse.move(to.x + to.width * 0.9, to.y + to.height / 2, { steps: 12 });
      await page.mouse.up();
      await sleep(500);
      const dragged = await page.evaluate((k) => JSON.parse(localStorage.getItem(k)), tabsKey);
      assert((await groups()).length === 3 && JSON.stringify(dragged.groups) === JSON.stringify(want.groups),
        `a drag in the narrow strip moved a tab between groups: ${JSON.stringify(dragged.groups)}`);
      await page.setViewportSize({ width: 1360, height: 900 });
      await page.reload({ waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      await waitGroups(3);
      await page.waitForSelector(`${tabSel("c.css")}.active`, { timeout: 10000 });
      await sleep(800);
      const after = await groups();
      for (let i = 0; i < 3; i++) {
        const a = before[i];
        const b = after[i];
        assert(JSON.stringify(a.tabs) === JSON.stringify(b.tabs) && near(a.w, b.w, 2) && near(a.h, b.h, 2), `group ${i + 1} did not come back: ${JSON.stringify([a, b])}`);
      }
      return after.map((g) => `${g.w}x${g.h}`).join(" | ");
    });

    await run("an old {open, active} value restores as one group", async () => {
      await page.evaluate((k) => localStorage.setItem(k, JSON.stringify({ open: ["a.js", "b.go", { type: "file", path: "c.css" }, "e.txt"], active: "c.css" })), tabsKey);
      await page.reload({ waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      await page.waitForSelector(`${tabSel("c.css")}.active`, { timeout: 10000 });
      await sleep(600);
      const list = await groups();
      assert(list.length === 1, `${list.length} groups`);
      assert(JSON.stringify(list[0].tabs) === '["a.js","b.go","c.css","e.txt"]' && list[0].active === "c.css" && list[0].focused, `the one group: ${JSON.stringify(list[0])}`);
      assert(await page.locator(".editor-split-handle").count() === 0, "a handle stands without a split");
      const stored = await page.evaluate((k) => JSON.parse(localStorage.getItem(k)), tabsKey);
      assert(stored.groups.length === 1 && stored.layout === 0 && stored.open.length === 4, `the value was not rewritten as one group: ${JSON.stringify(stored)}`);
      return list[0].tabs.join(", ");
    });

    await run("at 390px the focused group's strip carries every tab and the layout stays", async () => {
      const mp = await mobilePage();
      await mp.goto(editorURL, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(mp);
      const layout = { split: "row", sizes: [0.4, 0.6], children: [0, { split: "column", sizes: [0.3, 0.7], children: [1, 2] }] };
      await mp.evaluate(([k, l]) => localStorage.setItem(k, JSON.stringify({
        open: ["a.js", "b.go", "c.css", "d.md"],
        active: "c.css",
        groups: [{ tabs: [0, 1], active: 1 }, { tabs: [2], active: 2 }, { tabs: [3], active: 3 }],
        layout: l,
        focus: 1,
      })), [tabsKey, layout]);
      await mp.reload({ waitUntil: "domcontentloaded" });
      await L.dismissUpdate(mp);
      await waitGroups(3, mp);
      await mp.waitForSelector(`${tabSel("c.css")}.active`, { state: "attached", timeout: 10000 });
      await sleep(800);
      if (await mp.$(".editor.editor-drawer-open")) {
        await mp.evaluate(() => document.querySelector("[data-editor-backdrop]").click());
        await mp.waitForFunction(() => !document.querySelector(".editor.editor-drawer-open"), null, { timeout: 6000 });
      }
      const shown = async () => (await groups(mp)).filter((g) => g.display !== "none" && g.w > 0);
      let visible = await shown();
      assert(visible.length === 1 && visible[0].focused && visible[0].active === "c.css", `not the focused group alone: ${JSON.stringify(await groups(mp))}`);
      assert(JSON.stringify(visible[0].tabs) === '["a.js","b.go","c.css","d.md"]', `the strip does not carry every tab: ${JSON.stringify(visible[0].tabs)}`);
      assert(visible[0].w >= 388 && visible[0].h > 300, `the group does not fill the screen: ${JSON.stringify(visible[0])}`);
      const page390 = await mp.evaluate(() => {
        const box = (sel) => {
          const el = document.querySelector(sel);
          const r = el.getBoundingClientRect();
          return el.offsetParent !== null && r.width > 0 && r.right <= window.innerWidth + 1;
        };
        const handles = [...document.querySelectorAll(".editor-split-handle")].filter((el) => el.getBoundingClientRect().width > 0).length;
        return {
          toggle: box(".editor-group-focused [data-editor-drawer-toggle]"),
          menu: box(".editor-group-focused [data-editor-menu]"),
          handles,
          overflow: document.documentElement.scrollWidth > document.documentElement.clientWidth,
        };
      });
      assert(page390.toggle && page390.menu && page390.handles === 0 && !page390.overflow, `the phone layout: ${JSON.stringify(page390)}`);
      await mp.tap(tabSel("d.md"));
      await mp.waitForSelector(`${tabSel("d.md")}.active`, { state: "attached", timeout: 6000 });
      await sleep(400);
      visible = await shown();
      assert(visible.length === 1 && visible[0].active === "d.md" && JSON.stringify(visible[0].tabs) === '["a.js","b.go","c.css","d.md"]',
        `a tap did not bring the tab's group up: ${JSON.stringify(await groups(mp))}`);
      await mp.tap(`${tabSel("d.md")}.active`);
      await mp.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
      const labels = await mp.$$eval(".dc-context-menu .dc-menu-label-head", (els) => els.map((el) => el.textContent));
      assert(labels.includes("Close") && !labels.some((l) => /^(Split|Move to group)/.test(l)), `the phone menu offers groups: ${labels.join(", ")}`);
      await mp.keyboard.press("Escape");
      await mp.waitForSelector(".dc-context-menu", { state: "detached", timeout: 5000 }).catch(() => {});
      await mp.click(".editor-group-focused .cm-content", { force: true });
      await mp.keyboard.type("phone ");
      await mp.waitForSelector(`${tabSel("d.md")}.dirty`, { state: "attached", timeout: 6000 });
      const stored = await mp.evaluate((k) => JSON.parse(localStorage.getItem(k)), tabsKey);
      assert(stored.groups.length === 3 && sameLayout(stored.layout, layout) && stored.focus === 2, `the phone rewrote the layout: ${JSON.stringify(stored)}`);
      return `${visible[0].w}x${visible[0].h}, ${visible[0].tabs.length} tabs in the strip, layout kept`;
    });
  } finally {
    try {
      await page.evaluate((k) => localStorage.removeItem(k), tabsKey);
      await L.deleteProject(page, project);
    } catch (e) {
      console.log(`cleanup: ${e.message}`);
    }
  }
});
