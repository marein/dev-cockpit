const path = require("path");
const L = require("./lib");
const { assert, BASE, sleep } = L;

// Repository search of the create form (GET /projects/new/repositories,
// internal/forge repositories.go, dc-repo-search in projects_new.gohtml): the
// Create select offers, after "An existing repository", one entry per forge
// host with a token, named as Settings › Forges lists it, against the
// runner's fake forge (forge-fake.js) as a GitLab below /gl on FORGE_ADDR and
// a GitHub Enterprise Server at the root of FORGE_ADDR2 (default 127.0.0.2),
// both on FORGE_PORT, which the docker run publishes on both addresses. "An
// existing repository" is the plain address field again. An entry's field
// searches that host alone: focused it lists the account's own repositories
// in the forge's order, each with the forge's mark and private, internal,
// fork or archived, a hostile path as text and an SSH address on another
// server replaced by the host's own https one. Typing narrows them at once,
// GitLab's by every word anywhere in the full path with its groups and in any
// order, a name of one or two characters by itself and beside a longer word,
// from three characters the public ones follow under Public once the
// typing pauses, one search of the forge for the word and none of the other
// host. Arrows move the mark, Enter and a click fill in the address, show it
// under the field and name the project, Escape and leaving the field put back
// the repository picked, in the dialog Escape closes the list before the
// dialog. A term no search takes says why, without Try again. A public search
// slower than its bound and one GitHub limits drop out without a word, a
// limited search holds the next one back and never the list; a host that
// fails says so with Try again and holds no other host's search. A create
// carries the picked address as the clone URL and a refused one comes back on
// the same entry. On a phone the list stands within 390px and a tap fills the
// address. Without a token no host is offered. The docs entry names it. The
// runner measures how long the public search took and reports it. SHOTS_DIR,
// where set and mounted, takes the screenshots of the select, the list on the
// page, in the dialog and on the phone, light and dark. The hosts carry the
// run's stamp and go at the end; nothing is cloned.

const { startFakeForge, addHost } = require("./forge-fake");

const DIALOG = "[data-form-modal].show";
const MENU = "[data-repo-menu]";
const FAKE_PORT = Number(process.env.FORGE_PORT || 3157);
const FAKE_TLS_PORT = Number(process.env.FORGE_TLS_PORT || 3158);
const FAKE_ADDR = process.env.FORGE_ADDR || "127.0.0.1";
const GH_ADDR = process.env.FORGE_ADDR2 || "127.0.0.2";
const TOKEN = `glpat-e2e${Date.now().toString(36)}Rs7kQ2wE9tY4`;
let fakeForge = null;

const OWN_GITLAB = [
  { path: "team/sub/app", visibility: "private", fork: true },
  { path: "team/tool", visibility: "internal", archived: true },
  { path: "team/<b>bold</b>" },
  { path: "team/elsewhere", ssh: "git@evil.example.com:team/elsewhere.git" },
];
const OWN_GITHUB = [{ path: "octo/dev-cockpit" }, { path: "octo/dotfiles", visibility: "private" }];
const PUBLIC_GITLAB = [{ path: "public/app-kit", name: "app-kit" }, { path: "team/sub/app", visibility: "public" }];
const PUBLIC_GITHUB = [{ path: "torvalds/app-linux" }, { path: "octo/app-public" }];

L.runFeature("FORGE REPO SEARCH", async ({ page, run, mobilePage, engine }) => {
  const stamp = Date.now().toString(36);
  fakeForge ||= await startFakeForge({ port: FAKE_PORT, tlsPort: FAKE_TLS_PORT, token: TOKEN });
  const { state, log } = fakeForge;
  const glWeb = `http://${FAKE_ADDR}:${FAKE_PORT}/gl`;
  const ghWeb = `http://${GH_ADDR}:${FAKE_PORT}`;
  const hosts = {};
  const measured = [];

  const post = (target, fields) => page.evaluate(async ({ target, fields }) => {
    const body = new URLSearchParams({ ...fields, csrf_token: document.querySelector('meta[name="csrf-token"]').content });
    const r = await fetch(target, { method: "POST", body, headers: { Accept: "application/json" } });
    return r.status;
  }, { target, fields });

  // menuState reads the form as it stands: the list shown by its real box,
  // its rows with mark, path, hint and whether marked, the notes, the public
  // header, the busy lines, any element a forge's text could have made, the
  // search field, the address it filled in and the line that shows it.
  const menuState = (p, scope = "") => p.evaluate(({ MENU, scope }) => {
    const root = scope ? document.querySelector(scope) : document;
    const seen = (el) => {
      if (!el) return false;
      const box = el.getBoundingClientRect();
      const style = getComputedStyle(el);
      return box.width > 0 && box.height > 0 && style.display !== "none" && style.visibility !== "hidden";
    };
    const menu = root?.querySelector(MENU);
    const box = menu?.getBoundingClientRect();
    const line = root?.querySelector("[data-repo-address-line]");
    return {
      shown: seen(menu),
      box: box ? { left: box.left, right: box.right, top: box.top, bottom: box.bottom } : null,
      rows: [...(menu?.querySelectorAll("[data-repo-option]") || [])].map((r) => {
        const b = r.getBoundingClientRect();
        return {
          path: r.querySelector(".text-truncate")?.textContent || "",
          url: r.dataset.repoOption,
          icon: [...(r.querySelector("i")?.classList || [])].find((c) => c.startsWith("ti-")) || "",
          hint: r.querySelector("[data-repo-hint]")?.textContent || "",
          active: r.classList.contains("active"),
          public: [...menu.querySelectorAll("[data-repo-public] ~ [data-repo-option]")].includes(r),
          left: b.left, right: b.right,
        };
      }),
      notes: [...(menu?.querySelectorAll("[data-repo-note]") || [])].map((n) => ({ text: n.textContent, danger: n.classList.contains("text-danger") })),
      retry: seen(menu?.querySelector("[data-repo-retry]")),
      publicHead: !!menu?.querySelector("[data-repo-public]"),
      busy: !!menu?.querySelector("[data-repo-searching]"),
      publicBusy: !!menu?.querySelector("[data-repo-public-searching]"),
      madeTags: menu?.querySelectorAll("b").length || 0,
      value: root?.querySelector("[data-repo-search]")?.value ?? "",
      clone: root?.querySelector('input[name="clone_url"]')?.value ?? "",
      line: seen(line) ? line.textContent.trim() : "",
      name: root?.querySelector("[data-project-name-field]")?.value ?? "",
      focused: !!root && document.activeElement === root.querySelector("[data-repo-search]"),
    };
  }, { MENU, scope });

  const waitMenu = async (p, test, what, timeout = 8000, scope = "") => {
    const until = Date.now() + timeout;
    let last = null;
    while (Date.now() < until) {
      last = await menuState(p, scope);
      if (test(last)) return last;
      await sleep(100);
    }
    throw new Error(`${what}: ${JSON.stringify(last)}`);
  };

  const searches = () => log.filter((r) => r.path === "/api/v3/search/repositories");
  const ghAsked = () => log.filter((r) => r.path.startsWith("/api/v3/user/repos") || r.path.startsWith("/api/v3/search/"));
  const glAsked = () => log.filter((r) => r.path === "/gl/api/v4/projects");
  const glPublic = () => glAsked().filter((r) => r.query.includes("visibility=public"));

  const publicTimings = (p) => p.evaluate(() => performance.getEntriesByType("resource")
    .filter((e) => e.name.includes("/projects/new/repositories") && e.name.includes("scope=public"))
    .map((e) => Math.round(e.responseEnd - e.startTime)));

  const options = (p, scope = "") => p.evaluate((scope) => [...document.querySelectorAll(`${scope} select[name="create"] option`)].map((o) => ({ value: o.value, label: o.textContent })), scope);

  // openForm opens the create page on the entry of host. The select on the
  // page opens the create dialog, which the dialog's check covers.
  const openForm = async (p, host) => {
    await p.goto(`${BASE}/projects/new?create=${encodeURIComponent(`forge:${hosts[host]}`)}`, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(p);
    await p.waitForSelector(`dc-repo-search[data-repo-host="${hosts[host]}"]`, { timeout: 8000 });
    assert((await L.waitUpgraded(p, ["dc-repo-search", "dc-project-new"], 8000)).length === 0, "the search did not upgrade");
  };

  const focusField = async (p) => {
    if (!(await menuState(p)).focused) await p.click("[data-repo-search]");
  };

  const shoot = async (p, name) => {
    if (!process.env.SHOTS_DIR) return;
    await p.screenshot({ path: path.join(process.env.SHOTS_DIR, `repo-search-${engine}-${name}.png`) });
  };

  try {
    await run("setup: a GitLab and a GitHub Enterprise Server with the token", async () => {
      Object.assign(state, { glOwn: OWN_GITLAB, ghOwn: OWN_GITHUB, glPublic: PUBLIC_GITLAB, ghPublic: PUBLIC_GITHUB, publicDelay: 0, glOwnFail: false, searchRemaining: null });
      await page.goto(`${BASE}/settings/forges`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      for (const [key, host] of [["gl", { kind: "gitlab", web_url: glWeb, aliases: `gl-${stamp}` }], ["gh", { kind: "github", web_url: ghWeb, aliases: `gh-${stamp}` }]]) {
        const added = await addHost(page, { ...host, token: TOKEN });
        assert(!added.failed, added.failed);
        hosts[key] = added.id;
      }
    });

    await run("the select offers each host with a token after An existing repository, named as Settings › Forges lists it", async () => {
      await page.emulateMedia({ colorScheme: "light" });
      await page.goto(`${BASE}/projects/new`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      const got = (await options(page)).slice(0, 4);
      const want = [
        { value: "", label: "An empty project" },
        { value: "clone", label: "An existing repository" },
        { value: `forge:${hosts.gl}`, label: `From ${FAKE_ADDR}:${FAKE_PORT}/gl` },
        { value: `forge:${hosts.gh}`, label: `From ${GH_ADDR}:${FAKE_PORT}` },
      ];
      assert(JSON.stringify(got) === JSON.stringify(want), `the select reads ${JSON.stringify(got)}`);
      const all = await options(page);
      assert(all.slice(4).every((o) => o.value.startsWith("worktree:")), `after the hosts stand ${JSON.stringify(all.slice(4, 6))}`);
      assert(!all.some((o) => /From (GitHub|GitLab)$/.test(o.label)), "a built in host without a token is offered");
      return got.map((o) => o.label).join(" | ");
    });

    await run("An existing repository is the plain address field, nothing searched", async () => {
      log.splice(0);
      const asked = [];
      const listen = (r) => { if (r.url().includes("/projects/new/repositories")) asked.push(r.url()); };
      page.on("request", listen);
      await page.goto(`${BASE}/projects/new?create=clone`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector("#clone_url", { timeout: 8000 });
      const field = await page.evaluate(() => {
        const input = document.querySelector("#clone_url");
        return { search: !!document.querySelector("dc-repo-search"), role: input.getAttribute("role"), placeholder: input.placeholder, name: input.name };
      });
      assert(!field.search && field.role === null && field.name === "clone_url" && field.placeholder === "https://github.com/you/project.git", `the field reads ${JSON.stringify(field)}`);
      await page.click("#clone_url");
      await page.keyboard.type("app", { delay: 50 });
      await sleep(1200);
      await page.locator("#clone_url").fill("git@example.com:team/app.git");
      const name = await page.locator("[data-project-name-field]").inputValue();
      page.off("request", listen);
      assert(asked.length === 0 && log.length === 0, `the address field searched: ${asked.join(", ")}`);
      assert(name === "app", `the address named the project ${name}`);
    });

    await run("the GitLab entry lists its own repositories alone, in the forge's order", async () => {
      log.splice(0);
      await openForm(page, "gl");
      await shoot(page, "desktop-select");
      await focusField(page);
      const s = await waitMenu(page, (m) => m.shown && m.rows.length === 4 && !m.busy, "the own rows never stood");
      const got = s.rows.map((r) => `${r.icon} ${r.path} ${r.hint}`.trim());
      const want = [
        "ti-brand-gitlab team/sub/app private · fork",
        "ti-brand-gitlab team/tool internal · archived",
        "ti-brand-gitlab team/<b>bold</b>",
        "ti-brand-gitlab team/elsewhere",
      ];
      assert(JSON.stringify(got) === JSON.stringify(want), `the rows read ${JSON.stringify(got)}`);
      assert(s.rows[0].active && s.rows.filter((r) => r.active).length === 1, "the first row is not the one marked");
      assert(s.madeTags === 0, "a forge's path made markup");
      assert(s.rows[3].url === `${glWeb}/team/elsewhere.git` && s.rows[0].url === `git@${FAKE_ADDR}:team/sub/app.git`, `the addresses read ${s.rows[0].url} and ${s.rows[3].url}`);
      assert(s.notes.length === 0 && !s.publicHead && !s.publicBusy && !s.line, `the list says ${JSON.stringify(s.notes)}, line ${s.line}`);
      assert(glAsked().length >= 1 && ghAsked().length === 0, `the GitLab search asked GitHub ${ghAsked().length} times`);
      await shoot(page, "desktop-light");
      await page.emulateMedia({ colorScheme: "dark" });
      await openForm(page, "gl");
      await focusField(page);
      await waitMenu(page, (m) => m.shown && m.rows.length === 4 && !m.busy, "the own rows never stood in the dark");
      await shoot(page, "desktop-dark");
      await page.emulateMedia({ colorScheme: "light" });
      return got.join(" | ");
    });

    await run("typing narrows the own rows and the public ones follow under Public, one search of the forge", async () => {
      await openForm(page, "gl");
      await focusField(page);
      await waitMenu(page, (m) => m.shown && !m.busy, "the list never stood");
      log.splice(0);
      const start = Date.now();
      await page.keyboard.type("app", { delay: 70 });
      const own = await waitMenu(page, (m) => m.shown && !m.busy && m.rows.length >= 1 && m.rows[0].path === "team/sub/app", "the own row never stood");
      const ownAt = Date.now() - start;
      const s = await waitMenu(page, (m) => m.publicHead && !m.publicBusy, "the public rows never came", 8000);
      const publicAt = Date.now() - start;
      const mine = s.rows.filter((r) => !r.public).map((r) => r.path);
      const theirs = s.rows.filter((r) => r.public).map((r) => `${r.icon} ${r.path}`);
      assert(JSON.stringify(mine) === JSON.stringify(["team/sub/app"]), `own rows ${JSON.stringify(mine)}`);
      assert(JSON.stringify(theirs) === JSON.stringify(["ti-brand-gitlab public/app-kit"]), `public rows ${JSON.stringify(theirs)}`);
      assert(glPublic().length === 1 && ghAsked().length === 0, `searched GitLab ${glPublic().length} and GitHub ${ghAsked().length} times for one word`);
      assert(glPublic()[0].auth === `Bearer ${TOKEN}` && !glPublic()[0].query.includes(TOKEN), "the token went elsewhere than the header");
      assert(own.rows[0].active, "the first own row is not marked");
      const timings = await publicTimings(page);
      measured.push(`public search ${timings.join("/")} ms (request to answer), own rows ${ownAt} ms and public rows ${publicAt} ms after the first key`);
      await shoot(page, "desktop-public");
      return measured[measured.length - 1];
    });

    await run("arrows move the mark, Enter fills in the SSH address, shows it and names the project", async () => {
      await page.keyboard.press("ArrowDown");
      let s = await menuState(page);
      assert(s.rows[1].active && !s.rows[0].active, `ArrowDown marked ${JSON.stringify(s.rows.map((r) => r.active))}`);
      await page.keyboard.press("ArrowDown");
      s = await menuState(page);
      assert(s.rows[0].active, "ArrowDown from the last row does not wrap to the first");
      await page.keyboard.press("ArrowUp");
      s = await menuState(page);
      assert(s.rows[1].active, "ArrowUp from the first row does not wrap to the last");
      await page.keyboard.press("Enter");
      s = await waitMenu(page, (m) => !m.shown, "the list stayed after Enter");
      assert(s.value === "public/app-kit" && s.clone === `git@${FAKE_ADDR}:public/app-kit.git`, `Enter filled in ${s.value} and ${s.clone}`);
      assert(s.line === `Clones from git@${FAKE_ADDR}:public/app-kit.git`, `the line under the field reads ${s.line}`);
      assert(s.name === "app-kit", `the project name reads ${s.name}`);
      assert(s.focused, "the field lost the focus");
      assert(page.url().includes("/projects/new"), "Enter submitted the form");
    });

    await run("Escape and leaving the field put back the repository picked", async () => {
      await page.keyboard.type("zz", { delay: 50 });
      await waitMenu(page, (m) => m.shown && !m.busy, "typing opened no list");
      await page.keyboard.press("Escape");
      let s = await waitMenu(page, (m) => !m.shown, "Escape left the list");
      assert(s.value === "public/app-kit" && s.clone === `git@${FAKE_ADDR}:public/app-kit.git`, `Escape left ${s.value} and ${s.clone}`);
      await page.keyboard.press("ArrowDown");
      s = await waitMenu(page, (m) => m.shown && !m.busy && m.rows.length === 4, "ArrowDown did not list the own rows again");
      await page.locator("[data-repo-search]").fill("");
      await page.keyboard.type("too", { delay: 50 });
      await waitMenu(page, (m) => m.shown && !m.busy && m.rows.some((r) => r.path === "team/tool"), "the row never stood");
      await page.locator("[data-project-name-field]").click();
      s = await waitMenu(page, (m) => !m.shown, "leaving the field left the list");
      assert(s.value === "public/app-kit" && s.clone === `git@${FAKE_ADDR}:public/app-kit.git` && s.name === "app-kit", `leaving left ${s.value}, ${s.clone} and ${s.name}`);
    });

    await run("the own GitLab rows match the words anywhere in the full path with its groups, in any order, a short name too", async () => {
      state.glOwn = [...OWN_GITLAB, { path: "orbit/garden-portal/app", name: "app", visibility: "private" }, { path: "orbit/garden-portal/ui", name: "ui", visibility: "private" }];
      const app = "orbit/garden-portal/app";
      const ui = "orbit/garden-portal/ui";
      const asked = [];
      try {
        for (const c of [
          { term: "garden", search: "garden", groups: true, perPage: "8", want: [app, ui] },
          { term: "portal garden app", search: "portal garden app", groups: true, perPage: "8", want: [app] },
          { term: "garden-portal/app", search: "garden-portal/app", groups: true, perPage: "8", want: [app] },
          { term: "orbit app", search: "orbit app", groups: true, perPage: "8", want: [app] },
          { term: "ui", search: "ui", groups: false, perPage: "8", want: [ui] },
          { term: "ui garden", search: "garden", groups: true, perPage: "100", want: [ui] },
        ]) {
          await openForm(page, "gl");
          await focusField(page);
          await waitMenu(page, (m) => m.shown && !m.busy, "the list never stood");
          log.splice(0);
          await page.keyboard.type(c.term, { delay: 30 });
          const ownQuery = () => glAsked().map((r) => new URLSearchParams(r.query)).find((q) => q.get("membership") === "true" && q.get("search") === c.search && q.has("search_namespaces") === c.groups);
          const s = await waitMenu(page, (m) => m.value === c.term && !!ownQuery() && m.shown && !m.busy, `the search for ${c.term} never ended`);
          const mine = s.rows.filter((r) => !r.public).map((r) => r.path);
          assert(JSON.stringify(mine) === JSON.stringify(c.want), `${c.term} found ${JSON.stringify(mine)}`);
          assert((!c.groups || ownQuery().get("search_namespaces") === "true") && ownQuery().get("per_page") === c.perPage, `${c.term} asked ${ownQuery()}`);
          assert(glPublic().every((r) => !r.query.includes("search_namespaces")), `the public search matched the groups for ${c.term}`);
          asked.push(`${c.term} → ${ownQuery()}`);
        }
      } finally {
        state.glOwn = OWN_GITLAB;
        await page.keyboard.press("Escape");
      }
      return asked.join(" | ");
    });

    await run("a click on a row of the GitHub entry fills in its address and asks no GitLab", async () => {
      await openForm(page, "gh");
      await focusField(page);
      log.splice(0);
      await page.keyboard.type("dot", { delay: 50 });
      await waitMenu(page, (m) => m.shown && !m.busy && m.rows.length >= 1 && m.rows[0].path === "octo/dotfiles", "the row never stood");
      await page.locator(`${MENU} [data-repo-option]`, { hasText: "octo/dotfiles" }).click();
      const s = await waitMenu(page, (m) => !m.shown, "the list stayed after the click");
      assert(s.clone === `git@${GH_ADDR}:octo/dotfiles.git` && s.value === "octo/dotfiles" && s.name === "dotfiles", `the click filled in ${s.clone}, ${s.value} and ${s.name}`);
      assert(glAsked().length === 0 && ghAsked().length >= 1, `the GitHub search asked GitLab ${glAsked().length} and GitHub ${ghAsked().length} times`);
    });

    await run("a term no search takes says why, without Try again", async () => {
      await page.locator("[data-repo-search]").click();
      await page.locator("[data-repo-search]").fill("");
      await waitMenu(page, (m) => m.shown && !m.busy, "the list never stood");
      await sleep(300);
      log.splice(0);
      await page.keyboard.type("a:b", { delay: 50 });
      const s = await waitMenu(page, (m) => m.shown && !m.busy && m.notes.length === 1, "no sentence stood");
      assert(s.notes[0].text === "A search term holds no colon and no quote, an address goes under An existing repository." && s.notes[0].danger && !s.retry, `the list says ${JSON.stringify(s.notes)}, retry ${s.retry}`);
      await sleep(900);
      assert(log.length === 0, `a refused term reached the forge: ${JSON.stringify(log.map((r) => r.path))}`);
      await page.keyboard.press("Escape");
    });

    await run("a public search past its bound drops out quietly, the own rows stand at once", async () => {
      await openForm(page, "gl");
      await focusField(page);
      await waitMenu(page, (m) => m.shown && !m.busy, "the list never stood");
      state.publicDelay = 8000;
      await page.evaluate(() => performance.clearResourceTimings());
      const start = Date.now();
      await page.keyboard.type("tool", { delay: 60 });
      await waitMenu(page, (m) => m.shown && !m.busy && m.rows.length === 1 && m.rows[0].path === "team/tool", "the own row did not stand at once", 2500);
      const ownAt = Date.now() - start;
      await waitMenu(page, (m) => m.publicBusy, "the public search never started", 3000);
      const s = await waitMenu(page, (m) => !m.publicBusy, "the public search never ended", 10000);
      const endedAt = Date.now() - start;
      assert(!s.publicHead && s.notes.length === 0, `a slow public search left ${JSON.stringify(s)}`);
      assert(s.rows.length === 1, "the own row went with it");
      const timings = await publicTimings(page);
      assert(timings.length === 1 && timings[0] >= 4500 && timings[0] < 6500, `the slow public search took ${timings.join("/")} ms`);
      measured.push(`slow public search cut at ${timings[0]} ms, own row ${ownAt} ms after the first key, the section gone ${endedAt} ms after it`);
      state.publicDelay = 0;
      return measured[measured.length - 1];
    });

    await run("a GitHub search past its limit drops out quietly and holds the next one back, never the list", async () => {
      await openForm(page, "gh");
      await focusField(page);
      await waitMenu(page, (m) => m.shown && !m.busy, "the list never stood");
      state.searchRemaining = 0;
      state.searchReset = 5;
      log.splice(0);
      await page.keyboard.type("kit", { delay: 60 });
      let s = await waitMenu(page, (m) => !m.publicBusy && !m.busy && searches().length === 1, "the limited search never ended");
      assert(s.rows.length === 0 && !s.publicHead, `rows ${JSON.stringify(s.rows.map((r) => r.path))}`);
      assert(s.notes.length === 1 && s.notes[0].text === "No repository of yours matches." && !s.notes[0].danger, `notes ${JSON.stringify(s.notes)}`);
      const lists = log.filter((r) => r.path === "/api/v3/user/repos").length;
      await page.keyboard.type("-", { delay: 60 });
      await sleep(1500);
      s = await waitMenu(page, (m) => !m.publicBusy && !m.busy, "the second search never ended");
      assert(searches().length === 1, `the held search reached GitHub: ${searches().length}`);
      assert(log.filter((r) => r.path === "/api/v3/user/repos").length > lists, "the list was held back with the search");
      state.searchRemaining = null;
      await page.keyboard.press("Escape");
      await sleep(5500);
    });

    await run("a host that fails says so with Try again and holds no other host's search", async () => {
      state.glOwnFail = true;
      await openForm(page, "gl");
      await focusField(page);
      let s = await waitMenu(page, (m) => m.shown && !m.busy && m.notes.length === 1, "the failure never stood");
      assert(s.rows.length === 0 && s.notes[0].text === "The server answered 500 Internal Server Error." && s.notes[0].danger && s.retry, `the list reads ${JSON.stringify(s)}`);
      await openForm(page, "gh");
      await focusField(page);
      s = await waitMenu(page, (m) => m.shown && !m.busy && m.rows.length === 2, "the GitHub rows never stood");
      assert(s.notes.length === 0, `the GitHub list says ${JSON.stringify(s.notes)}`);
      await openForm(page, "gl");
      await focusField(page);
      await waitMenu(page, (m) => m.retry, "Try again never stood");
      state.glOwnFail = false;
      await page.locator(`${MENU} [data-repo-retry]`).click();
      s = await waitMenu(page, (m) => m.shown && !m.busy && m.rows.length === 4 && m.notes.length === 0, "Try again found nothing");
    });

    await run("a create carries the picked address and a refused one comes back on the entry", async () => {
      await openForm(page, "gl");
      await focusField(page);
      await page.keyboard.type("elsewhere", { delay: 30 });
      await waitMenu(page, (m) => m.shown && !m.busy && m.rows.length === 1, "the row never stood");
      await page.keyboard.press("Enter");
      const name = `rsclone-${stamp}`;
      await page.locator("[data-project-name-field]").fill(name);
      log.splice(0);
      await Promise.all([page.waitForURL(/\/projects\/new\?.*clone_url=/, { timeout: 30000 }), page.getByRole("button", { name: "Create project" }).click()]);
      await L.waitUpgraded(page, ["dc-repo-search"], 8000);
      const u = new URL(page.url());
      assert(u.searchParams.get("create") === `forge:${hosts.gl}` && u.searchParams.get("clone_url") === `${glWeb}/team/elsewhere.git`, `the refusal led to ${page.url()}`);
      const s = await menuState(page);
      const picked = await page.evaluate(() => document.querySelector('select[name="create"]').value);
      assert(picked === `forge:${hosts.gl}` && s.value === "team/elsewhere" && s.clone === `${glWeb}/team/elsewhere.git` && s.name === name, `the form came back as ${picked}, ${JSON.stringify(s)}`);
      assert(s.line === `Clones from ${glWeb}/team/elsewhere.git`, `the line reads ${s.line}`);
      assert(log.some((r) => r.path.startsWith("/gl/team/elsewhere.git")), `git never asked the fake: ${JSON.stringify(log.map((r) => r.path))}`);
      const flash = await page.locator(".alert, .toast").allInnerTexts().catch(() => []);
      return `git said ${flash.join(" ").replace(/\s+/g, " ").slice(0, 120)}`;
    });

    await run("dialog: an entry searches in place, Escape closes the list before the dialog", async () => {
      for (const scheme of ["light", "dark"]) {
        await page.emulateMedia({ colorScheme: scheme });
        await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
        await L.dismissUpdate(page);
        assert((await L.waitUpgraded(page, ["dc-form-modal"], 8000)).length === 0, "dc-form-modal not upgraded");
        await page.click('a[href="/projects/new"]');
        await page.waitForSelector(`${DIALOG} form`, { timeout: 8000 });
        const offered = (await options(page, DIALOG)).map((o) => o.value);
        assert(offered.includes(`forge:${hosts.gl}`) && offered.includes(`forge:${hosts.gh}`), `the dialog offers ${JSON.stringify(offered.slice(0, 5))}`);
        await page.selectOption(`${DIALOG} select[name="create"]`, `forge:${hosts.gl}`);
        await page.waitForSelector(`${DIALOG} [data-repo-search]`, { timeout: 8000 });
        await page.click(`${DIALOG} [data-repo-search]`);
        await page.keyboard.type("app", { delay: 50 });
        const s = await waitMenu(page, (m) => m.shown && !m.busy && m.publicHead && !m.publicBusy && m.rows.length === 2, "the dialog's list never stood", 8000, DIALOG);
        const dialogBox = await page.locator(`${DIALOG} .modal-content`).boundingBox();
        assert(s.box.left >= dialogBox.x - 1 && s.box.right <= dialogBox.x + dialogBox.width + 1, `the list leaves the dialog: ${JSON.stringify(s.box)} in ${JSON.stringify(dialogBox)}`);
        await shoot(page, `dialog-${scheme}`);
        await page.keyboard.press("Escape");
        await waitMenu(page, (m) => !m.shown, "Escape left the list", 3000, DIALOG);
        assert(await page.locator(DIALOG).count() === 1, "Escape closed the dialog with the list");
        await page.keyboard.press("Escape");
        await page.waitForSelector(DIALOG, { state: "detached", timeout: 5000 }).catch(() => {});
        assert(await page.locator(DIALOG).count() === 0, "the second Escape kept the dialog");
      }
      await page.emulateMedia({ colorScheme: "light" });
    });

    await run("phone: the list stands within 390px and a tap fills in the address", async () => {
      const mp = await mobilePage();
      for (const scheme of ["light", "dark"]) {
        await mp.emulateMedia({ colorScheme: scheme });
        await openForm(mp, "gh");
        await mp.locator("[data-repo-search]").tap();
        const s = await waitMenu(mp, (m) => m.shown && !m.busy && m.rows.length === 2, "the phone's list never stood");
        assert(s.box.left >= 0 && s.box.right <= 390, `the list leaves the screen: ${JSON.stringify(s.box)}`);
        assert(s.rows.every((r) => r.left >= 0 && r.right <= 390), "a row leaves the screen");
        await shoot(mp, `phone-${scheme}`);
        await mp.locator(`${MENU} [data-repo-option]`, { hasText: "octo/dev-cockpit" }).tap();
        const after = await waitMenu(mp, (m) => !m.shown, "the list stayed after the tap");
        assert(after.clone === `git@${GH_ADDR}:octo/dev-cockpit.git` && after.value === "octo/dev-cockpit" && after.name === "dev-cockpit", `the tap filled in ${after.clone}, ${after.value} and ${after.name}`);
        assert(after.line === `Clones from git@${GH_ADDR}:octo/dev-cockpit.git`, `the line reads ${after.line}`);
        await shoot(mp, `phone-picked-${scheme}`);
      }
      await mp.emulateMedia({ colorScheme: null });
    });

    await run("docs: Clone a repository names the entries", async () => {
      await page.goto(`${BASE}/docs`, { waitUntil: "domcontentloaded" });
      const text = await page.locator("#navigation .list-group-item", { hasText: "Clone a repository" }).innerText();
      for (const said of ["An existing repository", "From GitHub", "alone", "Public", "SSH address", "token"]) {
        assert(text.includes(said), `the entry lacks "${said}": ${text}`);
      }
    });
  } finally {
    Object.assign(state, { publicDelay: 0, glOwnFail: false, searchRemaining: null, searchReset: 60 });
    for (const id of Object.values(hosts)) await post(`/settings/forges/${id}`, { form: "delete" }).catch(() => {});
  }

  await run("without a token no host is offered and An existing repository stands as before", async () => {
    await page.goto(`${BASE}/projects/new?create=clone`, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page).catch(() => {});
    const offered = (await options(page)).map((o) => o.value);
    const s = await page.evaluate(() => ({ search: !!document.querySelector("dc-repo-search"), field: !!document.querySelector('#clone_url[name="clone_url"]') }));
    assert(!offered.some((v) => v.startsWith("forge:")) && offered[0] === "" && offered[1] === "clone" && !s.search && s.field, `the form reads ${JSON.stringify(offered.slice(0, 4))}, ${JSON.stringify(s)}`);
    return measured.join("; ");
  });
});
