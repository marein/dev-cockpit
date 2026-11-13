const L = require("./lib");
const { assert, sleep } = L;

// Not a runner of its own: editor-comments.js runs these checks in every
// engine over its scratch project. A line comment is a thread (POST
// .../editor/comments/reply adds or edits a reply, .../comments/reply/delete
// takes one, .../comments/resolve resolves or reopens): a click on its line
// number unfolds the server's card of it under the line (data-line-thread,
// pulled from .../comments/thread), the first note and every reply, one
// data-comment-note each with its author, You for the owner's own, the time it
// was written and the note as written, line breaks and indents kept, a pencil
// on each note that turns its text into a field holding it (Save, Cancel) and
// a bin on each reply, which asks the swal confirm. Reply opens
// the field (an empty one marks it invalid with "A reply is required."), and
// the switch under it resolves the thread or opens it again, with the reply
// or alone, the button then reading Resolve or Reopen; Resolve and Reopen
// stand beside Reply too. The line keeps one marker for the thread, green
// while it is resolved, the card's head saying Resolved, the sheet's cell
// counts the replies behind the line, and an unfolded thread follows a reply
// another device posts. Deleting the comment says it takes its replies along.
//
// A project comment is the same thread on the whole project: the sheet heads
// it under Project, above the line comments under Files, with a field to add
// one (data-project-comment-text, Add alone on its right edge below or
// Ctrl+Enter, an empty one marked invalid). It marks no line, counts in the
// menu's badge, its cell reads Project comment with its replies and opens the
// menu without Go to line, Open thread shows its card in the dialog titled
// Project comment without a place, the pencil edits it there in place as one,
// Escape in its field leaving the note and the dialog standing, and the field
// goes while the sheet's filter narrows. A project comment another device
// adds reaches the open sheet while the field keeps what was typed and the
// focus.

module.exports.threadChecks = async function threadChecks({ page, run, scratch, file, openEditor, openFile, openLineMenu, menuLabels, pickMenuItem, waitMarked, getComments, openSheet, sheetCell, commentDialogShown, commentDialogGone, confirmDialogGone }) {
  const comments = `/projects/${encodeURIComponent(scratch)}/editor/comments`;
  const post = (path, body) => page.evaluate(([p, b]) => fetch(p, {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]').content, Accept: "application/json" },
    body: JSON.stringify(b),
  }).then((r) => r.status), [path, body]);
  const thread = async () => (await getComments(page)).comments.find((c) => c.text === "a thread");
  const noteRows = (box) => box.locator("[data-comment-note]").evaluateAll((els) => els.map((el) => ({
    author: el.querySelector("[data-comment-note-author]").textContent,
    when: el.querySelector("dc-time")?.textContent || "",
    text: el.querySelector("[data-comment-note-text]").textContent,
    buttons: [...el.querySelectorAll("button[aria-label]")].map((b) => b.getAttribute("aria-label")).join(","),
  })));
  const card = () => L.lineThread(page, "a thread");
  const notes = () => noteRows(card());
  const shows = (selector) => page.locator(selector).evaluate((el) => getComputedStyle(el).display !== "none");
  const sendLabel = (box = card()) => box.locator("[data-line-thread-reply-send]").textContent();
  const reply = (box = card()) => box.locator("[data-line-thread-reply-text]");
  const noteButton = (n, label) => card().locator("[data-comment-note]").nth(n).locator(`button[aria-label="${label}"]`);
  const unfold = async () => {
    if (!(await L.seen(card()))) await L.pressLine(page, 2);
    await card().waitFor({ timeout: 6000 });
  };
  const fold = async () => {
    await L.pressLine(page, 2);
    await page.waitForFunction(() => !document.querySelector(".cm-content [data-line-thread]"), null, { timeout: 6000 });
  };

  await run("a comment unfolds as its thread with author and time, a reply joins it under the same marker", async () => {
    await openEditor(page);
    await openFile(page);
    const added = await post(comments, { path: file, line: 2, text: "a thread" });
    assert(added === 200, `the comment answered ${added}`);
    await waitMarked(page, "2");
    await unfold();
    const first = await notes();
    assert(first.length === 1 && first[0].author === "You" && first[0].when === "just now" && first[0].text === "a thread" && first[0].buttons === "Edit comment", `the thread reads ${JSON.stringify(first)}`);
    assert(await L.seen(card().locator("[data-line-thread-reply-open]")) && await L.seen(card().locator("[data-line-thread-resolve]")), "the thread offers no Reply or Resolve");
    assert(!(await L.seen(card().locator("[data-line-thread-reply]"))), "the reply form stands before Reply");
    await card().locator("[data-line-thread-reply-open]").click();
    assert(await L.seen(reply()) && await sendLabel() === "Reply" && await reply().inputValue() === "", "Reply opens no empty field");
    assert(await L.seen(card().locator("[data-line-thread-reply-resolve]")) && (await card().locator("[data-line-thread-reply] label.form-check").textContent()).trim() === "Resolve the thread", "the resolve switch is missing");
    assert(await card().locator("[data-line-thread-reply] .form-hint").count() === 0, "a hint stands over a local reply");
    await card().locator("[data-line-thread-reply-send]").click();
    assert(await card().locator("[data-line-thread-reply-text].is-invalid").count() === 1, "an empty reply does not mark the field");
    assert(await card().locator("[data-line-thread-reply] .invalid-feedback").textContent() === "A reply is required.", "the empty reply's message is off");
    await reply().fill("first answer\n  indented");
    await reply().press("Control+Enter");
    await page.waitForFunction(() => [...document.querySelectorAll(".cm-content [data-comment-note-text]")].some((el) => el.textContent === "first answer\n  indented"), null, { timeout: 6000 });
    assert(!(await L.seen(card().locator("[data-line-thread-reply]"))), "the reply's field stands after it went");
    const c = await thread();
    assert(c.replies.length === 1 && c.replies[0].text === "first answer\n  indented" && !c.replies[0].author && c.replies[0].updatedAt, `the reply is stored as ${JSON.stringify(c)}`);
    await waitMarked(page, "2");
    const both = await notes();
    assert(both.length === 2 && both[1].author === "You" && both[1].text === "first answer\n  indented" && both[1].buttons === "Edit reply,Delete reply", `the thread reads ${JSON.stringify(both)}`);
    const lines = await card().locator("[data-comment-note-text]").nth(1).evaluate((el) => Math.round(el.getBoundingClientRect().height / parseFloat(getComputedStyle(el).lineHeight)));
    assert(lines === 2, `the reply stands on ${lines} lines`);
    await openSheet(page);
    const cell = await sheetCell(page, "a thread").textContent();
    assert(cell.includes(`${file}:2 · 1 reply`), `the cell reads ${cell}`);
    await page.keyboard.press("Control+Shift+C");
    await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 6000 });
  });

  await run("an unfolded thread follows a reply another device posts, a reply being typed stays", async () => {
    await unfold();
    await card().locator("[data-line-thread-reply-open]").click();
    await reply().click();
    await page.keyboard.type("half a reply");
    const status = await post(`${comments}/reply`, { thread: (await thread()).id, text: "from the phone" });
    assert(status === 200, `the other device's reply answered ${status}`);
    await page.waitForFunction(() => [...document.querySelectorAll(".cm-content [data-line-thread] [data-comment-note-text]")].some((el) => el.textContent === "from the phone"), null, { timeout: 8000 });
    const kept = await page.evaluate(() => ({ value: document.activeElement?.value, field: !!document.activeElement?.matches("[data-line-thread-reply-text]") }));
    assert(kept.field && kept.value === "half a reply", `the reply being typed did not stay: ${JSON.stringify(kept)}`);
    await card().locator("[data-line-thread-reply-cancel]").click();
    assert(!(await L.seen(card().locator("[data-line-thread-reply]"))), "Cancel left the reply open");
  });

  await run("the pencil edits a reply in place, its bin deletes it behind a confirm", async () => {
    await unfold();
    await noteButton(2, "Edit reply").click();
    const field = card().locator("[data-comment-note]").nth(2).locator("[data-comment-note-field]");
    assert(await L.seen(field) && await field.inputValue() === "from the phone" && !(await L.seen(card().locator("[data-line-thread-reply]"))), "the reply's text did not turn into its field");
    await field.fill("");
    await card().locator("[data-comment-note]").nth(2).locator("[data-comment-note-save]").click();
    assert(await card().locator("[data-comment-note]").nth(2).locator(".invalid-feedback").textContent() === "A reply is required.", "an emptied edit is not refused at the field");
    await field.fill("from the phone, edited");
    await card().locator("[data-comment-note]").nth(2).locator("[data-comment-note-save]").click();
    await page.waitForFunction(() => [...document.querySelectorAll(".cm-content [data-comment-note-text]")].some((el) => el.textContent === "from the phone, edited" && el.getClientRects().length), null, { timeout: 6000 });
    let c = await thread();
    assert(c.replies.length === 2 && c.replies[1].text === "from the phone, edited", `the edit is stored as ${JSON.stringify(c.replies)}`);
    assert(!(await L.seen(field)) && await L.seen(card().locator("[data-line-thread-reply-open]")), "the field stands after the save");
    await noteButton(2, "Delete reply").click();
    await page.waitForSelector(".swal2-confirm", { timeout: 6000 });
    assert((await page.locator(".swal2-title").textContent()).includes("Delete this reply"), "no confirm before the reply's delete");
    assert((await page.locator(".swal2-html-container").textContent()).includes("from the phone, edited"), "the confirm does not name the reply");
    await page.click(".swal2-confirm");
    await confirmDialogGone(page);
    await page.waitForFunction(() => ![...document.querySelectorAll(".cm-content [data-comment-note-text]")].some((el) => el.textContent === "from the phone, edited"), null, { timeout: 6000 });
    c = await thread();
    assert(c.replies.length === 1 && c.replies[0].text === "first answer\n  indented", `the delete left ${JSON.stringify(c.replies)}`);
  });

  await run("the switch resolves the thread without a reply and opens it again with one, the line green meanwhile", async () => {
    await unfold();
    await card().locator("[data-line-thread-reply-open]").click();
    await card().locator("[data-line-thread-reply-resolve]").check();
    assert(await sendLabel() === "Resolve", "the button does not say it resolves");
    await reply().fill("x");
    assert(await sendLabel() === "Reply", "a typed reply does not turn the button back");
    await reply().fill("");
    assert(await sendLabel() === "Resolve", "an emptied field does not say it resolves again");
    await card().locator("[data-line-thread-reply-send]").click();
    await page.waitForFunction(() => document.querySelectorAll(".cm-lineNumbers .cm-comment-line-resolved").length === 1, null, { timeout: 6000 });
    let c = await thread();
    assert(c.resolved && c.replies.length === 1, `the resolve is stored as ${JSON.stringify(c)}`);
    await page.waitForFunction(() => !!document.querySelector(".cm-content [data-line-thread] [data-line-thread-reopen]"), null, { timeout: 6000 });
    assert(await L.seen(card().locator("[data-line-thread-resolved]")) && (await card().locator("[data-line-thread-resolved]").textContent()) === "Resolved", "the thread does not say it is resolved");
    assert(!(await L.seen(card().locator("[data-line-thread-resolve]"))), "a resolved thread still offers Resolve");
    await card().locator("[data-line-thread-reply-open]").click();
    assert((await card().locator("[data-line-thread-reply] label.form-check").textContent()).trim() === "Reopen the thread", "the switch does not reopen");
    await reply().fill("one more thing");
    await card().locator("[data-line-thread-reply-resolve]").check();
    assert(await sendLabel() === "Reply", "a reply with the switch does not read Reply");
    await card().locator("[data-line-thread-reply-send]").click();
    await page.waitForFunction(() => document.querySelectorAll(".cm-lineNumbers .cm-comment-line-resolved").length === 0, null, { timeout: 6000 });
    await waitMarked(page, "2");
    c = await thread();
    assert(!c.resolved && c.replies.length === 2 && c.replies[1].text === "one more thing", `the reopen is stored as ${JSON.stringify(c)}`);
    await page.waitForFunction(() => !!document.querySelector(".cm-content [data-line-thread] [data-line-thread-resolve]"), null, { timeout: 6000 });
    await card().locator("[data-line-thread-resolve]").click();
    await page.waitForFunction(() => document.querySelectorAll(".cm-lineNumbers .cm-comment-line-resolved").length === 1, null, { timeout: 6000 });
    await page.waitForFunction(() => !!document.querySelector(".cm-content [data-line-thread] [data-line-thread-reopen]"), null, { timeout: 6000 });
    await card().locator("[data-line-thread-reopen]").click();
    await page.waitForFunction(() => document.querySelectorAll(".cm-lineNumbers .cm-comment-line-resolved").length === 0, null, { timeout: 6000 });
    c = await thread();
    assert(!c.resolved && c.replies.length === 2, `Resolve and Reopen beside Reply left ${JSON.stringify(c)}`);
    await fold();
  });

  await run("deleting the comment says it takes its replies along", async () => {
    await openLineMenu(page, 2);
    const labels = await menuLabels(page);
    assert(labels.join("|") === "Open thread|Delete comment|Copy path:line", `the line's menu reads ${labels.join(", ")}`);
    await page.keyboard.press("Escape");
    await page.waitForSelector(".dc-context-menu", { state: "detached", timeout: 4000 });
    await unfold();
    await card().locator("[data-line-thread-delete]").click();
    await page.waitForSelector(".swal2-confirm", { timeout: 6000 });
    const text = await page.locator(".swal2-html-container").textContent();
    assert(text.includes("a thread") && text.includes("It takes its 2 replies along."), `the confirm reads ${text}`);
    await page.click(".swal2-confirm");
    await confirmDialogGone(page);
    await page.waitForFunction(() => document.querySelectorAll(".cm-lineNumbers .cm-comment-line").length === 0, null, { timeout: 6000 });
    await page.waitForFunction(() => !document.querySelector(".cm-content [data-line-block]"), null, { timeout: 6000 });
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

  await run("a project comment's cell opens its thread in the dialog without a place, a reply, an edit and a resolve work there", async () => {
    const dialogCard = page.locator("[data-editor-comment-modal] [data-line-thread]");
    const openProjectThread = async (text) => {
      await sleep(400);
      await projectCell(text).click();
      await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 6000 });
      await pickMenuItem(page, "Open thread");
      await commentDialogShown(page);
      await dialogCard.waitFor({ timeout: 6000 });
    };
    await projectCell("on the whole project").click();
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 6000 });
    const labels = await menuLabels(page);
    assert(labels.join("|") === "Open thread|Delete", `the project comment's menu reads ${labels.join(", ")}`);
    await page.keyboard.press("Escape");
    await page.waitForSelector(".dc-context-menu", { state: "detached", timeout: 4000 });
    await openProjectThread("on the whole project");
    assert(await page.locator("[data-editor-comment-title]").textContent() === "Project comment", "the thread is not titled Project comment");
    assert(!(await shows("[data-editor-comment-place]")) && await dialogCard.locator("[data-line-thread-quote]").count() === 0, "the thread names a place");
    const first = await noteRows(dialogCard);
    assert(first.length === 1 && first[0].author === "You" && first[0].text === "on the whole project" && first[0].buttons === "Edit comment", `the thread reads ${JSON.stringify(first)}`);
    await dialogCard.locator("[data-line-thread-reply-open]").click();
    await reply(dialogCard).fill("a project reply");
    await reply(dialogCard).press("Control+Enter");
    await page.waitForFunction(() => [...document.querySelectorAll("[data-editor-comment-modal] [data-comment-note-text]")].some((el) => el.textContent === "a project reply"), null, { timeout: 6000 });
    await page.waitForFunction(() => [...document.querySelectorAll("[data-editor-sheet-body] .row .dropdown-item")].some((el) => el.textContent.includes("Project comment · 1 reply")), null, { timeout: 6000 });
    const lead = dialogCard.locator("[data-comment-note]").nth(0);
    await lead.locator('button[aria-label="Edit comment"]').click();
    assert(await lead.locator("[data-comment-note-field]").inputValue() === "on the whole project" && !(await L.seen(lead.locator("[data-comment-note-text]"))), "the pencil does not edit a project comment in place");
    await page.keyboard.press("Escape");
    await sleep(400);
    assert(await page.locator("[data-editor-comment-modal].show").count() === 1 && await L.seen(lead.locator("[data-comment-note-text]")) && !(await L.seen(lead.locator("[data-comment-note-field]"))), "Escape in the field did not leave the note with the dialog standing");
    await lead.locator('button[aria-label="Edit comment"]').click();
    await lead.locator("[data-comment-note-field]").fill("on the whole project, edited");
    await lead.locator("[data-comment-note-save]").click();
    await projectCell("on the whole project, edited").waitFor({ timeout: 6000 });
    let c = await wide("on the whole project, edited");
    assert(c && c.project && c.path === "" && c.line === 0 && c.replies.length === 1 && c.replies[0].text === "a project reply", `the edit is stored as ${JSON.stringify(c)}`);
    await page.waitForFunction(() => [...document.querySelectorAll("[data-editor-comment-modal] [data-comment-note-text]")].some((el) => el.textContent === "on the whole project, edited"), null, { timeout: 6000 });
    await dialogCard.locator("[data-line-thread-reply-open]").click();
    await dialogCard.locator("[data-line-thread-reply-resolve]").check();
    assert(await sendLabel(dialogCard) === "Resolve", "the project thread does not resolve");
    await dialogCard.locator("[data-line-thread-reply-send]").click();
    await page.waitForFunction(() => [...document.querySelectorAll("[data-editor-sheet-body] .row .dropdown-item")].some((el) => el.textContent.includes("on the whole project, edited") && el.querySelector(".ti-message-circle.text-green")), null, { timeout: 6000 });
    c = await wide("on the whole project, edited");
    assert(c.resolved && c.project, `the resolve is stored as ${JSON.stringify(c)}`);
    assert(await page.locator(".cm-comment-line-resolved").count() === 0, "the resolved project comment marks a line");
    await page.waitForFunction(() => !!document.querySelector("[data-editor-comment-modal] [data-line-thread-reopen]"), null, { timeout: 6000 });
    await page.keyboard.press("Escape");
    await commentDialogGone(page);
    assert(await page.locator("[data-editor-sheet]:not([hidden])").count() === 1, "Escape closed the sheet behind the dialog too");
    await sleep(400);
    await projectCell("on a line").click();
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 6000 });
    await pickMenuItem(page, "Open thread");
    await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 6000 });
    const line = L.lineThread(page, "on a line");
    await line.waitFor({ timeout: 6000 });
    assert(await line.evaluate((el) => el.closest("[data-line-block]").dataset.lineBlock) === "3" && await page.locator("[data-editor-comment-modal].show").count() === 0, "a line comment's thread does not unfold under its line after a project comment's");
    await openSheet(page);
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
