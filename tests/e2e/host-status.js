#!/usr/bin/env node
const L = require("./lib");
const { assert, sleep, BASE } = L;

// Server status in the header: CPU, RAM and disk, each a percentage. The server
// reads them in internal/hostinfo (load against the cores, memory in use, the
// filesystem the projects sit on), renders the first values into the page and
// pushes every later reading as a `host` event on the shared stream, on connect
// and on every 15s heartbeat. dc-host-status only paints.
//
// The button sits at the right end of the header on both widths, next to the
// assistant, and opens the same panel: one bar per metric plus the plain
// numbers. Its icon carries the worst of the three, yellow from 80, red from 95,
// so a quiet header means a quiet machine. The one thing that moved into the
// burger menu is the update, as a primary button naming the version.
//
// Gotchas:
// - the layout mounts one dc-host-status per header breakpoint, like the bell,
//   so every selector here is scoped to a header: DESKTOP for the wide one,
//   MOBILE for the compact one. An unscoped query hits the hidden twin first.
// - the numbers are the real machine's, so no check asserts a specific value;
//   what is asserted is the shape (0-100 plus a percent sign, a label, a bar
//   width that matches) and how the surfaces react to a reading.
// - the paint path is driven by dispatching the `dc:host` DOM event the stream
//   client re-dispatches, which is exactly what @dc/events does; that keeps the
//   thresholds testable without loading the host.
// - the surfaces hide through the hidden attribute, and style.css makes that
//   win over d-flex, so checks read computed display, never the attribute.

const DESKTOP = ".navbar-collapse";
const MOBILE = "header.navbar";
const number = (text) => Number(String(text).replace("%", "").trim());

L.runFeature("HOST-STATUS", async ({ browser, page, run, mobilePage }) => {
  await run("the header carries the status button and its dropdown reads the machine", async () => {
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    assert((await L.waitUpgraded(page, ["dc-host-status"], 8000)).length === 0, "dc-host-status not upgraded");
    const shown = await page.$eval(`${DESKTOP} dc-host-status`, (el) => getComputedStyle(el).display !== "none");
    assert(shown, "the status element stayed hidden on a machine that can be read");
    await page.click(`${DESKTOP} [data-host-toggle]`);
    await page.waitForSelector(`${DESKTOP} dc-host-status .dropdown-menu.show`, { timeout: 4000 });
    const rows = await page.$$eval(`${DESKTOP} [data-host-row]`, (els) => els.map((el) => ({
      key: el.dataset.hostRow,
      hidden: getComputedStyle(el).display === "none",
      value: el.querySelector(".js-host-value").textContent,
      label: el.querySelector(".js-host-label").textContent,
      width: el.querySelector(".js-host-bar").style.width,
    })));
    assert(rows.length === 3, `expected three rows, got ${rows.length}`);
    assert(rows.map((r) => r.key).join(",") === "cpu,mem,disk", `row order ${rows.map((r) => r.key)}`);
    for (const row of rows) {
      if (row.hidden) continue;
      const value = number(row.value);
      assert(Number.isFinite(value) && value >= 0, `${row.key} value ${row.value}`);
      assert(/%$/.test(row.value.trim()), `${row.key} value carries no percent sign: ${row.value}`);
      assert(row.label.trim().length > 0, `${row.key} has no plain-numbers label`);
      // The bar is capped at 100 even when the load says more.
      assert(number(row.width) === Math.min(100, value), `${row.key} bar ${row.width} for ${row.value}`);
    }
    const disk = rows.find((r) => r.key === "disk");
    assert(!disk.hidden && /free/.test(disk.label), `disk label: ${disk.label}`);
    await page.keyboard.press("Escape");
    return rows.map((r) => `${r.key} ${r.value}`).join(", ");
  });

  await run("a reading repaints values, labels, bar widths and colors", async () => {
    await page.evaluate(() => document.dispatchEvent(new CustomEvent("dc:host", {
      detail: {
        hasCpu: true, hasMem: true, hasDisk: true,
        cpu: 12, mem: 84, disk: 97,
        cpuLabel: "Load 0.96 on 8 cores",
        memLabel: "13 GB of 16 GB used",
        diskLabel: "9.9 GB of 512 GB free",
        level: "crit",
      },
    })));
    await sleep(200);
    const painted = await page.evaluate((scope) => {
      const row = (key) => {
        const el = document.querySelector(`${scope} [data-host-row="${key}"]`);
        const bar = el.querySelector(".js-host-bar");
        return {
          value: el.querySelector(".js-host-value").textContent,
          label: el.querySelector(".js-host-label").textContent,
          width: bar.style.width,
          tone: ["bg-green", "bg-yellow", "bg-red"].find((c) => bar.classList.contains(c)),
        };
      };
      return {
        cpu: row("cpu"),
        mem: row("mem"),
        disk: row("disk"),
        icon: [...document.querySelectorAll(".js-host-icon")].map((i) => i.className),
      };
    }, DESKTOP);
    assert(painted.cpu.value === "12%" && painted.cpu.tone === "bg-green", `cpu ${JSON.stringify(painted.cpu)}`);
    assert(painted.mem.value === "84%" && painted.mem.tone === "bg-yellow", `mem ${JSON.stringify(painted.mem)}`);
    assert(painted.disk.value === "97%" && painted.disk.tone === "bg-red", `disk ${JSON.stringify(painted.disk)}`);
    assert(painted.cpu.label === "Load 0.96 on 8 cores", `cpu label ${painted.cpu.label}`);
    assert(painted.mem.width === "84%", `mem bar ${painted.mem.width}`);
    assert(painted.icon.every((c) => /text-red/.test(c)), `icon classes ${painted.icon}`);
  });

  await run("a load past the cores keeps the number and caps the bar", async () => {
    await page.evaluate(() => document.dispatchEvent(new CustomEvent("dc:host", {
      detail: { hasCpu: true, cpu: 140, cpuLabel: "Load 11.2 on 8 cores", hasMem: false, hasDisk: false, level: "crit" },
    })));
    await sleep(200);
    const cpu = await page.evaluate((scope) => {
      const el = document.querySelector(`${scope} [data-host-row="cpu"]`);
      return { value: el.querySelector(".js-host-value").textContent, width: el.querySelector(".js-host-bar").style.width };
    }, DESKTOP);
    assert(cpu.value === "140%", `value ${cpu.value}`);
    assert(cpu.width === "100%", `bar ${cpu.width}`);
    const gone = await page.$$eval(`${DESKTOP} [data-host-row="mem"], ${DESKTOP} [data-host-row="disk"]`,
      (els) => els.every((el) => getComputedStyle(el).display === "none"));
    assert(gone, "a metric the machine cannot answer still showed a row");
  });

  await run("a machine that answers nothing takes the whole status away", async () => {
    await page.evaluate(() => document.dispatchEvent(new CustomEvent("dc:host", {
      detail: { hasCpu: false, hasMem: false, hasDisk: false, cpu: -1, mem: -1, disk: -1, level: "" },
    })));
    await sleep(200);
    const hidden = await page.$$eval("dc-host-status", (els) => els.every((el) => getComputedStyle(el).display === "none"));
    assert(hidden, "the status element stayed visible with nothing to show");
    await page.reload({ waitUntil: "domcontentloaded" });
    await page.waitForSelector(`${DESKTOP} dc-host-status`, { timeout: 8000 });
  });

  await run("mobile: the status button stands in the compact header and opens the same panel", async () => {
    const mp = await mobilePage();
    await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await mp.waitForSelector(`${MOBILE} dc-host-status`, { timeout: 8000 });
    // The bell's dropdown carries a "Mark all read" button of its own, so the
    // count has to stay on the cluster's own children: status, assistant, bell.
    const cluster = await mp.$$eval(`${MOBILE} .d-md-none.d-flex > *`, (els) => els.map((el) => el.localName));
    assert(cluster.join(",") === "dc-host-status,a,dc-notifications", `the compact header carries ${cluster.join(", ")}`);
    assert((await mp.locator(`${MOBILE} .js-update-flag`).count()) === 0, "the update flag is still in the compact header");
    await mp.click(`${MOBILE} [data-host-toggle]`);
    await mp.waitForSelector(`${MOBILE} dc-host-status .dropdown-menu.show`, { timeout: 4000 });
    const rows = await mp.$$eval(`${MOBILE} [data-host-row]`, (els) => els
      .filter((el) => getComputedStyle(el).display !== "none")
      .map((el) => `${el.dataset.hostRow} ${el.querySelector(".js-host-value").textContent}`));
    assert(rows.length >= 1, "the panel opened without a single reading");
    const inside = await mp.$eval(`${MOBILE} dc-host-status .dropdown-menu`, (el) => {
      const box = el.getBoundingClientRect();
      return box.left >= -1 && box.right <= window.innerWidth + 1;
    });
    assert(inside, "the panel hangs over the edge of the phone");
    await mp.keyboard.press("Escape");
    return rows.join(", ");
  });

  await run("mobile: a threshold colors the button, quiet takes the color away", async () => {
    const mp = await mobilePage();
    const iconClass = () => mp.$eval(`${MOBILE} .js-host-icon`, (el) => el.className);
    await mp.evaluate(() => document.dispatchEvent(new CustomEvent("dc:host", {
      detail: {
        hasCpu: true, hasMem: true, hasDisk: true, cpu: 10, mem: 20, disk: 88,
        cpuLabel: "Load 0.8 on 8 cores", memLabel: "3 GB of 16 GB used", diskLabel: "60 GB of 512 GB free",
        level: "warn",
      },
    })));
    await sleep(200);
    const warn = await iconClass();
    assert(/text-yellow/.test(warn) && !/text-red/.test(warn), `icon at warn: ${warn}`);

    await mp.evaluate(() => document.dispatchEvent(new CustomEvent("dc:host", {
      detail: {
        hasCpu: true, hasMem: true, hasDisk: true, cpu: 10, mem: 20, disk: 97,
        cpuLabel: "a", memLabel: "b", diskLabel: "c", level: "crit",
      },
    })));
    await sleep(200);
    const crit = await iconClass();
    assert(/text-red/.test(crit) && !/text-yellow/.test(crit), `icon at crit: ${crit}`);

    await mp.evaluate(() => document.dispatchEvent(new CustomEvent("dc:host", {
      detail: {
        hasCpu: true, hasMem: true, hasDisk: true, cpu: 10, mem: 20, disk: 30,
        cpuLabel: "a", memLabel: "b", diskLabel: "c", level: "",
      },
    })));
    await sleep(200);
    const quiet = await iconClass();
    assert(!/text-(yellow|red)/.test(quiet), `icon stayed colored while quiet: ${quiet}`);
  });

  await run("mobile: the update is a button in the menu and names the version", async () => {
    const mp = await mobilePage();
    // Driven through the component itself, not by editing classes: whether this
    // host really has an update pending is none of this runner's business.
    const entry = await mp.evaluate(() => {
      const el = document.querySelector("li.js-update-flag");
      const check = document.querySelector("dc-update-check");
      if (!el || !check) return null;
      check.renderFlags({ available: false, latest: "9.9.9" });
      const away = getComputedStyle(el).display;
      check.renderFlags({ available: true, latest: "9.9.9" });
      const button = el.querySelector("[data-update-open]");
      return {
        away,
        shown: getComputedStyle(el).display,
        text: el.textContent.replace(/\s+/g, " ").trim(),
        title: el.title,
        button: button ? button.className : "",
      };
    });
    assert(entry, "no update entry in the burger menu");
    assert(entry.away === "none", "the update entry shows without an update");
    assert(entry.shown !== "none", "the update entry stayed hidden with an update available");
    assert(/^Update to 9\.9\.9$/.test(entry.text), `entry reads "${entry.text}"`);
    assert(entry.title === "Update to 9.9.9", `entry tooltip "${entry.title}"`);
    assert(/btn-primary/.test(entry.button) && /btn-sm/.test(entry.button), `the update is not a small primary button: ${entry.button}`);
  });

  await run("the desktop header keeps the wordmark and the menu-only rows stay off it", async () => {
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    // Two brands live in the layout, one per header breakpoint; this is the wide one.
    await page.waitForSelector(".navbar-collapse .navbar-brand", { timeout: 8000 });
    const brand = await page.$eval(".navbar-collapse .navbar-brand", (el) => el.textContent.trim());
    assert(/Dev Cockpit/.test(brand), `the wordmark is gone on a desktop: "${brand}"`);
    const hiddenOnWide = await page.$$eval("li.js-update-flag",
      (els) => els.every((el) => getComputedStyle(el).display === "none"));
    assert(hiddenOnWide, "the menu-only update row rendered into the desktop nav");
    // Two logouts live in the layout too: the icon in the wide header's control
    // cluster, and the spelled-out row in the burger menu, which keeps its words.
    const logout = await page.$eval('.navbar-collapse .col-auto form[action="/logout"] button', (el) => ({
      text: el.textContent.trim(),
      label: el.getAttribute("aria-label"),
    }));
    assert(logout.text === "", `logout still spells itself out: "${logout.text}"`);
    assert(logout.label === "Logout", `logout lost its label: ${logout.label}`);
    const menuLogout = await page.$eval('.navbar-nav form[action="/logout"] button', (el) => el.textContent.trim());
    assert(menuLogout === "Logout", `the menu row lost its words: "${menuLogout}"`);
  });
});
