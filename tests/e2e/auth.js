const L = require("./lib");
const { assert, sleep, BASE } = L;

// Auth: login, logout, the requireAuth gate, the login rate limiter, the session
// cookie, and the login CSRF + safe redirect. Default credentials admin / password.
// Routes: GET/POST /login, POST /logout, plus requireAuth on every page under /.
// safeRedirectPath returns / unless the value is a single-slash local path.
// runFeature auto-logs in `page`, which itself proves a valid login; logged-out
// flows use fresh contexts.

L.runFeature("AUTH", async ({ browser, ctx, page, run }) => {
  const fresh = async () => { const c = await browser.newContext({ ignoreHTTPSErrors: true }); return { c, p: await c.newPage() }; };

  await run("valid login landed on /projects + set tc_session cookie", async () => {
    assert(/\/projects/.test(page.url()), `url=${page.url()}`);
    const cookies = await ctx.cookies();
    const sc = cookies.find((c) => c.name === "tc_session");
    assert(sc, "no tc_session cookie");
    assert(sc.httpOnly, "cookie not HttpOnly");
    assert(sc.secure, "cookie not Secure over TLS");
    assert(/lax/i.test(sc.sameSite), `SameSite=${sc.sameSite}`);
    assert(sc.path === "/", `Path=${sc.path}`);
  });

  await run("login form has csrf, next, username, password", async () => {
    const { c, p } = await fresh();
    try {
      await p.goto(`${BASE}/login`, { waitUntil: "domcontentloaded" });
      const has = await p.evaluate(() => ["csrf_token", "next", "username", "password"].map((n) => !!document.querySelector(`input[name="${n}"]`)));
      assert(has.every(Boolean), `missing fields: ${has}`);
    } finally { await c.close(); }
  });

  await run("invalid login stays on /login, flash, not authenticated", async () => {
    const { c, p } = await fresh();
    try {
      await p.goto(`${BASE}/login`, { waitUntil: "domcontentloaded" });
      await p.fill('input[name="username"]', "admin");
      await p.fill('input[name="password"]', "wrongpass");
      await p.click('button[type="submit"]');
      await sleep(600);
      assert(/\/login/.test(p.url()), `url=${p.url()}`);
      assert(/invalid/i.test(await p.evaluate(() => document.body.innerText)), "no invalid flash");
      await p.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      assert(/\/login/.test(p.url()), "invalid login granted access");
    } finally { await c.close(); }
  });

  await run("requireAuth redirects to /login?next=/projects", async () => {
    const { c, p } = await fresh();
    try {
      await p.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      assert(/\/login\?next=%2Fprojects|\/login\?next=\/projects/.test(p.url()), `url=${p.url()}`);
      assert((await p.inputValue('input[name="next"]').catch(() => "")) === "/projects", "next input wrong");
    } finally { await c.close(); }
  });

  await run("logout clears session, protected page redirects again", async () => {
    const { c, p } = await fresh();
    try {
      await p.goto(`${BASE}/login`, { waitUntil: "domcontentloaded" });
      await p.fill('input[name="username"]', "admin"); await p.fill('input[name="password"]', "password");
      await Promise.all([p.waitForURL(/\/projects/, { timeout: 10000 }), p.click('button[type="submit"]')]);
      // /logout is an unsafe method behind the CSRF guard, so send the token.
      const token = await p.evaluate(() => { const m = document.querySelector('meta[name="csrf-token"]'); return m ? m.content : ""; });
      await c.request.post(`${BASE}/logout`, { headers: { "X-CSRF-Token": token }, maxRedirects: 0 }).catch(() => {});
      await p.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      assert(/\/login/.test(p.url()), "still authenticated after logout");
    } finally { await c.close(); }
  });

  await run("open-redirect matrix on ?next= (rendered input)", async () => {
    // Query values are pre-encoded so the server decodes the intended byte (%09 = a
    // real tab, which safeRedirectPath rejects). Do not re-encode.
    const cases = [["https%3A%2F%2Fevil.com", "/"], ["%2F%2Fevil.com", "/"], ["%2F%5Cevil.com", "/"], ["/%09/evil.com", "/"], ["%2Fprojects", "/projects"]];
    const { c, p } = await fresh();
    try {
      const bad = [];
      for (const [q, want] of cases) {
        await p.goto(`${BASE}/login?next=${q}`, { waitUntil: "domcontentloaded" });
        const got = await p.inputValue('input[name="next"]').catch(() => "<none>");
        if (got !== want) bad.push(`${q} -> '${got}' want '${want}'`);
      }
      assert(bad.length === 0, bad.join("; "));
    } finally { await c.close(); }
  });

  await run("POST login honors safe next, rejects host redirect", async () => {
    const { c, p } = await fresh();
    try {
      await p.goto(`${BASE}/login?next=${encodeURIComponent("//evil.com")}`, { waitUntil: "domcontentloaded" });
      await p.fill('input[name="username"]', "admin"); await p.fill('input[name="password"]', "password");
      await p.click('button[type="submit"]'); await sleep(800);
      assert(new URL(p.url()).host === new URL(BASE).host, `left host: ${p.url()}`);
      await c.clearCookies();
      await p.goto(`${BASE}/login?next=${encodeURIComponent("/instructions")}`, { waitUntil: "domcontentloaded" });
      await p.fill('input[name="username"]', "admin"); await p.fill('input[name="password"]', "password");
      await Promise.all([p.waitForNavigation({ timeout: 10000 }).catch(() => {}), p.click('button[type="submit"]')]);
      await sleep(300);
      assert(/\/instructions/.test(p.url()), `safe next not honored: ${p.url()}`);
    } finally { await c.close(); }
  });

  // Rate limit locks the IP ~15s; run last and wait it out so a cross-browser pass
  // does not block the next engine's login.
  await run("rate limit blocks after 3 failed logins", async () => {
    const { c, p } = await fresh();
    try {
      for (let i = 0; i < 3; i++) { await p.goto(`${BASE}/login`, { waitUntil: "domcontentloaded" }); await p.fill('input[name="username"]', "admin"); await p.fill('input[name="password"]', `bad${i}`); await p.click('button[type="submit"]'); await sleep(300); }
      await p.goto(`${BASE}/login`, { waitUntil: "domcontentloaded" }); await p.fill('input[name="username"]', "admin"); await p.fill('input[name="password"]', "badX"); await p.click('button[type="submit"]'); await sleep(500);
      assert(/second|too many|try again|wait|rate/i.test(await p.evaluate(() => document.body.innerText)), "no rate-limit message");
    } finally { await c.close(); await sleep(16000); }
  });
});
