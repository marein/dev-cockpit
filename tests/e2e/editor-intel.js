const http = require("http");
const L = require("./lib");
const { assert, sleep, BASE } = L;

// Editor intelligence: LSP completion popup and AI ghost text in the project
// editor. Routes: GET+POST /settings/editor (mode Off/Focused/Flow, language
// server switches, Ollama section with save-and-test), POST
// /projects/:name/editor/completion (JSON, sources ["lsp"]/["ai"]) and
// .../completion/close. The dc-editor element reads data-editor-intel; the
// LSP list renders in the CodeMirror popup, the AI answer as .cm-dc-ghost at
// the cursor (Tab accepts, Escape dismisses). This runner needs its own
// instance (see README "editor-intel.js"): a fake `gopls`
// (tests/e2e/fake-gopls.py) first on the instance's PATH, no real pyright,
// DEV_COCKPIT_OLLAMA_URL=http://127.0.0.1:3799 and the update stub URL on
// :3019; the runner hosts both stubs itself, so it needs --network host.
// Settings are per state dir, so this instance must not be shared with
// runners that assume default settings.

const OLLAMA_PORT = 3799;
const UPDATE_PORT = 3019;

function startStub(port, handler) {
  return new Promise((resolve, reject) => {
    const server = http.createServer(handler);
    server.on("error", reject);
    server.listen(port, "127.0.0.1", () => resolve(server));
  });
}

function startOllamaStub() {
  return startStub(OLLAMA_PORT, (req, res) => {
    res.setHeader("Content-Type", "application/json");
    if (req.url === "/api/tags") {
      res.end(JSON.stringify({ models: [{ name: "qwen2.5-coder:7b" }] }));
      return;
    }
    let body = "";
    req.on("data", (c) => (body += c));
    req.on("end", () => res.end(JSON.stringify({ response: "ghostSuggestion()" })));
  });
}

function startUpdateStub() {
  return startStub(UPDATE_PORT, (req, res) => {
    res.setHeader("Content-Type", "application/json");
    res.end("[]");
  });
}

L.runFeature("EDITOR-INTEL", async ({ page, run }) => {
  const stubs = [await startOllamaStub(), await startUpdateStub()];
  const tag = `intel-${Date.now().toString(36)}`;
  const project = `zztc-${tag}`;
  const editorURL = `${BASE}/projects/${encodeURIComponent(project)}/editor`;
  const tabSel = (path) => `.editor-tab[data-path="${path}"]`;
  const menuItem = (label) => page.locator(".dc-context-menu .dropdown-item", { hasText: new RegExp(`^${label}$`) });

  const treeRootMenu = async () => {
    const box = await page.locator("[data-editor-tree]").boundingBox();
    await page.mouse.click(box.x + box.width / 2, box.y + box.height - 12, { button: "right" });
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
  };
  const newFile = async (name) => {
    await treeRootMenu();
    await menuItem("New file").click();
    await page.waitForSelector(".swal2-input", { state: "visible", timeout: 4000 });
    await page.fill(".swal2-input", name);
    await page.click(".swal2-confirm");
    await page.waitForSelector(tabSel(name), { timeout: 8000 });
    await page.waitForSelector(".swal2-container", { state: "detached", timeout: 4000 }).catch(() => {});
    await sleep(800);
  };
  const openEditor = async () => {
    await page.goto(editorURL, { waitUntil: "domcontentloaded" });
    await page.waitForSelector(".cm-editor", { state: "attached", timeout: 12000 });
    await page.waitForFunction(() => {
      const t = document.querySelector("[data-editor-tree]");
      return t && !/Loading/.test(t.textContent);
    }, null, { timeout: 8000 });
  };
  const bufferText = () => page.evaluate(() => document.querySelector(".cm-content").cmView.view.state.doc.toString());
  const typeInBuffer = async (text) => {
    await page.click(".cm-content");
    await page.keyboard.type(text);
  };
  const saveForm = async (anchor, fill) => {
    await page.goto(`${BASE}/settings/editor`, { waitUntil: "domcontentloaded" });
    await fill(page.locator(`form#${anchor}`));
  };

  try {
    await L.createProject(page, project);

    await run("settings: page renders defaults, five fixed profiles, fake gopls detected", async () => {
      await page.goto(`${BASE}/settings/editor`, { waitUntil: "domcontentloaded" });
      assert(await page.isChecked('input[name="mode"][value="off"]'), "mode must default to off");
      assert((await page.locator("[data-intel-profile]").count()) === 5, "expected five profile rows");
      const goRow = page.locator('[data-intel-profile="go"]');
      assert(/Found/.test(await goRow.textContent()), "fake gopls not detected");
      const pyRow = page.locator('[data-intel-profile="python"]');
      assert(/Not found/.test(await pyRow.textContent()), "pyright must be absent on this instance");
    });

    await run("settings: Flow mode + Ollama save-and-test round trip", async () => {
      await saveForm("settings-intel-completion", async (form) => {
        await form.locator('input[name="mode"][value="lsp-ai"]').check();
        await form.locator('button[type="submit"]').click();
        await page.waitForSelector("form#settings-intel-completion .alert", { timeout: 10000 });
      });
      await saveForm("settings-intel-ai", async (form) => {
        await form.locator('input[name="ollama_enabled"]').check();
        await form.locator('input[name="model"]').fill("qwen2.5-coder:7b");
        await form.locator('button[value="ai-test"]').click();
        await page.waitForSelector("form#settings-intel-ai .alert", { timeout: 10000 });
      });
      const flash = await page.textContent("form#settings-intel-ai .alert");
      assert(/ready/i.test(flash), `save and test flash missing: ${flash.slice(0, 200)}`);
      await page.goto(`${BASE}/settings/editor`, { waitUntil: "domcontentloaded" });
      assert(await page.isChecked('input[name="mode"][value="lsp-ai"]'), "mode did not persist");
      assert(await page.isChecked('input[name="ollama_enabled"]'), "ollama switch did not persist");
    });

    await run("editor: fake gopls answers in the completion popup with kind and detail", async () => {
      await openEditor();
      const intel = await page.getAttribute("dc-editor", "data-editor-intel");
      assert(/lsp-ai/.test(intel), `editor config missing mode: ${intel}`);
      await newFile(`main_${tag}.go`);
      await typeInBuffer("package main\n\nfunc demo() {\n\tInt");
      await page.waitForSelector(".cm-tooltip-autocomplete li", { timeout: 10000 });
      const labels = await page.$$eval(".cm-tooltip-autocomplete li .cm-completionLabel", (els) => els.map((e) => e.textContent));
      assert(labels.includes("IntelAlpha") && labels.includes("IntelBeta"), `popup labels: ${labels.join(",")}`);
      const details = await page.$$eval(".cm-tooltip-autocomplete li .cm-completionDetail", (els) => els.map((e) => e.textContent));
      assert(details.some((d) => /func IntelAlpha/.test(d)), `popup details: ${details.join(",")}`);
    });

    await run("editor: Tab accepts the selected completion into the buffer only", async () => {
      await page.waitForSelector('.cm-tooltip-autocomplete li[aria-selected="true"]', { timeout: 6000 });
      await sleep(150);
      await page.keyboard.press("Tab");
      await page.waitForFunction(() => !document.querySelector(".cm-tooltip-autocomplete"), null, { timeout: 4000 });
      const text = await bufferText();
      assert(text.includes("\tIntelAlpha"), `buffer after accept: ${JSON.stringify(text)}`);
      assert(!text.includes("IntelAlphaInt"), "completion must replace the typed prefix");
      await page.waitForFunction((sel) => document.querySelector(sel).classList.contains("dirty"), tabSel(`main_${tag}.go`), { timeout: 4000 });
    });

    await run("editor: ghost text appears after the pause and Tab accepts it", async () => {
      await page.keyboard.type("()\n\tdone");
      await page.waitForSelector(".cm-dc-ghost", { timeout: 10000 });
      const ghost = await page.textContent(".cm-dc-ghost");
      assert(/ghostSuggestion\(\)/.test(ghost), `ghost text: ${ghost}`);
      await page.keyboard.press("Tab");
      await page.waitForFunction(() => !document.querySelector(".cm-dc-ghost"), null, { timeout: 4000 });
      assert((await bufferText()).includes("doneghostSuggestion()"), "ghost was not inserted at the cursor");
    });

    await run("editor: Escape dismisses ghost text without touching the buffer", async () => {
      await page.keyboard.type("\n\tmore");
      await page.waitForSelector(".cm-dc-ghost", { timeout: 10000 });
      const before = await bufferText();
      await page.keyboard.press("Escape");
      await page.waitForFunction(() => !document.querySelector(".cm-dc-ghost"), null, { timeout: 4000 });
      assert((await bufferText()) === before, "Escape must not change the buffer");
      const sensitiveGone = !(await bufferText()).includes("moreghostSuggestion");
      assert(sensitiveGone, "dismissed ghost must not insert");
    });

    await run("editor: status indicator reports the connected server and active AI", async () => {
      const title = await page.getAttribute("[data-editor-intel-status]", "title");
      assert(/Go language server connected/.test(title), `indicator title: ${title}`);
      assert(/AI suggestions active/.test(title), `indicator AI title: ${title}`);
    });

    await run("editor: sensitive file is withheld from AI and says so", async () => {
      await newFile(`secrets_${tag}.txt`);
      await typeInBuffer("KEY=");
      await sleep(1500);
      assert(!(await page.$(".cm-dc-ghost")), "ghost must not appear for a sensitive file");
      await page.waitForFunction(() => /sensitive/.test(document.querySelector("[data-editor-intel-status]")?.title || ""), null, { timeout: 6000 });
    });

    await run("editor: missing language server stays quiet, editing keeps working", async () => {
      await newFile(`script_${tag}.py`);
      await typeInBuffer("Int");
      await sleep(1200);
      assert(!(await page.$(".cm-tooltip-autocomplete")), "no popup expected without a server");
      await page.waitForFunction(() => /not installed/.test(document.querySelector("[data-editor-intel-status]")?.title || ""), null, { timeout: 6000 });
      await page.keyboard.type("x = 1");
      assert((await bufferText()).includes("Intx = 1"), "typing must keep working");
    });

    await run("settings: mode off removes intelligence from the editor", async () => {
      await saveForm("settings-intel-completion", async (form) => {
        await form.locator('input[name="mode"][value="off"]').check();
        await form.locator('button[type="submit"]').click();
        await page.waitForSelector("form#settings-intel-completion .alert", { timeout: 10000 });
      });
      await openEditor();
      const intel = await page.getAttribute("dc-editor", "data-editor-intel");
      assert(/"mode":"off"/.test(intel), `expected off config: ${intel}`);
      await page.click(tabSel(`main_${tag}.go`));
      await page.click(".cm-content");
      await page.keyboard.press("End");
      await page.keyboard.type("\n\tInt");
      await sleep(1200);
      const labels = await page.$$eval(".cm-tooltip-autocomplete li .cm-completionLabel", (els) => els.map((e) => e.textContent)).catch(() => []);
      assert(!labels.includes("IntelAlpha"), "fake gopls must not answer with mode off");
      assert(!(await page.$(".cm-dc-ghost")), "no ghost with mode off");
    });
  } finally {
    await L.deleteProject(page, project).catch(() => {});
    for (const stub of stubs) stub.close();
  }
});
