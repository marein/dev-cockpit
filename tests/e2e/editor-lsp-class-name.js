const L = require("./lib");
const { assert, sleep, BASE } = L;

// Copying a PHP class name in the editor, offered on type names alone: the
// word the pointer rests on is asked over the language server's hover
// (`POST /projects/:name/editor/lsp/class-name`) before any click, and a
// right click on a name the server answered as a class, interface, trait or
// enum opens the context menu with "Copy <FQCN>" ready, for the declaration,
// an import, an alias in its use statement and where it is used, a type
// hint, new, a static call, Bar::class, a docblock type, an enum and a class
// without a namespace. On anything else (variable, method, function,
// property, constant, enum case, keyword, string, comment, unknown class)
// the cockpit claims nothing and the browser's own menu shows; the same
// for a selection, a Go file and PHP switched off. While the server indexes
// or after it failed nothing is claimed either, and once the index stands
// the name is offered. On touch the cursor pill carries Copy FQCN beside
// Look up only on a type name, and the tap copies.
// Instance: the editor-lsp.js one, the fake language server first on PATH as
// `gopls` and as `intelephense` (tests/e2e/fake-lsp.py, whose hover contract
// stands in its header), plus PLAIN_URL: the same instance over plain http
// under a name that is not localhost, so the page has no secure context and
// no clipboard API, the way a phone reaches it in the LAN (see README.md).
// Gotchas: the copy is read off a recorder installed before the page loads,
// which wraps the clipboard API and execCommand, because a page without a
// secure origin cannot read the clipboard back; execCommand only copies
// inside a user gesture, so its success is the proof the copy ran in the
// click. The browser's own menu is invisible to the runner, an unclaimed
// right click shows as a contextmenu event nobody prevented. A right click
// only finds what the pointer's lookup answered, so every check rests the
// pointer and waits for the class name requests to go quiet first.

const PLAIN = (process.env.PLAIN_URL || "").replace(/\/$/, "");

function watchMenus() {
  document.addEventListener("contextmenu", (e) => { window.__menuClaimed = e.defaultPrevented; });
}

async function recordCopies(context) {
  await context.addInitScript(L.recordCopies);
  await context.addInitScript(watchMenus);
}

// The class name requests a page has out, and when the last one moved.
const lookups = new WeakMap();
function trackLookups(p) {
  const st = { out: 0, total: 0, at: 0 };
  const mine = (r) => r.url().includes("/editor/lsp/class-name");
  p.on("request", (r) => { if (mine(r)) { st.out += 1; st.total += 1; st.at = Date.now(); } });
  const end = (r) => { if (mine(r)) { st.out -= 1; st.at = Date.now(); } };
  p.on("requestfinished", end);
  p.on("requestfailed", end);
  lookups.set(p, st);
  return p;
}
// Quiet means nothing out for 300ms, measured from the gesture at the
// earliest: the request of a pointer move starts within the move's own task.
async function settled(p) {
  const st = lookups.get(p);
  st.at = Math.max(st.at, Date.now());
  const deadline = Date.now() + 20000;
  while (st.out > 0 || Date.now() - st.at < 300) {
    if (Date.now() > deadline) throw new Error(`the class name lookups never went quiet (${st.out} out)`);
    await sleep(50);
  }
  return st.total;
}

const TYPES = [
  ["src/Foo/Bar.php", "class Bar", "Bar", "Foo\\Bar", "the declaration"],
  ["src/App/User.php", "use Foo\\Bar;", "Bar", "Foo\\Bar", "an import"],
  ["src/App/User.php", "use Foo\\Bar as Baz;", "Baz", "Foo\\Bar", "an alias in its use statement"],
  ["src/App/User.php", "$z = new Baz", "Baz", "Foo\\Bar", "an alias where it is used"],
  ["src/App/User.php", "function typed(Bar", "Bar", "Foo\\Bar", "a type hint"],
  ["src/App/User.php", "$b = new Bar", "Bar", "Foo\\Bar", "new"],
  ["src/App/User.php", "$m = Bar::make", "Bar", "Foo\\Bar", "a static call"],
  ["src/App/User.php", "$k = Bar::class", "Bar", "Foo\\Bar", "Bar::class"],
  ["src/App/User.php", "@param Bar", "Bar", "Foo\\Bar", "a docblock type"],
  ["src/App/User.php", "$h = Suit::Hearts", "Suit", "Foo\\Suit", "an enum"],
  ["global.php", "new GlobalThing", "GlobalThing", "GlobalThing", "a class without a namespace"],
];
const OTHERS = [
  ["$b = new Bar", "$b", "a variable"],
  ["$m = Bar::make", "make", "a method"],
  ["helper();", "helper", "a function"],
  ["$n = $b->name", "name", "a property"],
  ["$c = Bar::X", "X", "a constant"],
  ["$h = Suit::Hearts", "Hearts", "an enum case"],
  ["$e = new Missing", "new", "a keyword"],
  ["$s = 'Bar'", "Bar", "a string"],
  ["// Bar in a comment", "Bar", "a comment"],
  ["$e = new Missing", "Missing", "an unknown class"],
];

L.runFeature("EDITOR-LSP-CLASS-NAME", async ({ engine, browser, ctx, page, run, mobilePage }) => {
  const tag = `fq-${engine}-${Date.now().toString(36)}`;
  const project = `zzfqcn-${tag}`;
  const held = `zzfqcnh-${tag}`;
  const broken = `zzfqcnx-${tag}`;
  const editorOf = (root, name) => `${root}/projects/${name}/editor`;
  const base = editorOf(BASE, project);
  await recordCopies(ctx);
  trackLookups(page);

  const post = (name, path, body) => page.evaluate(async ({ url, body }) => {
    const token = document.querySelector('meta[name="csrf-token"]').content;
    const res = await fetch(url, {
      method: "POST",
      headers: { "X-CSRF-Token": token, "Content-Type": "application/x-www-form-urlencoded" },
      body: new URLSearchParams(body).toString(),
    });
    if (!res.ok) throw new Error(`${url} -> ${res.status}`);
  }, { url: `${editorOf(BASE, name)}${path}`, body });

  const writeFiles = async (name, files) => {
    for (const dir of ["src", "src/Foo", "src/App"]) await post(name, "/mkdir", { path: dir });
    for (const [path, content] of files) {
      await post(name, "/create", { path });
      await post(name, "/file", { path, content });
    }
  };

  const openEditor = async (p, url) => {
    await p.goto(url, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(p);
  };
  // A folder row says nothing about being open, its child row standing does.
  const openFile = async (p, path, needle, { tap = false } = {}) => {
    if ((await p.evaluate(() => document.querySelector('.editor-tab[aria-selected="true"]')?.dataset.path || "")) !== path) {
      const parts = path.split("/");
      for (let i = 0; i < parts.length; i += 1) {
        const row = `.editor-item[data-path="${parts.slice(0, i + 1).join("/")}"]`;
        await p.waitForSelector(row, { timeout: 10000 });
        if (i < parts.length - 1 && (await p.$(`.editor-item[data-path="${parts.slice(0, i + 2).join("/")}"]`))) continue;
        if (tap) await p.tap(row);
        else await p.click(row);
      }
      await p.waitForFunction((want) => document.querySelector('.editor-tab[aria-selected="true"]')?.dataset.path === want, path, { timeout: 10000 });
    }
    await p.waitForFunction((n) => [...document.querySelectorAll(".cm-line")].some((l) => l.textContent.includes(n)), needle, { timeout: 10000 });
  };

  const restOn = async (p, needle, word) => {
    const pt = await L.wordPoint(p, needle, word);
    assert(pt, `word ${word} on the surface`);
    await p.mouse.move(pt.x, pt.y);
    await settled(p);
    return pt;
  };
  const rightClick = async (p, pt) => {
    await p.evaluate(() => { window.__copies = []; window.__menuClaimed = undefined; });
    await p.mouse.click(pt.x, pt.y, { button: "right" });
  };
  const menuRow = (p) => p.evaluate(() => {
    const row = document.querySelector(".dc-context-menu .dropdown-item");
    if (!row) return null;
    return { text: row.textContent.replace(/\s+/g, " ").trim(), disabled: row.disabled, visible: row.getClientRects().length > 0 };
  });
  const menuGone = (p) => p.waitForFunction(() => !document.querySelector(".dc-context-menu"), null, { timeout: 5000 });
  const unclaimed = async (p, what) => {
    await sleep(300);
    const state = await p.evaluate(() => ({ claimed: window.__menuClaimed, menu: !!document.querySelector(".dc-context-menu") }));
    assert(state.claimed === false && !state.menu, `${what} keeps the browser's menu: ${JSON.stringify(state)}`);
  };
  const copied = async (p) => {
    await p.waitForFunction(() => (window.__copies || []).some((c) => c.ok), null, { timeout: 5000 });
    return p.evaluate(() => window.__copies.filter((c) => c.ok).pop());
  };

  const copyFrom = async (p, needle, word, want) => {
    const pt = await restOn(p, needle, word);
    await rightClick(p, pt);
    await p.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
    const row = await menuRow(p);
    assert(row.text === `Copy ${want}` && !row.disabled && row.visible, `entry for ${want}, got ${JSON.stringify(row)}`);
    const label = await L.splitLabel(p.locator(".dc-context-menu .dropdown-item"));
    assert(Math.abs(label.gap) <= 1 && (!label.head.endsWith(" ") || label.space >= 2), `the entry reads "Copy ${want}" on screen: ${JSON.stringify(label)}`);
    assert(await p.evaluate(() => window.__menuClaimed === true), "the right click on a type is claimed");
    await p.click(".dc-context-menu .dropdown-item");
    const copy = await copied(p);
    assert(copy.text === want, `copied ${JSON.stringify(copy)}, want ${want}`);
    await p.waitForFunction((t) => (document.querySelector("[data-editor-status]")?.textContent || "") === `Copied ${t}`, want, { timeout: 5000 });
    await menuGone(p);
    return `${copy.via}: ${copy.text}`;
  };

  const lspSelects = () => page.$$eval("#settings-editor-lsp select", (els) => Object.fromEntries(els.map((sel) => [sel.name, sel.value])));
  const saveLSPSettings = async (picks) => {
    await page.goto(`${BASE}/settings/editor/lsp`, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    for (const [name, value] of Object.entries(picks)) await page.selectOption(`select[name="${name}"]`, value);
    await Promise.all([
      page.waitForResponse((r) => r.url().includes("/settings/editor/lsp") && r.request().method() === "POST", { timeout: 8000 }),
      page.click('#settings-editor-lsp button[type="submit"]'),
    ]);
    await page.waitForFunction(() => document.body.textContent.includes("Settings saved."), null, { timeout: 8000 });
  };

  const barFile = ["src/Foo/Bar.php", "<?php\n\nnamespace Foo;\n\nclass Bar\n{\n    public const X = 1;\n\n    public string $name = '';\n\n    public static function make(): static\n    {\n        return new static();\n    }\n}\n"];
  let found = null;

  await run("setup: PHP and Go pinned to the Docker pick, a project with every kind of name", async () => {
    await page.goto(`${BASE}/settings/editor/lsp`, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    found = await lspSelects();
    await saveLSPSettings({ server_php: "intelephense-docker", server_go: "gopls-docker" });
    await L.createProject(page, project);
    await L.setEditorFiles(page, { autosave: false });
    await page.waitForFunction(() => document.body.textContent.includes("Settings saved."), null, { timeout: 8000 });
    await writeFiles(project, [
      ["composer.json", "{}\n"],
      barFile,
      ["src/Foo/Suit.php", "<?php\n\nnamespace Foo;\n\nenum Suit: string\n{\n    case Hearts = 'h';\n}\n"],
      ["src/App/User.php", [
        "<?php", "", "namespace App;", "", "use Foo\\Bar;", "use Foo\\Bar as Baz;", "use Foo\\Suit;", "",
        "function helper(): void", "{", "}", "",
        "/** @param Bar $p */", "function typed(Bar $p): Baz", "{",
        "    $b = new Bar();", "    $z = new Baz();", "    $m = Bar::make();", "    $k = Bar::class;", "    $c = Bar::X;",
        "    $h = Suit::Hearts;", "    $n = $b->name;", "    $s = 'Bar';", "    // Bar in a comment", "    $e = new Missing();",
        "    helper();", "    return $z;", "}", "",
      ].join("\n")],
      ["global.php", "<?php\n\nclass GlobalThing\n{\n}\n\n$g = new GlobalThing();\n"],
      ["lib.go", "package lib\n\nfunc Target() {}\n"],
    ]);
    await openEditor(page, base);
    await page.waitForSelector('.editor-item[data-path="global.php"]', { timeout: 10000 });
    const names = await page.evaluate(() => document.querySelector("dc-editor").dataset.editorLspNames);
    assert(names === "php", `the page names php as the language with class names, got "${names}"`);
    return `found ${JSON.stringify(found)}`;
  });

  await run("a right click on a type name copies its fully qualified name, wherever the name stands", async () => {
    const done = [];
    for (const [path, needle, word, want, where] of TYPES) {
      await openFile(page, path, needle);
      try {
        done.push(`${where}: ${await copyFrom(page, needle, word, want)}`);
      } catch (err) {
        throw new Error(`${where}: ${err.message}`);
      }
    }
    return done.join("; ");
  });

  await run("a right click on anything that is no type claims nothing, the browser's menu shows", async () => {
    await openFile(page, "src/App/User.php", "$b = new Bar");
    for (const [needle, word, what] of OTHERS) {
      const before = lookups.get(page).total;
      const pt = await restOn(page, needle, word);
      await rightClick(page, pt);
      await unclaimed(page, what);
      assert(lookups.get(page).total > before, `${what} was asked, so the answer is the server's`);
    }
    return OTHERS.map((o) => o[2]).join(", ");
  });

  await run("a right click into a selection and off a word stay the browser's own", async () => {
    const pt = await restOn(page, "$b = new Bar", "Bar");
    await page.mouse.dblclick(pt.x, pt.y);
    await page.waitForFunction(() => String(getSelection()).includes("Bar"), null, { timeout: 5000 });
    await rightClick(page, pt);
    await unclaimed(page, "a selection over a type");
    await page.mouse.click(pt.x, pt.y);
    const blank = await page.evaluate(() => {
      const line = [...document.querySelectorAll(".cm-line")].find((l) => l.textContent === "");
      const rect = line.getBoundingClientRect();
      return { x: rect.left + 40, y: rect.top + rect.height / 2 };
    });
    await page.mouse.move(blank.x, blank.y);
    await settled(page);
    await rightClick(page, blank);
    await unclaimed(page, "a spot off a word");
  });

  await run("a Go file asks nothing and keeps the browser's menu", async () => {
    await openFile(page, "lib.go", "func Target");
    const before = lookups.get(page).total;
    const pt = await restOn(page, "func Target", "Target");
    await rightClick(page, pt);
    await unclaimed(page, "a Go symbol");
    assert(lookups.get(page).total === before, "no class name request for Go");
  });

  await run("an edit drops the answers and the name is asked again", async () => {
    await openFile(page, "src/App/User.php", "$b = new Bar");
    const pt = await restOn(page, "$b = new Bar", "Bar");
    const before = lookups.get(page).total;
    await page.mouse.click(pt.x, pt.y);
    await page.keyboard.press("End");
    await page.keyboard.type(" ");
    await page.mouse.move(pt.x + 1, pt.y);
    const after = await settled(page);
    assert(after > before, "the edited document is asked anew");
    await page.keyboard.press("Backspace");
    return copyFrom(page, "$b = new Bar", "Bar", "Foo\\Bar");
  });

  await run("while the index is not ready nothing is claimed, then the name is offered", async () => {
    await L.createProject(page, held);
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await writeFiles(held, [["composer.json", "{}\n"], [".fake-lsp-hold", "hold\n"], barFile]);
    await openEditor(page, editorOf(BASE, held));
    await page.waitForSelector('.editor-item[data-path="src"]', { timeout: 10000 });
    await openFile(page, barFile[0], "class Bar");
    const pt = await L.wordPoint(page, "class Bar", "Bar");
    await page.mouse.move(pt.x, pt.y);
    await sleep(1500);
    assert(lookups.get(page).out === 1, "the lookup waits for the index");
    const indexing = await page.evaluate(() => document.querySelector("[data-editor-lsp-index]").getClientRects().length > 0);
    assert(indexing, "the statusbar shows the index running");
    await rightClick(page, pt);
    await unclaimed(page, "a type while the index runs");

    await post(held, "/delete", { path: ".fake-lsp-hold" });
    await settled(page);
    return copyFrom(page, "class Bar", "Bar", "Foo\\Bar");
  });

  await run("after the server failed nothing is claimed, no broken entry", async () => {
    await L.createProject(page, broken);
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await writeFiles(broken, [["composer.json", "{}\n"], ["src/Foo/Bar.php", "<?php\n\nnamespace Foo;\n\nclass Bar extends Crash\n{\n}\n"]]);
    await openEditor(page, editorOf(BASE, broken));
    await page.waitForSelector('.editor-item[data-path="src"]', { timeout: 10000 });
    await openFile(page, "src/Foo/Bar.php", "class Bar");
    await restOn(page, "extends Crash", "Crash");
    const pt = await restOn(page, "class Bar", "Bar");
    await rightClick(page, pt);
    await unclaimed(page, "a type on a failed server");
  });

  // ---- touch ----------------------------------------------------------------

  const mOpenDrawer = async (mp) => {
    await mp.waitForSelector("[data-editor-drawer-toggle]", { timeout: 10000 });
    await mp.waitForSelector(".editor.editor-drawer-open", { timeout: 4000 }).catch(() => {});
    if (!(await mp.evaluate(() => !!document.querySelector(".editor.editor-drawer-open")))) {
      await mp.tap("[data-editor-drawer-toggle]");
      await mp.waitForSelector(".editor.editor-drawer-open", { timeout: 6000 });
    }
  };
  const mOpen = async (mp, url, path, needle) => {
    await openEditor(mp, url);
    await mOpenDrawer(mp);
    await openFile(mp, path, needle, { tap: true });
  };
  const pillLabels = (mp) => mp.$$eval("[data-editor-lsp-pill] button", (els) => els.map((b) => b.textContent.trim()).join(","));
  const tapWord = async (mp, needle, word) => {
    const pt = await L.wordPoint(mp, needle, word);
    assert(pt, `word ${word} on the phone surface`);
    await mp.waitForFunction(({ x, y }) => !!document.elementFromPoint(x, y)?.closest(".cm-content"), pt, { timeout: 5000 });
    await mp.touchscreen.tap(pt.x, pt.y);
    await mp.waitForSelector("[data-editor-lsp-pill]", { state: "visible", timeout: 8000 });
  };
  const pillCopy = async (mp, needle, word, want) => {
    await tapWord(mp, needle, word);
    await mp.waitForSelector("[data-pill-name]", { state: "visible", timeout: 15000 });
    assert((await pillLabels(mp)) === "Look up,Copy FQCN", `the pill offers the copy beside the lookup: ${await pillLabels(mp)}`);
    await mp.evaluate(() => { window.__copies = []; });
    await mp.tap("[data-pill-name]");
    const copy = await copied(mp);
    assert(copy.text === want, `copied ${JSON.stringify(copy)}`);
    await mp.waitForFunction(() => !document.querySelector("[data-editor-lsp-pill]"), null, { timeout: 5000 });
    return `${copy.via}: ${copy.text}`;
  };
  const mobile = async () => {
    const mp = await mobilePage();
    if (!lookups.has(mp)) {
      await recordCopies(mp.context());
      trackLookups(mp);
    }
    return mp;
  };

  await run("mobile: on a type name the pill's Copy FQCN copies the class an alias stands for", async () => {
    const mp = await mobile();
    await mOpen(mp, base, "src/App/User.php", "$z = new Baz");
    return pillCopy(mp, "$z = new Baz", "Baz", "Foo\\Bar");
  });

  await run("mobile: on anything else the pill carries Look up alone", async () => {
    const mp = await mobile();
    const seen = [];
    for (const [needle, word, what] of [["$m = Bar::make", "make", "a method"], ["$c = Bar::X", "X", "a constant"], ["$e = new Missing", "Missing", "an unknown class"]]) {
      await tapWord(mp, needle, word);
      await settled(mp);
      const labels = await pillLabels(mp);
      assert(labels === "Look up", `${what}: ${labels}`);
      seen.push(what);
    }
    await mOpen(mp, base, "lib.go", "func Target");
    await tapWord(mp, "func Target", "Target");
    await settled(mp);
    assert((await pillLabels(mp)) === "Look up", "a Go symbol carries Look up alone");
    return `${seen.join(", ")}, a Go symbol`;
  });

  // ---- plain http -------------------------------------------------------------

  // One navigation per page: WebKit fails a second top level load of a plain
  // http name now and then, so the login itself leads into the editor.
  const plainEditor = async (p) => {
    const editor = `/projects/${project}/editor`;
    await p.goto(`${PLAIN}/login?next=${encodeURIComponent(editor)}`, { waitUntil: "domcontentloaded" });
    await p.fill('input[name="username"]', "admin");
    await p.fill('input[name="password"]', "password");
    await Promise.all([p.waitForURL((url) => url.pathname === editor, { timeout: 15000 }), p.click('button[type="submit"]')]);
    await L.dismissUpdate(p);
  };
  const insecure = (p) => p.evaluate(() => ({ secure: window.isSecureContext, clipboard: !!navigator.clipboard }));

  await run("plain http: without a secure origin the copy still lands, inside the click", async () => {
    assert(PLAIN.startsWith("http://"), "PLAIN_URL names the instance over plain http, see the header");
    const plainCtx = await browser.newContext({ viewport: { width: 1360, height: 900 } });
    try {
      await recordCopies(plainCtx);
      const p = trackLookups(await plainCtx.newPage());
      await plainEditor(p);
      const origin = await insecure(p);
      assert(!origin.secure && !origin.clipboard, `the page has no clipboard API: ${JSON.stringify(origin)}`);
      await p.waitForSelector('.editor-item[data-path="src"]', { timeout: 10000 });
      await openFile(p, "src/App/User.php", "$b = new Bar");
      const done = await copyFrom(p, "$b = new Bar", "Bar", "Foo\\Bar");
      assert(done.startsWith("execCommand"), `the fallback did the copy: ${done}`);
      return done;
    } finally {
      await plainCtx.close();
    }
  });

  await run("plain http, mobile: the pill's copy lands without a clipboard API", async () => {
    assert(PLAIN.startsWith("http://"), "PLAIN_URL names the instance over plain http, see the header");
    const plainCtx = await L.newMobile(browser);
    try {
      await recordCopies(plainCtx);
      const mp = trackLookups(await plainCtx.newPage());
      await plainEditor(mp);
      await mOpenDrawer(mp);
      await openFile(mp, barFile[0], "class Bar", { tap: true });
      const origin = await insecure(mp);
      assert(!origin.secure && !origin.clipboard, `the page has no clipboard API: ${JSON.stringify(origin)}`);
      const done = await pillCopy(mp, "class Bar", "Bar", "Foo\\Bar");
      assert(done.startsWith("execCommand"), `the fallback did the copy: ${done}`);
      return done;
    } finally {
      await plainCtx.close();
    }
  });

  // ---- settings ---------------------------------------------------------------

  await run("settings: PHP switched off asks nothing, the right click stays the browser's", async () => {
    await saveLSPSettings({ server_php: "off" });
    await openEditor(page, base);
    const names = await page.evaluate(() => document.querySelector("dc-editor").dataset.editorLspNames);
    assert(names === "", `no language names classes, got "${names}"`);
    await page.waitForSelector('.editor-item[data-path="src"]', { timeout: 10000 });
    await openFile(page, barFile[0], "class Bar");
    const before = lookups.get(page).total;
    const pt = await restOn(page, "class Bar", "Bar");
    await rightClick(page, pt);
    await unclaimed(page, "PHP switched off");
    assert(lookups.get(page).total === before, "no class name request with PHP off");
  });

  await run("cleanup", async () => {
    await (await mobilePage()).goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    for (const name of [project, held, broken]) await L.deleteProject(page, name);
    if (found) await saveLSPSettings({ server_php: found.server_php, server_go: found.server_go });
  });
});
