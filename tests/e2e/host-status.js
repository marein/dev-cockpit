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
// The panel's Float button lifts the three values into dc-host-float, a
// draggable one-row card of ring gauges (value inside the ring, ring colored by
// the shared thresholds, plain numbers as tooltips) the layout mounts once next
// to the assistant panel, outside the region pe.js swaps: a boosted navigation
// leaves the element standing (asserted through element identity). Open state
// and position live in localStorage (dc-host-float); the card clamps itself
// back into the viewport on restore, drag and resize. z-order: 1045 over what
// stands (assistant panel 1040, fullscreen views 1030, sticky footers 10), and
// a body:has duck rule drops it to 5 while anything that asks for interaction
// is open (dropdowns incl. the quick nav, modals, dialogs, context menus, the
// switcher, the editor's quick open), because the strip's dropdowns live inside
// a z-10 sticky context no fixed number could respect.
//
// Gotchas:
// - the layout mounts one dc-host-status per header breakpoint, like the bell,
//   so every selector here is scoped to a header: DESKTOP for the wide one,
//   MOBILE for the compact one. An unscoped query hits the hidden twin first.
// - the float shares the [data-host-row] hooks with the dropdowns; float checks
//   scope to dc-host-float for the same reason the header checks scope.
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

  await run("mobile: the float squashes to mini bars with the value underneath", async () => {
    const mp = await mobilePage();
    await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await mp.waitForSelector(`${MOBILE} dc-host-status`, { timeout: 8000 });
    await mp.click(`${MOBILE} [data-host-toggle]`);
    await mp.waitForSelector(`${MOBILE} dc-host-status .dropdown-menu.show`, { timeout: 4000 });
    await mp.click(`${MOBILE} [data-host-float-open]`);
    await mp.waitForSelector("dc-host-float", { state: "visible", timeout: 4000 });
    const shape = await mp.evaluate(() => {
      const gauge = document.querySelector('dc-host-float [data-host-chip="cpu"]');
      const fill = gauge.querySelector(".dc-host-gauge-mini-fill");
      return {
        ring: getComputedStyle(gauge.querySelector(".dc-host-gauge-ring")).display,
        name: getComputedStyle(gauge.querySelector(".dc-host-gauge-name")).display,
        mini: getComputedStyle(gauge.querySelector(".dc-host-gauge-mini")).display,
        value: getComputedStyle(gauge.querySelector(".dc-host-gauge-mini-value")).display,
        fillHeight: fill.style.height,
        shown: gauge.querySelector(".dc-host-gauge-mini-value").textContent,
        title: gauge.title,
        height: document.querySelector("dc-host-float").offsetHeight,
      };
    });
    assert(shape.ring === "none" && shape.name === "none", `ring/name still shown on the phone: ${JSON.stringify(shape)}`);
    assert(shape.mini !== "none" && shape.value !== "none", `mini bar hidden on the phone: ${JSON.stringify(shape)}`);
    const value = Math.min(100, Number(shape.shown.replace("%", "")));
    assert(shape.fillHeight === `${value}%`, `fill ${shape.fillHeight} for ${shape.shown}`);
    assert(/^CPU · /.test(shape.title), `the tooltip does not name the metric: ${shape.title}`);
    assert(shape.height <= 44, `the phone card is ${shape.height}px tall, want <= 44`);
    await mp.click("dc-host-float [data-host-float-close]");
    return `${shape.height}px tall`;
  });

  await run("detach floats the panel, closes the dropdown, and the card carries the readings", async () => {
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await page.waitForSelector(`${DESKTOP} dc-host-status`, { timeout: 8000 });
    await page.click(`${DESKTOP} [data-host-toggle]`);
    await page.waitForSelector(`${DESKTOP} dc-host-status .dropdown-menu.show`, { timeout: 4000 });
    await page.click(`${DESKTOP} [data-host-float-open]`);
    await page.waitForSelector("dc-host-float", { state: "visible", timeout: 4000 });
    assert(!(await page.$(`${DESKTOP} dc-host-status .dropdown-menu.show`)), "the dropdown stayed open after detaching");
    const gauges = await page.$$eval("dc-host-float [data-host-chip]", (els) => els
      .filter((el) => getComputedStyle(el).display !== "none")
      .map((el) => ({
        key: el.dataset.hostChip,
        value: el.querySelector(".js-host-value").textContent,
        dash: el.querySelector(".dc-host-gauge-bar").getAttribute("stroke-dasharray"),
        title: el.title,
      })));
    assert(gauges.length >= 1 && gauges.every((g) => /%$/.test(g.value)), `float gauges: ${JSON.stringify(gauges)}`);
    for (const gauge of gauges) {
      const value = Math.min(100, Number(gauge.value.replace("%", "")));
      assert(gauge.dash.startsWith(`${value} `), `${gauge.key} ring dash ${gauge.dash} for ${gauge.value}`);
      assert(gauge.title.trim().length > 0, `${gauge.key} gauge carries no tooltip with the plain numbers`);
    }
    // No plain-numbers line in the card: the sentence lives in the tooltip only.
    assert((await page.locator("dc-host-float .js-host-label").count()) === 0, "the float still carries label lines");
    const inside = await page.$eval("dc-host-float", (el) => {
      const box = el.getBoundingClientRect();
      return box.left >= 0 && box.right <= window.innerWidth && box.top >= 0;
    });
    assert(inside, "the fresh float is not inside the viewport");
    const width = await page.$eval("dc-host-float", (el) => el.offsetWidth);
    assert(width < 260, `the card is not slim: ${width}px wide`);
    return gauges.map((g) => `${g.key} ${g.value}`).join(", ") + ` (${width}px)`;
  });

  await run("the float lives outside the swapped region: a boosted navigation keeps the element", async () => {
    await page.evaluate(() => { document.querySelector("dc-host-float").dataset.probe = "kept"; });
    await page.click('.navbar-collapse a[href="/docs"]');
    await page.waitForURL(/\/docs/, { timeout: 8000 });
    await sleep(400);
    const kept = await page.evaluate(() => ({
      probe: document.querySelector("dc-host-float")?.dataset.probe,
      visible: document.querySelector("dc-host-float") && !document.querySelector("dc-host-float").hidden,
      insidePage: !!document.querySelector("[data-page-content] dc-host-float"),
    }));
    assert(kept.probe === "kept", "the navigation re-created the float element");
    assert(kept.visible, "the float closed on navigation");
    assert(!kept.insidePage, "the float sits inside data-page-content and would flicker");
  });

  await run("dragging the card by its head persists, and a reload restores the spot", async () => {
    const grip = await page.locator("dc-host-float [data-host-float-grip]").boundingBox();
    await page.mouse.move(grip.x + grip.width / 2, grip.y + grip.height / 2);
    await page.mouse.down();
    await page.mouse.move(300, 400, { steps: 8 });
    await page.mouse.up();
    const before = await page.$eval("dc-host-float", (el) => ({ x: el.style.left, y: el.style.top }));
    await page.reload({ waitUntil: "domcontentloaded" });
    await page.waitForSelector("dc-host-float", { state: "visible", timeout: 8000 });
    const after = await page.$eval("dc-host-float", (el) => ({ x: el.style.left, y: el.style.top }));
    assert(before.x === after.x && before.y === after.y,
      `float moved across the reload: ${JSON.stringify(before)} -> ${JSON.stringify(after)}`);
  });

  await run("a shrinking window pushes the card back into view", async () => {
    await page.evaluate(() => {
      const el = document.querySelector("dc-host-float");
      el.place(window.innerWidth - el.offsetWidth - 8, 100);
    });
    const parked = await page.$eval("dc-host-float", (el) => el.getBoundingClientRect().right);
    assert(parked > 700, `expected the card at the right edge, right=${parked}`);
    await page.setViewportSize({ width: 700, height: 700 });
    await sleep(300);
    const clamped = await page.$eval("dc-host-float", (el) => {
      const box = el.getBoundingClientRect();
      return { right: box.right, bottom: box.bottom };
    });
    assert(clamped.right <= 700, `the card hangs outside after the resize: right=${clamped.right}`);
    assert(clamped.bottom <= 700, `the card hangs below after the resize: bottom=${clamped.bottom}`);
    await page.setViewportSize({ width: 1360, height: 900 });
    await sleep(200);
  });

  await run("z-order: over panel, fullscreen and footers; ducks under anything that pops up", async () => {
    const z = await page.$eval("dc-host-float", (el) => Number(getComputedStyle(el).zIndex));
    assert(z === 1045, `float z-index ${z}, want 1045 (over the 1040 panel and the 1030 fullscreen views)`);
    const steady = await page.evaluate(() => {
      const results = {};
      for (const mode of ["dc-terminal-fullscreen", "dc-editor-fullscreen"]) {
        document.documentElement.classList.add(mode);
        results[mode] = Number(getComputedStyle(document.querySelector("dc-host-float")).zIndex);
        document.documentElement.classList.remove(mode);
      }
      return results;
    });
    assert(steady["dc-terminal-fullscreen"] === 1045 && steady["dc-editor-fullscreen"] === 1045,
      `fullscreen z ${JSON.stringify(steady)}, want 1045 over the 1030 views`);
    // Any open popup has to win: an open dropdown ducks the card under everything.
    await page.click(".dc-notify-bell:visible");
    await page.waitForSelector(".dc-notify-menu.show", { timeout: 4000 });
    const ducked = await page.$eval("dc-host-float", (el) => Number(getComputedStyle(el).zIndex));
    assert(ducked === 5, `float z with an open dropdown ${ducked}, want 5 (under everything)`);
    await page.keyboard.press("Escape");
    await page.waitForFunction(() => !document.querySelector(".dropdown-menu.show"), null, { timeout: 4000 });
    const back = await page.$eval("dc-host-float", (el) => Number(getComputedStyle(el).zIndex));
    assert(back === 1045, `float z after closing the dropdown ${back}, want 1045`);
  });

  await run("the cross closes the float and the closed state survives a reload", async () => {
    await page.click("dc-host-float [data-host-float-close]");
    await page.waitForSelector("dc-host-float", { state: "hidden", timeout: 4000 });
    await page.reload({ waitUntil: "domcontentloaded" });
    await page.waitForSelector(`${DESKTOP} dc-host-status`, { timeout: 8000 });
    await sleep(300);
    const hidden = await page.$eval("dc-host-float", (el) => el.hidden);
    assert(hidden, "the float came back after being closed");
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
