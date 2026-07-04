const fs = require("fs");
const path = require("path");
const L = require("./lib");
const { assert, sleep, BASE } = L;

// Notification center: custom element dc-notifications (one per header
// breakpoint, shared SSE channel in the module). Routes: GET /notifications
// (JSON list), GET /notifications/stream (SSE unread count + added events),
// POST /notifications/read (id or all). Events are ingested server side from
// coder signals; this test injects fake events into the copilot notification
// inbox (the generic per-coder ingestion seam), which needs the instance's
// notification-inbox dir mounted into the runner:
//   docker run ... -v <state-dir>/notification-inbox:/inbox -e NOTIFY_DIR=/inbox ...
// Without NOTIFY_DIR the injection checks are skipped (soft), the UI checks
// still run. Opening a target's attach page marks its notifications read, so
// the badge assertions navigate around that.

const NOTIFY_DIR = process.env.NOTIFY_DIR || "";

function injectCopilotDone(targetId) {
  const dir = path.join(NOTIFY_DIR, "copilot");
  fs.mkdirSync(dir, { recursive: true });
  const file = path.join(dir, `${Date.now()}-${Math.random().toString(36).slice(2)}`);
  fs.writeFileSync(`${file}.tmp`, JSON.stringify({ session_id: targetId, hook_event_name: "Stop" }));
  fs.renameSync(`${file}.tmp`, `${file}.json`);
}

L.runFeature("NOTIFICATIONS", async ({ page, run, mobilePage }) => {
  const tag = `ntf-${Date.now().toString(36)}`;
  const project = `zztc-${tag}`;
  const coderName = `tcntf-${tag.slice(-4)}`;
  let coderUrl = null;
  try {
    await run("bell renders in the header and the center opens empty", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      assert((await L.waitUpgraded(page, ["dc-notifications"], 8000)).length === 0, "dc-notifications not upgraded");
      await page.locator(".dc-notify-bell:visible").first().click();
      await page.waitForSelector(".dc-notify-menu.show", { timeout: 6000 });
      assert(await page.locator(".dc-notify-menu.show .dc-notify-list").count(), "list container missing");
      await page.keyboard.press("Escape");
    });

    await run("mobile: bell present in the compact header", async () => {
      const mp = await mobilePage();
      await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      assert((await L.waitUpgraded(mp, ["dc-notifications"], 8000)).length === 0, "dc-notifications not upgraded (mobile)");
      await mp.locator(".dc-notify-bell:visible").first().click();
      await mp.waitForSelector(".dc-notify-menu.show", { timeout: 6000 });
      const box = await mp.locator(".dc-notify-menu.show").boundingBox();
      assert(box && box.width <= 390, "menu wider than the viewport");
      await mp.keyboard.press("Escape");
    });

    await run("settings: volume bar drives the scriptune master volume", async () => {
      await page.goto(`${BASE}/settings/notifications`, { waitUntil: "domcontentloaded" });
      assert((await L.waitUpgraded(page, ["dc-notify-volume"], 8000)).length === 0, "dc-notify-volume not upgraded");
      const initial = await page.evaluate(() => ({
        value: document.querySelector("dc-notify-volume input").value,
        badge: document.querySelector("[data-volume-output]").textContent,
      }));
      assert(initial.value === "1" && initial.badge === "100%", `default: ${JSON.stringify(initial)}`);
      await page.evaluate(() => {
        const range = document.querySelector("dc-notify-volume input");
        range.value = "0.3";
        range.dispatchEvent(new Event("input", { bubbles: true }));
      });
      const after = await page.evaluate(() => ({
        stored: localStorage.getItem("scriptune-master-volume"),
        badge: document.querySelector("[data-volume-output]").textContent,
      }));
      assert(after.stored === "0.3" && after.badge === "30%", `after: ${JSON.stringify(after)}`);
      await page.evaluate(() => {
        const range = document.querySelector("dc-notify-volume input");
        range.value = "1";
        range.dispatchEvent(new Event("input", { bubbles: true }));
      });
    });

    await run("settings: jingle selection persists server-side and reaches the meta tag", async () => {
      await page.goto(`${BASE}/settings/notifications`, { waitUntil: "domcontentloaded" });
      assert(await page.$('input[name="jingle"][value="arpeggio"]:checked'), "default jingle not arpeggio");
      await page.check('input[name="jingle"][value="retro"]');
      await Promise.all([
        page.waitForURL(/\/settings\/notifications$/, { timeout: 10000 }),
        page.locator('form[action="/settings/notifications"] button[type="submit"]').click(),
      ]);
      await page.waitForSelector('input[name="jingle"][value="retro"]:checked', { state: "attached", timeout: 6000 });
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      const meta = await page.getAttribute('meta[name="dc-jingle"]', "content");
      assert(meta === "retro", `meta dc-jingle: ${meta}`);
      await page.goto(`${BASE}/settings/notifications`, { waitUntil: "domcontentloaded" });
      await page.check('input[name="jingle"][value="arpeggio"]');
      await Promise.all([
        page.waitForURL(/\/settings\/notifications$/, { timeout: 10000 }),
        page.locator('form[action="/settings/notifications"] button[type="submit"]').click(),
      ]);
    });

    if (!NOTIFY_DIR) {
      await run("event injection (needs NOTIFY_DIR mount)", async () => {
        throw new Error("NOTIFY_DIR not set, skipping injection checks");
      }, { soft: true });
      return;
    }

    await L.createProject(page, project);
    coderUrl = await L.createSession(page, project, coderName);
    const coderId = new URL(coderUrl).pathname.split("/").pop();

    await run("injected done event -> toast, badge, title counter", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await sleep(1500);
      injectCopilotDone(coderId);
      await page.waitForFunction((name) =>
        [...document.querySelectorAll(".swal2-toast")].some((t) => t.textContent.includes(name)),
        coderName, { timeout: 12000 });
      await page.waitForFunction(() => {
        const badge = document.querySelector(".dc-notify-badge:not(.d-none)");
        return badge && parseInt(badge.textContent, 10) >= 1;
      }, null, { timeout: 6000 });
      assert(/^\(\d+\+?\)\s/.test(await page.title()), "title counter missing");
    });

    await run("follow-up signal within the dedupe window is swallowed", async () => {
      const first = await page.evaluate((sid) =>
        fetch("/notifications", { headers: { Accept: "application/json" } })
          .then((r) => r.json())
          .then((d) => (d.notifications || []).find((n) => n.targetId === sid && !n.read).id), coderId);
      injectCopilotDone(coderId);
      await sleep(2500);
      const state = await page.evaluate((sid) =>
        fetch("/notifications", { headers: { Accept: "application/json" } })
          .then((r) => r.json())
          .then((d) => {
            const own = (d.notifications || []).filter((n) => n.targetId === sid && !n.read);
            return { count: own.length, id: own[0] && own[0].id };
          }), coderId);
      assert(state.count === 1 && state.id === first, `expected the same single unread entry, got ${JSON.stringify(state)}`);
    });

    await run("center lists the entry unread with coder + project", async () => {
      await page.locator(".dc-notify-bell:visible").first().click();
      await page.waitForSelector(".dc-notify-menu.show", { timeout: 6000 });
      const item = page.locator(`.dc-notify-menu.show a[data-notify-target="${coderId}"]`).first();
      await item.waitFor({ state: "visible", timeout: 6000 });
      const text = await item.textContent();
      assert(text.includes(coderName) && text.includes(project), `item text: ${text}`);
      assert(await item.evaluate((el) => el.classList.contains("dc-notify-unread")), "entry not marked unread");
    });

    await run("projects page and quick nav mark the coder with news", async () => {
      // The page was loaded before the injection, so this dot can only have
      // appeared through the live SSE decoration.
      await page.waitForSelector(`#project-${project} .status-dot-animated.status-blue`, { state: "attached", timeout: 6000 });
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(`#project-${project} .status-dot-animated.status-blue`, { state: "attached", timeout: 6000 });
      await page.waitForFunction(() => {
        const badge = document.querySelector(".quicknav-toggle [data-notify-count]");
        return badge && !badge.classList.contains("d-none") && parseInt(badge.textContent, 10) >= 1;
      }, null, { timeout: 6000 });
      await page.click(".quicknav-toggle");
      await page.waitForSelector('[data-quicknav-pane="active"] .status-dot-animated.status-blue', { state: "attached", timeout: 6000 });
      await page.keyboard.press("Escape");
    });

    // The assertions below scope to the test's own coder instead of the
    // global badge: the instance also ingests bells from real copilot
    // coders running on the host, so background activity may keep the
    // global unread count above zero at any time.
    const ownEntriesRead = (id) =>
      page.waitForFunction((sid) =>
        fetch("/notifications", { headers: { Accept: "application/json" } })
          .then((r) => r.json())
          .then((d) => {
            const own = (d.notifications || []).filter((n) => n.targetId === sid);
            return own.length > 0 && own.every((n) => n.read);
          }), id, { timeout: 8000 });

    await run("clicking the entry opens the coder and marks it read", async () => {
      await page.locator(".dc-notify-bell:visible").first().click();
      await page.waitForSelector(".dc-notify-menu.show", { timeout: 6000 });
      await Promise.all([
        page.waitForURL(new RegExp(`/coders/${coderId}$`), { timeout: 15000 }),
        page.locator(`.dc-notify-menu.show a[data-notify-target="${coderId}"]`).first().click(),
      ]);
      await ownEntriesRead(coderId);
    });

    await run("toast on another device is dismissed live when read elsewhere", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await sleep(1500);
      injectCopilotDone(coderId);
      await page.waitForSelector(".swal2-toast", { timeout: 12000 });
      await page.evaluate((sid) => {
        const token = document.querySelector('meta[name="csrf-token"]').content;
        return fetch("/notifications", { headers: { Accept: "application/json" } })
          .then((r) => r.json())
          .then((d) => {
            const own = (d.notifications || []).find((n) => n.targetId === sid && !n.read);
            return fetch("/notifications/read", {
              method: "POST",
              headers: { "Content-Type": "application/x-www-form-urlencoded", "X-CSRF-Token": token },
              body: "id=" + encodeURIComponent(own.id),
            });
          });
      }, coderId);
      await page.waitForSelector(".swal2-toast", { state: "detached", timeout: 4000 });
    });

    await run("second event on the visible coder page is auto-marked read", async () => {
      await page.goto(coderUrl, { waitUntil: "domcontentloaded" });
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
      await sleep(1000);
      injectCopilotDone(coderId);
      await sleep(2500);
      await ownEntriesRead(coderId);
    });

    await run("grace window keeps the badge silent on other tabs when read within it", async () => {
      // page stays visibly on the coder's own page (auto-reads incoming news);
      // a second tab must never bump its badge, because the read lands inside
      // the 750ms grace window before the delayed render fires.
      await page.goto(coderUrl, { waitUntil: "domcontentloaded" });
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
      // Let the reader's SSE settle so its auto-read wins the race against the
      // other tab's 750ms grace timer reliably.
      await sleep(1500);
      const other = await page.context().newPage();
      try {
        await other.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
        await sleep(1500);
        injectCopilotDone(coderId);
        await sleep(2500);
        const bumped = await other.evaluate(() => !!document.querySelector(".dc-notify-badge:not(.d-none)"));
        assert(!bumped, "badge bumped on the other tab despite the grace read");
      } finally {
        await other.close();
      }
    });

    await run("shell: long command completion lands as notification with /shells link", async () => {
      const shellUrl = await L.createShell(page, project);
      const shellId = new URL(shellUrl).pathname.split("/").pop();
      try {
        await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 12000 });
        // The command watcher attaches on a 3s reconcile; give it time before
        // the command starts, or the start mark fires unseen.
        await sleep(4000);
        await page.evaluate((href) => {
          const token = document.querySelector('meta[name="csrf-token"]').content;
          return fetch(href + "/input", {
            method: "POST",
            headers: { "Content-Type": "application/json", "X-CSRF-Token": token },
            body: JSON.stringify({ items: [{ raw: "sleep 8\r" }] }),
          });
        }, new URL(shellUrl).pathname);
        await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
        let entry = null;
        const deadline = Date.now() + 45000;
        while (Date.now() < deadline && !entry) {
          const data = await page.evaluate(() =>
            fetch("/notifications", { headers: { Accept: "application/json" } }).then((r) => r.json()));
          entry = (data.notifications || []).find((n) => n.targetId === shellId && !n.read);
          if (!entry) await sleep(2000);
        }
        assert(entry, "no shell notification after 45s");
        assert(entry.url === `/shells/${shellId}`, `url: ${entry.url}`);
        await page.waitForSelector(`#project-${project} [data-notify-target="${shellId}"].status-blue`, { state: "attached", timeout: 6000 });
        await page.locator(".dc-notify-bell:visible").first().click();
        await page.waitForSelector(".dc-notify-menu.show", { timeout: 6000 });
        await Promise.all([
          page.waitForURL(new RegExp(`/shells/${shellId}$`), { timeout: 15000 }),
          page.locator(`.dc-notify-menu.show a[data-notify-target="${shellId}"]`).first().click(),
        ]);
        await page.waitForFunction((sid) =>
          fetch("/notifications", { headers: { Accept: "application/json" } })
            .then((r) => r.json())
            .then((d) => (d.notifications || []).filter((n) => n.targetId === sid).every((n) => n.read)),
          shellId, { timeout: 8000 });
      } finally {
        await L.deleteShell(page, shellUrl).catch(() => {});
      }
    });

    await run("mark all read clears the entry and its live dot", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await sleep(1500);
      injectCopilotDone(coderId);
      await page.waitForSelector(`#project-${project} .status-dot-animated.status-blue`, { state: "attached", timeout: 8000 });
      await page.locator(".dc-notify-bell:visible").first().click();
      await page.waitForSelector(".dc-notify-menu.show", { timeout: 6000 });
      await page.locator(".dc-notify-menu.show .dc-notify-read-all").click();
      await ownEntriesRead(coderId);
      await page.waitForFunction((sid) => {
        const dot = document.querySelector(`[data-notify-target="${sid}"]`);
        return dot && !dot.classList.contains("status-blue");
      }, coderId, { timeout: 6000 });
    });
  } finally {
    if (coderUrl) await L.stopSession(page, coderUrl).catch(() => {});
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" }).catch(() => {});
    const card = `#project-${project}`;
    for (let i = 0; i < 3; i++) {
      const d = page.locator(`${card} form[action^="/coders/"][action$="/delete"]`).first();
      if (await d.count() === 0) break;
      await d.locator("button").first().click().catch(() => {});
      await L.confirmSwal(page).catch(() => {});
      await sleep(500);
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" }).catch(() => {});
    }
    await L.deleteProject(page, project).catch(() => {});
  }
});
