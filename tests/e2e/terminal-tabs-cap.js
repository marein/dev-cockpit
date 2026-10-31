const L = require("./lib");
const { assert, sleep, BASE } = L;

// The cap under a filter, run by terminal-tabs-switcher.js on the project and
// shells of terminal-tabs.js: 31 stopped coders of the runner's own in its
// project make one group with more hits than the 30 a group shows. Every other
// name starts with the query, the rest carry it after a dash, so the ranking
// puts the first kind in front. The switcher and the + menu show the first 30
// the ranking gives, in its order, and a Show 1 more row below them, which the
// arrows reach like a row; Enter or a click on it shows the last one and marks
// it. A new query caps again. Without a query the group folds to three rows
// behind its own Show N more as before, no cap row stands. The coders start
// on the coder the form offers first and without a task, like the coders of
// the other checks, so no model is asked.

module.exports = async function terminalTabsCap({ run, page, project, ids }) {
  const prefix = "cp" + String(Date.now()).slice(-6);
  const names = Array.from({ length: 31 }, (_, i) => {
    const tag = "n" + String(i + 1).padStart(2, "0");
    return i % 2 === 0 ? `${prefix}-${tag}` : `${tag}-${prefix}`;
  });
  const coders = [];
  const shellUrl = `${BASE}/shells/${ids[0]}`;
  const post = (path, body) => page.evaluate(async ({ path, body }) => {
    const res = await fetch(path, {
      method: "POST",
      headers: { "X-CSRF-Token": document.querySelector('meta[name="csrf-token"]').content, Accept: "application/json" },
      body: new URLSearchParams(body),
    });
    return { status: res.status, text: await res.text() };
  }, { path, body });
  const toTerminal = async () => {
    await page.goto(shellUrl, { waitUntil: "domcontentloaded" });
    assert((await L.waitUpgraded(page, ["terminal-tabs"], 8000)).length === 0, "terminal-tabs not upgraded");
    await page.waitForSelector(".attach-terminal .xterm-screen canvas", { timeout: 15000 });
    await page.click(".attach-terminal");
    await sleep(300);
  };
  // Equal ranks keep the order the group stands in, the query first ones lead.
  const ranked = async () => {
    const order = await page.$$eval(`terminal-tabs .terminal-tabs-new-menu [data-tabs-resume-fold="${project}"] [data-resume-id]`, (els) => els.map((el) => el.dataset.resumeId));
    const own = order.filter((id) => coders.includes(id));
    const leads = (id) => coders.indexOf(id) % 2 === 0;
    return [...own.filter(leads), ...own.filter((id) => !leads(id))];
  };
  const listState = (root) => page.evaluate(({ root, project }) => {
    const shown = (node) => node.getClientRects().length > 0;
    const box = document.querySelector(root);
    const switcher = root === ".terminal-switcher-list";
    const items = switcher
      ? [...box.querySelectorAll(".terminal-switcher-item")].filter((n) => n.dataset.switcherGroup === project || n.dataset.switcherMore === `inactive\n${project}`)
      : [...box.querySelectorAll(`[data-project-name="${project}"] .dropdown-item`)];
    const kind = (n) => (n.dataset.switcherMore !== undefined || n.hasAttribute("data-tabs-resume-more") ? "more"
      : n.dataset.switcherToggle !== undefined || n.hasAttribute("data-tabs-resume-toggle") ? "fold" : "row");
    const key = (n) => n.dataset.switcherResume || n.dataset.resumeId || kind(n);
    const visible = items.filter(shown);
    const text = (k) => visible.filter((n) => kind(n) === k).map((n) => n.textContent.trim()).join();
    const marked = box.querySelector(".selected");
    return {
      rows: visible.filter((n) => kind(n) === "row").map(key),
      more: text("more"),
      fold: text("fold"),
      last: visible.length ? kind(visible[visible.length - 1]) : "",
      selected: marked ? key(marked) : "",
      others: (switcher ? [...box.querySelectorAll(".terminal-switcher-item")] : [...box.querySelectorAll(".dropdown-item")]).filter(shown).length - visible.length,
    };
  }, { root, project });

  try {
    await run("a group with more than 30 hits shows its 30 best in both lists, Show N more shows the rest, without a query it folds as before", async () => {
      const dir = await L.projectPath(page, project);
      const coder = await page.$eval('select[name="coder"]', (select) => select.value).catch(() => "");
      // A coder is resumable once its CLI wrote its session, so each one runs a
      // moment before it stops, at most four at a time.
      const running = [];
      const stopOldest = async () => {
        const { id, at } = running.shift();
        await sleep(Math.max(0, at + 3000 - Date.now()));
        assert((await post(`/coders/${id}/stop`, {})).status < 400, `the coder ${id} did not stop`);
      };
      for (const name of names) {
        if (running.length === 4) await stopOldest();
        const made = await post("/coders/new", { name, project: dir, coder });
        const id = JSON.parse(made.text || "{}").id;
        assert(made.status < 400 && id, `the coder ${name} was refused: ${made.status} ${made.text.slice(0, 200)}`);
        coders.push(id);
        running.push({ id, at: Date.now() });
      }
      while (running.length) await stopOldest();
      await toTerminal();
      for (const id of coders) await page.waitForSelector(`terminal-tabs .terminal-tabs-new-menu [data-resume-id="${id}"]`, { state: "attached", timeout: 15000 });
      const order = await ranked();
      assert(order.length === names.length, `the + menu lists ${order.length} of the ${names.length} stopped coders`);

      // The switcher.
      for (let attempt = 0; attempt < 3; attempt += 1) {
        await page.keyboard.press("Control");
        await sleep(120);
        await page.keyboard.press("Control");
        if (await page.waitForSelector(".terminal-switcher", { state: "visible", timeout: 2500 }).catch(() => null)) break;
        await sleep(600);
      }
      const sw = () => listState(".terminal-switcher-list");
      let st = await sw();
      assert(st.rows.length === 3 && /^Show \d+ more$/.test(st.fold) && !st.more, `the switcher's group is not folded as before: ${JSON.stringify(st)}`);
      await page.keyboard.type(prefix, { delay: 20 });
      st = await sw();
      assert(JSON.stringify(st.rows) === JSON.stringify(order.slice(0, 30)), `the switcher does not show the first 30 of the ranking: ${JSON.stringify(st)}`);
      assert(st.more === "Show 1 more" && st.last === "more" && !st.fold && st.others === 0, `the switcher's cap row is wrong: ${JSON.stringify(st)}`);
      await page.keyboard.press("ArrowUp");
      st = await sw();
      assert(st.selected === "more", `ArrowUp from the first row did not wrap to the cap row: ${JSON.stringify(st)}`);
      await page.keyboard.press("Enter");
      st = await sw();
      assert(JSON.stringify(st.rows) === JSON.stringify(order) && !st.more, `Enter on the cap row did not show the rest in order: ${JSON.stringify(st)}`);
      assert(st.selected === order[30], `the first row revealed is not marked: ${JSON.stringify(st)}`);
      await page.keyboard.press("Backspace");
      await page.keyboard.type(prefix.slice(-1));
      st = await sw();
      assert(st.rows.length === 30 && st.more === "Show 1 more", `a new query did not cap again: ${JSON.stringify(st)}`);
      await page.locator(".terminal-switcher-item[data-switcher-more]:visible").click();
      st = await sw();
      assert(JSON.stringify(st.rows) === JSON.stringify(order) && !st.more && st.selected === order[30], `a click on the cap row did not show the rest: ${JSON.stringify(st)}`);
      for (let i = 0; i < prefix.length; i += 1) await page.keyboard.press("Backspace");
      st = await sw();
      assert(st.rows.length === 3 && /^Show \d+ more$/.test(st.fold) && !st.more, `the emptied switcher filter did not fold the group again: ${JSON.stringify(st)}`);
      await page.keyboard.press("Escape");
      await page.waitForSelector(".terminal-switcher", { state: "detached", timeout: 4000 });

      // The + menu.
      const menu = () => listState("terminal-tabs .terminal-tabs-new-menu");
      await page.keyboard.press("Meta+t");
      await page.waitForSelector("terminal-tabs .terminal-tabs-new-menu.show", { state: "visible", timeout: 4000 });
      st = await menu();
      assert(st.rows.length === 3 && /^Show \d+ more$/.test(st.fold) && !st.more, `the + menu's group is not folded as before: ${JSON.stringify(st)}`);
      await page.keyboard.type(prefix, { delay: 20 });
      st = await menu();
      assert(JSON.stringify(st.rows) === JSON.stringify(order.slice(0, 30)), `the + menu does not show the first 30 of the ranking: ${JSON.stringify(st)}`);
      assert(st.more === "Show 1 more" && st.last === "more" && !st.fold && st.others === 0, `the + menu's cap row is wrong: ${JSON.stringify(st)}`);
      await page.keyboard.press("ArrowUp");
      st = await menu();
      assert(st.selected === "more", `ArrowUp from the first row did not wrap to the cap row: ${JSON.stringify(st)}`);
      await page.keyboard.press("Enter");
      await sleep(200);
      st = await menu();
      assert(JSON.stringify(st.rows) === JSON.stringify(order) && !st.more && st.selected === order[30], `Enter on the cap row did not show the rest: ${JSON.stringify(st)}`);
      assert(await page.$("terminal-tabs .terminal-tabs-new-menu.show"), "Enter on the cap row closed the menu");
      for (let i = 0; i < prefix.length; i += 1) await page.keyboard.press("Backspace");
      st = await menu();
      assert(st.rows.length === 3 && /^Show \d+ more$/.test(st.fold) && !st.more, `the emptied + menu filter did not fold the group again: ${JSON.stringify(st)}`);
      await page.keyboard.press("Escape");
      return `30 of ${order.length} shown, the rest behind the cap row in both lists`;
    });
  } finally {
    await page.keyboard.press("Escape").catch(() => {});
    for (const id of coders) await post(`/coders/${id}/delete`, {}).catch(() => {});
  }
};
