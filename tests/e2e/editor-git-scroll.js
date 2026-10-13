const L = require("./lib");
const { assert, sleep } = L;

// Not a runner of its own: editor-git.js runs these over its scratch
// repository, after the comparison of two files on disk. scrollChecks covers
// how the merge views scroll: the two sides of a git diff and of a comparison
// moving together sideways while the outer .cm-mergeView keeps the vertical
// axis, a finger doing on the phone what the wheel does, and the same diff
// with lines wrapping. Wrapping on and the text without the focus is the
// editor's swipe zone (syncSwipeZone), whose overscroll-behavior: contain
// keeps a wheel from running on into the page. It belongs on the box that
// really scrolls: on the inner .cm-scroller of a side by side diff, which
// never overflows, it ended every wheel before .cm-mergeView saw it, over
// either side, until a click focused the text and the zone went away.
// Gotchas:
//   - the zone only stands while nothing in the editor holds the focus, and
//     a file opened with a fine pointer takes it, so the wrap checks blur the
//     page and wait for the class before the wheel, and read it again after.
//   - the mobile wrap check reads the file the desktop one commits, so a
//     narrowed run names both (CHECK="lines wrapping").
//   - Chromium drives the phone's drag with a real finger through CDP, so the
//     browser's own pan arbitration is part of the check there; WebKit gets
//     the pointer events that finger would raise, dispatched at the point it
//     lands on, which proves the editor scrolls .cm-mergeView and no more.

async function scrollChecks({ page, run, engine, mobile, author, editorBase, openEditor, writeHere, runInShell, diffReady, setDiffView, setEditorSwitch, toggleDiff, diffPressed, pick }) {
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
  // The shell answers before git has run, and waitForFunction cannot await
  // a fetch (see the README note), so the poll runs from here.
  const commitFile = async (path, content) => {
    await writeHere(path, content);
    assert(await runInShell(`git add ${path} && git ${author} commit -qm ${path}\r`) === 200, "the shell refused the commit");
    const first = content.split("\n")[0];
    const deadline = Date.now() + 30000;
    while (Date.now() < deadline) {
      const answer = await page.evaluate(([b, p]) =>
        fetch(`${b}/git/file?path=${encodeURIComponent(p)}`, { headers: { Accept: "application/json" } }).then((r) => r.json()), [editorBase, path]);
      if (typeof answer.content === "string" && answer.content.includes(first)) return;
      await sleep(1000);
    }
    throw new Error(`${path} never reached the last commit`);
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
    await commitFile(wide, wideDoc(40, "a", 40));
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
    const mp = await mobile();
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

  const wrapFile = "wrap-scroll.txt";
  const zoneOn = (target) => target.evaluate(() =>
    document.querySelector("[data-editor-surface]").classList.contains("editor-swipe-zone"));
  const blurText = async (target) => {
    await target.evaluate(() => document.activeElement?.blur());
    await target.waitForFunction(() =>
      document.querySelector("[data-editor-surface]").classList.contains("editor-swipe-zone"), null, { timeout: 6000 })
      .catch(() => { throw new Error("the swipe zone never came up with the text unfocused"); });
  };
  // The scroller of a single editor, the file without a diff or the inline
  // diff, wheeled from its top and read back.
  const wheelPlain = async (target) => {
    const at = await target.evaluate(() => {
      const scroller = document.querySelector("[data-editor-surface] > .cm-editor .cm-scroller");
      scroller.scrollTop = 0;
      const box = scroller.getBoundingClientRect();
      return { x: Math.round(box.left + box.width / 2), y: Math.round(box.top + 60), max: scroller.scrollHeight - scroller.clientHeight };
    });
    assert(at.max > 0, "the file fits the box, so the wheel proves nothing");
    await sleep(300);
    await target.mouse.move(at.x, at.y);
    await target.mouse.wheel(0, 300);
    await sleep(500);
    return target.evaluate(() => Math.round(document.querySelector("[data-editor-surface] > .cm-editor .cm-scroller").scrollTop));
  };

  await run("with lines wrapping and the text not focused, the wheel scrolls a side by side diff over either side, inline and without a diff too", async () => {
    const doc = wideDoc(60, "w", 24);
    await openEditor(page);
    await commitFile(wrapFile, doc);
    await writeHere(wrapFile, doc.replace("w line 12 ", "w CHANGED 12 "));

    await setDiffView(page, "side");
    await setEditorSwitch(page, "diff_collapse", false);
    await setEditorSwitch(page, "line_wrap", true);
    await openEditor(page);
    await page.click(`.editor-item[data-path="${wrapFile}"]`);
    await page.waitForSelector(`.editor-tab[data-path="${wrapFile}"].active`, { timeout: 10000 });
    await diffReady(page);
    await toggleDiff(page);
    await page.waitForSelector(".cm-mergeView", { timeout: 20000 });
    await sleep(800);
    await blurText(page);

    const start = await scrollState(page);
    assert(start.sideScrollable.every((can) => !can), "a side scrolls vertically on its own, the outer view is supposed to");
    assert(start.outerMax > 0, "the diff fits the box, so the wheel proves nothing");
    const sides = [];
    for (const side of [0, 1]) {
      await page.evaluate(() => { document.querySelector(".cm-mergeView").scrollTop = 0; });
      await sleep(300);
      await wheelOverSide(page, side, 0, 300);
      sides.push((await scrollState(page)).outerTop);
    }
    assert(await zoneOn(page), "the swipe zone went away during the wheel, so it proves nothing");
    assert(sides.every((top) => top > 0), `the wheel left the side by side diff standing: left side ${sides[0]}px, right side ${sides[1]}px`);

    await setDiffView(page, "inline");
    await page.waitForSelector(".cm-mergeView", { state: "detached", timeout: 10000 });
    await page.waitForSelector(".cm-deletedChunk", { state: "attached", timeout: 20000 });
    await blurText(page);
    const inline = await wheelPlain(page);
    assert(await zoneOn(page), "the swipe zone went away during the inline wheel");
    assert(inline > 0, "the wheel left the inline diff standing");

    await toggleDiff(page);
    await page.waitForSelector(".cm-deletedChunk", { state: "detached", timeout: 10000 });
    await blurText(page);
    const plain = await wheelPlain(page);
    assert(await zoneOn(page), "the swipe zone went away during the wheel without a diff");
    assert(plain > 0, "the wheel left the file without a diff standing");

    await setEditorSwitch(page, "line_wrap", false);
    return `side by side ${sides.join("px / ")}px, inline ${inline}px, no diff ${plain}px`;
  });

  // A finger in the swipe zone is the editor's own: it scrolls from the box
  // under the finger up to the first one that overflows, .cm-mergeView here,
  // while touch-action stays on the inner boxes the finger lands on.
  const dragUp = async (target, cdp, side) => {
    const at = await target.evaluate((i) => {
      const outer = document.querySelector(".cm-mergeView");
      const box = outer.querySelectorAll(".cm-scroller")[i].getBoundingClientRect();
      const frame = outer.getBoundingClientRect();
      return { x: Math.round(box.left + box.width / 2), y: Math.round(frame.top + frame.height / 2) };
    }, side);
    const steps = 12;
    const travel = 180;
    if (cdp) {
      await cdp.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ x: at.x, y: at.y, id: 9 }] });
      for (let i = 1; i <= steps; i += 1) {
        await cdp.send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ x: at.x, y: Math.round(at.y - (travel * i) / steps), id: 9 }] });
        await sleep(20);
      }
      await cdp.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
    } else {
      await target.evaluate(async ([x, y, n, dy]) => {
        const el = document.elementFromPoint(x, y);
        const send = (type, py) => el.dispatchEvent(new PointerEvent(type, {
          bubbles: true, cancelable: true, pointerId: 33, pointerType: "touch", isPrimary: true,
          clientX: x, clientY: py, buttons: type === "pointerup" ? 0 : 1,
        }));
        send("pointerdown", y);
        for (let i = 1; i <= n; i += 1) {
          send("pointermove", Math.round(y - (dy * i) / n));
          await new Promise((done) => setTimeout(done, 16));
        }
        send("pointerup", y - dy);
      }, [at.x, at.y, steps, travel]);
    }
    await sleep(900);
  };

  await run("mobile: with lines wrapping a vertical drag still scrolls a side by side diff over either side", async () => {
    const mp = await mobile();
    await openEditor(mp);
    await diffReady(mp);
    if (!(await mp.$(".editor.editor-drawer-open"))) {
      await mp.click("[data-editor-drawer-toggle]");
      await mp.waitForSelector(".editor.editor-drawer-open", { timeout: 6000 });
    }
    await mp.waitForSelector(`.editor-item[data-path="${wrapFile}"]`, { timeout: 15000 });
    await mp.click(`.editor-item[data-path="${wrapFile}"]`);
    await mp.waitForSelector(`.editor-tab[data-path="${wrapFile}"].active`, { state: "attached", timeout: 10000 });
    await mp.waitForSelector(".editor.editor-drawer-open", { state: "detached", timeout: 6000 });
    await setDiffView(mp, "side");
    await setEditorSwitch(mp, "diff_collapse", false);
    await setEditorSwitch(mp, "line_wrap", true);
    if (!(await diffPressed(mp))) await toggleDiff(mp);
    await mp.waitForSelector(".cm-mergeView", { timeout: 20000 });
    await sleep(800);
    await blurText(mp);

    const start = await scrollState(mp);
    assert(start.outerMax > 0, "the diff fits the phone, so the drag proves nothing");
    const cdp = engine === "chromium" ? await mp.context().newCDPSession(mp) : null;
    await mp.evaluate(() => {
      window.__dcWrapCancelled = false;
      document.querySelector("[data-editor-surface]").addEventListener("pointercancel", () => { window.__dcWrapCancelled = true; });
    });
    const pageTop = await mp.evaluate(() => window.scrollY);
    const tops = [];
    for (const side of [0, 1]) {
      await mp.evaluate(() => { document.querySelector(".cm-mergeView").scrollTop = 0; });
      await sleep(300);
      await dragUp(mp, cdp, side);
      tops.push((await scrollState(mp)).outerTop);
    }
    const after = await mp.evaluate(() => ({
      cancelled: window.__dcWrapCancelled,
      page: window.scrollY,
      file: document.querySelector("[data-editor-tabs] .editor-tab.active").dataset.path,
    }));
    assert(!after.cancelled, "the browser took the drag: pointercancel instead of the editor's scroll");
    assert(tops.every((top) => top > 0), `the drag left the side by side diff standing: left side ${tops[0]}px, right side ${tops[1]}px`);
    assert(after.page === pageTop, `the page scrolled under the drag: ${pageTop} -> ${after.page}`);
    assert(after.file === wrapFile, `a vertical drag switched the file to ${after.file}`);

    await toggleDiff(mp);
    await mp.waitForSelector(".cm-mergeView", { state: "detached", timeout: 10000 });
    await setEditorSwitch(mp, "line_wrap", false);
    await setEditorSwitch(mp, "diff_collapse", true);
    await setDiffView(mp, "auto");
    return `${cdp ? "a real finger" : "pointer events"} moved the diff ${tops.join("px / ")}px`;
  });
}

module.exports = { scrollChecks };
