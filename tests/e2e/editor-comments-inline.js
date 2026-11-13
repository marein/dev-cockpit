const L = require("./lib");
const { assert, sleep } = L;

// Not a runner of its own: editor-comments.js runs these checks in every
// engine over its scratch project. The editor's line comment threads and the
// field of a new comment stand under their lines (@dc/editor-threads over
// @dc/line-blocks, a CodeMirror block widget that is the very node handed to
// it): a field holding words stays on a second press, which hands it the
// focus, and Escape closes only an empty one, on one with words it gives the
// keys back to the code. Keys typed into a field never reach the editor's
// bindings, the code stays as it was, a click on a note keeps the keys out
// of the code too and a drag selects the note's text. An unfolded thread and a field being
// typed stay under their line while the code above them is edited, saved and
// read again from the disk, with the words, the caret and the focus kept. Open
// thread in the Line comments sheet opens the file at the line with the thread
// unfolded and scrolled into view, also far down a long file; a thread whose
// file is gone shows in the dialog, titled Line comment with its place. Two
// editor groups keep their own threads, and a tab moved to the other group
// takes its unfolded thread along. In a file shorter than the pane every line
// number stays beside its code while a field or a card grows (alignedGutter,
// which forge-comments.js borrows): CodeMirror keeps such a file's content at
// the pane's height and measures its lines again only when told. On a 390px
// phone the field stays inside the width, and when the soft keyboard takes
// the lower half of the window the focused field stands in what remains.

// alignedGutter waits up to two seconds for every line of the focused group's
// code to have its line number at its own height, for a file the pane holds
// whole, and names the lines standing off otherwise.
async function alignedGutter(page, what) {
  const off = () => page.evaluate(() => {
    const group = document.querySelector(".editor-group-focused") || document;
    const numbers = new Map();
    for (const el of group.querySelectorAll(".cm-lineNumbers .cm-gutterElement")) {
      if (/^\d+$/.test(el.textContent) && el.style.visibility !== "hidden" && !numbers.has(Number(el.textContent))) numbers.set(Number(el.textContent), el);
    }
    return [...group.querySelectorAll(".cm-content > .cm-line")].map((line, i) => {
      const number = numbers.get(i + 1);
      return { line: i + 1, off: number ? Math.round(number.getBoundingClientRect().top - line.getBoundingClientRect().top) : "none" };
    }).filter((l) => l.off === "none" || Math.abs(l.off) > 1);
  });
  let lines = await off();
  for (let i = 0; lines.length && i < 20; i++) {
    await sleep(100);
    lines = await off();
  }
  assert(!lines.length, `${what}: line numbers stand off their code ${JSON.stringify(lines)}`);
}
module.exports.alignedGutter = alignedGutter;

module.exports.inlineChecks = async function inlineChecks({ page, run, mobilePage, scratch, file, saveFile, openEditor, openFile, waitMarked, getComments, openSheet, sheetCell, pickMenuItem }) {
  const comments = `/projects/${encodeURIComponent(scratch)}/editor/comments`;
  const post = (path, body) => page.evaluate(([p, b]) => fetch(p, {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]').content, Accept: "application/json" },
    body: JSON.stringify(b),
  }).then((r) => r.status), [path, body]);
  const writeFile = (path, content) => page.evaluate(([p, f, c]) => fetch(p, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded", "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]').content, Accept: "application/json" },
    body: new URLSearchParams({ path: f, content: c }).toString(),
  }).then((r) => r.status), [`/projects/${encodeURIComponent(scratch)}/editor/file`, path, content]);
  const clear = () => post(`${comments}/delete`, { all: true });
  const docText = (p = page) => p.evaluate(() => [...document.querySelectorAll(".editor-group-focused .cm-content > .cm-line")].map((l) => l.textContent).join("\n"));
  const blockOf = (locator) => locator.evaluate((el) => {
    const block = el.closest("[data-line-block]");
    return { line: block.dataset.lineBlock, above: block.previousElementSibling?.textContent };
  });
  const focused = (p = page) => p.evaluate(() => {
    const el = document.activeElement;
    return { field: !!el?.matches("[data-line-comment-text], [data-line-thread-reply-text]"), value: el?.value, caret: el?.selectionStart, code: !!el?.matches(".cm-content") };
  });
  const dirty = () => page.locator(`.editor-tab[data-path="${file}"]`).evaluate((el) => el.classList.contains("dirty"));

  await run("a field holding words stays on a second press and takes the focus, Escape closes only an empty one", async () => {
    await clear();
    await saveFile("one\ntwo\nthree\nfour\nfive\n");
    await openEditor(page);
    await openFile(page);
    await L.pressLine(page, 4);
    const field = L.lineField(page, 4);
    await field.waitFor({ timeout: 6000 });
    await field.locator("[data-line-comment-text]").click();
    await page.keyboard.type("keep me");
    await page.locator(".cm-content > .cm-line").first().click();
    await L.pressLine(page, 4);
    await sleep(300);
    let now = await focused();
    assert(await L.seen(field) && now.field && now.value === "keep me", `a press on the line took the field with words: ${JSON.stringify(now)}`);
    await page.keyboard.press("Escape");
    await sleep(200);
    now = await focused();
    assert(await L.seen(field) && now.code, `Escape on a field with words did not keep it and give the keys to the code: ${JSON.stringify(now)}`);
    await field.locator("[data-line-comment-text]").fill("");
    await field.locator("[data-line-comment-text]").press("Escape");
    await page.waitForFunction(() => !document.querySelector(".cm-content [data-line-comment-form]"), null, { timeout: 6000 });
    assert((await focused()).code, "Escape on the empty field did not hand the keys back to the code");
    await L.pressLine(page, 4);
    await field.waitFor({ timeout: 6000 });
    await L.pressLine(page, 4);
    await page.waitForFunction(() => !document.querySelector(".cm-content [data-line-comment-form]"), null, { timeout: 6000 });
    await L.pressLine(page, 4);
    await field.waitFor({ timeout: 6000 });
    await field.locator("[data-line-comment-cancel]").click();
    await page.waitForFunction(() => !document.querySelector(".cm-content [data-line-comment-form]"), null, { timeout: 6000 });
  });

  await run("keys typed into a field never reach the editor's bindings, the code stays as it was", async () => {
    const before = await docText();
    await L.pressLine(page, 2);
    const text = L.lineField(page, 2).locator("[data-line-comment-text]");
    await text.waitFor({ timeout: 6000 });
    await text.click();
    await page.keyboard.type("alpha");
    for (const key of ["Alt+ArrowUp", "Alt+ArrowDown", "Control+d", "Control+z", "Control+/", "Shift+Tab"]) {
      await text.focus();
      await page.keyboard.press(key);
    }
    await text.focus();
    await page.keyboard.press("End");
    await page.keyboard.press("Enter");
    await page.keyboard.type("beta");
    await page.keyboard.press("Tab");
    await sleep(300);
    assert(await docText() === before && !(await dirty()), `a key typed into the field reached the code: ${JSON.stringify(await docText())}`);
    assert((await text.inputValue()).endsWith("\nbeta") && await L.seen(text), `the field lost its words: ${JSON.stringify(await text.inputValue())}`);
    await text.focus();
    await page.keyboard.press("Control+Enter");
    const card = L.lineThread(page, "beta");
    await card.waitFor({ timeout: 6000 });
    assert(await docText() === before && !(await dirty()), "Ctrl+Enter in the field wrote into the code");
    const note = card.locator("[data-comment-note-text]");
    await note.click();
    await page.keyboard.type("qq");
    await page.keyboard.press("Backspace");
    assert(await docText() === before && !(await dirty()), "keys typed after a click on a note reached the code");
    const box = await note.boundingBox();
    await page.mouse.move(box.x + 2, box.y + 8);
    await page.mouse.down();
    await page.mouse.move(box.x + box.width - 2, box.y + 8, { steps: 6 });
    await page.mouse.up();
    const picked = await page.evaluate(() => String(getSelection()));
    assert(picked.includes("alpha") || picked.includes("beta"), `a drag over the note selected ${JSON.stringify(picked)}`);
    await L.pressLine(page, 2);
    await page.waitForFunction(() => !document.querySelector(".cm-content [data-line-thread]"), null, { timeout: 6000 });
    await clear();
    await waitMarked(page, "");
  });

  await run("an unfolded thread and a typed field stay under their lines while the code above is edited, saved and read again from the disk", async () => {
    const added = await post(comments, { path: file, line: 2, text: "stays put" });
    assert(added === 200, `the comment answered ${added}`);
    await waitMarked(page, "2");
    await L.pressLine(page, 2);
    const card = L.lineThread(page, "stays put");
    await card.waitFor({ timeout: 6000 });
    await L.pressLine(page, 4);
    await L.lineField(page, 4).waitFor({ timeout: 6000 });
    const field = page.locator(".cm-content [data-line-comment-form]");
    await field.locator("[data-line-comment-text]").click();
    await page.keyboard.type("typed under four");
    await page.locator(".cm-content > .cm-line").first().click();
    await page.keyboard.press("Control+Home");
    await page.keyboard.type("zero");
    await page.keyboard.press("Enter");
    await waitMarked(page, "3");
    let thread = await blockOf(card);
    let typed = await blockOf(field);
    assert(thread.line === "3" && thread.above === "two" && typed.line === "5" && typed.above === "four", `the edit above left them behind: ${JSON.stringify({ thread, typed })}`);
    await page.keyboard.press("Control+s");
    await page.waitForFunction((f) => !document.querySelector(`.editor-tab[data-path="${f}"]`)?.classList.contains("dirty"), file, { timeout: 8000 });
    await sleep(800);
    thread = await blockOf(card);
    typed = await blockOf(field);
    assert(thread.line === "3" && typed.line === "5" && await field.locator("[data-line-comment-text]").inputValue() === "typed under four", `the save moved them: ${JSON.stringify({ thread, typed })}`);
    const text = field.locator("[data-line-comment-text]");
    await text.click();
    await page.keyboard.press("End");
    await page.keyboard.type(" and more");
    const status = await writeFile(file, "new head\nzero\none\ntwo\nthree\nfour\nfive\n");
    assert(status === 200, `writing the disk answered ${status}`);
    await page.waitForFunction(() => document.querySelector(".editor-group-focused .cm-content > .cm-line")?.textContent === "new head", null, { timeout: 15000 });
    await waitMarked(page, "4");
    await sleep(600);
    thread = await blockOf(card);
    typed = await blockOf(field);
    const now = await focused();
    assert(thread.line === "4" && thread.above === "two" && typed.line === "6" && typed.above === "four", `the disk's change left them behind: ${JSON.stringify({ thread, typed })}`);
    assert(now.field && now.value === "typed under four and more" && now.caret === now.value.length, `the field lost its words, caret or focus to the disk: ${JSON.stringify(now)}`);
    await page.keyboard.press("Control+Enter");
    await L.lineThread(page, "typed under four and more").waitFor({ timeout: 6000 });
    const stored = (await getComments(page)).comments.find((c) => c.text === "typed under four and more");
    assert(stored && stored.line === 6 && stored.lineText === "four", `the field's comment is stored as ${JSON.stringify(stored)}`);
    await clear();
    await saveFile("one\ntwo\nthree\nfour\nfive\n");
    await page.waitForFunction(() => document.querySelector(".editor-group-focused .cm-content > .cm-line")?.textContent === "one", null, { timeout: 15000 });
  });

  await run("in a file shorter than the pane the line numbers stay beside their code while a field grows, Reply opens and a reply lands", async () => {
    await clear();
    await openEditor(page);
    await openFile(page);
    await L.pressLine(page, 2);
    const field = L.lineField(page, 2);
    await field.waitFor({ timeout: 6000 });
    await alignedGutter(page, "the field open");
    await field.locator("[data-line-comment-text]").click();
    await page.keyboard.type("grows one");
    for (const word of ["two", "three", "four", "five", "six"]) {
      await page.keyboard.press("Enter");
      await page.keyboard.type(`grows ${word}`);
    }
    await alignedGutter(page, "six lines typed into the field");
    await page.keyboard.press("Control+Enter");
    const card = L.lineThread(page, "grows one");
    await card.waitFor({ timeout: 6000 });
    await alignedGutter(page, "the thread added");
    await card.locator("[data-line-thread-reply-open]").click();
    await card.locator("[data-line-thread-reply-text]").waitFor({ timeout: 6000 });
    await alignedGutter(page, "Reply open");
    const id = (await getComments(page)).comments.find((c) => c.text.startsWith("grows one")).id;
    assert(await post(`${comments}/reply`, { thread: id, text: "landed\nfrom elsewhere\nin three lines" }) === 200, "the other device's reply was not posted");
    await page.waitForFunction(() => [...document.querySelectorAll(".cm-content [data-line-thread] [data-comment-note-text]")].some((el) => el.textContent.startsWith("landed")), null, { timeout: 8000 });
    await alignedGutter(page, "a reply landed");
    await card.locator("[data-line-thread-reply-cancel]").click();
    await alignedGutter(page, "Reply closed");
    await L.pressLine(page, 2);
    await page.waitForFunction(() => !document.querySelector(".cm-content [data-line-thread]"), null, { timeout: 6000 });
    await alignedGutter(page, "the thread folded");
    await clear();
    await waitMarked(page, "");
  });

  await run("Open thread in the sheet opens the file at the line with the thread unfolded and scrolled into view, one whose file is gone stands in the dialog", async () => {
    const far = "far.txt";
    const lines = Array.from({ length: 220 }, (_, i) => `far line ${i + 1}`);
    assert(await writeFile(far, `${lines.join("\n")}\n`) === 200, "the long file was not written");
    assert(await post(comments, { path: far, line: 180, text: "far below" }) === 200, "the far comment was not added");
    assert(await post(comments, { path: "gone.txt", line: 3, lineText: "was here", text: "file gone" }) === 200, "the orphan was not added");
    await openSheet(page);
    await sheetCell(page, "far below").click();
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 6000 });
    await pickMenuItem(page, "Open thread");
    await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 6000 });
    const card = L.lineThread(page, "far below");
    await card.waitFor({ timeout: 8000 });
    await sleep(600);
    const place = await card.evaluate((el) => {
      const scroller = el.closest(".cm-scroller").getBoundingClientRect();
      const box = el.getBoundingClientRect();
      return { line: el.closest("[data-line-block]").dataset.lineBlock, inside: box.top >= scroller.top - 1 && box.bottom <= scroller.bottom + 1 };
    });
    assert(place.line === "180" && place.inside, `the far thread does not stand unfolded in view: ${JSON.stringify(place)}`);
    await openSheet(page);
    await sheetCell(page, "file gone").click();
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 6000 });
    await pickMenuItem(page, "Open thread");
    await page.waitForSelector("[data-editor-comment-modal].show", { timeout: 6000 });
    const dialogCard = page.locator("[data-editor-comment-modal] [data-line-thread]");
    await dialogCard.waitFor({ timeout: 6000 });
    // Bootstrap ignores a hide inside the show transition.
    await sleep(500);
    const shown = await page.$eval("[data-editor-comment-modal]", (modal) => ({
      title: modal.querySelector("[data-editor-comment-title]").textContent,
      place: modal.querySelector("[data-editor-comment-place]").textContent,
      quote: modal.querySelector("[data-line-thread-quote]")?.textContent,
      note: modal.querySelector("[data-comment-note-text]").textContent,
    }));
    assert(shown.title === "Line comment" && shown.place === "gone.txt:3" && shown.quote === "was here" && shown.note === "file gone", `the gone file's thread reads ${JSON.stringify(shown)}`);
    assert(await page.locator(".dc-toast", { hasText: /not|could/i }).count() === 0, "opening the gone file toasted");
    await page.keyboard.press("Escape");
    await page.waitForSelector("[data-editor-comment-modal].show", { state: "detached", timeout: 6000 });
    await clear();
  });

  await run("two editor groups keep their own threads, a tab moved to the other group takes its unfolded thread along", async () => {
    await openFile(page);
    assert(await post(comments, { path: file, line: 3, text: "group one" }) === 200, "the first comment was not added");
    assert(await post(comments, { path: "far.txt", line: 2, text: "group two" }) === 200, "the second comment was not added");
    await page.click(`.editor-tab[data-path="far.txt"]`, { button: "right" });
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
    await page.locator(".dc-context-menu .dropdown-item", { has: page.locator(".dc-menu-label-head", { hasText: /^Split right$/ }) }).click();
    await page.waitForFunction(() => document.querySelectorAll(".editor-group").length === 2, null, { timeout: 8000 });
    await page.waitForFunction(() => document.querySelectorAll(".editor-group")[1].querySelector(".cm-content")?.textContent.includes("far line 1"), null, { timeout: 8000 });
    const groupLine = (g, n) => page.locator(".editor-group").nth(g).locator(".cm-lineNumbers .cm-gutterElement:visible").filter({ hasText: new RegExp(`^${n}$`) }).first();
    await page.locator(".editor-group").nth(1).locator(".cm-scroller").evaluate((el) => { el.scrollTop = 0; });
    await sleep(400);
    await groupLine(1, 2).click();
    const two = page.locator(".editor-group").nth(1).locator("[data-line-thread]");
    await two.waitFor({ timeout: 6000 });
    await sleep(400);
    await groupLine(0, 3).click();
    const one = page.locator(".editor-group").nth(0).locator("[data-line-thread]");
    await one.waitFor({ timeout: 6000 });
    assert(await L.seen(one) && await L.seen(two) && (await one.textContent()).includes("group one") && (await two.textContent()).includes("group two"), "the groups do not hold their own threads");
    await page.click(`.editor-tab[data-path="far.txt"]`, { button: "right" });
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
    await page.locator(".dc-context-menu .dropdown-item", { has: page.locator(".dc-menu-label-head", { hasText: /^Move to group 1$/ }) }).click();
    await page.waitForFunction(() => document.querySelectorAll(".editor-group").length === 1, null, { timeout: 8000 });
    const moved = L.lineThread(page, "group two");
    await moved.waitFor({ timeout: 6000 });
    assert(await L.seen(moved), "the moved tab lost its unfolded thread");
    await page.click(`.editor-tab[data-path="far.txt"] .editor-tab-state`);
    await clear();
  });

  await run("phone: the field stays inside the width and in view while the soft keyboard takes half the window", async () => {
    const mp = await mobilePage();
    await openEditor(mp);
    await openFile(mp);
    await L.pressLine(mp, 5, { tap: true });
    const field = L.lineField(mp, 5);
    await field.waitFor({ timeout: 6000 });
    await field.locator("[data-line-comment-text]").tap();
    await mp.keyboard.type("on the phone");
    for (const word of ["second", "third", "fourth"]) {
      await mp.keyboard.press("Enter");
      await mp.keyboard.type(`${word} line`);
    }
    await alignedGutter(mp, "the phone's field grown");
    const size = mp.viewportSize();
    await mp.setViewportSize({ width: size.width, height: Math.round(size.height / 2) });
    await sleep(800);
    const box = await field.locator("[data-line-comment-text]").evaluate((el) => {
      const r = el.getBoundingClientRect();
      const view = window.visualViewport;
      return { top: r.top, bottom: r.bottom, left: r.left, right: r.right, height: view ? view.height : innerHeight, width: innerWidth, focused: document.activeElement === el };
    });
    await mp.setViewportSize(size);
    assert(box.focused && box.top >= 0 && box.bottom <= box.height + 1 && box.left >= 0 && box.right <= box.width + 0.5, `the field leaves the room the keyboard leaves: ${JSON.stringify(box)}`);
    await field.locator("[data-line-comment-cancel]").tap();
    await mp.waitForFunction(() => !document.querySelector(".cm-content [data-line-comment-form]"), null, { timeout: 6000 });
  });
};
