const L = require("./lib");
const { assert, submitBtn, confirmSwal, BASE } = L;

// Skills: same shape as agents. Fields skill_id, skill_description,
// skill_instructions; edit carries hidden original_skill_id. Routes:
// GET /skills(/new|/:id/edit), POST /skills, POST /skills/:id, POST /skills/:id/delete.

L.runFeature("SKILLS", async ({ page, run }) => {
  const tag = `sk-${Date.now().toString(36)}`;
  const id = `tcskill-${tag.slice(-5)}`;
  const id2 = `${id}-x`;
  await run("create -> appears -> edit moves id -> delete", async () => {
    await page.goto(`${BASE}/skills/new`, { waitUntil: "domcontentloaded" });
    await page.fill('input[name="skill_id"]', id); await page.fill('input[name="skill_description"]', "throwaway"); await page.fill('textarea[name="skill_instructions"]', "test only");
    await Promise.all([page.waitForURL(/\/skills$/, { timeout: 10000 }), submitBtn(page, 'input[name="skill_id"]').click()]);
    assert(await page.evaluate((i) => document.body.innerHTML.includes(i), id), "skill not listed after create");
    await page.goto(`${BASE}/skills/${encodeURIComponent(id)}/edit`, { waitUntil: "domcontentloaded" });
    assert(await page.$('input[name="original_skill_id"]'), "no original_skill_id");
    assert((await page.inputValue('input[name="skill_id"]')) === id, "edit not prefilled");
    await page.fill('input[name="skill_id"]', id2);
    await Promise.all([page.waitForURL(/\/skills$/, { timeout: 10000 }), submitBtn(page, 'input[name="skill_id"]').click()]);
    assert(await page.evaluate((ids) => document.body.innerHTML.includes(ids.n2) && !document.querySelector(`a[href*="${ids.n1}/edit"]`), { n1: id, n2: id2 }), "id move did not take");
    const del = await page.$(`form[action="/skills/${id2}/delete"], form[action="/skills/${encodeURIComponent(id2)}/delete"]`);
    assert(del, "no delete form");
    await (await del.$("button, input[type=submit]")).click(); await confirmSwal(page);
    await page.waitForFunction((i) => !document.querySelector(`form[action="/skills/${i}/delete"]`), id2, { timeout: 8000 });
  });
});
