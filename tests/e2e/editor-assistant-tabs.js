const L = require("./lib");
const { assert, sleep, BASE } = L;

// Assistants as editor tabs: an assistant's conversation opens as a tab in
// the focused group through the assistant picker (Ctrl/Cmd+I or the rail's
// sparkle in the statusbar, the rows of GET /assistants/editor's
// [data-assistant-entry] elements after New assistant, which is selected when
// it opens, nothing of the terminals, no row leading back to what was used
// last; a click, a tap or Enter on a row opens its menu, whose Open opens the
// tab).
// The tab's path is //assistant/<id>, its
// saved entry {type: "assistant", id}, its body GET /assistants/<id>/tab, the
// page's own dc-assistant with the tab attribute: it pulls that address, its
// ids carry the assistant, and its aside waits hidden until the head's eye
// opens it in the editor's sheet. The front assistant of the focused group
// is the active one. Keys: the editor's own (Ctrl+J, Ctrl+I, Ctrl+Tab, Ctrl+Shift+X)
// work from the message box, a paste of a file attaches it there and never
// uploads it into the project. A file link in a message opens the file
// beside the tab, a link to a shell or an assistant as its tab. Marks: the
// working ring while a turn runs, the news dot, red for an unread failure,
// read once the tab shows. A rename follows, a deleted assistant closes its
// tab, a reload brings the tabs back. The tab and row menus offer Share and
// Delete as the assistant list's row does. On a phone a sideways swipe walks the
// editor's tabs from an assistant tab. Two assistant tabs share no id, their
// project views, @ lists and scroll keys stay their own. A guest of a project link sees no
// assistant, no sparkle and no picker on Ctrl+I, and reaches none.
// Instance like assistant.js's: tests/e2e/fakes first on PATH, a scratch
// HOME. The answers come from the fake claude (MAGIC, SLOW, FAIL,
// ECHO_ANSWER).
// Gotchas: a new assistant answers on the fake claude, which needs no login;
// the FAIL prompt fails once per session, so it runs on an assistant of its
// own; a message sent through the route instead of the box is how a check
// raises news behind another tab.

const ASK = ["ECHO_ANSWER", "Pick one.", "", "```ask", "Which database should the app use?", "- PostgreSQL", "- SQLite", "```", ""].join("\n");

L.runFeature("EDITOR-ASSISTANT-TABS", async ({ engine, browser, page, run, bag }) => {
  const tag = `eat-${engine}-${Date.now().toString(36)}`;
  const project = `zzeat-${tag}`;
  const editorURL = `${BASE}/projects/${project}/editor`;
  const made = [];
  let shell = "";
  let coder = "";
  const uploads = [];
  const streams = [];
  page.on("request", (r) => {
    const path = new URL(r.url()).pathname;
    if (r.method() === "POST" && /\/editor\/upload$/.test(path)) uploads.push(r.url());
    if (/^\/assistants\/[^/]+\/stream$/.test(path)) streams.push(path);
  });

  const postAs = (p, path, body) => p.evaluate(async ({ path, body }) => {
    const token = document.querySelector('meta[name="csrf-token"]')?.content || "";
    const res = await fetch(path, { method: "POST", headers: { "X-CSRF-Token": token, Accept: "application/json" }, body: new URLSearchParams(body) });
    return { ok: res.ok, status: res.status, text: await res.text() };
  }, { path, body });
  const newAssistant = async (title) => {
    const res = await postAs(page, "/assistants/new", { form: "new", coder: "claude" });
    assert(res.ok, `a new assistant answered ${res.status}: ${res.text.slice(0, 160)}`);
    const id = JSON.parse(res.text).id;
    made.push(id);
    assert((await postAs(page, `/assistants/${id}`, { form: "rename", title })).ok, "renaming the assistant failed");
    return id;
  };
  const openEditor = async (p = page) => {
    await p.goto(editorURL, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(p);
    await p.waitForSelector(".cm-editor, .editor-textarea", { state: "attached", timeout: 20000 });
    await sleep(600);
  };
  const tabSel = (id) => `.editor-tab[data-path="//assistant/${id}"]`;
  const hostSel = (id) => `.editor-assistant-box dc-assistant[assistant-id="${id}"]`;
  const inputSel = (id) => `${hostSel(id)} [data-assistant-input]`;
  const shown = (p, sel) => p.evaluate((s) => {
    const el = document.querySelector(s);
    if (!el) return false;
    const r = el.getBoundingClientRect();
    return r.width > 0 && r.height > 0 && getComputedStyle(el).visibility !== "hidden" && !!el.offsetParent;
  }, sel);
  const rect = (p, sel) => p.evaluate((s) => {
    const el = document.querySelector(s);
    if (!el) return null;
    const r = el.getBoundingClientRect();
    return { x: Math.round(r.x), y: Math.round(r.y), w: Math.round(r.width), h: Math.round(r.height), r: Math.round(r.right), b: Math.round(r.bottom) };
  }, sel);
  const groups = (p) => p.evaluate(() => [...document.querySelectorAll("[data-editor-group]")].map((g) => [...g.querySelectorAll(".editor-tab")].map((t) => (t.classList.contains("active") ? "*" : "") + t.dataset.path)));
  const ready = (p, id) => p.waitForSelector(`${hostSel(id)}[ready]`, { timeout: 15000 });
  const openPicker = async (p = page) => {
    const read = p.waitForResponse((r) => new URL(r.url()).pathname === "/assistants/editor", { timeout: 10000 }).catch(() => null);
    await p.keyboard.press("Control+i");
    await read;
    await p.waitForFunction(() => !document.querySelector("[data-editor-sheet]").hidden && !!document.querySelector("[data-editor-sheet-body] [data-asst-pick]"), null, { timeout: 10000 });
    await sleep(150);
  };
  const pick = async (id, p = page) => {
    await openPicker(p);
    await L.openPicked(p, `[data-asst-pick="${id}"]`);
    await ready(p, id);
    await sleep(300);
  };
  const messages = (p, id) => p.evaluate((s) => [...document.querySelectorAll(`${s} [data-assistant-message]`)].map((m) => `${m.getAttribute("data-role")}:${(m.querySelector("[data-assistant-text]")?.textContent || "").trim()}`), hostSel(id));
  const waitAnswer = async (p, id, needle, ms = 15000) => {
    for (let t = 0; t < ms; t += 250) {
      if ((await messages(p, id)).some((m) => m.startsWith("assistant:") && m.includes(needle))) return;
      await sleep(250);
    }
    throw new Error(`the tab of ${id} never showed the answer ${needle}: ${JSON.stringify(await messages(p, id))}`);
  };
  const plainSparkle = (p) => p.evaluate(() => {
    const b = document.querySelector("[data-editor-asst-status]");
    const shown = [...b.querySelectorAll("*")].filter((el) => { const r = el.getBoundingClientRect(); const c = getComputedStyle(el); return r.width > 0 && c.display !== "none" && c.visibility !== "hidden"; });
    return JSON.stringify(shown.map((el) => [el.tagName.toLowerCase(), ...el.classList].join("."))) === JSON.stringify(["i.ti.ti-sparkles"]) && b.textContent.trim() === "";
  });
  const tabIcon = (p, id) => p.evaluate((s) => document.querySelector(`${s} .dc-term-icon`)?.className || "", tabSel(id));
  const focusIn = (p, id) => p.evaluate((s) => !!document.querySelector(s)?.contains(document.activeElement), hostSel(id));
  const saved = (p) => p.evaluate((k) => JSON.parse(localStorage.getItem(k) || "null"), `dc-editor-tabs:${project}`);
  const fileChip = (p, id, name) => p.evaluate(({ s, name }) => [...document.querySelectorAll(`${s} [data-assistant-attachment]`)].map((c) => c.textContent.trim()).find((t) => t.startsWith(name)) || "", { s: hostSel(id), name });
  const waitChip = async (p, id, name) => {
    for (let t = 0; t < 8000; t += 200) {
      if (await p.evaluate(({ s, name }) => [...document.querySelectorAll(`${s} [data-assistant-attachment="${name}"]`)].some((c) => c.querySelector("[data-assistant-attachment-remove]")), { s: hostSel(id), name })) return;
      await sleep(200);
    }
    throw new Error(`${name} never stood attached: ${await fileChip(p, id, name)}`);
  };

  let alpha = "";
  let beta = "";
  try {
    await L.createProject(page, project);
    const dir = await L.projectPath(page, project);
    await openEditor();
    assert((await postAs(page, `/projects/${project}/editor/create`, { path: "notes.txt" })).ok, "creating notes.txt failed");
    alpha = await newAssistant(`alpha-${tag.slice(-5)}`);
    beta = await newAssistant(`beta-${tag.slice(-5)}`);
    shell = new URL(await L.createShell(page, project)).pathname.split("/").pop();
    const started = await postAs(page, "/coders/new", { name: `eat-${tag.slice(-5)}`, project: dir, coder: "claude", automatic_approval: "on", prompt: "job-eat" });
    assert(started.ok, `a new coder answered ${started.status}: ${started.text.slice(0, 160)}`);
    coder = JSON.parse(started.text).id;
    await openEditor();

    await run("desktop: Ctrl+I opens the assistant picker listing only the assistants with their marks and New assistant, Ctrl+J's lists none", async () => {
      const rowsNow = () => page.evaluate(() => [...document.querySelectorAll("[data-editor-sheet-body] .dropdown-item, [data-editor-sheet-body] .dropdown-header")].map((el) => ({
        pick: el.dataset.asstPick || "",
        term: el.dataset.termPick || "",
        text: el.textContent.replace(/\s+/g, " ").trim(),
        icon: el.querySelector(".dc-term-icon")?.className || "",
      })));
      await page.keyboard.press("Control+j");
      await page.waitForFunction((id) => document.querySelector("[data-editor-sheet-title]")?.textContent === "Terminals" && !!document.querySelector(`[data-editor-sheet-body] [data-term-pick="${id}"]`), shell, { timeout: 10000 });
      const terms = await rowsNow();
      assert(!terms.some((r) => r.pick || /assistant/i.test(r.text)), `the terminal picker lists assistants: ${JSON.stringify(terms.map((r) => r.text))}`);
      await page.keyboard.press("Escape");
      await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 5000 });
      await openPicker();
      const rows = await rowsNow();
      const title = await page.textContent("[data-editor-sheet-title]");
      assert(title === "Assistants", `the picker is titled ${title}`);
      const row = rows.findIndex((r) => r.pick === alpha);
      assert(row >= 0 && rows.some((r) => r.pick === beta), `the assistants are missing: ${JSON.stringify(rows.map((r) => r.text))}`);
      assert(!rows.some((r) => r.term || /New shell|New coder|Stopped coders|^Assistants$/.test(r.text)), `the assistant picker mixes in terminals: ${JSON.stringify(rows.map((r) => r.text))}`);
      const firstPick = rows.findIndex((r) => r.pick);
      assert(/^New assistant/.test(rows[0].text) && rows.slice(0, firstPick).every((r) => !r.pick), `the New actions do not stand first: ${JSON.stringify(rows.map((r) => r.text))}`);
      assert(rows[row].text.includes(`alpha-${tag.slice(-5)}`) && rows[row].text.includes("Claude"), `alpha's row reads ${rows[row].text}`);
      assert(/dc-term-icon assistant/.test(rows[row].icon), `alpha's row wears ${rows[row].icon}`);
      assert(rows.some((r) => /^New assistant/.test(r.text)), "the picker offers no New assistant");
      await page.keyboard.press("Escape");
    });

    await run("desktop: typing filters the picker, Enter opens the row's menu and Enter on its Open the assistant as a tab in the focused group with the focus in its box", async () => {
      await page.click('.editor-item:has-text("notes.txt")');
      await sleep(500);
      await openPicker();
      await page.keyboard.type(`alpha-${tag.slice(-5)}`);
      await sleep(250);
      await page.keyboard.press("Enter");
      await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
      await page.keyboard.press("Enter");
      await ready(page, alpha);
      await page.waitForSelector(`${tabSel(alpha)}.active`, { timeout: 8000 });
      assert(await shown(page, hostSel(alpha)), "the assistant tab is not on screen");
      assert(!(await shown(page, "[data-editor-sheet-panel]")), "the picker stayed open");
      const tab = await page.evaluate((s) => { const el = document.querySelector(s); return { name: el.querySelector(".editor-tab-name")?.textContent, icon: !!el.querySelector(".dc-term-icon.assistant"), title: el.title }; }, tabSel(alpha));
      assert(tab.name === `alpha-${tag.slice(-5)}` && tab.icon && tab.title.includes("Assistant"), `the tab reads ${JSON.stringify(tab)}`);
      let focused = false;
      for (let i = 0; i < 20 && !focused; i++) { focused = await page.evaluate((s) => document.activeElement === document.querySelector(s), inputSel(alpha)); if (!focused) await sleep(150); }
      assert(focused, "the message box does not have the focus");
      const box = await rect(page, hostSel(alpha));
      const group = await rect(page, ".editor-group-focused [data-editor-surface], .editor-group-focused .editor-surface");
      assert(box && group && box.w >= group.w - 2 && box.h >= group.h - 2, `the assistant does not fill its group: ${JSON.stringify({ box, group })}`);
    });

    await run("desktop: a second pick of an open assistant goes to its tab, never a second one", async () => {
      await page.click(`.editor-tab[data-path="notes.txt"]`);
      await sleep(300);
      await pick(alpha);
      const all = (await groups(page)).flat().filter((t) => t.endsWith(`//assistant/${alpha}`));
      assert(all.length === 1 && all[0].startsWith("*"), `the assistant stands in ${JSON.stringify(all)}`);
      assert(await page.evaluate((id) => document.querySelectorAll(`dc-assistant[assistant-id="${id}"]`).length, alpha) === 1, "the assistant is on the page twice");
    });

    await run("desktop: Enter sends from the box, the answer streams into the tab, Shift+Enter breaks the line", async () => {
      await page.click(inputSel(alpha));
      await page.keyboard.type("first");
      await page.keyboard.press("Shift+Enter");
      await page.keyboard.type("MAGIC second");
      assert(await page.inputValue(inputSel(alpha)) === "first\nMAGIC second", "Shift+Enter did not break the line");
      await page.keyboard.press("Enter");
      await waitAnswer(page, alpha, "AIRPORT");
      assert(await page.inputValue(inputSel(alpha)) === "", "the box kept the sent words");
      assert((await messages(page, alpha)).some((m) => m === "user:first\nMAGIC second" || m.startsWith("user:first")), "the sent message is not in the thread");
    });

    await run("desktop: Ctrl+Enter and Meta+Enter send from the box as on the page", async () => {
      const answers = () => messages(page, alpha).then((all) => all.filter((m) => m.startsWith("assistant:") && m.includes("AIRPORT")).length);
      for (const [combo, words] of [["Control+Enter", "MAGIC by ctrl"], ["Meta+Enter", "MAGIC by meta"]]) {
        const before = await answers();
        await page.click(inputSel(alpha));
        await page.keyboard.type(words);
        await page.keyboard.press(combo);
        await page.waitForFunction(({ s, w }) => [...document.querySelectorAll(`${s} [data-assistant-message][data-role="user"] [data-assistant-text]`)].some((t) => t.textContent.trim() === w), { s: hostSel(alpha), w: words }, { timeout: 8000 });
        assert(await page.inputValue(inputSel(alpha)) === "", `${combo} left ${JSON.stringify(await page.inputValue(inputSel(alpha)))} in the box`);
        for (let t = 0; t < 15000 && (await answers()) <= before; t += 250) await sleep(250);
        assert((await answers()) > before, `${combo} got no answer: ${JSON.stringify((await messages(page, alpha)).slice(-2))}`);
        await page.waitForFunction((h) => !document.querySelector(h).hasAttribute("running"), hostSel(alpha), { timeout: 15000 });
      }
    });

    await run("desktop: the tab wears the working ring while a turn runs, its picker row too, and the end is followed", async () => {
      await page.fill(inputSel(alpha), "SLOW running");
      await page.keyboard.press("Enter");
      await page.waitForFunction((s) => /working/.test(document.querySelector(`${s} .dc-term-icon`)?.className || ""), tabSel(alpha), { timeout: 10000 });
      assert(await plainSparkle(page), "a running turn put a mark on the statusbar's sparkles");
      await openPicker();
      const row = await page.evaluate((id) => { const r = document.querySelector(`[data-editor-sheet-body] [data-asst-pick="${id}"]`); return { text: r.textContent.replace(/\s+/g, " ").trim(), icon: r.querySelector(".dc-term-icon").className }; }, alpha);
      assert(/ open$/.test(row.text) && /working/.test(row.icon), `the picker's row reads ${JSON.stringify(row)}`);
      await page.keyboard.press("Escape");
      await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 5000 });
      assert(await focusIn(page, alpha), "Escape on the picker did not give the message box its focus back");
      await page.waitForFunction((s) => [...document.querySelectorAll(`${s} .dc-msg-assistant [data-assistant-text]`)].pop()?.textContent.includes("Working"), hostSel(alpha), { timeout: 10000 });
      await sleep(500);
      const scroll = await page.evaluate((s) => { const e = document.querySelector(`${s} [data-assistant-scroll]`); return e.scrollHeight - e.clientHeight - e.scrollTop; }, hostSel(alpha));
      assert(scroll <= 4, `the thread does not stand at its end: ${scroll}px above`);
      await page.click(`${hostSel(alpha)} [data-assistant-cancel]`);
      await page.waitForFunction((s) => !document.querySelector(s).hasAttribute("running"), hostSel(alpha), { timeout: 15000 });
      await page.waitForFunction((s) => !/working/.test(document.querySelector(`${s} .dc-term-icon`)?.className || ""), tabSel(alpha), { timeout: 15000 });
    });

    await run("desktop: the jump to end button brings a reader who scrolled up back to the end", async () => {
      for (let i = 0; i < 4; i++) {
        await page.fill(inputSel(alpha), `MAGIC filler ${i}\n` + "line\n".repeat(10));
        await page.keyboard.press("Enter");
        await waitAnswer(page, alpha, "AIRPORT");
        await sleep(400);
      }
      await page.evaluate((s) => { const e = document.querySelector(`${s} [data-assistant-scroll]`); e.dispatchEvent(new WheelEvent("wheel", { deltaY: -600, bubbles: true })); e.scrollTop = 0; }, hostSel(alpha));
      await page.waitForFunction((s) => { const b = document.querySelector(`${s} [data-chat-end]`); return b && !b.hidden && b.getBoundingClientRect().width > 0; }, hostSel(alpha), { timeout: 5000 });
      await page.click(`${hostSel(alpha)} [data-chat-end]`);
      await page.waitForFunction((s) => { const e = document.querySelector(`${s} [data-assistant-scroll]`); return e.scrollHeight - e.clientHeight - e.scrollTop <= 4; }, hostSel(alpha), { timeout: 5000 });
      assert(!(await shown(page, `${hostSel(alpha)} [data-chat-end]`)), "the jump button stayed after the jump");
    });

    await run("desktop: files attach through the button, a paste and a drop, and none goes into the project", async () => {
      const before = uploads.length;
      const chooser = page.waitForEvent("filechooser", { timeout: 5000 });
      await page.click(`${hostSel(alpha)} [data-assistant-attach]`);
      await (await chooser).setFiles({ name: "button.txt", mimeType: "text/plain", buffer: Buffer.from("from the button\n") });
      await waitChip(page, alpha, "button.txt");
      await page.click(inputSel(alpha));
      await page.evaluate((s) => {
        const dt = new DataTransfer();
        dt.items.add(new File(["pasted\n"], "pasted.txt", { type: "text/plain" }));
        document.querySelector(s).dispatchEvent(new ClipboardEvent("paste", { clipboardData: dt, bubbles: true, cancelable: true }));
      }, inputSel(alpha));
      await waitChip(page, alpha, "pasted.txt");
      await page.evaluate((s) => {
        const target = document.querySelector(`${s} [data-assistant-scroll]`);
        const dt = new DataTransfer();
        dt.items.add(new File(["dropped\n"], "dropped.txt", { type: "text/plain" }));
        for (const type of ["dragenter", "dragover", "drop"]) target.dispatchEvent(new DragEvent(type, { dataTransfer: dt, bubbles: true, cancelable: true }));
      }, hostSel(alpha));
      await waitChip(page, alpha, "dropped.txt");
      assert(uploads.length === before, `a file went into the project: ${uploads.slice(before).join(", ")}`);
    });

    await run("desktop (chromium): an attachment shows its progress while it uploads", async () => {
      if (engine !== "chromium") return "chromium only, the throttle is a devtools call";
      const cdp = await page.context().newCDPSession(page);
      await cdp.send("Network.enable");
      await cdp.send("Network.emulateNetworkConditions", { offline: false, latency: 50, downloadThroughput: 200 * 1024, uploadThroughput: 64 * 1024 });
      const seen = [];
      const watch = setInterval(() => { void fileChip(page, alpha, "big.bin").then((t) => { if (t) seen.push(t); }).catch(() => {}); }, 100);
      try {
        await page.setInputFiles(`${hostSel(alpha)} input[data-assistant-file]`, { name: "big.bin", mimeType: "application/octet-stream", buffer: Buffer.alloc(400 * 1024, 7) });
        await waitChip(page, alpha, "big.bin");
      } finally {
        clearInterval(watch);
        await cdp.send("Network.emulateNetworkConditions", { offline: false, latency: 0, downloadThroughput: -1, uploadThroughput: -1 });
        await cdp.detach();
      }
      assert(seen.some((t) => /\d+%/.test(t)), `no progress stood on the chip: ${JSON.stringify([...new Set(seen)])}`);
      await page.click(`${hostSel(alpha)} [data-assistant-attachment-remove="big.bin"]`);
    });

    await run("desktop: the draft and its files come back after a reload, the tab with them", async () => {
      await page.fill(inputSel(alpha), "kept for later");
      await page.waitForResponse((r) => r.request().method() === "POST" && new URL(r.url()).pathname === `/assistants/${alpha}`, { timeout: 5000 }).catch(() => {});
      await sleep(600);
      await page.reload({ waitUntil: "domcontentloaded" });
      await ready(page, alpha);
      await sleep(400);
      assert(await page.inputValue(inputSel(alpha)) === "kept for later", `the box holds ${JSON.stringify(await page.inputValue(inputSel(alpha)))}`);
      for (const name of ["button.txt", "pasted.txt", "dropped.txt"]) assert(await fileChip(page, alpha, name), `${name} did not come back`);
      const entry = (await saved(page))?.open?.find((e) => e && e.type === "assistant");
      assert(entry && entry.id === alpha, `the stored tabs carry no assistant entry: ${JSON.stringify((await saved(page))?.open)}`);
      await page.fill(inputSel(alpha), "");
      await page.evaluate((s) => { for (const b of document.querySelectorAll(`${s} [data-assistant-attachment-remove]`)) b.click(); }, hostSel(alpha));
      await sleep(500);
    });

    await run("desktop: an ask block in the tab takes its answer", async () => {
      const block = `${hostSel(alpha)} dc-assistant-choices`;
      const states = () => page.evaluate((b) => [...document.querySelectorAll(b)].map((c) => c.getAttribute("data-choices-state")), block);
      const before = (await states()).length;
      await page.fill(inputSel(alpha), ASK);
      await page.keyboard.press("Enter");
      for (let t = 0; t < 15000 && !((await states()).length > before && (await states()).pop() === "open"); t += 250) await sleep(250);
      assert((await states()).length > before && (await states()).pop() === "open", `no open ask block came: ${JSON.stringify(await states())}, ${JSON.stringify((await messages(page, alpha)).slice(-3))}`);
      await page.locator(block).last().locator("[data-choices-option]").nth(1).click();
      await sleep(300);
      await page.locator(block).last().locator("[data-choices-send]").click();
      await page.waitForFunction((b) => [...document.querySelectorAll(b)].pop()?.getAttribute("data-choices-state") === "answered", block, { timeout: 15000 });
    });

    await run("desktop: the eye opens what the assistant watches in the editor's sheet, Escape puts it back into the tab", async () => {
      await page.click(`${hostSel(alpha)} [data-assistant-watching-button]`);
      await page.waitForFunction(() => document.querySelector("[data-editor-sheet-title]")?.textContent === "Watching" && !document.querySelector("[data-editor-sheet]").hidden, null, { timeout: 5000 });
      assert(await shown(page, "[data-editor-sheet-body] [data-assistant-aside]"), "the aside is not in the sheet");
      for (const t of ["projects", "coders", "triggers"]) assert(await shown(page, `[data-editor-sheet-body] [data-assistant-${t}-tab]`), `the sheet misses the ${t} tab`);
      await page.click("[data-editor-sheet-body] [data-assistant-triggers-tab]");
      await page.waitForFunction((id) => { const p = document.getElementById(`assistant-${id}-triggers`); return p && p.classList.contains("active") && p.getBoundingClientRect().height > 0; }, alpha, { timeout: 4000 });
      assert(await shown(page, "[data-editor-sheet-body] [data-assistant-trigger-new]"), "the triggers tab misses New trigger");
      await page.keyboard.press("Escape");
      await page.waitForFunction(() => document.querySelector("[data-editor-sheet]").hidden, null, { timeout: 4000 });
      assert(await page.evaluate((s) => !!document.querySelector(`${s} [data-assistant-aside-home] [data-assistant-aside]`), hostSel(alpha)), "the aside did not go back into its tab");
      assert(!(await shown(page, `${hostSel(alpha)} [data-assistant-aside]`)), "the aside shows inside the tab");
    });

    await run("desktop: Ctrl+I, Cmd+I and Ctrl+J open their pickers from the message box with no row leading back, Escape gives the box the focus back, Ctrl+Tab walks on from it", async () => {
      const sheetOpen = (title) => page.waitForFunction((t) => !document.querySelector("[data-editor-sheet]").hidden && document.querySelector("[data-editor-sheet-title]").textContent === t && !!document.querySelector("[data-editor-sheet-body] .dropdown-item:not([disabled])"), title, { timeout: 6000 });
      const sheetShut = () => page.waitForFunction(() => document.querySelector("[data-editor-sheet]").hidden, null, { timeout: 4000 });
      const firstRow = () => page.evaluate(() => {
        const items = [...document.querySelectorAll("[data-editor-sheet-body] .dropdown-item")];
        const entry = items.find((el) => el.dataset.asstPick || el.dataset.termPick);
        return {
          head: items[0]?.textContent.replace(/\s+/g, " ").trim() || "",
          asst: entry?.dataset.asstPick || "",
          term: entry?.dataset.termPick || "",
          marked: !!items[0]?.classList.contains("selected") && items.filter((el) => el.classList.contains("selected")).length === 1,
          jump: !!document.querySelector("[data-editor-sheet-body] [data-term-jump]") || items.some((el) => /\bLast (file|terminal|assistant)\b/.test(el.textContent)),
        };
      });
      const inBox = () => page.evaluate((s) => document.activeElement === document.querySelector(s), inputSel(alpha));
      await page.click(inputSel(alpha));
      for (const key of ["Control+i", "Meta+i"]) {
        await page.keyboard.press(key);
        await sheetOpen("Assistants");
        const first = await firstRow();
        assert(!first.jump && first.asst && first.marked && /^New assistant/.test(first.head), `${key}'s picker reads ${JSON.stringify(first)}`);
        await page.keyboard.press("Escape");
        await sheetShut();
        assert(await inBox(), `the focus did not come back to the box after ${key}`);
      }
      await page.keyboard.press("Control+j");
      await sheetOpen("Terminals");
      const term = await firstRow();
      assert(!term.jump && term.term && term.marked && term.head === "New shell", `Ctrl+J's picker from the box reads ${JSON.stringify(term)}`);
      await page.keyboard.press("Escape");
      await sheetShut();
      assert(await inBox(), "the focus did not come back to the box after Ctrl+J");
      await page.keyboard.press("Control+Tab");
      await sleep(400);
      const front = (await groups(page)).flat().find((t) => t.startsWith("*"));
      assert(front !== `*//assistant/${alpha}`, `Ctrl+Tab stayed on the assistant: ${front}`);
      await page.keyboard.press("Control+Shift+Tab");
      await sleep(400);
      assert((await groups(page)).flat().includes(`*//assistant/${alpha}`), "Ctrl+Shift+Tab did not come back");
    });

    await run("desktop: a split keeps the assistant and its stream, two side by side answer on their own, the keys go to the focused one, only it is active", async () => {
      const node = await page.evaluateHandle((s) => document.querySelector(s), hostSel(alpha));
      const opened = streams.filter((p) => p.includes(alpha)).length;
      await page.click(tabSel(alpha), { button: "right" });
      await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
      await page.evaluate(() => [...document.querySelectorAll(".dc-context-menu .dropdown-item")].find((el) => el.textContent.trim() === "Split right").click());
      await sleep(800);
      assert(await page.evaluate(({ n, s }) => n === document.querySelector(s) && n.hasAttribute("ready"), { n: node, s: hostSel(alpha) }), "the split replaced the assistant or took it down");
      assert(streams.filter((p) => p.includes(alpha)).length === opened, "moving the tab into a new group opened its stream again");
      await page.click(`.editor-tab[data-path="notes.txt"]`);
      await pick(beta);
      const layout = await groups(page);
      assert(layout.length === 2 && layout[0].includes(`*//assistant/${beta}`) && layout[1].includes(`*//assistant/${alpha}`), `the groups read ${JSON.stringify(layout)}`);
      const a = await rect(page, hostSel(alpha));
      const b = await rect(page, hostSel(beta));
      assert(b.r <= a.x + 1, `the two overlap: ${JSON.stringify({ a, b })}`);
      const active = () => page.evaluate(() => [...document.querySelectorAll("dc-assistant[active]")].map((e) => e.getAttribute("assistant-id")));
      assert(JSON.stringify(await active()) === JSON.stringify([beta]), `active reads ${JSON.stringify(await active())}`);
      await page.keyboard.type("MAGIC for beta");
      await page.click(inputSel(alpha));
      assert(JSON.stringify(await active()) === JSON.stringify([alpha]), `after the click active reads ${JSON.stringify(await active())}`);
      await page.keyboard.type("STREAM_PARAGRAPH for alpha");
      await page.keyboard.press("Enter");
      await page.click(inputSel(beta));
      await page.keyboard.press("Enter");
      await waitAnswer(page, beta, "AIRPORT");
      await waitAnswer(page, alpha, "workspace");
      assert(!(await messages(page, beta)).some((m) => m.includes("STREAM_PARAGRAPH")), "alpha's words landed in beta");
      assert(!(await messages(page, alpha)).some((m) => m.includes("for beta")), "beta's words landed in alpha");
      await page.evaluate((s) => {
        const dt = new DataTransfer();
        dt.setData("text/plain", "pasted text");
        const box = document.querySelector(s);
        box.focus();
        box.dispatchEvent(new ClipboardEvent("paste", { clipboardData: dt, bubbles: true, cancelable: true }));
      }, inputSel(beta));
      assert(await page.inputValue(inputSel(alpha)) === "", "a paste into beta reached alpha");
    });

    await run("desktop (chromium): voice records in the assistant tab it starts in, by its button and by Alt Alt, and its words land there, not in the tab beside it", async () => {
      if (engine !== "chromium") return "chromium only, this WebKit has no MediaRecorder and the page offers no microphone there either";
      await L.stubMicrophone(page);
      const clips = [];
      await page.route("**/stt", (route) => {
        clips.push(new URL(route.request().url()).pathname);
        return L.answerSTT(route);
      });
      const send = (id) => `${hostSel(id)} [data-assistant-send]`;
      const spoken = (id) => page.evaluate((h) => [...document.querySelectorAll(`${h} [data-assistant-message][data-role="user"] [data-assistant-text]`)].filter((t) => t.textContent.trim() === "MAGIC spoken words").length, hostSel(id));
      const toggle = async (id, how) => {
        if (how === "button") await page.click(send(id));
        else {
          await page.keyboard.press("Alt");
          await page.keyboard.press("Alt");
        }
      };
      try {
        await page.fill(inputSel(alpha), "");
        await page.fill(inputSel(beta), "");
        await sleep(400);
        for (const [id, other, how] of [[beta, alpha, "button"], [alpha, beta, "Alt Alt"]]) {
          const before = { own: await spoken(id), other: await spoken(other) };
          await page.click(inputSel(id));
          assert((await page.getAttribute(`${send(id)} i`, "class")).includes("ti-microphone"), `${how}: the empty box offers no microphone`);
          await toggle(id, how);
          await page.waitForFunction((b) => document.querySelector(b)?.getAttribute("aria-label") === "Send the recording", send(id), { timeout: 8000 });
          assert((await page.getAttribute(send(other), "aria-label")) !== "Send the recording", `${how}: the tab beside it records too`);
          await sleep(1200);
          await toggle(id, how);
          await page.waitForFunction(({ h, n }) => [...document.querySelectorAll(`${h} [data-assistant-message][data-role="user"] [data-assistant-text]`)].filter((t) => t.textContent.trim() === "MAGIC spoken words").length > n, { h: hostSel(id), n: before.own }, { timeout: 15000 });
          assert(clips[clips.length - 1] === `/assistants/${id}/stt`, `${how}: the clip went to ${clips[clips.length - 1]}`);
          assert(await spoken(other) === before.other && await page.inputValue(inputSel(other)) === "", `${how}: the words reached the tab beside it`);
          await page.waitForFunction((h) => !document.querySelector(h).hasAttribute("running"), hostSel(id), { timeout: 15000 });
        }
      } finally {
        await page.unroute("**/stt");
      }
    });

    await run("desktop: a file link in a message opens the file beside the tab, a coder's, a shell's and an assistant's link open their tabs", async () => {
      await page.click(inputSel(alpha));
      const answer = ["ECHO_ANSWER", `See ${dir}/notes.txt:1 first.`, "", `Then [the shell](/shells/${shell}), [the coder](/coders/${coder}) and [beta](/assistants/${beta}).`, ""].join("\n");
      await page.fill(inputSel(alpha), answer);
      await page.keyboard.press("Enter");
      await page.waitForSelector(`${hostSel(alpha)} [data-assistant-message][data-role="assistant"]:last-of-type a[data-file-link], ${hostSel(alpha)} .dc-msg-assistant a[data-file-link]`, { timeout: 15000 });
      await page.click(`.editor-tab[data-path="//assistant/${beta}"]`);
      await page.click(`.editor-tab[data-path="notes.txt"]`);
      await page.click(tabSel(alpha));
      await sleep(300);
      const links = `${hostSel(alpha)} .dc-msg-assistant`;
      await page.evaluate((s) => [...document.querySelectorAll(`${s} a[data-file-link]`)].pop().click(), links);
      await sleep(1200);
      let layout = await groups(page);
      assert(layout[1].includes(`*//assistant/${alpha}`), `the assistant left the front of its group: ${JSON.stringify(layout)}`);
      assert(layout[0].includes("*notes.txt"), `the file did not open beside it: ${JSON.stringify(layout)}`);
      assert(page.url().startsWith(editorURL), `the page left the editor: ${page.url()}`);
      await page.click(tabSel(alpha));
      await page.evaluate(({ s, shell }) => [...document.querySelectorAll(`${s} a[href="/shells/${shell}"]`)].pop().click(), { s: links, shell });
      await page.waitForSelector(`.editor-tab[data-path="//terminal/${shell}"]`, { timeout: 8000 });
      layout = await groups(page);
      assert(layout[1].includes(`*//assistant/${alpha}`) && layout[0].includes(`*//terminal/${shell}`), `the shell's link reads ${JSON.stringify(layout)}`);
      await page.click(tabSel(alpha));
      await page.evaluate(({ s, coder }) => [...document.querySelectorAll(`${s} a[href="/coders/${coder}"]`)].pop().click(), { s: links, coder });
      await page.waitForSelector(`.editor-tab[data-path="//terminal/${coder}"]`, { timeout: 8000 });
      layout = await groups(page);
      assert(layout[1].includes(`*//assistant/${alpha}`) && layout[0].includes(`*//terminal/${coder}`), `the coder's link reads ${JSON.stringify(layout)}`);
      assert(page.url().startsWith(editorURL), `the coder's link left the editor: ${page.url()}`);
      await page.click(tabSel(alpha));
      await page.evaluate(({ s, beta }) => [...document.querySelectorAll(`${s} a[href="/assistants/${beta}"]`)].pop().click(), { s: links, beta });
      await sleep(800);
      layout = await groups(page);
      assert(layout[0].includes(`*//assistant/${beta}`) && layout[1].includes(`*//assistant/${alpha}`), `the assistant's link reads ${JSON.stringify(layout)}`);
      assert(page.url().startsWith(editorURL), `the page left the editor: ${page.url()}`);
    });

    await run("desktop: a stopped coder of the project resumed from the assistant tab's project card, its Coders tab and a link in a message comes back running in a tab beside it, the page stays in the editor", async () => {
      const coderTab = `.editor-tab[data-path="//terminal/${coder}"]`;
      const coderBox = `.editor-term-box:has([data-terminal-view="${coder}"])`;
      const stoppedIn = `.editor-term-box form[data-term-resume] [data-resume-id="${coder}"]`;
      const steered = await postAs(page, "/assistants/jobs", { form: "steer", terminal: coder, assistant: alpha, done_when: "" });
      assert(steered.ok, `steering the coder answered ${steered.status}: ${steered.text.slice(0, 160)}`);
      const stop = async () => {
        if (!(await page.$(coderTab))) {
          await page.click(tabSel(alpha));
          await page.evaluate(({ s, coder }) => [...document.querySelectorAll(`${s} a[href="/coders/${coder}"]`)].pop().click(), { s: `${hostSel(alpha)} .dc-msg-assistant`, coder });
          await page.waitForSelector(coderTab, { timeout: 8000 });
        }
        const stopped = await postAs(page, `/coders/${coder}/stop`, {});
        assert(stopped.ok, `stopping the coder answered ${stopped.status}`);
        await page.waitForSelector(stoppedIn, { state: "attached", timeout: 15000 });
        await page.click(coderTab, { button: "middle" });
        await page.waitForSelector(coderTab, { state: "detached", timeout: 5000 });
        await page.click(tabSel(alpha));
        await sleep(300);
      };
      const running = async (how) => {
        await page.waitForSelector(`${coderTab}.active`, { timeout: 15000 });
        await page.waitForSelector(`${coderBox} terminal-attach`, { timeout: 15000 });
        await page.waitForFunction((s) => !document.querySelector(s), stoppedIn, { timeout: 10000 });
        assert(page.url().startsWith(editorURL), `${how} left the editor: ${page.url()}`);
        const layout = await groups(page);
        assert(layout.some((g) => g.includes(`*//terminal/${coder}`)) && layout.some((g) => g.includes(`*//assistant/${alpha}`)), `${how} reads ${JSON.stringify(layout)}`);
        assert(await page.evaluate(() => document.querySelector("[data-editor-sheet]").hidden), `${how} left the sheet open`);
      };
      const watching = async () => {
        await page.click(`${hostSel(alpha)} [data-assistant-watching-button]`);
        await page.waitForFunction(() => document.querySelector("[data-editor-sheet-title]")?.textContent === "Watching" && !document.querySelector("[data-editor-sheet]").hidden, null, { timeout: 5000 });
      };
      try {
        await stop();
        await watching();
        await page.click("[data-editor-sheet-body] [data-assistant-projects-tab]");
        const cardRow = `[data-editor-sheet-body] form[data-assistant-project-job="${coder}"] [data-assistant-job-resume]`;
        await page.waitForSelector(cardRow, { state: "visible", timeout: 15000 });
        await page.click(cardRow);
        await running("the project card's row");
        await stop();
        await watching();
        await page.click("[data-editor-sheet-body] [data-assistant-coders-tab]");
        const jobRow = `[data-editor-sheet-body] [data-assistant-jobs-list] form[action="/coders/${coder}/resume"] [data-assistant-job-resume]`;
        await page.waitForSelector(jobRow, { state: "visible", timeout: 15000 });
        await page.click(jobRow);
        await running("the Coders tab's Resume coder");
        await stop();
        await page.evaluate(({ s, coder }) => [...document.querySelectorAll(`${s} a[href="/coders/${coder}"]`)].pop().click(), { s: `${hostSel(alpha)} .dc-msg-assistant`, coder });
        await running("the message's link");
      } finally {
        if (!(await page.evaluate(() => document.querySelector("[data-editor-sheet]").hidden))) await page.keyboard.press("Escape");
        await postAs(page, "/assistants/jobs", { form: "release", terminal: coder }).catch(() => {});
      }
      return "card row, Coders tab and message link";
    });

    await run("desktop: news behind another tab marks it blue, an unread failure red, and showing the tab reads it", async () => {
      await page.click(`.editor-tab[data-path="notes.txt"]`);
      await sleep(300);
      assert((await postAs(page, `/assistants/${beta}`, { form: "message", message: "MAGIC behind" })).ok, "sending beta a message failed");
      await page.waitForFunction((s) => /\bnews\b/.test(document.querySelector(`${s} .dc-term-icon`)?.className || ""), tabSel(beta), { timeout: 20000 });
      assert(!/text-danger/.test(await tabIcon(page, beta)), `a plain answer wears red: ${await tabIcon(page, beta)}`);
      const failing = await newAssistant(`fail-${tag.slice(-5)}`);
      await pick(failing);
      await page.click(`.editor-tab[data-path="notes.txt"]`);
      await sleep(300);
      assert((await postAs(page, `/assistants/${failing}`, { form: "message", message: "FAIL now" })).ok, "sending the failing message failed");
      await page.waitForFunction((s) => /text-danger/.test(document.querySelector(`${s} .dc-term-icon`)?.className || ""), tabSel(failing), { timeout: 20000 });
      assert(/\bnews\b/.test(await tabIcon(page, failing)), `the failure wears no news dot: ${await tabIcon(page, failing)}`);
      await page.click(tabSel(failing));
      await page.waitForFunction((s) => { const c = document.querySelector(`${s} .dc-term-icon`)?.className || ""; return !/\bnews\b/.test(c) && !/text-danger/.test(c); }, tabSel(failing), { timeout: 10000 });
      await page.click(tabSel(beta));
      await page.waitForFunction((s) => !/\bnews\b/.test(document.querySelector(`${s} .dc-term-icon`)?.className || ""), tabSel(beta), { timeout: 10000 });
      await postAs(page, `/assistants/${failing}`, { form: "delete" });
    });

    await run("desktop: a rename follows on the tab and its head, a reload brings the tabs back in their groups, a deleted assistant takes its tab", async () => {
      assert((await postAs(page, `/assistants/${beta}`, { form: "rename", title: `renamed-${tag.slice(-5)}` })).ok, "the rename failed");
      await page.waitForFunction(({ s, want }) => document.querySelector(`${s} .editor-tab-name`)?.textContent === want, { s: tabSel(beta), want: `renamed-${tag.slice(-5)}` }, { timeout: 10000 });
      const before = await groups(page);
      await page.reload({ waitUntil: "domcontentloaded" });
      await page.waitForSelector(tabSel(alpha), { timeout: 15000 });
      await sleep(800);
      const after = await groups(page);
      assert(JSON.stringify(after.map((g) => g.map((t) => t.replace("*", "")))) === JSON.stringify(before.map((g) => g.map((t) => t.replace("*", "")))), `the tabs came back as ${JSON.stringify(after)}, were ${JSON.stringify(before)}`);
      assert((await page.textContent(`${hostSel(beta)} [data-assistant-title]`).catch(() => "")) === `renamed-${tag.slice(-5)}` || !(await page.$(hostSel(beta))), "the head of the renamed assistant reads another name");
      assert((await postAs(page, `/assistants/${beta}`, { form: "delete" })).ok, "deleting beta failed");
      await page.waitForSelector(tabSel(beta), { state: "detached", timeout: 10000 });
      assert(!(await page.$(`dc-assistant[assistant-id="${beta}"]`)), "the deleted assistant is still on the page");
      await page.reload({ waitUntil: "domcontentloaded" });
      await page.waitForSelector(tabSel(alpha), { timeout: 15000 });
      await sleep(600);
      assert(!(await page.$(tabSel(beta))), "the deleted assistant came back after a reload");
    });

    await run("desktop: the statusbar's sparkles open the assistant picker, a click on an assistant row opens its menu whose Open opens it as a tab, a terminal row's menu stays the terminal's, New assistant opens as a tab", async () => {
      await page.click(tabSel(alpha), { button: "middle" });
      await page.waitForSelector(tabSel(alpha), { state: "detached", timeout: 5000 });
      const menuOf = async (sel) => {
        await page.click(sel);
        await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
        const labels = await page.$$eval(".dc-context-menu .dropdown-item", (els) => els.map((el) => (el.querySelector(".dc-menu-label-head") || el).textContent.trim()));
        await page.keyboard.press("Escape");
        await page.waitForSelector(".dc-context-menu", { state: "detached", timeout: 4000 }).catch(() => {});
        await sleep(700);
        return labels;
      };
      const byButton = async () => {
        const read = page.waitForResponse((r) => new URL(r.url()).pathname === "/assistants/editor", { timeout: 10000 }).catch(() => null);
        await page.click("[data-editor-asst-status]");
        await read;
        await page.waitForSelector("[data-editor-sheet-body] [data-asst-pick]", { timeout: 10000 });
        await sleep(150);
      };
      const row = `[data-editor-sheet-body] [data-asst-pick="${alpha}"]`;
      assert(await page.getAttribute("[data-editor-asst-status]", "aria-label") === "Assistants" && await page.getAttribute("[data-editor-asst-status]", "title") === "Assistants (Ctrl+I)", "the sparkles do not name the assistant picker and its key");
      assert(await page.getAttribute("[data-editor-term-status]", "aria-label") === "Terminals", "the terminal icon names more than the terminals");
      await page.click("[data-editor-term-status]");
      await page.waitForSelector(`[data-editor-sheet-body] [data-term-pick="${shell}"]`, { timeout: 10000 });
      await sleep(150);
      const term = await menuOf(`[data-editor-sheet-body] [data-term-pick="${shell}"]`);
      assert(term.includes("Open terminal page") && !term.includes("Open assistant page"), `the shell row's menu reads ${term.join(", ")}`);
      await page.keyboard.press("Escape");
      await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 5000 });
      await byButton();
      assert(await page.textContent("[data-editor-sheet-title]") === "Assistants", "the sparkles opened another picker");
      const own = await menuOf(row);
      assert(own[0] === "Open" && own.includes("Open assistant page") && own.includes("Rename"), `the assistant row's menu reads ${own.join(", ")}`);
      assert(await shown(page, "[data-editor-sheet-panel]") && !(await page.$("[data-editor-sheet-body] .terminal-tab-menu")), "the row's menu took the picker away or a row carries dots");
      await L.openPicked(page, row);
      await ready(page, alpha);
      assert((await groups(page)).flat().includes(`*//assistant/${alpha}`), "the row's menu did not open the tab");
      assert(!(await shown(page, "[data-editor-sheet-panel]")), "the picker stayed open over the tab");
      await sleep(400);
      await byButton();
      const known = new Set(await page.evaluate(() => [...document.querySelectorAll("[data-editor-sheet-body] [data-asst-pick]")].map((r) => r.dataset.asstPick)));
      await page.locator("[data-editor-sheet-body] .dropdown-item:not([data-asst-pick])", { hasText: /^New assistant/ }).first().click();
      await page.waitForFunction((known) => [...document.querySelectorAll(".editor-tab.active")].some((t) => t.dataset.path.startsWith("//assistant/") && !known.includes(t.dataset.path.slice(12))), [...known], { timeout: 10000 });
      const fresh = (await groups(page)).flat().find((t) => t.startsWith("*//assistant/") && !known.has(t.slice(13)));
      if (fresh) made.push(fresh.slice(13));
      assert(fresh, "New assistant did not open as a tab");
    });

    await run("desktop: an assistant's tab menu and picker row offer Share and Delete as its list row does, Share opens New link with it picked, Delete asks the list's question and its tab leaves every open page", async () => {
      const one = await newAssistant(`del1-${tag.slice(-5)}`);
      const two = await newAssistant(`del2-${tag.slice(-5)}`);
      const labelsOf = (p) => p.$$eval(".dc-context-menu .dropdown-item", (els) => els.map((el) => (el.querySelector(".dc-menu-label-head") || el).textContent.trim()));
      const item = (p, label) => p.locator(".dc-context-menu .dropdown-item", { hasText: new RegExp(`^${label}$`) }).first();
      const asked = async (p, name) => {
        await p.waitForSelector(".swal2-title", { state: "visible", timeout: 8000 });
        const said = [(await p.textContent(".swal2-title")).trim(), (await p.textContent(".swal2-html-container")).trim()];
        assert(said[0] === `Delete "${name}"?` && said[1] === "Its thread goes with it. The coders it steers are released and yours again, the shared memory is kept.", `the Delete asks ${JSON.stringify(said)}`);
      };
      await pick(one);
      await page.click(tabSel(one), { button: "right" });
      await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
      const tabMenu = await labelsOf(page);
      const own = tabMenu.slice(tabMenu.indexOf("Open assistant page"));
      assert(JSON.stringify(own) === JSON.stringify(["Open assistant page", "Rename", "Share", "Delete"]), `the assistant tab's menu reads ${tabMenu.join(", ")}`);
      assert(await item(page, "Delete").evaluate((el) => el.classList.contains("text-danger")), "the tab's Delete is not marked as danger");
      await item(page, "Share").click();
      await page.waitForSelector("[data-form-modal].show [data-share-new]", { timeout: 10000 });
      const picked = await page.$$eval("[data-share-new] [data-picker-value]:checked", (els) => els.map((el) => `${el.name}=${el.value}`));
      const at = new URL(page.url());
      assert(picked.join(",") === `assistant=${one}` && at.pathname === "/settings/sharing" && !at.search, `Share opened ${page.url()} with ${picked}`);
      await page.click("[data-form-modal].show .btn-close");
      await openEditor();
      await page.waitForSelector(tabSel(one), { timeout: 15000 });
      await openPicker();
      await page.click(`[data-editor-sheet-body] [data-asst-pick="${two}"]`);
      await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
      const rowMenu = await labelsOf(page);
      assert(JSON.stringify(rowMenu) === JSON.stringify(["Open", "Open assistant page", "Rename", "Share", "Delete"]), `the assistant row's menu reads ${rowMenu.join(", ")}`);
      assert(await item(page, "Share").evaluate((el, id) => el.getAttribute("href") === `/settings/sharing?assistant=${id}`, two), "the row's Share leads elsewhere");
      await item(page, "Open").click();
      await ready(page, two);
      const ctx2 = await L.newDesktop(browser, engine);
      const p2 = await ctx2.newPage();
      L.wirePage(p2, bag);
      try {
        await L.login(p2);
        await openEditor(p2);
        await pick(one, p2);
        await pick(two, p2);
        await page.click(tabSel(one), { button: "right" });
        await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
        await item(page, "Delete").click();
        await asked(page, `del1-${tag.slice(-5)}`);
        await L.confirmSwal(page);
        await page.waitForSelector(tabSel(one), { state: "detached", timeout: 10000 });
        await p2.waitForSelector(tabSel(one), { state: "detached", timeout: 15000 });
        assert(!(await p2.$(`dc-assistant[assistant-id="${one}"]`)), "the deleted assistant stays on the other page");
        await sleep(400);
        await openPicker();
        await page.click(`[data-editor-sheet-body] [data-asst-pick="${two}"]`);
        await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
        await item(page, "Delete").click();
        await asked(page, `del2-${tag.slice(-5)}`);
        await L.confirmSwal(page);
        await page.waitForSelector(`[data-editor-sheet-body] [data-asst-pick="${two}"]`, { state: "detached", timeout: 10000 });
        await page.waitForSelector(tabSel(two), { state: "detached", timeout: 10000 });
        await p2.waitForSelector(tabSel(two), { state: "detached", timeout: 15000 });
        const errors = await Promise.all([page, p2].map((p) => p.evaluate(() => [...document.querySelectorAll("[data-dc-toasts] .toast")].filter((t) => t.querySelector(".text-danger")).map((t) => t.textContent.trim()))));
        assert(!errors.flat().length, `a delete toasted an error: ${errors.flat().join(" | ")}`);
      } finally {
        await ctx2.close();
        if (!(await page.evaluate(() => document.querySelector("[data-editor-sheet]").hidden))) await page.keyboard.press("Escape");
      }
    });

    await run("desktop: the shortcuts modal names the assistant picker's Ctrl+I and the assistant tab, /docs describes both", async () => {
      await page.evaluate(() => document.querySelector('[data-bs-target="#editor-shortcuts-modal"], [data-editor-shortcuts-item]')?.click());
      await page.waitForSelector("#editor-shortcuts-modal.show", { timeout: 6000 }).catch(() => {});
      assert(await page.evaluate(() => !!document.querySelector("#editor-shortcuts-modal [data-editor-shortcuts-assistant]")), "the shortcuts modal says nothing about assistant tabs");
      const rows = await page.evaluate(() => [...document.querySelectorAll("#editor-shortcuts-modal .dc-shortcuts-row")].map((r) => r.textContent.replace(/\s+/g, " ").trim()));
      assert(rows.includes("Open assistant Ctrl+I") && rows.includes("Open terminal Ctrl+J"), `the shortcuts read ${JSON.stringify(rows)}`);
      await page.goto(`${BASE}/docs`, { waitUntil: "domcontentloaded" });
      const text = (await page.textContent("body")).replace(/\s+/g, " ");
      assert(text.includes("Assistants as tabs") && text.includes("A shared link shows no assistant in the editor"), "/docs does not describe assistant tabs");
      assert(text.includes("Assistant picker") && !text.includes("then Enter flips"), "/docs does not describe the assistant picker or still names the flip");
      assert(text.includes("works like the terminal picker, with Share and Delete") && text.includes("Resume restarts a stopped coder, Delete removes it"), "/docs does not name the pickers' Share and Delete");
    });

    await run("phone: the statusbar's picker opens an assistant as a tab that a tap, typing and the send button talk to", async () => {
      const mctx = await browser.newContext({ ignoreHTTPSErrors: true, hasTouch: true, isMobile: engine === "chromium", viewport: { width: 390, height: 844 } });
      const mp = await mctx.newPage();
      L.wirePage(mp, bag);
      try {
        await L.login(mp);
        await openEditor(mp);
        if (await mp.evaluate(() => { const b = document.querySelector("[data-editor-backdrop]"); return !!b && b.getBoundingClientRect().width > 0 && getComputedStyle(b).display !== "none"; })) {
          await mp.tap("[data-editor-backdrop]", { position: { x: 370, y: 400 } });
          await sleep(400);
        }
        const icons = await mp.evaluate(() => [document.querySelector("[data-editor-term-status] i"), document.querySelector("[data-editor-asst-status] i")].map((el) => { const r = el.getBoundingClientRect(); return { l: Math.round(r.left), r: Math.round(r.right), cy: Math.round(r.top + r.height / 2), b: Math.round(r.bottom), w: Math.round(r.width) }; }));
        assert(icons.every((i) => i.w > 0 && i.b <= 844) && Math.abs(icons[0].cy - icons[1].cy) <= 2 && icons[1].l >= icons[0].r, `the phone's statusbar icons stand at ${JSON.stringify(icons)}`);
        assert(await plainSparkle(mp), "the phone's sparkles carry a mark");
        await mp.tap("[data-editor-asst-status]");
        await mp.waitForSelector(`[data-asst-pick="${alpha}"]`, { state: "visible", timeout: 8000 });
        assert(await mp.textContent("[data-editor-sheet-title]") === "Assistants", "the phone's sparkles opened another picker");
        const row = await rect(mp, `[data-asst-pick="${alpha}"]`);
        assert(row.h >= 44 && row.w >= 380, `the phone's row is ${row.w}x${row.h}`);
        await L.openPicked(mp, `[data-asst-pick="${alpha}"]`, { tap: true });
        await ready(mp, alpha);
        const box = await rect(mp, hostSel(alpha));
        const composer = await rect(mp, `${hostSel(alpha)} [data-assistant-form]`);
        assert(box.w === 390 && composer.b <= 844 && composer.y > 300, `the tab on the phone reads ${JSON.stringify({ box, composer })}`);
        assert(!(await focusIn(mp, alpha)), "the tab took the focus and raised the keyboard");
        assert(await mp.evaluate(() => document.documentElement.scrollWidth <= innerWidth), "the page scrolls sideways");
        await mp.tap(inputSel(alpha));
        await mp.keyboard.type("MAGIC from");
        await mp.keyboard.press("Enter");
        await mp.keyboard.type("the phone");
        assert(await mp.inputValue(inputSel(alpha)) === "MAGIC from\nthe phone", "Enter on the phone did not break the line");
        await mp.tap(`${hostSel(alpha)} [data-assistant-send]`);
        await waitAnswer(mp, alpha, "AIRPORT");
        await mp.tap(`${hostSel(alpha)} [data-assistant-watching-button]`);
        await mp.waitForFunction(() => document.querySelector("[data-editor-sheet-title]")?.textContent === "Watching", null, { timeout: 5000 });
        const sheet = await rect(mp, "[data-editor-sheet-panel]");
        assert(sheet.w === 390 && sheet.b <= 844, `the Watching sheet reads ${JSON.stringify(sheet)}`);
        await mp.tap("[data-editor-sheet-close]");
      } finally {
        await mctx.close();
      }
    });

    await run("phone: a sideways swipe on an assistant tab walks to the next and the previous tab under the editor's pill, a vertical drag scrolls the conversation, a selection and the message box keep theirs", async () => {
      const mctx = await browser.newContext({ ignoreHTTPSErrors: true, hasTouch: true, isMobile: engine === "chromium", viewport: { width: 390, height: 844 } });
      const mp = await mctx.newPage();
      L.wirePage(mp, bag);
      // Chromium takes real touches over CDP; WebKit has no such door, so it
      // gets touch events dispatched where the finger is, and its page does
      // not scroll under them.
      const cdp = engine === "chromium" ? await mctx.newCDPSession(mp) : null;
      const touch = async (phase, x, y) => {
        if (cdp) {
          await cdp.send("Input.dispatchTouchEvent", { type: { start: "touchStart", move: "touchMove", end: "touchEnd" }[phase], touchPoints: phase === "end" ? [] : [{ x, y, id: 1 }] });
          return;
        }
        await mp.evaluate(({ phase, x, y }) => {
          if (phase === "start") window.dcSwipeTarget = document.elementFromPoint(x, y);
          const target = window.dcSwipeTarget;
          const point = { identifier: 1, target, clientX: x, clientY: y, pageX: x, pageY: y, screenX: x, screenY: y };
          const event = new Event(`touch${phase}`, { bubbles: true, cancelable: true });
          Object.defineProperty(event, "touches", { value: phase === "end" ? [] : [point] });
          Object.defineProperty(event, "targetTouches", { value: phase === "end" ? [] : [point] });
          Object.defineProperty(event, "changedTouches", { value: [point] });
          target.dispatchEvent(event);
        }, { phase, x, y });
      };
      const drag = async (from, dx, dy) => {
        let pill = null;
        await touch("start", from.x, from.y);
        for (let i = 1; i <= 10; i += 1) {
          await touch("move", Math.round(from.x + (dx * i) / 10), Math.round(from.y + (dy * i) / 10));
          await sleep(16);
          if (i === 8) pill = await mp.evaluate(() => document.querySelector("[data-editor-swipe-pill]")?.textContent.trim() || "");
        }
        await touch("end", from.x + dx, from.y + dy);
        await sleep(700);
        return pill;
      };
      const front = () => mp.evaluate(() => document.querySelector(".editor-group-focused .editor-tab.active")?.dataset.path || "");
      const thread = async () => {
        const r = await rect(mp, `${hostSel(alpha)} [data-assistant-scroll]`);
        return { x: Math.round(r.x + r.w / 2), y: Math.round(r.y + r.h / 2), r };
      };
      const stays = async (why) => {
        assert(await front() === `//assistant/${alpha}`, `${why} left the assistant tab for ${await front()}`);
        assert(!(await mp.$(".dc-swipe-pill")), `${why} left a pill standing`);
      };
      try {
        await L.login(mp);
        await mp.goto(`${editorURL}?file=notes.txt`, { waitUntil: "domcontentloaded" });
        await mp.waitForSelector('.editor-tab.active[data-path="notes.txt"]', { timeout: 20000 });
        await mp.goto(`${editorURL}?terminal=${shell}`, { waitUntil: "domcontentloaded" });
        await mp.waitForSelector(`.editor-tab.active[data-path="//terminal/${shell}"]`, { timeout: 20000 });
        await sleep(600);
        if (await mp.evaluate(() => { const b = document.querySelector("[data-editor-backdrop]"); return !!b && b.getBoundingClientRect().width > 0 && getComputedStyle(b).display !== "none"; })) {
          await mp.tap("[data-editor-backdrop]", { position: { x: 370, y: 400 } });
          await sleep(400);
        }
        await mp.tap("[data-editor-asst-status]");
        await mp.waitForSelector(`[data-asst-pick="${alpha}"]`, { state: "visible", timeout: 8000 });
        await L.openPicked(mp, `[data-asst-pick="${alpha}"]`, { tap: true });
        await ready(mp, alpha);
        await sleep(600);
        const strip = await mp.evaluate(() => [...document.querySelectorAll(".editor-group-focused .editor-tab")].map((t) => ({ path: t.dataset.path, name: t.querySelector(".editor-tab-name")?.textContent.trim() || "" })));
        const at = strip.findIndex((t) => t.path === `//assistant/${alpha}`);
        assert(strip.length >= 3 && at >= 0, `the strip reads ${JSON.stringify(strip)}`);
        const next = strip[(at + 1) % strip.length];
        const prev = strip[(at - 1 + strip.length) % strip.length];
        let m = await thread();
        const left = await drag({ x: Math.round(m.r.x + m.r.w * 0.8), y: m.y }, -160, 4);
        assert(left && left.includes(next.name), `the editor's pill read "${left}" while the swipe went to ${next.name}`);
        assert(await front() === next.path, `a swipe to the left landed on ${await front()}, not on ${next.path}`);
        await mp.tap(tabSel(alpha));
        await ready(mp, alpha);
        await sleep(500);
        m = await thread();
        const right = await drag({ x: Math.round(m.r.x + m.r.w * 0.2), y: m.y }, 160, -4);
        assert(right && right.includes(prev.name), `the editor's pill read "${right}" while the swipe went to ${prev.name}`);
        assert(await front() === prev.path, `a swipe to the right landed on ${await front()}, not on ${prev.path}`);
        await mp.tap(tabSel(alpha));
        await ready(mp, alpha);
        await sleep(500);
        await mp.evaluate((s) => {
          const pad = document.createElement("div");
          pad.dataset.swipeProbe = "";
          pad.style.height = "3000px";
          document.querySelector(s).prepend(pad);
        }, `${hostSel(alpha)} [data-assistant-log]`);
        await sleep(300);
        const top0 = await mp.evaluate((s) => document.querySelector(s).scrollTop, `${hostSel(alpha)} [data-assistant-scroll]`);
        m = await thread();
        await drag({ x: m.x, y: m.y - 100 }, 20, 220);
        await stays("a vertical drag");
        const top1 = await mp.evaluate((s) => document.querySelector(s).scrollTop, `${hostSel(alpha)} [data-assistant-scroll]`);
        if (cdp) assert(top1 < top0 - 100, `a vertical drag scrolled the conversation from ${top0} to ${top1}`);
        await mp.evaluate(() => document.querySelectorAll("[data-swipe-probe]").forEach((n) => n.remove()));
        const bubble = mp.locator(`${hostSel(alpha)} [data-assistant-log] [data-assistant-text]`).last();
        await bubble.scrollIntoViewIfNeeded();
        assert(await bubble.evaluate((node) => {
          window.getSelection().selectAllChildren(node);
          return window.getSelection().toString().length > 0;
        }), "the message holds no text to select");
        const text = await bubble.boundingBox();
        await drag({ x: Math.round(text.x + text.width * 0.8), y: Math.round(text.y + Math.min(text.height, 40) / 2) }, -160, 0);
        await stays("a swipe over a selection");
        assert(await mp.evaluate(() => window.getSelection().toString().length > 0), "the swipe took the selection away");
        await mp.evaluate(() => window.getSelection().removeAllRanges());
        const input = await rect(mp, inputSel(alpha));
        await drag({ x: Math.round(input.x + input.w * 0.8), y: Math.round(input.y + input.h / 2) }, -160, 0);
        await stays("a swipe on the message box");
        await mp.tap(inputSel(alpha));
        await mp.keyboard.type("after the swipes");
        assert(await mp.inputValue(inputSel(alpha)) === "after the swipes", "the message box lost the typing after the swipes");
        await mp.fill(inputSel(alpha), "");
      } finally {
        await mctx.close();
      }
    });

    await run("desktop: two assistant tabs carry no id twice, each one's project view and @ list work in it alone, PageUp and PageDown follow only the focused one", async () => {
      const gamma = await newAssistant(`gamma-${tag.slice(-5)}`);
      const delta = await newAssistant(`delta-${tag.slice(-5)}`);
      const dir = await L.projectPath(page, project);
      const coders = [];
      const asideOf = (who) => `#assistant-${who}-aside`;
      try {
        for (const who of [gamma, delta]) {
          const started = await postAs(page, "/coders/new", { name: `eat-${who.slice(0, 4)}`, project: dir, coder: "claude", automatic_approval: "on", prompt: "job-eat" });
          assert(started.ok, `a new coder answered ${started.status}: ${started.text.slice(0, 160)}`);
          const id = JSON.parse(started.text).id;
          coders.push(id);
          const steered = await postAs(page, "/assistants/jobs", { form: "steer", terminal: id, assistant: who, done_when: "" });
          assert(steered.ok, `steering a coder answered ${steered.status}: ${steered.text.slice(0, 160)}`);
        }
        const typed = await page.evaluate(async ({ shell, dir }) => {
          const token = document.querySelector('meta[name="csrf-token"]')?.content || "";
          const line = `cd -- '${dir}' && git init -q && git -c user.name=e2e -c user.email=e2e@example.invalid commit -q --allow-empty -m init`;
          const res = await fetch(`/shells/${shell}/input`, { method: "POST", headers: { "X-CSRF-Token": token, "Content-Type": "application/json" }, body: JSON.stringify({ items: [{ text: line }, { control: "enter" }] }) });
          return res.status;
        }, { shell, dir });
        assert(typed < 300, `typing git init into the shell answered ${typed}`);
        for (let t = 0; ; t += 500) {
          const list = await page.evaluate(async (u) => (await fetch(u)).text(), `/assistants/projects?assistant=${encodeURIComponent(delta)}`);
          if (list.includes("data-assistant-project-changes")) break;
          assert(t < 20000, "the project never read as a repository on the assistants' cards");
          await sleep(500);
        }
        await page.goto(`${editorURL}?file=notes.txt`, { waitUntil: "domcontentloaded" });
        await page.waitForSelector('.editor-tab.active[data-path="notes.txt"]', { timeout: 20000 });
        await sleep(600);
        await pick(gamma);
        await page.click(tabSel(gamma), { button: "right" });
        await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
        await page.evaluate(() => [...document.querySelectorAll(".dc-context-menu .dropdown-item")].find((el) => el.textContent.trim() === "Split right").click());
        await sleep(800);
        await page.click('.editor-tab[data-path="notes.txt"]');
        await pick(delta);
        const layout = await groups(page);
        assert(layout.findIndex((g) => g.includes(`*//assistant/${gamma}`)) >= 0 && layout.findIndex((g) => g.includes(`*//assistant/${delta}`)) >= 0
          && layout.findIndex((g) => g.includes(`*//assistant/${gamma}`)) !== layout.findIndex((g) => g.includes(`*//assistant/${delta}`)), `the two do not stand side by side: ${JSON.stringify(layout)}`);
        const watching = async (who) => {
          await page.click(`${hostSel(who)} [data-assistant-watching-button]`);
          await page.waitForFunction(() => document.querySelector("[data-editor-sheet-title]")?.textContent === "Watching" && !document.querySelector("[data-editor-sheet]").hidden, null, { timeout: 5000 });
        };
        const openView = async (who) => {
          await watching(who);
          await page.click("[data-editor-sheet-body] [data-assistant-projects-tab]");
          const line = `[data-editor-sheet-body] [data-assistant-project="${project}"] [data-assistant-project-changes]`;
          await page.waitForSelector(line, { state: "visible", timeout: 15000 });
          await page.click(line);
          await page.waitForSelector(`[data-editor-sheet-body] [data-assistant-project-view-body="${project}"] [data-assistant-project-switch="reviews"]`, { state: "visible", timeout: 10000 });
        };
        const closeSheet = async () => {
          await page.keyboard.press("Escape");
          await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 5000 });
        };
        const paneOf = (who) => page.evaluate((a) => {
          const view = document.querySelector(`${a} [data-assistant-project-view-body]`);
          const vis = (el) => !!el && el.getBoundingClientRect().height > 0 && getComputedStyle(el).display !== "none";
          const r = view?.querySelector("[data-assistant-project-reviews]");
          const c = view?.querySelector("[data-assistant-project-changes-section]");
          return `${r?.classList.contains("active") ? "R" : "-"}${c?.classList.contains("active") ? "C" : "-"}${vis(r) ? " sees reviews" : vis(c) ? " sees changes" : ""}`;
        }, asideOf(who));
        const toSection = async (section) => {
          await page.click(`[data-editor-sheet-body] [data-assistant-project-switch="${section}"]`);
          await sleep(400);
        };
        // Every id inside either assistant, its box and its aside wherever that
        // stands, is the only one in the document, and every reference of a
        // fold, a switch or a list stays inside the assistant that holds it.
        const ownIDs = () => page.evaluate((who) => who.map(({ host, aside }) => {
          const roots = [document.querySelector(host), document.querySelector(aside)].filter(Boolean);
          const inside = (el) => !!el && roots.some((r) => r.contains(el));
          const ids = roots.flatMap((r) => [...r.querySelectorAll("[id]")].map((el) => el.id));
          const twice = ids.filter((id) => document.querySelectorAll(`[id="${CSS.escape(id)}"]`).length !== 1);
          const refs = roots.flatMap((r) => [...r.querySelectorAll("[data-bs-target^='#'], [aria-controls], [aria-labelledby], [aria-activedescendant]")])
            .flatMap((el) => [el.getAttribute("data-bs-target")?.slice(1), el.getAttribute("aria-controls"), el.getAttribute("aria-labelledby"), el.getAttribute("aria-activedescendant")].filter(Boolean))
            .filter((id) => !inside(document.getElementById(id)));
          return { ids: ids.length, twice, astray: refs };
        }), [gamma, delta].map((id) => ({ host: hostSel(id), aside: asideOf(id) })));
        await openView(gamma);
        await toSection("reviews");
        await closeSheet();
        await openView(delta);
        const both = await ownIDs();
        assert(both.every((x) => x.ids > 10 && !x.twice.length && !x.astray.length), `the two assistants share ids or point at each other: ${JSON.stringify(both)}`);
        await toSection("reviews");
        await toSection("changes");
        let read = { gamma: await paneOf(gamma), delta: await paneOf(delta) };
        assert(read.delta === "-C sees changes" && read.gamma === "R-", `delta's switch reached across: ${JSON.stringify(read)}`);
        await toSection("reviews");
        await closeSheet();
        await watching(gamma);
        await toSection("changes");
        read = { gamma: await paneOf(gamma), delta: await paneOf(delta) };
        assert(read.gamma === "-C sees changes" && read.delta === "R-", `gamma's switch reached across: ${JSON.stringify(read)}`);
        await closeSheet();
        for (const who of [gamma, delta]) {
          await page.click(inputSel(who));
          await page.keyboard.type("@");
          await page.waitForSelector(`${hostSel(who)} [data-mention-panel]:not([hidden]) [data-mention-list] [role="option"]`, { timeout: 10000 });
          const listed = await page.evaluate((s) => {
            const host = document.querySelector(s);
            const input = host.querySelector("[data-assistant-input]");
            const list = document.getElementById(input.getAttribute("aria-controls") || "");
            const marked = document.getElementById(input.getAttribute("aria-activedescendant") || "");
            return { own: !!list && host.contains(list), marked: !!marked && !!list?.contains(marked) };
          }, hostSel(who));
          const ids = await ownIDs();
          assert(listed.own && listed.marked && ids.every((x) => !x.twice.length && !x.astray.length), `the @ list of one tab is not its own: ${JSON.stringify({ listed, ids })}`);
          await page.keyboard.press("Escape");
          await page.waitForSelector(`${hostSel(who)} [data-mention-panel]`, { state: "hidden", timeout: 5000 });
          await page.keyboard.press("Backspace");
        }
        // The keys a reader scrolls with reach the follow of the tab in front
        // of the focused group alone, also with the focus on the page itself.
        const steered = () => page.evaluate((ids) => ids.map((id) => document.querySelector(`.editor-assistant-box dc-assistant[assistant-id="${id}"]`).follow.steeredAt), [gamma, delta]);
        for (const [key, who] of [["PageUp", delta], ["PageDown", gamma], ["PageUp", gamma], ["PageDown", delta]]) {
          await page.click(`${hostSel(who)} [data-assistant-head]`);
          await page.evaluate(() => document.activeElement?.blur());
          const active = await page.evaluate(() => [...document.querySelectorAll("dc-assistant[active]")].map((e) => e.getAttribute("assistant-id")));
          assert(JSON.stringify(active) === JSON.stringify([who]), `after a click on its head active reads ${JSON.stringify(active)}`);
          const before = await steered();
          await page.keyboard.press(key);
          const after = await steered();
          const moved = [gamma, delta].filter((id, i) => after[i] !== before[i]);
          assert(JSON.stringify(moved) === JSON.stringify([who]), `${key} on the page moved the follow of ${JSON.stringify(moved)}, not of ${who} alone`);
        }
      } finally {
        if (!(await page.evaluate(() => document.querySelector("[data-editor-sheet]").hidden))) await page.keyboard.press("Escape");
        for (const id of coders) {
          await postAs(page, "/assistants/jobs", { form: "release", terminal: id }).catch(() => {});
          await postAs(page, `/coders/${id}/delete`, {}).catch(() => {});
        }
      }
    });

    await run("guest: a project link's editor shows no assistant, and a hand built request reaches none", async () => {
      const link = await postAs(page, "/settings/sharing/new", [["amount", "1"], ["unit", "hours"], ["name", `eat-${tag.slice(-5)}`], ["project", project]].reduce((o, [k, v]) => Object.assign(o, { [k]: v }), {}));
      const url = (link.text.match(/value="([^"]*\/share\/[^"]+)"/) || [])[1];
      assert(link.ok && url, `making a link answered ${link.status}`);
      const gctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1360, height: 900 } });
      const gp = await gctx.newPage();
      L.wirePage(gp, bag);
      const reqs = [];
      gp.on("request", (r) => reqs.push(new URL(r.url()).pathname));
      try {
        const base = new URL(url).pathname;
        await gp.goto(`${BASE}${base}/projects/${project}/editor`, { waitUntil: "domcontentloaded" });
        await gp.waitForSelector(".cm-editor, .editor-textarea", { state: "attached", timeout: 20000 });
        await gp.evaluate(({ project, alpha }) => {
          for (const k of Object.keys(localStorage)) if (k.includes(`dc-editor-tabs:${project}`)) localStorage.setItem(k, JSON.stringify({ open: [{ type: "assistant", id: alpha }], active: `//assistant/${alpha}` }));
        }, { project, alpha });
        await gp.reload({ waitUntil: "domcontentloaded" });
        await gp.waitForSelector(".cm-editor, .editor-textarea", { state: "attached", timeout: 20000 });
        await sleep(1200);
        await gp.keyboard.press("Control+j");
        await gp.waitForFunction(() => !document.querySelector("[data-editor-sheet]").hidden, null, { timeout: 5000 });
        const rows = await gp.evaluate(() => document.querySelector("[data-editor-sheet-body]").textContent);
        assert(!/Assistants|New assistant/.test(rows), `the guest's picker offers assistants: ${rows.replace(/\s+/g, " ")}`);
        assert(await gp.textContent("[data-editor-sheet-title]") === "Terminals", "the guest's picker is not titled Terminals");
        assert(!(await gp.$("[data-assistant-entry], [data-assistant-new-coder], .editor-assistant-box, [data-asst-pick], .editor-tab[data-path^='//assistant/']")), "the guest's editor carries an assistant");
        assert(await gp.getAttribute("[data-editor-term-status]", "aria-label") === "Terminals", "the guest's statusbar button names assistants");
        assert(!(await gp.$("[data-editor-asst-status], [data-editor-asst-open-item]")), "the guest's editor carries the sparkles or Open assistant");
        await gp.keyboard.press("Escape");
        await gp.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 5000 });
        await gp.click(".cm-content").catch(() => {});
        for (const key of ["Control+i", "Meta+i"]) {
          await gp.keyboard.press(key);
          await sleep(500);
          assert(await gp.evaluate(() => document.querySelector("[data-editor-sheet]").hidden), `${key} opened a picker for the guest`);
        }
        assert(!reqs.some((p) => p.includes("/assistants")), `the guest's page asked for assistants: ${reqs.filter((p) => p.includes("/assistants")).join(", ")}`);
        for (const path of [`${base}/assistants/editor`, `${base}/assistants/${alpha}/tab`, `${base}/assistants/${alpha}`]) {
          const status = await gp.evaluate(async (u) => (await fetch(u)).status, path);
          assert(status === 404, `${path.replace(base, "<link>")} answered ${status}`);
        }
      } finally {
        await gctx.close();
      }
    });
  } finally {
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" }).catch(() => {});
    for (const id of made) await postAs(page, `/assistants/${id}`, { form: "delete" }).catch(() => {});
    if (shell) await postAs(page, `/shells/${shell}/delete`, {}).catch(() => {});
    if (coder) await postAs(page, `/coders/${coder}/delete`, {}).catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
  }
});
