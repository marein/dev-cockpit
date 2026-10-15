const fs = require("fs");
const { execFileSync } = require("child_process");
const L = require("./lib");
const { assert, sleep, BASE } = L;

// Copy path with its chip "absolute" in the project editor's menus: the tree
// row of a file and of a folder, the file's tab, the tab of a file at a
// revision and the line number menu's Copy path:line. The row copies the path
// inside the project as before, the chip on the same row copies the path on
// the host, the project's own directory in front, for a linked worktree its
// own folder. The chip is a second button beside the row in a .dc-menu-split
// (@dc/contextmenu, item.chip): arrow right from the row reaches it, arrow
// left goes back, up and down walk the rows only, Enter on it copies, and a
// click or a tap on it copies the absolute path alone. On a phone the row and
// the chip are two tap targets side by side. A row without a chip stays a
// direct child of the menu.
// Instance: plain http under a name that is not localhost (BASE_URL), so the
// page has no secure context and no clipboard API, the way a phone reaches it
// in the LAN, and the copy has to run inside the click (copyText in @dc/dom).
// Its --projects-dir is $AUX_DIR/projects, mounted at the same host path: the
// runner builds a repository with a linked worktree there with the
// container's git and removes both at the end. SHOTS_DIR with a mount takes
// the desktop and the 390px screenshots of the menu.
// Gotchas: the copies are read off L.recordCopies, a page without a secure
// origin cannot read the clipboard back. The files are plain text, so the
// instance starts no language server for them.

const AUX = (process.env.AUX_DIR || "/tmp/dccpa").replace(/\/$/, "");
const SHOTS = process.env.SHOTS_DIR || "";
const FILE = "src/Game/Board.txt";
const FOLDER = "src/Game";

const git = (cwd, ...args) => execFileSync("git", ["-c", "user.name=e2e", "-c", "user.email=e2e@example.invalid", ...args], { cwd, timeout: 20000, stdio: "pipe" });
const pattern = (label) => new RegExp(`^${label.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}$`);

const split = (p, label) => p.locator(".dc-context-menu .dc-menu-split").filter({ has: p.locator(".dc-menu-label-head", { hasText: pattern(label) }) });
const row = (p, label) => split(p, label).locator(".dropdown-item");
const chip = (p, label) => split(p, label).locator(".dc-menu-chip");
const plainRow = (p, label) => p.locator(".dc-context-menu > .dropdown-item").filter({ has: p.locator(".dc-menu-label-head", { hasText: pattern(label) }) });

const menuShown = (p) => p.waitForSelector(".dc-context-menu", { state: "visible", timeout: 6000 });
const menuGone = (p) => p.waitForSelector(".dc-context-menu", { state: "detached", timeout: 5000 });

const forget = (p) => p.evaluate(() => { window.__copies.length = 0; });

async function copied(p) {
  await p.waitForFunction(() => window.__copies.length > 0, null, { timeout: 5000 });
  await sleep(150);
  const all = await p.evaluate(() => window.__copies.splice(0));
  assert(all.length === 1, `one copy expected, got ${JSON.stringify(all)}`);
  assert(all[0].via === "execCommand" && all[0].ok, `the copy did not land inside the click: ${JSON.stringify(all[0])}`);
  return all[0].text;
}

async function statusSays(p, text) {
  await p.waitForFunction((t) => (document.querySelector("[data-editor-status]")?.textContent || "").includes(t), text, { timeout: 5000 });
}

async function openEditor(p, project) {
  await p.goto(`${BASE}/projects/${encodeURIComponent(project)}/editor`, { waitUntil: "domcontentloaded" });
  await L.dismissUpdate(p);
  await L.editorReady(p);
  await p.waitForSelector('.editor-item[data-path="src"]', { state: "attached", timeout: 15000 });
}

async function openDrawer(mp) {
  await mp.waitForSelector("[data-editor-drawer-toggle]", { timeout: 10000 });
  await mp.waitForSelector(".editor.editor-drawer-open", { timeout: 4000 }).catch(() => {});
  if (!(await mp.evaluate(() => !!document.querySelector(".editor.editor-drawer-open")))) {
    await mp.tap("[data-editor-drawer-toggle]");
    await mp.waitForSelector(".editor.editor-drawer-open", { timeout: 6000 });
  }
}

// openFile unfolds the folders down to the file and opens it, so its tree
// row stands as well.
async function openFile(p, { tap = false } = {}) {
  const parts = FILE.split("/");
  for (let i = 0; i < parts.length; i += 1) {
    const sel = `.editor-item[data-path="${parts.slice(0, i + 1).join("/")}"]`;
    await p.waitForSelector(sel, { timeout: 10000 });
    if (i < parts.length - 1 && (await p.$(`.editor-item[data-path="${parts.slice(0, i + 2).join("/")}"]`))) continue;
    if (tap) await p.tap(sel);
    else await p.click(sel);
  }
  await p.waitForFunction((want) => document.querySelector('.editor-tab[aria-selected="true"]')?.dataset.path === want, FILE, { timeout: 10000 });
  await p.waitForFunction(() => [...document.querySelectorAll(".cm-line")].some((l) => l.textContent === "three"), null, { timeout: 10000 });
}

async function rightClick(p, selector) {
  await p.click(selector, { button: "right" });
  await menuShown(p);
}

const lineCell = (p, n) => p.locator(".cm-lineNumbers .cm-gutterElement:visible").filter({ hasText: new RegExp(`^${n}$`) }).first();

// geometry reads the row, its chip and the menu off the screen.
const geometry = (p, label) => split(p, label).evaluate((wrap) => {
  const box = (el) => {
    const r = el.getBoundingClientRect();
    return { left: r.left, right: r.right, top: r.top, bottom: r.bottom, width: r.width, height: r.height };
  };
  const chipEl = wrap.querySelector(".dc-menu-chip");
  const style = getComputedStyle(chipEl);
  return {
    row: box(wrap.querySelector(".dropdown-item")),
    chip: box(chipEl),
    menu: box(wrap.closest(".dc-context-menu")),
    shown: style.display !== "none" && style.visibility === "visible" && Number(style.opacity) > 0,
    text: chipEl.textContent.trim(),
  };
});

async function chipBeside(p, label) {
  const g = await geometry(p, label);
  assert(g.shown && g.chip.width > 0 && g.chip.height > 0, `the chip of ${label} does not show`);
  assert(g.text === "absolute", `the chip reads ${JSON.stringify(g.text)}`);
  assert(g.chip.left >= g.row.right - 0.5, `the chip overlaps its row: ${JSON.stringify(g)}`);
  assert(g.chip.right <= g.menu.right && g.row.left >= g.menu.left, `the chip leaves the menu: ${JSON.stringify(g)}`);
  const middle = (g.chip.top + g.chip.bottom) / 2;
  assert(middle > g.row.top && middle < g.row.bottom, `the chip does not stand on its row's line: ${JSON.stringify(g)}`);
  return g;
}

// rowAndChip copies once through the row and once through the chip, the menu
// opened anew by open() before each.
async function rowAndChip(p, open, label, relative, absolute, { tap = false } = {}) {
  await open();
  await chipBeside(p, label);
  await forget(p);
  if (tap) await row(p, label).tap();
  else await row(p, label).click();
  await menuGone(p);
  const viaRow = await copied(p);
  assert(viaRow === relative, `the row copied ${JSON.stringify(viaRow)}, not ${JSON.stringify(relative)}`);
  await statusSays(p, `Copied ${relative}`);
  await sleep(450);
  await open();
  if (tap) await chip(p, label).tap();
  else await chip(p, label).click();
  await menuGone(p);
  const viaChip = await copied(p);
  assert(viaChip === absolute, `the chip copied ${JSON.stringify(viaChip)}, not ${JSON.stringify(absolute)}`);
  await statusSays(p, `Copied ${absolute}`);
  await sleep(450);
  return `${viaRow} | ${viaChip}`;
}

// focused names what holds the focus in a menu: a row by its label, a chip by
// its row's label.
const focused = (p) => p.evaluate(() => {
  const el = document.activeElement;
  const head = (node) => node?.querySelector(".dc-menu-label-head")?.textContent.trim() || "";
  if (el?.matches(".dc-menu-chip")) return `chip:${head(el.closest(".dc-menu-split"))}`;
  if (el?.matches(".dropdown-item")) return `row:${head(el)}`;
  return el ? el.tagName.toLowerCase() : "";
});

async function press(p, key, want) {
  await p.keyboard.press(key);
  const got = await focused(p);
  assert(got === want, `${key} put the focus on ${got}, not ${want}`);
}

L.runFeature("EDITOR-COPY-PATH", async ({ engine, ctx, page, run, mobilePage }) => {
  const tag = `${engine}-${Date.now().toString(36)}`;
  const main = `zzcp-${tag}`;
  const wt = `zzcpw-${tag}`;
  const projects = `${AUX}/projects`;
  const mainDir = `${projects}/${main}`;
  const wtDir = `${projects}/${wt}`;
  const fileRow = `.editor-item[data-path="${FILE}"]`;
  const folderRow = `.editor-item[data-path="${FOLDER}"]`;
  const fileTab = `.editor-tab[data-path="${FILE}"]`;
  let mainAbs = "";
  let wtAbs = "";
  await ctx.addInitScript(L.recordCopies);
  let phoneRecords = false;
  const openPhoneFile = async (mp) => {
    if (!phoneRecords) {
      await mp.context().addInitScript(L.recordCopies);
      phoneRecords = true;
    }
    await openEditor(mp, main);
    await openDrawer(mp);
    await openFile(mp, { tap: true });
  };

  await run("setup: a repository with a linked worktree, the page without a secure origin", async () => {
    assert(fs.existsSync(projects), `${projects} is not mounted at its host path, see the header`);
    fs.mkdirSync(`${mainDir}/${FOLDER}`, { recursive: true });
    fs.writeFileSync(`${mainDir}/${FILE}`, "one\ntwo\nthree\nfour\nfive\n");
    git(mainDir, "init", "-q");
    git(mainDir, "add", "-A");
    git(mainDir, "commit", "-q", "-m", "board");
    git(mainDir, "worktree", "add", "-q", "-b", `wt-${tag}`, wtDir);
    mainAbs = await L.projectPath(page, main);
    wtAbs = await L.projectPath(page, wt);
    assert(mainAbs === mainDir, `the instance serves ${main} from ${mainAbs}`);
    assert(wtAbs === wtDir, `the instance serves ${wt} from ${wtAbs}`);
    await openEditor(page, main);
    const origin = await page.evaluate(() => ({ secure: window.isSecureContext, clipboard: !!navigator.clipboard }));
    assert(!origin.secure && !origin.clipboard, `BASE_URL must be plain http under a name that is not localhost: ${JSON.stringify(origin)}`);
  });

  await run("desktop, tree file: the row copies the path in the project, the chip the absolute one", async () => {
    await openEditor(page, main);
    await openFile(page);
    await rightClick(page, fileRow);
    const chips = await page.locator(".dc-context-menu .dc-menu-chip").count();
    assert(chips === 1, `the tree menu carries ${chips} chips, Copy path alone has one`);
    assert(await plainRow(page, "Copy contents").count() === 1, "a row without a chip is no longer a direct child of the menu");
    await page.keyboard.press("Escape");
    await menuGone(page);
    return rowAndChip(page, () => rightClick(page, fileRow), "Copy path", FILE, `${mainAbs}/${FILE}`);
  });

  await run("desktop, tree folder: the chip copies the folder's absolute path", async () => {
    return rowAndChip(page, () => rightClick(page, folderRow), "Copy path", FOLDER, `${mainAbs}/${FOLDER}`);
  });

  await run("desktop, tab menu: the row copies the path in the project, the chip the absolute one", async () => {
    return rowAndChip(page, () => rightClick(page, fileTab), "Copy path", FILE, `${mainAbs}/${FILE}`);
  });

  await run("desktop, line menu: Copy path:line copies with the line, the chip the absolute path with the line", async () => {
    await openEditor(page, main);
    await openFile(page);
    const open = async () => {
      await sleep(400);
      await lineCell(page, 3).click();
      await menuShown(page);
    };
    await open();
    assert(await plainRow(page, "Add line comment").count() === 1, "the comment row is not a plain row");
    await page.keyboard.press("Escape");
    await menuGone(page);
    return rowAndChip(page, open, "Copy path:line", `${FILE}:3`, `${mainAbs}/${FILE}:3`);
  });

  await run("desktop, revision tab: the file at a revision copies its path and the absolute one", async () => {
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await page.evaluate(({ key, file }) => {
      localStorage.setItem(key, JSON.stringify({ open: [file, { type: "revision", path: file, rev: "HEAD", label: "HEAD" }], active: file }));
    }, { key: `dc-editor-tabs:${main}`, file: FILE });
    await openEditor(page, main);
    const revTab = `.editor-tab[data-path="//rev/HEAD/${encodeURIComponent(FILE)}"]`;
    await page.waitForSelector(revTab, { timeout: 10000 });
    return rowAndChip(page, () => rightClick(page, revTab), "Copy path", FILE, `${mainAbs}/${FILE}`);
  });

  await run("desktop, keyboard: arrow right reaches the chip, left goes back, up and down walk the rows, Enter copies", async () => {
    await openEditor(page, main);
    await openFile(page);
    await rightClick(page, fileRow);
    const labels = await page.$$eval(".dc-context-menu .dropdown-item:not(:disabled)", (els) => els.map((el) => el.querySelector(".dc-menu-label-head").textContent.trim()));
    const at = labels.indexOf("Copy path");
    assert(at > 0 && at < labels.length - 1, `Copy path stands at ${at} of ${labels.join(", ")}`);
    for (let i = 0; i < labels.length && (await focused(page)) !== "row:Copy path"; i += 1) await page.keyboard.press("ArrowDown");
    assert(await focused(page) === "row:Copy path", "arrow down never reached Copy path");
    await press(page, "ArrowRight", "chip:Copy path");
    assert(await page.evaluate(() => document.activeElement.matches(":focus-visible")), "the chip reached by the keyboard shows no focus ring");
    await press(page, "ArrowLeft", "row:Copy path");
    await press(page, "ArrowRight", "chip:Copy path");
    await press(page, "ArrowDown", `row:${labels[at + 1]}`);
    await press(page, "ArrowUp", "row:Copy path");
    await press(page, "ArrowRight", "chip:Copy path");
    await press(page, "ArrowUp", `row:${labels[at - 1]}`);
    await press(page, "ArrowDown", "row:Copy path");
    await press(page, "ArrowRight", "chip:Copy path");
    await forget(page);
    await page.keyboard.press("Enter");
    await menuGone(page);
    const text = await copied(page);
    assert(text === `${mainAbs}/${FILE}`, `Enter on the chip copied ${JSON.stringify(text)}`);
    return text;
  });

  await run("desktop, pointer: the chip under the mouse marks its row once, moving on unmarks it", async () => {
    await rightClick(page, fileRow);
    await chip(page, "Copy path").hover();
    const marked = await split(page, "Copy path").evaluate((wrap) => ({
      within: wrap.matches(":focus-within"),
      chip: document.activeElement === wrap.querySelector(".dc-menu-chip"),
      wrap: getComputedStyle(wrap).backgroundColor,
      row: getComputedStyle(wrap.querySelector(".dropdown-item")).backgroundColor,
    }));
    assert(marked.within && marked.chip, `the hovered chip holds no focus: ${JSON.stringify(marked)}`);
    assert(marked.wrap !== "rgba(0, 0, 0, 0)", `the row of the hovered chip is not marked: ${JSON.stringify(marked)}`);
    assert(marked.row === "rgba(0, 0, 0, 0)", `the row is tinted a second time under the split: ${JSON.stringify(marked)}`);
    if (SHOTS) {
      await row(page, "Copy path").hover();
      await sleep(200);
      const box = await page.locator(".dc-context-menu").boundingBox();
      await page.screenshot({
        path: `${SHOTS}/copy-path-chip-desktop.png`,
        clip: { x: Math.max(0, box.x - 24), y: Math.max(0, box.y - 24), width: box.width + 48, height: box.height + 48 },
      });
    }
    await plainRow(page, "Copy contents").hover();
    const left = await split(page, "Copy path").evaluate((wrap) => ({
      within: wrap.matches(":focus-within"),
      hover: wrap.matches(":hover"),
      wrap: getComputedStyle(wrap).backgroundColor,
    }));
    assert(!left.within && !left.hover && left.wrap === "rgba(0, 0, 0, 0)", `the row stays marked after the pointer left: ${JSON.stringify(left)}`);
    await page.keyboard.press("Escape");
    await menuGone(page);
  });

  await run("worktree: the chip copies the path under the worktree's own folder", async () => {
    assert(wtAbs && wtAbs !== mainAbs, `the worktree has no folder of its own: ${wtAbs}`);
    await openEditor(page, wt);
    await openFile(page);
    return rowAndChip(page, () => rightClick(page, fileRow), "Copy path", FILE, `${wtAbs}/${FILE}`);
  });

  await run("phone 390px: the tab menu's row and chip are two tap targets, each copies its own path", async () => {
    const mp = await mobilePage();
    await openPhoneFile(mp);
    const origin = await mp.evaluate(() => ({ secure: window.isSecureContext, clipboard: !!navigator.clipboard, width: innerWidth }));
    assert(!origin.secure && !origin.clipboard && origin.width === 390, `the phone page: ${JSON.stringify(origin)}`);
    const activeTab = '.editor-tab[aria-selected="true"]';
    const open = async () => {
      await mp.tap(activeTab);
      await menuShown(mp);
    };
    await open();
    const g = await chipBeside(mp, "Copy path");
    assert(g.chip.height >= 32, `the chip is ${g.chip.height}px high, too small for a finger`);
    if (SHOTS) {
      await sleep(200);
      await mp.screenshot({ path: `${SHOTS}/copy-path-chip-phone.png` });
    }
    await mp.keyboard.press("Escape");
    await menuGone(mp);
    await sleep(450);
    return rowAndChip(mp, open, "Copy path", FILE, `${mainAbs}/${FILE}`, { tap: true });
  });

  await run("phone 390px: the line menu's chip copies the absolute path with the line", async () => {
    const mp = await mobilePage();
    await openPhoneFile(mp);
    const open = async () => {
      await sleep(400);
      await lineCell(mp, 2).tap();
      await menuShown(mp);
    };
    return rowAndChip(mp, open, "Copy path:line", `${FILE}:2`, `${mainAbs}/${FILE}:2`, { tap: true });
  });

  await run("cleanup: remove the worktree and the repository", async () => {
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    fs.rmSync(wtDir, { recursive: true, force: true });
    fs.rmSync(mainDir, { recursive: true, force: true });
    assert(!fs.existsSync(wtDir) && !fs.existsSync(mainDir), "the scratch projects are still there");
  });
});
