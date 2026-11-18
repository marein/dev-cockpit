const L = require("./lib");
const X = require("./browser-extension-fixture");
const { assert } = L;

// Not a runner of its own: browser-extension.js runs these checks on its
// panel with a tray to send. The Send menu follows the cockpit's + menu of
// Terminals and ranks with the cockpit's filter.js, which the zip carries.

const { PROJECT, SHOTS, TAG } = X;

function menuState(panel) {
  return panel.eval(() => {
    const seen = (n) => {
      if (!n) return false;
      const st = getComputedStyle(n);
      const b = n.getBoundingClientRect();
      return st.display !== "none" && st.visibility !== "hidden" && b.width > 0 && b.height > 0;
    };
    const menu = document.querySelector("[data-menu]");
    const field = menu.querySelector("[data-menu-filter]");
    const rows = [...menu.querySelectorAll("[role=menuitem]")].filter(seen);
    return {
      open: seen(menu),
      focus: document.activeElement === field,
      onSend: document.activeElement === document.querySelector("[data-send]"),
      query: field.value,
      above: rows.every((r) => r.getBoundingClientRect().top >= field.getBoundingClientRect().bottom),
      rows: rows.map((r) => `${r.dataset.target}:${r.querySelector(".menu-name").textContent}`),
      selected: rows.findIndex((r) => r.classList.contains("selected")),
      marks: rows.filter((r) => r.classList.contains("selected") && getComputedStyle(r).backgroundColor !== "rgba(0, 0, 0, 0)").length,
      heads: [...menu.querySelectorAll(".menu-head")].filter(seen).map((h) => h.textContent),
      lines: [...menu.querySelectorAll(".menu-line")].filter(seen).length,
      none: seen([...menu.querySelectorAll(".menu-empty")].find((n) => n.textContent === "No matches")),
      clear: seen(menu.querySelector("[data-menu-filter-clear]")),
    };
  });
}

// A menu taller than the panel scrolls it, the rows slide under the pointer
// resting on Send, and the rows a filter drops leave it again. The browser
// reports both as pointer events while the hand stays still, Firefox its
// leave a fraction of a pixel off the pointer, which Chromium does not, so
// a leave made here stands in for it. The panel ends where that scroll puts
// the pointer 20px above the rows' end, which rises past it once "taxi"
// leaves one row.
async function shortPanel(panel) {
  const { width, height } = await panel.eval(() => {
    const box = (selector) => document.querySelector(selector).getBoundingClientRect();
    const send = box("[data-send]");
    return { width: innerWidth, height: Math.round(send.top + send.height / 2 + scrollY + 20 + box("[data-menu]").bottom - box("[data-menu-rows]").bottom) };
  });
  await panel.send("Emulation.setDeviceMetricsOverride", { width, height, deviceScaleFactor: 0, mobile: false });
  await panel.eval(() => scrollTo(0, 0));
  return () => panel.send("Emulation.clearDeviceMetricsOverride");
}

function pointerOnRows(panel, at) {
  return panel.eval(({ x, y }) => {
    const rows = document.querySelector("[data-menu-rows]");
    if (!window.rowsLeft) {
      window.rowsLeft = { count: 0 };
      rows.addEventListener("pointerleave", () => (window.rowsLeft.count += 1));
    }
    return { scrolled: document.scrollingElement.scrollTop, on: rows.contains(document.elementFromPoint(x, y)), left: window.rowsLeft.count };
  }, at);
}

async function restingPointerChecks({ run, s }) {
  await run("in a panel too short for the menu, Send clicked with the mouse left where it was, a pasted filter and Enter send to the first row: rows sliding under the resting pointer and away mark nothing and keep the mark, also with a leave a fraction of a pixel off as Firefox reports it", async () => {
    await X.press(s.ext, s.site);
    await X.until(() => X.panelState(s.panel), (p) => p.picking, "no picking");
    await X.pickAt(s.site, "#buy");
    await X.until(() => X.chips(s.panel), (c) => c.length === 1, "no pick");
    await s.panel.click("[data-note]");
    await s.panel.type(`Resting ${TAG} note`);
    await s.panel.click("[data-send]");
    await s.panel.waitFor("document.querySelectorAll('[data-menu] [role=menuitem]').length > 1", 8000, "the send menu");
    const restore = await shortPanel(s.panel);
    try {
      await s.panel.key("Escape", "Escape", 27);
      await X.until(() => menuState(s.panel), (m) => !m.open, "Escape did not close the menu");
      const at = await s.panel.center("[data-send]");
      await s.panel.click("[data-send]");
      await s.panel.waitFor("document.querySelectorAll('[data-menu] [role=menuitem]').length > 1", 8000, "the send menu in the short panel");
      const under = await X.until(() => pointerOnRows(s.panel, at), (p) => p.scrolled > 0 && p.on, "the open menu did not scroll its rows under the resting pointer");
      const opened = await menuState(s.panel);
      assert(opened.selected === -1, `the rows sliding under the pointer marked one: ${JSON.stringify(opened)}`);
      await s.panel.type("taxi");
      await X.until(() => pointerOnRows(s.panel, at), (p) => !p.on && p.left > under.left, "the narrowed rows did not slide away from the resting pointer");
      const marked = await menuState(s.panel);
      assert(JSON.stringify(marked.rows) === JSON.stringify([`assistant:${s.second}`]) && marked.selected === 0 && marked.marks === 1, `the rows sliding away took the mark: ${JSON.stringify(marked)}`);
      await s.panel.eval(({ x, y }) => {
        document.querySelector("[data-menu-rows]").dispatchEvent(new PointerEvent("pointerleave", { pointerType: "mouse", clientX: x, clientY: y + 0.25 }));
      }, at);
      const drifted = await menuState(s.panel);
      assert(drifted.selected === 0 && drifted.marks === 1, `a leave a fraction of a pixel off the resting pointer took the mark: ${JSON.stringify(drifted)}`);
      await s.panel.key("Enter", "Enter", 13);
      const done = await X.until(() => X.panelState(s.panel), (p) => p.status.startsWith("Sent to"), "Enter sent nothing", 15000);
      assert(done.status === `Sent to ${s.second}. Open the assistant` && !(await menuState(s.panel)).open, done.status);
    } finally {
      await restore();
    }
  });
}

async function menuChecks({ run, s }) {
  await run("the Send menu: a focused filter on top, New first, then the assistants and the running coders; typing narrows and ranks like the cockpit, the arrows wrap, Escape empties the filter and then closes, an empty result says so", async () => {
    const at = (state, name) => state.rows.indexOf(name);
    const escape = () => s.panel.key("Escape", "Escape", 27);
    const typed = async (text, test, label) => {
      await escape();
      await X.until(() => menuState(s.panel), (m) => m.open && m.query === "", "Escape did not empty the filter");
      if (text) await s.panel.type(text);
      return X.until(() => menuState(s.panel), (m) => m.query === text && test(m), label);
    };
    const first = `assistant:${s.title}`;
    const second = `assistant:${s.second}`;
    const coder = `coder:${s.coderName}`;
    const fresh = s.newWith.map((one) => `new:New with ${one.name}`);
    await s.panel.click("[data-send]");
    await s.panel.waitFor("document.querySelectorAll('[data-menu] [role=menuitem]').length > 1", 8000, "the send menu");
    const open = await menuState(s.panel);
    const kinds = open.rows.map((one) => one.split(":")[0]).join(" ");
    assert(open.focus && open.above && open.selected === -1 && !open.clear && !open.none, `the menu as it opens: ${JSON.stringify(open)}`);
    assert(JSON.stringify(open.rows.slice(0, fresh.length)) === JSON.stringify(fresh) && /^(new )+(assistant )+(coder ?)+$/.test(kinds) && at(open, first) >= 0 && at(open, second) >= 0 && at(open, coder) >= 0, `the rows: ${JSON.stringify(open.rows)}`);
    assert(JSON.stringify(open.heads) === JSON.stringify(["Assistants", "Running coders"]) && open.lines === 2, `heads ${JSON.stringify(open.heads)}, lines ${open.lines}`);
    await s.panel.eval((name) => {
      document.querySelectorAll("[data-runner-aim]").forEach((el) => el.removeAttribute("data-runner-aim"));
      [...document.querySelectorAll("[data-menu] [role=menuitem]")].find((r) => r.querySelector(".menu-name").textContent === name).setAttribute("data-runner-aim", "");
    }, s.title);
    await s.panel.hover("[data-runner-aim]");
    const hovered = await X.until(() => menuState(s.panel), (m) => m.selected === at(open, first), "the row under the pointer is not marked");
    assert(hovered.marks === 1 && hovered.focus, JSON.stringify(hovered));
    await s.panel.mouse("mouseMoved", 1, 1);
    await X.until(() => menuState(s.panel), (m) => m.selected === -1, "the mark stayed after the pointer left");
    const aim = await s.panel.center("[data-runner-aim]");
    await s.panel.mouse("mouseMoved", aim.x, aim.y);
    await X.until(() => menuState(s.panel), (m) => m.selected === at(open, first), "the row under the pointer is not marked again");
    await s.panel.mouse("mouseMoved", -20, aim.y);
    await X.until(() => menuState(s.panel), (m) => m.selected === -1, "the mark stayed after the pointer left the panel from the row");
    if (SHOTS) {
      for (const dark of [false, true]) {
        await s.panel.scheme(dark);
        await s.panel.shot(`${SHOTS}/panel-menu-${dark ? "dark" : "light"}.png`);
      }
      await s.panel.scheme(false);
    }

    await s.panel.type("t");
    const byT = await X.until(() => menuState(s.panel), (m) => m.query === "t", "typing reached no filter");
    assert(byT.selected === 0 && byT.marks === 1 && byT.clear && at(byT, second) >= 0 && at(byT, second) < at(byT, first), `"t" ranks ${s.second} first: ${JSON.stringify(byT.rows)}`);
    const byExt = await typed("ext", (m) => at(m, first) >= 0, "no rows for ext");
    assert(at(byExt, second) > at(byExt, first) && !byExt.rows.some((one) => one.startsWith("new:")), `"ext" ranks ${s.title} first: ${JSON.stringify(byExt.rows)}`);
    const byProject = await typed(PROJECT, (m) => m.rows.length === 1, `${PROJECT} did not narrow to the coder`);
    assert(byProject.rows[0] === coder && JSON.stringify(byProject.heads) === JSON.stringify(["Running coders"]) && byProject.lines === 0 && byProject.selected === 0, JSON.stringify(byProject));
    const nothing = await typed("zq9 nothing", (m) => m.rows.length === 0, "a filter matching nothing left rows");
    assert(nothing.none && nothing.heads.length === 0 && nothing.selected === -1 && nothing.open, JSON.stringify(nothing));
    if (SHOTS) {
      for (const dark of [false, true]) {
        await s.panel.scheme(dark);
        await s.panel.shot(`${SHOTS}/panel-menu-empty-${dark ? "dark" : "light"}.png`);
      }
      await s.panel.scheme(false);
    }
    const back = await typed("", () => true, "the filter did not empty");
    assert(JSON.stringify(back.rows) === JSON.stringify(open.rows) && !back.none && !back.clear, `the emptied filter: ${JSON.stringify(back.rows)}`);
    await escape();
    const shut = await X.until(() => menuState(s.panel), (m) => !m.open, "a second Escape did not close the menu");
    assert(shut.onSend, "the focus did not go back to Send");

    await s.panel.click("[data-note]");
    await s.panel.key("Enter", "Enter", 13, 2);
    await s.panel.waitFor("document.querySelectorAll('[data-menu] [role=menuitem]').length > 1", 8000, "the send menu after Ctrl+Enter");
    const keyed = await menuState(s.panel);
    assert(keyed.focus && keyed.selected === 0 && keyed.marks === 1, `opened by Ctrl+Enter: ${JSON.stringify(keyed)}`);
    await s.panel.key("ArrowUp", "ArrowUp", 38);
    const last = await X.until(() => menuState(s.panel), (m) => m.selected === keyed.rows.length - 1, "ArrowUp did not wrap to the last row");
    await s.panel.key("ArrowDown", "ArrowDown", 40);
    await X.until(() => menuState(s.panel), (m) => m.selected === 0, "ArrowDown did not wrap to the first row");
    await s.panel.key("ArrowDown", "ArrowDown", 40);
    await s.panel.key("Tab", "Tab", 9);
    const moved = await X.until(() => menuState(s.panel), (m) => m.selected === 1, "ArrowDown did not move");
    assert(moved.focus && last.marks === 1, "Tab took the focus out of the filter");
    await escape();
    await X.until(() => menuState(s.panel), (m) => !m.open, "Escape did not close the menu");
  });
}

module.exports = { menuChecks, menuState, restingPointerChecks };
