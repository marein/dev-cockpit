const L = require("./lib");
const { assert, sleep, BASE } = L;

// File links: a path of an existing file of the terminal's project, relative
// to its root or absolute inside it, alone or with :line or :line:col, links
// into the project's editor (GET /projects/:name/editor?file=&line=&col=, the
// cursor lands there). The server decides what is a file
// (filesystem.FileRefFinder): the copy view's text arrives with its links in
// the GET /shells/:id/copy answer, a conversation with them in its markup, and
// the live terminal asks POST /shells/:id/file-links (and the /coders/ twin)
// when a click or a tap lands on a token that may be a path, never on
// hover. It reads the rows the link runs over again when the answer comes and
// opens the link only while they read the same, a row beside them may change.
// A later click or tap drops the answer of an earlier one, also of a
// program's file link (GET /terminal-link). A path the terminal wrapped, or a
// program wrapped at the last column, links over its rows. In the live
// terminal so does one a program wrapped at its margin and went on with at an
// indent under a word of the first row, as claude's tool lines do, in the
// longest join that names a file and never from the indent, while a row that
// names a file on its own keeps it. Other rows never join, a join that names
// no file links nothing, its parts alone still do. A program's OSC 8 link
// wins on the cells it covers. Touch: a tap opens an OSC 8 link or a web
// address at once and leaves the keyboard alone, any other tap toggles the
// keyboard and opens a path once the answer is there.
// Inside the editor's terminal panel a link opens in place, also for a project
// named like p(1), which the server escapes otherwise than the page.
// Every check prints its own lines behind a fresh mark (m<n>m) and waits for
// them, the shell gets its commands through the input route. The files are
// not Go, so opening the editor starts no language server, whose container
// start drops the browser's connections.
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
  const enter = (p, url = shellUrl) => input(p, url, { raw: "\r" });

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
  const countAsks = (p) => {
    const asks = [];
    const on = (req) => { if (req.method() === "POST" && req.url().includes("/file-links")) asks.push(req.postData() || ""); };
    p.on("request", on);
    return { asks, stop: () => p.off("request", on) };
  };
  // The answer of a request to path whose address or body holds needle waits
  // until answer() lets it go and returns once it arrived. The route stays
  // until stop(), after what the answer opened: removing it while the page
  // starts a navigation strands that navigation.
  const holdAnswer = async (p, path, needle) => {
    let asked;
    let release;
    const arrived = new Promise((resolve) => { asked = resolve; });
    const held = new Promise((resolve) => { release = resolve; });
    const matches = (url) => url.pathname.endsWith(path);
    const holds = (req) => matches(new URL(req.url())) && (req.url() + (req.postData() || "")).includes(needle);
    const handler = async (route) => {
      if (holds(route.request())) {
        asked();
        await held;
      }
      await route.continue();
    };
    await p.route(matches, handler);
    return {
      arrived,
      answer: async () => {
        const answered = p.waitForResponse((res) => holds(res.request()), { timeout: 15000 });
        release();
        await answered;
      },
      stop: () => p.unroute(matches, handler),
    };
  };
  // What window.open was asked for, kept in sessionStorage so it outlives a
  // page a link opens, and in which event: a tap must open within the click
  // the scroll zone replays at the touch's end, where a new tab is still
  // allowed.
  const recordOpens = (p) => p.evaluate(() => {
    sessionStorage.setItem("e2e-opened", "[]");
    window.open = (url) => {
      const all = JSON.parse(sessionStorage.getItem("e2e-opened") || "[]");
      sessionStorage.setItem("e2e-opened", JSON.stringify([...all, { url, during: window.event?.type || "" }]));
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

  await run("setup: a project with two files, a symlink out, one into a sibling project named like p(1), a look-alike sibling", async () => {
    await L.createProject(page, project);
    root = await L.projectPath(page, project);
    shellUrl = await L.createShell(page, project);
    shellId = new URL(shellUrl).pathname.split("/").pop();
    await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
    const { m, sh } = mark();
    await shell(page, `mkdir -p src '~' '../${project}-wt' '../${escaped}' && printf 'one\\ntwo\\nthree  seven\\n' > ${FILE} && printf 'hi\\n' > notes.txt && touch '~/home.txt' '../${project}-wt/main.py' && printf 'hi\\n' > '../${escaped}/notes.txt' && ln -s /etc/hostname out.txt && ln -s '../${escaped}/notes.txt' into.txt && clear && echo ${sh} ready`);
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

  await run("live terminal: a click opens a path at its line and column, a click on plain text asks nothing, a missing file opens nothing", async () => {
    await freshShellPage(page);
    const { m, sh } = mark();
    await shell(page, `clear; printf '%s %s\\n' ${sh} 'plain words only' ${sh} 'see ${LINKED} now' ${sh} 'gone missing.py:3'`);
    await waitLine(page, `${m} gone`);
    const watch = countAsks(page);
    const plain = await cellOf(page, m, "words");
    await page.mouse.click(plain.x, plain.y);
    await stays(page, "a click on plain text", 800);
    assert(watch.asks.length === 0, `a click on plain text asked ${watch.asks.length} times`);
    const missing = await cellOf(page, `${m} gone`, "missing.py");
    await page.mouse.click(missing.x, missing.y);
    await stays(page, "a click on a missing file");
    assert(watch.asks.length === 1, `a click on a missing file asked ${watch.asks.length} times`);
    const linked = await cellOf(page, `${m} see`, LINKED);
    await opens(page, () => page.mouse.click(linked.x, linked.y), { path: FILE, line: 3, col: 7 });
    watch.stop();
    assert(watch.asks.length === 2, `the click on a path asked ${watch.asks.length - 1} times`);
  });

  await run("live terminal: a drag over a path selects and opens nothing, Ctrl+click opens it in a new tab", async () => {
    await freshShellPage(page);
    const { m, sh } = mark();
    await shell(page, `clear; printf '%s %s\\n' ${sh} 'see ${LINKED} now'`);
    await waitLine(page, m);
    const watch = countAsks(page);
    const from = await cellOf(page, m, "see");
    const to = await cellOf(page, m, "3:7");
    await page.mouse.move(from.x, from.y);
    await page.mouse.down();
    await page.mouse.move(to.x, to.y, { steps: 8 });
    await page.mouse.up();
    await stays(page, "a drag over a path");
    assert(watch.asks.length === 0, `the drag asked ${watch.asks.length} times`);
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
    watch.stop();
    assert(href === editorHref("col=7&file=src%2Fapp.py&line=3"), `the new tab is ${href}`);
    await stays(page, "Ctrl+click", 300);
  });

  // The terminal wraps the first, the program the second and the third (a row
  // filled to the last column, the next one starting without a space): the
  // second names a file joined, the third joined names none, the fourth
  // joined names none while its first part alone does.
  const printWrapped = async (p, m, sh, url) => {
    const w = m.length + 1;
    await shell(p, `clear; seq 12; c=$(tput cols); printf '%s%*s%s\\n%s%*s%s\\n%s\\n%s%*s%s\\n%s\\n%s%*s%s\\nmore\\n' ${sh}s $((c-${w + 5})) '' '${LINKED}' ${sh}t $((c-${w + 6})) '' 'notes.' 'txt' ${sh}n $((c-${w + 6})) '' 'ghost/' 'nothing.py' ${sh}p $((c-${w + 9})) '' 'notes.txt'`, url);
    await waitLine(p, `${m}p`);
  };

  await run("live terminal: a wrapped path opens from any of its rows, a join naming no file links only its parts", async () => {
    await freshShellPage(page);
    const { m, sh } = mark();
    await printWrapped(page, m, sh);
    const second = await cellOf(page, `${m}s`, "pp.py:3:7", { row: 1 });
    await opens(page, () => page.mouse.click(second.x, second.y), { path: FILE, line: 3, col: 7 });
    for (const [row, needle, down] of [[`${m}t`, "notes.", 0], [`${m}t`, "txt", 1], [`${m}p`, "notes.txt", 0]]) {
      await freshShellPage(page);
      await waitLine(page, `${m}p`);
      const at = await cellOf(page, row, needle, { row: down });
      await opens(page, () => page.mouse.click(at.x, at.y), { path: "notes.txt" });
    }
    await freshShellPage(page);
    await waitLine(page, `${m}p`);
    for (const [row, needle, down] of [[`${m}n`, "ghost/", 0], [`${m}n`, "nothing.py", 1], [`${m}p`, "more", 1]]) {
      const at = await cellOf(page, row, needle, { row: down });
      await page.mouse.click(at.x, at.y);
      await stays(page, `a click on ${needle}`, 1000);
    }
  });

  await run("copy view: a wrapped path links over the line break, a join naming no file links only its parts", async () => {
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
    const want = [
      { text: "src/a\npp.py:3:7", href: editorHref("col=7&file=src%2Fapp.py&line=3") },
      { text: "notes.\ntxt", href: editorHref("file=notes.txt") },
      { text: "notes.txt", href: editorHref("file=notes.txt") },
    ];
    assert(JSON.stringify(links) === JSON.stringify(want), `links ${JSON.stringify(links)}, want ${JSON.stringify(want)}`);
    await page.locator("terminal-copy [data-copy-close]").first().click();
  });

  // Paths a program wraps itself, as claude wraps a tool line: it fills a row
  // up to its margin, here the 18th column, and goes on at an indent under a
  // word of the first row. Every press lands on a freshly loaded page that
  // shows the rows as printed, a click on the desktop and a tap on the phone,
  // opens what want names or nothing, and sends one request with the rows it
  // names, none for a blank cell.
  const HOOK = "\\342\\216\\277";
  const AT_3_7 = { path: FILE, line: 3, col: 7 };
  const AT_2 = { path: FILE, line: 2, col: 1 };
  const TERMLINKS = { path: "internal/web/static/js/dc/termlinks.js" };
  const wrapCases = [
    {
      name: "a path wrapped with a hanging indent, as claude does, opens from either row and never from the indent, a wrong join opens nothing",
      print: (sh) => `printf '%s\\n  ${HOOK}  Updated src/a\\n     pp.py:3:7 (+5\\n      -5)\\n       1 + one\\n%s\\n  ${HOOK}  Updated src/a\\n     bc.py (+1 -1)\\n' ${sh}c ${sh}w`,
      presses: [["c", "src/a", 1, AT_3_7, 2], ["c", "pp.py", 2, AT_3_7, 2], ["c", "     pp", 2, null, 0], ["w", "src/a", 1, null, 2], ["w", "bc.py", 2, null, 2]],
    },
    {
      name: "claude's own tool line, a no-break space before its first word, opens from either row and never from the indent",
      print: (sh) => `mkdir -p internal/web/static/js/dc && touch internal/web/static/js/dc/termlinks.js; printf '%s\\n  ${HOOK} \\302\\240Updated internal/web/static/js/dc/ter\\n     mlinks.js (+5 -4)\\n' ${sh}n`,
      presses: [["n", "internal/", 1, TERMLINKS, 2], ["n", "mlinks.js", 2, TERMLINKS, 2], ["n", "Updated", 1, null, 0], ["n", "     ml", 2, null, 0]],
    },
    {
      name: "a path over three rows opens the longest join that names a file from any of its rows, wrapped with an indent or by the terminal",
      print: (sh, m) => `c=$(tput cols); d=$(printf 'd%.0s' $(seq $c)); mkdir -p src/$d && touch src/app-styles.css src/app-styles.css.map src/$d/app.css src/$d/app.css.map; printf '%s\\n  ${HOOK}  Updated src/a\\n     pp-styles.css\\n     .map (+1 -1)\\n%s%*s%s\\n' ${sh}c ${sh}t $((c-${m.length + 13})) '' src/$d/app.css.map`,
      presses: [
        ["c", "src/a", 1, { path: "src/app-styles.css.map" }, 3], ["c", "pp-styles", 2, { path: "src/app-styles.css.map" }, 3], ["c", ".map", 3, { path: "src/app-styles.css.map" }, 3],
        ["t", "src/d", 0, { path: /^src\/d+\/app\.css\.map$/ }, 3], ["t", ".map", 2, { path: /^src\/d+\/app\.css\.map$/ }, 3],
      ],
    },
    {
      name: "a path keeps its line over an indented row below it, also one under its first word",
      print: (sh) => `printf '%s\\nsee src/app.py:2\\n 3 more warnings\\n%s\\n  see src/app.py:2\\n  3 more warnings\\n' ${sh}s ${sh}a`,
      presses: [["s", "app.py", 1, AT_2, 1], ["s", " 3", 2, null, 0], ["a", "app.py", 1, AT_2, 2], ["a", " 3", 2, null, 2]],
    },
    {
      name: "in a git status list a file opens itself and the directory above it nothing, also below a longer one",
      print: (sh) => `mkdir -p build build-artifacts && printf 'hi\\n' | tee build/notes.txt > build-artifacts/notes.txt; printf '%s\\nUntracked files:\\n\\tbuild/\\n\\tnotes.txt\\n%s\\n\\tbuild-artifacts/\\n\\tnotes.txt\\n' ${sh}g ${sh}l`,
      presses: [["g", "build/", 2, null, 1], ["g", "notes.txt", 3, { path: "notes.txt" }, 1], ["l", "build-artifacts/", 1, null, 2], ["l", "notes.txt", 2, { path: "notes.txt" }, 2]],
    },
  ];
  const printed = async (p, m) => {
    const lines = (await mirror(p, "#terminal")).split("\n");
    return lines.slice(lines.findIndex((line) => line.startsWith(m)), lines.indexOf(`${m}e`) + 1).join("\n");
  };
  const pressWrapped = async (p, tap, { print, presses }) => {
    await freshShellPage(p);
    const { m, sh } = mark();
    await shell(p, `clear; seq 12; ${print(sh, m)}; echo ${sh}e`);
    await waitLine(p, `${m}e`);
    const rowsPrinted = await printed(p, m);
    for (const [row, needle, down, want, rows] of presses) {
      await freshShellPage(p);
      for (let i = 0; i < 80 && (await printed(p, m)) !== rowsPrinted; i += 1) await sleep(250);
      assert((await printed(p, m)) === rowsPrinted, `the terminal shows ${JSON.stringify(await printed(p, m))}, printed ${JSON.stringify(rowsPrinted)}`);
      const at = await cellOf(p, `${m}${row}`, needle, { row: down });
      const watch = countAsks(p);
      const act = () => (tap ? p.touchscreen.tap(at.x, at.y) : p.mouse.click(at.x, at.y));
      if (!want) {
        await act();
        await stays(p, `a press on ${JSON.stringify(needle)}`, 1000);
      } else if (want.path instanceof RegExp) {
        await Promise.all([p.waitForURL(/\/editor\?/, { timeout: 15000 }), act()]);
        const file = new URL(p.url()).searchParams.get("file");
        assert(want.path.test(file), `a press on ${JSON.stringify(needle)} opened ${file}`);
        await waitCursor(p, { path: file });
      } else {
        await opens(p, act, want);
      }
      watch.stop();
      const asked = watch.asks.map((body) => JSON.parse(body).text);
      assert(JSON.stringify(asked.map((text) => text.split("\n").length)) === JSON.stringify(rows ? [rows] : []), `a press on ${JSON.stringify(needle)} asked for ${JSON.stringify(asked)}, want ${rows} rows`);
    }
  };

  for (const wrapCase of wrapCases) {
    await run(`live terminal: ${wrapCase.name}`, () => pressWrapped(page, false, wrapCase));
  }

  await run("live terminal: a row printed again while its answer is on the way opens nothing, never the old file", async () => {
    await freshShellPage(page);
    const { m, sh } = mark();
    await shell(page, `clear; printf '%s %s\\n' ${sh} 'late ${FILE}:2 here'; read -rs; printf '\\033[1A\\r\\033[2K%s %s\\n' ${sh} 'late notes.txt:1 here'`);
    await waitLine(page, `${m} late ${FILE}`);
    const held = await holdAnswer(page, "/file-links", `late ${FILE}:2`);
    const at = await cellOf(page, m, `${FILE}:2`);
    await page.mouse.click(at.x, at.y);
    await held.arrived;
    await enter(page);
    await waitLine(page, `${m} late notes`);
    await Promise.all([stays(page, "a click whose row was printed again"), held.answer()]);
    await held.stop();
  });

  // A row below a path redrawn every 100ms while the answer takes 150ms: a
  // progress line right below and below another row, a count of files, a
  // spinner, a size, and a count standing under the path's first character,
  // where it reads like a row the path goes on in.
  const redraws = [
    ["", () => "'\\r   [ %s%%]' $i"], ["ok\\n", () => "'\\r   [ %s%%]' $i"], ["", () => "'\\r %s files' $i"],
    ["", () => "'\\r %s building' $(echo '-/|' | cut -c$((i % 3 + 1)))"], ["", () => "'\\r %s.5MB' $i"], ["", (m) => `'\\r%${m.length + 5}s%s files' '' $i`],
  ];
  const opensBesideRedraw = async (p, tap) => {
    const matches = (url) => url.pathname.endsWith("/file-links");
    const slow = async (route) => {
      await sleep(150);
      await route.continue();
    };
    await p.route(matches, slow);
    try {
      for (const [between, redraw] of redraws) {
        await freshShellPage(p);
        const { m, sh } = mark();
        await shell(p, `clear; seq 12; printf '%s see %s\\n${between}' ${sh} '${FILE}:2'; for i in $(seq 300); do printf ${redraw(m)}; sleep 0.1; done`);
        await waitLine(p, m);
        const at = await cellOf(p, m, `${FILE}:2`);
        await opens(p, () => (tap ? p.touchscreen.tap(at.x, at.y) : p.mouse.click(at.x, at.y)), AT_2);
        await input(p, shellUrl, { raw: "\x03" });
      }
    } finally {
      await p.unroute(matches, slow);
      await input(p, shellUrl, { raw: "\x03" });
    }
  };

  await run("live terminal: a path opens while a row below it is redrawn every 100ms and the answer takes 150ms", () => opensBesideRedraw(page, false));

  await run("live terminal: a later click wins over a program's file link waiting for its answer", async () => {
    await freshShellPage(page);
    const { m, sh } = mark();
    await shell(page, `clear; printf '%s \\033]8;;file://%s\\007slow\\033]8;;\\007 notes.txt:1\\n' ${sh} '${root}/${FILE}'`);
    await waitLine(page, m);
    await recordOpens(page);
    const held = await holdAnswer(page, "/terminal-link", "app.py");
    const slow = await cellOf(page, m, "slow");
    const later = await cellOf(page, m, "notes.txt");
    await page.keyboard.down("Control");
    await page.mouse.click(slow.x, slow.y);
    await held.arrived;
    await page.mouse.click(later.x, later.y);
    await page.keyboard.up("Control");
    await page.waitForFunction(() => sessionStorage.getItem("e2e-opened") !== "[]", null, { timeout: 10000 });
    await held.answer();
    await sleep(800);
    await held.stop();
    const urls = (await opened(page)).map((o) => o.url);
    assert(JSON.stringify(urls) === JSON.stringify([editorHref("file=notes.txt&line=1")]), `the clicks opened ${JSON.stringify(urls)}`);
  });

  await run("live terminal: a resting pointer asks nothing while output dense with paths scrolls under it", async () => {
    await freshShellPage(page);
    const { m, sh } = mark();
    await shell(page, `clear; printf '%s %s\\n' ${sh} 'rest ${LINKED}'; sleep 2; for i in $(seq 300); do printf '%s %s %s\\n' ${sh}x ${LINKED} $i; sleep 0.01; done; echo ${sh}done`);
    await waitLine(page, `${m} rest`);
    const at = await cellOf(page, m, LINKED);
    const watch = countAsks(page);
    await page.mouse.move(at.x, at.y);
    await waitLine(page, `${m}done`);
    watch.stop();
    assert(watch.asks.length === 0, `${watch.asks.length} requests while the output scrolled`);
  });

  await run("live terminal: a web address shows its link at once and opens it in a new tab, as before", async () => {
    await freshShellPage(page);
    const { m, sh } = mark();
    await shell(page, `clear; printf '%s %s\\n' ${sh} 'web https://example.invalid/m'`);
    await waitLine(page, m);
    const at = await cellOf(page, m, "example");
    await recordOpens(page);
    const t0 = Date.now();
    await page.mouse.move(at.x, at.y);
    await page.waitForFunction(() => document.querySelector("#terminal .xterm-screen").classList.contains("xterm-cursor-pointer"), null, { timeout: 1000, polling: 10 });
    const shown = Date.now() - t0;
    await page.mouse.click(at.x, at.y);
    await page.waitForFunction(() => sessionStorage.getItem("e2e-opened") !== "[]", null, { timeout: 1000, polling: 10 });
    const urls = (await opened(page)).map((o) => o.url);
    assert(JSON.stringify(urls) === JSON.stringify(["https://example.invalid/m"]), `the click opened ${JSON.stringify(urls)}`);
    await stays(page, "a web address", 300);
    return `pointer after ${shown}ms`;
  });

  await run("live terminal: a program's OSC 8 link wins over a path on the cells it covers", async () => {
    await freshShellPage(page);
    const { m, sh } = mark();
    await shell(page, `clear; printf '%s part src/\\033]8;;https://example.invalid/osc\\007app.py\\033]8;;\\007:2\\n' ${sh}`);
    await waitLine(page, m);
    await recordOpens(page);
    const path = await cellOf(page, m, "src/");
    await page.mouse.click(path.x, path.y);
    await stays(page, "a click on the path part beside an OSC 8 link");
    assert((await opened(page)).length === 0, `the path part opened ${JSON.stringify(await opened(page))}`);
    const osc = await cellOf(page, m, "app.py");
    await page.mouse.click(osc.x, osc.y);
    await sleep(800);
    const urls = (await opened(page)).map((o) => o.url);
    assert(JSON.stringify(urls) === JSON.stringify(["https://example.invalid/osc"]), `the OSC 8 cells opened ${JSON.stringify(urls)}`);
  });

  await run("live terminal: nothing outside the project opens, and /file-links refuses a post without the CSRF token", async () => {
    await freshShellPage(page);
    const { m, sh } = mark();
    const shown = ["out.txt", "into.txt", `${root}-wt/main.py`, "~/home.txt", `../${project}/notes.txt`];
    await shell(page, `clear; printf '%s %s\\n' ${shown.map((line) => `${sh} '${line}'`).join(" ")}`);
    await waitLine(page, `${m} ../`);
    for (const needle of shown) {
      const at = await cellOf(page, `${m} ${needle}`, needle);
      await page.mouse.click(at.x, at.y);
      await stays(page, `a click on ${needle}`, 1000);
    }
    const status = await page.evaluate(async (url) => (await fetch(url, {
      method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ text: "notes.txt" }),
    })).status, `/shells/${shellId}/file-links`);
    assert(status === 403, `a post without the token answered ${status}`);
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

  await run("live terminal: in output of one path per line a click asks for its own row alone", async () => {
    await freshShellPage(page);
    const { m, sh } = mark();
    await shell(page, `clear; seq 20 | sed 's|.*|./internal/web/static/js/file&.js|'; printf '%s\\n' ${sh} ./${FILE} ./notes.txt ./${FILE}`);
    await waitLine(page, m);
    const watch = countAsks(page);
    const at = await cellOf(page, m, "./notes.txt", { row: 2 });
    await opens(page, () => page.mouse.click(at.x, at.y), { path: "notes.txt" });
    watch.stop();
    const asked = watch.asks.map((body) => JSON.parse(body).text.trimEnd());
    assert(JSON.stringify(asked) === JSON.stringify(["./notes.txt"]), `the click asked for ${JSON.stringify(asked)}`);
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
    await page.goto(coderUrl, { waitUntil: "domcontentloaded" });
    await page.waitForSelector("#terminal .xterm-screen", { timeout: 15000 });
    const watch = countAsks(page);
    await page.locator("[data-terminal-copy]:visible").first().click();
    await page.waitForSelector("terminal-copy [data-copy-log] a[data-file-link]", { state: "visible", timeout: 10000 });
    await sleep(1000);
    watch.stop();
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
    assert(watch.asks.length === 0, `opening the conversation asked ${watch.asks.length} times`);
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
  const keyboardOpen = (mp) => mp.evaluate(() => document.activeElement?.id === "terminal-cursor-input");
  const watchKeyboard = (mp) => mp.evaluate(() => {
    sessionStorage.removeItem("e2e-keyboard");
    document.getElementById("terminal-cursor-input").addEventListener("focus", () => sessionStorage.setItem("e2e-keyboard", "1"));
  });
  const keyboardOpened = (mp) => mp.evaluate(() => sessionStorage.getItem("e2e-keyboard") === "1");

  await run("touch: a tap on an OSC 8 file link opens it, the keyboard stays closed", async () => {
    const mp = await phone();
    const { m, sh } = mark();
    await printOnPhone(mp, `printf '%s osc \\033]8;;file://%s/${FILE}\\007here\\033]8;;\\007\\n' ${sh} "$PWD"`, m);
    await mp.evaluate(() => document.activeElement?.blur());
    await watchKeyboard(mp);
    const at = await cellOf(mp, m, "here");
    await opens(mp, () => mp.touchscreen.tap(at.x, at.y), { path: FILE });
    assert(!(await keyboardOpened(mp)), "the tap opened the keyboard");
  });

  await run("touch: a tap opens a program's web link and a web address within the touch, the keyboard stays closed", async () => {
    const mp = await phone();
    const { m, sh } = mark();
    await printOnPhone(mp, `printf '%s osc \\033]8;;https://example.invalid/osc\\007here\\033]8;;\\007\\n%s web https://example.invalid/web\\n' ${sh} ${sh}w`, `${m}w`);
    await mp.evaluate(() => document.activeElement?.blur());
    await watchKeyboard(mp);
    await recordOpens(mp);
    for (const at of [await cellOf(mp, m, "here"), await cellOf(mp, `${m}w`, "example")]) await mp.touchscreen.tap(at.x, at.y);
    await sleep(500);
    const got = await opened(mp);
    const want = [{ url: "https://example.invalid/osc", during: "click" }, { url: "https://example.invalid/web", during: "click" }];
    assert(JSON.stringify(got) === JSON.stringify(want), `opened ${JSON.stringify(got)}`);
    assert(!(await keyboardOpened(mp)), "a tap opened the keyboard");
  });

  await run("touch: a tap opens a path in the editor", async () => {
    const mp = await phone();
    const { m, sh } = mark();
    await printOnPhone(mp, `printf '%s %s\\n' ${sh} 'tap ${LINKED}'`, m);
    const at = await cellOf(mp, m, LINKED);
    await opens(mp, () => mp.touchscreen.tap(at.x, at.y), { path: FILE, line: 3, col: 7 });
  });

  for (const wrapCase of wrapCases) {
    await run(`touch: ${wrapCase.name}`, async () => pressWrapped(await phone(), true, wrapCase));
  }

  await run("touch: a path opens while a row below it is redrawn every 100ms and the answer takes 150ms", async () => opensBesideRedraw(await phone(), true));

  await run("touch: a tap on plain text toggles the keyboard, asks nothing and opens nothing", async () => {
    const mp = await phone();
    const { m, sh } = mark();
    await printOnPhone(mp, `printf '%s %s\\n' ${sh} 'plain words only'`, m);
    await mp.evaluate(() => document.activeElement?.blur());
    const watch = countAsks(mp);
    const at = await cellOf(mp, m, "words");
    await mp.touchscreen.tap(at.x, at.y);
    await sleep(600);
    assert(await keyboardOpen(mp), "the first tap left the keyboard closed");
    await mp.touchscreen.tap(at.x, at.y);
    await stays(mp, "a tap on plain text");
    watch.stop();
    assert(!(await keyboardOpen(mp)), "the second tap left the keyboard open");
    assert(watch.asks.length === 0, `the taps asked ${watch.asks.length} times`);
  });

  await run("touch: a swipe that starts on a path opens nothing, the keyboard stays closed", async () => {
    const mp = await phone();
    const { m, sh } = mark();
    await printOnPhone(mp, `printf '%s %s\\n' ${sh} 'swipe ${LINKED}'`, m);
    await mp.evaluate(() => document.activeElement?.blur());
    await watchKeyboard(mp);
    const watch = countAsks(mp);
    await L.swipeAt(mp, await cellOf(mp, m, LINKED), 200);
    await stays(mp, "a swipe from a path");
    watch.stop();
    assert(watch.asks.length === 0, `the swipe asked ${watch.asks.length} times`);
    assert(!(await keyboardOpened(mp)), "the swipe opened the keyboard");
  });

  await run("touch: a tap and a swipe on a row, the row printed again with another path, a tap opens the new one", async () => {
    const mp = await phone();
    const { m, sh } = mark();
    await printOnPhone(mp, `printf '%s %s\\n' ${sh} 'row ${LINKED}'; read -rs; printf '\\033[1A\\r\\033[2K%s %s\\n' ${sh} 'row notes.txt:1 now'`, `${m} row ${FILE}`);
    const word = await cellOf(mp, m, "row");
    await mp.touchscreen.tap(word.x, word.y);
    await sleep(400);
    await L.swipeAt(mp, await cellOf(mp, m, FILE), 120);
    await sleep(1200);
    await enter(mp);
    await waitLine(mp, `${m} row notes`);
    const at = await cellOf(mp, m, "notes.txt");
    await opens(mp, () => mp.touchscreen.tap(at.x, at.y), { path: "notes.txt", line: 1, col: 1 });
  });

  await run("touch: a tap waiting for a slow answer, then a tap on plain text, never opens the first", async () => {
    const mp = await phone();
    const { m, sh } = mark();
    await printOnPhone(mp, `printf '%s %s\\n' ${sh} 'wait notes.txt:1 plain'`, m);
    const held = await holdAnswer(mp, "/file-links", "wait notes.txt:1");
    const first = await cellOf(mp, m, "notes.txt");
    await mp.touchscreen.tap(first.x, first.y);
    await held.arrived;
    const plain = await cellOf(mp, m, "plain");
    await mp.touchscreen.tap(plain.x, plain.y);
    await sleep(500);
    await held.answer();
    await stays(mp, "the first tap");
    await held.stop();
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
