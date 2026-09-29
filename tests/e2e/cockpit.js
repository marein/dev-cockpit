#!/usr/bin/env node
const fs = require("fs");
const L = require("./lib");
const { assert, sleep, BASE } = L;

// The phone's Cockpit sheet, the tab labelled Cockpit: the last entry of the
// tab bar, in the place Settings had. Its icon is the app's gauge
// (ti-dashboard in a dc-host-status painted from the `host` event), the tab's
// own color while every reading is below 80, yellow from 80 and red from 95 by
// the worst of CPU, RAM and disk, labelled with that state while there is one,
// and it wears one plain dot, no number ([data-cockpit-dot]), while a
// notification is unread or a backup review is open: rendered by the server on
// every page and kept live by the notification channel from the
// `notifications` and `backupreviews` events. It opens the list sheet on the
// area `cockpit` (GET /ctx/cockpit), a dialog labelled Cockpit, as tall as
// every other list sheet, that takes the focus and gives it back to the tab:
// the server's CPU, RAM and disk in one row (read only, the plain numbers in
// the tooltip), the three newest notifications one line each with Show all
// (the area `news`, labelled Notifications, every entry and Mark all read),
// the theme, update and logout tiles (one height, the theme tile a square),
// then the settings sections, the Settings head counting the open backup
// reviews, and the docs. Opening it leaves the page's tab marked. A phone
// carries no bell, theme, update or logout in its work head any more.
//
// Gotchas:
// - the numbers are the real machine's, so the bars are driven by the `dc:host`
//   DOM event and the count by `dc:notifications`, the events @dc/events
//   re-dispatches; nothing asserts a live value.
// - the news list is read from GET /notifications, which the runner answers
//   itself (page.route) with five entries, so "the newest three" is checkable
//   on an instance that has no news.
// - the dot checks need the instance's state directory mounted
//   (`-v <state-dir>:/state -e STATE_DIR=/state`): they drop a real coder
//   signal into its notification inbox and write an open review into its
//   import-review.json, and soft skip without it. A review nobody can resolve
//   through the page (no pre-import copy) is resolved through the page's own
//   review-keep post, which is what publishes the event.
// - the update tile checks answer /update/check themselves (page.route), with
//   the prompt for that version marked as shown, so no dialog pops on load.
// - logout ends the phone's session, so it runs last.
// - SHOTS_DIR saves the tab bar and the open sheet side by side as
//   control-center.png.

const COCKPIT = '.dc-tabbar button[data-ctx-area="cockpit"]';
const SHEET = "dc-ctx-sheet:not([hidden])";
const TONES = ["bg-secondary", "bg-yellow", "bg-red", "bg-green"];

const STATE_DIR = process.env.STATE_DIR || "";

const fire = (page, type, detail) => page.evaluate(([t, d]) => document.dispatchEvent(new CustomEvent(`dc:${t}`, { detail: d })), [type, detail]);

// The tab's name carries the server's state next to the news, and the real
// machine's host beat may put a state there any moment, so a check about the
// news alone accepts one.
const withNews = (name) => /^Cockpit(, server (busy|critical))?, news$/.test(name || "");
const withoutNews = (name) => name === null || /^Cockpit, server (busy|critical)$/.test(name);

const tabIcon = (page) => page.$eval(`${COCKPIT} [data-host-worst-icon]`, (icon) => {
  const rgb = (name) => `rgb(${getComputedStyle(document.body).getPropertyValue(`--tblr-${name}-rgb`).trim().replace(/\s*,\s*/g, ", ")})`;
  const color = getComputedStyle(icon).color;
  return {
    gauge: icon.classList.contains("ti-dashboard"),
    shown: icon.getClientRects().length > 0,
    tone: icon.classList.contains("text-red") ? "red" : icon.classList.contains("text-yellow") ? "yellow" : color === getComputedStyle(icon.closest("button")).color ? "tab" : color,
    tabColor: color === getComputedStyle(icon.closest("button")).color,
    active: color === rgb("primary"),
    label: icon.getAttribute("title"),
    name: icon.closest("button").getAttribute("aria-label"),
    hidden: icon.getAttribute("aria-hidden") === "true",
    bars: icon.closest("button").querySelectorAll("[data-host-meter]").length,
  };
});

const WORDS = [
  ["Job done.", "editor-nits: the review comments are fixed"],
  ["Coder has news.", "editor-nits-review"],
  ["Compose finished.", "start"],
  ["Command finished.", "make test"],
  ["Backup ready.", "dev-cockpit-backup.tar.gz"],
];
const FAKE = WORDS.map(([title, detail], i) => ({
  id: `cockpit-fake-${i + 1}`,
  targetId: `cockpit-target-${i + 1}`,
  targetName: `target ${i + 1}`,
  title,
  detail,
  createdAt: new Date(Date.now() - (i + 1) * 60000).toISOString(),
  read: i >= 2,
  url: "/settings/general",
}));

async function openCockpit(mp) {
  await mp.tap(COCKPIT);
  await mp.waitForSelector(`${SHEET} [data-cockpit]`, { timeout: 8000 });
}

async function closeSheet(mp) {
  await mp.keyboard.press("Escape");
  await mp.waitForFunction(() => document.querySelector("dc-ctx-sheet").hidden, null, { timeout: 4000 });
}

L.runFeature("COCKPIT", async ({ run, mobilePage }) => {
  const mp = await mobilePage();
  await mp.route("**/notifications", (route) => {
    if (route.request().method() !== "GET") return route.continue();
    return route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ notifications: FAKE, unread: 2 }) });
  });

  await run("phone: the work head carries no bell, theme, update or logout", async () => {
    const bad = [];
    for (const path of ["/projects", "/settings/general", "/docs", "/assistants"]) {
      await mp.goto(`${BASE}${path}`, { waitUntil: "domcontentloaded" });
      await mp.waitForSelector(COCKPIT, { timeout: 8000 });
      const found = await mp.evaluate(() => {
        const heads = [...document.querySelectorAll(".dc-work-head")];
        const hits = [];
        for (const sel of [".dc-notify-bell", "dc-notifications", "[data-theme-cycle]", "[data-update-open]", 'form[action="/logout"]', "dc-host-status", ".dc-head-tools"]) {
          if (heads.some((h) => h.querySelector(sel))) hits.push(sel);
        }
        const bells = [...document.querySelectorAll(".dc-notify-bell")].filter((el) => el.getClientRects().length > 0).length;
        if (bells) hits.push(`${bells} visible bell(s)`);
        return hits;
      });
      if (found.length) bad.push(`${path}: ${found.join(", ")}`);
    }
    assert(bad.length === 0, bad.join("; "));
  });

  await run("phone: the tab bar ends in Cockpit, Settings is gone, Cockpit stands on the settings pages", async () => {
    await mp.goto(`${BASE}/settings/general`, { waitUntil: "domcontentloaded" });
    await mp.waitForSelector(COCKPIT, { timeout: 8000 });
    const tabs = await mp.$$eval(".dc-tabbar .dc-tabbar-btn", (els) => els.map((el) => ({
      label: el.textContent.trim(),
      area: el.dataset.ctxArea || el.getAttribute("href"),
      active: el.classList.contains("active"),
    })));
    const labels = tabs.map((t) => t.area).join(",");
    assert(labels === "projects,terminals,/editor,assistants,cockpit", `tab bar ${labels}`);
    assert(tabs[4].active, "Cockpit does not stand active on a settings page");
    assert(tabs[4].label === "Cockpit", `the last tab reads ${tabs[4].label}`);
    assert(!(await mp.$('.dc-tabbar [data-ctx-area="settings"]')), "the Settings tab is still there");
    const extra = await mp.$$eval(`${COCKPIT} .js-update-flag, ${COCKPIT} [data-notify-any], ${COCKPIT} [data-backup-reviews], ${COCKPIT} .badge`, (els) => els.length);
    assert(extra === 0, "the Cockpit tab carries an update flag, an area dot or a number");
    assert((await mp.$$(`${COCKPIT} [data-cockpit-dot]`)).length === 1, "the Cockpit tab carries no dot of its own");
  });

  await run("phone: Cockpit stands active on /docs, loaded and reached through its sheet", async () => {
    const actives = () => mp.$$eval(".dc-tabbar .dc-tabbar-btn.active", (els) => els.map((el) => el.dataset.ctxArea || el.getAttribute("href")));
    await mp.goto(`${BASE}/docs`, { waitUntil: "domcontentloaded" });
    await mp.waitForSelector(COCKPIT, { timeout: 8000 });
    let got = await actives();
    assert(got.join(",") === "cockpit", `on a loaded /docs the active tabs are ${JSON.stringify(got)}`);
    await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await mp.waitForSelector(COCKPIT, { timeout: 8000 });
    await openCockpit(mp);
    await mp.tap(`${SHEET} a[href="/docs"]`);
    await mp.waitForURL(/\/docs$/, { timeout: 8000 });
    await mp.waitForFunction(() => document.querySelector("dc-ctx-sheet").hidden, null, { timeout: 4000 });
    await mp.waitForFunction(() => document.querySelector('.dc-tabbar button[data-ctx-area="cockpit"]').classList.contains("active"), null, { timeout: 4000 });
    got = await actives();
    assert(got.join(",") === "cockpit", `on /docs through the sheet the active tabs are ${JSON.stringify(got)}`);
  });

  await run("phone: the Cockpit sheet marks and centers the entry of the page on screen, the tab stands active with it", async () => {
    const state = () => mp.$eval(SHEET, (sheet) => {
      const rows = [...sheet.querySelectorAll("[data-cockpit-section=settings] .list-group-item.active")];
      const body = sheet.querySelector(".dc-ctx-body");
      const b = body.getBoundingClientRect();
      const r = rows[0]?.getBoundingClientRect();
      return {
        active: rows.map((row) => row.dataset.settingsCoder || row.getAttribute("href")),
        top: body.scrollTop,
        centered: !r || Math.abs((r.top + r.bottom) / 2 - (b.top + b.bottom) / 2) <= 3
          || (body.scrollTop >= body.scrollHeight - body.clientHeight - 1 && r.top >= b.top - 1 && r.bottom <= b.bottom + 1),
        room: body.scrollHeight - body.scrollTop - (sheet.querySelector("[data-cockpit]").getBoundingClientRect().bottom - b.top) + parseFloat(getComputedStyle(body).paddingBottom),
        tab: document.querySelector('.dc-tabbar button[data-ctx-area="cockpit"]').classList.contains("active"),
        rowTop: r ? Math.round(r.top) : null,
      };
    });
    // Safari has no scroll anchoring, so news landing above the entry after
    // the open would push it down there. Chromium is made to behave the same,
    // the news is held back and the sheet opened a second time: the body
    // appears with the news in place and nothing moves afterwards.
    const late = async (route) => {
      if (route.request().method() === "GET") await sleep(800);
      return route.fallback();
    };
    await mp.route("**/notifications", late);
    const openOn = async (path) => {
      await mp.goto(`${BASE}${path}`, { waitUntil: "domcontentloaded" });
      await mp.waitForSelector(COCKPIT, { timeout: 8000 });
      await mp.addStyleTag({ content: ".dc-ctx-body { overflow-anchor: none; }" });
      await openCockpit(mp);
      await closeSheet(mp);
      await mp.evaluate(() => {
        window.dcNewsAtAppear = null;
        const sheet = document.querySelector("dc-ctx-sheet");
        const seen = new MutationObserver(() => {
          const root = sheet.querySelector("[data-cockpit]");
          if (!root) return;
          window.dcNewsAtAppear = root.querySelectorAll("[data-cockpit-section=news] a[data-notify-id]").length;
          seen.disconnect();
        });
        seen.observe(sheet, { childList: true, subtree: true });
      });
      await openCockpit(mp);
      const news = await mp.evaluate(() => window.dcNewsAtAppear);
      await sleep(300);
      const got = await state();
      await sleep(1200);
      const later = await state();
      await closeSheet(mp);
      return { ...got, news, moved: later.rowTop !== got.rowTop || later.top !== got.top };
    };
    const none = await openOn("/projects");
    assert(none.news === 3 && !none.moved, `on /projects the news was not in place at the open or the sheet moved: ${JSON.stringify(none)}`);
    assert(none.active.length === 0 && none.top === 0, `on /projects the sheet marks ${JSON.stringify(none.active)} at scroll ${none.top}`);
    const docs = await openOn("/docs");
    assert(docs.active.join(",") === "/docs" && docs.news === 3 && !docs.moved && docs.centered && docs.room <= 1 && docs.tab, `on /docs: ${JSON.stringify(docs)}`);
    await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await openCockpit(mp);
    const coder = await mp.$eval(`${SHEET} a[data-settings-coder]`, (a) => ({ id: a.dataset.settingsCoder, href: a.getAttribute("href") }));
    await closeSheet(mp);
    const onCoder = await openOn(coder.href);
    assert(onCoder.active.join(",") === coder.id && onCoder.news === 3 && !onCoder.moved && onCoder.centered && onCoder.room <= 1 && onCoder.tab, `on ${coder.href}: ${JSON.stringify(onCoder)}`);
    await mp.unroute("**/notifications", late);
  });

  await run("phone: news that fails to load shows the sheet's Try again, which recovers", async () => {
    const fail = (route) => route.request().method() === "GET" ? route.fulfill({ status: 500, body: "" }) : route.fallback();
    await mp.route("**/notifications", fail);
    await mp.goto(`${BASE}/docs`, { waitUntil: "domcontentloaded" });
    await mp.waitForSelector(COCKPIT, { timeout: 8000 });
    await mp.tap(COCKPIT);
    await mp.waitForSelector(`${SHEET} [data-ctx-sheet-retry]`, { timeout: 8000 });
    const error = await mp.$eval(`${SHEET} [data-ctx-sheet-error]`, (el) => el.textContent.trim());
    assert(error === "The list could not be loaded." && !(await mp.$(`${SHEET} [data-cockpit]`)), `a failed news load shows ${error}`);
    await mp.unroute("**/notifications", fail);
    await mp.tap(`${SHEET} [data-ctx-sheet-retry]`);
    await mp.waitForSelector(`${SHEET} [data-cockpit-section=news] a[data-notify-id]`, { timeout: 8000 });
    const active = await mp.$$eval(`${SHEET} [data-cockpit-section=settings] .list-group-item.active`, (els) => els.map((el) => el.getAttribute("href")));
    assert(active.join(",") === "/docs", `after Try again the sheet marks ${JSON.stringify(active)}`);
    await closeSheet(mp);
  });

  await run("the Cockpit icon is the gauge, the tab's color while quiet, yellow and red by the worst reading, labelled", async () => {
    await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    assert((await L.waitUpgraded(mp, ["dc-host-status"], 8000)).length === 0, "dc-host-status not upgraded");
    await fire(mp, "host", { hasCpu: true, hasMem: true, hasDisk: true, cpu: 12, mem: 34, disk: 56, cpuLabel: "a", memLabel: "b", diskLabel: "c" });
    await sleep(150);
    const quiet = await tabIcon(mp);
    assert(quiet.gauge && quiet.shown && quiet.bars === 0, `not the gauge alone: ${JSON.stringify(quiet)}`);
    assert(quiet.tone === "tab" && !quiet.label && quiet.hidden && !/server/.test(quiet.name || ""), `quiet: ${JSON.stringify(quiet)}`);
    await fire(mp, "host", { hasCpu: true, hasMem: true, hasDisk: true, cpu: 12, mem: 84, disk: 56 });
    await sleep(150);
    const warn = await tabIcon(mp);
    assert(!warn.tabColor, `warn keeps the tab's color: ${JSON.stringify(warn)}`);
    assert(warn.tone === "yellow" && warn.label === "Server busy, RAM 84%" && warn.hidden && /^Cockpit, server busy(, news)?$/.test(warn.name), `warn: ${JSON.stringify(warn)}`);
    await fire(mp, "host", { hasCpu: true, hasMem: true, hasDisk: true, cpu: 140, mem: 84, disk: 97 });
    await sleep(150);
    const crit = await tabIcon(mp);
    assert(!crit.tabColor, `crit keeps the tab's color: ${JSON.stringify(crit)}`);
    assert(crit.tone === "red" && crit.label === "Server critical, CPU 140%" && crit.hidden, `crit: ${JSON.stringify(crit)}`);
    await fire(mp, "notifications", { targets: ["a"], terminals: [], assistants: [] });
    await sleep(150);
    assert((await tabIcon(mp)).name === "Cockpit, server critical, news", `the news took the server's state out of the name: ${(await tabIcon(mp)).name}`);
    await fire(mp, "notifications", { targets: [], terminals: [], assistants: [] });
    await sleep(150);
    assert((await tabIcon(mp)).name === "Cockpit, server critical", `without news the name reads ${(await tabIcon(mp)).name}`);
    await fire(mp, "host", { hasCpu: false, hasMem: false, hasDisk: false });
    await sleep(150);
    const none = await tabIcon(mp);
    assert(none.shown && none.tone === "tab" && !none.label, `a host that answers nothing: ${JSON.stringify(none)}`);
    // The real machine's host beat may land any moment, so the active color
    // is read with the tone classes taken off rather than after an event.
    await mp.goto(`${BASE}/settings/general`, { waitUntil: "domcontentloaded" });
    await mp.$eval(`${COCKPIT} [data-host-worst-icon]`, (icon) => icon.classList.remove("text-yellow", "text-red"));
    const active = await tabIcon(mp);
    assert(active.active, `quiet on an active tab the gauge is not the active color: ${JSON.stringify(active)}`);
    await mp.waitForFunction(() => customElements.get("dc-notifications") && customElements.get("dc-host-status"), null, { timeout: 8000 });
  });

  await run("the Cockpit dot follows the notifications and backupreviews events, blue, no number, labelled", async () => {
    const dot = () => mp.$eval(`${COCKPIT} [data-cockpit-dot]`, (el) => ({
      shown: getComputedStyle(el).display !== "none",
      text: el.textContent.trim(),
      blue: el.classList.contains("status-blue") && el.classList.contains("dc-news-dot"),
      label: el.closest("button").getAttribute("aria-label"),
      rail: document.querySelector(".dc-rail .dc-notify-badge").textContent,
    }));
    await fire(mp, "backupreviews", { count: 0 });
    await fire(mp, "notifications", { targets: ["a", "b", "c"], terminals: [], assistants: [] });
    await mp.waitForFunction((sel) => getComputedStyle(document.querySelector(`${sel} [data-cockpit-dot]`)).display !== "none", COCKPIT, { timeout: 4000 });
    const on = await dot();
    assert(on.text === "", `the dot carries a number: ${on.text}`);
    assert(on.blue, "the dot is not the app's blue news dot");
    assert(withNews(on.label), `the tab reads ${on.label}`);
    assert(on.rail === "3", `the bell reads ${on.rail}`);
    await fire(mp, "notifications", { targets: [], terminals: [], assistants: [] });
    await mp.waitForFunction((sel) => getComputedStyle(document.querySelector(`${sel} [data-cockpit-dot]`)).display === "none", COCKPIT, { timeout: 4000 });
    assert(withoutNews((await dot()).label), "the tab keeps its news label with nothing unread");
    await fire(mp, "backupreviews", { count: 2 });
    await mp.waitForFunction((sel) => getComputedStyle(document.querySelector(`${sel} [data-cockpit-dot]`)).display !== "none", COCKPIT, { timeout: 4000 });
    await fire(mp, "backupreviews", { count: 0 });
    await mp.waitForFunction((sel) => getComputedStyle(document.querySelector(`${sel} [data-cockpit-dot]`)).display === "none", COCKPIT, { timeout: 4000 });
  });

  if (STATE_DIR) {
    const readAll = () => mp.evaluate(() => fetch("/notifications/read", {
      method: "POST",
      headers: { "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]').content, "Content-Type": "application/x-www-form-urlencoded" },
      body: "all=1",
    }).then((r) => r.status));
    const reviewFile = `${STATE_DIR}/import-review.json`;
    const writeReviews = (list) => {
      fs.writeFileSync(`${reviewFile}.tmp`, JSON.stringify(list));
      fs.renameSync(`${reviewFile}.tmp`, reviewFile);
    };
    const serverDot = (path) => mp.evaluate((p) => fetch(p, { headers: { Accept: "text/html" } }).then((r) => r.text()).then((html) => {
      const doc = new DOMParser().parseFromString(html, "text/html");
      const el = doc.querySelector('.dc-tabbar button[data-ctx-area="cockpit"] [data-cockpit-dot]');
      return el ? { shown: !el.classList.contains("d-none"), label: el.closest("button").getAttribute("aria-label") } : null;
    }), path);
    const liveDot = () => mp.$eval(`${COCKPIT} [data-cockpit-dot]`, (el) => getComputedStyle(el).display !== "none");
    const boosted = async (href) => {
      await mp.evaluate((h) => {
        const a = document.createElement("a");
        a.href = h;
        document.querySelector("[data-page-content]").append(a);
        a.click();
      }, href);
      await mp.waitForURL((url) => url.pathname === href, { timeout: 8000 });
      await sleep(300);
    };

    await run("the Cockpit dot: none, notifications only, backup reviews only, rendered and after in app navigation", async () => {
      writeReviews([]);
      assert(await readAll() < 400, "marking everything read failed");
      await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await sleep(800);
      let rendered = await serverDot("/projects");
      assert(rendered && !rendered.shown && withoutNews(rendered.label), `with nothing open the server renders ${JSON.stringify(rendered)}`);
      assert(!(await liveDot()), "with nothing open the dot shows");

      const target = `cockpit-dot-${Date.now().toString(36)}`;
      const dir = `${STATE_DIR}/notification-inbox/copilot`;
      fs.mkdirSync(dir, { recursive: true });
      fs.writeFileSync(`${dir}/${target}.tmp`, JSON.stringify({ session_id: target, hook_event_name: "Stop" }));
      fs.renameSync(`${dir}/${target}.tmp`, `${dir}/${target}.json`);
      await mp.waitForFunction((sel) => getComputedStyle(document.querySelector(`${sel} [data-cockpit-dot]`)).display !== "none", COCKPIT, { timeout: 10000 });
      rendered = await serverDot("/docs");
      assert(rendered && rendered.shown && withNews(rendered.label), `with a notification unread the server renders ${JSON.stringify(rendered)}`);
      await boosted("/docs");
      assert(await liveDot(), "the dot is gone after an in app navigation");
      await mp.goto(`${BASE}/settings/general`, { waitUntil: "domcontentloaded" });
      assert(await liveDot(), "the dot is gone after a full load");
      assert(await readAll() < 400, "marking everything read failed");
      await mp.waitForFunction((sel) => getComputedStyle(document.querySelector(`${sel} [data-cockpit-dot]`)).display === "none", COCKPIT, { timeout: 6000 });

      writeReviews([{ id: "cockpit-e2e-review", path: `${STATE_DIR}/cockpit-e2e-missing.json`, section: "settings", createdAt: new Date().toISOString() }]);
      rendered = await serverDot("/projects");
      assert(rendered && rendered.shown, `with a review open the server renders ${JSON.stringify(rendered)}`);
      await boosted("/projects");
      assert(await liveDot(), "an open review shows no dot after an in app navigation");
      await openCockpit(mp);
      const head = await mp.$eval(`${SHEET} [data-cockpit-section=settings] .dc-section-head [data-backup-reviews]`, (el) => ({
        shown: getComputedStyle(el).display !== "none", text: el.textContent.trim(), label: el.getAttribute("aria-label"),
      }));
      assert(head.shown && head.text === "1" && head.label === "1 backup file to resolve", `the Settings head reads ${JSON.stringify(head)}`);
      await closeSheet(mp);
      const status = await mp.evaluate(() => fetch("/settings/backup", {
        method: "POST",
        headers: { "Content-Type": "application/x-www-form-urlencoded" },
        body: new URLSearchParams({ csrf_token: document.querySelector('meta[name="csrf-token"]').content, form: "review-keep", id: "cockpit-e2e-review" }),
      }).then((r) => r.status));
      assert(status < 400, `resolving the review answered ${status}`);
      await mp.waitForFunction((sel) => getComputedStyle(document.querySelector(`${sel} [data-cockpit-dot]`)).display === "none", COCKPIT, { timeout: 6000 });
      rendered = await serverDot("/projects");
      assert(rendered && !rendered.shown, `a resolved review still renders ${JSON.stringify(rendered)}`);
    });
  } else {
    await run("the Cockpit dot against the server (needs STATE_DIR mount)", async () => {
      throw new Error("STATE_DIR not set, skipping the rendered dot checks");
    }, { soft: true });
  }

  await run("the sheet: server, news, the three tiles, settings, in that order", async () => {
    await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await openCockpit(mp);
    await mp.waitForSelector(`${SHEET} [data-cockpit-section=news] a[data-notify-id]`, { timeout: 8000 });
    const sheet = await mp.evaluate((sel) => {
      const root = document.querySelector(`${sel} [data-cockpit]`);
      const top = (s) => Math.round(root.querySelector(`[data-cockpit-section=${s}]`).getBoundingClientRect().top);
      const server = root.querySelector("[data-cockpit-section=server]");
      return {
        order: ["server", "news", "actions", "settings"].map(top),
        rows: [...server.querySelectorAll("[data-host-meter]")].filter((el) => getComputedStyle(el).display !== "none").map((el) => ({
          key: el.dataset.hostMeter,
          name: el.querySelector("div > span").textContent.trim(),
          value: el.querySelector(".js-host-value").textContent.trim(),
          label: el.title,
          top: Math.round(el.getBoundingClientRect().top),
          lines: el.querySelectorAll(".js-host-label").length,
        })),
        controls: server.querySelectorAll("a, button, input, [data-bs-toggle]").length,
        serverHead: /\bServer\b/.test(server.textContent),
        serverGroup: server.getAttribute("aria-label"),
        news: [...root.querySelectorAll("[data-cockpit-section=news] a[data-notify-id]")].map((a) => a.dataset.notifyId),
        showAll: Boolean(root.querySelector("[data-cockpit-section=news] [data-cockpit-show-all]")),
        themeName: root.querySelector("[data-cockpit-section=actions] [data-theme-cycle]").getAttribute("aria-label"),
        tiles: [...root.querySelectorAll("[data-cockpit-section=actions] .btn")].map((b) => b.textContent.replace(/\s+/g, " ").trim()),
        newsRows: [...root.querySelectorAll("[data-cockpit-section=news] a[data-notify-id]")].map((a) => {
          const style = getComputedStyle(a);
          const inner = a.getBoundingClientRect().height - parseFloat(style.paddingTop) - parseFloat(style.paddingBottom);
          return { inner: Math.round(inner), line: Math.round(parseFloat(style.lineHeight)), title: a.title };
        }),
        logout: (() => {
          const form = root.querySelector('[data-cockpit-section=actions] form[action="/logout"]');
          return form ? { method: form.method, token: Boolean(form.querySelector('[name="csrf_token"]').value), noPe: form.hasAttribute("data-no-pe") } : null;
        })(),
        settings: [...root.querySelectorAll("[data-cockpit-section=settings] a[href]")].map((a) => a.getAttribute("href")),
      };
    }, SHEET);
    for (let i = 1; i < sheet.order.length; i++) assert(sheet.order[i - 1] < sheet.order[i], `sections out of order: ${sheet.order}`);
    assert(sheet.rows.map((r) => r.key).join(",") === "cpu,mem,disk", `server rows ${JSON.stringify(sheet.rows)}`);
    assert(sheet.rows.map((r) => r.name).join(",") === "CPU,RAM,Disk", `row names ${sheet.rows.map((r) => r.name)}`);
    assert(sheet.rows.every((r) => /^\d+%$/.test(r.value) && r.label.startsWith(`${r.name} ${r.value}`)), `row values ${JSON.stringify(sheet.rows)}`);
    assert(sheet.rows.every((r) => r.top === sheet.rows[0].top && r.lines === 0), `the server is not one row without detail lines: ${JSON.stringify(sheet.rows)}`);
    assert(sheet.newsRows.every((r) => r.inner <= r.line + 2 && r.title), `a news row is not one line: ${JSON.stringify(sheet.newsRows)}`);
    assert(sheet.controls === 0, `the server rows carry ${sheet.controls} controls`);
    assert(!sheet.serverHead && sheet.serverGroup === "Server", `the server row carries a heading or lost its name: ${JSON.stringify([sheet.serverHead, sheet.serverGroup])}`);
    assert(sheet.news.join(",") === "cockpit-fake-1,cockpit-fake-2,cockpit-fake-3", `news ${sheet.news}`);
    assert(sheet.showAll, "no Show all");
    assert(sheet.tiles.length === 3 && sheet.tiles[0] === "" && /^Theme: (Light|Dark|Follow the OS)$/.test(sheet.themeName) && /^(Up to date|Update to \S+)$/.test(sheet.tiles[1]) && sheet.tiles[2] === "Logout", `tiles ${JSON.stringify(sheet.tiles)}`);
    assert(sheet.logout && sheet.logout.method === "post" && sheet.logout.token && sheet.logout.noPe, `logout form ${JSON.stringify(sheet.logout)}`);
    assert(sheet.settings.includes("/settings/general") && sheet.settings.includes("/settings/notifications") && sheet.settings.includes("/docs"), `settings links ${sheet.settings}`);
    const tones = () => mp.$$eval(".dc-tabbar .dc-tabbar-btn", (els) => {
      const primary = `rgb(${getComputedStyle(document.body).getPropertyValue("--tblr-primary-rgb").trim().replace(/\s*,\s*/g, ", ")})`;
      return els.filter((el) => getComputedStyle(el).color === primary).map((el) => el.dataset.ctxArea || el.getAttribute("href"));
    });
    const open = await tones();
    assert(open.join(",") === "projects", `with the sheet open the tabs marked are ${open}`);
    const focus = await mp.evaluate(() => {
      const panel = document.querySelector("dc-ctx-sheet [data-ctx-sheet-panel]");
      return { inside: panel.contains(document.activeElement), label: panel.getAttribute("aria-label"), role: panel.getAttribute("role") };
    });
    assert(focus.inside && focus.label === "Cockpit" && focus.role === "dialog", `the sheet ${JSON.stringify(focus)}`);
    const inside = await mp.$eval(`${SHEET} .dc-sheet-panel`, (el) => {
      const box = el.getBoundingClientRect();
      const bar = document.querySelector(".dc-tabbar").getBoundingClientRect();
      return { bottom: Math.round(box.bottom), tabTop: Math.round(bar.top), height: Math.round(box.height), vh: window.innerHeight };
    });
    assert(Math.abs(inside.bottom - inside.tabTop) <= 1, `the sheet does not stand on the tab bar: ${JSON.stringify(inside)}`);
    assert(inside.height <= inside.vh * 0.6, `the sheet is taller than the list sheets: ${JSON.stringify(inside)}`);
    await fire(mp, "host", { hasCpu: true, hasMem: true, hasDisk: true, cpu: 23, mem: 84, disk: 44, cpuLabel: "Usage across 8 cores", memLabel: "27 GB of 32 GB used", diskLabel: "410 GB free" });
    await sleep(150);
    const live = await mp.$eval(`${SHEET} [data-host-meter=mem]`, (el) => ({ value: el.querySelector(".js-host-value").textContent, label: el.title.split(" · ")[1], tone: [...el.querySelector(".js-host-bar").classList].find((c) => c.startsWith("bg-")) }));
    assert(live.value === "84%" && live.label === "27 GB of 32 GB used" && live.tone === "bg-yellow", `the sheet did not follow the host event: ${JSON.stringify(live)}`);
    const cpuTone = await mp.$eval(`${SHEET} [data-host-meter=cpu] .js-host-bar`, (el) => [...el.classList].find((c) => c.startsWith("bg-")));
    assert(cpuTone === "bg-green", `a quiet reading in the sheet is not green: ${cpuTone}`);
  });

  await run("the theme tile is a square mark named Theme: <mode>, walks the three states and keeps the sheet open", async () => {
    await mp.emulateMedia({ colorScheme: "light" });
    await mp.evaluate(() => { try { localStorage.removeItem("dc-theme"); } catch (e) { /* blocked storage */ } });
    await mp.evaluate(() => document.dispatchEvent(new CustomEvent("dc:theme")));
    const tile = `${SHEET} [data-cockpit-section=actions] [data-theme-cycle]`;
    const state = () => mp.evaluate((sel) => {
      const button = document.querySelector(sel);
      return {
        scheme: document.documentElement.getAttribute("data-bs-theme"),
        text: button.textContent.trim(),
        aria: button.getAttribute("aria-label"),
        title: button.getAttribute("title"),
        marks: [...button.querySelectorAll("[data-theme-mark]")].filter((m) => getComputedStyle(m).display !== "none").length,
      };
    }, tile);
    const walked = [];
    for (const want of [{ scheme: "light", label: "Light" }, { scheme: "dark", label: "Dark" }, { scheme: "light", label: "Follow the OS" }]) {
      await mp.tap(tile);
      await sleep(450);
      const got = await state();
      walked.push(`${got.aria}/${got.scheme}`);
      assert(got.scheme === want.scheme && got.aria === `Theme: ${want.label}` && got.title === got.aria, `tile walk ${walked.join(" > ")}, expected Theme: ${want.label}/${want.scheme}`);
      assert(got.text === "" && got.marks === 1, `the tile is not its mark alone: ${JSON.stringify(got)}`);
      assert(!/dark mode/i.test(got.aria), "the tile calls itself a dark mode toggle");
    }
    assert(await mp.$(`${SHEET} [data-cockpit]`), "a tap on the theme tile closed the sheet");
    await mp.emulateMedia({ colorScheme: null });
  });

  await run("the sheet is as tall as the other list sheets", async () => {
    await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    const height = () => mp.$eval(`${SHEET} .dc-sheet-panel`, (el) => Math.round(el.getBoundingClientRect().height));
    await openCockpit(mp);
    const control = await height();
    await closeSheet(mp);
    await mp.tap('.dc-tabbar button[data-ctx-area="projects"]');
    await mp.waitForSelector(`${SHEET} .dc-ctx`, { timeout: 8000 });
    const projects = await height();
    await closeSheet(mp);
    assert(Math.abs(control - projects) <= 1, `the Cockpit sheet is ${control}px, the projects sheet ${projects}px`);
  });

  await run("the tiles share one height, the theme a square, the update label whole, a longer one wrapped and never cut", async () => {
    await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await openCockpit(mp);
    const measure = () => mp.$$eval(`${SHEET} [data-cockpit-section=actions] .btn`, (els) => els.map((b) => {
      const box = b.getBoundingClientRect();
      const icon = [...b.querySelectorAll(".ti")].find((i) => i.getClientRects().length).getBoundingClientRect();
      const label = b.querySelector(":scope > span");
      const lb = label ? label.getBoundingClientRect() : null;
      return {
        w: Math.round(box.width), h: Math.round(box.height),
        icon: Math.round(icon.top + icon.height / 2 - box.top), mid: Math.round(box.height / 2),
        label: lb ? Math.round(lb.top + lb.height / 2 - box.top) : null,
        cut: label ? label.scrollWidth > label.clientWidth || label.classList.contains("text-truncate") || getComputedStyle(label).textOverflow === "ellipsis" : false,
        text: label ? label.textContent : null,
        inside: lb ? lb.right <= box.right + 0.5 : true,
      };
    }));
    await mp.$eval(`${SHEET} [data-cockpit-update-label]`, (el) => { el.textContent = "Update to 1.70.0"; });
    const tiles = await measure();
    assert(tiles.length === 3, `${tiles.length} tiles`);
    assert(tiles.every((t) => t.h === tiles[0].h), `the tiles differ in height: ${JSON.stringify(tiles)}`);
    assert(tiles[0].w === tiles[0].h && tiles[0].label === null, `the theme tile is not a square mark: ${JSON.stringify(tiles[0])}`);
    assert(tiles.every((t) => Math.abs(t.icon - t.mid) <= 1 && (t.label === null || Math.abs(t.label - t.mid) <= 1)), `the content is not centred: ${JSON.stringify(tiles)}`);
    assert(!tiles[1].cut && tiles[1].w > tiles[2].w, `Update to 1.70.0 does not stand whole in the widest tile: ${JSON.stringify(tiles)}`);
    await mp.$eval(`${SHEET} [data-cockpit-update-label]`, (el) => { el.textContent = "Update to 1.70.0-a-rather-long-name"; });
    const long = await measure();
    assert(!long[1].cut && long[1].text === "Update to 1.70.0-a-rather-long-name", `the long update label is cut: ${JSON.stringify(long[1])}`);
    assert(long.every((t, i) => t.w === tiles[i].w && t.inside), `a long label moved a tile sideways or ran out of it: ${JSON.stringify(long)}`);
    assert(long[0].w === long[0].h && long[1].h === long[2].h && long[1].h >= tiles[1].h, `the row did not grow as one around the wrapped label: ${JSON.stringify(long)}`);
    await closeSheet(mp);
  });

  await run("the update tile: Up to date and inert, Update to <version> and acting only while one exists", async () => {
    const running = await mp.$eval("dc-update-check", (el) => el.dataset.version);
    const answer = { body: null };
    await mp.route("**/update/check*", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(answer.body) }));
    const seed = (promptedVersion) => mp.evaluate((v) => {
      try { localStorage.setItem("dc-update", JSON.stringify(v ? { prompted: Date.now(), promptedVersion: v } : {})); } catch (e) { /* blocked storage */ }
    }, promptedVersion);
    const tile = () => mp.$eval(`${SHEET} [data-cockpit-update]`, (el) => ({
      text: el.textContent.replace(/\s+/g, " ").trim(), disabled: el.disabled, acts: el.hasAttribute("data-update-open"),
    }));
    const dialogFor = async (ms) => mp.waitForSelector(".swal2-popup", { timeout: ms }).then(() => true, () => false);
    const cases = [
      { name: "a build that cannot update itself", body: { supported: false } },
      { name: "no newer version", body: { supported: true, available: false, current: running, latest: running, writable: true } },
    ];
    for (const c of cases) {
      answer.body = c.body;
      await seed("");
      await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await sleep(800);
      await openCockpit(mp);
      const t = await tile();
      assert(t.text === "Up to date" && t.disabled && !t.acts, `${c.name}: the tile reads ${JSON.stringify(t)}`);
      await mp.tap(`${SHEET} [data-cockpit-update]`, { force: true });
      assert(!(await dialogFor(1500)), `${c.name}: a tap on the tile opened a dialog`);
      await closeSheet(mp);
    }
    answer.body = { supported: true, available: true, current: running, latest: "9.9.9", writable: true, releases: [] };
    await seed("9.9.9");
    await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await mp.waitForFunction(() => {
      try { return (JSON.parse(localStorage.getItem("dc-update")).status || {}).available; } catch (e) { return false; }
    }, null, { timeout: 8000 });
    await openCockpit(mp);
    const t = await tile();
    assert(t.text === "Update to 9.9.9" && !t.disabled && t.acts, `with an update the tile reads ${JSON.stringify(t)}`);
    await mp.tap(`${SHEET} [data-cockpit-update]`);
    await mp.waitForFunction(() => /Update to 9\.9\.9\?/.test(document.querySelector(".swal2-title")?.textContent || ""), null, { timeout: 12000 });
    await L.dismissUpdate(mp);
    await mp.unroute("**/update/check*");
    await seed("");
  });

  await run("focus: the sheet takes it on open, Show all keeps it inside, closing gives it back to Cockpit", async () => {
    await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await openCockpit(mp);
    const inside = () => mp.evaluate(() => {
      const panel = document.querySelector("dc-ctx-sheet [data-ctx-sheet-panel]");
      return { inside: panel.contains(document.activeElement), label: panel.getAttribute("aria-label") };
    });
    let got = await inside();
    assert(got.inside && got.label === "Cockpit", `after opening ${JSON.stringify(got)}`);
    await mp.tap(`${SHEET} [data-cockpit-show-all]`);
    await mp.waitForSelector(`${SHEET} [data-cockpit-news]`, { timeout: 8000 });
    got = await inside();
    assert(got.inside && got.label === "Notifications", `after Show all ${JSON.stringify(got)}`);
    await closeSheet(mp);
    const back = await mp.evaluate(() => document.activeElement && document.activeElement.matches('.dc-tabbar [data-ctx-area="cockpit"]'));
    assert(back, "closing did not give the focus back to the Cockpit tab");
    const tones = await mp.$$eval(".dc-tabbar .dc-tabbar-btn", (els) => {
      const primary = `rgb(${getComputedStyle(document.body).getPropertyValue("--tblr-primary-rgb").trim().replace(/\s*,\s*/g, ", ")})`;
      return els.filter((el) => getComputedStyle(el).color === primary).map((el) => el.dataset.ctxArea || el.getAttribute("href"));
    });
    assert(tones.join(",") === "projects", `after closing the tabs marked are ${tones}`);
  });

  await run("phone: a sheet closed by touch leaves no focus ring on its tab", async () => {
    await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await mp.waitForSelector(COCKPIT, { timeout: 8000 });
    // A tap does not focus a button in iOS Safari, so the focus handed back
    // on close is a script's, which WebKit shows as keyboard focus.
    await mp.$eval(COCKPIT, (el) => el.addEventListener("mousedown", (event) => event.preventDefault()));
    await openCockpit(mp);
    await mp.touchscreen.tap(195, 20);
    await mp.waitForFunction(() => document.querySelector("dc-ctx-sheet").hidden, null, { timeout: 4000 });
    const ring = await mp.$eval(COCKPIT, (el) => ({ focused: document.activeElement === el, outline: getComputedStyle(el).outlineStyle, shadow: getComputedStyle(el).boxShadow }));
    assert(ring.focused && ring.outline === "none" && ring.shadow === "none", `after a touch close the tab shows ${JSON.stringify(ring)}`);
  });

  await run("Show all opens every notification in the same sheet, an entry navigates", async () => {
    await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await openCockpit(mp);
    await mp.tap(`${SHEET} [data-cockpit-show-all]`);
    await mp.waitForSelector(`${SHEET} [data-cockpit-news] a[data-notify-id]`, { timeout: 8000 });
    const list = await mp.evaluate((sel) => ({
      title: document.querySelector(`${sel} .dc-ctx-title`).textContent.trim(),
      ids: [...document.querySelectorAll(`${sel} [data-cockpit-news] a[data-notify-id]`)].map((a) => a.dataset.notifyId),
      readAll: Boolean(document.querySelector(`${sel} [data-cockpit-news] .dc-notify-read-all`)),
      cockpit: Boolean(document.querySelector(`${sel} [data-cockpit]`)),
    }), SHEET);
    assert(list.title === "Notifications", `the sheet reads ${list.title}`);
    assert(list.ids.length === 5, `the full list shows ${list.ids.length} of 5`);
    assert(list.readAll && !list.cockpit, `the list sheet is not the full list: ${JSON.stringify(list)}`);
    await mp.tap(`${SHEET} a[data-notify-id="cockpit-fake-2"]`);
    await mp.waitForURL(/\/settings\/general$/, { timeout: 8000 });
    await mp.waitForFunction(() => document.querySelector("dc-ctx-sheet").hidden, null, { timeout: 4000 });
  });

  if (process.env.SHOTS_DIR) {
    await run("screenshots: the tab bar and the open sheet", async () => {
      await fire(mp, "notifications", { targets: ["a", "b", "c"], terminals: [], assistants: [] });
      await mp.emulateMedia({ colorScheme: "dark" });
      await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await sleep(800);
      await fire(mp, "notifications", { targets: ["a", "b", "c"], terminals: [], assistants: [] });
      await sleep(300);
      const bar = await mp.screenshot();
      await openCockpit(mp);
      await mp.waitForSelector(`${SHEET} [data-cockpit-section=news] a[data-notify-id]`, { timeout: 8000 });
      await sleep(500);
      const sheet = await mp.screenshot();
      await closeSheet(mp);
      const img = (buf) => `<img src="data:image/png;base64,${buf.toString("base64")}" style="width:390px;border-radius:24px;border:1px solid #334">`;
      const shot = await mp.context().newPage();
      await shot.setViewportSize({ width: 880, height: 900 });
      await shot.setContent(`<body style="margin:0;background:#0b0f17;display:flex;gap:40px;padding:20px 30px;font:14px sans-serif;color:#aab">
        <div><div style="text-align:center;margin-bottom:8px">Tab bar</div>${img(bar)}</div>
        <div><div style="text-align:center;margin-bottom:8px">Cockpit tapped</div>${img(sheet)}</div></body>`);
      await shot.screenshot({ path: `${process.env.SHOTS_DIR}/control-center.png`, fullPage: true });
      await shot.close();
      fs.writeFileSync(`${process.env.SHOTS_DIR}/cockpit-tabbar.png`, bar);
      fs.writeFileSync(`${process.env.SHOTS_DIR}/cockpit-sheet.png`, sheet);
      await mp.emulateMedia({ colorScheme: null });
    });
  }

  await run("the logout tile signs the phone out", async () => {
    await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await openCockpit(mp);
    await Promise.all([
      mp.waitForURL(/\/login/, { timeout: 8000 }),
      mp.tap(`${SHEET} [data-cockpit-logout]`),
    ]);
    await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    assert(/\/login/.test(mp.url()), "still signed in after the logout tile");
  });
});
