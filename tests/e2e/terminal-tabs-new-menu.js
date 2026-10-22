const L = require("./lib");
const { assert, sleep, BASE } = L;

// The + menu of the tab strip and its filter field, run by terminal-tabs.js on
// its project and one of its shells. Cmd+T opens the menu with the keyboard
// in the field, empty, on its first row; a mouse open marks nothing. Typing
// hides the rows that miss and ranks the hits of each group like the
// switcher, the arrows walk the rows left and wrap, Tab stays in the field,
// Enter takes the marked row and Esc closes and hands the focus back. Two
// stopped coders of the runner's own make the resume group the ranking is
// seen in; a name starting a word earlier comes first. The X of the field
// stands only while it holds text, a click empties it in the open menu. An
// emptied filter folds a resume group in its own order again, so four stopped
// coders, the newest ranked last by the filter, keep the newest in front of
// Show N more. On a phone the menu opens from the sheet without focusing the
// field, a keyboard would cover it.

module.exports = async function terminalTabsNewMenu({ run, page, mobilePage, project, shellUrl }) {
  const menu = "terminal-tabs .terminal-tabs-new-menu";
  const state = (target = page, root = "terminal-tabs") => target.evaluate((root) => {
    const box = document.querySelector(`${root} .terminal-tabs-new-menu`);
    const shown = (node) => Boolean(node) && node.getClientRects().length > 0;
    const key = (row) => row.getAttribute("data-tabs-new") || row.getAttribute("data-resume-id") || row.textContent.trim();
    const field = box.querySelector("[data-tabs-new-filter]");
    const clear = box.querySelector("[data-tabs-new-filter-clear]");
    return {
      open: box.classList.contains("show") && shown(box),
      rows: [...box.querySelectorAll(".dropdown-item")].filter(shown).map(key),
      selected: [...box.querySelectorAll(".dropdown-item.selected")].map(key),
      focused: document.activeElement === field,
      terminal: Boolean(document.activeElement?.closest(".xterm")),
      value: field.value,
      none: shown(box.querySelector("[data-tabs-new-none]")),
      head: shown(box.querySelector("[data-tabs-resume-head]")),
      divider: shown(box.querySelector("[data-tabs-resume-divider]")),
      clear: shown(clear) && getComputedStyle(clear).visibility !== "hidden",
      more: Object.fromEntries([...box.querySelectorAll("[data-tabs-resume-fold]")].map((group) => {
        const toggle = group.querySelector(":scope > [data-tabs-resume-toggle]");
        return [group.getAttribute("data-tabs-resume-fold"), shown(toggle) ? toggle.textContent.trim() : ""];
      })),
    };
  }, root);
  const opened = () => page.waitForSelector(`${menu}.show`, { state: "visible", timeout: 4000 });
  const closed = () => page.waitForSelector(`${menu}.show`, { state: "detached", timeout: 4000 });
  const toTerminal = async () => {
    await page.goto(shellUrl, { waitUntil: "domcontentloaded" });
    assert((await L.waitUpgraded(page, ["terminal-tabs"], 8000)).length === 0, "terminal-tabs not upgraded");
    await page.waitForSelector(".attach-terminal .xterm-screen canvas", { timeout: 15000 });
    await page.click(".attach-terminal");
    await sleep(200);
  };

  // Cmd is no terminal modifier, so the pane loses nothing by it.
  await run("Cmd+T opens the + menu on its first row with the keyboard in its filter, a mouse open selects nothing", async () => {
    await toTerminal();
    await page.keyboard.press("Meta+t");
    await opened();
    let st = await state();
    assert(st.selected.join() === "coder", `the menu did not open on its first row: ${JSON.stringify(st)}`);
    assert(st.focused && st.value === "", `the filter does not hold the keyboard, empty: ${JSON.stringify(st)}`);
    await page.keyboard.press("ArrowDown");
    st = await state();
    assert(st.selected.join() === "shell", `the arrow did not step to the new shell row: ${JSON.stringify(st)}`);
    await page.keyboard.press("ArrowUp");
    st = await state();
    assert(st.selected.join() === "coder", `the arrow did not step back: ${JSON.stringify(st)}`);
    await page.keyboard.press("Escape");
    await closed();
    st = await state();
    assert(st.selected.length === 0, "the selection outlived the closed menu");
    assert(st.terminal, "Esc did not hand the keyboard back to the terminal");

    // The pointer is the pointer: nothing is marked until the arrows or the
    // pointer mark a row, and leaving the menu clears the mark.
    await page.click("terminal-tabs [data-tabs-new-menu]");
    await opened();
    st = await state();
    assert(st.selected.length === 0, `the mouse opened menu preselected a row: ${JSON.stringify(st)}`);
    assert(st.focused, "a fine pointer open did not put the keyboard in the filter");
    await page.keyboard.press("ArrowDown");
    st = await state();
    assert(st.selected.join() === "coder", `the arrow did not take the first row: ${JSON.stringify(st)}`);
    await page.hover(`${menu} [data-tabs-new="shell"]`);
    st = await state();
    assert(st.selected.join() === "shell", `the pointer did not mark the hovered row: ${JSON.stringify(st)}`);
    await page.keyboard.press("ArrowUp");
    st = await state();
    assert(st.selected.join() === "coder", `the arrow did not continue from the hovered row: ${JSON.stringify(st)}`);
    const vp = page.viewportSize();
    await page.mouse.move(vp.width - 2, vp.height - 2);
    st = await state();
    assert(st.selected.length === 0, `leaving the menu left a row marked: ${JSON.stringify(st)}`);
    await page.keyboard.press("Escape");
    await closed();

    // Enter on the focused + button is a keyboard open too.
    await page.focus("terminal-tabs [data-tabs-new-menu]");
    await page.keyboard.press("Enter");
    await opened();
    st = await state();
    assert(st.selected.join() === "coder" && st.focused, `Enter on the + button did not open on the first row: ${JSON.stringify(st)}`);
    await page.keyboard.press("Escape");
    await closed();
    return "keyboard opens on the first row in the filter, the mouse on none";
  });

  await run("the + menu filter hides and ranks the rows, the arrows walk the hits and wrap, Tab stays, Enter takes, Esc closes", async () => {
    const prefix = "fq" + String(Date.now()).slice(-6);
    const names = { ab: `${prefix}-ab-cd`, cd: `${prefix}-cd-ab` };
    const ids = {};
    try {
      for (const [word, name] of Object.entries(names)) {
        const url = await L.createSession(page, project, name);
        ids[word] = new URL(url).pathname.split("/").pop();
        await L.stopSession(page, url);
      }
      await toTerminal();
      for (const id of Object.values(ids)) await page.waitForSelector(`${menu} [data-resume-id="${id}"]`, { state: "attached", timeout: 8000 });
      const own = (rows) => rows.filter((row) => Object.values(ids).includes(row));
      await page.keyboard.press("Meta+t");
      await opened();
      let st = await state();
      const built = own(st.rows);
      assert(built.length === 2 && st.head && st.divider, `the menu does not list both stopped coders: ${JSON.stringify(st)}`);

      await page.keyboard.type(prefix, { delay: 30 });
      st = await state();
      assert(JSON.stringify(st.rows) === JSON.stringify(built), `the filter did not narrow to the two coders in their order: ${JSON.stringify(st)}`);
      assert(st.selected.join() === built[0], `the first hit is not marked while typing: ${JSON.stringify(st)}`);
      assert(st.head && !st.divider && !st.none, `the heads around the hits are wrong: ${JSON.stringify(st)}`);
      for (let i = 0; i < prefix.length; i += 1) await page.keyboard.press("Backspace");
      st = await state();
      assert(st.rows[0] === "coder" && st.rows[1] === "shell" && JSON.stringify(own(st.rows)) === JSON.stringify(built), `an empty filter did not bring every row back in its order: ${JSON.stringify(st)}`);

      // The first token ranks, a word start earlier in the name comes first,
      // so the coder listed second moves up.
      const second = built[1];
      const word = second === ids.cd ? "cd" : "ab";
      await page.keyboard.type(`${word} ${prefix}`, { delay: 30 });
      st = await state();
      assert(JSON.stringify(st.rows) === JSON.stringify([second, built[0]]), `the better hit did not move up: ${JSON.stringify(st)}`);
      assert(st.selected.join() === second, `the first hit is not marked: ${JSON.stringify(st)}`);
      await page.keyboard.press("ArrowDown");
      assert((await state()).selected.join() === built[0], "ArrowDown did not step to the next hit");
      await page.keyboard.press("ArrowDown");
      assert((await state()).selected.join() === second, "ArrowDown did not wrap to the first hit");
      await page.keyboard.press("ArrowUp");
      assert((await state()).selected.join() === built[0], "ArrowUp did not wrap to the last hit");
      await page.keyboard.press("Tab");
      await page.keyboard.press("Shift+Tab");
      st = await state();
      assert(st.open && st.focused && st.selected.join() === built[0], `Tab moved the focus or the mark: ${JSON.stringify(st)}`);

      // Bootstrap closes the menu on a click inside it, a click into the field
      // and onto its icon must not.
      await page.click(`${menu} [data-tabs-new-filter]`);
      await page.click(`${menu} .editor-sheet-filter > .ti-search`);
      await sleep(200);
      st = await state();
      assert(st.open && st.focused && st.value === `${word} ${prefix}`, `a click on the filter closed the menu or took the focus: ${JSON.stringify(st)}`);

      await page.keyboard.type("zzz", { delay: 30 });
      st = await state();
      assert(st.rows.length === 0 && st.none && !st.head && st.selected.length === 0, `no hits left a row or no empty state: ${JSON.stringify(st)}`);
      await page.keyboard.press("Enter");
      assert((await state()).open, "Enter without a hit closed the menu");
      await page.keyboard.press("Escape");
      await closed();
      await page.keyboard.press("Meta+t");
      await opened();
      st = await state();
      assert(st.value === "" && st.rows[0] === "coder" && JSON.stringify(own(st.rows)) === JSON.stringify(built), `the menu did not open again empty and in order: ${JSON.stringify(st)}`);

      await page.keyboard.type(`${word} ${prefix}`, { delay: 30 });
      await page.keyboard.press("Enter");
      await page.waitForURL(new RegExp(`/coders/${second}$`), { timeout: 20000 });
      await page.waitForSelector(`terminal-tabs .terminal-tab[data-tab-id="${second}"].active`, { state: "attached", timeout: 8000 });
      return `ranked ${second} over ${built[0]}, Enter resumed it`;
    } finally {
      for (const id of Object.values(ids)) {
        await page.evaluate(async (target) => {
          await fetch(`/coders/${target}/delete`, {
            method: "POST",
            headers: { "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]').content, Accept: "application/json" },
          });
        }, id).catch(() => {});
      }
    }
  });

  await run("the + menu filter's X stands only while the field holds text, a click empties it and the menu stays open with the keyboard in the field", async () => {
    await toTerminal();
    await page.keyboard.press("Meta+t");
    await opened();
    let st = await state();
    const rows = st.rows;
    assert(st.focused && st.value === "" && !st.clear, `the X stands on an empty field: ${JSON.stringify(st)}`);
    await page.keyboard.type("shell", { delay: 30 });
    st = await state();
    assert(st.clear && st.rows.join() === "shell", `typing did not bring the X or did not filter: ${JSON.stringify(st)}`);
    await page.click(`${menu} [data-tabs-new-filter-clear]`);
    await sleep(200);
    st = await state();
    assert(st.open && st.focused && st.value === "" && !st.clear, `the X closed the menu, took the focus or left text: ${JSON.stringify(st)}`);
    assert(JSON.stringify(st.rows) === JSON.stringify(rows), `the X did not bring every row back in its order: ${JSON.stringify(st)}`);
    await page.keyboard.press("Escape");
    await closed();
    return "the X empties the filter in the open menu";
  });

  await run("an emptied + menu filter folds the resume group in its own order, the newest coder stays in front of Show N more", async () => {
    const prefix = "fo" + String(Date.now()).slice(-6);
    // Every name holds every key and its own key last, so the newest coder's
    // key ranks it behind the other three.
    const keys = ["k0", "k1", "k2", "k3"];
    const ids = [];
    try {
      for (let i = 0; i < keys.length; i += 1) {
        const url = await L.createSession(page, project, [prefix, ...keys.slice(i + 1), ...keys.slice(0, i + 1)].join("-"));
        ids.push(new URL(url).pathname.split("/").pop());
        await L.stopSession(page, url);
      }
      await toTerminal();
      for (const id of ids) await page.waitForSelector(`${menu} [data-resume-id="${id}"]`, { state: "attached", timeout: 8000 });
      const own = (rows) => rows.filter((row) => ids.includes(row));
      const newest = await page.$eval(`${menu} [data-tabs-resume-fold="${project}"]`, (group) => group.querySelector("[data-resume-id]").dataset.resumeId);
      assert(ids.includes(newest), `the newest coder of the project is none of the runner's four: ${newest}`);
      await page.keyboard.press("Meta+t");
      await opened();
      let st = await state();
      const folded = own(st.rows);
      assert(folded[0] === newest && folded.length === 3 && /^Show \d+ more$/.test(st.more[project]), `the group is not folded behind the newest coder: ${JSON.stringify(st)}`);

      const query = `${keys[ids.indexOf(newest)]} ${prefix}`;
      await page.keyboard.type(query, { delay: 30 });
      st = await state();
      assert(own(st.rows).length === 4 && own(st.rows)[3] === newest && st.more[project] === "", `the filter did not unfold the group with the newest coder last: ${JSON.stringify(st)}`);
      for (let i = 0; i < query.length; i += 1) await page.keyboard.press("Backspace");
      st = await state();
      assert(JSON.stringify(own(st.rows)) === JSON.stringify(folded) && /^Show \d+ more$/.test(st.more[project]), `the emptied filter folded the group in the filtered order: ${JSON.stringify(st)}`);
      await page.keyboard.press("Escape");
      await closed();
      return `${newest} stays in front of ${st.more[project]}`;
    } finally {
      for (const id of ids) {
        await page.evaluate(async (target) => {
          await fetch(`/coders/${target}/delete`, {
            method: "POST",
            headers: { "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]').content, Accept: "application/json" },
          });
        }, id).catch(() => {});
      }
    }
  });

  await run("on a coarse pointer the + menu opens from the sheet without focusing its filter", async () => {
    const mp = await mobilePage();
    const sheet = "dc-ctx-sheet:not([hidden])";
    await mp.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
    await L.dismissUpdate(mp);
    await mp.tap('.dc-tabbar button[data-ctx-area="terminals"]');
    await mp.waitForSelector(`${sheet} [data-tabs-new-menu]`, { state: "visible", timeout: 8000 });
    await mp.tap(`${sheet} [data-tabs-new-menu]`);
    await mp.waitForSelector(`${sheet} .terminal-tabs-new-menu.show [data-tabs-new-filter]`, { state: "visible", timeout: 4000 });
    let st = await state(mp, sheet);
    assert(st.open && !st.focused && st.selected.length === 0, `the touch open focused the filter or marked a row: ${JSON.stringify(st)}`);
    await mp.tap(`${sheet} [data-tabs-new-filter]`);
    await mp.keyboard.type("shell");
    st = await state(mp, sheet);
    assert(st.focused && st.rows.join() === "shell", `typing on the phone did not filter: ${JSON.stringify(st)}`);
    await mp.tap('.dc-tabbar button[data-ctx-area="terminals"]');
    await mp.waitForSelector("dc-ctx-sheet[hidden]", { state: "attached", timeout: 4000 });
  });
};
