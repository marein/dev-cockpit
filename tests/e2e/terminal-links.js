const L = require("./lib");
const { assert, sleep } = L;

// Links in the live terminal: OSC 8 hyperlinks a program prints reach xterm
// through the stream filter (deltas) and the pane capture (snapshot), and the
// terminal's linkHandler opens them. A file:// URL inside a project goes to
// GET /terminal-link, which answers the project's editor URL
// (/projects/:name/editor?file=<relative>) or 404 outside every project; the
// editor opens the file from data-editor-file. http(s) opens a new tab, any
// other scheme nothing, a relative or protocol relative address neither, also
// not with Ctrl or Shift. Ctrl opens a project file in a new tab. The
// throwaway shell stands in for claude: it prints
// the bytes claude writes for a tool header, an id parameter and BEL
// terminated. Desktop: a click opens, a drag selects, a click beside a link
// focuses like before. Touch: the scroll zone eats the touch's mouse events, a
// tap opens the link the buffer holds under the finger and leaves the keyboard
// alone, also on a row printed again after a tap; a tap beside a link toggles
// the keyboard, a swipe opens nothing. SHOTS_DIR and VIDEO_DIR, when set,
// collect a desktop screenshot and a 360px touch video.
const SHOTS_DIR = process.env.SHOTS_DIR || "";
const VIDEO_DIR = process.env.VIDEO_DIR || "";
const SHOT_PREFIX = process.env.SHOT_PREFIX || "terminal-links";

const ROWS = ["Update(links.go)", "Read(/etc/hostname)", "Security guide", "Relative settings", "Protocol relative", "PLAIN-NO-LINK-TEXT"];

const printCommand = () => [
  "clear; printf '",
  "\\033[1mUpdate\\033[22m(\\033]8;id=1j518kd;file://%s/links.go\\007links.go\\033]8;;\\007)\\n",
  "\\033[1mRead\\033[22m(\\033]8;id=th1gw7;file:///etc/hostname\\007/etc/hostname\\033]8;;\\007)\\n",
  "\\033]8;id=zaxmda;https://example.invalid/security\\007Security guide\\033]8;;\\007\\n",
  "\\033]8;;/settings\\007Relative settings\\033]8;;\\007\\n",
  "\\033]8;;//example.com/x\\007Protocol relative\\033]8;;\\007\\n",
  "PLAIN-NO-LINK-TEXT\\n",
  "' \"$PWD\"",
].join("");

// cellPoint is the viewport point of a cell, found by the text on its row.
async function cellPoint(page, rowText, col) {
  return page.evaluate(({ rowText, col }) => {
    const screen = document.querySelector("#terminal .xterm-screen");
    const layer = screen.querySelector(".attach-selection");
    const lines = (layer.textContent || "").split("\n");
    const row = lines.findIndex((line) => line.startsWith(rowText));
    if (row < 0) return null;
    const style = getComputedStyle(layer);
    const probe = document.createElement("span");
    probe.style.cssText = `position:absolute;visibility:hidden;white-space:pre;font-family:${style.fontFamily};font-size:${style.fontSize}`;
    probe.textContent = "0".repeat(64);
    document.body.appendChild(probe);
    const cellWidth = probe.getBoundingClientRect().width / 64 + parseFloat(style.letterSpacing || "0");
    probe.remove();
    const rect = screen.getBoundingClientRect();
    return { x: rect.left + (col + 0.5) * cellWidth, y: rect.top + (row + 0.5) * parseFloat(style.lineHeight) };
  }, { rowText, col });
}

async function waitLine(page, rowText) {
  for (let i = 0; i < 40; i++) {
    if (await cellPoint(page, rowText, 0)) return;
    await sleep(300);
  }
  throw new Error(`no row starts with ${JSON.stringify(rowText)}`);
}

async function waitRows(page) {
  let text = "";
  for (let i = 0; i < 30; i++) {
    text = await page.evaluate(() => document.querySelector(".attach-selection")?.textContent || "");
    if (ROWS.every((row) => text.includes(row))) return;
    await sleep(300);
  }
  throw new Error(`the link rows never showed up: ${JSON.stringify(text.trim())}`);
}

function watchLinkRequests(page) {
  const seen = [];
  const on = (r) => { if (r.url().includes("/terminal-link?")) seen.push(r.url()); };
  page.on("request", on);
  return { seen, stop: () => page.off("request", on) };
}

async function stays(page, url, ms = 1500) {
  await sleep(ms);
  assert(page.url() === url, `navigated to ${page.url()}`);
}

async function editorOpened(page, project) {
  await page.waitForURL(new RegExp(`/projects/${project}/editor\\?file=links\\.go$`), { timeout: 10000 });
  await page.waitForFunction(() => [...document.querySelectorAll("[data-editor-tabs] *")].some((el) => el.textContent.trim() === "links.go"), null, { timeout: 10000 });
}

L.runFeature("TERMINAL LINKS", async ({ engine, browser, page, run }) => {
  const tag = `tl-${engine}-${Date.now().toString(36)}`;
  const project = `zztc-${tag}`;
  let shellUrl = null;
  let mobileCtx = null;
  try {
    await L.createProject(page, project);
    shellUrl = await L.createShell(page, project);
    await L.waitUpgraded(page, ["terminal-attach"], 12000);
    await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 10000 });
    await sleep(1200);
    await page.click("#terminal .xterm-screen");
    await page.keyboard.type("printf 'package links\\n' > links.go");
    await page.keyboard.press("Enter");
    await page.keyboard.type(printCommand());
    await page.keyboard.press("Enter");
    await waitRows(page);

    await run("desktop: hovering a link shows the pointer, plain text does not", async () => {
      const link = await cellPoint(page, "Update(", 9);
      await page.mouse.move(link.x, link.y);
      await page.waitForFunction(() => document.querySelector("#terminal .xterm-screen").classList.contains("xterm-cursor-pointer"), null, { timeout: 4000 });
      const plain = await cellPoint(page, "PLAIN-NO-LINK-TEXT", 4);
      await page.mouse.move(plain.x, plain.y);
      await page.waitForFunction(() => !document.querySelector("#terminal .xterm-screen").classList.contains("xterm-cursor-pointer"), null, { timeout: 4000 });
    });

    await run("desktop: a drag over a link selects and opens nothing", async () => {
      const watch = watchLinkRequests(page);
      const from = await cellPoint(page, "Update(", 7);
      const to = await cellPoint(page, "Update(", 16);
      await page.mouse.move(from.x, from.y);
      await page.mouse.down();
      await page.mouse.move(to.x, to.y, { steps: 8 });
      await page.mouse.up();
      await page.keyboard.press("Control+c");
      await sleep(300);
      const copied = await page.evaluate(() => navigator.clipboard.readText().catch(() => ""));
      watch.stop();
      assert(copied.includes("links.go)"), `the drag selected ${JSON.stringify(copied)}`);
      assert(watch.seen.length === 0, "the drag asked for a link");
      await stays(page, shellUrl, 500);
    });

    await run("desktop: a drag inside a link selects and opens nothing", async () => {
      const watch = watchLinkRequests(page);
      const from = await cellPoint(page, "Update(", 8);
      const to = await cellPoint(page, "Update(", 13);
      await page.mouse.move(from.x, from.y);
      await page.mouse.down();
      await page.mouse.move(to.x, to.y, { steps: 8 });
      await page.mouse.up();
      await page.keyboard.press("Control+c");
      await sleep(300);
      const copied = await page.evaluate(() => navigator.clipboard.readText().catch(() => ""));
      watch.stop();
      assert(copied.includes("inks."), `the drag selected ${JSON.stringify(copied)}`);
      assert(watch.seen.length === 0, "the drag asked for a link");
      await stays(page, shellUrl, 800);
    });

    await run("desktop: a click beside a link focuses the terminal and opens nothing", async () => {
      const watch = watchLinkRequests(page);
      await page.evaluate(() => document.activeElement?.blur());
      const plain = await cellPoint(page, "PLAIN-NO-LINK-TEXT", 4);
      await page.mouse.click(plain.x, plain.y);
      await stays(page, shellUrl, 800);
      watch.stop();
      assert(watch.seen.length === 0, "a plain click asked for a link");
      assert(await page.evaluate(() => Boolean(document.querySelector("#terminal .xterm.focus"))), "the terminal did not take focus");
    });

    await run("desktop: a file link outside every project opens nothing", async () => {
      const answer = page.waitForResponse((r) => r.url().includes("/terminal-link?"), { timeout: 5000 });
      const link = await cellPoint(page, "Read(", 8);
      await page.mouse.click(link.x, link.y);
      assert((await answer).status() === 404, "the outside link was not refused");
      await stays(page, shellUrl);
    });

    await run("desktop: a web link opens a new tab", async () => {
      const popup = page.context().waitForEvent("page", { timeout: 5000 });
      const link = await cellPoint(page, "Security guide", 3);
      await page.mouse.click(link.x, link.y);
      const tab = await popup;
      await tab.close();
      assert(page.url() === shellUrl, "the web link moved the terminal page");
    });

    await run("desktop: a relative or protocol relative link opens nothing, with Ctrl or Shift neither", async () => {
      const popups = [];
      const onPage = (p) => popups.push(p.url());
      page.context().on("page", onPage);
      for (const row of ["Relative settings", "Protocol relative"]) {
        const link = await cellPoint(page, row, 3);
        await page.mouse.move(link.x, link.y);
        await page.waitForFunction(() => document.querySelector("#terminal .xterm-screen").classList.contains("xterm-cursor-pointer"), null, { timeout: 4000 });
        for (const modifier of [null, "Control", "Shift"]) {
          if (modifier) await page.keyboard.down(modifier);
          await page.mouse.click(link.x, link.y);
          if (modifier) await page.keyboard.up(modifier);
          await stays(page, shellUrl, 800);
        }
      }
      page.context().off("page", onPage);
      assert(popups.length === 0, `a tab opened: ${popups.join(", ")}`);
    });

    await run("desktop: Ctrl+click on a project file link opens it in a new tab", async () => {
      const popup = page.context().waitForEvent("page", { timeout: 10000 });
      const link = await cellPoint(page, "Update(", 9);
      await page.keyboard.down("Control");
      await page.mouse.click(link.x, link.y);
      await page.keyboard.up("Control");
      const tab = await popup;
      await tab.waitForURL(new RegExp(`/projects/${project}/editor\\?file=links\\.go$`), { timeout: 10000 });
      await tab.close();
      await stays(page, shellUrl, 300);
    });

    await run("desktop: a click on a project file link opens it in the editor", async () => {
      const link = await cellPoint(page, "Update(", 9);
      await page.mouse.click(link.x, link.y);
      await editorOpened(page, project);
      if (SHOTS_DIR) {
        await page.goBack();
        await waitRows(page);
        const hover = await cellPoint(page, "Update(", 9);
        await page.mouse.move(hover.x, hover.y);
        await sleep(400);
        await page.screenshot({ path: `${SHOTS_DIR}/${SHOT_PREFIX}-desktop.png` });
      }
    });

    await run("desktop: after a reload the snapshot still carries the link", async () => {
      await page.goto(shellUrl, { waitUntil: "domcontentloaded" });
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 10000 });
      await waitRows(page);
      const link = await cellPoint(page, "Update(", 9);
      await page.mouse.click(link.x, link.y);
      await editorOpened(page, project);
    });

    await page.goto(`${L.BASE}/projects`, { waitUntil: "domcontentloaded" });
    mobileCtx = await browser.newContext({
      ignoreHTTPSErrors: true, hasTouch: true, isMobile: true, viewport: { width: 360, height: 760 },
      ...(VIDEO_DIR ? { recordVideo: { dir: VIDEO_DIR, size: { width: 360, height: 760 } } } : {}),
    });
    const mp = await mobileCtx.newPage();
    L.wirePage(mp, { consoleErrors: [], pageErrors: [], cdnNoise: [] });
    await L.login(mp);
    await mp.goto(shellUrl, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(mp);
    await mp.waitForSelector("#terminal .xterm-screen canvas", { timeout: 10000 });
    await mp.evaluate(() => document.getElementById("terminal-cursor-input").focus());
    await mp.keyboard.type("seq 80", { delay: 30 });
    await mp.keyboard.press("Enter");
    await mp.keyboard.type(printCommand().slice("clear; ".length), { delay: 30 });
    await mp.keyboard.press("Enter");
    await mp.evaluate(() => document.activeElement?.blur());
    await waitRows(mp);
    await sleep(800);

    await run("touch: a tap on plain text opens the keyboard and nothing else", async () => {
      const watch = watchLinkRequests(mp);
      await waitRows(mp);
      const plain = await cellPoint(mp, "PLAIN-NO-LINK-TEXT", 4);
      await mp.touchscreen.tap(plain.x, plain.y);
      await stays(mp, shellUrl, 1000);
      watch.stop();
      assert(watch.seen.length === 0, "a plain tap asked for a link");
      assert(await mp.evaluate(() => document.activeElement?.id === "terminal-cursor-input"), "the tap left the keyboard closed");
    });

    await run("touch: a tap on a relative or protocol relative link opens nothing", async () => {
      await mp.evaluate(() => { window.open = (url) => { window.__terminalLinksOpened = url; return null; }; });
      for (const row of ["Relative settings", "Protocol relative"]) {
        const link = await cellPoint(mp, row, 3);
        await mp.touchscreen.tap(link.x, link.y);
        await stays(mp, shellUrl, 800);
      }
      const opened = await mp.evaluate(() => window.__terminalLinksOpened);
      assert(opened === undefined, `the tap opened ${JSON.stringify(opened)}`);
    });

    await run("touch: a tap on a project file link opens it in the editor, the keyboard stays closed", async () => {
      await mp.evaluate(() => {
        document.activeElement?.blur();
        sessionStorage.removeItem("e2e-keyboard");
        document.getElementById("terminal-cursor-input").addEventListener("focus", () => sessionStorage.setItem("e2e-keyboard", "1"));
      });
      const link = await cellPoint(mp, "Update(", 9);
      await mp.touchscreen.tap(link.x, link.y);
      await editorOpened(mp, project);
      assert(await mp.evaluate(() => sessionStorage.getItem("e2e-keyboard") !== "1"), "the tap opened the keyboard");
      await sleep(1500);
    });

    await run("touch: a swipe that starts on a link scrolls and opens nothing", async () => {
      await mp.goto(shellUrl, { waitUntil: "domcontentloaded" });
      await mp.waitForSelector("#terminal .xterm-screen canvas", { timeout: 10000 });
      await waitRows(mp);
      await sleep(800);
      const watch = watchLinkRequests(mp);
      const scrolled = mp.waitForRequest((r) => /\/input$/.test(r.url()) && /scroll/.test(r.postData() || ""), { timeout: 8000 });
      const start = await cellPoint(mp, "Update(", 9);
      const cdp = await mp.context().newCDPSession(mp);
      const touch = (type, y) => cdp.send("Input.dispatchTouchEvent", { type, touchPoints: type === "touchEnd" ? [] : [{ x: start.x, y, id: 1 }] });
      await touch("touchStart", start.y);
      for (let i = 1; i <= 10; i++) { await touch("touchMove", start.y + i * 20); await sleep(16); }
      await touch("touchEnd", start.y + 200);
      await scrolled;
      await stays(mp, shellUrl);
      watch.stop();
      assert(watch.seen.length === 0, "the swipe asked for a link");
    });

    await run("touch: a tap on a row opens its link, the row printed again with another link, a tap opens the new one", async () => {
      await mp.goto(shellUrl, { waitUntil: "domcontentloaded" });
      await mp.waitForSelector("#terminal .xterm-screen canvas", { timeout: 10000 });
      await sleep(800);
      await mp.evaluate(() => document.getElementById("terminal-cursor-input").focus());
      await mp.keyboard.type("clear; seq 12; printf 'Row \\033]8;;https://example.invalid/old\\007Swap link\\033]8;;\\007\\n'; read -rs; printf '\\033[1A\\r\\033[2KRow \\033]8;;https://example.invalid/new\\007Swap link\\033]8;;\\007 now\\n'", { delay: 30 });
      await mp.keyboard.press("Enter");
      await mp.evaluate(() => document.activeElement?.blur());
      await waitLine(mp, "Row Swap link");
      await mp.evaluate(() => { window.__terminalLinksOpened = []; window.open = (url) => { window.__terminalLinksOpened.push(url); return null; }; });
      const row = await cellPoint(mp, "Row Swap link", 6);
      await mp.touchscreen.tap(row.x, row.y);
      await sleep(600);
      await mp.evaluate(() => document.getElementById("terminal-cursor-input").focus());
      await mp.keyboard.press("Enter");
      await waitLine(mp, "Row Swap link now");
      const now = await cellPoint(mp, "Row Swap link now", 6);
      await mp.touchscreen.tap(now.x, now.y);
      await sleep(800);
      const opened = await mp.evaluate(() => window.__terminalLinksOpened);
      assert(JSON.stringify(opened) === JSON.stringify(["https://example.invalid/old", "https://example.invalid/new"]), `the taps opened ${JSON.stringify(opened)}`);
    });
  } finally {
    if (mobileCtx) await mobileCtx.close().catch(() => {});
    if (shellUrl) { try { await L.deleteShell(page, shellUrl); } catch {} }
    try { await L.deleteProject(page, project); } catch {}
  }
});
