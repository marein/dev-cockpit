const L = require("./lib");
const { assert, sleep } = L;

// Not a runner of its own: events.js runs these before its cleanup, on an
// assistant of their own that they delete again. Each Watching tab lists the
// newest five closed jobs or spent triggers and counts the rest in one line,
// "and N older". A tap on it lists the rest in place as the same rows, icon,
// fold and actions, in both tabs, on the desktop and on a phone. The line is
// a text link as wide as its words, on a phone it takes the tap height. The rows stay through the lists' pull on the next
// assistant event, on both, through the page swapping itself around the aside
// and through an action of an older row.
// Gotchas:
// - a spent trigger is a cron trigger posted with `until=1s`, the reactor ends
//   it on its next tick, at most ten seconds later,
// - a closed job is a coder steered and released right away,
// - the page swaps itself only when the thread moved, so the swap check flips
//   `blocked` on the live element before it asks for the pull.

const SHOWN = 5;
const OLDER = 2;
const ALL = SHOWN + OLDER;

function readTab(page, tab) {
  return page.evaluate((tab) => {
    const pane = document.getElementById(`assistant-${tab}`);
    const kind = tab === "coders" ? "job" : "trigger";
    const action = tab === "coders" ? "[data-assistant-job-again]" : "[data-assistant-trigger-remove]";
    const seen = (el) => !!el && el.getClientRects().length > 0 && getComputedStyle(el).visibility !== "hidden";
    const rows = [...pane.querySelectorAll(`[data-assistant-${kind}]`)].filter(seen);
    const older = pane.querySelector("[data-assistant-older]");
    return {
      ids: rows.map((row) => row.getAttribute(`data-assistant-${kind}`)),
      whole: rows.filter((row) => [`[data-assistant-${kind}-state]`, `[data-assistant-${kind}-fold]`, action].every((sel) => seen(row.querySelector(sel)))).length,
      older: seen(older) ? older.textContent.trim() : "",
      olderHeight: seen(older) ? older.getBoundingClientRect().height : 0,
      olderSpan: seen(older) ? older.getBoundingClientRect().width / older.parentElement.getBoundingClientRect().width : 0,
    };
  }, tab);
}

function rowCount(page, tab, want) {
  const kind = tab === "coders" ? "job" : "trigger";
  return page.waitForFunction(([tab, kind, want]) => {
    const pane = document.getElementById(`assistant-${tab}`);
    return pane.querySelectorAll(`[data-assistant-${kind}]`).length === want && !pane.querySelector("[data-assistant-older]");
  }, [tab, kind, want], { timeout: 15000 });
}

function probe(page) {
  return page.evaluate(() => {
    for (const body of document.querySelectorAll("#assistant-aside [data-assistant-body]")) body.dataset.probe = "1";
  });
}

function swapped(page) {
  return page.waitForFunction(() => !document.querySelector("#assistant-aside [data-assistant-body][data-probe]"), null, { timeout: 15000 });
}

function unfolded(page, kind, id) {
  return page.evaluate(([kind, id]) => !!document.querySelector(`[data-assistant-${kind}="${id}"] [data-assistant-${kind}-details].show`), [kind, id]);
}

async function olderChecks({ page, run, mobilePage, helpers }) {
  const { openAssistant, ownAssistant, postTo, startCoder, waitFor, projectDir } = helpers;
  let helper = "";
  let standing = "";
  let oldestJob = "";
  let oldestTrigger = "";
  const coders = [];

  await run("and N older opens the closed jobs and the spent triggers in place, in both tabs", async () => {
    helper = await ownAssistant(page);
    for (let i = 0; i < ALL; i++) {
      const made = await startCoder(page, projectDir, `older-${i}`, "Write the file.");
      assert(made.status === 200, `create answered ${made.status}: ${JSON.stringify(made.body)}`);
      coders.push(made.body.id);
      const steered = await postTo(page, "/assistants/jobs", {
        form: "steer", assistant: helper, terminal: made.body.id, task: "Write the file", done_when: "WAKE_DONE: the file is there",
      });
      assert(steered.status === 200, `steer answered ${steered.status}: ${JSON.stringify(steered.body)}`);
      const released = await postTo(page, "/assistants/jobs", { form: "release", terminal: made.body.id });
      assert(released.status === 200, `release answered ${released.status}: ${JSON.stringify(released.body)}`);
      const trigger = await postTo(page, "/assistants/triggers", {
        form: "new", assistant: helper, event: "cron", spec: "0 3 * * *", task: `EVENT_NOTHING older ${i}`, batch: "0", until: "1s",
      });
      assert(trigger.status === 200, `the trigger answered ${trigger.status}: ${JSON.stringify(trigger.body)}`);
    }
    const spent = await waitFor(() => page.evaluate(async (id) => {
      const res = await fetch(`/assistants/triggers?assistant=${id}`, { headers: { Accept: "text/html" } });
      const holder = document.createElement("div");
      holder.innerHTML = await res.text();
      return holder.querySelector("[data-assistant-older]")?.textContent.trim() || "";
    }, helper), 30000);
    assert(spent === `and ${OLDER} older`, `the triggers did not run out: ${spent}`);

    await openAssistant(page, helper);
    const jobs = await readTab(page, "coders");
    assert(jobs.ids.length === SHOWN && jobs.older === `and ${OLDER} older`, `the coders tab is not capped: ${JSON.stringify(jobs)}`);
    assert(jobs.olderSpan < 0.5, `the older line spans ${jobs.olderSpan} of the list, not its words`);
    await page.click("#assistant-coders [data-assistant-older]");
    await rowCount(page, "coders", ALL);
    const allJobs = await readTab(page, "coders");
    assert(allJobs.ids.length === ALL && allJobs.whole === ALL && !allJobs.older, `the older coders are not whole rows: ${JSON.stringify(allJobs)}`);
    assert(jobs.ids.every((id) => allJobs.ids.includes(id)), "the recent coders left the list");
    oldestJob = allJobs.ids[ALL - 1];
    await page.click(`[data-assistant-job="${oldestJob}"] [data-assistant-job-fold]`);
    await page.waitForSelector(`[data-assistant-job="${oldestJob}"] [data-assistant-job-details].show`, { timeout: 8000 });

    await page.click("[data-assistant-triggers-tab]");
    await page.waitForSelector("#assistant-triggers [data-assistant-older]", { state: "visible", timeout: 8000 });
    const triggers = await readTab(page, "triggers");
    assert(triggers.ids.length === SHOWN && triggers.older === `and ${OLDER} older`, `the triggers tab is not capped: ${JSON.stringify(triggers)}`);
    assert(triggers.olderSpan < 0.5, `the older line spans ${triggers.olderSpan} of the list, not its words`);
    await page.click("#assistant-triggers [data-assistant-older]");
    await rowCount(page, "triggers", ALL);
    const allTriggers = await readTab(page, "triggers");
    assert(allTriggers.ids.length === ALL && allTriggers.whole === ALL && !allTriggers.older, `the older triggers are not whole rows: ${JSON.stringify(allTriggers)}`);
    oldestTrigger = allTriggers.ids[ALL - 1];
    await page.click(`[data-assistant-trigger="${oldestTrigger}"] [data-assistant-trigger-fold]`);
    await page.waitForSelector(`[data-assistant-trigger="${oldestTrigger}"] [data-assistant-trigger-details].show`, { timeout: 8000 });
    return `${SHOWN} of ${ALL} became ${ALL} in both tabs`;
  });

  await run("the older rows stay through the next pull, the page's own swap and an action of their own", async () => {
    assert(helper && oldestJob && oldestTrigger, "the check before built nothing");
    await probe(page);
    const made = await postTo(page, "/assistants/triggers", {
      form: "new", assistant: helper, event: "cron", spec: "0 4 * * *", task: "EVENT_NOTHING the standing one", batch: "0",
    });
    assert(made.status === 200, `the trigger answered ${made.status}: ${JSON.stringify(made.body)}`);
    standing = made.body.id;
    await page.waitForSelector(`#assistant-triggers [data-assistant-trigger="${standing}"]`, { timeout: 15000 });
    await swapped(page);
    const afterPull = await readTab(page, "triggers");
    assert(afterPull.ids.length === ALL + 1 && !afterPull.older, `the triggers snapped back on the pull: ${JSON.stringify(afterPull)}`);
    assert(await unfolded(page, "trigger", oldestTrigger), "the pull closed the unfolded older trigger");

    await page.evaluate(() => {
      const surface = document.querySelector("dc-assistant");
      surface.dataset.probe = "1";
      surface.toggleAttribute("blocked");
      return surface.syncFromServer();
    });
    await page.waitForFunction(() => {
      const surface = document.querySelector("dc-assistant");
      return surface && !surface.dataset.probe;
    }, null, { timeout: 15000 });

    await probe(page);
    await page.click(`[data-assistant-trigger="${oldestTrigger}"] [data-assistant-trigger-remove]`);
    await L.confirmSwal(page);
    await page.waitForFunction((id) => !document.querySelector(`[data-assistant-trigger="${id}"]`), oldestTrigger, { timeout: 15000 });
    await swapped(page);
    const triggers = await readTab(page, "triggers");
    assert(triggers.ids.length === ALL && !triggers.older, `the triggers snapped back after the swap: ${JSON.stringify(triggers)}`);
    await page.click("[data-assistant-coders-tab]");
    const jobs = await readTab(page, "coders");
    assert(jobs.ids.length === ALL && !jobs.older, `the coders snapped back after the swap: ${JSON.stringify(jobs)}`);
    assert(await unfolded(page, "job", oldestJob), "the swap closed the unfolded older coder");
    return "both tabs kept every row through a pull, the page's swap and a removal";
  });

  await run("on a phone and older opens on a tap in both tabs and stays through the next pull", async () => {
    assert(helper && standing, "the checks before built nothing");
    const mp = await mobilePage();
    await openAssistant(mp, helper);
    await mp.tap("[data-assistant-watching-button]");
    await mp.waitForSelector("#assistant-aside.show", { timeout: 8000 });
    await sleep(400);
    const jobs = await readTab(mp, "coders");
    assert(jobs.ids.length === SHOWN && jobs.older === `and ${OLDER} older`, `the phone's coders tab is not capped: ${JSON.stringify(jobs)}`);
    assert(jobs.olderHeight >= 44, `the older line is ${jobs.olderHeight}px high on the phone`);
    assert(jobs.olderSpan < 0.5, `the older line spans ${jobs.olderSpan} of the phone's list, not its words`);
    await mp.tap("#assistant-coders [data-assistant-older]");
    await rowCount(mp, "coders", ALL);

    await mp.tap("[data-assistant-triggers-tab]");
    await mp.waitForSelector("#assistant-triggers [data-assistant-older]", { state: "visible", timeout: 8000 });
    const triggers = await readTab(mp, "triggers");
    assert(triggers.ids.length === 1 + SHOWN && triggers.older === `and ${ALL - 1 - SHOWN} older`, `the phone's triggers tab is not capped: ${JSON.stringify(triggers)}`);
    await mp.tap("#assistant-triggers [data-assistant-older]");
    await rowCount(mp, "triggers", ALL);
    const wide = await mp.evaluate(() => {
      const aside = document.getElementById("assistant-aside");
      return aside.scrollWidth > aside.clientWidth + 1 || document.documentElement.scrollWidth > window.innerWidth + 1;
    });
    assert(!wide, "the phone's aside overflows sideways");

    await probe(mp);
    const made = await postTo(page, "/assistants/triggers", {
      form: "new", assistant: helper, event: "cron", spec: "0 5 * * *", task: "EVENT_NOTHING one more", batch: "0",
    });
    assert(made.status === 200, `the trigger answered ${made.status}: ${JSON.stringify(made.body)}`);
    await mp.waitForSelector(`#assistant-triggers [data-assistant-trigger="${made.body.id}"]`, { timeout: 15000 });
    await swapped(mp);
    const again = await readTab(mp, "triggers");
    assert(again.ids.length === ALL + 1 && !again.older, `the phone's triggers snapped back on the pull: ${JSON.stringify(again)}`);
    await mp.tap("[data-assistant-coders-tab]");
    const jobsAgain = await readTab(mp, "coders");
    assert(jobsAgain.ids.length === ALL && !jobsAgain.older, `the phone's coders snapped back on the pull: ${JSON.stringify(jobsAgain)}`);
    return `the line stands ${Math.round(jobs.olderHeight)}px high, both tabs kept every row`;
  });

  await run("older cleanup", async () => {
    for (const id of coders) await postTo(page, `/coders/${id}/delete`, {});
    if (helper) await postTo(page, `/assistants/${helper}`, { form: "delete" });
    return `${coders.length} coders and the assistant removed`;
  });
}

module.exports = { olderChecks };
