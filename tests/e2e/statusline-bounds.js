const { assert, sleep } = require("./lib");

// Not a runner of its own: statusline.js runs this check on its throwaway
// instance, with its own open and rows helpers. Every count and length the
// save takes is bounded on the server, a hand built post past one is refused
// with the normal flash, short enough for the session cookie it rides on,
// never a 500, and the saved list stays as it was. The length of a part of the
// line is one of them, said at its entry before a save, and a Save tapped
// before that is said waits for it.
module.exports.boundChecks = async function boundChecks({ page, run, open, rows }) {
  await run("a hand built save past the bounds is refused with a short flash, never a 500", async () => {
    await open(page);
    const before = JSON.stringify(await rows(page));
    const boundMax = await page.locator('[name="threshold_at"]').first().getAttribute("maxlength");
    assert(boundMax === "32", `a bound field is held to ${boundMax} characters, want the 32 the save takes`);
    // The page's own form, posted by hand: one field replaced by what no
    // field of the page lets through, or rows appended past the count. pe.js
    // wraps fetch and follows the flash redirect itself, so the flash stands
    // in the page that answers, and a 500 comes back as it is.
    const post = (field, value, extraRows = 0) => page.evaluate(async ([field, value, extraRows]) => {
      const form = document.querySelector("dc-claude-statusline form");
      const body = new URLSearchParams();
      let replaced = false;
      for (const [key, sent] of new FormData(form)) {
        body.append(key, key === field && !replaced ? value : sent);
        if (key === field) replaced = true;
      }
      for (let i = 0; i < extraRows; i++) body.append("entry_kind", "break");
      const res = await fetch(form.action, { method: "POST", body });
      const answered = new DOMParser().parseFromString(await res.text(), "text/html");
      const flash = answered.querySelector("[data-statusline-flash] .alert");
      return { status: res.status, redirected: res.redirected, flash: flash ? flash.textContent.replace(/\s+/g, " ").trim() : null };
    }, [field, value, extraRows]);
    const cases = [
      { what: "a bound of 2000 characters", field: "threshold_at", value: "x".repeat(2000), says: /At most 32 characters per bound\./ },
      { what: "a bound that is no number", field: "threshold_at", value: "twelve", says: /Bound "twelve" is no number\./ },
      { what: "a label of 65 characters", field: "entry_label", value: "l".repeat(65), says: /At most 64 characters per label\./ },
      { what: "a command of 1025 characters", field: "entry_text", value: "c".repeat(1025), says: /At most 1024 characters per text\./ },
      { what: "17 bounds on one entry", field: "entry_thresholds", value: "17", says: /At most 16 bounds per entry\./ },
      { what: "101 entries", field: "", value: "", extra: 101, says: /At most 100 entries\./ },
    ];
    for (const c of cases) {
      const answer = await post(c.field, c.value, c.extra || 0);
      assert(answer.status === 200 && answer.redirected, `${c.what} answered ${answer.status}, want the redirect back with a flash`);
      assert(answer.flash !== null, `${c.what} left no flash`);
      assert(c.says.test(answer.flash), `${c.what} says ${JSON.stringify(answer.flash)}`);
      assert(answer.flash.length < 200, `${c.what} says ${answer.flash.length} characters`);
      await open(page);
      assert(JSON.stringify(await rows(page)) === before, `${c.what} changed the saved list`);
    }
    return `${cases.length} refusals, each a short flash`;
  });

  // A command typed past the length of a part of the line, then switched to a
  // separator or the free text and folded: the browser would hold the field to
  // its new maxlength and block the save without a word, so the length is the
  // save's rule and the entry says it while folded.
  await run("a separator and a free text past 24 characters are refused at their folded entry", async () => {
    await open(page);
    await page.click("[data-entry-add]");
    const added = page.locator("[data-entry-rows] [data-entry-row]").last();
    const text = added.locator('[name="entry_text"]');
    const error = added.locator("[data-entry-error] [data-statusline-error]");
    const saveButton = page.locator("[data-statusline-save]");
    await added.locator("[data-entry-value]").selectOption("command");
    await text.fill("c".repeat(30));
    await added.locator("[data-entry-kind]").selectOption("separator");
    assert(await text.getAttribute("maxlength") === "24", `a separator is held to ${await text.getAttribute("maxlength")} characters`);
    await error.waitFor({ state: "visible", timeout: 3000 });
    assert(/At most 24 characters per text\./.test(await error.textContent()), `the separator says ${await error.textContent()}`);
    await added.locator("[data-entry-toggle]").click();
    assert(!(await added.locator("[data-entry-body]").isVisible()), "the entry did not fold");
    assert(await error.isVisible(), "a folded separator hides its refusal");
    assert(await saveButton.isDisabled(), "Save takes a separator past 24 characters");

    await added.locator("[data-entry-toggle]").click();
    await added.locator("[data-entry-kind]").selectOption("value");
    await error.waitFor({ state: "detached", timeout: 3000 });
    await added.locator("[data-entry-value]").selectOption("text");
    await error.waitFor({ state: "visible", timeout: 3000 });
    await added.locator("[data-entry-toggle]").click();
    assert(await error.isVisible(), "a folded free text hides its refusal");
    assert(await saveButton.isDisabled(), "Save takes a free text past 24 characters");
    return "separator and free text, folded, Save off";
  });

  // Save stays on until the check of an edit answers. A tap in that window
  // waits for the answer: one that carries an error posts nothing and keeps
  // the edit, a clean one saves. The route holds every check until released,
  // so the tap surely lands while one runs.
  await run("a Save during a running check posts only when the answer carries no error", async () => {
    await open(page);
    const before = JSON.stringify(await rows(page));
    const context = page.locator("[data-entry-rows] [data-entry-row]", { has: page.locator('[data-entry-value] option[value="context"]:checked') }).first();
    const bound = context.locator('[name="threshold_at"]').first();
    const label = context.locator('[name="entry_label"]');
    const saveButton = page.locator("[data-statusline-save]");
    const held = [];
    const holding = async (n) => {
      const deadline = Date.now() + 3000;
      while (held.length < n && Date.now() < deadline) await sleep(20);
      assert(held.length === n, `${held.length} checks went out, want ${n}`);
    };
    const saves = [];
    const count = (request) => {
      if (request.method() === "POST" && new URL(request.url()).pathname.endsWith("/statusline")) saves.push(request.url());
    };
    await page.route("**/statusline/state", (route) => new Promise((done) => {
      let waiting = true;
      held.push(() => {
        if (waiting) done(route.continue().catch(() => {}));
        waiting = false;
      });
    }));
    page.on("request", count);
    try {
      await bound.fill("x");
      await holding(1);
      assert(!(await saveButton.isDisabled()), "Save is off before the answer, the tap would prove nothing");
      await saveButton.click();
      await sleep(300);
      assert(saves.length === 0, "the tap posted while the check ran");
      held[0]();
      await context.locator("[data-entry-error] [data-statusline-error]").waitFor({ state: "visible", timeout: 3000 });
      await sleep(300);
      assert(saves.length === 0, `the tap posted ${saves.length} times next to a refusal`);
      assert(await bound.inputValue() === "x", "the edit is gone");
      assert(await saveButton.isDisabled(), "Save stays on next to the refusal");

      await bound.fill("0");
      await holding(2);
      held[1]();
      await page.waitForFunction(() => !document.querySelector("[data-statusline-save]").disabled, null, { timeout: 3000 });
      await label.fill("cx");
      await holding(3);
      await saveButton.click();
      await sleep(300);
      assert(saves.length === 0, "the tap posted before the answer");
      held[2]();
      await page.locator("[data-statusline-flash] .alert-success").waitFor({ state: "visible", timeout: 8000 });
      assert(saves.length === 1, `the tap posted ${saves.length} times after a clean answer`);
    } finally {
      page.off("request", count);
      await page.unroute("**/statusline/state");
      held.forEach((release) => release());
    }
    await open(page);
    assert((await rows(page)).find((row) => row.value === "context")?.label === "cx", "the held save did not store the edit");
    await label.fill("c");
    await saveButton.click();
    await page.locator("[data-statusline-flash] .alert-success").waitFor({ state: "visible", timeout: 8000 });
    await open(page);
    assert(JSON.stringify(await rows(page)) === before, "the list was not put back");
    return "refused answer posts nothing, clean answer saves";
  });

  // A check that fails, an ended session or a lost connection, knows nothing,
  // so the held Save posts nothing, a toast names the reason and Save stays
  // on. Only the next tap asks again, one check per tap: asking on its own
  // once sent thousands of checks a second. The 403 is the server's own, a
  // check carrying a token the session does not hold, the way a logout in
  // another tab leaves the page. The first pass saves "cx", the second puts
  // "c" back.
  await run("a Save during a failing check posts nothing and says why, the next tap checks anew", async () => {
    await open(page);
    const before = JSON.stringify(await rows(page));
    const failures = [
      ["403", (route) => route.continue({ headers: { ...route.request().headers(), "x-csrf-token": "stale" } }),
        "Your session expired or the form is no longer valid. Reload the page and try again."],
      ["no connection", (route) => route.abort("connectionfailed"), "The connection failed or was cut off."],
    ];
    const toasts = page.locator(".dc-toast", { hasText: "The check failed, nothing was saved." });
    const said = [];
    for (const [i, [name, fail, reason]] of failures.entries()) {
      await open(page);
      const context = page.locator("[data-entry-rows] [data-entry-row]", { has: page.locator('[data-entry-value] option[value="context"]:checked') }).first();
      const label = context.locator('[name="entry_label"]');
      const saveButton = page.locator("[data-statusline-save]");
      const want = i === 0 ? "cx" : "c";
      let asked = 0;
      const saves = [];
      const count = (request) => {
        const path = new URL(request.url()).pathname;
        if (path.endsWith("/statusline/state")) asked++;
        else if (request.method() === "POST" && path.endsWith("/statusline")) saves.push(request.url());
      };
      const askedUpTo = async (n) => {
        const deadline = Date.now() + 3000;
        while (asked < n && Date.now() < deadline) await sleep(20);
        assert(asked === n, `${name}: ${asked} checks went out, want ${n}`);
      };
      const failedOnce = async (tap) => {
        await toasts.first().waitFor({ state: "visible", timeout: 3000 });
        const detail = (await toasts.first().locator(".dc-toast-detail").textContent()).trim();
        assert(detail === reason, `${name}: the toast of tap ${tap} says ${JSON.stringify(detail)}, want ${JSON.stringify(reason)}`);
        await sleep(500);
        assert(await toasts.count() === 1, `${name}: tap ${tap} left ${await toasts.count()} toasts`);
        assert(asked === tap, `${name}: tap ${tap} asked ${asked} checks in all, a failed check asked again on its own`);
        assert(saves.length === 0, `${name}: tap ${tap} posted ${saves.length} times after a failed check`);
        assert(!(await saveButton.isDisabled()), `${name}: Save is off after the failed check of tap ${tap}`);
        assert(await label.inputValue() === want, `${name}: the edit is gone after tap ${tap}`);
        await toasts.first().locator(".btn-close").click();
        await toasts.first().waitFor({ state: "detached", timeout: 3000 });
      };
      let release;
      const gate = new Promise((done) => { release = done; });
      await page.route("**/statusline/state", (route) => gate.then(() => fail(route)).catch(() => {}));
      page.on("request", count);
      try {
        await label.fill(want);
        await askedUpTo(1);
        assert(!(await saveButton.isDisabled()), `${name}: Save is off before the answer, the tap would prove nothing`);
        await saveButton.click();
        await sleep(300);
        assert(saves.length === 0, `${name}: the tap posted while the check ran`);
        release();
        await failedOnce(1);

        await saveButton.click();
        await askedUpTo(2);
        await failedOnce(2);

        await page.unroute("**/statusline/state");
        await saveButton.click();
        await page.locator("[data-statusline-flash] .alert-success").waitFor({ state: "visible", timeout: 8000 });
        await sleep(300);
        assert(asked === 3, `${name}: the tap after the failures asked ${asked - 2} checks`);
        assert(saves.length === 1, `${name}: the tap after the failures posted ${saves.length} times`);
        said.push(`${name} ${asked}+${saves.length}`);
      } finally {
        page.off("request", count);
        release();
        await page.unroute("**/statusline/state");
      }
      await open(page);
      assert((await rows(page)).find((row) => row.value === "context")?.label === want, `${name}: the third tap did not store ${want}`);
    }
    assert(JSON.stringify(await rows(page)) === before, "the list was not put back");
    return `checks+posts over three taps: ${said.join(", ")}`;
  });
};
