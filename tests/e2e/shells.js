const L = require("./lib");
const { assert, sleep, confirmSwal } = L;

// Shells: plain throwaway terminals, the safe target. Custom elements terminal-attach,
// terminal-input, terminal-setting-select, dc-inline-rename. The shared
// terminal interaction (typing, controls, copy, scroll) is in terminal.js; this
// covers what is shell specific. Routes: GET /shells/new, POST /shells/new,
// GET /shells/:id, POST /shells/:id/{delete,rename,input,resize}, GET .../stream.

L.runFeature("SHELLS", async ({ page, run }) => {
  const tag = `shell-${Date.now().toString(36)}`;
  const project = `zztc-${tag}`;
  let shellUrl = null;
  try {
    await L.createProject(page, project);

    await run("create shell -> attach page + dc-inline-rename upgraded", async () => {
      shellUrl = await L.createShell(page, project);
      assert(/\/shells\/(?!new)[^/]+$/.test(shellUrl), `bad url ${shellUrl}`);
      assert((await L.waitUpgraded(page, ["terminal-attach", "terminal-input", "dc-inline-rename"], 12000)).length === 0, "not upgraded");
    });

    await run("scroll-history is set on attach + input (shell history scroll)", async () => {
      const ok = await page.evaluate(() => document.getElementById("terminal")?.hasAttribute("scroll-history") && document.querySelector("terminal-input")?.hasAttribute("scroll-history"));
      assert(ok, "scroll-history attribute missing");
    });

    await run("inline rename (CSRF header path) persists across reload", async () => {
      const name = `renamed-${tag.slice(-5)}`;
      await page.click("[data-rename-label]");
      await page.waitForSelector("[data-rename-input]:not(.d-none)", { timeout: 4000 });
      await page.fill("[data-rename-input]", name);
      await page.keyboard.press("Enter"); await sleep(800);
      await page.reload({ waitUntil: "domcontentloaded" });
      assert((await page.textContent("[data-rename-label]")).trim() === name, "rename not persisted");
    });

    await run("delete shell (data-confirm) redirects + cleans up", async () => {
      await page.goto(shellUrl, { waitUntil: "domcontentloaded" });
      const delPath = new URL(shellUrl).pathname + "/delete";
      await page.click(`form[action="${delPath}"] button[type="submit"], form[action="${delPath}"] button`);
      await confirmSwal(page);
      await page.waitForURL((u) => !new RegExp(new URL(shellUrl).pathname + "$").test(u.toString()), { timeout: 10000 });
      shellUrl = null;
    });
  } finally {
    if (shellUrl) await L.deleteShell(page, shellUrl).catch(() => {});
    await L.deleteProject(page, project).catch(() => {});
  }
});
