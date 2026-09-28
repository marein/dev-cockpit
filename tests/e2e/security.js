const L = require("./lib");
const { assert, sleep, confirmSwal, BASE } = L;

// Security (cross cutting): CSRF and open redirects. The per session token is
// rendered once into <meta name="csrf-token">; @dc/http attaches it as the
// X-CSRF-Token header on every JS POST, server rendered forms keep a hidden
// csrf_token field. The server accepts either on every unsafe method. Open redirect
// matrix lives in auth.js (?next=) and repeats for ?return= on /coders/new.

L.runFeature("SECURITY", async ({ page, ctx, run }) => {
  const tag = `sec-${Date.now().toString(36)}`;
  const project = `zztc-${tag}`;
  let shellUrl = null;
  let webhook = null;
  try {
    await L.createProject(page, project);
    shellUrl = await L.createShell(page, project);

    await run("header path: rename through @dc/http persists (a 403 would not)", async () => {
      const name = `sec-${tag.slice(-5)}`;
      await page.click("[data-rename-label]");
      await page.waitForSelector("[data-rename-input]:not(.d-none)", { timeout: 4000 });
      await page.fill("[data-rename-input]", name); await page.keyboard.press("Enter"); await sleep(800);
      await page.reload({ waitUntil: "domcontentloaded" });
      assert((await page.textContent("[data-rename-label]")).trim() === name, "rename did not persist (header CSRF path broke)");
    });

    // The shell's own page carries no delete form any more (the work head holds
    // no stop or delete), so the form path runs on the webhook list: a plain
    // server rendered form with the hidden csrf_token field, confirmed through
    // data-confirm and submitted by pe.js, whose request carries no header.
    await run("form path: data-confirm delete redirects, not the 403 page", async () => {
      const hook = `http://127.0.0.1:9/${tag}`;
      const row = page.locator(`dc-push-settings .list-group-item:has-text("${hook}")`);
      await page.goto(`${BASE}/settings/notifications`, { waitUntil: "domcontentloaded" });
      await page.fill('dc-push-settings input[name="url"]', hook);
      await Promise.all([
        page.waitForURL(/\/settings\/notifications(#[a-z-]+)?$/, { timeout: 10000 }),
        page.locator('form:has(input[name="url"]) button[type="submit"]').click(),
      ]);
      await row.waitFor({ state: "visible", timeout: 6000 });
      webhook = hook;
      const posted = page.waitForResponse((r) => r.request().method() === "POST" && new URL(r.url()).pathname === "/settings/notifications", { timeout: 10000 });
      await row.locator('form[data-confirm] button[type="submit"]').click();
      await confirmSwal(page);
      const res = await posted;
      assert(res.status() !== 403, "the confirmed form post was refused as a CSRF failure");
      assert(!res.request().headers()["x-csrf-token"], "the form path sent the header, so the hidden field went unproven");
      await row.waitFor({ state: "detached", timeout: 6000 });
      assert(/\/settings\/notifications(#[a-z-]+)?$/.test(page.url()), `landed on ${page.url()}`);
      assert(await page.locator("text=Forbidden").count() === 0, "the 403 page is showing");
      webhook = null;
    });

    await run("negative: wrong CSRF token -> 403", async () => {
      const res = await ctx.request.post(`${BASE}/instructions`, { form: { csrf_token: "definitely-wrong", instructions: "x" }, headers: { "X-CSRF-Token": "definitely-wrong" }, maxRedirects: 0 });
      assert(res.status() === 403, `expected 403, got ${res.status()}`);
    });

    await run("negative: empty CSRF token -> 403", async () => {
      const res = await ctx.request.post(`${BASE}/instructions`, { form: { csrf_token: "", instructions: "x" }, maxRedirects: 0 });
      assert(res.status() === 403, `expected 403, got ${res.status()}`);
    });
  } finally {
    if (webhook) {
      await page.goto(`${BASE}/settings/notifications`, { waitUntil: "domcontentloaded" }).catch(() => {});
      await page.evaluate(async (hook) => {
        const token = document.querySelector('meta[name="csrf-token"]').content;
        for (const marker of document.querySelectorAll('input[name="form"][value="webhook-remove"]')) {
          if (!marker.closest(".list-group-item")?.textContent.includes(hook)) continue;
          const id = marker.parentElement.querySelector('input[name="id"]').value;
          await fetch("/settings/notifications", { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded", "X-CSRF-Token": token }, body: "form=webhook-remove&id=" + encodeURIComponent(id) });
        }
      }, webhook).catch(() => {});
    }
    if (shellUrl) await L.deleteShell(page, shellUrl).catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
  }
});
