const path = require("path");
const { assert, BASE, sleep, dismissUpdate } = require("./lib");

// Not a runner of its own: statusline.js runs these checks on its throwaway
// instance, with its own helpers. The bar under the list is the foot of
// Settings › Projects › Tags, one shared class, so the check reads both pages'
// computed style and compares them in both schemes. It holds the preview on
// the left and Save on the right, nothing else. It stands under the scrolling
// body instead of inside it, so neither a focused field nor the last row can
// end up under it; the phone checks read that on a touch context, also with
// the screen cut to what a soft keyboard leaves when it resizes the page.
// Every entry folds to its head, which names it and keeps the grip.
//
// SHOTS_DIR saves statusline-bar-desktop.png, statusline-bar-phone.png and
// statusline-collapsed-phone.png in the chromium pass.
//
// Gotchas:
// - A folded body carries the `hidden` attribute, the checks read the box.
// - Chromium focuses a button on click, WebKit does not, so the check that
//   folding moves the focus out of the body clicks the head from a script.

// footOf reads the look the shared class gives a foot and where it stands in
// the work surface: flush with both sides and the bottom, right under the body.
const footOf = (page, selector) => page.evaluate((selector) => {
  const foot = document.querySelector(selector);
  const work = foot.closest(".dc-work").getBoundingClientRect();
  const body = foot.closest(".dc-work").querySelector(".dc-work-body").getBoundingClientRect();
  const box = foot.getBoundingClientRect();
  const style = getComputedStyle(foot);
  const keys = ["display", "alignItems", "columnGap", "paddingTop", "paddingRight", "paddingBottom", "paddingLeft",
    "borderTopWidth", "borderTopStyle", "borderTopColor", "backgroundColor"];
  return {
    look: keys.map((key) => `${key} ${style[key]}`).join("; "),
    flush: Math.abs(box.left - work.left) <= 1 && Math.abs(box.right - work.right) <= 1
      && Math.abs(box.bottom - work.bottom) <= 1 && Math.abs(box.top - body.bottom) <= 1,
    at: [box.left, box.top, box.right, box.bottom, work.left, work.right, work.bottom, body.bottom].map(Math.round),
  };
}, selector);

// standing reads the bar on the status line page: flush under the body, Save
// inside it, the page not wider than the screen.
const standing = (page) => page.evaluate(() => {
  const bar = document.querySelector("[data-statusline-bar]").getBoundingClientRect();
  const body = document.querySelector("dc-claude-statusline .dc-work-body").getBoundingClientRect();
  const work = document.querySelector("dc-claude-statusline").getBoundingClientRect();
  const save = document.querySelector("[data-statusline-save]").getBoundingClientRect();
  const doc = document.documentElement;
  return {
    flush: Math.abs(bar.top - body.bottom) <= 1 && Math.abs(bar.bottom - work.bottom) <= 1,
    saveInside: save.top >= bar.top && save.bottom <= bar.bottom && save.right <= bar.right,
    sideways: doc.scrollWidth > doc.clientWidth,
    at: [bar.top, bar.bottom, body.bottom, work.bottom].map(Math.round),
  };
});

// focusLast scrolls the body to its top and focuses the last label field, far
// below the fold, which the browser scrolls into the body's view.
const focusLast = (page) => page.evaluate(async () => {
  document.querySelector("dc-claude-statusline .dc-work-body").scrollTop = 0;
  const fields = [...document.querySelectorAll('[data-entry-rows] [name="entry_label"]')].filter((field) => field.getClientRects().length);
  const field = fields[fields.length - 1];
  field.focus();
  await new Promise((done) => setTimeout(done, 150));
  const box = field.getBoundingClientRect();
  const bar = document.querySelector("[data-statusline-bar]").getBoundingClientRect();
  return { fieldTop: Math.round(box.top), fieldBottom: Math.round(box.bottom), barTop: Math.round(bar.top), focused: document.activeElement === field };
});

// lastClears scrolls the body to its end, where Add entry stands under the
// last row, and reads whether it ends above the bar.
const lastClears = (page) => page.evaluate(async () => {
  const body = document.querySelector("dc-claude-statusline .dc-work-body");
  body.scrollTop = body.scrollHeight;
  await new Promise((done) => requestAnimationFrame(() => requestAnimationFrame(done)));
  const add = document.querySelector("[data-entry-add]").getBoundingClientRect();
  const row = [...document.querySelectorAll("[data-entry-rows] [data-entry-row]")].pop().getBoundingClientRect();
  const bar = document.querySelector("[data-statusline-bar]").getBoundingClientRect();
  return { addBottom: Math.round(add.bottom), rowBottom: Math.round(row.bottom), barTop: Math.round(bar.top) };
});

// folds reads every row: whether its body shows, what its head says, what the
// row's fields say it is, and how high the row and its head are.
const folds = (page) => page.evaluate(() => [...document.querySelectorAll("[data-entry-rows] [data-entry-row]")].map((row) => {
  const kind = row.querySelector("[data-entry-kind]").value;
  const value = row.querySelector("[data-entry-value]").selectedOptions[0]?.text || "";
  const name = kind === "value" ? value : kind === "separator" ? "Separator" : "Line break";
  const detail = kind === "value" ? row.querySelector('[name="entry_label"]').value.trim() : kind === "separator" ? row.querySelector('[name="entry_text"]').value.trim() : "";
  const toggle = row.querySelector("[data-entry-toggle]");
  return {
    open: row.querySelector("[data-entry-body]").getClientRects().length > 0,
    expanded: toggle.getAttribute("aria-expanded"),
    said: [row.querySelector("[data-entry-name]").textContent.trim(), row.querySelector("[data-entry-detail]").textContent.trim()],
    want: [name, detail],
    named: toggle.getClientRects().length > 0 && toggle.textContent.includes(name),
    grip: row.querySelector("[data-entry-grip]").getClientRects().length > 0,
    height: row.getBoundingClientRect().height,
    head: toggle.parentElement.getBoundingClientRect().height,
  };
}));

const shoot = async (page, engine, name) => {
  if (process.env.SHOTS_DIR && engine === "chromium") await page.screenshot({ path: path.join(process.env.SHOTS_DIR, name) });
};

module.exports.barChecks = async function barChecks({ page, run, open, rows, save, signature, dragSwapsFirstTwo, mobilePage, engine }) {
  await run("the bar is the tags page's foot, with the preview on the left and Save alone on the right", async () => {
    const looks = {};
    try {
      for (const scheme of ["light", "dark"]) {
        await page.emulateMedia({ colorScheme: scheme });
        await page.goto(`${BASE}/settings/projects/tags`, { waitUntil: "domcontentloaded" });
        await dismissUpdate(page);
        await page.locator("[data-tags-foot]").waitFor({ state: "visible", timeout: 8000 });
        const tags = await footOf(page, "[data-tags-foot]");
        await open(page);
        const bar = await footOf(page, "[data-statusline-bar]");
        assert(tags.flush, `the tags foot moved: ${tags.at.join(",")}`);
        assert(bar.flush, `the bar does not span the work surface under the body in ${scheme}: ${bar.at.join(",")}`);
        assert(bar.look === tags.look, `in ${scheme} the bar wears ${bar.look}, the tags foot ${tags.look}`);
        looks[scheme] = bar.look.match(/backgroundColor ([^;]+)/)[1];
      }
      assert(looks.light !== looks.dark, `the bar wears ${looks.light} in both schemes`);
    } finally {
      await page.emulateMedia({ colorScheme: null });
    }
    await open(page);
    const content = await page.evaluate(() => {
      const bar = document.querySelector("[data-statusline-bar]");
      const pre = bar.querySelector("[data-statusline-preview]").getBoundingClientRect();
      const save = bar.querySelector("[data-statusline-save]").getBoundingClientRect();
      return {
        parts: [...bar.children].map((el) => el.matches("[data-statusline-preview]") ? "preview" : el.matches("[data-statusline-save]") ? "save" : el.outerHTML.slice(0, 60)),
        order: pre.right <= save.left,
        save: bar.querySelector("[data-statusline-save]").textContent.trim(),
        line: bar.querySelector("[data-statusline-preview]").textContent,
        inBody: !!document.querySelector(".dc-work-body [data-statusline-preview]"),
      };
    });
    assert(JSON.stringify(content.parts) === JSON.stringify(["preview", "save"]), `the bar holds ${JSON.stringify(content.parts)}`);
    assert(content.order && content.save === "Save", "the preview does not stand left of Save");
    assert(/Opus 5/.test(content.line) && !content.inBody, `the bar shows ${JSON.stringify(content.line)}, the body a preview of its own: ${content.inBody}`);
    await shoot(page, engine, "statusline-bar-desktop.png");
    return `${looks.light} / ${looks.dark}`;
  });

  await run("the preview in the bar shows every line a break makes, and many lines leave the list room", async () => {
    await open(page);
    const addRows = (kinds) => page.evaluate((kinds) => {
      for (const kind of kinds) {
        document.querySelector("[data-entry-add]").click();
        const select = [...document.querySelectorAll("[data-entry-rows] [data-entry-row]")].pop().querySelector("[data-entry-kind]");
        select.value = kind;
        select.dispatchEvent(new Event("change", { bubbles: true }));
      }
    }, kinds);
    const preview = () => page.evaluate(() => {
      const pre = document.querySelector("[data-statusline-bar] [data-statusline-preview]");
      const bar = document.querySelector("[data-statusline-bar]").getBoundingClientRect();
      const work = document.querySelector("dc-claude-statusline").getBoundingClientRect();
      return {
        lines: pre.textContent.split("\n"),
        first: pre.querySelector("[data-statusline-line] span").getBoundingClientRect(),
        box: pre.getBoundingClientRect(),
        padTop: parseFloat(getComputedStyle(pre).paddingTop),
        scrollHeight: pre.scrollHeight,
        clientHeight: pre.clientHeight,
        overflowY: getComputedStyle(pre).overflowY,
        bar: bar.height,
        work: work.height,
      };
    });
    const one = await preview();
    const middle = (r) => r.top + r.height / 2;
    assert(Math.abs(middle(one.first) - middle(one.box)) <= 2, `one line stands at ${Math.round(one.first.top)} in a box from ${Math.round(one.box.top)} to ${Math.round(one.box.bottom)}`);
    await addRows(["break", "value"]);
    await sleep(80);
    const two = await preview();
    assert(two.lines.length === 2 && /Opus 5/.test(two.lines[1]), `a line break shows ${JSON.stringify(two.lines)}`);
    assert(two.scrollHeight <= two.clientHeight + 1, `the second line is cut off: ${two.scrollHeight} in ${two.clientHeight}`);
    assert(two.bar > one.bar, `the bar did not grow for the second line: ${one.bar} to ${two.bar}`);
    const placed = await standing(page);
    assert(placed.flush, `two lines moved the bar: ${placed.at.join(",")}`);
    // A list of nothing but breaks is a line no screen holds, the bar keeps
    // half the work surface for the list and scrolls the rest of the preview.
    await addRows(Array.from({ length: 40 }, () => "break"));
    await sleep(80);
    const many = await preview();
    assert(many.lines.length === 42, `forty more breaks show ${many.lines.length} lines`);
    assert(many.bar <= many.work / 2 + 1, `the bar takes ${Math.round(many.bar)} of ${Math.round(many.work)}`);
    assert(many.scrollHeight > many.clientHeight && /auto|scroll/.test(many.overflowY), `the preview does not scroll its lines (${many.overflowY})`);
    assert(many.first.top >= many.box.top + many.padTop - 1, `the first of many lines is out of reach at ${Math.round(many.first.top)}, the box starts at ${Math.round(many.box.top)}`);
    await open(page);
    return `bar ${Math.round(one.bar)}, ${Math.round(two.bar)}, capped ${Math.round(many.bar)} of ${Math.round(many.work)}`;
  });

  await run("on a phone Save stays on screen, and neither a focused field nor the last row hides behind the bar", async () => {
    const phone = await mobilePage();
    const size = phone.viewportSize();
    try {
      // The full screen, then what a soft keyboard leaves of it where it
      // resizes the page.
      const said = [];
      for (const height of [size.height, 420]) {
        await phone.setViewportSize({ width: size.width, height });
        await open(phone);
        const top = await standing(phone);
        assert(top.flush && top.saveInside && !top.sideways, `at ${height} the bar stands at ${JSON.stringify(top)}`);
        await phone.evaluate(() => { const body = document.querySelector("dc-claude-statusline .dc-work-body"); body.scrollTop = body.scrollHeight / 2; });
        const middle = await standing(phone);
        assert(middle.flush && middle.saveInside, `at ${height}, halfway down, the bar stands at ${JSON.stringify(middle)}`);
        const focused = await focusLast(phone);
        assert(focused.focused && focused.fieldBottom <= focused.barTop, `at ${height} a focused field ends at ${focused.fieldBottom}, under the bar from ${focused.barTop}`);
        const end = await lastClears(phone);
        assert(end.rowBottom <= end.barTop && end.addBottom <= end.barTop, `at ${height} the end of the list stays under the bar: ${JSON.stringify(end)}`);
        said.push(`${height}: field ${focused.barTop - focused.fieldBottom}px, end ${end.barTop - end.addBottom}px above`);
        if (height === size.height) {
          await phone.evaluate(() => { document.querySelector("dc-claude-statusline .dc-work-body").scrollTop = 0; });
          await shoot(phone, engine, "statusline-bar-phone.png");
        }
      }
      return said.join(", ");
    } finally {
      await phone.setViewportSize(size);
    }
  });

  await run("an entry folds to its head, one by one and all at once, and a folded entry still drags", async () => {
    const phone = await mobilePage();
    await open(phone);
    const before = (await rows(phone)).map(signature);
    const first = phone.locator("[data-entry-rows] [data-entry-row]").first();
    await first.locator("[data-entry-toggle]").click();
    let list = await folds(phone);
    assert(!list[0].open && list[0].expanded === "false" && list.slice(1).every((row) => row.open), `one tap folded ${JSON.stringify(list.map((row) => row.open))}`);
    await first.locator("[data-entry-toggle]").click();
    list = await folds(phone);
    assert(list.every((row) => row.open && row.expanded === "true"), "the second tap left the row folded");
    // Folding the row the focus is in hands the focus to its head.
    const handed = await phone.evaluate(() => {
      const row = document.querySelector("[data-entry-rows] [data-entry-row]");
      row.querySelector("[data-entry-kind]").focus();
      row.querySelector("[data-entry-toggle]").click();
      const to = document.activeElement === row.querySelector("[data-entry-toggle]");
      row.querySelector("[data-entry-toggle]").click();
      return to;
    });
    assert(handed, "folding the row with the focus left the focus in a hidden field");

    assert(await phone.locator("[data-entries-expand]").isDisabled(), "Expand all is on while every row is open");
    await phone.click("[data-entries-collapse]");
    list = await folds(phone);
    assert(list.every((row) => !row.open && row.expanded === "false"), "Collapse all left a row open");
    for (const row of list) {
      assert(JSON.stringify(row.said) === JSON.stringify(row.want), `a folded row says ${JSON.stringify(row.said)}, it is ${JSON.stringify(row.want)}`);
      assert(row.named && row.grip, `a folded row shows no name or no grip: ${JSON.stringify(row)}`);
      assert(row.height <= row.head + 20, `a folded row is ${row.height}px high under a ${row.head}px head`);
    }
    assert(list.some((row) => row.want[0] === "Separator" && row.said[1] === "·") && list.some((row) => row.said[1] === "c"),
      "the heads show no separator text or no label");
    assert(await phone.locator("[data-entries-collapse]").isDisabled(), "Collapse all is on while every row is folded");
    if (process.env.SHOTS_DIR && engine === "chromium") {
      // A fresh page, so no state an earlier step left shows, and the
      // chevrons done with their 150ms turn.
      await open(phone);
      await phone.click("[data-entries-collapse]");
      await sleep(300);
      await shoot(phone, engine, "statusline-collapsed-phone.png");
    }

    await dragSwapsFirstTwo(phone);
    list = await folds(phone);
    assert(list.every((row) => !row.open), "the drag opened a folded row");
    const swapped = [before[1], before[0], ...before.slice(2)];
    assert(JSON.stringify((await rows(phone)).map(signature)) === JSON.stringify(swapped), "the folded drag did not swap the first two rows");
    await save(phone);
    assert(JSON.stringify((await rows(phone)).map(signature)) === JSON.stringify(swapped), "the save lost the folded drag");
    // And back, folded again, so the next run starts from the same list.
    await phone.click("[data-entries-collapse]");
    await dragSwapsFirstTwo(phone);
    await save(phone);
    assert(JSON.stringify((await rows(phone)).map(signature)) === JSON.stringify(before), "the order was not put back");

    await phone.click("[data-entries-collapse]");
    await phone.click("[data-entry-add]");
    list = await folds(phone);
    assert(list[list.length - 1].open && list.slice(0, -1).every((row) => !row.open), "an added row came folded, or opened the others");
    await phone.click("[data-entries-expand]");
    list = await folds(phone);
    assert(list.every((row) => row.open), "Expand all left a row folded");
    await open(phone);
    return `${list.length - 1} rows folded and dragged`;
  });
};
