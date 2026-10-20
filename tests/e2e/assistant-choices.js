const path = require("path");
const L = require("./lib");
const { assert, BASE, sleep } = L;

// Not a runner of its own: assistant.js runs these at its end, on an
// assistant of their own that they delete again. An answer ends in an ask
// block, which the fake claude streams back a line at a time (ECHO_ANSWER),
// and the page turns it into questions with their options as buttons and a
// field under each, answered with one send. The assistant reads the answers as
// the user's next message, the block shows the picks and that message stands
// as one slim row under it, "You · <time> · answered". A block the thread
// moved on past keeps its card and says Not answered. SHOTS_DIR with a mount
// takes the phone screenshots of an open block, of an answered one with its
// row and of an answered one above an open one.
// Gotchas:
// - the block is server rendered in every state, the element only answers an
//   open one, so a check reads data-choices-state, never the script,
// - a radio drawn as a button is visually hidden, a check clicks its label,
// - nothing in an answer may name a fake keyword (MAGIC, SLOW, FAIL, TOOL,
//   MODEL ...), the answer to the answers is the fake's plain line,
// - a tap leaves :hover on the option, sticky hover only shows in a context
//   with hasTouch and isMobile and a real tap, never a click.

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
    return `${seen.streamingTexts} streaming reads without a block, then two questions with two buttons each`;
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

  await run("on a phone the open block fits, with an option picked and a note typed", async () => {
    const mp = await mobilePage();
    await openAssistant(mp, id);
    await waitState(mp, "open");
    await option(mp, 0, 0).tap();
    await note(mp, 0).fill("Version 16, the managed one");
    await mp.waitForFunction((sel) => document.querySelector(`${sel} [data-choices-count]`)?.textContent.includes("1 of 2"), BLOCK, { timeout: 5000 });
    await mp.locator(BLOCK).scrollIntoViewIfNeeded();
    const fit = await phoneFit(mp);
    assert(!fit.sideways && fit.right <= fit.width && fit.outside === 0, `the block does not fit the phone: ${JSON.stringify(fit)}`);
    assert(fit.small === 0, `options are too small to tap: ${JSON.stringify(fit.heights)}`);
    assert(fit.checked === "0", "the tapped option is not picked");
    if (process.env.SHOTS_DIR) await mp.locator(BLOCK).screenshot({ path: path.join(process.env.SHOTS_DIR, "assistant-choices.png") });
    await closePanel(mp);
    return `fits at 390px, options ${fit.heights.join("/")}px high`;
  });

  await run("Send stays off until every question has an option or words, a second tap takes a pick back", async () => {
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
    await note(page, 1).fill("   ");
    assert(await sendDisabled(page), "blanks count as an answer");
    await note(page, 1).fill("After the review on Friday, not before");
    assert(!(await sendDisabled(page)) && await countText(page) === "2 of 2 answered", "an option plus words do not enable Send");
    await page.reload({ waitUntil: "domcontentloaded" });
    await waitState(page, "open");
    const kept = await page.evaluate((sel) => ({
      pick: document.querySelector(`${sel} input:checked`)?.value,
      notes: [...document.querySelectorAll(`${sel} textarea`)].map((t) => t.value),
      disabled: document.querySelector(`${sel} [data-choices-send]`).disabled,
    }), BLOCK);
    assert(kept.pick === "0" && kept.notes[0] === "Version 16, the managed one" && kept.notes[1] === "After the review on Friday, not before" && !kept.disabled,
      `a reload lost what was picked and typed: ${JSON.stringify(kept)}`);
    return "disabled at 0 and 1 of 2 and on blanks, enabled at 2 of 2, kept across a reload";
  });

  await run("one send answers the questions in the block, the user's message for it is one slim answered row under it", async () => {
    const users = await page.locator('[data-role="user"]').count();
    const sentAt = Date.now();
    await watchUserBubbles(page);
    await sendButton(page).click();
    await waitState(page, "answered");
    await idle(page);
    const reply = await page.locator('[data-role="assistant"]').last().textContent();
    assert(reply.includes("Fake claude answer."), "the assistant did not answer the answers");
    const seen = await userBubblesSeen(page);
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
    await page.fill("[data-assistant-input]", "Never mind, something else");
    await page.click("[data-assistant-send]");
    await waitState(page, "passed");
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
    return "card, inactive buttons and Not answered, the user's own message under it, no answered row, live and after a reload";
  });

  await run("the choices runner's assistant is deleted", async () => {
    if (!id) return "nothing to delete";
    const gone = await postForm(page, id, { form: "delete" });
    assert(gone.status === 200, `the assistant could not be deleted: ${gone.status}`);
    return "deleted";
  });

}

module.exports = { choicesChecks };
