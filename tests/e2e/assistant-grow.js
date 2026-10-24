const L = require("./lib");
const { assert, BASE, sleep } = L;

// Not a runner of its own: assistant-choices.js runs these before its
// assistant is deleted, on an assistant of their own that they delete again.
// Every box that grows with its text (the composer, a note under a question,
// the trigger form's task) has to stand at the height its text needs without
// a keystroke: after leaving and coming back inside the app, after a reload,
// after being hidden while code set its text, and after the viewport turned
// or was resized. Desktop and a 390px touch phone.
// Gotchas:
// - fit reads the height the text needs by setting the box to auto for one
//   synchronous read, nothing paints or observes that in between,
// - words are saved 200ms after the hand rests and the server renders them
//   back, so a check that leaves or reloads waits until the draft holds them,
// - a draft saved from the page itself reaches it as a draft event, which is
//   code setting the text with no input event.

const READY = "dc-assistant[ready]";
const INPUT = "[data-assistant-input]";
const NOTE = "dc-assistant-choices[post-url] textarea[data-choices-note]";
const TASK = ".modal.show [data-trigger-task]";
const LINES = ["first line", `second line ${"that wraps ".repeat(36)}`, "third line", "fourth line", "fifth line"];
const TEXT = LINES.join("\n");
const OTHER = ["set by code", `while hidden ${"and wrapping ".repeat(30)}`, "third", "fourth"].join("\n");

// The cap is 35% of the box's surface: the transcript's scroller for the
// composer, the window for the others.
function fit(target, selector) {
  return target.evaluate((sel) => {
    const box = document.querySelector(sel);
    if (!box) return null;
    const shown = box.style.height;
    box.style.height = "auto";
    const need = box.scrollHeight;
    const surface = box.matches("[data-assistant-input]") ? box.closest("dc-assistant").querySelector("[data-assistant-scroll]").clientHeight : window.innerHeight;
    box.style.height = shown;
    return { need, cap: Math.round(surface * 0.35), height: Math.round(box.getBoundingClientRect().height), lines: box.value.split("\n").length };
  }, selector);
}

async function assertFits(target, selector, where) {
  const got = await fit(target, selector);
  assert(got, `${where}: the box is gone`);
  const want = Math.min(got.need, got.cap);
  assert(Math.abs(got.height - want) <= 1, `${where}: the box stands at ${got.height}px, its ${got.lines} lines need ${got.need}px under a cap of ${got.cap}px`);
  return got;
}

async function inApp(target, act) {
  await target.evaluate(() => { window.__growNav = new Promise((done) => window.addEventListener("dc:navigated", done, { once: true })); });
  await act();
  await target.evaluate(() => window.__growNav);
}

function draftOf(target, id) {
  return target.evaluate(async (instance) => (await fetch(`/assistants/${instance}/draft`, { headers: { Accept: "application/json" } })).json(), id);
}

async function waitDraft(target, id, ok) {
  let got = null;
  for (const until = Date.now() + 10000; Date.now() < until; await sleep(150)) {
    got = await draftOf(target, id);
    if (ok(got)) return got;
  }
  throw new Error(`the draft never held what was typed: ${JSON.stringify(got).slice(0, 300)}`);
}

async function typeLines(target, selector, newline) {
  await target.fill(selector, "");
  await target.click(selector);
  for (const [i, line] of LINES.entries()) {
    if (i) await target.keyboard.press(newline);
    await target.keyboard.type(line);
  }
}

// leaveAndReturn goes somewhere else and back the way a hand does: the rail
// on a desktop, on a phone a link and the back gesture, all swapped in by
// pe.js without a page load. The rail's entry opens the assistant looked at
// last by the second, and the runner made several within one, so where it
// lands on another the list column's row leads on.
async function leaveAndReturn(target, phone, id) {
  if (phone) {
    await inApp(target, () => target.evaluate(() => window.app.navigate("/docs")));
    await inApp(target, () => target.goBack());
  } else {
    await inApp(target, () => target.click(".dc-rail a.dc-rail-btn[href='/docs']"));
    await inApp(target, () => target.click(".dc-rail a.dc-rail-btn[href='/assistants']"));
    await target.waitForSelector(READY, { timeout: 15000 });
    if ((await target.locator("dc-assistant").getAttribute("assistant-id")) !== id) {
      await inApp(target, () => target.click(`.dc-app > .dc-ctx a[data-assistant-instance="${id}"]`));
    }
  }
  await target.waitForSelector(READY, { timeout: 15000 });
  const shown = await target.locator("dc-assistant").getAttribute("assistant-id");
  assert(shown === id, `coming back landed on ${shown}, not on ${id}`);
  await sleep(300);
}

async function hideWhile(target, selector, act) {
  await target.evaluate((sel) => { document.querySelector(sel).hidden = true; }, selector);
  await act();
  await target.evaluate((sel) => { document.querySelector(sel).hidden = false; }, selector);
  await sleep(300);
}

async function turn(target, phone, check) {
  const back = target.viewportSize();
  const sizes = phone ? [{ width: 844, height: 390 }, back] : [{ width: 1000, height: 700 }, { width: 420, height: 900 }, back];
  const seen = [];
  for (const size of sizes) {
    await target.setViewportSize(size);
    await sleep(400);
    seen.push(`${size.width}x${size.height} ${JSON.stringify(await check(`${size.width}x${size.height}`))}`);
  }
  return seen;
}

async function growChecks({ page, run, mobilePage, helpers }) {
  const { openAssistant, useClaude, newConversation, idle, postForm, ask } = helpers;
  let id = "";
  const own = async () => {
    if (id) return id;
    await openAssistant(page);
    await useClaude(page);
    id = await newConversation(page);
    return id;
  };

  await run("the composer stands at its draft's height after coming back, a reload, being hidden and a turn, without typing", async () => {
    await own();
    const seen = {};
    for (const phone of [false, true]) {
      const target = phone ? await mobilePage() : page;
      const where = phone ? "phone" : "desktop";
      await openAssistant(target, id);
      await idle(target);
      await typeLines(target, INPUT, "Shift+Enter");
      await waitDraft(target, id, (d) => d.text === TEXT);
      const typed = await assertFits(target, INPUT, `${where} typed`);
      assert(typed.lines === LINES.length, `${where}: the typed text is not in the box: ${JSON.stringify(typed)}`);

      await leaveAndReturn(target, phone, id);
      const back = await assertFits(target, INPUT, `${where} back`);
      assert(back.height === typed.height, `${where}: the box came back at ${back.height}px, it stood at ${typed.height}px`);

      await target.reload({ waitUntil: "domcontentloaded" });
      await target.waitForSelector(READY, { timeout: 15000 });
      await sleep(300);
      await assertFits(target, INPUT, `${where} reload`);

      await hideWhile(target, "[data-assistant-footer]", async () => {
        const saved = await postForm(target, id, { form: "draft", message: OTHER });
        assert(saved.status === 200, `${where}: the draft could not be saved: ${saved.status}`);
        await target.waitForFunction(([sel, want]) => document.querySelector(sel)?.value === want, [INPUT, OTHER], { timeout: 10000 });
      });
      const shown = await assertFits(target, INPUT, `${where} shown again`);

      const turned = await turn(target, phone, (size) => assertFits(target, INPUT, `${where} ${size}`));
      seen[where] = { typed: typed.height, back: back.height, shown: shown.height, turned };
      await postForm(target, id, { form: "draft", message: "" });
    }
    return JSON.stringify(seen);
  });

  await run("a note under a question stands at its words' height after coming back, a reload, being hidden and a turn, without typing", async () => {
    await own();
    await openAssistant(page, id);
    if (!(await page.locator(NOTE).count())) await ask(page);
    const messageID = await page.locator("dc-assistant-choices[post-url]").last().getAttribute("message-id");
    const seen = {};
    for (const phone of [false, true]) {
      const target = phone ? await mobilePage() : page;
      const where = phone ? "phone" : "desktop";
      await openAssistant(target, id);
      await target.waitForSelector(NOTE, { timeout: 15000 });
      await typeLines(target, NOTE, "Enter");
      await waitDraft(target, id, (d) => ((d.answers || {})[messageID] || [])[0]?.text === TEXT);
      const typed = await assertFits(target, NOTE, `${where} typed`);
      assert(typed.lines === LINES.length, `${where}: the typed note is not in the box: ${JSON.stringify(typed)}`);

      await leaveAndReturn(target, phone, id);
      await target.waitForSelector(NOTE, { timeout: 15000 });
      const back = await assertFits(target, NOTE, `${where} back`);
      assert(back.height === typed.height, `${where}: the note came back at ${back.height}px, it stood at ${typed.height}px`);

      await target.reload({ waitUntil: "domcontentloaded" });
      await target.waitForSelector(NOTE, { timeout: 15000 });
      await sleep(300);
      await assertFits(target, NOTE, `${where} reload`);

      await hideWhile(target, "dc-assistant-choices[post-url]", async () => {
        const saved = await postForm(target, id, { form: "answers-draft", message_id: messageID, "note-0": OTHER });
        assert(saved.status === 200, `${where}: the note could not be saved: ${saved.status}`);
        await target.waitForFunction(([sel, want]) => document.querySelector(sel)?.value === want, [NOTE, OTHER], { timeout: 10000 });
      });
      const shown = await assertFits(target, NOTE, `${where} shown again`);

      const turned = await turn(target, phone, (size) => assertFits(target, NOTE, `${where} ${size}`));
      seen[where] = { typed: typed.height, back: back.height, shown: shown.height, turned };
      await postForm(target, id, { form: "answers-draft", message_id: messageID });
    }
    return JSON.stringify(seen);
  });

  await run("the trigger form's task stands at its text's height when the dialog opens again, on its own page and after a turn there, without typing", async () => {
    await own();
    const made = await page.evaluate(async ([instance, task]) => {
      const body = new URLSearchParams({ form: "new", assistant: instance, event: "cron", spec: "0 4 1 1 *", task });
      body.set("csrf_token", document.querySelector('meta[name="csrf-token"]').content);
      const res = await fetch("/assistants/triggers", { method: "POST", headers: { Accept: "application/json" }, body });
      return { status: res.status, body: await res.json().catch(() => ({})) };
    }, [id, `MAGIC ${TEXT}`]);
    assert(made.status === 200, `the trigger answered ${made.status}: ${JSON.stringify(made.body)}`);
    const trigger = made.body.id;
    const edit = `[data-assistant-trigger="${trigger}"] [data-assistant-trigger-edit]`;
    const seen = {};
    for (const phone of [false, true]) {
      const target = phone ? await mobilePage() : page;
      const where = phone ? "phone" : "desktop";
      const open = async () => {
        if (phone) {
          await target.click("[data-assistant-watching-button]");
          await target.waitForSelector("#assistant-aside.show", { timeout: 8000 });
        }
        await target.click("[data-assistant-triggers-tab]");
        await target.waitForSelector(edit, { timeout: 8000 });
        await target.click(edit);
        await target.waitForSelector(TASK, { timeout: 8000 });
        await sleep(400);
      };
      const close = async () => {
        await target.click('.modal.show .modal-footer [data-bs-dismiss="modal"]');
        await target.waitForSelector(".modal.show", { state: "hidden", timeout: 8000 });
      };
      await openAssistant(target, id);
      await open();
      const first = await assertFits(target, TASK, `${where} opened`);
      assert(first.lines === LINES.length, `${where}: the stored task is not in the box: ${JSON.stringify(first)}`);
      await close();

      await leaveAndReturn(target, phone, id);
      await open();
      const again = await assertFits(target, TASK, `${where} opened again`);
      assert(again.height === first.height, `${where}: the task opened again at ${again.height}px, it stood at ${first.height}px`);
      await close();

      // The turn runs on the form's own page: WebKit's phone keeps a
      // dialog's footer under the dialog's box after a turn, and a tap on
      // Cancel there would miss it.
      const href = await target.locator(edit).getAttribute("href");
      const paged = "dc-assistant-trigger [data-trigger-task]";
      await target.goto(new URL(href, BASE).toString(), { waitUntil: "domcontentloaded" });
      await target.waitForSelector(paged, { timeout: 8000 });
      await sleep(300);
      const page1 = await assertFits(target, paged, `${where} page`);
      const turned = await turn(target, phone, (size) => assertFits(target, paged, `${where} page ${size}`));
      seen[where] = { opened: first.height, again: again.height, page: page1.height, turned };
    }
    const removed = await page.evaluate(async (sub) => {
      const body = new URLSearchParams({ form: "remove", id: sub, csrf_token: document.querySelector('meta[name="csrf-token"]').content });
      return (await fetch("/assistants/triggers", { method: "POST", headers: { Accept: "application/json" }, body })).status;
    }, trigger);
    assert(removed === 200, `the trigger could not be removed: ${removed}`);
    return JSON.stringify(seen);
  });

  await run("the grow checks' assistant is deleted", async () => {
    if (!id) return "nothing to delete";
    const gone = await postForm(page, id, { form: "delete" });
    assert(gone.status === 200, `the assistant could not be deleted: ${gone.status}`);
    return "deleted";
  });
}

module.exports = { growChecks };
