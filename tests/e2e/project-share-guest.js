const L = require("./lib");
const { assert, BASE, sleep } = L;

// Not a runner of its own: project-share.js runs these checks once its link
// stands, over its fixture and with its helpers. A guest is a browser context
// that never logs in, every request it makes is recorded and must stay under
// the link's address.
//
// The guest sees the projects through the owner's own pages in a guest
// mode: the editor with every file, .env and an ignored file included, as
// read only buffers, search, git history and diff, the line comments, Copy
// path with its chip absolute copying the path on the host, a definition
// into the standard library as a read only tab while a hand built source
// read outside the language server's source roots is refused, the type
// search of the palette landing on a declaration while another project's
// types answer the plain 404, the usages of an interface's declaration
// listing its implementations apart while another project's usages answer
// the plain 404, the
// terminal panel and the attach pages with every coder and shell of the
// project live, one started after the link was made included, and one of
// another project nowhere, the copy view of a shell and of a coder, the
// project's container in the docker sheet with its logs read only, and the
// owner's status line with the server's reading live on the guest's stream,
// the links of the link's running stack alone (another project's running
// stack nowhere, also not from a hand built pull) and the version, without the
// spend, the songs and the update check. The line
// comments stand marked in the gutter and listed in the sheet on every project
// of the link, a marked line's menu shows its comment in a dialog that saves
// nothing, and a comment the owner adds or deletes reaches the open guest
// without a reload. A project
// added to the link shows up in the open guest's project switcher without a
// reload and opens in the same tab, its shell joins the strip, and taken off
// again it answers the plain 404 under the link, its shell's stream ends and
// the guest's open page moves to the link's first project. No write control
// stands anywhere (the menus, the git sheet, the comments, the panel, the
// strip, the footers), and every write route refuses a hand built request
// carrying the guest's own CSRF token, a coder's and a shell's deletion
// above all, both still running after it, the line comments' with the 403 of
// a link that does not grant Comment. The guest never resizes a pane
// (stty size before and after a desktop and a phone guest), never changes the
// terminal theme (tmux window-style with the guest's scheme dark), never
// moves the owner's view (no second snapshot on the owner's stream, the
// owner's /editor and /terminals entries unchanged), and never hears the git
// question the owner's fetch parks (a stub remote answering 401), neither
// on its event stream nor as a dialog. Every request of a guest page stays
// under the link's address. On a phone the guest pages fit the width, the
// tab bar holds Editor and Terminals, the sheet lists the project's
// terminals.

const ASSET = /^\/(js|css)\/|^\/[\w.-]+\.(svg|png|json)$/;

async function guestPage(browser, bag, fx, origin, options = {}) {
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1360, height: 900 }, ...options });
  const page = await ctx.newPage();
  L.wirePage(page, bag);
  page.on("request", (r) => {
    const url = new URL(r.url());
    if (url.origin !== origin) return;
    fx.requests.push(`${r.method()} ${url.pathname}`);
    if (url.search) (fx.queries ??= []).push(url.search);
  });
  return { ctx, page };
}

const menuLabels = (page) => page.$$eval(".dc-context-menu .dropdown-item", (els) => els.map((el) => (el.querySelector("span") || el).textContent.trim()));

async function closeMenu(page) {
  await page.keyboard.press("Escape");
  await page.waitForSelector(".dc-context-menu", { state: "detached", timeout: 4000 }).catch(() => {});
  await sleep(400);
}

function noneOf(labels, forbidden, where) {
  const hit = labels.filter((label) => forbidden.some((word) => label === word || label.startsWith(`${word} `) || label.startsWith(`${word}"`)));
  assert(!hit.length, `${where} offers ${JSON.stringify(hit)}`);
}

const mirror = (page, scope) => page.evaluate((s) => document.querySelector(`${s} .attach-selection`)?.textContent || "", scope);

async function openFile(page, path) {
  await page.click(`.editor-file[data-path="${path}"]`);
  await page.waitForSelector(`.editor-tab[data-path="${path}"]`, { timeout: 8000 });
  await page.waitForSelector(".cm-content", { timeout: 8000 });
}

const switcherNames = (page) => page.$$eval("[data-editor-project-list] [data-project-name]", (els) => els.map((el) => el.dataset.projectName));

const statusSays = (page, text) => page.waitForFunction((t) => (document.querySelector("[data-editor-status]")?.textContent || "").includes(t), text, { timeout: 5000 });

// marked reads the line numbers the gutter draws as commented, in order.
const marked = (page) => page.$$eval(".cm-lineNumbers .cm-comment-line", (els) => els
  .filter((el) => el.getClientRects().length && getComputedStyle(el).visibility !== "hidden")
  .map((el) => el.textContent.trim()).join(","));

const commentCount = (page) => page.$eval("[data-editor-comments-count]", (el) => (getComputedStyle(el).display === "none" ? "" : el.textContent.trim()));

async function openCommentsSheet(page) {
  await page.locator("[data-editor-menu]").first().click();
  await page.click("[data-editor-comments-item]");
  await page.waitForSelector("[data-editor-sheet]:not([hidden])", { timeout: 6000 });
}

async function lineMenu(page, line) {
  await page.locator(".cm-lineNumbers .cm-gutterElement:visible", { hasText: new RegExp(`^${line}$`) }).first().click();
  await page.waitForSelector(".dc-context-menu", { timeout: 4000 });
  return menuLabels(page);
}

module.exports.guestPage = guestPage;
module.exports.openCommentsSheet = openCommentsSheet;
module.exports.menuLabels = menuLabels;
module.exports.closeMenu = closeMenu;
module.exports.openFile = openFile;
module.exports.marked = marked;
module.exports.lineMenu = lineMenu;

module.exports.guestChecks = async function guestChecks({ browser, page, run, bag, fx, origin, gitStubPort, postAs, getAs, type, shellAsk, fetchRaw, tap, waitText, addViaEdit, removeFromLink, sharingRows }) {
  fx.requests = [];
  fx.queries = [];
  const { ctx, page: gp } = await guestPage(browser, bag, fx, origin, { colorScheme: "dark" });
  await ctx.addInitScript(L.recordCopies);
  const at = (path) => `${BASE}${fx.base}${path}`;
  try {
    await run("the link lands on the project's editor with no cookie and only its two areas", async () => {
      await gp.goto(fx.link, { waitUntil: "domcontentloaded" });
      assert(new URL(gp.url()).pathname === `${fx.base}/projects/${fx.project}/editor`, `landed on ${gp.url()}`);
      await gp.waitForSelector(".editor-file", { timeout: 15000 });
      assert((await ctx.cookies()).length === 0, "the guest got a cookie");
      const rail = await gp.$$eval(".dc-rail a", (els) => els.map((el) => `${el.textContent.trim()} ${el.getAttribute("href")}`));
      assert(rail.length === 2 && rail.every((entry) => entry.includes(fx.base)), `the rail holds ${JSON.stringify(rail)}`);
      const outside = await gp.$$eval("a[href], form[action]", (els, base) => els
        .map((el) => el.getAttribute("href") || el.getAttribute("action"))
        .filter((href) => href.startsWith("/") && !href.startsWith(base) && !href.startsWith("/css/") && !href.startsWith("/js/")), fx.base);
      assert(!outside.length, `links leaving the link: ${JSON.stringify(outside)}`);
      assert(!(await gp.$("dc-notifications")), "an owner surface stands on the guest's editor");
      const switcher = await switcherNames(gp);
      assert(switcher.join(",") === fx.project, `the guest's switcher lists ${switcher}`);
    });

    await run("the guest's status line is the owner's server, its link's stack links and the version, live", async () => {
      const line = await gp.$eval(".dc-status", (el) => ({
        shown: getComputedStyle(el).display !== "none" && el.getBoundingClientRect().height > 0,
        meters: [...el.querySelectorAll("dc-host-status [data-host-meter]")].filter((m) => m.getClientRects().length).map((m) => m.dataset.hostMeter),
        owners: !!el.querySelector("dc-cost-status, dc-song-player, dc-update-check, a[href]:not(.dc-status-link)"),
        text: el.textContent.replace(/\s+/g, " "),
        html: el.innerHTML,
      }));
      assert(line.shown && ["mem", "disk"].every((m) => line.meters.includes(m)) && line.text.includes("Version ") && !line.owners, `the guest's status line reads ${JSON.stringify(line)}`);
      const rows = await gp.$$eval("dc-status-links [data-links-project]", (els) => els.map((el) => el.dataset.linksProject));
      const owners = (await getAs(page, `/docker/links?path=${encodeURIComponent("/projects")}`)).text;
      assert(owners.includes(`data-links-project="${fx.other}"`) && owners.includes(`:${fx.ports[fx.other]}`), "the owner's links miss the other project's stack");
      assert(rows.join(",") === fx.project && line.html.includes(`:${fx.ports[fx.project]}`) && !line.html.includes(`:${fx.ports[fx.other]}`) && !line.html.includes(fx.other), `the guest's links read ${rows}: ${line.html.slice(0, 300)}`);
      assert(await gp.isVisible("dc-status-links [data-links-toggle]"), "the guest's links chip is hidden");
      const pulled = Date.now() + 10000;
      while (Date.now() < pulled && !fx.requests.includes(`GET ${fx.base}/docker/links`)) await sleep(200);
      assert(fx.requests.includes(`GET ${fx.base}/docker/links`), `the chip did not pull under the link: ${fx.requests.filter((r) => r.includes("links"))}`);
      const reading = await gp.evaluate(() => new Promise((resolve) => {
        document.addEventListener("dc:host", (event) => resolve(event.detail), { once: true });
        setTimeout(() => resolve(null), 15000);
      }));
      assert(reading && reading.hasMem && reading.hasDisk, `no host reading reached the guest: ${JSON.stringify(reading)}`);
      const fresh = (await fetchRaw(at("/docker/links?path=" + encodeURIComponent(`/projects/${fx.other}/editor`)))).body;
      assert(fresh.includes(`data-links-project="${fx.project}"`) && !fresh.includes(fx.other), `a hand built links pull names ${fresh.slice(0, 300)}`);
    });

    await run("the editor lists every file, .env and the ignored one, and shows them read only", async () => {
      for (const path of [".env", "README.md", "ignored", "src", "compose.yaml"]) {
        await gp.waitForSelector(`.editor-tree [data-path="${path}"]`, { timeout: 8000 });
      }
      await openFile(gp, ".env");
      assert((await gp.textContent(".cm-content")).includes("env-marker"), ".env reads wrong");
      await gp.click('.editor-dir[data-path="ignored"]');
      await gp.waitForSelector('.editor-file[data-path="ignored/secret.txt"]', { timeout: 8000 });
      await openFile(gp, "ignored/secret.txt");
      assert((await gp.textContent(".cm-content")).includes("gitignored-secret-marker"), "the ignored file reads wrong");
      await openFile(gp, "README.md");
      await gp.click(".cm-content");
      await gp.keyboard.type("guest-typed");
      await gp.keyboard.press("Control+s");
      await sleep(800);
      assert(!(await gp.textContent(".cm-content")).includes("guest-typed"), "the buffer took the guest's typing");
      assert(!(await gp.isVisible("[data-editor-save]")), "Save shows");
      assert(!fx.requests.some((r) => r.startsWith("POST") && /\/editor\/(file|create|upload|comments)/.test(r)), "a write left the guest's editor");
    });

    await run("search, git history and the line comment are there, no git or comment write is", async () => {
      await gp.keyboard.press("Control+Shift+F");
      await gp.fill("[data-editor-quickopen-input]", `shareNeedle${fx.tag}`);
      await waitText(() => gp.textContent("[data-editor-quickopen-list]"), new RegExp(`app\\.js.*shareNeedle${fx.tag}`), "the search");
      await gp.keyboard.press("Escape");
      await gp.click("[data-editor-git-status]");
      await gp.waitForSelector("[data-editor-sheet]:not([hidden]) [data-git-commit]", { timeout: 10000 });
      assert((await gp.textContent("[data-editor-sheet-body]")).includes("first-share-commit"), "the history misses the commit");
      const rows = await gp.$$eval("[data-editor-sheet-body] .dropdown-item:not([data-git-commit])", (els) => els.map((el) => el.textContent.trim().split("\n")[0].trim()));
      noneOf(rows, ["Push", "Pull", "Fetch", "Force push", "Commit", "Switch branch", "New branch", "Clone repository"], "the git sheet");
      await gp.locator("[data-editor-sheet-body] [data-git-commit]").first().click();
      noneOf(await menuLabels(gp), ["Tag this commit", "Delete", "Push"], "a commit's menu");
      await closeMenu(gp);
      await gp.click("[data-editor-sheet-close]");
      assert(!fx.requests.some((r) => r.includes("/git/fetch") || r.includes("commit-draft") || r.includes("search-draft")), "the guest fetched or read a draft");
      await openCommentsSheet(gp);
      await waitText(() => gp.textContent("[data-editor-sheet-body]"), `owner-note-${fx.tag}`, "the comments sheet");
      assert(!(await gp.textContent("[data-editor-sheet-body]")).includes("Delete all comments"), "the comments offer a delete");
      await gp.click("[data-editor-sheet-close]");
      await waitText(() => marked(gp), /^2$/, "the gutter's comment marks");
      noneOf(await lineMenu(gp, 1), ["Show comment", "Add line comment"], "an uncommented line's menu");
      await closeMenu(gp);
      const labels = await lineMenu(gp, 2);
      noneOf(labels, ["Add line comment", "Edit comment", "Delete comment"], "a line's menu");
      assert(labels.includes("Show comment"), `the commented line's menu reads ${JSON.stringify(labels)}`);
      await gp.locator(".dc-context-menu .dropdown-item", { hasText: /^Show comment$/ }).click();
      await gp.waitForSelector("[data-editor-comment-modal].show", { timeout: 6000 });
      await sleep(500);
      const shown = await gp.$eval("[data-editor-comment-modal]", (el) => ({
        title: el.querySelector("[data-editor-comment-title]").textContent.trim(),
        place: el.querySelector("[data-editor-comment-place]").textContent.trim(),
        text: el.querySelector("[data-editor-comment-text]").value,
        readOnly: el.querySelector("[data-editor-comment-text]").readOnly,
        writes: el.querySelectorAll("[data-editor-comment-save], .form-hint").length,
      }));
      const want = { title: "Line comment", place: "README.md:2", text: `owner-note-${fx.tag}`, readOnly: true, writes: 0 };
      assert(JSON.stringify(shown) === JSON.stringify(want), `the comment shows as ${JSON.stringify(shown)}`);
      await gp.keyboard.press("Control+Enter");
      await gp.click("[data-editor-comment-cancel]");
      await gp.waitForFunction(() => getComputedStyle(document.querySelector("[data-editor-comment-modal]")).display === "none", null, { timeout: 6000 });
      assert(!fx.requests.some((r) => r.startsWith("POST") && r.includes("/comments")), "the guest posted a comment");
    });

    await run("a comment the owner adds or deletes reaches the open guest without a reload", async () => {
      await gp.evaluate(() => { window.__guestDocument = true; });
      const added = await postAs(page, `/projects/${fx.project}/editor/comments`, { path: "README.md", line: 3, text: `live-note-${fx.tag}` }, { json: true });
      assert(added.ok, `adding the comment answered ${added.status}: ${added.text}`);
      await waitText(() => marked(gp), /^2,3$/, "the guest's gutter after the add");
      await waitText(() => commentCount(gp), /^2$/, "the guest's comment count after the add");
      await openCommentsSheet(gp);
      await waitText(() => gp.textContent("[data-editor-sheet-body]"), `live-note-${fx.tag}`, "the guest's sheet after the add");
      const deleted = await postAs(page, `/projects/${fx.project}/editor/comments/delete`, { ids: [JSON.parse(added.text).comment.id] }, { json: true });
      assert(deleted.ok, `deleting the comment answered ${deleted.status}: ${deleted.text}`);
      await waitText(() => marked(gp), /^2$/, "the guest's gutter after the delete");
      await waitText(async () => ((await gp.textContent("[data-editor-sheet-body]")).includes(`live-note-${fx.tag}`) ? "listed" : "gone"), /^gone$/, "the guest's sheet after the delete");
      await waitText(() => commentCount(gp), /^1$/, "the guest's comment count after the delete");
      assert(await gp.evaluate(() => window.__guestDocument === true), "the guest's page reloaded");
      await gp.click("[data-editor-sheet-close]");
    });

    await run("the tree, the tab and the editor menu offer no write", async () => {
      await gp.click('.editor-file[data-path="README.md"]', { button: "right" });
      await gp.waitForSelector(".dc-context-menu", { timeout: 4000 });
      const tree = await menuLabels(gp);
      noneOf(tree, ["New file", "New folder", "Upload files", "Upload folder", "Paste", "Copy file", "Rename", "Delete", "Revert changes", "Extract here", "Replace in files"], "a tree row");
      assert(tree.some((label) => label.startsWith("Download")), `a tree row lacks Download: ${JSON.stringify(tree)}`);
      await closeMenu(gp);
      await gp.click('.editor-tab[data-path="README.md"]', { button: "right" });
      await gp.waitForSelector(".dc-context-menu", { timeout: 4000 });
      noneOf(await menuLabels(gp), ["Copy file", "Paste", "Rename", "Delete", "Revert changes"], "a tab");
      await closeMenu(gp);
      for (const sel of ["[data-editor-replace-project-item]", "[data-editor-save-all]", "[data-editor-reindex-item]", "[data-editor-term-plus]", "[data-editor-commit-toggle]:not([hidden])"]) {
        assert(!(await gp.$(sel)), `${sel} stands on the guest's editor`);
      }
    });

    await run("Copy path offers the guest the chip absolute, which copies the path on the host", async () => {
      const absolute = `${fx.path}/README.md`;
      await gp.evaluate(() => { window.__copies.length = 0; });
      await gp.click('.editor-file[data-path="README.md"]', { button: "right" });
      await gp.waitForSelector(".dc-context-menu", { timeout: 4000 });
      const chip = gp.locator(".dc-context-menu .dc-menu-split")
        .filter({ has: gp.locator(".dc-menu-label-head", { hasText: /^Copy path$/ }) })
        .locator(".dc-menu-chip");
      assert((await chip.count()) === 1 && (await chip.isVisible()), "the guest's Copy path has no chip");
      assert((await chip.textContent()).trim() === "absolute", `the chip reads ${await chip.textContent()}`);
      await chip.click();
      await statusSays(gp, `Copied ${absolute}`);
      const copies = await gp.evaluate(() => window.__copies.splice(0));
      assert(copies.some((c) => c.ok && c.text === absolute), `the chip copied ${JSON.stringify(copies)}`);
      return absolute;
    });

    await run("a definition leads the guest into the standard library read only, a hand built path outside the source roots is refused", async () => {
      const std = "/usr/local/go/src/fmt/errors.go";
      await openFile(gp, "main.go");
      await gp.waitForFunction(() => [...document.querySelectorAll(".cm-line")].some((l) => l.textContent.includes("fmt.Errorf(")), null, { timeout: 8000 });
      const pt = await L.wordPoint(gp, "fmt.Errorf(", "Errorf");
      assert(pt, "Errorf is not on the surface");
      await gp.keyboard.down("Control");
      await gp.mouse.click(pt.x, pt.y);
      await gp.keyboard.up("Control");
      await gp.waitForFunction((p) => document.querySelector('.editor-tab[aria-selected="true"]')?.dataset.path === p, std, { timeout: 90000 });
      const tab = await gp.evaluate(() => ({
        locked: !!document.querySelector('.editor-tab[aria-selected="true"] .ti-lock'),
        readOnly: document.querySelector("[data-editor-readonly]").getClientRects().length > 0,
        line: document.querySelector(".cm-activeLine")?.textContent || "",
      }));
      assert(tab.locked && tab.readOnly, `the standard library opens as ${JSON.stringify(tab)}`);
      assert(tab.line.startsWith("func Errorf("), `the cursor stands on "${tab.line}"`);
      await gp.evaluate(() => document.querySelector('.editor-tab[aria-selected="true"] .editor-tab-close').click());
      await gp.waitForSelector(`.editor-tab[data-path="${std}"]`, { state: "detached", timeout: 5000 });
      const nothing = await fetchRaw(`${BASE}/no/such/page`, { headers: { Accept: "application/json" } });
      const ask = (project, path) => fetchRaw(at(`/projects/${project}/editor/lsp/source?path=${encodeURIComponent(path)}`), { headers: { Accept: "application/json" } });
      const read = await ask(fx.project, std);
      assert(read.status === 200 && JSON.parse(read.body).readOnly === true, `the standard library by hand answered ${read.status}`);
      for (const path of ["/etc/passwd", "/usr/local/go/src/../../../../etc/passwd", `${fx.path}/main.go`, "main.go"]) {
        const res = await ask(fx.project, path);
        assert(res.status === 400 && !res.body.includes("root:"), `${path} answered ${res.status}`);
      }
      const other = await ask(fx.other, std);
      assert(other.status === 404 && other.body === nothing.body, `a project off the link answered ${other.status}`);
    });

    await run("the guest's type search lands on the declaration, another project's types answer the plain 404", async () => {
      await gp.keyboard.press("Control+o");
      await gp.waitForFunction(() => !document.querySelector("[data-editor-quickopen]").hidden, null, { timeout: 5000 });
      await gp.keyboard.type("ShareProbe");
      await gp.waitForFunction(() => [...document.querySelectorAll("[data-editor-quickopen-list] .editor-quickopen-item")]
        .some((row) => row.querySelector(".editor-quickopen-name")?.textContent === "ShareProbe" && row.querySelector(".badge")?.textContent === "struct"), null, { timeout: 90000 });
      await gp.keyboard.press("Enter");
      await gp.waitForFunction(() => document.querySelector('.editor-tab[aria-selected="true"]')?.dataset.path === "main.go"
        && document.querySelector("[data-editor-pos]")?.textContent === "9:6", null, { timeout: 10000 });
      const nothing = await fetchRaw(`${BASE}/no/such/page`, { headers: { Accept: "application/json" } });
      const ask = (project) => fetchRaw(at(`/projects/${project}/editor/lsp/types?client=guest&q=ShareProbe`), { headers: { Accept: "application/json" } });
      const own = await ask(fx.project);
      assert(own.status === 200 && JSON.parse(own.body).types.some((t) => t.name === "ShareProbe"), `the link's project answered ${own.status}`);
      const other = await ask(fx.other);
      assert(other.status === 404 && other.body === nothing.body, `a project off the link answered ${other.status}`);
    });

    await run("the guest's Ctrl+Alt+U on an interface's declaration lists its implementations above the usages, another project's usages answer the plain 404", async () => {
      await gp.waitForFunction(() => [...document.querySelectorAll(".cm-line")].some((l) => l.textContent.includes("type Prober interface")), null, { timeout: 8000 });
      const pt = await L.wordPoint(gp, "type Prober interface", "Prober");
      assert(pt, "Prober is not on the surface");
      await gp.mouse.click(pt.x, pt.y);
      await gp.keyboard.press("Control+Alt+u");
      await gp.waitForFunction(() => !document.querySelector("[data-editor-quickopen]").hidden
        && document.querySelector("[data-editor-quickopen-input]").placeholder === '1 implementation and 2 usages of "Prober"', null, { timeout: 90000 });
      const rows = await gp.$$eval("[data-editor-quickopen-list] [data-editor-usage-group], [data-editor-quickopen-list] .editor-quickopen-item",
        (els) => els.map((el) => (el.matches("[data-editor-usage-group]") ? `# ${el.textContent}` : el.title)).join(","));
      assert(rows === "# Implementations,main.go:9,# Usages,main.go:11,main.go:15", `ShareProbe implements it apart from the usages, got ${rows}`);
      await gp.keyboard.press("Escape");
      await gp.waitForFunction(() => document.querySelector("[data-editor-quickopen]").hidden, null, { timeout: 5000 });
      const csrf = await gp.evaluate(() => document.querySelector('meta[name="csrf-token"]').content);
      const nothing = await fetchRaw(`${BASE}/no/such/page`, { headers: { Accept: "application/json" } });
      const body = JSON.stringify({ client: "guest", path: "main.go", content: "package main\n\nvar _ Prober\n", position: { line: 2, character: 7 } });
      const other = await fetchRaw(at(`/projects/${fx.other}/editor/lsp/references`), {
        method: "POST", body, headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf, Accept: "application/json" },
      });
      assert(other.status === 404 && other.body === nothing.body, `a project off the link answered ${other.status}`);
    });

    await run("the terminal panel shows the project's terminals live, nothing of another, nothing to change", async () => {
      await gp.keyboard.press("Control+j");
      await gp.waitForSelector("[data-term-tab]", { timeout: 10000 });
      const ids = await gp.$$eval("[data-term-tab]", (els) => els.map((el) => el.getAttribute("data-term-tab")));
      for (const id of [fx.shell, fx.coder, fx.after]) assert(ids.includes(id), `the panel misses ${id}: ${ids}`);
      assert(!ids.includes(fx.otherShell), "the panel lists another project's shell");
      assert(!(await gp.$("[data-term-close], [data-term-resume], terminal-input")), "the panel offers a close, a resume or input");
      await gp.click(`[data-term-tab="${fx.shell}"]`);
      await gp.waitForSelector(`[data-term-pane="${fx.shell}"] terminal-attach`, { timeout: 8000 });
      const attrs = await gp.$eval(`[data-term-pane="${fx.shell}"] terminal-attach`, (el) => ({ resize: el.hasAttribute("resize-url"), readOnly: el.hasAttribute("read-only") }));
      assert(!attrs.resize && attrs.readOnly, `the panel's terminal is ${JSON.stringify(attrs)}`);
      await type(page, "shell", fx.shell, `echo panel-live-${fx.tag}`);
      await waitText(() => mirror(gp, `[data-term-pane="${fx.shell}"]`), `panel-live-${fx.tag}`, "the guest's panel");
      await gp.click(`[data-term-tab="${fx.shell}"]`, { button: "right" });
      noneOf(await menuLabels(gp), ["Rename", "Mark read", "Steer", "Release", "Open project", "Stop", "Delete"], "a panel tab");
      await closeMenu(gp);
    });

    await run("the terminal pages show every terminal of the project live, typing and closing nowhere", async () => {
      await gp.goto(at("/terminals"), { waitUntil: "domcontentloaded" });
      await gp.waitForSelector("terminal-tabs .terminal-tab", { timeout: 10000 });
      assert(new URL(gp.url()).pathname.startsWith(`${fx.base}/`), `the entry left the link: ${gp.url()}`);
      const hrefs = await gp.$$eval("terminal-tabs .terminal-tab", (els) => els.map((el) => el.getAttribute("href")));
      for (const id of [fx.shell, fx.coder, fx.after]) assert(hrefs.some((h) => h.endsWith(id) && h.startsWith(fx.base)), `the strip misses ${id}: ${hrefs}`);
      assert(!hrefs.some((h) => h.endsWith(fx.otherShell)), "the strip lists another project's shell");
      await gp.goto(at(`/shells/${fx.shell}`), { waitUntil: "domcontentloaded" });
      await gp.waitForSelector("#terminal .xterm-screen", { timeout: 10000 });
      assert(!(await gp.$("[data-tab-close], [data-tab-menu], terminal-input, [data-terminal-control], [data-terminal-paste], dc-inline-rename, [data-tabs-new-menu]")), "the shell's page offers a write");
      await type(page, "shell", fx.shell, `echo page-live-${fx.tag}`);
      await waitText(() => mirror(gp, "#terminal"), `page-live-${fx.tag}`, "the guest's shell page");
      await gp.click("#terminal .xterm-screen");
      await gp.keyboard.type("echo guest-typed\n");
      await sleep(800);
      assert(!(await shellAsk(page, fx.shell, "echo probe")).includes("guest-typed") && !fx.requests.some((r) => r.endsWith("/input")), "the guest's keys reached the shell");
      await gp.click("[data-terminal-copy]:visible");
      await gp.waitForSelector("terminal-copy:not([hidden]) [data-copy-text]:not([hidden])", { timeout: 10000 });
      assert((await gp.textContent("terminal-copy [data-copy-text]")).includes(`page-live-${fx.tag}`), "the copy view misses the shell's text");
      await gp.goto(at(`/coders/${fx.coder}`), { waitUntil: "domcontentloaded" });
      await gp.waitForSelector("#terminal .xterm-screen", { timeout: 10000 });
      await gp.click("[data-terminal-copy]:visible");
      await gp.waitForSelector("terminal-copy:not([hidden])", { timeout: 10000 });
      assert(!(await gp.isVisible("[data-copy-hint]")), "the coder's copy view tells the guest to type");
    });

    await run("the docker sheet shows the container, its logs read only, no compose action", async () => {
      await gp.goto(at(`/projects/${fx.project}/editor`), { waitUntil: "domcontentloaded" });
      await gp.waitForSelector(".editor-file", { timeout: 15000 });
      await gp.keyboard.press("Control+Shift+D");
      await gp.waitForSelector("[data-editor-sheet]:not([hidden]) [data-docker-container]", { timeout: 15000 });
      const sheet = await gp.textContent("[data-editor-sheet-body]");
      assert(sheet.includes("1 of 1 running") && !/Restore the default actions|Logs \(|^Up$/.test(sheet), `the sheet reads ${JSON.stringify(sheet.slice(0, 300))}`);
      await gp.click("[data-docker-container]");
      const items = await menuLabels(gp);
      noneOf(items, ["Shell", "Filter logs…", "Restart", "Stop", "Start"], "the container's menu");
      assert(items.includes("Logs"), `the container's menu lacks Logs: ${items}`);
      await gp.locator(".dc-context-menu .dropdown-item", { hasText: /^Logs$/ }).click();
      await gp.waitForSelector("[data-docker-logs]", { timeout: 10000 });
      assert(new URL(gp.url()).pathname.startsWith(`${fx.base}/projects/${fx.project}/docker/logs`), `the logs left the link: ${gp.url()}`);
      const first = await waitText(() => gp.textContent("[data-docker-logs]"), "share-log-start", "the logs");
      const ticks = (first.match(/tick-\d+/g) || []).pop();
      await waitText(() => gp.textContent("[data-docker-logs]"), new RegExp(`tick-\\d+\\s*$`), "the logs");
      await waitText(async () => ((await gp.textContent("[data-docker-logs]")).match(/tick-\d+/g) || []).pop() || "", new RegExp(`^(?!${ticks}$)`), "the following logs");
    });

    await run("a project the owner adds reaches the open guest, who moves to it in the same tab", async () => {
      await gp.goto(at(`/projects/${fx.project}/editor`), { waitUntil: "domcontentloaded" });
      await gp.waitForSelector(".editor-file", { timeout: 15000 });
      await addViaEdit(page, fx.second, fx.linkId);
      await waitText(async () => (await switcherNames(gp)).join(","), new RegExp(`(^|,)${fx.second}(,|$)`), "the open guest's switcher");
      const listed = await switcherNames(gp);
      assert(listed.length === 2 && listed.includes(fx.project) && !listed.includes(fx.other), `the switcher lists ${listed}`);
      await gp.click("[data-editor-project-switch]");
      await gp.waitForSelector("[data-editor-palette]", { state: "visible", timeout: 4000 });
      await Promise.all([
        gp.waitForURL((u) => u.pathname === `${fx.base}/projects/${fx.second}/editor`, { timeout: 10000 }),
        gp.click(`[data-editor-palette-list] [data-project-name="${fx.second}"]`),
      ]);
      await gp.waitForSelector('.editor-file[data-path="second.md"]', { timeout: 15000 });
      await openFile(gp, "second.md");
      assert((await gp.textContent(".cm-content")).includes(`second-marker-${fx.tag}`), "the second project's file reads wrong");
      await waitText(() => marked(gp), /^1$/, "the second project's gutter");
      await openCommentsSheet(gp);
      const sheet = await waitText(() => gp.textContent("[data-editor-sheet-body]"), `second-note-${fx.tag}`, "the second project's comments sheet");
      assert(!sheet.includes(`owner-note-${fx.tag}`), "the second project's sheet lists the first one's comment");
      await gp.click("[data-editor-sheet-close]");
      assert(ctx.pages().length === 1, `the guest stands in ${ctx.pages().length} tabs`);
      await gp.goto(at("/terminals"), { waitUntil: "domcontentloaded" });
      await gp.waitForSelector("terminal-tabs .terminal-tab", { timeout: 10000 });
      const hrefs = await gp.$$eval("terminal-tabs .terminal-tab", (els) => els.map((el) => el.getAttribute("href")));
      for (const id of [fx.shell, fx.secondShell]) assert(hrefs.some((h) => h === `${fx.base}/shells/${id}`), `the strip misses ${id}: ${hrefs}`);
      assert(!hrefs.some((h) => h.endsWith(fx.otherShell)), "the strip lists another project's shell");
      return listed.join(", ");
    });

    await run("a project taken off the link answers the plain 404 under it and moves the guest's open page to the link's first project", async () => {
      await gp.goto(at(`/projects/${fx.second}/editor`), { waitUntil: "domcontentloaded" });
      await gp.waitForSelector('.editor-file[data-path="second.md"]', { timeout: 15000 });
      const stream = tap(at(`/shells/${fx.secondShell}/stream`));
      assert((await stream.opened) === 200, "the second project's shell stream did not open");
      await waitText(async () => stream.text(), "event: snapshot", "the second project's shell stream");
      await removeFromLink(page, fx.linkId, fx.second);
      const ended = await Promise.race([stream.closed.then(() => true), sleep(5000).then(() => false)]);
      assert(ended, "the second project's shell stream outlived its removal");
      await gp.waitForURL((u) => u.pathname === `${fx.base}/projects/${fx.project}/editor`, { timeout: 10000 });
      await gp.waitForSelector(".editor-file", { timeout: 15000 });
      assert((await switcherNames(gp)).join(",") === fx.project, `the switcher still lists ${await switcherNames(gp)}`);
      const nothing = await fetchRaw(`${BASE}/no/such/page`);
      for (const path of [`/projects/${fx.second}/editor`, `/projects/${fx.second}/editor/list`, `/shells/${fx.secondShell}`, `/shells/${fx.secondShell}/stream`]) {
        const res = await fetchRaw(at(path));
        assert(res.status === 404 && res.body === nothing.body, `${path} under the link answered ${res.status}`);
      }
      assert((await fetchRaw(at(`/projects/${fx.project}/editor`))).status === 200, "the first project went with the second");
      const rows = (await sharingRows(page)).find((r) => r.id === fx.linkId);
      assert(rows && rows.projects.join(",") === `${fx.project} Read only`, `the link lists ${JSON.stringify(rows)}`);
    });

    await run("a phone guest fits the screen, finds the project's terminals in the sheet and watches one", async () => {
      const phone = await guestPage(browser, bag, fx, origin, { hasTouch: true, isMobile: true, viewport: { width: 390, height: 844 } });
      try {
        const mp = phone.page;
        await mp.goto(fx.link, { waitUntil: "domcontentloaded" });
        await mp.waitForSelector(".dc-tabbar", { timeout: 15000 });
        const fits = () => mp.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1);
        assert(await fits(), "the editor scrolls sideways");
        const tabs = await mp.$$eval(".dc-tabbar .dc-tabbar-btn", (els) => els.map((el) => el.textContent.trim()));
        assert(tabs.join(",") === "Editor,Terminals", `the tab bar holds ${tabs}`);
        await mp.click('.dc-tabbar [data-ctx-area="terminals"]');
        await mp.waitForSelector("[data-ctx-sheet] .terminal-tab", { timeout: 10000 });
        const rows = await mp.$$eval("[data-ctx-sheet] .terminal-tab", (els) => els.map((el) => el.getAttribute("href")));
        assert(rows.some((h) => h.endsWith(fx.after)) && !rows.some((h) => h.endsWith(fx.otherShell)), `the sheet lists ${rows}`);
        await mp.click(`[data-ctx-sheet] .terminal-tab[href$="${fx.shell}"]`);
        await mp.waitForSelector("#terminal .xterm-screen", { timeout: 10000 });
        await type(page, "shell", fx.shell, `echo phone-live-${fx.tag}`);
        await waitText(() => mirror(mp, "#terminal"), `phone-live-${fx.tag}`, "the phone guest's shell");
        assert(await fits(), "the shell page scrolls sideways");
        assert(!(await mp.$("[data-terminal-control], terminal-input")) && await mp.isVisible("[data-terminal-copy]:visible"), "the phone footer is not the watching one");
      } finally {
        await phone.ctx.close();
      }
    });

    await run("a guest never resizes a pane, never sets its theme, never moves the owner's view", async () => {
      const cookies = (await page.context().cookies()).map((c) => `${c.name}=${c.value}`).join("; ");
      const owner = tap(`${BASE}/shells/${fx.shell}/stream`, { Cookie: cookies });
      assert((await owner.opened) === 200, "the owner's stream did not open");
      await waitText(async () => owner.text(), "event: snapshot", "the owner's stream");
      await gp.goto(at(`/shells/${fx.shell}`), { waitUntil: "domcontentloaded" });
      await gp.waitForSelector("#terminal .xterm-screen", { timeout: 10000 });
      await gp.setViewportSize({ width: 700, height: 500 });
      await sleep(2500);
      const snapshots = (owner.stop().match(/event: snapshot/g) || []).length;
      assert(snapshots === 1, `the owner's stream got ${snapshots} snapshots while a guest came`);
      const size = await shellAsk(page, fx.shell, "stty size");
      const theme = await shellAsk(page, fx.shell, "tmux show -pv window-style");
      assert(size === fx.before.size, `the pane went from ${fx.before.size} to ${size}`);
      assert(theme === fx.before.theme, `the theme went from ${fx.before.theme} to ${theme}`);
      assert(!fx.requests.some((r) => /\/resize$|terminal-theme|notifications/.test(r)), "a guest posted a resize, a theme or a read");
      const editorEntry = (await getAs(page, "/editor")).url;
      const terminalsEntry = (await getAs(page, "/terminals")).url;
      assert(editorEntry === fx.before.editorEntry && terminalsEntry === fx.before.terminalsEntry, `the owner's entries moved: ${editorEntry}, ${terminalsEntry}`);
      return `${size}, ${theme || "no style"}`;
    });

    await run("the git question reaches the owner and never the guest", async () => {
      await shellAsk(page, fx.shell, `git remote add origin http://127.0.0.1:${gitStubPort}/x.git`);
      await gp.goto(at(`/projects/${fx.project}/editor`), { waitUntil: "domcontentloaded" });
      const events = tap(at("/events"));
      assert((await events.opened) === 200, "the guest's event stream did not open");
      await page.evaluate((p) => {
        const token = document.querySelector('meta[name="csrf-token"]').content;
        void fetch(`/projects/${p}/editor/git/fetch`, { method: "POST", headers: { "X-CSRF-Token": token, "Content-Type": "application/json" }, body: "{}" });
      }, fx.project);
      let question = null;
      const until = Date.now() + 20000;
      while (!question && Date.now() < until) {
        question = (JSON.parse((await getAs(page, "/git/prompt")).text).questions || [])[0] || null;
        if (!question) await sleep(400);
      }
      assert(question, "the owner's fetch parked no question");
      await sleep(2000);
      const seen = events.stop();
      assert(!seen.includes("gitprompt"), "the guest's stream carried the git question");
      assert(!(await gp.$(".swal2-container")), "a dialog stands on the guest's page");
      assert((await fetchRaw(at("/git/prompt"))).status === 404, "the guest reaches the questions");
      const answered = await postAs(page, "/git/prompt", { key: question.key, project: question.project, id: question.id, cancel: true }, { json: true });
      assert(answered.ok, `cancelling the question answered ${answered.status}`);
    });

    await run("every write route refuses the guest, a coder's and a shell's deletion above all", async () => {
      const csrf = await gp.evaluate(() => document.querySelector('meta[name="csrf-token"]').content);
      const p = `/projects/${fx.project}`;
      const writes = [
        `${p}/editor/file`, `${p}/editor/create`, `${p}/editor/mkdir`, `${p}/editor/delete`, `${p}/editor/rename`, `${p}/editor/move`,
        `${p}/editor/copy`, `${p}/editor/extract`, `${p}/editor/upload`, `${p}/editor/replace`, `${p}/editor/comments`, `${p}/editor/comments/delete`,
        `${p}/editor/comments/move`, `${p}/editor/git/commit`, `${p}/editor/git/commit-draft`, `${p}/editor/git/push`, `${p}/editor/git/fetch`,
        `${p}/editor/git/pull`, `${p}/editor/git/checkout`, `${p}/editor/git/branch`, `${p}/editor/git/tag`, `${p}/editor/git/revert`,
        `${p}/editor/git/clone`, `${p}/editor/search-draft`, `${p}/editor/lsp/reindex`, `${p}/editor/lsp/source`, `${p}/docker/compose`, `${p}/docker/logs`, `${p}/tags`,
        `/coders/${fx.coder}/delete`, `/shells/${fx.shell}/delete`, `/coders/delete`, `/coders/${fx.coder}/stop`, `/coders/${fx.coder}/input`,
        `/shells/${fx.shell}/input`, `/coders/${fx.coder}/resize`, `/shells/${fx.shell}/resize`, `/coders/${fx.coder}/rename`, `/shells/${fx.shell}/rename`,
        `/coders/${fx.coder}/resume`, `/coders/${fx.coder}/files`, `/shells/new`, `/coders/new`, `/docker/${fx.container.id}/stop`,
        `/docker/${fx.container.id}/shell`, `/terminal-theme`, `/terminal-tabs/order`, `/notifications/read`, `/projects/delete`, `/git/prompt`,
        "/settings/sharing/new",
      ];
      const headers = { "Content-Type": "application/x-www-form-urlencoded", "X-CSRF-Token": csrf, Accept: "application/json" };
      const body = new URLSearchParams({ path: "README.md", content: "guest", project: fx.project, ids: fx.shell, stack: "", action: "up" }).toString();
      const notes = (await getAs(page, `${p}/editor/comments`)).text;
      const granted = (path) => /\/editor\/comments(\/delete|\/move)?$/.test(path);
      for (const path of writes) {
        const viaLink = await fetchRaw(at(path), { method: "POST", body, headers });
        assert(granted(path) ? viaLink.status === 403 : viaLink.status === 404 || viaLink.status === 405, `${path} under the link answered ${viaLink.status}`);
        const bare = await fetchRaw(`${BASE}${path}`, { method: "POST", body, headers });
        assert(bare.status >= 300, `${path} without a session answered ${bare.status}`);
      }
      const id = JSON.parse(notes).comments[0].id;
      for (const [path, json] of [
        ["/comments", { path: "README.md", line: 1, text: "guest" }],
        ["/comments", { id, path: "README.md", line: 2, text: "guest" }],
        ["/comments/move", { path: "README.md", comments: [{ id, line: 3, lineText: "guest" }] }],
        ["/comments/delete", { ids: [id] }],
        ["/comments/delete", { all: true }],
      ]) {
        const res = await fetchRaw(at(`${p}/editor${path}`), { method: "POST", body: JSON.stringify(json), headers: { ...headers, "Content-Type": "application/json" } });
        assert(res.status === 403 && res.body.includes("The link does not allow that."), `${path} with ${JSON.stringify(json)} under the link answered ${res.status}`);
      }
      const after = (await getAs(page, `${p}/editor/comments`)).text;
      assert(after === notes && after.includes(`owner-note-${fx.tag}`), `the line comments moved: ${after}`);
      const readme = JSON.parse((await getAs(page, `${p}/editor/file?path=README.md`)).text).content;
      assert(readme.includes("an uncommitted line") && !readme.includes("guest"), "README.md changed");
      for (const path of [`/coders/${fx.coder}/name`, `/shells/${fx.shell}/name`]) {
        assert((await getAs(page, path)).status === 200, `${path} no longer runs after the guest's deletes`);
      }
      const docker = JSON.parse((await getAs(page, `${p}/editor/docker`)).text);
      assert(docker.containers.some((c) => c.running), "the container stopped");
      return `${writes.length} routes`;
    });

    await run("every request of a guest page stayed under the link and no query names its token", async () => {
      const outside = [...new Set(fx.requests.filter((r) => {
        const path = r.split(" ")[1];
        return !path.startsWith(`${fx.base}`) && !ASSET.test(path);
      }))];
      assert(!outside.length, `requests outside the link: ${outside.join(", ")}`);
      const token = fx.base.split("/").pop();
      const named = fx.queries.filter((q) => q.includes(token) || decodeURIComponent(q).includes(token));
      assert(fx.queries.some((q) => q.startsWith("?path=")) && !named.length, `queries naming the token: ${named.join(", ")}`);
      return `${fx.requests.length} requests, ${fx.queries.length} queries`;
    });
  } finally {
    await ctx.close();
  }
};
