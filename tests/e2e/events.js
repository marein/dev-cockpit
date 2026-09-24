const L = require("./lib");
const { assert, BASE, sleep } = L;

// The assistant reacting to events: a trigger made from the page's own
// form (POST /assistants/triggers with form=new, the path and the fields
// `trigger-new` posts, opened in the create dialog like every other form
// that makes something), the row in the aside's Triggers tab, New trigger
// standing at the head of that tab and nowhere else, the two tabs sharing the
// row in equal halves down to a phone, the count on a tab standing raised at
// the word with its bottom edge on the label's middle line and moving nothing
// when it comes and goes, a shut row still carrying its actions while it folds
// only its text, a coder's row leading to no trigger form,
// a job closing DONE firing it (the check's report as a grey note of the
// cockpit and the reaction's answer in that same grey, both unasked,
// the event's note, an answer marked as started by an event), the handover a
// coder started with `then` carries (the field `coder-new --then` posts,
// wired in the create request, refused without a done-when), a cron
// schedule ticking and a NOTHING answer folded away as quiet with no news, an
// optional name as the row's heading with the schedule on the line under it
// and a nameless trigger reading exactly as it did before names existed, that
// same name standing wherever one line is all there is, the header over the
// pushed answer and the notification, the result standing alone under that
// header with no task and no line saying nobody asked for it, a report and a
// pushed answer folding only what the preview cut, with no switch in the
// header and an event leaving an open one open, a
// note arriving while an answer streams held in the bar above the composer
// and landing under the finished answer, a barrier over three terminals firing
// once after the third, a trigger changed from the row's own Change in
// the very form that makes one with the next reaction taking the new task,
// the expiry as a number with a unit beside it that carries No expiry among
// its units, given as 60 minutes and read back as the span the trigger has
// left, a
// target added to a barrier that then waits for it, a spent trigger
// refused, the coder umbrella firing on a signal that was read as another
// kind, a deleted coder closing its job and taking the triggers that
// only it could fire, the task field growing with what is typed up to the
// room the dialog has left, on the desktop and on the phone, and the row's
// own button removing a trigger.
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
//   say AIRPORT, EVENT_NOTHING makes it answer NOTHING, EVENT_LONG makes it
//   answer past the preview a note stands over and WAKE_LONG does the same
//   for a check's report, which is how a fold is looked at at all: a message
//   its preview holds whole folds nothing. A reaction runs in a session of
//   its own, so nothing streams in the thread for it: its answer is pushed
//   whole, and the trigger's row says a reaction runs by showing the working
//   assistant while it does,
// - the task never stands in the thread, so two reactions are told apart by
//   the answer they came back with or by being the fresh one (pushedIDs,
//   newPushed), never by the task they were given,
// - a cron tick is due at the next full minute, so that check waits up to
//   ninety seconds,
// - SLOW in a trigger's task keeps the reaction open for two minutes,
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

// triggerRows reads the fragment the aside's list is fed from, so the
// state comes from the server whatever the aside shows.
async function triggerRows(page, assistant) {
  return page.evaluate(async (id) => {
    const res = await fetch(`/assistants/triggers?assistant=${id}`, { headers: { Accept: "text/html" } });
    const holder = document.createElement("div");
    holder.innerHTML = await res.text();
    return [...holder.querySelectorAll("[data-assistant-trigger]")].map((row) => ({
      id: row.getAttribute("data-assistant-trigger"),
      // The name is the row's heading where there is one, and the event then
      // stands on the line under it: it keeps its marker either way, and its
      // class says which of the two lines it is.
      name: row.querySelector("[data-assistant-trigger-name]")?.textContent.trim() || "",
      event: row.querySelector("[data-assistant-trigger-event]")?.textContent.trim() || "",
      eventIsHeading: !!row.querySelector("[data-assistant-trigger-event].fw-medium"),
      nameFirst: row.querySelector("[data-assistant-trigger-name], [data-assistant-trigger-event]")?.hasAttribute("data-assistant-trigger-name") || false,
      task: row.querySelector("[data-assistant-trigger-task]")?.textContent.trim() || "",
      state: row.querySelector("[data-assistant-trigger-state]")?.dataset.assistantTriggerState || "",
      pending: row.querySelector("[data-assistant-trigger-pending]")?.textContent.trim() || "",
      facts: row.querySelector("[data-assistant-trigger-facts]")?.textContent.trim() || "",
      note: row.querySelector("[data-assistant-trigger-note]")?.textContent.trim() || "",
      reacting: !!row.querySelector('[data-assistant-working="react"]'),
      // The way to the form that changes it, only while it still fires: a
      // spent trigger carries no Change button at all.
      edit: row.querySelector("[data-assistant-trigger-edit]")?.getAttribute("href") || "",
    }));
  }, assistant);
}

// counts reads the three numbers the aside carries: one per tab and the sum
// on the button, which is all a phone sees of it.
async function counts(page) {
  return page.evaluate(() => {
    const read = (sel) => {
      const el = document.querySelector(sel);
      return el && !el.classList.contains("d-none") ? Number(el.textContent) : 0;
    };
    return {
      coders: read("[data-assistant-coders-tab] dc-steer-badge"),
      triggers: read("[data-assistant-triggers-tab] dc-steer-badge"),
      watching: read("[data-assistant-watching-button] dc-steer-badge"),
    };
  });
}

async function waitFor(read, timeout = 15000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    const seen = await read();
    if (seen) return seen;
    await sleep(400);
  }
  return null;
}

async function waitRow(page, assistant, id, want, timeout = 30000) {
  const deadline = Date.now() + timeout;
  let seen = null;
  while (Date.now() < deadline) {
    seen = (await triggerRows(page, assistant)).find((row) => row.id === id) || null;
    if (seen && want(seen)) return seen;
    await sleep(400);
  }
  assert(false, `trigger ${id} is ${JSON.stringify(seen)}`);
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
    stripe: getComputedStyle(row).borderLeftColor,
    all: (row.textContent || "").trim(),
  })));
}

// pushedIDs is what the thread already holds of the reactions. With the task
// out of the thread every pushed answer reads alike, so what tells a fresh one
// from the ones before it is that it is fresh.
function pushedIDs(rows) {
  return new Set(rows.filter((m) => m.auto).map((m) => m.id));
}

async function newPushed(page, before, timeout = 60000) {
  return waitMessage(page, (m) => m.auto && m.state === "complete" && !before.has(m.id), timeout);
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
  let namedCron = "";
  let bareCron = "";
  let thenCoder = "";
  let thenSub = "";
  let barrierSub = "";
  let editCoder = "";
  let longCoder = "";
  let editSub = "";
  let grownSub = "";
  const barrierCoders = [];
  const grownCoders = [];

  await run("a trigger on a job is made from the page's form", async () => {
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

    // The form posts exactly what `trigger-new` posts, so this is the
    // page's way and the CLI's way at once.
    const made = await postTo(page, "/assistants/triggers", {
      form: "new", assistant, event: "job-done", terminal: coderID, task: "MAGIC summarize what the job did", batch: "0",
    });
    assert(made.status === 200, `the trigger answered ${made.status}: ${JSON.stringify(made.body)}`);
    jobSub = made.body.id;
    assert(jobSub && /Job done, wake-event/.test(made.body.summary || ""), `the answer does not name the event: ${JSON.stringify(made.body)}`);

    const refused = await postTo(page, "/assistants/triggers", { form: "new", assistant, event: "job-done", task: "" });
    assert(refused.status >= 400, "a trigger without a task was accepted");
    const unknown = await postTo(page, "/assistants/triggers", { form: "new", assistant, event: "job-whatever", task: "x" });
    assert(unknown.status >= 400, "an unknown event was accepted");

    await openAssistant(page, assistant);
    const row = await waitRow(page, assistant, jobSub, (r) => /Job done/.test(r.event));
    assert(/wake-event/.test(row.event), `the row does not name the coder: ${JSON.stringify(row)}`);
    assert(row.task === "MAGIC summarize what the job did", `the row does not carry the task: ${JSON.stringify(row)}`);
    // Nobody named an expiry, so the trigger stands until it is removed.
    assert(row.state === "standing" && /fired 0/.test(row.facts) && /no expiry/.test(row.facts), `the row's state is off: ${JSON.stringify(row)}`);
    // The aside on the page shows the same row.
    const onPage = await page.evaluate((id) => !!document.querySelector(`[data-assistant-triggers-list] [data-assistant-trigger="${id}"]`), jobSub);
    assert(onPage, "the page's aside does not list the trigger");
    return `trigger ${jobSub}`;
  });

  // The plus used to hang beside the two tabs, so it stood there with Steered
  // coders open: a control next to both halves that only ever made a trigger.
  // It belongs to the triggers and is the tab pane's own child now, which is
  // what makes it go away with the tab without anybody switching a visibility.
  await run("New trigger stands at the head of the triggers tab and nowhere else", async () => {
    await openAssistant(page, assistant);
    await page.setViewportSize({ width: 1440, height: 900 });
    const plus = page.locator("#assistant-triggers [data-assistant-trigger-new]");
    await plus.waitFor({ state: "attached", timeout: 15000 });
    assert(!(await plus.isVisible()), "it shows while the coders tab is the open one");
    const strip = await page.evaluate(() =>
      !!document.querySelector("[data-assistant-coders-tab]")?.closest("[role=tablist]")?.parentElement?.querySelector(":scope > [data-assistant-trigger-new]"));
    assert(!strip, "it still hangs beside the tab strip");

    await page.click("[data-assistant-triggers-tab]");
    await plus.waitFor({ state: "visible", timeout: 8000 });
    // A word and a plus, over the full width of the list it heads, carrying no
    // colour of its own and at the weight a trigger row's own actions wear.
    const shape = await page.evaluate(() => {
      const button = document.querySelector("#assistant-triggers [data-assistant-trigger-new]");
      const list = document.querySelector("#assistant-triggers [data-assistant-body]");
      const b = button.getBoundingClientRect();
      const l = list.getBoundingClientRect();
      return {
        text: button.textContent.trim(),
        icon: !!button.querySelector(".ti-plus"),
        href: button.getAttribute("href"),
        above: b.bottom <= l.top + 1,
        outside: !list.contains(button),
        full: Math.abs(b.width - l.width) <= 1 && Math.abs(b.x - l.x) <= 1,
        small: button.classList.contains("btn-sm"),
        colour: [...button.classList].some((c) => /^btn-(outline-)?(primary|secondary|success|danger|warning|info)$/.test(c)),
      };
    });
    assert(shape.text === "New trigger" && shape.icon, `the button is bare: ${JSON.stringify(shape)}`);
    assert(/form=new/.test(shape.href), `the button lost the form it opens: ${JSON.stringify(shape)}`);
    assert(shape.above && shape.outside, `the button is not the head of the list: ${JSON.stringify(shape)}`);
    assert(shape.full, `the button does not run the width of the list: ${JSON.stringify(shape)}`);
    assert(shape.small && !shape.colour, `the button shouts over the rows below it: ${JSON.stringify(shape)}`);
    await page.click("[data-assistant-coders-tab]");
    return shape.text;
  });

  // What the form says about a trigger before anything is typed: a task may
  // answer with nothing at all, a trigger nobody bounds stands until it is
  // removed, and a schedule has no batch window, so the field goes away with
  // the event rather than standing there meaning nothing.
  await run("the form says what a task may answer and drops what a schedule has no use for", async () => {
    await openAssistant(page, assistant);
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.click("[data-assistant-triggers-tab]");
    await page.click("#assistant-triggers [data-assistant-trigger-new]");
    await page.waitForSelector(".modal.show [data-assistant-trigger-form]", { timeout: 8000 });

    const read = () => page.evaluate(() => {
      const form = document.querySelector(".modal.show [data-assistant-trigger-form]");
      const batch = form.querySelector('[data-trigger-batch]');
      const box = batch.getBoundingClientRect();
      const until = form.querySelector('[name="until"]');
      const unit = form.querySelector('[name="untilUnit"]');
      return {
        hint: form.querySelector('[name="task"]').closest(".mb-2").querySelector(".form-hint")?.textContent.trim() || "",
        event: form.querySelector('[name="event"]').value,
        until: until.value,
        untilType: until.type,
        untilWidth: Math.round(until.getBoundingClientRect().width),
        units: [...unit.options].map((o) => o.textContent.trim()),
        unit: unit.value,
        numberPosts: !until.disabled,
        batchShown: box.width > 0 && box.height > 0,
        batchPosts: !form.querySelector('[name="batch"]').disabled,
        model: {
          options: [...form.querySelectorAll('select[name="model"] option')].map((o) => o.textContent.trim()),
          value: form.querySelector('select[name="model"]').value,
          typedHidden: form.querySelector("[data-model-other-input]").hidden && form.querySelector("[data-model-other-input]").disabled,
          afterTask: form.querySelector('[name="task"]').closest(".mb-2").nextElementSibling?.contains(form.querySelector('select[name="model"]')) || false,
        },
      };
    });

    const fresh = await read();
    // The model the reaction runs on stands right after the task: the empty
    // entry is the assistant default, named as it resolves (this assistant
    // picked nothing, so the CLI), the coder's list follows, and Other… is
    // the way past it, with the typed field posting nothing until it is
    // picked.
    assert(fresh.model.options[0] === "Assistant default (CLI)" && fresh.model.options.includes("haiku") && fresh.model.options[fresh.model.options.length - 1] === "Other…",
      `the model select is off: ${JSON.stringify(fresh.model)}`);
    assert(fresh.model.value === "" && fresh.model.typedHidden && fresh.model.afterTask, `the model select does not start on the assistant's own after the task: ${JSON.stringify(fresh.model)}`);
    assert(/NOTHING/.test(fresh.hint) && /notifies nobody/.test(fresh.hint),
      `the task field does not say what NOTHING does: ${JSON.stringify(fresh)}`);
    // No expiry is where a new trigger starts, and it is the unit select that
    // says so over an empty number: that entry leaves nothing to count, so the
    // number is disabled and posts nothing. It is called No expiry and not
    // Never, because the label over it reads Until. The units beside it are
    // what the flag takes, so the page can say every span the command can and
    // nothing beyond it.
    assert(fresh.until === "" && fresh.untilType === "number" && fresh.unit === "never",
      `the expiry does not start on none: ${JSON.stringify(fresh)}`);
    assert(fresh.units.join(",") === "minutes,hours,days,No expiry", `the units are off: ${JSON.stringify(fresh)}`);
    assert(!fresh.numberPosts, `the entry does not take the number with it: ${JSON.stringify(fresh)}`);
    // The field has the row to itself, so the number is wide enough to read
    // its placeholder in; beside the window there was no room left for it.
    assert(fresh.untilWidth >= 200, `the number field is too narrow to read: ${JSON.stringify(fresh)}`);
    assert(fresh.batchShown && fresh.batchPosts, `the batch window is not offered: ${JSON.stringify(fresh)}`);
    await shot(page, "events-desktop-form-nothing.png");

    // Picking a unit hands the number back, which is the whole switching the
    // select does.
    await page.selectOption('.modal.show [data-assistant-trigger-form] [name="untilUnit"]', "h");
    const span = await read();
    assert(span.numberPosts && span.unit === "h", `picking a unit leaves the number disabled: ${JSON.stringify(span)}`);
    await page.selectOption('.modal.show [data-assistant-trigger-form] [name="untilUnit"]', "never");

    await page.selectOption('.modal.show [data-assistant-trigger-form] [name="event"]', "cron");
    await sleep(200);
    const schedule = await read();
    assert(schedule.event === "cron", `the event did not move: ${JSON.stringify(schedule)}`);
    // Hidden and disabled together: a field that posts nothing is one the
    // server leaves alone.
    assert(!schedule.batchShown && !schedule.batchPosts, `a schedule still carries a batch window: ${JSON.stringify(schedule)}`);
    await shot(page, "events-desktop-form-cron.png");

    await page.setViewportSize({ width: 390, height: 844 });
    await sleep(300);
    const phone = await read();
    assert(!phone.batchShown, `the phone shows a schedule a batch window: ${JSON.stringify(phone)}`);
    await shot(page, "events-phone-form-cron.png");
    await page.click('.modal.show .modal-footer [data-bs-dismiss="modal"]');
    await page.waitForSelector(".modal.show", { state: "hidden", timeout: 8000 });
    await page.setViewportSize({ width: 1360, height: 900 });
    return "the hint stands and the window goes with the schedule";
  });

  // A trigger keeps the model it was posted with: the row's fold and the JSON
  // `trigger-list` reads both say it, a name no CLI takes is refused, and an
  // empty post clears it back to the assistant's, which is what `--model
  // default` posts.
  await run("a trigger keeps a model of its own and an empty post clears it", async () => {
    const made = await postTo(page, "/assistants/triggers", {
      form: "new", assistant, event: "cron", spec: "0 3 * * *", task: "EVENT_NOTHING the modelled one", model: " haiku ",
    });
    assert(made.status === 200, `the trigger answered ${made.status}: ${JSON.stringify(made.body)}`);
    const id = made.body.id;
    const listed = async () => {
      const res = await page.request.get(`${BASE}/assistants/triggers?assistant=${assistant}`, { headers: { Accept: "application/json" } });
      return ((await res.json()).triggers || []).find((row) => row.id === id) || {};
    };
    assert((await listed()).model === "haiku", `the listing does not carry the model: ${JSON.stringify(await listed())}`);
    await openAssistant(page, assistant);
    const row = await waitRow(page, assistant, id, (r) => /model haiku/.test(r.facts));
    assert(/model haiku/.test(row.facts), `the row does not say the model: ${JSON.stringify(row)}`);
    const refused = await postTo(page, "/assistants/triggers", { form: "edit", id, model: "two words" });
    assert(refused.status >= 400, "a model no CLI takes was accepted");
    assert((await listed()).model === "haiku", "a refused name changed the model");
    const cleared = await postTo(page, "/assistants/triggers", { form: "edit", id, model: "" });
    assert(cleared.status === 200 && /the assistant default/.test(cleared.body.changed || ""), `the change does not name the model: ${JSON.stringify(cleared.body)}`);
    assert((await listed()).model === "", "an empty post did not clear the model");
    const gone = await postTo(page, "/assistants/triggers", { form: "remove", id });
    assert(gone.status === 200, `the trigger could not be removed: ${JSON.stringify(gone.body)}`);
    return "haiku on the row and in the list, refused when wrong, cleared by an empty post";
  });

  // The two halves are the row's width and not their text: `Steered coders` is
  // the longer name and used to take the larger share, which left the strip
  // crooked. Equal halves only hold while the longer name fits into one, so
  // this is measured where the room runs out, on the phone.
  await run("the two tabs share the row in equal halves, on a phone too", async () => {
    const measure = (p) => p.evaluate(() => {
      const strip = document.querySelector("[data-assistant-coders-tab]").parentElement;
      const read = (sel) => {
        const el = document.querySelector(sel);
        const rect = el.getBoundingClientRect();
        const style = getComputedStyle(el);
        // The content is the icon, the words and the count, measured as they
        // are laid out, so what is left over is the room the half still has.
        let content = 0;
        for (const node of el.childNodes) {
          if (node.nodeType === Node.TEXT_NODE) {
            if (!node.textContent.trim()) continue;
            const range = document.createRange();
            range.selectNodeContents(node);
            content += range.getBoundingClientRect().width;
          } else if (node.nodeType === Node.ELEMENT_NODE && !node.classList.contains("d-none")) {
            const cs = getComputedStyle(node);
            content += node.getBoundingClientRect().width + parseFloat(cs.marginLeft) + parseFloat(cs.marginRight);
          }
        }
        return {
          width: rect.width,
          content,
          // One line, whatever the padding around it is.
          lines: Math.round((el.scrollHeight - (parseFloat(style.paddingTop) + parseFloat(style.paddingBottom))) / parseFloat(style.lineHeight)),
        };
      };
      return { strip: strip.getBoundingClientRect().width, coders: read("[data-assistant-coders-tab]"), triggers: read("[data-assistant-triggers-tab]") };
    });
    const check = (where, m) => {
      assert(Math.abs(m.coders.width - m.triggers.width) <= 1,
        `${where}: the halves are not equal: ${JSON.stringify(m)}`);
      assert(Math.abs(m.coders.width + m.triggers.width - m.strip) <= 1,
        `${where}: the halves do not fill the row: ${JSON.stringify(m)}`);
      for (const [name, tab] of [["Steered coders", m.coders], ["Triggers", m.triggers]]) {
        assert(tab.lines === 1, `${where}: ${name} wrapped: ${JSON.stringify(m)}`);
        assert(tab.content <= tab.width, `${where}: ${name} does not fit its half: ${JSON.stringify(m)}`);
      }
    };

    await openAssistant(page, assistant);
    await page.setViewportSize({ width: 1440, height: 900 });
    await sleep(300);
    const desktop = await measure(page);
    check("desktop", desktop);

    const mp = await mobilePage();
    await openAssistant(mp, assistant);
    await mp.click("[data-assistant-watching-button]");
    await mp.waitForSelector("#assistant-aside.show", { timeout: 8000 });
    await sleep(400);
    const phone = await measure(mp);
    check("phone", phone);
    return `halves ${desktop.coders.width.toFixed(0)}px on the desktop, ${phone.coders.width.toFixed(0)}px on the phone, the longer name needing ${phone.coders.content.toFixed(0)}px`;
  });

  // The count is a note at the word, not a second word beside it: it stands
  // raised, its bottom edge on the label's middle line, which covers the upper
  // half of the ink the way every corner badge in the cockpit covers the top of
  // its glyph. Out of the flow is what keeps the row still while a number comes
  // and goes, and ending inside the tab's own padding is what keeps it off the
  // row above and out of the scroller's clip.
  await run("the tab's count stands raised at the word and moves nothing", async () => {
    const measure = (p) => p.evaluate(() => {
      const read = (sel) => {
        const tab = document.querySelector(sel);
        const strip = tab.parentElement;
        const label = tab.querySelector("[data-assistant-tab-label]");
        const badge = label.querySelector(".dc-steer-badge");
        const was = badge.className;
        const stand = () => `${strip.getBoundingClientRect().height} ${tab.getBoundingClientRect().top}`;
        badge.classList.remove("d-none");
        const on = stand();
        const b = badge.getBoundingClientRect();
        const l = label.getBoundingClientRect();
        const t = tab.getBoundingClientRect();
        // The ink is the word as it is laid out, its own text node measured.
        const range = document.createRange();
        range.selectNodeContents(label.firstChild);
        const ink = range.getBoundingClientRect();
        // Zero hides the badge exactly this way, see steer-badge.js.
        badge.classList.add("d-none");
        const off = stand();
        badge.className = was;
        let clip = tab;
        while (clip && getComputedStyle(clip).overflowY === "visible") clip = clip.parentElement;
        return {
          position: getComputedStyle(badge).position,
          still: on === off,
          onMid: +(l.top + l.height / 2 - b.bottom).toFixed(1),
          covers: Math.round(((Math.min(b.bottom, ink.bottom) - Math.max(b.top, ink.top)) / ink.height) * 100),
          inTab: +(b.top - t.top).toFixed(1),
          toClip: clip ? +(b.top - clip.getBoundingClientRect().top).toFixed(1) : null,
          beside: +(b.left - l.right).toFixed(1),
        };
      };
      return { coders: read("[data-assistant-coders-tab]"), triggers: read("[data-assistant-triggers-tab]") };
    });
    const check = (where, m) => {
      for (const [name, tab] of [["Steered coders", m.coders], ["Triggers", m.triggers]]) {
        assert(tab.position === "absolute", `${where}: ${name} carries its count in the flow: ${JSON.stringify(tab)}`);
        assert(tab.still, `${where}: ${name} moves when the number comes and goes: ${JSON.stringify(tab)}`);
        assert(Math.abs(tab.onMid) <= 1, `${where}: ${name} does not stand on the word's middle line: ${JSON.stringify(tab)}`);
        assert(tab.inTab > 0, `${where}: ${name} raises its count out of the tab: ${JSON.stringify(tab)}`);
        assert(tab.toClip > 0, `${where}: ${name} raises its count into the clip: ${JSON.stringify(tab)}`);
        assert(tab.beside > 0, `${where}: ${name} lays its count over the word: ${JSON.stringify(tab)}`);
      }
    };

    await openAssistant(page, assistant);
    await page.setViewportSize({ width: 1440, height: 900 });
    await sleep(300);
    const desktop = await measure(page);
    check("desktop", desktop);

    const mp = await mobilePage();
    await openAssistant(mp, assistant);
    if (!(await mp.locator("#assistant-aside.show").count())) {
      await mp.click("[data-assistant-watching-button]");
      await mp.waitForSelector("#assistant-aside.show", { timeout: 8000 });
    }
    await sleep(400);
    const phone = await measure(mp);
    check("phone", phone);
    return `raised over ${desktop.coders.covers}% of the ink on the desktop and ${phone.coders.covers}% on the phone, ${desktop.coders.inTab}px inside the tab and ${desktop.coders.toClip}px clear of the clip`;
  });

  // The fold holds the text and nothing else. Acting on a coder or a trigger
  // is the everyday case, so its actions stand under the row whether it is
  // open or shut; only what the coder was sent, what it is measured against
  // and what came back is behind the chevron.
  await run("a shut row still carries its actions, and folds only its text", async () => {
    await openAssistant(page, assistant);
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.waitForSelector(`[data-assistant-job="${coderID}"]`, { timeout: 15000 });
    const read = (kind, id, actions, text) => page.evaluate(([kind, id, actions, text]) => {
      const row = document.querySelector(`[data-assistant-${kind}="${id}"]`);
      const fold = row.querySelector(`[data-assistant-${kind}-details]`);
      const shown = (sel) => {
        const el = row.querySelector(sel);
        return el ? el.getClientRects().length > 0 : null;
      };
      return {
        open: fold.classList.contains("show"),
        actions: Object.fromEntries(actions.map((sel) => [sel, shown(sel)])),
        text: shown(text),
        // Nothing that acts may sit inside the fold any more.
        inFold: [...fold.querySelectorAll("a[href], button")].map((el) => el.textContent.trim()),
      };
    }, [kind, id, actions, text]);

    const jobActions = ["[data-assistant-job-open]", "[data-assistant-job-stop]"];
    const shutJob = await read("job", coderID, jobActions, "[data-assistant-job-done-when]");
    assert(!shutJob.open, `the row started open: ${JSON.stringify(shutJob)}`);
    assert(Object.values(shutJob.actions).every(Boolean), `a shut coder row hides its actions: ${JSON.stringify(shutJob)}`);
    assert(!shutJob.text, `a shut coder row shows the text it folds: ${JSON.stringify(shutJob)}`);
    assert(!shutJob.inFold.length, `the coder row still folds something that acts: ${JSON.stringify(shutJob)}`);

    await page.click(`[data-assistant-job="${coderID}"] [data-assistant-job-fold]`);
    await page.waitForSelector(`[data-assistant-job="${coderID}"] [data-assistant-job-details].show`, { timeout: 8000 });
    const openJob = await read("job", coderID, jobActions, "[data-assistant-job-done-when]");
    assert(openJob.text, `the open coder row does not show its text: ${JSON.stringify(openJob)}`);
    assert(Object.values(openJob.actions).every(Boolean), `the open coder row lost its actions: ${JSON.stringify(openJob)}`);
    await page.click(`[data-assistant-job="${coderID}"] [data-assistant-job-fold]`);

    await page.click("[data-assistant-triggers-tab]");
    await page.waitForSelector(`[data-assistant-trigger="${jobSub}"]`, { timeout: 8000 });
    const triggerActions = ["[data-assistant-trigger-edit]", "[data-assistant-trigger-remove]"];
    const shutTrigger = await read("trigger", jobSub, triggerActions, "[data-assistant-trigger-task]");
    assert(!shutTrigger.open, `the trigger row started open: ${JSON.stringify(shutTrigger)}`);
    assert(Object.values(shutTrigger.actions).every(Boolean), `a shut trigger row hides its actions: ${JSON.stringify(shutTrigger)}`);
    assert(!shutTrigger.text, `a shut trigger row shows the text it folds: ${JSON.stringify(shutTrigger)}`);
    assert(!shutTrigger.inFold.length, `the trigger row still folds something that acts: ${JSON.stringify(shutTrigger)}`);
    await page.click("[data-assistant-coders-tab]");
    return "both rows act while shut and fold only their text";
  });

  // The aside holds two kinds of standing work, and one way in to each. A
  // trigger is made over the list and nowhere else, so a steered coder's row
  // offers no way to one, and the counts on the two heads add up to the one on
  // the button a phone sees.
  await run("a trigger is made over the list alone, and the counts cover both halves", async () => {
    await openAssistant(page, assistant);
    await page.waitForSelector(`[data-assistant-job="${coderID}"] [data-assistant-job-open]`, { state: "attached", timeout: 15000 });
    const before = await counts(page);
    assert(before.coders === 1 && before.triggers === 1 && before.watching === 2,
      `the heads do not add up to the button: ${JSON.stringify(before)}`);

    // Nothing on the coder's row reaches the trigger form any more, neither
    // beside the actions nor inside the fold.
    await page.click(`[data-assistant-job="${coderID}"] [data-assistant-job-fold]`);
    await page.waitForSelector(`[data-assistant-job="${coderID}"] [data-assistant-job-details].show`, { timeout: 8000 });
    const toTrigger = await page.evaluate((id) => {
      const row = document.querySelector(`[data-assistant-job="${id}"]`);
      return [...row.querySelectorAll("a[href]")].filter((a) => a.getAttribute("href").includes("/assistants/triggers")).length;
    }, coderID);
    assert(toTrigger === 0, `the coder's row still leads to the trigger form ${toTrigger} times`);
    await page.click(`[data-assistant-job="${coderID}"] [data-assistant-job-fold]`);

    // The one way in is the button over the list, and what it makes moves the
    // counts live, over the assistant event.
    await page.click("[data-assistant-triggers-tab]");
    await page.click("#assistant-triggers [data-assistant-trigger-new]");
    await page.waitForSelector(".modal.show [data-assistant-trigger-form]", { timeout: 8000 });
    await sleep(300);
    await page.selectOption(".modal.show [data-trigger-event]", "job-done");
    await page.selectOption(".modal.show #assistant-trigger-job", coderID);
    await page.fill(".modal.show #assistant-trigger-task", "MAGIC the sequel over the list");
    await page.click('.modal.show button[type="submit"]');
    await page.waitForSelector(".modal.show", { state: "hidden", timeout: 8000 });
    const rowSub = await waitFor(async () => {
      const rows = await triggerRows(page, assistant);
      return rows.find((r) => r.task === "MAGIC the sequel over the list")?.id || null;
    }, 15000);
    assert(rowSub, "the button over the list made no trigger");
    await page.waitForFunction(() => Number(document.querySelector("[data-assistant-triggers-tab] dc-steer-badge")?.textContent) === 2,
      null, { timeout: 15000 });
    const after = await counts(page);
    assert(after.coders === 1 && after.triggers === 2 && after.watching === 3,
      `the counts did not follow: ${JSON.stringify(after)}`);
    await page.click("[data-assistant-coders-tab]");

    // It is taken back right away: what this run checks after it expects the
    // job to fire one trigger, not two.
    const gone = await postTo(page, "/assistants/triggers", { form: "remove", id: rowSub });
    assert(gone.status === 200, `remove answered ${gone.status}`);
    return `made ${rowSub} over the list`;
  });

  // A live update must not take what the reader opened out from under them.
  // Both lists swap their rows on every assistant event, so the tab and the
  // unfolded rows are what a swap is measured against: the strip stands
  // outside the swapped body, an open fold is carried onto the row that
  // arrives.
  await run("an event swaps the lists without closing what is open", async () => {
    await openAssistant(page, assistant);
    await page.click(`[data-assistant-job="${coderID}"] [data-assistant-job-fold]`);
    await page.waitForSelector(`[data-assistant-job="${coderID}"] [data-assistant-job-details].show`, { timeout: 8000 });
    await page.click("[data-assistant-triggers-tab]");
    await page.click(`[data-assistant-trigger="${jobSub}"] [data-assistant-trigger-fold]`);
    await page.waitForSelector(`[data-assistant-trigger="${jobSub}"] [data-assistant-trigger-details].show`, { timeout: 8000 });
    // Both bodies are marked, so what follows proves they really were
    // swapped and not merely left alone.
    await page.evaluate(() => {
      for (const body of document.querySelectorAll("#assistant-aside [data-assistant-body]")) body.dataset.probe = "1";
    });

    const made = await postTo(page, "/assistants/triggers", {
      form: "new", assistant, event: "cron", spec: "0 4 * * *", task: "EVENT_NOTHING the quiet one", batch: "0",
    });
    assert(made.status === 200, `the trigger answered ${made.status}: ${JSON.stringify(made.body)}`);
    await page.waitForSelector(`[data-assistant-trigger="${made.body.id}"]`, { timeout: 15000 });
    await sleep(600);
    const kept = await page.evaluate((ids) => ({
      swapped: document.querySelectorAll("#assistant-aside [data-assistant-body][data-probe]").length,
      tab: !!document.querySelector("[data-assistant-triggers-tab].active"),
      pane: !!document.querySelector("#assistant-triggers.active"),
      plus: !!document.querySelector("#assistant-triggers [data-assistant-trigger-new]"),
      job: !!document.querySelector(`[data-assistant-job="${ids.job}"] [data-assistant-job-details].show`),
      trigger: !!document.querySelector(`[data-assistant-trigger="${ids.sub}"] [data-assistant-trigger-details].show`),
    }), { job: coderID, sub: jobSub });
    assert(kept.swapped === 0, "the lists did not swap, so this proves nothing");
    assert(kept.tab && kept.pane, `the swap moved the chosen tab: ${JSON.stringify(kept)}`);
    assert(kept.plus, `the swap took New trigger with the list: ${JSON.stringify(kept)}`);
    assert(kept.trigger, `the swap closed the open trigger: ${JSON.stringify(kept)}`);
    assert(kept.job, `the swap closed the open coder on the tab behind it: ${JSON.stringify(kept)}`);

    const gone = await postTo(page, "/assistants/triggers", { form: "remove", id: made.body.id });
    assert(gone.status === 200, `remove answered ${gone.status}`);
    await page.click("[data-assistant-coders-tab]");
    return "the tab and both open rows survived the swap";
  });

  await run("a job closing DONE fires it: the reaction's answer alone is pushed into the thread under its origin", async () => {
    const newsBefore = (await newsFor(page, assistant)).length;
    ring(coderID);

    // The check's report lands first, as a note: the cockpit speaks, never
    // the user, headline first.
    const report = await waitMessage(page, (m) => m.note === "check" && /DONE: wake-event/.test(m.headline));
    assert(report.role === "cockpit", `the report is not a note of the cockpit: ${JSON.stringify(report)}`);
    assert(/job is finished/.test(report.text), `the report's body is missing: ${JSON.stringify(report)}`);

    // The trigger's row says the reaction runs, in a session of its
    // own: nothing streams in the thread for it.
    await waitRow(page, assistant, jobSub, (r) => r.reacting || /fired 1/.test(r.facts));
    // Then the answer, pushed whole: an assistant message marked as started
    // without the user, with the one line it is read by above it.
    const answer = await waitMessage(page, (m) => m.role === "assistant" && m.auto && m.state === "complete");
    assert(/AIRPORT/.test(answer.text), `the reaction did not answer the task: ${JSON.stringify(answer)}`);
    assert(answer.origin && /Job done: wake-event/.test(answer.headline), `the answer does not carry its origin: ${JSON.stringify(answer)}`);
    // Under that header stands the result and nothing else: that nobody asked
    // for it is what the bolt and the headline say, and the task is the user's
    // own words on the trigger, which is where they are read.
    for (const unwanted of ["answered without you", "MAGIC summarize what the job did", "Task:", "Event:"]) {
      assert(!answer.all.includes(unwanted), `the message carries ${JSON.stringify(unwanted)} beside its answer: ${JSON.stringify(answer.all)}`);
    }
    // No note for the event stands in the thread, and no user turn either:
    // the report, then the pushed answer.
    const all = await messages(page);
    assert(!all.find((m) => m.note === "event"), `an event wrote a note into the thread: ${JSON.stringify(all)}`);
    assert(!all.find((m) => m.role === "user"), `an event wrote a user turn: ${JSON.stringify(all)}`);
    const at = (id) => all.findIndex((m) => m.id === id);
    assert(at(report.id) < at(answer.id), `the thread is out of order: ${JSON.stringify(all.map((m) => m.role))}`);
    // Both are messages nobody asked for, so both carry the cockpit's grey
    // stripe and neither carries the purple an answer to the user wears.
    const steer = await page.evaluate(() => {
      const probe = document.createElement("span");
      probe.style.color = "var(--dc-steer)";
      document.body.append(probe);
      const color = getComputedStyle(probe).color;
      probe.remove();
      return color;
    });
    assert(report.stripe === answer.stripe && answer.stripe !== steer, `the report and the pushed answer wear different stripes: ${JSON.stringify([report.stripe, answer.stripe, steer])}`);
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
    assert(news.length > newsBefore && news[0].title === "Trigger fired.", `the pushed answer did not ring as a trigger: ${JSON.stringify(news[0])}`);
    assert(/^Job done: wake-event.*AIRPORT/.test(news[0].detail || ""), `the news does not name what fired it and carry the reaction's answer: ${JSON.stringify(news[0])}`);
    await desktopShot(page, "events-desktop-thread.png");
    return "report, pushed answer with its origin, news naming the trigger";
  });

  // The two messages nobody asked for read the same way: a header that carries
  // no switch, and under it the result and nothing else, with a fold over it
  // only where the preview had to cut the text. One ring gives both halves at
  // length, a check that reports past the preview and a reaction that answers
  // past it, so the two folds stand next to each other. An event arriving
  // while one of them is open must not shut it again, which is what the
  // surface's own pull is measured against here.
  await run("the two unasked messages fold what the preview cut and nothing else, and an event leaves an open one open", async () => {
    const before = await messages(page);
    const shortReport = before.find((m) => m.note === "check");
    const shortPushed = before.find((m) => m.auto && m.origin);
    assert(shortReport && shortPushed, `the thread lost its two cockpit messages: ${JSON.stringify(before)}`);
    const shape = await page.evaluate((ids) => ids.map((id) => {
      const row = document.querySelector(`[data-message-id="${id}"]`);
      const head = row.querySelector("[data-assistant-note], [data-assistant-origin]");
      return {
        id,
        folds: row.querySelectorAll("[data-assistant-note-fold]").length,
        collapses: row.querySelectorAll("[data-assistant-note-rest]").length,
        headSwitch: !!head.querySelector('[data-bs-toggle="collapse"]'),
        shown: (row.querySelector("[data-assistant-text]")?.getClientRects().length || 0) > 0,
      };
    }), [shortReport.id, shortPushed.id]);
    // Both answers stand whole in their preview, so neither folds: a fold over
    // a text the reader is already looking at opens onto nothing. The pushed
    // answer used to fold whatever its length, because the line that said
    // nobody asked for it had to go somewhere.
    for (const one of shape) {
      assert(!one.headSwitch, `a header still carries the switch: ${JSON.stringify(one)}`);
      assert(one.folds === 0 && one.collapses === 0, `a message its preview holds whole still folds: ${JSON.stringify(one)}`);
      assert(one.shown, `a message that folds nothing hides its text: ${JSON.stringify(one)}`);
    }

    // One coder, one ring, both halves at length: the check reports past the
    // preview and the trigger on the same signal answers past it.
    const created = await startCoder(page, projectDir, "long-pair", "Write the file.");
    assert(created.status === 200, `create answered ${created.status}: ${JSON.stringify(created.body)}`);
    longCoder = created.body.id;
    const steered = await postTo(page, "/assistants/jobs", {
      form: "steer", assistant, terminal: longCoder, task: "Write the file", done_when: "WAKE_LONG: the file is there",
    });
    assert(steered.status === 200, `steer answered ${steered.status}: ${JSON.stringify(steered.body)}`);
    const made = await postTo(page, "/assistants/triggers", {
      form: "new", assistant, event: "coder-news", terminal: longCoder, batch: "0", once: "on",
      task: "EVENT_LONG answer past the preview",
    });
    assert(made.status === 200, `the trigger answered ${made.status}: ${JSON.stringify(made.body)}`);
    const pushedBefore = pushedIDs(before);
    ring(longCoder);
    const report = await waitMessage(page, (m) => m.note === "check" && /DONE: long-pair/.test(m.headline), 60000);
    const pushed = await newPushed(page, pushedBefore, 120000);

    // Both fold now, both start folded, and the fold holds the text alone.
    const folded = await page.evaluate((ids) => ids.map((id) => {
      const row = document.querySelector(`[data-message-id="${id}"]`);
      return {
        id,
        folds: row.querySelectorAll("[data-assistant-note-fold]").length,
        preview: row.querySelector("[data-assistant-note-preview]")?.textContent.trim() || "",
        open: !!row.querySelector("[data-assistant-note-rest].show"),
        shown: (row.querySelector("[data-assistant-text]")?.getClientRects().length || 0) > 0,
      };
    }), [report.id, pushed.id]);
    for (const one of folded) {
      assert(one.folds === 1, `a cut message carries ${one.folds} fold controls: ${JSON.stringify(one)}`);
      assert(one.preview.endsWith("\u2026"), `the fold shows no cut preview beside its chevron: ${JSON.stringify(one)}`);
      assert(!one.open && !one.shown, `the fold did not start closed: ${JSON.stringify(one)}`);
    }
    for (const one of [report, pushed]) {
      for (const unwanted of ["answered without you", "EVENT_LONG answer past the preview", "Task:", "Event:"]) {
        assert(!one.all.includes(unwanted), `the message carries ${JSON.stringify(unwanted)} beside its text: ${JSON.stringify(one.all)}`);
      }
    }

    // Opened, the two read alike: the picture the review reads, on both widths.
    for (const id of [report.id, pushed.id]) {
      await page.click(`[data-message-id="${id}"] [data-assistant-note-fold]`);
      await page.waitForSelector(`[data-message-id="${id}"] [data-assistant-note-rest].collapse.show`, { timeout: 8000 });
    }
    await page.evaluate((id) => document.querySelector(`[data-message-id="${id}"]`)?.scrollIntoView({ block: "center" }), report.id);
    await sleep(400);
    await desktopShot(page, "events-desktop-folds-open.png");
    if (SHOTS_DIR) {
      const mp = await mobilePage();
      await openAssistant(mp, assistant);
      for (const id of [report.id, pushed.id]) {
        await mp.click(`[data-message-id="${id}"] [data-assistant-note-fold]`);
        await mp.waitForSelector(`[data-message-id="${id}"] [data-assistant-note-rest].collapse.show`, { timeout: 8000 });
      }
      await mp.evaluate((id) => document.querySelector(`[data-message-id="${id}"]`)?.scrollIntoView({ block: "center" }), report.id);
      await sleep(400);
      await shot(mp, "events-phone-folds-open.png");
    }

    // A trigger coming and going is an assistant event that writes no
    // message: the surface pulls its own address and must leave the thread
    // alone.
    const quiet = await postTo(page, "/assistants/triggers", {
      form: "new", assistant, event: "cron", spec: "0 5 * * *", task: "EVENT_NOTHING the quiet one", batch: "0",
    });
    assert(quiet.status === 200, `the trigger answered ${quiet.status}: ${JSON.stringify(quiet.body)}`);
    await page.waitForSelector(`[data-assistant-trigger="${quiet.body.id}"]`, { state: "attached", timeout: 15000 });
    await sleep(800);
    const stillOpen = await page.evaluate((id) => !!document.querySelector(`[data-message-id="${id}"] [data-assistant-note-rest].show`), pushed.id);
    assert(stillOpen, "the event closed the message the reader had opened");
    const gone = await postTo(page, "/assistants/triggers", { form: "remove", id: quiet.body.id });
    assert(gone.status === 200, `remove answered ${gone.status}`);
    for (const id of [report.id, pushed.id]) {
      await page.click(`[data-message-id="${id}"] [data-assistant-note-fold]`);
      await page.waitForSelector(`[data-message-id="${id}"] [data-assistant-note-rest]`, { state: "hidden", timeout: 8000 });
    }
    return "a whole preview folds nothing, a cut one folds, and the event left it open";
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
    thenSub = created.body.trigger || "";
    assert(/^[0-9a-f]{16}$/.test(thenSub), `the create does not name the trigger: ${JSON.stringify(created.body)}`);
    assert(!created.body.steerError && !created.body.thenError, `the create reported a failure: ${JSON.stringify(created.body)}`);

    // One shot, on that terminal, standing before the coder can be done.
    await openAssistant(page, assistant);
    const row = await waitRow(page, assistant, thenSub, (r) => /Job done/.test(r.event));
    assert(/then-event/.test(row.event), `the sequel does not name its coder: ${JSON.stringify(row)}`);
    assert(row.state === "once" && row.task === "MAGIC start the reviewer for then-event", `the sequel's row is off: ${JSON.stringify(row)}`);

    // The sequel carries the ordinary bounds, so its window is the default
    // thirty seconds: the check has to report, the window has to close and
    // the reaction has to answer, which is what this wait is long for.
    const newsBefore = (await newsFor(page, assistant)).length;
    ring(thenCoder);
    await waitMessage(page, (m) => m.note === "check" && /DONE: then-event/.test(m.headline));
    // A sequel is named after the coder it waits for, and that name is the one
    // line over its answer: what fired it and the task it was given are read
    // on the trigger's own row, never in the thread.
    const answer = await waitMessage(page, (m) => m.role === "assistant" && m.auto && m.state === "complete"
      && m.headline === "then-event", 120000);
    assert(/AIRPORT/.test(answer.text), `the sequel did not answer its task: ${JSON.stringify(answer)}`);
    for (const unwanted of ["Job done: then-event", "MAGIC start the reviewer for then-event", "answered without you"]) {
      assert(!answer.all.includes(unwanted), `the message carries ${JSON.stringify(unwanted)} beside its answer: ${JSON.stringify(answer.all)}`);
    }
    const spent = await waitRow(page, assistant, thenSub, (r) => /fired 1/.test(r.facts) && !r.reacting, 60000);
    assert(spent.state === "done", `a one shot sequel has to end with its turn: ${JSON.stringify(spent)}`);
    // The trigger's own note stands under its heading, which is the name
    // already, so it says what happened and never repeats the name.
    assert(/Answered for Job done: then-event/.test(spent.note), `the row does not say what the sequel answered: ${JSON.stringify(spent)}`);
    // And the notification, which holds one line, rings under the name: the
    // event used to fill that line and a chain of coders read alike.
    const deadline = Date.now() + 15000;
    let news = [];
    while (Date.now() < deadline) {
      news = await newsFor(page, assistant);
      if (news.length > newsBefore) break;
      await sleep(400);
    }
    assert(news.length > newsBefore && news[0].title === "Trigger fired."
      && /^then-event: /.test(news[0].detail || ""),
      `the named sequel did not ring under its name: ${JSON.stringify(news[0])}`);
    return `sequel ${thenSub} wired at the start, answered on DONE, rang as "${news[0].title} ${news[0].detail}"`;
  });

  // A barrier over three terminals: one turn at the end, not one per job. The
  // mode the page's select posts is the one `trigger-new --all` posts.
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

    const made = await postTo(page, "/assistants/triggers", {
      form: "new", assistant, event: "job-closed", mode: "all", batch: "0",
      terminal: barrierCoders, task: "MAGIC summarize all three",
    });
    assert(made.status === 200, `the trigger answered ${made.status}: ${JSON.stringify(made.body)}`);
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
    assert(/AIRPORT/.test(answer.text), `the barrier did not answer its task: ${JSON.stringify(answer)}`);
    const row = await waitRow(page, assistant, barrierSub, (r) => /fired 1/.test(r.facts) && !r.reacting, 60000);
    assert(row.pending === "", `the window is spent: ${JSON.stringify(row)}`);
    await sleep(1500);
    const after = (await messages(page)).filter((m) => m.auto).length;
    assert(after === pushedBefore + 1, `the barrier answered ${after - pushedBefore} times, want one`);
    return `one turn for three terminals, ${barrierSub}`;
  });

  // A typo in the task is one change away, from the very form that makes one:
  // the row's own Change opens it filled, the event is locked because another
  // event is another trigger, and the next reaction is asked the new task.
  await run("the row's button changes a trigger in the form that makes one", async () => {
    const created = await startCoder(page, projectDir, "edit-me", "Write the file.");
    assert(created.status === 200, `create answered ${created.status}: ${JSON.stringify(created.body)}`);
    editCoder = created.body.id;
    const made = await postTo(page, "/assistants/triggers", {
      form: "new", assistant, event: "coder-news", terminal: editCoder, batch: "0", task: "MAGIC the frist task",
    });
    assert(made.status === 200, `the trigger answered ${made.status}: ${JSON.stringify(made.body)}`);
    editSub = made.body.id;

    await openAssistant(page, assistant);
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.click("[data-assistant-triggers-tab]");
    await page.waitForSelector(`[data-assistant-trigger="${editSub}"]`, { timeout: 8000 });
    await page.click(`[data-assistant-trigger="${editSub}"] [data-assistant-trigger-fold]`);
    await page.click(`[data-assistant-trigger="${editSub}"] [data-assistant-trigger-edit]`);
    await page.waitForSelector(".modal.show [data-assistant-trigger-form]", { timeout: 8000 });

    // The form stands filled with what is stored, and the event cannot move.
    const filled = await page.evaluate(() => {
      const form = document.querySelector(".modal.show [data-assistant-trigger-form]");
      const picked = [...form.querySelectorAll('select[name="terminal"]')].flatMap((s) => [...s.selectedOptions].map((o) => o.value));
      const fields = [...form.querySelectorAll(".form-label")].map((l) => l.getAttribute("for"));
      return {
        action: form.querySelector('[name="form"]').value,
        id: form.querySelector('[name="id"]').value,
        name: form.querySelector('[name="name"]').value,
        firstField: fields[0] || "",
        nameLabel: form.querySelector('label[for="assistant-trigger-name"]').textContent.trim(),
        nameOptional: form.querySelector('[name="name"]').placeholder,
        task: form.querySelector('[name="task"]').value,
        event: form.querySelector('[name="event"]').value,
        locked: form.querySelector('[name="event"]').disabled,
        until: form.querySelector('[name="until"]').value,
        unit: form.querySelector('[name="untilUnit"]').value,
        submit: form.querySelector('button[type="submit"]').textContent.trim(),
        picked,
      };
    });
    assert(filled.action === "edit" && filled.id === editSub, `the form is not on the trigger: ${JSON.stringify(filled)}`);
    // The name is the form's first field and says it may be left out, in the
    // box and not beside the label; this trigger was made without one, so it
    // stands empty and the placeholder is what shows.
    assert(filled.firstField === "assistant-trigger-name", `the name is not the first field: ${JSON.stringify(filled)}`);
    assert(/optional/i.test(filled.nameOptional), `the name field does not read as optional: ${JSON.stringify(filled)}`);
    assert(filled.nameLabel === "Name", `the label carries more than the name: ${JSON.stringify(filled)}`);
    assert(filled.name === "", `a trigger nobody named carries no name: ${JSON.stringify(filled)}`);
    assert(filled.task === "MAGIC the frist task", `the form is not filled: ${JSON.stringify(filled)}`);
    assert(filled.event === "coder-news" && filled.locked, `the event is not locked: ${JSON.stringify(filled)}`);
    // This trigger expires never, so the number stands empty with the select
    // on No expiry, the stand a new trigger starts on.
    assert(filled.until === "" && filled.unit === "never", `the expiry does not read as none: ${JSON.stringify(filled)}`);
    assert(filled.submit === "Save trigger", `the button still says ${filled.submit}`);
    assert(filled.picked.join(",") === editCoder, `the terminal is not picked: ${JSON.stringify(filled)}`);
    await shot(page, "events-desktop-edit.png");

    await page.fill('.modal.show [data-assistant-trigger-form] [name="task"]', "EVENT_LONG the second task");
    // An expiry is a number and a unit, so 60 minutes is one the form can say
    // where the five guessed spans could not.
    await page.selectOption('.modal.show [data-assistant-trigger-form] [name="untilUnit"]', "m");
    await page.fill('.modal.show [data-assistant-trigger-form] [name="until"]', "60");
    await page.click('.modal.show button[type="submit"]');
    const toast = page.locator(".dc-toast", { hasText: "Changed:" });
    await toast.waitFor({ state: "visible", timeout: 8000 });
    const said = (await toast.textContent()).trim();
    assert(/task/.test(said) && /until \d{4}-/.test(said), `the answer does not say what changed: ${said}`);

    // The row carries the new task, and nothing of what it already is moved.
    const row = await waitRow(page, assistant, editSub, (r) => r.task === "EVENT_LONG the second task");
    assert(/fired 0/.test(row.facts), `the count moved with the change: ${JSON.stringify(row)}`);
    // And the dialog is gone with the change, so the next one that opens is
    // whatever it was opened on and never the entry before it.
    await page.waitForSelector(".modal.show", { state: "hidden", timeout: 8000 });
    await page.click("[data-assistant-trigger-new]");
    await page.waitForSelector(".modal.show [data-assistant-trigger-form]", { timeout: 8000 });
    const back = await page.evaluate(() => {
      const form = document.querySelector(".modal.show [data-assistant-trigger-form]");
      return {
        action: form.querySelector('[name="form"]').value,
        locked: form.querySelector('[name="event"]').disabled,
        task: form.querySelector('[name="task"]').value,
        submit: form.querySelector('button[type="submit"]').textContent.trim(),
      };
    });
    assert(back.action === "new" && !back.locked && !back.task && back.submit === "Add trigger",
      `the plus did not open a new trigger: ${JSON.stringify(back)}`);
    await page.click('.modal.show .modal-footer [data-bs-dismiss="modal"]');
    await page.waitForSelector(".modal.show", { state: "hidden", timeout: 8000 });

    // And the expiry that was just given comes back as the span it is: what
    // is stored is a moment, so the form opens on what is left, in the unit
    // that keeps the number whole. An empty field would put the select on No
    // expiry and the next save of the task would take the expiry away.
    await page.click(`[data-assistant-trigger="${editSub}"] [data-assistant-trigger-edit]`);
    await page.waitForSelector(".modal.show [data-assistant-trigger-form]", { timeout: 8000 });
    const again = await page.evaluate(() => {
      const form = document.querySelector(".modal.show [data-assistant-trigger-form]");
      return {
        until: form.querySelector('[name="until"]').value,
        unit: form.querySelector('[name="untilUnit"]').value,
        posts: !form.querySelector('[name="until"]').disabled,
      };
    });
    const minutes = Number(again.until) * ({ m: 1, h: 60, d: 1440 })[again.unit];
    assert(again.posts && minutes >= 57 && minutes <= 60,
      `the expiry does not open on what is left: ${JSON.stringify(again)}`);
    await shot(page, "events-desktop-edit-expiry.png");
    await page.click('.modal.show .modal-footer [data-bs-dismiss="modal"]');
    await page.waitForSelector(".modal.show", { state: "hidden", timeout: 8000 });

    // The next reaction is asked the new task, and what says so is the answer
    // it came back with: the two tasks steer the fake to two different
    // answers, because the task itself is nowhere in the thread. A coder names
    // its own session, so the headline carries whatever it calls itself.
    const pushedBefore = pushedIDs(await messages(page));
    ring(editCoder);
    const answer = await newPushed(page, pushedBefore, 60000);
    assert(/I read every file the coder touched/.test(answer.text),
      `the reaction was not asked the new task: ${JSON.stringify(answer)}`);
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
    const made = await postTo(page, "/assistants/triggers", {
      form: "new", assistant, event: "job-closed", mode: "all", batch: "0",
      terminal: grownCoders, task: "MAGIC summarize the group",
    });
    assert(made.status === 200, `the trigger answered ${made.status}: ${JSON.stringify(made.body)}`);
    grownSub = made.body.id;

    await openAssistant(page, assistant);
    const pushedBefore = (await messages(page)).filter((m) => m.auto).length;
    ring(grownCoders[0]);
    await waitMessage(page, (m) => m.note === "check" && /DONE: grown-one/.test(m.headline));
    await waitRow(page, assistant, grownSub, (r) => r.pending === "1");

    // The third joins the barrier, and the arrival that is held stays held.
    const third = await steer("grown-three");
    const changed = await postTo(page, "/assistants/triggers", {
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
    assert(/AIRPORT/.test(answer.text), `the grown barrier did not answer its task: ${JSON.stringify(answer)}`);
    return `the barrier waited for the target that joined it, ${grownSub}`;
  });

  // A trigger that is over is spent: it offers no way to the form, so
  // its row has no Change, and the path refuses a change with one sentence.
  await run("a trigger that is done cannot be changed", async () => {
    const made = await postTo(page, "/assistants/triggers", {
      form: "new", assistant, event: "coder-news", terminal: editCoder, batch: "0", once: "on",
      task: "EVENT_NOTHING the one shot",
    });
    assert(made.status === 200, `the trigger answered ${made.status}: ${JSON.stringify(made.body)}`);
    const spent = made.body.id;
    ring(editCoder);
    const row = await waitRow(page, assistant, spent, (r) => r.state === "done", 60000);
    assert(row.edit === "", `a spent trigger still offers a change: ${JSON.stringify(row)}`);

    const refused = await postTo(page, "/assistants/triggers", { form: "edit", id: spent, task: "try again" });
    assert(refused.status >= 400, `a done trigger was changed: ${JSON.stringify(refused)}`);
    assert(/is done and cannot be changed/.test(refused.body.error || ""), `the refusal is not the sentence: ${JSON.stringify(refused.body)}`);
    // And the event never moves, whichever one is named.
    const moved = await postTo(page, "/assistants/triggers", { form: "edit", id: editSub, event: "job-done", task: "x" });
    assert(moved.status >= 400, `the event was changed: ${JSON.stringify(moved)}`);
    assert(/cannot change its event/.test(moved.body.error || ""), `the refusal is not the sentence: ${JSON.stringify(moved.body)}`);
    const stood = (await triggerRows(page, assistant)).find((r) => r.id === editSub);
    assert(/Coder has news/.test(stood.event), `the event moved anyway: ${JSON.stringify(stood)}`);
    return "a spent trigger and the event are both refused";
  });

  // A coder has one event and it fires on every signal of that coder, the way
  // Job closed stands over the three job ends: which of the two a signal was
  // is read off the coder's own hook name and copilot has none, so a narrower
  // one is not offered at all. A Stop is read as ended, and the answer it
  // pushes still says so, because the event carries the kind it was read as
  // and never the umbrella.
  await run("the one coder event fires on a kind that is not its own", async () => {
    const created = await startCoder(page, projectDir, "any-signal", "Write the file.");
    assert(created.status === 200, `create answered ${created.status}: ${JSON.stringify(created.body)}`);
    const newsCoder = created.body.id;

    // The form offers it, on the coder's own source, so the select shows the
    // coders and not the jobs, and it offers nothing else for a coder: the
    // two narrow kinds are gone from the list, not only from the help. New
    // trigger stands in the triggers tab and nowhere else, so a fresh page has
    // to pick that tab before it is there.
    await openAssistant(page, assistant);
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.click("[data-assistant-triggers-tab]");
    await page.click("[data-assistant-trigger-new]");
    await page.waitForSelector(".modal.show [data-assistant-trigger-form]", { timeout: 8000 });
    const coderEvents = await page.evaluate(() => [...document.querySelectorAll('.modal.show [name="event"] option')]
      .filter((o) => o.dataset.source === "coder")
      .map((o) => ({ value: o.value, label: o.textContent.trim(), source: o.dataset.source })));
    assert(coderEvents.length === 1, `the form offers ${coderEvents.length} coder events: ${JSON.stringify(coderEvents)}`);
    const offered = coderEvents[0];
    assert(offered.value === "coder-news" && offered.source === "coder", `the form does not offer the coder umbrella: ${JSON.stringify(offered)}`);
    await page.click(".modal.show [data-bs-dismiss='modal']");
    await page.waitForSelector(".modal.show", { state: "hidden", timeout: 8000 });

    const made = await postTo(page, "/assistants/triggers", {
      form: "new", assistant, event: "coder-news", terminal: newsCoder, batch: "0", once: "on",
      task: "MAGIC any signal of that coder",
    });
    assert(made.status === 200, `the trigger answered ${made.status}: ${JSON.stringify(made.body)}`);
    const pushedBefore = pushedIDs(await messages(page));

    // Being the fresh one is what tells this answer from the ones before it,
    // the task is nowhere in the thread; the coder's own name in the headline
    // is whatever the session is called by then.
    ring(newsCoder);
    const answer = await newPushed(page, pushedBefore, 60000);
    assert(/AIRPORT/.test(answer.text), `the umbrella did not answer its task: ${JSON.stringify(answer)}`);
    // The event carries the kind it was read as and never the umbrella, so the
    // headline still says which of the two arrived.
    assert(/^Coder ended its turn:/.test(answer.headline), `the answer does not name the kind that arrived: ${JSON.stringify(answer)}`);
    const after = (await messages(page)).filter((m) => m.auto).length;
    assert(after === pushedBefore.size + 1, `the umbrella answered ${after - pushedBefore.size} times, want one`);
    const row = await waitRow(page, assistant, made.body.id, (r) => /fired 1/.test(r.facts) && !r.reacting, 60000);
    assert(/Coder has news/.test(row.event), `the row does not name the umbrella: ${JSON.stringify(row)}`);
    return `an ended signal fired ${offered.label}`;
  });

  // Deleting a coder is the last thing that terminal does: its job closes with
  // that reason, what waited for the terminal fires once for the deletion, and
  // what could only ever have fired on it is dropped and said out loud.
  await run("deleting a coder closes its job, fires its triggers and drops the dead ones", async () => {
    const created = await startCoder(page, projectDir, "doomed-event", "Write the file.");
    assert(created.status === 200, `create answered ${created.status}: ${JSON.stringify(created.body)}`);
    const doomed = created.body.id;
    const steered = await postTo(page, "/assistants/jobs", {
      form: "steer", assistant, terminal: doomed, task: "Write the file", done_when: "WAKE_DONE: the file is there",
    });
    assert(steered.status === 200, `steer answered ${steered.status}: ${JSON.stringify(steered.body)}`);
    const subs = [];
    for (const which of ["first", "second"]) {
      const made = await postTo(page, "/assistants/triggers", {
        form: "new", assistant, event: "coder-news", terminal: doomed, batch: "0", task: `MAGIC the ${which} one on the doomed coder`,
      });
      assert(made.status === 200, `the trigger answered ${made.status}: ${JSON.stringify(made.body)}`);
      subs.push(made.body.id);
    }
    // One that outlives the deletion: it names no terminal, so nothing about it
    // is dropped.
    const survivor = await postTo(page, "/assistants/triggers", {
      form: "new", assistant, event: "coder-news", batch: "0", task: "EVENT_NOTHING any coder",
    });
    assert(survivor.status === 200, `the trigger answered ${survivor.status}: ${JSON.stringify(survivor.body)}`);

    await openAssistant(page, assistant);
    const deleted = await postTo(page, `/coders/${doomed}/delete`, {});
    assert(deleted.status === 200, `delete answered ${deleted.status}: ${JSON.stringify(deleted.body)}`);
    // The answer says what it took with it, the sentence the flash and
    // `coder-delete` print.
    assert(deleted.body.dropped === "2 triggers dropped",
      `the delete does not name what it dropped: ${JSON.stringify(deleted.body)}`);

    // The job closed with the reason, as a note of the cockpit.
    const report = await waitMessage(page, (m) => m.note === "check" && /EXPIRED: doomed-event/.test(m.headline));
    assert(/the coder was deleted/.test(report.text), `the report does not say why: ${JSON.stringify(report)}`);

    // Both triggers fired once for the deletion and are gone from the list.
    const answer = await waitMessage(page, (m) => m.role === "assistant" && m.auto && m.state === "complete"
      && /Coder deleted: doomed-event/.test(m.headline), 60000);
    assert(/AIRPORT/.test(answer.text), `the reaction did not answer: ${JSON.stringify(answer)}`);
    const deadline = Date.now() + 20000;
    let rows = [];
    while (Date.now() < deadline) {
      rows = await triggerRows(page, assistant);
      if (!rows.find((r) => subs.includes(r.id))) break;
      await sleep(400);
    }
    assert(!rows.find((r) => subs.includes(r.id)), `a trigger on a deleted coder still stands: ${JSON.stringify(rows)}`);
    assert(rows.find((r) => r.id === survivor.body.id), "a trigger without a terminal must not be dropped");
    // Two reactions run for the one deletion and they end in their own time,
    // so the second answer is waited for: the rows above are dropped at the
    // delete itself and say nothing about a turn that is still writing.
    const both = await waitFor(async () => {
      const seen = (await messages(page)).filter((m) => m.auto && /Coder deleted: doomed-event/.test(m.headline));
      return seen.length >= 2 ? seen : null;
    }, 60000);
    assert(both && both.length === 2, `want one answer per trigger, got ${both ? both.length : 0}`);
    return `job closed, both fired once, ${deleted.body.dropped}`;
  });

  await run("a schedule ticks and a NOTHING answer pushes nothing", async () => {
    const made = await postTo(page, "/assistants/triggers", {
      form: "new", assistant, event: "cron", spec: "* * * * *", task: "EVENT_NOTHING every minute", batch: "0", once: "on",
    });
    assert(made.status === 200, `the trigger answered ${made.status}: ${JSON.stringify(made.body)}`);
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

  // A trigger may carry a name, and then the row reads by it: a crontab line
  // says when a schedule fires and never what for, so a named one moves it to
  // the line under the name. Without a name nothing moves at all, which is
  // what every trigger made before names existed falls back to, and nothing
  // is ever derived from the task.
  await run("a name is the row's heading and the schedule moves under it", async () => {
    const named = await postTo(page, "/assistants/triggers", {
      form: "new", assistant, event: "cron", name: "Nightly summary", spec: "0 4 * * *",
      task: "EVENT_NOTHING summarise the day", batch: "0",
    });
    assert(named.status === 200, `the named trigger answered ${named.status}: ${JSON.stringify(named.body)}`);
    namedCron = named.body.id;
    assert(named.body.summary === "Nightly summary", `the answer does not read by the name: ${JSON.stringify(named.body)}`);
    const bare = await postTo(page, "/assistants/triggers", {
      form: "new", assistant, event: "cron", name: "", spec: "0 5 * * *",
      task: "EVENT_NOTHING the morning one", batch: "0",
    });
    assert(bare.status === 200, `the trigger without a name answered ${bare.status}: ${JSON.stringify(bare.body)}`);
    bareCron = bare.body.id;
    assert(/Schedule/.test(bare.body.summary) && /0 5 \* \* \*/.test(bare.body.summary),
      `a trigger without a name reads by its event: ${JSON.stringify(bare.body)}`);

    const rows = await triggerRows(page, assistant);
    const withName = rows.find((r) => r.id === namedCron);
    const without = rows.find((r) => r.id === bareCron);
    assert(withName.name === "Nightly summary" && withName.nameFirst,
      `the name is not the row's heading: ${JSON.stringify(withName)}`);
    assert(/Schedule/.test(withName.event) && /0 4 \* \* \*/.test(withName.event) && !withName.eventIsHeading,
      `the schedule left the named row or still stands on top: ${JSON.stringify(withName)}`);
    assert(!without.name && without.eventIsHeading && /0 5 \* \* \*/.test(without.event),
      `a trigger without a name must read exactly as before: ${JSON.stringify(without)}`);

    // The JSON the CLI reads carries it the same way, so `trigger-list` can
    // print the name where there is one and nothing where there is none.
    const listed = await page.evaluate(async (id) => {
      const res = await fetch(`/assistants/triggers?assistant=${id}`, { headers: { Accept: "application/json" } });
      return (await res.json()).triggers || [];
    }, assistant);
    const jsonNamed = listed.find((r) => r.id === namedCron);
    const jsonBare = listed.find((r) => r.id === bareCron);
    assert(jsonNamed.name === "Nightly summary" && !jsonBare.name,
      `the JSON does not carry the name: ${JSON.stringify([jsonNamed, jsonBare])}`);

    // A name it never got is a name it must never have: the task says
    // "summarise the day" and the row without a name stays nameless.
    await openAssistant(page, assistant);
    await page.click("[data-assistant-triggers-tab]");
    await page.waitForSelector(`[data-assistant-trigger="${namedCron}"]`, { timeout: 8000 });
    await desktopShot(page, "events-desktop-triggers.png");
    return `named and bare schedule side by side, ${namedCron} and ${bareCron}`;
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
    // A trigger whose task keeps the fake busy (SLOW holds the reaction
    // open), fired by one more steered coder closing DONE.
    const made = await postTo(page, "/assistants/triggers", {
      form: "new", assistant, event: "job-done", task: "SLOW keep working on it", batch: "0", once: "on",
    });
    assert(made.status === 200, `the trigger answered ${made.status}: ${JSON.stringify(made.body)}`);
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
      reacting: !!document.querySelector('[data-assistant-triggers-list] [data-assistant-working="react"]'),
    }));
    assert(!composer.running && composer.stopHidden && composer.sendVisible && composer.sendEnabled && !composer.streaming,
      `the composer must be untouched by a reaction: ${JSON.stringify(composer)}`);
    assert(composer.reacting, "the trigger's row must say the reaction runs");

    // A chat turn goes through beside the running reaction: its own session,
    // its own slot, nothing queues.
    await page.fill("[data-assistant-input]", "PAUSE_CHAT while the reaction runs");
    await page.click("[data-assistant-send]");
    await page.waitForFunction(() => !!document.querySelector("[data-assistant-instance] .dc-term-icon.assistant.working"),
      null, { timeout: 30000 });

    // And the row says it with the icon it already carries: the trigger's own
    // bolt in the steering colour with the dot running along its edge, no word
    // beside it, no spinner and no second icon, with the colours read back out
    // of the palette instead of pinned here. The tab has to be the one on top
    // for that: a pane that is switched away has no box to measure.
    await page.click("[data-assistant-triggers-tab]");
    await page.waitForSelector("#assistant-triggers.active", { timeout: 8000 });
    const mark = await page.evaluate(() => {
      const icon = document.querySelector('[data-assistant-triggers-list] [data-assistant-working="react"]');
      if (!icon) return null;
      const s = getComputedStyle(icon);
      const dot = getComputedStyle(icon, "::before");
      const root = getComputedStyle(document.documentElement);
      const flat = (v) => v.replace(/\s/g, "");
      return {
        label: icon.getAttribute("aria-label"),
        word: icon.textContent.trim(),
        spinner: !!icon.querySelector(".spinner-border"),
        icons: document.querySelectorAll('[data-assistant-trigger] .dc-term-icon').length,
        rows: document.querySelectorAll("[data-assistant-trigger]").length,
        classes: icon.className,
        glyph: icon.querySelector("i")?.className,
        bg: flat(s.backgroundColor),
        ring: s.boxShadow,
        dotAnim: dot.animationName,
        steer: flat(root.getPropertyValue("--dc-steer-rgb")),
        ok: flat(root.getPropertyValue("--dc-ok-rgb")),
      };
    });
    assert(mark && mark.label === "Reacting" && !mark.word && !mark.spinner,
      `the row does not say it with the picture alone: ${JSON.stringify(mark)}`);
    assert(mark.classes === "dc-term-icon steered working" && mark.glyph === "ti ti-bolt" && mark.icons === mark.rows,
      `the state does not ride the row's one own icon: ${JSON.stringify(mark)}`);
    assert(mark.bg.includes(mark.steer) && !mark.bg.includes(mark.ok),
      `a reacting trigger is not in the steering colour: ${JSON.stringify(mark)}`);
    assert(mark.dotAnim === "dc-run" && mark.ring !== "none",
      `the reacting icon does not run the dot: ${JSON.stringify(mark)}`);
    await page.click("[data-assistant-coders-tab]");

    const answer = await waitMessage(page, (m) => m.role === "assistant" && !m.auto && m.state === "complete" && /and after it/.test(m.text), 60000);
    assert(answer, "the chat turn did not answer beside the reaction");
    const queued = await page.evaluate(() => !!document.querySelector("[data-assistant-queued]"));
    assert(!queued, "nothing may queue behind a reaction");
    const still = await triggerRows(page, assistant);
    assert((still.find((r) => r.id === slowSub) || {}).reacting, "the reaction is still running beside the chat");
    return "composer untouched, chat answered beside the reaction";
  });

  // The task field follows what is typed, the assistant composer's growth, and
  // stops where the dialog runs out of screen instead of pushing its footer
  // past the edge. Both ways in are read: a fresh form stands on its rows, and
  // one opened over a stored task stands grown before anything is typed. It
  // carries no drag handle either, for the composer's reason: what grows by
  // itself needs none, and in the dialog one would pull it wider than the
  // dialog itself.
  await run("the trigger form's task field grows with the text and stops at the dialog's edge", async () => {
    const long = "MAGIC a task that is written out in full. ".repeat(40);
    // A schedule that cannot come round while the runner is up: this trigger
    // is here to be looked at, not to fire.
    const grown = await postTo(page, "/assistants/triggers", {
      form: "new", assistant, event: "cron", spec: "0 4 1 1 *", task: long,
    });
    assert(grown.status === 200, `the long trigger answered ${grown.status}: ${JSON.stringify(grown.body)}`);

    const measure = (target) => target.evaluate(() => {
      const task = document.querySelector(".modal.show [data-trigger-task]");
      const footer = document.querySelector(".modal.show .modal-footer");
      return {
        h: task.getBoundingClientRect().height,
        scroll: task.scrollHeight,
        overflow: getComputedStyle(task).overflowY,
        foot: footer.getBoundingClientRect().bottom,
        window: window.innerHeight,
        rows: Number(task.rows),
        line: parseFloat(getComputedStyle(task).lineHeight) || 0,
        resize: getComputedStyle(task).resize,
      };
    });
    const close = async (target) => {
      await target.click('.modal.show .modal-footer [data-bs-dismiss="modal"]');
      await target.waitForSelector(".modal.show", { state: "hidden", timeout: 8000 });
    };

    const seen = {};
    for (const name of ["desktop", "phone"]) {
      const phone = name === "phone";
      const target = phone ? await mobilePage() : page;
      await openAssistant(target, assistant);
      if (phone) {
        await target.click("[data-assistant-watching-button]");
        await target.waitForSelector("#assistant-aside.show", { timeout: 8000 });
      } else {
        await target.setViewportSize({ width: 1280, height: 800 });
      }
      await target.click("[data-assistant-triggers-tab]");
      await target.waitForSelector("[data-assistant-trigger-new]", { timeout: 8000 });

      await target.click("[data-assistant-trigger-new]");
      await target.waitForSelector(".modal.show [data-trigger-task]", { timeout: 8000 });
      await sleep(400);
      const empty = await measure(target);
      assert(empty.h < empty.line * (empty.rows + 2), `${name}: the empty field is ${empty.h}px for ${empty.rows} rows`);
      assert(empty.overflow === "hidden", `${name}: the empty field scrolls`);
      assert(empty.resize === "none", `${name}: the field carries a drag handle, resize is ${empty.resize}`);

      await target.fill(".modal.show [data-trigger-task]", "MAGIC one line.\nMAGIC two.\nMAGIC three.\nMAGIC four.\nMAGIC five.");
      await sleep(200);
      const middle = await measure(target);
      assert(middle.h > empty.h, `${name}: the field did not grow, ${middle.h}px against ${empty.h}px`);
      assert(middle.scroll <= middle.h + 4, `${name}: the grown field still scrolls, ${middle.scroll}px in ${middle.h}px`);

      await target.fill(".modal.show [data-trigger-task]", long);
      await sleep(200);
      const full = await measure(target);
      assert(full.overflow === "auto", `${name}: the field past the limit does not scroll`);
      assert(Math.abs(full.h - full.window * 0.35) <= 2, `${name}: the limit is ${full.h}px of a ${full.window}px window`);
      assert(full.foot <= full.window + 1, `${name}: Save left the screen, footer at ${full.foot}px of ${full.window}px`);
      await close(target);

      // Opened over a stored task the field stands grown at once, with no
      // keystroke and no resize in between.
      await target.waitForSelector(`[data-assistant-trigger="${grown.body.id}"]`, { timeout: 8000 });
      await target.click(`[data-assistant-trigger="${grown.body.id}"] [data-assistant-trigger-edit]`);
      await target.waitForSelector(".modal.show [data-trigger-task]", { timeout: 8000 });
      await sleep(400);
      const opened = await measure(target);
      assert(opened.h > empty.h * 2, `${name}: the stored task opened in a peephole, ${opened.h}px`);
      assert(opened.foot <= opened.window + 1, `${name}: Save left the screen over a stored task, footer at ${opened.foot}px`);
      await close(target);
      seen[name] = { empty: Math.round(empty.h), middle: Math.round(middle.h), full: Math.round(full.h), opened: Math.round(opened.h) };
    }

    await postTo(page, "/assistants/triggers", { form: "remove", id: grown.body.id });
    await page.setViewportSize({ width: 1360, height: 900 });
    return JSON.stringify(seen);
  });

  await run("the phone shows the notes, the aside and the form", async () => {
    const mp = await mobilePage();
    await openAssistant(mp, assistant);
    await mp.waitForFunction(() => document.querySelectorAll("[data-assistant-note]").length >= 2, null, { timeout: 15000 });
    const wide = await mp.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1);
    assert(!wide, "the phone's thread overflows sideways");
    const folds = await tapHeights(mp, "[data-assistant-note-fold]");
    const smallFolds = folds.filter((f) => f.h < 44);
    assert(!smallFolds.length, `fold controls under 44px on the phone: ${JSON.stringify(smallFolds)}`);
    await shot(mp, "events-phone-thread.png");
    await mp.click("[data-assistant-watching-button]");
    await mp.waitForSelector("#assistant-aside.show", { timeout: 8000 });
    await mp.click("[data-assistant-triggers-tab]");
    await mp.waitForSelector("#assistant-aside [data-assistant-trigger]", { timeout: 8000 });
    const asideWide = await mp.evaluate(() => {
      const aside = document.getElementById("assistant-aside");
      return aside.scrollWidth > aside.clientWidth + 1 || document.documentElement.scrollWidth > window.innerWidth + 1;
    });
    assert(!asideWide, "the phone's aside overflows sideways");
    await shot(mp, "events-phone-aside.png");
    await mp.click("[data-assistant-trigger-new]");
    await mp.waitForSelector(".modal.show [data-assistant-trigger-form]", { timeout: 8000 });
    const targets = await mp.evaluate(() => [...document.querySelectorAll(".modal.show button, .modal.show select, .modal.show input:not([type=hidden]):not([type=checkbox]), .modal.show textarea")]
      .filter((el) => el.getClientRects().length)
      .map((el) => ({ tag: el.tagName, name: el.name || el.textContent.trim(), h: el.getBoundingClientRect().height })));
    const small = targets.filter((t) => t.tag === "BUTTON" && t.h < 44);
    assert(!small.length, `buttons under 44px on the phone: ${JSON.stringify(small)}`);
    await shot(mp, "events-phone-form.png");
    return `${targets.length} controls, none under the finger`;
  });

  await run("the row's own button removes a trigger", async () => {
    await openAssistant(page, assistant);
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.click("[data-assistant-triggers-tab]");
    await page.waitForSelector(`[data-assistant-trigger="${jobSub}"]`, { timeout: 8000 });
    await page.click("[data-assistant-trigger-new]");
    await page.waitForSelector(".modal.show [data-assistant-trigger-form]", { timeout: 8000 });
    await sleep(600);
    await shot(page, "events-desktop-form.png");
    await page.click('.modal.show .modal-footer [data-bs-dismiss="modal"]');
    await page.waitForSelector(".modal.show", { state: "hidden", timeout: 8000 });
    await page.click(`[data-assistant-trigger="${jobSub}"] [data-assistant-trigger-fold]`);
    await page.click(`[data-assistant-trigger="${jobSub}"] [data-assistant-trigger-remove]`);
    await L.confirmSwal(page);
    await page.waitForFunction((id) => !document.querySelector(`[data-assistant-trigger="${id}"]`), jobSub, { timeout: 10000 });
    const rows = await triggerRows(page, assistant);
    assert(!rows.find((r) => r.id === jobSub), "the trigger is still stored");
    await page.setViewportSize({ width: 1360, height: 900 });
    return "removed from the row's own button";
  });

  await run("cleanup", async () => {
    for (const id of [coderID, thenCoder, heldCoder, heldCoder2, slowCoder, editCoder, longCoder, ...barrierCoders, ...grownCoders].filter(Boolean)) {
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
