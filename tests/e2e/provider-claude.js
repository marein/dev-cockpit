// Provider coverage: the session create + attach path against `--provider claude`,
// not only copilot. Point BASE_URL at a claude throwaway instance.
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

  try {
    await L.login(page);
    await run("claude: create project + session (claude provider) attaches", async () => {
      await L.createProject(page, project);
      sessionUrl = await L.createSession(page, project, `tccla-${tag.slice(-4)}`);
      const missing = await L.waitUpgraded(page, [
        "session-attach", "session-input", "session-scroll-zone", "session-direction-pad",
        "session-terminal-setting-select", "session-file-upload",
      ], 12000);
      assert(missing.length === 0, `not upgraded: ${missing}`);
      await page.waitForSelector("#session-terminal .xterm-screen canvas", { timeout: 15000 });
      await sleep(1500);
    });

    await run("claude: prompt reaches /input, agent pane reacts", async () => {
      const marker = `CLA${tag.slice(-4)}`;
      await page.click(".attach-desktop [data-session-prompt-modal-open]");
      await L.modalShown(page, "session-prompt-modal");
      await sleep(600);
      await page.fill("#session-prompt-modal-text", `${marker} please reply`);
      const reqP = page.waitForRequest((r) => /\/input$/.test(r.url()) && r.method() === "POST", { timeout: 8000 });
      await page.keyboard.press("Control+Enter");
      const req = await reqP;
      assert((req.postData() || "").includes(marker), "prompt not carried to /input");
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

    await run("claude: stop session redirects", async () => {
      await L.stopSession(page, sessionUrl);
      sessionUrl = null;
    });
  } finally {
    try {
      if (sessionUrl) await L.stopSession(page, sessionUrl);
      await L.deleteProject(page, project);
    } catch (e) { console.log("cleanup note:", e.message); }
  }

  const anyFail = L.report("PROVIDER-CLAUDE", results, bag);
  await ctx.close();
  await browser.close();
  process.exit(anyFail ? 1 : 0);
})();
