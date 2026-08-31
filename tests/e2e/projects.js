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
  const source = `zzwt-${tag}`;
  const remote = `zzrm-${tag}`;
  const solo = `zzsolo-${tag}`;
  const shellUrls = [];
  const worktrees = [];
  let sourcePath = "";
  let remotePath = "";

  // git runs one command line in a project through the cockpit's own proxy
  // (POST /git), the same door `dev-cockpit git` uses. It is how this runner
  // builds real repositories without a shell of its own; the answer comes back
  // base64 with git's exit code.
  const git = async (cwd, args) => {
    const res = await page.evaluate(async ({ cwd, args }) => {
      const token = document.querySelector('meta[name="csrf-token"]')?.content || "";
      const r = await fetch("/git", {
        method: "POST",
        headers: { "X-CSRF-Token": token, "Content-Type": "application/json" },
        body: JSON.stringify({ cwd, args }),
      });
      if (!r.ok) return { failed: `${r.status} ${await r.text()}` };
      const d = await r.json();
      return { code: d.exitCode, out: atob(d.stdout || ""), err: atob(d.stderr || "") };
    }, { cwd, args });
    if (res.failed || res.code !== 0) throw new Error(`git ${args.join(" ")}: ${res.failed || res.err}`);
    return res.out;
  };

  // seedRepo turns a project directory into a repository with one commit and
  // the given extra branches. The identity is set in the repository itself,
  // the host need not carry one.
  const seedRepo = async (path, branches) => {
    await git(path, ["init", "-q", "-b", "master"]);
    await git(path, ["config", "user.email", "e2e@example.com"]);
    await git(path, ["config", "user.name", "e2e"]);
    await git(path, ["config", "commit.gpgsign", "false"]);
    await git(path, ["commit", "-q", "--allow-empty", "-m", "init"]);
    for (const branch of branches) await git(path, ["branch", branch]);
  };

  const openCreateForm = async () => {
    await page.goto(`${BASE}/projects/new?create=${encodeURIComponent(`worktree:${source}`)}`, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    // The pickers, not the resync: that one lives inside the menus now and is
    // only visible while one of them is open.
    await page.waitForSelector('[data-branch-picker="branch"]', { state: "attached", timeout: 8000 });
    await page.waitForSelector("#branch", { state: "attached", timeout: 8000 });
  };

  // rows reads what one picker is showing right now: the value each row would
  // post, the text somebody reads on it, and whether it can be picked.
  const rows = (field) => page.evaluate((sel) => {
    const menu = document.querySelector(`${sel}`).closest("[data-branch-picker]").querySelector("[data-branch-menu]");
    return [...menu.querySelectorAll("[data-branch-option]")].map((el) => ({
      value: el.dataset.branchOption,
      label: el.textContent.replace(/\s+/g, " ").trim(),
      note: (el.querySelector("[data-branch-note]")?.textContent || "").trim(),
      disabled: el.classList.contains("disabled"),
    }));
  }, field);

  // idle is the honest end of a fetch: the row gives itself back. Waiting for
  // an origin row instead proves nothing once an earlier fetch already brought
  // them in, and the check then races on while the server still holds the
  // repository, which the next git call reads as a refusal.
  const idle = (field) => page.waitForSelector(
    `[data-branch-picker="${field === "#branch" ? "branch" : "start"}"] [data-branch-resync]:not([aria-busy="true"]):not([disabled])`,
    { timeout: 30000 },
  );

  const menuRows = (field) => page.waitForFunction((sel) => {
    const menu = document.querySelector(sel).closest("[data-branch-picker]").querySelector("[data-branch-menu]");
    return menu.classList.contains("show") && !!menu.querySelector("[data-branch-option]");
  }, field, { timeout: 10000 });

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

    // This self-contained runner creates its project before exercising the list.
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

    // The create form's worktree half: a source project that is a real
    // repository, two branch autocompletes over it and the resync that brings
    // its remotes down. The repositories are built through the cockpit's own
    // git proxy (POST /git), so the runner needs no shell of its own and the
    // working copies live inside the projects it created.
    await run("worktree source: branch pickers search on the server, token by token", async () => {
      await L.createProject(page, source);
      await L.createProject(page, remote);
      sourcePath = await L.projectPath(page, source);
      remotePath = await L.projectPath(page, remote);
      await seedRepo(remotePath, ["only/on-remote"]);
      await seedRepo(sourcePath, ["topic/alpha-login", "topic/beta-logout"]);
      await git(sourcePath, ["remote", "add", "origin", remotePath]);

      await openCreateForm();
      // A click opens the list without a query: the plain answer, the
      // repository's own branches, and nothing from a remote yet.
      await page.click("#branch");
      await menuRows("#branch");
      const first = await rows("#branch");
      assert(first.some((r) => r.label.startsWith("topic/alpha-login")), `the branches are missing: ${JSON.stringify(first)}`);
      assert(!first.some((r) => r.label.startsWith("origin/")), `a branch nobody fetched is offered: ${JSON.stringify(first)}`);

      // Every token has to be contained, in any order and any case. A plain
      // substring match answers nothing for this query.
      await page.fill("#branch", "LOGIN topic");
      await page.waitForFunction(() => {
        const menu = document.querySelector('[data-branch-picker="branch"] [data-branch-menu]');
        const items = [...menu.querySelectorAll("[data-branch-option]")];
        return items.length === 1 && items[0].dataset.branchOption === "topic/alpha-login";
      }, null, { timeout: 8000 });

      // A token that matches nothing takes the whole query with it.
      await page.fill("#branch", "alpha beta");
      await page.waitForFunction(() => {
        const menu = document.querySelector('[data-branch-picker="branch"] [data-branch-menu]');
        return !menu.querySelector("[data-branch-option]") && /No matching branch/.test(menu.textContent);
      }, null, { timeout: 8000 });
    });

    // The three things that make it an autocomplete instead of a request per
    // keystroke: the round says it runs, and a slow answer to an older query
    // never paints over a newer one.
    await run("branch picker shows the round in flight and drops a stale answer", async () => {
      await openCreateForm();
      await page.route("**/branches**", async (route) => {
        if (route.request().url().includes("q=alpha")) await sleep(1500);
        // An unroute while this handler still sleeps hands the route on
        // without it, and continuing a second time throws into nobody's
        // catch, which takes the whole runner down.
        await route.continue().catch(() => {});
      });
      try {
        await page.click("#branch");
        await page.fill("#branch", "alpha");
        await page.waitForSelector('[data-branch-picker="branch"] [data-branch-searching]', { state: "visible", timeout: 4000 });
        // The slow round is out; a newer query overtakes it.
        await sleep(500);
        await page.fill("#branch", "logout");
        await page.waitForFunction(() => {
          const menu = document.querySelector('[data-branch-picker="branch"] [data-branch-menu]');
          const items = [...menu.querySelectorAll("[data-branch-option]")];
          return items.length === 1 && items[0].dataset.branchOption === "topic/beta-logout";
        }, null, { timeout: 8000 });
        // Long enough for the older answer to arrive. It must be dropped.
        await sleep(1800);
        const after = await rows("#branch");
        assert(
          after.length === 1 && after[0].label.startsWith("topic/beta-logout"),
          `a stale answer painted over the newer list: ${JSON.stringify(after)}`,
        );
      } finally {
        await page.unroute("**/branches**");
      }
    });

    await run("a branch another working copy holds is marked and cannot be picked", async () => {
      await openCreateForm();
      await page.click("#branch");
      await menuRows("#branch");
      const held = (await rows("#branch")).find((r) => r.value === "master");
      assert(held, "the source's own branch is not offered");
      assert(held.label.includes(`(in ${source})`), `the taken branch does not name its holder: ${held.label}`);
      assert(held.disabled, "the taken branch can be picked");
      const before = await page.inputValue('[data-branch-picker="branch"] [data-branch-value]');
      // Dispatched on the row itself: a real click on a disabled row lands on
      // whatever sits under it, and that would pick another branch instead of
      // proving this one cannot be picked.
      await page.locator('[data-branch-picker="branch"] [data-branch-option="master"]').dispatchEvent("click");
      await sleep(300);
      const after = await page.inputValue('[data-branch-picker="branch"] [data-branch-value]');
      assert(after === before, `a taken branch was picked anyway: ${after}`);
    });

    await run("the resync row heads the menu, above the hits, and stays put while they scroll", async () => {
      // Enough branches that the hit list has to scroll; without that the
      // check would pass on a list that never moved.
      for (let i = 0; i < 20; i++) await git(sourcePath, ["branch", `bulk/branch-${i}`]);
      await openCreateForm();
      await page.click("#branch");
      await menuRows("#branch");
      const seat = await page.evaluate(() => {
        const picker = document.querySelector('[data-branch-picker="branch"]');
        const menu = picker.querySelector("[data-branch-menu]");
        const list = picker.querySelector("[data-branch-list]");
        const row = picker.querySelector("[data-branch-resync]");
        return {
          inMenu: menu.contains(row),
          outsideTheScroller: !list.contains(row),
          aboveTheHits: row.getBoundingClientRect().bottom <= list.getBoundingClientRect().top + 0.5,
          says: row.textContent.replace(/\s+/g, " ").trim(),
        };
      });
      assert(seat.inMenu, "the resync is not a row of the menu");
      assert(seat.outsideTheScroller, "the resync scrolls away with the hits");
      assert(seat.aboveTheHits, "the resync does not head the menu");
      assert(/^Resync (fetched .+|never fetched)$/.test(seat.says), `the row does not say when it last fetched: "${seat.says}"`);

      // A list long enough to scroll must leave the row where it is.
      const moved = await page.evaluate(async () => {
        const picker = document.querySelector('[data-branch-picker="branch"]');
        const list = picker.querySelector("[data-branch-list]");
        const row = picker.querySelector("[data-branch-resync]");
        const before = row.getBoundingClientRect().top;
        list.scrollTop = list.scrollHeight;
        await new Promise((r) => requestAnimationFrame(r));
        return { scrolled: list.scrollTop, shift: Math.abs(row.getBoundingClientRect().top - before) };
      });
      assert(moved.scrolled > 0, "the hit list did not scroll, the check proves nothing");
      assert(moved.shift === 0, `the resync moved by ${moved.shift}px while the hits scrolled`);

      // And it is there when nothing matched, which is when it is looked for.
      await page.fill("#branch", "zzz-no-such-branch");
      await page.waitForFunction(() => {
        const picker = document.querySelector('[data-branch-picker="branch"]');
        return !picker.querySelector("[data-branch-option]")
          && /No matching branch/.test(picker.querySelector("[data-branch-list]").textContent);
      }, null, { timeout: 8000 });
      assert(await page.isVisible('[data-branch-picker="branch"] [data-branch-resync]'), "the resync is gone when nothing matches");
    });

    // The row is the first stop of the keyboard, but never the one a freshly
    // opened menu stands on: Enter has to mean a branch until somebody aims at
    // the fetch on purpose.
    await run("the resync row is the first keyboard stop and fetches on Enter", async () => {
      const marked = (field) => page.evaluate((sel) => {
        const root = document.querySelector(sel);
        const row = root.querySelector("[data-branch-resync]");
        const item = root.querySelector("[data-branch-list] .dropdown-item.active");
        if (row.classList.contains("active")) return "resync";
        return item ? item.dataset.branchOption : "";
      }, `[data-branch-picker="${field === "#branch" ? "branch" : "start"}"]`);

      for (const field of ["#branch", "#start"]) {
        await openCreateForm();
        if (field === "#start") {
          await page.selectOption("[data-branch-mode]", "new");
          await page.waitForSelector("#start", { state: "visible", timeout: 4000 });
        }
        await page.click(field);
        await menuRows(field);
        const opened = await marked(field);
        assert(opened && opened !== "resync", `${field}: a freshly opened menu marks the resync row (${opened})`);

        // The topmost branch, then one step up onto the row.
        await page.keyboard.press("ArrowUp");
        assert(await marked(field) === "resync", `${field}: ArrowUp from the top branch did not reach the resync row`);
        await page.keyboard.press("ArrowDown");
        const back = await marked(field);
        assert(back === opened, `${field}: ArrowDown from the row did not go back into the list (${back})`);
      }

      // Enter on the row fetches and leaves the menu standing, and the mark
      // stays on the row so a second Enter fetches again.
      await openCreateForm();
      await page.click("#branch");
      await menuRows("#branch");
      const before = await page.getAttribute('[data-branch-picker="branch"] [data-branch-value]', "value");
      await page.keyboard.press("ArrowUp");
      assert(await marked("#branch") === "resync", "the row is not marked before Enter");
      await page.keyboard.press("Enter");
      await idle("#branch");
      await page.waitForFunction(() => {
        const picker = document.querySelector('[data-branch-picker="branch"]');
        return picker.querySelector("[data-branch-menu]").classList.contains("show")
          && !!picker.querySelector('[data-branch-option^="origin/"]');
      }, null, { timeout: 20000 });
      assert(await marked("#branch") === "resync", "Enter moved the mark off the row it fetched from");
      const after = await page.getAttribute('[data-branch-picker="branch"] [data-branch-value]', "value");
      assert(after === before, `Enter on the resync row picked a branch (${before} -> ${after})`);
      await page.keyboard.press("Escape");
    });

    // The mark used to walk out of the box: the focus stays in the field, so
    // nothing scrolls by itself the way it did while this was a select.
    await run("the arrow keys keep the marked row in sight, never behind the resync row", async () => {
      const walk = async (field, steps) => {
        const picker = `[data-branch-picker="${field === "#branch" ? "branch" : "start"}"]`;
        await page.click(field);
        await menuRows(field);
        const seen = new Set();
        for (const key of ["ArrowDown", "ArrowUp"]) {
          for (let i = 0; i < steps; i++) {
            await page.keyboard.press(key);
            const at = await page.evaluate((sel) => {
              const root = document.querySelector(sel);
              const list = root.querySelector("[data-branch-list]");
              const head = root.querySelector("[data-branch-resync]");
              // The row is a stop of its own and sits outside the list, so it
              // has nothing to stay clear of.
              if (head.classList.contains("active")) {
                const h = head.getBoundingClientRect();
                return {
                  value: "resync",
                  inList: true,
                  belowHead: true,
                  inWindow: h.top >= -0.5 && h.bottom <= window.innerHeight + 0.5,
                };
              }
              const item = list.querySelector(".dropdown-item.active");
              if (!item) return null;
              const i = item.getBoundingClientRect();
              const l = list.getBoundingClientRect();
              const h = head.getBoundingClientRect();
              return {
                value: item.dataset.branchOption,
                // Half a pixel of slack, the boxes are fractional.
                inList: i.top >= l.top - 0.5 && i.bottom <= l.bottom + 0.5,
                belowHead: i.top >= h.bottom - 0.5,
                inWindow: i.top >= -0.5 && i.bottom <= window.innerHeight + 0.5,
              };
            }, picker);
            assert(at, `${field}: nothing is marked after ${key} ${i + 1}`);
            seen.add(at.value);
            assert(at.inList, `${field}: "${at.value}" left the box on ${key} ${i + 1}`);
            assert(at.belowHead, `${field}: "${at.value}" sits behind the resync row on ${key} ${i + 1}`);
            assert(at.inWindow, `${field}: "${at.value}" left the window on ${key} ${i + 1}`);
          }
        }
        return seen;
      };

      await openCreateForm();
      // The list has to be longer than the menu or the walk proves nothing.
      const long = await page.evaluate(async () => {
        const input = document.querySelector("#branch");
        input.focus();
        await new Promise((r) => setTimeout(r, 900));
        const list = document.querySelector('[data-branch-picker="branch"] [data-branch-list]');
        return list.scrollHeight > list.clientHeight + 4;
      });
      assert(long, "the hit list is not longer than the menu, the walk proves nothing");
      const branchSeen = await walk("#branch", 12);
      assert(branchSeen.size >= 6, `the mark barely moved: ${JSON.stringify([...branchSeen])}`);

      // The starting point is the same picker and gets the same walk.
      await page.keyboard.press("Escape");
      await page.selectOption("[data-branch-mode]", "new");
      await page.waitForSelector("#start", { state: "visible", timeout: 4000 });
      const startSeen = await walk("#start", 12);
      assert(startSeen.size >= 6, `the start mark barely moved: ${JSON.stringify([...startSeen])}`);
      await page.keyboard.press("Escape");
    });

    await run("resync fetches the source in place, the menu stays open and the remotes appear grouped", async () => {
      await openCreateForm();
      await page.click("#branch");
      await menuRows("#branch");
      await page.route("**/fetch", async (route) => { await sleep(900); await route.continue().catch(() => {}); });
      const restTops = await page.evaluate(() =>
        [...document.querySelectorAll("[data-branch-block], [name=project_name], .card-footer, [data-branch-resync]")]
          .map((el) => el.getBoundingClientRect().top));
      try {
        await page.click('[data-branch-picker="branch"] [data-branch-resync]');
        await page.waitForSelector('[data-branch-picker="branch"] [data-branch-resync][aria-busy="true"] [data-branch-resync-icon].dc-spin', { timeout: 4000 });
        // The busy state may not move anything: the icon turns, no node is
        // swapped in, so the row and every field under it stand where they stood.
        const moved = await page.evaluate((before) => {
          const now = [...document.querySelectorAll("[data-branch-block], [name=project_name], .card-footer, [data-branch-resync]")]
            .map((el) => el.getBoundingClientRect().top);
          return now.map((top, i) => Math.abs(top - before[i]));
        }, restTops);
        assert(moved.length && moved.every((d) => d === 0), `the busy state moved the page by ${JSON.stringify(moved)}px`);
        // The list is answered again where it stands: the menu never closed.
        // This waits inside the route, so the delayed handler is through
        // before the route is taken away again.
        await idle("#branch");
        await page.waitForFunction(() => {
          const picker = document.querySelector('[data-branch-picker="branch"]');
          return picker.querySelector("[data-branch-menu]").classList.contains("show")
            && !!picker.querySelector('[data-branch-option^="origin/"]');
        }, null, { timeout: 20000 });
      } finally {
        await page.unroute("**/fetch");
      }
      const after = await rows("#branch");
      assert(after.some((r) => r.value === "origin/only/on-remote"), `the fetched branch is not offered: ${JSON.stringify(after)}`);
      assert(!after.some((r) => r.value === "origin/master"), `a remote branch whose local branch exists is offered twice: ${JSON.stringify(after)}`);
      const says = await page.textContent('[data-branch-picker="branch"] [data-branch-fetched]');
      assert(/^fetched /.test(says.trim()), `the row did not take the new fetch time: "${says}"`);
      const grouped = await page.evaluate(() => {
        const list = document.querySelector('[data-branch-picker="branch"] [data-branch-list]');
        const header = list.querySelector(".dropdown-header");
        if (!header) return { header: "" };
        const nodes = [...list.children];
        const at = nodes.indexOf(header);
        return { header: header.textContent.trim(), remoteAfter: (nodes[at + 1] || {}).dataset?.branchOption || "" };
      });
      assert(grouped.header === "On a remote", `the remote branches are not grouped: ${JSON.stringify(grouped)}`);
      assert(grouped.remoteAfter.startsWith("origin/"), `the group label does not head the remote rows: ${JSON.stringify(grouped)}`);
    });

    // A fetch reaches the network and may fail. It has to say so in git's own
    // words and give the button back, the form is not over.
    await run("a resync that fails says so and leaves the form standing", async () => {
      await git(sourcePath, ["remote", "set-url", "origin", "/nonexistent/repository.git"]);
      try {
        await openCreateForm();
        await page.click("#branch");
        await menuRows("#branch");
        await page.click('[data-branch-picker="branch"] [data-branch-resync]');
        await page.waitForSelector('.dc-toast:has-text("nonexistent")', { state: "visible", timeout: 20000 });
        await idle("#branch");
        await page.waitForFunction(() => {
          const button = document.querySelector('[data-branch-picker="branch"] [data-branch-resync]');
          const menu = document.querySelector('[data-branch-picker="branch"] [data-branch-menu]');
          return button && !button.disabled && !button.querySelector(".dc-spin") && menu.classList.contains("show");
        }, null, { timeout: 8000 });
      } finally {
        await git(sourcePath, ["remote", "set-url", "origin", remotePath]);
      }
    });

    // A branch name says nothing about how old the place behind it is. The row
    // carries the distance to the branch it follows, so a worktree is not
    // started three commits behind without a word.
    await run("a local branch row says how far it stands from its upstream", async () => {
      // The two repositories were seeded apart and their histories are not
      // related: whether their empty init commits share a hash depends only on
      // the second they were made in, same tree, no parent, same identity, same
      // message. A distance read off that is a coin toss, so the local branch is
      // put onto the remote's history here, once, before anything is measured.
      // It cannot happen at seed time, the checks above have to see a source
      // that has never fetched.
      await git(sourcePath, ["fetch", "-q"]);
      await git(sourcePath, ["reset", "--hard", "origin/master"]);
      // The source was made with init and a remote added, so nothing follows
      // anything yet. The upstream can only be set once the ref is here.
      await git(sourcePath, ["branch", "--set-upstream-to", "origin/master", "master"]);
      // Now the remote moves twice and this copy never pulls, which is the one
      // thing the row has to report.
      await git(remotePath, ["commit", "-q", "--allow-empty", "-m", "one"]);
      await git(remotePath, ["commit", "-q", "--allow-empty", "-m", "two"]);
      await git(sourcePath, ["fetch", "-q"]);
      // The fixture says what it built, so a broken one names itself instead of
      // failing as a wrong number on a row.
      const gap = async (range) => (await git(sourcePath, ["rev-list", "--count", range])).trim();
      assert(await gap("origin/master..master") === "0", "the fixture left commits on the local branch");
      assert(await gap("master..origin/master") === "2", "the fixture did not put the remote two commits ahead");
      await openCreateForm();
      await page.click("#branch");
      await menuRows("#branch");
      await page.fill("#branch", "master");
      await page.waitForFunction(() => {
        const list = document.querySelector('[data-branch-picker="branch"] [data-branch-list]');
        return [...list.querySelectorAll("[data-branch-option]")].some((el) => el.dataset.branchOption === "master");
      }, null, { timeout: 8000 });
      const master = (await rows("#branch")).find((r) => r.value === "master");
      assert(master, "master is not offered");
      assert(master.note === "2 behind origin/master", `the row does not carry the distance: "${master.note}"`);

      // A branch that follows nothing says nothing.
      const alpha = (await page.evaluate(async () => {
        const input = document.querySelector("#branch");
        input.value = "alpha";
        input.dispatchEvent(new Event("input", { bubbles: true }));
        await new Promise((r) => setTimeout(r, 900));
        const el = document.querySelector('[data-branch-option="topic/alpha-login"]');
        return el ? (el.querySelector("[data-branch-note]")?.textContent || "").trim() : null;
      }));
      assert(alpha === "", `a branch without an upstream carries a distance: "${alpha}"`);
    });

    // Branching off a head that has only fallen behind means branching off an
    // old commit. The default moves to the upstream, which is the same history
    // further along; a head with commits of its own keeps the start, or they
    // would be dropped without a word.
    await run("starting at defaults to the upstream when the head only fell behind", async () => {
      await openCreateForm();
      await page.selectOption("[data-branch-mode]", "new");
      const behind = await page.inputValue('[data-branch-picker="start"] [data-branch-value]');
      assert(behind === "origin/master", `a head that only fell behind starts at ${behind}`);
      // The start list is the one place a remote branch stands beside its local
      // one: the two have drifted apart, and they are different starting points.
      await page.click("#start");
      await menuRows("#start");
      await page.fill("#start", "master");
      await page.waitForFunction(() => {
        const list = document.querySelector('[data-branch-picker="start"] [data-branch-list]');
        const values = [...list.querySelectorAll("[data-branch-option]")].map((el) => el.dataset.branchOption);
        return values.includes("master") && values.includes("origin/master");
      }, null, { timeout: 8000 });

      // One commit of its own and the local branch keeps the start.
      await git(sourcePath, ["commit", "-q", "--allow-empty", "-m", "mine"]);
      await openCreateForm();
      await page.selectOption("[data-branch-mode]", "new");
      const diverged = await page.inputValue('[data-branch-picker="start"] [data-branch-value]');
      assert(diverged === "master", `a diverged head must keep its own commits, started at ${diverged}`);
    });

    await run("starting at is the same autocomplete, and takes a branch that is checked out elsewhere", async () => {
      await openCreateForm();
      await page.selectOption("[data-branch-mode]", "new");
      await page.waitForSelector("#start", { state: "visible", timeout: 4000 });
      await page.click("#start");
      await menuRows("#start");
      const all = await rows("#start");
      const head = all.find((r) => r.value === "master");
      assert(head && !head.disabled, `the source's own branch cannot start a new one: ${JSON.stringify(head)}`);
      assert(all.some((r) => r.value.startsWith("origin/")), `the start picker has no remotes: ${JSON.stringify(all)}`);
      await page.fill("#start", "logout topic");
      await page.waitForFunction(() => {
        const menu = document.querySelector('[data-branch-picker="start"] [data-branch-menu]');
        const items = [...menu.querySelectorAll("[data-branch-option]")];
        return items.length === 1 && items[0].dataset.branchOption === "topic/beta-logout";
      }, null, { timeout: 8000 });
    });

    // The two modes are one select now. The half that has nothing behind it is
    // closed rather than leading into an empty list: a project with a single
    // branch holds that branch itself, which is the ordinary state.
    await run("the branch mode is a select, and its existing half closes when every branch is taken", async () => {
      await openCreateForm();
      const both = await page.evaluate(() => {
        const select = document.querySelector("[data-branch-mode]");
        return {
          tag: select.tagName,
          name: select.getAttribute("name"),
          values: [...select.options].map((o) => `${o.value}${o.disabled ? ":disabled" : ""}`),
          value: select.value,
        };
      });
      assert(both.tag === "SELECT", `the branch mode is a ${both.tag}`);
      assert(both.name === "branch_mode", `the field name changed to ${both.name}`);
      assert(JSON.stringify(both.values) === JSON.stringify(["existing", "new"]), `the options are ${JSON.stringify(both.values)}`);
      assert(both.value === "existing", `the form opens on ${both.value} with free branches around`);
      // Switching swaps the blocks, which is what the select is for.
      await page.selectOption("[data-branch-mode]", "new");
      await page.waitForSelector("#new_branch", { state: "visible", timeout: 4000 });
      assert(!(await page.isVisible("#branch")), "the existing block stayed on screen");
      await page.selectOption("[data-branch-mode]", "existing");
      await page.waitForSelector("#branch", { state: "visible", timeout: 4000 });
      assert(!(await page.isVisible("#new_branch")), "the new block stayed on screen");

      // A repository with one branch holds it in the source project itself, so
      // there is nothing left to check out.
      await L.createProject(page, solo);
      const soloPath = await L.projectPath(page, solo);
      await seedRepo(soloPath, []);
      await page.goto(`${BASE}/projects/new?create=${encodeURIComponent(`worktree:${solo}`)}`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      await page.waitForSelector("[data-branch-mode]", { state: "attached", timeout: 8000 });
      const taken = await page.evaluate(() => {
        const select = document.querySelector("[data-branch-mode]");
        const existing = [...select.options].find((o) => o.value === "existing");
        return { value: select.value, existingDisabled: existing.disabled };
      });
      assert(taken.value === "new", `a source with nothing to check out opens on ${taken.value}`);
      assert(taken.existingDisabled, "the existing half is still selectable with nothing to check out");
      assert(await page.isVisible("#new_branch"), "the new branch block is not showing");
    });

    // What is chosen has to stay readable. Focusing selects instead of
    // emptying, a remote hit shows the branch it creates plus where it came
    // from, and leaving without a choice puts the choice back.
    await run("the field keeps showing what is chosen, and names the remote a pick came from", async () => {
      await openCreateForm();
      const shown = await page.inputValue("#branch");
      assert(shown && !shown.startsWith("origin/"), `the field opens on a raw ref: "${shown}"`);
      await page.click("#branch");
      const onFocus = await page.evaluate(() => {
        const el = document.querySelector("#branch");
        return { value: el.value, selected: el.value.slice(el.selectionStart, el.selectionEnd) };
      });
      assert(onFocus.value === shown, `focusing emptied the field: "${onFocus.value}"`);
      assert(onFocus.selected === shown, `focusing did not select the choice: "${onFocus.selected}"`);

      // A branch that only exists on a remote is shown by the branch it makes,
      // with its remote said beside the label.
      await menuRows("#branch");
      await page.fill("#branch", "only");
      await page.waitForSelector('[data-branch-option="origin/only/on-remote"]', { timeout: 8000 });
      await page.click('[data-branch-option="origin/only/on-remote"]');
      await sleep(200);
      assert(await page.inputValue("#branch") === "only/on-remote", `the field shows the ref, not the branch: "${await page.inputValue("#branch")}"`);
      assert(await page.inputValue('[data-branch-picker="branch"] [data-branch-value]') === "origin/only/on-remote", "the posted value is not the ref");
      const from = await page.textContent("[data-branch-from]");
      assert(from.trim() === "from origin", `the remote is not named: "${from}"`);

      // Typing without picking is a query, and leaving puts the choice back.
      await page.click("#branch");
      await page.fill("#branch", "zzz");
      await sleep(500);
      await page.click("h2.page-title");
      await sleep(300);
      assert(await page.inputValue("#branch") === "only/on-remote", "the field kept a query instead of the choice");
    });

    // A branch that only fell behind can be brought up on the way in. The offer
    // stands exactly where doing so is a fast forward, and the create really
    // moves the new working copy.
    await run("a branch that is purely behind is offered a catch up, and gets one", async () => {
      // A free branch that follows the remote and sits two commits behind it.
      // Free, because the offer is about a branch somebody can actually pick.
      await git(sourcePath, ["branch", "-f", "catch/up", "origin/master~2"]);
      await git(sourcePath, ["branch", "--set-upstream-to=origin/master", "catch/up"]);
      await openCreateForm();
      await page.click("#branch");
      await menuRows("#branch");
      await page.fill("#branch", "catch up");
      await page.waitForSelector('[data-branch-option="catch/up"]', { timeout: 8000 });
      await page.click('[data-branch-option="catch/up"]');
      await sleep(300);
      const offered = await page.evaluate(() => {
        const row = document.querySelector("[data-branch-ff]");
        const box = document.querySelector("[data-branch-ff-input]");
        return { hidden: row.hidden, label: row.textContent.replace(/\s+/g, " ").trim(), checked: box.checked, disabled: box.disabled };
      });
      assert(!offered.hidden, "no catch up offered for a branch that only fell behind");
      assert(offered.label === "Fast-forward to origin/master after creating", `the catch up does not name its target: "${offered.label}"`);
      assert(offered.checked && !offered.disabled, `the catch up is not ticked and live: ${JSON.stringify(offered)}`);

      // A branch from a remote is created here and stands behind nothing.
      await page.click("#branch");
      await menuRows("#branch");
      await page.fill("#branch", "only");
      await page.waitForSelector('[data-branch-option="origin/only/on-remote"]', { timeout: 8000 });
      await page.click('[data-branch-option="origin/only/on-remote"]');
      await sleep(300);
      assert(await page.evaluate(() => document.querySelector("[data-branch-ff]").hidden), "the catch up is offered for a branch from a remote");
      assert(await page.isDisabled("[data-branch-ff-input]"), "a hidden catch up would still be posted");

      // And the whole way through: pick it again and create.
      const name = `${source}-ff`;
      await page.click("#branch");
      await menuRows("#branch");
      await page.fill("#branch", "catch up");
      await page.waitForSelector('[data-branch-option="catch/up"]', { timeout: 8000 });
      await page.click('[data-branch-option="catch/up"]');
      await sleep(200);
      assert(await page.isChecked("[data-branch-ff-input]"), "the catch up came back unticked");
      await page.fill('input[name="project_name"]', name);
      worktrees.push(name);
      await Promise.all([
        page.waitForURL(/\/projects/, { timeout: 20000 }),
        submitBtn(page, 'input[name="project_name"]').click(),
      ]);
      await page.waitForSelector(`[data-project-name="${name}"]`, { timeout: 10000 });
      const made = sourcePath.replace(source, name);
      const head = (await git(made, ["rev-parse", "HEAD"])).trim();
      const upstream = (await git(sourcePath, ["rev-parse", "origin/master"])).trim();
      assert(head === upstream, `the new working copy was not caught up: ${head} vs ${upstream}`);
      assert((await git(made, ["rev-parse", "--abbrev-ref", "HEAD"])).trim() === "catch/up", "the new working copy is on the wrong branch");
    });

    await run("create a worktree on a picked existing branch", async () => {
      const name = `${source}-wt1`;
      await openCreateForm();
      await page.click("#branch");
      await menuRows("#branch");
      await page.fill("#branch", "alpha");
      await page.waitForSelector('[data-branch-picker="branch"] [data-branch-option="topic/alpha-login"]', { timeout: 8000 });
      await page.click('[data-branch-picker="branch"] [data-branch-option="topic/alpha-login"]');
      assert(
        await page.inputValue('[data-branch-picker="branch"] [data-branch-value]') === "topic/alpha-login",
        "the picked branch did not reach the field the form posts",
      );
      assert(await page.inputValue("#branch") === "topic/alpha-login", "the field does not show the picked branch");
      await page.fill('input[name="project_name"]', name);
      worktrees.push(name);
      await Promise.all([
        page.waitForURL(/\/projects/, { timeout: 20000 }),
        submitBtn(page, 'input[name="project_name"]').click(),
      ]);
      await page.waitForSelector(`[data-project-name="${name}"]`, { timeout: 10000 });
      const branch = await git(sourcePath.replace(source, name), ["rev-parse", "--abbrev-ref", "HEAD"]);
      assert(branch.trim() === "topic/alpha-login", `the working copy stands on ${branch}`);
    });

    await run("create a worktree on a new branch starting at a picked one", async () => {
      const name = `${source}-wt2`;
      await openCreateForm();
      await page.selectOption("[data-branch-mode]", "new");
      await page.fill("#new_branch", "wip/e2e");
      await page.click("#start");
      await menuRows("#start");
      await page.fill("#start", "logout");
      await page.waitForSelector('[data-branch-picker="start"] [data-branch-option="topic/beta-logout"]', { timeout: 8000 });
      await page.click('[data-branch-picker="start"] [data-branch-option="topic/beta-logout"]');
      assert(
        await page.inputValue('[data-branch-picker="start"] [data-branch-value]') === "topic/beta-logout",
        "the picked start did not reach the field the form posts",
      );
      await page.fill('input[name="project_name"]', name);
      worktrees.push(name);
      await Promise.all([
        page.waitForURL(/\/projects/, { timeout: 20000 }),
        submitBtn(page, 'input[name="project_name"]').click(),
      ]);
      await page.waitForSelector(`[data-project-name="${name}"]`, { timeout: 10000 });
      const path = sourcePath.replace(source, name);
      const branch = await git(path, ["rev-parse", "--abbrev-ref", "HEAD"]);
      assert(branch.trim() === "wip/e2e", `the new branch is not checked out: ${branch}`);
      const base = await git(path, ["rev-parse", "topic/beta-logout"]);
      const head = await git(path, ["rev-parse", "HEAD"]);
      assert(base.trim() === head.trim(), "the new branch does not start where it was told to");
    });

    await run("delete project shows a toast and removes the card without a redirect", async () => {
      for (const u of shellUrls.splice(0)) await L.deleteShell(page, u).catch(() => {});
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      const btn = await page.evaluateHandle((p) => { const c = [...document.querySelectorAll("[data-project-name]")].find((e) => e.dataset.projectName === p); const s = c.closest('[id^="project-"]') || c; return s.querySelector('form[action="/projects/delete"] [type="submit"], form[action="/projects/delete"] button'); }, project);
      await btn.asElement().click();
      await confirmSwal(page);
      await page.waitForSelector('.dc-toast:has-text("Project")', { state: "visible", timeout: 8000 });
      await page.waitForSelector(`#project-${project}`, { state: "detached", timeout: 10000 });
      assert(page.url().endsWith("/projects"), `project deletion redirected: ${page.url()}`);
    });
  } finally {
    for (const u of shellUrls) await L.deleteShell(page, u).catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
    // The worktrees go before the repository they belong to: deleting the
    // source takes them with it, and a runner must leave nothing either way.
    for (const name of worktrees) await L.deleteProject(page, name).catch(() => {});
    await L.deleteProject(page, source).catch(() => {});
    await L.deleteProject(page, remote).catch(() => {});
    await L.deleteProject(page, solo).catch(() => {});
  }
});
