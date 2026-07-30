const L = require("./lib");
const { assert, sleep, submitBtn, confirmSwal, modalShown, BASE } = L;

// Sessions: the provider agent runtime, plus resumable sessions and session files.
// Custom elements terminal-attach, terminal-input, terminal-scroll-zone,
// terminal-direction-pad, coder-file-upload, terminal-setting-select. The
// shared terminal interaction is in terminal.js. The prompt modal is session only and
// diverges by pointer: desktop opens it from .attach-desktop, mobile from
// .attach-mobile; both submit as one whole prompt to /input (Ctrl/Cmd+Enter). Routes:
// GET/POST /coders/new, GET /sessions/:id, POST /sessions/:id/{stop,input,resize},
// GET .../stream, .../files (+POST upload, /download, /delete), POST /coders/:id/{resume,delete}.
// Creates a real provider session and stops it; safe because it is our own throwaway.

L.runFeature("SESSIONS", async ({ page, run, mobilePage }) => {
  const tag = `sess-${Date.now().toString(36)}`;
  const project = `zztc-${tag}`;
  const agentId = `tcagent-${tag.slice(-5)}`;
  let sessionUrl = null;
  try {
    await L.createProject(page, project);
    // an agent so we can assert it populates the session agent select
    await page.goto(`${BASE}/agents/new`, { waitUntil: "domcontentloaded" });
    await page.fill('input[name="agent_id"]', agentId); await page.fill('input[name="agent_description"]', "test"); await page.fill('textarea[name="agent_instructions"]', "test");
    await Promise.all([page.waitForURL(/\/agents(\?coder=\w+)?$/, { timeout: 10000 }), submitBtn(page, 'input[name="agent_id"]').click()]);

    await run("new session form renders fields + agent select is populated", async () => {
      await page.goto(`${BASE}/coders/new?project=${encodeURIComponent(project)}`, { waitUntil: "domcontentloaded" });
      const has = await page.evaluate(() => ({ name: !!document.querySelector('input[name="name"]'), project: !!document.querySelector('select[name="project"]'), agent: !!document.querySelector('select[name="agent"]'), approval: !!document.querySelector('input[name="automatic_approval"]') }));
      assert(Object.values(has).every(Boolean), `missing fields: ${JSON.stringify(has)}`);
      const agentOption = await page.evaluate((id) => [...document.querySelectorAll('select[name="agent"] option')].some((o) => o.value === id || o.textContent.includes(id)), agentId);
      assert(agentOption, "created agent not in the agent select");
    });

    await run("create -> attach elements + canvas", async () => {
      sessionUrl = await L.createSession(page, project, `tcsess-${tag.slice(-4)}`);
      assert((await L.waitUpgraded(page, ["terminal-attach", "terminal-input", "coder-file-upload", "terminal-setting-select"], 12000)).length === 0, "not upgraded");
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
      await sleep(2000);
    });

    await run("desktop: prompt modal sends to /input, closes, agent reacts", async () => {
      const marker = `PMK${tag.slice(-4)}`;
      await page.click(".attach-desktop [data-terminal-prompt-modal-open]");
      await modalShown(page, "terminal-prompt-modal"); await sleep(600);
      await page.fill("#terminal-prompt-modal-text", `${marker} please reply`);
      const reqP = page.waitForRequest((r) => /\/input$/.test(r.url()) && r.method() === "POST", { timeout: 8000 });
      await page.keyboard.press("Control+Enter");
      assert(((await reqP).postData() || "").includes(marker), "prompt not carried to /input");
      await page.waitForFunction(() => { const m = document.getElementById("terminal-prompt-modal"); return m && !m.classList.contains("show"); }, null, { timeout: 6000 });
      const before = await page.evaluate(() => (document.querySelector(".attach-selection") || {}).textContent || "");
      let changed = false; for (let i = 0; i < 30; i++) { await sleep(600); if ((await page.evaluate(() => (document.querySelector(".attach-selection") || {}).textContent || "")) !== before) { changed = true; break; } }
      assert(changed, "agent pane did not react (slow/not authed)");
    }, { soft: true });

    await run("files: multi upload -> Done -> list, reference (Copied), download, delete", async () => {
      await page.goto(sessionUrl, { waitUntil: "domcontentloaded" });
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 12000 });
      await page.click(".attach-desktop .coder-files-button");
      await modalShown(page, "coder-files-modal");
      const f1 = `u1_${tag.slice(-4)}.txt`, f2 = `u2_${tag.slice(-4)}.txt`, content = `payload ${tag}\n`;
      // Picking sends right away, the button only opens the file chooser.
      await page.setInputFiles('#coder-files-modal input[type="file"][name="files"]', [{ name: f1, mimeType: "text/plain", buffer: Buffer.from(content) }, { name: f2, mimeType: "text/plain", buffer: Buffer.from("two\n") }]);
      await page.waitForFunction(() => document.querySelectorAll('#coder-files-modal [data-file-index]').length >= 2, null, { timeout: 8000 });
      await page.waitForFunction(() => { const s = [...document.querySelectorAll('#coder-files-modal [data-file-status]')].map((e) => e.textContent.trim()); return s.length >= 2 && s.every((x) => x === "Done"); }, null, { timeout: 15000 });
      await page.waitForFunction((ns) => { const c = document.querySelector("[data-coder-files-content]"); return c && ns.every((n) => c.textContent.includes(n)); }, [f1, f2], { timeout: 10000 });
      // reference + download the first file
      const copyBtn = page.locator("#coder-files-modal [data-copy-file-path]").first();
      await copyBtn.click();
      await page.waitForFunction(() => /Copied/.test((document.querySelector("[data-copy-file-path]") || {}).innerHTML || ""), null, { timeout: 3000 });
      const href = await page.locator('#coder-files-modal a[href*="/files/download"]').first().getAttribute("href");
      const dl = await page.context().request.get(BASE + href);
      assert(dl.status() === 200 && (await dl.text()).includes(content.trim()), "download mismatch");
      // delete one; wait for the delete POST (the confirm is a swal targeted into the
      // modal) then for a download link to drop. The success fragment echoes the name,
      // so count the rows, do not text-match.
      const beforeLinks = await page.locator('#coder-files-modal a[href*="/files/download"]').count();
      await page.click("#coder-files-modal form[data-coder-file-delete] button[type=\"submit\"]");
      await page.waitForSelector(".swal2-confirm", { state: "visible", timeout: 8000 });
      const respP = page.waitForResponse((r) => /\/files\/delete$/.test(r.url()) && r.request().method() === "POST", { timeout: 10000 });
      await sleep(150); await page.click(".swal2-confirm");
      assert((await respP).status() < 400, "delete POST failed");
      await page.waitForFunction((n) => document.querySelectorAll('#coder-files-modal a[href*="/files/download"]').length < n, beforeLinks, { timeout: 10000 });
      await page.keyboard.press("Escape").catch(() => {});
    });

    // A paste into the open dialog takes the same path as a pick: it uploads,
    // the hidden input is never something the user has to submit.
    await run("files: a paste into the open dialog uploads right away", async () => {
      await page.goto(sessionUrl, { waitUntil: "domcontentloaded" });
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 12000 });
      await page.click(".attach-desktop .coder-files-button");
      await modalShown(page, "coder-files-modal");
      const f3 = `p1_${tag.slice(-4)}.txt`;
      const carried = await page.evaluate((name) => {
        const data = new DataTransfer();
        data.items.add(new File(["pasted one\n"], name, { type: "text/plain" }));
        if (!data.files.length) return false;
        document.getElementById("coder-files-modal").dispatchEvent(new ClipboardEvent("paste", { clipboardData: data, bubbles: true, cancelable: true }));
        return true;
      }, f3);
      assert(carried, "the engine builds no file clipboard");
      await page.waitForFunction((n) => { const c = document.querySelector("[data-coder-files-content]"); return c && c.textContent.includes(n); }, f3, { timeout: 15000 });
      await page.keyboard.press("Escape").catch(() => {});
    });

    // The file input is hidden, so the button is the way to it: with nothing
    // picked it opens the file chooser instead of posting an empty form.
    await run("files: upload with nothing picked opens the file chooser", async () => {
      await page.goto(sessionUrl, { waitUntil: "domcontentloaded" });
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 12000 });
      await page.click(".attach-desktop .coder-files-button");
      await modalShown(page, "coder-files-modal");
      assert(!(await page.locator('#coder-files-modal input[type="file"][name="files"]').isVisible()), "the file input is on the page next to the button");
      const posts = [];
      const watch = (r) => { if (/\/files$/.test(r.url()) && r.method() === "POST") posts.push(r.url()); };
      page.on("request", watch);
      try {
        const chooser = page.waitForEvent("filechooser", { timeout: 6000 });
        await page.click('#coder-files-modal [data-coder-file-upload-form] button[type="submit"]');
        assert(await chooser, "no file chooser opened");
        await sleep(400);
        assert(posts.length === 0, `the empty upload still posted: ${posts.join(" ")}`);
        assert((await page.locator("#coder-files-modal .alert-danger").count()) === 0, "the empty upload answered with an error");
      } finally {
        page.off("request", watch);
      }
      await page.keyboard.press("Escape").catch(() => {});
    });

    // On the terminal a file clipboard takes the drop path, and the text part of
    // that same clipboard must never arrive in the pane as keystrokes.
    await run("files: a paste onto the terminal uploads like a drop, nothing types", async () => {
      await page.goto(sessionUrl, { waitUntil: "domcontentloaded" });
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 12000 });
      await sleep(1000);
      const f4 = `p2_${tag.slice(-4)}.txt`;
      const typed = [];
      const watch = (r) => { if (/\/input$/.test(r.url()) && r.method() === "POST") typed.push(r.postData() || ""); };
      page.on("request", watch);
      try {
        const carried = await page.evaluate((name) => {
          const data = new DataTransfer();
          data.items.add(new File(["pasted two\n"], name, { type: "text/plain" }));
          data.setData("text/plain", name);
          if (!data.files.length) return false;
          document.querySelector(".xterm-helper-textarea").dispatchEvent(new ClipboardEvent("paste", { clipboardData: data, bubbles: true, cancelable: true }));
          return true;
        }, f4);
        assert(carried, "the engine builds no file clipboard");
        await modalShown(page, "coder-files-modal");
        await page.waitForFunction((n) => { const c = document.querySelector("[data-coder-files-content]"); return c && c.textContent.includes(n); }, f4, { timeout: 15000 });
        await sleep(500);
        assert(typed.length === 0, `the file clipboard reached the pane: ${typed.join(" ")}`);
      } finally {
        page.off("request", watch);
      }
      await page.keyboard.press("Escape").catch(() => {});
    });

    await run("mobile: prompt modal opens from the mobile toolbar", async () => {
      const mp = await mobilePage();
      await mp.goto(sessionUrl, { waitUntil: "domcontentloaded" });
      await mp.waitForSelector("#terminal .xterm-screen canvas", { timeout: 12000 }); await sleep(800);
      await mp.locator(".attach-mobile [data-terminal-prompt-modal-open]").first().click();
      await modalShown(mp, "terminal-prompt-modal");
      assert(await mp.$("#terminal-prompt-modal-text"), "prompt textarea missing on mobile");
      await mp.keyboard.press("Escape").catch(() => {});
    });

    await run("legacy /sessions URLs redirect to /coders", async () => {
      await page.goto(`${BASE}/sessions/new?project=${encodeURIComponent(project)}`, { waitUntil: "domcontentloaded" });
      assert(page.url().includes("/coders/new"), `not redirected: ${page.url()}`);
    });

    await run("resumable: stop -> resumable entry -> resume -> delete", async () => {
      // Every resume/delete selector is scoped to the scratch project card:
      // /projects lists the real projects' resumables too, and an unscoped
      // .first() resumes or deletes someone else's stored session.
      const card = `#project-${project}`;
      await L.stopSession(page, sessionUrl); sessionUrl = null;
      assert((await page.locator(".swal2-toast .swal2-error").count()) === 0, "error toast after user stop");
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(`${card} form[action^="/coders/"][action$="/resume"]`, { timeout: 8000 });
      await Promise.all([page.waitForURL(/\/coders\/(?!new)[^/]+$/, { timeout: 20000 }), page.locator(`${card} form[action$="/resume"]`).first().locator('button[type="submit"]').first().click()]);
      sessionUrl = page.url();
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
      await L.stopSession(page, sessionUrl); sessionUrl = null;
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      const before = await page.locator(`${card} form[action^="/coders/"][action$="/delete"]`).count();
      assert(before >= 1, "no resumable to delete");
      await page.locator(`${card} form[action^="/coders/"][action$="/delete"]`).first().locator('button[type="submit"]').first().click();
      await confirmSwal(page); await sleep(800);
      assert(await page.locator(`${card} form[action^="/coders/"][action$="/delete"]`).count() < before, "resumable row not removed");
    });

    // The chip only carries stop, delete lives in its context menu and takes a
    // running coder in one request: the server stops it before it drops the
    // conversation, so it must not come back as a resumable row.
    await run("chip context menu deletes a running coder, stop first", async () => {
      const card = `#project-${project}`;
      const url = await L.createSession(page, project, `chipdel-${tag.slice(-4)}`);
      const id = url.split("/").filter(Boolean).pop();
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      const chip = page.locator(`${card} [data-chip][data-chip-kind="coder"][data-chip-id="${id}"]`);
      await chip.waitFor({ state: "visible", timeout: 10000 });
      await chip.click({ button: "right" });
      await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
      await page.click('.dc-context-menu button:text-is("Delete")');
      await confirmSwal(page);
      await page.waitForSelector(`${card} [data-chip][data-chip-id="${id}"]`, { state: "detached", timeout: 10000 });
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await sleep(500);
      assert((await page.locator(`${card} form[action="/coders/${id}/resume"]`).count()) === 0, "the deleted coder is still resumable");
    });

    // The input route is shared: the assistant reaches it over the local socket
    // and is told to run `coder-resume`, a browser must never read a command it
    // cannot type on a phone. Both are answered from the same classified state,
    // so this checks the browser half of it: what is wrong, and nothing offered
    // where there is nothing to offer.
    await run("input to a coder that is not running says why, in browser words", async () => {
      const probe = (target) => page.evaluate(async (t) => {
        const res = await fetch(`/coders/${t}/input`, {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            Accept: "application/json",
            "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]').content,
          },
          body: JSON.stringify({ items: [{ prompt: "ping" }] }),
        });
        return { status: res.status, body: await res.text() };
      }, target);

      const url = await L.createSession(page, project, `gone-${tag.slice(-4)}`);
      const id = url.split("/").filter(Boolean).pop();
      await L.stopSession(page, url);

      const stopped = await probe(id);
      assert(stopped.status === 410, `a stopped coder answered ${stopped.status}: ${stopped.body}`);
      assert(/not running/i.test(stopped.body), `the stopped coder's answer says nothing: ${stopped.body}`);
      assert(/resume/i.test(stopped.body), `the way back is not named: ${stopped.body}`);
      assert(!stopped.body.includes("coder-resume"), `browser answer carries a CLI command: ${stopped.body}`);

      const unknown = await probe(`zznone${tag.slice(-4)}`);
      assert(unknown.status === 410, `an unknown id answered ${unknown.status}: ${unknown.body}`);
      assert(/no session to resume/i.test(unknown.body), `an unknown id claims something to resume: ${unknown.body}`);
      assert(!/coder-resume/.test(unknown.body), `browser answer carries a CLI command: ${unknown.body}`);
    });
  } finally {
    if (sessionUrl) await L.stopSession(page, sessionUrl).catch(() => {});
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" }).catch(() => {});
    for (let i = 0; i < 4; i++) { const d = page.locator(`#project-${project} form[action^="/coders/"][action$="/delete"]`).first(); if (await d.count() === 0) break; await d.locator("button").first().click().catch(() => {}); await confirmSwal(page).catch(() => {}); await sleep(500); await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" }).catch(() => {}); }
    const af = await page.$(`form[action="/agents/${agentId}/delete"], form[action="/agents/${encodeURIComponent(agentId)}/delete"]`).catch(() => null);
    if (af) { await (await af.$("button")).click().catch(() => {}); await confirmSwal(page).catch(() => {}); await sleep(400); }
    await L.deleteProject(page, project).catch(() => {});
  }
});
