const L = require("./lib");
const { assert, sleep, BASE } = L;

// Forge comments: each review thread of the change request of the branch a
// bound project has checked out is one of its line comment threads
// (internal/linecomment, SyncForge, fed by the forge poller through
// Server.ForgeReview), at the thread's line with the forge's icon, its first
// note the comment's and the others its replies with their authors there,
// and a link to the thread, kept in step without a reload over the SSE
// signal linecomments: a new reply appears, a resolved thread turns green, a
// deleted reply goes, a branch without a change request takes them all, and
// so does the end of the forge's reading of the project: the binding off, a
// detached HEAD, the token removed. GitLab's quote is the line at the commit
// the thread stands on, read from the local repository, GitHub's the last
// line of the thread's diff hunk; the thread follows its line like any
// comment and reads outdated where its text is gone. Nobody edits or deletes
// one here, Delete all comments keeps them, a guest of a shared link reads
// them like every thread and answers none. The forge is
// the runner's own fake (forge-fake.js) on FORGE_PORT (default 3157),
// published on the host's loopback, where the instance reaches it at
// FORGE_ADDR (default 127.0.0.1); its merge request's discussions are the
// ones this runner sets in state.discussions. The repository is made with git
// through the cockpit's own proxy (POST /git), a push is an update-ref of the
// tracking ref. The host this runner adds carries its stamp in an alias and
// goes at the end with its project and its link. A resolved thread without a
// push is read on the poller's own pace, about a minute per engine.

const { startFakeForge, addHost, gitlabNote, gitlabDiscussion, EARLIER_HEAD } = require("./forge-fake");

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
    await lineCell(p, n).click();
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
  const modal = (p) => p.evaluate(() => {
    const shown = (el) => !!el && el.getBoundingClientRect().height > 0 && getComputedStyle(el).display !== "none";
    const q = (sel) => document.querySelector(sel);
    const a = q("[data-editor-comment-link]");
    return {
      title: q("[data-editor-comment-title]").textContent.trim(),
      place: q("[data-editor-comment-place]").textContent.trim(),
      author: shown(q("[data-editor-comment-author]")) ? q("[data-editor-comment-author]").textContent.trim() : "",
      text: q("[data-editor-comment-text]").value,
      field: shown(q("[data-editor-comment-text]")) && !q("[data-editor-comment-text]").readOnly,
      save: shown(q("[data-editor-comment-save]")) ? q("[data-editor-comment-save]").textContent.trim() : "",
      cancel: q("[data-editor-comment-cancel]").textContent.trim(),
      hint: shown(q("[data-editor-comment-hint]")) ? q("[data-editor-comment-hint]").textContent.trim() : "",
      resolve: shown(q("[data-editor-comment-resolve-row]")),
      notes: [...(shown(q("[data-editor-comment-thread]")) ? document.querySelectorAll("[data-editor-comment-thread] [data-comment-note]") : [])].map((n) => `${n.querySelector("[data-comment-note-author]").textContent.trim()}: ${n.querySelector("[data-comment-note-text]").textContent}${n.querySelector("button") ? " +buttons" : ""}`),
      link: shown(a) ? { text: a.textContent.trim(), href: a.getAttribute("href"), target: a.getAttribute("target") } : null,
    };
  });
  const closeModal = async (p) => {
    await p.click("[data-editor-comment-cancel]");
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
      assert(list.length === 3 && Object.keys(by).sort().join(",") === "note_29,note_30,note_31", `the comments read ${JSON.stringify(list)}`);
      const first = by.note_29;
      assert(first.path === FILE && first.line === 4 && first.lineText === "\tname := bb" && first.author === "root" && first.text === "New name bb?" && !first.outdated && !first.resolved && first.updatedAt === "2026-10-28T23:03:29.551Z", `the first thread reads ${JSON.stringify(first)}`);
      assert(replies(first) === "33 alice: Reply: yes, bb." && first.replies[0].updatedAt === "2026-10-28T23:03:33.551Z", `its replies read ${JSON.stringify(first.replies)}`);
      assert(JSON.stringify(first.forge) === JSON.stringify({ kind: "gitlab", label: "GitLab", icon: "ti-brand-gitlab", url: `${mrPage}#note_29`, thread: "d1e2e" }), `its forge reads ${JSON.stringify(first.forge)}`);
      assert(by.note_30.line === 5 && by.note_30.lineText === "\tc := 3" && !by.note_30.replies, `the other reads ${JSON.stringify(by.note_30)}`);
      assert(by.note_31.line === 7 && by.note_31.outdated && !by.note_31.lineText, `the thread on a commit the repository lacks reads ${JSON.stringify(by.note_31)}`);
      assert(!list.some((c) => /General thread|File note|changed this line/.test(c.text)), "a general, a whole file or a system note stands as a line comment");
      assert(!(await page.content()).includes(TOKEN), "the page shows the token");
    });

    await run("the line's menu opens the thread with its replies and the link to it, two threads on one line are told apart, an own comment is still added", async () => {
      const items = await lineMenu(page, 4);
      const labels = items.map((i) => i.label);
      assert(JSON.stringify(labels.slice(0, 4)) === JSON.stringify(["Comment by root", "Open thread", "Add line comment", "Copy path:line"]), `the menu reads ${labels}`);
      assert(items[0].icon === "ti-brand-gitlab" && items[1].icon === "ti-brand-gitlab" && items[1].hint === "", `the thread's entries read ${JSON.stringify(items)}`);
      assert(!labels.some((l) => /Delete comment|Publish comment|^Reply|^Resolve/.test(l)), `the menu offers to change a thread of the forge here: ${labels}`);
      await page.locator(".dc-context-menu .dropdown-item", { hasText: "Open thread" }).click();
      await modalShown(page);
      const got = await modal(page);
      assert(got.title === "Line comment" && got.place === `${FILE}:4` && got.author === "" && JSON.stringify(got.notes) === JSON.stringify(["root: New name bb?", "alice: Reply: yes, bb."]), `the dialog reads ${JSON.stringify(got)}`);
      assert(got.field && got.save === "Reply" && got.resolve && got.hint === "It is posted on merge request !4 on GitLab, for everybody who can read it there.", `the dialog's reply reads ${JSON.stringify(got)}`);
      assert(got.link && got.link.text === "Open the thread on GitLab" && got.link.href === `${mrPage}#note_29` && got.link.target === "_blank", `the link reads ${JSON.stringify(got.link)}`);
      await closeModal(page);
      await lineMenu(page, 4);
      await page.locator(".dc-context-menu .dropdown-item", { hasText: "Add line comment" }).click();
      await modalShown(page);
      const fresh = await modal(page);
      assert(fresh.author === "" && fresh.link === null && fresh.field && fresh.save === "Add" && fresh.notes.length === 0, `the new comment's dialog reads ${JSON.stringify(fresh)}`);
      await page.fill("[data-editor-comment-text]", "mine on the same line");
      await page.click("[data-editor-comment-save]");
      await page.waitForSelector("[data-editor-comment-modal].show", { state: "detached", timeout: 6000 });
      await page.waitForFunction(() => document.querySelector("[data-editor-comments-count]")?.textContent.trim() === "4", null, { timeout: 8000 });
      const items2 = await lineMenu(page, 4);
      const labels2 = items2.map((i) => i.label);
      assert(JSON.stringify(labels2.slice(0, 2)) === JSON.stringify(["Open your thread: mine on the same line", "Open root's thread: New name bb?"]) && items2[0].hint === "Ctrl+Alt+C" && items2[0].icon === "ti-messages" && items2[1].icon === "ti-brand-gitlab", `with an own comment the menu reads ${JSON.stringify(items2)}`);
      assert(labels2.includes("Delete comment") && !labels2.some((l) => l.startsWith("Comment by") || l === "Add line comment"), `with an own comment the menu reads ${labels2}`);
      await page.locator(".dc-context-menu .dropdown-item", { hasText: "Open root's thread" }).click();
      await modalShown(page);
      assert((await modal(page)).notes[0] === "root: New name bb?", "the second entry opened another thread");
      await closeModal(page);
    });

    await run("the sheet heads each thread with the forge and its author, offers no edit or delete, and Delete all keeps the threads", async () => {
      await openSheet(page);
      let cells = await sheetCells(page);
      const forgeCells = cells.filter((c) => c.icon === "ti-brand-gitlab");
      assert(forgeCells.length === 3 && forgeCells.every((c) => c.head === "root"), `the sheet reads ${JSON.stringify(cells)}`);
      assert(forgeCells.find((c) => c.text.includes("New name bb?")).text.includes(":4 · 1 reply"), `the thread's cell reads ${JSON.stringify(forgeCells)}`);
      assert(cells.some((c) => !c.head && c.text.includes("mine on the same line")), `the own comment is missing: ${JSON.stringify(cells)}`);
      await page.locator("[data-editor-sheet-body] .row .dropdown-item", { hasText: "New name bb?" }).click();
      await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 6000 });
      const items = await page.$$eval(".dc-context-menu .dropdown-item", (els) => els.map((e) => ({ label: (e.querySelector(".dc-menu-label-head") || e).textContent.trim(), href: e.getAttribute("href") || "", target: e.getAttribute("target") || "" })));
      assert(items.map((i) => i.label).join("|") === "Go to line|Open thread|Open the thread on GitLab", `the cell's menu reads ${JSON.stringify(items)}`);
      const open = items.find((i) => i.label === "Open the thread on GitLab");
      assert(open.href === `${mrPage}#note_29` && open.target === "_blank", `the thread link reads ${JSON.stringify(open)}`);
      await closeMenu(page);
      await page.locator("[data-editor-sheet-body] .dropdown-item", { hasText: "Delete all comments" }).click();
      await page.waitForSelector(".swal2-popup", { state: "visible", timeout: 6000 });
      const said = await page.locator(".swal2-html-container").innerText();
      assert(said === "This removes the one comment of this project. The review threads of the forge stay.", `the confirm says ${said}`);
      await page.click(".swal2-confirm");
      await page.waitForSelector(".swal2-container", { state: "detached", timeout: 6000 });
      await page.waitForFunction(() => document.querySelector("[data-editor-comments-count]")?.textContent.trim() === "3", null, { timeout: 8000 });
      const list = await comments(page);
      assert(list.length === 3 && list.every((c) => c.forge), `after Delete all the comments read ${JSON.stringify(list)}`);
      const id = ofForge(list).note_29.id;
      const edit = await postJSON(page, `/projects/${project}/editor/comments`, { id, path: FILE, line: 4, text: "rewritten" });
      assert(edit.status === 403 && edit.text.includes("changes there, not here"), `an edit of a thread of the forge answered ${edit.status} ${edit.text}`);
      for (const [route, body] of [["reply", { thread: id, text: "here" }], ["resolve", { id, resolved: true }]]) {
        const r = await postJSON(page, `/projects/${project}/editor/comments/${route}`, body);
        assert(r.status === 403, `a ${route} here of a thread of the forge answered ${r.status} ${r.text}`);
      }
      const del = await postJSON(page, `/projects/${project}/editor/comments/delete`, { ids: [id] });
      assert(del.status === 200 && del.text.includes('"removed":0'), `a delete of a thread of the forge answered ${del.status} ${del.text}`);
      await openSheet(page);
      cells = await sheetCells(page);
      assert(cells.length === 3 && cells.every((c) => c.icon === "ti-brand-gitlab"), `the sheet reads ${JSON.stringify(cells)}`);
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
      assert(list.length === 3 && replies(by.note_29) === "41 bob: Also rename the variable.", `the thread's replies read ${JSON.stringify(by.note_29)}`);
      assert(by.note_29.line === 5 && by.note_30.line === 6 && by.note_30.resolved && !by.note_29.resolved, `the threads read ${JSON.stringify([by.note_29, by.note_30])}`);
      await openSheet(page);
      const cells = await sheetCells(page);
      const resolvedCell = cells.find((c) => c.text.includes("Is c needed?"));
      assert(resolvedCell && resolvedCell.resolved && !cells.find((c) => c.text.includes("New name bb?")).resolved, `the sheet reads ${JSON.stringify(cells)}`);
      await page.keyboard.press("Escape");
      await lineMenu(page, 6);
      await page.locator(".dc-context-menu .dropdown-item", { hasText: "Open thread" }).click();
      await modalShown(page);
      const got = await modal(page);
      assert(got.author === "Resolved" && got.field && got.save === "Reply" && !got.resolve, `the resolved thread's dialog reads ${JSON.stringify(got)}`);
      await closeModal(page);
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
        assert(list.length === 3 && list.every((c) => c.forge && c.forge.url && !("thread" in c.forge) && !c.mine) && replies(ofForge(list).note_29) === "41 bob: Also rename the variable.", `the guest reads ${JSON.stringify(list)}`);
        const items = await lineMenu(guest, 5);
        const labels = items.map((i) => i.label);
        assert(labels[0] === "Comment by root" && labels[1] === "Open thread" && labels[2] === "Add line comment" && !labels.some((l) => /Delete comment|Publish/.test(l)), `the guest's menu reads ${labels}`);
        await guest.locator(".dc-context-menu .dropdown-item", { hasText: "Open thread" }).click();
        await modalShown(guest);
        const got = await modal(guest);
        assert(JSON.stringify(got.notes) === JSON.stringify(["root: New name bb?", "bob: Also rename the variable."]) && !got.field && !got.save && got.cancel === "Close" && !got.resolve && got.link?.href === `${mrPage}#note_29`, `the guest's dialog reads ${JSON.stringify(got)}`);
        await closeModal(guest);
        const id = ofForge(list).note_29.id;
        const prefix = new URL(link).pathname;
        const edit = await postJSON(guest, `${prefix}/projects/${project}/editor/comments`, { id, path: FILE, line: 5, text: "the guest's" });
        assert(edit.status === 403, `the guest's edit of a thread of the forge answered ${edit.status} ${edit.text}`);
        const reply = await postJSON(guest, `${prefix}/projects/${project}/editor/comments/reply`, { thread: id, text: "the guest's" });
        assert(reply.status === 403, `the guest's reply to a thread of the forge answered ${reply.status} ${reply.text}`);
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
        if (github.length === 3 && github.every((c) => c.forge && c.forge.kind === "github")) break;
      }
      const by = ofForge(github);
      assert(by.discussion_r1 && by.discussion_r3 && by.discussion_r4, `the comments read ${JSON.stringify(github)}`);
      assert(by.discussion_r1.line === 5 && by.discussion_r1.lineText === "\tname := bb" && !by.discussion_r1.outdated && by.discussion_r1.author === "octocat", `the thread on its line reads ${JSON.stringify(by.discussion_r1)}`);
      assert(JSON.stringify(by.discussion_r1.forge) === JSON.stringify({ kind: "github", label: "GitHub", icon: "ti-brand-github", url: `${prPage}#discussion_r1`, thread: "PRRT_kwDOe2e1" }), `its forge reads ${JSON.stringify(by.discussion_r1.forge)}`);
      assert(by.discussion_r3.outdated && by.discussion_r3.lineText === "\te := 5", `the outdated thread reads ${JSON.stringify(by.discussion_r3)}`);
      assert(by.discussion_r4.line === 4 && by.discussion_r4.outdated && by.discussion_r4.lineText === "\tb := 2" && by.discussion_r4.resolved, `the resolved thread on the old side reads ${JSON.stringify(by.discussion_r4)}`);
      await waitMarks(page, "4r,5o,7x", 10000);
      await notReloaded(page);
    });

    await run("phone: the sheet's threads and a thread's dialog stand within 390px", async () => {
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
        const boxes = [...document.querySelectorAll("[data-editor-sheet-body] [data-comment-author], [data-editor-comment-modal].show .modal-content")]
          .map((el) => el.getBoundingClientRect()).filter((b) => b.width > 0);
        return { inner, scroll: document.documentElement.scrollWidth, out: boxes.filter((b) => b.left < 0 || b.right > inner).length, count: boxes.length };
      });
      let w = await fits();
      assert(w.count >= 3 && w.out === 0 && w.scroll <= w.inner, `the sheet on the phone: ${JSON.stringify(w)}`);
      await phone.locator("[data-editor-sheet-body] .row .dropdown-item", { hasText: "New name bb?" }).tap();
      await phone.locator(".dc-context-menu .dropdown-item", { hasText: "Open thread" }).tap();
      await modalShown(phone);
      w = await fits();
      assert(w.out === 0 && w.scroll <= w.inner, `the dialog on the phone: ${JSON.stringify(w)}`);
      assert(!(await phone.evaluate(() => document.activeElement?.matches("textarea:not([readonly]), input") || false)), "a writable field took the focus on touch");
    });

    await run("once the forge is no longer read the threads go without a reload, the own one stays: binding off, detached HEAD, token removed", async () => {
      await openEditor(page);
      const own = await postJSON(page, `/projects/${project}/editor/comments`, { path: FILE, line: 1, text: "mine stays" });
      assert(own.status === 200, `the own comment answered ${own.status} ${own.text}`);
      await waitMarks(page, "1o,4r,5o,7x", 15000);
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
      await git(["switch", "-q", "--detach"]);
      await gone("a detached HEAD");
      await git(["switch", "-q", "feature"]);
      await waitMarks(page, "1o,4r,5o,7x", 20000);
      const removed = await post(`${hostEdit}/token`, { token_remove: "1" });
      assert(removed.status === 200, `removing the token answered ${removed.status}`);
      await gone("the token removed");
      await notReloaded(page);
      const out = await postJSON(page, `/projects/${project}/editor/comments/delete`, { all: true });
      assert(out.status === 200 && out.text.includes('"removed":1'), `Delete all after the notes went answered ${out.status} ${out.text}`);
    });

    await run("a sheet with only threads of the forge shows Delete all comments disabled", async () => {
      await post(`${hostEdit}/token`, { token: TOKEN });
      await waitMarks(page, "4r,5o,7x", 20000);
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
      for (const said of ["the others are its replies", "Open the thread on", "at most every 1m", "follow their line like any comment", "go with the change request or the binding", "Delete all comments keeps them", "A guest reads them and answers none"]) {
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
