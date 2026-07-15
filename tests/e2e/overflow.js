// Overflow coverage: no page scrolls horizontally at any width, and long
// unbreakable user provided names never widen the layout. Creates a project,
// shell, agent, skill, editor file and instructions all carrying a long unbreakable
// name, then checks every page that renders one across phone/tablet/desktop widths.
const { chromium } = require("playwright-core");
const L = require("./lib");
const { assert, sleep, submitBtn, confirmSwal } = L;

const VIEWPORTS = [[320, 568], [375, 667], [768, 1024], [1366, 768]];
const LN = "zz" + "o".repeat(110); // 112 chars, no spaces, within the 120 maxlength

// A page overflows when the document is wider than its client box (a horizontal
// scrollbar). Returns the offending widths per viewport, empty when clean.
async function overflowAt(page, url) {
  const bad = [];
  for (const [w, h] of VIEWPORTS) {
    await page.setViewportSize({ width: w, height: h });
    await page.goto(url, { waitUntil: "domcontentloaded" });
    await sleep(400);
    const m = await page.evaluate(() => {
      const de = document.documentElement;
      // Find the element that sticks out furthest past the viewport right edge.
      let culprit = "", maxRight = 0;
      for (const el of document.querySelectorAll("*")) {
        const r = el.getBoundingClientRect();
        if (r.width > 0 && r.right > maxRight) {
          maxRight = r.right;
          culprit = el.tagName.toLowerCase() + (el.id ? "#" + el.id : "") + (typeof el.className === "string" && el.className ? "." + el.className.trim().split(/\s+/).slice(0, 2).join(".") : "");
        }
      }
      return { sw: de.scrollWidth, cw: de.clientWidth, culprit, maxRight: Math.round(maxRight) };
    });
    if (m.sw > m.cw + 1) bad.push(`${w}px: doc ${m.sw}/${m.cw}, widest=${m.culprit} @${m.maxRight}`);
  }
  return bad;
}

(async () => {
  const browser = await chromium.launch({ args: ["--no-sandbox"] });
  const bag = { consoleErrors: [], pageErrors: [] };
  const { results, run } = L.makeRunner();
  const tag = `ovf-${Date.now().toString(36)}`;
  const project = `${LN}-${tag.slice(-4)}`.slice(0, 120);
  const agentId = `${LN}-a`.slice(0, 120);
  const skillId = `${LN}-s`.slice(0, 120);
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1366, height: 768 } });
  const page = await ctx.newPage();
  L.wirePage(page, bag);
  let shellUrl = null;

  const editorURL = `${L.BASE}/projects/${encodeURIComponent(project)}/editor`;

  try {
    await L.login(page);

    await run("overflow: seed long-named project, shell, agent, skill, file, instructions", async () => {
      await L.createProject(page, project);
      // shell + long rename
      shellUrl = await L.createShell(page, project);
      await page.waitForSelector("[data-rename-label]", { timeout: 8000 });
      await page.click("[data-rename-label]");
      await page.waitForSelector("[data-rename-input]:not(.d-none)", { timeout: 4000 });
      await page.fill("[data-rename-input]", LN);
      await page.keyboard.press("Enter");
      await sleep(800);
      // agent
      await page.goto(`${L.BASE}/agents/new`, { waitUntil: "domcontentloaded" });
      await page.fill('input[name="agent_id"]', agentId);
      await page.fill('input[name="agent_description"]', LN + " " + LN);
      await page.fill('textarea[name="agent_instructions"]', LN + LN);
      await Promise.all([page.waitForURL(/\/agents(\?coder=\w+)?$/, { timeout: 10000 }), submitBtn(page, 'input[name="agent_id"]').click()]);
      // skill
      await page.goto(`${L.BASE}/skills/new`, { waitUntil: "domcontentloaded" });
      await page.fill('input[name="skill_id"]', skillId);
      await page.fill('input[name="skill_description"]', LN + " " + LN);
      await page.fill('textarea[name="skill_instructions"]', LN + LN);
      await Promise.all([page.waitForURL(/\/skills(\?coder=\w+)?$/, { timeout: 10000 }), submitBtn(page, 'input[name="skill_id"]').click()]);
      // editor file with a long name (created via the tree's context menu, the
      // tree header only carries the refresh button)
      await page.goto(editorURL, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(".cm-editor", { state: "attached", timeout: 12000 });
      await page.waitForFunction(() => { const t = document.querySelector("[data-editor-tree]"); return t && !/Loading/.test(t.textContent); }, null, { timeout: 8000 });
      const treeBox = await page.locator("[data-editor-tree]").boundingBox();
      await page.mouse.click(treeBox.x + treeBox.width / 2, treeBox.y + treeBox.height - 12, { button: "right" });
      await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
      await page.locator(".dc-context-menu .dropdown-item", { hasText: /^New file$/ }).click();
      await page.waitForSelector(".swal2-input", { state: "visible", timeout: 4000 });
      await page.fill(".swal2-input", LN + ".txt");
      await page.click(".swal2-confirm");
      await page.waitForSelector(`.editor-file[data-path="${LN}.txt"]`, { timeout: 8000 });
      // instructions long unbroken line
      await page.goto(`${L.BASE}/instructions`, { waitUntil: "domcontentloaded" });
      await page.fill('textarea[name="instructions"]', LN + LN + LN);
      await Promise.all([page.waitForNavigation({ timeout: 10000 }).catch(() => {}), submitBtn(page, 'textarea[name="instructions"]').click()]);
    });

    const pages = {
      "projects list": `${L.BASE}/projects`,
      "project editor": editorURL,
      "shell attach header": shellUrl,
      "agents list": `${L.BASE}/agents`,
      "skills list": `${L.BASE}/skills`,
      "instructions": `${L.BASE}/instructions`,
    };
    for (const [name, url] of Object.entries(pages)) {
      await run(`overflow: no horizontal overflow, ${name} (long name)`, async () => {
        const bad = await overflowAt(page, url);
        assert(bad.length === 0, `overflow: ${bad.join("; ")}`);
      });
    }

    await run("overflow: quick nav with long project + shell names", async () => {
      const bad = [];
      for (const [w, h] of VIEWPORTS) {
        await page.setViewportSize({ width: w, height: h });
        await page.goto(`${L.BASE}/projects`, { waitUntil: "domcontentloaded" });
        await page.click(".quicknav-toggle");
        await page.waitForSelector("[data-quicknav-tabs]", { state: "visible", timeout: 6000 }).catch(() => {});
        await sleep(400);
        const m = await page.evaluate(() => ({ sw: document.documentElement.scrollWidth, cw: document.documentElement.clientWidth }));
        if (m.sw > m.cw + 1) bad.push(`${w}px: ${m.sw}/${m.cw}`);
      }
      assert(bad.length === 0, `overflow: ${bad.join("; ")}`);
    });
  } finally {
    try {
      await page.setViewportSize({ width: 1366, height: 768 });
      if (shellUrl) await L.deleteShell(page, shellUrl);
      // delete agent + skill
      for (const [base, id] of [["agents", agentId], ["skills", skillId]]) {
        await page.goto(`${L.BASE}/${base}`, { waitUntil: "domcontentloaded" }).catch(() => {});
        const f = await page.$(`form[action$="/${base}/${id}/delete"]`);
        if (f) { await (await f.$("button, input[type=submit]")).click().catch(() => {}); await confirmSwal(page).catch(() => {}); await sleep(500); }
      }
      // reset instructions
      await page.goto(`${L.BASE}/instructions`, { waitUntil: "domcontentloaded" }).catch(() => {});
      await page.fill('textarea[name="instructions"]', "").catch(() => {});
      await Promise.all([page.waitForNavigation({ timeout: 8000 }).catch(() => {}), submitBtn(page, 'textarea[name="instructions"]').click().catch(() => {})]);
      await L.deleteProject(page, project);
    } catch (e) { console.log("cleanup note:", e.message); }
  }

  const anyFail = L.report("OVERFLOW", results, bag);
  await ctx.close();
  await browser.close();
  process.exit(anyFail ? 1 : 0);
})();
