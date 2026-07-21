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
      await page.setInputFiles('#coder-files-modal input[type="file"][name="files"]', [{ name: f1, mimeType: "text/plain", buffer: Buffer.from(content) }, { name: f2, mimeType: "text/plain", buffer: Buffer.from("two\n") }]);
      await page.click('#coder-files-modal [data-coder-file-upload-form] button[type="submit"]');
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
  } finally {
    if (sessionUrl) await L.stopSession(page, sessionUrl).catch(() => {});
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" }).catch(() => {});
    for (let i = 0; i < 4; i++) { const d = page.locator(`#project-${project} form[action^="/coders/"][action$="/delete"]`).first(); if (await d.count() === 0) break; await d.locator("button").first().click().catch(() => {}); await confirmSwal(page).catch(() => {}); await sleep(500); await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" }).catch(() => {}); }
    const af = await page.$(`form[action="/agents/${agentId}/delete"], form[action="/agents/${encodeURIComponent(agentId)}/delete"]`).catch(() => null);
    if (af) { await (await af.$("button")).click().catch(() => {}); await confirmSwal(page).catch(() => {}); await sleep(400); }
    await L.deleteProject(page, project).catch(() => {});
  }
});
