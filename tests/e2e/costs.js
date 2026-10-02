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
// HOST_STATE_DIR is the instance's --state-dir as the host spells it, only
// read as text: it names the assistants' workspaces, where their chat turns
// leave their transcripts.
//
// Gotchas:
// - the fixtures price output tokens only, at the embedded list prices:
//   opus 5.5 20, sonnet 5.5 10, haiku 4.5 5 USD per million. A refresh of the
//   prices from LiteLLM holds the same numbers for these models.
// - claude writes one line per content block of a call, the shop's first call
//   is written twice and must count once.
// - a poll runs every 10s, so a check waits up to 25s for a change to land.
// - the books in the cost folder outlive a run, and the breakdowns fold after 12 rows and the legend
//   at 7 projects, so every run needs a fresh state directory.
// - visibility is read from computed style and the box, never the attribute.

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

const shareRow = (page, key, label) => page.evaluate(([k, l]) => {
  const rows = [...document.querySelectorAll(`[data-cost-shares="${k}"] [data-cost-row]`)];
  const row = rows.find((r) => r.querySelector(":scope > .dc-cost-item [data-cost-label]").textContent.trim() === l);
  const text = (sel) => row.querySelector(`:scope > .dc-cost-item ${sel}`)?.textContent.trim() || "";
  return row ? {
    nested: Boolean(row.parentElement.closest("[data-cost-row]")),
    value: text("[data-cost-value]"),
    badge: text("[data-cost-badge]"),
    sub: text("[data-cost-sub]"),
    alt: text("[data-cost-alt]"),
    shown: getComputedStyle(row).display !== "none" && row.getBoundingClientRect().height > 0,
  } : null;
}, [key, label]);
const shareValue = async (page, key, label) => (await shareRow(page, key, label))?.value || "";
const shareLabels = (page, key) => page.$$eval(`[data-cost-shares="${key}"] [data-cost-list] > [data-cost-row]`, (rows) => rows
  .filter((r) => getComputedStyle(r).display !== "none")
  .map((r) => r.querySelector(":scope > .dc-cost-item [data-cost-label]").textContent.trim()));

// pickRange opens the period menu and takes a preset by its label.
async function pickRange(page, label, want) {
  await page.click("[data-cost-period]");
  await page.locator("[data-cost-range]", { hasText: new RegExp(`^${label}$`) }).click();
  await page.waitForURL(want, { timeout: 8000 });
  await waitFor(async () => (await page.locator("[data-cost-period-label]").innerText()) === label, `the period reading ${label}`, 8000);
}

const chartTitle = (page) => page.locator("[data-cost-chart] .card-title").innerText();
const tipShown = (page) => page.evaluate(() => {
  const tip = document.querySelector("[data-cost-tip]");
  const box = tip.getBoundingClientRect();
  return getComputedStyle(tip).display !== "none" && box.width > 0 ? tip.innerText : "";
});

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
  const now = Date.now();
  const twoDaysAgo = now - 2 * 86400000;
  const shopCall = call(A, shopPath, "claude-opus-5-5", 2.5, twoDaysAgo);
  writeTranscript(A, shopPath, SHOP_TITLE, shopCall + shopCall + call(A, shopPath, "claude-haiku-4-5", 0.5, twoDaysAgo + 60000));
  writeTranscript(B, blogPath, BLOG_TITLE, call(B, blogPath, "claude-sonnet-5-5", 1.25, now - 30 * 60000));
  writeTranscript(D, `/opt/tccost-elsewhere-${tag}`, "cost elsewhere", call(D, `/opt/tccost-elsewhere-${tag}`, "claude-sonnet-5-5", 3.75, now - 20 * 60000));
  const oldWorkspace = `${HOST_STATE_DIR}/assistant/workspace`;
  writeTranscript(OLD, oldWorkspace, "cost old assistant", call(OLD, oldWorkspace, "claude-sonnet-5-5", 0.25, now - 20 * 60000));
  writeTranscript(E, blogPath, E_TITLE, call(E, blogPath, "nemotron-3-ultra", 0, now - 20 * 60000));

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
        return (await shareValue(page, "projects", SHOP)) === "$3.00" && (await shareValue(page, "projects", BLOG)) === "$1.25" &&
          (await shareValue(page, "projects", "Assistants")) === "$2.75";
      }, "booking the fake transcripts", 30000);
    });

    await run("the books are one file of rows per month beside the owners and the cursors", async () => {
      const names = fs.readdirSync(path.join(STATE_DIR, "cost")).sort();
      const month = (at) => { const d = new Date(at); return `rows-${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}.json`; };
      for (const want of ["cursors.json", "sessions.json", month(now), month(twoDaysAgo)]) {
        assert(names.includes(want), `${want} missing in ${names}`);
      }
      assert(names.every((n) => /^(rows-\d{4}-\d{2}|sessions|cursors|prices)\.json$/.test(n)), `unexpected files ${names}`);
      const rows = JSON.parse(fs.readFileSync(path.join(STATE_DIR, "cost", month(now)), "utf8")).rows;
      assert(rows.some((r) => r.session === B), `the blog call is not in ${month(now)}`);
    });

    await run("the assistants are one group split by assistant, apart from no project", async () => {
      const rows = await page.$$eval('[data-cost-shares="projects"] [data-cost-row]', (items) => items.map((r) => ({
        label: r.querySelector(":scope > .dc-cost-item [data-cost-label]").textContent.trim(),
        nested: Boolean(r.parentElement.closest("[data-cost-row]")),
        indent: r.querySelector(":scope > .dc-cost-item [data-cost-label]").getBoundingClientRect().left,
      })));
      const at = rows.findIndex((r) => r.label === "Assistants");
      assert(at >= 0 && !rows[at].nested, `no Assistants group in ${JSON.stringify(rows)}`);
      const members = rows.slice(at + 1, at + 3);
      assert(members.map((r) => r.label).join(",") === `${OPS},${WRITER}` && members.every((r) => r.nested && r.indent > rows[at].indent),
        `the assistants under the group ${JSON.stringify(rows)}`);
      assert(rows.filter((r) => r.nested).length === 2, `nested rows outside the group ${JSON.stringify(rows)}`);
      assert((await shareValue(page, "projects", OPS)) === "$2.00", "ops counts its chat and its check");
      assert((await shareValue(page, "projects", WRITER)) === "$0.75", "writer");
      const none = await shareRow(page, "projects", "No project");
      assert(none && none.value === "$4.00" && !none.nested, `no project ${JSON.stringify(none)}`);
      const legend = await page.locator("[data-cost-legend]").innerText();
      assert(legend.includes("Assistants") && legend.includes("No project"), `legend ${legend}`);
      const purple = await page.$$eval('[data-cost-legend] [data-series]', (items) => items.filter((i) => i.textContent.includes("Assistants"))
        .map((i) => getComputedStyle(i.querySelector(".dc-cost-swatch")).backgroundColor));
      const others = await page.$$eval('[data-cost-legend] [data-series]', (items) => items.filter((i) => !i.textContent.includes("Assistants"))
        .map((i) => getComputedStyle(i.querySelector(".dc-cost-swatch")).backgroundColor));
      assert(purple.length === 1 && !others.includes(purple[0]), `the assistants share a color: ${purple} in ${others}`);
      const day = await page.$$eval("template[data-cost-tipbody]", (tips) => tips[27].content.textContent.replace(/\s+/g, " "));
      assert(/\$2\.75\s*Assistants/.test(day), `the tooltip two days ago ${day}`);
      const opsRow = await shareRow(page, "sessions", OPS);
      assert(opsRow && opsRow.value === "$2.00" && opsRow.badge === "Assistant", `ops with its check in the sessions ${JSON.stringify(opsRow)}`);
      const sessions = await shareLabels(page, "sessions");
      assert(sessions.filter((l) => l === OPS).length === 1, `ops is not one line ${JSON.stringify(sessions)}`);
      const kinds = await shareLabels(page, "kinds");
      assert(kinds.every((k) => ["Coder", "Assistant", "Other"].includes(k)), `kinds ${JSON.stringify(kinds)}`);
    });

    await run("a renamed assistant shows its new name on what it booked before, live", async () => {
      const renamed = `${OPS} renamed`;
      await openCosts(page);
      await renameAssistant(page, assistants[0], renamed);
      await waitFor(async () => (await shareValue(page, "projects", renamed)) === "$2.00", "the new name on the open page", 15000);
      assert(!(await shareRow(page, "projects", OPS)), "the old name still stands");
      const row = await shareRow(page, "sessions", renamed);
      assert(row && row.value === "$2.00" && row.badge === "Assistant", `the sessions under the new name ${JSON.stringify(row)}`);
      await renameAssistant(page, assistants[0], OPS);
      await waitFor(async () => (await shareValue(page, "projects", OPS)) === "$2.00", "the old name back", 15000);
    });

    await run("the status line shows today and the burn rate, desktop only", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      assert(await visible(page, ".dc-status dc-cost-status"), "the cost item is not visible in the status line");
      const burn = await page.locator(".dc-status [data-cost-burn]").innerText();
      assert(burn === "$5.25", `status line burn ${burn}, want $5.25`);
      const title = await page.locator(".dc-status [data-cost-status]").getAttribute("title");
      assert(/API list price/.test(title), `title ${title}`);
    });

    await run("a click on the item opens the cost page", async () => {
      await Promise.all([page.waitForURL(/\/costs/), page.click(".dc-status [data-cost-status]")]);
      assert((await page.locator(".dc-work-title").innerText()) === "Costs", "no Costs head");
      assert(/API list price equivalent/.test(await page.locator(".dc-work-body").innerText()), "the list price note is missing");
    });

    await run("the tiles name today, the week, the month and the burn rate", async () => {
      await openCosts(page);
      const burn = await tile(page, "Burn rate");
      assert(burn === "$5.25/h", `burn tile ${burn}, want $5.25/h`);
      for (const label of ["Today", "This week", "This month"]) assert(/^\$\d/.test(await tile(page, label)), `${label} tile empty`);
    });

    await run("the chart stacks 30 days by project with a legend and a running total", async () => {
      const shape = await page.evaluate(() => {
        const chart = document.querySelector("[data-cost-chart]");
        const frame = chart.querySelector("[data-cost-frame]").getBoundingClientRect();
        return {
          title: chart.querySelector(".card-title").textContent.trim(),
          columns: chart.querySelectorAll("[data-cost-col]").length,
          tips: chart.querySelectorAll("template[data-cost-tipbody]").length,
          height: frame.height,
          width: frame.width,
          legend: chart.querySelector("[data-cost-legend]").innerText,
          segments: chart.querySelectorAll(".dc-cost-seg").length,
          line: chart.querySelector("[data-cost-cum] polyline")?.getAttribute("points").split(" ").length || 0,
        };
      });
      assert(shape.title === "Spend per day" && shape.columns === 30 && shape.tips === 30, `chart ${JSON.stringify(shape)}`);
      assert(shape.height > 150 && shape.width > 200, `chart box ${shape.width}x${shape.height}`);
      assert(shape.legend.includes(SHOP) && shape.legend.includes(BLOG), `legend ${shape.legend}`);
      assert(shape.segments >= 2 && shape.line === 31, `segments ${shape.segments}, running total points ${shape.line}`);
    });

    await run("the breakdowns name projects, coders with their kind and models", async () => {
      const shop = await shareRow(page, "sessions", SHOP_TITLE);
      assert(shop && shop.value === "$3.00" && shop.badge === "Coder" && shop.sub === SHOP, `coder row ${JSON.stringify(shop)}`);
      const elsewhere = await shareRow(page, "sessions", `Unnamed ${D.slice(0, 8)}`);
      assert(elsewhere && elsewhere.value === "$3.75" && elsewhere.badge === "Other" && elsewhere.sub === "No project", `a coder the cockpit does not know ${JSON.stringify(elsewhere)}`);
      const old = await shareRow(page, "sessions", `Unnamed ${OLD.slice(0, 8)}`);
      assert(old && old.value === "$0.25" && old.badge === "Other" && old.sub === "No project", `the assistant from before parallel assistants ${JSON.stringify(old)}`);
      const opus = await shareRow(page, "models", "claude-opus-5-5");
      assert(opus && opus.value === "$2.50" && opus.alt === "125.0K tokens", `opus share ${JSON.stringify(opus)}, the call written twice counts once`);
      assert((await shareValue(page, "models", "claude-haiku-4-5")) === "$0.50", "haiku share");
    });

    await run("a model without a list price counts its tokens and no money", async () => {
      const ollama = await shareRow(page, "models", "nemotron-3-ultra");
      assert(ollama && ollama.value === "$0.00" && ollama.alt === "1.0K tokens", `ollama share ${JSON.stringify(ollama)}`);
      assert(await visible(page, "[data-cost-unpriced]"), "the unpriced note is not visible");
      const note = await page.locator("[data-cost-unpriced]").innerText();
      assert(/1 session ran a model without a list price, their tokens are counted, no spend/.test(note), `note ${note}`);
    });

    await run("the page names the list prices it priced with", async () => {
      const prices = await page.locator("[data-cost-prices]").innerText();
      assert(/^List prices (built into this version|refreshed \d+ \w+ \d\d:\d\d)\./.test(prices), `price note ${prices}`);
    });

    await run("the periods: presets, steps back and forth, custom days, the chart's grain and Back", async () => {
      await openCosts(page);
      await page.evaluate(() => { window.__costsKeep = 1; });
      await pickRange(page, "Today", /range=today/);
      assert((await shareValue(page, "projects", SHOP)) === "", "the shop spent two days ago and is listed today");
      assert((await shareValue(page, "projects", BLOG)) === "$1.25", "blog today");
      assert(/Today/.test(await page.locator("h2").first().innerText()), "breakdown head");
      assert((await chartTitle(page)) === "Spend per hour" && (await page.locator("[data-cost-col]").count()) >= 23, "today is not cut in hours");
      assert(await page.locator("[data-cost-next]").count() === 0, "today offers a next period");
      await Promise.all([page.waitForURL(/range=yesterday/), page.click("[data-cost-prev]")]);
      await waitFor(async () => (await page.locator("[data-cost-period-label]").innerText()) === "Yesterday", "the previous period of today");
      await pickRange(page, "Last 90 days", /range=90d/);
      assert((await chartTitle(page)) === "Spend per week", `90 days in ${await chartTitle(page)}`);
      for (const [label, re, grain] of [["This week", /range=week/, "day"], ["Last week", /range=lastweek/, "day"], ["This month", /range=month/, "day"],
        ["Last month", /range=lastmonth/, "day"], ["Last 7 days", /range=7d/, "day"], ["Last 30 days", (u) => u.pathname === "/costs" && !u.search, "day"], ["Yesterday", /range=yesterday/, "hour"]]) {
        await pickRange(page, label, re);
        const title = await chartTitle(page);
        assert(title === `Spend per ${grain}`, `${label} in ${title}`);
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
      await waitFor(async () => (await shareValue(page, "projects", SHOP)) === "$3.00", "the shop on its day");
      assert((await shareValue(page, "projects", BLOG)) === "", "blog spent today and is listed two days ago");
      assert((await chartTitle(page)) === "Spend per hour", "a custom day is not cut in hours");
      await page.goBack();
      await page.waitForURL(/range=yesterday/);
      await waitFor(async () => (await page.locator("[data-cost-period-label]").innerText()) === "Yesterday", "Back to yesterday");
      assert(await page.evaluate(() => window.__costsKeep === 1), "the page reloaded");
    });

    await run("a breakdown row narrows the page, its chip takes it back", async () => {
      await openCosts(page);
      await page.evaluate(() => { window.__costsKeep = 1; });
      await Promise.all([page.waitForURL(/project=/), page.click(`[data-cost-shares="projects"] [data-cost-row][data-name="${SHOP}"] > [data-cost-drill]`)]);
      await waitFor(async () => (await page.locator('[data-cost-chip="Project"]').count()) === 1, "the project chip");
      assert((await page.locator('[data-cost-chip="Project"]').innerText()).includes(SHOP), "the chip names the project");
      assert(JSON.stringify(await shareLabels(page, "projects")) === JSON.stringify([SHOP]), `projects ${await shareLabels(page, "projects")}`);
      const legend = await page.locator("[data-cost-legend]").innerText();
      assert(legend.includes(SHOP) && !legend.includes(BLOG) && !legend.includes("Assistants"), `the chart is not narrowed: ${legend}`);
      assert(JSON.stringify(await shareLabels(page, "sessions")) === JSON.stringify([SHOP_TITLE]), `sessions ${await shareLabels(page, "sessions")}`);
      await Promise.all([page.waitForURL((u) => !u.searchParams.has("project")), page.click('[data-cost-chip="Project"]')]);
      await waitFor(async () => (await shareValue(page, "projects", BLOG)) === "$1.25", "blog back after the chip went");
      assert(await page.locator("[data-cost-chips]").count() === 0, "chips stand without a filter");
      assert(await page.evaluate(() => window.__costsKeep === 1), "the page reloaded");
    });

    await run("a session row narrows to the session and its coder, Clear filters drops both", async () => {
      await Promise.all([page.waitForURL(/session=/), page.click(`[data-cost-shares="sessions"] [data-cost-row][data-name="${SHOP_TITLE}"] > [data-cost-drill]`)]);
      const params = new URL(page.url()).searchParams;
      assert(params.get("session") === A && params.get("coder") === "claude", `drill ${page.url()}`);
      await waitFor(async () => (await page.locator("[data-cost-chip]").count()) === 2, "session and coder chips");
      assert((await page.locator('[data-cost-chip="Session"]').innerText()).includes(SHOP_TITLE), "the session chip names the session");
      assert(JSON.stringify(await shareLabels(page, "models")) === JSON.stringify(["claude-opus-5-5", "claude-haiku-4-5"]), `models ${await shareLabels(page, "models")}`);
      await Promise.all([page.waitForURL((u) => !u.searchParams.has("session") && !u.searchParams.has("coder")), page.click("[data-cost-clear]")]);
      await waitFor(async () => (await page.locator("[data-cost-chip]").count()) === 0, "the chips gone");
    });

    await run("a bar opens the hours of its day, Back returns to the month", async () => {
      await openCosts(page);
      await page.evaluate(() => { window.__costsKeep = 1; });
      await Promise.all([page.waitForURL(/range=custom/), page.locator("[data-cost-col]").nth(27).click()]);
      const params = new URL(page.url()).searchParams;
      assert(params.get("from") === params.get("to"), `the bar did not open one day: ${page.url()}`);
      await waitFor(async () => (await chartTitle(page)) === "Spend per hour", "the day's hours");
      assert((await shareValue(page, "projects", SHOP)) === "$3.00", "the shop on its day");
      await page.goBack();
      await page.waitForURL(/range=30d/);
      await waitFor(async () => (await page.locator("[data-cost-col]").count()) === 30, "the 30 days again");
      assert(await page.evaluate(() => window.__costsKeep === 1), "the page reloaded");
    });

    await run("the breakdowns sort by spend, tokens, name and share and keep it on this screen", async () => {
      await openCosts(page);
      const byName = (labels) => [...labels].sort((a, b) => a.localeCompare(b, undefined, { numeric: true, sensitivity: "base" }));
      await page.click('[data-sort="name"]');
      let labels = await shareLabels(page, "projects");
      assert(JSON.stringify(labels) === JSON.stringify(byName(labels)), `name ascending ${labels}`);
      assert(new URL(page.url()).searchParams.get("sort") === "name-asc", `sort in the URL ${page.url()}`);
      await page.click('[data-sort="name"]');
      labels = await shareLabels(page, "projects");
      assert(JSON.stringify(labels) === JSON.stringify(byName(labels).reverse()), `name descending ${labels}`);
      await openCosts(page);
      labels = await shareLabels(page, "projects");
      assert(JSON.stringify(labels) === JSON.stringify(byName(labels).reverse()), `the kept sort after a load ${labels}`);
      assert((await page.getAttribute('[data-sort="name"]', "aria-pressed")) === "true", "the kept sort is not marked");
      await page.goto(`${BASE}/costs?range=30d&sort=usd-asc`, { waitUntil: "domcontentloaded" });
      await L.waitUpgraded(page, ["dc-costs"]);
      const usd = await page.$$eval('[data-cost-shares="projects"] [data-cost-list] > [data-cost-row]', (rows) => rows.map((r) => parseFloat(r.dataset.usd)));
      assert(usd.every((v, i) => i === 0 || usd[i - 1] <= v), `the URL's sort loses to the kept one ${usd}`);
      await page.click('[data-sort="tokens"]');
      const tokens = await page.$$eval('[data-cost-shares="models"] [data-cost-list] > [data-cost-row]', (rows) => rows.map((r) => parseFloat(r.dataset.tokens)));
      assert(tokens.every((v, i) => i === 0 || tokens[i - 1] >= v), `tokens descending ${tokens}`);
      await page.click('[data-sort="share"]');
      const share = await page.$$eval('[data-cost-shares="projects"] [data-cost-list] > [data-cost-row]', (rows) => rows.map((r) => parseFloat(r.dataset.share)));
      assert(share.every((v, i) => i === 0 || share[i - 1] >= v), `share descending ${share}`);
      await page.click('[data-sort="usd"]');
      assert(!new URL(page.url()).searchParams.has("sort"), `the default sort stays out of the URL ${page.url()}`);
    });

    await run("the chart's tooltip follows the mouse and the legend hides a series", async () => {
      await openCosts(page);
      const col = page.locator("[data-cost-col]").nth(27);
      await col.hover();
      const tip = await waitFor(() => tipShown(page), "the tooltip on hover", 4000);
      assert(tip.includes("$2.75") && tip.includes("Assistants") && /Running total/.test(tip), `tooltip ${tip}`);
      const height = () => col.locator(".dc-cost-stack").evaluate((el) => el.getBoundingClientRect().height);
      const before = await height();
      await page.click('[data-cost-legend] [data-series="assistants"]');
      assert((await page.getAttribute('[data-cost-legend] [data-series="assistants"]', "aria-pressed")) === "false", "the legend key is not off");
      const shown = await page.$$eval('.dc-cost-seg[data-series="assistants"]', (segs) => segs.filter((s) => getComputedStyle(s).display !== "none").length);
      assert(shown === 0, `${shown} assistant segments still show`);
      assert((await height()) < before - 1, "the bar did not stack again without the assistants");
      await col.hover();
      await waitFor(() => tipShown(page), "the tooltip again", 4000);
      assert(await page.locator('[data-cost-tip] [data-series="assistants"].off').count() === 1, "the hidden series is not dimmed in the tooltip");
      await page.mouse.move(2, 2);
      await waitFor(async () => !(await tipShown(page)), "the tooltip leaving with the mouse", 4000);
      await page.click('[data-cost-legend] [data-series="assistants"]');
      assert(Math.abs((await height()) - before) < 1, "the series did not come back");
    });

    await run("the chart stacks by kind or model and counts tokens", async () => {
      await Promise.all([page.waitForURL(/by=kind/), page.click('[data-cost-by="Kind"]')]);
      await waitFor(async () => /Coder/.test(await page.locator("[data-cost-legend]").innerText()), "the kind legend");
      assert(/Assistant/.test(await page.locator("[data-cost-legend]").innerText()), "no assistant in the kind legend");
      await Promise.all([page.waitForURL(/by=model/), page.click('[data-cost-by="Model"]')]);
      await waitFor(async () => /claude-opus-5-5/.test(await page.locator("[data-cost-legend]").innerText()), "the model legend");
      await Promise.all([page.waitForURL(/unit=tokens/), page.click('[data-cost-unit="Tokens"]')]);
      await waitFor(async () => (await chartTitle(page)) === "Tokens per day", "the token chart");
      const shop = await shareRow(page, "projects", SHOP);
      assert(/tokens$/.test(shop.value) && shop.alt === "$3.00", `a token row ${JSON.stringify(shop)}`);
      await openCosts(page);
    });

    await run("a phone at 360px reads every row whole and scrolls nothing sideways", async () => {
      const ctx = await browser.newContext({ ignoreHTTPSErrors: true, hasTouch: true, isMobile: true, viewport: { width: 360, height: 780 } });
      const m = await ctx.newPage();
      try {
        await L.login(m);
        await openCosts(m);
        const overflow = await m.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
        assert(overflow <= 0, `the page scrolls sideways by ${overflow}px`);
        const cut = await m.$$eval("[data-cost-label], [data-cost-legend] [data-series] .text-break, [data-cost-period-label]", (els) => els.filter((el) => {
          const style = getComputedStyle(el);
          return style.textOverflow === "ellipsis" || el.matches("[data-cost-label]") && style.whiteSpace === "nowrap" || el.scrollWidth > el.clientWidth + 1;
        }).map((el) => el.textContent.trim()));
        assert(cut.length === 0, `main labels cut: ${cut}`);
        const rows = await m.$$eval("[data-cost-shares] [data-cost-row]", (rows) => rows.filter((r) => r.getBoundingClientRect().height > 0).map((r) => {
          const item = r.querySelector(":scope > .dc-cost-item");
          const label = item.querySelector("[data-cost-label]").getBoundingClientRect();
          const value = item.querySelector("[data-cost-value]");
          const percent = item.querySelector("[data-cost-percent]");
          const v = value.getBoundingClientRect();
          const box = item.getBoundingClientRect();
          return { name: r.dataset.name, lines: label.height / parseFloat(getComputedStyle(item.querySelector("[data-cost-label]")).lineHeight),
            wrapped: value.getClientRects().length !== 1 || percent.getClientRects().length !== 1 || v.height > 30,
            right: box.right - v.right, overlap: label.right > v.left + 1 && label.top < v.bottom };
        }));
        const bad = rows.filter((r) => r.wrapped || r.overlap || r.right > 24);
        assert(bad.length === 0, `rows whose value wraps or collides: ${JSON.stringify(bad)}`);
        assert(rows.some((r) => r.name === E_TITLE && r.lines > 1.5), `the long title does not wrap: ${JSON.stringify(rows.find((r) => r.name === E_TITLE))}`);
        const tiles = await m.$$eval("[data-cost-tiles] > div", (els) => els.map((el) => Math.round(el.getBoundingClientRect().top)));
        assert(tiles[0] === tiles[1] && tiles[2] > tiles[1], `tiles not two per row ${tiles}`);
        await m.locator("[data-cost-col]").nth(27).tap();
        const tip = await waitFor(() => tipShown(m), "the tooltip on a tap", 4000);
        assert(/Show the hours of/.test(tip), `the tapped tooltip ${tip}`);
        assert(new URL(m.url()).searchParams.get("range") === "30d", "the tap drilled instead of showing the numbers");
        await Promise.all([m.waitForURL(/range=custom/), m.locator("[data-cost-tipdrill]").tap()]);
        await waitFor(async () => (await chartTitle(m)) === "Spend per hour", "the tapped link opening the day");
      } finally {
        await ctx.close();
      }
    });

    await run("new spend arrives live and keeps the filter text, the hidden series, the tooltip and the scroll", async () => {
      await openCosts(page);
      await page.evaluate(() => { window.__costsMarker = 1; });
      await page.click('[data-cost-legend] [data-series="assistants"]');
      await page.fill("[data-cost-query]", BLOG);
      await waitFor(async () => JSON.stringify(await shareLabels(page, "projects")) === JSON.stringify([BLOG]), "the text filter narrowing the rows");
      await page.locator(".dc-work-body").evaluate((el) => { el.scrollTop = 120; });
      await page.locator("[data-cost-col]").nth(29).hover();
      await waitFor(() => tipShown(page), "the tooltip before the update", 4000);
      const scroll = await page.locator(".dc-work-body").evaluate((el) => el.scrollTop);
      await page.focus("[data-cost-query]");
      fs.appendFileSync(files[B], call(B, blogPath, "claude-sonnet-5-5", 0.75, Date.now()));
      const want = "$6.00";
      await waitFor(async () => (await tile(page, "Burn rate")) === `${want}/h`, `the burn tile moving to ${want}/h`);
      await waitFor(async () => (await page.locator(".dc-status [data-cost-burn]").innerText()) === want, `the status line moving to ${want}`);
      assert(await page.evaluate(() => window.__costsMarker === 1), "the page reloaded");
      assert(await page.evaluate(() => document.activeElement?.matches("[data-cost-query]") && document.activeElement.value), "the filter field lost its text or the focus");
      assert(JSON.stringify(await shareLabels(page, "projects")) === JSON.stringify([BLOG]), `the text filter did not survive: ${await shareLabels(page, "projects")}`);
      assert((await page.getAttribute('[data-cost-legend] [data-series="assistants"]', "aria-pressed")) === "false", "the hidden series came back");
      assert((await tipShown(page)).includes("$6.00"), `the tooltip did not survive or kept old numbers: ${await tipShown(page)}`);
      const after = await page.locator(".dc-work-body").evaluate((el) => el.scrollTop);
      assert(Math.abs(after - scroll) <= 2, `the work body scrolled from ${scroll} to ${after}`);
      assert(new URL(page.url()).searchParams.get("q") === BLOG, `the text filter is not in the URL ${page.url()}`);
      await page.fill("[data-cost-query]", "");
      await page.click('[data-cost-legend] [data-series="assistants"]');
    });

    await run("claude's own total tops up what its calls do not explain, once", async () => {
      fs.appendFileSync(files[B], costLine(B, { "claude-sonnet-5-5": 2.25 }) + costLine(B, { "claude-sonnet-5-5": 2.25 }));
      await waitFor(async () => {
        await openCosts(page);
        return (await shareValue(page, "projects", BLOG)) === "$2.25";
      }, "the blog topped up to $2.25");
      const note = await page.locator("[data-cost-prices]").innerText();
      assert(/\$0\.25 of this period is what claude's own totals hold above its logged calls/.test(note), `top-up note ${note}`);
      await sleep(12000);
      await openCosts(page);
      assert((await shareValue(page, "projects", BLOG)) === "$2.25", "the total was booked twice");
    });

    await run("a line written right before a coder delete is booked", async () => {
      fs.appendFileSync(files[A], call(A, shopPath, "claude-opus-5-5", 0.5, twoDaysAgo + 120000));
      const token = await page.evaluate(() => document.querySelector('meta[name="csrf-token"]').content);
      const res = await page.request.post(`${BASE}/coders/${A}/delete`, { headers: { "X-CSRF-Token": token, Accept: "application/json" } });
      assert(res.ok(), `delete answered ${res.status()}`);
      assert(!fs.existsSync(files[A]), "the transcript is still there");
      await openCosts(page);
      const shop = await shareValue(page, "projects", SHOP);
      assert(shop === "$3.50", `shop ${shop}, want $3.50`);
      assert((await shareValue(page, "sessions", SHOP_TITLE)) === "$3.50", "the deleted coder keeps its name and money");
    });

    await run("deleting a running coder waits for it to end, no exit record comes back", async () => {
      const url = await L.createSession(page, SHOP, `cost-run-${tag}`, "claude");
      const id = url.split("/").filter(Boolean).pop();
      const transcript = path.join(CLAUDE_DIR, shopPath.replace(/[/.]/g, "-"), `${id}.jsonl`);
      await waitFor(async () => fs.existsSync(transcript), "the running coder's transcript", 15000);
      const token = await page.evaluate(() => document.querySelector('meta[name="csrf-token"]').content);
      const res = await page.request.post(`${BASE}/coders/${id}/delete`, { headers: { "X-CSRF-Token": token, Accept: "application/json" } });
      assert(res.ok(), `delete answered ${res.status()}`);
      await sleep(3000);
      assert(!fs.existsSync(transcript), "the coder wrote its exit record after the delete, the transcript came back");
    });

    await run("a transcript that disappears is no refund", async () => {
      fs.rmSync(files[B]);
      await sleep(12000);
      await openCosts(page);
      const blog = await shareValue(page, "projects", BLOG);
      assert(blog === "$2.25", `blog ${blog}, want $2.25`);
    });

    await run("a deleted project keeps its money and its name", async () => {
      await L.deleteProject(page, SHOP);
      await openCosts(page);
      const shop = await shareValue(page, "projects", SHOP);
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
      assert(/API list price equivalents, a subscription does not pay them\. Ollama models are not priced yet/.test(await text("[data-costs-settings-note]")), "the note is missing");
      const table = await text("[data-price-table]");
      const fetched = await text("[data-price-fetched]");
      assert(table === "Built into this version" && fetched === "Never" || table === "Refreshed from LiteLLM" && fetched !== "Never" && fetched !== "",
        `table ${table}, last fetch ${fetched}`);
      const next = await text("[data-price-next]");
      assert(next === "Within a minute" || /\d/.test(next), `next fetch ${next}`);
      const models = await page.$$eval("[data-price-model]", (rows) => rows.map((r) => r.dataset.priceModel));
      for (const m of ["claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-4-5", "nemotron-3-ultra"]) assert(models.includes(m), `model ${m} missing in ${models}`);
      const opus = '[data-price-model="claude-opus-5-5"]';
      assert((await text(`${opus} [data-price-output]`)) === "$20.00", "opus output price");
      for (const f of ["input", "cache-read", "cache-write-5m", "cache-write-1h", "web-search"]) {
        assert(/^\$\d/.test(await text(`${opus} [data-price-${f}]`)), `opus ${f} price`);
      }
      assert((await text('[data-price-model="claude-haiku-4-5"] [data-price-output]')) === "$5.00", "haiku output price");
      const ollama = '[data-price-model="nemotron-3-ultra"]';
      assert(await visible(page, `${ollama} [data-price-unpriced]`), "the ollama model is not marked unpriced");
      assert(await page.locator(`${ollama} [data-price-input]`).count() === 0, "the ollama model shows a price");

      await page.locator(box).uncheck();
      await Promise.all([page.waitForURL(/\/settings\/costs/), page.click('#settings-costs button[type="submit"]')]);
      await page.goto(`${BASE}/settings/costs`, { waitUntil: "domcontentloaded" });
      assert(!(await page.locator(box).isChecked()), "switching the refresh off did not stick");
      assert((await text("[data-price-next]")) === "None, the daily refresh is off", "the next fetch does not say the refresh is off");
      await openCosts(page);
      assert(/the daily refresh is off\./.test(await page.locator("[data-cost-prices]").innerText()), "the page does not say the refresh is off");
      await page.goto(`${BASE}/settings/costs`, { waitUntil: "domcontentloaded" });
      await page.locator(box).check();
      await Promise.all([page.waitForURL(/\/settings\/costs/), page.click('#settings-costs button[type="submit"]')]);
      await page.goto(`${BASE}/settings/costs`, { waitUntil: "domcontentloaded" });
      assert(await page.locator(box).isChecked(), "switching the refresh back on did not stick");
      assert((await text("[data-price-next]")) !== "None, the daily refresh is off", "the next fetch still says the refresh is off");
      await openCosts(page);
      assert(!/the daily refresh is off/.test(await page.locator("[data-cost-prices]").innerText()), "the page still says the refresh is off");
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
          form.dataset.stale = "1";
        });
        await page.click('#settings-costs button[type="submit"]');
        await page.waitForSelector("#settings-costs:not([data-stale])", { state: "visible", timeout: 8000 });
      };
      await page.goto(`${BASE}/settings/costs`, { waitUntil: "domcontentloaded" });
      assert((await page.inputValue(field)) === "3", `default ${await page.inputValue(field)}`);
      assert(await visible(page, field), "the months field is not visible");
      const help = await page.locator("[data-cost-retention-help]").innerText();
      assert(/this month included\. The default 3 keeps the current month and the two before it\. At least 2/.test(help), `help ${help}`);
      assert((await kept()) === `Kept now: ${months(3)}.`, `kept ${await kept()}`);
      await save("1");
      assert(/Keep at least 2 months\./.test(await page.locator("#settings-costs").innerText()), "one month is not refused");
      assert((await page.inputValue(field)) === "3", "a refused value was stored");
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
      assert(!(await visible(m, ".dc-status")), "the status line shows on a phone");
      await m.click('.dc-tabbar [data-ctx-area="cockpit"]');
      await m.waitForSelector("[data-cockpit-costs]", { state: "visible", timeout: 8000 });
      await Promise.all([m.waitForURL(/\/costs/), m.click("[data-cockpit-costs]")]);
      await m.waitForSelector("[data-cost-chart]", { state: "visible", timeout: 8000 });
      const overflow = await m.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
      assert(overflow <= 0, `the page scrolls sideways by ${overflow}px`);
      const active = await m.evaluate(() => document.querySelector('.dc-tabbar [data-ctx-area="cockpit"]').classList.contains("active"));
      assert(active, "the Cockpit tab is not active on the cost page");
    });
  } finally {
    await L.deleteProject(page, SHOP).catch(() => {});
    await L.deleteProject(page, BLOG).catch(() => {});
    for (const id of assistants) await deleteAssistant(page, id).catch(() => {});
    for (const f of Object.values(files)) fs.rmSync(f, { force: true });
  }
});
