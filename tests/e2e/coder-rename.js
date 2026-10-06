const L = require("./lib");
const { assert, sleep, BASE } = L;

// Coder rename: a running coder is renamed wherever a shell is (the tab strip
// menu, a split pane head, a project chip, the editor terminal panel, and a
// click on the attach page heading). A coder's name lives in its CLI's own
// session record, so POST /coders/:id/rename types `/rename <name>` into the
// session as is, with no key before or after it, and the new name comes back
// through the turn watch's terminals event. The dialog says the command goes
// in as is and that the user cancels when that does not fit. The checks rename
// a coder with an empty input line. OpenCode's /rename takes no name, so its
// entry stands in the same places but only opens a dialog saying to run
// /rename in the coder: no name field, no POST, nothing typed into the pane.
// An inactive coder's chip offers a disabled entry saying it cannot be
// renamed, and the route refuses it with 409.
// Needs the claude, copilot and opencode CLIs on the host, like
// coder-claude.js; the rename itself is a local command of each CLI and sends
// nothing to a model. Run against a throwaway with an isolated XDG_DATA_HOME
// like coder-opencode.js, the opencode session lands in its global database.
// Gotchas: headless renders xterm on canvas, the input line is read from the
// .attach-selection mirror; the new name arrives within a watch tick, so the
// checks poll for it.

L.runFeature("CODER-RENAME", async ({ engine, page, run }) => {
  const tag = `crn-${engine}-${Date.now().toString(36)}`;
  const project = `zzcr-${tag}`;
  const ids = {};
  let shellUrl = null;

  const mirror = () => page.evaluate(() => (document.querySelector(".attach-selection") || {}).textContent || "");
  const idOf = (url) => new URL(url).pathname.split("/").pop();
  const waitReady = async () => {
    let text = "";
    for (let i = 0; i < 60; i++) {
      text = await mirror();
      if (text.includes("❯")) return;
      await sleep(1000);
    }
    throw new Error(`coder UI not ready, mirror tail: ${text.slice(-200)}`);
  };
  const pickRename = async (selector) => {
    await page.click(selector, { button: "right" });
    const item = page.locator(".dc-context-menu .dropdown-item", { hasText: /^Rename$/ }).first();
    await item.waitFor({ state: "visible", timeout: 5000 });
    await item.click();
  };
  const confirmName = async (name) => {
    await page.waitForSelector(".swal2-input", { state: "visible", timeout: 5000 });
    const note = (await page.locator(".swal2-html-container").textContent()) || "";
    assert(note.includes("/rename <new name>") && /as is/.test(note) && /which dialog is open/.test(note)
      && /cancel and type the command into the coder yourself/.test(note),
      `the dialog does not say what is sent: ${JSON.stringify(note)}`);
    assert(!/draft|set aside|comes back/i.test(note), `the dialog promises to keep a draft: ${JSON.stringify(note)}`);
    await page.fill(".swal2-input", name);
    await page.click(".swal2-confirm");
    await page.waitForSelector(".swal2-container", { state: "detached", timeout: 5000 });
  };
  const waitFor = async (probe, what) => {
    for (let i = 0; i < 40; i++) {
      if (await probe()) return;
      await sleep(500);
    }
    throw new Error(`${what} never happened`);
  };
  const renamePosts = [];
  page.on("request", (request) => {
    if (request.method() === "POST" && new URL(request.url()).pathname.endsWith("/rename")) renamePosts.push(request.url());
  });
  const insideOnly = async () => {
    await page.waitForSelector(".swal2-popup", { state: "visible", timeout: 5000 });
    const note = (await page.locator(".swal2-html-container").textContent()) || "";
    assert(/Renaming OpenCode from the cockpit is not possible/.test(note) && /Run \/rename directly in the coder/.test(note),
      `the dialog does not point to the coder: ${JSON.stringify(note)}`);
    assert((await page.locator(".swal2-input:visible").count()) === 0, "the OpenCode dialog asks for a name");
    await page.click(".swal2-confirm");
    await page.waitForSelector(".swal2-container", { state: "detached", timeout: 5000 });
  };
  const cliConfirmed = (name) => waitFor(async () => (await mirror()).includes(`renamed to: ${name}`), `the CLI confirming "${name}"`);

  await L.createProject(page, project);
  try {
    await run("claude: the strip menu renames a running coder", async () => {
      const url = await L.createSession(page, project, `rn-cl-${tag.slice(-4)}`, "claude");
      ids.claude = idOf(url);
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
      await waitReady();
      const name = `strip-${tag.slice(-4)}`;
      await pickRename(`terminal-tabs .terminal-tab[data-tab-id="${ids.claude}"]`);
      await confirmName(name);
      await waitFor(() => page.evaluate(([id, n]) => document.querySelector(`terminal-tabs .terminal-tab[data-tab-id="${id}"]`)?.dataset.tabName === n, [ids.claude, name]), "the strip taking the name");
      await waitFor(async () => (await page.locator("[data-name-label]").textContent()).trim() === name, "the heading taking the name");
      await cliConfirmed(name);
    });

    await run("claude: a click on the heading renames it", async () => {
      const name = `head-${tag.slice(-4)}`;
      await page.click("[data-name-label]");
      await confirmName(name);
      await waitFor(async () => (await page.locator("[data-name-label]").textContent()).trim() === name, "the heading taking the name");
      await waitFor(async () => (await page.title()).startsWith(name), "the browser title taking the name");
    });

    await run("copilot: the project chip menu renames it", async () => {
      const url = await L.createSession(page, project, `rn-cp-${tag.slice(-4)}`, "copilot");
      ids.copilot = idOf(url);
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
      await waitReady();
      const name = `chip-${tag.slice(-4)}`;
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      const chip = `#project-${project} .project-chip[data-chip-id="${ids.copilot}"]`;
      await page.waitForSelector(chip, { timeout: 8000 });
      await pickRename(chip);
      await confirmName(name);
      await waitFor(() => page.evaluate(([sel, n]) => document.querySelector(sel)?.dataset.chipName === n, [chip, name]), "the chip taking the name");
      await page.goto(url, { waitUntil: "domcontentloaded" });
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
      await cliConfirmed(name);
    });

    await run("opencode: strip, heading and chip open a dialog that sends and posts nothing", async () => {
      const url = await L.createSession(page, project, `rn-oc-${tag.slice(-4)}`, "opencode");
      ids.opencode = idOf(url);
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
      const before = (await page.locator("[data-name-label]").textContent()).trim();
      await pickRename(`terminal-tabs .terminal-tab[data-tab-id="${ids.opencode}"]`);
      await insideOnly();
      await page.click("[data-name-label]");
      await insideOnly();
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      const chip = `#project-${project} .project-chip[data-chip-id="${ids.opencode}"]`;
      await page.waitForSelector(chip, { timeout: 8000 });
      await pickRename(chip);
      await insideOnly();
      await sleep(1500);
      const posted = renamePosts.filter((u) => u.includes(ids.opencode));
      assert(posted.length === 0, `the OpenCode rename posted: ${posted}`);
      await page.goto(url, { waitUntil: "domcontentloaded" });
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
      await sleep(1000);
      assert(!(await mirror()).includes("/rename"), "something was typed into the OpenCode pane");
      assert((await page.locator("[data-name-label]").textContent()).trim() === before, "the OpenCode name moved");
    });

    await run("split pane head and editor terminal panel offer rename for a coder", async () => {
      shellUrl = await L.createShell(page, project);
      const group = await page.evaluate(async (list) => {
        const token = document.querySelector('meta[name="csrf-token"]').content;
        const r = await fetch("/terminal-tabs/group", { method: "POST", headers: { "X-CSRF-Token": token, "Content-Type": "application/json" }, body: JSON.stringify({ ids: list }) });
        return r.json();
      }, [ids.copilot, ids.opencode, idOf(shellUrl)].filter(Boolean));
      await page.goto(`${BASE}/splits/${group.id}`, { waitUntil: "domcontentloaded" });
      const head = `.attach-split-pane[data-pane-id="${ids.copilot}"] [data-pane-head]`;
      await page.waitForSelector(head, { timeout: 10000 });
      await page.click(head, { button: "right" });
      await page.locator(".dc-context-menu .dropdown-item", { hasText: /^Rename$/ }).first().waitFor({ state: "visible", timeout: 5000 });
      await page.keyboard.press("Escape");
      if (ids.opencode) {
        await pickRename(`.attach-split-pane[data-pane-id="${ids.opencode}"] [data-pane-head]`);
        await insideOnly();
      }
      await page.evaluate((p) => localStorage.setItem(`dc-editor-term-open:${p}`, "1"), project);
      await page.goto(`${BASE}/projects/${project}/editor`, { waitUntil: "domcontentloaded" });
      const tab = `[data-editor-term-panel] [data-term-tab="${ids.copilot}"]`;
      await page.waitForSelector(tab, { timeout: 15000 });
      await page.click(tab, { button: "right" });
      await page.locator(".dc-context-menu .dropdown-item", { hasText: /^Rename$/ }).first().waitFor({ state: "visible", timeout: 5000 });
      await page.keyboard.press("Escape");
      if (ids.opencode) {
        await pickRename(`[data-editor-term-panel] [data-term-tab="${ids.opencode}"]`);
        await insideOnly();
      }
      const posted = renamePosts.filter((u) => u.includes(ids.opencode || "-"));
      assert(posted.length === 0, `the OpenCode rename posted: ${posted}`);
    });

    await run("an inactive coder shows it cannot be renamed, the route refuses it", async () => {
      await L.stopSession(page, `${BASE}/coders/${ids.claude}`);
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      const chip = `#project-${project} .project-chip.is-idle`;
      await page.waitForSelector(chip, { timeout: 10000 });
      await page.click(chip, { button: "right" });
      const entry = page.locator(".dc-context-menu .dropdown-item", { hasText: "Inactive coders cannot be renamed" }).first();
      await entry.waitFor({ state: "visible", timeout: 5000 });
      assert(await entry.isDisabled(), "the inactive entry can be clicked");
      const plain = await page.locator(".dc-context-menu .dropdown-item", { hasText: /^Rename$/ }).count();
      assert(plain === 0, "an inactive coder offers a working rename");
      await page.keyboard.press("Escape");
      const status = await page.evaluate(async (id) => {
        const token = document.querySelector('meta[name="csrf-token"]').content;
        const r = await fetch(`/coders/${id}/rename`, { method: "POST", headers: { "X-CSRF-Token": token, "Content-Type": "application/x-www-form-urlencoded" }, body: "name=x" });
        return r.status;
      }, ids.claude);
      assert(status === 409, `rename of an inactive coder answered ${status}`);
    });
  } finally {
    try {
      if (shellUrl) await L.deleteShell(page, shellUrl);
      for (const id of Object.values(ids)) {
        await page.evaluate(async (target) => {
          const token = document.querySelector('meta[name="csrf-token"]')?.content || "";
          await fetch(`/coders/${target}/delete`, { method: "POST", headers: { "X-CSRF-Token": token, Accept: "application/json" } });
        }, id);
      }
      await L.deleteProject(page, project);
    } catch (e) { console.log("cleanup note:", e.message); }
  }
});
