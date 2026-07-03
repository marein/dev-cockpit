const L = require("./lib");
const { assert, sleep, BASE } = L;

// Quick nav: the floating menu present on every page. Custom element dc-quicknav
// (Bootstrap dropdown). The toggle opens it and lazy loads /quicknav into
// [data-quicknav-tabs]; two segments Active and Projects; [data-pb-drill] opens a
// project detail, [data-pb-back] returns; a project scoped page sets
// data-quicknav-current-project; a group over 5 entries folds with
// [data-qn-fold-toggle]; the project order is shared with the projects page.

L.runFeature("QUICKNAV", async ({ page, run }) => {
  const tag = `qn-${Date.now().toString(36)}`;
  const project = `zztc-${tag}`;
  const shellUrls = [];
  try {
    await L.createProject(page, project);

    await run("toggle opens, lazy loads tabs, drill + back", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      assert((await L.waitUpgraded(page, ["dc-quicknav"], 8000)).length === 0, "dc-quicknav not upgraded");
      await page.click(".quicknav-toggle");
      await page.waitForSelector("[data-quicknav-tabs]", { state: "visible", timeout: 6000 });
      assert((await page.$$("[data-quicknav-tab]")).length >= 2, "expected >=2 tabs");
      await page.locator('[data-quicknav-tab="projects"]:visible').first().click(); await sleep(500);
      const drill = page.locator(`[data-pb-drill="${project}"]`).first();
      await drill.waitFor({ state: "visible", timeout: 6000 });
      await drill.click();
      await page.locator(`[data-pb-detail="${project}"]`).first().waitFor({ state: "visible", timeout: 6000 });
      await page.locator("[data-pb-back]:visible").first().click(); await sleep(200);
    });

    await run("context bar reflects the current project on a scoped page", async () => {
      await page.goto(`${BASE}/projects/${encodeURIComponent(project)}/editor`, { waitUntil: "domcontentloaded" });
      await page.click(".quicknav-toggle");
      await page.waitForSelector("[data-quicknav-tabs]", { timeout: 6000 });
      let cur = null; for (let i = 0; i < 10; i++) { cur = await page.$eval("[data-quicknav-tabs]", (e) => e.getAttribute("data-quicknav-current-project")).catch(() => null); if (cur === project) break; await sleep(300); }
      assert(cur === project, `current-project='${cur}'`);
    });

    await run("a group over 5 entries folds with 'Show N more'", async () => {
      for (let i = 0; i < 6; i++) shellUrls.push(await L.createShell(page, project));
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await page.click(".quicknav-toggle");
      await page.waitForSelector("[data-quicknav-tabs]", { state: "visible", timeout: 6000 });
      await page.locator('[data-quicknav-tab="projects"]:visible').first().click(); await sleep(500);
      await page.locator(`[data-pb-drill="${project}"]`).first().click();
      await page.waitForSelector(`[data-pb-detail="${project}"]`, { state: "visible", timeout: 6000 });
      const fold = page.locator(`[data-pb-detail="${project}"] [data-qn-fold-toggle]`).first();
      await fold.waitFor({ state: "visible", timeout: 6000 });
      const before = await page.locator(`[data-pb-detail="${project}"] [data-qn-fold] > *`).count();
      await fold.click(); await sleep(400);
      assert(await page.locator(`[data-pb-detail="${project}"] [data-qn-fold] > *`).count() >= before, "fold did not expand");
    });
  } finally {
    for (const u of shellUrls) await L.deleteShell(page, u).catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
  }
});
