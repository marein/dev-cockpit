const L = require("./lib");
const { assert, sleep, submitBtn, BASE, createProject, deleteProject } = L;

// Multi-coder: every instance serves all installed coders (--provider is
// deprecated and ignored). The UI adapts: /coders/new grows a coder select
// with one agent select per coder (dc-coder-select toggles them), /agents
// /skills /instructions get coder tabs (?coder=), and session rows show coder
// badges. MODE=single asserts the adaptive parts stay off; it only applies on
// a host where a single coder CLI is installed.
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
    await run("no coder tabs on agents/skills/instructions", async () => {
      for (const path of ["/agents", "/skills", "/instructions"]) {
        await page.goto(`${BASE}${path}`, { waitUntil: "domcontentloaded" });
        assert(!(await page.$('.nav-pills a[href*="coder="]')), `coder tabs rendered on ${path}`);
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

  await run("agents: coder tabs scope list and create", async () => {
    const id = `tcagent-${tag.slice(-5)}`;
    await page.goto(`${BASE}/agents`, { waitUntil: "domcontentloaded" });
    const tabs = await page.$$eval('.nav-pills a[href*="coder="]', (as) => as.map((a) => a.href));
    assert(tabs.length === 2, `expected 2 coder tabs, got ${tabs.length}`);
    assert(await page.$('.nav-pills a[title="Claude"] svg.coder-icon'), "no Claude icon in tabs");
    await page.goto(`${BASE}/agents/new?coder=claude`, { waitUntil: "domcontentloaded" });
    assert((await page.$eval('input[name="coder"]', (i) => i.value)) === "claude", "form does not carry coder");
    await page.fill('input[name="agent_id"]', id); await page.fill('input[name="agent_description"]', "throwaway"); await page.fill('textarea[name="agent_instructions"]', "test only");
    await Promise.all([page.waitForURL(/\/agents\?coder=claude$/, { timeout: 10000 }), submitBtn(page, 'input[name="agent_id"]').click()]);
    assert(await page.evaluate((i) => document.body.innerHTML.includes(i), id), "agent not listed in claude tab");
    await page.goto(`${BASE}/agents?coder=copilot`, { waitUntil: "domcontentloaded" });
    assert(!(await page.$(`a[href*="${id}/edit"]`)), "claude agent leaked into copilot tab");
    await page.goto(`${BASE}/agents?coder=claude`, { waitUntil: "domcontentloaded" });
    const del = await page.$(`form[action="/agents/${id}/delete"]`);
    assert(del, "no delete form in claude tab");
    assert(await del.$('input[name="coder"][value="claude"]'), "delete form does not carry coder");
    await (await del.$("button, input[type=submit]")).click(); await L.confirmSwal(page);
    await page.waitForFunction((i) => !document.querySelector(`form[action="/agents/${i}/delete"]`), id, { timeout: 8000 });
  });

  await run("skills + instructions: coder tabs present, forms carry coder", async () => {
    for (const path of ["/skills", "/instructions"]) {
      await page.goto(`${BASE}${path}?coder=copilot`, { waitUntil: "domcontentloaded" });
      const active = await page.$eval(".nav-pills a.active", (a) => a.href);
      assert(active.includes("coder=copilot"), `${path}: copilot tab not active`);
    }
    assert((await page.$eval('input[name="coder"]', (i) => i.value)) === "copilot", "instructions form does not carry coder");
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
        const scope = document.getElementById(`project-${p}-coders`);
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
