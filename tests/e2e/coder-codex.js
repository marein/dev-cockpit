#!/usr/bin/env node
// Coder coverage: codex as a coder from start to stop, against the fake codex
// of tests/e2e/fakes, which keeps sessions the way codex 0.172.0 does and
// prints what it was started with. Chromium only.
//
// What is special about codex is when a session gets its identity: the CLI
// mints the id itself and writes the session's record with the first message,
// never before. A start with a task therefore lands on codex's id at once,
// tied to the record through the process that writes it. A start without a
// task, the only one the form makes, keeps the key the cockpit minted until
// the first message: from then on the coder is listed under codex's id, the
// page that is open keeps working under the key it was opened with and a
// reload lands on the id. The name rides no flag, it is written into the
// record through codex's own app server once the record exists.
//
// The folder trust rides the command line (-c projects={...}), automatic
// approval is --dangerously-bypass-approvals-and-sandbox, the task stands
// behind `--`. Needs an instance with tests/e2e/fakes first on PATH and a
// scratch HOME and CODEX_HOME, see the README.
const { chromium } = require("playwright-core");
const L = require("./lib");
const { assert, sleep } = L;

const BASE = process.env.BASE_URL || "https://localhost:3010";

(async () => {
  const browser = await chromium.launch({ args: ["--no-sandbox"] });
  const bag = { consoleErrors: [], pageErrors: [] };
  const { results, run } = L.makeRunner();
  const tag = `cx-${Date.now().toString(36)}`;
  const project = `zzcx-${tag}`;
  const ctx = await L.newDesktop(browser, "chromium");
  const page = await ctx.newPage();
  L.wirePage(page, bag);
  const phoneCtx = await L.newMobile(browser);
  const phone = await phoneCtx.newPage();
  L.wirePage(phone, bag);

  const mirrorOf = (p) => p.evaluate(() => (document.querySelector(".attach-selection") || {}).textContent || "");
  const flat = (text) => text.replace(/\s+/g, "");
  const idOf = (url) => new URL(url).pathname.split("/").pop();
  const waitFor = async (probe, what, tries = 40) => {
    for (let i = 0; i < tries; i++) {
      if (await probe()) return;
      await sleep(250);
    }
    throw new Error(`timed out waiting for ${what}`);
  };
  const shows = (p, needle, what) => waitFor(async () => flat(await mirrorOf(p)).includes(flat(needle)), what || `the pane showing ${needle}`, 80);
  const post = (p, path, values = {}) => p.evaluate(async ([path, values]) => {
    const body = new URLSearchParams(values);
    body.set("csrf_token", document.querySelector('meta[name="csrf-token"]').content);
    const res = await fetch(path, {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded", Accept: "application/json" },
      body: body.toString(),
    });
    return { status: res.status, body: await res.json().catch(() => ({})) };
  }, [path, values]);
  // The project's row on the projects page: the ids it links as running and
  // the ids it offers to resume.
  const listed = () => page.evaluate(async (project) => {
    const html = await (await fetch("/projects")).text();
    const row = new DOMParser().parseFromString(html, "text/html").getElementById(`project-${project}`);
    const ids = (selector, attribute, pattern) => [...new Set([...(row ? row.querySelectorAll(selector) : [])]
      .map((el) => (el.getAttribute(attribute).match(pattern) || [])[1]).filter(Boolean))];
    return {
      running: ids('a[href^="/coders/"]', "href", /^\/coders\/([0-9a-f-]{36})$/),
      inactive: ids('form[action$="/resume"]', "action", /^\/coders\/([0-9a-f-]{36})\/resume$/),
      text: row ? row.textContent : "",
    };
  }, project);
  const typeLine = async (p, text) => {
    await p.keyboard.type(text, { delay: 15 });
    await p.keyboard.press("Enter");
  };
  let dir = "";
  let key = "";
  let lateID = "";
  let taskID = "";
  let askID = "";
  let stripID = "";
  let stripKey = "";
  let earlyKey = "";
  let earlyID = "";

  try {
    await L.login(page);
    await L.login(phone);
    await L.createProject(page, project);
    dir = await L.projectPath(page, project);

    await run("the form offers Codex, a start without a prompt lands at once on a phone", async () => {
      await phone.goto(`${BASE}/coders/new?project=${encodeURIComponent(project)}`, { waitUntil: "domcontentloaded" });
      const f = phone.locator('form:has(select[name="agent"])').first();
      const offered = await f.locator('select[name="coder"] option').evaluateAll((os) => os.map((o) => `${o.value}:${o.textContent.trim()}`));
      assert(offered.includes("codex:Codex"), `the form does not offer Codex: ${offered}`);
      await f.locator('select[name="coder"]').selectOption("codex");
      await f.locator('input[name="name"]').fill(`late ${tag}`);
      const started = Date.now();
      await Promise.all([phone.waitForURL(/\/coders\/(?!new)[^/]+$/, { timeout: 20000 }), f.locator('button[type="submit"]').first().tap()]);
      assert(Date.now() - started < 6000, `the start waited ${Date.now() - started} ms for a record that cannot exist yet`);
      key = idOf(phone.url());
      await phone.waitForSelector("#terminal-cursor-input", { timeout: 15000 });
      await shows(phone, "MODE yolo");
    });

    // Read on the desktop page: the phone's pane is too narrow to keep the
    // whole argument line in view.
    await run("codex starts in the project directory, trusted on its command line, without approvals or sandbox", async () => {
      await page.goto(`${BASE}/coders/${key}`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
      await shows(page, `CWD ${dir}`);
      await shows(page, "TRUST yes");
      await shows(page, "MODE yolo");
      await shows(page, "COLORS screen unset");
      await shows(page, 'THEME "ansi"');
      const text = flat(await mirrorOf(page));
      assert(text.includes(flat(`"--no-daemon", "-c", "projects={\\"${dir}\\"={trust_level=\\"trusted\\"}}", "-c", "tui.theme=\\"ansi\\"", "-c", "tui.notifications=true", "-c", "tui.notification_method=\\"bel\\"", "-c", "tui.notification_condition=\\"always\\"", "--dangerously-bypass-approvals-and-sandbox"]`)),
        `the start carries other arguments: ${text.slice(0, 400)}`);
      assert(!text.includes("Trustthisfolder?"), "codex came up on its trust question");
    });

    await run("codex finds a dark background behind every scheme of the terminal, after a live switch too", async () => {
      const switched = async (scheme) => {
        const posted = page.waitForRequest((r) => /\/terminal-theme$/.test(r.url()) && r.method() === "POST", { timeout: 8000 });
        await page.emulateMedia({ colorScheme: scheme });
        return JSON.parse((await posted).postData() || "{}").bg || "";
      };
      const dark = await switched("dark");
      const light = await switched("light");
      assert(dark !== light && /^#[ef]/i.test(light), `the terminal posted ${dark} for dark and ${light} for light`);
      await sleep(500);
      await page.click("#terminal");
      await typeLine(page, "BG");
      await shows(page, "BACKGROUND rgb:0000/0000/0000");
    });

    await run("before the first message the coder runs under the cockpit's key and nothing is resumable", async () => {
      const l = await listed();
      assert(l.running.includes(key) && l.running.length === 1, `running: ${l.running}`);
      assert(l.inactive.length === 0, `a session is offered for resume before codex wrote one: ${l.inactive}`);
      const head = (await phone.locator("[data-name-label]").first().textContent()).trim();
      assert(head === `late-${tag}` || head === `late ${tag}`, `the heading is ${head}`);
    });

    await run("before the first message there are no files to keep, an upload is refused", async () => {
      const res = await phone.evaluate(async (key) => {
        const body = new FormData();
        body.set("csrf_token", document.querySelector('meta[name="csrf-token"]').content);
        body.set("files", new File(["early\n"], "early.txt", { type: "text/plain" }));
        const r = await fetch(`/coders/${key}/files`, { method: "POST", body });
        return { status: r.status, text: await r.text() };
      }, key);
      assert(res.status === 400 && res.text.includes("Session files will be available after the first message."), `the upload answered ${res.status}: ${res.text.slice(0, 300)}`);
    });

    await run("the first message typed on the phone ties the coder to codex's record: listed once under codex's id, the name written into it", async () => {
      await phone.evaluate(() => document.getElementById("terminal-cursor-input").focus());
      await typeLine(phone, "hello from the phone");
      await shows(phone, 'USER "hello from the phone"');
      await shows(phone, "ANSWER pong");
      await waitFor(async () => {
        const l = await listed();
        return l.running.length === 1 && l.running[0] !== key;
      }, "the coder being listed under codex's own id");
      const l = await listed();
      lateID = l.running[0];
      assert(l.inactive.length === 0, `the running session is also offered for resume: ${l.inactive}`);
      await waitFor(async () => {
        const name = await (await page.request.get(`${BASE}/coders/${lateID}/name`)).text();
        return name.includes(`late-${tag}`) || name.includes(`late ${tag}`);
      }, "the session carrying the name it was started with");
    });

    await run("the open page keeps working under the key it was opened with, a reload lands on codex's id", async () => {
      await phone.evaluate(() => document.getElementById("terminal-cursor-input").focus());
      await typeLine(phone, "still here");
      await shows(phone, 'USER "still here"');
      assert(idOf(phone.url()) === key, `the open page moved by itself: ${phone.url()}`);
      await phone.reload({ waitUntil: "domcontentloaded" });
      await waitFor(async () => idOf(phone.url()) === lateID, "the reload landing on codex's id");
      await phone.waitForSelector("#terminal-cursor-input", { timeout: 15000 });
      await shows(phone, 'USER "still here"');
    });

    await run("on the desktop the strip follows the record: the tab moves onto codex's id and stays the active one on the open page", async () => {
      const url = await L.createSession(page, project, `strip ${tag}`, "codex");
      const minted = idOf(url);
      stripKey = minted;
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
      await shows(page, "MODE yolo");
      await page.click("#terminal");
      await typeLine(page, "BG");
      await shows(page, "BACKGROUND rgb:0000/0000/0000", "a codex started under a light terminal finding a dark background");
      await typeLine(page, "hello from the desk");
      await shows(page, 'USER "hello from the desk"');
      const active = () => page.evaluate(() => document.querySelector("terminal-tabs .terminal-tab.active")?.dataset.tabId || "");
      await waitFor(async () => { const id = await active(); return id !== "" && id !== minted; }, "the active tab moving onto codex's id", 80);
      stripID = await active();
      assert(idOf(page.url()) === minted, `the open page moved by itself: ${page.url()}`);
      assert(await page.locator(`terminal-tabs .terminal-tab[data-tab-id="${minted}"]`).count() === 0, "the strip still carries the minted key");
      await typeLine(page, "desk again");
      await shows(page, 'USER "desk again"');
    });

    await run("the page left on the minted key copies the screen, finds file links and keeps an upload with the coder", async () => {
      const toasts = () => page.evaluate(() => [...document.querySelectorAll(".toast")].map((t) => t.textContent).join(" "));
      await page.locator("[data-terminal-copy]:visible").first().click();
      const screen = page.locator('terminal-copy [data-copy-face="text"]:visible');
      if (await screen.count()) await screen.first().click();
      await page.waitForFunction(() => (document.querySelector("terminal-copy [data-copy-text]")?.textContent || "").includes("desk again"), null, { timeout: 15000 });
      assert(!(await toasts()).includes("not running"), `a toast says: ${await toasts()}`);
      await page.locator("terminal-copy [data-copy-close]").first().click();
      const links = await page.evaluate(async (key) => {
        const r = await fetch(`/coders/${key}/file-links`, {
          method: "POST",
          headers: { "Content-Type": "application/json", "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]').content },
          body: JSON.stringify({ words: ["notes.txt"] }),
        });
        return r.status;
      }, stripKey);
      assert(links === 200, `file links under the minted key answer ${links}`);
      const name = `late-${tag}.txt`;
      await page.click(".attach-desktop .coder-files-button");
      await L.modalShown(page, "coder-files-modal");
      await page.setInputFiles('#coder-files-modal input[type="file"][name="files"]', [{ name, mimeType: "text/plain", buffer: Buffer.from("late file\n") }]);
      const row = page.locator("#coder-files-modal .list-group-item", { hasText: name });
      await row.locator("[data-copy-file-path]").waitFor({ state: "visible", timeout: 15000 });
      const path = await row.locator("[data-copy-file-path]").getAttribute("data-copy-file-path");
      assert(path.includes(`/dev-cockpit-files/${stripID}/`), `the upload lives at ${path}`);
      await page.click("#coder-files-modal .btn-close");
      await page.waitForSelector("#coder-files-modal", { state: "hidden", timeout: 6000 });
      assert((await (await page.request.get(`${BASE}/coders/${stripID}/files`)).text()).includes(name), "the upload is not listed under codex's id");
      assert(idOf(page.url()) === stripKey, `the open page moved by itself: ${page.url()}`);
      await post(page, `/coders/${stripID}/stop`);
      await post(page, `/coders/${stripID}/delete`);
      await waitFor(async () => { const l = await listed(); return !l.running.includes(stripID) && !l.inactive.includes(stripID); }, "the desk coder being gone");
    });

    await run("a rename from the heading before the first message stands once codex writes the record", async () => {
      const url = await L.createSession(page, project, `early ${tag}`, "codex");
      earlyKey = idOf(url);
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
      await shows(page, "MODE yolo");
      const name = `renamed-${tag}`;
      await page.click("[data-name-label]");
      await page.waitForSelector(".swal2-input", { state: "visible", timeout: 5000 });
      await page.fill(".swal2-input", name);
      await page.click(".swal2-confirm");
      await shows(page, `RENAMED ${name}`);
      const nameOf = async () => (await (await page.request.get(`${BASE}/coders/${earlyKey}/name`)).text());
      for (let i = 0; i < 20; i++) {
        const now = await nameOf();
        assert(now.includes(name) && !now.includes(`early-${tag}`) && !now.includes(`early ${tag}`), `the name is ${now}`);
        await sleep(250);
      }
      await page.click("#terminal");
      await typeLine(page, "after the rename");
      await shows(page, 'USER "after the rename"');
      await sleep(2000);
      assert((await nameOf()).includes(name), `the first message took the name: ${await nameOf()}`);
      await waitFor(async () => (await page.locator("[data-name-label]").textContent()).trim() === name, "the heading keeping the name");
      const active = () => page.evaluate(() => document.querySelector("terminal-tabs .terminal-tab.active")?.dataset.tabId || "");
      await waitFor(async () => { const id = await active(); return id !== "" && id !== earlyKey; }, "the active tab moving onto codex's id", 80);
      earlyID = await active();
      await post(page, `/coders/${earlyID}/stop`);
      await post(page, `/coders/${earlyID}/delete`);
      await waitFor(async () => { const l = await listed(); return !l.running.includes(earlyID) && !l.inactive.includes(earlyID); }, "the renamed coder being gone");
    });

    await run("a prompt that starts with a dash and holds quotes, a newline and shell code arrives as the first message, a hostile name as text", async () => {
      const prompt = `-x "quoted" 'single' $(touch pwned) \`touch pwned2\`; touch pwned3\nsecond line`;
      const name = `-n "q" $(touch named) ${tag}`;
      const res = await post(page, "/coders/new", { name, project: dir, coder: "codex", automatic_approval: "on", prompt });
      assert(res.status === 200 && res.body.id, `start answered ${res.status} ${JSON.stringify(res.body)}`);
      taskID = res.body.id;
      await page.goto(`${BASE}/coders/${taskID}`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
      await shows(page, `USER ${JSON.stringify(prompt)}`);
      const text = flat(await mirrorOf(page));
      assert(text.includes(flat(`"--", ${JSON.stringify(prompt)}]`)), `the prompt is not the one argument behind the end of options: ${text.slice(0, 500)}`);
      await page.keyboard.type("LS", { delay: 15 });
      await page.keyboard.press("Enter");
      await shows(page, "FILES []", "the project directory staying empty");
    });

    await run("a start with a prompt lands on codex's own id at once, named in codex's record", async () => {
      const l = await listed();
      assert(l.running.includes(taskID), `the coder is not listed under the id the start answered: ${JSON.stringify(l)}`);
      assert(l.running.length === 2 && l.inactive.length === 0, `listed: ${JSON.stringify(l)}`);
      const name = (await page.locator("[data-name-label]").first().textContent()).trim();
      assert(name.includes(tag) && !name.includes("coder-"), `the heading is ${name}`);
    });

    await run("automatic approval off: codex asks, the question is answered from the page", async () => {
      const res = await post(page, "/coders/new", { name: `ask ${tag}`, project: dir, coder: "codex", prompt: "ASK" });
      assert(res.status === 200 && res.body.id, `start answered ${res.status} ${JSON.stringify(res.body)}`);
      askID = res.body.id;
      await page.goto(`${BASE}/coders/${askID}`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
      await shows(page, "MODE ask");
      const text = flat(await mirrorOf(page));
      assert(!text.includes("--dangerously-bypass-approvals-and-sandbox"), "a coder without automatic approval runs without approvals");
      await shows(page, "Allow command? (y/n)");
      await page.click("#terminal");
      await page.keyboard.type("y");
      await page.keyboard.press("Enter");
      await shows(page, "APPROVED");
    });

    await run("automatic approval on: the same request runs without a question", async () => {
      await page.goto(`${BASE}/coders/${taskID}`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
      await page.click("#terminal");
      await page.keyboard.type("ASK", { delay: 15 });
      await page.keyboard.press("Enter");
      await shows(page, "RAN without asking");
      assert(!flat(await mirrorOf(page)).includes("Allowcommand?"), "codex asked although approvals are off");
    });

    await run("a click on the heading renames the coder through codex's own /rename", async () => {
      const name = `head-${tag}`;
      await page.click("[data-name-label]");
      await page.waitForSelector(".swal2-input", { state: "visible", timeout: 5000 });
      const note = (await page.locator(".swal2-html-container").textContent()) || "";
      assert(note.includes("/rename <new name>"), `the dialog does not say what is sent: ${JSON.stringify(note)}`);
      await page.fill(".swal2-input", name);
      await page.click(".swal2-confirm");
      await shows(page, `RENAMED ${name}`);
      await waitFor(async () => (await page.locator("[data-name-label]").textContent()).trim() === name, "the heading taking the name");
      await waitFor(() => page.evaluate(([id, n]) => document.querySelector(`terminal-tabs .terminal-tab[data-tab-id="${id}"]`)?.dataset.tabName === n, [taskID, name]), "the strip taking the name");
    });

    await run("the strip menu offers Rename for a codex coder", async () => {
      await page.click(`terminal-tabs .terminal-tab[data-tab-id="${taskID}"]`, { button: "right" });
      const item = page.locator(".dc-context-menu .dropdown-item", { hasText: /^Rename$/ }).first();
      await item.waitFor({ state: "visible", timeout: 5000 });
      await page.keyboard.press("Escape");
    });

    await run("session files: an upload is listed and opened", async () => {
      const name = `note-${tag}.txt`;
      await page.click(".attach-desktop .coder-files-button");
      await L.modalShown(page, "coder-files-modal");
      await page.setInputFiles('#coder-files-modal input[type="file"][name="files"]', [{ name, mimeType: "text/plain", buffer: Buffer.from("codex file\n") }]);
      const row = page.locator("#coder-files-modal .list-group-item", { hasText: name });
      await row.locator('a[href*="/files/download"]').waitFor({ state: "visible", timeout: 15000 });
      const href = await row.locator('a[href*="/files/download"]').getAttribute("href");
      const body = await (await page.request.get(new URL(href, BASE).toString())).text();
      assert(body === "codex file\n", `the file reads ${JSON.stringify(body)}`);
      const path = await row.locator("[data-copy-file-path]").getAttribute("data-copy-file-path");
      assert(path.includes(`/dev-cockpit-files/${taskID}/`), `the file lives at ${path}`);
      await page.click("#coder-files-modal .btn-close");
      await page.waitForSelector("#coder-files-modal", { state: "hidden", timeout: 6000 });
    });

    await run("stop keeps the session resumable under the same id, with its name", async () => {
      const res = await post(page, `/coders/${taskID}/stop`);
      assert(res.status < 400, `stop answered ${res.status}`);
      await waitFor(async () => {
        const l = await listed();
        return l.inactive.includes(taskID) && !l.running.includes(taskID);
      }, "the stopped coder being offered for resume");
      assert((await listed()).text.includes(`head-${tag}`), "the stopped session lost its name");
    });

    await run("resume brings the same conversation back, also after the first message came late", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      const card = `#project-${project}`;
      await Promise.all([
        page.waitForURL(new RegExp(`/coders/${taskID}$`), { timeout: 20000 }),
        page.locator(`${card} form[action="/coders/${taskID}/resume"]`).first().locator('button[type="submit"]').first().click(),
      ]);
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
      await shows(page, `RESUMED ${taskID}`);
      await shows(page, 'HISTORY "ASK"');
      await shows(page, "TRUST yes");
      await shows(page, "COLORS screen unset");
      await shows(page, 'THEME "ansi"');
      const text = flat(await mirrorOf(page));
      assert(text.includes(flat(`"--", "${taskID}"]`)) && text.includes('["resume","--no-daemon",'), `the resume carries other arguments: ${text.slice(0, 400)}`);

      await post(page, `/coders/${lateID}/stop`);
      await waitFor(async () => (await listed()).inactive.includes(lateID), "the late session being offered for resume");
      const res = await post(page, `/coders/${lateID}/resume`);
      assert(res.status < 400, `resume answered ${res.status}`);
      await page.goto(`${BASE}/coders/${lateID}`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
      await shows(page, `RESUMED ${lateID}`);
      await shows(page, 'HISTORY "hello from the phone"');
      await shows(page, 'HISTORY "still here"');
    });

    await run("delete removes the session, codex's own record and its files", async () => {
      await post(page, `/coders/${taskID}/stop`);
      await waitFor(async () => (await listed()).inactive.includes(taskID), "the coder being stopped");
      const res = await post(page, `/coders/${taskID}/delete`);
      assert(res.status < 400, `delete answered ${res.status}`);
      await waitFor(async () => {
        const l = await listed();
        return !l.inactive.includes(taskID) && !l.running.includes(taskID);
      }, "the deleted session leaving the lists");
      const again = await post(page, `/coders/${taskID}/resume`);
      assert(again.status >= 400 || !(await listed()).running.includes(taskID), "a deleted session came back");
      const files = await page.request.get(`${BASE}/coders/${taskID}/files`);
      assert(files.status() >= 400, `the files of a deleted session answer ${files.status()}`);
    });

    await run("a session codex no longer has leaves the lists once its coder stops", async () => {
      await page.goto(`${BASE}/coders/${askID}`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
      await page.click("#terminal");
      await page.keyboard.type("FORGET", { delay: 15 });
      await page.keyboard.press("Enter");
      await shows(page, "FORGOTTEN");
      await waitFor(async () => (await listed()).running.includes(askID), "the running coder staying listed without its record");
      const res = await post(page, `/coders/${askID}/stop`);
      assert(res.status < 400, `stop answered ${res.status}`);
      await waitFor(async () => {
        const l = await listed();
        return !l.running.includes(askID) && !l.inactive.includes(askID);
      }, "the session without a record leaving the lists");
      const resume = await post(page, `/coders/${askID}/resume`);
      assert(resume.status >= 400 || !(await listed()).running.includes(askID), `a session codex no longer has was resumed: ${resume.status}`);
    });

    await run("the other coders are still offered beside Codex", async () => {
      await page.goto(`${BASE}/coders/new?project=${encodeURIComponent(project)}`, { waitUntil: "domcontentloaded" });
      const offered = await page.locator('form:has(select[name="agent"]) select[name="coder"] option').evaluateAll((os) => os.map((o) => o.value));
      for (const id of ["claude", "copilot", "opencode", "codex"]) assert(offered.includes(id), `${id} is not offered: ${offered}`);
    });
  } finally {
    for (const id of [key, lateID, taskID, askID, stripID, earlyID].filter(Boolean)) {
      await post(page, `/coders/${id}/stop`).catch(() => {});
      await post(page, `/coders/${id}/delete`).catch(() => {});
    }
    await L.deleteProject(page, project).catch(() => {});
  }
  const anyFail = L.report("CODER-CODEX", results, bag);
  await browser.close();
  process.exit(anyFail ? 1 : 0);
})();
