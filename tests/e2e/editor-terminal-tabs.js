const fs = require("fs");
const path = require("path");
const L = require("./lib");
const { assert, sleep, confirmSwal, BASE } = L;

// Terminals as editor tabs: a running coder or shell of the project opens as a
// tab in the focused group, next to files, through the picker (the menu's Open
// terminal, a sheet with a filter, rows of GET /projects/:name/editor/terminals'
// [data-term-session] elements with their marks, New shell, New coder and, for
// the owner, the stopped coders), a click, a tap or Enter on a row opening its
// menu and its Open (Resume for a stopped coder) the tab. A coder stopped
// while the editor was closed comes back as its tab with Resume. A stopped
// coder's row and tab menus offer Delete, which asks what its chip on the
// projects page asks.
// The tab's path is //terminal/<id>, its saved entry {type: "terminal", id},
// its body the fragment's [data-terminal-view]: the stage mountTermIsland
// mounts the island into on first show, and the attach pages' footer
// (terminal_footer.gohtml) with Settings. A terminal stands in one tab at
// most, closing the tab leaves it running. Keys: from a terminal tab
// Ctrl+Tab, Ctrl+Shift+X and the editor's Ctrl+Shift lists stay the editor's,
// everything else is the terminal's. A coder stopped elsewhere shows Resume in
// its tab (owner), a terminal deleted elsewhere closes its tab live, a reload
// brings the tabs back and drops the gone. A file link in a terminal tab opens
// the file beside it. A guest of a share link watches: no New, no Resume, no
// Stop, Delete or Rename, no input without the input grant.
// Needs the instance editor-terminal.js runs on (a real coder CLI for the New
// coder round trip, the form preselects copilot on this host, which resumes
// right after its start).
// The coder's working ring is raised through the copilot notify inbox
// (NOTIFY_DIR, the instance's notification-inbox mounted, see
// notifications.js); without it that check is skipped (soft). The steered
// mark comes from an assistant steering the coder for a few seconds and
// releasing it again, so no signal and no heartbeat ever wakes that assistant.
// The picker repaints its rows when its own read lands, so a check reads its
// rows in one evaluation after that read.
// Gotchas: headless renders xterm on canvas, output is read from the
// .attach-selection mirror inside the tab's view; a shell gets its commands
// through the input route ({prompt}) where a check is not about typing.

const NOTIFY_DIR = process.env.NOTIFY_DIR || "";

function injectCopilot(targetId, hook) {
  const dir = path.join(NOTIFY_DIR, "copilot");
  fs.mkdirSync(dir, { recursive: true });
  const file = path.join(dir, `${Date.now()}-${Math.random().toString(36).slice(2)}`);
  fs.writeFileSync(`${file}.tmp`, JSON.stringify({ session_id: targetId, hook_event_name: hook }));
  fs.renameSync(`${file}.tmp`, `${file}.json`);
}

L.runFeature("EDITOR-TERMINAL-TABS", async ({ engine, browser, page, run, mobilePage, bag }) => {
  const tag = `ett-${engine}-${Date.now().toString(36)}`;
  const project = `zzett-${tag}`;
  const other = `zzetto-${tag}`;
  const editorURL = `${BASE}/projects/${project}/editor`;
  const ids = {};
  let coderId = "";
  let steerer = "";
  const stoppedCoders = [];
  const inputs = {};
  page.on("request", (r) => {
    const m = new URL(r.url()).pathname.match(/^\/(?:shells|coders)\/([^/]+)\/input$/);
    if (m && r.method() === "POST") (inputs[m[1]] ??= []).push(r.postData() || "");
  });

  const postAs = (p, path, body, { json = false } = {}) => p.evaluate(async ({ path, body, json }) => {
    const token = document.querySelector('meta[name="csrf-token"]')?.content || "";
    const res = await fetch(path, {
      method: "POST",
      headers: { "X-CSRF-Token": token, Accept: "application/json", ...(json ? { "Content-Type": "application/json" } : {}) },
      body: json ? JSON.stringify(body) : new URLSearchParams(body),
    });
    return { ok: res.ok, status: res.status, text: await res.text(), url: res.url };
  }, { path, body, json });
  const prompt = (p, id, command) => postAs(p, `/shells/${id}/input`, { items: [{ prompt: command }] }, { json: true });
  const shellOf = async (p, name) => {
    const url = await L.createShell(p, name);
    return new URL(url).pathname.split("/").pop();
  };
  const openEditor = async (p = page) => {
    await p.goto(editorURL, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(p);
    await p.waitForSelector(".cm-editor, .editor-textarea", { state: "attached", timeout: 20000 });
    await sleep(500);
  };
  const tabSel = (id) => `.editor-tab[data-path="//terminal/${id}"]`;
  const boxSel = (id) => `.editor-term-box:has([data-terminal-view="${id}"])`;
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
  const mirror = (p, id) => p.evaluate((s) => document.querySelector(`${s} .attach-selection`)?.textContent || "", boxSel(id));
  const waitText = async (p, id, needle, ms = 12000) => {
    for (let t = 0; t < ms; t += 250) {
      if ((await mirror(p, id)).includes(needle)) return;
      await sleep(250);
    }
    const focus = await p.evaluate(() => { const e = document.activeElement; return e ? `${e.tagName}#${e.id} in ${e.closest("[data-terminal-view]")?.dataset.terminalView || "-"}` : "none"; });
    throw new Error(`the tab of ${id} never showed ${needle} (focus ${focus}, sent ${JSON.stringify((inputs[id] || []).slice(-4))}): ${JSON.stringify((await mirror(p, id)).slice(-300))}`);
  };
  const mounted = (p, id) => p.waitForSelector(`${boxSel(id)} terminal-attach[embedded] .xterm-screen canvas`, { timeout: 20000 });
  const activeTabPath = (p) => p.evaluate(() => {
    const g = document.querySelector(".editor-group-focused") || document;
    return g.querySelector(".editor-tab.active")?.dataset.path || "";
  });
  const openPicker = async (p = page) => {
    await p.click("[data-editor-menu]");
    await p.waitForSelector("[data-editor-menu-list].show", { timeout: 5000 });
    const read = p.waitForResponse((r) => new URL(r.url()).pathname.endsWith("/editor/terminals"), { timeout: 10000 }).catch(() => null);
    await p.click("[data-editor-term-open-item]");
    await read;
    await sleep(150);
    await p.waitForFunction(() => {
      const sheet = document.querySelector("[data-editor-sheet]");
      return sheet && !sheet.hidden && document.querySelector("[data-editor-sheet-title]").textContent.startsWith("Terminals")
        && !!document.querySelector("[data-editor-sheet-body] [data-term-pick]");
    }, null, { timeout: 10000 });
  };
  const pickRows = (p) => p.evaluate(() => [...document.querySelectorAll("[data-editor-sheet-body] .dropdown-item")].filter((el) => {
    const r = el.getBoundingClientRect();
    return r.height > 0 && !!el.offsetParent;
  }).map((el) => ({
    id: el.dataset.termPick || "",
    text: el.textContent.replace(/\s+/g, " ").trim(),
    icon: el.querySelector(".dc-term-icon")?.className || "",
    h: Math.round(el.getBoundingClientRect().height),
  })));
  const focusedIn = (p, id) => p.evaluate((s) => !!document.activeElement && !!document.querySelector(s)?.contains(document.activeElement), boxSel(id));
  const clickTerminal = async (p, id) => {
    const r = await rect(p, `${boxSel(id)} terminal-attach`);
    await p.mouse.click(r.x + r.w / 2, r.y + r.h / 2);
    await sleep(250);
  };
  const tabMenu = async (p, sel) => {
    await p.click(sel, { button: "right" });
    await p.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
    return p.$$eval(".dc-context-menu .dropdown-item", (els) => els.map((el) => (el.querySelector(".dc-menu-label-head") || el).textContent.trim()).filter(Boolean));
  };
  const menuClick = (p, label) => p.evaluate((l) => {
    const item = [...document.querySelectorAll(".dc-context-menu .dropdown-item")].find((el) => (el.querySelector(".dc-menu-label-head") || el).textContent.trim() === l);
    if (!item) throw new Error(`no menu entry ${l}`);
    item.click();
  }, label);
  const closeMenu = async (p) => {
    await p.keyboard.press("Escape");
    await p.waitForSelector(".dc-context-menu", { state: "detached", timeout: 4000 }).catch(() => {});
  };
  const closeFilesModal = async (id) => {
    const modal = `#coder-files-modal-${id}`;
    if (await page.evaluate((m) => document.querySelector(m)?.classList.contains("show"), modal)) {
      await sleep(400);
      await page.click(`${modal} .btn-close`);
    }
    await page.waitForFunction(() => !document.querySelector(".modal.show, .modal-backdrop"), null, { timeout: 8000 });
  };
  const savedTabs = (p) => p.evaluate((k) => JSON.parse(localStorage.getItem(k) || "null"), `dc-editor-tabs:${project}`);

  try {
    await L.createProject(page, project);
    await L.createProject(page, other);
    ids.alpha = await shellOf(page, project);
    ids.beta = await shellOf(page, project);
    ids.foreign = await shellOf(page, other);
    await openEditor();
    assert((await postAs(page, `/shells/${ids.alpha}/rename`, { name: `alpha-${tag.slice(-5)}` })).ok, "renaming alpha failed");
    assert((await postAs(page, `/shells/${ids.beta}/rename`, { name: `beta-${tag.slice(-5)}` })).ok, "renaming beta failed");
    await prompt(page, ids.alpha, "printf 'one\\ntwo\\n' > notes.txt");
    await sleep(800);

    await run("desktop: Open terminal lists this project's terminals with icon, name and kind, and the owner's New shell and New coder", async () => {
      await openEditor();
      await openPicker();
      const rows = await pickRows(page);
      const alpha = rows.find((r) => r.id === ids.alpha);
      const beta = rows.find((r) => r.id === ids.beta);
      assert(alpha && beta, `the picker misses a shell: ${JSON.stringify(rows)}`);
      assert(alpha.text.includes(`alpha-${tag.slice(-5)}`) && alpha.text.includes("Shell"), `alpha's row reads ${alpha.text}`);
      assert(/dc-term-icon/.test(alpha.icon) && /running/.test(alpha.icon), `alpha's row wears no running icon: ${alpha.icon}`);
      assert(!rows.some((r) => r.id === ids.foreign), "the picker lists another project's shell");
      assert(rows.some((r) => r.text === "New shell") && rows.some((r) => r.text === "New coder"), `no New shell or New coder: ${rows.map((r) => r.text).join(" | ")}`);
      const sheet = await rect(page, "[data-editor-sheet-panel]");
      const filterFocused = await page.evaluate(() => document.activeElement === document.querySelector("[data-editor-sheet-filter]"));
      assert(filterFocused, "the picker's filter does not take the keys on a fine pointer");
      return `${rows.length} rows, sheet ${sheet.w}x${sheet.h}`;
    });

    await run("desktop: typing filters the picker, Enter opens the marked row's menu with Open marked, Enter again opens the terminal as a tab in the focused group, focused", async () => {
      await page.keyboard.type(`beta-${tag.slice(-5)}`);
      await sleep(250);
      const rows = await pickRows(page);
      assert(rows.length === 1 && rows[0].id === ids.beta, `the filter leaves ${JSON.stringify(rows.map((r) => r.text))}`);
      await page.keyboard.press("Enter");
      await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
      assert(await page.evaluate(() => {
        const first = document.querySelector(".dc-context-menu .dropdown-item");
        return (first.querySelector(".dc-menu-label-head") || first).textContent.trim() === "Open" && (document.activeElement === first || first.classList.contains("active"));
      }), "the row's menu does not open with Open marked");
      assert(await shown(page, "[data-editor-sheet-panel]"), "the row's menu took the picker away");
      await page.keyboard.press("Enter");
      await page.waitForSelector(`${tabSel(ids.beta)}.active`, { timeout: 8000 });
      await mounted(page, ids.beta);
      assert(await shown(page, `${boxSel(ids.beta)} terminal-attach`), "the terminal tab's terminal is not visible");
      assert(!(await shown(page, "[data-editor-sheet-panel]")), "the picker stayed open");
      const tab = await page.evaluate((s) => {
        const el = document.querySelector(s);
        return { name: el.querySelector(".editor-tab-name")?.textContent, icon: !!el.querySelector(".dc-term-icon.running"), title: el.title };
      }, tabSel(ids.beta));
      assert(tab.name === `beta-${tag.slice(-5)}` && tab.icon, `the tab reads ${JSON.stringify(tab)}`);
      let focused = false;
      for (let i = 0; i < 20 && !focused; i++) { focused = await focusedIn(page, ids.beta); if (!focused) await sleep(200); }
      assert(focused, "the opened terminal does not have the focus");
    });

    await run("desktop: a terminal tab streams and takes typing, its footer carries copy, refresh, settings and help", async () => {
      await page.keyboard.type("echo typed-in-tab-$((6*7))");
      await page.keyboard.press("Enter");
      await waitText(page, ids.beta, "typed-in-tab-42");
      for (const sel of ["[data-terminal-copy]", "[data-terminal-refresh]", "[data-terminal-settings]", '[data-bs-target="#editor-shortcuts-modal"]']) {
        assert(await shown(page, `${boxSel(ids.beta)} .attach-desktop ${sel}`), `the footer misses ${sel}`);
      }
      const box = await rect(page, boxSel(ids.beta));
      const term = await rect(page, `${boxSel(ids.beta)} .xterm-screen`);
      assert(term.x >= box.x && term.r <= box.r + 1 && term.b <= box.b + 1, `the terminal runs out of its box: ${JSON.stringify({ box, term })}`);
    });

    await run("desktop: a paste into a focused terminal tab goes to its terminal as one paste", async () => {
      await clickTerminal(page, ids.beta);
      const before = (inputs[ids.beta] || []).length;
      await page.evaluate(() => {
        const dt = new DataTransfer();
        dt.setData("text/plain", "echo pasted-in-tab-$((5*5))");
        document.activeElement.dispatchEvent(new ClipboardEvent("paste", { clipboardData: dt, bubbles: true, cancelable: true }));
      });
      await sleep(400);
      const sent = (inputs[ids.beta] || []).slice(before).join(" ");
      assert(sent.includes('"paste"'), `the paste did not go out as one: ${sent}`);
      await page.keyboard.press("Enter");
      await waitText(page, ids.beta, "pasted-in-tab-25");
    });

    await run("desktop: the footer's help opens the editor's shortcuts, which name what a terminal tab keeps", async () => {
      await page.click(`${boxSel(ids.beta)} .attach-desktop [data-bs-target="#editor-shortcuts-modal"]`);
      await page.waitForSelector("#editor-shortcuts-modal.show", { timeout: 6000 });
      assert(await shown(page, "#editor-shortcuts-modal [data-editor-shortcuts-terminal]"), "the shortcuts modal says nothing about terminal tabs");
      await sleep(600);
      await page.click("#editor-shortcuts-modal .btn-close");
      await page.waitForFunction(() => !document.querySelector(".modal.show, .modal-backdrop"), null, { timeout: 6000 });
    });

    await run("desktop: the footer's settings open the terminal settings and a font size reaches the tab's terminal", async () => {
      const font = () => page.evaluate((s) => document.querySelector(`${s} .attach-selection`)?.style.fontSize || "", boxSel(ids.beta));
      await page.click(`${boxSel(ids.beta)} .attach-desktop [data-terminal-settings]`);
      await page.waitForFunction(() => document.querySelector("[data-editor-sheet-title]")?.textContent === "Terminal settings", null, { timeout: 5000 });
      const select = '[data-editor-sheet-body] terminal-setting-select[setting="font-size"] select';
      assert(await shown(page, select), "the sheet shows no font size");
      const was = await page.$eval(select, (s) => s.value);
      const want = was === "20" ? "12" : "20";
      await page.selectOption(select, want);
      await page.click("[data-editor-sheet-close]");
      await prompt(page, ids.beta, "echo font");
      let now = "";
      for (let i = 0; i < 20 && now !== `${want}px`; i++) { await sleep(250); now = await font(); }
      await page.click(`${boxSel(ids.beta)} .attach-desktop [data-terminal-settings]`);
      await page.waitForSelector(select, { state: "visible", timeout: 5000 });
      await page.selectOption(select, was);
      await page.click("[data-editor-sheet-close]");
      assert(now === `${want}px`, `the terminal's font stands at ${now}, asked ${want}px`);
    });

    await run("desktop: picking an open terminal goes to its tab, a terminal stands in one tab at most", async () => {
      await page.goto(`${editorURL}?file=notes.txt`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector('.editor-tab.active[data-path="notes.txt"]', { timeout: 15000 });
      await openPicker();
      await L.openPicked(page, `[data-editor-sheet-body] [data-term-pick="${ids.beta}"]`);
      await page.waitForSelector(`${tabSel(ids.beta)}.active`, { timeout: 8000 });
      assert((await page.locator(tabSel(ids.beta)).count()) === 1, "the terminal got a second tab");
      await mounted(page, ids.beta);
    });

    await run("desktop: on a fine pointer the picker opens with its first row, New shell, selected, the arrows move the mark and Enter on a terminal opens its menu, whose Open opens it", async () => {
      await openPicker();
      await page.mouse.move(1, 1);
      const state = () => page.evaluate(() => {
        const rows = [...document.querySelectorAll("[data-editor-sheet-body] .dropdown-item")].filter((el) => el.offsetParent && !el.disabled);
        return rows.map((el) => ({
          id: el.dataset.termPick || el.textContent.trim(),
          marked: el.classList.contains("selected") || document.activeElement === el,
          painted: getComputedStyle(el).backgroundColor !== "rgba(0, 0, 0, 0)",
        }));
      });
      let rows = await state();
      const k = rows.findIndex((r) => r.id === ids.alpha || r.id === ids.beta);
      assert(rows[0].id === "New shell" && rows[1].id === "New coder" && k > 1, `the New actions do not stand first: ${JSON.stringify(rows)}`);
      assert(rows.filter((r) => r.marked).length === 1 && rows[0].marked && rows[0].painted, `the picker does not open with New shell selected: ${JSON.stringify(rows)}`);
      await page.keyboard.press("ArrowDown");
      await sleep(150);
      rows = await state();
      assert(rows.findIndex((r) => r.marked) === 1 && rows[1].painted && !rows[0].marked && !rows[0].painted, `ArrowDown left the mark at ${JSON.stringify(rows)}`);
      for (let i = 1; i < k; i++) {
        await page.keyboard.press("ArrowDown");
        await sleep(120);
      }
      rows = await state();
      assert(rows.findIndex((r) => r.marked) === k, `the arrows did not reach the first terminal: ${JSON.stringify(rows)}`);
      await page.keyboard.press("ArrowUp");
      await sleep(150);
      assert((await state()).findIndex((r) => r.marked) === k - 1, `ArrowUp left the mark at ${JSON.stringify(await state())}`);
      await page.keyboard.press("ArrowDown");
      await page.keyboard.press("ArrowDown");
      await sleep(150);
      rows = await state();
      assert(rows.findIndex((r) => r.marked) === k + 1, `the second terminal is not marked: ${JSON.stringify(rows)}`);
      const want = rows[k + 1].id;
      assert(want === ids.alpha || want === ids.beta, `the row below the first terminal is no terminal: ${want}`);
      await page.keyboard.press("Enter");
      await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
      await page.keyboard.press("Enter");
      await page.waitForSelector(`${tabSel(want)}.active`, { timeout: 8000 });
      await mounted(page, want);
      await page.click(tabSel(ids.beta));
      await mounted(page, ids.beta);
    });

    await run("desktop: a tab behind another gets no keys", async () => {
      await clickTerminal(page, ids.beta);
      await page.click('.editor-tab[data-path="notes.txt"]');
      await page.waitForSelector('.editor-tab.active[data-path="notes.txt"]', { timeout: 6000 });
      const before = (inputs[ids.beta] || []).length;
      assert(!(await focusedIn(page, ids.beta)), "the hidden terminal kept the focus");
      assert(!(await shown(page, `${boxSel(ids.beta)} terminal-attach`)), "the hidden terminal still shows");
      await page.keyboard.press("ArrowDown");
      await page.keyboard.press("ArrowUp");
      await sleep(600);
      assert((inputs[ids.beta] || []).length === before, `keys reached the hidden terminal: ${(inputs[ids.beta] || []).slice(before).join(" ")}`);
    });

    await run("desktop: two terminal tabs side by side fit their boxes, stream live and take keys only when focused", async () => {
      await openPicker();
      await L.openPicked(page, `[data-editor-sheet-body] [data-term-pick="${ids.alpha}"]`);
      await page.waitForSelector(`${tabSel(ids.alpha)}.active`, { timeout: 8000 });
      await mounted(page, ids.alpha);
      await tabMenu(page, tabSel(ids.alpha));
      await menuClick(page, "Split right");
      await page.waitForFunction(() => document.querySelectorAll("[data-editor-group]").length === 2, null, { timeout: 6000 });
      await page.click(tabSel(ids.beta));
      await mounted(page, ids.beta);
      await sleep(800);
      const a = await rect(page, `${boxSel(ids.alpha)} terminal-attach`);
      const b = await rect(page, `${boxSel(ids.beta)} terminal-attach`);
      assert(a && b && a.w > 100 && b.w > 100 && (a.x >= b.r - 1 || b.x >= a.r - 1), `the two terminals do not stand side by side: ${JSON.stringify({ a, b })}`);
      for (const id of [ids.alpha, ids.beta]) {
        const box = await rect(page, boxSel(id));
        const screen = await rect(page, `${boxSel(id)} .xterm-screen`);
        assert(screen.x >= box.x - 1 && screen.r <= box.r + 1 && screen.b <= box.b + 1, `${id} runs out of its box: ${JSON.stringify({ box, screen })}`);
      }
      await clickTerminal(page, ids.alpha);
      await page.keyboard.type("echo only-alpha-$((2+3))");
      await page.keyboard.press("Enter");
      await waitText(page, ids.alpha, "only-alpha-5");
      await clickTerminal(page, ids.beta);
      await page.keyboard.type("echo only-beta-$((3+4))");
      await page.keyboard.press("Enter");
      await waitText(page, ids.beta, "only-beta-7");
      assert(!(await mirror(page, ids.beta)).includes("only-alpha"), "alpha's keys reached beta");
      assert(!(await mirror(page, ids.alpha)).includes("only-beta"), "beta's keys reached alpha");
      await prompt(page, ids.alpha, "echo live-$((40+1))");
      await waitText(page, ids.alpha, "live-41");
    });

    await run("desktop: dragging the line between the groups and the window resize the terminals", async () => {
      const before = await rect(page, `${boxSel(ids.alpha)} .xterm-screen`);
      const handle = await rect(page, ".editor-split-handle");
      await page.mouse.move(handle.x + handle.w / 2, handle.y + handle.h / 3);
      await page.mouse.down();
      await page.mouse.move(handle.x + handle.w / 2 - 80, handle.y + handle.h / 3, { steps: 6 });
      await page.mouse.move(handle.x + handle.w / 2 - 160, handle.y + handle.h / 3, { steps: 6 });
      await page.mouse.up();
      await sleep(1200);
      const after = await rect(page, `${boxSel(ids.alpha)} .xterm-screen`);
      assert(after.w > before.w + 80, `alpha's terminal did not grow with its group: ${before.w} -> ${after.w}`);
      await page.setViewportSize({ width: 1100, height: 760 });
      await sleep(1200);
      const narrow = await rect(page, `${boxSel(ids.alpha)} .xterm-screen`);
      const box = await rect(page, boxSel(ids.alpha));
      await page.setViewportSize({ width: 1360, height: 900 });
      await sleep(800);
      assert(narrow.w < after.w && narrow.r <= box.r + 1 && narrow.b <= box.b + 1, `the window resize left the terminal out of its box: ${JSON.stringify({ after, narrow, box })}`);
    });

    await run("desktop: Ctrl+Tab steps from a terminal to the next tab and back, Ctrl+K stays the terminal's", async () => {
      await clickTerminal(page, ids.alpha);
      const before = (inputs[ids.alpha] || []).length;
      await page.keyboard.press("Control+k");
      await sleep(500);
      const sent = (inputs[ids.alpha] || []).slice(before).join(" ");
      assert(sent.includes("\\u000b"), `Ctrl+K did not reach the terminal: ${sent}`);
      assert(!(await shown(page, "[data-editor-commit]")), "Ctrl+K opened the commit view from the terminal");
      await page.keyboard.press("Control+Tab");
      await sleep(400);
      const next = await activeTabPath(page);
      assert(next !== `//terminal/${ids.alpha}`, "Ctrl+Tab stayed on the terminal tab");
      await page.keyboard.press("Control+Shift+Tab");
      await page.waitForFunction((p) => (document.querySelector(".editor-group-focused .editor-tab.active") || {}).dataset?.path === p, `//terminal/${ids.alpha}`, { timeout: 5000 });
      let back = false;
      for (let i = 0; i < 15 && !back; i++) { back = await focusedIn(page, ids.alpha); if (!back) await sleep(200); }
      assert(back, "Ctrl+Shift+Tab back did not hand the terminal the keys");
    });

    await run("desktop: the tab menu carries the terminal's actions, and a rename follows live in the tab", async () => {
      const labels = await tabMenu(page, tabSel(ids.alpha));
      for (const want of ["Close", "Close others", "Open terminal page", "Rename", "Open project", "Delete"]) {
        assert(labels.includes(want), `the terminal tab's menu misses ${want}: ${labels.join(", ")}`);
      }
      assert(!labels.includes("Copy file") && !labels.includes("Download"), `the terminal tab offers file actions: ${labels.join(", ")}`);
      await menuClick(page, "Rename");
      await page.waitForSelector(".swal2-input", { state: "visible", timeout: 5000 });
      await page.fill(".swal2-input", `renamed-${tag.slice(-5)}`);
      await confirmSwal(page);
      await page.waitForFunction(([s, n]) => document.querySelector(`${s} .editor-tab-name`)?.textContent === n, [tabSel(ids.alpha), `renamed-${tag.slice(-5)}`], { timeout: 10000 });
    });

    await run("desktop: Ctrl+Shift+X from a terminal closes its tab, the terminal keeps running", async () => {
      await clickTerminal(page, ids.alpha);
      await page.keyboard.press("Control+Shift+X");
      await page.waitForSelector(tabSel(ids.alpha), { state: "detached", timeout: 6000 });
      assert(!(await page.$(boxSel(ids.alpha))), "the closed tab left its view behind");
      await openPicker();
      const rows = await pickRows(page);
      assert(rows.some((r) => r.id === ids.alpha && !r.text.includes("open")), `the closed terminal is not offered as running: ${JSON.stringify(rows)}`);
      await page.keyboard.press("Escape");
    });

    await run("desktop: a picker row's menu, opened by a click, opens its terminal as a tab and the row reads open, closing the tab leaves the terminal running", async () => {
      const rowSel = (id) => `[data-editor-sheet-body] [data-term-pick="${id}"]`;
      await openPicker();
      let rows = await pickRows(page);
      assert(/ open$/.test(rows.find((r) => r.id === ids.beta)?.text || ""), `the row of the terminal that is a tab does not read open: ${JSON.stringify(rows)}`);
      await page.click(rowSel(ids.alpha));
      await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
      await menuClick(page, "Open");
      await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 5000 });
      await page.waitForSelector(`${tabSel(ids.alpha)}.active`, { timeout: 8000 });
      await mounted(page, ids.alpha);
      assert((await page.locator(`terminal-attach[terminal-id="${ids.alpha}"]`).count()) === 1, "the terminal stands twice on the page");
      await sleep(400);
      await openPicker();
      rows = await pickRows(page);
      assert(/ open$/.test(rows.find((r) => r.id === ids.alpha)?.text || ""), `the row of the tab in front does not read open: ${JSON.stringify(rows)}`);
      await page.keyboard.press("Escape");
      await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 5000 });
      await page.click(`${tabSel(ids.alpha)} .editor-tab-state`);
      await page.waitForSelector(tabSel(ids.alpha), { state: "detached", timeout: 6000 });
      assert(!(await page.$(`terminal-attach[terminal-id="${ids.alpha}"]`)), "the closed tab left its terminal mounted");
      await openPicker();
      rows = await pickRows(page);
      assert(rows.some((r) => r.id === ids.alpha && !/ open$/.test(r.text)), `closing the tab took the terminal out of the picker or kept it open: ${JSON.stringify(rows)}`);
      await L.openPicked(page, rowSel(ids.alpha));
      await page.waitForSelector(`${tabSel(ids.alpha)}.active`, { timeout: 8000 });
    });

    await run("desktop: the open files sheet lists a terminal tab with its icon and kind", async () => {
      await page.click("[data-editor-menu]");
      await page.click("[data-editor-files-item]");
      await page.waitForSelector(`.editor-sheet-row[data-path="//terminal/${ids.alpha}"]`, { timeout: 5000 });
      const row = await page.$eval(`.editor-sheet-row[data-path="//terminal/${ids.alpha}"]`, (el) => ({ icon: !!el.querySelector(".dc-term-icon"), dir: el.querySelector(".editor-sheet-dir").textContent }));
      assert(row.icon && row.dir === "Shell", `the sheet row reads ${JSON.stringify(row)}`);
      await page.keyboard.press("Escape");
    });

    await run("desktop: a terminal tab drags onto another group's strip like a file", async () => {
      await tabMenu(page, tabSel(ids.alpha));
      await menuClick(page, "Split right");
      await page.waitForFunction(() => document.querySelectorAll("[data-editor-group]").length === 2, null, { timeout: 6000 });
      const groupOf = (id) => page.evaluate((s) => [...document.querySelectorAll("[data-editor-group]")].findIndex((g) => g.querySelector(s)), tabSel(id));
      const from = await groupOf(ids.beta);
      const target = await page.evaluate((i) => {
        const strip = document.querySelectorAll("[data-editor-group]")[i].querySelector("[data-editor-tabs]").getBoundingClientRect();
        return { x: strip.right - 20, y: strip.top + strip.height / 2 };
      }, await groupOf(ids.alpha));
      const t = await rect(page, tabSel(ids.beta));
      await page.mouse.move(t.x + t.w / 2, t.y + t.h / 2);
      await page.mouse.down();
      await page.mouse.move(t.x + t.w / 2 + 8, t.y + t.h / 2 + 8, { steps: 3 });
      await page.mouse.move(target.x, target.y, { steps: 16 });
      await sleep(200);
      await page.mouse.up();
      await sleep(800);
      assert((await groupOf(ids.beta)) !== from && (await groupOf(ids.beta)) === (await groupOf(ids.alpha)), "the terminal tab did not move to the other group");
      await mounted(page, ids.beta);
      await prompt(page, ids.beta, "echo moved-$((50+5))");
      await waitText(page, ids.beta, "moved-55");
    });

    await run("desktop: a terminal tab in copy mode keeps its copy view, its scroll and the green button through a split and a move to another group, and leaves copy mode afterwards", async () => {
      const id = await shellOf(page, project);
      const copyBtn = `[data-terminal-footer="${id}"] [data-terminal-copy]:visible`;
      const copyState = () => page.evaluate((id) => {
        const copy = document.querySelector(`terminal-copy[terminal-id="${id}"]`);
        const scroller = copy?.querySelector("[data-copy-scroll]");
        const attach = document.querySelector(`terminal-attach[terminal-id="${id}"]`);
        const r = (scroller && !copy.hidden ? scroller : attach).getBoundingClientRect();
        const top = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
        const button = [...document.querySelectorAll(`[data-terminal-footer="${id}"] [data-terminal-copy]`)].find((b) => b.offsetParent);
        return {
          open: !!copy && !copy.hidden && getComputedStyle(copy).display !== "none" && r.width > 0,
          behind: attach.classList.contains("attach-terminal-behind"),
          onTop: top?.closest("terminal-copy") ? "copy" : top?.closest("terminal-attach") ? "terminal" : top?.tagName || "",
          green: button.classList.contains("active") && button.getAttribute("aria-pressed") === "true",
          scroll: scroller ? Math.round(scroller.scrollTop) : -1,
          text: !!copy?.querySelector("[data-copy-text]")?.textContent.includes("150"),
        };
      }, id);
      const agree = (st, at) => st.open && st.behind && st.onTop === "copy" && st.green && st.text && Math.abs(st.scroll - at) <= 2;
      const groupOf = () => page.evaluate((s) => [...document.querySelectorAll("[data-editor-group]")].findIndex((g) => g.querySelector(s)), tabSel(id));
      let at = 0;
      let home = -1;
      try {
        await openEditor();
        await openPicker();
        await L.openPicked(page, `[data-editor-sheet-body] [data-term-pick="${id}"]`);
        await page.waitForSelector(`${tabSel(id)}.active`, { timeout: 8000 });
        await mounted(page, id);
        await prompt(page, id, "seq 1 300");
        await waitText(page, id, "299");
        await page.click(copyBtn);
        await page.waitForFunction((id) => {
          const scroller = document.querySelector(`terminal-copy[terminal-id="${id}"] [data-copy-scroll]`);
          return scroller?.querySelector("[data-copy-text]")?.textContent.includes("150") && scroller.scrollHeight - scroller.clientHeight - scroller.scrollTop <= 2;
        }, id, { timeout: 10000 });
        await sleep(500);
        at = await page.evaluate((id) => {
          const scroller = document.querySelector(`terminal-copy[terminal-id="${id}"] [data-copy-scroll]`);
          scroller.scrollTop = Math.round(scroller.scrollHeight / 3);
          return Math.round(scroller.scrollTop);
        }, id);
        await sleep(200);
        let st = await copyState();
        assert(agree(st, at) && at > 0, `copy mode before any move reads ${JSON.stringify(st)} at ${at}`);
        home = await groupOf();
        const groupsBefore = await page.evaluate(() => document.querySelectorAll("[data-editor-group]").length);
        await tabMenu(page, tabSel(id));
        await menuClick(page, "Split right");
        await page.waitForFunction((n) => document.querySelectorAll("[data-editor-group]").length === n + 1, groupsBefore, { timeout: 6000 });
        await sleep(400);
        st = await copyState();
        assert(agree(st, at), `after a split the copy view, its scroll and its button disagree: ${JSON.stringify(st)}, scrolled to ${at}`);
        await sleep(400);
        await tabMenu(page, tabSel(id));
        await menuClick(page, `Move to group ${home + 1}`);
        await page.waitForFunction((n) => document.querySelectorAll("[data-editor-group]").length === n, groupsBefore, { timeout: 6000 });
        await sleep(600);
        assert((await groupOf()) === home, "the tab did not move back to the group it came from");
        st = await copyState();
        assert(agree(st, at), `after the move to another group the copy view, its scroll and its button disagree: ${JSON.stringify(st)}, scrolled to ${at}`);
        await page.click(copyBtn);
        await page.waitForFunction((id) => document.querySelector(`terminal-copy[terminal-id="${id}"]`)?.hidden, id, { timeout: 5000 });
        st = await copyState();
        assert(!st.open && !st.behind && st.onTop === "terminal" && !st.green, `leaving copy mode after the moves reads ${JSON.stringify(st)}`);
        await prompt(page, id, "echo copy-left-$((60+6))");
        await waitText(page, id, "copy-left-66");
      } finally {
        if (await page.$(tabSel(id))) {
          await page.click(`${tabSel(id)} .editor-tab-state`).catch(() => {});
          await page.waitForSelector(tabSel(id), { state: "detached", timeout: 6000 }).catch(() => {});
        }
        await postAs(page, `/shells/${id}/delete`, {}).catch(() => {});
      }
      return `scrolled to ${at}, kept through a split and a move back to group ${home + 1}`;
    });

    await run("desktop: the picker's New shell opens the new shell as a tab", async () => {
      const before = await page.$$eval(".editor-tab-terminal", (els) => els.map((el) => el.dataset.path));
      await openPicker();
      await page.click("[data-editor-sheet-body] .dropdown-item:has-text('New shell')");
      await page.waitForFunction((b) => [...document.querySelectorAll(".editor-tab-terminal.active")].some((el) => !b.includes(el.dataset.path)), before, { timeout: 15000 });
      ids.fresh = (await activeTabPath(page)).replace("//terminal/", "");
      await mounted(page, ids.fresh);
    });

    await run("desktop: a shell deleted on another device closes its tab live", async () => {
      const ctx2 = await L.newDesktop(browser, engine);
      const p2 = await ctx2.newPage();
      L.wirePage(p2, bag);
      await L.login(p2);
      await p2.goto(editorURL, { waitUntil: "domcontentloaded" });
      const res = await postAs(p2, `/shells/${ids.fresh}/delete`, {});
      await ctx2.close();
      assert(res.ok, `the delete answered ${res.status}`);
      await page.waitForSelector(tabSel(ids.fresh), { state: "detached", timeout: 10000 });
      assert(!(await page.$(`terminal-attach[terminal-id="${ids.fresh}"]`)), "the deleted shell's island stayed");
    });

    await run("desktop: the tabs come back after a reload in their groups and order, a terminal gone meanwhile is dropped", async () => {
      const groupsBefore = await page.$$eval("[data-editor-group]", (gs) => gs.map((g) => [...g.querySelectorAll(".editor-tab")].map((t) => t.dataset.path)));
      ids.gone = await shellOf(page, project);
      await openEditor();
      await openPicker();
      await L.openPicked(page, `[data-editor-sheet-body] [data-term-pick="${ids.gone}"]`);
      await page.waitForSelector(`${tabSel(ids.gone)}.active`, { timeout: 8000 });
      const withGone = await page.$$eval("[data-editor-group]", (gs) => gs.map((g) => [...g.querySelectorAll(".editor-tab")].map((t) => t.dataset.path)));
      assert(JSON.stringify(withGone).includes(ids.gone), "the extra shell did not open");
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      assert((await postAs(page, `/shells/${ids.gone}/delete`, {})).ok, "deleting the extra shell failed");
      await openEditor();
      await mounted(page, ids.alpha);
      const after = await page.$$eval("[data-editor-group]", (gs) => gs.map((g) => [...g.querySelectorAll(".editor-tab")].map((t) => t.dataset.path)));
      assert(JSON.stringify(after) === JSON.stringify(groupsBefore), `the groups came back as ${JSON.stringify(after)}, were ${JSON.stringify(groupsBefore)}`);
      const saved = await savedTabs(page);
      assert(!JSON.stringify(saved).includes(ids.gone), "the stored tabs still name the gone shell");
      assert(saved.open.some((e) => e && e.type === "terminal" && e.id === ids.alpha), `the stored tabs carry no terminal entry: ${JSON.stringify(saved.open)}`);
    });

    await run("desktop: a terminal tab dragged within its strip changes the order, which survives a reload", async () => {
      const stripOf = (id) => page.evaluate((s) => [...document.querySelector(s).closest("[data-editor-tabs]").querySelectorAll(".editor-tab")].map((t) => t.dataset.path), tabSel(id));
      const before = await stripOf(ids.beta);
      assert(before.includes(`//terminal/${ids.alpha}`), `alpha and beta share no strip: ${JSON.stringify(before)}`);
      const [first, second] = before.indexOf(`//terminal/${ids.alpha}`) < before.indexOf(`//terminal/${ids.beta}`) ? [ids.alpha, ids.beta] : [ids.beta, ids.alpha];
      const from = await rect(page, tabSel(second));
      const to = await rect(page, tabSel(first));
      await page.mouse.move(from.x + from.w / 2, from.y + from.h / 2);
      await page.mouse.down();
      await page.mouse.move(from.x + from.w / 2 - 10, from.y + from.h / 2, { steps: 3 });
      await page.mouse.move(to.x + 4, to.y + to.h / 2, { steps: 12 });
      await sleep(200);
      await page.mouse.up();
      await sleep(600);
      const after = await stripOf(ids.beta);
      assert(after.indexOf(`//terminal/${second}`) < after.indexOf(`//terminal/${first}`), `the drag left the strip at ${JSON.stringify(after)}`);
      await openEditor();
      await mounted(page, ids.alpha).catch(() => {});
      const reloaded = await stripOf(ids.beta);
      assert(JSON.stringify(reloaded) === JSON.stringify(after), `the reload brought ${JSON.stringify(reloaded)}, the drag left ${JSON.stringify(after)}`);
    });

    await run("desktop: a file link in a terminal tab opens the file beside it and the terminal tab stays", async () => {
      await page.click(tabSel(ids.beta));
      await mounted(page, ids.beta);
      await prompt(page, ids.beta, "clear; echo notes.txt:2");
      await waitText(page, ids.beta, "notes.txt:2");
      await sleep(2000);
      const at = await page.evaluate((s) => {
        const node = document.querySelector(`${s} .attach-selection`)?.firstChild;
        const text = node ? node.data : "";
        const lines = text.split("\n");
        const row = lines.findIndex((l) => l.startsWith("notes.txt:2"));
        if (row < 0) return null;
        const offset = lines.slice(0, row).reduce((n, l) => n + l.length + 1, 0) + 1;
        const range = document.createRange();
        range.setStart(node, offset);
        range.setEnd(node, offset + 1);
        const r = range.getBoundingClientRect();
        return { x: r.left + r.width / 2, y: r.top + r.height / 2 };
      }, boxSel(ids.beta));
      assert(at, "the path is not on the screen");
      const screen = await rect(page, `${boxSel(ids.beta)} .xterm-screen`);
      let linked = false;
      for (let i = 0; i < 10 && !linked; i++) {
        await page.mouse.move(screen.x + 2, screen.b - 2);
        await page.mouse.move(at.x, at.y);
        linked = await page.waitForFunction((s) => document.querySelector(`${s} .xterm-screen`).classList.contains("xterm-cursor-pointer"), boxSel(ids.beta), { timeout: 800, polling: 25 }).then(() => true, () => false);
      }
      assert(linked, "the path shows no link");
      const url = page.url();
      await page.mouse.click(at.x, at.y);
      await page.waitForSelector('.editor-tab.active[data-path="notes.txt"]', { timeout: 8000 });
      assert(page.url().split("?")[0] === url.split("?")[0], "the link left the page");
      assert(await page.$(tabSel(ids.beta)), "the terminal tab closed");
      const sameGroup = await page.evaluate(([t, f]) => document.querySelector(t)?.closest("[data-editor-group]") === document.querySelector(f)?.closest("[data-editor-group]"), [tabSel(ids.beta), '.editor-tab[data-path="notes.txt"]']);
      assert(!sameGroup && (await shown(page, `${boxSel(ids.beta)} terminal-attach`)), "the file did not open beside the terminal, which is out of sight now");
    });

    await run("desktop: a terminal tab behind another and its picker row wear the news dot once its shell's command ended, showing the tab reads the news", async () => {
      const icon = `${tabSel(ids.alpha)} .dc-term-icon`;
      assert(!(await shown(page, `${boxSel(ids.alpha)} terminal-attach`)), "alpha is not behind beta");
      const reads = [];
      const onRequest = (r) => { if (r.url().endsWith("/notifications/read") && r.method() === "POST") reads.push(r.postData() || ""); };
      page.on("request", onRequest);
      try {
        await prompt(page, ids.alpha, "sleep 3");
        await page.waitForSelector(`${icon}.news`, { timeout: 15000 });
        assert(await shown(page, `${icon}.news .dc-news-dot`), "the news dot does not show on the tab");
        await openPicker();
        assert(await shown(page, `[data-editor-sheet-body] [data-term-pick="${ids.alpha}"] .dc-term-icon.news .dc-news-dot`), "the news dot does not show on the picker's row");
        await page.click("[data-editor-sheet-close]");
        await page.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 5000 });
        await page.click(tabSel(ids.alpha));
        await mounted(page, ids.alpha);
        await page.waitForFunction((s) => !document.querySelector(s)?.classList.contains("news"), icon, { timeout: 8000 });
        assert(reads.some((b) => b.includes(ids.alpha)), `showing the tab read nothing: ${reads.join(" | ")}`);
      } finally {
        page.off("request", onRequest);
      }
      await page.click(tabSel(ids.beta));
    });

    await run("desktop: a coder made from the picker's New coder comes back as a tab, with no error toast for the terminals read the page change cut", async () => {
      await openPicker();
      await page.click("[data-editor-sheet-body] .dropdown-item:has-text('New coder')");
      await page.waitForSelector("[data-form-modal].show form", { timeout: 10000 });
      const f = page.locator('[data-form-modal] form:has(input[name="name"])').first();
      await f.locator('input[name="name"]').fill(`ettc-${tag.slice(-5)}`);
      const from = page.url();
      await page.evaluate(() => {
        const seen = (window.__ettErrorToasts = []);
        window.__ettToastWatch = new MutationObserver(() => {
          for (const t of document.querySelectorAll("[data-dc-toasts] .toast")) {
            if (!t.querySelector(".text-danger")) continue;
            const text = t.textContent.replace(/\s+/g, " ").trim();
            if (!seen.includes(text)) seen.push(text);
          }
        });
        window.__ettToastWatch.observe(document.body, { childList: true, subtree: true });
      });
      const termsRead = (u) => new URL(u).pathname.endsWith("/editor/terminals");
      const cut = [];
      const onFailed = (r) => { if (termsRead(r.url())) cut.push(r.failure()?.errorText || "failed"); };
      const hold = async (route) => {
        await sleep(1500);
        await route.continue().catch(() => {});
      };
      page.on("requestfailed", onFailed);
      await page.route(termsRead, hold);
      try {
        await f.locator('button[type="submit"]').first().click();
        await page.waitForURL((u) => /\/editor\?terminal=[^&]+$/.test(u.href) && u.href !== from, { timeout: 30000 });
        coderId = new URL(page.url()).searchParams.get("terminal");
        await page.waitForSelector(`${tabSel(coderId)}.active`, { timeout: 15000 });
        await mounted(page, coderId);
      } finally {
        await page.unroute(termsRead, hold);
        page.off("requestfailed", onFailed);
      }
      await sleep(500);
      const errorToasts = await page.evaluate(() => {
        window.__ettToastWatch?.disconnect();
        return window.__ettErrorToasts;
      });
      assert(Array.isArray(errorToasts), "the New coder round trip reloaded the page instead of navigating in it");
      assert(cut.length, "the editor's read of its terminals was never in flight when the page moved on");
      assert(!errorToasts.length, `the New coder round trip showed an error toast: ${errorToasts.join(" | ")}`);
      assert(await shown(page, `${boxSel(coderId)} .attach-desktop .coder-files-button`), "the coder tab's footer carries no Files button");
    });

    await run("desktop: a coder tab wears the working ring of a turn and loses it when the turn ends", async () => {
      if (!NOTIFY_DIR) throw new Error("NOTIFY_DIR not set, the working ring needs the notify inbox");
      const icon = `${tabSel(coderId)} .dc-term-icon`;
      injectCopilot(coderId, "TurnStart");
      await page.waitForSelector(`${icon}.working[data-working-since]`, { timeout: 12000 });
      await openPicker();
      const row = await page.$eval(`[data-editor-sheet-body] [data-term-pick="${coderId}"] .dc-term-icon`, (el) => el.className);
      assert(/working/.test(row), `the picker's row of the working coder reads ${row}`);
      await page.click("[data-editor-sheet-close]");
      injectCopilot(coderId, "Stop");
      await page.waitForFunction((s) => !document.querySelector(s)?.classList.contains("working"), icon, { timeout: 12000 });
    }, { soft: !NOTIFY_DIR });

    await run("desktop: the coder tab's Files opens its upload, a file dropped on the terminal is uploaded", async () => {
      await page.click(`${boxSel(coderId)} .attach-desktop .coder-files-button`);
      await page.waitForFunction((id) => document.getElementById(`coder-files-modal-${id}`)?.classList.contains("show"), coderId, { timeout: 8000 });
      const action = await page.evaluate((id) => document.querySelector(`#coder-files-modal-${id} form[data-coder-file-upload-form]`)?.getAttribute("action"), coderId);
      assert(action === `/coders/${coderId}/files`, `the upload form posts to ${action}`);
      await page.click(`#coder-files-modal-${coderId} .btn-close`);
      await page.waitForFunction((id) => !document.getElementById(`coder-files-modal-${id}`)?.classList.contains("show"), coderId, { timeout: 8000 });
      await sleep(600);
      const upload = page.waitForResponse((r) => r.url().endsWith(`/coders/${coderId}/files`) && r.request().method() === "POST", { timeout: 15000 });
      await page.evaluate((s) => {
        const target = document.querySelector(`${s} terminal-attach`);
        const dt = new DataTransfer();
        dt.items.add(new File(["dropped"], `drop-${Date.now()}.txt`, { type: "text/plain" }));
        for (const type of ["dragenter", "dragover", "drop"]) {
          target.dispatchEvent(new DragEvent(type, { bubbles: true, cancelable: true, dataTransfer: dt }));
        }
      }, boxSel(coderId));
      assert((await upload).ok(), "the dropped file was not uploaded");
      await page.waitForFunction((id) => document.getElementById(`coder-files-modal-${id}`)?.classList.contains("show"), coderId, { timeout: 8000 }).catch(() => {});
      await closeFilesModal(coderId);
    });

    await run("desktop: a file pasted into a coder tab is uploaded", async () => {
      await closeFilesModal(coderId);
      await clickTerminal(page, coderId);
      assert(await focusedIn(page, coderId), "the coder's terminal does not have the focus");
      const name = `pasted-${Date.now()}.txt`;
      const upload = page.waitForResponse((r) => r.url().endsWith(`/coders/${coderId}/files`) && r.request().method() === "POST", { timeout: 15000 });
      upload.catch(() => {});
      const handled = await page.evaluate((n) => {
        const dt = new DataTransfer();
        dt.items.add(new File(["pasted"], n, { type: "text/plain" }));
        const event = new ClipboardEvent("paste", { clipboardData: dt, bubbles: true, cancelable: true });
        document.activeElement.dispatchEvent(event);
        return { files: event.clipboardData?.files?.length || 0, taken: event.defaultPrevented, in: document.activeElement.className };
      }, name);
      assert(handled.files === 1 && handled.taken, `the paste was not taken: ${JSON.stringify(handled)}`);
      assert((await upload).ok(), "the pasted file was not uploaded");
      await page.waitForFunction(([id, n]) => (document.getElementById(`coder-files-modal-${id}`)?.textContent || "").includes(n), [coderId, name], { timeout: 10000 });
      await closeFilesModal(coderId);
    });

    await run("desktop: Insert in the coder tab's Files pastes the path into that coder and stays, closing gives the tab the focus", async () => {
      await closeFilesModal(coderId);
      await page.click(`${boxSel(coderId)} .attach-desktop .coder-files-button`);
      await page.waitForFunction((id) => document.getElementById(`coder-files-modal-${id}`)?.classList.contains("show"), coderId, { timeout: 8000 });
      const insert = page.locator(`#coder-files-modal-${coderId} [data-insert-file-path]`).first();
      await insert.waitFor({ state: "visible", timeout: 8000 });
      const word = await insert.getAttribute("data-insert-file-path");
      const sent = [];
      const watch = (r) => { if (r.url().endsWith("/input") && r.method() === "POST") sent.push(`${new URL(r.url()).pathname} ${r.postData()}`); };
      page.on("request", watch);
      try {
        await sleep(400);
        await insert.click();
        await page.waitForSelector(`#coder-files-modal-${coderId} [data-insert-file-path] .ti-check`, { state: "visible", timeout: 3000 });
        await sleep(800);
        const want = `/coders/${coderId}/input ${JSON.stringify({ items: [{ paste: ` ${word} ` }] })}`;
        assert(JSON.stringify(sent) === JSON.stringify([want]), `Insert sent ${JSON.stringify(sent)}`);
        assert(await page.evaluate((id) => { const m = document.getElementById(`coder-files-modal-${id}`); return !!m && m.classList.contains("show") && getComputedStyle(m).display !== "none"; }, coderId), "the dialog closed on Insert");
        await page.click(`#coder-files-modal-${coderId} .btn-close`);
        await page.waitForFunction((id) => { const m = document.getElementById(`coder-files-modal-${id}`); return m && getComputedStyle(m).display === "none"; }, coderId, { timeout: 8000 });
        await sleep(400);
        assert(await focusedIn(page, coderId), "the coder tab does not have the focus after the dialog closed");
      } finally {
        page.off("request", watch);
      }
    });

    await run("desktop: a steered coder wears the steered mark on its tab and its picker row, with the assistant in the tooltip", async () => {
      const made = JSON.parse((await postAs(page, "/assistants/new", { form: "new", coder: "claude" })).text || "{}");
      steerer = made.id || "";
      assert(steerer, "no assistant to steer with");
      const title = `ett-steer-${tag.slice(-5)}`;
      assert((await postAs(page, `/assistants/${steerer}`, { form: "rename", title })).ok, "renaming the assistant failed");
      const steered = await postAs(page, "/assistants/jobs", { form: "steer", terminal: coderId, assistant: steerer, done_when: "" });
      assert(steered.ok, `the steer answered ${steered.status}: ${steered.text.slice(0, 200)}`);
      try {
        const icon = `${tabSel(coderId)} .dc-term-icon`;
        await page.waitForSelector(`${icon}.steered`, { timeout: 15000 });
        const tab = await page.evaluate((s) => {
          const el = document.querySelector(s);
          return { title: el.title, bg: getComputedStyle(el.querySelector(".dc-term-icon")).backgroundColor };
        }, tabSel(coderId));
        assert(tab.title.includes(`steered by ${title}`), `the tab's tooltip reads ${tab.title}`);
        const labels = await tabMenu(page, tabSel(coderId));
        await closeMenu(page);
        assert(labels.includes("Release") && labels.includes(title) && !labels.includes("Steer"), `the tab menu reads ${labels.join(", ")}`);
        await openPicker();
        const row = await page.evaluate((s) => {
          const el = document.querySelector(s);
          return el && { cls: el.className, bg: getComputedStyle(el).backgroundColor };
        }, `[data-editor-sheet-body] [data-term-pick="${coderId}"] .dc-term-icon`);
        assert(/steered/.test(row.cls) && row.bg === tab.bg, `the picker's row reads ${JSON.stringify(row)}, the tab ${tab.bg}`);
        await page.click("[data-editor-sheet-close]");
      } finally {
        await postAs(page, "/assistants/jobs", { form: "release", terminal: coderId });
      }
      await page.waitForFunction((s) => !document.querySelector(s)?.classList.contains("steered"), `${tabSel(coderId)} .dc-term-icon`, { timeout: 15000 });
    });

    await run("desktop: a coder stopped on another device shows Resume in its tab, Resume brings it back", async () => {
      const ctx2 = await L.newDesktop(browser, engine);
      const p2 = await ctx2.newPage();
      L.wirePage(p2, bag);
      await L.login(p2);
      await p2.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      const res = await postAs(p2, `/coders/${coderId}/stop`, {});
      await ctx2.close();
      assert(res.ok, `the stop answered ${res.status}`);
      const resume = `.editor-term-box form[data-term-resume] [data-resume-id="${coderId}"]`;
      await page.waitForSelector(resume, { state: "visible", timeout: 15000 });
      assert(!(await page.$(`terminal-attach[terminal-id="${coderId}"]`)), "the stopped coder's island stayed");
      const icon = await page.evaluate((s) => document.querySelector(`${s} .dc-term-icon`)?.className || "none", tabSel(coderId));
      assert(!/running/.test(icon), `the stopped coder's tab still runs: ${icon}`);
      await page.click(resume);
      await mounted(page, coderId);
      await page.waitForSelector(`${tabSel(coderId)} .dc-term-icon.running`, { timeout: 10000 });
    });

    await run("desktop: a coder stopped while the editor was not open comes back as its tab with Resume in its group and place, Resume brings it back running", async () => {
      await page.waitForSelector(tabSel(coderId), { timeout: 10000 });
      const layout = () => page.$$eval("[data-editor-group]", (gs) => gs.map((g) => [...g.querySelectorAll(".editor-tab")].map((t) => t.dataset.path)));
      const before = await layout();
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      assert((await postAs(page, `/coders/${coderId}/stop`, {})).ok, "stopping the coder away from the editor failed");
      await openEditor();
      await page.waitForSelector(tabSel(coderId), { timeout: 15000 });
      await sleep(600);
      const after = await layout();
      assert(JSON.stringify(after) === JSON.stringify(before), `the tabs did not come back in their groups and places: ${JSON.stringify({ before, after })}`);
      await page.click(tabSel(coderId));
      const resume = `.editor-term-box form[data-term-resume] [data-resume-id="${coderId}"]`;
      await page.waitForSelector(resume, { state: "visible", timeout: 15000 });
      const icon = await page.evaluate((s) => document.querySelector(`${s} .dc-term-icon`)?.className || "none", tabSel(coderId));
      assert(!/running/.test(icon), `the coder stopped away still runs on its tab: ${icon}`);
      assert(!(await page.$(`terminal-attach[terminal-id="${coderId}"]`)), "the stopped coder's tab mounted a terminal");
      await page.click(resume);
      await mounted(page, coderId);
      await page.waitForSelector(`${tabSel(coderId)} .dc-term-icon.running`, { timeout: 10000 });
      assert(JSON.stringify(await layout()) === JSON.stringify(before), "the resume moved the tab");
    });

    await run("desktop: the picker lists a stopped coder with its stopped mark, its menu resumes it into a tab", async () => {
      assert((await postAs(page, `/coders/${coderId}/stop`, {})).ok, "stopping the coder failed");
      await page.waitForSelector(`.editor-term-box form[data-term-resume] [data-resume-id="${coderId}"]`, { state: "visible", timeout: 15000 });
      await page.click(`${tabSel(coderId)} .editor-tab-state`);
      await page.waitForSelector(tabSel(coderId), { state: "detached", timeout: 6000 });
      await openPicker();
      const rows = await pickRows(page);
      const row = rows.find((r) => r.id === coderId);
      const sheet = await page.evaluate(() => [...document.querySelectorAll("[data-editor-sheet-body] > *")].map((e) => `${e.dataset.termPick || ""}:${e.hidden}:${e.textContent.replace(/\s+/g, " ").trim()}`));
      assert(row && /stopped/.test(row.text) && !/running/.test(row.icon), `the stopped coder's row reads ${JSON.stringify(row)}, the sheet holds ${JSON.stringify(sheet)}, the coder ${coderId}`);
      const head = await page.evaluate(() => [...document.querySelectorAll("[data-editor-sheet-body] [data-editor-sheet-head]")].map((h) => h.textContent.trim()));
      assert(head.includes("Stopped coders"), `no Stopped coders head: ${head.join(", ")}`);
      const running = rows.find((r) => r.id === ids.alpha);
      assert(running && /running/.test(running.icon), "a running shell lost its mark next to the stopped coder");
      await L.openPicked(page, `[data-editor-sheet-body] [data-term-pick="${coderId}"]`, { item: "Resume" });
      await page.waitForSelector(`${tabSel(coderId)}.active`, { timeout: 30000 });
      await mounted(page, coderId);
      await page.waitForSelector(`${tabSel(coderId)} .dc-term-icon.running`, { timeout: 10000 });
      assert((await page.locator(tabSel(coderId)).count()) === 1, "the resumed coder got a second tab");
    });

    await run("desktop: the coder tab's Delete removes the coder and closes its tab", async () => {
      await tabMenu(page, tabSel(coderId));
      await page.evaluate(() => {
        const items = [...document.querySelectorAll(".dc-context-menu .dropdown-item")];
        items.reverse().find((b) => (b.querySelector(".dc-menu-label-head") || b).textContent.trim() === "Delete").click();
      });
      await confirmSwal(page);
      await page.waitForSelector(tabSel(coderId), { state: "detached", timeout: 15000 });
      coderId = "";
    });

    await run("desktop: a stopped coder's picker row and tab menu offer Delete, which asks like its chip on the projects page and closes its tab on every open page", async () => {
      const dir = await L.projectPath(page, project);
      const coder = await page.evaluate(async (u) => {
        const doc = new DOMParser().parseFromString(await (await fetch(u)).text(), "text/html");
        const select = doc.querySelector('select[name="coder"]');
        return select?.querySelector('option[value="copilot"]') ? "copilot" : select?.value || "";
      }, `/coders/new?project=${encodeURIComponent(project)}`);
      const start = async (name) => {
        const res = await postAs(page, "/coders/new", { name, project: dir, coder });
        assert(res.ok, `a new ${coder} coder answered ${res.status}: ${res.text.slice(0, 160)}`);
        const id = JSON.parse(res.text).id;
        stoppedCoders.push(id);
        return id;
      };
      const one = await start(`ettd1-${tag.slice(-5)}`);
      const two = await start(`ettd2-${tag.slice(-5)}`);
      await sleep(5000);
      for (const id of [one, two]) assert((await postAs(page, `/coders/${id}/stop`, {})).ok, `stopping ${id} failed`);
      for (let t = 0; ; t += 500) {
        const stopped = await page.evaluate(async (u) => {
          const doc = new DOMParser().parseFromString(await (await fetch(u)).text(), "text/html");
          return [...doc.querySelectorAll("[data-term-stopped]")].map((v) => v.getAttribute("data-term-stopped"));
        }, `/projects/${project}/editor/terminals`);
        if (stopped.includes(one) && stopped.includes(two)) break;
        assert(t < 15000, `the stopped ${coder} coders never became resumable: ${stopped.join(", ")}`);
        await sleep(500);
      }
      const swalTitle = async (p) => {
        await p.waitForSelector(".swal2-title", { state: "visible", timeout: 8000 });
        return (await p.textContent(".swal2-title")).trim();
      };
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      const chipX = `#project-${project} form[data-inactive-coder="${one}"] button`;
      await page.waitForSelector(chipX, { state: "visible", timeout: 15000 });
      await page.click(chipX);
      const chipTitle = await swalTitle(page);
      assert(chipTitle === `Delete inactive coder "ettd1-${tag.slice(-5)}"?`, `the chip asks ${chipTitle}`);
      await page.click(".swal2-cancel");
      await page.waitForSelector(".swal2-container", { state: "detached", timeout: 5000 });
      await openEditor();
      await openPicker();
      await page.waitForSelector(`[data-editor-sheet-body] [data-term-pick="${one}"]`, { timeout: 10000 });
      await page.click(`[data-editor-sheet-body] [data-term-pick="${one}"]`);
      await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
      const rowMenu = await page.$$eval(".dc-context-menu .dropdown-item", (els) => els.map((el) => (el.querySelector(".dc-menu-label-head") || el).textContent.trim()));
      assert(JSON.stringify(rowMenu) === JSON.stringify(["Open", "Resume", "Delete"]), `the stopped coder's row menu reads ${rowMenu.join(", ")}`);
      assert(await page.evaluate(() => [...document.querySelectorAll(".dc-context-menu .dropdown-item")].pop().classList.contains("text-danger")), "the row's Delete is not marked as danger");
      await menuClick(page, "Open");
      await page.waitForSelector(`${tabSel(one)}.active`, { timeout: 10000 });
      await page.waitForSelector(`.editor-term-box form[data-term-resume] [data-resume-id="${one}"]`, { state: "visible", timeout: 10000 });
      const ctx2 = await L.newDesktop(browser, engine);
      const p2 = await ctx2.newPage();
      L.wirePage(p2, bag);
      try {
        await L.login(p2);
        for (const id of [one, two]) {
          await p2.goto(`${editorURL}?terminal=${id}`, { waitUntil: "domcontentloaded" });
          await L.dismissUpdate(p2);
          await p2.waitForSelector(`${tabSel(id)}.active`, { timeout: 15000 });
        }
        await p2.waitForSelector(tabSel(one), { timeout: 10000 });
        await sleep(400);
        const labels = await tabMenu(page, tabSel(one));
        assert(labels.includes("Resume") && labels[labels.length - 1] === "Delete" && !labels.includes("Stop"), `the stopped coder's tab menu reads ${labels.join(", ")}`);
        await menuClick(page, "Delete");
        assert((await swalTitle(page)) === chipTitle, "the tab's Delete asks other than the chip");
        await confirmSwal(page);
        await page.waitForSelector(`.dc-toast:has-text('Coder "ettd1-${tag.slice(-5)}" deleted.')`, { state: "visible", timeout: 10000 });
        await page.waitForSelector(tabSel(one), { state: "detached", timeout: 10000 });
        await p2.waitForSelector(tabSel(one), { state: "detached", timeout: 15000 });
        await sleep(400);
        await openPicker();
        await page.click(`[data-editor-sheet-body] [data-term-pick="${two}"]`);
        await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
        await menuClick(page, "Delete");
        assert((await swalTitle(page)) === `Delete inactive coder "ettd2-${tag.slice(-5)}"?`, "the row's Delete asks other than the chip");
        await confirmSwal(page);
        await page.waitForSelector(`[data-editor-sheet-body] [data-term-pick="${two}"]`, { state: "detached", timeout: 10000 });
        assert(await shown(page, "[data-editor-sheet-panel]"), "the picker closed with the row's Delete");
        await p2.waitForSelector(tabSel(two), { state: "detached", timeout: 15000 });
        const gone = await page.evaluate(() => [...document.querySelectorAll("[data-dc-toasts] .toast")].filter((t) => t.querySelector(".text-danger")).map((t) => t.textContent.trim()));
        assert(!gone.length, `a delete toasted an error: ${gone.join(" | ")}`);
        stoppedCoders.length = 0;
      } finally {
        await ctx2.close();
        await page.keyboard.press("Escape").catch(() => {});
      }
    });

    await run("mobile: Open terminal is a sheet with big rows, a tap opens the terminal as a tab with the phone's controls", async () => {
      const mp = await mobilePage();
      await mp.goto(`${editorURL}?file=notes.txt`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(mp);
      await mp.waitForSelector(".cm-editor, .editor-textarea", { state: "attached", timeout: 20000 });
      await sleep(1200);
      if (await shown(mp, "[data-editor-backdrop]")) {
        await mp.touchscreen.tap(380, 400);
        await mp.waitForFunction(() => document.querySelector("[data-editor-backdrop]").hidden, null, { timeout: 5000 });
      }
      await mp.tap("[data-editor-menu]");
      await mp.waitForSelector("[data-editor-menu-list].show", { timeout: 5000 });
      await mp.tap("[data-editor-term-open-item]");
      await mp.waitForSelector(`[data-editor-sheet-body] [data-term-pick="${ids.beta}"]`, { state: "visible", timeout: 10000 });
      const sheet = await rect(mp, "[data-editor-sheet-panel]");
      const rows = await pickRows(mp);
      assert(sheet.w >= 385 && sheet.b >= 760, `the picker is no bottom sheet: ${JSON.stringify(sheet)}`);
      assert(rows.every((r) => (r.id ? r.h >= 48 : r.h >= 36)), `a row is small for a finger: ${JSON.stringify(rows.map((r) => r.h))}`);
      assert(!(await mp.evaluate(() => document.activeElement === document.querySelector("[data-editor-sheet-filter]"))), "the filter took the focus on a phone");
      await L.openPicked(mp, `[data-editor-sheet-body] [data-term-pick="${ids.beta}"]`, { tap: true });
      await mp.waitForSelector(`${tabSel(ids.beta)}.active`, { timeout: 8000 });
      await mounted(mp, ids.beta);
      for (const sel of ['[data-terminal-control="escape"]', '[data-terminal-control="enter"]', "terminal-direction-pad", "[data-terminal-paste]", "[data-terminal-copy]", "[data-terminal-settings]"]) {
        assert(await shown(mp, `${boxSel(ids.beta)} .attach-mobile ${sel}`), `the phone's controls miss ${sel}`);
      }
      const width = await mp.evaluate(() => document.documentElement.scrollWidth);
      assert(width <= 390, `the page scrolls sideways: ${width}`);
    });

    await run("mobile: a tap puts the keyboard on the cursor input, typed text, Enter and the Paste button reach the shell", async () => {
      const mp = await mobilePage();
      const t = await rect(mp, `${boxSel(ids.beta)} terminal-attach`);
      await mp.touchscreen.tap(t.x + t.w / 2, t.y + t.h / 2);
      await sleep(400);
      const focus = await mp.evaluate(() => document.activeElement?.id || "");
      assert(focus === "terminal-cursor-input", `a tap focused ${focus}`);
      await mp.keyboard.type("echo phone-$((8*8))");
      await mp.tap(`${boxSel(ids.beta)} [data-terminal-control="enter"]`);
      await waitText(mp, ids.beta, "phone-64");
      if (engine !== "chromium") return "the Paste button needs a clipboard grant, read in Chromium";
      await mp.context().grantPermissions(["clipboard-read", "clipboard-write"], { origin: BASE });
      await mp.evaluate(() => navigator.clipboard.writeText("echo pasted-$((9*9))"));
      await mp.tap(`${boxSel(ids.beta)} [data-terminal-paste]`);
      await sleep(500);
      await mp.tap(`${boxSel(ids.beta)} [data-terminal-control="enter"]`);
      await waitText(mp, ids.beta, "pasted-81");
    });

    await run("mobile: a sideways swipe on the terminal tab walks to the next tab, the settings open as a sheet", async () => {
      const mp = await mobilePage();
      const before = await activeTabPath(mp);
      const t = await rect(mp, `${boxSel(ids.beta)} terminal-attach`);
      const cdp = engine === "chromium" ? await mp.context().newCDPSession(mp) : null;
      if (cdp) {
        const y = t.y + t.h / 2;
        await cdp.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ x: t.x + t.w - 30, y }] });
        for (let i = 1; i <= 10; i++) {
          await cdp.send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ x: t.x + t.w - 30 - i * 22, y }] });
          await sleep(16);
        }
        await cdp.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
        await sleep(900);
        assert((await activeTabPath(mp)) !== before, "the swipe did not walk to the next tab");
        await mp.tap(tabSel(ids.beta)).catch(() => {});
        await openTabFromStrip(mp, ids.beta);
      }
      await mp.tap(`${boxSel(ids.beta)} .attach-mobile [data-terminal-settings]`);
      await mp.waitForFunction(() => document.querySelector("[data-editor-sheet-title]")?.textContent === "Terminal settings", null, { timeout: 5000 });
      assert(await shown(mp, '[data-editor-sheet-body] terminal-setting-select[setting="theme"] select'), "the phone's settings sheet has no theme");
      await mp.tap("[data-editor-sheet-close]");
      return cdp ? "swiped" : "swipe needs CDP touch, read in Chromium";
    });

    await run("guest: a link's guest picks a terminal into a tab and only watches it", async () => {
      const made = await postAs(page, "/settings/sharing/new", [["amount", "1"], ["unit", "hours"], ["name", `ett-${tag.slice(-5)}`], ["project", project]]);
      const link = (made.text.match(/value="([^"]*\/share\/[^"]+)"/) || [])[1];
      assert(made.ok && link, `making a link answered ${made.status}`);
      const gctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1360, height: 900 } });
      const gp = await gctx.newPage();
      L.wirePage(gp, bag);
      const guestReqs = [];
      gp.on("request", (r) => guestReqs.push(`${r.method()} ${new URL(r.url()).pathname}`));
      try {
        const base = new URL(link).pathname;
        await gp.goto(`${BASE}${base}/projects/${project}/editor`, { waitUntil: "domcontentloaded" });
        await gp.waitForSelector(".cm-editor, .editor-textarea", { state: "attached", timeout: 20000 });
        await sleep(800);
        await openPicker(gp);
        const rows = await pickRows(gp);
        assert(rows.some((r) => r.id === ids.beta), `the guest's picker misses the shell: ${JSON.stringify(rows)}`);
        assert(!rows.some((r) => /New shell|New coder|stopped/.test(r.text)), `the guest is offered more than watching: ${rows.map((r) => r.text).join(" | ")}`);
        assert(!rows.some((r) => r.id === ids.foreign), "the guest sees another project's shell");
        await L.openPicked(gp, `[data-editor-sheet-body] [data-term-pick="${ids.beta}"]`);
        await gp.waitForSelector(`${tabSel(ids.beta)}.active`, { timeout: 8000 });
        await mounted(gp, ids.beta);
        assert(!(await gp.$(`${boxSel(ids.beta)} terminal-input`)), "the guest's tab mounted an input");
        assert(await gp.$(`${boxSel(ids.beta)} terminal-attach[read-only]`), "the guest's terminal is not read only");
        assert(!(await shown(gp, `${boxSel(ids.beta)} [data-terminal-control]`)), "the guest's footer carries controls");
        await prompt(page, ids.beta, "echo for-the-guest-$((6+6))");
        await waitText(gp, ids.beta, "for-the-guest-12");
        const labels = await tabMenu(gp, tabSel(ids.beta));
        for (const never of ["Rename", "Delete", "Stop", "Steer", "Mark read", "Open project"]) {
          assert(!labels.includes(never), `the guest's tab menu offers ${never}: ${labels.join(", ")}`);
        }
        assert(labels.includes("Close") && labels.includes("Open terminal page"), `the guest's tab menu reads ${labels.join(", ")}`);
        await closeMenu(gp);
        const outside = guestReqs.filter((r) => /^[A-Z]+ \/(projects|shells|coders|notifications|terminal-tabs|events|settings)\b/.test(r));
        assert(!outside.length, `the guest's page reached owner routes: ${outside.join(", ")}`);
        assert(!guestReqs.some((r) => /\/input$|\/resize$|\/notifications\/read$/.test(r)), `the guest posted input, resize or reads: ${guestReqs.filter((r) => r.startsWith("POST")).join(", ")}`);
      } finally {
        await gctx.close();
      }
    });
  } finally {
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" }).catch(() => {});
    for (const id of Object.values(ids)) {
      if (id) await postAs(page, `/shells/${id}/delete`, {}).catch(() => {});
    }
    if (coderId) await postAs(page, `/coders/${coderId}/delete`, {}).catch(() => {});
    for (const id of stoppedCoders) await postAs(page, `/coders/${id}/delete`, {}).catch(() => {});
    if (steerer) await postAs(page, `/assistants/${steerer}`, { form: "delete" }).catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
    await L.deleteProject(page, other).catch(() => {});
  }

  async function openTabFromStrip(p, id) {
    await p.waitForSelector(`${tabSel(id)}.active`, { timeout: 6000 }).catch(async () => {
      await p.evaluate((s) => document.querySelector(s)?.click(), tabSel(id));
    });
    await mounted(p, id);
  }
});
