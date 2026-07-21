const L = require("./lib");
const { assert, sleep, submitBtn, confirmSwal, BASE } = L;

// Projects: the dense board with filter, sort, chip fold, and the create +
// delete flows. Custom element dc-project-list. Routes: GET /projects,
// GET /projects/new, POST /projects, POST /projects/delete. Rows are
// .list-group-item[data-project-name] with id project-<name>; sessions render
// as [data-chip] entries inside [data-sessions-body], folded past 8 behind a
// [data-chips-toggle] chip.

L.runFeature("PROJECTS", async ({ engine, page, run }) => {
  const tag = `proj-${Date.now().toString(36)}`;
  const project = `zztc-${tag}`;
  const shellUrls = [];
  try {
    await run("create project shows card with editor link + delete form", async () => {
      await L.createProject(page, project);
      const ok = await page.evaluate((p) => {
        const c = [...document.querySelectorAll("[data-project-name]")].find((e) => e.dataset.projectName === p);
        if (!c) return { found: false };
        const s = c.closest('[id^="project-"]') || c;
        return { found: true, editor: !!s.querySelector('a[href*="/editor"]'), del: !!s.querySelector('form[action="/projects/delete"]') };
      }, project);
      assert(ok.found && ok.editor && ok.del, `card wrong: ${JSON.stringify(ok)}`);
    });

    // dc-project-list renders only when at least one project exists, so this
    // runs after the create (self-contained on a fresh instance).
    await run("base custom elements upgraded on /projects", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      const missing = await L.waitUpgraded(page, ["dc-quicknav", "dc-update-check", "dc-project-list"], 8000);
      assert(missing.length === 0, `not upgraded: ${missing}`);
    });

    await run("filter hides non-matching + empty state + clear restores", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      const f = await page.$("[data-project-filter]");
      assert(f, "no filter");
      await f.fill(project); await sleep(300);
      const match = await page.evaluate((p) => { const v = [...document.querySelectorAll("[data-project-name]")].filter((c) => c.offsetParent !== null); return { visible: v.length, onlyMine: v.every((c) => c.dataset.projectName === p) }; }, project);
      assert(match.visible >= 1 && match.onlyMine, `filter wrong: ${JSON.stringify(match)}`);
      await f.fill("zzzz-no-such-xyz"); await sleep(300);
      assert(await page.evaluate(() => { const e = document.querySelector("[data-project-filter-empty]"); return e && e.offsetParent !== null; }), "no empty state");
      const clear = await page.$("[data-project-filter-clear]"); if (clear) await clear.click(); else await f.fill(""); await sleep(200);
      assert(await page.evaluate(() => [...document.querySelectorAll("[data-project-name]")].filter((c) => c.offsetParent !== null).length) >= 1, "not restored");
    });

    await run("sort toggle + option updates current + persists across reload", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await page.click("[data-project-sort-toggle]");
      await page.waitForSelector('[data-project-sort-option="alpha"]', { state: "visible", timeout: 5000 });
      await page.click('[data-project-sort-option="alpha"]'); await sleep(300);
      const cur1 = await page.textContent("[data-project-sort-current]");
      await page.reload({ waitUntil: "domcontentloaded" }); await sleep(300);
      const cur2 = await page.textContent("[data-project-sort-current]");
      assert(/alpha/i.test(cur2 || "") || cur1 === cur2, `sort not persisted '${cur1}' vs '${cur2}'`);
    });

    await run("more than 8 sessions folds the chips behind a +N toggle", async () => {
      for (let i = 0; i < 9; i++) shellUrls.push(await L.createShell(page, project));
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      const row = `#project-${project}`;
      const toggle = page.locator(`${row} [data-chips-toggle]`);
      await toggle.waitFor({ state: "visible", timeout: 8000 });
      const visible = () => page.locator(`${row} [data-chip]:not(.d-none)`).count();
      assert((await visible()) === 8, "collapsed chip count wrong");
      await toggle.click(); await sleep(400);
      assert((await visible()) === 9, "expand did not reveal all chips");
    });

    await run("chip context menu renames a shell (right click)", async () => {
      const chip = page.locator(`#project-${project} [data-chip][data-chip-kind="shell"]:not(.d-none)`).first();
      await chip.click({ button: "right" });
      await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
      await page.click('.dc-context-menu button:has-text("Rename")');
      await page.waitForSelector(".swal2-input", { state: "visible", timeout: 5000 });
      const newName = `ren-${tag.slice(-4)}`;
      await page.fill(".swal2-input", newName);
      await confirmSwal(page);
      await page.waitForFunction(
        (n) => [...document.querySelectorAll(".project-chip-name")].some((e) => e.textContent.trim() === n),
        newName,
        { timeout: 8000 },
      );
    });

    // The iPhone path over a chip: the row holds a link, so iOS hands the long
    // press to its own gesture recognizer, which fires pointercancel and raises
    // no contextmenu. Only the touch timer survives that, and the lift must not
    // follow the link.
    // Synthesizing a touch needs the Touch constructor, which WebKit does not
    // expose, so the gesture runs in chromium; the code path under test is
    // plain JS and engine independent.
    await run("touch long-press on a chip link survives the iOS gesture recognizer", async () => {
      if (engine !== "chromium") return;
      // Fresh load and settle: the connect snapshot swaps the chip bodies right
      // after the page opens, and this test holds one chip element across
      // delayed dispatches, so the swap must be done before it is captured
      // (events on a detached node no longer bubble to the container).
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await sleep(1200);
      const chip = page.locator(`#project-${project} [data-chip]:not(.d-none)`).first();
      const before = page.url();
      await chip.evaluate((el) => {
        const r = el.getBoundingClientRect();
        const x = r.x + 8;
        const y = r.y + 8;
        // Real device order: pointerdown fires BEFORE touchstart. Dispatching
        // them the other way around hides the bug where the press stays
        // pointer-owned and iOS's pointercancel kills it.
        el.dispatchEvent(new PointerEvent("pointerdown", {
          bubbles: true, cancelable: true, pointerId: 4, pointerType: "touch", clientX: x, clientY: y,
        }));
        const touch = new Touch({ identifier: 1, target: el, clientX: x, clientY: y });
        el.dispatchEvent(new TouchEvent("touchstart", {
          bubbles: true, cancelable: true, touches: [touch], targetTouches: [touch], changedTouches: [touch],
        }));
        // iOS claims the hold: the link drag recognizer ends the pointer stream
        // AND the touch stream, and tries to start a native drag. The armed
        // press must survive all three.
        setTimeout(() => {
          el.dispatchEvent(new PointerEvent("pointercancel", {
            bubbles: true, pointerId: 4, pointerType: "touch",
          }));
        }, 150);
        setTimeout(() => {
          el.dispatchEvent(new TouchEvent("touchcancel", {
            bubbles: true, touches: [], targetTouches: [], changedTouches: [touch],
          }));
        }, 250);
        window.__dragPrevented = null;
        setTimeout(() => {
          const link = el.querySelector("a") || el;
          const drag = new Event("dragstart", { bubbles: true, cancelable: true });
          link.dispatchEvent(drag);
          window.__dragPrevented = drag.defaultPrevented;
        }, 300);
      });
      await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
      assert(await page.evaluate(() => window.__dragPrevented) === true, "dragstart not prevented, iOS starts the native link drag");
      // Lifting must not navigate to the chip's href, with or without a
      // touchend (a cancelled stream delivers none and iOS then clicks).
      const followed = await chip.evaluate((el) => {
        const r = el.getBoundingClientRect();
        const touch = new Touch({ identifier: 1, target: el, clientX: r.x + 8, clientY: r.y + 8 });
        const ev = new TouchEvent("touchend", {
          bubbles: true, cancelable: true, touches: [], targetTouches: [], changedTouches: [touch],
        });
        el.dispatchEvent(ev);
        const click = new MouseEvent("click", { bubbles: true, cancelable: true });
        (el.querySelector("a") || el).dispatchEvent(click);
        return { touchend: !ev.defaultPrevented, click: !click.defaultPrevented };
      });
      assert(!followed.touchend, "touchend was not prevented, the lift would follow the link");
      assert(!followed.click, "the click after the hold was not suppressed");
      await sleep(300);
      assert(page.url() === before, `navigated away on lift: ${page.url()}`);
      await page.keyboard.press("Escape");
      await page.waitForSelector(".dc-context-menu", { state: "detached", timeout: 4000 });
    });

    await run("chip context menu opens on touch long-press (timer path)", async () => {
      const chip = page.locator(`#project-${project} [data-chip]:not(.d-none)`).first();
      await chip.evaluate((el) => {
        const r = el.getBoundingClientRect();
        el.dispatchEvent(new PointerEvent("pointerdown", {
          bubbles: true, cancelable: true, pointerId: 9, pointerType: "touch",
          clientX: r.x + 8, clientY: r.y + 8,
        }));
      });
      await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
      await page.keyboard.press("Escape");
      await page.waitForSelector(".dc-context-menu", { state: "detached", timeout: 4000 });
    });

    // iOS Safari raises its own contextmenu on a long press and it carries no
    // coordinates. Anchored at 0,0 the menu lands in the screen corner, which is
    // what kept it invisible on the iPhone; it must anchor on the chip instead.
    await run("iOS-style contextmenu without coordinates anchors on the chip", async () => {
      // Clear the 600ms window that swallows a contextmenu right after a
      // timer-opened menu, so this checks the coordinate handling only.
      await sleep(700);
      const chip = page.locator(`#project-${project} [data-chip]:not(.d-none)`).first();
      const box = await chip.boundingBox();
      await chip.evaluate((el) => {
        el.dispatchEvent(new MouseEvent("contextmenu", {
          bubbles: true, cancelable: true, clientX: 0, clientY: 0,
        }));
      });
      await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 4000 });
      const menu = await page.evaluate(() => {
        const r = document.querySelector(".dc-context-menu").getBoundingClientRect();
        return { x: r.x, y: r.y };
      });
      assert(menu.x > 0 || menu.y > 0, "menu anchored at 0,0 (would be invisible on iOS)");
      assert(
        Math.abs(menu.y - box.y) < box.height + 60,
        `menu not anchored near the chip: menu.y=${menu.y} chip.y=${box.y}`,
      );
      await page.keyboard.press("Escape");
      await sleep(200);
    });

    await run("chip order follows the tab strip order (@dc_tab_pos)", async () => {
      const row = `#project-${project}`;
      const chipIds = () => page.$$eval(`${row} [data-chip][data-chip-kind="shell"]`, (els) => els.map((e) => e.dataset.chipId));
      const ids = await chipIds();
      assert(ids.length >= 2, "need two shells");
      const swapped = [ids[1], ids[0]];
      const ok = await page.evaluate(async (want) => {
        const token = document.querySelector('meta[name="csrf-token"]')?.content || "";
        const r = await fetch("/terminal-tabs/order", {
          method: "POST",
          headers: { "X-CSRF-Token": token, "Content-Type": "application/json" },
          body: JSON.stringify({ ids: want }),
        });
        return r.ok;
      }, swapped);
      assert(ok, "order POST failed");
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      const after = await chipIds();
      assert(
        after.indexOf(swapped[0]) < after.indexOf(swapped[1]),
        `chips do not follow the strip order: ${JSON.stringify(after)}`,
      );
    });

    await run("delete project (data-confirm) removes the card", async () => {
      for (const u of shellUrls.splice(0)) await L.deleteShell(page, u).catch(() => {});
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      const btn = await page.evaluateHandle((p) => { const c = [...document.querySelectorAll("[data-project-name]")].find((e) => e.dataset.projectName === p); const s = c.closest('[id^="project-"]') || c; return s.querySelector('form[action="/projects/delete"] [type="submit"], form[action="/projects/delete"] button'); }, project);
      await btn.asElement().click();
      await confirmSwal(page);
      await page.waitForFunction((p) => ![...document.querySelectorAll("[data-project-name]")].some((e) => e.dataset.projectName === p), project, { timeout: 10000 });
    });
  } finally {
    for (const u of shellUrls) await L.deleteShell(page, u).catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
  }
});
