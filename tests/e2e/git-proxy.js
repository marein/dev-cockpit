const { spawn } = require("child_process");
const fs = require("fs");
const path = require("path");
const L = require("./lib");
const { assert, BASE, sleep } = L;

// The git proxy: `dev-cockpit git <args...>` hands a whole git command line to
// the running cockpit, which runs it in the caller's own working directory
// with the askpass bridge attached (POST /git with {cwd, args},
// handlers_projectgit.go). A coder in a terminal cannot answer an ssh
// passphrase, so this is the path that lets one push a passphrased key at all.
// The command carries no project and no projects root: the directory is the
// whole of what it sends, and the server scopes by it.
//
// What this runner proves, and what nothing else covers:
//   - the arguments travel unchanged and output plus exit code come back as
//     git's own, a failing git included (that is a 200 carrying git's code,
//     never an HTTP failure)
//   - the one exception to that: git's own options, everything in front of a
//     subcommand, are refused rather than proxied
//   - a question raised by a call NO page started reaches the browser anyway:
//     the runner sits on /projects, never opens an editor, and the app-wide
//     dialog (@dc/gitprompt) comes up there
//   - a directory outside every project runs like any other, this is a proxy
//     and no project surface
//   - the same question is a notification, one entry per scope, so it
//     reaches somebody with no page open at all, and the entry reads itself
//     the moment the dialog stands in front of somebody
//   - answering delivers the answer to the waiting helper
//   - cancelling fails the operation honestly: the CLI exits non-zero and its
//     stderr carries git's words plus the cockpit's own sentence about the
//     cancel (cancelNote), instead of hanging or claiming success
//
// Two gotchas this runner is built around:
//   - `lib.dismissUpdate` clicks whatever `.swal2-cancel` is visible, and the
//     git question's dialog carries one, so it has to run BEFORE a question
//     stands or it cancels the very question a check is waiting for.
//   - the notification entry cannot be read from an open page: the dialog
//     marks it read the moment it stands in front of somebody, which is the
//     documented behavior. The entry is therefore read through the browser
//     context's request API (no page, no cockpit JS), which is also the
//     honest shape of the case, a coder pushing with the app closed.
//
// It needs an instance of its own plus the paths below, see the README: the
// runner starts the real CLI as a child process, so the binary, the state
// directory holding the local API socket, and the projects directory all have
// to be reachable under /aux inside the container. The container path and the
// host path of the projects tree differ, which is why the checks that read a
// directory back out of the UI compare the end of it and not the whole.
const AUX = process.env.AUX_DIR || "/aux";
const BIN = path.join(AUX, "bin", "dev-cockpit");
const STATE = path.join(AUX, "state");
const PROJECTS = path.join(AUX, "projects");
const PROJECT = process.env.GIT_PROXY_PROJECT || "gitproxy";
const REPO = path.join(PROJECTS, PROJECT);
// The ssh stand-in writes whatever it was told down here, which is how the
// runner sees that the typed answer really reached the helper.
const ANSWER_FILE = path.join(REPO, "zz-ask-answer.txt");

// gitProxy runs one proxied command line and resolves with what a shell would
// see: both streams and the exit status.
function gitProxy(args, { cwd = REPO } = {}) {
  return new Promise((resolve) => {
    const child = spawn(BIN, ["git", "--state-dir", STATE, ...args], {
      cwd,
      env: { ...process.env, HOME: path.join(AUX, "home") },
    });
    let stdout = "", stderr = "";
    child.stdout.on("data", (d) => { stdout += d; });
    child.stderr.on("data", (d) => { stderr += d; });
    child.on("close", (code) => resolve({ code, stdout, stderr }));
  });
}

// startPush kicks off the push that will park a question and hand back a
// promise for its result, because nothing about it finishes until somebody
// answers in the browser.
function startPush() {
  try { fs.unlinkSync(ANSWER_FILE); } catch { /* the first run has none */ }
  return gitProxy(["push"]);
}

const dialogUp = (page) => page.waitForSelector(".swal2-popup:not(.swal2-toast) input.swal2-input", { timeout: 30000 });

// unreadFor reads the notification list through the context's request API and
// deliberately not through a page: an open cockpit page shows the dialog, and
// the dialog marks the entry read the moment it stands in front of somebody,
// so reading this from a page would measure the reading, not the entry. Only
// the unread ones, a read entry is history and stays in the list on purpose.
async function entriesFor(ctx, target) {
  const res = await ctx.request.get(`${BASE}/notifications`, { headers: { Accept: "application/json" } });
  const data = await res.json();
  return (data.notifications || []).filter((n) => n.targetId === target);
}

async function unreadFor(ctx, target) {
  return (await entriesFor(ctx, target)).filter((n) => !n.read);
}

// waitForNewEntry waits for an entry this check's own question raised, told
// apart from the ones before it by id. Two things make that necessary: a read
// entry stays in the list as history, so the target is rarely empty, and the
// server publishes the gitprompt event before it writes the notification, so
// the dialog is up a moment before the entry exists. Waiting for "any entry"
// would return one of the old read ones immediately and read as a question
// somebody had already seen.
async function waitForNewEntry(ctx, target, before) {
  const known = new Set(before.map((n) => n.id));
  for (let i = 0; i < 60; i += 1) {
    const fresh = (await entriesFor(ctx, target)).filter((n) => !known.has(n.id));
    if (fresh.length) return fresh;
    await sleep(250);
  }
  throw new Error(`no new notification entry for ${target} ever appeared`);
}

// nothingUnread waits until the project's question entry is read again, which
// is what the dialog standing in front of somebody and the question ending
// both have to cause.
async function nothingUnread(ctx, target) {
  for (let i = 0; i < 60; i += 1) {
    if ((await unreadFor(ctx, target)).length === 0) return;
    await sleep(250);
  }
  throw new Error(`the question entry stayed unread in ${target}`);
}

L.runFeature("GIT PROXY", async ({ page, ctx, run }) => {
  const target = `gitprompt:${PROJECT}`;

  await run("the arguments travel unchanged and git's own answer comes back", async () => {
    const branch = await gitProxy(["rev-parse", "--abbrev-ref", "HEAD"]);
    assert(branch.code === 0, `a working call must exit 0, got ${branch.code}: ${branch.stderr}`);
    assert(branch.stdout.trim().length > 0, "the branch name never reached stdout");

    // The one -c every other call of internal/git injects is core.quotepath,
    // and an injected -c is visible to `config --get`. An unset key answering
    // empty is the proof that the proxy adds nothing to the line.
    const injected = await gitProxy(["config", "--get", "core.quotepath"]);
    assert(injected.code !== 0 && injected.stdout.trim() === "",
      `the proxy injected configuration: ${injected.code} ${injected.stdout}`);

    // A git that refused something is git's answer, not a failure of the
    // command: its own exit code and its own words.
    const bad = await gitProxy(["rev-parse", "--verify", "zz-no-such-ref"]);
    assert(bad.code !== 0, "a failing git must fail the command");
    assert(/fatal|Needed a single revision/i.test(bad.stderr), `git's words did not come back: ${bad.stderr}`);
    return `branch read, nothing injected, failing git exited ${bad.code}`;
  });

  // The one thing read out of the line before it travels: git's own options,
  // everything that would stand in front of a subcommand, point git at another
  // program or another repository than the one the dialog names, and the first
  // word is what the dialog shows as the server's own truth.
  await run("git's own options are refused instead of proxied", async () => {
    const injected = await gitProxy(["-c", "core.sshCommand=/bin/false", "push"]);
    assert(injected.code !== 0, "a line pointing git at another program must fail");
    assert(/subcommand/i.test(injected.stderr), `the refusal has to say what is wrong: ${injected.stderr}`);

    const moved = await gitProxy(["-C", "/tmp", "status"]);
    assert(moved.code !== 0, "a line moving the call out of the working copy must fail");

    const transport = await gitProxy(["fetch", "--upload-pack=/bin/false"]);
    assert(transport.code !== 0, "a line naming the transport's program must fail");
    assert(/upload-pack/.test(transport.stderr), `the refusal has to name it: ${transport.stderr}`);
    return "config, directory and transport program each refused";
  });

  // A proxy runs where it is called, and that is the whole of it: a checkout
  // outside the projects root is an ordinary thing to have. It used to be
  // refused, which only sent somebody back to the plain git that cannot ask
  // for the passphrase.
  await run("a directory outside every project runs like any other", async () => {
    const outside = path.join(AUX, "outside-repo");
    fs.mkdirSync(outside, { recursive: true });
    await new Promise((done) => {
      const init = spawn("git", ["init", "-q"], { cwd: outside });
      init.on("close", done);
    });
    const ran = await gitProxy(["rev-parse", "--is-inside-work-tree"], { cwd: outside });
    assert(ran.code === 0, `a repository outside every project must run: ${ran.code} ${ran.stderr}`);
    assert(ran.stdout.trim() === "true", `git did not run where it was called: ${ran.stdout}`);
    return "ran in a repository outside the projects root";
  });

  // The heart of it, in the order the two halves really happen: a coder pushes
  // with nobody looking, the question becomes news that reaches a closed app,
  // and then any page somebody opens shows the dialog and answers it. The
  // runner never opens an editor anywhere in this check.
  await run("a question from a terminal reaches a closed app, then any page", async () => {
    // The update dialog is taken down first, while no question stands.
    // lib's dismissUpdate clicks whatever .swal2-cancel is visible, and the
    // git question's dialog has one, so running it with a question standing
    // would cancel the question this check is about.
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    // No cockpit page at all: nothing here can show a dialog or read an entry.
    await page.goto("about:blank");
    const before = await entriesFor(ctx, target);
    const push = startPush();

    // Whatever the checks below find, the question gets answered: a check that
    // threw with a question still parked would leave the push hanging and its
    // bridge open, and every later check would read "already running" instead
    // of its own subject.
    let seen = [];
    try {
      seen = await waitForNewEntry(ctx, target, before);
      assert(seen.length === 1, `want one entry with no page open, got ${seen.length}`);
      assert(!seen[0].read, "a question nobody has seen must be unread");
      assert(/Git asks a question/.test(seen[0].title), `unexpected title: ${seen[0].title}`);
      assert(new RegExp(`"push".*${PROJECT}`).test(seen[0].detail || ""), `unexpected detail: ${seen[0].detail}`);

      // Now somebody opens the app, on the page furthest from the action.
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await dialogUp(page);
      const question = await page.textContent(".swal2-html-container");
      assert(/Enter passphrase for key/.test(question), `the dialog does not carry ssh's line: ${question}`);
      // The working copy and the command line stand in the block below, so
      // the dialog does not say them a second time above ssh's own words.
      // The editor's question has no such block and keeps its line, which is
      // what tests/e2e/editor-git.js holds on to.
      assert(!question.includes(`${PROJECT} · push`), `the dialog repeats project and action: ${question}`);
      assert(await page.getAttribute(".swal2-popup input.swal2-input", "type") === "password",
        "a passphrase field is not masked");

      // Whoever answers this is answering for a caller they cannot see, so
      // the dialog shows the whole picture, as the plain monospace block the
      // compose run output uses: which working copy, and what runs in it.
      // The directory is half of it, the same push means different things in
      // two checkouts of one repository.
      const command = page.locator("[data-gitprompt-command]");
      assert(await command.count() === 1, "the dialog does not show the command");
      const block = (await command.textContent()).trim();
      // The directory the dialog shows is the server's own, and the server
      // runs on the host while this runner sees the same tree mounted at
      // /aux, so the two spellings differ by their prefix. What is checked is
      // the part that is the same on both sides, the project directory the
      // command really runs in.
      assert(new RegExp(`^cwd: \\S*/${PROJECT}$`, "m").test(block),
        `the command block does not name the working copy: ${block}`);
      assert(/\$ git push\b/.test(block), `the command block does not read like the call: ${block}`);
      const mono = await command.evaluate((el) => getComputedStyle(el).fontFamily);
      assert(/mono/i.test(mono), `the command block is not monospace: ${mono}`);
      // A long path plus a long command line is wider than a phone, and a
      // sideways scrollbar inside a dialog hides the half nobody scrolls to.
      // The block wraps instead, so the whole of it is readable on every
      // width, and the height alone is what a very long one scrolls.
      const wrap = await command.evaluate((el) => {
        const style = getComputedStyle(el);
        return { white: style.whiteSpace, wide: el.scrollWidth > el.clientWidth + 1 };
      });
      assert(wrap.white === "pre-wrap", `the command block does not wrap: ${wrap.white}`);
      assert(!wrap.wide, "the command block scrolls sideways instead of wrapping");

      // The dialog standing in front of somebody is what reads the entry, so
      // the bell never claims a question that is being answered right now.
      await nothingUnread(ctx, target);
    } finally {
      await page.fill(".swal2-popup input.swal2-input", "opensesame").catch(() => {});
      await page.click(".swal2-confirm").catch(() => {});
    }

    // The typed answer goes back to the helper, which writes down what it got.
    const result = await push;
    assert(fs.readFileSync(ANSWER_FILE, "utf8") === "opensesame", "the answer never reached the helper");
    // The stand-in refuses whatever it gets, so the push still ends in git's
    // words; what this check is about is that the answer travelled.
    assert(result.code !== 0, "the stand-in refuses every key, so the push cannot succeed");
    return "news with nothing open, dialog on a page that started nothing, answer delivered";
  });

  // A window can be visible without anybody looking at it: the browser on a
  // second monitor, or sitting behind another app. Reading the entry there
  // would also stop the push (internal/push re-checks that the target is
  // still unread two seconds later), and the git operation would wait for an
  // answer nobody was ever told about. The rule is the terminal
  // notifications' own, visible AND focused (`shownTargets`).
  //
  // Headless has no second monitor, so the focus is stubbed: what is under
  // test is that the code asks document.hasFocus() at all and acts on it,
  // which is exactly the regression. Visibility stays real.
  await run("a visible but unfocused page neither reads the question nor stops the push", async () => {
    // The other page is still on /projects from the check before, and it
    // would show the same question and read it, being focused. Off the app.
    await page.goto("about:blank");
    const away = await ctx.newPage();
    await away.addInitScript(() => {
      window.__dcFocused = false;
      document.hasFocus = () => window.__dcFocused;
    });
    // The update dialog is taken down before a question stands, see above.
    await away.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(away);

    // The entries standing before this check are history from the ones above,
    // and they are all read; the new one is told apart from them by id.
    const before = await entriesFor(ctx, target);
    const push = startPush();
    try {
      // The dialog still comes up: an unfocused page shows the question, it
      // just does not claim that anybody has seen it.
      await dialogUp(away);
      const fresh = await waitForNewEntry(ctx, target, before);
      assert(fresh.length === 1 && !fresh[0].read,
        `an unfocused page must leave the question unread, got read=${fresh.map((e) => e.read)}`);
      // And it stays that way rather than being read a moment later, which is
      // what would keep the push from going out two seconds in.
      await sleep(2500);
      const still = await unreadFor(ctx, target);
      assert(still.length === 1, "the question was read while nobody was looking at the page");

      // Coming back is what turns it into a seen question, and on the desktop
      // that signal is the window focus alone, the page was visible all along.
      await away.evaluate(() => {
        window.__dcFocused = true;
        window.dispatchEvent(new Event("focus"));
      });
      await nothingUnread(ctx, target);
    } finally {
      await away.fill(".swal2-popup input.swal2-input", "opensesame").catch(() => {});
      await away.click(".swal2-confirm").catch(() => {});
      await away.close().catch(() => {});
    }
    await push;
    return "unread while away, read on focus";
  });

  await run("a cancelled question fails the operation with a visible reason", async () => {
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    const push = startPush();

    try {
      await dialogUp(page);
    } finally {
      await page.click(".swal2-cancel").catch(() => {});
    }

    const result = await push;
    assert(result.code !== 0, `a cancelled question has to fail the command, got exit ${result.code}`);
    assert(/the question was cancelled/.test(result.stderr),
      `the caller cannot see why it failed: ${JSON.stringify(result.stderr)}`);
    assert(fs.readFileSync(ANSWER_FILE, "utf8") === "denied", "the cancel never reached the helper");

    // The question is gone, so the entry must not stand unread behind it.
    await nothingUnread(ctx, target);
    return `exit ${result.code}, reason named, helper denied`;
  });

  await run("the cockpit's own skill is listed and cannot be edited or deleted", async () => {
    await page.goto(`${BASE}/settings/coders/copilot/skills`, { waitUntil: "domcontentloaded" });
    const row = page.locator(".list-group-item", { hasText: "dev-cockpit-git" });
    assert(await row.count() === 1, "the managed skill is not listed");
    const badge = row.locator(".badge");
    assert(await badge.count() === 1, "the row does not say who manages it");
    assert((await badge.textContent()).trim() === "Managed", `unexpected badge text: ${await badge.textContent()}`);
    assert(await row.locator('a[href*="/edit"]').count() === 0, "the managed skill offers an edit link");
    assert(await row.locator('button[aria-label*="Delete"]').count() === 0, "the managed skill offers a delete button");

    // A hand typed URL is refused the same way, the badge is not the guard.
    await page.goto(`${BASE}/settings/coders/copilot/skills/dev-cockpit-git/edit`, { waitUntil: "domcontentloaded" });
    assert(/\/skills$/.test(page.url()), `the edit page opened anyway: ${page.url()}`);
    const flash = await page.textContent("body");
    assert(/managed by the cockpit/i.test(flash), "the refusal does not say why");
    return "listed locked, edit URL refused";
  });

  await sleep(200);
});
