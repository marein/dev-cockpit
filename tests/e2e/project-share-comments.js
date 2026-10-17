const http = require("http");
const L = require("./lib");
const { assert, BASE, sleep } = L;
const { guestPage, openCommentsSheet, menuLabels, closeMenu, openFile, marked, lineMenu } = require("./project-share-guest");

// Not a runner of its own: project-share.js runs these checks after the guest
// checks, over its fixture and with its helpers. Comment is off on a new link
// until switched on in a project's entry of New link, which lists every pick
// at once with its switch and its cross, one of two projects alone if so,
// and then shows per project in Settings › Sharing, marked in the link's row
// and switched on in its Edit, whose Add lists a pick with its switch before
// the save. A guest
// whose link grants it adds a line comment through the line's menu, edits
// it, moves it (the route a save would call) and deletes it. The owner's
// comment and one of another link show to it read only, in the line's menu,
// the dialog and the sheet, and a hand built save, move or delete of them
// changes nothing; a write needs the link's CSRF token and the local socket
// is refused even with it. A guest's comment carries the link's name, shown
// as text, in the owner's line menu, dialog and sheet, in every guest's sheet
// and over the local API, which never names the link's id; the owner edits
// and deletes it, the name staying. A guest's path led by Unicode spaces, its
// quote with an escape and markup in its note are stored as sent and show as
// text, its mark in front of the comment in the owner's line menu, dialog,
// sheet (its name on a line of its own at the head of the owner's row, behind
// the person icon, its note flattened to one line, no box and no bar) and
// text, where the note stays as written inside the guest's entry, its
// path's Unicode spaces and its quote's escape shown as escapes, and the
// sheet cell's title names it on every line. Switching Comment off in the
// link's Edit refuses the guest's next write at once, from a hand built
// request and from its page still open, whose reload offers no comment
// anymore.

const APISOCK = process.env.APISOCK || "/apisock/s";

function overSocket(method, path, body, headers = {}) {
  return new Promise((resolve) => {
    const data = body ? JSON.stringify(body) : "";
    const req = http.request({
      socketPath: APISOCK, path, method,
      headers: { Accept: "application/json", ...(data ? { "Content-Type": "application/json", "Content-Length": Buffer.byteLength(data) } : {}), ...headers },
    }, (res) => {
      let raw = "";
      res.on("data", (chunk) => { raw += chunk; });
      res.on("end", () => resolve({ status: res.statusCode, body: raw }));
    });
    req.on("error", () => resolve({ status: 0, body: "" }));
    req.end(data);
  });
}

async function menuAt(gp, line) {
  await sleep(400);
  return lineMenu(gp, line);
}

async function pick(gp, label) {
  await gp.locator(".dc-context-menu .dropdown-item", { hasText: label }).first().click();
}

async function dialog(gp) {
  await gp.waitForSelector("[data-editor-comment-modal].show", { timeout: 6000 });
  await sleep(500);
  return gp.$eval("[data-editor-comment-modal]", (el) => {
    const shows = (sel) => { const n = el.querySelector(sel); return !!n && getComputedStyle(n).display !== "none"; };
    return {
      title: el.querySelector("[data-editor-comment-title]").textContent.trim(),
      author: shows("[data-editor-comment-author]") ? el.querySelector("[data-editor-comment-author]").textContent : "",
      readOnly: el.querySelector("[data-editor-comment-text]").readOnly,
      save: shows("[data-editor-comment-save]"),
      hint: shows(".form-hint"),
      close: el.querySelector("[data-editor-comment-cancel]").textContent.trim(),
    };
  });
}

async function dialogGone(gp) {
  await gp.waitForFunction(() => getComputedStyle(document.querySelector("[data-editor-comment-modal]")).display === "none", null, { timeout: 6000 });
}

async function write(gp, text) {
  await gp.fill("[data-editor-comment-text]", text);
  await gp.click("[data-editor-comment-save]");
  await dialogGone(gp);
}

module.exports.commentChecks = async function commentChecks({ browser, page, run, bag, fx, origin, postAs, getAs, fetchRaw, waitText, openShare, madeLink, editLink, saveEdit, sharingRows }) {
  const anna = `anna-${fx.tag}`;
  const bert = `Bert <b>${fx.tag}</b>`;
  const links = {};
  const notes = async () => JSON.parse((await getAs(page, `/projects/${fx.project}/editor/comments`)).text).comments;
  const noted = async (text) => (await notes()).find((c) => c.text === text);
  const csrfOf = (gp) => gp.evaluate(() => document.querySelector('meta[name="csrf-token"]').content);
  const asGuest = (who, path, body, csrf = who.csrf) => fetchRaw(`${BASE}${who.base}/projects/${fx.project}/editor${path}`, {
    method: "POST", body: JSON.stringify(body), headers: { "Content-Type": "application/json", Accept: "application/json", ...(csrf ? { "X-CSRF-Token": csrf } : {}) },
  });
  // sheetLines reads the open comments sheet a row a line: a head action as
  // its text, a comment as its note behind the name of the guest who wrote it.
  const sheetLines = (gp) => gp.$$eval("[data-editor-sheet-body] .dropdown-item", (els) => els.map((el) => {
    if (!el.closest(".row")) return el.textContent.trim();
    const head = el.querySelector(".ti-user")?.parentElement;
    const sub = el.querySelector("span.flex-column").children[1];
    const note = (sub.matches(".text-truncate") ? sub : sub.querySelector(".text-truncate")).textContent;
    return head ? `${head.textContent}: ${note}` : note;
  }).join("\n"));
  const sheetText = async (gp) => {
    await openCommentsSheet(gp);
    const text = await sheetLines(gp);
    await gp.click("[data-editor-sheet-close]");
    return text;
  };
  const openAs = async (who) => {
    const opened = await guestPage(browser, bag, fx, origin);
    await opened.page.goto(`${who.link}/projects/${fx.project}/editor`, { waitUntil: "domcontentloaded" });
    await opened.page.waitForSelector(".editor-file", { timeout: 15000 });
    await openFile(opened.page, "README.md");
    who.csrf = await csrfOf(opened.page);
    return opened;
  };
  const contexts = [];
  try {
    await run("Comment is off on a new link until switched on in its project's entry and shows per project in Settings › Sharing", async () => {
      const form = await openShare(page, fx.project);
      const entry = form.locator(`[data-share-entries] [data-share-picked="project:${fx.project}"]`);
      const box = entry.locator('[data-share-grant="comments"]');
      assert(await entry.isVisible() && await box.isVisible() && !(await box.isChecked()), "the picked project stands in the list without its Comment switch, or with it on");
      assert(await form.locator('[name="permission"]').count() === 0, "New link still holds the shared Permissions checkbox");
      await form.locator('[name="name"]').fill(anna);
      await box.check();
      const madeAnna = await madeLink(page, form);
      assert(madeAnna.says.includes(`${fx.project} Read, comment until`), `the new link reads ${JSON.stringify(madeAnna.says)}`);
      links.anna = { link: madeAnna.link };
      const made = await postAs(page, "/settings/sharing/new", [["amount", "2"], ["unit", "hours"], ["name", bert], ["project", fx.project], ["grant:comments", fx.project]]);
      links.bert = { link: (made.text.match(/value="([^"]*\/share\/[^"]+)"/) || [])[1] };
      assert(made.ok && links.bert.link, `making Bert's link answered ${made.status}`);
      const rows = await sharingRows(page);
      for (const [who, name] of [["anna", anna], ["bert", bert]]) {
        const row = rows.find((r) => r.name === name);
        assert(row && row.projects.join(",") === `${fx.project} Read, comment`, `${name}'s link lists ${JSON.stringify(row)}`);
        links[who].id = row.id;
        links[who].base = new URL(links[who].link).pathname;
      }
      const edit = await editLink(page, links.anna.id);
      assert(await edit.locator(`[data-share-project="${fx.project}"] [data-share-grant="comments"]`).isChecked(), "Anna's Edit has Comment off");
      await page.click("[data-form-modal].show .btn-close");
    });

    await run("New link switches Comment on one of two projects, the Edit switches it on a project its Add picks before the save", async () => {
      const pick = async (form, name) => {
        await form.locator("dc-picker [data-picker-toggle]").click();
        await form.locator("dc-picker [data-picker-filter]").fill(name);
        await form.locator(`dc-picker [data-picker-list] [data-project-name="${name}"]`).click();
        await page.keyboard.press("Escape");
      };
      const listed = (form) => form.locator("[data-share-entries] [data-share-picked]").evaluateAll((els) => els.filter((el) => el.getClientRects().length).map((el) => el.dataset.sharePicked));
      const form = await openShare(page, fx.project);
      await pick(form, fx.second);
      assert((await listed(form)).join(",") === `project:${fx.project},project:${fx.second}`, `New link lists ${await listed(form)}`);
      await form.locator(`[data-share-picked="project:${fx.second}"] [data-share-unpick]`).click();
      const count = await form.locator("dc-picker [data-picker-count]").textContent();
      assert((await listed(form)).join(",") === `project:${fx.project}` && count.trim() === "1" && await form.locator(`[name="project"][value="${fx.second}"]:checked`).count() === 0, `the cross left ${await listed(form)}, count ${count}`);
      await pick(form, fx.second);
      await form.locator(`[data-share-picked="project:${fx.second}"] [data-share-grant="comments"]`).check();
      const duo = `duo-${fx.tag}`;
      await form.locator('[name="name"]').fill(duo);
      const made = await madeLink(page, form);
      assert(made.says.includes(`${fx.second} Read, comment until`) && made.says.includes("The rest is read only."), `the new link reads ${JSON.stringify(made.says)}`);
      let row = (await sharingRows(page)).find((r) => r.name === duo);
      assert(row && row.projects.join(",") === `${fx.project} Read only,${fx.second} Read, comment`, `the link lists ${JSON.stringify(row)}`);
      const edit = await editLink(page, row.id);
      await pick(edit, fx.other);
      const added = edit.locator(`[data-share-entries] [data-share-picked="project:${fx.other}"] [data-share-grant="comments"]`);
      assert(await added.isVisible() && !(await added.isChecked()), "the Add's project stands in the list without its Comment switch before the save");
      await added.check();
      await saveEdit(page, edit);
      row = (await sharingRows(page)).find((r) => r.name === duo);
      assert(row && row.projects.join(",") === `${fx.project} Read only,${fx.second} Read, comment,${fx.other} Read, comment`, `after the save the link lists ${JSON.stringify(row)}`);
      const revoked = await postAs(page, `/settings/sharing/${row.id}`, { form: "revoke" });
      assert(revoked.ok, `revoking the link answered ${revoked.status}`);
    });

    await run("a guest with Comment adds, edits, moves and deletes its own comment", async () => {
      const a = await openAs(links.anna);
      contexts.push(a.ctx);
      links.anna.page = a.page;
      const gp = a.page;
      const first = await menuAt(gp, 1);
      assert(first.includes("Add line comment") && !first.includes("Show comment"), `an empty line's menu reads ${JSON.stringify(first)}`);
      await pick(gp, "Add line comment");
      const adding = await dialog(gp);
      assert(adding.title === "Line comment" && !adding.author && !adding.readOnly && adding.save, `adding reads ${JSON.stringify(adding)}`);
      await write(gp, `anna-note-${fx.tag}`);
      await waitText(() => marked(gp), /^1,2$/, "the guest's gutter after the add");
      const added = await noted(`anna-note-${fx.tag}`);
      assert(added && added.author === anna && added.line === 1 && !("link" in added) && !added.mine, `the owner reads ${JSON.stringify(added)}`);
      const own = await menuAt(gp, 1);
      assert(own.includes(`Comment by ${anna}`) && own.includes("Edit comment") && own.includes("Delete comment"), `its own comment's menu reads ${JSON.stringify(own)}`);
      await pick(gp, "Edit comment");
      const editing = await dialog(gp);
      assert(editing.title === "Edit line comment" && editing.author === `By ${anna}` && !editing.readOnly && editing.save && editing.hint, `editing reads ${JSON.stringify(editing)}`);
      await write(gp, `anna-edited-${fx.tag}`);
      const edited = await waitText(async () => JSON.stringify(await noted(`anna-edited-${fx.tag}`) || ""), anna, "the edit");
      const id = JSON.parse(edited).id;
      const moved = await gp.evaluate(async ({ path, id }) => {
        const res = await fetch(path, { method: "POST", headers: { "Content-Type": "application/json", "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]').content }, body: JSON.stringify({ path: "README.md", comments: [{ id, line: 3, lineText: "an uncommitted line" }] }) });
        return res.status;
      }, { path: `${links.anna.base}/projects/${fx.project}/editor/comments/move`, id });
      assert(moved === 200, `the move answered ${moved}`);
      await waitText(() => marked(gp), /^2,3$/, "the guest's gutter after the move");
      assert((await noted(`anna-edited-${fx.tag}`)).line === 3, "the move did not land");
      await menuAt(gp, 3);
      await pick(gp, "Delete comment");
      await L.confirmSwal(gp);
      await waitText(() => marked(gp), /^2$/, "the guest's gutter after the delete");
      assert(!(await noted(`anna-edited-${fx.tag}`)), "the guest's comment stands after its delete");
    });

    await run("the owner's and another link's comments stay read only for the guest, by the page and by hand", async () => {
      const b = await openAs(links.bert);
      contexts.push(b.ctx);
      links.bert.page = b.page;
      const res = await asGuest(links.bert, "/comments", { path: "README.md", line: 1, text: `bert-note-${fx.tag}` });
      assert(res.status === 200, `Bert's add answered ${res.status}: ${res.body}`);
      const gp = links.anna.page;
      await waitText(() => marked(gp), /^1,2$/, "Anna's gutter after Bert's add");
      const others = await menuAt(gp, 1);
      assert(others.includes(`Comment by ${bert}`) && others.includes("Show comment"), `another link's comment's menu reads ${JSON.stringify(others)}`);
      assert(!others.some((l) => /^(Add line comment|Edit comment|Delete comment)$/.test(l)), `another link's comment offers ${JSON.stringify(others)}`);
      await pick(gp, "Show comment");
      const shown = await dialog(gp);
      assert(shown.author === `By ${bert}` && shown.readOnly && !shown.save && !shown.hint && shown.close === "Close", `another link's comment shows as ${JSON.stringify(shown)}`);
      await gp.click("[data-editor-comment-cancel]");
      await dialogGone(gp);
      const owners = await menuAt(gp, 2);
      assert(owners.includes("Show comment") && !owners.some((l) => /^(Comment by|Edit comment|Delete comment)/.test(l)), `the owner's comment's menu reads ${JSON.stringify(owners)}`);
      await closeMenu(gp);
      await openCommentsSheet(gp);
      const sheet = await sheetLines(gp);
      assert(sheet.includes(`${bert}: bert-note-${fx.tag}`) && sheet.includes(`owner-note-${fx.tag}`) && !sheet.includes("Delete all comments"), `Anna's sheet reads ${JSON.stringify(sheet)}`);
      for (const cell of await gp.locator("[data-editor-sheet-body] .row .dropdown-item").all()) {
        await cell.click();
        await gp.waitForSelector(".dc-context-menu", { timeout: 4000 });
        const items = await menuLabels(gp);
        assert(items.includes("Go to line") && !items.includes("Edit") && !items.includes("Delete"), `a cell's menu reads ${JSON.stringify(items)}`);
        await closeMenu(gp);
      }
      await gp.click("[data-editor-sheet-close]");
      const before = await notes();
      const ownerNote = before.find((c) => c.text === `owner-note-${fx.tag}`);
      const bertNote = before.find((c) => c.text === `bert-note-${fx.tag}`);
      for (const target of [ownerNote, bertNote]) {
        const edit = await asGuest(links.anna, "/comments", { id: target.id, path: "README.md", line: 3, text: "taken" });
        assert(edit.status === 403, `editing ${target.text} answered ${edit.status}`);
      }
      const move = await asGuest(links.anna, "/comments/move", { path: "README.md", comments: [ownerNote, bertNote].map((c) => ({ id: c.id, line: 3, lineText: "an uncommitted line" })) });
      assert(move.status === 200, `the move answered ${move.status}`);
      for (const body of [{ ids: [ownerNote.id, bertNote.id] }, { paths: ["README.md"] }, { outdated: true }, { all: true }]) {
        const del = await asGuest(links.anna, "/comments/delete", body);
        assert(del.status === 200 && JSON.parse(del.body).removed === 0, `deleting ${JSON.stringify(body)} answered ${del.status}: ${del.body}`);
      }
      const unsigned = await asGuest(links.anna, "/comments", { path: "README.md", line: 3, text: "unsigned" }, "");
      assert(unsigned.status === 403, `a write without the CSRF token answered ${unsigned.status}`);
      const local = await overSocket("POST", `${links.anna.base}/projects/${fx.project}/editor/comments`, { path: "README.md", line: 3, text: "local" }, { "X-CSRF-Token": links.anna.csrf });
      assert(local.status === 403 && local.body.includes("local socket"), `the local socket answered ${local.status}: ${local.body}`);
      assert(JSON.stringify(await notes()) === JSON.stringify(before), `the comments moved: ${JSON.stringify(await notes())}`);
    });

    await run("the author shows for the owner, every guest and the local API, the owner changes and deletes a guest's comment", async () => {
      await page.goto(`${BASE}/projects/${fx.project}/editor`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(".editor-file", { timeout: 15000 });
      await openFile(page, "README.md");
      await waitText(() => marked(page), /^1,2$/, "the owner's gutter");
      const items = await menuAt(page, 1);
      assert(items.includes(`Comment by ${bert}`) && items.includes("Edit comment") && items.includes("Delete comment"), `the owner's menu of Bert's comment reads ${JSON.stringify(items)}`);
      await pick(page, "Edit comment");
      const editing = await dialog(page);
      assert(editing.author === `By ${bert}` && !editing.readOnly && editing.save, `the owner's dialog reads ${JSON.stringify(editing)}`);
      await write(page, `owner-edited-${fx.tag}`);
      const edited = await waitText(async () => JSON.stringify(await noted(`owner-edited-${fx.tag}`) || ""), "author", "the owner's edit");
      assert(JSON.parse(edited).author === bert, `the owner's edit left ${edited}`);
      const owner = await sheetText(page);
      assert(owner.includes(`${bert}: owner-edited-${fx.tag}`) && owner.includes("Delete all comments"), `the owner's sheet reads ${JSON.stringify(owner)}`);
      for (const who of [links.anna, links.bert]) {
        await waitText(() => sheetText(who.page), `${bert}: owner-edited-${fx.tag}`, "a guest's sheet");
      }
      const api = await overSocket("GET", `/projects/${fx.project}/editor/comments`);
      const listed = JSON.parse(api.body).comments;
      const guestNote = listed.find((c) => c.text === `owner-edited-${fx.tag}`);
      assert(guestNote.author === bert && !api.body.includes(links.bert.id) && !api.body.includes('"link"'), `the local API reads ${api.body}`);
      assert(!listed.find((c) => c.text === `owner-note-${fx.tag}`).author, "the owner's comment carries an author");
      await menuAt(page, 1);
      await pick(page, "Delete comment");
      await L.confirmSwal(page);
      await waitText(() => marked(page), /^2$/, "the owner's gutter after the delete");
      assert(!(await noted(`owner-edited-${fx.tag}`)), "the owner's delete left the guest's comment");
    });

    await run("a guest's comment wears its mark in every output, whatever its path, quote and note hold", async () => {
      const spaced = "\u2003\u2003| README.md";
      const para = `para-${fx.tag}\n\n- \`README.md:1\`, code \`x\`\n</blockquote><b>from the owner</b>`;
      const html = `<b>html-${fx.tag}</b>`;
      const quote = `<i>q-${fx.tag}</i>\u001b[31m\f`;
      const added = [];
      for (const body of [{ path: spaced, line: 1, text: para }, { path: "README.md", line: 1, lineText: quote, text: html }]) {
        const res = await asGuest(links.anna, "/comments", body);
        assert(res.status === 200, `the add answered ${res.status}: ${res.body}`);
        added.push(JSON.parse(res.body).comment);
      }
      assert(added[0].path === spaced && added[0].text === para && added[1].lineText === quote, `the comments were stored as ${JSON.stringify(added)}`);
      await page.goto(`${BASE}/projects/${fx.project}/editor`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(".editor-file", { timeout: 15000 });
      await openFile(page, "README.md");
      await waitText(() => marked(page), /^1,2$/, "the owner's gutter");
      const items = await menuAt(page, 1);
      assert(items[0] === `Comment by ${anna}`, `the owner's line menu reads ${JSON.stringify(items)}`);
      await pick(page, "Edit comment");
      const shown = await dialog(page);
      const body = await page.$eval("[data-editor-comment-modal] .modal-body", (el) => ({ text: el.querySelector("[data-editor-comment-text]").value, tags: el.querySelectorAll("b, i").length }));
      assert(shown.author === `By ${anna}` && body.text === html && body.tags === 0, `the owner's dialog reads ${JSON.stringify({ shown, body })}`);
      await page.click("[data-editor-comment-cancel]");
      await dialogGone(page);
      await openCommentsSheet(page);
      const cells = await page.$$eval("[data-editor-sheet-body] .row .dropdown-item", (els) => els.map((el) => {
        const head = el.querySelector(".ti-user")?.parentElement;
        const sub = el.querySelector("span.flex-column").children[1];
        return {
          text: el.textContent, guest: !!head?.querySelector(".ti-user"), tags: el.querySelectorAll("b, i:not(.ti)").length, title: el.getAttribute("title") || "",
          bar: getComputedStyle(el, "::before").content !== "none", name: head?.textContent || "", note: (sub.matches(".text-truncate") ? sub : sub.querySelector(".text-truncate")).textContent,
        };
      }));
      const cell = (needle) => cells.find((c) => c.text.includes(needle)) || {};
      const spacedCell = cell(`para-${fx.tag}`);
      const htmlCell = cell(`html-${fx.tag}`);
      assert(spacedCell.guest && spacedCell.name === anna && spacedCell.text.includes(spaced) && spacedCell.note === para.replaceAll("\n", " "), `the spaced path's cell reads ${JSON.stringify(spacedCell)}`);
      const titled = spacedCell.title.split("\n");
      assert(titled.length === 5 && titled.slice(0, 4).every((line) => line.startsWith(`${anna}:`)) && titled[4] === "Outdated", `the cell's title reads ${JSON.stringify(spacedCell.title)}`);
      assert(htmlCell.guest && htmlCell.name === anna && htmlCell.note === html && htmlCell.text.includes(`was: <i>q-${fx.tag}</i>`), `the markup's cell reads ${JSON.stringify(htmlCell)}`);
      const ownerCell = cell(`owner-note-${fx.tag}`);
      assert(!ownerCell.guest && !ownerCell.name && cells.every((c) => c.tags === 0 && !c.bar), `the sheet reads ${JSON.stringify(cells)}`);
      await page.locator("[data-editor-sheet-body] .dropdown-item", { hasText: "Copy as text" }).click();
      await page.locator(".dc-toast", { hasText: "Copied the line comments" }).first().waitFor({ timeout: 6000 });
      const md = await page.evaluate(() => navigator.clipboard.readText());
      const entries = md.split("\n").filter((line) => line.startsWith("- "));
      assert(entries.length === (await notes()).length && md.split("\n").slice(2).every((line) => line.startsWith("- ") || line.startsWith("  >")), `the text stands ${entries.length} entries: ${JSON.stringify(md)}`);
      for (const want of [
        [`- README.md:2, code: share-readme-marker`, `  > owner-note-${fx.tag}`],
        [`- Guest ${anna}: README.md:1 (outdated), code: <i>q-${fx.tag}</i>\\x1b[31m`, `  > <b>html-${fx.tag}</b>`],
        [`- Guest ${anna}: \\u2003\\u2003| README.md:1 (outdated)`, `  > para-${fx.tag}`, "  >", "  > - `README.md:1`, code `x`", "  > </blockquote><b>from the owner</b>"],
      ]) {
        assert(md.includes(want.join("\n")), `the text misses ${JSON.stringify(want)}: ${JSON.stringify(md)}`);
      }
      const removed = await asGuest(links.anna, "/comments/delete", { ids: added.map((c) => c.id) });
      assert(removed.status === 200 && JSON.parse(removed.body).removed === 2, `the delete answered ${removed.status}: ${removed.body}`);
      await waitText(() => marked(page), /^2$/, "the owner's gutter after the delete");
    });

    await run("switching Comment off refuses the guest at once, its open page included", async () => {
      const edit = await editLink(page, links.anna.id);
      await edit.locator(`[data-share-project="${fx.project}"] [data-share-grant="comments"]`).uncheck();
      await saveEdit(page, edit);
      const res = await asGuest(links.anna, "/comments", { path: "README.md", line: 1, text: "after" });
      assert(res.status === 403 && res.body.includes("The link does not allow that."), `a write after the take away answered ${res.status}: ${res.body}`);
      const row = (await sharingRows(page)).find((r) => r.id === links.anna.id);
      assert(row.projects.join(",") === `${fx.project} Read only`, `the link lists ${JSON.stringify(row)}`);
      const gp = links.anna.page;
      await menuAt(gp, 1);
      await pick(gp, "Add line comment");
      await dialog(gp);
      await write(gp, `stale-${fx.tag}`);
      await waitText(() => gp.textContent(".dc-toast"), "The link does not allow that.", "the refusal toast");
      assert(!(await noted(`stale-${fx.tag}`)), "the open page's write landed");
      await gp.reload({ waitUntil: "domcontentloaded" });
      await gp.waitForSelector(".editor-file", { timeout: 15000 });
      await openFile(gp, "README.md");
      const items = await menuAt(gp, 1);
      assert(!items.includes("Add line comment"), `the reloaded page offers ${JSON.stringify(items)}`);
      await closeMenu(gp);
    });
  } finally {
    for (const ctx of contexts) await ctx.close().catch(() => {});
    for (const who of Object.values(links)) {
      if (who.id) await postAs(page, `/settings/sharing/${who.id}`, { form: "revoke" }).catch(() => {});
    }
  }
};
