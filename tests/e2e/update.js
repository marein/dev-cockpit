// Complete self update coverage. Point BASE_URL at a throwaway started with
// DEV_COCKPIT_UPDATE_API_URL. MODE=available (default) expects a stub advertising
// v999.0.0 whose asset is this tree built with -X main.version=999.0.0 (see the
// README), and drives check -> daily auto modal -> badge -> changelog dialog ->
// version pin (superseded version -> 409 + fresh status) -> real apply through
// the dialog, proven by current flipping to the stub version after the re-exec.
// MODE=uptodate expects a stub returning [] and checks the no-update state plus
// the 409 on apply. The page is parked on about:blank right after the apply is
// on the wire (the handler runs on a background context, the disconnect does not
// cancel it) so the restart gap does not gate the run with console noise.
const { chromium } = require("playwright-core");
const L = require("./lib");
const { assert, sleep } = L;
const MODE = process.env.MODE || "available";

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
    const csrf = () => page.evaluate(() => {
      const m = document.querySelector('meta[name="csrf-token"]');
      return m ? m.content : "";
    });

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

      await run("update: apply with no update -> 409 + status.available=false", async () => {
        const token = await csrf();
        assert(token, "no csrf token");
        const res = await ctx.request.post(`${L.BASE}/update/apply`, {
          headers: { "X-CSRF-Token": token, "Content-Type": "application/json", Accept: "application/json" },
          data: { version: "1.0.0" },
          failOnStatusCode: false,
        });
        assert(res.status() === 409, `expected 409, got ${res.status()}`);
        const j = await res.json();
        assert(j.error, "no error text in 409 body");
        assert(j.status && j.status.available === false, `expected available=false status: ${JSON.stringify(j)}`);
      });

      await run("update: apply with an empty body (released clients) stays supported", async () => {
        const token = await csrf();
        const res = await ctx.request.post(`${L.BASE}/update/apply`, {
          headers: { "X-CSRF-Token": token, Accept: "application/json" },
          failOnStatusCode: false,
        });
        assert(res.status() === 409, `expected 409, got ${res.status()}`);
        const j = await res.json();
        assert(/up to date/i.test(j.error || ""), `unexpected error text: ${j.error}`);
        assert(j.status && j.status.available === false, `expected available=false status: ${JSON.stringify(j)}`);
      });
    } else {
      await run("update: available -> daily modal auto-opens once, not again within a day", async () => {
        await page.evaluate(() => localStorage.removeItem("dc-update"));
        await page.goto(`${L.BASE}/projects`, { waitUntil: "domcontentloaded" });
        await page.waitForSelector(".swal2-title", { state: "visible", timeout: 10000 });
        const title = await page.textContent(".swal2-title");
        assert(/Update to/i.test(title), `auto modal title: ${title}`);
        const st = await page.evaluate(() => JSON.parse(localStorage.getItem("dc-update") || "{}"));
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
          const st = JSON.parse(localStorage.getItem("dc-update") || "{}");
          st.promptedVersion = "998.0.0";
          localStorage.setItem("dc-update", JSON.stringify(st));
        });
        await page.goto(`${L.BASE}/projects`, { waitUntil: "domcontentloaded" });
        await page.waitForSelector(".swal2-title", { state: "visible", timeout: 10000 });
        const title = await page.textContent(".swal2-title");
        assert(/Update to/i.test(title), `modal title: ${title}`);
        const version = await page.evaluate(() => (JSON.parse(localStorage.getItem("dc-update") || "{}").promptedVersion || ""));
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

      await run("update: apply pins the version, superseded request -> 409 + fresh status", async () => {
        const token = await csrf();
        assert(token, "no csrf token");
        const res = await ctx.request.post(`${L.BASE}/update/apply`, {
          headers: { "X-CSRF-Token": token, "Content-Type": "application/json", Accept: "application/json" },
          data: { version: "1.0.0" },
          failOnStatusCode: false,
        });
        assert(res.status() === 409, `expected 409, got ${res.status()}`);
        const j = await res.json();
        assert(j.error, "no error text in 409 body");
        assert(j.status && j.status.available === true && j.status.latest, `no fresh status in 409 body: ${JSON.stringify(j)}`);
        return `error='${j.error}' latest=${j.status.latest}`;
      });

      await run("update: apply through the dialog pins the version, swaps, re-execs as the new version", async () => {
        const before = await (await ctx.request.get(`${L.BASE}/update/check?force=1`)).json();
        assert(before.available && before.latest, `no pending update before apply: ${JSON.stringify(before)}`);
        await page.goto(`${L.BASE}/projects`, { waitUntil: "domcontentloaded" });
        await page.locator("dc-update-check a[data-update-open]").first().click();
        await page.waitForFunction(
          () => /update to/i.test((document.querySelector(".swal2-title") || {}).textContent || ""),
          null,
          { timeout: 8000 },
        );
        await page.click(".swal2-confirm");
        await page.waitForFunction(
          () => /^updating/i.test((document.querySelector(".swal2-title") || {}).textContent || ""),
          null,
          { timeout: 8000 },
        );
        await page.goto("about:blank");
        const deadline = Date.now() + 60000;
        let current = "";
        while (Date.now() < deadline && current !== before.latest) {
          try {
            const r = await ctx.request.get(`${L.BASE}/update/check`, { timeout: 3000 });
            if (r.status() === 200) current = (await r.json()).current;
          } catch {}
          await sleep(1000);
        }
        assert(current === before.latest, `instance did not come back as ${before.latest} (current=${current})`);
        return `re-execed as ${current}`;
      });
    }
  } finally { /* no app resources created */ }

  const anyFail = L.report(`UPDATE-${MODE.toUpperCase()}`, results, bag);
  await ctx.close();
  await browser.close();
  process.exit(anyFail ? 1 : 0);
})();
