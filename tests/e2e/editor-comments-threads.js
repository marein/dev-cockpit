const L = require("./lib");
const { assert, sleep } = L;

// Not a runner of its own: editor-comments.js runs these checks in every
// engine over its scratch project. A line comment is a thread (POST
// .../editor/comments/reply adds or edits a reply, .../comments/reply/delete
// takes one, .../comments/resolve resolves or reopens): Open thread in the
// line's menu shows the first note and every reply in the dialog
// (data-editor-comment-thread, one data-comment-note each with its author,
// You for the owner's own, the time it was written and the note as written,
// line breaks and indents kept), a pencil on each note to edit it in the
// field (Save, the resolve switch gone meanwhile) and a bin on each reply,
// which closes the dialog for the swal confirm. The field below takes a
// reply (an empty one marks it invalid with "A reply is required."), and the
// switch under it resolves the thread or opens it again, with the reply or
// alone, the button then reading Resolve or Reopen. The line keeps one
// marker for the thread, green while it is resolved, the sheet's cell counts
// the replies behind the line, and an open thread follows a reply another
// device posts. Deleting the comment says it takes its replies along.

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
    when: el.querySelector("time")?.textContent || "",
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
};
