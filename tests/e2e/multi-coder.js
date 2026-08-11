const L = require("./lib");
const { assert, sleep, submitBtn, BASE, createProject, deleteProject } = L;

// Multi-coder: every instance serves all installed coders (--provider is
// deprecated and ignored). The UI adapts: /coders/new grows a coder select
// with one agent select per coder (dc-coder-select toggles them), the coder
// pages are settings of one coder and live at canonical
// /settings/coders/<coder>/{instructions,agents,skills} URLs: the settings
// sidebar ([data-settings-nav]) picks the coder ([data-settings-coder] rows,
// one per coder, the page's own marked active), the card header holds that
// coder's section tabs ([data-coder-sections]), and session rows show coder
// badges. Both older shapes 308-redirect to the canonical URLs: the
// pre-settings /coders/<coder>/... and the legacy top-level paths (/agents
// etc., coder via ?coder=). MODE=single asserts the adaptive parts stay off;
// it only applies on a host where a single coder CLI is installed.
// Gotcha: never save /instructions here, the instance writes the real
// per-coder files in $HOME. Only sessions created by this script are touched.

const MODE = process.env.MODE || "multi";

L.runFeature(`MULTI-CODER (${MODE})`, async ({ page, run }) => {
  const tag = `mc-${Date.now().toString(36)}`;

  if (MODE === "single") {
    await run("new session form: no coder select, hidden coder input", async () => {
      await page.goto(`${BASE}/coders/new`, { waitUntil: "domcontentloaded" });
      assert(!(await page.$('select[name="coder"]')), "coder select rendered in single mode");
      assert(await page.$('input[type="hidden"][name="coder"]'), "hidden coder input missing");
      const agents = await page.$$('select[name="agent"]');
      assert(agents.length === 1, `expected one agent select, got ${agents.length}`);
      assert(!(await page.$eval('select[name="agent"]', (s) => s.disabled)), "agent select disabled");
    });
    await run("one plain Coder entry on agents/skills/instructions", async () => {
      for (const path of ["/agents", "/skills", "/instructions"]) {
        await page.goto(`${BASE}${path}`, { waitUntil: "domcontentloaded" });
        assert(/\/settings\/coders\/\w+/.test(page.url()), `${path} did not land on a canonical coder URL: ${page.url()}`);
        const coders = await page.$$("[data-settings-coder]");
        assert(coders.length === 1, `expected one Coder entry, got ${coders.length} on ${path}`);
        assert((await coders[0].innerText()).trim() === "Coder", `single coder entry is not the plain Coder row on ${path}`);
        assert(await page.$("[data-coder-sections]"), `no section tabs on ${path}`);
      }
    });
    return;
  }

  await run("new session form: coder select toggles per-coder agent selects", async () => {
    await page.goto(`${BASE}/coders/new`, { waitUntil: "domcontentloaded" });
    const coders = await page.$$eval('select[name="coder"] option', (os) => os.map((o) => o.value));
    assert(coders.includes("copilot") && coders.includes("claude"), `coder options wrong: ${coders}`);
    const state = () => page.evaluate(() => [...document.querySelectorAll("[data-coder-agents]")].map((g) => ({
      coder: g.dataset.coderAgents, hidden: g.hidden, disabled: g.querySelector("select").disabled,
    })));
    let groups = await state();
    assert(groups.length === coders.length, `expected ${coders.length} agent groups, got ${groups.length}`);
    assert(groups.filter((g) => !g.hidden && !g.disabled).length === 1, "not exactly one active agent group");
    const inactive = groups.find((g) => g.hidden);
    await page.selectOption('select[name="coder"]', inactive.coder);
    groups = await state();
    const nowActive = groups.find((g) => g.coder === inactive.coder);
    assert(nowActive && !nowActive.hidden && !nowActive.disabled, "switching coder did not activate its agent select");
    assert(groups.filter((g) => !g.hidden && !g.disabled).length === 1, "more than one active agent group after switch");
  });

  await run("agents: sidebar coder rows + canonical URLs scope list and create", async () => {
    const id = `tcagent-${tag.slice(-5)}`;
    await page.goto(`${BASE}/agents`, { waitUntil: "domcontentloaded" });
    assert(/\/settings\/coders\/\w+\/agents$/.test(page.url()), `legacy /agents did not land on a canonical URL: ${page.url()}`);
    const rows = await page.$$eval("[data-settings-nav] [data-settings-coder]", (as) => as.map((a) => new URL(a.href).pathname));
    assert(rows.length === 2, `expected 2 coder rows in the sidebar, got ${rows.length}`);
    assert(rows.includes("/settings/coders/claude/agents") && rows.includes("/settings/coders/copilot/agents"), `coder rows wrong: ${rows}`);
    assert(await page.$("[data-settings-coder] svg.coder-icon"), "no Claude icon in the sidebar coder rows");
    const sections = await page.$$eval("[data-coder-sections] a", (as) => as.map((a) => new URL(a.href).pathname));
    assert(JSON.stringify(sections) === JSON.stringify(["/settings/coders/copilot/account", "/settings/coders/copilot/instructions", "/settings/coders/copilot/agents", "/settings/coders/copilot/skills"]), `section tabs wrong: ${sections}`);
    await page.goto(`${BASE}/agents/new?coder=claude`, { waitUntil: "domcontentloaded" });
    assert(page.url().endsWith("/settings/coders/claude/agents/new"), `legacy coder query did not map to canonical URL: ${page.url()}`);
    await page.fill('input[name="agent_id"]', id); await page.fill('input[name="agent_description"]', "throwaway"); await page.fill('textarea[name="agent_instructions"]', "test only");
    await Promise.all([page.waitForURL(/\/settings\/coders\/claude\/agents$/, { timeout: 10000 }), submitBtn(page, 'input[name="agent_id"]').click()]);
    assert(await page.evaluate((i) => document.body.innerHTML.includes(i), id), "agent not listed on the claude page");
    await page.goto(`${BASE}/settings/coders/copilot/agents`, { waitUntil: "domcontentloaded" });
    assert(!(await page.$(`a[href*="${id}/edit"]`)), "claude agent leaked into the copilot page");
    await page.goto(`${BASE}/coders/claude/agents`, { waitUntil: "domcontentloaded" });
    assert(page.url().endsWith("/settings/coders/claude/agents"), `pre-settings coder URL did not redirect: ${page.url()}`);
    const del = await page.$(`form[action="/settings/coders/claude/agents/${id}/delete"]`);
    assert(del, "no coder-scoped delete form on the claude page");
    await (await del.$("button, input[type=submit]")).click(); await L.confirmSwal(page);
    await page.waitForFunction((i) => !document.querySelector(`form[action$="/agents/${i}/delete"]`), id, { timeout: 8000 });
  });

  await run("skills + instructions: sidebar marks coder, tabs mark section, form posts canonical", async () => {
    for (const path of ["/skills", "/instructions"]) {
      await page.goto(`${BASE}${path}?coder=copilot`, { waitUntil: "domcontentloaded" });
      assert(page.url().endsWith(`/settings/coders/copilot${path}`), `legacy ${path}?coder=copilot did not land on canonical URL: ${page.url()}`);
      const row = await page.$eval("[data-settings-coder].active", (a) => new URL(a.href).pathname);
      assert(row === `/settings/coders/copilot${path}`, `${path}: copilot row not active or lost the section (${row})`);
      const section = await page.$eval("[data-coder-sections] a.active", (a) => new URL(a.href).pathname);
      assert(section === `/settings/coders/copilot${path}`, `${path}: section tab not active (${section})`);
    }
    assert(await page.$('form[action="/settings/coders/copilot/instructions"]'), "instructions form does not post to the canonical path");
  });

  const project = `tcmulti-${tag.slice(-5)}`;
  let sessionUrl = "";
  try {
    await run("create copilot session via coder select, badge on attach + projects", async () => {
      await createProject(page, project);
      await page.goto(`${BASE}/coders/new?project=${encodeURIComponent(project)}`, { waitUntil: "domcontentloaded" });
      await page.selectOption('select[name="coder"]', "copilot");
      await page.fill('input[name="name"]', `s-${tag}`);
      await Promise.all([page.waitForURL(/\/coders\/(?!new)[^/]+$/, { timeout: 20000 }), submitBtn(page, 'input[name="name"]').click()]);
      sessionUrl = page.url();
      assert(await page.$('.attach-page span[title="Copilot"] i.ti-brand-github-copilot'), "no Copilot icon on attach page");
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      const badge = await page.evaluate((p) => {
        const scope = document.getElementById(`project-${p}`);
        return scope && !!scope.querySelector('span[title="Copilot"] i.ti-brand-github-copilot');
      }, project);
      assert(badge, "no Copilot icon on the project session row");
    });

    await run("quicknav fragment shows coder labels", async () => {
      const html = await page.evaluate(async () => (await fetch("/quicknav?path=/projects")).text());
      assert(html.includes("Copilot") || html.includes("Claude"), "quicknav fragment has no coder label");
    });
  } finally {
    if (sessionUrl) { await L.stopSession(page, sessionUrl).catch(() => {}); await sleep(500); }
    await deleteProject(page, project).catch(() => {});
  }
});
