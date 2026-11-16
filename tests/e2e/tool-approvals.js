const http = require("http");
const L = require("./lib");
const { assert, BASE, sleep, dismissUpdate } = L;

// Tool approvals of an assistant's turn: a claude turn (`claude -p`) cannot
// show a permission dialog, so it hands every call that would ask, a connector
// tool the organization wants approved first among them, to the cockpit's tool
// approval server (`tool-approval-serve` behind the turn's wrapper, configured
// inline with --mcp-config and named with --permission-prompt-tool). The fake
// claude does what the real one does for a prompt with PERMIT:<tool>: it starts
// that server, calls its tool and answers what came back. The question is the
// app-wide approval dialog (@dc/gitprompt's second kind in the look of
// @dc/prompt-view): the head "Use the tool <name>" with the tool icon, who
// asks and "waits up to 5 min", the tool as a mono row and its input as a
// code block, the box "Always allow this tool", plus its news entry
// (`approval:<id>`). Approve lets the turn's tool run, Deny tells the turn
// with the cockpit's sentence, no note lands in the thread either way. The
// assistant cannot answer it over the local socket. Stopping the turn and
// deleting the assistant take the question along. The box stores the tool for
// every assistant, its next call runs without a question, and the Approvals
// tab (/settings/assistant/approvals) lists it under Always allowed tools
// with a Remove that makes it ask again.
//
// Instance: the assistant shape of the README (tests/e2e/fakes ahead of the
// real CLIs on PATH, a scratch HOME) and its local API socket mounted, e.g.
// -v <state-dir>/api:/apisock with APISOCK=/apisock/s.

const APISOCK = process.env.APISOCK || "/apisock/s";
const DIALOG = ".swal2-popup:not(.swal2-toast)";
const TOOL = "mcp__claude_ai_Slack__slack_search_channels";
const INPUT = '{\n  "query": "general",\n  "limit": 5\n}';

// overSocket posts JSON over the local socket as the assistant, the way a
// command of its turn would.
function overSocket(id, path, body) {
  return new Promise((resolve, reject) => {
    const data = JSON.stringify(body);
    const req = http.request({
      socketPath: APISOCK,
      path,
      method: "POST",
      headers: { "X-Dev-Cockpit-Assistant": id, "Content-Type": "application/json", "Content-Length": Buffer.byteLength(data), Accept: "application/json" },
    }, (res) => {
      let raw = "";
      res.on("data", (chunk) => { raw += chunk; });
      res.on("end", () => resolve({ status: res.statusCode, raw }));
    });
    req.on("error", reject);
    req.end(data);
  });
}

async function csrfOf(page) {
  return page.evaluate(() => document.querySelector('meta[name="csrf-token"]')?.content || "");
}

async function notifications(page) {
  const res = await page.request.get(`${BASE}/notifications`, { headers: { Accept: "application/json" } });
  return (await res.json()).notifications || [];
}

async function waitFor(fn, what, tries = 60) {
  for (let i = 0; i < tries; i += 1) {
    if (await fn()) return;
    await sleep(250);
  }
  throw new Error(`timed out waiting for ${what}`);
}

// shown reads real visibility, not the attribute alone.
async function shown(page, selector) {
  return page.evaluate((sel) => {
    const node = document.querySelector(sel);
    if (!node) return false;
    const style = getComputedStyle(node);
    const box = node.getBoundingClientRect();
    return style.display !== "none" && style.visibility !== "hidden" && box.width > 0 && box.height > 0;
  }, selector);
}

// dialogRows reads what the approval dialog shows as label and value pairs,
// a row and a code block alike.
async function dialogRows(page) {
  return page.locator(`${DIALOG} [data-approval-details]`).evaluate((root) => [...root.querySelectorAll("[data-approval-detail]")]
    .map((el) => [el.dataset.approvalDetail, (el.querySelector("[data-approval-value]") || el).textContent.trim()]));
}

// ask sends the turn that calls the tool and waits for the question.
async function ask(page, id) {
  await page.goto(`${BASE}/assistants/${id}`, { waitUntil: "domcontentloaded" });
  await page.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
  await page.waitForFunction(() => !document.querySelector("dc-assistant")?.hasAttribute("running"), null, { timeout: 60000 });
  await page.fill("[data-assistant-input]", `PERMIT:${TOOL}`);
  await page.click("[data-assistant-send]");
}

async function waitQuestion(page) {
  await page.waitForSelector(`${DIALOG} [data-approval-what]`, { timeout: 20000 });
}

// lastAnswer waits for the turn to end and reads its answer.
async function lastAnswer(page) {
  await page.waitForFunction(() => {
    const nodes = document.querySelectorAll('[data-assistant-message][data-role="assistant"]');
    const last = nodes[nodes.length - 1];
    return last && last.getAttribute("data-state") !== "streaming" && !document.querySelector("dc-assistant")?.hasAttribute("running");
  }, null, { timeout: 30000 });
  return page.evaluate(() => {
    const nodes = document.querySelectorAll('[data-assistant-message][data-role="assistant"]');
    const last = nodes[nodes.length - 1];
    return { text: (last.querySelector("[data-assistant-text]")?.innerText || "").trim(), state: last.getAttribute("data-state") };
  });
}

async function newAssistant(page) {
  await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
  const created = await page.request.post(`${BASE}/assistants/new`, { form: { csrf_token: await csrfOf(page), form: "new", coder: "claude" }, headers: { Accept: "application/json" } });
  const id = (await created.json().catch(() => ({}))).id || "";
  assert(id, `no assistant for the checks: ${created.status()}`);
  return id;
}

L.runFeature("TOOL APPROVALS", async ({ page, run }) => {
  let assistantID = "";

  await run("an assistant is made for the checks", async () => {
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    assistantID = await newAssistant(page);
    return assistantID;
  });

  await run("a tool call the turn would stop on asks in the app-wide dialog and rings", async () => {
    const before = (await notifications(page)).map((n) => n.id);
    await ask(page, assistantID);
    await waitQuestion(page);
    assert(await page.locator(`${DIALOG} .swal2-title`).isHidden(), "the dialog carries a title above its head");
    const what = (await page.textContent(`${DIALOG} [data-approval-what]`)).trim();
    assert(what === "Use the tool slack_search_channels", `the dialog's head reads "${what}"`);
    assert(await page.locator(`${DIALOG} .dc-ask-icon .ti-tool`).isVisible(), "the head does not wear the tool icon");
    // The first prompt names the assistant, so the name is the prompt's start.
    const asker = (await page.textContent(`${DIALOG} [data-approval-asker]`)).replace(/\s+/g, " ").trim();
    assert(asker === `PERMIT:${TOOL}`.slice(0, 32) + "… " + assistantID.slice(0, 8), `the dialog names who asks as "${asker}"`);
    const meta = (await page.textContent(`${DIALOG} .dc-ask-meta`)).replace(/\s+/g, " ");
    assert(meta.includes("waits up to 5 min"), `the head's line reads "${meta}"`);
    const rows = await dialogRows(page);
    const want = [["Tool", TOOL], ["Input", INPUT]];
    assert(JSON.stringify(rows) === JSON.stringify(want), `the dialog's rows read ${JSON.stringify(rows)}`);
    assert(await page.locator(`${DIALOG} [data-approval-detail="Tool"] .font-monospace`).isVisible(), "the tool is no mono row");
    assert(await page.locator(`${DIALOG} [data-approval-detail="Input"] pre`).isVisible(), "the input is no code block");
    assert(await page.locator(`${DIALOG} [data-approval-cut]`).count() === 0, "a whole input says it was cut");
    const remember = (await page.locator(`${DIALOG} .swal2-checkbox`).textContent()).trim();
    assert(remember === "Always allow this tool", `the box reads "${remember}"`);
    assert(!(await page.locator(`${DIALOG} .swal2-checkbox input`).isChecked()), "the box starts ticked");
    assert(await page.locator(`${DIALOG} [data-gitprompt-command]`).count() === 0, "a tool call shows a command block");
    const entry = (await notifications(page)).find((n) => !before.includes(n.id) && (n.targetId || "").startsWith("approval:"));
    assert(entry, "the question is no news");
    assert(entry.title === "Assistant asks approval." && (entry.detail || entry.body || "").includes("Use the tool slack_search_channels"), `the entry reads "${entry.title}" "${entry.detail || entry.body}"`);
    return entry.targetId;
  });

  await run("the assistant cannot answer its own question over the socket", async () => {
    const question = (await (await page.request.get(`${BASE}/git/prompt`, { headers: { Accept: "application/json" } })).json()).questions[0];
    const answer = await overSocket(assistantID, "/git/prompt", { key: question.key, id: question.id, approve: true });
    assert(answer.status === 403, `the socket answered ${answer.status}: ${answer.raw}`);
    await sleep(500);
    assert(await shown(page, `${DIALOG} [data-approval-what]`), "the question went away");
  });

  await run("Approve runs the tool and the turn goes on, no note in the thread", async () => {
    await page.click(`${DIALOG} .swal2-confirm`);
    await page.waitForSelector(DIALOG, { state: "detached", timeout: 8000 });
    const answer = await lastAnswer(page);
    assert(answer.text === `Tool ${TOOL}: allowed`, `the turn answered "${answer.text}"`);
    assert(await page.locator('[data-assistant-note="approval"]').count() === 0, "an approval note landed in the thread");
  });

  await run("Deny tells the turn the user denied it", async () => {
    await ask(page, assistantID);
    await waitQuestion(page);
    await page.click(`${DIALOG} .swal2-deny`);
    await page.waitForSelector(DIALOG, { state: "detached", timeout: 8000 });
    const answer = await lastAnswer(page);
    assert(answer.text.startsWith(`Tool ${TOOL}: denied: The user denied this tool call in the cockpit.`), `the turn answered "${answer.text}"`);
  });

  await run("stopping the turn takes its question along", async () => {
    await ask(page, assistantID);
    await waitQuestion(page);
    const stopped = await page.request.post(`${BASE}/assistants/${assistantID}`, { form: { csrf_token: await csrfOf(page), form: "cancel" }, headers: { Accept: "application/json" } });
    assert(stopped.status() < 400, `the stop answered ${stopped.status()}`);
    await page.waitForSelector(DIALOG, { state: "detached", timeout: 15000 });
    await waitFor(async () => (await (await page.request.get(`${BASE}/git/prompt`, { headers: { Accept: "application/json" } })).json()).questions.length === 0, "the question to go");
    const answer = await lastAnswer(page);
    assert(answer.state === "cancelled", `the stopped turn reads ${answer.state}`);
  });

  await run("Always allow this tool stores it and its next call runs without asking", async () => {
    await ask(page, assistantID);
    await waitQuestion(page);
    await page.check(`${DIALOG} .swal2-checkbox input`);
    await page.click(`${DIALOG} .swal2-confirm`);
    await page.waitForSelector(DIALOG, { state: "detached", timeout: 8000 });
    assert((await lastAnswer(page)).text === `Tool ${TOOL}: allowed`, "the approved call did not run");
    const other = await newAssistant(page);
    await ask(page, other);
    const answer = await lastAnswer(page);
    assert(answer.text === `Tool ${TOOL}: allowed`, `another assistant's call answered "${answer.text}"`);
    assert(await page.locator(DIALOG).count() === 0, "an allowed tool still asked");
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    const gone = await page.request.post(`${BASE}/assistants/${other}`, { form: { csrf_token: await csrfOf(page), form: "delete" }, headers: { Accept: "application/json" } });
    assert(gone.status() === 200, `the other assistant's delete answered ${gone.status()}`);
  });

  await run("the Approvals tab lists the tool and Remove makes it ask again", async () => {
    await page.goto(`${BASE}/settings/assistant/approvals`, { waitUntil: "domcontentloaded" });
    const row = `[data-allowed-tool="${TOOL}"]`;
    assert(await shown(page, row), "the tab does not show the tool");
    assert(!(await shown(page, "[data-allowed-tools-empty]")), "the tab says no tool");
    assert((await page.textContent(`${row} code`)).trim() === TOOL, "the row does not name the tool");
    await Promise.all([page.waitForURL(/\/settings\/assistant\/approvals/), page.click(`${row} button[type="submit"]`)]);
    await page.waitForSelector("[data-allowed-tools-empty]", { timeout: 8000 });
    assert(!(await shown(page, row)), "the removed tool still shows");
    assert(await shown(page, "[data-allowed-tools-empty]"), "the empty list says nothing");
    const save = await page.request.post(`${BASE}/settings/assistant/approvals`, { form: { csrf_token: await csrfOf(page), "forget-tool": TOOL } });
    assert(save.status() < 400, `a second remove answered ${save.status()}`);
    await ask(page, assistantID);
    await waitQuestion(page);
    return "asks again";
  });

  await run("deleting the assistant takes its question along", async () => {
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await waitQuestion(page);
    const gone = await page.request.post(`${BASE}/assistants/${assistantID}`, { form: { csrf_token: await csrfOf(page), form: "delete" }, headers: { Accept: "application/json" } });
    assert(gone.status() === 200, `the delete answered ${gone.status()}`);
    await page.waitForSelector(DIALOG, { state: "detached", timeout: 15000 });
    await waitFor(async () => (await (await page.request.get(`${BASE}/git/prompt`, { headers: { Accept: "application/json" } })).json()).questions.length === 0, "the question to go");
    await sleep(500);
    assert(await page.locator(DIALOG).count() === 0, "the dialog still stands");
  });
});
