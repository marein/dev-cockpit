const http = require("http");
const L = require("./lib");
const { assert, BASE, sleep } = L;

// Forge change request: a pushed branch opens a merge or pull request on the
// forge (GET and POST /projects/:name/forge/change, internal/forge
// change.go, forge_change.gohtml in dc-form-modal). The editor's forge sheet
// offers New merge request once the branch is pushed and has none open, never
// on the default branch the forge names; the form is prefilled from the
// commits the pushed head carries since it, a draft into it, and opens what
// was edited, GitLab's draft by the mark its title opens with. The page form
// counts each line break of the description once, as its text box does,
// though the browser sends two characters. A branch with an open one shows
// that one in place of the form and opens no second one. A
// token without the right to write is refused in a sentence over what was
// typed. An assistant's open over the local socket waits for the user's
// approval (internal/approval, the Change request approval under Settings ›
// Assistants › Approvals): the app-wide dialog names the title, the
// description, the branch and where it merges into, Approve opens it and the
// note in the assistant's thread links it; asked again while one stands open,
// the open one answers; with the switch off it opens at once. On a GitHub
// Enterprise Server New pull request opens a draft by GitHub's own field. A
// guest of a shared link reaches none of it.
//
// The forge is the runner's own fake (forge-fake.js) on FORGE_PORT (default
// 3157), published on the host's loopback, where the instance reaches it at
// FORGE_ADDR (default 127.0.0.1). The instance MUST be the assistant one
// (tests/e2e/fakes ahead of the real CLIs on PATH, a scratch HOME), with its
// local API socket mounted (APISOCK, default /apisock/s), because nothing but
// the socket may act as an assistant. A run takes about two minutes.

const { startFakeForge, addHost } = require("./forge-fake");

const FAKE_PORT = Number(process.env.FORGE_PORT || 3157);
const FAKE_TLS_PORT = Number(process.env.FORGE_TLS_PORT || 3158);
const FAKE_ADDR = process.env.FORGE_ADDR || "127.0.0.1";
const APISOCK = process.env.APISOCK || "/apisock/s";
const TOKEN = `glpat-e2e${Date.now().toString(36)}Ch4nG3sT9uV4`;
const MODAL = "[data-form-modal].show";
const DIALOG = ".swal2-popup:not(.swal2-toast)";
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

L.runFeature("FORGE_CHANGE", async ({ browser, page, run, mobilePage, bag }) => {
  const stamp = Date.now().toString(36);
  const project = `zzforgechange-${stamp}`;
  const alias = `fc-${stamp}`;
  const linkName = `forge change ${stamp}`;
  const fakeWeb = `http://${FAKE_ADDR}:${FAKE_PORT}`;
  const editor = `${BASE}/projects/${project}/editor`;
  const address = `${FAKE_ADDR}:${FAKE_PORT}/gl/team/sub/app`;
  fakeForge ||= await startFakeForge({ port: FAKE_PORT, tlsPort: FAKE_TLS_PORT, token: TOKEN });
  const state = fakeForge.state;
  let path = "";
  let hostEdit = "";
  let pushed = "";
  let assistant = "";
  let linkId = "";

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

  // post sends a form the way a page does, the JSON or the page back.
  const post = (target, fields, accept = "application/json") => page.evaluate(async ({ target, fields, accept }) => {
    const body = new URLSearchParams([...fields, ["csrf_token", document.querySelector('meta[name="csrf-token"]').content]]);
    const r = await fetch(target, { method: "POST", body, headers: { Accept: accept } });
    const text = await r.text();
    let json = {};
    try { json = JSON.parse(text); } catch {}
    return { status: r.status, url: r.url, text, json };
  }, { target, fields: Array.isArray(fields) ? fields : Object.entries(fields), accept });

  // refresh has the forge read now, which the poller keeps and signals.
  const refresh = () => page.evaluate(async (p) => (await fetch(`/projects/${p}/forge/status?fresh=1`, { headers: { Accept: "application/json" } })).status, project);
  const writes = () => fakeForge.log.filter((r) => r.method === "POST" && /\/(merge_requests|pulls)$/.test(r.path));
  const shown = (p, selector) => p.evaluate((sel) => [...document.querySelectorAll(sel)].filter((el) => {
    const box = el.getBoundingClientRect();
    const style = getComputedStyle(el);
    return box.width > 0 && box.height > 0 && style.visibility !== "hidden" && style.display !== "none";
  }).length, selector);
  // offerRow reads the New merge request row the sheet shows, null for none.
  const offerRow = (p) => p.evaluate(() => {
    const row = [...document.querySelectorAll("[data-editor-sheet-body] [data-forge-change-new]")].find((r) => r.getBoundingClientRect().height > 0);
    if (!row) return null;
    const box = row.getBoundingClientRect();
    return {
      label: row.querySelector(":scope > .d-flex > span:first-child").textContent.trim(),
      sub: row.querySelector(":scope > .d-flex > .small").textContent.trim(),
      href: row.getAttribute("href"),
      left: box.left, right: box.right, inner: window.innerWidth,
    };
  });
  const toast = async (p, text) => {
    await p.waitForFunction((t) => [...document.querySelectorAll(".dc-toast")].some((el) => el.textContent.includes(t)), text, { timeout: 10000 });
  };
  const titled = async (p, title) => {
    try {
      await p.waitForFunction((title) => document.querySelector("[data-editor-sheet-title]")?.textContent.trim() === title, title, { timeout: 10000 });
    } catch {
      throw new Error(`the sheet is titled ${JSON.stringify(await p.evaluate(() => document.querySelector("[data-editor-sheet-title]")?.textContent.trim()))}, not ${JSON.stringify(title)}`);
    }
  };
  const openEditor = async (p) => {
    await p.goto(editor, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(p);
    await p.waitForSelector("[data-editor-git-status]:not([hidden])", { timeout: 20000 });
    const backdrop = p.locator("[data-editor-backdrop]");
    if (await backdrop.isVisible()) {
      await backdrop.click({ position: { x: 370, y: 300 } });
      await p.waitForSelector("[data-editor-backdrop]", { state: "hidden", timeout: 5000 });
    }
  };
  const openGit = async (p) => {
    await p.click("[data-editor-git-status]");
    await titled(p, "Git");
  };
  const openForge = async (p, title = "GitLab") => {
    await p.waitForSelector("[data-editor-forge-status]:not([hidden])", { timeout: 20000 });
    await p.click("[data-editor-forge-status]");
    await titled(p, title);
  };
  const offered = (p, timeout = 20000) => p.waitForFunction(() => [...document.querySelectorAll("[data-editor-sheet-body] [data-forge-change-new]")].some((r) => r.getBoundingClientRect().height > 0), null, { timeout });
  const formFields = (p) => p.evaluate(() => {
    const form = document.querySelector("[data-form-modal].show [data-forge-change-form]");
    if (!form) return null;
    return {
      heading: document.querySelector("[data-form-modal-title]")?.textContent.trim(),
      title: form.querySelector("[data-forge-change-title]").value,
      body: form.querySelector("[data-forge-change-body]").value,
      target: form.querySelector("[data-forge-change-target]").value,
      draft: form.querySelector("[data-forge-change-draft]").checked,
      carried: form.querySelector("[data-forge-change-carried]").textContent.trim(),
      from: form.querySelector("[data-forge-change-from]").textContent.replace(/\s+/g, " ").trim(),
      local: !!form.querySelector("[data-forge-change-local]"),
      send: form.querySelector("[data-forge-change-open]").textContent.trim(),
    };
  });
  const openForm = async (p) => {
    await p.click("[data-editor-sheet-body] [data-forge-change-new]");
    await p.waitForSelector(`${MODAL} [data-forge-change-form]`, { state: "visible", timeout: 10000 });
  };
  const closeModal = async (p) => {
    await p.click(`${MODAL} [data-bs-dismiss="modal"]`);
    await p.waitForFunction(() => !document.querySelector("[data-form-modal].show"), null, { timeout: 8000 });
  };
  const dialogRows = () => page.locator(`${DIALOG} dl[data-approval-details]`).evaluate((list) => {
    const labels = [...list.querySelectorAll("dt")].map((dt) => dt.textContent.trim());
    return [...list.querySelectorAll("dd")].map((dd, i) => [labels[i], dd.textContent]);
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
    await run("setup: a repository on the fake GitLab whose branch login carries two commits past main, main pushed", async () => {
      await L.createProject(page, project);
      path = await L.projectPath(page, project);
      await git(["init", "-q", "-b", "main"]);
      for (const [key, value] of [["user.email", "e2e@example.com"], ["user.name", "e2e"], ["commit.gpgsign", "false"]]) await git(["config", key, value]);
      await git(["commit", "-q", "--allow-empty", "-m", "first"]);
      await git(["update-ref", "refs/remotes/origin/main", "HEAD"]);
      await git(["switch", "-q", "-c", "login"]);
      await git(["commit", "-q", "--allow-empty", "-m", "Add the login <form>", "-m", "It checks the name."]);
      await git(["commit", "-q", "--allow-empty", "-m", "Fix the login"]);
      pushed = await git(["rev-parse", "HEAD"]);
      await git(["remote", "add", "origin", `${fakeWeb}/gl/team/sub/app.git`]);
      Object.assign(state, { head: pushed, defaultBranch: "main", readOnly: false, changes: [], pulls: [] });
      fakeForge.log.splice(0);
      await page.goto(`${BASE}/settings/forges`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      const added = await addHost(page, { kind: "gitlab", web_url: `${fakeWeb}/gl`, aliases: alias, token: TOKEN });
      assert(!added.failed, added.failed);
      await page.goto(`${BASE}/settings/forges`, { waitUntil: "domcontentloaded" });
      hostEdit = await page.evaluate((mark) => [...document.querySelectorAll("[data-forge-host]")]
        .find((r) => r.textContent.includes(mark))?.querySelector("[data-forge-edit]")?.getAttribute("href") || "", alias);
      assert(hostEdit, "the added host has no Edit on the list");
    });

    await run("the forge sheet offers New merge request once the branch is pushed, without a reload", async () => {
      await openEditor(page);
      await sleep(1500);
      assert(await shown(page, "[data-editor-forge-status]") === 0, "a branch not pushed offers a merge request");
      await page.evaluate(() => { window.__forgeChangeMark = 1; });
      await git(["update-ref", "refs/remotes/origin/login", pushed]);
      await page.waitForFunction(() => document.querySelector("[data-editor-forge-status]")?.getBoundingClientRect().height > 0, null, { timeout: 30000 });
      const lead = await page.getAttribute("[data-editor-forge-status]", "title");
      assert(lead === "GitLab: New merge request", `the forge's button reads ${lead}`);
      await page.click("[data-editor-forge-status]");
      await titled(page, "GitLab");
      await offered(page);
      const r = await offerRow(page);
      assert(r.label === "New merge request" && r.sub === "login into main" && r.href === `/projects/${project}/forge/change?branch=login`, `the row reads ${JSON.stringify(r)}`);
      assert(await page.evaluate(() => window.__forgeChangeMark === 1), "the page reloaded");
      assert(await shown(page, "[data-editor-sheet-body] [data-forge-head]") === 0, "a branch without change request or pipeline shows a badge");
    });

    await run("the default branch offers none", async () => {
      await git(["switch", "-q", "main"]);
      await sleep(6000);
      assert(await refresh() === 200, "main could not be read");
      await openEditor(page);
      await sleep(1500);
      assert(await shown(page, "[data-editor-forge-status]") === 0, "the default branch offers a merge request");
      await git(["switch", "-q", "login"]);
      await openEditor(page);
      await openForge(page);
      await offered(page);
    });

    await run("the row opens the form in the dialog, a draft into main made of the commits login carries", async () => {
      await openForm(page);
      const f = await formFields(page);
      const want = {
        heading: "New merge request", title: "Add the login <form>", body: "* Add the login <form>\n* Fix the login", target: "main", draft: true,
        carried: "Made of the 2 commits login carries since main.", from: `From login at ${pushed.slice(0, 12)} on GitLab · ${address}`, local: false, send: "Open merge request",
      };
      assert(JSON.stringify(f) === JSON.stringify(want), `the form reads ${JSON.stringify(f)}`);
      if (await page.evaluate(() => matchMedia("(hover: hover) and (pointer: fine)").matches)) {
        await page.waitForFunction(() => document.activeElement?.matches("[data-forge-change-title]"), null, { timeout: 5000 }).catch(() => {
          throw new Error("the title has no focus on a fine pointer");
        });
      }
      await closeModal(page);
      assert(writes().length === 0, "opening the form wrote to the forge");
    });

    await run("a branch that carries 105 commits is titled by its oldest one, lists every subject and counts them all", async () => {
      const tree = await git(["rev-parse", "main^{tree}"]);
      let tip = await git(["rev-parse", "main"]);
      const subjects = [];
      for (let i = 1; i <= 105; i++) {
        tip = await git(["commit-tree", tree, "-p", tip, "-m", `commit number ${i}`]);
        subjects.push(`* commit number ${i}`);
      }
      await git(["update-ref", "refs/remotes/origin/many", tip]);
      const f = await page.evaluate(async (target) => {
        const form = new DOMParser().parseFromString(await (await fetch(target)).text(), "text/html");
        return {
          title: form.querySelector("[data-forge-change-title]")?.value,
          body: form.querySelector("[data-forge-change-body]")?.value,
          carried: form.querySelector("[data-forge-change-carried]")?.textContent.trim(),
        };
      }, `/projects/${project}/forge/change?branch=many`);
      assert(f.title === "commit number 1", `the title reads ${JSON.stringify(f.title)}`);
      assert(f.body === subjects.join("\n"), `the body lists ${f.body?.split("\n").length} lines, from ${JSON.stringify(f.body?.split("\n")[0])}`);
      assert(f.carried === "Made of the 105 commits many carries since main.", `the hint reads ${JSON.stringify(f.carried)}`);
      await git(["update-ref", "-d", "refs/remotes/origin/many"]);
      assert(writes().length === 0, "opening the form wrote to the forge");
    });

    await run("sent as edited it opens the draft, the dialog closes and the sheet shows it without a reload", async () => {
      await page.evaluate(() => { window.__forgeChangeMark = 2; });
      await openForm(page);
      await page.fill(`${MODAL} [data-forge-change-title]`, "Fix the login form");
      await page.fill(`${MODAL} [data-forge-change-body]`, "Two fixes.\nBoth small.");
      await page.click(`${MODAL} [data-forge-change-open]`);
      await toast(page, "The merge request is open.");
      await page.waitForFunction(() => !document.querySelector("[data-form-modal].show"), null, { timeout: 8000 });
      const sent = writes();
      assert(sent.length === 1 && sent[0].auth === `Bearer ${TOKEN}` && sent[0].query === "", `the writes went as ${JSON.stringify(sent)}`);
      assert(JSON.stringify(sent[0].body) === JSON.stringify({ description: "Two fixes.\nBoth small.", source_branch: "login", target_branch: "main", title: "Draft: Fix the login form" }), `the forge was sent ${JSON.stringify(sent[0].body)}`);
      await page.waitForFunction(() => document.querySelector("[data-editor-sheet-body] [data-forge-head] [data-forge-change]")?.textContent.trim() === "!20", null, { timeout: 15000 });
      assert(await offerRow(page) === null, "the branch with an open merge request still offers one");
      assert(await page.evaluate(() => window.__forgeChangeMark === 2), "the page reloaded");
    });

    await run("a branch with an open merge request shows that one in place of the form and opens no second one", async () => {
      const before = writes().length;
      await page.goto(`${BASE}/projects/${project}/forge/change`, { waitUntil: "domcontentloaded" });
      const exists = page.locator("[data-forge-change-exists]");
      assert(await exists.isVisible(), "the page of a branch with an open one shows no notice of it");
      assert(await page.title() === "New merge request - Dev Cockpit", `the page is titled ${await page.title()}`);
      const link = page.locator("[data-forge-change-link]");
      assert(await link.getAttribute("href") === "https://gitlab.example.com/team/sub/app/-/merge_requests/20" && (await link.innerText()).trim() === "!20 Draft: Fix the login form", `the link reads ${await link.innerText()}`);
      assert(await page.locator("[data-forge-change-form]").count() === 0, "the page still holds the form");
      const again = await post(`/projects/${project}/forge/change?modal=1`, { branch: "login", title: "Again", target: "main", draft: "1" });
      assert(again.status === 200 && again.text.includes("data-forge-change-exists") && !again.text.includes("<form"), `a second open answered ${again.status} ${again.text.slice(0, 200)}`);
      assert(writes().length === before, "a second open reached the forge");
    });

    await run("a token without the right to write is refused in a sentence over what was typed", async () => {
      state.changes = [];
      state.readOnly = true;
      await refresh();
      await openEditor(page);
      await openForge(page);
      await offered(page);
      await openForm(page);
      await page.fill(`${MODAL} [data-forge-change-title]`, "Kept title");
      await page.click(`${MODAL} [data-forge-change-open]`);
      await page.waitForSelector(`${MODAL} [data-form-modal-error]`, { state: "visible", timeout: 10000 });
      const said = (await page.textContent(`${MODAL} [data-form-modal-error]`)).trim();
      assert(said === "The token may not write to GitLab: give it the scope api.", `the dialog says ${said}`);
      assert(await page.inputValue(`${MODAL} [data-forge-change-title]`) === "Kept title", "the typed title is gone");
      assert(state.changes.length === 0, "a refused open opened something");
      await closeModal(page);
      state.readOnly = false;
    });

    await run("the page is titled New merge request, the local branch says how it stands to the pushed head in one block of text", async () => {
      const form = `${BASE}/projects/${project}/forge/change`;
      const before = writes().length;
      const title = "New merge request - Dev Cockpit";
      const notice = (p) => p.evaluate(() => {
        const el = document.querySelector("[data-forge-change-local]");
        if (!el) return null;
        const text = el.firstElementChild;
        const code = el.querySelector("code").getBoundingClientRect();
        return {
          level: [...el.classList].filter((c) => /^alert-(warning|info)$/.test(c)).join(" "),
          text: el.textContent.replace(/\s+/g, " ").trim(),
          children: el.children.length,
          codeHeight: code.height,
          lineHeight: parseFloat(getComputedStyle(text).lineHeight),
          right: el.getBoundingClientRect().right,
          inner: window.innerWidth,
          scroll: document.documentElement.scrollWidth,
          title: document.title,
        };
      });
      const stands = async (p, level, says) => {
        await p.goto(form, { waitUntil: "domcontentloaded" });
        const n = await notice(p);
        const local = (await git(["rev-parse", "HEAD"])).slice(0, 12);
        const want = `The local branch stands at ${local}, ${says}`;
        assert(n && n.level === `alert-${level}` && n.text === want && n.title === title, `the page reads ${JSON.stringify(n)}, want ${level} ${want}`);
        assert(n.children === 1 && n.codeHeight < n.lineHeight * 1.5 && n.right <= n.inner && n.scroll <= n.inner, `the notice breaks apart: ${JSON.stringify(n)}`);
      };
      try {
        await page.goto(form, { waitUntil: "domcontentloaded" });
        assert(await page.title() === title && await page.locator("[data-forge-change-local]").count() === 0, `the page of a pushed branch reads ${await page.title()}`);
        await git(["commit", "-q", "--allow-empty", "-m", "Not pushed"]);
        await stands(page, "warning", "1 commit ahead of the pushed head: push first to carry it.");
        await stands(await mobilePage(), "warning", "1 commit ahead of the pushed head: push first to carry it.");
        await git(["reset", "-q", "--hard", "HEAD~3"]);
        await stands(page, "info", "2 commits behind the pushed head, which this merge request carries.");
        await git(["commit", "-q", "--allow-empty", "-m", "Apart"]);
        await stands(page, "warning", "1 commit ahead of the pushed head and 2 behind it: this merge request lacks it.");
        await page.fill("[data-forge-change-title]", "Typed title");
        await page.fill("[data-forge-change-target]", "-x");
        await page.click("[data-forge-change-open]");
        await page.waitForFunction(() => document.querySelector(".dc-notice")?.textContent.includes("A branch name must not start with a dash."), null, { timeout: 10000 });
        assert(await page.title() === title && await page.inputValue("[data-forge-change-title]") === "Typed title", `the refused page reads ${await page.title()}`);
        assert(writes().length === before, "a refused open reached the forge");
      } finally {
        await git(["reset", "-q", "--hard", pushed]);
      }
    });

    await run("the page form opens a description at its bound, each line break counted once", async () => {
      const body = `${"x".repeat(63)}\n`.repeat(1023) + "x".repeat(64);
      const before = writes().length;
      try {
        await page.goto(`${BASE}/projects/${project}/forge/change`, { waitUntil: "domcontentloaded" });
        await page.fill("[data-forge-change-title]", "Long description");
        await page.fill("[data-forge-change-body]", body);
        assert(await page.inputValue("[data-forge-change-body]") === body, "the text box does not hold the whole description");
        await page.click("[data-forge-change-open]");
        await waitFor(() => writes().length > before, "the open", 15000).catch(async () => {
          throw new Error(`nothing opened, the page says ${JSON.stringify(await page.evaluate(() => document.querySelector(".dc-notice")?.textContent.trim()))}`);
        });
        const sent = writes().slice(before);
        assert(sent.length === 1 && sent[0].body.description === body, `the forge was sent ${sent.length} opens, a description of ${sent[0]?.body.description?.length} characters`);
        await page.waitForURL((u) => u.pathname === "/projects", { timeout: 10000 });
      } finally {
        state.changes = [];
        await refresh();
      }
    });

    await run("an assistant's open waits for the approval, the dialog names it all, Approve opens it and the note links it", async () => {
      const made = await post("/assistants/new", { form: "new", coder: "claude" });
      assistant = made.json.id || "";
      assert(assistant, `no assistant for the checks: ${made.status}`);
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      const answer = await asAssistant(assistant, `/projects/${project}/forge/change`, { draft: "1" });
      const what = `Open draft merge request of login into main in ${project}`;
      assert(answer.status === 200 && answer.json.pending === true && answer.json.what === what, `the socket answered ${answer.status} ${answer.raw}`);
      await page.waitForSelector(`${DIALOG} [data-approval-what]`, { timeout: 15000 });
      assert((await page.textContent(`${DIALOG} [data-approval-what]`)).trim() === what, "the dialog's first line is another");
      const rows = await dialogRows();
      const want = [
        ["Asked by", `Assistant · ${assistant.slice(0, 8)}`],
        ["Action", "Open draft merge request"],
        ["Title", "Add the login <form>"],
        ["Description", "* Add the login <form>\n* Fix the login"],
        ["Branch", `login at ${pushed.slice(0, 12)}`],
        ["Into", "main"],
        ["Forge", `GitLab · ${address}`],
        ["Project", project],
      ];
      assert(JSON.stringify(rows) === JSON.stringify(want), `the dialog's rows read ${JSON.stringify(rows)}`);
      assert(state.changes.length === 0, "a waiting open opened something");
      const again = await asAssistant(assistant, `/projects/${project}/forge/change`, { title: "Other" });
      assert(again.status === 409 && again.json.error === `${what} already waits for the user's approval.`, `a second open answered ${again.status} ${again.raw}`);
      await page.click(`${DIALOG} .swal2-confirm`);
      await page.waitForSelector(DIALOG, { state: "detached", timeout: 8000 });
      const note = await approvalNote("done", 1);
      assert(note.headline === `Approved: ${what}`, `the note reads ${note.headline}`);
      assert(note.text.includes(`On GitLab ${address} the draft merge request !20 "Draft: Add the login <form>" of login into main in ${project} is open: https://gitlab.example.com/team/sub/app/-/merge_requests/20.`), `the note says ${note.text}`);
      assert(state.changes.length === 1 && state.changes[0].title === "Draft: Add the login <form>" && state.changes[0].description === "* Add the login <form>\n* Fix the login", `the approval opened ${JSON.stringify(state.changes)}`);
    });

    await run("asked again while one stands open, the open one answers and nobody is asked", async () => {
      const answer = await asAssistant(assistant, `/projects/${project}/forge/change`, { draft: "1" });
      assert(answer.status === 200 && answer.json.existing === `On GitLab ${address} login of ${project} has the open draft merge request !20 "Draft: Add the login <form>" into main already: https://gitlab.example.com/team/sub/app/-/merge_requests/20.`, `the socket answered ${answer.raw}`);
      await sleep(1000);
      assert(await page.locator(DIALOG).count() === 0, "the user was asked about an open one");
      assert(state.changes.length === 1, "a second one opened");
    });

    await run("the Approvals tab lists the Change request approval, off opens at once as named, the socket cannot move it", async () => {
      await page.goto(`${BASE}/settings/assistant/approvals`, { waitUntil: "domcontentloaded" });
      const kinds = await page.locator("[data-assistant-approval]").evaluateAll((rows) => rows.map((row) => row.dataset.assistantApproval));
      assert(JSON.stringify(kinds) === JSON.stringify(["compose-actions", "project-delete", "coder-delete", "assistant-delete", "worktree-post-script", "forge-retry", "forge-change", "forge-publish", "forge-thread"]), `the tab lists ${JSON.stringify(kinds)}`);
      const label = (await page.locator('[data-assistant-approval="forge-change"]').innerText()).replace(/\s+/g, " ").trim();
      assert(label === "Change request approval A merge or pull request an assistant wants to open stays unopened until you approve.", `the switch reads ${label}`);
      const refused = await asAssistant(assistant, "/settings/assistant/approvals", { "approval-forge-change": "0" });
      assert(refused.status === 403, `the socket moved the switch: ${refused.status}`);
      const off = await post("/settings/assistant/approvals", { "approval-forge-change": "0" });
      assert(off.status === 200, `switching it off answered ${off.status}`);
      state.changes = [];
      await refresh();
      const answer = await asAssistant(assistant, `/projects/${project}/forge/change`, { title: "Ship the login", body: "", target: "main" });
      assert(answer.status === 200 && !answer.json.pending && answer.json.opened === `On GitLab ${address} the merge request !20 "Ship the login" of login into main in ${project} is open: https://gitlab.example.com/team/sub/app/-/merge_requests/20.`, `with the switch off the socket answered ${answer.raw}`);
      assert(state.changes.length === 1 && state.changes[0].title === "Ship the login" && state.changes[0].description === null, `the open sent ${JSON.stringify(state.changes)}`);
      const on = await post("/settings/assistant/approvals", { "approval-forge-change": "1" });
      assert(on.status === 200, `switching it on answered ${on.status}`);
    });

    await run("GitHub: New pull request opens a draft by GitHub's own field", async () => {
      state.changes = [];
      const saved = await post(hostEdit, { form: "save", kind: "github", web_url: fakeWeb, aliases: alias });
      assert(saved.status === 200, `turning the host into GitHub answered ${saved.status}`);
      await git(["remote", "set-url", "origin", `${fakeWeb}/octo/app.git`]);
      await refresh();
      await openEditor(page);
      await openForge(page, "GitHub");
      await offered(page);
      const r = await offerRow(page);
      assert(r.label === "New pull request" && r.sub === "login into main", `the row reads ${JSON.stringify(r)}`);
      await openForm(page);
      const f = await formFields(page);
      assert(f.heading === "New pull request" && f.title === "Add the login <form>" && f.draft && f.send === "Open pull request" && f.from === `From login at ${pushed.slice(0, 12)} on GitHub · ${FAKE_ADDR}:${FAKE_PORT}/octo/app`, `the form reads ${JSON.stringify(f)}`);
      await page.click(`${MODAL} [data-forge-change-open]`);
      await toast(page, "The pull request is open.");
      const pull = state.pulls[0];
      assert(state.pulls.length === 1 && pull.title === "Add the login <form>" && pull.draft === true && pull.head.label === "octo:login" && pull.base.ref === "main", `the forge opened ${JSON.stringify(state.pulls)}`);
      await page.waitForFunction(() => document.querySelector("[data-editor-sheet-body] [data-forge-head] [data-forge-change]")?.textContent.trim() === "#30", null, { timeout: 15000 });
    });

    await run("phone: the row and the form stand within 390px", async () => {
      state.pulls = [];
      await refresh();
      const phone = await mobilePage();
      await openEditor(phone);
      await openForge(phone, "GitHub");
      await offered(phone);
      const r = await offerRow(phone);
      const scroll = await phone.evaluate(() => document.documentElement.scrollWidth);
      assert(r.left >= 0 && r.right <= r.inner && scroll <= r.inner, `the row stands at ${JSON.stringify(r)}, the page scrolls ${scroll}`);
      await phone.tap("[data-editor-sheet-body] [data-forge-change-new]");
      await phone.waitForSelector(`${MODAL} [data-forge-change-form]`, { state: "visible", timeout: 10000 });
      const box = await phone.evaluate(() => {
        const dialog = document.querySelector("[data-form-modal].show .modal-content").getBoundingClientRect();
        return { left: dialog.left, right: dialog.right, inner: window.innerWidth, page: document.documentElement.scrollWidth, focused: document.activeElement?.matches("[data-forge-change-title]") };
      });
      assert(box.left >= 0 && box.right <= box.inner && box.page <= box.inner, `the dialog stands at ${JSON.stringify(box)}`);
      assert(!box.focused, "a touch keyboard jumps up over the dialog");
      await closeModal(phone);
    });

    await run("a guest of a shared link sees no New merge request and reaches no form", async () => {
      await page.goto(`${BASE}/settings/sharing`, { waitUntil: "domcontentloaded" });
      const made = await post("/settings/sharing/new", [["amount", "1"], ["unit", "hours"], ["name", linkName], ["project", project]]);
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
        await guest.goto(`${new URL(link).origin}${base}/projects/${project}/editor`, { waitUntil: "domcontentloaded" });
        await guest.waitForSelector("[data-editor-git-status]:not([hidden])", { timeout: 20000 });
        await openGit(guest);
        await sleep(1500);
        assert(await guest.locator("[data-forge-change-new]").count() === 0, "the guest's sheet offers a change request");
        const before = writes().length;
        const codes = await guest.evaluate(async (target) => {
          const token = document.querySelector('meta[name="csrf-token"]')?.content || "";
          const get = (await fetch(`${target}?modal=1`)).status;
          const send = (await fetch(target, { method: "POST", headers: { "X-CSRF-Token": token, "Content-Type": "application/x-www-form-urlencoded" }, body: "title=x&draft=1" })).status;
          return { get, send };
        }, `${base}/projects/${project}/forge/change`);
        assert(codes.get === 404 && codes.send === 404, `a guest's form answered ${JSON.stringify(codes)}`);
        assert(writes().length === before, "a guest's open reached the forge");
      } finally {
        await ctx.close();
      }
    });

    await run("docs: the Forges topic explains the open of a change request, Tokens and Approvals name it", async () => {
      await page.goto(`${BASE}/docs`, { waitUntil: "domcontentloaded" });
      const item = page.locator("section#forges .list-group-item", { hasText: /^\s*Open a change request/ });
      assert(await item.count() === 1, "no docs entry for the open of a change request");
      const text = await item.innerText();
      for (const said of ["New merge request", "New pull request", "other than the default branch", "the commits the pushed head carries since the default branch", "opens as a draft", "the form says when the local branch stands elsewhere", "forge-change-new", "only once you approve"]) {
        assert(text.includes(said), `the entry lacks "${said}": ${text}`);
      }
      const tokens = await page.locator("section#forges .list-group-item", { hasText: "Tokens and the connection test" }).innerText();
      assert(tokens.includes("writing api") && tokens.includes("Pull requests read and write"), `the tokens' entry reads ${tokens}`);
      const approvals = await page.locator("section#assistants .list-group-item", { hasText: /^\s*Approvals/ }).innerText();
      assert(approvals.includes("a new change request") && approvals.includes("change request"), `the approvals' entry reads ${approvals}`);
    });
  } finally {
    if (linkId) await post(`/settings/sharing/${linkId}`, { form: "revoke" }).catch(() => {});
    await post("/settings/assistant/approvals", { "approval-forge-change": "1" }).catch(() => {});
    if (hostEdit) await post(hostEdit, { form: "delete" }).catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
    if (assistant) await post(`/assistants/${assistant}`, { form: "delete" }).catch(() => {});
  }
});
