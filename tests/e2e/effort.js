const L = require("./lib");
const { assert, sleep, BASE } = L;

// Effort: the level picked beside every model. The new coder dialog, a
// coder's Models section and the assistant Models tab, the ring button's menu
// and the trigger form each carry an effort select under their model select,
// over the levels the coder takes; empty is the CLI's own default, and the
// chain behind an empty pick is the model's. Routes: GET/POST /coders/new,
// GET/POST /settings/coders/<coder>/models, GET/POST /settings/assistant/models,
// POST /assistants/:id (form=model), GET/POST /assistants/triggers. Custom
// elements dc-coder-select, dc-model-pick and dc-assistant-picks, the ring's
// own element.
//
// Needs an instance like assistant.js's: tests/e2e/fakes first on PATH, a
// scratch HOME. Both fakes take the effort flag, print `effort: <level>` in a
// terminal they start in and answer an `EFFORT` prompt with the level the
// turn was started at; the copilot fake refuses `max` on gpt-5-mini with the
// line copilot 1.0.98 prints. OPENCODE=1 adds the checks against the real
// opencode CLI, which then has to be on PATH behind the fakes, with
// XDG_CONFIG_HOME, XDG_DATA_HOME and XDG_STATE_HOME on scratch directories so
// nothing lands in the real opencode store, and reach the free zen models
// (opencode/beam-free takes the variants low to max, opencode/big-pickle none).

const OPENCODE = process.env.OPENCODE === "1";
const READY = "dc-assistant[ready]";
const MODEL_MENU = 'dc-assistant [data-bs-toggle="dropdown"][data-assistant-new-label]';
const MENU = "dc-assistant [data-assistant-new-label] + .dropdown-menu";
const CLAUDE_LEVELS = ["low", "medium", "high", "xhigh", "max"];
const COPILOT_LEVELS = ["none", "minimal", "low", "medium", "high", "xhigh", "max"];

async function call(page, method, path, fields) {
  return page.evaluate(async ([verb, url, values]) => {
    const body = new URLSearchParams(values || {});
    body.set("csrf_token", document.querySelector('meta[name="csrf-token"]').content);
    const res = await fetch(url, {
      method: verb,
      headers: { "Content-Type": "application/x-www-form-urlencoded", Accept: "application/json", "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]').content },
      body: verb === "GET" ? undefined : body.toString(),
    });
    const text = await res.text();
    let json = {};
    try { json = JSON.parse(text); } catch { json = { text }; }
    return { status: res.status, body: json };
  }, [method, path, fields]);
}

async function newAssistant(page, coder) {
  const made = await call(page, "POST", "/assistants/new", { form: "new", coder });
  assert(made.status === 200 && made.body.id, `no ${coder} assistant was made: ${JSON.stringify(made)}`);
  return made.body.id;
}

async function openAssistant(page, id) {
  await page.goto(`${BASE}/assistants/${id}`, { waitUntil: "domcontentloaded" });
  await L.dismissUpdate(page);
  await page.waitForSelector(READY, { timeout: 15000 });
  assert((await L.waitUpgraded(page, ["dc-assistant-picks", "dc-model-pick"], 8000)).length === 0, "the ring's elements did not upgrade");
}

async function openMenu(page) {
  if (await page.locator(`${MENU}.show`).count()) return;
  await page.click(MODEL_MENU);
  await page.waitForSelector(`${MENU}.show`, { timeout: 8000 });
}

async function closeMenu(page) {
  if (!(await page.locator(`${MENU}.show`).count())) return;
  await page.focus(`${MENU} [data-assistant-effort-pick="chat"]`);
  await page.keyboard.press("Escape");
  await page.waitForSelector(`${MENU}.show`, { state: "detached", timeout: 8000 });
}

async function idle(page) {
  await page.waitForFunction(() => !document.querySelector("dc-assistant")?.hasAttribute("running"), null, { timeout: 90000 });
}

// send puts one prompt in and answers the assistant message it bought once it
// settled, read off the page the server renders, so a failed turn's sentence
// is read where the user reads it.
async function send(page, id, text) {
  await idle(page);
  const before = await page.locator('[data-role="assistant"]').count();
  await page.fill("[data-assistant-input]", text);
  await page.click("[data-assistant-send]");
  await page.waitForFunction((count) => document.querySelectorAll('[data-role="assistant"]').length > count, before, { timeout: 20000 });
  await idle(page);
  await openAssistant(page, id);
  return page.evaluate(() => {
    const nodes = [...document.querySelectorAll('[data-role="assistant"]')];
    const last = nodes[nodes.length - 1];
    return {
      state: last.getAttribute("data-state"),
      text: last.querySelector("[data-assistant-text], .dc-msg-body, .markdown")?.textContent.trim() || last.textContent.trim(),
      error: last.querySelector("[data-assistant-error]")?.textContent.trim() || "",
      model: last.querySelector("[data-assistant-model]")?.textContent.trim() || "",
    };
  });
}

function visible(el) {
  if (!el) return false;
  const box = el.getBoundingClientRect();
  return box.width > 0 && box.height > 0 && getComputedStyle(el).visibility !== "hidden";
}

L.runFeature("EFFORT", async ({ page, run }) => {
  const tag = `eff-${Date.now().toString(36)}`;
  const project = `zztc-${tag}`;
  const assistants = [];
  const coders = [];
  let projectDir = "";
  const dropCoder = async (id) => {
    await call(page, "POST", `/coders/${id}/stop`);
    await call(page, "POST", `/coders/${id}/delete`);
  };
  const dialog = async () => {
    await page.goto(`${BASE}/coders/new?project=${encodeURIComponent(projectDir)}`, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    assert((await L.waitUpgraded(page, ["dc-coder-select", "dc-model-pick"], 8000)).length === 0, "the dialog's elements did not upgrade");
  };
  const readBlocks = () => page.evaluate(() => [...document.querySelectorAll("[data-coder-models]")].map((block) => {
    const effort = block.querySelector('select[name="effort"]');
    const box = (el) => { const r = el.getBoundingClientRect(); return r.width > 0 && r.height > 0; };
    return {
      coder: block.dataset.coderModels,
      shown: box(block) && box(effort),
      disabled: effort.disabled,
      label: effort.closest(".input-group")?.querySelector("label")?.textContent.trim() || "",
      afterModel: !!(block.querySelector("[data-model-select]").compareDocumentPosition(effort) & Node.DOCUMENT_POSITION_FOLLOWING),
      options: [...effort.options].map((o) => o.textContent.trim()),
    };
  }));

  try {
    await L.createProject(page, project);
    projectDir = await L.projectPath(page, project);

    await run("dialog: the effort select stands under the model for the picked coder, over that coder's levels", async () => {
      await dialog();
      const coderSelect = page.locator('select[name="coder"]');
      const offered = await coderSelect.count() ? await coderSelect.evaluate((s) => [...s.options].map((o) => o.value)) : [await page.inputValue('input[name="coder"]')];
      assert(offered.includes("claude"), `claude is not offered: ${offered}`);
      if (await coderSelect.count()) await page.selectOption('select[name="coder"]', "claude");
      await sleep(150);
      let blocks = await readBlocks();
      const claude = blocks.find((b) => b.coder === "claude");
      assert(claude.shown && !claude.disabled && claude.label === "Effort" && claude.afterModel, `claude's effort select is not live under its model: ${JSON.stringify(claude)}`);
      assert(JSON.stringify(claude.options) === JSON.stringify(["Default (CLI)", ...CLAUDE_LEVELS]), `claude's levels are off: ${JSON.stringify(claude.options)}`);
      for (const other of blocks.filter((b) => b.coder !== "claude")) {
        assert(!other.shown && other.disabled, `another coder's effort select is live: ${JSON.stringify(other)}`);
      }
      if (!offered.includes("copilot")) return "claude only";
      await page.selectOption('select[name="coder"]', "copilot");
      await sleep(150);
      blocks = await readBlocks();
      const copilot = blocks.find((b) => b.coder === "copilot");
      assert(copilot.shown && !copilot.disabled && JSON.stringify(copilot.options) === JSON.stringify(["Default (CLI)", ...COPILOT_LEVELS]), `copilot's effort select is off: ${JSON.stringify(copilot)}`);
      assert(blocks.filter((b) => b.coder !== "copilot").every((b) => !b.shown && b.disabled), `the switch left another effort select live: ${JSON.stringify(blocks)}`);
      return `claude ${claude.options.length - 1} levels, copilot ${copilot.options.length - 1}`;
    });

    await run("dialog: a picked effort reaches the coder's command line", async () => {
      await dialog();
      if (await page.locator('select[name="coder"]').count()) await page.selectOption('select[name="coder"]', "claude");
      await page.fill('input[name="name"]', `tceff-${tag.slice(-5)}`);
      await page.selectOption('[data-coder-models="claude"] select[name="effort"]', "high");
      await Promise.all([
        page.waitForURL(/\/coders\/(?!new)[^/?]+$/, { timeout: 20000 }),
        page.locator('form:has(select[name="agent"]) button[type="submit"]').first().click(),
      ]);
      const id = new URL(page.url()).pathname.split("/").pop();
      coders.push(id);
      let text = "";
      for (let i = 0; i < 30 && !text.includes("effort: high"); i++) {
        await sleep(300);
        text = await page.evaluate(() => document.querySelector(".attach-selection")?.textContent || "");
      }
      assert(text.includes("effort: high"), `the terminal never showed the level: ${JSON.stringify(text.slice(0, 300))}`);
      return id;
    });

    await run("a resume passes no effort", async () => {
      const started = await call(page, "POST", "/coders/new", { name: `tcres-${tag.slice(-5)}`, project: projectDir, coder: "claude", effort: "max", prompt: "EFFORT resume" });
      assert(started.status === 200 && started.body.id && started.body.effort === "max", `the start did not take the level: ${JSON.stringify(started)}`);
      const id = started.body.id;
      coders.push(id);
      await page.goto(`${BASE}/coders/${id}`, { waitUntil: "domcontentloaded" });
      let first = "";
      for (let i = 0; i < 30 && !first.includes("effort: max"); i++) {
        await sleep(300);
        first = await page.evaluate(() => document.querySelector(".attach-selection")?.textContent || "");
      }
      assert(first.includes("effort: max"), `the start never showed its level: ${JSON.stringify(first.slice(0, 300))}`);
      await call(page, "POST", `/coders/${id}/stop`);
      await sleep(800);
      const resumed = await call(page, "POST", `/coders/${id}/resume`);
      assert(resumed.status === 200, `the resume answered ${resumed.status}: ${JSON.stringify(resumed.body)}`);
      await page.goto(`${BASE}/coders/${id}`, { waitUntil: "domcontentloaded" });
      let text = "";
      for (let i = 0; i < 30 && !text.includes("resumed"); i++) {
        await sleep(300);
        text = await page.evaluate(() => document.querySelector(".attach-selection")?.textContent || "");
      }
      assert(text.includes("resumed") && !text.includes("effort:"), `the resume carried a level: ${JSON.stringify(text.slice(0, 300))}`);
      return "resumed without --effort";
    });

    await run("a level the coder does not take is refused before anything starts", async () => {
      const refused = await call(page, "POST", "/coders/new", { name: `tcno-${tag.slice(-5)}`, project: projectDir, coder: "claude", effort: "none" });
      assert(refused.status === 400 && refused.body.error === "The effort is one of low, medium, high, xhigh, max.", `the level was not refused with claude's levels: ${JSON.stringify(refused)}`);
      return refused.body.error;
    });

    await run("coder settings: the start effort round trips and the dialog's empty entry names it", async () => {
      const store = async (level) => {
        await page.goto(`${BASE}/settings/coders/claude/models`, { waitUntil: "domcontentloaded" });
        await L.dismissUpdate(page);
        await page.selectOption('select[name="start_effort"]', level);
        await page.click('#coder-models button[type="submit"]');
        await page.waitForFunction(() => document.body.innerText.includes("Settings saved."), null, { timeout: 8000 });
        const kept = await page.$eval('select[name="start_effort"]', (s) => ({ value: s.value, first: s.options[0].textContent.trim() }));
        assert(kept.value === level && kept.first === "Default (CLI)", `the start effort did not come back: ${JSON.stringify(kept)}`);
      };
      await store("low");
      try {
        await dialog();
        const first = await page.$eval('[data-coder-models="claude"] select[name="effort"]', (s) => s.options[0].textContent.trim());
        assert(first === "Default (low)", `the dialog's empty entry does not name the start effort: ${first}`);
      } finally {
        await store("");
      }
      return "Default (low) in the dialog, cleared again";
    });

    let ringID = "";
    await run("ring: an effort under every model, saved on change, and the chat turn runs at it", async () => {
      ringID = await newAssistant(page, "claude");
      assistants.push(ringID);
      await openAssistant(page, ringID);
      await openMenu(page);
      const shape = await page.evaluate((menuSel) => {
        const menu = document.querySelector(menuSel);
        const box = (el) => { const r = el.getBoundingClientRect(); return r.width > 0 && r.height > 0; };
        const read = (kind) => {
          const select = menu.querySelector(`[data-assistant-effort-pick="${kind}"]`);
          return { options: [...select.options].map((o) => o.textContent.trim()), value: select.value, shown: box(select), field: select.name };
        };
        return { chat: read("chat"), check: read("check"), trigger: read("trigger"), labels: [...menu.querySelectorAll("[data-assistant-models] label")].map((l) => l.textContent.trim()) };
      }, MENU);
      assert(shape.labels.join("|") === "Chat|Effort|Checks|Effort|Triggers|Effort", `the menu is not labelled as model and effort per purpose: ${JSON.stringify(shape.labels)}`);
      assert(JSON.stringify(shape.chat.options) === JSON.stringify(["Coder default (CLI)", ...CLAUDE_LEVELS]) && shape.chat.shown && shape.chat.value === "" && shape.chat.field === "effort",
        `the chat effort select is off: ${JSON.stringify(shape.chat)}`);
      for (const kind of ["check", "trigger"]) {
        assert(shape[kind].options[0] === "Same as chat" && shape[kind].shown && shape[kind].value === "", `the ${kind} effort select is off: ${JSON.stringify(shape[kind])}`);
      }
      await page.selectOption(`${MENU} [data-assistant-effort-pick="chat"]`, "high");
      await page.waitForSelector(".dc-toast:has-text(\"Chat, checks and triggers on the CLI's default at high effort.\")", { timeout: 8000 });
      assert(await page.locator(`${MENU}.show`).count(), "a pick closed the menu");
      await closeMenu(page);
      const answer = await send(page, ringID, "EFFORT please");
      assert(answer.state === "complete" && answer.text.includes("effort: high"), `the turn did not run at high: ${JSON.stringify(answer)}`);
      assert(answer.model === "high effort", `the answer does not say its effort: ${JSON.stringify(answer)}`);
      await openMenu(page);
      const kept = await page.$eval(`${MENU} [data-assistant-effort-pick="chat"]`, (s) => s.value);
      assert(kept === "high", `the pick did not survive the reload: ${kept}`);
      await closeMenu(page);
      return "chat at high, answered effort: high, labelled high effort";
    });

    await run("ring: an empty effort beside a model of its own is the CLI's level and says so", async () => {
      assert(ringID, "no assistant from the check before");
      await openAssistant(page, ringID);
      await openMenu(page);
      const label = () => page.$eval(`${MENU} [data-assistant-effort-pick="check"]`, (s) => s.options[0].textContent.trim());
      assert(await label() === "Same as chat", `the checks' empty effort does not follow the chat: ${await label()}`);
      await page.selectOption(`${MENU} [data-assistant-model-pick="check"] select`, "haiku");
      await page.waitForSelector(".dc-toast:has-text(\"Chat and triggers on the CLI's default at high effort, checks on haiku.\")", { timeout: 8000 });
      assert(await label() === "Default (CLI)", `the checks' empty effort beside their own model is not the CLI's: ${await label()}`);
      await page.selectOption(`${MENU} [data-assistant-model-pick="check"] select`, "");
      await page.waitForSelector(".dc-toast:has-text(\"Chat, checks and triggers on the CLI's default at high effort.\")", { timeout: 8000 });
      assert(await label() === "Same as chat", `the label did not follow the model back: ${await label()}`);
      await closeMenu(page);
      return "Same as chat, Default (CLI) beside haiku, Same as chat again";
    });

    await run("trigger form: the empty effort follows the model picked in the form", async () => {
      assert(ringID, "no assistant from the check before");
      await page.goto(`${BASE}/assistants/triggers?form=new&assistant=${ringID}`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      assert((await L.waitUpgraded(page, ["dc-effort-pick", "dc-model-pick"], 8000)).length === 0, "the form's elements did not upgrade");
      const label = () => page.$eval('select[name="effort"]', (s) => s.options[0].textContent.trim());
      assert(await label() === "Assistant default (high)", `the empty effort does not name the assistant default: ${await label()}`);
      await page.selectOption('[data-model-select][name="model"]', "haiku");
      assert(await label() === "Default (CLI)", `the empty effort beside the trigger's own model is not the CLI's: ${await label()}`);
      await page.selectOption('[data-model-select][name="model"]', "");
      assert(await label() === "Assistant default (high)", `the label did not follow the model back: ${await label()}`);
      return "Assistant default (high), Default (CLI) beside haiku";
    });

    await run("ring: an effort moved elsewhere lands in the open menu without a reload", async () => {
      assert(ringID, "no assistant from the check before");
      await openAssistant(page, ringID);
      const moved = await call(page, "POST", `/assistants/${ringID}`, { form: "model", check_effort: "low" });
      assert(moved.status === 200 && moved.body.checkEffort === "low", `the save from elsewhere failed: ${JSON.stringify(moved)}`);
      await openMenu(page);
      await page.waitForFunction((sel) => document.querySelector(sel)?.value === "low", `${MENU} [data-assistant-effort-pick="check"]`, { timeout: 8000 });
      await closeMenu(page);
      return moved.body.message;
    });

    await run("trigger form: the effort stands under the model, reads the assistant default, and a trigger keeps its own", async () => {
      assert(ringID, "no assistant from the check before");
      const form = await page.evaluate(async (id) => {
        const res = await fetch(`/assistants/triggers?form=new&assistant=${id}&modal=1`);
        const doc = new DOMParser().parseFromString(await res.text(), "text/html");
        const effort = doc.querySelector('select[name="effort"]');
        const model = doc.querySelector('select[name="model"]');
        return {
          options: effort ? [...effort.options].map((o) => o.textContent.trim()) : [],
          sameBlock: !!effort && effort.closest(".mb-2") === model.closest(".mb-2"),
        };
      }, ringID);
      assert(JSON.stringify(form.options) === JSON.stringify(["Assistant default (high)", ...CLAUDE_LEVELS]) && form.sameBlock, `the trigger form's effort select is off: ${JSON.stringify(form)}`);
      const made = await call(page, "POST", "/assistants/triggers", { form: "new", assistant: ringID, event: "cron", spec: "0 3 * * *", task: "EVENT_NOTHING at low", effort: "low" });
      assert(made.status === 200 && made.body.effort === "low" && made.body.effortDefault === "high", `the trigger did not keep its own effort: ${JSON.stringify(made)}`);
      const id = made.body.id;
      const row = await page.evaluate(async ([owner, trigger]) => {
        const res = await fetch(`/assistants/triggers?assistant=${owner}`);
        const doc = new DOMParser().parseFromString(await res.text(), "text/html");
        const facts = [...doc.querySelectorAll("[data-assistant-trigger-facts]")].find((f) => f.closest(`[data-trigger-id="${trigger}"], [data-assistant-trigger="${trigger}"]`)) || doc.querySelector("[data-assistant-trigger-facts]");
        return facts?.querySelector("[data-assistant-trigger-effort]")?.textContent.trim() || "";
      }, [ringID, id]);
      assert(row === "low", `the row does not say the effort: ${JSON.stringify(row)}`);
      const refused = await call(page, "POST", "/assistants/triggers", { form: "edit", id, effort: "none" });
      assert(refused.status >= 400 && /low, medium, high, xhigh, max/.test(refused.body.error || ""), `a level claude does not take was accepted: ${JSON.stringify(refused)}`);
      const cleared = await call(page, "POST", "/assistants/triggers", { form: "edit", id, effort: "" });
      assert(cleared.status === 200 && /the assistant's effort/.test(cleared.body.changed || ""), `the change does not name the effort: ${JSON.stringify(cleared.body)}`);
      await call(page, "POST", "/assistants/triggers", { form: "remove", id });
      return "Assistant default (high), low on the row, none refused, cleared";
    });

    await run("Models tab: the efforts are creation defaults of a new assistant", async () => {
      const store = async (level) => {
        await page.goto(`${BASE}/settings/assistant/models`, { waitUntil: "domcontentloaded" });
        await L.dismissUpdate(page);
        await page.selectOption('select[name="effort-chat-claude"]', level);
        await page.click('#settings-assistant-models button[type="submit"]');
        await page.waitForFunction(() => document.body.innerText.includes("Settings saved."), null, { timeout: 8000 });
        assert(await page.$eval('select[name="effort-chat-claude"]', (s) => s.value) === level, "the tab's effort did not come back");
      };
      await store("max");
      try {
        const id = await newAssistant(page, "claude");
        assistants.push(id);
        await openAssistant(page, id);
        await openMenu(page);
        const value = await page.$eval(`${MENU} [data-assistant-effort-pick="chat"]`, (s) => s.value);
        assert(value === "max", `the new assistant was not made at the tab's effort: ${value}`);
        await closeMenu(page);
      } finally {
        await store("");
      }
      return "a new assistant at max, the default cleared again";
    });

    await run("copilot refuses a level its model does not take, and the answer says where to pick another", async () => {
      const id = await newAssistant(page, "copilot").catch(() => "");
      if (!id) return "copilot is not installed here";
      assistants.push(id);
      await openAssistant(page, id);
      const set = await call(page, "POST", `/assistants/${id}`, { form: "model", model: "gpt-5-mini", effort: "max" });
      assert(set.status === 200, `the picks were not taken: ${JSON.stringify(set)}`);
      const answer = await send(page, id, "hello");
      assert(answer.state === "failed" && answer.error === "The coder does not take the effort max with the model gpt-5-mini. Pick another effort at the ring button of this assistant.",
        `the refusal did not reach the answer: ${JSON.stringify(answer)}`);
      return answer.error;
    });

    if (OPENCODE) {
      await run("opencode: a start refuses a level the model lacks", async () => {
        const refused = await call(page, "POST", "/coders/new", { name: `tcoc-${tag.slice(-5)}`, project: projectDir, coder: "opencode", model: "opencode/big-pickle", effort: "high" });
        assert(refused.status === 400 && refused.body.error === "opencode/big-pickle takes no effort level.", `the level was not refused: ${JSON.stringify(refused)}`);
        const bare = await call(page, "POST", "/coders/new", { name: `tcoc-${tag.slice(-5)}`, project: projectDir, coder: "opencode", effort: "high" });
        assert(bare.status === 400 && /only together with a model/.test(bare.body.error || ""), `an effort without a model was not refused: ${JSON.stringify(bare)}`);
        return refused.body.error;
      });

      await run("opencode: Settings refuses a start effort without a start model", async () => {
        await page.goto(`${BASE}/settings/coders/opencode/models`, { waitUntil: "domcontentloaded" });
        await L.dismissUpdate(page);
        const levels = await page.$$eval('select[name="start_effort"] option', (options) => options.map((o) => o.value));
        assert(levels.includes("high"), `opencode offers no high here: ${levels}`);
        await page.selectOption('select[name="start"]', "");
        await page.selectOption('select[name="start_effort"]', "high");
        await page.click('#coder-models button[type="submit"]');
        await page.waitForFunction(() => document.body.innerText.includes("only together with a model"), null, { timeout: 15000 });
        const kept = await page.$eval('select[name="start_effort"]', (s) => s.value);
        assert(kept === "", `the refused start effort was stored: ${kept}`);
        return "refused on the page that sets it";
      });

      await run("opencode: a start at an effort runs the terminal at that variant", async () => {
        await dialog();
        await page.selectOption('select[name="coder"]', "opencode");
        await sleep(150);
        const block = '[data-coder-models="opencode"]';
        const listed = await page.$$eval(`${block} [data-model-select] option`, (options) => options.map((o) => o.value));
        if (listed.includes("opencode/beam-free")) {
          await page.selectOption(`${block} [data-model-select]`, "opencode/beam-free");
        } else {
          await page.selectOption(`${block} [data-model-select]`, { label: "Other…" });
          await page.fill(`${block} [data-model-other-input]`, "opencode/beam-free");
        }
        const levels = await page.$$eval(`${block} select[name="effort"] option`, (options) => options.map((o) => o.value));
        assert(levels.includes("high"), `opencode offers no high: ${levels}`);
        await page.selectOption(`${block} select[name="effort"]`, "high");
        await page.fill('input[name="name"]', `tcocv-${tag.slice(-5)}`);
        // The terminal shows its level once a prompt ran at it, so the start
        // carries a task, the field the local API posts and the form leaves out.
        await page.evaluate(() => {
          const task = document.createElement("input");
          task.type = "hidden";
          task.name = "prompt";
          task.value = "Reply with the single word ok.";
          document.querySelector('form:has(select[name="agent"])').append(task);
        });
        // A first opencode in fresh directories takes up to a minute or more
        // to come up and answer, so the waits are long.
        await Promise.all([
          page.waitForURL(/\/coders\/(?!new)[^/?]+$/, { timeout: 120000 }),
          page.locator('form:has(select[name="agent"]) button[type="submit"]').first().click(),
        ]);
        const id = new URL(page.url()).pathname.split("/").pop();
        coders.push(id);
        let text = "";
        for (let i = 0; i < 360 && !/Beam Free.*· high/.test(text); i++) {
          await sleep(500);
          text = await page.evaluate(() => document.querySelector(".attach-selection")?.textContent || "");
        }
        assert(/Beam Free.*· high/.test(text), `the terminal does not run at high: ${JSON.stringify(text.slice(-400))}`);
        return "the footer reads Beam Free … · high";
      });

      let ocID = "";
      await run("opencode: an assistant turn runs at its variant", async () => {
        ocID = await newAssistant(page, "opencode");
        assistants.push(ocID);
        await openAssistant(page, ocID);
        const set = await call(page, "POST", `/assistants/${ocID}`, { form: "model", model: "opencode/beam-free", effort: "high" });
        assert(set.status === 200, `the picks were not taken: ${JSON.stringify(set)}`);
        const answer = await send(page, ocID, "Reply with the single word ok.");
        assert(answer.state === "complete" && /ok/i.test(answer.text), `the turn did not answer: ${JSON.stringify(answer)}`);
        assert(answer.model === "opencode/beam-free · high effort", `the answer does not say its model and effort: ${JSON.stringify(answer)}`);
        return answer.text;
      });

      await run("opencode: a trigger on a model of its own runs at its own level while the chat runs at high", async () => {
        assert(ocID, "no opencode assistant from the check before");
        const made = await call(page, "POST", "/assistants/triggers", { form: "new", assistant: ocID, event: "cron", spec: "* * * * *", once: "on", task: "Reply with the single word ok.", model: "opencode/big-pickle" });
        assert(made.status === 200 && made.body.effort === "" && made.body.effortDefault === "", `the trigger was not made at the CLI's level: ${JSON.stringify(made)}`);
        let reaction = null;
        for (let i = 0; i < 36 && !reaction; i++) {
          await sleep(5000);
          reaction = await page.evaluate(async (id) => {
            const res = await fetch(`/assistants/${id}`);
            const doc = new DOMParser().parseFromString(await res.text(), "text/html");
            const auto = [...doc.querySelectorAll("[data-assistant-auto]")].pop();
            if (!auto || auto.getAttribute("data-state") === "streaming") return null;
            return { state: auto.getAttribute("data-state"), error: auto.querySelector("[data-assistant-error]")?.textContent.trim() || "", model: auto.querySelector("[data-assistant-model]")?.textContent.trim() || "" };
          }, ocID);
        }
        await call(page, "POST", "/assistants/triggers", { form: "remove", id: made.body.id });
        assert(reaction && reaction.state === "complete" && reaction.model === "opencode/big-pickle", `the reaction did not run on its own model at its own level: ${JSON.stringify(reaction)}`);
        return "the reaction answered on opencode/big-pickle without the chat's high";
      });

      await run("opencode: the ring refuses a level its model lacks, and an effort without a model", async () => {
        assert(ocID, "no opencode assistant from the check before");
        await openAssistant(page, ocID);
        const lacks = await call(page, "POST", `/assistants/${ocID}`, { form: "model", model: "opencode/big-pickle", effort: "max" });
        assert(lacks.status === 400 && lacks.body.error === "Chat: opencode/big-pickle takes no effort level.", `the pair was not refused at the ring: ${JSON.stringify(lacks)}`);
        const bare = await call(page, "POST", `/assistants/${ocID}`, { form: "model", model: "", effort: "high" });
        assert(bare.status === 400 && bare.body.error === "Chat: OpenCode takes an effort only together with a model, its levels are the variants of one model.", `an effort without a model was not refused at the ring: ${JSON.stringify(bare)}`);
        const kept = await call(page, "POST", `/assistants/${ocID}`, { form: "model" });
        assert(kept.status === 200 && kept.body.model === "opencode/beam-free" && kept.body.effort === "high", `a refused save moved a pick: ${JSON.stringify(kept)}`);
        return "both refused where they are picked";
      });
    }
  } finally {
    for (const id of coders) await dropCoder(id).catch(() => {});
    for (const id of assistants) await call(page, "POST", `/assistants/${id}`, { form: "delete" }).catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
  }
});
