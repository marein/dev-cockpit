const L = require("./lib");
const { assert, BASE, sleep, dismissUpdate } = L;

// Coder login: the account section under the coder settings
// (/settings/coders/<coder>/account) and the dc-coder-login dialog that runs
// the CLI's own login headless (GET/POST /settings/coders/<coder>/login).
// Claude's flow shows an oauth link and takes the pasted code back, copilot's
// shows the device code and finishes on its own. Needs an instance with
// tests/e2e/fakes on the serve PATH and a scratch HOME: the fake claude reads
// its login state from $HOME/.claude/fake-logged-out (seed the marker to start
// logged out; the flow's good code is GOOD-CODE, everything else is refused
// with the recorded complaint). The copilot happy path needs the browser-side
// authorization marker in the instance's HOME, which a runner cannot write, so
// copilot is covered up to the shown code plus cancel.

const claudeLogin = `${BASE}/settings/coders/claude/account`;
const copilotLogin = `${BASE}/settings/coders/copilot/account`;

const visible = async (page, selector) => (await page.locator(selector).count()) > 0 && page.locator(selector).first().isVisible();

// The assistant's own way in, the overlay's copy of the same element. The
// panel is opened from the corner button and a conversation on claude is what
// makes a turn fail with the login sentence, so the coder is picked when the
// host has more than one.
async function openAssistantOnClaude(page) {
  await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
  await dismissUpdate(page);
  // A stored open panel reopens by itself, so the corner is only pressed while
  // the card is still hidden, the same rule the assistant runner follows.
  if (!(await page.locator(".dc-assistant-panel-card:not([hidden])").count())) {
    await page.click("[data-assistant-corner]");
  }
  await page.waitForSelector(".dc-assistant-panel-card:not([hidden]) dc-assistant[ready]", { timeout: 15000 });
  const picker = page.locator('[data-assistant-new="claude"]');
  const onClaude = (await page.locator("dc-assistant").getAttribute("data-assistant-coder")) === "claude";
  if ((await picker.count()) && !onClaude) {
    await page.click('[data-bs-toggle="dropdown"][data-assistant-new-label]');
    await picker.click();
    await page.waitForSelector('dc-assistant[data-assistant-coder="claude"][ready]', { timeout: 15000 });
  }
}

L.runFeature("CODER-LOGIN", async ({ page, run }) => {
  await run("the coder settings carry the account section", async () => {
    await page.goto(claudeLogin, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    assert(await page.locator('[data-coder-sections] a.active:has-text("Account")').count() === 1, "account tab is not active");
    assert(await page.locator("dc-coder-login").count() === 1, "dc-coder-login element missing");
  });

  await run("a not logged in coder gets the hint on the new-coder form", async () => {
    await page.goto(claudeLogin, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    if (!(await visible(page, "[data-login-out]"))) {
      return "claude already logged in, hint not rendered";
    }
    await page.goto(`${BASE}/coders/new`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    const hint = page.locator('[data-coder-login="claude"]');
    assert(await hint.count() === 1, "login hint block missing");
    const picker = page.locator('select[name="coder"]');
    if (await picker.count() === 1) {
      await picker.selectOption("claude");
      assert(await hint.isVisible(), "hint hidden while claude is picked");
      await picker.selectOption("copilot");
      assert(!(await hint.isVisible()), "hint visible while another coder is picked");
    }
  });

  // The overlay is its own world: a turn that failed on a missing login logs
  // the coder in where it failed, without navigating the page underneath.
  await run("a failed assistant turn logs the coder in on the spot", async () => {
    await page.goto(claudeLogin, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    if (!(await visible(page, "[data-login-out]"))) {
      return "claude already logged in, the failing turn cannot be produced";
    }
    await openAssistantOnClaude(page);
    const before = await page.locator('[data-role="assistant"]').count();
    await page.fill("[data-assistant-input]", "does this reach the coder");
    await page.click("[data-assistant-send]");
    await page.waitForFunction(
      (count) => document.querySelectorAll('[data-role="assistant"]').length > count,
      before,
      { timeout: 20000 },
    );
    const error = page.locator("[data-assistant-error]").last();
    await error.waitFor({ state: "visible", timeout: 15000 });
    assert((await error.textContent()).includes("not logged in"), `error text: ${await error.textContent()}`);
    const button = page.locator("dc-assistant [data-login-start]").last();
    await button.waitFor({ state: "visible", timeout: 10000 });
    const url = page.url();
    await button.click();
    await page.waitForSelector('.swal2-html-container a[href*="claude.example.test"]', { timeout: 10000 });
    assert(page.url() === url, "the dialog navigated the page under the overlay");
    await page.fill(".swal2-input", "GOOD-CODE");
    await page.click(".swal2-confirm");
    await page.waitForSelector('.dc-toast:has-text("Logged in.")', { timeout: 10000 });
    await page.waitForSelector(".swal2-container", { state: "detached", timeout: 5000 });
    assert(await page.locator("dc-assistant[ready]").count() === 1, "the overlay did not survive the login");
    assert(!(await button.isVisible()), "the login button stayed after a successful login");
  });

  await run("the claude flow logs in with the pasted code, wrong code first", async () => {
    await page.goto(claudeLogin, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    const startedOut = await visible(page, "[data-login-out]");
    await page.locator("[data-login-start]:visible").first().click();
    const link = page.locator('.swal2-html-container a[href*="claude.example.test"]');
    await link.waitFor({ state: "visible", timeout: 10000 });
    assert((await link.getAttribute("href")).startsWith("https://claude.example.test/oauth/authorize"), "oauth link is not the CLI's URL");
    await page.fill(".swal2-input", "WRONG-CODE");
    await page.click(".swal2-confirm");
    await page.waitForSelector('.swal2-html-container .text-danger:has-text("Invalid code")', { timeout: 10000 });
    await page.fill(".swal2-input", "GOOD-CODE");
    await page.click(".swal2-confirm");
    await page.waitForSelector('.dc-toast:has-text("Logged in.")', { timeout: 10000 });
    await page.waitForSelector(".swal2-container", { state: "detached", timeout: 5000 });
    assert(await visible(page, "[data-login-in]"), "logged-in block did not appear");
    assert((await page.locator("[data-login-account]").textContent()).includes("fake@example.test"), "account name missing");
    return startedOut ? "started logged out" : "started logged in, re-login driven";
  });

  await run("a fresh render agrees with the CLI about the state", async () => {
    await page.goto(claudeLogin, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    assert(await visible(page, "[data-login-in]"), "account page does not show the login");
    assert(await page.locator('[data-login-start]:has-text("Log in again")').count() === 1, "re-login button missing");
  });

  await run("the copilot flow shows the device code and cancels cleanly", async () => {
    await page.goto(copilotLogin, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    await page.locator("[data-login-start]:visible").first().click();
    await page.waitForSelector('.swal2-html-container:has-text("FAKE-1234")', { timeout: 10000 });
    const link = page.locator('.swal2-html-container a[href*="github.example.test"]');
    assert(await link.count() === 1, "device link missing");
    assert(!(await page.locator(".swal2-input").isVisible()), "the device flow must not ask for a code");
    await page.click(".swal2-cancel");
    await page.waitForSelector(".swal2-container", { state: "detached", timeout: 5000 });
    await sleep(1200);
    assert(await page.locator(".swal2-container").count() === 0, "the cancelled dialog came back");
  });
});
