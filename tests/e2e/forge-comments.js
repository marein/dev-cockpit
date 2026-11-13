const L = require("./lib");
const { assert, sleep, BASE } = L;

// Forge comments: each review thread of the change request of the branch a
// bound project has checked out is one of its line comment threads
// (internal/linecomment, SyncForge, fed by the forge poller through
// Server.ForgeReview), at the thread's line with the forge's icon, its first
// note the comment's and the others its replies with their authors there,
// and a link to the thread, a general thread a project comment at the top of
// the sheet under Project, kept in step without a reload over the SSE
// signal linecomments: a new reply appears, a resolved thread turns green, a
// deleted reply goes, a branch without a change request takes them all, and
// so does the end of the forge's reading of the project: the binding off, a
// detached HEAD, the token removed. GitLab's quote is the line at the commit
// the thread stands on, read from the local repository, GitHub's the last
// line of the thread's diff hunk; the thread follows its line like any
// comment and reads outdated where its text is gone, every line number and
// mark beside its code with the threads of a line unfolded. Nobody edits or deletes
// one here, Delete all comments keeps them, a guest of a shared link reads
// them like every thread and answers none. A thread that goes with the
// binding comes back under the id it had; GitHub's conversation comments and
// review bodies are project comments without a thread id, read only. The forge is
// the runner's own fake (forge-fake.js) on FORGE_PORT (default 3157),
// published on the host's loopback, where the instance reaches it at
// FORGE_ADDR (default 127.0.0.1); its merge request's discussions are the
// ones this runner sets in state.discussions. The repository is made with git
// through the cockpit's own proxy (POST /git), a push is an update-ref of the
// tracking ref. The host this runner adds carries its stamp in an alias and
// goes at the end with its project and its link. A resolved thread without a
// push is read on the poller's own pace, about a minute per engine.

const { startFakeForge, addHost, gitlabNote, gitlabDiscussion, EARLIER_HEAD } = require("./forge-fake");
const { alignedGutter } = require("./editor-comments-inline");

const FAKE_PORT = Number(process.env.FORGE_PORT || 3157);
const FAKE_TLS_PORT = Number(process.env.FORGE_TLS_PORT || 3158);
const FAKE_ADDR = process.env.FORGE_ADDR || "127.0.0.1";
const TOKEN = `glpat-e2e${Date.now().toString(36)}Cm4xQ7rZ2vN8`;
const FILE = "src/login.go";
const CONTENT = "package login\n\nfunc login() {\n\tname := bb\n\tc := 3\n\td := 4\n\treturn\n}\n";
let fakeForge = null;

L.runFeature("FORGE_COMMENTS", async ({ browser, page, run, mobilePage, bag }) => {
  const stamp = Date.now().toString(36);
  const project = `zzforgecomments-${stamp}`;
  const alias = `fc-${stamp}`;
  const linkName = `forge comments ${stamp}`;
  const fakeWeb = `http://${FAKE_ADDR}:${FAKE_PORT}`;
  const mrPage = "https://gitlab.example.com/team/sub/app/-/merge_requests/4";
  const prPage = "https://github.example.com/octo/app/pull/7";
  const editor = `${BASE}/projects/${project}/editor`;
  fakeForge ||= await startFakeForge({ port: FAKE_PORT, tlsPort: FAKE_TLS_PORT, token: TOKEN });
  let path = "";
  let hostEdit = "";
  let link = "";
  // What the merge request's threads hold right now; review() turns it into
  // GitLab's answer at the head the fake stands at.
  const threads = { replyGone: false, extra: false, resolved: false };
  fakeForge.state.discussions = (state) => [
    gitlabDiscussion("d1e2e", false, [
      gitlabNote(29, "root", "New name bb?", { resolvable: true, at: { head: state.head, newLine: 4 } }),
      ...(threads.replyGone ? [] : [gitlabNote(33, "alice", "Reply: yes, bb.", { resolvable: true, at: { head: state.head, newLine: 4 } })]),
      gitlabNote(38, "root", "changed this line in version 2 of the diff", { system: true, at: { head: state.head, newLine: 4 } }),
      ...(threads.extra ? [gitlabNote(41, "bob", "Also rename the variable.", { resolvable: true, at: { head: state.head, newLine: 4 } })] : []),
    ]),
    gitlabDiscussion("d2e2e", false, [gitlabNote(30, "root", "Is c needed?", { resolvable: true, resolved: threads.resolved, at: { head: state.head, newLine: 5 } })]),
    gitlabDiscussion("d3e2e", false, [gitlabNote(31, "root", "Added e", { resolvable: true, at: { head: EARLIER_HEAD, newLine: 7 } })]),
    gitlabDiscussion("d4e2e", false, [gitlabNote(34, "root", "General thread", { resolvable: true })]),
    gitlabDiscussion("d5e2e", false, [gitlabNote(37, "root", "File note", { resolvable: true, at: { head: state.head, file: true } })]),
  ];

  const git = async (args) => {
    const res = await page.evaluate(async ({ cwd, args }) => {
      const token = document.querySelector('meta[name="csrf-token"]')?.content || "";
      const r = await fetch("/git", {
        method: "POST",
        headers: { "X-CSRF-Token": token, "Content-Type": "application/json" },
        body: JSON.stringify({ cwd, args }),
      });
      if (!r.ok) return { failed: `${r.status} ${await r.text()}` };
      const d = await r.json();
      return { code: d.exitCode, out: atob(d.stdout || ""), err: atob(d.stderr || "") };
    }, { cwd: path, args });
    if (res.failed || res.code !== 0) throw new Error(`git ${args.join(" ")}: ${res.failed || res.err}`);
    return res.out.trim();
  };

  const post = (target, fields) => page.evaluate(async ({ target, fields }) => {
    const body = new URLSearchParams([...fields, ["csrf_token", document.querySelector('meta[name="csrf-token"]').content]]);
    const r = await fetch(target, { method: "POST", body });
    return { status: r.status, url: r.url, text: await r.text() };
  }, { target, fields: Array.isArray(fields) ? fields : Object.entries(fields) });

  const postJSON = (p, target, body) => p.evaluate(async ({ target, body }) => {
    const r = await fetch(target, {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]').content, Accept: "application/json" },
      body: JSON.stringify(body),
    });
    return { status: r.status, text: await r.text() };
  }, { target, body });

  const saveFile = (content) => page.evaluate(([target, file, content]) => fetch(target, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded", "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]').content, Accept: "application/json" },
    body: new URLSearchParams({ path: file, content }).toString(),
  }).then((r) => r.status), [`/projects/${project}/editor/file`, FILE, content]);

  const comments = (p, base = "") => p.evaluate((target) => fetch(target, { headers: { Accept: "application/json" } }).then((r) => r.json()).then((d) => d.comments), `${base}/projects/${project}/editor/comments`);
  const ofForge = (list) => Object.fromEntries(list.filter((c) => c.forge).map((c) => [c.forge.url.split("#").pop(), c]));
  const forgeIDs = (list) => JSON.stringify(Object.entries(ofForge(list)).map(([k, c]) => `${k}=${c.id}`).sort());
  let idsBefore = "";
  // openProjectThread opens the project comment holding text from its cell in
  // the sheet and answers the cell's menu.
  const ensureSheet = async (p) => {
    if (!(await p.locator("[data-editor-sheet]:not([hidden])").count())) await openSheet(p);
  };
  const openProjectThread = async (p, text) => {
    await ensureSheet(p);
    await p.locator("[data-editor-sheet-body] .row .dropdown-item", { hasText: text }).click();
    await p.waitForSelector(".dc-context-menu", { state: "visible", timeout: 6000 });
    const items = await p.$$eval(".dc-context-menu .dropdown-item", (els) => els.map((e) => ({ label: (e.querySelector(".dc-menu-label-head") || e).textContent.trim(), href: e.getAttribute("href") || "" })));
    await p.locator(".dc-context-menu .dropdown-item", { hasText: "Open thread" }).click();
    await modalShown(p);
    await L.threadCard(p, text).waitFor({ timeout: 6000 });
    return items;
  };
  const replies = (c) => (c.replies || []).map((r) => `${r.id} ${r.author}: ${r.text}`).join(" | ");

  const openEditor = async (p, base = BASE) => {
    await p.goto(`${base}/projects/${project}/editor?file=${encodeURIComponent(FILE)}`, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(p);
    await p.waitForSelector(".cm-lineNumbers", { state: "attached", timeout: 20000 });
    await p.waitForFunction(() => document.querySelector(".cm-content")?.textContent.includes("func login()"), null, { timeout: 15000 });
    await p.evaluate(() => { window.__forgeCommentsMark = 1; });
  };
  const notReloaded = async (p) => assert(await p.evaluate(() => window.__forgeCommentsMark === 1), "the editor reloaded");
  // marks reads the commented lines of the gutter, each as its number with
  // the mark it wears, o for an open one, x outdated, r resolved.
  const marks = (p) => p.evaluate(() => [...document.querySelectorAll(".cm-lineNumbers .cm-comment-line")]
    .filter((el) => el.getBoundingClientRect().height > 0)
    .map((el) => `${el.textContent.trim()}${el.classList.contains("cm-comment-line-outdated") ? "x" : el.classList.contains("cm-comment-line-resolved") ? "r" : "o"}`).join(","));
  const waitMarks = async (p, want, timeout) => {
    try {
      await p.waitForFunction((want) => [...document.querySelectorAll(".cm-lineNumbers .cm-comment-line")]
        .filter((el) => el.getBoundingClientRect().height > 0)
        .map((el) => `${el.textContent.trim()}${el.classList.contains("cm-comment-line-outdated") ? "x" : el.classList.contains("cm-comment-line-resolved") ? "r" : "o"}`).join(",") === want, want, { timeout });
    } catch {
      throw new Error(`the gutter reads ${await marks(p)}, want ${want}`);
    }
  };
  const lineCell = (p, n) => p.locator(".cm-lineNumbers .cm-gutterElement:visible").filter({ hasText: new RegExp(`^${n}$`) }).first();
  const lineMenu = async (p, n) => {
    await sleep(400);
    await lineCell(p, n).click({ button: "right" });
    await p.waitForSelector(".dc-context-menu", { state: "visible", timeout: 6000 });
    return p.$$eval(".dc-context-menu .dropdown-item", (els) => els.map((e) => ({
      label: (e.querySelector(".dc-menu-label-head") || e).textContent.trim(),
      icon: [...(e.querySelector("i")?.classList || [])].find((c) => c.startsWith("ti-")) || "",
      hint: e.querySelector(".dc-menu-hint")?.textContent.trim() || "",
      href: e.getAttribute("href") || "",
      target: e.getAttribute("target") || "",
    })));
  };
  const closeMenu = async (p) => {
    await p.keyboard.press("Escape");
    await p.waitForSelector(".dc-context-menu", { state: "detached", timeout: 4000 });
  };
  const modalShown = async (p) => {
    await p.waitForSelector("[data-editor-comment-modal].show", { timeout: 6000 });
    await sleep(500);
  };
  // thread reads the card of the thread a note with text opens, see
  // L.readThread, its reply form opened first where it offers one.
  const thread = async (p, text, { reply = false } = {}) => {
    const card = L.threadCard(p, text);
    await card.waitFor({ timeout: 8000 });
    if (reply && await card.locator("[data-line-thread-reply-open]").count()) await card.locator("[data-line-thread-reply-open]").click();
    return L.readThread(p, text);
  };
  const closeModal = async (p) => {
    await p.click("[data-editor-comment-modal] .btn-close");
    await p.waitForSelector("[data-editor-comment-modal].show", { state: "detached", timeout: 6000 });
  };
  const openSheet = async (p) => {
    await p.keyboard.press("Control+Shift+C");
    await p.waitForSelector("[data-editor-sheet]:not([hidden])", { timeout: 6000 });
  };
  const sheetCells = (p) => p.$$eval("[data-editor-sheet-body] .row .dropdown-item", (els) => els.map((e) => {
    const head = e.querySelector("[data-comment-author]");
    return {
      head: head ? head.textContent.trim() : "",
      icon: head ? [...head.querySelector("i").classList].find((c) => c.startsWith("ti-")) : "",
      resolved: !!e.querySelector("[data-comment-resolved]"),
      text: e.textContent.trim(),
    };
  }));

  try {
    await run("setup: a repository on the fake GitLab whose branch feature has a merge request, the editor open on its file", async () => {
      await L.createProject(page, project);
      path = await L.projectPath(page, project);
      await git(["init", "-q", "-b", "main"]);
      for (const [key, value] of [["user.email", "e2e@example.com"], ["user.name", "e2e"], ["commit.gpgsign", "false"]]) await git(["config", key, value]);
      await git(["commit", "-q", "--allow-empty", "-m", "first"]);
      await git(["switch", "-q", "-c", "feature"]);
      const dir = await post(`/projects/${project}/editor/mkdir`, { path: "src" });
      assert(dir.status === 200, `the folder could not be made: ${dir.status} ${dir.text}`);
      assert(await saveFile(CONTENT) === 200, "the file could not be written");
      await git(["add", "-A"]);
      await git(["commit", "-q", "-m", "login"]);
      const pushed = await git(["rev-parse", "HEAD"]);
      await git(["update-ref", "refs/remotes/origin/feature", pushed]);
      await git(["remote", "add", "origin", `${fakeWeb}/gl/team/sub/app.git`]);
      fakeForge.state.head = pushed;
      fakeForge.state.status = "running";
      await openEditor(page);
      assert(await marks(page) === "", `the gutter is marked before the forge was read: ${await marks(page)}`);
    });

    await run("the merge request's threads on a line appear as line comment threads without a reload, quoting their line", async () => {
      const added = await addHost(page, { kind: "gitlab", web_url: `${fakeWeb}/gl`, aliases: alias, token: TOKEN });
      assert(!added.failed, added.failed);
      hostEdit = await page.evaluate(async (mark) => {
        const html = await (await fetch("/settings/forges")).text();
        const doc = new DOMParser().parseFromString(html, "text/html");
        return [...doc.querySelectorAll("[data-forge-host]")].find((r) => r.textContent.includes(mark))?.querySelector("[data-forge-edit]")?.getAttribute("href") || "";
      }, alias);
      assert(hostEdit, "the added host has no Edit on the list");
      await waitMarks(page, "4o,5o,7x", 30000);
      await notReloaded(page);
      const list = await comments(page);
      const by = ofForge(list);
      assert(list.length === 4 && Object.keys(by).sort().join(",") === "note_29,note_30,note_31,note_34", `the comments read ${JSON.stringify(list)}`);
      const first = by.note_29;
      assert(first.path === FILE && first.line === 4 && first.lineText === "\tname := bb" && first.author === "root" && first.text === "New name bb?" && !first.outdated && !first.resolved && first.updatedAt === "2026-10-28T23:03:29.551Z", `the first thread reads ${JSON.stringify(first)}`);
      assert(replies(first) === "33 alice: Reply: yes, bb." && first.replies[0].updatedAt === "2026-10-28T23:03:33.551Z", `its replies read ${JSON.stringify(first.replies)}`);
      assert(JSON.stringify(first.forge) === JSON.stringify({ kind: "gitlab", label: "GitLab", icon: "ti-brand-gitlab", url: `${mrPage}#note_29`, thread: "d1e2e" }), `its forge reads ${JSON.stringify(first.forge)}`);
      assert(by.note_30.line === 5 && by.note_30.lineText === "\tc := 3" && !by.note_30.replies, `the other reads ${JSON.stringify(by.note_30)}`);
      assert(by.note_31.line === 7 && by.note_31.outdated && !by.note_31.lineText, `the thread on a commit the repository lacks reads ${JSON.stringify(by.note_31)}`);
      assert(!list.some((c) => /File note|changed this line/.test(c.text)), "a whole file or a system note stands as a comment");
      const general = by.note_34;
      assert(list[0] === general && general.project && general.path === "" && general.line === 0 && !general.lineText && !general.outdated && general.author === "root" && general.text === "General thread" && general.updatedAt === "2026-10-28T23:03:34.551Z", `the general thread reads ${JSON.stringify(general)}`);
      assert(JSON.stringify(general.forge) === JSON.stringify({ kind: "gitlab", label: "GitLab", icon: "ti-brand-gitlab", url: `${mrPage}#note_34`, thread: "d4e2e" }), `its forge reads ${JSON.stringify(general.forge)}`);
      assert(!(await page.content()).includes(TOKEN), "the page shows the token");
    });

    await run("the line's menu opens the thread with its replies and the link to it, two threads on one line are told apart, an own comment is still added", async () => {
      const items = await lineMenu(page, 4);
      const labels = items.map((i) => i.label);
      assert(JSON.stringify(labels.slice(0, 4)) === JSON.stringify(["Comment by root", "Open thread", "Add line comment", "Copy path:line"]), `the menu reads ${labels}`);
      assert(items[0].icon === "ti-brand-gitlab" && items[1].icon === "ti-brand-gitlab" && items[1].hint === "", `the thread's entries read ${JSON.stringify(items)}`);
      assert(!labels.some((l) => /Delete comment|Publish comment|^Reply|^Resolve/.test(l)), `the menu offers to change a thread of the forge here: ${labels}`);
      await page.locator(".dc-context-menu .dropdown-item", { hasText: "Open thread" }).click();
      const got = await thread(page, "New name bb?", { reply: true });
      assert(got.title === "Line comment" && got.line === "4" && got.author === "" && JSON.stringify(got.notes) === JSON.stringify(["root: New name bb?", "alice: Reply: yes, bb."]), `the thread reads ${JSON.stringify(got)}`);
      assert(got.field && got.save === "Reply" && got.resolve && got.hint === "It is posted on merge request !4 on GitLab, for everybody who can read it there.", `the thread's reply reads ${JSON.stringify(got)}`);
      assert(got.link && got.link.text === "Open the thread on GitLab" && got.link.href === `${mrPage}#note_29` && got.link.target === "_blank", `the link reads ${JSON.stringify(got.link)}`);
      await L.threadCard(page, "New name bb?").locator("[data-line-thread-reply-cancel]").click();
      await lineMenu(page, 4);
      await page.locator(".dc-context-menu .dropdown-item", { hasText: "Add line comment" }).click();
      const field = L.lineField(page, 4);
      await field.waitFor({ timeout: 6000 });
      assert(await L.seen(field) && await field.locator("[data-line-comment-add]").textContent() === "Add" && await field.locator("[data-comment-note]").count() === 0, "the new comment's field is not a bare field under line 4");
      await field.locator("[data-line-comment-text]").fill("mine on the same line");
      await field.locator("[data-line-comment-add]").click();
      await L.lineThread(page, "mine on the same line").waitFor({ timeout: 6000 });
      await page.waitForFunction(() => document.querySelector("[data-editor-comments-count]")?.textContent.trim() === "5", null, { timeout: 8000 });
      const items2 = await lineMenu(page, 4);
      const labels2 = items2.map((i) => i.label);
      assert(JSON.stringify(labels2.slice(0, 2)) === JSON.stringify(["Open your thread: mine on the same line", "Open root's thread: New name bb?"]) && items2[0].hint === "Ctrl+Alt+C" && items2[0].icon === "ti-messages" && items2[1].icon === "ti-brand-gitlab", `with an own comment the menu reads ${JSON.stringify(items2)}`);
      assert(labels2.includes("Delete comment") && !labels2.some((l) => l.startsWith("Comment by") || l === "Add line comment"), `with an own comment the menu reads ${labels2}`);
      await page.locator(".dc-context-menu .dropdown-item", { hasText: "Open root's thread" }).click();
      assert((await thread(page, "New name bb?")).notes[0] === "root: New name bb?", "the second entry opened another thread");
      await L.pressLine(page, 4);
      await page.waitForFunction(() => !document.querySelector(".cm-content [data-line-thread]"), null, { timeout: 6000 });
      const onFour = (await comments(page)).filter((c) => c.path === FILE && c.line === 4).length;
      // Each card after the first joins the line's block once that stood, so
      // the block grows after its placement.
      let pulled = 0;
      const stagger = async (route) => {
        await sleep(700 * pulled++);
        await route.continue();
      };
      await page.route(/\/comments\/thread\?/, stagger);
      await L.pressLine(page, 4);
      await page.waitForFunction((n) => [...document.querySelectorAll(".cm-content [data-line-thread]")].filter((card) => card.querySelector("[data-comment-note-text]")).length === n, onFour, { timeout: 8000 });
      await page.unroute(/\/comments\/thread\?/, stagger);
      assert(pulled === onFour, `the ${onFour} threads of line 4 pulled ${pulled} cards`);
      await alignedGutter(page, `the ${onFour} threads of line 4 unfolded one after the other`);
      await L.pressLine(page, 4);
      await page.waitForFunction(() => !document.querySelector(".cm-content [data-line-thread]"), null, { timeout: 6000 });
    });

    await run("the sheet heads each thread with the forge and its author, offers no edit or delete, and Delete all keeps the threads", async () => {
      await openSheet(page);
      let cells = await sheetCells(page);
      const forgeCells = cells.filter((c) => c.icon === "ti-brand-gitlab");
      assert(forgeCells.length === 4 && forgeCells.every((c) => c.head === "root"), `the sheet reads ${JSON.stringify(cells)}`);
      assert(cells[0].text === "rootProject commentGeneral thread" && cells[0].icon === "ti-brand-gitlab", `the project section reads ${JSON.stringify(cells[0])}`);
      const heads = await page.$$eval("[data-editor-sheet-body] [data-editor-sheet-head]", (els) => els.map((e) => e.textContent.trim()));
      assert(heads.indexOf("Project") >= 0 && heads.indexOf("Project") < heads.indexOf("Files"), `the sheet's heads read ${JSON.stringify(heads)}`);
      assert(forgeCells.find((c) => c.text.includes("New name bb?")).text.includes(":4 · 1 reply"), `the thread's cell reads ${JSON.stringify(forgeCells)}`);
      assert(cells.some((c) => !c.head && c.text.includes("mine on the same line")), `the own comment is missing: ${JSON.stringify(cells)}`);
      await page.locator("[data-editor-sheet-body] .row .dropdown-item", { hasText: "New name bb?" }).click();
      await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 6000 });
      const items = await page.$$eval(".dc-context-menu .dropdown-item", (els) => els.map((e) => ({ label: (e.querySelector(".dc-menu-label-head") || e).textContent.trim(), href: e.getAttribute("href") || "", target: e.getAttribute("target") || "" })));
      assert(items.map((i) => i.label).join("|") === "Go to line|Open thread|Open the thread on GitLab", `the cell's menu reads ${JSON.stringify(items)}`);
      const open = items.find((i) => i.label === "Open the thread on GitLab");
      assert(open.href === `${mrPage}#note_29` && open.target === "_blank", `the thread link reads ${JSON.stringify(open)}`);
      await closeMenu(page);
      const general = await openProjectThread(page, "General thread");
      assert(general.map((i) => i.label).join("|") === "Open thread|Open the thread on GitLab" && general[1].href === `${mrPage}#note_34`, `the general thread's menu reads ${JSON.stringify(general)}`);
      const got = await thread(page, "General thread", { reply: true });
      assert(got.title === "Project comment" && got.place === "" && JSON.stringify(got.notes) === JSON.stringify(["root: General thread"]), `the general thread's dialog reads ${JSON.stringify(got)}`);
      assert(got.field && got.save === "Reply" && got.resolve && got.hint === "It is posted on merge request !4 on GitLab, for everybody who can read it there." && got.link?.href === `${mrPage}#note_34`, `the general thread's reply reads ${JSON.stringify(got)}`);
      await closeModal(page);
      await ensureSheet(page);
      await page.locator("[data-editor-sheet-body] .dropdown-item", { hasText: "Delete all comments" }).click();
      await page.waitForSelector(".swal2-popup", { state: "visible", timeout: 6000 });
      const said = await page.locator(".swal2-html-container").innerText();
      assert(said === "This removes the one comment of this project. The review threads of the forge stay.", `the confirm says ${said}`);
      await page.click(".swal2-confirm");
      await page.waitForSelector(".swal2-container", { state: "detached", timeout: 6000 });
      await page.waitForFunction(() => document.querySelector("[data-editor-comments-count]")?.textContent.trim() === "4", null, { timeout: 8000 });
      const list = await comments(page);
      assert(list.length === 4 && list.every((c) => c.forge), `after Delete all the comments read ${JSON.stringify(list)}`);
      const id = ofForge(list).note_29.id;
      const edit = await postJSON(page, `/projects/${project}/editor/comments`, { id, path: FILE, line: 4, text: "rewritten" });
      assert(edit.status === 403 && edit.text.includes("changes there, not here"), `an edit of a thread of the forge answered ${edit.status} ${edit.text}`);
      for (const [route, body] of [["reply", { thread: id, text: "here" }], ["resolve", { id, resolved: true }]]) {
        const r = await postJSON(page, `/projects/${project}/editor/comments/${route}`, body);
        assert(r.status === 403, `a ${route} here of a thread of the forge answered ${r.status} ${r.text}`);
      }
      const projectID = ofForge(list).note_34.id;
      const projectEdit = await postJSON(page, `/projects/${project}/editor/comments`, { id: projectID, project: true, text: "rewritten" });
      assert(projectEdit.status === 403, `an edit of a general thread of the forge answered ${projectEdit.status} ${projectEdit.text}`);
      const projectReply = await postJSON(page, `/projects/${project}/editor/comments/reply`, { thread: projectID, text: "here" });
      assert(projectReply.status === 403, `a reply here to a general thread of the forge answered ${projectReply.status} ${projectReply.text}`);
      const del = await postJSON(page, `/projects/${project}/editor/comments/delete`, { ids: [id, projectID] });
      assert(del.status === 200 && del.text.includes('"removed":0'), `a delete of a thread of the forge answered ${del.status} ${del.text}`);
      await openSheet(page);
      cells = await sheetCells(page);
      assert(cells.length === 4 && cells.every((c) => c.icon === "ti-brand-gitlab"), `the sheet reads ${JSON.stringify(cells)}`);
      await page.keyboard.press("Escape");
    });

    await run("a thread follows its line on the disk, a resolved thread turns green, a new reply appears and a deleted one goes, without a reload", async () => {
      assert(await saveFile(`// Package login signs people in.\n${CONTENT}`) === 200, "the file could not be changed");
      await waitMarks(page, "5o,6o,7x", 15000);
      threads.resolved = true;
      threads.extra = true;
      threads.replyGone = true;
      const at = Date.now();
      await page.waitForFunction(() => document.querySelector(".cm-lineNumbers .cm-comment-line-resolved"), null, { timeout: 100000 });
      await waitMarks(page, "5o,6r,7x", 5000);
      const list = await comments(page);
      const by = ofForge(list);
      assert(list.length === 4 && replies(by.note_29) === "41 bob: Also rename the variable.", `the thread's replies read ${JSON.stringify(by.note_29)}`);
      assert(by.note_29.line === 5 && by.note_30.line === 6 && by.note_30.resolved && !by.note_29.resolved, `the threads read ${JSON.stringify([by.note_29, by.note_30])}`);
      await openSheet(page);
      const cells = await sheetCells(page);
      const resolvedCell = cells.find((c) => c.text.includes("Is c needed?"));
      assert(resolvedCell && resolvedCell.resolved && !cells.find((c) => c.text.includes("New name bb?")).resolved, `the sheet reads ${JSON.stringify(cells)}`);
      await page.keyboard.press("Escape");
      await lineMenu(page, 6);
      await page.locator(".dc-context-menu .dropdown-item", { hasText: "Open thread" }).click();
      const got = await thread(page, "Is c needed?", { reply: true });
      assert(got.author === "Resolved" && got.field && got.save === "Reply" && !got.resolve && !got.resolveButton, `the resolved thread reads ${JSON.stringify(got)}`);
      await L.threadCard(page, "Is c needed?").locator("[data-line-thread-reply-cancel]").click();
      await L.pressLine(page, 6);
      await notReloaded(page);
      return `read after ${Date.now() - at} ms`;
    });

    await run("a branch without a merge request takes the threads away, back on feature they return", async () => {
      await git(["switch", "-q", "-c", "other"]);
      await page.waitForFunction(() => document.querySelector("[data-editor-comments-count]")?.hidden, null, { timeout: 20000 });
      assert((await comments(page)).length === 0, "the notes stand on a branch without a merge request");
      await git(["switch", "-q", "feature"]);
      await waitMarks(page, "5o,6r,7x", 20000);
      await notReloaded(page);
    });

    await run("a guest of a shared link reads the threads like every comment and answers or changes none", async () => {
      await page.goto(`${BASE}/settings/sharing`, { waitUntil: "domcontentloaded" });
      const made = await post("/settings/sharing/new", [["amount", "1"], ["unit", "hours"], ["name", linkName], ["project", project], ["grant:comments", project]]);
      link = (made.text.match(/value="([^"]*\/share\/[^"]+)"/) || [])[1] || "";
      assert(link, `making a link answered ${made.status}`);
      const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1360, height: 900 } });
      try {
        const guest = await ctx.newPage();
        L.wirePage(guest, bag);
        const base = `${new URL(link).origin}${new URL(link).pathname}`;
        await openEditor(guest, base);
        await waitMarks(guest, "5o,6r,7x", 15000);
        const list = await comments(guest, new URL(link).pathname);
        assert(list.length === 4 && list.every((c) => c.forge && c.forge.url && !("thread" in c.forge) && !c.mine) && replies(ofForge(list).note_29) === "41 bob: Also rename the variable." && ofForge(list).note_34.project, `the guest reads ${JSON.stringify(list)}`);
        const items = await lineMenu(guest, 5);
        const labels = items.map((i) => i.label);
        assert(labels[0] === "Comment by root" && labels[1] === "Open thread" && labels[2] === "Add line comment" && !labels.some((l) => /Delete comment|Publish/.test(l)), `the guest's menu reads ${labels}`);
        await guest.locator(".dc-context-menu .dropdown-item", { hasText: "Open thread" }).click();
        const got = await thread(guest, "New name bb?");
        assert(JSON.stringify(got.notes) === JSON.stringify(["root: New name bb?", "bob: Also rename the variable."]) && got.line === "5" && !got.reply && !got.field && !got.save && !got.resolve && !got.resolveButton && !got.remove && got.link?.href === `${mrPage}#note_29`, `the guest's thread reads ${JSON.stringify(got)}`);
        const id = ofForge(list).note_29.id;
        const prefix = new URL(link).pathname;
        const edit = await postJSON(guest, `${prefix}/projects/${project}/editor/comments`, { id, path: FILE, line: 5, text: "the guest's" });
        assert(edit.status === 403, `the guest's edit of a thread of the forge answered ${edit.status} ${edit.text}`);
        const reply = await postJSON(guest, `${prefix}/projects/${project}/editor/comments/reply`, { thread: id, text: "the guest's" });
        assert(reply.status === 403, `the guest's reply to a thread of the forge answered ${reply.status} ${reply.text}`);
        const items2 = await openProjectThread(guest, "General thread");
        assert(items2.map((i) => i.label).join("|") === "Open thread|Open the thread on GitLab", `the guest's menu of the general thread reads ${JSON.stringify(items2)}`);
        const general = await thread(guest, "General thread");
        assert(general.title === "Project comment" && JSON.stringify(general.notes) === JSON.stringify(["root: General thread"]) && !general.reply && !general.field && !general.save && !general.resolve && !general.hint && general.link?.href === `${mrPage}#note_34`, `the guest's dialog of the general thread reads ${JSON.stringify(general)}`);
        await closeModal(guest);
        const generalID = ofForge(list).note_34.id;
        for (const [route, body] of [["", { id: generalID, project: true, text: "the guest's" }], ["/reply", { thread: generalID, text: "the guest's" }], ["/resolve", { id: generalID, resolved: true }]]) {
          const r = await postJSON(guest, `${prefix}/projects/${project}/editor/comments${route}`, body);
          assert(r.status === 403, `the guest's write ${route || "/"} on the general thread answered ${r.status} ${r.text}`);
        }
      } finally {
        await ctx.close();
      }
    });

    await run("on GitHub the threads quote the line of their diff hunk, a resolved one marks its line green though outdated", async () => {
      await openEditor(page);
      const saved = await post(hostEdit, { form: "save", kind: "github", web_url: fakeWeb, aliases: alias });
      assert(saved.status === 200, `turning the host into a GitHub answered ${saved.status}`);
      await git(["remote", "set-url", "origin", `${fakeWeb}/octo/app.git`]);
      let github = [];
      for (const until = Date.now() + 30000; Date.now() < until; await sleep(1000)) {
        github = await comments(page);
        if (github.length === 5 && github.every((c) => c.forge && c.forge.kind === "github")) break;
      }
      const by = ofForge(github);
      assert(by.discussion_r1 && by.discussion_r3 && by.discussion_r4, `the comments read ${JSON.stringify(github)}`);
      const comment = by["issuecomment-11"];
      const review = by["pullrequestreview-80"];
      assert(github[0] === review && github[1] === comment && comment.project && review.project && comment.author === "hubot" && comment.text === "Looks good\r\nbut see the threads" && review.text === "Please rename", `the conversation reads ${JSON.stringify([review, comment])}`);
      assert(JSON.stringify(comment.forge) === JSON.stringify({ kind: "github", label: "GitHub", icon: "ti-brand-github", url: `${prPage}#issuecomment-11`, unresolvable: true }), `the conversation comment's forge reads ${JSON.stringify(comment.forge)}`);
      const items = await openProjectThread(page, "Please rename");
      assert(items.map((i) => i.label).join("|") === "Open thread|Open the thread on GitHub", `the review body's menu reads ${JSON.stringify(items)}`);
      const got = await thread(page, "Please rename");
      assert(got.title === "Project comment" && JSON.stringify(got.notes) === JSON.stringify(["octocat: Please rename"]) && !got.reply && !got.field && !got.save && !got.resolve && !got.resolveButton && got.link?.href === `${prPage}#pullrequestreview-80`, `the review body's dialog reads ${JSON.stringify(got)}`);
      await closeModal(page);
      assert(by.discussion_r1.line === 5 && by.discussion_r1.lineText === "\tname := bb" && !by.discussion_r1.outdated && by.discussion_r1.author === "octocat", `the thread on its line reads ${JSON.stringify(by.discussion_r1)}`);
      assert(JSON.stringify(by.discussion_r1.forge) === JSON.stringify({ kind: "github", label: "GitHub", icon: "ti-brand-github", url: `${prPage}#discussion_r1`, thread: "PRRT_kwDOe2e1" }), `its forge reads ${JSON.stringify(by.discussion_r1.forge)}`);
      assert(by.discussion_r3.outdated && by.discussion_r3.lineText === "\te := 5", `the outdated thread reads ${JSON.stringify(by.discussion_r3)}`);
      assert(by.discussion_r4.line === 4 && by.discussion_r4.outdated && by.discussion_r4.lineText === "\tb := 2" && by.discussion_r4.resolved, `the resolved thread on the old side reads ${JSON.stringify(by.discussion_r4)}`);
      await waitMarks(page, "4r,5o,7x", 10000);
      await notReloaded(page);
    });

    await run("phone: the sheet's threads and a thread unfolded under its line stand within 390px", async () => {
      const phone = await mobilePage();
      await openEditor(phone);
      await openSheet(phone).catch(async () => {
        await phone.click("[data-editor-menu]");
        await phone.locator("[data-editor-comments-item]").click();
        await phone.waitForSelector("[data-editor-sheet]:not([hidden])", { timeout: 6000 });
      });
      await phone.waitForSelector("[data-editor-sheet-body] [data-comment-author]", { timeout: 10000 });
      const fits = () => phone.evaluate(() => {
        const inner = window.innerWidth;
        const boxes = [...document.querySelectorAll("[data-editor-sheet-body] [data-comment-author], .cm-content [data-line-thread]")]
          .map((el) => el.getBoundingClientRect()).filter((b) => b.width > 0);
        return { inner, scroll: document.documentElement.scrollWidth, out: boxes.filter((b) => b.left < 0 || b.right > inner).length, count: boxes.length };
      });
      let w = await fits();
      assert(w.count >= 3 && w.out === 0 && w.scroll <= w.inner, `the sheet on the phone: ${JSON.stringify(w)}`);
      await phone.locator("[data-editor-sheet-body] .row .dropdown-item", { hasText: "New name bb?" }).tap();
      await phone.locator(".dc-context-menu .dropdown-item", { hasText: "Open thread" }).tap();
      await L.threadCard(phone, "New name bb?").waitFor({ timeout: 8000 });
      await sleep(400);
      w = await fits();
      assert(w.count === 1 && w.out === 0 && w.scroll <= w.inner, `the thread on the phone: ${JSON.stringify(w)}`);
      assert(!(await phone.evaluate(() => document.activeElement?.matches("textarea:not([readonly]), input") || false)), "a writable field took the focus on touch");
    });

    await run("once the forge is no longer read the threads go without a reload, the own one stays: binding off, detached HEAD, token removed", async () => {
      await openEditor(page);
      const own = await postJSON(page, `/projects/${project}/editor/comments`, { path: FILE, line: 1, text: "mine stays" });
      assert(own.status === 200, `the own comment answered ${own.status} ${own.text}`);
      await waitMarks(page, "1o,4r,5o,7x", 15000);
      idsBefore = forgeIDs(await comments(page));
      assert(idsBefore.includes("issuecomment-11=") && idsBefore.includes("pullrequestreview-80="), `the threads before the drop read ${idsBefore}`);
      const same = async (why) => {
        let now = "";
        for (const until = Date.now() + 15000; Date.now() < until; await sleep(500)) {
          now = forgeIDs(await comments(page));
          if (now === idsBefore) return;
        }
        throw new Error(`${why}: the threads came back as ${now}, they stood as ${idsBefore}`);
      };
      const gone = async (why) => {
        try {
          await waitMarks(page, "1o", 20000);
        } catch (err) {
          throw new Error(`${why}: ${err.message}`);
        }
        const list = await comments(page);
        assert(list.length === 1 && list[0].text === "mine stays" && !list[0].forge, `${why}: the comments read ${JSON.stringify(list)}`);
        assert(await page.evaluate(() => document.querySelector("[data-editor-comments-count]")?.textContent.trim()) === "1", `${why}: the count is not 1`);
      };
      const bind = async (how) => {
        const r = await post(`/projects/${project}/forge`, { how, host: "", path: "" });
        assert(r.status === 200, `binding ${how} answered ${r.status} ${r.url}`);
      };
      await bind("off");
      await gone("the binding off");
      await bind("origin");
      await waitMarks(page, "1o,4r,5o,7x", 20000);
      await same("back from the binding off");
      await git(["switch", "-q", "--detach"]);
      await gone("a detached HEAD");
      await git(["switch", "-q", "feature"]);
      await waitMarks(page, "1o,4r,5o,7x", 20000);
      await same("back from a detached HEAD");
      const removed = await post(`${hostEdit}/token`, { token_remove: "1" });
      assert(removed.status === 200, `removing the token answered ${removed.status}`);
      await gone("the token removed");
      await notReloaded(page);
      const out = await postJSON(page, `/projects/${project}/editor/comments/delete`, { all: true });
      assert(out.status === 200 && out.text.includes('"removed":1'), `Delete all after the notes went answered ${out.status} ${out.text}`);
    });

    await run("a sheet with only threads of the forge shows Delete all comments disabled, the threads back under the ids they had", async () => {
      await post(`${hostEdit}/token`, { token: TOKEN });
      await waitMarks(page, "4r,5o,7x", 20000);
      for (let i = 0; i < 30 && forgeIDs(await comments(page)) !== idsBefore; i++) await sleep(500);
      assert(forgeIDs(await comments(page)) === idsBefore, `back with the token the threads read ${forgeIDs(await comments(page))}, they stood as ${idsBefore}`);
      await openSheet(page);
      const row = await page.locator("[data-editor-sheet-body] .dropdown-item", { hasText: "Delete all comments" }).evaluate((el) => ({ disabled: el.disabled, opacity: Number(getComputedStyle(el).opacity) }));
      assert(row.disabled && row.opacity < 0.75, `Delete all comments reads ${JSON.stringify(row)}`);
      await page.keyboard.press("Escape");
    });

    await run("docs: the Forges topic explains the review threads as line comments, the editor's entry points there", async () => {
      await page.goto(`${BASE}/docs`, { waitUntil: "domcontentloaded" });
      const item = page.locator("section#forges .list-group-item", { hasText: "Review threads as line comments" });
      assert(await item.count() === 1, "no docs entry for the review threads as line comments");
      const text = await item.innerText();
      for (const said of ["the others are its replies", "Open the thread on", "at most every 1m", "follow their line like any comment", "go with the change request or the binding", "Delete all comments keeps them", "A general thread of the conversation is a project comment with the forge's icon", "A guest reads them and answers none"]) {
        assert(text.includes(said), `the entry lacks "${said}": ${text}`);
      }
      const comments = await page.locator("section#editor .list-group-item", { hasText: "Line comments" }).first().innerText();
      assert(comments.includes("The forge's review threads stand among them, see Forges") && comments.includes("one entry per thread where several share the line"), `the line comments' entry reads ${comments}`);
    });
  } finally {
    await page.goto(`${BASE}/settings/sharing`, { waitUntil: "domcontentloaded" }).catch(() => {});
    const linkId = await page.evaluate((name) => [...document.querySelectorAll("[data-share-row]")]
      .find((r) => r.querySelector("[data-share-name]")?.textContent.trim() === name)?.dataset.shareRow || "", linkName).catch(() => "");
    if (linkId) await post(`/settings/sharing/${linkId}`, { form: "revoke" }).catch(() => {});
    if (hostEdit) await post(hostEdit, { form: "delete" }).catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
  }
});
