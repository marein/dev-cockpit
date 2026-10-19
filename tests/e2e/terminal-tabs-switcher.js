const L = require("./lib");
const { assert, sleep, BASE } = L;

// The open switcher across a strip refresh, run by terminal-tabs.js on its
// project and shells: twelve projects of its own make the list scroll, and a
// terminal and a project come and go behind it over the server events alone.
// The marked row keeps its mark and its place in the list, measured as its
// offset from the list's top, so a row added or dropped above it moves
// nothing. A list scrolled by the wheel away from the mark stays where it was
// and is not pulled back to the mark at the top, which is what the rebuild did
// before. A mark whose row goes moves to the row after it, the typed filter
// stays. Both on a terminal page, where the strip is the switcher, and on
// /projects, where the hidden switcher only instance serves it.

module.exports = async function terminalTabsSwitcher({ run, page, project, ids }) {
  const own = Array.from({ length: 12 }, (_, i) => `${project}-p${String(i + 1).padStart(2, "0")}`);
  const comer = `${project}-p05x`;
  const post = (path, body) => page.evaluate(async ({ path, body }) => {
    const token = document.querySelector('meta[name="csrf-token"]').content;
    const res = await fetch(path, { method: "POST", headers: { "X-CSRF-Token": token, "Content-Type": "application/x-www-form-urlencoded" }, body });
    return { status: res.status, url: res.url };
  }, { path, body });
  const editorRow = (name) => `.terminal-switcher-item[data-switcher-section="editors"][data-switcher-url^="/projects/${name}/editor"]`;
  const shellRow = (id) => `.terminal-switcher-item[data-switcher-id="${id}"]`;
  const place = () => page.evaluate(() => {
    const list = document.querySelector(".terminal-switcher-list");
    const top = list.getBoundingClientRect().top;
    const rows = [...list.querySelectorAll(".terminal-switcher-item")].filter((row) => row.getClientRects().length);
    const marked = list.querySelector(".terminal-switcher-item.selected");
    const first = rows.find((row) => row.getBoundingClientRect().bottom > top);
    const key = (row) => row && (row.dataset.switcherId || row.dataset.switcherUrl);
    return {
      mark: key(marked),
      off: marked ? Math.round(marked.getBoundingClientRect().top - top) : null,
      first: key(first),
      firstOff: first ? Math.round(first.getBoundingClientRect().top - top) : null,
      scroll: Math.round(list.scrollTop),
      height: list.clientHeight,
      filter: document.querySelector(".terminal-switcher-filter").value,
    };
  });
  const openSwitcher = async (url) => {
    await page.goto(url, { waitUntil: "domcontentloaded" });
    assert((await L.waitUpgraded(page, ["terminal-tabs"], 8000)).length === 0, "terminal-tabs not upgraded");
    if (url.includes("/shells/")) {
      await page.waitForSelector(`terminal-attach[terminal-id="${ids[0]}"] .xterm-screen canvas`, { timeout: 15000 });
      await page.click(".attach-terminal");
    }
    await sleep(300);
    for (let attempt = 0; attempt < 3; attempt += 1) {
      await page.keyboard.press("Control");
      await sleep(120);
      await page.keyboard.press("Control");
      if (await page.waitForSelector(".terminal-switcher", { state: "visible", timeout: 2500 }).catch(() => null)) break;
      await sleep(600);
    }
    await page.waitForSelector(editorRow(own[11]), { state: "attached", timeout: 8000 });
    await sleep(500);
  };
  const markRow = async (selector) => {
    const steps = await page.$$eval(".terminal-switcher-item", (rows, sel) => {
      const shown = rows.filter((row) => row.getClientRects().length);
      return shown.findIndex((row) => row.matches(sel)) - shown.findIndex((row) => row.classList.contains("selected"));
    }, selector);
    for (let i = 0; i < Math.abs(steps); i += 1) await page.keyboard.press(steps > 0 ? "ArrowDown" : "ArrowUp");
    await page.waitForSelector(`${selector}.selected`, { state: "attached", timeout: 4000 });
  };
  const wheel = async (dy) => {
    const box = await page.locator(".terminal-switcher-list").boundingBox();
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
    await page.mouse.wheel(0, dy);
    await sleep(500);
  };
  let projPath = "";
  const ghosts = [];
  const newShell = async () => {
    const made = await post("/shells/new", "project=" + encodeURIComponent(projPath));
    const ghost = new URL(made.url).pathname.split("/").pop();
    assert(ghost && !made.url.includes("/new"), `the background shell was refused: ${made.url}`);
    ghosts.push(ghost);
    return ghost;
  };
  const comeAndGo = async (check) => {
    const ghost = await newShell();
    await page.waitForSelector(shellRow(ghost), { state: "attached", timeout: 8000 });
    await check("a terminal came");
    assert((await post(`/shells/${ghost}/delete`, "")).status < 400, "the background shell delete was refused");
    await page.waitForSelector(shellRow(ghost), { state: "detached", timeout: 8000 });
    await check("the terminal went");
    assert((await post("/projects", "project_name=" + encodeURIComponent(comer))).status < 400, "the project create was refused");
    await page.waitForSelector(editorRow(comer), { state: "attached", timeout: 8000 });
    await check("a project came");
    assert((await post("/projects/delete", "project=" + encodeURIComponent(comer))).status < 400, "the project delete was refused");
    await page.waitForSelector(editorRow(comer), { state: "detached", timeout: 8000 });
    await check("the project went");
  };

  try {
    projPath = await L.projectPath(page, project);
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    for (const name of own) assert((await post("/projects", "project_name=" + encodeURIComponent(name))).status < 400, `project ${name} was refused`);

    for (const [where, url] of [["a terminal page", `${BASE}/shells/${ids[0]}`], ["a page without a strip", `${BASE}/projects`]]) {
      await run(`switcher on ${where}: a marked row deep in the list keeps its mark and its place while a terminal and a project come and go`, async () => {
        await openSwitcher(url);
        await markRow(editorRow(own[9]));
        const marked = await place();
        await wheel(Math.round(marked.off - marked.height / 2));
        const before = await place();
        assert(before.scroll > 0 && before.off > 30 && before.off < before.height - 60, `the marked row does not stand inside a scrolled list: ${JSON.stringify(before)}`);
        await comeAndGo(async (what) => {
          await sleep(400);
          const now = await place();
          assert(now.mark === before.mark, `${what}: the mark moved from ${before.mark} to ${now.mark}`);
          assert(Math.abs(now.off - before.off) <= 1, `${what}: the marked row moved from ${before.off}px to ${now.off}px in the list`);
        });
        await page.keyboard.press("Escape");
        return `held at ${before.off}px of ${before.height}`;
      });
    }

    await run("switcher: a list scrolled by the wheel away from the mark stays where it is on a refresh", async () => {
      await openSwitcher(`${BASE}/shells/${ids[0]}`);
      await wheel(400);
      const before = await place();
      assert(before.scroll > 200 && before.off < 0, `the wheel did not scroll the mark out of view: ${JSON.stringify(before)}`);
      await comeAndGo(async (what) => {
        await sleep(400);
        const now = await place();
        assert(now.mark === before.mark, `${what}: the mark moved from ${before.mark} to ${now.mark}`);
        assert(now.first === before.first && Math.abs(now.firstOff - before.firstOff) <= 1,
          `${what}: the list left the row it was scrolled to, ${before.first} at ${before.firstOff}px, now ${now.first} at ${now.firstOff}px (scroll ${before.scroll} -> ${now.scroll})`);
      });
      await page.keyboard.press("Escape");
    });

    await run("switcher: a marked row that goes hands the mark to the row after it, the filter stays", async () => {
      const ghost = await newShell();
      await openSwitcher(`${BASE}/shells/${ids[0]}`);
      await page.keyboard.type(project, { delay: 20 });
      await page.waitForSelector(shellRow(ghost), { state: "visible", timeout: 4000 });
      await markRow(shellRow(ghost));
      const next = await page.$$eval(".terminal-switcher-item", (rows, id) => {
        const shown = rows.filter((row) => row.getClientRects().length);
        const at = shown.findIndex((row) => row.dataset.switcherId === id);
        const row = shown[at + 1] || shown[at - 1];
        return row && (row.dataset.switcherId || row.dataset.switcherUrl);
      }, ghost);
      assert((await post(`/shells/${ghost}/delete`, "")).status < 400, "the background shell delete was refused");
      await page.waitForSelector(shellRow(ghost), { state: "detached", timeout: 8000 });
      await sleep(400);
      const now = await place();
      assert(now.mark === next, `the mark went to ${now.mark}, not to the neighbour ${next}`);
      assert(now.filter === project, `the filter reads '${now.filter}'`);
      await page.keyboard.press("Escape");
    });
  } finally {
    await page.keyboard.press("Escape").catch(() => {});
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" }).catch(() => {});
    for (const ghost of ghosts) await post(`/shells/${ghost}/delete`, "").catch(() => {});
    await post("/projects/delete", "project=" + encodeURIComponent(comer)).catch(() => {});
    await post("/projects/delete", own.map((name) => "project=" + encodeURIComponent(name)).join("&")).catch(() => {});
  }
};
