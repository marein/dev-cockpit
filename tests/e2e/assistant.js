const L = require("./lib");
const { assert, BASE, sleep } = L;
const fs = require("fs");
const path = require("path");

// Assistants: the cockpit's own conversation partners, a page of their own.
// Several of them live side by side, each with its own thread and its own
// steered coders, and each lives until it is deleted. /assistants lands on the
// one the user was last in, /assistants/:id opens one, and with none at all the
// page renders the empty state whose button makes the first. The list column
// beside it holds every assistant (the phone opens it as a sheet from the tab
// bar), the steered coders and the memory stand in an aside that is inline
// from xl up and a sheet the head's buttons open below. A notification link
// (/assistants/<id>#message-<id>) lands on the announced answer. The APIs stay:
// POST /assistants/:id dispatching on the hidden form field (message, retry,
// cancel, draft, new, rename, delete), the SSE at
// /assistants/:id/stream, the message fragment, uploads, drafts and the byte
// ranged media route.
//
// The instance MUST run with tests/e2e/fakes ahead of the real CLIs on PATH,
// with a scratch HOME and its own TMUX_TMPDIR: the fakes persist provider
// conversations under $HOME and no check may spend a model request. Prompts
// steer the fakes: MAGIC (a fixed answer), SLOW (a turn that keeps running),
// FAIL (a failing turn), MARKDOWN (markup and an injection attempt), TOOL (a
// tool signal), CONTEXT_HIGH (a turn that reports a nearly full context
// window; both fakes otherwise report 68 percent of it), PAUSE_CHAT (an answer
// with twelve silent seconds in the middle of it), STREAM_PARAGRAPH (one
// sentence in many small deltas, so the streamed text can be read while it
// grows).
//
// One check restarts the instance: the RESTART_CHAT prompt makes the fake kill
// the cockpit mid answer and start it again, which is how the run proves that a
// turn outlives its server. Point this runner at a throwaway only, and never at
// an instance somebody is using.
//
// Gotchas:
// - nothing creates an assistant by itself: the page never leaves one behind,
//   so a run that needs one presses New,
// - pressing New twice makes two assistants, they are not reused and nothing
//   is archived,
// - the composer is JS owned, every interaction waits for dc-assistant[ready],
// - a surface on screen reads its own news, so a check that wants the marks
//   leaves the page first,
// - an upload finishes before the message is sent: the chip carries the name
//   the send posts back, so a check waits for the chip, not for the network,
// - the memory is what a coder reads at startup, so the generated CLAUDE.md
//   and AGENTS.md in the workspace must carry a saved entry,
// - every assistant stays live: starting a new one leaves the others alone,
//   composer and provider session included, so a check may come back to one it
//   left,
// - a job belongs to the assistant that steered it, so the jobs aside of a
//   page shows that assistant's coders and nobody else's,
// - silence in the middle of an answer costs nothing: the stream says it is
//   alive with a ping frame every 15s, only a missing ping rebuilds it, and the
//   message is pulled after a break alone (a rebuilt stream, a page coming
//   back), never on a quiet tick; a pull that lands mid answer must leave the
//   bubble alone, the store holds an answer only once the turn settled,
// - the composer never locks, and a send during a running turn waits in that
//   assistant's queue: it stands in the transcript as Waiting, it can be taken
//   back until it goes, and the end of the turn sends what is left as one new
//   turn, so a check that wants the queue keeps the first turn running (SLOW)
//   and stops it again afterwards.

const jobsProject = "zzjobs";

const memoryTitle = "zztc assistant fact";
const memorySlug = "zztc-assistant-fact";
const memoryBody = "The runner wrote this memory.";

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

const READY = "dc-assistant[ready]";

// The inbox the coder's Stop hook drops into, mounted like wake.js mounts it.
// Only the show earlier check rings a coder, and without the mount it runs
// without the note instead of failing.
const NOTIFY_DIR = process.env.NOTIFY_DIR || "";
function ring(sessionID) {
  const name = `${Date.now()}-${Math.floor(Math.random() * 1e6)}`;
  const payload = JSON.stringify({ session_id: sessionID, hook_event_name: "Stop" });
  fs.writeFileSync(path.join(NOTIFY_DIR, `${name}.tmp`), payload);
  fs.renameSync(path.join(NOTIFY_DIR, `${name}.tmp`), path.join(NOTIFY_DIR, `${name}.json`));
}

// openAssistant lands on one assistant. With an id it opens that one, which is
// what every check that has a thread of its own wants. Without one it takes the
// area's own address, which resolves to the assistant last looked at the way
// /terminals resolves to a terminal, and with no assistant at all it makes the
// first one, because nothing else does any more. Returns the id on screen.
async function openAssistant(page, id) {
  if (id) {
    await openConversation(page, id);
    return id;
  }
  await page.goto(`${BASE}/assistants`, { waitUntil: "domcontentloaded" });
  await dismissUpdate(page);
  if (await page.locator("[data-assistant-none]").count()) await createFirst(page);
  await page.waitForSelector(READY, { timeout: 15000 });
  return page.locator("dc-assistant").getAttribute("assistant-id");
}

// createFirst presses the empty state's button. With more than one coder
// installed that button is a dropdown toggle and the coders are its items, so
// the menu has to be opened before one of them can be clicked.
async function createFirst(page) {
  const toggle = page.locator('[data-assistant-none] [data-bs-toggle="dropdown"][data-assistant-new-label]');
  if (await toggle.count()) {
    await toggle.click();
    await page.waitForSelector('[data-assistant-none] .dropdown-menu.show', { timeout: 8000 });
  }
  await afterSwap(page, () => page.locator("[data-assistant-none] [data-assistant-new]").first().click());
}

// openConversation opens one assistant's page, the address a notification link
// carries.
async function openConversation(page, id) {
  await page.goto(`${BASE}/assistants/${id}`, { waitUntil: "domcontentloaded" });
  await dismissUpdate(page);
  await page.waitForSelector(READY, { timeout: 15000 });
}

// closePanel leaves the assistant: the page is the surface, so leaving it is
// going somewhere else, and a surface that is off screen reads no news.
async function closePanel(page) {
  if (!new URL(page.url()).pathname.startsWith("/assistants")) return;
  await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
  await dismissUpdate(page);
}

// openView brings one of the page's lists on screen: the assistants stand in
// the list column, the jobs and the memory in the aside, which is inline on a
// wide window and a sheet the head's buttons open below xl.
async function openView(page, view, readySelector) {
  // The area's own address is the list and carries no surface, so only an
  // assistant's own page counts as being there already.
  if (!/^\/assistants\/[^/]+$/.test(new URL(page.url()).pathname)) await openAssistant(page);
  if (view === "jobs" && !(await page.locator("#assistant-aside").isVisible())) {
    await page.click("[data-assistant-watching-button]");
    await page.waitForSelector("#assistant-aside.show", { timeout: 8000 });
  }
  if (view === "memory" && !(await page.locator("#assistant-memory.show").count())) {
    await page.click(".dc-app > .dc-ctx [data-assistant-memory-button]");
    await page.waitForSelector("#assistant-memory.show", { timeout: 8000 });
  }
  await page.waitForSelector(readySelector, { timeout: 15000 });
}

// unfoldJob opens a steered coder's row. The fold holds the text, what the
// coder was sent, what it is measured against and what came back, so a check
// that reads one of those opens the row first, the way a person does. The
// actions stand outside the fold and need no opening.
async function unfoldJob(page, id) {
  const row = page.locator(`[data-assistant-job="${id}"]`);
  if (await row.locator("[data-assistant-job-details].show").count()) return;
  await row.locator("[data-assistant-job-fold]").click();
  await page.waitForSelector(`[data-assistant-job="${id}"] [data-assistant-job-details].show`, { timeout: 8000 });
}

// NEW_MENU is the list column's new assistant control, where making one lives
// now that the composer's ring only carries this assistant's models. The
// composer's own toggle carries the same attribute, so both are scoped to
// their surface: the list column through .dc-ctx, the composer (MODEL_MENU)
// through dc-assistant.
const NEW_MENU = '.dc-app > .dc-ctx [data-bs-toggle="dropdown"][data-assistant-new-label]';

// MODEL_MENU is the composer's own ring button, dedicated to this assistant's
// models: the coder it runs on and the Chat and Checks selects. It carries no
// New with entries any more, those moved to NEW_MENU in the list column.
const MODEL_MENU = 'dc-assistant [data-bs-toggle="dropdown"][data-assistant-new-label]';

// A new assistant posts through pe.js and lands on the new page, so a change
// is seen through the swapped body: a mark set before the click is gone
// after it, then the surface is ready again.
async function afterSwap(page, act) {
  await page.evaluate(() => { document.querySelector(".dc-app").dataset.runnerSwap = "1"; });
  await act();
  await page.waitForFunction(() => !document.querySelector(".dc-app")?.dataset.runnerSwap, null, { timeout: 15000 });
  await page.waitForSelector(READY, { timeout: 15000 });
}

// The fakes differ per coder, and the checks below read claude's answers, so a
// host with both installed picks claude explicitly. With one coder the picker
// is not rendered at all and the conversation already runs on it.
async function useClaude(page) {
  const picker = page.locator('.dc-app > .dc-ctx [data-assistant-new="claude"]');
  const onClaude = (await page.locator("dc-assistant").getAttribute("data-assistant-coder")) === "claude";
  if (!(await picker.count()) || onClaude) {
    return page.locator("dc-assistant").getAttribute("assistant-id");
  }
  await page.click(NEW_MENU);
  await afterSwap(page, () => picker.click());
  await page.waitForSelector('dc-assistant[data-assistant-coder="claude"][ready]', { timeout: 15000 });
  return page.locator("dc-assistant").getAttribute("assistant-id");
}

// Always the coder the assistant on screen runs on, so a run that reads
// claude's answers keeps reading claude's. The post lands on the new
// assistant's page, the address names the one that answers.
async function newConversation(page) {
  const current = await page.locator("dc-assistant").getAttribute("data-assistant-coder").catch(() => null);
  const dropdown = page.locator(NEW_MENU);
  if (await dropdown.count()) await dropdown.click();
  await afterSwap(page, () => page.locator(current ? `.dc-app > .dc-ctx [data-assistant-new="${current}"]` : ".dc-app > .dc-ctx [data-assistant-new]").first().click());
  const target = new URL(page.url()).pathname.split("/").pop();
  assert(target && target.length > 8, `the new assistant did not land on a page: ${page.url()}`);
  assert((await page.locator("dc-assistant").getAttribute("assistant-id")) === target, "the surface shows another assistant than the address");
  return target;
}

// SVG_CODER_VIEWBOX names the coders whose ring glyph is an svg, each with
// the viewBox coder_icon.gohtml draws it in; every other coder (copilot
// today) is a font icon, wrapped in a span the ring icon template centers
// the same way.
const SVG_CODER_VIEWBOX = { claude: "0 0 24 24", opencode: "0 0 256 320" };

// coderIDs reads which coders this host actually offers, out of the New
// assistant button's own entries in the list column, so a check that must
// hold for every installed coder hardcodes none of them: with one coder
// installed there is no dropdown and the assistant on screen already runs on
// the one there is.
async function coderIDs(page) {
  const ids = await page.locator(".dc-app > .dc-ctx [data-assistant-new]").evaluateAll(
    (nodes) => nodes.map((n) => n.getAttribute("data-assistant-new")),
  );
  if (ids.length) return [...new Set(ids)];
  return [await page.locator("dc-assistant").getAttribute("data-assistant-coder")];
}

// switchToCoder lands the page's own assistant on the given coder, making a
// fresh one when the one on screen runs on another (useClaude's pattern, for
// any coder id).
async function switchToCoder(page, coderID) {
  if ((await page.locator("dc-assistant").getAttribute("data-assistant-coder")) === coderID) {
    return page.locator("dc-assistant").getAttribute("assistant-id");
  }
  const picker = page.locator(`.dc-app > .dc-ctx [data-assistant-new="${coderID}"]`);
  const dropdown = page.locator(NEW_MENU);
  if (await dropdown.count()) await dropdown.click();
  await afterSwap(page, () => picker.click());
  await page.waitForSelector(`dc-assistant[data-assistant-coder="${coderID}"][ready]`, { timeout: 15000 });
  return page.locator("dc-assistant").getAttribute("assistant-id");
}

// A turn that replaces the last answer (a retry) settles into a new message,
// so the wait keys on the id changing. Waiting for a settled state alone would
// pass on the old bubble that is still on screen.
async function clickAndWait(page, selector) {
  const before = await page.locator('[data-role="assistant"]').last().getAttribute("data-message-id");
  await page.click(selector);
  await page.waitForFunction((previous) => {
    const nodes = document.querySelectorAll('[data-role="assistant"]');
    const last = nodes[nodes.length - 1];
    return last && last.getAttribute("data-message-id") !== previous;
  }, before, { timeout: 20000 });
  await waitSettled(page);
}

// The memory is shared by every assistant, so the run clears its own entries
// first instead of assuming an empty memory. Saving under a title that
// exists deliberately writes a second file, which would make the checks below
// read the leftover one.
async function dropRunnerMemories(page) {
  for (let i = 0; i < 5; i += 1) {
    const row = page.locator(`[data-memory-entry^="${memorySlug}"]`).first();
    if (!(await row.count())) return;
    await row.locator('form[data-confirm] button[type="submit"]').click();
    await L.confirmSwal(page);
    await page.waitForFunction(
      (count) => document.querySelectorAll("[data-memory-entry]").length === count,
      (await page.locator("[data-memory-entry]").count()) - 1,
      { timeout: 10000 },
    );
  }
}

async function waitSettled(page, timeout = 20000) {
  await page.waitForFunction(() => {
    const nodes = document.querySelectorAll("[data-assistant-message]");
    const last = nodes[nodes.length - 1];
    return last && last.getAttribute("data-state") !== "streaming";
  }, null, { timeout });
}

// The context ring around the new conversation button. Its fill is a dash of the
// percentage on a circle whose circumference is exactly 100, so the attribute is
// the number, and the level attribute is what colors it (absent below 85).
function ringFill(page) {
  return page.locator("dc-assistant [data-assistant-ring-fill]").first().getAttribute("stroke-dasharray");
}

function ringLevel(page) {
  return page.locator("dc-assistant [data-assistant-ring]").first().getAttribute("data-assistant-ring-level");
}

function newLabel(page) {
  return page.locator("dc-assistant [data-assistant-new-label]").first().getAttribute("title");
}

// An assistant takes one turn at a time and a second prompt waits in its queue,
// so a check that wants its own turn has to wait for the one before it to be
// over instead of queueing behind it. The wait is on the surface's own running
// mark, the same one the stop button hangs on, not on the last bubble: a bubble
// settles a moment before the turn stops counting as running.
async function idle(page) {
  await page.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
  await page.waitForFunction(
    () => !document.querySelector("dc-assistant")?.hasAttribute("running"),
    null,
    { timeout: 60000 },
  );
}

async function send(page, text) {
  await idle(page);
  const before = await page.locator('[data-role="assistant"]').count();
  await page.fill("[data-assistant-input]", text);
  await page.click("[data-assistant-send]");
  await page.waitForFunction(
    (count) => document.querySelectorAll('[data-role="assistant"]').length > count,
    before,
    { timeout: 20000 },
  );
}

// A one pixel PNG and a tiny wav, small enough to inline and real enough that
// the browser decodes them.
const PNG = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==",
  "base64",
);
const WAV = Buffer.concat([
  Buffer.from("RIFF", "ascii"), Buffer.from([0x2c, 0, 0, 0]),
  Buffer.from("WAVEfmt ", "ascii"), Buffer.from([16, 0, 0, 0, 1, 0, 1, 0, 0x44, 0xac, 0, 0, 0x88, 0x58, 1, 0, 2, 0, 16, 0]),
  Buffer.from("data", "ascii"), Buffer.from([8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0]),
]);
const WIDE_SVG = Buffer.from('<svg xmlns="http://www.w3.org/2000/svg" width="2400" height="200"><rect width="2400" height="200" fill="#888"/></svg>');

// The file input and the files of a sent message share the attribute, so the
// picker is addressed as the input it is: a transcript that carries an
// attachment would otherwise make this ambiguous.
async function attach(page, files) {
  await page.setInputFiles("input[data-assistant-file]", files);
  await page.waitForFunction(
    (count) => document.querySelectorAll("[data-assistant-attachment-remove]").length === count,
    files.length,
    { timeout: 20000 },
  );
}

// startCoder and postForm are what the jobs checks need: a coder to steer and
// the conversation's own dispatch route, the same one the page's forms and
// the assistant's commands post to.
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

async function postForm(page, instance, fields) {
  return page.evaluate(async ([id, values]) => {
    const body = new URLSearchParams(values);
    body.set("csrf_token", document.querySelector('meta[name="csrf-token"]').content);
    const res = await fetch(`/assistants/${id}`, {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded", Accept: "application/json" },
      body: body.toString(),
    });
    return { status: res.status, body: await res.json().catch(() => ({})) };
  }, [instance, fields]);
}

// The jobs of the assistant sit on one path, not on a conversation: steering and
// calling a job off post there, from the page's list and from the command line.
async function postJobs(page, fields) {
  return page.evaluate(async (values) => {
    const body = new URLSearchParams(values);
    body.set("csrf_token", document.querySelector('meta[name="csrf-token"]').content);
    const res = await fetch("/assistants/jobs", {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded", Accept: "application/json" },
      body: body.toString(),
    });
    return { status: res.status, body: await res.json().catch(() => ({})) };
  }, fields);
}

L.runFeature("assistant", async ({ browser, ctx, page, run, mobilePage }) => {
  let jobCoder = "";
  let chatID = "";
  let freshID = "";

  // Nothing creates an assistant by itself any more, so the page's own empty
  // state is the first thing there is, and its button is the only way to the
  // first one.
  await run("the page offers the first assistant and creates none by itself", async () => {
    await page.goto(`${BASE}/assistants`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    const empty = await page.locator("[data-assistant-none]").count();
    if (empty) {
      assert((await page.locator("dc-assistant").count()) === 0, "an empty page rendered a surface anyway");
      assert((await page.locator("[data-assistant-none] [data-assistant-new]").count()) >= 1,
        "the empty state offers no way to the first assistant");
    }
    const opened = await openAssistant(page);
    assert(opened && opened.length > 8, "no assistant id on the surface");
    assert(await page.locator("[data-assistant-input]").isVisible(), "no composer");
    // The run takes an assistant of its own instead of reading whatever the
    // instance already held.
    chatID = await useClaude(page);
    if (!(await page.locator("[data-assistant-empty]").count())) {
      chatID = await newConversation(page);
    }
    assert(await page.locator("[data-assistant-empty]").isVisible(), "the run's assistant is not empty");
  });

  // The empty state used to promise a model choice for a later release. That
  // release is here, and the note now says where the choice is made.
  await run("the empty state points at the ring button for the model", async () => {
    const note = await page.locator("[data-assistant-empty] [role=note]").innerText();
    assert(!/later release/i.test(note), `the empty state still promises a later release: ${note}`);
    assert(/ring button/.test(note), `the empty state does not say where the model is picked: ${note}`);
  });

  // The area's own address leads to the assistant last looked at, the way
  // /terminals leads to a terminal: the list stands in the column beside it
  // and marks its row, so the area opens on what is in it and on the thread
  // at once. The answer is a See Other nobody may cache, never a permanent
  // one, or the browser would keep reopening the assistant of the first click.
  await run("the bare address opens the assistant last looked at, the list beside it", async () => {
    // The recent store keeps whole seconds and breaks a tie by id, so this
    // open has to land in a second of its own to be the one looked at last.
    await sleep(1100);
    await openConversation(page, chatID);
    const answer = await page.request.get(`${BASE}/assistants`, { maxRedirects: 0 });
    assert(answer.status() === 303, `the area answered ${answer.status()} instead of a See Other`);
    assert(answer.headers().location === `/assistants/${chatID}`,
      `the area led to ${answer.headers().location} instead of the one last looked at`);
    assert((answer.headers()["cache-control"] || "").includes("no-store"),
      `the area's answer may be cached: ${answer.headers()["cache-control"]}`);

    await page.goto(`${BASE}/assistants`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    await page.waitForSelector(READY, { timeout: 15000 });
    const one = await page.evaluate((id) => ({
      path: window.location.pathname,
      column: Boolean(document.querySelector("[data-assistant-rows]")),
      surface: document.querySelector("dc-assistant")?.getAttribute("assistant-id") === id,
      active: document.querySelector(`[data-assistant-rows] [data-assistant-instance="${id}"]`)?.classList.contains("active"),
      marked: document.querySelectorAll("[data-assistant-rows] [data-assistant-instance].active").length,
    }), chatID);
    assert(one.path === `/assistants/${chatID}`, `the bare address landed on ${one.path}`);
    assert(one.column && one.surface && one.active && one.marked === 1,
      `the assistant's own page is not whole: ${JSON.stringify(one)}`);
    return "the one last looked at, its row marked in the list";
  });

  await run("a turn runs end to end over the assistant's own stream", async () => {
    await send(page, "MAGIC what is the word");
    await waitSettled(page);
    const text = await page.locator('[data-role="assistant"]').last().innerText();
    assert(text.includes("AIRPORT"), `answer missing: ${text}`);
  });

  await run("the transcript survives a reload of the page", async () => {
    await page.reload({ waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    await page.waitForSelector(READY, { timeout: 15000 });
    assert((await page.locator("[data-assistant-message]").count()) === 2, "transcript did not survive");
  });

  // How full the coder's context window is, as the ring around the new
  // conversation button. Both fakes report the same 68 percent, and CONTEXT_HIGH
  // pushes them to 96, where the ring turns red. The number moves once per turn,
  // on the end frame, and the next page load renders the last one server side, so
  // the panel never opens on an empty ring and then fills.
  await run("the context ring follows the turn and comes back rendered", async () => {
    assert(await ringFill(page) === "68 100", `the turn above did not fill the ring: ${await ringFill(page)}`);
    assert(!(await ringLevel(page)), "68 percent must stay quiet, no level");
    assert((await newLabel(page)).includes("Context 68 percent"),
      `the percentage is not readable without a pointer: ${await newLabel(page)}`);

    // Live, off the end frame: no reload between the answer and the new ring.
    await send(page, "MAGIC CONTEXT_HIGH nearly full now");
    await waitSettled(page);
    await page.waitForFunction(
      () => document.querySelector("[data-assistant-ring-fill]")?.getAttribute("stroke-dasharray") === "96 100",
      null,
      { timeout: 10000 },
    );
    assert(await ringLevel(page) === "full", `96 percent must go loud, got ${await ringLevel(page)}`);
    assert((await newLabel(page)).includes("Context 96 percent"), `the label kept the old number: ${await newLabel(page)}`);

    await page.reload({ waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    await page.waitForSelector(READY, { timeout: 15000 });
    assert(await ringFill(page) === "96 100", `the ring is not rendered on load: ${await ringFill(page)}`);
    assert(await ringLevel(page) === "full", "the level is not rendered on load");

    // The button still opens its models menu: the ring sits inside the
    // button so it cannot break that either.
    const opens = await page.locator(MODEL_MENU).first().getAttribute("data-bs-toggle");
    assert(opens === "dropdown", "the ring button lost its dropdown");
    return "68, live to 96, rendered on load";
  });

  // The ring is drawn inside the button and the icon inside the ring, so the
  // three boxes have to share one center: the button, the ring's svg and the
  // icon, which is an svg path and not a webfont glyph because the font puts a
  // glyph's ink flush left in its box. The svg's box is the button's padding
  // box inset by one pixel on every side, and it is read as a box and never as
  // a center alone: an iPhone stood the ring one pixel right and down at 38px,
  // which is what an engine draws that sizes an auto width svg from its
  // containing block instead of from its two insets and then drops the
  // over-constrained right and bottom (WebKit before its inset aware replaced
  // width), and a center within one pixel let exactly that through. So every
  // side's gap to the padding box has to be the inset, which pins the size
  // with the place, the track circle's own box, which carries the viewBox
  // scaling, has to sit on the same center, and the tolerance is half a pixel.
  // Measured on the desktop page and on the phone, in light and dark, at rest
  // and with the button hovered and its menu open, since the halo widens the
  // stroke there and the phone's picture was taken in that state.
  await run("the ring fills the button's inset box and shares its center with the icon", async () => {
    const sel = "dc-assistant .assistant-composer [data-assistant-new-label]";
    const boxes = (p) => p.evaluate((s) => {
      const button = document.querySelector(s);
      const box = (el) => { const r = el.getBoundingClientRect(); return { x: r.x, y: r.y, w: r.width, h: r.height, cx: r.x + r.width / 2, cy: r.y + r.height / 2 }; };
      const cs = getComputedStyle(button);
      return {
        theme: document.documentElement.getAttribute("data-bs-theme"),
        border: { l: parseFloat(cs.borderLeftWidth), r: parseFloat(cs.borderRightWidth), t: parseFloat(cs.borderTopWidth), b: parseFloat(cs.borderBottomWidth) },
        button: box(button),
        ring: box(button.querySelector("[data-assistant-ring]")),
        track: box(button.querySelector("[data-assistant-ring] .dc-context-ring-track")),
        icon: box(button.querySelector("[data-assistant-ring-icon]")),
      };
    }, sel);
    const TOL = 0.5, INSET = 1;
    const fmt = (v) => v.toFixed(2);
    const check = (c, where) => {
      for (const part of ["ring", "track", "icon"]) {
        assert(c[part].w > 0 && c[part].h > 0, `${where}: the ${part} has no box`);
        const dx = Math.abs(c[part].cx - c.button.cx);
        const dy = Math.abs(c[part].cy - c.button.cy);
        assert(dx <= TOL && dy <= TOL, `${where}: the ${part} is off the button's center by ${fmt(dx)}, ${fmt(dy)}`);
      }
      const gaps = {
        left: c.ring.x - (c.button.x + c.border.l),
        right: c.button.x + c.button.w - c.border.r - (c.ring.x + c.ring.w),
        top: c.ring.y - (c.button.y + c.border.t),
        bottom: c.button.y + c.button.h - c.border.b - (c.ring.y + c.ring.h),
      };
      for (const [side, gap] of Object.entries(gaps)) {
        assert(Math.abs(gap - INSET) <= TOL, `${where}: the ring's ${side} gap to the padding box is ${fmt(gap)}, not ${INSET} (ring ${fmt(c.ring.w)}x${fmt(c.ring.h)} in a ${fmt(c.button.w)}x${fmt(c.button.h)} button)`);
      }
      const dx = Math.abs(c.icon.cx - c.ring.cx);
      const dy = Math.abs(c.icon.cy - c.ring.cy);
      assert(dx <= TOL && dy <= TOL, `${where}: the icon is off the ring's center by ${fmt(dx)}, ${fmt(dy)}`);
    };
    const states = async (p, where) => {
      const c = await boxes(p);
      check(c, `${where} ${c.theme} at rest`);
      await p.click(sel);
      await p.waitForFunction((s) => document.querySelector(s).classList.contains("show"), sel, { timeout: 5000 });
      await p.hover(sel);
      check(await boxes(p), `${where} ${c.theme} hovered with the menu open`);
      await p.keyboard.press("Escape");
      await p.waitForFunction((s) => !document.querySelector(s).classList.contains("show"), sel, { timeout: 5000 });
      await p.mouse.move(0, 0);
    };
    const both = async (p, where) => {
      await states(p, where);
      await p.emulateMedia({ colorScheme: "dark" });
      await p.waitForFunction(() => document.documentElement.getAttribute("data-bs-theme") === "dark", null, { timeout: 5000 });
      await states(p, where);
      await p.emulateMedia({ colorScheme: null });
    };
    await both(page, "desktop");
    const mp = await mobilePage();
    await openConversation(mp, chatID);
    await both(mp, "390px");
    return "desktop and 390px, light and dark, at rest and open, the inset box within half a pixel";
  });

  // The ring wears the icon of the coder each assistant runs on, never a
  // fixed glyph, so the center and box math the check above ran once now
  // runs once per coder this host installs, plus a shape check that it is
  // really that coder's own glyph: an svg with that coder's own viewBox
  // where the glyph is an svg (claude, opencode), a span wrapping the
  // webfont glyph otherwise (copilot). Rest state only: the geometry itself
  // does not change with the coder, only what is drawn inside it, so
  // light/dark and hover/open stay the check above's job. An assistant per
  // coder is the honest way to render each icon, and the cheap one too: a
  // bare creation never starts the coder's own CLI, only a chat turn does,
  // and this check sends none.
  await run("the ring icon is the coder's own glyph, centered, for every installed coder", async () => {
    const sel = "dc-assistant .assistant-composer [data-assistant-new-label]";
    const shapeOf = (p) => p.evaluate((s) => {
      const button = document.querySelector(s);
      const icon = button.querySelector("[data-assistant-ring-icon]");
      const box = (el) => { const r = el.getBoundingClientRect(); return { w: r.width, h: r.height, cx: r.x + r.width / 2, cy: r.y + r.height / 2 }; };
      return {
        tag: icon?.tagName.toLowerCase() || "",
        viewBox: icon?.getAttribute("viewBox") || "",
        glyph: icon?.querySelector("i")?.className || "",
        button: box(button),
        icon: icon ? box(icon) : null,
      };
    }, sel);
    const check = (shape, coderID, where) => {
      assert(shape.icon && shape.icon.w > 0 && shape.icon.h > 0, `${where}: ${coderID}'s ring icon has no box: ${JSON.stringify(shape)}`);
      const dx = Math.abs(shape.icon.cx - shape.button.cx);
      const dy = Math.abs(shape.icon.cy - shape.button.cy);
      assert(dx <= 0.5 && dy <= 0.5,
        `${where}: ${coderID}'s ring icon is off the button's center by ${dx.toFixed(2)}, ${dy.toFixed(2)}`);
      const wantViewBox = SVG_CODER_VIEWBOX[coderID];
      if (wantViewBox) {
        assert(shape.tag === "svg" && shape.viewBox === wantViewBox,
          `${where}: ${coderID} did not draw its own svg: ${JSON.stringify(shape)}`);
      } else {
        assert(coderID === "copilot" && shape.tag === "span" && shape.glyph.includes("ti-brand-github-copilot"),
          `${where}: ${coderID} did not wrap its font glyph: ${JSON.stringify(shape)}`);
      }
    };

    const ids = await coderIDs(page);
    const assistants = {};
    for (const coderID of ids) {
      assistants[coderID] = await switchToCoder(page, coderID);
      check(await shapeOf(page), coderID, "desktop");
    }
    // Every one of those assistants already exists, so the phone page opens
    // each by id directly: no dropdown to reach, which below lg sits inside
    // the list column's sheet and not on screen.
    const mp = await mobilePage();
    for (const coderID of ids) {
      await openConversation(mp, assistants[coderID]);
      check(await shapeOf(mp), coderID, "390px");
    }
    // Back to the assistant every check after this one reads.
    await openConversation(page, chatID);
    return `${ids.length} coder(s): ${ids.join(", ")}, desktop and 390px`;
  });

  // The ring button's menu carries this assistant's models and nothing else,
  // making a new one moved to the list column: the button's own icon says
  // which coder without a head line any more, a select for the chat, one
  // for the checks and one for the triggers over that coder's list, every
  // pick stored the moment it is made and standing after a reload, the
  // third one round tripping through the same form=model post as
  // trigger_model, Other… taking a name the list does not hold, which then
  // stands as the selected entry, and the turn really carrying the pick,
  // which the fake proves by answering MODEL with the model it was started
  // with.
  await run("the ring menu picks the models and the choice survives a reload", async () => {
    const MENU = "dc-assistant [data-assistant-new-label] + .dropdown-menu";
    const pick = (kind) => `dc-assistant [data-assistant-model-pick="${kind}"] select`;
    const openMenu = async () => {
      if (await page.locator(`${MENU}.show`).count()) return;
      await page.click(MODEL_MENU);
      await page.waitForSelector(`${MENU}.show`, { timeout: 8000 });
    };
    const closeMenu = async () => {
      if (!(await page.locator(`${MENU}.show`).count())) return;
      await page.keyboard.press("Escape");
      await page.waitForSelector(`${MENU}.show`, { state: "detached", timeout: 8000 });
    };
    const reopen = async () => {
      await page.reload({ waitUntil: "domcontentloaded" });
      await dismissUpdate(page);
      await page.waitForSelector(READY, { timeout: 15000 });
      await openMenu();
    };

    await openMenu();
    const shape = await page.evaluate((menuSel) => {
      const menu = document.querySelector(menuSel);
      const box = menu.getBoundingClientRect();
      const options = (kind) => [...menu.querySelectorAll(`[data-assistant-model-pick="${kind}"] select option`)].map((o) => o.textContent.trim());
      return {
        chat: options("chat"),
        check: options("check"),
        trigger: options("trigger"),
        labels: [...menu.querySelectorAll("[data-assistant-models] label")].map((l) => l.textContent.trim()),
        chatValue: menu.querySelector('[data-assistant-model-pick="chat"] select').value,
        checkValue: menu.querySelector('[data-assistant-model-pick="check"] select').value,
        triggerValue: menu.querySelector('[data-assistant-model-pick="trigger"] select').value,
        hints: [...menu.querySelectorAll(".form-hint")].map((h) => h.textContent.trim()).join(" | "),
        saveHidden: menu.querySelector("[data-assistant-model-save]")?.hidden === true,
        typedHidden: [...menu.querySelectorAll("[data-model-other-input]")].every((i) => i.hidden && i.disabled),
        newWith: menu.querySelectorAll("[data-assistant-new]").length,
        inside: box.left >= 0 && box.right <= window.innerWidth && box.top >= 0 && box.bottom <= window.innerHeight,
      };
    }, MENU);
    // No head line names the coder any more, the ring icon itself is that
    // coder's own glyph: claude's is an svg with its own viewBox, never the
    // font glyph the plus button carries.
    const toggleIcon = await page.locator(MODEL_MENU).evaluate((button) => ({
      icon: !!button.querySelector("[data-assistant-ring-icon]"),
      tag: button.querySelector("[data-assistant-ring-icon]")?.tagName.toLowerCase() || "",
      viewBox: button.querySelector("[data-assistant-ring-icon]")?.getAttribute("viewBox") || "",
      glyph: button.querySelector("i")?.className || "",
    }));
    assert(toggleIcon.icon && toggleIcon.tag === "svg" && toggleIcon.viewBox === "0 0 24 24" && !toggleIcon.glyph.includes("ti-message-plus"),
      `the ring button did not switch to claude's own icon: ${JSON.stringify(toggleIcon)}`);
    assert(shape.chat[0] === "Coder default (CLI)" && shape.chat.includes("haiku") && shape.chat[shape.chat.length - 1] === "Other…",
      `the chat select is off: ${JSON.stringify(shape)}`);
    assert(shape.check[0] === "Same as chat" && shape.check.includes("sonnet") && shape.check[shape.check.length - 1] === "Other…",
      `the checks select is off: ${JSON.stringify(shape)}`);
    assert(shape.trigger[0] === "Same as chat" && shape.trigger.includes("fable") && shape.trigger[shape.trigger.length - 1] === "Other…",
      `the triggers select is off: ${JSON.stringify(shape)}`);
    assert(shape.labels.join("|") === "Chat|Checks|Triggers", `the three picks are not labelled in order: ${JSON.stringify(shape.labels)}`);
    assert(shape.chatValue === "" && shape.checkValue === "" && shape.triggerValue === "", `a fresh assistant starts on the coder's default: ${JSON.stringify(shape)}`);
    assert(shape.hints === "Aliases, always the newest of each family.", `the one line under the picks is off: ${JSON.stringify(shape)}`);
    assert(shape.saveHidden && shape.typedHidden, `the no JS parts stand while the element runs: ${JSON.stringify(shape)}`);
    assert(shape.newWith === 0, `the ring menu still carries New with entries: ${JSON.stringify(shape)}`);
    assert(shape.inside, `the menu stands outside the desktop viewport: ${JSON.stringify(shape)}`);

    await page.selectOption(pick("chat"), "haiku");
    await page.waitForSelector('.dc-toast:has-text("Chat, checks and triggers on haiku.")', { timeout: 8000 });
    await page.selectOption(pick("check"), "sonnet");
    await page.waitForSelector('.dc-toast:has-text("Chat and triggers on haiku, checks on sonnet.")', { timeout: 8000 });
    await page.selectOption(pick("trigger"), "fable");
    await page.waitForSelector('.dc-toast:has-text("Chat on haiku, checks on sonnet, triggers on fable.")', { timeout: 8000 });
    assert(await page.locator(`${MENU}.show`).count(), "a pick closed the menu");

    await reopen();
    const kept = await page.evaluate((p) => ({ chat: document.querySelector(p.chat).value, check: document.querySelector(p.check).value, trigger: document.querySelector(p.trigger).value }), { chat: pick("chat"), check: pick("check"), trigger: pick("trigger") });
    assert(kept.chat === "haiku" && kept.check === "sonnet" && kept.trigger === "fable", `the picks did not survive the reload: ${JSON.stringify(kept)}`);

    // The keyboard steps the select: a real arrow on the focused select moves
    // it to the neighbouring entry and the focus stays on it. Bootstrap's
    // dropdown handler used to take the key on the document and move the
    // focus among the menu's own items instead, which selectOption never
    // notices.
    await page.focus(pick("chat"));
    const neighbours = await page.evaluate((p) => {
      const s = document.querySelector(p);
      return { above: s.options[s.selectedIndex - 1]?.value, below: s.options[s.selectedIndex + 1]?.value };
    }, pick("chat"));
    assert(neighbours.above !== undefined, `haiku has no entry above it to step onto: ${JSON.stringify(neighbours)}`);
    await page.keyboard.press("ArrowUp");
    const stepped = await page.evaluate((p) => {
      const s = document.querySelector(p);
      return { value: s.value, focused: document.activeElement === s, open: !!s.closest(".dropdown-menu.show") };
    }, pick("chat"));
    assert(stepped.value === neighbours.above && stepped.focused && stepped.open,
      `ArrowUp did not step the focused select: ${JSON.stringify({ neighbours, stepped })}`);
    await page.waitForSelector(`.dc-toast:has-text("Chat on ${neighbours.above}, checks on sonnet, triggers on fable.")`, { timeout: 8000 });
    await page.keyboard.press("ArrowDown");
    const back = await page.evaluate((p) => {
      const s = document.querySelector(p);
      return { value: s.value, focused: document.activeElement === s };
    }, pick("chat"));
    assert(back.value === "haiku" && back.focused, `ArrowDown did not step the focused select back: ${JSON.stringify(back)}`);
    await page.waitForSelector('.dc-toast:has-text("Chat on haiku, checks on sonnet, triggers on fable.")', { timeout: 8000 });

    await page.selectOption(pick("chat"), { label: "Other…" });
    const typed = page.locator('dc-assistant [data-assistant-model-pick="chat"] [data-model-other-input]');
    await typed.waitFor({ state: "visible", timeout: 5000 });
    assert(await page.locator(pick("chat")).evaluate((s) => !s.hasAttribute("name")), "the select still posts beside the typed name");
    await typed.fill("claude-haiku-4-5");
    await typed.press("Enter");
    await page.waitForSelector('.dc-toast:has-text("Chat on claude-haiku-4-5, checks on sonnet, triggers on fable.")', { timeout: 8000 });
    assert(await page.locator(pick("chat")).evaluate((s) => s.value === "claude-haiku-4-5" && s.hasAttribute("name")),
      "the typed name did not become the selected entry");
    await reopen();
    const typedKept = await page.evaluate((p) => {
      const s = document.querySelector(p);
      return { value: s.value, label: s.selectedOptions[0]?.textContent.trim() };
    }, pick("chat"));
    assert(typedKept.value === "claude-haiku-4-5" && typedKept.label === "claude-haiku-4-5",
      `a stored name the list does not hold is not the selected entry: ${JSON.stringify(typedKept)}`);
    await closeMenu();

    await send(page, "MODEL which one is it");
    await waitSettled(page);
    const answer = await page.locator('[data-role="assistant"]').last().innerText();
    assert(answer.includes("model: claude-haiku-4-5"), `the turn did not run on the picked model: ${answer}`);

    await page.setViewportSize({ width: 390, height: 844 });
    await openMenu();
    const phone = await page.evaluate((menuSel) => {
      const box = document.querySelector(menuSel).getBoundingClientRect();
      return { left: Math.round(box.left), right: Math.round(box.right), width: window.innerWidth, selects: document.querySelectorAll(`${menuSel} select`).length };
    }, MENU);
    assert(phone.left >= 0 && phone.right <= phone.width && phone.selects === 3, `the menu does not fit the phone: ${JSON.stringify(phone)}`);
    await closeMenu();
    await page.setViewportSize({ width: 1360, height: 900 });

    // Back to the CLI's default, so the checks after this one run the fake
    // the way they always did. A cleared pick follows the chat pick, Same as
    // chat, which the sentence says by folding it into the chat's clause.
    await openMenu();
    await page.selectOption(pick("check"), "");
    await page.waitForSelector('.dc-toast:has-text("Chat and checks on claude-haiku-4-5, triggers on fable.")', { timeout: 8000 });
    await page.selectOption(pick("trigger"), "");
    await page.waitForSelector('.dc-toast:has-text("Chat, checks and triggers on claude-haiku-4-5.")', { timeout: 8000 });
    await page.selectOption(pick("chat"), "");
    await page.waitForSelector(".dc-toast:has-text(\"Chat, checks and triggers on the CLI's default.\")", { timeout: 8000 });
    await closeMenu();
    return "Claude named, haiku, sonnet and fable kept, the arrows step the focused select, a typed name kept, the turn ran on it, the menu fits a phone";
  });

  // A pick set from outside the page, on another tab or by the assistant's
  // own assistant-models-set, which posts the same form=model, reaches the
  // ring's selects over the assistant's own stream: the three move without a
  // reload, a name the list does not hold stands as the selected entry with
  // the typed field hidden, a select that holds the focus keeps its value
  // until the focus leaves it, a save from the page itself never bounces, a
  // second assistant open beside it keeps its own picks because the frame
  // travels its assistant's stream alone, and a reload shows the same.
  await run("the ring menu follows picks set from outside without a reload", async () => {
    const id = await page.locator("dc-assistant").getAttribute("assistant-id");
    const MENU = "dc-assistant [data-assistant-new-label] + .dropdown-menu";
    const pick = (kind) => `dc-assistant [data-assistant-model-pick="${kind}"] select`;
    const token = await page.locator('meta[name="csrf-token"]').getAttribute("content");
    // The post comes from outside the page, the runner's own request with
    // the page's cookies: the element never sees it, the way it never sees
    // the CLI's post.
    const setOutside = async (assistantID, fields) => {
      const res = await page.request.post(`${BASE}/assistants/${assistantID}`, {
        form: { csrf_token: token, form: "model", ...fields },
        headers: { Accept: "application/json" },
      });
      assert(res.status() === 200, `the outside post was refused: ${res.status()} ${await res.text()}`);
    };
    const values = (p) => p.evaluate(() => Object.fromEntries(["chat", "check", "trigger"].map((kind) => {
      const pick = document.querySelector(`dc-assistant [data-assistant-model-pick="${kind}"]`);
      const select = pick.querySelector("select");
      const typed = pick.querySelector("[data-model-other-input]");
      return [kind, {
        value: select.value,
        label: select.selectedOptions[0]?.textContent.trim() || "",
        other: select.querySelector("[data-model-other]")?.value,
        typedAway: typed.hidden && typed.disabled,
        named: select.hasAttribute("name"),
      }];
    })));
    const waitValues = (p, want) => p.waitForFunction((expected) => ["chat", "check", "trigger"].every((kind) =>
      document.querySelector(`dc-assistant [data-assistant-model-pick="${kind}"] select`)?.value === expected[kind]), want, { timeout: 8000 });
    await page.evaluate(() => { document.querySelector(".dc-app").dataset.runnerLive = "1"; });

    // The second assistant stands open on a page of its own with a pick that
    // tells it apart, and its own frame moves its own ring.
    const created = await page.request.post(`${BASE}/assistants/new`, { form: { csrf_token: token, form: "new", coder: "claude" }, headers: { Accept: "application/json" } });
    const otherID = (await created.json().catch(() => ({}))).id || "";
    assert(otherID && otherID !== id, `no second assistant for the check: ${created.status()}`);
    const other = await ctx.newPage();
    await openConversation(other, otherID);
    await setOutside(otherID, { model: "haiku" });
    await waitValues(other, { chat: "haiku", check: "", trigger: "" });

    await setOutside(id, { model: "sonnet", check_model: "haiku", trigger_model: "claude-sonnet-4-5" });
    await waitValues(page, { chat: "sonnet", check: "haiku", trigger: "claude-sonnet-4-5" });
    assert(await page.evaluate(() => document.querySelector(".dc-app").dataset.runnerLive === "1"), "the page reloaded to show the picks");
    let seen = await values(page);
    assert(seen.trigger.label === "claude-sonnet-4-5" && seen.trigger.other === "claude-sonnet-4-5" && seen.trigger.typedAway && seen.trigger.named,
      `a name the list does not hold is not the selected entry with the typed field away: ${JSON.stringify(seen.trigger)}`);
    const beside = await values(other);
    assert(beside.chat.value === "haiku" && beside.check.value === "" && beside.trigger.value === "",
      `the other assistant's ring took the first one's picks: ${JSON.stringify(beside)}`);

    // A select that holds the focus is being edited: it keeps what it shows
    // while the others move, and takes the fresh pick once the focus leaves,
    // here onto the next select, the way Tab moves it. The focus stays inside
    // the menu on purpose, Escape closes it only from inside or its toggle.
    await page.click(MODEL_MENU);
    await page.waitForSelector(`${MENU}.show`, { timeout: 8000 });
    await page.focus(pick("check"));
    await setOutside(id, { model: "haiku", check_model: "fable" });
    await waitValues(page, { chat: "haiku", check: "haiku", trigger: "claude-sonnet-4-5" });
    const held = await page.evaluate((sel) => ({ value: document.querySelector(sel).value, focused: document.activeElement === document.querySelector(sel) }), pick("check"));
    assert(held.value === "haiku" && held.focused, `the focused select did not keep its value: ${JSON.stringify(held)}`);
    await page.focus(pick("trigger"));
    await waitValues(page, { chat: "haiku", check: "fable", trigger: "claude-sonnet-4-5" });

    // The page's own save answers with the same reading and its frame
    // follows: the select shows the pick and never anything else after it.
    const sampling = page.evaluate((sel) => new Promise((resolve) => {
      const select = document.querySelector(sel);
      const samples = [];
      const until = performance.now() + 1500;
      const tick = () => {
        if (samples[samples.length - 1] !== select.value) samples.push(select.value);
        if (performance.now() < until) requestAnimationFrame(tick);
        else resolve(samples);
      };
      tick();
    }), pick("chat"));
    await page.selectOption(pick("chat"), "sonnet");
    await page.waitForSelector('.dc-toast:has-text("Chat on sonnet, checks on fable, triggers on claude-sonnet-4-5.")', { timeout: 8000 });
    const samples = await sampling;
    assert(samples.join(">") === "haiku>sonnet", `the page's own save bounced the select: ${samples.join(">")}`);
    await page.keyboard.press("Escape");
    await page.waitForSelector(`${MENU}.show`, { state: "detached", timeout: 8000 });

    await page.reload({ waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    await page.waitForSelector(READY, { timeout: 15000 });
    seen = await values(page);
    assert(seen.chat.value === "sonnet" && seen.check.value === "fable" && seen.trigger.value === "claude-sonnet-4-5",
      `the reload does not show the picks set from outside: ${JSON.stringify(seen)}`);

    // Back to the CLI's default from outside, live again, and the second
    // assistant goes with its page.
    await setOutside(id, { model: "", check_model: "", trigger_model: "" });
    await waitValues(page, { chat: "", check: "", trigger: "" });
    await other.close();
    const gone = await page.request.post(`${BASE}/assistants/${otherID}`, { form: { csrf_token: token, form: "delete" }, headers: { Accept: "application/json" } });
    assert(gone.status() === 200, `the second assistant could not be deleted: ${gone.status()}`);
    return "three selects moved live, a typed name as the entry, the focused select held until blur, no bounce on the page's own save, the other assistant untouched, the reload agrees";
  });

  // applyModels decides whether an incoming frame is newer than the one it
  // already applied by comparing the two RFC3339Nano stamps, and the compare
  // has to read them as moments in time, not as plain strings: Go marshals
  // UpdatedAt with the fraction's trailing zeros trimmed, so a stamp that is
  // a digit prefix of a later one in the same second ("…15.1Z" against
  // "…15.100000001Z") sorts after it as a string, "Z" coming after every
  // digit and after the dot, and a lexical compare would drop the newer one.
  await run("a models frame is judged by the clock, never by the shape of its stamp", async () => {
    const id = await page.locator("dc-assistant").getAttribute("assistant-id");
    const chatValue = () => page.$eval('dc-assistant [data-assistant-model-pick="chat"] select', (s) => s.value);
    const apply = (chat, updatedAt) => page.evaluate(({ assistantID, chatModel, at }) => {
      document.querySelector("dc-assistant").applyModels(
        { assistant: assistantID, chat: chatModel, check: "", trigger: "", updatedAt: at }, false);
    }, { assistantID: id, chatModel: chat, at: updatedAt });
    // No stamp stands on the element yet, so the first frame below applies
    // whatever the clock on it says, whichever year it names.
    await page.evaluate(() => { document.querySelector("dc-assistant").modelsAt = ""; });

    await apply("haiku", "2026-01-01T00:00:15.1Z");
    assert(await chatValue() === "haiku", "the first frame was not applied");

    // The same instant to the millisecond a browser keeps, only written with
    // more digits, so its stamp is longer and a lexical compare reads it as
    // older, "0" sorting under "Z".
    await apply("sonnet", "2026-01-01T00:00:15.100000001Z");
    assert(await chatValue() === "sonnet",
      "a frame that is no older, only a digit prefix of the one applied, was dropped");

    // An actually older frame, prefix or not, still loses.
    await apply("fable", "2026-01-01T00:00:15Z");
    assert(await chatValue() === "sonnet", "a chronologically older frame was applied over a newer one");

    await page.reload({ waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    await page.waitForSelector(READY, { timeout: 15000 });
    return "a digit prefix stamp that is not older still wins, an actually older one still loses";
  });

  // A name typed under Other… is remembered by the coder's repository, so the
  // trigger form, which reads the same list, offers it afterwards; the form
  // is read the way the dialog reads it, alone with modal=1. Its empty entry
  // names what a trigger without a model of its own runs on, the assistant
  // default: the ring's Triggers pick where one stands, else the chat as it
  // resolves, else the CLI, each set through the same form=model post the
  // ring's menu sends.
  await run("the trigger form's empty entry names the assistant default the ring resolves to", async () => {
    const id = await page.locator("dc-assistant").getAttribute("assistant-id");
    const options = () => page.evaluate(async (assistantID) => {
      const res = await fetch(`/assistants/triggers?form=new&assistant=${assistantID}&modal=1`);
      const doc = new DOMParser().parseFromString(await res.text(), "text/html");
      return [...doc.querySelectorAll('select[name="model"] option')].map((o) => o.textContent.trim());
    }, id);
    const setModels = async (fields) => {
      const answer = await postForm(page, id, { form: "model", ...fields });
      assert(answer.status === 200, `the models could not be set: ${JSON.stringify(answer)}`);
    };
    let list = await options();
    assert(list[0] === "Assistant default (CLI)" && list[list.length - 1] === "Other…", `the trigger form's list is off: ${JSON.stringify(list)}`);
    assert(list.includes("claude-haiku-4-5"), `the trigger form's list does not carry the typed name: ${JSON.stringify(list)}`);
    await setModels({ model: "haiku" });
    list = await options();
    assert(list[0] === "Assistant default (haiku)", `the empty entry does not follow the chat pick: ${JSON.stringify(list)}`);
    await setModels({ trigger_model: "sonnet" });
    list = await options();
    assert(list[0] === "Assistant default (sonnet)", `the empty entry does not follow the ring's Triggers pick: ${JSON.stringify(list)}`);
    await setModels({ model: "", trigger_model: "" });
    list = await options();
    assert(list[0] === "Assistant default (CLI)", `the empty entry does not fall back to the CLI: ${JSON.stringify(list)}`);
    return "claude-haiku-4-5 offered, the entry reads CLI, then haiku from the chat, then sonnet from the Triggers pick";
  });

  // A default stored on the Models tab, the first tab of the assistant
  // settings and where the bare path lands, is copied onto a new assistant
  // as its own pick: the ring's Chat select stands on it over the empty
  // entry naming the coder's default, and its turn really starts on it,
  // which the fake proves by answering MODEL with the model it was started
  // with. The default is cleared again at the end, so the checks after this
  // one run the fake the way they always did.
  await run("a default stored on the settings tab reaches a new assistant resolved", async () => {
    const chat = 'select[name="chat-claude"]';
    const openSettings = async () => {
      await page.goto(`${BASE}/settings/assistant`, { waitUntil: "domcontentloaded" });
      await dismissUpdate(page);
      assert(new URL(page.url()).pathname === "/settings/assistant/models", `the bare assistant settings do not land on Models: ${page.url()}`);
      await page.waitForSelector(chat, { timeout: 8000 });
    };
    const storeDefault = async (value) => {
      await openSettings();
      await page.selectOption(chat, value);
      await page.click('#settings-assistant-models button[type="submit"]');
      await page.waitForFunction(() => document.body.innerText.includes("Settings saved."), null, { timeout: 8000 });
      const kept = await page.$eval(chat, (s) => s.value);
      assert(kept === value, `the stored default did not come back: ${JSON.stringify(kept)}`);
    };
    await openSettings();
    const empties = await page.evaluate(() => ({
      first: [...document.querySelectorAll("[data-assistant-sections] a")].map((a) => a.textContent.trim())[0],
      chat: document.querySelector('select[name="chat-claude"]').options[0].textContent.trim(),
      check: document.querySelector('select[name="check-claude"]').options[0].textContent.trim(),
      trigger: document.querySelector('select[name="trigger-claude"]').options[0].textContent.trim(),
    }));
    assert(empties.first === "Models", `Models is not the first tab: ${JSON.stringify(empties)}`);
    assert(empties.chat === "Coder default (CLI)" && empties.check === "Same as chat" && empties.trigger === "Same as chat",
      `the Models tab's empty entries are off: ${JSON.stringify(empties)}`);
    await storeDefault("haiku");
    try {
      await openAssistant(page, chatID);
      await newConversation(page);
      const MENU = "dc-assistant [data-assistant-new-label] + .dropdown-menu";
      await page.click(MODEL_MENU);
      await page.waitForSelector(`${MENU}.show`, { timeout: 8000 });
      const chatPick = await page.$eval('dc-assistant [data-assistant-model-pick="chat"] select', (s) => ({ value: s.value, empty: s.options[0].textContent.trim() }));
      assert(chatPick.value === "haiku" && chatPick.empty === "Coder default (CLI)",
        `the stored default did not land on the new assistant's chat pick: ${JSON.stringify(chatPick)}`);
      await page.keyboard.press("Escape");
      await page.waitForSelector(`${MENU}.show`, { state: "detached", timeout: 8000 });
      await send(page, "MODEL which one is it");
      await waitSettled(page);
      const answer = await page.locator('[data-role="assistant"]').last().innerText();
      assert(answer.includes("model: haiku"), `the turn did not run on the stored default: ${answer}`);
    } finally {
      await storeDefault("");
      // The checks after this one start on the run's assistant, where the
      // ring check left the page.
      await openAssistant(page, chatID);
    }
    return "Coder default (CLI) first, haiku stored, the new assistant's ring stands on haiku, the turn ran on it, cleared again";
  });

  // Frames stop in the middle of every real answer: thinking sends nothing at
  // all and a tool run one frame at its start. The page pulls the message when
  // that silence outlasts its watchdog, and the store holds an answer only
  // once the turn settled, so a pull landing mid answer has to leave the
  // bubble alone. It used to put the store's empty bubble on screen instead,
  // which wiped the streamed words until the next frame brought them back. And
  // silence must cost nothing at all: not a rebuilt stream, the ping says it is
  // alive, and not a pull either, a half hour of tool work would be hundreds of
  // requests whose answer is thrown away.
  await run("a silent gap in the middle of an answer leaves the streamed text standing", async () => {
    const opened = [];
    const pulled = [];
    const watch = (request) => {
      if (/\/assistants\/[^/]+\/stream/.test(request.url())) opened.push(request.url());
      if (/\/assistants\/[^/]+\/messages\//.test(request.url())) pulled.push(request.url());
    };
    page.on("request", watch);
    try {
      await send(page, "PAUSE_CHAT hold in the middle");
      const answer = page.locator('[data-role="assistant"]').last();
      await page.waitForFunction(() => {
        const nodes = document.querySelectorAll('[data-role="assistant"]');
        return nodes[nodes.length - 1]?.innerText.includes("before the pause");
      }, null, { timeout: 20000 });
      const connects = opened.length;
      const pulls = pulled.length;

      // The fake says nothing for twelve seconds. A watchdog round lands within
      // six of them at the latest, so the words are watched well past the first
      // two rounds: every one of them has to leave them where they are.
      for (let i = 0; i < 10; i += 1) {
        await sleep(1000);
        const during = await answer.innerText();
        assert(during.includes("before the pause"),
          `the streamed text was wiped after ${i + 1}s of silence: ${during}`);
      }
      assert(opened.length === connects,
        `the silence cost ${opened.length - connects} reconnects of the assistant's stream`);
      assert(pulled.length === pulls,
        `the silence cost ${pulled.length - pulls} pulls of the message`);

      await waitSettled(page, 30000);
      const text = await answer.innerText();
      assert(text.includes("before the pause") && text.includes("and after it"),
        `the finished answer lost a half: ${text}`);
      return text.trim();
    } finally {
      page.off("request", watch);
    }
  });

  // While an answer streams, the bubble carries the prefix the server
  // rendered plus the raw text that arrived after that render, and the seam
  // between the two falls wherever the render landed, mid sentence. The raw
  // text used to be hung behind the rendered markup, where it became a block
  // of its own: a sentence still being typed read as two paragraphs until the
  // finished message replaced it. What stands there mid answer is the
  // beginning of what stands there at the end, break for break.
  await run("the streamed text shows no break the finished answer does not have", async () => {
    await send(page, "STREAM_PARAGRAPH one sentence in many pieces");

    const shots = [];
    const deadline = Date.now() + 30000;
    while (Date.now() < deadline) {
      const shot = await page.evaluate(() => {
        const nodes = document.querySelectorAll('[data-role="assistant"]');
        const bubble = nodes[nodes.length - 1];
        const body = bubble?.querySelector("[data-assistant-text]");
        if (!body) return null;
        const tail = body.querySelector("[data-assistant-tail]");
        return {
          state: bubble.getAttribute("data-state"),
          text: body.innerText.trim(),
          tail: tail ? tail.textContent : "",
        };
      });
      if (shot) shots.push(shot);
      if (shot && shot.state !== "streaming") break;
      await sleep(60);
    }

    await waitSettled(page, 30000);
    const final = (await page.locator('[data-role="assistant"] [data-assistant-text]').last().innerText()).trim();
    assert(final === "The workspace only says where you started, never where you are working now.",
      `the finished answer is not the one sentence: ${JSON.stringify(final)}`);

    const seams = shots.filter((s) => s.state === "streaming" && s.tail.length && s.text.length);
    assert(seams.length >= 3,
      `only ${seams.length} snapshots caught the rendered prefix meeting the raw tail`);
    for (const shot of seams) {
      assert(final.startsWith(shot.text),
        `the streamed text is not the beginning of the finished answer: ${JSON.stringify(shot.text)}`);
    }
    return `${seams.length} snapshots at the seam`;
  });

  // The cross on a chip read a data attribute that no longer exists, so the
  // filter kept every file and the wrong one went along with the message.
  await run("an attached file can be taken back before the message goes", async () => {
    await attach(page, [{ name: "wrong.png", mimeType: "image/png", buffer: PNG }]);
    await page.click("[data-assistant-attachment-remove]");
    await page.waitForFunction(() => document.querySelectorAll("[data-assistant-attachment]").length === 0, null, { timeout: 8000 });
    assert(await page.locator("[data-assistant-attachments]").evaluate((tray) => tray.classList.contains("d-none")),
      "the tray stays open with nothing in it");
    await send(page, "MAGIC and nothing attached");
    await waitSettled(page);
    const bubble = page.locator('[data-role="user"]').last();
    assert(await bubble.locator("img, audio, video").count() === 0, "the file that was taken back went along anyway");
  });

  // The draft belongs to the conversation, not to the browser that typed it:
  // the same words are there after a page change and on the next device, the
  // files that were uploaded for them come along, and sending takes both.
  await run("an unsent message waits in the assistant, on every device", async () => {
    const draft = "a question I have not sent off yet";
    await page.fill("[data-assistant-input]", draft);
    await attach(page, [{ name: "draft.png", mimeType: "image/png", buffer: PNG }]);
    // One path saves the draft, the debounce after the typing stops, so the
    // check waits for that and for nothing else.
    await sleep(1600);
    await openConversation(page, chatID);
    assert((await page.inputValue("[data-assistant-input]")) === draft, "the draft did not survive the page change");
    assert((await page.locator(`[data-assistant-attachment="draft.png"]`).count()) === 1,
      "the draft came back without the file that was attached to it");

    const mp = await mobilePage();
    await openConversation(mp, chatID);
    assert((await mp.inputValue("[data-assistant-input]")) === draft, "the second device does not see the draft");
    assert((await mp.locator(`[data-assistant-attachment="draft.png"]`).count()) === 1,
      "the second device sees the draft without its file");

    await idle(page);
    await page.fill("[data-assistant-input]", "MAGIC and this one goes");
    await page.click("[data-assistant-send]");
    await waitSettled(page);
    await openConversation(page, chatID);
    assert((await page.inputValue("[data-assistant-input]")) === "", "the sent draft is still in the box");
    assert((await page.locator("[data-assistant-attachment]").count()) === 0, "the sent draft still holds its file");
    return "kept across a page change and a device, gone after sending";
  });

  // Two surfaces of the same conversation, both open. The draft rides the
  // shared event stream like everything else that is live in the cockpit: the
  // event names the conversation, the composer pulls the draft itself, and
  // neither page is reloaded here.
  await run("a draft typed on one device reaches the other one live", async () => {
    const mp = await mobilePage();
    await openConversation(mp, chatID);
    await openConversation(page, chatID);

    const typed = "typed on the phone";
    await mp.fill("[data-assistant-input]", typed);
    await page.waitForFunction(
      (text) => document.querySelector("[data-assistant-input]").value === text,
      typed,
      { timeout: 15000 },
    );

    // Sending on one device empties the other one's box the same way.
    await idle(page);
    await page.fill("[data-assistant-input]", "MAGIC carry on");
    await sleep(1400);
    await page.click("[data-assistant-send]");
    await waitSettled(page);
    await mp.waitForFunction(
      () => document.querySelector("[data-assistant-input]").value === "",
      null,
      { timeout: 15000 },
    );
    await closePanel(mp);
    return "typed here, there a moment later";
  });

  // The question travels like the answer does. A sent message is announced on
  // the conversation's stream before its turn opens, so the panel that stands
  // open on the other device puts it above the answer, and the device that
  // wrote it keeps the one bubble it already has instead of a second copy.
  await run("a message sent on one device shows up on the other one live", async () => {
    const mp = await mobilePage();
    await openConversation(mp, chatID);
    await openConversation(page, chatID);

    const typed = "MAGIC sent from the phone";
    await send(mp, typed);
    await page.waitForFunction(
      (needle) => Array.from(document.querySelectorAll('[data-role="user"]'))
        .some((node) => node.innerText.includes(needle)),
      typed,
      { timeout: 20000 },
    );
    await waitSettled(page);
    await waitSettled(mp);

    const read = (target) => target.evaluate((needle) => {
      const nodes = Array.from(document.querySelectorAll("[data-assistant-message]"));
      const mine = nodes.filter((node) => node.getAttribute("data-role") === "user" && node.innerText.includes(needle));
      const answers = nodes.filter((node) => node.getAttribute("data-role") === "assistant");
      const last = answers[answers.length - 1];
      return {
        copies: mine.length,
        above: Boolean(mine.length && last) && nodes.indexOf(mine[0]) < nodes.indexOf(last),
        answered: Boolean(last && last.innerText.includes("AIRPORT")),
      };
    }, typed);

    const there = await read(page);
    assert(there.copies === 1, `the other device shows the message ${there.copies} times`);
    assert(there.above, "the answer stands above the question it answers");
    assert(there.answered, "the other device did not follow the answer");
    const here = await read(mp);
    assert(here.copies === 1, `the sending device shows its own message ${here.copies} times`);

    await closePanel(mp);
    return "sent there, read here, one bubble on both";
  });

  // The cut connection surfaces as CLOSED without an auto-retry (window.stop is
  // an abort, not a network error), so coming back rides the same visibility
  // path a woken phone takes, which reopens the stream and lands the snapshot.
  await run("a message that arrived while the stream was down appears on reconnect", async () => {
    await openAssistant(page, chatID);
    const before = await page.locator("[data-assistant-message]").count();
    await ctx.setOffline(true);
    await page.evaluate(() => window.stop());
    const mp = await mobilePage();
    await openConversation(mp, chatID);
    await send(mp, "MAGIC offline news");
    await waitSettled(mp);
    await closePanel(mp);
    await ctx.setOffline(false);
    await page.evaluate(() => document.dispatchEvent(new Event("visibilitychange")));
    await page.waitForFunction(
      (count) => document.querySelectorAll("[data-assistant-message]").length > count,
      before,
      { timeout: 30000 },
    );
    const texts = await page.locator("[data-assistant-message]").allInnerTexts();
    assert(texts.some((text) => text.includes("offline news")), "the missed message is not in the transcript");
    return "no reload, no toggle, the transcript caught up";
  });

  await run("an image and an audio file attach, embed and serve with a range request", async () => {
    await attach(page, [
      { name: "shot.png", mimeType: "image/png", buffer: PNG },
      { name: "note.wav", mimeType: "audio/wav", buffer: WAV },
    ]);
    await idle(page);
    await page.fill("[data-assistant-input]", "MAGIC look at this");
    await page.click("[data-assistant-send]");
    await waitSettled(page);
    // The bubble the composer wrote is replaced by the server rendered one
    // when the announcement arrives, so the wait is for the files to stand in
    // it, not for the answer beside it to settle.
    await page.waitForFunction(() => {
      const bubbles = document.querySelectorAll('[data-role="user"]');
      const last = bubbles[bubbles.length - 1];
      return last && last.querySelectorAll("[data-assistant-file]").length === 2;
    }, null, { timeout: 15000 });

    const bubble = page.locator('[data-role="user"]').last();
    assert(await bubble.locator("img.dc-assistant-media").count() === 1, "no image in the message");
    assert(await bubble.locator("audio.dc-assistant-audio").count() === 1, "no player in the message");
    assert(await bubble.evaluate((node) => node.hasAttribute("data-no-pe")), "message content is not opted out of pe boosting");

    const url = await bubble.locator("img.dc-assistant-media").getAttribute("src");
    assert(url.includes(`/assistants/${chatID}/media/`), `unexpected media url: ${url}`);
    const ranged = await page.evaluate(async (target) => {
      const res = await fetch(target, { headers: { Range: "bytes=0-9" } });
      return { status: res.status, length: (await res.arrayBuffer()).byteLength };
    }, url);
    assert(ranged.status === 206, `range request not honoured: ${ranged.status}`);
    assert(ranged.length === 10, `range request served ${ranged.length} bytes`);

    const decoded = await page.evaluate((target) => new Promise((resolve) => {
      const image = new Image();
      image.onload = () => resolve(image.naturalWidth);
      image.onerror = () => resolve(0);
      image.src = target;
    }), url);
    assert(decoded === 1, "the browser could not decode the served image");
  });

  await run("a file the assistant writes into its workspace downloads from the link in its answer", async () => {
    await send(page, "FILELINK");
    await waitSettled(page);
    const bubble = page.locator('[data-role="assistant"]').last();
    const link = bubble.locator("a[href*='/media/assistant-files/proof.txt']");
    await link.waitFor({ timeout: 15000 });
    const href = await link.getAttribute("href");
    assert(href.startsWith(`/assistants/${chatID}/media/assistant-files/proof.txt`), `the link points elsewhere: ${href}`);
    const served = await page.evaluate(async (target) => {
      const res = await fetch(target);
      return { status: res.status, body: await res.text(), disposition: res.headers.get("content-disposition") || "" };
    }, href);
    assert(served.status === 200, `the linked file answered ${served.status}`);
    assert(served.body.startsWith("proof from "), `the served file is not the one written: ${served.body}`);
    const workspace = served.body.trim().slice("proof from ".length);
    assert(workspace.endsWith(`/instances/${chatID}/workspace`), `the turn ran outside its own workspace: ${workspace}`);
    if (href.includes("download=1")) {
      assert(/attachment/.test(served.disposition), `a download link served inline: ${served.disposition}`);
    }
    return `served from ${workspace}`;
  });

  await run("a cockpit command run through the workspace wrapper is charged to the assistant", async () => {
    await send(page, "OWNER");
    await waitSettled(page);
    const text = await page.locator('[data-role="assistant"]').last().innerText();
    assert(!text.includes("wrapper failed"), `the wrapper did not run: ${text}`);
    const own = text.split("\n").find((line) => line.trim().startsWith("*") && line.includes(`id ${chatID}`));
    assert(own, `the list does not mark this assistant as the caller:\n${text}`);
    assert(text.includes("* is you"), `the list does not know who is calling:\n${text}`);
    return own.trim();
  });

  await run("a picture wider than the thread clamps instead of widening it", async () => {
    await attach(page, [{ name: "wide.svg", mimeType: "image/svg+xml", buffer: WIDE_SVG }]);
    await idle(page);
    await page.fill("[data-assistant-input]", "MAGIC how wide");
    await page.click("[data-assistant-send]");
    await waitSettled(page);
    await page.waitForFunction(() => {
      const bubbles = document.querySelectorAll('[data-role="user"]');
      const last = bubbles[bubbles.length - 1];
      return last && last.querySelector('[data-assistant-file="wide.svg"]');
    }, null, { timeout: 15000 });
    await page.waitForFunction(() => {
      const img = [...document.querySelectorAll('[data-role="user"] img.dc-assistant-media')].pop();
      return img && img.getBoundingClientRect().width >= 50;
    }, null, { timeout: 10000 });
    const wide = await page.evaluate(() => {
      const img = [...document.querySelectorAll('[data-role="user"] img.dc-assistant-media')].pop();
      const scroller = document.querySelector("[data-assistant-scroll]");
      if (Math.round(img.getBoundingClientRect().width) > scroller.clientWidth) return "the picture is wider than the thread";
      return scroller.scrollWidth > scroller.clientWidth + 1 ? "the transcript scrolls sideways" : "";
    });
    assert(wide === "", `a wide picture breaks the thread: ${wide}`);
  });

  // The composer's file input and the files of a sent message carry the same
  // attribute, and the transcript sits above the composer: an unscoped lookup
  // hands the element a message's link instead of its picker, and the
  // paperclip stays dead for the rest of the conversation.
  await run("the paperclip still picks files after a message carried one", async () => {
    await attach(page, [{ name: "again.png", mimeType: "image/png", buffer: PNG }]);
    assert((await page.locator(`[data-assistant-attachment="again.png"]`).count()) === 1,
      "the picker no longer reaches the composer");
    await page.click("[data-assistant-attachment-remove]");
    await page.waitForFunction(() => document.querySelectorAll("[data-assistant-attachment]").length === 0, null, { timeout: 8000 });
    return "the input, not the message's link";
  });

  // A browser calls every clipboard image image.png, so two screenshots in one
  // message used to be one file: the second upload replaced the first on disk
  // while the tray showed two chips. A taken name counts up on the server, and
  // the chip carries the name the server chose, which the send posts back.
  await run("two files named image.png attach as image.png and image-2.png", async () => {
    await attach(page, [
      { name: "image.png", mimeType: "image/png", buffer: PNG },
      { name: "image.png", mimeType: "image/png", buffer: PNG },
    ]);
    const names = await page.evaluate(() => [...document.querySelectorAll("[data-assistant-attachments] [data-assistant-attachment]")]
      .map((chip) => chip.getAttribute("data-assistant-attachment")));
    assert(names.length === 2 && names.includes("image.png") && names.includes("image-2.png"), `the tray holds ${names.join(", ")}`);
    for (const name of names) {
      await page.click(`[data-assistant-attachment-remove="${name}"]`);
    }
    await page.waitForFunction(() => document.querySelectorAll("[data-assistant-attachment]").length === 0, null, { timeout: 8000 });
  });

  await run("the attachment reaches the coder as a path it can open", async () => {
    const text = await page.locator('[data-role="user"]').last().innerText();
    assert(!text.includes("Attached files:"), "the path note leaked into the transcript");
  });

  await run("markdown renders and model HTML is dropped", async () => {
    await send(page, "MARKDOWN please");
    await waitSettled(page);
    // A settled turn is still the plain text the stream assembled: the server
    // rendered markup arrives with the message fragment right after.
    await page.waitForSelector('[data-assistant-message]:last-child h1', { timeout: 10000 });
    const bubble = page.locator('[data-role="assistant"]').last();
    assert(await bubble.locator("h1").count() === 1, "no heading rendered");
    assert(await bubble.locator("code").count() >= 1, "no code rendered");
    assert(await bubble.locator("img").count() === 0, "raw model HTML survived");

    // The answer carries a command line that is wider than a phone. It has to
    // scroll inside its own bubble, never take the conversation sideways with it.
    const mp = await mobilePage();
    await openConversation(mp, chatID);
    await sleep(400);
    const wide = await mp.evaluate(() => {
      const pre = [...document.querySelectorAll('[data-role="assistant"] pre')].pop();
      if (!pre) return "no code block in the transcript";
      if (pre.scrollWidth <= pre.clientWidth) return "the code block fits, so it proves nothing";
      if (getComputedStyle(pre).overflowX === "visible") return "the code block does not scroll inside its bubble";
      const scroller = document.querySelector("[data-assistant-scroll]");
      return scroller.scrollWidth > scroller.clientWidth + 1 ? "the transcript scrolls sideways" : "";
    });
    assert(wide === "", `a code block wider than the screen breaks the thread: ${wide}`);
    await closePanel(mp);
  });

  await run("a memory is saved and listed in the memory sheet", async () => {
    await openAssistant(page, chatID);
    await openView(page, "memory", "#memory-new");
    await dropRunnerMemories(page);
    if (!(await page.locator("#memory-new.show").count())) {
      await page.click("[data-memory-add]");
      await page.waitForSelector("#memory-new.show", { timeout: 8000 });
    }
    await page.fill("#memory-title", memoryTitle);
    await page.fill("#memory-body", memoryBody);
    await page.click('#memory-new form button[type="submit"]');
    await page.waitForSelector(`[data-memory-entry="${memorySlug}"]`, { timeout: 10000 });
    const shown = await page.locator(`[data-memory-entry="${memorySlug}"]`).innerText();
    assert(shown.includes(memoryTitle), `memory not listed: ${shown}`);
    assert(shown.includes(memoryBody), "memory body not listed");
    return "saved without leaving the page";
  });

  await run("editing a memory in place keeps its file and deleting removes it", async () => {
    const before = await page.locator("[data-memory-entry]").count();
    // The row itself turns into the prefilled fields, no round trip: the
    // reading half steps aside so no word stands twice.
    await page.click(`[data-memory-entry="${memorySlug}"] [data-memory-edit-open]`);
    await page.waitForSelector(`[data-memory-edit="${memorySlug}"]`, { state: "visible", timeout: 8000 });
    assert(
      !(await page.locator(`[data-memory-entry="${memorySlug}"] [data-memory-view]`).isVisible()),
      "the read-only row stayed next to its own fields",
    );
    await page.fill(`[data-memory-edit="${memorySlug}"] textarea[name="body"]`, "Edited by the runner.");
    await page.click(`[data-memory-edit="${memorySlug}"] button[type="submit"]`);
    await page.waitForSelector(`[data-memory-entry="${memorySlug}"]`, { timeout: 10000 });
    await page.waitForFunction(
      (slug) => document.querySelector(`[data-memory-entry="${slug}"]`)?.innerText.includes("Edited by the runner."),
      memorySlug,
      { timeout: 10000 },
    );
    assert((await page.locator("[data-memory-entry]").count()) === before, "editing created a second memory");

    await page.click(`[data-memory-entry="${memorySlug}"] form[data-confirm] button[type="submit"]`);
    await L.confirmSwal(page);
    await page.waitForSelector(`[data-memory-entry="${memorySlug}"]`, { state: "detached", timeout: 10000 });
  });

  // The rule the whole feature turns on: a new assistant is another one, not a
  // replacement. Nothing is reused, nothing is archived, and the one that was
  // there keeps its thread and its composer.
  await run("a new assistant starts empty and leaves the one before it alone", async () => {
    await openAssistant(page, chatID);
    freshID = await newConversation(page);
    assert(freshID !== chatID, "new assistant did not open");
    assert(await page.locator("[data-assistant-empty]").isVisible(), "the new assistant is not empty");

    const third = await newConversation(page);
    assert(third !== freshID, "pressing New twice reused an assistant instead of making one");

    await openView(page, "history", "[data-assistant-rows] [data-assistant-instance]");
    const rows = await page.locator("[data-assistant-rows] [data-assistant-instance]").count();
    assert(rows >= 3, `the column holds ${rows} assistants, expected at least 3`);
    assert(await page.locator("[data-assistant-rows] [data-assistant-instance].active").count() === 1,
      "the column does not mark the open assistant");

    // The first one is still live: it takes a message, which is what the
    // single live conversation used to refuse.
    await openConversation(page, chatID);
    assert((await page.locator("dc-assistant [data-assistant-input]").count()) === 1,
      "an assistant lost its composer when another one was started");
    const status = await page.evaluate(async (id) => {
      const token = document.querySelector('meta[name="csrf-token"]')?.content || "";
      const res = await fetch(`/assistants/${id}`, {
        method: "POST",
        headers: { "Content-Type": "application/x-www-form-urlencoded", Accept: "application/json", "X-CSRF-Token": token },
        body: "form=draft&message=still+mine",
      });
      return res.status;
    }, chatID);
    assert(status === 200, `an assistant refused a draft after another one was started: ${status}`);

    // Every one of them has a stream of its own, and the area's own address
    // comes back to the one that was open last, which is this one.
    await openConversation(page, third);
    assert((await page.locator("dc-assistant").getAttribute("stream-url")) === `/assistants/${third}/stream`,
      "an assistant does not carry its own stream");
    await page.goto(`${BASE}/assistants`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    await page.waitForSelector(READY, { timeout: 15000 });
    assert(new URL(page.url()).pathname === `/assistants/${third}`,
      `the bare address landed on ${page.url()} instead of the one opened last`);
    assert((await page.locator(`[data-assistant-rows] [data-assistant-instance="${third}"].active`).count()) === 1,
      "the list does not mark the assistant the area opened");
    chatID = third;
  });

  // The list is the user's to sort: a row is dragged into place, the order goes
  // to the server, and it is still there after a reload. Nothing about when an
  // assistant last said something moves anybody.
  await run("the list keeps the order the rows were dragged into", async () => {
    await openAssistant(page, chatID);
    await openView(page, "history", "[data-assistant-rows] [data-assistant-instance]");
    const ids = () => page.locator("[data-assistant-rows] [data-assistant-instance]")
      .evaluateAll((rows) => rows.map((row) => row.dataset.assistantInstance));
    const before = await ids();
    assert(before.length >= 3, `the column holds ${before.length} assistants, expected at least 3`);

    // The first row onto the third one's place, with a real pointer: the drag
    // starts past the threshold and the drop is where the pointer lets go.
    const rows = page.locator("[data-assistant-rows] [data-assistant-instance]");
    const from = await rows.first().boundingBox();
    const to = await rows.nth(2).boundingBox();
    await page.mouse.move(from.x + from.width / 2, from.y + from.height / 2);
    await page.mouse.down();
    await page.mouse.move(from.x + from.width / 2, from.y + from.height / 2 + 20, { steps: 4 });
    await page.mouse.move(to.x + to.width / 2, to.y + to.height / 2 + 4, { steps: 12 });
    const [saved] = await Promise.all([
      page.waitForResponse((r) => r.url().includes("/assistants/order") && r.request().method() === "POST", { timeout: 15000 }),
      page.mouse.up(),
    ]);
    assert(saved.status() === 204, `the order answered ${saved.status()}`);

    const want = [before[1], before[2], before[0], ...before.slice(3)];
    await page.waitForFunction(
      (order) => [...document.querySelectorAll("[data-assistant-rows] [data-assistant-instance]")]
        .map((row) => row.dataset.assistantInstance).join(" ") === order.join(" "),
      want,
      { timeout: 15000 },
    );

    // And it is the server's now, so a fresh page reads the same order.
    await page.goto(`${BASE}/assistants`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    await page.waitForSelector("[data-assistant-rows] [data-assistant-instance]", { timeout: 15000 });
    const after = await ids();
    assert(after.join(" ") === want.join(" "),
      `the order did not survive the reload: ${after.join(" ")} instead of ${want.join(" ")}`);
    // This check ends on the list, and the checks after it expect a thread.
    await openConversation(page, chatID);
    return `dragged to ${want.slice(0, 3).map((id) => id.slice(0, 4)).join(" ")}`;
  });

  // Ctrl+Tab walks the assistants the way it walks the terminals: the order the
  // list stands in, and it wraps at both ends. The cursor sits in the composer
  // when an assistant opens, so this also proves the key is caught there.
  await run("Ctrl+Tab steps to the next assistant and wraps at the end", async () => {
    await openAssistant(page, chatID);
    await openView(page, "history", "[data-assistant-rows] [data-assistant-instance]");
    const ids = await page.locator(".dc-app > .dc-ctx[data-assistant-rows] [data-assistant-instance]")
      .evaluateAll((rows) => rows.map((row) => row.dataset.assistantInstance));
    assert(ids.length >= 2, `the column holds ${ids.length} assistants, expected at least 2`);
    const at = ids.indexOf(chatID);
    assert(at !== -1, "the open assistant has no row in the column");

    await page.keyboard.press("Control+Tab");
    await page.waitForURL(new RegExp(`/assistants/${ids[(at + 1) % ids.length]}$`), { timeout: 10000 });
    await page.waitForSelector(READY, { timeout: 15000 });
    await page.keyboard.press("Control+Shift+Tab");
    await page.waitForURL(new RegExp(`/assistants/${chatID}$`), { timeout: 10000 });
    await page.waitForSelector(READY, { timeout: 15000 });

    // The wrap: forward from the last row is the first one.
    await openConversation(page, ids[ids.length - 1]);
    await page.keyboard.press("Control+Tab");
    await page.waitForURL(new RegExp(`/assistants/${ids[0]}$`), { timeout: 10000 });
    await page.waitForSelector(READY, { timeout: 15000 });

    await openConversation(page, chatID);
    return `stepped both ways over ${ids.length} rows and wrapped`;
  });

  // A name is how several of them are told apart, so the list column renames
  // in place and the page's head follows.
  await run("an assistant is renamed from its row", async () => {
    await openView(page, "history", "[data-assistant-rows] [data-assistant-instance]");
    const id = await page.locator("dc-assistant").getAttribute("assistant-id");
    await page.click(`[data-assistant-rows] [data-assistant-instance="${id}"] [data-assistant-menu]`);
    await page.waitForSelector(".dc-context-menu", { timeout: 8000 });
    await page.click('.dc-context-menu .dropdown-item:has-text("Rename")');
    await page.waitForSelector(".swal2-input", { timeout: 8000 });
    await page.fill(".swal2-input", "zztc renamed");
    await page.click(".swal2-confirm");
    await page.waitForFunction(
      (target) => document.querySelector(`[data-assistant-instance="${target}"]`)?.dataset.assistantName === "zztc renamed",
      id,
      { timeout: 10000 },
    );
    await openConversation(page, id);
    assert((await page.locator("[data-assistant-title]").innerText()).includes("zztc renamed"),
      "the page head does not carry the new name");
  });

  await run("stopping a running answer keeps the part that arrived", async () => {
    await send(page, "SLOW please");
    await page.waitForSelector("[data-assistant-cancel]:not(.d-none)", { timeout: 10000 });
    await page.waitForFunction(() => {
      const last = document.querySelectorAll('[data-role="assistant"]');
      return last.length && last[last.length - 1].innerText.includes("still working");
    }, null, { timeout: 15000 });
    await page.click("[data-assistant-cancel]");
    await waitSettled(page);
    const bubble = page.locator('[data-role="assistant"]').last();
    assert((await bubble.getAttribute("data-state")) === "cancelled", "not cancelled");
    assert((await bubble.innerText()).includes("still working"), "partial answer lost");
  });

  // Working is not a fault. While the turn runs the row carries the ring
  // around the assistant's round icon and no badge; the Unfinished badge
  // belongs to a turn that stopped before it was done, and both swap on the
  // assistant event, without a reload.
  await run("the list runs the ring while a turn runs and badges only a stopped one", async () => {
    await openView(page, "history", "[data-assistant-rows] [data-assistant-instance]");
    const id = await page.locator("dc-assistant").getAttribute("assistant-id");
    const row = `[data-assistant-rows] [data-assistant-instance="${id}"]`;
    const badged = (sel) => {
      const el = document.querySelector(sel);
      return !!el && [...el.querySelectorAll(".badge")].some((b) => b.textContent.trim() === "Unfinished");
    };

    await send(page, "SLOW and let the row say so");
    await page.waitForSelector(`${row} .dc-term-icon.assistant.working`, { timeout: 15000 });
    assert(!(await page.evaluate(badged, row)), "a running turn is shown as unfinished");

    await page.click("[data-assistant-cancel]");
    await waitSettled(page);
    await page.waitForFunction(
      (sel) => {
        const el = document.querySelector(sel);
        if (!el || el.querySelector(".dc-term-icon.assistant.working")) return false;
        return [...el.querySelectorAll(".badge")].some((b) => b.textContent.trim() === "Unfinished");
      },
      row,
      { timeout: 15000 },
    );
  });

  // The head's sparkle is the phone's, in every state: the desktop head stays
  // the title alone, a running turn never makes an icon appear there, and on
  // the phone the same icon wears the ring while the turn runs and loses it
  // when the turn ends, without a reload.
  await run("the assistant head runs the ring on the phone and stays bare on the desktop", async () => {
    await openAssistant(page, chatID);
    const mp = await mobilePage();
    await openConversation(mp, chatID);
    const head = "dc-assistant [data-assistant-head-icon]";
    const ringing = (sel) => !!document.querySelector(sel)?.classList.contains("working");
    assert(!(await page.locator(head).isVisible()), "the desktop head carries the icon while nothing runs");
    assert(await mp.locator(head).isVisible(), "the phone head has no icon");

    await send(page, "SLOW and hold the head");
    await mp.waitForFunction(ringing, head, { timeout: 15000 });
    assert(await mp.locator(head).isVisible(), "the phone head icon went away while the turn runs");
    await page.waitForFunction(ringing, head, { timeout: 15000 });
    assert(!(await page.locator(head).isVisible()), "the desktop head shows an icon while the turn runs");

    await page.click("[data-assistant-cancel]");
    await waitSettled(page);
    await mp.waitForFunction((sel) => !document.querySelector(sel)?.classList.contains("working"), head, { timeout: 15000 });
    assert(await mp.locator(head).isVisible(), "the phone head icon went away after the turn");
    assert(!(await page.locator(head).isVisible()), "the desktop head kept an icon after the turn");
    await closePanel(mp);
    return "ring on the phone while it runs, desktop head bare throughout";
  });

  // One turn per assistant, and the queue is that assistant's: a second prompt
  // into one that is answering waits in the transcript, can be taken back while
  // it waits, and goes out as one turn when the running one ends. The composer
  // never locks for it.
  await run("a second message while an answer runs waits and goes out afterwards", async () => {
    await send(page, "SLOW again please");
    await page.waitForSelector("[data-assistant-cancel]:not(.d-none)", { timeout: 10000 });
    const composer = await page.locator("[data-assistant-input]").evaluate((el) => ({ readOnly: el.readOnly, disabled: el.disabled }));
    assert(!composer.readOnly && !composer.disabled, "the composer locked during a turn");
    assert(await page.locator("[data-assistant-send]").isVisible(), "the send button is gone during a turn");

    // One that is taken back again, and one that stays.
    await page.fill("[data-assistant-input]", "this one is taken back");
    await page.click("[data-assistant-send]");
    await page.waitForSelector('[data-assistant-message][data-state="queued"]', { timeout: 15000 });
    assert(await page.locator("[data-assistant-queued]").first().isVisible(),
      "a waiting message wears no badge");
    await page.click("[data-assistant-discard]");
    await page.waitForFunction(
      () => document.querySelectorAll('[data-assistant-message][data-state="queued"]').length === 0,
      null,
      { timeout: 15000 },
    );

    await page.fill("[data-assistant-input]", "MAGIC this one waits");
    await page.click("[data-assistant-send]");
    await page.waitForSelector('[data-assistant-message][data-state="queued"]', { timeout: 15000 });
    assert((await page.inputValue("[data-assistant-input]")) === "",
      "the composer kept the words of a message that went into the queue");

    // Stopping the running turn is what lets the queue go.
    await page.click("[data-assistant-cancel]");
    await waitSettled(page);
    // The flush is a turn of its own: the waiting entry stops saying so when it
    // starts, and the answer arrives after that.
    await page.waitForFunction(
      () => document.querySelectorAll('[data-assistant-message][data-state="queued"]').length === 0,
      null,
      { timeout: 30000 },
    );
    // The answer comes after that, so this waits for the words and not for a
    // settled last bubble: right after the flush started, the last thing in the
    // transcript is the flushed question and it is settled already.
    await page.waitForFunction(
      () => [...document.querySelectorAll('[data-role="assistant"]')].some((m) => m.innerText.includes("AIRPORT")),
      null,
      { timeout: 60000 },
    ).catch(() => {});
    const texts = await page.locator('[data-role="assistant"]').allInnerTexts();
    assert(texts.some((t) => t.includes("AIRPORT")), "the waiting message was never answered");
    const asked = await page.locator('[data-role="user"]').allInnerTexts();
    assert(!asked.some((t) => t.includes("taken back")), "the message that was taken back went out anyway");
    return "one waited, one was taken back, the composer stayed open";
  });

  // A waiting assistant blocks nobody: another one answers at the same time.
  await run("a message waiting in one assistant does not hold up another", async () => {
    await openConversation(page, chatID);
    const other = await page.evaluate(async (mine) => {
      const res = await fetch("/assistants/instances", { headers: { Accept: "application/json" } });
      const data = await res.json();
      return (data.assistants || []).map((a) => a.id).find((id) => id !== mine) || "";
    }, chatID);
    assert(other, "there is no second assistant to answer at the same time");
    await send(page, "SLOW once more");
    await page.waitForSelector("[data-assistant-cancel]:not(.d-none)", { timeout: 10000 });
    await page.fill("[data-assistant-input]", "waiting behind it");
    await page.click("[data-assistant-send]");
    await page.waitForSelector('[data-assistant-message][data-state="queued"]', { timeout: 15000 });

    await openConversation(page, other);
    await send(page, "MAGIC answer me now");
    await waitSettled(page);
    const text = await page.locator('[data-role="assistant"]').last().innerText();
    assert(text.includes("AIRPORT"), `the other assistant did not answer: ${text}`);

    await openConversation(page, chatID);
    await page.click("[data-assistant-cancel]");
    await waitSettled(page);
    await page.waitForFunction(
      () => document.querySelectorAll('[data-assistant-message][data-state="queued"]').length === 0,
      null,
      { timeout: 30000 },
    );
    return "the other one answered while the first still held a message";
  });

  await run("a failed turn offers a retry that recovers", async () => {
    await send(page, "FAIL please");
    await waitSettled(page);
    const bubble = page.locator('[data-role="assistant"]').last();
    assert((await bubble.getAttribute("data-state")) === "failed", "turn did not fail");
    await clickAndWait(page, "[data-assistant-retry]");
    const retried = page.locator('[data-role="assistant"]').last();
    assert((await retried.innerText()).includes("Recovered"), "retry did not recover");
  });

  await run("an assistant's provider session is never listed as a resumable coder", async () => {
    await closePanel(page);
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    const body = await page.locator("body").innerText();
    assert(!body.includes(chatID), "an assistant's session showed up as a coder");
  });

  // On a desktop the assistant docks next to the page instead of leaving it:
  // the rail's assistant entry opens it (the floating sheet has no place
  // on a wide screen), the shell shifts aside, the surface loads the live
  // conversation, a navigation keeps the panel (it lives outside the swapped
  // region), and Escape closes it.
  await run("on a desktop the assistant is a page with the list column and the aside", async () => {
    // The entry answers with the one looked at last, so this run's own is
    // opened first and is what the rail has to land on.
    await openConversation(page, chatID);
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    const railEntry = '.dc-rail .dc-rail-btn[href="/assistants"]';
    assert(await page.locator(railEntry).count() === 1, "the assistant is missing from the rail");
    assert((await page.locator(railEntry).innerText()).trim() === "Assistants",
      "the rail entry is not in the plural");
    assert(await page.locator("dc-ctx-sheet").isHidden(), "the sheet stands open on a desktop");
    // The entry leads into the assistant that was open last, with the list
    // standing beside it, the way the terminals entry leads into a terminal.
    await page.click(railEntry);
    await page.waitForSelector(READY, { timeout: 15000 });
    assert(new URL(page.url()).pathname === `/assistants/${chatID}`,
      `the rail entry landed on ${page.url()} instead of the one open last`);
    // And a row of that list opens its own, the column staying where it is.
    const other = await page.locator("[data-assistant-rows] [data-assistant-instance]:not(.active)").first()
      .getAttribute("data-assistant-instance");
    assert(other, "the column offers no second assistant");
    await afterSwap(page, () => page.locator(`[data-assistant-rows] [data-assistant-instance="${other}"] a[href]`).click());
    await page.waitForSelector(READY, { timeout: 15000 });
    assert(new URL(page.url()).pathname === `/assistants/${other}`, `the row landed on ${page.url()}`);
    await openConversation(page, chatID);
    const shape = await page.evaluate(() => {
      const column = document.querySelector(".dc-app > .dc-ctx[data-assistant-rows]");
      const aside = document.getElementById("assistant-aside");
      const work = document.querySelector("dc-assistant.dc-work");
      return {
        railActive: Boolean(document.querySelector('.dc-rail .dc-rail-btn.active[href="/assistants"]')),
        column: Boolean(column) && column.offsetParent !== null,
        activeRow: Boolean(column?.querySelector("[data-assistant-instance].active")),
        aside: aside.offsetParent !== null && getComputedStyle(aside).position === "static",
        composer: Boolean(work?.querySelector("[data-assistant-input]")),
        overflow: document.documentElement.scrollWidth > window.innerWidth,
      };
    });
    assert(shape.railActive, "the rail does not mark the assistant");
    assert(shape.column && shape.activeRow, `the list column is missing or unmarked: ${JSON.stringify(shape)}`);
    assert(shape.aside, "the aside does not stand beside the thread on a wide window");
    assert(shape.composer && !shape.overflow, `the page is not whole: ${JSON.stringify(shape)}`);
    return "rail, column, thread, aside";
  });

  // The aside's head is the sheet's way out and nothing more: beside the
  // thread from xl up the aside stands under the page's own head, there is
  // nothing to close, and the list starts where the aside starts. The width
  // decides, the element is the same one in both sizes, so a window that grows
  // past xl with the sheet closed loses the head without a reload.
  await run("the aside's head shows only as a sheet, the inline aside starts with its list", async () => {
    await openAssistant(page, chatID);
    const shape = (p) => p.evaluate(() => {
      const aside = document.getElementById("assistant-aside");
      const head = aside.querySelector(".dc-ctx-head");
      const body = aside.querySelector(".offcanvas-body");
      const workHead = document.querySelector("dc-assistant > .dc-work-head");
      const a = aside.getBoundingClientRect();
      return {
        width: window.innerWidth,
        inline: getComputedStyle(aside).position === "static",
        headShown: head.getBoundingClientRect().height > 0 && getComputedStyle(head).display !== "none",
        title: head.querySelector(".dc-ctx-title")?.textContent.trim(),
        closeShown: head.querySelector(".btn-close")?.getClientRects().length > 0,
        bodyAtTop: Math.round(body.getBoundingClientRect().top - a.top),
        underWorkHead: Math.round(a.top - workHead.getBoundingClientRect().bottom),
      };
    });
    const wide = await shape(page);
    assert(wide.inline, `the aside is not inline at ${wide.width}px: ${JSON.stringify(wide)}`);
    assert(!wide.headShown && !wide.closeShown, `the inline aside wears a head: ${JSON.stringify(wide)}`);
    assert(wide.bodyAtTop === 0, `the inline aside's list does not start at its top: ${JSON.stringify(wide)}`);
    assert(wide.underWorkHead === 0, `the inline aside does not start right under the page's head: ${JSON.stringify(wide)}`);
    // Below xl on a fine pointer too: the head is there, with the way out.
    const narrow = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1100, height: 800 } });
    let sheet = null;
    let grown = null;
    try {
      const np = await narrow.newPage();
      await L.login(np);
      await openConversation(np, chatID);
      assert(!(await np.locator("#assistant-aside").isVisible()), "the aside stands open below xl before its button is pressed");
      await np.click("[data-assistant-watching-button]");
      await np.waitForSelector("#assistant-aside.show", { timeout: 8000 });
      await sleep(500);
      sheet = await shape(np);
      assert(!sheet.inline && sheet.headShown && sheet.closeShown, `the sheet has no head to close it by: ${JSON.stringify(sheet)}`);
      assert(sheet.title === "Watching", `the sheet's head reads ${sheet.title}`);
      await np.click("#assistant-aside .dc-ctx-head .btn-close");
      await np.waitForSelector("#assistant-aside.show", { state: "detached", timeout: 8000 });
      await np.waitForSelector(".offcanvas-backdrop", { state: "detached", timeout: 8000 });
      await np.setViewportSize({ width: 1360, height: 800 });
      await sleep(500);
      grown = await shape(np);
      assert(grown.inline && !grown.headShown && grown.bodyAtTop === 0, `a window grown past xl keeps the head: ${JSON.stringify(grown)}`);
    } finally {
      await narrow.close();
    }
    return `no head inline at ${wide.width}px, a head on the sheet at ${sheet.width}px, gone again at ${grown.width}px`;
  });

  // Below lg the tab bar's sparkle opens the conversations as a sheet, the
  // way the other areas open theirs, and a row opens the conversation's page.
  // Opening an assistant puts the cursor in the box. On a desktop always, by
  // the mouse and by the keyboard alike, and without pulling the transcript
  // off its end; on a phone never, where it would raise the keyboard over half
  // the screen on every tap.
  await run("opening an assistant puts the cursor in the box on a desktop", async () => {
    await openAssistant(page, chatID);
    await openView(page, "history", "[data-assistant-rows] [data-assistant-instance]");
    const rows = page.locator("[data-assistant-rows] [data-assistant-instance]:not(.active) a[href]");
    assert(await rows.count(), "no other assistant to switch to");

    // By the mouse. The wait is on the address and then on the surface of the
    // assistant it names: the page being left already carries [ready], so a
    // bare wait for it would measure the old one.
    const first = await rows.first().getAttribute("href");
    await rows.first().click();
    await page.waitForURL(new RegExp(`${first}$`), { timeout: 15000 });
    await page.waitForSelector(`dc-assistant[ready][assistant-id="${first.split("/").pop()}"]`, { timeout: 15000 });
    await page.waitForFunction(
      () => document.activeElement?.hasAttribute("data-assistant-input"),
      null,
      { timeout: 5000 },
    ).catch(() => {});
    assert(await page.evaluate(() => document.activeElement?.hasAttribute("data-assistant-input")),
      "a mouse switch left the cursor outside the box");
    // And the transcript kept its place at the end, the focus pulled nothing.
    const atEnd = await page.evaluate(() => {
      const box = document.querySelector("[data-assistant-scroll]");
      return !box || box.scrollHeight - box.clientHeight - box.scrollTop < 40;
    });
    assert(atEnd, "taking the focus moved the transcript off its end");

    // By the keyboard: the row's link is focused and opened with Enter, the
    // way somebody walking the page reaches it.
    await openView(page, "history", "[data-assistant-rows] [data-assistant-instance]");
    const next = page.locator("[data-assistant-rows] [data-assistant-instance]:not(.active) a[href]").first();
    const target = await next.getAttribute("href");
    await next.focus();
    await page.keyboard.press("Enter");
    await page.waitForURL(new RegExp(`${target}$`), { timeout: 15000 });
    await page.waitForSelector(`dc-assistant[ready][assistant-id="${target.split("/").pop()}"]`, { timeout: 15000 });
    await page.waitForFunction(
      () => document.activeElement?.hasAttribute("data-assistant-input"),
      null,
      { timeout: 5000 },
    ).catch(() => {});
    assert(await page.evaluate(() => document.activeElement?.hasAttribute("data-assistant-input")),
      "a keyboard switch left the cursor outside the box");
    return "cursor in the box, transcript untouched";
  });

  await run("on a phone the box is not focused and no keyboard rises", async () => {
    const mp = await mobilePage();
    await openConversation(mp, chatID);
    assert(!(await mp.evaluate(() => document.activeElement?.hasAttribute("data-assistant-input"))),
      "the phone focused the box and would raise the keyboard on every open");
    await closePanel(mp);
    return "no focus on a coarse pointer";
  });

  await run("on a phone the tab bar opens the assistants sheet and a row opens the page", async () => {
    const mp = await mobilePage();
    await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(mp);
    await mp.tap('.dc-tabbar button[data-ctx-area="assistants"]');
    await mp.waitForSelector("dc-ctx-sheet:not([hidden]) [data-assistant-rows] [data-assistant-instance]", { timeout: 8000 });
    assert(mp.url().endsWith("/projects"), "the sparkle button left the page");
    assert(await mp.locator("dc-ctx-sheet [data-assistant-new]").count() >= 1, "the sheet offers no new assistant");
    await mp.tap(`dc-ctx-sheet [data-assistant-instance="${chatID}"] a[href]`);
    await mp.waitForURL(new RegExp(`/assistants/${chatID}$`), { timeout: 10000 });
    await mp.waitForSelector(READY, { timeout: 15000 });
    const fit = await mp.evaluate(() => ({
      sheetHidden: document.querySelector("dc-ctx-sheet").hidden,
      tabActive: Boolean(document.querySelector('.dc-tabbar button[data-ctx-area="assistants"].active')),
      overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
      composerBottom: Math.round(document.querySelector("[data-assistant-form]").getBoundingClientRect().bottom),
      tabbarTop: Math.round(document.querySelector(".dc-tabbar").getBoundingClientRect().top),
    }));
    assert(fit.sheetHidden && fit.tabActive, `the sheet or the tab bar is off: ${JSON.stringify(fit)}`);
    assert(fit.overflow <= 0, `horizontal overflow of ${fit.overflow}px on a phone`);
    assert(fit.composerBottom <= fit.tabbarTop + 1, `the composer sits under the tab bar: ${JSON.stringify(fit)}`);
    // The sheet is the same list the desktop column is, so opened over an
    // assistant it marks that assistant's row, the way the terminals sheet
    // marks the terminal on screen.
    await mp.tap('.dc-tabbar button[data-ctx-area="assistants"]');
    await mp.waitForSelector("dc-ctx-sheet:not([hidden]) [data-assistant-rows] [data-assistant-instance]", { state: "attached", timeout: 8000 });
    const marked = await mp.$$eval("dc-ctx-sheet [data-assistant-instance].active", (els) => els.map((el) => el.dataset.assistantInstance));
    assert(marked.length === 1 && marked[0] === chatID, `the sheet marks ${JSON.stringify(marked)} instead of the open assistant`);
    await mp.tap('.dc-tabbar button[data-ctx-area="assistants"]');
    await mp.waitForSelector("dc-ctx-sheet[hidden]", { state: "attached", timeout: 4000 });
    await closePanel(mp);
    return "sheet from the tab bar, page from the row, the row marked";
  });

  // The row of the assistant on screen stands in the middle of the list when
  // the list comes up, in the desktop column and in the phone's sheet alike,
  // the way the terminals column centers its active tab: with more assistants
  // than the column shows, the open one would otherwise stand below its edge.
  // A swap of the rows from the fragment leaves the row where it is. The
  // window is short so the list scrolls whatever this run has made by now.
  await run("the list column centers the open assistant's row, on a load, after a click, in the sheet, through a rerender", async () => {
    await openAssistant(page, chatID);
    const rowsSel = "[data-assistant-rows] [data-assistant-instance]";
    while ((await page.locator(rowsSel).count()) < 7) await newConversation(page);
    const order = await page.$$eval(rowsSel, (els) => els.map((el) => el.dataset.assistantInstance));
    const middle = order[Math.floor(order.length / 2)];
    const column = ".dc-app > .dc-ctx[data-assistant-rows]";
    // Where the marked row stands against the middle of its scroller.
    const place = (p, scope) => p.evaluate((sel) => {
      const row = document.querySelector(`${sel} [data-assistant-instance].active`);
      const body = row?.closest(".dc-ctx-body");
      if (!row || !body) return null;
      const r = row.getBoundingClientRect();
      const b = body.getBoundingClientRect();
      return { off: Math.round((r.top + r.height / 2) - (b.top + b.height / 2)), scroll: Math.round(body.scrollTop), max: body.scrollHeight - body.clientHeight };
    }, scope);
    const short = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1360, height: 300 } });
    let clicked = null;
    let swapped = null;
    let loaded = null;
    try {
      const sp = await short.newPage();
      await L.login(sp);
      await openConversation(sp, order[1]);
      await afterSwap(sp, () => sp.locator(`${column} [data-assistant-instance="${middle}"] a[href]`).click());
      await sleep(700);
      clicked = await place(sp, column);
      assert(clicked && clicked.max > 40, `the column does not scroll in a short window: ${JSON.stringify(clicked)}`);
      assert(Math.abs(clicked.off) <= 8, `the row is not centered after a row click: ${JSON.stringify(clicked)}`);
      await sp.evaluate((sel) => document.querySelector(sel).refresh(), column);
      await sleep(300);
      swapped = await place(sp, column);
      assert(Math.abs(swapped.off) <= 8, `the row moved when the list was rendered again: ${JSON.stringify(swapped)}`);
      await openConversation(sp, middle);
      await sleep(700);
      loaded = await place(sp, column);
      assert(Math.abs(loaded.off) <= 8, `the row is not centered on a plain load: ${JSON.stringify(loaded)}`);
    } finally {
      await short.close();
    }
    // The phone builds the same column when the sheet opens, so the row is
    // centered once the list is on screen, and a swap keeps it there.
    const phone = await browser.newContext({ ignoreHTTPSErrors: true, hasTouch: true, isMobile: true, viewport: { width: 390, height: 600 } });
    let sheet = null;
    let sheetSwapped = null;
    try {
      const pp = await phone.newPage();
      await L.login(pp);
      await openConversation(pp, middle);
      await pp.tap('.dc-tabbar button[data-ctx-area="assistants"]');
      await pp.waitForSelector("dc-ctx-sheet:not([hidden]) [data-assistant-rows] [data-assistant-instance].active", { state: "attached", timeout: 8000 });
      await sleep(700);
      sheet = await place(pp, "dc-ctx-sheet");
      assert(sheet && sheet.max > 40, `the sheet's list does not scroll: ${JSON.stringify(sheet)}`);
      assert(Math.abs(sheet.off) <= 8, `the row is not centered in the sheet: ${JSON.stringify(sheet)}`);
      await pp.evaluate(() => document.querySelector("dc-ctx-sheet dc-assistant-list").refresh());
      await sleep(300);
      sheetSwapped = await place(pp, "dc-ctx-sheet");
      assert(Math.abs(sheetSwapped.off) <= 8, `the row moved in the sheet when the list was rendered again: ${JSON.stringify(sheetSwapped)}`);
    } finally {
      await phone.close();
    }
    // The one looked at last is this run's own again, in a second of its own.
    await sleep(1100);
    await openConversation(page, chatID);
    return `off by ${clicked.off}/${swapped.off}/${loaded.off}px in the column, ${sheet.off}/${sheetSwapped.off}px in the sheet`;
  });

  // The list column carries the phone's filter, the same ctx_filter.gohtml row
  // over the same kind of list the terminals and the projects have: it hides
  // rows as one types, and the query is the area's own and outlives the sheet.
  await run("the assistants sheet filters its rows and remembers the query", async () => {
    const mp = await mobilePage();
    await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(mp);
    // Attached, not visible: a remembered query hides rows, and the sheet that
    // opens with one would never show the row this waits for.
    const openSheet = async () => {
      await mp.tap('.dc-tabbar button[data-ctx-area="assistants"]');
      await mp.waitForSelector("dc-ctx-sheet:not([hidden]) [data-assistant-rows] [data-assistant-instance]", { state: "attached", timeout: 8000 });
      await sleep(300);
    };
    const closeSheet = async () => {
      await mp.tap('.dc-tabbar button[data-ctx-area="assistants"]');
      await mp.waitForSelector("dc-ctx-sheet[hidden]", { state: "attached", timeout: 4000 });
    };
    const hidden = (id) => mp.$eval(`dc-ctx-sheet [data-assistant-instance="${id}"]`, (el) => el.classList.contains("d-none"));
    await openSheet();
    // The query comes out of the list itself: a word that stands in one row
    // and in no other, whatever this run has named its assistants by then.
    const rows = await mp.$$eval("dc-ctx-sheet [data-assistant-instance]", (els) =>
      els.map((el) => ({ id: el.dataset.assistantInstance, text: (el.textContent || "").toLowerCase() })));
    assert(rows.length >= 2, `the column holds ${rows.length} assistants, expected at least 2`);
    const counts = new Map();
    for (const row of rows) {
      for (const word of new Set(row.text.match(/[a-z]{4,}/g) || [])) counts.set(word, (counts.get(word) || 0) + 1);
    }
    const query = [...counts.entries()].find(([, seen]) => seen === 1)?.[0];
    assert(query, `no word tells the rows apart: ${JSON.stringify(rows.map((row) => row.text))}`);
    const mine = rows.find((row) => row.text.includes(query));
    const other = rows.find((row) => row.id !== mine.id);

    await mp.fill("dc-ctx-sheet [data-ctx-filter]", query);
    await sleep(200);
    assert(!(await hidden(mine.id)), `the filter hid the row '${query}' names`);
    assert(await hidden(other.id), "the filter left an unmatched row standing");
    assert(await mp.evaluate(() => localStorage.getItem("dc-ctx-filter:assistants")) === query,
      "the area does not remember what was typed into its filter");

    await closeSheet();
    await openSheet();
    assert(await hidden(other.id), "the remembered query does not hide the unmatched row on open");
    await mp.click("dc-ctx-sheet [data-ctx-filter-clear]");
    await sleep(200);
    assert(!(await hidden(other.id)), "clearing the filter leaves the row hidden");
    await closeSheet();
    return `filtered by '${query}', remembered, cleared`;
  });

  // The same drag on a phone: a finger starts one only on a row's grip, the way
  // the tab strip splits scrolling from sorting, and it is the same gesture
  // (@dc/rowdrag) the mouse uses on the desktop column.
  await run("a finger sorts the list by a row's grip", async () => {
    const mp = await mobilePage();
    await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(mp);
    await mp.tap('.dc-tabbar button[data-ctx-area="assistants"]');
    await mp.waitForSelector("dc-ctx-sheet:not([hidden]) [data-assistant-rows] [data-assistant-instance]", { state: "attached", timeout: 8000 });
    await sleep(400);
    const ids = () => mp.$$eval("dc-ctx-sheet [data-assistant-instance]", (els) => els.map((el) => el.dataset.assistantInstance));
    const before = await ids();
    assert(before.length >= 2, `the sheet holds ${before.length} assistants, expected at least 2`);
    const grip = await mp.locator(`dc-ctx-sheet [data-assistant-instance="${before[0]}"] [data-assistant-grip]`).boundingBox();
    assert(grip, "the row carries no grip on a phone");
    const second = await mp.locator(`dc-ctx-sheet [data-assistant-instance="${before[1]}"]`).boundingBox();
    const cdp = await mp.context().newCDPSession(mp);
    const start = { x: grip.x + grip.width / 2, y: grip.y + grip.height / 2 };
    const end = second.y + second.height / 2 + 4;
    await cdp.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ x: start.x, y: start.y, id: 1 }] });
    for (let i = 1; i <= 8; i += 1) {
      await cdp.send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ x: start.x, y: Math.round(start.y + (end - start.y) * (i / 8)), id: 1 }] });
      await sleep(30);
    }
    // Read while the finger is still down: the row has to be the carried one.
    const carried = await mp.locator(`dc-ctx-sheet [data-assistant-instance="${before[0]}"].dc-row-dragging`).count();
    const [saved] = await Promise.all([
      mp.waitForResponse((r) => r.url().includes("/assistants/order") && r.request().method() === "POST", { timeout: 15000 }),
      cdp.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] }),
    ]);
    assert(carried === 1, "the finger on the grip carried no row");
    assert(saved.status() === 204, `the order answered ${saved.status()}`);
    const want = [before[1], before[0], ...before.slice(2)];
    await mp.waitForFunction(
      (order) => [...document.querySelectorAll("dc-ctx-sheet [data-assistant-instance]")]
        .map((row) => row.dataset.assistantInstance).join(" ") === order.join(" "),
      want,
      { timeout: 15000 },
    );
    // Out through a navigation: a tap right after a touch stream that the CDP
    // drove is not reliably the next gesture the browser sees.
    await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    return `the top row dragged to ${want.slice(0, 2).map((id) => id.slice(0, 4)).join(" ")}`;
  });

  await run("a finished answer marks the entry points until it is read", async () => {
    // Any other surface of this run that still shows the conversation would
    // read the news the moment it arrives, so everything closes first.
    const parked = await mobilePage();
    await closePanel(parked);
    await parked.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });

    await openConversation(page, chatID);
    await page.fill("[data-assistant-input]", "Tell me the MAGIC word, answer LATER");
    await Promise.all([
      page.waitForResponse((r) => r.request().method() === "POST" && r.url().includes(`/assistants/${chatID}`), { timeout: 20000 }),
      page.click("[data-assistant-send]"),
    ]);
    // Leave before the answer lands: an open surface reads its own news, so
    // the marks only have something to show once it is out of sight.
    await closePanel(page);
    // The mark on the entry points is one for the whole area, not one
    // assistant's: news in any of them lights the rail and the tab bar,
    // whichever one an entry would open, and which one it is in is what the
    // list column's rows say.
    const areaDots = '[data-notify-any="assistant"]:not(.d-none)';
    await page.waitForFunction(
      (sel) => document.querySelectorAll(sel).length >= 2,
      areaDots,
      { timeout: 20000 },
    );
    assert(await page.locator(`.dc-rail ${areaDots}`).count() === 1,
      "the rail entry carries no news mark");
    assert(await page.locator(`.dc-tabbar ${areaDots}`).count() === 1,
      "the tab bar entry carries no news mark");
    // And it is on the screen, not only in the markup: the entries wear the
    // assistant's session icon, which hides the dot that names one target
    // until the icon says news, and this dot answers for the whole area
    // instead. Each entry is asked where it is the one that shows, the rail on
    // the desktop and the tab bar on the phone.
    const painted = async (surface, where) => surface.locator(`${where} ${areaDots}`).evaluate((dot) => {
      const style = window.getComputedStyle(dot);
      return style.display !== "none" && style.visibility !== "hidden" && dot.getBoundingClientRect().width > 0;
    });
    assert(await painted(page, ".dc-rail"), "the rail mark is in the markup but not on the screen");
    await parked.waitForFunction(
      (sel) => document.querySelectorAll(`.dc-tabbar ${sel}`).length === 1,
      areaDots,
      { timeout: 20000 },
    );
    assert(await painted(parked, ".dc-tabbar"), "the tab bar mark is in the markup but not on the screen");
    // And the row of the assistant that rang says which one it was.
    const fragment = await page.evaluate(async () => {
      const res = await fetch("/ctx/assistants?path=/assistants", { headers: { Accept: "text/html" } });
      return res.text();
    });
    assert(fragment.includes(`data-notify-target="${chatID}"`), "the assistant column drops the mark");
  });

  await run("the notification opens the page on the answer it announces", async () => {
    // What this assistant is called right now, read from its own row: the
    // first prompt named it, so the name is not a constant.
    const answeredBy = await page.evaluate(async (id) => {
      const res = await fetch("/assistants/instances", { headers: { Accept: "application/json" } });
      const data = await res.json();
      return (data.assistants || []).find((a) => a.id === id)?.title || "";
    }, chatID);
    assert(answeredBy, "the assistant is not in the index");
    const entry = await page.evaluate(async (id) => {
      const res = await fetch("/notifications", { headers: { Accept: "application/json" } });
      const data = await res.json();
      return (data.notifications || []).find((n) => n.targetId === id) || null;
    }, chatID);
    assert(entry, "no notification for the assistant");
    // The title is what happened and nothing else, because a phone's push
    // gives it one line: the assistant's name is not in it at all.
    assert(/^Answer ready\.$/.test(entry.title || ""),
      `the title is not what happened alone (${answeredBy}): ${entry.title}`);
    assert([...(entry.title || "")].length <= 32,
      `the title runs past what a push shows: ${entry.title}`);
    // The name opens the line below instead, in front of the first words of
    // the answer, which is what the list, the toast and the phone show.
    assert((entry.detail || "").startsWith(`${answeredBy.slice(0, 12)}`),
      `the detail does not open with the assistant (${answeredBy}): ${entry.detail}`);
    assert(!entry.detail.includes("\n") && [...entry.detail].length <= 175,
      `the detail is not one short line: ${entry.detail}`);
    assert(entry.url.startsWith(`/assistants/${chatID}`), `the entry does not name the assistant's page: ${entry.url}`);
    const answered = entry.url.split("#message-")[1];
    assert(answered, `the entry does not link at a message: ${entry.url}`);

    // An early answer, so the jump has somewhere to go: the newest one sits at
    // the end of the transcript, where an un-anchored open lands anyway.
    await openConversation(page, chatID);
    const first = await page.locator('[data-role="assistant"]').first().getAttribute("data-message-id");
    assert(first !== answered, "the transcript is too short for this check");
    await closePanel(page);

    await page.goto(`${BASE}/assistants/${chatID}#message-${first}`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    await page.waitForSelector(READY, { timeout: 15000 });
    await sleep(500);
    const placed = await page.evaluate((id) => {
      const node = document.querySelector(`[data-message-id="${id}"]`);
      const scroller = document.querySelector("[data-assistant-scroll]");
      if (!node || !scroller) return { missing: true };
      const top = node.getBoundingClientRect().top - scroller.getBoundingClientRect().top;
      return { top, limit: scroller.clientHeight * 0.4 };
    }, first);
    assert(!placed.missing && placed.top > -4 && placed.top < placed.limit,
      `the page did not land on the message (${JSON.stringify(placed)})`);
  });

  await run("a bell entry click opens the assistant's page on the answer", async () => {
    // The notification center follows the entry boosted: the dropdown closes,
    // the page is the announced conversation, landed on the message.
    await closePanel(page);
    const entry = await page.evaluate(async (id) => {
      const res = await fetch("/notifications", { headers: { Accept: "application/json" } });
      const data = await res.json();
      return (data.notifications || []).find((n) => n.targetId === id) || null;
    }, chatID);
    assert(entry, "no notification for the assistant");
    const answered = entry.url.split("#message-")[1];
    assert(answered, `the entry does not link at a message: ${entry.url}`);
    await page.locator(".dc-notify-bell:visible").first().click();
    await page.waitForSelector(".dc-notify-menu.show", { timeout: 6000 });
    await page.locator(`.dc-notify-menu.show a[data-notify-target="${chatID}"]`).first().click();
    await page.waitForURL(new RegExp(`/assistants/${chatID}`), { timeout: 15000 });
    await page.waitForSelector(READY, { timeout: 15000 });
    assert((await page.locator("dc-assistant").getAttribute("assistant-id")) === chatID,
      "the page is not on the announced assistant");
    await page.waitForSelector(`[data-message-id="${answered}"]`, { state: "attached", timeout: 8000 });
    await page.waitForSelector(".dc-notify-menu.show", { state: "detached", timeout: 4000 });
    // Reading it takes the area's mark out again, live and without a reload:
    // one dot for the whole area means nothing is left to light it.
    await page.waitForFunction(
      () => document.querySelectorAll('[data-notify-any="assistant"]:not(.d-none)').length === 0,
      null,
      { timeout: 15000 },
    );
  });

  await run("opening the page lands at the end of the transcript", async () => {
    await closePanel(page);
    await openAssistant(page, chatID);
    // Pictures in the transcript decode after the element is done, so the
    // measurement has to happen after the layout settled, not on ready.
    await sleep(500);
    const rest = await page.evaluate(() => {
      const scroller = document.querySelector("[data-assistant-scroll]");
      return scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight;
    });
    assert(rest < 8, `the transcript opened ${rest}px above its end`);
  });

  await run("a long transcript renders its end and opens the rest on request", async () => {
    await openAssistant(page, chatID);
    // The window is 20 messages, so the transcript is pushed past it.
    for (let i = 0; (await page.locator("[data-assistant-message]").count()) <= 22 && i < 12; i += 1) {
      await send(page, `MAGIC turn ${i}`);
      await waitSettled(page);
    }
    await closePanel(page);
    await openAssistant(page, chatID);
    const shown = await page.locator("[data-assistant-message]").count();
    assert(shown === 20, `the window rendered ${shown} messages instead of 20`);
    const all = page.locator("[data-assistant-all]");
    assert(await all.isVisible(), "no way back to the earlier messages");

    // A reader pressing the button has scrolled up. The message they were
    // looking at must keep its place in the viewport, the earlier messages
    // extend above it.
    const place = await page.evaluate(() => {
      const scroller = document.querySelector("[data-assistant-scroll]");
      scroller.scrollTop = 0;
      const message = scroller.querySelector("[data-assistant-message][data-message-id]");
      return {
        id: message.getAttribute("data-message-id"),
        top: message.getBoundingClientRect().top - scroller.getBoundingClientRect().top,
      };
    });

    await all.click();
    await page.waitForFunction((count) => document.querySelectorAll("[data-assistant-message]").length > count, shown, { timeout: 15000 });
    await page.waitForSelector(READY, { timeout: 15000 });
    assert((await page.locator("[data-assistant-all]").count()) === 0, "the whole transcript still offers to show earlier messages");
    assert(new URL(page.url()).pathname === `/assistants/${chatID}` && new URL(page.url()).search === "?all=1",
      `showing the rest landed on ${page.url()}`);
    await sleep(500);
    const after = await page.evaluate((id) => {
      const scroller = document.querySelector("[data-assistant-scroll]");
      const message = scroller?.querySelector(`[data-message-id="${id}"]`);
      if (!message) return null;
      return {
        top: message.getBoundingClientRect().top - scroller.getBoundingClientRect().top,
        above: scroller.scrollTop,
      };
    }, place.id);
    assert(after, "the anchor message is gone from the full transcript");
    assert(Math.abs(after.top - place.top) < 8,
      `the anchor message moved from ${Math.round(place.top)} to ${Math.round(after.top)}`);
    assert(after.above > 0, "no earlier messages extend above the anchor");
  });

  // The reader's frame moves by nothing at all: the message they looked at
  // keeps its place on the screen, measured against the viewport and not
  // against the scroller, because what once moved was the whole app grid
  // under an unchanged scroller. A note in the thread is the case that found
  // it (its badge's hidden label stood outside the scroller), so one is
  // written first when the inbox is mounted.
  await run("show earlier messages keeps the reader's place on the phone and on the desktop", async () => {
    let noted = "no note, NOTIFY_DIR is not set";
    if (NOTIFY_DIR) {
      await L.createProject(page, jobsProject).catch(() => {});
      const jobsDir = await L.projectPath(page, jobsProject);
      const created = await startCoder(page, jobsDir, "place-task", "Write the README.");
      assert(created.status === 200, `create answered ${created.status}`);
      const steered = await postJobs(page, {
        form: "steer",
        assistant: chatID,
        terminal: created.body.id,
        task: "Write the README",
        done_when: "WAKE_DONE: README.md exists",
      });
      assert(steered.status === 200, `steer answered ${steered.status}: ${JSON.stringify(steered.body)}`);
      await openAssistant(page, chatID);
      ring(created.body.id);
      await page.waitForSelector('[data-assistant-wake="done"]', { timeout: 30000 });
      await L.stopSession(page, `${BASE}/coders/${created.body.id}`);
      noted = "a note in the thread";
    }
    const mp = await mobilePage();
    for (const [label, p] of [["desktop", page], ["phone", mp]]) {
      await openAssistant(p, chatID);
      const all = p.locator("[data-assistant-all]");
      assert(await all.isVisible(), `${label}: no way back to the earlier messages`);
      const shown = await p.locator("[data-assistant-message]").count();
      const place = await p.evaluate(() => {
        const scroller = document.querySelector("[data-assistant-scroll]");
        scroller.scrollTop = 0;
        const message = scroller.querySelector("[data-assistant-message][data-message-id]");
        return {
          id: message.getAttribute("data-message-id"),
          top: message.getBoundingClientRect().top,
          thread: scroller.getBoundingClientRect().top,
        };
      });
      await sleep(300);
      // The element's own click, not the driver's: the driver scrolls a
      // button into view first, and the place is read on the click.
      await p.evaluate(() => document.querySelector("[data-assistant-all]").click());
      await p.waitForFunction((count) => document.querySelectorAll("[data-assistant-message]").length > count, shown, { timeout: 15000 });
      await p.waitForSelector(READY, { timeout: 15000 });
      await sleep(500);
      const after = await p.evaluate((id) => {
        const scroller = document.querySelector("[data-assistant-scroll]");
        const message = scroller?.querySelector(`[data-message-id="${id}"]`);
        if (!message) return null;
        return { top: message.getBoundingClientRect().top, thread: scroller.getBoundingClientRect().top };
      }, place.id);
      assert(after, `${label}: the anchor message is gone from the full transcript`);
      assert(Math.abs(after.top - place.top) < 2,
        `${label}: the anchor message moved from ${Math.round(place.top)} to ${Math.round(after.top)} on the screen`);
      assert(Math.abs(after.thread - place.thread) < 2,
        `${label}: the thread moved from ${Math.round(place.thread)} to ${Math.round(after.thread)} on the screen`);
    }
    return `${noted}, the anchor stands where it stood on both`;
  });

  await run("the assistants, memory and jobs lists serve their own fragments", async () => {
    for (const path of [`/ctx/assistants?path=/assistants/${chatID}`, "/assistants/memory", "/assistants/jobs"]) {
      const fragment = await page.evaluate(async (p) => {
        const res = await fetch(p, { headers: { Accept: "text/html" } });
        return { status: res.status, body: await res.text() };
      }, path);
      assert(fragment.status === 200, `${path} answered ${fragment.status}`);
      assert(fragment.body.includes("data-assistant-body") && !fragment.body.includes("<html"),
        `${path} is not just the list body`);
    }
  });

  await run("the jobs button says whether anything is steered, live", async () => {
    await L.createProject(page, jobsProject).catch(() => {});
    const jobsDir = await L.projectPath(page, jobsProject);
    await openAssistant(page, chatID);
    const badge = page.locator("[data-assistant-watching-button] .dc-steer-badge");
    assert(await badge.count() === 1, "no badge on the jobs button in the page head");
    assert((await badge.first().getAttribute("class")).includes("d-none"),
      "the badge claims a steered job before there is one");
    assert((await page.locator("[data-assistant-watching-button]").getAttribute("title")) === "Watching",
      "the button that opens the aside does not say Watching");

    const created = await startCoder(page, jobsDir, "jobs-task", "Write the README.");
    assert(created.status === 200, `create answered ${created.status}`);
    jobCoder = created.body.id;
    // Which assistant a job reports to travels in the form: with several of
    // them the server refuses to pick one, and the aside below is this
    // assistant's, so the job has to be its.
    const steered = await postJobs(page, {
      form: "steer",
      assistant: chatID,
      terminal: jobCoder,
      task: "Write the README",
      done_when: "WAKE_NOTHING: never true",
    });
    assert(steered.status === 200, `steer answered ${steered.status}: ${JSON.stringify(steered.body)}`);

    // Live, over the assistant event: no reload, no polling.
    await page.waitForFunction(
      () => {
        const mark = document.querySelector("[data-assistant-watching-button] .dc-steer-badge");
        return mark && !mark.classList.contains("d-none") && Number(mark.textContent) > 0;
      },
      null,
      { timeout: 15000 },
    );

    // A count in the icon's corner, in the steered purple, not a number
    // standing next to the wheel.
    const mark = await page.evaluate(() => {
      const el = document.querySelector("[data-assistant-watching-button] .dc-steer-badge");
      const own = getComputedStyle(el);
      const steer = getComputedStyle(document.documentElement).getPropertyValue("--dc-steer-rgb").trim();
      return { position: own.position, background: own.backgroundColor, steer: `rgb(${steer})` };
    });
    assert(mark.position === "absolute", `the steer badge is ${mark.position}, not a corner mark`);
    assert(mark.background === mark.steer, `the steer badge is ${mark.background}, not ${mark.steer}`);

    await openView(page, "jobs", `[data-assistant-job="${jobCoder}"]`);
    // The state is the row's own icon, not a word beside it: the marker
    // carries it and the icon names itself for whoever reads by a label.
    await page.waitForFunction(
      (id) => document.querySelector(`[data-assistant-job="${id}"] [data-assistant-job-state]`)?.dataset.assistantJobState === "steering",
      jobCoder,
      { timeout: 15000 },
    );
    const icon = page.locator(`[data-assistant-job="${jobCoder}"] [data-assistant-job-state]`);
    assert((await icon.getAttribute("aria-label")) === "Steering",
      "the steering job's icon does not name its state");
    assert((await page.locator(".modal.show").count()) === 0, "the jobs list opened a modal");
    assert((await page.locator(".modal-backdrop").count()) === 0, "the jobs list left a backdrop");
    // The aside holds both kinds of standing work, so its own head names them
    // together; the page speaks of coders inside it, the word job stays in the
    // code and the CLI.
    const head = await page.locator("#assistant-aside .dc-ctx-title").textContent();
    assert(head.includes("Watching"), `the aside's sheet is not headed Watching: ${head}`);
    const section = await page.locator("[data-assistant-coders-tab]").textContent();
    assert(section.includes("Steered coders"), `the first tab is not named Steered coders: ${section}`);
    // The task and the criterion sit folded under the row, so the words are
    // read from the content, not from what is on screen.
    const text = await page.locator(`[data-assistant-job="${jobCoder}"]`).textContent();
    for (const want of ["jobs-task", "WAKE_NOTHING", "Write the README", "0 of"]) {
      assert(text.includes(want), `the job row misses ${want}:\n${text}`);
    }
    // One row per terminal. The list is the assistant's, not the conversation's,
    // so it also carries what earlier runs on this instance left behind.
    assert(await page.locator(`[data-assistant-job="${jobCoder}"]`).count() === 1,
      "the job is listed twice");
    return "one list, beside the thread";
  });

  await run("stopping a job asks first and the view acts in place", async () => {
    const row = page.locator(`[data-assistant-job="${jobCoder}"]`);
    await unfoldJob(page, jobCoder);
    await row.locator("[data-assistant-job-stop]").click();
    await page.waitForSelector(".swal2-container", { state: "visible", timeout: 8000 });
    await page.click(".swal2-cancel");
    await page.waitForSelector(".swal2-container", { state: "detached", timeout: 8000 });
    assert((await row.locator("[data-assistant-job-state]").getAttribute("data-assistant-job-state")) === "steering",
      "a cancelled confirmation stopped the job anyway");

    await row.locator("[data-assistant-job-stop]").click();
    await L.confirmSwal(page);
    await page.waitForFunction(
      (id) => document.querySelector(`[data-assistant-job="${id}"] [data-assistant-job-state]`)?.dataset.assistantJobState === "stopped",
      jobCoder,
      { timeout: 15000 },
    );
    assert(await page.locator("#assistant-aside").isVisible(), "the aside went away under the action");
    return "confirmed, stopped in place";
  });

  await run("a stopped job can be steered again with the same criterion", async () => {
    const row = page.locator(`[data-assistant-job="${jobCoder}"]`);
    await unfoldJob(page, jobCoder);
    await row.locator("[data-assistant-job-again]").click();
    await page.waitForFunction(
      (id) => document.querySelector(`[data-assistant-job="${id}"] [data-assistant-job-state]`)?.dataset.assistantJobState === "steering",
      jobCoder,
      { timeout: 15000 },
    );
    const text = await row.textContent();
    assert(text.includes("WAKE_NOTHING"), `the criterion did not survive: ${text}`);
    assert(text.includes("0 of"), `the budget was not given back: ${text}`);
    assert(await row.locator("[data-assistant-job-open]").count() === 1, "no way from the job to its coder");
    // Two ways to look at a steered coder: its screen and the files it writes.
    const editor = row.locator("[data-assistant-job-editor]");
    assert(await editor.count() === 1, "no way from the job to the editor");
    const href = await editor.getAttribute("href");
    assert(/^\/projects\/[^/]+\/editor$/.test(href), `the editor link points at ${href}`);
    // The two names on the row lead where they name.
    const coderHref = await row.locator("[data-assistant-job-coder-link]").getAttribute("href");
    assert(coderHref === `/coders/${jobCoder}`, `the name points at ${coderHref}`);
    const projectHref = await row.locator("[data-assistant-job-project-link]").getAttribute("href");
    assert(/^\/projects#project-.+/.test(projectHref), `the project points at ${projectHref}`);
    return "steering again, same criterion";
  });

  await run("on a phone the jobs sheet is thumb sized", async () => {
    const mp = await mobilePage();
    await openConversation(mp, chatID);
    await mp.click("[data-assistant-watching-button]");
    await mp.waitForSelector(`#assistant-aside.show [data-assistant-job="${jobCoder}"]`, { timeout: 15000 });
    await unfoldJob(mp, jobCoder);
    await sleep(400);
    const fit = await mp.evaluate((id) => {
      const stop = document.querySelector(`[data-assistant-job="${id}"] [data-assistant-job-stop]`);
      if (!stop) return "the action is missing";
      // Every action of the row is thumb sized and inside the screen, the way
      // to the editor next to the way to the coder included.
      for (const sel of ["[data-assistant-job-open]", "[data-assistant-job-editor]", "[data-assistant-job-stop]"]) {
        const el = document.querySelector(`[data-assistant-job="${id}"] ${sel}`);
        if (!el) return `${sel} is missing`;
        const r = el.getBoundingClientRect();
        if (r.height < 26) return `${sel} is ${r.height} high`;
        if (r.right > window.innerWidth + 1 || r.left < -1) return `${sel} sticks out: ${JSON.stringify(r)}`;
      }
      const b = stop.getBoundingClientRect();
      if (b.height < 26) return `the action is ${b.height} high`;
      if (b.right > window.innerWidth + 1 || b.left < -1) return `the action sticks out: ${JSON.stringify(b)}`;
      return "";
    }, jobCoder);
    assert(fit === "", `the view is not usable with a thumb: ${fit}`);

    // The aside starts with the head of its first section, and that head stands
    // as far from the top as the rows stand from the sides: one gap, the sheet
    // body's own padding. The rows below it keep it too.
    const gaps = await mp.evaluate(() => {
      const body = document.querySelector("#assistant-aside .offcanvas-body");
      const head = document.querySelector("#assistant-aside [data-assistant-coders-tab]");
      const content = document.querySelector("#assistant-aside [data-assistant-job] .d-flex");
      const b = body.getBoundingClientRect();
      const h = head.getBoundingClientRect();
      const c = content.getBoundingClientRect();
      return {
        top: Math.round(h.top - b.top),
        headLeft: Math.round(h.left - b.left),
        left: Math.round(c.left - b.left),
        right: Math.round(b.right - c.right),
      };
    });
    assert(gaps.top === gaps.left && gaps.top === gaps.right && gaps.headLeft === gaps.left,
      `the sheet's gaps differ: ${JSON.stringify(gaps)}`);
    return `full width, reachable, gaps ${gaps.top}/${gaps.left}/${gaps.right}`;
  });

  // A job's coder is a link that leaves the page. On a phone
  // the sheet is a visit, so it goes with the navigation, and the way back
  // is the tab bar's sparkle and the current conversation's row.
  await run("the way from a job to its coder leaves and comes back clean", async () => {
    const mp = await mobilePage();
    await unfoldJob(mp, jobCoder);
    await mp.click(`[data-assistant-job="${jobCoder}"] [data-assistant-job-open]`);
    await mp.waitForURL((url) => url.pathname.includes(jobCoder), { timeout: 15000 });
    await sleep(400);
    assert(await mp.locator("#assistant-aside.show, .offcanvas-backdrop").count() === 0,
      "the aside sheet stayed over the coder page");
    await mp.tap('.dc-tabbar button[data-ctx-area="assistants"]');
    await mp.waitForSelector("dc-ctx-sheet:not([hidden]) [data-assistant-rows] [data-assistant-instance]", { timeout: 8000 });
    await mp.tap("dc-ctx-sheet [data-assistant-rows] [data-assistant-instance] a[href]");
    await mp.waitForSelector(READY, { timeout: 15000 });
    await closePanel(mp);
    return "coder page usable, two taps back";
  });

  // A turn is not a child of the server any more. The fake kills the cockpit
  // halfway through its answer and starts it again, so this check restarts the
  // instance it runs against: throwaway only. Nothing here reloads the page,
  // which is what makes the assertion mean something: what stands in the
  // bubble at the end came over the conversation's own stream, reconnected by
  // the browser across the gap.
  await run("an answer survives a restart of the cockpit and finishes on the open surface", async () => {
    await openConversation(page, chatID);
    await page.evaluate(() => { window.__dcSamePage = true; });

    await send(page, "please RESTART_CHAT now");
    await waitSettled(page, 90000);

    const samePage = await page.evaluate(() => window.__dcSamePage === true);
    assert(samePage, "the page reloaded, so this says nothing about the open stream");

    const last = page.locator('[data-role="assistant"]').last();
    const state = await last.getAttribute("data-state");
    assert(state === "complete", `the answer did not complete across the restart: ${state}`);
    const text = (await last.innerText()).trim();
    assert(text.includes("before the restart") && text.includes("and after it"),
      `the answer lost a half: ${text}`);
    assert(!/interrupted/i.test(text), `the answer is marked interrupted: ${text}`);
    return text;
  });

  // A job belongs to the assistant that steered it, so the aside of another
  // assistant's page does not show it: with several of them, "who holds what"
  // is the question the surface has to answer.
  await run("a job stands in the aside of the assistant that steers it and nowhere else", async () => {
    const owner = await openAssistant(page, chatID);
    await openView(page, "jobs", `[data-assistant-job="${jobCoder}"]`);
    const state = await page.locator(`[data-assistant-job="${jobCoder}"] [data-assistant-job-state]`).getAttribute("data-assistant-job-state");
    assert(state === "steering", `the job is not steering: ${state}`);

    const other = await newConversation(page);
    assert(other !== owner, "the second assistant did not open");
    await openView(page, "jobs", "[data-assistant-jobs-empty], [data-assistant-job]");
    assert((await page.locator(`[data-assistant-job="${jobCoder}"]`).count()) === 0,
      "another assistant's page lists a job it does not steer");

    await openConversation(page, owner);
    await openView(page, "jobs", `[data-assistant-job="${jobCoder}"]`);
    return "one owner, one aside";
  });

  // Deleting an assistant releases the coders it steers and says which ones:
  // a coder that quietly stops being watched is the ending nobody hears. It is
  // deleted from another assistant's page on purpose, so the toast is not
  // carried away by the navigation off the deleted one's own page.
  await run("deleting an assistant hands its coders back and names them", async () => {
    const owner = await openAssistant(page, chatID);
    // The job has to be open and it has to be this assistant's for the check to
    // say anything, and by now it may be closed or held by another one. The
    // browser is the user, so it may call any job off; then it is steered for
    // the assistant about to be deleted.
    await postJobs(page, { form: "release", terminal: jobCoder });
    const steered = await postJobs(page, { form: "steer", assistant: owner, terminal: jobCoder, done_when: "still steering" });
    assert(steered.status === 200, `steering for the owner answered ${steered.status}: ${JSON.stringify(steered.body)}`);
    const from = await newConversation(page);
    assert(from !== owner, "no second assistant to delete from");

    await openView(page, "history", "[data-assistant-rows] [data-assistant-instance]");
    await page.click(`[data-assistant-rows] [data-assistant-instance="${owner}"] [data-assistant-menu]`);
    await page.waitForSelector(".dc-context-menu", { timeout: 8000 });
    await page.click('.dc-context-menu .dropdown-item:has-text("Delete")');
    await L.confirmSwal(page);
    const toast = await page.waitForSelector(".dc-toast", { timeout: 15000 });
    const said = (await toast.innerText()).trim();
    assert(/yours again/.test(said), `the deletion did not name the released coders: ${said}`);
    await page.waitForSelector(`[data-assistant-rows] [data-assistant-instance="${owner}"]`, { state: "detached", timeout: 10000 });

    // The job went with its assistant: nothing steers that coder any more.
    const jobs = await page.evaluate(async () => {
      const res = await fetch("/assistants/jobs", { headers: { Accept: "text/html" } });
      return res.text();
    });
    assert(!jobs.includes(jobCoder), "a released job is still listed after its assistant was deleted");
    // The run's assistant was the one deleted, so the checks after this one
    // carry on in the one it was deleted from.
    chatID = from;
    return said;
  });

  // The one steered indicator is the coder icon itself turning purple,
  // rendered on the server and riding the terminals event: it appears with
  // the job and goes with the release, on pages that are never reloaded here.
  // The steer itself comes from the page without a criterion, which the page
  // path allows.
  await run("a steered coder's icon turns purple and release takes it back", async () => {
    await openAssistant(page, chatID);
    const dir = await L.projectPath(page, jobsProject);
    const created = await startCoder(page, dir, "mark-task", "Write the file.");
    assert(created.status === 200, `create answered ${created.status}`);
    const marked = created.body.id;
    const steered = await postJobs(page, { form: "steer", assistant: chatID, terminal: marked, task: "Write the file", done_when: "" });
    assert(steered.status === 200, `steer answered ${steered.status}: ${JSON.stringify(steered.body)}`);

    await closePanel(page);
    await page.goto(`${BASE}/coders/${marked}`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    // The mark sits on every surface that lists the coder, so the selector is
    // scoped to the one that is on screen here: the tab strip. Unscoped it
    // resolves to several nodes and the first is the attach page's own badge in
    // the work head, which is a phone only element (d-lg-none), so a wait for
    // it to become visible can never pass on a desktop viewport. The others
    // are checked as rendered.
    const mark = `.terminal-tabs-strip .dc-term-icon.steered[data-notify-target="${marked}"]`;
    await page.waitForSelector(mark, { timeout: 10000 });
    assert(await page.locator(`.dc-work-head dc-steered-mark .dc-term-icon.steered[data-notify-target="${marked}"]`).count() === 1,
      "the attach page's own badge does not carry the mark");

    // The phone's project list carries the mark and the menu data too.
    const mp = await mobilePage();
    await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(mp);
    await mp.waitForSelector(`[data-chip-id="${marked}"][data-chip-steered="1"]`, { timeout: 10000 });

    // Released from the other device, the attach header follows the
    // terminals event without a reload.
    await postJobs(mp, { form: "release", terminal: marked });
    await page.waitForSelector(mark, { state: "detached", timeout: 15000 });

    await page.evaluate(async (session) => {
      const token = document.querySelector('meta[name="csrf-token"]').content;
      await fetch(`/coders/${session}/delete`, { method: "POST", headers: { "X-CSRF-Token": token } });
    }, marked);
    return "purple with the job, gone with the release";
  });

  await run("the steered coder and its project are cleaned up", async () => {
    await page.evaluate(async (session) => {
      const token = document.querySelector('meta[name="csrf-token"]').content;
      await fetch(`/coders/${session}/delete`, { method: "POST", headers: { "X-CSRF-Token": token } });
    }, jobCoder);
    await L.deleteProject(page, jobsProject).catch(() => {});
  });

  await run("deleting an assistant through its menu keeps the surface reachable", async () => {
    const conversation = await openAssistant(page, chatID);
    await openView(page, "history", "[data-assistant-rows] [data-assistant-instance]");
    await page.click(`[data-assistant-rows] [data-assistant-instance="${conversation}"] [data-assistant-menu]`);
    await page.waitForSelector(".dc-context-menu", { timeout: 8000 });
    const items = await page.locator(".dc-context-menu .dropdown-item").allInnerTexts();
    assert(items.some((t) => t.includes("Open")) && items.some((t) => t.includes("Delete")),
      `the row menu misses an action: ${items.join(", ")}`);
    await page.click('.dc-context-menu .dropdown-item:has-text("Delete")');
    await L.confirmSwal(page);
    // The deleted assistant's page is gone with it: the bare address comes
    // back to one of the others, or to the empty state when it was the last.
    await page.waitForFunction(
      (gone) => document.querySelector("dc-assistant[ready]")?.getAttribute("assistant-id") !== gone
        || Boolean(document.querySelector("[data-assistant-none]")),
      conversation,
      { timeout: 15000 },
    );
    assert(new URL(page.url()).pathname.startsWith("/assistants"), `the deletion landed on ${page.url()}`);
    assert(await page.locator(`[data-assistant-rows] [data-assistant-instance="${conversation}"]`).count() === 0,
      "the deleted assistant is still in the column");
    // This one deleted the run's assistant too, so the rest carries on in
    // whichever is left standing at the top of the list.
    chatID = await page.locator("[data-assistant-rows] [data-assistant-instance]").first()
      .getAttribute("data-assistant-instance");
    assert(chatID, "the deletion left no assistant to carry on in");
    await closePanel(page);
  });

  // A column row opens that assistant's page, the menu's Open does the same,
  // and the one on screen is the one the row named.
  await run("a column row opens its assistant on its page", async () => {
    await openAssistant(page, chatID);
    await openView(page, "history", "[data-assistant-rows] [data-assistant-instance]");
    const row = page.locator("[data-assistant-rows] [data-assistant-instance]:not(.active)").first();
    const victim = await row.getAttribute("data-assistant-instance").catch(() => null);
    assert(victim, "no other row to open");
    await row.locator("a").click();
    await page.waitForURL(new RegExp(`/assistants/${victim}$`), { timeout: 15000 });
    await page.waitForSelector(READY, { timeout: 15000 });
    assert((await page.locator("dc-assistant").getAttribute("assistant-id")) === victim,
      "the page shows a different assistant than the row named");
    assert((await page.locator("dc-assistant [data-assistant-input]").count()) === 1,
      "an assistant opened from the column has no composer");
    await closePanel(page);
    return "every row is live";
  });

  await run("a prompt keeps its line breaks in the optimistic bubble", async () => {
    await openAssistant(page, chatID);
    await send(page, "MAGIC first line\nsecond line");
    const optimistic = await page.locator('[data-role="user"] [data-assistant-text]').last()
      .evaluate((n) => n.innerHTML);
    assert(optimistic.includes("first line<br>second line"),
      `the optimistic bubble collapsed the breaks: ${optimistic}`);
    await waitSettled(page);
  });

  // The stamp belongs to the bubble the moment it appears, and the server
  // rendered message that may replace it must not put a second one next to it.
  await run("a sent message shows its time at once and one after a reload", async () => {
    await openAssistant(page, chatID);
    await idle(page);
    const marker = "MAGIC stamped right away";
    await page.fill("[data-assistant-input]", marker);
    await page.click("[data-assistant-send]");
    await page.waitForFunction(() => {
      const nodes = document.querySelectorAll('[data-role="user"]');
      const stamps = nodes[nodes.length - 1]?.querySelectorAll("dc-time") || [];
      return stamps.length === 1 && stamps[0].textContent.trim() !== "";
    }, null, { timeout: 8000 });
    const fresh = await page.evaluate(() => {
      const nodes = document.querySelectorAll('[data-role="user"]');
      const stamp = nodes[nodes.length - 1].querySelector("dc-time");
      return {
        raw: stamp.getAttribute("datetime"),
        shown: stamp.textContent.trim(),
        want: new Date(stamp.getAttribute("datetime"))
          .toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" }),
      };
    });
    assert(!Number.isNaN(Date.parse(fresh.raw)), `the fresh stamp is not machine readable: ${fresh.raw}`);
    assert(fresh.shown === fresh.want, `the fresh stamp is not locale formatted: ${fresh.shown} != ${fresh.want}`);
    await waitSettled(page);
    await openAssistant(page, chatID);
    const after = await page.evaluate((text) => {
      const nodes = [...document.querySelectorAll('[data-role="user"]')]
        .filter((node) => node.textContent.includes(text));
      const last = nodes[nodes.length - 1];
      return {
        found: !!last,
        stamps: last ? last.querySelectorAll("dc-time").length : 0,
        shown: last?.querySelector("dc-time")?.textContent.trim() || "",
      };
    }, marker);
    assert(after.found, "the message is gone after the reload");
    assert(after.stamps === 1, `the reloaded bubble carries ${after.stamps} stamps`);
    assert(after.shown !== "", "the reloaded bubble shows no time");
    return fresh.shown;
  });

  await run("message times are machine stamps the browser formats in its locale", async () => {
    await page.waitForFunction(() => {
      const nodes = document.querySelectorAll('[data-role="assistant"]');
      const stamp = nodes[nodes.length - 1]?.querySelector("dc-time");
      return stamp && stamp.textContent.trim() !== "";
    }, null, { timeout: 10000 });
    const time = await page.evaluate(() => {
      const nodes = document.querySelectorAll('[data-role="assistant"]');
      const stamp = nodes[nodes.length - 1].querySelector("dc-time");
      return {
        raw: stamp.getAttribute("datetime"),
        shown: stamp.textContent,
        want: new Date(stamp.getAttribute("datetime"))
          .toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" }),
      };
    });
    assert(!Number.isNaN(Date.parse(time.raw)), `datetime is not machine readable: ${time.raw}`);
    assert(time.shown === time.want, `the browser did not format the stamp: ${time.shown} != ${time.want}`);
    return time.shown;
  });

  await run("the composer resists resizing while the aside's boxes allow it", async () => {
    const composer = await page.locator("[data-assistant-input]")
      .evaluate((n) => getComputedStyle(n).resize);
    assert(composer === "none", `the composer grew a resize handle: ${composer}`);
    await openView(page, "memory", "#memory-new");
    if (!(await page.locator("#memory-new.show").count())) {
      await page.click("[data-memory-add]");
      await page.waitForSelector("#memory-new.show", { timeout: 8000 });
    }
    const memo = await page.locator("#memory-body").evaluate((n) => getComputedStyle(n).resize);
    assert(memo === "vertical", `the memory box lost its resize handle: ${memo}`);
    await closePanel(page);
  });

  // macOS sends Home and End for Fn+Left and Fn+Right, and a textarea answers
  // them by scrolling its box while the caret stays where it was. The composer
  // takes both keys and jumps through the whole text.
  await run("Home and End jump to the start and the end of the composer", async () => {
    await openAssistant(page, chatID);
    const box = "dc-assistant [data-assistant-input]";
    await page.fill(box, "first line\nsecond line\nthird line");
    const text = await page.inputValue(box);
    await page.locator(box).evaluate((el) => el.setSelectionRange(15, 15));
    await page.keyboard.press("End");
    let caret = await page.locator(box).evaluate((el) => [el.selectionStart, el.selectionEnd]);
    assert(caret[0] === text.length && caret[1] === text.length, `End did not reach the end: ${caret}`);
    await page.keyboard.press("Home");
    caret = await page.locator(box).evaluate((el) => [el.selectionStart, el.selectionEnd]);
    assert(caret[0] === 0 && caret[1] === 0, `Home did not reach the start: ${caret}`);
    await page.locator(box).evaluate((el) => el.setSelectionRange(15, 15));
    await page.keyboard.press("Shift+End");
    caret = await page.locator(box).evaluate((el) => [el.selectionStart, el.selectionEnd]);
    assert(caret[0] === 15 && caret[1] === text.length, `Shift+End did not extend to the end: ${caret}`);
    await page.keyboard.press("Shift+Home");
    caret = await page.locator(box).evaluate((el) => [el.selectionStart, el.selectionEnd]);
    assert(caret[0] === 0 && caret[1] === 15, `Shift+Home did not extend to the start: ${caret}`);
    await page.fill(box, "");
    await sleep(1200);
    await closePanel(page);
    return "both keys jump, Shift extends";
  });

  // The error state replaces the whole panel body, so it must carry its own
  // close control, on a phone there is no Escape key to fall back to.
});
