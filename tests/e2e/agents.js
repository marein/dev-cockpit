const L = require("./lib");
const { assert, sleep, submitBtn, confirmSwal, BASE } = L;

// Agents: agent definitions list, create, edit (identifier move), delete. Fields
// agent_id, agent_description, agent_instructions; edit carries hidden
// original_agent_id. Routes: GET /agents(/new|/:id/edit), POST /agents,
// POST /agents/:id, POST /agents/:id/delete. Agents feed the session agent select.

L.runFeature("AGENTS", async ({ page, run }) => {
  const tag = `ag-${Date.now().toString(36)}`;
  const id = `tcagent-${tag.slice(-5)}`;
  const id2 = `${id}-x`;
  await run("create -> appears -> edit moves id -> delete", async () => {
    await page.goto(`${BASE}/agents/new`, { waitUntil: "domcontentloaded" });
    await page.fill('input[name="agent_id"]', id); await page.fill('input[name="agent_description"]', "throwaway"); await page.fill('textarea[name="agent_instructions"]', "test only");
    await Promise.all([page.waitForURL(/\/agents$/, { timeout: 10000 }), submitBtn(page, 'input[name="agent_id"]').click()]);
    assert(await page.evaluate((i) => document.body.innerHTML.includes(i), id), "agent not listed after create");
    await page.goto(`${BASE}/agents/${encodeURIComponent(id)}/edit`, { waitUntil: "domcontentloaded" });
    assert(await page.$('input[name="original_agent_id"]'), "no original_agent_id");
    assert((await page.inputValue('input[name="agent_id"]')) === id, "edit not prefilled");
    await page.fill('input[name="agent_id"]', id2);
    await Promise.all([page.waitForURL(/\/agents$/, { timeout: 10000 }), submitBtn(page, 'input[name="agent_id"]').click()]);
    assert(await page.evaluate((ids) => document.body.innerHTML.includes(ids.n2) && !document.querySelector(`a[href*="${ids.n1}/edit"]`), { n1: id, n2: id2 }), "id move did not take");
    const del = await page.$(`form[action="/agents/${id2}/delete"], form[action="/agents/${encodeURIComponent(id2)}/delete"]`);
    assert(del, "no delete form");
    await (await del.$("button, input[type=submit]")).click(); await confirmSwal(page);
    await page.waitForFunction((i) => !document.querySelector(`form[action="/agents/${i}/delete"]`), id2, { timeout: 8000 });
  });

  await run("validation: identifier and description required", async () => {
    await page.goto(`${BASE}/agents/new`, { waitUntil: "domcontentloaded" });
    const req = await page.evaluate(() => ({ id: document.querySelector('input[name="agent_id"]').required, desc: document.querySelector('input[name="agent_description"]').required, ml: document.querySelector('input[name="agent_id"]').maxLength }));
    assert(req.id && req.desc && req.ml === 120, `validation attrs wrong: ${JSON.stringify(req)}`);
  });
});
