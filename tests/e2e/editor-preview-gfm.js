const fs = require("fs");
const path = require("path");
const L = require("./lib");
const { assert, sleep, confirmSwal } = L;

// Not a runner of its own: editor.js runs this after its markdown preview
// check, in its scratch project. It previews editor-preview-gfm.md, the
// README the Go tests of internal/markdown read too, plus filler and markup
// that must not run, and the folder gfm-docs its relative links and its
// image point into.
// Gotcha: a closed fold keeps a box for its content, visibility is read via
// checkVisibility.

const PANE = "[data-editor-preview-pane]";
const README = fs.readFileSync(path.join(__dirname, "editor-preview-gfm.md"), "utf8");
const DOCS = "gfm-docs";
const GUIDE = `${DOCS}/guide one.md`;
// The first diagram fetches mermaid from the CDN, which took up to half a
// minute on a cold cache.
const MERMAID_LOAD_MS = 30000;
const PIXEL = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg==";

function content(origin) {
  const filler = Array.from({ length: 80 }, (_, i) => `Filler paragraph ${i}.\n`).join("\n");
  return README.replace("## Security", `${filler}\n## Security`) + [
    "",
    "<script>window.__gfmRan = 1</script>",
    `<img src="missing.png" onerror="window.__gfmRan = 1">`,
    `<a href="javascript:window.__gfmRan = 1">jslink</a> [outLink](${origin}/health) [mailLink](mailto:dev@example.test)`,
  ].join("\n\n");
}

// The diagrams of the mermaid checks, their links and a label image point into
// gfm-docs like the markdown's. Only strict mode takes out the javascript: link
// of badDiagram, mermaid sanitizes its label in every mode.
const diagrams = (origin, tag) => [
  "",
  "```mermaid",
  "graph TD",
  "  Out[outDiagram] --> Rel[relDiagram]",
  "  Rel --> Bad[\"badDiagram <img src=x onerror=window.__gfmRan=2>\"]",
  `  Pic["<img src='${DOCS}/pixel.png'> picDiagram"]`,
  `  click Out href "${origin}/health"`,
  `  click Rel href "${encodeURI(GUIDE)}"`,
  "  click Bad href \"javascript:void(window.__gfmRan=3)\"",
  "```",
  "",
  "```mermaid",
  "graph LR",
  `  ${Array.from({ length: 12 }, (_, i) => `W${i}[Wide${tag} node ${i}]`).join(" --> ")}`,
  `  W11 --> End${tag}`,
  "```",
  "",
].join("\n");

// watchDiagrams reads the pane on every frame until watched collects it: the
// most mermaid sources shown at once, the fewest drawings, every scroll top.
const watchDiagrams = (target) => target.evaluate((s) => {
  const pane = document.querySelector(s);
  const seen = { source: 0, fewest: 99, tops: [] };
  window.__mermaidWatch = seen;
  const tick = () => {
    if (window.__mermaidWatch !== seen) return;
    seen.source = Math.max(seen.source, [...pane.querySelectorAll("pre > code.language-mermaid")].filter((c) => c.checkVisibility()).length);
    seen.fewest = Math.min(seen.fewest, pane.querySelectorAll(".dc-mermaid svg").length);
    const top = Math.round(pane.scrollTop);
    if (!seen.tops.includes(top)) seen.tops.push(top);
    requestAnimationFrame(tick);
  };
  tick();
}, PANE);
const watched = (target) => target.evaluate(() => {
  const seen = window.__mermaidWatch;
  window.__mermaidWatch = null;
  return seen;
});

async function gfmPreviewChecks({ browser, page, run, bag, project, tag, tabSel, openRowMenu, menuItem, waitDirty }) {
  const file = `gfm_${tag}.md`;
  const origin = new URL(page.url()).origin;
  const visible = (target, sel, want) => target.waitForFunction(([s, w]) => {
    const el = document.querySelector(s);
    return el && el.checkVisibility({ visibilityProperty: true }) === w;
  }, [`${PANE} ${sel}`, want], { timeout: 4000 });
  const summary = (target, text) => target.locator(`${PANE} summary`, { hasText: text }).click();
  const showPreview = async (target) => {
    await target.click(`.editor-file[data-path="${file}"] .editor-item-name`);
    await openRowMenu(target, tabSel(file));
    await menuItem(target, "Show preview").click();
    await visible(target, "summary", true);
  };

  const diagramLink = (text) => page.locator(`${PANE} .dc-mermaid a`, { hasText: text });
  const pixelLoaded = (target) => target.waitForFunction((s) => {
    const img = document.querySelector(`${s} img[alt="a pixel"]`);
    return img && img.complete && img.naturalWidth === 1 && img.getAttribute("src").includes("/raw?path=gfm-docs%2Fpixel.png");
  }, PANE, { timeout: 6000 });

  await run("markdown preview folds <details> and runs nothing of the file", async () => {
    await page.evaluate(async ([proj, file, body, docs, guide, pixel]) => {
      const headers = { "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]').content };
      const post = (to, fields) => fetch(`/projects/${proj}/editor/${to}`, { method: "POST", headers, body: fields });
      const guideBody = `# Guide\n\n${Array.from({ length: 80 }, (_, i) => `Step ${i}.`).join("\n\n")}\n\n## Install\n\nRun it.\n`;
      const upload = new FormData();
      upload.append("dir", docs);
      upload.append("files", new Blob([Uint8Array.from(atob(pixel), (c) => c.charCodeAt(0))], { type: "image/png" }), "pixel.png");
      await post("create", new URLSearchParams({ path: file }));
      await post("file", new URLSearchParams({ path: file, content: body }));
      await post("mkdir", new URLSearchParams({ path: docs }));
      await post("create", new URLSearchParams({ path: guide }));
      await post("file", new URLSearchParams({ path: guide, content: guideBody }));
      await post("upload", upload);
    }, [project, file, content(origin), DOCS, GUIDE, PIXEL]);
    await page.click("[data-editor-refresh]");
    await showPreview(page);
    await visible(page, "details td", false);
    await summary(page, "Start the server");
    await visible(page, "details td", true);
    await summary(page, "Build from source");
    await visible(page, "details details pre", true);
    const look = await page.evaluate((s) => {
      const td = document.querySelector(`${s} details td`);
      return { border: parseFloat(getComputedStyle(td).borderBottomWidth), code: td.querySelector("code")?.textContent };
    }, PANE);
    assert(look.border > 0 && look.code === "--state-dir", `the table in the fold is not Markdown with the table look: ${JSON.stringify(look)}`);
    await summary(page, "Start the server");
    await visible(page, "details td", false);
    const stripped = await page.evaluate((s) => {
      const pane = document.querySelector(s);
      return {
        scripts: pane.querySelectorAll("script").length,
        handlers: [...pane.querySelectorAll("*")].flatMap((el) => [...el.attributes].map((a) => a.name)).filter((n) => n.startsWith("on")),
        jsHref: [...pane.querySelectorAll("[href], [src]")].some((el) => /^\s*javascript:/i.test(el.getAttribute("href") || el.getAttribute("src"))),
        ran: window.__gfmRan !== undefined,
      };
    }, PANE);
    assert(!stripped.scripts && !stripped.handlers.length && !stripped.jsHref && !stripped.ran, `markup that runs reached the preview: ${JSON.stringify(stripped)}`);
  });

  await run("preview links: one out opens a new tab, mailto is left to the browser, heading links scroll, an umlaut one too", async () => {
    const [tab] = await Promise.all([
      page.context().waitForEvent("page", { timeout: 8000 }),
      page.locator(`${PANE} a`, { hasText: "outLink" }).click(),
    ]);
    await tab.waitForLoadState("domcontentloaded");
    const opened = tab.url();
    await tab.close();
    assert(opened === `${origin}/health`, `the link opened ${opened}`);
    const mail = await page.evaluate((s) => {
      let prevented = null;
      window.addEventListener("click", (e) => {
        prevented = e.defaultPrevented;
        e.preventDefault();
      }, { once: true });
      const link = [...document.querySelectorAll(`${s} a`)].find((a) => a.textContent === "mailLink");
      link.click();
      return { href: link.href, target: link.target, prevented };
    }, PANE);
    assert(mail.href === "mailto:dev@example.test" && !mail.target && mail.prevented === false, `the mailto link is held back: ${JSON.stringify(mail)}`);
    const inView = ([s, id]) => {
      const pane = document.querySelector(s);
      const box = document.getElementById(id).getBoundingClientRect();
      const frame = pane.getBoundingClientRect();
      return box.top >= frame.top && box.bottom <= frame.bottom;
    };
    for (const [text, id] of [["Security", "user-content-security"], ["Über uns", "user-content-über-uns"]]) {
      await page.$eval(PANE, (pane) => pane.scrollTo({ top: 0, behavior: "instant" }));
      assert(!(await page.evaluate(inView, [PANE, id])), `${text} is in view before the click`);
      const before = await page.evaluate(() => [location.href, history.length]);
      await page.locator(`${PANE} a`, { hasText: text }).click();
      await page.waitForFunction(inView, [PANE, id], { timeout: 4000 });
      const after = await page.evaluate(() => [location.href, history.length]);
      assert(after[0] === before[0] && after[1] === before[1], `the link touched the address or the history: ${JSON.stringify({ before, after })}`);
    }
  });

  await run("the preview follows a theme switch", async () => {
    const look = () => page.evaluate((s) => {
      const pane = document.querySelector(s);
      return { bg: getComputedStyle(pane).backgroundColor, text: getComputedStyle(pane.querySelector("h1")).color };
    }, PANE);
    const before = await look();
    const flip = await page.evaluate(() => (document.documentElement.getAttribute("data-bs-theme") === "dark" ? "light" : "dark"));
    await page.evaluate(async (to) => (await import("@dc/theme")).setThemePreference(to), flip);
    await page.waitForFunction((to) => document.documentElement.getAttribute("data-bs-theme") === to, flip, { timeout: 4000 });
    await sleep(400);
    const after = await look();
    await page.evaluate(async () => (await import("@dc/theme")).setThemePreference("auto"));
    assert(after.bg !== before.bg && after.text !== before.text, `the preview did not follow the switch: ${JSON.stringify({ before, after })}`);
  });

  await run("an opened fold stays open and in place while the file is typed into", async () => {
    await summary(page, "Start the server");
    await visible(page, "details td", true);
    await page.$eval(PANE, (pane) => pane.scrollTo({ top: 150, behavior: "instant" }));
    const place = () => page.$eval(PANE, (pane) => ({ top: pane.scrollTop, open: pane.querySelector("details").open }));
    const before = await place();
    await page.focus(".cm-content");
    await page.keyboard.press("Control+End");
    await page.keyboard.type(`\n\nTyped${tag}`);
    await page.waitForFunction(([s, t]) => document.querySelector(s).textContent.includes(t), [PANE, `Typed${tag}`], { timeout: 8000 });
    const after = await place();
    assert(before.top === 150 && after.top === before.top && after.open, `the fold closed or the preview jumped: ${JSON.stringify({ before, after })}`);
  });

  await run("the preview stays filled after a split and a group change, its keys work after a click into it", async () => {
    const side = await page.evaluate((p) => [...document.querySelectorAll(".editor-tab[data-path]")].map((t) => t.dataset.path).find((path) => path !== p), file);
    for (const [label, groups] of [["Split right", 2], ["Move to group 1", 1]]) {
      await openRowMenu(page, tabSel(side));
      await menuItem(page, label).click();
      await page.waitForFunction((n) => document.querySelectorAll("[data-editor-group]").length === n, groups, { timeout: 6000 });
      if (!(await page.$(`${tabSel(file)}.active`))) await page.click(tabSel(file));
      await visible(page, "details td", true);
    }
    await waitDirty(file, true);
    await page.click(`${PANE} h1`);
    await page.keyboard.press("Control+S");
    await waitDirty(file, false);
    await page.keyboard.press("Shift");
    await sleep(80);
    await page.keyboard.press("Shift");
    await page.waitForSelector("[data-editor-quickopen]:not([hidden])", { timeout: 6000 });
    await page.keyboard.press("Escape");
    await page.waitForSelector("[data-editor-quickopen][hidden]", { state: "attached", timeout: 6000 });
  });

  await run("a guest of a link sees the same preview", async () => {
    const made = await page.evaluate(async (fields) => {
      const res = await fetch("/settings/sharing/new", {
        method: "POST",
        headers: { "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]').content },
        body: new URLSearchParams(fields),
      });
      return { status: res.status, text: await res.text() };
    }, [["amount", "1"], ["unit", "hours"], ["name", `gfm ${tag}`], ["project", project]]);
    const link = (made.text.match(/value="([^"]*\/share\/[^"]+)"/) || [])[1];
    assert(made.status === 200 && link, `making the link answered ${made.status}`);
    const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1360, height: 900 } });
    try {
      const gp = await ctx.newPage();
      L.wirePage(gp, bag);
      await gp.goto(link, { waitUntil: "domcontentloaded" });
      await gp.waitForSelector(".editor-file", { timeout: 15000 });
      await showPreview(gp);
      await visible(gp, "details td", false);
      await summary(gp, "Start the server");
      await visible(gp, "details td", true);
      await pixelLoaded(gp);
    } finally {
      await ctx.close();
    }
  });

  await run("a mermaid block renders as a diagram, a bad one shows its own error and leaves the rest alone", async () => {
    await page.focus(".cm-content");
    await page.keyboard.press("Control+End").catch(() => {});
    await page.keyboard.type([
      "",
      "```mermaid",
      `graph TD; Ok${tag}-->Go${tag};`,
      "```",
      "",
      "```mermaid",
      `not a diagram ${tag}`,
      "```",
      "",
    ].join("\n"));
    await page.waitForFunction(([s, t]) => {
      const pane = document.querySelector(s);
      const svg = pane.querySelector(".dc-mermaid svg");
      const err = pane.querySelector(".dc-mermaid-error");
      return !!svg?.textContent.includes(`Ok${t}`) && !!err?.textContent && !!pane.querySelector("h1");
    }, [PANE, tag], { timeout: MERMAID_LOAD_MS });
  });

  await run("a diagram's link to a full address opens a new tab, a relative one opens the project's file, a relative image in a label shows it", async () => {
    await page.focus(".cm-content");
    await page.keyboard.press("Control+End");
    await page.keyboard.insertText(diagrams(origin, tag));
    await page.waitForFunction(([s, t]) => {
      const svgs = [...document.querySelectorAll(`${s} .dc-mermaid svg`)];
      return svgs.some((svg) => svg.textContent.includes("badDiagram")) && svgs.some((svg) => svg.textContent.includes(`End${t}`));
    }, [PANE, tag], { timeout: MERMAID_LOAD_MS });
    const [tab] = await Promise.all([
      page.context().waitForEvent("page", { timeout: 8000 }),
      diagramLink("outDiagram").click(),
    ]);
    await tab.waitForLoadState("domcontentloaded");
    const opened = tab.url();
    await tab.close();
    assert(opened === `${origin}/health`, `the diagram's link opened ${opened}`);
    const before = page.url();
    await diagramLink("relDiagram").click();
    await page.waitForSelector(`${tabSel(GUIDE)}.active`, { timeout: 8000 });
    assert(page.url() === before, `the diagram's relative link moved the page to ${page.url()}`);
    await page.evaluate((sel) => document.querySelector(`${sel} .editor-tab-state`)?.click(), tabSel(GUIDE));
    await page.waitForFunction((sel) => !document.querySelector(sel), tabSel(GUIDE), { timeout: 6000 });
    await page.click(tabSel(file));
    await page.waitForFunction((s) => {
      const img = document.querySelector(`${s} .dc-mermaid img[src*="pixel"]`);
      return img?.complete && img.naturalWidth === 1 && img.getAttribute("src").includes("/raw?path=gfm-docs%2Fpixel.png");
    }, PANE, { timeout: 8000 });
  });

  await run("a hostile diagram runs nothing, not even the javascript: link only mermaid's strict mode takes out", async () => {
    const before = await page.evaluate(() => [location.href, history.length, document.querySelectorAll(".editor-tab[data-path]").length]);
    await diagramLink("badDiagram").click();
    await sleep(500);
    const after = await page.evaluate(() => [location.href, history.length, document.querySelectorAll(".editor-tab[data-path]").length]);
    const left = await page.evaluate((s) => {
      const all = [...document.querySelectorAll(`${s} .dc-mermaid *`)];
      const scheme = (value) => {
        try {
          return new URL(value, location.href).protocol;
        } catch {
          return "";
        }
      };
      return {
        ran: window.__gfmRan ?? null,
        scripts: all.filter((el) => el.localName === "script").length,
        handlers: all.flatMap((el) => [...el.attributes].map((a) => a.name)).filter((n) => /^on/i.test(n)),
        jsAddress: all.some((el) => [...el.attributes].some((a) => scheme(a.value) === "javascript:")),
      };
    }, PANE);
    assert(JSON.stringify(after) === JSON.stringify(before) && !left.ran && !left.scripts && !left.handlers.length && !left.jsAddress, `the hostile diagram did something: ${JSON.stringify({ before, after, left })}`);
  });

  await run("a theme switch redraws the diagrams in the new theme without showing their source", async () => {
    const look = () => page.evaluate((s) => [...document.querySelectorAll(`${s} .dc-mermaid svg`)].map((svg) => ({
      id: svg.id,
      fill: getComputedStyle(svg.querySelector(".node :is(rect, path, polygon)")).fill,
    })), PANE);
    const redrawn = (from) => page.waitForFunction(([s, ids]) => {
      const now = [...document.querySelectorAll(`${s} .dc-mermaid svg`)].map((svg) => svg.id);
      return now.length === ids.length && now.every((id) => !ids.includes(id));
    }, [PANE, from.map((d) => d.id)], { timeout: 15000 });
    const before = await look();
    const flip = await page.evaluate(() => (document.documentElement.getAttribute("data-bs-theme") === "dark" ? "light" : "dark"));
    await watchDiagrams(page);
    await page.evaluate(async (to) => (await import("@dc/theme")).setThemePreference(to), flip);
    await redrawn(before);
    const seen = await watched(page);
    const after = await look();
    await page.evaluate(async () => (await import("@dc/theme")).setThemePreference("auto"));
    await redrawn(after);
    assert(seen.source === 0 && seen.fewest === before.length && after.every((d, i) => d.fill !== before[i].fill), `the diagrams did not follow the switch cleanly: ${JSON.stringify({ before, after, seen })}`);
  });

  await run("typing keeps the drawings of unchanged blocks and the scroll position, a changed block shows its old drawing until the new one stands", async () => {
    const drawn = () => page.evaluate((s) => [...document.querySelectorAll(`${s} .dc-mermaid svg`)].map((svg) => ({ id: svg.id, text: svg.textContent })), PANE);
    const before = await drawn();
    await page.focus(".cm-content");
    await page.keyboard.press("Control+End");
    await page.keyboard.press("ArrowUp");
    await page.keyboard.press("ArrowUp");
    await page.keyboard.press("End");
    await watchDiagrams(page);
    await page.keyboard.insertText(` --> Typed${tag}`);
    await page.waitForFunction(([s, t]) => [...document.querySelectorAll(`${s} .dc-mermaid svg`)].some((svg) => svg.textContent.includes(`Typed${t}`)), [PANE, tag], { timeout: 15000 });
    await sleep(300);
    const inside = await watched(page);
    const between = await drawn();
    const changed = between.filter((d, i) => d.id !== before[i]?.id);
    assert(inside.source === 0 && inside.fewest === before.length && changed.length === 1 && changed[0].text.includes(`Typed${tag}`), `typing in a block disturbed the diagrams: ${JSON.stringify({ inside, changed })}`);
    const top = await page.evaluate((s) => {
      const pane = document.querySelector(s);
      const first = pane.querySelector(".dc-mermaid").getBoundingClientRect().top - pane.getBoundingClientRect().top;
      pane.scrollTo({ top: Math.round(pane.scrollTop + first + 40), behavior: "instant" });
      return pane.scrollTop;
    }, PANE);
    await page.keyboard.press("Control+End");
    await watchDiagrams(page);
    await page.keyboard.type(`\n\nAfter${tag}`);
    await page.waitForFunction(([s, t]) => document.querySelector(s).textContent.includes(`After${t}`), [PANE, tag], { timeout: 8000 });
    await sleep(500);
    const outside = await watched(page);
    const after = await drawn();
    assert(outside.source === 0 && outside.fewest === before.length && JSON.stringify(outside.tops) === JSON.stringify([top]) && after.every((d, i) => d.id === between[i].id), `typing below the diagrams disturbed them: ${JSON.stringify({ top, outside, after: after.map((d) => d.id), between: between.map((d) => d.id) })}`);
  });

  await run("on a phone a wide diagram keeps its size and scrolls sideways, the pane and the page do not", async () => {
    await page.setViewportSize({ width: 390, height: 844 });
    try {
      await page.waitForFunction((s) => document.querySelector(s).clientWidth <= 390, PANE, { timeout: 6000 });
      const at = await page.evaluate(([s, t]) => {
        const host = [...document.querySelectorAll(`${s} .dc-mermaid`)].find((d) => d.textContent.includes(`Wide${t}`));
        host.scrollIntoView({ block: "center", behavior: "instant" });
        const r = host.getBoundingClientRect();
        return { x: r.x + r.width / 2, y: r.y + r.height / 2 };
      }, [PANE, tag]);
      await page.mouse.move(at.x, at.y);
      await page.mouse.wheel(240, 0);
      await page.waitForFunction(([s, t]) => [...document.querySelectorAll(`${s} .dc-mermaid`)].find((d) => d.textContent.includes(`Wide${t}`)).scrollLeft > 0, [PANE, tag], { timeout: 4000 }).catch(() => {});
      const wide = await page.evaluate(([s, t]) => {
        const pane = document.querySelector(s);
        const host = [...pane.querySelectorAll(".dc-mermaid")].find((d) => d.textContent.includes(`Wide${t}`));
        const svg = host.querySelector("svg");
        return {
          natural: svg.viewBox.baseVal.width,
          shown: svg.getBoundingClientRect().width,
          box: host.clientWidth,
          left: host.scrollLeft,
          pane: pane.scrollWidth - pane.clientWidth,
          page: document.documentElement.scrollWidth - innerWidth,
        };
      }, [PANE, tag]);
      assert(Math.abs(wide.shown - wide.natural) < 1 && wide.natural > wide.box && wide.left > 0 && wide.pane <= 0 && wide.page <= 0, `the wide diagram shrank or did not scroll on its own: ${JSON.stringify(wide)}`);
    } finally {
      await page.setViewportSize({ width: 1360, height: 900 });
      await page.waitForFunction((s) => document.querySelector(s).clientWidth > 390, PANE, { timeout: 6000 });
    }
  });

  await run("relative links and images point into the project, none leads out of it", async () => {
    await pixelLoaded(page);
    const before = await page.evaluate(() => [location.href, history.length, document.querySelectorAll(".editor-tab[data-path]").length]);
    await page.locator(`${PANE} a`, { hasText: "a way out" }).click();
    await sleep(500);
    const after = await page.evaluate(() => [location.href, history.length, document.querySelectorAll(".editor-tab[data-path]").length]);
    assert(JSON.stringify(after) === JSON.stringify(before), `the link out of the project did something: ${JSON.stringify({ before, after })}`);
    await page.locator(`${PANE} a`, { hasText: "the docs folder" }).click();
    await page.waitForFunction((d) => {
      const row = document.querySelector(`.editor-dir.selected[data-path="${d}"]`);
      const child = document.querySelector(`.editor-file[data-path="${d}/pixel.png"]`);
      return row?.checkVisibility() && child?.checkVisibility();
    }, DOCS, { timeout: 8000 });
    await page.locator(`${PANE} a`, { hasText: "the guide" }).click();
    await page.waitForSelector(`${tabSel(GUIDE)}.active`, { timeout: 8000 });
    await page.waitForFunction((s) => {
      const heading = document.getElementById("user-content-install");
      if (!heading?.closest(s)) return false;
      const box = heading.getBoundingClientRect();
      const frame = document.querySelector(s).getBoundingClientRect();
      return box.top >= frame.top && box.bottom <= frame.bottom;
    }, PANE, { timeout: 8000 });
    assert(page.url() === before[0], `the guide link moved the page to ${page.url()}`);
    await page.evaluate((sel) => document.querySelector(`${sel} .editor-tab-state`)?.click(), tabSel(GUIDE));
    await page.waitForFunction((sel) => !document.querySelector(sel), tabSel(GUIDE), { timeout: 6000 });
    await page.click(tabSel(file));
    await visible(page, "summary", true);
    await page.evaluate(async ([proj, docs]) => {
      const headers = { "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]').content };
      await fetch(`/projects/${proj}/editor/delete`, { method: "POST", headers, body: new URLSearchParams({ path: docs }) });
    }, [project, DOCS]);
  });

  await run("the preview's file goes away with the check", async () => {
    await openRowMenu(page, tabSel(file));
    await menuItem(page, "Hide preview").click();
    await page.waitForFunction((s) => document.querySelector(s).hidden, PANE, { timeout: 6000 });
    await openRowMenu(page, tabSel(file));
    await menuItem(page, "Delete").click();
    await confirmSwal(page);
    await page.waitForFunction((p) => !document.querySelector(`.editor-tab[data-path="${p}"]`), file, { timeout: 8000 });
  });
}

module.exports = { gfmPreviewChecks };
