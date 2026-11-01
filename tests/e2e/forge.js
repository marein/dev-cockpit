const L = require("./lib");
const { assert, BASE } = L;

// Forge: which forge a project's origin lives on (internal/forge). The
// project row's origin ([data-project-origin] in [data-project-meta]) wears
// the brand of the forge the origin binds to and links the repository's page
// there, also for an origin that names the forge's SSH host; an origin on any
// other host keeps a globe and the address read off its url, a local path no
// link at all. The built in hosts are github.com and gitlab.com, Settings ›
// Forges (/settings/forges) lists them and adds, changes and deletes self
// hosted ones in the create dialog (dc-form-modal): kind, web address with a
// path of its own and alias hosts, the server refusing what no host can be.
// The origin is set with git through the cockpit's own proxy (POST /git) and
// never reached: nothing here fetches, every host is only a name in the
// config. The hosts this runner adds carry its stamp and go again at the end.
//
// An added host's Edit takes its own fields alone, a built in host has no
// Edit. A host's token has a view of its own ([data-forge-token-form]),
// which an add leads on to, in the dialog at the height of the add and as a
// page without it, and which the row's token link opens
// ([data-forge-token-link], "add token", with a token "change token"):
// Save and test keeps the token and lands on the row like the row's Test,
// a failed test keeps it too and the view, answered again, names it. With
// a token the view offers Replace and test, Remove asks first and takes it
// away, and however the dialog closes it leads to the host's row. A row's
// buttons end flush at its right edge, whichever it has, no hidden button
// keeps room and a row without one holds no column for them. The token never
// comes back: the row and the view show its last four characters. The view
// links the server's page that creates a token, prefilled, in a new tab
// ([data-forge-token-page]), the row no page of the server. A row's Test asks the server whom the token belongs to: one
// that went through leads to the list, whose row says "The token works." once
// and shows what the test found until the host or its token changes or the
// server restarts ([data-forge-tested]);
// one that failed answers the list with why on the row, without a redirect. The server is the runner's own fake forge
// (forge-fake.js), listening on FORGE_PORT and FORGE_TLS_PORT (default 3157
// and 3158), published on the host's loopback, where the instance reaches it
// at FORGE_ADDR (default 127.0.0.1). No check tests a built in host, nothing
// here ever reaches a real forge.

const { startFakeForge } = require("./forge-fake");

const DIALOG = "[data-form-modal].show";
const FAKE_PORT = Number(process.env.FORGE_PORT || 3157);
const FAKE_TLS_PORT = Number(process.env.FORGE_TLS_PORT || 3158);
const FAKE_ADDR = process.env.FORGE_ADDR || "127.0.0.1";
// The fake starts once and answers every browser of a run, so the token it
// takes is the run's, not a browser's.
const TOKEN = `glpat-e2e${Date.now().toString(36)}Xq3vR8sT2uW5`;
let fakeForge = null;

L.runFeature("FORGE", async ({ page, run, mobilePage }) => {
  const stamp = Date.now().toString(36);
  const project = `zzforge-${stamp}`;
  const host = `git-${stamp}.example.com`;
  const ghe = `ghe-${stamp}.example.com`;
  const alias = `gl-${stamp}`;
  const token = TOKEN;
  const fakeWeb = `http://${FAKE_ADDR}:${FAKE_PORT}`;
  const fakeTLS = `https://${FAKE_ADDR}:${FAKE_TLS_PORT}`;
  fakeForge ||= await startFakeForge({ port: FAKE_PORT, tlsPort: FAKE_TLS_PORT, token });
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

  // origin reads the row's origin as it stands on the page: the element, its
  // link, its text, the tabler classes of its icon, whether the icon is
  // painted with a glyph, and the whole row's markup for what must not be in it.
  const origin = async () => {
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    await page.waitForSelector(`#project-${project}`, { timeout: 8000 });
    return page.evaluate((id) => {
      const row = document.querySelector(`#project-${id}`);
      const el = row.querySelector("[data-project-meta] [data-project-origin]");
      if (!el) return { row: row.outerHTML };
      const icon = el.querySelector("i.ti");
      const box = icon.getBoundingClientRect();
      const style = getComputedStyle(icon);
      const glyph = getComputedStyle(icon, "::before").content;
      return {
        row: row.outerHTML,
        tag: el.tagName.toLowerCase(),
        href: el.getAttribute("href") || "",
        text: el.textContent.trim(),
        icons: [...icon.classList].filter((c) => c.startsWith("ti-")),
        painted: box.width > 0 && box.height > 0 && style.visibility !== "hidden" && style.display !== "none" && glyph !== "none" && glyph !== "",
      };
    }, project);
  };

  // hostRows reads Settings › Forges as it stands: per row its address, its
  // kind line, its alias hosts, its icon painted or not, whether an Edit is
  // really visible on it, its token link and how many links lead to the
  // server's token page.
  const hostRows = (p = page) => p.evaluate(() => {
    const shown = (el) => {
      if (!el) return false;
      const box = el.getBoundingClientRect();
      const style = getComputedStyle(el);
      return box.width > 0 && box.height > 0 && style.visibility !== "hidden" && style.display !== "none";
    };
    return [...document.querySelectorAll("[data-forge-host]")].map((row) => {
      const icon = row.querySelector("[data-forge-icon]");
      const glyph = getComputedStyle(icon, "::before").content;
      const tokenLink = row.querySelector("[data-forge-token-link]");
      return {
        web: row.dataset.forgeHost,
        href: row.querySelector("[data-forge-web]").getAttribute("href"),
        kind: row.querySelector("[data-forge-kind]").textContent.trim(),
        aliases: row.querySelector("[data-forge-aliases]").textContent.trim(),
        token: row.querySelector("[data-forge-token]").textContent.trim(),
        ca: shown(row.querySelector("[data-forge-ca]")),
        icons: [...icon.classList].filter((c) => c.startsWith("ti-")),
        painted: shown(icon) && glyph !== "none" && glyph !== "",
        edit: [...row.querySelectorAll("a, span.btn")].some((el) => /Edit/.test(el.textContent) && shown(el)),
        test: [...row.querySelectorAll("button, span.btn")].some((el) => /Test/.test(el.textContent) && shown(el)),
        editHref: row.querySelector("[data-forge-edit]")?.getAttribute("href") || "",
        tokenLink: tokenLink && shown(tokenLink) ? { text: tokenLink.textContent.trim(), href: tokenLink.getAttribute("href"), target: tokenLink.target } : null,
        serverLinks: row.querySelectorAll('[data-forge-token-page], a[href*="personal_access_tokens"], a[href*="/settings/tokens"]').length,
      };
    });
  });

  // actionEnds reads per row the buttons it really shows, how far the last
  // one ends before the row's right edge, how many hidden buttons it holds,
  // whether it holds a column for buttons and how far its alias hosts stand
  // off their head.
  const actionEnds = (p = page) => p.evaluate(() => {
    const head = document.querySelector("[data-forge-host]").closest(".card").querySelector(".card-header");
    const aliasHead = getComputedStyle(head).display === "none" ? null : head.querySelector(".row").children[1].getBoundingClientRect().left;
    return [...document.querySelectorAll("[data-forge-host]")].map((row) => {
      const all = [...row.querySelectorAll(".btn")];
      const shown = all.filter((b) => {
        const box = b.getBoundingClientRect();
        const style = getComputedStyle(b);
        return box.width > 0 && box.height > 0 && style.visibility !== "hidden" && style.display !== "none";
      });
      const end = row.getBoundingClientRect().right - parseFloat(getComputedStyle(row).paddingRight);
      return {
        web: row.dataset.forgeHost,
        buttons: shown.map((b) => b.textContent.trim()),
        gap: shown.length ? Math.round(end - Math.max(...shown.map((b) => b.getBoundingClientRect().right))) : null,
        hidden: all.length - shown.length,
        column: !!row.querySelector("[data-forge-actions]"),
        aliasOff: aliasHead === null ? null : Math.round(row.querySelector("[data-forge-aliases]").getBoundingClientRect().left - aliasHead),
      };
    });
  });
  // expectFlush checks that every row's buttons end at its right edge, and
  // that the rows show at least the button sets of want.
  const expectFlush = async (want, p = page) => {
    const rows = await actionEnds(p);
    for (const r of rows) {
      assert(r.hidden === 0 && r.column === (r.buttons.length > 0) && (r.gap === null || r.gap === 0) && (r.aliasOff === null || r.aliasOff === 0), `${r.web} reads ${JSON.stringify(r)}, want its buttons flush right, no empty column and its alias hosts under their head`);
    }
    for (const set of want) {
      assert(rows.some((r) => r.buttons.join(" ") === set), `no row shows ${JSON.stringify(set)}: ${JSON.stringify(rows)}`);
    }
  };

  const openForges = async (p = page) => {
    await p.goto(`${BASE}/settings/forges`, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(p);
    await p.waitForSelector("[data-forge-host]", { timeout: 8000 });
    await L.waitUpgraded(p, ["dc-form-modal"]);
  };

  const glPage = (web) => `${web}/-/user_settings/personal_access_tokens?name=dev-cockpit&scopes=api`;
  const ghPage = (web) => `${web}/settings/tokens/new?description=dev-cockpit&scopes=repo,workflow,read:org`;

  const dialogError = async () => {
    await page.waitForSelector(`${DIALOG} [data-form-modal-error]`, { timeout: 8000 });
    return (await page.textContent(`${DIALOG} [data-form-modal-error]`)).trim();
  };

  // tokenView reads the token view the dialog stands on as it really shows.
  const tokenView = (p = page) => p.evaluate(() => {
    const d = document.querySelector("[data-form-modal].show");
    const field = d.querySelector('[data-forge-token-form] input[name="token"]');
    const remove = d.querySelector("[data-forge-token-remove]");
    return {
      title: document.querySelector("[data-form-modal-title]").textContent.trim(),
      intro: d.querySelector("[data-forge-token-intro]").textContent.trim(),
      links: [...d.querySelectorAll("label[for=forge-token] a")].map((a) => {
        const box = a.getBoundingClientRect();
        return { text: a.textContent.trim(), href: a.getAttribute("href"), shown: box.width > 0 && box.height > 0 && getComputedStyle(a).visibility !== "hidden", target: a.target };
      }),
      type: field.type,
      required: field.required,
      value: field.value,
      remove: !!remove && remove.getBoundingClientRect().width > 0 && getComputedStyle(remove).visibility !== "hidden",
      save: d.querySelector("[data-forge-token-save]").textContent.trim(),
      cancel: d.querySelector("[data-forge-token-cancel]").textContent.trim(),
      hint: d.querySelector("[data-forge-token-hint]").textContent.trim(),
      height: Math.round(d.querySelector(".modal-content").getBoundingClientRect().height),
    };
  });
  const dialogHeight = (p = page) => p.evaluate(() => Math.round(document.querySelector("[data-form-modal].show .modal-content").getBoundingClientRect().height));

  // atRow waits for the dialog to lead to the list again, at the row of web.
  const atRow = async (web, p = page) => {
    await p.waitForFunction((w) => {
      const row = document.querySelector(`[data-forge-host="${w}"]`);
      return !document.querySelector("[data-form-modal].show") && location.pathname === "/settings/forges" && !!row && location.hash === `#${row.id}`;
    }, web, { timeout: 8000 });
  };

  // openToken opens a row's token view in the dialog over the list.
  const openToken = async (web) => {
    await openForges();
    await page.click(`[data-forge-host="${web}"] [data-forge-token-link]`);
    await page.waitForSelector(`${DIALOG} [data-forge-token-form]`, { timeout: 8000 });
  };

  // landed waits for the dialog to lead to the list again with the notice the
  // server left.
  const landed = async (notice) => {
    await page.waitForFunction((text) => !document.querySelector("[data-form-modal].show") && location.pathname === "/settings/forges" && document.body.innerText.includes(text), notice, { timeout: 8000 });
  };

  // dropHosts deletes what this runner added, through the Edit's own delete.
  const dropHosts = async () => {
    await page.goto(`${BASE}/settings/forges`, { waitUntil: "domcontentloaded" });
    const edits = await page.evaluate((mark) => [...document.querySelectorAll("[data-forge-host]")]
      .filter((row) => row.textContent.includes(mark))
      .map((row) => row.querySelector("[data-forge-edit]")?.getAttribute("href"))
      .filter(Boolean), stamp);
    for (const href of edits) {
      await page.evaluate(async (target) => {
        const body = new URLSearchParams({ form: "delete", csrf_token: document.querySelector('meta[name="csrf-token"]').content });
        await fetch(target, { method: "POST", body });
      }, href);
    }
  };

  const expectOrigin = async (url, want) => {
    await git(["remote", "set-url", "origin", url]);
    const got = await origin();
    assert(got.tag, `${url}: the row shows no origin`);
    assert(got.icons.length === 1 && got.icons[0] === want.icon, `${url}: icon ${got.icons.join(" ")}, want ${want.icon}`);
    assert(got.painted, `${url}: the icon is not painted`);
    assert(got.text === want.text, `${url}: text "${got.text}", want "${want.text}"`);
    if (want.href) {
      assert(got.tag === "a" && got.href === want.href, `${url}: ${got.tag} to "${got.href}", want a link to ${want.href}`);
    } else {
      assert(got.tag === "span" && !got.href, `${url}: a local path is a ${got.tag} to "${got.href}", want no link`);
    }
    return got;
  };

  try {
    await run("setup: a repository without an origin shows none", async () => {
      await L.createProject(page, project);
      path = await L.projectPath(page, project);
      await git(["init", "-q", "-b", "master"]);
      const got = await origin();
      assert(!got.tag, "a repository without an origin shows one");
      await git(["remote", "add", "origin", "git@github.com:marein/dev-cockpit.git"]);
    });

    await run("github.com in scp form wears its brand and links the repository", async () => {
      await expectOrigin("git@github.com:marein/dev-cockpit.git", { icon: "ti-brand-github", text: "github.com/marein/dev-cockpit", href: "https://github.com/marein/dev-cockpit" });
    });

    await run("an origin over ssh.github.com on port 443 binds to github.com and links its web page", async () => {
      await expectOrigin("ssh://git@ssh.github.com:443/marein/dev-cockpit.git", { icon: "ti-brand-github", text: "ssh.github.com/marein/dev-cockpit", href: "https://github.com/marein/dev-cockpit" });
    });

    await run("gitlab.com over https with user, password, port and subgroups shows no credential", async () => {
      const got = await expectOrigin("https://e2e:s3cr3t-token@gitlab.com:443/group/sub/deeper/repo.git", { icon: "ti-brand-gitlab", text: "gitlab.com/group/sub/deeper/repo", href: "https://gitlab.com/group/sub/deeper/repo" });
      assert(!got.row.includes("s3cr3t") && !got.row.includes("e2e:"), "the row carries the credential of the origin url");
    });

    await run("gitlab.com over ssh:// with a port and without .git", async () => {
      await expectOrigin("ssh://git@gitlab.com:2222/group/repo", { icon: "ti-brand-gitlab", text: "gitlab.com/group/repo", href: "https://gitlab.com/group/repo" });
    });

    await run("a host no forge is known on drops the brand for the globe and keeps the address", async () => {
      const got = await expectOrigin("git@gitlab.example.com:group/sub/repo.git", { icon: "ti-world", text: "gitlab.example.com/group/sub/repo", href: "https://gitlab.example.com/group/sub/repo" });
      assert(!/ti-brand-git(hub|lab)/.test(got.row), "the row still wears a forge brand");
      await expectOrigin("https://git.example.com:8443/team/app.git", { icon: "ti-world", text: "git.example.com/team/app", href: "https://git.example.com:8443/team/app" });
    });

    await run("a local path origin is plain text under the globe", async () => {
      await expectOrigin("/srv/git/app.git", { icon: "ti-world", text: "/srv/git/app" });
    });

    await run("settings: Forges tells the pace per project, the Forges row lists github.com and gitlab.com as built in, without a token, without an Edit, with add token", async () => {
      await openForges();
      assert(await page.locator('[data-settings-nav] a[href="/settings/forges"].active').count() === 1, "the settings column does not mark Forges");
      const pace = await page.locator("[data-forge-pace]").evaluate((el) => ({ text: el.textContent, height: el.getBoundingClientRect().height, visibility: getComputedStyle(el).visibility }));
      assert(pace.height > 0 && pace.visibility === "visible" && pace.text === "Each project is checked on its own: every 15s while a pipeline runs or 2m after a push, every 5m when idle. Review threads are read along with these checks, at most every 1m. After a failed check it waits longer, up to 5m.", `the pace reads ${JSON.stringify(pace)}`);
      const rows = await hostRows();
      const want = [
        { web: "https://github.com", kind: "GitHub, built in", aliases: "ssh.github.com", icon: "ti-brand-github", tokenView: "/settings/forges/github.com/token" },
        { web: "https://gitlab.com", kind: "GitLab, built in", aliases: "altssh.gitlab.com", icon: "ti-brand-gitlab", tokenView: "/settings/forges/gitlab.com/token" },
      ];
      for (const [i, w] of want.entries()) {
        const row = rows[i];
        assert(row && row.web === w.web && row.href === w.web && row.kind === w.kind && row.aliases === w.aliases, `row ${i} reads ${JSON.stringify(row)}`);
        assert(row.icons.length === 1 && row.icons[0] === w.icon && row.painted, `${w.web} wears ${row.icons.join(" ")}, painted ${row.painted}`);
        assert(!row.edit && !row.test && row.token === "no token", `${w.web} reads ${JSON.stringify(row)}, want no Edit, no Test and no token`);
        assert(JSON.stringify(row.tokenLink) === JSON.stringify({ text: "add token", href: w.tokenView, target: "" }) && row.serverLinks === 0, `${w.web} links ${JSON.stringify(row.tokenLink)} and ${row.serverLinks} pages of the server`);
      }
      await expectFlush([""]);
    });

    await run("add host: the dialog refuses a web address with a password and keeps what was typed", async () => {
      await openForges();
      await page.click("[data-forge-add]");
      await page.waitForSelector(`${DIALOG} [data-forge-host-form]`, { timeout: 8000 });
      const title = (await page.textContent("[data-form-modal-title]")).trim();
      assert(title === "Add host", `the dialog is titled "${title}"`);
      await page.selectOption(`${DIALOG} select[name="kind"]`, "gitlab");
      await page.fill(`${DIALOG} input[name="web_url"]`, `https://e2e:pw@${host}/gitlab/`);
      await page.fill(`${DIALOG} input[name="aliases"]`, `${alias} ssh.${host}`);
      await page.click(`${DIALOG} [data-forge-save]`);
      const said = await dialogError();
      assert(said === "A web address carries no user name or password.", `refused with "${said}"`);
      assert(await page.inputValue(`${DIALOG} input[name="web_url"]`) === `https://e2e:pw@${host}/gitlab/`, "the typed address is gone");
      assert(await page.inputValue(`${DIALOG} input[name="aliases"]`) === `${alias} ssh.${host}`, "the typed alias hosts are gone");
    });

    await run("add host: a name github.com holds is refused, a self hosted GitLab under a path leads on to its token, Later lands on the list without one", async () => {
      await page.fill(`${DIALOG} input[name="web_url"]`, `https://${host}/gitlab/`);
      await page.fill(`${DIALOG} input[name="aliases"]`, `${alias} ssh.github.com`);
      await page.click(`${DIALOG} [data-forge-save]`);
      await page.waitForFunction(() => /already belongs/.test(document.querySelector("[data-form-modal].show [data-form-modal-error]")?.textContent || ""), null, { timeout: 8000 });
      const said = await dialogError();
      assert(said === "ssh.github.com already belongs to https://github.com.", `refused with "${said}"`);
      const before = await dialogHeight();
      await page.fill(`${DIALOG} input[name="aliases"]`, `${alias}, ssh.${host}`);
      await page.click(`${DIALOG} [data-forge-save]`);
      await page.waitForSelector(`${DIALOG} [data-forge-token-form]`, { timeout: 8000 });
      await page.waitForFunction(() => document.activeElement?.id === "forge-token", null, { timeout: 3000 }).catch(() => {});
      const view = await tokenView();
      const web = `https://${host}/gitlab`;
      assert(view.title === web, `the token view is titled "${view.title}"`);
      assert(view.intro === `A token lets the cockpit act on GitLab at ${web} as you, give it one now or later from its row.`, `the token view says "${view.intro}"`);
      const links = [{ text: "New token", href: glPage(web), shown: true, target: "_blank" }];
      assert(JSON.stringify(view.links) === JSON.stringify(links), `the token view links ${JSON.stringify(view.links)}`);
      assert(view.type === "password" && view.required && view.value === "" && !view.remove && view.save === "Save and test" && view.cancel === "Later", `the token view reads ${JSON.stringify(view)}`);
      assert(view.height === before, `the dialog went from ${before}px to ${view.height}px`);
      assert(await page.evaluate(() => document.activeElement?.id) === "forge-token", "the token field has no focus on a fine pointer");
      await page.click(`${DIALOG} [data-forge-token-cancel]`);
      await atRow(web);
      assert(!(await page.content()).includes("Host added."), "the list flashes the add after the dialog");
      const row = (await hostRows()).find((r) => r.web === web);
      assert(row, `no row for ${web}`);
      assert(row.token === "no token" && !row.test, `Later left a token: ${JSON.stringify(row)}`);
      assert(row.kind === "GitLab" && row.aliases === `${alias}, ssh.${host}` && row.edit, `the row reads ${JSON.stringify(row)}`);
      assert(row.tokenLink?.text === "add token" && row.tokenLink.href === `${row.editHref}/token` && row.serverLinks === 0, `the row links ${JSON.stringify(row.tokenLink)} and ${row.serverLinks} pages of the server`);
      assert(row.icons[0] === "ti-brand-gitlab" && row.painted, `the row wears ${row.icons.join(" ")}`);
      await expectFlush(["", "Edit"]);
    });

    await run("project row: origins on the added host wear its brand and link under its path", async () => {
      const page2 = `https://${host}/gitlab/team/app`;
      await expectOrigin(`git@${host}:team/app.git`, { icon: "ti-brand-gitlab", text: `${host}/team/app`, href: page2 });
      await expectOrigin(`https://${host}/gitlab/team/app.git`, { icon: "ti-brand-gitlab", text: `${host}/gitlab/team/app`, href: page2 });
      await expectOrigin(`${alias}:team/app.git`, { icon: "ti-brand-gitlab", text: `${alias}/team/app`, href: page2 });
      await expectOrigin(`ssh://git@ssh.${host}:2222/team/sub/app.git`, { icon: "ti-brand-gitlab", text: `ssh.${host}/team/sub/app`, href: `https://${host}/gitlab/team/sub/app` });
    });

    await run("edit host: the row's Edit shows the host, refuses a taken name and saves a GitHub Enterprise", async () => {
      await openForges();
      await page.click(`[data-forge-host="https://${host}/gitlab"] [data-forge-edit]`);
      await page.waitForSelector(`${DIALOG} [data-forge-host-form]`, { timeout: 8000 });
      const title = (await page.textContent("[data-form-modal-title]")).trim();
      assert(title === `https://${host}/gitlab`, `the dialog is titled "${title}"`);
      assert(await page.inputValue(`${DIALOG} select[name="kind"]`) === "gitlab", "the kind is not the host's");
      assert(await page.inputValue(`${DIALOG} input[name="web_url"]`) === `https://${host}/gitlab`, "the address is not the host's");
      assert(await page.inputValue(`${DIALOG} input[name="aliases"]`) === `${alias} ssh.${host}`, "the alias hosts are not the host's");
      assert(await page.locator(`${DIALOG} input[name="token"], ${DIALOG} input[name="token_remove"], ${DIALOG} [data-forge-token-page]`).count() === 0, "the Edit asks for a token");
      await page.selectOption(`${DIALOG} select[name="kind"]`, "github");
      await page.fill(`${DIALOG} input[name="web_url"]`, "https://gitlab.com");
      await page.click(`${DIALOG} [data-forge-save]`);
      const said = await dialogError();
      assert(said === "gitlab.com already belongs to https://gitlab.com.", `refused with "${said}"`);
      await page.fill(`${DIALOG} input[name="web_url"]`, `https://${ghe}`);
      await page.fill(`${DIALOG} input[name="aliases"]`, `${alias}`);
      await page.click(`${DIALOG} [data-forge-save]`);
      await landed("Host saved.");
      const rows = await hostRows();
      assert(!rows.some((r) => r.web.includes(host)), "the old address still stands");
      const row = rows.find((r) => r.web === `https://${ghe}`);
      assert(row && row.kind === "GitHub" && row.aliases === alias && row.icons[0] === "ti-brand-github" && row.painted, `the row reads ${JSON.stringify(row)}`);
      assert(row.tokenLink?.text === "add token" && row.serverLinks === 0, `the row links ${JSON.stringify(row.tokenLink)} and ${row.serverLinks} pages of the server`);
    });

    await run("project row: the changed host binds its new names and lets the old one go", async () => {
      await expectOrigin(`${alias}:team/app.git`, { icon: "ti-brand-github", text: `${alias}/team/app`, href: `https://${ghe}/team/app` });
      await expectOrigin(`git@${host}:team/app.git`, { icon: "ti-world", text: `${host}/team/app`, href: `https://${host}/team/app` });
    });

    await run("delete host: Delete asks first, Cancel keeps it, the confirm removes it and the origin falls back to the globe", async () => {
      await openForges();
      const edit = `[data-forge-host="https://${ghe}"] [data-forge-edit]`;
      await page.click(edit);
      await page.waitForSelector(`${DIALOG} [data-forge-delete]`, { timeout: 8000 });
      await page.click(`${DIALOG} [data-forge-delete]`);
      await page.waitForSelector(".swal2-cancel", { state: "visible", timeout: 8000 });
      const question = (await page.textContent(".swal2-title")).trim();
      assert(question === `Delete the host https://${ghe} and its token?`, `the question reads "${question}"`);
      await page.click(".swal2-cancel");
      await page.waitForSelector(".swal2-container", { state: "detached", timeout: 5000 });
      await openForges();
      assert((await hostRows()).some((r) => r.web === `https://${ghe}`), "Cancel deleted the host");
      await page.click(edit);
      await page.waitForSelector(`${DIALOG} [data-forge-delete]`, { timeout: 8000 });
      await page.click(`${DIALOG} [data-forge-delete]`);
      await L.confirmSwal(page);
      await landed("Host deleted.");
      assert(!(await hostRows()).some((r) => r.web.includes(stamp)), "the host is still listed");
      await expectOrigin(`${alias}:team/app.git`, { icon: "ti-world", text: `${alias}/team/app`, href: `https://${alias}/team/app` });
    });

    let fakeHost = `${fakeWeb}/gl`;
    const noToken = async (where) => {
      assert(!(await page.content()).includes(token), `${where} shows the token`);
    };

    await run("page: without the dialog an add lands on the host's token page, which answers a wrong token with why and lands a good one on the row", async () => {
      await page.goto(`${BASE}/settings/forges/new`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      await page.waitForSelector("[data-forge-host-form]", { timeout: 8000 });
      assert(!(await page.locator(DIALOG).count()), "the page opened the dialog");
      await page.selectOption('[data-forge-host-form] select[name="kind"]', "gitlab");
      await page.fill('[data-forge-host-form] input[name="web_url"]', fakeHost);
      await page.fill('[data-forge-host-form] input[name="aliases"]', `pg-${stamp}`);
      await page.click("[data-forge-host-form] [data-forge-save]");
      await page.waitForFunction(() => /^\/settings\/forges\/[^/]+\/token$/.test(location.pathname) && !!document.querySelector("[data-forge-token-form]"), null, { timeout: 8000 });
      const at = new URL(page.url()).pathname;
      const id = at.split("/")[3];
      assert(await page.evaluate(() => document.body.innerText.includes("Host added.")), "the token page does not say the host was added");
      assert(await page.getAttribute("[data-forge-token-form] [data-forge-token-page]", "href") === glPage(fakeHost), "the token page does not link the server's token page");
      assert(await page.getAttribute("[data-forge-token-form] [data-forge-token-cancel]", "href") === `/settings/forges#forge-host-${id}`, "Later does not lead to the host's row");
      await page.fill('[data-forge-token-form] input[name="token"]', "glpat-wrong-on-the-page-0000");
      const [answer] = await Promise.all([
        page.waitForResponse((r) => r.request().method() === "POST" && new URL(r.url()).pathname === at, { timeout: 30000 }),
        page.click("[data-forge-token-save]"),
      ]);
      await page.waitForFunction(() => document.body.innerText.includes("GitLab refused the token: it is wrong, expired or revoked."), null, { timeout: 8000 });
      assert(answer.status() === 409 && new URL(page.url()).pathname === at, `the wrong token answered ${answer.status()} at ${page.url()}`);
      const hint = (await page.textContent("[data-forge-token-form] [data-forge-token-hint]")).trim();
      assert(hint === "A token is set, it ends in …0000.", `the hint reads "${hint}"`);
      assert((await page.textContent("[data-forge-token-save]")).trim() === "Replace and test" && await page.locator("[data-forge-token-remove]").isVisible(), "the page does not offer to replace and remove the kept token");
      await page.fill('[data-forge-token-form] input[name="token"]', token);
      await page.click("[data-forge-token-save]");
      await page.waitForSelector(`[data-forge-host="${fakeHost}"] [data-forge-test-result] .alert-success`, { timeout: 30000 });
      const row = await page.evaluate((w) => {
        const r = document.querySelector(`[data-forge-host="${w}"]`);
        return { text: r.querySelector("[data-forge-test-result]").textContent.trim(), kept: r.querySelector("[data-forge-tested]")?.textContent.trim(), atRow: location.hash === `#${r.id}`, path: location.pathname };
      }, fakeHost);
      assert(row.text === "The token works." && row.kept === "Connected to GitLab 17.5.1 as root (Administrator)." && row.atRow && row.path === "/settings/forges", `the row reads ${JSON.stringify(row)}`);
      await noToken("the list");
      await dropHosts();
      await openForges();
      assert(!(await hostRows()).some((r) => r.web === fakeHost), "the page's host still stands");
    });

    // openEdit opens a row's Edit in the dialog, tested lands its outcome on
    // the row after its Test and reads it and what the row keeps of it as
    // they really show.
    const openEdit = async (web) => {
      await openForges();
      await page.click(`[data-forge-host="${web}"] [data-forge-edit]`);
      await page.waitForSelector(`${DIALOG} [data-forge-host-form]`, { timeout: 8000 });
    };
    const saveEdit = async () => {
      await page.click(`${DIALOG} [data-forge-save]`);
      await landed("Host saved.");
    };
    const tested = async (web) => {
      await openForges();
      await page.click(`[data-forge-host="${web}"] [data-forge-test]`);
      await page.waitForSelector(`[data-forge-host="${web}"] [data-forge-test-result]`, { timeout: 30000 });
      return page.evaluate((w) => {
        const row = document.querySelector(`[data-forge-host="${w}"]`);
        const el = row.querySelector("[data-forge-test-result]");
        const kept = row.querySelector("[data-forge-tested]");
        const box = el.getBoundingClientRect();
        const style = getComputedStyle(el);
        return {
          text: el.textContent.trim(),
          kept: kept ? kept.textContent.trim() : "",
          keptShown: !!kept && kept.getBoundingClientRect().height > 0 && getComputedStyle(kept).display !== "none",
          shown: box.width > 0 && box.height > 0 && style.visibility !== "hidden" && style.display !== "none",
          inView: box.top >= 0 && box.bottom <= innerHeight,
          success: !!el.querySelector(".alert-success"),
          danger: !!el.querySelector(".alert-danger"),
          atRow: location.hash === `#${row.id}`,
          path: location.pathname,
        };
      }, web);
    };
    // expectTest runs a row's Test: a good one says so in the fixed sentence
    // and keeps what it found, found, on the row, a failed one says why,
    // found, and keeps nothing.
    const expectTest = async (web, found, good) => {
      const got = await tested(web);
      const text = good ? "The token works." : found;
      assert(got.text === text, `the test says "${got.text}", want "${text}"`);
      assert(good ? got.kept === found && got.keptShown : got.kept === "", `the row keeps "${got.kept}": ${JSON.stringify(got)}`);
      assert(got.shown && got.inView && got.path === "/settings/forges" && (!good || got.atRow), `the outcome is not shown on the row: ${JSON.stringify(got)}`);
      assert(good ? got.success && !got.danger : got.danger && !got.success, `the outcome wears the wrong color: ${JSON.stringify(got)}`);
    };
    await run("token: Add host takes no token, its token view keeps a wrong one and names it in the dialog, lands a good one on the row, which shows its last four characters, never the token", async () => {
      await openForges();
      await page.click("[data-forge-add]");
      await page.waitForSelector(`${DIALOG} [data-forge-host-form]`, { timeout: 8000 });
      assert(await page.locator(`${DIALOG} input[name="token"], ${DIALOG} input[name="token_remove"], ${DIALOG} [data-forge-token-page]`).count() === 0, "Add host asks for a token");
      await page.selectOption(`${DIALOG} select[name="kind"]`, "gitlab");
      await page.fill(`${DIALOG} input[name="web_url"]`, fakeHost);
      await page.fill(`${DIALOG} input[name="aliases"]`, `fake-${stamp}`);
      await page.click(`${DIALOG} [data-forge-save]`);
      await page.waitForSelector(`${DIALOG} [data-forge-token-form]`, { timeout: 8000 });
      const view = await tokenView();
      assert(view.type === "password" && view.required && !view.remove && view.save === "Save and test" && view.hint === "A personal access token of your account on the server.", `the token view reads ${JSON.stringify(view)}`);
      await page.fill(`${DIALOG} input[name="token"]`, "glpat-wrong-in-the-dialog-1111");
      await page.click(`${DIALOG} [data-forge-token-save]`);
      const said = await dialogError();
      assert(said === "GitLab refused the token: it is wrong, expired or revoked.", `the wrong token said "${said}"`);
      const refused = await tokenView();
      assert(refused.title === fakeHost && refused.intro === `A new token replaces the token of GitLab at ${fakeHost}, Remove takes it away.` && refused.hint === "A token is set, it ends in …1111.", `after the refusal the token view reads ${JSON.stringify(refused)}`);
      assert(refused.value === "" && refused.required && refused.remove && refused.save === "Replace and test" && refused.cancel === "Cancel" && refused.height === view.height, `after the refusal the token view reads ${JSON.stringify(refused)}, it stood ${view.height}px high`);
      await noToken("the refused token view");
      await page.fill(`${DIALOG} input[name="token"]`, token);
      await page.click(`${DIALOG} [data-forge-token-save]`);
      await page.waitForSelector(`[data-forge-host="${fakeHost}"] [data-forge-test-result] .alert-success`, { timeout: 30000 });
      assert(!(await page.locator(DIALOG).count()), "the dialog still stands after the test went through");
      const result = (await page.textContent(`[data-forge-host="${fakeHost}"] [data-forge-test-result]`)).trim();
      assert(result === "The token works.", `the row says "${result}"`);
      const row = (await hostRows()).find((r) => r.web === fakeHost);
      assert(row && row.token === `token …${token.slice(-4)}` && row.test && row.edit && !row.ca && row.tokenLink?.text === "change token", `the row reads ${JSON.stringify(row)}`);
      await noToken("the list");
      await openToken(fakeHost);
      const kept = await tokenView();
      assert(kept.title === fakeHost && kept.intro === `A new token replaces the token of GitLab at ${fakeHost}, Remove takes it away.`, `the token view reads ${JSON.stringify(kept)}`);
      assert(kept.value === "" && kept.required && kept.remove && kept.save === "Replace and test" && kept.cancel === "Cancel", `the token view reads ${JSON.stringify(kept)}`);
      assert(kept.hint === `A token is set, it ends in …${token.slice(-4)}.`, `the hint reads "${kept.hint}"`);
      await noToken("the token view");
      await page.click(`${DIALOG} [data-forge-token-cancel]`);
      await atRow(fakeHost);
    });

    await run("settings: a row's buttons end flush at its right edge, whichever it has, on the desktop and the phone", async () => {
      await openForges();
      await expectFlush(["", "Test Edit"]);
      const mp = await mobilePage();
      await openForges(mp);
      await expectFlush(["", "Test Edit"], mp);
    });

    await run("token page: the token view's New token opens the server's page prefilled in a new tab, the row links no page of the server", async () => {
      const opens = async (selector, want) => {
        const from = fakeForge.log.length;
        const [tab] = await Promise.all([page.context().waitForEvent("page", { timeout: 8000 }), page.click(selector)]);
        await tab.waitForLoadState("domcontentloaded");
        const url = tab.url();
        await tab.close();
        const path = new URL(want).pathname;
        const got = fakeForge.log.slice(from).filter((e) => e.path.endsWith("/personal_access_tokens"));
        assert(url === want, `the tab opened ${url}`);
        assert(got.length === 1 && got[0].path === path && got[0].query === "?name=dev-cockpit&scopes=api" && got[0].auth === "", `the fake got ${JSON.stringify(got)}`);
      };
      await openToken(fakeHost);
      assert((await hostRows()).find((r) => r.web === fakeHost)?.serverLinks === 0, "the row links a page of the server");
      const links = (await tokenView()).links;
      assert(JSON.stringify(links) === JSON.stringify([{ text: "New token", href: glPage(fakeHost), shown: true, target: "_blank" }]), `the token view links ${JSON.stringify(links)}`);
      await opens(`${DIALOG} [data-forge-token-page]`, glPage(fakeHost));
      assert(await page.locator(`${DIALOG} [data-forge-token-form]`).isVisible(), "the token view closed");
      await page.keyboard.press("Escape");
      await atRow(fakeHost);
    });

    await run("test: the row's Test shows whom the token belongs to on GitLab and the version, on the row", async () => {
      const from = fakeForge.log.length;
      await expectTest(fakeHost, "Connected to GitLab 17.5.1 as root (Administrator).", true);
      await page.reload({ waitUntil: "domcontentloaded" });
      await page.waitForSelector("[data-forge-host]", { timeout: 8000 });
      assert(await page.locator("[data-forge-test-result]").count() === 0, "the outcome stays after a reload");
      const kept = (await page.textContent(`[data-forge-host="${fakeHost}"] [data-forge-tested]`)).trim();
      assert(kept === "Connected to GitLab 17.5.1 as root (Administrator).", `after a reload the row keeps "${kept}"`);
      const sent = fakeForge.log.slice(from).filter((e) => e.path.startsWith("/gl/api/v4/"));
      assert(sent.length >= 2 && sent.every((e) => e.auth === `Bearer ${token}` && !e.query), `the fake got ${JSON.stringify(sent)}`);
    });

    await run("test: an Edit keeps the token, a GitLab below 15.0 is refused with a sentence", async () => {
      await openEdit(fakeHost);
      fakeHost = `${fakeWeb}/old`;
      await page.fill(`${DIALOG} input[name="web_url"]`, fakeHost);
      await saveEdit();
      await expectTest(fakeHost, "The server runs GitLab 14.10, the forge needs GitLab 15.0 or newer.", false);
    });

    await run("test: a redirect is named and never followed, the token never reaches its target", async () => {
      await openEdit(fakeHost);
      fakeHost = `${fakeWeb}/moved`;
      await page.fill(`${DIALOG} input[name="web_url"]`, fakeHost);
      await saveEdit();
      await expectTest(fakeHost, `The server redirects to ${fakeWeb}/elsewhere/api/v4/version, which is not followed: check the web address.`, false);
      assert(!fakeForge.log.some((e) => e.path.startsWith("/elsewhere")), "the redirect was followed");
    });

    await run("test: GitHub Enterprise Server shows its user and version, the same server as GitLab says it is none", async () => {
      await openEdit(fakeHost);
      fakeHost = fakeWeb;
      await page.selectOption(`${DIALOG} select[name="kind"]`, "github");
      await page.fill(`${DIALOG} input[name="web_url"]`, fakeHost);
      await saveEdit();
      await expectTest(fakeHost, "Connected to GitHub Enterprise Server 3.14.2 as octocat (monalisa octocat).", true);
      await openEdit(fakeHost);
      await page.selectOption(`${DIALOG} select[name="kind"]`, "gitlab");
      await saveEdit();
      await expectTest(fakeHost, `No GitLab API answers at ${fakeWeb}/api/v4: check the kind and the web address.`, false);
    });

    await run("test: a new wrong token replaces the old one, is refused in the dialog, whose view then names it, and shows on the row once the dialog closes", async () => {
      await openEdit(fakeHost);
      await page.selectOption(`${DIALOG} select[name="kind"]`, "github");
      await saveEdit();
      await openToken(fakeHost);
      const before = await tokenView();
      assert(before.hint === `A token is set, it ends in …${token.slice(-4)}.`, `before the replace the hint reads "${before.hint}"`);
      await page.fill(`${DIALOG} input[name="token"]`, "ghp_wrongwrongwrongwrong1234");
      await page.click(`${DIALOG} [data-forge-token-save]`);
      const said = await dialogError();
      assert(said === "GitHub refused the token: it is wrong, expired or revoked.", `the wrong token said "${said}"`);
      const after = await tokenView();
      assert(after.hint === "A token is set, it ends in …1234." && after.value === "" && after.save === "Replace and test" && after.remove, `after the refusal the token view reads ${JSON.stringify(after)}`);
      await page.click(`${DIALOG} [data-forge-token-cancel]`);
      await atRow(fakeHost);
      assert((await hostRows()).find((r) => r.web === fakeHost)?.token === "token …1234", "the new token's hint does not show");
      await expectTest(fakeHost, "GitHub refused the token: it is wrong, expired or revoked.", false);
    });

    await run("CA bundle: refused for http, an https server it does not trust fails until its CA bundle is on the host", async () => {
      await openEdit(fakeHost);
      await page.selectOption(`${DIALOG} select[name="kind"]`, "gitlab");
      await page.fill(`${DIALOG} input[name="web_url"]`, `${fakeWeb}/gl`);
      await page.fill(`${DIALOG} textarea[name="ca_bundle"]`, fakeForge.ca);
      await page.click(`${DIALOG} [data-forge-save]`);
      const said = await dialogError();
      assert(said === "A CA bundle only serves a web address that starts with https://.", `refused with "${said}"`);
      assert((await page.inputValue(`${DIALOG} textarea[name="ca_bundle"]`)).trim() === fakeForge.ca.trim(), "the typed CA bundle is gone after the refusal");
      fakeHost = `${fakeTLS}/gl`;
      await page.fill(`${DIALOG} input[name="web_url"]`, fakeHost);
      await page.fill(`${DIALOG} textarea[name="ca_bundle"]`, "");
      await saveEdit();
      const untrusted = "The server's certificate is signed by an unknown authority, an internal CA needs its CA bundle.";
      await openToken(fakeHost);
      await page.fill(`${DIALOG} input[name="token"]`, token);
      await page.click(`${DIALOG} [data-forge-token-save]`);
      const refused = await dialogError();
      assert(refused === untrusted, `the token's test said "${refused}"`);
      await page.click(`${DIALOG} [data-forge-token-cancel]`);
      await atRow(fakeHost);
      await expectTest(fakeHost, untrusted, false);
      await openEdit(fakeHost);
      await page.fill(`${DIALOG} textarea[name="ca_bundle"]`, fakeForge.ca);
      await saveEdit();
      assert((await hostRows()).find((r) => r.web === fakeHost)?.ca, "the row does not name its own CA");
      await expectTest(fakeHost, "Connected to GitLab 17.5.1 as root (Administrator).", true);
      await openEdit(fakeHost);
      const kept = await page.inputValue(`${DIALOG} textarea[name="ca_bundle"]`);
      assert(kept === fakeForge.ca.trim(), "the Edit does not show the CA bundle");
    });

    await run("token: Remove asks first, Cancel keeps the token, the confirm takes it away with its Test and lands on the row, the next Edit without the answered popup", async () => {
      await openToken(fakeHost);
      await page.click(`${DIALOG} [data-forge-token-remove]`);
      await page.waitForSelector(".swal2-cancel", { state: "visible", timeout: 8000 });
      const question = (await page.textContent(".swal2-title")).trim();
      assert(question === `Remove the token of ${fakeHost}?`, `the question reads "${question}"`);
      await page.click(".swal2-cancel");
      await page.waitForSelector(".swal2-container", { state: "detached", timeout: 5000 });
      assert(await page.locator(`${DIALOG} [data-forge-token-form]`).isVisible(), "Cancel closed the token view");
      await page.click(`${DIALOG} [data-forge-token-remove]`);
      await L.confirmSwal(page);
      await atRow(fakeHost);
      const result = (await page.textContent(`[data-forge-host="${fakeHost}"] [data-forge-test-result]`)).trim();
      assert(result === "Token removed.", `the row says "${result}"`);
      const row = (await hostRows()).find((r) => r.web === fakeHost);
      assert(row && row.token === "no token" && !row.test && row.edit && row.tokenLink?.text === "add token", `the row reads ${JSON.stringify(row)}`);
      assert(await page.locator(`[data-forge-host="${fakeHost}"] [data-forge-test]`).count() === 0, "a Test stands without a token");
      await page.waitForFunction(() => !document.querySelector(".swal2-container"), null, { timeout: 3000 }).catch(() => {});
      await page.click(`[data-forge-host="${fakeHost}"] [data-forge-edit]`);
      await page.waitForSelector(`${DIALOG} [data-forge-host-form]`, { timeout: 8000 });
      const stale = await page.$$eval(".swal2-container", (els) => els.map((el) => el.textContent.trim()));
      assert(!stale.length, `the Edit after the Remove holds the popup ${JSON.stringify(stale)}`);
      await page.click(`${DIALOG} .btn-close`);
    });

    await run("built in: github.com has no Edit, its address refuses one, its row opens its token view with GitHub's token page", async () => {
      const answer = await page.goto(`${BASE}/settings/forges/github.com`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      assert(answer.status() === 404 && await page.evaluate(() => document.body.innerText.includes("A built in host has nothing to edit, its token is set from its row.")), `the Edit of github.com answered ${answer.status()}`);
      assert(await page.locator("[data-forge-host-form]").count() === 0, "github.com has an Edit form");
      await openToken("https://github.com");
      const view = await tokenView();
      assert(view.title === "https://github.com" && view.intro === "A token lets the cockpit act on GitHub at https://github.com as you, give it one now or later from its row.", `the token view reads ${JSON.stringify(view)}`);
      assert(JSON.stringify(view.links) === JSON.stringify([{ text: "New token", href: ghPage("https://github.com"), shown: true, target: "_blank" }]), `the token view links ${JSON.stringify(view.links)}`);
      assert(view.required && !view.remove && view.save === "Save and test" && view.cancel === "Later", `the token view reads ${JSON.stringify(view)}`);
      await page.click(`${DIALOG} [data-forge-token-cancel]`);
      await atRow("https://github.com");
    });

    await run("phone: the list, the Add host dialog and the token view it leads on to stand within 390px, the close leads to the host's row", async () => {
      const mp = await mobilePage();
      await openForges(mp);
      const rows = await hostRows(mp);
      assert(rows.length >= 2 && rows.every((r) => r.painted), `the phone lists ${JSON.stringify(rows)}`);
      const wide = await mp.evaluate(() => ({ page: document.scrollingElement.scrollWidth, body: document.querySelector("[data-forge-host]").closest(".card").getBoundingClientRect().right, screen: innerWidth }));
      assert(wide.page <= wide.screen && wide.body <= wide.screen, `the list runs past the screen: ${JSON.stringify(wide)}`);
      await expectFlush(["", "Edit"], mp);
      await mp.click("[data-forge-add]");
      await mp.waitForSelector(`${DIALOG} [data-forge-host-form]`, { timeout: 8000 });
      const fit = await mp.evaluate(() => {
        const box = document.querySelector("[data-form-modal].show .modal-content").getBoundingClientRect();
        return { left: box.left, right: box.right, screen: innerWidth, focused: document.activeElement?.tagName };
      });
      assert(fit.left >= 0 && fit.right <= fit.screen, `the dialog runs past the screen: ${JSON.stringify(fit)}`);
      assert(fit.focused !== "INPUT" && fit.focused !== "SELECT" && fit.focused !== "TEXTAREA", `a field took the focus on touch: ${fit.focused}`);
      const before = await dialogHeight(mp);
      const web = `https://ph-${stamp}.example.com`;
      await mp.selectOption(`${DIALOG} select[name="kind"]`, "github");
      await mp.fill(`${DIALOG} input[name="web_url"]`, web);
      await mp.tap(`${DIALOG} [data-forge-save]`);
      await mp.waitForSelector(`${DIALOG} [data-forge-token-form]`, { timeout: 8000 });
      const view = await tokenView(mp);
      const links = [{ text: "New token", href: ghPage(web), shown: true, target: "_blank" }];
      assert(JSON.stringify(view.links) === JSON.stringify(links) && view.height === before, `the phone's token view reads ${JSON.stringify(view)}, the add stood ${before}px high`);
      const after = await mp.evaluate(() => {
        const box = document.querySelector("[data-form-modal].show .modal-content").getBoundingClientRect();
        const save = document.querySelector("[data-form-modal].show [data-forge-token-save]").getBoundingClientRect();
        return { left: box.left, right: box.right, bottom: box.bottom, save: save.bottom, screen: innerWidth, high: innerHeight, page: document.scrollingElement.scrollWidth, focused: document.activeElement?.tagName };
      });
      assert(after.left >= 0 && after.right <= after.screen && after.page <= after.screen && after.bottom <= after.high && after.save <= after.bottom, `the token view runs past the screen: ${JSON.stringify(after)}`);
      assert(after.focused !== "INPUT", "the token field took the focus on touch");
      await mp.tap(`${DIALOG} .btn-close`);
      await atRow(web, mp);
      const row = (await hostRows(mp)).find((r) => r.web === web);
      assert(row && row.token === "no token", `after the close the phone lists ${JSON.stringify(row)}`);
    });

    await run("docs: the projects topic explains the origin with its three icons, the Forges topic the hosts", async () => {
      await page.goto(`${BASE}/docs`, { waitUntil: "domcontentloaded" });
      const item = page.locator("#navigation .list-group-item", { hasText: "Where a repository lives" });
      assert(await item.count() === 1, "no docs entry for the origin");
      for (const icon of ["ti-brand-github", "ti-brand-gitlab", "ti-world"]) {
        assert(await item.locator(`i.${icon}`).count() === 1, `the docs entry lacks ${icon}`);
      }
      assert(await page.locator("section#forges .list-group-item", { hasText: "Forge hosts" }).count() === 1, "no docs entry for the hosts");
      const tokens = page.locator("section#forges .list-group-item", { hasText: "Tokens and the connection test" });
      assert(await tokens.count() === 1, "no docs entry for the tokens");
      const said = await tokens.textContent();
      assert(/add token.*change token.*Save and test.*Replace and test.*Remove takes it away.*New token opens the server's page/.test(said) && !said.includes("16.7"), `the tokens entry reads ${said}`);
      assert(await page.locator('section#forges a[href="/settings/forges"]').count() === 1, "the Forges topic does not link the page");
    });
  } finally {
    await dropHosts().catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
  }
});
