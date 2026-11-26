const L = require("./lib");
const { assert, BASE, sleep, dismissUpdate } = L;

// Compacting an assistant's conversation from the ring's menu (POST
// /assistants/:id with form=compact), on a phone first: Compact conversation
// stands under the models with how full the context is, off before the first
// answer and while one runs with its reason under it, on Claude, Codex,
// Copilot and OpenCode alike. A tap asks first, Cancel compacts nothing. The compaction
// runs like a turn: its line in the thread, Compacting the conversation in the
// running light of a working coder while it runs, then in the same place
// Conversation compacted, Nothing to compact, or The conversation was not
// compacted with the coder's words, never an answer bubble. A message sent
// meanwhile waits and goes out after it. Claude's, Codex's and OpenCode's ring
// turn dashed until the next answer reads it, Copilot's shows the compacted
// size at once. A restart of the cockpit while it compacts leaves one line that
// ends normally on the open page, and OpenCode's server is gone once it
// ended. A guest reads the line and gets no control.
//
// Instance: the assistant shape of the README (tests/e2e/fakes ahead of the
// real CLIs on PATH, a scratch HOME). The claude and copilot fakes compact on
// a prompt that is exactly /compact, the opencode fake on the summarize of its
// server, the codex fake on thread/compact/start of its app server, and all of
// them read the prompt of the turn before: COMPACT_LONG, COMPACT_RESTART,
// COMPACT_EMPTY (claude, copilot) and COMPACT_REFUSE. codex itself compacts
// whatever it is given, so the cockpit finds nothing to compact where its
// record holds no answer since the last compaction, and asks codex nothing;
// the codex fake's COMPACTIONS says how many compactions reached it on which
// model, whether its record holds a summary and how many of its app servers
// still run.
// SERVE_LEFT makes the opencode fake count its servers still running. A
// compaction OpenCode did not finish, refused or stopped, must not cost the
// next message: the fake finishes it in place of the answer, as opencode
// does, unless the cockpit cleared it. COMPACT_KILL_HELPER kills the opencode
// helper inside summarize and CLEAR_RESTART restarts the cockpit while a turn
// clears what that left; neither may leave a server. A stopped copilot
// compaction keeps its gauge, a second claude compaction in a row reads
// Nothing to compact, and a Compact confirmed while another tab starts a turn
// is refused with the server's reason. The restart checks kill and restart
// the instance, point this runner at a throwaway only.

const READY = "dc-assistant[ready]";
const RING = 'dc-assistant [data-bs-toggle="dropdown"][data-assistant-new-label]';
const COMPACT = "dc-assistant button[data-assistant-compact]";
const LINE = "[data-assistant-message][data-assistant-compaction]";
const PENDING = "Compacted, the next answer shows how full the context is.";

async function csrfOf(page) {
  return page.evaluate(() => document.querySelector('meta[name="csrf-token"]')?.content || "");
}

async function newAssistant(page, coder) {
  await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
  const created = await page.request.post(`${BASE}/assistants/new`, { form: { csrf_token: await csrfOf(page), form: "new", coder }, headers: { Accept: "application/json" } });
  const id = (await created.json().catch(() => ({}))).id || "";
  assert(id, `no ${coder} assistant: ${created.status()}`);
  return id;
}

async function open(page, id) {
  await page.goto(`${BASE}/assistants/${id}`, { waitUntil: "domcontentloaded" });
  await dismissUpdate(page);
  await page.waitForSelector(READY, { timeout: 15000 });
}

async function working(page) {
  return page.evaluate(() => Boolean(document.querySelector("dc-assistant")?.hasAttribute("running")));
}

async function idle(page, timeout = 30000) {
  await page.waitForFunction(() => !document.querySelector("dc-assistant")?.hasAttribute("running"), null, { timeout });
}

// send types a message, taps send and waits for its answer to settle.
async function send(page, text) {
  await idle(page);
  const before = await page.locator('[data-role="assistant"]').count();
  await page.fill("[data-assistant-input]", text);
  await page.locator("[data-assistant-send]").tap();
  await page.waitForFunction((count) => {
    const answers = document.querySelectorAll('[data-role="assistant"]');
    return answers.length > count && answers[answers.length - 1].getAttribute("data-state") !== "streaming";
  }, before, { timeout: 30000 });
  await idle(page);
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

async function openRing(page) {
  if (!(await shown(page, "dc-assistant .dc-model-menu.show"))) await page.locator(RING).tap();
  await page.waitForSelector("dc-assistant .dc-model-menu.show", { timeout: 8000 });
}

async function closeRing(page) {
  if (await shown(page, "dc-assistant .dc-model-menu.show")) await page.locator(RING).tap();
  await page.waitForSelector("dc-assistant .dc-model-menu.show", { state: "detached", timeout: 8000 }).catch(() => {});
}

// control reads the compact button and the text under it as the phone shows them.
async function control(page) {
  return page.evaluate(() => {
    const visible = (node) => {
      if (!node) return false;
      const style = getComputedStyle(node);
      const box = node.getBoundingClientRect();
      return style.display !== "none" && style.visibility !== "hidden" && box.width > 0 && box.height > 0;
    };
    const button = document.querySelector("dc-assistant button[data-assistant-compact]");
    const texts = (sel) => [...document.querySelectorAll(sel)].filter(visible).map((n) => n.textContent.trim()).join(" ");
    return {
      present: Boolean(button),
      shown: visible(button),
      disabled: Boolean(button?.disabled),
      hint: texts("dc-assistant [data-assistant-compact-hint] span"),
      context: texts("dc-assistant [data-assistant-context-line] > span"),
    };
  });
}

// compact taps the ring's Compact conversation and answers the question.
async function compact(page, confirm = true) {
  await openRing(page);
  await page.locator(COMPACT).tap();
  await page.waitForSelector(".swal2-popup:not(.swal2-toast)", { state: "visible", timeout: 8000 });
  const asked = await page.locator(".swal2-popup:not(.swal2-toast)").evaluate((popup) => ({
    title: popup.querySelector(".dc-ask-title")?.textContent.trim() || "",
    text: popup.querySelector(".dc-ask-body")?.textContent.replace(/\s+/g, " ").trim() || "",
    confirm: popup.querySelector(".swal2-confirm")?.textContent.trim() || "",
  }));
  await sleep(150);
  await page.locator(confirm ? ".swal2-confirm" : ".swal2-cancel").tap();
  await page.waitForSelector(".swal2-popup:not(.swal2-toast)", { state: "detached", timeout: 8000 });
  assert(asked.title === "Compact this conversation?" && asked.confirm === "Compact" && asked.text.includes("cannot be undone"), `the question reads ${JSON.stringify(asked)}`);
}

// lines reads each compaction line, with whether its icon wears the running
// light of a working coder (the box shown and its segment moving) and where
// its words stand, so running and ended can be held against each other.
async function lines(page) {
  return page.$$eval(LINE, (nodes) => nodes.map((n) => {
    const icon = n.querySelector(".dc-term-icon.working");
    const box = icon?.getBoundingClientRect();
    const words = n.querySelector("[data-assistant-compact-line]");
    return {
      mark: n.getAttribute("data-assistant-compaction"),
      line: words?.textContent.trim() || "",
      error: n.querySelector("[data-assistant-error]")?.textContent.trim() || "",
      role: n.getAttribute("data-role"),
      light: Boolean(icon) && getComputedStyle(icon).visibility !== "hidden" && box.width > 0 && box.height > 0 && getComputedStyle(icon, "::before").animationName === "dc-run",
      spinner: Boolean(n.querySelector(".dc-icon-spinner")),
      at: words ? [Math.round(words.getBoundingClientRect().left * 100) / 100, Math.round(n.getBoundingClientRect().height * 100) / 100] : [],
    };
  }));
}

async function waitLine(page, count, mark, timeout = 30000) {
  await page.waitForFunction(({ sel, count, mark }) => {
    const nodes = document.querySelectorAll(sel);
    return nodes.length === count && nodes[count - 1].getAttribute("data-assistant-compaction") === mark;
  }, { sel: LINE, count, mark }, { timeout });
  return (await lines(page))[count - 1];
}

async function ring(page) {
  return page.evaluate(() => {
    const svg = document.querySelector("dc-assistant [data-assistant-ring]");
    return {
      fill: svg?.querySelector("[data-assistant-ring-fill]")?.getAttribute("stroke-dasharray") || "",
      pending: Boolean(svg?.hasAttribute("data-assistant-ring-pending")),
      dashed: svg ? getComputedStyle(svg.querySelector(".dc-context-ring-track")).strokeDasharray : "",
      label: document.querySelector('dc-assistant [data-bs-toggle="dropdown"][data-assistant-new-label]')?.getAttribute("aria-label") || "",
    };
  });
}

L.runFeature("assistant-compact", async ({ browser, run, mobilePage }) => {
  const page = await mobilePage();
  const made = [];
  let claude = "";
  let opencode = "";
  let codex = "";
  let linkId = "";
  try {
    await checks();
  } finally {
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" }).catch(() => {});
    if (linkId) await post(page, `/settings/sharing/${linkId}`, { form: "revoke" }).catch(() => {});
    for (const id of made) await post(page, `/assistants/${id}`, { form: "delete" }).catch(() => {});
  }

  async function checks() {
    await run("an assistant without an answer offers the compaction off, with its reason", async () => {
      claude = await newAssistant(page, "claude");
      made.push(claude);
      await open(page, claude);
      await openRing(page);
      const state = await control(page);
      assert(state.shown && state.disabled, `the control reads ${JSON.stringify(state)}`);
      assert(state.hint === "Possible after the first answer.", `the reason reads "${state.hint}"`);
      await closeRing(page);
      return state.hint;
    });

    await run("Cancel on the question compacts nothing", async () => {
      await send(page, "MAGIC");
      await openRing(page);
      const state = await control(page);
      assert(state.shown && !state.disabled && state.hint === "", `after an answer the control reads ${JSON.stringify(state)}`);
      assert(state.context === "Context 68 percent full.", `the context reads "${state.context}"`);
      await compact(page, false);
      await sleep(1500);
      assert((await lines(page)).length === 0, "a cancelled question compacted");
      assert(!(await working(page)), "a cancelled question started something");
      return "nothing";
    });

    await run("a confirmed compaction on claude shows its line and leaves the ring waiting for the next answer", async () => {
      const answers = await page.locator('[data-role="assistant"]').count();
      await compact(page);
      const running = await waitLine(page, 1, "streaming", 8000);
      assert(running.line === "Compacting the conversation…" && running.role === "cockpit" && running.light && !running.spinner, `while it runs the line reads ${JSON.stringify(running)}`);
      assert(await shown(page, "[data-assistant-cancel]"), "the stop button is not there while it compacts");
      await openRing(page);
      const busy = await control(page);
      assert(busy.disabled && busy.hint === "Possible once the assistant is done.", `while it compacts the control reads ${JSON.stringify(busy)}`);
      await closeRing(page);
      const done = await waitLine(page, 1, "compacted");
      await idle(page);
      assert(done.line === "Conversation compacted." && !done.error && !done.light, `the line reads ${JSON.stringify(done)}`);
      assert(JSON.stringify(done.at) === JSON.stringify(running.at), `the line moved when it ended: ${JSON.stringify(running.at)} then ${JSON.stringify(done.at)}`);
      assert((await page.locator('[data-role="assistant"]').count()) === answers, "the compaction wrote an answer bubble");
      let gauge = await ring(page);
      assert(gauge.pending && gauge.fill === "0 100" && gauge.dashed !== "none" && gauge.label.endsWith(PENDING), `the ring reads ${JSON.stringify(gauge)}`);
      await openRing(page);
      let state = await control(page);
      assert(state.context === PENDING && !state.disabled, `the menu reads ${JSON.stringify(state)}`);
      await closeRing(page);

      await open(page, claude);
      gauge = await ring(page);
      assert(gauge.pending && gauge.fill === "0 100" && (await lines(page)).length === 1, `after a reload the ring reads ${JSON.stringify(gauge)}`);

      await send(page, "MAGIC after the compaction");
      gauge = await ring(page);
      assert(!gauge.pending && gauge.fill === "68 100" && gauge.dashed === "none", `the next answer did not read the ring again: ${JSON.stringify(gauge)}`);
      await openRing(page);
      state = await control(page);
      assert(state.context === "Context 68 percent full.", `the menu after the next answer reads "${state.context}"`);
      await closeRing(page);
      const last = (await page.locator('[data-role="assistant"]').last().innerText()).trim();
      assert(last.includes("AIRPORT"), `the turn after the compaction answered ${last}`);
      return "line, dashed ring, next answer 68";
    });

    await run("a message sent while it compacts waits and goes out after it", async () => {
      await send(page, "MAGIC COMPACT_LONG");
      await compact(page);
      await waitLine(page, 2, "streaming", 8000);
      await page.fill("[data-assistant-input]", "MAGIC meanwhile");
      await page.locator("[data-assistant-send]").tap();
      await page.waitForFunction(() => [...document.querySelectorAll('[data-role="user"]')].some((n) => n.textContent.includes("MAGIC meanwhile") && n.querySelector("[data-assistant-queued]")), null, { timeout: 8000 });
      await waitLine(page, 2, "compacted", 30000);
      await page.waitForFunction(() => {
        const answers = document.querySelectorAll('[data-role="assistant"]');
        const last = answers[answers.length - 1];
        return last && last.getAttribute("data-state") === "complete" && last.textContent.includes("AIRPORT")
          && [...document.querySelectorAll('[data-role="user"]')].some((n) => n.textContent.includes("MAGIC meanwhile") && !n.querySelector("[data-assistant-queued]"));
      }, null, { timeout: 30000 });
      const order = await page.$$eval("[data-assistant-message]", (nodes) => nodes.slice(-3).map((n) => n.getAttribute("data-role") + ":" + (n.getAttribute("data-assistant-compaction") || n.textContent.replace(/\s+/g, " ").trim().slice(-20))));
      assert(order[0] === "cockpit:compacted" && order[1].startsWith("user:") && order[1].includes("MAGIC meanwhile") && order[2].startsWith("assistant:"), `the thread ends ${JSON.stringify(order)}`);
      return order.join(" | ");
    });

    await run("nothing to compact and a refusal each show their line and no answer", async () => {
      const answers = await page.locator('[data-role="assistant"]').count();
      await send(page, "MAGIC COMPACT_EMPTY");
      await compact(page);
      const nothing = await waitLine(page, 3, "nothing");
      assert(nothing.line === "Nothing to compact." && !nothing.error, `nothing reads ${JSON.stringify(nothing)}`);
      await idle(page);
      await send(page, "MAGIC COMPACT_REFUSE");
      await compact(page);
      const refused = await waitLine(page, 4, "failed");
      assert(refused.line === "The conversation was not compacted." && refused.error.includes("The coder said: Compaction failed: the summary was too long"), `the refusal reads ${JSON.stringify(refused)}`);
      assert((await page.locator('[data-role="assistant"]').count()) === answers + 2, "a compaction wrote an answer bubble");
      return `${nothing.line} / ${refused.error}`;
    });

    await run("a compaction survives a restart of the cockpit and ends once on the open page", async () => {
      await send(page, "MAGIC COMPACT_RESTART");
      await page.evaluate(() => { window.__dcSamePage = true; });
      await compact(page);
      const running = await waitLine(page, 5, "streaming", 15000);
      assert(running.light, `while it runs across the restart the line reads ${JSON.stringify(running)}`);
      const done = await waitLine(page, 5, "compacted", 60000);
      await idle(page, 30000);
      assert(await page.evaluate(() => window.__dcSamePage === true), "the page reloaded, this says nothing about the open stream");
      assert(done.line === "Conversation compacted." && !done.light, `after the restart the line reads ${JSON.stringify(done)}`);
      await openRing(page);
      const state = await control(page);
      assert(!state.disabled, `after the restart the control reads ${JSON.stringify(state)}`);
      await closeRing(page);
      await open(page, claude);
      const all = await lines(page);
      assert(all.length === 5 && all[4].mark === "compacted", `after a reload the lines read ${JSON.stringify(all)}`);
      assert(!(await working(page)), "the assistant still counts as working");
      await send(page, "MAGIC after the restart");
      return "one line, idle, next turn answers";
    });

    await run("a claude compaction right after another has nothing to compact, in claude 2.1.330's words", async () => {
      const id = await newAssistant(page, "claude");
      made.push(id);
      await open(page, id);
      await send(page, "MAGIC");
      await compact(page);
      const first = await waitLine(page, 1, "compacted");
      await idle(page);
      await compact(page);
      const second = await waitLine(page, 2, "nothing");
      await idle(page);
      assert(first.line === "Conversation compacted." && second.line === "Nothing to compact." && !second.error, `back to back reads ${JSON.stringify([first, second])}`);
      return `${first.line} / ${second.line}`;
    });

    await run("a Compact confirmed while another tab starts a turn is refused with its reason", async () => {
      const id = await newAssistant(page, "claude");
      made.push(id);
      await open(page, id);
      await send(page, "MAGIC");
      const tab2 = await page.context().newPage();
      try {
        await open(tab2, id);
        await openRing(tab2);
        await tab2.locator(COMPACT).tap();
        await tab2.waitForSelector(".swal2-popup:not(.swal2-toast)", { state: "visible", timeout: 8000 });
        await page.fill("[data-assistant-input]", "MAGIC PAUSE_CHAT");
        await page.locator("[data-assistant-send]").tap();
        await tab2.waitForFunction(() => document.querySelector("dc-assistant")?.hasAttribute("running"), null, { timeout: 15000 });
        await sleep(150);
        await tab2.locator(".swal2-confirm").tap();
        await tab2.waitForFunction(() => [...document.querySelectorAll(".toast")].some((t) => t.textContent.includes("still working")), null, { timeout: 8000 });
        const toast = await tab2.evaluate(() => [...document.querySelectorAll(".toast")].map((t) => t.textContent.replace(/\s+/g, " ").trim()).find((t) => t.includes("still working")));
        await idle(page, 40000);
        assert((await lines(page)).length === 0, "a refused compaction wrote a line");
        return toast;
      } finally {
        await tab2.close();
      }
    });

    await run("a stopped copilot compaction leaves its gauge as it was", async () => {
      const id = await newAssistant(page, "copilot");
      made.push(id);
      await open(page, id);
      await send(page, "MAGIC COMPACT_LONG");
      assert((await ring(page)).fill === "68 100", `copilot's ring before reads ${JSON.stringify(await ring(page))}`);
      await compact(page);
      await waitLine(page, 1, "streaming", 8000);
      await sleep(2000);
      await page.locator("[data-assistant-cancel]").tap();
      const stopped = await waitLine(page, 1, "cancelled", 20000);
      await idle(page);
      const onPage = await ring(page);
      await open(page, id);
      const reloaded = await ring(page);
      await openRing(page);
      const state = await control(page);
      await closeRing(page);
      assert(stopped.line === "You stopped the compaction." && onPage.fill === "68 100" && reloaded.fill === "68 100" && state.context === "Context 68 percent full.", `after the stop: ${JSON.stringify([stopped, onPage, reloaded, state.context])}`);
      return `${stopped.line} ${state.context}`;
    });

    await run("copilot shows the compacted size at once and nothing to compact as its line", async () => {
      const id = await newAssistant(page, "copilot");
      made.push(id);
      await open(page, id);
      await send(page, "MAGIC");
      assert((await ring(page)).fill === "68 100", `copilot's ring before reads ${JSON.stringify(await ring(page))}`);
      await compact(page);
      const done = await waitLine(page, 1, "compacted");
      await idle(page);
      const gauge = await ring(page);
      assert(done.line === "Conversation compacted." && !gauge.pending && gauge.fill === "30 100", `after copilot's compaction: ${JSON.stringify(done)} ${JSON.stringify(gauge)}`);
      await openRing(page);
      const state = await control(page);
      assert(state.context === "Context 30 percent full.", `copilot's menu reads "${state.context}"`);
      await closeRing(page);
      await send(page, "MAGIC COMPACT_EMPTY");
      await compact(page);
      const nothing = await waitLine(page, 2, "nothing");
      assert(nothing.line === "Nothing to compact." && !nothing.error, `copilot's nothing reads ${JSON.stringify(nothing)}`);
      return "30 percent, nothing";
    });

    await run("OpenCode compacts through its server, leaves its ring waiting and no server running", async () => {
      opencode = await newAssistant(page, "opencode").catch(() => "");
      assert(opencode, "this instance runs no opencode, put tests/e2e/fakes on the PATH");
      made.push(opencode);
      await open(page, opencode);
      await openRing(page);
      const first = await control(page);
      assert(first.shown && first.disabled && first.hint === "Possible after the first answer.", `opencode's control before an answer reads ${JSON.stringify(first)}`);
      await closeRing(page);
      await send(page, "MAGIC");
      assert((await ring(page)).fill === "68 100", `opencode's ring before reads ${JSON.stringify(await ring(page))}`);
      const answers = await page.locator('[data-role="assistant"]').count();
      await compact(page);
      const running = await waitLine(page, 1, "streaming", 8000);
      assert(running.line === "Compacting the conversation…", `while opencode compacts the line reads ${JSON.stringify(running)}`);
      const done = await waitLine(page, 1, "compacted");
      await idle(page);
      const gauge = await ring(page);
      assert(done.line === "Conversation compacted." && !done.error && gauge.pending && gauge.fill === "0 100" && gauge.label.endsWith(PENDING), `after opencode's compaction: ${JSON.stringify(done)} ${JSON.stringify(gauge)}`);
      assert((await page.locator('[data-role="assistant"]').count()) === answers, "opencode's compaction wrote an answer bubble");
      await send(page, "MAGIC SERVE_LEFT");
      const last = (await page.locator('[data-role="assistant"]').last().innerText()).trim();
      const after = await ring(page);
      assert(last.includes("AIRPORT Servers left: 0."), `the turn after opencode's compaction answered ${last}`);
      assert(!after.pending && after.fill === "68 100", `the next answer read the summary or nothing: ${JSON.stringify(after)}`);
      return `${done.line} ${last}`;
    });

    await run("OpenCode shows a refused compaction with its words and nothing to compact without an answer", async () => {
      await send(page, "MAGIC COMPACT_REFUSE");
      await compact(page);
      const refused = await waitLine(page, 2, "failed");
      await idle(page);
      assert(refused.line === "The conversation was not compacted." && refused.error.includes("The coder said: UnknownError: Compaction failed: the summary was too long."), `opencode's refusal reads ${JSON.stringify(refused)}`);
      assert(!(await ring(page)).pending, "a refused compaction left the ring waiting");
      await send(page, "MAGIC after the refusal");
      const next = (await page.locator('[data-role="assistant"]').last().innerText()).trim();
      assert(next.includes("AIRPORT") && !next.includes("Summary"), `the turn after the refusal answered ${next}`);

      const empty = await newAssistant(page, "opencode");
      made.push(empty);
      await open(page, empty);
      await idle(page);
      await page.fill("[data-assistant-input]", "FAIL");
      await page.locator("[data-assistant-send]").tap();
      await page.waitForSelector('[data-role="assistant"][data-state="failed"]', { timeout: 30000 });
      await idle(page);
      await compact(page);
      const nothing = await waitLine(page, 1, "nothing");
      assert(nothing.line === "Nothing to compact." && !nothing.error, `opencode's nothing reads ${JSON.stringify(nothing)}`);
      await open(page, opencode);
      return `${refused.error} / ${nothing.line}`;
    });

    await run("an OpenCode compaction survives a restart of the cockpit and leaves no server", async () => {
      await send(page, "MAGIC COMPACT_RESTART");
      await page.evaluate(() => { window.__dcSamePage = true; });
      await compact(page);
      const done = await waitLine(page, 3, "compacted", 60000);
      await idle(page, 30000);
      assert(await page.evaluate(() => window.__dcSamePage === true), "the page reloaded, this says nothing about the open stream");
      assert(done.line === "Conversation compacted.", `after the restart opencode's line reads ${JSON.stringify(done)}`);
      await open(page, opencode);
      const all = await lines(page);
      assert(all.length === 3 && all[2].mark === "compacted" && !(await working(page)), `after a reload opencode's lines read ${JSON.stringify(all)}`);
      await send(page, "MAGIC SERVE_LEFT");
      const last = (await page.locator('[data-role="assistant"]').last().innerText()).trim();
      assert(last.includes("Servers left: 0."), `after the restart the servers read ${last}`);
      return "one line, no server left";
    });

    await run("a stopped OpenCode compaction leaves the next message answered and no server", async () => {
      await send(page, "MAGIC COMPACT_LONG");
      const count = (await lines(page)).length;
      await compact(page);
      await waitLine(page, count + 1, "streaming", 8000);
      await sleep(2000);
      await page.locator("[data-assistant-cancel]").tap();
      const stopped = await waitLine(page, count + 1, "cancelled", 20000);
      await idle(page);
      assert(stopped.line === "You stopped the compaction." && !(await ring(page)).pending, `the stop reads ${JSON.stringify(stopped)} ${JSON.stringify(await ring(page))}`);
      await send(page, "MAGIC SERVE_LEFT after the stop");
      const last = (await page.locator('[data-role="assistant"]').last().innerText()).trim();
      assert(last.includes("AIRPORT Servers left: 0.") && !last.includes("Summary"), `the turn after the stop answered ${last}`);
      return `${stopped.line} ${last.slice(-26)}`;
    });

    await run("a killed OpenCode helper leaves no server, nor does a restart while a turn clears what it left", async () => {
      await send(page, "MAGIC COMPACT_KILL_HELPER CLEAR_RESTART");
      const count = (await lines(page)).length;
      await compact(page);
      const killed = await waitLine(page, count + 1, "failed", 30000);
      await idle(page);
      const answers = await page.locator('[data-role="assistant"]').count();
      await page.fill("[data-assistant-input]", "MAGIC during the restart");
      await page.locator("[data-assistant-send]").tap();
      await page.waitForFunction((n) => { const a = document.querySelectorAll('[data-role="assistant"]'); return a.length > n && a[a.length - 1].getAttribute("data-state") !== "streaming"; }, answers, { timeout: 90000 });
      await open(page, opencode);
      await idle(page, 30000);
      const lost = (await page.locator('[data-role="assistant"]').last().innerText()).trim();
      assert(!lost.includes("Summary"), `the turn during the restart answered ${lost}`);
      await send(page, "MAGIC SERVE_LEFT after the restart");
      const last = (await page.locator('[data-role="assistant"]').last().innerText()).trim();
      assert(last.includes("AIRPORT Servers left: 0.") && !last.includes("Summary"), `after the kill and the restart: ${last}`);
      return `${killed.line} ${last.slice(-26)}`;
    });

    await run("codex compacts through its app server on the chat's model, its ring waits and the next turn goes on from the summary", async () => {
      codex = await newAssistant(page, "codex").catch(() => "");
      assert(codex, "this instance runs no codex, put tests/e2e/fakes on the PATH");
      made.push(codex);
      assert((await post(page, `/assistants/${codex}`, { form: "model", model: "gpt-fake-luna" })) === 200, "the chat model was refused");
      await open(page, codex);
      await openRing(page);
      const first = await control(page);
      assert(first.shown && first.disabled && first.hint === "Possible after the first answer.", `codex's control before an answer reads ${JSON.stringify(first)}`);
      await closeRing(page);
      await send(page, "MAGIC");
      assert((await ring(page)).fill === "68 100", `codex's ring before reads ${JSON.stringify(await ring(page))}`);
      const answers = await page.locator('[data-role="assistant"]').count();
      await compact(page);
      const running = await waitLine(page, 1, "streaming", 8000);
      assert(running.line === "Compacting the conversation…" && running.light, `while codex compacts the line reads ${JSON.stringify(running)}`);
      const done = await waitLine(page, 1, "compacted");
      await idle(page);
      const gauge = await ring(page);
      assert(done.line === "Conversation compacted." && !done.error && gauge.pending && gauge.fill === "0 100" && gauge.label.endsWith(PENDING), `after codex's compaction: ${JSON.stringify(done)} ${JSON.stringify(gauge)}`);
      assert((await page.locator('[data-role="assistant"]').count()) === answers, "codex's compaction wrote an answer bubble");
      await send(page, "COMPACTIONS");
      const last = (await page.locator('[data-role="assistant"]').last().innerText()).trim();
      const after = await ring(page);
      assert(last.includes("compactions: 1 (gpt-fake-luna), summary: yes, app servers: 0"), `the turn after codex's compaction answered ${last}`);
      assert(!after.pending && after.fill === "68 100", `the next answer did not read codex's ring again: ${JSON.stringify(after)}`);
      return `${done.line} ${last}`;
    });

    await run("codex has nothing to compact right after a compaction and shows a refusal in its words", async () => {
      await compact(page);
      const again = await waitLine(page, 2, "compacted");
      await idle(page);
      await compact(page);
      const nothing = await waitLine(page, 3, "nothing");
      await idle(page);
      assert(again.line === "Conversation compacted." && nothing.line === "Nothing to compact." && !nothing.error, `back to back codex reads ${JSON.stringify([again, nothing])}`);
      await send(page, "COMPACTIONS after nothing");
      const asked = (await page.locator('[data-role="assistant"]').last().innerText()).trim();
      assert(asked.includes("compactions: 2 (gpt-fake-luna gpt-fake-luna)"), `nothing to compact still asked codex: ${asked}`);
      await send(page, "MAGIC COMPACT_REFUSE");
      await compact(page);
      const refused = await waitLine(page, 4, "failed");
      await idle(page);
      assert(refused.line === "The conversation was not compacted." && refused.error.includes("The coder said: Compaction failed: the summary was too long."), `codex's refusal reads ${JSON.stringify(refused)}`);
      assert(!(await ring(page)).pending, "a refused codex compaction left the ring waiting");
      await send(page, "MAGIC after the refusal");
      const next = (await page.locator('[data-role="assistant"]').last().innerText()).trim();
      assert(next.includes("AIRPORT"), `the turn after codex's refusal answered ${next}`);
      return `${nothing.line} / ${refused.error}`;
    });

    await run("a stopped codex compaction leaves no app server, and one survives a restart of the cockpit", async () => {
      await send(page, "MAGIC COMPACT_LONG");
      let count = (await lines(page)).length;
      await compact(page);
      await waitLine(page, count + 1, "streaming", 8000);
      await sleep(2000);
      await page.locator("[data-assistant-cancel]").tap();
      const stopped = await waitLine(page, count + 1, "cancelled", 20000);
      await idle(page);
      assert(stopped.line === "You stopped the compaction." && !(await ring(page)).pending, `codex's stop reads ${JSON.stringify(stopped)} ${JSON.stringify(await ring(page))}`);
      await send(page, "COMPACTIONS after the stop");
      const left = (await page.locator('[data-role="assistant"]').last().innerText()).trim();
      assert(left.includes("app servers: 0"), `after the stop: ${left}`);

      await send(page, "MAGIC COMPACT_RESTART");
      count = (await lines(page)).length;
      await page.evaluate(() => { window.__dcSamePage = true; });
      await compact(page);
      const done = await waitLine(page, count + 1, "compacted", 60000);
      await idle(page, 30000);
      assert(await page.evaluate(() => window.__dcSamePage === true), "the page reloaded, this says nothing about the open stream");
      assert(done.line === "Conversation compacted.", `after the restart codex's line reads ${JSON.stringify(done)}`);
      await open(page, codex);
      const all = await lines(page);
      assert(all.length === count + 1 && all[count].mark === "compacted" && !(await working(page)), `after a reload codex's lines read ${JSON.stringify(all)}`);
      await send(page, "COMPACTIONS after the restart");
      const last = (await page.locator('[data-role="assistant"]').last().innerText()).trim();
      assert(last.includes("summary: yes, app servers: 0"), `after the restart: ${last}`);
      return `${stopped.line} / ${done.line}`;
    });

    await run("a guest reads the line and gets no control, and cannot post one", async () => {
      await page.goto(`${BASE}/settings/sharing`, { waitUntil: "domcontentloaded" });
      const created = await page.request.post(`${BASE}/settings/sharing/new`, {
        form: { csrf_token: await csrfOf(page), name: "zzcompact", assistant: claude, amount: "1", unit: "hours" },
        headers: { Accept: "text/html" },
      });
      const token = ((await created.text()).match(/data-share-created[\s\S]*?value="https?:\/\/[^"]*\/share\/([A-Za-z0-9_-]+)"/) || [])[1];
      assert(token, `no link: ${created.status()}`);
      await page.goto(`${BASE}/settings/sharing`, { waitUntil: "domcontentloaded" });
      linkId = (await page.locator("[data-share-row]", { has: page.locator(`[data-share-assistant="${claude}"]`) }).first().getAttribute("data-share-row")) || "";
      const guestCtx = await browser.newContext({ ignoreHTTPSErrors: true, hasTouch: true, isMobile: true, viewport: { width: 390, height: 844 } });
      try {
        const guest = await guestCtx.newPage();
        await guest.goto(`${BASE}/share/${token}/assistants/${claude}`, { waitUntil: "domcontentloaded" });
        await guest.waitForSelector(LINE, { timeout: 15000 });
        const seen = await lines(guest);
        assert(seen.length === 5 && seen[0].line === "Conversation compacted.", `the guest reads ${JSON.stringify(seen)}`);
        assert((await guest.locator("button[data-assistant-compact]").count()) === 0, "the guest got a compact control");
        const posted = await guest.request.post(`${BASE}/share/${token}/assistants/${claude}`, { form: { form: "compact" }, maxRedirects: 0 });
        assert(posted.status() >= 400, `a guest's compaction answered ${posted.status()}`);
        return `guest post ${posted.status()}`;
      } finally {
        await guestCtx.close();
      }
    });
  }
});

async function post(page, path, body) {
  return page.evaluate(async ({ path, body }) => {
    const token = document.querySelector('meta[name="csrf-token"]')?.content || "";
    const res = await fetch(path, { method: "POST", headers: { "X-CSRF-Token": token, Accept: "application/json" }, body: new URLSearchParams(body) });
    return res.status;
  }, { path, body });
}
