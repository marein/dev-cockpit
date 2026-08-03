const L = require("./lib");
const { assert, BASE, sleep } = L;

// Assistant: the cockpit's own conversation. It has no pages: the overlay
// (dc-assistant-panel, fetched from GET /assistant/panel) is its only surface,
// docked as a resizable side panel from lg up (its corner button replaces the
// floating quick nav there) and covering the screen below that. Jobs, memory
// and history are views inside the overlay, an earlier conversation opens
// read-only inside it, and a notification link (/projects?assistant=<id>
// #message-<id>) opens it on the announced answer. The old /assistant paths
// redirect into that query. The conversation APIs stay: POST /assistant/:id
// dispatching on the hidden form field, the SSE at /assistant/:id/stream, the
// message fragment, uploads, drafts and the byte ranged media route.
//
// The instance MUST run with tests/e2e/fakes ahead of the real CLIs on PATH,
// with a scratch HOME and its own TMUX_TMPDIR: the fakes persist provider
// conversations under $HOME and no check may spend a model request. Prompts
// steer the fakes: MAGIC (a fixed answer), SLOW (a turn that keeps running),
// FAIL (a failing turn), MARKDOWN (markup and an injection attempt), TOOL (a
// tool signal), CONTEXT_HIGH (a turn that reports a nearly full context
// window; both fakes otherwise report 68 percent of it).
//
// One check restarts the instance: the RESTART_CHAT prompt makes the fake kill
// the cockpit mid answer and start it again, which is how the run proves that a
// turn outlives its server. Point this runner at a throwaway only, and never at
// an instance somebody is using.
//
// Gotchas:
// - opening the panel opens the current conversation and creates one when
//   there is none, so a check never posts a bare "open",
// - an untouched conversation is reused, so pressing New twice must not leave
//   two empty conversations behind,
// - the composer is JS owned, every interaction waits for dc-assistant[ready],
// - the docked panel's open state is stored per device: a goto reopens it, and
//   a check that needs it closed closes it explicitly,
// - an upload finishes before the message is sent: the chip carries the name
//   the send posts back, so a check waits for the chip, not for the network,
// - the memory is what a coder reads at startup, so the generated CLAUDE.md
//   and AGENTS.md in the workspace must carry a saved entry,
// - one conversation is live at a time and starting a new one archives the
//   previous for good (its provider session is dropped), so a check that opens
//   a new conversation has to keep working in that one,
// - the composer never locks: a send during a running turn queues the message
//   (bubble with data-state="queued", Waiting badge, Don't send button) and
//   the end of the turn flushes the queue as one new turn, so a check that
//   wants a waiting bubble keeps the first turn running (SLOW) and a check
//   that wants it flushed stops that turn.

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

const READY = ".dc-assistant-panel-card:not([hidden]) dc-assistant[ready]";

// openAssistant opens the overlay on /projects (the neutral page) and returns
// the live conversation's id. A stored open panel reopens on its own, so the
// corner is only clicked when the card is still hidden.
async function openAssistant(page) {
  await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
  await dismissUpdate(page);
  if (!(await page.locator(".dc-assistant-panel-card:not([hidden])").count())) {
    await page.click("[data-assistant-corner]");
  }
  await page.waitForSelector(READY, { timeout: 15000 });
  return page.locator("dc-assistant").getAttribute("conversation-id");
}

// openConversation opens one conversation in the overlay through the same
// query a notification link carries.
async function openConversation(page, id) {
  await page.goto(`${BASE}/projects?assistant=${id}`, { waitUntil: "domcontentloaded" });
  await dismissUpdate(page);
  await page.waitForSelector(READY, { timeout: 15000 });
}

async function closePanel(page) {
  if (!(await page.locator(".dc-assistant-panel-card:not([hidden])").count())) return;
  await page.click("[data-assistant-panel-close]");
  await page.waitForSelector(".dc-assistant-panel-card[hidden]", { state: "attached", timeout: 8000 });
}

// openView switches the overlay to one of its own views. The buttons for the
// other views live in the chat head, so the way there goes back to the chat
// first.
async function openView(page, view, readySelector) {
  if (await page.locator('dc-assistant-panel [data-assistant-view-open="chat"]').count()) {
    await page.click('dc-assistant-panel [data-assistant-view-open="chat"]');
    await page.waitForSelector(READY, { timeout: 15000 });
  }
  await page.click(`dc-assistant-panel [data-assistant-view-open="${view}"]`);
  await page.waitForSelector(readySelector, { timeout: 15000 });
}

// NEW_MENU is the new conversation control on a host with several coders. Its
// label is not a selector: it carries the context percentage, so it changes with
// every turn. The attribute holding the label's stable part is what identifies
// the button.
const NEW_MENU = '[data-bs-toggle="dropdown"][data-assistant-new-label]';

// The fakes differ per coder, and the checks below read claude's answers, so a
// host with both installed picks claude explicitly. With one coder the picker
// is not rendered at all and the conversation already runs on it.
async function useClaude(page) {
  const picker = page.locator('[data-assistant-new="claude"]');
  const onClaude = (await page.locator("dc-assistant").getAttribute("data-assistant-coder")) === "claude";
  if (!(await picker.count()) || onClaude) {
    return page.locator("dc-assistant").getAttribute("conversation-id");
  }
  await page.click(NEW_MENU);
  await picker.click();
  await page.waitForSelector('dc-assistant[data-assistant-coder="claude"][ready]', { timeout: 15000 });
  return page.locator("dc-assistant").getAttribute("conversation-id");
}

// Always the coder the current conversation runs on, so the reuse check keeps
// comparing like with like: an untouched conversation is only reused for the
// coder that was asked for. The new-conversation post answers JSON, and the
// wait keys on the surface reaching that id.
async function newConversation(page) {
  const current = await page.locator("dc-assistant").getAttribute("data-assistant-coder").catch(() => null);
  const dropdown = page.locator(NEW_MENU);
  if (await dropdown.count()) await dropdown.click();
  const [response] = await Promise.all([
    page.waitForResponse((r) => r.request().method() === "POST" && r.url().includes("/assistant/")),
    page.locator(current ? `[data-assistant-new="${current}"]` : "[data-assistant-new]").first().click(),
  ]);
  const target = (await response.json().catch(() => ({}))).id;
  assert(target, `the new conversation did not answer an id: ${response.status()}`);
  await page.waitForFunction(
    (id) => document.querySelector("dc-assistant")?.getAttribute("conversation-id") === id
      && document.querySelector("dc-assistant")?.hasAttribute("ready"),
    target,
    { timeout: 15000 },
  );
  return target;
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

// The memory is shared state on a singleton surface, so the run clears its own
// entries first instead of assuming an empty memory. Saving under a title that
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
  return page.locator("[data-assistant-ring-fill]").first().getAttribute("stroke-dasharray");
}

function ringLevel(page) {
  return page.locator("[data-assistant-ring]").first().getAttribute("data-assistant-ring-level");
}

function newLabel(page) {
  return page.locator("[data-assistant-new-label]").first().getAttribute("title");
}

async function send(page, text) {
  await page.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
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
// the conversation's own dispatch route, the same one the overlay's forms and
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

async function postForm(page, conversation, fields) {
  return page.evaluate(async ([id, values]) => {
    const body = new URLSearchParams(values);
    body.set("csrf_token", document.querySelector('meta[name="csrf-token"]').content);
    const res = await fetch(`/assistant/${id}`, {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded", Accept: "application/json" },
      body: body.toString(),
    });
    return { status: res.status, body: await res.json().catch(() => ({})) };
  }, [conversation, fields]);
}

// The jobs of the assistant sit on one path, not on a conversation: steering and
// calling a job off post there, from the overlay's list and from the command line.
async function postJobs(page, fields) {
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

L.runFeature("assistant", async ({ ctx, page, run, mobilePage }) => {
  let jobCoder = "";
  let chatID = "";
  let freshID = "";

  await run("the overlay opens a conversation and creates one when there is none", async () => {
    const opened = await openAssistant(page);
    assert(opened && opened.length > 8, "no conversation id on the surface");
    assert(await page.locator("[data-assistant-input]").isVisible(), "no composer");
    // The surface is a singleton, so the run takes a conversation of its own
    // instead of asserting on whatever the instance already held.
    chatID = await useClaude(page);
    if (!(await page.locator("[data-assistant-empty]").count())) {
      chatID = await newConversation(page);
    }
    assert(await page.locator("[data-assistant-empty]").isVisible(), "the run's conversation is not empty");
  });

  await run("the assistant pages are gone, their addresses open the overlay", async () => {
    // /assistant and a conversation address redirect into the query the
    // overlay consumes; memory, history and jobs are the overlay's own
    // fragments, covered below.
    for (const path of ["/assistant", `/assistant/${chatID}`]) {
      const landed = await page.evaluate(async (p) => {
        const res = await fetch(p, { redirect: "follow", headers: { Accept: "text/html" } });
        return { url: res.url, ok: res.ok };
      }, path);
      assert(landed.ok && new URL(landed.url).pathname === "/projects",
        `${path} did not redirect into the overlay query: ${landed.url}`);
    }
    return "redirects, no pages";
  });

  await run("a turn runs end to end over the conversation's own stream", async () => {
    await send(page, "MAGIC what is the word");
    await waitSettled(page);
    const text = await page.locator('[data-role="assistant"]').last().innerText();
    assert(text.includes("FLUGHAFEN"), `answer missing: ${text}`);
  });

  await run("the transcript survives a reload of the page under the overlay", async () => {
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

    // The button still posts its form and, with more than one coder, still opens
    // its menu: the ring sits inside the button so it cannot break either.
    const posts = await page.locator("[data-assistant-new]").first().evaluate((button) =>
      button.getAttribute("form") || button.getAttribute("data-bs-toggle"));
    assert(posts, "the new conversation button lost its form or its dropdown");
    return "68, live to 96, rendered on load";
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
  await run("an unsent message waits in the conversation, on every device", async () => {
    const draft = "eine Frage, die ich noch nicht abgeschickt habe";
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

    const typed = "vom Telefon aus getippt";
    await mp.fill("[data-assistant-input]", typed);
    await page.waitForFunction(
      (text) => document.querySelector("[data-assistant-input]").value === text,
      typed,
      { timeout: 15000 },
    );

    // Sending on one device empties the other one's box the same way.
    await page.fill("[data-assistant-input]", "MAGIC weiter");
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

    const typed = "MAGIC vom Telefon geschickt";
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
        answered: Boolean(last && last.innerText.includes("FLUGHAFEN")),
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
    await openAssistant(page);
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
    await page.fill("[data-assistant-input]", "MAGIC look at this");
    await page.click("[data-assistant-send]");
    await waitSettled(page);

    const bubble = page.locator('[data-role="user"]').last();
    assert(await bubble.locator("img.dc-assistant-media").count() === 1, "no image in the message");
    assert(await bubble.locator("audio.dc-assistant-audio").count() === 1, "no player in the message");
    assert(await bubble.evaluate((node) => node.hasAttribute("data-no-pe")), "message content is not opted out of pe boosting");

    const url = await bubble.locator("img.dc-assistant-media").getAttribute("src");
    assert(url.includes(`/assistant/${chatID}/media/`), `unexpected media url: ${url}`);
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

  await run("a picture wider than the overlay clamps instead of widening it", async () => {
    await attach(page, [{ name: "wide.svg", mimeType: "image/svg+xml", buffer: WIDE_SVG }]);
    await page.fill("[data-assistant-input]", "MAGIC how wide");
    await page.click("[data-assistant-send]");
    await waitSettled(page);
    await page.waitForFunction(() => {
      const img = [...document.querySelectorAll('[data-role="user"] img.dc-assistant-media')].pop();
      return img && img.getBoundingClientRect().width >= 50;
    }, null, { timeout: 10000 });
    const wide = await page.evaluate(() => {
      const img = [...document.querySelectorAll('[data-role="user"] img.dc-assistant-media')].pop();
      const scroller = document.querySelector("[data-assistant-scroll]");
      if (Math.round(img.getBoundingClientRect().width) > scroller.clientWidth) return "the picture is wider than the overlay";
      return scroller.scrollWidth > scroller.clientWidth + 1 ? "the transcript scrolls sideways" : "";
    });
    assert(wide === "", `a wide picture breaks the overlay: ${wide}`);
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
    // scroll inside its own bubble, never take the overlay sideways with it.
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
    assert(wide === "", `a code block wider than the screen breaks the overlay: ${wide}`);
    await closePanel(mp);
  });

  await run("a memory is saved and listed in the overlay's own view", async () => {
    await openAssistant(page);
    await openView(page, "memory", "dc-assistant-panel #memory-new");
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
    return "saved without leaving the overlay";
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

  await run("a new conversation starts empty and reuses an untouched one", async () => {
    await openAssistant(page);
    freshID = await newConversation(page);
    assert(freshID !== chatID, "new conversation did not open");
    assert(await page.locator("[data-assistant-empty]").isVisible(), "new conversation is not empty");

    const again = await newConversation(page);
    assert(again === freshID, "an untouched conversation was not reused");

    await openView(page, "history", "dc-assistant-panel [data-assistant-conversation]");
    const rows = await page.locator("[data-assistant-conversation]").count();
    assert(rows >= 2, `history holds ${rows} conversations, expected at least 2`);
    assert(await page.locator(`[data-assistant-conversation] .bg-purple-lt`).count() === 1,
      "the history does not mark the current conversation");
  });

  // Runs right after the check above, which left a newer conversation behind:
  // the run's own conversation is history now, and history stays history, so
  // this is where the run adopts the live one for everything that follows.
  await run("an earlier conversation opens read-only inside the overlay", async () => {
    await openConversation(page, chatID);
    assert((await page.locator("dc-assistant-panel [data-assistant-input]").count()) === 0,
      "an earlier conversation still has a composer");
    assert((await page.locator("[data-assistant-retry]").count()) === 0, "an earlier conversation still offers a retry");
    const link = page.locator("[data-assistant-current]");
    assert(await link.isVisible(), "no link to the current conversation");

    const status = await page.evaluate(async (id) => {
      const token = document.querySelector('meta[name="csrf-token"]')?.content || "";
      const res = await fetch(`/assistant/${id}`, {
        method: "POST",
        headers: { "Content-Type": "application/x-www-form-urlencoded", Accept: "application/json", "X-CSRF-Token": token },
        body: "form=message&message=this+must+not+land",
      });
      return res.status;
    }, chatID);
    assert(status === 400, `a message to an earlier conversation answered ${status}`);

    // The way back stays inside the overlay: no navigation, the live chat.
    await link.click();
    await page.waitForFunction(
      (gone) => document.querySelector("dc-assistant")?.getAttribute("conversation-id") !== gone
        && document.querySelector("dc-assistant")?.hasAttribute("ready"),
      chatID,
      { timeout: 15000 },
    );
    assert(new URL(page.url()).pathname === "/projects", "the way back navigated the page");
    assert((await page.locator("dc-assistant").getAttribute("conversation-id")) === freshID,
      "the way back did not open the current conversation");
    assert((await page.locator("dc-assistant-panel [data-assistant-input]").count()) === 1,
      "the current conversation has no composer");
    chatID = freshID;
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

  await run("a message sent while an answer runs queues and flushes after it", async () => {
    await send(page, "SLOW again please");
    await page.waitForSelector("[data-assistant-cancel]:not(.d-none)", { timeout: 10000 });
    const composer = await page.locator("[data-assistant-input]").evaluate((el) => ({ readOnly: el.readOnly, disabled: el.disabled }));
    assert(!composer.readOnly && !composer.disabled, "the composer locked during a turn");
    assert(await page.locator("[data-assistant-send]").isVisible(), "the send button is gone during a turn");
    const before = await page.locator('[data-role="assistant"]').count();
    await page.fill("[data-assistant-input]", "MAGIC after the queue");
    await page.click("[data-assistant-send]");
    await page.waitForSelector('[data-assistant-message][data-state="queued"] [data-assistant-queued]', { timeout: 10000 });
    assert((await page.locator('[data-assistant-message][data-state="queued"]').count()) === 1,
      "the queued message renders more than one bubble");
    await page.click("[data-assistant-cancel]");
    await page.waitForFunction(
      (count) => document.querySelectorAll('[data-role="assistant"]').length > count,
      before,
      { timeout: 20000 },
    );
    await waitSettled(page);
    assert((await page.locator('[data-assistant-message][data-state="queued"]').count()) === 0,
      "the queued mark did not clear after the flush");
    const text = await page.locator('[data-role="assistant"]').last().innerText();
    assert(text.includes("FLUGHAFEN"), `the queued message was not answered: ${text}`);
  });

  await run("a queued message can be taken back before it goes out", async () => {
    await send(page, "SLOW once more");
    await page.waitForSelector("[data-assistant-cancel]:not(.d-none)", { timeout: 10000 });
    await page.fill("[data-assistant-input]", "MAGIC do not send this");
    await page.click("[data-assistant-send]");
    await page.waitForSelector("[data-assistant-discard]", { timeout: 10000 });
    const answers = await page.locator('[data-role="assistant"]').count();
    await page.click("[data-assistant-discard]");
    await page.waitForFunction(
      () => !document.querySelector('[data-assistant-message][data-state="queued"]'),
      null,
      { timeout: 10000 },
    );
    await page.click("[data-assistant-cancel]");
    await waitSettled(page);
    await sleep(1500);
    assert((await page.locator('[data-role="assistant"]').count()) === answers,
      "a turn ran for the message that was taken back");
    const text = await page.locator('[data-role="assistant"]').last().innerText();
    assert(!text.includes("FLUGHAFEN"), "the message that was taken back was answered anyway");
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

  await run("the assistant conversation is never listed as a resumable coder", async () => {
    await closePanel(page);
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    const body = await page.locator("body").innerText();
    assert(!body.includes(chatID), "the assistant conversation showed up as a coder");
  });

  // On a desktop the assistant docks next to the page instead of leaving it:
  // the corner button (which replaces the floating quick nav there) opens it,
  // the page shifts aside, the surface loads the live conversation, a
  // navigation keeps the panel (it lives outside the swapped region), and
  // Escape closes it.
  await run("on a desktop the assistant docks as a panel and survives navigation", async () => {
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    assert(!(await page.locator(".navbar-nav a[href='/assistant']").count()),
      "the assistant still sits in the main navigation");
    assert(await page.locator(".quicknav-toggle").isHidden(), "the quick nav still shows on a desktop");
    await page.click("[data-assistant-corner]");
    await page.waitForSelector(READY, { timeout: 15000 });
    assert(new URL(page.url()).pathname === "/projects", `the corner button left the page: ${page.url()}`);
    const docked = await page.evaluate(() => ({
      body: document.body.classList.contains("dc-assistant-docked"),
      margin: getComputedStyle(document.querySelector(".page")).marginRight,
      composer: Boolean(document.querySelector("dc-assistant-panel [data-assistant-input]")),
    }));
    assert(docked.body && docked.margin !== "0px", `the page did not make room: ${JSON.stringify(docked)}`);
    assert(docked.composer, "the panel has no composer");

    const bands = await page.evaluate(() => {
      const bar = document.querySelector("#navbar-menu .navbar").getBoundingClientRect();
      const head = document.querySelector("[data-assistant-head]").getBoundingClientRect();
      return { barBottom: Math.round(bar.bottom), headBottom: Math.round(head.bottom), barHeight: Math.round(bar.height) };
    });
    assert(Math.abs(bands.barBottom - bands.headBottom) <= 1 && Math.abs(bands.barHeight - 56) <= 1,
      `the panel head does not line up with the page header: ${JSON.stringify(bands)}`);

    // The panel sits next to the swapped region, so a boosted navigation does
    // not touch it: the same element stays, nothing is torn down and refetched.
    await page.evaluate(() => { document.querySelector("dc-assistant-panel").dataset.runnerMark = "1"; });
    await page.click('.navbar-nav a[href="/docs"]');
    await page.waitForFunction(() => window.location.pathname === "/docs", null, { timeout: 15000 });
    assert(await page.locator(".dc-assistant-panel-card:not([hidden]) dc-assistant").count() === 1,
      "the panel did not survive a boosted navigation");
    assert(await page.evaluate(() => document.querySelector("dc-assistant-panel")?.dataset.runnerMark === "1"),
      "the panel was rebuilt by the navigation instead of surviving it");
    assert(await page.locator("[data-assistant-corner]").isHidden(), "the corner button shows next to the open panel");

    await page.focus("dc-assistant-panel [data-assistant-input]");
    await page.keyboard.press("Escape");
    await page.waitForSelector(".dc-assistant-panel-card[hidden]", { state: "attached", timeout: 8000 });
    assert(!(await page.evaluate(() => document.body.classList.contains("dc-assistant-docked"))),
      "closing the panel left the page shifted aside");
    return "docked, kept across pages, closed with Escape";
  });

  // Below lg the same surface covers the whole screen instead of docking, and
  // every entry opens it in place: the header sparkle and the quick nav row.
  await run("on a phone the assistant opens as a fullscreen overlay", async () => {
    const mp = await mobilePage();
    await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(mp);
    await mp.click("header.d-md-none [data-assistant-link]");
    await mp.waitForSelector(READY, { timeout: 15000 });
    assert(mp.url().endsWith("/projects"), "the sparkle button left the page");
    const covers = await mp.evaluate(() => {
      const card = document.querySelector(".dc-assistant-panel-card");
      const r = card.getBoundingClientRect();
      return r.width >= window.innerWidth - 1 && r.height >= window.innerHeight - 1;
    });
    assert(covers, "the overlay does not cover the screen");
    const overflow = await mp.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    assert(overflow <= 0, `horizontal overflow of ${overflow}px on a phone`);
    await closePanel(mp);

    await mp.click(".quicknav-toggle");
    await mp.waitForSelector("[data-quicknav-assistant]", { state: "visible", timeout: 8000 });
    await mp.click("[data-quicknav-assistant]");
    await mp.waitForSelector(READY, { timeout: 15000 });
    await closePanel(mp);
    return "sparkle and quick nav both open it in place";
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
      page.waitForResponse((r) => r.request().method() === "POST" && r.url().includes(`/assistant/${chatID}`), { timeout: 20000 }),
      page.click("[data-assistant-send]"),
    ]);
    // Close before the answer lands: an open surface reads its own news, so
    // the marks only have something to show once it is out of sight.
    await closePanel(page);
    await page.waitForFunction(
      (id) => {
        const marks = [...document.querySelectorAll(`[data-notify-target="${id}"]`)];
        return marks.length >= 1 && marks.some((m) => m.classList.contains("news"));
      },
      chatID,
      { timeout: 20000 },
    );
    assert(await page.locator(`header a[data-assistant-link] [data-notify-target="${chatID}"]`).count() === 2,
      "the header sparkles carry no news mark");
    assert(await page.locator(`[data-assistant-corner] [data-notify-target="${chatID}"]`).count() === 1,
      "the corner button carries no news mark");
    const fragment = await page.evaluate(async () => {
      const res = await fetch("/quicknav?path=/projects", { headers: { Accept: "text/html" } });
      return res.text();
    });
    assert(fragment.includes(`data-notify-target="${chatID}"`), "the refreshed quick nav drops the mark");
  });

  await run("the notification opens the overlay on the answer it announces", async () => {
    const entry = await page.evaluate(async (id) => {
      const res = await fetch("/notifications", { headers: { Accept: "application/json" } });
      const data = await res.json();
      return (data.notifications || []).find((n) => n.targetId === id) || null;
    }, chatID);
    assert(entry, "no notification for the conversation");
    assert(entry.title === "Assistant answered.", `the title does not name the assistant: ${entry.title}`);
    // The title only says that an answer arrived, so the line below it carries
    // the first words of it, which is what the list, the toast and the phone show.
    assert((entry.detail || "").length > 0, "the entry carries no words of the answer");
    assert(!entry.detail.includes("\n") && [...entry.detail].length <= 141,
      `the detail is not one short line: ${entry.detail}`);
    assert(entry.url.includes(`?assistant=${chatID}`), `the entry does not carry the overlay query: ${entry.url}`);
    const answered = entry.url.split("#message-")[1];
    assert(answered, `the entry does not link at a message: ${entry.url}`);

    // An early answer, so the jump has somewhere to go: the newest one sits at
    // the end of the transcript, where an un-anchored open lands anyway.
    await openConversation(page, chatID);
    const first = await page.locator('[data-role="assistant"]').first().getAttribute("data-message-id");
    assert(first !== answered, "the transcript is too short for this check");
    await closePanel(page);

    await page.goto(`${BASE}/projects?assistant=${chatID}#message-${first}`, { waitUntil: "domcontentloaded" });
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
      `the overlay did not land on the message (${JSON.stringify(placed)})`);
    assert(new URL(page.url()).search === "", "the consumed query is still in the address");
  });

  await run("a bell entry click opens the overlay in place", async () => {
    // The notification center hands an assistant link to the panel instead of
    // navigating: the page stays, the dropdown closes, the overlay opens on
    // the announced conversation and message.
    await closePanel(page);
    const entry = await page.evaluate(async (id) => {
      const res = await fetch("/notifications", { headers: { Accept: "application/json" } });
      const data = await res.json();
      return (data.notifications || []).find((n) => n.targetId === id) || null;
    }, chatID);
    assert(entry, "no notification for the conversation");
    const answered = entry.url.split("#message-")[1];
    assert(answered, `the entry does not link at a message: ${entry.url}`);
    const before = page.url();
    await page.evaluate(() => { window.__inPlaceProbe = true; });
    await page.locator(".dc-notify-bell:visible").first().click();
    await page.waitForSelector(".dc-notify-menu.show", { timeout: 6000 });
    await page.locator(`.dc-notify-menu.show a[data-notify-target="${chatID}"]`).first().click();
    await page.waitForSelector(READY, { timeout: 15000 });
    assert(await page.evaluate(() => window.__inPlaceProbe === true), "the click navigated, probe lost");
    assert(page.url() === before, `the address changed: ${page.url()}`);
    assert((await page.locator("dc-assistant").getAttribute("conversation-id")) === chatID,
      "the overlay is not on the announced conversation");
    await page.waitForSelector(`[data-message-id="${answered}"]`, { state: "attached", timeout: 8000 });
    await page.waitForSelector(".dc-notify-menu.show", { state: "detached", timeout: 4000 });
  });

  await run("opening the overlay lands at the end of the transcript", async () => {
    await closePanel(page);
    await openAssistant(page);
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
    await openAssistant(page);
    // The window is 20 messages, so the transcript is pushed past it.
    for (let i = 0; (await page.locator("[data-assistant-message]").count()) <= 22 && i < 12; i += 1) {
      await send(page, `MAGIC turn ${i}`);
      await waitSettled(page);
    }
    await closePanel(page);
    await openAssistant(page);
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
    assert(new URL(page.url()).pathname === "/projects", "showing the rest navigated the page");
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

  await run("the history and memory lists serve their own fragments", async () => {
    for (const path of ["/assistant/history", "/assistant/memory", "/assistant/jobs"]) {
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
    await openAssistant(page);
    const badge = page.locator("dc-assistant-panel [data-assistant-jobs-button] .dc-steer-badge");
    assert(await badge.count() === 1, "no badge on the jobs button in the panel head");
    assert((await badge.first().getAttribute("class")).includes("d-none"),
      "the badge claims a steered job before there is one");
    assert((await page.locator("dc-assistant-panel [data-assistant-jobs-button]").getAttribute("title")) === "Steered coders",
      "the jobs button does not say Steered coders");

    const created = await startCoder(page, jobsDir, "jobs-task", "Write the README.");
    assert(created.status === 200, `create answered ${created.status}`);
    jobCoder = created.body.id;
    const steered = await postJobs(page, {
      form: "steer",
      terminal: jobCoder,
      task: "Write the README",
      done_when: "WAKE_NOTHING: never true",
    });
    assert(steered.status === 200, `steer answered ${steered.status}: ${JSON.stringify(steered.body)}`);

    // Live, over the assistant event: no reload, no polling.
    await page.waitForFunction(
      () => {
        const mark = document.querySelector("dc-assistant-panel [data-assistant-jobs-button] .dc-steer-badge");
        return mark && !mark.classList.contains("d-none") && Number(mark.textContent) > 0;
      },
      null,
      { timeout: 15000 },
    );

    await page.click("dc-assistant-panel [data-assistant-jobs-button]");
    await page.waitForFunction(
      (id) => (document.querySelector(`[data-assistant-job="${id}"]`)?.innerText || "").includes("steering"),
      jobCoder,
      { timeout: 15000 },
    );
    assert((await page.locator(".modal.show").count()) === 0, "the jobs view opened a modal");
    assert((await page.locator(".modal-backdrop").count()) === 0, "the jobs view left a backdrop");
    // The panel speaks of coders, the word job stays in the code and the CLI.
    const head = await page.locator("dc-assistant-panel [data-assistant-head]").innerText();
    assert(head.includes("Steered coders"), `the jobs view is not headed Steered coders: ${head}`);
    const text = await page.locator(`[data-assistant-job="${jobCoder}"]`).innerText();
    for (const want of ["jobs-task", "steering", "WAKE_NOTHING", "Write the README", "0 of"]) {
      assert(text.includes(want), `the job row misses ${want}:\n${text}`);
    }
    // One row per terminal. The list is the assistant's, not the conversation's,
    // so it also carries what earlier runs on this instance left behind.
    assert(await page.locator(`[data-assistant-job="${jobCoder}"]`).count() === 1,
      "the job is listed twice");
    return "one list, a view of the overlay";
  });

  await run("stopping a job asks first and the view acts in place", async () => {
    const row = page.locator(`[data-assistant-job="${jobCoder}"]`);
    await row.locator("[data-assistant-job-stop]").click();
    await page.waitForSelector(".swal2-container", { state: "visible", timeout: 8000 });
    await page.click(".swal2-cancel");
    await page.waitForSelector(".swal2-container", { state: "detached", timeout: 8000 });
    assert((await row.locator("[data-assistant-job-state]").innerText()).trim() === "steering",
      "a cancelled confirmation stopped the job anyway");

    await row.locator("[data-assistant-job-stop]").click();
    await L.confirmSwal(page);
    await page.waitForFunction(
      (id) => document.querySelector(`[data-assistant-job="${id}"] [data-assistant-job-state]`)?.textContent.trim() === "stopped",
      jobCoder,
      { timeout: 15000 },
    );
    assert(await page.locator(".dc-assistant-panel-card:not([hidden])").count() === 1, "the overlay closed under the action");
    return "confirmed, stopped in place";
  });

  await run("a stopped job can be steered again with the same criterion", async () => {
    const row = page.locator(`[data-assistant-job="${jobCoder}"]`);
    await row.locator("[data-assistant-job-again]").click();
    await page.waitForFunction(
      (id) => document.querySelector(`[data-assistant-job="${id}"] [data-assistant-job-state]`)?.textContent.trim() === "steering",
      jobCoder,
      { timeout: 15000 },
    );
    const text = await row.innerText();
    assert(text.includes("WAKE_NOTHING"), `the criterion did not survive: ${text}`);
    assert(text.includes("0 of"), `the budget was not given back: ${text}`);
    assert(await row.locator("[data-assistant-job-open]").count() === 1, "no way from the job to its coder");
    return "steering again, same criterion";
  });

  await run("on a phone the jobs view is thumb sized", async () => {
    const mp = await mobilePage();
    await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(mp);
    await mp.click("header.d-md-none [data-assistant-link]");
    await mp.waitForSelector(READY, { timeout: 15000 });
    await mp.click("dc-assistant-panel [data-assistant-jobs-button]");
    await mp.waitForSelector(`[data-assistant-job="${jobCoder}"]`, { timeout: 15000 });
    await sleep(400);
    const fit = await mp.evaluate((id) => {
      const stop = document.querySelector(`[data-assistant-job="${id}"] [data-assistant-job-stop]`);
      if (!stop) return "the action is missing";
      const b = stop.getBoundingClientRect();
      if (b.height < 36) return `the action is ${b.height} high`;
      if (b.right > window.innerWidth + 1 || b.left < -1) return `the action sticks out: ${JSON.stringify(b)}`;
      return "";
    }, jobCoder);
    assert(fit === "", `the view is not usable with a thumb: ${fit}`);
    return "full width, reachable";
  });

  // A job's coder is the one link that really leaves the overlay. On a phone
  // the overlay is a visit, so it closes with the navigation, and the way
  // back is one tap on the sparkle.
  await run("the way from a job to its coder leaves and comes back clean", async () => {
    const mp = await mobilePage();
    await mp.click(`[data-assistant-job="${jobCoder}"] [data-assistant-job-open]`);
    await mp.waitForURL((url) => url.pathname.includes(jobCoder), { timeout: 15000 });
    await sleep(400);
    assert(await mp.evaluate(() => document.querySelector(".dc-assistant-panel-card")?.hasAttribute("hidden")),
      "the overlay stayed over the coder page");
    await mp.click("header.d-md-none [data-assistant-link]");
    await mp.waitForSelector(READY, { timeout: 15000 });
    await closePanel(mp);
    return "coder page usable, one tap back";
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

  await run("a job survives the conversation it was started from", async () => {
    // The job of jobCoder is steering. Jobs belong to the assistant, not to one
    // conversation, so deleting the conversation leaves it alone: the next
    // conversation lists the same job and its reports land there.
    const conversation = await openAssistant(page);
    await openView(page, "history", "dc-assistant-panel [data-assistant-conversation]");
    await page.click(`[data-assistant-conversation="${conversation}"] [data-conversation-menu]`);
    await page.waitForSelector(".dc-context-menu", { timeout: 8000 });
    await page.click('.dc-context-menu .dropdown-item:has-text("Delete")');
    await L.confirmSwal(page);
    await page.waitForSelector(`[data-assistant-conversation="${conversation}"]`, { state: "detached", timeout: 10000 });

    await openView(page, "jobs", `dc-assistant-panel [data-assistant-job="${jobCoder}"]`);
    const state = await page.locator(`[data-assistant-job="${jobCoder}"] [data-assistant-job-state]`).innerText();
    assert(state.trim() === "steering",
      `the job did not survive the deleted conversation: ${state}`);
    return "job kept by the assistant";
  });

  // The one steered indicator is the coder icon itself turning purple,
  // rendered on the server and riding the terminals event: it appears with
  // the job and goes with the release, on pages that are never reloaded here.
  // The steer itself comes from the page without a criterion, which the page
  // path allows.
  await run("a steered coder's icon turns purple and release takes it back", async () => {
    await openAssistant(page);
    const dir = await L.projectPath(page, jobsProject);
    const created = await startCoder(page, dir, "mark-task", "Write the file.");
    assert(created.status === 200, `create answered ${created.status}`);
    const marked = created.body.id;
    const steered = await postJobs(page, { form: "steer", terminal: marked, task: "Write the file", done_when: "" });
    assert(steered.status === 200, `steer answered ${steered.status}: ${JSON.stringify(steered.body)}`);

    await closePanel(page);
    await page.goto(`${BASE}/coders/${marked}`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    // The mark sits on every surface that lists the coder, so the selector is
    // scoped to the one that is on screen here: the tab strip. Unscoped it
    // resolves to four nodes and the first is the attach page's own badge, which
    // lives in the settings row that style.css hides on a fine pointer
    // (`@media not (pointer: coarse)`), so a wait for it to become visible can
    // never pass on a desktop viewport. The others are checked as rendered.
    const mark = `.terminal-tabs-strip .dc-term-icon.steered[data-notify-target="${marked}"]`;
    await page.waitForSelector(mark, { timeout: 10000 });
    assert(await page.locator(`.attach-settings .dc-term-icon.steered[data-notify-target="${marked}"]`).count() === 1,
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

  await run("deleting a conversation through its menu keeps the assistant reachable", async () => {
    const conversation = await openAssistant(page);
    await openView(page, "history", "dc-assistant-panel [data-assistant-conversation]");
    await page.click(`[data-assistant-conversation="${conversation}"] [data-conversation-menu]`);
    await page.waitForSelector(".dc-context-menu", { timeout: 8000 });
    const items = await page.locator(".dc-context-menu .dropdown-item").allInnerTexts();
    assert(items.some((t) => t.includes("Open")) && items.some((t) => t.includes("Delete")),
      `the row menu misses an action: ${items.join(", ")}`);
    await page.click('.dc-context-menu .dropdown-item:has-text("Delete")');
    await L.confirmSwal(page);
    await page.waitForSelector(`[data-assistant-conversation="${conversation}"]`, { state: "detached", timeout: 10000 });
    assert(new URL(page.url()).pathname === "/projects", "the deletion navigated away");

    // The chat comes back with a fresh conversation, the overlay never dies.
    await page.click('dc-assistant-panel [data-assistant-view-open="chat"]');
    await page.waitForSelector(READY, { timeout: 15000 });
    assert((await page.locator("dc-assistant").getAttribute("conversation-id")) !== conversation,
      "the deleted conversation is still the live one");
    assert(await page.locator("dc-assistant-panel [data-assistant-input]").isVisible(), "no composer after the deletion");
    await closePanel(page);
  });

  // A history row opens its conversation read-only inside the overlay through
  // the row itself; the menu's Open does the same.
  await run("a history row opens its transcript in place", async () => {
    await openAssistant(page);
    await openView(page, "history", "dc-assistant-panel [data-assistant-conversation]");
    const row = page.locator("dc-assistant-panel [data-assistant-conversation]:not(:has(.bg-purple-lt))").first();
    const victim = await row.getAttribute("data-assistant-conversation").catch(() => null);
    assert(victim, "no archived row to open");
    await row.locator("a").click();
    await page.waitForSelector("dc-assistant-panel [data-assistant-current]", { timeout: 15000 });
    assert(new URL(page.url()).pathname === "/projects", "opening a history row navigated the page");
    assert((await page.locator("dc-assistant").getAttribute("conversation-id")) === victim,
      "the overlay shows a different conversation than the row named");
    await closePanel(page);
    return "read only, in place";
  });

  await run("a prompt keeps its line breaks in the optimistic bubble", async () => {
    await openAssistant(page);
    if (!(await page.locator("[data-assistant-input]").count())) {
      await page.click('dc-assistant-panel [data-assistant-view-open="chat"]');
      await page.waitForSelector(READY, { timeout: 15000 });
    }
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
    await openAssistant(page);
    if (!(await page.locator("[data-assistant-input]").count())) {
      await page.click('dc-assistant-panel [data-assistant-view-open="chat"]');
      await page.waitForSelector(READY, { timeout: 15000 });
    }
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
    await openAssistant(page);
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

  await run("the composer resists resizing while the other overlay boxes allow it", async () => {
    const composer = await page.locator("[data-assistant-input]")
      .evaluate((n) => getComputedStyle(n).resize);
    assert(composer === "none", `the composer grew a resize handle: ${composer}`);
    await openView(page, "memory", "dc-assistant-panel #memory-new");
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
    await openAssistant(page);
    const box = "dc-assistant-panel [data-assistant-input]";
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
  await run("a failed panel load keeps a close control and retry recovers", async () => {
    await closePanel(page);
    await page.route("**/assistant/panel*", (route) => route.abort());
    await page.click("[data-assistant-corner]");
    await page.waitForSelector(".dc-assistant-panel-card [data-assistant-panel-retry]", { timeout: 8000 });
    assert(await page.locator(".dc-assistant-panel-card [data-assistant-panel-close]").isVisible(),
      "the error state has no close control");
    await page.unroute("**/assistant/panel*");
    await page.click(".dc-assistant-panel-card [data-assistant-panel-retry]");
    await page.waitForSelector(READY, { timeout: 15000 });
    await closePanel(page);
    return "close there, retry recovers";
  });

  await run("a slow panel load shows a spinner before the content arrives", async () => {
    await page.route("**/assistant/panel*", async (route) => {
      await sleep(700);
      await route.continue();
    });
    await page.click("[data-assistant-corner]");
    await page.waitForSelector('.dc-assistant-panel-card [aria-label="Loading the assistant"]', { timeout: 4000 });
    await page.unroute("**/assistant/panel*");
    await page.waitForSelector(READY, { timeout: 15000 });
    await closePanel(page);
  });
});
