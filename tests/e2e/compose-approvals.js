const http = require("http");
const L = require("./lib");
const { assert, BASE, sleep, dismissUpdate } = L;

// Compose commands run by an assistant, and the approval a confirm action
// asks for: the assistant reaches POST /projects/:name/docker/compose over the
// local API socket the way its `compose-start` does (the socket is mounted
// into the runner, APISOCK names it, the X-Dev-Cockpit-Assistant header says
// who calls), a command marked to ask first parks the run instead of starting
// it (the answer carries `pending`, the run page reads Awaiting approval with
// its Cancel showing), the question is news of its own (`approval:<run>`,
// "Assistant asks approval.", read through the request API because an open
// page reads the entry by showing the dialog) and stands on any page as the
// app-wide dialog (@dc/gitprompt's second kind: the assistant, the project,
// the stack and the command, Approve and Deny, one "Approve and don't ask
// again" box). Deny ends the run declined and the assistant's thread holds a
// grey compose note saying so (data-assistant-compose="declined"); Approve
// runs it, the note says done, and the next confirm action still asks, and
// nothing about an assistant's run rings the bell (no docker:<project> entry
// for it). The Compose actions approval under /settings/assistant/approvals
// is one switch for every assistant (on by default, off lets every confirm
// action run, the socket refused, on the Docker settings too), the dialog's
// box turns it off for every assistant, and the trigger form hides any or all
// for a compose event.
//
// Fixture the host prepares before the run: the instance's projects dir holds
// a project named DOCKER_PROJECT (default "dockere2e") with a compose file of
// one service "web" on a local image, already up, the instance reaches the
// daemon and the docker CLI, has a coder installed (the fakes suffice) and
// its default compose actions (the confirm action is "Compose down with
// volumes", which the checks approve once, so the fixture goes down and is
// brought back up over the socket at the end). The runner needs the
// instance's local API socket mounted, e.g. -v <state-dir>/api:/apisock with
// APISOCK=/apisock/s, because nothing but the socket may act as an assistant.

const NAME = process.env.DOCKER_PROJECT || "dockere2e";
const APISOCK = process.env.APISOCK || "/apisock/s";
const DIALOG = ".swal2-popup:not(.swal2-toast)";

// asAssistant posts one form over the local socket as the assistant, which
// is exactly what the generated `cockpit` wrapper does for a turn.
function asAssistant(id, path, body) {
  return new Promise((resolve, reject) => {
    const data = new URLSearchParams(body).toString();
    const req = http.request({
      socketPath: APISOCK,
      path,
      method: "POST",
      headers: {
        "X-Dev-Cockpit-Assistant": id,
        "Content-Type": "application/x-www-form-urlencoded",
        "Content-Length": Buffer.byteLength(data),
        Accept: "application/json",
      },
    }, (res) => {
      let raw = "";
      res.on("data", (chunk) => { raw += chunk; });
      res.on("end", () => {
        let json = {};
        try { json = JSON.parse(raw); } catch {}
        resolve({ status: res.statusCode, json, raw });
      });
    });
    req.on("error", reject);
    req.end(data);
  });
}

async function csrfOf(page) {
  return page.evaluate(() => document.querySelector('meta[name="csrf-token"]')?.content || "");
}

async function runJSON(page, id) {
  const res = await page.request.get(`${BASE}/projects/${NAME}/docker/runs/${id}/output`, { headers: { Accept: "application/json" } });
  return res.json();
}

async function waitRun(page, id, done, what) {
  for (let i = 0; i < 120; i += 1) {
    const run = await runJSON(page, id);
    if (done(run)) return run;
    await sleep(500);
  }
  throw new Error(`run ${id} never ${what}`);
}

async function notifications(page) {
  const res = await page.request.get(`${BASE}/notifications`, { headers: { Accept: "application/json" } });
  return (await res.json()).notifications || [];
}

async function waitFor(fn, what, tries = 40) {
  for (let i = 0; i < tries; i += 1) {
    if (await fn()) return;
    await sleep(250);
  }
  throw new Error(`timed out waiting for ${what}`);
}

L.runFeature("COMPOSE APPROVALS", async ({ page, run }) => {
  let assistantID = "";
  let parked = "";

  await run("an assistant is made for the checks", async () => {
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    await page.waitForSelector(`#project-${NAME} [data-chip-kind="docker"]`, { timeout: 8000 });
    const created = await page.request.post(`${BASE}/assistants/new`, { form: { csrf_token: await csrfOf(page), form: "new", coder: "claude" }, headers: { Accept: "application/json" } });
    assistantID = (await created.json().catch(() => ({}))).id || "";
    assert(assistantID, `no assistant for the checks: ${created.status()}`);
    return assistantID;
  });

  await run("a confirm command started by the assistant parks the run, rings as an approval and asks on any page", async () => {
    // Off every page first: the entry has to exist before a page shows the
    // dialog and reads it.
    await page.goto("about:blank");
    const before = (await notifications(page)).map((n) => n.id);
    const answer = await asAssistant(assistantID, `/projects/${NAME}/docker/compose`, { stack: "", action: "down-volumes" });
    assert(answer.status === 200, `the socket answered ${answer.status}: ${answer.raw}`);
    assert(answer.json.pending === true && answer.json.run, `the confirm action did not park: ${answer.raw}`);
    parked = answer.json.run;
    let fresh = [];
    await waitFor(async () => {
      fresh = (await notifications(page)).filter((n) => !before.includes(n.id));
      return fresh.length > 0;
    }, "the approval entry");
    const entry = fresh.find((n) => n.targetId === `approval:${parked}`);
    assert(entry, `no entry for the approval, only ${JSON.stringify(fresh.map((n) => n.targetId))}`);
    assert(entry.title === "Assistant asks approval.", `the entry's title reads "${entry.title}"`);
    assert(/Compose down with volumes in dockere2e/.test(entry.detail || entry.body || ""), `the entry's line reads "${entry.detail || entry.body}"`);
    assert(!entry.read, "the entry was read before anybody saw it");
    assert((entry.url || "").endsWith(`/docker/runs/${parked}`), `the entry leads to ${entry.url}`);

    await page.goto(`${BASE}/projects/${NAME}/docker/runs/${parked}`, { waitUntil: "domcontentloaded" });
    await page.waitForSelector(`${DIALOG} .swal2-title`, { timeout: 15000 });
    const title = await page.textContent(`${DIALOG} .swal2-title`);
    assert(/asks for approval/.test(title), `the dialog's title reads "${title}"`);
    const details = await page.textContent(`${DIALOG} [data-approval-details]`);
    for (const word of ["Assistant", NAME, "Compose down with volumes"]) {
      assert(details.includes(word), `the dialog does not say ${word}: ${details}`);
    }
    const command = await page.textContent(`${DIALOG} [data-gitprompt-command]`);
    assert(command.includes("docker compose down -v"), `the dialog does not show the command line: ${command}`);
    assert(await page.locator(`${DIALOG} .swal2-confirm`).textContent() === "Approve", "the confirm button is not Approve");
    assert(await page.locator(`${DIALOG} .swal2-deny`).textContent() === "Deny", "the deny button is not Deny");
    const remember = (await page.locator(`${DIALOG} .swal2-checkbox`).textContent()).trim();
    assert(remember === "Approve and don't ask again", `the box reads "${remember}"`);
    assert(await page.locator(`${DIALOG} .swal2-radio`).isHidden(), "the dialog still offers scopes");
    assert(!(await page.locator(`${DIALOG} .swal2-checkbox input`).isChecked()), "the dialog does not start on asking again");
    assert(await page.locator(`${DIALOG} dl[data-approval-details] dt`).count() === 4, "the details are no list of four");
    // The page under it is the run's, already saying what it waits for.
    const status = await page.textContent("[data-run-status]");
    assert(status.trim() === "Awaiting approval", `the run page reads "${status}"`);
    assert(await page.locator("[data-run-stop]").isVisible(), "a parked run hides its Cancel");
    await waitFor(async () => (await notifications(page)).filter((n) => n.targetId === `approval:${parked}` && !n.read).length === 0, "the shown entry to read itself");
    return `run ${parked} parked`;
  });

  await run("Deny ends the run declined and tells the assistant in its thread", async () => {
    await page.click(`${DIALOG} .swal2-deny`);
    await page.waitForSelector(DIALOG, { state: "detached", timeout: 8000 });
    const run = await waitRun(page, parked, (r) => !r.pending, "left its question");
    assert(run.declined === true && run.failure === "declined by the user", `the denied run reads ${JSON.stringify(run)}`);
    await waitFor(async () => (await page.textContent("[data-run-status]")).trim() === "Declined by the user", "the page to say declined");
    await page.goto(`${BASE}/assistants/${assistantID}`, { waitUntil: "domcontentloaded" });
    await page.waitForSelector('[data-assistant-note="compose"][data-assistant-compose="declined"]', { timeout: 15000 });
    const headline = await page.textContent('[data-assistant-compose="declined"] [data-assistant-note-headline]');
    assert(headline.trim() === `Compose declined: Compose down with volumes on ${NAME}`, `the note reads "${headline}"`);
    const bell = (await notifications(page)).filter((n) => n.targetId === `docker:${NAME}` && !n.read);
    assert(bell.length === 0, "an assistant's run rang the project's docker target");
    return headline.trim();
  });

  await run("Approve runs it, the note says done, and the next confirm command still asks", async () => {
    const answer = await asAssistant(assistantID, `/projects/${NAME}/docker/compose`, { stack: "", action: "down-volumes" });
    assert(answer.json.pending === true, `the second confirm action did not park: ${answer.raw}`);
    const id = answer.json.run;
    await page.waitForSelector(`${DIALOG} .swal2-confirm`, { timeout: 15000 });
    await page.click(`${DIALOG} .swal2-confirm`);
    await page.waitForSelector(DIALOG, { state: "detached", timeout: 8000 });
    const done = await waitRun(page, id, (r) => !r.pending && !r.running, "finished after the approval");
    assert(done.exited === true && done.exit === 0 && done.status === "Exit status 0", `the approved run reads ${JSON.stringify(done)}`);
    await page.waitForSelector('[data-assistant-note="compose"][data-assistant-compose="done"]', { timeout: 15000 });
    const headline = await page.textContent('[data-assistant-compose="done"] [data-assistant-note-headline]');
    assert(headline.trim() === `Compose done: Compose down with volumes on ${NAME}`, `the done note reads "${headline}"`);
    assert(await page.locator('[data-assistant-compose="done"]').locator("xpath=..").locator("a[href$='/docker/runs/" + id + "']").count() === 1, "the note carries no link to the run");

    const again = await asAssistant(assistantID, `/projects/${NAME}/docker/compose`, { stack: "", action: "down-volumes" });
    assert(again.json.pending === true, `an approval without the box stopped the asking: ${again.raw}`);
    await page.waitForSelector(`${DIALOG} .swal2-deny`, { timeout: 15000 });
    await page.click(`${DIALOG} .swal2-deny`);
    await page.waitForSelector(DIALOG, { state: "detached", timeout: 8000 });
    await waitRun(page, again.json.run, (r) => !r.pending, "was declined");
    const up = await asAssistant(assistantID, `/projects/${NAME}/docker/compose`, { stack: "", action: "up" });
    assert(up.status === 200 && up.json.run, `bringing the fixture back answered ${up.raw}`);
    await waitRun(page, up.json.run, (r) => !r.running, "brought the fixture back");
    assert((await notifications(page)).filter((n) => n.targetId === `docker:${NAME}` && !n.read).length === 0, "an assistant's run rang the project's docker target");
    return "approved once, asked again, fixture back up";
  });

  const composeSwitch = '[data-assistant-approval="compose-actions"] input[type="checkbox"]';
  const saveApprovals = () => Promise.all([
    page.waitForNavigation({ waitUntil: "domcontentloaded" }),
    page.click('#settings-assistant-approvals button[type="submit"]'),
  ]);

  await run("the Compose actions approval is one switch for every assistant, and the socket cannot move it or the compose commands", async () => {
    await page.goto(`${BASE}/settings/assistant/approvals`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    assert(await page.locator("[data-assistant-approval]").count() === 1, "the tab lists more than the one approval");
    const label = (await page.textContent('[data-assistant-approval="compose-actions"]')).trim();
    assert(label.startsWith("Compose actions approval"), `the row reads "${label}"`);
    assert(await page.locator(`[data-assistant-approval="${assistantID}"]`).count() === 0, "the tab still carries a row per assistant");
    assert(await page.locator(composeSwitch).isChecked(), "the approval is not on by default");
    await page.locator(composeSwitch).uncheck();
    await saveApprovals();
    assert(!(await page.locator(composeSwitch).isChecked()), "the switch did not stay off");
    const free = await asAssistant(assistantID, `/projects/${NAME}/docker/compose`, { stack: "", action: "down-volumes" });
    assert(free.status === 200 && free.json.pending !== true, `a confirm action was parked with the approval off: ${free.raw}`);
    await waitRun(page, free.json.run, (r) => !r.running, "finished");
    const refused = await asAssistant(assistantID, "/settings/assistant/approvals", { "approval-compose-actions": "1" });
    assert(refused.status === 403, `the socket moved the approval: ${refused.status}`);
    for (const path of ["/settings/docker", "/docker/actions/restore"]) {
      const docker = await asAssistant(assistantID, path, { docker_host: "" });
      assert(docker.status === 403, `the socket reached the compose commands on ${path}: ${docker.status} ${docker.raw}`);
    }
    await page.reload({ waitUntil: "domcontentloaded" });
    assert(!(await page.locator(composeSwitch).isChecked()), "the refused socket call moved the switch");
    await page.locator(composeSwitch).check();
    await saveApprovals();
    assert(await page.locator(composeSwitch).isChecked(), "the switch did not come back on");
    const up = await asAssistant(assistantID, `/projects/${NAME}/docker/compose`, { stack: "", action: "up" });
    await waitRun(page, up.json.run, (r) => !r.running, "brought the fixture back");
    return "off, socket refused on the approval and the docker settings, back on";
  });

  await run("Approve and don't ask again turns the approval off for every assistant", async () => {
    const created = await page.request.post(`${BASE}/assistants/new`, { form: { csrf_token: await csrfOf(page), form: "new", coder: "claude" }, headers: { Accept: "application/json" } });
    const otherID = (await created.json().catch(() => ({}))).id || "";
    assert(otherID, `no second assistant: ${created.status()}`);
    const parkedRun = await asAssistant(assistantID, `/projects/${NAME}/docker/compose`, { stack: "", action: "down-volumes" });
    assert(parkedRun.json.pending === true, `the confirm action did not park: ${parkedRun.raw}`);
    await page.waitForSelector(`${DIALOG} .swal2-checkbox input`, { timeout: 15000 });
    await page.check(`${DIALOG} .swal2-checkbox input`);
    await page.click(`${DIALOG} .swal2-confirm`);
    await page.waitForSelector(DIALOG, { state: "detached", timeout: 8000 });
    await waitRun(page, parkedRun.json.run, (r) => !r.pending && !r.running, "finished after the approval");
    const other = await asAssistant(otherID, `/projects/${NAME}/docker/compose`, { stack: "", action: "down-volumes" });
    assert(other.status === 200 && other.json.pending !== true, `another assistant was still asked: ${other.raw}`);
    await waitRun(page, other.json.run, (r) => !r.running, "finished");
    await page.goto(`${BASE}/settings/assistant/approvals`, { waitUntil: "domcontentloaded" });
    assert(!(await page.locator(composeSwitch).isChecked()), "the box left the approval on");
    await page.locator(composeSwitch).check();
    await saveApprovals();
    const up = await asAssistant(assistantID, `/projects/${NAME}/docker/compose`, { stack: "", action: "up" });
    await waitRun(page, up.json.run, (r) => !r.running, "brought the fixture back");
    const gone = await page.request.post(`${BASE}/assistants/${otherID}`, { form: { csrf_token: await csrfOf(page), form: "delete" }, headers: { Accept: "application/json" } });
    assert(gone.status() === 200, `the second assistant's delete answered ${gone.status()}`);
    return "off from the dialog for both, back on";
  });

  await run("the trigger form offers no any or all for a compose event", async () => {
    await page.goto(`${BASE}/assistants/triggers?form=new&assistant=${assistantID}`, { waitUntil: "domcontentloaded" });
    await page.waitForSelector("[data-trigger-event]", { timeout: 8000 });
    await page.selectOption("[data-trigger-event]", "compose-ended");
    const mode = page.locator("[data-trigger-mode]");
    assert(!(await mode.isVisible()), "a compose event shows the mode");
    assert(await page.locator("[data-trigger-mode] select").isDisabled(), "the hidden mode still posts");
    await page.selectOption("[data-trigger-event]", "job-closed");
    assert(await mode.isVisible(), "a job event hides the mode");
    return "mode only for job and coder";
  });

  await run("the assistant is taken away again", async () => {
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    const gone = await page.request.post(`${BASE}/assistants/${assistantID}`, { form: { csrf_token: await csrfOf(page), form: "delete" }, headers: { Accept: "application/json" } });
    assert(gone.status() === 200, `the delete answered ${gone.status()}`);
  });
});
