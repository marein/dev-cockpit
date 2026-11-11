// Coder coverage: the session create + attach path with the claude coder picked
// in the new-session form, not only copilot. Needs the claude CLI on the host;
// on a single-coder (copilot only) instance the coder select is absent and the
// createSession coder argument is a no-op, so run this against a host with claude.
const { chromium } = require("playwright-core");
const L = require("./lib");
const { assert, sleep } = L;

(async () => {
  const browser = await chromium.launch({ args: ["--no-sandbox"] });
  const bag = { consoleErrors: [], pageErrors: [] };
  const { results, run } = L.makeRunner();
  const tag = `cla-${Date.now().toString(36)}`;
  const project = `zztc-${tag}`;
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1360, height: 900 } });
  const page = await ctx.newPage();
  L.wirePage(page, bag);
  let sessionUrl = null;
  let shellUrl = null;

  try {
    await L.login(page);
    await run("claude: create project + session (claude provider) attaches", async () => {
      await L.createProject(page, project);
      sessionUrl = await L.createSession(page, project, `tccla-${tag.slice(-4)}`, "claude");
      const missing = await L.waitUpgraded(page, [
        "terminal-attach", "terminal-input", "terminal-scroll-zone", "terminal-direction-pad",
        "terminal-setting-select", "coder-file-upload",
      ], 12000);
      assert(missing.length === 0, `not upgraded: ${missing}`);
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
      await sleep(1500);
    });

    await run("claude: Shift+Enter inserts a newline in the prompt box instead of submitting", async () => {
      const mirror = () => page.evaluate(() => (document.querySelector(".attach-selection") || {}).textContent || "");
      await page.click("#terminal");
      let text = "";
      let ready = false;
      for (let i = 0; i < 60; i++) {
        text = await mirror();
        if (/trust this folder|trust the files/i.test(text)) {
          await page.keyboard.press("Enter");
          await sleep(2000);
          continue;
        }
        L.assertNoLogin(text);
        if (text.includes("❯") || /shortcuts|for help/i.test(text)) { ready = true; break; }
        await sleep(1000);
      }
      assert(ready, `claude UI not ready, mirror tail: ${text.slice(-200)}`);
      await sleep(1000);
      await page.keyboard.type("abc", { delay: 50 });
      await sleep(500);
      await page.keyboard.press("Shift+Enter");
      await sleep(800);
      await page.keyboard.type("def", { delay: 50 });
      let box = "";
      let twoLines = false;
      for (let i = 0; i < 12; i++) {
        await sleep(500);
        box = await mirror();
        if (/abc[^\S\n]*\n[^\S\n]*def/.test(box)) { twoLines = true; break; }
      }
      assert(twoLines, `no two-line prompt, mirror tail: ${JSON.stringify(box.slice(-300))}`);
      assert(!box.includes("S-Enter"), "literal S-Enter text leaked into claude");
      await page.keyboard.press("Control+C");
      await sleep(800);
    }, { soft: true });

    await run("claude: a prompt item posted to /input reaches the agent, the pane reacts", async () => {
      const marker = `CLA${tag.slice(-4)}`;
      await page.evaluate(async (text) => {
        const token = document.querySelector('meta[name="csrf-token"]').content;
        await fetch(`${location.pathname}/input`, { method: "POST", headers: { "X-CSRF-Token": token, "Content-Type": "application/json" }, body: JSON.stringify({ items: [{ prompt: text }] }) });
      }, `${marker} please reply`);
      const before = await page.evaluate(() => (document.querySelector(".attach-selection") || {}).textContent || "");
      let changed = false, snippet = "";
      for (let i = 0; i < 40; i++) {
        await sleep(600);
        const now = await page.evaluate(() => (document.querySelector(".attach-selection") || {}).textContent || "");
        if (now !== before) { changed = true; snippet = now.replace(/\s+/g, " ").trim().slice(0, 120); break; }
      }
      assert(changed, "claude pane did not react to the prompt (slow/not authed)");
      return `pane: "${snippet}"`;
    }, { soft: true });

    // Insert hands the path to the pane as one paste with a space on either
    // side, quoted for the space in its name, and never Enter, the dialog
    // stays and closing it hands the terminal the focus. An empty prompt
    // shows it after the prompt mark with no menu or mode, text already
    // there stays around the cursor. Files exist only after the first
    // message, which the check above sent.
    await run("claude: Insert pastes a file's path quoted into the prompt, nothing submits, the dialog stays, closing it focuses the terminal", async () => {
      const mirror = () => page.evaluate(() => (document.querySelector("#terminal .attach-selection") || {}).textContent || "");
      const promptRow = (text) => text.split("\n").filter((l) => l.includes("❯")).pop() || "";
      for (let i = 0; i < 90 && /esc to interrupt/i.test(await mirror()); i++) await sleep(1000);
      const name = `in sert_${tag.slice(-4)}.txt`;
      await page.click(".attach-desktop .coder-files-button");
      await L.modalShown(page, "coder-files-modal");
      await page.setInputFiles('#coder-files-modal input[type="file"][name="files"]', [{ name, mimeType: "text/plain", buffer: Buffer.from("insert\n") }]);
      const row = page.locator("#coder-files-modal .list-group-item", { hasText: name });
      await row.locator("[data-insert-file-path]").waitFor({ state: "visible", timeout: 15000 });
      const path = await row.locator("[data-copy-file-path]").getAttribute("data-copy-file-path");
      const sent = [];
      const watch = (r) => { if (/\/input$/.test(r.url()) && r.method() === "POST") sent.push(r.postData() || ""); };
      const insert = async () => {
        sent.length = 0;
        if (!(await page.locator("#coder-files-modal").isVisible())) {
          await page.click(".attach-desktop .coder-files-button");
          await L.modalShown(page, "coder-files-modal");
          await sleep(400);
        }
        await row.locator("[data-insert-file-path]").click();
        await row.locator("[data-insert-file-path] .ti-check").waitFor({ state: "visible", timeout: 3000 });
        assert(await page.locator("#coder-files-modal .modal-content").isVisible(), "the dialog closed on Insert");
        await page.click("#coder-files-modal .btn-close");
        await page.waitForSelector("#coder-files-modal", { state: "hidden", timeout: 6000 });
        await sleep(2500);
      };
      const want = JSON.stringify({ items: [{ paste: ` '${path}' ` }] });
      const focused = () => page.evaluate(() => document.activeElement?.matches(".xterm-helper-textarea") && !!document.activeElement.closest("#terminal"));
      page.on("request", watch);
      try {
        await insert();
        assert(sent.length === 1 && sent[0] === want, `Insert sent ${JSON.stringify(sent)}, wanted ${want}`);
        assert(await focused(), "the terminal does not have the focus after Insert and close");
        let screen = await mirror();
        assert(/^❯\s\s'\//.test(promptRow(screen)), `the empty prompt reads ${JSON.stringify(promptRow(screen))}`);
        assert(!/for bash mode|\/add-dir\b|\/compact\b|esc to interrupt/i.test(screen), `the insert opened a menu or a turn: ${JSON.stringify(screen.slice(-400))}`);
        await page.keyboard.press("Control+C");
        await sleep(800);
        await page.keyboard.type("abc def", { delay: 40 });
        for (let i = 0; i < 4; i++) await page.keyboard.press("ArrowLeft");
        await sleep(800);
        await insert();
        assert(sent.length === 1 && sent[0] === want, `the second Insert sent ${JSON.stringify(sent)}`);
        screen = await mirror();
        const flat = screen.replace(/\s+/g, "");
        assert(flat.includes(`abc'${path.replace(/\s+/g, "")}'def`), `the path did not land at the cursor: ${JSON.stringify(screen.slice(-400))}`);
        assert(!/esc to interrupt/i.test(screen), "the insert submitted the prompt");
        assert(await focused(), "the terminal does not have the focus after the second Insert and close");
        await page.keyboard.press("Control+C");
        await sleep(800);
      } finally {
        page.off("request", watch);
      }
    });

    await run("claude: stop session redirects", async () => {
      await L.stopSession(page, sessionUrl);
      sessionUrl = null;
    });

    // Shift+Enter follows the pane's foreground program, not the session kind:
    // claude launched by hand inside a shell gets the real key (newline), and
    // after it exits the same pane falls back to plain Enter.
    await run("claude in a shell: Shift+Enter follows the foreground program", async () => {
      const mirror = () => page.evaluate(() => (document.querySelector(".attach-selection") || {}).textContent || "");
      shellUrl = await L.createShell(page, project);
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
      await sleep(1500);
      await page.click("#terminal");
      await page.keyboard.type("claude", { delay: 30 });
      await page.keyboard.press("Enter");
      let text = "";
      let ready = false;
      for (let i = 0; i < 60; i++) {
        await sleep(1000);
        text = await mirror();
        if (/trust this folder|trust the files/i.test(text)) {
          await page.keyboard.press("Enter");
          continue;
        }
        L.assertNoLogin(text);
        if (text.includes("❯") || /shortcuts|for help/i.test(text)) { ready = true; break; }
      }
      assert(ready, `claude not ready in the shell, mirror tail: ${text.slice(-200)}`);
      await sleep(1000);
      await page.keyboard.type("abc", { delay: 50 });
      await sleep(500);
      await page.keyboard.press("Shift+Enter");
      await sleep(800);
      await page.keyboard.type("def", { delay: 50 });
      let box = "";
      let twoLines = false;
      for (let i = 0; i < 12; i++) {
        await sleep(500);
        box = await mirror();
        if (/abc[^\S\n]*\n[^\S\n]*def/.test(box)) { twoLines = true; break; }
      }
      assert(twoLines, `no newline in shell-hosted claude, mirror tail: ${JSON.stringify(box.slice(-300))}`);
      await page.keyboard.press("Control+C");
      await sleep(500);
      await page.keyboard.type("/exit", { delay: 50 });
      await page.keyboard.press("Enter");
      for (let i = 0; i < 20; i++) {
        await sleep(1000);
        text = await mirror();
        if (!text.includes("❯") && !/esc to interrupt/i.test(text)) break;
      }
      await page.keyboard.type("echo BA'C'K", { delay: 30 });
      await page.keyboard.press("Shift+Enter");
      let executed = false;
      for (let i = 0; i < 12; i++) {
        await sleep(500);
        text = await mirror();
        if (text.includes("BACK")) { executed = true; break; }
      }
      assert(executed, `Shift+Enter did not run the command after claude exited, tail: ${text.slice(-200)}`);
    }, { soft: true });
  } finally {
    try {
      if (sessionUrl) await L.stopSession(page, sessionUrl);
      if (shellUrl) await L.deleteShell(page, shellUrl);
      await L.deleteProject(page, project);
    } catch (e) { console.log("cleanup note:", e.message); }
  }

  const anyFail = L.report("PROVIDER-CLAUDE", results, bag);
  await ctx.close();
  await browser.close();
  process.exit(anyFail ? 1 : 0);
})();
