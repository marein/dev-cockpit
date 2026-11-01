const L = require("./lib");
const { assert, BASE } = L;

// Forge binding: which forge a project is bound to and how (internal/forge
// bindings.go). `Forge` in a repository's git menu, on the board row and in
// the list column's row menu, opens the project's forge in the create dialog
// (dc-form-modal, GET/POST /projects/:name/forge): the binding with its
// brand, repository path, link and host, then how it binds. From the origin,
// set by hand to a host and a repository path, whose fields dc-forge-binding
// keeps disabled while another way is picked, or off. A worktree shows its
// main project's binding and changes none. The board row's origin
// ([data-project-origin]) follows over the projects event. The origin is set
// with git through the cockpit's own proxy (POST /git) and never reached:
// nothing here asks a forge.

const DIALOG = "[data-form-modal].show";

L.runFeature("FORGE-BINDING", async ({ page, run, mobilePage }) => {
  const stamp = Date.now().toString(36);
  const project = `zzbind-${stamp}`;
  const worktree = `${project}-wt`;
  const repo = `e2e/bind-${stamp}`;
  let path = "";

  const git = async (args) => {
    const res = await page.evaluate(async ({ cwd, args }) => {
      const token = document.querySelector('meta[name="csrf-token"]')?.content || "";
      const r = await fetch("/git", {
        method: "POST",
        headers: { "X-CSRF-Token": token, "Content-Type": "application/json" },
        body: JSON.stringify({ cwd, args }),
      });
      if (!r.ok) return { failed: `${r.status} ${await r.text()}` };
      const d = await r.json();
      return { code: d.exitCode, err: atob(d.stderr || "") };
    }, { cwd: path, args });
    if (res.failed || res.code !== 0) throw new Error(`git ${args.join(" ")}: ${res.failed || res.err}`);
  };

  const seed = async () => {
    await L.createProject(page, project);
    path = await L.projectPath(page, project);
    await git(["init", "-q", "-b", "master"]);
    await git(["config", "user.email", "e2e@example.com"]);
    await git(["config", "user.name", "e2e"]);
    await git(["config", "commit.gpgsign", "false"]);
    await git(["commit", "-q", "--allow-empty", "-m", "init"]);
    await git(["remote", "add", "origin", `git@github.com:${repo}.git`]);
  };

  const openBoard = async (p = page) => {
    await p.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(p);
    await p.waitForSelector(`#project-${project} [data-git-project-menu]`, { state: "visible", timeout: 8000 });
    await L.waitUpgraded(p, ["dc-form-modal", "dc-project-list"]);
  };

  const openForge = async (name, p = page) => {
    await p.click(`#project-${name} [data-git-project-menu]`);
    await p.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
    await p.click('.dc-context-menu .dropdown-item:has-text("Forge")');
    await p.waitForSelector(`${DIALOG} [data-project-forge-form]`, { state: "visible", timeout: 8000 });
    await p.waitForFunction((t) => document.querySelector("[data-form-modal-title]")?.textContent.trim() === t, `Forge of ${name}`, { timeout: 8000 });
  };

  // dialog reads the open forge dialog as it stands: the binding with its
  // painted brand, how it binds, whether the fields of the binding by hand
  // are disabled, their values and the hints.
  const dialog = () => page.evaluate((sel) => {
    const root = document.querySelector(sel);
    const text = (s) => root.querySelector(s)?.textContent.replace(/\s+/g, " ").trim() ?? null;
    const link = root.querySelector("[data-forge-bound-link]");
    const icon = root.querySelector("[data-forge-bound-icon]");
    const box = icon?.getBoundingClientRect();
    const glyph = icon ? getComputedStyle(icon, "::before").content : "";
    const field = root.querySelector('input[name="path"]');
    return {
      title: document.querySelector("[data-form-modal-title]")?.textContent.trim(),
      bound: link ? {
        href: link.getAttribute("href"),
        text: link.textContent.trim(),
        host: text("[data-forge-bound-host]"),
        icons: [...icon.classList].filter((c) => c.startsWith("ti-")),
        painted: box.width > 0 && box.height > 0 && glyph !== "none" && glyph !== "",
      } : null,
      unbound: text("[data-forge-unbound]"),
      how: root.querySelector('input[name="how"]:checked')?.value ?? "",
      radios: root.querySelectorAll('input[name="how"]').length,
      handDisabled: field ? field.matches(":disabled") && root.querySelector('select[name="host"]').matches(":disabled") : null,
      host: root.querySelector('select[name="host"]')?.value ?? null,
      path: field?.value ?? null,
      origin: text("[data-forge-origin]"),
      worktree: text("[data-forge-worktree]"),
      save: Boolean(root.querySelector("[data-forge-binding-save]")),
    };
  }, DIALOG);

  const dialogError = async () => {
    await page.waitForSelector(`${DIALOG} [data-form-modal-error]`, { state: "visible", timeout: 8000 });
    return (await page.textContent(`${DIALOG} [data-form-modal-error]`)).trim();
  };

  // save posts the dialog and waits for it to close over its toast.
  const save = async () => {
    await page.click(`${DIALOG} [data-forge-binding-save]`);
    await page.waitForSelector(".dc-toast:has-text('Forge saved.')", { state: "visible", timeout: 8000 });
    await page.waitForFunction(() => !document.querySelector("[data-form-modal].show"), null, { timeout: 8000 });
  };

  // origin waits for the board row's origin to lead to href, null for none,
  // and reads it: its link, its text and its painted icon.
  const origin = async (href) => {
    await page.waitForFunction(({ id, href }) => {
      const el = document.querySelector(`#project-${id} [data-project-meta] [data-project-origin]`);
      return href === null ? !el : el?.getAttribute("href") === href;
    }, { id: project, href }, { timeout: 8000 });
    return page.evaluate((id) => {
      const el = document.querySelector(`#project-${id} [data-project-meta] [data-project-origin]`);
      if (!el) return null;
      const icon = el.querySelector("i.ti");
      const box = icon.getBoundingClientRect();
      const glyph = getComputedStyle(icon, "::before").content;
      return {
        href: el.getAttribute("href"),
        text: el.textContent.trim(),
        icons: [...icon.classList].filter((c) => c.startsWith("ti-")),
        painted: box.width > 0 && box.height > 0 && glyph !== "none" && glyph !== "",
      };
    }, project);
  };

  try {
    await run("setup: a repository whose origin is on github.com", async () => {
      await seed();
    });

    await run("git menu: Forge opens the dialog with where the origin binds the project", async () => {
      await openBoard();
      await openForge(project);
      const d = await dialog();
      assert(d.bound && d.bound.href === `https://github.com/${repo}` && d.bound.text === repo && d.bound.host === "GitHub at https://github.com", `the binding reads ${JSON.stringify(d.bound)}`);
      assert(d.bound.icons.join(" ") === "ti-brand-github" && d.bound.painted, `the brand reads ${JSON.stringify(d.bound)}`);
      assert(d.how === "origin" && d.origin === `github.com/${repo} on GitHub`, `how reads ${d.how}, the origin "${d.origin}"`);
      assert(d.handDisabled === true && d.host === "github.com" && d.path === repo, `the binding by hand reads ${JSON.stringify(d)}`);
    });

    await run("set by hand: picking it frees the fields, a path without group is refused with the values kept", async () => {
      await page.check(`${DIALOG} input[name="how"][value="hand"]`);
      assert((await dialog()).handDisabled === false, "the fields of the binding by hand stay disabled");
      await page.selectOption(`${DIALOG} select[name="host"]`, "gitlab.com");
      await page.fill(`${DIALOG} input[name="path"]`, "app");
      await page.click(`${DIALOG} [data-forge-binding-save]`);
      const said = await dialogError();
      assert(said === '"app" is no repository path, like group/app.', `refused with "${said}"`);
      const d = await dialog();
      assert(d.how === "hand" && d.host === "gitlab.com" && d.path === "app" && d.handDisabled === false, `the dialog after the refusal reads ${JSON.stringify(d)}`);
    });

    await run("set by hand: the save closes the dialog and the row wears GitLab and links the repository there", async () => {
      await page.fill(`${DIALOG} input[name="path"]`, "/e2e/sub/app.git");
      await save();
      const got = await origin("https://gitlab.com/e2e/sub/app");
      assert(got.icons.join(" ") === "ti-brand-gitlab" && got.painted && got.text === `github.com/${repo}`, `the row reads ${JSON.stringify(got)}`);
    });

    await run("list column: the row menu's Forge opens the binding by hand", async () => {
      await page.click(`.dc-app > .dc-ctx [data-index-project="${project}"]`, { button: "right" });
      await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
      await page.click('.dc-context-menu .dropdown-item:has-text("Forge")');
      await page.waitForSelector(`${DIALOG} [data-project-forge-form]`, { state: "visible", timeout: 8000 });
      const d = await dialog();
      assert(d.bound && d.bound.href === "https://gitlab.com/e2e/sub/app" && d.bound.text === "e2e/sub/app" && d.bound.host === "GitLab at https://gitlab.com", `the binding reads ${JSON.stringify(d.bound)}`);
      assert(d.bound.icons.join(" ") === "ti-brand-gitlab" && d.bound.painted, `the brand reads ${JSON.stringify(d.bound)}`);
      assert(d.how === "hand" && d.host === "gitlab.com" && d.path === "e2e/sub/app" && d.handDisabled === false, `the dialog reads ${JSON.stringify(d)}`);
      assert(d.origin === `github.com/${repo} on GitHub`, `the origin reads "${d.origin}"`);
      await page.click(`${DIALOG} [data-bs-dismiss="modal"]`);
      await page.waitForFunction(() => !document.querySelector("[data-form-modal].show"), null, { timeout: 8000 });
    });

    await run("worktree: its Forge shows the main project's binding, changes none and leads to the main's", async () => {
      await git(["worktree", "add", "-q", "-b", `wt-${stamp}`, `../${worktree}`]);
      await openBoard();
      await page.waitForSelector(`#project-${worktree}`, { state: "attached", timeout: 8000 });
      const toggle = page.locator(`#project-${project} [data-worktrees-toggle][aria-expanded="false"]`);
      if (await toggle.count()) await toggle.click();
      await page.waitForSelector(`#project-${worktree} [data-git-project-menu]`, { state: "visible", timeout: 8000 });
      await openForge(worktree);
      const d = await dialog();
      assert(d.bound && d.bound.href === "https://gitlab.com/e2e/sub/app", `the worktree's binding reads ${JSON.stringify(d.bound)}`);
      assert(d.worktree === `A worktree binds like its main project, ${project}.`, `the worktree says "${d.worktree}"`);
      assert(d.radios === 0 && !d.save, `the worktree offers a binding of its own: ${JSON.stringify(d)}`);
      await page.click(`${DIALOG} [data-forge-worktree] a`);
      await page.waitForFunction((t) => document.querySelector("[data-form-modal-title]")?.textContent.trim() === t, `Forge of ${project}`, { timeout: 8000 });
      const main = await dialog();
      assert(main.radios === 3 && main.how === "hand" && main.save, `the main's dialog reads ${JSON.stringify(main)}`);
      await page.click(`${DIALOG} [data-bs-dismiss="modal"]`);
      await page.waitForFunction(() => !document.querySelector("[data-form-modal].show"), null, { timeout: 8000 });
    });

    await run("off: the row drops the brand for the globe and links the origin's address", async () => {
      await openBoard();
      await openForge(project);
      await page.check(`${DIALOG} input[name="how"][value="off"]`);
      assert((await dialog()).handDisabled === true, "the fields of the binding by hand stay free while off is picked");
      await save();
      const got = await origin(`https://github.com/${repo}`);
      assert(got.icons.join(" ") === "ti-world" && got.painted && got.text === `github.com/${repo}`, `the row reads ${JSON.stringify(got)}`);
      await openForge(project);
      const d = await dialog();
      assert(!d.bound && d.unbound === "No forge." && d.how === "off", `the dialog reads ${JSON.stringify(d)}`);
      await page.click(`${DIALOG} [data-bs-dismiss="modal"]`);
      await page.waitForFunction(() => !document.querySelector("[data-form-modal].show"), null, { timeout: 8000 });
    });

    await run("no origin: a binding by hand names its repository on the row, back to the origin names none", async () => {
      await git(["remote", "remove", "origin"]);
      await openBoard();
      await origin(null);
      await openForge(project);
      let d = await dialog();
      assert(d.origin === "The project has no origin." && d.host === "" && d.path === "", `the dialog reads ${JSON.stringify(d)}`);
      await page.check(`${DIALOG} input[name="how"][value="hand"]`);
      await page.selectOption(`${DIALOG} select[name="host"]`, "github.com");
      await page.fill(`${DIALOG} input[name="path"]`, "e2e/elsewhere");
      await save();
      const got = await origin("https://github.com/e2e/elsewhere");
      assert(got.icons.join(" ") === "ti-brand-github" && got.painted && got.text === "github.com/e2e/elsewhere", `the row reads ${JSON.stringify(got)}`);
      await openForge(project);
      await page.check(`${DIALOG} input[name="how"][value="origin"]`);
      await save();
      await origin(null);
      await openForge(project);
      d = await dialog();
      assert(!d.bound && d.unbound === "No forge." && d.how === "origin", `the dialog reads ${JSON.stringify(d)}`);
      await page.click(`${DIALOG} [data-bs-dismiss="modal"]`);
      await page.waitForFunction(() => !document.querySelector("[data-form-modal].show"), null, { timeout: 8000 });
    });

    await run("phone: the dialog stands within 390px and no field takes the focus", async () => {
      const mp = await mobilePage();
      await openBoard(mp);
      await openForge(project, mp);
      const fit = await mp.evaluate(() => {
        const box = document.querySelector("[data-form-modal].show .modal-content").getBoundingClientRect();
        return { left: box.left, right: box.right, screen: innerWidth, page: document.scrollingElement.scrollWidth, focused: document.activeElement?.tagName };
      });
      assert(fit.left >= 0 && fit.right <= fit.screen && fit.page <= fit.screen, `the dialog runs past the screen: ${JSON.stringify(fit)}`);
      assert(!["INPUT", "SELECT", "TEXTAREA"].includes(fit.focused), `a field took the focus on touch: ${fit.focused}`);
      const footer = await mp.locator(`${DIALOG} [data-forge-binding-save]`).boundingBox();
      assert(footer && footer.y + footer.height <= 844, `Save stands below the screen: ${JSON.stringify(footer)}`);
    });

    await run("delete: the binding goes with the project, a new one of the same name binds by its origin", async () => {
      await openBoard();
      await openForge(project);
      await page.check(`${DIALOG} input[name="how"][value="off"]`);
      await save();
      await L.deleteProject(page, project);
      await page.waitForSelector(`#project-${project}`, { state: "detached", timeout: 15000 });
      await page.waitForSelector(`#project-${worktree}`, { state: "detached", timeout: 15000 });
      await seed();
      await openBoard();
      await origin(`https://github.com/${repo}`);
      await openForge(project);
      const d = await dialog();
      assert(d.how === "origin" && d.bound && d.bound.href === `https://github.com/${repo}`, `the new project reads ${JSON.stringify(d)}`);
      await page.click(`${DIALOG} [data-bs-dismiss="modal"]`);
    });

    await run("docs: the Forges topic explains a project's forge, the git entry names it", async () => {
      await page.goto(`${BASE}/docs`, { waitUntil: "domcontentloaded" });
      assert(await page.locator("section#forges .list-group-item", { hasText: "A project's forge" }).count() === 1, "no docs entry for a project's forge");
      assert(await page.locator("#navigation .list-group-item", { hasText: "Git from the projects page" }).filter({ hasText: "Forge" }).count() === 1, "the git entry does not name Forge");
    });
  } finally {
    await L.deleteProject(page, project).catch(() => {});
    await L.deleteProject(page, worktree).catch(() => {});
  }
});
