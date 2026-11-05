const L = require("./lib");
const { assert, sleep } = L;

// Not a runner of its own: editor-comments.js runs these checks in every
// engine over its scratch project. A line comment is a thread (POST
// .../editor/comments/reply adds or edits a reply, .../comments/reply/delete
// takes one, .../comments/resolve resolves or reopens): Open thread in the
// line's menu shows the first note and every reply in the dialog
// (data-editor-comment-thread, one data-comment-note each with its author,
// You for the owner's own, the time it was written and the note as written,
// line breaks and indents kept, the server's rendering of the thread pulled
// from .../comments/thread), a pencil on each note to edit it in the
// field (Save, the resolve switch gone meanwhile) and a bin on each reply,
// which closes the dialog for the swal confirm. The field below takes a
// reply (an empty one marks it invalid with "A reply is required."), and the
// switch under it resolves the thread or opens it again, with the reply or
// alone, the button then reading Resolve or Reopen. The line keeps one
// marker for the thread, green while it is resolved, the sheet's cell counts
// the replies behind the line, and an open thread follows a reply another
// device posts. Deleting the comment says it takes its replies along.
//
// A project comment is the same thread on the whole project: the sheet heads
// it under Project, above the line comments under Files, with a field to add
// one (data-project-comment-text, Add alone on its right edge below or
// Ctrl+Enter, an empty one marked invalid). It marks no line, counts in the
// menu's badge, its cell reads Project comment with its replies and opens the
// menu without Go to line, its thread is titled Project comment without a
// place, the pencil edits it as one, and the field goes while the sheet's
// filter narrows. A project comment another device adds reaches the open
// sheet while the field keeps what was typed and the focus.

module.exports.threadChecks = async function threadChecks({ page, run, scratch, file, openEditor, openFile, openLineMenu, menuLabels, pickMenuItem, waitMarked, getComments, openSheet, sheetCell, commentDialogShown, commentDialogGone, confirmDialogGone }) {
  const comments = `/projects/${encodeURIComponent(scratch)}/editor/comments`;
  const post = (path, body) => page.evaluate(([p, b]) => fetch(p, {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]').content, Accept: "application/json" },
    body: JSON.stringify(b),
  }).then((r) => r.status), [path, body]);
  const thread = async () => (await getComments(page)).comments.find((c) => c.text === "a thread");
  const notes = () => page.locator("[data-editor-comment-thread] [data-comment-note]").evaluateAll((els) => els.map((el) => ({
    author: el.querySelector("[data-comment-note-author]").textContent,
    when: el.querySelector("dc-time")?.textContent || "",
    text: el.querySelector("[data-comment-note-text]").textContent,
    buttons: [...el.querySelectorAll("button")].map((b) => b.getAttribute("aria-label")).join(","),
  })));
  const shows = (selector) => page.locator(selector).evaluate((el) => getComputedStyle(el).display !== "none");
  const saveLabel = () => page.locator("[data-editor-comment-save]").textContent();
  const noteButton = (n, label) => page.locator("[data-editor-comment-thread] [data-comment-note]").nth(n).locator(`button[aria-label="${label}"]`);
  const openThread = async (line) => {
    await openLineMenu(page, line);
    await pickMenuItem(page, "Open thread");
    await commentDialogShown(page);
  };
  const closeThread = async () => {
    await page.click("[data-editor-comment-cancel]");
    await commentDialogGone(page);
  };

  await run("a comment opens as its thread with author and time, a reply joins it under the same marker", async () => {
    await openEditor(page);
    await openFile(page);
    const added = await post(comments, { path: file, line: 2, text: "a thread" });
    assert(added === 200, `the comment answered ${added}`);
    await waitMarked(page, "2");
    await openThread(2);
    const first = await notes();
    assert(first.length === 1 && first[0].author === "You" && first[0].when === "just now" && first[0].text === "a thread" && first[0].buttons === "Edit comment", `the thread reads ${JSON.stringify(first)}`);
    assert(await saveLabel() === "Reply" && await page.locator("[data-editor-comment-text]").inputValue() === "", "the thread offers no reply");
    assert(await shows("[data-editor-comment-resolve-row]") && (await page.locator("[data-editor-comment-resolve-row]").textContent()).trim() === "Resolve the thread", "the resolve switch is missing");
    assert(!(await shows("[data-editor-comment-hint]")), "the add's hint stands over a reply");
    await page.click("[data-editor-comment-save]");
    assert(await page.locator("[data-editor-comment-text].is-invalid").count() === 1, "an empty reply does not mark the field");
    assert(await page.locator("[data-editor-comment-invalid]").textContent() === "A reply is required.", "the empty reply's message is off");
    await page.fill("[data-editor-comment-text]", "first answer\n  indented");
    await page.locator("[data-editor-comment-text]").press("Control+Enter");
    await commentDialogGone(page);
    await sleep(300);
    const c = await thread();
    assert(c.replies.length === 1 && c.replies[0].text === "first answer\n  indented" && !c.replies[0].author && c.replies[0].updatedAt, `the reply is stored as ${JSON.stringify(c)}`);
    await waitMarked(page, "2");
    await openSheet(page);
    const cell = await sheetCell(page, "a thread").textContent();
    assert(cell.includes(`${file}:2 · 1 reply`), `the cell reads ${cell}`);
    await page.keyboard.press("Control+Shift+C");
    await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 6000 });
    await openThread(2);
    const both = await notes();
    assert(both.length === 2 && both[1].author === "You" && both[1].text === "first answer\n  indented" && both[1].buttons === "Edit reply,Delete reply", `the thread reads ${JSON.stringify(both)}`);
    const lines = await page.locator("[data-editor-comment-thread] [data-comment-note-text]").nth(1).evaluate((el) => Math.round(el.getBoundingClientRect().height / parseFloat(getComputedStyle(el).lineHeight)));
    assert(lines === 2, `the reply stands on ${lines} lines`);
    await closeThread();
  });

  await run("an open thread follows a reply another device posts", async () => {
    await openThread(2);
    const status = await post(`${comments}/reply`, { thread: (await thread()).id, text: "from the phone" });
    assert(status === 200, `the other device's reply answered ${status}`);
    await page.waitForFunction(() => [...document.querySelectorAll("[data-editor-comment-thread] [data-comment-note-text]")].some((el) => el.textContent === "from the phone"), null, { timeout: 8000 });
    await closeThread();
  });

  await run("the pencil edits a reply in the field, its bin deletes it behind a confirm", async () => {
    await openThread(2);
    await noteButton(2, "Edit reply").click();
    assert(await page.locator("[data-editor-comment-title]").textContent() === "Edit reply", "the pencil does not edit the reply");
    assert(await page.locator("[data-editor-comment-text]").inputValue() === "from the phone" && await saveLabel() === "Save", "the field does not hold the reply");
    assert(!(await shows("[data-editor-comment-resolve-row]")), "the resolve switch stands while a note is edited");
    await page.fill("[data-editor-comment-text]", "from the phone, edited");
    await page.click("[data-editor-comment-save]");
    await commentDialogGone(page);
    await sleep(300);
    let c = await thread();
    assert(c.replies.length === 2 && c.replies[1].text === "from the phone, edited", `the edit is stored as ${JSON.stringify(c.replies)}`);
    await openThread(2);
    await noteButton(2, "Delete reply").click();
    await commentDialogGone(page);
    await page.waitForSelector(".swal2-confirm", { timeout: 6000 });
    assert((await page.locator(".swal2-title").textContent()).includes("Delete this reply"), "no confirm before the reply's delete");
    assert((await page.locator(".swal2-html-container").textContent()).includes("from the phone, edited"), "the confirm does not name the reply");
    await page.click(".swal2-confirm");
    await confirmDialogGone(page);
    await sleep(400);
    c = await thread();
    assert(c.replies.length === 1 && c.replies[0].text === "first answer\n  indented", `the delete left ${JSON.stringify(c.replies)}`);
  });

  await run("the switch resolves the thread without a reply and opens it again with one, the line green meanwhile", async () => {
    await openThread(2);
    await page.check("[data-editor-comment-resolve]");
    assert(await saveLabel() === "Resolve", "the button does not say it resolves");
    await page.fill("[data-editor-comment-text]", "x");
    assert(await saveLabel() === "Reply", "a typed reply does not turn the button back");
    await page.fill("[data-editor-comment-text]", "");
    await page.click("[data-editor-comment-save]");
    await commentDialogGone(page);
    await page.waitForFunction(() => document.querySelectorAll(".cm-lineNumbers .cm-comment-line-resolved").length === 1, null, { timeout: 6000 });
    let c = await thread();
    assert(c.resolved && c.replies.length === 1, `the resolve is stored as ${JSON.stringify(c)}`);
    await openThread(2);
    assert(await shows("[data-editor-comment-author]") && await page.locator("[data-editor-comment-author]").textContent() === "Resolved", "the thread does not say it is resolved");
    assert((await page.locator("[data-editor-comment-resolve-row]").textContent()).trim() === "Reopen the thread", "the switch does not reopen");
    await page.fill("[data-editor-comment-text]", "one more thing");
    await page.check("[data-editor-comment-resolve]");
    assert(await saveLabel() === "Reply", "a reply with the switch does not read Reply");
    await page.click("[data-editor-comment-save]");
    await commentDialogGone(page);
    await page.waitForFunction(() => document.querySelectorAll(".cm-lineNumbers .cm-comment-line-resolved").length === 0, null, { timeout: 6000 });
    await waitMarked(page, "2");
    c = await thread();
    assert(!c.resolved && c.replies.length === 2 && c.replies[1].text === "one more thing", `the reopen is stored as ${JSON.stringify(c)}`);
  });

  await run("deleting the comment says it takes its replies along", async () => {
    await openLineMenu(page, 2);
    const labels = await menuLabels(page);
    assert(labels.join("|") === "Open thread|Delete comment|Copy path:line", `the line's menu reads ${labels.join(", ")}`);
    await pickMenuItem(page, "Delete comment");
    await page.waitForSelector(".swal2-confirm", { timeout: 6000 });
    const text = await page.locator(".swal2-html-container").textContent();
    assert(text.includes("a thread") && text.includes("It takes its 2 replies along."), `the confirm reads ${text}`);
    await page.click(".swal2-confirm");
    await confirmDialogGone(page);
    await page.waitForFunction(() => document.querySelectorAll(".cm-lineNumbers .cm-comment-line").length === 0, null, { timeout: 6000 });
    assert((await getComments(page)).comments.length === 0, "the thread stands after its delete");
  });

  const field = "[data-project-comment-text]";
  const projectCell = (text) => page.locator("[data-editor-sheet-body] .row .dropdown-item", { hasText: text });
  const sheetOrder = () => page.$$eval("[data-editor-sheet-body] [data-editor-sheet-head], [data-editor-sheet-body] [data-editor-project-comment-form], [data-editor-sheet-body] .row .dropdown-item", (els) => els
    .filter((el) => el.getClientRects().length > 0)
    .map((el) => (el.matches("[data-editor-sheet-head]") ? `# ${el.textContent}` : el.matches("[data-editor-project-comment-form]") ? "field" : el.querySelector(".dc-menu-label-head").textContent + (el.querySelector(".dc-menu-label-tail")?.textContent || ""))));
  const badge = () => page.locator("[data-editor-comments-count]").evaluate((el) => (getComputedStyle(el).display === "none" ? "" : el.textContent.trim()));
  const wide = async (text) => (await getComments(page)).comments.find((c) => c.text === text);

  await run("the sheet heads a field for project comments, an add stands first and marks no line", async () => {
    await sleep(400);
    await openSheet(page);
    assert((await sheetOrder()).join("|") === "# Project|field|# Files", `the empty sheet reads ${(await sheetOrder()).join(", ")}`);
    assert((await page.textContent("[data-editor-sheet-body]")).includes("No comments yet. Click a line number to add one."), "the empty sheet does not ask for a line comment");
    const foot = await page.$eval("[data-editor-sheet-body] [data-editor-project-comment-form]", (form) => {
      const add = form.querySelector("[data-project-comment-add]");
      const box = form.querySelector("[data-project-comment-text]").getBoundingClientRect();
      return { text: add.parentElement.textContent.replace(/\s+/g, " ").trim(), flush: Math.abs(add.getBoundingClientRect().right - box.right) <= 1 };
    });
    assert(foot.text === "Add" && foot.flush, `the field's foot reads ${JSON.stringify(foot)}`);
    await page.click("[data-project-comment-add]");
    assert(await page.locator(`${field}.is-invalid`).count() === 1 && await shows("[data-editor-project-comment-form] .invalid-feedback"), "an empty add does not mark the field");
    await page.fill(field, "on the whole project");
    assert(await page.locator(`${field}.is-invalid`).count() === 0, "typing keeps the field marked");
    await page.locator(field).press("Control+Enter");
    await projectCell("on the whole project").waitFor({ timeout: 6000 });
    const after = await page.evaluate((f) => ({ value: document.querySelector(f).value, focused: document.activeElement === document.querySelector(f), active: document.activeElement?.outerHTML.slice(0, 60) }), field);
    assert(after.value === "" && after.focused, `the field is not emptied with the focus kept: ${JSON.stringify(after)}`);
    const c = await wide("on the whole project");
    assert(c && c.project && c.path === "" && c.line === 0 && !c.lineText && !c.outdated, `the project comment is stored as ${JSON.stringify(c)}`);
    assert(await page.locator(".cm-comment-line").count() === 0, "the project comment marks a line");
    assert(await badge() === "1", `the badge reads ${await badge()}`);
    const added = await post(comments, { path: file, line: 3, text: "on a line" });
    assert(added === 200, `the line comment answered ${added}`);
    await projectCell("on a line").waitFor({ timeout: 6000 });
    await waitMarked(page, "3");
    const order = (await sheetOrder()).join("|");
    assert(order === `# Project|field|Project comment|# Files|${file}:3`, `the sheet reads ${order}`);
    assert(await badge() === "2", `the badge reads ${await badge()}`);
    await page.fill("[data-editor-sheet-filter]", "whole");
    await sleep(250);
    const narrowed = (await sheetOrder()).join("|");
    assert(narrowed === "# Project|Project comment" && !(await shows("[data-editor-project-comment-form]")), `the filter leaves ${narrowed}`);
    await page.fill("[data-editor-sheet-filter]", "");
    await sleep(250);
    assert(await shows("[data-editor-project-comment-form]"), "the field does not come back with the filter cleared");
  });

  await run("a project comment's cell opens its thread without a place, a reply, an edit and a resolve work there", async () => {
    await projectCell("on the whole project").click();
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 6000 });
    const labels = await menuLabels(page);
    assert(labels.join("|") === "Open thread|Delete", `the project comment's menu reads ${labels.join(", ")}`);
    await pickMenuItem(page, "Open thread");
    await commentDialogShown(page);
    assert(await page.locator("[data-editor-comment-title]").textContent() === "Project comment", "the thread is not titled Project comment");
    assert(!(await shows("[data-editor-comment-place]")) && !(await shows("[data-editor-comment-code]")), "the thread names a place");
    const first = await notes();
    assert(first.length === 1 && first[0].author === "You" && first[0].text === "on the whole project" && first[0].buttons === "Edit comment", `the thread reads ${JSON.stringify(first)}`);
    await page.fill("[data-editor-comment-text]", "a project reply");
    await page.locator("[data-editor-comment-text]").press("Control+Enter");
    await commentDialogGone(page);
    await page.waitForFunction(() => [...document.querySelectorAll("[data-editor-sheet-body] .row .dropdown-item")].some((el) => el.textContent.includes("Project comment · 1 reply")), null, { timeout: 6000 });
    await sleep(400);
    await projectCell("on the whole project").click();
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 6000 });
    await pickMenuItem(page, "Open thread");
    await commentDialogShown(page);
    await noteButton(0, "Edit comment").click();
    assert(await page.locator("[data-editor-comment-title]").textContent() === "Edit project comment", "the pencil does not edit a project comment");
    await page.fill("[data-editor-comment-text]", "on the whole project, edited");
    await page.click("[data-editor-comment-save]");
    await commentDialogGone(page);
    await projectCell("on the whole project, edited").waitFor({ timeout: 6000 });
    let c = await wide("on the whole project, edited");
    assert(c && c.project && c.path === "" && c.line === 0 && c.replies.length === 1 && c.replies[0].text === "a project reply", `the edit is stored as ${JSON.stringify(c)}`);
    await sleep(400);
    await projectCell("on the whole project, edited").click();
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 6000 });
    await pickMenuItem(page, "Open thread");
    await commentDialogShown(page);
    await page.check("[data-editor-comment-resolve]");
    assert(await saveLabel() === "Resolve", "the project thread does not resolve");
    await page.click("[data-editor-comment-save]");
    await commentDialogGone(page);
    await page.waitForFunction(() => [...document.querySelectorAll("[data-editor-sheet-body] .row .dropdown-item")].some((el) => el.textContent.includes("on the whole project, edited") && el.querySelector(".ti-message-circle.text-green")), null, { timeout: 6000 });
    c = await wide("on the whole project, edited");
    assert(c.resolved && c.project, `the resolve is stored as ${JSON.stringify(c)}`);
    assert(await page.locator(".cm-comment-line-resolved").count() === 0, "the resolved project comment marks a line");
    await sleep(400);
    await projectCell("on a line").click();
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 6000 });
    await pickMenuItem(page, "Open thread");
    await commentDialogShown(page);
    assert(await page.locator("[data-editor-comment-title]").textContent() === "Line comment" && await shows("[data-editor-comment-place]") && await page.locator("[data-editor-comment-place]").textContent() === `${file}:3`, "a line comment's thread lost its place after a project comment's");
    await closeThread();
  });

  await run("a project comment from another device reaches the open sheet, the field keeps what was typed", async () => {
    await page.click(field);
    await page.keyboard.type("half typed");
    const added = await post(comments, { project: true, text: "from another device" });
    assert(added === 200, `the other device's project comment answered ${added}`);
    await projectCell("from another device").waitFor({ timeout: 8000 });
    assert(await page.locator(field).inputValue() === "half typed" && await page.evaluate((f) => document.activeElement === document.querySelector(f), field), "the repaint took the typed field or its focus");
    await page.fill(field, "");
    await sleep(400);
    await projectCell("from another device").click();
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 6000 });
    await pickMenuItem(page, "Delete");
    await page.waitForSelector(".swal2-confirm", { timeout: 6000 });
    const text = await page.locator(".swal2-html-container").textContent();
    assert(text.includes("Project comment") && text.includes("from another device"), `the confirm reads ${text}`);
    await page.click(".swal2-confirm");
    await confirmDialogGone(page);
    await page.waitForFunction(() => ![...document.querySelectorAll("[data-editor-sheet-body] .dropdown-item")].some((el) => el.textContent.includes("from another device")), null, { timeout: 6000 });
    assert(!(await wide("from another device")), "the project comment stands after its delete");
    const cleared = await post(`${comments}/delete`, { all: true });
    assert(cleared === 200, `clearing answered ${cleared}`);
    await page.waitForFunction(() => document.querySelectorAll("[data-editor-sheet-body] .row .dropdown-item").length === 0, null, { timeout: 6000 });
    await page.keyboard.press("Escape");
    await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 6000 });
  });
};
