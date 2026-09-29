const L = require("./lib");
const { assert, sleep, BASE } = L;

// Editor change bars: the gutter (.cm-changes) that marks what the buffer
// holds against HEAD, always on where a repository is, with no switch and no
// menu entry. Green is a new line, blue a changed one, a grey tick sits on the
// boundary deleted lines vanished from, a blue bar that swallowed more HEAD
// lines than it shows carries that tick under its last line. The bars are a
// line diff computed in the browser (@dc/linediff) and follow the buffer by
// the keystroke. They rest while a diff or a comparison is up and are absent
// for a file HEAD does not hold. The buffer's lines carry no line ending and
// HEAD is split on LF only, so a CRLF file marks every line as changed.
//
// The diff is bounded the way the diff views bound theirs, counted in edited
// lines instead of characters: a region past the bound is marked as one block,
// and the same text always gives the same bars.
//
// Gotchas:
//   - the gutter keeps a hidden spacer cell for its width, reads skip it.
//   - CodeMirror renders only the lines around the viewport. readGutter maps
//     every change cell to the line number cell at the same height and scrolls
//     through the whole file, so a line by line claim covers every line.
//   - the buffer is read back through a save and the file route, the runner
//     never reaches into the editor's internals.
//   - the repository is built through a shell: the app itself never writes git.
//   - keystroke timing runs in the page, from the keydown to the frame after
//     the update, so it includes the whole synchronous work of the editor and
//     one frame. A round trip of page.keyboard.press says nothing, it returns
//     before the page handled the key.
//   - the timing bound is about the regression, not about the machine: the
//     old character diff took about 10 s per key on the rewritten block, the
//     line diff takes one frame and about 33 ms at the change limits. A second
//     is a tenth of the old cost and thirty times the new one, so a busy
//     machine does not trip it and the old behavior cannot pass it.

const KEY_TO_FRAME_MS = 1000;

let seed = 11;
const rnd = (n) => {
  seed = (Math.imul(seed, 1103515245) + 12345) >>> 0;
  return (seed >>> 16) % n;
};
const VOCAB = ("the release manager coordinates rollout schedule with team about latency budget database "
  + "migration background workers validates alert thresholds dependency graph internal libraries "
  + "pipeline change window access tokens expire within runbook owner reports quarter build cache").split(" ");
const REWRITE = ("young apple tree tends rows crisp green lettuce beneath pale autumn sky rainwater barrel watches "
  + "over earthworms deep below mulch while soil still damp patient gardener quietly shelters baskets ripe "
  + "blackberries greenhouse vent gathers frost sensitive dahlias until first hard").split(" ");
const prose = (words = VOCAB) => Array.from({ length: 10 + rnd(6) }, () => words[rnd(words.length)]).join(" ");

// expectedBars is the reference the gutter is held to: an LCS over whole
// lines, read the way the gutter reads chunks. The fixtures keep every line
// distinct, so the alignment is unique and the bars are one answer.
function expectedBars(headLines, bufLines) {
  const n = headLines.length;
  const m = bufLines.length;
  const w = m + 1;
  const t = new Int32Array((n + 1) * w);
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      t[i * w + j] = headLines[i] === bufLines[j] ? t[(i + 1) * w + j + 1] + 1 : Math.max(t[(i + 1) * w + j], t[i * w + j + 1]);
    }
  }
  const bars = new Map();
  const tick = (line, kind) => bars.set(line, bars.has(line) && bars.get(line) !== kind ? "del:top+bottom" : kind);
  let i = 0;
  let j = 0;
  while (i < n || j < m) {
    if (i < n && j < m && headLines[i] === bufLines[j]) {
      i++;
      j++;
      continue;
    }
    const fromJ = j;
    let lost = 0;
    while (i < n || j < m) {
      if (i < n && j < m && headLines[i] === bufLines[j]) break;
      if (i < n && (j >= m || t[(i + 1) * w + j] >= t[i * w + j + 1])) i++, lost++;
      else j++;
    }
    const shown = j - fromJ;
    if (shown === 0) {
      if (fromJ < m) tick(fromJ + 1, "del:top");
      else tick(m, "del:bottom");
      continue;
    }
    for (let k = fromJ; k < j; k++) {
      bars.set(k + 1, lost ? (k === j - 1 && lost > shown ? "mod+tick" : "mod") : "add");
    }
  }
  return bars;
}

L.runFeature("EDITOR CHANGE BARS", async ({ page, run }) => {
  const tag = `bars-${Date.now().toString(36)}`;
  const project = `zzbars-${tag}`;
  const editorBase = `/projects/${encodeURIComponent(project)}/editor`;
  const editorURL = `${BASE}${editorBase}`;
  let shellUrl = "";

  const tracked = "tracked.txt";
  const worded = "word.txt";
  const big = "big.txt";
  const crlf = "crlf.txt";
  const bounded = "bounded.txt";

  const bigHead = Array.from({ length: 900 }, (_, i) => `${i} ${prose()}`);
  const bigWork = [
    ...bigHead.slice(0, 50), `${bigHead[50]} edited`, ...bigHead.slice(51, 120), ...bigHead.slice(123, 300),
    ...Array.from({ length: 210 }, (_, i) => `r${i} ${prose(REWRITE)}`),
    "TYPE HERE",
    ...Array.from({ length: 210 }, (_, i) => `s${i} ${prose(REWRITE)}`),
    ...bigHead.slice(700, 800), ...Array.from({ length: 5 }, (_, i) => `n${i} ${prose()}`), ...bigHead.slice(800),
  ];
  const boundedLines = 49999;
  const token = (k) => `t${k}`.padEnd(79, ".");
  const boundedHead = Array.from({ length: boundedLines }, () => rnd(4));
  const boundedWork = boundedHead.map((k, i) => (i % 100 === 50 ? (k + 1) % 4 : k));

  const post = (path, fields) => page.evaluate(([p, f]) => {
    const csrf = document.querySelector('meta[name="csrf-token"]').content;
    return fetch(p, {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded", "X-CSRF-Token": csrf, Accept: "application/json" },
      body: new URLSearchParams(f).toString(),
    }).then((r) => r.status);
  }, [path, fields]);
  const write = async (path, content) => assert(await post(`${editorBase}/file`, { path, content }) === 200, `write ${path} failed`);
  const diskText = (path) => page.evaluate(async (url) => {
    const res = await fetch(url, { headers: { Accept: "application/json" } });
    return (await res.json()).content;
  }, `${editorBase}/file?path=${encodeURIComponent(path)}`);
  const gitChanges = () => page.evaluate((url) => fetch(url, { headers: { Accept: "application/json" } }).then((r) => r.json()),
    `${editorBase}/git/changes`);

  const diffReady = () => page.waitForFunction(() => document.querySelector("dc-editor")?.dataset.gitRepo === "1", null, { timeout: 20000 });
  const openFile = async (path) => {
    await page.goto(editorURL, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    await L.waitUpgraded(page, ["dc-editor"]);
    await diffReady();
    await page.waitForSelector(`.editor-item[data-path="${path}"]`, { timeout: 15000 });
    await page.click(`.editor-item[data-path="${path}"]`);
    await page.waitForSelector(`.editor-tab[data-path="${path}"].active`, { timeout: 15000 });
  };
  const toggleDiff = async () => {
    await page.click(".editor-tab.active", { button: "right" });
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 5000 });
    await page.locator(".dc-context-menu .dropdown-item", { hasText: /git diff/ }).first().click();
    await sleep(400);
  };
  const toLine = async (text) => {
    await page.locator(".cm-content").first().click({ force: true });
    await page.keyboard.press("Control+f");
    await page.waitForSelector(".cm-search input[name=search]", { timeout: 5000 });
    await page.fill(".cm-search input[name=search]", "");
    await page.keyboard.type(text);
    await page.keyboard.press("Enter");
    await page.keyboard.press("Escape");
    await page.keyboard.press("Home");
  };
  const saveAndRead = async (path, previous) => {
    await page.keyboard.press("Control+S");
    const deadline = Date.now() + 15000;
    let text = previous;
    while (Date.now() < deadline && text === previous) {
      await sleep(200);
      text = await diskText(path);
    }
    assert(text !== previous, `the save of ${path} never reached the disk`);
    return text;
  };

  // readBars reads the bars of the lines on screen, in order, classified by
  // the inline style the markers carry (the Tabler variable names).
  const readBars = () => page.evaluate(() => {
    const out = [];
    for (const cell of document.querySelectorAll(".cm-changes .cm-gutterElement")) {
      if (cell.style.visibility === "hidden") continue;
      for (const el of cell.children) {
        const bg = el.style.background || "";
        const ticks = [...el.children].map((c) => (c.style.top ? "top" : "bottom"));
        if (bg.includes("azure")) out.push(ticks.length ? "mod+tick" : "mod");
        else if (bg.includes("green")) out.push("add");
        else if (ticks.length) out.push(`del:${ticks.join("+")}`);
      }
    }
    return out;
  });
  const waitBars = async (want, label) => {
    const deadline = Date.now() + 15000;
    let bars = [];
    while (Date.now() < deadline) {
      bars = await readBars();
      if (JSON.stringify(bars) === JSON.stringify(want)) return;
      await sleep(250);
    }
    assert(false, `${label}: the gutter carries ${JSON.stringify(bars)}, not ${JSON.stringify(want)}`);
  };
  const visibleBars = () => page.evaluate(() => {
    const numbers = [...document.querySelectorAll(".cm-lineNumbers .cm-gutterElement")]
      .filter((c) => c.style.visibility !== "hidden" && /^\d+$/.test(c.textContent.trim()));
    const byTop = new Map(numbers.map((c) => [Math.round(c.getBoundingClientRect().top), Number(c.textContent.trim())]));
    const out = {};
    for (const cell of document.querySelectorAll(".cm-changes .cm-gutterElement")) {
      if (cell.style.visibility === "hidden") continue;
      const line = byTop.get(Math.round(cell.getBoundingClientRect().top));
      for (const el of cell.children) {
        const bg = el.style.background || "";
        const ticks = [...el.children].map((c) => (c.style.top ? "top" : "bottom"));
        let kind = "";
        if (bg.includes("azure")) kind = ticks.length ? "mod+tick" : "mod";
        else if (bg.includes("green")) kind = "add";
        else if (ticks.length) kind = `del:${ticks.join("+")}`;
        if (kind && line) out[line] = kind;
      }
    }
    return { out, first: numbers.length ? Number(numbers[0].textContent) : 0, last: numbers.length ? Number(numbers[numbers.length - 1].textContent) : 0 };
  });
  const readGutter = async (lines) => {
    const bars = new Map();
    const seen = new Set();
    const scroller = ".cm-editor:not([style*='hidden']) .cm-scroller";
    await page.$eval(scroller, (el) => { el.scrollTop = 0; });
    for (;;) {
      await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));
      const { out, first, last } = await visibleBars();
      for (let n = first; n <= last; n++) seen.add(n);
      for (const [line, kind] of Object.entries(out)) bars.set(Number(line), kind);
      if (last >= lines) break;
      await page.$eval(scroller, (el) => { el.scrollTop += el.clientHeight * 0.8; });
    }
    for (let n = 1; n <= lines; n++) assert(seen.has(n), `line ${n} was never on screen while reading the gutter`);
    return bars;
  };
  const sameBars = (got, want, label) => {
    const lines = new Set([...got.keys(), ...want.keys()]);
    const wrong = [...lines].sort((x, y) => x - y).filter((n) => got.get(n) !== want.get(n))
      .map((n) => `${n}: ${got.get(n) || "none"} instead of ${want.get(n) || "none"}`);
    assert(!wrong.length, `${label}: ${wrong.length} lines differ, ${wrong.slice(0, 6).join("; ")}`);
  };
  const barsShown = (timeout) => page.waitForFunction(() => [...document.querySelectorAll(".cm-changes .cm-gutterElement")]
    .some((c) => c.style.visibility !== "hidden" && c.children.length), null, { timeout });
  const typeTimed = async (keys) => {
    await page.evaluate(() => {
      if (window.__barsKeyAt) return;
      window.__barsKeyAt = 0;
      document.addEventListener("keydown", () => { window.__barsKeyAt = performance.now(); }, true);
    });
    const times = [];
    for (const key of keys) {
      await page.keyboard.press(key);
      times.push(await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => setTimeout(() => r(Math.round(performance.now() - window.__barsKeyAt)), 0)))));
    }
    times.sort((x, y) => x - y);
    return { median: times[times.length >> 1], max: times[times.length - 1] };
  };
  const keysOf = (text) => [...text].map((c) => (c === " " ? "Space" : c));

  try {
    await L.createProject(page, project);
    await L.setEditorFiles(page, { autosave: false });
    await page.goto(editorURL, { waitUntil: "domcontentloaded" });
    await L.waitUpgraded(page, ["dc-editor"]);
    await write(tracked, "one\n");
    await write(worded, "alpha\nbeta gamma delta\nepsilon\n");
    await write(big, `${bigHead.join("\n")}\n`);
    await write(crlf, "alpha\r\nbeta\r\ngamma\r\n");
    await write(bounded, `${boundedHead.map(token).join("\n")}\n`);

    shellUrl = await L.createShell(page, project);
    await page.waitForSelector("#terminal .xterm-screen canvas", { timeout: 15000 });
    await sleep(2000);
    await page.evaluate((href) => {
      const csrf = document.querySelector('meta[name="csrf-token"]').content;
      const command = "git -c core.autocrlf=false init -q && git -c core.autocrlf=false add -A && "
        + "git -c user.email=e2e@example.com -c user.name=e2e -c commit.gpgsign=false commit -qm init\r";
      return fetch(href + "/input", {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf },
        body: JSON.stringify({ items: [{ raw: command }] }),
      });
    }, new URL(shellUrl).pathname);
    await page.goto(editorURL, { waitUntil: "domcontentloaded" });
    const deadline = Date.now() + 45000;
    let changes = null;
    while (Date.now() < deadline) {
      changes = await gitChanges();
      if (changes.repo && changes.worktree.length === 0) break;
      await sleep(1000);
    }
    assert(changes && changes.repo && changes.worktree.length === 0, `the commit did not land: ${JSON.stringify(changes)}`);

    await write(tracked, "one\ntwo\n");
    await write(big, `${bigWork.join("\n")}\n`);
    await write(bounded, `${boundedWork.map(token).join("\n")}\n`);
    await write("fresh.txt", "new\n");

    await run("always on, following the buffer live, resting under a comparison", async () => {
      await openFile(tracked);
      await waitBars(["add"], "after opening");
      await page.locator(".cm-content").first().click({ force: true });
      await page.keyboard.press("Control+Home");
      await page.keyboard.press("End");
      await page.keyboard.type("X");
      await waitBars(["mod", "mod"], "after typing");
      await page.keyboard.press("Control+z");
      await waitBars(["add"], "after undo");
      await toggleDiff();
      await page.waitForSelector(".cm-mergeView", { timeout: 20000 });
      assert((await page.locator(".cm-mergeView .cm-changes").count()) === 0, "the merge view carries the change bars");
      await toggleDiff();
      await page.waitForSelector(".cm-mergeView", { state: "detached", timeout: 10000 });
      await waitBars(["add"], "after the diff went away");
      return "green for the new line, live to the keystroke, resting under the diff";
    });

    await run("a clean file shows none, a deleted line leaves a tick on the boundary", async () => {
      await openFile(worded);
      await waitBars([], "on a committed unchanged file");
      await page.locator(".cm-content").first().click({ force: true });
      await page.keyboard.press("Control+Home");
      await page.keyboard.press("ArrowDown");
      await page.keyboard.press("Home");
      await page.keyboard.down("Shift");
      await page.keyboard.press("ArrowDown");
      await page.keyboard.up("Shift");
      await page.keyboard.press("Delete");
      await waitBars(["del:top"], "after deleting the middle line");
      await page.keyboard.press("Control+z");
      await waitBars([], "after undo");
      return "nothing on a clean file, a grey tick where the line vanished";
    });

    await run("a file HEAD does not hold shows no gutter at all", async () => {
      await openFile("fresh.txt");
      await sleep(1500);
      assert((await page.locator(".cm-changes").count()) === 0, "an untracked file carries a changes gutter");
      return "untracked means nothing to compare, no gutter";
    });

    await run("a large rewritten block: every line exact, and typing in it stays fast", async () => {
      await openFile(big);
      await barsShown(20000);
      let text = `${bigWork.join("\n")}\n`;
      const check = async (label) => {
        const lines = text.split("\n");
        sameBars(await readGutter(lines.length), expectedBars([...bigHead, ""], lines), label);
      };
      await check("after opening");
      await toLine("TYPE HERE");
      await page.keyboard.press("End");
      const typed = await typeTimed(["Enter", ...keysOf("typed line one"), "Enter", ...keysOf("typed line two")]);
      assert(typed.median < KEY_TO_FRAME_MS, `typing in the rewritten block takes ${typed.median} ms per key (max ${typed.max})`);
      text = await saveAndRead(big, text);
      assert(text.includes("\nTYPE HERE\ntyped line one\ntyped line two\n"), "the typing did not land in the rewritten block");
      await check("after typing");
      await toLine("r100 ");
      await page.keyboard.down("Shift");
      for (let k = 0; k < 41; k++) await page.keyboard.press("ArrowDown");
      await page.keyboard.up("Shift");
      await page.keyboard.press("Delete");
      const lines41 = text.split("\n");
      text = await saveAndRead(big, text);
      const at100 = lines41.findIndex((l) => l.startsWith("r100 "));
      assert(text === [...lines41.slice(0, at100), ...lines41.slice(at100 + 41)].join("\n"), "the 41 deleted lines are not r100 onwards");
      await check("after deleting 41 lines");
      await toLine(bigHead[60]);
      await page.keyboard.down("Shift");
      for (let k = 0; k < 3; k++) await page.keyboard.press("ArrowDown");
      await page.keyboard.up("Shift");
      await page.keyboard.press("Delete");
      const lines3 = text.split("\n");
      text = await saveAndRead(big, text);
      const at60 = lines3.indexOf(bigHead[60]);
      assert(text === [...lines3.slice(0, at60), ...lines3.slice(at60 + 3)].join("\n"), "the 3 deleted lines are not the committed lines from line 61");
      await check("after deleting 3 committed lines");
      await page.keyboard.press("Control+z");
      await page.keyboard.press("Control+z");
      text = await saveAndRead(big, text);
      await check("after undo");
      return `${bigWork.length} lines checked each round, ${typed.median} ms per key (max ${typed.max})`;
    });

    await run("CRLF: a freshly opened, unchanged CRLF file marks every line as changed", async () => {
      await openFile(crlf);
      await waitBars(["mod", "mod", "mod"], "an unchanged CRLF file");
      sameBars(await readGutter(4), new Map([[1, "mod"], [2, "mod"], [3, "mod"]]), "the CRLF file");
      return "all three lines blue, the empty last line unmarked";
    });

    await run("past the scan limit: one stable block, the same text gives the same bars, typing stays fast", async () => {
      await openFile(bounded);
      await barsShown(30000);
      const sample = async () => {
        const got = [];
        for (const at of [0, 0.5, 1]) {
          await page.$eval(".cm-editor:not([style*='hidden']) .cm-scroller", (el, f) => { el.scrollTop = (el.scrollHeight - el.clientHeight) * f; }, at);
          await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));
          got.push(await visibleBars());
        }
        return got;
      };
      const firstMarked = 51;
      const lastMarked = boundedLines - 48;
      const before = await sample();
      for (const { out, first, last } of before) {
        for (let n = first; n <= last; n++) {
          const want = n >= firstMarked && n <= lastMarked ? "mod" : undefined;
          assert(out[n] === want, `line ${n} carries ${out[n] || "none"}, the block past the limit spans ${firstMarked} to ${lastMarked}`);
        }
      }
      await page.locator(".cm-content").first().click({ force: true });
      const typed = await typeTimed(["x", "y", "z", "Backspace", "Backspace", "Backspace", "x", "Backspace", "x", "Backspace"]);
      assert(typed.median < KEY_TO_FRAME_MS, `typing past the scan limit takes ${typed.median} ms per key (max ${typed.max})`);
      const after = await sample();
      before.forEach((was, k) => {
        const now = after[k];
        const lo = Math.max(was.first, now.first);
        const hi = Math.min(was.last, now.last);
        assert(hi - lo > 10, `the reads ${k} share only lines ${lo} to ${hi}`);
        for (let n = lo; n <= hi; n++) assert(was.out[n] === now.out[n], `line ${n} carries ${now.out[n] || "none"} for the same text, ${was.out[n] || "none"} before`);
      });
      return `${boundedLines} lines of ${token(0).length + 1} bytes, one block, ${typed.median} ms per key (max ${typed.max})`;
    });
  } finally {
    if (shellUrl) await L.deleteShell(page, shellUrl).catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
  }
});
