const L = require("./lib");
const { assert, submitBtn, BASE } = L;

// Instructions: the single global instructions document. One textarea
// (name="instructions") plus hidden csrf_token. Empty content is allowed (no
// required attribute). Routes: GET /instructions, POST /instructions.

L.runFeature("INSTRUCTIONS", async ({ page, run }) => {
  const tag = `ins-${Date.now().toString(36)}`;
  try {
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
