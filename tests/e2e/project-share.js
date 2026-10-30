const http = require("http");
const https = require("https");
const L = require("./lib");
const { assert, BASE, sleep, dismissUpdate } = L;
const { guestPage, openCommentsSheet } = require("./project-share-guest");

// Projects shared read only with a guest who never logs in (internal/share,
// routes under /share/<token>/...), chromium. Settings › Sharing is the list
// of links, a compact row each with its name, what it opens with the Comment
// mark and its expiry, marked when it ends within a day. Share in the
// project's row menu opens it with the New link dialog standing and that
// project picked, the address left bare. A link is named for whom it is (a
// name is required, refused empty also from a hand built request and stays
// one short line) and never renamed, neither by its Edit, which has no name
// field, nor by a hand built save. It is shown once in the dialog with Copy.
// Projects are picked in one dropdown that searches them (dc-picker), several
// at once, Enter picks the hit and sends nothing, Escape closes the dropdown
// and leaves the dialog, and with forty more projects the dialog still fits
// one phone screen while the dropdown scrolls. A link's Edit puts projects on
// from the same dropdown and takes them off in one save, the last one stays,
// Revoke ends it, and the local API socket can change none of it. A guest's
// empty comments sheet says so without asking to add one, the owner's still
// asks. A link carries the address the owner made it on, a Base URL under
// Settings › Notifications set to another one changes nothing, and a name
// with markup in it shows as the text it is in the revoke dialog.
//
// What the guest sees and may not do is checked in project-share-guest.js,
// once the link stands, what a link granting Comment lets it do in
// project-share-comments.js. Unknown, revoked, expired (a one minute link, read
// at the end), deleted project and emptied links answer the 404 of an
// address that does not exist, a link of two projects keeps opening the one
// left when the other is deleted in the cockpit and keeps that one when a
// save would take it off. A project removed in the owner's shell
// answers it under its old link and is gone from Sharing, its name made again
// opens nothing. Deleting the shared project in the cockpit ends the guest's
// event and shell streams while its directory still stands: the ticker
// ignores SIGTERM as PID 1, so its compose down takes the 10s stop timeout
// before the directory may go.
//
// Instance: its own, shaped like assistant.js's (tests/e2e/fakes first on
// PATH for the fake claude, a scratch HOME, --projects-dir and --state-dir
// of its own), reaching the docker daemon with alpine:latest local, its
// local API socket mounted (APISOCK, default /apisock/s), its Go file on the
// real gopls image the cockpit builds when the tag is missing. The stub remote
// listens on GIT_STUB_PORT (default 3142) inside the runner, published on the
// host's loopback (-p 127.0.0.1:3142:3142) where the instance's git reaches
// it. The runner stays in a network namespace of its own: the stacks it
// composes and the language server's container add host interfaces, and
// under --network host each of them aborts the browser's loads with
// ERR_NETWORK_CHANGED. A run takes about three minutes.

const APISOCK = process.env.APISOCK || "/apisock/s";
const GIT_STUB_PORT = Number(process.env.GIT_STUB_PORT || 3142);
const TAG = Date.now().toString(36).slice(-5);
const PROJECT = `pshare${TAG}`;
const SECOND = `psharesecond${TAG}`;
const OTHER = `pshareother${TAG}`;
const GONE = `psharegone${TAG}`;
const REMOVED = `psharerm${TAG}`;
// More projects than the review measured the pill block with (44), as empty
// directories made in the owner's shell, so rmdir alone takes them again.
const MANY = `pshare${TAG}many`;
const each = (parent, command) => `for i in $(seq 10 49); do ${command} -- '${parent}/${MANY}'$i || exit 1; done`;
const ORIGIN = new URL(BASE).origin;
// The one picker of a form, and the dialog a form stands in.
const PROJECTS = "dc-picker";
const DIALOG = "[data-form-modal].show";

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
// opens Settings › Sharing with the New link dialog on it, and answers the
// dialog's form.
async function openShare(page, project) {
  await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
  await dismissUpdate(page);
  await page.locator(`[data-index-project="${project}"]`).click({ button: "right" });
  await page.locator(".dc-context-menu .dropdown-item", { hasText: /^Share$/ }).click();
  await page.waitForSelector(`${DIALOG} [data-share-new]`, { timeout: 10000 });
  const at = new URL(page.url());
  assert(at.pathname === "/settings/sharing" && !at.search, `Share landed on ${page.url()}`);
  return page.locator(`${DIALOG} [data-share-new]`);
}

// readPicker reads a picker: what it sends, what its closed toggle says and
// the projects it offers.
function readPicker(scope) {
  return scope.locator(PROJECTS).evaluate((el) => ({
    sends: [...el.querySelectorAll('[name="project"]:checked')].map((box) => box.value),
    says: el.querySelector("[data-picker-summary]").textContent.trim(),
    count: el.querySelector("[data-picker-count]").hidden ? "" : el.querySelector("[data-picker-count]").textContent.trim(),
    offers: [...el.querySelectorAll("[data-picker-source] [data-project-name]")].map((row) => row.dataset.projectName),
  }));
}

// listed reads the rows an open picker shows, each with whether it is picked.
function listed(scope) {
  return scope.locator(`${PROJECTS} [data-picker-list]`).evaluate((list) => [...list.querySelectorAll('[role="option"]')]
    .map((row) => `${row.dataset.projectName || row.dataset.pickerName}${row.getAttribute("aria-selected") === "true" && row.querySelector('input[type="checkbox"]').checked ? " picked" : ""}`));
}

// menuShows reads whether a picker's menu really stands on the screen.
function menuShows(scope) {
  return scope.locator(`${PROJECTS} .dropdown-menu`).evaluate((menu) => {
    const box = menu.getBoundingClientRect();
    return getComputedStyle(menu).display !== "none" && box.width > 0 && box.height > 0;
  });
}

// madeLink sends the New link dialog and answers the link it shows once and
// what it says about it, then closes the dialog.
async function madeLink(page, form) {
  await form.locator('button[type="submit"]').click();
  const field = page.locator(`${DIALOG} [data-share-created] [data-share-url]`);
  await field.waitFor({ state: "visible", timeout: 10000 });
  const made = { link: await field.inputValue(), says: (await page.textContent(`${DIALOG} [data-share-created]`)).trim() };
  assert(new URL(made.link).origin === ORIGIN, `the link stands under ${new URL(made.link).origin}`);
  await page.click(`${DIALOG} [data-share-created] .btn-primary`);
  await page.waitForSelector(DIALOG, { state: "detached", timeout: 10000 });
  return made;
}

// makeLink makes a named link from the project's Share and answers the link
// the dialog shows once.
async function makeLink(page, project, name) {
  const form = await openShare(page, project);
  const picker = await readPicker(form);
  assert(picker.sends.join(",") === project && picker.says === project && picker.count === "1", `the new link's projects stand on ${JSON.stringify(picker)}`);
  await form.locator('[name="name"]').fill(name);
  await form.locator('[name="amount"]').fill("1");
  await form.locator('[name="unit"]').selectOption("hours");
  return (await madeLink(page, form)).link;
}

// linkOver makes a link of one project or several without the page, for the
// links whose end is all a check reads.
async function linkOver(page, projects, amount, unit, name) {
  const form = [["amount", amount], ["unit", unit], ["name", name], ...[].concat(projects).map((p) => ["project", p])];
  const res = await postAs(page, "/settings/sharing/new", form);
  const link = (res.text.match(/value="([^"]*\/share\/[^"]+)"/) || [])[1];
  assert(res.ok && link, `making a link of ${projects} answered ${res.status}`);
  assert(new URL(link).origin === ORIGIN, `the link stands under ${new URL(link).origin}`);
  return link;
}

// sharingRows reads Settings › Sharing: every link by id with its name and
// its projects with what they grant, read from the Comment mark.
async function sharingRows(page) {
  await page.goto(`${BASE}/settings/sharing`, { waitUntil: "domcontentloaded" });
  return page.$$eval("[data-share-row]", (els) => els.map((el) => ({
    id: el.dataset.shareRow,
    name: el.querySelector("[data-share-name]").textContent.trim(),
    projects: [...el.querySelectorAll("[data-share-project]")].map((row) => `${row.dataset.shareProject} ${row.querySelector('[data-share-grant="comments"]') ? "Read, comment" : "Read only"}`),
  })));
}

async function linkIdNamed(page, name) {
  const row = (await sharingRows(page)).find((r) => r.name === name);
  assert(row, `Settings › Sharing lists no link named ${name}`);
  return row.id;
}

// editLink opens a link's Edit from its row and answers the dialog's form.
async function editLink(page, id) {
  await page.goto(`${BASE}/settings/sharing`, { waitUntil: "domcontentloaded" });
  await dismissUpdate(page);
  await page.click(`[data-share-row="${id}"] [data-share-edit]`);
  await page.waitForSelector(`${DIALOG} [data-share-edit-form]`, { timeout: 10000 });
  return page.locator(`${DIALOG} [data-share-edit-form]`);
}

// saveEdit saves a link's Edit, which closes the dialog with its toast.
async function saveEdit(page, form) {
  await form.locator("[data-share-save]").click();
  await page.waitForSelector(DIALOG, { state: "detached", timeout: 10000 });
  await waitText(() => page.textContent(".dc-toast"), "Link saved.", "the save's toast");
}

// addViaEdit puts a project on a standing link from its Edit's Add, and the
// open list shows it without a reload.
async function addViaEdit(page, project, id) {
  const form = await editLink(page, id);
  await form.locator(`${PROJECTS} [data-picker-toggle]`).click();
  await form.locator(`${PROJECTS} [data-picker-filter]`).fill(project);
  await form.locator(`${PROJECTS} [data-picker-list] [data-project-name="${project}"]`).click();
  await page.keyboard.press("Escape");
  await saveEdit(page, form);
  await page.waitForSelector(`[data-share-row="${id}"] [data-share-project="${project}"]`, { timeout: 10000 });
}

// removeFromLink takes a project off a link with its cross in the link's
// Edit and saves, and the open list follows.
async function removeFromLink(page, id, project) {
  const form = await editLink(page, id);
  await form.locator(`[data-share-project="${project}"] label[for^="share-remove-"]`).click();
  await saveEdit(page, form);
  await page.waitForSelector(`[data-share-row="${id}"] [data-share-project="${project}"]`, { state: "detached", timeout: 10000 });
}

// gitStub is a remote that asks for a login, so git parks its question.
function gitStub() {
  const server = http.createServer((req, res) => {
    res.writeHead(401, { "WWW-Authenticate": 'Basic realm="share"' });
    res.end();
  });
  return new Promise((resolve) => server.listen(GIT_STUB_PORT, () => resolve(server)));
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


L.runFeature("PROJECT-SHARE", async ({ browser, page, run, bag, mobilePage }) => {
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
        "go.mod": "module example.com/share\n\ngo 1.22\n",
        "main.go": "package main\n\nimport \"fmt\"\n\nfunc main() {\n\t_ = fmt.Errorf(\"share\")\n}\n\ntype ShareProbe struct{}\n\ntype Prober interface{ Probe() }\n\nfunc (ShareProbe) Probe() {}\n\nvar _ Prober = ShareProbe{}\n",
        "compose.yaml": `services:\n  ticker:\n    image: alpine:latest\n    command: ["sh", "-c", "echo share-log-start; while true; do echo tick-$$(date +%s); sleep 1; done"]\n    ports: ["127.0.0.1::8080"]\n`,
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
      const secondNote = await postAs(page, `/projects/${SECOND}/editor/comments`, { path: "second.md", line: 1, text: `second-note-${TAG}` }, { json: true });
      assert(secondNote.ok, `the second project's comment answered ${secondNote.status}: ${secondNote.text}`);
      fx.secondShell = new URL(await L.createShell(page, await L.projectPath(page, SECOND))).pathname.split("/").pop();
      const idle = `services:\n  idle:\n    image: alpine:latest\n    command: ["sh", "-c", "trap exit TERM; while true; do sleep 1; done"]\n    ports: ["127.0.0.1::8081"]\n`;
      const otherCompose = await postAs(page, `/projects/${OTHER}/editor/file`, { path: "compose.yaml", content: idle });
      assert(otherCompose.ok, `writing the other project's compose.yaml answered ${otherCompose.status}: ${otherCompose.text}`);
      const running = {};
      for (const name of [PROJECT, OTHER]) {
        const up = await postAs(page, `/projects/${name}/docker/compose`, { stack: "", action: "up" });
        assert(up.ok, `compose up of ${name} answered ${up.status}: ${up.text}`);
      }
      const until = Date.now() + 60000;
      while (Date.now() < until && !(running[PROJECT] && running[OTHER])) {
        for (const name of [PROJECT, OTHER]) {
          const docker = JSON.parse((await getAs(page, `/projects/${name}/editor/docker`)).text);
          running[name] = (docker.containers || []).find((c) => c.running && (c.links || []).length);
        }
        if (!(running[PROJECT] && running[OTHER])) await sleep(500);
      }
      fx.container = running[PROJECT];
      assert(fx.container && running[OTHER], "a project's container never ran with its published port");
      fx.ports = Object.fromEntries([PROJECT, OTHER].map((name) => [name, running[name].links[0].port]));
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
        fx.expiring = await linkOver(page, PROJECT, "1", "minutes", `expiring-${TAG}`);
        fx.expiringAt = Date.now();
      } finally {
        await postAs(page, "/settings/notifications", { form: "base-url", base_url: "" });
      }
      fx.base = new URL(fx.link).pathname;
      const rows = (await sharingRows(page)).filter((row) => row.projects.some((p) => p.startsWith(`${PROJECT} `)));
      assert(rows.length === 2 && rows.every((row) => row.projects.join(",") === `${PROJECT} Read only`), `the list reads ${JSON.stringify(rows)}`);
      assert(rows.map((row) => row.name).join(",") === `${fx.name},expiring-${TAG}`, `the links are named ${rows.map((row) => row.name)}`);
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
      for (const [path, form] of [
        ["/settings/sharing/new", { name: "socket", project: PROJECT, amount: "1", unit: "hours" }],
        [`/settings/sharing/${fx.linkId}`, { form: "save", project: OTHER }],
        [`/settings/sharing/${fx.linkId}`, { form: "save", resource: `project:${PROJECT}`, drop: `project:${PROJECT}` }],
        [`/settings/sharing/${fx.linkId}`, { form: "save", amount: "5", unit: "minutes" }],
        [`/settings/sharing/${fx.linkId}`, { form: "revoke" }],
      ]) {
        answers.push(await overSocket(path, form));
      }
      assert(answers.every((status) => status === 403), `the socket answered ${answers}`);
      assert((await fetchRaw(fx.link)).status === 303, "the link moved over the socket");
    });

    await run("the list is one compact row per link, a card on a phone with every name whole", async () => {
      fx.longName = `Maximilian Oberhauser-Wittgenstein, external review ${TAG}`;
      await linkOver(page, OTHER, "2", "days", fx.longName);
      await linkOver(page, OTHER, "3", "days", `${"w".repeat(58)}${TAG}`);
      await page.goto(`${BASE}/settings/sharing`, { waitUntil: "domcontentloaded" });
      const desk = await page.evaluate((long) => {
        const rows = [...document.querySelectorAll("[data-share-row]")];
        const right = (el) => Math.round(el.getBoundingClientRect().right);
        const head = document.querySelector("[data-share-list] .card-header .col-3");
        const row = rows.find((r) => r.querySelector("[data-share-name]").textContent.trim() === long);
        return {
          rows: rows.length,
          tall: rows.filter((r) => r.getBoundingClientRect().height > 110).length,
          aligned: rows.every((r) => Math.abs(right(r.querySelector("[data-share-expires]")) - right(head)) <= 1),
          edits: new Set(rows.map((r) => right(r.querySelector("[data-share-edit]")))).size,
          soon: rows.filter((r) => r.querySelector("[data-share-soon]")).map((r) => r.querySelector("[data-share-name]").textContent.trim()),
          kinds: row && [...row.querySelectorAll("[data-share-project] .ti-folder")].length,
        };
      }, fx.longName);
      assert(desk.rows >= 4 && !desk.tall && desk.aligned && desk.edits === 1 && desk.kinds === 1, `the desktop list reads ${JSON.stringify(desk)}`);
      assert(desk.soon.includes(fx.name) && desk.soon.includes(`expiring-${TAG}`) && !desk.soon.includes(fx.longName), `ending soon are ${desk.soon}`);
      const mp = await mobilePage();
      await mp.goto(`${BASE}/settings/sharing`, { waitUntil: "domcontentloaded" });
      await dismissUpdate(mp);
      const phone = await mp.evaluate(() => {
        const width = window.innerWidth;
        const cut = (el) => el.scrollWidth > el.clientWidth + 1 || getComputedStyle(el).textOverflow === "ellipsis";
        const names = [...document.querySelectorAll("[data-share-name], [data-share-row] .text-break")];
        const rows = [...document.querySelectorAll("[data-share-row]")];
        return {
          scrolls: document.documentElement.scrollWidth > width + 1,
          cut: names.filter(cut).map((el) => el.textContent.trim()),
          outside: rows.filter((r) => r.getBoundingClientRect().right > width + 1 || r.querySelector("[data-share-edit]").getBoundingClientRect().right > width).length,
          head: getComputedStyle(document.querySelector("[data-share-list] .card-header")).display,
          stacked: rows.every((r) => r.querySelector("[data-share-name]").getBoundingClientRect().bottom <= r.querySelector("[data-share-expires]").getBoundingClientRect().top),
        };
      });
      assert(!phone.scrolls && !phone.cut.length && !phone.outside && phone.head === "none" && phone.stacked, `the phone list reads ${JSON.stringify(phone)}`);
    });

    await run("a link's name is required, stays one short line and is never renamed", async () => {
      fx.teamName = `Team <b>${TAG}</b> & co`;
      fx.team = await linkOver(page, PROJECT, "2", "hours", fx.teamName);
      const before = (await sharingRows(page)).length;
      const needed = "A link needs a name.";
      for (const [name, says] of [["", needed], ["  ", needed], ["x".repeat(65), "at most 64 characters"], ["a\nb", "at most 64 characters"]]) {
        const res = await postAs(page, "/settings/sharing/new", { project: PROJECT, amount: "1", unit: "hours", name });
        assert(res.text.includes(says) && !res.text.includes("data-share-url"), `the name ${JSON.stringify(name)} answered ${res.status}`);
      }
      const create = await openShare(page, PROJECT);
      await create.locator('[name="name"]').fill("");
      await create.locator('button[type="submit"]').click();
      await sleep(500);
      assert(await create.locator('[name="name"]').evaluate((el) => el.validity.valueMissing), "the form sent an empty name");
      assert(await page.isVisible(`${DIALOG} [data-share-new]`), "an empty name closed the dialog");
      const rows = await sharingRows(page);
      assert(rows.length === before, `a refused name made a link: ${rows.length} after ${before}`);
      const id = await linkIdNamed(page, fx.teamName);
      const edit = await editLink(page, id);
      const title = await page.$eval("[data-form-modal-title]", (el) => ({ text: el.textContent, markup: el.children.length }));
      assert(await edit.locator('[name="name"]').count() === 0 && title.text === fx.teamName && !title.markup, `the Edit reads ${JSON.stringify(title)}`);
      await edit.locator('[name="amount"]').fill("3");
      await saveEdit(page, edit);
      const saved = await postAs(page, `/settings/sharing/${id}`, { form: "save", name: "renamed" });
      const old = await postAs(page, "/settings/sharing", { form: "update", id, name: "renamed" });
      assert(saved.ok && saved.text.includes("Link saved.") && [404, 405].includes(old.status), `the hand built renames answered ${saved.status} and ${old.status}`);
      assert((await sharingRows(page)).find((r) => r.id === id).name === fx.teamName, "a hand built request renamed the link");
      assert((await fetchRaw(fx.team)).status === 303, "the saved link no longer opens");
      fx.teamId = id;
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
      postAs, getAs, type, shellAsk, fetchRaw, tap, waitText, addViaEdit, removeFromLink, sharingRows,
    });
    await require("./project-share-comments").commentChecks({
      browser, page, run, bag, fx, origin: ORIGIN, postAs, getAs, fetchRaw, waitText, openShare, madeLink, editLink, saveEdit, sharingRows,
    });

    await run("a link opens several projects picked at once from the searched dropdown", async () => {
      fx.parent = fx.path.slice(0, fx.path.lastIndexOf("/"));
      const made = await shellAsk(page, fx.shell, `(${each(fx.parent, "mkdir")}) && echo made`);
      assert(made === "made", `making the projects answered ${made}`);
      fx.many = true;
      const form = await openShare(page, PROJECT);
      const offered = (await readPicker(form)).offers;
      assert(offered.filter((name) => name.startsWith(MANY)).length === 40 && offered.includes(OTHER), `the picker offers ${offered.length} projects`);
      await form.locator(`${PROJECTS} [data-picker-toggle]`).click();
      assert(await menuShows(form), "the picker's menu did not open");
      assert(await form.locator(`${PROJECTS} [data-picker-filter]`).evaluate((el) => el === document.activeElement), "the search does not hold the focus");
      const scroll = await form.locator(`${PROJECTS} [data-picker-list]`).evaluate((el) => ({ scrolls: el.scrollHeight > el.clientHeight, bottom: el.closest(".dropdown-menu").getBoundingClientRect().bottom, height: window.innerHeight }));
      assert(scroll.scrolls && scroll.bottom <= scroll.height, `the menu reads ${JSON.stringify(scroll)}`);
      await page.keyboard.type(OTHER);
      assert((await listed(form)).join(",") === OTHER, `searching ${OTHER} lists ${await listed(form)}`);
      await page.keyboard.press("Enter");
      assert((await listed(form)).join(",") === `${OTHER} picked`, `Enter left ${await listed(form)}`);
      assert(!(await page.$("[data-share-created]")) && await page.isVisible(`${DIALOG} [data-share-new]`), "Enter sent the form");
      await page.keyboard.press("Escape");
      assert(!(await menuShows(form)) && await page.isVisible(`${DIALOG} [data-share-new]`), "Escape left the menu open or closed the dialog");
      const picker = await readPicker(form);
      assert(picker.sends.slice().sort().join(",") === [PROJECT, OTHER].sort().join(",") && picker.count === "2" && picker.says.includes(OTHER) && picker.says.includes(PROJECT), `the picker reads ${JSON.stringify(picker)}`);
      fx.twoName = `two-${TAG}`;
      await form.locator('[name="name"]').fill(fx.twoName);
      const two = await madeLink(page, form);
      fx.twoLink = two.link;
      assert(two.says.includes(PROJECT) && two.says.includes(OTHER), `the new link reads ${JSON.stringify(two.says)}`);
      await page.waitForSelector(`[data-share-row] [data-share-name]:text-is("${fx.twoName}")`, { timeout: 10000 });
      const want = [`${PROJECT} Read only`, `${OTHER} Read only`].sort().join(",");
      const row = (await sharingRows(page)).find((r) => r.name === fx.twoName);
      assert(row && row.projects.slice().sort().join(",") === want, `the link lists ${JSON.stringify(row)}`);
      fx.twoId = row.id;
      for (const name of [PROJECT, OTHER]) {
        const res = await fetchRaw(`${fx.twoLink}/projects/${name}/editor`);
        assert(res.status === 200, `${name} under the new link answered ${res.status}`);
      }
      return `${offered.length} projects offered`;
    });

    await run("a link's Edit puts several projects on it from the same dropdown", async () => {
      const form = await editLink(page, fx.twoId);
      const before = await readPicker(form);
      assert(!before.offers.includes(PROJECT) && !before.offers.includes(OTHER) && !before.sends.length && before.says === "Pick projects or assistants", `the Add reads ${JSON.stringify(before)}`);
      await form.locator(`${PROJECTS} [data-picker-toggle]`).click();
      await form.locator(`${PROJECTS} [data-picker-filter]`).fill(`${MANY}1`);
      const hits = await listed(form);
      assert(hits.length === 10 && hits.every((name) => name.startsWith(`${MANY}1`)), `the search lists ${hits}`);
      for (const name of [`${MANY}11`, `${MANY}12`]) await form.locator(`${PROJECTS} [data-picker-list] [data-project-name="${name}"]`).click();
      assert(await menuShows(form), "picking a row closed the menu");
      const picker = await readPicker(form);
      assert(picker.sends.slice().sort().join(",") === `${MANY}11,${MANY}12` && picker.count === "2", `the Add reads ${JSON.stringify(picker)}`);
      await page.keyboard.press("Escape");
      await saveEdit(page, form);
      await page.waitForSelector(`[data-share-row="${fx.twoId}"] [data-share-project="${MANY}12"]`, { timeout: 10000 });
      const row = (await sharingRows(page)).find((r) => r.id === fx.twoId);
      const want = [PROJECT, OTHER, `${MANY}11`, `${MANY}12`].map((name) => `${name} Read only`).sort().join(",");
      assert(row.projects.slice().sort().join(",") === want, `the link lists ${JSON.stringify(row)}`);
      assert((await fetchRaw(`${fx.twoLink}/projects/${MANY}11/editor`)).status === 200, `${MANY}11 does not open under the link`);
    });

    await run("with many projects the dialogs fit a phone and the dropdown scrolls", async () => {
      const mp = await mobilePage();
      await mp.goto(`${BASE}/settings/sharing?project=${PROJECT}`, { waitUntil: "domcontentloaded" });
      await dismissUpdate(mp);
      await mp.waitForSelector(`${DIALOG} [data-share-new] ${PROJECTS}:defined`, { timeout: 10000 });
      const form = mp.locator(`${DIALOG} [data-share-new]`);
      const fits = (selector) => mp.evaluate((sel) => {
        const width = window.innerWidth;
        const dialog = document.querySelector("[data-form-modal].show .modal-content");
        const controls = [...dialog.querySelectorAll(sel)];
        return {
          scrolls: document.documentElement.scrollWidth > width + 1,
          dialog: Math.round(dialog.getBoundingClientRect().height),
          screen: window.innerHeight,
          controls: controls.length,
          outside: controls.filter((el) => { const box = el.getBoundingClientRect(); return !box.width || box.left < 0 || box.right > width + 1; }).map((el) => el.name || el.textContent.trim()),
        };
      }, selector);
      const fit = await fits('[data-picker-toggle], button[type="submit"], [name="name"], [name="amount"], [name="unit"]');
      assert(!fit.scrolls && fit.dialog <= fit.screen && fit.controls === 5 && !fit.outside.length, `the phone's New link reads ${JSON.stringify(fit)}`);
      const picker = await readPicker(form);
      assert(picker.sends.join(",") === PROJECT && picker.offers.length >= 43, `the phone's picker reads ${JSON.stringify({ ...picker, offers: picker.offers.length })}`);
      await form.locator(`${PROJECTS} [data-picker-toggle]`).tap();
      assert(await menuShows(form), "the phone's menu did not open");
      const menu = await form.locator(`${PROJECTS} [data-picker-list]`).evaluate((list) => {
        const box = list.closest(".dropdown-menu").getBoundingClientRect();
        return { scrolls: list.scrollHeight > list.clientHeight, left: box.left, right: box.right, width: window.innerWidth, focused: document.activeElement?.matches("[data-picker-filter]") };
      });
      assert(menu.scrolls && menu.left >= 0 && menu.right <= menu.width + 1 && !menu.focused, `the phone's menu reads ${JSON.stringify(menu)}`);
      await form.locator(`${PROJECTS} [data-picker-filter]`).tap();
      await form.locator(`${PROJECTS} [data-picker-filter]`).fill(`${MANY}2`);
      await form.locator(`${PROJECTS} [data-picker-list] [data-project-name="${MANY}25"]`).tap();
      const picked = await readPicker(form);
      assert(picked.sends.slice().sort().join(",") === [PROJECT, `${MANY}25`].sort().join(",") && picked.count === "2" && (await listed(form)).includes(`${MANY}25 picked`), `the phone's pick reads ${JSON.stringify({ ...picked, offers: picked.offers.length })}`);
      await mp.keyboard.press("Escape");
      assert(!(await menuShows(form)), "Escape left the phone's menu open");
      await mp.tap(`${DIALOG} .btn-close`);
      await mp.waitForSelector(DIALOG, { state: "detached", timeout: 10000 });
      await mp.tap(`[data-share-row="${fx.twoId}"] [data-share-edit]`);
      await mp.waitForSelector(`${DIALOG} [data-share-edit-form]`, { timeout: 10000 });
      const edit = await fits('[data-picker-toggle], [data-share-save], [data-share-revoke], [name="amount"], label[for^="share-remove-"]');
      assert(!edit.scrolls && edit.controls === 8 && !edit.outside.length, `the phone's Edit reads ${JSON.stringify(edit)}`);
      await mp.tap(`${DIALOG} .btn-close`);
      return `New link ${fit.dialog}px of ${fit.screen}px, ${picker.offers.length} projects in the dropdown`;
    });

    await run("the projects made for the dropdown go again and leave the link", async () => {
      const removed = await shellAsk(page, fx.shell, `(${each(fx.parent, "rmdir")}) && echo removed`);
      assert(removed === "removed", `removing the projects answered ${removed}`);
      fx.many = false;
      const row = (await sharingRows(page)).find((r) => r.id === fx.twoId);
      const want = [`${PROJECT} Read only`, `${OTHER} Read only`].sort().join(",");
      assert(row && row.projects.slice().sort().join(",") === want, `the link lists ${JSON.stringify(row)}`);
    });

    await run("a guest's empty comments sheet says so and asks for nothing, the owner's asks", async () => {
      const treeLoaded = "[data-editor-tree] .editor-empty, [data-editor-tree] [data-path]";
      const { ctx, page: gp } = await guestPage(browser, bag, fx, ORIGIN);
      try {
        await gp.goto(`${fx.twoLink}/projects/${OTHER}/editor`, { waitUntil: "domcontentloaded" });
        await gp.waitForSelector(treeLoaded, { timeout: 15000 });
        await openCommentsSheet(gp);
        const guestSays = await waitText(() => gp.textContent("[data-editor-sheet-body]"), "No comments yet.", "the guest's comments sheet");
        assert(!guestSays.includes("Click a line number") && !guestSays.includes("Delete all comments"), `the guest's sheet reads ${JSON.stringify(guestSays)}`);
      } finally {
        await ctx.close();
      }
      await page.goto(`${BASE}/projects/${OTHER}/editor`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(treeLoaded, { timeout: 15000 });
      await openCommentsSheet(page);
      await waitText(() => page.textContent("[data-editor-sheet-body]"), "No comments yet. Click a line number to add one.", "the owner's comments sheet");
      await page.click("[data-editor-sheet-close]");
    });

    await run("a project removed outside the cockpit takes its link with it", async () => {
      await L.createProject(page, REMOVED);
      const link = await linkOver(page, REMOVED, "2", "hours", `removed-${TAG}`);
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

    await run("a link keeps its last project when the other is deleted and when a save would take it off", async () => {
      const nothing = await fetchRaw(`${BASE}/no/such/page`);
      await L.createProject(page, GONE);
      fx.goneLink = await linkOver(page, GONE, "2", "hours", `gone-${TAG}`);
      const pair = await linkOver(page, GONE, "2", "hours", `pair-${TAG}`);
      const pairId = await linkIdNamed(page, `pair-${TAG}`);
      const added = await postAs(page, `/settings/sharing/${pairId}`, { form: "save", project: OTHER });
      assert(added.ok && added.text.includes("Link saved."), `adding ${OTHER} answered ${added.status}`);
      await L.deleteProject(page, GONE);
      const kept = await fetchRaw(pair);
      assert(kept.status === 303 && kept.headers.location === `${new URL(pair).pathname}/projects/${OTHER}/editor`, `the pair answered ${kept.status} ${kept.headers.location}`);
      const off = await fetchRaw(`${pair}/projects/${GONE}/editor`);
      assert(off.status === 404 && off.body === nothing.body, `the deleted project under the pair answered ${off.status}`);
      const row = (await sharingRows(page)).find((r) => r.id === pairId);
      assert(row && row.projects.join(",") === `${OTHER} Read only`, `the pair lists ${JSON.stringify(row)}`);
      const form = await editLink(page, pairId);
      await form.locator(`[data-share-project="${OTHER}"] label[for^="share-remove-"]`).click();
      const struck = await form.locator(`[data-share-project="${OTHER}"] [data-share-resource-name]`).evaluate((el) => getComputedStyle(el).textDecorationLine);
      assert(struck.includes("line-through"), `the cross leaves the project ${struck}`);
      await form.locator("[data-share-save]").click();
      const refused = await waitText(() => page.textContent(`${DIALOG} [data-form-modal-error]`), "Revoke it to end it", "the refused save");
      await page.click(`${DIALOG} .btn-close`);
      await page.waitForSelector(DIALOG, { state: "detached", timeout: 10000 });
      assert((await fetchRaw(pair)).status === 303 && (await sharingRows(page)).some((r) => r.id === pairId), "a refused save ended the link");
      await postAs(page, `/settings/sharing/${pairId}`, { form: "revoke" });
      const ended = await fetchRaw(pair);
      assert(ended.status === 404 && ended.body === nothing.body, `the revoked pair answered ${ended.status}`);
      return `kept ${kept.status}, ${refused.trim()}`;
    });

    await run("dead links answer the 404 of an address that does not exist", async () => {
      const nothing = await fetchRaw(`${BASE}/no/such/page`);
      const unknown = await fetchRaw(`${BASE}/share/${"A".repeat(43)}`);
      await editLink(page, fx.teamId);
      await page.click(`${DIALOG} [data-share-revoke]`);
      await page.waitForSelector(".swal2-title", { state: "visible", timeout: 8000 });
      const asked = await page.$eval(".swal2-title", (el) => ({ text: el.textContent, markup: el.children.length }));
      assert(asked.text === `Revoke the link "${fx.teamName}"? It stops working at once.` && !asked.markup, `the revoke dialog reads ${JSON.stringify(asked)}`);
      await L.confirmSwal(page);
      await page.waitForSelector(`[data-share-row="${fx.teamId}"]`, { state: "detached", timeout: 10000 });
      await waitText(() => page.$$eval(".dc-notice", (els) => els.map((el) => el.textContent).join(" ")), "Link revoked.", "the revoke's notice");
      assert(!(await page.$(DIALOG)), "the revoke left the dialog standing");
      const wait = 62000 - (Date.now() - fx.expiringAt);
      if (wait > 0) await sleep(wait);
      const answers = {};
      for (const [name, url] of Object.entries({ unknown: unknown, revoked: fx.team, deleted: fx.goneLink, expired: fx.expiring })) {
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
    if (fx.many) await shellAsk(page, fx.shell, `(${each(fx.parent, "rmdir")}); echo swept`).catch(() => {});
    await L.deleteProject(page, PROJECT).catch(() => {});
    await L.deleteProject(page, SECOND).catch(() => {});
    await L.deleteProject(page, OTHER).catch(() => {});
    await L.deleteProject(page, GONE).catch(() => {});
    await L.deleteProject(page, REMOVED).catch(() => {});
  }
});
