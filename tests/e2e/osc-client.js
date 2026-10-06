const L = require("./lib");
const { assert, sleep } = L;

// Escape sequences in the browser: the stream hands the pane bytes to xterm
// unchanged, and xterm stays a passive viewer. tmux is the pane's terminal and
// answers every query itself, so no attached browser may answer again: with
// two desktop contexts on one shell, the shell prints every query xterm would
// answer (DSR, CPR, DA1, DA2, DECRQM, window reports, DECRQSS, color and
// clipboard queries, bare and tmux wrapped) and neither page posts any input.
// DECSET and DECRST mixing input modes with harmless ones (?25;1004h,
// ?1049;1000h, ?1004;2004h) apply the cursor, the alt screen and paste, while
// focus changes and mouse use in both pages report nothing.
// OSC 52 copies: a coder sends the copy bare and wrapped in tmux passthrough,
// each page copies it once; the p target, queries and malformed base64 copy
// nothing and the terminal keeps rendering. On a secure origin the copy goes
// through the clipboard API (writeText is counted by an init script). Run the
// file a second time with BASE_URL on plain HTTP at a non loopback address,
// where there is no clipboard API and a copy without a gesture fails: a toast
// with a Copy button stands in, and its click copies. The copy arrives after
// the transient activation of the typed Enter ran out, and nothing evaluates
// in the pages meanwhile, because Playwright's evaluate counts as a gesture.

const b64 = (text) => Buffer.from(text, "utf8").toString("base64");

const QUERIES = [
  "\\033[6n", "\\033[5n", "\\033[?6n", "\\033[c", "\\033[0c", "\\033[>c", "\\033[=c",
  "\\033[2$p", "\\033[?25$p", "\\033[14t", "\\033[16t", "\\033[18t", "\\033[21t",
  "\\033P$qm\\033\\\\", "\\033P$qr\\033\\\\",
  "\\033]4;1;?\\007", "\\033]10;?\\007", "\\033]11;?\\033\\\\", "\\033]12;?\\007",
  "\\033]52;c;?\\007", "\\033Ptmux;\\033\\033]11;?\\007\\033\\\\", "\\033Ptmux;\\033\\033[6n\\033\\\\",
].join("");

async function screenText(page) {
  return page.evaluate(() => document.querySelector("#terminal .attach-selection")?.textContent || "");
}

async function waitText(page, text, timeout = 10000) {
  const until = Date.now() + timeout;
  while (Date.now() < until) {
    if ((await screenText(page)).includes(text)) return;
    await sleep(200);
  }
  throw new Error(`${text} never showed up: ${JSON.stringify((await screenText(page)).trim().slice(-300))}`);
}

function watchInput(page) {
  const seen = [];
  const on = (r) => { if (r.method() === "POST" && /\/input$/.test(r.url())) seen.push(r.postData() || ""); };
  page.on("request", on);
  return { seen, stop: () => page.off("request", on) };
}

async function type(page, command) {
  await page.click("#terminal .xterm-screen");
  await page.keyboard.type(command);
  await page.keyboard.press("Enter");
}

async function openShell(page, url) {
  await page.goto(url, { waitUntil: "domcontentloaded" });
  await L.dismissUpdate(page);
  await L.waitUpgraded(page, ["terminal-attach"], 12000);
  await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 10000 });
  await sleep(1200);
}

const copies = (page) => page.evaluate(() => window.dcCopies || []);

const cursorPixels = (page) => page.evaluate(() => {
  const layer = document.querySelector("#terminal canvas.xterm-cursor-layer");
  const data = layer.getContext("2d").getImageData(0, 0, layer.width, layer.height).data;
  let painted = 0;
  for (let i = 3; i < data.length; i += 4) if (data[i]) painted++;
  return painted;
});

async function copyToasts(page) {
  return page.evaluate(() => [...document.querySelectorAll(".dc-toast")].filter((toast) => {
    const box = toast.getBoundingClientRect();
    return toast.textContent.includes("The terminal copied text") && getComputedStyle(toast).display !== "none" && box.height > 0;
  }).length);
}

L.runFeature("OSC CLIENT", async ({ engine, browser, ctx, page, run }) => {
  const tag = `oc-${engine}-${Date.now().toString(36)}`;
  const project = `zztc-${tag}`;
  const record = () => {
    window.dcCopies = [];
    const write = navigator.clipboard?.writeText?.bind(navigator.clipboard);
    if (write) navigator.clipboard.writeText = (text) => { window.dcCopies.push(text); return write(text); };
  };
  await ctx.addInitScript(record);
  let shellUrl = null;
  let other = null;
  try {
    await L.createProject(page, project);
    shellUrl = await L.createShell(page, project);
    await openShell(page, shellUrl);
    other = await L.newDesktop(browser, engine);
    await other.addInitScript(record);
    const second = await other.newPage();
    L.wirePage(second, { consoleErrors: [], pageErrors: [], cdnNoise: [] });
    await L.login(second);
    await openShell(second, shellUrl);
    const pages = [page, second];
    const secure = await page.evaluate(() => window.isSecureContext && Boolean(navigator.clipboard));
    if (!secure) {
      await ctx.clearPermissions();
      await other.clearPermissions();
    }

    await run("two browsers attached: no query gets a reply typed into the pane", async () => {
      await type(page, `sleep 1; printf '${QUERIES}'; read -rs -t 2 -d '' _; echo QUERIES-$((40+2))`);
      await sleep(600);
      const watches = pages.map(watchInput);
      for (const p of pages) await waitText(p, "QUERIES-42");
      await sleep(1500);
      watches.forEach((w) => w.stop());
      const posted = watches.flatMap((w) => w.seen);
      assert(posted.length === 0, `a browser answered: ${JSON.stringify(posted)}`);
    });

    await run("mixed DECSET and DECRST: no focus or mouse report from two browsers, cursor, alt screen and paste still apply", async () => {
      await type(page, "clear; echo MAIN-$((7*7)); printf '\\033[?25;1004lHIDE\\n'; read -rs -t 3 -d '' _; "
        + "printf '\\033[?1049;1000h\\033[?1004;2004h\\033[?25;1004h'; echo ALT-$((8*8)); read -rs -t 8 -d '' _; "
        + "printf '\\033[?1049;1000l'; echo BACK-$((9*9))");
      await waitText(page, "HIDE");
      const watches = pages.map(watchInput);
      await sleep(600);
      assert(await cursorPixels(page) === 0, "?25;1004l left the cursor painted");
      for (const p of pages) await waitText(p, "ALT-64");
      await sleep(600);
      for (const p of pages) assert(!(await screenText(p)).includes("MAIN-49"), "?1049;1000h left the main screen up");
      assert(await cursorPixels(page) > 0, "?25;1004h left the cursor hidden");
      for (const p of pages) {
        const box = await p.locator("#terminal .xterm-screen").boundingBox();
        await p.evaluate(() => document.activeElement?.blur());
        await p.mouse.move(box.x + 40, box.y + 40);
        await p.mouse.click(box.x + 60, box.y + 50);
        await p.mouse.move(box.x + 120, box.y + 80, { steps: 5 });
        await p.evaluate(() => document.activeElement?.blur());
        await p.mouse.click(box.x + 80, box.y + 30);
      }
      for (const p of pages) await waitText(p, "BACK-81");
      await sleep(800);
      watches.forEach((w) => w.stop());
      const reports = watches.flatMap((w) => w.seen).filter((body) => /\\u001b\[(I|O|M|<)/.test(body));
      assert(reports.length === 0, `a browser reported: ${JSON.stringify(reports)}`);
      for (const p of pages) {
        const text = await screenText(p);
        assert(text.includes("MAIN-49") && !text.includes("ALT-64"), "?1049;1000l did not bring the main screen back");
      }
      await page.click("#terminal .xterm-screen");
      await page.evaluate(() => {
        const data = new DataTransfer();
        data.setData("text/plain", "echo PASTED-$((4*4))");
        document.querySelector("#terminal .xterm-helper-textarea").dispatchEvent(new ClipboardEvent("paste", { clipboardData: data, bubbles: true, cancelable: true }));
      });
      await sleep(500);
      await page.keyboard.press("Enter");
      await waitText(page, "PASTED-16");
    });

    const copied = `copied-${tag}-äö`;
    await run("OSC 52 bare and tmux wrapped copies once per browser", async () => {
      const data = b64(copied);
      await type(page, `sleep 7; printf '\\033]52;c;${data}\\007\\033Ptmux;\\033\\033]52;c;${data}\\007\\033\\\\'; echo COPY-$((1+1))`);
      await sleep(9000);
      for (const p of pages) await waitText(p, "COPY-2");
      await sleep(1500);
      for (const p of pages) {
        if (secure) {
          const seen = await copies(p);
          assert(seen.length === 1 && seen[0] === copied, `writeText saw ${JSON.stringify(seen)}`);
          assert(await copyToasts(p) === 0, "a copy toast showed up on a secure origin");
        } else {
          assert(await copyToasts(p) === 1, `expected one copy toast, got ${await copyToasts(p)}`);
        }
      }
      if (!secure) {
        assert(await page.evaluate(() => Boolean(document.querySelector("#terminal .xterm.focus"))), "the refused copy took the focus from the terminal");
      }
      if (secure) {
        const clip = await page.evaluate(() => navigator.clipboard.readText());
        assert(clip === copied, `the clipboard holds ${JSON.stringify(clip)}`);
      }
    });

    await run("OSC 52 with the p target, queries and malformed base64 copy nothing, the terminal keeps rendering", async () => {
      const before = await Promise.all(pages.map(copyToasts));
      const bad = [
        `52;p;${b64("primary")}`, `52;p!;${b64("primary")}`, "52;c;?", "52;;?", "52;c;QUJ", "52;c;Q@==",
        "52;c;!!!!", "52;c;/w==", "52;x;QUJD", "52",
      ].map((body) => `\\033]${body}\\007`).join("");
      await type(page, `printf '${bad}\\033Ptmux;\\033\\033]52;c;QUJ\\007\\033\\\\'; echo AFTER-MALFORMED-$((3*3))`);
      for (const p of pages) await waitText(p, "AFTER-MALFORMED-9");
      await sleep(1500);
      for (const [i, p] of pages.entries()) {
        if (secure) {
          const seen = await copies(p);
          assert(seen.length === 1, `writeText saw ${JSON.stringify(seen)}`);
        } else {
          assert(await copyToasts(p) === before[i], "a refused copy offered a toast");
        }
      }
      await type(page, "echo STILL-$((5+5))");
      for (const p of pages) await waitText(p, "STILL-10");
    });

    await run("a copy the browser refuses offers a toast whose Copy button copies", async () => {
      if (secure) return "secure origin, the clipboard API copied (run again on plain HTTP)";
      const toast = page.locator(".dc-toast", { hasText: "The terminal copied text" }).first();
      await toast.locator("button", { hasText: "Copy" }).click();
      await page.waitForFunction(() => ![...document.querySelectorAll(".dc-toast")].some((t) => t.textContent.includes("The terminal copied text") && t.getBoundingClientRect().height > 0), null, { timeout: 5000 });
      await sleep(500);
      const refused = await page.evaluate(() => [...document.querySelectorAll(".dc-toast")].some((t) => t.textContent.includes("Clipboard is not available")));
      assert(!refused, "the Copy button could not copy");
      await type(page, "echo TYPED-$((6+6))");
      await waitText(page, "TYPED-12");
    });
  } finally {
    if (other) await other.close().catch(() => {});
    if (shellUrl) { try { await L.deleteShell(page, shellUrl); } catch {} }
    try { await L.deleteProject(page, project); } catch {}
  }
});
