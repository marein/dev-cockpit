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

    await run("form path: data-confirm delete redirects, not the 403 page", async () => {
      await page.goto(shellUrl, { waitUntil: "domcontentloaded" });
      const delPath = new URL(shellUrl).pathname + "/delete";
      await page.click(`form[action="${delPath}"] button[type="submit"], form[action="${delPath}"] button`);
      await confirmSwal(page);
      await page.waitForURL((u) => !new RegExp(new URL(shellUrl).pathname + "$").test(u.toString()), { timeout: 10000 });
      shellUrl = null;
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
    if (shellUrl) await L.deleteShell(page, shellUrl).catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
  }
});
