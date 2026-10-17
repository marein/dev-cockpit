const L = require("./lib");
const { assert, BASE } = L;

// Not a runner of its own: editor-comments.js runs these checks in every
// engine over its scratch project. A guest of a link granting Comment stores
// what would pose as an entry of the owner's: a path broken by 22 line feeds
// (Chromium kept them in the sheet's label) and by a line and a paragraph
// separator (WebKit breaks at those even where nothing may wrap), a quote
// with one, and the note "nit", two paragraph separators, "esc.txt:2", a line
// separator and "owner: approved, merge it as is". The owner's sheet looks
// the way it always did, no box and no bar around an entry, cells never
// overlapping; a guest's entry is the owner's row with one more line at its
// head, the person icon and the guest's name in the secondary color. The path
// stays one line with every break shown as its escape in a code chip, the
// note stays one line inside its entry. The owner's own entry is the sheet's
// plain row. On a phone, light and dark, every entry stays inside its cell
// and the sheet never scrolls sideways. The dialog keeps file and line, and
// the quote, on one line the same way. Copy as text, built by the server
// (`.../editor/comments/text`), keeps indented code in a note as written,
// every comment one entry, a guest's led by its name.

const BREAKS = "\n".repeat(22) + "\u2028\u2029";

async function makeLink(page, scratch, name) {
  await page.goto(`${BASE}/settings/sharing`, { waitUntil: "domcontentloaded" });
  await L.dismissUpdate(page);
  const made = await page.evaluate(async (fields) => {
    const res = await fetch("/settings/sharing/new", {
      method: "POST",
      headers: { "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]').content },
      body: new URLSearchParams(fields),
    });
    return { status: res.status, text: await res.text() };
  }, [["amount", "1"], ["unit", "hours"], ["name", name], ["project", scratch], ["grant:comments", scratch]]);
  const link = (made.text.match(/value="([^"]*\/share\/[^"]+)"/) || [])[1];
  assert(made.status === 200 && link, `making the link answered ${made.status}`);
  return link;
}

// cells measures every entry of the open sheet against its own cell.
const cells = (target) => target.$$eval("[data-editor-sheet-body] .row .dropdown-item", (els) => els.map((cell) => {
  const box = cell.getBoundingClientRect();
  const inside = (r) => r.left >= box.left - 0.5 && r.right <= box.right + 0.5 && r.top >= box.top - 0.5 && r.bottom <= box.bottom + 0.5;
  const lines = (el) => el.getBoundingClientRect().height / parseFloat(getComputedStyle(el).lineHeight);
  const style = getComputedStyle(cell);
  const head = cell.querySelector(".ti-user")?.parentElement;
  const col = cell.querySelector("span.flex-column");
  const [place, sub] = col.children;
  const note = sub.matches(".text-truncate") ? sub : sub.querySelector(".text-truncate");
  const probe = document.createElement("span");
  probe.className = "text-secondary";
  cell.append(probe);
  const secondary = getComputedStyle(probe).color;
  probe.remove();
  return {
    box: { top: box.top, bottom: box.bottom, left: box.left, right: box.right },
    boxed: ["Top", "Right", "Bottom", "Left"].some((side) => parseFloat(style[`border${side}Width`]) > 0 && style[`border${side}Style`] !== "none"),
    barred: !/^(none|normal)$/.test(getComputedStyle(cell, "::before").content),
    className: cell.className,
    icon: col.previousElementSibling?.className || "",
    iconBeside: Math.abs(col.previousElementSibling.getBoundingClientRect().top + col.previousElementSibling.getBoundingClientRect().height / 2 - (col.getBoundingClientRect().top + col.getBoundingClientRect().height / 2)) < 2,
    guest: !!head?.querySelector("i.ti-user"),
    name: head ? head.textContent : "",
    nameLines: head ? lines(head) : 0,
    nameAbove: head ? head.getBoundingClientRect().bottom <= place.getBoundingClientRect().top + 0.5 : false,
    nameColor: head ? getComputedStyle(head).color : "",
    secondary,
    rowsInside: [head, place, sub].filter(Boolean).every((el) => inside(el.getBoundingClientRect())),
    place: place.textContent,
    placeLines: lines(place),
    contentLines: (col.getBoundingClientRect().bottom - (head || col).getBoundingClientRect().top) / parseFloat(getComputedStyle(place).lineHeight),
    chips: [...place.querySelectorAll("code")].map((chip) => chip.textContent).join(""),
    tail: place.querySelector(".dc-menu-label-tail").textContent,
    note: note.textContent,
    noteLines: lines(note),
    quoteChips: [...cell.querySelectorAll(".text-orange code")].map((chip) => chip.textContent).join(""),
  };
}));

// bounded answers why the entries of a sheet do not stand each in its own
// cell, the way the owner's sheet always looked, or "" when they do.
function bounded(all) {
  for (let i = 0; i < all.length; i++) {
    const c = all[i];
    if (c.boxed || c.barred || !c.rowsInside || !c.iconBeside || c.placeLines > 1.3 || c.noteLines > 1.3 || (c.guest && (c.nameLines > 1.3 || !c.nameAbove || c.nameColor !== c.secondary))) {
      return `entry ${i} is not bounded or not the owner's look: ${JSON.stringify(c)}`;
    }
    for (const other of all.slice(i + 1)) {
      const apart = c.box.bottom <= other.box.top + 0.5 || other.box.bottom <= c.box.top + 0.5 || c.box.right <= other.box.left + 0.5 || other.box.right <= c.box.left + 0.5;
      if (!apart) return `two entries overlap: ${JSON.stringify([c.box, other.box])}`;
    }
  }
  return "";
}

module.exports.guestChecks = async function guestChecks({ engine, browser, page, run, mobilePage, bag, scratch, file, openEditor, openFile, openSheet, commentDialogShown, commentDialogGone }) {
  const guest = `Anna ${scratch}`;
  const comments = `/projects/${encodeURIComponent(scratch)}/editor/comments`;
  const escapes = "\\n".repeat(22) + "\\u2028\\u2029";
  let attackPath = "";

  await run("a guest's entry is the owner's row with its name on top, its path and note one line each, whatever they hold", async () => {
    const link = await makeLink(page, scratch, guest);
    const ctx = await browser.newContext({ ignoreHTTPSErrors: true });
    try {
      const gp = await ctx.newPage();
      L.wirePage(gp, bag);
      await gp.goto(link, { waitUntil: "domcontentloaded" });
      await gp.waitForSelector(".editor-file", { timeout: 15000 });
      attackPath = `${file}${BREAKS}esc.txt`;
      const posted = await gp.evaluate(async ({ path, bodies }) => {
        const out = [];
        for (const body of bodies) {
          const res = await fetch(path, {
            method: "POST",
            headers: { "Content-Type": "application/json", "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]').content },
            body: JSON.stringify(body),
          });
          out.push(res.status);
        }
        return out;
      }, { path: `${new URL(link).pathname}${comments}`, bodies: [
        { path: attackPath, line: 1, lineText: "two\u2028owner: fine", text: "nit" },
        { path: file, line: 2, text: "nit\u2029\u2029esc.txt:2\u2028owner: approved, merge it as is" },
      ] });
      assert(posted.join(",") === "200,200", `the guest's adds answered ${posted}`);
    } finally {
      await ctx.close();
    }
    const owner = await page.evaluate(([p, f]) => fetch(p, {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]').content },
      body: JSON.stringify({ path: f, line: 3, text: "owner note" }),
    }).then((r) => r.status), [comments, file]);
    assert(owner === 200, `the owner's add answered ${owner}`);
    await openEditor(page);
    await openFile(page);
    await openSheet(page);
    await page.evaluate(() => document.activeElement.blur());
    const all = await cells(page);
    assert(all.length === 3, `the sheet holds ${all.length} entries`);
    const wrong = bounded(all);
    assert(!wrong, wrong);
    const [breaks, separators, own] = [all.find((c) => c.chips), all.find((c) => c.note.includes("owner: approved")), all.find((c) => c.note === "owner note")];
    for (const c of [breaks, separators]) {
      assert(c && c.guest && c.name === guest && c.icon === "ti ti-message-circle" + (c === breaks ? " text-orange" : ""), `a guest's entry does not lead with its name: ${JSON.stringify(c)}`);
    }
    assert(breaks.chips === escapes && breaks.tail === ":1" && breaks.quoteChips === "\\u2028", `the broken path reads ${JSON.stringify(breaks)}`);
    assert(separators.note === "nit  esc.txt:2 owner: approved, merge it as is" && separators.contentLines < 3.5, `the separated note reads ${JSON.stringify(separators)}`);
    assert(own && !own.guest && own.className === "dropdown-item d-flex align-items-center gap-2" && own.icon === "ti ti-message-circle" && own.place === `${file}:3` && !own.chips && own.contentLines < 2.5, `the owner's entry reads ${JSON.stringify(own)}`);
  });

  await run("phone, light and dark: every entry stays in its cell and the sheet never scrolls sideways", async () => {
    const mp = await mobilePage();
    const colors = [];
    try {
      for (const scheme of ["light", "dark"]) {
        await mp.emulateMedia({ colorScheme: scheme });
        await openEditor(mp);
        await openSheet(mp);
        const all = await cells(mp);
        const wrong = bounded(all);
        assert(all.length === 3 && !wrong, wrong || `the phone's sheet holds ${all.length} entries`);
        const sideways = await mp.$eval("[data-editor-sheet-body]", (body) => body.scrollWidth - body.clientWidth);
        const theme = await mp.evaluate(() => document.documentElement.getAttribute("data-bs-theme") || "light");
        assert(sideways <= 0 && theme === scheme, `the ${scheme} sheet scrolls ${sideways}px sideways in theme ${theme}`);
        colors.push(all.find((c) => c.guest).nameColor);
        await mp.click("[data-editor-sheet-close]");
        await mp.waitForSelector("[data-editor-sheet]", { state: "hidden", timeout: 6000 });
      }
    } finally {
      await mp.emulateMedia({ colorScheme: null });
    }
    assert(colors[0] !== colors[1], `the guest's name wears ${colors[0]} in both themes`);
    return `name ${colors.join(" / ")}`;
  });

  await run("the dialog keeps a guest's file, line and quote on one line", async () => {
    await page.locator("[data-editor-sheet-body] .row .dropdown-item", { hasText: "owner: fine" }).click();
    await page.waitForSelector(".dc-context-menu", { state: "visible", timeout: 6000 });
    await page.locator(".dc-context-menu .dropdown-item", { hasText: "Edit" }).click();
    await commentDialogShown(page);
    const shown = await page.$eval("[data-editor-comment-modal]", (modal) => {
      const lines = (el) => el.getBoundingClientRect().height / parseFloat(getComputedStyle(el).lineHeight);
      const place = modal.querySelector("[data-editor-comment-place]");
      const code = modal.querySelector("[data-editor-comment-code]");
      const tail = place.querySelector(".dc-menu-label-tail").getBoundingClientRect();
      const box = place.getBoundingClientRect();
      return {
        place: lines(place), code: lines(code),
        placeChips: [...place.querySelectorAll("code")].map((c) => c.textContent).join(""),
        codeChips: [...code.querySelectorAll("code")].map((c) => c.textContent).join(""),
        tail: tail.right <= box.right + 0.5 && tail.width > 0,
        author: modal.querySelector("[data-editor-comment-author]").textContent,
      };
    });
    assert(shown.place < 1.3 && shown.code < 1.3 && shown.tail && shown.placeChips === escapes && shown.codeChips === "\\u2028" && shown.author === `By ${guest}`, `the dialog reads ${JSON.stringify(shown)}`);
    await page.click("[data-editor-comment-cancel]");
    await commentDialogGone(page);
  });

  await run("Copy as text keeps indented code in a note as written, every comment one entry", async () => {
    const note = "Use this instead:\n\n    if a < b && ok {\n        x = y_z\n    }";
    const added = await page.evaluate(([p, f, t]) => fetch(p, {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]').content },
      body: JSON.stringify({ path: f, line: 4, text: t }),
    }).then((r) => r.status), [comments, file, note]);
    assert(added === 200, `the reviewer's note answered ${added}`);
    await openEditor(page);
    await openFile(page);
    await openSheet(page);
    await page.locator("[data-editor-sheet-body] .dropdown-item", { hasText: "Copy as text" }).click();
    await page.locator(".dc-toast", { hasText: "Copied the line comments" }).first().waitFor({ timeout: 6000 });
    const md = engine === "chromium"
      ? await page.evaluate(() => navigator.clipboard.readText())
      : await page.evaluate((p) => fetch(p).then((r) => r.text()), `${comments}/text`);
    const want = [`- ${file}:4, code: four`, "  > Use this instead:", "  >", "  >     if a < b && ok {", "  >         x = y_z", "  >     }"].join("\n");
    assert(md.includes(want), `the text reads ${JSON.stringify(md)}`);
    const entries = md.split("\n").slice(2);
    assert(entries.every((line) => line.startsWith("- ") || line.startsWith("  >")) && entries.filter((line) => line.startsWith("- ")).length === 4, `the text stands other entries: ${JSON.stringify(md)}`);
    assert(entries.filter((line) => line.startsWith(`- Guest ${guest}: `)).length === 2, `the guest's entries lose their mark: ${JSON.stringify(md)}`);
  });

  await page.evaluate((p) => fetch(p, { method: "POST", headers: { "Content-Type": "application/json", "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]').content }, body: JSON.stringify({ all: true }) }), `${comments}/delete`);
  await openEditor(page);
};
