const L = require("./lib");
const { assert, sleep } = L;

// The size of the editor's sheets ([data-editor-sheet-panel]), run by
// editor-terminal.js on its project, its shells and its picker: on a phone
// every sheet is as tall as the rail's list sheet (dc-ctx-sheet, 52% of the
// window, one --dc-sheet-h both read), from 768px wide it is 70% of the
// editor, docked at its right edge at three quarters of its width, whatever
// it holds. Filtering a list down to one row and to none and a row arriving
// live never move the panel; the rows start under the filter, the list fills
// the panel and scrolls inside it, Nothing matches stands right under the
// filter. Where the editor is shorter (a phone in landscape, a low window)
// the sheet stays inside the editor. Every reading is a measured box.

const measure = (p) => p.evaluate(() => {
  const q = (s) => document.querySelector(s);
  const r = (el) => {
    if (!el) return null;
    const b = el.getBoundingClientRect();
    return b.width > 0 && b.height > 0 && getComputedStyle(el).visibility !== "hidden" ? { t: b.top, b: b.bottom, h: b.height, l: b.left, r: b.right, w: b.width } : null;
  };
  const body = q("[data-editor-sheet-body]");
  const rows = [...body.querySelectorAll(".dropdown-item, .editor-sheet-open")].filter((el) => el.getClientRects().length);
  return {
    open: !q("[data-editor-sheet]").hidden,
    title: q("[data-editor-sheet-title]").textContent,
    editor: r(q("[data-editor-sheet]")),
    panel: r(q("[data-editor-sheet-panel]")),
    head: r(q(".editor-sheet-head")),
    filter: r(q("[data-editor-sheet-filter-row]")),
    body: r(body),
    empty: r(q("[data-editor-sheet-empty]")),
    first: r(rows[0]),
    last: r(rows[rows.length - 1]),
    rows: rows.length,
    scroll: { top: body.scrollTop, height: body.scrollHeight, client: body.clientHeight },
    vh: innerHeight,
    page: document.scrollingElement.scrollTop,
  };
});

const near = (a, b, tol = 1) => Math.abs(a - b) <= tol;
const brief = (m) => JSON.stringify({ title: m.title, editor: m.editor, panel: m.panel, body: m.body, filter: m.filter, empty: m.empty, rows: m.rows, scroll: m.scroll, vh: m.vh });

module.exports = async function sheetSizeChecks({ browser, page, run, mobilePage, bag, openEditor, pickerOpen, waitRows, elsewhere, shellOf, postAs, made }) {
  // Where the file tree is a drawer (a phone, a low window) the first load
  // opens it over the code; a tap or a click on its backdrop closes it.
  const openClear = async (p, touch) => {
    await openEditor(p);
    const covered = await p.evaluate(() => { const b = document.querySelector("[data-editor-backdrop]"); return !!b && !b.hidden && b.getBoundingClientRect().width > 0; });
    if (!covered) return;
    const at = await p.evaluate(() => [innerWidth - 10, Math.round(innerHeight / 2)]);
    if (touch) await p.touchscreen.tap(at[0], at[1]);
    else await p.mouse.click(at[0], at[1]);
    await p.waitForFunction(() => document.querySelector("[data-editor-backdrop]").hidden, null, { timeout: 5000 });
  };
  const sheetUp = (p) => p.waitForFunction(() => !document.querySelector("[data-editor-sheet]").hidden, null, { timeout: 8000 });
  const sheetDown = (p) => p.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 5000 });
  const press = (p, sel, touch) => (touch ? p.tap(sel) : p.click(sel));
  // The sheets every project has, each opened the way a hand does: the
  // statusbar's two icons and the editor menu's entries.
  const kinds = [
    ["Terminals", async (p, touch) => { await press(p, "[data-editor-term-status]", touch); await pickerOpen(p); }],
    ["Assistants", async (p, touch) => { await press(p, "[data-editor-asst-status]", touch); await sheetUp(p); }],
    ["Line comments", async (p, touch) => { await press(p, "[data-editor-menu]", touch); await press(p, "[data-editor-comments-item]", touch); await sheetUp(p); }],
    ["Editor settings", async (p, touch) => { await press(p, "[data-editor-menu]", touch); await press(p, "[data-editor-settings-item]", touch); await sheetUp(p); }],
  ];
  const close = async (p, touch) => {
    await press(p, "[data-editor-sheet-close]", touch);
    await sheetDown(p);
    await sleep(350);
  };
  const eachKind = async (p, touch, check) => {
    const seen = [];
    for (const [title, open] of kinds) {
      await open(p, touch);
      await sleep(400);
      const m = await measure(p);
      assert(m.open && m.title === title, `${title} did not open: ${brief(m)}`);
      check(m, title);
      seen.push(`${title} ${Math.round(m.panel.h)}`);
      await close(p, touch);
    }
    return seen;
  };
  const docked = (m, title, where) => {
    assert(near(m.panel.h, m.editor.h * 0.7), `${where}: ${title} is not 70% of the editor: ${brief(m)}`);
    assert(near(m.panel.b, m.editor.b) && near(m.panel.r, m.editor.r) && near(m.panel.w, m.editor.w * 0.75), `${where}: ${title} does not dock at the editor's right edge at three quarters of its width: ${brief(m)}`);
    assert(m.panel.t >= m.editor.t - 1, `${where}: ${title} stands above the editor: ${brief(m)}`);
  };
  const fills = (m, where) => {
    assert(near(m.body.b, m.panel.b), `${where}: the list does not reach the panel's bottom: ${brief(m)}`);
    assert(near(m.body.t, (m.filter || m.head).b), `${where}: the list does not start under the ${m.filter ? "filter" : "head"}: ${brief(m)}`);
  };

  await run("sheet size: on a phone every editor sheet is as tall as the rail's list sheet, 52% of the window, and its list fills it", async () => {
    const mp = await mobilePage();
    await mp.setViewportSize({ width: 390, height: 844 });
    await openClear(mp, true);
    await mp.tap('.dc-tabbar button[data-ctx-area="projects"]');
    await mp.waitForSelector("dc-ctx-sheet:not([hidden]) .dc-ctx", { timeout: 8000 });
    await sleep(400);
    const rail = await mp.$eval("dc-ctx-sheet:not([hidden]) .dc-sheet-panel", (el) => el.getBoundingClientRect().height);
    await mp.keyboard.press("Escape");
    await mp.waitForFunction(() => document.querySelector("dc-ctx-sheet").hidden, null, { timeout: 4000 });
    assert(near(rail, 844 * 0.52), `the rail's list sheet is ${rail}px, not 52% of 844`);
    const seen = await eachKind(mp, true, (m, title) => {
      assert(near(m.panel.h, rail), `${title} is ${m.panel.h}px, the rail's sheet ${rail}px: ${brief(m)}`);
      assert(near(m.panel.b, m.editor.b) && near(m.panel.w, m.editor.w), `${title} is no full width bottom sheet: ${brief(m)}`);
      fills(m, `phone, ${title}`);
    });
    return `rail ${Math.round(rail)}, ${seen.join(", ")}`;
  });

  await run("sheet size: from 768px wide every editor sheet is 70% of the editor at its right edge, a tablet and a desktop", async () => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await openClear(page, false);
    const desk = await eachKind(page, false, (m, title) => {
      docked(m, title, "desktop 1440x900");
      fills(m, `desktop, ${title}`);
    });
    const ctx = await browser.newContext({ ignoreHTTPSErrors: true, hasTouch: true, viewport: { width: 834, height: 1194 } });
    try {
      const tp = await ctx.newPage();
      L.wirePage(tp, bag);
      await L.login(tp);
      await openClear(tp, true);
      const tab = await eachKind(tp, true, (m, title) => {
        docked(m, title, "tablet 834x1194");
        fills(m, `tablet, ${title}`);
      });
      return `desktop ${desk.join(", ")}; tablet ${tab.join(", ")}`;
    } finally {
      await ctx.close();
      await page.setViewportSize({ width: 1360, height: 900 });
    }
  });

  // One phone and one desktop pass over the terminal picker: a shell arriving
  // live, the filter down to one row and to none, then the list scrolled.
  const steady = async (p, touch, where) => {
    const open = async () => {
      await press(p, "[data-editor-term-status]", touch);
      await pickerOpen(p);
      await sleep(400);
    };
    await open();
    const base = await measure(p);
    fills(base, where);
    assert(base.filter, `${where}: the picker shows no filter: ${brief(base)}`);
    const same = (m, what) => {
      assert(near(m.panel.t, base.panel.t) && near(m.panel.h, base.panel.h), `${where}: ${what} moved the panel from ${JSON.stringify(base.panel)} to ${brief(m)}`);
      assert(near(m.filter.t, base.filter.t) && near(m.head.t, base.head.t), `${where}: ${what} moved the head or the filter: ${brief(m)}`);
      assert(m.page === 0, `${where}: ${what} scrolled the page by ${m.page}px`);
    };
    const rows = await waitRows(() => true, `${where}: the picker never painted`, p);
    const before = rows.length;
    const query = rows.map((r) => r.name).find((n) => /^size-\d-of-the-sheet$/.test(n));
    assert(query, `${where}: none of the shells made for the list stands in the picker: ${JSON.stringify(rows.map((r) => r.name))}`);
    const live = await elsewhere((p2) => shellOf(p2));
    await waitRows((r) => r.length === before + 1 && r.some((x) => x.pick === live), `${where}: the shell made elsewhere never reached the picker`, p);
    await sleep(300);
    same(await measure(p), "a shell arriving live");
    await p.fill("[data-editor-sheet-filter]", query);
    await waitRows((r) => r.length === 1, `${where}: the filter ${query} did not leave one row`, p);
    await sleep(250);
    const one = await measure(p);
    same(one, "filtering down to one row");
    fills(one, `${where}, one row`);
    assert(near(one.first.t, one.body.t, 2) && one.panel.b - one.last.b > 100, `${where}: the one row does not stand on top with room below it: ${brief(one)}`);
    await p.fill("[data-editor-sheet-filter]", "zz-no-such-terminal");
    await p.waitForFunction(() => !document.querySelector("[data-editor-sheet-empty]").hidden, null, { timeout: 5000 });
    await sleep(250);
    const none = await measure(p);
    same(none, "filtering down to no row");
    assert(none.rows === 0 && none.empty && none.empty.t - none.filter.b <= 12, `${where}: Nothing matches does not stand right under the filter: ${brief(none)}`);
    await p.fill("[data-editor-sheet-filter]", "");
    await waitRows((r) => r.length === before + 1, `${where}: clearing the filter did not bring the rows back`, p);
    await sleep(250);
    const all = await measure(p);
    same(all, "clearing the filter");
    if (all.scroll.height > all.scroll.client + 1) {
      await p.evaluate(() => { const b = document.querySelector("[data-editor-sheet-body]"); b.scrollTop = b.scrollHeight; });
      await sleep(250);
      const down = await measure(p);
      same(down, "scrolling the list");
      assert(down.scroll.top > 0 && near(down.last.b, down.body.b - 8, 2), `${where}: the list did not scroll inside the panel to its last row: ${brief(down)}`);
    }
    await close(p, touch);
    return { rows: all.rows, scrolls: all.scroll.height > all.scroll.client + 1, panel: Math.round(base.panel.h) };
  };

  await run("sheet size: a shell arriving live and the filter down to one row and to none never move the picker's panel, Nothing matches right under the filter, the list scrolls inside, phone and desktop", async () => {
    const dir = await page.getAttribute("dc-editor", "data-editor-project-path");
    const listed = await page.evaluate(async (path) => (await (await fetch(path)).text()).split("data-term-pick=").length - 1, `${new URL(page.url()).pathname}/terminals`);
    for (let i = listed; i < 8; i++) {
      const res = await postAs(page, "/shells/new", { project: dir });
      const id = new URL(res.url).pathname.split("/").pop();
      assert(res.ok && id !== "new", `a shell for the list failed: ${JSON.stringify(res)}`);
      made.add(id);
      assert((await postAs(page, `/shells/${id}/rename`, { name: `size-${i}-of-the-sheet` })).ok, "renaming a shell for the list failed");
    }
    const mp = await mobilePage();
    await mp.setViewportSize({ width: 390, height: 844 });
    await openClear(mp, true);
    const phone = await steady(mp, true, "phone");
    assert(phone.scrolls, `the phone's picker of ${phone.rows} rows does not scroll, the list check ran on nothing`);
    await page.setViewportSize({ width: 1440, height: 900 });
    await openClear(page, false);
    try {
      const desk = await steady(page, false, "desktop");
      return `phone ${phone.panel}px ${phone.rows} rows, desktop ${desk.panel}px ${desk.rows} rows`;
    } finally {
      await page.setViewportSize({ width: 1360, height: 900 });
    }
  });

  await run("sheet size: where the editor is short, a landscape phone, a low phone window and a low desktop window, the sheet stays inside the editor", async () => {
    const mp = await mobilePage();
    const seen = [];
    const inside = async (p, touch, where, want) => {
      await openClear(p, touch);
      await press(p, "[data-editor-term-status]", touch);
      await pickerOpen(p);
      await sleep(400);
      const m = await measure(p);
      assert(m.panel.t >= m.editor.t - 1 && m.panel.b <= m.editor.b + 1, `${where}: the sheet stands outside the editor: ${brief(m)}`);
      assert(near(m.panel.h, want(m)), `${where}: the sheet is ${m.panel.h}px, not ${want(m)}px: ${brief(m)}`);
      seen.push(`${where} ${Math.round(m.panel.h)} of ${Math.round(m.editor.h)}`);
      await close(p, touch);
    };
    try {
      await mp.setViewportSize({ width: 844, height: 390 });
      await inside(mp, true, "landscape phone 844x390", (m) => m.editor.h * 0.7);
      await mp.setViewportSize({ width: 390, height: 420 });
      await inside(mp, true, "low phone 390x420", (m) => Math.min(m.vh * 0.52, m.editor.h * 0.85));
      await page.setViewportSize({ width: 1100, height: 420 });
      await inside(page, false, "low desktop 1100x420", (m) => m.editor.h * 0.7);
    } finally {
      await mp.setViewportSize({ width: 390, height: 844 });
      await page.setViewportSize({ width: 1360, height: 900 });
    }
    return seen.join(", ");
  });
};
