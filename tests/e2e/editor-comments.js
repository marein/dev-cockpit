const L = require("./lib");
const { assert, sleep, BASE } = L;
const { guestChecks } = require("./editor-comments-guest");
const { threadChecks } = require("./editor-comments-threads");
const { inlineChecks } = require("./editor-comments-inline");
const { editChecks } = require("./editor-comments-edit");

// Line comments in the editor: notes pinned to single lines. Routes: GET
// /projects/:name/editor/comments (the project's notes, the project comments
// first, then by file and line; ?path= narrows for the CLI and leaves the
// project comments out), POST .../editor/comments (upsert one: without an id
// a new note, with one an edit, path change included; a save without a quote
// gets the code line read from the disk; project: true without path and line
// is a project comment, a thread on the whole project), POST
// .../editor/comments/delete ({ids}, {paths} or {all}), POST
// .../editor/comments/move (one file's lines after the buffer edited around
// them, posted by the save alone). State lives per project in
// <state-dir>/line-comments/<project>.json, published as the bare
// "linecomments" event (project named; the connect snapshot carries it bare),
// every open editor pulls the list itself. Client: no gutter column of its
// own; a commented line tints its whole gutter row (.cm-comment-line via
// gutterLineClass, so the tint spans the line numbers AND the gutters beside
// them, the fold gutter included). A plain click anywhere in the gutter beside
// a line folds the line's threads in or out under it, on a bare line it opens
// the field of a new comment there (@dc/editor-threads). The line's menu
// opens on a right click and on a touch long press (wireRowMenus), and holds
// in this order: add the comment or open its thread, Delete comment on a
// commented line, Copy path:line, and the blame toggle (only in a git
// repository, blameMenuItem answers null without one, so this runner's
// scratch project shows a bare line's menu with two entries). The field
// (data-line-comment-form) stands under its line: textarea with a dimmed
// form-hint under it, Cancel, Add, an empty add marks the field is-invalid,
// Ctrl/Cmd+Enter saves like the commit message, and the new thread unfolds
// there. A thread unfolds as the server's card (data-line-thread, GET
// .../comments/thread): the conversation, a pencil on each note to edit it in
// place, Reply with the field and the resolve switch, Resolve or Reopen, the
// threads' checks standing in editor-comments-threads.js, what the inline
// threads keep through edits, saves, the disk and the keys in
// editor-comments-inline.js and the edit in place in editor-comments-edit.js;
// every delete lives in the menus and the card's
// bins behind a swal confirm. Ctrl+Alt+C opens the field on the cursor line
// or the reply of its thread, Ctrl+Shift+C toggles the Line comments sheet, and the menu badges
// (Line comments, Git) hide at zero instead of standing as an empty pill. The
// sheet follows the git history's shape: one cell per comment, the sheet's
// own row, in a row-deck grid (col-12 col-lg-6, two per line from lg up), the
// whole cell is the control and a click on it opens the app's menu over it (Go
// to line jumps there, Open thread opens the thread, Delete is the danger
// entry), anchored at the click or at the cell when Enter got there; the sheet
// stands behind that menu, Open thread closes it for the thread unfolded
// under its line. The head actions close the sheet themselves: Copy
// as text (built by GET .../comments/text: heading "Line comments in
// <project>:", then an entry per comment, path:line and the code line in its
// head, every line of the comment behind "  > " as written, a line break or a
// control character in path, quote and note shown as its escape, so no line
// reads as another entry) and Delete all
// comments behind a confirm. Path and quote are stored as sent, a Windows
// line's carriage return cut from the quote. A guest's entries and the attacks
// on them are editor-comments-guest.js's. Gotchas:
//   - comments follow the buffer through CodeMirror's position mapping while
//     the tab is open, as a purely local overlay: no move request leaves the
//     client while the buffer is dirty, the save posts the file's anchors in
//     one move and pulls the reconciled answer, and a discard only drops the
//     overlay. A comment another device pins meanwhile arrives mapped through
//     the overlay (tab.commentChanges) onto the locally shifted line.
//   - the lineNumbers gutter holds an invisible spacer cell carrying a line
//     number as text, so cell lookups filter on visibility.
//   - a click right after a menu closed is swallowed (menuJustClosed, 350ms),
//     so the checks wait a beat between two gutter menus.
//   - the quote is the anchor: every read of the list judges the stored
//     quotes against the files (reconcileLineComments) — a quote standing in
//     its file exactly once rebinds in the same read and persists, everything
//     else reads as outdated at its last known line (orange struck-through
//     number, the "Outdated · was:" line in the sheet cell, "(outdated)" in
//     the text's head), and delete {outdated:true} takes exactly those,
//     which is what the CLI's --outdated rides on. A last known line past
//     the file's end paints no gutter mark at all, the sheet is the only
//     place then, like a deleted file.
//   - the save's quote is the sender's buffer line, stored untouched (a note
//     born in a dirty buffer reads outdated to other readers until the file
//     is saved); only a request without the field, the CLI's add, is quoted
//     from the disk. A rename or tree move migrates the notes' paths on the
//     server (lineComments.Rename), the client re-posts nothing.

const scratch = `zztc-cmt-${Math.random().toString(36).slice(2, 8)}`;
const FILE = "notes.txt";
const CONTENT = "one\ntwo\nthree\nfour\nfive\n";

const getComments = (target) => target.evaluate((p) => fetch(p, { headers: { Accept: "application/json" } }).then((r) => r.json()), `/projects/${encodeURIComponent(scratch)}/editor/comments`);

async function openEditor(target) {
  await target.goto(`${BASE}/projects/${encodeURIComponent(scratch)}/editor`, { waitUntil: "domcontentloaded" });
  await L.dismissUpdate(target);
  await target.waitForSelector("[data-editor-tree]", { timeout: 15000 });
}

async function openFile(target, probe = "one") {
  const open = await target.evaluate(([f, p]) => document.querySelector(`.editor-tab[data-path="${f}"]`) && document.querySelector(".editor-group-focused .cm-content")?.textContent.includes(p), [FILE, probe]);
  const row = target.locator(`.editor-file[data-path="${FILE}"]`);
  if (!open) {
    await row.waitFor({ state: "visible", timeout: 15000 });
    await row.click();
  }
  await target.waitForSelector(".cm-lineNumbers", { state: "attached", timeout: 15000 });
  await target.waitForFunction((p) => document.querySelector(".cm-content")?.textContent.includes(p), probe, { timeout: 15000 });
}

// codeLine is the first line of the code, a click there never lands in a
// thread standing under a line.
const codeLine = (target) => target.locator(".cm-content > .cm-line").first();

const lineCell = (target, n) => target
  .locator(".cm-lineNumbers .cm-gutterElement:visible")
  .filter({ hasText: new RegExp(`^${n}$`) })
  .first();

async function openLineMenu(target, n) {
  await sleep(400);
  await lineCell(target, n).click({ button: "right" });
  await target.waitForSelector(".dc-context-menu", { state: "visible", timeout: 6000 });
}

// addInline writes a note into the field the cursor line's Ctrl+Alt+C opens
// and saves it with Ctrl+Enter, the thread then unfolding under its line.
async function addInline(target, line, text) {
  await target.keyboard.press("Control+Alt+C");
  const field = L.lineField(target, line);
  await field.waitFor({ timeout: 6000 });
  await target.waitForFunction(() => document.activeElement?.matches("[data-line-comment-text]"), null, { timeout: 6000 });
  await target.keyboard.insertText(text);
  await target.keyboard.press("Control+Enter");
  await L.lineThread(target, text).waitFor({ timeout: 6000 });
  assert(await field.count() === 0, "the field stands after its add");
}

const menuLabels = (target) => target.$$eval(".dc-context-menu .dropdown-item", (els) => els.map((e) => (e.querySelector(".dc-menu-label-head") || e).textContent.trim()));

async function pickMenuItem(target, label) {
  await target.locator(".dc-context-menu .dropdown-item", { hasText: label }).click();
}

// The line numbers of the commented lines, in document order.
const waitMarked = (target, want) => target.waitForFunction(
  (w) => [...document.querySelectorAll(".cm-lineNumbers .cm-comment-line")].map((el) => el.textContent.trim()).join(",") === w,
  want,
  { timeout: 8000 },
);

async function confirmDialogGone(target) {
  await target.waitForSelector(".swal2-container", { state: "detached", timeout: 6000 });
}

// The comment editor is a Bootstrap modal (the delete confirms stay swal).
// Bootstrap ignores a hide() that lands inside the ~150ms show transition,
// so the shown helper waits it out before any check clicks a footer button.
async function commentDialogShown(target) {
  await target.waitForSelector("[data-editor-comment-modal].show", { timeout: 6000 });
  await sleep(500);
}

async function commentDialogGone(target) {
  await target.waitForSelector("[data-editor-comment-modal].show", { state: "detached", timeout: 6000 });
}

const badge = (target) => target.locator("[data-editor-comments-count]").evaluate((el) => el.textContent.trim());
const badgeHidden = (target) => target.locator("[data-editor-comments-count]").evaluate((el) => getComputedStyle(el).display === "none");

async function openMenu(target) {
  await target.click("[data-editor-menu]");
  await target.waitForSelector("[data-editor-menu-list].show", { timeout: 4000 });
}

const sheetCell = (target, text) => target.locator("[data-editor-sheet-body] .row .dropdown-item", { hasText: text });

async function openSheet(target) {
  await target.keyboard.press("Control+Shift+C");
  await target.waitForSelector("[data-editor-sheet]:not([hidden])", { timeout: 6000 });
}

L.runFeature("EDITOR COMMENTS", async ({ engine, browser, page, run, mobilePage, bag }) => {
  const saveFile = (content) => page.evaluate(([p, f, c]) => {
    const token = document.querySelector('meta[name="csrf-token"]').content;
    return fetch(p, {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded", "X-CSRF-Token": token, Accept: "application/json" },
      body: new URLSearchParams({ path: f, content: c }).toString(),
    }).then((r) => r.status);
  }, [`/projects/${encodeURIComponent(scratch)}/editor/file`, FILE, content]);

  const addComment = (line, text) => page.evaluate(([p, f, l, t]) => {
    const token = document.querySelector('meta[name="csrf-token"]').content;
    return fetch(p, {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": token, Accept: "application/json" },
      body: JSON.stringify({ path: f, line: l, text: t }),
    }).then((r) => r.json());
  }, [`/projects/${encodeURIComponent(scratch)}/editor/comments`, FILE, line, text]);

  await run("setup: scratch project with a seeded file", async () => {
    await L.createProject(page, scratch);
    // Autosave is on by default and the checks read the dirty state.
    await L.setEditorFiles(page, { autosave: false });
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    const save = await saveFile(CONTENT);
    assert(save === 200, `seeding the file answered ${save}`);
    // The projects page pulls its own fragment right after the connect
    // snapshot; navigating away mid-pull reads as a WebKit pageerror.
    await sleep(600);
    await openEditor(page);
    await openFile(page);
  });

  await run("a plain click on a bare line number opens the field under it, Add stores the note and unfolds its thread, a click folds it", async () => {
    assert(await badgeHidden(page), "the empty count badge is not hidden");
    const cursor = await page.locator(".cm-gutters").first().evaluate((el) => getComputedStyle(el).cursor);
    assert(cursor === "pointer", `the gutter cursor is ${cursor}`);
    await L.pressLine(page, 2);
    const field = L.lineField(page, 2);
    await field.waitFor({ timeout: 6000 });
    assert(await L.seen(field), "the field does not show under line 2");
    assert(await page.locator("[data-editor-comment-modal].show").count() === 0, "a dialog opened for the line");
    const under = await page.evaluate(() => {
      const block = document.querySelector(".cm-content [data-line-block]");
      return block?.previousElementSibling?.textContent === "two" && block.nextElementSibling?.textContent === "three";
    });
    assert(under, "the field does not stand between line 2 and line 3");
    assert(await field.locator("[data-line-comment-hint]").textContent() ===
      "The comment follows this line as the file changes, but works best when the line keeps its content. The assistant can read and manage all line comments.",
      "the field's hint is missing or off");
    await field.locator("[data-line-comment-add]").click();
    assert(await field.locator("[data-line-comment-text].is-invalid").count() === 1, "an empty add does not mark the field");
    assert(await field.locator(".invalid-feedback").textContent() === "A comment is required." && await L.seen(field.locator(".invalid-feedback")), "the empty add's message does not show");
    await field.locator("[data-line-comment-text]").fill("needs a guard");
    await field.locator("[data-line-comment-add]").click();
    const card = L.lineThread(page, "needs a guard");
    await card.waitFor({ timeout: 6000 });
    assert(await field.count() === 0, "the field stands after the add");
    assert(await L.seen(card) && await card.evaluate((el) => el.closest("[data-line-block]").dataset.lineBlock) === "2", "the new thread does not unfold under line 2");
    await waitMarked(page, "2");
    assert(await page.locator(".cm-line-comments").count() === 0, "a comment gutter column of its own exists");
    assert(!(await badgeHidden(page)) && await badge(page) === "1", "the menu badge does not count the note");
    await L.pressLine(page, 2);
    await page.waitForFunction(() => !document.querySelector(".cm-content [data-line-thread]"), null, { timeout: 6000 });
    await L.pressLine(page, 2);
    await card.waitFor({ timeout: 6000 });
    assert(await L.seen(card), "a second click does not unfold the thread again");
    await L.pressLine(page, 2);
    await page.waitForFunction(() => !document.querySelector(".cm-content [data-line-block]"), null, { timeout: 6000 });
    await openLineMenu(page, 1);
    const labels = await menuLabels(page);
    assert(labels.join("|") === "Add line comment|Copy path:line", `the right click's bare line menu reads ${labels.join(", ")}`);
    await page.keyboard.press("Escape");
    await page.waitForSelector(".dc-context-menu", { state: "detached", timeout: 4000 });
  });

  await run("the highlight tints every gutter of the line, the fold gutter too", async () => {
    const row = (n) => page.evaluate((n) => {
      const number = [...document.querySelectorAll(".cm-lineNumbers .cm-gutterElement")].find((el) => el.textContent.trim() === String(n) && el.getClientRects().length);
      if (!number) return null;
      const box = number.getBoundingClientRect();
      const y = box.top + box.height / 2;
      return [...number.closest(".cm-gutters").querySelectorAll(".cm-gutter")].map((g) => {
        const cell = [...g.querySelectorAll(".cm-gutterElement")].find((el) => {
          const b = el.getBoundingClientRect();
          return b.height > 0 && b.top <= y && b.bottom >= y;
        });
        return { gutter: g.className, paint: getComputedStyle(cell || g).backgroundColor };
      });
    }, n);
    const marked = await row(2);
    const plain = await row(1);
    const number = marked?.find((c) => c.gutter.includes("cm-lineNumbers"));
    assert(number && number.paint !== plain.find((c) => c.gutter.includes("cm-lineNumbers")).paint, `the number of line 2 reads ${JSON.stringify(marked)} against ${JSON.stringify(plain)}`);
    assert(marked.some((c) => c.gutter.includes("cm-foldGutter")), `no fold gutter stands beside the number: ${JSON.stringify(marked)}`);
    assert(marked.every((c) => c.paint === number.paint), `a gutter of line 2 stays plain: ${JSON.stringify(marked)}`);
  });

  await run("the note is stored per project with the quoted code line", async () => {
    await sleep(300);
    const data = await getComments(page);
    assert(data.comments.length === 1, `server holds ${data.comments.length} comments`);
    const c = data.comments[0];
    assert(c.path === FILE && c.line === 2 && c.text === "needs a guard" && c.lineText === "two", `stored as ${JSON.stringify(c)}`);
    assert(typeof c.id === "string" && c.id.length > 0, "no id on the stored comment");
  });

  await run("editing above moves the highlight with the line and syncs the move", async () => {
    await codeLine(page).click();
    await page.keyboard.press("Control+Home");
    await page.keyboard.type("zero");
    await page.keyboard.press("Enter");
    await waitMarked(page, "3");
    // The server judges the saved state: an unsaved buffer's move reads back
    // as the disk's line, so the stored position is asserted after the save.
    await page.keyboard.press("Control+s");
    await sleep(1200);
    const data = await getComments(page);
    assert(data.comments.length === 1 && data.comments[0].line === 3, `moved comment stored as line ${data.comments[0] && data.comments[0].line}`);
    assert(data.comments[0].lineText === "two", `moved quote is ${JSON.stringify(data.comments[0].lineText)}`);
    assert(!data.comments[0].outdated, "the saved move reads as outdated");
  });

  await run("an unsaved move keeps gutter, sheet and jump on the same line", async () => {
    await codeLine(page).click();
    await page.keyboard.press("Control+Home");
    await page.keyboard.press("Enter");
    await waitMarked(page, "4");
    // While the buffer is unsaved no move leaves the client: the server keeps
    // the saved anchor, and the local overlay keeps all three consumers on 4.
    await sleep(1500);
    await waitMarked(page, "4");
    const server = await getComments(page);
    assert(server.comments.length === 1 && server.comments[0].line === 3 && !server.comments[0].outdated,
      `the unsaved edit reached the server: ${JSON.stringify(server.comments)}`);
    await openSheet(page);
    const cell = sheetCell(page, "needs a guard");
    assert((await cell.textContent()).includes(`${FILE}:4`), "the sheet cell lags the live mapping");
    await cell.click();
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 6000 });
    await pickMenuItem(page, "Go to line");
    await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 6000 });
    await page.waitForFunction(() => (document.querySelector("[data-editor-pos]")?.textContent || "").startsWith("4:"), null, { timeout: 6000 });
    await page.keyboard.press("Control+z");
    await waitMarked(page, "3");
    await page.keyboard.press("Control+s");
    await sleep(1200);
  });

  await run("deleting the commented line reads outdated at once, undo heals it", async () => {
    await codeLine(page).click();
    await page.keyboard.press("Control+Home");
    await page.keyboard.press("ArrowDown");
    await page.keyboard.press("ArrowDown");
    await page.keyboard.press("Shift+ArrowDown");
    await page.keyboard.press("Delete");
    await page.waitForFunction(() => document.querySelectorAll(".cm-lineNumbers .cm-comment-line-outdated").length === 1, null, { timeout: 6000 });
    await openSheet(page);
    const cell = sheetCell(page, "needs a guard");
    const cellText = await cell.textContent();
    assert(cellText.includes("Outdated") && cellText.includes("was: two"), `the cell does not freeze the old quote: ${cellText}`);
    await page.keyboard.press("Control+Shift+C");
    await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 6000 });
    // Nothing leaves the client while the buffer is unsaved: the orphan must
    // not write a new quote, and the server's disk view (where the line still
    // stands) must not un-orphan the dirty buffer either.
    await sleep(1500);
    assert(await page.locator(".cm-lineNumbers .cm-comment-line-outdated").count() === 1, "the sync cycle laundered the orphan");
    const data = await getComments(page);
    assert(data.comments.length === 1 && data.comments[0].lineText === "two" && !data.comments[0].outdated,
      `the orphan wrote a new quote to the server: ${JSON.stringify(data.comments)}`);
    await codeLine(page).click();
    await page.keyboard.press("Control+z");
    await page.waitForFunction(() => document.querySelectorAll(".cm-lineNumbers .cm-comment-line-outdated").length === 0, null, { timeout: 6000 });
    await waitMarked(page, "3");
    await page.keyboard.press("Control+s");
    await sleep(1200);
  });

  await run("cut and paste elsewhere drags the marker along without a save", async () => {
    await codeLine(page).click();
    await page.keyboard.press("Control+Home");
    await page.keyboard.press("ArrowDown");
    await page.keyboard.press("ArrowDown");
    await page.keyboard.press("Shift+ArrowDown");
    await page.keyboard.press("Delete");
    await page.waitForFunction(() => document.querySelectorAll(".cm-lineNumbers .cm-comment-line-outdated").length === 1, null, { timeout: 6000 });
    await page.keyboard.press("Control+End");
    await page.keyboard.press("Enter");
    await page.keyboard.insertText("two");
    await page.waitForFunction(() => document.querySelectorAll(".cm-lineNumbers .cm-comment-line-outdated").length === 0, null, { timeout: 6000 });
    await waitMarked(page, "7");
    let data = await getComments(page);
    assert(data.comments[0].line === 3, `the rebind reached the server before the save: ${JSON.stringify(data.comments)}`);
    await page.keyboard.press("Control+s");
    await sleep(1200);
    data = await getComments(page);
    assert(data.comments[0].line === 7 && data.comments[0].lineText === "two" && !data.comments[0].outdated,
      `the save did not confirm the overlay: ${JSON.stringify(data.comments)}`);
    const id = data.comments[0].id;
    await saveFile("zero\none\ntwo\nthree\nfour\nfive\n");
    await page.evaluate(([p, cid, f]) => {
      const token = document.querySelector('meta[name="csrf-token"]').content;
      return fetch(p, {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-CSRF-Token": token, Accept: "application/json" },
        body: JSON.stringify({ id: cid, path: f, line: 3, text: "needs a guard" }),
      }).then((r) => r.status);
    }, [`/projects/${encodeURIComponent(scratch)}/editor/comments`, id, FILE]);
    await openEditor(page);
    await openFile(page);
    await waitMarked(page, "3");
  });

  await run("an orphan past the file end leaves the gutter, the sheet keeps it", async () => {
    await codeLine(page).click();
    await page.keyboard.press("Control+Home");
    await page.keyboard.press("ArrowDown");
    await page.keyboard.press("Shift+Control+End");
    await page.keyboard.press("Delete");
    await page.waitForFunction(() => document.querySelectorAll(".cm-lineNumbers .cm-comment-line").length === 0, null, { timeout: 6000 });
    assert(await badge(page) === "1", "the badge dropped the orphan");
    await openSheet(page);
    const cellText = await sheetCell(page, "needs a guard").textContent();
    assert(cellText.includes("Outdated") && cellText.includes("was: two"), `the sheet lost the over-the-end orphan: ${cellText}`);
    await page.keyboard.press("Control+Shift+C");
    await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 6000 });
    await codeLine(page).click();
    await page.keyboard.press("Control+z");
    await page.waitForFunction(() => document.querySelectorAll(".cm-lineNumbers .cm-comment-line-outdated").length === 0, null, { timeout: 6000 });
    await waitMarked(page, "3");
    await page.keyboard.press("Control+s");
    await sleep(1200);
  });

  await run("a reload brings the highlight back on its line", async () => {
    await page.keyboard.press("Control+s");
    await sleep(500);
    await openEditor(page);
    await openFile(page);
    await waitMarked(page, "3");
    assert(await badge(page) === "1", "the badge does not survive the reload");
  });

  await run("Ctrl+Alt+C opens the field on the cursor line", async () => {
    await codeLine(page).click();
    await page.keyboard.press("Control+Home");
    await addInline(page, 1, "cursor note");
    await waitMarked(page, "1,3");
    assert(await badge(page) === "2", "the badge does not count both notes");
  });

  await run("Ctrl+Alt+C opens the reply of the existing note's thread, a right click opens the menu too", async () => {
    await codeLine(page).click();
    await page.keyboard.press("Control+Home");
    await page.keyboard.press("Control+Alt+C");
    const card = L.lineThread(page, "cursor note");
    await page.waitForFunction(() => document.activeElement?.matches("[data-line-thread-reply-text]"), null, { timeout: 6000 });
    assert(await card.locator("[data-comment-note-text]").allTextContents().then((t) => t.join("|")) === "cursor note", "the thread does not hold the note");
    assert(await L.seen(card.locator("[data-line-thread-reply]")) && await card.locator("[data-line-thread-reply-text]").inputValue() === "" && await card.locator("[data-line-thread-reply-send]").textContent() === "Reply", "the thread does not offer its reply");
    await page.keyboard.press("Escape");
    await page.waitForFunction(() => document.activeElement?.matches(".cm-content"), null, { timeout: 6000 });
    assert(!(await L.seen(card.locator("[data-line-thread-reply]"))), "Escape left the empty reply open");
    await openLineMenu(page, 1);
    const labels = await menuLabels(page);
    assert(labels.join("|") === "Open thread|Delete comment|Copy path:line", `marked line menu reads ${labels.join(", ")}`);
    await page.keyboard.press("Escape");
    await page.waitForSelector(".dc-context-menu", { state: "detached", timeout: 4000 });
  });

  await run("the pencil edits the note in place and saves on Ctrl+Enter, the line menu's Delete comment confirms and removes", async () => {
    const card = L.lineThread(page, "cursor note");
    if (!(await L.seen(card))) await L.pressLine(page, 1);
    await card.waitFor({ timeout: 6000 });
    assert(await card.locator("[data-line-thread-delete]").count() === 1, "the thread's head carries no bin");
    await card.locator('button[aria-label="Edit comment"]').click();
    const text = card.locator("[data-comment-note-field]");
    assert(await L.seen(text) && await text.inputValue() === "cursor note" && !(await L.seen(card.locator("[data-comment-note-text]"))) && !(await L.seen(card.locator("[data-line-thread-reply]"))), "the note's text did not turn into its field");
    await text.fill("cursor note, edited");
    await text.press("Control+Enter");
    await page.waitForFunction(() => [...document.querySelectorAll(".cm-content [data-comment-note-text]")].some((el) => el.textContent === "cursor note, edited" && el.getClientRects().length), null, { timeout: 6000 });
    let data = await getComments(page);
    assert(data.comments.some((c) => c.text === "cursor note, edited") && !(await L.seen(text)), "the Ctrl+Enter save did not land in place");
    await openLineMenu(page, 1);
    await pickMenuItem(page, "Delete comment");
    await page.waitForSelector(".swal2-confirm", { timeout: 6000 });
    assert((await page.locator(".swal2-title").textContent()).includes("Delete this comment"), "no confirm before the line menu delete");
    const confirmText = await page.locator(".swal2-html-container").textContent();
    assert(confirmText.includes(`${FILE}:1`) && confirmText.includes("cursor note, edited"), `the confirm does not name file, line and text: ${confirmText}`);
    assert(!confirmText.includes("—"), `the confirm still joins with a dash: ${confirmText}`);
    assert(await page.locator(".swal2-html-container .text-secondary").textContent() === "cursor note, edited", "the comment text is not the dimmed own line");
    await page.click(".swal2-confirm");
    await confirmDialogGone(page);
    await waitMarked(page, "3");
    await page.waitForFunction(() => !document.querySelector(".cm-content [data-line-thread]"), null, { timeout: 6000 });
    await sleep(400);
    data = await getComments(page);
    assert(data.comments.length === 1 && data.comments[0].text === "needs a guard", "Delete removed the wrong note");
    assert(await badge(page) === "1", "the badge does not follow the delete");
  });

  await run("Copy path:line puts the relative path with the line on the clipboard", async () => {
    await openLineMenu(page, 3);
    await pickMenuItem(page, "Copy path:line");
    await page.waitForFunction(() => (document.querySelector("[data-editor-status]")?.textContent || "").includes("Copied"), null, { timeout: 6000 });
    if (engine === "chromium") {
      const text = await page.evaluate(() => navigator.clipboard.readText());
      assert(text === `${FILE}:3`, `the clipboard holds ${JSON.stringify(text)}`);
    }
  });

  await run("the sheet holds one cell per comment, and its menu's Go to line jumps", async () => {
    await openSheet(page);
    assert(await page.locator("[data-editor-sheet-title]").textContent() === "Line comments", "sheet title");
    await page.keyboard.press("Control+Shift+C");
    await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 6000 });
    await openSheet(page);
    const cell = sheetCell(page, "needs a guard");
    assert(await cell.count() === 1, "the note has no cell");
    assert((await cell.textContent()).includes(`${FILE}:3`), "the cell does not name file and line");
    const wrapper = await cell.evaluate((el) => el.parentElement.className);
    assert(wrapper.includes("col-12") && wrapper.includes("col-lg-6"), `the cell stands in ${wrapper}, not in the git grid`);
    await cell.click();
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 6000 });
    const labels = await menuLabels(page);
    assert(labels.join("|") === "Go to line|Open thread|Delete", `cell menu reads ${labels.join(", ")}`);
    assert(await page.locator(".dc-context-menu .dropdown-item.text-danger", { hasText: "Delete" }).count() === 1, "Delete is not the danger entry");
    assert(await page.locator("[data-editor-sheet]:not([hidden])").count() === 1, "the sheet does not stand behind the menu");
    await pickMenuItem(page, "Go to line");
    await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 6000 });
    await page.waitForFunction(() => (document.querySelector("[data-editor-pos]")?.textContent || "").startsWith("3:"), null, { timeout: 6000 });
  });

  await run("the cell menu opens the thread under its line and deletes with the sheet standing, Enter opens it too", async () => {
    await codeLine(page).click();
    await page.keyboard.press("Control+Home");
    await addInline(page, 1, "temp note");
    await L.pressLine(page, 1);
    await page.waitForFunction(() => !document.querySelector(".cm-content [data-line-thread]"), null, { timeout: 6000 });
    await waitMarked(page, "1,3");
    await openSheet(page);
    // The sheet opens on its filter with the first head action marked, so
    // the first arrow stands on the second one and the next on the first cell.
    await page.keyboard.press("ArrowDown");
    await page.keyboard.press("ArrowDown");
    await page.keyboard.press("Enter");
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 6000 });
    await page.keyboard.press("Escape");
    await page.waitForSelector(".dc-context-menu", { state: "detached", timeout: 4000 });
    await sleep(400);
    await sheetCell(page, "temp note").click();
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 6000 });
    await pickMenuItem(page, "Open thread");
    await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 6000 });
    const card = L.lineThread(page, "temp note");
    await card.waitFor({ timeout: 6000 });
    assert(await L.seen(card) && await card.evaluate((el) => el.closest("[data-line-block]").dataset.lineBlock) === "1", "Open thread does not unfold the thread under line 1");
    assert(await page.locator("[data-editor-comment-modal].show").count() === 0, "Open thread opened a dialog");
    await openSheet(page);
    await sheetCell(page, "temp note").click();
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 6000 });
    await pickMenuItem(page, "Delete");
    await page.waitForSelector(".swal2-confirm", { timeout: 6000 });
    assert((await page.locator(".swal2-title").textContent()).includes("Delete this comment"), "no confirm before the cell delete");
    const confirmText = await page.locator(".swal2-html-container").textContent();
    assert(confirmText.includes(`${FILE}:1`) && confirmText.includes("temp note"), `the confirm does not name file, line and text: ${confirmText}`);
    assert(await page.locator(".swal2-html-container .text-secondary").textContent() === "temp note", "the comment text is not the dimmed own line");
    await page.click(".swal2-cancel");
    await confirmDialogGone(page);
    assert(await sheetCell(page, "temp note").count() === 1, "cancelling the confirm deleted anyway");
    await sleep(400);
    await sheetCell(page, "temp note").click();
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 6000 });
    await pickMenuItem(page, "Delete");
    await page.waitForSelector(".swal2-confirm", { timeout: 6000 });
    await page.click(".swal2-confirm");
    await confirmDialogGone(page);
    await page.waitForFunction(() => ![...document.querySelectorAll("[data-editor-sheet-body] .dropdown-item")].some((el) => el.textContent.includes("temp note")), null, { timeout: 6000 });
    assert(await page.locator("[data-editor-sheet]:not([hidden])").count() === 1, "the sheet closed on the cell delete");
    assert(await sheetCell(page, "needs a guard").count() === 1, "the other cell went with it");
    await sleep(400);
    const data = await getComments(page);
    assert(data.comments.length === 1 && data.comments[0].text === "needs a guard", "the cell delete removed the wrong note");
    await page.keyboard.press("Control+Shift+C");
    await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 6000 });
  });

  // The filter every sheet carries in its head: typing narrows the cells, the
  // head actions go when they are no hit, Enter in the field opens the first
  // hit's menu, and Escape in the field clears the query before the sheet's
  // own Escape closes it.
  await run("the sheet's filter narrows the cells, Enter opens the first hit's menu", async () => {
    await codeLine(page).click();
    await page.keyboard.press("Control+Home");
    await addInline(page, 1, "second thought");
    await waitMarked(page, "1,3");
    await openSheet(page);
    const shown = () => page.evaluate(() =>
      [...document.querySelectorAll("[data-editor-sheet-body] .dropdown-item")]
        .filter((row) => row.getClientRects().length > 0)
        .map((row) => row.textContent.replace(/\s+/g, " ").trim()));
    assert(await page.evaluate(() => document.activeElement === document.querySelector("[data-editor-sheet-filter]")), "the sheet did not open on its filter");
    const all = await shown();
    assert(all.length === 4, `the sheet carries ${all.length} rows: ${JSON.stringify(all)}`);
    await page.keyboard.type("guard");
    await sleep(250);
    const hits = await shown();
    assert(hits.length === 1 && /needs a guard/.test(hits[0]), `the filter left ${JSON.stringify(hits)}`);
    const count = (await page.locator("[data-editor-sheet-filter-count]").textContent()).trim();
    assert(count === "1 of 4", `the count reads "${count}"`);
    const marked = await page.evaluate(() => document.querySelector("[data-editor-sheet-body] .selected")?.textContent.replace(/\s+/g, " ").trim() || null);
    assert(marked && /needs a guard/.test(marked), `the hit is not marked: "${marked}"`);
    await page.keyboard.press("Enter");
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 6000 });
    const labels = await menuLabels(page);
    assert(labels.join("|") === "Go to line|Open thread|Delete", `Enter opened ${labels.join(", ")}`);
    await page.keyboard.press("Escape");
    await page.waitForSelector(".dc-context-menu", { state: "detached", timeout: 4000 });
    await sleep(400);
    await page.click("[data-editor-sheet-filter]");
    await page.keyboard.press("Escape");
    await sleep(250);
    assert(await page.locator("[data-editor-sheet]:not([hidden])").count() === 1, "Escape closed the sheet instead of clearing the filter");
    assert((await shown()).length === 4, "the cleared filter did not bring the rows back");
    await page.keyboard.press("Escape");
    await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 6000 });
    const data = await getComments(page);
    const extra = data.comments.find((c) => c.text === "second thought");
    assert(extra, "the second note is not stored");
    const status = await page.evaluate(([p, id]) => fetch(p, {
      method: "POST",
      headers: {
        "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]').content,
        "Content-Type": "application/json",
        Accept: "application/json",
      },
      body: JSON.stringify({ ids: [id] }),
    }).then((r) => r.status), [`/projects/${encodeURIComponent(scratch)}/editor/comments/delete`, extra.id]);
    assert(status === 200, `removing the second note answered ${status}`);
    await waitMarked(page, "3");
  });

  await run("copy as text puts path, comment and quoted line on the clipboard", async () => {
    await openMenu(page);
    await page.click("[data-editor-comments-item]");
    await page.waitForSelector("[data-editor-sheet]:not([hidden])", { timeout: 6000 });
    await page.locator("[data-editor-sheet-body] .dropdown-item", { hasText: "Copy as text" }).click();
    await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 6000 });
    await page.waitForSelector(".dc-toast", { timeout: 6000 });
    if (engine === "chromium") {
      const text = await page.evaluate(() => navigator.clipboard.readText());
      assert(text.includes(`Line comments in ${scratch}:`), `the text's heading is off: ${JSON.stringify(text.split("\n")[0])}`);
      assert(text.includes(`- ${FILE}:3, code: two\n  > needs a guard`), `the text's format is ${JSON.stringify(text)}`);
    }
  });

  await run("Delete all clears every comment behind its confirm", async () => {
    await openSheet(page);
    await page.locator("[data-editor-sheet-body] .dropdown-item", { hasText: "Delete all comments" }).click();
    await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 6000 });
    await page.waitForSelector(".swal2-confirm", { timeout: 6000 });
    assert((await page.locator(".swal2-title").textContent()).includes("Delete all comments"), "no confirm before the clear");
    await page.click(".swal2-confirm");
    await confirmDialogGone(page);
    await waitMarked(page, "");
    await sleep(400);
    const data = await getComments(page);
    assert(data.comments.length === 0, `the clear left ${data.comments.length} comments`);
    assert(await badgeHidden(page), "the emptied badge is not hidden");
  });

  await run("a uniquely moved quote rebinds on read, a vanished one reads outdated", async () => {
    await saveFile("alpha\nbeta\ntwo\ngamma\n");
    const added = await addComment(3, "orphan candidate");
    assert(added.comment && added.comment.lineText === "two", `the add did not quote the disk: ${JSON.stringify(added)}`);
    await saveFile("alpha\nbeta\ngamma\ntwo\ndelta\n");
    let data = await getComments(page);
    assert(data.comments.length === 1 && data.comments[0].line === 4 && !data.comments[0].outdated, `the unique quote did not rebind: ${JSON.stringify(data.comments)}`);
    data = await getComments(page);
    assert(data.comments[0].line === 4, "the rebind was not persisted");
    await saveFile("alpha\nbeta\ngamma\ndelta\n");
    data = await getComments(page);
    assert(data.comments[0].outdated === true && data.comments[0].line === 4 && data.comments[0].lineText === "two",
      `the vanished quote is not outdated at its last line: ${JSON.stringify(data.comments)}`);
  });

  await run("an outdated note shows in gutter, sheet and text", async () => {
    await openEditor(page);
    await openFile(page, "alpha");
    await page.waitForFunction(() => {
      const cell = document.querySelector(".cm-lineNumbers .cm-comment-line-outdated");
      return cell && cell.textContent.trim() === "4";
    }, null, { timeout: 8000 });
    await openSheet(page);
    const cell = sheetCell(page, "orphan candidate");
    assert(await cell.count() === 1, "the orphan has no cell");
    const cellText = await cell.textContent();
    assert(cellText.includes("Outdated") && cellText.includes("was: two"), `the cell does not say outdated with the old quote: ${cellText}`);
    await page.locator("[data-editor-sheet-body] .dropdown-item", { hasText: "Copy as text" }).click();
    await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 6000 });
    await page.waitForSelector(".dc-toast", { timeout: 6000 });
    if (engine === "chromium") {
      const text = await page.evaluate(() => navigator.clipboard.readText());
      assert(text.includes(`- ${FILE}:4 (outdated), code: two`), `the text does not mark the orphan: ${JSON.stringify(text)}`);
    }
  });

  await run("deleting the outdated removes exactly the orphans", async () => {
    await addComment(1, "healthy note");
    const status = await page.evaluate((p) => {
      const token = document.querySelector('meta[name="csrf-token"]').content;
      return fetch(p, {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-CSRF-Token": token, Accept: "application/json" },
        body: JSON.stringify({ outdated: true }),
      }).then((r) => r.json());
    }, `/projects/${encodeURIComponent(scratch)}/editor/comments/delete`);
    assert(status.removed === 1, `the outdated delete removed ${status.removed}`);
    const data = await getComments(page);
    assert(data.comments.length === 1 && data.comments[0].text === "healthy note" && !data.comments[0].outdated,
      `the orphan delete took the wrong note: ${JSON.stringify(data.comments)}`);
    await page.evaluate((p) => {
      const token = document.querySelector('meta[name="csrf-token"]').content;
      return fetch(p, {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-CSRF-Token": token, Accept: "application/json" },
        body: JSON.stringify({ all: true }),
      }).then((r) => r.status);
    }, `/projects/${encodeURIComponent(scratch)}/editor/comments/delete`);
    await saveFile(CONTENT);
  });

  await run("a comment pinned elsewhere lands on the locally shifted line", async () => {
    await openEditor(page);
    await openFile(page);
    await codeLine(page).click();
    await page.keyboard.press("Control+Home");
    await page.keyboard.press("Enter");
    const added = await addComment(2, "from device b");
    assert(added.comment && added.comment.lineText === "two", `the add did not anchor on the disk: ${JSON.stringify(added)}`);
    await waitMarked(page, "3");
    await page.keyboard.press("Control+s");
    await sleep(1200);
    const data = await getComments(page);
    assert(data.comments.length === 1 && data.comments[0].line === 3 && data.comments[0].lineText === "two" && !data.comments[0].outdated,
      `the save did not persist the mapped anchor: ${JSON.stringify(data.comments)}`);
    await page.evaluate((p) => {
      const token = document.querySelector('meta[name="csrf-token"]').content;
      return fetch(p, {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-CSRF-Token": token, Accept: "application/json" },
        body: JSON.stringify({ all: true }),
      }).then((r) => r.status);
    }, `/projects/${encodeURIComponent(scratch)}/editor/comments/delete`);
    await saveFile(CONTENT);
  });

  await run("a note born in a dirty buffer quotes the buffer, the save heals it", async () => {
    await openEditor(page);
    await openFile(page);
    await codeLine(page).click();
    await page.keyboard.press("Control+Home");
    await page.keyboard.insertText("fresh\n");
    await page.keyboard.press("Control+Home");
    await addInline(page, 1, "dirty born");
    await waitMarked(page, "1");
    assert(!(await L.seen(L.lineThread(page, "dirty born").locator("[data-line-thread-outdated]"))), "the born note's card reads outdated in its own buffer");
    await sleep(600);
    assert(await page.locator(".cm-lineNumbers .cm-comment-line-outdated").count() === 0, "the born note reads outdated in its own buffer");
    let data = await getComments(page);
    assert(data.comments.length === 1 && data.comments[0].lineText === "fresh" && data.comments[0].outdated === true,
      `the stored quote is not the buffer's line: ${JSON.stringify(data.comments)}`);
    await page.keyboard.press("Control+s");
    await sleep(1200);
    data = await getComments(page);
    assert(data.comments[0].line === 1 && data.comments[0].lineText === "fresh" && !data.comments[0].outdated,
      `the save did not heal the born note: ${JSON.stringify(data.comments)}`);
    await page.evaluate((p) => {
      const token = document.querySelector('meta[name="csrf-token"]').content;
      return fetch(p, {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-CSRF-Token": token, Accept: "application/json" },
        body: JSON.stringify({ all: true }),
      }).then((r) => r.status);
    }, `/projects/${encodeURIComponent(scratch)}/editor/comments/delete`);
    await saveFile(CONTENT);
  });

  await run("a rename over the tree route takes the notes to the new path", async () => {
    const added = await addComment(2, "rides along");
    assert(added.comment && added.comment.lineText === "two", `the add did not quote the disk: ${JSON.stringify(added)}`);
    const rename = (from, to) => page.evaluate(([p, f, n]) => {
      const token = document.querySelector('meta[name="csrf-token"]').content;
      return fetch(p, {
        method: "POST",
        headers: { "Content-Type": "application/x-www-form-urlencoded", "X-CSRF-Token": token, Accept: "application/json" },
        body: new URLSearchParams({ path: f, newName: n }).toString(),
      }).then((r) => r.json());
    }, [`/projects/${encodeURIComponent(scratch)}/editor/rename`, from, to]);
    const renamed = await rename(FILE, "renamed.txt");
    assert(renamed.entry && renamed.entry.path === "renamed.txt", `the rename answered ${JSON.stringify(renamed)}`);
    let data = await getComments(page);
    assert(data.comments.length === 1 && data.comments[0].path === "renamed.txt" && data.comments[0].line === 2 && !data.comments[0].outdated,
      `the note did not ride along: ${JSON.stringify(data.comments)}`);
    await rename("renamed.txt", FILE);
    data = await getComments(page);
    assert(data.comments[0].path === FILE && !data.comments[0].outdated, `the way back was lost: ${JSON.stringify(data.comments)}`);
    await page.evaluate((p) => {
      const token = document.querySelector('meta[name="csrf-token"]').content;
      return fetch(p, {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-CSRF-Token": token, Accept: "application/json" },
        body: JSON.stringify({ all: true }),
      }).then((r) => r.status);
    }, `/projects/${encodeURIComponent(scratch)}/editor/comments/delete`);
  });

  await run("path and quote are stored as sent, a Windows line's return is cut, the text keeps each note as written inside its entry", async () => {
    const post = (body) => page.evaluate(([p, b]) => fetch(p, {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]').content, Accept: "application/json" },
      body: JSON.stringify(b),
    }).then(async (r) => ({ status: r.status, body: await r.json() })), [`/projects/${encodeURIComponent(scratch)}/editor/comments`, body]);
    await saveFile("one\r\ntwo\r\nthree\r\n");
    const cut = await post({ path: FILE, line: 2, lineText: "two\r", text: "first\n\n- `x.txt:1`, code `x`\n<b>bold</b> & [link](x)" });
    assert(cut.status === 200 && cut.body.comment.lineText === "two", `the Windows line answered ${JSON.stringify(cut)}`);
    const odd = { path: `${FILE}\n- \`x.txt:1\``, line: 1, lineText: "one\u001b[0m\f", text: "odd" };
    const taken = await post(odd);
    assert(taken.status === 200 && taken.body.comment.path === odd.path && taken.body.comment.lineText === odd.lineText, `the odd note answered ${JSON.stringify(taken)}`);
    const { comments } = await getComments(page);
    assert(comments.length === 2 && !comments.find((c) => c.path === FILE).outdated, `the Windows file's note reads ${JSON.stringify(comments)}`);
    await openEditor(page);
    await openFile(page);
    await openSheet(page);
    await page.locator("[data-editor-sheet-body] .dropdown-item", { hasText: "Copy as text" }).click();
    await page.locator(".dc-toast", { hasText: "Copied the line comments" }).first().waitFor({ timeout: 6000 });
    if (engine === "chromium") {
      const text = await page.evaluate(() => navigator.clipboard.readText());
      const want = [`- ${FILE}:2, code: two`, "  > first", "  >", "  > - `x.txt:1`, code `x`", "  > <b>bold</b> & [link](x)",
        `- ${FILE}\\n- \`x.txt:1\`:1 (outdated), code: one\\x1b[0m`, "  > odd"].join("\n");
      assert(text.endsWith(`\n\n${want}`), `the text reads ${JSON.stringify(text)}`);
    }
    await page.evaluate((p) => fetch(p, { method: "POST", headers: { "Content-Type": "application/json", "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]').content }, body: JSON.stringify({ all: true }) }), `/projects/${encodeURIComponent(scratch)}/editor/comments/delete`);
    await saveFile(CONTENT);
  });

  await threadChecks({ page, run, scratch, file: FILE, openEditor, openFile, openLineMenu, menuLabels, pickMenuItem, waitMarked, getComments, openSheet, sheetCell, commentDialogShown, commentDialogGone, confirmDialogGone });

  await inlineChecks({ page, run, mobilePage, scratch, file: FILE, saveFile, openEditor, openFile, waitMarked, getComments, openSheet, sheetCell, pickMenuItem });
  await editChecks({ page, run, mobilePage, scratch, file: FILE, saveFile, openEditor, openFile, waitMarked, getComments });

  await guestChecks({ engine, browser, page, run, mobilePage, bag, scratch, file: FILE, openEditor, openFile, openSheet, commentDialogShown, commentDialogGone });

  await run("phone: a tap on a line number opens the field, a typed note adds on Add, a tap folds its thread, the sheet reads on touch", async () => {
    const mp = await mobilePage();
    await sleep(600);
    await openEditor(mp);
    await openFile(mp);
    await L.pressLine(mp, 2, { tap: true });
    const field = L.lineField(mp, 2);
    await field.waitFor({ timeout: 6000 });
    assert(await L.seen(field), "the tap opened no field under line 2");
    const box = await field.evaluate((el) => {
      const r = el.getBoundingClientRect();
      return { left: r.left, right: r.right, width: innerWidth };
    });
    assert(box.left >= 0 && box.right <= box.width + 0.5, `the field leaves the phone's width: ${JSON.stringify(box)}`);
    await field.locator("[data-line-comment-text]").tap();
    await mp.keyboard.type("phone note");
    await field.locator("[data-line-comment-add]").tap();
    const card = L.lineThread(mp, "phone note");
    await card.waitFor({ timeout: 6000 });
    await waitMarked(mp, "2");
    await L.pressLine(mp, 2, { tap: true });
    await mp.waitForFunction(() => !document.querySelector(".cm-content [data-line-thread]"), null, { timeout: 6000 });
    await sleep(400);
    await L.longPress(mp, lineCell(mp, 2));
    const held = await menuLabels(mp);
    assert(held[0] === "Open thread" && held.includes("Copy path:line"), `the long press opened ${held.join(", ")}`);
    assert(await mp.locator(".cm-content [data-line-thread], .cm-content [data-line-comment-form]").count() === 0, "the long press also unfolded the line");
    await mp.keyboard.press("Escape");
    await mp.waitForSelector(".dc-context-menu", { state: "detached", timeout: 4000 });
    await openMenu(mp);
    await mp.click("[data-editor-comments-item]");
    await mp.waitForSelector("[data-editor-sheet]:not([hidden])", { timeout: 6000 });
    const cell = sheetCell(mp, "phone note");
    assert(await cell.count() === 1, "the phone note has no cell");
    await mp.click("[data-editor-sheet-close]");
    await mp.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 6000 });
  });

  await run("cleanup: delete the scratch project", async () => {
    await L.deleteProject(page, scratch);
  });
});
