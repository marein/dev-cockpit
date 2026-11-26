const fs = require("fs");
const path = require("path");
const L = require("./lib");
const { assert, BASE, sleep, dismissUpdate } = L;

// An assistant on codex, chromium, the chat on a 390px phone, against the fake
// codex of tests/e2e/fakes. A turn is `codex exec resume --json` against a
// session the cockpit made through codex's app server (thread/start, named
// with thread/name/set), which carries the assistant's id in its thread
// source: the rollout under CODEX_HOME is where the checks read that.
//
// What stands here: the first answer and the second turn in the same codex
// session, the turn's argv (no sandbox, the hook trust flag, the PreToolUse
// hook over MCP tools), an answer in blocks with a tool line while it runs,
// a file link, markdown, an ask block and a picture rendering, records no
// version knows read past, a stopped turn keeping its part, a failed turn
// recovering, a turn outliving a restart of the cockpit, the ring at codex's
// own reading, the model picks of the ring reaching the argv for a chat, a
// check and a trigger, an unknown model named, a steered job's check giving
// its verdict, a trigger's reaction running, the tool approval (Approve,
// Deny, Always allow this tool, a tool that is none of an MCP server running
// unasked), a project delete a turn asks for waiting for the user (Deny
// keeps the project, Approve removes it), the cockpit's own commands from
// inside a turn, and the delete taking the
// codex session along.
//
// Instance: the assistant shape of the README (tests/e2e/fakes ahead of the
// real CLIs on PATH, a scratch HOME and CODEX_HOME), that CODEX_HOME mounted
// into the runner at the same path and named in CODEX_HOME, and the claude
// inbox mounted like wake.js mounts it (NOTIFY_DIR). One check restarts the
// instance, point this runner at a throwaway only.

const CODEX_HOME = process.env.CODEX_HOME || "";
const NOTIFY_DIR = process.env.NOTIFY_DIR || "";
const DIALOG = ".swal2-popup:not(.swal2-toast)";
const TOOL = "mcp__slack__search_channels";
const KEPT = "zzcodexkept";
const GONE = "zzcodexgone";
const ANSWERS = '[data-assistant-message][data-role="assistant"]';

function ring(sessionID) {
  const name = `${Date.now()}-${Math.floor(Math.random() * 1e6)}`;
  fs.writeFileSync(path.join(NOTIFY_DIR, `${name}.tmp`), JSON.stringify({ session_id: sessionID, hook_event_name: "Stop" }));
  fs.renameSync(path.join(NOTIFY_DIR, `${name}.tmp`), path.join(NOTIFY_DIR, `${name}.json`));
}

// rollouts reads the sessions the fake codex holds, by the first line of each.
function rollouts() {
  const out = [];
  const walk = (dir) => {
    for (const entry of fs.existsSync(dir) ? fs.readdirSync(dir, { withFileTypes: true }) : []) {
      const full = path.join(dir, entry.name);
      if (entry.isDirectory()) walk(full);
      else if (/^rollout-.*\.jsonl$/.test(entry.name)) {
        const meta = JSON.parse(fs.readFileSync(full, "utf8").split("\n")[0]).payload;
        out.push({ id: meta.id, source: meta.thread_source, cwd: meta.cwd, file: full });
      }
    }
  };
  walk(path.join(CODEX_HOME, "sessions"));
  return out;
}

function sessionOf(assistant) {
  return rollouts().find((r) => r.source === `dev-cockpit:${assistant}`);
}

async function postForm(page, url, fields) {
  return page.evaluate(async ([to, values]) => {
    const body = new URLSearchParams(values);
    body.set("csrf_token", document.querySelector('meta[name="csrf-token"]').content);
    const res = await fetch(to, { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded", Accept: "application/json" }, body: body.toString() });
    return { status: res.status, body: await res.json().catch(() => ({})) };
  }, [url, fields]);
}

async function newAssistant(page) {
  const made = await postForm(page, "/assistants/new", { form: "new", coder: "codex" });
  assert(made.status === 200 && made.body.id, `no codex assistant: ${made.status} ${JSON.stringify(made.body)}`);
  return made.body.id;
}

async function open(page, id) {
  await page.goto(`${BASE}/assistants/${id}`, { waitUntil: "domcontentloaded" });
  await dismissUpdate(page);
  await page.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
}

// idle waits for the turn to be over: the surface's running mark gone and
// the last answer settled, which follow each other by a moment.
async function idle(page, timeout = 60000) {
  await page.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
  await page.waitForFunction((sel) => {
    const nodes = document.querySelectorAll(sel);
    const settled = !nodes.length || nodes[nodes.length - 1].getAttribute("data-state") !== "streaming";
    return settled && !document.querySelector("dc-assistant")?.hasAttribute("running");
  }, ANSWERS, { timeout });
}

// send types the message and taps Send, the way a phone does, and waits for
// the answer's bubble to appear.
async function send(page, text) {
  await idle(page);
  const before = await page.locator(ANSWERS).count();
  await page.fill("[data-assistant-input]", text);
  await page.tap("[data-assistant-send]");
  await page.waitForFunction(([sel, count]) => document.querySelectorAll(sel).length > count, [ANSWERS, before], { timeout: 20000 });
}

async function last(page) {
  return page.evaluate((sel) => {
    const nodes = document.querySelectorAll(sel);
    const bubble = nodes[nodes.length - 1];
    return { text: (bubble.querySelector("[data-assistant-text]")?.innerText || "").trim(), state: bubble.getAttribute("data-state") };
  }, ANSWERS);
}

// answer sends one message and reads the settled answer.
async function answer(page, text, timeout = 60000) {
  await send(page, text);
  await idle(page, timeout);
  return last(page);
}

async function ringFill(page) {
  return page.locator("dc-assistant [data-assistant-ring-fill]").first().getAttribute("stroke-dasharray");
}

async function waitFor(fn, what, tries = 120) {
  for (let i = 0; i < tries; i += 1) {
    const got = await fn();
    if (got) return got;
    await sleep(250);
  }
  throw new Error(`timed out waiting for ${what}`);
}

L.runFeature("ASSISTANT CODEX", async ({ page, run, mobilePage }) => {
  assert(CODEX_HOME && fs.existsSync(CODEX_HOME), `CODEX_HOME ${CODEX_HOME || "(unset)"} is not mounted`);
  assert(NOTIFY_DIR && fs.existsSync(NOTIFY_DIR), `NOTIFY_DIR ${NOTIFY_DIR || "(unset)"} is not mounted`);

  const project = "zzcodexassistant";
  let phone = null;
  let id = "";
  let native = "";
  let coder = "";

  await run("a new assistant on codex answers and wears codex's glyph in the ring", async () => {
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    id = await newAssistant(page);
    assert(!sessionOf(id), "an assistant nobody wrote to has a codex session already");
    phone = await mobilePage();
    await open(phone, id);
    assert((await phone.locator("dc-assistant").getAttribute("data-assistant-coder")) === "codex", "the assistant runs on another coder");
    const glyph = await phone.evaluate(() => {
      const icon = document.querySelector("dc-assistant .assistant-composer [data-assistant-ring-icon]");
      const button = icon.closest("[data-assistant-new-label]").getBoundingClientRect();
      const box = icon.getBoundingClientRect();
      return { tag: icon.tagName.toLowerCase(), glyph: icon.querySelector("i")?.className || "", text: icon.textContent.trim(),
        dx: Math.abs(box.x + box.width / 2 - button.x - button.width / 2), dy: Math.abs(box.y + box.height / 2 - button.y - button.height / 2) };
    });
    assert(glyph.tag === "span" && glyph.glyph.includes("ti-brand-openai") && glyph.text === "", `the ring wears ${JSON.stringify(glyph)}`);
    assert(glyph.dx <= 0.5 && glyph.dy <= 0.5, `the glyph is off the ring's center: ${JSON.stringify(glyph)}`);
    const first = await answer(phone, "TURNS first");
    assert(first.state === "complete" && /^turn 1 of session /.test(first.text), `the first answer reads "${first.text}" (${first.state})`);
    native = first.text.replace("turn 1 of session ", "");
    return native;
  });

  await run("the session is codex's own, tied to the assistant and named, and the next message continues it", async () => {
    const session = sessionOf(id);
    assert(session && session.id === native && native !== id, `the assistant's session is ${JSON.stringify(session)}, the turn ran in ${native}`);
    assert(session.cwd.endsWith(`/assistant/instances/${id}/workspace`), `the session runs in ${session.cwd}`);
    const names = fs.readFileSync(path.join(CODEX_HOME, "session_index.jsonl"), "utf8").trim().split("\n").map((l) => JSON.parse(l)).filter((n) => n.id === native);
    assert(names.length === 1 && names[0].thread_name, `the session's names: ${JSON.stringify(names)}`);
    const second = await answer(phone, "TURNS second");
    assert(second.text === `turn 2 of session ${native}`, `the second answer reads "${second.text}"`);
    assert(rollouts().filter((r) => r.source === `dev-cockpit:${id}`).length === 1, "a second session was made");
    return names[0].thread_name;
  });

  await run("the turn runs without codex's sandbox, with the cockpit's hook in front of MCP tools", async () => {
    const run1 = await waitFor(() => {
      const dir = path.join(CODEX_HOME, "argv");
      return fs.existsSync(dir) && fs.readdirSync(dir).length ? JSON.parse(fs.readFileSync(path.join(dir, fs.readdirSync(dir).sort().pop()), "utf8")) : null;
    }, "the fake's note of its argv", 20);
    const hook = run1[run1.indexOf("-c") + 1];
    assert(run1.slice(0, 2).join(" ") === "exec resume" && run1.includes("--json") && run1.includes("--skip-git-repo-check")
      && run1.includes("--dangerously-bypass-approvals-and-sandbox") && run1.includes("--dangerously-bypass-hook-trust"), `argv = ${JSON.stringify(run1)}`);
    assert(/^hooks\.PreToolUse=\[\{matcher="\^mcp__",hooks=\[\{type="command",command=".*\/workspace\/cockpit' 'tool-approval-ask'",timeout=420\}\]\}\]$/.test(hook), `the hook override reads ${hook}`);
    assert(JSON.stringify(run1.slice(-3)) === JSON.stringify(["--", native, "TURNS second"]), `the argv ends ${JSON.stringify(run1.slice(-3))}`);
    assert(!run1.some((arg) => arg.startsWith("--model")), "a turn without a pick names a model");
  });

  await run("an answer arrives block by block with the tool it runs, and Stop keeps what arrived", async () => {
    await send(phone, "SLOW TOOL please");
    await phone.waitForSelector("[data-assistant-cancel]:not(.d-none)", { timeout: 10000 });
    await phone.waitForFunction((sel) => {
      const nodes = document.querySelectorAll(sel);
      return nodes.length && nodes[nodes.length - 1].innerText.includes("still working");
    }, ANSWERS, { timeout: 15000 });
    const tool = await waitFor(() => phone.evaluate((sel) => {
      const nodes = document.querySelectorAll(sel);
      const line = nodes[nodes.length - 1].querySelector("[data-assistant-tool]");
      if (!line || getComputedStyle(line).display === "none" || line.getBoundingClientRect().height === 0) return "";
      return line.querySelector("[data-assistant-tool-name]").textContent.trim();
    }, ANSWERS), "the tool line", 40);
    assert(tool === "shell", `the tool line names "${tool}"`);
    assert(await phone.locator(`${ANSWERS} h2`).last().innerText() === "Working", "the block that arrived is not rendered while the turn runs");
    await phone.tap("[data-assistant-cancel]");
    await idle(phone);
    const stopped = await last(phone);
    assert(stopped.state === "cancelled" && stopped.text.includes("still working"), `the stopped answer: ${JSON.stringify(stopped)}`);
    const next = await answer(phone, "MAGIC after the stop");
    assert(next.text === "AIRPORT" && next.state === "complete", `the turn after a stop: ${JSON.stringify(next)}`);
  });

  await run("two blocks around a tool stand as two paragraphs", async () => {
    const got = await answer(phone, "TOOL please");
    assert(got.state === "complete", `the answer is ${got.state}`);
    const blocks = await phone.locator(`${ANSWERS} [data-assistant-text]`).last().locator("p").allInnerTexts();
    assert(JSON.stringify(blocks) === JSON.stringify(["Looking at the project.", "I looked at the project."]), `the blocks read ${JSON.stringify(blocks)}`);
  });

  await run("markdown, an ask block and a file link render like any coder's", async () => {
    const linked = await answer(phone, "FILELINK please");
    assert(linked.state === "complete", `the answer with the link is ${linked.state}`);
    const link = phone.locator(ANSWERS).last().locator("a[href*='/media/assistant-files/proof.txt']");
    const href = await link.getAttribute("href");
    assert(href.startsWith(`/assistants/${id}/media/assistant-files/proof.txt`), `the file link points to ${href}`);
    const proof = await phone.request.get(`${BASE}${href}`);
    assert(proof.status() === 200 && (await proof.text()).includes(`/assistant/instances/${id}/workspace`), `the linked file answers ${proof.status()}`);

    const message = ["ECHO_ANSWER", "## Plan", "", "Some `code` first.", "", "```ask", "Which one?", "- This", "- That", "```", ""].join("\n");
    await send(phone, message);
    await idle(phone);
    const bubble = phone.locator(ANSWERS).last();
    assert((await bubble.locator("h2").innerText()) === "Plan" && (await bubble.locator("code").first().innerText()) === "code", "the markdown is not rendered");
    const block = bubble.locator("[data-choices-state]");
    assert((await block.getAttribute("data-choices-state")) === "open", "the ask block is not open");
    assert((await block.locator("[data-choices-option]").count()) === 2, "the ask block lost an option");
    await block.locator("[data-choices-option]").nth(1).tap();
    await block.locator("[data-choices-send]").tap();
    await phone.waitForFunction((sel) => {
      const blocks = document.querySelectorAll(sel);
      return blocks[blocks.length - 1].getAttribute("data-choices-state") === "answered";
    }, `${ANSWERS} [data-choices-state]`, { timeout: 20000 });
    await idle(phone);
    const row = phone.locator('[data-assistant-message][data-role="user"]').last();
    assert(await row.locator("[data-assistant-answered]").count() === 1, `the pick did not go out as the answered row: ${await row.innerText()}`);
  });

  await run("a picture the answer links from its files shows as a picture in the thread", async () => {
    const got = await answer(phone, "IMAGE please");
    assert(got.state === "complete", `the answer with the picture is ${got.state}`);
    const img = phone.locator(ANSWERS).last().locator("img");
    assert(await img.count() === 1, `the answer holds ${await img.count()} pictures: ${await phone.locator(ANSWERS).last().innerHTML()}`);
    const src = await img.getAttribute("src");
    assert(src.startsWith(`/assistants/${id}/media/assistant-files/pic.png`), `the picture points to ${src}`);
    await phone.waitForFunction((sel) => {
      const nodes = document.querySelectorAll(sel);
      const pic = nodes[nodes.length - 1].querySelector("img");
      return pic && pic.complete && pic.naturalWidth > 0;
    }, ANSWERS, { timeout: 10000 });
    const shown = await img.evaluate((pic) => {
      const box = pic.getBoundingClientRect();
      const thread = pic.closest("[data-assistant-message]").getBoundingClientRect();
      return { natural: `${pic.naturalWidth}x${pic.naturalHeight}`, width: box.width, height: box.height, inside: box.left >= thread.left && box.right <= thread.right, alt: pic.alt };
    });
    assert(shown.natural === "40x30" && shown.width > 0 && shown.height > 0 && shown.inside && shown.alt === "the picture", `the picture shows as ${JSON.stringify(shown)}`);
  });

  await run("records no version knows do not break a turn", async () => {
    const got = await answer(phone, "UNKNOWN records");
    assert(got.state === "complete" && got.text === "Read past what nobody knows.", `the answer: ${JSON.stringify(got)}`);
  });

  await run("a turn codex fails says so and the next one recovers", async () => {
    const failed = await answer(phone, "FAIL once");
    assert(failed.state === "failed", `the failed answer is ${failed.state}`);
    const said = await phone.locator(ANSWERS).last().innerText();
    assert(said.includes("The coder could not finish this answer.") && said.includes("stream disconnected before completion"), `the failure reads "${said}"`);
    const retry = await answer(phone, "FAIL again");
    assert(retry.text === "Recovered after the retry.", `the retry answered "${retry.text}"`);
  });

  await run("the ring shows codex's own reading of the context", async () => {
    assert((await ringFill(phone)) === "68 100", `the ring stands at ${await ringFill(phone)}`);
    await answer(phone, "CONTEXT_HIGH please");
    await phone.waitForFunction(() => document.querySelector("[data-assistant-ring-fill]")?.getAttribute("stroke-dasharray") === "96 100", null, { timeout: 8000 });
    const level = await phone.locator("dc-assistant [data-assistant-ring]").first().getAttribute("data-assistant-ring-level");
    assert(level === "full", `96 percent wears level ${level}`);
    const label = await phone.locator("dc-assistant [data-assistant-new-label]").first().getAttribute("title");
    assert(label.includes("Context 96 percent"), `the label reads "${label}"`);
    await answer(phone, "MAGIC back down");
    await phone.waitForFunction(() => document.querySelector("[data-assistant-ring-fill]")?.getAttribute("stroke-dasharray") === "68 100", null, { timeout: 8000 });
  });

  await run("the ring's model picks hold codex's models and the chat pick reaches the turn", async () => {
    const options = await phone.evaluate(() => Object.fromEntries(["chat", "check", "trigger"].map((kind) => [kind,
      [...document.querySelectorAll(`dc-assistant [data-assistant-model-pick="${kind}"] select option`)].map((o) => o.value)])));
    for (const kind of ["chat", "check", "trigger"]) {
      assert(options[kind].includes("gpt-fake-sol") && options[kind].includes("gpt-fake-luna") && !options[kind].includes("gpt-fake-hidden"), `the ${kind} pick offers ${JSON.stringify(options[kind])}`);
    }
    const set = await postForm(phone, `/assistants/${id}`, { form: "model", model: "gpt-fake-luna" });
    assert(set.status === 200, `the pick was refused: ${set.status} ${JSON.stringify(set.body)}`);
    const picked = await answer(phone, "MODEL please");
    assert(picked.text === "model: gpt-fake-luna", `the turn ran on "${picked.text}"`);
  });

  await run("a model codex does not have is named, with where to change it", async () => {
    const set = await postForm(phone, `/assistants/${id}`, { form: "model", model: "no-such-model" });
    assert(set.status === 200, `the pick was refused: ${set.status}`);
    const refused = await answer(phone, "MODEL please");
    const said = await phone.locator(ANSWERS).last().innerText();
    assert(refused.state === "failed" && said.includes("The coder does not know the model no-such-model. Pick another at the ring button of this assistant."), `the refusal reads "${said}"`);
    await postForm(phone, `/assistants/${id}`, { form: "model", model: "" });
    const back = await answer(phone, "MODEL please");
    assert(back.text === "model: default", `after the pick was cleared the turn ran on "${back.text}"`);
  });

  await run("a turn in flight survives a restart of the cockpit", async () => {
    await idle(phone);
    await phone.evaluate(() => { window.__dcSamePage = true; });
    await send(phone, "please RESTART_CHAT now");
    await idle(phone, 90000);
    assert(await phone.evaluate(() => window.__dcSamePage === true), "the page reloaded, so this says nothing about the open stream");
    const got = await last(phone);
    assert(got.state === "complete" && got.text.includes("before the restart") && got.text.includes("and after it"), `the answer across the restart: ${JSON.stringify(got)}`);
    const after = await answer(phone, "TURNS after the restart");
    assert(new RegExp(`^turn \\d+ of session ${native}$`).test(after.text), `after the restart the turn ran as "${after.text}"`);
  });

  await run("a steered job's check runs on codex, on the check model, and gives its verdict", async () => {
    await open(page, id);
    await L.createProject(page, project).catch(() => {});
    const projectDir = await L.projectPath(page, project);
    await open(page, id);
    const created = await postForm(page, "/coders/new", { name: "codex-job", project: projectDir, coder: "claude", automatic_approval: "on", prompt: "Write the README." });
    assert(created.status === 200 && created.body.id, `no coder to steer: ${created.status} ${JSON.stringify(created.body)}`);
    coder = created.body.id;
    assert((await postForm(page, `/assistants/${id}`, { form: "model", check_model: "gpt-fake-luna" })).status === 200, "the check model was refused");
    const steered = await postForm(page, "/assistants/jobs", { form: "steer", assistant: id, terminal: coder, task: "Write the README", done_when: "WAKE_DONE: README.md exists" });
    assert(steered.status === 200, `steer answered ${steered.status}: ${JSON.stringify(steered.body)}`);
    await idle(phone);
    const before = await phone.locator("[data-assistant-message]").count();
    ring(coder);
    await phone.waitForFunction((count) => document.querySelectorAll("[data-assistant-message]").length > count, before, { timeout: 40000 });
    await phone.locator('[data-assistant-wake="done"]').last().waitFor({ state: "attached", timeout: 10000 });
    const report = await phone.locator("[data-assistant-message]").last().innerText();
    assert(report.includes("job is finished"), `the report reads "${report}"`);
    assert((await ringFill(phone)) === "68 100", `the check's own reading reached the ring: ${await ringFill(phone)}`);
    const turn = await waitFor(() => {
      const dir = path.join(CODEX_HOME, "argv");
      return fs.readdirSync(dir).sort().map((f) => JSON.parse(fs.readFileSync(path.join(dir, f), "utf8"))).reverse().find((argv) => argv[argv.length - 1].includes("you are steering"));
    }, "the check's argv", 20);
    assert(turn.includes("--model=gpt-fake-luna"), `the check ran without its model: ${JSON.stringify(turn.slice(0, -1))}`);
    const own = turn[turn.length - 2];
    assert(own !== native, "the check ran in the conversation's own session");
    await waitFor(() => !rollouts().some((r) => r.id === own), "the check's session to go");
  });

  await run("a trigger's reaction runs on codex, on the trigger's own model", async () => {
    const made = await postForm(page, "/assistants/triggers", { form: "new", assistant: id, event: "coder-news", terminal: coder, task: "MODEL say it", batch: "0", once: "on", model: "gpt-fake-sol" });
    assert(made.status === 200 && made.body.id, `the trigger was refused: ${made.status} ${JSON.stringify(made.body)}`);
    await idle(phone);
    ring(coder);
    await phone.waitForFunction((sel) => [...document.querySelectorAll(sel)].some((n) => n.innerText.includes("model: gpt-fake-sol")), "[data-assistant-message]", { timeout: 40000 });
    await waitFor(() => rollouts().length === 1, "the reaction's session to go");
  });

  await run("a tool of an MCP server waits for the user: Approve runs it, Deny tells the turn", async () => {
    await open(page, id);
    await idle(phone);
    await send(phone, `PERMIT:${TOOL}`);
    await phone.waitForSelector(`${DIALOG} [data-approval-what]`, { timeout: 20000 });
    assert((await phone.textContent(`${DIALOG} [data-approval-what]`)).trim() === "Use search_channels", "the dialog's head names another tool");
    const rows = await phone.locator(`${DIALOG} [data-approval-details]`).evaluate((root) => [...root.querySelectorAll("[data-approval-detail]")]
      .map((el) => [el.dataset.approvalDetail, (el.querySelector("[data-approval-value]") || el).textContent.trim()]));
    assert(JSON.stringify(rows) === JSON.stringify([["Tool", TOOL], ["query", "general"], ["limit", "5"]]), `the dialog's rows read ${JSON.stringify(rows)}`);
    assert(await phone.evaluate((sel) => {
      const box = document.querySelector(sel).getBoundingClientRect();
      return box.left >= 0 && box.right <= window.innerWidth && box.bottom <= window.innerHeight;
    }, DIALOG), "the dialog does not fit the phone");
    await phone.tap(`${DIALOG} .swal2-confirm`);
    await phone.waitForSelector(DIALOG, { state: "detached", timeout: 8000 });
    await idle(phone);
    assert((await last(phone)).text === `Tool ${TOOL}: allowed`, `the approved call answered "${(await last(phone)).text}"`);
    assert(await phone.locator('[data-assistant-note="approval"]').count() === 0, "an approval note landed in the thread");

    await send(phone, `PERMIT:${TOOL}`);
    await phone.waitForSelector(`${DIALOG} [data-approval-what]`, { timeout: 20000 });
    await phone.tap(`${DIALOG} .swal2-deny`);
    await phone.waitForSelector(DIALOG, { state: "detached", timeout: 8000 });
    await idle(phone);
    assert((await last(phone)).text.startsWith(`Tool ${TOOL}: denied: The user denied this tool call in the cockpit.`), `the denied call answered "${(await last(phone)).text}"`);
  });

  await run("Always allow this tool lets its next call run unasked, and codex's own tools never ask", async () => {
    await send(phone, `PERMIT:${TOOL}`);
    await phone.waitForSelector(`${DIALOG} [data-approval-what]`, { timeout: 20000 });
    await phone.check(`${DIALOG} .swal2-checkbox input`);
    await phone.tap(`${DIALOG} .swal2-confirm`);
    await phone.waitForSelector(DIALOG, { state: "detached", timeout: 8000 });
    await idle(phone);
    const again = await answer(phone, `PERMIT:${TOOL}`);
    assert(again.text === `Tool ${TOOL}: allowed`, `the allowed tool answered "${again.text}"`);
    assert(await phone.locator(DIALOG).count() === 0, "the allowed tool asked again");
    const own = await answer(phone, "PERMIT:shell");
    assert(own.text === "Tool shell: ran unasked", `codex's own tool answered "${own.text}"`);

    await page.goto(`${BASE}/settings/assistant/approvals`, { waitUntil: "domcontentloaded" });
    const listed = await page.evaluate(() => ({
      heading: document.querySelector("dc-allowed-tools h3").textContent.trim(),
      hint: document.querySelector("[data-allowed-tools-hint]").textContent.trim(),
      tools: [...document.querySelectorAll("[data-allowed-tool]")].map((row) => row.dataset.allowedTool),
    }));
    assert(listed.heading === "Always allowed tools" && listed.hint.startsWith("When a Claude or Codex assistant wants a tool that needs approval"), `the tab reads ${JSON.stringify(listed)}`);
    assert(listed.tools.includes(TOOL), `the tab lists ${JSON.stringify(listed.tools)}`);
    await page.locator(`[data-allowed-tool="${TOOL}"] button`).click();
    await waitFor(async () => !(await page.locator(`[data-allowed-tool="${TOOL}"]`).count()), "the tool to leave the list");
  });

  await run("a delete the turn asks for waits for the user: Deny keeps the project, Approve removes it", async () => {
    const listed = async (name) => page.evaluate(async (wanted) => {
      const html = await (await fetch("/projects")).text();
      return Boolean(new DOMParser().parseFromString(html, "text/html").getElementById(`project-${wanted}`));
    }, name);
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    for (const name of [KEPT, GONE]) await L.createProject(page, name).catch(() => {});
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    assert(await listed(KEPT) && await listed(GONE), "the scratch projects of the delete are missing");
    const notes = () => phone.locator('[data-assistant-note="approval"]').count();
    const ask = async (name) => {
      const got = await answer(phone, `COCKPIT: project-delete ${name}`);
      assert(got.state === "complete" && got.text.includes("waits for the user's approval"), `the delete from the turn printed "${got.text}"`);
      await phone.waitForSelector(`${DIALOG} [data-approval-what]`, { timeout: 20000 });
      const what = (await phone.textContent(`${DIALOG} [data-approval-what]`)).trim();
      assert(what === `Delete project ${name}`, `the dialog's head reads "${what}"`);
      await sleep(1500);
      assert(await listed(name), `${name} went before anybody approved`);
    };

    let before = await notes();
    await ask(KEPT);
    await phone.tap(`${DIALOG} .swal2-deny`);
    await phone.waitForSelector(DIALOG, { state: "detached", timeout: 8000 });
    await waitFor(async () => (await notes()) > before, "the note of the denied delete", 60);
    const denied = (await phone.locator('[data-assistant-note="approval"]').last().innerText()).replace(/\s+/g, " ");
    assert(denied.startsWith(`Declined: Delete project ${KEPT}`), `the note of the denied delete reads "${denied}"`);
    assert(await listed(KEPT), "a denied delete removed the project");

    before = await notes();
    await ask(GONE);
    await phone.tap(`${DIALOG} .swal2-confirm`);
    await phone.waitForSelector(DIALOG, { state: "detached", timeout: 8000 });
    await waitFor(async () => !(await listed(GONE)), "the approved delete", 80);
    await waitFor(async () => (await notes()) > before, "the note of the approved delete", 60);
    assert(await listed(KEPT), "the approved delete took the other project along");
    return denied;
  });

  await run("a turn reaches the cockpit's own commands as its assistant", async () => {
    const listed = await answer(phone, "OWNER please");
    const text = await phone.locator(ANSWERS).last().innerText();
    const own = text.split("\n").find((line) => line.trim().startsWith("*codex") && line.includes(`id ${id}`));
    assert(listed.state === "complete" && own && text.includes("* is you"), `assistant-list from inside the turn (${listed.state}) printed:\n${text}`);
    return own.trim();
  });

  await run("deleting the assistant removes its codex session, and the scratch is gone", async () => {
    assert(rollouts().some((r) => r.id === native), "the session went before the delete");
    await L.stopSession(page, `${BASE}/coders/${coder}`).catch(() => {});
    const gone = await postForm(phone, `/assistants/${id}`, { form: "delete" });
    assert(gone.status === 200, `the delete answered ${gone.status}: ${JSON.stringify(gone.body)}`);
    await waitFor(() => rollouts().length === 0, "the codex session to go");
    const names = fs.readFileSync(path.join(CODEX_HOME, "session_index.jsonl"), "utf8");
    assert(!names.includes(native), "the session's name stayed behind");
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await L.deleteProject(page, project).catch(() => {});
    await L.deleteProject(page, KEPT).catch(() => {});
    // The fake's own notes go too, so a runner that reads what codex's home
    // holds finds it the way codex leaves it.
    fs.rmSync(path.join(CODEX_HOME, "argv"), { recursive: true, force: true });
    for (const name of fs.readdirSync(CODEX_HOME)) {
      if (name.startsWith("failed-")) fs.rmSync(path.join(CODEX_HOME, name));
    }
  });
});
