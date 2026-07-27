const L = require("./lib");
const { assert, BASE, sleep } = L;

// Coders waking the assistant: a steered job (POST /assistant/jobs with
// form=steer), the job list fragment (GET /assistant/jobs, swapped by
// dc-assistant-list on the assistant event), the check itself (a wake turn in
// its own provider session), and what a check leaves behind: one message marked
// as a check plus cockpit news naming the job when there is something to say,
// and nothing at all when there is not. The coder's own signal stays quiet while
// its job is open: its entry is written already read, the report is the news.
//
// The instance MUST be the assistant one: tests/e2e/fakes ahead of the real CLIs
// on PATH and a scratch HOME. The signal is injected the way notifications.js
// does it, by dropping a claude Stop hook payload into the notify inbox, so the
// run needs that directory mounted:
//
//   -v <state-dir>/notification-inbox:/inbox -e NOTIFY_DIR=/inbox
//
// Without it this runner fails right away: a check that quietly does nothing is a
// check that lies later.
//
// One check restarts the instance: the WAKE_RESTART criterion makes the fake
// kill the cockpit in the middle of a check and start it again, which is how the
// run proves that a check outlives its server. Point this runner at a throwaway
// only, and never at an instance somebody is using.
//
// Gotchas:
// - the fake reads the criterion out of the wake prompt, so WAKE_DONE,
//   WAKE_BLOCKED, WAKE_NOTHING, WAKE_WORKING and WAKE_PREAMBLE in a criterion
//   decide what the check answers, and LEAVE_WORKING in a coder's task leaves
//   its transcript on a tool call, which is what a running turn looks like,
// - a check reads the coder's session, not its screen: the fake writes a claude
//   shaped transcript when it starts, ending on its own answer, so a job here
//   counts as standing still and the plain WORKING of WAKE_WORKING becomes a
//   blocked report,
// - QUESTION in a coder's task leaves a chooser on its screen and nothing in its
//   transcript, which is what a real coder waiting on a question looks like: to
//   everything outside it is a coder whose turn is over. WAKE_ANSWER in a
//   criterion makes the check press the keys itself and report WORKING,
// - the fakes carry one more branch, WAKE_STEER, where the check sends to the
//   coder and keeps its WORKING. It calls `dev-cockpit assistant
//   coder-send-prompt`, so it needs the cockpit CLI on the instance's PATH,
//   which a runner instance has no reason to have; that rule is covered by the
//   Go test in internal/assistant instead,
// - a check runs with its own provider session, so the conversation's own turn
//   stays free: a chat message during a check has to go through, and what the
//   check consumed stays out of the conversation's context ring, which is why
//   both fakes report a different number on a check than on a chat turn,
// - a check counts when it came back, so the counter says how many checks
//   answered; while one runs the job carries data-assistant-job-checking, which
//   is what a wait for "a check is running" keys on.

const NOTIFY_DIR = process.env.NOTIFY_DIR || "";
const fs = require("fs");
const path = require("path");

function ring(sessionID) {
  const name = `${Date.now()}-${Math.floor(Math.random() * 1e6)}`;
  const payload = JSON.stringify({ session_id: sessionID, hook_event_name: "Stop" });
  fs.writeFileSync(path.join(NOTIFY_DIR, `${name}.tmp`), payload);
  fs.renameSync(path.join(NOTIFY_DIR, `${name}.tmp`), path.join(NOTIFY_DIR, `${name}.json`));
}

async function dismissUpdate(page) {
  const cancel = page.locator(".swal2-cancel");
  try {
    await cancel.waitFor({ state: "visible", timeout: 2000 });
  } catch {
    return;
  }
  await cancel.click();
  await page.waitForSelector(".swal2-container", { state: "detached", timeout: 5000 });
}

// The assistant has no pages: /assistant redirects to /projects with the
// query that opens the overlay, so the conversation id comes from the surface
// element, not from the address.
async function openAssistant(page) {
  await page.goto(`${BASE}/assistant`, { waitUntil: "domcontentloaded" });
  await dismissUpdate(page);
  await page.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
  return page.locator("dc-assistant").getAttribute("conversation-id");
}

// post sends one of the job forms the way the page's own list does. Jobs belong
// to the assistant, not to a conversation, so they all go to the one jobs path.
async function post(page, fields) {
  return page.evaluate(async (values) => {
    const body = new URLSearchParams(values);
    body.set("csrf_token", document.querySelector('meta[name="csrf-token"]').content);
    const res = await fetch("/assistant/jobs", {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded", Accept: "application/json" },
      body: body.toString(),
    });
    return { status: res.status, body: await res.json().catch(() => ({})) };
  }, fields);
}

async function startCoder(page, project, name, task) {
  return page.evaluate(async ([values]) => {
    const body = new URLSearchParams(values);
    body.set("csrf_token", document.querySelector('meta[name="csrf-token"]').content);
    const res = await fetch("/coders/new", {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded", Accept: "application/json" },
      body: body.toString(),
    });
    return { status: res.status, body: await res.json().catch(() => ({})) };
  }, [{ name, project, coder: "claude", automatic_approval: "on", prompt: task }]);
}

// The job rows render inside the overlay's jobs view only, so the state comes
// from the list fragment the view is fed from, independent of what the
// overlay currently shows.
async function jobState(page, terminal) {
  return page.evaluate(async (id) => {
    const res = await fetch("/assistant/jobs", { headers: { Accept: "text/html" } });
    const holder = document.createElement("div");
    holder.innerHTML = await res.text();
    const row = holder.querySelector(`[data-assistant-job="${id}"]`);
    if (!row) return null;
    return {
      state: row.querySelector("[data-assistant-job-state]")?.textContent.trim() || "",
      note: row.querySelector("[data-assistant-job-note]")?.textContent.trim() || "",
      doneWhen: row.querySelector("[data-assistant-job-done-when]")?.textContent.trim() || "",
    };
  }, terminal);
}

async function waitJobNote(page, terminal, want, timeout = 40000) {
  const deadline = Date.now() + timeout;
  let seen = null;
  while (Date.now() < deadline) {
    seen = await jobState(page, terminal);
    if (seen && want.test(seen.note)) return seen;
    await sleep(400);
  }
  assert(false, `job ${terminal} is ${JSON.stringify(seen)}, want a note matching ${want}`);
  return seen;
}

async function waitJobState(page, terminal, want, timeout = 30000) {
  const deadline = Date.now() + timeout;
  let seen = null;
  while (Date.now() < deadline) {
    seen = await jobState(page, terminal);
    if (seen && seen.state === want) return seen;
    await sleep(400);
  }
  assert(false, `job ${terminal} is ${JSON.stringify(seen)}, want state ${want}`);
  return seen;
}

// messageText is the body of the newest report about one coder, without the
// badge line: the assertions are about what the check wrote, and a report from
// another job may sit below this one.
async function messageText(page, coderName) {
  return page.evaluate((name) => {
    const rows = [...document.querySelectorAll("[data-assistant-message]")].filter((row) => {
      const wake = row.querySelector("[data-assistant-wake]");
      return wake && wake.textContent.includes(name);
    });
    const last = rows[rows.length - 1];
    return last ? (last.querySelector("[data-assistant-text]")?.innerText || "").trim() : "";
  }, coderName);
}

L.runFeature("wake", async ({ page, run }) => {
  assert(NOTIFY_DIR, "NOTIFY_DIR is not set: run with -v <state-dir>/notification-inbox/claude:/inbox -e NOTIFY_DIR=/inbox, "
    + "without the notify inbox nothing can make a coder report and this runner would check nothing");

  const project = "zzwake";
  let projectDir = "";
  let conversation = "";
  let coderID = "";
  let nothingCoder = "";
  let standCoder = "";
  let busyCoder = "";
  let stopCoder = "";
  let preambleCoder = "";
  let askCoder = "";
  let ringCoder = "";

  await run("a coder starts with its task in the argv", async () => {
    await L.createProject(page, project).catch(() => {});
    projectDir = await L.projectPath(page, project);
    conversation = await openAssistant(page);
    const created = await startCoder(page, projectDir, "wake-task", "Write the README.");
    assert(created.status === 200, `create answered ${created.status}: ${JSON.stringify(created.body)}`);
    coderID = created.body.id;
    assert(coderID, "the create call did not name the coder");
  });

  await run("steering shows the job, and the page may leave the criterion empty", async () => {
    // From the page the criterion is optional: the check then judges against
    // the session's own task, and the job list says so instead of a blank.
    const open = await post(page, { form: "steer", terminal: coderID, done_when: "" });
    assert(open.status === 200, `a page steer without a criterion was refused: ${open.status}`);
    await page.reload({ waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    await page.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
    const bare = await jobState(page, coderID);
    assert(bare && /session's own task/.test(bare.doneWhen || ""),
      `a job without a criterion does not say what decides it: ${JSON.stringify(bare)}`);

    const steered = await post(page, {
      form: "steer",
      terminal: coderID,
      task: "Write the README",
      done_when: "WAKE_DONE: README.md exists",
    });
    assert(steered.status === 200, `steer answered ${steered.status}: ${JSON.stringify(steered.body)}`);
    assert(steered.body.maxWakes > 0, "a job without a budget");

    await page.reload({ waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    await page.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
    const shown = await jobState(page, coderID);
    assert(shown && shown.state === "steering", `the job list shows ${JSON.stringify(shown)}`);
  });

  await run("an unknown terminal cannot be steered", async () => {
    const refused = await post(page, {
      form: "steer",
      terminal: "11111111-1111-4111-8111-111111111111",
      done_when: "anything",
    });
    assert(refused.status >= 400, "a job on a terminal that is not running was accepted");
  });

  await run("a coder that reports gets checked, and a finished job reaches the user", async () => {
    const before = await page.locator("[data-assistant-message]").count();
    ring(coderID);

    // The report appears live, without a reload, and it is marked as a check.
    await page.waitForFunction(
      (count) => document.querySelectorAll("[data-assistant-message]").length > count,
      before,
      { timeout: 40000 },
    );
    const wake = page.locator('[data-assistant-wake="done"]').last();
    await wake.waitFor({ state: "attached", timeout: 10000 });
    const bubble = page.locator("[data-assistant-message]").last();
    assert((await bubble.getAttribute("data-role")) === "assistant", "a check must never look like a user message");
    const text = await bubble.innerText();
    assert(text.includes("job is finished"), `the report is missing: ${text}`);
    assert(!text.includes("DONE:"), `the verdict leaked into the text: ${text}`);

    // The job flips to done in place, and the news is there for the phone,
    // naming the job it is about.
    await waitJobState(page, coderID, "done");
    const stored = await page.evaluate(async () => {
      const res = await fetch("/notifications", { headers: { Accept: "application/json" } });
      const data = await res.json();
      return data.notifications || [];
    });
    const news = stored.filter((n) => n.targetId === conversation);
    assert(news.length >= 1 && news[0].title === "Job done.", `unexpected news ${JSON.stringify(news)}`);
    // The name and the project come from the report's own note, the job the
    // check was about, so the lower line names it without any lookup.
    assert(new RegExp(`^".+" - ${project}$`).test(news[0].detail || ""),
      `the news does not name the job: ${JSON.stringify(news[0])}`);
    // The signal that bought the check rang nowhere: while a job is open the
    // assistant's report is the message the user gets, so the coder's own
    // entry is written read and only keeps the history complete.
    const own = stored.filter((n) => n.targetId === coderID);
    assert(own.length >= 1 && own.every((n) => n.read && n.silent),
      `a steered coder rang the user: ${JSON.stringify(own)}`);
    return `report and news for ${coderID.slice(0, 8)}`;
  });

  await run("a check without news leaves no trace", async () => {
    const created = await startCoder(page, projectDir, "wake-quiet", "Idle for a while.");
    assert(created.status === 200, `create answered ${created.status}`);
    nothingCoder = created.body.id;
    const steered = await post(page, {
      form: "steer",
      terminal: nothingCoder,
      task: "Nothing to report",
      done_when: "WAKE_NOTHING: never true",
    });
    assert(steered.status === 200, `steer answered ${steered.status}`);

    await page.reload({ waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    await page.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
    const before = await page.locator("[data-assistant-message]").count();
    ring(nothingCoder);

    // Wait for the check to have happened: the job's own line is the only place
    // a quiet check shows up.
    const deadline = Date.now() + 40000;
    let checked = false;
    while (Date.now() < deadline && !checked) {
      await sleep(500);
      const state = await jobState(page, nothingCoder);
      checked = !!state && state.state === "steering" && await page.evaluate(async (id) => {
        const res = await fetch("/assistant/jobs", { headers: { Accept: "text/html" } });
        const html = await res.text();
        return html.includes(`data-assistant-job="${id}"`) && /1 of \d+ checks/.test(html);
      }, nothingCoder);
    }
    assert(checked, "the quiet job was never checked");
    const after = await page.locator("[data-assistant-message]").count();
    assert(after === before, `a quiet check wrote ${after - before} message(s) into the transcript`);
    return "no message, no news";
  });

  await run("a job that stands still is never reported as still working", async () => {
    const created = await startCoder(page, projectDir, "wake-stand", "Sit still.");
    assert(created.status === 200, `create answered ${created.status}`);
    standCoder = created.body.id;
    // The fake coder sits idle after it printed its task, so the two captures a
    // check takes are identical. Its answer is a plain WORKING, which is the
    // one thing a standing job may not end as.
    const steered = await post(page, {
      form: "steer",
      terminal: standCoder,
      task: "Something nobody moves",
      done_when: "WAKE_WORKING: never true",
    });
    assert(steered.status === 200, `steer answered ${steered.status}`);

    await page.reload({ waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    await page.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
    const before = await page.locator("[data-assistant-message]").count();
    ring(standCoder);

    await waitJobState(page, standCoder, "blocked");
    await page.waitForFunction(
      (count) => document.querySelectorAll("[data-assistant-message]").length > count,
      before,
      { timeout: 40000 },
    );
    // Scoped to this coder's own report: another job's late check may land in
    // the same transcript.
    const text = await messageText(page, "wake-stand");
    assert(/is idle and the job is not done/.test(text), `the standstill was not reported: ${text}`);
    assert(text.includes("still going"), `the check's own line is missing: ${text}`);
    return "a standing job ends blocked, with a report";
  });

  await run("a coder whose turn is still running keeps its quiet WORKING", async () => {
    // LEAVE_WORKING leaves the fake's transcript on a tool call it has not
    // answered, which is what a running turn looks like. The check must then
    // read a working coder and stay quiet, instead of turning its WORKING into a
    // standstill report.
    const created = await startCoder(page, projectDir, "wake-busy", "LEAVE_WORKING keep going");
    assert(created.status === 200, `create answered ${created.status}`);
    busyCoder = created.body.id;
    const steered = await post(page, {
      form: "steer",
      terminal: busyCoder,
      task: "Something that is still running",
      done_when: "WAKE_WORKING: never true",
    });
    assert(steered.status === 200, `steer answered ${steered.status}`);

    await page.reload({ waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    await page.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
    const before = await page.locator("[data-assistant-message]").count();
    ring(busyCoder);

    const deadline = Date.now() + 40000;
    let note = "";
    while (Date.now() < deadline && !note) {
      await sleep(500);
      const state = await jobState(page, busyCoder);
      if (state && state.state === "steering" && state.note) note = state.note;
      if (state && state.state !== "steering") assert(false, `the job left steering: ${JSON.stringify(state)}`);
    }
    assert(/still going/.test(note), `the check did not report it as working: ${note}`);
    const after = await page.locator("[data-assistant-message]").count();
    assert(after === before, `a working coder cost ${after - before} message(s)`);
    return "working coder, no report";
  });

  await run("the note starts at the verdict", async () => {
    const created = await startCoder(page, projectDir, "wake-preamble", "Sit still.");
    assert(created.status === 200, `create answered ${created.status}`);
    preambleCoder = created.body.id;
    // The fake talks before it judges, the way a model does that reasons out
    // loud. Everything before the verdict has to be gone.
    const steered = await post(page, {
      form: "steer",
      terminal: preambleCoder,
      task: "Something with a talkative check",
      done_when: "WAKE_PREAMBLE: never true",
    });
    assert(steered.status === 200, `steer answered ${steered.status}`);

    await page.reload({ waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    await page.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
    const before = await page.locator("[data-assistant-message]").count();
    ring(preambleCoder);

    await waitJobState(page, preambleCoder, "blocked");
    await page.waitForFunction(
      (count) => document.querySelectorAll("[data-assistant-message]").length > count,
      before,
      { timeout: 40000 },
    );
    const text = await messageText(page, "wake-preamble");
    assert(!/rather than trust the summary/.test(text), `the preamble reached the user: ${text}`);
    assert(!/BLOCKED/.test(text), `the verdict itself leaked into the text: ${text}`);
    assert(text.startsWith("it needs a decision"), `the report does not start at the verdict: ${text}`);
    const shown = await jobState(page, preambleCoder);
    assert(shown && /^it needs a decision/.test(shown.note || ""), `the job's note keeps the preamble: ${JSON.stringify(shown)}`);
    return "preamble dropped, report starts at the verdict";
  });

  await run("typing at a blocked job leaves it blocked", async () => {
    // The standstill report left this job blocked. Steering is ownership and
    // only steer and release change it, so an answer typed from the browser
    // must not reopen the job behind the user's back. Only an assistant send
    // over the local API takes a blocked job up again.
    const shown = await jobState(page, standCoder);
    assert(shown && shown.state === "blocked", `the job is ${JSON.stringify(shown)}, want blocked first`);

    const sent = await page.evaluate(async (id) => {
      const token = document.querySelector('meta[name="csrf-token"]').content;
      const res = await fetch(`/coders/${id}/input`, {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-CSRF-Token": token },
        body: JSON.stringify({ items: [{ prompt: "carry on then" }] }),
      });
      return res.status;
    }, standCoder);
    assert(sent === 200, `the input answered ${sent}`);

    await sleep(1500);
    const again = await jobState(page, standCoder);
    assert(again && again.state === "blocked", `a browser input changed the job: ${JSON.stringify(again)}`);
    return "blocked, answered from the browser, still blocked";
  });

  await run("a check never blocks the chat", async () => {
    const created = await startCoder(page, projectDir, "wake-slow", "Take your time.");
    assert(created.status === 200, `create answered ${created.status}`);
    const slowCoder = created.body.id;
    // SLOW makes the fake hold the check open; WAKE_NOTHING makes its late
    // answer write nothing, so the run leaves no message behind.
    const steered = await post(page, {
      form: "steer",
      terminal: slowCoder,
      task: "Something long",
      done_when: "SLOW WAKE_NOTHING: never true",
    });
    assert(steered.status === 200, `steer answered ${steered.status}`);
    ring(slowCoder);

    // Wait until the check really holds a turn. The counter only moves when a
    // check came back, so what says "a check is running" is the job's own mark,
    // the same one the page shows.
    const deadline = Date.now() + 30000;
    let checking = false;
    while (Date.now() < deadline && !checking) {
      await sleep(400);
      checking = await page.evaluate(async (id) => {
        const res = await fetch("/assistant/jobs", { headers: { Accept: "text/html" } });
        const html = await res.text();
        return new RegExp(`data-assistant-job="${id}"[\\s\\S]*?data-assistant-job-checking`).test(html);
      }, slowCoder);
    }
    assert(checking, "the slow check never started");

    // The user writes while that check is still running. This is the rule the
    // whole design turns on, so it is checked against the real thing.
    await page.reload({ waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    await page.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
    const before = await page.locator('[data-role="assistant"]').count();
    await page.fill("[data-assistant-input]", "MAGIC what is the word");
    await page.click("[data-assistant-send]");
    await page.waitForFunction(
      (count) => document.querySelectorAll('[data-role="assistant"]').length > count,
      before,
      { timeout: 30000 },
    );
    await page.waitForFunction(() => {
      const nodes = document.querySelectorAll("[data-assistant-message]");
      const last = nodes[nodes.length - 1];
      return last && last.getAttribute("data-state") !== "streaming";
    }, null, { timeout: 30000 });
    const answer = await page.locator('[data-role="assistant"]').last().innerText();
    assert(answer.includes("FLUGHAFEN"), `the chat answer did not arrive while a check ran: ${answer}`);

    await post(page, { form: "release", terminal: slowCoder });
    await page.evaluate(async (session) => {
      const token = document.querySelector('meta[name="csrf-token"]').content;
      await fetch(`/coders/${session}/delete`, { method: "POST", headers: { "X-CSRF-Token": token } });
    }, slowCoder);
    return "chat answered while a check held a turn";
  });

  await run("the user can call a job off", async () => {
    const stopped = await post(page, { form: "release", terminal: nothingCoder || coderID });
    assert(stopped.status === 200, `release answered ${stopped.status}`);
    await page.reload({ waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    await page.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
    const shown = await jobState(page, nothingCoder || coderID);
    assert(shown && shown.state === "stopped", `the job shows ${JSON.stringify(shown)}`);

    const before = await page.locator("[data-assistant-message]").count();
    ring(nothingCoder || coderID);
    await sleep(6000);
    const after = await page.locator("[data-assistant-message]").count();
    assert(after === before, "a stopped job still woke the assistant");
    return "stopped, and it stays quiet";
  });

  await run("deleting a coder calls its job off", async () => {
    const created = await startCoder(page, projectDir, "wake-gone", "Nothing to do.");
    assert(created.status === 200, `create answered ${created.status}`);
    const goneCoder = created.body.id;
    const steered = await post(page, {
      form: "steer",
      terminal: goneCoder,
      task: "A job on a coder that gets deleted",
      done_when: "WAKE_NOTHING: never true",
    });
    assert(steered.status === 200, `steer answered ${steered.status}`);

    await page.evaluate(async (session) => {
      const token = document.querySelector('meta[name="csrf-token"]').content;
      await fetch(`/coders/${session}/delete`, { method: "POST", headers: { "X-CSRF-Token": token } });
    }, goneCoder);

    await page.reload({ waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    await page.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
    // Forget, not Release: a stopped coder keeps its job line because the
    // terminal is still there to read it next to, a deleted one leaves nothing
    // to read it next to and nothing that could ever move the job again, so the
    // whole row is gone.
    const shown = await jobState(page, goneCoder);
    assert(shown === null, `the job of a deleted coder is still listed as ${JSON.stringify(shown)}`);
    return "the job of a deleted coder is gone from the list";
  });

  // A check outlives the server too. The fake kills the cockpit in the middle of
  // the check and starts it again, so this restarts the instance it runs
  // against: throwaway only. What the check concluded still reaches the job and
  // the transcript, exactly once, and the page never reloads.
  await run("a check survives a restart of the cockpit and still reports", async () => {
    const created = await startCoder(page, projectDir, "wake-restart", "Work across a restart.");
    assert(created.status === 200, `create answered ${created.status}`);
    const restartCoder = created.body.id;
    const steered = await post(page, {
      form: "steer",
      terminal: restartCoder,
      task: "Survive a restart",
      done_when: "WAKE_RESTART: the check is killed halfway through",
    });
    assert(steered.status === 200, `steer answered ${steered.status}`);

    await page.reload({ waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    await page.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
    await page.evaluate(() => { window.__dcSamePage = true; });

    // One check runs at a time across the whole cockpit, and the slow check
    // above may still hold that slot. A queued check would spend this wait
    // sitting in the queue instead of surviving a restart.
    const free = async () => page.evaluate(async () => {
      const res = await fetch("/assistant/jobs", { headers: { Accept: "text/html" } });
      return !(await res.text()).includes("data-assistant-job-checking");
    });
    const idleBy = Date.now() + 180000;
    while (Date.now() < idleBy && !(await free())) await sleep(1000);
    assert(await free(), "the check slot never came free, so nothing was checked across a restart");

    const before = await page.locator("[data-assistant-message]").count();
    ring(restartCoder);
    await page.waitForFunction(
      (count) => document.querySelectorAll("[data-assistant-message]").length > count,
      before,
      { timeout: 90000 },
    );
    const samePage = await page.evaluate(() => window.__dcSamePage === true);
    assert(samePage, "the page reloaded, so this says nothing about the open stream");

    await waitJobState(page, restartCoder, "done");
    const text = await messageText(page, "wake-restart");
    assert(text.includes("checked across a restart"), `the report is missing: ${text}`);

    // Exactly one report: the check reserved the id of its message when it
    // started, so concluding it after a restart cannot write a second one.
    const reports = await page.evaluate(() => document.querySelectorAll('[data-assistant-wake="done"]').length);
    const jobReports = await page.evaluate((name) => [...document.querySelectorAll("[data-assistant-wake]")]
      .filter((wake) => wake.textContent.includes(name)).length, "wake-restart");
    assert(jobReports === 1, `want one report for the restarted check, got ${jobReports} (${reports} done reports on the page)`);

    await page.evaluate(async (session) => {
      const token = document.querySelector('meta[name="csrf-token"]').content;
      await fetch(`/coders/${session}/delete`, { method: "POST", headers: { "X-CSRF-Token": token } });
    }, restartCoder);
    return text;
  });

  await run("stopping a coder ends its job", async () => {
    const created = await startCoder(page, projectDir, "wake-stop", "Sit still.");
    assert(created.status === 200, `create answered ${created.status}`);
    stopCoder = created.body.id;
    const steered = await post(page, {
      form: "steer", terminal: stopCoder, task: "Something", done_when: "WAKE_NOTHING: never true",
    });
    assert(steered.status === 200, `steer answered ${steered.status}`);

    await page.evaluate(async (session) => {
      const token = document.querySelector('meta[name="csrf-token"]').content;
      await fetch(`/coders/${session}/stop`, { method: "POST", headers: { "X-CSRF-Token": token } });
    }, stopCoder);

    await page.goto(`${BASE}/assistant/${conversation}`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    await page.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
    // A stopped coder cannot report again, so a job left steering would wait
    // for a signal that never comes.
    await waitJobState(page, stopCoder, "stopped");
    return "stopped coder, stopped job";
  });

  await run("deleting a project ends the jobs of its coders", async () => {
    const scratch = "zzwakeproj";
    await L.createProject(page, scratch).catch(() => {});
    const dir = await L.projectPath(page, scratch);
    const created = await startCoder(page, dir, "wake-doomed", "Sit still.");
    assert(created.status === 200, `create answered ${created.status}`);
    const doomed = created.body.id;
    const steered = await post(page, {
      form: "steer", terminal: doomed, task: "Something", done_when: "WAKE_NOTHING: never true",
    });
    assert(steered.status === 200, `steer answered ${steered.status}`);

    await L.deleteProject(page, scratch);

    await page.goto(`${BASE}/assistant/${conversation}`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    await page.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
    await waitJobState(page, doomed, "stopped");
    return "project gone, job closed";
  });

  await run("a job survives a new conversation and reports into it", async () => {
    // A job belongs to the assistant, not to the conversation it was started
    // from. Starting a new one must not lose it, and everything it finds from
    // here on belongs where the user is now.
    const before = await jobState(page, busyCoder);
    assert(before && before.state === "steering", `want a steering job first, got ${JSON.stringify(before)}`);

    const other = await page.evaluate(async (current) => {
      const body = new URLSearchParams({ form: "new", coder: "claude" });
      body.set("csrf_token", document.querySelector('meta[name="csrf-token"]').content);
      const res = await fetch(`/assistant/${current}`, {
        method: "POST",
        headers: { "Content-Type": "application/x-www-form-urlencoded", Accept: "application/json" },
        body: body.toString(),
      });
      const payload = await res.json().catch(() => ({}));
      return payload.id || "";
    }, conversation);
    assert(other && other !== conversation, `no second conversation: ${other}`);

    // From here on the new conversation is the live one, which is where the
    // rest of this run looks for reports.
    conversation = other;
    await page.goto(`${BASE}/assistant/${conversation}`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    await page.waitForSelector("dc-assistant[ready]", { timeout: 15000 });

    const after = await jobState(page, busyCoder);
    assert(after && after.state === "steering",
      `the job did not survive the new conversation: ${JSON.stringify(after)}`);
    return "job kept, new conversation is the live one";
  });

  await run("a check answers a dialog with keys and the job keeps running", async () => {
    // A coder waiting in a chooser has nothing in its transcript, so it looks
    // exactly like a coder whose turn is over. That is deliberate: nothing in Go
    // tries to tell the two apart, the check reads the screen, presses the keys
    // and reports WORKING, which is quiet and leaves the job open.
    const created = await startCoder(page, projectDir, "wake-ask", "QUESTION: ask before running the tests.");
    assert(created.status === 200, `create answered ${created.status}`);
    askCoder = created.body.id;
    const steered = await post(page, {
      form: "steer",
      terminal: askCoder,
      task: "Run the tests",
      done_when: "WAKE_ANSWER: the tests pass",
    });
    assert(steered.status === 200, `steer answered ${steered.status}`);

    await page.goto(`${BASE}/assistant/${conversation}`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    await page.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
    const before = await page.locator("[data-assistant-message]").count();
    // Only the news of this conversation counts: the signal that starts the
    // check is itself a notification about the coder, and that one is not the
    // check.
    const conversationNews = async () => page.evaluate(async (id) => {
      const res = await fetch("/notifications", { headers: { Accept: "application/json" } });
      const body = await res.json().catch(() => ({}));
      return (body.notifications || []).filter((n) => n.targetId === id).length;
    }, conversation);
    const newsBefore = await conversationNews();
    ring(askCoder);

    // The check's line lands on the job, and nowhere else.
    const seen = await waitJobNote(page, askCoder, /pressed enter/);
    assert(seen.state === "steering", `answering the dialog ended the job: ${JSON.stringify(seen)}`);
    const after = await page.locator("[data-assistant-message]").count();
    assert(after === before, `a WORKING wrote ${after - before} message(s) into the conversation`);
    const newsAfter = await conversationNews();
    assert(newsAfter === newsBefore,
      `a WORKING rang the user: the conversation's news went from ${newsBefore} to ${newsAfter}`);
    return "keys pressed, job open, no message, no push";
  });

  // A check runs in a provider session of its own, so what it consumed is not
  // the conversation's context. Both fakes report a number of their own on a
  // check for exactly this: if the parser wrote every reading onto the
  // conversation, the ring would jump to the check's value and show a number
  // that has nothing to do with the chat.
  await run("a check never moves the conversation's context ring", async () => {
    await page.goto(`${BASE}/assistant/${conversation}`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    await page.waitForSelector("dc-assistant[ready]", { timeout: 15000 });

    // The chat's own turn sets the ring first, so a leak afterwards is visible.
    const before = await page.locator('[data-role="assistant"]').count();
    await page.fill("[data-assistant-input]", "MAGIC set the ring");
    await page.click("[data-assistant-send]");
    await page.waitForFunction(
      (count) => document.querySelectorAll('[data-role="assistant"]').length > count,
      before,
      { timeout: 30000 },
    );
    await page.waitForFunction(
      () => document.querySelector("[data-assistant-ring-fill]")?.getAttribute("stroke-dasharray") === "68 100",
      null,
      { timeout: 30000 },
    );

    const created = await startCoder(page, projectDir, "wake-ring", "Write the file.");
    assert(created.status === 200, `create answered ${created.status}`);
    ringCoder = created.body.id;
    const steered = await post(page, {
      form: "steer",
      terminal: ringCoder,
      task: "Write the file",
      done_when: "WAKE_DONE: the file is there",
    });
    assert(steered.status === 200, `steer answered ${steered.status}`);
    ring(ringCoder);
    await waitJobState(page, ringCoder, "done");

    // The report is in the conversation, the check's context is not.
    await page.reload({ waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    await page.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
    const fill = await page.locator("[data-assistant-ring-fill]").first().getAttribute("stroke-dasharray");
    assert(fill === "68 100", `the check's own context reached the conversation's ring: ${fill}`);
    return "ring still the chat's own 68 percent";
  });

  // Clean up what this run started: the coders and the scratch project.
  await run("cleanup", async () => {
    for (const id of [coderID, nothingCoder, standCoder, busyCoder, stopCoder, preambleCoder, askCoder, ringCoder].filter(Boolean)) {
      await page.evaluate(async (session) => {
        const token = document.querySelector('meta[name="csrf-token"]').content;
        await fetch(`/coders/${session}/delete`, { method: "POST", headers: { "X-CSRF-Token": token } });
      }, id);
    }
    await L.deleteProject(page, project).catch(() => {});
    return "coders and project removed";
  });
});
