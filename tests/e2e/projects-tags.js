const L = require("./lib");
const { assert, sleep, BASE } = L;

// Not a runner of its own: projects.js runs these checks over its scratch
// projects, a plain directory and a repository with a worktree, once they
// stand. Tags are maintained on Settings › Projects › Tags
// (/settings/projects/tags, dc-tags-settings), reached from the pencil at the
// end of the tag bar: a row per tag with its color, its name and the projects
// carrying it, a link to the board filtered by it. Add tag stages a new tag
// in the next free color, which lives with no project and is offered to
// every project, as every tag lives on until it is removed here. Nothing is
// written before Save: a rename onto another tag's name shows the merge with
// the result's color, Remove is staged with Undo, the footer counts the changes, Discard
// drops them, and a save that removes asks through the cockpit's confirm.
// With no tags the page says so alone, without the filter and Select all.
// Selected rows merge (the dialog asks which name and color live on and names
// the projects), take a color or go at once. A page loaded before a change
// elsewhere saves nothing and keeps what it sent. On the board a row names its
// tags above its terminals, folds past eight behind +N and ends with the
// dashed way into its tags dialog. Picked tags stand in the filter field, the
// bar counts what the filter leaves and dims a tag that would leave nothing,
// on a desktop it keeps one line with +N more. The dialog shows the project's
// tags first, a comma ends a tag, a second spelling adds none and a new tag
// keeps the color it shows. The page form offers the tags in use, and a
// refusal shows the form again at its address with what was sent. A tag
// filter leaves the worktrees of its mains folded, the text filters and both
// switchers find a worktree by its main's tag, every color reads 4.5:1 on its
// tint, and deleting a project takes it off its tags, which live on. The list
// column and the switchers show one disc with a slice per tag. The instance
// is shared with whatever else it serves, so every tag here carries the run's
// own suffix and no check touches a tag it did not make.

const MODAL = "[data-form-modal].show";
const SETTINGS = "dc-tags-settings";
const TAG_COLORS = ["blue", "azure", "indigo", "purple", "pink", "red", "orange", "yellow", "lime", "green", "teal", "cyan"];

// onChip is a chip on top of a tags form, found by the name it shows.
function onChip(scope, name) {
  return `${scope} [data-tag-on]:has([data-tag-name]:text-is("${name}"))`;
}

async function setTags(page, name, tags, colors = {}) {
  const token = await page.evaluate(() => document.querySelector('meta[name="csrf-token"]').content);
  const fields = [["csrf_token", token], ...tags.map((t) => ["new", t]), ...Object.entries(colors).map(([t, c]) => [`new-color:${t}`, c])];
  const res = await page.request.post(`${BASE}/projects/${name}/tags?modal=1`, {
    data: new URLSearchParams(fields).toString(),
    headers: { Accept: "application/json", "Content-Type": "application/x-www-form-urlencoded" },
  });
  assert(res.ok(), `the tags of ${name} answered ${res.status()} ${await res.text()}`);
}

// idOf answers the id the board's bar knows a tag by.
async function idOf(page, name) {
  const deadline = Date.now() + 8000;
  while (Date.now() < deadline) {
    const id = await page.evaluate((n) => Array.from(document.querySelectorAll("dc-project-list [data-tag-bar] [data-tag-toggle]")).find((b) => b.querySelector(".dc-tag-name").textContent === n)?.dataset.tagToggle, name);
    if (id) return id;
    await sleep(200);
  }
  throw new Error(`the bar offers no tag ${name}`);
}

// rowTags lists the tags a board row names, as name:color:id.
function rowTags(page, name) {
  return page.$$eval(`#project-${name} [data-tag-line] [data-tag-toggle]`, (els) => els.map((e) => {
    const color = [...e.classList].map((c) => c.match(/^dc-tag-(\w+)$/)).find(Boolean);
    return `${e.querySelector(".dc-tag-name").textContent}:${color ? color[1] : ""}:${e.dataset.tag}`;
  }));
}

async function waitRowTags(page, name, wanted, missing) {
  const deadline = Date.now() + 8000;
  let got = [];
  while (Date.now() < deadline) {
    got = await rowTags(page, name);
    if (wanted.every((w) => got.includes(w)) && !got.some((g) => missing.some((m) => g.toLowerCase().startsWith(`${m.toLowerCase()}:`)))) return got;
    await sleep(200);
  }
  throw new Error(`${name} shows ${JSON.stringify(got)}, want ${JSON.stringify(wanted)} without ${JSON.stringify(missing)}`);
}

function shown(page, selector) {
  return page.evaluate((sel) => {
    const el = document.querySelector(sel);
    return !!el && el.getClientRects().length > 0 && getComputedStyle(el).visibility !== "hidden";
  }, selector);
}

async function closeModal(page) {
  await page.click(`${MODAL} .btn-close`);
  await page.waitForFunction(() => !document.querySelector("[data-form-modal]").classList.contains("show"), null, { timeout: 8000 });
}

// dialogBox answers where the dialog stands, rounded, and whether it fits the
// viewport.
function dialogBox(page) {
  return page.$eval(`${MODAL} .modal-content`, (content) => {
    const box = content.getBoundingClientRect();
    return { top: Math.round(box.top), height: Math.round(box.height), fits: box.top >= 0 && box.bottom <= window.innerHeight };
  });
}

async function openSettings(page, filter) {
  await page.goto(`${BASE}/settings/projects/tags`, { waitUntil: "domcontentloaded" });
  await L.dismissUpdate(page);
  await page.waitForFunction(() => customElements.get("dc-tags-settings") && document.querySelector("dc-tags-settings [data-tags-pending]"), null, { timeout: 12000 });
  if (filter && await page.isVisible(`${SETTINGS} [data-tags-filter]`)) await page.fill(`${SETTINGS} [data-tags-filter]`, filter);
}

const row = (id) => `${SETTINGS} [data-tag-row="${id}"]`;
const pending = (page) => page.textContent("[data-tags-pending]");

async function saveSettings(page, confirm) {
  await page.click("[data-tags-save]");
  if (confirm) await L.confirmSwal(page);
  await page.waitForSelector(`.alert:has-text("Tags saved")`, { state: "visible", timeout: 10000 });
  return (await page.textContent(".alert")).trim();
}

// removeRunTags removes every tag these checks made, each carries the run's
// suffix in any case.
module.exports.removeRunTags = (page, tag) => L.removeTags(page, (name) => name.toLowerCase().includes(tag));

module.exports.tagChecks = async function tagChecks({ page, run, mobilePage, project, source, tag }) {
  const worktree = `${source}-wt1`;
  const other = `zztg-${tag.slice(-6)}`;
  const first = `ta-${tag}`;
  const second = `tb-${tag}`;
  const third = `tc-${tag}`;
  const ten = Array.from({ length: 10 }, (_, i) => `t${i}-${tag}`);
  let firstID = "";

  await run("tags: at 390px the settings with no tags say so in a row's inset without stacked lines, no filter and no Select all, while a first tag comes and goes too, and No tag matches only over tags the filter hides", async () => {
    const mp = await mobilePage();
    const lone = `te-${tag}`;
    const expect = async (want, when) => {
      const got = {};
      for (const [key, sel] of Object.entries({ empty: "[data-tags-empty]", none: "[data-tags-none]", head: "[data-tags-head]", filter: ".dc-tags-filter" })) got[key] = await shown(mp, `${SETTINGS} ${sel}`);
      assert(JSON.stringify(got) === JSON.stringify(want), `${when} shows ${JSON.stringify(got)}`);
    };
    const inset = (sel, text) => mp.$eval(`${SETTINGS} ${sel}`, (el, byText) => {
      const range = document.createRange();
      range.selectNodeContents(el);
      const box = byText ? range.getBoundingClientRect() : el.getBoundingClientRect();
      return Math.round(box.left - el.closest(".card").getBoundingClientRect().left);
    }, text);
    const lines = (sel) => mp.$eval(`${SETTINGS} .dc-tags-list`, (card, inner) => {
      const cs = getComputedStyle(card);
      return card.getBoundingClientRect().height - parseFloat(cs.borderTopWidth) - parseFloat(cs.borderBottomWidth) - card.querySelector(inner).getBoundingClientRect().height;
    }, sel);
    await openSettings(mp);
    assert((await mp.locator(`${SETTINGS} [data-tag-row]`).count()) === 0, "the instance holds tags already, the empty state needs one without");
    const nothing = { empty: true, none: false, head: false, filter: false };
    await expect(nothing, "a page with no tags");
    const text = await inset("[data-tags-empty]", true);
    const around = await lines("[data-tags-empty]");
    assert(Math.abs(around) < 0.5, `the card stacks ${around}px of lines around its line`);

    await mp.click("[data-tags-add]");
    await expect({ ...nothing, empty: false }, "a first new tag");
    const row = await inset("[data-tag-new] .dc-tag-row-check", false);
    const aroundNew = await lines("[data-tag-new]");
    assert(Math.abs(aroundNew) < 0.5, `the card stacks ${aroundNew}px of lines around the new row`);
    assert(text >= 12 && Math.abs(text - row) <= 1, `the line starts ${text}px into the card, a row's content ${row}px`);
    await mp.click(`${SETTINGS} [data-tag-new] [data-tag-drop]`);
    await expect(nothing, "the first new tag dropped");

    await mp.click("[data-tags-add]");
    await mp.fill(`${SETTINGS} [data-tag-new] [data-tag-name]`, lone);
    await saveSettings(mp, false);
    const tags = { empty: false, none: false, head: true, filter: true };
    await expect(tags, "a page with one tag");
    await mp.fill(`${SETTINGS} [data-tags-filter]`, `zz-${tag}`);
    await expect({ ...tags, none: true }, "a filter over every tag");
    await mp.fill(`${SETTINGS} [data-tags-filter]`, "");
    await mp.click(`${SETTINGS} [data-tag-row][data-loaded-name="${lone}"] [data-tag-remove]`);
    await expect(tags, "the last tag staged for removal");
    await saveSettings(mp, true);
    await mp.waitForSelector(`${SETTINGS} [data-tag-row]`, { state: "detached", timeout: 8000 });
    await expect(nothing, "the last tag removed");
  });

  await run("tags: the bar's pencil leads to Settings › Projects › Tags, a row per tag with its color, name and projects, the filter counts, a count shows the board filtered by its tag", async () => {
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    await setTags(page, project, [first, second, third], { [first]: "pink", [second]: "lime", [third]: "orange" });
    await setTags(page, source, [first]);
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    firstID = await idOf(page, first);
    await Promise.all([page.waitForURL(/\/settings\/projects\/tags$/, { timeout: 10000 }), page.click("dc-project-list [data-tags-manage]")]);
    assert((await page.getAttribute(".dc-work-tabs .nav-link.active", "href")) === "/settings/projects/tags", "the Tags tab is not the active one");
    await page.waitForSelector(row(firstID), { state: "visible", timeout: 8000 });
    assert((await page.inputValue(`${row(firstID)} [data-tag-name]`)) === first, "the row does not hold the tag's name");
    assert(await page.$eval(`${row(firstID)} [data-tag-color-dot]`, (dot) => dot.classList.contains("dc-tag-pink")), "the row shows the tag in another color");
    const count = `${row(firstID)} a.dc-tag-row-count`;
    assert((await page.textContent(count)).trim() === "2 projects" && (await page.getAttribute(count, "href")) === `/projects?tag=${firstID}`, `the count reads ${await page.textContent(count)}`);
    assert((await pending(page)).trim() === "No changes" && await page.isDisabled("[data-tags-save]"), "a fresh page has something to save");
    await page.fill(`${SETTINGS} [data-tags-filter]`, tag);
    const counted = (await page.textContent("[data-tags-filter-count]")).trim();
    assert(/^3 of \d+$/.test(counted), `the filter counts ${counted}`);
    assert(await shown(page, `${SETTINGS} [data-tags-filter-clear]`), "the filter offers no clear");

    await Promise.all([page.waitForURL(/\/projects$/, { timeout: 10000 }), page.click(count)]);
    await page.waitForSelector(`dc-project-list [data-tag-picked] [data-tag-unpick="${firstID}"]`, { state: "visible", timeout: 8000 });
    assert(await shown(page, `#project-${project}`) && await shown(page, `#project-${source}`), "the projects carrying the tag are hidden");
    const left = (await page.textContent("dc-project-list [data-filter-count]")).trim();
    assert(/^2 of \d+$/.test(left), `the field counts ${left}`);
    await page.click("dc-project-list [data-project-filter-clear]");
    await sleep(200);
    assert((await page.locator("dc-project-list [data-tag-picked] [data-tag-unpick]").count()) === 0, "the clear left the pick");
  });

  await run("tags: nothing is written before Save, a rename onto another tag shows the merge in the result's color, the palette recolors, Remove stands with Undo, the footer counts, Discard drops it all", async () => {
    const ids = { second: await (async () => { await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" }); return idOf(page, second); })(), third: await idOf(page, third) };
    await openSettings(page, tag);
    await page.fill(`${row(ids.second)} [data-tag-name]`, first.toUpperCase());
    const hint = `${row(ids.second)} [data-tag-hint]`;
    await page.waitForSelector(hint, { state: "visible", timeout: 4000 });
    const said = (await page.textContent(hint)).trim();
    assert(said === `Merges into ${first} on save, then 2 projects carry it.`, `the merge line reads ${said}`);
    assert(await page.$eval(`${row(ids.second)} [data-tag-color-dot]`, (dot) => dot.classList.contains("dc-tag-pink")), "the merging row does not wear the color of the tag it merges into");
    assert((await pending(page)).trim() === "1 change" && !(await page.isDisabled("[data-tags-save]")) && await shown(page, "[data-tags-discard]"), `the footer reads ${await pending(page)}`);

    await page.click(`${row(firstID)} [data-tag-color]`);
    await page.waitForSelector("[data-tag-palette].show", { state: "visible", timeout: 4000 });
    await page.click('[data-tag-palette] [data-tag-swatch="teal"]');
    assert(!(await shown(page, "[data-tag-palette].show")), "the palette stayed open");
    for (const id of [firstID, ids.second]) {
      assert(await page.$eval(`${row(id)} [data-tag-color-dot]`, (dot) => dot.classList.contains("dc-tag-teal")), `the row ${id} does not show the new color`);
    }
    await page.click(`${row(ids.third)} [data-tag-remove]`);
    assert(await page.$eval(row(ids.third), (r) => r.classList.contains("is-removed")) && await shown(page, `${row(ids.third)} [data-tag-undo]`) && !(await shown(page, `${row(ids.third)} [data-tag-remove]`)), "the removal does not stand with its Undo");
    assert((await pending(page)).trim() === "3 changes", `the footer reads ${await pending(page)}`);
    assert((await page.getAttribute("[data-tags-form]", "data-confirm")) === `Remove tag "${third}"?` && (await page.getAttribute("[data-tags-form]", "data-confirm-text")) === `It comes off 1 project: ${project}.`, "the save does not ask before the removal");
    await page.click(`${row(ids.third)} [data-tag-undo]`);
    assert((await pending(page)).trim() === "2 changes" && (await page.getAttribute("[data-tags-form]", "data-confirm")) === null, "Undo left the removal");

    const stored = await (await page.request.get(`${BASE}/settings/projects/tags`)).text();
    assert(stored.includes(`data-loaded-name="${second}"`) && stored.includes(`name="color:${firstID}" value="pink"`), "a staged change was written before Save");
    await page.click("[data-tags-discard]");
    assert((await pending(page)).trim() === "No changes" && await page.isDisabled("[data-tags-save]") && (await page.inputValue(`${row(ids.second)} [data-tag-name]`)) === second, "Discard left a change");
    assert(!(await shown(page, hint)), "Discard left the merge line");
  });

  await run("tags: Save asks before it removes through the cockpit's confirm, then renames, merges, recolors and removes on every project under the same ids", async () => {
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    const ids = { second: await idOf(page, second), third: await idOf(page, third) };
    await openSettings(page, tag);
    await page.fill(`${row(ids.second)} [data-tag-name]`, first.toUpperCase());
    await page.click(`${row(firstID)} [data-tag-color]`);
    await page.click('[data-tag-palette] [data-tag-swatch="teal"]');
    await page.click(`${row(ids.third)} [data-tag-remove]`);
    await page.click("[data-tags-save]");
    await page.waitForSelector(".swal2-popup:not(.swal2-toast)", { state: "visible", timeout: 8000 });
    const question = (await page.textContent(".swal2-popup")).trim();
    assert(question.includes(`Remove tag "${third}"?`) && question.includes(project), `the confirm reads ${question}`);
    await L.confirmSwal(page);
    await page.waitForSelector(`.alert:has-text("Tags saved")`, { state: "visible", timeout: 10000 });
    const saved = (await page.textContent(".alert")).trim();
    assert(saved.includes("Tags saved: 1 renamed, 1 recolored, 1 merged, 1 removed."), `the save says ${saved}`);
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    const merged = `${first.toUpperCase()}:teal:${firstID}`;
    for (const name of [project, source]) await waitRowTags(page, name, [merged], [second, third]);
    assert((await page.locator(`dc-project-list [data-tag-bar] [data-tag-toggle="${ids.second}"], dc-project-list [data-tag-bar] [data-tag-toggle="${ids.third}"]`).count()) === 0, "the bar still offers a merged or removed tag");
    assert((await rowTags(page, worktree)).length === 0, "the worktree shows tags");
  });

  await run("tags: selected rows merge into the name and color picked with the projects named, take a color and go at once", async () => {
    const [a, b, c] = ["ma", "mb", "mc"].map((p) => `${p}-${tag}`);
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await setTags(page, project, [first.toUpperCase(), a, b, c], { [a]: "red", [b]: "lime", [c]: "purple" });
    await setTags(page, source, [first.toUpperCase(), b]);
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    const ids = { a: await idOf(page, a), b: await idOf(page, b), c: await idOf(page, c) };
    await openSettings(page, tag);
    await page.check(`${row(ids.a)} [data-tag-select]`);
    await page.check(`${row(ids.b)} [data-tag-select]`);
    assert((await page.textContent("[data-tags-selected]")).trim() === "2 selected" && await shown(page, "[data-tags-bulk]") && !(await page.isDisabled("[data-tags-merge]")), "the selection offers no merge");
    await page.click("[data-tags-merge]");
    const dialog = "[data-tags-merge-dialog].show";
    await page.waitForSelector(dialog, { state: "visible", timeout: 8000 });
    await sleep(400);
    assert((await page.textContent(`${dialog} [data-tags-merge-title]`)).trim() === "Merge 2 tags", "the dialog names no count");
    assert((await page.inputValue(`${dialog} [name=tags-merge-keep]:checked`)) === ids.b, "the dialog does not keep the tag on the most projects");
    const result = (await page.textContent(`${dialog} [data-tags-merge-result]`)).trim();
    assert(result === `Afterwards 2 projects carry ${b}: ${[project, source].sort().join(", ")}.`, `the dialog says ${result}`);
    await page.check(`${dialog} input[value="${ids.a}"]`);
    assert((await page.getAttribute(`${dialog} [data-tags-merge-color="red"]`, "aria-pressed")) === "true", "the color does not follow the kept name");
    await page.click(`${dialog} [data-tags-merge-color="cyan"]`);
    assert(await page.$eval(`${dialog} [data-tags-merge-result] .dc-tag`, (chip, name) => chip.classList.contains("dc-tag-cyan") && chip.textContent === name, a), "the result shows another name or color");
    const footer = await page.$$eval(`${dialog} .modal-footer .btn`, (els) => els.map((e) => `${e.textContent.trim()}:${e.classList.contains("btn-primary") ? "primary" : e.classList.contains("btn-link") ? "link" : "other"}`));
    assert(JSON.stringify(footer) === JSON.stringify(["Cancel:link", "Merge:primary"]), `the footer is ${JSON.stringify(footer)}`);
    await page.click(`${dialog} [data-tags-merge-apply]`);
    await page.waitForFunction(() => !document.querySelector("[data-tags-merge-dialog]").classList.contains("show"), null, { timeout: 8000 });
    assert((await page.inputValue(`${row(ids.b)} [data-tag-name]`)) === a && await shown(page, `${row(ids.b)} [data-tag-hint]`), "the merge was not staged");
    assert((await page.textContent("[data-tags-selected]")).trim() === "Select all", "the selection stayed");

    await page.check(`${row(ids.c)} [data-tag-select]`);
    await page.click("[data-tags-recolor]");
    await page.click('[data-tag-palette] [data-tag-swatch="orange"]');
    assert(await page.$eval(`${row(ids.c)} [data-tag-color-dot]`, (dot) => dot.classList.contains("dc-tag-orange")), "the bulk color did not land");
    await page.check(`${row(ids.c)} [data-tag-select]`);
    await page.click("[data-tags-remove-selected]");
    assert(await page.$eval(row(ids.c), (r) => r.classList.contains("is-removed")), "the bulk remove did not stage");
    const saved = await saveSettings(page, true);
    assert(saved.includes("1 recolored, 1 merged, 1 removed"), `the save says ${saved}`);
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await waitRowTags(page, project, [`${a}:cyan:${ids.a}`], [b, c]);
    await waitRowTags(page, source, [`${a}:cyan:${ids.a}`], [b]);
  });

  await run("tags: a page loaded before a change elsewhere saves nothing, says which tag moved and keeps what it sent for the others", async () => {
    const [s1, s2] = ["sa", "sb"].map((p) => `${p}-${tag}`);
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await setTags(page, project, [first.toUpperCase(), s1, s2], { [s1]: "lime", [s2]: "blue" });
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    const ids = { s1: await idOf(page, s1), s2: await idOf(page, s2) };
    await openSettings(page, tag);
    await page.fill(`${row(ids.s1)} [data-tag-name]`, `${s1}x`);
    await page.fill(`${row(ids.s2)} [data-tag-name]`, `${s2}x`);
    await L.saveTagSettings(page, (fields) => L.setField(fields, `color:${ids.s1}`, "teal"));
    await page.click("[data-tags-save]");
    await page.waitForSelector(`.alert:has-text("Changed elsewhere since this page loaded: ${s1}.")`, { state: "visible", timeout: 10000 });
    await page.waitForFunction(() => customElements.get("dc-tags-settings") && document.querySelector("dc-tags-settings [data-tags-pending]"), null, { timeout: 8000 });
    assert((await page.inputValue(`${row(ids.s1)} [data-tag-name]`)) === s1 && await page.$eval(`${row(ids.s1)} [data-tag-color-dot]`, (dot) => dot.classList.contains("dc-tag-teal")), "the tag changed elsewhere does not show as it is now");
    assert((await page.inputValue(`${row(ids.s2)} [data-tag-name]`)) === `${s2}x` && (await pending(page)).trim() === "1 change", "the refusal lost what was sent for the other tag");
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await waitRowTags(page, project, [`${s1}:teal:${ids.s1}`, `${s2}:blue:${ids.s2}`], [`${s2}x`]);
    await openSettings(page, tag);
    await page.fill(`${row(ids.s2)} [data-tag-name]`, `${s2}x`);
    await saveSettings(page, false);
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await waitRowTags(page, project, [`${s2}x:blue:${ids.s2}`], []);
  });

  await run("tags: Add tag stages a new tag in the next color, a name in use makes none, Save makes it with no project, the tags dialog offers it and it lives on without its last project", async () => {
    const fresh = `nw-${tag}`;
    await openSettings(page, fresh);
    const before = await page.locator(`${SETTINGS} [data-tag-new]`).count();
    await page.click("[data-tags-add]");
    const added = `${SETTINGS} [data-tag-new] >> nth=${before}`;
    assert(await page.evaluate(() => document.activeElement?.matches("[data-tag-new] [data-tag-name]")), "the new row's name has no focus");
    assert(await page.locator(added).locator("[data-tag-color-dot]").evaluate((dot) => [...dot.classList].some((c) => /^dc-tag-[a-z]+$/.test(c) && c !== "dc-tag-dot")), "the new row wears no color");
    await page.locator(added).locator("[data-tag-name]").fill(fresh);
    assert((await pending(page)).trim() === "1 change", `the footer reads ${await pending(page)}`);
    await page.locator(added).locator("[data-tag-color]").click();
    await page.click('[data-tag-palette] [data-tag-swatch="cyan"]');
    assert(await page.locator(added).locator("[data-tag-color-dot]").evaluate((dot) => dot.classList.contains("dc-tag-cyan")), "the palette did not color the new row");
    await page.click("[data-tags-add]");
    const twin = `${SETTINGS} [data-tag-new] >> nth=${before + 1}`;
    await page.locator(twin).locator("[data-tag-name]").fill(first.toUpperCase());
    assert(await page.locator(twin).locator("[data-tag-hint]").isVisible() && (await pending(page)).trim() === "1 change", "a new row of a name in use counts as a change");
    await page.locator(twin).locator("[data-tag-drop]").click();
    assert((await page.locator(`${SETTINGS} [data-tag-new]`).count()) === before + 1, "Drop left the row");
    const saved = await saveSettings(page, false);
    assert(saved.includes("Tags saved: 1 created."), `the save says ${saved}`);
    const made = `${SETTINGS} [data-tag-row][data-loaded-name="${fresh}"]`;
    await page.waitForSelector(made, { state: "attached", timeout: 8000 });
    assert((await page.textContent(`${made} .dc-tag-row-count`)).trim() === "0 projects" && await page.$eval(`${made} [data-tag-color-dot]`, (dot) => dot.classList.contains("dc-tag-cyan")), "the new tag does not stand cyan with no project");

    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await page.click(`#project-${project} [data-project-tags-edit]`);
    await page.waitForSelector(`${MODAL} [data-tag-offer="${fresh}"]`, { state: "visible", timeout: 8000 });
    await page.click(`${MODAL} [data-tag-offer="${fresh}"]`);
    await page.click(`${MODAL} form[data-project-tags-form] button[type="submit"]`);
    await page.waitForSelector(`.dc-toast:has-text('Tags of "${project}" saved.')`, { state: "visible", timeout: 8000 });
    const id = await idOf(page, fresh);
    await waitRowTags(page, project, [`${fresh}:cyan:${id}`], []);
    await page.click(`#project-${project} [data-project-tags-edit]`);
    await page.waitForSelector(`${MODAL} [data-tag-on][data-tag="${id}"]`, { state: "visible", timeout: 8000 });
    await page.click(`${MODAL} [data-tag-on][data-tag="${id}"] [data-tag-remove]`);
    await page.click(`${MODAL} form[data-project-tags-form] button[type="submit"]`);
    await page.waitForFunction(() => !document.querySelector("[data-form-modal]").classList.contains("show"), null, { timeout: 8000 });
    await waitRowTags(page, project, [], [fresh]);
    await openSettings(page, fresh);
    assert((await page.textContent(`${made} .dc-tag-row-count`)).trim() === "0 projects", "the tag did not live on without its last project");
    await page.click(`${made} [data-tag-remove]`);
    await saveSettings(page, true);
    assert((await page.locator(made).count()) === 0, "Remove left the tag");
  });

  await run("tags: a board row names its tags above its terminals, folds past eight behind +N, ends with the dashed way into its dialog, + Tag without tags, and no tags icon among its actions", async () => {
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await setTags(page, project, ten);
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    const line = `#project-${project} [data-tag-line]`;
    await page.waitForFunction((sel) => document.querySelectorAll(`${sel} [data-tag-toggle]`).length === 10, line, { timeout: 8000 });
    const fold = await page.$eval(line, (el) => ({
      shown: [...el.querySelectorAll("[data-tag-toggle]")].filter((c) => c.getClientRects().length > 0).length,
      toggle: el.querySelector("[data-chips-toggle]")?.textContent.trim(),
      last: el.lastElementChild.matches("[data-project-tags-edit]") && !!el.lastElementChild.querySelector(".ti-pencil"),
      dashed: getComputedStyle(el.lastElementChild).borderTopStyle,
    }));
    assert(fold.shown === 8 && fold.toggle === "+2" && fold.last && fold.dashed === "dashed", `the line folds as ${JSON.stringify(fold)}`);
    await page.click(`${line} [data-chips-toggle]`);
    assert(await page.$eval(line, (el) => [...el.querySelectorAll("[data-tag-toggle]")].every((c) => c.getClientRects().length > 0)), "+N did not unfold the tags");
    await page.click(`${line} [data-chips-toggle]`);
    const order = await page.$eval(`#project-${project}`, (el) => ({ line: el.querySelector("[data-tag-line]").getBoundingClientRect().bottom, chips: el.querySelector("[data-sessions-body] .project-chips").getBoundingClientRect().top }));
    assert(order.line <= order.chips, `the tags do not stand above the terminals: ${JSON.stringify(order)}`);
    assert((await page.locator("dc-project-list .project-row .btn-list .ti-tags").count()) === 0, "a row keeps the tags icon among its actions");
    await page.click(`${line} [data-project-tags-edit]`);
    await page.waitForSelector(`${MODAL} dc-tag-editor`, { state: "visible", timeout: 8000 });
    await closeModal(page);
    await setTags(page, project, []);
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await page.waitForFunction((sel) => document.querySelector(`${sel} [data-project-tags-edit]`)?.textContent.trim() === "Tag", line, { timeout: 8000 });
    assert((await page.locator(`${line} [data-project-tags-edit] .ti-plus`).count()) === 1, "an untagged row's chip does not read + Tag");
  });

  await run("tags: picked tags stand in the field with a cross each, the bar counts what is left, dims and stops a tag that would leave nothing, one cross clears text and tags, a pick moves no chip", async () => {
    const [f1, f2, g0] = ["fa", "fb", "ga"].map((p) => `${p}-${tag}`);
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await L.createProject(page, other);
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await setTags(page, project, [f1, f2]);
    await setTags(page, source, [f1]);
    await setTags(page, other, [g0]);
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    const ids = { f1: await idOf(page, f1), f2: await idOf(page, f2), g0: await idOf(page, g0) };
    const boxes = () => page.$$eval("dc-project-list [data-tag-bar] [data-tag-toggle]", (els) => els.filter((e) => !e.hidden).map((e) => { const b = e.getBoundingClientRect(); return `${e.dataset.tagToggle}:${Math.round(b.x * 10)}:${Math.round(b.width * 10)}`; }).join("|"));
    const before = await boxes();
    await page.click(`#project-${source} [data-tag-toggle="${ids.f1}"]`);
    await sleep(300);
    assert((await boxes()) === before, "a pick moved or resized a chip of the bar");
    const bar = (id) => `dc-project-list [data-tag-bar] [data-tag-toggle="${id}"]`;
    const state = (id) => page.$eval(bar(id), (e) => `${e.querySelector("[data-tag-count]").textContent}:${e.hasAttribute("data-tag-none")}:${e.disabled}:${e.getAttribute("aria-pressed")}`);
    assert((await state(ids.f1)) === "2:false:false:true" && (await state(ids.f2)) === "1:false:false:false" && (await state(ids.g0)) === "0:true:true:false", `the bar reads ${await state(ids.f1)} ${await state(ids.f2)} ${await state(ids.g0)}`);
    assert(/^2 of \d+$/.test((await page.textContent("dc-project-list [data-filter-count]")).trim()), "the field does not count what is left");
    assert(await page.$eval(`dc-project-list [data-tag-picked] [data-tag-unpick="${ids.f1}"]`, (chip) => chip.textContent.trim() === chip.querySelector(".dc-tag-name").textContent && !!chip.querySelector(".ti-x")), "the field's chip has no cross");
    await page.click(`#project-${project} [data-tag-toggle="${ids.f2}"]`);
    await sleep(200);
    assert((await page.locator("dc-project-list [data-tag-picked] [data-tag-unpick]").count()) === 2 && !(await shown(page, `#project-${source}`)), "a second pick did not narrow");
    await page.click(`dc-project-list [data-tag-picked] [data-tag-unpick="${ids.f2}"]`);
    await sleep(200);
    assert((await page.locator("dc-project-list [data-tag-picked] [data-tag-unpick]").count()) === 1 && await shown(page, `#project-${source}`), "the field's cross did not take its tag off");
    await page.fill("[data-project-filter]", source);
    await sleep(200);
    assert(/^1 of \d+$/.test((await page.textContent("dc-project-list [data-filter-count]")).trim()), "the count does not follow the text");
    await page.click("dc-project-list [data-project-filter-clear]");
    await sleep(200);
    assert((await page.inputValue("[data-project-filter]")) === "" && (await page.locator("dc-project-list [data-tag-picked] [data-tag-unpick]").count()) === 0 && !(await shown(page, "dc-project-list [data-filter-count]")) && await shown(page, `#project-${other}`), "the one cross did not clear text and tags");
  });

  await run("tags: on a desktop the bar keeps one line, the tags that do not fit wait behind +N more, which lists every tag with a filter of its own", async () => {
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await setTags(page, other, ten);
    await page.setViewportSize({ width: 1100, height: 900 });
    try {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      const target = await idOf(page, ten[7]);
      await page.waitForSelector("dc-project-list [data-tag-more]:not([hidden])", { state: "visible", timeout: 8000 });
      const line = await page.$eval("dc-project-list [data-tag-bar]", (bar) => {
        const chips = [...bar.querySelectorAll("[data-tag-toggle]")];
        const shownChips = chips.filter((c) => !c.hidden);
        return { tops: new Set(shownChips.map((c) => Math.round(c.getBoundingClientRect().top))).size, hidden: chips.length - shownChips.length, label: bar.querySelector("[data-tag-more-label]").textContent };
      });
      assert(line.tops === 1 && line.hidden > 0 && line.label === `+${line.hidden} more`, `the bar stands as ${JSON.stringify(line)}`);
      await page.click("dc-project-list [data-tag-more-toggle]");
      await page.waitForSelector("dc-project-list .dc-tag-menu.show", { state: "visible", timeout: 4000 });
      assert(await page.evaluate(() => document.activeElement?.matches("[data-tag-menu-filter]")), "the menu's filter has no focus");
      assert((await page.locator("dc-project-list [data-tag-menu-row]").count()) === (await page.locator("dc-project-list [data-tag-bar] [data-tag-toggle]").count()), "the menu does not list every tag");
      await page.fill("dc-project-list [data-tag-menu-filter]", ten[7]);
      const rows = await page.$$eval("dc-project-list [data-tag-menu-row]", (els) => els.filter((e) => !e.hidden).map((e) => e.dataset.tagMenuRow));
      assert(JSON.stringify(rows) === JSON.stringify([target]), `the menu filter leaves ${JSON.stringify(rows)}`);
      await page.check(`dc-project-list [data-tag-check="${target}"]`);
      await sleep(200);
      assert(await shown(page, `#project-${other}`) && !(await shown(page, `#project-${project}`)) && (await page.locator(`dc-project-list [data-tag-picked] [data-tag-unpick="${target}"]`).count()) === 1, "the menu's checkbox did not filter");
      await page.uncheck(`dc-project-list [data-tag-check="${target}"]`);
      await sleep(200);
      assert(await shown(page, `#project-${project}`), "unchecking left the filter");
      await page.keyboard.press("Escape");
    } finally {
      await page.setViewportSize({ width: 1360, height: 900 });
    }
  });

  await run("tags: the dialog shows the project's tags first, then the field and the tags used on other projects, no colors, a link to the settings, 12px chips with 20px crosses, and keeps one height", async () => {
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await setTags(page, project, [first.toUpperCase()]);
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await page.click(`#project-${project} [data-project-tags-edit]`);
    const input = `${MODAL} dc-tag-editor [data-tag-input]`;
    await page.waitForSelector(input, { state: "visible", timeout: 8000 });
    await sleep(400);
    const order = await page.$eval(`${MODAL} dc-tag-editor`, (editor) => {
      const top = (sel) => editor.querySelector(sel).getBoundingClientRect().top;
      return { on: top("[data-tags-on]"), label: top("label[for='project-tag-input']"), offers: top("[data-tags-offered-label]"), title: editor.querySelector("#project-tags-on").textContent, offered: editor.querySelector("[data-tags-offered-label]").textContent };
    });
    assert(order.on < order.label && order.label < order.offers && order.title === `On ${project}` && order.offered === "Other tags", `the dialog stands as ${JSON.stringify(order)}`);
    assert((await page.locator(`${MODAL} [data-tag-swatch], ${MODAL} [data-tag-pick]`).count()) === 0, "the dialog still picks colors");
    assert((await page.locator(`${MODAL} a[href="/settings/projects/tags"]`).count()) === 1, "the dialog does not lead to the settings");
    const chip = await page.$eval(`${MODAL} [data-tag-on]`, (el) => ({ font: getComputedStyle(el).fontSize, x: Math.round(el.querySelector("[data-tag-remove]").getBoundingClientRect().width), xh: Math.round(el.querySelector("[data-tag-remove]").getBoundingClientRect().height) }));
    assert(chip.font === "12px" && chip.x === 20 && chip.xh === 20, `a chip is ${JSON.stringify(chip)}`);
    const start = await dialogBox(page);
    const seen = [];
    for (let i = 0; i < 6; i += 1) {
      await page.fill(input, `dh${i}-${tag}`);
      await page.press(input, "Enter");
      seen.push(await dialogBox(page));
    }
    await page.click(`${MODAL} [data-tag-on] [data-tag-remove] >> nth=1`);
    seen.push(await dialogBox(page));
    assert(start.fits && seen.every((box) => box.top === start.top && box.height === start.height), `the dialog moved: ${JSON.stringify([start, ...seen])}`);
    await closeModal(page);
  });

  await run("tags: the list column and the switchers show one disc with a slice per tag, as wide for ten tags as for one, and ten tags never cost a project's name a character there or on the board", async () => {
    const long = `zzsymfony-lock-doctrine-migrations-bundle-${tag.slice(-4)}`;
    const mp = await mobilePage();
    // The list column refreshes on the projects event a create or a save
    // sends, so a row found a moment ago may be gone: measure the one there.
    const width = async (p, selector) => {
      for (let i = 0; ; i += 1) {
        const out = await p.$eval(selector, (el) => {
          const text = document.createRange();
          text.selectNodeContents(el);
          return { w: Math.round(Math.min(text.getBoundingClientRect().width, el.clientWidth)), h: Math.round(el.getBoundingClientRect().height * 10) / 10, cut: el.scrollWidth > el.clientWidth, attached: el.isConnected };
        });
        if (out.attached || i >= 20) {
          delete out.attached;
          return out;
        }
        await sleep(100);
      }
    };
    const disc = async (p, selector) => {
      for (let i = 0; ; i += 1) {
        const out = await p.$eval(selector, (el) => ({ discs: el.querySelectorAll(".dc-tag-disc").length, slices: el.querySelectorAll(".dc-tag-disc > .dc-tag-slice").length, w: el.querySelector(".dc-tag-disc")?.getBoundingClientRect().width || 0, attached: el.isConnected }));
        if (out.attached || i >= 20) {
          delete out.attached;
          return out;
        }
        await sleep(100);
      }
    };
    const switcher = async (name) => {
      await page.goto(`${BASE}/projects/${project}/editor`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector("[data-editor-project-switch]", { state: "attached", timeout: 12000 });
      await page.keyboard.press("Control");
      await page.keyboard.press("Control");
      await page.waitForSelector(".terminal-switcher", { state: "visible", timeout: 4000 });
      await page.keyboard.type(name, { delay: 10 });
      const item = `.terminal-switcher-item[data-switcher-url^="/projects/${name}/editor"]`;
      await page.waitForSelector(item, { state: "visible", timeout: 4000 });
      const out = await page.$eval(item, (el) => {
        const text = el.querySelector(".terminal-switcher-name .text-truncate") || el.querySelector(".terminal-switcher-name");
        const label = el.querySelector(".terminal-switcher-project");
        const range = document.createRange();
        range.selectNodeContents(text);
        return { w: Math.round(Math.min(range.getBoundingClientRect().width, text.clientWidth)), cut: text.scrollWidth > text.clientWidth, label: label.textContent.trim(), labelWhole: label.scrollWidth <= label.clientWidth, slices: el.querySelectorAll(".dc-tag-disc > .dc-tag-slice").length, discs: el.querySelectorAll(".dc-tag-disc").length, disc: el.querySelector(".dc-tag-disc")?.getBoundingClientRect().width || 0, named: el.querySelector(".dc-tag-pie")?.getAttribute("aria-label") || "" };
      });
      await page.keyboard.press("Escape");
      return out;
    };
    const palette = async (p, name, phone) => {
      await p.goto(`${BASE}/projects/${project}/editor`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(p);
      await p.waitForSelector("[data-editor-project-switch], [data-editor-drawer-toggle]", { state: "attached", timeout: 12000 });
      if (phone && !(await p.isVisible("[data-editor-project-switch]"))) await p.tap("[data-editor-drawer-toggle]");
      if (phone) await p.tap("[data-editor-project-switch]");
      else await p.click("[data-editor-project-switch]");
      await p.waitForSelector("[data-editor-palette]", { state: "visible", timeout: 4000 });
      await p.fill("[data-editor-project-filter]", name);
      const item = `[data-editor-palette-list] [data-project-name="${name}"]`;
      await p.waitForSelector(item, { state: "visible", timeout: 4000 });
      const out = { repo: await width(p, `${item} .editor-palette-repo`), slices: await p.locator(`${item} .dc-tag-disc > .dc-tag-slice`).count(), discs: await p.locator(`${item} .dc-tag-disc`).count() };
      await p.keyboard.press("Escape").catch(() => {});
      return out;
    };
    const surfaces = async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(`#project-${long}`, { state: "visible", timeout: 8000 });
      const out = {
        board: await width(page, `#project-${long} .dc-project-name`),
        column: await width(page, `.dc-ctx [data-index-project="${long}"] .dc-trunc`),
        columnDisc: await disc(page, `.dc-ctx [data-index-project="${long}"]`),
        switcher: await switcher(long),
        palette: await palette(page, long, false),
        phonePalette: await palette(mp, long, true),
      };
      await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await mp.waitForSelector(`#project-${long}`, { state: "visible", timeout: 8000 });
      out.phoneBoard = await width(mp, `#project-${long} .dc-project-name`);
      await mp.click('.dc-tabbar button[data-ctx-area="projects"]');
      const sheetRow = `dc-ctx-sheet:not([hidden]) [data-index-project="${long}"]`;
      await mp.waitForSelector(sheetRow, { state: "visible", timeout: 8000 });
      out.sheet = await width(mp, `${sheetRow} .dc-trunc`);
      await mp.keyboard.press("Escape").catch(() => {});
      return out;
    };
    await L.createProject(page, long);
    try {
      const bare = await surfaces();
      assert(bare.column.cut && bare.sheet.cut, `the name is too short to need room: ${JSON.stringify(bare)}`);
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await setTags(page, long, ten);
      const tagged = await surfaces();
      for (const where of ["board", "column", "palette", "phonePalette", "phoneBoard", "sheet", "switcher"]) {
        const was = where.endsWith("alette") ? bare[where].repo : bare[where];
        const now = where.endsWith("alette") ? tagged[where].repo : tagged[where];
        assert(now.w >= was.w - 0.5 && (!now.cut || was.cut) && (where !== "board" && where !== "phoneBoard" || now.h === was.h), `with ten tags the name on ${where} stands at ${JSON.stringify(now)}, without at ${JSON.stringify(was)}`);
      }
      assert(tagged.switcher.discs === 1 && tagged.switcher.slices === 10 && tagged.switcher.named === `Tags: ${ten.join(", ")}` && tagged.palette.discs === 1 && tagged.palette.slices === 10 && tagged.columnDisc.discs === 1 && tagged.columnDisc.slices === 10, `the tags are not one disc of ten slices: ${JSON.stringify(tagged)}`);
      assert(tagged.switcher.label === "Editor" && tagged.switcher.labelWhole, `the switcher cut its Editor label: ${JSON.stringify(tagged.switcher)}`);
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await setTags(page, long, ten.slice(0, 1));
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      const one = await disc(page, `.dc-ctx [data-index-project="${long}"]`);
      assert(one.discs === 1 && one.slices === 1 && one.w > 0 && one.w === tagged.columnDisc.w && one.w === tagged.switcher.disc, `one tag makes ${JSON.stringify(one)}, ten made ${JSON.stringify(tagged.columnDisc)}`);
    } finally {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await L.deleteProject(page, long).catch(() => {});
    }
  });

  await run("tags: a name outside A to Z, a to z, 0 to 9, - and _ never becomes a chip in the dialog, the field says why, a hand built save from the dialog or the settings is refused with the name as text, and nothing runs", async () => {
    const markup = `<img src=x onerror=window.dcx=1>-${tag.slice(-4)}`;
    const ran = () => page.evaluate(() => window.dcx === 1 || !!document.querySelector('img[src="x"]'));
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await page.click(`#project-${project} [data-project-tags-edit]`);
    const input = `${MODAL} dc-tag-editor [data-tag-input]`;
    await page.waitForSelector(input, { state: "visible", timeout: 8000 });
    await sleep(400);
    const chips = () => page.$$eval(`${MODAL} [data-tag-on] [data-tag-name]`, (els) => els.map((e) => e.textContent));
    const before = await chips();
    for (const name of [markup, `a b-${tag.slice(-4)}`, `caf\u00e9-${tag.slice(-4)}`]) {
      await page.fill(input, name);
      await page.press(input, "Enter");
      assert(JSON.stringify(await chips()) === JSON.stringify(before) && !(await page.$eval(input, (el) => el.validity.valid)), `${JSON.stringify(name)} became a chip or the field does not say why`);
    }
    await page.fill(input, "");
    await closeModal(page);
    const token = await page.evaluate(() => document.querySelector('meta[name="csrf-token"]').content);
    const dialog = await page.request.post(`${BASE}/projects/${project}/tags?modal=1`, { form: { csrf_token: token, new: markup }, headers: { Accept: "application/json" } });
    const reason = (await dialog.json().catch(() => ({}))).error || "";
    assert(dialog.status() === 400 && reason.includes("may hold only") && reason.includes(markup), `the dialog's save answered ${dialog.status()} ${reason}`);
    const settings = await page.request.post(`${BASE}/settings/projects/tags`, { form: { csrf_token: token, "new-name": markup } });
    const html = await settings.text();
    assert(settings.status() === 200 && html.includes("may hold only") && html.includes("&lt;img src=x") && !html.includes("<img src=x"), "the settings' refusal does not show the name as text");
    await openSettings(page, tag.slice(-4));
    assert((await page.locator(`${SETTINGS} [data-tag-row]`).evaluateAll((rows, m) => rows.filter((r) => r.dataset.loadedName === m).length, markup)) === 0, "a refused name was made a tag");
    assert(!(await ran()), "a refused name ran");
  });

  await run("tags: in the dialog a comma ends a tag, a second spelling adds none, Enter creates, a new tag keeps the color it shows, and one taken off with its cross comes back from its offer under its id", async () => {
    const [cx, bk] = [`cx-${tag}`, `Bk-${tag}`];
    const input = `${MODAL} dc-tag-editor [data-tag-input]`;
    const open = async () => {
      await page.click(`#project-${project} [data-project-tags-edit]`);
      await page.waitForSelector(input, { state: "visible", timeout: 8000 });
      await sleep(400);
    };
    const saveDialog = async () => {
      await page.click(`${MODAL} form[data-project-tags-form] button[type="submit"]`);
      await page.waitForFunction(() => !document.querySelector("[data-form-modal]").classList.contains("show"), null, { timeout: 8000 });
    };
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await setTags(page, project, []);
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await open();
    await page.fill(input, cx);
    assert(await shown(page, `${MODAL} [data-tag-create][data-tag-next]`), "a new name offers no create for Enter");
    await page.press(input, "Enter");
    await page.type(input, ` ${bk} ,${bk.toLowerCase()}`, { delay: 20 });
    await page.press(input, "Enter");
    const on = await page.$$eval(`${MODAL} [data-tag-on] [data-tag-field]`, (els) => els.map((e) => `${e.name}=${e.value}`));
    assert(JSON.stringify(on) === JSON.stringify([`new=${cx}`, `new=${bk}`]), `the comma and the second spelling made ${JSON.stringify(on)}`);
    const color = await page.$eval(onChip(MODAL, bk), (e) => e.dataset.tagColor);
    assert(color && (await page.inputValue(`${onChip(MODAL, bk)} [data-tag-color-field]`)) === color, "a new tag does not post the color it shows");
    await saveDialog();
    const ids = { cx: await idOf(page, cx), bk: await idOf(page, bk) };
    const shownRow = await waitRowTags(page, project, [`${bk}:${color}:${ids.bk}`], []);
    const cxColor = shownRow.find((t) => t.startsWith(`${cx}:`))?.split(":")[1];
    assert(cxColor && cxColor !== color, `the two new tags wear ${JSON.stringify(shownRow)}`);

    await open();
    await page.click(`${onChip(MODAL, cx)} [data-tag-remove]`);
    await page.click(`${MODAL} [data-tag-offer="${cx}"]`);
    assert(await page.$eval(onChip(MODAL, cx), (e, id) => e.dataset.tag === id && e.querySelector("[data-tag-field]").name === "tag" && e.querySelector("[data-tag-field]").value === id, ids.cx), "the tag tapped again did not come back under its id");
    await saveDialog();
    await waitRowTags(page, project, [`${cx}:${cxColor}:${ids.cx}`, `${bk}:${color}:${ids.bk}`], []);
  });

  await run("tags: the page offers the tags in use, the field narrows them, a tap adds one in its color under its id, one taken off is offered again, and a refusal shows the form again at its address with what was sent", async () => {
    const [cx, bk, tl] = [`cx-${tag}`, `Bk-${tag}`, `tl-${tag}`];
    const editor = "dc-tag-editor";
    const offers = () => page.$$eval(`${editor} [data-tag-offer]`, (els) => els.filter((e) => e.getClientRects().length > 0).map((e) => e.dataset.tagOffer));
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    const ids = { cx: await idOf(page, cx), bk: await idOf(page, bk) };
    const colors = Object.fromEntries((await rowTags(page, project)).map((t) => t.split(":")));
    await setTags(page, source, []);
    await page.goto(`${BASE}/projects/${source}/tags`, { waitUntil: "domcontentloaded" });
    await page.waitForSelector(`${editor} [data-tag-offer="${bk}"]`, { state: "visible", timeout: 8000 });
    let offered = await offers();
    assert(offered.includes(cx) && offered.includes(bk), `the page offers ${JSON.stringify(offered)}`);
    await page.fill(`${editor} [data-tag-input]`, cx);
    offered = await offers();
    assert(JSON.stringify(offered) === JSON.stringify([cx]), `the field narrows the offers to ${JSON.stringify(offered)}`);
    await page.fill(`${editor} [data-tag-input]`, "");
    await page.click(`${editor} [data-tag-offer="${bk}"]`);
    assert(await page.$eval(onChip(editor, bk), (e, [id, color]) => e.classList.contains(`dc-tag-${color}`) && e.dataset.tag === id && e.querySelector("[data-tag-field]").name === "tag" && e.querySelector("[data-tag-field]").value === id && !e.querySelector("[data-tag-color-field]").name, [ids.bk, colors[bk]]), "an offered tag lost its color or its id, or posts a color");
    await page.fill(`${editor} [data-tag-input]`, tl);
    await page.press(`${editor} [data-tag-input]`, "Enter");
    await page.click(`${editor} [data-tag-offer="${cx}"]`);
    await page.click(`${onChip(editor, cx)} [data-tag-remove]`);
    assert((await offers()).includes(cx), "a removed tag is not offered again");
    await page.click(`${editor} [data-tag-offer="${cx}"]`);
    assert(await page.$eval(onChip(editor, cx), (e, id) => e.dataset.tag === id && e.querySelector("[data-tag-field]").name === "tag" && e.querySelector("[data-tag-field]").value === id, ids.cx), "a tag tapped again in the offers did not come back with its id");
    assert(!(await offers()).includes(cx), "the tag tapped again is still offered");
    await page.click(`${onChip(editor, cx)} [data-tag-remove]`);
    await page.$eval(`${onChip(editor, tl)} [data-tag-color-field]`, (field) => { field.value = "teal;}"; });
    await page.click('form[data-project-tags-form] button[type="submit"]');
    await page.waitForSelector('.alert:has-text("none of the tag colors")', { state: "visible", timeout: 8000 });
    assert(new URL(page.url()).pathname === `/projects/${source}/tags` && !page.url().includes("?"), `the refusal moved to ${page.url()}`);
    const kept = await page.$$eval(`${editor} [data-tag-on] [data-tag-name]`, (els) => els.map((e) => e.textContent));
    assert(JSON.stringify(kept) === JSON.stringify([bk, tl]), `the refusal kept ${JSON.stringify(kept)}`);
    assert((await page.$$eval("[class]", (els) => els.filter((e) => e.className.toString().includes(";")).length)) === 0, "the refused color reached a class");
    const stored = await (await page.request.get(`${BASE}/projects/${source}/tags?modal=1`)).text();
    assert(!stored.includes('name="tag" value="'), "a refused save wrote tags");
    await Promise.all([
      page.waitForURL(new RegExp(`/projects#project-${source}$`), { timeout: 8000 }),
      page.click('form[data-project-tags-form] button[type="submit"]'),
    ]);
    const alert = await page.locator(`#project-${source} .alert`).textContent({ timeout: 8000 });
    assert(alert.includes(`Tags of "${source}" saved.`), `the row says ${alert}`);
    const saved = await waitRowTags(page, source, [`${bk}:${colors[bk]}:${ids.bk}`], [cx]);
    assert(saved.some((t) => t.startsWith(`${tl}:`)), `the save made ${JSON.stringify(saved)}`);
  });

  await run("tags: a tag filter leaves the worktrees of its main projects folded, an unfolded group shows them, the pick survives a reload and a rename, Clear the filters resets, and the text filter finds a main by a tag and unfolds its worktree", async () => {
    const tl = `tl-${tag}`;
    const bar = "dc-project-list [data-tag-bar]";
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await page.evaluate(() => localStorage.removeItem("dc-project-worktrees-open"));
    await page.reload({ waitUntil: "domcontentloaded" });
    try {
      const id = await idOf(page, tl);
      const cx = await idOf(page, `cx-${tag}`);
      await page.click(`#project-${source} [data-tag-toggle="${id}"]`);
      await sleep(200);
      assert(await shown(page, `#project-${source}`) && !(await shown(page, `#project-${project}`)), "the tag filter shows the wrong projects");
      assert(!(await shown(page, `#project-${worktree}`)), "the tag filter unfolded a worktree");
      await page.click(`#project-${source} [data-worktrees-toggle]`);
      await sleep(200);
      assert(await shown(page, `#project-${worktree}`), "an unfolded group hides its worktree under the tag filter");
      await page.click(`#project-${source} [data-worktrees-toggle]`);
      await sleep(200);
      assert(!(await shown(page, `#project-${worktree}`)), "the group did not fold again");

      const rename = (name) => L.saveTagSettings(page, (fields) => L.setField(fields, `name:${id}`, name));
      await rename(`tm-${tag}`);
      await page.reload({ waitUntil: "domcontentloaded" });
      await sleep(300);
      assert(!(await shown(page, `#project-${project}`)) && await shown(page, `#project-${source}`), "the reload or the rename forgot the tag filter");
      assert((await page.textContent(`${bar} [data-tag-toggle="${id}"][aria-pressed="true"]`)).startsWith(`tm-${tag}`), "the renamed tag is not the one pressed");
      assert((await page.textContent(`#project-${source} [data-tag-toggle="${id}"] .dc-tag-name`)) === `tm-${tag}`, "the row's chip does not name the renamed tag");
      await rename(tl);
      await page.reload({ waitUntil: "domcontentloaded" });
      await sleep(300);
      await page.click("dc-project-list [data-project-filter-clear]");
      await sleep(200);
      await page.click(`#project-${project} [data-tag-toggle="${cx}"]`);
      await sleep(200);
      await page.fill("[data-project-filter]", worktree);
      await sleep(200);
      assert(await shown(page, "[data-project-filter-empty]"), "nothing matches and the board does not say so");
      await page.click("[data-project-filter-reset]");
      await sleep(200);
      assert(await shown(page, `#project-${source}`) && await shown(page, `#project-${project}`) && (await page.inputValue("[data-project-filter]")) === "", "Clear the filters left one");
      await page.fill("[data-project-filter]", tl);
      await sleep(200);
      assert(await shown(page, `#project-${source}`) && await shown(page, `#project-${worktree}`) && !(await shown(page, `#project-${project}`)), "the text filter does not find the main and unfold its worktree by a tag");
    } finally {
      await page.evaluate(() => { localStorage.removeItem("dc-project-tag-filter"); localStorage.removeItem("dc-project-filter"); localStorage.removeItem("dc-project-worktrees-open"); });
    }
  });

  await run("tags: every color reads on its tint, pressed and counted, in light and in dark", async () => {
    try {
      for (const scheme of ["light", "dark"]) {
        await page.emulateMedia({ colorScheme: scheme });
        await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
        const result = await page.evaluate((colors) => {
          const canvas = document.createElement("canvas");
          canvas.width = canvas.height = 1;
          const ctx = canvas.getContext("2d", { willReadFrequently: true });
          const paint = (layers) => {
            ctx.clearRect(0, 0, 1, 1);
            for (const css of layers) { ctx.fillStyle = css; ctx.fillRect(0, 0, 1, 1); }
            return Array.from(ctx.getImageData(0, 0, 1, 1).data).slice(0, 3);
          };
          const lum = (rgb) => rgb.map((c) => c / 255).map((c) => (c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4)).reduce((sum, c, i) => sum + c * [0.2126, 0.7152, 0.0722][i], 0);
          const ratio = (a, b) => { const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x); return (hi + 0.05) / (lo + 0.05); };
          const bar = document.querySelector("dc-project-list [data-tag-bar]");
          const surface = getComputedStyle(bar).backgroundColor;
          let worst = { ratio: Infinity };
          for (const color of colors) {
            for (const pressed of ["false", "true"]) {
              const chip = document.createElement("button");
              chip.className = `dc-tag dc-tag-${color}`;
              chip.setAttribute("aria-pressed", pressed);
              chip.innerHTML = '<span class="dc-tag-name">tag</span><span class="dc-tag-count">2</span>';
              bar.append(chip);
              const tint = getComputedStyle(chip).backgroundColor;
              const text = getComputedStyle(chip).color;
              const count = getComputedStyle(chip.querySelector(".dc-tag-count"));
              const pairs = [["name", text, paint([surface, tint])], ["count", count.color, paint([surface, tint, count.backgroundColor])]];
              chip.remove();
              for (const [where, ink, back] of pairs) {
                const r = ratio(paint([ink]), back);
                if (r < worst.ratio) worst = { ratio: r, color, pressed, where };
              }
            }
          }
          return { worst, dark: lum(paint([surface])) < 0.2 };
        }, TAG_COLORS);
        assert(result.dark === (scheme === "dark"), `the ${scheme} scheme did not reach the page`);
        assert(result.worst.ratio >= 4.5, `in ${scheme} ${JSON.stringify(result.worst)} falls under 4.5:1`);
      }
    } finally {
      await page.emulateMedia({ colorScheme: null });
    }
  });

  await run("tags: the editor's project switcher and the terminal switcher find a worktree by its main project's tag", async () => {
    const tl = `tl-${tag}`;
    await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    const id = await idOf(page, tl);
    await page.goto(`${BASE}/projects/${project}/editor`, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(page);
    await L.editorReady(page);
    await page.click("[data-editor-project-switch]");
    await page.waitForSelector("[data-editor-palette]", { state: "visible", timeout: 4000 });
    await page.fill("[data-editor-project-filter]", tl);
    await sleep(300);
    const rows = await page.$$eval("[data-editor-palette-list] [data-project-name]", (els) => els.filter((e) => e.getClientRects().length > 0).map((e) => e.dataset.projectName));
    assert(rows.includes(source) && rows.includes(worktree) && !rows.includes(project), `the palette shows ${JSON.stringify(rows)}`);
    assert(await shown(page, `[data-editor-palette-list] [data-project-name="${source}"] [data-tag="${id}"]`), "the palette row misses its tag");
    await page.keyboard.press("Escape");
    await page.waitForSelector("[data-editor-palette]", { state: "hidden", timeout: 4000 });

    await page.keyboard.press("Control");
    await page.keyboard.press("Control");
    await page.waitForSelector(".terminal-switcher", { state: "visible", timeout: 4000 });
    await page.keyboard.type(tl, { delay: 20 });
    await sleep(300);
    const editors = await page.$$eval('.terminal-switcher-item[data-switcher-section="editors"]', (els) => els.filter((e) => e.getClientRects().length > 0).map((e) => e.dataset.switcherUrl));
    assert(editors.some((u) => u.startsWith(`/projects/${source}/editor`)) && editors.some((u) => u.startsWith(`/projects/${worktree}/editor`)) && !editors.some((u) => u.startsWith(`/projects/${project}/editor`)), `the switcher shows ${JSON.stringify(editors)}`);
    assert(await shown(page, `.terminal-switcher-item[data-switcher-url^="/projects/${source}/editor"] [data-tag="${id}"]`), "the switcher row misses its tag");
    await page.keyboard.press("Escape");
  });

  await run("tags: deleting a project takes it off its tags, which live on with no project, and a project made under its name carries none", async () => {
    const name = `zzdt-${tag.slice(-6)}`;
    const dl = `dl-${tag}`;
    await L.createProject(page, name);
    try {
      await setTags(page, name, [dl]);
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await idOf(page, dl);
      await L.deleteProject(page, name);
      await openSettings(page, dl);
      const made = `${SETTINGS} [data-tag-row][data-loaded-name="${dl}"]`;
      await page.waitForSelector(made, { state: "attached", timeout: 8000 });
      assert((await page.textContent(`${made} .dc-tag-row-count`)).trim() === "0 projects", "the deleted project still carries its tag");
      await L.createProject(page, name);
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector(`#project-${name}`, { state: "attached", timeout: 8000 });
      assert((await rowTags(page, name)).length === 0, `a project made under the deleted name carries ${JSON.stringify(await rowTags(page, name))}`);
    } finally {
      await L.deleteProject(page, name).catch(() => {});
    }
  });

  await run("tags: at 390px every chip and cross takes a thumb in a bar and a tag line of one row each, the sheet keeps its own picks with the count, colors and finds by a main's tag, and the dialog grows with the screen and fits landscape", async () => {
    const mp = await mobilePage();
    await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(mp);
    const [p1, p2] = ["pa", "pb"].map((p) => `${p}-${tag}`);
    await setTags(mp, project, [p1, p2]);
    try {
      await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      const id = await idOf(mp, p1);
      const chip = `#project-${project} [data-tag-toggle="${id}"]`;
      await mp.waitForSelector(chip, { state: "visible", timeout: 8000 });
      const box = await mp.locator(chip).boundingBox();
      assert(box.height <= 28 && await L.reach(mp, chip, [[0, -15], [0, 15]]), `a row's chip is ${box.height}px high and a thumb 15px off misses it`);
      assert(await L.reach(mp, `#project-${project} [data-project-tags-edit]`, [[0, -15], [0, 15]]), "the dashed chip misses a thumb");
      assert(await L.reach(mp, "dc-project-list [data-tags-manage]", [[0, -15], [0, 15]]), "the bar's pencil misses a thumb");
      const barChip = `dc-project-list [data-tag-bar] [data-tag-toggle="${id}"]`;
      const barBox = await mp.locator(barChip).boundingBox();
      assert(barBox.height <= 28 && await L.reach(mp, barChip, [[0, -15], [0, 15]]), `a bar chip is ${barBox.height}px high and a thumb 15px off misses it`);
      const heights = { bar: (await mp.locator("dc-project-list [data-tag-bar]").boundingBox()).height, line: (await mp.locator(`#project-${project} [data-tag-line]`).boundingBox()).height };
      assert(heights.bar <= 34 && heights.line <= 34, `the bar and the tag line take ${JSON.stringify(heights)}`);
      const color = (await rowTags(mp, project)).find((t) => t.startsWith(`${p1}:`)).split(":")[1];
      const tl = await idOf(mp, `tl-${tag}`);
      await mp.tap(chip);
      await sleep(300);
      const picked = `dc-project-list [data-tag-picked] [data-tag-unpick="${id}"]`;
      assert(await shown(mp, picked) && await L.reach(mp, picked, [[0, -15], [0, 15]]), "the field's chip is not there or misses a thumb");
      assert(/^1 of \d+$/.test((await mp.textContent("dc-project-list [data-filter-count]")).trim()), "the phone's field does not count");

      await mp.click('.dc-tabbar button[data-ctx-area="projects"]');
      const sheet = "dc-ctx-sheet:not([hidden])";
      await mp.waitForSelector(`${sheet} [data-index-project="${project}"]`, { state: "attached", timeout: 8000 });
      await sleep(300);
      assert((await mp.locator(`${sheet} [data-tag-picked] [data-tag-unpick]`).count()) === 0, "the sheet took the board's picks");
      assert((await mp.locator(`${sheet} [data-index-project="${project}"] .dc-tag-slice.dc-tag-${color}[data-tag="${id}"]`).count()) === 1, "the sheet's row misses its colored tag");
      await mp.click(`${sheet} [data-tag-bar] [data-tag-toggle="${tl}"]`);
      await sleep(300);
      assert(await shown(mp, `${sheet} [data-tag-picked] [data-tag-unpick="${tl}"]`) && /^1 of \d+$/.test((await mp.textContent(`${sheet} [data-filter-count]`)).trim()), "the sheet's field shows no pick or count");
      assert(await shown(mp, `${sheet} [data-index-project="${source}"]`) && !(await shown(mp, `${sheet} [data-index-project="${worktree}"]`)) && !(await shown(mp, `${sheet} [data-index-project="${project}"]`)), "the sheet's pick did not filter to the main with its worktree folded");
      await mp.click(`${sheet} [data-ctx-filter-clear]`);
      await sleep(200);
      assert((await mp.locator(`${sheet} [data-tag-picked] [data-tag-unpick]`).count()) === 0 && await shown(mp, `${sheet} [data-index-project="${project}"]`), "the sheet's cross left its pick");
      await mp.fill(`${sheet} [data-ctx-filter]`, `tl-${tag}`);
      await sleep(300);
      assert(await shown(mp, `${sheet} [data-index-project="${worktree}"]`) && !(await shown(mp, `${sheet} [data-index-project="${project}"]`)), "the sheet's text filter does not find the worktree by its main's tag");
      await mp.fill(`${sheet} [data-ctx-filter]`, "");
      await mp.keyboard.press("Escape");
      await sleep(300);
      assert((await mp.getAttribute(barChip, "aria-pressed")) === "true", "the sheet moved the board's tag filter");
      await mp.click("dc-project-list [data-project-filter-clear]");

      for (const scheme of ["light", "dark"]) {
        await mp.emulateMedia({ colorScheme: scheme });
        await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
        await mp.tap(`#project-${project} [data-project-tags-edit]`);
        await mp.waitForSelector(`${MODAL} dc-tag-editor [data-tag-on]`, { state: "visible", timeout: 8000 });
        await sleep(400);
        const dialog = await dialogBox(mp);
        assert(dialog.fits && dialog.height >= 844 * 0.85, `in ${scheme} the dialog stands at ${JSON.stringify(dialog)}`);
        assert(await L.reach(mp, `${MODAL} [data-tag-on] [data-tag-remove]`, [[-15, 0], [15, 0], [0, -15], [0, 15]]), "a chip's cross misses a thumb");
        await closeModal(mp);
      }
      await mp.emulateMedia({ colorScheme: null });
      await mp.setViewportSize({ width: 844, height: 390 });
      await mp.tap(`#project-${project} [data-project-tags-edit]`);
      await mp.waitForSelector(`${MODAL} dc-tag-editor`, { state: "visible", timeout: 8000 });
      await sleep(400);
      const landscape = await dialogBox(mp);
      const button = await mp.locator(`${MODAL} button[type="submit"]`).boundingBox();
      assert(landscape.fits && button && button.y + button.height <= 390, `in landscape the dialog stands at ${JSON.stringify(landscape)}, Save at ${JSON.stringify(button)}`);
      await closeModal(mp);
    } finally {
      await mp.emulateMedia({ colorScheme: null });
      await mp.setViewportSize({ width: 390, height: 844 });
      await mp.evaluate(() => { localStorage.removeItem("dc-project-tag-filter"); localStorage.removeItem("dc-ctx-tags:projects"); });
      await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await L.deleteProject(mp, other).catch(() => {});
    }
  });
};
