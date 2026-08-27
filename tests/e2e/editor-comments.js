const L = require("./lib");
const { assert, sleep, BASE } = L;

// Line comments in the editor: notes pinned to single lines. Routes:
// GET /projects/:name/editor/comments (the project's notes, ordered by file
// then line; ?path= narrows for the CLI), POST .../editor/comments (upsert
// one: without an id a new note, with one an edit, path change included; a
// save without a quote gets the code line read from the disk), POST
// .../editor/comments/delete ({ids}, {paths} or {all}), POST
// .../editor/comments/move (one file's lines after the buffer edited around
// them, posted by the save alone). State lives per project in
// <state-dir>/line-comments/<project>.json, published as the bare
// "linecomments" event (project named; the connect snapshot carries it bare),
// every open editor pulls the list itself. Client: no gutter column of its
// own; a commented line highlights its whole gutter row (.cm-comment-line via
// gutterLineClass, so the mark spans the line numbers AND the gutters beside
// them). The line's menu opens on a plain click anywhere in the gutter, on a
// right click and on a touch long press (wireRowMenus), and holds in this
// order: add or edit the comment, Delete comment on a commented line, Copy
// path:line, and the blame toggle (only in a git repository, blameMenuItem
// answers null without one, so this runner's scratch project shows a bare
// line's menu with two entries). The
// note's dialog is a Bootstrap modal (data-editor-comment-modal, its host
// moved to document.body for the fullscreen editor): textarea with a dimmed
// form-hint under it, Cancel, Save
// or Add, an empty save marks the field is-invalid, Ctrl/Cmd+Enter saves
// like the commit message; the dialog only creates and edits, every delete
// lives in the menus behind a swal confirm. Ctrl+Alt+C comments the cursor
// line, Ctrl+Shift+C
// toggles the Line comments sheet, and the menu badges (Line comments, Git)
// hide at zero instead of standing as an empty pill. The sheet follows the
// git history's shape: one cell per comment in a row-deck grid (col-12
// col-lg-6, two per line from lg up), the whole cell is the control and a
// click on it opens the app's menu over it (Go to line jumps there, Edit
// opens the note's dialog, Delete is the danger entry), anchored at the click
// or at the cell when Enter got there; the sheet stands behind that menu.
// The head actions close the sheet themselves: Copy as Markdown (heading
// "Line comments in <project>:", then path:line, the comment, the code line
// as quote) and Delete all comments behind a confirm. Gotchas:
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
//     the Markdown head), and delete {outdated:true} takes exactly those,
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
  const row = target.locator(`.editor-file[data-path="${FILE}"]`);
  await row.waitFor({ state: "visible", timeout: 15000 });
  await row.click();
  await target.waitForSelector(".cm-lineNumbers", { state: "attached", timeout: 15000 });
  await target.waitForFunction((p) => document.querySelector(".cm-content")?.textContent.includes(p), probe, { timeout: 15000 });
}

const lineCell = (target, n) => target
  .locator(".cm-lineNumbers .cm-gutterElement:visible")
  .filter({ hasText: new RegExp(`^${n}$`) })
  .first();

async function openLineMenu(target, n, { button = "left" } = {}) {
  await sleep(400);
  await lineCell(target, n).click({ button });
  await target.waitForSelector(".dc-context-menu", { state: "visible", timeout: 6000 });
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

L.runFeature("EDITOR COMMENTS", async ({ engine, page, run, mobilePage }) => {
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
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    const save = await page.evaluate(([p, f, c]) => {
      const token = document.querySelector('meta[name="csrf-token"]').content;
      return fetch(p, {
        method: "POST",
        headers: { "Content-Type": "application/x-www-form-urlencoded", "X-CSRF-Token": token, Accept: "application/json" },
        body: new URLSearchParams({ path: f, content: c }).toString(),
      }).then((r) => r.status);
    }, [`/projects/${encodeURIComponent(scratch)}/editor/file`, FILE, CONTENT]);
    assert(save === 200, `seeding the file answered ${save}`);
    // The projects page pulls its own fragment right after the connect
    // snapshot; navigating away mid-pull reads as a WebKit pageerror.
    await sleep(600);
    await openEditor(page);
    await openFile(page);
  });

  await run("a plain click on the line number opens the menu and adds a note", async () => {
    assert(await badgeHidden(page), "the empty count badge is not hidden");
    const cursor = await page.locator(".cm-gutters").first().evaluate((el) => getComputedStyle(el).cursor);
    assert(cursor === "pointer", `the gutter cursor is ${cursor}`);
    await openLineMenu(page, 2);
    const labels = await menuLabels(page);
    assert(labels.join("|") === "Add line comment|Copy path:line", `bare line menu reads ${labels.join(", ")}`);
    await pickMenuItem(page, "Add line comment");
    await commentDialogShown(page);
    assert(await page.locator("[data-editor-comment-title]").textContent() === "Line comment", "dialog title");
    assert(await page.locator("[data-editor-comment-place]").textContent() === `${FILE}:2`, "dialog does not name file and line");
    assert(await page.locator("[data-editor-comment-modal] .form-hint").textContent() ===
      "The comment follows this line as the file changes, but works best when the line keeps its content. The assistant can read and manage all line comments.",
      "the dialog hint is missing or off");
    await page.click("[data-editor-comment-save]");
    assert(await page.locator("[data-editor-comment-text].is-invalid").count() === 1, "an empty save does not mark the field");
    assert(await page.locator("[data-editor-comment-modal].show").count() === 1, "an empty save closed the dialog");
    await page.fill("[data-editor-comment-text]", "needs a guard");
    await page.click("[data-editor-comment-save]");
    await commentDialogGone(page);
    await waitMarked(page, "2");
    assert(await page.locator(".cm-line-comments").count() === 0, "a comment gutter column of its own exists");
    assert(!(await badgeHidden(page)) && await badge(page) === "1", "the menu badge does not count the note");
  });

  await run("the highlight spans the whole gutter, not just the number cell", async () => {
    const counts = await page.evaluate(() => ({
      all: document.querySelectorAll(".cm-gutters .cm-comment-line").length,
      numbers: document.querySelectorAll(".cm-lineNumbers .cm-comment-line").length,
    }));
    assert(counts.numbers === 1, `${counts.numbers} number cells carry the mark`);
    assert(counts.all > counts.numbers, `the mark stops at the number cell (${counts.all} cells in all gutters)`);
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
    await page.click(".cm-content");
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
    await page.click(".cm-content");
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
    await page.click(".cm-content");
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
    await page.click(".cm-content");
    await page.keyboard.press("Control+z");
    await page.waitForFunction(() => document.querySelectorAll(".cm-lineNumbers .cm-comment-line-outdated").length === 0, null, { timeout: 6000 });
    await waitMarked(page, "3");
    await page.keyboard.press("Control+s");
    await sleep(1200);
  });

  await run("cut and paste elsewhere drags the marker along without a save", async () => {
    await page.click(".cm-content");
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
    await page.click(".cm-content");
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
    await page.click(".cm-content");
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

  await run("Ctrl+Alt+C comments the cursor line", async () => {
    await page.click(".cm-content");
    await page.keyboard.press("Control+Home");
    await page.keyboard.press("Control+Alt+C");
    await commentDialogShown(page);
    assert(await page.locator("[data-editor-comment-title]").textContent() === "Line comment", "the shortcut does not open the add dialog");
    await page.fill("[data-editor-comment-text]", "cursor note");
    await page.click("[data-editor-comment-save]");
    await commentDialogGone(page);
    await waitMarked(page, "1,3");
    assert(await badge(page) === "2", "the badge does not count both notes");
  });

  await run("Ctrl+Alt+C opens the existing note, a right click opens the menu too", async () => {
    await page.click(".cm-content");
    await page.keyboard.press("Control+Home");
    await page.keyboard.press("Control+Alt+C");
    await commentDialogShown(page);
    assert(await page.locator("[data-editor-comment-title]").textContent() === "Edit line comment", "the shortcut does not open the existing note");
    assert(await page.locator("[data-editor-comment-text]").inputValue() === "cursor note", "the dialog does not hold the note");
    await page.click("[data-editor-comment-cancel]");
    await commentDialogGone(page);
    await openLineMenu(page, 1, { button: "right" });
    const labels = await menuLabels(page);
    assert(labels.join("|") === "Edit comment|Delete comment|Copy path:line", `marked line menu reads ${labels.join(", ")}`);
    await page.keyboard.press("Escape");
    await page.waitForSelector(".dc-context-menu", { state: "detached", timeout: 4000 });
  });

  await run("the dialog saves on Ctrl+Enter, the line menu's Delete comment confirms and removes", async () => {
    await openLineMenu(page, 1);
    await pickMenuItem(page, "Edit comment");
    await commentDialogShown(page);
    assert(await page.locator("[data-editor-comment-delete]").count() === 0, "the dialog still carries a Delete button");
    await page.fill("[data-editor-comment-text]", "cursor note, edited");
    await page.locator("[data-editor-comment-text]").press("Control+Enter");
    await commentDialogGone(page);
    await sleep(400);
    let data = await getComments(page);
    assert(data.comments.some((c) => c.text === "cursor note, edited"), "the Ctrl+Enter save did not land");
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
    assert(labels.join("|") === "Go to line|Edit|Delete", `cell menu reads ${labels.join(", ")}`);
    assert(await page.locator(".dc-context-menu .dropdown-item.text-danger", { hasText: "Delete" }).count() === 1, "Delete is not the danger entry");
    assert(await page.locator("[data-editor-sheet]:not([hidden])").count() === 1, "the sheet does not stand behind the menu");
    await pickMenuItem(page, "Go to line");
    await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 6000 });
    await page.waitForFunction(() => (document.querySelector("[data-editor-pos]")?.textContent || "").startsWith("3:"), null, { timeout: 6000 });
  });

  await run("the cell menu edits and deletes with the sheet standing, Enter opens it too", async () => {
    await page.click(".cm-content");
    await page.keyboard.press("Control+Home");
    await page.keyboard.press("Control+Alt+C");
    await commentDialogShown(page);
    await page.fill("[data-editor-comment-text]", "temp note");
    await page.click("[data-editor-comment-save]");
    await commentDialogGone(page);
    await waitMarked(page, "1,3");
    await openSheet(page);
    await page.keyboard.press("ArrowDown");
    await page.keyboard.press("ArrowDown");
    await page.keyboard.press("Enter");
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 6000 });
    await page.keyboard.press("Escape");
    await page.waitForSelector(".dc-context-menu", { state: "detached", timeout: 4000 });
    await sleep(400);
    await sheetCell(page, "temp note").click();
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 6000 });
    await pickMenuItem(page, "Edit");
    await commentDialogShown(page);
    assert(await page.locator("[data-editor-comment-text]").inputValue() === "temp note", "Edit does not hold the note");
    await page.click("[data-editor-comment-cancel]");
    await commentDialogGone(page);
    assert(await page.locator("[data-editor-sheet]:not([hidden])").count() === 1, "the sheet went away under the dialog");
    await sleep(400);
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

  await run("copy as Markdown puts path, comment and quoted line on the clipboard", async () => {
    await openMenu(page);
    await page.click("[data-editor-comments-item]");
    await page.waitForSelector("[data-editor-sheet]:not([hidden])", { timeout: 6000 });
    await page.locator("[data-editor-sheet-body] .dropdown-item", { hasText: "Copy as Markdown" }).click();
    await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 6000 });
    await page.waitForSelector(".dc-toast", { timeout: 6000 });
    if (engine === "chromium") {
      const text = await page.evaluate(() => navigator.clipboard.readText());
      assert(text.includes(`Line comments in ${scratch}:`), `markdown heading is off: ${JSON.stringify(text.split("\n")[0])}`);
      assert(text.includes(`${FILE}:3\nneeds a guard\n> two`), `markdown format is ${JSON.stringify(text)}`);
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

  await run("an outdated note shows in gutter, sheet and Markdown", async () => {
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
    await page.locator("[data-editor-sheet-body] .dropdown-item", { hasText: "Copy as Markdown" }).click();
    await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 6000 });
    await page.waitForSelector(".dc-toast", { timeout: 6000 });
    if (engine === "chromium") {
      const text = await page.evaluate(() => navigator.clipboard.readText());
      assert(text.includes(`${FILE}:4 (outdated)`), `the markdown does not mark the orphan: ${JSON.stringify(text)}`);
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
    await page.click(".cm-content");
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
    await page.click(".cm-content");
    await page.keyboard.press("Control+Home");
    await page.keyboard.insertText("fresh\n");
    await page.keyboard.press("Control+Home");
    await page.keyboard.press("Control+Alt+C");
    await commentDialogShown(page);
    await page.fill("[data-editor-comment-text]", "dirty born");
    await page.click("[data-editor-comment-save]");
    await commentDialogGone(page);
    await waitMarked(page, "1");
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

  await run("phone: tapping a line number opens the menu, the sheet reads on touch", async () => {
    const mp = await mobilePage();
    await sleep(600);
    await openEditor(mp);
    await openFile(mp);
    await lineCell(mp, 2).tap();
    await mp.waitForSelector(".dc-context-menu", { state: "visible", timeout: 6000 });
    await mp.locator(".dc-context-menu .dropdown-item", { hasText: "Add line comment" }).click();
    await commentDialogShown(mp);
    await mp.fill("[data-editor-comment-text]", "phone note");
    await mp.click("[data-editor-comment-save]");
    await commentDialogGone(mp);
    await waitMarked(mp, "2");
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
