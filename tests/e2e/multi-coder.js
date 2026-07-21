const L = require("./lib");
const { assert, sleep, submitBtn, BASE, createProject, deleteProject } = L;

// Multi-coder: every instance serves all installed coders (--provider is
// deprecated and ignored). The UI adapts: /coders/new grows a coder select
// with one agent select per coder (dc-coder-select toggles them), the Coder
// pages live at canonical /coders/<coder>/{instructions,agents,skills} URLs
// with a horizontal coder switcher ([data-coder-nav]) above the section tabs
// ([data-coder-sections]), and session rows show coder badges. The legacy
// top-level paths (/agents etc., coder via ?coder=) 308-redirect to the
// canonical URLs. MODE=single asserts the adaptive parts stay off; it only
// applies on a host where a single coder CLI is installed.
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
    await run("no coder switcher on agents/skills/instructions", async () => {
      for (const path of ["/agents", "/skills", "/instructions"]) {
        await page.goto(`${BASE}${path}`, { waitUntil: "domcontentloaded" });
        assert(/\/coders\/\w+/.test(page.url()), `${path} did not land on a canonical coder URL: ${page.url()}`);
        assert(!(await page.$("[data-coder-nav]")), `coder switcher rendered on ${path}`);
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

  await run("agents: coder switcher + canonical URLs scope list and create", async () => {
    const id = `tcagent-${tag.slice(-5)}`;
    await page.goto(`${BASE}/agents`, { waitUntil: "domcontentloaded" });
    assert(/\/coders\/\w+\/agents$/.test(page.url()), `legacy /agents did not land on a canonical URL: ${page.url()}`);
    const pills = await page.$$eval("[data-coder-nav] a", (as) => as.map((a) => new URL(a.href).pathname));
    assert(pills.length === 2, `expected 2 coder pills, got ${pills.length}`);
    assert(pills.includes("/coders/claude/agents") && pills.includes("/coders/copilot/agents"), `coder pills wrong: ${pills}`);
    assert(await page.$("[data-coder-nav] svg.coder-icon"), "no Claude icon in the coder switcher");
    const sections = await page.$$eval("[data-coder-sections] a", (as) => as.map((a) => new URL(a.href).pathname));
    assert(JSON.stringify(sections) === JSON.stringify(["/coders/copilot/instructions", "/coders/copilot/agents", "/coders/copilot/skills"]), `section tabs wrong: ${sections}`);
    await page.goto(`${BASE}/agents/new?coder=claude`, { waitUntil: "domcontentloaded" });
    assert(page.url().endsWith("/coders/claude/agents/new"), `legacy coder query did not map to canonical URL: ${page.url()}`);
    await page.fill('input[name="agent_id"]', id); await page.fill('input[name="agent_description"]', "throwaway"); await page.fill('textarea[name="agent_instructions"]', "test only");
    await Promise.all([page.waitForURL(/\/coders\/claude\/agents$/, { timeout: 10000 }), submitBtn(page, 'input[name="agent_id"]').click()]);
    assert(await page.evaluate((i) => document.body.innerHTML.includes(i), id), "agent not listed on the claude page");
    await page.goto(`${BASE}/coders/copilot/agents`, { waitUntil: "domcontentloaded" });
    assert(!(await page.$(`a[href*="${id}/edit"]`)), "claude agent leaked into the copilot page");
    await page.goto(`${BASE}/coders/claude/agents`, { waitUntil: "domcontentloaded" });
    const del = await page.$(`form[action="/coders/claude/agents/${id}/delete"]`);
    assert(del, "no coder-scoped delete form on the claude page");
    await (await del.$("button, input[type=submit]")).click(); await L.confirmSwal(page);
    await page.waitForFunction((i) => !document.querySelector(`form[action$="/agents/${i}/delete"]`), id, { timeout: 8000 });
  });

  await run("skills + instructions: switcher marks coder, tabs mark section, form posts canonical", async () => {
    for (const path of ["/skills", "/instructions"]) {
      await page.goto(`${BASE}${path}?coder=copilot`, { waitUntil: "domcontentloaded" });
      assert(page.url().endsWith(`/coders/copilot${path}`), `legacy ${path}?coder=copilot did not land on canonical URL: ${page.url()}`);
      const pill = await page.$eval("[data-coder-nav] a.active", (a) => new URL(a.href).pathname);
      assert(pill.startsWith("/coders/copilot/"), `${path}: copilot pill not active (${pill})`);
      const section = await page.$eval("[data-coder-sections] a.active", (a) => new URL(a.href).pathname);
      assert(section === `/coders/copilot${path}`, `${path}: section tab not active (${section})`);
    }
    assert(await page.$('form[action="/coders/copilot/instructions"]'), "instructions form does not post to the canonical path");
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
