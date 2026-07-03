// Complete self update coverage. Point BASE_URL at a throwaway started with
// DEV_COCKPIT_UPDATE_API_URL. MODE=available (default) expects a stub advertising a
// higher version with real assets, and drives check -> daily auto modal -> badge ->
// changelog dialog -> real (non destructive) apply. MODE=uptodate expects a stub
// returning [] and checks the no-update state. Apply re-execs into the same repackaged binary, so the
// instance restarts healthy without a real version change.
const { chromium } = require("playwright-core");
const L = require("./lib");
const { assert, sleep } = L;
const MODE = process.env.MODE || "available";

async function pollHealth(ctx, timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try { const r = await ctx.request.get(`${L.BASE}/health`, { timeout: 3000 }); if (r.status() === 200) return true; } catch {}
    await sleep(1000);
  }
  return false;
}

(async () => {
  const browser = await chromium.launch({ args: ["--no-sandbox"] });
  const bag = { consoleErrors: [], pageErrors: [] };
  const { results, run } = L.makeRunner();
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1360, height: 900 } });
  const page = await ctx.newPage();
  L.wirePage(page, bag);

  try {
    await L.login(page);
    await page.goto(`${L.BASE}/projects`, { waitUntil: "domcontentloaded" });

    await run("update: check endpoint returns the status shape", async () => {
      const res = await ctx.request.get(`${L.BASE}/update/check?force=1`);
      assert(res.status() === 200, `status ${res.status()}`);
      const j = await res.json();
      for (const k of ["supported", "current", "latest", "available", "writable", "releases"]) assert(k in j, `missing key ${k}`);
      if (MODE === "available") assert(j.available === true && j.latest, `expected available update, got ${JSON.stringify(j)}`);
      else assert(j.available === false, `expected no update, got available=${j.available}`);
      return `available=${j.available} latest=${j.latest} writable=${j.writable}`;
    });

    // The footer link is always present (it is the "Up to date" / "Update to X"
    // indicator); the header .js-update-flag badge is what toggles on availability.
    const footerState = () => page.evaluate(() => ({
      badge: [...document.querySelectorAll(".js-update-flag")].some((f) => !f.classList.contains("d-none")),
      text: (document.querySelector("dc-update-check a[data-update-open]") || {}).textContent || "",
    }));

    if (MODE === "uptodate") {
      await run("update: no update -> badge hidden + footer 'Up to date'", async () => {
        await page.goto(`${L.BASE}/projects`, { waitUntil: "domcontentloaded" });
        let st = { badge: true, text: "" };
        for (let i = 0; i < 20; i++) { st = await footerState(); if (!st.badge && /up to date/i.test(st.text)) break; await sleep(400); }
        assert(!st.badge, "badge visible with no update");
        assert(/up to date/i.test(st.text), `footer link text: '${st.text}'`);
        const open = await page.evaluate(() => Boolean(document.querySelector(".swal2-container")));
        assert(!open, "unexpected auto modal with no update");
      });
    } else {
      await run("update: available -> daily modal auto-opens once, not again within a day", async () => {
        await page.evaluate(() => localStorage.removeItem("dcUpdate"));
        await page.goto(`${L.BASE}/projects`, { waitUntil: "domcontentloaded" });
        await page.waitForSelector(".swal2-title", { state: "visible", timeout: 10000 });
        const title = await page.textContent(".swal2-title");
        assert(/Update to/i.test(title), `auto modal title: ${title}`);
        const st = await page.evaluate(() => JSON.parse(localStorage.getItem("dcUpdate") || "{}"));
        assert((st.prompted || 0) > 0, "prompted timestamp not saved");
        assert(st.promptedVersion, "promptedVersion not saved");
        await page.click(".swal2-cancel").catch(() => page.keyboard.press("Escape"));
        await sleep(400);
        await page.goto(`${L.BASE}/projects`, { waitUntil: "domcontentloaded" });
        await sleep(2500);
        const reopened = await page.evaluate(() => Boolean(document.querySelector(".swal2-container")));
        assert(!reopened, "modal reopened within a day");
        return `title='${title}' promptedVersion=${st.promptedVersion}`;
      });

      await run("update: newer version than last prompted -> modal reopens despite daily gate", async () => {
        await page.evaluate(() => {
          const st = JSON.parse(localStorage.getItem("dcUpdate") || "{}");
          st.promptedVersion = "998.0.0";
          localStorage.setItem("dcUpdate", JSON.stringify(st));
        });
        await page.goto(`${L.BASE}/projects`, { waitUntil: "domcontentloaded" });
        await page.waitForSelector(".swal2-title", { state: "visible", timeout: 10000 });
        const title = await page.textContent(".swal2-title");
        assert(/Update to/i.test(title), `modal title: ${title}`);
        const version = await page.evaluate(() => (JSON.parse(localStorage.getItem("dcUpdate") || "{}").promptedVersion || ""));
        assert(version && version !== "998.0.0", `promptedVersion not advanced: ${version}`);
        await page.click(".swal2-cancel").catch(() => page.keyboard.press("Escape"));
        await sleep(400);
      });

      await run("update: higher version -> badge shown + footer 'Update to' link", async () => {
        await page.goto(`${L.BASE}/projects`, { waitUntil: "domcontentloaded" });
        let st = { badge: false, text: "" };
        for (let i = 0; i < 25; i++) { st = await footerState(); if (st.badge && /update to/i.test(st.text)) break; await sleep(400); }
        assert(st.badge, "badge stayed hidden");
        assert(/update to/i.test(st.text), `footer link text: '${st.text}'`);
      });

      await run("update: changelog dialog shows version + notes + apply button", async () => {
        await page.locator("dc-update-check a[data-update-open]").first().click();
        await page.waitForFunction(
          () => /update to/i.test((document.querySelector(".swal2-title") || {}).textContent || ""),
          null,
          { timeout: 8000 },
        );
        const title = await page.textContent(".swal2-title");
        assert(/Update to/i.test(title), `dialog title: ${title}`);
        const bodyText = await page.evaluate(() => (document.querySelector(".swal2-html-container") || {}).textContent || "");
        assert(/Stub release/i.test(bodyText), `changelog notes missing: ${bodyText.slice(0, 80)}`);
        const confirm = await page.textContent(".swal2-confirm");
        assert(/Update & restart|Update/i.test(confirm), `confirm label: ${confirm}`);
        await page.click(".swal2-cancel").catch(() => page.keyboard.press("Escape"));
        await sleep(400);
      });

      await run("update: apply downloads, verifies, swaps, re-execs, comes back healthy", async () => {
        const token = await page.evaluate(() => { const m = document.querySelector('meta[name="csrf-token"]'); return m ? m.content : ""; });
        assert(token, "no csrf token");
        // The handler answers 200 {restarting:true} then re-execs ~300ms later,
        // which resets this connection, so tolerate a read error and prove success
        // by the process coming back healthy. A real apply failure would be a 502.
        let applyStatus = 0;
        try {
          const res = await ctx.request.post(`${L.BASE}/update/apply`, { headers: { "X-CSRF-Token": token, Accept: "application/json" }, timeout: 60000, failOnStatusCode: false });
          applyStatus = res.status();
        } catch (e) { applyStatus = -1; }
        assert(applyStatus === 200 || applyStatus === -1, `apply failed with status ${applyStatus}`);
        await sleep(1500);
        const back = await pollHealth(ctx, 45000);
        assert(back, `instance did not come back healthy after apply/re-exec (apply status ${applyStatus})`);
        const chk = await ctx.request.get(`${L.BASE}/update/check`);
        assert(chk.status() === 200, `post-restart check ${chk.status()}`);
      });
    }
  } finally { /* no app resources created */ }

  const anyFail = L.report(`UPDATE-${MODE.toUpperCase()}`, results, bag);
  await ctx.close();
  await browser.close();
  process.exit(anyFail ? 1 : 0);
})();
