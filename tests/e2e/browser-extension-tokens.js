const http = require("http");
const https = require("https");
const L = require("./lib");
const { assert, BASE, sleep } = L;

// The browser extension's tokens and the routes they open
// (internal/browserextension, Settings › Browser with its dialogs in
// dc-browser-extension-tokens, internal/web browserextensionapi.go). The
// message and the card are built by @dc/browser-context in the cockpit's page,
// the format the extension sends. Instance with tests/e2e/fakes first on PATH
// and a scratch HOME, so the assistant and the coder are the fakes, and the
// local API socket mounted (APISOCK, default /apisock/s).

process.env.NODE_TLS_REJECT_UNAUTHORIZED = "0";
const APISOCK = process.env.APISOCK || "/apisock/s";
const TAG = Date.now().toString(36).slice(-5);
const PROJECT = `ext-${TAG}`;
const PAGE_URL = "http://localhost:8095/cart";
const NOTE = `The button ${TAG} needs room.`;
const DIALOG = ".swal2-popup.dc-ask";

function request(method, path, { token = "", cookie = "", body = null, type = "", accept = "application/json" } = {}) {
  const url = new URL(path, BASE);
  const lib = url.protocol === "https:" ? https : http;
  return new Promise((resolve) => {
    const headers = { Accept: accept };
    if (token) headers.Authorization = `Bearer ${token}`;
    if (cookie) headers.Cookie = cookie;
    if (body) Object.assign(headers, { "Content-Type": type, "Content-Length": Buffer.byteLength(body) });
    const req = lib.request(url, { method, headers, rejectUnauthorized: false }, (res) => {
      let raw = "";
      res.setEncoding("utf8");
      res.on("data", (chunk) => { raw += chunk; });
      res.on("end", () => {
        let json = null;
        try { json = JSON.parse(raw); } catch {}
        resolve({ status: res.statusCode, headers: res.headers, text: raw, json });
      });
    });
    req.on("error", (e) => resolve({ status: 0, text: e.message, json: null, headers: {} }));
    req.end(body || undefined);
  });
}

const form = (fields) => new URLSearchParams(fields).toString();
const apiGet = (path, token) => request("GET", path, { token });
const apiPost = (path, token, fields) => request("POST", path, { token, body: form(fields), type: "application/x-www-form-urlencoded" });

function apiPicture(token, fields, name, bytes) {
  const boundary = `----ext${TAG}`;
  const parts = Object.entries(fields).map(([k, v]) => Buffer.from(`--${boundary}\r\nContent-Disposition: form-data; name="${k}"\r\n\r\n${v}\r\n`));
  parts.push(Buffer.from(`--${boundary}\r\nContent-Disposition: form-data; name="image"; filename="${name}"\r\nContent-Type: image/png\r\n\r\n`), bytes, Buffer.from(`\r\n--${boundary}--\r\n`));
  return request("POST", "/api/browser-extension/pictures", { token, body: Buffer.concat(parts), type: `multipart/form-data; boundary=${boundary}` });
}

function overSocket(method, path, fields = {}) {
  return new Promise((resolve) => {
    const data = form(fields);
    const req = http.request({ socketPath: APISOCK, path, method, headers: { "Content-Type": "application/x-www-form-urlencoded", "Content-Length": Buffer.byteLength(data), Accept: "application/json" } }, (res) => {
      let raw = "";
      res.on("data", (c) => { raw += c; });
      res.on("end", () => resolve({ status: res.statusCode, text: raw }));
    });
    req.on("error", () => resolve({ status: 0, text: "" }));
    req.end(data);
  });
}

function postAs(page, url, body) {
  return page.evaluate(async ({ url, body }) => {
    const token = document.querySelector('meta[name="csrf-token"]')?.content || "";
    const res = await fetch(url, { method: "POST", headers: { "X-CSRF-Token": token, Accept: "application/json" }, body: new URLSearchParams(body) });
    return { ok: res.ok, status: res.status, text: await res.text() };
  }, { url, body });
}

function seen(page, selector) {
  return page.evaluate((sel) => {
    const el = document.querySelector(sel);
    if (!el) return false;
    const style = getComputedStyle(el);
    const box = el.getBoundingClientRect();
    return style.display !== "none" && style.visibility !== "hidden" && box.width > 0 && box.height > 0;
  }, selector);
}

async function waitSeen(page, selector, want = true, timeout = 8000) {
  const until = Date.now() + timeout;
  while (Date.now() < until) {
    if ((await seen(page, selector)) === want) return;
    await sleep(100);
  }
  throw new Error(`${selector} is ${want ? "not shown" : "still shown"}`);
}

const tap = (page, phone, selector) => (phone ? page.tap(selector) : page.click(selector));

async function sessionCookie(page) {
  const cookies = await page.context().cookies(BASE);
  return cookies.filter((c) => c.name === "tc_session" || c.name.endsWith("_session")).map((c) => `${c.name}=${c.value}`).join("; ");
}

function tokenRows(page) {
  return page.evaluate(() => [...document.querySelectorAll("[data-browser-extension-token]")].map((row) => {
    const box = row.getBoundingClientRect();
    const style = getComputedStyle(row);
    return { id: row.dataset.browserExtensionToken, name: row.querySelector("[data-browser-extension-token-name]").textContent, dates: row.querySelector("[data-browser-extension-token-dates]").textContent.replace(/\s+/g, " ").trim(), seen: style.display !== "none" && box.height > 0 };
  }));
}

// A goto that changes only the hash would keep the page that stands.
async function openTokens(page) {
  await page.goto(`${BASE}/settings/browser`, { waitUntil: "domcontentloaded" });
  await L.dismissUpdate(page);
  await page.waitForSelector("[data-browser-extension-token-new]");
  await page.waitForFunction(() => customElements.get("dc-browser-extension-tokens"));
  await page.locator("#settings-browser-tokens").scrollIntoViewIfNeeded();
}

async function askName(page, name, { phone = false } = {}) {
  await page.locator("[data-browser-extension-token-new]").scrollIntoViewIfNeeded();
  await tap(page, phone, "[data-browser-extension-token-new]");
  await waitSeen(page, `${DIALOG} .swal2-input`);
  const hint = await page.evaluate((d) => document.querySelector(`${d} .dc-ask-body`).innerText, DIALOG);
  if (phone) await page.tap(`${DIALOG} .swal2-input`);
  else await page.click(`${DIALOG} .swal2-input`);
  await page.keyboard.type(name);
  await page.keyboard.press("Enter");
  return hint;
}

const tapBeside = (page, phone) => (phone ? page.touchscreen.tap(5, 5) : page.mouse.click(5, 5));

async function makeToken(page, name, { phone = false } = {}) {
  await openTokens(page);
  const hint = await askName(page, name, { phone });
  await waitSeen(page, `${DIALOG} .dc-ask-code pre`, true, 10000);
  const shown = await page.evaluate((d) => {
    const dialog = document.querySelector(d);
    const value = dialog.querySelector(".dc-ask-code pre");
    const box = value.getBoundingClientRect();
    const popup = dialog.getBoundingClientRect();
    return {
      section: document.querySelector("#settings-browser-tokens").innerText,
      token: value.textContent,
      title: dialog.querySelector(".dc-ask-title").textContent,
      once: dialog.querySelector(".dc-ask-note").innerText,
      copy: dialog.querySelector(".swal2-deny").textContent,
      done: dialog.querySelector(".swal2-confirm").textContent,
      cancel: getComputedStyle(dialog.querySelector(".swal2-cancel")).display,
      inside: box.left >= popup.left && box.right <= popup.right && popup.left >= 0 && popup.right <= innerWidth,
      sideways: document.documentElement.scrollWidth - innerWidth,
    };
  }, DIALOG);
  shown.hint = hint;
  shown.copied = await page.evaluate(async (d) => {
    window.__dcCopied = "";
    Object.defineProperty(navigator.clipboard, "writeText", { configurable: true, value: async (text) => { window.__dcCopied = text; } });
    return null;
  }, DIALOG);
  await tap(page, phone, `${DIALOG} .swal2-deny`);
  for (let i = 0; i < 40 && !shown.copied; i++) {
    shown.copied = await page.evaluate(() => window.__dcCopied);
    if (!shown.copied) await sleep(50);
  }
  await tapBeside(page, phone);
  await sleep(300);
  shown.kept = await seen(page, `${DIALOG} .dc-ask-code pre`);
  await tap(page, phone, `${DIALOG} .swal2-confirm`);
  await page.waitForSelector(".swal2-container", { state: "detached", timeout: 5000 });
  await page.waitForFunction((n) => location.pathname === "/settings/browser" && [...document.querySelectorAll("[data-browser-extension-token-name]")].some((x) => x.textContent === n), name, { timeout: 10000 });
  return shown;
}

async function revoke(page, name, { phone = false, cancel = false } = {}) {
  const row = page.locator("[data-browser-extension-token]", { has: page.locator("[data-browser-extension-token-name]", { hasText: name }) });
  await row.scrollIntoViewIfNeeded();
  const button = row.locator("[data-browser-extension-token-revoke]");
  await (phone ? button.tap() : button.click());
  await waitSeen(page, ".swal2-popup");
  const asked = await page.evaluate(() => {
    const popup = document.querySelector(".swal2-popup");
    return { text: popup.innerText, danger: popup.matches(".dc-ask.dc-ask-danger"), warning: popup.querySelector(".dc-ask-warning")?.innerText.trim() || "", button: popup.querySelector(".swal2-confirm").className };
  });
  const answer = cancel ? ".swal2-cancel" : ".swal2-confirm";
  await sleep(150);
  await tap(page, phone, answer);
  await page.waitForSelector(".swal2-container", { state: "detached", timeout: 5000 }).catch(() => {});
  if (!cancel) await page.waitForFunction((n) => ![...document.querySelectorAll("[data-browser-extension-token-name]")].some((x) => x.textContent === n), name, { timeout: 10000 });
  return asked;
}

function built(page, note, file = "") {
  return page.evaluate(async ({ note, url, file }) => {
    const ctx = await import("@dc/browser-context");
    const element = ctx.readElement({ title: "Shop <b>cart</b>", tag: "button", id: "buy", classes: ["primary"], selector: "#buy", role: "button", name: "Buy", text: "Buy <img src=/ext-pwned onerror=\"window.__pwned=1\">", viewport: { w: 1440, h: 900, dpr: 1 }, box: { x: 10, y: 20, w: 80, h: 30 }, styles: [["display", "block"], ["color", "rgb(0, 0, 0)"]], html: "<button id=\"buy\">Buy</button>", attrs: [["data-testid", "buy"]] }, url);
    const items = [element];
    if (file) items.push({ ...ctx.readImage({ kind: "image", url, title: "Shop <b>cart</b>", viewport: { w: 1440, h: 900 }, width: 40, height: 20, ratio: 1, pixels: true, marks: [{ kind: "rect", color: "red", target: { label: "button#buy", selector: "#buy" }, seen: true }], at: Date.now() }), file });
    return { message: ctx.contextMarkdown(note, items, "Picked with the browser extension"), card: JSON.stringify(ctx.contextCard(note, items)) };
  }, { note, url: PAGE_URL, file });
}

async function png(page) {
  const data = await page.evaluate(() => {
    const canvas = document.createElement("canvas");
    canvas.width = 40;
    canvas.height = 20;
    const g = canvas.getContext("2d");
    g.fillStyle = "#c00";
    g.fillRect(0, 0, 40, 20);
    return canvas.toDataURL("image/png").split(",")[1];
  });
  return Buffer.from(data, "base64");
}

function messages(page, id) {
  return page.evaluate(async (instance) => (await (await fetch(`/assistants/instances/${instance}?full=true`, { headers: { Accept: "application/json" } })).json()).messages, id);
}

async function openThread(page, id) {
  await page.goto(`${BASE}/assistants/${id}`, { waitUntil: "domcontentloaded" });
  await page.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
}

function cardOf(page, note, root = "") {
  return page.evaluate(({ scope, note }) => {
    const el = [...document.querySelectorAll(`${scope} [data-picked]`)].find((card) => card.closest("[data-assistant-message]").querySelector("[data-assistant-text]").innerText.trim() === note);
    if (!el) return null;
    const vis = (n) => { if (!n) return false; const s = getComputedStyle(n); const b = n.getBoundingClientRect(); return s.display !== "none" && s.visibility !== "hidden" && b.width > 0 && b.height > 0; };
    const bubble = el.closest("[data-assistant-message]");
    const via = el.querySelector("[data-picked-via]");
    const head = el.querySelector("[data-picked-fold] .fw-medium");
    return {
      note: bubble.querySelector("[data-assistant-text]").innerText.trim(),
      state: bubble.dataset.state,
      queued: bubble.querySelector("[data-assistant-queued]")?.textContent.trim() || "",
      head: head ? head.innerText.trim() : "",
      icon: head?.querySelector("i")?.className || "",
      via: via ? via.textContent.trim() : "",
      viaSeen: vis(via),
      open: vis(el.querySelector("[data-picked-body]")),
      pictures: [...el.querySelectorAll("img")].map((img) => ({ src: img.getAttribute("src"), width: img.naturalWidth, seen: vis(img) })),
      links: el.querySelectorAll("a").length,
      text: el.textContent.replace(/\s+/g, " "),
    };
  }, { scope: root, note });
}

async function waitCard(page, note, want, label, root = "", timeout = 15000) {
  const until = Date.now() + timeout;
  let got = null;
  while (Date.now() < until) {
    got = await cardOf(page, note, root);
    if (got && want(got)) return got;
    await sleep(150);
  }
  throw new Error(`${label}: ${JSON.stringify(got)?.slice(0, 500)}`);
}

async function newAssistant(page, title) {
  const made = await postAs(page, "/assistants/new", { coder: "claude" });
  assert(made.ok, `making the assistant answered ${made.status}`);
  const id = JSON.parse(made.text).id;
  await postAs(page, `/assistants/${id}`, { form: "rename", title });
  return id;
}

async function management({ page, run, fx, engine }) {
  await run("Settings › Browser says a token also starts new assistants, the dialog asks one per browser, New token in it makes a named token and shows it once with Copy and a sentence that only its hash stays", async () => {
    const shown = await makeToken(page, fx.name);
    assert(shown.section.includes("With a token the extension lists the assistants and running coders, starts new assistants and sends to them."), `the section says ${shown.section}`);
    assert(shown.hint.includes("One token per browser, named after it.") && !shown.hint.includes("lists the assistants"), `the dialog says ${shown.hint}`);
    assert(/^[A-Za-z0-9_-]{43}$/.test(shown.token), `the dialog shows ${shown.token}`);
    assert(shown.title === fx.name && /^Shown only now, the cockpit keeps only its hash\.$/.test(shown.once.trim()) && shown.copy === "Copy" && shown.done === "Done" && shown.cancel === "none", JSON.stringify(shown));
    assert(shown.copied === shown.token, `Copy wrote ${shown.copied}`);
    assert(shown.kept, "Copy or a click beside it closed the token's dialog");
    fx.token = shown.token;
    const rows = await tokenRows(page);
    const row = rows.find((r) => r.name === fx.name);
    assert(row && row.seen && /^Created .+, never used$/.test(row.dates), JSON.stringify(rows));
    const html = await page.content();
    assert(!html.includes(fx.token), "the page shows the token again");
    await page.reload({ waitUntil: "domcontentloaded" });
    assert(!(await page.content()).includes(fx.token), "the page shows the token after a reload");
  });

  await run("a taken name is refused in the dialog with the server's sentence, what was typed stays and Cancel makes nothing", async () => {
    await openTokens(page);
    const before = (await tokenRows(page)).length;
    const taken = fx.name.toUpperCase();
    await askName(page, taken);
    await waitSeen(page, `${DIALOG} .swal2-validation-message`);
    const refused = await page.evaluate((d) => ({ said: document.querySelector(`${d} .swal2-validation-message`).innerText.trim(), typed: document.querySelector(`${d} .swal2-input`).value }), DIALOG);
    assert(refused.said === "A token with that name exists. Name each after its browser." && refused.typed === taken, JSON.stringify(refused));
    await page.click(`${DIALOG} .swal2-cancel`);
    await page.waitForSelector(".swal2-container", { state: "detached", timeout: 5000 });
    assert((await tokenRows(page)).length === before, "a refused name made a token");
  });

  await run("the token opens the extension's routes, and the list then says when it was used", async () => {
    const answer = await apiGet("/api/browser-extension/targets", fx.token);
    assert(answer.status === 200 && answer.headers["cache-control"] === "no-store" && !answer.headers["access-control-allow-origin"], `${answer.status} ${JSON.stringify(answer.headers)}`);
    const mine = answer.json.assistants.find((a) => a.id === fx.assistant);
    const coder = answer.json.coders.find((c) => c.id === fx.coder);
    assert(mine && mine.name === fx.title && Object.keys(mine).length === 2, JSON.stringify(answer.json.assistants));
    assert(coder && coder.project === PROJECT && Object.keys(coder).sort().join() === "id,name,project", JSON.stringify(answer.json.coders));
    const newWith = answer.json.assistantCoders;
    assert(Array.isArray(newWith) && newWith.some((c) => c.id === "claude" && c.name === "Claude") && newWith.every((c) => Object.keys(c).sort().join() === "id,name"), JSON.stringify(newWith));
    fx.newWith = newWith;
    await openTokens(page);
    const rows = await tokenRows(page);
    const row = rows.find((r) => r.name === fx.name);
    assert(row && /^Created .+, used .+/.test(row.dates), JSON.stringify(rows));
  });

  await run("a token and a session never cross: session routes answer a token as if none was sent, the extension's routes refuse a session", async () => {
    for (const path of ["/assistants/instances", "/settings/browser", `/assistants/${fx.assistant}`]) {
      const answer = await request("GET", path, { token: fx.token });
      assert(answer.status === 401 || (answer.status === 303 && /\/login/.test(answer.headers.location || "")), `${path} with the token answered ${answer.status}`);
    }
    const send = await request("POST", `/assistants/${fx.assistant}`, { token: fx.token, body: form({ form: "message", message: "x" }), type: "application/x-www-form-urlencoded" });
    assert(send.status === 401 || send.status === 403, `a session send with the token answered ${send.status}`);
    const cookie = await sessionCookie(page);
    assert(cookie, "no session cookie");
    for (const [method, path] of [["GET", "/api/browser-extension/version"], ["GET", "/api/browser-extension/targets"], ["POST", "/api/browser-extension/assistants"], ["POST", "/api/browser-extension/pictures"], ["POST", "/api/browser-extension/send"]]) {
      const answer = await request(method, path, { cookie });
      assert(answer.status === 401 && /The token is wrong or revoked\./.test(answer.json?.error || ""), `${path} with the session answered ${answer.status}: ${answer.text.slice(0, 120)}`);
    }
    const fromPage = await page.evaluate(async () => (await fetch("/api/browser-extension/targets", { credentials: "include" })).status);
    assert(fromPage === 401, `the cockpit's own page reached the targets with its session: ${fromPage}`);
    for (const bad of [`${fx.token}x`, fx.token.toUpperCase(), "A".repeat(43)]) {
      const answer = await apiGet("/api/browser-extension/targets", bad);
      assert(answer.status === 401 && !answer.text.includes(fx.token), `${bad.slice(0, 8)} answered ${answer.status}`);
    }
  });

  await run("the routes stand under /api/browser-extension/, the paths before it answer the plain 404 to a token and a session, nothing else under /api/ opens", async () => {
    const cookie = await sessionCookie(page);
    for (const [method, name] of [["GET", "targets"], ["POST", "assistants"], ["POST", "pictures"], ["POST", "send"]]) {
      const old = `/extension/${name}`;
      for (const [who, answer] of [["the token", await request(method, old, { token: fx.token, accept: "text/html" })], ["the session", await request(method, old, { cookie, accept: "text/html" })]]) {
        assert(answer.status === 404 && !answer.headers.location && /Page not found/.test(answer.text) && !answer.text.includes(fx.token), `${method} ${old} with ${who} answered ${answer.status} ${answer.headers.location || ""}`);
      }
    }
    const fromPage = await page.evaluate(async () => {
      const res = await fetch(`/extension/targets`, { credentials: "include", redirect: "manual" });
      return res.status;
    });
    assert(fromPage === 404, `the page's own fetch of the old path answered ${fromPage}`);
    for (const path of ["/api/browser-extension", "/api/browser-extension/", "/api/targets", "/api/browser-extension/targets/x", "/api/settings/browser"]) {
      const answer = await apiGet(path, fx.token);
      assert(answer.status === 404 && !answer.text.includes(fx.token), `${path} with the token answered ${answer.status}`);
    }
  });

  await run("the local socket and a share link's guest are refused on the tokens and reach no token route", async () => {
    for (const [method, path, fields] of [["GET", "/settings/browser"], ["POST", "/settings/browser", { form: "new-token", name: `Socket ${TAG}` }], ["POST", "/settings/browser", { form: "revoke-token", id: fx.keepId }]]) {
      const answer = await overSocket(method, path, fields);
      assert(answer.status === 403 && /Ask the user/.test(answer.text) && !answer.text.includes(fx.name), `${method} ${path} over the socket answered ${answer.status}: ${answer.text.slice(0, 160)}`);
    }
    const made = await postAs(page, "/settings/sharing/new", [["amount", "1"], ["unit", "hours"], ["name", `ext-${TAG}-${engine}`], ["assistant", fx.assistant]]);
    const link = (made.text.match(/value="([^"]*\/share\/[^"]+)"/) || [])[1];
    assert(made.ok && link, `making the link answered ${made.status}`);
    fx.link = new URL(link).pathname;
    fx.linkToken = fx.link.split("/")[2];
    for (const path of ["/settings/browser", "/api/browser-extension/targets"]) {
      const answer = await request("GET", `${fx.link}${path}`);
      assert(answer.status === 404, `${fx.link}${path} answered ${answer.status}`);
    }
    const guestCtx = await page.context().browser().newContext({ ignoreHTTPSErrors: true });
    try {
      const guest = await guestCtx.newPage();
      await guest.goto(`${BASE}/settings/browser`, { waitUntil: "domcontentloaded" });
      assert(new URL(guest.url()).pathname === "/login", `a guest without a session landed on ${guest.url()}`);
    } finally {
      await guestCtx.close();
    }
    const asToken = await apiGet("/api/browser-extension/targets", fx.linkToken);
    assert(asToken.status === 401, `the link's token opened the targets: ${asToken.status}`);
    await openTokens(page);
    const rows = await tokenRows(page);
    assert(!rows.some((r) => r.name === `Socket ${TAG}`) && rows.some((r) => r.name === fx.name) && rows.some((r) => r.id === fx.keepId), JSON.stringify(rows));
    assert((await apiGet("/api/browser-extension/targets", fx.keep)).status === 200, "a revoke over the socket ended the token");
  });
}

async function sends({ page, run, fx }) {
  await run("a send to an assistant shows the note and its folded card live, from the extension with the token's name, the assistant reads the same Markdown and the picture is the message's own image", async () => {
    const chat = await page.context().newPage();
    fx.chat = chat;
    L.wirePage(chat, fx.bag);
    const requests = [];
    chat.on("request", (r) => requests.push(r.url()));
    await openThread(chat, fx.assistant);
    const picture = await apiPicture(fx.token, { to: "assistant", id: fx.assistant }, `drawing-localhost-8095-${TAG}.png`, await png(page));
    assert(picture.status === 200 && picture.json?.file === `drawing-localhost-8095-${TAG}.png`, `the picture answered ${picture.status}: ${picture.text}`);
    const { message, card } = await built(page, NOTE, picture.json.file);
    const sent = await apiPost("/api/browser-extension/send", fx.token, [["to", "assistant"], ["id", fx.assistant], ["message", message], ["card", card], ["picture", picture.json.file]]);
    assert(sent.status === 200 && sent.json?.url === `/assistants/${fx.assistant}`, `the send answered ${sent.status}: ${sent.text}`);
    const got = (await messages(page, fx.assistant)).filter((m) => m.role === "user").pop();
    assert(got && got.content === message, `the assistant got ${JSON.stringify(got?.content.slice(0, 200))}`);
    const live = await waitCard(chat, NOTE, (c) => c.viaSeen, "the open thread shows no card from the extension");
    assert(live.head.startsWith("From the extension") && live.icon.includes("ti-puzzle") && live.via === `via ${fx.name}` && !live.open, JSON.stringify(live));
    await chat.locator("[data-assistant-message]", { hasText: NOTE }).locator("[data-picked-fold]").click();
    const open = await waitCard(chat, NOTE, (c) => c.open && c.pictures.length === 1 && c.pictures[0].width > 0 && c.pictures[0].seen, "the card did not unfold with its picture");
    assert(open.pictures[0].src.startsWith(`/assistants/${fx.assistant}/media/`) && open.links === 0, JSON.stringify(open));
    assert(open.text.includes('Buy <img src=/ext-pwned onerror="window.__pwned=1">') && !(await chat.evaluate(() => window.__pwned)) && !requests.some((u) => /ext-pwned/.test(u)), "the page's text ran or loaded");
    await chat.reload({ waitUntil: "domcontentloaded" });
    await chat.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
    const again = await waitCard(chat, NOTE, () => true, "no card after the reload");
    assert(again.viaSeen && again.via === `via ${fx.name}` && !again.open, JSON.stringify(again));
  });

  await run("a send while a turn runs waits with its card, a new assistant runs on the coder named and none without one, a send without a note titles it after the page, and a guest of the share link sees the token's name read only", async () => {
    const busy = await postAs(page, `/assistants/${fx.assistant}`, { form: "message", message: `SLOW ${TAG}` });
    assert(busy.ok, `the slow message answered ${busy.status}`);
    const { message, card } = await built(page, `Waiting ${TAG}`);
    const sent = await apiPost("/api/browser-extension/send", fx.token, { to: "assistant", id: fx.assistant, message, card });
    assert(sent.status === 200 && sent.json?.queued === true, `the waiting send answered ${sent.status}: ${sent.text}`);
    // A fresh load: an open thread that hears a run start before its pull
    // shows the run's messages only once it ends (syncFromServer waits).
    await openThread(fx.chat, fx.assistant);
    const waiting = await waitCard(fx.chat, `Waiting ${TAG}`, () => true, "the waiting message shows no card");
    assert(waiting.state === "queued" && waiting.queued === "Waiting" && waiting.viaSeen, JSON.stringify(waiting));
    await postAs(page, `/assistants/${fx.assistant}`, { form: "cancel" });
    for (const fields of [{}, { coder: "nobody" }]) {
      const refused = await apiPost("/api/browser-extension/assistants", fx.token, fields);
      assert(refused.status === 400 && refused.json?.error && refused.json.error.length <= 300, `a new assistant with ${JSON.stringify(fields)} answered ${refused.status}: ${refused.text}`);
    }
    const last = fx.newWith.at(-1);
    const made = await apiPost("/api/browser-extension/assistants", fx.token, { coder: last.id });
    assert(made.status === 200 && made.json?.id && made.json.url === `/assistants/${made.json.id}`, `the new assistant answered ${made.status}: ${made.text}`);
    fx.made.push(made.json.id);
    const fresh = await built(page, "");
    const toNew = await apiPost("/api/browser-extension/send", fx.token, { to: "assistant", id: made.json.id, ...fresh });
    assert(toNew.status === 200 && toNew.json?.name === "Shop <b>cart</b>", `the send to the new assistant answered ${toNew.status}: ${toNew.text}`);
    const instance = await page.evaluate(async (id) => (await fetch(`/assistants/instances/${id}`, { headers: { Accept: "application/json" } })).json(), made.json.id);
    assert(instance.coderId === last.id && instance.title === "Shop <b>cart</b>", `the new assistant runs as ${JSON.stringify({ coder: instance.coderId, title: instance.title })}`);
    await openThread(fx.chat, made.json.id);
    await waitSeen(fx.chat, "[data-assistant-title]");
    const title = await fx.chat.evaluate(() => document.querySelector("[data-assistant-title]").innerText.trim());
    assert(title === "Shop <b>cart</b>", `the new assistant's page is titled ${title}`);
    const guestCtx = await page.context().browser().newContext({ ignoreHTTPSErrors: true, viewport: { width: 1440, height: 900 } });
    try {
      const guest = await guestCtx.newPage();
      await guest.goto(`${BASE}${fx.link}`, { waitUntil: "domcontentloaded" });
      await guest.waitForSelector("dc-share-chat [data-picked-via]", { timeout: 15000 });
      const card = await waitCard(guest, NOTE, (c) => c.viaSeen, "the guest sees no token name", "dc-share-chat");
      assert(card.via === `via ${fx.name}` && card.links === 0, JSON.stringify(card));
      const fields = await guest.evaluate(() => document.querySelectorAll("dc-share-chat [data-picked] form, dc-share-chat [data-picked] input, dc-share-chat [data-picked] textarea").length);
      assert(fields === 0, `the guest's card holds ${fields} fields`);
    } finally {
      await guestCtx.close();
    }
  });

  await run("a send to a running coder types the same Markdown in, its control characters as text, and a picture lands in the coder's files", async () => {
    const picture = await apiPicture(fx.token, { to: "coder", id: fx.coder }, "drawing.png", await png(page));
    assert(picture.status === 200 && picture.json?.file?.endsWith("/drawing.png"), `the coder's picture answered ${picture.status}: ${picture.text}`);
    const note = `Coder note ${TAG} \u001b[31mred\u001b[0m`;
    const { message } = await built(page, note);
    const sent = await apiPost("/api/browser-extension/send", fx.token, { to: "coder", id: fx.coder, message });
    assert(sent.status === 200 && sent.json?.url === `/coders/${fx.coder}`, `the coder send answered ${sent.status}: ${sent.text}`);
    let pane = "";
    for (let i = 0; i < 40 && !pane.includes(`Coder note ${TAG}`); i++) {
      await sleep(250);
      pane = await page.evaluate(async (id) => (await (await fetch(`/coders/${id}/copy?lines=300`)).json()).text || "", fx.coder);
    }
    assert(pane.includes(`Coder note ${TAG} ␛[31mred␛[0m`) && pane.includes("#buy"), `the coder's pane shows ${JSON.stringify(pane.slice(-400))}`);
    const files = await page.evaluate(async (id) => (await fetch(`/coders/${id}/files`)).text(), fx.coder);
    assert(files.includes("drawing.png"), "the coder's files do not list the picture");
  });

  await run("every bound of a send is refused with a 4xx and a short sentence, and nothing arrives", async () => {
    const before = (await messages(page, fx.assistant)).length;
    const { message, card } = await built(page, `Bound ${TAG}`);
    const other = (await built(page, `Other ${TAG}`)).card;
    const cases = [
      ["no target", { id: fx.assistant, message, card }, 400],
      ["an unknown target kind", { to: "shell", id: fx.assistant, message, card }, 400],
      ["an unknown assistant", { to: "assistant", id: "nobody", message, card }, 404],
      ["no note and nothing picked", { to: "assistant", id: fx.assistant, message: " ", card }, 400],
      ["no card", { to: "assistant", id: fx.assistant, message }, 400],
      ["a card of another message", { to: "assistant", id: fx.assistant, message, card: other }, 400],
      ["a card past its bound", { to: "assistant", id: fx.assistant, message, card: " ".repeat(70000) }, 400],
      ["a picture not uploaded", { to: "assistant", id: fx.assistant, message, card, picture: "../../tokens.json" }, 400],
      ["a message past its bound", { to: "assistant", id: fx.assistant, message: `Bound ${TAG}${"x".repeat(33000)}`, card }, 400],
      ["a coder that does not run", { to: "coder", id: "nobody", message }, 410],
      ["a message too long for a coder", { to: "coder", id: fx.coder, message: "x".repeat(16000) }, 400],
    ];
    for (const [what, fields, code] of cases) {
      const answer = await apiPost("/api/browser-extension/send", fx.token, fields);
      assert(answer.status === code && typeof answer.json?.error === "string" && answer.json.error.length > 0 && answer.json.error.length <= 300, `${what} answered ${answer.status}: ${answer.text.slice(0, 160)}`);
    }
    const pictures = [
      ["text named .png", Buffer.from("<svg onload=alert(1)>"), 400],
      ["a GIF", Buffer.from("GIF89a\u0001\u0000\u0001\u0000\u0000\u0000\u0000;"), 400],
    ];
    for (const [what, bytes, code] of pictures) {
      const answer = await apiPicture(fx.token, { to: "assistant", id: fx.assistant }, "drawing.png", bytes);
      assert(answer.status === code && answer.json?.error, `${what} answered ${answer.status}: ${answer.text.slice(0, 160)}`);
    }
    const wide = await page.evaluate(async () => {
      const canvas = document.createElement("canvas");
      canvas.width = 8193;
      canvas.height = 1;
      return canvas.toDataURL("image/png").split(",")[1];
    });
    const tooWide = await apiPicture(fx.token, { to: "assistant", id: fx.assistant }, "wide.png", Buffer.from(wide, "base64"));
    assert(tooWide.status === 413 && /8192/.test(tooWide.json?.error || ""), `a picture 8193 wide answered ${tooWide.status}`);
    const after = await messages(page, fx.assistant);
    assert(after.length === before, `a refused send left ${after.length - before} messages`);
  });
}

async function revokes({ page, run, fx }) {
  await run("Revoke asks first, Cancel keeps the token, the confirm ends it at once", async () => {
    await openTokens(page);
    const asked = await revoke(page, fx.name, { cancel: true });
    assert(asked.text.includes(`Revoke the token "${fx.name}"?`) && asked.danger && asked.warning === "It stops working at once." && /\bbtn-danger\b/.test(asked.button), JSON.stringify(asked));
    assert((await tokenRows(page)).some((r) => r.name === fx.name), "Cancel took the token");
    assert((await apiGet("/api/browser-extension/targets", fx.token)).status === 200, "the token stopped after Cancel");
    await revoke(page, fx.name);
    await waitSeen(page, "#settings-browser-tokens .alert, #settings-browser-tokens [role=alert], #settings-browser-tokens .dc-section-flash, #settings-browser-tokens [data-flash]", true, 5000).catch(() => {});
    const flash = await page.evaluate(() => document.querySelector("#settings-browser-tokens")?.innerText || "");
    assert(/Token revoked\./.test(flash), `no flash: ${flash.slice(0, 200)}`);
    const answer = await apiGet("/api/browser-extension/targets", fx.token);
    assert(answer.status === 401, `the revoked token answered ${answer.status}`);
    const send = await apiPost("/api/browser-extension/send", fx.token, { to: "assistant", id: fx.assistant, message: "x", card: "{}" });
    assert(send.status === 401, `a send with the revoked token answered ${send.status}`);
  });
}

async function phone({ browser, engine, run, fx }) {
  for (const scheme of ["dark", "light"]) {
    await run(`phone 390x844, ${scheme}: a token is made with taps, typing and Enter, copied with a tap and revoked with taps, all within the width`, async () => {
      const ctx = await browser.newContext({ ignoreHTTPSErrors: true, hasTouch: true, isMobile: engine === "chromium", viewport: { width: 390, height: 844 }, colorScheme: scheme });
      const page = await ctx.newPage();
      L.wirePage(page, fx.bag);
      const name = `Phone ${scheme} ${TAG}`;
      try {
        await L.login(page);
        await L.dismissUpdate(page);
        const shown = await makeToken(page, name, { phone: true });
        assert(/^[A-Za-z0-9_-]{43}$/.test(shown.token) && shown.copied === shown.token && shown.kept && shown.inside && shown.sideways <= 0, JSON.stringify(shown));
        const look = await page.evaluate(() => {
          const section = document.querySelector("#settings-browser-tokens");
          const rgb = getComputedStyle(document.body).backgroundColor.match(/\d+(\.\d+)?/g).slice(0, 3).map(Number);
          const wide = [...section.querySelectorAll("*")].filter((n) => { const b = n.getBoundingClientRect(); return b.width > 0 && b.right > innerWidth + 1; }).length;
          return { wide, sideways: document.documentElement.scrollWidth - innerWidth, luma: (0.2126 * rgb[0] + 0.7152 * rgb[1] + 0.0722 * rgb[2]) / 255 };
        });
        assert(look.wide === 0 && look.sideways <= 0, JSON.stringify(look));
        assert(scheme === "dark" ? look.luma < 0.3 : look.luma > 0.8, `the page has a luma of ${look.luma.toFixed(2)} in ${scheme}`);
        await revoke(page, name, { phone: true });
        assert((await apiGet("/api/browser-extension/targets", shown.token)).status === 401, "the token revoked on the phone still opens");
      } finally {
        await ctx.close();
      }
    });
  }
}

async function looks({ browser, engine, run, fx }) {
  for (const [label, viewport, touch] of [["desktop 1440x900", { width: 1440, height: 900 }, false], ["tablet 834x1194", { width: 834, height: 1194 }, true]]) {
    for (const scheme of ["dark", "light"]) {
      await run(`${label}, ${scheme}: the tokens stand in Settings › Browser within the width, in the cockpit's colors`, async () => {
        const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport, hasTouch: touch, isMobile: touch && engine === "chromium", colorScheme: scheme });
        const page = await ctx.newPage();
        L.wirePage(page, fx.bag);
        try {
          await L.login(page);
          await L.dismissUpdate(page);
          await openTokens(page);
          await waitSeen(page, "[data-browser-extension-token-new]");
          await waitSeen(page, `[data-browser-extension-token="${fx.keepId}"] [data-browser-extension-token-revoke]`);
          const look = await page.evaluate(() => {
            const section = document.querySelector("#settings-browser-tokens");
            const rgb = getComputedStyle(document.body).backgroundColor.match(/\d+(\.\d+)?/g).slice(0, 3).map(Number);
            const right = section.getBoundingClientRect().right;
            const wide = [...section.querySelectorAll("*")].filter((n) => { const b = n.getBoundingClientRect(); return b.width > 0 && b.right > right + 1; }).length;
            return { wide, sideways: document.documentElement.scrollWidth - innerWidth, luma: (0.2126 * rgb[0] + 0.7152 * rgb[1] + 0.0722 * rgb[2]) / 255 };
          });
          assert(look.wide === 0 && look.sideways <= 0, JSON.stringify(look));
          assert(scheme === "dark" ? look.luma < 0.3 : look.luma > 0.8, `the page has a luma of ${look.luma.toFixed(2)} in ${scheme}`);
        } finally {
          await ctx.close();
        }
      });
    }
  }
}

async function docs({ page, run }) {
  await run("the docs say what a token opens and how the extension's API answers", async () => {
    await page.goto(`${BASE}/docs#browser`, { waitUntil: "domcontentloaded" });
    const text = await page.evaluate(() => document.body.innerText);
    for (const want of ["Tokens for the browser extension", "shows the token once with Copy", "Revoke asks first, the token's next request is refused", "Authorization: Bearer <token>", "A token lists the assistants and the running coders, starts new assistants and sends to them.", "GET /api/browser-extension/targets", "POST /api/browser-extension/assistants", "POST /api/browser-extension/send", "marked From the extension with the token's name"]) {
      assert(text.includes(want), `the docs lack ${want}`);
    }
  });
}

(async () => {
  const results = [];
  const bag = { consoleErrors: [], pageErrors: [], cdnNoise: [] };
  const engines = L.engineList();
  for (const engine of engines) {
    const browser = await L.launch(engine);
    const ctx = await L.newDesktop(browser, engine);
    const page = await ctx.newPage();
    await page.setViewportSize({ width: 1440, height: 900 });
    L.wirePage(page, bag);
    const fx = { bag, made: [], title: `Ext ${engine} ${TAG}`, name: `Laptop ${engine} <b>${TAG}</b>` };
    const run = async (name, fn) => {
      if (!(process.env.CHECK ? process.env.CHECK.split(",").some((t) => name.toLowerCase().includes(t.trim().toLowerCase())) : true)) return;
      const t0 = Date.now();
      try { const d = await fn(); results.push({ name: `[${engine}] ${name}`, status: "PASS", detail: d || "", ms: Date.now() - t0 }); }
      catch (e) { results.push({ name: `[${engine}] ${name}`, status: "FAIL", detail: e.message.split("\n")[0], ms: Date.now() - t0 }); }
    };
    try {
      await L.login(page);
      await L.dismissUpdate(page);
      if (engine === engines[0]) await L.createProject(page, PROJECT);
      fx.coder = new URL(await L.createSession(page, PROJECT, `ext-${engine}-${TAG}`, "claude")).pathname.split("/").pop();
      fx.assistant = await newAssistant(page, fx.title);
      fx.made.push(fx.assistant);
      const keep = await makeToken(page, `Keep ${engine} ${TAG}`);
      fx.keepId = (await tokenRows(page)).find((r) => r.name === `Keep ${engine} ${TAG}`).id;
      fx.keep = keep.token;
      await management({ page, run, fx, engine });
      await sends({ page, run, fx });
      await revokes({ page, run, fx });
      await phone({ browser, engine, run, fx });
      await looks({ browser, engine, run, fx });
      if (engine === engines[0]) await docs({ page, run });
    } catch (e) {
      results.push({ name: `[${engine}] FATAL`, status: "FAIL", detail: e.message });
    } finally {
      try {
        await fx.chat?.close();
        await openTokens(page);
        for (const row of await tokenRows(page)) if (row.name.includes(TAG)) await postAs(page, "/settings/browser", { form: "revoke-token", id: row.id });
        if (fx.link) {
          const ids = await page.evaluate(async (assistant) => {
            const html = await (await fetch("/settings/sharing", { headers: { Accept: "text/html" } })).text();
            return [...new DOMParser().parseFromString(html, "text/html").querySelectorAll("[data-share-row]")].filter((row) => row.querySelector(`[data-share-assistant="${assistant}"]`)).map((row) => row.dataset.shareRow);
          }, fx.assistant);
          for (const one of ids) await postAs(page, `/settings/sharing/${one}`, { form: "revoke" });
        }
        for (const one of fx.made) await postAs(page, `/assistants/${one}`, { form: "delete" });
        if (fx.coder) await postAs(page, `/coders/${fx.coder}/delete`, {});
        if (engine === engines.slice(-1)[0]) await L.deleteProject(page, PROJECT);
      } catch (e) {
        results.push({ name: `[${engine}] cleanup`, status: "FAIL", detail: e.message });
      }
      await ctx.close().catch(() => {});
      await browser.close().catch(() => {});
    }
  }
  process.exit(L.report("BROWSER EXTENSION TOKENS", results, bag) ? 1 : 0);
})();
