const http = require("http");
const https = require("https");
const L = require("./lib");
const { assert, BASE, sleep, dismissUpdate } = L;

// Projects shared read only with a guest who never logs in (internal/share,
// routes under /share/<token>/...), chromium. The owner makes a link under
// Settings › Sharing, reached from Share in the project's row menu with that
// project preselected and no dialog, named for whom it is (a name is
// optional, changes later and stays one short line), shown once with Copy.
// The list shows each project of a link as Read only, a project goes on a
// standing link from the same Share (preselected in the link's own Add) and
// comes off it again, revoke ends it, and the local API socket can change
// none of it. A link carries the address the owner made it on, a Base URL
// under Settings › Notifications set to another one changes nothing, and a
// name with markup in it shows as the text it is in the revoke dialog.
//
// What the guest sees and may not do is checked in project-share-guest.js,
// once the link stands. Unknown, revoked, expired (a one minute link, read
// at the end), deleted project and emptied links answer the 404 of an
// address that does not exist, a link of two projects keeps opening the one
// left when the other is deleted in the cockpit and ends when that one is
// removed in Settings › Sharing. A project removed in the owner's shell
// answers it under its old link and is gone from Sharing, its name made again
// opens nothing. Deleting the shared project in the cockpit ends the guest's
// event and shell streams while its directory still stands: the ticker
// ignores SIGTERM as PID 1, so its compose down takes the 10s stop timeout
// before the directory may go.
//
// Instance: its own, shaped like assistant.js's (tests/e2e/fakes first on
// PATH for the fake claude, a scratch HOME, --projects-dir and --state-dir
// of its own), reaching the docker daemon with alpine:latest local, its
// local API socket mounted (APISOCK, default /apisock/s). The stub remote
// listens on GIT_STUB_PORT (default 3142) inside the runner, so the runner
// needs --network host. A run takes about three minutes.

const APISOCK = process.env.APISOCK || "/apisock/s";
const GIT_STUB_PORT = Number(process.env.GIT_STUB_PORT || 3142);
const TAG = Date.now().toString(36).slice(-5);
const PROJECT = `pshare${TAG}`;
const SECOND = `psharesecond${TAG}`;
const OTHER = `pshareother${TAG}`;
const GONE = `psharegone${TAG}`;
const REMOVED = `psharerm${TAG}`;
const ORIGIN = new URL(BASE).origin;

// postAs posts from inside the owner's page, the way @dc/http does.
async function postAs(page, path, body, { json = false } = {}) {
  return page.evaluate(async ({ path, body, json }) => {
    const token = document.querySelector('meta[name="csrf-token"]')?.content || "";
    const res = await fetch(path, {
      method: "POST",
      headers: { "X-CSRF-Token": token, Accept: "application/json", ...(json ? { "Content-Type": "application/json" } : {}) },
      body: json ? JSON.stringify(body) : new URLSearchParams(body),
    });
    return { ok: res.ok, status: res.status, text: await res.text(), url: res.url };
  }, { path, body, json });
}

async function getAs(page, path) {
  return page.evaluate(async (p) => {
    const res = await fetch(p, { headers: { Accept: "application/json" } });
    return { status: res.status, text: await res.text(), url: res.url, redirected: res.redirected };
  }, path);
}

// type sends one line into an owner's terminal, through the route the
// terminal-input element posts to.
async function type(page, kind, id, line) {
  const res = await postAs(page, `/${kind}s/${id}/input`, { items: [{ text: line }, { control: "enter" }] }, { json: true });
  assert(res.ok, `typing into ${kind} ${id} answered ${res.status}`);
}

async function screen(page, kind, id) {
  const res = await getAs(page, `/${kind}s/${id}/copy?lines=400`);
  return res.status === 200 ? JSON.parse(res.text).text : "";
}

async function waitScreen(page, kind, id, needle, what, timeout = 20000) {
  const until = Date.now() + timeout;
  let text = "";
  while (Date.now() < until) {
    text = await screen(page, kind, id);
    if (needle instanceof RegExp ? needle.test(text) : text.includes(needle)) return text;
    await sleep(300);
  }
  throw new Error(`${what} never showed ${needle}: ${JSON.stringify(text.trim().slice(-300))}`);
}

// shellAsk runs a command in the owner's shell and answers the line it
// printed right before the marker.
let asked = 0;
async function shellAsk(page, id, command) {
  asked += 1;
  const marker = `asked-${TAG}-${asked}`;
  await type(page, "shell", id, `${command}; echo ${marker}`);
  const text = await waitScreen(page, "shell", id, new RegExp(`\\n${marker}`), command);
  const lines = text.split("\n").map((line) => line.trimEnd());
  const at = lines.lastIndexOf(marker);
  return lines[at - 1] || "";
}

// fetchRaw asks the instance with nothing of a browser: no cookie, no
// redirect followed, so a refusal shows as what it is.
function fetchRaw(url, { method = "GET", body = "", headers = {} } = {}) {
  return new Promise((resolve, reject) => {
    const req = https.request(url, { method, rejectUnauthorized: false, headers: { ...headers, ...(body ? { "Content-Length": Buffer.byteLength(body) } : {}) } }, (res) => {
      let raw = "";
      res.on("data", (chunk) => { raw += chunk; });
      res.on("end", () => resolve({ status: res.statusCode, body: raw, headers: res.headers }));
    });
    req.on("error", reject);
    req.end(body);
  });
}

// tap reads a stream raw, the way it reaches a browser, until stop or until
// the server ends it, which closed resolves.
function tap(url, headers = {}) {
  let raw = "";
  let req = null;
  let ended = () => {};
  const closed = new Promise((resolve) => { ended = resolve; });
  const opened = new Promise((resolve) => {
    req = https.request(url, { rejectUnauthorized: false, headers }, (res) => {
      res.on("data", (chunk) => { raw += chunk; });
      res.on("error", () => {});
      res.on("close", ended);
      resolve(res.statusCode);
    });
    req.on("error", () => { ended(); resolve(0); });
    req.end();
  });
  return { opened, closed, text: () => raw, stop: () => { req.destroy(); return raw; } };
}

function overSocket(path, form) {
  return new Promise((resolve) => {
    const data = new URLSearchParams(form).toString();
    const req = http.request({
      socketPath: APISOCK, path, method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded", "Content-Length": Buffer.byteLength(data), Accept: "application/json" },
    }, (res) => {
      res.resume();
      res.on("end", () => resolve(res.statusCode));
    });
    req.on("error", () => resolve(0));
    req.end(data);
  });
}

// openShare follows Share in the project's row menu of the list column, which
// leads to Settings › Sharing with the project preselected, no dialog.
async function openShare(page, project) {
  await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
  await dismissUpdate(page);
  await page.locator(`[data-index-project="${project}"]`).click({ button: "right" });
  await Promise.all([
    page.waitForURL((u) => u.pathname === "/settings/sharing" && u.searchParams.get("project") === project, { timeout: 10000 }),
    page.locator(".dc-context-menu .dropdown-item", { hasText: /^Share$/ }).click(),
  ]);
  await page.waitForSelector("[data-share-new]", { timeout: 10000 });
  assert(!(await page.$("[data-form-modal].show")), "Share opened a dialog");
}

// makeLink makes a named link from the project's Share and answers the link
// the page shows once.
async function makeLink(page, project, name) {
  await openShare(page, project);
  const form = page.locator("[data-share-new]");
  const picked = await form.locator('[name="project"]').inputValue();
  assert(picked === project, `the new link's project stands on ${picked}`);
  await form.locator('[name="name"]').fill(name);
  await form.locator('[name="amount"]').fill("1");
  await form.locator('[name="unit"]').selectOption("hours");
  await form.locator('button[type="submit"]').click();
  const field = page.locator("[data-share-created] [data-share-url]");
  await field.waitFor({ state: "visible", timeout: 10000 });
  const link = await field.inputValue();
  assert(new URL(link).origin === ORIGIN, `the link stands under ${new URL(link).origin}`);
  return link;
}

// linkOver makes a link without the page, for the links whose end is all
// a check reads.
async function linkOver(page, project, amount, unit, name = "") {
  const res = await postAs(page, "/settings/sharing", { form: "create", project, amount, unit, name });
  const link = (res.text.match(/value="([^"]*\/share\/[^"]+)"/) || [])[1];
  assert(res.ok && link, `making a link of ${project} answered ${res.status}`);
  assert(new URL(link).origin === ORIGIN, `the link stands under ${new URL(link).origin}`);
  return link;
}

// sharingRows reads Settings › Sharing: every link by id with its name and
// its projects with what they grant.
async function sharingRows(page) {
  await page.goto(`${BASE}/settings/sharing`, { waitUntil: "domcontentloaded" });
  return page.$$eval("[data-share-row]", (els) => els.map((el) => ({
    id: el.dataset.shareRow,
    name: el.querySelector("[data-share-name]").textContent.trim(),
    projects: [...el.querySelectorAll("[data-share-project]")].map((row) => `${row.dataset.shareProject} ${row.querySelector("[data-share-grants]").textContent.trim()}`),
  })));
}

async function linkIdNamed(page, name) {
  const row = (await sharingRows(page)).find((r) => r.name === name);
  assert(row, `Settings › Sharing lists no link named ${name}`);
  return row.id;
}

// addViaShare puts a project on a standing link from the project's own
// Share, where the link's Add stands on that project already.
async function addViaShare(page, project, id) {
  await openShare(page, project);
  const add = page.locator(`[data-share-row="${id}"] [data-share-add]`);
  const picked = await add.locator('[name="project"]').inputValue();
  assert(picked === project, `the link's Add stands on ${picked}`);
  await add.locator('button[type="submit"]').click();
  await page.waitForSelector(`[data-share-row="${id}"] [data-share-project="${project}"]`, { timeout: 10000 });
}

// removeFromLink takes a project off a link in Settings › Sharing. Only the
// last one asks, its removal ends the link.
async function removeFromLink(page, id, project, { last = false } = {}) {
  await page.goto(`${BASE}/settings/sharing`, { waitUntil: "domcontentloaded" });
  await page.click(`[data-share-row="${id}"] [data-share-project="${project}"] [data-share-remove]`);
  if (last) await L.confirmSwal(page);
  await page.waitForSelector(`[data-share-row="${id}"] [data-share-project="${project}"]`, { state: "detached", timeout: 10000 });
}

// gitStub is a remote that asks for a login, so git parks its question.
function gitStub() {
  const server = http.createServer((req, res) => {
    res.writeHead(401, { "WWW-Authenticate": 'Basic realm="share"' });
    res.end();
  });
  return new Promise((resolve) => server.listen(GIT_STUB_PORT, "127.0.0.1", () => resolve(server)));
}

async function waitText(read, needle, what, timeout = 15000) {
  const until = Date.now() + timeout;
  let text = "";
  while (Date.now() < until) {
    text = await read();
    if (needle instanceof RegExp ? needle.test(text) : text.includes(needle)) return text;
    await sleep(250);
  }
  throw new Error(`${what} never showed ${needle}: ${JSON.stringify(text.trim().slice(-200))}`);
}


L.runFeature("PROJECT-SHARE", async ({ browser, page, run, bag }) => {
  const fx = { project: PROJECT, second: SECOND, other: OTHER, tag: TAG, name: `for-guest-${TAG}` };
  let stub = null;
  try {
    await run("fixture: a project with files, git, a comment, terminals and a container", async () => {
      await L.createProject(page, PROJECT);
      await L.createProject(page, OTHER);
      const files = {
        "README.md": "# Shared\nshare-readme-marker\n",
        "src/app.js": `export const shareNeedle${TAG} = 42;\n`,
        ".gitignore": "ignored/\n",
        "ignored/secret.txt": "gitignored-secret-marker\n",
        ".env": "SHARE_ENV=env-marker\n",
        "compose.yaml": `services:\n  ticker:\n    image: alpine:latest\n    command: ["sh", "-c", "echo share-log-start; while true; do echo tick-$$(date +%s); sleep 1; done"]\n`,
      };
      for (const dir of ["src", "ignored"]) {
        const res = await postAs(page, `/projects/${PROJECT}/editor/mkdir`, { path: dir });
        assert(res.ok, `making ${dir} answered ${res.status}: ${res.text}`);
      }
      for (const [path, content] of Object.entries(files)) {
        const res = await postAs(page, `/projects/${PROJECT}/editor/file`, { path, content });
        assert(res.ok, `writing ${path} answered ${res.status}: ${res.text}`);
      }
      fx.shellUrl = await L.createShell(page, await L.projectPath(page, PROJECT));
      fx.shell = new URL(fx.shellUrl).pathname.split("/").pop();
      await type(page, "shell", fx.shell, `git init -q && git add -A && git -c user.name=Owner -c user.email=o@x commit -qm first-share-commit && echo git-ready-${TAG}`);
      await waitScreen(page, "shell", fx.shell, `\ngit-ready-${TAG}`, "the shell");
      const readme = await postAs(page, `/projects/${PROJECT}/editor/file`, { path: "README.md", content: "# Shared\nshare-readme-marker\nan uncommitted line\n" });
      assert(readme.ok, `changing README.md answered ${readme.status}`);
      const comment = await postAs(page, `/projects/${PROJECT}/editor/comments`, { path: "README.md", line: 2, text: `owner-note-${TAG}` }, { json: true });
      assert(comment.ok, `the comment answered ${comment.status}: ${comment.text}`);
      fx.path = await L.projectPath(page, PROJECT);
      fx.coderUrl = await L.createSession(page, fx.path, `pscoder${TAG}`, "claude");
      fx.coder = new URL(fx.coderUrl).pathname.split("/").pop();
      fx.otherShell = new URL(await L.createShell(page, await L.projectPath(page, OTHER))).pathname.split("/").pop();
      await L.createProject(page, SECOND);
      const second = await postAs(page, `/projects/${SECOND}/editor/file`, { path: "second.md", content: `second-marker-${TAG}\n` });
      assert(second.ok, `writing second.md answered ${second.status}: ${second.text}`);
      fx.secondShell = new URL(await L.createShell(page, await L.projectPath(page, SECOND))).pathname.split("/").pop();
      const up = await postAs(page, `/projects/${PROJECT}/docker/compose`, { stack: "", action: "up" });
      assert(up.ok, `compose up answered ${up.status}: ${up.text}`);
      const until = Date.now() + 60000;
      while (Date.now() < until && !fx.container) {
        const docker = JSON.parse((await getAs(page, `/projects/${PROJECT}/editor/docker`)).text);
        fx.container = (docker.containers || []).find((c) => c.running);
        if (!fx.container) await sleep(500);
      }
      assert(fx.container, "the project's container never ran");
      // The owner looks at the shell last and opens the other project's
      // editor last, which is what the two entries must keep answering.
      await page.goto(`${BASE}/projects/${OTHER}/editor`, { waitUntil: "domcontentloaded" });
      await page.goto(fx.shellUrl, { waitUntil: "domcontentloaded" });
      await sleep(1500);
    });

    await run("the owner makes the link from the project's Share on the address of the request and Sharing lists it", async () => {
      const elsewhere = await postAs(page, "/settings/notifications", { form: "base-url", base_url: "https://elsewhere.invalid:8443" });
      assert(elsewhere.ok, `setting the base URL answered ${elsewhere.status}`);
      try {
        fx.link = await makeLink(page, PROJECT, fx.name);
        fx.expiring = await linkOver(page, PROJECT, "1", "minutes");
        fx.expiringAt = Date.now();
      } finally {
        await postAs(page, "/settings/notifications", { form: "base-url", base_url: "" });
      }
      fx.base = new URL(fx.link).pathname;
      const rows = (await sharingRows(page)).filter((row) => row.projects.some((p) => p.startsWith(`${PROJECT} `)));
      assert(rows.length === 2 && rows.every((row) => row.projects.join(",") === `${PROJECT} Read only`), `the list reads ${JSON.stringify(rows)}`);
      assert(rows.map((row) => row.name).join(",") === `${fx.name},Unnamed link`, `the links are named ${rows.map((row) => row.name)}`);
      fx.linkId = rows[0].id;
      const html = await page.content();
      assert(!html.includes(fx.base.split("/").pop()), "the list shows a token");
      fx.after = new URL(await L.createShell(page, await L.projectPath(page, PROJECT))).pathname.split("/").pop();
      await page.goto(fx.shellUrl, { waitUntil: "domcontentloaded" });
      await sleep(1000);
      return `${fx.base.slice(0, 15)}…`;
    });

    await run("the local API socket can change no link", async () => {
      const answers = [];
      for (const form of [
        { form: "create", project: PROJECT, amount: "1", unit: "hours" },
        { form: "add", id: fx.linkId, project: OTHER },
        { form: "remove", id: fx.linkId, project: PROJECT },
        { form: "update", id: fx.linkId, name: "socket" },
        { form: "revoke", id: fx.linkId },
      ]) {
        answers.push(await overSocket("/settings/sharing", form));
      }
      assert(answers.every((status) => status === 403), `the socket answered ${answers}`);
      assert((await fetchRaw(fx.link)).status === 303, "the link moved over the socket");
    });

    await run("a link's name is optional, changes later and stays one short line", async () => {
      fx.unnamed = await linkOver(page, PROJECT, "2", "hours");
      const before = (await sharingRows(page)).length;
      const long = await postAs(page, "/settings/sharing", { form: "create", project: PROJECT, amount: "1", unit: "hours", name: "x".repeat(65) });
      assert(long.text.includes("at most 64 characters") && !long.text.includes("data-share-url"), `a long name answered ${long.status}`);
      const twoLines = await postAs(page, "/settings/sharing", { form: "create", project: PROJECT, amount: "1", unit: "hours", name: "a\nb" });
      assert(twoLines.text.includes("at most 64 characters") && !twoLines.text.includes("data-share-url"), `a name of two lines answered ${twoLines.status}`);
      const rows = await sharingRows(page);
      assert(rows.length === before, `a refused name made a link: ${rows.length} after ${before}`);
      const id = rows[rows.length - 1].id;
      assert(rows[rows.length - 1].name === "Unnamed link", `the new link is named ${rows[rows.length - 1].name}`);
      const form = page.locator(`[data-share-row="${id}"] [data-share-update]`);
      fx.unnamedName = `Team <b>${TAG}</b> & co`;
      await form.locator('[name="name"]').fill(fx.unnamedName);
      await form.locator('button[type="submit"]').click();
      await page.waitForFunction(({ i, name }) => document.querySelector(`[data-share-row="${i}"] [data-share-name]`)?.textContent.trim() === name, { i: id, name: fx.unnamedName }, { timeout: 10000 });
      assert((await fetchRaw(fx.unnamed)).status === 303, "the renamed link no longer opens");
      fx.unnamedId = id;
    });

    fx.before = {
      size: await shellAsk(page, fx.shell, "stty size"),
      theme: await shellAsk(page, fx.shell, "tmux show -pv window-style"),
      editorEntry: (await getAs(page, "/editor")).url,
      terminalsEntry: (await getAs(page, "/terminals")).url,
    };
    stub = await gitStub();
    await require("./project-share-guest").guestChecks({
      browser, page, run, bag, fx, origin: ORIGIN, gitStubPort: GIT_STUB_PORT,
      postAs, getAs, type, shellAsk, fetchRaw, tap, waitText, addViaShare, removeFromLink, sharingRows,
    });

    await run("a project removed outside the cockpit takes its link with it", async () => {
      await L.createProject(page, REMOVED);
      const link = await linkOver(page, REMOVED, "2", "hours");
      const path = await L.projectPath(page, REMOVED);
      assert(path.endsWith(`/${REMOVED}`), `the project lies at ${path}`);
      const removed = await shellAsk(page, fx.shell, `rm -rf -- '${path}'; test -e '${path}' && echo kept || echo removed`);
      assert(removed === "removed", `rm -rf answered ${removed}`);
      const rows = (await sharingRows(page)).filter((row) => row.projects.some((p) => p.startsWith(`${REMOVED} `)));
      assert(!rows.length, `Sharing still lists ${JSON.stringify(rows)}`);
      const nothing = await fetchRaw(`${BASE}/no/such/page`);
      const gone = await fetchRaw(link);
      assert(gone.status === 404 && gone.body === nothing.body, `the old link answered ${gone.status}`);
      await L.createProject(page, REMOVED);
      const again = await fetchRaw(link);
      assert(again.status === 404 && again.body === nothing.body, `the name made again answered ${again.status}`);
      return `${gone.status}, made again ${again.status}`;
    });

    await run("a link ends with its last project, deleted in the cockpit or removed in Settings › Sharing", async () => {
      const nothing = await fetchRaw(`${BASE}/no/such/page`);
      await L.createProject(page, GONE);
      fx.goneLink = await linkOver(page, GONE, "2", "hours");
      const pair = await linkOver(page, GONE, "2", "hours", `pair-${TAG}`);
      const pairId = await linkIdNamed(page, `pair-${TAG}`);
      const added = await postAs(page, "/settings/sharing", { form: "add", id: pairId, project: OTHER });
      assert(added.ok && added.text.includes("Project added"), `adding ${OTHER} answered ${added.status}`);
      await L.deleteProject(page, GONE);
      const kept = await fetchRaw(pair);
      assert(kept.status === 303 && kept.headers.location === `${new URL(pair).pathname}/projects/${OTHER}/editor`, `the pair answered ${kept.status} ${kept.headers.location}`);
      const off = await fetchRaw(`${pair}/projects/${GONE}/editor`);
      assert(off.status === 404 && off.body === nothing.body, `the deleted project under the pair answered ${off.status}`);
      const row = (await sharingRows(page)).find((r) => r.id === pairId);
      assert(row && row.projects.join(",") === `${OTHER} Read only`, `the pair lists ${JSON.stringify(row)}`);
      await removeFromLink(page, pairId, OTHER, { last: true });
      assert(!(await sharingRows(page)).some((r) => r.id === pairId), "the emptied link is still listed");
      const ended = await fetchRaw(pair);
      assert(ended.status === 404 && ended.body === nothing.body, `the emptied link answered ${ended.status}`);
      return `kept ${kept.status}, emptied ${ended.status}`;
    });

    await run("dead links answer the 404 of an address that does not exist", async () => {
      const nothing = await fetchRaw(`${BASE}/no/such/page`);
      const unknown = await fetchRaw(`${BASE}/share/${"A".repeat(43)}`);
      await page.goto(`${BASE}/settings/sharing`, { waitUntil: "domcontentloaded" });
      await page.locator(`[data-share-row="${fx.unnamedId}"] [data-share-revoke]`).click();
      await page.waitForSelector(".swal2-title", { state: "visible", timeout: 8000 });
      const asked = await page.$eval(".swal2-title", (el) => ({ text: el.textContent, markup: el.children.length }));
      assert(asked.text === `Revoke the link "${fx.unnamedName}"? It stops working at once.` && !asked.markup, `the revoke dialog reads ${JSON.stringify(asked)}`);
      await L.confirmSwal(page);
      await page.waitForSelector(`[data-share-row="${fx.unnamedId}"]`, { state: "detached", timeout: 10000 });
      const wait = 62000 - (Date.now() - fx.expiringAt);
      if (wait > 0) await sleep(wait);
      const answers = {};
      for (const [name, url] of Object.entries({ unknown: unknown, revoked: fx.unnamed, deleted: fx.goneLink, expired: fx.expiring })) {
        const res = typeof url === "string" ? await fetchRaw(url) : url;
        answers[name] = res.status;
        assert(res.status === 404 && res.body === nothing.body, `${name} answered ${res.status}`);
      }
      return JSON.stringify(answers);
    });

    await run("deleting the project in the cockpit ends the guest's streams before its directory goes", async () => {
      const at = (path) => `${BASE}${fx.base}${path}`;
      const events = tap(at("/events"));
      const stream = tap(at(`/shells/${fx.shell}/stream`));
      assert((await events.opened) === 200 && (await stream.opened) === 200, "a guest stream did not open");
      await waitText(async () => events.text(), `"type":"docker"`, "the guest's event stream");
      await waitText(async () => stream.text(), "event: snapshot", "the guest's shell stream");
      await L.deleteProject(page, PROJECT);
      const ended = await Promise.race([Promise.all([events.closed, stream.closed]).then(() => true), sleep(5000).then(() => false)]);
      assert(ended, "a guest stream outlived the start of the deletion");
      const stands = await shellAsk(page, fx.otherShell, `test -e '${fx.path}' && echo kept || echo removed`);
      assert(stands === "kept", `the directory went before the guest's streams ended: ${stands}`);
      const until = Date.now() + 60000;
      let state = stands;
      while (state !== "removed" && Date.now() < until) {
        await sleep(1000);
        state = await shellAsk(page, fx.otherShell, `test -e '${fx.path}' && echo kept || echo removed`);
      }
      assert(state === "removed", "the deletion never removed the directory");
      const nothing = await fetchRaw(`${BASE}/no/such/page`);
      const gone = await fetchRaw(fx.link);
      assert(gone.status === 404 && gone.body === nothing.body, `the link answered ${gone.status} after the deletion`);
      return `streams ended, then the directory went`;
    });
  } finally {
    if (stub) stub.close();
    await L.deleteProject(page, PROJECT).catch(() => {});
    await L.deleteProject(page, SECOND).catch(() => {});
    await L.deleteProject(page, OTHER).catch(() => {});
    await L.deleteProject(page, GONE).catch(() => {});
    await L.deleteProject(page, REMOVED).catch(() => {});
  }
});
