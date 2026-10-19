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
