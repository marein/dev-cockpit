const L = require("./lib");
const { assert, sleep, submitBtn, confirmSwal, modalShown, BASE } = L;

// Sessions: the provider agent runtime, plus resumable sessions and session files.
// Custom elements terminal-attach, terminal-input, terminal-scroll-zone,
// terminal-direction-pad, coder-file-upload, terminal-setting-select,
// dc-project-select. The
// shared terminal interaction is in terminal.js. Routes:
// GET/POST /coders/new (the create dialog asks for the same GET with modal=1 and
// posts to the same path, dc-form-modal), GET /sessions/:id, POST /sessions/:id/{stop,input,resize},
// GET .../stream, .../files (+POST upload, /download, /delete), POST /coders/:id/{resume,delete}.
// Creates a real provider session and stops it; safe because it is our own throwaway.

L.runFeature("SESSIONS", async ({ page, run, mobilePage }) => {
  const tag = `sess-${Date.now().toString(36)}`;
  const project = `zztc-${tag}`;
  const agentId = `tcagent-${tag.slice(-5)}`;
  let sessionUrl = null;
  try {
    await L.createProject(page, project);
    // an agent so we can assert it populates the session agent select
    await page.goto(`${BASE}/agents/new`, { waitUntil: "domcontentloaded" });
    await page.fill('input[name="agent_id"]', agentId); await page.fill('input[name="agent_description"]', "test"); await page.fill('textarea[name="agent_instructions"]', "test");
    await Promise.all([page.waitForURL(/\/agents(\?coder=\w+)?$/, { timeout: 10000 }), submitBtn(page, 'input[name="agent_id"]').click()]);

    await run("new session form renders fields + agent select is populated", async () => {
      await page.goto(`${BASE}/coders/new?project=${encodeURIComponent(project)}`, { waitUntil: "domcontentloaded" });
      const has = await page.evaluate(() => ({ name: !!document.querySelector('input[name="name"]'), project: !!document.querySelector('select[name="project"]'), agent: !!document.querySelector('select[name="agent"]'), approval: !!document.querySelector('input[name="automatic_approval"]') }));
      assert(Object.values(has).every(Boolean), `missing fields: ${JSON.stringify(has)}`);
      const agentOption = await page.evaluate((id) => [...document.querySelectorAll('select[name="agent"] option')].some((o) => o.value === id || o.textContent.includes(id)), agentId);
      assert(agentOption, "created agent not in the agent select");
    });

    // The name is optional. The form asks for none, and a coder started
    // without one runs under the label the cockpit falls back to until there
    // is a prompt to name it after.
    await run("a coder starts without a name", async () => {
      await page.goto(`${BASE}/coders/new?project=${encodeURIComponent(project)}`, { waitUntil: "domcontentloaded" });
      const field = await page.evaluate(() => ({
        required: document.querySelector('input[name="name"]').required,
        label: document.querySelector('label[for="name"]').className,
      }));
      assert(!field.required, "the name field is still required");
      assert(!/required/.test(field.label), `the label still marks the name required: ${field.label}`);
      const form = page.locator('form:has(select[name="agent"])').first();
      await Promise.all([
        page.waitForURL(/\/coders\/(?!new)[^/]+$/, { timeout: 20000 }),
        form.locator('button[type="submit"]').first().click(),
      ]);
      const created = page.url();
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
      const label = (await page.textContent("[data-name-label]")).trim();
      assert(label.length > 0, "an unnamed coder has no label at all");
      await page.evaluate(async (id) => {
        const token = document.querySelector('meta[name="csrf-token"]').content;
        await fetch(`/coders/${id}/stop`, { method: "POST", headers: { "X-CSRF-Token": token } });
        await fetch(`/coders/${id}/delete`, { method: "POST", headers: { "X-CSRF-Token": token } });
      }, new URL(created).pathname.split("/").pop());
      await sleep(800);
      return label;
    });

    // The project select stands in the order the projects page is in, out of the
    // shared "dc-project-sort" key, and the preselection follows that order,
    // unless the form was opened from a project. Same select as in shells.js.
    await run("project select follows the projects page sort order", async () => {
      await page.goto(`${BASE}/coders/new`, { waitUntil: "domcontentloaded" });
      await page.evaluate(() => localStorage.setItem("dc-project-sort", "recent"));
      try {
        await page.goto(`${BASE}/coders/new`, { waitUntil: "domcontentloaded" });
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
        await page.goto(`${BASE}/coders/new?project=${encodeURIComponent(project)}`, { waitUntil: "domcontentloaded" });
        await L.waitUpgraded(page, ["dc-project-select"], 8000);
        const pinned = await page.$eval('select[name="project"]', (sel) => sel.value);
        assert(pinned.split("/").pop() === project, `opened from ${project}, but ${pinned} is selected`);
      } finally {
        await page.evaluate(() => localStorage.removeItem("dc-project-sort"));
      }
    });

    // The dialog element upgrades lazily like every custom element, and a click
    // that beats it lands on the form page, which is the fallback and not what
    // these checks are about.
    const dialogReady = async (target) => {
      assert((await L.waitUpgraded(target, ["dc-form-modal"], 8000)).length === 0, "dc-form-modal not upgraded");
    };

    // The create form opens in a dialog, from every way that used to lead to
    // the form page: the dialog fetches the same /coders/new with modal=1 and
    // posts to the same path. The page stays a page, the two checks above open
    // it directly and read its fields.
    await run("dialog: the projects chip and the sheet both open the create form", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await dialogReady(page);
      await page.click(`#project-${project} a[href^="/coders/new"]`);
      await page.waitForSelector("[data-form-modal].show form", { timeout: 8000 });
      await sleep(600);
      const desktop = await page.evaluate(() => ({
        path: window.location.pathname,
        title: document.querySelector("[data-form-modal] .modal-title").textContent.trim(),
        action: document.querySelector("[data-form-modal] form").getAttribute("action"),
        focused: document.activeElement?.getAttribute("name") || "",
        project: document.querySelector('[data-form-modal] select[name="project"]').value,
      }));
      assert(desktop.path === "/projects", `the page moved to ${desktop.path}`);
      assert(desktop.title === "Start coder", `dialog head reads ${desktop.title}`);
      assert(desktop.action.startsWith("/coders/new"), `the form posts to ${desktop.action}`);
      assert(desktop.focused === "name", `focus sits on ${desktop.focused || "nothing"}`);
      assert(desktop.project.endsWith(`/${project}`), `the chip's project is not preselected: ${desktop.project}`);
      // Escape closes and the focus goes back to the chip that opened it. The
      // show class goes at the start of the fade, the focus travels when the
      // dialog is really hidden, so the read waits out the transition.
      await page.keyboard.press("Escape");
      await page.waitForFunction(() => !document.querySelector("[data-form-modal] form"), null, { timeout: 8000 });
      await sleep(400);
      const closed = await page.evaluate(() => ({
        focus: document.activeElement?.getAttribute("href") || "",
        forms: document.querySelectorAll("[data-form-modal] form").length,
        backdrops: document.querySelectorAll(".modal-backdrop").length,
      }));
      assert(closed.focus.startsWith("/coders/new"), `focus did not return to the chip: ${closed.focus}`);
      assert(closed.forms === 0 && closed.backdrops === 0, `the dialog left something behind: ${JSON.stringify(closed)}`);

      // The sheet is the phone's way in, and there no field may take the
      // focus, a keyboard would cover the dialog the moment it opens.
      const mp = await mobilePage();
      await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await dialogReady(mp);
      await mp.tap('.dc-tabbar button[data-ctx-area="terminals"]');
      await mp.waitForSelector("dc-ctx-sheet:not([hidden]) [data-tabs-new-menu]", { timeout: 8000 });
      await mp.click("dc-ctx-sheet [data-tabs-new-menu]");
      await mp.click('dc-ctx-sheet a[href^="/coders/new"]');
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
      // With the keyboard open a phone leaves about half the screen, and the
      // dialog scrolls, so the submit is still reachable.
      await mp.setViewportSize({ width: 390, height: 420 });
      await sleep(500);
      await mp.locator('[data-form-modal] button[type="submit"]').scrollIntoViewIfNeeded();
      await sleep(300);
      const cramped = await mp.evaluate(() => {
        const box = document.querySelector('[data-form-modal] button[type="submit"]').getBoundingClientRect();
        return { visible: box.top >= 0 && box.bottom <= window.innerHeight, scrolls: getComputedStyle(document.querySelector("[data-form-modal]")).overflowY };
      });
      await mp.setViewportSize({ width: 390, height: 844 });
      assert(cramped.visible, `the submit is out of reach with the keyboard open: ${JSON.stringify(cramped)}`);
      assert(cramped.scrolls === "auto" || cramped.scrolls === "scroll", `the dialog does not scroll: ${cramped.scrolls}`);
      await mp.keyboard.press("Escape").catch(() => {});
      return "chip and sheet, focus on the desktop and none on the phone";
    });

    // A refused create is the whole reason the dialog talks to the server
    // itself: the message belongs in the dialog and the typed values have to
    // survive it. The select only offers projects that exist, so the value the
    // server refuses is planted.
    await run("dialog: a refused create shows the message and keeps the values", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await dialogReady(page);
      await page.click(`#project-${project} a[href^="/coders/new"]`);
      await page.waitForSelector("[data-form-modal].show form", { timeout: 8000 });
      await sleep(400);
      const typed = `tcref-${tag.slice(-4)}`;
      await page.fill('[data-form-modal] input[name="name"]', typed);
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
        name: document.querySelector('[data-form-modal] input[name="name"]').value,
        submitting: document.querySelector('[data-form-modal] button[type="submit"]').disabled,
      }));
      assert(state.path === "/projects" && state.open, `thrown out of the dialog: ${JSON.stringify(state)}`);
      assert(state.error.length > 0, "no message in the dialog");
      assert(state.name === typed, `the typed name is gone: ${state.name}`);
      assert(!state.submitting, "the submit button stayed disabled after the refusal");
      await page.keyboard.press("Escape");
      await page.waitForFunction(() => !document.querySelector("[data-form-modal].show"), null, { timeout: 6000 });
      return state.error;
    });

    // While the create runs the form gives way to a spinner and one line; the
    // create is held back so that state can be read.
    await run("dialog: a create shows the wait in place of the form, then lands on the new coder's page", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await dialogReady(page);
      await page.click(`#project-${project} a[href^="/coders/new"]`);
      await page.waitForSelector("[data-form-modal].show form", { timeout: 8000 });
      await sleep(400);
      await page.fill('[data-form-modal] input[name="name"]', `tcdlg-${tag.slice(-4)}`);
      await page.route("**/coders/new?*", async (route) => {
        if (route.request().method() !== "POST") return route.continue().catch(() => {});
        await new Promise((resolve) => setTimeout(resolve, 2000));
        await route.continue().catch(() => {});
      });
      let created;
      try {
        await page.click('[data-form-modal] button[type="submit"]');
        await page.waitForSelector("[data-form-modal] [data-form-modal-wait] .spinner-border", { state: "visible", timeout: 5000 });
        const waiting = await page.evaluate(() => ({
          formHidden: document.querySelector("[data-form-modal] form").hidden,
          line: document.querySelector("[data-form-modal] [data-form-modal-wait]").textContent.trim(),
          centered: getComputedStyle(document.querySelector("[data-form-modal] [data-form-modal-wait]")).textAlign,
          buttons: document.querySelectorAll("[data-form-modal] .btn-loading").length,
        }));
        assert(waiting.formHidden, "the form still shows under the wait");
        assert(/Starting the coder/.test(waiting.line), `the wait line reads ${JSON.stringify(waiting.line)}`);
        assert(waiting.centered === "center" && waiting.buttons === 0, `the wait is not the spinner and a line: ${JSON.stringify(waiting)}`);
        await page.waitForURL(/\/coders\/(?!new)[^/]+$/, { timeout: 20000 });
        created = page.url();
      } finally {
        await page.unroute("**/coders/new?*").catch(() => {});
      }
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
      // The dialog takes its fade to close, so the landing is what is waited
      // for and the empty dialog is read after it.
      await page.waitForFunction(() => !document.querySelector("[data-form-modal] form"), null, { timeout: 6000 });
      const left = await page.evaluate(() => ({
        open: document.querySelectorAll("[data-form-modal].show").length,
        backdrops: document.querySelectorAll(".modal-backdrop").length,
        locked: document.body.classList.contains("modal-open"),
      }));
      assert(left.open === 0 && left.backdrops === 0 && !left.locked, `the dialog stayed behind: ${JSON.stringify(left)}`);
      // Cleanup through the routes: stopping and deleting from the UI has its
      // own checks below, and the stored session must go with it, the runner's
      // instance shares the coder's real store.
      await page.evaluate(async (id) => {
        const token = document.querySelector('meta[name="csrf-token"]').content;
        await fetch(`/coders/${id}/stop`, { method: "POST", headers: { "X-CSRF-Token": token } });
        await fetch(`/coders/${id}/delete`, { method: "POST", headers: { "X-CSRF-Token": token } });
      }, new URL(created).pathname.split("/").pop());
      await sleep(800);
      return created;
    });

    await run("create -> attach elements + canvas", async () => {
      sessionUrl = await L.createSession(page, project, `tcsess-${tag.slice(-4)}`);
      assert((await L.waitUpgraded(page, ["terminal-attach", "terminal-input", "coder-file-upload", "terminal-setting-select"], 12000)).length === 0, "not upgraded");
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
      await sleep(2000);
    });

    await run("files: multi upload -> Done -> list, reference (Copied), download, delete", async () => {
      await page.goto(sessionUrl, { waitUntil: "domcontentloaded" });
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 12000 });
      await page.click(".attach-desktop .coder-files-button");
      await modalShown(page, "coder-files-modal");
      const f1 = `u1_${tag.slice(-4)}.txt`, f2 = `u2_${tag.slice(-4)}.txt`, content = `payload ${tag}\n`;
      // Picking sends right away, the button only opens the file chooser.
      await page.setInputFiles('#coder-files-modal input[type="file"][name="files"]', [{ name: f1, mimeType: "text/plain", buffer: Buffer.from(content) }, { name: f2, mimeType: "text/plain", buffer: Buffer.from("two\n") }]);
      await page.waitForFunction(() => document.querySelectorAll('#coder-files-modal [data-file-index]').length >= 2, null, { timeout: 8000 });
      await page.waitForFunction(() => { const s = [...document.querySelectorAll('#coder-files-modal [data-file-status]')].map((e) => e.textContent.trim()); return s.length >= 2 && s.every((x) => x === "Done"); }, null, { timeout: 15000 });
      await page.waitForFunction((ns) => { const c = document.querySelector("[data-coder-files-content]"); return c && ns.every((n) => c.textContent.includes(n)); }, [f1, f2], { timeout: 10000 });
      // reference + download the first file
      const copyBtn = page.locator("#coder-files-modal [data-copy-file-path]").first();
      await copyBtn.click();
      await page.waitForFunction(() => /Copied/.test((document.querySelector("[data-copy-file-path]") || {}).innerHTML || ""), null, { timeout: 3000 });
      const href = await page.locator('#coder-files-modal a[href*="/files/download"]').first().getAttribute("href");
      const dl = await page.context().request.get(BASE + href);
      assert(dl.status() === 200 && (await dl.text()).includes(content.trim()), "download mismatch");
      // delete one; wait for the delete POST (the confirm is a swal targeted into the
      // modal) then for a download link to drop. The success fragment echoes the name,
      // so count the rows, do not text-match.
      const beforeLinks = await page.locator('#coder-files-modal a[href*="/files/download"]').count();
      await page.click("#coder-files-modal form[data-coder-file-delete] button[type=\"submit\"]");
      await page.waitForSelector(".swal2-confirm", { state: "visible", timeout: 8000 });
      const respP = page.waitForResponse((r) => /\/files\/delete$/.test(r.url()) && r.request().method() === "POST", { timeout: 10000 });
      await sleep(150); await page.click(".swal2-confirm");
      assert((await respP).status() < 400, "delete POST failed");
      await page.waitForFunction((n) => document.querySelectorAll('#coder-files-modal a[href*="/files/download"]').length < n, beforeLinks, { timeout: 10000 });
      await page.keyboard.press("Escape").catch(() => {});
    });

    // A paste into the open dialog takes the same path as a pick: it uploads,
    // the hidden input is never something the user has to submit.
    await run("files: a paste into the open dialog uploads right away", async () => {
      await page.goto(sessionUrl, { waitUntil: "domcontentloaded" });
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 12000 });
      await page.click(".attach-desktop .coder-files-button");
      await modalShown(page, "coder-files-modal");
      const f3 = `p1_${tag.slice(-4)}.txt`;
      const carried = await page.evaluate((name) => {
        const data = new DataTransfer();
        data.items.add(new File(["pasted one\n"], name, { type: "text/plain" }));
        if (!data.files.length) return false;
        document.getElementById("coder-files-modal").dispatchEvent(new ClipboardEvent("paste", { clipboardData: data, bubbles: true, cancelable: true }));
        return true;
      }, f3);
      assert(carried, "the engine builds no file clipboard");
      await page.waitForFunction((n) => { const c = document.querySelector("[data-coder-files-content]"); return c && c.textContent.includes(n); }, f3, { timeout: 15000 });
      await page.keyboard.press("Escape").catch(() => {});
    });

    // The file input is hidden, so the button is the way to it: with nothing
    // picked it opens the file chooser instead of posting an empty form.
    await run("files: upload with nothing picked opens the file chooser", async () => {
      await page.goto(sessionUrl, { waitUntil: "domcontentloaded" });
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 12000 });
      await page.click(".attach-desktop .coder-files-button");
      await modalShown(page, "coder-files-modal");
      assert(!(await page.locator('#coder-files-modal input[type="file"][name="files"]').isVisible()), "the file input is on the page next to the button");
      const posts = [];
      const watch = (r) => { if (/\/files$/.test(r.url()) && r.method() === "POST") posts.push(r.url()); };
      page.on("request", watch);
      try {
        const chooser = page.waitForEvent("filechooser", { timeout: 6000 });
        await page.click('#coder-files-modal [data-coder-file-upload-form] button[type="submit"]');
        assert(await chooser, "no file chooser opened");
        await sleep(400);
        assert(posts.length === 0, `the empty upload still posted: ${posts.join(" ")}`);
        assert((await page.locator("#coder-files-modal .alert-danger").count()) === 0, "the empty upload answered with an error");
      } finally {
        page.off("request", watch);
      }
      await page.keyboard.press("Escape").catch(() => {});
    });

    // On the terminal a file clipboard takes the drop path, and the text part of
    // that same clipboard must never arrive in the pane as keystrokes.
    await run("files: a paste onto the terminal uploads like a drop, nothing types", async () => {
      await page.goto(sessionUrl, { waitUntil: "domcontentloaded" });
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 12000 });
      await sleep(1000);
      const f4 = `p2_${tag.slice(-4)}.txt`;
      const typed = [];
      const watch = (r) => { if (/\/input$/.test(r.url()) && r.method() === "POST") typed.push(r.postData() || ""); };
      page.on("request", watch);
      try {
        const carried = await page.evaluate((name) => {
          const data = new DataTransfer();
          data.items.add(new File(["pasted two\n"], name, { type: "text/plain" }));
          data.setData("text/plain", name);
          if (!data.files.length) return false;
          document.querySelector(".xterm-helper-textarea").dispatchEvent(new ClipboardEvent("paste", { clipboardData: data, bubbles: true, cancelable: true }));
          return true;
        }, f4);
        assert(carried, "the engine builds no file clipboard");
        await modalShown(page, "coder-files-modal");
        await page.waitForFunction((n) => { const c = document.querySelector("[data-coder-files-content]"); return c && c.textContent.includes(n); }, f4, { timeout: 15000 });
        await sleep(500);
        assert(typed.length === 0, `the file clipboard reached the pane: ${typed.join(" ")}`);
      } finally {
        page.off("request", watch);
      }
      await page.keyboard.press("Escape").catch(() => {});
    });

    await run("legacy /sessions URLs redirect to /coders", async () => {
      await page.goto(`${BASE}/sessions/new?project=${encodeURIComponent(project)}`, { waitUntil: "domcontentloaded" });
      assert(page.url().includes("/coders/new"), `not redirected: ${page.url()}`);
    });

    await run("resumable: stop -> resumable entry -> resume -> delete", async () => {
      // Every resume/delete selector is scoped to the scratch project card:
      // /projects lists the real projects' resumables too, and an unscoped
      // .first() resumes or deletes someone else's stored session.
      const card = `#project-${project}`;
      await L.stopSession(page, sessionUrl); sessionUrl = null;
      assert((await page.locator(".dc-toast .ti-alert-circle").count()) === 0, "error toast after user stop");
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(`${card} form[action^="/coders/"][action$="/resume"]`, { timeout: 8000 });
      await Promise.all([page.waitForURL(/\/coders\/(?!new)[^/]+$/, { timeout: 20000 }), page.locator(`${card} form[action$="/resume"]`).first().locator('button[type="submit"]').first().click()]);
      sessionUrl = page.url();
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
      await L.stopSession(page, sessionUrl); sessionUrl = null;
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      const before = await page.locator(`${card} form[action^="/coders/"][action$="/delete"]`).count();
      assert(before >= 1, "no resumable to delete");
      await page.locator(`${card} form[action^="/coders/"][action$="/delete"]`).first().locator('button[type="submit"]').first().click();
      await confirmSwal(page); await sleep(800);
      assert(await page.locator(`${card} form[action^="/coders/"][action$="/delete"]`).count() < before, "resumable row not removed");
    });

    // The chip only carries stop, delete lives in its context menu and takes a
    // running coder in one request: the server stops it before it drops the
    // conversation, so it must not come back as a resumable row.
    await run("chip context menu deletes a running coder, stop first", async () => {
      const card = `#project-${project}`;
      const url = await L.createSession(page, project, `chipdel-${tag.slice(-4)}`);
      const id = url.split("/").filter(Boolean).pop();
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      const chip = page.locator(`${card} [data-chip][data-chip-kind="coder"][data-chip-id="${id}"]`);
      await chip.waitFor({ state: "visible", timeout: 10000 });
      await chip.click({ button: "right" });
      await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
      await page.click('.dc-context-menu button:text-is("Delete")');
      await confirmSwal(page);
      await page.waitForSelector(`${card} [data-chip][data-chip-id="${id}"]`, { state: "detached", timeout: 10000 });
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await sleep(500);
      assert((await page.locator(`${card} form[action="/coders/${id}/resume"]`).count()) === 0, "the deleted coder is still resumable");
    });

    // The input route is shared: the assistant reaches it over the local socket
    // and is told to run `coder-resume`, a browser must never read a command it
    // cannot type on a phone. Both are answered from the same classified state,
    // so this checks the browser half of it: what is wrong, and nothing offered
    // where there is nothing to offer.
    await run("input to a coder that is not running says why, in browser words", async () => {
      const probe = (target) => page.evaluate(async (t) => {
        const res = await fetch(`/coders/${t}/input`, {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            Accept: "application/json",
            "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]').content,
          },
          body: JSON.stringify({ items: [{ prompt: "ping" }] }),
        });
        return { status: res.status, body: await res.text() };
      }, target);

      const url = await L.createSession(page, project, `gone-${tag.slice(-4)}`);
      const id = url.split("/").filter(Boolean).pop();
      await L.stopSession(page, url);

      const stopped = await probe(id);
      assert(stopped.status === 410, `a stopped coder answered ${stopped.status}: ${stopped.body}`);
      assert(/not running/i.test(stopped.body), `the stopped coder's answer says nothing: ${stopped.body}`);
      assert(/resume/i.test(stopped.body), `the way back is not named: ${stopped.body}`);
      assert(!stopped.body.includes("coder-resume"), `browser answer carries a CLI command: ${stopped.body}`);

      const unknown = await probe(`zznone${tag.slice(-4)}`);
      assert(unknown.status === 410, `an unknown id answered ${unknown.status}: ${unknown.body}`);
      assert(/no session to resume/i.test(unknown.body), `an unknown id claims something to resume: ${unknown.body}`);
      assert(!/coder-resume/.test(unknown.body), `browser answer carries a CLI command: ${unknown.body}`);
    });
  } finally {
    if (sessionUrl) await L.stopSession(page, sessionUrl).catch(() => {});
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" }).catch(() => {});
    for (let i = 0; i < 4; i++) { const d = page.locator(`#project-${project} form[action^="/coders/"][action$="/delete"]`).first(); if (await d.count() === 0) break; await d.locator("button").first().click().catch(() => {}); await confirmSwal(page).catch(() => {}); await sleep(500); await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" }).catch(() => {}); }
    const af = await page.$(`form[action="/agents/${agentId}/delete"], form[action="/agents/${encodeURIComponent(agentId)}/delete"]`).catch(() => null);
    if (af) { await (await af.$("button")).click().catch(() => {}); await confirmSwal(page).catch(() => {}); await sleep(400); }
    await L.deleteProject(page, project).catch(() => {});
  }
});
