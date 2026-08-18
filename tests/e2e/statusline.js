const L = require("./lib");
const { assert, BASE, sleep } = L;

// The claude status line settings, `/settings/coders/claude/statusline`: one
// ordered list of entries that is the line itself, a preview that follows it
// while it is edited, and a save that writes the line the cockpit's own binary
// draws for every claude session it starts.
//
// Routes: GET/POST /settings/coders/<coder>/statusline, claude only, one form
// carrying the mode and every row, one POST. POST .../statusline/state answers
// the picker and a refusal for the rows as they stand, nothing is stored.
// Custom element: dc-claude-statusline (rows, kind and value switching, the
// bounds, the grip drag, folding, the preview in the bar under the list, the
// state asked again on every edit). The bar, the folding and the phone checks
// are in `statusline-bar.js`.
//
// It edits a setting of its instance and writes the line into its state dir,
// so it belongs on a throwaway instance; the mode check ends on the fallback,
// which is the state an untouched instance is in.
//
// Gotchas:
// - The element upgrades lazily, so every check waits for `.rows` before it
//   drives anything, the way docker.js waits before its drag.
// - The lift the drag paints comes from style.css and hangs on the shared
//   hooks (`.dc-drag-list`, `[data-drag-row]`, `.dc-drag-lift`), not on one
//   element name. The check reads the computed style of a carried row on both
//   settings pages and compares them, so the two lists cannot drift apart.
// - The horizontal overflow check only bites in WebKit: a select's widest
//   option keeps its intrinsic width there and overflows the column the box
//   was shrunk into, while chromium clips it. Run the cross browser pass
//   (`ENGINE=chromium,webkit`) or the check passes on a page that scrolls on
//   a phone.
// - A row hides the parts its kind does not mean with the `hidden` attribute.
//   Reading the attribute proves nothing (style.css decides), so visibility is
//   read as a real one.
// - The bounds of a row travel as a flat list plus the count the row carries.
//   A row that is no number posts none of them, which is what the count check
//   after a kind switch is about.

const PAGE = `${BASE}/settings/coders/claude/statusline`;

const ready = (page) => page.waitForFunction(() => !!document.querySelector("dc-claude-statusline")?.rows, null, { timeout: 8000 });

async function open(page) {
  await page.goto(PAGE, { waitUntil: "domcontentloaded" });
  await L.dismissUpdate(page);
  await ready(page);
}

const rows = (page) => page.evaluate(() => [...document.querySelectorAll("[data-entry-rows] [data-entry-row]")].map((row) => ({
  kind: row.querySelector("[data-entry-kind]").value,
  value: row.querySelector("[data-entry-value]").value,
  label: row.querySelector('[name="entry_label"]').value,
  text: row.querySelector('[name="entry_text"]').value,
  count: Number(row.querySelector("[data-threshold-count]").value),
  bounds: [...row.querySelectorAll("[data-threshold-row]")].map((bound) => ({
    at: bound.querySelector('[name="threshold_at"]').value,
    color: bound.querySelector('[name="threshold_color"]').value,
    disabled: bound.querySelector('[name="threshold_at"]').disabled,
  })),
})));

// liftedStyle starts a drag on the first row of a list, reads what the page
// paints on the row while it is carried, and cancels the drag again. Nothing
// is dropped and nothing is posted, so it leaves the list as it was.
const liftedStyle = (page, rowSelector, gripSelector) => page.evaluate(([rows, grips]) => {
  const row = document.querySelector(rows);
  if (!row) return { missing: true };
  const grip = row.querySelector(grips);
  const box = grip.getBoundingClientRect();
  const x = Math.round(box.left + box.width / 2);
  const y = Math.round(box.top + box.height / 2);
  const send = (type, at) => grip.dispatchEvent(new PointerEvent(type, {
    bubbles: true, cancelable: true, pointerId: 91, pointerType: "touch", isPrimary: true,
    clientX: x, clientY: at, buttons: type === "pointercancel" ? 0 : 1,
  }));
  send("pointerdown", y);
  send("pointermove", y + 12);
  const style = getComputedStyle(row);
  const read = {
    lifted: row.classList.contains("dc-drag-lift"),
    host: !!row.closest(".dc-drag-list"),
    background: style.backgroundColor,
    shadow: style.boxShadow,
    position: style.position,
    zIndex: style.zIndex,
    transition: style.transitionProperty,
  };
  send("pointercancel", y + 12);
  return read;
}, [rowSelector, gripSelector]);

const previewText = (page) => page.locator("[data-statusline-preview]").evaluate((el) => el.textContent);
const previewColors = (page) => page.locator("[data-statusline-preview]").evaluate((el) =>
  [...el.querySelectorAll("span")].map((span) => getComputedStyle(span).color));

// submit posts one of the page's two forms and waits for the section flash
// the redirect brings back. The flash standing from an earlier save is taken
// down first: waiting for a selector that is already there resolves at once
// and the check would then read the page it just left.
async function submit(page, selector) {
  await page.evaluate(() => document.querySelectorAll("dc-claude-statusline .alert").forEach((alert) => alert.remove()));
  await page.click(selector);
  await page.waitForSelector("dc-claude-statusline .alert", { timeout: 8000 });
  await ready(page);
}

const save = (page) => submit(page, "[data-statusline-save]");

// pickPreset opens the picker and follows one of its rows. The rows stand in a
// closed menu, so they are only there to click once the picker is open.
async function pickPreset(page, id) {
  await page.click("[data-statusline-presets-toggle]");
  await page.locator(`[data-statusline-preset="${id}"]`).click();
  await page.waitForURL(new RegExp(`preset=${id}`));
  await ready(page);
}

const presetShown = (page, id) => page.locator(`[data-statusline-preset="${id}"]`).isVisible();

// says reads every place that tells whether the list is saved and waits until
// they all give the one answer wanted: the closed picker's name and badge, the
// badge of the menu row it names and the flash a save leaves. The bar next to
// Save says nothing about it. The answer is the server's after every edit, a
// local request that is back long before the three seconds run out.
async function says(page, name, saved) {
  const read = () => page.evaluate(() => {
    const shown = (el) => !!el && el.getClientRects().length > 0;
    const toggle = document.querySelector("[data-statusline-presets-toggle]");
    const row = document.querySelector('[data-statusline-preset][aria-current="true"]');
    return {
      name: toggle?.querySelector("span:not(.badge)")?.textContent.trim() || "",
      badge: toggle?.querySelector(".badge")?.textContent.trim() || "",
      rowTitle: row?.querySelector(".fw-medium")?.textContent.trim() || "",
      rowBadge: row?.querySelector(".badge")?.textContent.trim() || "",
      bar: [...(document.querySelector("[data-statusline-bar]")?.children || [])].filter((el) => !el.matches("[data-statusline-preview]")).map((el) => el.textContent.trim()).join(" "),
      flash: shown(document.querySelector("[data-statusline-flash] .alert-success")),
    };
  });
  const badge = saved ? "Saved" : "Not saved";
  const agrees = (r) => r.name === name && r.badge === badge && r.bar === "Save" && !(r.flash && !saved)
    && (name === "Your own line" ? r.rowTitle === "" : r.rowTitle === name && r.rowBadge === badge);
  const deadline = Date.now() + 3000;
  let said = await read();
  while (!agrees(said) && Date.now() < deadline) {
    await sleep(50);
    said = await read();
  }
  assert(agrees(said), `the page says ${JSON.stringify(said)}, want ${name} · ${badge}`);
}

const entryOf = (page, value) => page.locator("[data-entry-rows] [data-entry-row]", { has: page.locator(`[data-entry-value] option[value="${value}"]:checked`) }).first();

// signature is what a row means once posted: the kind and only the fields that
// kind carries, so a separator is not read by the value select it hides.
const signature = (row) => JSON.stringify(row.kind === "value"
  ? [row.kind, row.value, row.label, row.text, row.bounds.map((b) => [b.at, b.color])]
  : [row.kind, row.text]);

// stampRows marks every row with its place, so a drag is read off the rows
// themselves and two rows that mean the same still tell apart.
const stampRows = (page) => page.evaluate(() => {
  document.querySelectorAll("[data-entry-rows] [data-entry-row]").forEach((row, i) => { row.dataset.e2eRow = String(i); });
});
const stamps = (page) => page.$$eval("[data-entry-rows] [data-entry-row]", (list) => list.map((row) => row.dataset.e2eRow));

async function dragSwapsFirstTwo(page) {
  await stampRows(page);
  const before = await stamps(page);
  assert(before.length >= 2, `expected at least two rows, got ${before.length}`);
  await dragFirstRowDown(page);
  const after = await stamps(page);
  const want = [before[1], before[0], ...before.slice(2)];
  assert(JSON.stringify(after) === JSON.stringify(want), `the drag did not swap the first two rows: ${after.join(",")}`);
}

// dragFirstRowDown carries the first row past the second one's center, the
// finger gesture docker.js drives on the compose actions. The rows differ in
// height, a separator is shorter than a value with bounds, so the distance is
// measured between the centers, which is what the drop is decided by.
const dragFirstRowDown = (page) => page.evaluate(async () => {
  const list = [...document.querySelectorAll("[data-entry-rows] [data-entry-row]")];
  const grip = list[0].querySelector("[data-entry-grip]");
  const box = grip.getBoundingClientRect();
  const x = Math.round(box.left + box.width / 2);
  const y0 = Math.round(box.top + box.height / 2);
  const lift = 8;
  const center = (row) => { const r = row.getBoundingClientRect(); return r.top + r.height / 2; };
  const raw = center(list[1]) - center(list[0]) + 10;
  const send = (type, y) => grip.dispatchEvent(new PointerEvent(type, {
    bubbles: true, cancelable: true, pointerId: 71, pointerType: "touch", isPrimary: true,
    clientX: x, clientY: y, buttons: type === "pointerup" ? 0 : 1,
  }));
  send("pointerdown", y0);
  send("pointermove", y0 + lift);
  await new Promise((done) => setTimeout(done, 16));
  for (let i = 1; i <= 10; i++) {
    send("pointermove", Math.round(y0 + lift + (raw * i) / 10));
    await new Promise((done) => setTimeout(done, 16));
  }
  send("pointerup", y0 + lift + raw);
});

L.runFeature("STATUSLINE", async ({ page, run, mobilePage, engine }) => {
  await run("the coder settings carry a Status line section, claude alone", async () => {
    await page.goto(`${BASE}/settings/coders/claude/instructions`, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    assert(await page.locator('[data-coder-sections] a[href$="/statusline"]').count() === 1, "no Status line tab on the claude pages");
    await open(page);
    assert(await page.locator('[data-coder-sections] a.active[href$="/statusline"]').count() === 1, "the Status line tab is not marked");
    // Switching the coder must never land on a page that coder has not, so a
    // second coder's sidebar row keeps the instructions instead.
    const others = await page.evaluate(() => [...document.querySelectorAll("[data-settings-coder]")]
      .filter((row) => row.dataset.settingsCoder !== "claude")
      .map((row) => row.getAttribute("href")));
    for (const href of others) {
      assert(href.endsWith("/instructions"), `a second coder's row points at ${href}`);
      const response = await page.request.get(`${BASE}${href.replace(/\/instructions$/, "/statusline")}`);
      assert(response.status() === 404, `${href} has a status line page with status ${response.status()}`);
    }
    return others.length ? `${others.length} other coder checked` : "single coder host";
  });

  await run("the defaults are the Essentials line", async () => {
    await open(page);
    const list = await rows(page);
    const values = list.filter((row) => row.kind === "value").map((row) => row.value);
    assert(JSON.stringify(values) === JSON.stringify(["model", "effort", "context", "cache_left", "session", "week"]),
      `the default values are ${values.join(", ")}`);
    assert(list.filter((row) => row.kind === "separator").length === 4, "the default line has no four separators");
    // One character per label, the same in every preset, for a phone's width.
    const labels = list.filter((row) => row.kind === "value").map((row) => row.label).join("");
    assert(labels === "c◔5w", `the default labels are ${labels}`);
    const context = list.find((row) => row.value === "context");
    assert(context.count === 3 && context.bounds.length === 3, "a number does not carry its three bounds");
    assert(context.bounds.map((b) => b.color).join(",") === "green,yellow,red", "the bounds are not green, yellow, red");
    assert(context.bounds.map((b) => b.at).join(",") === "0,50,80", `the bounds sit at ${context.bounds.map((b) => b.at).join(",")}`);
    const model = list.find((row) => row.value === "model");
    assert(model.count === 0 && model.bounds.length === 0, "a value that is no number carries bounds");
    const effort = list.findIndex((row) => row.kind === "value" && row.value === "effort");
    assert(list[effort - 1]?.kind === "value" && list[effort - 1].value === "model", "a separator stands between the model and its effort");
    assert(await entryOf(page, "effort").locator("[data-entry-color]").inputValue() === "yellow", "the effort is not yellow");
    const cache = list.find((row) => row.value === "cache_left");
    assert(cache.bounds.map((b) => `${b.at}:${b.color}`).join(",") === "0:red,1:green",
      `the cache carries ${JSON.stringify(cache.bounds)}, want red when cold and green from a minute`);
  });

  await run("a preset fills the list without saving it, and Save takes it", async () => {
    await open(page);
    const toggle = page.locator("[data-statusline-presets-toggle]");
    const rowIds = await page.$$eval("[data-statusline-preset]", (list) => list.map((row) => row.dataset.statuslinePreset));
    assert(JSON.stringify(rowIds) === JSON.stringify(["essentials", "api", "subscription"]), `the presets are ${rowIds.join(", ")}`);
    const titles = await page.$$eval("[data-statusline-preset] .fw-medium", (list) => list.map((title) => title.textContent.trim()));
    assert(JSON.stringify(titles) === JSON.stringify(["Essentials", "API key", "Subscription"]), `the presets read ${titles.join(", ")}`);
    // Closed, the picker is one control as high as the mode select above it,
    // and it names the saved preset.
    const heights = await page.evaluate(() => ({
      picker: document.querySelector("[data-statusline-presets-toggle]").getBoundingClientRect().height,
      mode: document.querySelector("[data-statusline-mode]").getBoundingClientRect().height,
    }));
    assert(heights.picker <= heights.mode + 1, `the closed picker is ${heights.picker}px high, the mode select ${heights.mode}px`);
    assert(!(await presetShown(page, "essentials")), "the preset rows stand on the page while the picker is closed");
    await says(page, "Essentials", true);

    // Open, every row shows its line, painted by the renderer the preview uses.
    await toggle.click();
    for (const id of rowIds) assert(await presetShown(page, id), `the open picker hides ${id}`);
    const previews = await page.$$eval("[data-preset-preview]", (list) => list.map((pre) => pre.textContent));
    assert(previews.length === 3 && previews.every((text) => /Opus 5/.test(text)), `a row shows no line: ${JSON.stringify(previews)}`);
    assert(previews[1] === "Opus 5 high · c 42% · ◔ 4m · $1.24 · >200k" && /F 82%/.test(previews[2]), `the rows show ${JSON.stringify(previews)}`);
    await page.keyboard.press("Escape");
    await page.locator('[data-statusline-preset="essentials"]').waitFor({ state: "hidden" });
    const saved = JSON.stringify(await rows(page));

    await pickPreset(page, "api");
    await says(page, "API key", false);
    assert(await page.locator('[data-statusline-preset="essentials"] [data-badge="saved"]').count() === 1, "the saved preset lost its mark while another one is shown");
    const shown = (await rows(page)).filter((row) => row.kind === "value").map((row) => row.value);
    assert(JSON.stringify(shown) === JSON.stringify(["model", "effort", "context", "cache_left", "cost", "over_200k"]), `the API key line is ${shown.join(", ")}`);
    assert(/\$1\.24/.test(await previewText(page)), "the preview does not follow the preset");
    // Leaving without a save keeps the saved line.
    await open(page);
    assert(JSON.stringify(await rows(page)) === saved, "looking at a preset changed the saved line");
    await says(page, "Essentials", true);

    await pickPreset(page, "subscription");
    await save(page);
    await says(page, "Subscription", true);
    // Back to the default, so the next check starts where this one did.
    await pickPreset(page, "essentials");
    await save(page);
    await says(page, "Essentials", true);
    assert(JSON.stringify(await rows(page)) === saved, "the default line did not come back as it was");
    return "viewed, saved, back";
  });

  await run("the page says saved or not saved in one voice, live with the list", async () => {
    await open(page);
    await says(page, "Essentials", true);
    const label = entryOf(page, "context").locator('[name="entry_label"]');
    // Picking the preset that is the saved line is no change.
    await pickPreset(page, "essentials");
    await says(page, "Essentials", true);

    // A saved own line, then a preset over it edited back to that line.
    await label.fill("cx");
    await says(page, "Your own line", false);
    await save(page);
    await says(page, "Your own line", true);
    assert(await page.locator("[data-statusline-flash] .alert-success").isVisible(), "the save left no flash");
    await pickPreset(page, "essentials");
    await says(page, "Essentials", false);
    await label.fill("cx");
    await says(page, "Your own line", true);

    // The flash of a save goes with the first edit and stays gone.
    await open(page);
    await label.fill("cy");
    await save(page);
    await label.fill("cz");
    await says(page, "Your own line", false);
    assert(await page.locator("[data-statusline-flash]").count() === 0, "the flash of the save outlived an edit");
    await label.fill("cy");
    await says(page, "Your own line", true);

    // A bound that is no number cannot be saved, and its row says why, also
    // folded, while the bar keeps out of it.
    const bound = entryOf(page, "context").locator('[name="threshold_at"]').first();
    await bound.fill("x");
    const error = entryOf(page, "context").locator("[data-entry-error] [data-statusline-error]");
    await error.waitFor({ state: "visible", timeout: 3000 });
    assert(/"x"/.test(await error.textContent()), `the row says ${await error.textContent()}`);
    assert(await page.locator("[data-statusline-error]").count() === 1, "the refusal stands in more than one place");
    assert(await page.locator("[data-statusline-bar] [data-statusline-error]").count() === 0, "the refusal stands in the bar");
    await entryOf(page, "context").locator("[data-entry-toggle]").click();
    assert(await error.isVisible(), "a folded row hides its refusal");
    await entryOf(page, "context").locator("[data-entry-toggle]").click();
    const toggle = (await page.locator("[data-statusline-presets-toggle]").textContent()).replace(/\s+/g, " ").trim();
    assert(toggle === "Your own line Not saved", `the picker says ${JSON.stringify(toggle)} next to a list Save refuses`);
    assert(await page.locator("[data-statusline-save]").isDisabled(), "Save takes a bound that is no number");
    await bound.fill("0");
    await says(page, "Your own line", true);
    assert(!(await page.locator("[data-statusline-save]").isDisabled()), "Save stays off after the bound was fixed");
    assert(await page.locator("[data-statusline-error]").count() === 0, "the refusal outlived the fixed bound");

    await pickPreset(page, "essentials");
    await save(page);
    await says(page, "Essentials", true);
    return "preset picked again, edited back to a saved own line, flash, bad bound";
  });

  await run("an answer leaves an open picker open, and one edit asks once", async () => {
    await open(page);
    let asked = 0;
    const count = (request) => { if (request.url().endsWith("/statusline/state")) asked++; };
    page.on("request", count);
    try {
      const label = entryOf(page, "context").locator('[name="entry_label"]');
      await label.fill("cx");
      await says(page, "Your own line", false);
      // Leaving the field fires change after the input, the list is the same.
      await label.press("Tab");
      await sleep(300);
      assert(asked === 1, `one edit asked ${asked} times`);
      await page.click("[data-statusline-presets-toggle]");
      await page.locator('[data-statusline-preset="essentials"]').waitFor({ state: "visible" });
      const setLabel = (text) => page.evaluate((text) => {
        const row = [...document.querySelectorAll("[data-entry-rows] [data-entry-row]")].find((r) => r.querySelector("[data-entry-value]")?.value === "context");
        const input = row.querySelector('[name="entry_label"]');
        input.value = text;
        input.dispatchEvent(new Event("input", { bubbles: true }));
      }, text);
      await setLabel("c");
      await says(page, "Essentials", true);
      assert(await presetShown(page, "essentials"), "the answer closed the open picker");
      assert(await page.locator("[data-statusline-presets-toggle]").getAttribute("aria-expanded") === "true", "the toggle says the picker is closed");
      await setLabel("cx");
      await says(page, "Your own line", false);
      assert(await presetShown(page, "api"), "the second answer closed the open picker");
      await setLabel("c");
      await says(page, "Essentials", true);
      await page.keyboard.press("Escape");
      return `${asked} requests`;
    } finally {
      page.off("request", count);
    }
  });

  await run("the closed picker follows the list while it is edited, and a reload says the same", async () => {
    await open(page);
    await says(page, "Essentials", true);
    const saved = JSON.stringify(await rows(page));
    const label = entryOf(page, "context").locator('[name="entry_label"]');

    await dragSwapsFirstTwo(page);
    await says(page, "Your own line", false);
    await dragSwapsFirstTwo(page);
    await says(page, "Essentials", true);

    await label.fill("cx");
    await says(page, "Your own line", false);
    await label.fill("c");
    await says(page, "Essentials", true);

    await page.click("[data-entry-add]");
    await says(page, "Your own line", false);
    await page.locator("[data-entry-rows] [data-entry-row]").last().locator("[data-entry-remove]").click();
    await says(page, "Essentials", true);

    const color = entryOf(page, "model").locator("[data-entry-color]");
    await color.selectOption("red");
    await says(page, "Your own line", false);
    await color.selectOption("cyan");
    await says(page, "Essentials", true);

    // A preset the list was opened with stays not saved while it is edited
    // away from and back to.
    await pickPreset(page, "api");
    await says(page, "API key", false);
    await label.fill("cx");
    await says(page, "Your own line", false);
    await label.fill("c");
    await says(page, "API key", false);

    // A saved own line reads the same rendered by the page after a reload
    // as answered live for the same list.
    await open(page);
    await label.fill("cx");
    await says(page, "Your own line", false);
    await save(page);
    await says(page, "Your own line", true);
    await page.reload({ waitUntil: "domcontentloaded" });
    await ready(page);
    await says(page, "Your own line", true);
    await label.fill("cy");
    await says(page, "Your own line", false);
    await label.fill("cx");
    await says(page, "Your own line", true);

    await pickPreset(page, "essentials");
    await says(page, "Essentials", false);
    await save(page);
    await says(page, "Essentials", true);
    assert(JSON.stringify(await rows(page)) === saved, "the default line did not come back as it was");
    return "change, add, remove, reorder, from a preset, saved own line";
  });

  await run("the preview paints the line and follows an edit without a save", async () => {
    await open(page);
    const before = await previewText(page);
    assert(/Opus 5/.test(before) && /42%/.test(before) && /·/.test(before), `the preview reads ${JSON.stringify(before)}`);
    // The weekly values have their stand-in like every other value, so the
    // preview shows the whole list, not only what a machine could answer.
    assert(/16%/.test(before) && /69%/.test(before), "the preview drops values it has stand-ins for");
    const colors = await previewColors(page);
    assert(new Set(colors).size > 1, "the preview paints everything in one color");

    const contextLabel = page.locator("[data-entry-row]", { has: page.locator('[data-entry-value] option[value="context"]:checked') }).first();
    await contextLabel.locator('[name="entry_label"]').fill("ctx");
    await sleep(50);
    assert(/ctx/.test(await previewText(page)), "the preview did not follow the label");

    // The bound the sample value reaches decides its color, so moving that
    // bound repaints it without anything being saved.
    const painted = await page.evaluate(() => {
      const row = [...document.querySelectorAll("[data-entry-row]")].find((r) => r.querySelector("[data-entry-value]")?.value === "context");
      const spans = () => [...document.querySelectorAll("[data-statusline-preview] span")];
      const value = () => spans().find((span) => span.textContent.includes("42%"));
      const before = getComputedStyle(value()).color;
      const bound = row.querySelectorAll("[data-threshold-row]")[0];
      bound.querySelector('[name="threshold_color"]').value = "red";
      bound.querySelector('[name="threshold_color"]').dispatchEvent(new Event("change", { bubbles: true }));
      return { before, after: getComputedStyle(value()).color };
    });
    assert(painted.before !== painted.after, `the value stayed ${painted.before} after its bound changed`);
  });

  await run("the values stand in groups, and the free text is the entry's own", async () => {
    await open(page);
    const groups = await page.evaluate(() => {
      const select = document.querySelector("[data-entry-value]");
      return [...select.querySelectorAll("optgroup")].map((group) => ({
        label: group.label,
        values: [...group.querySelectorAll("option")].map((option) => option.value),
      }));
    });
    const labels = groups.map((group) => group.label);
    for (const want of ["Coder", "Context", "Tokens", "Cost", "Limits", "Git", "Place", "System", "Free"]) {
      assert(labels.includes(want), `the select has no ${want} group: ${labels.join(", ")}`);
    }
    const flat = groups.flatMap((group) => group.values);
    for (const want of ["model", "effort", "fast", "thinking", "context_size", "over_200k", "tokens_cache_read", "cache_left", "session_cache_read", "burn", "cost_turn", "lines_added", "week_top", "git_stashes", "pr", "dir_full", "host", "text", "command"]) {
      assert(flat.includes(want), `the select does not offer ${want}`);
    }
    assert(new Set(flat).size === flat.length, "a value is offered twice");
    // claude redraws the line after every request, so the rise it shows is one
    // request and the option must not promise a whole turn.
    const costLabel = await page.$eval('[data-entry-value] option[value="cost_turn"]', (option) => option.textContent.trim());
    assert(costLabel === "Cost, last request", `the cost rise is offered as "${costLabel}"`);

    // The free text carries what is typed, in the same field the separator
    // uses, and the preview shows exactly that instead of a stand-in.
    await page.click("[data-entry-add]");
    const added = page.locator("[data-entry-rows] [data-entry-row]").last();
    await added.locator("[data-entry-value]").selectOption("text");
    await sleep(50);
    assert(await added.locator('[data-entry-part="text"]').isVisible(), "the free text has no field to type in");
    await added.locator('[name="entry_text"]').fill("on eax");
    await sleep(80);
    assert(/on eax/.test(await previewText(page)), "the preview does not show the typed text");
    // The command takes its line in the same field, named for what it is and
    // held to the length the save takes instead of the length a part of the
    // line is held to, and the preview stands in for its output, which only a
    // redraw can know.
    await added.locator("[data-entry-value]").selectOption("command");
    await sleep(50);
    assert(await added.locator('[data-entry-part="text"]').isVisible(), "the command has no field to type in");
    const textLabel = (await added.locator("[data-entry-text-label]").textContent()).trim();
    assert(textLabel === "Command", `the command's field is called "${textLabel}"`);
    const commandMax = await added.locator('[name="entry_text"]').getAttribute("maxlength");
    assert(commandMax === "1024", `the command line is held to ${commandMax} characters, want the 1024 the save takes`);
    await added.locator('[name="entry_text"]').fill("~/bin/line --short");
    await sleep(80);
    assert(/output/.test(await previewText(page)) && !/line --short/.test(await previewText(page)), "the preview shows the command line instead of standing in for its output");
    // The weekly limit of one model takes the model's name in the same field.
    await added.locator("[data-entry-value]").selectOption("week_top");
    await sleep(50);
    assert(await added.locator('[data-entry-part="text"]').isVisible(), "the weekly limit of one model has no field for the model");
    const modelLabel = (await added.locator("[data-entry-text-label]").textContent()).trim();
    assert(modelLabel === "Model", `the model's field is called "${modelLabel}"`);
    // Back to the free text, the field is held to a part of the line again.
    await added.locator("[data-entry-value]").selectOption("text");
    await sleep(50);
    assert(await added.locator('[name="entry_text"]').getAttribute("maxlength") === "24", "the free text lost its length");
    // Nothing was saved, so a fresh page is the list as it was.
    await open(page);
    assert(!/on eax/.test(await previewText(page)), "the unsaved text survived a reload");
    return `${groups.length} groups, ${flat.length} values`;
  });

  await run("a command entry keeps its whole line through a save", async () => {
    await open(page);
    const before = (await rows(page)).length;
    const line = "/usr/local/bin/my-status-line --format short --with 'two words'";
    await page.click("[data-entry-add]");
    const added = page.locator("[data-entry-rows] [data-entry-row]").last();
    await added.locator("[data-entry-value]").selectOption("command");
    await added.locator('[name="entry_text"]').fill(line);
    await save(page);
    await page.reload({ waitUntil: "domcontentloaded" });
    await ready(page);
    const list = await rows(page);
    const command = list.find((row) => row.value === "command");
    assert(command && command.text === line, `the command came back as ${JSON.stringify(command)}`);
    const last = page.locator("[data-entry-rows] [data-entry-row]").last();
    assert((await last.locator("[data-entry-text-label]").textContent()).trim() === "Command", "a saved command row does not name its field");
    await last.locator("[data-entry-remove]").click();
    await save(page);
    assert((await rows(page)).length === before, "the removed command came back");
    return `${line.length} characters`;
  });

  await require("./statusline-bounds").boundChecks({ page, run, open, rows });

  await run("a row becomes a separator and a number brings its bounds", async () => {
    await open(page);
    const first = page.locator("[data-entry-rows] [data-entry-row]").first();
    await first.locator("[data-entry-kind]").selectOption("separator");
    await sleep(50);
    assert(!(await first.locator('[data-entry-part="value"]').first().isVisible()), "the value part stands on a separator row");
    assert(await first.locator('[data-entry-part="text"]').isVisible(), "the text field is missing on a separator row");
    await first.locator("[data-entry-kind]").selectOption("value");
    await sleep(50);
    assert(await first.locator('[data-entry-part="value"]').first().isVisible(), "the value part stayed away");

    // A new row starts on a value that is no number, so it shows one color.
    await page.click("[data-entry-add]");
    await sleep(50);
    const added = page.locator("[data-entry-rows] [data-entry-row]").last();
    assert(await added.locator('[data-entry-part="color"]').isVisible(), "a text value shows no color select");
    assert(!(await added.locator('[data-entry-part="thresholds"]').isVisible()), "a text value shows bounds");
    await added.locator("[data-entry-value]").selectOption("cost");
    await sleep(50);
    assert(await added.locator('[data-entry-part="thresholds"]').isVisible(), "a number shows no bounds");
    assert(!(await added.locator('[data-entry-part="color"]').isVisible()), "a number still shows the fixed color");
    const list = await rows(page);
    const last = list[list.length - 1];
    assert(last.count === 1 && last.bounds.length === 1, `a fresh number carries ${last.count} bounds`);

    // Back to a separator: the bounds stay in the row but must not travel, or
    // the flat list would be read into the next row that has some.
    await added.locator("[data-entry-kind]").selectOption("separator");
    await sleep(50);
    const off = (await rows(page)).pop();
    assert(off.count === 0, `a separator row still posts ${off.count} bounds`);
    assert(off.bounds.every((bound) => bound.disabled), "a separator row's bounds still travel");
  });

  await run("the grip drags a row and the save keeps the new order", async () => {
    await open(page);
    const before = (await rows(page)).map(signature);
    assert(before[0] !== before[1], `the first two rows mean the same, a swap could not be told: ${before[0]}`);
    await dragSwapsFirstTwo(page);
    const dragged = (await rows(page)).map(signature);
    const swapped = [before[1], before[0], ...before.slice(2)];
    assert(JSON.stringify(dragged) === JSON.stringify(swapped), `the rows read ${dragged.join(", ")} after the drag`);
    await save(page);
    const saved = (await rows(page)).map(signature);
    assert(JSON.stringify(saved) === JSON.stringify(swapped), `the save lost the order: ${saved.join(", ")}`);
    await page.reload({ waitUntil: "domcontentloaded" });
    await ready(page);
    const reloaded = (await rows(page)).map(signature);
    assert(JSON.stringify(reloaded) === JSON.stringify(swapped), `the reload lost the order: ${reloaded.join(", ")}`);
    // And back the way it was, so the list this runner leaves behind is the
    // one it found and a second run starts from the same place.
    await dragSwapsFirstTwo(page);
    await save(page);
    const restored = (await rows(page)).map(signature);
    assert(JSON.stringify(restored) === JSON.stringify(before), `the order was not put back: ${restored.join(", ")}`);
  });

  await run("an added entry survives the save with its label and its bounds", async () => {
    await open(page);
    await page.click("[data-entry-add]");
    const added = page.locator("[data-entry-rows] [data-entry-row]").last();
    await added.locator("[data-entry-value]").selectOption("branch");
    await added.locator('[name="entry_label"]').fill("git");
    await added.locator("[data-entry-label-color]").selectOption("magenta");
    await save(page);
    const list = await rows(page);
    const branch = list.find((row) => row.value === "branch");
    assert(branch && branch.label === "git", "the added entry is gone after the save");
    assert(await page.locator('[data-entry-row] [data-entry-label-color] option[value="magenta"]:checked').count() === 1, "the label color did not survive");
    // And out again, so the list is the one the next check starts from.
    const removed = list.length - 1;
    await page.locator("[data-entry-rows] [data-entry-row]").last().locator("[data-entry-remove]").click();
    await save(page);
    assert((await rows(page)).length === removed, "the removed entry came back");
  });

  await run("the mode select says when the line is shown, and no mode touches the list", async () => {
    await open(page);
    const mode = page.locator("[data-statusline-mode]");
    assert(await mode.count() === 1, "the page has no mode select");
    // It is the first thing on the page, above everything the list needs.
    const shape = await page.evaluate(() => {
      const root = document.querySelector("dc-claude-statusline");
      const mark = root.querySelector("[data-statusline-mode]");
      const rows = root.querySelector("[data-entry-rows]");
      const preview = root.querySelector("[data-statusline-preview]");
      return {
        beforeRows: !!(mark.compareDocumentPosition(rows) & Node.DOCUMENT_POSITION_FOLLOWING),
        beforePreview: !!(mark.compareDocumentPosition(preview) & Node.DOCUMENT_POSITION_FOLLOWING),
        tag: mark.tagName,
        options: [...mark.options].map((option) => option.value),
      };
    });
    assert(shape.tag === "SELECT", `the mode is a ${shape.tag}, want a select`);
    assert(JSON.stringify(shape.options) === JSON.stringify(["always", "fallback", "off"]), `the modes are ${shape.options.join(",")}`);
    assert(shape.beforeRows && shape.beforePreview, "the mode does not stand first on the page");
    assert(await mode.inputValue() === "fallback", `an untouched instance stands on ${await mode.inputValue()}, want the fallback`);
    const fallback = await mode.locator('option[value="fallback"]').textContent();
    assert(fallback === "If ~/.claude/settings.json sets none", `the fallback reads ${JSON.stringify(fallback)}`);
    // The mode is read when a coder starts or resumes, the list at every
    // redraw, and the hint says exactly that and names the one file the
    // fallback looks at.
    const hint = (await mode.evaluate((el) => el.closest(".mb-3").querySelector(".form-text").textContent)).trim();
    assert(hint === "The mode is read when a coder starts or resumes, the list at its next redraw. The fallback checks only ~/.claude/settings.json.",
      `the mode's hint reads ${JSON.stringify(hint)}`);

    const listBefore = JSON.stringify(await rows(page));
    for (const want of ["always", "off", "fallback"]) {
      await page.locator("[data-statusline-mode]").selectOption(want);
      await save(page);
      const flash = (await page.locator("[data-statusline-flash] .dc-notice-text").textContent()).trim();
      assert(flash === "Settings saved.", `saving ${want} says ${JSON.stringify(flash)}`);
      await page.reload({ waitUntil: "domcontentloaded" });
      await ready(page);
      const got = await page.locator("[data-statusline-mode]").inputValue();
      assert(got === want, `the mode ${want} came back as ${got}`);
      assert(JSON.stringify(await rows(page)) === listBefore, `the mode ${want} changed the list`);
    }
    return "always, off and back to the fallback, list untouched";
  });

  await run("a carried row is painted like the compose actions', not see through", async () => {
    // The compose actions are the original of this gesture, so their lift is
    // the reference. Both lists have to read the same rules, which is what a
    // shared hook buys and what an element name in the selector took away.
    await page.goto(`${BASE}/settings/docker`, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    await page.waitForFunction(() => !!document.querySelector("dc-docker-actions")?.rows, null, { timeout: 8000 });
    const docker = await liftedStyle(page, "#settings-docker-actions [data-action-row]", "[data-action-grip]");
    assert(!docker.missing, "the docker settings carry no compose action to drag");
    assert(docker.lifted && docker.host, "the compose action was not carried at all");
    assert(docker.background !== "rgba(0, 0, 0, 0)" && docker.background !== "transparent",
      `the carried compose action has no surface (${docker.background})`);

    await open(page);
    const entry = await liftedStyle(page, "[data-entry-rows] [data-entry-row]", "[data-entry-grip]");
    assert(!entry.missing, "the status line carries no entry to drag");
    assert(entry.lifted && entry.host, "the status line entry was not carried at all");
    for (const key of ["background", "shadow", "position", "zIndex", "transition"]) {
      assert(entry[key] === docker[key], `a carried entry has ${key} ${entry[key]}, the compose actions have ${docker[key]}`);
    }
    return `both ${docker.background}`;
  });

  await require("./statusline-bar").barChecks({ page, run, open, rows, save, signature, dragSwapsFirstTwo, mobilePage, engine });

  await run("a phone width never scrolls sideways, only the preview does", async () => {
    await page.setViewportSize({ width: 390, height: 844 });
    try {
      await open(page);
      // A filled list and a line that is far wider than the screen: the value
      // selects carry the longest words on the page, the preview the longest
      // line, and neither may push the page sideways.
      const list = await rows(page);
      assert(list.length >= 8, `the list is too short to say anything (${list.length} rows)`);
      // A separator row carries no label, and which row stands first depends
      // on what is stored, so the label goes into the first value row.
      const label = page.locator('[data-entry-rows] [data-entry-row][data-entry-numeric]:has([data-entry-part="value"]:not([hidden]))').first().locator('[name="entry_label"]');
      await label.fill("a very long label here");
      await sleep(80);
      const measured = await page.evaluate(() => {
        const doc = document.documentElement;
        const pre = document.querySelector("[data-statusline-preview]");
        return {
          scrollWidth: doc.scrollWidth,
          clientWidth: doc.clientWidth,
          preScroll: pre.scrollWidth,
          preClient: pre.clientWidth,
          preOverflow: getComputedStyle(pre).overflowX,
        };
      });
      assert(measured.preScroll > measured.preClient, "the preview line is not longer than its box, the check proves nothing");
      assert(measured.preOverflow === "auto" || measured.preOverflow === "scroll", `the preview does not scroll itself (${measured.preOverflow})`);
      assert(measured.scrollWidth <= measured.clientWidth,
        `the page scrolls sideways at 390: scrollWidth ${measured.scrollWidth} over clientWidth ${measured.clientWidth}`);
      // The open picker stays inside the screen too, its rows carry a
      // description and a line each.
      await page.click("[data-statusline-presets-toggle]");
      await page.locator('[data-statusline-preset="subscription"]').waitFor({ state: "visible" });
      const menu = await page.evaluate(() => {
        const box = document.querySelector("[data-statusline-presets] .dropdown-menu").getBoundingClientRect();
        return { left: box.left, right: box.right, scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth };
      });
      await page.keyboard.press("Escape");
      assert(menu.left >= 0 && menu.right <= menu.clientWidth && menu.scrollWidth <= menu.clientWidth,
        `the open picker runs off the screen at 390: ${JSON.stringify(menu)}`);
      return `page ${measured.scrollWidth}, preview ${measured.preScroll} in ${measured.preClient}, picker ${Math.round(menu.right - menu.left)}`;
    } finally {
      await page.setViewportSize({ width: 1360, height: 900 });
    }
  });
});
