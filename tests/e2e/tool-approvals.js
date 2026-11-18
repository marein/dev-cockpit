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
// @dc/prompt-view): the head "Use <name>" with the tool icon, who
// asks and "waits up to 5 min", the tool as a mono row and each key of its
// input as a code block under the key, the box "Always allow this tool", plus
// its news entry (`approval:<id>`). Approve lets the turn's tool run, Deny
// tells the turn with the cockpit's sentence, no note lands in the thread
// either way. The assistant cannot answer it over the local socket. Stopping
// the turn and deleting the assistant take the question along. The box stores
// the tool for every Claude assistant, its next call runs without a question,
// and the Approvals tab (/settings/assistant/approvals) lists it under Always
// allowed Claude tools by its own name, its MCP server and its full name, with
// a Remove that makes it ask again. On the phone Remove takes the row out in
// place, without a navigation, counts down, toasts "Tool removed." and ends in
// the empty state, which a reload keeps. The fake's INPUT:<kind> picks the
// input: a shell command with && and redirections shows as it will run, a multi line Write with its
// line breaks, a Write of 98 KB of minified JSON and one of 1 MB with their
// first 64 KiB and the cut note, markup as text that stays out of the news. RESTART_WAIT restarts the cockpit while the question
// waits, which denies the call with one sentence.
//
// Instance: the assistant shape of the README (tests/e2e/fakes ahead of the
// real CLIs on PATH, a scratch HOME) and its local API socket mounted, e.g.
// -v <state-dir>/api:/apisock with APISOCK=/apisock/s. The last check restarts
// it, point this runner at a throwaway only.

const APISOCK = process.env.APISOCK || "/apisock/s";
const DIALOG = ".swal2-popup:not(.swal2-toast)";
const TOOL = "mcp__claude_ai_Slack__slack_search_channels";
const APPROVALS = "/settings/assistant/approvals";
const DENIED = "denied: The user denied this tool call in the cockpit.";
const CUT = "Only the first 64 KiB are shown, the run takes the whole value.";

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

// ask sends the turn that calls the tool.
async function ask(page, id, prompt = `PERMIT:${TOOL}`) {
  await page.goto(`${BASE}/assistants/${id}`, { waitUntil: "domcontentloaded" });
  await page.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
  await page.waitForFunction(() => !document.querySelector("dc-assistant")?.hasAttribute("running"), null, { timeout: 60000 });
  await page.fill("[data-assistant-input]", prompt);
  await page.click("[data-assistant-send]");
}

// deny presses Deny and reads what the turn answered.
async function deny(page, tool) {
  await page.click(`${DIALOG} .swal2-deny`);
  await page.waitForSelector(DIALOG, { state: "detached", timeout: 8000 });
  const answer = await lastAnswer(page);
  assert(answer.text.startsWith(`Tool ${tool}: ${DENIED}`), `the turn answered "${answer.text.slice(0, 300)}"`);
}

async function waitQuestion(page) {
  await page.waitForSelector(`${DIALOG} [data-approval-what]`, { timeout: 20000 });
}

// lastAnswer waits for the turn to end and reads its answer.
async function lastAnswer(page, timeout = 30000) {
  await page.waitForFunction(() => {
    const nodes = document.querySelectorAll('[data-assistant-message][data-role="assistant"]');
    const last = nodes[nodes.length - 1];
    return last && last.getAttribute("data-state") !== "streaming" && !document.querySelector("dc-assistant")?.hasAttribute("running");
  }, null, { timeout });
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

// allowForGood approves a call of the tool with Always allow this tool ticked.
async function allowForGood(page, id, tool) {
  await ask(page, id, `PERMIT:${tool}`);
  await waitQuestion(page);
  await page.check(`${DIALOG} .swal2-checkbox input`);
  await page.click(`${DIALOG} .swal2-confirm`);
  await page.waitForSelector(DIALOG, { state: "detached", timeout: 8000 });
  const answer = await lastAnswer(page);
  assert(answer.text === `Tool ${tool}: allowed`, `the approved call of ${tool} answered "${answer.text}"`);
}

// allowedList reads the list the way a person sees it: the tools shown, the
// count beside the head and whether the empty state stands.
async function allowedList(page) {
  return page.evaluate(() => {
    const seen = (el) => {
      if (!el) return false;
      const style = getComputedStyle(el);
      const box = el.getBoundingClientRect();
      return style.display !== "none" && style.visibility !== "hidden" && box.width > 0 && box.height > 0;
    };
    const count = document.querySelector("[data-allowed-tools-count]");
    return {
      tools: [...document.querySelectorAll("[data-allowed-tool]")].filter(seen).map((row) => row.dataset.allowedTool),
      count: seen(count) ? count.textContent.trim() : "",
      empty: seen(document.querySelector("[data-allowed-tools-empty]")),
    };
  });
}

// watchApprovals counts the requests to the Approvals tab by method, and marks
// the list, so a navigation of any kind, pe.js's swap included, shows.
async function watchApprovals(page) {
  const seen = { GET: 0, POST: 0 };
  const count = (req) => {
    if (new URL(req.url()).pathname === APPROVALS) seen[req.method()] = (seen[req.method()] || 0) + 1;
  };
  page.on("request", count);
  await page.evaluate(() => { document.querySelector("dc-allowed-tools").dataset.e2eStay = "1"; });
  return {
    seen,
    async stayed() {
      return page.evaluate(() => document.querySelector("dc-allowed-tools")?.dataset.e2eStay === "1");
    },
    stop: () => page.off("request", count),
  };
}

async function toastSays(page, text) {
  await page.waitForFunction((want) => [...document.querySelectorAll("[data-dc-toasts] .toast.show")]
    .some((toast) => toast.textContent.includes(want)), text, { timeout: 8000 });
}

// openApprovals opens the tab and waits for the list's element.
async function openApprovals(page) {
  await page.goto(`${BASE}${APPROVALS}`, { waitUntil: "domcontentloaded" });
  assert((await L.waitUpgraded(page, ["dc-allowed-tools"])).length === 0, "dc-allowed-tools did not upgrade");
}

L.runFeature("TOOL APPROVALS", async ({ page, run, mobilePage }) => {
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
    assert(what === "Use slack_search_channels", `the dialog's head reads "${what}"`);
    assert(await page.locator(`${DIALOG} .dc-ask-icon .ti-tool`).isVisible(), "the head does not wear the tool icon");
    // The first prompt names the assistant, so the name is the prompt's start.
    const asker = (await page.textContent(`${DIALOG} [data-approval-asker]`)).replace(/\s+/g, " ").trim();
    assert(asker === `PERMIT:${TOOL}`.slice(0, 32) + "… " + assistantID.slice(0, 8), `the dialog names who asks as "${asker}"`);
    const meta = (await page.textContent(`${DIALOG} .dc-ask-meta`)).replace(/\s+/g, " ");
    assert(meta.includes("waits up to 5 min"), `the head's line reads "${meta}"`);
    const rows = await dialogRows(page);
    const want = [["Tool", TOOL], ["query", "general"], ["limit", "5"]];
    assert(JSON.stringify(rows) === JSON.stringify(want), `the dialog's rows read ${JSON.stringify(rows)}`);
    assert(await page.locator(`${DIALOG} [data-approval-detail="Tool"] .font-monospace`).isVisible(), "the tool is no mono row");
    assert(await page.locator(`${DIALOG} [data-approval-detail="query"] pre`).isVisible(), "the input is no code block");
    assert((await page.textContent(`${DIALOG} [data-approval-detail="query"] .dc-ask-code-head`)).trim() === "query", "the box does not name its key");
    assert(await page.locator(`${DIALOG} [data-approval-cut]`).count() === 0, "a whole input says it was cut");
    const remember = (await page.locator(`${DIALOG} .swal2-checkbox`).textContent()).trim();
    assert(remember === "Always allow this tool", `the box reads "${remember}"`);
    assert(!(await page.locator(`${DIALOG} .swal2-checkbox input`).isChecked()), "the box starts ticked");
    assert(await page.locator(`${DIALOG} [data-gitprompt-command]`).count() === 0, "a tool call shows a command block");
    const entry = (await notifications(page)).find((n) => !before.includes(n.id) && (n.targetId || "").startsWith("approval:"));
    assert(entry, "the question is no news");
    assert(entry.title === "Assistant asks approval." && (entry.detail || entry.body || "").includes("Use slack_search_channels"), `the entry reads "${entry.title}" "${entry.detail || entry.body}"`);
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
    await deny(page, TOOL);
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
    await allowForGood(page, assistantID, TOOL);
    const other = await newAssistant(page);
    await ask(page, other);
    const answer = await lastAnswer(page);
    assert(answer.text === `Tool ${TOOL}: allowed`, `another assistant's call answered "${answer.text}"`);
    assert(await page.locator(DIALOG).count() === 0, "an allowed tool still asked");
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    const gone = await page.request.post(`${BASE}/assistants/${other}`, { form: { csrf_token: await csrfOf(page), form: "delete" }, headers: { Accept: "application/json" } });
    assert(gone.status() === 200, `the other assistant's delete answered ${gone.status()}`);
  });

  await run("the Approvals tab lists the tool under its heading by its name, its server and its full name", async () => {
    await openApprovals(page);
    const row = `[data-allowed-tool="${TOOL}"]`;
    assert(await shown(page, row), "the tab does not show the tool");
    assert(!(await shown(page, "[data-allowed-tools-empty]")), "the tab says no tool");
    const named = await page.evaluate((sel) => {
      const el = document.querySelector(sel);
      const text = (part) => (el.querySelector(`[data-allowed-tool-${part}]`)?.textContent || "").trim();
      const section = document.querySelector("dc-allowed-tools");
      return {
        heading: section.querySelector("h3").textContent.trim(),
        hint: section.querySelector("[data-allowed-tools-hint]").textContent.trim(),
        name: text("name"), server: text("server"), full: text("full"),
        count: section.querySelector("[data-allowed-tools-count]").textContent.trim(),
        remove: el.querySelector('button[type="submit"]').getAttribute("aria-label"),
      };
    }, row);
    assert(named.heading === "Always allowed Claude tools", `the heading reads "${named.heading}"`);
    const hint = "When a Claude assistant wants a tool that needs approval, like a Slack connector, it asks you first. The tools below run without asking. OpenCode and Copilot assistants never ask.";
    assert(named.hint === hint, `the description reads "${named.hint}"`);
    assert(named.name === "slack_search_channels" && named.server === "claude_ai_Slack" && named.full === TOOL, `the row reads ${JSON.stringify(named)}`);
    assert(named.count === "1" && named.remove === `Remove ${TOOL}`, `the count and the Remove read ${JSON.stringify(named)}`);
  });

  await run("on the phone Remove takes two tools out in place, counts down and ends in the empty state", async () => {
    await allowForGood(page, assistantID, "WebFetch");
    const phone = await mobilePage();
    await dismissUpdate(phone);
    await openApprovals(phone);
    const before = await allowedList(phone);
    assert(JSON.stringify(before.tools) === JSON.stringify(["WebFetch", TOOL]) && before.count === "2" && !before.empty, `the list reads ${JSON.stringify(before)}`);
    const watch = await watchApprovals(phone);
    await phone.locator('[data-allowed-tool="WebFetch"] button[type="submit"]').tap();
    await phone.waitForSelector('[data-allowed-tool="WebFetch"]', { state: "detached", timeout: 8000 });
    await toastSays(phone, "Tool removed.");
    const one = await allowedList(phone);
    assert(JSON.stringify(one.tools) === JSON.stringify([TOOL]) && one.count === "1" && !one.empty, `after one remove the list reads ${JSON.stringify(one)}`);
    // The row slid up under the finger, a tap on it within 350 ms is not taken.
    await sleep(400);
    await phone.locator(`[data-allowed-tool="${TOOL}"] button[type="submit"]`).tap();
    await phone.waitForSelector("[data-allowed-tool]", { state: "detached", timeout: 8000 });
    watch.stop();
    const none = await allowedList(phone);
    assert(none.tools.length === 0 && none.count === "" && none.empty, `after the last remove the list reads ${JSON.stringify(none)}`);
    assert(await watch.stayed() && watch.seen.POST === 2 && watch.seen.GET === 0 && new URL(phone.url()).pathname === APPROVALS, `the removes navigated: ${JSON.stringify(watch.seen)} ${phone.url()}`);
  });

  await run("after a reload the removed tools stay gone and a removed tool asks again", async () => {
    const phone = await mobilePage();
    await openApprovals(phone);
    const list = await allowedList(phone);
    assert(list.tools.length === 0 && list.count === "" && list.empty, `the reloaded list reads ${JSON.stringify(list)}`);
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    const save = await page.request.post(`${BASE}${APPROVALS}`, { form: { csrf_token: await csrfOf(page), "forget-tool": TOOL } });
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

  let inputs = "";

  await run("a shell command shows as it will run, && and redirections and all", async () => {
    inputs = await newAssistant(page);
    await ask(page, inputs, "PERMIT:Bash INPUT:shell");
    await waitQuestion(page);
    const rows = await dialogRows(page);
    const want = [["Tool", "Bash"], ["command", "cd /tmp && ls > out.txt 2>&1 <done>"], ["description", "List <@user> files"]];
    assert(JSON.stringify(rows) === JSON.stringify(want), `the dialog's rows read ${JSON.stringify(rows)}`);
    await deny(page, "Bash");
  });

  await run("a multi line Write shows its content with its line breaks under its key", async () => {
    await ask(page, inputs, "PERMIT:Write INPUT:write");
    await waitQuestion(page);
    const rows = await dialogRows(page);
    const want = [["Tool", "Write"], ["file_path", "/tmp/notes.md"], ["content", "# Notes\n\nfirst line && more\n<b>second</b> line"]];
    assert(JSON.stringify(rows) === JSON.stringify(want), `the dialog's rows read ${JSON.stringify(rows)}`);
    const box = await page.locator(`${DIALOG} [data-approval-detail="content"]`).evaluate((el) => ({
      head: el.querySelector(".dc-ask-code-head").textContent.trim(),
      lines: el.querySelector("pre").innerText.split("\n").length,
      height: el.querySelector("pre").getBoundingClientRect().height,
      line: parseFloat(getComputedStyle(el.querySelector("pre")).lineHeight),
    }));
    assert(box.head === "content" && box.lines >= 4 && box.height >= 3 * box.line, `the content box reads ${JSON.stringify(box)}`);
    await deny(page, "Write");
  });

  for (const [kind, what] of [["dense", "98 KB of minified JSON"], ["huge", "1 MB"]]) {
    await run(`a Write of ${what} asks with its first 64 KiB and the cut note`, async () => {
      await ask(page, inputs, `PERMIT:Write INPUT:${kind}`);
      await waitQuestion(page);
      const box = await page.locator(`${DIALOG} [data-approval-detail="content"]`).evaluate((el) => ({
        bytes: new TextEncoder().encode(el.querySelector("pre").textContent).length,
        cut: el.querySelector("[data-approval-cut]")?.textContent.trim() || "",
      }));
      assert(box.bytes === 64 << 10 && box.cut === CUT, `the content box shows ${box.bytes} bytes with the note "${box.cut}"`);
      assert(await shown(page, `${DIALOG} [data-approval-detail="content"] [data-approval-cut]`), "the cut note is not in view");
      assert(await page.locator(`${DIALOG} [data-approval-detail="file_path"] [data-approval-cut]`).count() === 0, "the whole path says it was cut");
      await deny(page, "Write");
    });
  }

  await run("markup in an input shows as text in the dialog and stays out of the news", async () => {
    const before = (await notifications(page)).map((n) => n.id);
    await ask(page, inputs, "PERMIT:Bash INPUT:markup");
    await waitQuestion(page);
    const rows = await dialogRows(page);
    const want = [["Tool", "Bash"], ["command", "<script>window.__dcInjected = 1</script>"], ["description", '<img src=x onerror="window.__dcInjected = 2">']];
    assert(JSON.stringify(rows) === JSON.stringify(want), `the dialog's rows read ${JSON.stringify(rows)}`);
    let entry;
    await waitFor(async () => {
      entry = (await notifications(page)).find((n) => !before.includes(n.id) && (n.targetId || "").startsWith("approval:"));
      return entry;
    }, "the question's news");
    const said = `${entry.title} ${entry.detail || entry.body || ""}`;
    assert(entry.title === "Assistant asks approval." && said.includes("Use Bash") && !/[<>]|script|onerror/.test(said), `the news reads "${said}"`);
    await sleep(500);
    const injected = await page.evaluate(() => ({ nodes: document.querySelectorAll("[data-approval-details] script, [data-approval-details] img, .toast script, .toast img").length, ran: window.__dcInjected }));
    assert(injected.nodes === 0 && injected.ran === undefined, `the markup came alive: ${JSON.stringify(injected)}`);
    await deny(page, "Bash");
  });

  // The page reads the end over its stream, which WebKit may only open again
  // once the 45s of silence passed, so this waits as long as assistant.js does.
  await run("a restart of the cockpit while the question waits denies the call with one sentence", async () => {
    await ask(page, inputs, "PERMIT:Bash INPUT:shell RESTART_WAIT");
    await waitQuestion(page);
    const answer = await lastAnswer(page, 90000);
    const sentence = "The cockpit restarted or stopped answering before the user decided, so this tool call was denied. Do not call it again in this turn; tell the user it needs their approval.";
    assert(answer.text === `Tool Bash: denied: ${sentence}`, `the turn answered "${answer.text}"`);
    await page.waitForSelector(DIALOG, { state: "detached", timeout: 60000 });
    await waitFor(async () => (await (await page.request.get(`${BASE}/git/prompt`, { headers: { Accept: "application/json" } })).json()).questions.length === 0, "the question to go");
    const gone = await page.request.post(`${BASE}/assistants/${inputs}`, { form: { csrf_token: await csrfOf(page), form: "delete" }, headers: { Accept: "application/json" } });
    assert(gone.status() === 200, `the delete answered ${gone.status()}`);
  });
});
