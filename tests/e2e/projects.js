const L = require("./lib");
const { assert, sleep, submitBtn, confirmSwal, BASE } = L;

// Projects: the list with filter, sort, collapse, and the create + delete flows.
// Custom elements dc-project-list, dc-collapse-list. Routes: GET /projects,
// GET /projects/new, POST /projects, POST /projects/delete. Cards are
// .list-group-item[data-project-name] inside #project-<name>.

L.runFeature("PROJECTS", async ({ page, run }) => {
  const tag = `proj-${Date.now().toString(36)}`;
  const project = `zztc-${tag}`;
  const shellUrls = [];
  try {
    await run("base custom elements upgraded on /projects", async () => {
      const missing = await L.waitUpgraded(page, ["dc-quicknav", "dc-update-check", "dc-project-list", "dc-collapse-list"], 8000);
      assert(missing.length === 0, `not upgraded: ${missing}`);
    });

    await run("create project shows card with editor link + delete form", async () => {
      await L.createProject(page, project);
      const ok = await page.evaluate((p) => {
        const c = [...document.querySelectorAll("[data-project-name]")].find((e) => e.dataset.projectName === p);
        if (!c) return { found: false };
        const s = c.closest('[id^="project-"]') || c;
        return { found: true, editor: !!s.querySelector('a[href*="/editor"]'), del: !!s.querySelector('form[action="/projects/delete"]') };
      }, project);
      assert(ok.found && ok.editor && ok.del, `card wrong: ${JSON.stringify(ok)}`);
    });

    await run("filter hides non-matching + empty state + clear restores", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      const f = await page.$("[data-project-filter]");
      assert(f, "no filter");
      await f.fill(project); await sleep(300);
      const match = await page.evaluate((p) => { const v = [...document.querySelectorAll("[data-project-name]")].filter((c) => c.offsetParent !== null); return { visible: v.length, onlyMine: v.every((c) => c.dataset.projectName === p) }; }, project);
      assert(match.visible >= 1 && match.onlyMine, `filter wrong: ${JSON.stringify(match)}`);
      await f.fill("zzzz-no-such-xyz"); await sleep(300);
      assert(await page.evaluate(() => { const e = document.querySelector("[data-project-filter-empty]"); return e && e.offsetParent !== null; }), "no empty state");
      const clear = await page.$("[data-project-filter-clear]"); if (clear) await clear.click(); else await f.fill(""); await sleep(200);
      assert(await page.evaluate(() => [...document.querySelectorAll("[data-project-name]")].filter((c) => c.offsetParent !== null).length) >= 1, "not restored");
    });

    await run("sort toggle + option updates current + persists across reload", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await page.click("[data-project-sort-toggle]");
      await page.waitForSelector('[data-project-sort-option="alpha"]', { state: "visible", timeout: 5000 });
      await page.click('[data-project-sort-option="alpha"]'); await sleep(300);
      const cur1 = await page.textContent("[data-project-sort-current]");
      await page.reload({ waitUntil: "domcontentloaded" }); await sleep(300);
      const cur2 = await page.textContent("[data-project-sort-current]");
      assert(/alpha/i.test(cur2 || "") || cur1 === cur2, `sort not persisted '${cur1}' vs '${cur2}'`);
    });

    await run("more than 5 shells shows the collapse toggle (dc-collapse-list)", async () => {
      for (let i = 0; i < 6; i++) shellUrls.push(await L.createShell(page, project));
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      const toggle = page.locator(`[data-project-name="${project}"]`).locator('xpath=ancestor-or-self::*[starts-with(@id,"project-")]').locator("[data-collapse-toggle]").first();
      await toggle.waitFor({ state: "visible", timeout: 8000 });
      await toggle.click(); await sleep(400);
    });

    await run("delete project (data-confirm) removes the card", async () => {
      for (const u of shellUrls.splice(0)) await L.deleteShell(page, u).catch(() => {});
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      const btn = await page.evaluateHandle((p) => { const c = [...document.querySelectorAll("[data-project-name]")].find((e) => e.dataset.projectName === p); const s = c.closest('[id^="project-"]') || c; return s.querySelector('form[action="/projects/delete"] [type="submit"], form[action="/projects/delete"] button'); }, project);
      await btn.asElement().click();
      await confirmSwal(page);
      await page.waitForFunction((p) => ![...document.querySelectorAll("[data-project-name]")].some((e) => e.dataset.projectName === p), project, { timeout: 10000 });
    });
  } finally {
    for (const u of shellUrls) await L.deleteShell(page, u).catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
  }
});
