const fs = require("node:fs");
const path = require("node:path");
const L = require("./lib");
const { assert, sleep, BASE } = L;

// Auth: login, logout, the requireAuth gate, the login rate limiter, the session
// cookie, and the login CSRF + safe redirect. Default credentials admin / password.
// Routes: GET/POST /login, POST /logout, plus requireAuth on every page under /.
// safeRedirectPath returns / unless the value is a single-slash local path.
// runFeature auto-logs in `page`, which itself proves a valid login; logged-out
// flows use fresh contexts.
//
// Plus the passkey, which is driven by one file in the state dir
// (auth/passkey.json): the file is the whole switch, so the runner writes one
// and reloads instead of restarting anything. That needs the instance's auth
// dir mounted, otherwise those checks soft-skip:
//   -v <state-dir>/auth:/auth -e AUTH_DIR=/auth
// Routes: POST /login/passkey{,/options}, GET /settings/login and the
// registration under it. The ceremony needs a virtual authenticator over CDP,
// which only Chromium has; the WebKit pass checks that the mask is built right
// and the way to the password works. Every check removes the file it wrote, a
// leftover would decide how every other runner's login page looks.

const AUTH_DIR = process.env.AUTH_DIR || "";
const RP_ID = new URL(BASE).hostname;
const skipNoAuthDir = "skipped, AUTH_DIR not mounted";

function writePasskeyFile(value) {
  fs.writeFileSync(path.join(AUTH_DIR, "passkey.json"), JSON.stringify(value, null, 2), { mode: 0o600 });
}

function clearPasskeyFile() {
  if (!AUTH_DIR) return;
  try { fs.unlinkSync(path.join(AUTH_DIR, "passkey.json")); } catch {}
}

// A credential the browser will never be asked to answer: these checks only
// need the login page to count a passkey as registered for this host.
const inertPasskey = (rpID = RP_ID) => ({
  credentials: [{
    id: "aW5lcnQtY3JlZGVudGlhbA",
    public_key: "aW5lcnQ",
    sign_count: 0,
    rp_id: rpID,
    label: "Mask check",
    created_at: new Date().toISOString(),
  }],
});

async function passwordLogin(p) {
  await p.goto(`${BASE}/login?method=password`, { waitUntil: "domcontentloaded" });
  await p.fill('input[name="username"]', "admin");
  await p.fill('input[name="password"]', "password");
  await Promise.all([p.waitForURL(/\/projects/, { timeout: 15000 }), p.click('button[type="submit"]')]);
}

async function logout(c, p) {
  const token = await p.evaluate(() => { const m = document.querySelector('meta[name="csrf-token"]'); return m ? m.content : ""; });
  await c.request.post(`${BASE}/logout`, { headers: { "X-CSRF-Token": token }, maxRedirects: 0 }).catch(() => {});
}

L.runFeature("AUTH", async ({ engine, browser, ctx, page, run }) => {
  const fresh = async () => { const c = await browser.newContext({ ignoreHTTPSErrors: true }); return { c, p: await c.newPage() }; };
  clearPasskeyFile();

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

  await run("a placed passkey.json is on the login page at the next load", async () => {
    if (!AUTH_DIR) return skipNoAuthDir;
    const { c, p } = await fresh();
    try {
      await p.goto(`${BASE}/login`, { waitUntil: "domcontentloaded" });
      assert(await p.$('input[name="username"]'), "the password mask is not what an empty state dir shows");
      assert(!(await p.$("[data-login-passkey]")), "a passkey is offered although none is registered");
      // No restart, no unlocking step: the file is read on the next request.
      writePasskeyFile(inertPasskey());
      await p.reload({ waitUntil: "domcontentloaded" });
      assert(await p.$("dc-passkey-login [data-passkey-start]"), "the placed passkey did not reach the login page");
      assert(!(await p.$('input[name="username"]')), "the password mask is still what the page opens with");
      const back = await p.$("[data-login-password]");
      assert(back, "no link to the password");
      assert(/Sign in with a password/.test(await back.innerText()), "the way to the password is not named");
    } finally { clearPasskeyFile(); await c.close(); }
  });

  await run("?method=password renders the classic mask with the way back", async () => {
    if (!AUTH_DIR) return skipNoAuthDir;
    const { c, p } = await fresh();
    try {
      writePasskeyFile(inertPasskey());
      await p.goto(`${BASE}/login?method=password&next=%2Fprojects`, { waitUntil: "domcontentloaded" });
      assert(await p.$('input[name="username"]'), "the password mask is missing");
      assert((await p.inputValue('input[name="next"]')) === "/projects", "next did not survive the switch");
      const back = await p.$("[data-login-passkey]");
      assert(back, "no link back to the passkey");
      assert(/next=%2Fprojects/.test(await back.getAttribute("href")), "the link back drops next");
    } finally { clearPasskeyFile(); await c.close(); }
  });

  // A passkey belongs to the host it was registered under. Under another name
  // the page has to stay on the password instead of offering a key nothing can
  // answer.
  await run("a passkey of another host leaves the login page alone", async () => {
    if (!AUTH_DIR) return skipNoAuthDir;
    const { c, p } = await fresh();
    try {
      writePasskeyFile(inertPasskey("somewhere.else.example"));
      await p.goto(`${BASE}/login`, { waitUntil: "domcontentloaded" });
      assert(await p.$('input[name="username"]'), "a foreign passkey pushed the password aside");
      assert(!(await p.$("[data-login-passkey]")), "a foreign passkey is offered as a way in");
    } finally { clearPasskeyFile(); await c.close(); }
  });

  await run("the passkey mask offers the way to the password", async () => {
    if (!AUTH_DIR) return skipNoAuthDir;
    const { c, p } = await fresh();
    try {
      writePasskeyFile(inertPasskey());
      await p.goto(`${BASE}/login`, { waitUntil: "domcontentloaded" });
      assert(await p.$("dc-passkey-login [data-passkey-start]"), "no passkey button");
      const back = await p.$("[data-login-password]");
      assert(back, "the passkey mask has no way to the password");
      await Promise.all([p.waitForURL(/method=password/, { timeout: 8000 }), back.click()]);
      await p.fill('input[name="username"]', "admin");
      await p.fill('input[name="password"]', "password");
      await Promise.all([p.waitForURL(/\/projects/, { timeout: 15000 }), p.click('button[type="submit"]')]);
      assert(/\/projects/.test(p.url()), `the password way out of the passkey mask failed: ${p.url()}`);
    } finally { clearPasskeyFile(); await c.close(); }
  });

  await run("passkey registers under /settings/login and signs in on /login", async () => {
    if (!AUTH_DIR) return skipNoAuthDir;
    if (engine !== "chromium") return "skipped, only Chromium has a virtual authenticator";
    const c = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1360, height: 900 } });
    const p = await c.newPage();
    try {
      const cdp = await c.newCDPSession(p);
      await cdp.send("WebAuthn.enable");
      const { authenticatorId } = await cdp.send("WebAuthn.addVirtualAuthenticator", {
        options: {
          protocol: "ctap2", transport: "internal", hasResidentKey: true,
          hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true,
        },
      });

      await passwordLogin(p);
      await p.goto(`${BASE}/settings/login`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(p);
      await p.waitForSelector("dc-passkey-settings [data-passkey-add]", { timeout: 10000 });
      await p.click("[data-passkey-add]");
      await p.waitForSelector(".swal2-input", { timeout: 8000 });
      await p.fill(".swal2-input", "E2E key");
      await p.click(".swal2-confirm");
      await p.waitForSelector('[data-passkey-row]:has-text("E2E key")', { timeout: 15000 });
      const held = await cdp.send("WebAuthn.getCredentials", { authenticatorId });
      assert(held.credentials.length === 1, `the authenticator holds ${held.credentials.length} credentials`);

      await logout(c, p);
      await p.goto(`${BASE}/login`, { waitUntil: "domcontentloaded" });
      await p.waitForSelector("[data-passkey-start]", { timeout: 8000 });
      await Promise.all([p.waitForURL(/\/projects/, { timeout: 20000 }), p.click("[data-passkey-start]")]);
      assert(/\/projects/.test(p.url()), `the passkey did not sign in: ${p.url()}`);

      // The counter the authenticator presented is written back, that is what
      // the next sign in is compared against.
      const stored = JSON.parse(fs.readFileSync(path.join(AUTH_DIR, "passkey.json"), "utf8"));
      assert(stored.credentials.length === 1, `the file holds ${stored.credentials.length} credentials`);
      assert(stored.credentials[0].rp_id === RP_ID, `stored under ${stored.credentials[0].rp_id}`);
      assert(stored.credentials[0].last_used_at, "the sign in left no last used stamp");

      // Both ways stay open side by side: with that same passkey registered,
      // the link under it still signs in with username and password.
      await logout(c, p);
      await p.goto(`${BASE}/login`, { waitUntil: "domcontentloaded" });
      const toPassword = await p.$("[data-login-password]");
      assert(toPassword, "the registered passkey left no way to the password");
      await Promise.all([p.waitForURL(/method=password/, { timeout: 8000 }), toPassword.click()]);
      await p.fill('input[name="username"]', "admin");
      await p.fill('input[name="password"]', "password");
      await Promise.all([p.waitForURL(/\/projects/, { timeout: 15000 }), p.click('button[type="submit"]')]);
      assert(/\/projects/.test(p.url()), `the password way failed next to a registered passkey: ${p.url()}`);

      await p.goto(`${BASE}/settings/login`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(p);
      await p.click('form[action="/settings/login/passkey/delete"] button[type="submit"]');
      await L.confirmSwal(p);
      await p.waitForSelector('[data-passkey-row]', { state: "detached", timeout: 10000 });
    } finally { clearPasskeyFile(); await c.close(); }
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
