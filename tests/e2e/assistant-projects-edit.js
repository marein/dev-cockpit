const L = require("./lib");
const { assert, BASE, sleep } = L;

// Not a runner of its own: assistant-projects-diff-comments.js runs these
// checks over its project, chromium, against the fake GitLab. The owner edits
// the local notes of a project opened in an assistant's aside in place, the
// thread's first note and its replies (openNoteEdit of @dc/editor-comments over
// the note's own form in line_comment_notes.gohtml, the thread's card in
// Reviews and its copy under a diff's line alike): the pencil turns exactly
// that note's text into its field holding it with Save and Cancel, nothing
// opens under the thread, Escape and Cancel leave the note and the view
// standing, an emptied field is refused under it and Save or Ctrl+Enter writes
// it through the editor's routes, the first note at the place the comments
// hold for it. Its new text shows at once in Reviews, in the copy under the
// diff's line and in the editor that has the thread unfolded. A field shows
// its caret, in a diff too, where CodeMirror hides it in its content, and the
// diff's line numbers stand still while Enter grows a copy. A note of the
// forge offers no pencil. A note being edited keeps its words and focus while
// another device writes a reply or a comment and the view pulls again. On a
// phone's sheet a tap on the pencil, typing, Enter, a paste and a tap on Save
// edit a note inside the width.

module.exports.editChecks = async function editChecks({ page, run, mobilePage, owner, REPO, FILE, PANE, openPage, openChanges, unfold, press, untilDiff, block, rowOf, stored, comment, postJSON }) {
  const reviewsCard = (id) => `${PANE} [data-assistant-project-reviews] [data-line-thread="${id}"]`;
  const copyCard = (line) => `${rowOf(FILE)} [data-line-block="${line}"] [data-line-thread]`;
  // notes reads the notes of the card a selector names by what really shows:
  // the text, the field with its value, focus and box, Save, Cancel and the
  // refusal under it, the pencil, and whether the card shows a reply form.
  const notes = (p, sel) => p.evaluate((sel) => {
    const card = document.querySelector(sel);
    if (!card) return null;
    const shown = (el) => !!el && el.getClientRects().length > 0 && getComputedStyle(el).visibility !== "hidden" && el.getBoundingClientRect().height > 0;
    return {
      reply: shown(card.querySelector("[data-line-thread-reply]")),
      view: shown(document.querySelector("[data-assistant-project-view-body]")),
      notes: [...card.querySelectorAll("[data-comment-note]")].map((n) => {
        const field = n.querySelector("[data-comment-note-field]");
        const parts = [field, n.querySelector("[data-comment-note-save]"), n.querySelector("[data-comment-note-cancel]")].filter(shown).map((el) => el.getBoundingClientRect());
        return {
          text: shown(n.querySelector("[data-comment-note-text]")) ? n.querySelector("[data-comment-note-text]").textContent : null,
          stored: n.querySelector("[data-comment-note-text]").textContent,
          field: shown(field), value: field?.value ?? null, focused: !!field && document.activeElement === field,
          save: shown(n.querySelector("[data-comment-note-save]")), cancel: shown(n.querySelector("[data-comment-note-cancel]")),
          refusal: field?.classList.contains("is-invalid") && shown(n.querySelector(".invalid-feedback")) ? n.querySelector(".invalid-feedback").textContent : "",
          pencil: shown(n.querySelector("[data-comment-note-edit]")),
          left: Math.min(...parts.map((r) => r.left), Infinity), right: Math.max(...parts.map((r) => r.right), 0), inner: window.innerWidth,
        };
      }),
    };
  }, sel);
  const until = async (p, sel, want, what) => {
    let last = null;
    for (let end = Date.now() + 15000; Date.now() < end; await sleep(150)) {
      last = await notes(p, sel);
      if (last && want(last)) return last;
    }
    throw new Error(`${what}: ${JSON.stringify(last)}`);
  };
  const pencil = (sel, n) => `${sel} [data-comment-note]:nth-child(${n + 1}) [data-comment-note-edit]`;
  const field = (sel, n) => `${sel} [data-comment-note]:nth-child(${n + 1}) [data-comment-note-field]`;
  const saveOf = (sel, n) => `${sel} [data-comment-note]:nth-child(${n + 1}) [data-comment-note-save]`;
  const cancelOf = (sel, n) => `${sel} [data-comment-note]:nth-child(${n + 1}) [data-comment-note-cancel]`;
  const openReviews = async (p, tap = false) => {
    const line = `${PANE} [data-assistant-project="${REPO}"] [data-assistant-project-review]`;
    await p.waitForSelector(line, { state: "visible", timeout: 30000 });
    await (tap ? p.tap(line) : p.click(line));
    await p.waitForSelector(`${PANE} [data-assistant-project-view-body="${REPO}"] [data-assistant-project-reviews]`, { state: "visible", timeout: 30000 });
  };
  let id = "";
  let reply = "";
  let editor = null;

  await run("the pencil of a local note edits it in place in Reviews, Escape and Cancel leave it, an emptied one is refused, a note of the forge offers none", async () => {
    await page.setViewportSize({ width: 1360, height: 900 });
    id = await comment(page, 26, "Edit in Reviews");
    reply = (await postJSON(page, `/projects/${REPO}/editor/comments/reply`, { thread: id, text: "Edit the reply\n  indented" }, { json: true })).comment?.replies?.[0]?.id;
    assert(id && reply, "the local thread was not made");
    await openPage(page, `${BASE}/assistants/${owner}`);
    await page.evaluate(() => { window.__noReload = 1; });
    await openReviews(page);
    const card = reviewsCard(id);
    let r = await until(page, card, (x) => x.notes.length === 2, "the local thread is not in Reviews");
    assert(r.notes.every((n) => n.pencil && !n.field), `the local notes offer no pencil: ${JSON.stringify(r)}`);
    const forge = await page.evaluate((pane) => [...document.querySelectorAll(`${pane} [data-assistant-project-reviews] [data-line-thread]:not([data-line-thread-local]) [data-comment-note]`)].map((n) => !!n.querySelector("[data-comment-note-edit], [data-comment-note-form]")), PANE);
    assert(forge.length && forge.every((has) => !has), `a note of the forge offers an edit: ${JSON.stringify(forge)}`);
    await page.click(pencil(card, 1));
    r = await notes(page, card);
    assert(r.notes[1].text === null && r.notes[1].field && r.notes[1].value === "Edit the reply\n  indented" && r.notes[1].save && r.notes[1].cancel && r.notes[1].focused, `the reply's pencil reads ${JSON.stringify(r.notes[1])}`);
    const caret = await page.evaluate(() => getComputedStyle(document.activeElement).caretColor);
    assert(!/^(transparent|rgba\(.*,\s*0\))$/.test(caret), `the reply's field hides its caret: ${caret}`);
    assert(r.notes[0].text === "Edit in Reviews" && !r.notes[0].field && !r.reply, `more than the reply's text changed: ${JSON.stringify(r)}`);
    await page.keyboard.type(" and more");
    await page.keyboard.press("Escape");
    r = await notes(page, card);
    assert(r.view && r.notes[1].text === "Edit the reply\n  indented" && !r.notes[1].field, `Escape did not leave the reply and the view: ${JSON.stringify(r)}`);
    await page.click(pencil(card, 0));
    await page.fill(field(card, 0), " ");
    await page.click(saveOf(card, 0));
    r = await notes(page, card);
    assert(r.notes[0].field && r.notes[0].refusal === "A comment is required.", `an emptied note is not refused at its field: ${JSON.stringify(r.notes[0])}`);
    await page.click(cancelOf(card, 0));
    r = await notes(page, card);
    assert(r.notes[0].text === "Edit in Reviews" && !r.notes[0].field, `Cancel did not leave the note: ${JSON.stringify(r.notes[0])}`);
    const c = (await stored(page)).find((x) => x.id === id);
    assert(c.text === "Edit in Reviews" && c.replies[0].text === "Edit the reply\n  indented", `a cancelled edit stored ${JSON.stringify(c)}`);
    assert(await page.evaluate(() => window.__noReload === 1), "the page reloaded");
  });

  await run("an edit in Reviews keeps its words and focus over a reply from elsewhere, Ctrl+Enter saves it in place, in the editor too, its line and quote kept", async () => {
    editor = await page.context().newPage();
    await editor.goto(`${BASE}/projects/${REPO}/editor?file=${encodeURIComponent(FILE)}&line=26`, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(editor);
    await editor.waitForFunction(() => document.querySelector(".cm-content")?.textContent.includes("app 26"), null, { timeout: 15000 });
    await L.pressLine(editor, 26);
    await L.lineThread(editor, "Edit in Reviews").waitFor({ timeout: 8000 });
    await page.bringToFront();
    const card = reviewsCard(id);
    await page.click(pencil(card, 0));
    await page.keyboard.type(", edited");
    await postJSON(page, `/projects/${REPO}/editor/comments/reply`, { thread: id, text: "From elsewhere" }, { json: true });
    await sleep(1500);
    let r = await notes(page, card);
    assert(r.notes[0].field && r.notes[0].value === "Edit in Reviews, edited" && r.notes[0].focused, `the reply from elsewhere took the edit: ${JSON.stringify(r.notes[0])}`);
    await page.keyboard.press("Control+Enter");
    r = await until(page, card, (x) => x.notes[0].text === "Edit in Reviews, edited" && !x.notes[0].field, "the edit does not show in place");
    const c = (await stored(page)).find((x) => x.id === id);
    assert(c.text === "Edit in Reviews, edited" && c.path === FILE && c.line === 26 && c.lineText === "app 26" && c.replies.length === 2, `the edit is stored as ${JSON.stringify(c)}`);
    await until(page, card, (x) => x.notes.length === 3 && x.notes[2].text === "From elsewhere", "the reply from elsewhere did not join once the edit went");
    await editor.waitForFunction(() => [...document.querySelectorAll(".cm-content [data-comment-note-text]")].some((el) => el.textContent === "Edit in Reviews, edited" && el.getClientRects().length), null, { timeout: 8000 });
  });

  await run("an edit in a diff's copy shows its caret, the line numbers stand still while Enter grows it, it keeps its words over a comment made elsewhere, Save shows it in the copy, Reviews and the editor", async () => {
    await openPage(page, `${BASE}/assistants/${owner}`);
    await openChanges(page);
    await unfold(page);
    await press(page, 26);
    await untilDiff(page, (x) => block(x, 26)?.threads.length === 1, "the thread on 26 did not unfold in the diff");
    const copy = copyCard(26);
    await page.click(pencil(copy, 1));
    let r = await notes(page, copy);
    assert(r.notes[1].field && r.notes[1].value === "Edit the reply\n  indented" && r.notes[1].text === null && !r.reply, `the copy's pencil reads ${JSON.stringify(r)}`);
    const caret = await page.evaluate(() => getComputedStyle(document.activeElement).caretColor);
    assert(!/^(transparent|rgba\(.*,\s*0\))$/.test(caret), `the copy's field hides its caret: ${caret}`);
    await page.evaluate((root) => {
      const cm = document.querySelector(root);
      const off = () => [...cm.querySelectorAll(".cm-content > [data-line-block]")].map((b) => {
        const next = b.nextElementSibling;
        const cell = [...cm.querySelectorAll(".cm-lineNumbers .cm-gutterElement")].find((el) => el.textContent.trim() === String(Number(b.dataset.lineBlock) + 1) && el.getClientRects().length);
        return next?.matches(".cm-line") && cell ? Math.round(cell.getBoundingClientRect().top - next.getBoundingClientRect().top) : 0;
      }).filter((o) => Math.abs(o) > 2);
      window.dcGutterOff = [];
      window.dcGutterWatch = new ResizeObserver(() => { const o = off(); if (o.length) window.dcGutterOff.push(o); });
      for (const b of cm.querySelectorAll(".cm-content > [data-line-block]")) window.dcGutterWatch.observe(b);
    }, `${rowOf(FILE)} [data-assistant-fold] .cm-editor`);
    await page.keyboard.type(" in the diff");
    for (const word of ["and", "two lines"]) {
      await page.keyboard.press("Enter");
      await page.keyboard.type(word);
    }
    await sleep(300);
    const moved = await page.evaluate(() => { window.dcGutterWatch.disconnect(); return window.dcGutterOff; });
    assert(!moved.length, `the diff's line numbers stood off their code while Enter grew the field: ${JSON.stringify(moved.slice(0, 3))}`);
    const edited = "Edit the reply\n  indented in the diff\nand\ntwo lines";
    const made = await comment(page, 28, "Made elsewhere");
    await untilDiff(page, (x) => x.marks.split(",").includes("28o"), "the comment made elsewhere did not mark its line");
    r = await notes(page, copy);
    assert(r.notes[1].field && r.notes[1].value === edited && r.notes[1].focused, `the comment made elsewhere took the copy's edit: ${JSON.stringify(r.notes[1])}`);
    await page.click(saveOf(copy, 1));
    await until(page, copy, (x) => x.notes[1].text === edited, "the copy does not show the edit");
    const shown = await page.evaluate((sel) => document.querySelector(sel).querySelectorAll("[data-comment-note-text]")[1].textContent, reviewsCard(id));
    assert(shown === edited, `Reviews shows ${JSON.stringify(shown)}`);
    const c = (await stored(page)).find((x) => x.id === id);
    assert(c.replies.find((x) => x.id === reply)?.text === edited, `the copy's edit is stored as ${JSON.stringify(c.replies)}`);
    await editor.waitForFunction((t) => [...document.querySelectorAll(".cm-content [data-comment-note-text]")].some((el) => el.textContent === t && el.getClientRects().length), edited, { timeout: 8000 });
    await postJSON(page, `/projects/${REPO}/editor/comments/delete`, { ids: [made] }, { json: true });
  });

  await run("on a phone's sheet a tap on the pencil, typing, Enter, a paste and a tap on Save edit a note inside the width", async () => {
    const mp = await mobilePage();
    await openPage(mp, `${BASE}/assistants/${owner}`);
    await mp.tap("[data-assistant-watching-button]");
    await openReviews(mp, true);
    const card = reviewsCard(id);
    await until(mp, card, (x) => x.notes.length === 3, "the phone's Reviews lack the thread");
    await mp.tap(pencil(card, 2));
    await mp.keyboard.press("End");
    await mp.keyboard.type(" on the phone");
    await mp.keyboard.press("Enter");
    await mp.keyboard.insertText("pasted");
    const r = await notes(mp, card);
    assert(r.notes[2].field && r.notes[2].value === "From elsewhere on the phone\npasted" && r.notes[2].left >= 0 && r.notes[2].right <= r.notes[2].inner, `the phone's edit reads ${JSON.stringify(r.notes[2])}`);
    await mp.tap(saveOf(card, 2));
    await until(mp, card, (x) => x.notes[2].text === "From elsewhere on the phone\npasted", "the phone's edit does not show in place");
    const c = (await stored(page)).find((x) => x.id === id);
    assert(c.replies[1].text === "From elsewhere on the phone\npasted", `the phone's edit is stored as ${JSON.stringify(c.replies)}`);
    await editor?.close().catch(() => {});
    await postJSON(page, `/projects/${REPO}/editor/comments/delete`, { ids: [id] }, { json: true });
  });
};
