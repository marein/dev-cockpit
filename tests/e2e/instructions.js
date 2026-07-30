const L = require("./lib");
const { assert, submitBtn, BASE } = L;

// Instructions: the single global instructions document. One textarea
// (name="instructions") plus hidden csrf_token. Empty content is allowed (no
// required attribute). Routes: GET /instructions, POST /instructions.
// The coder pages are settings pages: canonical under /settings/coders/<coder>,
// no Coder item in the main navigation, Settings is the active tab, and the
// settings sidebar marks the coder entry the page belongs to. The pre-settings
// /coders/<coder>/... paths 308-redirect there.

L.runFeature("INSTRUCTIONS", async ({ page, run }) => {
  const tag = `ins-${Date.now().toString(36)}`;
  try {
    await run("coder pages sit under settings: URL, main nav, sidebar", async () => {
      await page.goto(`${BASE}/instructions`, { waitUntil: "domcontentloaded" });
      const canonical = new URL(page.url()).pathname;
      assert(/^\/settings\/coders\/\w+\/instructions$/.test(canonical), `not canonical under settings: ${canonical}`);
      assert(!(await page.$('.navbar-nav a[href$="/instructions"]')), "Coder still in the main navigation");
      assert(await page.$('.navbar-nav a[href="/settings/general"].active'), "Settings is not the active main nav tab");
      assert(await page.$('[data-settings-nav] a[href="/settings/notifications"]'), "no settings sidebar on the coder page");
      assert(await page.$("[data-settings-coder].active"), "coder entry in the settings sidebar not marked");
      assert(await page.$('[data-coder-sections] a.active[href$="/instructions"]'), "instructions section tab not marked");
      await page.goto(`${BASE}${canonical.replace("/settings", "")}`, { waitUntil: "domcontentloaded" });
      assert(new URL(page.url()).pathname === canonical, `pre-settings coder URL did not redirect: ${page.url()}`);
    });
    await run("textarea + csrf; not required; save persists; empty allowed", async () => {
      await page.goto(`${BASE}/instructions`, { waitUntil: "domcontentloaded" });
      assert(await page.$('textarea[name="instructions"]'), "no textarea");
      assert(await page.$('input[name="csrf_token"]'), "no csrf field");
      assert(!(await page.$eval('textarea[name="instructions"]', (t) => t.hasAttribute("required"))), "should not be required");
      const content = `tc-instructions ${tag}`;
      await page.fill('textarea[name="instructions"]', content);
      await Promise.all([page.waitForNavigation({ timeout: 10000 }).catch(() => {}), submitBtn(page, 'textarea[name="instructions"]').click()]);
      await page.goto(`${BASE}/instructions`, { waitUntil: "domcontentloaded" });
      assert((await page.inputValue('textarea[name="instructions"]')).includes(content), "not persisted");
    });
  } finally {
    await page.goto(`${BASE}/instructions`, { waitUntil: "domcontentloaded" }).catch(() => {});
    await page.fill('textarea[name="instructions"]', "").catch(() => {});
    await Promise.all([page.waitForNavigation({ timeout: 8000 }).catch(() => {}), submitBtn(page, 'textarea[name="instructions"]').click().catch(() => {})]);
  }
});
