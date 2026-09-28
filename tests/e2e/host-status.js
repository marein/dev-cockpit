#!/usr/bin/env node
const L = require("./lib");
const { assert, sleep, BASE } = L;

// Server status in the status line: CPU, RAM and disk, each a percentage. The
// server reads them in internal/hostinfo (busy cores from /proc/stat on Linux,
// load against the cores on a Mac, memory in use, the filesystem the projects
// sit on), renders the first values into the page and pushes every later
// reading as a `host` event on the shared stream, on connect and on its own 5s
// beat. dc-host-status only paints.
//
// The status line carries three slim bars, one per metric, filled to its
// percent: green while quiet, yellow from 80, red from 95. Each bar names itself
// with its value as title and aria label. The bars are the toggle of a dropup
// with one detail row per metric plus the plain numbers. A phone has no server
// status in its work head and no floating card any more; the same three bars
// are its Cockpit tab's icon, which cockpit.js covers with the sheet they open.
//
// Gotchas:
// - the numbers are the real machine's, so no check asserts a specific value;
//   what is asserted is the shape (0-100 plus a percent sign, a label, a bar
//   width that matches) and how the surfaces react to a reading.
// - the paint path is driven by dispatching the `dc:host` DOM event the stream
//   client re-dispatches, which is exactly what @dc/events does; that keeps the
//   thresholds testable without loading the host.
// - the surfaces hide through the hidden attribute, and style.css makes that
//   win over any display rule, so checks read computed display, never the
//   attribute.
// - SHOTS_DIR saves the status line in its quiet and its warn state.

const DESKTOP = ".dc-status";
const number = (text) => Number(String(text).replace("%", "").trim());
const TONES = ["bg-green", "bg-secondary", "bg-yellow", "bg-red"];

const reading = (detail) => (page) => page.evaluate((d) => document.dispatchEvent(new CustomEvent("dc:host", { detail: d })), detail);

const meters = (page) => page.$$eval(`${DESKTOP} [data-host-meter]`, (els, tones) => els.map((el) => {
  const bar = el.querySelector(".js-host-bar");
  const track = el.querySelector(".dc-host-meter-track").getBoundingClientRect();
  return {
    key: el.dataset.hostMeter,
    shown: getComputedStyle(el).display !== "none",
    title: el.title,
    label: el.getAttribute("aria-label"),
    width: bar.style.width,
    tone: tones.find((c) => bar.classList.contains(c)),
    trackWidth: track.width,
    trackHeight: track.height,
    fill: getComputedStyle(bar).backgroundColor,
  };
}), TONES);

async function shot(page, name) {
  if (!process.env.SHOTS_DIR) return;
  const box = await page.locator(DESKTOP).boundingBox();
  await page.screenshot({
    path: `${process.env.SHOTS_DIR}/${name}.png`,
    clip: { x: box.x, y: Math.max(0, box.y - 150), width: Math.min(box.width, 520), height: box.height + 150 },
  });
}

L.runFeature("HOST-STATUS", async ({ page, run, mobilePage }) => {
  await run("the status line carries three bars filled to the machine's readings", async () => {
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    assert((await L.waitUpgraded(page, ["dc-host-status"], 8000)).length === 0, "dc-host-status not upgraded");
    const shown = await page.$eval(`${DESKTOP} dc-host-status`, (el) => getComputedStyle(el).display !== "none");
    assert(shown, "the status element stayed hidden on a machine that can be read");
    const word = await page.$eval(`${DESKTOP} [data-host-toggle]`, (el) => ({ text: el.textContent.replace(/\s+/g, " ").trim(), icon: !!el.querySelector(".ti-server") }));
    assert(!word.icon, "the status line still wears the server icon");
    assert(!/Server/.test(word.text), `the status line still spells Server: "${word.text}"`);
    const bars = await meters(page);
    assert(bars.map((b) => b.key).join(",") === "cpu,mem,disk", `bar order ${bars.map((b) => b.key)}`);
    const names = { cpu: "CPU", mem: "RAM", disk: "Disk" };
    for (const bar of bars) {
      if (!bar.shown) continue;
      const m = bar.label.match(/^(\S+) (\d+)%$/);
      assert(m && m[1] === names[bar.key], `${bar.key} aria label "${bar.label}"`);
      assert(bar.title.startsWith(bar.label), `${bar.key} title "${bar.title}" does not lead with "${bar.label}"`);
      assert(number(bar.width) === Math.min(100, Number(m[2])), `${bar.key} bar ${bar.width} for ${bar.label}`);
      assert(bar.trackWidth > 10 && bar.trackWidth < 60 && bar.trackHeight > 0 && bar.trackHeight <= 8,
        `${bar.key} track is not a slim bar: ${bar.trackWidth}x${bar.trackHeight}`);
    }
    assert(bars.find((b) => b.key === "disk").shown, "the disk bar is missing");
    await shot(page, "server-bars-live");
    return bars.map((b) => b.label).join(", ");
  });

  await run("a quiet reading is green, warn is yellow, crit is red", async () => {
    await reading({
      hasCpu: true, hasMem: true, hasDisk: true, cpu: 12, mem: 34, disk: 56,
      cpuLabel: "Usage across 8 cores", memLabel: "5 GB of 16 GB used", diskLabel: "225 GB of 512 GB free",
    })(page);
    await sleep(200);
    const quiet = await meters(page);
    assert(quiet.every((b) => b.tone === "bg-green"), `quiet tones ${quiet.map((b) => b.tone)}`);
    assert(quiet.map((b) => b.width).join(",") === "12%,34%,56%", `quiet widths ${quiet.map((b) => b.width)}`);
    assert(quiet[0].title === "CPU 12% · Usage across 8 cores", `cpu title "${quiet[0].title}"`);
    await shot(page, "server-bars-quiet");

    await reading({
      hasCpu: true, hasMem: true, hasDisk: true, cpu: 12, mem: 84, disk: 97,
      cpuLabel: "Load 0.96 on 8 cores", memLabel: "13 GB of 16 GB used", diskLabel: "9.9 GB of 512 GB free",
    })(page);
    await sleep(200);
    const loud = await meters(page);
    assert(loud.map((b) => b.tone).join(",") === "bg-green,bg-yellow,bg-red", `tones ${loud.map((b) => b.tone)}`);
    assert(loud[1].label === "RAM 84%" && loud[2].label === "Disk 97%", `labels ${loud.map((b) => b.label)}`);
    assert(new Set(loud.map((b) => b.fill)).size === 3, `green, yellow and red paint alike: ${loud.map((b) => b.fill)}`);
    await shot(page, "server-bars-warn");
  });

  await run("a click on the bars opens the dropup with the detail rows", async () => {
    await page.click(`${DESKTOP} [data-host-toggle]`);
    await page.waitForSelector(`${DESKTOP} dc-host-status .dropdown-menu.show`, { timeout: 4000 });
    const rows = await page.$$eval(`${DESKTOP} [data-host-row]`, (els, tones) => els.map((el) => {
      const bar = el.querySelector(".js-host-bar");
      return {
        key: el.dataset.hostRow,
        value: el.querySelector(".js-host-value").textContent,
        label: el.querySelector(".js-host-label").textContent,
        width: bar.style.width,
        tone: tones.find((c) => bar.classList.contains(c)),
      };
    }), TONES);
    assert(rows.map((r) => r.key).join(",") === "cpu,mem,disk", `row order ${rows.map((r) => r.key)}`);
    assert(rows[0].value === "12%" && rows[0].tone === "bg-green", `cpu ${JSON.stringify(rows[0])}`);
    assert(rows[1].value === "84%" && rows[1].tone === "bg-yellow" && rows[1].width === "84%", `mem ${JSON.stringify(rows[1])}`);
    assert(rows[2].value === "97%" && rows[2].tone === "bg-red", `disk ${JSON.stringify(rows[2])}`);
    assert(rows[0].label === "Load 0.96 on 8 cores", `cpu label ${rows[0].label}`);
    assert(!(await page.$(`${DESKTOP} [data-host-float-open]`)), "the panel still offers the Float button");
    const head = await page.$eval(`${DESKTOP} dc-host-status .dropdown-menu`, (el) => ({
      text: el.textContent.replace(/\s+/g, " ").trim(),
      group: el.querySelector('[role="group"]')?.getAttribute("aria-label"),
    }));
    assert(!/\bServer\b/.test(head.text) && head.group === "Server", `the dropup still carries a heading or lost its name: ${JSON.stringify(head)}`);
    const inside = await page.$eval(`${DESKTOP} dc-host-status .dropdown-menu`, (el) => {
      const box = el.getBoundingClientRect();
      return box.top >= 0 && box.bottom <= window.innerHeight && box.right <= window.innerWidth;
    });
    assert(inside, "the dropup hangs outside the window");
    await page.keyboard.press("Escape");
    await page.waitForFunction(() => !document.querySelector(".dc-status .dropdown-menu.show"), null, { timeout: 4000 });
  });

  await run("a load past the cores keeps the number and caps the bar", async () => {
    await reading({ hasCpu: true, cpu: 140, cpuLabel: "Load 11.2 on 8 cores", hasMem: false, hasDisk: false })(page);
    await sleep(200);
    const bars = await meters(page);
    assert(bars[0].label === "CPU 140%" && bars[0].width === "100%" && bars[0].tone === "bg-red", `cpu ${JSON.stringify(bars[0])}`);
    assert(!bars[1].shown && !bars[2].shown, "a metric the machine cannot answer still showed a bar");
    const gone = await page.$$eval(`${DESKTOP} [data-host-row="mem"], ${DESKTOP} [data-host-row="disk"]`,
      (els) => els.every((el) => getComputedStyle(el).display === "none"));
    assert(gone, "a metric the machine cannot answer still showed a row");
  });

  await run("a machine that answers nothing takes the whole status away", async () => {
    await reading({ hasCpu: false, hasMem: false, hasDisk: false, cpu: -1, mem: -1, disk: -1 })(page);
    await sleep(200);
    const hidden = await page.$$eval("dc-host-status:not([data-host-worst])", (els) => els.every((el) => el.hidden));
    assert(hidden, "a status element stayed with nothing to show");
    const shown = await page.$eval(`${DESKTOP} dc-host-status`, (el) => getComputedStyle(el).display);
    assert(shown === "none", "the status line kept its bars with nothing to show");
  });

  await run("the float is gone: no element, no Float button, no stored card coming back", async () => {
    await page.evaluate(() => localStorage.setItem("dc-host-float", JSON.stringify({ open: true, x: 100, y: 100 })));
    await page.reload({ waitUntil: "domcontentloaded" });
    await page.waitForSelector(`${DESKTOP} dc-host-status`, { timeout: 8000 });
    await sleep(300);
    const left = await page.evaluate(() => ({
      float: document.querySelectorAll("dc-host-float, .dc-host-float, [data-host-float-open], [data-host-chip]").length,
      defined: !!customElements.get("dc-host-float"),
      mapped: !!JSON.parse(document.querySelector('script[type="importmap"]').textContent).imports["dc-host-float"],
      statuses: document.querySelectorAll(".dc-status dc-host-status").length,
      tab: document.querySelectorAll(".dc-tabbar dc-host-status").length,
      all: document.querySelectorAll("dc-host-status").length,
    }));
    assert(left.float === 0, `${left.float} float nodes still in the page`);
    assert(!left.defined && !left.mapped, "the float element is still defined or mapped");
    assert(left.statuses === 1 && left.tab === 1 && left.all === 2, `expected the status line's and the Cockpit tab's dc-host-status, found ${JSON.stringify(left)}`);
    await page.evaluate(() => localStorage.removeItem("dc-host-float"));
  });

  await run("mobile: the work head carries no server status, the Cockpit tab does", async () => {
    const mp = await mobilePage();
    await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await mp.waitForSelector('.dc-tabbar button[data-ctx-area="cockpit"]', { timeout: 8000 });
    const left = await mp.evaluate(() => ({
      tools: document.querySelectorAll(".dc-head-tools").length,
      head: document.querySelectorAll(".dc-work-head dc-host-status, .dc-work-head .ti-server, .dc-work-head [data-host-toggle]").length,
      visible: [...document.querySelectorAll("dc-host-status, dc-host-float")]
        .filter((el) => el.getClientRects().length > 0 && getComputedStyle(el).visibility !== "hidden")
        .map((el) => (el.closest(".dc-tabbar") ? "tab" : el.parentElement.className)),
    }));
    assert(left.tools === 0, "the phone's head tools are still rendered");
    assert(left.head === 0, "the phone's work head still carries the server button");
    assert(left.visible.join(",") === "tab", `visible server status on the phone: ${left.visible.join(", ") || "none"}`);
  });

  await run("the rail keeps the mark and the icon only logout", async () => {
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await page.waitForSelector(".dc-rail .dc-rail-mark", { timeout: 8000 });
    const mark = await page.$eval(".dc-rail .dc-rail-mark", (el) => el.getAttribute("aria-label"));
    assert(mark === "Dev Cockpit", `the mark lost its name on a desktop: "${mark}"`);
    const hiddenOnWide = await page.$$eval(".js-update-flag",
      (els) => els.every((el) => getComputedStyle(el).display === "none"));
    assert(hiddenOnWide, "an update button rendered without an update");
    const logout = await page.$eval('.dc-rail form[action="/logout"] button', (el) => ({
      text: el.textContent.trim(),
      label: el.getAttribute("aria-label"),
    }));
    assert(logout.text === "", `logout still spells itself out: "${logout.text}"`);
    assert(logout.label === "Logout", `logout lost its label: ${logout.label}`);
  });
});
