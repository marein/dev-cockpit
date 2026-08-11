// Demo recording for the split view's column layout, not part of a runner and
// not a test: it drives the real app at watching speed and writes one video,
// artifacts/split-view-grid.webm. Every step is announced in a caption and
// every drag runs in many small moves, because a jump in the picture shows
// nothing.
//
//   docker run --rm --network host -v "$PWD/tests/e2e":/work -w /work \
//     dc-e2e:1.60.0 node demo-split-view-grid.js
//
// It creates its own project, shells and coder on the throwaway instance and
// takes all of them with it at the end.
const fs = require("fs");
const path = require("path");
const L = require("./lib");
const { sleep } = L;

const OUT_DIR = "artifacts";
const OUT_FILE = path.join(OUT_DIR, "split-view-grid.webm");
// Wide enough for three columns with readable pane heads, small enough that
// the whole page fits without scrolling and survives being watched on a phone.
const VIEWPORT = { width: 1180, height: 820 };

// Deliberate pauses: the picture has to stand still long enough to be read.
const BEAT = 900;
const READ = 2400;

async function main() {
  fs.mkdirSync(OUT_DIR, { recursive: true });
  const browser = await L.launch("chromium");
  const context = await browser.newContext({
    ignoreHTTPSErrors: true,
    viewport: VIEWPORT,
    recordVideo: { dir: OUT_DIR, size: VIEWPORT },
  });
  const page = await context.newPage();
  const tag = Date.now().toString(36).slice(-5);
  const project = `zzdemo-${tag}`;
  const shells = [];
  let coderId = null;

  // The caption rides above everything, outside the swapped page content, so a
  // navigation leaves it standing.
  const caption = async (text) => {
    await page.evaluate((message) => {
      let bar = document.getElementById("dc-demo-caption");
      if (!bar) {
        bar = document.createElement("div");
        bar.id = "dc-demo-caption";
        bar.style.cssText = [
          "position:fixed", "left:50%", "top:12px", "transform:translateX(-50%)",
          "z-index:2000", "padding:8px 18px", "border-radius:999px",
          "background:rgba(17,24,39,0.92)", "color:#f9fafb",
          "font:500 17px/1.3 ui-sans-serif,system-ui,sans-serif",
          "box-shadow:0 6px 20px rgba(0,0,0,0.35)", "pointer-events:none",
          "max-width:90vw", "white-space:nowrap",
        ].join(";");
        document.body.appendChild(bar);
      }
      bar.textContent = message;
    }, text).catch(() => {});
    await sleep(BEAT);
  };

  // A guide line at the bottom edge of the split, so the rows budget is
  // something you can see instead of something you have to believe.
  const guide = async (on) => {
    await page.evaluate((show) => {
      const old = document.getElementById("dc-demo-guide");
      if (old) old.remove();
      if (!show) return;
      const split = document.querySelector("terminal-split");
      if (!split) return;
      const rect = split.getBoundingClientRect();
      const line = document.createElement("div");
      line.id = "dc-demo-guide";
      line.style.cssText = [
        "position:fixed", `top:${Math.round(rect.bottom)}px`, "left:0", "right:0",
        "height:3px", "background:#f59e0b", "z-index:1999", "pointer-events:none",
      ].join(";");
      document.body.appendChild(line);
    }, on).catch(() => {});
  };

  // A drag the eye can follow: down, many small moves, up.
  const dragTo = async (fromBox, x, y, steps = 30) => {
    const startX = fromBox.x + fromBox.width / 2;
    const startY = fromBox.y + fromBox.height / 2;
    await page.mouse.move(startX, startY);
    await sleep(BEAT / 2);
    await page.mouse.down();
    await sleep(BEAT / 2);
    for (let i = 1; i <= steps; i += 1) {
      await page.mouse.move(startX + (x - startX) * (i / steps), startY + (y - startY) * (i / steps), { steps: 2 });
      await sleep(45);
    }
    await sleep(BEAT);
    await page.mouse.up();
  };

  const box = async (selector) => {
    for (let i = 0; i < 20; i += 1) {
      const b = await page.locator(selector).first().boundingBox().catch(() => null);
      if (b) return b;
      await sleep(200);
    }
    throw new Error(`no box for ${selector}`);
  };
  const splitBox = () => box("terminal-split");
  const headBox = (id) => box(`.attach-split-pane[data-pane-id="${id}"] [data-pane-head]`);
  const panesSettled = async () => {
    await page.waitForSelector(".attach-split-pane .xterm-screen canvas", { timeout: 25000 });
    await sleep(1500);
  };
  const menuItem = async (selector, label) => {
    await page.click(selector, { button: "right" });
    const item = page.locator(".dc-context-menu .dropdown-item", { hasText: label }).first();
    await item.waitFor({ state: "visible", timeout: 6000 });
    await sleep(BEAT);
    return item;
  };
  const paneCount = (n) => page.waitForFunction(
    (want) => document.querySelectorAll("terminal-attach[terminal-id]").length === want,
    n,
    { timeout: 30000 },
  );

  try {
    await L.login(page);
    // Bigger type than the default, so the terminals read on a small screen.
    await page.evaluate(() => {
      window.localStorage.setItem("dc-terminal-font-size", "16");
      window.localStorage.setItem("dc-terminal-rows", "24");
    });
    await L.createProject(page, project);
    for (let i = 0; i < 2; i += 1) shells.push(await L.createShell(page, project));
    const ids = shells.map((u) => new URL(u).pathname.split("/").pop());
    const group = await page.evaluate(async (list) => {
      const token = document.querySelector('meta[name="csrf-token"]').content;
      const response = await fetch("/terminal-tabs/group", {
        method: "POST",
        headers: { "X-CSRF-Token": token, "Content-Type": "application/json" },
        body: JSON.stringify({ ids: list }),
      });
      return response.json();
    }, ids);
    const gid = group.id;

    // 1 — the split as it has always been: one column per member.
    await page.goto(`${L.BASE}${group.url}`, { waitUntil: "domcontentloaded" });
    await panesSettled();
    await caption("A split view of two terminals, side by side");
    await sleep(READ);

    // 2 — a third terminal joins and is stacked into the left column.
    await caption("A third terminal joins the split");
    const thirdUrl = await L.createShell(page, project);
    shells.push(thirdUrl);
    const thirdId = new URL(thirdUrl).pathname.split("/").pop();
    await page.goto(`${L.BASE}/splits/${gid}`, { waitUntil: "domcontentloaded" });
    await page.waitForSelector(`terminal-tabs .terminal-tab[data-tab-id="${thirdId}"]`, { state: "attached", timeout: 15000 });
    await page.evaluate(async (list) => {
      const token = document.querySelector('meta[name="csrf-token"]').content;
      await fetch("/terminal-tabs/group", {
        method: "POST",
        headers: { "X-CSRF-Token": token, "Content-Type": "application/json" },
        body: JSON.stringify({ ids: list }),
      });
    }, [...ids, thirdId]);
    await paneCount(3);
    await panesSettled();
    await sleep(READ);

    await caption("Drag its head sideways into the left column: it stacks there");
    let split = await splitBox();
    await dragTo(await headBox(thirdId), split.x + split.width * 0.16, split.y + split.height * 0.8);
    await panesSettled();
    await caption("Two stacked on the left, one at full height on the right");
    await sleep(READ + 800);

    // 3 — a pane moves from one column into the other.
    await caption("A drag into the other column moves the pane over");
    split = await splitBox();
    await dragTo(await headBox(thirdId), split.x + split.width * 0.8, split.y + split.height * 0.75);
    await panesSettled();
    await sleep(READ);

    // 4 — a drop on the outer edge opens a column.
    await caption("A drop on the outer edge opens a column of its own");
    split = await splitBox();
    await dragTo(await headBox(thirdId), split.x + split.width - 12, split.y + split.height * 0.4);
    await panesSettled();
    await sleep(READ);

    // 5 — the rows budget: the page height does not move when panes stack.
    await caption("The rows setting is the height of the page, not of a pane");
    await guide(true);
    await sleep(READ);
    await caption("Stacking shares that height, the page keeps it");
    split = await splitBox();
    await dragTo(await headBox(thirdId), split.x + split.width * 0.16, split.y + split.height * 0.8);
    await panesSettled();
    await sleep(READ + 1200);
    await guide(false);

    // 6 — a new shell created straight into a pane's column.
    await caption("New shell here: the pane head names the column");
    const shellItem = await menuItem(`.attach-split-pane[data-pane-id="${ids[1]}"] [data-pane-head]`, "New shell here");
    await shellItem.click();
    await page.waitForURL(/\/shells\/new/, { timeout: 15000 });
    await sleep(BEAT);
    await caption("The form opens prefilled, the target column rides along");
    await sleep(READ);
    await page.locator('form:has(select[name="project"]) button[type="submit"]').first().click();
    await paneCount(4);
    await panesSettled();
    const fresh = await page.evaluate((known) => [...document.querySelectorAll(".attach-split-pane")]
      .map((pane) => pane.dataset.paneId)
      .find((id) => !known.includes(id)), [...ids, thirdId]);
    if (fresh) shells.push(`${L.BASE}/shells/${fresh}`);
    await caption("It came up in exactly that column");
    await sleep(READ);

    // 7 — a coder created for the whole split, in a column of its own.
    await caption("New coder here on the split tab: a column of its own");
    const coderItem = await menuItem("terminal-tabs .terminal-tab-split", "New coder here");
    await coderItem.click();
    await page.waitForURL(/\/coders\/new/, { timeout: 15000 });
    await sleep(BEAT);
    await caption("The form opens prefilled and carries the split along");
    await sleep(READ);
    const form = page.locator('form:has(select[name="agent"])').first();
    await form.locator('input[name="name"]').fill(`demo-${tag}`);
    await sleep(BEAT);
    await form.locator('button[type="submit"]').first().click();
    await page.waitForURL(new RegExp(`/splits/${gid}\\?focus=`), { timeout: 40000 });
    coderId = new URL(page.url()).searchParams.get("focus");
    await paneCount(5);
    await panesSettled();
    await caption("The new coder stands in its own column at the right edge");
    await sleep(READ + 1500);
  } finally {
    // The recording ends with the last step: the context is closed before
    // anything is taken back, so no cleanup lands in the video.
    const video = page.video();
    await page.close().catch(() => {});
    await context.close().catch(() => {});
    if (video) {
      const raw = await video.path();
      fs.rmSync(OUT_FILE, { force: true });
      fs.renameSync(raw, OUT_FILE);
      console.log(`video: ${OUT_FILE}`);
    }
    // Take everything back from a plain context: the sessions first, then the
    // project.
    const after = await browser.newContext({ ignoreHTTPSErrors: true, viewport: VIEWPORT });
    const cleanup = await after.newPage();
    try {
      await L.login(cleanup);
      await cleanup.evaluate(async (list) => {
        const token = document.querySelector('meta[name="csrf-token"]').content;
        for (const url of list) {
          await fetch(url, { method: "POST", headers: { "X-CSRF-Token": token, Accept: "application/json" } }).catch(() => {});
        }
      }, [
        ...shells.map((u) => `${new URL(u).pathname}/delete`),
        ...(coderId ? [`/coders/${coderId}/delete`] : []),
      ]);
      await sleep(1500);
      await L.deleteProject(cleanup, project).catch(() => {});
    } finally {
      await after.close().catch(() => {});
      await browser.close();
    }
  }
}

main().catch((error) => {
  console.error(error);
  process.exit(1);
});
