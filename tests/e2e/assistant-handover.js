const L = require("./lib");
const { assert, BASE } = L;

// Not a runner of its own: assistant.js runs these before its cleanup. An
// assistant starting another one on the user's word, `assistant-new`: the
// page's create (`POST /assistants/new`) over the local socket with a name and
// a first message. A chat turn starts one, it shows up live in the list of
// every open page like any other row and answers that message in a thread of
// its own. An assistant prompting one that already runs, itself included,
// `assistant-send-prompt`: the page's send over the local socket, a bubble
// like the user's own, answered at once or waiting in the queue of a busy one
// beside a message typed there. The instruction file carries the rule that
// both run only on the user's word, which is the whole of that rule.
//
// The claude fake drives every turn here: SPAWN:<name> in a prompt runs
// `./cockpit assistant-new --name <name> --message MAGIC`, PROMPT_TO:<id>:<word>
// runs `./cockpit assistant-send-prompt <id> "MAGIC <word>"`, both answer with
// what the command printed, and INSTRUCTIONS:<word> answers the lines of the
// turn's CLAUDE.md that carry the word.
//
// Gotchas:
// - the second page is a desktop context of its own: WebKit holds a POST while
//   about six streams of one context stand open, and every assistant page holds
//   two,
// - every assistant made here is deleted again, the started one included.

const READY = "dc-assistant[ready]";

function stamp() {
  return `${Date.now().toString(36)}${Math.floor(Math.random() * 1e4)}`;
}

async function post(page, url, fields) {
  return page.evaluate(async ([target, values]) => {
    const body = new URLSearchParams(values);
    body.set("csrf_token", document.querySelector('meta[name="csrf-token"]').content);
    const res = await fetch(target, {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded", Accept: "application/json" },
      body: body.toString(),
    });
    return { status: res.status, body: await res.json().catch(() => ({})) };
  }, [url, fields]);
}

async function open(page, id) {
  await page.goto(`${BASE}/assistants/${id}`, { waitUntil: "domcontentloaded" });
  await L.dismissUpdate(page);
  await page.waitForSelector(READY, { timeout: 15000 });
}

// answerOf waits for the newest answer of the assistant on screen to settle
// and reads its text.
async function answerOf(page, before) {
  await page.waitForFunction((count) => {
    const rows = document.querySelectorAll('[data-assistant-message][data-role="assistant"]');
    const last = rows[rows.length - 1];
    return rows.length > count && last.getAttribute("data-state") === "complete";
  }, before, { timeout: 40000 });
  return page.locator('[data-assistant-message][data-role="assistant"] [data-assistant-text]').last().innerText();
}

// ask types a message into the composer and sends it, the way the user does
// on the phone or the desktop, and answers what the assistant replied.
async function ask(page, text) {
  await page.waitForFunction(() => !document.querySelector("dc-assistant")?.hasAttribute("running"), null, { timeout: 60000 });
  const before = await page.locator('[data-assistant-message][data-role="assistant"]').count();
  await page.fill("[data-assistant-input]", text);
  await page.click("[data-assistant-send]");
  return answerOf(page, before);
}

// row reads one assistant's row of the list column the way the eye does: is
// it there with a box, what it is called, and which attributes it carries.
function row(page, id) {
  return page.evaluate((target) => {
    const node = document.querySelector(`.dc-app > .dc-ctx[data-assistant-rows] [data-assistant-instance="${target}"]`);
    if (!node || !node.getClientRects().length || getComputedStyle(node).visibility === "hidden") return null;
    return { name: node.getAttribute("data-assistant-name"), attrs: node.getAttributeNames().sort().join(" ") };
  }, id);
}

// bubbles reads the user's bubbles that carry exactly these words the way
// the eye compares two of them: whether it stands on screen, its state, and
// its markup and words with the message's own words and time taken out, which
// is what would tell one from the other.
function bubbles(page, texts) {
  return page.evaluate((wanted) => wanted.map((words) => {
    const node = [...document.querySelectorAll('[data-assistant-message][data-role="user"]')]
      .find((m) => (m.querySelector("[data-assistant-text]")?.innerText || "").trim() === words);
    if (!node) return null;
    const clone = node.cloneNode(true);
    clone.querySelectorAll("[data-assistant-text], dc-time").forEach((n) => n.replaceChildren());
    const shape = [clone, ...clone.querySelectorAll("*")].map((n) =>
      `${n.tagName}.${n.getAttribute("class") || ""}[${n.getAttributeNames().filter((a) => a !== "id" && a !== "class").sort().join(",")}]`).join(" ");
    const badge = node.querySelector("[data-assistant-queued]");
    return {
      visible: node.getClientRects().length > 0 && getComputedStyle(node).visibility !== "hidden",
      state: node.getAttribute("data-state"),
      badge: !!badge && badge.getClientRects().length > 0,
      shape,
      rest: clone.innerText.replace(/\s+/g, " ").trim(),
    };
  }), texts);
}

// answeredAfter waits until an answer AIRPORT stands complete after the user's
// bubble with these words.
async function answeredAfter(page, words) {
  await page.waitForFunction((wanted) => {
    const rows = [...document.querySelectorAll("[data-assistant-message]")];
    const at = rows.findIndex((m) => m.getAttribute("data-role") === "user" && (m.querySelector("[data-assistant-text]")?.innerText || "").trim() === wanted);
    return at >= 0 && rows.slice(at + 1).some((m) => m.getAttribute("data-role") === "assistant" && m.getAttribute("data-state") === "complete" && m.innerText.includes("AIRPORT"));
  }, words, { timeout: 60000 });
}

// printed waits for the asking assistant's answer that carries what the
// command printed for this target, read by its words and not by its place:
// a prompt to itself starts the next turn right behind it.
async function printed(page, target) {
  const handle = await page.waitForFunction((id) => {
    const hit = [...document.querySelectorAll('[data-assistant-message][data-role="assistant"]')]
      .find((m) => m.getAttribute("data-state") === "complete" && (m.innerText || "").includes(`assistant ${id},`));
    return hit ? hit.innerText : false;
  }, target, { timeout: 40000 });
  return handle.jsonValue();
}

async function sendTyped(page, text) {
  await page.fill("[data-assistant-input]", text);
  await page.click("[data-assistant-send]");
}

async function chatChecks({ page, run, browser, engine }) {
  await run("an assistant asked in its chat starts another one with a name and a first message, live in every open list", async () => {
    const made = await post(page, "/assistants/new", { form: "new", coder: "claude" });
    const asker = made.body.id;
    assert(asker, `no assistant to ask: ${made.status}`);
    const name = `e2e-asked-${stamp()}`;
    const other = await L.newDesktop(browser, engine);
    let started = "";
    try {
      const watcher = await other.newPage();
      await L.login(watcher);
      await open(watcher, asker);
      await open(page, asker);

      const answer = await ask(page, `Start an assistant for me. SPAWN:${name}`);
      const said = answer.match(/assistant (\S+) started, called "([^"]+)", answering its first message/);
      assert(said && said[2] === name, `the command did not print the id and the name: ${answer}`);
      started = said[1];

      // Both open pages list it without a reload, a row like every other.
      for (const [who, target] of [["the asking page", page], ["a second page", watcher]]) {
        await target.waitForFunction((id) => !!document.querySelector(`.dc-app > .dc-ctx[data-assistant-rows] [data-assistant-instance="${id}"]`), started, { timeout: 15000 });
        const seen = await row(target, started);
        const mine = await row(target, asker);
        assert(seen && seen.name === name, `${who} lists ${JSON.stringify(seen)}`);
        assert(mine && seen.attrs === mine.attrs, `${who} tells the new row apart: ${seen.attrs} against ${mine.attrs}`);
      }
      await watcher.close();

      // It answers the first message as if the user had sent it, in a thread
      // of its own that holds nothing of the one that asked.
      await open(page, started);
      await page.waitForFunction(() => {
        const rows = document.querySelectorAll('[data-assistant-message][data-role="assistant"]');
        return rows.length && rows[rows.length - 1].getAttribute("data-state") === "complete";
      }, null, { timeout: 40000 });
      const thread = await page.evaluate(() => [...document.querySelectorAll("[data-assistant-message]")].map((m) => ({
        role: m.getAttribute("data-role"),
        text: (m.querySelector("[data-assistant-text]")?.innerText || "").trim(),
      })));
      assert(thread.length === 2 && thread[0].role === "user" && thread[0].text === "MAGIC" && thread[1].role === "assistant" && thread[1].text === "AIRPORT",
        `the new thread holds ${JSON.stringify(thread)}`);
      assert(!thread.some((m) => m.text.includes("SPAWN")), "the asking thread leaked into the new one");
      return `${started.slice(0, 8)} named ${name}, answered AIRPORT`;
    } finally {
      await other.close().catch(() => {});
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      for (const id of [started, asker].filter(Boolean)) await post(page, `/assistants/${id}`, { form: "delete" });
    }
  });

  await run("an assistant asked in its chat prompts one that already runs, itself included, and it reads as the user's own message", async () => {
    const asker = (await post(page, "/assistants/new", { form: "new", coder: "claude" })).body.id;
    const target = (await post(page, "/assistants/new", { form: "new", coder: "claude" })).body.id;
    assert(asker && target, "no assistants to prompt between");
    const word = `w${stamp()}`;
    const self = `s${stamp()}`;
    try {
      // A message the user typed there is the yardstick.
      await open(page, target);
      const typed = await ask(page, "MAGIC typed by hand");
      assert(typed.includes("AIRPORT"), `the typed message was answered ${typed}`);

      await open(page, asker);
      await sendTyped(page, `Tell the other one. PROMPT_TO:${target}:${word}`);
      const sent = await printed(page, target);
      assert(new RegExp(`prompt sent to assistant ${target}, called "[^"]+", answering it now`).test(sent), `the command printed ${sent}`);

      // To itself it waits until the turn that sent it ends, then it is answered.
      await page.waitForFunction(() => !document.querySelector("dc-assistant")?.hasAttribute("running"), null, { timeout: 60000 });
      await sendTyped(page, `And me. PROMPT_TO:${asker}:${self}`);
      const own = await printed(page, asker);
      assert(new RegExp(`prompt sent to assistant ${asker}, called "[^"]+", waiting in its queue`).test(own), `the command printed ${own}`);
      await answeredAfter(page, `MAGIC ${self}`);

      await open(page, target);
      await answeredAfter(page, `MAGIC ${word}`);
      const [mine, theirs] = await bubbles(page, ["MAGIC typed by hand", `MAGIC ${word}`]);
      assert(mine && theirs && theirs.visible && theirs.state === "complete" && !theirs.badge,
        `the prompted bubble reads ${JSON.stringify(theirs)}`);
      assert(theirs.shape === mine.shape && theirs.rest === mine.rest,
        `the prompted bubble is told apart from the typed one: ${JSON.stringify(theirs)} against ${JSON.stringify(mine)}`);
      return `${target.slice(0, 8)} answered AIRPORT, the asker answered itself, bubbles alike`;
    } finally {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      for (const id of [target, asker]) await post(page, `/assistants/${id}`, { form: "delete" });
    }
  });

  await run("a prompt into a busy assistant waits in its queue like a second message typed there", async () => {
    const asker = (await post(page, "/assistants/new", { form: "new", coder: "claude" })).body.id;
    const target = (await post(page, "/assistants/new", { form: "new", coder: "claude" })).body.id;
    assert(asker && target, "no assistants to prompt between");
    const word = `q${stamp()}`;
    try {
      await open(page, target);
      await sendTyped(page, "SLOW hold on");
      await page.waitForSelector("[data-assistant-cancel]:not(.d-none)", { timeout: 15000 });
      await sendTyped(page, "MAGIC typed while busy");
      await page.waitForSelector('[data-assistant-message][data-state="queued"]', { timeout: 15000 });

      await open(page, asker);
      const answer = await ask(page, `Tell the busy one. PROMPT_TO:${target}:${word}`);
      assert(new RegExp(`prompt sent to assistant ${target}, called "[^"]+", waiting in its queue`).test(answer), `the command printed ${answer}`);

      await open(page, target);
      await page.waitForFunction((words) => [...document.querySelectorAll('[data-assistant-message][data-role="user"]')]
        .some((m) => (m.querySelector("[data-assistant-text]")?.innerText || "").trim() === words), `MAGIC ${word}`, { timeout: 15000 });
      const [mine, theirs] = await bubbles(page, ["MAGIC typed while busy", `MAGIC ${word}`]);
      assert(mine && theirs && theirs.visible && mine.state === "queued" && theirs.state === "queued" && theirs.badge,
        `the waiting bubbles read ${JSON.stringify(mine)} and ${JSON.stringify(theirs)}`);
      assert(theirs.shape === mine.shape && theirs.rest === mine.rest,
        `the prompted bubble is told apart from the typed one: ${JSON.stringify(theirs)} against ${JSON.stringify(mine)}`);

      // Stopping the running turn lets the queue go, both as one turn.
      await page.click("[data-assistant-cancel]");
      await page.waitForFunction(() => document.querySelectorAll('[data-assistant-message][data-state="queued"]').length === 0, null, { timeout: 30000 });
      await answeredAfter(page, `MAGIC ${word}`);
      return "waited beside the typed one, answered with it";
    } finally {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      for (const id of [target, asker]) await post(page, `/assistants/${id}`, { form: "delete" });
    }
  });

  await run("the instructions start or prompt an assistant only on the user's word", async () => {
    const made = await post(page, "/assistants/new", { form: "new", coder: "claude" });
    const id = made.body.id;
    assert(id, `no assistant to read the instructions: ${made.status}`);
    try {
      await open(page, id);
      const lines = await ask(page, "INSTRUCTIONS:assistant-send-prompt");
      for (const want of [
        "assistant-send-prompt <id> <text> sends the text as if the user had typed it into that assistant's composer",
        "answered at once or waiting in its queue while it answers something else",
        "assistant-new [--name <name>] [--message <text>]",
        "answers its first message as if the user had sent it",
        "Run each only when the user asks for exactly that, never on your own idea",
        "a trigger or a sequel whose task the user asked for counts as their word",
      ]) assert(lines.includes(want), `the instructions miss ${JSON.stringify(want)}: ${lines}`);
      assert(!lines.includes("There is no way to message another assistant"), `the instructions still forbid it: ${lines}`);
      return "the rule stands in CLAUDE.md";
    } finally {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await post(page, `/assistants/${id}`, { form: "delete" });
    }
  });
}

module.exports = { chatChecks };
