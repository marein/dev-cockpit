const L = require("./lib");
const { assert, BASE, sleep } = L;

// Not a runner of its own: assistant-share.js runs this check over its link,
// chromium. The Projects tab opens first for a guest that never looked at the
// aside: every card of the assistant, read only, a project leading into the
// share only where the link grants it, a stopped coder's row resuming
// nothing, the rows following a job live, the count hidden while nothing
// needs the user, the coder's icon the owner's without the owner's news,
// another assistant's tab and a watch without the link's token refused.

module.exports.projectsCheck = async function projectsCheck({ run, page, guest, fx, PROJECT, OFF, TAG, newCoder, steer, postJSON, postAs, hook, notFound, fetchRaw }) {
  await run("the guest reads the Projects tab read only, a project leading into the share only where the link grants it", async () => {
    const gp = guest.page;
    const pane = "#assistant-projects";
    const onLink = await newCoder(page, fx[PROJECT], `onlink-${TAG}`);
    try {
      await steer(page, fx.a, onLink);
      // The guest left this aside on Triggers before.
      await L.forgetPlaces(gp);
      await gp.goto(`${BASE}${fx.base}/assistants/${fx.a}`, { waitUntil: "domcontentloaded" });
      await gp.waitForSelector(`${pane} [data-assistant-project="${PROJECT}"]`, { state: "visible", timeout: 10000 });
      assert(await gp.getAttribute("#assistant-projects-tab", "aria-selected") === "true", "Projects is not the guest's open tab");
      const names = await gp.$$eval(`${pane} [data-assistant-project]`, (els) => els.map((el) => el.dataset.assistantProject));
      assert(names.join(",") === `${PROJECT},${OFF}`, `the guest's cards read ${names}`);
      assert(await gp.getAttribute(`${pane} [data-assistant-project="${PROJECT}"] [data-assistant-project-link]`, "href") === `${fx.base}/projects/${PROJECT}/editor`,
        "the project on the link does not lead into the share");
      assert(!(await gp.$(`${pane} [data-assistant-project="${OFF}"] [data-assistant-project-link]`)), "the project off the link leads somewhere");
      const hrefs = await gp.$$eval(`${pane} a[href]`, (els) => els.map((el) => el.getAttribute("href")));
      assert(hrefs.every((h) => h.startsWith(`${fx.base}/`)), `the guest's cards link ${hrefs}`);
      assert(!(await gp.$(`${pane} form, ${pane} input, ${pane} button:not([data-assistant-project-older]):not([data-assistant-older]):not([data-assistant-project-open])`)), "the guest's Projects tab offers a change");
      const stoppedRow = await gp.evaluate(([sel, id]) => {
        const row = document.querySelector(`${sel} [data-assistant-project-job="${id}"]`);
        return row && { tag: row.tagName, href: row.getAttribute("href") || "", resume: row.matches("form") || !!row.querySelector("[data-assistant-job-resume]") };
      }, [pane, fx.stopped]);
      assert(stoppedRow && stoppedRow.tag === "DIV" && !stoppedRow.href && !stoppedRow.resume, `the guest's stopped coder row resumes or leads somewhere: ${JSON.stringify(stoppedRow)}`);
      const count = () => gp.evaluate(() => {
        const el = document.querySelector("[data-assistant-projects-tab] dc-steer-badge");
        return el && el.getClientRects().length ? Number(el.textContent) : 0;
      });
      assert(await count() === 0, `the guest's count reads ${await count()} while nothing needs the user`);
      const marker = await gp.evaluate(() => { window.dcStay = Math.random(); return window.dcStay; });
      await postJSON(page, "/assistants/jobs", { form: "release", terminal: onLink });
      await gp.waitForFunction(([sel, id]) => document.querySelector(`${sel} [data-assistant-project-job="${id}"] [data-assistant-job-state]`)?.dataset.assistantJobState === "stopped",
        [pane, onLink], { timeout: 15000 });
      assert(await count() === 0, `the guest's count reads ${await count()} after the release`);
      assert(await gp.evaluate(() => window.dcStay) === marker, "the guest's page reloaded");
      const mark = (p, id) => p.evaluate(([sel, id]) => {
        const el = document.querySelector(`${sel} [data-assistant-project-job="${id}"] [data-assistant-job-state]`);
        const dot = el?.querySelector(".dc-news-dot");
        return el && { news: el.classList.contains("news"), dot: !!dot && getComputedStyle(dot).display !== "none" && dot.getClientRects().length > 0,
          running: el.classList.contains("running"), label: el.getAttribute("aria-label"), paint: getComputedStyle(el).backgroundColor };
      }, [pane, id]);
      hook(onLink, "Stop");
      await page.goto(`${BASE}/assistants/${fx.a}`, { waitUntil: "domcontentloaded" });
      await page.waitForFunction(([sel, id]) => document.querySelector(`${sel} [data-assistant-project-job="${id}"] [data-assistant-job-state].news`), [pane, onLink], { timeout: 15000 });
      const owners = await mark(page, onLink);
      assert(owners.dot && owners.running && owners.label === "Released, running", `the owner's row reads ${JSON.stringify(owners)}`);
      await gp.goto(`${BASE}${fx.base}/assistants/${fx.a}`, { waitUntil: "domcontentloaded" });
      await gp.waitForSelector(`${pane} [data-assistant-project-job="${onLink}"]`, { state: "visible", timeout: 10000 });
      await sleep(1500);
      const guests = await mark(gp, onLink);
      assert(!guests.news && !guests.dot && guests.running && guests.label === owners.label && guests.paint === owners.paint, `the guest's row reads ${JSON.stringify(guests)}`);
      assert(!(await gp.$(`[data-assistant-job="${onLink}"] .news, [data-assistant-job="${onLink}"].news`)), "the guest's Coders tab wears the owner's news");
      await notFound(`${fx.link}/assistants/projects?assistant=${fx.b}`, "the other assistant's projects");
      const watch = await fetchRaw(`${fx.link}/assistants/projects/watch?assistant=${fx.a}`, { method: "POST" });
      assert(watch.status === 403, `a watch without the link's token answered ${watch.status}`);
    } finally {
      await postAs(page, `/coders/${onLink}/delete`, {}).catch(() => {});
    }
  });
};
