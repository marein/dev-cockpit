const L = require("./lib");
const { assert, sleep, confirmSwal, BASE } = L;

// Shells: plain throwaway terminals, the safe target. Custom elements terminal-attach,
// terminal-input, terminal-setting-select, dc-inline-rename, dc-project-select. The shared
// terminal interaction (typing, controls, copy, scroll) is in terminal.js; this
// covers what is shell specific. The create form also stands in the app wide
// create dialog (dc-form-modal): it asks for the same GET with modal=1 and posts
// to the same path. Routes: GET /shells/new, POST /shells/new,
// GET /shells/:id, POST /shells/:id/{delete,rename,input,resize}, GET .../stream.

L.runFeature("SHELLS", async ({ page, run, mobilePage }) => {
  const tag = `shell-${Date.now().toString(36)}`;
  const project = `zztc-${tag}`;
  let shellUrl = null;
  try {
    await L.createProject(page, project);

    // The project select stands in the order the projects page is in, out of the
    // shared "dc-project-sort" key, and the preselection follows that order. A
    // form opened from a project keeps that project wherever it ends up.
    await run("project select follows the projects page sort order", async () => {
      await page.goto(`${BASE}/shells/new`, { waitUntil: "domcontentloaded" });
      await page.evaluate(() => localStorage.setItem("dc-project-sort", "recent"));
      try {
        await page.goto(`${BASE}/shells/new`, { waitUntil: "domcontentloaded" });
        assert((await L.waitUpgraded(page, ["dc-project-select"], 8000)).length === 0, "dc-project-select not upgraded");
        const options = await page.evaluate(() => [...document.querySelectorAll('select[name="project"] option')]
          .map((o) => ({ name: o.dataset.projectName || "", used: Number(o.dataset.projectUsed) || 0, value: o.value })));
        assert(options.length > 0, "the select carries no projects");
        for (let i = 1; i < options.length; i++) {
          const a = options[i - 1];
          const b = options[i];
          const ok = a.used > b.used || (a.used === b.used && a.name.toLowerCase() <= b.name.toLowerCase());
          assert(ok, `not in recent order: ${a.name}(${a.used}) before ${b.name}(${b.used})`);
        }
        const first = await page.$eval('select[name="project"]', (sel) => sel.value);
        assert(first === options[0].value, `preselection ${first} is not the first option ${options[0].value}`);
        await page.goto(`${BASE}/shells/new?project=${encodeURIComponent(project)}`, { waitUntil: "domcontentloaded" });
        await L.waitUpgraded(page, ["dc-project-select"], 8000);
        const pinned = await page.$eval('select[name="project"]', (sel) => sel.value);
        assert(pinned.split("/").pop() === project, `opened from ${project}, but ${pinned} is selected`);
      } finally {
        await page.evaluate(() => localStorage.removeItem("dc-project-sort"));
      }
    });

    await run("create shell -> attach page + dc-inline-rename upgraded", async () => {
      shellUrl = await L.createShell(page, project);
      assert(/\/shells\/(?!new)[^/]+$/.test(shellUrl), `bad url ${shellUrl}`);
      assert((await L.waitUpgraded(page, ["terminal-attach", "terminal-input", "dc-inline-rename"], 12000)).length === 0, "not upgraded");
    });

    await run("scroll-history is set on attach + input (shell history scroll)", async () => {
      const ok = await page.evaluate(() => document.getElementById("terminal")?.hasAttribute("scroll-history") && document.querySelector("terminal-input")?.hasAttribute("scroll-history"));
      assert(ok, "scroll-history attribute missing");
    });

    await run("inline rename (CSRF header path) persists across reload", async () => {
      const name = `renamed-${tag.slice(-5)}`;
      await page.click("[data-rename-label]");
      await page.waitForSelector("[data-rename-input]:not(.d-none)", { timeout: 4000 });
      await page.fill("[data-rename-input]", name);
      await page.keyboard.press("Enter"); await sleep(800);
      await page.reload({ waitUntil: "domcontentloaded" });
      assert((await page.textContent("[data-rename-label]")).trim() === name, "rename not persisted");
    });

    // The dialog element upgrades lazily like every custom element, and a click
    // that beats it lands on the form page, which is the fallback and not what
    // these checks are about.
    const dialogReady = async (target) => {
      assert((await L.waitUpgraded(target, ["dc-form-modal"], 8000)).length === 0, "dc-form-modal not upgraded");
    };

    // The create form opens in a dialog, from every way that used to lead to
    // the form page: the dialog fetches the same /shells/new with modal=1 and
    // posts to the same path. The page stays a page, the check above opens it
    // directly and creates through it.
    await run("dialog: the tab strip + menu and the sheet both open the create form", async () => {
      await page.goto(shellUrl, { waitUntil: "domcontentloaded" });
      await dialogReady(page);
      await page.click("terminal-tabs .terminal-tabs-new-btn");
      await page.click('terminal-tabs .dropdown-menu.show a[href^="/shells/new"]');
      await page.waitForSelector("[data-form-modal].show form", { timeout: 8000 });
      await sleep(600);
      const desktop = await page.evaluate(() => ({
        path: window.location.pathname,
        title: document.querySelector("[data-form-modal] .modal-title").textContent.trim(),
        action: document.querySelector("[data-form-modal] form").getAttribute("action"),
        focused: document.activeElement?.getAttribute("name") || "",
        menu: Boolean(document.querySelector("terminal-tabs .dropdown-menu.show")),
      }));
      assert(desktop.path.startsWith("/shells/"), `the page moved to ${desktop.path}`);
      assert(desktop.title === "Start shell", `dialog head reads ${desktop.title}`);
      assert(desktop.action.startsWith("/shells/new"), `the form posts to ${desktop.action}`);
      assert(!desktop.menu, "the + menu stayed open behind the dialog");
      assert(desktop.focused === "project", `focus sits on ${desktop.focused || "nothing"}`);
      await page.keyboard.press("Escape");
      await page.waitForFunction(() => !document.querySelector("[data-form-modal].show"), null, { timeout: 6000 });
      await sleep(400);
      const closed = await page.evaluate(() => ({
        forms: document.querySelectorAll("[data-form-modal] form").length,
        backdrops: document.querySelectorAll(".modal-backdrop").length,
      }));
      assert(closed.forms === 0 && closed.backdrops === 0, `the dialog left something behind: ${JSON.stringify(closed)}`);

      // The sheet is the phone's way in, and there no field may take the
      // focus, a keyboard would cover the dialog the moment it opens.
      const mp = await mobilePage();
      await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await dialogReady(mp);
      await mp.tap('.dc-tabbar button[data-ctx-area="terminals"]');
      await mp.waitForSelector("dc-ctx-sheet:not([hidden]) [data-tabs-new-menu]", { timeout: 8000 });
      await mp.click("dc-ctx-sheet [data-tabs-new-menu]");
      await mp.click('dc-ctx-sheet a[href^="/shells/new"]');
      await mp.waitForSelector("[data-form-modal].show form", { timeout: 8000 });
      await sleep(600);
      const mobile = await mp.evaluate(() => {
        const submit = document.querySelector('[data-form-modal] button[type="submit"]').getBoundingClientRect();
        return {
          path: window.location.pathname,
          menu: !document.querySelector("dc-ctx-sheet").hidden,
          focused: document.activeElement?.getAttribute("name") || "",
          overflow: document.documentElement.scrollWidth > window.innerWidth,
          submitReachable: submit.width > 0 && submit.bottom <= window.innerHeight,
        };
      });
      assert(mobile.path === "/projects", `the phone left the page for ${mobile.path}`);
      assert(!mobile.menu, "the sheet stayed open behind the dialog");
      assert(mobile.focused === "", `a touch keyboard would pop up on ${mobile.focused}`);
      assert(!mobile.overflow && mobile.submitReachable, `not usable at 390: ${JSON.stringify(mobile)}`);
      await mp.keyboard.press("Escape").catch(() => {});
      return "+ menu and sheet, focus on the desktop and none on the phone";
    });

    // A refused create is the whole reason the dialog talks to the server
    // itself: the message belongs in the dialog and the chosen values have to
    // survive it. The select only offers projects that exist, so the value the
    // server refuses is planted.
    await run("dialog: a refused create shows the message and keeps the values", async () => {
      await page.goto(shellUrl, { waitUntil: "domcontentloaded" });
      await dialogReady(page);
      await page.click("terminal-tabs .terminal-tabs-new-btn");
      await page.click('terminal-tabs .dropdown-menu.show a[href^="/shells/new"]');
      await page.waitForSelector("[data-form-modal].show form", { timeout: 8000 });
      await sleep(400);
      await page.evaluate(() => {
        const select = document.querySelector('[data-form-modal] select[name="project"]');
        const option = document.createElement("option");
        option.value = "/zz-no-such-project";
        select.appendChild(option);
        select.value = option.value;
      });
      await page.click('[data-form-modal] button[type="submit"]');
      await page.waitForSelector("[data-form-modal] [data-form-modal-error]", { timeout: 8000 });
      const state = await page.evaluate(() => ({
        path: window.location.pathname,
        open: document.querySelector("[data-form-modal]").classList.contains("show"),
        error: document.querySelector("[data-form-modal-error]").textContent.trim(),
        project: document.querySelector('[data-form-modal] select[name="project"]').value,
        submitting: document.querySelector('[data-form-modal] button[type="submit"]').disabled,
      }));
      assert(state.path.startsWith("/shells/") && state.open, `thrown out of the dialog: ${JSON.stringify(state)}`);
      assert(state.error.length > 0, "no message in the dialog");
      assert(state.project === "/zz-no-such-project", `the chosen project is gone: ${state.project}`);
      assert(!state.submitting, "the submit button stayed disabled after the refusal");
      await page.keyboard.press("Escape");
      await page.waitForFunction(() => !document.querySelector("[data-form-modal].show"), null, { timeout: 6000 });
      return state.error;
    });

    await run("dialog: a create lands on the new shell's page", async () => {
      await page.goto(shellUrl, { waitUntil: "domcontentloaded" });
      await dialogReady(page);
      await page.click("terminal-tabs .terminal-tabs-new-btn");
      await page.click('terminal-tabs .dropdown-menu.show a[href^="/shells/new"]');
      await page.waitForSelector("[data-form-modal].show form", { timeout: 8000 });
      await sleep(400);
      await page.selectOption('[data-form-modal] select[name="project"]', { label: project });
      // The page under the dialog is a shell page already, so the landing is
      // the move to a different one.
      const from = new URL(page.url()).pathname;
      await page.click('[data-form-modal] button[type="submit"]');
      await page.waitForURL((u) => /\/shells\/(?!new)[^/]+$/.test(u.pathname) && u.pathname !== from, { timeout: 15000 });
      const created = page.url();
      // The dialog takes its fade to close, so the landing is what is waited
      // for and the empty dialog is read after it.
      await page.waitForFunction(() => !document.querySelector("[data-form-modal] form"), null, { timeout: 6000 });
      const left = await page.evaluate(() => ({
        open: document.querySelectorAll("[data-form-modal].show").length,
        backdrops: document.querySelectorAll(".modal-backdrop").length,
        locked: document.body.classList.contains("modal-open"),
      }));
      assert(left.open === 0 && left.backdrops === 0 && !left.locked, `the dialog stayed behind: ${JSON.stringify(left)}`);
      // Cleanup through the route: deleting from the strip has its own check
      // right below, and its handle goes stale under WebKit now and then, which
      // would fail this check for something it is not about.
      await page.evaluate(async (id) => {
        await fetch(`/shells/${id}/delete`, { method: "POST", headers: { "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]').content } });
      }, new URL(created).pathname.split("/").pop());
      await sleep(600);
      return created;
    });

    // The attach header carries the delete on touch only, so the desktop way
    // is the tab strip's close control; both ask the same confirm.
    await run("delete shell from the tab strip redirects + cleans up, no ended toast", async () => {
      await page.goto(shellUrl, { waitUntil: "domcontentloaded" });
      const id = new URL(shellUrl).pathname.split("/").pop();
      await page.click(`terminal-tabs .terminal-tab[data-tab-id="${id}"] [data-tab-close]`);
      await confirmSwal(page);
      await page.waitForURL((u) => !new RegExp(new URL(shellUrl).pathname + "$").test(u.toString()), { timeout: 10000 });
      await sleep(800);
      assert((await page.locator(".dc-toast .ti-alert-circle").count()) === 0, "error toast after user delete");
      const toasts = (await page.locator(".dc-toast").allTextContents()).join(" ");
      assert(!toasts.includes("Terminal has ended"), "ended toast not suppressed on user delete");
      shellUrl = null;
    });

    await run("typing exit ends the shell with an info toast, not an error", async () => {
      await L.createShell(page, project);
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
      await page.click("#terminal");
      await page.keyboard.type("exit");
      await page.keyboard.press("Enter");
      await page.waitForSelector(".dc-toast", { timeout: 10000 });
      const text = (await page.locator(".dc-toast").allTextContents()).join(" ");
      assert(text.includes("Terminal has ended"), `unexpected toast: ${text}`);
      assert((await page.locator(".dc-toast .ti-info-circle").count()) === 1, "ended toast is not info");
      assert((await page.locator(".dc-toast .ti-alert-circle").count()) === 0, "ended toast rendered as error");
    });

    // A shell leaves nothing behind, so nothing is offered: the route knows it
    // was asked about a shell and says that much, and never a resume, which
    // only a coder session has.
    await run("input to a shell that is not running says so and offers nothing", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      const answer = await page.evaluate(async (t) => {
        const res = await fetch(`/shells/${t}/input`, {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            Accept: "application/json",
            "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]').content,
          },
          body: JSON.stringify({ items: [{ text: "x" }] }),
        });
        return { status: res.status, body: await res.text() };
      }, `zznone${tag.slice(-4)}`);
      assert(answer.status === 410, `a shell that is gone answered ${answer.status}: ${answer.body}`);
      assert(/shell is not running/i.test(answer.body), `the answer does not name the shell: ${answer.body}`);
      assert(/cannot be resumed/i.test(answer.body), `the answer does not say nothing can be brought back: ${answer.body}`);
      assert(!/coder-resume/.test(answer.body), `browser answer carries a CLI command: ${answer.body}`);
    });
  } finally {
    if (shellUrl) await L.deleteShell(page, shellUrl).catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
  }
});
