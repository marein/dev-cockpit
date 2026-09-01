// Overflow coverage: no page scrolls horizontally at any width, and long
// unbreakable user provided names never widen the layout. Creates a project,
// shell, agent, skill, editor file and instructions all carrying a long unbreakable
// name, then checks every page that renders one across phone/tablet/desktop widths.
// The open burger menu is a layout of its own and gets its own pass: below md
// Tabler strips the horizontal padding off the container inside .navbar-collapse,
// so anything in there carrying negative margins (a grid row's gutters) leaves
// the viewport with nothing left to cancel it, and the page rocks sideways for
// as long as the menu stands open.
const { chromium } = require("playwright-core");
const L = require("./lib");
const { assert, sleep, submitBtn, confirmSwal } = L;

const VIEWPORTS = [[320, 568], [375, 667], [768, 1024], [1366, 768]];
const LN = "zz" + "o".repeat(110); // 112 chars, no spaces, within the 120 maxlength
const NEEDLE = "needlehaystack";
const EDITOR_VIEWPORTS = [[390, 844], [1366, 768]];
// The header that carries the toggler is d-md-none, so the open menu only
// exists below md and only these widths can be measured with it.
const MENU_VIEWPORTS = [[320, 568], [375, 667], [390, 844]];

// treeMenu opens the file tree's context menu, on a row when one is named and
// on the tree's empty area below the rows otherwise, and picks an entry.
async function treeMenu(page, rowSel, label) {
  // a dialog from the step before fades out over the tree, and its container
  // takes the right click while it does
  await page.waitForSelector(".swal2-container", { state: "detached", timeout: 6000 }).catch(() => {});
  await sleep(200);
  if (rowSel) {
    await page.click(rowSel, { button: "right" });
  } else {
    const box = await page.locator("[data-editor-tree]").boundingBox();
    await page.mouse.click(box.x + box.width / 2, box.y + box.height - 12, { button: "right" });
  }
  await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
  await page.locator(".dc-context-menu .dropdown-item", { hasText: label }).click();
  await page.waitForSelector(".swal2-input", { state: "visible", timeout: 4000 });
}

// openPalette drives the quick open palette from the editor menu. The keyboard
// shortcuts (Ctrl+O, Ctrl+Shift+F) never reach the page in headless Chromium,
// the browser keeps them for itself.
async function openPalette(page, which) {
  const backdrop = page.locator("[data-editor-backdrop]");
  if (await backdrop.isVisible().catch(() => false)) {
    await backdrop.dispatchEvent("click");
    await sleep(300);
  }
  await page.click("[data-editor-menu]");
  await sleep(300);
  await page.click(which === "search" ? "[data-editor-search-project-item]" : "[data-editor-quick-open-item]");
  await page.waitForSelector("[data-editor-quickopen]", { state: "visible", timeout: 8000 });
}

// tabsOverflow reports every tab whose content leaves the tab: a name or a path
// hint painted over the neighbour, a close control pushed out of reach, or a
// tab that carries no full path to read the truncated one from.
const tabsOverflow = () => {
  const bad = [];
  document.querySelectorAll(".editor-tab").forEach((tab) => {
    const box = tab.getBoundingClientRect();
    for (const sel of [".editor-tab-name", ".editor-tab-hint", ".editor-tab-state"]) {
      const el = tab.querySelector(sel);
      if (!el) continue;
      const kid = el.getBoundingClientRect();
      if (kid.right > box.right + 1 || kid.left < box.left - 1) bad.push(`${sel} ${Math.round(kid.left)}..${Math.round(kid.right)} outside the tab ${Math.round(box.left)}..${Math.round(box.right)}`);
    }
    const state = tab.querySelector(".editor-tab-state");
    if (!state || state.getBoundingClientRect().width < 12) bad.push("close control collapsed");
    if (tab.scrollWidth > Math.ceil(box.width) + 1) bad.push(`content ${tab.scrollWidth} wider than the tab ${Math.round(box.width)}`);
    if (!tab.title) bad.push("tab without a title");
  });
  return bad;
};

// panelOverflow reports every quick open row that leaves the panel, plus a row
// that shows a cut name without carrying the whole path as its title.
const panelOverflow = () => {
  const panel = document.querySelector(".editor-quickopen-panel");
  if (!panel) return ["no panel"];
  const box = panel.getBoundingClientRect();
  const bad = [];
  document.querySelectorAll(".editor-quickopen-item").forEach((item) => {
    item.querySelectorAll("span, div").forEach((el) => {
      const kid = el.getBoundingClientRect();
      if (kid.width > 0 && kid.right > box.right + 1) bad.push(`${el.className} right ${Math.round(kid.right)} past the panel ${Math.round(box.right)}`);
    });
    if (item.scrollWidth > item.clientWidth + 1) bad.push(`row scrolls (${item.scrollWidth}/${item.clientWidth})`);
    if (!item.title) bad.push("row without a title");
  });
  return bad;
};

// docOverflow reports the document against its client box (wider means a
// horizontal scrollbar), naming the element that sticks out furthest past the
// viewport's right edge, which is the one to repair.
const docOverflow = () => {
  const de = document.documentElement;
  let culprit = "", maxRight = 0;
  for (const el of document.querySelectorAll("*")) {
    const r = el.getBoundingClientRect();
    if (r.width > 0 && r.right > maxRight) {
      maxRight = r.right;
      culprit = el.tagName.toLowerCase() + (el.id ? "#" + el.id : "") + (typeof el.className === "string" && el.className ? "." + el.className.trim().split(/\s+/).slice(0, 2).join(".") : "");
    }
  }
  return { sw: de.scrollWidth, cw: de.clientWidth, culprit, maxRight: Math.round(maxRight) };
};

// menuClipped reports what the open menu loses at the viewport's edges: the menu
// itself hanging over one of them, a scrollbar of its own, or an entry painted
// outside or squeezed to nothing. Overflow that is merely hidden fails here too,
// which is the point: a menu nobody can read fully is not a fix.
const menuClipped = () => {
  const menu = document.getElementById("navbar-menu");
  if (!menu || !menu.classList.contains("show")) return ["the menu is not open"];
  const cw = document.documentElement.clientWidth;
  const bad = [];
  const box = menu.getBoundingClientRect();
  if (box.left < -0.5 || box.right > cw + 0.5) bad.push(`the menu spans ${Math.round(box.left)}..${Math.round(box.right)} in a ${cw} viewport`);
  if (menu.scrollWidth > menu.clientWidth + 1) bad.push(`the menu scrolls sideways (${menu.scrollWidth}/${menu.clientWidth})`);
  menu.querySelectorAll(".nav-link, [data-update-open]").forEach((el) => {
    const r = el.getBoundingClientRect();
    if (!r.width && !r.height) return; // entries this page keeps hidden
    const name = (typeof el.className === "string" ? el.className : "").trim().split(/\s+/).slice(0, 2).join(".") || el.tagName.toLowerCase();
    if (r.left < -0.5 || r.right > cw + 0.5) bad.push(`${name} ${Math.round(r.left)}..${Math.round(r.right)} outside the ${cw} viewport`);
    if (r.width < 24) bad.push(`${name} squeezed to ${Math.round(r.width)}px`);
  });
  return bad;
};

// A page overflows when the document is wider than its client box (a horizontal
// scrollbar). Returns the offending widths per viewport, empty when clean.
async function overflowAt(page, url) {
  const bad = [];
  for (const [w, h] of VIEWPORTS) {
    await page.setViewportSize({ width: w, height: h });
    await page.goto(url, { waitUntil: "domcontentloaded" });
    await sleep(400);
    const m = await page.evaluate(docOverflow);
    if (m.sw > m.cw + 1) bad.push(`${w}px: doc ${m.sw}/${m.cw}, widest=${m.culprit} @${m.maxRight}`);
  }
  return bad;
}

// menuOverflow works one page through the burger menu at every width the menu
// exists at: open it, measure, close it, measure again. Neither state may scroll
// the page sideways, and the open menu has to stay inside the viewport.
async function menuOverflow(page, url) {
  const bad = [];
  for (const [w, h] of MENU_VIEWPORTS) {
    await page.setViewportSize({ width: w, height: h });
    await page.goto(url, { waitUntil: "domcontentloaded" });
    await sleep(600);
    const toggler = page.locator(".navbar-toggler").first();
    if (!await toggler.isVisible().catch(() => false)) {
      bad.push(`${w}px: no navbar toggler, nothing opens here`);
      continue;
    }
    for (const open of [true, false]) {
      await toggler.click();
      // the collapse animates, and a measurement taken while it runs is a
      // measurement of a width the menu never rests at
      await page.waitForFunction((want) => {
        const menu = document.getElementById("navbar-menu");
        return menu && !menu.classList.contains("collapsing") && menu.classList.contains("show") === want;
      }, open, { timeout: 8000 });
      await sleep(300);
      const state = open ? "open" : "closed again";
      const m = await page.evaluate(docOverflow);
      if (m.sw > m.cw + 1) bad.push(`${w}px ${state}: doc ${m.sw}/${m.cw}, widest=${m.culprit} @${m.maxRight}`);
      if (open) (await page.evaluate(menuClipped)).forEach((b) => bad.push(`${w}px open: ${b}`));
    }
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
      // a repository on a branch carrying the same long name: the branch chip is
      // the projects list's own unbreakable string, and it sits in the row that
      // the open menu covers. The shell writes it, the app never runs git itself,
      // and no commit is needed, the branch is read straight out of .git/HEAD.
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
      await sleep(1500);
      await page.evaluate(([href, cmd]) => {
        const token = document.querySelector('meta[name="csrf-token"]').content;
        return fetch(`${href}/input`, {
          method: "POST",
          headers: { "Content-Type": "application/json", "X-CSRF-Token": token },
          body: JSON.stringify({ items: [{ raw: cmd }] }),
        });
      }, [new URL(shellUrl).pathname, `git init -q && git symbolic-ref HEAD refs/heads/${LN}\r`]);
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
      await treeMenu(page, null, /^New file$/);
      await page.fill(".swal2-input", LN + ".txt");
      await page.click(".swal2-confirm");
      await page.waitForSelector(`.editor-file[data-path="${LN}.txt"]`, { timeout: 8000 });
      // a folder with a long name holding a file of the same name as the one
      // above: two tabs sharing a name is what puts the parent directory into
      // the tab as a hint, the second thing in the strip that can overflow it
      await treeMenu(page, null, /^New folder$/);
      await page.fill(".swal2-input", LN);
      await page.click(".swal2-confirm");
      await page.waitForSelector(`.editor-item[data-path="${LN}"]`, { timeout: 8000 });
      await treeMenu(page, `.editor-item[data-path="${LN}"]`, /^New file$/);
      await page.fill(".swal2-input", LN + ".txt");
      await page.click(".swal2-confirm");
      await page.waitForSelector(`.editor-tab[data-path="${LN}/${LN}.txt"]`, { timeout: 8000 });
      // content for the find in files check, saved through the editor itself
      await page.waitForSelector(".swal2-container", { state: "detached", timeout: 6000 }).catch(() => {});
      await page.click(".cm-content");
      await page.keyboard.type(NEEDLE);
      await page.waitForSelector("[data-editor-save]:not([disabled])", { timeout: 8000 });
      await page.click("[data-editor-save]");
      await page.waitForSelector("[data-editor-save][disabled]", { timeout: 8000 });
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

    await run("overflow: the projects list carries the long branch name", async () => {
      await page.setViewportSize({ width: 375, height: 667 });
      const deadline = Date.now() + 30000;
      let branch = "";
      while (Date.now() < deadline) {
        await page.goto(`${L.BASE}/projects`, { waitUntil: "domcontentloaded" });
        branch = await page.locator(`[id="project-${project}"] .ti-git-branch`).first()
          .evaluate((el) => el.parentElement.textContent.trim()).catch(() => "");
        if (branch.includes(LN)) break;
        await sleep(1500);
      }
      assert(branch.includes(LN), `the row shows no long branch (${branch.slice(0, 40)}), the case is not covered`);
    });

    const menuPages = {
      "projects list": `${L.BASE}/projects`,
      "shell attach": shellUrl,
      "project editor": editorURL,
    };
    for (const [name, url] of Object.entries(menuPages)) {
      await run(`overflow: opening and closing the navigation moves nothing sideways, ${name} (long name)`, async () => {
        const bad = await menuOverflow(page, url);
        assert(bad.length === 0, `menu: ${bad.join("; ")}`);
      });
    }

    await run("overflow: editor tab strip truncates a long name and a long path hint", async () => {
      const bad = [];
      for (const [w, h] of EDITOR_VIEWPORTS) {
        await page.setViewportSize({ width: w, height: h });
        await page.goto(editorURL, { waitUntil: "domcontentloaded" });
        await page.waitForSelector(".cm-editor", { state: "attached", timeout: 12000 });
        await page.waitForSelector(".editor-tab", { timeout: 8000 });
        await sleep(500);
        const strip = await page.evaluate(() => {
          const el = document.querySelector(".editor-tabs");
          return {
            overflowX: getComputedStyle(el).overflowX,
            hints: document.querySelectorAll(".editor-tab-hint").length,
            wide: [...document.querySelectorAll(".editor-tab")].filter((t) => t.getBoundingClientRect().width > 200).length,
          };
        });
        (await page.evaluate(tabsOverflow)).forEach((b) => bad.push(`${w}px: ${b}`));
        if (strip.wide) bad.push(`${w}px: ${strip.wide} tab(s) past the 11rem cap`);
        // the two files share a name, so the strip has to be showing path hints
        if (!strip.hints) bad.push(`${w}px: no path hint in the strip, the case is not covered`);
        // the strip still scrolls sideways, that is how the other tabs stay reachable
        if (!/auto|scroll/.test(strip.overflowX)) bad.push(`${w}px: tab strip overflow-x is ${strip.overflowX}`);
        // the close control is reachable, not just painted inside the tab
        await page.locator(".editor-tab").first().locator(".editor-tab-state").click({ trial: true, timeout: 4000 });
      }
      assert(bad.length === 0, `tabs: ${bad.join("; ")}`);
    });

    await run("overflow: quick open rows stay inside the panel with a long file name", async () => {
      const bad = [];
      for (const [w, h] of EDITOR_VIEWPORTS) {
        await page.setViewportSize({ width: w, height: h });
        await page.goto(editorURL, { waitUntil: "domcontentloaded" });
        await page.waitForSelector(".cm-editor", { state: "attached", timeout: 12000 });
        await sleep(500);
        await openPalette(page, "files");
        await page.fill("[data-editor-quickopen-input]", LN.slice(0, 30));
        await page.waitForSelector(".editor-quickopen-item", { timeout: 8000 });
        await sleep(300);
        (await page.evaluate(panelOverflow)).forEach((b) => bad.push(`${w}px: ${b}`));
        const titled = await page.locator(".editor-quickopen-item").first().getAttribute("title");
        if (!titled || !titled.includes(LN)) bad.push(`${w}px: row title does not carry the path (${titled})`);
      }
      assert(bad.length === 0, `quick open: ${bad.join("; ")}`);
    });

    await run("overflow: find in files match heads stay inside the panel", async () => {
      const bad = [];
      for (const [w, h] of EDITOR_VIEWPORTS) {
        await page.setViewportSize({ width: w, height: h });
        await page.goto(editorURL, { waitUntil: "domcontentloaded" });
        await page.waitForSelector(".cm-editor", { state: "attached", timeout: 12000 });
        await sleep(500);
        await openPalette(page, "search");
        await page.fill("[data-editor-quickopen-input]", NEEDLE);
        await page.waitForSelector(".editor-quickopen-match", { timeout: 12000 });
        await sleep(300);
        (await page.evaluate(panelOverflow)).forEach((b) => bad.push(`${w}px: ${b}`));
      }
      assert(bad.length === 0, `find in files: ${bad.join("; ")}`);
    });

    await run("overflow: quick nav with long project + shell names", async () => {
      const bad = [];
      for (const [w, h] of VIEWPORTS) {
        await page.setViewportSize({ width: w, height: h });
        await page.goto(`${L.BASE}/projects`, { waitUntil: "domcontentloaded" });
        // From lg up the assistant's corner button replaces the quick nav, so
        // there is no menu to open there; the width still gets measured.
        if (await page.locator(".quicknav-toggle").isVisible()) {
          await page.click(".quicknav-toggle");
          await page.waitForSelector("[data-quicknav-tabs]", { state: "visible", timeout: 6000 }).catch(() => {});
        }
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
