const path = require("path");
const L = require("./lib");
const { assert, BASE, sleep } = L;

// Not a runner of its own: assistant.js runs these at its end, on an
// assistant of their own that they delete again. An answer ends in an ask
// block, which the fake claude streams back a line at a time (ECHO_ANSWER),
// and the page turns it into questions with their options as buttons and a
// field under each, answered with one send, the button or Ctrl+Enter in a
// note. The assistant reads the answers as
// the user's next message, the block shows the picks and that message stands
// as one slim row under it, "You · <time> · answered". A block the thread
// moved on past keeps its card and says Not answered. What is picked and
// typed in an open block is the assistant's draft, beside the composer's
// words: a second browser of the owner shows it live and after a reload, a
// send there answers the block in the first and leaves the words typed there.
// A question whose options stand behind "- [ ] " takes several picks, its
// checkboxes drawn like the radios, and one block mixes both kinds. Answers
// sent from above the end jump the thread to its end and follow the reply,
// as a typed message does.
// SHOTS_DIR with a mount takes the phone screenshots of an open block, of an
// answered one with its row, of an answered one above an open one and of an
// open mixed block with two picks on its question that takes several.
// Gotchas:
// - the block is server rendered in every state, the element only answers an
//   open one, so a check reads data-choices-state, never the script,
// - a radio drawn as a button is visually hidden, a check clicks its label,
// - nothing in an answer may name a fake keyword (MAGIC, SLOW, FAIL, TOOL,
//   MODEL ...), the answer to the answers is the fake's plain line,
// - a tap leaves :hover on the option, sticky hover only shows in a context
//   with hasTouch and isMobile and a real tap, never a click,
// - picks and notes are saved 200ms after the hand rests, and the phone's
//   page shares them with the desktop's, so a check that reloads, leaves or
//   hands the block on first waits until the draft holds them (waitStored),
//   and a check that picks on the phone leaves the block empty again.

const BLOCK = "dc-assistant-choices";
const QUESTIONS = [
  "Which database should the app use?",
  "- PostgreSQL",
  "- SQLite",
  "Deploy right away?",
  "- Yes",
  "- No, after the review",
];
const PROMPT = ["ECHO_ANSWER", "Two things to decide first.", "", "```ask", ...QUESTIONS, "```", "", "Everything else waits for that.", ""].join("\n");
const PATHS = ["/root/projects/shop/internal/checkout/payment_provider_adapter_test.go", "/root/projects/shop/internal/checkout/payment_provider_adapter.go"];
const LONG_PROMPT = ["ECHO_ANSWER", "One file first.", "", "```ask", "Which file should change first?", ...PATHS.map((p) => `- ${p}`), "```", ""].join("\n");
const MIXED = ["Which checks should run before the deploy?", "- [ ] Unit tests", "- [ ] Lint", "- [ ] End to end", "Deploy right away?", "- Yes", "- No"];
const MIXED_PROMPT = ["ECHO_ANSWER", "Pick what ships.", "", "```ask", ...MIXED, "```", ""].join("\n");
// The text under the block stands taller than the thread, so a reader who
// comes up to the questions stands further above the end than the block
// shrinks once answered, and the shrink alone never clamps them to the end.
const TAIL = "the rest of the plan waits for these answers, ".repeat(24).trim();
const TAIL_PROMPT = ["ECHO_ANSWER", "Two things to decide first.", "", "```ask", ...QUESTIONS, "```", "", ...Array(4).fill([TAIL, ""]).flat()].join("\n");
const MIN_TARGET = 24;

const state = (page) => page.locator(BLOCK).last().getAttribute("data-choices-state");
const option = (page, q, o) => page.locator(`${BLOCK} [data-choices-question]`).nth(q).locator("[data-choices-option]").nth(o);
const note = (page, q) => page.locator(`${BLOCK} [data-choices-question]`).nth(q).locator("textarea");
const sendButton = (page) => page.locator(`${BLOCK} [data-choices-send]`);

async function waitState(page, want) {
  await page.waitForFunction(([sel, w]) => {
    const blocks = document.querySelectorAll(sel);
    return blocks.length && blocks[blocks.length - 1].getAttribute("data-choices-state") === w;
  }, [BLOCK, want], { timeout: 20000 });
}

async function sendDisabled(page) {
  return sendButton(page).evaluate((b) => b.disabled);
}

async function countText(page) {
  return (await page.locator(`${BLOCK} [data-choices-count]`).textContent()).trim();
}

function visible(locator) {
  return locator.evaluate((el) => {
    const box = el.getBoundingClientRect();
    return box.width > 0 && box.height > 0 && getComputedStyle(el).visibility !== "hidden" && el.checkVisibility();
  });
}

// look reads a button the way it stands once a tap's :active and the
// button's color transition are over.
function look(locator) {
  return locator.evaluate(async (el) => {
    await new Promise((done) => {
      const tick = () => (el.matches(":active") ? requestAnimationFrame(tick) : done());
      tick();
    });
    await Promise.all(el.getAnimations().map((a) => a.finished.catch(() => {})));
    const cs = getComputedStyle(el);
    return [cs.color, cs.backgroundColor, cs.borderColor, cs.boxShadow, cs.outlineStyle].join(" ");
  });
}

function draftOf(page, id) {
  return page.evaluate(async (instance) => (await fetch(`/assistants/${instance}/draft`, { headers: { Accept: "application/json" } })).json(), id);
}

// waitStored waits until the assistant's draft holds want for one block, one
// [option, words] per question, [option, options, words] where it took
// several, nothing for an empty block.
async function waitStored(page, id, messageID, want) {
  let got = null;
  for (const until = Date.now() + 10000; Date.now() < until; await sleep(150)) {
    got = ((await draftOf(page, id)).answers || {})[messageID]?.map((p) => (p.options ? [p.option, p.options, p.text || ""] : [p.option, p.text || ""])) || [];
    if (JSON.stringify(got) === JSON.stringify(want)) return;
  }
  throw new Error(`the draft holds ${JSON.stringify(got)} for ${messageID}, want ${JSON.stringify(want)}`);
}

// blockState reads one block as the page shows it, or as html serves it.
function blockState(page, messageID, html = "") {
  return page.evaluate(([sel, id, served]) => {
    const root = served ? new DOMParser().parseFromString(served, "text/html") : document;
    const block = root.querySelector(`${sel}[message-id="${id}"]`);
    return {
      checked: [...block.querySelectorAll("input:checked")].map((i) => i.name + "=" + i.value),
      notes: [...block.querySelectorAll("textarea")].map((t) => t.value),
      count: block.querySelector("[data-choices-count]")?.textContent.trim(),
      send: block.querySelector("[data-choices-send]")?.hasAttribute("disabled"),
    };
  }, [BLOCK, messageID, html]);
}

function picks(page) {
  return page.evaluate((sel) => ({
    checked: [...document.querySelectorAll(`${sel} input:checked`)].map((i) => i.name + "=" + i.value),
    count: document.querySelector(`${sel} [data-choices-count]`)?.textContent.trim(),
    send: document.querySelector(`${sel} [data-choices-send]`)?.disabled,
  }), BLOCK);
}

// rowAfter reads what stands right under the asked message: the user's
// answered row, its words and how tall it is.
function rowAfter(page, askedID) {
  return page.evaluate((id) => {
    const row = document.getElementById(`message-${id}`)?.nextElementSibling;
    if (!row) return null;
    const time = row.querySelector("dc-time");
    return {
      role: row.getAttribute("data-role"),
      answered: Boolean(row.querySelector("[data-assistant-answered]")),
      text: row.textContent.replace(/\s+/g, " ").trim(),
      time: time?.textContent.trim() || "",
      at: Date.parse(time?.getAttribute("datetime") || ""),
      words: row.querySelectorAll("[data-assistant-text]").length,
      height: Math.round(row.getBoundingClientRect().height),
      stripe: getComputedStyle(row).borderLeftColor,
      userStripe: getComputedStyle(document.querySelector('[data-role="user"]:not(:has([data-assistant-answered]))') || row).borderLeftColor,
    };
  }, askedID);
}

// phoneFit measures the last block where it stands: whether the thread scrolls
// sideways, and every option, too small to tap or reaching past its button or
// the block.
function phoneFit(page) {
  return page.evaluate(([sel, min]) => {
    const scroller = document.querySelector("[data-assistant-scroll]");
    const blocks = document.querySelectorAll(sel);
    const block = blocks[blocks.length - 1];
    const box = block.getBoundingClientRect();
    const labels = [...block.querySelectorAll("[data-choices-option]")].map((label) => ({ box: label.getBoundingClientRect(), spill: label.scrollWidth > label.clientWidth + 1 }));
    return {
      sideways: scroller.scrollWidth > scroller.clientWidth + 1,
      right: box.right,
      width: window.innerWidth,
      heights: labels.map((l) => Math.round(l.box.height)),
      small: labels.filter((l) => l.box.height < min).length,
      outside: labels.filter((l) => l.spill || l.box.left < box.left || l.box.right > box.right).length,
      checked: block.querySelector("input:checked")?.value,
    };
  }, [BLOCK, MIN_TARGET]);
}

// buttonSizes reads Send and the first option of the last block, what makes a
// button's size and the height it ends at.
function buttonSizes(page) {
  return page.evaluate((sel) => {
    const blocks = document.querySelectorAll(sel);
    const block = blocks[blocks.length - 1];
    const read = (el) => {
      const cs = getComputedStyle(el);
      return { height: el.getBoundingClientRect().height, font: cs.fontSize, padding: `${cs.paddingTop} ${cs.paddingRight}`, radius: cs.borderTopLeftRadius };
    };
    return { send: read(block.querySelector("[data-choices-send]")), option: read(block.querySelector("[data-choices-option]")) };
  }, BLOCK);
}

function sameSize({ send, option }) {
  return Math.abs(send.height - option.height) <= 1 && send.font === option.font && send.padding === option.padding && send.radius === option.radius;
}

// countAnswers counts the sends of answers the page posts from now on, the
// drafts saved on the way left out. The returned function stops counting.
function countAnswers(page) {
  let sends = 0;
  const onRequest = (request) => {
    if (request.method() === "POST" && new URLSearchParams(request.postData() || "").get("form") === "answers") sends += 1;
  };
  page.on("request", onRequest);
  return () => {
    page.off("request", onRequest);
    return sends;
  };
}

// watchUserBubbles counts every bubble of the user that turns up in the page
// from now on and was not there before, however briefly it stands.
function watchUserBubbles(page) {
  return page.evaluate(() => {
    const known = new Set([...document.querySelectorAll('[data-role="user"]')].map((node) => node.getAttribute("data-message-id")));
    window.dcChoicesUsers = [];
    window.dcChoicesWatch = new MutationObserver((records) => {
      for (const record of records) {
        for (const node of record.addedNodes) {
          if (node.nodeType !== 1) continue;
          const users = node.matches('[data-role="user"]') ? [node] : [...node.querySelectorAll('[data-role="user"]')];
          for (const user of users) {
            if (!known.has(user.getAttribute("data-message-id"))) window.dcChoicesUsers.push(user.textContent.trim().slice(0, 80));
          }
        }
      }
    });
    window.dcChoicesWatch.observe(document.body, { childList: true, subtree: true });
  });
}

function userBubblesSeen(page) {
  return page.evaluate(() => {
    window.dcChoicesWatch?.disconnect();
    return window.dcChoicesUsers || [];
  });
}

// ask sends the prompt the fake streams back and watches the bubble while it
// grows: never a block, never a broken page, the questions as text.
async function ask(page, idle, prompt = PROMPT) {
  await idle(page);
  const before = await page.locator('[data-role="assistant"]').count();
  await page.fill("[data-assistant-input]", prompt);
  await page.click("[data-assistant-send]");
  await page.waitForFunction((n) => document.querySelectorAll('[data-role="assistant"]').length > n, before, { timeout: 20000 });
  const seen = { streamingTexts: 0, block: false, questionAsText: false };
  for (let i = 0; i < 200; i += 1) {
    const look = await page.evaluate((sel) => {
      const nodes = document.querySelectorAll('[data-role="assistant"]');
      const last = nodes[nodes.length - 1];
      return { state: last?.getAttribute("data-state"), block: Boolean(last?.querySelector(sel)), text: last?.querySelector("[data-assistant-text]")?.textContent || "" };
    }, BLOCK);
    if (look.state !== "streaming") break;
    seen.streamingTexts += 1;
    if (look.block) seen.block = true;
    if (look.text.includes("Which database")) seen.questionAsText = true;
    await sleep(100);
  }
  await waitState(page, "open");
  return seen;
}

async function choicesChecks({ page, run, mobilePage, helpers }) {
  const { openAssistant, useClaude, newConversation, idle, postForm, closePanel } = helpers;
  let id = "";
  let askedID = "";
  let sentText = "";

  await run("an answer's questions stream in as text and settle into a block with buttons", async () => {
    await openAssistant(page);
    await useClaude(page);
    id = await newConversation(page);
    const seen = await ask(page, idle);
    assert(seen.streamingTexts > 2, `the answer never streamed: ${JSON.stringify(seen)}`);
    assert(!seen.block, "a block stood in the bubble while the answer streamed");
    assert(seen.questionAsText, "the half arrived block did not show as text");
    const bubble = page.locator('[data-role="assistant"]').last();
    askedID = await bubble.getAttribute("data-message-id");
    const texts = await bubble.locator("[data-assistant-text]").allTextContents();
    assert(texts.length === 2 && texts[0].includes("Two things to decide first.") && texts[1].includes("Everything else waits for that."), `the text around the block is not in place: ${JSON.stringify(texts)}`);
    assert(await bubble.locator("code.language-ask").count() === 0, "the block also stands as code");
    const questions = await page.locator(`${BLOCK} [data-choices-question] > .fw-medium`).allTextContents();
    assert(JSON.stringify(questions) === JSON.stringify([QUESTIONS[0], QUESTIONS[3]]), `unexpected questions: ${JSON.stringify(questions)}`);
    for (const [q, o, label] of [[0, 0, "PostgreSQL"], [0, 1, "SQLite"], [1, 0, "Yes"], [1, 1, "No, after the review"]]) {
      assert((await option(page, q, o).textContent()).trim() === label, `option ${q}/${o} is not ${label}`);
      assert(await visible(option(page, q, o)), `option ${label} is not visible as a button`);
    }
    assert(await visible(note(page, 0)) && await visible(note(page, 1)), "a question has no field for a note");
    assert(await sendDisabled(page), "Send is enabled before anything was answered");
    const size = await buttonSizes(page);
    assert(sameSize(size), `Send is not the size of an option: ${JSON.stringify(size)}`);
    return `${seen.streamingTexts} streaming reads without a block, then two questions with two buttons each, Send ${Math.round(size.send.height)}px high like them`;
  });

  await run("on a phone a pick taken back or left for another option looks untouched and is gone, a mouse still hovers", async () => {
    const mp = await mobilePage();
    await openAssistant(mp, id);
    await waitState(mp, "open");
    const untouched = await look(option(mp, 0, 0));
    await option(mp, 0, 0).tap();
    const picked = await look(option(mp, 0, 0));
    assert(picked !== untouched && JSON.stringify((await picks(mp)).checked) === '["pick-0=0"]', `a tap did not pick: ${picked}`);
    await option(mp, 0, 0).tap();
    const takenBack = await look(option(mp, 0, 0));
    const gone = await picks(mp);
    assert(takenBack === untouched, `the option taken back reads ${takenBack}, untouched ${untouched}`);
    assert(gone.checked.length === 0 && gone.count === "0 of 2 answered" && gone.send, `the pick taken back is still there: ${JSON.stringify(gone)}`);
    await option(mp, 0, 0).tap();
    await option(mp, 0, 1).tap();
    const left = await look(option(mp, 0, 0));
    const moved = await picks(mp);
    assert(left === untouched && JSON.stringify(moved.checked) === '["pick-0=1"]', `the option left for another reads ${left}, picks ${JSON.stringify(moved)}`);
    await option(mp, 0, 1).tap();
    assert((await picks(mp)).send, "Send is enabled with nothing picked");
    await waitStored(mp, id, askedID, []);
    await closePanel(mp);

    await openAssistant(page, id);
    await waitState(page, "open");
    const plain = await look(option(page, 0, 0));
    await option(page, 0, 0).hover();
    const hovered = await look(option(page, 0, 0));
    await page.mouse.move(0, 0);
    assert(hovered !== plain, `a mouse over an option shows no hover: ${hovered}`);
    return "taken back and left both read like untouched, the pick gone and Send off, a mouse hover still shows";
  });

  await run("on a phone the open block fits, with an option picked and notes typed, Send the size of an option", async () => {
    const mp = await mobilePage();
    await openAssistant(mp, id);
    await waitState(mp, "open");
    await option(mp, 0, 0).tap();
    await note(mp, 0).fill("Version 16, the managed one");
    await note(mp, 1).fill("After the review");
    await mp.waitForFunction((sel) => document.querySelector(`${sel} [data-choices-count]`)?.textContent.includes("2 of 2"), BLOCK, { timeout: 5000 });
    await mp.locator(BLOCK).scrollIntoViewIfNeeded();
    const fit = await phoneFit(mp);
    assert(!fit.sideways && fit.right <= fit.width && fit.outside === 0, `the block does not fit the phone: ${JSON.stringify(fit)}`);
    assert(fit.small === 0, `options are too small to tap: ${JSON.stringify(fit.heights)}`);
    assert(fit.checked === "0", "the tapped option is not picked");
    const size = await buttonSizes(mp);
    assert(sameSize(size), `Send is not the size of an option: ${JSON.stringify(size)}`);
    if (process.env.SHOTS_DIR) await mp.locator(BLOCK).screenshot({ path: path.join(process.env.SHOTS_DIR, "assistant-choices.png") });
    await option(mp, 0, 0).tap();
    await note(mp, 0).fill("");
    await note(mp, 1).fill("");
    await waitStored(mp, id, askedID, []);
    await closePanel(mp);
    return `fits at 390px, options ${fit.heights.join("/")}px high, Send ${Math.round(size.send.height)}px`;
  });

  await run("Send stays off until every question has an option or words, a second tap takes a pick back, Ctrl+Enter sends nothing then and Enter makes a new line", async () => {
    await openAssistant(page, id);
    await waitState(page, "open");
    await option(page, 0, 0).click();
    assert(await sendDisabled(page) && await countText(page) === "1 of 2 answered", `one answer enables Send: ${await countText(page)}`);
    await option(page, 0, 0).click();
    const unpicked = await page.locator(`${BLOCK} input:checked`).count();
    assert(unpicked === 0 && await countText(page) === "0 of 2 answered", `a second tap left the pick: ${unpicked}`);
    await option(page, 0, 0).click();
    await note(page, 0).fill("Version 16, the managed one");
    assert(await sendDisabled(page), "Send is enabled with the second question open");
    const early = countAnswers(page);
    await note(page, 0).press("Control+Enter");
    await note(page, 0).press("Meta+Enter");
    await note(page, 0).press("Enter");
    await sleep(500);
    const typed = await note(page, 0).inputValue();
    const sends = early();
    assert(sends === 0 && await state(page) === "open" && await sendDisabled(page), `Ctrl+Enter sent while Send was off: ${sends} sends, ${await state(page)}`);
    assert(typed === "Version 16, the managed one\n", `Ctrl+Enter or Enter typed the wrong thing: ${JSON.stringify(typed)}`);
    await note(page, 0).fill("Version 16, the managed one");
    await note(page, 1).fill("   ");
    assert(await sendDisabled(page), "blanks count as an answer");
    await note(page, 1).fill("After the review on Friday, not before");
    assert(!(await sendDisabled(page)) && await countText(page) === "2 of 2 answered", "an option plus words do not enable Send");
    await waitStored(page, id, askedID, [[0, "Version 16, the managed one"], [-1, "After the review on Friday, not before"]]);
    await page.reload({ waitUntil: "domcontentloaded" });
    await waitState(page, "open");
    const kept = await page.evaluate((sel) => ({
      pick: document.querySelector(`${sel} input:checked`)?.value,
      notes: [...document.querySelectorAll(`${sel} textarea`)].map((t) => t.value),
      disabled: document.querySelector(`${sel} [data-choices-send]`).disabled,
    }), BLOCK);
    assert(kept.pick === "0" && kept.notes[0] === "Version 16, the managed one" && kept.notes[1] === "After the review on Friday, not before" && !kept.disabled,
      `a reload lost what was picked and typed: ${JSON.stringify(kept)}`);
    return "disabled at 0 and 1 of 2 and on blanks, enabled at 2 of 2, in the draft and kept across a reload";
  });

  await run("Ctrl+Enter in a note answers the questions in the block once, the user's message for it is one slim answered row under it", async () => {
    const users = await page.locator('[data-role="user"]').count();
    const sentAt = Date.now();
    await watchUserBubbles(page);
    const answers = countAnswers(page);
    await note(page, 1).focus();
    await page.keyboard.down("Control");
    await page.keyboard.press("Enter");
    await page.keyboard.press("Enter");
    await page.keyboard.up("Control");
    await waitState(page, "answered");
    await waitStored(page, id, askedID, []);
    await idle(page);
    const reply = await page.locator('[data-role="assistant"]').last().textContent();
    assert(reply.includes("Fake claude answer."), "the assistant did not answer the answers");
    const seen = await userBubblesSeen(page);
    const sends = answers();
    assert(sends === 1, `Ctrl+Enter pressed twice sent the answers ${sends} times`);
    assert(seen.length === 1 && /answered$/.test(seen[0]), `want one answered row of the user after the send, saw ${JSON.stringify(seen)}`);
    assert(await page.locator('[data-role="user"]').count() === users + 1, "the thread does not hold one message of the user for the answers");
    const row = await rowAfter(page, askedID);
    assert(row && row.role === "user" && row.answered && row.words === 0 && row.text === `You · ${row.time} · answered` && Math.abs(row.at - sentAt) < 60000,
      `the answered row is not right under the block: ${JSON.stringify(row)}`);
    assert(row.height < 40 && row.stripe === row.userStripe, `the row is not slim in the user's stripe: ${JSON.stringify(row)}`);
    const transcript = await page.evaluate(async (instance) => (await fetch(`/assistants/instances/${instance}?full=1&entries=50`, { headers: { Accept: "application/json" } })).json(), id);
    const stored = transcript.messages.filter((m) => m.role === "user");
    sentText = stored[stored.length - 1]?.content || "";
    const want = [
      "Which database should the app use?",
      "Answer: PostgreSQL",
      "Note: Version 16, the managed one",
      "",
      "Deploy right away?",
      "Answer in my own words: After the review on Friday, not before",
    ].join("\n");
    assert(sentText === want, `the assistant reads ${JSON.stringify(sentText)}`);
    const over = await page.evaluate((sel) => {
      const block = document.querySelector(sel);
      return {
        fieldset: block.querySelector("fieldset").disabled,
        send: block.querySelectorAll("[data-choices-send], textarea").length,
        checked: [...block.querySelectorAll("input:checked")].map((i) => i.name + "=" + i.value),
        notes: [...block.querySelectorAll("[data-choices-note]")].map((n) => n.textContent.trim()),
        over: block.querySelectorAll("[data-choices-over]").length,
      };
    }, BLOCK);
    assert(over.fieldset && over.send === 0 && JSON.stringify(over.checked) === '["pick-0=0"]', `the block is not over: ${JSON.stringify(over)}`);
    assert(over.over === 0, `the block still carries an answered line: ${JSON.stringify(over)}`);
    assert(JSON.stringify(over.notes) === JSON.stringify(["Version 16, the managed one", "After the review on Friday, not before"]), `the notes are not shown: ${JSON.stringify(over.notes)}`);
    const picked = page.locator(`${BLOCK} input:checked + label`);
    assert(await visible(picked) && (await picked.textContent()).trim() === "PostgreSQL", "the picked option is not shown");
    if (process.env.SHOTS_DIR) {
      await page.locator(BLOCK).screenshot({ path: path.join(process.env.SHOTS_DIR, "assistant-choices-answered.png") });
      const mp = await mobilePage();
      await openAssistant(mp, id);
      await waitState(mp, "answered");
      await mp.setViewportSize({ width: 390, height: 2400 });
      await mp.locator(`[id="message-${askedID}"]`).scrollIntoViewIfNeeded();
      const clip = await mp.evaluate((asked) => {
        const top = document.getElementById(`message-${asked}`).getBoundingClientRect().top - 8;
        const bottom = document.getElementById(`message-${asked}`).nextElementSibling.getBoundingClientRect().bottom + 8;
        return { x: 0, y: Math.max(0, top), width: window.innerWidth, height: bottom - Math.max(0, top) };
      }, askedID);
      await mp.screenshot({ path: path.join(process.env.SHOTS_DIR, "assistant-choices-answered-row.png"), clip });
      await mp.setViewportSize({ width: 390, height: 844 });
      await closePanel(mp);
    }
    return `"${row.text}" ${row.height}px under the block, the assistant read ${JSON.stringify(sentText)}`;
  });

  await run("an answered block stays over after a reload and takes no second send", async () => {
    await page.reload({ waitUntil: "domcontentloaded" });
    await page.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
    await waitState(page, "answered");
    const after = await page.evaluate((sel) => {
      const block = document.querySelector(sel);
      return { fieldset: block.querySelector("fieldset").disabled, send: block.querySelectorAll("[data-choices-send]").length, checked: [...block.querySelectorAll("input:checked")].map((i) => i.value) };
    }, BLOCK);
    assert(after.fieldset && after.send === 0 && JSON.stringify(after.checked) === '["0"]', `the reload lost the answered block: ${JSON.stringify(after)}`);
    const users = await page.locator('[data-role="user"]').count();
    const row = await rowAfter(page, askedID);
    assert(users === 2 && row?.answered && row.text === `You · ${row.time} · answered`, `after the reload the thread holds ${users} messages of the user and reads ${JSON.stringify(row)}`);
    const again = await postForm(page, id, { form: "answers", message_id: askedID, "pick-0": "1", "pick-1": "0" });
    assert(again.status === 400 && /answered already/.test(again.body.error || ""), `a second send was not refused: ${JSON.stringify(again)}`);
    const bad = await postForm(page, id, { form: "answers", message_id: "nope", "pick-0": "x" });
    assert(bad.status === 400, `a hand built answer was not refused: ${JSON.stringify(bad)}`);
    await sleep(500);
    assert(await page.locator('[data-role="user"]').count() === users, "a refused send wrote a message");
    return "answered after the reload, a second send and a bad one refused with 400";
  });

  await run("on a phone options that are long paths wrap inside their buttons", async () => {
    await ask(page, idle, LONG_PROMPT);
    const mp = await mobilePage();
    await openAssistant(mp, id);
    await waitState(mp, "open");
    await mp.locator(BLOCK).last().scrollIntoViewIfNeeded();
    const fit = await phoneFit(mp);
    assert(!fit.sideways && fit.right <= fit.width && fit.outside === 0, `a long option does not fit the phone: ${JSON.stringify(fit)}`);
    assert(fit.small === 0, `options are too small to tap: ${JSON.stringify(fit.heights)}`);
    const labels = (await mp.locator(BLOCK).last().locator("[data-choices-option]").allTextContents()).map((t) => t.trim());
    assert(JSON.stringify(labels) === JSON.stringify(PATHS), `the paths do not read whole: ${JSON.stringify(labels)}`);
    if (process.env.SHOTS_DIR) {
      await mp.setViewportSize({ width: 390, height: 2400 });
      await mp.locator(BLOCK).first().scrollIntoViewIfNeeded();
      const clip = await mp.evaluate((sel) => {
        const blocks = document.querySelectorAll(sel);
        const top = blocks[0].getBoundingClientRect().top - 8;
        const bottom = blocks[blocks.length - 1].getBoundingClientRect().bottom + 8;
        return { x: 0, y: Math.max(0, top), width: window.innerWidth, height: bottom - Math.max(0, top) };
      }, BLOCK);
      await mp.screenshot({ path: path.join(process.env.SHOTS_DIR, "assistant-choices-states.png"), clip });
      await mp.setViewportSize({ width: 390, height: 844 });
    }
    await closePanel(mp);
    return `both paths whole inside their buttons, ${fit.heights.join("/")}px high`;
  });

  await run("a thread that moves on past a block leaves it behind with its card and no answered row, live and after a reload", async () => {
    await openAssistant(page, id);
    await waitState(page, "open");
    const passedID = await page.locator(BLOCK).last().getAttribute("message-id");
    await page.locator(BLOCK).last().locator("[data-choices-option]").first().click();
    await waitStored(page, id, passedID, [[0, ""]]);
    await page.fill("[data-assistant-input]", "Never mind, something else");
    await page.click("[data-assistant-send]");
    await waitState(page, "passed");
    await waitStored(page, id, passedID, []);
    await page.waitForFunction((asked) => {
      const next = document.getElementById(`message-${asked}`)?.nextElementSibling;
      return next?.getAttribute("data-role") === "user" && Boolean(next.querySelector("[data-assistant-text]"));
    }, passedID, { timeout: 15000 });
    const read = () => page.evaluate(([sel, asked]) => {
      const blocks = document.querySelectorAll(sel);
      const b = blocks[blocks.length - 1];
      const card = b.querySelector("[data-choices-question]");
      const next = document.getElementById(`message-${asked}`)?.nextElementSibling;
      return {
        fieldset: b.querySelector("fieldset").disabled,
        send: b.querySelectorAll("[data-choices-send]").length,
        options: b.querySelectorAll("[data-choices-option]").length,
        card: Boolean(card) && getComputedStyle(card).borderTopStyle !== "none" && parseFloat(getComputedStyle(card).borderTopWidth) > 0,
        over: b.querySelector("[data-choices-over]")?.textContent.trim(),
        rows: document.querySelectorAll("[data-assistant-answered]").length,
        next: next ? { role: next.getAttribute("data-role"), answered: Boolean(next.querySelector("[data-assistant-answered]")), text: next.querySelector("[data-assistant-text]")?.textContent.trim() } : null,
      };
    }, [BLOCK, passedID]);
    const want = (got) => got.fieldset && got.send === 0 && got.options === 2 && got.card && got.over === "Not answered" && got.rows === 1
      && got.next?.role === "user" && !got.next.answered && got.next.text === "Never mind, something else";
    const live = await read();
    assert(want(live), `the block did not end live as a card with its own message under it: ${JSON.stringify(live)}`);
    await idle(page);
    await page.reload({ waitUntil: "domcontentloaded" });
    await page.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
    await waitState(page, "passed");
    const reloaded = await read();
    assert(want(reloaded), `after a reload the passed block reads ${JSON.stringify(reloaded)}`);
    return "card, inactive buttons and Not answered, its draft entry gone, the user's own message under it, no answered row, live and after a reload";
  });

  await run("picks and notes of one browser show in a second one live and after its reload, a send there answers the block here and keeps the words typed here", async () => {
    await openAssistant(page, id);
    await ask(page, idle);
    const blockID = await page.locator(BLOCK).last().getAttribute("message-id");
    const words = "Half a thought, still mine";
    await page.fill("[data-assistant-input]", words);
    for (const until = Date.now() + 10000; (await draftOf(page, id)).text !== words; await sleep(150)) {
      assert(Date.now() < until, "the composer's words never reached the draft");
    }
    const mp = await mobilePage();
    await openAssistant(mp, id);
    await waitState(mp, "open");
    const empty = await blockState(mp, blockID);
    assert(empty.checked.length === 0 && empty.notes.join("") === "" && empty.send, `the second browser's block does not start empty: ${JSON.stringify(empty)}`);
    const question = (q) => page.locator(`${BLOCK}[message-id="${blockID}"] [data-choices-question]`).nth(q);
    await question(0).locator("[data-choices-option]").nth(1).click();
    await question(0).locator("textarea").fill("Embedded is enough");
    await question(1).locator("textarea").fill("Next week");
    const want = JSON.stringify({ checked: ["pick-0=1"], notes: ["Embedded is enough", "Next week"], count: "2 of 2 answered", send: false });
    const t0 = Date.now();
    let live = null;
    for (const until = t0 + 10000; JSON.stringify(live) !== want; await sleep(100)) {
      assert(Date.now() < until, `the second browser does not follow live: ${JSON.stringify(live)}`);
      live = await blockState(mp, blockID);
    }
    const ms = Date.now() - t0;
    const served = await blockState(mp, blockID, await mp.evaluate(async (p) => (await fetch(p, { headers: { Accept: "text/html" } })).text(), `/assistants/${id}`));
    assert(JSON.stringify(served) === want, `the page is not served filled: ${JSON.stringify(served)}`);
    await mp.reload({ waitUntil: "domcontentloaded" });
    await waitState(mp, "open");
    const reloaded = await blockState(mp, blockID);
    assert(JSON.stringify(reloaded) === want, `after its reload the second browser reads ${JSON.stringify(reloaded)}`);
    await mp.locator(`${BLOCK}[message-id="${blockID}"] [data-choices-send]`).tap();
    await waitState(mp, "answered");
    await waitState(page, "answered");
    await waitStored(page, id, blockID, []);
    assert(await page.inputValue("[data-assistant-input]") === words, `the composer here lost its words: ${await page.inputValue("[data-assistant-input]")}`);
    await idle(page);
    await page.reload({ waitUntil: "domcontentloaded" });
    await page.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
    const kept = { box: await page.inputValue("[data-assistant-input]"), draft: (await draftOf(page, id)).text };
    assert(kept.box === words && kept.draft === words, `the words typed here are gone after the send there: ${JSON.stringify(kept)}`);
    await closePanel(mp);
    return `live in ${ms}ms, served filled and kept across a reload, answered here after the send there, the composer's words kept`;
  });

  let mixedID = "";
  const mixedOption = (p, q, o) => p.locator(`${BLOCK}[message-id="${mixedID}"] [data-choices-question]`).nth(q).locator("[data-choices-option]").nth(o);
  const mixedNote = (p, q) => p.locator(`${BLOCK}[message-id="${mixedID}"] [data-choices-question]`).nth(q).locator("textarea");
  const twoPicks = ["picks-0=0", "picks-0=2", "pick-1=0"];

  await run("a mixed block toggles several picks on their own and takes one back, drawn like the single ones, a hand built list that does not fit is refused", async () => {
    await openAssistant(page, id);
    await ask(page, idle, MIXED_PROMPT);
    mixedID = await page.locator(BLOCK).last().getAttribute("message-id");
    const kinds = await page.locator(`${BLOCK}[message-id="${mixedID}"] [data-choices-question]`).evaluateAll((qs) => qs.map((q) => [...q.querySelectorAll("input")].map((i) => i.type).join(",")));
    assert(JSON.stringify(kinds) === '["checkbox,checkbox,checkbox","radio,radio"]', `the questions are not one of several picks and one of one: ${JSON.stringify(kinds)}`);
    await page.mouse.move(0, 0);
    assert(await look(mixedOption(page, 0, 0)) === await look(mixedOption(page, 1, 0)), "an option of several does not look like an option of one");
    for (const o of [0, 1, 2]) await mixedOption(page, 0, o).click();
    const all = await blockState(page, mixedID);
    assert(JSON.stringify(all.checked) === '["picks-0=0","picks-0=1","picks-0=2"]' && all.count === "1 of 2 answered" && all.send, `three picks did not stand together: ${JSON.stringify(all)}`);
    await mixedOption(page, 0, 1).click();
    await mixedOption(page, 1, 0).click();
    await page.mouse.move(0, 0);
    const two = await blockState(page, mixedID);
    assert(JSON.stringify(two.checked) === JSON.stringify(twoPicks) && two.count === "2 of 2 answered" && !two.send, `taking one back left ${JSON.stringify(two)}`);
    assert(await look(mixedOption(page, 0, 0)) === await look(mixedOption(page, 1, 0)), "a pick of several does not look like a pick of one");
    await waitStored(page, id, mixedID, [[-1, [0, 2], ""], [0, ""]]);
    for (const extra of [[["picks-0", "1"], ["picks-0", "1"]], [["pick-0", "1"]], [["picks-1", "0"], ["picks-1", "1"]], [["picks-0", "3"]]]) {
      const bad = await postForm(page, id, [["form", "answers-draft"], ["message_id", mixedID], ...extra]);
      assert(bad.status === 400, `a hand built draft ${JSON.stringify(extra)} answered ${bad.status}`);
    }
    await waitStored(page, id, mixedID, [[-1, [0, 2], ""], [0, ""]]);
    return "checkboxes beside radios, three picks then two, the draft holding [0, 2], four hand built lists refused";
  });

  await run("the picks of a question that takes several hand over to a phone and back live, a pick taken back there looks untouched, Ctrl+Enter sends every pick", async () => {
    const mp = await mobilePage();
    await openAssistant(mp, id);
    await waitState(mp, "open");
    const served = await blockState(mp, mixedID);
    assert(JSON.stringify(served.checked) === JSON.stringify(twoPicks) && served.count === "2 of 2 answered" && !served.send, `the phone does not open on the picks: ${JSON.stringify(served)}`);
    await mp.locator(`${BLOCK}[message-id="${mixedID}"]`).scrollIntoViewIfNeeded();
    const fit = await phoneFit(mp);
    assert(!fit.sideways && fit.right <= fit.width && fit.outside === 0 && fit.small === 0, `the mixed block does not fit the phone: ${JSON.stringify(fit)}`);
    if (process.env.SHOTS_DIR) {
      const clip = await mp.evaluate((sel) => {
        const box = document.querySelector(sel).getBoundingClientRect();
        return { x: 0, y: Math.max(0, box.top - 8), width: window.innerWidth, height: box.height + 16 };
      }, `${BLOCK}[message-id="${mixedID}"]`);
      await mp.screenshot({ path: path.join(process.env.SHOTS_DIR, "ask-multi.png"), clip });
    }
    const untouched = await look(mixedOption(mp, 0, 1));
    const follows = async (p, want, what) => {
      let got = null;
      for (const until = Date.now() + 10000; JSON.stringify(got) !== JSON.stringify(want); await sleep(100)) {
        assert(Date.now() < until, `${what}: ${JSON.stringify(got)}`);
        got = (await blockState(p, mixedID)).checked;
      }
    };
    await mixedOption(mp, 0, 1).tap();
    await follows(page, ["picks-0=0", "picks-0=1", "picks-0=2", "pick-1=0"], "the desktop does not follow the phone's pick");
    await mixedOption(mp, 0, 1).tap();
    assert(await look(mixedOption(mp, 0, 1)) === untouched, "the pick taken back on the phone does not look untouched");
    await follows(page, twoPicks, "the desktop does not follow the pick the phone took back");
    await mixedNote(page, 0).fill("Before Friday");
    for (const until = Date.now() + 10000; (await blockState(mp, mixedID)).notes[0] !== "Before Friday"; await sleep(100)) {
      assert(Date.now() < until, "the phone does not follow the desktop's note");
    }
    const sends = countAnswers(page);
    await mixedNote(page, 0).press("Control+Enter");
    await waitState(page, "answered");
    await waitState(mp, "answered");
    await waitStored(page, id, mixedID, []);
    await idle(page);
    assert(sends() === 1, "Ctrl+Enter did not send the answers once");
    const transcript = await page.evaluate(async (instance) => (await fetch(`/assistants/instances/${instance}?full=1&entries=50`, { headers: { Accept: "application/json" } })).json(), id);
    const stored = transcript.messages.filter((m) => m.role === "user");
    const text = stored[stored.length - 1]?.content || "";
    const want = "Which checks should run before the deploy?\n- [x] Unit tests\n- [ ] Lint\n- [x] End to end\nNote: Before Friday\n\nDeploy right away?\nAnswer: Yes";
    assert(text === want, `the assistant reads ${JSON.stringify(text)}`);
    const over = await page.evaluate(([sel, asked]) => {
      const block = document.querySelector(`${sel}[message-id="${asked}"]`);
      return {
        fieldset: block.querySelector("fieldset").disabled,
        fields: block.querySelectorAll("[data-choices-send], textarea").length,
        checked: [...block.querySelectorAll("input:checked")].map((i) => i.name + "=" + i.value),
        notes: [...block.querySelectorAll("[data-choices-note]")].map((n) => n.textContent.trim()),
      };
    }, [BLOCK, mixedID]);
    assert(over.fieldset && over.fields === 0 && JSON.stringify(over.checked) === JSON.stringify(twoPicks) && JSON.stringify(over.notes) === '["Before Friday"]', `the answered block reads ${JSON.stringify(over)}`);
    const row = await rowAfter(page, mixedID);
    assert(row?.answered && row.text === `You · ${row.time} · answered`, `the answered row reads ${JSON.stringify(row)}`);
    await closePanel(mp);
    return `served with both picks, followed live both ways, answered with ${JSON.stringify(text)}`;
  });

  // sentFromAbove asks a block with a tall text under it, picks on p, scrolls
  // up from the end until Send stands just above the thread's bottom edge,
  // sends there and reads where the thread stands once the reply is in.
  const sentFromAbove = async (p, act) => {
    await openAssistant(page, id);
    await ask(page, idle, TAIL_PROMPT);
    const blockID = await page.locator(BLOCK).last().getAttribute("message-id");
    if (p !== page) {
      await openAssistant(p, id);
      await waitState(p, "open");
    }
    const block = p.locator(`${BLOCK}[message-id="${blockID}"]`);
    const question = (q) => block.locator("[data-choices-question]").nth(q);
    for (const q of [0, 1]) {
      const label = question(q).locator("[data-choices-option]").first();
      await (p === page ? label.click() : label.tap());
    }
    await sleep(300);
    const away = await p.evaluate(async (sel) => {
      const scroller = document.querySelector("[data-assistant-scroll]");
      const send = () => document.querySelector(`${sel} [data-choices-send]`).getBoundingClientRect();
      scroller.scrollTop += send().bottom - (scroller.getBoundingClientRect().bottom - 40);
      await new Promise((done) => requestAnimationFrame(() => requestAnimationFrame(done)));
      const view = scroller.getBoundingClientRect();
      return {
        rest: Math.round(scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight),
        sendInView: send().top >= view.top && send().bottom <= view.bottom,
      };
    }, `${BLOCK}[message-id="${blockID}"]`);
    const endButton = p.locator("dc-assistant [data-chat-end]");
    assert(away.rest > 300 && away.sendInView && await visible(endButton), `the reader does not stand above the end with Send in view: ${JSON.stringify(away)}`);
    const before = await p.locator('[data-role="assistant"]').count();
    await act(p, block, question);
    await waitState(p, "answered");
    await p.waitForFunction((n) => {
      const nodes = document.querySelectorAll('[data-role="assistant"]');
      return nodes.length > n && nodes[nodes.length - 1].getAttribute("data-state") === "complete";
    }, before, { timeout: 30000 });
    await idle(p);
    const end = await p.evaluate(async () => {
      await new Promise((done) => requestAnimationFrame(() => requestAnimationFrame(done)));
      const scroller = document.querySelector("[data-assistant-scroll]");
      const nodes = document.querySelectorAll('[data-role="assistant"]');
      return {
        rest: Math.round(scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight),
        reply: nodes[nodes.length - 1].textContent.includes("Fake claude answer."),
      };
    });
    const shown = await visible(endButton);
    assert(end.reply, "the assistant did not answer the answers");
    assert(end.rest <= 2 && !shown, `the thread stands ${end.rest}px above its end after the send from ${away.rest}px, the jump to end button ${shown ? "shown" : "hidden"}`);
    return `sent from ${away.rest}px above the end, the reply in at the end`;
  };

  await run("answers sent by Send from above the end jump the thread to its end and follow the reply", () =>
    sentFromAbove(page, (p, block) => block.locator("[data-choices-send]").click()));

  await run("answers sent by Ctrl+Enter in a note from above the end jump the thread to its end and follow the reply", () =>
    sentFromAbove(page, async (p, block, question) => {
      await question(1).locator("textarea").evaluate((el) => el.focus({ preventScroll: true }));
      await p.keyboard.press("Control+Enter");
    }));

  await run("on a phone answers sent by Send from above the end jump the thread to its end and follow the reply", async () => {
    const mp = await mobilePage();
    const read = await sentFromAbove(mp, (p, block) => block.locator("[data-choices-send]").tap());
    await closePanel(mp);
    return read;
  });

  await require("./assistant-grow").growChecks({ page, run, mobilePage, helpers: { ...helpers, ask: (target) => ask(target, idle) } });

  await run("the choices runner's assistant is deleted", async () => {
    if (!id) return "nothing to delete";
    const gone = await postForm(page, id, { form: "delete" });
    assert(gone.status === 200, `the assistant could not be deleted: ${gone.status}`);
    return "deleted";
  });

}

module.exports = { choicesChecks };
