const L = require("./lib");
const { assert, BASE, sleep } = L;

// The cockpit's own ssh agent (internal/sshagent), Settings > SSH agent
// (/settings/ssh-agent, dc-ssh-keys) and its question in the app wide dialog
// (@dc/gitprompt, kind ssh-key). A shell reaches the agent through the
// SSH_AUTH_SOCK of its session, and the runner signs from a real shell with
// `ssh-keygen -Y sign` against a copy of the public key alone, so only the
// agent can sign and nothing ever falls back to a terminal prompt.
//
// What this runner proves:
//   - the page lists the key with a passphrase as locked and the plain key as
//     not the agent's, with the default policies
//   - deny: the shell's signature fails at once and no question comes up
//   - ask with a locked key: the dialog names the shell, the key and the
//     command, a wrong passphrase asks again, the right one opens the key
//     and the signature goes through
//   - the open key shows on a settings page that was already open, live
//   - ask with an open key: the dialog only asks for a yes, Deny fails the
//     signature, Allow lets it through
//   - allow with an open key signs without a question
//   - Forget locks the key again, the next signature asks for the passphrase
//   - a time out of range is refused
//
// It needs an instance of its own started without SSH_AUTH_SOCK and with HOME
// at a scratch directory whose .ssh holds id_ed25519 with the passphrase in
// PASSPHRASE (default e2e-agent-pass) and id_ecdsa without one, see the README.
// It restores the default policies and forgets the key at the end.
const PASSPHRASE = process.env.PASSPHRASE || "e2e-agent-pass";

const keyRow = (page, state) => page.locator(`dc-ssh-keys [data-ssh-key] [data-ssh-key-state="${state}"]`);

// flashSays waits for the page's notice, the boosted form swaps the page in
// after the response.
async function flashSays(page, text) {
  await page.waitForFunction((t) => (document.querySelector("#settings-ssh-agent")?.innerText || "").includes(t), text, { timeout: 10000 })
    .catch(async () => {
      throw new Error(`no "${text}" on the page: ${(await page.locator("#settings-ssh-agent").innerText()).slice(0, 160)}`);
    });
}

// savePolicies saves the policy form and waits for what the page says to it.
// unchecked skips the browser's own field checks, so the server's rules meet
// a value a page would not send.
async function savePolicies(page, { ui = "allow", shell = "ask", coder = "ask", ttl = "60", unchecked = false, expect = "Settings saved." } = {}) {
  await page.goto(`${BASE}/settings/ssh-agent`, { waitUntil: "domcontentloaded" });
  const form = "#settings-ssh-agent-policy";
  if (unchecked) await page.evaluate((f) => { document.querySelector(f).noValidate = true; }, form);
  await page.selectOption(`${form} [name="policy_ui"]`, ui);
  await page.selectOption(`${form} [name="policy_shell"]`, shell);
  await page.selectOption(`${form} [name="policy_coder"]`, coder);
  await page.fill(`${form} [name="ttl_minutes"]`, ttl);
  await Promise.all([
    page.waitForResponse((r) => r.url().includes("/settings/ssh-agent") && r.request().method() === "POST", { timeout: 15000 }),
    page.click(`${form} button[type="submit"]`),
  ]);
  await flashSays(page, expect);
}

async function mirror(page) {
  return page.evaluate(() => document.querySelector(".attach-selection")?.textContent || "");
}

// sign types one signature into the shell. Its two outcomes print R<n>OK and
// R<n>NO through arithmetic, so the typed line itself never matches them.
async function sign(page, shellUrl, n) {
  if (page.url() !== shellUrl) await page.goto(shellUrl, { waitUntil: "domcontentloaded" });
  await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
  await sleep(600);
  await page.click("#terminal .xterm-screen");
  const line = `T=$(mktemp -d); cp ~/.ssh/id_ed25519.pub $T/k.pub; echo hi > $T/m; ssh-keygen -Y sign -n file -f $T/k.pub $T/m >/dev/null 2>&1 && echo R$((${n}+0))OK || echo R$((${n}+0))NO`;
  await page.keyboard.type(line);
  await page.keyboard.press("Enter");
}

async function outcome(page, n, timeout = 20000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    const text = await mirror(page);
    if (text.includes(`R${n}OK`)) return "OK";
    if (text.includes(`R${n}NO`)) return "NO";
    await sleep(300);
  }
  throw new Error(`the shell never printed the outcome of signature ${n}`);
}

const dialog = (page) => page.locator(".swal2-popup:not(.swal2-toast):has([data-ssh-key-details])");

async function dialogUp(page) {
  await dialog(page).waitFor({ state: "visible", timeout: 20000 });
  const rows = await dialog(page).locator("[data-ssh-key-details] dt, [data-ssh-key-details] dd").allInnerTexts();
  const details = {};
  for (let i = 0; i + 1 < rows.length; i += 2) details[rows[i].trim()] = rows[i + 1].trim();
  return {
    title: (await dialog(page).locator(".swal2-title").innerText()).trim(),
    details,
    command: (await dialog(page).locator("[data-gitprompt-command]").innerText().catch(() => "")).trim(),
    input: await dialog(page).locator("input.swal2-input").isVisible().catch(() => false),
  };
}

async function dialogGone(page) {
  await dialog(page).waitFor({ state: "detached", timeout: 10000 }).catch(async () => {
    await dialog(page).waitFor({ state: "hidden", timeout: 2000 });
  });
}

// settle denies a question a failed check left standing, its backdrop would
// swallow every click of the next one.
async function settle(page) {
  for (let i = 0; i < 5 && await dialog(page).isVisible().catch(() => false); i += 1) {
    await dialog(page).locator(".swal2-deny").click().catch(() => {});
    await sleep(500);
  }
}

// forgetAll locks every open key, so a run starts and ends with none, an
// earlier run's open key would answer the first question for it.
async function forgetAll(page) {
  await settle(page);
  await page.goto(`${BASE}/settings/ssh-agent`, { waitUntil: "domcontentloaded" });
  for (let i = 0; i < 5 && await page.locator("[data-ssh-key-forget]").count(); i += 1) {
    await page.locator("[data-ssh-key-forget]").first().click();
    await flashSays(page, "The key was forgotten.");
  }
}

L.runFeature("SSH AGENT", async ({ engine, ctx, page, run }) => {
  const project = `zzsa-${engine}-${Date.now().toString(36)}`;
  let shellUrl = null;
  try {
    await L.createProject(page, project);
    await forgetAll(page);
    await savePolicies(page, {});

    await run("the page lists the protected key locked, the plain one as not the agent's, with the defaults", async () => {
      await page.goto(`${BASE}/settings/ssh-agent`, { waitUntil: "domcontentloaded" });
      assert((await L.waitUpgraded(page, ["dc-ssh-keys"])).length === 0, "dc-ssh-keys did not upgrade");
      const rows = await page.locator("dc-ssh-keys [data-ssh-key]").allInnerTexts();
      assert(rows.length === 2, `the page lists ${rows.length} keys: ${rows.join(" | ")}`);
      assert(await keyRow(page, "locked").isVisible(), "the protected key is not shown as locked");
      assert(await keyRow(page, "plain").isVisible(), "the plain key is not shown as having no passphrase");
      const locked = await page.locator('dc-ssh-keys [data-ssh-key]:has([data-ssh-key-state="locked"])').innerText();
      assert(locked.includes("id_ed25519") && locked.includes("SHA256:"), `the locked row reads ${locked}`);
      const policy = (kind) => page.inputValue(`#settings-ssh-agent-policy [name="policy_${kind}"]`);
      assert((await policy("ui")) === "allow" && (await policy("shell")) === "ask" && (await policy("coder")) === "ask",
        "the default policies are not allow, ask, ask");
      assert((await page.inputValue('#settings-ssh-agent-policy [name="ttl_minutes"]')) === "60", "the default time is not 60 minutes");
    });

    await run("deny: a shell's signature fails at once and nothing is asked", async () => {
      await settle(page);
      await savePolicies(page, { shell: "deny" });
      shellUrl = await L.createShell(page, project);
      await sign(page, shellUrl, 1);
      assert((await outcome(page, 1)) === "NO", "a denied shell signed");
      assert(!(await dialog(page).isVisible()), "a question came up for a denied shell");
    });

    await run("ask with a locked key: the dialog names shell, key and command, a wrong passphrase asks again, the right one signs", async () => {
      await settle(page);
      await savePolicies(page, { shell: "ask" });
      await sign(page, shellUrl, 2);
      const first = await dialogUp(page);
      assert(first.title === "Open an SSH key", `the title reads ${first.title}`);
      assert(first.input, "the locked key's question has no passphrase field");
      assert((first.details.Shell || "") !== "" && (first.details.Project || "") === project, `the caller rows read ${JSON.stringify(first.details)}`);
      assert((first.details.Key || "").endsWith("/.ssh/id_ed25519"), `the key reads ${first.details.Key}`);
      assert((first.details.Fingerprint || "").startsWith("SHA256:"), `the fingerprint reads ${first.details.Fingerprint}`);
      assert(first.command.includes("ssh-keygen") && first.command.includes("-Y sign"), `the command reads ${first.command}`);
      await dialog(page).locator("input.swal2-input").fill("not-the-passphrase");
      await dialog(page).locator(".swal2-confirm").click();
      await page.locator("[data-ssh-key-retry]").waitFor({ state: "visible", timeout: 20000 });
      await dialog(page).locator("input.swal2-input").fill(PASSPHRASE);
      await dialog(page).locator(".swal2-confirm").click();
      assert((await outcome(page, 2)) === "OK", "the opened key did not sign");
      await dialogGone(page);
    });

    await run("an open key shows on an open settings page with its time left, live", async () => {
      await settle(page);
      const settings = await ctx.newPage();
      try {
        await settings.goto(`${BASE}/settings/ssh-agent`, { waitUntil: "domcontentloaded" });
        await keyRow(settings, "open").waitFor({ state: "visible", timeout: 10000 });
        await settings.waitForFunction(() => (document.querySelector("[data-ssh-key-left]")?.textContent || "").trim() !== "", null, { timeout: 10000 });
        const left = (await settings.locator("[data-ssh-key-left]").innerText()).trim();
        assert(/^(\d+ min|\d+ h( \d+ min)?) left$/.test(left), `the time left reads "${left}"`);
        await page.bringToFront();
        await settings.locator("[data-ssh-key-forget]").click();
        await flashSays(settings, "The key was forgotten.");
        await keyRow(settings, "locked").waitFor({ state: "visible", timeout: 10000 });
        await sign(page, shellUrl, 3);
        const asked = await dialogUp(page);
        assert(asked.input, "a forgotten key did not ask for the passphrase");
        await dialog(page).locator("input.swal2-input").fill(PASSPHRASE);
        await dialog(page).locator(".swal2-confirm").click();
        assert((await outcome(page, 3)) === "OK", "the key opened again did not sign");
        await keyRow(settings, "open").waitFor({ state: "visible", timeout: 10000 });
      } finally {
        await settings.close().catch(() => {});
      }
    });

    await run("ask with an open key: only a yes is asked, Deny fails and Allow signs", async () => {
      await settle(page);
      await sign(page, shellUrl, 4);
      const asked = await dialogUp(page);
      assert(asked.title === "Sign with an SSH key" && !asked.input, `an open key asked ${asked.title}, field ${asked.input}`);
      await dialog(page).locator(".swal2-deny").click();
      assert((await outcome(page, 4)) === "NO", "a denied signature went through");
      await dialogGone(page);
      await sign(page, shellUrl, 5);
      await dialogUp(page);
      await dialog(page).locator(".swal2-confirm").click();
      assert((await outcome(page, 5)) === "OK", "an allowed signature failed");
    });

    await run("allow: an open key signs without a question", async () => {
      await settle(page);
      await savePolicies(page, { shell: "allow" });
      await sign(page, shellUrl, 6);
      assert((await outcome(page, 6)) === "OK", "an allowed shell did not sign");
      assert(!(await dialog(page).isVisible()), "an allowed shell was asked");
    });

    await run("Forget locks the key and the next signature asks for the passphrase again", async () => {
      await settle(page);
      await page.goto(`${BASE}/settings/ssh-agent`, { waitUntil: "domcontentloaded" });
      await keyRow(page, "open").waitFor({ state: "visible", timeout: 10000 });
      await page.locator("[data-ssh-key-forget]").click();
      await flashSays(page, "The key was forgotten.");
      await keyRow(page, "locked").waitFor({ state: "visible", timeout: 10000 });
      await sign(page, shellUrl, 7);
      const asked = await dialogUp(page);
      assert(asked.input, "allow with a locked key did not ask for the passphrase");
      await dialog(page).locator(".swal2-deny").click();
      assert((await outcome(page, 7)) === "NO", "a declined passphrase still signed");
    });

    await run("a time out of range is refused and changes nothing", async () => {
      await settle(page);
      await savePolicies(page, { shell: "deny", ttl: "99999", unchecked: true, expect: "Keep a key open between 1 and 10080 minutes." });
      assert((await page.inputValue('#settings-ssh-agent-policy [name="policy_shell"]')) === "allow", "a refused save moved the policy");
    });
  } finally {
    await forgetAll(page).catch(() => {});
    await savePolicies(page, {}).catch(() => {});
    if (shellUrl) await L.deleteShell(page, shellUrl).catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
  }
});
