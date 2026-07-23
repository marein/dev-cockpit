const L = require("./lib");
const { assert, sleep } = L;

// Claude login link prompt: claude's /login screen prints its OAuth URL hard
// wrapped (every screen row ends in a real newline), so selection copy carries
// the breaks and no linkifier matches the rows. terminal-attach rejoins the
// rows client side (URL starts at column zero on an https://claude.com|ai/
// ...oauth... row, continuation rows fill the exact pane width with URL
// characters only, bottom-most match wins) and auto-opens a dialog offering
// Open (new tab) / Copy link / Cancel. One prompt per distinct URL, so
// claude's redraws of the same screen stay silent; a fresh /login carries a
// new state parameter and prompts again. Purely client side, no server
// endpoint. Gotchas: checks emulate the hard wrap with `fold -w $COLUMNS`
// and split the "claude" literal in the typed command ("https://claude"".com")
// so the command echo itself cannot match; `clear` between checks keeps stale
// URLs from re-prompting via the bottom-most rule.

const PAD = (c, n) => `$(printf '${c}%.0s' {1..${n}})`;
const fakeLoginCmd = (state, padChar) =>
  `printf '%s\\n' "https://claude"".com/cai/oauth/authorize?code=true&client_id=abc&state=${state}${PAD(padChar, 150)}" | fold -w "$COLUMNS"`;
const fakeLoginUrl = (state, padChar) =>
  `https://claude.com/cai/oauth/authorize?code=true&client_id=abc&state=${state}${padChar.repeat(150)}`;

L.runFeature("LOGIN-LINK", async ({ engine, page, run, mobilePage }) => {
  const tag = `ll-${Date.now().toString(36)}`;
  const project = `zztc-${tag}`;
  let shellUrl = null;

  const typeCmd = async (p, cmd) => {
    await p.click("#terminal");
    await p.keyboard.type(cmd);
    await p.keyboard.press("Enter");
  };
  const waitDialog = async (p) => {
    await p.waitForSelector(".swal2-popup", { timeout: 8000 });
    const title = (await p.locator(".swal2-title").textContent()) || "";
    assert(title.includes("Claude login"), `unexpected dialog: ${title}`);
  };
  const dialogGone = (p) => p.waitForFunction(() => !document.querySelector(".swal2-popup:not(.swal2-toast)"), { timeout: 8000 });

  try {
    await L.createProject(page, project);

    await run("hard wrapped claude OAuth URL -> auto prompt, Copy joins the URL", async () => {
      shellUrl = await L.createShell(page, project);
      await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
      await sleep(1500);
      await typeCmd(page, fakeLoginCmd(`${tag}-one`, "a"));
      await waitDialog(page);
      await page.click(".swal2-deny");
      await dialogGone(page);
      if (engine !== "chromium") return "copy assert skipped (no clipboard grant)";
      await page.waitForSelector(".swal2-toast", { timeout: 8000 });
      const toast = (await page.locator(".swal2-toast").textContent()) || "";
      assert(toast.includes("Login link copied."), `unexpected toast: ${toast}`);
      const clip = await page.evaluate(() => navigator.clipboard.readText());
      assert(clip === fakeLoginUrl(`${tag}-one`, "a"), `clipboard mismatch: ${clip.slice(0, 90)}...`);
      await page.locator(".swal2-toast .swal2-close").click().catch(() => {});
    });

    await run("same URL on screen never re-prompts", async () => {
      await typeCmd(page, "echo idle");
      await sleep(3000);
      assert(!(await page.$(".swal2-popup:not(.swal2-toast)")), "dialog re-appeared for the same URL");
    });

    await run("new login URL prompts again, Open opens the link in a new tab", async () => {
      await typeCmd(page, "clear");
      await sleep(800);
      await typeCmd(page, fakeLoginCmd(`${tag}-two`, "b"));
      await waitDialog(page);
      const popupPromise = page.context().waitForEvent("page", { timeout: 8000 });
      await page.click(".swal2-confirm");
      const popup = await popupPromise;
      await popup.waitForLoadState("domcontentloaded").catch(() => {});
      assert(/claude\.(com|ai)\//.test(popup.url()), `popup url: ${popup.url()}`);
      await popup.close();
      await dialogGone(page);
    });

    await run("cancel dismisses without side effects", async () => {
      await typeCmd(page, "clear");
      await sleep(800);
      await typeCmd(page, fakeLoginCmd(`${tag}-three`, "c"));
      await waitDialog(page);
      await page.click(".swal2-cancel");
      await dialogGone(page);
      await sleep(1500);
      assert(!(await page.$(".swal2-popup:not(.swal2-toast)")), "dialog came back after cancel");
    });

    await run("non-claude URLs never prompt", async () => {
      await typeCmd(page, "clear");
      await sleep(800);
      await typeCmd(page, `printf '%s\\n' "https://example"".com/oauth/authorize?state=${tag}${PAD("d", 150)}" | fold -w "$COLUMNS"`);
      await sleep(2500);
      assert(!(await page.$(".swal2-popup:not(.swal2-toast)")), "dialog for non-claude URL");
    });

    await run("mobile: prompt appears on the mirror page too", async () => {
      await typeCmd(page, "clear");
      await sleep(500);
      await page.goto(`${L.BASE}/projects`, { waitUntil: "domcontentloaded" });
      const mp = await mobilePage();
      await mp.goto(shellUrl, { waitUntil: "domcontentloaded" });
      await mp.waitForSelector("#terminal-cursor-input", { timeout: 12000 });
      await sleep(1500);
      await mp.evaluate(() => document.getElementById("terminal-cursor-input").focus());
      await mp.keyboard.type(fakeLoginCmd(`${tag}-mob`, "e"));
      await mp.keyboard.press("Enter");
      await waitDialog(mp);
      await mp.click(".swal2-cancel");
      await dialogGone(mp);
    });
  } finally {
    if (shellUrl) await L.deleteShell(page, shellUrl).catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
  }
});
