const L = require("./lib");
const { assert, sleep } = L;
const { alignedGutter } = require("./editor-comments-inline");

// Not a runner of its own: editor-comments.js runs these checks in every
// engine over its scratch project. The pencil of a note edits it in place
// (openNoteEdit of @dc/editor-comments over the note's own form in
// line_comment_notes.gohtml): exactly that note's text turns into its field
// holding the text, sized to it, with Save and Cancel right there, the reply
// form and the other notes as they were. Escape and Cancel leave the note as
// it was and store nothing, Escape giving the keys back to the code; Save and
// Ctrl+Enter store it, the first note through .../comments with its line and
// quote as the buffer holds them, a reply through .../comments/reply by its
// id, and show the new text in place at once; an emptied field is refused
// under it. Keys typed there never reach the editor's bindings. A note being
// edited keeps its words, caret and focus while another device replies, the
// code above is edited and saved and the disk changes. In a file shorter than
// the pane the line numbers and the marks with them stand beside their code
// from the first frame a reply or a note's field opens or closes, CodeMirror
// taking the block's own height until it measured, at every height Enter
// gives a note's, a reply's or a new comment's field and a landing reply gives
// a card, and once another device moved the thread. Every inline field shows
// its caret, which CodeMirror hides in its content outside iOS; a block stands
// no wider than 48rem on a large screen and takes the visible width on a
// tablet and a phone. On a 390px phone a tap on the pencil, typing,
// Enter, a paste and a tap on Save edit a reply inside the width, and the
// field stands in view while the soft keyboard takes half the window.

module.exports.editChecks = async function editChecks({ page, run, mobilePage, scratch, file, saveFile, openEditor, openFile, waitMarked, getComments }) {
  const comments = `/projects/${encodeURIComponent(scratch)}/editor/comments`;
  const post = (p, path, body) => p.evaluate(([u, b]) => fetch(u, {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]').content, Accept: "application/json" },
    body: JSON.stringify(b),
  }).then(async (r) => ({ status: r.status, body: await r.json().catch(() => ({})) })), [path, body]);
  const writeFile = (content) => page.evaluate(([p, f, c]) => fetch(p, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded", "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]').content, Accept: "application/json" },
    body: new URLSearchParams({ path: f, content: c }).toString(),
  }).then((r) => r.status), [`/projects/${encodeURIComponent(scratch)}/editor/file`, file, content]);
  const clear = () => post(page, `${comments}/delete`, { all: true });
  const docText = () => page.evaluate(() => [...document.querySelectorAll(".editor-group-focused .cm-content > .cm-line")].map((l) => l.textContent).join("\n"));
  const stored = async (id) => (await getComments(page)).comments.find((c) => c.id === id);
  const noteOf = (card, n) => card.locator("[data-comment-note]").nth(n);
  // readNotes reads every note of a card by what really shows: its text, its
  // field with value, height, focus and caret, Save, Cancel and the refusal
  // under it, and whether anything else of the card holds a field.
  const readNotes = (card) => card.evaluate((c) => {
    const shown = (el) => !!el && el.getClientRects().length > 0 && getComputedStyle(el).visibility !== "hidden" && el.getBoundingClientRect().height > 0;
    const notes = [...c.querySelectorAll("[data-comment-note]")].map((n) => {
      const text = n.querySelector("[data-comment-note-text]");
      const field = n.querySelector("[data-comment-note-field]");
      const box = n.getBoundingClientRect();
      const inside = [field, n.querySelector("[data-comment-note-save]"), n.querySelector("[data-comment-note-cancel]")].filter(shown).every((el) => {
        const r = el.getBoundingClientRect();
        return r.left >= box.left - 0.5 && r.right <= box.right + 0.5 && r.top >= box.top - 0.5 && r.bottom <= box.bottom + 0.5;
      });
      return {
        text: shown(text) ? text.textContent : null,
        field: shown(field), value: field?.value ?? null, height: shown(field) ? Math.round(field.getBoundingClientRect().height) : 0,
        focused: !!field && document.activeElement === field, caret: field?.selectionStart, inside,
        save: shown(n.querySelector("[data-comment-note-save]")) ? n.querySelector("[data-comment-note-save]").textContent.trim() : "",
        cancel: shown(n.querySelector("[data-comment-note-cancel]")),
        refusal: field?.classList.contains("is-invalid") && shown(n.querySelector(".invalid-feedback")) ? n.querySelector(".invalid-feedback").textContent : "",
      };
    });
    const others = [...c.querySelectorAll("textarea")].filter((t) => !t.matches("[data-comment-note-field]") && shown(t)).length;
    return { notes, others, reply: shown(c.querySelector("[data-line-thread-reply]")), code: !!document.activeElement?.matches(".cm-content") };
  });
  const textHeight = (card, n) => noteOf(card, n).locator("[data-comment-note-text]").evaluate((el) => Math.round(el.getBoundingClientRect().height));
  // firstFrame clicks what selector names and reads, at the first frame that
  // shows it, the lines whose number stands off its code.
  const firstFrame = (selector) => page.evaluate(async (sel) => {
    document.querySelector(sel).click();
    await new Promise((r) => requestAnimationFrame(r));
    await new Promise((r) => requestAnimationFrame(r));
    const group = document.querySelector(".editor-group-focused") || document;
    const numbers = new Map();
    for (const el of group.querySelectorAll(".cm-lineNumbers .cm-gutterElement")) {
      if (/^\d+$/.test(el.textContent) && el.style.visibility !== "hidden" && !numbers.has(Number(el.textContent))) numbers.set(Number(el.textContent), el);
    }
    return [...group.querySelectorAll(".cm-content > .cm-line")].map((line, i) => ({ line: i + 1, off: numbers.get(i + 1) ? Math.round(numbers.get(i + 1).getBoundingClientRect().top - line.getBoundingClientRect().top) : "none" }))
      .filter((l) => l.off === "none" || Math.abs(l.off) > 1);
  }, selector);
  // watchGutter records, each time a block of the focused group shows a new
  // height, the blocks whose next line number stands off the code line
  // after them, as the browser paints it: its observer runs after the one of
  // @dc/line-blocks, which hands the view the new height. stillGutter stops
  // it and answers what it recorded.
  const watchGutter = () => page.evaluate(() => {
    const group = document.querySelector(".editor-group-focused") || document;
    const off = () => [...group.querySelectorAll(".cm-content > [data-line-block]")].map((block) => {
      const next = block.nextElementSibling;
      const cell = [...group.querySelectorAll(".cm-lineNumbers .cm-gutterElement")].find((el) => el.textContent === String(Number(block.dataset.lineBlock) + 1) && el.style.visibility !== "hidden");
      return next && cell ? { line: Number(block.dataset.lineBlock) + 1, off: Math.round(cell.getBoundingClientRect().top - next.getBoundingClientRect().top) } : null;
    }).filter((l) => l && Math.abs(l.off) > 1);
    window.dcGutterOff = [];
    window.dcGutterWatch?.disconnect();
    window.dcGutterWatch = new ResizeObserver(() => { const lines = off(); if (lines.length) window.dcGutterOff.push(lines); });
    for (const block of group.querySelectorAll(".cm-content > [data-line-block]")) window.dcGutterWatch.observe(block);
  });
  const stillGutter = async (what) => {
    const moved = await page.evaluate(() => { window.dcGutterWatch?.disconnect(); return window.dcGutterOff || []; });
    assert(!moved.length, `${what}: line numbers stood off their code ${JSON.stringify(moved.slice(0, 3))}`);
  };
  const typeLines = async (p, words) => {
    for (const word of words) {
      await p.keyboard.press("Enter");
      await p.keyboard.type(word);
    }
  };
  // caretOf reads the caret colour of the focused field, transparent where
  // the field hides its caret.
  const caretOf = (p = page) => p.evaluate(() => ({ field: document.activeElement?.tagName, caret: getComputedStyle(document.activeElement).caretColor }));
  const hidden = (c) => c.field !== "TEXTAREA" || /^(transparent|rgba\(.*,\s*0\))$/.test(c.caret);
  let id = "";
  let reply = "";
  const unfold = async (text, p = page, tap = false, line = 2) => {
    const card = L.lineThread(p, text);
    if (!(await L.seen(card))) await L.pressLine(p, line, { tap });
    await card.waitFor({ timeout: 6000 });
    return card;
  };

  await run("the pencil turns exactly its note's text into its field with Save and Cancel, Escape and Cancel leave it, an emptied one is refused", async () => {
    await clear();
    assert(await saveFile("one\ntwo\nthree\nfour\nfive\n") === 200, "the file was not reset");
    await openEditor(page);
    await openFile(page);
    const made = await post(page, comments, { path: file, line: 2, text: "edit me" });
    id = made.body.comment?.id;
    reply = (await post(page, `${comments}/reply`, { thread: id, text: "first reply\n  on two lines" })).body.comment?.replies?.[0]?.id;
    assert(id && reply, `the thread was not made: ${JSON.stringify(made)}`);
    await waitMarked(page, "2");
    const card = await unfold("edit me");
    const tall = await textHeight(card, 1);
    const before = await docText();
    await noteOf(card, 1).locator('button[aria-label="Edit reply"]').click();
    let r = await readNotes(card);
    const edit = r.notes[1];
    assert(edit.text === null && edit.field && edit.value === "first reply\n  on two lines" && edit.save === "Save" && edit.cancel && edit.inside, `the reply's pencil reads ${JSON.stringify(edit)}`);
    assert(edit.focused && edit.caret === edit.value.length, `the field does not take the focus at the end of its text: ${JSON.stringify(edit)}`);
    assert(edit.height >= tall && edit.height <= tall + 16, `the field is ${edit.height}px for a text of ${tall}px`);
    assert(r.notes[0].text === "edit me" && !r.notes[0].field && !r.reply && r.others === 0, `more than the reply's text changed: ${JSON.stringify(r)}`);
    for (const key of ["Alt+ArrowUp", "Control+d", "Control+z", "Tab"]) {
      await noteOf(card, 1).locator("[data-comment-note-field]").focus();
      await page.keyboard.press(key);
    }
    await noteOf(card, 1).locator("[data-comment-note-field]").focus();
    await page.keyboard.press("End");
    await page.keyboard.type(" and so");
    assert(await docText() === before, `a key typed into the note's field reached the code: ${JSON.stringify(await docText())}`);
    await page.keyboard.press("Escape");
    r = await readNotes(card);
    assert(r.notes[1].text === "first reply\n  on two lines" && !r.notes[1].field && r.code, `Escape did not leave the reply as it was with the keys in the code: ${JSON.stringify(r)}`);
    await noteOf(card, 1).locator('button[aria-label="Edit reply"]').click();
    r = await readNotes(card);
    assert(r.notes[1].value === "first reply\n  on two lines", `the pencil again holds ${JSON.stringify(r.notes[1].value)}`);
    await noteOf(card, 1).locator("[data-comment-note-field]").fill("  ");
    await noteOf(card, 1).locator("[data-comment-note-save]").click();
    r = await readNotes(card);
    assert(r.notes[1].field && r.notes[1].refusal === "A reply is required." && r.notes[1].focused, `an emptied reply is not refused at its field: ${JSON.stringify(r.notes[1])}`);
    await noteOf(card, 1).locator("[data-comment-note-cancel]").click();
    r = await readNotes(card);
    assert(r.notes[1].text === "first reply\n  on two lines" && !r.notes[1].field, `Cancel did not leave the reply as it was: ${JSON.stringify(r.notes[1])}`);
    await noteOf(card, 0).locator('button[aria-label="Edit comment"]').click();
    await noteOf(card, 0).locator("[data-comment-note-field]").fill("");
    await page.keyboard.press("Control+Enter");
    r = await readNotes(card);
    assert(r.notes[0].refusal === "A comment is required.", `an emptied comment is not refused at its field: ${JSON.stringify(r.notes[0])}`);
    await page.keyboard.press("Escape");
    const c = await stored(id);
    assert(c.text === "edit me" && c.replies[0].text === "first reply\n  on two lines", `a cancelled edit stored ${JSON.stringify(c)}`);
  });

  await run("Ctrl+Enter and Save store the edit and show the new text in place, the first note keeping its line and quote", async () => {
    const card = await unfold("edit me");
    await noteOf(card, 0).locator('button[aria-label="Edit comment"]').click();
    await page.keyboard.type(", edited");
    await page.keyboard.press("Control+Enter");
    const edited = L.lineThread(page, "edit me, edited");
    await page.waitForFunction(() => [...document.querySelectorAll(".cm-content [data-comment-note-text]")].some((el) => el.textContent === "edit me, edited" && el.getClientRects().length), null, { timeout: 6000 });
    let r = await readNotes(edited);
    assert(r.notes[0].text === "edit me, edited" && !r.notes[0].field && !r.reply, `the saved note reads ${JSON.stringify(r)}`);
    let c = await stored(id);
    assert(c.text === "edit me, edited" && c.line === 2 && c.lineText === "two" && c.replies.length === 1, `the edit is stored as ${JSON.stringify(c)}`);
    await noteOf(edited, 1).locator('button[aria-label="Edit reply"]').click();
    await page.keyboard.press("Enter");
    await page.keyboard.insertText("a pasted line");
    await noteOf(edited, 1).locator("[data-comment-note-save]").click();
    await page.waitForFunction(() => [...document.querySelectorAll(".cm-content [data-comment-note-text]")].some((el) => el.textContent.endsWith("\na pasted line") && el.getClientRects().length), null, { timeout: 6000 });
    r = await readNotes(edited);
    assert(r.notes[1].text === "first reply\n  on two lines\na pasted line" && !r.notes[1].field, `the saved reply reads ${JSON.stringify(r.notes[1])}`);
    c = await stored(id);
    assert(c.replies.length === 1 && c.replies[0].id === reply && c.replies[0].text === "first reply\n  on two lines\na pasted line", `the reply's edit is stored as ${JSON.stringify(c.replies)}`);
  });

  await run("a note being edited keeps its words, caret and focus while another device replies, the code above is edited and saved and the disk changes", async () => {
    const card = await unfold("edit me, edited");
    await noteOf(card, 0).locator('button[aria-label="Edit comment"]').click();
    await page.keyboard.type(" again");
    const kept = async (what, line) => {
      const r = await readNotes(card);
      const at = await card.evaluate((el) => el.closest("[data-line-block]")?.dataset.lineBlock);
      assert(r.notes[0].field && r.notes[0].value === "edit me, edited again" && at === line, `${what} took the edit: ${JSON.stringify({ note: r.notes[0], at })}`);
      return r.notes[0];
    };
    assert((await post(page, `${comments}/reply`, { thread: id, text: "from the phone" })).status === 200, "the other device's reply was not posted");
    await page.waitForFunction(() => [...document.querySelectorAll(".cm-content [data-comment-note-text]")].some((el) => el.textContent === "from the phone"), null, { timeout: 8000 });
    const typing = await kept("a reply from another device", "2");
    assert(typing.focused && typing.caret === typing.value.length, `the reply from another device took the focus or caret: ${JSON.stringify(typing)}`);
    await page.locator(".cm-content > .cm-line").first().click();
    await page.keyboard.press("Control+Home");
    await page.keyboard.type("zero");
    await page.keyboard.press("Enter");
    await waitMarked(page, "3");
    await kept("an edit above", "3");
    await page.keyboard.press("Control+s");
    await page.waitForFunction((f) => !document.querySelector(`.editor-tab[data-path="${f}"]`)?.classList.contains("dirty"), file, { timeout: 8000 });
    await sleep(800);
    await kept("the save", "3");
    await noteOf(card, 0).locator("[data-comment-note-field]").click();
    await page.keyboard.press("End");
    assert(await writeFile("new head\nzero\none\ntwo\nthree\nfour\nfive\n") === 200, "the disk was not written");
    await page.waitForFunction(() => document.querySelector(".editor-group-focused .cm-content > .cm-line")?.textContent === "new head", null, { timeout: 15000 });
    await waitMarked(page, "4");
    await sleep(600);
    const after = await kept("the disk's change", "4");
    assert(after.focused && after.caret === after.value.length, `the disk's change took the focus or caret: ${JSON.stringify(after)}`);
    await page.keyboard.press("Control+Enter");
    await L.lineThread(page, "edit me, edited again").waitFor({ timeout: 6000 });
    const c = await stored(id);
    assert(c.text === "edit me, edited again" && c.line === 4 && c.lineText === "two" && c.replies.length === 2, `the edit is stored as ${JSON.stringify(c)}`);
  });

  await run("in a file shorter than the pane the line numbers stand still beside their code as a field opens and closes, Enter grows a note's, a reply's and a new comment's field, a reply lands and the thread moves", async () => {
    await clear();
    assert(await saveFile("one\ntwo\nthree\nfour\nfive\n") === 200, "the file was not reset");
    await page.waitForFunction(() => document.querySelector(".editor-group-focused .cm-content > .cm-line")?.textContent === "one", null, { timeout: 15000 });
    const made = await post(page, comments, { path: file, line: 2, text: "short file" });
    await post(page, `${comments}/reply`, { thread: made.body.comment.id, text: "a reply\nof two lines" });
    await waitMarked(page, "2");
    const card = await unfold("short file");
    await alignedGutter(page, "the thread unfolded");
    const sel = '.cm-content [data-line-thread] [data-comment-note]';
    for (const [what, click] of [["Reply opening", ".cm-content [data-line-thread] [data-line-thread-reply-open]"], ["Reply cancelled", ".cm-content [data-line-thread] [data-line-thread-reply-cancel]"],
      ["the pencil", `${sel}:nth-child(2) [data-comment-note-edit]`], ["Cancel", `${sel}:nth-child(2) [data-comment-note-cancel]`], ["the pencil again", `${sel}:nth-child(2) [data-comment-note-edit]`]]) {
      const off = await firstFrame(click);
      assert(!off.length, `${what}: line numbers stand off their code at the first frame ${JSON.stringify(off)}`);
    }
    await watchGutter();
    await typeLines(page, ["three", "four", "five"]);
    assert((await post(page, `${comments}/reply`, { thread: made.body.comment.id, text: "landing\nin two lines" })).status === 200, "the other device's reply was not posted");
    await page.waitForFunction(() => [...document.querySelectorAll(".cm-content [data-comment-note-text]")].some((el) => el.textContent === "landing\nin two lines"), null, { timeout: 8000 });
    await sleep(300);
    await stillGutter("Enter in a note's field and a reply landing");
    await page.click(`${sel}:nth-child(2) [data-comment-note-save]`);
    await page.waitForFunction(() => [...document.querySelectorAll(".cm-content [data-comment-note-text]")].some((el) => el.textContent.endsWith("five")), null, { timeout: 6000 });
    await alignedGutter(page, "the edit saved");
    await card.locator("[data-line-thread-reply-open]").click();
    await watchGutter();
    await page.keyboard.type("growing reply");
    await typeLines(page, ["two", "three"]);
    await sleep(300);
    await stillGutter("Enter in the reply's field");
    await card.locator("[data-line-thread-reply-cancel]").click();
    await L.pressLine(page, 4);
    const field = L.lineField(page, 4);
    await field.waitFor({ timeout: 6000 });
    await field.locator("[data-line-comment-text]").click();
    await alignedGutter(page, "a new comment's field opened");
    await watchGutter();
    await page.keyboard.type("growing comment");
    await typeLines(page, ["two", "three"]);
    await sleep(300);
    await stillGutter("Enter in a new comment's field");
    await field.locator("[data-line-comment-text]").fill("");
    await field.locator("[data-line-comment-cancel]").click();
    assert((await post(page, `${comments}/move`, { path: file, comments: [{ id: made.body.comment.id, line: 4, lineText: "four" }] })).status === 200, "the move was not posted");
    await waitMarked(page, "4");
    await card.evaluate((el) => new Promise((resolve) => { const at = () => (el.closest("[data-line-block]")?.dataset.lineBlock === "4" ? resolve() : requestAnimationFrame(at)); at(); }));
    await alignedGutter(page, "the thread moved by another device");
    assert(await L.seen(card), "the thread folded");
  });

  await run("every inline field shows its caret, a block stands no wider than 48rem on a large screen and takes the visible width on a tablet", async () => {
    const card = L.lineThread(page, "short file");
    if (!(await L.seen(card))) await L.pressLine(page, 4);
    await card.waitFor({ timeout: 6000 });
    const carets = {};
    await card.locator("[data-line-thread-reply-open]").click();
    await card.locator("[data-line-thread-reply-text]").click();
    carets.reply = await caretOf();
    await card.locator("[data-line-thread-reply-cancel]").click();
    await card.locator("[data-comment-note]").nth(0).locator("[data-comment-note-edit]").click();
    carets.note = await caretOf();
    await card.locator("[data-comment-note]").nth(0).locator("[data-comment-note-cancel]").click();
    await L.pressLine(page, 1);
    const field = L.lineField(page, 1);
    await field.waitFor({ timeout: 6000 });
    await field.locator("[data-line-comment-text]").click();
    carets.comment = await caretOf();
    assert(Object.values(carets).every((c) => !hidden(c)), `an inline field hides its caret: ${JSON.stringify(carets)}`);
    const widths = () => page.evaluate(() => [...document.querySelectorAll(".editor-group-focused .cm-content > [data-line-block]")].map((block) => {
      const scroller = block.closest(".cm-scroller");
      const gutters = scroller.querySelector(".cm-gutters");
      return { width: Math.round(block.getBoundingClientRect().width), room: scroller.clientWidth - (gutters ? gutters.offsetWidth : 0), rem: parseFloat(getComputedStyle(document.documentElement).fontSize) };
    }));
    const size = page.viewportSize();
    await page.setViewportSize({ width: 1600, height: 900 });
    await sleep(500);
    const large = await widths();
    assert(large.length === 2 && large.every((b) => b.room > 48 * b.rem && Math.abs(b.width - 48 * b.rem) <= 1), `on a large screen the blocks are ${JSON.stringify(large)}`);
    await page.setViewportSize({ width: 1180, height: 820 });
    await sleep(500);
    const tablet = await widths();
    assert(tablet.length === 2 && tablet.every((b) => b.room > 48 * b.rem && Math.abs(b.width - b.room) <= 1), `on a tablet the blocks are ${JSON.stringify(tablet)}`);
    await page.setViewportSize(size);
    await field.locator("[data-line-comment-cancel]").click();
  });

  await run("phone: a tap on the pencil, typing, Enter, a paste and a tap on Save edit a reply inside the width and in view of the soft keyboard", async () => {
    const mp = await mobilePage();
    await openEditor(mp);
    await openFile(mp);
    const card = await unfold("short file", mp, true, 4);
    await noteOf(card, 1).locator('button[aria-label="Edit reply"]').tap();
    const field = noteOf(card, 1).locator("[data-comment-note-field]");
    await field.waitFor({ timeout: 6000 });
    const caret = await caretOf(mp);
    assert(!hidden(caret), `the phone's note field hides its caret: ${JSON.stringify(caret)}`);
    const room = await card.evaluate((el) => {
      const block = el.closest("[data-line-block]");
      const scroller = block.closest(".cm-scroller");
      return { width: Math.round(block.getBoundingClientRect().width), room: scroller.clientWidth - scroller.querySelector(".cm-gutters").offsetWidth };
    });
    assert(Math.abs(room.width - room.room) <= 1, `the phone's thread does not take the visible width: ${JSON.stringify(room)}`);
    await mp.keyboard.press("End");
    await mp.keyboard.type(" typed");
    await mp.keyboard.press("Enter");
    await mp.keyboard.insertText("pasted on the phone");
    const size = mp.viewportSize();
    await mp.setViewportSize({ width: size.width, height: Math.round(size.height / 2) });
    await sleep(800);
    const box = await field.evaluate((el) => {
      const r = el.getBoundingClientRect();
      const save = el.form.querySelector("[data-comment-note-save]").getBoundingClientRect();
      const view = window.visualViewport;
      return { top: r.top, bottom: r.bottom, left: r.left, right: Math.max(r.right, save.right), height: view ? view.height : innerHeight, width: innerWidth, focused: document.activeElement === el };
    });
    await mp.setViewportSize(size);
    assert(box.focused && box.top >= 0 && box.bottom <= box.height + 1 && box.left >= 0 && box.right <= box.width + 0.5, `the note's field leaves the room the keyboard leaves: ${JSON.stringify(box)}`);
    await mp.evaluate(() => document.querySelector(".editor-group-focused .cm-scroller")?.scrollBy(0, 40));
    await noteOf(card, 1).locator("[data-comment-note-save]").tap();
    await mp.waitForFunction(() => [...document.querySelectorAll(".cm-content [data-comment-note-text]")].some((el) => el.textContent === "a reply\nof two lines\nthree\nfour\nfive typed\npasted on the phone" && el.getClientRects().length), null, { timeout: 6000 });
    const c = (await getComments(mp)).comments.find((x) => x.text === "short file");
    assert(c.replies[0].text === "a reply\nof two lines\nthree\nfour\nfive typed\npasted on the phone", `the phone's edit is stored as ${JSON.stringify(c.replies)}`);
    await clear();
    await waitMarked(page, "");
  });
};
