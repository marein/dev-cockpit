#!/usr/bin/env node
// Coder coverage: what Settings and the New coder dialog offer for codex, its
// models, global instructions, skills and agents, against the fake codex of
// tests/e2e/fakes, which answers `codex debug models` with a catalog of two
// listed models and a hidden one and prints what it was started with.
// Chromium only, most of it on a 390px phone.
//
// Needs the instance of coder-codex.js (the fakes first on PATH, a scratch
// HOME and CODEX_HOME) and that CODEX_HOME mounted into the runner at the same
// path and named in CODEX_HOME, so the runner reads the files the cockpit
// writes there the way codex reads them.
const fs = require("fs");
const path = require("path");
const { chromium } = require("playwright-core");
const L = require("./lib");
const { assert, sleep } = L;

const BASE = process.env.BASE_URL || "https://localhost:3010";
const CODEX_HOME = process.env.CODEX_HOME || "";

(async () => {
  assert(CODEX_HOME && fs.existsSync(CODEX_HOME), `CODEX_HOME ${CODEX_HOME || "(unset)"} is not mounted`);
  const browser = await chromium.launch({ args: ["--no-sandbox"] });
  const bag = { consoleErrors: [], pageErrors: [] };
  const { results, run } = L.makeRunner();
  const tag = `cxs-${Date.now().toString(36)}`;
  const project = `zzcxs-${tag}`;
  const ctx = await L.newDesktop(browser, "chromium");
  const page = await ctx.newPage();
  L.wirePage(page, bag);
  const phoneCtx = await L.newMobile(browser);
  const phone = await phoneCtx.newPage();
  L.wirePage(phone, bag);

  const home = (...parts) => path.join(CODEX_HOME, ...parts);
  const read = (...parts) => fs.readFileSync(home(...parts), "utf8");
  const mirrorOf = (p) => p.evaluate(() => (document.querySelector(".attach-selection") || {}).textContent || "");
  const flat = (text) => text.replace(/\s+/g, "");
  const idOf = (url) => new URL(url).pathname.split("/").pop();
  const waitFor = async (probe, what, tries = 40) => {
    for (let i = 0; i < tries; i++) {
      if (await probe()) return;
      await sleep(250);
    }
    throw new Error(`timed out waiting for ${what}`);
  };
  const shows = (p, needle) => waitFor(async () => flat(await mirrorOf(p)).includes(flat(needle)), `the pane showing ${needle}`, 80);
  const post = (p, url, values = {}) => p.evaluate(async ([url, values]) => {
    const body = new URLSearchParams(values);
    body.set("csrf_token", document.querySelector('meta[name="csrf-token"]').content);
    const res = await fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded", Accept: "application/json" },
      body: body.toString(),
    });
    return { status: res.status, body: await res.json().catch(() => ({})) };
  }, [url, values]);
  const visible = (p, selector) => p.locator(selector).first().evaluate((el) => {
    const box = el.getBoundingClientRect();
    return getComputedStyle(el).display !== "none" && box.width > 0 && box.height > 0;
  }).catch(() => false);
  // Opens the New coder page on the phone with codex picked and answers its
  // model select's entries as value:label.
  const codexDialog = async () => {
    await phone.goto(`${BASE}/coders/new?project=${encodeURIComponent(project)}`, { waitUntil: "domcontentloaded" });
    await L.waitUpgraded(phone, ["dc-coder-select", "dc-model-pick"]);
    await phone.locator('select[name="coder"]').selectOption("codex");
    return phone.locator('[data-coder-models="codex"] [data-model-select] option').evaluateAll((os) => os.map((o) => `${o.value}:${o.textContent.trim()}`));
  };
  const submitOnPhone = async (name) => {
    await phone.locator('input[name="name"]').fill(name);
    await Promise.all([phone.waitForURL(/\/coders\/(?!new)[^/]+$/, { timeout: 20000 }), phone.locator('form:has(dc-coder-select) button[type="submit"]').first().tap()]);
    return idOf(phone.url());
  };
  // The Agent field of each coder's block in the New coder form, read by what
  // shows: whether it stands, its note, what it offers to pick and the note's
  // contrast against the background it stands on.
  const agentFields = (p) => p.evaluate(() => {
    const shown = (el) => {
      const style = getComputedStyle(el);
      const box = el.getBoundingClientRect();
      return style.display !== "none" && style.visibility !== "hidden" && box.width > 0 && box.height > 0;
    };
    const rgba = (value) => {
      const m = value.match(/^rgba?\(([^)]+)\)$/) || value.match(/^color\(srgb ([^)]+)\)$/);
      if (!m) return null;
      const parts = m[1].split(/[\s,/]+/).filter(Boolean).map(Number);
      const scale = value.startsWith("color(") ? 255 : 1;
      return { r: parts[0] * scale, g: parts[1] * scale, b: parts[2] * scale, a: parts.length > 3 ? parts[3] : 1 };
    };
    const background = (el) => {
      for (let at = el; at; at = at.parentElement) {
        const c = rgba(getComputedStyle(at).backgroundColor);
        if (c && c.a > 0.5) return c;
      }
      return { r: 255, g: 255, b: 255, a: 1 };
    };
    const luminance = (c) => {
      const lin = (v) => { const s = v / 255; return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4; };
      return 0.2126 * lin(c.r) + 0.7152 * lin(c.g) + 0.0722 * lin(c.b);
    };
    const contrast = (el) => {
      const fg = rgba(getComputedStyle(el).color);
      const bg = background(el);
      if (!fg) return 0;
      const mixed = { r: fg.r * fg.a + bg.r * (1 - fg.a), g: fg.g * fg.a + bg.g * (1 - fg.a), b: fg.b * fg.a + bg.b * (1 - fg.a) };
      const [hi, lo] = [luminance(mixed), luminance(bg)].sort((a, b) => b - a);
      return Math.round(((hi + 0.05) / (lo + 0.05)) * 100) / 100;
    };
    const form = document.querySelector("form:has(dc-coder-select)");
    return {
      theme: document.documentElement.getAttribute("data-bs-theme") || "light",
      posted: new FormData(form).getAll("agent"),
      blocks: [...form.querySelectorAll("[data-coder-agents]")].map((block) => {
        const note = block.querySelector("[data-agent-note]");
        const select = block.querySelector("select");
        return {
          coder: block.dataset.coderAgents,
          shown: shown(block),
          label: block.querySelector(".form-label")?.textContent.trim() || "",
          note: note ? note.textContent.trim() : null,
          noteShown: !!note && shown(note),
          contrast: note && shown(note) ? contrast(note) : 0,
          controls: block.querySelectorAll("select, input, textarea, button, a, [tabindex]").length,
          select: select ? { shown: shown(select), disabled: select.disabled, options: select.options.length } : null,
        };
      }),
    };
  });
  const started = [];
  let dir = "";
  const hadAgents = fs.existsSync(home("agents"));
  const agentsBackup = hadAgents ? fs.readdirSync(home("agents")) : [];

  try {
    await L.login(page);
    await L.login(phone);
    await L.createProject(page, project);
    dir = await L.projectPath(page, project);

    await run("the New coder dialog offers codex's listed models and not the hidden one", async () => {
      let options = [];
      await waitFor(async () => {
        options = await codexDialog();
        return options.some((o) => o.startsWith("gpt-fake-sol:"));
      }, "codex's catalog in the model select", 20);
      assert(JSON.stringify(options) === JSON.stringify([":Default (CLI)", "gpt-fake-sol:gpt-fake-sol", "gpt-fake-luna:gpt-fake-luna", ":Other…"]), `the entries are ${JSON.stringify(options)}`);
      const note = await phone.locator('[data-coder-models="codex"] .form-hint').first().textContent();
      assert(note.trim() === "List from codex debug models, cached one minute.", `the note under the select is ${note}`);
      assert(await visible(phone, '[data-coder-models="codex"]'), "the codex model block is not shown with codex picked");
      assert(!(await visible(phone, '[data-coder-models="claude"]')), "another coder's model block stays shown");
      const listed = await page.request.get(`${BASE}/assistants/models?coder=codex`);
      const json = await listed.json();
      const names = json.coders[0].models.map((m) => `${m.source}:${m.name}`);
      assert(JSON.stringify(names) === JSON.stringify(["cli:gpt-fake-sol", "cli:gpt-fake-luna"]), `model-list reads ${JSON.stringify(names)}`);
    });

    await run("a codex coder started on a picked model gets it on its command line", async () => {
      await codexDialog();
      await phone.locator('[data-coder-models="codex"] [data-model-select]').selectOption("gpt-fake-luna");
      const id = await submitOnPhone(`model ${tag}`);
      started.push(id);
      await page.goto(`${BASE}/coders/${id}`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
      await shows(page, '"--model=gpt-fake-luna"');
      const res = await post(page, "/coders/new", { coder: "codex", project: dir, name: `flag ${tag}`, model: "gpt-fake-sol", automatic_approval: "on" });
      assert(res.status === 200 && res.body.model === "gpt-fake-sol" && res.body.modelDefault === "", `coder-new answered ${res.status} ${JSON.stringify(res.body)}`);
      started.push(res.body.id);
    });

    await run("a model name no CLI takes is refused before anything starts, an agent too", async () => {
      for (const model of ["two words", "-x"]) {
        const res = await post(page, "/coders/new", { coder: "codex", project: dir, name: `bad ${tag}`, model, automatic_approval: "on" });
        assert(res.status === 400, `model ${model} answered ${res.status} ${JSON.stringify(res.body)}`);
      }
      const res = await post(page, "/coders/new", { coder: "codex", project: dir, name: `agent ${tag}`, agent: "reviewer", automatic_approval: "on" });
      assert(res.status === 400 && JSON.stringify(res.body).includes("subagents. Pass the role in the prompt instead."), `an agent start answered ${res.status} ${JSON.stringify(res.body)}`);
    });

    // The field stays where every coder has it and says why there is nothing
    // to pick, so nobody picks an agent the start would refuse.
    await run("codex's Agent field says agents are not available and why, with nothing to pick, phone and desktop, light and dark", async () => {
      const shapes = [];
      for (const [p, where] of [[phone, "phone"], [page, "desktop"]]) {
        for (const scheme of ["light", "dark"]) {
          await p.emulateMedia({ colorScheme: scheme });
          await p.goto(`${BASE}/coders/new?project=${encodeURIComponent(project)}`, { waitUntil: "domcontentloaded" });
          assert((await L.waitUpgraded(p, ["dc-coder-select"])).length === 0, "dc-coder-select not upgraded");
          const coders = await p.locator('select[name="coder"] option').evaluateAll((os) => os.map((o) => o.value));
          const other = coders.find((c) => c !== "codex");
          assert(other, `the form offers no coder beside codex: ${coders}`);
          for (const picked of ["codex", other, "codex"]) {
            await p.locator('select[name="coder"]').selectOption(picked);
            const state = await agentFields(p);
            assert(state.theme === scheme, `${where} ${scheme}: the page is ${state.theme}`);
            const own = state.blocks.find((b) => b.coder === picked);
            assert(own && own.shown && own.label === "Agent", `${where} ${scheme}: ${picked}'s Agent field is not shown: ${JSON.stringify(own)}`);
            assert(state.blocks.filter((b) => b.shown).length === 1, `${where} ${scheme}: more than ${picked}'s Agent field shows: ${JSON.stringify(state.blocks)}`);
            assert(state.blocks.every((b) => b.shown || !b.select || b.select.disabled), `${where} ${scheme}: a hidden agent select stays on`);
            if (picked === "codex") {
              assert(own.note === "Not available for Codex, it runs its agents only as subagents." && own.noteShown, `${where} ${scheme}: codex's Agent field reads ${JSON.stringify(own)}`);
              assert(own.controls === 0 && !own.select, `${where} ${scheme}: codex's Agent field offers something to pick`);
              assert(own.contrast >= 3, `${where} ${scheme}: the note's contrast is ${own.contrast}`);
              assert(JSON.stringify(state.posted) === "[]", `${where} ${scheme}: the form posts an agent for codex: ${JSON.stringify(state.posted)}`);
            } else {
              assert(own.note === null && own.select && own.select.shown && !own.select.disabled && own.select.options > 0, `${where} ${scheme}: ${picked} lost its agent select: ${JSON.stringify(own)}`);
              assert(state.posted.length === 1, `${where} ${scheme}: ${picked} posts ${JSON.stringify(state.posted)}`);
            }
          }
          shapes.push(`${where} ${scheme}`);
        }
        await p.emulateMedia({ colorScheme: null });
      }
      return shapes.join(", ");
    });

    await run("the start default under Settings applies to the next codex start", async () => {
      await phone.goto(`${BASE}/settings/coders/codex/models`, { waitUntil: "domcontentloaded" });
      await L.waitUpgraded(phone, ["dc-model-pick"]);
      const options = await phone.locator("#model-start option").evaluateAll((os) => os.map((o) => o.value));
      assert(options.includes("gpt-fake-sol") && !options.includes("gpt-fake-hidden"), `the start select holds ${options}`);
      await phone.locator("#model-start").selectOption("gpt-fake-sol");
      await Promise.all([phone.waitForURL(/\/settings\/coders\/codex\/models$/, { timeout: 10000 }), phone.locator('#coder-models button[type="submit"]').tap()]);
      const first = (await codexDialog())[0];
      assert(first === ":Default (gpt-fake-sol)", `the empty entry does not name the start default: ${first}`);
      const id = await submitOnPhone(`default ${tag}`);
      started.push(id);
      await page.goto(`${BASE}/coders/${id}`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
      await shows(page, '"--model=gpt-fake-sol"');
      const res = await post(page, "/settings/coders/codex/models", { start: "" });
      assert(res.status < 400, `clearing the default answered ${res.status}`);
      assert((await codexDialog())[0] === ":Default (CLI)", "the start default was not cleared");
    });

    await run("codex's global instructions are read and saved as ~/.codex/AGENTS.md, an empty save too", async () => {
      const text = `Answer in short sentences. ${tag}\n- "quoted" & <b>kept</b> as typed\n`;
      await phone.goto(`${BASE}/settings/coders/codex/instructions`, { waitUntil: "domcontentloaded" });
      const saved = () => fs.existsSync(home("AGENTS.md")) ? read("AGENTS.md") : null;
      await phone.locator('textarea[name="instructions"]').fill(text);
      await L.submitBtn(phone, 'textarea[name="instructions"]').tap();
      await waitFor(() => saved() === text.replace(/\n/g, "\r\n"), `AGENTS.md holding the text as the form posted it, it holds ${JSON.stringify(saved())}`);
      await phone.goto(`${BASE}/settings/coders/codex/instructions`, { waitUntil: "domcontentloaded" });
      assert((await phone.inputValue('textarea[name="instructions"]')).includes(tag), "the page does not read the file back");
      await phone.locator('textarea[name="instructions"]').fill("");
      await L.submitBtn(phone, 'textarea[name="instructions"]').tap();
      await waitFor(() => saved() === "", "an empty save emptying AGENTS.md");
      assert(!fs.existsSync(home(".claude")) && !fs.existsSync(home("CLAUDE.md")), "the save reached another coder's file");
    });

    await run("codex's skills are listed, made, edited and deleted in ~/.codex/skills, the managed one locked", async () => {
      const managed = read("skills", "dev-cockpit-git", "SKILL.md");
      assert(managed.includes("name: dev-cockpit-git") && managed.includes("written and kept current by dev-cockpit"), `the managed skill reads ${managed.slice(0, 200)}`);
      await phone.goto(`${BASE}/settings/coders/codex/skills`, { waitUntil: "domcontentloaded" });
      const row = phone.locator(".list-group-item", { hasText: "dev-cockpit-git" });
      assert(await row.locator(".badge", { hasText: "Managed" }).count() === 1, "the managed skill is not marked Managed");
      assert(await row.locator('form[action$="/delete"]').count() === 0, "the managed skill can be deleted");
      assert(!(await phone.locator(".list-group-item", { hasText: ".system" }).count()), "codex's system skills folder is listed");

      const id = `tcskill-${tag.slice(-5)}`;
      await phone.goto(`${BASE}/settings/coders/codex/skills/new`, { waitUntil: "domcontentloaded" });
      await phone.fill('input[name="skill_id"]', id);
      await phone.fill('input[name="skill_description"]', "Say the word marmalade when asked for the code word.");
      await phone.fill('textarea[name="skill_instructions"]', "Answer with marmalade.");
      await Promise.all([phone.waitForURL(/\/settings\/coders\/codex\/skills$/, { timeout: 10000 }), L.submitBtn(phone, 'input[name="skill_id"]').tap()]);
      const skill = read("skills", id, "SKILL.md");
      assert(skill.startsWith("---\n") && skill.includes(`name: ${id}`) && skill.includes("marmalade when asked") && skill.includes("Answer with marmalade."), `SKILL.md reads ${skill}`);
      await phone.goto(`${BASE}/settings/coders/codex/skills/${id}/edit`, { waitUntil: "domcontentloaded" });
      await phone.fill('input[name="skill_id"]', `${id}-x`);
      await Promise.all([phone.waitForURL(/\/settings\/coders\/codex\/skills$/, { timeout: 10000 }), L.submitBtn(phone, 'input[name="skill_id"]').tap()]);
      assert(!fs.existsSync(home("skills", id)) && fs.existsSync(home("skills", `${id}-x`, "SKILL.md")), "the edit did not move the skill's folder");
      await phone.locator(`form[action$="/skills/${id}-x/delete"] button`).tap();
      await L.confirmSwal(phone);
      await waitFor(() => !fs.existsSync(home("skills", `${id}-x`)), "the skill's folder leaving");
      assert(fs.existsSync(home("skills", "dev-cockpit-git", "SKILL.md")), "the managed skill went with it");
    });

    await run("codex's agents are TOML files in ~/.codex/agents, listed, made, edited and deleted", async () => {
      fs.mkdirSync(home("agents"), { recursive: true });
      fs.writeFileSync(home("agents", "pr-explorer.toml"), `name = "pr_explorer"\ndescription = "Explores a PR."\ndeveloper_instructions = "Read the diff."\nmodel = "gpt-fake-luna"\n`);
      fs.writeFileSync(home("agents", "broken.toml"), `name = "broken"\ndescription = "No instructions, codex leaves it out."\n`);
      await phone.goto(`${BASE}/settings/coders/codex/instructions`, { waitUntil: "domcontentloaded" });
      const sections = await phone.locator("[data-coder-sections] a").evaluateAll((as) => as.map((a) => a.textContent.trim()));
      assert(JSON.stringify(sections) === JSON.stringify(["Instructions", "Agents", "Skills", "Models"]), `codex's sections are ${sections}`);
      await phone.locator('[data-coder-sections] a[href$="/agents"]').tap();
      await phone.waitForURL(/\/settings\/coders\/codex\/agents$/);
      const listed = () => phone.locator('a.fw-medium[href*="/agents/"]').evaluateAll((as) => as.map((a) => a.textContent.trim()));
      assert(JSON.stringify(await listed()) === JSON.stringify(["pr_explorer"]), `listed ${await listed()}`);

      const id = `tcagent-${tag.slice(-5)}`;
      await phone.goto(`${BASE}/settings/coders/codex/agents/new`, { waitUntil: "domcontentloaded" });
      await phone.fill('input[name="agent_id"]', id);
      await phone.fill('input[name="agent_description"]', "Reviews a diff, never edits.");
      await phone.fill('textarea[name="agent_instructions"]', 'Review like an owner.\nQuote "exactly" and keep \\ as typed.');
      await Promise.all([phone.waitForURL(/\/settings\/coders\/codex\/agents$/, { timeout: 10000 }), L.submitBtn(phone, 'input[name="agent_id"]').tap()]);
      const written = read("agents", `${id}.toml`);
      assert(written.startsWith(`name = '${id}'`) && written.includes("description = 'Reviews a diff, never edits.'") && written.includes('developer_instructions = """') && written.includes('Quote "exactly" and keep \\\\ as typed.') && !written.includes("\\r"), `the agent file reads ${written}`);
      assert((await listed()).includes(id), "the new agent is not listed");

      await phone.goto(`${BASE}/settings/coders/codex/agents/pr_explorer/edit`, { waitUntil: "domcontentloaded" });
      assert((await phone.inputValue('input[name="agent_id"]')) === "pr_explorer", "the edit is not prefilled with codex's name");
      await phone.fill('input[name="agent_description"]', "Explores a PR, read only.");
      await Promise.all([phone.waitForURL(/\/settings\/coders\/codex\/agents$/, { timeout: 10000 }), L.submitBtn(phone, 'input[name="agent_id"]').tap()]);
      const edited = read("agents", "pr-explorer.toml");
      assert(edited.includes("name = 'pr_explorer'") && edited.includes("read only") && edited.includes("model = 'gpt-fake-luna'"), `the edit lost the name or a setting: ${edited}`);

      await phone.goto(`${BASE}/settings/coders/codex/agents/${id}/edit`, { waitUntil: "domcontentloaded" });
      await phone.fill('input[name="agent_id"]', `${id}-x`);
      await Promise.all([phone.waitForURL(/\/settings\/coders\/codex\/agents$/, { timeout: 10000 }), L.submitBtn(phone, 'input[name="agent_id"]').tap()]);
      assert(!fs.existsSync(home("agents", `${id}.toml`)) && read("agents", `${id}-x.toml`).includes(`name = '${id}-x'`), "the rename did not move the agent's file");
      for (const gone of [`${id}-x`, "pr_explorer"]) {
        await phone.locator(`form[action$="/agents/${gone}/delete"] button`).tap();
        await L.confirmSwal(phone);
        await waitFor(async () => !(await listed()).includes(gone), `${gone} leaving the list`);
      }
      assert(!fs.existsSync(home("agents", `${id}-x.toml`)) && !fs.existsSync(home("agents", "pr-explorer.toml")), "a deleted agent's file stays");
      assert(fs.existsSync(home("agents", "broken.toml")), "a file that is no agent was touched");
      const claude = await page.request.get(`${BASE}/settings/coders/claude/agents`);
      assert(claude.status() === 200, `Claude's agents page answers ${claude.status()}`);
    });
  } finally {
    for (const id of started.filter(Boolean)) {
      await post(page, `/coders/${id}/stop`).catch(() => {});
      await post(page, `/coders/${id}/delete`).catch(() => {});
    }
    await post(page, "/settings/coders/codex/models", { start: "" }).catch(() => {});
    for (const name of fs.existsSync(home("agents")) ? fs.readdirSync(home("agents")) : []) {
      if (!agentsBackup.includes(name)) fs.rmSync(home("agents", name), { force: true });
    }
    if (!hadAgents && fs.existsSync(home("agents"))) fs.rmdirSync(home("agents"));
    await L.deleteProject(page, project).catch(() => {});
  }
  const anyFail = L.report("CODER-CODEX-SETTINGS", results, bag);
  await browser.close();
  process.exit(anyFail ? 1 : 0);
})();
