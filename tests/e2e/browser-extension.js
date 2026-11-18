const fs = require("fs");
const L = require("./lib");
const X = require("./browser-extension-fixture");
const { menuChecks, menuState, restingPointerChecks } = require("./browser-extension-menu");
const { assert, BASE, sleep } = L;

// The browser extension (internal/browserextension/addon), loaded unpacked
// into a Chromium of its own. The button is pressed through the DevTools
// protocol's Extensions.triggerAction, the action a click runs, the side panel
// gets real input events over the protocol. No runner answers the browser's
// access prompt, so this copy lists every page in host_permissions, a second
// one only the cockpit's address; internal/browserextension's tests pin that
// the zip asks for none. Instance with tests/e2e/fakes first on PATH and a
// scratch HOME, sites A and B of browser-extension-fixture.js run in the
// runner. EXT_SHOTS: screenshots.

process.env.NODE_TLS_REJECT_UNAUTHORIZED = "0";
const { TAG, PROJECT, ORIGIN, QUIET, SHOTS, NOTE, SITE_A, SITE_B } = X;
const TOKEN_NAME = `Desk ${TAG}`;

async function main() {
  const results = [];
  const bag = { consoleErrors: [], pageErrors: [], cdnNoise: [] };
  const run = async (name, fn) => {
    if (!(process.env.CHECK ? process.env.CHECK.split(",").some((t) => name.toLowerCase().includes(t.trim().toLowerCase())) : true)) return;
    const t0 = Date.now();
    try { const d = await fn(); results.push({ name: `[chromium] ${name}`, status: "PASS", detail: d || "", ms: Date.now() - t0 }); }
    catch (e) { results.push({ name: `[chromium] ${name}`, status: "FAIL", detail: e.message.split("\n")[0], ms: Date.now() - t0 }); }
  };
  const sites = await X.start(ORIGIN);
  const browser = await L.launch("chromium");
  const ctx = await L.newDesktop(browser, "chromium");
  const page = await ctx.newPage();
  L.wirePage(page, bag);
  const s = { made: [], files: null, ext: null, panel: null };
  try {
    await L.login(page);
    await L.dismissUpdate(page);
    await L.createProject(page, PROJECT);
    s.coder = new URL(await L.createSession(page, PROJECT, `extc-${TAG}`, "claude")).pathname.split("/").pop();
    const made = await X.postAs(page, "/assistants/new", { coder: "claude" });
    s.assistant = JSON.parse(made.text).id;
    s.made.push(s.assistant);
    s.title = `Ext ${TAG}`;
    await X.postAs(page, `/assistants/${s.assistant}`, { form: "rename", title: s.title });
    const second = JSON.parse((await X.postAs(page, "/assistants/new", { coder: "claude" })).text).id;
    s.made.push(second);
    s.second = `Taxi ext ${TAG}`;
    await X.postAs(page, `/assistants/${second}`, { form: "rename", title: s.second });
    s.token = await X.makeToken(page, TOKEN_NAME);
    s.spare = await X.makeToken(page, `Spare ${TAG}`);
    const targets = (await X.request("GET", "/api/browser-extension/targets", { token: s.token })).json;
    s.coderName = targets.coders.find((one) => one.id === s.coder).name;
    s.newWith = targets.assistantCoders;

    s.zips = { chrome: await X.download(page, "chrome"), firefox: await X.download(page, "firefox") };
    s.files = X.unzip(s.zips.chrome.bytes);
    s.version = await page.textContent("#settings-browser-extension [data-browser-extension-version]");

    await run("Settings › Browser offers the extension for Chrome and Firefox as a zip of the extension's version", async () => {
      const section = await page.evaluate(() => {
        const el = document.querySelector("#settings-browser-extension");
        const box = el.getBoundingClientRect();
        return { text: el.innerText, inside: box.right <= innerWidth };
      });
      assert(section.inside && /Picks elements, lists the console and draws on any page/.test(section.text) && /For Chrome/.test(section.text) && /For Firefox/.test(section.text), JSON.stringify(section));
      for (const [name, got] of Object.entries(s.zips)) {
        const files = X.unzip(got.bytes);
        const manifest = JSON.parse(files["manifest.json"]);
        assert(/^\d+(\.\d+){0,3}$/.test(s.version) && got.name === `dev-cockpit-browser-extension-${name}-${s.version}.zip`, `the zip is named ${got.name}`);
        assert(manifest.version === s.version && !manifest.version_name && manifest.manifest_version === 3 && /^export const API = \d+;$/m.test(String(files["version.js"])) && files["pick.js"] && files["browser-context.js"] && files["filter.js"], `${name}: ${manifest.version} ${JSON.stringify(Object.keys(files))}`);
        assert(!manifest.host_permissions && JSON.stringify(manifest.optional_host_permissions) === JSON.stringify(["<all_urls>"]) && !manifest.content_scripts && !manifest.web_accessible_resources && !manifest.externally_connectable, `${name}: ${files["manifest.json"]}`);
        if (process.env.EXT_ZIPS) fs.writeFileSync(`${process.env.EXT_ZIPS}/${got.name}`, got.bytes);
      }
    });

    s.dir = X.unpack(s.files, X.EVERY_PAGE);
    s.ext = await X.launchWith(s.dir);

    await run("the setup shows the extension's version, and says in one sentence when nothing answers, the address redirects or the token is wrong", async () => {
      const setup = await X.setupPage(s.ext);
      const head = await setup.textContent("[data-version]");
      assert(head === `Version ${s.version}`, `the setup says ${head}`);
      await setup.close();
      const quiet = await X.setUp(s.ext, QUIET, s.token);
      assert(quiet.error && quiet.said === `Nothing answers at ${QUIET}. Check the address and that the cockpit runs.`, `no answer: ${quiet.said}`);
      await quiet.page.close();
      const probe = await X.makeToken(page, `Probe ${TAG}`);
      const moved = await X.setUp(s.ext, SITE_B, probe);
      assert(moved.error && moved.said === `${SITE_B} redirects to another address. Enter the address the cockpit's pages open on.`, `redirect: ${moved.said}`);
      await moved.page.close();
      const used = await X.tokenUsed(page, `Probe ${TAG}`);
      assert(X.seen.redirected > 0 && X.seen.bearer > 0 && used.endsWith("never used"), `the cockpit got the token through the redirect: ${used}`);
      const wrong = await X.setUp(s.ext, ORIGIN, "x".repeat(43));
      assert(wrong.error && /refuses the token/.test(wrong.said), `wrong token: ${wrong.said}`);
      await wrong.page.close();
    });

    await run("the setup with the cockpit's address and the token says how many assistants and running coders it lists, and keeps the token out of the page", async () => {
      const ok = await X.setUp(s.ext, ORIGIN, s.token);
      assert(!ok.error && /^Connected\. The cockpit lists \d+ assistants? and \d+ running coders?\.$/.test(ok.said), `the setup says ${ok.said}`);
      const form = await ok.page.evaluate(() => ({ address: document.querySelector("[data-address]").value, token: document.querySelector("[data-token]").value, forget: !document.querySelector("[data-forget]").hidden, every: document.querySelector("[data-every-page]").checked && document.querySelector("[data-every-page]").disabled, html: document.documentElement.outerHTML }));
      assert(form.address === ORIGIN && form.token === "" && form.forget && form.every && !form.html.includes(s.token), JSON.stringify({ ...form, html: form.html.length }));
      await ok.page.close();
      assert(/used/.test(await X.tokenUsed(page, TOKEN_NAME)), "the token was never used");
    });

    s.site = await s.ext.context.newPage();
    await s.site.goto(`${SITE_A}/plain`);

    await run("the toolbar button opens the side panel and starts picking: outline and label under the pointer, a click picks and reaches no page", async () => {
      await X.press(s.ext, s.site);
      s.panel = await X.Panel.open(s.ext.session, `chrome-extension://${s.ext.id}/panel.html`);
      await X.until(() => X.panelState(s.panel), (p) => p.picking, "the panel never said it picks");
      const buy = await X.boxOf(s.site, "#buy");
      await s.site.mouse.move(buy.x + 10, buy.y + 10);
      const hovered = await X.until(() => X.overlay(s.site), (o) => o.host && o.hover.shown, "no outline under the pointer");
      assert(hovered.mode === "closed" && hovered.glass.shown, JSON.stringify(hovered));
      assert(Math.abs(hovered.hover.box.x - buy.x) <= 1 && Math.abs(hovered.hover.box.w - buy.width) <= 1 && hovered.hover.label.startsWith("button#buy.btn.primary") && hovered.hover.label.includes(`${Math.round(buy.width)}×${Math.round(buy.height)}`), JSON.stringify(hovered.hover));
      await s.site.mouse.click(buy.x + 10, buy.y + 10);
      const list = await X.until(() => X.chips(s.panel), (c) => c.length === 1, "the click picked nothing");
      assert(list[0].label === 'button#buy "Buy now"' && list[0].title === "#buy" && list[0].active, JSON.stringify(list));
      const clicks = await s.site.evaluate(() => ({ clicks: window.clicks, seen: window.seenClicks }));
      assert(clicks.clicks === 0 && clicks.seen === 0, `the page saw the click: ${JSON.stringify(clicks)}`);
    });

    await run("Parent and Child widen and narrow the pick, Escape ends picking, the outline goes and clicks reach the page again", async () => {
      await s.panel.click("[data-parent]");
      const wider = await X.until(() => X.chips(s.panel), (c) => c.length === 1 && c[0].label.startsWith("div.card"), "Parent did not widen the pick");
      const card = await X.boxOf(s.site, ".card");
      await X.until(() => X.overlay(s.site), (o) => o.mark.shown && Math.abs(o.mark.box.w - card.width) <= 1 && Math.abs(o.mark.box.h - card.height) <= 1, "the outline does not stand on the parent");
      await s.panel.click("[data-child]");
      await X.until(() => X.chips(s.panel), (c) => c.length === 1 && c[0].label === 'button#buy "Buy now"', `Child did not narrow back from ${wider[0].label}`);
      await s.site.keyboard.press("Escape");
      await X.until(() => X.panelState(s.panel), (p) => !p.picking, "Escape did not end picking");
      await X.until(() => X.overlay(s.site), (o) => !o.host || (!o.glass.shown && !o.hover.shown && !o.mark.shown), "the outline stays after Escape");
      await s.site.click("#buy");
      assert((await s.site.evaluate(() => window.clicks)) === 1, "a click after picking does not reach the page");
      await X.press(s.ext, s.site);
      await X.until(() => X.panelState(s.panel), (p) => p.picking, "the button did not start picking again");
      await X.press(s.ext, s.site);
      await X.until(() => X.panelState(s.panel), (p) => !p.picking, "the button did not end picking");
    });

    await run("the tray of the tab survives a reload and another page, a chip outlines its element again on hover, is dashed where its element is not, and leaves with its cross", async () => {
      await s.site.reload();
      await sleep(300);
      assert((await X.chips(s.panel)).length === 1, "the reload emptied the tray");
      await s.panel.mouse("mouseMoved", 2, 2);
      await s.panel.hover("[data-chips] .chip [data-chip-show]");
      const buy = await X.boxOf(s.site, "#buy");
      await X.until(() => X.overlay(s.site), (o) => o.host && o.mark.shown && Math.abs(o.mark.box.x - buy.x) <= 1 && Math.abs(o.mark.box.w - buy.width) <= 1, "hovering the chip after the reload outlines nothing");
      await s.panel.mouse("mouseMoved", 2, 2);
      await X.until(() => X.overlay(s.site), (o) => !o.host || !o.mark.shown, "the outline stays after the pointer left the chip");
      await s.site.goto(`${SITE_A}/second`);
      await sleep(300);
      await s.panel.hover("[data-chips] .chip [data-chip-show]");
      const gone = await X.until(() => X.chips(s.panel), (c) => c.length === 1 && c[0].gone, "the chip is not dashed on another page");
      assert(gone[0].title.includes("Not on this page any more"), JSON.stringify(gone));
      await s.panel.mouse("mouseMoved", 2, 2);
      await s.site.goto(`${SITE_A}/plain`);
      await s.panel.click("[data-chips] .chip [data-chip-remove]");
      await X.until(() => X.chips(s.panel), (c) => c.length === 0, "the cross did not remove the chip");
    });

    await run("Copy writes the note and the picks as the context's Markdown", async () => {
      await X.press(s.ext, s.site);
      await X.until(() => X.panelState(s.panel), (p) => p.picking, "no picking");
      await X.pickAt(s.site, "#buy");
      await X.until(() => X.chips(s.panel), (c) => c.length === 1, "no pick");
      await s.panel.click("[data-note]");
      await s.panel.type(NOTE);
      await s.panel.eval(() => {
        window.copied = "";
        Object.defineProperty(navigator.clipboard, "writeText", { configurable: true, value: async (text) => { window.copied = text; } });
      });
      await s.panel.click("[data-copy]");
      s.copied = await X.until(() => s.panel.eval(() => window.copied), (t) => t, "Copy wrote nothing");
      assert(s.copied.startsWith(`${NOTE}\n\n## Picked with the browser extension\n`) && s.copied.includes("- Selector: `#buy`") && s.copied.includes("- Text: `Buy now`") && s.copied.includes(`${SITE_A}/plain`), s.copied.slice(0, 300));
      assert((await X.panelState(s.panel)).status === "Copied as Markdown.", "Copy says nothing");
    });

    await menuChecks({ run, s });

    await run("Send lists the assistants, a new assistant with each installed coder and the running coders; a send by the filter and Enter shows the note with the folded card from the extension and the token's name, the assistant reads the copied Markdown", async () => {
      await s.panel.click("[data-send]");
      await s.panel.waitFor("document.querySelectorAll('[data-menu] [role=menuitem]').length > 1", 8000, "the send menu");
      const menu = await s.panel.eval(() => [...document.querySelectorAll("[data-menu] [role=menuitem]")].filter((b) => { const st = getComputedStyle(b); const r = b.getBoundingClientRect(); return st.display !== "none" && st.visibility !== "hidden" && r.width > 0 && r.height > 0; }).map((b) => b.textContent));
      const fresh = menu.filter((one) => one.startsWith("New "));
      assert(s.newWith.length > 1, `the instance offers ${JSON.stringify(s.newWith)}, the runner needs the fakes of claude and copilot`);
      assert(menu.includes(s.title) && JSON.stringify(fresh) === JSON.stringify(s.newWith.map((one) => `New with ${one.name}`)) && menu.includes(`${s.coderName}${PROJECT}`), JSON.stringify(menu));
      await s.panel.type(s.title);
      await X.until(() => menuState(s.panel), (m) => m.rows[m.selected] === `assistant:${s.title}`, "the filter did not mark the assistant");
      await s.panel.key("Enter", "Enter", 13);
      const done = await X.until(() => X.panelState(s.panel), (p) => p.status.startsWith("Sent to"), "the send never ended", 15000);
      const link = await s.panel.eval(() => document.querySelector("[data-status] [data-open]")?.href || "");
      assert(done.status === `Sent to ${s.title}. Open the assistant` && link === `${ORIGIN}/assistants/${s.assistant}`, `${done.status} ${link}`);
      assert(done.note === "" && !done.picking && (await X.chips(s.panel)).length === 0, `the tray kept what went: ${JSON.stringify(done)}`);
      const got = await X.until(() => page.evaluate(async (id) => (await (await fetch(`/assistants/instances/${id}?full=true`, { headers: { Accept: "application/json" } })).json()).messages.filter((m) => m.role === "user").pop(), s.assistant), (m) => m, "the assistant got nothing");
      assert(got.content === s.copied, `the assistant read ${JSON.stringify(got.content.slice(0, 200))}`);
      await page.goto(`${BASE}/assistants/${s.assistant}`, { waitUntil: "domcontentloaded" });
      await page.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
      const card = await X.until(() => page.evaluate((note) => {
        const el = [...document.querySelectorAll("[data-picked]")].find((one) => one.closest("[data-assistant-message]").querySelector("[data-assistant-text]").innerText.trim() === note);
        if (!el) return null;
        const vis = (n) => { if (!n) return false; const st = getComputedStyle(n); const b = n.getBoundingClientRect(); return st.display !== "none" && st.visibility !== "hidden" && b.width > 0 && b.height > 0; };
        return { head: el.querySelector("[data-picked-fold] .fw-medium")?.innerText.trim() || "", via: el.querySelector("[data-picked-via]")?.textContent.trim() || "", viaSeen: vis(el.querySelector("[data-picked-via]")), seen: vis(el), open: vis(el.querySelector("[data-picked-body]")) };
      }, NOTE), (c) => c && c.seen, "the thread shows no card");
      assert(card.head.startsWith("From the extension") && card.via === `via ${TOKEN_NAME}` && card.viaSeen && !card.open, JSON.stringify(card));
    });

    await restingPointerChecks({ run, s });

    await run("a send to a new assistant starts it on the coder chosen, named after the note's first line, and a send to a running coder types the Markdown in", async () => {
      const chosen = s.newWith.at(-1);
      for (const target of [`New with ${chosen.name}`, s.coderName]) {
        await X.press(s.ext, s.site);
        await X.until(() => X.panelState(s.panel), (p) => p.picking, "no picking");
        await X.pickAt(s.site, "#buy");
        await X.until(() => X.chips(s.panel), (c) => c.length === 1, "no pick");
        await s.panel.click("[data-note]");
        await s.panel.type(target === s.coderName ? `${target} ${TAG} note` : `${target} ${TAG} note\nsecond line ${TAG}`);
        await s.panel.click("[data-send]");
        await s.panel.waitFor("document.querySelectorAll('[data-menu] [role=menuitem]').length > 1", 8000, "the send menu");
        const wide = await s.panel.eval(() => ({ page: document.documentElement.scrollWidth - innerWidth, menu: Math.max(...[...document.querySelectorAll("[data-menu] [role=menuitem]")].map((b) => b.getBoundingClientRect().right)) - innerWidth }));
        assert(wide.page <= 0 && wide.menu <= 0, `the menu runs past the panel: ${JSON.stringify(wide)}`);
        await s.panel.clickText("[data-menu] [role=menuitem]", target);
        const done = await X.until(async () => ({ ...(await X.panelState(s.panel)), chips: (await X.chips(s.panel)).length }), (p) => (p.status.startsWith("Sent to") && p.chips === 0) || /refuse|not/.test(p.status), "the send never ended", 15000);
        const link = await s.panel.eval(() => document.querySelector("[data-status] [data-open]")?.href || "");
        if (target !== s.coderName) {
          const id = link.split("/assistants/")[1] || "";
          if (id) s.made.push(id);
          assert(done.status === `Sent to ${target} ${TAG} note. Open the assistant` && id && id !== s.assistant, `${done.status} ${link}`);
          const made = await X.until(() => page.evaluate(async (one) => (await (await fetch(`/assistants/instances/${one}?full=true`, { headers: { Accept: "application/json" } })).json()), id), (m) => m.messages.some((one) => one.role === "user"), "the new assistant got nothing");
          const got = made.messages.filter((m) => m.role === "user").pop();
          assert(made.coderId === chosen.id && made.title === `${target} ${TAG} note`, JSON.stringify({ coder: made.coderId, title: made.title }));
          assert(got.content.startsWith(`${target} ${TAG} note\nsecond line ${TAG}\n\n## Picked with the browser extension\n`), got.content.slice(0, 160));
          await page.goto(`${BASE}/assistants/${id}`, { waitUntil: "domcontentloaded" });
          const title = await X.until(() => page.evaluate(() => { const el = document.querySelector("[data-assistant-title]"); const b = el?.getBoundingClientRect(); return el && b.width > 0 && b.height > 0 && getComputedStyle(el).visibility !== "hidden" ? el.innerText.trim() : ""; }), (t) => t, "the new assistant's page shows no title");
          assert(title === `${target} ${TAG} note`, `the new assistant's page is titled ${title}`);
        } else {
          assert(done.status === `Sent to ${s.coderName}. Open the coder` && link === `${ORIGIN}/coders/${s.coder}`, `${done.status} ${link}`);
          await X.until(() => page.evaluate(async (id) => (await (await fetch(`/coders/${id}/copy?lines=300`)).json()).text || "", s.coder), (t) => t.includes(`${s.coderName} ${TAG} note`) && t.includes("#buy"), "the coder never showed the message");
        }
      }
    });

    await run("the status line belongs to its tab: another tab shows its own, a send that ends after a switch speaks, ends picking and empties the tray only in the tab it left", async () => {
      await s.site.goto(`${SITE_A}/plain`);
      await s.site.bringToFront();
      await sleep(300);
      if ((await X.panelState(s.panel)).picking) await s.site.keyboard.press("Escape");
      await X.until(() => X.panelState(s.panel), (p) => !p.picking, "A still picks");
      await s.panel.click("[data-clear]").catch(() => {});
      await X.until(async () => ({ ...(await X.panelState(s.panel)), chips: (await X.chips(s.panel)).length }), (p) => p.chips === 0 && p.note === "", "A keeps a tray");
      const other = await s.ext.context.newPage();
      try {
        await other.goto(`${SITE_A}/second`);
        await X.press(s.ext, other);
        await X.until(() => X.panelState(s.panel), (p) => p.picking, "no picking in B");
        await X.pickAt(other, "#other");
        await X.until(() => X.chips(s.panel), (c) => c.length === 1 && c[0].label.startsWith("p#other"), "no pick in B");
        await X.press(s.ext, s.site);
        await X.until(() => X.chips(s.panel), (c) => c.length === 0, "the panel did not follow to A");
        await X.until(() => X.panelState(s.panel), (p) => p.picking, "no picking in A");
        await X.pickAt(s.site, "#buy");
        await X.until(() => X.chips(s.panel), (c) => c.length === 1 && c[0].label.startsWith("button#buy"), "no pick in A");
        await s.panel.click("[data-note]");
        await s.panel.type(`Status of A ${TAG}`);
        await s.panel.eval(() => {
          Object.defineProperty(navigator.clipboard, "writeText", { configurable: true, value: async () => {} });
        });
        await s.panel.click("[data-copy]");
        await X.until(() => X.panelState(s.panel), (p) => p.status === "Copied as Markdown.", "Copy said nothing in A");
        await other.bringToFront();
        await X.until(() => X.chips(s.panel), (c) => c.length === 1 && c[0].label.startsWith("p#other"), "the panel did not follow to B");
        const inB = await X.panelState(s.panel);
        assert(inB.status === "" && inB.note === "" && inB.picking, `B shows ${JSON.stringify(inB)}`);
        await s.site.bringToFront();
        await X.until(() => X.panelState(s.panel), (p) => p.status === "Copied as Markdown." && p.note === `Status of A ${TAG}`, "A lost its status");
        await s.panel.eval(() => {
          window.realFetch = window.fetch;
          window.fetch = (input, init) => (String(input).includes("/api/browser-extension/send") ? new Promise((resolve) => setTimeout(resolve, 2500)).then(() => window.realFetch(input, init)) : window.realFetch(input, init));
        });
        await s.panel.click("[data-send]");
        await s.panel.waitFor("document.querySelectorAll('[data-menu] [role=menuitem]').length > 1", 8000, "the send menu");
        await s.panel.clickText("[data-menu] [role=menuitem]", s.title);
        await X.until(() => X.panelState(s.panel), (p) => p.sending && p.status === "", "the send in A never started");
        await other.bringToFront();
        await X.until(() => X.chips(s.panel), (c) => c.length === 1 && c[0].label.startsWith("p#other"), "the panel did not follow to B while A sends");
        const during = await X.panelState(s.panel);
        assert(!during.sending && during.status === "", `B shows A's send: ${JSON.stringify(during)}`);
        await X.until(() => page.evaluate(async ({ id, note }) => (await (await fetch(`/assistants/instances/${id}?full=true`, { headers: { Accept: "application/json" } })).json()).messages.some((m) => m.role === "user" && m.content.startsWith(note)), { id: s.assistant, note: `Status of A ${TAG}` }), (got) => got, "the send of A never arrived", 15000);
        await sleep(800);
        const after = { ...(await X.panelState(s.panel)), chips: (await X.chips(s.panel)).map((c) => c.label).join("|") };
        assert(after.status === "" && !after.sending && after.picking && after.chips.startsWith("p#other") && after.note === "", `B after A's send: ${JSON.stringify(after)}`);
        await s.site.bringToFront();
        await X.until(() => X.panelState(s.panel), (p) => p.status.startsWith("Sent to"), "A does not say it sent");
        const back = { ...(await X.panelState(s.panel)), chips: (await X.chips(s.panel)).length };
        assert(back.status === `Sent to ${s.title}. Open the assistant` && back.chips === 0 && back.note === "" && !back.picking, JSON.stringify(back));
        const outline = await X.overlay(s.site);
        assert(!outline.host || (!outline.glass.shown && !outline.hover.shown), `A still picks after its send: ${JSON.stringify(outline)}`);
        await s.panel.eval(() => { window.fetch = window.realFetch; });
        await other.bringToFront();
        await other.keyboard.press("Escape");
        await X.until(() => X.panelState(s.panel), (p) => !p.picking, "Escape did not end picking in B");
        await s.panel.click("[data-clear]");
        await X.until(() => X.chips(s.panel), (c) => c.length === 0, "Clear left chips in B");
      } finally {
        await s.panel.eval(() => { if (window.realFetch) window.fetch = window.realFetch; }).catch(() => {});
        await other.close();
      }
      await s.site.bringToFront();
    });

    await run("at the page's right edge the label stays in view, a long chip label starts with its tag and ends in an ellipsis, the current chip keeps its look and its whole ring under the pointer, Escape in the panel ends picking unless the Send menu is open", async () => {
      await s.site.goto(`${SITE_A}/edge`);
      await s.panel.click("[data-pick]");
      await X.until(() => X.panelState(s.panel), (p) => p.picking, "Pick in the panel did not pick");
      const account = await X.boxOf(s.site, "#account");
      await s.site.mouse.move(account.x + account.width / 2, account.y + account.height / 2);
      const edge = await X.until(() => X.overlay(s.site), (o) => o.host && o.hover.shown && o.hover.tag, "no outline on the button at the edge");
      const room = await s.site.evaluate(() => document.documentElement.clientWidth);
      const size = edge.hover.size || {};
      assert(edge.hover.tag.x >= 0 && edge.hover.tag.x + edge.hover.tag.w <= room + 0.5 && size.text === `  ${Math.round(account.width)}×${Math.round(account.height)}` && size.box && size.box.x + size.box.w <= room + 0.5, JSON.stringify({ tag: edge.hover.tag, room, size }));
      await s.site.mouse.click(account.x + account.width / 2, account.y + account.height / 2);
      await X.pickAt(s.site, ".product-card__description-text--highlighted");
      await X.until(() => X.chips(s.panel), (c) => c.length === 2 && c[1].active && c[1].label.startsWith("p.product-card__description-text-with"), "no pick of the long element");
      const look = await s.panel.eval(() => {
        const button = document.querySelectorAll("[data-chips] .chip [data-chip-show]")[1];
        const text = button.querySelector(".chip-text");
        const b = button.getBoundingClientRect();
        const t = text.getBoundingClientRect();
        return { cut: text.scrollWidth > text.clientWidth, ellipsis: getComputedStyle(text).textOverflow, start: t.left - b.left - parseFloat(getComputedStyle(button).paddingLeft), inside: t.right <= b.right + 0.5 };
      });
      assert(look.cut && look.ellipsis === "ellipsis" && Math.abs(look.start) <= 1 && look.inside, `the long chip label: ${JSON.stringify(look)}`);
      const colors = [];
      for (const at of [0, 1]) {
        await s.panel.hover(`[data-chips] .chip:nth-child(${at + 1}) [data-chip-show]`);
        await sleep(150);
        colors.push(await s.panel.eval((i) => { const st = getComputedStyle(document.querySelectorAll("[data-chips] .chip [data-chip-show]")[i]); return `${st.backgroundColor} ${st.color} ${st.filter}`; }, at));
      }
      assert(colors[0] === colors[1], `the current chip under the pointer looks unlike the other: ${JSON.stringify(colors)}`);
      const ring = async () => {
        const clip = await s.panel.eval(() => { const b = document.querySelectorAll("[data-chips] .chip")[1].getBoundingClientRect(); return { x: Math.round(b.x + scrollX), y: Math.round(b.y + scrollY), width: Math.round(b.width), height: Math.round(b.height), scale: 1 }; });
        const { data } = await s.panel.send("Page.captureScreenshot", { format: "png", clip });
        return s.panel.eval(async (b64) => {
          const img = new Image();
          img.src = `data:image/png;base64,${b64}`;
          await img.decode();
          const g = Object.assign(document.createElement("canvas"), { width: img.width, height: img.height }).getContext("2d");
          g.drawImage(img, 0, 0);
          return [0, 1, img.height - 2, img.height - 1].map((y) => g.getImageData(img.height, y, img.width - 2 * img.height, 1).data.join()).join("|");
        }, data);
      };
      await s.panel.mouse("mouseMoved", 2, 2);
      await sleep(150);
      const still = await ring();
      for (const part of ["[data-chip-show]", "[data-chip-remove]"]) {
        await s.panel.hover(`[data-chips] .chip:nth-child(2) ${part}`);
        await sleep(150);
        assert((await ring()) === still, `the ring of the current chip changes under the pointer on ${part}`);
      }
      await s.panel.click("[data-send]");
      await s.panel.waitFor("document.querySelectorAll('[data-menu] [role=menuitem]').length > 1", 8000, "the send menu");
      await s.panel.key("Escape", "Escape", 27);
      const menuShut = await X.until(async () => ({ ...(await X.panelState(s.panel)), menu: await s.panel.eval(() => !document.querySelector("[data-menu]").hidden) }), (p) => !p.menu, "Escape did not close the menu");
      assert(menuShut.picking, "Escape on the open menu ended picking");
      await s.panel.click("[data-note]");
      await s.panel.key("Escape", "Escape", 27);
      await X.until(() => X.panelState(s.panel), (p) => !p.picking, "Escape in the panel did not end picking");
      await X.until(() => X.overlay(s.site), (o) => !o.host || (!o.glass.shown && !o.hover.shown), "the outline stays after Escape in the panel");
      await s.panel.click("[data-clear]");
      await X.until(() => X.chips(s.panel), (c) => c.length === 0, "Clear left chips");
    });

    await run("a send without a note names the new assistant after the page's title, else its address, picks of two pages after the first; a name cut short or ending its own sentence gets no second period; the status line cuts a name past 40 characters with an ellipsis, never inside an emoji, for a new and an existing assistant, the assistant keeps its whole name; the New rows wear a plus", async () => {
      const chosen = s.newWith.at(-1);
      const pickOn = async (path, selector, { goesOn = false } = {}) => {
        await s.site.goto(`${SITE_A}${path}`);
        if (goesOn) await X.until(() => X.overlay(s.site), (o) => o.host && o.glass.shown, `picking did not go on at ${path}`);
        else await X.press(s.ext, s.site);
        await X.until(() => X.panelState(s.panel), (p) => p.picking, `no picking on ${path}`);
        await X.pickAt(s.site, selector);
      };
      const sendTo = async (row, note) => {
        if (note) {
          await s.panel.click("[data-note]");
          await s.panel.type(note);
        }
        await s.panel.click("[data-send]");
        await s.panel.waitFor("document.querySelectorAll('[data-menu] [role=menuitem]').length > 1", 8000, "the send menu");
        const marks = await s.panel.eval(() => [...document.querySelectorAll("[data-menu] [role=menuitem]")].map((b) => [b.dataset.target, getComputedStyle(b.querySelector(".menu-name"), "::before").content]));
        await s.panel.clickText("[data-menu] [role=menuitem]", row);
        const done = await X.until(() => X.panelState(s.panel), (p) => p.status.startsWith("Sent to") || /refuse|not/.test(p.status), "the send never ended", 15000);
        const id = (await s.panel.eval(() => document.querySelector("[data-status] [data-open]")?.href || "")).split("/assistants/")[1] || "";
        if (id && !s.made.includes(id)) s.made.push(id);
        const made = id ? await X.until(() => page.evaluate(async (one) => (await (await fetch(`/assistants/instances/${one}?full=true`, { headers: { Accept: "application/json" } })).json()), id), (m) => m.messages.some((one) => one.role === "user"), "the new assistant got nothing") : {};
        return { marks, status: done.status, title: made.title };
      };
      const sendNew = (note) => sendTo(`New with ${chosen.name}`, note);
      await pickOn("/notitle", "#buy");
      await X.until(() => X.chips(s.panel), (c) => c.length === 1, "no pick on the page without a title");
      const bare = await sendNew("");
      const address = `localhost:${X.PORT_A}/notitle`;
      assert(bare.status === `Sent to ${address}. Open the assistant` && bare.title === address, JSON.stringify(bare));
      assert(bare.marks.length > 2 && bare.marks.every(([kind, mark]) => (kind === "new") === (mark === '"+ "')), `the menu rows: ${JSON.stringify(bare.marks)}`);
      await pickOn("/plain", "#buy");
      await X.until(() => X.chips(s.panel), (c) => c.length === 1, "no pick on the first page");
      await pickOn("/second", "#other", { goesOn: true });
      await X.until(() => X.chips(s.panel), (c) => c.length === 2, "no pick on the second page");
      const two = await sendNew("");
      assert(two.status === "Sent to Plain page. Open the assistant" && two.title === "Plain page", JSON.stringify(two));
      await pickOn("/plain", "#buy");
      await X.until(() => X.chips(s.panel), (c) => c.length === 1, "no pick for the long note");
      const long = `${"Make the buy button wider and give it more room on phones, ".repeat(3)}${TAG}`;
      const cut = await sendNew(long);
      assert(cut.status === `Sent to ${long.slice(0, 39)}… Open the assistant` && cut.title === `${long.slice(0, 120)}…`, JSON.stringify(cut));
      await pickOn("/plain", "#buy");
      await X.until(() => X.chips(s.panel), (c) => c.length === 1, "no pick for the assistant with the long name");
      const existing = await sendTo(cut.title, `To the long name ${TAG}`);
      assert(existing.status === `Sent to ${long.slice(0, 39)}… Open the assistant` && existing.title === cut.title, JSON.stringify(existing));
      const lead = `The cart icon ${TAG} is wrong on phones`;
      const emoji = `${lead}\u{1F469}\u{1F3FD}\u200D\u{1F4BB} and the menu icon too`;
      await pickOn("/plain", "#buy");
      await X.until(() => X.chips(s.panel), (c) => c.length === 1, "no pick for the note with the emoji");
      const whole = await sendNew(emoji);
      assert(lead.length === 38 && whole.status === `Sent to ${lead}\u{1F469}\u{1F3FD}\u200D\u{1F4BB}… Open the assistant` && whole.title === emoji, JSON.stringify(whole));
      for (const line of [`The buy button needs more room ${TAG}.`, `Make the buy button say "Buy now ${TAG}."`, `Why is the buy button green ${TAG}?`]) {
        await pickOn("/plain", "#buy");
        await X.until(() => X.chips(s.panel), (c) => c.length === 1, `no pick for the note ${line}`);
        const ended = await sendNew(`${line}\nThe second line.`);
        assert(ended.status === `Sent to ${line} Open the assistant` && ended.title === line, JSON.stringify(ended));
      }
    });

    await run("picks work on pages that ban frames, set a strict policy, load assets from or log in on another origin", async () => {
      const pages = [
        { url: `${SITE_A}/banned`, pick: "#buy", want: (item) => item.text === "Buy banned" },
        { url: `${SITE_A}/strict`, pick: ".price", want: (item) => item.styles.some(([k, v]) => k === "color" && v === "rgb(200, 0, 0)") && item.styles.some(([k, v]) => k === "font-size" && v === "22px") },
        { url: `${SITE_A}/assets`, pick: "#assets", want: (item) => item.styles.some(([k, v]) => k === "background-color" && v === "rgb(0, 0, 200)") },
        { url: `${SITE_A}/account`, pick: ".who", want: (item) => item.text === "Signed in as the ext tester", login: true },
      ];
      for (const one of pages) {
        if (one.login && (await X.panelState(s.panel)).picking) {
          await s.site.keyboard.press("Escape");
          await X.until(() => X.panelState(s.panel), (p) => !p.picking, "Escape did not end picking");
        }
        const goesOn = (await X.panelState(s.panel)).picking;
        await s.site.goto(one.url);
        if (one.login) {
          assert(s.site.url().startsWith(`${SITE_B}/login`), `the account page did not ask for the login: ${s.site.url()}`);
          await Promise.all([s.site.waitForURL(`${SITE_A}/account`), s.site.click("#sign-in")]);
        }
        if (goesOn) await X.until(() => X.overlay(s.site), (o) => o.host && o.glass.shown, `picking did not go on at ${one.url}`);
        else await X.press(s.ext, s.site);
        await X.until(() => X.panelState(s.panel), (p) => p.picking, `no picking on ${one.url}`);
        const before = (await X.chips(s.panel)).length;
        await X.pickAt(s.site, one.pick);
        await X.until(() => X.chips(s.panel), (c) => c.length === before + 1, `no pick on ${one.url}`);
        const kept = await s.ext.worker.evaluate(async () => Object.values(await chrome.storage.local.get(null)).filter((v) => v && Array.isArray(v.items)).flatMap((v) => v.items.map((e) => e.item)));
        const item = kept.find((it) => it.url === one.url);
        assert(item && one.want(item), `${one.url}: ${JSON.stringify(item)?.slice(0, 400)}`);
        assert((await s.site.evaluate(() => window.clicks || 0)) === 0, `${one.url} saw the pick's click`);
      }
      await s.site.keyboard.press("Escape");
      await X.until(() => X.panelState(s.panel), (p) => !p.picking, "Escape did not end picking");
      await s.panel.click("[data-clear]");
      await X.until(() => X.chips(s.panel), (c) => c.length === 0, "Clear left chips");
    });

    await run("a modal dialog of the page, open before picking or opened while it picks, takes no click: the outline and the pick reach into it", async () => {
      await s.site.goto(`${SITE_A}/modal`);
      await X.press(s.ext, s.site);
      await X.until(() => X.panelState(s.panel), (p) => p.picking, "no picking");
      const near = (box, el) => box && Math.abs(box.x - el.x) <= 1 && Math.abs(box.y - el.y) <= 1 && Math.abs(box.w - el.width) <= 1;
      const pickIn = async (selector, label, count) => {
        const el = await X.boxOf(s.site, selector);
        await s.site.mouse.move(el.x + el.width / 2, el.y + el.height / 2);
        await X.until(() => X.overlay(s.site), (o) => o.host && o.hover.shown && near(o.hover.box, el), `no outline on ${selector}`);
        await s.site.mouse.click(el.x + el.width / 2, el.y + el.height / 2);
        const got = await X.until(() => X.chips(s.panel), (c) => c.length === count, `the click on ${selector} picked nothing`);
        assert(got[count - 1].label === label && got[count - 1].active, JSON.stringify(got));
        const seen = await s.site.evaluate(() => ({ clicks: window.clicks, seen: window.seenClicks, title: document.title }));
        assert(seen.clicks === 0 && seen.seen === 0 && seen.title === "Modal page", `the page saw the click on ${selector}: ${JSON.stringify(seen)}`);
      };
      await pickIn("#ok", 'button#ok "OK"', 1);
      await s.panel.click("[data-parent]");
      await X.until(() => X.chips(s.panel), (c) => c.length === 1 && c[0].label.startsWith("dialog#confirm"), "Parent did not widen to the dialog");
      const kept = await s.ext.worker.evaluate(async () => Object.values(await chrome.storage.local.get(null)).filter((v) => v && Array.isArray(v.items)).flatMap((v) => v.items.map((e) => e.item)));
      const dialog = kept.find((it) => it.tag === "dialog");
      assert(dialog && dialog.html.includes('id="ok"') && !dialog.html.includes("dc-extension-pick"), `the dialog's HTML: ${dialog?.html.slice(0, 300)}`);
      await s.site.evaluate(() => document.getElementById("later").showModal());
      await pickIn("#later-ok", 'button#later-ok "Later OK"', 2);
      await s.site.evaluate(() => document.querySelectorAll("dialog").forEach((one) => one.close()));
      await pickIn("#buy", 'button#buy "Buy behind"', 3);
      await s.site.keyboard.press("Escape");
      await X.until(() => X.panelState(s.panel), (p) => !p.picking, "Escape did not end picking");
      assert((await s.site.evaluate(() => document.querySelectorAll("dc-extension-pick").length)) === 0, "the overlay stayed in the page");
      await s.site.click("#buy");
      assert((await s.site.evaluate(() => window.clicks)) === 1, "a click after picking does not reach the page");
      await s.panel.click("[data-clear]");
      await X.until(() => X.chips(s.panel), (c) => c.length === 0, "Clear left chips");
    });

    await run("a native popover menu of the page, open before picking or opened while it picks, stays open: a click picks in it and beside it and reaches no page", async () => {
      await s.site.goto(`${SITE_A}/menu`);
      await s.site.click("#open-menu");
      await s.site.click("#open-sub");
      const state = () => s.site.evaluate(() => ({ open: ["menu", "sub", "late"].filter((id) => document.getElementById(id).matches(":popover-open")).join(), toggles: window.toggles.join(), clicks: window.clicks, seen: window.seenClicks }));
      await X.until(state, (o) => o.open === "menu,sub" && o.toggles === "menu open,sub open", "the menu did not open");
      await s.site.evaluate(() => { window.seenClicks = 0; });
      await X.press(s.ext, s.site);
      await X.until(() => X.panelState(s.panel), (p) => p.picking, "no picking");
      const near = (box, el) => box && Math.abs(box.x - el.x) <= 1 && Math.abs(box.y - el.y) <= 1 && Math.abs(box.w - el.width) <= 1;
      const pickIn = async (selector, label, count, open, toggles) => {
        const el = await X.boxOf(s.site, selector);
        await s.site.mouse.move(el.x + el.width / 2, el.y + el.height / 2);
        await X.until(() => X.overlay(s.site), (o) => o.host && o.hover.shown && near(o.hover.box, el), `no outline on ${selector}`);
        await s.site.mouse.click(el.x + el.width / 2, el.y + el.height / 2);
        const got = await X.until(() => X.chips(s.panel), (c) => c.length === count, `the click on ${selector} picked nothing`);
        assert(got[count - 1].label === label && got[count - 1].active, JSON.stringify(got));
        const now = await state();
        assert(now.open === open && now.toggles === toggles && now.clicks === 0 && now.seen === 0, `the page saw the click on ${selector}: ${JSON.stringify(now)}`);
      };
      await pickIn("#item2", 'button#item2 "Item two"', 1, "menu,sub", "menu open,sub open");
      await pickIn("#sub1", 'button#sub1 "Sub item"', 2, "menu,sub", "menu open,sub open");
      await s.panel.click("[data-parent]");
      await X.until(() => X.chips(s.panel), (c) => c.length === 2 && c[1].label.startsWith("div#sub"), "Parent did not widen to the submenu");
      const kept = await s.ext.worker.evaluate(async () => Object.values(await chrome.storage.local.get(null)).filter((v) => v && Array.isArray(v.items)).flatMap((v) => v.items.map((e) => e.item)));
      const sub = kept.find((it) => it.tag === "div" && it.id === "sub");
      assert(sub && sub.html.includes('id="sub1"') && !sub.html.includes("dc-extension-pick") && sub.text === "Sub item", `the submenu's context: ${JSON.stringify(sub)?.slice(0, 400)}`);
      await s.site.evaluate(() => document.getElementById("late").showPopover());
      await X.until(state, (o) => o.toggles === "menu open,sub open,late open", "the page's popover did not open");
      await pickIn("#buy", 'button#buy "Buy behind"', 3, "menu,sub,late", "menu open,sub open,late open");
      await pickIn("#late-ok", 'button#late-ok "Late OK"', 4, "menu,sub,late", "menu open,sub open,late open");
      await s.site.keyboard.press("Escape");
      await X.until(() => X.panelState(s.panel), (p) => !p.picking, "Escape did not end picking");
      assert((await s.site.evaluate(() => document.querySelectorAll("dc-extension-pick").length)) === 0, "the overlay stayed in the page");
      await s.site.evaluate(() => ["late", "sub", "menu"].forEach((id) => document.getElementById(id).hidePopover()));
      await s.panel.click("[data-clear]");
      await X.until(() => X.chips(s.panel), (c) => c.length === 0, "Clear left chips");
    });

    await run("the note belongs to its tab: after a tab switch with the note in focus the panel shows that tab's note, typing and Copy keep to it", async () => {
      const trays = () => s.ext.worker.evaluate(async () => Object.values(await chrome.storage.local.get(null)).filter((v) => v && Array.isArray(v.items)).map((v) => ({ urls: v.items.map((e) => e.item.url), note: v.note })));
      const noteOf = async (url) => (await trays()).find((one) => one.urls.includes(url))?.note;
      const pickIn = async (tab, selector, label) => {
        await X.press(s.ext, tab);
        await X.until(() => X.panelState(s.panel), (p) => p.picking, "no picking");
        await X.pickAt(tab, selector);
        await X.until(() => X.chips(s.panel), (c) => c.length === 1 && c[0].label.startsWith(label), `no pick of ${selector}`);
        await tab.keyboard.press("Escape");
        await X.until(() => X.panelState(s.panel), (p) => !p.picking, "Escape did not end picking");
      };
      const other = await s.ext.context.newPage();
      try {
        await s.site.goto(`${SITE_A}/plain`);
        await pickIn(s.site, "#buy", "button#buy");
        await other.goto(`${SITE_A}/second`);
        await pickIn(other, "#other", "p#other");
        await s.panel.click("[data-note]");
        await s.panel.type(`note of B ${TAG}`);
        await X.until(() => noteOf(`${SITE_A}/second`), (n) => n === `note of B ${TAG}`, "the note of B was not kept");
        await s.site.bringToFront();
        await X.until(() => X.chips(s.panel), (c) => c.length === 1 && c[0].label.startsWith("button#buy"), "the panel did not follow to A");
        await s.panel.click("[data-note]");
        await s.panel.type(`note of A ${TAG}`);
        await other.bringToFront();
        await X.until(() => X.chips(s.panel), (c) => c.length === 1 && c[0].label.startsWith("p#other"), "the panel did not follow to B");
        const shown = await s.panel.eval(() => ({ note: document.querySelector("[data-note]").value, focus: document.activeElement === document.querySelector("[data-note]") }));
        assert(shown.note === `note of B ${TAG}` && shown.focus, `B shows ${JSON.stringify(shown)}`);
        await s.panel.type(" plus");
        await X.until(async () => [await noteOf(`${SITE_A}/second`), await noteOf(`${SITE_A}/plain`)], ([b, a]) => b === `note of B ${TAG} plus` && a === `note of A ${TAG}`, "a note went to the other tab");
        await s.panel.eval(() => {
          window.copied = "";
          Object.defineProperty(navigator.clipboard, "writeText", { configurable: true, value: async (text) => { window.copied = text; } });
        });
        await s.panel.click("[data-copy]");
        const copied = await X.until(() => s.panel.eval(() => window.copied), (t) => t, "Copy wrote nothing");
        assert(copied.startsWith(`note of B ${TAG} plus\n\n`), copied.slice(0, 120));
        await s.panel.click("[data-clear]");
        await X.until(() => X.chips(s.panel), (c) => c.length === 0, "Clear left chips in B");
      } finally {
        await other.close();
      }
      await s.site.bringToFront();
      await X.until(() => X.chips(s.panel), (c) => c.length === 1 && c[0].label.startsWith("button#buy"), "the panel did not come back to A");
      await s.panel.click("[data-clear]");
      await X.until(async () => ({ chips: (await X.chips(s.panel)).length, note: (await X.panelState(s.panel)).note }), (p) => p.chips === 0 && p.note === "", "Clear left the tray of A");
    });

    await run("with access to every page, Pick in the panel picks in a tab never clicked, picking goes on through a reload and another site, and each tab keeps its own tray", async () => {
      const home = { ...(await X.panelState(s.panel)), chips: (await X.chips(s.panel)).map((c) => c.label).join("|") };
      assert(home.access === "", "the panel says it lacks access to every page");
      const other = await s.ext.context.newPage();
      try {
        await other.goto(`${X.SITE_A_AGAIN}/second`);
        await other.bringToFront();
        await X.until(async () => ({ ...(await X.panelState(s.panel)), chips: (await X.chips(s.panel)).length }), (p) => p.chips === 0 && !p.picking, "the panel did not follow to the new tab");
        await s.panel.click("[data-pick]");
        await X.until(() => X.panelState(s.panel), (p) => p.picking && !p.alert, "Pick in the panel did not pick in a tab never clicked");
        await X.pickAt(other, "#other");
        await X.until(() => X.chips(s.panel), (c) => c.length === 1 && c[0].label.startsWith("p#other"), "no pick in a tab never clicked");
        await other.reload();
        await X.until(() => X.overlay(other), (o) => o.host && o.glass.shown, "no picker after the reload");
        await X.pickAt(other, "h1");
        await X.until(() => X.chips(s.panel), (c) => c.length === 2 && c[1].label.startsWith("h1"), "no pick after the reload");
        await other.goto(`${SITE_B}/login?back=none`);
        await X.until(() => X.overlay(other), (o) => o.host && o.glass.shown, "no picker on another site");
        await X.pickAt(other, "#sign-in");
        await X.until(() => X.chips(s.panel), (c) => c.length === 3 && c[2].label.startsWith("button#sign-in"), "no pick on another site");
        assert(other.url().startsWith(`${SITE_B}/login`) && (await X.panelState(s.panel)).picking, `the pick reached the page or picking ended: ${other.url()}`);
        await s.site.bringToFront();
        await X.until(async () => ({ ...(await X.panelState(s.panel)), chips: (await X.chips(s.panel)).map((c) => c.label).join("|") }), (p) => p.chips === home.chips && p.picking === home.picking, "the panel kept the other tab's tray or picking");
        assert(home.picking || !(await X.overlay(s.site)).host, "the picker went into a tab nobody picks in");
        await other.bringToFront();
        await X.until(async () => ({ ...(await X.panelState(s.panel)), chips: (await X.chips(s.panel)).length }), (p) => p.chips === 3 && p.picking, "the panel lost the other tab's tray");
        await other.keyboard.press("Escape");
        await X.until(() => X.panelState(s.panel), (p) => !p.picking, "Escape did not end picking");
        await other.reload();
        await sleep(600);
        assert(!(await X.panelState(s.panel)).picking && !(await X.overlay(other)).host, "picking came back after Escape and a reload");
        await s.panel.click("[data-clear]");
        await X.until(() => X.chips(s.panel), (c) => c.length === 0, "Clear left chips");
      } finally {
        await other.close();
      }
      await s.site.bringToFront();
    });

    await run("a fresh setup ticks Access to every page and its save asks for it; without that access the panel says so in one sentence, Pick refuses a tab never clicked, and picking ends when the clicked tab opens another site", async () => {
      const dir = X.unpack(s.files, [ORIGIN]);
      const narrow = await X.launchWith(dir);
      let panel = null;
      try {
        const first = await X.setupPage(narrow);
        const ticked = await first.evaluate(() => ({ checked: document.querySelector("[data-every-page]").checked, disabled: document.querySelector("[data-every-page]").disabled }));
        assert(ticked.checked && !ticked.disabled, `a fresh setup shows Access to every page as ${JSON.stringify(ticked)}`);
        await first.fill("[data-address]", ORIGIN);
        await first.fill("[data-token]", s.spare);
        await first.click("[data-save]");
        const asking = await X.until(() => first.textContent("[data-result]"), (t) => t, "the save asked for nothing");
        assert(asking === `Allow the extension to reach ${ORIGIN} and every page in the browser's prompt.`, asking);
        await first.close();
        const set = await X.setUp(narrow, ORIGIN, s.spare, { every: false });
        assert(!set.error && set.said.startsWith("Connected."), set.said);
        await set.page.close();
        const setup = await X.setupPage(narrow);
        const box = await X.until(() => setup.evaluate(() => document.querySelector("[data-forget]").hidden ? null : { checked: document.querySelector("[data-every-page]").checked, disabled: document.querySelector("[data-every-page]").disabled }), (v) => v !== null, "the setup did not read what is saved");
        assert(!box.checked && !box.disabled, `the setup shows Access to every page as ${JSON.stringify(box)} without the grant`);
        await setup.close();
        const tab = await narrow.context.newPage();
        await tab.goto(`${SITE_A}/plain`);
        await X.press(narrow, tab);
        panel = await X.Panel.open(narrow.session, `chrome-extension://${narrow.id}/panel.html`);
        const said = await X.until(() => X.panelState(panel), (p) => p.picking && p.access, "the panel does not say it lacks access to every page");
        assert(said.access === "Without access to every page the extension picks and draws only after a click on its button in the tab, and lists no console. Setup gives the access.", said.access);
        await X.pickAt(tab, "#buy");
        await X.until(() => X.chips(panel), (c) => c.length === 1, "the button did not let the extension pick in its tab");
        const other = await narrow.context.newPage();
        await other.goto(`${SITE_A}/second`);
        await other.bringToFront();
        await X.until(() => X.chips(panel), (c) => c.length === 0, "the panel did not follow to the new tab");
        await panel.click("[data-pick]");
        const refused = await X.until(() => X.panelState(panel), (p) => p.alert.includes("cannot pick"), "Pick in a tab never clicked said nothing");
        assert(!refused.picking && refused.alert.startsWith("The extension cannot pick or draw on this page. Browser pages and the extension stores stay closed to it, on other pages click its button in the toolbar or press its key.") && !(await X.overlay(other)).host, JSON.stringify(refused));
        await other.close();
        await tab.bringToFront();
        await X.until(() => X.chips(panel), (c) => c.length === 1, "the panel did not come back to the clicked tab");
        await tab.goto(`${X.SITE_A_AGAIN}/plain`);
        await sleep(800);
        const away = await X.panelState(panel);
        assert(!away.picking && !(await X.overlay(tab)).host && away.access, `picking went on on another site: ${JSON.stringify(away)}`);
        await X.press(narrow, tab);
        await X.until(() => X.panelState(panel), (p) => p.picking, "the button did not pick on the other site");
        await X.pickAt(tab, "#buy");
        await X.until(() => X.chips(panel), (c) => c.length === 2, "no pick after the button on the other site");
      } finally {
        await panel?.close();
        await narrow.close();
        fs.rmSync(dir, { recursive: true, force: true });
      }
    });

    await run("a hostile page can neither start nor end picking, fill the tray, send, nor reach the extension's pages, and its markup stays text", async () => {
      await s.site.goto(`${SITE_A}/hostile`);
      const asked = await s.site.evaluate((id) => window.attack(id), s.ext.id);
      await sleep(600);
      let state = await X.panelState(s.panel);
      assert(!state.picking && (await X.chips(s.panel)).length === 0, `the page changed the tray: ${JSON.stringify(state)}`);
      assert(asked.includes("runtime refused") && ["panel.html", "options.html", "manifest.json", "pick.js"].every((f) => asked.includes(`${f} refused`)), JSON.stringify(asked));
      await X.press(s.ext, s.site);
      await X.until(() => X.panelState(s.panel), (p) => p.picking, "no picking");
      await s.site.evaluate((id) => window.attack(id), s.ext.id);
      await sleep(600);
      state = await X.panelState(s.panel);
      assert(state.picking && (await X.chips(s.panel)).length === 0, `the page picked or ended picking: ${JSON.stringify(state)}`);
      await X.pickAt(s.site, "#markup");
      const list = await X.until(() => X.chips(s.panel), (c) => c.length === 1, "the user's own pick did not land");
      const panelView = await s.panel.eval(() => ({ imgs: [...document.querySelectorAll("img")].map((i) => i.getAttribute("src")), pwned: Boolean(window.__pwned) }));
      assert(list[0].label.startsWith('button#markup "<img src=/ext-pwned') && JSON.stringify(panelView.imgs) === JSON.stringify(["icons/32.png"]) && !panelView.pwned, JSON.stringify({ list, panelView }));
      const html = await s.site.content();
      assert(!html.includes(s.token) && !(await s.site.evaluate((t) => JSON.stringify({ ...localStorage }).includes(t) || JSON.stringify({ ...sessionStorage }).includes(t), s.token)), "the page holds the token");
      await s.site.keyboard.press("Escape");
      await X.until(() => X.panelState(s.panel), (p) => !p.picking, "Escape did not end picking");
    });

    await run("the panel and the setup in light and dark, read by hand from the screenshots", async () => {
      if (!SHOTS) return "EXT_SHOTS not set";
      await s.panel.click("[data-note]");
      await s.panel.type("Look at the hostile button.");
      for (const dark of [false, true]) {
        await s.panel.scheme(dark);
        await s.panel.shot(`${SHOTS}/panel-${dark ? "dark" : "light"}.png`);
        const setup = await X.setupPage(s.ext);
        await setup.emulateMedia({ colorScheme: dark ? "dark" : "light" });
        await setup.screenshot({ path: `${SHOTS}/setup-${dark ? "dark" : "light"}.png`, fullPage: true });
        await setup.close();
      }
      await s.panel.scheme(false);
      await X.until(async () => ({ chips: (await X.chips(s.panel)).length, send: await s.panel.eval(() => !document.querySelector("[data-send]").disabled) }), (p) => p.chips === 1 && p.send, "the panel did not come back to the page's tray after the setup closed");
      await s.panel.click("[data-send]");
      await s.panel.waitFor("document.querySelectorAll('[data-menu] [role=menuitem]').length > 1", 8000, "the send menu");
      await s.panel.shot(`${SHOTS}/panel-menu.png`);
      await s.panel.key("Escape", "Escape", 27);
      await X.press(s.ext, s.site);
      const bait = await X.boxOf(s.site, "#bait");
      await s.site.mouse.move(bait.x + 20, bait.y + 20);
      await sleep(200);
      await s.site.screenshot({ path: `${SHOTS}/page-picking.png` });
      await s.site.keyboard.press("Escape");
      return "screenshots written";
    });

    await run("a revoked token is refused at once, Send says so in one sentence, and a new token saved in the setup goes out with the next send", async () => {
      await X.revokeToken(page, TOKEN_NAME);
      await s.panel.click("[data-send]");
      const said = await X.until(() => X.panelState(s.panel), (p) => /refuses the token/.test(p.status), "Send did not say the token is refused");
      assert(said.status === "The cockpit refuses the token, it is wrong or revoked. Make a new one under Settings › Browser and paste it into the setup.", said.status);
      assert((await s.panel.eval(() => document.querySelector("[data-menu]").hidden)) === true, "the menu stayed open");
      const fresh = await X.setUp(s.ext, ORIGIN, s.spare);
      assert(!fresh.error, fresh.said);
      await fresh.page.close();
      await s.site.bringToFront();
      await X.until(async () => ({ chips: (await X.chips(s.panel)).length, send: await s.panel.eval(() => !document.querySelector("[data-send]").disabled) }), (p) => p.chips === 1 && p.send, "the panel did not come back to the page's tray");
      await s.panel.click("[data-send]");
      await s.panel.waitFor("document.querySelectorAll('[data-menu] [role=menuitem]').length > 1", 8000, "the send menu with the new token");
      await s.panel.clickText("[data-menu] [role=menuitem]", s.title);
      const sent = await X.until(() => X.panelState(s.panel), (p) => p.status.startsWith("Sent to") || /refuse/.test(p.status), "the send with the new token never ended", 15000);
      assert(sent.status === `Sent to ${s.title}. Open the assistant`, sent.status);
    });

    await require("./browser-extension-access").accessChecks({ run, s });

    await run("the setup says when the browser does not trust the cockpit's certificate", async () => {
      if (!ORIGIN.startsWith("https:")) return "the cockpit runs without TLS, the certificate check is skipped";
      const strict = await X.launchWith(s.dir, { trustAll: false });
      try {
        const said = await X.setUp(strict, ORIGIN, s.spare);
        assert(said.error && said.said === `This browser does not trust the certificate of ${ORIGIN}. Open that address in a tab and trust it, then check again.`, said.said);
        const tab = await strict.context.newPage();
        await tab.goto(`${ORIGIN}/login`).catch(() => {});
        await tab.click("#details-button");
        await Promise.all([tab.waitForURL(`${ORIGIN}/login`, { waitUntil: "domcontentloaded" }), tab.click("#proceed-link")]);
        await said.page.click("[data-save]");
        const again = await X.until(() => said.page.textContent("[data-result]"), (t) => t && t !== "Checking…" && !t.startsWith("Allow the extension to reach"), "the check never answered", 12000);
        assert(again.startsWith("Connected."), `after trusting the certificate in a tab: ${again}`);
      } finally {
        await strict.close();
      }
    });
  } catch (e) {
    results.push({ name: "[chromium] FATAL", status: "FAIL", detail: e.stack || e.message });
  } finally {
    try {
      await s.panel?.close();
      await s.ext?.close();
      if (s.dir) fs.rmSync(s.dir, { recursive: true, force: true });
      await X.revokeToken(page, TOKEN_NAME);
      await X.revokeToken(page, `Spare ${TAG}`);
      await X.revokeToken(page, `Probe ${TAG}`);
      for (const one of s.made) await X.postAs(page, `/assistants/${one}`, { form: "delete" });
      if (s.coder) await X.postAs(page, `/coders/${s.coder}/delete`, {});
      await L.deleteProject(page, PROJECT);
    } catch (e) {
      results.push({ name: "[chromium] cleanup", status: "FAIL", detail: e.message });
    }
    sites.close();
    await ctx.close().catch(() => {});
    await browser.close().catch(() => {});
  }
  process.exit(L.report("BROWSER EXTENSION", results, bag) ? 1 : 0);
}

main();
