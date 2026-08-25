// Coder coverage: the session create + attach path with the opencode coder
// picked in the new-session form. Needs the opencode CLI on the host. What is
// special about opencode is the session identity: the CLI mints its own ids
// and takes no name, so a start without a task pre-creates the session over
// opencode's server API and resumes it, while a start with a task rides
// --prompt and the promote matches the fresh session on the working
// directory. Both paths have to end on a /coders/ses_… URL, a tab that stays
// on the cockpit's temp key means the promote failed. Model answers ride the
// zen free tier and are checked softly, the identity checks are hard. Run
// against a throwaway instance with an isolated XDG_DATA_HOME, the sessions
// land in opencode's global database.
const { chromium } = require("playwright-core");
const L = require("./lib");
const { assert, sleep } = L;

(async () => {
  const browser = await chromium.launch({ args: ["--no-sandbox"] });
  const bag = { consoleErrors: [], pageErrors: [] };
  const { results, run } = L.makeRunner();
  const tag = `oc-${Date.now().toString(36)}`;
  const project = `zztc-${tag}`;
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1360, height: 900 } });
  const page = await ctx.newPage();
  L.wirePage(page, bag);
  let sessionUrl = null;
  let taskUrl = null;
  const mirror = () => page.evaluate(() => (document.querySelector(".attach-selection") || {}).textContent || "");

  try {
    await L.login(page);
    await run("opencode: a session without a task lands on opencode's own id", async () => {
      await L.createProject(page, project);
      sessionUrl = await L.createSession(page, project, `tcoc-${tag.slice(-4)}`, "opencode");
      const id = new URL(sessionUrl).pathname.split("/").pop();
      assert(id.startsWith("ses"), `the pane kept the temp key, pre-create or promote failed: ${id}`);
      const missing = await L.waitUpgraded(page, [
        "terminal-attach", "terminal-input", "terminal-scroll-zone", "terminal-direction-pad",
        "terminal-setting-select", "coder-file-upload",
      ], 12000);
      assert(missing.length === 0, `not upgraded: ${missing}`);
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
    });

    await run("opencode: the TUI comes up in the pane", async () => {
      let text = "";
      let ready = false;
      for (let i = 0; i < 40; i++) {
        await sleep(1000);
        text = await mirror();
        if (/opencode|Ask anything/i.test(text)) { ready = true; break; }
      }
      assert(ready, `opencode UI not visible, mirror tail: ${text.slice(-200)}`);
    }, { soft: true });

    await run("opencode: stop session redirects", async () => {
      await L.stopSession(page, sessionUrl);
      sessionUrl = null;
    });

    // The page renders no task field, the assistant's create posts it as
    // `prompt` into the same handler: the runner rides that same POST.
    await run("opencode: a session with a task lands on opencode's own id too", async () => {
      await page.goto(`${L.BASE}/coders/new?project=${encodeURIComponent(project)}`, { waitUntil: "domcontentloaded" });
      const f = page.locator('form:has(select[name="agent"])').first();
      await f.locator('input[name="name"]').fill(`tcot-${tag.slice(-4)}`);
      await f.locator('select[name="project"]').selectOption(project).catch(() => {});
      await f.locator('select[name="coder"]').selectOption("opencode").catch(() => {});
      await f.evaluate((form) => {
        const task = document.createElement("input");
        task.type = "hidden";
        task.name = "prompt";
        task.value = "say just: ok";
        form.appendChild(task);
      });
      await Promise.all([
        page.waitForURL(/\/coders\/(?!new)[^/]+$/, { timeout: 30000 }),
        f.locator('button[type="submit"]').first().click(),
      ]);
      taskUrl = page.url();
      const id = new URL(taskUrl).pathname.split("/").pop();
      assert(id.startsWith("ses"), `the pane kept the temp key, the promote missed the fresh session: ${id}`);
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
    });

    await run("opencode: the task reaches the session", async () => {
      let text = "";
      let seen = false;
      for (let i = 0; i < 40; i++) {
        await sleep(1000);
        text = await mirror();
        if (text.includes("say just: ok")) { seen = true; break; }
      }
      assert(seen, `the task is not on the screen, mirror tail: ${text.slice(-200)}`);
    }, { soft: true });
  } finally {
    try {
      if (sessionUrl) await L.stopSession(page, sessionUrl);
      if (taskUrl) await L.stopSession(page, taskUrl);
      await L.deleteProject(page, project);
    } catch (e) { console.log("cleanup note:", e.message); }
  }

  const anyFail = L.report("PROVIDER-OPENCODE", results, bag);
  await ctx.close();
  await browser.close();
  process.exit(anyFail ? 1 : 0);
})();
