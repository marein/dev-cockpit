const http = require("http");
const https = require("https");
const L = require("./lib");
const { assert, BASE, sleep } = L;

// The card under a message the browser extension sent, in the assistant's
// thread (chat_picked.gohtml, internal/assistant picked.go). The message and
// card are built by @dc/browser-context in the cockpit's page from a pick on
// localhost:PAGE_PORT/card, where no page runs, and sent with a token of the
// runner's own. Instance with tests/e2e/fakes first on PATH and a scratch
// HOME, so the assistant is the fake.

process.env.NODE_TLS_REJECT_UNAUTHORIZED = "0";
const PAGE_PORT = Number(process.env.PAGE_PORT || 3304);
const TAG = Date.now().toString(36).slice(-5);
const SITE = `localhost:${PAGE_PORT}`;
const CARD_URL = `http://${SITE}/card`;
const ORIGIN = new URL(BASE).origin;
const NOTE = `The button ${TAG} needs more room.\nAnd why the errors?`;

function postAs(page, url, body) {
  return page.evaluate(async ({ url, body }) => {
    const token = document.querySelector('meta[name="csrf-token"]')?.content || "";
    const res = await fetch(url, { method: "POST", headers: { "X-CSRF-Token": token, Accept: "application/json" }, body: new URLSearchParams(body) });
    return { ok: res.ok, status: res.status, text: await res.text() };
  }, { url, body });
}

function request(method, path, { token = "", body = null, type = "" } = {}) {
  const url = new URL(path, BASE);
  const lib = url.protocol === "https:" ? https : http;
  return new Promise((resolve) => {
    const headers = { Accept: "application/json" };
    if (token) headers.Authorization = `Bearer ${token}`;
    if (body) Object.assign(headers, { "Content-Type": type, "Content-Length": Buffer.byteLength(body) });
    const req = lib.request(url, { method, headers, rejectUnauthorized: false }, (res) => {
      let raw = "";
      res.setEncoding("utf8");
      res.on("data", (chunk) => { raw += chunk; });
      res.on("end", () => {
        let json = null;
        try { json = JSON.parse(raw); } catch {}
        resolve({ status: res.statusCode, text: raw, json });
      });
    });
    req.on("error", (e) => resolve({ status: 0, text: e.message, json: null }));
    req.end(body || undefined);
  });
}

const apiPost = (token, path, fields) => request("POST", path, { token, body: new URLSearchParams(fields).toString(), type: "application/x-www-form-urlencoded" });

function apiPicture(token, fields, name, bytes) {
  const boundary = `----card${TAG}`;
  const parts = Object.entries(fields).map(([k, v]) => Buffer.from(`--${boundary}\r\nContent-Disposition: form-data; name="${k}"\r\n\r\n${v}\r\n`));
  parts.push(Buffer.from(`--${boundary}\r\nContent-Disposition: form-data; name="image"; filename="${name}"\r\nContent-Type: image/png\r\n\r\n`), bytes, Buffer.from(`\r\n--${boundary}--\r\n`));
  return request("POST", "/api/browser-extension/pictures", { token, body: Buffer.concat(parts), type: `multipart/form-data; boundary=${boundary}` });
}

async function makeToken(page, name) {
  const made = await postAs(page, "/settings/browser", { form: "new-token", name });
  const token = made.ok ? JSON.parse(made.text).token : "";
  assert(/^[A-Za-z0-9_-]{43}$/.test(token), `making the token answered ${made.status}`);
  return token;
}

function built(page, note, { logs = true, file = "" } = {}) {
  return page.evaluate(async ({ note, url, logs, file }) => {
    const ctx = await import("@dc/browser-context");
    const title = "Card <b>shop</b>";
    const viewport = { w: 1440, h: 900, dpr: 1 };
    const items = [ctx.readElement({
      title, tag: "button", id: "buy", selector: "#buy", viewport, box: { x: 40, y: 120, w: 160, h: 44 },
      text: 'Buy <img src=/card-pwned onerror="window.__pwned=1"> [a link](http://evil.invalid/md)',
      styles: [["display", "block"], ["font-family", "system-ui"], ["font-size", "15px"], ["font-weight", "600"], ["color", "rgb(255, 255, 255)"], ["background-color", "rgb(32, 107, 196)"]],
      html: '<button id="buy">Buy <img src=/card-pwned onerror="window.__pwned=1"></button>',
    }, url)];
    if (logs) {
      const at = Date.now();
      items.push(ctx.readLog({ kind: "log", all: true, url, title, viewport, entries: [
        { level: "error", kind: "console", message: "Cart failed to load", time: at },
        { level: "error", kind: "request", method: "GET", url: `${new URL(url).origin}/api/card-missing`, status: 404, message: "Not Found", time: at + 1 },
      ] }));
    }
    if (file) items.push({ ...ctx.readImage({ kind: "image", url, title, viewport, width: 640, height: 400, ratio: 1, pixels: true, marks: [{ kind: "pen", color: "red", target: { label: "button#buy", selector: "#buy" }, seen: true }], at: Date.now() }), file });
    return { message: ctx.contextMarkdown(note, items, "Picked with the browser extension"), card: JSON.stringify(ctx.contextCard(note, items)) };
  }, { note, url: CARD_URL, logs, file });
}

async function png(page) {
  const data = await page.evaluate(() => {
    const canvas = document.createElement("canvas");
    canvas.width = 640;
    canvas.height = 400;
    const g = canvas.getContext("2d");
    g.fillStyle = "#206bc4";
    g.fillRect(0, 0, 640, 400);
    g.strokeStyle = "#d63939";
    g.lineWidth = 6;
    g.strokeRect(40, 120, 160, 44);
    return canvas.toDataURL("image/png").split(",")[1];
  });
  return Buffer.from(data, "base64");
}

async function send(page, fx, id, note, { logs = true, draw = false } = {}) {
  let file = "";
  if (draw) {
    const picture = await apiPicture(fx.token, { to: "assistant", id }, `drawing-${TAG}.png`, await png(page));
    assert(picture.status === 200 && picture.json?.file, `the picture answered ${picture.status}: ${picture.text}`);
    file = picture.json.file;
  }
  const { message, card } = await built(page, note, { logs, file });
  const fields = [["to", "assistant"], ["id", id], ["message", message], ["card", card]];
  if (file) fields.push(["picture", file]);
  const sent = await apiPost(fx.token, "/api/browser-extension/send", fields);
  assert(sent.status === 200, `the send answered ${sent.status}: ${sent.text}`);
  return message;
}

function messages(page, id) {
  return page.evaluate(async (instance) => (await (await fetch(`/assistants/instances/${instance}?full=true`, { headers: { Accept: "application/json" } })).json()).messages, id);
}

function shown(page, selector) {
  return page.evaluate((sel) => {
    const el = document.querySelector(sel);
    if (!el) return false;
    const style = getComputedStyle(el);
    const box = el.getBoundingClientRect();
    return style.display !== "none" && style.visibility !== "hidden" && box.width > 0 && box.height > 0;
  }, selector);
}

async function waitShown(page, selector, want = true, timeout = 8000) {
  const until = Date.now() + timeout;
  while (Date.now() < until) {
    if ((await shown(page, selector)) === want) return;
    await sleep(100);
  }
  throw new Error(`${selector} is ${want ? "not shown" : "still shown"}`);
}

function card(page, root = "") {
  return page.evaluate((scope) => {
    const cards = document.querySelectorAll(`${scope} [data-picked]`);
    const el = cards[cards.length - 1];
    if (!el) return null;
    const seen = (node) => {
      if (!node) return false;
      const style = getComputedStyle(node);
      const box = node.getBoundingClientRect();
      return style.display !== "none" && style.visibility !== "hidden" && box.width > 0 && box.height > 0;
    };
    const fold = el.querySelector("[data-picked-fold]");
    const body = el.querySelector("[data-picked-body]");
    const bubble = el.closest("[data-assistant-message]");
    const rgb = getComputedStyle(el).backgroundColor.match(/\d+(\.\d+)?/g).slice(0, 3).map(Number);
    const wide = [...el.querySelectorAll("*")].filter((node) => { const b = node.getBoundingClientRect(); return b.width > 0 && b.right > innerWidth + 1; }).map((node) => node.tagName);
    return {
      id: bubble.dataset.messageId,
      state: bubble.dataset.state,
      queued: bubble.querySelector("[data-assistant-queued]")?.textContent.trim() || "",
      note: bubble.querySelector("[data-assistant-text]").innerText.replace(/\n+$/, ""),
      files: bubble.querySelectorAll("[data-assistant-file]").length,
      head: seen(fold),
      headY: fold.getBoundingClientRect().y,
      open: seen(body),
      expanded: fold.getAttribute("aria-expanded"),
      page: el.querySelector("[data-picked-page]").textContent.trim(),
      icon: fold.querySelector(".fw-medium i").className,
      chips: [...el.querySelectorAll("[data-picked-chip]")].map((chip) => ({ kind: chip.dataset.pickedChip, text: chip.textContent.trim(), seen: seen(chip) })),
      sections: [...el.querySelectorAll("[data-picked-section]")].map((s) => ({ kind: s.dataset.pickedSection, text: s.textContent.replace(/\s+/g, " "), lines: [...s.querySelectorAll("[data-picked-line]")].map((l) => l.dataset.pickedLine) })),
      pictures: [...el.querySelectorAll("img")].map((img) => ({ src: img.getAttribute("src"), width: img.naturalWidth, seen: seen(img) })),
      links: el.querySelectorAll("a, [href]").length,
      luma: (0.2126 * rgb[0] + 0.7152 * rgb[1] + 0.0722 * rgb[2]) / 255,
      wide,
      bubbleRight: bubble.getBoundingClientRect().right,
      right: el.getBoundingClientRect().right,
    };
  }, root);
}

async function waitCard(page, want, label, root = "", timeout = 15000) {
  const until = Date.now() + timeout;
  let seen = null;
  while (Date.now() < until) {
    seen = await card(page, root);
    if (seen && want(seen)) return seen;
    await sleep(150);
  }
  throw new Error(`${label}: ${JSON.stringify(seen)?.slice(0, 600)}`);
}

async function tapHead(page, { phone = false, root = "" } = {}) {
  const head = page.locator(`${root} [data-picked-fold]`).last();
  const box = await head.boundingBox();
  if (phone) await page.touchscreen.tap(box.x + 60, box.y + 14);
  else await page.mouse.click(box.x + 60, box.y + 14);
  await sleep(150);
}

function copyFrom(page, selector) {
  return page.evaluate(async (sel) => {
    window.__dcCopied = "";
    Object.defineProperty(navigator.clipboard, "writeText", { configurable: true, value: async (text) => { window.__dcCopied = text; } });
    [...document.querySelectorAll(sel)].pop().click();
    for (let i = 0; i < 40 && !window.__dcCopied; i++) await new Promise((r) => setTimeout(r, 50));
    return window.__dcCopied;
  }, selector);
}

async function openThread(page, id) {
  for (let i = 0; ; i++) {
    try {
      await page.goto(`${BASE}/assistants/${id}`, { waitUntil: "domcontentloaded" });
      break;
    } catch (e) {
      if (i > 2) throw e;
      await sleep(1000);
    }
  }
  await page.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
}

function wire(page, bag) {
  const fromPage = (url) => {
    try {
      const u = new URL(url);
      return u.origin !== ORIGIN && u.hostname === new URL(ORIGIN).hostname;
    } catch {
      return false;
    }
  };
  page.on("console", (m) => {
    if (m.type() !== "error") return;
    const where = m.location()?.url || "";
    if (fromPage(where) || /Content Security Policy|jsdelivr|cdn\./i.test(m.text())) bag.noise.push(`[${where}] ${m.text()}`);
    else bag.consoleErrors.push(`[${page.url()}] ${m.text()}`);
  });
  page.on("pageerror", (e) => {
    if (/due to access control checks/.test(e.message) && /\/(events|update\/check|costs\/status|git\/prompt|time|projects|ctx\/\w+|assistants\/[^/\s]+|share\/[^/\s]+\/[^\s]*)\b/.test(e.message)) bag.noise.push(e.message);
    else bag.pageErrors.push(`[${page.url()}] ${e.message}`);
  });
}

async function newAssistant(page, title) {
  const made = await postAs(page, "/assistants/new", { coder: "claude" });
  assert(made.ok, `making the assistant answered ${made.status}: ${made.text}`);
  const id = JSON.parse(made.text).id;
  const renamed = await postAs(page, `/assistants/${id}`, { form: "rename", title });
  assert(renamed.ok, `renaming the assistant answered ${renamed.status}`);
  return id;
}

async function desktop({ page, run, fx, engine }) {
  await run("a send from the extension shows the note and a folded card in the open thread, live, and the assistant reads the Markdown as sent", async () => {
    const chat = await page.context().newPage();
    wire(chat, fx.bag);
    fx.chat = chat;
    const requests = [];
    chat.on("request", (r) => requests.push(r.url()));
    fx.chatRequests = requests;
    await openThread(chat, fx.assistant);
    const want = await send(page, fx, fx.assistant, NOTE, { draw: true });
    assert(want.startsWith(`${NOTE}\n\n## Picked with the browser extension\n\nOne element and 2 console entries and a drawing of \`${CARD_URL}\``) && /- Picture: `drawing-[^`]+\.png`/.test(want), want.slice(0, 240));
    const sent = (await messages(page, fx.assistant)).filter((m) => m.role === "user");
    const message = sent[sent.length - 1];
    const at = [...want].findIndex((ch, i) => message?.content[i] !== ch);
    assert(message && message.content === want, `the assistant got ${JSON.stringify(message?.content.slice(Math.max(0, at - 80), at + 80))} for ${JSON.stringify(want.slice(Math.max(0, at - 80), at + 80))}`);
    fx.content = message.content;
    const seen = await waitCard(chat, (c) => c.note === NOTE && c.head, "the open thread shows no card under the note");
    assert(!seen.open && seen.expanded === "false", `the card stands unfolded: ${JSON.stringify(seen)}`);
    assert(seen.icon === "ti ti-puzzle me-1", `the card's head wears ${seen.icon}`);
    assert(seen.page.startsWith("Card <b>shop</b> · localhost:") && /\/card · \d+×\d+$/.test(seen.page), `the card's head says ${seen.page}`);
    const kinds = seen.chips.map((c) => `${c.kind}:${c.text}`);
    assert(seen.chips.length === 3 && seen.chips.every((c) => c.seen) && /^element:button#buy "Buy <img src=\/card-pwned/.test(kinds[0]) && kinds[1] === "error:2 errors" && kinds[2] === "image:Drawing, one mark", JSON.stringify(kinds));
    assert(seen.files === 0, `the drawing stands under the message too: ${seen.files} files`);
  });

  await run("a tap unfolds every chip under the card's head and a second folds it back", async () => {
    const chat = fx.chat;
    const before = await card(chat);
    await tapHead(chat);
    const open = await waitCard(chat, (c) => c.open && c.expanded === "true", "the card did not unfold");
    assert(Math.abs(open.headY - before.headY) <= 2, `the head moved from ${before.headY} to ${open.headY}`);
    const [element, log, image] = open.sections;
    assert(element.kind === "element" && /Selector #buy/.test(element.text) && /Styles display: block;/.test(element.text) && /HTML <button id="buy"/.test(element.text) && /Font system-ui 15px, 600/.test(element.text), element.text.slice(0, 400));
    assert(log.kind === "log" && log.lines.length === 2 && log.lines.every((k) => k === "error") && /GET http:\/\/localhost:\d+\/api\/card-missing 404/.test(log.text), log.text.slice(0, 400));
    assert(image.kind === "image" && /Pen over button#buy/.test(image.text), image.text.slice(0, 300));
    await waitCard(chat, (c) => c.pictures.length === 1 && c.pictures[0].width > 0 && c.pictures[0].seen, "the drawing's picture does not show");
    const pic = (await card(chat)).pictures[0];
    assert(pic.src.startsWith(`/assistants/${fx.assistant}/media/`), pic.src);
    await tapHead(chat);
    await waitCard(chat, (c) => !c.open && c.expanded === "false", "the card did not fold back");
  });

  await run("Copy as Markdown copies the whole message the assistant read", async () => {
    const chat = fx.chat;
    await tapHead(chat);
    await waitCard(chat, (c) => c.open, "the card did not unfold");
    const text = await copyFrom(chat, "[data-picked] [data-copy]");
    assert(text === fx.content, `copied ${JSON.stringify(text.slice(0, 200))}`);
    await tapHead(chat);
  });

  await run("the page's texts stay text in the card: no markup runs, nothing links or loads", async () => {
    const chat = fx.chat;
    await tapHead(chat);
    const seen = await waitCard(chat, (c) => c.open, "the card did not unfold");
    assert(seen.links === 0 && seen.pictures.length === 1, `the card holds ${seen.links} links and ${seen.pictures.length} pictures`);
    assert(seen.sections[0].text.includes('<img src=/card-pwned onerror="window.__pwned=1">') && seen.sections[0].text.includes("[a link](http://evil.invalid/md)"), seen.sections[0].text.slice(0, 300));
    const pwned = await chat.evaluate(() => window.__pwned);
    const strays = fx.chatRequests.filter((u) => /card-pwned|evil\.invalid/.test(u) || u.includes(`:${PAGE_PORT}`));
    assert(pwned === undefined && strays.length === 0, `the card ran or loaded the page's text: ${pwned} ${JSON.stringify(strays)}`);
    await tapHead(chat);
  });

  await run("after a reload the card stands folded under the note as before", async () => {
    const chat = fx.chat;
    await chat.reload({ waitUntil: "domcontentloaded" });
    await chat.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
    const seen = await waitCard(chat, (c) => c.head, "no card after the reload");
    assert(seen.note === NOTE && !seen.open && seen.chips.length === 3 && seen.files === 0, JSON.stringify(seen).slice(0, 400));
  });

  await run("a waiting message shows its card with Waiting, and unfolded it stays open when it goes out", async () => {
    const chat = fx.chat;
    const busy = await postAs(page, `/assistants/${fx.assistant}`, { form: "message", message: `SLOW ${TAG}` });
    assert(busy.ok, `the slow message answered ${busy.status}`);
    await send(page, fx, fx.assistant, `Waiting ${TAG}`);
    const waiting = await waitCard(chat, (c) => c.note === `Waiting ${TAG}`, "the waiting message shows no card");
    assert(waiting.state === "queued" && waiting.queued === "Waiting" && waiting.head && !waiting.open, JSON.stringify(waiting).slice(0, 300));
    await tapHead(chat);
    await waitCard(chat, (c) => c.open, "the waiting card did not unfold");
    const cancel = await postAs(page, `/assistants/${fx.assistant}`, { form: "cancel" });
    assert(cancel.ok, `the cancel answered ${cancel.status}`);
    const gone = await waitCard(chat, (c) => c.state !== "queued", "the waiting message never went out", "", 30000);
    await sleep(800);
    const after = await card(chat);
    assert(after.id === gone.id && after.open && after.expanded === "true" && after.queued === "", `the card after the message went out: ${JSON.stringify(after).slice(0, 300)}`);
    await chat.waitForFunction(() => !document.querySelector("dc-assistant")?.hasAttribute("running") && ![...document.querySelectorAll('[data-assistant-message][data-state="streaming"]')].length, null, { timeout: 30000 }).catch(() => {});
  });

  await run("every other message renders as before, a card that does not fit its message is refused", async () => {
    const chat = fx.chat;
    const raw = `Typed ${TAG}\n\n## Picked in the Browser\n\nOne element.`;
    const plain = await postAs(page, `/assistants/${fx.assistant}`, { form: "message", message: raw });
    assert(plain.ok, `a message without a card answered ${plain.status}`);
    await chat.waitForFunction((text) => [...document.querySelectorAll('[data-assistant-message][data-role="user"]')].some((n) => n.querySelector("[data-assistant-text]").innerText === text), raw, { timeout: 15000 });
    const bare = await chat.evaluate((text) => { const n = [...document.querySelectorAll('[data-assistant-message][data-role="user"]')].find((m) => m.querySelector("[data-assistant-text]").innerText === text); return n.querySelectorAll("[data-picked]").length; }, raw);
    assert(bare === 0, "a message without a card shows one");
    const cardJSON = (note) => JSON.stringify({ note, chips: [{ kind: "element", text: "x" }], sections: [{ kind: "element", title: "1. x" }] });
    for (const [what, card] of [
      ["a note the message does not begin with", cardJSON("Something else")],
      ["an unknown kind", cardJSON(`Typed ${TAG}`).replace('"kind":"element","title"', '"kind":"script","title"')],
      ["a card past its bound", JSON.stringify({ note: "", chips: [], sections: [{ kind: "element", title: "x".repeat(70000) }] })],
    ]) {
      const refused = await apiPost(fx.token, "/api/browser-extension/send", { to: "assistant", id: fx.assistant, message: raw, card });
      assert(refused.status === 400 && /^The card /.test(refused.json?.error || ""), `${what} answered ${refused.status}: ${refused.text.slice(0, 120)}`);
    }
  });

  await run("a guest of an assistant share link sees the same card, read only", async () => {
    const made = await postAs(page, "/settings/sharing/new", [["amount", "1"], ["unit", "hours"], ["name", `card-${TAG}`], ["assistant", fx.assistant]]);
    const link = (made.text.match(/value="([^"]*\/share\/[^"]+)"/) || [])[1];
    assert(made.ok && link, `making the link answered ${made.status}`);
    fx.linkId = await page.evaluate(async (id) => {
      const html = await (await fetch("/settings/sharing", { headers: { Accept: "text/html" } })).text();
      const doc = new DOMParser().parseFromString(html, "text/html");
      return [...doc.querySelectorAll("[data-share-row]")].find((row) => row.querySelector(`[data-share-assistant="${id}"]`))?.dataset.shareRow || "";
    }, fx.assistant);
    const guestCtx = await page.context().browser().newContext({ ignoreHTTPSErrors: true, viewport: { width: 1440, height: 900 } });
    try {
      const guest = await guestCtx.newPage();
      wire(guest, fx.bag);
      await guest.goto(link.replace(/^https?:\/\/[^/]+/, ORIGIN), { waitUntil: "domcontentloaded" });
      await guest.waitForSelector("dc-share-chat [data-picked]", { timeout: 15000 });
      const first = await guest.evaluate(() => [...document.querySelectorAll("dc-share-chat [data-picked]")].findIndex((n) => n.closest("[data-assistant-message]").querySelector("[data-assistant-text]").textContent.startsWith("The button")));
      assert(first >= 0, "the guest sees no card of the extension's message");
      const sel = `dc-share-chat [data-assistant-message]:has([data-picked]) >> nth=${first}`;
      const bubble = guest.locator(sel);
      await bubble.scrollIntoViewIfNeeded();
      const head = await bubble.locator("[data-picked-fold]").boundingBox();
      const folded = await bubble.evaluate((n) => ({ note: n.querySelector("[data-assistant-text]").textContent, body: n.querySelector("[data-picked-body]").getBoundingClientRect().height, chips: [...n.querySelectorAll("[data-picked-chip]")].map((c) => c.textContent.trim()) }));
      assert(folded.note.startsWith("The button") && folded.body === 0 && folded.chips.length === 3, JSON.stringify(folded));
      await guest.mouse.click(head.x + 60, head.y + 14);
      await guest.waitForFunction((i) => { const b = [...document.querySelectorAll("dc-share-chat [data-picked]")][i].querySelector("[data-picked-body]"); return b.getBoundingClientRect().height > 100; }, first, { timeout: 5000 });
      const open = await bubble.evaluate((n) => ({ img: n.querySelector("[data-picked] img")?.getAttribute("src") || "", loaded: n.querySelector("[data-picked] img")?.naturalWidth || 0, links: n.querySelectorAll("[data-picked] a").length, forms: n.querySelectorAll("[data-picked] form, [data-picked] input, [data-picked] textarea").length }));
      for (let i = 0; i < 30 && !open.loaded; i++) { await sleep(150); open.loaded = await bubble.evaluate((n) => n.querySelector("[data-picked] img")?.naturalWidth || 0); }
      assert(/^\/share\/[^/]+\/assistants\//.test(open.img) && open.loaded > 0 && open.links === 0 && open.forms === 0, JSON.stringify(open));
      const text = await guest.evaluate(async (i) => {
        window.__dcCopied = "";
        Object.defineProperty(navigator.clipboard, "writeText", { configurable: true, value: async (t) => { window.__dcCopied = t; } });
        [...document.querySelectorAll("dc-share-chat [data-picked]")][i].querySelector("[data-copy]").click();
        for (let k = 0; k < 40 && !window.__dcCopied; k++) await new Promise((r) => setTimeout(r, 50));
        return window.__dcCopied;
      }, first);
      assert(text === fx.content, `the guest copied ${JSON.stringify(text.slice(0, 120))}`);
    } finally {
      await guestCtx.close();
    }
  });
}

async function phone({ browser, engine, run, fx }) {
  for (const scheme of ["dark", "light"]) {
    await run(`phone 390x844, ${scheme}: a tap on the note's card unfolds it within the width under its head, the thread scrolls on, a tap folds it`, async () => {
      const ctx = await browser.newContext({ ignoreHTTPSErrors: true, hasTouch: true, isMobile: engine === "chromium", viewport: { width: 390, height: 844 }, colorScheme: scheme });
      const page = await ctx.newPage();
      wire(page, fx.bag);
      try {
        await L.login(page);
        await L.dismissUpdate(page);
        if (scheme === "dark") await send(page, fx, fx.assistant, `Phone ${TAG}`, { logs: false });
        await openThread(page, fx.assistant);
        await page.evaluate((note) => [...document.querySelectorAll("[data-assistant-message]")].find((n) => n.querySelector("[data-assistant-text]")?.textContent === note)?.scrollIntoView({ block: "center" }), `Phone ${TAG}`);
        await sleep(400);
        const before = await page.evaluate((note) => {
          const n = [...document.querySelectorAll("[data-assistant-message]")].find((m) => m.querySelector("[data-assistant-text]")?.textContent === note);
          const fold = n?.querySelector("[data-picked-fold]");
          return fold ? { y: fold.getBoundingClientRect().y, chips: [...n.querySelectorAll("[data-picked-chip]")].map((c) => c.textContent.trim()) } : null;
        }, `Phone ${TAG}`);
        assert(before && before.chips.length === 1 && /^button#buy/.test(before.chips[0]), `the phone's message shows ${JSON.stringify(before)}`);
        const head = await page.evaluate((note) => { const n = [...document.querySelectorAll("[data-assistant-message]")].find((m) => m.querySelector("[data-assistant-text]")?.textContent === note); return n.querySelector("[data-picked-fold]").getBoundingClientRect().toJSON(); }, `Phone ${TAG}`);
        await page.touchscreen.tap(head.x + 60, head.y + 14);
        await sleep(700);
        const open = await page.evaluate((note) => {
          const n = [...document.querySelectorAll("[data-assistant-message]")].find((m) => m.querySelector("[data-assistant-text]")?.textContent === note);
          const body = n.querySelector("[data-picked-body]");
          const style = getComputedStyle(body);
          const card = n.querySelector("[data-picked]");
          const rgb = getComputedStyle(card).backgroundColor.match(/\d+(\.\d+)?/g).slice(0, 3).map(Number);
          return {
            y: n.querySelector("[data-picked-fold]").getBoundingClientRect().y,
            shown: style.display !== "none" && body.getBoundingClientRect().height > 100,
            wide: [...card.querySelectorAll("*")].filter((x) => { const b = x.getBoundingClientRect(); return b.width > 0 && b.right > innerWidth + 1; }).length,
            sideways: document.documentElement.scrollWidth - innerWidth,
            luma: (0.2126 * rgb[0] + 0.7152 * rgb[1] + 0.0722 * rgb[2]) / 255,
          };
        }, `Phone ${TAG}`);
        assert(open.shown && Math.abs(open.y - before.y) <= 2 && open.wide === 0 && open.sideways <= 0, JSON.stringify({ before, open }));
        assert(scheme === "dark" ? open.luma < 0.3 : open.luma > 0.8, `the card's background has a luma of ${open.luma.toFixed(2)} in ${scheme}`);
        const scroller = await page.evaluate(() => { const s = document.querySelector("dc-assistant").scroller; return { top: s.scrollTop }; });
        if (engine === "chromium") {
          const cdp = await ctx.newCDPSession(page);
          const x = 200;
          let y = 600;
          await cdp.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ x, y }] });
          for (let i = 0; i < 10; i++) {
            y -= 25;
            await cdp.send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ x, y }] });
            await sleep(16);
          }
          await cdp.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
        } else {
          await page.mouse.move(200, 600);
          await page.mouse.wheel(0, 250);
        }
        await sleep(500);
        const moved = await page.evaluate(() => document.querySelector("dc-assistant").scroller.scrollTop);
        assert(moved > scroller.top + 100, `the thread scrolled from ${scroller.top} to ${moved}`);
        const again = await page.evaluate((note) => { const n = [...document.querySelectorAll("[data-assistant-message]")].find((m) => m.querySelector("[data-assistant-text]")?.textContent === note); return n.querySelector("[data-picked-fold]").getBoundingClientRect().toJSON(); }, `Phone ${TAG}`);
        if (again.y < 60) await page.evaluate((note) => [...document.querySelectorAll("[data-assistant-message]")].find((m) => m.querySelector("[data-assistant-text]")?.textContent === note).scrollIntoView({ block: "start" }), `Phone ${TAG}`);
        const at = await page.evaluate((note) => { const n = [...document.querySelectorAll("[data-assistant-message]")].find((m) => m.querySelector("[data-assistant-text]")?.textContent === note); return n.querySelector("[data-picked-fold]").getBoundingClientRect().toJSON(); }, `Phone ${TAG}`);
        await page.touchscreen.tap(at.x + 60, at.y + 14);
        await page.waitForFunction((note) => { const n = [...document.querySelectorAll("[data-assistant-message]")].find((m) => m.querySelector("[data-assistant-text]")?.textContent === note); return n.querySelector("[data-picked-body]").getBoundingClientRect().height === 0; }, `Phone ${TAG}`, { timeout: 5000 });
      } finally {
        await ctx.close();
      }
    });
  }
}

async function looks({ browser, engine, run, fx }) {
  for (const [name, viewport, touch] of [["desktop 1440x900", { width: 1440, height: 900 }, false], ["tablet 834x1194", { width: 834, height: 1194 }, true]]) {
    for (const scheme of ["dark", "light"]) {
      await run(`${name}, ${scheme}: the card stands inside its bubble in the cockpit's colors and unfolds within it`, async () => {
        const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport, hasTouch: touch, isMobile: touch && engine === "chromium", colorScheme: scheme });
        const page = await ctx.newPage();
        wire(page, fx.bag);
        try {
          await L.login(page);
          await L.dismissUpdate(page);
          await openThread(page, fx.assistant);
          await page.evaluate(() => { const n = [...document.querySelectorAll("[data-picked]")].find((c) => c.closest("[data-assistant-message]").querySelector("[data-assistant-text]").textContent.startsWith("The button")); n.scrollIntoView({ block: "start" }); n.setAttribute("data-card-under-test", ""); });
          await sleep(300);
          const fold = page.locator("[data-card-under-test] [data-picked-fold]");
          await (touch ? fold.tap() : fold.click());
          await page.waitForFunction(() => document.querySelector("[data-card-under-test] [data-picked-body]").getBoundingClientRect().height > 100, null, { timeout: 5000 });
          const look = await page.evaluate(() => {
            const cardEl = document.querySelector("[data-card-under-test]");
            const bubble = cardEl.closest("[data-assistant-message]").getBoundingClientRect();
            const rgb = getComputedStyle(cardEl).backgroundColor.match(/\d+(\.\d+)?/g).slice(0, 3).map(Number);
            const wide = [...cardEl.querySelectorAll("*")].filter((x) => { const b = x.getBoundingClientRect(); return b.width > 0 && b.right > bubble.right + 1; }).length;
            return { right: cardEl.getBoundingClientRect().right, bubble: bubble.right, wide, luma: (0.2126 * rgb[0] + 0.7152 * rgb[1] + 0.0722 * rgb[2]) / 255, sideways: document.documentElement.scrollWidth - innerWidth };
          });
          assert(look.right <= look.bubble + 1 && look.wide === 0 && look.sideways <= 0, JSON.stringify(look));
          assert(scheme === "dark" ? look.luma < 0.3 : look.luma > 0.8, `the card's background has a luma of ${look.luma.toFixed(2)} in ${scheme}`);
        } finally {
          await ctx.close();
        }
      });
    }
  }
}

async function docs({ page, run }) {
  await run("the docs say how a send shows in the assistant's thread", async () => {
    await page.goto(`${BASE}/docs#browser`, { waitUntil: "domcontentloaded" });
    const text = await page.evaluate(() => document.body.innerText);
    for (const want of ["In the assistant's thread a send shows your note with a folded card", "Copy as Markdown copies the whole message", "The assistant reads the Markdown"]) {
      assert(text.includes(want), `the docs lack ${want}`);
    }
  });
}

(async () => {
  const results = [];
  const bag = { consoleErrors: [], pageErrors: [], cdnNoise: [], noise: [] };
  const engines = L.engineList();
  const made = [];
  for (const engine of engines) {
    const browser = await L.launch(engine);
    const ctx = await L.newDesktop(browser, engine);
    await ctx.setViewportSize?.({ width: 1440, height: 900 });
    const page = await ctx.newPage();
    await page.setViewportSize({ width: 1440, height: 900 });
    wire(page, bag);
    const fx = { bag, title: `Card ${engine} ${TAG}` };
    const run = async (name, fn) => {
      if (!(process.env.CHECK ? process.env.CHECK.split(",").some((t) => name.toLowerCase().includes(t.trim().toLowerCase())) : true)) return;
      const t0 = Date.now();
      try { const d = await fn(); results.push({ name: `[${engine}] ${name}`, status: "PASS", detail: d || "", ms: Date.now() - t0 }); }
      catch (e) { results.push({ name: `[${engine}] ${name}`, status: "FAIL", detail: e.message.split("\n")[0], ms: Date.now() - t0 }); }
    };
    try {
      await L.login(page);
      await L.dismissUpdate(page);
      fx.assistant = await newAssistant(page, fx.title);
      made.push(fx.assistant);
      fx.token = await makeToken(page, `Card ${engine} ${TAG}`);
      await desktop({ page, run, fx, engine });
      await phone({ browser, engine, run, fx });
      await looks({ browser, engine, run, fx });
      if (engine === engines[0]) await docs({ page, run });
    } catch (e) {
      results.push({ name: `[${engine}] FATAL`, status: "FAIL", detail: e.message });
    } finally {
      try {
        await fx.chat?.close();
        if (fx.linkId) await postAs(page, `/settings/sharing/${fx.linkId}`, { form: "revoke" });
        const tokens = await page.evaluate(async (name) => {
          const html = await (await fetch("/settings/browser", { headers: { Accept: "text/html" } })).text();
          return [...new DOMParser().parseFromString(html, "text/html").querySelectorAll("[data-browser-extension-token]")].filter((row) => row.querySelector("[data-browser-extension-token-name]").textContent === name).map((row) => row.dataset.browserExtensionToken);
        }, `Card ${engine} ${TAG}`);
        for (const id of tokens) await postAs(page, "/settings/browser", { form: "revoke-token", id });
        if (engine === engines.slice(-1)[0]) {
          await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
          for (const id of made) await postAs(page, `/assistants/${id}`, { form: "delete" });
          await sleep(2000);
        }
      } catch (e) {
        results.push({ name: "cleanup", status: "FAIL", detail: e.message });
      }
      await ctx.close().catch(() => {});
      await browser.close().catch(() => {});
    }
  }
  bag.cdnNoise = bag.cdnNoise.concat(bag.noise);
  process.exit(L.report("BROWSER CARD", results, bag) ? 1 : 0);
})();
