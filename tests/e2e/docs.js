const L = require("./lib");
const { assert, BASE } = L;

// Documentation: the app-wide Docs navigation entry and the /docs page. The
// content (navigation, terminal and editor controls, gestures inline with
// their topic, notifications, push delivery, settings tips) comes from Go
// (internal/web/render/docs.go); the page renders it as one Bootstrap
// accordion, a panel per topic with title/description rows, first topic open.
// Built from Tabler components only, no page specific CSS.

const TOPICS = ["navigation", "terminals", "editor", "docker", "notifications", "push", "settings"];

const docsText = (page) => page.locator("#docs-accordion").evaluate((el) => el.textContent);

// A throwaway instance built from a dev tree offers an update on first visit;
// the modal would swallow the panel click. Deny it, never confirm.
async function dismissUpdate(page) {
  const cancel = page.locator(".swal2-cancel");
  // The check runs against the network, so the modal can still be on its way.
  try {
    await cancel.waitFor({ state: "visible", timeout: 2000 });
  } catch {
    return;
  }
  await cancel.click();
  await page.waitForSelector(".swal2-container", { state: "detached", timeout: 5000 });
}

L.runFeature("DOCS", async ({ page, run }) => {
  await run("the docs page lists every topic as a collapsible panel", async () => {
    await page.goto(`${BASE}/docs`, { waitUntil: "domcontentloaded" });
    assert(await page.locator('.navbar-nav a[href="/docs"].active').count() === 1, "Docs nav item is not active");
    assert(await page.locator("h2", { hasText: "Documentation" }).count() === 1, "documentation heading missing");
    for (const topic of TOPICS) {
      assert(await page.locator(`.accordion-item#${topic}`).count() === 1, `missing topic ${topic}`);
    }
    assert(await page.locator("#docs-panel-navigation.show").count() === 1, "first topic is not open");
    assert(await page.locator("#docs-panel-editor.show").count() === 0, "a later topic starts open");
    await dismissUpdate(page);
    await page.click('button[data-bs-target="#docs-panel-editor"]');
    await page.waitForSelector("#docs-panel-editor.show", { timeout: 4000 });
    assert(await page.locator("#docs-panel-editor").getByText("Quick open").count() >= 1, "opened topic has no rows");
    // Collapsed panels hold no rendered text, so the copy checks read
    // textContent, which covers the folded topics too.
    const text = await docsText(page);
    assert(!/\bsession\b/i.test(text), "user-facing terminology includes session");
  });

  await run("docs describe the global switcher, shell bell, and push settings", async () => {
    await page.goto(`${BASE}/docs`, { waitUntil: "domcontentloaded" });
    const text = await docsText(page);
    assert(/Ctrl\s*\+\s*Ctrl/.test(text), "double Ctrl shortcut missing");
    assert(text.includes("printf '\\a'"), "shell bell example missing");
    assert(/Web push/.test(text) && /Webhooks/.test(text), "push channels missing");
    assert(/Restore terminals at startup/.test(text) && /Separate history per shell/.test(text) && /Back up/.test(text), "settings tips missing");
    assert(await page.locator('#push a[href="/settings/notifications#settings-webpush"]').count() === 1, "push settings link missing");
    // The main nav carries /settings/general too, so the topic link is scoped
    // to the settings topic.
    assert(await page.locator('#settings a[href="/settings/general"]').count() === 1, "general settings link missing");
  });

  await run("named controls carry their own icon", async () => {
    await page.goto(`${BASE}/docs`, { waitUntil: "domcontentloaded" });
    for (const [topic, icon] of [["terminals", "ti-upload"], ["terminals", "ti-refresh"], ["editor", "ti-eye"], ["navigation", "ti-layout-grid"]]) {
      assert(await page.locator(`#${topic} .col-md-8 i.${icon}`).count() >= 1, `${icon} missing in ${topic}`);
    }
  });
});
