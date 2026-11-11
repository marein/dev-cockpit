const L = require("./lib");
const { assert, sleep, BASE } = L;

// Not a runner of its own: assistant.js runs these. The terminals page's keys
// on the assistant page, answered by the list column (dc-assistant-list).
// Ctrl+T and Cmd+T open the column head's new assistant control from the
// message box with the keyboard on its first coder, Escape hands the box the
// keyboard back with its words, and Enter starts a new assistant as the
// control's own click does. Ctrl+Shift+X and Cmd+Shift+X ask the question of
// Delete in the row's menu; Escape on it deletes nothing, a key repeat and an
// open menu ask nothing, and the confirm deletes the open assistant and lands
// where the bare address leads, as the row's Delete does. None of these keys
// reaches the message box as text. The guest's side is in assistant-share.js.

const READY = "dc-assistant[ready]";
const BOX = "dc-assistant [data-assistant-input]";
const QUESTION = "Its thread goes with it. The coders it steers are released and yours again, the shared memory is kept.";

// newState reads the column head's new assistant control: with several
// coders a dropdown whose rows start one each, with one coder a plain button.
function newState(page) {
  return page.evaluate(() => {
    const shown = (node) => Boolean(node) && node.getClientRects().length > 0 && getComputedStyle(node).visibility !== "hidden";
    const toggle = document.querySelector(".dc-app > .dc-ctx .dc-ctx-head [data-assistant-new-label]");
    const menu = toggle?.matches('[data-bs-toggle="dropdown"]') ? toggle.nextElementSibling : null;
    const rows = menu ? [...menu.querySelectorAll("[data-assistant-new]")].filter(shown) : [];
    const active = document.activeElement;
    const first = rows[0] || toggle;
    return {
      dropdown: Boolean(menu),
      open: Boolean(menu) && menu.classList.contains("show") && shown(menu),
      first: Boolean(first) && active === first,
      coder: first?.getAttribute("data-assistant-new") || "",
      inBox: Boolean(active?.matches?.("dc-assistant [data-assistant-input]")),
      value: document.querySelector("dc-assistant [data-assistant-input]")?.value ?? null,
    };
  });
}

function dialogState(page) {
  return page.evaluate(() => {
    const popup = document.querySelector(".swal2-popup");
    return {
      shown: Boolean(popup) && popup.getClientRects().length > 0,
      title: document.querySelector(".swal2-title")?.textContent.trim() || "",
      text: document.querySelector(".swal2-html-container")?.textContent.trim() || "",
      confirm: document.querySelector(".swal2-confirm")?.textContent.trim() || "",
    };
  });
}

// make starts an assistant the way the page's New posts, without a coder the
// server picks the one it starts on.
async function make(page) {
  const made = await page.evaluate(async () => {
    const body = new URLSearchParams({ form: "new", csrf_token: document.querySelector('meta[name="csrf-token"]').content });
    const res = await fetch("/assistants/new", { method: "POST", headers: { Accept: "application/json" }, body });
    return { status: res.status, json: await res.json().catch(() => ({})) };
  });
  assert(made.json.id, `the run could not start an assistant (${made.status}): ${JSON.stringify(made.json)}`);
  return made.json.id;
}

async function status(page, path) {
  return (await page.request.get(`${BASE}${path}`, { maxRedirects: 0 })).status();
}

async function keyChecks({ page, run, helpers }) {
  const { openAssistant, afterSwap, postForm, closePanel } = helpers;

  await run("Ctrl+T and Cmd+T open the new assistant control on its first coder from the message box, Escape gives the box back, Enter starts one", async () => {
    const home = await openAssistant(page);
    await page.fill(BOX, "");
    await page.focus(BOX);
    await page.keyboard.type("draft ");
    let st = await newState(page);
    const menuOpen = () => page.waitForFunction(() => {
      const toggle = document.querySelector(".dc-app > .dc-ctx .dc-ctx-head [data-assistant-new-label]");
      const menu = toggle?.nextElementSibling;
      return Boolean(menu?.classList.contains("show")) && menu.getClientRects().length > 0;
    }, null, { timeout: 5000 });
    const menuClosed = () => page.waitForFunction(() => !document.querySelector(".dc-app > .dc-ctx .dc-ctx-head .dropdown-menu.show"), null, { timeout: 5000 });
    for (const key of ["Control+t", "Meta+t"]) {
      await page.keyboard.press(key);
      if (st.dropdown) await menuOpen();
      st = await newState(page);
      assert(st.first && (!st.dropdown || st.open), `${key} did not open the control on its first coder: ${JSON.stringify(st)}`);
      assert(st.value === "draft ", `${key} reached the message box: ${JSON.stringify(st.value)}`);
      if (!st.dropdown) {
        await page.focus(BOX);
        continue;
      }
      await page.keyboard.press("Escape");
      await menuClosed();
      st = await newState(page);
      assert(!st.open && st.inBox && st.value === "draft ", `Escape did not give the message box the keyboard back: ${JSON.stringify(st)}`);
    }
    await page.keyboard.type("on");
    st = await newState(page);
    assert(st.value === "draft on", `typing after the keys reads ${JSON.stringify(st.value)}`);

    await page.keyboard.press("Control+t");
    if (st.dropdown) await menuOpen();
    st = await newState(page);
    const coder = st.coder;
    await afterSwap(page, () => page.keyboard.press("Enter"));
    const fresh = new URL(page.url()).pathname.split("/").pop();
    assert(fresh && fresh !== home, `Enter did not land on a new assistant: ${page.url()}`);
    assert((await page.locator("dc-assistant").getAttribute("assistant-id")) === fresh, "the surface shows another assistant than the address");
    if (coder) assert((await page.locator("dc-assistant").getAttribute("data-assistant-coder")) === coder, `the new assistant runs on another coder than the first row's ${coder}`);
    assert(await page.locator(`.dc-app > .dc-ctx [data-assistant-instance="${fresh}"].active`).count() === 1, "the new assistant has no marked row");

    await openAssistant(page, home);
    const gone = await postForm(page, fresh, { form: "delete" });
    assert(gone.status < 300, `deleting the new assistant answered ${gone.status}`);
    await page.fill(BOX, "");
    await sleep(1200);
    await closePanel(page);
    return st.dropdown ? `both keys opened the menu on ${coder}, Escape gave the box back, Enter started one` : "one coder: both keys focused the button, Enter started one";
  });

  await run("Ctrl+Shift+X asks Delete's question and Escape deletes nothing, a repeat or an open menu asks nothing, Cmd+Shift+X with the confirm deletes and lands like Delete", async () => {
    await openAssistant(page);
    const victim = await make(page);
    await openAssistant(page, victim);
    const row = `.dc-app > .dc-ctx [data-assistant-instance="${victim}"]`;
    const name = await page.locator(row).getAttribute("data-assistant-name");
    await page.focus(BOX);
    await page.keyboard.type("keep");

    await page.keyboard.press("Control+Shift+X");
    await page.waitForSelector(".swal2-popup", { state: "visible", timeout: 5000 });
    const asked = await dialogState(page);
    assert(asked.title === (name ? `Delete "${name}"?` : "Delete this assistant?"), `the question reads ${JSON.stringify(asked.title)}`);
    assert(asked.text === QUESTION && asked.confirm === "Delete", `the question is not Delete's: ${JSON.stringify(asked)}`);
    await page.keyboard.press("Escape");
    await page.waitForSelector(".swal2-container", { state: "detached", timeout: 5000 });
    await sleep(500);
    assert(new URL(page.url()).pathname === `/assistants/${victim}`, `Escape moved the page to ${page.url()}`);
    assert(await page.locator(row).count() === 1 && await status(page, `/assistants/${victim}`) === 200, "Escape on the question deleted the assistant");
    assert((await page.inputValue(BOX)) === "keep", `the message box reads ${JSON.stringify(await page.inputValue(BOX))}`);

    await page.focus(BOX);
    await page.evaluate((box) => document.querySelector(box).dispatchEvent(new KeyboardEvent("keydown", { key: "X", ctrlKey: true, shiftKey: true, repeat: true, bubbles: true, cancelable: true })), BOX);
    await sleep(400);
    assert(!(await dialogState(page)).shown, "a key repeat asked the question");

    await page.click(`${row} [data-assistant-menu]`);
    await page.waitForSelector(".dc-context-menu", { timeout: 5000 });
    await page.keyboard.press("Control+Shift+X");
    await sleep(400);
    assert(!(await dialogState(page)).shown, "the key asked the question over an open row menu");
    await page.keyboard.press("Escape");
    await page.waitForSelector(".dc-context-menu", { state: "detached", timeout: 5000 });

    await page.focus(BOX);
    await page.keyboard.press("Meta+Shift+X");
    await page.waitForSelector(".swal2-popup", { state: "visible", timeout: 5000 });
    assert((await dialogState(page)).title === asked.title, "Cmd+Shift+X asked another question");
    await L.confirmSwal(page);
    await page.waitForFunction(
      (id) => (document.querySelector("dc-assistant[ready]") && document.querySelector("dc-assistant").getAttribute("assistant-id") !== id)
        || Boolean(document.querySelector("[data-assistant-none]")),
      victim,
      { timeout: 15000 },
    );
    if (await page.locator(READY).count()) await page.waitForSelector(READY, { timeout: 15000 });
    const landed = new URL(page.url()).pathname;
    const entry = await page.evaluate(async () => new URL((await fetch("/assistants", { headers: { Accept: "text/html" } })).url).pathname);
    assert(landed === entry, `the page landed on ${landed}, the bare address leads to ${entry}`);
    assert(await page.locator(row).count() === 0, "the deleted assistant is still in the column");
    assert(await status(page, `/assistants/${victim}`) === 404, "the deleted assistant's page still answers");
    await closePanel(page);
    return `asked "${asked.title}", Escape kept it, the confirm deleted it and landed on ${landed}`;
  });
}

module.exports = { keyChecks };
