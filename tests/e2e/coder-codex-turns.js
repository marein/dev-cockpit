#!/usr/bin/env node
// Coder coverage: what the cockpit knows about a codex coder's turns, against
// the fake codex of tests/e2e/fakes, which records a turn in its rollout the
// way codex 0.172.0 does and rings the bell codex is told to ring. Chromium
// only.
//
// codex reports nothing through hooks. Its rollout marks a turn's start, its
// end and its abort, and a tool call from the call to its output, so the
// working mark follows the record (coder.ActivityProfile.WatchRecord). The
// news comes from the bell: `-c tui.notifications=true`,
// `-c tui.notification_method="bel"` and
// `-c tui.notification_condition="always"` on the command line make codex
// ring for a turn that ended and for a question, and the rollout tells the
// two apart, a question stands behind a tool call without its output
// (coder.BellReader). The title codex sets while it works ends in the same
// character and is no bell.
//
// The same record answers /coders/:id/activity, which `coder-activity` and
// the job checks read, and the conversation of the copy view. Nothing of it
// is written into codex's home.
//
// A steered codex coder gets its check on both bells: the job's criterion
// names what the fake claude that runs the check answers (WAKE_DONE,
// WAKE_BLOCKED, see wake.js), so what is pinned here is that the bell buys
// the check and the job closes, not what a check thinks.
//
// Needs an instance with tests/e2e/fakes first on PATH and a scratch HOME and
// CODEX_HOME, see the README.
const { chromium } = require("playwright-core");
const L = require("./lib");
const { assert, sleep } = L;

const BASE = process.env.BASE_URL || "https://localhost:3010";

(async () => {
  const browser = await chromium.launch({ args: ["--no-sandbox"] });
  const bag = { consoleErrors: [], pageErrors: [] };
  const { results, run } = L.makeRunner();
  const tag = `ct-${Date.now().toString(36)}`;
  const project = `zzct-${tag}`;
  const ctx = await L.newDesktop(browser, "chromium");
  const page = await ctx.newPage();
  L.wirePage(page, bag);
  const board = await ctx.newPage();
  L.wirePage(board, bag);
  const phoneCtx = await L.newMobile(browser);
  const phone = await phoneCtx.newPage();
  L.wirePage(phone, bag);

  const mirrorOf = (p) => p.evaluate(() => (document.querySelector(".attach-selection") || {}).textContent || "");
  const flat = (text) => text.replace(/\s+/g, "");
  const idOf = (url) => new URL(url).pathname.split("/").pop();
  const waitFor = async (probe, what, tries = 60) => {
    for (let i = 0; i < tries; i++) {
      if (await probe()) return;
      await sleep(250);
    }
    throw new Error(`timed out waiting for ${what}`);
  };
  const shows = (p, needle, what) => waitFor(async () => flat(await mirrorOf(p)).includes(flat(needle)), what || `the pane showing ${needle}`, 80);
  const count = async (p, needle) => flat(await mirrorOf(p)).split(flat(needle)).length - 1;
  const post = (p, path, values = {}) => p.evaluate(async ([path, values]) => {
    const body = new URLSearchParams(values);
    body.set("csrf_token", document.querySelector('meta[name="csrf-token"]').content);
    const res = await fetch(path, {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded", Accept: "application/json" },
      body: body.toString(),
    });
    return { status: res.status, body: await res.json().catch(() => ({})) };
  }, [path, values]);
  const start = async (name, prompt) => {
    const res = await post(page, "/coders/new", { name: `${name} ${tag}`, project: dir, coder: "codex", prompt });
    assert(res.status === 200 && res.body.id, `start answered ${res.status} ${JSON.stringify(res.body)}`);
    return res.body.id;
  };
  const open = async (p, id) => {
    await p.goto(`${BASE}/coders/${id}`, { waitUntil: "domcontentloaded" });
    await p.waitForSelector(p === phone ? "#terminal-cursor-input" : "#terminal .xterm-screen canvas", { timeout: 15000 });
  };
  const startOnPhone = async (name) => {
    await phone.goto(`${BASE}/coders/new?project=${encodeURIComponent(project)}`, { waitUntil: "domcontentloaded" });
    const f = phone.locator('form:has(select[name="agent"])').first();
    await f.locator('select[name="coder"]').selectOption("codex");
    await f.locator('input[name="name"]').fill(`${name} ${tag}`);
    await Promise.all([phone.waitForURL(/\/coders\/(?!new)[^/]+$/, { timeout: 20000 }), f.locator('button[type="submit"]').first().tap()]);
    await phone.waitForSelector("#terminal-cursor-input", { timeout: 15000 });
    await shows(phone, "BELL on");
    return idOf(phone.url());
  };
  const typeLine = async (p, text) => {
    await p.keyboard.type(text, { delay: 15 });
    await p.keyboard.press("Enter");
  };
  // The coder's icon in its project's row on the board, read on a page that
  // stays there and follows the stream.
  const icon = (id) => `#project-${project} [data-notify-target="${id}"]`;
  const marked = (id) => board.evaluate((sel) => {
    const el = document.querySelector(sel);
    return !!el && el.classList.contains("working") && el.hasAttribute("data-working-since");
  }, icon(id));
  const working = (id, what) => waitFor(() => marked(id), what || `the working mark of ${id}`, 60);
  const idle = (id, what) => waitFor(async () => !(await marked(id)), what || `the working mark of ${id} going down`, 60);
  const news = async (id) => {
    const list = (await (await page.request.get(`${BASE}/notifications`)).json()).notifications || [];
    return list.filter((n) => n.targetId === id);
  };
  const newsCount = async (id) => (await news(id)).length;
  const activity = async (id) => (await page.request.get(`${BASE}/coders/${id}/activity`)).json();
  // The ids the project's row links as running, the way coder-codex.js reads
  // them.
  const running = () => page.evaluate(async (project) => {
    const html = await (await fetch("/projects")).text();
    const row = new DOMParser().parseFromString(html, "text/html").getElementById(`project-${project}`);
    return [...new Set([...(row ? row.querySelectorAll('a[href^="/coders/"]') : [])]
      .map((a) => (a.getAttribute("href").match(/^\/coders\/([0-9a-f-]{36})$/) || [])[1]).filter(Boolean))];
  }, project);

  let dir = "";
  let workID = "";
  let askID = "";
  let lateKey = "";
  let lateID = "";
  let awayKey = "";
  let awayID = "";
  let assistant = "";
  let doneID = "";
  let blockedID = "";
  let keyedKey = "";
  let keyedID = "";
  let askedKey = "";
  let askedID = "";
  const tookId = async (held, what) => {
    let id = "";
    await waitFor(async () => {
      id = (await running()).find((one) => ![workID, askID, lateKey, lateID, doneID, blockedID, ...held].includes(one)) || "";
      return id !== "";
    }, what);
    return id;
  };
  const jobs = (fields) => post(page, "/assistants/jobs", fields);
  const jobState = (terminal) => page.evaluate(async (id) => {
    const holder = document.createElement("div");
    holder.innerHTML = await (await fetch("/assistants/jobs", { headers: { Accept: "text/html" } })).text();
    const row = holder.querySelector(`[data-assistant-job="${id}"]`);
    return row ? (row.querySelector("[data-assistant-job-state]") || { dataset: {} }).dataset.assistantJobState || "" : "none";
  }, terminal);

  try {
    await L.login(page);
    await L.login(phone);
    await L.createProject(page, project);
    dir = await L.projectPath(page, project);
    await board.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });

    await run("codex is told to ring the plain bell on its command line", async () => {
      workID = await start("work", "WORK");
      await open(page, workID);
      await shows(page, "BELL on");
      await shows(page, "WORKING");
    });

    await run("the working mark is up while codex works, its title is no bell and no news", async () => {
      await working(workID);
      const a = await activity(workID);
      assert(a.finished === false && a.empty === false && a.screen === false, `the activity of a working codex reads ${JSON.stringify(a)}`);
      assert(a.text.includes("user: WORK"), `the activity misses the prompt: ${a.text}`);
      await sleep(3500);
      assert(await marked(workID), "the mark went down while codex worked");
      assert(await newsCount(workID) === 0, `a working codex made news: ${JSON.stringify(await news(workID))}`);
    });

    await run("the turn's end takes the mark down within seconds and is news", async () => {
      await page.click("#terminal");
      const ended = Date.now();
      await page.keyboard.press("Enter");
      await shows(page, "FINISHED");
      await idle(workID);
      await waitFor(async () => (await newsCount(workID)) === 1, "the news of the turn's end", 40);
      assert(Date.now() - ended < 8000, `the end took ${Date.now() - ended} ms to arrive`);
      const [n] = await news(workID);
      assert(n.title === "Coder has news." && n.project === project, `the news reads ${JSON.stringify(n)}`);
    });

    await run("the activity reading shows the words, the tool call and that the turn is over", async () => {
      const a = await activity(workID);
      assert(a.finished === true && a.screen === false, `the activity reads ${JSON.stringify(a)}`);
      assert(a.text === "user: WORK\ncoder ran shell\ncoder: The work is finished.", `the activity text is ${JSON.stringify(a.text)}`);
      assert(!a.text.includes("AGENTS.md"), "what codex sent the model beside the prompt stands in the reading");
    });

    await run("Escape aborts a turn: the mark goes down, an abort is no news", async () => {
      const before = await newsCount(workID);
      await page.click("#terminal");
      await typeLine(page, "WORK");
      await waitFor(async () => (await count(page, "WORKING")) >= 2, "the second turn working");
      await working(workID, "the mark of the second turn");
      await page.keyboard.press("Escape");
      await shows(page, "INTERRUPTED");
      await idle(workID, "the mark going down after Escape");
      const a = await activity(workID);
      assert(a.finished === true, `an aborted turn reads as running: ${JSON.stringify(a)}`);
      await sleep(2500);
      assert(await newsCount(workID) === before, "an aborted turn made news");
    });

    await run("the conversation is readable on a phone: the typed words alone, one coder turn with its tool line", async () => {
      await open(phone, workID);
      await phone.locator("[data-terminal-copy]:visible").first().tap();
      const view = `terminal-copy[terminal-id="${workID}"]`;
      await phone.waitForSelector(`${view} [data-copy-log] [data-message-id]`, { timeout: 10000 });
      const roles = await phone.locator(`${view} [data-copy-log] [data-message-id]`).evaluateAll((els) => els.map((el) => el.getAttribute("data-role")));
      assert(roles.join(",") === "user,coder,user", `the conversation holds ${roles}, want the finished turn and the words of the aborted one`);
      const text = await phone.locator(`${view} [data-copy-log]`).innerText();
      assert(text.includes("WORK") && text.includes("The work is finished."), `the conversation reads ${text.slice(0, 300)}`);
      assert(!text.includes("AGENTS.md"), "what codex sent the model beside the prompt stands in the conversation");
      const tools = await phone.locator(`${view} [data-copy-tool="shell"]`).allInnerTexts();
      assert(tools.length === 1 && tools[0].includes("make all"), `the tool lines are ${JSON.stringify(tools)}`);
      const box = await phone.locator(`${view} [data-copy-tool="shell"]`).first().boundingBox();
      assert(box && box.x >= 0 && box.x + box.width <= 390, `the tool line leaves the phone's width: ${JSON.stringify(box)}`);
    });

    await run("a question is seen within seconds: news, and the mark down while codex waits inside its tool call", async () => {
      askID = await start("ask", "");
      await open(page, askID);
      await shows(page, "MODE ask");
      await page.click("#terminal");
      await typeLine(page, "ASK");
      const asked = Date.now();
      await shows(page, "Allow command? (y/n)");
      await waitFor(async () => {
        const ids = await running();
        const now = ids.find((id) => id !== workID && id !== askID);
        if (now) askID = now;
        return (await newsCount(askID)) === 1;
      }, "the news of the question", 40);
      assert(Date.now() - asked < 8000, `the question took ${Date.now() - asked} ms to arrive`);
      await idle(askID, "the mark of a codex that waits for its person");
      const a = await activity(askID);
      assert(a.finished === false, `a turn that waits at a question reads as over: ${JSON.stringify(a)}`);
      await sleep(2500);
      assert(!(await marked(askID)), "the record put the mark back up while the question stands");
    });

    await run("the answer from the page lets the turn go on, its end is seen like any other", async () => {
      await page.click("#terminal");
      await typeLine(page, "y");
      await shows(page, "APPROVED");
      await waitFor(async () => (await activity(askID)).finished === true, "the turn's end in the activity");
      const a = await activity(askID);
      assert(a.text === "user: ASK\ncoder ran shell\ncoder: approved", `the activity text is ${JSON.stringify(a.text)}`);
      assert(!(await marked(askID)), "the mark is up behind the turn's end");
    });

    await run("a stopped session still says what it did and that its turn is over", async () => {
      const res = await post(page, `/coders/${askID}/stop`);
      assert(res.status < 400, `stop answered ${res.status}`);
      await waitFor(async () => !(await running()).includes(askID), "the coder leaving the running ones");
      const a = await activity(askID);
      assert(a.finished === true && a.text.endsWith("coder: approved"), `a stopped session reads ${JSON.stringify(a)}`);
    });

    await run("a resumed session comes back idle, and its next turn raises and drops the mark again", async () => {
      const res = await post(page, `/coders/${askID}/resume`);
      assert(res.status < 400, `resume answered ${res.status}`);
      await open(page, askID);
      await shows(page, `RESUMED ${askID}`);
      await sleep(3000);
      assert(!(await marked(askID)), "a resumed session came back marked as working");
      const before = await newsCount(askID);
      await page.click("#terminal");
      await typeLine(page, "WORK");
      await shows(page, "WORKING");
      await working(askID, "the mark of a turn in a resumed session");
      await page.keyboard.press("Enter");
      await shows(page, "FINISHED");
      await idle(askID, "the mark going down in a resumed session");
      await waitFor(async () => (await newsCount(askID)) > before, "the news of the turn's end in a resumed session", 40);
    });

    await run("a coder stopped in the middle of a turn reads as over, and resumed it comes back idle with the turn still over", async () => {
      await open(page, workID);
      await page.click("#terminal");
      await typeLine(page, "WORK");
      await waitFor(async () => (await count(page, "WORKING")) >= 3, "the third turn working");
      await working(workID, "the mark of the turn that is about to be cut off");
      assert((await activity(workID)).finished === false, "a turn in flight reads as over");
      const stopped = await post(page, `/coders/${workID}/stop`);
      assert(stopped.status < 400, `stop answered ${stopped.status}`);
      await waitFor(async () => !(await running()).includes(workID), "the coder leaving the running ones");
      const left = await activity(workID);
      assert(left.finished === true && left.text.endsWith("user: WORK"), `a session stopped mid turn reads ${JSON.stringify(left)}`);
      const resumed = await post(page, `/coders/${workID}/resume`);
      assert(resumed.status < 400, `resume answered ${resumed.status}`);
      await open(page, workID);
      await shows(page, `RESUMED ${workID}`);
      await sleep(3000);
      assert(!(await marked(workID)), "the turn a previous life left open marks the resumed coder as working");
      assert((await activity(workID)).finished === true, "the turn a previous life left open reads as running in the resumed coder");
    });

    await run("a coder started without a prompt on the phone: the first turn is marked and its news lands under codex's id", async () => {
      lateKey = await startOnPhone("late");
      await sleep(4000);
      assert(!(await marked(lateKey)), "a codex nobody talked to is marked as working");
      await phone.evaluate(() => document.getElementById("terminal-cursor-input").focus());
      await typeLine(phone, "WORK");
      await shows(phone, "WORKING");
      await waitFor(async () => {
        const ids = await running();
        lateID = ids.find((id) => id !== workID && id !== askID && id !== lateKey) || "";
        return lateID !== "";
      }, "the coder being listed under codex's own id");
      await working(lateID, "the mark under codex's own id");
      await phone.keyboard.press("Enter");
      await shows(phone, "FINISHED");
      await idle(lateID, "the mark going down under codex's own id");
      await waitFor(async () => (await newsCount(lateID)) === 1, "the news under codex's own id", 40);
      assert(await newsCount(lateKey) === 0, "the news went to the key the coder was started under");
      await waitFor(async () => (await news(lateID)).every((n) => n.read), "the phone looking at the coder under its key reading the news", 40);
      await sleep(1500);
      const seen = await phone.evaluate(() => ({ title: document.title, toasts: [...document.querySelectorAll(".dc-toast")].map((t) => t.textContent) }));
      assert(!/^\(\d+\)/.test(seen.title) && !seen.toasts.some((t) => t.includes(`late ${tag}`)), `the phone looking at the coder was told about its news: ${JSON.stringify(seen)}`);
    });

    await run("a steered codex coder gets its check on the turn's end, the job closes done and the coder's own news stays quiet", async () => {
      const made = await post(page, "/assistants/new", { form: "new", coder: "claude" });
      assistant = made.body.id || "";
      assert(assistant, `the run could not start an assistant of its own: ${made.status}`);
      doneID = await start("done", "WORK");
      await open(page, doneID);
      await shows(page, "WORKING");
      const steered = await jobs({ form: "steer", assistant, terminal: doneID, done_when: "WAKE_DONE the work is finished" });
      assert(steered.status === 200, `steering a codex coder answered ${steered.status} ${JSON.stringify(steered.body)}`);
      await sleep(2500);
      assert(await jobState(doneID) === "steering", `the job is ${await jobState(doneID)} while codex works`);
      await page.click("#terminal");
      await page.keyboard.press("Enter");
      await shows(page, "FINISHED");
      await waitFor(async () => (await jobState(doneID)) === "done", "the job closing done", 160);
      const own = await news(doneID);
      assert(own.length >= 1 && own.every((n) => n.read), `a steered codex coder rang the user: ${JSON.stringify(own)}`);
    });

    await run("a steered codex coder that asks gets its check on the question, the job closes blocked", async () => {
      blockedID = await start("blocked", "");
      await open(page, blockedID);
      await shows(page, "MODE ask");
      await page.click("#terminal");
      await typeLine(page, "hello");
      await shows(page, "ANSWER pong");
      await waitFor(async () => {
        const now = (await running()).find((id) => ![workID, askID, lateID, doneID, blockedID].includes(id));
        if (now) blockedID = now;
        return !!now;
      }, "the coder being listed under codex's own id");
      const steered = await jobs({ form: "steer", assistant, terminal: blockedID, done_when: "WAKE_BLOCKED it asks for a permission" });
      assert(steered.status === 200, `steering answered ${steered.status} ${JSON.stringify(steered.body)}`);
      await sleep(2500);
      assert(await jobState(blockedID) === "steering", `the job is ${await jobState(blockedID)} before codex asks`);
      await typeLine(page, "ASK");
      await shows(page, "Allow command? (y/n)");
      await waitFor(async () => (await jobState(blockedID)) === "blocked", "the job closing blocked", 160);
    });

    await run("codex's home holds what codex wrote and the cockpit's own folders, no config and no hooks", async () => {
      await phone.evaluate(() => document.getElementById("terminal-cursor-input").focus());
      await typeLine(phone, "HOME");
      await shows(phone, "HOMEFILES");
      const text = await mirrorOf(phone);
      const listed = JSON.parse((flat(text).match(/HOMEFILES(\[[^\]]*\])/) || [])[1] || "null");
      assert(Array.isArray(listed) && listed.includes("sessions"), `the home lists ${text.slice(-300)}`);
      const foreign = listed.filter((name) => !["sessions", "session_index.jsonl", "skills", "agents", "dev-cockpit-files", "AGENTS.md"].includes(name));
      assert(foreign.length === 0, `codex's home holds ${foreign}`);
    });

    await run("a codex coder steered under its start key before its first message gets its check on the turn's end, which reads its screen under codex's id", async () => {
      keyedKey = await startOnPhone("keyed");
      const steered = await jobs({ form: "steer", assistant, terminal: keyedKey, done_when: "WAKE_SCREEN its screen reads under the id it took" });
      assert(steered.status === 200 && steered.body.terminal === keyedKey, `steering under the start key answered ${steered.status} ${JSON.stringify(steered.body)}`);
      assert(await jobState(keyedKey) === "steering", `the job under the start key is ${await jobState(keyedKey)}`);
      await phone.evaluate(() => document.getElementById("terminal-cursor-input").focus());
      await typeLine(phone, "WORK");
      await shows(phone, "WORKING");
      keyedID = await tookId([keyedKey], "the steered coder being listed under codex's own id");
      await waitFor(async () => (await jobState(keyedID)) === "steering", "the job following the coder to codex's id", 20);
      assert(await jobState(keyedKey) === "none", "a job still stands under the start key");
      const ended = Date.now();
      await phone.keyboard.press("Enter");
      await shows(phone, "FINISHED");
      await waitFor(async () => (await jobState(keyedID)) === "done", "the job closing done, its check read the screen", 160);
      assert(Date.now() - ended < 30000, `the check took ${Date.now() - ended} ms to close the job`);
      const own = await news(keyedID);
      assert(own.length >= 1 && own.every((n) => n.read), `a steered codex coder rang the user: ${JSON.stringify(own)}`);
    });

    await run("a codex coder steered under its start key before it asks gets its check on the question, steer and release take either id", async () => {
      askedKey = await start("asked", "");
      const steered = await jobs({ form: "steer", assistant, terminal: askedKey, done_when: "WAKE_BLOCKED it asks for a permission" });
      assert(steered.status === 200, `steering under the start key answered ${steered.status} ${JSON.stringify(steered.body)}`);
      await open(page, askedKey);
      await shows(page, "MODE ask");
      await page.click("#terminal");
      await typeLine(page, "ASK");
      await shows(page, "Allow command? (y/n)");
      askedID = await tookId([keyedKey, keyedID, askedKey], "the asking coder being listed under codex's own id");
      await waitFor(async () => (await jobState(askedID)) === "blocked", "the job closing blocked on the question", 160);
      const own = await news(askedID);
      assert(own.length >= 1 && own.every((n) => n.read), `a steered codex coder rang the user: ${JSON.stringify(own)}`);
      const again = await jobs({ form: "steer", assistant, terminal: askedKey, done_when: "WAKE_NOTHING it answered" });
      assert(again.status === 200 && again.body.terminal === askedID, `steering again under the start key answered ${again.status} ${JSON.stringify(again.body)}`);
      assert(await jobState(askedID) === "steering", `the job steered again is ${await jobState(askedID)}`);
      const released = await jobs({ form: "release", terminal: askedKey });
      assert(released.status === 200, `releasing under the start key answered ${released.status} ${JSON.stringify(released.body)}`);
      assert(await jobState(askedID) === "stopped", `the released job is ${await jobState(askedID)}`);
    });

    await run("a coder started without a prompt whose page was left keeps its first turn's news for later", async () => {
      awayKey = await startOnPhone("away");
      await phone.evaluate(() => document.getElementById("terminal-cursor-input").focus());
      await typeLine(phone, "WORK");
      await shows(phone, "WORKING");
      awayID = await tookId([keyedKey, keyedID, askedKey, askedID, awayKey], "the coder being listed under codex's own id");
      await phone.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      const sent = await phone.evaluate(async (key) => (await fetch(`/coders/${key}/input`, {
        method: "POST",
        headers: { "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]').content, "Content-Type": "application/json" },
        body: JSON.stringify({ items: [{ raw: "\r" }] }),
      })).status, awayKey);
      assert(sent < 300, `Enter for the coder answered ${sent}`);
      await waitFor(async () => (await newsCount(awayID)) === 1, "the news under codex's own id", 40);
      await sleep(1500);
      const own = await news(awayID);
      assert(own.length === 1 && !own[0].read, `news nobody looked at was read: ${JSON.stringify(own)}`);
    });
  } finally {
    for (const id of [workID, askID, lateKey, lateID, doneID, blockedID, keyedKey, keyedID, askedKey, askedID, awayKey, awayID].filter(Boolean)) {
      await post(page, `/coders/${id}/stop`).catch(() => {});
      await post(page, `/coders/${id}/delete`).catch(() => {});
    }
    if (assistant) await post(page, `/assistants/${assistant}`, { form: "delete" }).catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
  }
  const anyFail = L.report("CODER-CODEX-TURNS", results, bag);
  await browser.close();
  process.exit(anyFail ? 1 : 0);
})();
