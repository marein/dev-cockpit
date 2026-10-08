const L = require("./lib");
const { assert, sleep, BASE } = L;

// File links: a path of an existing file of the terminal's project, relative
// to its root or absolute inside it, alone or with :line or :line:col, links
// into the project's editor (GET /projects/:name/editor?file=&line=&col=, the
// cursor lands there). The server decides what is a file
// (filesystem.FileRefs). The live terminal marks the paths in xterm's own
// buffer (@dc/termlinks): after every write it reads the screen, asks
// POST /shells/:id/file-links once for each word it never asked about and
// gives the cells of a file the link a program's OSC 8 link gets, a file://
// address with the location as #line:col, which opens over GET
// /terminal-link. The stream itself carries no link. A path the terminal
// wrapped (a row filled to the last column, the next starting in the first)
// is one link, one a program wrapped (a row filled to the margin of its
// block, the next starting at an indent where a word of it starts) links on
// both rows when the joined text names a file and neither row's word names
// one alone. The copy view's text
// arrives with its links in the GET /shells/:id/copy answer, a conversation
// with them in its markup. A program's own link stays its own. A mark goes
// with its text: what a program left of a path after overwriting part of it
// in place links nothing. A new word on the row the cursor stands on is
// asked about once the cursor left it, so typing asks nothing, one on a row
// whose text a program keeps replacing once the row stood still for 1.5s. A
// word without a letter, a time, a version or a size, is never asked about.
// Inside the editor's terminal panel a link opens in place, also for a project
// named like p(1), which the server escapes otherwise than the page.
// Every check prints its own lines behind a fresh mark (m<n>m) and waits for
// them, the shell gets its commands through the input route, so a line
// arrives in one piece. The files are not Go, so opening the editor starts no
// language server, whose container start drops the browser's connections.
// Gotchas: headless renders xterm on canvas, so a cell is found through the
// .attach-selection text layer, which follows the cell grid. On a phone the
// reconnect toast covers the top rows, so the phone prints below a seq 12.

L.runFeature("FILE-LINKS", async ({ engine, page, run, mobilePage }) => {
  const stamp = `${engine}-${Date.now().toString(36)}`;
  const project = `zzfl-${stamp}`;
  const escaped = `zzfl(1)-${stamp}`;
  const FILE = "src/app.py";
  const LINKED = `${FILE}:3:7`;
  let root = "";
  let shellUrl = "";
  let shellId = "";
  let escapedShellUrl = "";
  let coderUrl = "";
  let marks = 0;
  // A fresh mark, and how a command writes it so its echo never reads as it.
  const mark = () => {
    marks += 1;
    return { m: `m${marks}m`, sh: `m''${marks}m` };
  };

  const input = (p, url, item) => p.evaluate(async ([to, it]) => {
    const res = await fetch(to, {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]').content },
      body: JSON.stringify({ items: [it] }),
    });
    if (!res.ok) throw new Error(`input answered ${res.status}`);
  }, [`${new URL(url).pathname}/input`, item]);
  const shell = (p, command, url = shellUrl) => input(p, url, { prompt: command });

  const mirror = (p, scope) => p.evaluate((s) => document.querySelector(`${s} .attach-selection`)?.textContent || "", scope);
  const waitLine = async (p, start, scope = "#terminal") => {
    for (let i = 0; i < 80; i += 1) {
      if ((await mirror(p, scope)).split("\n").some((line) => line.startsWith(start))) return;
      await sleep(250);
    }
    throw new Error(`the terminal never showed ${JSON.stringify(start)}: ${JSON.stringify((await mirror(p, scope)).slice(-400))}`);
  };
  // The middle of the second cell of needle, on the row that starts with the
  // mark or as many rows below it.
  const cellOf = async (p, m, needle, { row = 0, scope = "#terminal" } = {}) => {
    const at = await p.evaluate(([s, mk, n, down]) => {
      const node = document.querySelector(`${s} .attach-selection`)?.firstChild;
      const lines = node ? node.data.split("\n") : [];
      const first = lines.findIndex((line) => line.startsWith(mk));
      const line = first < 0 ? undefined : lines[first + down];
      const index = line === undefined ? -1 : line.indexOf(n);
      if (index < 0) return null;
      const offset = lines.slice(0, first + down).reduce((sum, l) => sum + l.length + 1, 0) + index;
      const range = document.createRange();
      range.setStart(node, offset + 1);
      range.setEnd(node, offset + 2);
      const box = range.getBoundingClientRect();
      return { x: box.left + box.width / 2, y: box.top + box.height / 2 };
    }, [scope, m, needle, row]);
    assert(at, `${JSON.stringify(needle)} is not on the row of ${m}: ${JSON.stringify((await mirror(p, scope)).slice(-400))}`);
    return at;
  };
  // Whether xterm shows a link under the point: it gives its screen the
  // pointer class while one is hovered. The pointer leaves first, xterm asks
  // nothing for the cell it saw last.
  const linkShown = async (p, at, scope = "#terminal", ms = 2000) => {
    const box = await p.locator(`${scope} .xterm-screen`).boundingBox();
    await p.mouse.move(box.x + 2, box.y + box.height - 2);
    await p.mouse.move(at.x, at.y);
    return p.waitForFunction((s) => document.querySelector(`${s} .xterm-screen`).classList.contains("xterm-cursor-pointer"), scope, { timeout: ms, polling: 25 })
      .then(() => true, () => false);
  };
  const cursorOf = (p) => p.evaluate(async () => {
    const { EditorView } = await import("@codemirror/view");
    const dom = [...document.querySelectorAll(".cm-editor")].find((el) => el.getClientRects().length && getComputedStyle(el).visibility !== "hidden");
    const view = dom && EditorView.findFromDOM(dom);
    if (!view) return null;
    const head = view.state.selection.main.head;
    const line = view.state.doc.lineAt(head);
    return { path: document.querySelector(".editor-tab.active")?.dataset.path || "", line: line.number, col: head - line.from + 1 };
  });
  const waitCursor = async (p, want) => {
    let got = null;
    for (let i = 0; i < 60; i += 1) {
      got = await cursorOf(p).catch(() => null);
      if (got && got.path === want.path && (!want.line || (got.line === want.line && got.col === want.col))) return got;
      await sleep(250);
    }
    throw new Error(`the editor stands at ${JSON.stringify(got)}, want ${JSON.stringify(want)}`);
  };
  const opens = async (p, act, want) => {
    await Promise.all([p.waitForURL(/\/editor\?/, { timeout: 15000 }), act()]);
    await waitCursor(p, want);
  };
  // Nothing opened within ms: no editor page was asked for, which shows an
  // open even before it lands, and the page is still the shell's.
  const stays = async (p, what, ms = 1500) => {
    const asked = await p.waitForRequest((req) => req.url().includes("/editor?"), { timeout: ms }).then((req) => req.url(), () => "");
    assert(!asked && new URL(p.url()).pathname === new URL(shellUrl).pathname, `${what} opened ${asked || p.url()}`);
  };
  // What window.open was asked for, kept in sessionStorage so it outlives a
  // page a link opens.
  const recordOpens = (p) => p.evaluate(() => {
    sessionStorage.setItem("e2e-opened", "[]");
    window.open = (url) => {
      sessionStorage.setItem("e2e-opened", JSON.stringify([...JSON.parse(sessionStorage.getItem("e2e-opened") || "[]"), url]));
      return null;
    };
  });
  const opened = (p) => p.evaluate(() => JSON.parse(sessionStorage.getItem("e2e-opened") || "[]"));
  const freshShellPage = async (p) => {
    await p.goto(shellUrl, { waitUntil: "domcontentloaded" });
    await p.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
    await sleep(600);
  };
  const editorHref = (query) => `/projects/${project}/editor?${query}`;

  await run("setup: a project with three files, a symlink out, one into a sibling project named like p(1), a look-alike sibling", async () => {
    await L.createProject(page, project);
    root = await L.projectPath(page, project);
    shellUrl = await L.createShell(page, project);
    shellId = new URL(shellUrl).pathname.split("/").pop();
    await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
    const { m, sh } = mark();
    await shell(page, `mkdir -p src build '~' '../${project}-wt' '../${escaped}' && printf 'one\\ntwo\\nthree  seven\\n' > ${FILE} && printf 'hi\\n' > notes.txt && printf 'no\\n' > build/notes.txt && touch '~/home.txt' '../${project}-wt/main.py' && printf 'hi\\n' > '../${escaped}/notes.txt' && ln -s /etc/hostname out.txt && ln -s '../${escaped}/notes.txt' into.txt && clear && echo ${sh} ready`);
    await waitLine(page, m);
  });

  const outside = [
    `../${project}/notes.txt`, `%2e%2e/${project}/notes.txt`, "out.txt", "into.txt", `${root}-wt/main.py`,
    `/proc/self/root${root}/notes.txt`, "src\\app.py", "~/home.txt", `file://${root}/notes.txt`, "missing.py:3", "src", "README",
  ];

  await run("copy view: existing files of the project link into the editor, nothing outside it does", async () => {
    const { m, sh } = mark();
    const lines = [`see ${LINKED} and notes.txt.`, `abs ${root}/notes.txt:1 and ./notes.txt`, "web https://example.invalid/src/app.py", ...outside];
    await shell(page, `clear; printf '%s %s\\n' ${lines.map((line) => `${sh} '${line}'`).join(" ")}`);
    await waitLine(page, `${m} README`);
    await page.locator("[data-terminal-copy]:visible").first().click();
    await page.waitForSelector("terminal-copy [data-copy-text] a[data-file-link]", { state: "visible", timeout: 10000 });
    const links = await page.evaluate(() => [...document.querySelectorAll("terminal-copy [data-copy-text] a")]
      .map((a) => ({ text: a.textContent, href: a.getAttribute("href"), file: a.hasAttribute("data-file-link") })));
    const want = [
      { text: LINKED, href: editorHref("col=7&file=src%2Fapp.py&line=3"), file: true },
      { text: "notes.txt", href: editorHref("file=notes.txt"), file: true },
      { text: `${root}/notes.txt:1`, href: editorHref("file=notes.txt&line=1"), file: true },
      { text: "./notes.txt", href: editorHref("file=notes.txt"), file: true },
      { text: "https://example.invalid/src/app.py", href: "https://example.invalid/src/app.py", file: false },
    ];
    assert(JSON.stringify(links) === JSON.stringify(want), `links ${JSON.stringify(links)}, want ${JSON.stringify(want)}`);
  });

  await run("copy view: a click opens the editor on the file, line and column", async () => {
    await opens(page, () => page.click(`terminal-copy a[data-file-link]:text-is("${LINKED}")`), { path: FILE, line: 3, col: 7 });
  });

  await run("live terminal: a printed path shows as a link and opens at its line and column, plain text and a missing file do not", async () => {
    await freshShellPage(page);
    const { m, sh } = mark();
    await shell(page, `clear; printf '%s %s\\n' ${sh} 'plain words only' ${sh} 'gone missing.py:3' ${sh} 'see ${LINKED} now'`);
    await waitLine(page, `${m} see`);
    assert(!(await linkShown(page, await cellOf(page, m, "words"), "#terminal", 800)), "plain text shows a link");
    const missing = await cellOf(page, `${m} gone`, "missing.py");
    assert(!(await linkShown(page, missing, "#terminal", 800)), "a missing file shows a link");
    await page.mouse.click(missing.x, missing.y);
    await stays(page, "a click on a missing file");
    const linked = await cellOf(page, `${m} see`, LINKED);
    assert(await linkShown(page, linked), "the path shows no link");
    await opens(page, () => page.mouse.click(linked.x, linked.y), { path: FILE, line: 3, col: 7 });
  });

  await run("live terminal: a drag over a path selects and opens nothing, Ctrl+click opens it in a new tab", async () => {
    await freshShellPage(page);
    const { m, sh } = mark();
    await shell(page, `clear; printf '%s %s\\n' ${sh} 'see ${LINKED} now'`);
    await waitLine(page, m);
    const from = await cellOf(page, m, "see");
    const to = await cellOf(page, m, "3:7");
    await page.mouse.move(from.x, from.y);
    await page.mouse.down();
    await page.mouse.move(to.x, to.y, { steps: 8 });
    await page.mouse.up();
    await stays(page, "a drag over a path");
    const blank = await cellOf(page, m, "now");
    await page.mouse.click(blank.x + 200, blank.y);
    const popup = page.context().waitForEvent("page", { timeout: 10000 });
    const linked = await cellOf(page, m, LINKED);
    await page.keyboard.down("Control");
    await page.mouse.click(linked.x, linked.y);
    await page.keyboard.up("Control");
    const tab = await popup;
    await tab.waitForURL(/\/editor\?/, { timeout: 10000 });
    const href = new URL(tab.url()).pathname + new URL(tab.url()).search;
    await tab.close();
    assert(href === editorHref("col=7&file=src%2Fapp.py&line=3"), `the new tab is ${href}`);
    await stays(page, "Ctrl+click", 300);
  });

  // The terminal wraps the first path. A program wraps the second claude's
  // way (the row filled to the last column, a carriage return, two cells to
  // the right, one row down), the third with a line break: both name a file
  // joined. The fourth joined names none, the fifth's first part alone does.
  const printWrapped = async (p, m, sh, url) => {
    const w = m.length + 1;
    await shell(p, [
      "clear; seq 12; c=$(tput cols); printf '%s%*s%s\\n%s  Read%*s%s\\n%*s%s\\n%s%*s%s\\n%s\\n%s%*s%s\\n%s\\n%s%*s%s\\nmore\\n'",
      `${sh}s $((c-${w + 5})) '' '${LINKED}'`,
      `${sh}c $((c-${w + 11})) '' 'src/a' ${w + 2} '' 'pp.py:3:7 (+5 -5)'`,
      `${sh}t $((c-${w + 6})) '' 'notes.' 'txt'`,
      `${sh}n $((c-${w + 6})) '' 'ghost/' 'nothing.py'`,
      `${sh}p $((c-${w + 9})) '' 'notes.txt'`,
    ].join(" "), url);
    await waitLine(p, `${m}p`);
  };

  await run("live terminal: a path the terminal or a program wrapped opens from either row, a join naming no file links only its parts", async () => {
    await freshShellPage(page);
    const { m, sh } = mark();
    await printWrapped(page, m, sh);
    const parts = [[`${m}s`, "src/a", 0, FILE], [`${m}s`, "pp.py:3:7", 1, FILE], [`${m}c`, "src/a", 0, FILE], [`${m}c`, "pp.py:3:7", 1, FILE],
      [`${m}t`, "notes.", 0, "notes.txt"], [`${m}t`, "txt", 1, "notes.txt"], [`${m}p`, "notes.txt", 0, "notes.txt"]];
    for (const [row, needle, down, file] of parts) {
      await freshShellPage(page);
      await waitLine(page, `${m}p`);
      const at = await cellOf(page, row, needle, { row: down });
      await opens(page, () => page.mouse.click(at.x, at.y), file === FILE ? { path: FILE, line: 3, col: 7 } : { path: file });
    }
    await freshShellPage(page);
    await waitLine(page, `${m}p`);
    for (const [row, needle, down] of [[`${m}n`, "ghost/", 0], [`${m}n`, "nothing.py", 1], [`${m}p`, "more", 1]]) {
      const at = await cellOf(page, row, needle, { row: down });
      assert(!(await linkShown(page, at, "#terminal", 800)), `${needle} shows a link`);
      await page.mouse.click(at.x, at.y);
      await stays(page, `a click on ${needle}`, 1000);
    }
  });

  await run("copy view: a wrapped path links on every row, a join naming no file links only its parts", async () => {
    await freshShellPage(page);
    const { m, sh } = mark();
    await printWrapped(page, m, sh);
    await page.locator("[data-terminal-copy]:visible").first().click();
    await page.waitForFunction((mk) => document.querySelector("terminal-copy [data-copy-text]")?.textContent.includes(`${mk}p`), m, { timeout: 10000 });
    const links = await page.evaluate((mk) => {
      const pre = document.querySelector("terminal-copy [data-copy-text]");
      const from = pre.textContent.lastIndexOf(`${mk}s`);
      let at = 0;
      const found = [];
      for (const node of pre.childNodes) {
        if (at >= from && node.nodeName === "A") found.push({ text: node.textContent, href: node.getAttribute("href") });
        at += node.textContent.length;
      }
      return found;
    }, m);
    const app = editorHref("col=7&file=src%2Fapp.py&line=3");
    const notes = editorHref("file=notes.txt");
    const want = [
      { text: "src/a", href: app }, { text: "pp.py:3:7", href: app }, { text: "src/a", href: app }, { text: "pp.py:3:7", href: app },
      { text: "notes.", href: notes }, { text: "txt", href: notes }, { text: "notes.txt", href: notes },
    ];
    assert(JSON.stringify(links) === JSON.stringify(want), `links ${JSON.stringify(links)}, want ${JSON.stringify(want)}`);
    await page.locator("terminal-copy [data-copy-close]").first().click();
  });

  await run("live terminal: a reload keeps the links, a wrapped path's too, one wrapped right after its file name opens at its line", async () => {
    await freshShellPage(page);
    const { m, sh } = mark();
    const w = m.length + 1;
    await shell(page, `clear; c=$(tput cols); printf '%s %s\\n%s%*s%s\\n%s%*s%s\\n' ${sh} 'kept notes.txt:2' ${sh}s $((c-${w + 5})) '' '${LINKED}' ${sh}d $((c-${w + 10})) '' '${LINKED}'`);
    await waitLine(page, `${m}d`);
    await freshShellPage(page);
    await waitLine(page, `${m}d`);
    assert(await linkShown(page, await cellOf(page, `${m} kept`, "notes.txt")), "the path shows no link after the reload");
    const second = await cellOf(page, `${m}s`, "pp.py:3:7", { row: 1 });
    assert(await linkShown(page, second), "the wrapped path's second row shows no link after the reload");
    await opens(page, () => page.mouse.click(second.x, second.y), { path: FILE, line: 3, col: 7 });
    await freshShellPage(page);
    await waitLine(page, `${m}d`);
    const name = await cellOf(page, `${m}d`, FILE);
    await opens(page, () => page.mouse.click(name.x, name.y), { path: FILE, line: 3, col: 7 });
  });

  await run("live terminal: a path wrapped over four rows and a file after it keep the terminal and their links, after a reload and in the copy view too", async () => {
    await freshShellPage(page);
    const { m, sh } = mark();
    await shell(page, `clear; printf '%s\\n' ${sh}w '  -  x sr' '     c/ap' '     p.py' '     :3:7' 'notes.txt x' ${sh}e`);
    await waitLine(page, `${m}e`);
    for (const reload of [false, true]) {
      if (reload) {
        await freshShellPage(page);
        await waitLine(page, `${m}e`);
      }
      for (const [needle, down] of [["sr", 1], ["p.py", 3], ["notes.txt", 5]]) {
        assert(await linkShown(page, await cellOf(page, `${m}w`, needle, { row: down })), `${needle} shows no link${reload ? " after the reload" : ""}`);
      }
    }
    await page.locator("[data-terminal-copy]:visible").first().click();
    await page.waitForSelector('terminal-copy a[data-file-link]:text-is("p.py")', { state: "visible", timeout: 10000 });
    await page.locator("terminal-copy [data-copy-close]").first().click();
    const first = await cellOf(page, `${m}w`, "sr", { row: 1 });
    await opens(page, () => page.mouse.click(first.x, first.y), { path: FILE, line: 3, col: 7 });
  });

  // Rewrites the tail of a printed path in place, as a program that repaints
  // only the cells that changed does, which leaves the link on the others.
  const overwriteTail = (m, sh) => `printf '%s %s\\n' ${sh} ${LINKED}; printf '\\033[1A\\033[${m.length + 6}Gzz.txt\\033[K\\n%s %s\\n' ${sh}k notes.txt`;

  await run("live terminal: a path a program partly overwrote in place loses its link, a click on the rest opens nothing", async () => {
    await freshShellPage(page);
    const { m, sh } = mark();
    await shell(page, `clear; ${overwriteTail(m, sh)}`);
    await waitLine(page, `${m}k`);
    await waitLine(page, `${m} src/zz.txt`);
    const fresh = await cellOf(page, `${m}k`, "notes.txt");
    assert(await linkShown(page, fresh), "the path below shows no link");
    const rest = await cellOf(page, m, "src/");
    assert(!(await linkShown(page, rest, "#terminal", 800)), "the rest of the overwritten path kept its link");
    await page.mouse.click(rest.x, rest.y);
    await stays(page, "a click on the rest of an overwritten path");
    await opens(page, () => page.mouse.click(fresh.x, fresh.y), { path: "notes.txt" });
  });

  // Rows that end and start with words without a program wrapping anything:
  // a line above an indented count, a git status list with build/notes.txt
  // beside notes.txt, a path above a count redrawn every 100ms.
  await run("live terminal: rows that only end and start with words keep their own links, a redrawn count below a path asks nothing", async () => {
    await freshShellPage(page);
    const { m, sh } = mark();
    await shell(page, `clear; printf '%s see %s\\n   3 more warnings\\n%s status\\n\\tbuild/\\n\\tnotes.txt\\n' ${sh}w ${FILE}:2 ${sh}g`);
    await waitLine(page, `${m}g`);
    for (const [row, needle, down] of [[`${m}w`, "3", 1], [`${m}g`, "build/", 1]]) {
      assert(!(await linkShown(page, await cellOf(page, row, needle, { row: down }), "#terminal", 800)), `${needle} shows a link`);
    }
    await opens(page, async () => {
      const at = await cellOf(page, `${m}w`, `${FILE}:2`);
      await page.mouse.click(at.x, at.y);
    }, { path: FILE, line: 2, col: 1 });
    await freshShellPage(page);
    await waitLine(page, `${m}g`);
    await opens(page, async () => {
      const at = await cellOf(page, `${m}g`, "notes.txt", { row: 2 });
      await page.mouse.click(at.x, at.y);
    }, { path: "notes.txt" });
    await freshShellPage(page);
    const counted = mark();
    await shell(page, `clear; printf '%s see notes.txt\\n%s see build/\\n' ${counted.sh} ${counted.sh}d; for i in $(seq 40); do printf '\\r   %s files' $i; sleep 0.1; done; printf '\\n%s\\n' ${counted.sh}e`);
    await waitLine(page, `${counted.m}d`);
    await waitLine(page, "   3 files");
    const asks = [];
    const onRequest = (req) => { if (req.url().endsWith("/file-links")) asks.push(req.postDataJSON().words); };
    page.on("request", onRequest);
    await sleep(1500);
    page.off("request", onRequest);
    assert(asks.length === 0, `the count asked ${JSON.stringify(asks)}`);
    const at = await cellOf(page, counted.m, "notes.txt");
    assert(await linkShown(page, at), "the path above the count shows no link");
    await opens(page, () => page.mouse.click(at.x, at.y), { path: "notes.txt" });
    await freshShellPage(page);
    await waitLine(page, `${counted.m}e`);
  });

  // A tool line wrapped at claude's margin above a count, one above a number
  // after a colon, one above "45 lines checked.", a numbered list fold -w 40
  // wrapped narrower than the pane. Every case reports what a click opened.
  await run("live terminal: no join appends the number of the row below to a path", async () => {
    await freshShellPage(page);
    const { m, sh } = mark();
    await shell(page, `clear; printf '%s\\n  ⎿  Warn in src/ap\\n     p.py:2\\n     3 more.\\n  ⎿  Wrote build/no\\n     tes.txt:\\n     4 lines\\n' ${sh}j; printf '1. %s src/app.py:3\\n2. next\\n' ${"a".repeat(31)} | fold -w 40; printf '  ⎿  Error checked in src/ap\\n     p.py:12\\n     45 lines checked.\\n%s\\n' ${sh}e`);
    await waitLine(page, `${m}e`);
    const got = [];
    for (const [needle, down, file, line] of [["p.py:2", 2, FILE, "2"], ["tes.txt", 5, "build/notes.txt", null], ["pp.py:3", 8, null, null], ["p.py:12", 11, FILE, "12"]]) {
      const at = await cellOf(page, `${m}j`, needle, { row: down });
      await linkShown(page, at, "#terminal", file ? 2000 : 800);
      const asked = page.waitForRequest((req) => req.url().includes("/editor?"), { timeout: 1500 }).then((req) => new URL(req.url()).searchParams, () => null);
      await page.mouse.click(at.x, at.y);
      const q = await asked;
      got.push({ needle, opened: q ? `${q.get("file")}${q.get("line") ? `:${q.get("line")}` : ""}` : "nothing", ok: q ? q.get("file") === file && q.get("line") === line : !file });
      if (q) {
        await freshShellPage(page);
        await waitLine(page, `${m}e`);
      }
    }
    const report = got.map((g) => `${g.needle} opened ${g.opened}`).join(", ");
    assert(got.every((g) => g.ok), report);
    for (const [needle, down] of [[" 3", 3], [" 4", 6], ["2.", 9], [" 45", 12]]) {
      assert(!(await linkShown(page, await cellOf(page, `${m}j`, needle, { row: down }), "#terminal", 800)), `${needle.trim()} shows a link`);
    }
    return report;
  });

  await run("live terminal: rows watch -n 0.2 keeps redrawing ask nothing while it runs, a path standing still among them links", async () => {
    await freshShellPage(page);
    const { m, sh } = mark();
    await shell(page, `clear; watch -n 0.2 "printf '%s %s\\n' ${sh} notes.txt; date +%T.%N; cat /proc/loadavg"`);
    await waitLine(page, `${m} notes.txt`);
    await sleep(2500);
    const asks = [];
    const onRequest = (req) => { if (req.url().endsWith("/file-links")) asks.push(req.postDataJSON().words); };
    page.on("request", onRequest);
    await sleep(4000);
    page.off("request", onRequest);
    assert(asks.length <= 1, `watch asked ${asks.length} times in 4s: ${JSON.stringify(asks)}`);
    assert(await linkShown(page, await cellOf(page, m, "notes.txt")), "the path among the redrawn rows shows no link");
    await input(page, shellUrl, { control: "ctrl-c" });
    await shell(page, "clear");
    return `${asks.length} requests in 4s`;
  });

  await run("live terminal: top -d 0.5 re-sorting its rows asks next to nothing, never about a word without a letter", async () => {
    await freshShellPage(page);
    const asks = [];
    const onRequest = (req) => { if (req.url().endsWith("/file-links")) asks.push({ at: Date.now(), words: req.postDataJSON().words }); };
    page.on("request", onRequest);
    await shell(page, "clear; top -d 0.5");
    await waitLine(page, "top - ");
    await sleep(2500);
    const from = Date.now();
    await sleep(4000);
    page.off("request", onRequest);
    await input(page, shellUrl, { prompt: "q" });
    await shell(page, "clear");
    const late = asks.filter((a) => a.at >= from);
    const letterless = asks.flatMap((a) => a.words).filter((word) => !/\p{L}/u.test(word));
    assert(late.length <= 1, `top asked ${late.length} times in 4s: ${JSON.stringify(late.map((a) => a.words))}`);
    assert(!letterless.length, `top asked about ${JSON.stringify(letterless)}`);
    return `${late.length} requests in 4s`;
  });

  await run("live terminal: a word is asked about once, however often it is printed or redrawn", async () => {
    const words = [];
    const onRequest = (req) => { if (req.url().endsWith("/file-links")) words.push(...req.postDataJSON().words); };
    page.on("request", onRequest);
    await freshShellPage(page);
    const { m, sh } = mark();
    await shell(page, `clear; for i in $(seq 30); do printf '%s %s %s plain words\\n' ${sh} notes.txt ${LINKED}; sleep 0.05; clear; done; printf '%s %s %s\\n' ${sh}e notes.txt ${LINKED}`);
    await waitLine(page, `${m}e`);
    assert(await linkShown(page, await cellOf(page, `${m}e`, LINKED)), "the path shows no link");
    page.off("request", onRequest);
    const counts = Object.fromEntries(["notes.txt", LINKED].map((w) => [w, words.filter((x) => x === w).length]));
    assert(counts["notes.txt"] === 1 && counts[LINKED] === 1, `asked ${JSON.stringify(counts)} times, all words ${JSON.stringify(words)}`);
    return `${words.length} words asked`;
  });

  await run("live terminal: parts of a path on rows that are not adjacent link nothing, a part on the row right below still opens", async () => {
    await freshShellPage(page);
    const { m, sh } = mark();
    const w = m.length + 1;
    await shell(page, `clear; printf '%s src/\\r\\n\\r\\napp.py\\n%s src/\\033[2Bapp.py\\n%s src/\\033[3Eapp.py\\n%s src/a\\r\\n\\r\\n  pp.py:3:7 (+5 -5)\\n%s  Read src/a\\r\\033[1B\\033[${w + 2}Cpp.py:3:7\\n' ${sh}a ${sh}b ${sh}c ${sh}d ${sh}e`);
    await waitLine(page, `${m}e`);
    for (const [row, needle, down] of [["a", "src/", 0], ["a", "app.py", 2], ["b", "src/", 0], ["b", "app.py", 2], ["c", "src/", 0], ["c", "app.py", 3], ["d", "src/a", 0], ["d", "pp.py:3:7", 2]]) {
      const at = await cellOf(page, `${m}${row}`, needle, { row: down });
      assert(!(await linkShown(page, at, "#terminal", 800)), `${needle} ${down} rows below ${m}${row} shows a link`);
    }
    const below = await cellOf(page, `${m}e`, "pp.py:3:7", { row: 1 });
    await opens(page, () => page.mouse.click(below.x, below.y), { path: FILE, line: 3, col: 7 });
  });

  // Keeps every xterm the page makes, whose link entries live in its core.
  const keepTerms = () => {
    let wrapped;
    Object.defineProperty(window, "Terminal", {
      configurable: true,
      get: () => wrapped,
      set: (T) => {
        wrapped = new Proxy(T, { construct: (target, args) => {
          const term = Reflect.construct(target, args);
          (window.dcTerms ||= []).push(term);
          return term;
        } });
      },
    });
  };

  await run("live terminal: a row redrawn in place 5,000 times adds one link entry per path, every mention of a path opens, beside another on its row and below it too", async () => {
    await page.addInitScript(keepTerms);
    await freshShellPage(page);
    const entries = () => page.evaluate(() => window.dcTerms.find((t) => document.querySelector("#terminal").contains(t.element))._core._oscLinkService._dataByLinkId.size);
    const before = await entries();
    const { m, sh } = mark();
    await shell(page, `clear; for i in $(seq 5000); do printf '\\r%s %s %s' ${sh} notes.txt ${LINKED}; done; printf '\\n%s %s (%s)\\n%s %s\\n' ${sh}a notes.txt notes.txt ${sh}b notes.txt`);
    await waitLine(page, `${m}b`);
    const added = (await entries()) - before;
    assert(added <= 2, `${added} link entries for two paths`);
    const scrolled = await page.evaluate(() => new Promise((resolve) => {
      const term = window.dcTerms.find((t) => document.querySelector("#terminal").contains(t.element));
      const t0 = performance.now();
      term.write(Array.from({ length: 10000 }, (_, i) => `scroll ${i}`).join("\r\n"), () => resolve(Math.round(performance.now() - t0)));
    }));
    for (const [row, needle, want] of [[m, LINKED, { path: FILE, line: 3, col: 7 }], [`${m}a`, "notes.txt", { path: "notes.txt" }], [`${m}a`, "(notes.txt", { path: "notes.txt" }], [`${m}b`, "notes.txt", { path: "notes.txt" }]]) {
      await freshShellPage(page);
      await waitLine(page, `${m}b`);
      const at = await cellOf(page, row, needle);
      await opens(page, () => page.mouse.click(at.x, at.y), want);
    }
    return `${added} link entries added, 10,000 lines scrolled in ${scrolled}ms`;
  });

  await run("live terminal: a program's own file link with an id like a mark's opens as before", async () => {
    await freshShellPage(page);
    const { m, sh } = mark();
    await shell(page, `clear; printf '%s own \\033]8;id=dc1.zz;file://${root}/${FILE}\\007${FILE}\\033]8;;\\007 x\\n' ${sh}`);
    await waitLine(page, m);
    const at = await cellOf(page, m, FILE);
    assert(await linkShown(page, at), "the program's link shows no link");
    await opens(page, () => page.mouse.click(at.x, at.y), { path: FILE });
  });

  await run("live terminal: a program's own link stays its own, also over a path, and wins on the cells it covers", async () => {
    await freshShellPage(page);
    const { m, sh } = mark();
    await shell(page, `clear; printf '%s own \\033]8;;https://example.invalid/own\\007${FILE}\\033]8;;\\007 x\\n%s part src/\\033]8;;https://example.invalid/osc\\007app.py\\033]8;;\\007:2 x\\n' ${sh} ${sh}p`);
    await waitLine(page, `${m}p`);
    await recordOpens(page);
    const part = await cellOf(page, `${m}p`, "src/");
    assert(!(await linkShown(page, part, "#terminal", 800)), "the path part beside a program's link shows a link");
    await page.mouse.click(part.x, part.y);
    await stays(page, "a click on the path part beside a program's link");
    for (const [row, needle] of [[m, FILE], [`${m}p`, "app.py"]]) {
      const at = await cellOf(page, row, needle);
      assert(await linkShown(page, at), `${needle} shows no link`);
      await page.mouse.click(at.x, at.y);
      await sleep(600);
    }
    const urls = await opened(page);
    assert(JSON.stringify(urls) === JSON.stringify(["https://example.invalid/own", "https://example.invalid/osc"]), `the program's links opened ${JSON.stringify(urls)}`);
    await stays(page, "a program's link", 300);
  });

  await run("live terminal: a web address shows its link and opens it in a new tab, as before", async () => {
    await freshShellPage(page);
    const { m, sh } = mark();
    await shell(page, `clear; printf '%s %s\\n' ${sh} 'web https://example.invalid/src/app.py'`);
    await waitLine(page, m);
    const at = await cellOf(page, m, "example");
    await recordOpens(page);
    assert(await linkShown(page, at), "the web address shows no link");
    await page.mouse.click(at.x, at.y);
    await page.waitForFunction(() => sessionStorage.getItem("e2e-opened") !== "[]", null, { timeout: 2000, polling: 25 });
    const urls = await opened(page);
    assert(JSON.stringify(urls) === JSON.stringify(["https://example.invalid/src/app.py"]), `the click opened ${JSON.stringify(urls)}`);
    await stays(page, "a web address", 300);
  });

  await run("live terminal: nothing outside the project links", async () => {
    await freshShellPage(page);
    const { m, sh } = mark();
    await shell(page, `clear; printf '%s %s\\n' ${outside.map((line) => `${sh} '${line}'`).join(" ")}`);
    await waitLine(page, `${m} README`);
    for (const needle of outside) {
      const at = await cellOf(page, `${m} ${needle}`, needle);
      assert(!(await linkShown(page, at, "#terminal", 600)), `${needle} shows a link`);
      await page.mouse.click(at.x, at.y);
      await stays(page, `a click on ${needle}`, 600);
    }
  });

  await run("live terminal: typing echoes as before, asks and links nothing, the command's output links", async () => {
    await freshShellPage(page);
    const { m } = mark();
    await page.locator("#terminal .xterm-screen").click();
    const asks = [];
    const onRequest = (req) => { if (req.url().endsWith("/file-links")) asks.push(req.postDataJSON().words); };
    page.on("request", onRequest);
    const typed = `echo ${m} ${FILE}`;
    const waits = [];
    for (const ch of typed) {
      const before = await mirror(page, "#terminal");
      const t0 = Date.now();
      await page.keyboard.type(ch);
      await page.waitForFunction(([b]) => (document.querySelector("#terminal .attach-selection")?.textContent || "") !== b, [before], { timeout: 5000, polling: 5 });
      waits.push(Date.now() - t0);
    }
    const lines = (await mirror(page, "#terminal")).split("\n");
    assert(lines.some((line) => line.endsWith(typed)), `the prompt line does not read ${JSON.stringify(typed)}: ${JSON.stringify(lines.slice(-3))}`);
    const prompt = await page.evaluate(([t, f]) => {
      const node = document.querySelector("#terminal .attach-selection").firstChild;
      const index = node.data.lastIndexOf(t) + t.length - f.length;
      const range = document.createRange();
      range.setStart(node, index + 1);
      range.setEnd(node, index + 2);
      const box = range.getBoundingClientRect();
      return { x: box.left + box.width / 2, y: box.top + box.height / 2 };
    }, [typed, FILE]);
    assert(!(await linkShown(page, prompt, "#terminal", 800)), "the typed path shows a link");
    page.off("request", onRequest);
    assert(asks.length === 0, `typing asked ${JSON.stringify(asks)}`);
    await page.keyboard.press("Enter");
    await waitLine(page, `${m} ${FILE}`);
    assert(await linkShown(page, await cellOf(page, m, FILE)), "the echoed path shows no link");
    waits.sort((a, b) => a - b);
    return `echo median ${waits[Math.floor(waits.length / 2)]}ms, max ${waits.at(-1)}ms over ${waits.length} keys`;
  });

  // The panel's terminal must still be the very node it was: a page load
  // would replace it.
  const opensInPlace = async (name, id) => {
    await page.goto(`${BASE}/projects/${encodeURIComponent(name)}/editor?file=${encodeURIComponent(FILE)}`, { waitUntil: "domcontentloaded" });
    await waitCursor(page, { path: FILE });
    const scope = `[data-term-pane="${id}"]`;
    if (!(await page.isVisible(scope))) await page.keyboard.press("Control+j");
    await page.waitForSelector(`${scope} terminal-attach .xterm-screen canvas`, { state: "visible", timeout: 15000 });
    const url = `${BASE}/shells/${id}`;
    const { m, sh } = mark();
    await shell(page, `clear; printf '%s %s\\n' ${sh} 'pin notes.txt:1'`, url);
    await waitLine(page, m, scope);
    await page.evaluate((s) => { window.__panelTerm = document.querySelector(`${s} terminal-attach`); }, scope);
    const at = await cellOf(page, m, "notes.txt", { scope });
    assert(await linkShown(page, at, scope), "the panel's path shows no link");
    await page.mouse.click(at.x, at.y);
    await waitCursor(page, { path: "notes.txt", line: 1, col: 1 });
    assert(await page.evaluate((s) => document.querySelector(`${s} terminal-attach`) === window.__panelTerm, scope), "the page was loaded again, the panel's terminal is another one");
    assert(page.url().includes(`/projects/${encodeURIComponent(name)}/editor`) || page.url().includes(`/projects/${name}/editor`), `the page moved to ${page.url()}`);
  };

  await run("editor panel: a file link opens in place, the panel's terminal stays, for a project named like p(1) too", async () => {
    await opensInPlace(project, shellId);
    escapedShellUrl = await L.createShell(page, escaped);
    await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
    const { m, sh } = mark();
    await shell(page, `mkdir -p src && printf 'one\\n' > ${FILE} && clear && echo ${sh} made`, escapedShellUrl);
    await waitLine(page, m);
    await opensInPlace(escaped, new URL(escapedShellUrl).pathname.split("/").pop());
  });

  await run("copy view: 50,000 lines dense with paths and addresses answer within a second and render every link", async () => {
    await freshShellPage(page);
    const { m, sh } = mark();
    await shell(page, `clear; seq 50000 | sed 's|.*|m${marks}m ${FILE}:& https://example.invalid/&|'; echo ${sh}done`);
    await waitLine(page, `${m}done`);
    await page.locator("[data-terminal-copy]:visible").first().click();
    await page.waitForSelector("terminal-copy [data-copy-text] a", { state: "visible", timeout: 15000 });
    const answer = page.waitForResponse((res) => res.url().includes("/copy?") && res.url().includes("lines=50000"), { timeout: 30000 });
    await page.selectOption("terminal-copy [data-copy-lines]", "50000");
    const res = await answer;
    const timing = res.request().timing();
    const serverMs = Math.round(timing.responseStart - timing.requestStart);
    const data = await res.json();
    await page.waitForFunction((n) => document.querySelectorAll("terminal-copy [data-copy-text] a").length >= n, 50000, { timeout: 30000, polling: 500 });
    const counts = await page.evaluate(() => {
      const anchors = [...document.querySelectorAll("terminal-copy [data-copy-text] a")];
      const files = anchors.filter((a) => a.hasAttribute("data-file-link"));
      const last = files.at(-1);
      return { all: anchors.length, files: files.length, last: last?.textContent, href: last?.getAttribute("href") };
    });
    assert(serverMs < 1000, `the answer took ${serverMs}ms`);
    assert(counts.files === data.links.length && counts.files > 4000, `${counts.files} file links rendered, the answer named ${data.links.length}`);
    assert(counts.last === `${FILE}:50000` && counts.href === editorHref("file=src%2Fapp.py&line=50000"), `the newest link is ${JSON.stringify(counts)}`);
    await page.locator("terminal-copy [data-copy-close]").first().click();
    return `${counts.all} links, ${counts.files} files, answer in ${serverMs}ms`;
  });

  await run("conversation view: the bubbles carry their file links on the rendered text, opening it asks for none, a link opens", async () => {
    coderUrl = await L.createSession(page, project, "fl-conv", "claude");
    const id = new URL(coderUrl).pathname.split("/").pop();
    const user = JSON.stringify({ type: "user", uuid: "fl-u", isSidechain: false, message: { role: "user", content: `see ${LINKED} and notes.txt.` } });
    const coder = JSON.stringify({ type: "assistant", uuid: "fl-a", isSidechain: false, message: { role: "assistant", content: [
      { type: "text", text: `Changed \`${LINKED}\`, see [notes.txt](https://example.com/) and notes.txt.` },
      { type: "tool_use", id: "fl-t", name: "Read", input: { file_path: `${root}/notes.txt` } },
    ] } });
    const { m, sh } = mark();
    await shell(page, `f=$(ls ~/.claude/projects/*/${id}.jsonl 2>/dev/null | head -1); [ -n "$f" ] || { mkdir -p ~/.claude/projects/zzfl && f=~/.claude/projects/zzfl/${id}.jsonl; }; printf '%s\\n' '${user}' '${coder}' >> "$f" && clear && echo ${sh} seeded`);
    await page.goto(shellUrl, { waitUntil: "domcontentloaded" });
    await waitLine(page, m);
    const linkRoute = (req) => {
      const path = new URL(req.url()).pathname;
      return path === "/terminal-link" || path.endsWith("/file-links");
    };
    let lastAsk = Date.now();
    const onTerminalAsk = (req) => { if (linkRoute(req)) lastAsk = Date.now(); };
    page.on("request", onTerminalAsk);
    await page.goto(coderUrl, { waitUntil: "domcontentloaded" });
    await page.waitForSelector("#terminal .xterm-screen", { timeout: 15000 });
    while (Date.now() - lastAsk < 1000) await sleep(100);
    page.off("request", onTerminalAsk);
    const asks = [];
    const onRequest = (req) => { if (linkRoute(req)) asks.push(req.url()); };
    page.on("request", onRequest);
    await page.locator("[data-terminal-copy]:visible").first().click();
    await page.waitForSelector("terminal-copy [data-copy-log] a[data-file-link]", { state: "visible", timeout: 10000 });
    await sleep(1000);
    page.off("request", onRequest);
    const links = await page.evaluate(() => [...document.querySelectorAll("terminal-copy [data-copy-log] a")]
      .map((a) => ({ text: a.textContent, href: a.getAttribute("href"), file: a.hasAttribute("data-file-link") })));
    const want = [
      { text: LINKED, href: editorHref("col=7&file=src%2Fapp.py&line=3"), file: true },
      { text: "notes.txt", href: editorHref("file=notes.txt"), file: true },
      { text: LINKED, href: editorHref("col=7&file=src%2Fapp.py&line=3"), file: true },
      { text: "notes.txt", href: "https://example.com/", file: false },
      { text: "notes.txt", href: editorHref("file=notes.txt"), file: true },
      { text: `${root}/notes.txt`, href: editorHref("file=notes.txt"), file: true },
    ];
    assert(JSON.stringify(links) === JSON.stringify(want), `links ${JSON.stringify(links)}, want ${JSON.stringify(want)}`);
    assert(asks.length === 0, `opening the conversation asked ${JSON.stringify(asks)}`);
    await opens(page, () => page.locator("terminal-copy [data-copy-log] code a[data-file-link]").first().click(), { path: FILE, line: 3, col: 7 });
  });

  // ---- touch, 360px -------------------------------------------------------------

  const phone = async () => {
    const mp = await mobilePage();
    await mp.setViewportSize({ width: 360, height: 760 });
    return mp;
  };
  const printOnPhone = async (mp, lines, last) => {
    await freshShellPage(mp);
    await shell(mp, `clear; seq 12; ${lines}`);
    await waitLine(mp, last);
  };

  await run("touch: a tap on a printed path opens it at its line and column", async () => {
    const mp = await phone();
    const { m, sh } = mark();
    await printOnPhone(mp, `printf '%s %s\\n' ${sh} 'tap ${LINKED}'`, m);
    await mp.evaluate(() => document.activeElement?.blur());
    const at = await cellOf(mp, m, LINKED);
    await opens(mp, () => mp.touchscreen.tap(at.x, at.y), { path: FILE, line: 3, col: 7 });
  });

  await run("touch: a path the terminal wrapped over four rows keeps the terminal and its links after a reload, at 360px", async () => {
    const mp = await phone();
    const { m, sh } = mark();
    await printOnPhone(mp, `c=$(tput cols); d=deep/$(printf "%0$((3*c))d" 0); mkdir -p $d && touch $d/file.txt && printf '%s %s\\n%s\\n%s\\n%s\\n' ${sh}w $c $d/file.txt notes.txt ${sh}e`, `${m}e`);
    const cols = Number((await mirror(mp, "#terminal")).split("\n").find((line) => line.startsWith(`${m}w`)).split(" ")[1]);
    for (const [needle, down, path] of [["deep", 1, `deep/${"0".repeat(3 * cols)}/file.txt`], ["notes.txt", 5, "notes.txt"]]) {
      await freshShellPage(mp);
      await waitLine(mp, `${m}e`);
      await mp.evaluate(() => document.activeElement?.blur());
      const at = await cellOf(mp, `${m}w`, needle, { row: down });
      await opens(mp, () => mp.touchscreen.tap(at.x, at.y), { path });
    }
  });

  await run("touch: a tap on the rest of a path a program partly overwrote opens nothing", async () => {
    const mp = await phone();
    const { m, sh } = mark();
    await printOnPhone(mp, overwriteTail(m, sh), `${m}k`);
    await waitLine(mp, `${m} src/zz.txt`);
    await mp.evaluate(() => document.activeElement?.blur());
    const rest = await cellOf(mp, m, "src/");
    await mp.touchscreen.tap(rest.x, rest.y);
    await stays(mp, "a tap on the rest of an overwritten path");
    await mp.evaluate(() => document.activeElement?.blur());
    const fresh = await cellOf(mp, `${m}k`, "notes.txt");
    await opens(mp, () => mp.touchscreen.tap(fresh.x, fresh.y), { path: "notes.txt" });
  });

  await run("touch: a tap on a file link in the copy view opens the editor", async () => {
    const mp = await phone();
    const { m, sh } = mark();
    await printOnPhone(mp, `printf '%s %s\\n' ${sh} 'copy ${LINKED}'`, m);
    await mp.locator("[data-terminal-copy]:visible").first().tap();
    const link = mp.locator(`terminal-copy a[data-file-link]:text-is("${LINKED}")`).last();
    await link.waitFor({ state: "visible", timeout: 10000 });
    await opens(mp, () => link.tap(), { path: FILE, line: 3, col: 7 });
  });

  await run("cleanup: coder, shells and projects deleted", async () => {
    if (coderUrl) await L.stopSession(page, coderUrl);
    if (shellUrl) await L.deleteShell(page, shellUrl);
    if (escapedShellUrl) await L.deleteShell(page, escapedShellUrl);
    for (const name of [project, escaped, `${project}-wt`]) await L.deleteProject(page, name);
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    for (const name of [project, escaped, `${project}-wt`]) {
      assert(!(await page.$(`[data-project-name="${name}"]`)), `${name} is still listed`);
    }
  });
});
