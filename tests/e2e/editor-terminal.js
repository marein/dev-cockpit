const L = require("./lib");
const { assert, sleep, confirmSwal, BASE } = L;

// The editor's terminal picker: the terminals sheet listing the project's
// coders and shells from the fragment GET /projects/:name/editor/terminals
// ([data-term-session] with its marks, the owner's stopped coders as
// [data-term-stopped] views), each a row ([data-term-pick]) that opens its
// terminal as an editor tab in the focused group or brings its tab to the
// front, read "open" while it stands in one, the owner's New shell, New coder
// and stopped coders to resume, never an assistant. A click, a tap or Enter on
// a row opens its menu like a container's in the docker sheet, Open first and
// the terminal's actions after it. There is no dock or panel: the code reaches
// the statusbar on every width, and the way in is the statusbar's plain
// terminal icon, the menu's Open terminal and Ctrl/Cmd+J, from anywhere in the
// editor, a terminal tab included. Button and keys open the same picker: its
// filter takes the keys on a fine pointer with the first row (New shell)
// selected, no row leads back to a file or a terminal used last, and Escape
// gives the focus back. Beside the terminal icon stands the rail's sparkle, which opens
// the assistant picker like Ctrl/Cmd+I, a key CodeMirror (select parent) and
// the terminal (Tab) never see; the Tab key itself still reaches the terminal.
// ?terminal=<id> on the editor's address, the landing of a New coder whose
// form carries panel=1, opens that terminal as a tab and leaves the address,
// &tab=1 with it; a gone id stays quiet. The statusbar's buttons wear the
// focus ring reached with Tab and none after a click or a tap.
// The keys of the terminal panel this replaced (dc-editor-term-open/-active/
// -height) are ignored. The probes launch Chromium with blink settings for
// each pointer setup.
// Gotchas: headless renders xterm on canvas, output is read from the
// .attach-selection mirror inside the tab's view; the swal prompt of a rename
// is filled through its input field; a click within 350 ms after a context
// menu closed is swallowed, the checks wait; the coder round trip needs a
// coder CLI on the host like coder-claude.js (the form preselects copilot
// here).

L.runFeature("EDITOR-TERMINAL", async ({ engine, browser, page, run, mobilePage, bag }) => {
  const tag = `edt-${engine}-${Date.now().toString(36)}`;
  const project = `zzet-${tag}`;
  const editorURL = `${BASE}/projects/${project}/editor`;
  const made = new Set();
  const postAs = (p, path, body, { json = false } = {}) => p.evaluate(async ({ path, body, json }) => {
    const token = document.querySelector('meta[name="csrf-token"]')?.content || "";
    const res = await fetch(path, {
      method: "POST",
      headers: { "X-CSRF-Token": token, Accept: "application/json", ...(json ? { "Content-Type": "application/json" } : {}) },
      body: json ? JSON.stringify(body) : new URLSearchParams(body),
    });
    return { ok: res.ok, status: res.status, url: res.url };
  }, { path, body, json });
  const shellOf = async (p) => {
    const url = await L.createShell(p, project);
    const id = new URL(url).pathname.split("/").pop();
    made.add(id);
    return id;
  };
  const elsewhere = async (fn) => {
    const p2 = await page.context().newPage();
    L.wirePage(p2, bag);
    try {
      return await fn(p2);
    } finally {
      await p2.close().catch(() => {});
    }
  };
  const openEditor = async (p = page, query = "") => {
    const loaded = p.waitForResponse((r) => new URL(r.url()).pathname === `/projects/${project}/editor/terminals`, { timeout: 20000 }).catch(() => null);
    await p.goto(`${editorURL}${query}`, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(p);
    await p.waitForSelector(".cm-editor, .editor-textarea", { state: "attached", timeout: 20000 });
    await loaded;
    await sleep(500);
  };
  const shown = (p, sel) => p.evaluate((s) => {
    const el = document.querySelector(s);
    if (!el) return false;
    const r = el.getBoundingClientRect();
    return r.width > 0 && r.height > 0 && getComputedStyle(el).visibility !== "hidden" && !!el.offsetParent;
  }, sel);
  const box = (p, sel) => p.evaluate((s) => {
    const el = document.querySelector(s);
    if (!el) return null;
    const r = el.getBoundingClientRect();
    return { x: Math.round(r.x), y: Math.round(r.y), w: Math.round(r.width), h: Math.round(r.height), b: Math.round(r.bottom), r: Math.round(r.right) };
  }, sel);
  const rowSel = (id) => `[data-editor-sheet-body] [data-term-pick="${id}"]`;
  const tabSel = (id) => `.editor-tab[data-path="//terminal/${id}"]`;
  const boxSel = (id) => `.editor-term-box:has([data-terminal-view="${id}"])`;
  const mounted = (p, id) => p.waitForSelector(`${boxSel(id)} terminal-attach[embedded] .xterm-screen canvas`, { timeout: 20000 });
  const mirror = (p, id) => p.evaluate((s) => document.querySelector(`${s} .attach-selection`)?.textContent || "", boxSel(id));
  const waitText = async (p, id, needle) => {
    for (let t = 0; t < 12000; t += 250) {
      if ((await mirror(p, id)).includes(needle)) return;
      await sleep(250);
    }
    throw new Error(`the tab of ${id} never showed ${needle}`);
  };
  const front = (p = page) => p.evaluate(() => {
    const el = document.activeElement;
    return {
      tab: document.querySelector(".editor-group-focused .editor-tab.active")?.dataset.path || "",
      cm: !!el?.closest(".cm-editor"),
      term: el?.closest("[data-terminal-view]")?.getAttribute("data-terminal-view") || "",
      sheet: !document.querySelector("[data-editor-sheet]").hidden,
    };
  });
  const pickRows = (p = page) => p.evaluate(() => [...document.querySelectorAll("[data-editor-sheet-body] .dropdown-item")]
    .filter((el) => el.getBoundingClientRect().height > 0)
    .map((el) => ({
      pick: el.dataset.termPick || "",
      marked: !!el.closest(".selected") || el.classList.contains("selected"),
      text: el.textContent.replace(/\s+/g, " ").trim(),
      name: el.querySelector(".dc-menu-label-head")?.textContent.trim() || "",
      icon: el.querySelector(".dc-term-icon")?.className || "",
      h: Math.round(el.getBoundingClientRect().height),
      w: Math.round(el.getBoundingClientRect().width),
    })));
  const topRow = (p = page) => p.evaluate(() => {
    const body = document.querySelector("[data-editor-sheet-body]");
    const el = [...body.querySelectorAll(".dropdown-item")].find((x) => x.getBoundingClientRect().height > 0);
    const r = el.getBoundingClientRect();
    const b = body.getBoundingClientRect();
    return {
      top: Math.round(body.scrollTop),
      text: el.textContent.replace(/\s+/g, " ").trim(),
      marked: !!el.closest(".selected") || el.classList.contains("selected"),
      seen: r.top >= b.top - 1 && r.bottom <= b.bottom + 1,
    };
  });
  const pickerOpen = (p = page) => p.waitForFunction(() => {
    const sheet = document.querySelector("[data-editor-sheet]");
    return sheet && !sheet.hidden && document.querySelector("[data-editor-sheet-title]").textContent.startsWith("Terminals")
      && !!document.querySelector("[data-editor-sheet-body] .dropdown-item");
  }, null, { timeout: 8000 });
  // No picker row leads back to the file, terminal or assistant used last.
  const noJump = (rows) => !rows.some((r) => /\bLast (file|terminal|assistant)\b/.test(r.text));
  const pickerClosed = (p = page) => p.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 5000 });
  const asstPickerOpen = (p = page) => p.waitForFunction(() => {
    const sheet = document.querySelector("[data-editor-sheet]");
    return sheet && !sheet.hidden && document.querySelector("[data-editor-sheet-title]").textContent === "Assistants"
      && !!document.querySelector("[data-editor-sheet-body] .dropdown-item");
  }, null, { timeout: 8000 });
  // The statusbar's two icons as they stand: each button's visible parts, so
  // a count, a dot or a ring on either shows up as a part it should not have.
  const statusIcons = (p = page) => p.evaluate(() => {
    const vis = (el) => { const r = el.getBoundingClientRect(); const c = getComputedStyle(el); return r.width > 0 && r.height > 0 && c.display !== "none" && c.visibility !== "hidden"; };
    const parts = (b) => [...b.querySelectorAll("*")].filter(vis).map((el) => [el.tagName.toLowerCase(), ...el.classList].join("."));
    const at = (el) => { const r = el.getBoundingClientRect(); return { l: Math.round(r.left), r: Math.round(r.right), cy: Math.round(r.top + r.height / 2), b: Math.round(r.bottom) }; };
    const read = (b, icon) => b && vis(b) ? { box: at(b.querySelector(icon)), parts: parts(b), text: b.textContent.trim(), title: b.title, label: b.getAttribute("aria-label") } : null;
    return { term: read(document.querySelector("[data-editor-term-status]"), "i"), asst: read(document.querySelector("[data-editor-asst-status]"), "i"), h: innerHeight };
  });
  // Every entry of the statusbar on at once, the way a project with a long
  // branch, a change request with its pipeline, containers, an index running,
  // a read only file and a diff shows them. The entries are set in place, the
  // fit that follows is the editor's own; read two frames later.
  const fullBar = (p, { readOnly = false } = {}) => p.evaluate(async (readOnly) => {
    const q = (sel) => document.querySelector(sel);
    q("[data-editor-status]").textContent = "Diff against c2ea522ae1f0b9e4d3c2";
    q("[data-editor-lsp-index]").hidden = false;
    q("[data-editor-lsp-index-label]").textContent = "Indexing gopls…";
    q("[data-editor-git-status]").hidden = false;
    q("[data-editor-git-branch]").textContent = "replace-htmx-with-pe-and-a-long-branch-name";
    q("[data-editor-git-ab]").hidden = false;
    q("[data-editor-git-ab]").textContent = "↑3 ↓1";
    const forge = q("[data-editor-forge-status]");
    forge.hidden = false;
    forge.innerHTML = '<i class="ti ti-brand-github dc-ink-icon" aria-hidden="true"></i><span class="dc-ink-cap" data-statusbar-text>#316</span><i class="ti ti-circle-check text-green dc-ink-icon" aria-hidden="true"></i>';
    q("[data-editor-docker-status]").hidden = false;
    q("[data-editor-docker-status-text]").hidden = false;
    q("[data-editor-docker-status-text]").textContent = "20/21";
    q("[data-editor-readonly]").hidden = !readOnly;
    q("[data-editor-readonly-path]").textContent = "/usr/local/go/src/net/http/server-with-a-long-name.go";
    q("[data-editor-pos]").hidden = false;
    q("[data-editor-pos]").textContent = "1:1";
    await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
    const bar = q("[data-editor-statusbar]");
    const box = bar.getBoundingClientRect();
    const vis = (el) => { const r = el.getBoundingClientRect(); const c = getComputedStyle(el); return r.width > 0 && r.height > 0 && c.display !== "none" && c.visibility !== "hidden"; };
    const entries = [...bar.children].filter(vis).map((el) => { const r = el.getBoundingClientRect(); return { l: r.left, r: r.right, w: r.width, h: r.height, control: el.tagName === "BUTTON" }; });
    // The gaps between the entries stand between what each shows, the icons,
    // the bar, the texts and the position; the message is elastic and left out.
    const content = (entry) => {
      const parts = [...entry.querySelectorAll("i.ti, .progress, [data-statusbar-text]")].filter(vis).map((el) => el.getBoundingClientRect());
      if (!parts.length) {
        const range = document.createRange();
        range.selectNodeContents(entry);
        parts.push(range.getBoundingClientRect());
      }
      return { l: Math.min(...parts.map((r) => r.left)), r: Math.max(...parts.map((r) => r.right)) };
    };
    const entryEls = [...bar.children].filter(vis).filter((el) => !el.hasAttribute("data-editor-status"));
    const shown = entryEls.map(content);
    const gaps = shown.slice(1).map((c, i) => Math.round((c.l - shown[i].r) * 10) / 10);
    // No entry keeps room for a text it does not show: its box is what it
    // shows plus its own padding.
    const slack = entryEls.map((el, i) => {
      const c = getComputedStyle(el);
      return Math.round((el.getBoundingClientRect().width - (shown[i].r - shown[i].l) - parseFloat(c.paddingLeft) - parseFloat(c.paddingRight)) * 10) / 10;
    });
    const icons = [...bar.querySelectorAll("i.ti, .dc-term-icon, .progress")].filter(vis).map((el) => {
      const r = el.getBoundingClientRect();
      const e = el.closest("[data-editor-statusbar] > *").getBoundingClientRect();
      return { w: Math.round(r.width), whole: r.width >= 10 && r.left >= e.left - 0.5 && r.right <= e.right + 0.5 && r.left >= box.left && r.right <= box.right };
    });
    return {
      overlap: entries.some((a, i) => entries.some((b, j) => j > i && a.r > b.l + 0.5 && b.r > a.l + 0.5)),
      inside: entries.every((e) => e.l >= box.left - 0.5 && e.r <= box.right + 0.5) && bar.scrollWidth <= bar.clientWidth + 1,
      tappable: entries.filter((e) => e.control).every((e) => e.w >= 18 && e.h >= 18),
      icons,
      texts: [...bar.querySelectorAll("[data-statusbar-text]")].filter((el) => el.closest("[data-editor-readonly]") ? readOnly : true).map(vis),
      pos: vis(q("[data-editor-pos]")) && q("[data-editor-pos]").textContent === "1:1",
      gaps,
      slack,
      even: gaps.length >= 5 && Math.max(...gaps) - Math.min(...gaps) <= 1 && slack.every((v) => Math.abs(v) <= 1),
    };
  }, readOnly);
  const plainIcons = (got) => got.term && got.asst && got.term.text === "" && got.asst.text === ""
    && JSON.stringify(got.term.parts) === JSON.stringify(["i.ti.ti-terminal-2"])
    && JSON.stringify(got.asst.parts) === JSON.stringify(["i.ti.ti-sparkles"])
    && Math.abs(got.term.box.cy - got.asst.box.cy) <= 1 && got.asst.box.l >= got.term.box.r && got.asst.box.l - got.term.box.r <= 32
    && got.asst.box.b <= got.h;
  // The sparkle in the statusbar against the rail's (the tab bar's on a
  // phone) and the terminal icon beside it: the rail's purple without its
  // disc, the terminal icon's size and line.
  const sparkles = (p = page) => p.evaluate(() => {
    const rgb = (c) => {
      const n = (c.match(/[\d.]+/g) || []).slice(0, 3).map(Number);
      return (c.startsWith("color(") ? n.map((v) => Math.round(v * 255)) : n).join(",");
    };
    const rail = [...document.querySelectorAll(".dc-rail .dc-term-icon.assistant, .dc-tabbar .dc-term-icon.assistant")].find((el) => el.getBoundingClientRect().width > 0);
    const button = document.querySelector("[data-editor-asst-status]");
    const glyph = button.querySelector("i");
    const term = document.querySelector("[data-editor-term-status] i");
    const g = glyph.getBoundingClientRect();
    const t = term.getBoundingClientRect();
    return {
      rail: rail ? rgb(getComputedStyle(rail).color) : "",
      color: rgb(getComputedStyle(glyph).color),
      glyph: glyph.className,
      disc: !!button.querySelector(".dc-term-icon") || getComputedStyle(button).backgroundColor !== "rgba(0, 0, 0, 0)" || getComputedStyle(glyph).backgroundColor !== "rgba(0, 0, 0, 0)",
      size: [Math.round(g.width), Math.round(g.height), getComputedStyle(glyph).fontSize].join(),
      termSize: [Math.round(t.width), Math.round(t.height), getComputedStyle(term).fontSize].join(),
      line: Math.abs((g.top + g.bottom) / 2 - (t.top + t.bottom) / 2),
    };
  });
  const plainSparkle = (l) => !!l.rail && l.color === l.rail && /\bti-sparkles\b/.test(l.glyph) && !l.disc && l.size === l.termSize && l.line <= 1;
  const byButton = async (p = page) => {
    await p.click("[data-editor-term-status]");
    await pickerOpen(p);
  };
  const waitRows = async (want, what, p = page) => {
    let got = [];
    for (let i = 0; i < 40; i++) {
      got = (await pickRows(p)).filter((r) => r.pick);
      if (want(got)) return got;
      await sleep(250);
    }
    throw new Error(`${what}: the picker lists ${JSON.stringify(got.map((r) => r.text))}`);
  };
  const clickAction = (p, label) => p.locator("[data-editor-sheet-body] .dropdown-item", { hasText: new RegExp(`^${label}$`) }).first().click();
  const noDock = (p = page) => p.evaluate(() => {
    const groups = document.querySelector("[data-editor-groups]").getBoundingClientRect();
    const bar = document.querySelector(".editor-statusbar").getBoundingClientRect();
    const at = document.elementFromPoint(groups.left + groups.width / 2, bar.top - 4);
    return {
      gone: !document.querySelector("[data-editor-dock], [data-editor-dock-host], [data-editor-dock-plus], .editor-dock, [data-term-chip]"),
      meets: Math.abs(groups.bottom - bar.top) <= 1,
      code: !!at?.closest("[data-editor-groups], .editor-tree-col"),
      bar: bar.bottom <= innerHeight && bar.height > 0,
    };
  });
  const menuOf = async (p, sel) => {
    await p.click(sel);
    await p.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
    return p.$$eval(".dc-context-menu .dropdown-item", (els) => els.map((el) => (el.querySelector(".dc-menu-label-head") || el).textContent.trim()).filter(Boolean));
  };
  // The menu a row opens, read the way the docker and git sheets' rows are:
  // where it stands against the point that opened it.
  const menuAt = (p) => p.evaluate(() => {
    const m = document.querySelector(".dc-context-menu");
    if (!m) return null;
    const r = m.getBoundingClientRect();
    return { l: r.left, t: r.top, r: r.right, b: r.bottom, cls: m.className, labels: [...m.querySelectorAll(".dropdown-item")].map((el) => (el.querySelector(".dc-menu-label-head") || el).textContent.trim()) };
  });
  const menuClick = (p, label, last = false) => p.evaluate(([l, fromEnd]) => {
    const items = [...document.querySelectorAll(".dc-context-menu .dropdown-item")];
    const item = (fromEnd ? items.reverse() : items).find((el) => (el.querySelector(".dc-menu-label-head") || el).textContent.trim() === l);
    if (!item) throw new Error(`no menu entry ${l}`);
    item.click();
  }, [label, last]);
  // From the picker's filter the arrows walk to a terminal's row, Enter opens
  // its menu with Open marked, a second Enter takes it.
  const pickByKeys = async (p, id) => {
    const on = () => p.evaluate((id) => document.activeElement?.dataset?.termPick === id, id);
    for (let i = 0; i < 12 && !(await on()); i++) {
      await p.keyboard.press("ArrowDown");
      await sleep(80);
    }
    assert(await on(), `the arrows never reached the row of ${id}`);
    await p.keyboard.press("Enter");
    await p.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
    await p.keyboard.press("Enter");
  };
  const closeMenu = async (p) => {
    await p.keyboard.press("Escape");
    await p.waitForSelector(".dc-context-menu", { state: "detached", timeout: 4000 }).catch(() => {});
  };
  // The statusbar's buttons and what each draws around itself, read once
  // their transition settled: a focus ring is a shadow at no offset with a
  // spread, or an outline, in a colour that shows.
  const rings = async (p = page) => {
    await sleep(450);
    return p.evaluate(() => {
      const shows = (c) => !/transparent|rgba\(0, 0, 0, 0\)|\/ 0\)\s*$/.test(c.trim());
      const ringed = (b) => {
        const c = getComputedStyle(b);
        const spread = (c.boxShadow === "none" ? [] : c.boxShadow.split(/,(?![^(]*\))/)).some((layer) => {
          const m = layer.match(/(-?[\d.]+)px (-?[\d.]+)px ([\d.]+)px (-?[\d.]+)px/);
          return !!m && +m[1] === 0 && +m[2] === 0 && +m[4] > 0 && shows(layer.replace(m[0], ""));
        });
        return spread || (c.outlineStyle !== "none" && parseFloat(c.outlineWidth) > 0 && shows(c.outlineColor));
      };
      return [...document.querySelectorAll("[data-editor-statusbar] > button")].filter((b) => b.getBoundingClientRect().width > 0)
        .map((b) => ({ name: b.getAttributeNames().find((n) => n.startsWith("data-editor-")), focused: b === document.activeElement, ring: ringed(b) }));
    });
  };
  const ringedOnes = (list) => list.filter((b) => b.ring).map((b) => b.name);
  const sheetUp = (p = page) => p.waitForFunction(() => !document.querySelector("[data-editor-sheet]").hidden, null, { timeout: 8000 });
  const sheetGone = (p = page) => p.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 5000 });
  const errorToasts = (p = page) => p.evaluate(() => [...document.querySelectorAll("[data-dc-toasts] .toast")]
    .filter((t) => t.querySelector(".text-danger")).map((t) => t.textContent.replace(/\s+/g, " ").trim()));

  let first = "";
  let second = "";
  let coderId = "";
  try {
    await L.createProject(page, project);
    await openEditor();

    await run("desktop: no dock or panel, the code reaches the statusbar, whose terminal icon and the menu's Open terminal open the picker, which lists no assistant", async () => {
      const geo = await noDock();
      assert(geo.gone && geo.meets && geo.code && geo.bar, `something stands between the code and the statusbar: ${JSON.stringify(geo)}`);
      assert(!(await page.$("[data-editor-term-panel], [data-editor-term-item], [data-editor-term-count]")), "the terminal panel, its menu entry or the count is still on the page");
      assert(await shown(page, "[data-editor-term-status]"), "the statusbar's terminal icon is missing");
      await byButton();
      const rows = await pickRows();
      assert(await page.textContent("[data-editor-sheet-title]") === "Terminals", "the picker is not titled Terminals");
      assert(rows[0]?.text === "New shell" && rows[1]?.text === "New coder" && rows.some((r) => /No terminal running/.test(r.text)),
        `the picker of a project without terminals reads ${JSON.stringify(rows.map((r) => r.text))}`);
      assert(!rows.some((r) => /assistant/i.test(r.text)) && !(await page.$("[data-editor-sheet-body] [data-asst-pick]")), `the terminal picker offers assistants: ${JSON.stringify(rows.map((r) => r.text))}`);
      await page.keyboard.press("Escape");
      await pickerClosed();
      await page.click("[data-editor-menu]");
      await page.waitForSelector("[data-editor-menu-list].show", { timeout: 5000 });
      await page.click("[data-editor-term-open-item]");
      await pickerOpen();
      await page.keyboard.press("Escape");
      await pickerClosed();
    });

    await run("desktop: the statusbar shows two plain icons side by side, the sparkles in the rail's purple without its disc at the terminal icon's size and line in light and dark, each naming its picker and key", async () => {
      const got = await statusIcons();
      assert(plainIcons(got), `the statusbar's icons read ${JSON.stringify(got)}`);
      assert(got.term.title === "Terminals (Ctrl+J)" && got.term.label === "Terminals", `the terminal icon is named ${got.term.title} / ${got.term.label}`);
      assert(got.asst.title === "Assistants (Ctrl+I)" && got.asst.label === "Assistants", `the sparkles are named ${got.asst.title} / ${got.asst.label}`);
      const looks = [];
      for (const scheme of ["light", "dark"]) {
        await page.emulateMedia({ colorScheme: scheme });
        await page.waitForFunction((s) => document.documentElement.getAttribute("data-bs-theme") === s, scheme, { timeout: 4000 });
        await sleep(200);
        const look = await sparkles();
        assert(plainSparkle(look), `in ${scheme} the statusbar's sparkles read ${JSON.stringify(look)}`);
        looks.push(`${scheme} rgb(${look.color})`);
      }
      await page.emulateMedia({ colorScheme: null });
      await page.click("[data-editor-asst-status]");
      await asstPickerOpen();
      const rows = await pickRows();
      assert(!(await page.$("[data-editor-sheet-body] [data-term-pick]")) && !rows.some((r) => /New shell|New coder|No terminal/.test(r.text)) && rows.some((r) => /^New assistant/.test(r.text)),
        `the assistant picker reads ${JSON.stringify(rows.map((r) => r.text))}`);
      await page.keyboard.press("Escape");
      await pickerClosed();
      await page.click("[data-editor-menu]");
      await page.waitForSelector("[data-editor-menu-list].show", { timeout: 5000 });
      await page.click("[data-editor-asst-open-item]");
      await asstPickerOpen();
      await page.keyboard.press("Escape");
      await pickerClosed();
      return looks.join(", ");
    });

    await run("desktop: the picker's New shell opens the shell as a tab and its row reads open, the statusbar's icons stay plain", async () => {
      await byButton();
      const created = page.waitForResponse((r) => new URL(r.url()).pathname === "/shells/new" && r.request().method() === "POST", { timeout: 15000 });
      await clickAction(page, "New shell");
      await created;
      await page.waitForSelector('.editor-group-focused .editor-tab.active[data-path^="//terminal/"]', { timeout: 10000 });
      first = (await page.getAttribute('.editor-group-focused .editor-tab.active[data-path^="//terminal/"]', "data-path")).replace("//terminal/", "");
      made.add(first);
      await mounted(page, first);
      await byButton();
      const got = await waitRows((r) => r.length === 1, "the new shell never reached the picker");
      assert(got[0].pick === first && / open$/.test(got[0].text) && /running/.test(got[0].icon), `the shell's row reads ${JSON.stringify(got[0])}`);
      await page.keyboard.press("Escape");
      await pickerClosed();
      const icons = await statusIcons();
      assert(plainIcons(icons), `a running shell put something on the statusbar's icons: ${JSON.stringify(icons)}`);
    });

    await run("desktop: reached with Tab every statusbar button shows its focus ring, a click shows none, also once its picker or sheet closed from the pointer", async () => {
      await openEditor();
      await fullBar(page);
      const order = ["data-editor-git-status", "data-editor-forge-status", "data-editor-docker-status", "data-editor-term-status", "data-editor-asst-status"];
      const onlyRing = async (key, name) => {
        await page.keyboard.press(key);
        const got = await rings();
        const at = got.find((b) => b.focused);
        assert(at?.name === name && at.ring && ringedOnes(got).length === 1, `${key} onto ${name} reads ${JSON.stringify(got)}`);
      };
      await page.focus("[data-editor-git-status]");
      for (const name of order.slice(1)) await onlyRing("Tab", name);
      for (const name of order.slice(0, -1).reverse()) await onlyRing("Shift+Tab", name);
      await openEditor();
      const backdrop = async () => {
        const at = await box(page, "[data-editor-sheet]");
        await page.mouse.click(at.x + 12, at.y + 12);
        await sheetGone();
      };
      const none = async (how) => {
        const got = await rings();
        assert(!ringedOnes(got).length, `${how} leaves a ring: ${JSON.stringify(got)}`);
      };
      for (const [btn, open] of [["[data-editor-term-status]", pickerOpen], ["[data-editor-asst-status]", asstPickerOpen], ["[data-editor-git-status]", sheetUp]]) {
        await page.click(btn);
        await open();
        await none(`a click on ${btn} with its sheet standing`);
        await backdrop();
        await none(`${btn}'s sheet closed by a click beside it`);
        await sleep(400);
        await page.click(btn);
        await open();
        await page.click("[data-editor-sheet-close]");
        await sheetGone();
        await none(`${btn}'s sheet closed by its X`);
        await sleep(400);
      }
      await byButton();
      await L.openPicked(page, rowSel(first));
      await page.waitForSelector(`${tabSel(first)}.active`, { timeout: 8000 });
      await pickerClosed();
      await none("a picker row's Open");
      await page.focus("[data-editor-git-status]");
      await page.keyboard.press("Tab");
      await page.keyboard.press("Enter");
      await pickerOpen();
      await backdrop();
      await none("a picker opened by its key and closed by a click");
    });

    await run("desktop: typing reaches the shell in its tab and the editor's own keys stay away from it", async () => {
      const marker = `ETP${tag.slice(-4)}`;
      await sleep(800);
      const r = await box(page, `${boxSel(first)} terminal-attach`);
      await page.mouse.click(r.x + r.w / 2, r.y + r.h / 2);
      await page.keyboard.type(`echo ${marker}`);
      await page.keyboard.press("Enter");
      await waitText(page, first, marker);
      for (const key of ["Control+o", "Control+s"]) {
        await page.keyboard.press(key);
        await sleep(400);
        assert(await page.evaluate(() => document.querySelector("[data-editor-quickopen]").hidden), `${key} opened the quick open from inside the terminal`);
      }
      await page.keyboard.press("Control+c");
      assert((await postAs(page, `/shells/${first}/input`, { items: [{ prompt: "printf 'one\\ntwo\\n' > notes.txt" }] }, { json: true })).ok, "writing notes.txt failed");
      await sleep(800);
    });

    await run("desktop: Ctrl+J opens the picker from a file, from a terminal tab and over the quick open with New shell selected and no row leading back, the arrows and Enter open a terminal through its menu, Escape gives the focus back", async () => {
      await openEditor(page, "?file=notes.txt");
      await page.waitForSelector('.editor-group-focused .editor-tab.active[data-path="notes.txt"]', { timeout: 10000 });
      await page.click(".cm-content");
      let at = await front();
      assert(at.cm && at.tab === "notes.txt", `the file does not hold the keys: ${JSON.stringify(at)}`);
      await page.keyboard.press("Control+j");
      await pickerOpen();
      let rows = await pickRows();
      assert(noJump(rows) && !(await page.$("[data-editor-sheet-body] [data-term-jump]")), `from a file a row leads back: ${JSON.stringify(rows.map((r) => r.text))}`);
      assert(rows[0].text === "New shell" && rows[0].marked && rows[1].text === "New coder" && rows.find((r) => r.pick)?.pick === first && rows.filter((r) => r.marked).length === 1, `from a file the picker reads ${JSON.stringify(rows)}`);
      await page.keyboard.press("Escape");
      await pickerClosed();
      at = await front();
      assert(at.cm && !at.sheet, `Escape did not give the file its focus back: ${JSON.stringify(at)}`);
      await page.keyboard.press("Control+j");
      await pickerOpen();
      await pickByKeys(page, first);
      await page.waitForSelector(`${tabSel(first)}.active`, { timeout: 8000 });
      await page.waitForFunction((id) => !!document.activeElement?.closest(`[data-terminal-view="${id}"]`), first, { timeout: 8000 });
      await page.keyboard.press("Control+j");
      await pickerOpen();
      rows = await pickRows();
      assert(noJump(rows) && rows[0].marked && rows.find((r) => r.pick)?.pick === first && / open$/.test(rows.find((r) => r.pick).text), `from a terminal the picker reads ${JSON.stringify(rows.map((r) => r.text))}`);
      await page.keyboard.press("Escape");
      await pickerClosed();
      at = await front();
      assert(at.term === first, `Escape did not give the terminal its focus back: ${JSON.stringify(at)}`);
      await page.keyboard.type("echo back-in-term");
      await page.keyboard.press("Enter");
      await waitText(page, first, "back-in-term");
      await page.keyboard.press("Control+j");
      await pickerOpen();
      await page.keyboard.press("Control+j");
      await pickerClosed();
      at = await front();
      assert(at.term === first, `Ctrl+J closing the picker left the keys at ${JSON.stringify(at)}`);
      await page.click('.editor-tab[data-path="notes.txt"]');
      await page.click(".cm-content");
      await page.keyboard.press("Control+Shift+O");
      await page.waitForSelector("[data-editor-quickopen]:not([hidden])", { timeout: 5000 });
      await page.keyboard.press("Control+j");
      await pickerOpen();
      assert(await page.evaluate(() => document.querySelector("[data-editor-quickopen]").hidden), "the quick open stayed open under the picker");
      await page.keyboard.press("Escape");
      await pickerClosed();
      at = await front();
      assert(at.cm, `Escape on the picker opened over the quick open left the keys at ${JSON.stringify(at)}`);
      return "file and terminal, Escape back to each, the quick open giving way";
    });

    await run("desktop: Ctrl+I and Cmd+I open the assistant picker from the code, which keeps its selection, and from a terminal, where Tab still completes; J and I switch and close the pickers", async () => {
      const stamp = tag.slice(-4);
      assert((await postAs(page, `/shells/${first}/input`, { items: [{ prompt: `printf '%s\\n' "$((6*7))TAB${stamp}" > zztab${stamp}.txt` }] }, { json: true })).ok, "writing the tab probe failed");
      await page.click('.editor-tab[data-path="notes.txt"]');
      await page.click(".cm-content");
      await page.keyboard.press("Control+Home");
      await page.keyboard.press("ArrowRight");
      const where = () => page.evaluate(() => ({ pos: document.querySelector("[data-editor-pos]").textContent.trim(), sel: document.getSelection().toString(), cm: !!document.activeElement?.closest(".cm-editor") }));
      const before = await where();
      for (const key of ["Control+i", "Meta+i"]) {
        await page.keyboard.press(key);
        await asstPickerOpen();
        assert(!(await page.$("[data-editor-sheet-body] [data-term-pick]")) && noJump(await pickRows()), `${key} opened a picker with terminals or a row leading back`);
        await page.keyboard.press("Escape");
        await pickerClosed();
        const after = await where();
        assert(after.cm && after.pos === before.pos && after.sel === "", `${key} reached the code: ${JSON.stringify({ before, after })}`);
      }
      await page.keyboard.press("Control+i");
      await asstPickerOpen();
      await page.keyboard.press("Control+j");
      await pickerOpen();
      await page.keyboard.press("Control+i");
      await asstPickerOpen();
      await page.keyboard.press("Control+i");
      await pickerClosed();
      assert((await where()).cm, "closing the assistant picker with its key left the code without the keys");
      await page.click(tabSel(first));
      await page.waitForFunction((id) => !!document.activeElement?.closest(`[data-terminal-view="${id}"]`), first, { timeout: 8000 });
      await page.keyboard.type(`cat zztab${stamp}`);
      await page.keyboard.press("Control+i");
      await asstPickerOpen();
      await page.keyboard.press("Escape");
      await pickerClosed();
      const at = await front();
      assert(at.term === first, `Escape on the assistant picker did not give the terminal its focus back: ${JSON.stringify(at)}`);
      await page.keyboard.press("Enter");
      await waitText(page, first, `zztab${stamp}: No such file`);
      assert(!(await mirror(page, first)).includes(`42TAB${stamp}`), "Ctrl+I reached the terminal as a Tab");
      await page.keyboard.type(`cat zztab${stamp.slice(0, 2)}`);
      await page.keyboard.press("Tab");
      await sleep(300);
      await page.keyboard.press("Enter");
      await waitText(page, first, `42TAB${stamp}`);
      return "Ctrl+I and Cmd+I from the code and the terminal, Tab completing";
    });

    await run("desktop: the statusbar button opens the picker with the keys of Ctrl+J: the filter with New shell selected, no row leading back, the arrows and Enter opening a terminal through its menu, Escape giving the focus back", async () => {
      await page.click('.editor-tab[data-path="notes.txt"]');
      await page.click(".cm-content");
      const filtered = () => page.evaluate(() => document.activeElement === document.querySelector("[data-editor-sheet-filter]"));
      await byButton();
      let rows = await pickRows();
      assert(await filtered(), "the button's picker does not give its filter the keys");
      assert(noJump(rows) && rows[0].marked && rows.find((r) => r.pick)?.pick === first, `from a file the button's picker reads ${JSON.stringify(rows.map((r) => r.text))}`);
      await pickByKeys(page, first);
      await page.waitForSelector(`${tabSel(first)}.active`, { timeout: 8000 });
      await page.waitForFunction((id) => !!document.activeElement?.closest(`[data-terminal-view="${id}"]`), first, { timeout: 8000 });
      await byButton();
      rows = await pickRows();
      assert(await filtered(), "the button's picker over a terminal does not give its filter the keys");
      assert(noJump(rows) && rows.find((r) => r.pick)?.pick === first, `from a terminal the button's picker reads ${JSON.stringify(rows.map((r) => r.text))}`);
      await page.keyboard.press("Escape");
      await pickerClosed();
      const at = await front();
      assert(at.term === first, `Escape on the button's picker did not give the terminal its focus back: ${JSON.stringify(at)}`);
      await page.keyboard.type("echo via-button");
      await page.keyboard.press("Enter");
      await waitText(page, first, "via-button");
    });

    await run("desktop: a shell started elsewhere reaches the open picker live, a rename elsewhere follows, its row opens its tab once", async () => {
      await page.keyboard.press("Control+j");
      await pickerOpen();
      second = await elsewhere((p2) => shellOf(p2));
      let got = await waitRows((r) => r.some((x) => x.pick === second), "the second shell never reached the picker");
      const a = got.find((x) => x.pick === first);
      const b = got.find((x) => x.pick === second);
      assert(/ open$/.test(a.text) && !/ open$/.test(b.text) && /running/.test(b.icon), `the rows read ${JSON.stringify(got)}`);
      await elsewhere(async (p2) => {
        await p2.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
        assert((await postAs(p2, `/shells/${second}/rename`, { name: `far-${tag.slice(-4)}` })).ok, "the rename elsewhere failed");
      });
      got = await waitRows((r) => r.find((x) => x.pick === second)?.name === `far-${tag.slice(-4)}`, "the rename never reached the picker");
      await L.openPicked(page, rowSel(second));
      await pickerClosed();
      await page.waitForSelector(`${tabSel(second)}.active`, { timeout: 8000 });
      await mounted(page, second);
      await byButton();
      got = await waitRows((r) => / open$/.test(r.find((x) => x.pick === second)?.text || ""), "the picker does not read the second shell open");
      await L.openPicked(page, rowSel(second));
      await pickerClosed();
      await sleep(400);
      assert((await page.locator(tabSel(second)).count()) === 1, "a second pick made a second tab");
    });

    await run("desktop: a click on a picker row opens its menu at the click like a docker row, Open first, the picker standing and Escape closing the menu alone; it carries the terminal's actions and renames the shell", async () => {
      await byButton();
      const row = await box(page, rowSel(second));
      assert(!(await page.$("[data-editor-sheet-body] .terminal-tab-menu")), "a picker row still carries dots");
      const at = { x: row.x + row.w * 0.6, y: row.y + row.h / 2 };
      await page.mouse.click(at.x, at.y);
      await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
      const menu = await menuAt(page);
      assert(menu.labels[0] === "Open" && menu.labels.includes("Rename"), `the row's menu reads ${menu.labels.join(", ")}`);
      assert(at.x >= menu.l - 4 && at.x <= menu.r + 4 && at.y >= menu.t - 4 && at.y <= menu.b + 4, `the row's menu does not stand at the click: ${JSON.stringify({ at, menu })}`);
      assert(!(await page.evaluate(() => document.querySelector("[data-editor-sheet]").hidden)), "the row's click closed the picker");
      await closeMenu(page);
      assert(!(await page.$(".dc-context-menu")) && !(await page.evaluate(() => document.querySelector("[data-editor-sheet]").hidden)), "Escape on the row's menu left it or took the picker");
      await sleep(400);
      const labels = await menuOf(page, rowSel(second));
      for (const want of ["Open", "Open terminal page", "Rename", "Open project", "Delete"]) {
        assert(labels.includes(want), `the row's menu misses ${want}: ${labels.join(", ")}`);
      }
      await menuClick(page, "Rename");
      await page.waitForSelector(".swal2-input", { state: "visible", timeout: 5000 });
      await page.fill(".swal2-input", `renamed-${tag.slice(-4)}`);
      await page.click(".swal2-confirm");
      await waitRows((r) => r.find((x) => x.pick === second)?.name === `renamed-${tag.slice(-4)}`, "the rename never reached the open picker");
      await page.waitForFunction(([s, n]) => document.querySelector(s)?.textContent.includes(n), [tabSel(second), `renamed-${tag.slice(-4)}`], { timeout: 8000 });
      await sleep(400);
      await menuOf(page, rowSel(second));
      await Promise.all([page.waitForURL(new RegExp(`/shells/${second}`), { timeout: 10000 }), menuClick(page, "Open terminal page")]);
      await openEditor();
    });

    await run("desktop: many terminals stand in the picker inside the window, its body scrolls, the filter narrows them and Enter opens the one left through its menu", async () => {
      const dir = await page.getAttribute("dc-editor", "data-editor-project-path");
      for (let i = 0; i < 6; i++) {
        const res = await postAs(page, "/shells/new", { project: dir });
        const id = new URL(res.url).pathname.split("/").pop();
        assert(res.ok && id !== "new", `a shell of the burst failed: ${JSON.stringify(res)}`);
        made.add(id);
        assert((await postAs(page, `/shells/${id}/rename`, { name: `burst-${i}-with-a-long-name-that-runs-on-and-on-past-the-edge` })).ok, "renaming a shell of the burst failed");
      }
      await page.setViewportSize({ width: 900, height: 640 });
      await sleep(500);
      await byButton();
      await waitRows((r) => r.length === 8 && r.every((x) => x.name), "the eight shells never stood in the picker");
      const geo = await page.evaluate(() => {
        const panel = document.querySelector("[data-editor-sheet-panel]").getBoundingClientRect();
        const body = document.querySelector("[data-editor-sheet-body]");
        const rows = [...body.querySelectorAll("[data-term-pick]")].map((r) => r.getBoundingClientRect());
        return {
          inside: panel.top >= 0 && panel.bottom <= innerHeight && panel.right <= innerWidth,
          scrolls: body.scrollHeight > body.clientHeight && getComputedStyle(body).overflowY === "auto",
          fit: rows.every((r) => r.right <= panel.right + 1 && r.left >= panel.left - 1),
          page: document.scrollingElement.scrollWidth <= innerWidth,
        };
      });
      assert(geo.inside && geo.scrolls && geo.fit && geo.page, `the picker of eight terminals reads ${JSON.stringify(geo)}`);
      const scrolled = await page.evaluate(async () => {
        const body = document.querySelector("[data-editor-sheet-body]");
        body.scrollTop = body.scrollHeight;
        await new Promise((r) => setTimeout(r, 100));
        return body.scrollTop > 0;
      });
      assert(scrolled, "the picker's body did not scroll");
      await page.keyboard.press("Escape");
      await pickerClosed();
      await page.keyboard.press("Control+j");
      await pickerOpen();
      const reopened = await topRow();
      assert(reopened.top === 0 && reopened.text === "New shell" && reopened.marked && reopened.seen, `the picker reopened scrolled away from its selected New shell: ${JSON.stringify(reopened)}`);
      await page.keyboard.type("burst-3");
      await sleep(300);
      const left = (await pickRows()).filter((r) => r.pick);
      assert(left.length === 1 && left[0].name.startsWith("burst-3"), `the filter left ${JSON.stringify(left.map((r) => r.name))}`);
      await page.keyboard.press("Enter");
      await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
      await page.keyboard.press("Enter");
      await pickerClosed();
      await page.waitForSelector(`${tabSel(left[0].pick)}.active`, { timeout: 8000 });
      await page.setViewportSize({ width: 1360, height: 900 });
      await sleep(300);
      return "8 rows in a scrolling sheet, one left by the filter";
    });

    await run("desktop: a shell deleted elsewhere leaves the picker and its tab live", async () => {
      await byButton();
      await L.openPicked(page, rowSel(second));
      await pickerClosed();
      await page.waitForSelector(`${tabSel(second)}.active`, { timeout: 8000 });
      await page.keyboard.press("Control+j");
      await pickerOpen();
      const before = (await waitRows((r) => r.some((x) => x.pick === second), "the shell is not in the picker")).length;
      await elsewhere(async (p2) => L.deleteShell(p2, `${BASE}/shells/${second}`));
      made.delete(second);
      await waitRows((r) => r.length === before - 1 && !r.some((x) => x.pick === second), "the deleted shell stayed in the open picker");
      await page.waitForSelector(tabSel(second), { state: "detached", timeout: 10000 });
      await page.keyboard.press("Escape");
      await pickerClosed();
      second = "";
    });


    await run("desktop: ?terminal=<id> opens that terminal as a tab and leaves the address with &tab=1, a closed tab stays closed through a reload, a gone one stays quiet", async () => {
      const other = [...made].find((id) => id !== first);
      for (const query of [`?terminal=${other}`, `?terminal=${other}&tab=1`]) {
        await openEditor(page, query);
        await page.waitForSelector(`${tabSel(other)}.active`, { timeout: 10000 });
        await mounted(page, other);
        await page.waitForFunction(() => !/[?&](terminal|tab)=/.test(location.search), null, { timeout: 5000 })
          .catch(() => { throw new Error(`${query} stayed in the address: ${page.url()}`); });
        assert(new URL(page.url()).pathname === `/projects/${project}/editor`, `the address left the editor: ${page.url()}`);
        await page.click(tabSel(other), { button: "middle" });
        await page.waitForSelector(tabSel(other), { state: "detached", timeout: 5000 });
        await sleep(400);
        await page.reload({ waitUntil: "domcontentloaded" });
        await page.waitForSelector(".cm-editor, .editor-textarea", { state: "attached", timeout: 20000 });
        await sleep(1500);
        assert(!(await page.$(tabSel(other))), `the closed tab of ${query} came back after a reload`);
      }
      await openEditor(page, "?terminal=00000000-0000-4000-8000-000000000000");
      await sleep(1500);
      assert(!(await page.$('.editor-tab[data-path="//terminal/00000000-0000-4000-8000-000000000000"]')), "a gone terminal got a tab");
      const toasts = await errorToasts();
      assert(!toasts.length, `a gone terminal toasted ${toasts.join(" | ")}`);
    });

    await run("desktop: the terminal panel's stored keys are ignored without error", async () => {
      const errors = bag.pageErrors.length;
      await page.evaluate(([p, id]) => {
        localStorage.setItem(`dc-editor-term-open:${p}`, "1");
        localStorage.setItem(`dc-editor-term-active:${p}`, id);
        localStorage.setItem(`dc-editor-term-height:${p}`, "420");
        localStorage.setItem("dc-editor-term-height", "420");
      }, [project, first]);
      await openEditor();
      const geo = await noDock();
      assert(geo.gone && geo.meets && geo.code, `the stored panel took room below the code: ${JSON.stringify(geo)}`);
      assert(bag.pageErrors.length === errors, `the old keys raised ${bag.pageErrors.slice(errors).join(" | ")}`);
      await page.evaluate((p) => {
        for (const k of [`dc-editor-term-open:${p}`, `dc-editor-term-active:${p}`, `dc-editor-term-height:${p}`, "dc-editor-term-height"]) localStorage.removeItem(k);
      }, project);
    });

    await run("desktop: a coder made from the picker's New coder comes back as a tab its row reads open", async () => {
      await byButton();
      await clickAction(page, "New coder");
      await page.waitForSelector("[data-form-modal].show form", { timeout: 10000 });
      const action = await page.getAttribute("[data-form-modal] form", "action");
      assert(/panel=1/.test(action), `the coder form does not carry the panel marker: ${action}`);
      const f = page.locator('[data-form-modal] form:has(input[name="name"])').first();
      await f.locator('input[name="name"]').fill(`edt-${tag.slice(-6)}`);
      const from = page.url();
      let landed = "";
      await f.locator('button[type="submit"]').first().click();
      await page.waitForURL((u) => {
        if (/\/projects\/[^/]+\/editor\?terminal=/.test(u.href) && u.href !== from) landed = u.href;
        return !!landed;
      }, { timeout: 30000 });
      coderId = new URL(landed).searchParams.get("terminal");
      assert(coderId, "the landing carries no terminal");
      await page.waitForSelector(`${tabSel(coderId)}.active`, { timeout: 15000 });
      await mounted(page, coderId);
      await page.waitForFunction((id) => !!document.activeElement?.closest(`[data-terminal-view="${id}"]`), coderId, { timeout: 10000 });
      await page.keyboard.press("Control+j");
      await pickerOpen();
      await waitRows((r) => / open$/.test(r.find((x) => x.pick === coderId)?.text || ""), "the coder's row does not read open");
      await page.keyboard.press("Escape");
      await pickerClosed();
    });

    await run("desktop: the coder row's menu carries steer, stop and delete", async () => {
      assert(coderId, "no coder from the check before");
      await byButton();
      const labels = await menuOf(page, rowSel(coderId));
      await closeMenu(page);
      for (const want of ["Open terminal page", "Steer", "Open project", "Stop", "Delete"]) {
        assert(labels.includes(want), `the coder row's menu misses ${want}: ${labels.join(", ")}`);
      }
      await page.keyboard.press("Escape");
      await pickerClosed();
    });

    await run("desktop: a stopped coder's row moves under Stopped coders and its tab turns to Resume, its row resumes it into the tab", async () => {
      assert(coderId, "no coder from the check before");
      await byButton();
      await menuOf(page, rowSel(coderId));
      await menuClick(page, "Stop");
      await confirmSwal(page);
      await page.waitForSelector(`.editor-term-box form[data-term-resume] [data-resume-id="${coderId}"]`, { state: "visible", timeout: 10000 });
      if (await page.evaluate(() => document.querySelector("[data-editor-sheet]").hidden)) await byButton();
      const got = await waitRows((r) => /stopped · open$/.test(r.find((x) => x.pick === coderId)?.text || ""), "the stopped coder's row is not stopped");
      const row = got.find((x) => x.pick === coderId);
      assert(!/running/.test(row.icon), `the stopped row still runs: ${row.icon}`);
      const order = await page.evaluate((id) => {
        const els = [...document.querySelectorAll("[data-editor-sheet-body] .dropdown-item, [data-editor-sheet-body] .dropdown-header")];
        return { head: els.findIndex((e) => e.textContent.trim() === "Stopped coders"), row: els.findIndex((e) => e.dataset.termPick === id) };
      }, coderId);
      assert(order.head >= 0 && order.row > order.head, `the stopped coder stands outside Stopped coders: ${JSON.stringify(order)}`);
      await sleep(400);
      const menu = await menuOf(page, rowSel(coderId));
      await closeMenu(page);
      assert(menu[0] === "Open" && menu.includes("Resume") && !menu.includes("Stop"), `the stopped row's menu reads ${menu.join(", ")}`);
      await sleep(400);
      await L.openPicked(page, rowSel(coderId), { item: "Resume" });
      await pickerClosed();
      await mounted(page, coderId);
      await page.waitForSelector(`${tabSel(coderId)}.active`, { timeout: 10000 });
      assert((await page.locator(tabSel(coderId)).count()) === 1, "the resume made a second tab");
      await byButton();
      await waitRows((r) => / open$/.test(r.find((x) => x.pick === coderId)?.text || "") && !/stopped/.test(r.find((x) => x.pick === coderId)?.text || ""), "the resumed coder's row does not run");
      await page.keyboard.press("Escape");
      await pickerClosed();
    });

    await run("desktop: the coder row's Delete removes the coder, its row and its tab", async () => {
      assert(coderId, "no coder from the check before");
      await byButton();
      await menuOf(page, rowSel(coderId));
      await menuClick(page, "Delete", true);
      await confirmSwal(page);
      await page.waitForSelector(tabSel(coderId), { state: "detached", timeout: 10000 });
      if (await page.evaluate(() => document.querySelector("[data-editor-sheet]").hidden)) await byButton();
      await waitRows((r) => !r.some((x) => x.pick === coderId), "the deleted coder kept its row");
      await sleep(400);
      const keys = await page.evaluate(() => ({ sheet: !!document.activeElement?.closest("[data-editor-sheet-panel]"), at: document.activeElement?.outerHTML.slice(0, 120) }));
      assert(keys.sheet, `the closed tab handed the keys behind the picker: ${JSON.stringify(keys)}`);
      await page.keyboard.press("Escape");
      await pickerClosed();
      coderId = "";
    });

    await run("desktop: the shortcuts modal and the docs name Ctrl+J, Ctrl+I and the statusbar's icons, never a dock, a flip or assistants in the terminal picker", async () => {
      await openEditor();
      await page.click("[data-editor-menu]");
      await page.waitForSelector("[data-editor-menu-list].show", { timeout: 5000 });
      await page.click('[data-editor-menu-list] [data-bs-target="#editor-shortcuts-modal"]');
      await page.waitForSelector("#editor-shortcuts-modal.show", { timeout: 8000 });
      const modal = await page.evaluate(() => {
        const rows = [...document.querySelectorAll("#editor-shortcuts-modal .dc-shortcuts-row")].map((r) => r.textContent.replace(/\s+/g, " ").trim());
        const notes = [...document.querySelectorAll("#editor-shortcuts-modal .dc-shortcuts-note")].map((r) => r.textContent.replace(/\s+/g, " ").trim());
        return { rows, notes };
      });
      assert(modal.rows.includes("Open terminal Ctrl+J") && modal.rows.includes("Open assistant Ctrl+I") && !modal.rows.some((r) => /panel/i.test(r)), `the shortcuts read ${JSON.stringify(modal.rows)}`);
      assert(modal.rows.some((r) => /^In a terminal tab .*Ctrl\+J, Ctrl\+I$/.test(r)), `the terminal tab's keys miss Ctrl+I: ${JSON.stringify(modal.rows)}`);
      assert(modal.notes.includes("Or the terminal icon in the statusbar.") && modal.notes.includes("Or the sparkles in the statusbar."), `no note on the statusbar's icons: ${JSON.stringify(modal.notes)}`);
      assert(!modal.notes.some((n) => /\bdock\b|chip|assistants stand in it|flips|leads back|used last/i.test(n)), `a note still names the dock, the flip or the assistants in the terminal picker: ${JSON.stringify(modal.notes)}`);
      await sleep(600);
      await page.click("#editor-shortcuts-modal .btn-close");
      await page.waitForFunction(() => !document.querySelector(".modal.show, .modal-backdrop"), null, { timeout: 8000 });
      await page.goto(`${BASE}/docs`, { waitUntil: "domcontentloaded" });
      const docs = await page.locator(".dc-work-body").evaluate((el) => el.textContent.replace(/\s+/g, " "));
      for (const want of ["Terminal picker", "Assistant picker", "Lists New shell, New coder, the project's coders and shells, then stopped coders.", "With a mouse the first row is selected, on touch none.", "A row opens its menu: Open shows its tab", "Tab included", "a coder or shell of this project opens as a tab, in another group if there is one, a stopped coder resumes. One of another project opens its page."]) {
        assert(docs.includes(want), `the docs miss ${want}`);
      }
      assert(!/the first of them selected|a link to a coder, shell or assistant opens/.test(docs), "the docs still say a picker always selects its first entry or every coder link opens as a tab");
      assert(!/counts the running terminals|the assistants with New assistant follow|then Enter flips|first row leads back/.test(docs), "the docs still name the count, the flip or the assistants in the terminal picker");
      assert(!/Terminal panel|Terminal dock|\bdock\b/i.test(docs), `the docs still describe a panel or a dock: ${(docs.match(/.{60}\bdock\b.{60}/i) || [""])[0]}`);
      await openEditor();
    });

    // The pointer decides only how the terminal takes the keys: a Windows Edge
    // seen over a remote desktop reports no pointer and no hover at all, and an
    // iPad with a trackpad a coarse primary pointer beside a fine one. No
    // emulation reaches the media features apart, Chromium's blink settings
    // do: a browser of its own per pointer setup, logged into the same
    // instance.
    const pointerProbe = async (blink, viewport, typeMarker) => {
      const { chromium } = require("playwright-core");
      const b = await chromium.launch({ args: ["--no-sandbox", `--blink-settings=${blink}`] });
      try {
        const p = await (await b.newContext({ ignoreHTTPSErrors: true, viewport })).newPage();
        await L.login(p);
        await openEditor(p);
        const got = await p.evaluate(() => {
          const media = {};
          for (const q of ["(hover: hover)", "(pointer: fine)", "(pointer: coarse)", "(any-pointer: fine)", "(any-pointer: coarse)"]) media[q] = matchMedia(q).matches;
          const b = document.querySelector("[data-editor-term-status]").getBoundingClientRect();
          return { media, button: b.width > 0 && b.bottom <= innerHeight };
        });
        got.geo = await noDock(p);
        if (!typeMarker) return got;
        await byButton(p);
        const id = (await waitRows((r) => r.length > 0, "the probe's picker lists no terminal", p))[0].pick;
        await L.openPicked(p, rowSel(id));
        await mounted(p, id);
        await sleep(600);
        got.interactive = await p.evaluate((s) => ({
          classed: document.querySelector(`${s} terminal-attach`).classList.contains("attach-terminal-interactive"),
          cursorInput: !!document.querySelector(`${s} #terminal-cursor-input`),
        }), boxSel(id));
        const r = await box(p, `${boxSel(id)} terminal-attach`);
        await p.mouse.click(r.x + r.w / 2, r.y + r.h / 2);
        await p.keyboard.type(`echo ${typeMarker}`);
        await p.keyboard.press("Enter");
        got.echoed = await waitText(p, id, typeMarker).then(() => true, () => false);
        return got;
      } finally {
        await b.close();
      }
    };
    const desk = { width: 1360, height: 900 };
    const bare = (g) => g.geo.gone && g.geo.meets && g.geo.code && g.button;

    await run("desktop: a browser reporting no pointer and no hover opens a terminal tab through the statusbar button and types into it", async () => {
      if (engine !== "chromium") return "skipped, the pointer setup is a chromium launch flag";
      const marker = `ENP${tag.slice(-4)}`;
      const got = await pointerProbe("primaryPointerType=1,availablePointerTypes=1,primaryHoverType=1,availableHoverTypes=1", desk, marker);
      assert(Object.values(got.media).every((v) => !v), `the pointer setup did not take: ${JSON.stringify(got.media)}`);
      assert(bare(got), `no pointer at all shows a dock or no statusbar button: ${JSON.stringify(got)}`);
      assert(got.interactive.classed && !got.interactive.cursorInput, `the terminal tab runs in phone mode: ${JSON.stringify(got.interactive)}`);
      assert(got.echoed, `the typed ${marker} never reached the terminal`);
    });

    await run("desktop: a coarse primary pointer beside a fine one opens a terminal tab through the statusbar button and types into it", async () => {
      if (engine !== "chromium") return "skipped, the pointer setup is a chromium launch flag";
      const marker = `ECP${tag.slice(-4)}`;
      const got = await pointerProbe("primaryPointerType=2,availablePointerTypes=6,primaryHoverType=1,availableHoverTypes=3", desk, marker);
      assert(got.media["(pointer: coarse)"] && got.media["(any-pointer: fine)"], `the pointer setup did not take: ${JSON.stringify(got.media)}`);
      assert(bare(got), `a fine pointer behind a coarse primary one shows a dock or no statusbar button: ${JSON.stringify(got)}`);
      assert(got.interactive.classed && !got.interactive.cursorInput, `the terminal tab runs in phone mode: ${JSON.stringify(got.interactive)}`);
      assert(got.echoed, `the typed ${marker} never reached the terminal`);
    });

    await run("desktop: a touch only device shows no dock and the statusbar button at a tablet's and at a phone's width", async () => {
      if (engine !== "chromium") return "skipped, the pointer setup is a chromium launch flag";
      const touch = await pointerProbe("primaryPointerType=2,availablePointerTypes=2,primaryHoverType=1,availableHoverTypes=1", { width: 834, height: 1194 }, null);
      assert(touch.media["(any-pointer: coarse)"] && !touch.media["(any-pointer: fine)"], `the pointer setup did not take: ${JSON.stringify(touch.media)}`);
      assert(bare(touch), `a touch tablet shows a dock or no statusbar button: ${JSON.stringify(touch)}`);
      const phone = await pointerProbe("primaryPointerType=2,availablePointerTypes=2,primaryHoverType=1,availableHoverTypes=1", { width: 390, height: 844 }, null);
      assert(bare(phone), `a phone width shows a dock or no statusbar button: ${JSON.stringify(phone)}`);
    });

    await run("tablet: at 834x1194 with touch no dock, a tap on the statusbar button opens the picker and a tap on a row the terminal as a tab", async () => {
      const ctx = await browser.newContext({ ignoreHTTPSErrors: true, hasTouch: true, viewport: { width: 834, height: 1194 } });
      try {
        const tp = await ctx.newPage();
        L.wirePage(tp, bag);
        await L.login(tp);
        await openEditor(tp);
        const geo = await noDock(tp);
        assert(geo.gone && geo.meets && geo.code && geo.bar, `something stands between the code and the statusbar on a tablet: ${JSON.stringify(geo)}`);
        assert(plainIcons(await statusIcons(tp)), `the tablet's statusbar icons read ${JSON.stringify(await statusIcons(tp))}`);
        await tp.tap("[data-editor-term-status]");
        await pickerOpen(tp);
        const id = (await waitRows((r) => r.length > 0, "the tablet's picker lists no terminal", tp))[0].pick;
        await L.openPicked(tp, rowSel(id), { tap: true });
        await tp.waitForSelector(`${tabSel(id)}.active`, { timeout: 8000 });
        await mounted(tp, id);
        await pickerClosed(tp);
      } finally {
        await ctx.close();
      }
    });

    await run("mobile: no dock, the statusbar's two plain icons each open their picker as a sheet, a tap on a row opens its menu inside the screen and Open its tab", async () => {
      const mp = await mobilePage();
      await openEditor(mp);
      if (await shown(mp, "[data-editor-backdrop]")) {
        await mp.touchscreen.tap(380, 400);
        await mp.waitForFunction(() => document.querySelector("[data-editor-backdrop]").hidden, null, { timeout: 5000 });
      }
      const geo = await noDock(mp);
      assert(geo.gone && geo.meets && geo.code, `the phone shows something between the code and the statusbar: ${JSON.stringify(geo)}`);
      const icons = await statusIcons(mp);
      assert(plainIcons(icons), `the phone's statusbar icons read ${JSON.stringify(icons)}`);
      const look = await sparkles(mp);
      assert(plainSparkle(look), `the phone's sparkles read ${JSON.stringify(look)}`);
      await mp.tap("[data-editor-asst-status]");
      await asstPickerOpen(mp);
      const asheet = await box(mp, "[data-editor-sheet-panel]");
      const arows = await pickRows(mp);
      assert(asheet.w >= 385 && asheet.b >= 760, `the assistant picker is no bottom sheet: ${JSON.stringify(asheet)}`);
      assert(arows.every((r) => r.h >= 36) && arows.some((r) => /^New assistant/.test(r.text)) && !arows.some((r) => r.pick), `the assistant sheet reads ${JSON.stringify(arows)}`);
      await mp.tap("[data-editor-sheet-close]");
      await pickerClosed(mp);
      await mp.tap("[data-editor-term-status]");
      await pickerOpen(mp);
      const sheet = await box(mp, "[data-editor-sheet-panel]");
      await waitRows((r) => r.length > 0, "the phone's picker lists no terminal", mp);
      const rows = await pickRows(mp);
      assert(sheet.w >= 385 && sheet.b >= 760, `the picker is no bottom sheet: ${JSON.stringify(sheet)}`);
      assert(rows.every((r) => r.h >= 36), `a row is small for a finger: ${JSON.stringify(rows.map((r) => r.h))}`);
      assert(!rows.some((r) => /assistant/i.test(r.text)), `the phone's terminal picker offers assistants: ${JSON.stringify(rows.map((r) => r.text))}`);
      assert(rows[0].text === "New shell" && rows[1].text === "New coder", `the phone's picker does not start with its New actions: ${JSON.stringify(rows.map((r) => r.text))}`);
      const dir = await mp.getAttribute("dc-editor", "data-editor-project-path");
      const burst = [];
      for (let i = 0; i < 6; i++) {
        const res = await postAs(mp, "/shells/new", { project: dir });
        const id = new URL(res.url).pathname.split("/").pop();
        assert(res.ok && id !== "new", `a shell of the phone's burst failed: ${JSON.stringify(res)}`);
        made.add(id);
        burst.push(id);
      }
      await waitRows((r) => burst.every((id) => r.some((x) => x.pick === id)), "the phone's burst never stood in the picker", mp);
      const scrolled = await mp.evaluate(async () => {
        const body = document.querySelector("[data-editor-sheet-body]");
        body.scrollTop = body.scrollHeight;
        await new Promise((r) => setTimeout(r, 100));
        return { top: body.scrollTop, height: body.scrollHeight, box: body.clientHeight };
      });
      assert(scrolled.top > 0, `the phone's picker did not scroll: ${JSON.stringify(scrolled)}`);
      await mp.tap("[data-editor-sheet-close]");
      await pickerClosed(mp);
      await mp.tap("[data-editor-term-status]");
      await pickerOpen(mp);
      const reopened = await topRow(mp);
      assert(reopened.top === 0 && reopened.text === "New shell" && reopened.seen, `the phone's picker reopened scrolled away from New shell: ${JSON.stringify(reopened)}`);
      const pick = rows.find((r) => r.pick);
      const row = await box(mp, rowSel(pick.pick));
      await mp.touchscreen.tap(row.x + row.w / 2, row.y + row.h / 2);
      await mp.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
      const menu = await menuAt(mp);
      assert(menu.labels[0] === "Open" && menu.labels.includes("Open terminal page") && menu.l >= 0 && menu.r <= 390 && menu.t >= 0 && menu.b <= 844, `the phone's row menu reads ${JSON.stringify(menu)}`);
      const title = await box(mp, "[data-editor-sheet-title]");
      await mp.touchscreen.tap(title.x + 10, title.y + title.h / 2);
      await mp.waitForSelector(".dc-context-menu", { state: "detached", timeout: 5000 });
      assert(!(await mp.evaluate(() => document.querySelector("[data-editor-sheet]").hidden)), "closing the row's menu on the phone took the picker");
      await sleep(400);
      await L.openPicked(mp, rowSel(pick.pick), { tap: true });
      await mp.waitForSelector(`${tabSel(pick.pick)}.active`, { timeout: 8000 });
      await mounted(mp, pick.pick);
      await pickerClosed(mp);
    });

    await run("mobile: a tap on a statusbar icon shows no focus ring, neither while its sheet stands nor once a tap closed it", async () => {
      const mp = await mobilePage();
      await openEditor(mp);
      if (await shown(mp, "[data-editor-backdrop]")) {
        await mp.touchscreen.tap(380, 400);
        await mp.waitForFunction(() => document.querySelector("[data-editor-backdrop]").hidden, null, { timeout: 5000 });
      }
      const none = async (how) => {
        const got = await rings(mp);
        assert(!ringedOnes(got).length, `${how} leaves a ring: ${JSON.stringify(got)}`);
      };
      for (const [btn, open] of [["[data-editor-term-status]", pickerOpen], ["[data-editor-asst-status]", asstPickerOpen]]) {
        await mp.tap(btn);
        await open(mp);
        await none(`a tap on ${btn} with its sheet standing`);
        const at = await box(mp, "[data-editor-sheet]");
        await mp.touchscreen.tap(at.x + at.w / 2, at.y + 12);
        await sheetGone(mp);
        await none(`${btn}'s sheet closed by a tap above it`);
        await sleep(400);
        await mp.tap(btn);
        await open(mp);
        await mp.tap("[data-editor-sheet-close]");
        await sheetGone(mp);
        await none(`${btn}'s sheet closed by its X`);
        await sleep(400);
      }
      await mp.tap("[data-editor-term-status]");
      await pickerOpen(mp);
      const pick = (await waitRows((r) => r.length > 0, "the phone's picker lists no terminal", mp))[0];
      await L.openPicked(mp, rowSel(pick.pick), { tap: true });
      await mp.waitForSelector(`${tabSel(pick.pick)}.active`, { timeout: 8000 });
      await pickerClosed(mp);
      await none("a picker row's Open tapped");
    });

    await run("statusbar: with every entry on, a phone shows only icons and the cursor position, a desktop its texts while they fit and none when they do not, nothing ever overlaps and the entries stand at even gaps", async () => {
      const mp = await mobilePage();
      await openEditor(mp);
      await sleep(1500);
      const phone = await fullBar(mp);
      assert(!phone.overlap && phone.inside && phone.tappable, `the phone's statusbar breaks: ${JSON.stringify(phone)}`);
      assert(phone.texts.length >= 5 && phone.texts.every((t) => !t) && phone.pos, `the phone's statusbar keeps a text or loses the position: ${JSON.stringify(phone)}`);
      assert(phone.icons.length >= 7 && phone.icons.every((i) => i.whole), `an icon of the phone's statusbar is cut: ${JSON.stringify(phone.icons)}`);
      assert(phone.even, `the phone's statusbar entries stand at uneven gaps or keep room for a hidden text: ${JSON.stringify({ gaps: phone.gaps, slack: phone.slack })}`);
      await page.setViewportSize({ width: 1100, height: 800 });
      await openEditor();
      await sleep(1500);
      const short = await fullBar(page, { readOnly: true });
      assert(!short.overlap && short.inside, `a desktop running short breaks the statusbar: ${JSON.stringify(short)}`);
      assert(short.texts.every((t) => !t) && short.pos && short.icons.every((i) => i.whole), `a desktop running short keeps a text or cuts an icon: ${JSON.stringify(short)}`);
      await page.setViewportSize({ width: 1440, height: 900 });
      const wide = await fullBar(page);
      assert(!wide.overlap && wide.inside && wide.texts.every((t) => t) && wide.pos, `the texts did not come back where they fit: ${JSON.stringify(wide)}`);
      assert(wide.even && short.even, `the desktop's statusbar entries stand at uneven gaps or keep room for a hidden text: ${JSON.stringify({ wide: [wide.gaps, wide.slack], short: [short.gaps, short.slack] })}`);
      await page.setViewportSize({ width: 1360, height: 900 });
      await openEditor();
      return `gaps phone ${phone.gaps.join("/")}, desktop ${wide.gaps.join("/")}, short ${short.gaps.join("/")}`;
    });
  } finally {
    try {
      for (const id of made) await L.deleteShell(page, `${BASE}/shells/${id}`).catch(() => {});
      if (coderId) await postAs(page, `/coders/${coderId}/delete`, {}).catch(() => {});
      await L.deleteProject(page, project);
    } catch (e) {
      console.log("cleanup: " + e.message);
    }
  }
});
