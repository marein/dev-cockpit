const fs = require("fs");
const path = require("path");
const L = require("./lib");
const { assert, sleep } = L;

// The copy view of a terminal: the copy button of every footer shows the
// terminal's text in its place, on the attach pages and per pane in a split,
// and a close control brings the live terminal back. The terminal stays
// mounted and connected behind the view (the stage puts both into one grid
// cell), so opening and closing never resizes the pane or reconnects the
// stream. One element, terminal-copy, with two faces in one frame: a coder's
// recorded conversation as bubbles (chat_message.gohtml, the assistant's own
// partial; GET /coders/:id/conversation answers one page as an HTML fragment,
// the newest one, or with ?before=<message id> the one above it), and plain
// text, a shell's history in a picked number of lines or a coder's screen
// (GET /shells/:id/copy and /coders/:id/copy, JSON, the screen and nothing
// else for a coder, whatever the request carries). The view is
// a snapshot: it asks when it opens, when the face or the amount changes and
// when somebody wants older messages, never on an event.
//
// Needs the real claude CLI like coder-claude.js, the bubbles come out of
// real turns. The older page needs more than one page of messages, which no
// real exchange delivers in time, so the runner appends synthetic entries to
// the session's own transcript through a cockpit shell (the instance runs on
// the host with the claude CLI's home). SHOTS_DIR, when set, is where the
// runner saves screenshots of the states it reaches.
const SHOTS_DIR = process.env.SHOTS_DIR || "";
const SEED_PAIRS = 30;

async function shot(page, name) {
  if (!SHOTS_DIR) return;
  fs.mkdirSync(SHOTS_DIR, { recursive: true });
  await page.screenshot({ path: path.join(SHOTS_DIR, name), fullPage: false });
}

L.runFeature("COPY VIEW", async ({ page, run, mobilePage }) => {
  const tag = `copy-${Date.now().toString(36)}`;
  const mark = tag.slice(-4).toUpperCase();
  const project = `zztc-${tag}`;
  let coderUrl = null;
  let coderId = null;
  let shellUrl = null;
  let gid = null;

  const mirror = (p, sel = ".attach-selection") => p.evaluate((s) => (document.querySelector(s) || {}).textContent || "", sel);
  const waitReady = async (p) => {
    let text = "";
    for (let i = 0; i < 60; i += 1) {
      text = await mirror(p);
      if (text.includes("❯") || /shortcuts|for help/i.test(text)) return;
      await sleep(1000);
    }
    throw new Error(`claude UI not ready, mirror tail: ${text.slice(-200)}`);
  };
  const hintAtFoot = async (p, id) => {
    const sel = viewOf(id);
    const hint = p.locator(`${sel} [data-copy-hint]`);
    assert(await hint.isVisible(), "the coder's view carries no hint");
    const text = (await hint.innerText()).trim();
    assert(text === "Read and copy here, type in the terminal. Chatting in this view may come later.", `the hint reads ${JSON.stringify(text)}`);
    const g = await p.evaluate((sel) => {
      const v = document.querySelector(sel).getBoundingClientRect();
      const s = document.querySelector(`${sel} [data-copy-scroll]`);
      const h = document.querySelector(`${sel} [data-copy-hint]`).getBoundingClientRect();
      const before = h.top;
      s.scrollTop = 0;
      const moved = document.querySelector(`${sel} [data-copy-hint]`).getBoundingClientRect().top;
      s.scrollTop = s.scrollHeight;
      return { scrollBottom: s.getBoundingClientRect().bottom, top: h.top, bottom: h.bottom, frameBottom: v.bottom, before, moved };
    }, sel);
    assert(g.top >= g.scrollBottom - 0.5, `the hint covers the scroller: ${JSON.stringify(g)}`);
    assert(Math.abs(g.bottom - g.frameBottom) < 1, `the hint does not stand at the frame's foot: ${JSON.stringify(g)}`);
    assert(Math.abs(g.before - g.moved) < 0.5, `the hint scrolls with the messages: ${JSON.stringify(g)}`);
  };
  const typeIntoShell = async (p, command) => {
    await p.click("#terminal");
    await sleep(300);
    await p.keyboard.type(command, { delay: 10 });
    await p.keyboard.press("Enter");
  };
  const prompt = async (text) => {
    const token = await page.getAttribute('meta[name="csrf-token"]', "content");
    const r = await page.context().request.post(`${L.BASE}/coders/${coderId}/input`, { headers: { "X-CSRF-Token": token, "Content-Type": "application/json" }, data: JSON.stringify({ items: [{ prompt: text }] }) });
    return r.status();
  };
  const waitRecord = async (pattern, timeout) => {
    const until = Date.now() + timeout;
    let html = "";
    while (Date.now() < until) {
      html = await (await page.context().request.get(`${L.BASE}/coders/${coderId}/conversation`)).text();
      if (html.split('data-role="coder"').slice(1).some((bubble) => pattern.test(bubble.split("data-role=")[0]))) return;
      await sleep(1500);
    }
    throw new Error(`the record never showed ${pattern}: ${html.slice(-300)}`);
  };
  const viewOf = (id) => `terminal-copy[terminal-id="${id}"]`;
  const scrollState = (p, id) => p.evaluate((sel) => {
    const s = document.querySelector(`${sel} [data-copy-scroll]`);
    return { top: s.scrollTop, max: s.scrollHeight - s.clientHeight, font: getComputedStyle(s).fontSize };
  }, viewOf(id));

  try {
    await L.createProject(page, project);

    await run("setup: a claude coder attaches and is ready", async () => {
      await page.goto(`${L.BASE}/coders/new?project=${encodeURIComponent(project)}`, { waitUntil: "domcontentloaded" });
      const f = page.locator('form:has(select[name="agent"])').first();
      await f.locator('input[name="name"]').fill(`tccopy-${mark}`);
      await f.locator('select[name="project"]').selectOption(project).catch(() => {});
      await f.locator('select[name="coder"]').selectOption("claude").catch(() => {});
      await Promise.all([page.waitForURL(/\/coders\/(?!new)[^/]+$/, { timeout: 20000 }), f.locator('button[type="submit"]').first().click()]);
      coderUrl = page.url();
      coderId = new URL(coderUrl).pathname.split("/").pop();
      const missing = await L.waitUpgraded(page, ["terminal-attach", "terminal-input", "terminal-copy"], 12000);
      assert(missing.length === 0, `not upgraded: ${missing}`);
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
      await waitReady(page);
    });

    await run("setup: a first exchange with a tool call, older entries, a second exchange", async () => {
      assert(await prompt(`Run the shell command echo $((6*7))XY${mark} and then reply with exactly the words FIRST${mark} OK and nothing else.`) === 200, "the first prompt was refused");
      await waitRecord(new RegExp(`FIRST${mark} OK`), 150000);
      shellUrl = await L.createShell(page, project);
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
      await sleep(1000);
      await typeIntoShell(page, `f=$(ls ~/.claude/projects/*/${coderId}.jsonl | head -1); for i in $(seq 1 ${SEED_PAIRS}); do printf '{"type":"user","uuid":"seed-u-%s","isSidechain":false,"message":{"role":"user","content":"seed question %s"}}\\n{"type":"assistant","uuid":"seed-a-%s","isSidechain":false,"message":{"role":"assistant","content":[{"type":"text","text":"seed answer %s"}]}}\\n' $i $i $i $i >> "$f"; done && printf '%s\\n' '{"type":"assistant","uuid":"seed-code","isSidechain":false,"message":{"role":"assistant","content":[{"type":"text","text":"seed code \`inline\`\\n\\n\`\`\`\\nblock\\n\`\`\`"}]}}' >> "$f" && echo SEED''DONE`);
      let text = "";
      for (let i = 0; i < 20 && !text.includes("SEEDDONE"); i += 1) {
        await sleep(500);
        text = await mirror(page);
      }
      assert(text.includes("SEEDDONE"), `the seeding did not echo: ${text.slice(-200)}`);
      await L.deleteShell(page, shellUrl);
      shellUrl = null;
      await page.goto(coderUrl, { waitUntil: "domcontentloaded" });
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
      assert(await prompt(`Reply with exactly the words SECOND${mark} OK and nothing else.`) === 200, "the second prompt was refused");
      await waitRecord(new RegExp(`SECOND${mark} OK`), 150000);
    });

    await run("open: the copy button shows the newest messages in the terminal's place, the terminal keeps its box and stream", async () => {
      const streams = [];
      const onRequest = (r) => { if (r.url().includes("/stream")) streams.push(r.url()); };
      page.on("request", onRequest);
      const before = await page.locator("#terminal").boundingBox();
      await page.click(".attach-desktop [data-terminal-copy]");
      await page.waitForSelector(`${viewOf(coderId)} [data-copy-log] [data-message-id]`, { timeout: 10000 });
      await sleep(400);
      const after = await page.locator("#terminal").boundingBox();
      const surface = await page.locator(viewOf(coderId)).boundingBox();
      page.off("request", onRequest);
      assert(streams.length === 0, `the stream reconnected: ${streams}`);
      assert(Math.abs(before.width - after.width) < 1 && Math.abs(before.height - after.height) < 1, `the terminal box moved: ${JSON.stringify(before)} -> ${JSON.stringify(after)}`);
      assert(Math.abs(surface.x - after.x) < 1 && Math.abs(surface.width - after.width) < 1 && Math.abs(surface.height - after.height) < 1, `the view does not stand in the terminal's place: ${JSON.stringify(surface)} vs ${JSON.stringify(after)}`);
      assert((await page.evaluate(() => getComputedStyle(document.getElementById("terminal")).visibility)) === "hidden", "the terminal still shows");
      assert((await page.getAttribute(".attach-desktop [data-terminal-copy]", "aria-pressed")) === "true", "the copy button does not say the view is open");
      const count = await page.locator(`${viewOf(coderId)} [data-message-id]`).count();
      assert(count === 50, `the newest page holds ${count} messages, want 50`);
      const last = await page.locator(`${viewOf(coderId)} [data-message-id]`).last().innerText();
      assert(last.includes(`SECOND${mark} OK`), `the view does not end on the newest answer: ${last}`);
      const s = await scrollState(page, coderId);
      assert(s.max > 0 && s.top >= s.max - 2, `the view does not open at its end: ${JSON.stringify(s)}`);
      await hintAtFoot(page, coderId);
      await shot(page, "desktop-coder-copy.png");
    });

    await run("earlier: older pages load above on demand, the reading position holds, and the record stays clean", async () => {
      const sel = viewOf(coderId);
      for (let round = 0; round < 3; round += 1) {
        const more = page.locator(`${sel} [data-copy-more]`);
        if (!(await more.count())) break;
        await page.evaluate((sel) => { document.querySelector(`${sel} [data-copy-scroll]`).scrollTop = 0; }, sel);
        const anchorId = await page.locator(`${sel} [data-message-id]`).first().getAttribute("data-message-id");
        const topBefore = await page.evaluate(({ sel, id }) => document.querySelector(`${sel} [data-message-id="${id}"]`).getBoundingClientRect().top, { sel, id: anchorId });
        const countBefore = await page.locator(`${sel} [data-message-id]`).count();
        await more.click();
        await page.waitForFunction(({ sel, n }) => document.querySelectorAll(`${sel} [data-message-id]`).length > n, { sel, n: countBefore }, { timeout: 10000 });
        await sleep(200);
        const topAfter = await page.evaluate(({ sel, id }) => document.querySelector(`${sel} [data-message-id="${id}"]`).getBoundingClientRect().top, { sel, id: anchorId });
        assert(Math.abs(topAfter - topBefore) < 2, `the reading position jumped by ${topAfter - topBefore}px`);
      }
      assert(await page.locator(`${sel} [data-copy-more]`).count() === 0, "the whole conversation never loaded");
      const text = await page.locator(`${sel} [data-copy-log]`).innerText();
      assert(text.includes(`FIRST${mark} OK`) && text.includes("seed answer 1"), "the older messages are missing");
      const tool = await page.locator(`${sel} [data-copy-tool="Bash"]`).allInnerTexts();
      assert(tool.some((t) => t.includes(`XY${mark}`)), `the tool call is no compact line: ${tool}`);
      assert(!text.includes(`42XY${mark}`), "a tool result reached the view");
      await page.locator(`${sel} [data-copy-tool="Bash"]`).first().scrollIntoViewIfNeeded();
      await shot(page, "desktop-coder-copy-earlier.png");
      const html = await page.locator(`${sel} [data-copy-log]`).innerHTML();
      for (const bad of ["system-reminder", "command-name", "tool_result", "Caveat", "local-command", "terminal-direction-pad", "<textarea", "<form"]) {
        assert(!html.includes(bad), `${bad} stands in the view`);
      }
      const firstUser = await page.locator(`${sel} [data-role="user"]`).filter({ hasText: `FIRST${mark}` }).first().locator("[data-assistant-text]").innerText();
      assert(firstUser.trim() === `Run the shell command echo $((6*7))XY${mark} and then reply with exactly the words FIRST${mark} OK and nothing else.`, `the user bubble carries more than the words: ${firstUser}`);
    });

    await run("copy: text is selectable, one message copies alone, Copy all takes every shown message", async () => {
      const sel = viewOf(coderId);
      const selected = await page.evaluate((sel) => {
        const node = [...document.querySelectorAll(`${sel} [data-role="coder"] [data-assistant-text]`)].pop();
        const range = document.createRange();
        range.selectNodeContents(node);
        const selection = window.getSelection();
        selection.removeAllRanges();
        selection.addRange(range);
        const picked = selection.toString();
        selection.removeAllRanges();
        return { picked, select: getComputedStyle(node).userSelect };
      }, sel);
      assert(selected.select !== "none" && selected.picked.includes(`SECOND${mark}`), `the text is not selectable: ${JSON.stringify(selected)}`);
      await page.locator(`${sel} [data-role="coder"]`).last().locator("[data-copy-message]").click();
      await sleep(300);
      assert(await page.locator(`${sel} [data-copy-message][data-copied] .ti-check`).count() === 1, "the copied message's button does not say so");
      const one = await page.evaluate(() => navigator.clipboard.readText());
      assert(one === `SECOND${mark} OK`, `one message copied as ${JSON.stringify(one)}`);
      await page.click(`${sel} [data-copy-all]`);
      await sleep(300);
      const all = await page.evaluate(() => navigator.clipboard.readText());
      assert(all.startsWith("user:\n") && all.includes(`coder:\nFIRST${mark} OK`) && all.endsWith(`coder:\nSECOND${mark} OK`), `Copy all: ${all.slice(0, 120)} … ${all.slice(-120)}`);
      assert(!all.includes(`42XY${mark}`), "Copy all carries a tool result");
      return `${all.split("\n\n").length} blocks`;
    });

    await run("font: every text of a bubble stands in the terminal's own font and size for every setting", async () => {
      const setFont = async (size) => {
        await page.click(".attach-settings > button");
        await page.selectOption('terminal-setting-select[setting="font-size"] select', String(size));
        await page.keyboard.press("Escape");
        await sleep(300);
      };
      // The terminal's size is what xterm draws with, read off the text layer
      // that follows its cell grid. Every part of a bubble that the terminal
      // shows as well has to stand at exactly that size: the words, the tool
      // line, inline code and a code block.
      const sizes = () => page.evaluate(({ sel, id }) => {
        const size = (el) => (el ? getComputedStyle(el).fontSize : "missing");
        const last = (q) => [...document.querySelectorAll(`${sel} ${q}`)].pop();
        return {
          terminal: size(document.querySelector(`terminal-attach[terminal-id="${id}"] .attach-selection`)),
          answer: size(last('[data-role="coder"] [data-assistant-text] p')),
          user: size(last('[data-role="user"] [data-assistant-text]')),
          tool: size(last("[data-copy-tool]")),
          code: size(last('[data-role="coder"] .markdown p code')),
          block: size(last('[data-role="coder"] .markdown pre code')),
        };
      }, { sel: viewOf(coderId), id: coderId });
      // The bubbles read in the terminal's own font, the one it keeps on its
      // text layer, never in the page's.
      const families = await page.evaluate(({ sel, id }) => {
        const family = (el) => (el ? getComputedStyle(el).fontFamily : "missing");
        const last = (q) => [...document.querySelectorAll(`${sel} ${q}`)].pop();
        return {
          terminal: family(document.querySelector(`terminal-attach[terminal-id="${id}"] .attach-selection`)),
          answer: family(last('[data-role="coder"] [data-assistant-text] p')),
          user: family(last('[data-role="user"] [data-assistant-text]')),
        };
      }, { sel: viewOf(coderId), id: coderId });
      assert(families.terminal !== "missing" && families.answer === families.terminal && families.user === families.terminal, `the bubbles are not in the terminal's font: ${JSON.stringify(families)}`);
      assert((await scrollState(page, coderId)).font === "14px", "the view does not start at the terminal's 14px");
      const seen = [];
      for (const size of [10, 14, 20]) {
        await setFont(size);
        const got = await sizes();
        seen.push(`${size}: ${JSON.stringify(got)}`);
        assert(got.terminal === `${size}px`, `the terminal is not at ${size}px: ${got.terminal}`);
        for (const [part, value] of Object.entries(got)) assert(value === got.terminal, `${part} stands at ${value} beside the terminal's ${got.terminal}`);
        if (size === 20) await shot(page, "desktop-coder-copy-font-20.png");
      }
      await setFont(14);
      assert((await scrollState(page, coderId)).font === "14px", "the view did not follow back to 14px");
      return `${families.terminal}; ${seen.join("; ")}`;
    });

    await run("snapshot: nothing arrives while the view stands, reopening shows the newest state", async () => {
      const sel = viewOf(coderId);
      const asks = [];
      const onRequest = (r) => { if (/\/conversation(\?|$)/.test(r.url())) asks.push(r.url()); };
      page.on("request", onRequest);
      assert(await prompt(`Reply with exactly the words THIRD${mark} OK and nothing else.`) === 200, "the third prompt was refused");
      await waitRecord(new RegExp(`THIRD${mark} OK`), 150000);
      await sleep(2000);
      page.off("request", onRequest);
      assert(asks.length === 0, `the standing view asked again: ${asks}`);
      assert(!(await page.locator(`${sel} [data-copy-log]`).innerText()).includes(`THIRD${mark}`), "the standing view took the new answer");
      await page.click(`${sel} [data-copy-close]`);
      await page.click(".attach-desktop [data-terminal-copy]");
      await page.waitForFunction((sel) => (document.querySelector(`${sel} [data-copy-log]`) || {}).innerText?.includes("THIRD"), sel, { timeout: 10000 });
    });

    await run("screen: the Screen button turns the same frame to the coder's screen as text, Conversation turns it back", async () => {
      const sel = viewOf(coderId);
      await page.click(`${sel} [data-copy-face="text"]`);
      await page.waitForFunction((sel) => (document.querySelector(`${sel} [data-copy-text]`) || {}).textContent?.trim(), sel, { timeout: 8000 });
      const seen = await page.evaluate((sel) => {
        const v = document.querySelector(sel);
        const pre = v.querySelector("[data-copy-text]");
        const layer = document.querySelector(`terminal-attach[terminal-id="${v.getAttribute("terminal-id")}"] .attach-selection`);
        return {
          title: v.querySelector("[data-copy-title]").textContent,
          logShown: getComputedStyle(v.querySelector("[data-copy-log]")).display !== "none",
          preShown: getComputedStyle(pre).display !== "none",
          lines: !!v.querySelector("[data-copy-lines]"),
          font: getComputedStyle(pre).fontFamily,
          termFont: layer ? getComputedStyle(layer).fontFamily : "",
          modal: !!document.getElementById("terminal-copy-modal"),
        };
      }, sel);
      assert(seen.title === "Screen" && !seen.logShown && seen.preShown, `the frame did not turn to the screen: ${JSON.stringify(seen)}`);
      assert(!seen.lines, "a coder's screen offers an amount");
      assert(seen.termFont && seen.font === seen.termFont, `the screen text is not in the terminal's font: ${seen.font} vs ${seen.termFont}`);
      assert(!seen.modal, "the copy modal is still on the page");
      assert((await page.locator(`${sel} [data-copy-face="text"]`).isHidden()) && (await page.locator(`${sel} [data-copy-face="conversation"]`).isVisible()), "the face buttons did not swap");
      const raw = await (await page.context().request.get(`${L.BASE}/coders/${coderId}/copy?messages=5`)).json();
      assert(raw.screen === true && raw.kind === "coder" && raw.text && !/^(user|coder):$/m.test(raw.text), `the copy route answered a coder with its record: ${JSON.stringify(raw).slice(0, 300)}`);
      assert(!("lines" in raw) && !("dropped" in raw), `the copy route still answers record fields: ${Object.keys(raw)}`);
      await shot(page, "desktop-coder-screen.png");
      await page.click(`${sel} [data-copy-face="conversation"]`);
      await page.waitForSelector(`${sel} [data-copy-log] [data-message-id]`, { timeout: 10000 });
      assert((await page.locator(`${sel} [data-copy-title]`).textContent()) === "Conversation", "the title did not come back");
    });

    await run("close: the live terminal comes back without a reconnect, the snapshot is gone", async () => {
      const streams = [];
      const onRequest = (r) => { if (r.url().includes("/stream")) streams.push(r.url()); };
      page.on("request", onRequest);
      await page.click(`${viewOf(coderId)} [data-copy-close]`);
      await page.waitForSelector(viewOf(coderId), { state: "hidden", timeout: 5000 });
      await sleep(500);
      page.off("request", onRequest);
      assert(streams.length === 0, `closing reconnected the stream: ${streams}`);
      assert((await page.evaluate(() => getComputedStyle(document.getElementById("terminal")).visibility)) === "visible", "the terminal did not come back");
      assert(await page.locator(`${viewOf(coderId)} [data-message-id]`).count() === 0, "the snapshot stayed behind");
      await page.click(".attach-desktop [data-terminal-copy]");
      await page.waitForSelector(`${viewOf(coderId)} [data-message-id]`, { timeout: 10000 });
      await page.keyboard.press("Escape");
      await page.waitForSelector(viewOf(coderId), { state: "hidden", timeout: 5000 });
    });

    await run("shell: the copy button shows the history as plain text in the terminal's place, lines are a choice", async () => {
      shellUrl = await L.createShell(page, project);
      const shellId = new URL(shellUrl).pathname.split("/").pop();
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
      await sleep(800);
      await typeIntoShell(page, `for i in $(seq 1 80); do echo HIST${mark}-$i; done; echo 'see https://example.com/x).'`);
      let text = "";
      for (let i = 0; i < 20 && !text.includes(`HIST${mark}-80`); i += 1) {
        await sleep(300);
        text = await mirror(page);
      }
      const sel = viewOf(shellId);
      const streams = [];
      const onRequest = (r) => { if (r.url().includes("/stream")) streams.push(r.url()); };
      page.on("request", onRequest);
      const before = await page.locator("#terminal").boundingBox();
      await page.click(".attach-desktop [data-terminal-copy]");
      await page.waitForFunction((sel) => (document.querySelector(`${sel} [data-copy-text]`) || {}).textContent?.includes("-80"), sel, { timeout: 8000 });
      await sleep(300);
      page.off("request", onRequest);
      const surface = await page.locator(sel).boundingBox();
      assert(streams.length === 0, `the stream reconnected: ${streams}`);
      assert(Math.abs(surface.x - before.x) < 1 && Math.abs(surface.width - before.width) < 1 && Math.abs(surface.height - before.height) < 1, `the view does not stand in the terminal's place: ${JSON.stringify(surface)} vs ${JSON.stringify(before)}`);
      const seen = await page.evaluate((sel) => {
        const v = document.querySelector(sel);
        const pre = v.querySelector("[data-copy-text]");
        const s = v.querySelector("[data-copy-scroll]");
        const layer = document.querySelector("#terminal .attach-selection");
        const range = document.createRange();
        range.selectNodeContents(pre);
        const selection = window.getSelection();
        selection.removeAllRanges();
        selection.addRange(range);
        const picked = selection.toString();
        selection.removeAllRanges();
        return {
          title: v.querySelector("[data-copy-title]").textContent,
          wrap: getComputedStyle(pre).whiteSpace,
          select: getComputedStyle(pre).webkitUserSelect || getComputedStyle(pre).userSelect,
          chars: pre.textContent.length,
          picked: picked.length,
          font: getComputedStyle(pre).fontFamily,
          termFont: layer ? getComputedStyle(layer).fontFamily : "",
          size: getComputedStyle(pre).fontSize,
          atEnd: s.scrollTop >= s.scrollHeight - s.clientHeight - 2,
          lines: v.querySelector("[data-copy-lines]").value,
          faces: v.querySelectorAll("[data-copy-face]").length,
          link: v.querySelector("[data-copy-text] a")?.getAttribute("href") || "",
          behind: getComputedStyle(document.getElementById("terminal")).visibility,
          inset: getComputedStyle(s).paddingLeft,
          selectH: v.querySelector("[data-copy-lines]").getBoundingClientRect().height,
          buttonH: v.querySelector("[data-copy-all]").getBoundingClientRect().height,
          selectFont: getComputedStyle(v.querySelector("[data-copy-lines]")).fontSize,
          buttonFont: getComputedStyle(v.querySelector("[data-copy-all]")).fontSize,
        };
      }, sel);
      assert(seen.inset === "3px", `the text face is inset ${seen.inset}, not the terminal's 3px`);
      assert(Math.abs(seen.selectH - seen.buttonH) < 0.5 && seen.selectFont === seen.buttonFont, `the lines select does not match the head's buttons: ${JSON.stringify(seen)}`);
      assert(await page.locator(`${sel} [data-copy-hint]`).count() === 0, "a shell's view carries the coder's hint");
      assert(seen.title === "History" && seen.faces === 0, `a shell's view is no history: ${JSON.stringify(seen)}`);
      assert(seen.wrap === "pre-wrap" && seen.select === "text" && seen.chars > 0 && seen.picked === seen.chars, `the text is not plain selectable text: ${JSON.stringify(seen)}`);
      assert(seen.termFont && seen.font === seen.termFont, `the text is not in the terminal's font: ${seen.font} vs ${seen.termFont}`);
      assert(seen.size === "14px", `the text is ${seen.size}, not the terminal's 14px`);
      assert(seen.atEnd, "the history does not open at its end");
      assert(seen.lines === "500", `the lines start at ${seen.lines}`);
      assert(seen.link === "https://example.com/x", `the address is no link: ${seen.link}`);
      assert(seen.behind === "hidden", "the terminal still shows");
      await shot(page, "desktop-shell-copy.png");
      await page.selectOption(`${sel} [data-copy-lines]`, "2000");
      await page.waitForFunction((sel) => !document.querySelector(`${sel} [data-copy-lines]`).disabled, sel, { timeout: 8000 });
      assert((await page.evaluate(() => localStorage.getItem("dc-copy-lines"))) === "2000", "the lines choice was not kept");
      await page.click(`${sel} [data-copy-all]`);
      await sleep(300);
      const all = await page.evaluate(() => navigator.clipboard.readText());
      assert(all.includes(`HIST${mark}-1\n`) && all.includes(`HIST${mark}-80`), `Copy all: ${all.slice(-160)}`);
      await page.click(".attach-settings > button");
      await page.selectOption('terminal-setting-select[setting="font-size"] select', "18");
      await page.keyboard.press("Escape");
      await sleep(300);
      assert(await page.locator(sel).isVisible(), "the settings Escape closed the view");
      assert((await page.evaluate((sel) => getComputedStyle(document.querySelector(`${sel} [data-copy-text]`)).fontSize, sel)) === "18px", "the history did not follow the font size");
      await page.click(".attach-settings > button");
      await page.selectOption('terminal-setting-select[setting="font-size"] select', "14");
      await page.keyboard.press("Escape");
      await sleep(300);
      await page.locator(`${sel} [data-copy-scroll]`).focus();
      await page.keyboard.press("Escape");
      await page.waitForSelector(sel, { state: "hidden", timeout: 5000 });
      assert((await page.evaluate(() => getComputedStyle(document.getElementById("terminal")).visibility)) === "visible", "the shell terminal did not come back");
      assert((await page.locator(`${sel} [data-copy-text]`).textContent()) === "", "the snapshot stayed behind");
      await sleep(200);
      await page.keyboard.type(`echo BACK'${mark}'`, { delay: 20 });
      await page.keyboard.press("Enter");
      let back = "";
      for (let i = 0; i < 10 && !back.includes(`BACK${mark}`); i += 1) {
        await sleep(500);
        back = await mirror(page);
      }
      assert(back.includes(`BACK${mark}`), `the terminal did not get the keyboard back after Escape: ${back.slice(-120)}`);
      await page.click(".attach-desktop [data-terminal-copy]");
      await page.waitForFunction((sel) => (document.querySelector(`${sel} [data-copy-text]`) || {}).textContent?.includes("-80"), sel, { timeout: 8000 });
      assert((await page.locator(`${sel} [data-copy-lines]`).inputValue()) === "2000", "the stored lines did not come back");
      await page.click(".attach-desktop [data-terminal-copy]");
      await page.waitForSelector(sel, { state: "hidden", timeout: 5000 });
      await page.route("**/shells/*/copy*", (route) => route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: `READFAIL${mark}` }) }));
      try {
        await page.click(".attach-desktop [data-terminal-copy]");
        const toast = page.locator(".dc-toast", { hasText: `READFAIL${mark}` });
        await toast.waitFor({ timeout: 5000 });
        assert(!(await toast.innerText()).includes("[object Object]"), "the read error toast shows an object");
        await page.click(`${sel} [data-copy-close]`);
        await page.waitForSelector(sel, { state: "hidden", timeout: 5000 });
      } finally {
        await page.unroute("**/shells/*/copy*");
      }
    });

    await run("split: the coder pane shows its conversation, the shell pane its history, each in its own pane", async () => {
      const shellId = new URL(shellUrl).pathname.split("/").pop();
      const group = await page.evaluate(async (list) => {
        const token = document.querySelector('meta[name="csrf-token"]').content;
        const r = await fetch("/terminal-tabs/group", { method: "POST", headers: { "X-CSRF-Token": token, "Content-Type": "application/json" }, body: JSON.stringify({ ids: list }) });
        return r.json();
      }, [coderId, shellId]);
      gid = group.id;
      assert(gid, `no group id: ${JSON.stringify(group)}`);
      await page.goto(`${L.BASE}/splits/${gid}?focus=${coderId}`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(`terminal-attach[terminal-id="${shellId}"] .xterm-screen canvas`, { timeout: 15000 });
      const paneSel = (id) => `.attach-split-pane[data-pane-id="${id}"]`;
      const icons = await page.evaluate((ids) => ids.map((id) => [...document.querySelectorAll(`[data-terminal-footer="${id}"] [data-terminal-copy] .ti`)].map((i) => i.className)), [coderId, shellId]);
      assert(icons[0].length === 2 && icons[0].every((c) => c.includes("ti-messages") && !c.includes("ti-copy")), `the coder's copy buttons do not wear the chat icon: ${JSON.stringify(icons[0])}`);
      assert(icons[1].length === 2 && icons[1].every((c) => c.includes("ti-copy") && !c.includes("ti-messages")), `the shell's copy buttons do not wear the copy icon: ${JSON.stringify(icons[1])}`);
      const before = await page.locator(`terminal-attach[terminal-id="${coderId}"]`).boundingBox();
      await page.click(`[data-terminal-footer="${coderId}"] .attach-desktop [data-terminal-copy]`);
      await page.waitForSelector(`${viewOf(coderId)} [data-message-id]`, { timeout: 10000 });
      const surface = await page.locator(viewOf(coderId)).boundingBox();
      assert(Math.abs(surface.width - before.width) < 1 && Math.abs(surface.height - before.height) <= 1.5 && Math.abs(surface.y - before.y) <= 1.5, `the view does not stand in the pane's terminal: ${JSON.stringify(surface)} vs ${JSON.stringify(before)}`);
      assert((await page.evaluate((id) => getComputedStyle(document.querySelector(`terminal-attach[terminal-id="${id}"]`)).visibility, shellId)) === "visible", "the shell pane hid its terminal");
      await page.click(`terminal-attach[terminal-id="${shellId}"]`);
      await sleep(300);
      await page.keyboard.type("echo SPLIT'OK'", { delay: 20 });
      await page.keyboard.press("Enter");
      let echoed = "";
      for (let i = 0; i < 10 && !echoed.includes("SPLITOK"); i += 1) {
        await sleep(500);
        echoed = await page.locator(`${paneSel(shellId)} .attach-selection`).textContent();
      }
      assert(echoed.includes("SPLITOK"), `the shell pane did not take the typing: ${echoed.slice(-120)}`);
      assert(await page.locator(viewOf(coderId)).isVisible(), "typing in the shell closed the coder's view");
      await page.click(`[data-terminal-footer="${shellId}"] .attach-desktop [data-terminal-copy]`);
      await page.waitForFunction((sel) => (document.querySelector(`${sel} [data-copy-text]`) || {}).textContent?.includes("SPLITOK"), viewOf(shellId), { timeout: 8000 });
      const shellBox = await page.locator(`terminal-attach[terminal-id="${shellId}"]`).boundingBox();
      const shellView = await page.locator(viewOf(shellId)).boundingBox();
      assert(Math.abs(shellView.x - shellBox.x) < 1 && Math.abs(shellView.width - shellBox.width) < 1, `the shell view does not stand in its pane: ${JSON.stringify(shellView)} vs ${JSON.stringify(shellBox)}`);
      assert(await page.locator(viewOf(coderId)).isVisible(), "the shell's view closed the coder's");
      const heads = await page.evaluate((sels) => sels.map((sel) => document.querySelector(`${sel} .dc-copy-head`).getBoundingClientRect().height), [viewOf(coderId), viewOf(shellId)]);
      assert(Math.abs(heads[0] - heads[1]) < 0.5, `the coder and shell heads differ: ${heads}`);
      const bubblePad = await page.evaluate((sel) => getComputedStyle(document.querySelector(`${sel} [data-copy-scroll]`)).paddingLeft, viewOf(coderId));
      assert(bubblePad === "16px", `the bubbles lost their padding: ${bubblePad}`);
      await shot(page, "desktop-split-copy.png");
      await page.click(`${viewOf(shellId)} [data-copy-close]`);
      await page.waitForSelector(viewOf(shellId), { state: "hidden", timeout: 5000 });
      await page.click(`${viewOf(coderId)} [data-copy-close]`);
      await page.waitForSelector(viewOf(coderId), { state: "hidden", timeout: 5000 });
      assert((await page.evaluate((id) => getComputedStyle(document.querySelector(`terminal-attach[terminal-id="${id}"]`)).visibility, coderId)) === "visible", "the coder pane did not bring its terminal back");
    });

    await run("phone: the footer's copy button shows the conversation at 390x844 and closes back", async () => {
      const mp = await mobilePage();
      await mp.goto(`${L.BASE}/splits/${gid}?focus=${coderId}`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(mp);
      await mp.waitForSelector(`terminal-attach[terminal-id="${coderId}"] .xterm-screen canvas`, { timeout: 15000 });
      await mp.click(`[data-terminal-footer="${coderId}"] .attach-mobile [data-terminal-copy]`);
      await mp.waitForSelector(`${viewOf(coderId)} [data-message-id]`, { timeout: 10000 });
      await sleep(400);
      const box = await mp.locator(viewOf(coderId)).boundingBox();
      assert(box && box.width <= 390 && box.width > 300, `the view does not fit the phone: ${JSON.stringify(box)}`);
      const overflow = await mp.evaluate((sel) => { const s = document.querySelector(`${sel} [data-copy-scroll]`); return s.scrollWidth - s.clientWidth; }, viewOf(coderId));
      assert(overflow <= 0, `the view scrolls sideways by ${overflow}px`);
      const s = await scrollState(mp, coderId);
      assert(s.top >= s.max - 2, `the phone view does not open at its end: ${JSON.stringify(s)}`);
      await hintAtFoot(mp, coderId);
      await shot(mp, "phone-split-copy.png");
      await mp.click(`${viewOf(coderId)} [data-copy-face="text"]`);
      await mp.waitForFunction((sel) => (document.querySelector(`${sel} [data-copy-text]`) || {}).textContent?.trim(), viewOf(coderId), { timeout: 8000 });
      const head = await mp.evaluate((sel) => { const h = document.querySelector(`${sel} .dc-copy-head`); return h.scrollWidth - h.clientWidth; }, viewOf(coderId));
      assert(head <= 0, `the head overflows by ${head}px`);
      await shot(mp, "phone-split-coder-screen.png");
      await mp.click(`${viewOf(coderId)} [data-copy-close]`);
      await mp.waitForSelector(viewOf(coderId), { state: "hidden", timeout: 5000 });
      assert((await mp.evaluate((id) => getComputedStyle(document.querySelector(`terminal-attach[terminal-id="${id}"]`)).visibility, coderId)) === "visible", "the phone did not bring the terminal back");
    });

    await run("phone: a shell's copy button shows its history at 390x844, on its page and in the split", async () => {
      const mp = await mobilePage();
      const shellId = new URL(shellUrl).pathname.split("/").pop();
      const check = async (name) => {
        await mp.click(`${name ? `[data-terminal-footer="${shellId}"] ` : ""}.attach-mobile [data-terminal-copy]`);
        await mp.waitForFunction((sel) => (document.querySelector(`${sel} [data-copy-text]`) || {}).textContent?.includes("-80"), viewOf(shellId), { timeout: 8000 });
        await sleep(400);
        const seen = await mp.evaluate((sel) => {
          const v = document.querySelector(sel);
          const s = v.querySelector("[data-copy-scroll]");
          const h = v.querySelector(".dc-copy-head");
          const lines = v.querySelector("[data-copy-lines]");
          const all = v.querySelector("[data-copy-all]");
          return {
            w: v.getBoundingClientRect().width, side: s.scrollWidth - s.clientWidth, head: h.scrollWidth - h.clientWidth, atEnd: s.scrollTop >= s.scrollHeight - s.clientHeight - 2,
            select: lines.getBoundingClientRect().height, button: all.getBoundingClientRect().height,
            selectFont: getComputedStyle(lines).fontSize, buttonFont: getComputedStyle(all).fontSize,
          };
        }, viewOf(shellId));
        assert(seen.w <= 390 && seen.w > 300 && seen.side <= 0 && seen.head <= 0 && seen.atEnd, `the phone view: ${JSON.stringify(seen)}`);
        assert(await mp.locator(`${viewOf(shellId)} [data-copy-hint]`).count() === 0, "a shell's phone view carries the coder's hint");
        assert(Math.abs(seen.select - seen.button) < 0.5, `the phone's lines select is not as tall as the head's buttons: ${JSON.stringify(seen)}`);
        await shot(mp, name ? "phone-split-shell-copy.png" : "phone-shell-copy.png");
        await mp.click(`${viewOf(shellId)} [data-copy-close]`);
        await mp.waitForSelector(viewOf(shellId), { state: "hidden", timeout: 5000 });
      };
      await mp.goto(`${L.BASE}/splits/${gid}?focus=${shellId}`, { waitUntil: "domcontentloaded" });
      await mp.waitForSelector(`terminal-attach[terminal-id="${shellId}"] .xterm-screen canvas`, { timeout: 15000 });
      await check(true);
      await mp.evaluate(async (ids) => {
        const token = document.querySelector('meta[name="csrf-token"]').content;
        await fetch("/terminal-tabs/ungroup", { method: "POST", headers: { "X-CSRF-Token": token, "Content-Type": "application/json" }, body: JSON.stringify({ ids }) });
      }, [coderId]);
      gid = null;
      await sleep(600);
      await mp.goto(shellUrl, { waitUntil: "domcontentloaded" });
      await mp.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
      await check(false);
    });
  } finally {
    try {
      if (gid) {
        await page.evaluate(async (ids) => {
          const token = document.querySelector('meta[name="csrf-token"]').content;
          await fetch("/terminal-tabs/ungroup", { method: "POST", headers: { "X-CSRF-Token": token, "Content-Type": "application/json" }, body: JSON.stringify({ ids }) });
        }, [coderId]);
        await sleep(600);
      }
      if (shellUrl) await L.deleteShell(page, shellUrl);
      if (coderUrl) await L.stopSession(page, coderUrl);
      await L.deleteProject(page, project);
    } catch (e) { console.log("cleanup note:", e.message); }
  }
});
