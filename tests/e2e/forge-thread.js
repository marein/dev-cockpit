const http = require("http");
const L = require("./lib");
const { assert, sleep, BASE } = L;

// Forge thread: a review thread on the diff of the change request of the
// branch a bound project has checked out takes a reply and a resolve
// (POST /projects/:name/forge/thread, internal/forge reply.go) from its line
// comment thread: Open thread in its entry of the Line comments sheet and in
// the line's menu, two threads on one line each by whose it is and how it
// opens, shows the reply field and, while the thread is unresolved, Resolve
// the thread, offered once the forge poller read the change request
// (data-forge-threads in the forge fragment) and only where a read named the
// thread. The switch resolves the thread after the reply, or alone with the
// field empty. The forge is read again first; a thread it does not hold, a
// general one and a resolve of a resolved one are refused, a token without
// the right to write in the forge's words, and a resolve refused after the
// reply went closes the dialog all the same, the reply posted once. The line
// comments follow with the poller's read right after the write: the reply
// stands under the thread, a resolved thread turns green. On GitLab a reply is a note of the discussion and a resolve a PUT of
// it, on a GitHub Enterprise Server both go over GraphQL, a reply GitHub puts
// into a pending review is said. An assistant's reply or resolve over the
// local socket waits for the user's approval (the Review thread approval
// under Settings › Assistants › Approvals): the dialog names the thread with
// its opening note, the reply, the change request, branch, forge and project,
// Approve writes it and the note links the reply, a thread gone meanwhile
// fails it. A guest of a shared link sees no thread id, no reply field and
// reaches no write.
//
// The forge is the runner's own fake (forge-fake.js) on FORGE_PORT (default
// 3157), published on the host's loopback, where the instance reaches it at
// FORGE_ADDR (default 127.0.0.1); its merge request's discussions are the
// ones this runner sets in state.discussions. The instance MUST be the
// assistant one (tests/e2e/fakes ahead of the real CLIs on PATH, a scratch
// HOME), with its local API socket mounted (APISOCK, default /apisock/s). A
// run takes about two minutes.

const { startFakeForge, addHost, gitlabNote, gitlabDiscussion } = require("./forge-fake");

const FAKE_PORT = Number(process.env.FORGE_PORT || 3157);
const FAKE_TLS_PORT = Number(process.env.FORGE_TLS_PORT || 3158);
const FAKE_ADDR = process.env.FORGE_ADDR || "127.0.0.1";
const APISOCK = process.env.APISOCK || "/apisock/s";
const TOKEN = `glpat-e2e${Date.now().toString(36)}Th5rE9aD2wQ7`;
const DIALOG = ".swal2-popup:not(.swal2-toast)";
const MODAL = "[data-editor-comment-modal]";
const FILE = "src/login.go";
const CONTENT = "package login\n\nfunc login() {\n\tname := bb\n\tc := 3\n\td := 4\n\treturn\n}\n";
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

async function waitFor(fn, what, timeout = 20000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    if (await fn()) return;
    await sleep(250);
  }
  throw new Error(`timed out waiting for ${what}`);
}

L.runFeature("FORGE_THREAD", async ({ browser, page, run, mobilePage, bag }) => {
  const stamp = Date.now().toString(36);
  const project = `zzforgethread-${stamp}`;
  const alias = `ft-${stamp}`;
  const linkName = `forge thread ${stamp}`;
  const fakeWeb = `http://${FAKE_ADDR}:${FAKE_PORT}`;
  const address = `${FAKE_ADDR}:${FAKE_PORT}/gl/team/sub/app`;
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
  let gone = false;
  state.discussions = (s) => [
    ...(gone ? [] : [gitlabDiscussion("t1e2e", false, [gitlabNote(29, "alice", "New name bb?", { resolvable: true, at: { head: s.head, newLine: 4 } })])]),
    gitlabDiscussion("t2e2e", false, [gitlabNote(30, "alice", "Return early?", { resolvable: true, at: { head: s.head, newLine: 7 } })]),
    gitlabDiscussion("t3e2e", false, [gitlabNote(31, "bob", "Or call it auth?", { resolvable: true, at: { head: s.head, newLine: 4 } })]),
    gitlabDiscussion("t4e2e", false, [gitlabNote(32, "alice", "Is c needed?", { resolvable: true, at: { head: s.head, newLine: 5 } })]),
    gitlabDiscussion("g1e2e", false, [gitlabNote(34, "alice", "General thread", { resolvable: true })]),
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

  // post sends a form the way a page does and answers the JSON or the page.
  const post = (target, fields, p = page) => p.evaluate(async ({ target, fields }) => {
    const body = new URLSearchParams([...fields, ["csrf_token", document.querySelector('meta[name="csrf-token"]').content]]);
    const r = await fetch(target, { method: "POST", body, headers: { Accept: "application/json" } });
    const text = await r.text();
    let json = {};
    try { json = JSON.parse(text); } catch {}
    return { status: r.status, url: r.url, text, json };
  }, { target, fields: Array.isArray(fields) ? fields : Object.entries(fields) });

  const saveFile = (content) => page.evaluate(([target, file, content]) => fetch(target, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded", "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]').content, Accept: "application/json" },
    body: new URLSearchParams({ path: file, content }).toString(),
  }).then((r) => r.status), [`/projects/${project}/editor/file`, FILE, content]);

  const comments = (p = page, base = "") => p.evaluate((target) => fetch(target, { headers: { Accept: "application/json" } }).then((r) => r.json()).then((d) => d.comments), `${base}/projects/${project}/editor/comments`);
  const ofThread = async (thread) => (await comments()).find((c) => c.forge?.thread === thread) || null;
  const replyTexts = (c) => (c?.replies || []).map((r) => r.text);
  // writes are the requests that answer or resolve a thread, in order.
  const writes = () => fakeForge.log.filter((r) => (r.method === "POST" && /\/discussions\/[^/]+\/notes$/.test(r.path)) || (r.method === "PUT" && /\/discussions\/[^/]+$/.test(r.path)) || r.mutation);
  const writesAfter = (n) => writes().slice(n).map((r) => (r.mutation ? `${r.mutation} ${JSON.stringify(r.variables)}` : `${r.method} ${r.path.slice(discussions.length)}${r.query} ${JSON.stringify(r.body)}`));

  const openEditor = async (p, base = BASE) => {
    await p.goto(`${base}/projects/${project}/editor?file=${encodeURIComponent(FILE)}`, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(p);
    await p.waitForSelector(".cm-lineNumbers", { state: "attached", timeout: 20000 });
    await p.waitForFunction(() => document.querySelector(".cm-content")?.textContent.includes("func login()"), null, { timeout: 15000 });
    await p.evaluate(() => { window.__forgeThreadMark = 1; });
  };
  const notReloaded = async (p) => assert(await p.evaluate(() => window.__forgeThreadMark === 1), "the editor reloaded");
  const openSheet = async (p) => {
    if (await p.locator("[data-editor-sheet]:not([hidden])").count()) return;
    await p.keyboard.press("Control+Shift+C");
    await p.waitForSelector("[data-editor-sheet]:not([hidden])", { timeout: 6000 });
  };
  const closeSheet = async (p) => {
    await p.keyboard.press("Escape");
    await p.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 6000 }).catch(() => {});
  };
  const menuItems = (p) => p.$$eval(".dc-context-menu .dropdown-item", (els) => els.map((e) => ({
    label: (e.querySelector(".dc-menu-label-head") || e).textContent.trim(),
    icon: [...(e.querySelector("i")?.classList || [])].find((c) => c.startsWith("ti-")) || "",
    hint: e.querySelector(".dc-menu-hint")?.textContent.trim() || "",
  })));
  const clickItem = async (p, label) => {
    const items = await menuItems(p);
    const at = items.findIndex((i) => i.label === label);
    assert(at >= 0, `the menu has no ${label}: ${JSON.stringify(items)}`);
    await p.locator(".dc-context-menu .dropdown-item").nth(at).click();
  };
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
  // modal reads the comment dialog the way it shows, every part by its box.
  const modal = (p) => p.evaluate((sel) => {
    const m = document.querySelector(sel);
    const shown = (el) => !!el && getComputedStyle(el).display !== "none" && getComputedStyle(el).visibility !== "hidden" && el.getBoundingClientRect().height > 0;
    const box = m.querySelector(".modal-dialog").getBoundingClientRect();
    const row = m.querySelector("[data-editor-comment-resolve-row]");
    return {
      open: m.classList.contains("show") && shown(m),
      title: m.querySelector("[data-editor-comment-title]").textContent.trim(),
      author: shown(m.querySelector("[data-editor-comment-author]")) ? m.querySelector("[data-editor-comment-author]").textContent.trim() : "",
      notes: [...(shown(m.querySelector("[data-editor-comment-thread]")) ? m.querySelectorAll("[data-editor-comment-thread] [data-comment-note]") : [])].map((n) => `${n.querySelector("[data-comment-note-author]").textContent.trim()}: ${n.querySelector("[data-comment-note-text]").textContent}`),
      field: shown(m.querySelector("[data-editor-comment-text]")) && !m.querySelector("[data-editor-comment-text]").readOnly,
      save: shown(m.querySelector("[data-editor-comment-save]")) ? m.querySelector("[data-editor-comment-save]").textContent.trim() : "",
      text: m.querySelector("[data-editor-comment-text]").value,
      readOnly: m.querySelector("[data-editor-comment-text]").readOnly,
      invalid: m.querySelector("[data-editor-comment-text]").classList.contains("is-invalid"),
      hint: shown(m.querySelector("[data-editor-comment-hint]")) ? m.querySelector("[data-editor-comment-hint]").textContent.trim() : "",
      resolve: shown(row),
      resolveRight: row ? row.getBoundingClientRect().right : 0,
      left: box.left, right: box.right, inner: window.innerWidth,
    };
  }, MODAL);
  const waitModal = async (p) => {
    await p.waitForFunction((sel) => document.querySelector(sel)?.classList.contains("show"), MODAL, { timeout: 6000 });
    await sleep(400);
  };
  // openFromCell opens the thread of the sheet's entry holding text.
  const openFromCell = async (p, text) => {
    await openSheet(p);
    await cellMenu(p, text);
    await clickItem(p, "Open thread");
    await waitModal(p);
  };
  const waitModalGone = (p) => p.waitForFunction((sel) => !document.querySelector(sel)?.classList.contains("show") && getComputedStyle(document.querySelector(sel)).display === "none", MODAL, { timeout: 6000 });
  const dialogRows = () => page.locator(`${DIALOG} dl[data-approval-details]`).evaluate((list) => {
    const labels = [...list.querySelectorAll("dt")].map((dt) => dt.textContent.trim());
    return [...list.querySelectorAll("dd")].map((dd, i) => [labels[i], dd.textContent.trim()]);
  });
  // approvalNote reads the assistant's page with a fetch, not a visit, see
  // forge-publish.js.
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
    await run("setup: a repository on the fake GitLab whose pushed branch feature has a merge request with review threads, the editor open on its file", async () => {
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
      Object.assign(state, { head: pushed, status: "success", readOnly: false, ghPending: false, diffFiles: [], published: [], replies: [], resolved: [] });
      fakeForge.log.splice(0);
      const own = await page.evaluate(async ([target, body]) => (await fetch(target, {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]').content, Accept: "application/json" },
        body: JSON.stringify(body),
      })).status, [`/projects/${project}/editor/comments`, { path: FILE, line: 3, text: "Mine." }]);
      assert(own === 200, `the own comment answered ${own}`);
      const added = await addHost(page, { kind: "gitlab", web_url: `${fakeWeb}/gl`, aliases: alias, token: TOKEN });
      assert(!added.failed, added.failed);
      hostEdit = await page.evaluate(async (mark) => {
        const html = await (await fetch("/settings/forges")).text();
        const doc = new DOMParser().parseFromString(html, "text/html");
        return [...doc.querySelectorAll("[data-forge-host]")].find((r) => r.textContent.includes(mark))?.querySelector("[data-forge-edit]")?.getAttribute("href") || "";
      }, alias);
      assert(hostEdit, "the added host has no Edit on the list");
      await openEditor(page);
      await waitFor(async () => (await comments()).filter((c) => c.forge?.thread).length === 4, "the notes of the four threads on the diff with their threads", 30000);
      const list = await comments();
      const by = Object.fromEntries(list.filter((c) => c.forge).map((c) => [c.text, c.forge.thread]));
      assert(JSON.stringify(by) === JSON.stringify({ "New name bb?": "t1e2e", "Or call it auth?": "t3e2e", "Is c needed?": "t4e2e", "Return early?": "t2e2e" }), `the notes read ${JSON.stringify(list)}`);
    });

    await run("a thread of the forge opens from its entry and from the line's menu, two threads on one line each by whose it is and how it opens, nothing written", async () => {
      await openSheet(page);
      const items = await cellMenu(page, "Return early?");
      assert(items.map((i) => i.label).join("|") === "Go to line|Open thread|Open the thread on GitLab", `the thread's menu reads ${JSON.stringify(items)}`);
      await closeMenu(page);
      const own = await cellMenu(page, "Mine.");
      assert(own.some((i) => i.label === "Open thread") && !own.some((i) => /^(Reply|Resolve)/.test(i.label)), `an own comment's menu reads ${JSON.stringify(own)}`);
      await closeMenu(page);
      await closeSheet(page);
      const line = await lineMenu(page, 4);
      assert(JSON.stringify(line.slice(0, 3).map((i) => i.label)) === JSON.stringify(["Open alice's thread: New name bb?", "Open bob's thread: Or call it auth?", "Add line comment"]) && line[3].label === "Copy path:line" && line.slice(0, 2).every((i) => i.icon === "ti-brand-gitlab"), `the line's menu reads ${JSON.stringify(line)}`);
      await closeMenu(page);
      const single = (await lineMenu(page, 7)).map((i) => i.label);
      assert(JSON.stringify(single.slice(0, 3)) === JSON.stringify(["Comment by alice", "Open thread", "Add line comment"]), `the menu of a line with one thread reads ${JSON.stringify(single)}`);
      await closeMenu(page);
      assert(writes().length === 0, "offering wrote something");
      await notReloaded(page);
    });

    await run("Open thread shows the thread with a reply field, an empty one stays, the reply posts and stands under the thread, without a reload", async () => {
      await openFromCell(page, "Return early?");
      let m = await modal(page);
      assert(m.open && m.title === "Line comment" && m.author === "" && JSON.stringify(m.notes) === JSON.stringify(["alice: Return early?"]) && m.save === "Reply" && m.text === "" && m.field && m.resolve && m.hint === "It is posted on merge request !4 on GitLab, for everybody who can read it there.", `the dialog reads ${JSON.stringify(m)}`);
      await page.click(`${MODAL} [data-editor-comment-save]`);
      m = await modal(page);
      assert(m.open && m.invalid && writes().length === 0, `an empty reply left ${JSON.stringify(m)}`);
      await page.fill(`${MODAL} [data-editor-comment-text]`, "fixed in abc123");
      await page.click(`${MODAL} [data-editor-comment-save]`);
      await toast(page, "The reply is posted on merge request !4.");
      await waitModalGone(page);
      const sent = writes();
      assert(sent.length === 1 && sent[0].path === `${discussions}/t2e2e/notes` && sent[0].query === "" && sent[0].auth === `Bearer ${TOKEN}` && JSON.stringify(sent[0].body) === JSON.stringify({ body: "fixed in abc123" }), `the forge was asked ${JSON.stringify(sent)}`);
      await waitFor(async () => replyTexts(await ofThread("t2e2e")).length === 1, "the reply under the thread", 30000);
      const thread = await ofThread("t2e2e");
      assert(thread.line === 7 && thread.replies[0].id === "70" && thread.replies[0].author === "root" && thread.replies[0].text === "fixed in abc123" && !thread.resolved, `the thread reads ${JSON.stringify(thread)}`);
      await page.waitForFunction(() => [...document.querySelectorAll("[data-editor-sheet-body] .row .dropdown-item")].some((e) => e.textContent.includes(":7 · 1 reply")), null, { timeout: 10000 });
      await openFromCell(page, "Return early?");
      m = await modal(page);
      assert(JSON.stringify(m.notes) === JSON.stringify(["alice: Return early?", "root: fixed in abc123"]), `the thread's dialog reads ${JSON.stringify(m)}`);
      await page.click(`${MODAL} [data-editor-comment-cancel]`);
      await waitModalGone(page);
      await notReloaded(page);
    });

    await run("the switch alone resolves the thread, it turns green and offers no switch any more", async () => {
      await openFromCell(page, "Return early?");
      await page.check(`${MODAL} [data-editor-comment-resolve]`);
      assert((await modal(page)).save === "Resolve", "the button does not read Resolve with the switch on and the field empty");
      await page.click(`${MODAL} [data-editor-comment-save]`);
      await toast(page, "The thread is resolved on merge request !4.");
      await waitModalGone(page);
      assert(JSON.stringify(writesAfter(1)) === JSON.stringify(["PUT /t2e2e?resolved=true {}"]), `the forge was asked ${JSON.stringify(writesAfter(1))}`);
      await waitFor(async () => (await ofThread("t2e2e"))?.resolved, "the thread resolved", 30000);
      await page.waitForFunction(() => [...document.querySelectorAll(".cm-lineNumbers .cm-comment-line")].some((el) => el.textContent.trim() === "7" && el.classList.contains("cm-comment-line-resolved")), null, { timeout: 10000 });
      await openFromCell(page, "Return early?");
      const m = await modal(page);
      assert(m.open && m.author === "Resolved" && !m.resolve && m.field && m.save === "Reply", `the dialog of a resolved thread reads ${JSON.stringify(m)}`);
      await page.click(`${MODAL} [data-editor-comment-cancel]`);
      await waitModalGone(page);
      assert(writes().length === 2, "a cancelled dialog wrote something");
      await closeSheet(page);
    });

    await run("a reply with Resolve the thread checked posts the reply and then resolves the thread, from the line's menu", async () => {
      await lineMenu(page, 4);
      await clickItem(page, "Open bob's thread: Or call it auth?");
      await waitModal(page);
      const m = await modal(page);
      assert(JSON.stringify(m.notes) === JSON.stringify(["bob: Or call it auth?"]) && m.resolve, `the dialog reads ${JSON.stringify(m)}`);
      await page.check(`${MODAL} [data-editor-comment-resolve]`);
      await page.fill(`${MODAL} [data-editor-comment-text]`, "Renamed to auth.");
      assert((await modal(page)).save === "Reply", "the button does not read Reply with text in the field");
      await page.keyboard.press("Control+Enter");
      await toast(page, "The reply is posted on merge request !4, the thread is resolved.");
      assert(JSON.stringify(writesAfter(2)) === JSON.stringify(["POST /t3e2e/notes {\"body\":\"Renamed to auth.\"}", "PUT /t3e2e?resolved=true {}"]), `the forge was asked ${JSON.stringify(writesAfter(2))}`);
      await waitFor(async () => {
        const thread = await ofThread("t3e2e");
        return thread?.resolved && JSON.stringify(replyTexts(thread)) === JSON.stringify(["Renamed to auth."]);
      }, "the reply and the resolved thread among the line comments", 30000);
      assert(!(await ofThread("t1e2e")).resolved, "the other thread on the line turned resolved");
    });

    await run("a token without the right to write is refused in GitLab's words and the reply stays in its dialog, a thread the forge does not hold, a general one and a bad id are refused before it is asked", async () => {
      await openSheet(page);
      state.readOnly = true;
      const replies = state.replies.length;
      try {
        await openFromCell(page, "New name bb?");
        await page.fill(`${MODAL} [data-editor-comment-text]`, "no right");
        await page.click(`${MODAL} [data-editor-comment-save]`);
        await toast(page, "The token may not write to GitLab: give it the scope api.");
      } finally {
        state.readOnly = false;
      }
      const kept = await modal(page);
      assert(kept.open && kept.text === "no right" && !kept.readOnly, `after the refusal the dialog reads ${JSON.stringify(kept)}`);
      await page.click(`${MODAL} [data-editor-comment-cancel]`);
      await waitModalGone(page);
      assert(state.replies.length === replies && !(await comments()).some((c) => replyTexts(c).includes("no right")), "the refused reply stands");
      await closeSheet(page);
      const sent = writes().length;
      for (const [fields, code, says] of [
        [{ thread: "t9e2e", body: "x" }, 409, 'The merge request !4 has no such review thread on its diff.'],
        [{ thread: "g1e2e", resolve: "1" }, 409, 'The merge request !4 holds the thread as a general one, only a thread on its diff is answered or resolved here.'],
        [{ thread: "t2e2e", resolve: "1" }, 409, 'The thread is resolved already.'],
        [{ thread: "t1\u202ee2e", body: "x" }, 400, "The id of a review thread holds no spaces, no control characters and no invisible format characters."],
        [{ thread: "t1e2e" }, 400, "Write a reply, or resolve the thread."],
      ]) {
        const r = await post(`/projects/${project}/forge/thread`, fields);
        assert(r.status === code && r.json.error === says, `${JSON.stringify(fields)} answered ${r.status} ${r.text}`);
      }
      assert(writes().length === sent, "a refused write reached the forge");
    });

    await run("a resolve refused after the reply went closes the dialog, the reply is posted once and a second Reply cannot post it again", async () => {
      await openSheet(page);
      const sent = writes().length;
      state.reporter = true;
      try {
        await openFromCell(page, "New name bb?");
        await page.check(`${MODAL} [data-editor-comment-resolve]`);
        await page.fill(`${MODAL} [data-editor-comment-text]`, "posted once");
        await page.click(`${MODAL} [data-editor-comment-save]`);
        await toast(page, "The reply is posted, the thread is not resolved. The token's account may not resolve threads of merge request !4");
        await waitModalGone(page);
      } finally {
        state.reporter = false;
      }
      assert(JSON.stringify(writesAfter(sent)) === JSON.stringify(["POST /t1e2e/notes {\"body\":\"posted once\"}", "PUT /t1e2e?resolved=true {}"]), `the forge was asked ${JSON.stringify(writesAfter(sent))}`);
      await waitFor(async () => replyTexts(await ofThread("t1e2e")).filter((t) => t === "posted once").length === 1, "the reply under the thread", 30000);
      assert(!(await ofThread("t1e2e")).resolved, "the thread turned resolved");
      await closeSheet(page);
    });

    await run("an assistant's reply waits for the approval, the dialog names it all, Approve posts and resolves and the note links the reply", async () => {
      const made = await post("/assistants/new", { form: "new", coder: "claude" });
      assistant = made.json.id || "";
      assert(assistant, `no assistant for the checks: ${made.status}`);
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      const sent = writes().length;
      const answer = await asAssistant(assistant, `/projects/${project}/forge/thread`, { thread: "t1e2e", body: "fixed in abc123", resolve: "1" });
      const what = `Reply and resolve the thread at ${FILE}:4 on merge request !4 of feature in ${project}`;
      assert(answer.status === 200 && answer.json.pending === true && answer.json.what === what, `the socket answered ${answer.status} ${answer.raw}`);
      await page.waitForSelector(`${DIALOG} [data-approval-what]`, { timeout: 15000 });
      assert((await page.textContent(`${DIALOG} [data-approval-what]`)).trim() === what, "the dialog's first line is another");
      const want = [
        ["Asked by", `Assistant · ${assistant.slice(0, 8)}`],
        ["Action", "Reply and resolve the thread"],
        ["Thread", `${FILE}:4 · alice: New name bb?`],
        ["Reply", "fixed in abc123"],
        ["On", `merge request !4 · Fix the login · ${mrPage}`],
        ["Branch", "feature"],
        ["Forge", `GitLab · ${address}`],
        ["Project", project],
      ];
      const rows = await dialogRows();
      assert(JSON.stringify(rows) === JSON.stringify(want), `the dialog's rows read ${JSON.stringify(rows)}`);
      const again = await asAssistant(assistant, `/projects/${project}/forge/thread`, { thread: "t1e2e", body: "again" });
      assert(again.status === 409 && again.json.error === `${what} already waits for the user's approval.`, `a second write on the thread answered ${again.status} ${again.raw}`);
      assert(writes().length === sent, "a waiting write reached the forge");
      await page.click(`${DIALOG} .swal2-confirm`);
      await page.waitForSelector(DIALOG, { state: "detached", timeout: 8000 });
      const note = await approvalNote("done", 1);
      const id = 70 + state.replies.length - 1;
      assert(note.headline === `Approved: ${what}` && note.text.includes(`On GitLab ${address} the review thread on ${FILE}:4 of the merge request !4 "Fix the login" of feature in ${project} holds your reply and is resolved: ${mrPage}#note_${id}.`), `the note reads ${note.headline}: ${note.text}`);
      assert(JSON.stringify(writesAfter(sent)) === JSON.stringify(["POST /t1e2e/notes {\"body\":\"fixed in abc123\"}", "PUT /t1e2e?resolved=true {}"]), `the approval asked ${JSON.stringify(writesAfter(sent))}`);
      await waitFor(async () => {
        const thread = await ofThread("t1e2e");
        return thread?.resolved && JSON.stringify(replyTexts(thread)) === JSON.stringify(["posted once", "fixed in abc123"]);
      }, "the approved reply under the thread", 30000);
    });

    await run("an approval of a thread gone meanwhile fails and writes nothing", async () => {
      const sent = writes().length;
      const answer = await asAssistant(assistant, `/projects/${project}/forge/thread`, { thread: "t1e2e", body: "late" });
      assert(answer.json.pending === true, `the reply did not wait: ${answer.raw}`);
      await page.waitForSelector(`${DIALOG} .swal2-confirm`, { timeout: 15000 });
      gone = true;
      try {
        await page.click(`${DIALOG} .swal2-confirm`);
        await page.waitForSelector(DIALOG, { state: "detached", timeout: 8000 });
        const note = await approvalNote("failed", 1);
        assert(note.text.includes('The merge request !4 has no such review thread on its diff.'), `the note says ${note.text}`);
      } finally {
        gone = false;
      }
      assert(writes().length === sent, "the approval of a thread gone wrote something");
    });

    await run("the Approvals tab lists the Review thread approval, off resolves at once, a resolved thread is refused before anybody is asked, the socket cannot move it", async () => {
      await page.goto(`${BASE}/settings/assistant/approvals`, { waitUntil: "domcontentloaded" });
      const kinds = await page.locator("[data-assistant-approval]").evaluateAll((rows) => rows.map((row) => row.dataset.assistantApproval));
      assert(JSON.stringify(kinds) === JSON.stringify(["compose-actions", "project-delete", "coder-delete", "assistant-delete", "worktree-post-script", "forge-retry", "forge-change", "forge-publish", "forge-thread"]), `the tab lists ${JSON.stringify(kinds)}`);
      const label = (await page.locator('[data-assistant-approval="forge-thread"]').innerText()).replace(/\s+/g, " ").trim();
      assert(label === "Review thread approval A reply or resolve an assistant wants on a forge's review thread waits until you approve.", `the switch reads ${label}`);
      const refused = await asAssistant(assistant, "/settings/assistant/approvals", { "approval-forge-thread": "0" });
      assert(refused.status === 403, `the socket moved the switch: ${refused.status}`);
      const settled = await asAssistant(assistant, `/projects/${project}/forge/thread`, { thread: "t2e2e", resolve: "1" });
      assert(settled.status === 409 && settled.json.error === 'The thread is resolved already.', `a resolve of a resolved thread answered ${settled.status} ${settled.raw}`);
      const off = await post("/settings/assistant/approvals", { "approval-forge-thread": "0" });
      assert(off.status === 200, `switching it off answered ${off.status}`);
      const sent = writes().length;
      const answer = await asAssistant(assistant, `/projects/${project}/forge/thread`, { thread: "t4e2e", resolve: "1" });
      assert(answer.status === 200 && !answer.json.pending && answer.json.written === `On GitLab ${address} the review thread on ${FILE}:5 of the merge request !4 "Fix the login" of feature in ${project} is resolved.`, `with the switch off the socket answered ${answer.raw}`);
      assert(JSON.stringify(writesAfter(sent)) === JSON.stringify(["PUT /t4e2e?resolved=true {}"]), `the resolve asked ${JSON.stringify(writesAfter(sent))}`);
      const on = await post("/settings/assistant/approvals", { "approval-forge-thread": "1" });
      assert(on.status === 200, `switching it on answered ${on.status}`);
    });

    await run("GitHub: a reply goes into the review thread over GraphQL, the switch resolves it there, a reply into a pending review is said", async () => {
      await openEditor(page);
      const saved = await post(hostEdit, { form: "save", kind: "github", web_url: fakeWeb, aliases: alias });
      assert(saved.status === 200, `turning the host into a GitHub answered ${saved.status}`);
      await git(["remote", "set-url", "origin", `${fakeWeb}/octo/app.git`]);
      await waitFor(async () => {
        const list = (await comments()).filter((c) => c.forge);
        return list.length === 3 && list.every((c) => c.forge.kind === "github" && c.forge.thread);
      }, "the notes of the pull request's review threads", 30000);
      await openSheet(page);
      await page.waitForFunction(() => [...document.querySelectorAll("[data-editor-sheet-body] .row .dropdown-item")].some((e) => e.textContent.includes("Added e")), null, { timeout: 10000 });
      const items = await cellMenu(page, "New name bb?");
      assert(items.map((i) => i.label).join("|") === "Go to line|Open thread|Open the thread on GitHub", `the thread's menu reads ${JSON.stringify(items)}`);
      const sent = writes().length;
      await clickItem(page, "Open thread");
      await waitModal(page);
      const m = await modal(page);
      assert(m.hint === "It is posted on pull request #7 on GitHub, for everybody who can read it there." && JSON.stringify(m.notes) === JSON.stringify(["octocat: New name bb?"]), `the dialog reads ${JSON.stringify(m)}`);
      await page.fill(`${MODAL} [data-editor-comment-text]`, "fixed in abc123");
      await page.click(`${MODAL} [data-editor-comment-save]`);
      await toast(page, "The reply is posted on pull request #7.");
      const asked = writes().slice(sent);
      assert(asked.length === 1 && asked[0].path === "/api/graphql" && asked[0].auth === `Bearer ${TOKEN}` && asked[0].mutation === "addPullRequestReviewThreadReply" && asked[0].variables.thread === "PRRT_kwDOe2e1" && asked[0].variables.body === "fixed in abc123" && Object.keys(asked[0].variables).length === 2, `the forge was asked ${JSON.stringify(asked)}`);
      const id = 80 + state.replies.length - 1;
      await waitFor(async () => ((await ofThread("PRRT_kwDOe2e1"))?.replies || []).some((r) => r.id === `PRRC_kwDOe2e${id}` && r.author === "octocat" && r.text === "fixed in abc123"), "the reply under the thread", 30000);
      assert((await ofThread("PRRT_kwDOe2e1")).forge.url === `${prPage}#discussion_r1`, "the thread's link moved to the reply");
      await openFromCell(page, "New name bb?");
      await page.check(`${MODAL} [data-editor-comment-resolve]`);
      await page.click(`${MODAL} [data-editor-comment-save]`);
      await toast(page, "The thread is resolved on pull request #7.");
      assert(JSON.stringify(writesAfter(sent + 1)) === JSON.stringify(['resolveReviewThread {"thread":"PRRT_kwDOe2e1"}']), `the resolve asked ${JSON.stringify(writesAfter(sent + 1))}`);
      await waitFor(async () => (await ofThread("PRRT_kwDOe2e1"))?.resolved, "the resolved thread", 30000);
      state.ghPending = true;
      try {
        await openFromCell(page, "Added e");
        await page.fill(`${MODAL} [data-editor-comment-text]`, "pending");
        await page.click(`${MODAL} [data-editor-comment-save]`);
        await toast(page, "GitHub put the reply on pull request #7 into the token's pending review, it shows once that review is submitted.");
      } finally {
        state.ghPending = false;
      }
      await closeSheet(page);
      await notReloaded(page);
    });

    await run("phone: a thread's dialog with its reply field and Resolve the thread stands within 390px", async () => {
      const phone = await mobilePage();
      await openEditor(phone);
      await openSheet(phone).catch(async () => {
        await phone.click("[data-editor-menu]");
        await phone.locator("[data-editor-comments-item]").click();
        await phone.waitForSelector("[data-editor-sheet]:not([hidden])", { timeout: 6000 });
      });
      await phone.waitForFunction(() => [...document.querySelectorAll("[data-editor-sheet-body] .row .dropdown-item")].some((e) => e.textContent.includes("Added e")), null, { timeout: 15000 });
      await phone.locator("[data-editor-sheet-body] .row .dropdown-item", { hasText: "Added e" }).tap();
      await phone.waitForSelector(".dc-context-menu", { state: "visible", timeout: 6000 });
      await clickItem(phone, "Open thread");
      await waitModal(phone);
      const m = await modal(phone);
      const scroll = await phone.evaluate(() => document.documentElement.scrollWidth);
      assert(m.open && m.field && m.resolve && m.left >= 0 && m.right <= m.inner && m.resolveRight <= m.inner && scroll <= m.inner, `the dialog on the phone: ${JSON.stringify({ ...m, scroll })}`);
      await phone.click(`${MODAL} [data-editor-comment-cancel]`);
      await waitModalGone(phone);
    });

    await run("a guest of a shared link reads no thread id, sees no reply field or switch and reaches no write", async () => {
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
        const list = await comments(guest, base);
        assert(list.some((c) => c.forge) && list.filter((c) => c.forge).every((c) => !("thread" in c.forge)), `the guest reads ${JSON.stringify(list)}`);
        await openSheet(guest);
        await guest.waitForSelector("[data-editor-sheet-body] .row .dropdown-item", { timeout: 15000 });
        await sleep(1500);
        const items = await cellMenu(guest, "Added e");
        assert(items.some((i) => i.label === "Open thread") && !items.some((i) => /^(Reply|Resolve)/.test(i.label)), `the guest's menu reads ${JSON.stringify(items)}`);
        await clickItem(guest, "Open thread");
        await waitModal(guest);
        const m = await modal(guest);
        assert(m.open && !m.field && !m.save && !m.resolve && !m.hint && m.notes.length === 1, `the guest's dialog reads ${JSON.stringify(m)}`);
        await guest.click(`${MODAL} [data-editor-comment-cancel]`);
        await waitModalGone(guest);
        const sent = writes().length;
        const code = await guest.evaluate(async (target) => (await fetch(target, { method: "POST", headers: { "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]')?.content || "", "Content-Type": "application/x-www-form-urlencoded" }, body: "thread=PRRT_kwDOe2e2&resolve=1" })).status, `${base}/projects/${project}/forge/thread`);
        assert(code === 404, `a guest's write answered ${code}`);
        assert(writes().length === sent, "a guest's write reached the forge");
      } finally {
        await ctx.close();
      }
    });

    await run("docs: the Forges topic explains the reply and the resolve, Tokens, Approvals and Line comments name them", async () => {
      await page.goto(`${BASE}/docs`, { waitUntil: "domcontentloaded" });
      const item = page.locator("section#forges .list-group-item", { hasText: /^\s*Answer and resolve review threads/ });
      assert(await item.count() === 1, "no docs entry for the reply and the resolve");
      const text = await item.innerText();
      for (const said of ["Open thread", "offers a reply", "Resolve the thread", "after the reply or alone", "A resolved thread reopens on the forge, not here", "the Developer role", "once that review is submitted", "forge-thread-reply", "forge-thread-resolve", "only once you approve"]) {
        assert(text.includes(said), `the entry lacks "${said}": ${text}`);
      }
      const tokens = await page.locator("section#forges .list-group-item", { hasText: "Tokens and the connection test" }).innerText();
      assert(tokens.includes("writing api") && tokens.includes("Pull requests read and write"), `the tokens' entry reads ${tokens}`);
      const reads = await page.locator("section#forges .list-group-item", { hasText: "For the assistants" }).innerText();
      assert(reads.includes("the id a reply takes"), `the assistants' entry reads ${reads}`);
      const approvals = await page.locator("section#assistants .list-group-item", { hasText: /^\s*Approvals/ }).innerText();
      assert(approvals.includes("a reply on or resolve of a review thread") && approvals.includes("comments or thread"), `the approvals' entry reads ${approvals}`);
      const lines = await page.locator("section#editor .list-group-item", { hasText: "Line comments" }).first().innerText();
      assert(lines.includes("The forge's review threads stand among them, see Forges"), `the line comments' entry reads ${lines}`);
    });
  } finally {
    if (linkId) await post(`/settings/sharing/${linkId}`, { form: "revoke" }).catch(() => {});
    await post("/settings/assistant/approvals", { "approval-forge-thread": "1" }).catch(() => {});
    if (hostEdit) await post(hostEdit, { form: "delete" }).catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
    if (assistant) await post(`/assistants/${assistant}`, { form: "delete" }).catch(() => {});
  }
});
