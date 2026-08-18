const L = require("./lib");
const { assert, BASE, sleep } = L;

// The claude status line settings, `/settings/coders/claude/statusline`: one
// ordered list of entries that is the line itself, a preview that follows it
// while it is edited, and a save that writes the script the cockpit hands to
// every claude session it starts.
//
// Routes: GET/POST /settings/coders/<coder>/statusline, claude only, one form
// carrying the switch and every row, one POST.
// Custom element: dc-claude-statusline (rows, kind and value switching, the
// bounds, the grip drag, the preview).
//
// It edits a setting of its instance and writes a script into its state dir,
// so it belongs on a throwaway instance; the last check hands the status line
// back to claude, which is the state an untouched instance is in.
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

// rows reads the list the way the form posts it: kind, value, label, and how
// many bounds the row says it carries.
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

const save = (page) => submit(page, 'dc-claude-statusline form button[type="submit"]');

// dragFirstRowDown carries the first row past the second one, the finger
// gesture docker.js drives on the compose actions.
const dragFirstRowDown = (page) => page.evaluate(async () => {
  const list = [...document.querySelectorAll("[data-entry-rows] [data-entry-row]")];
  const grip = list[0].querySelector("[data-entry-grip]");
  const box = grip.getBoundingClientRect();
  const x = Math.round(box.left + box.width / 2);
  const y0 = Math.round(box.top + box.height / 2);
  const lift = 8;
  const raw = list[1].getBoundingClientRect().top - list[0].getBoundingClientRect().top + 10;
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

L.runFeature("STATUSLINE", async ({ page, run }) => {
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

  await run("the defaults are the line that runs today", async () => {
    await open(page);
    const list = await rows(page);
    const values = list.filter((row) => row.kind === "value").map((row) => row.value);
    assert(JSON.stringify(values) === JSON.stringify(["model", "context", "session", "week", "week_top", "reset"]),
      `the default values are ${values.join(", ")}`);
    assert(list.filter((row) => row.kind === "separator").length === 4, "the default line has no four separators");
    const labels = list.filter((row) => row.kind === "value").map((row) => row.label).join("");
    assert(labels === "c5wF↻", `the default labels are ${labels}`);
    const context = list.find((row) => row.value === "context");
    assert(context.count === 3 && context.bounds.length === 3, "a number does not carry its three bounds");
    assert(context.bounds.map((b) => b.color).join(",") === "green,yellow,red", "the bounds are not green, yellow, red");
    assert(context.bounds.map((b) => b.at).join(",") === "0,50,80", `the bounds sit at ${context.bounds.map((b) => b.at).join(",")}`);
    const model = list.find((row) => row.value === "model");
    assert(model.count === 0 && model.bounds.length === 0, "a value that is no number carries bounds");
    const reset = list.find((row) => row.value === "reset");
    assert(reset.bounds.length === 1 && reset.bounds[0].color === "blue" && reset.bounds[0].at === "0",
      `the reset entry carries ${JSON.stringify(reset.bounds)}, want one blue bound at zero`);
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
    for (const want of ["model", "context_size", "tokens_cache_read", "session_cache_read", "burn", "cost_turn", "lines_added", "week_top", "git_stashes", "dir_full", "host", "text"]) {
      assert(flat.includes(want), `the select does not offer ${want}`);
    }
    assert(new Set(flat).size === flat.length, "a value is offered twice");

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
    // Nothing was saved, so a fresh page is the list as it was.
    await open(page);
    assert(!/on eax/.test(await previewText(page)), "the unsaved text survived a reload");
    return `${groups.length} groups, ${flat.length} values`;
  });

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
    const before = (await rows(page)).map((row) => row.value || row.kind);
    await dragFirstRowDown(page);
    const dragged = (await rows(page)).map((row) => row.value || row.kind);
    assert(dragged[0] === before[1] && dragged[1] === before[0], `the drag did not swap the first two rows: ${dragged.join(", ")}`);
    await save(page);
    const saved = (await rows(page)).map((row) => row.value || row.kind);
    assert(JSON.stringify(saved) === JSON.stringify(dragged), `the save lost the order: ${saved.join(", ")}`);
    await page.reload({ waitUntil: "domcontentloaded" });
    await ready(page);
    const reloaded = (await rows(page)).map((row) => row.value || row.kind);
    assert(JSON.stringify(reloaded) === JSON.stringify(dragged), `the reload lost the order: ${reloaded.join(", ")}`);
    // And back the way it was, so the list this runner leaves behind is the
    // one it found and a second run starts from the same place.
    await dragFirstRowDown(page);
    await save(page);
    const restored = (await rows(page)).map((row) => row.value || row.kind);
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

  await run("the switch decides whether it is in effect, and off keeps the list", async () => {
    await open(page);
    const box = page.locator("[data-statusline-enabled]");
    assert(await box.count() === 1, "the page has no switch");
    // It is the first thing on the page, above everything the list needs.
    const order = await page.evaluate(() => {
      const root = document.querySelector("dc-claude-statusline");
      const mark = root.querySelector("[data-statusline-enabled]");
      const rows = root.querySelector("[data-entry-rows]");
      const preview = root.querySelector("[data-statusline-preview]");
      return {
        beforeRows: !!(mark.compareDocumentPosition(rows) & Node.DOCUMENT_POSITION_FOLLOWING),
        beforePreview: !!(mark.compareDocumentPosition(preview) & Node.DOCUMENT_POSITION_FOLLOWING),
        type: mark.getAttribute("type"),
      };
    });
    assert(order.type === "checkbox", `the switch is a ${order.type}, want a plain checkbox`);
    assert(order.beforeRows && order.beforePreview, "the switch does not stand first on the page");
    assert(/claude/i.test(await page.locator("[data-statusline-enabled]").evaluate((el) => el.closest(".mb-3").textContent)),
      "the switch does not say what off means");

    const listBefore = JSON.stringify(await rows(page));
    await box.check();
    await save(page);
    assert(await page.locator("[data-statusline-enabled]").isChecked(), "the switch did not stay on");
    assert(JSON.stringify(await rows(page)) === listBefore, "switching on changed the list");

    // Off is the case this exists for: it must keep every entry.
    await page.locator("[data-statusline-enabled]").uncheck();
    await save(page);
    await page.reload({ waitUntil: "domcontentloaded" });
    await ready(page);
    assert(!(await page.locator("[data-statusline-enabled]").isChecked()), "the switch did not stay off");
    assert(JSON.stringify(await rows(page)) === listBefore, "switching off threw the list away");
    return "on and off, list untouched";
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
      return `page ${measured.scrollWidth}, preview ${measured.preScroll} in ${measured.preClient}`;
    } finally {
      await page.setViewportSize({ width: 1360, height: 900 });
    }
  });
});
