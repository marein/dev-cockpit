const L = require("./lib");
const { assert, BASE, sleep, confirmSwal, dismissUpdate } = L;

// The assistant settings page (/settings/assistant) and its one section today,
// Telegram. The page is built from a list of sections, and every section owns
// its forms and its own POST target (/settings/assistant/telegram, whose GET
// renders the page again so the form path pairing holds). Checked here: the
// bot token as a write only field that shows stored or not set, a pairing code
// with its remaining lifetime, the two choices for what the bot sends, the
// channel switch, and the rule that matters most, that the token never comes
// back to the browser.
//
// The instance MUST be started with DEV_COCKPIT_TELEGRAM_API_URL pointing
// somewhere dead, for example http://127.0.0.1:1: saving a token starts the
// poller, and without the override it would poll api.telegram.org. The runner
// removes the token again at the end, so the instance is left as it was found.

const PAGE = `${BASE}/settings/assistant`;
const SECTION = "/settings/assistant/telegram";
// Shaped like what BotFather hands out, so the form's format check passes. It
// is not a real token and never reaches Telegram, see the note above.
const TOKEN = "123456789:AAtest-e2e-token-value-000000";

const html = (page) => page.content();
const section = (page) => page.locator("#settings-telegram").evaluate((el) => el.textContent);

async function open(page) {
  await page.goto(PAGE, { waitUntil: "domcontentloaded" });
  await dismissUpdate(page);
}

async function post(page, form) {
  const locator = page.locator(`form:has(input[value="${form}"])`).first();
  await Promise.all([page.waitForURL(/\/settings\/assistant/, { timeout: 15000 }), locator.locator('button[type="submit"]').first().click()]);
  await sleep(200);
}

const selected = (page, name) => page.locator(`select[name="${name}"]`).inputValue();

L.runFeature("ASSISTANT-TELEGRAM", async ({ page, run }) => {
  await run("the settings sidebar carries the assistant page", async () => {
    await page.goto(`${BASE}/settings/general`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    const entry = page.locator('[data-settings-nav] a[href="/settings/assistant"]');
    assert(await entry.count() === 1, "no Assistant entry in the settings sidebar");
    assert((await entry.innerText()).trim() === "Assistant", "the entry is not labelled Assistant");
    await Promise.all([page.waitForURL(/\/settings\/assistant/, { timeout: 15000 }), entry.click()]);
    assert(await page.locator('[data-settings-nav] a[href="/settings/assistant"].active').count() === 1, "the entry is not marked active");
    assert(await page.locator("h3", { hasText: "Telegram" }).count() === 1, "the Telegram section is missing");
  });

  await run("the section owns its forms and its own path", async () => {
    await open(page);
    const forms = await page.locator("#settings-telegram form").count();
    assert(forms >= 4, `the section has ${forms} forms, want one per thing it saves`);
    const foreign = await page.locator(`#settings-telegram form:not([action="${SECTION}"])`).count();
    assert(foreign === 0, "a form of the section posts somewhere else");
    // The GET of the section path renders the page, which is what a login
    // redirect after a POST lands on.
    await page.goto(`${BASE}${SECTION}`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    assert(await page.locator("#settings-telegram").count() === 1, "the section path does not render the page");
  });

  await run("without a token the page says so and offers no pairing code", async () => {
    await open(page);
    assert((await page.locator("[data-telegram-token-state]").innerText()).trim() === "Not set", "the token state does not say Not set");
    const text = await section(page);
    assert(/No bot token/.test(text), `the status line does not name the missing token: ${text.slice(0, 120)}`);
    assert(await page.locator("[data-telegram-code]").count() === 0, "a pairing code is shown without a token");
  });

  await run("a rejected token is refused with a sentence", async () => {
    await open(page);
    await page.fill('input[name="token"]', "not-a-token");
    await post(page, "telegram-token");
    assert(/does not look like a bot token/.test(await section(page)), "a malformed token was accepted without a word");
    assert((await page.locator("[data-telegram-token-state]").innerText()).trim() === "Not set", "a malformed token was stored");
  });

  await run("a saved token shows as stored and never comes back to the browser", async () => {
    await open(page);
    await page.fill('input[name="token"]', TOKEN);
    await post(page, "telegram-token");
    assert((await page.locator("[data-telegram-token-state]").innerText()).trim() === "Stored", "the token state does not say Stored");
    const body = await html(page);
    assert(!body.includes(TOKEN), "the token came back in the HTML");
    assert(!body.includes("AAtest-e2e"), "part of the token came back in the HTML");
    // A reload renders from the stored state, which is the second place a
    // token could leak into.
    await open(page);
    assert(!(await html(page)).includes(TOKEN), "the token is in the rendered page after a reload");
    assert((await page.locator("[data-telegram-token-state]").innerText()).trim() === "Stored", "the token was not kept");
  });

  await run("an empty token field keeps what is stored", async () => {
    await open(page);
    await post(page, "telegram-token");
    assert(/was kept/.test(await section(page)), "an empty save said nothing about keeping the token");
    assert((await page.locator("[data-telegram-token-state]").innerText()).trim() === "Stored", "an empty save dropped the token");
  });

  await run("a pairing code is created and says how long it works", async () => {
    await open(page);
    await post(page, "telegram-pair");
    const code = (await page.locator("[data-telegram-code]").innerText()).trim();
    assert(/^[A-Z0-9]{8}$/.test(code), `the code does not look like one: ${code}`);
    const text = await section(page);
    assert(text.includes(code), "the code is not shown in the section");
    assert(/minutes?\./.test(text), `the remaining lifetime is missing: ${text.slice(0, 200)}`);
    // A second code replaces the first one, so a code left on a screen is
    // worth nothing.
    await post(page, "telegram-pair");
    const second = (await page.locator("[data-telegram-code]").innerText()).trim();
    assert(second !== code, "the new code is the old one");
  });

  await run("what goes to the chat is two choices, both kept", async () => {
    await open(page);
    assert(await selected(page, "answers") === "", "answers does not default to everything");
    assert(await selected(page, "reports") === "", "job reports do not default to everything");
    await page.selectOption('select[name="answers"]', "telegram");
    await page.selectOption('select[name="reports"]', "telegram");
    await post(page, "telegram-delivery");
    assert(await selected(page, "answers") === "telegram", "the answers choice was not kept");
    assert(await selected(page, "reports") === "telegram", "the job reports choice was not kept");
    await open(page);
    assert(await selected(page, "answers") === "telegram", "the answers choice did not survive a reload");
    // Back to the default, so the instance is left as it was found.
    await page.selectOption('select[name="answers"]', "");
    await page.selectOption('select[name="reports"]', "");
    await post(page, "telegram-delivery");
    assert(await selected(page, "answers") === "", "the answers choice could not be widened again");
  });

  await run("the channel switch turns the bot off without losing the token", async () => {
    await open(page);
    await page.uncheck('input[name="enabled"]');
    await post(page, "telegram-enabled");
    assert(/switched off/.test(await section(page)), "the status line does not say the channel is off");
    assert((await page.locator("[data-telegram-token-state]").innerText()).trim() === "Stored", "switching off lost the token");
    await page.check('input[name="enabled"]');
    await post(page, "telegram-enabled");
    assert(/running/.test(await section(page)), "the channel did not come back on");
  });

  await run("the token can be removed again", async () => {
    await open(page);
    await page.locator('form:has(input[value="telegram-token-clear"]) button[type="submit"]').first().click();
    await confirmSwal(page);
    await page.waitForURL(/\/settings\/assistant/, { timeout: 15000 });
    await sleep(200);
    assert((await page.locator("[data-telegram-token-state]").innerText()).trim() === "Not set", "the token survived the remove");
    assert(await page.locator('form:has(input[value="telegram-token-clear"])').count() === 0, "the remove button is offered without a token");
  });
});
