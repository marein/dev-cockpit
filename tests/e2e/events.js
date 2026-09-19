const L = require("./lib");
const { assert, BASE, sleep } = L;

// The assistant reacting to events: a subscription made from the page's own
// form (POST /assistants/subscriptions with form=new, the path and the fields
// `subscription-new` posts), the row in the aside under the steered coders,
// a job closing DONE firing it (the check's report as a note of the cockpit,
// the event's note, an answer marked as started by an event), the handover a
// coder started with `then` carries (the field `coder-new --then` posts,
// wired in the create request, refused without a done-when), a cron
// schedule ticking and a NOTHING answer folded away as quiet with no news, a
// note arriving while an answer streams held in the bar above the composer
// and landing under the finished answer, a barrier over three terminals firing
// once after the third, a subscription changed from the row's menu in the very
// form that makes one with the next reaction taking the new task, a target
// added to a barrier that then waits for it, a spent subscription refused, a
// deleted coder closing its job and taking the subscriptions that only it
// could fire, and the row's menu removing a subscription.
//
// The instance MUST be the assistant one (tests/e2e/fakes ahead of the real
// CLIs on PATH, a scratch HOME), and the notify inbox has to be mounted the way
// wake.js needs it:
//
//   -v <state-dir>/notification-inbox/claude:/inbox -e NOTIFY_DIR=/inbox
//
// SHOTS_DIR, optional, is where the desktop and phone screenshots go.
//
// Gotchas:
// - the fake answers a reaction like a chat turn: MAGIC in the task makes it
//   say FLUGHAFEN, EVENT_NOTHING makes it answer NOTHING, and a reaction runs
//   in a session of its own, so nothing streams in the thread for it: its
//   answer is pushed whole, and the subscription's row says reacting while
//   it runs,
// - a cron tick is due at the next full minute, so that check waits up to
//   ninety seconds,
// - SLOW in a subscription's task keeps the reaction open for two minutes,
//   which is how the composer is looked at while a reaction runs,
// - a barrier over three terminals is posted with three `terminal` fields and
//   `mode=all`, the shape the page's multiple select and `--all` post, and its
//   batch window is zero, so the third end fires it at once instead of thirty
//   seconds later,
// - PAUSE_CHAT is the answer that streams half, waits twelve seconds and
//   finishes; two coders rung one after the other inside that pause are how
//   two notes arrive in a known order while an answer streams, and the
//   desktop window is short for that check so the thread overflows and the
//   scroller's position around the landing says something.

const NOTIFY_DIR = process.env.NOTIFY_DIR || "";
const SHOTS_DIR = process.env.SHOTS_DIR || "";
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

async function openAssistant(page, id) {
  await page.goto(`${BASE}/assistants/${id}`, { waitUntil: "domcontentloaded" });
  await dismissUpdate(page);
  await page.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
}

async function ownAssistant(page) {
  const created = await page.evaluate(async () => {
    const body = new URLSearchParams({ form: "new", coder: "claude" });
    body.set("csrf_token", document.querySelector('meta[name="csrf-token"]').content);
    const res = await fetch("/assistants/new", {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded", Accept: "application/json" },
      body: body.toString(),
    });
    return (await res.json().catch(() => ({}))).id || "";
  });
  assert(created, "the run could not start an assistant of its own");
  await openAssistant(page, created);
  return created;
}

// postTo posts a form the way a page does. A field whose value is an array is
// posted once per entry, which is what a multiple select posts and what the
// barrier's terminals travel as.
async function postTo(page, url, fields) {
  return page.evaluate(async ([target, values]) => {
    const body = new URLSearchParams();
    for (const [key, value] of Object.entries(values)) {
      for (const one of Array.isArray(value) ? value : [value]) body.append(key, one);
    }
    body.set("csrf_token", document.querySelector('meta[name="csrf-token"]').content);
    const res = await fetch(target, {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded", Accept: "application/json" },
      body: body.toString(),
    });
    return { status: res.status, body: await res.json().catch(() => ({})) };
  }, [url, fields]);
}

async function startCoder(page, project, name, task) {
  return postTo(page, "/coders/new", { name, project, coder: "claude", automatic_approval: "on", prompt: task });
}

// subscriptionRows reads the fragment the aside's list is fed from, so the
// state comes from the server whatever the aside shows.
async function subscriptionRows(page, assistant) {
  return page.evaluate(async (id) => {
    const res = await fetch(`/assistants/subscriptions?assistant=${id}`, { headers: { Accept: "text/html" } });
    const holder = document.createElement("div");
    holder.innerHTML = await res.text();
    return [...holder.querySelectorAll("[data-assistant-subscription]")].map((row) => ({
      id: row.getAttribute("data-assistant-subscription"),
      event: row.querySelector("[data-assistant-subscription-event]")?.textContent.trim() || "",
      task: row.querySelector("[data-assistant-subscription-task]")?.textContent.trim() || "",
      state: row.querySelector("[data-assistant-subscription-state]")?.textContent.trim() || "",
      pending: row.querySelector("[data-assistant-subscription-pending]")?.textContent.trim() || "",
      facts: row.querySelector("[data-assistant-subscription-facts]")?.textContent.trim() || "",
      note: row.querySelector("[data-assistant-subscription-note]")?.textContent.trim() || "",
      reacting: !!row.querySelector("[data-assistant-subscription-reacting]"),
      // The stand the row hands its form, only while it still fires: that
      // attribute is what puts Edit into the row's menu.
      edit: row.getAttribute("data-assistant-subscription-edit") || "",
    }));
  }, assistant);
}

async function waitRow(page, assistant, id, want, timeout = 30000) {
  const deadline = Date.now() + timeout;
  let seen = null;
  while (Date.now() < deadline) {
    seen = (await subscriptionRows(page, assistant)).find((row) => row.id === id) || null;
    if (seen && want(seen)) return seen;
    await sleep(400);
  }
  assert(false, `subscription ${id} is ${JSON.stringify(seen)}`);
  return seen;
}

// messages reads the thread as the page shows it, in order.
async function messages(page) {
  return page.evaluate(() => [...document.querySelectorAll("[data-assistant-message][data-message-id]")].map((row) => ({
    id: row.getAttribute("data-message-id"),
    role: row.getAttribute("data-role"),
    state: row.getAttribute("data-state"),
    auto: row.hasAttribute("data-assistant-auto"),
    quiet: row.hasAttribute("data-assistant-quiet"),
    note: row.querySelector("[data-assistant-note]")?.getAttribute("data-assistant-note") || "",
    headline: row.querySelector("[data-assistant-note-headline]")?.textContent.trim() || "",
    text: (row.querySelector("[data-assistant-text]")?.textContent || "").trim(),
    origin: !!row.querySelector("[data-assistant-origin]"),
    task: row.querySelector("[data-assistant-origin-task]")?.textContent.trim() || "",
  })));
}

async function waitMessage(page, want, timeout = 40000) {
  const deadline = Date.now() + timeout;
  let seen = [];
  while (Date.now() < deadline) {
    seen = await messages(page);
    const hit = seen.find(want);
    if (hit) return hit;
    await sleep(400);
  }
  assert(false, `no message matched, the thread holds ${JSON.stringify(seen)}`);
  return null;
}

async function newsFor(page, assistant) {
  return page.evaluate(async (id) => {
    const res = await fetch("/notifications", { headers: { Accept: "application/json" } });
    const data = await res.json();
    return (data.notifications || []).filter((n) => n.targetId === id);
  }, assistant);
}

async function shot(page, name) {
  if (!SHOTS_DIR) return;
  await page.screenshot({ path: path.join(SHOTS_DIR, name), fullPage: false });
}

// desktopShot takes the desktop picture at the size the review reads, 1280
// by 800, and puts the runner's own viewport back afterwards.
async function desktopShot(page, name) {
  if (!SHOTS_DIR) return;
  await page.setViewportSize({ width: 1280, height: 800 });
  await sleep(300);
  await shot(page, name);
  await page.setViewportSize({ width: 1360, height: 900 });
  await sleep(300);
}

// tapHeights measures the boxes of the controls a finger has to hit, the
// ones that are rendered; a control under 44px on a phone is a defect.
async function tapHeights(page, selector) {
  return page.evaluate((sel) => [...document.querySelectorAll(sel)]
    .filter((el) => el.getClientRects().length)
    .map((el) => ({ what: (el.getAttribute("aria-label") || el.textContent.trim()).slice(0, 40), h: Math.round(el.getBoundingClientRect().height) })), selector);
}

L.runFeature("events", async ({ page, run, mobilePage }) => {
  assert(NOTIFY_DIR, "NOTIFY_DIR is not set: run with -v <state-dir>/notification-inbox/claude:/inbox -e NOTIFY_DIR=/inbox");

  const project = "zzevents";
  let projectDir = "";
  let assistant = "";
  let coderID = "";
  let heldCoder = "";
  let heldCoder2 = "";
  let slowCoder = "";
  let slowSub = "";
  let jobSub = "";
  let cronSub = "";
  let thenCoder = "";
  let thenSub = "";
  let barrierSub = "";
  let editCoder = "";
  let editSub = "";
  let grownSub = "";
  const barrierCoders = [];
  const grownCoders = [];

  await run("a subscription on a job is made from the page's form", async () => {
    await L.createProject(page, project).catch(() => {});
    projectDir = await L.projectPath(page, project);
    assistant = await ownAssistant(page);
    const created = await startCoder(page, projectDir, "wake-event", "Write the README.");
    assert(created.status === 200, `create answered ${created.status}: ${JSON.stringify(created.body)}`);
    coderID = created.body.id;
    const steered = await postTo(page, "/assistants/jobs", {
      form: "steer", assistant, terminal: coderID, task: "Write the README", done_when: "WAKE_DONE: README.md exists",
    });
    assert(steered.status === 200, `steer answered ${steered.status}: ${JSON.stringify(steered.body)}`);

    // The form posts exactly what `subscription-new` posts, so this is the
    // page's way and the CLI's way at once.
    const made = await postTo(page, "/assistants/subscriptions", {
      form: "new", assistant, event: "job-done", terminal: coderID, task: "MAGIC summarize what the job did", batch: "0",
    });
    assert(made.status === 200, `subscribe answered ${made.status}: ${JSON.stringify(made.body)}`);
    jobSub = made.body.id;
    assert(jobSub && /Job done, wake-event/.test(made.body.summary || ""), `the answer does not name the event: ${JSON.stringify(made.body)}`);

    const refused = await postTo(page, "/assistants/subscriptions", { form: "new", assistant, event: "job-done", task: "" });
    assert(refused.status >= 400, "a subscription without a task was accepted");
    const unknown = await postTo(page, "/assistants/subscriptions", { form: "new", assistant, event: "job-whatever", task: "x" });
    assert(unknown.status >= 400, "an unknown event was accepted");

    await openAssistant(page, assistant);
    const row = await waitRow(page, assistant, jobSub, (r) => /Job done/.test(r.event));
    assert(/wake-event/.test(row.event), `the row does not name the coder: ${JSON.stringify(row)}`);
    assert(row.task === "MAGIC summarize what the job did", `the row does not carry the task: ${JSON.stringify(row)}`);
    assert(row.state === "standing" && /fired 0/.test(row.facts) && /until/.test(row.facts), `the row's state is off: ${JSON.stringify(row)}`);
    // The aside on the page shows the same row.
    const onPage = await page.evaluate((id) => !!document.querySelector(`[data-assistant-subscriptions-list] [data-assistant-subscription="${id}"]`), jobSub);
    assert(onPage, "the page's aside does not list the subscription");
    return `subscription ${jobSub}`;
  });

  await run("a job closing DONE fires it: the reaction's answer is pushed into the thread with its origin", async () => {
    const newsBefore = (await newsFor(page, assistant)).length;
    ring(coderID);

    // The check's report lands first, as a note: the cockpit speaks, never
    // the user, headline first.
    const report = await waitMessage(page, (m) => m.note === "check" && /DONE: wake-event/.test(m.headline));
    assert(report.role === "cockpit", `the report is not a note of the cockpit: ${JSON.stringify(report)}`);
    assert(/job is finished/.test(report.text), `the report's body is missing: ${JSON.stringify(report)}`);

    // The subscription's row says the reaction runs, in a session of its
    // own: nothing streams in the thread for it.
    await waitRow(page, assistant, jobSub, (r) => r.reacting || /fired 1/.test(r.facts));
    // Then the answer, pushed whole: an assistant message marked as started
    // without the user, its origin folded above it, the task behind a tap.
    const answer = await waitMessage(page, (m) => m.role === "assistant" && m.auto && m.state === "complete");
    assert(/FLUGHAFEN/.test(answer.text), `the reaction did not answer the task: ${JSON.stringify(answer)}`);
    assert(answer.origin && /Job done: wake-event/.test(answer.headline), `the answer does not carry its origin: ${JSON.stringify(answer)}`);
    assert(/Task: MAGIC summarize what the job did/.test(answer.task), `the origin does not carry the task: ${JSON.stringify(answer)}`);
    // No note for the event stands in the thread, and no user turn either:
    // the report, then the pushed answer.
    const all = await messages(page);
    assert(!all.find((m) => m.note === "event"), `an event wrote a note into the thread: ${JSON.stringify(all)}`);
    assert(!all.find((m) => m.role === "user"), `an event wrote a user turn: ${JSON.stringify(all)}`);
    const at = (id) => all.findIndex((m) => m.id === id);
    assert(at(report.id) < at(answer.id), `the thread is out of order: ${JSON.stringify(all.map((m) => m.role))}`);
    // The folded origin opens on a tap and closes again.
    const folded = await page.evaluate((id) => {
      const row = document.querySelector(`[data-message-id="${id}"]`);
      return row.querySelector("[data-assistant-origin-task]").getClientRects().length === 0;
    }, answer.id);
    assert(folded, "the origin's task must stand folded");
    await page.click(`[data-message-id="${answer.id}"] [data-assistant-origin-fold]`);
    await page.waitForFunction((id) => document.querySelector(`[data-message-id="${id}"] [data-assistant-origin-task]`)?.getClientRects().length > 0, answer.id, { timeout: 5000 });

    const row = await waitRow(page, assistant, jobSub, (r) => /fired 1/.test(r.facts) && !r.reacting);
    assert(/Answered for Job done: wake-event/.test(row.note), `the row does not say the reaction answered: ${JSON.stringify(row)}`);
    // The answer rings, and the first words of the title say what it is: a
    // trigger fired, so nobody looks for a message they never wrote, with the
    // reaction's own answer below it, which is what the trigger was for.
    const deadline = Date.now() + 10000;
    let news = [];
    while (Date.now() < deadline) {
      news = await newsFor(page, assistant);
      if (news.length > newsBefore) break;
      await sleep(400);
    }
    assert(news.length > newsBefore && /^Trigger fired[ ,.]/.test(news[0].title || ""), `the pushed answer did not ring as a trigger: ${JSON.stringify(news[0])}`);
    assert(/FLUGHAFEN/.test(news[0].detail || ""), `the news does not carry the reaction's answer: ${JSON.stringify(news[0])}`);
    await desktopShot(page, "events-desktop-thread.png");
    return "report, pushed answer with its origin, news naming the trigger";
  });

  // The handover in one call: the sequel of a job is wired in the very
  // request that starts the coder, so nothing can finish between the start
  // and the arrangement. This is the route `coder-new --then` posts to, with
  // the field it sets.
  await run("a coder started with --then carries its sequel, and the job's DONE pushes the sequel's answer", async () => {
    const refused = await postTo(page, "/coders/new", {
      name: "then-nojob", project: projectDir, coder: "claude", automatic_approval: "on",
      prompt: "Write it.", then: "start a reviewer",
    });
    assert(refused.status >= 400, `a sequel without a job was accepted: ${JSON.stringify(refused)}`);
    assert(/A sequel needs a steered job/.test(JSON.stringify(refused.body)), `the refusal does not say why: ${JSON.stringify(refused.body)}`);

    // A browser says which assistant the job reports to, a turn says it with
    // --as; the field is the jobs route's own (steerOwner).
    const created = await postTo(page, "/coders/new", {
      name: "then-event", project: projectDir, coder: "claude", automatic_approval: "on",
      prompt: "Write the README.", done_when: "WAKE_DONE: README.md exists", assistant,
      then: "MAGIC start the reviewer for then-event",
    });
    assert(created.status === 200, `create answered ${created.status}: ${JSON.stringify(created.body)}`);
    thenCoder = created.body.id;
    thenSub = created.body.subscription || "";
    assert(/^[0-9a-f]{16}$/.test(thenSub), `the create does not name the subscription: ${JSON.stringify(created.body)}`);
    assert(!created.body.steerError && !created.body.thenError, `the create reported a failure: ${JSON.stringify(created.body)}`);

    // One shot, on that terminal, standing before the coder can be done.
    await openAssistant(page, assistant);
    const row = await waitRow(page, assistant, thenSub, (r) => /Job done/.test(r.event));
    assert(/then-event/.test(row.event), `the sequel does not name its coder: ${JSON.stringify(row)}`);
    assert(row.state === "once" && row.task === "MAGIC start the reviewer for then-event", `the sequel's row is off: ${JSON.stringify(row)}`);

    // The sequel carries the ordinary bounds, so its window is the default
    // thirty seconds: the check has to report, the window has to close and
    // the reaction has to answer, which is what this wait is long for.
    ring(thenCoder);
    await waitMessage(page, (m) => m.note === "check" && /DONE: then-event/.test(m.headline));
    const answer = await waitMessage(page, (m) => m.role === "assistant" && m.auto && m.state === "complete"
      && /Job done: then-event/.test(m.headline), 120000);
    assert(/FLUGHAFEN/.test(answer.text), `the sequel did not answer its task: ${JSON.stringify(answer)}`);
    assert(/Task: MAGIC start the reviewer for then-event/.test(answer.task), `the sequel's origin does not carry the task: ${JSON.stringify(answer)}`);
    const spent = await waitRow(page, assistant, thenSub, (r) => /fired 1/.test(r.facts) && !r.reacting, 60000);
    assert(spent.state === "done", `a one shot sequel has to end with its turn: ${JSON.stringify(spent)}`);
    return `sequel ${thenSub} wired at the start, answered on DONE`;
  });

  // A barrier over three terminals: one turn at the end, not one per job. The
  // mode the page's select posts is the one `subscription-new --all` posts.
  await run("a barrier over three terminals fires once, after the third", async () => {
    const steer = async (name) => {
      const created = await startCoder(page, projectDir, name, "Write the file.");
      assert(created.status === 200, `create answered ${created.status}: ${JSON.stringify(created.body)}`);
      const steered = await postTo(page, "/assistants/jobs", {
        form: "steer", assistant, terminal: created.body.id, task: "Write the file", done_when: "WAKE_DONE: the file is there",
      });
      assert(steered.status === 200, `steer answered ${steered.status}: ${JSON.stringify(steered.body)}`);
      return created.body.id;
    };
    for (const name of ["all-one", "all-two", "all-three"]) barrierCoders.push(await steer(name));

    const made = await postTo(page, "/assistants/subscriptions", {
      form: "new", assistant, event: "job-closed", mode: "all", batch: "0",
      terminal: barrierCoders, task: "MAGIC summarize all three",
    });
    assert(made.status === 200, `subscribe answered ${made.status}: ${JSON.stringify(made.body)}`);
    barrierSub = made.body.id;
    assert(/all of all-one, all-two, all-three/.test(made.body.summary || ""),
      `the answer does not name the barrier's terminals: ${JSON.stringify(made.body)}`);

    await openAssistant(page, assistant);
    const pushedBefore = (await messages(page)).filter((m) => m.auto).length;
    // The first two ends wait in the window: the row counts them and the
    // barrier stays at fired 0.
    await waitRow(page, assistant, barrierSub, (r) => /all of all-one/.test(r.event));
    ring(barrierCoders[0]);
    await waitMessage(page, (m) => m.note === "check" && /DONE: all-one/.test(m.headline));
    await waitRow(page, assistant, barrierSub, (r) => r.pending === "1");
    ring(barrierCoders[1]);
    await waitMessage(page, (m) => m.note === "check" && /DONE: all-two/.test(m.headline));
    const held = await waitRow(page, assistant, barrierSub, (r) => r.pending === "2");
    assert(/fired 0/.test(held.facts), `a barrier must not fire before its last target: ${JSON.stringify(held)}`);
    const between = (await messages(page)).filter((m) => m.auto).length;
    assert(between === pushedBefore, `the barrier answered ${between - pushedBefore} time(s) too early`);

    // The third closes it: one turn, every report in it.
    ring(barrierCoders[2]);
    await waitMessage(page, (m) => m.note === "check" && /DONE: all-three/.test(m.headline));
    const answer = await waitMessage(page, (m) => m.role === "assistant" && m.auto && m.state === "complete"
      && /3 events arrived/.test(m.headline), 120000);
    assert(/FLUGHAFEN/.test(answer.text), `the barrier did not answer its task: ${JSON.stringify(answer)}`);
    const row = await waitRow(page, assistant, barrierSub, (r) => /fired 1/.test(r.facts) && !r.reacting, 60000);
    assert(row.pending === "", `the window is spent: ${JSON.stringify(row)}`);
    await sleep(1500);
    const after = (await messages(page)).filter((m) => m.auto).length;
    assert(after === pushedBefore + 1, `the barrier answered ${after - pushedBefore} times, want one`);
    return `one turn for three terminals, ${barrierSub}`;
  });

  // A typo in the task is one change away, from the very form that makes one:
  // the row's menu fills it, the event is locked because another event is
  // another subscription, and the next reaction is asked the new task.
  await run("the row's menu changes a subscription in the form that makes one", async () => {
    const created = await startCoder(page, projectDir, "edit-me", "Write the file.");
    assert(created.status === 200, `create answered ${created.status}: ${JSON.stringify(created.body)}`);
    editCoder = created.body.id;
    const made = await postTo(page, "/assistants/subscriptions", {
      form: "new", assistant, event: "coder-ended", terminal: editCoder, batch: "0", task: "MAGIC the frist task",
    });
    assert(made.status === 200, `subscribe answered ${made.status}: ${JSON.stringify(made.body)}`);
    editSub = made.body.id;

    await openAssistant(page, assistant);
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.waitForSelector(`[data-assistant-subscription="${editSub}"]`, { timeout: 8000 });
    await page.click(`[data-assistant-subscription="${editSub}"] [data-assistant-subscription-menu]`);
    const edit = page.locator(".dc-context-menu button", { hasText: "Edit" });
    await edit.waitFor({ state: "visible", timeout: 5000 });
    await edit.click();
    await page.waitForSelector("#assistant-subscribe-form.show", { timeout: 8000 });

    // The form stands filled with what is stored, and the event cannot move.
    const filled = await page.evaluate(() => {
      const form = document.querySelector("[data-assistant-subscribe-form]");
      const picked = [...form.querySelectorAll('select[name="terminal"]')].flatMap((s) => [...s.selectedOptions].map((o) => o.value));
      return {
        action: form.querySelector("[data-subscribe-form-field]").value,
        id: form.querySelector("[data-subscribe-id]").value,
        task: form.querySelector('[name="task"]').value,
        event: form.querySelector('[name="event"]').value,
        locked: form.querySelector('[name="event"]').disabled,
        until: form.querySelector('[name="until"]').value,
        submit: form.querySelector("[data-assistant-subscribe-submit]").textContent.trim(),
        picked,
      };
    });
    assert(filled.action === "edit" && filled.id === editSub, `the form is not on the subscription: ${JSON.stringify(filled)}`);
    assert(filled.task === "MAGIC the frist task", `the form is not filled: ${JSON.stringify(filled)}`);
    assert(filled.event === "coder-ended" && filled.locked, `the event is not locked: ${JSON.stringify(filled)}`);
    assert(filled.until === "", `the expiry must start on the entry that changes nothing: ${JSON.stringify(filled)}`);
    assert(filled.submit === "Save", `the button still says ${filled.submit}`);
    assert(filled.picked.join(",") === editCoder, `the terminal is not picked: ${JSON.stringify(filled)}`);
    await shot(page, "events-desktop-edit.png");

    await page.fill('[data-assistant-subscribe-form] [name="task"]', "MAGIC the second task");
    await page.fill('[data-assistant-subscribe-form] [name="max_per_hour"]', "9");
    await page.click("[data-assistant-subscribe-submit]");
    const toast = page.locator(".dc-toast", { hasText: "Changed:" });
    await toast.waitFor({ state: "visible", timeout: 8000 });
    const said = (await toast.textContent()).trim();
    assert(/task/.test(said) && /9 turns per hour/.test(said), `the answer does not say what changed: ${said}`);

    // The row carries the new task, and nothing of what it already is moved.
    const row = await waitRow(page, assistant, editSub, (r) => r.task === "MAGIC the second task");
    assert(/fired 0/.test(row.facts), `the count moved with the change: ${JSON.stringify(row)}`);
    // And the form is a new subscription's again.
    const back = await page.evaluate(() => {
      const form = document.querySelector("[data-assistant-subscribe-form]");
      return {
        action: form.querySelector("[data-subscribe-form-field]").value,
        locked: form.querySelector('[name="event"]').disabled,
        submit: form.querySelector("[data-assistant-subscribe-submit]").textContent.trim(),
      };
    });
    assert(back.action === "new" && !back.locked && back.submit === "Subscribe",
      `the form did not go back to a new subscription: ${JSON.stringify(back)}`);

    // The next reaction is asked the new task. A coder names its own session,
    // so the headline carries whatever it calls itself; the task is what this
    // check is about.
    ring(editCoder);
    const answer = await waitMessage(page, (m) => m.role === "assistant" && m.auto
      && /Task: MAGIC the second task/.test(m.task), 60000);
    assert(/Coder ended its turn/.test(answer.headline), `the reaction is not the coder's: ${JSON.stringify(answer)}`);
    await page.setViewportSize({ width: 1360, height: 900 });
    return "the task changed and the next reaction took it";
  });

  // A barrier is changed instead of being made again, which is what makes a
  // change worth having: the arrivals it already holds stay, and the target
  // that joins is one more it waits for.
  await run("a target added to a barrier is waited for", async () => {
    const steer = async (name) => {
      const created = await startCoder(page, projectDir, name, "Write the file.");
      assert(created.status === 200, `create answered ${created.status}: ${JSON.stringify(created.body)}`);
      const steered = await postTo(page, "/assistants/jobs", {
        form: "steer", assistant, terminal: created.body.id, task: "Write the file", done_when: "WAKE_DONE: the file is there",
      });
      assert(steered.status === 200, `steer answered ${steered.status}: ${JSON.stringify(steered.body)}`);
      grownCoders.push(created.body.id);
      return created.body.id;
    };
    for (const name of ["grown-one", "grown-two"]) await steer(name);
    const made = await postTo(page, "/assistants/subscriptions", {
      form: "new", assistant, event: "job-closed", mode: "all", batch: "0",
      terminal: grownCoders, task: "MAGIC summarize the group",
    });
    assert(made.status === 200, `subscribe answered ${made.status}: ${JSON.stringify(made.body)}`);
    grownSub = made.body.id;

    await openAssistant(page, assistant);
    const pushedBefore = (await messages(page)).filter((m) => m.auto).length;
    ring(grownCoders[0]);
    await waitMessage(page, (m) => m.note === "check" && /DONE: grown-one/.test(m.headline));
    await waitRow(page, assistant, grownSub, (r) => r.pending === "1");

    // The third joins the barrier, and the arrival that is held stays held.
    const third = await steer("grown-three");
    const changed = await postTo(page, "/assistants/subscriptions", {
      form: "edit", id: grownSub, terminal: grownCoders,
    });
    assert(changed.status === 200, `edit answered ${changed.status}: ${JSON.stringify(changed.body)}`);
    assert(changed.body.changed === "3 terminals", `the answer does not say what changed: ${JSON.stringify(changed.body)}`);
    const grown = await waitRow(page, assistant, grownSub, (r) => /grown-three/.test(r.event));
    assert(grown.pending === "1" && /fired 0/.test(grown.facts), `the change spent the window: ${JSON.stringify(grown)}`);

    // The second closes the pair the barrier started with, and it still waits.
    ring(grownCoders[1]);
    await waitMessage(page, (m) => m.note === "check" && /DONE: grown-two/.test(m.headline));
    const held = await waitRow(page, assistant, grownSub, (r) => r.pending === "2");
    assert(/fired 0/.test(held.facts), `a barrier fired without the target that joined it: ${JSON.stringify(held)}`);
    await sleep(1500);
    const between = (await messages(page)).filter((m) => m.auto).length;
    assert(between === pushedBefore, `the barrier answered ${between - pushedBefore} time(s) too early`);

    // The one that joined closes it: one turn, all three reports in it.
    ring(third);
    await waitMessage(page, (m) => m.note === "check" && /DONE: grown-three/.test(m.headline));
    const answer = await waitMessage(page, (m) => m.role === "assistant" && m.auto && m.state === "complete"
      && /3 events arrived/.test(m.headline), 120000);
    assert(/FLUGHAFEN/.test(answer.text), `the grown barrier did not answer its task: ${JSON.stringify(answer)}`);
    return `the barrier waited for the target that joined it, ${grownSub}`;
  });

  // A subscription that is over is spent: it carries no stand for the form, so
  // its menu has no Edit, and the path refuses a change with one sentence.
  await run("a subscription that is done cannot be changed", async () => {
    const made = await postTo(page, "/assistants/subscriptions", {
      form: "new", assistant, event: "coder-ended", terminal: editCoder, batch: "0", once: "on",
      task: "EVENT_NOTHING the one shot",
    });
    assert(made.status === 200, `subscribe answered ${made.status}: ${JSON.stringify(made.body)}`);
    const spent = made.body.id;
    ring(editCoder);
    const row = await waitRow(page, assistant, spent, (r) => r.state === "done", 60000);
    assert(row.edit === "", `a spent subscription still carries its stand: ${JSON.stringify(row)}`);

    const refused = await postTo(page, "/assistants/subscriptions", { form: "edit", id: spent, task: "try again" });
    assert(refused.status >= 400, `a done subscription was changed: ${JSON.stringify(refused)}`);
    assert(/is done and cannot be changed/.test(refused.body.error || ""), `the refusal is not the sentence: ${JSON.stringify(refused.body)}`);
    // And the event never moves, whichever one is named.
    const moved = await postTo(page, "/assistants/subscriptions", { form: "edit", id: editSub, event: "coder-asks", task: "x" });
    assert(moved.status >= 400, `the event was changed: ${JSON.stringify(moved)}`);
    assert(/cannot change its event/.test(moved.body.error || ""), `the refusal is not the sentence: ${JSON.stringify(moved.body)}`);
    const stood = (await subscriptionRows(page, assistant)).find((r) => r.id === editSub);
    assert(/Coder ended/.test(stood.event), `the event moved anyway: ${JSON.stringify(stood)}`);
    return "a spent subscription and the event are both refused";
  });

  // Deleting a coder is the last thing that terminal does: its job closes with
  // that reason, what waited for the terminal fires once for the deletion, and
  // what could only ever have fired on it is dropped and said out loud.
  await run("deleting a coder closes its job, fires its subscriptions and drops the dead ones", async () => {
    const created = await startCoder(page, projectDir, "doomed-event", "Write the file.");
    assert(created.status === 200, `create answered ${created.status}: ${JSON.stringify(created.body)}`);
    const doomed = created.body.id;
    const steered = await postTo(page, "/assistants/jobs", {
      form: "steer", assistant, terminal: doomed, task: "Write the file", done_when: "WAKE_DONE: the file is there",
    });
    assert(steered.status === 200, `steer answered ${steered.status}: ${JSON.stringify(steered.body)}`);
    const subs = [];
    for (const event of ["coder-asks", "coder-ended"]) {
      const made = await postTo(page, "/assistants/subscriptions", {
        form: "new", assistant, event, terminal: doomed, batch: "0", task: `MAGIC ${event} on the doomed coder`,
      });
      assert(made.status === 200, `subscribe answered ${made.status}: ${JSON.stringify(made.body)}`);
      subs.push(made.body.id);
    }
    // One that outlives the deletion: it names no terminal, so nothing about it
    // is dropped.
    const survivor = await postTo(page, "/assistants/subscriptions", {
      form: "new", assistant, event: "coder-asks", batch: "0", task: "EVENT_NOTHING any coder",
    });
    assert(survivor.status === 200, `subscribe answered ${survivor.status}: ${JSON.stringify(survivor.body)}`);

    await openAssistant(page, assistant);
    const deleted = await postTo(page, `/coders/${doomed}/delete`, {});
    assert(deleted.status === 200, `delete answered ${deleted.status}: ${JSON.stringify(deleted.body)}`);
    // The answer says what it took with it, the sentence the flash and
    // `coder-delete` print.
    assert(deleted.body.dropped === "2 subscriptions dropped",
      `the delete does not name what it dropped: ${JSON.stringify(deleted.body)}`);

    // The job closed with the reason, as a note of the cockpit.
    const report = await waitMessage(page, (m) => m.note === "check" && /EXPIRED: doomed-event/.test(m.headline));
    assert(/the coder was deleted/.test(report.text), `the report does not say why: ${JSON.stringify(report)}`);

    // Both subscriptions fired once for the deletion and are gone from the list.
    const answer = await waitMessage(page, (m) => m.role === "assistant" && m.auto && m.state === "complete"
      && /Coder deleted: doomed-event/.test(m.headline), 60000);
    assert(/FLUGHAFEN/.test(answer.text), `the reaction did not answer: ${JSON.stringify(answer)}`);
    const deadline = Date.now() + 20000;
    let rows = [];
    while (Date.now() < deadline) {
      rows = await subscriptionRows(page, assistant);
      if (!rows.find((r) => subs.includes(r.id))) break;
      await sleep(400);
    }
    assert(!rows.find((r) => subs.includes(r.id)), `a subscription on a deleted coder still stands: ${JSON.stringify(rows)}`);
    assert(rows.find((r) => r.id === survivor.body.id), "a subscription without a terminal must not be dropped");
    const both = (await messages(page)).filter((m) => m.auto && /Coder deleted: doomed-event/.test(m.headline));
    assert(both.length === 2, `want one answer per subscription, got ${both.length}`);
    return `job closed, both fired once, ${deleted.body.dropped}`;
  });

  await run("a schedule ticks and a NOTHING answer pushes nothing", async () => {
    const made = await postTo(page, "/assistants/subscriptions", {
      form: "new", assistant, event: "cron", spec: "* * * * *", task: "EVENT_NOTHING every minute", batch: "0", once: "on",
    });
    assert(made.status === 200, `subscribe answered ${made.status}: ${JSON.stringify(made.body)}`);
    cronSub = made.body.id;
    const row = await waitRow(page, assistant, cronSub, (r) => /Schedule/.test(r.event));
    assert(/\* \* \* \* \*/.test(row.event) && row.state === "once" && /next/.test(row.facts), `the schedule's row is off: ${JSON.stringify(row)}`);

    const newsBefore = (await newsFor(page, assistant)).length;
    const before = await page.locator("[data-assistant-message]").count();
    // The tick fires within the minute; the reaction answers NOTHING, so the
    // row says it fired and had nothing to do, and the thread hears nothing.
    const done = await waitRow(page, assistant, cronSub, (r) => r.state === "done" && /nothing to do/.test(r.note), 95000);
    assert(/fired 1/.test(done.facts), `a one shot that fired is done: ${JSON.stringify(done)}`);
    assert(/Fired for Schedule \* \* \* \* \* ticked at/.test(done.note), `the row does not name the tick: ${JSON.stringify(done)}`);
    await sleep(1500);
    const after = await page.locator("[data-assistant-message]").count();
    assert(after === before, `a NOTHING answer pushed ${after - before} message(s) into the thread`);
    const news = await newsFor(page, assistant);
    assert(news.length === newsBefore, `a NOTHING answer rang the user: ${JSON.stringify(news[0])}`);
    return "tick, nothing pushed, no news";
  });

  await run("two notes arriving while an answer streams wait above the composer in order and land under it without moving the page", async () => {
    // Two steered coders, rung one after the other inside the answer's
    // pause, so two reports arrive in a known order while the answer streams.
    // Each one is steered right after its create, the way every other check
    // does it: the job takes the coder's name as it stands at that moment.
    const steer = async (name) => {
      const created = await startCoder(page, projectDir, name, "Write the file.");
      assert(created.status === 200, `create answered ${created.status}`);
      const steered = await postTo(page, "/assistants/jobs", {
        form: "steer", assistant, terminal: created.body.id, task: "Write the file", done_when: "WAKE_DONE: the file is there",
      });
      assert(steered.status === 200, `steer answered ${steered.status}`);
      return created.body.id;
    };
    heldCoder = await steer("held-first");
    heldCoder2 = await steer("held-second");

    // The phone watches the same thread: opened before the send, it follows
    // the stream and holds the notes the way the desktop does.
    const mp = await mobilePage();
    await openAssistant(mp, assistant);
    await openAssistant(page, assistant);
    // The scroll assertion only says something on a thread that overflows
    // its scroller, so the desktop window is short for this check.
    await page.setViewportSize({ width: 1280, height: 520 });
    await sleep(300);
    const before = await page.locator("[data-assistant-message]").count();
    await page.fill("[data-assistant-input]", "PAUSE_CHAT tell me something");
    await page.click("[data-assistant-send]");
    await page.waitForFunction(() => document.querySelector("dc-assistant")?.hasAttribute("running"), null, { timeout: 15000 });
    await mp.waitForFunction(() => document.querySelector("dc-assistant")?.hasAttribute("running"), null, { timeout: 15000 });
    // The desktop page samples its scroller every frame; when the first note
    // lands in the log it keeps the frame before the landing, the landing
    // itself and the picture 300ms later, after the answer's own fragment
    // and the pin had their say.
    await page.evaluate(() => {
      const scroller = document.querySelector("[data-assistant-scroll]");
      const log = document.querySelector("[data-assistant-log]");
      const read = () => ({ top: scroller.scrollTop, height: scroller.scrollHeight, client: scroller.clientHeight });
      const landing = { last: null, before: null, at: null, after: null, done: false };
      window.__dcLanding = landing;
      const tick = () => {
        if (landing.done) return;
        landing.last = read();
        window.requestAnimationFrame(tick);
      };
      window.requestAnimationFrame(tick);
      new MutationObserver((records) => {
        if (landing.at) return;
        for (const record of records) {
          for (const node of record.addedNodes) {
            if (node.nodeType !== 1 || node.getAttribute("data-role") !== "cockpit") continue;
            landing.before = landing.last;
            landing.at = read();
            window.setTimeout(() => { landing.after = read(); landing.done = true; }, 300);
            return;
          }
        }
      }).observe(log, { childList: true });
    });

    // The checks answer inside the answer's pause, so both reports arrive
    // while the answer streams, the first before the second.
    const rows = () => page.evaluate(() => [...document.querySelectorAll("[data-assistant-held] [data-assistant-held-note]")].map((row) => row.textContent.trim()));
    ring(heldCoder);
    await page.waitForSelector("[data-assistant-held] [data-assistant-held-note]", { timeout: 30000 });
    ring(heldCoder2);
    await page.waitForFunction(() => document.querySelectorAll("[data-assistant-held] [data-assistant-held-note]").length >= 2, null, { timeout: 30000 });
    const bar = await page.evaluate(() => ({
      running: document.querySelector("dc-assistant")?.hasAttribute("running"),
      inLog: !![...document.querySelectorAll("[data-assistant-log] [data-assistant-note]")].find((n) => /held-first|held-second/.test(n.textContent)),
    }));
    assert(bar.running, "the answer must still be streaming while the notes are held");
    assert(!bar.inLog, "a held note must not enter the thread while the answer streams");
    const barRows = await rows();
    assert(barRows.length === 2 && /DONE: held-first/.test(barRows[0]) && /DONE: held-second/.test(barRows[1]),
      `the bar does not hold both notes in arrival order: ${JSON.stringify(barRows)}`);

    await mp.waitForFunction(() => document.querySelectorAll("[data-assistant-held] [data-assistant-held-note]").length >= 2, null, { timeout: 30000 });
    const phoneBar = await mp.evaluate(() => ({
      running: document.querySelector("dc-assistant")?.hasAttribute("running"),
      wide: document.documentElement.scrollWidth > window.innerWidth + 1,
      rows: [...document.querySelectorAll("[data-assistant-held] [data-assistant-held-note]")].map((row) => row.textContent.trim()),
    }));
    assert(phoneBar.running && /DONE: held-first/.test(phoneBar.rows[0] || "") && /DONE: held-second/.test(phoneBar.rows[1] || ""),
      `the phone does not hold both notes in order: ${JSON.stringify(phoneBar)}`);
    assert(!phoneBar.wide, "the phone's held bar overflows sideways");
    const phoneRows = await tapHeights(mp, "[data-assistant-held] button");
    const smallRows = phoneRows.filter((r) => r.h < 44);
    assert(!smallRows.length, `held rows under 44px on the phone: ${JSON.stringify(smallRows)}`);
    await shot(mp, "events-phone-held.png");

    // A tap on a headline opens the whole note there.
    await page.click("[data-assistant-held] [data-assistant-held-note]");
    const opened = await page.evaluate(() => {
      const full = document.querySelector("[data-assistant-held] [data-assistant-held-full]");
      return !!full && !full.hidden && /file is there/.test(full.textContent);
    });
    assert(opened, "the tap did not open the full text in the bar");
    await desktopShot(page, "events-desktop-held.png");
    // Back to the short window for the landing, with the reader at the end.
    await page.setViewportSize({ width: 1280, height: 520 });
    await sleep(300);

    await page.waitForFunction(() => !document.querySelector("dc-assistant")?.hasAttribute("running"), null, { timeout: 40000 });
    await page.waitForFunction(() => !document.querySelector("[data-assistant-held]"), null, { timeout: 10000 });
    await page.waitForFunction(() => window.__dcLanding?.done, null, { timeout: 10000 });
    const landing = await page.evaluate(() => window.__dcLanding);
    assert(landing.before && landing.at && landing.after, `the landing was not observed: ${JSON.stringify(landing)}`);
    assert(landing.before.height > landing.before.client + 20,
      `the thread does not overflow its scroller, the scroll assertion would say nothing: ${JSON.stringify(landing.before)}`);
    assert(landing.before.top + landing.before.client >= landing.before.height - 2,
      `the reader was not at the end before the landing: ${JSON.stringify(landing.before)}`);
    assert(landing.at.top === landing.before.top && landing.after.top === landing.at.top,
      `the page scrolled for the notes: before ${landing.before.top}, at the landing ${landing.at.top}, after it ${landing.after.top}`);
    assert(landing.after.height > landing.before.height, `the notes added no height under the answer: ${JSON.stringify(landing)}`);

    const all = await messages(page);
    const answer = all.find((m) => m.role === "assistant" && /and after it/.test(m.text));
    const first = all.find((m) => m.note === "check" && /DONE: held-first/.test(m.headline));
    const second2 = all.find((m) => m.note === "check" && /DONE: held-second/.test(m.headline));
    assert(answer && first && second2, `the answer or a note is missing: ${JSON.stringify(all)}`);
    assert(all.indexOf(answer) < all.indexOf(first) && all.indexOf(first) < all.indexOf(second2),
      `the held notes do not stand under the finished answer in arrival order: ${JSON.stringify(all.map((m) => m.headline || m.text.slice(0, 20)))}`);
    assert((await page.locator("[data-assistant-message]").count()) >= before + 4, "the sent message, the answer and both notes stand in the thread");
    await page.setViewportSize({ width: 1360, height: 900 });
    return `held in order, landed in order, scrollTop ${landing.before.top} before and ${landing.after.top} after`;
  });

  await run("the composer is the user's alone while a reaction runs, and a chat turn goes through beside it", async () => {
    // A subscription whose task keeps the fake busy (SLOW holds the reaction
    // open), fired by one more steered coder closing DONE.
    const made = await postTo(page, "/assistants/subscriptions", {
      form: "new", assistant, event: "job-done", task: "SLOW keep working on it", batch: "0", once: "on",
    });
    assert(made.status === 200, `subscribe answered ${made.status}: ${JSON.stringify(made.body)}`);
    slowSub = made.body.id;
    const created = await startCoder(page, projectDir, "slow-trigger", "Write the file.");
    assert(created.status === 200, `create answered ${created.status}`);
    slowCoder = created.body.id;
    const steered = await postTo(page, "/assistants/jobs", {
      form: "steer", assistant, terminal: slowCoder, task: "Write the file", done_when: "WAKE_DONE: the file is there",
    });
    assert(steered.status === 200, `steer answered ${steered.status}`);

    await openAssistant(page, assistant);
    ring(slowCoder);
    await waitRow(page, assistant, slowSub, (r) => r.reacting);
    // Exactly as on master: no turn runs in the thread, Send is Send, the
    // Stop is hidden, nothing streams.
    const composer = await page.evaluate(() => ({
      running: document.querySelector("dc-assistant")?.hasAttribute("running"),
      stopHidden: document.querySelector("[data-assistant-cancel]")?.classList.contains("d-none"),
      sendVisible: !!document.querySelector("[data-assistant-send]")?.getClientRects().length,
      sendEnabled: !document.querySelector("[data-assistant-send]")?.disabled,
      streaming: !!document.querySelector('[data-assistant-message][data-state="streaming"]'),
      reacting: !!document.querySelector("[data-assistant-subscriptions-list] [data-assistant-subscription-reacting]"),
    }));
    assert(!composer.running && composer.stopHidden && composer.sendVisible && composer.sendEnabled && !composer.streaming,
      `the composer must be untouched by a reaction: ${JSON.stringify(composer)}`);
    assert(composer.reacting, "the subscription's row must say the reaction runs");

    // A chat turn goes through beside the running reaction: its own session,
    // its own slot, nothing queues.
    await page.fill("[data-assistant-input]", "MAGIC while the reaction runs");
    await page.click("[data-assistant-send]");
    const answer = await waitMessage(page, (m) => m.role === "assistant" && !m.auto && m.state === "complete" && /FLUGHAFEN/.test(m.text), 40000);
    assert(answer, "the chat turn did not answer beside the reaction");
    const queued = await page.evaluate(() => !!document.querySelector("[data-assistant-queued]"));
    assert(!queued, "nothing may queue behind a reaction");
    const still = await subscriptionRows(page, assistant);
    assert((still.find((r) => r.id === slowSub) || {}).reacting, "the reaction is still running beside the chat");
    return "composer untouched, chat answered beside the reaction";
  });

  await run("the phone shows the notes, the aside and the form", async () => {
    const mp = await mobilePage();
    await openAssistant(mp, assistant);
    await mp.waitForFunction(() => document.querySelectorAll("[data-assistant-note]").length >= 2, null, { timeout: 15000 });
    const wide = await mp.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1);
    assert(!wide, "the phone's thread overflows sideways");
    const folds = await tapHeights(mp, "[data-assistant-origin-fold], [data-assistant-note-fold]");
    const smallFolds = folds.filter((f) => f.h < 44);
    assert(!smallFolds.length, `fold controls under 44px on the phone: ${JSON.stringify(smallFolds)}`);
    await shot(mp, "events-phone-thread.png");
    await mp.click("[data-assistant-jobs-button]");
    await mp.waitForSelector("#assistant-aside.show", { timeout: 8000 });
    await mp.waitForSelector("#assistant-aside [data-assistant-subscription]", { timeout: 8000 });
    const asideWide = await mp.evaluate(() => {
      const aside = document.getElementById("assistant-aside");
      return aside.scrollWidth > aside.clientWidth + 1 || document.documentElement.scrollWidth > window.innerWidth + 1;
    });
    assert(!asideWide, "the phone's aside overflows sideways");
    await shot(mp, "events-phone-aside.png");
    await mp.click("[data-assistant-subscribe-open]");
    await mp.waitForSelector("#assistant-subscribe-form.show", { timeout: 8000 });
    const targets = await mp.evaluate(() => [...document.querySelectorAll("#assistant-subscribe-form button, #assistant-subscribe-form select, #assistant-subscribe-form input:not([type=hidden]):not([type=checkbox]), #assistant-subscribe-form textarea")]
      .filter((el) => el.getClientRects().length)
      .map((el) => ({ tag: el.tagName, name: el.name || el.textContent.trim(), h: el.getBoundingClientRect().height })));
    const small = targets.filter((t) => t.tag === "BUTTON" && t.h < 44);
    assert(!small.length, `buttons under 44px on the phone: ${JSON.stringify(small)}`);
    await shot(mp, "events-phone-form.png");
    return `${targets.length} controls, none under the finger`;
  });

  await run("the row's menu removes a subscription", async () => {
    await openAssistant(page, assistant);
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.waitForSelector(`[data-assistant-subscription="${jobSub}"]`, { timeout: 8000 });
    await page.click("[data-assistant-subscribe-open]");
    await page.waitForSelector("#assistant-subscribe-form.show", { timeout: 8000 });
    await sleep(600);
    await shot(page, "events-desktop-form.png");
    await page.click("[data-assistant-subscribe-open]");
    await sleep(600);
    await page.click(`[data-assistant-subscription="${jobSub}"] [data-assistant-subscription-menu]`);
    const remove = page.locator(".dc-context-menu button", { hasText: "Remove" });
    await remove.waitFor({ state: "visible", timeout: 5000 });
    await remove.click();
    await L.confirmSwal(page);
    await page.waitForFunction((id) => !document.querySelector(`[data-assistant-subscription="${id}"]`), jobSub, { timeout: 10000 });
    const rows = await subscriptionRows(page, assistant);
    assert(!rows.find((r) => r.id === jobSub), "the subscription is still stored");
    await page.setViewportSize({ width: 1360, height: 900 });
    return "removed from the row's menu";
  });

  await run("cleanup", async () => {
    for (const id of [coderID, thenCoder, heldCoder, heldCoder2, slowCoder, editCoder, ...barrierCoders, ...grownCoders].filter(Boolean)) {
      await page.evaluate(async (session) => {
        const token = document.querySelector('meta[name="csrf-token"]').content;
        await fetch(`/coders/${session}/delete`, { method: "POST", headers: { "X-CSRF-Token": token } });
      }, id);
    }
    await L.deleteProject(page, project).catch(() => {});
    if (assistant) {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await page.evaluate(async (target) => {
        const token = document.querySelector('meta[name="csrf-token"]').content;
        await fetch(`/assistants/${target}`, {
          method: "POST",
          headers: { "Content-Type": "application/x-www-form-urlencoded", Accept: "application/json", "X-CSRF-Token": token },
          body: "form=delete",
        });
      }, assistant);
    }
    return "coders, project and assistant removed";
  });
});
