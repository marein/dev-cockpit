const http = require("http");
const zlib = require("zlib");
const L = require("./lib");
const { assert, BASE, sleep, dismissUpdate } = L;

// Compose commands run by an assistant, and the approval a confirm action
// asks for: the assistant reaches POST /projects/:name/docker/compose over the
// local API socket the way its `compose-start` does (the socket is mounted
// into the runner, APISOCK names it, the X-Dev-Cockpit-Assistant header says
// who calls), a command marked to ask first starts nothing yet (the answer
// carries `pending` and no run), the question is news of its own
// (`approval:<id>`, "Assistant asks approval.", leading home, read through the
// request API because an open page reads the entry by showing the dialog) and
// stands on any page as the app-wide dialog (@dc/gitprompt's second kind: no
// title above it, the action as its head with who asks, the project and the
// stack or project root as chips, the working directory and command line in
// a code panel, Approve and Deny, one "Don't ask again for this kind" box; a
// delete warns what it removes, shows its rows, a red Approve and no
// command; a delete of several lists a row per target, its icon drawn on the
// middle of the name's capitals at 2x and 3x, read from the screenshot's ink
// at four positions on the pixel grid, because the icon font and the text
// font round their metrics apart and an engine snaps each glyph its own way).
// Deny runs nothing and the assistant's thread holds a
// grey approval note saying so (data-assistant-approval="declined"); Approve
// starts the run, the approval note names it, the run's own note says done,
// and the next confirm action still asks, and nothing about an assistant's run
// rings the bell (no docker:<project> entry for it). The Compose actions approval under /settings/assistant/approvals
// is one switch for every assistant (on by default, off lets every confirm
// action run, the socket refused, on the Docker settings too), the dialog's
// box turns it off for every assistant, approvals arriving while another
// dialog stands wait for that dialog to close instead of replacing it and then
// show one after the other, one whose dialog another dialog replaced is asked
// again once that closes, and the trigger form hides any or all for a compose
// event.
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
const LINK_HOST = process.env.DOCKER_LINK_HOST || "dockere2e.test";
const DIALOG = ".swal2-popup:not(.swal2-toast)";
const ICON_TOLERANCE = 0.75;

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

// startedRun reads the run id out of the note an approved command ends in,
// "Run <id> started: ...".
function startedRun(text) {
  return ((text || "").match(/Run (\w+) started/) || [])[1] || "";
}

// visit navigates and tries again when a compose run moved the docker
// networks under the browser: a down removes the fixture's network, an up
// makes it again, and Chromium aborts a navigation it caught in between.
async function visit(page, url) {
  for (let i = 0; ; i += 1) {
    try {
      return await page.goto(url, { waitUntil: "domcontentloaded" });
    } catch (error) {
      if (i >= 5 || !/ERR_NETWORK_CHANGED|interrupted by another navigation/.test(String(error))) throw error;
      await sleep(500);
    }
  }
}

// dialogRows reads what the approval dialog shows, each detail as its label
// and its value: the chips, warnings, rows and sections alike.
async function dialogRows(page) {
  return page.locator(`${DIALOG} [data-approval-details]`).evaluate((root) => [...root.querySelectorAll("[data-approval-detail]")]
    .map((el) => [el.dataset.approvalDetail, (el.querySelector("[data-approval-value]") || el).textContent.trim()]));
}

// askerOf reads who asks from the dialog's head: the name and the short id.
async function askerOf(page) {
  return (await page.textContent(`${DIALOG} [data-approval-asker]`)).replace(/\s+/g, " ").trim();
}

// approvalNote waits until the assistant's page shows count approval notes
// with the verdict and answers the last one's text.
async function approvalNote(page, id, verdict, count) {
  await visit(page, `${BASE}/assistants/${id}`);
  const notes = page.locator(`[data-assistant-note="approval"][data-assistant-approval-note="${verdict}"]`);
  await waitFor(async () => (await notes.count()) >= count, `${count} ${verdict} approval notes`, 60);
  const last = notes.nth(count - 1);
  return { headline: (await last.locator("[data-assistant-note-headline]").textContent()).trim(), text: await last.locator("xpath=..").textContent() };
}

function decodePNG(buf) {
  let pos = 8;
  let width = 0;
  let height = 0;
  let bpp = 0;
  const parts = [];
  while (pos < buf.length) {
    const len = buf.readUInt32BE(pos);
    const kind = buf.toString("ascii", pos + 4, pos + 8);
    const body = buf.subarray(pos + 8, pos + 8 + len);
    if (kind === "IHDR") [width, height, bpp] = [body.readUInt32BE(0), body.readUInt32BE(4), body[9] === 6 ? 4 : 3];
    if (kind === "IDAT") parts.push(body);
    pos += 12 + len;
  }
  const raw = zlib.inflateSync(Buffer.concat(parts));
  const stride = width * bpp;
  const px = Buffer.alloc(height * stride);
  for (let y = 0; y < height; y += 1) {
    const filter = raw[y * (stride + 1)];
    for (let x = 0; x < stride; x += 1) {
      const a = x >= bpp ? px[y * stride + x - bpp] : 0;
      const b = y > 0 ? px[(y - 1) * stride + x] : 0;
      const c = x >= bpp && y > 0 ? px[(y - 1) * stride + x - bpp] : 0;
      const p = a + b - c;
      const paeth = Math.abs(p - a) <= Math.abs(p - b) && Math.abs(p - a) <= Math.abs(p - c) ? a : Math.abs(p - b) <= Math.abs(p - c) ? b : c;
      px[y * stride + x] = (raw[y * (stride + 1) + 1 + x] + [0, a, b, (a + b) >> 1, paeth][filter]) & 255;
    }
  }
  return { width, height, at: (x, y) => [0, 1, 2].map((i) => px[(y * width + x) * bpp + i]) };
}

async function inkMiddle(page, box) {
  const clip = { x: box.x - 2, y: box.y - 2, width: box.width + 4, height: box.height + 4 };
  const { width, height, at } = decodePNG(await page.screenshot({ clip, scale: "device" }));
  const counts = new Map();
  for (let x = 0; x < width; x += 1) for (const y of [0, height - 1]) counts.set(at(x, y).join(), (counts.get(at(x, y).join()) || 0) + 1);
  const bg = [...counts].sort((a, b) => b[1] - a[1])[0][0].split(",").map(Number);
  const inked = [];
  for (let y = 0; y < height; y += 1) {
    let ink = 0;
    for (let x = 0; x < width; x += 1) {
      const diff = Math.max(...at(x, y).map((v, i) => Math.abs(v - bg[i]))) / 255;
      if (diff > 0.06) ink += diff;
    }
    if (ink > 0.3) inked.push(y);
  }
  assert(inked.length, `no ink in ${JSON.stringify(box)}`);
  return clip.y + (inked[0] + inked.at(-1) + 1) / 2 / (height / clip.height);
}

async function waitFor(fn, what, tries = 40) {
  for (let i = 0; i < tries; i += 1) {
    if (await fn()) return;
    await sleep(250);
  }
  throw new Error(`timed out waiting for ${what}`);
}

L.runFeature("COMPOSE APPROVALS", async ({ engine, browser, page, run }) => {
  let assistantID = "";

  await run("an assistant is made for the checks", async () => {
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    await page.waitForSelector(`#project-${NAME} [data-chip-kind="docker"]`, { timeout: 8000 });
    const created = await page.request.post(`${BASE}/assistants/new`, { form: { csrf_token: await csrfOf(page), form: "new", coder: "claude" }, headers: { Accept: "application/json" } });
    assistantID = (await created.json().catch(() => ({}))).id || "";
    assert(assistantID, `no assistant for the checks: ${created.status()}`);
    return assistantID;
  });

  await run("a confirm command started by the assistant starts nothing, rings as an approval and asks on any page", async () => {
    // Off every page first: the entry has to exist before a page shows the
    // dialog and reads it.
    await page.goto("about:blank");
    const before = (await notifications(page)).map((n) => n.id);
    const answer = await asAssistant(assistantID, `/projects/${NAME}/docker/compose`, { stack: "", action: "down-volumes" });
    assert(answer.status === 200, `the socket answered ${answer.status}: ${answer.raw}`);
    assert(answer.json.pending === true && !answer.json.run && answer.json.what === `Compose down with volumes in ${NAME}`, `the confirm action did not wait: ${answer.raw}`);
    let fresh = [];
    await waitFor(async () => {
      fresh = (await notifications(page)).filter((n) => !before.includes(n.id));
      return fresh.length > 0;
    }, "the approval entry");
    const entry = fresh.find((n) => (n.targetId || "").startsWith("approval:"));
    assert(entry, `no entry for the approval, only ${JSON.stringify(fresh.map((n) => n.targetId))}`);
    assert(entry.title === "Assistant asks approval.", `the entry's title reads "${entry.title}"`);
    assert((entry.detail || entry.body || "").includes(`Compose down with volumes in ${NAME}`), `the entry's line reads "${entry.detail || entry.body}"`);
    assert(!entry.read, "the entry was read before anybody saw it");
    assert((entry.url || "").endsWith("/projects"), `the entry leads to ${entry.url}`);

    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await page.waitForSelector(`${DIALOG} [data-approval-what]`, { state: "visible", timeout: 15000 });
    assert(await page.locator(`${DIALOG} .swal2-title`).isHidden(), "the dialog carries a title above its head");
    const what = (await page.textContent(`${DIALOG} [data-approval-what]`)).trim();
    assert(what === "Compose down with volumes", `the dialog's head reads "${what}"`);
    const asker = await askerOf(page);
    assert(asker === `Assistant ${assistantID.slice(0, 8)}`, `the dialog names who asks as "${asker}"`);
    const rows = await dialogRows(page);
    const want = [["Project", NAME], ["Stack", "project root"]];
    assert(JSON.stringify(rows) === JSON.stringify(want), `the dialog's rows read ${JSON.stringify(rows)}`);
    const command = (await page.textContent(`${DIALOG} [data-gitprompt-command]`)).trim().split("\n");
    assert(command.length === 2 && command[0].startsWith("/") && command[0].endsWith(`/${NAME}`) && command[1] === "$ docker compose down -v", `the command block reads ${JSON.stringify(command)}`);
    assert(await page.locator(`${DIALOG} .swal2-confirm`).textContent() === "Approve", "the confirm button is not Approve");
    assert(await page.locator(`${DIALOG} .swal2-deny`).textContent() === "Deny", "the deny button is not Deny");
    const remember = (await page.locator(`${DIALOG} .swal2-checkbox`).textContent()).trim();
    assert(remember === "Don't ask again for this kind", `the box reads "${remember}"`);
    assert(!(await page.locator(`${DIALOG} .swal2-checkbox input`).isChecked()), "the dialog does not start on asking again");
    await waitFor(async () => (await notifications(page)).filter((n) => n.targetId === entry.targetId && !n.read).length === 0, "the shown entry to read itself");
    return entry.targetId;
  });

  await run("Deny runs nothing and tells the assistant in its thread", async () => {
    await page.click(`${DIALOG} .swal2-deny`);
    await page.waitForSelector(DIALOG, { state: "detached", timeout: 8000 });
    const note = await approvalNote(page, assistantID, "declined", 1);
    assert(note.headline === `Declined: Compose down with volumes in ${NAME}`, `the note reads "${note.headline}"`);
    assert(note.text.includes("Not done, declined by the user."), `the note says "${note.text}"`);
    const bell = (await notifications(page)).filter((n) => n.targetId === `docker:${NAME}` && !n.read);
    assert(bell.length === 0, "an assistant's action rang the project's docker target");
    return note.headline;
  });

  await run("Approve starts the run, the notes name it and say done, and the next confirm command still asks", async () => {
    const answer = await asAssistant(assistantID, `/projects/${NAME}/docker/compose`, { stack: "", action: "down-volumes" });
    assert(answer.json.pending === true && !answer.json.run, `the second confirm action did not wait: ${answer.raw}`);
    await page.waitForSelector(`${DIALOG} .swal2-confirm`, { timeout: 15000 });
    await page.click(`${DIALOG} .swal2-confirm`);
    await page.waitForSelector(DIALOG, { state: "detached", timeout: 8000 });
    const approved = await approvalNote(page, assistantID, "done", 1);
    assert(approved.headline === `Approved: Compose down with volumes in ${NAME}`, `the approval note reads "${approved.headline}"`);
    const id = startedRun(approved.text);
    assert(id, `the approval note names no run: ${approved.text}`);
    const done = await waitRun(page, id, (r) => !r.running, "finished after the approval");
    assert(done.exited === true && done.exit === 0 && done.status === "Exit status 0", `the approved run reads ${JSON.stringify(done)}`);
    await page.reload({ waitUntil: "domcontentloaded" });
    await page.waitForSelector('[data-assistant-note="compose"][data-assistant-compose="done"]', { timeout: 15000 });
    const headline = await page.textContent('[data-assistant-compose="done"] [data-assistant-note-headline]');
    assert(headline.trim() === `Compose done: Compose down with volumes on ${NAME}`, `the done note reads "${headline}"`);
    const runLink = page.locator('[data-assistant-compose="done"]').locator("xpath=..").locator("a[href*='/docker/runs/" + id + "']");
    assert(await runLink.count() === 1, "the note carries no link to the run");
    const purged = await page.locator('[data-assistant-compose="done"]').locator("xpath=..").textContent();
    assert(!purged.includes("Answers on"), `a purge names addresses: ${purged}`);

    // Opened from the thread, the run page's arrow leads back to it.
    const thread = new URL(page.url()).pathname;
    const fold = page.locator('[data-assistant-compose="done"]').locator("xpath=..").locator("[data-assistant-note-fold]");
    if (await fold.count() && !(await runLink.isVisible())) await fold.click();
    await runLink.waitFor({ state: "visible", timeout: 4000 });
    await Promise.all([
      page.waitForURL(/\/docker\/runs\//, { timeout: 15000 }),
      runLink.click(),
    ]);
    await page.waitForSelector("dc-docker-run", { timeout: 8000 });
    const back = await page.locator("[data-run-back]").getAttribute("href");
    assert(back === `/assistants/${assistantID}`, `the run page's arrow leads to "${back}"`);
    await Promise.all([
      page.waitForURL((u) => u.pathname === thread, { timeout: 15000 }),
      page.click("[data-run-back]"),
    ]);
    await page.waitForSelector('[data-assistant-note="compose"][data-assistant-compose="done"]', { timeout: 15000 });

    const again = await asAssistant(assistantID, `/projects/${NAME}/docker/compose`, { stack: "", action: "down-volumes" });
    assert(again.json.pending === true, `an approval without the box stopped the asking: ${again.raw}`);
    await page.waitForSelector(`${DIALOG} .swal2-deny`, { timeout: 15000 });
    await page.click(`${DIALOG} .swal2-deny`);
    await page.waitForSelector(DIALOG, { state: "detached", timeout: 8000 });
    await approvalNote(page, assistantID, "declined", 2);
    const up = await asAssistant(assistantID, `/projects/${NAME}/docker/compose`, { stack: "", action: "up" });
    assert(up.status === 200 && up.json.run, `bringing the fixture back answered ${up.raw}`);
    await waitRun(page, up.json.run, (r) => !r.running, "brought the fixture back");
    assert((await notifications(page)).filter((n) => n.targetId === `docker:${NAME}` && !n.read).length === 0, "an assistant's run rang the project's docker target");
    // The up is a start: its note waits for the cache reading after the run
    // and names the fixture's route before its port, both as links. The route
    // is linked by the server, protocol relative; the port by the page, on the
    // host the page was reached on, live and after a reload alike.
    await waitFor(async () => (await page.locator('[data-assistant-compose="done"]').count()) === 2, "the up's note", 60);
    const upNote = () => page.locator('[data-assistant-compose="done"]').nth(1).locator("xpath=..");
    const answers = ((await upNote().textContent()).match(/Answers on: ([^\n]*)/) || [])[1] || "";
    assert(answers.trim() === `${LINK_HOST}, :18088`, `the up note names "${answers}"`);
    const readLinks = async () => ({
      route: await upNote().locator(`a:text-is("${LINK_HOST}")`).getAttribute("href"),
      port: await upNote().locator('a:text-is(":18088")').getAttribute("href"),
      host: await page.evaluate(() => window.location.hostname),
    });
    for (const when of ["live", "after a reload"]) {
      if (when !== "live") {
        await page.reload({ waitUntil: "domcontentloaded" });
        await page.waitForSelector('[data-assistant-compose="done"] >> nth=1', { timeout: 15000 });
      }
      const links = await readLinks();
      assert(links.route === `//${LINK_HOST}`, `${when}, the route links ${links.route}`);
      assert(links.port === `http://${links.host}:18088`, `${when}, the port links ${links.port} on a page of ${links.host}`);
    }
    return `approved run ${id}, asked again, fixture back up; up note: Answers on: ${answers}`;
  });

  const composeSwitch = '[data-assistant-approval="compose-actions"] input[type="checkbox"]';
  const saveApprovals = () => Promise.all([
    page.waitForNavigation({ waitUntil: "domcontentloaded" }),
    page.click('#settings-assistant-approvals button[type="submit"]'),
  ]);

  await run("the Compose actions approval is one switch for every assistant, and the socket cannot move it or the compose commands", async () => {
    await visit(page, `${BASE}/settings/assistant/approvals`);
    await dismissUpdate(page);
    const kinds = await page.locator("[data-assistant-approval]").evaluateAll((rows) => rows.map((row) => row.dataset.assistantApproval));
    assert(JSON.stringify(kinds) === JSON.stringify(["compose-actions", "project-delete", "coder-delete", "assistant-delete", "worktree-post-script", "forge-retry", "forge-change", "forge-publish", "forge-thread"]), `the tab lists ${JSON.stringify(kinds)}`);
    const label = (await page.textContent('[data-assistant-approval="compose-actions"]')).trim();
    assert(label.startsWith("Compose actions approval"), `the row reads "${label}"`);
    assert(await page.locator(`[data-assistant-approval="${assistantID}"]`).count() === 0, "the tab still carries a row per assistant");
    assert(await page.locator(composeSwitch).isChecked(), "the approval is not on by default");
    await page.locator(composeSwitch).uncheck();
    await saveApprovals();
    assert(!(await page.locator(composeSwitch).isChecked()), "the switch did not stay off");
    const free = await asAssistant(assistantID, `/projects/${NAME}/docker/compose`, { stack: "", action: "down-volumes" });
    assert(free.status === 200 && free.json.pending !== true && free.json.run && free.json.action === "Compose down with volumes" && (free.json.url || "").endsWith(`/docker/runs/${free.json.run}`), `a confirm action with the approval off did not answer like any start: ${free.raw}`);
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

  await run("Approve with Don't ask again for this kind turns the approval off for every assistant", async () => {
    const created = await page.request.post(`${BASE}/assistants/new`, { form: { csrf_token: await csrfOf(page), form: "new", coder: "claude" }, headers: { Accept: "application/json" } });
    const otherID = (await created.json().catch(() => ({}))).id || "";
    assert(otherID, `no second assistant: ${created.status()}`);
    const waiting = await asAssistant(assistantID, `/projects/${NAME}/docker/compose`, { stack: "", action: "down-volumes" });
    assert(waiting.json.pending === true, `the confirm action did not wait: ${waiting.raw}`);
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await page.waitForSelector(`${DIALOG} .swal2-checkbox input`, { timeout: 15000 });
    await page.check(`${DIALOG} .swal2-checkbox input`);
    await page.click(`${DIALOG} .swal2-confirm`);
    await page.waitForSelector(DIALOG, { state: "detached", timeout: 8000 });
    const approved = await approvalNote(page, assistantID, "done", 2);
    await waitRun(page, startedRun(approved.text), (r) => !r.running, "finished after the approval");
    const other = await asAssistant(otherID, `/projects/${NAME}/docker/compose`, { stack: "", action: "down-volumes" });
    assert(other.status === 200 && other.json.pending !== true && other.json.run, `another assistant was still asked: ${other.raw}`);
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

  await run("a delete dialog names the action, who asks and what it acts on, and shows no command", async () => {
    const created = await page.request.post(`${BASE}/assistants/new`, { form: { csrf_token: await csrfOf(page), form: "new", coder: "claude" }, headers: { Accept: "application/json" } });
    const otherID = (await created.json().catch(() => ({}))).id || "";
    assert(otherID, `no second assistant: ${created.status()}`);
    const waiting = await asAssistant(assistantID, `/assistants/${otherID}`, { form: "delete" });
    assert(waiting.status === 200 && waiting.json.pending === true, `the delete did not wait: ${waiting.raw}`);
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await page.waitForSelector(`${DIALOG} [data-approval-what]`, { timeout: 15000 });
    const what = (await page.textContent(`${DIALOG} [data-approval-what]`)).trim();
    const rows = await dialogRows(page);
    const labels = rows.map(([label]) => label);
    assert(JSON.stringify(labels) === JSON.stringify(["warning", "Assistant", "ID"]) && rows[0][1].endsWith("This cannot be undone.") && rows[2][1] === otherID.slice(0, 8), `the dialog's rows read ${JSON.stringify(rows)}`);
    assert(await askerOf(page) === `Assistant ${assistantID.slice(0, 8)}`, `the dialog names who asks as "${await askerOf(page)}"`);
    assert(what === `Delete assistant ${rows[1][1]}`, `the dialog's head reads "${what}"`);
    assert(await page.locator(`${DIALOG} .swal2-confirm.btn-danger`).count() === 1, "a delete's Approve is not red");
    assert(await page.locator(`${DIALOG} [data-gitprompt-command]`).count() === 0, "a delete shows a command line");
    await page.click(`${DIALOG} .swal2-deny`);
    await page.waitForSelector(DIALOG, { state: "detached", timeout: 8000 });
    const gone = await page.request.post(`${BASE}/assistants/${otherID}`, { form: { csrf_token: await csrfOf(page), form: "delete" }, headers: { Accept: "application/json" } });
    assert(gone.status() === 200, `the second assistant's delete answered ${gone.status()}`);
    return what;
  });

  await run("a project delete warns that its containers go down with their volumes", async () => {
    const waiting = await asAssistant(assistantID, "/projects/delete", { project: NAME });
    assert(waiting.status === 200 && waiting.json.pending === true, `the delete did not wait: ${waiting.raw}`);
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await page.waitForSelector(`${DIALOG} [data-approval-what]`, { timeout: 15000 });
    const warnings = (await dialogRows(page)).filter(([label]) => label === "warning").map(([, value]) => value);
    assert(JSON.stringify(warnings) === JSON.stringify([
      "Its coders and shells stop and the directory is removed with everything in it. This cannot be undone.",
      "Its directory goes, and its containers are brought down with compose first, volumes included.",
    ]), `the warnings read ${JSON.stringify(warnings)}`);
    await page.click(`${DIALOG} .swal2-deny`);
    await page.waitForSelector(DIALOG, { state: "detached", timeout: 8000 });
    return warnings[1];
  });

  await run("a delete of several lists one row each, its icon on the middle of the name's capitals", async () => {
    const ids = [];
    for (let i = 0; i < 2; i += 1) {
      const created = await page.request.post(`${BASE}/assistants/new`, { form: { csrf_token: await csrfOf(page), form: "new", coder: "claude" }, headers: { Accept: "application/json" } });
      const id = (await created.json().catch(() => ({}))).id || "";
      assert(id, `no extra assistant: ${created.status()}`);
      ids.push(id);
    }
    const offsets = [];
    try {
      const waiting = await asAssistant(assistantID, "/assistants/delete", ids.map((id) => ["assistant", id]));
      assert(waiting.status === 200 && waiting.json.pending === true, `the delete did not wait: ${waiting.raw}`);
      const views = [
        ["desktop 2x", { viewport: { width: 1440, height: 900 }, deviceScaleFactor: 2 }],
        ["phone 3x", { viewport: { width: 390, height: 844 }, deviceScaleFactor: 3, hasTouch: true, isMobile: engine === "chromium" }],
      ];
      for (const [where, options] of views) {
        const ctx = await browser.newContext({ ignoreHTTPSErrors: true, ...options });
        try {
          const p = await ctx.newPage();
          await L.login(p);
          await dismissUpdate(p);
          const rows = p.locator(`${DIALOG} .dc-ask-item`);
          await rows.first().waitFor({ state: "visible", timeout: 15000 });
          await p.waitForFunction(() => document.querySelector(".swal2-container").getAnimations({ subtree: true }).every((a) => a.playState !== "running"), null, { timeout: 8000 });
          assert(await rows.count() === 2, `${where}: the dialog lists ${await rows.count()} rows`);
          const drawn = [];
          for (let shift = 0; shift < 4; shift += 1) {
            await p.setViewportSize({ ...options.viewport, height: options.viewport.height + shift });
            await p.evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
            for (let i = 0; i < 2; i += 1) {
              const { icon, cap } = await rows.nth(i).evaluate((row) => {
                const h = document.createElement("span");
                h.textContent = "H";
                h.dataset.e2eCap = "";
                row.querySelector(".dc-ask-name").prepend(h);
                const box = (r) => ({ x: r.x, y: r.y, width: r.width, height: r.height });
                const r = h.getBoundingClientRect();
                return { icon: box(row.querySelector(":scope > .ti").getBoundingClientRect()), cap: { x: r.x + 1, y: r.y, width: r.width - 2, height: r.height } };
              });
              drawn.push((await inkMiddle(p, icon)) - (await inkMiddle(p, cap)));
              await rows.nth(i).evaluate((row) => row.querySelector("[data-e2e-cap]").remove());
            }
          }
          const mean = drawn.reduce((sum, v) => sum + v, 0) / drawn.length;
          offsets.push(`${where} ${mean.toFixed(2)}`);
          assert(Math.abs(mean) <= ICON_TOLERANCE, `${where}: the row icons stand ${mean.toFixed(2)}px off the middle of their names' capitals on average (${drawn.map((v) => v.toFixed(2)).join(" ")})`);
        } finally {
          await ctx.close();
        }
      }
    } finally {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      if (await page.locator(`${DIALOG} [data-approval-what]`).waitFor({ state: "visible", timeout: 15000 }).then(() => true, () => false)) {
        await page.click(`${DIALOG} .swal2-deny`);
        await page.waitForSelector(DIALOG, { state: "detached", timeout: 8000 });
      }
      for (const id of ids) {
        await page.request.post(`${BASE}/assistants/${id}`, { form: { csrf_token: await csrfOf(page), form: "delete" }, headers: { Accept: "application/json" } });
      }
    }
    return offsets.join(", ");
  });

  await run("approvals wait behind an open dialog, show one after the other once it closes, and are asked again when another dialog replaced them", async () => {
    const others = [];
    for (let i = 0; i < 2; i += 1) {
      const created = await page.request.post(`${BASE}/assistants/new`, { form: { csrf_token: await csrfOf(page), form: "new", coder: "claude" }, headers: { Accept: "application/json" } });
      const id = (await created.json().catch(() => ({}))).id || "";
      assert(id, `no extra assistant: ${created.status()}`);
      others.push(id);
    }
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await dismissUpdate(page);
    const openConfirm = (title) => page.evaluate((t) => {
      window.__dcConfirm = "open";
      return import("@dc/dialog").then((d) => { d.confirm({ title: t }).then((v) => { window.__dcConfirm = v; }); });
    }, title);
    const askedAbout = async () => (await dialogRows(page)).find(([label]) => label === "ID")?.[1];
    await openConfirm("E2E confirm");
    await page.waitForSelector(`${DIALOG} .dc-ask-title:text-is("E2E confirm")`, { timeout: 8000 });
    const listed = page.waitForResponse(async (res) => {
      if (res.request().method() !== "GET" || new URL(res.url()).pathname !== "/git/prompt") return false;
      const body = await res.json().catch(() => ({}));
      return (body.questions || []).length === 2;
    }, { timeout: 15000 });
    for (const id of others) {
      const waiting = await asAssistant(assistantID, `/assistants/${id}`, { form: "delete" });
      assert(waiting.status === 200 && waiting.json.pending === true, `the delete did not wait: ${waiting.raw}`);
    }
    await listed;
    await page.evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    const title = await page.textContent(`${DIALOG} .dc-ask-title`);
    assert(title === "E2E confirm", `an approval replaced the open confirm, the dialog reads "${title}"`);
    assert(await page.evaluate(() => window.__dcConfirm) === "open", "the open confirm was settled by an approval");
    assert(await page.locator(`${DIALOG} [data-approval-what]`).count() === 0, "an approval stands beside the confirm");

    await page.click(`${DIALOG} .swal2-cancel`);
    await page.waitForSelector(`${DIALOG} [data-approval-what]`, { timeout: 15000 });
    assert(await page.evaluate(() => window.__dcConfirm) === false, "the confirm did not end on its own Cancel");
    const first = await askedAbout();
    assert(first === others[0].slice(0, 8), `the first approval shown is about "${first}", not the oldest`);

    await openConfirm("E2E replace");
    await page.waitForSelector(`${DIALOG} .dc-ask-title:text-is("E2E replace")`, { timeout: 8000 });
    const still = await page.request.get(`${BASE}/git/prompt`, { headers: { Accept: "application/json" } });
    const standing = ((await still.json()).questions || []).length;
    assert(standing === 2, `the replaced approval was answered, ${standing} questions stand`);
    await page.click(`${DIALOG} .swal2-cancel`);
    await page.waitForSelector(`${DIALOG} [data-approval-what]`, { timeout: 15000 });
    assert(await page.evaluate(() => window.__dcConfirm) === false, "the replacing confirm did not end on its own Cancel");
    const again = await askedAbout();
    assert(again === first, `after the replacing dialog the approval is about "${again}", not "${first}" again`);

    await page.click(`${DIALOG} .swal2-deny`);
    await waitFor(async () => (await page.locator(`${DIALOG} [data-approval-details]`).count()) === 1 && (await askedAbout()) === others[1].slice(0, 8), "the second approval after the first was answered");
    await page.click(`${DIALOG} .swal2-deny`);
    await page.waitForSelector(DIALOG, { state: "detached", timeout: 8000 });
    for (const id of others) {
      const gone = await page.request.post(`${BASE}/assistants/${id}`, { form: { csrf_token: await csrfOf(page), form: "delete" }, headers: { Accept: "application/json" } });
      assert(gone.status() === 200, `the extra assistant's delete answered ${gone.status()}`);
    }
    return `${first}, asked again, then the second`;
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
