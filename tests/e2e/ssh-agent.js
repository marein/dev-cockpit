const L = require("./lib");
const { assert, BASE, sleep } = L;

// The cockpit's ssh agents (internal/sshagent): one OpenSSH ssh-agent per kind
// of caller behind a relay socket at a fixed path in the state directory,
// keys loaded with ssh-add, Settings > SSH agent (/settings/ssh-agent,
// dc-ssh-keys) and the question in the app wide dialog (@dc/gitprompt, kind
// ssh-key). A shell reaches the shells' socket through its SSH_AUTH_SOCK, an
// assistant's turn the assistants'. The runner signs from a real shell, and
// from a turn of the fake claude that runs a script the shell wrote, with
// `ssh-keygen -Y sign` against a copy of the public key alone, so only an
// agent can sign and nothing ever falls back to a terminal prompt.
//
// What this runner proves:
//   - the page lists the key with a passphrase as locked and the plain key as
//     not the agent's, with the default policies, UI allow and shells, coders
//     and assistants ask, every policy select offers Allow, Ask in the browser
//     and Off, and the hint says what Off does
//   - off: the agent offers the shell no key, its signature fails at once
//     and no question comes up
//   - ask with a locked key: the dialog in the app's dialog design
//     (@dc/prompt-view, sshKeyView) names the kind of caller and how long it
//     waits in its head, the key and its fingerprint as mono rows and the
//     command in a code panel, Deny and Unlock and sign below, a wrong
//     passphrase asks again with a warning, the right one opens the key and
//     the signature goes through
//   - the open key shows on a settings page that was already open, live
//   - a device clock an hour ahead or behind reads the same time left and
//     fetches the keys at most once, the countdown runs on the page's own
//     monotonic clock
//   - ask with an open key: the dialog only asks for a yes, Deny fails the
//     signature, Allow lets it through
//   - two signatures of the shells at once: the question names both commands
//   - allow with an open key signs without a question
//   - each kind signs under its own policy: the shell that points its
//     signature at the coders' socket meets the coders' off
//   - the quiet fetch (`POST .../editor/git/fetch` with auto) runs without
//     the agent: a program the repository names in core.sshCommand finds no
//     socket in its environment, while the key is open and the UI allows,
//     called by the page as by a shell or a coder over the local API. An
//     explicit fetch of the page signs under the UI's allow, the same fetch
//     of a coder over the local API runs without the agent
//   - `dev-cockpit git` from a shell signs under the shells' policy, refused
//     under off and signed under allow
//   - a question names the topmost git command above the signature in git's
//     directory under the head command: the shell's `git pull` and not the
//     fetch it runs, its `dev-cockpit git fetch`, a `git fetch` in a window of
//     the coder's session and the page's Fetch with the options the cockpit
//     gives git; a process named ssh without git above it reads as its own
//     line under the head `ssh for a git push`
//   - the head names who the command runs for, read from the processes above
//     it: the shell or the coder with its project, the assistant by its name;
//     the cockpit's own git (the page's Fetch, a `dev-cockpit git` under the
//     agent) and a signature outside every terminal name nobody and read as
//     before
//   - an assistant's turn signs through the assistants' socket under their
//     policy: off fails it without a question, ask names the assistants and
//     the command and Allow signs, a `git fetch` of the turn names that
//     command in its directory, and its `dev-cockpit git` signs under the
//     assistants' allow while the shells are off and is refused under the
//     assistants' off while the shells allow
//   - under off, where ssh asks for the passphrase itself, the git question
//     (gitQuestionView) names the kind of caller with its icon in its head:
//     `dev-cockpit git` of the shell, of a window of the coder's session and
//     of a turn, the page's Fetch as git action beside `git fetch in`, and
//     nothing extra for a `dev-cockpit git` without the agent's socket or the
//     page's fetch over the local API; its command panel names the shell, the
//     coder or the assistant, also without the agent's socket, and nobody for
//     a `dev-cockpit git` outside every terminal
//   - a restart of the cockpit, which the fake claude does from inside a turn
//     whose SSH_AUTH_SOCK names the assistants' socket: every key reads
//     locked, the ssh-agents of the process before end, and the shell started
//     before signs through the same socket after the passphrase again
//   - Forget locks the key again, the next signature asks for the passphrase
//   - a time out of range is refused
//
// It needs an instance of its own started without SSH_AUTH_SOCK, with HOME
// at a scratch directory whose .ssh holds id_ed25519 with the passphrase in
// PASSPHRASE (default e2e-agent-pass) and id_ecdsa without one, and the fake
// claude and the instance's own binary on PATH, see the README. It restores
// the default policies and forgets the key at the end.
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

const policyLabels = { allow: "Allow", ask: "Ask in the browser", off: "Off" };

// savePolicies picks each policy by the label the page shows and saves the
// form, then waits for what the page says to it. unchecked skips the
// browser's own field checks, so the server's rules meet a value a page would
// not send.
async function savePolicies(page, { ui = "allow", shell = "ask", coder = "ask", assistant = "ask", ttl = "60", unchecked = false, expect = "Settings saved." } = {}) {
  await page.goto(`${BASE}/settings/ssh-agent`, { waitUntil: "domcontentloaded" });
  const form = "#settings-ssh-agent-policy";
  if (unchecked) await page.evaluate((f) => { document.querySelector(f).noValidate = true; }, form);
  await page.selectOption(`${form} [name="policy_ui"]`, { label: policyLabels[ui] });
  await page.selectOption(`${form} [name="policy_shell"]`, { label: policyLabels[shell] });
  await page.selectOption(`${form} [name="policy_coder"]`, { label: policyLabels[coder] });
  await page.selectOption(`${form} [name="policy_assistant"]`, { label: policyLabels[assistant] });
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

async function typeLine(page, shellUrl, line) {
  if (page.url() !== shellUrl) await page.goto(shellUrl, { waitUntil: "domcontentloaded" });
  await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
  await sleep(600);
  await page.click("#terminal .xterm-screen");
  await page.keyboard.type(line);
  await page.keyboard.press("Enter");
}

// sign types one signature into the shell. Its two outcomes print R<n>OK and
// R<n>NO through arithmetic, so the typed line itself never matches them.
// sock, a shell expression, points the signature at another socket than the
// shell's own.
async function sign(page, shellUrl, n, sock = "") {
  const env = sock ? `SSH_AUTH_SOCK=${sock} ` : "";
  await typeLine(page, shellUrl, `T=$(mktemp -d); cp ~/.ssh/id_ed25519.pub $T/k.pub; echo hi > $T/m; ${env}ssh-keygen -Y sign -n file -f $T/k.pub $T/m >/dev/null 2>&1 && echo R$((${n}+0))OK || echo R$((${n}+0))NO`);
}

async function outcome(page, n, words = ["OK", "NO"], timeout = 20000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    const text = await mirror(page);
    const word = words.find((w) => text.includes(`R${n}${w}`));
    if (word) return word;
    await sleep(300);
  }
  throw new Error(`the shell never printed the outcome of signature ${n}`);
}

// fetchRepo turns the shell's project into a repository whose fetch runs a
// program its own config names in place of ssh: it signs a challenge of its
// own through the socket in its environment, and writes SIGNED to $F/out if
// it signed, REFUSED otherwise. D is the agent's directory, S the state
// directory around it, A the local API's socket beside it.
const fetchRepo = "F=$(mktemp -d); D=$(dirname \"$SSH_AUTH_SOCK\"); S=$(dirname \"$D\"); A=$S/api/s; cp ~/.ssh/id_ed25519.pub $F/k.pub; echo hi > $F/m; " +
  "echo '{\"auto\":true}' > $F/auto.json; echo '{\"auto\":false}' > $F/explicit.json; " +
  "printf '#!/bin/sh\\nw=REFUSED\\nif [ -S \"$SSH_AUTH_SOCK\" ]; then\\nrm -f %s/m.sig\\nssh-keygen -Y sign -n file -f %s/k.pub %s/m >/dev/null 2>&1 && w=SIGNED\\nfi\\necho $w > %s/out\\nexit 1\\n' $F $F $F $F > $F/ssh; chmod +x $F/ssh; " +
  "git init -q; git remote remove origin 2>/dev/null; git remote add origin ssh://e2e.invalid/x; git config core.sshCommand $F/ssh; [ -S \"$A\" ] && echo R$((16+0))READY || echo R$((16+0))NOAPI";

// localFetch is the fetch a process sends over the local API, body names
// $F/auto.json or $F/explicit.json.
const localFetch = (project, body) =>
  `curl -s -o /dev/null --unix-socket $A -H Content-Type:application/json --data-binary @$F/${body} http://cockpit/projects/${project}/editor/git/fetch`;

// fetched prints what the planted program of the last fetch did, NEVER when
// no fetch reached it. The quiet fetch only runs on a stale FETCH_HEAD, so it goes first.
const fetchReset = "rm -f $F/out $F/done .git/FETCH_HEAD";
const fetched = (n) => `echo R$((${n}+0))$(cat $F/out 2>/dev/null || echo NEVER)`;
const fetchWords = ["SIGNED", "REFUSED", "NEVER"];

// inCoder runs a command in a window of its own inside the coder's tmux
// session, so the coder's session is who sends it, and waits for its end.
const inCoder = (coder, command) =>
  `tmux new-window -d -t "${coder}:" "${command}; touch $F/done"; until [ -e $F/done ]; do sleep 0.2; done`;

const coderSock = `"$(dirname "$SSH_AUTH_SOCK")/coder"`;

// cliFetch is `dev-cockpit git fetch` the way a terminal types it, in the
// shell's repository, against this instance's state directory.
const cliFetch = "dev-cockpit git --state-dir \"$S\" fetch >/dev/null 2>&1";

// turnScripts writes the three scripts a turn of the fake claude runs for the
// runner (RUN_SCRIPT=<path>) into td: sign.sh signs through the socket in
// the turn's environment and writes OK or NO to td/turn, git.sh runs
// `dev-cockpit git fetch` and fetch.sh `git fetch` in the shell's repository.
// All touch td/done. The screen is cleared after, no path of the long line may
// stand where typeLine clicks.
const turnScripts = (td) => `mkdir -p ${td}; ` +
  `printf '#!/bin/sh\\nrm -f %s/m.sig; cp ~/.ssh/id_ed25519.pub %s/k.pub; echo hi > %s/m\\nssh-keygen -Y sign -n file -f %s/k.pub %s/m >/dev/null 2>&1 && echo OK > %s/turn || echo NO > %s/turn\\ntouch %s/done\\n' ${Array(8).fill(td).join(" ")} > ${td}/sign.sh; ` +
  `printf '#!/bin/sh\\ncd %s && %s git --state-dir %s fetch >/dev/null 2>&1\\ntouch %s/done\\n' "$PWD" "$(command -v dev-cockpit)" "$S" ${td} > ${td}/git.sh; ` +
  `printf '#!/bin/sh\\ncd %s && git fetch >/dev/null 2>&1\\ntouch %s/done\\n' "$PWD" ${td} > ${td}/fetch.sh; ` +
  `clear; command -v dev-cockpit >/dev/null && echo R$((60+0))READY || echo R$((60+0))NOCLI`;

// askSsh stands in for ssh like the program of fetchRepo, and where its socket
// offers no key, as under off, asks for the passphrase through SSH_ASKPASS the
// way ssh does. $F/out reads ASKED with the answer, DENIED or SIGNED.
const askSsh = "printf '#!/bin/sh\\nrm -f %s/m.sig\\nif [ -S \"$SSH_AUTH_SOCK\" ] && ssh-keygen -Y sign -n file -f %s/k.pub %s/m >/dev/null 2>&1; then echo SIGNED > %s/out; exit 1; fi\\n" +
  "if a=$(\"$SSH_ASKPASS\" \"Enter passphrase for key %s/k:\"); then echo ASKED$a > %s/out; else echo DENIED > %s/out; fi\\nexit 1\\n' $F $F $F $F $F $F $F > $F/askssh; chmod +x $F/askssh; git config ssh.variant simple; git config core.sshCommand $F/askssh";
const askWords = ["ASKED", "DENIED", "SIGNED", "NEVER"];

const gitDialog = (page) => page.locator(".swal2-popup:not(.swal2-toast):has(.dc-ask-prompt)");

// gitAsked reads what the head of the standing git question says, answers it
// and waits until it is gone.
async function gitAsked(page) {
  await gitDialog(page).waitFor({ state: "visible", timeout: 20000 });
  const read = await gitDialog(page).evaluate((popup) => ({
    caller: [...popup.querySelectorAll("[data-gitprompt-caller]")].map((el) => el.textContent.trim()),
    icon: [...popup.querySelectorAll(".dc-ask-meta .dc-ask-by i")].map((el) => el.className),
    meta: [...popup.querySelectorAll(".dc-ask-meta > :not(.dc-ask-sep)")].map((el) => el.textContent.trim()),
    heads: [...popup.querySelectorAll(".dc-ask-code-head")].map((el) => el.textContent.trim()),
  }));
  await gitDialog(page).locator("input.swal2-input").fill("e2e");
  await gitDialog(page).locator(".swal2-confirm").click();
  await gitDialog(page).waitFor({ state: "detached", timeout: 10000 });
  return read;
}

// postAs posts a form the way the page's own scripts do, with its CSRF token.
const postAs = (page, path, body) => page.evaluate(async ({ path, body }) => {
  const token = document.querySelector('meta[name="csrf-token"]')?.content || "";
  const r = await fetch(path, { method: "POST", headers: { "X-CSRF-Token": token, Accept: "application/json" }, body: new URLSearchParams(body) });
  return { status: r.status, text: await r.text() };
}, { path, body });

// inTurn hands a script to a turn of the assistant and waits in the shell
// until it ran, then prints R<n> and what report reads.
async function inTurn(page, shellUrl, assistant, td, script, n, report) {
  await typeLine(page, shellUrl, `rm -f ${td}/done ${td}/turn; until [ -e ${td}/done ]; do sleep 0.2; done; echo R$((${n}+0))$(${report})`);
  await sleep(500);
  const sent = await postAs(page, `/assistants/${assistant}`, { form: "message", message: `RUN_SCRIPT=${td}/${script}` });
  assert(sent.status === 200, `the message to the assistant answered ${sent.status}: ${sent.text.slice(0, 160)}`);
}

// askedOnce reads the commands of the question standing and their heads,
// answers it, with the passphrase where it asks for one, and waits until the
// shell printed what signature n did.
async function askedOnce(page, n) {
  const asked = await dialogUp(page);
  if (asked.input) await dialog(page).locator("input.swal2-input").fill(PASSPHRASE);
  await dialog(page).locator(".swal2-confirm").click();
  const signed = await outcome(page, n, fetchWords, 40000);
  await dialogGone(page);
  return { caller: asked.details.Caller, commands: asked.commands, heads: asked.heads, signed };
}

// pageFetch fetches the way the editor does, quietly on its way into the git
// surfaces with auto, or the Fetch in the sheet without, and answers the status.
const pageFetch = (page, project, auto) => page.evaluate(async ({ path, body }) => {
  const token = document.querySelector('meta[name="csrf-token"]')?.content || "";
  const r = await fetch(path, { method: "POST", headers: { "X-CSRF-Token": token, "Content-Type": "application/json" }, body });
  return r.status;
}, { path: `/projects/${project}/editor/git/fetch`, body: JSON.stringify({ auto }) });

const dialog = (page) => page.locator(".swal2-popup:not(.swal2-toast):has([data-ssh-key-details])");

async function dialogUp(page) {
  await dialog(page).waitFor({ state: "visible", timeout: 20000 });
  const rows = await dialog(page).locator("[data-ssh-key-details]").evaluate((root) => [...root.querySelectorAll("[data-approval-detail]")]
    .map((el) => [el.dataset.approvalDetail, (el.querySelector("[data-approval-value]") || el).textContent.trim()]));
  const details = Object.fromEntries(rows);
  details.Caller = (await dialog(page).locator("[data-ssh-key-caller]").innerText()).trim();
  const commands = (await dialog(page).locator("[data-gitprompt-command]").allInnerTexts()).map((c) => c.trim());
  return {
    title: (await dialog(page).locator(".dc-ask-title").innerText()).trim(),
    details,
    command: commands.join("\n"),
    commands,
    heads: await dialog(page).locator(".dc-ask-code-head").evaluateAll((els) => els.map((el) => el.textContent.trim())),
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

// allowAll answers every question that comes up with Allow, until the shell
// printed signature n's outcome.
async function allowAll(page, n, timeout = 30000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    const text = await mirror(page);
    const word = ["OK", "NO"].find((w) => text.includes(`R${n}${w}`));
    if (word) return word;
    if (await dialog(page).isVisible().catch(() => false)) await dialog(page).locator(".swal2-confirm").click().catch(() => {});
    await sleep(300);
  }
  throw new Error(`the shell never printed the outcome of signature ${n}`);
}

// agentsUp keeps the pids of this instance's ssh-agents in the shell's P and
// prints whether any runs, agentsGone waits up to twenty seconds for those
// pids to end and prints whether they did.
const agentsUp = (n) => `P=$(pgrep -d ' ' -f "ssh-agent -a $(dirname "$SSH_AUTH_SOCK")/agents/"); [ -n "$P" ] && echo R$((${n}+0))UP || echo R$((${n}+0))NONE`;
const agentsGone = (n) => `for i in $(seq 1 40); do A=; for p in $P; do kill -0 $p 2>/dev/null && A=1; done; [ -z "$A" ] && break; sleep 0.5; done; [ -z "$A" ] && echo R$((${n}+0))GONE || echo R$((${n}+0))ALIVE`;

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

L.runFeature("SSH AGENT", async ({ engine, ctx, page, run, bag }) => {
  const project = `zzsa-${engine}-${Date.now().toString(36)}`;
  let shellUrl = null;
  let assistant = "";
  try {
    await L.createProject(page, project);
    const dir = await L.projectPath(page, project);
    const td = `${dir}/.zzsa-turn`;
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
      assert((await policy("ui")) === "allow" && (await policy("shell")) === "ask" && (await policy("coder")) === "ask" && (await policy("assistant")) === "ask",
        "the default policies are not allow, ask, ask, ask");
      const kinds = await page.locator("#settings-ssh-agent-policy label").allInnerTexts();
      assert(kinds.join("|") === "Git actions of the UI|Shells|Coders|Assistants|Keep a key open for", `the policies read ${kinds.join(", ")}`);
      for (const kind of ["ui", "shell", "coder", "assistant"]) {
        const labels = await page.locator(`#settings-ssh-agent-policy [name="policy_${kind}"] option`).allTextContents();
        assert(labels.join("|") === "Allow|Ask in the browser|Off", `the ${kind} policy offers ${labels.join(", ")}`);
      }
      const hint = await page.locator("#settings-ssh-agent-policy").innerText();
      assert(hint.includes("With Off the agent offers no key, ssh asks for the passphrase itself."), `the policy hint reads ${hint.slice(0, 200)}`);
      assert(!/\bdeny\b|as before/i.test(hint), `the policy form still reads deny or as before: ${hint.slice(0, 200)}`);
      assert((await page.inputValue('#settings-ssh-agent-policy [name="ttl_minutes"]')) === "60", "the default time is not 60 minutes");
    });

    await run("off: a shell's signature fails at once and nothing is asked", async () => {
      await settle(page);
      await savePolicies(page, { shell: "off" });
      shellUrl = await L.createShell(page, project);
      await sign(page, shellUrl, 1);
      assert((await outcome(page, 1)) === "NO", "a shell under off signed");
      assert(!(await dialog(page).isVisible()), "a question came up for a shell under off");
    });

    await run("ask with a locked key: the dialog names the shells, the key and the command, a wrong passphrase asks again, the right one signs", async () => {
      await settle(page);
      await savePolicies(page, { shell: "ask" });
      await sign(page, shellUrl, 2);
      const first = await dialogUp(page);
      assert(first.title === "Unlock your SSH key", `the title reads ${first.title}`);
      assert(first.input, "the locked key's question has no passphrase field");
      assert(first.details.Caller === "Shell" && !("Project" in first.details), `the caller rows read ${JSON.stringify(first.details)}`);
      const look = await dialog(page).evaluate((popup) => ({
        ask: popup.classList.contains("dc-ask"),
        title: Boolean(popup.querySelector(".swal2-title")?.offsetParent),
        meta: [...popup.querySelectorAll(".dc-ask-meta .dc-ask-by")].map((el) => el.textContent.trim()),
        mono: [...popup.querySelectorAll("[data-ssh-key-details] .dc-ask-rows [data-approval-value]")].every((el) => el.classList.contains("font-monospace")),
        buttons: [...popup.querySelectorAll(".swal2-actions button")].filter((b) => b.offsetParent).map((b) => b.textContent.trim()),
        note: popup.querySelector("[data-ssh-key-details] .dc-ask-note")?.textContent.trim(),
      }));
      assert(look.ask && !look.title && look.mono, `the question does not wear the app's dialog: ${JSON.stringify(look)}`);
      assert(JSON.stringify(look.meta) === JSON.stringify(["Shell", "waits up to 2 min"]), `the head names ${JSON.stringify(look.meta)}`);
      assert(JSON.stringify(look.buttons) === JSON.stringify(["Deny", "Unlock and sign"]), `the buttons read ${JSON.stringify(look.buttons)}`);
      assert((look.note || "").startsWith("The cockpit hands it to ssh-add and never keeps it."), `the note reads ${look.note}`);
      assert((first.details.Key || "").endsWith("/.ssh/id_ed25519"), `the key reads ${first.details.Key}`);
      assert((first.details.Fingerprint || "").startsWith("SHA256:"), `the fingerprint reads ${first.details.Fingerprint}`);
      assert(first.command.includes("ssh-keygen") && first.command.includes("-Y sign"), `the command reads ${first.command}`);
      assert(JSON.stringify(first.heads) === JSON.stringify([`command · shell in ${project}`]), `the command is headed ${JSON.stringify(first.heads)}`);
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

    await run("a device clock an hour ahead or behind reads the same time left and fetches the keys at most once", async () => {
      await settle(page);
      for (const [name, clock] of await L.skewedClocks(ctx, bag, 60)) {
        try {
          let fetched = 0;
          clock.on("request", (r) => { if (r.url().includes("/settings/ssh-agent/keys")) fetched += 1; });
          await clock.goto(`${BASE}/settings/ssh-agent`, { waitUntil: "domcontentloaded" });
          await keyRow(clock, "open").waitFor({ state: "visible", timeout: 10000 });
          await sleep(5000);
          const left = (await clock.locator("[data-ssh-key-left]").innerText()).trim();
          assert(/^(1 h|5\d min) left$/.test(left), `a clock ${name} reads "${left}" for a key opened a minute ago`);
          assert(fetched <= 1, `a clock ${name} fetched the keys ${fetched} times in 5 s`);
        } finally {
          await clock.close().catch(() => {});
        }
      }
    });

    await run("ask with an open key: only a yes is asked, Deny fails and Allow signs", async () => {
      await settle(page);
      await sign(page, shellUrl, 4);
      const asked = await dialogUp(page);
      assert(asked.title === "Sign with your SSH key" && !asked.input, `an open key asked ${asked.title}, field ${asked.input}`);
      await dialog(page).locator(".swal2-deny").click();
      assert((await outcome(page, 4)) === "NO", "a denied signature went through");
      await dialogGone(page);
      await sign(page, shellUrl, 5);
      await dialogUp(page);
      await dialog(page).locator(".swal2-confirm").click();
      assert((await outcome(page, 5)) === "OK", "an allowed signature failed");
    });

    await run("two signatures of the shells at once: the question names both commands", async () => {
      await settle(page);
      await typeLine(page, shellUrl, "T=$(mktemp -d); cp ~/.ssh/id_ed25519.pub $T/k.pub; echo a > $T/one; echo b > $T/two; " +
        "ssh-keygen -Y sign -n file -f $T/k.pub $T/one >/dev/null 2>&1 & ssh-keygen -Y sign -n file -f $T/k.pub $T/two >/dev/null 2>&1; wait; " +
        "[ -s $T/one.sig ] && [ -s $T/two.sig ] && echo R$((8+0))OK || echo R$((8+0))NO");
      const asked = await dialogUp(page);
      assert(asked.title === "Sign with your SSH key" && asked.details.Caller === "Shell", `the question reads ${asked.title} ${JSON.stringify(asked.details)}`);
      assert(asked.commands.length === 2 && asked.commands.some((c) => c.endsWith("/one")) && asked.commands.some((c) => c.endsWith("/two"))
        && asked.heads.every((h) => h === `command · shell in ${project}`), `the question names ${JSON.stringify(asked.commands)} ${JSON.stringify(asked.heads)}`);
      assert((await allowAll(page, 8)) === "OK", "the two allowed signatures did not both go through");
      await dialogGone(page);
    });

    await run("allow: an open key signs without a question", async () => {
      await settle(page);
      await savePolicies(page, { shell: "allow" });
      await sign(page, shellUrl, 6);
      assert((await outcome(page, 6)) === "OK", "an allowed shell did not sign");
      assert(!(await dialog(page).isVisible()), "an allowed shell was asked");
    });

    await run("each kind signs under its own policy, the coders' socket refuses the shell under the coders' off", async () => {
      await settle(page);
      await savePolicies(page, { shell: "allow", coder: "off" });
      await sign(page, shellUrl, 9, coderSock);
      assert((await outcome(page, 9)) === "NO", "the coders' socket signed under the coders' off");
      assert(!(await dialog(page).isVisible()), "a refused socket asked");
      await sign(page, shellUrl, 10);
      assert((await outcome(page, 10)) === "OK", "the shells' socket did not sign while allowed");
      await savePolicies(page, { shell: "allow" });
    });

    await run("the quiet fetch runs without the agent, a planted ssh program finds no socket, an explicit fetch signs for the page alone", async () => {
      await settle(page);
      await savePolicies(page, { ui: "allow", shell: "allow", coder: "allow" });
      const coderUrl = await L.createSession(page, project, "zzsa-coder");
      const coder = new URL(coderUrl).pathname.split("/").pop();
      try {
        await page.goto(`${BASE}/settings/ssh-agent`, { waitUntil: "domcontentloaded" });
        await keyRow(page, "open").waitFor({ state: "visible", timeout: 10000 });
        await typeLine(page, shellUrl, fetchRepo);
        assert((await outcome(page, 16, ["READY", "NOAPI"])) === "READY", "the local API socket is not beside the agent's, see the README");
        await typeLine(page, shellUrl, `${fetchReset}; ${inCoder(coder, localFetch(project, "auto.json"))}; ${fetched(13)}`);
        assert((await outcome(page, 13, fetchWords)) === "REFUSED", "the quiet fetch of a coder over the local API got an agent");
        await typeLine(page, shellUrl, `${fetchReset}; ${localFetch(project, "auto.json")}; ${fetched(12)}`);
        assert((await outcome(page, 12, fetchWords)) === "REFUSED", "the quiet fetch of a shell over the local API got an agent");
        await typeLine(page, shellUrl, `${fetchReset}; ${inCoder(coder, localFetch(project, "explicit.json"))}; ${fetched(14)}`);
        assert((await outcome(page, 14, fetchWords)) === "REFUSED", "the explicit fetch of a coder over the local API got an agent");
        assert(!(await dialog(page).isVisible()), "a fetch without the agent asked");
        await typeLine(page, shellUrl, fetchReset);
        await sleep(500);
        assert((await pageFetch(page, project, true)) === 200, "the page's quiet fetch did not answer 200");
        await typeLine(page, shellUrl, fetched(15));
        assert((await outcome(page, 15, fetchWords)) === "REFUSED", "the page's quiet fetch got a planted program a signature");
        await typeLine(page, shellUrl, fetchReset);
        await sleep(500);
        await pageFetch(page, project, false);
        await typeLine(page, shellUrl, fetched(18));
        assert((await outcome(page, 18, fetchWords)) === "SIGNED", "the page's explicit fetch did not sign under the UI's allow");
        assert(!(await dialog(page).isVisible()), "a fetch under allow asked");
      } finally {
        await page.evaluate(async (path) => {
          const token = document.querySelector('meta[name="csrf-token"]')?.content || "";
          await fetch(path, { method: "POST", headers: { "X-CSRF-Token": token } });
        }, `/coders/${coder}/delete`).catch(() => {});
        await savePolicies(page, { shell: "allow" });
      }
    });

    await run("dev-cockpit git from a shell signs under the shells' policy", async () => {
      await settle(page);
      await savePolicies(page, { shell: "off" });
      await typeLine(page, shellUrl, `${fetchReset}; ${cliFetch}; ${fetched(19)}`);
      assert((await outcome(page, 19, fetchWords)) === "REFUSED", "the shell's dev-cockpit git signed under the shells' off");
      assert(!(await dialog(page).isVisible()), "the shell's dev-cockpit git under off asked");
      await savePolicies(page, { shell: "allow" });
      await typeLine(page, shellUrl, `${fetchReset}; ${cliFetch}; ${fetched(20)}`);
      assert((await outcome(page, 20, fetchWords)) === "SIGNED", "the shell's dev-cockpit git did not sign under the shells' allow");
    });

    await run("a question names the git command above the signature in its directory, an ssh line without git its transfer", async () => {
      await settle(page);
      await savePolicies(page, { ui: "ask", shell: "ask", coder: "ask" });
      const coderUrl = await L.createSession(page, project, "zzsa-git");
      const coder = new URL(coderUrl).pathname.split("/").pop();
      try {
        // A window opened later in the coder's session finds no SSH_AUTH_SOCK,
        // tmux drops it from the session when a client without one attaches.
        const asks = [
          ["the shell's git pull", "Shell", `git config ssh.variant simple; ${fetchReset}; git pull; ${fetched(21)}`, 21, "git pull", `command · shell in ${project}`],
          ["the shell's dev-cockpit git", "Shell", `${fetchReset}; ${cliFetch}; ${fetched(22)}`, 22, "git fetch", "command"],
          ["the coder's git fetch", "Coder", `${fetchReset}; ${inCoder(coder, `cd ${dir} && SSH_AUTH_SOCK=$D/coder git fetch`)}; ${fetched(23)}`, 23, "git fetch", `command · zzsa-git in ${project}`],
        ];
        for (const [who, caller, line, n, command, head] of asks) {
          await typeLine(page, shellUrl, line);
          const asked = await askedOnce(page, n);
          assert(asked.caller === caller && JSON.stringify(asked.commands) === JSON.stringify([`${dir}\n$ ${command}`]) && JSON.stringify(asked.heads) === JSON.stringify([head]),
            `${who} asked ${JSON.stringify(asked)}`);
          assert(asked.signed === "SIGNED", `${who} did not sign once allowed`);
        }
        await typeLine(page, shellUrl, fetchReset);
        await sleep(500);
        const fetching = pageFetch(page, project, false);
        const asked = await dialogUp(page);
        assert(asked.details.Caller === "Git action" && JSON.stringify(asked.commands) === JSON.stringify([`${dir}\n$ git -c core.quotepath=false fetch`]) && JSON.stringify(asked.heads) === '["command"]',
          `the page's fetch asked ${JSON.stringify(asked)}`);
        if (asked.input) await dialog(page).locator("input.swal2-input").fill(PASSPHRASE);
        await dialog(page).locator(".swal2-confirm").click();
        await fetching;
        await dialogGone(page);
        await typeLine(page, shellUrl, fetched(24));
        assert((await outcome(page, 24, fetchWords)) === "SIGNED", "the page's fetch did not sign once allowed");
        // A real ssh signs only for a server, exec -a gives ssh-keygen the
        // name and the line of one, with no git above it.
        await typeLine(page, shellUrl, "T=$(mktemp -d); cp ~/.ssh/id_ed25519.pub $T/k.pub; echo hi > $T/m; " +
          `(cd $T && exec -a ssh ssh-keygen -Y sign -n file -f k.pub m git@e2e.invalid "git-receive-pack 'x.git'") >/dev/null 2>&1; echo R$((25+0))DONE`);
        const line = await dialogUp(page);
        assert(JSON.stringify(line.heads) === JSON.stringify([`ssh for a git push · shell in ${project}`]) && line.commands.length === 1
          && line.commands[0].endsWith(`\n$ ssh -Y sign -n file -f k.pub m git@e2e.invalid "git-receive-pack 'x.git'"`), `the ssh line asked ${JSON.stringify(line)}`);
        await dialog(page).locator(".swal2-deny").click();
        assert((await outcome(page, 25, ["DONE"])) === "DONE", "the denied ssh line never ended");
        await dialogGone(page);
        await typeLine(page, shellUrl, "T=$(mktemp -d); cp ~/.ssh/id_ed25519.pub $T/k.pub; echo hi > $T/m; " +
          `(sh -c "ssh-keygen -Y sign -n file -f $T/k.pub $T/m >/dev/null 2>&1; touch $T/d" &); until [ -e $T/d ]; do sleep 0.2; done; echo R$((26+0))DONE`);
        const orphan = await dialogUp(page);
        assert(orphan.details.Caller === "Shell" && JSON.stringify(orphan.heads) === '["command"]' && orphan.command.includes("ssh-keygen -Y sign"),
          `a signature outside every terminal asked ${JSON.stringify(orphan)}`);
        await dialog(page).locator(".swal2-deny").click();
        assert((await outcome(page, 26, ["DONE"])) === "DONE", "the denied signature outside every terminal never ended");
        await dialogGone(page);
      } finally {
        await page.evaluate(async (path) => {
          const token = document.querySelector('meta[name="csrf-token"]')?.content || "";
          await fetch(path, { method: "POST", headers: { "X-CSRF-Token": token } });
        }, `/coders/${coder}/delete`).catch(() => {});
        await savePolicies(page, { shell: "allow" });
      }
    });

    await run("an assistant's turn signs under the assistants' policy, directly and through dev-cockpit git", async () => {
      await settle(page);
      const made = await postAs(page, "/assistants/new", { form: "new", coder: "claude" });
      assistant = (JSON.parse(made.text || "{}").id) || "";
      assert(assistant, `no assistant was made: ${made.status} ${made.text.slice(0, 160)}`);
      assert((await postAs(page, `/assistants/${assistant}`, { form: "rename", title: "zzsa helper" })).status === 200, "the assistant was not renamed");
      await typeLine(page, shellUrl, turnScripts(td));
      assert((await outcome(page, 60, ["READY", "NOCLI"])) === "READY", "dev-cockpit is not on the instance's PATH, see the README");

      await savePolicies(page, { shell: "allow", assistant: "off" });
      await inTurn(page, shellUrl, assistant, td, "sign.sh", 61, `cat ${td}/turn`);
      assert((await outcome(page, 61, ["OK", "NO"], 40000)) === "NO", "a turn signed under the assistants' off");
      assert(!(await dialog(page).isVisible()), "a turn under off asked");

      await savePolicies(page, { shell: "off", assistant: "ask" });
      await inTurn(page, shellUrl, assistant, td, "sign.sh", 62, `cat ${td}/turn`);
      const asked = await dialogUp(page);
      assert(asked.title === "Sign with your SSH key" && asked.details.Caller === "Assistant", `the turn's question reads ${asked.title} ${JSON.stringify(asked.details)}`);
      assert(asked.command.includes("ssh-keygen") && asked.command.includes("-Y sign") && JSON.stringify(asked.heads) === '["command · zzsa helper"]',
        `the turn's command reads ${asked.command} under ${JSON.stringify(asked.heads)}`);
      await dialog(page).locator(".swal2-confirm").click();
      assert((await outcome(page, 62, ["OK", "NO"], 40000)) === "OK", "the allowed turn did not sign");
      await dialogGone(page);
      await typeLine(page, shellUrl, fetchReset);
      await inTurn(page, shellUrl, assistant, td, "fetch.sh", 65, "cat $F/out 2>/dev/null || echo NEVER");
      const fetchAsked = await askedOnce(page, 65);
      assert(fetchAsked.caller === "Assistant" && JSON.stringify(fetchAsked.commands) === JSON.stringify([`${dir}\n$ git fetch`]) && fetchAsked.signed === "SIGNED"
        && JSON.stringify(fetchAsked.heads) === '["command · zzsa helper"]',
        `the turn's git fetch asked ${JSON.stringify(fetchAsked)}`);

      await savePolicies(page, { shell: "off", assistant: "allow" });
      await typeLine(page, shellUrl, fetchReset);
      await inTurn(page, shellUrl, assistant, td, "git.sh", 63, "cat $F/out 2>/dev/null || echo NEVER");
      assert((await outcome(page, 63, fetchWords, 40000)) === "SIGNED", "the turn's dev-cockpit git did not sign under the assistants' allow");

      await savePolicies(page, { shell: "allow", assistant: "off" });
      await typeLine(page, shellUrl, fetchReset);
      await inTurn(page, shellUrl, assistant, td, "git.sh", 64, "cat $F/out 2>/dev/null || echo NEVER");
      assert((await outcome(page, 64, fetchWords, 40000)) === "REFUSED", "the turn's dev-cockpit git signed under the assistants' off");
      assert(!(await dialog(page).isVisible()), "the turn's dev-cockpit git under off asked");
      await savePolicies(page, { shell: "allow" });
    });

    await run("under off the question ssh asks itself names the kind of caller, and nothing where it is unknown", async () => {
      await settle(page);
      assert(assistant, "the turn needs the assistant of the assistants' check");
      await savePolicies(page, { ui: "off", shell: "off", coder: "off", assistant: "off" });
      const coderUrl = await L.createSession(page, project, "zzsa-ask");
      const coder = new URL(coderUrl).pathname.split("/").pop();
      try {
        await typeLine(page, shellUrl, `${askSsh}; clear`);
        const cli = "dev-cockpit git --state-dir $S fetch >/dev/null 2>&1";
        const asks = [
          ["the shell's dev-cockpit git", ["Shell"], "terminal-2", `${fetchReset}; ${cli}; ${fetched(70)}`, 70, `command · shell in ${project}`],
          ["the coder's dev-cockpit git", ["Coder"], "robot", `${fetchReset}; ${inCoder(coder, `cd ${dir} && SSH_AUTH_SOCK=$D/coder ${cli}`)}; ${fetched(71)}`, 71, `command · zzsa-ask in ${project}`],
          ["a dev-cockpit git without the agent", [], "", `${fetchReset}; env -u SSH_AUTH_SOCK ${cli}; ${fetched(72)}`, 72, `command · shell in ${project}`],
          ["a dev-cockpit git outside every terminal", ["Shell"], "terminal-2", `${fetchReset}; (sh -c "${cli}; touch $F/done" &); until [ -e $F/done ]; do sleep 0.2; done; ${fetched(76)}`, 76, "command"],
        ];
        for (const [who, meta, icon, line, n, head] of asks) {
          await typeLine(page, shellUrl, line);
          const asked = await gitAsked(page);
          assert(JSON.stringify(asked.meta) === JSON.stringify(meta) && JSON.stringify(asked.caller) === JSON.stringify(meta)
            && asked.icon.every((c) => c.includes(`ti-${icon}`)) && JSON.stringify(asked.heads) === JSON.stringify([head]), `${who} asked ${JSON.stringify(asked)}`);
          assert((await outcome(page, n, askWords, 40000)) === "ASKED", `${who} never got the answer`);
        }
        await typeLine(page, shellUrl, fetchReset);
        await inTurn(page, shellUrl, assistant, td, "git.sh", 73, "cat $F/out 2>/dev/null || echo NEVER");
        const turn = await gitAsked(page);
        assert(JSON.stringify(turn.meta) === '["Assistant"]' && turn.icon.every((c) => c.includes("ti-sparkles")) && JSON.stringify(turn.heads) === '["command · zzsa helper"]',
          `the turn asked ${JSON.stringify(turn)}`);
        assert((await outcome(page, 73, askWords, 40000)) === "ASKED", "the turn never got the answer");
        await typeLine(page, shellUrl, fetchReset);
        await sleep(500);
        const fetching = pageFetch(page, project, false);
        const ui = await gitAsked(page);
        await fetching;
        assert(JSON.stringify(ui.meta) === JSON.stringify(["Git action", `git fetch in ${project}`]) && JSON.stringify(ui.caller) === '["Git action"]'
          && ui.icon.every((c) => c.includes("ti-git-branch")), `the page's fetch asked ${JSON.stringify(ui)}`);
        await typeLine(page, shellUrl, fetched(74));
        assert((await outcome(page, 74, askWords)) === "ASKED", "the page's fetch never got the answer");
        await typeLine(page, shellUrl, `${fetchReset}; ${localFetch(project, "explicit.json")}; ${fetched(75)}`);
        const local = await gitAsked(page);
        assert(JSON.stringify(local.meta) === JSON.stringify([`git fetch in ${project}`]) && local.caller.length === 0, `the local fetch asked ${JSON.stringify(local)}`);
        assert((await outcome(page, 75, askWords)) === "ASKED", "the local fetch never got the answer");
      } finally {
        for (let i = 0; i < 5 && await gitDialog(page).isVisible().catch(() => false); i += 1) {
          await gitDialog(page).locator(".swal2-cancel").click().catch(() => {});
          await sleep(500);
        }
        await page.evaluate(async (path) => {
          const token = document.querySelector('meta[name="csrf-token"]')?.content || "";
          await fetch(path, { method: "POST", headers: { "X-CSRF-Token": token } });
        }, `/coders/${coder}/delete`).catch(() => {});
        await typeLine(page, shellUrl, "git config core.sshCommand $F/ssh; clear").catch(() => {});
        await savePolicies(page, { shell: "allow" });
      }
    });

    await run("a restart locks every key, ends the agents of the process before and keeps the shell's socket", async () => {
      await settle(page);
      assert(assistant, "the restart needs the assistant of the check before");
      await savePolicies(page, { shell: "allow" });
      await keyRow(page, "open").waitFor({ state: "visible", timeout: 10000 });
      // The screen the shell shows again after the restart must hold no file
      // link where typeLine clicks, a click on one opens the editor.
      await typeLine(page, shellUrl, `clear; ${agentsUp(30)}`);
      assert((await outcome(page, 30, ["UP", "NONE"])) === "UP", "no ssh-agent runs while a key is open");
      const sent = await postAs(page, `/assistants/${assistant}`, { form: "message", message: "please RESTART_CHAT now" });
      assert(sent.status === 200, `the restart message answered ${sent.status}`);
      const deadline = Date.now() + 90000;
      for (;;) {
        await sleep(1500);
        const locked = await page.goto(`${BASE}/settings/ssh-agent`, { waitUntil: "domcontentloaded", timeout: 10000 })
          .then(() => keyRow(page, "locked").isVisible()).catch(() => false);
        if (locked) break;
        assert(Date.now() < deadline, "the key never read locked, the cockpit did not come back from its restart");
      }
      await typeLine(page, shellUrl, agentsGone(31));
      assert((await outcome(page, 31, ["GONE", "ALIVE"], 40000)) === "GONE", "an ssh-agent of the process before outlived it");
      await sign(page, shellUrl, 32);
      const asked = await dialogUp(page);
      assert(asked.input && asked.details.Caller === "Shell", `after the restart the shell was asked ${asked.title} ${JSON.stringify(asked.details)}`);
      await dialog(page).locator("input.swal2-input").fill(PASSPHRASE);
      await dialog(page).locator(".swal2-confirm").click();
      assert((await outcome(page, 32)) === "OK", "the shell's socket did not sign after the restart");
      await dialogGone(page);
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
      await savePolicies(page, { shell: "off", ttl: "99999", unchecked: true, expect: "Keep a key open between 1 and 10080 minutes." });
      assert((await page.inputValue('#settings-ssh-agent-policy [name="policy_shell"]')) === "allow", "a refused save moved the policy");
    });
  } finally {
    await forgetAll(page).catch(() => {});
    await savePolicies(page, {}).catch(() => {});
    if (assistant) await postAs(page, `/assistants/${assistant}`, { form: "delete" }).catch(() => {});
    if (shellUrl) await L.deleteShell(page, shellUrl).catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
  }
});
