const L = require("./lib");
const { assert, sleep } = L;

// Not a runner of its own: editor-git.js runs these over its scratch
// repository right after editor-git-scroll.js, on the wide.txt that one leaves
// changed. dividerChecks covers the line between the two sides of a side by
// side diff (`[data-editor-merge-divider]`, @dc/editor-merge): standing
// between the two panes over their whole height, a mouse drag sharing the
// width differently, neither side narrower than 120px however far the drag
// goes, the share kept on the tab (`diffShare` in its entry of
// `dc-editor-tabs:<project>`, the drag adding no key of its own) and back
// after a tab switch and a reload, a double click evening it; the gutter of
// the right side, its change marks and the sideways sync of the two sides
// untouched by it, a line number of the right side still opening the field
// of a new line comment. Two split diff tabs (wide.txt and wrap-scroll.txt,
// both committed by editor-git-scroll.js) keep their own share through a tab
// switch, a split into a second group, a reload and the move back, a newly
// opened one starts even and a closed one forgets its share. The bar over a
// comparison (`[data-editor-compare]`) parts where the divider stands, on a
// two file compare tab and on a revision comparison of the panel alike,
// through a drag and a reload, and evenly while the inline view leaves no
// divider, so no name and no Save stands over the other side's code. On
// the phone a finger drags it with the editor's swipe zone up (lines
// wrapping, the text not focused), and the gesture stays the divider's: no
// file switch, no pointercancel, no page scroll, the divider at least 15px
// wide under a coarse pointer.
// Gotchas:
//   - Chromium drives the phone's drag with a real finger through CDP, so
//     the browser's own pan arbitration is part of the check there; WebKit
//     gets the pointer events that finger would raise, dispatched on the
//     divider, which proves the handler and the stop at the surface.
//   - the share is a fraction of the room both sides have, the divider's
//     own width left out, so the checks read it the same way.

async function dividerChecks({ page, run, engine, mobile, project, runInShell, openEditor, diffReady, setDiffView, setEditorSwitch, toggleDiff, diffPressed, pick, h }) {
  const wide = "wide.txt";
  const wrap = "wrap-scroll.txt";
  const tabsKey = `dc-editor-tabs:${project}`;
  const geometry = (target, at = 0) => target.evaluate((i) => {
    const outer = document.querySelectorAll(".editor-group")[i].querySelector(".cm-mergeView");
    const box = outer.querySelector(".cm-mergeViewEditors");
    const [a, b] = [...box.querySelectorAll(":scope > .cm-mergeViewEditor")].map((el) => el.getBoundingClientRect());
    const divider = box.querySelector(":scope > [data-editor-merge-divider]");
    const d = divider.getBoundingClientRect();
    const frame = outer.getBoundingClientRect();
    const cs = getComputedStyle(divider);
    return {
      left: a.width,
      right: b.width,
      share: a.width / (a.width + b.width),
      divider: { x: d.left + d.width / 2, width: d.width, height: d.height, display: cs.display, visibility: cs.visibility, touch: cs.touchAction },
      between: a.right <= d.left + 0.5 && d.right <= b.left + 0.5,
      paneHeight: Math.min(a.height, b.height),
      boxLeft: box.getBoundingClientRect().left,
      frame: { top: frame.top, height: frame.height },
      coarse: matchMedia("(pointer: coarse)").matches,
    };
  }, at);
  const stored = (target) => target.evaluate((k) => {
    const saved = JSON.parse(localStorage.getItem(k) || "{}");
    const shares = {};
    for (const e of saved.open || []) if (e && typeof e === "object" && e.path) shares[e.path] = e.diffShare;
    return { shares, keys: Object.keys(localStorage) };
  }, tabsKey);
  const near = (got, want, tolerance = 0.02) => Math.abs(got - want) <= tolerance;
  const mouseDrag = async (toX, at = 0) => {
    const g = await geometry(page, at);
    const y = Math.round(g.frame.top + 40);
    await page.mouse.move(Math.round(g.divider.x), y);
    await page.mouse.down();
    const steps = 8;
    for (let i = 1; i <= steps; i += 1) {
      await page.mouse.move(Math.round(g.divider.x + ((toX - g.divider.x) * i) / steps), y);
      await sleep(16);
    }
    await page.mouse.up();
    await sleep(400);
    return geometry(page, at);
  };
  // Where the divider's center has to stand for the left side to get share
  // of the room.
  const xFor = (g, share) => g.boxLeft + share * (g.left + g.right) + g.divider.width / 2;
  const openSplit = async (path) => {
    await page.click(`.editor-item[data-path="${path}"]`);
    await page.waitForSelector(`.editor-tab[data-path="${path}"].active`, { timeout: 10000 });
    await diffReady(page);
    if (!(await diffPressed(page))) await toggleDiff(page);
    await page.waitForSelector(".cm-mergeView [data-editor-merge-divider]", { timeout: 20000 });
    await sleep(800);
  };
  const openWide = () => openSplit(wide);
  const showTab = async (path) => {
    await page.click(`.editor-tab[data-path="${path}"]`);
    await page.waitForSelector(`.editor-tab[data-path="${path}"].active`, { timeout: 10000 });
    await page.waitForSelector(".cm-mergeView [data-editor-merge-divider]", { timeout: 20000 });
    await sleep(600);
    return geometry(page);
  };
  const barSplit = () => page.evaluate(() => {
    const bar = document.querySelector("[data-editor-compare]");
    const [l, r] = [...bar.children].map((el) => el.getBoundingClientRect());
    const d = document.querySelector(".cm-mergeView [data-editor-merge-divider]")?.getBoundingClientRect();
    const pane = document.querySelector(".cm-mergeView .cm-mergeViewEditor")?.getBoundingClientRect();
    return {
      shown: !bar.hidden && bar.getBoundingClientRect().height > 0,
      start: l.left,
      part: r.left,
      end: r.right,
      divider: d ? { left: d.left, right: d.right } : null,
      pane: pane ? pane.left : null,
    };
  });
  const barFollows = async (what) => {
    const b = await barSplit();
    assert(b.shown && b.divider, `${what}: no bar over a divider: ${JSON.stringify(b)}`);
    assert(b.part >= b.divider.left - 0.5 && b.part <= b.divider.right + 0.5 && Math.abs(b.start - b.pane) <= 1,
      `${what}: the bar parts at ${b.part.toFixed(1)}, the divider stands at ${b.divider.left.toFixed(1)} to ${b.divider.right.toFixed(1)}`);
    return b;
  };
  const dividersStand = (n) => page.waitForFunction((want) => [...document.querySelectorAll(".editor-group")]
    .filter((g) => g.querySelector(".cm-mergeView [data-editor-merge-divider]")).length === want, n, { timeout: 20000 });

  await run("the divider of a side by side diff shares the width by mouse, keeps both sides at 120px, the share rides the tab through a switch and a reload, a double click evens it", async () => {
    await setDiffView(page, "side");
    await setEditorSwitch(page, "diff_collapse", false);
    await setEditorSwitch(page, "line_wrap", false);
    await openEditor(page);
    await openWide();

    const start = await geometry(page);
    assert(start.divider.display !== "none" && start.divider.visibility === "visible" && start.divider.width > 0,
      `the divider does not show: ${JSON.stringify(start.divider)}`);
    assert(start.between, `the divider does not stand between the two sides: ${JSON.stringify(start)}`);
    assert(start.divider.height >= start.paneHeight - 1, `the divider is shorter than the sides: ${start.divider.height} < ${start.paneHeight}`);
    assert(start.divider.touch === "none", `the divider leaves the touch to the browser: ${start.divider.touch}`);
    assert(Math.abs(start.left - start.right) <= 2, `the sides do not start even: ${start.left} / ${start.right}`);

    const keysBefore = (await stored(page)).keys;
    const dragged = await mouseDrag(xFor(start, 0.3));
    assert(near(dragged.share, 0.3), `the drag left the share at ${dragged.share.toFixed(3)}`);
    const saved = await stored(page);
    assert(near(saved.shares[wide] ?? 0, 0.3), `the tab entry stores the share as ${saved.shares[wide]}`);
    const added = saved.keys.filter((k) => !keysBefore.includes(k));
    assert(added.length === 0, `the drag stored a key of its own: ${added.join(", ")}`);

    // What already worked beside it: the right side's gutter is reached at
    // its own left edge, both sides carry their change marks, and the two
    // sides still scroll together sideways.
    const beside = await page.evaluate(() => {
      const outer = document.querySelector(".cm-mergeView");
      const gutter = outer.querySelector(".cm-merge-b .cm-gutters").getBoundingClientRect();
      const line = outer.querySelector(".cm-merge-b .cm-content").getBoundingClientRect();
      const hit = document.elementFromPoint(gutter.left + 1, line.top + 8);
      const scrollers = outer.querySelectorAll(".cm-scroller");
      scrollers[0].scrollLeft = 300;
      return {
        gutterHit: !!hit && !!hit.closest(".cm-merge-b .cm-gutters"),
        hit: hit ? hit.className : "",
        marks: [".cm-merge-a .cm-changedLine", ".cm-merge-b .cm-changedLine"].map((sel) => outer.querySelectorAll(sel).length),
      };
    });
    await sleep(400);
    const sideways = await page.evaluate(() => [...document.querySelectorAll(".cm-mergeView .cm-scroller")].map((el) => Math.round(el.scrollLeft)));
    assert(beside.gutterHit, `the right side's gutter is covered at its edge by ${beside.hit}`);
    assert(beside.marks.every((n) => n > 0), `the change marks went missing: ${JSON.stringify(beside.marks)}`);
    assert(sideways.join() === "300,300", `the two sides no longer scroll together: ${sideways.join()}`);
    await page.evaluate(() => { document.querySelectorAll(".cm-mergeView .cm-scroller")[0].scrollLeft = 0; });
    // A click on a line number of the resized right side still opens the
    // field of a new line comment under it, and a second one closes it.
    const number = page.locator(".cm-merge-b .cm-lineNumbers .cm-gutterElement:visible").filter({ hasText: /^3$/ }).first();
    const field = page.locator('.cm-merge-b .cm-content [data-line-block="3"] [data-line-comment-form]');
    await number.click();
    await field.waitFor({ timeout: 6000 }).catch(() => { throw new Error("a line number of the resized right side opened no comment field"); });
    await number.click();
    await field.waitFor({ state: "detached", timeout: 6000 }).catch(() => { throw new Error("a second click left the comment field standing"); });

    const farLeft = await mouseDrag(start.boxLeft + 5);
    assert(farLeft.left >= 119 && farLeft.left <= 122, `the left side went to ${farLeft.left}px`);
    const farRight = await mouseDrag(start.boxLeft + start.left + start.right + 60);
    assert(farRight.right >= 119 && farRight.right <= 122, `the right side went to ${farRight.right}px`);

    const set = await mouseDrag(xFor(farRight, 0.35));
    assert(near(set.share, 0.35), `the drag back left the share at ${set.share.toFixed(3)}`);

    // A tab switch builds the merge view anew, and so does a reload.
    const other = await page.evaluate((w) => [...document.querySelectorAll(".editor-item[data-path]:not([data-dir])")]
      .filter((row) => row.getBoundingClientRect().height > 0).map((row) => row.dataset.path).find((p) => p !== w), wide);
    assert(other, "the tree holds no second file to switch to");
    await page.click(`.editor-item[data-path="${other}"]`);
    await page.waitForSelector(`.editor-tab[data-path="${other}"].active`, { timeout: 10000 });
    await sleep(600);
    await page.click(`.editor-tab[data-path="${wide}"]`);
    await page.waitForSelector(`.editor-tab[data-path="${wide}"].active`, { timeout: 10000 });
    await page.waitForSelector(".cm-mergeView [data-editor-merge-divider]", { timeout: 20000 });
    await sleep(600);
    const switched = await geometry(page);
    assert(near(switched.share, 0.35), `after a tab switch the share is ${switched.share.toFixed(3)}`);

    await openEditor(page);
    await page.waitForSelector(`.editor-tab[data-path="${wide}"].active`, { timeout: 15000 });
    await page.waitForSelector(".cm-mergeView [data-editor-merge-divider]", { timeout: 20000 });
    await sleep(800);
    const reloaded = await geometry(page);
    assert(near(reloaded.share, 0.35), `after a reload the share is ${reloaded.share.toFixed(3)}`);

    await page.dblclick("[data-editor-merge-divider]");
    await sleep(400);
    const even = await geometry(page);
    assert(Math.abs(even.left - even.right) <= 2, `a double click left the sides at ${even.left} / ${even.right}`);
    const evened = (await stored(page)).shares[wide];
    assert(near(evened ?? 0, 0.5, 0.001), `a double click left ${evened} stored`);

    await toggleDiff(page);
    await page.waitForSelector(".cm-mergeView", { state: "detached", timeout: 10000 });
    return `mouse to ${dragged.share.toFixed(2)}, floors ${Math.round(farLeft.left)}px / ${Math.round(farRight.right)}px, ${switched.share.toFixed(2)} after a tab switch, ${reloaded.share.toFixed(2)} after a reload, even again`;
  });

  await run("two split diff tabs keep their own share through a switch, a second group and a reload, a new tab starts even, a closed one forgets", async () => {
    await openEditor(page);
    await openWide();
    const wideStart = await geometry(page);
    await mouseDrag(xFor(wideStart, 0.3));
    if (await page.$(`.editor-tab[data-path="${wrap}"]`)) {
      await page.click(`.editor-tab[data-path="${wrap}"] .editor-tab-close`);
      await page.waitForSelector(`.editor-tab[data-path="${wrap}"]`, { state: "detached", timeout: 6000 });
    }
    await openSplit(wrap);
    const fresh = await geometry(page);
    assert(near(fresh.share, 0.5), `a new split diff tab starts at ${fresh.share.toFixed(3)} beside a tab at 0.3`);
    const wrapSet = await mouseDrag(xFor(fresh, 0.65));
    assert(near(wrapSet.share, 0.65), `the drag left the second tab at ${wrapSet.share.toFixed(3)}`);

    const backToWide = await showTab(wide);
    const backToWrap = await showTab(wrap);
    assert(near(backToWide.share, 0.3), `the first tab came back at ${backToWide.share.toFixed(3)}`);
    assert(near(backToWrap.share, 0.65), `the second tab came back at ${backToWrap.share.toFixed(3)}`);

    // Side by side in two groups, each merge view reads its own tab.
    await pick(`.editor-tab[data-path="${wrap}"]`, "Split right");
    await page.waitForFunction(() => document.querySelectorAll(".editor-group").length === 2, null, { timeout: 10000 });
    await page.click(`.editor-tab[data-path="${wide}"]`);
    await dividersStand(2);
    await sleep(800);
    const grouped = [await geometry(page, 0), await geometry(page, 1)];
    assert(near(grouped[0].share, 0.3) && near(grouped[1].share, 0.65), `in two groups the shares are ${grouped.map((g) => g.share.toFixed(3)).join(" / ")}`);
    const moved = await mouseDrag(xFor(grouped[1], 0.55), 1);
    const untouched = await geometry(page, 0);
    assert(near(moved.share, 0.55) && near(untouched.share, 0.3), `a drag in the second group left ${untouched.share.toFixed(3)} / ${moved.share.toFixed(3)}`);

    await openEditor(page);
    await dividersStand(2);
    await sleep(800);
    const restored = [await geometry(page, 0), await geometry(page, 1)];
    const saved = await stored(page);
    assert(near(restored[0].share, 0.3) && near(restored[1].share, 0.55), `after a reload the groups stand at ${restored.map((g) => g.share.toFixed(3)).join(" / ")}`);
    assert(near(saved.shares[wide] ?? 0, 0.3) && near(saved.shares[wrap] ?? 0, 0.55), `the tab entries store ${JSON.stringify(saved.shares)}`);

    await pick(`.editor-tab[data-path="${wrap}"]`, "Move to group 1");
    await page.waitForFunction(() => document.querySelectorAll(".editor-group").length === 1, null, { timeout: 10000 });
    const rejoined = await showTab(wrap);
    assert(near(rejoined.share, 0.55), `back in the first group the tab stands at ${rejoined.share.toFixed(3)}`);

    await page.click(`.editor-tab[data-path="${wrap}"] .editor-tab-close`);
    await page.waitForSelector(`.editor-tab[data-path="${wrap}"]`, { state: "detached", timeout: 6000 });
    await sleep(400);
    const forgotten = await stored(page);
    assert(!(wrap in forgotten.shares), `the closed tab is still stored: ${JSON.stringify(forgotten.shares)}`);
    await openSplit(wrap);
    const reopened = await geometry(page);
    assert(near(reopened.share, 0.5), `the reopened tab starts at ${reopened.share.toFixed(3)}`);
    const wideAgain = await showTab(wide);
    assert(near(wideAgain.share, 0.3), `the first tab moved along to ${wideAgain.share.toFixed(3)}`);

    await toggleDiff(page);
    await page.waitForSelector(".cm-mergeView", { state: "detached", timeout: 10000 });
    return `new tab ${fresh.share.toFixed(2)}, ${backToWide.share.toFixed(2)} / ${backToWrap.share.toFixed(2)} after switches, groups ${restored.map((g) => g.share.toFixed(2)).join(" / ")} after a reload, ${reopened.share.toFixed(2)} after close and reopen`;
  });

  await run("the bar over a comparison parts where the divider stands, on a compare tab and a revision comparison, through a drag, a reload and the inline view", async () => {
    await setDiffView(page, "side");
    await openEditor(page);
    await page.click(`.editor-item[data-path="${wide}"]`);
    await page.waitForSelector(`.editor-tab[data-path="${wide}"].active`, { timeout: 10000 });
    await pick(`.editor-tab[data-path="${wide}"]`, "Select for compare");
    await pick(`.editor-item[data-path="${wrap}"]`, "^Compare with");
    await page.waitForSelector("[data-editor-compare]:not([hidden])", { timeout: 20000 });
    await page.waitForSelector(".cm-mergeView [data-editor-merge-divider]", { timeout: 20000 });
    await sleep(800);
    const compareTab = await page.evaluate(() => document.querySelector(".editor-tab.active").dataset.path);
    assert(/^\/\/compare\//.test(compareTab), `the comparison opened as ${compareTab}`);
    await barFollows("a new compare tab");
    const start = await geometry(page);
    const dragged = await mouseDrag(xFor(start, 0.7));
    assert(near(dragged.share, 0.7), `the drag left the compare tab at ${dragged.share.toFixed(3)}`);
    const atDrag = await barFollows("after a drag on the compare tab");
    const names = await page.evaluate(() => [...document.querySelectorAll("[data-editor-compare-name]")].map((el) => el.getBoundingClientRect()));
    assert(names[0].right <= atDrag.divider.left && names[1].left >= atDrag.divider.right,
      `the names stand over the other side: ${names.map((n) => `${n.left.toFixed(0)}-${n.right.toFixed(0)}`).join(" / ")}`);

    await openEditor(page);
    await page.waitForSelector(`.editor-tab[data-path="${compareTab}"].active`, { timeout: 15000 });
    await page.waitForSelector(".cm-mergeView [data-editor-merge-divider]", { timeout: 20000 });
    await sleep(800);
    const reloaded = await geometry(page);
    assert(near(reloaded.share, 0.7), `after a reload the compare tab stands at ${reloaded.share.toFixed(3)}`);
    await barFollows("on the compare tab after a reload");
    await page.click(`.editor-tab[data-path="${compareTab}"] .editor-tab-state`);
    await page.waitForSelector(`.editor-tab[data-path="${compareTab}"]`, { state: "detached", timeout: 8000 });

    // A row of the panel opens a revision comparison only between two
    // commits, against the working copy it opens the file itself.
    const added = `$(git log -1 --format=%H -- ${wrap})`;
    assert(await runInShell(`git tag dvd-a ${added}~1 && git tag dvd-b ${added}\r`) === 200, "the shell refused the tags");
    await h.waitCompare("dvd-a", "dvd-b", "since", 1);
    await h.closeRevdiffTabs(page);
    await openEditor(page);
    await diffReady(page);
    await h.openRevdiff(page);
    await h.revdiffSettled(page);
    await h.typeSide(page, "from", "dvd-a");
    await h.typeSide(page, "to", "dvd-b");
    await page.fill("[data-editor-revdiff-filter]", wrap);
    await page.waitForFunction(() => /^1 of /.test(document.querySelector("[data-editor-revdiff-filter-count]").textContent.trim()), null, { timeout: 10000 });
    await page.keyboard.press("Enter");
    await page.waitForSelector(".editor-tab.active[data-path^='//revdiff/']", { timeout: 15000 });
    await page.waitForSelector(".cm-mergeView [data-editor-merge-divider]", { timeout: 20000 });
    await sleep(800);
    const revStart = await geometry(page);
    const revDragged = await mouseDrag(xFor(revStart, 0.3));
    assert(near(revDragged.share, 0.3), `the drag left the revision comparison at ${revDragged.share.toFixed(3)}`);
    await barFollows("after a drag on the revision comparison");
    await setDiffView(page, "inline");
    await page.waitForSelector(".cm-mergeView", { state: "detached", timeout: 10000 });
    await sleep(400);
    const inline = await barSplit();
    const middle = (inline.start + inline.end) / 2;
    assert(inline.shown && Math.abs(inline.part - middle) <= 1, `inline, the bar parts at ${inline.part.toFixed(1)}, not at its middle ${middle.toFixed(1)}`);
    await setDiffView(page, "side");
    await page.waitForSelector(".cm-mergeView [data-editor-merge-divider]", { timeout: 20000 });
    await sleep(800);
    const back = await geometry(page);
    assert(near(back.share, 0.3), `back side by side the revision comparison stands at ${back.share.toFixed(3)}`);
    await barFollows("back side by side on the revision comparison");

    await h.closeRevdiffTabs(page);
    if (await page.isVisible("[data-editor-revdiff-close]")) await page.click("[data-editor-revdiff-close]");
    await page.evaluate((k) => localStorage.removeItem(k), h.revdiffKey);
    assert(await runInShell("git tag -d dvd-a dvd-b > /dev/null\r") === 200, "the shell refused the cleanup");
    return `compare tab at ${dragged.share.toFixed(2)} and ${reloaded.share.toFixed(2)} after a reload, revision comparison at ${revDragged.share.toFixed(2)}, the bar parted at the divider each time and in the middle inline`;
  });

  await run("mobile: a finger drags the divider of a side by side diff in the swipe zone, the gesture stays the divider's", async () => {
    const mp = await mobile();
    await openEditor(mp);
    await diffReady(mp);
    if (!(await mp.$(".editor.editor-drawer-open"))) {
      await mp.click("[data-editor-drawer-toggle]");
      await mp.waitForSelector(".editor.editor-drawer-open", { timeout: 6000 });
    }
    await mp.waitForSelector(`.editor-item[data-path="${wide}"]`, { timeout: 15000 });
    await mp.click(`.editor-item[data-path="${wide}"]`);
    await mp.waitForSelector(`.editor-tab[data-path="${wide}"].active`, { state: "attached", timeout: 10000 });
    await mp.waitForSelector(".editor.editor-drawer-open", { state: "detached", timeout: 6000 });
    await setDiffView(mp, "side");
    await setEditorSwitch(mp, "diff_collapse", false);
    await setEditorSwitch(mp, "line_wrap", true);
    if (!(await diffPressed(mp))) await toggleDiff(mp);
    await mp.waitForSelector(".cm-mergeView [data-editor-merge-divider]", { timeout: 20000 });
    await sleep(800);
    await mp.evaluate(() => document.activeElement?.blur());
    await mp.waitForFunction(() => document.querySelector("[data-editor-surface]").classList.contains("editor-swipe-zone"), null, { timeout: 6000 })
      .catch(() => { throw new Error("the swipe zone never came up, so the drag proves nothing about it"); });

    const start = await geometry(mp);
    if (start.coarse) assert(start.divider.width >= 15, `the divider is ${start.divider.width}px under a coarse pointer`);
    await mp.evaluate(() => {
      window.__dcDividerCancelled = false;
      document.querySelector("[data-editor-surface]").addEventListener("pointercancel", () => { window.__dcDividerCancelled = true; });
    });
    const pageTop = await mp.evaluate(() => window.scrollY);
    const cdp = engine === "chromium" ? await mp.context().newCDPSession(mp) : null;
    const fingerDrag = async (toX) => {
      const g = await geometry(mp);
      const x = Math.round(g.divider.x);
      const y = Math.round(g.frame.top + g.frame.height / 2);
      const steps = 12;
      if (cdp) {
        await cdp.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ x, y, id: 11 }] });
        for (let i = 1; i <= steps; i += 1) {
          await cdp.send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ x: Math.round(x + ((toX - x) * i) / steps), y, id: 11 }] });
          await sleep(20);
        }
        await cdp.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
      } else {
        await mp.evaluate(async ([fx, fy, n, tx]) => {
          const el = document.elementFromPoint(fx, fy);
          const send = (type, px) => el.dispatchEvent(new PointerEvent(type, {
            bubbles: true, cancelable: true, pointerId: 41, pointerType: "touch", isPrimary: true,
            clientX: px, clientY: fy, buttons: type === "pointerup" ? 0 : 1,
          }));
          send("pointerdown", fx);
          for (let i = 1; i <= n; i += 1) {
            send("pointermove", Math.round(fx + ((tx - fx) * i) / n));
            await new Promise((done) => setTimeout(done, 16));
          }
          send("pointerup", tx);
        }, [x, y, steps, toX]);
      }
      await sleep(700);
      return geometry(mp);
    };

    const moved = await fingerDrag(xFor(start, 0.62));
    const floor = await fingerDrag(start.boxLeft + 2);
    const after = await mp.evaluate(() => ({
      cancelled: window.__dcDividerCancelled,
      page: window.scrollY,
      file: document.querySelector("[data-editor-tabs] .editor-tab.active").dataset.path,
      pill: [...document.querySelectorAll("[data-editor-swipe-pill]")].some((el) => el.getBoundingClientRect().width > 0),
    }));
    assert(near(moved.share, 0.62, 0.03), `the finger left the share at ${moved.share.toFixed(3)}`);
    assert(floor.left >= 119 && floor.left <= 122, `the finger took the left side down to ${floor.left}px`);
    assert(!after.cancelled, "the browser took the drag: pointercancel instead of the divider's move");
    assert(after.page === pageTop, `the page scrolled under the drag: ${pageTop} -> ${after.page}`);
    assert(after.file === wide && !after.pill, `the drag swiped to another file: ${after.file}, pill ${after.pill}`);

    await toggleDiff(mp);
    await mp.waitForSelector(".cm-mergeView", { state: "detached", timeout: 10000 });
    await setEditorSwitch(mp, "line_wrap", false);
    await setEditorSwitch(mp, "diff_collapse", true);
    await setDiffView(mp, "auto");
    return `${cdp ? "a real finger" : "pointer events"} moved the share to ${moved.share.toFixed(2)}, the floor held at ${Math.round(floor.left)}px, divider ${start.divider.width}px wide`;
  });
}

module.exports = { dividerChecks };
