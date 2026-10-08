#!/usr/bin/env node
const fs = require("fs");
const path = require("path");
const crypto = require("crypto");
const L = require("./lib");
const { assert, sleep, BASE } = L;

// The cost page and the status line's cost item. The cockpit reads the calls
// claude logs in its transcripts under ~/.claude/projects every 10s, prices
// each at the list price and books it on its own timestamp; a cost-state line
// tops up what claude's own total holds above the calls (internal/cost,
// internal/cost/claude). This runner writes fake transcripts into a fake HOME
// of the instance, mounted at CLAUDE_DIR, and checks what the page, the
// status line and the phone's Cockpit sheet show from them.
//
// Instance: a throwaway with HOME pointing at a scratch directory whose
// .claude/projects is mounted into the runner, and tests/e2e/fakes first on
// its PATH, so the one coder the runner starts is the fake claude:
//   -e CLAUDE_DIR=/claude -v <home>/.claude/projects:/claude
//   -e HOST_STATE_DIR=<state-dir>
//   -e STATE_DIR=/state -v <state-dir>:/state:ro
// HOST_STATE_DIR is the instance's --state-dir as the host spells it, only
// read as text: it names the assistants' workspaces, where their chat turns
// leave their transcripts. STATE_DIR is the same directory mounted read
// only, where the checks read the books on disk.
//
// Gotchas:
// - the fixtures price output tokens only, at the embedded list prices:
//   opus 5.5 20, sonnet 5.5 10, haiku 4.5 5 USD per million. A refresh of the
//   prices from LiteLLM holds the same numbers for these models.
// - claude writes one line per content block of a call, the shop's first call
//   is written twice and must count once.
// - a poll runs every 10s, so a check waits up to 25s for a change to land.
// - the books in the cost folder outlive a run, so every run needs a fresh
//   state directory.
// - visibility is read from computed style and the box, never the attribute.
// - without an update stub the dialog is clicked away with the mouse, which then
//   rests over a column: its hover tip stays in the DOM, hidden, so a tip link
//   is tapped through :visible.

const CLAUDE_DIR = process.env.CLAUDE_DIR || "/claude";
const HOST_STATE_DIR = process.env.HOST_STATE_DIR || "";
const STATE_DIR = process.env.STATE_DIR || "";
const tag = crypto.randomBytes(3).toString("hex");
const SHOP = `tccost-shop-${tag}`;
const BLOG = `tccost-blog-${tag}`;
const A = crypto.randomUUID();
const B = crypto.randomUUID();
const D = crypto.randomUUID();
const E = crypto.randomUUID();
const CHECK = crypto.randomUUID();
const OLD = crypto.randomUUID();
const F = crypto.randomUUID();
const files = {};
const SHOP_TITLE = `cost shop ${tag}`;
const BLOG_TITLE = `cost blog ${tag}`;
const OPS = `cost ops ${tag}`;
const E_TITLE = `cost ollama ${tag} with a title long enough to wrap on a phone`;
const WRITER = `cost writer ${tag}`;
const PER_MILLION = { "claude-opus-5-5": 20, "claude-sonnet-5-5": 10, "claude-haiku-4-5": 5 };

// call is one transcript line of an API call that spent usd on output tokens.
function call(session, cwd, model, usd, at, id = crypto.randomBytes(8).toString("hex")) {
  const output = model in PER_MILLION ? Math.round(usd / PER_MILLION[model] * 1e6) : 1000;
  return JSON.stringify({
    type: "assistant", cwd, sessionId: session, timestamp: new Date(at).toISOString(),
    requestId: model in PER_MILLION ? `req_${id}` : null,
    message: { id: `msg_${id}`, model, role: "assistant", content: [{ type: "text", text: "ok" }],
      usage: { input_tokens: 0, output_tokens: output, cache_read_input_tokens: 0, cache_creation_input_tokens: 0,
        cache_creation: { ephemeral_5m_input_tokens: 0, ephemeral_1h_input_tokens: 0 }, service_tier: "standard", speed: "standard" } },
  }) + "\n";
}

function costLine(session, models) {
  const usage = Object.fromEntries(Object.entries(models).map(([m, v]) => [m, { inputTokens: 1, outputTokens: 1, costUSD: v }]));
  const total = Object.values(models).reduce((a, b) => a + b, 0);
  return JSON.stringify({ type: "cost-state", sessionId: session, totalCostUSD: total, totalDuration: 1, startTime: Date.now(), modelUsage: usage, hasUnknownModelCost: false }) + "\n";
}

function writeTranscript(session, cwd, title, lines) {
  const dir = path.join(CLAUDE_DIR, cwd.replace(/[^a-zA-Z0-9]/g, "-"));
  fs.mkdirSync(dir, { recursive: true });
  files[session] = path.join(dir, `${session}.jsonl`);
  const head = JSON.stringify({ type: "user", cwd, sessionId: session, timestamp: new Date().toISOString(), message: { role: "user", content: title } }) + "\n" +
    JSON.stringify({ type: "custom-title", customTitle: title, sessionId: session }) + "\n";
  fs.writeFileSync(files[session], head + lines);
}

const visible = (page, sel) => page.evaluate((s) => {
  const el = document.querySelector(s);
  if (!el) return false;
  const box = el.getBoundingClientRect();
  return getComputedStyle(el).display !== "none" && box.width > 0 && box.height > 0;
}, sel);

const tile = (page, label) => page.locator(`[data-cost-tile="${label}"]`).innerText();

// legendValue is a series' total for the period in one chart, empty where it
// has none.
const legendValue = (page, label, chart = "project") => page.evaluate(([l, c]) => {
  const key = [...document.querySelectorAll(`[data-cost-chart="${c}"] [data-cost-legend] [data-series]`)].find((k) => k.querySelector(".text-break").textContent.trim() === l);
  return key ? key.lastElementChild.textContent.trim() : "";
}, [label, chart]);
const legendLabels = (page, chart) => page.$$eval(`[data-cost-chart="${chart}"] [data-cost-legend] [data-series] .text-break`, (els) => els.map((el) => el.textContent.trim()));
const COL = '[data-cost-chart="project"] [data-cost-col]';

// pickRange opens the period menu and takes a preset by its label.
async function pickRange(page, label, want) {
  await page.click("[data-cost-period]");
  await page.locator("[data-cost-range]", { hasText: new RegExp(`^${label}$`) }).click();
  await page.waitForURL(want, { timeout: 8000 });
  await waitFor(async () => (await page.locator("[data-cost-period-label]").innerText()) === label, `the period reading ${label}`, 8000);
}

const chartTitle = (page) => page.locator('[data-cost-chart="project"] .card-title').innerText();
const tipShown = (page) => page.evaluate(() => [...document.querySelectorAll("[data-cost-tip]")]
  .filter((tip) => getComputedStyle(tip).display !== "none" && tip.getBoundingClientRect().width > 0).map((tip) => tip.innerText).join("\n"));

async function waitFor(fn, what, timeout = 25000) {
  const deadline = Date.now() + timeout;
  let last;
  while (Date.now() < deadline) {
    last = await fn();
    if (last) return last;
    await sleep(500);
  }
  throw new Error(`${what} did not happen within ${timeout / 1000}s (last: ${JSON.stringify(last)})`);
}

// newAssistant starts an assistant on the fake claude and names it.
async function newAssistant(page, title) {
  const token = await page.evaluate(() => document.querySelector('meta[name="csrf-token"]').content);
  const headers = { Accept: "application/json" };
  const created = await page.request.post(`${BASE}/assistants/new`, { form: { csrf_token: token, form: "new", coder: "claude" }, headers });
  const id = (await created.json().catch(() => ({}))).id || "";
  assert(id, `no assistant: ${created.status()}`);
  await renameAssistant(page, id, title);
  return id;
}

async function renameAssistant(page, id, title) {
  const token = await page.evaluate(() => document.querySelector('meta[name="csrf-token"]').content);
  const renamed = await page.request.post(`${BASE}/assistants/${id}`, { form: { csrf_token: token, form: "rename", title }, headers: { Accept: "application/json" } });
  assert(renamed.ok(), `rename answered ${renamed.status()}`);
}

// assistantSpends runs one chat turn on the fake claude, so the session is the
// assistant's own, then appends priced calls to its transcript.
async function assistantSpends(page, id, usds, at) {
  const token = await page.evaluate(() => document.querySelector('meta[name="csrf-token"]').content);
  const sent = await page.request.post(`${BASE}/assistants/${id}`, { form: { csrf_token: token, form: "message", message: "MAGIC" }, headers: { Accept: "application/json" } });
  assert(sent.ok(), `message answered ${sent.status()}`);
  const cwd = `${HOST_STATE_DIR}/assistant/instances/${id}/workspace`;
  const dir = path.join(CLAUDE_DIR, cwd.replace(/[^a-zA-Z0-9]/g, "-"));
  const file = await waitFor(async () => fs.existsSync(dir) && fs.readdirSync(dir).find((f) => f.endsWith(".jsonl")), `the chat transcript of ${id}`, 15000);
  const session = file.replace(/\.jsonl$/, "");
  files[session] = path.join(dir, file);
  fs.appendFileSync(files[session], usds.map((usd) => call(session, cwd, "claude-sonnet-5-5", usd, at)).join(""));
}

async function deleteAssistant(page, id) {
  const token = await page.evaluate(() => document.querySelector('meta[name="csrf-token"]').content);
  await page.request.post(`${BASE}/assistants/${id}`, { form: { csrf_token: token, form: "delete" }, headers: { Accept: "application/json" } });
}

async function openCosts(page, range = "30d") {
  await page.goto(`${BASE}/costs?range=${range}`, { waitUntil: "domcontentloaded" });
  await L.dismissUpdate(page);
  await L.waitUpgraded(page, ["dc-costs"]);
}

L.runFeature("costs", async ({ browser, page, run, mobilePage }) => {
  assert(fs.existsSync(CLAUDE_DIR), `CLAUDE_DIR ${CLAUDE_DIR} is not mounted`);
  assert(HOST_STATE_DIR, "HOST_STATE_DIR is not set");
  assert(STATE_DIR && fs.existsSync(STATE_DIR), `STATE_DIR ${STATE_DIR} is not mounted`);
  const assistants = [];
  await L.createProject(page, SHOP);
  await L.createProject(page, BLOG);
  const shopPath = await L.projectPath(page, SHOP);
  const blogPath = await L.projectPath(page, BLOG);
  await run("with nothing booked the page shows both tiles at $0.00, the period and four empty charts", async () => {
    for (const [p, shot] of [[page, "cost-empty-new-desktop.png"], [await mobilePage(), "cost-empty-new-mobile.png"]]) {
      await openCosts(p);
      const empty = await p.evaluate(() => {
        const shown = (el) => !!el && getComputedStyle(el).display !== "none" && el.getBoundingClientRect().height > 0;
        const charts = [...document.querySelectorAll("[data-cost-chart]")];
        return {
          tiles: [...document.querySelectorAll("[data-cost-tile]")].filter(shown).map((el) => el.textContent.trim()),
          bar: shown(document.querySelector("[data-cost-toolbar] [data-cost-period]")),
          charts: charts.filter((c) => [".dc-cost-frame", ".dc-cost-axis", "[data-cost-nothing]"].every((sel) => shown(c.querySelector(sel)))).length,
          legend: document.querySelectorAll("[data-cost-legend] [data-series]").length,
          note: shown(document.querySelector("[data-cost-note]")),
          old: document.querySelectorAll("dc-costs .empty").length,
        };
      });
      assert(empty.tiles.join() === "$0.00,$0.00" && empty.bar && empty.charts === 4 && empty.legend === 0 && empty.note && empty.old === 0, `the empty page ${JSON.stringify(empty)}`);
      if (process.env.SHOTS_DIR) await p.screenshot({ path: path.join(process.env.SHOTS_DIR, shot) });
    }
  });

  const now = Date.now();
  const twoDaysAgo = now - 2 * 86400000;
  const shopCall = call(A, shopPath, "claude-opus-5-5", 2.5, twoDaysAgo);
  writeTranscript(A, shopPath, SHOP_TITLE, shopCall + shopCall + call(A, shopPath, "claude-haiku-4-5", 0.5, twoDaysAgo + 60000));
  writeTranscript(B, blogPath, BLOG_TITLE, call(B, blogPath, "claude-sonnet-5-5", 1.25, now - 30 * 60000));
  writeTranscript(D, `/opt/tccost-elsewhere-${tag}`, "cost elsewhere", call(D, `/opt/tccost-elsewhere-${tag}`, "claude-sonnet-5-5", 3.75, now - 20 * 60000));
  const oldWorkspace = `${HOST_STATE_DIR}/assistant/workspace`;
  writeTranscript(OLD, oldWorkspace, "cost old assistant", call(OLD, oldWorkspace, "claude-sonnet-5-5", 0.25, now - 20 * 60000));
  writeTranscript(E, blogPath, E_TITLE, call(E, blogPath, "nemotron-3-ultra", 0, now - 20 * 60000) + call(E, blogPath, "qwen3:8b", 0, now - 20 * 60000));
  writeTranscript(F, blogPath, "cost old days", call(F, blogPath, "claude-haiku-4-5", 0.1, now - 33 * 86400000) + call(F, blogPath, "claude-haiku-4-5", 0.1, now - 40 * 86400000));

  try {
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    const ops = await newAssistant(page, OPS);
    const writer = await newAssistant(page, WRITER);
    assistants.push(ops, writer);
    await assistantSpends(page, ops, [1.5], twoDaysAgo);
    const opsWorkspace = `${HOST_STATE_DIR}/assistant/instances/${ops}/workspace`;
    writeTranscript(CHECK, opsWorkspace, "cost check", call(CHECK, opsWorkspace, "claude-sonnet-5-5", 0.5, twoDaysAgo));
    await assistantSpends(page, writer, [0.75], twoDaysAgo);

    await run("the fixtures are booked within a poll", async () => {
      await waitFor(async () => {
        await openCosts(page);
        return (await legendValue(page, SHOP)) === "$3.00" && (await legendValue(page, BLOG)) === "$1.25" &&
          (await legendValue(page, "Assistants")) === "$2.75";
      }, "booking the fake transcripts", 30000);
    });

    await run("the books are one file of rows per month, the current one with the cursors, beside the owners", async () => {
      const names = fs.readdirSync(path.join(STATE_DIR, "cost")).sort();
      const d = new Date(now);
      const current = `rows-${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}.json`;
      for (const want of ["sessions.json", current]) {
        assert(names.includes(want), `${want} missing in ${names}`);
      }
      assert(names.every((n) => /^(rows-\d{4}-\d{2}|sessions|prices)\.json$/.test(n)), `unexpected files ${names}`);
      const file = JSON.parse(fs.readFileSync(path.join(STATE_DIR, "cost", current), "utf8"));
      assert(file.rows.some((r) => r.session === B) && file.rows.some((r) => r.session === A), `the calls booked this run are not in ${current}`);
      assert(file.cursors && file.cursors.claude, `${current} holds no cursor of claude`);
    });

    await run("the assistants are one series by project and one each by assistant, apart from no project", async () => {
      assert((await legendValue(page, "No project")) === "$4.00", "no project in the legend");
      assert((await legendValue(page, OPS)) === "" && (await legendValue(page, WRITER)) === "", "an assistant is a series of its own");
      const purple = await page.$$eval('[data-cost-chart="project"] [data-cost-legend] [data-series]', (items) => items.filter((i) => i.textContent.includes("Assistants"))
        .map((i) => getComputedStyle(i.querySelector(".dc-cost-swatch")).backgroundColor));
      const others = await page.$$eval('[data-cost-chart="project"] [data-cost-legend] [data-series]', (items) => items.filter((i) => !i.textContent.includes("Assistants"))
        .map((i) => getComputedStyle(i.querySelector(".dc-cost-swatch")).backgroundColor));
      assert(purple.length === 1 && !others.includes(purple[0]), `the assistants share a color: ${purple} in ${others}`);
      const day = await page.$$eval("template[data-cost-tipbody]", (tips) => tips[27].content.textContent.replace(/\s+/g, " "));
      assert(/\$2\.75\s*Assistants/.test(day), `the tooltip two days ago ${day}`);
      assert((await legendValue(page, OPS, "assistant")) === "$2.00", "ops with its check");
      assert((await legendValue(page, WRITER, "assistant")) === "$0.75", "writer");
      const names = await legendLabels(page, "assistant");
      assert(names.length === 2 && names.filter((l) => l === OPS).length === 1, `ops is not one series, or the old workspace counts ${JSON.stringify(names)}`);
    });

    await run("a renamed assistant shows its new name on what it booked before, live", async () => {
      const renamed = `${OPS} renamed`;
      await openCosts(page);
      await renameAssistant(page, assistants[0], renamed);
      await waitFor(async () => (await legendValue(page, renamed, "assistant")) === "$2.00", "the new name on the open page", 15000);
      assert((await legendValue(page, OPS, "assistant")) === "", "the old name still stands");
      await renameAssistant(page, assistants[0], OPS);
      await waitFor(async () => (await legendValue(page, OPS, "assistant")) === "$2.00", "the old name back", 15000);
    });

    await run("the status line shows today alone, desktop only", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      assert(await visible(page, ".dc-status dc-cost-status"), "the cost item is not visible in the status line");
      const today = await page.locator(".dc-status [data-cost-today]").innerText();
      assert(today === "$5.25", `status line today ${today}, want $5.25`);
      assert(await page.locator(".dc-status [data-cost-burn]").count() === 0, "the burn rate is still in the status line");
      const title = await page.locator(".dc-status [data-cost-status]").getAttribute("title");
      assert(/API list price/.test(title), `title ${title}`);
    });

    await run("a click on the item opens the cost page", async () => {
      await Promise.all([page.waitForURL(/\/costs/), page.click(".dc-status [data-cost-status]")]);
      assert((await page.locator(".dc-work-title").innerText()) === "Costs", "no Costs head");
      assert(await visible(page, ".alert-info[data-cost-note]"), "the info box is missing");
      assert(/^Experimental\. Counts claude with Claude and Ollama Cloud models/.test(await page.locator("[data-cost-note]").innerText()), "the info box text");
    });

    await run("two tiles side by side, today and this month", async () => {
      await openCosts(page);
      const tiles = await page.$$eval("[data-cost-tile]", (els) => els.map((el) => [el.dataset.costTile, Math.round(el.getBoundingClientRect().top)]));
      assert(tiles.length === 2 && tiles[0][0] === "Today" && tiles[1][0] === "This month" && tiles[0][1] === tiles[1][1], `tiles ${JSON.stringify(tiles)}`);
      assert((await tile(page, "Today")) === "$5.25" && /^\$\d/.test(await tile(page, "This month")), "the tile values");
    });

    await run("the project chart stacks 30 days with a legend and nothing under it", async () => {
      const shape = await page.evaluate(() => {
        const chart = document.querySelector('[data-cost-chart="project"]');
        const frame = chart.querySelector("[data-cost-frame]").getBoundingClientRect();
        return {
          title: chart.querySelector(".card-title").textContent.trim(),
          columns: chart.querySelectorAll("[data-cost-col]").length,
          tips: chart.querySelectorAll("template[data-cost-tipbody]").length,
          height: frame.height,
          width: frame.width,
          legend: chart.querySelector("[data-cost-legend]").innerText,
          segments: chart.querySelectorAll(".dc-cost-seg").length,
          gone: document.querySelectorAll("[data-cost-cum], .dc-cost-dot, .dc-cost-spark, [data-cost-by], [data-cost-unit], [data-cost-sort]").length,
        };
      });
      assert(shape.title === "Spend per day by project" && shape.columns === 30 && shape.tips === 30, `chart ${JSON.stringify(shape)}`);
      assert(shape.height > 150 && shape.width > 200, `chart box ${shape.width}x${shape.height}`);
      assert(shape.legend.includes(SHOP) && shape.legend.includes(BLOG), `legend ${shape.legend}`);
      assert(shape.segments >= 2 && shape.gone === 0, `segments ${shape.segments}, removed parts left ${shape.gone}`);
    });

    await run("four charts of one look, by project, assistant, coder and model, each series its own color", async () => {
      const charts = await page.$$eval("[data-cost-chart]", (cards) => cards.map((c) => ({
        key: c.dataset.costChart,
        title: c.querySelector(".card-title").textContent.trim(),
        total: c.querySelector("[data-cost-chart-total]").textContent.trim(),
        columns: c.querySelectorAll("[data-cost-col]").length,
        box: c.getBoundingClientRect().toJSON(),
        colors: [...c.querySelectorAll("[data-cost-legend] .dc-cost-swatch")].map((s) => getComputedStyle(s).backgroundColor),
      })));
      assert(charts.map((c) => c.title).join("|") === "Spend per day by project|Spend per day by assistant|Spend per day by coder|Spend per day by model", `titles ${charts.map((c) => c.title)}`);
      assert(charts.every((c) => c.columns === 30 && c.box.width === charts[0].box.width && c.box.left === charts[0].box.left), `not one look ${JSON.stringify(charts)}`);
      assert(charts.every((c, i) => i === 0 || c.box.top >= charts[i - 1].box.bottom), "the charts do not stand one under the other");
      assert(charts.every((c) => new Set(c.colors).size === c.colors.length), `a color repeats in a chart ${JSON.stringify(charts.map((c) => c.colors))}`);
      assert(charts[0].total === charts[3].total, `project ${charts[0].total} and model ${charts[3].total} totals differ`);
      assert((await legendValue(page, SHOP_TITLE, "coder")) === "$3.00" && (await legendValue(page, BLOG_TITLE, "coder")) === "$1.25", "the coders");
      const coders = await legendLabels(page, "coder");
      assert(!coders.some((l) => l.startsWith("Unnamed") || l === OPS), `the coder chart holds what is no coder ${coders}`);
      assert((await legendValue(page, "claude-opus-5-5", "model")) === "$2.50", "opus, the call written twice counts once");
      assert((await legendValue(page, "claude-haiku-4-5", "model")) === "$0.50", "haiku");
      assert(await page.locator("[data-cost-gauge], [data-cost-bar]").count() === 0, "the gauges remain");
    });

    await run("an Ollama Cloud model is priced, a local one is booked at $0 and named once", async () => {
      assert((await legendValue(page, "nemotron-3-ultra", "model")) === "<$0.01", "the ollama cloud model");
      assert((await legendValue(page, "qwen3:8b", "model")) === "$0.00", "the local model");
      assert(await visible(page, "[data-cost-unpriced]"), "the unpriced line is not visible");
      const note = await page.locator("[data-cost-unpriced]").innerText();
      assert(note === "1 session ran a model without a list price, booked at $0.", `note ${note}`);
    });

    await run("the page names the list prices it priced with", async () => {
      const prices = await page.locator("[data-cost-prices]").innerText();
      assert(/^List prices (built into this version|refreshed \d+ \w+ \d\d:\d\d)\./.test(prices), `price note ${prices}`);
    });

    await run("the periods: presets, steps back and forth, custom days, the chart's grain and Back", async () => {
      await openCosts(page);
      await page.evaluate(() => { window.__costsKeep = 1; });
      const tops = await page.evaluate(() => ["[data-cost-tiles]", "[data-cost-toolbar]", '[data-cost-chart="project"]'].map((s) => document.querySelector(s).getBoundingClientRect().top));
      assert(tops[0] < tops[1] && tops[1] < tops[2], `the period is not between the tiles and the chart: ${tops}`);
      await pickRange(page, "Today", /range=today/);
      assert((await legendValue(page, SHOP)) === "", "the shop spent two days ago and is listed today");
      assert((await legendValue(page, BLOG)) === "$1.25", "blog today");
      assert(/^Today, /.test(await page.locator('[data-cost-chart="project"] .card-header .text-secondary').innerText()), "the chart names the period");
      assert((await chartTitle(page)) === "Spend per hour by project" && (await page.locator(COL).count()) >= 23, "today is not cut in hours");
      assert(await page.locator("[data-cost-next]").count() === 0, "today offers a next period");
      await Promise.all([page.waitForURL(/range=yesterday/), page.click("[data-cost-prev]")]);
      await waitFor(async () => (await page.locator("[data-cost-period-label]").innerText()) === "Yesterday", "the previous period of today");
      await pickRange(page, "Last 90 days", /range=90d/);
      assert((await chartTitle(page)) === "Spend per week by project", `90 days in ${await chartTitle(page)}`);
      for (const [label, re, grain] of [["This week", /range=week/, "day"], ["Last week", /range=lastweek/, "day"], ["This month", /range=month/, "day"],
        ["Last month", /range=lastmonth/, "day"], ["Last 7 days", /range=7d/, "day"], ["Last 30 days", (u) => u.pathname === "/costs" && !u.search, "day"], ["Yesterday", /range=yesterday/, "hour"]]) {
        await pickRange(page, label, re);
        const title = await chartTitle(page);
        assert(title === `Spend per ${grain} by project`, `${label} in ${title}`);
      }
      const today = await page.locator("#cost-to").getAttribute("max");
      const d = new Date(`${today}T12:00:00Z`);
      d.setUTCDate(d.getUTCDate() - 2);
      const day = d.toISOString().slice(0, 10);
      await page.click("[data-cost-period]");
      await page.fill("#cost-from", day);
      await page.fill("#cost-to", day);
      await Promise.all([page.waitForURL(/range=custom/), page.click('[data-cost-custom] button[type="submit"]')]);
      const params = new URL(page.url()).searchParams;
      assert(params.get("from") === day && params.get("to") === day, `custom days ${page.url()}`);
      await waitFor(async () => (await legendValue(page, SHOP)) === "$3.00", "the shop on its day");
      assert((await legendValue(page, BLOG)) === "", "blog spent today and is listed two days ago");
      assert((await chartTitle(page)) === "Spend per hour by project", "a custom day is not cut in hours");
      await page.goBack();
      await page.waitForURL(/range=yesterday/);
      await waitFor(async () => (await page.locator("[data-cost-period-label]").innerText()) === "Yesterday", "Back to yesterday");
      assert(await page.evaluate(() => window.__costsKeep === 1), "the page reloaded");
    });

    await run("old addresses with filters, stack by, unit and sort answer and show the one view", async () => {
      const res = await page.goto(`${BASE}/costs?range=30d&project=${SHOP}&session=${A}&coder=claude&q=x&by=model&unit=tokens&sort=name-asc`, { waitUntil: "domcontentloaded" });
      assert(res.status() === 200, `answered ${res.status()}`);
      assert((await legendValue(page, BLOG)) === "$1.25", "an old parameter still changes the view");
      assert((await chartTitle(page)) === "Spend per day by project", `chart ${await chartTitle(page)}`);
      assert(await page.locator("[data-cost-query], [data-cost-chip], [data-cost-clear]").count() === 0, "filter controls remain");
    });

    await run("a bar opens the hours of its day, Back returns to the month", async () => {
      await openCosts(page);
      await page.evaluate(() => { window.__costsKeep = 1; });
      await Promise.all([page.waitForURL(/range=custom/), page.locator(COL).nth(27).click()]);
      const params = new URL(page.url()).searchParams;
      assert(params.get("from") === params.get("to"), `the bar did not open one day: ${page.url()}`);
      await waitFor(async () => (await chartTitle(page)) === "Spend per hour by project", "the day's hours");
      assert((await legendValue(page, SHOP)) === "$3.00", "the shop on its day");
      await page.goBack();
      await page.waitForURL(/range=30d/);
      await waitFor(async () => (await page.locator(COL).count()) === 30, "the 30 days again");
      assert(await page.evaluate(() => window.__costsKeep === 1), "the page reloaded");
    });

    await run("a drilldown lands with the clicked chart right below the pinned bar, desktop and phone", async () => {
      const ctx = await browser.newContext({ ignoreHTTPSErrors: true, hasTouch: true, isMobile: true, viewport: { width: 360, height: 780 } });
      const m = await ctx.newPage();
      await L.login(m);
      for (const p of [page, m]) {
        await openCosts(p);
        await p.evaluate(() => document.getElementById("cost-assistant").scrollIntoView());
        const col = p.locator('[data-cost-chart="assistant"] [data-cost-col]').nth(27);
        if (p === m) {
          await col.tap();
          await Promise.all([p.waitForURL(/range=custom/), p.locator('[data-cost-chart="assistant"] [data-cost-tipdrill]:visible').tap()]);
        } else {
          await Promise.all([p.waitForURL(/range=custom/), col.click()]);
        }
        assert(new URL(p.url()).hash === "#cost-assistant", `the drill lost the chart ${p.url()}`);
        await waitFor(async () => (await p.locator('[data-cost-chart="assistant"] .card-title').innerText()) === "Spend per hour by assistant", "the hours by assistant");
        await sleep(300);
        const at = await p.evaluate(() => {
          const bar = document.querySelector("[data-cost-toolbar]").getBoundingClientRect();
          const chart = document.getElementById("cost-assistant").getBoundingClientRect();
          return { below: chart.top - bar.bottom, top: chart.top, view: window.innerHeight };
        });
        assert(at.below >= 0 && at.below <= 16 && at.top < at.view, `the clicked chart is not right below the bar ${JSON.stringify(at)}`);
      }
      await ctx.close();
    });

    await run("a day older than 30 days has no hours, neither by its bar nor by its URL", async () => {
      await openCosts(page);
      const today = await page.locator("#cost-to").getAttribute("max");
      const ago = (n) => {
        const d = new Date(`${today}T12:00:00Z`);
        d.setUTCDate(d.getUTCDate() - n);
        return d.toISOString().slice(0, 10);
      };
      await openCosts(page, `custom&from=${ago(35)}&to=${ago(25)}`);
      assert((await page.locator(COL).count()) === 11, "the eleven days");
      const drills = await page.locator(COL).evaluateAll((cols) => cols.map((c) => c.hasAttribute("data-drill")));
      assert(JSON.stringify(drills) === JSON.stringify([false, false, false, false, false, true, true, true, true, true, true]), `the bars that open hours ${drills}`);
      await openCosts(page, `custom&from=${ago(40)}&to=${ago(40)}`);
      assert((await chartTitle(page)) === "Spend per day by project" && (await page.locator(COL).count()) === 1, `a folded day in ${await chartTitle(page)}`);
      assert(await page.locator(`${COL}[data-drill]`).count() === 0, "the folded day offers its hours");
      await openCosts(page, `custom&from=${ago(30)}&to=${ago(30)}`);
      assert((await chartTitle(page)) === "Spend per hour by project", "the 30th day has no hours");
    });

    await run("the chart's tooltip follows the mouse and the legend hides a series", async () => {
      await openCosts(page);
      const col = page.locator(COL).nth(27);
      await page.mouse.move(2, 2);
      await col.hover();
      const tip = await waitFor(() => tipShown(page), "the tooltip on hover", 4000);
      assert(tip.includes("$2.75") && tip.includes("Assistants") && !/Running total/.test(tip), `tooltip ${tip}`);
      assert(/Click the bar to show the hours/.test(tip) && !/Show the hours of/.test(tip) && await page.locator("[data-cost-tip] [data-cost-tipdrill]").count() === 0, `the hover tooltip offers a link, not the hint: ${tip}`);
      if (process.env.SHOTS_DIR) await page.screenshot({ path: path.join(process.env.SHOTS_DIR, "cost-units-tooltip-desktop.png") });
      const height = () => col.locator(".dc-cost-stack").evaluate((el) => el.getBoundingClientRect().height);
      const before = await height();
      await page.click('[data-cost-legend] [data-series="assistants"]', { modifiers: ["Control"] });
      assert((await page.getAttribute('[data-cost-legend] [data-series="assistants"]', "aria-pressed")) === "false", "the legend key is not off");
      const shown = await page.$$eval('.dc-cost-seg[data-series="assistants"]', (segs) => segs.filter((s) => getComputedStyle(s).display !== "none").length);
      assert(shown === 0, `${shown} assistant segments still show`);
      assert((await height()) < before - 1, "the bar did not stack again without the assistants");
      await col.hover();
      await waitFor(() => tipShown(page), "the tooltip again", 4000);
      assert(await page.locator('[data-cost-tip] [data-series="assistants"].off').count() === 1, "the hidden series is not dimmed in the tooltip");
      await page.mouse.move(2, 2);
      await waitFor(async () => !(await tipShown(page)), "the tooltip leaving with the mouse", 4000);
      await page.click('[data-cost-legend] [data-series="assistants"]', { modifiers: ["Control"] });
      assert(Math.abs((await height()) - before) < 1, "the series did not come back");
    });

    await run("a legend click isolates a series, again shows all, Ctrl click toggles one, per chart", async () => {
      await openCosts(page);
      const state = () => page.evaluate(() => Object.fromEntries([...document.querySelectorAll("[data-cost-chart]")].map((c) => {
        return [c.dataset.costChart, {
          off: [...c.querySelectorAll('[data-cost-legend] [data-series][aria-pressed="false"]')].map((k) => k.dataset.series),
          keys: c.querySelectorAll("[data-cost-legend] [data-series]").length,
          shown: [...new Set([...c.querySelectorAll(".dc-cost-seg")].filter((s) => getComputedStyle(s).display !== "none").map((s) => s.dataset.series))],
        }];
      })));
      const shop = `[data-cost-chart="project"] [data-cost-legend] [data-series="p:${SHOP}"]`;
      const blog = `[data-cost-chart="project"] [data-cost-legend] [data-series="p:${BLOG}"]`;
      await page.click(shop);
      let s = await state();
      assert(s.project.off.length === s.project.keys - 1 && !s.project.off.includes(`p:${SHOP}`), `not isolated ${JSON.stringify(s.project)}`);
      assert(JSON.stringify(s.project.shown) === JSON.stringify([`p:${SHOP}`]), `other series still drawn ${s.project.shown}`);
      assert(s.model.off.length === 0 && s.coder.off.length === 0, "the filter reached another chart");
      await page.click(blog, { modifiers: ["Control"] });
      s = await state();
      assert(s.project.off.length === s.project.keys - 2 && !s.project.off.includes(`p:${BLOG}`), `Ctrl click did not add blog ${JSON.stringify(s.project)}`);
      await page.click(blog, { modifiers: ["Shift"] });
      s = await state();
      assert(s.project.off.length === s.project.keys - 1 && s.project.off.includes(`p:${BLOG}`), `Shift click did not take blog off ${JSON.stringify(s.project)}`);
      await page.click(shop);
      s = await state();
      assert(s.project.off.length === 0, `a click on the isolated series did not show all ${JSON.stringify(s.project)}`);
      await page.click(blog);
      const opus = '[data-cost-chart="model"] [data-cost-legend] [data-series="m:claude-opus-5-5"]';
      await page.click(opus);
      await page.click(blog);
      s = await state();
      assert(s.project.off.length === 0, `blog did not show all again ${JSON.stringify(s.project)}`);
      assert(s.model.off.length === s.model.keys - 1, `showing all by project reached the model chart ${JSON.stringify(s.model)}`);
      await page.click(opus);
    });

    await run("a phone at 360px reads every legend key whole, stacks the charts and scrolls nothing sideways", async () => {
      const ctx = await browser.newContext({ ignoreHTTPSErrors: true, hasTouch: true, isMobile: true, viewport: { width: 360, height: 780 } });
      const m = await ctx.newPage();
      try {
        await L.login(m);
        await openCosts(m);
        const overflow = await m.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
        assert(overflow <= 0, `the page scrolls sideways by ${overflow}px`);
        const cut = await m.$$eval("[data-cost-legend] [data-series] .text-break, [data-cost-period-label]", (els) => els.filter((el) => {
          const style = getComputedStyle(el);
          return style.textOverflow === "ellipsis" || el.scrollWidth > el.clientWidth + 1;
        }).map((el) => el.textContent.trim()));
        assert(cut.length === 0, `main labels cut: ${cut}`);
        const keys = await m.$$eval("[data-cost-legend] [data-series]", (items) => items.map((k) => {
          const label = k.querySelector(".text-break");
          const value = k.lastElementChild;
          return { name: label.textContent.trim(), lines: label.getBoundingClientRect().height / parseFloat(getComputedStyle(label).lineHeight),
            wrapped: value.getClientRects().length !== 1, out: k.getBoundingClientRect().right > document.documentElement.clientWidth };
        }));
        const bad = keys.filter((k) => k.wrapped || k.out);
        assert(bad.length === 0, `legend keys whose value wraps or that stick out: ${JSON.stringify(bad)}`);
        assert(keys.some((k) => k.name === E_TITLE && k.lines > 1.5), `the long title does not wrap: ${JSON.stringify(keys.find((k) => k.name === E_TITLE))}`);
        const charts = await m.$$eval("[data-cost-chart]", (cards) => cards.map((c) => c.getBoundingClientRect()).map((b) => [Math.round(b.top), Math.round(b.bottom)]));
        assert(charts.length === 4 && charts.every((c, i) => i === 0 || charts[i - 1][1] <= c[0]), `the charts are not stacked ${JSON.stringify(charts)}`);
        const tiles = await m.$$eval("[data-cost-tiles] > div", (els) => els.map((el) => Math.round(el.getBoundingClientRect().top)));
        assert(tiles.length === 2 && tiles[0] === tiles[1], `the two tiles are not in one row ${tiles}`);
        const legendKey = `[data-cost-chart="project"] [data-cost-legend] [data-series="p:${BLOG}"]`;
        const offCount = () => m.locator('[data-cost-chart="project"] [data-cost-legend] [aria-pressed="false"]').count();
        await m.locator(legendKey).tap();
        const count = await m.locator('[data-cost-chart="project"] [data-cost-legend] [data-series]').count();
        assert((await offCount()) === count - 1, "a tap does not isolate");
        await m.locator(legendKey).tap();
        assert((await offCount()) === 0, "a second tap does not show all");
        const box = await m.locator(legendKey).boundingBox();
        const cdp = await m.context().newCDPSession(m);
        const point = [{ x: box.x + box.width / 2, y: box.y + box.height / 2 }];
        const longPress = async () => {
          await cdp.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: point });
          await m.waitForTimeout(700);
          await cdp.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
          await m.waitForTimeout(200);
        };
        await longPress();
        assert((await m.getAttribute(legendKey, "aria-pressed")) === "false" && (await offCount()) === 1, "a long press does not toggle one");
        await longPress();
        assert((await offCount()) === 0, "a second long press does not bring it back");
        await m.locator(COL).nth(27).tap();
        const tip = await waitFor(() => tipShown(m), "the tooltip on a tap", 4000);
        assert(/Show the hours of/.test(tip) && !/Click the bar/.test(tip), `the tapped tooltip ${tip}`);
        assert(new URL(m.url()).searchParams.get("range") === "30d", "the tap drilled instead of showing the numbers");
        await Promise.all([m.waitForURL(/range=custom/), m.locator("[data-cost-tipdrill]:visible").tap()]);
        await waitFor(async () => (await chartTitle(m)) === "Spend per hour by project", "the tapped link opening the day");
      } finally {
        await ctx.close();
      }
    });

    await run("the period stays pinned over the scrolling charts and an anchor lands below it, desktop and phone", async () => {
      const ctx = await browser.newContext({ ignoreHTTPSErrors: true, hasTouch: true, isMobile: true, viewport: { width: 360, height: 780 } });
      const m = await ctx.newPage();
      await L.login(m);
      for (const p of [page, m]) {
        await openCosts(p);
        await p.evaluate(() => document.getElementById("cost-assistant").scrollIntoView());
        const at = await p.evaluate(() => {
          const body = document.querySelector(".dc-work-body");
          const bar = document.querySelector("[data-cost-toolbar]");
          const b = bar.getBoundingClientRect();
          const chart = document.getElementById("cost-assistant").getBoundingClientRect();
          const button = bar.querySelector("[data-cost-period]").getBoundingClientRect();
          return { scrolled: body.scrollTop, offset: b.top - body.getBoundingClientRect().top, below: chart.top - b.bottom,
            inside: button.top >= b.top && button.bottom <= b.bottom, bg: getComputedStyle(bar).backgroundColor };
        });
        assert(at.scrolled > 0 && Math.abs(at.offset) < 1, `the bar is not pinned to the top edge ${JSON.stringify(at)}`);
        assert(at.below >= 0 && at.inside && at.bg !== "rgba(0, 0, 0, 0)", `the chart lands under the bar or the bar shows through ${JSON.stringify(at)}`);
      }
      await ctx.close();
    });

    await run("the period menu fits a landscape phone and scrolls to its last entry", async () => {
      const ctx = await browser.newContext({ ignoreHTTPSErrors: true, hasTouch: true, isMobile: true, viewport: { width: 740, height: 360 } });
      const m = await ctx.newPage();
      await L.login(m);
      await openCosts(m);
      for (const pinned of [false, true]) {
        if (pinned) await m.evaluate(() => document.getElementById("cost-assistant").scrollIntoView());
        await m.click("[data-cost-period]");
        await m.waitForSelector("[data-cost-ranges].show");
        await sleep(300);
        const fit = await m.evaluate(() => {
          const body = document.querySelector(".dc-work-body").getBoundingClientRect();
          const menu = document.querySelector("[data-cost-ranges]");
          const r = menu.getBoundingClientRect();
          return { top: r.top, bottom: r.bottom, bodyTop: body.top, bodyBottom: body.bottom, vh: innerHeight, overflow: getComputedStyle(menu).overflowY, scrolls: menu.scrollHeight > menu.clientHeight };
        });
        assert(fit.top >= fit.bodyTop && fit.bottom <= Math.min(fit.bodyBottom, fit.vh), `the menu runs out of the work body ${JSON.stringify({ pinned, ...fit })}`);
        assert(fit.overflow === "auto" && fit.scrolls, `the menu does not scroll inside ${JSON.stringify({ pinned, ...fit })}`);
        const before = await m.evaluate(() => document.querySelector(".dc-work-body").scrollTop);
        const box = await m.locator("[data-cost-ranges]").boundingBox();
        await m.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
        await m.mouse.wheel(0, 2000);
        const last = '[data-cost-custom] button[type="submit"]';
        await waitFor(() => m.evaluate((sel) => {
          const menu = document.querySelector("[data-cost-ranges]").getBoundingClientRect();
          const b = document.querySelector(sel).getBoundingClientRect();
          return b.top >= menu.top && b.bottom <= menu.bottom + 1;
        }, last), "the last entry scrolled into the menu", 5000);
        const after = await m.evaluate(() => document.querySelector(".dc-work-body").scrollTop);
        assert(after === before, `scrolling the menu moved the page from ${before} to ${after}`);
        if (pinned) {
          if (process.env.SHOTS_DIR) await m.screenshot({ path: path.join(process.env.SHOTS_DIR, "cost-dropdown-landscape.png") });
          await Promise.all([m.waitForURL(/range=custom/), m.locator(last).click()]);
        } else {
          await m.keyboard.press("Escape");
          await m.waitForSelector("[data-cost-ranges].show", { state: "detached" });
        }
      }
      await ctx.close();
    });

    await run("new spend arrives live and keeps the hidden series, the focus, the tooltip and the scroll", async () => {
      await openCosts(page);
      await page.evaluate(() => { window.__costsMarker = 1; });
      await page.click('[data-cost-legend] [data-series="assistants"]', { modifiers: ["Control"] });
      await page.locator(".dc-work-body").evaluate((el) => { el.scrollTop = 120; });
      await page.locator(COL).nth(29).hover();
      await waitFor(() => tipShown(page), "the tooltip before the update", 4000);
      const scroll = await page.locator(".dc-work-body").evaluate((el) => el.scrollTop);
      await page.focus('[data-cost-legend] [data-series="assistants"]');
      fs.appendFileSync(files[B], call(B, blogPath, "claude-sonnet-5-5", 0.75, Date.now()));
      const want = "$6.00";
      await waitFor(async () => (await tile(page, "Today")) === want, `the today tile moving to ${want}`);
      await waitFor(async () => (await page.locator(".dc-status [data-cost-today]").innerText()) === want, `the status line moving to ${want}`);
      assert(await page.evaluate(() => window.__costsMarker === 1), "the page reloaded");
      assert(await page.evaluate(() => document.activeElement?.matches('[data-cost-legend] [data-series="assistants"]')), "the legend key lost the focus");
      assert((await page.getAttribute('[data-cost-legend] [data-series="assistants"]', "aria-pressed")) === "false", "the hidden series came back");
      assert((await tipShown(page)).includes("$6.00"), `the tooltip did not survive or kept old numbers: ${await tipShown(page)}`);
      const after = await page.locator(".dc-work-body").evaluate((el) => el.scrollTop);
      assert(Math.abs(after - scroll) <= 2, `the work body scrolled from ${scroll} to ${after}`);
      await page.click('[data-cost-legend] [data-series="assistants"]', { modifiers: ["Control"] });
    });

    await run("claude's own total tops up what its calls do not explain, once", async () => {
      fs.appendFileSync(files[B], costLine(B, { "claude-sonnet-5-5": 2.25 }) + costLine(B, { "claude-sonnet-5-5": 2.25 }));
      await waitFor(async () => {
        await openCosts(page);
        return (await legendValue(page, BLOG)) === "$2.25";
      }, "the blog topped up to $2.25");
      const note = await page.locator("[data-cost-prices]").innerText();
      assert(/\$0\.25 from claude's own totals beyond its logged calls/.test(note), `top-up note ${note}`);
      await sleep(12000);
      await openCosts(page);
      assert((await legendValue(page, BLOG)) === "$2.25", "the total was booked twice");
    });

    await run("a line written right before a coder delete is booked", async () => {
      fs.appendFileSync(files[A], call(A, shopPath, "claude-opus-5-5", 0.5, twoDaysAgo + 120000));
      const token = await page.evaluate(() => document.querySelector('meta[name="csrf-token"]').content);
      const res = await page.request.post(`${BASE}/coders/${A}/delete`, { headers: { "X-CSRF-Token": token, Accept: "application/json" } });
      assert(res.ok(), `delete answered ${res.status()}`);
      assert(!fs.existsSync(files[A]), "the transcript is still there");
      await openCosts(page);
      const shop = await legendValue(page, SHOP);
      assert(shop === "$3.50", `shop ${shop}, want $3.50`);
      assert((await legendValue(page, SHOP_TITLE, "coder")) === "$3.50", "the deleted coder keeps its name and money");
    });

    await run("a transcript that disappears is no refund", async () => {
      fs.rmSync(files[B]);
      await sleep(12000);
      await openCosts(page);
      const blog = await legendValue(page, BLOG);
      assert(blog === "$2.25", `blog ${blog}, want $2.25`);
    });

    await run("a deleted project keeps its money and its name", async () => {
      await L.deleteProject(page, SHOP);
      await openCosts(page);
      const shop = await legendValue(page, SHOP);
      assert(shop === "$3.50", `shop ${shop} after the project went`);
    });

    await run("the costs settings switch the refresh and show where the prices stand", async () => {
      const box = '#settings-costs input[name="price_refresh"]';
      const text = (sel) => page.locator(sel).innerText();
      await page.goto(`${BASE}/settings/general`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      assert(await page.locator('input[name="price_refresh"], #settings-price-refresh, [data-price-status]').count() === 0, "the list prices still sit in the general settings");
      await Promise.all([page.waitForURL(/\/settings\/costs$/), page.click('[data-settings-nav] a[href="/settings/costs"]')]);
      await page.waitForSelector("#settings-costs", { state: "visible", timeout: 8000 });
      assert(await page.locator('[data-settings-nav] a[href="/settings/costs"].active').count() === 1, "the costs entry is not marked");
      assert(await page.locator(box).isChecked(), "the refresh is not on by default");
      assert(/^API list prices\. Local Ollama models count tokens only\.$/.test(await text("[data-costs-settings-note]")), "the note is missing");
      const table = await text("[data-price-table]");
      const fetched = await text("[data-price-fetched]");
      assert(table === "Built into this version" && fetched === "Never" || table === "Refreshed from LiteLLM" && fetched !== "Never" && fetched !== "",
        `table ${table}, last fetch ${fetched}`);
      const next = await text("[data-price-next]");
      assert(next === "Within a minute" || /\d/.test(next), `next fetch ${next}`);
      const otable = await text("[data-ollama-table]");
      const ofetched = await text("[data-ollama-fetched]");
      const outcome = await text("[data-ollama-outcome]");
      assert(otable === "Built into this version" && ofetched === "Never" || otable === "Refreshed from ollama.com" && /\d/.test(ofetched) && /^Took \d+ models$/.test(outcome),
        `ollama table ${otable}, last fetch ${ofetched}, outcome ${outcome}`);
      assert(outcome === "None yet" || /^(Took \d+ models|Kept the last table, .+)$/.test(outcome), `ollama outcome ${outcome}`);
      const ultra = '[data-ollama-model="nemotron-3-ultra"]';
      assert(await visible(page, ultra), "the Ollama table lacks nemotron-3-ultra");
      assert(/^\$\d/.test(await text(`${ultra} [data-ollama-input]`)) && /^\$\d/.test(await text(`${ultra} [data-ollama-output]`)), "nemotron-3-ultra prices");
      assert(/^\$[\d.]+ \/ \$[\d.]+ \/ \$[\d.]+$/.test(await text('[data-ollama-model="deepseek-v4-pro"] [data-ollama-off-peak]')), "deepseek-v4-pro has no off-peak rate");
      const models = await page.$$eval("[data-price-model]", (rows) => rows.map((r) => r.dataset.priceModel));
      for (const m of ["claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-4-5", "nemotron-3-ultra", "qwen3:8b"]) assert(models.includes(m), `model ${m} missing in ${models}`);
      const opus = '[data-price-model="claude-opus-5-5"]';
      assert((await text(`${opus} [data-price-output]`)) === "$20.00", "opus output price");
      for (const f of ["input", "cache-read", "cache-write-5m", "cache-write-1h", "web-search"]) {
        assert(/^\$\d/.test(await text(`${opus} [data-price-${f}]`)), `opus ${f} price`);
      }
      assert((await text('[data-price-model="claude-haiku-4-5"] [data-price-output]')) === "$5.00", "haiku output price");
      const ollama = '[data-price-model="nemotron-3-ultra"]';
      assert(await page.locator(`${ollama} [data-price-unpriced]`).count() === 0, "the ollama cloud model is marked unpriced");
      assert(/^\$\d/.test(await text(`${ollama} [data-price-input]`)), "the ollama cloud model shows no price");
      const local = '[data-price-model="qwen3:8b"]';
      assert(await visible(page, `${local} [data-price-unpriced]`), "the local model is not marked unpriced");
      assert(await page.locator(`${local} [data-price-input]`).count() === 0, "the local model shows a price");

      await page.locator(box).uncheck();
      await Promise.all([page.waitForURL(/\/settings\/costs/), page.click('#settings-costs button[type="submit"]')]);
      await page.goto(`${BASE}/settings/costs`, { waitUntil: "domcontentloaded" });
      assert(!(await page.locator(box).isChecked()), "switching the refresh off did not stick");
      assert((await text("[data-price-next]")) === "None, refresh is off", "the next fetch does not say the refresh is off");
      await openCosts(page);
      assert(/refresh is off\./.test(await page.locator("[data-cost-prices]").innerText()), "the page does not say the refresh is off");
      await page.goto(`${BASE}/settings/costs`, { waitUntil: "domcontentloaded" });
      await page.locator(box).check();
      await Promise.all([page.waitForURL(/\/settings\/costs/), page.click('#settings-costs button[type="submit"]')]);
      await page.goto(`${BASE}/settings/costs`, { waitUntil: "domcontentloaded" });
      assert(await page.locator(box).isChecked(), "switching the refresh back on did not stick");
      assert((await text("[data-price-next]")) !== "None, refresh is off", "the next fetch still says the refresh is off");
      await openCosts(page);
      assert(!/refresh is off/.test(await page.locator("[data-cost-prices]").innerText()), "the page still says the refresh is off");
    });

    await run("the costs settings keep three months by default, at least two", async () => {
      const field = '#settings-costs input[name="retention"]';
      const kept = () => page.locator("[data-cost-retention-kept]").innerText();
      const months = (n) => {
        const d = new Date();
        const names = [];
        for (let i = n - 1; i >= 0; i--) names.push(new Date(d.getFullYear(), d.getMonth() - i, 1).toLocaleString("en-US", { month: "long" }));
        return `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}`;
      };
      const save = async (value) => {
        await page.fill(field, value);
        await page.evaluate(() => {
          const form = document.querySelector("#settings-costs");
          form.querySelector('input[name="retention"]').removeAttribute("min");
          form.querySelector('input[name="retention"]').removeAttribute("max");
          form.dataset.stale = "1";
        });
        await page.click('#settings-costs button[type="submit"]');
        await page.waitForSelector("#settings-costs:not([data-stale])", { state: "visible", timeout: 8000 });
      };
      await page.goto(`${BASE}/settings/costs`, { waitUntil: "domcontentloaded" });
      assert((await page.inputValue(field)) === "3", `default ${await page.inputValue(field)}`);
      assert(await visible(page, field), "the months field is not visible");
      const help = await page.locator("[data-cost-retention-help]").innerText();
      assert(/^This month included, 2 to 120, default 3\. Older months are deleted\.$/.test(help), `help ${help}`);
      assert((await kept()) === `Kept now: ${months(3)}.`, `kept ${await kept()}`);
      for (const refused of ["1", "121", "9999999999999999"]) {
        await save(refused);
        assert(/Keep 2 to 120 months\./.test(await page.locator("#settings-costs").innerText()), `${refused} months are not refused`);
        assert((await page.inputValue(field)) === "3", `a refused ${refused} was stored`);
      }
      await save("2");
      assert((await page.inputValue(field)) === "2" && (await kept()) === `Kept now: ${months(2)}.`, `two months ${await kept()}`);
      await save("3");
      assert((await page.inputValue(field)) === "3", "three months did not come back");
    });

    await run("the phone reaches the costs settings from the Cockpit sheet", async () => {
      const m = await mobilePage();
      await m.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(m);
      await m.click('.dc-tabbar [data-ctx-area="cockpit"]');
      const link = 'dc-ctx-sheet:not([hidden]) [data-settings-nav] a[href="/settings/costs"]';
      await m.waitForSelector(link, { state: "visible", timeout: 8000 });
      await Promise.all([m.waitForURL(/\/settings\/costs$/), m.click(link)]);
      await m.waitForSelector('[data-price-model="claude-opus-5-5"]', { state: "visible", timeout: 8000 });
      assert(await visible(m, '[data-price-model="claude-opus-5-5"] [data-price-web-search]'), "the last price of a row is not shown on a phone");
      const cut = await m.$$eval("[data-price-model] dd", (dds) => dds.filter((d) => d.scrollWidth > d.clientWidth).map((d) => d.textContent));
      assert(cut.length === 0, `prices overflow their cell: ${cut}`);
      const overflow = await m.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
      assert(overflow <= 0, `the costs settings scroll sideways by ${overflow}px`);
    });

    await run("the phone reaches the page from the Cockpit sheet", async () => {
      const m = await mobilePage();
      await m.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(m);
      assert(!(await visible(m, ".dc-status dc-cost-status")), "the cost item shows on a phone");
      await m.click('.dc-tabbar [data-ctx-area="cockpit"]');
      await m.waitForSelector("[data-cockpit-costs]", { state: "visible", timeout: 8000 });
      await Promise.all([m.waitForURL(/\/costs/), m.click("[data-cockpit-costs]")]);
      await m.waitForSelector("[data-cost-chart]", { state: "visible", timeout: 8000 });
      const overflow = await m.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
      assert(overflow <= 0, `the page scrolls sideways by ${overflow}px`);
      const active = await m.evaluate(() => document.querySelector('.dc-tabbar [data-ctx-area="cockpit"]').classList.contains("active"));
      assert(active, "the Cockpit tab is not active on the cost page");
    });

    const many = (i) => `tccost-many-${tag}-${String(i).padStart(2, "0")}`;
    const bars = () => page.$$eval(COL, (cols) => {
      const card = getComputedStyle(cols[0].closest(".card")).backgroundColor;
      const swatch = Object.fromEntries([...document.querySelectorAll('[data-cost-chart="project"] [data-cost-legend] [data-series]')]
        .map((k) => [k.dataset.series, getComputedStyle(k.querySelector(".dc-cost-swatch")).backgroundColor]));
      const plot = cols[0].closest("[data-cost-frame]").querySelector(".dc-cost-plot").getBoundingClientRect().height;
      const grid = [...document.querySelectorAll('[data-cost-chart="project"] .dc-cost-grid')].filter((g) => getComputedStyle(g).display !== "none")
        .map((g) => [parseFloat(g.style.getPropertyValue("--b")), g.textContent.trim()]).sort((x, y) => x[0] - y[0]).map((g) => g[1]);
      return { card, plot, grid, order: Object.keys(swatch), keys: Object.keys(swatch).length, cols: cols.map((c) => {
        const stack = c.querySelector(".dc-cost-stack");
        const folded = [...stack.querySelectorAll(".dc-cost-seg[data-series]")].filter((s) => getComputedStyle(s).display === "none").map((s) => parseFloat(s.dataset.v));
        const segs = [...stack.children].filter((s) => getComputedStyle(s).display !== "none").map((s) => ({
          series: s.dataset.series || "other",
          v: parseFloat(s.dataset.v),
          h: s.getBoundingClientRect().height,
          bg: getComputedStyle(s).backgroundColor,
          swatch: swatch[s.dataset.series] || "",
          shadow: getComputedStyle(s).boxShadow,
        }));
        return { segs, folded, stack: stack.getBoundingClientRect().height, share: parseFloat(stack.style.getPropertyValue("--h")) / 100 * plot };
      }) };
    });
    const dollars = (label) => parseFloat(label.replace(/[^0-9.]/g, ""));
    let before;

    await run("a bar folds only the series too thin to show into Other on top, the biggest of the period at the bottom, without a gap, a line or a background block", async () => {
      const dir = (name) => path.join(path.dirname(shopPath), name);
      const book = (name, usd, at) => {
        const id = crypto.randomUUID();
        writeTranscript(id, dir(name), `cost ${name}`, call(id, dir(name), "claude-sonnet-5-5", usd, at));
      };
      book(`tccost-big-${tag}`, 600, now - 6 * 86400000);
      for (let i = 0; i < 60; i++) book(many(i), 1, now - 5 * 86400000);
      for (let i = 0; i < 30; i++) book(many(i), 0.25, now - 4 * 86400000);
      book(`tccost-mid-${tag}`, 60, now - 4 * 86400000);
      book(`tccost-top-${tag}`, 100, now - 4 * 86400000);
      for (let i = 0; i < 12; i++) book(`tccost-wide-${tag}-${String(i).padStart(2, "0")}`, 30, now - 3 * 86400000);
      await waitFor(async () => {
        await openCosts(page);
        return (await legendValue(page, many(59))) === "$1.00" && (await legendValue(page, many(0))) === "$1.25" && (await legendValue(page, `tccost-top-${tag}`)) === "$100.00" &&
          (await legendValue(page, `tccost-wide-${tag}-11`)) === "$30.00";
      }, "booking the 74 projects", 30000);
      const cards = [];
      for (const scheme of ["light", "dark"]) {
        await page.emulateMedia({ colorScheme: scheme });
        await openCosts(page);
        const b = await bars();
        cards.push(b.card);
        assert(b.keys >= 20, `${b.keys} series in the legend`);
        for (const [i, c] of b.cols.entries()) {
          if (!c.segs.length) continue;
          const own = c.segs.filter((s) => s.series !== "other");
          const px = c.share / c.segs.reduce((sum, s) => sum + s.v, 0);
          const thin = own.filter((s) => s.v * px < 3);
          assert(c.folded.every((v) => v * px < 3) && (c.folded.length ? c.folded.length > 1 && !thin.length : thin.length <= 1) && !!c.folded.length === (c.segs.at(-1).series === "other"),
            `${scheme} bar ${i} keeps ${JSON.stringify(own.map((s) => s.v * px))}px on its own, folds ${JSON.stringify(c.folded.map((v) => v * px))}px`);
          const ranks = c.segs.map((s) => s.series === "other" ? b.order.length : b.order.indexOf(s.series));
          assert(ranks.every((r, j) => r >= 0 && (j === 0 || ranks[j - 1] < r)), `${scheme} bar ${i} does not stack in the legend's order ${JSON.stringify(c.segs.map((s) => s.series))}`);
          const drawn = c.segs.reduce((sum, s) => sum + s.h, 0);
          assert(Math.abs(c.stack - c.share) < 1 && Math.abs(drawn - c.stack) < 1, `${scheme} bar ${i}: segments ${drawn}px, stack ${c.stack}px, share ${c.share}px`);
          for (const s of c.segs) {
            assert(s.bg !== b.card && s.bg !== "rgba(0, 0, 0, 0)" && (s.series === "other" || s.bg === s.swatch) && s.shadow === "none", `${scheme} bar ${i}: ${s.series} paints ${s.bg} with ${s.shadow} on a card of ${b.card}`);
          }
        }
        const x = b.cols[24].segs;
        assert(x.length === 1 && x[0].series === "other" && x[0].h >= 3, `${scheme} the bar of 60 thin projects ${JSON.stringify(x)}`);
        const y = b.cols[25].segs;
        assert(y.length === 3 && y[0].series === `p:tccost-top-${tag}` && y[1].series === `p:tccost-mid-${tag}` && y[2].series === "other" && y[2].h >= 3,
          `${scheme} the bar of two big and 30 tiny ${JSON.stringify(y)}`);
        const z = b.cols[26].segs;
        assert(z.length === 12 && z.every((s) => s.series !== "other" && s.h >= 3), `${scheme} the bar of 12 wide projects folds ${JSON.stringify(z)}`);
        if (process.env.SHOTS_DIR) await page.locator('[data-cost-chart="project"]').screenshot({ path: path.join(process.env.SHOTS_DIR, `cost-many-series-${scheme}.png`) });
      }
      await page.emulateMedia({ colorScheme: null });
      assert(cards[0] !== cards[1], `the card is ${cards[0]} in both schemes`);
      before = (await bars()).grid;
    });

    await run("isolating a series from Other draws it in its own color on a rescaled axis, showing all brings the scale back", async () => {
      await openCosts(page);
      assert(JSON.stringify((await bars()).grid) === JSON.stringify(before), "the axis is not the server's on a fresh page");
      const key = `[data-cost-chart="project"] [data-cost-legend] [data-series="p:${many(20)}"]`;
      await page.click(key);
      const b = await bars();
      const x = b.cols[24].segs;
      assert(x.length === 1 && x[0].series === `p:${many(20)}` && x[0].bg === x[0].swatch && x[0].bg !== b.card, `the isolated series ${JSON.stringify(x)}`);
      assert(dollars(b.grid.at(-1)) < dollars(before.at(-1)), `top label ${b.grid.at(-1)}, before ${before.at(-1)}`);
      assert(x[0].h > b.plot / 2 && Math.abs(b.cols[24].stack - b.cols[24].share) < 1, `the bar is ${x[0].h}px of ${b.plot}px`);
      await page.click(key);
      const all = await bars();
      assert(JSON.stringify(all.grid) === JSON.stringify(before), `the axis ${all.grid}, the server's ${before}`);
      assert(all.cols[24].segs.at(-1).series === "other", "Other did not come back");
    });

    await run("a Ctrl click takes one series off and rescales, a second brings it and the server's axis back", async () => {
      await openCosts(page);
      const key = `[data-cost-chart="project"] [data-cost-legend] [data-series="p:tccost-big-${tag}"]`;
      await page.click(key, { modifiers: ["Control"] });
      const off = await bars();
      assert((await page.getAttribute(key, "aria-pressed")) === "false" && off.cols[23].segs.length === 0, `the big series still draws ${JSON.stringify(off.cols[23])}`);
      assert(dollars(off.grid.at(-1)) < dollars(before.at(-1)) && off.cols[25].segs.some((s) => s.series === `p:tccost-top-${tag}`), `top label ${off.grid.at(-1)}, before ${before.at(-1)}`);
      const pressed = await page.$$eval('[data-cost-chart="project"] [data-cost-legend] [aria-pressed="false"]', (keys) => keys.length);
      assert(pressed === 1, `${pressed} keys are off after one Ctrl click`);
      await page.click(key, { modifiers: ["Control"] });
      const on = await bars();
      assert(JSON.stringify(on.grid) === JSON.stringify(before) && on.cols[23].segs.length === 1, `the axis ${on.grid}, the server's ${before}`);
    });

    await run("the tooltip lists every series of a bar by value, none at zero", async () => {
      await openCosts(page);
      await page.mouse.move(2, 2);
      await page.locator(COL).nth(25).hover();
      await waitFor(() => tipShown(page), "the tooltip on hover", 4000);
      const rows = await page.$$eval("[data-cost-tip] .dc-cost-tiprow", (els) => els.map((el) => el.querySelector(".fw-medium").textContent.trim()));
      const values = rows.map(dollars);
      assert(rows.length === 32 && values.every((v, i) => v > 0 && (i === 0 || values[i - 1] >= v)), `tooltip rows ${rows}`);
      await page.mouse.move(2, 2);
    });
  } finally {
    await L.deleteProject(page, SHOP).catch(() => {});
    await L.deleteProject(page, BLOG).catch(() => {});
    for (const id of assistants) await deleteAssistant(page, id).catch(() => {});
    for (const f of Object.values(files)) fs.rmSync(f, { force: true });
  }
});
