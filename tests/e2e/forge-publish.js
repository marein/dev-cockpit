const http = require("http");
const L = require("./lib");
const { assert, sleep, BASE } = L;

// Forge publish: line comments the owner wrote publish as review comments on
// the open change request of the branch a bound project has checked out
// (POST /projects/:name/forge/publish, internal/forge publish.go), each a
// review thread of its own on its line of the diff: Publish in a comment's
// entry of the Line comments sheet, Publish comment in the line's menu and
// Publish all comments in the sheet after asking, offered once the forge
// poller read an open change request (data-forge-publish in the forge
// fragment). A comment published becomes the thread of the forge it opened
// (internal/linecomment Published), keeping its id, with the forge's icon,
// its author and the link to its thread, and the poller's next read keeps it
// as it stands. On GitLab a line the change adds names its new line, one both
// sides share its old one too; on a GitHub Enterprise Server it is a review
// comment on the right side at the head commit. A comment on a line outside
// the diff refuses every comment of the same publish, one whose quoted line
// is not pushed is refused until it is, one outdated by a pushed change with
// the sentence that no push carries it, a token without the right to write
// in the forge's words, and nothing is posted. An assistant's publish over
// the local socket waits for the user's approval (the Review comment approval
// under Settings › Assistants › Approvals): the dialog names each comment
// with its line, the change request, branch, forge and project, Approve
// publishes them and the note links each thread, a comment changed meanwhile
// fails it. A guest of a shared link publishes nothing.
//
// The forge is the runner's own fake (forge-fake.js) on FORGE_PORT (default
// 3157), published on the host's loopback, where the instance reaches it at
// FORGE_ADDR (default 127.0.0.1); the diff of its merge request and pull
// request is the one this runner sets in state.diffFiles. The instance MUST
// be the assistant one (tests/e2e/fakes ahead of the real CLIs on PATH, a
// scratch HOME), with its local API socket mounted (APISOCK, default
// /apisock/s). A run takes about two minutes.

const { startFakeForge, addHost } = require("./forge-fake");

const FAKE_PORT = Number(process.env.FORGE_PORT || 3157);
const FAKE_TLS_PORT = Number(process.env.FORGE_TLS_PORT || 3158);
const FAKE_ADDR = process.env.FORGE_ADDR || "127.0.0.1";
const APISOCK = process.env.APISOCK || "/apisock/s";
const TOKEN = `glpat-e2e${Date.now().toString(36)}Pb7uK3wQ5xZ1`;
const DIALOG = ".swal2-popup:not(.swal2-toast)";
const FILE = "src/login.go";
const CONTENT = "package login\n\nfunc login() {\n\tname := bb\n\tc := 3\n\td := 4\n\treturn\n}\n\nfunc logout() {\n\treturn\n}\n";
const PATCH = "@@ -1,8 +1,8 @@\n package login\n \n func login() {\n-\tname := b\n+\tname := bb\n \tc := 3\n \td := 4\n \treturn\n }";
const BASE_SHA = "4b449edfa7d092dd25836adba8055a2e4223fbe8";
let fakeForge = null;

// asAssistant posts one form over the local socket as the assistant, which
// is what the generated `cockpit` wrapper does for a turn.
function asAssistant(id, path, body) {
  return new Promise((resolve, reject) => {
    const data = new URLSearchParams(body).toString();
    const req = http.request({
      socketPath: APISOCK,
      path,
      method: "POST",
      headers: {
        "X-Dev-Cockpit-Assistant": id,
        "Content-Type": "application/x-www-form-urlencoded",
        "Content-Length": Buffer.byteLength(data),
        Accept: "application/json",
      },
    }, (res) => {
      let raw = "";
      res.on("data", (chunk) => { raw += chunk; });
      res.on("end", () => {
        let json = {};
        try { json = JSON.parse(raw); } catch {}
        resolve({ status: res.statusCode, json, raw });
      });
    });
    req.on("error", reject);
    req.end(data);
  });
}

// canon is JSON with every object's keys sorted, so two bodies compare by
// what they hold and not by the order a sender wrote them in.
const canon = (v) => JSON.stringify(v, (_, x) => (x && typeof x === "object" && !Array.isArray(x) ? Object.fromEntries(Object.entries(x).sort(([a], [b]) => a.localeCompare(b))) : x));

async function waitFor(fn, what, timeout = 20000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    if (await fn()) return;
    await sleep(250);
  }
  throw new Error(`timed out waiting for ${what}`);
}

L.runFeature("FORGE_PUBLISH", async ({ browser, page, run, mobilePage, bag }) => {
  const stamp = Date.now().toString(36);
  const project = `zzforgepublish-${stamp}`;
  const alias = `fp-${stamp}`;
  const linkName = `forge publish ${stamp}`;
  const fakeWeb = `http://${FAKE_ADDR}:${FAKE_PORT}`;
  const mrPage = "https://gitlab.example.com/team/sub/app/-/merge_requests/4";
  const prPage = "https://github.example.com/octo/app/pull/7";
  const discussions = "/gl/api/v4/projects/team%2Fsub%2Fapp/merge_requests/4/discussions";
  fakeForge ||= await startFakeForge({ port: FAKE_PORT, tlsPort: FAKE_TLS_PORT, token: TOKEN });
  const state = fakeForge.state;
  let path = "";
  let hostEdit = "";
  let pushed = "";
  let assistant = "";
  let linkId = "";
  const ids = {};

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

  // post sends a form the way a page does and answers the JSON or the page.
  const post = (target, fields, p = page) => p.evaluate(async ({ target, fields }) => {
    const body = new URLSearchParams([...fields, ["csrf_token", document.querySelector('meta[name="csrf-token"]').content]]);
    const r = await fetch(target, { method: "POST", body, headers: { Accept: "application/json" } });
    const text = await r.text();
    let json = {};
    try { json = JSON.parse(text); } catch {}
    return { status: r.status, url: r.url, text, json };
  }, { target, fields: Array.isArray(fields) ? fields : Object.entries(fields) });

  const postJSON = (target, body) => page.evaluate(async ({ target, body }) => {
    const r = await fetch(target, {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]').content, Accept: "application/json" },
      body: JSON.stringify(body),
    });
    return { status: r.status, json: await r.json().catch(() => ({})) };
  }, { target, body });

  const saveFile = (content) => page.evaluate(([target, file, content]) => fetch(target, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded", "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]').content, Accept: "application/json" },
    body: new URLSearchParams({ path: file, content }).toString(),
  }).then((r) => r.status), [`/projects/${project}/editor/file`, FILE, content]);

  // comment adds an own comment on a line of FILE, quoting the disk unless
  // lineText says otherwise, and keeps its id under name.
  const comment = async (name, line, text, lineText) => {
    const r = await postJSON(`/projects/${project}/editor/comments`, { path: FILE, line, text, ...(lineText === undefined ? {} : { lineText }) });
    assert(r.status === 200 && r.json.comment?.id, `the comment ${name} answered ${r.status} ${JSON.stringify(r.json)}`);
    ids[name] = r.json.comment.id;
    return r.json.comment.id;
  };
  const uncomment = async (name) => {
    const r = await postJSON(`/projects/${project}/editor/comments/delete`, { ids: [ids[name]] });
    assert(r.status === 200 && r.json.removed === 1, `removing ${name} answered ${r.status} ${JSON.stringify(r.json)}`);
  };
  const comments = (p = page, base = "") => p.evaluate((target) => fetch(target, { headers: { Accept: "application/json" } }).then((r) => r.json()).then((d) => d.comments), `${base}/projects/${project}/editor/comments`);
  const byID = async (name) => (await comments()).find((c) => c.id === ids[name]);
  const posts = () => fakeForge.log.filter((r) => r.method === "POST" && /\/discussions$|\/pulls\/7\/comments$/.test(r.path));
  const postsAfter = (n) => posts().slice(n);

  const openEditor = async (p, base = BASE) => {
    await p.goto(`${base}/projects/${project}/editor?file=${encodeURIComponent(FILE)}`, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(p);
    await p.waitForSelector(".cm-lineNumbers", { state: "attached", timeout: 20000 });
    await p.waitForFunction(() => document.querySelector(".cm-content")?.textContent.includes("func login()"), null, { timeout: 15000 });
    await p.evaluate(() => { window.__forgePublishMark = 1; });
  };
  const notReloaded = async (p) => assert(await p.evaluate(() => window.__forgePublishMark === 1), "the editor reloaded");
  const openSheet = async (p) => {
    if (await p.locator("[data-editor-sheet]:not([hidden])").count()) return;
    await p.keyboard.press("Control+Shift+C");
    await p.waitForSelector("[data-editor-sheet]:not([hidden])", { timeout: 6000 });
  };
  const closeSheet = async (p) => {
    await p.keyboard.press("Escape");
    await p.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 6000 }).catch(() => {});
  };
  // publishRow reads the sheet's Publish all comments row, null for none.
  const publishRow = (p) => p.evaluate(() => {
    const row = [...document.querySelectorAll("[data-editor-sheet-body] > .dropdown-item")].find((r) => r.textContent.includes("Publish all comments") && r.getBoundingClientRect().height > 0);
    if (!row) return null;
    const box = row.getBoundingClientRect();
    return {
      label: row.querySelector(":scope > .d-flex > span:first-child").textContent.trim(),
      sub: row.querySelector(":scope > .d-flex > .small")?.textContent.trim() || "",
      icon: [...row.querySelector("i").classList].find((c) => c.startsWith("ti-")),
      disabled: row.disabled,
      left: box.left, right: box.right, inner: window.innerWidth,
    };
  });
  const waitPublishRow = async (p, sub, timeout = 30000) => {
    try {
      await p.waitForFunction((sub) => [...document.querySelectorAll("[data-editor-sheet-body] > .dropdown-item")]
        .some((r) => r.textContent.includes("Publish all comments") && r.textContent.includes(sub) && r.getBoundingClientRect().height > 0), sub, { timeout });
    } catch {
      throw new Error(`the sheet's publish row reads ${JSON.stringify(await publishRow(p))}, want ${sub}`);
    }
  };
  const menuItems = (p) => p.$$eval(".dc-context-menu .dropdown-item", (els) => els.map((e) => ({
    label: (e.querySelector(".dc-menu-label-head") || e).textContent.trim(),
    icon: [...(e.querySelector("i")?.classList || [])].find((c) => c.startsWith("ti-")) || "",
    hint: e.querySelector(".dc-menu-hint")?.textContent.trim() || "",
  })));
  const cellMenu = async (p, text) => {
    await p.locator("[data-editor-sheet-body] .row .dropdown-item", { hasText: text }).click();
    await p.waitForSelector(".dc-context-menu", { state: "visible", timeout: 6000 });
    return menuItems(p);
  };
  const closeMenu = async (p) => {
    await p.keyboard.press("Escape");
    await p.waitForSelector(".dc-context-menu", { state: "detached", timeout: 4000 });
  };
  const lineMenu = async (p, n) => {
    await sleep(400);
    await p.locator(".cm-lineNumbers .cm-gutterElement:visible").filter({ hasText: new RegExp(`^${n}$`) }).first().click();
    await p.waitForSelector(".dc-context-menu", { state: "visible", timeout: 6000 });
    return menuItems(p);
  };
  const toast = async (p, text) => {
    try {
      await p.waitForFunction((t) => [...document.querySelectorAll(".dc-toast")].some((el) => el.textContent.includes(t)), text, { timeout: 15000 });
    } catch {
      throw new Error(`no toast "${text}", the toasts read ${JSON.stringify(await p.$$eval(".dc-toast", (els) => els.map((e) => e.textContent.trim())))}`);
    }
  };
  // forgeCell waits until the sheet heads the comment holding text with the
  // forge's icon and the author there.
  const forgeCell = (p, text, icon, author) => p.waitForFunction(({ text, icon, author }) => [...document.querySelectorAll("[data-editor-sheet-body] .row .dropdown-item")]
    .some((e) => e.textContent.includes(text) && e.querySelector(`[data-comment-author] .${icon}`) && e.querySelector("[data-comment-author]").textContent.trim() === author), { text, icon, author }, { timeout: 15000 });
  const dialogRows = () => page.locator(`${DIALOG} dl[data-approval-details]`).evaluate((list) => {
    const labels = [...list.querySelectorAll("dt")].map((dt) => dt.textContent.trim());
    return [...list.querySelectorAll("dd")].map((dd, i) => [labels[i], dd.textContent.trim()]);
  });
  // approvalNote reads the assistant's page with a fetch, not a visit: a
  // visit leaves the page's conversation stream connecting, and the next
  // navigation that cuts it is a page error in WebKit.
  const approvalNote = async (verdict, count) => {
    let note = null;
    await waitFor(async () => {
      note = await page.evaluate(async ({ target, verdict, count }) => {
        const html = await (await fetch(target)).text();
        const notes = new DOMParser().parseFromString(html, "text/html").querySelectorAll(`[data-assistant-note="approval"][data-assistant-approval-note="${verdict}"]`);
        if (notes.length < count) return null;
        const last = notes[count - 1];
        return { headline: last.querySelector("[data-assistant-note-headline]").textContent.trim(), text: last.parentElement.textContent };
      }, { target: `/assistants/${assistant}`, verdict, count });
      return note !== null;
    }, `${count} ${verdict} approval notes`, 30000);
    return note;
  };

  try {
    await run("setup: a repository on the fake GitLab whose pushed branch feature has a merge request, the editor open on its file", async () => {
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
      pushed = await git(["rev-parse", "HEAD"]);
      await git(["update-ref", "refs/remotes/origin/feature", pushed]);
      await git(["remote", "add", "origin", `${fakeWeb}/gl/team/sub/app.git`]);
      Object.assign(state, { head: pushed, status: "success", readOnly: false, discussions: () => [], diffFiles: [{ path: FILE, patch: PATCH }], published: [] });
      fakeForge.log.splice(0);
      await openEditor(page);
      await comment("added", 4, "Why bb?");
      await openSheet(page);
      await sleep(800);
      assert(await publishRow(page) === null, "the sheet offers to publish before the forge was read");
      await closeSheet(page);
    });

    await run("the sheet offers Publish all comments once the merge request is read, a comment's entry Publish and the line's menu Publish comment, without a reload", async () => {
      const added = await addHost(page, { kind: "gitlab", web_url: `${fakeWeb}/gl`, aliases: alias, token: TOKEN });
      assert(!added.failed, added.failed);
      hostEdit = await page.evaluate(async (mark) => {
        const html = await (await fetch("/settings/forges")).text();
        const doc = new DOMParser().parseFromString(html, "text/html");
        return [...doc.querySelectorAll("[data-forge-host]")].find((r) => r.textContent.includes(mark))?.querySelector("[data-forge-edit]")?.getAttribute("href") || "";
      }, alias);
      assert(hostEdit, "the added host has no Edit on the list");
      await openSheet(page);
      await waitPublishRow(page, "each a review thread on merge request !4");
      const row = await publishRow(page);
      assert(row.icon === "ti-send" && !row.disabled, `the row reads ${JSON.stringify(row)}`);
      const items = await cellMenu(page, "Why bb?");
      assert(items.map((i) => i.label).join("|") === "Go to line|Open thread|Publish|Delete", `the comment's menu reads ${JSON.stringify(items)}`);
      const publish = items.find((i) => i.label === "Publish");
      assert(publish.icon === "ti-send" && publish.hint === "!4", `Publish reads ${JSON.stringify(publish)}`);
      await closeMenu(page);
      await closeSheet(page);
      const line = await lineMenu(page, 4);
      assert(line.slice(0, 3).map((i) => `${i.label} ${i.hint}`.trim()).join("|") === "Open thread Ctrl+Alt+C|Publish comment !4|Delete comment", `the line's menu reads ${JSON.stringify(line)}`);
      await closeMenu(page);
      const empty = await lineMenu(page, 5);
      assert(!empty.some((i) => i.label.startsWith("Publish")), `a line without a comment offers to publish: ${JSON.stringify(empty)}`);
      await closeMenu(page);
      assert(posts().length === 0, "offering posted something");
      await notReloaded(page);
    });

    await run("Publish posts the comment on its line of the diff, it becomes a thread of GitLab linked there, without a reload", async () => {
      const before = fakeForge.log.length;
      await openSheet(page);
      await cellMenu(page, "Why bb?");
      await page.locator(".dc-context-menu .dropdown-item", { hasText: "Publish" }).click();
      await toast(page, "The comment is published on merge request !4.");
      const sent = posts();
      assert(sent.length === 1 && sent[0].path === discussions && sent[0].query === "" && sent[0].auth === `Bearer ${TOKEN}`, `the forge was asked ${JSON.stringify(sent)}`);
      const want = { body: "Why bb?", position: { position_type: "text", base_sha: BASE_SHA, start_sha: BASE_SHA, head_sha: pushed, old_path: FILE, new_path: FILE, new_line: 4 } };
      assert(canon(sent[0].body) === canon(want), `the note was sent as ${JSON.stringify(sent[0].body)}`);
      const c = await byID("added");
      const { thread, ...origin } = c?.forge || {};
      assert(c && c.line === 4 && c.author === "root" && c.text === "Why bb?" && JSON.stringify(origin) === JSON.stringify({ kind: "gitlab", label: "GitLab", icon: "ti-brand-gitlab", url: `${mrPage}#note_50` }) && [undefined, "p50"].includes(thread), `the published comment reads ${JSON.stringify(c)}`);
      await forgeCell(page, "Why bb?", "ti-brand-gitlab", "root");
      await waitFor(() => {
        const after = fakeForge.log.slice(before);
        const at = after.findIndex((r) => r.method === "POST" && r.path === discussions);
        return at >= 0 && after.slice(at).some((r) => r.method === "GET" && r.path === discussions);
      }, "the poller's read of the threads after the publish", 30000);
      await sleep(1500);
      const list = await comments();
      assert(list.length === 1 && list[0].id === ids.added && list[0].line === 4 && list[0].forge?.url === `${mrPage}#note_50` && list[0].forge.thread === "p50", `after the poller's read the comments read ${JSON.stringify(list)}`);
      const items = await cellMenu(page, "Why bb?");
      assert(items.map((i) => i.label).join("|") === "Go to line|Open thread|Open the thread on GitLab", `the published comment's menu reads ${JSON.stringify(items)}`);
      await closeMenu(page);
      await closeSheet(page);
      await notReloaded(page);
    });

    await run("Publish all comments asks first and publishes every own comment it counted, a line both sides share with its old line", async () => {
      await comment("first", 1, "Name it auth?");
      await comment("context", 3, "Exported?");
      await openSheet(page);
      await page.locator("[data-editor-sheet-body] > .dropdown-item", { hasText: "Publish all comments" }).click();
      await page.waitForSelector(DIALOG, { state: "visible", timeout: 6000 });
      const said = { title: (await page.textContent(`${DIALOG} .swal2-title`)).trim(), text: (await page.textContent(`${DIALOG} .swal2-html-container`)).trim() };
      assert(said.title === "Publish all 2 comments?" && said.text === "Each becomes a review thread of its own on merge request !4 on GitLab, for everybody who can read it there.", `the dialog says ${JSON.stringify(said)}`);
      assert(posts().length === 1, "asking posted something");
      await comment("late", 5, "Written while asked.");
      await page.click(`${DIALOG} .swal2-confirm`);
      await toast(page, "The 2 comments are published on merge request !4.");
      const sent = postsAfter(1).map((r) => [r.body.position.new_line, r.body.position.old_line, r.body.body]);
      assert(JSON.stringify(sent) === JSON.stringify([[1, 1, "Name it auth?"], [3, 3, "Exported?"]]), `the forge was asked ${JSON.stringify(sent)}`);
      assert(!(await byID("late")).forge, "a comment written while the dialog stood was published");
      await uncomment("late");
      const list = await comments();
      assert(list.length === 3 && list.every((c) => c.forge?.kind === "gitlab"), `the comments read ${JSON.stringify(list)}`);
      await openSheet(page);
      const row = await publishRow(page);
      assert(row && row.disabled, `with nothing left to publish the row reads ${JSON.stringify(row)}`);
      await closeSheet(page);
    });

    await run("a comment outside the diff refuses the whole publish and posts nothing, one whose line is not pushed is refused until it is, an outdated one with no push to wait for", async () => {
      await comment("inside", 5, "Is c needed?");
      await comment("outside", 10, "Logout too?");
      const sent = posts().length;
      await openSheet(page);
      await page.locator("[data-editor-sheet-body] > .dropdown-item", { hasText: "Publish all comments" }).click();
      await page.waitForSelector(DIALOG, { state: "visible", timeout: 6000 });
      await page.click(`${DIALOG} .swal2-confirm`);
      await toast(page, `1 of the 2 comments stands on no line the diff of merge request !4 shows, nothing was published: "${FILE}":10.`);
      assert(posts().length === sent, "a publish with a comment outside the diff posted something");
      assert(!(await byID("inside")).forge && !(await byID("outside")).forge, "a refused comment became a note of the forge");
      await uncomment("outside");
      assert(await saveFile(CONTENT.replace("\td := 4", "\tlocal := 1")) === 200, "the file could not be written");
      await comment("local", 6, "Saved here");
      const refused = await post(`/projects/${project}/forge/publish`, { id: ids.local });
      assert(refused.status === 409 && refused.json.error === `The line the comment at "${FILE}":6 quotes is not in the pushed head ${pushed.slice(0, 12)} of "feature", push it first.`, `a line not pushed answered ${refused.status} ${refused.text}`);
      await uncomment("local");
      assert(await saveFile(CONTENT) === 200, "the file could not be written back");
      const outdated = `The line the comment at "${FILE}":6 quotes is not in the saved file: save it, or delete the outdated comment or write it again on its line.`;
      await comment("stale", 6, "Is 4 right?");
      assert(await saveFile(CONTENT.replace("\td := 4", "\td := 5")) === 200, "the file could not be changed");
      await git(["commit", "-q", "-am", "five"]);
      const moved = await git(["rev-parse", "HEAD"]);
      await git(["update-ref", "refs/remotes/origin/feature", moved]);
      state.head = moved;
      try {
        await waitFor(async () => (await byID("stale"))?.outdated === true, "the comment outdated by the pushed change");
        await openSheet(page);
        await cellMenu(page, "Is 4 right?");
        await page.locator(".dc-context-menu .dropdown-item", { hasText: "Publish" }).click();
        await toast(page, outdated);
        const all = await post(`/projects/${project}/forge/publish`, { all: "1" });
        assert(all.status === 409 && all.json.error === outdated, `Publish all with an outdated comment answered ${all.status} ${all.text}`);
        await closeSheet(page);
      } finally {
        await uncomment("stale");
        await git(["reset", "-q", "--hard", pushed]);
        await git(["update-ref", "refs/remotes/origin/feature", pushed]);
        state.head = pushed;
      }
      assert(posts().length === sent, "a refused publish posted something");
    });

    await run("a token without the right to write is refused in GitLab's words and the comment stays", async () => {
      await openSheet(page);
      state.readOnly = true;
      try {
        await cellMenu(page, "Is c needed?");
        await page.locator(".dc-context-menu .dropdown-item", { hasText: "Publish" }).click();
        await toast(page, "The token may not write to GitLab: give it the scope api.");
      } finally {
        state.readOnly = false;
      }
      assert(!(await byID("inside")).forge, "the refused comment became a note of the forge");
      await closeSheet(page);
    });

    await run("an assistant's publish waits for the approval, the dialog names it all, Approve publishes and the note links the thread", async () => {
      const made = await post("/assistants/new", { form: "new", coder: "claude" });
      assistant = made.json.id || "";
      assert(assistant, `no assistant for the checks: ${made.status}`);
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      const sent = posts().length;
      const answer = await asAssistant(assistant, `/projects/${project}/forge/publish`, { id: ids.inside });
      const what = `Publish a line comment on merge request !4 of feature in ${project}`;
      assert(answer.status === 200 && answer.json.pending === true && answer.json.what === what, `the socket answered ${answer.status} ${answer.raw}`);
      await page.waitForSelector(`${DIALOG} [data-approval-what]`, { timeout: 15000 });
      assert((await page.textContent(`${DIALOG} [data-approval-what]`)).trim() === what, "the dialog's first line is another");
      const want = [
        ["Asked by", `Assistant · ${assistant.slice(0, 8)}`],
        ["Action", "Publish line comments"],
        ["Comment", `${FILE}:5 · Is c needed?`],
        ["On", `merge request !4 · Fix the login · ${mrPage}`],
        ["Branch", `feature at ${pushed.slice(0, 12)}`],
        ["Forge", `GitLab · ${FAKE_ADDR}:${FAKE_PORT}/gl/team/sub/app`],
        ["Project", project],
      ];
      const rows = await dialogRows();
      assert(JSON.stringify(rows) === JSON.stringify(want), `the dialog's rows read ${JSON.stringify(rows)}`);
      const again = await asAssistant(assistant, `/projects/${project}/forge/publish`, { all: "1" });
      assert(again.status === 409 && again.json.error === `${what} already waits for the user's approval.`, `the same publish again answered ${again.status} ${again.raw}`);
      assert(posts().length === sent, "a waiting publish posted something");
      await page.click(`${DIALOG} .swal2-confirm`);
      await page.waitForSelector(DIALOG, { state: "detached", timeout: 8000 });
      const note = await approvalNote("done", 1);
      assert(note.headline === `Approved: ${what}`, `the note reads ${note.headline}`);
      for (const says of [`On GitLab ${FAKE_ADDR}:${FAKE_PORT}/gl/team/sub/app the line comment of feature in ${project} is published on the merge request !4 "Fix the login":`, `${FILE}:5 ${mrPage}#note_53`]) {
        assert(note.text.includes(says), `the note lacks ${says}: ${note.text}`);
      }
      assert(posts().length === sent + 1 && (await byID("inside")).forge?.url === `${mrPage}#note_53`, "the approval published nothing");
    });

    await run("an approval of a comment changed meanwhile fails and publishes nothing", async () => {
      await comment("changed", 6, "Is d needed?");
      const sent = posts().length;
      const answer = await asAssistant(assistant, `/projects/${project}/forge/publish`, { id: ids.changed });
      assert(answer.json.pending === true, `the publish did not wait: ${answer.raw}`);
      await page.waitForSelector(`${DIALOG} .swal2-confirm`, { timeout: 15000 });
      const edit = await postJSON(`/projects/${project}/editor/comments`, { id: ids.changed, path: FILE, line: 6, text: "Rather drop d." });
      assert(edit.status === 200, `the edit answered ${edit.status}`);
      await page.click(`${DIALOG} .swal2-confirm`);
      await page.waitForSelector(DIALOG, { state: "detached", timeout: 8000 });
      const note = await approvalNote("failed", 1);
      assert(note.text.includes(`The comment at "${FILE}":6 changed since you were asked, nothing was published.`), `the note says ${note.text}`);
      assert(posts().length === sent && !(await byID("changed")).forge, "the approval of a changed comment published it");
    });

    await run("the Approvals tab lists the Review comment approval, off publishes an assistant's comments at once, the socket cannot move it", async () => {
      await page.goto(`${BASE}/settings/assistant/approvals`, { waitUntil: "domcontentloaded" });
      const kinds = await page.locator("[data-assistant-approval]").evaluateAll((rows) => rows.map((row) => row.dataset.assistantApproval));
      assert(JSON.stringify(kinds) === JSON.stringify(["compose-actions", "project-delete", "coder-delete", "assistant-delete", "worktree-post-script", "forge-retry", "forge-change", "forge-publish", "forge-thread"]), `the tab lists ${JSON.stringify(kinds)}`);
      const label = (await page.locator('[data-assistant-approval="forge-publish"]').innerText()).replace(/\s+/g, " ").trim();
      assert(label === "Review comment approval Line comments an assistant wants to publish on a forge stay unpublished until you approve.", `the switch reads ${label}`);
      const refused = await asAssistant(assistant, "/settings/assistant/approvals", { "approval-forge-publish": "0" });
      assert(refused.status === 403, `the socket moved the switch: ${refused.status}`);
      const off = await post("/settings/assistant/approvals", { "approval-forge-publish": "0" });
      assert(off.status === 200, `switching it off answered ${off.status}`);
      const answer = await asAssistant(assistant, `/projects/${project}/forge/publish`, { all: "1" });
      assert(answer.status === 200 && !answer.json.pending && answer.json.published === `On GitLab ${FAKE_ADDR}:${FAKE_PORT}/gl/team/sub/app the line comment of feature in ${project} is published on the merge request !4 "Fix the login":\n- ${FILE}:6 ${mrPage}#note_54`, `with the switch off the socket answered ${answer.raw}`);
      const on = await post("/settings/assistant/approvals", { "approval-forge-publish": "1" });
      assert(on.status === 200, `switching it on answered ${on.status}`);
    });

    await run("GitHub: Publish posts a review comment on the right side at the head commit, and it becomes a thread of GitHub", async () => {
      await openEditor(page);
      const saved = await post(hostEdit, { form: "save", kind: "github", web_url: fakeWeb, aliases: alias });
      assert(saved.status === 200, `turning the host into a GitHub answered ${saved.status}`);
      await git(["remote", "set-url", "origin", `${fakeWeb}/octo/app.git`]);
      await comment("github", 7, "Return early?");
      await openSheet(page);
      await waitPublishRow(page, "each a review thread on pull request #7");
      const sent = posts().length;
      const before = fakeForge.log.length;
      await cellMenu(page, "Return early?");
      await page.locator(".dc-context-menu .dropdown-item", { hasText: "Publish" }).click();
      await toast(page, "The comment is published on pull request #7.");
      const asked = postsAfter(sent);
      assert(asked.length === 1 && asked[0].path === "/api/v3/repos/octo/app/pulls/7/comments" && asked[0].auth === `Bearer ${TOKEN}`, `the forge was asked ${JSON.stringify(asked)}`);
      assert(canon(asked[0].body) === canon({ body: "Return early?", commit_id: pushed, path: FILE, line: 7, side: "RIGHT" }), `the comment was sent as ${JSON.stringify(asked[0].body)}`);
      const id = 60 + state.published.length - 1;
      const c = await byID("github");
      const { thread, ...origin } = c?.forge || {};
      assert(c && c.author === "octocat" && JSON.stringify(origin) === JSON.stringify({ kind: "github", label: "GitHub", icon: "ti-brand-github", url: `${prPage}#discussion_r${id}` }) && [undefined, `PRRT_kwDOe2ep${id}`].includes(thread), `the published comment reads ${JSON.stringify(c)}`);
      await forgeCell(page, "Return early?", "ti-brand-github", "octocat");
      await waitFor(() => {
        const after = fakeForge.log.slice(before);
        const at = after.findIndex((r) => r.method === "POST" && r.path === "/api/v3/repos/octo/app/pulls/7/comments");
        return at >= 0 && after.slice(at).some((r) => r.path === "/api/graphql");
      }, "the poller's read of the threads after the publish", 30000);
      await sleep(1500);
      const after = await byID("github");
      assert(after && after.line === 7 && after.forge?.url === `${prPage}#discussion_r${id}` && after.forge.thread === `PRRT_kwDOe2ep${id}`, `after the poller's read it reads ${JSON.stringify(after)}`);
      await closeSheet(page);
      await notReloaded(page);
    });

    await run("phone: the sheet's Publish all comments stands within 390px", async () => {
      await comment("phone", 2, "Blank line?");
      const phone = await mobilePage();
      await openEditor(phone);
      await openSheet(phone).catch(async () => {
        await phone.click("[data-editor-menu]");
        await phone.locator("[data-editor-comments-item]").click();
        await phone.waitForSelector("[data-editor-sheet]:not([hidden])", { timeout: 6000 });
      });
      await waitPublishRow(phone, "pull request #7");
      const row = await publishRow(phone);
      const scroll = await phone.evaluate(() => document.documentElement.scrollWidth);
      assert(row.left >= 0 && row.right <= row.inner && scroll <= row.inner && !row.disabled, `the row on the phone: ${JSON.stringify({ ...row, scroll })}`);
    });

    await run("a guest of a shared link sees no Publish and reaches none", async () => {
      await page.goto(`${BASE}/settings/sharing`, { waitUntil: "domcontentloaded" });
      const made = await post("/settings/sharing/new", [["amount", "1"], ["unit", "hours"], ["name", linkName], ["project", project], ["grant:comments", project]]);
      const link = (made.text.match(/value="([^"]*\/share\/[^"]+)"/) || [])[1];
      assert(link, `making a link answered ${made.status}`);
      await page.goto(`${BASE}/settings/sharing`, { waitUntil: "domcontentloaded" });
      linkId = await page.evaluate((name) => [...document.querySelectorAll("[data-share-row]")]
        .find((r) => r.querySelector("[data-share-name]")?.textContent.trim() === name)?.dataset.shareRow || "", linkName);
      const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1360, height: 900 } });
      try {
        const guest = await ctx.newPage();
        L.wirePage(guest, bag);
        const base = new URL(link).pathname;
        await openEditor(guest, `${new URL(link).origin}${base}`);
        await openSheet(guest);
        await guest.waitForSelector("[data-editor-sheet-body] .row .dropdown-item", { timeout: 15000 });
        await sleep(1500);
        assert(await publishRow(guest) === null, "the guest's sheet offers Publish all comments");
        const items = await cellMenu(guest, "Blank line?");
        assert(!items.some((i) => i.label.startsWith("Publish")), `the guest's menu reads ${JSON.stringify(items)}`);
        await closeMenu(guest);
        const sent = posts().length;
        const code = await guest.evaluate(async (target) => (await fetch(target, { method: "POST", headers: { "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]')?.content || "", "Content-Type": "application/x-www-form-urlencoded" }, body: "all=1" })).status, `${base}/projects/${project}/forge/publish`);
        assert(code === 404, `a guest's publish answered ${code}`);
        assert(posts().length === sent && !(await byID("phone")).forge, "a guest's publish posted something");
      } finally {
        await ctx.close();
      }
    });

    await run("docs: the Forges topic explains the publish of line comments, Tokens, Approvals and Line comments name it", async () => {
      await page.goto(`${BASE}/docs`, { waitUntil: "domcontentloaded" });
      const item = page.locator("section#forges .list-group-item", { hasText: /^\s*Publish line comments/ });
      assert(await item.count() === 1, "no docs entry for the publish of line comments");
      const text = await item.innerText();
      for (const said of ["Publish comment", "Publish all comments", "in the pushed head and in the change request's diff", "else the publish is refused as a whole", "becomes that review thread", "A guest's comment and one with replies do not publish", "goes behind a backslash", "forge-comment-publish", "only once you approve"]) {
        assert(text.includes(said), `the entry lacks "${said}": ${text}`);
      }
      const tokens = await page.locator("section#forges .list-group-item", { hasText: "Tokens and the connection test" }).innerText();
      assert(tokens.includes("writing api") && tokens.includes("Pull requests read and write"), `the tokens' entry reads ${tokens}`);
      const approvals = await page.locator("section#assistants .list-group-item", { hasText: /^\s*Approvals/ }).innerText();
      assert(approvals.includes("a publish of line comments") && approvals.includes("comments or thread"), `the approvals' entry reads ${approvals}`);
      const lines = await page.locator("section#editor .list-group-item", { hasText: "Line comments" }).first().innerText();
      assert(lines.includes("The forge's review threads stand among them, see Forges"), `the line comments' entry reads ${lines}`);
    });
  } finally {
    if (linkId) await post(`/settings/sharing/${linkId}`, { form: "revoke" }).catch(() => {});
    await post("/settings/assistant/approvals", { "approval-forge-publish": "1" }).catch(() => {});
    if (hostEdit) await post(hostEdit, { form: "delete" }).catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
    if (assistant) await post(`/assistants/${assistant}`, { form: "delete" }).catch(() => {});
  }
});
