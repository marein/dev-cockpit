const fs = require("fs");
const os = require("os");
const path = require("path");
const http = require("http");
const https = require("https");
const zlib = require("zlib");
const { chromium } = require("playwright-core");
const L = require("./lib");

const { BASE, sleep } = L;

// Helpers of browser-extension.js, browser-extension-console-draw.js and
// browser-extension-safari.js, run nowhere on its own: two test sites served
// in the runner (site A as localhost and as 127.0.0.1, which the browser takes
// for another site, site B as 127.0.0.1 on its own port), a zip reader, a
// driver of the side panel over the DevTools protocol and a reader of the
// picker's closed shadow root. Site B redirects every /api/browser-extension/
// call to the cockpit.

const PORT_A = Number(process.env.EXT_PORT_A || 3311);
const PORT_B = Number(process.env.EXT_PORT_B || 3312);
const SITE_A = `http://localhost:${PORT_A}`;
const SITE_A_AGAIN = `http://127.0.0.1:${PORT_A}`;
const SITE_B = `http://127.0.0.1:${PORT_B}`;
const EVERY_PAGE = ["<all_urls>"];

const seen = { redirected: 0, bearer: 0 };

function page(title, body, head = "") {
  return `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>${title}</title>${head}</head><body>${body}</body></html>`;
}

const SHOP = `<style>
  body { font: 16px/1.4 sans-serif; margin: 0; }
  header { padding: 12px 24px; background: rgb(240, 240, 240); }
  .card { margin: 40px; padding: 16px; width: 320px; border: 1px solid rgb(200, 200, 200); }
  .btn { display: inline-block; padding: 8px 16px; border: 0; background: rgb(0, 120, 80); color: rgb(255, 255, 255); font-size: 15px; }
</style>`;

const COUNT = `<script>
  window.clicks = 0;
  window.seenClicks = 0;
  document.addEventListener("click", () => { window.seenClicks += 1; }, true);
  document.getElementById("buy").addEventListener("click", () => { window.clicks += 1; });
</script>`;

function plainPage() {
  return page("Plain page", `<header id="top"><h1>Plain page</h1></header>
<main><div class="card shop" data-testid="card"><p class="lead">A thing to buy.</p><button id="buy" class="btn primary" type="button">Buy now</button></div>
<a id="away" href="/second">Second page</a></main>${COUNT}`, SHOP);
}

// A button at the right edge and class names as long as a component library
// writes them.
function edgePage() {
  return page("Edge page", `<header style="position:relative;height:56px;background:rgb(240,240,240)"><button id="account" class="btn header-actions__account-button header-actions__account-button--signed-in" type="button" style="position:absolute;right:8px;top:12px">Account</button></header>
<main><p class="product-card__description-text-with-a-long-block-element-name product-card__description-text--highlighted" style="margin:40px">A very good thing to buy, really</p></main>`, SHOP);
}

function noTitlePage() {
  return `<!doctype html><html lang="en"><head><meta charset="utf-8">${SHOP}</head><body><main><div class="card"><button id="buy" class="btn" type="button">Buy untitled</button></div></main></body></html>`;
}

function secondPage() {
  return page("Second page", `<header><h1>Second page</h1></header><main><p id="other" class="other">Nothing to buy here.</p><a href="/plain">Back</a></main>`, SHOP);
}

function bannedPage() {
  return page("Banned page", `<main><div class="card"><button id="buy" class="btn" type="button">Buy banned</button></div></main>${COUNT}`, SHOP);
}

function strictPage() {
  return page("Strict page", `<main><div class="card strict-box" id="strict"><span class="price">12 EUR</span><button id="buy" class="btn" type="button">Buy strict</button></div></main><script src="/strict.js"></script>`, `<link rel="stylesheet" href="/strict.css">`);
}

const STRICT_CSS = ".strict-box { color: rgb(200, 0, 0); font-size: 22px; margin: 40px; padding: 16px; width: 320px; }\n.btn { padding: 8px 16px; }\n";
const STRICT_JS = "window.clicks = 0; document.getElementById('buy').addEventListener('click', function () { window.clicks += 1; });\n";

function assetsPage() {
  return page("Assets page", `<main><div class="card from-b" id="assets"><img id="logo" src="${SITE_B}/b.svg" width="48" height="48" alt="Logo from B"><button id="buy" class="btn" type="button">Buy assets</button></div></main><script src="${SITE_B}/b.js"></script>`, `<link rel="stylesheet" href="${SITE_B}/b.css">${SHOP}`);
}

const B_CSS = ".from-b { background-color: rgb(0, 0, 200); color: rgb(255, 255, 255); }\n";
const B_SVG = `<svg xmlns="http://www.w3.org/2000/svg" width="48" height="48"><rect width="48" height="48" fill="#c80"/></svg>`;
const B_JS = "window.fromB = true;\n";

function accountPage() {
  return page("Account", `<main><div class="card" id="account"><p class="who">Signed in as the ext tester</p><button id="buy" class="btn" type="button">Buy signed in</button></div></main>${COUNT}`, SHOP);
}

function loginPage(back) {
  return page("Login on B", `<main><form method="post" action="/login"><input type="hidden" name="back" value="${back.replace(/"/g, "&quot;")}"><label>Name <input id="name" name="name" value="tester"></label><button id="sign-in" type="submit">Sign in</button></form></main>`);
}

// showModal as the page loads, the way a confirm does, makes everything
// outside the dialog inert. The second opens over it on the runner's word.
function modalPage() {
  return page("Modal page", `<main><div class="card"><button id="buy" class="btn" type="button">Buy behind</button></div>
<dialog id="later"><p>Opened later</p><button id="later-ok" class="btn" type="button">Later OK</button></dialog>
<dialog id="confirm"><p class="ask">Delete the thing?</p><button id="ok" class="btn" type="button">OK</button></dialog></main>
<script>
  window.clicks = 0;
  window.seenClicks = 0;
  document.addEventListener("click", () => { window.seenClicks += 1; }, true);
  for (const id of ["buy", "ok", "later-ok"]) {
    document.getElementById(id).addEventListener("click", () => {
      window.clicks += 1;
      document.title = id + " pressed";
      document.getElementById(id).closest("dialog")?.close();
    });
  }
  document.getElementById("confirm").showModal();
</script>`, SHOP);
}

// Native popovers: a press outside the topmost closes them, the page logs it.
function menuPage() {
  return page("Menu", `<style>
  [popover] { position: fixed; inset: auto; margin: 0; left: 420px; padding: 8px; border: 1px solid rgb(120, 120, 120); }
  #menu { top: 40px; } #sub { top: 180px; } #late { top: 300px; left: 640px; }
</style>
<main><div class="card"><button id="buy" class="btn" type="button">Buy behind</button> <button id="open-menu" class="btn" type="button" popovertarget="menu">Menu</button></div>
<div id="sub" popover><button id="sub1" class="btn" type="button">Sub item</button><div id="late" popover><button id="late-ok" class="btn" type="button">Late OK</button></div></div>
<div id="menu" popover><button id="item1" class="btn" type="button">Item one</button> <button id="item2" class="btn" type="button">Item two</button> <button id="open-sub" class="btn" type="button" popovertarget="sub">More</button></div></main>
<script>
  window.clicks = 0;
  window.seenClicks = 0;
  window.toggles = [];
  document.addEventListener("click", () => { window.seenClicks += 1; }, true);
  for (const id of ["buy", "item1", "item2", "sub1", "late-ok"]) document.getElementById(id).addEventListener("click", () => { window.clicks += 1; });
  for (const id of ["menu", "sub", "late"]) document.getElementById(id).addEventListener("toggle", (event) => window.toggles.push(id + " " + event.newState));
</script>`, SHOP);
}

// Goes wrong from its first script on: a head script throws before anything
// else runs. route() moves to another view without a load.
function errorsPage() {
  return page("Errors page", `<main><h1>Errors</h1><button id="again" class="btn" type="button">Fail again</button><img id="gone" src="/gone.png" alt="gone" width="20" height="20"></main>
<script>
  console.warn("Careful, the cache is cold");
  console.error("Cart total is", { total: -1 });
  Promise.reject(new Error("The price service never answered"));
  fetch("/missing.json").catch(() => {});
  fetch("${QUIET}/never").catch(() => {});
  document.getElementById("again").addEventListener("click", () => { throw new Error("Clicked into a bug"); });
  window.route = (path) => history.pushState({}, "", path);
</script>`, `<script>throw new Error("Head boom before anything")</script>${SHOP}`);
}

// An app whose main area scrolls under a fixed header.
function innerPage() {
  const rows = Array.from({ length: 30 }, (_, i) => `<div id="row${i + 1}" class="row">Row ${i + 1}</div>`).join("");
  return page("Inner page", `<style>body { margin: 0; font: 16px/1.4 sans-serif; } header { position: fixed; top: 0; left: 0; right: 0; height: 56px; background: rgb(240, 240, 240); } main { position: fixed; top: 56px; left: 0; right: 0; bottom: 0; overflow: auto; } .row { height: 44px; margin: 8px 40px; padding: 4px 12px; width: 300px; background: rgb(250, 250, 250); }</style>
<header><h1 style="margin:8px 40px;font-size:20px">Inner</h1></header><main>${rows}</main>`);
}

// A style sheet's background and font fail without an event of an element.
function styledPage() {
  return page("Styled page", `<style>@font-face { font-family: Gone; src: url(/missing-font.woff2); } .bg { width: 40px; height: 40px; background: url(/missing-bg.png); } .gone-font { font-family: Gone, sans-serif; }</style>
<main><div class="bg"></div><p class="gone-font">A font that never loads</p><img src="/missing.png" alt="gone" width="20" height="20"></main>`);
}

// 40 more errors than the catch keeps for a page.
function floodPage() {
  return page("Flood page", `<main><h1>Flood</h1></main><script>for (let i = 1; i <= 240; i += 1) console.error("Flood " + i);</script>`);
}

// Counts every click and scroll that reaches it. attack(id) tries what a page
// could to draw, take a picture or fill the tray without the user.
function drawPage() {
  return page("Draw page", `<style>.spot { margin: 40px; padding: 24px; width: 320px; background: rgb(0, 120, 80); color: rgb(255, 255, 255); } .tall { height: 2400px; }</style>
<main><button id="fix" class="btn" type="button" style="margin:40px">Fix me</button><div id="spot" class="spot">Look here</div><div class="tall"></div></main>
<script>
  window.clicks = 0;
  window.scrolls = 0;
  document.addEventListener("click", () => { window.clicks += 1; }, true);
  document.addEventListener("pointerdown", () => { window.clicks += 1; }, true);
  window.addEventListener("scroll", () => { window.scrolls += 1; }, true);
  window.attack = async function (id) {
    const log = [];
    const box = document.getElementById("spot").getBoundingClientRect();
    for (const el of document.elementsFromPoint(box.x + 30, box.y + 30)) {
      for (const [type, x] of [["pointerdown", 30], ["pointermove", 120], ["pointerup", 200], ["click", 200]]) {
        el.dispatchEvent(new PointerEvent(type, { bubbles: true, cancelable: true, composed: true, clientX: box.x + x, clientY: box.y + 30, pointerId: 7, pointerType: "mouse", button: 0, buttons: 1 }));
      }
    }
    window.dispatchEvent(new CustomEvent("dc-extension-console", { detail: JSON.stringify({ type: "console", entries: [{ level: "error", kind: "console", message: "Forged by the page", stack: "" }], dropped: 0 }) }));
    for (const data of [{ op: "add-log", all: true, tabId: 1 }, { op: "draw-prepare", tabId: 1 }, { op: "add-image", tabId: 1, key: "abc", item: { kind: "image" } }, { op: "draw", on: true, tabId: 1 }]) {
      try {
        await chrome.runtime.sendMessage(id, data);
        log.push(data.op + " answered");
      } catch (e) {
        log.push(data.op + " refused");
      }
    }
    return log;
  };
</script>`, SHOP);
}

// Tries what a page could to make the extension act without the user:
// synthetic events, messages, the extension's API and its pages.
function hostilePage() {
  return page("Hostile page", `<main><button id="bait" class="btn" type="button" style="margin:40px;width:240px;height:60px">Hostile bait</button>
<button id="markup" class="btn" type="button" style="margin:40px">&lt;img src=/ext-pwned onerror="window.__pwned=1"&gt; Buy</button></main>
<script>
  window.attack = async function (id) {
    const log = [];
    const box = document.getElementById("bait").getBoundingClientRect();
    const x = box.x + 10, y = box.y + 10;
    const fire = (el) => {
      for (const type of ["pointermove", "pointerdown", "mousedown", "pointerup", "mouseup", "click"]) {
        const Kind = type.startsWith("pointer") ? PointerEvent : MouseEvent;
        el.dispatchEvent(new Kind(type, { bubbles: true, cancelable: true, composed: true, clientX: x, clientY: y, view: window, pointerType: "mouse" }));
      }
    };
    for (const el of document.elementsFromPoint(x, y)) fire(el);
    for (const el of document.documentElement.children) if (el !== document.head && el !== document.body) { fire(el); el.click(); }
    for (const target of [window, document, document.body]) {
      target.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", code: "Escape", bubbles: true, cancelable: true }));
      target.dispatchEvent(new KeyboardEvent("keydown", { key: "C", code: "KeyC", altKey: true, shiftKey: true, bubbles: true, cancelable: true }));
    }
    for (const data of [{ type: "pick", on: true }, { type: "picked", handle: 1, element: { tag: "fake", selector: "#fake" } }, { op: "toggle" }, { op: "sent" }]) window.postMessage(data, "*");
    try {
      await chrome.runtime.sendMessage(id, { op: "toggle", tabId: 1 });
      log.push("runtime answered");
    } catch (e) {
      log.push("runtime refused");
    }
    for (const file of ["panel.html", "options.html", "manifest.json", "pick.js"]) {
      try {
        const r = await fetch("chrome-extension://" + id + "/" + file);
        log.push(file + " read " + r.status);
      } catch (e) {
        log.push(file + " refused");
      }
    }
    return log;
  };
</script>`);
}

function send(res, status, body, headers = {}) {
  res.writeHead(status, { "Content-Type": "text/html; charset=utf-8", "Cache-Control": "no-store", ...headers });
  res.end(body);
}

function readBody(req) {
  return new Promise((resolve) => {
    let raw = "";
    req.on("data", (c) => { raw += c; });
    req.on("end", () => resolve(raw));
  });
}

function siteA() {
  return http.createServer(async (req, res) => {
    const url = new URL(req.url, SITE_A);
    const p = url.pathname;
    const cookie = req.headers.cookie || "";
    if (p === "/plain") return send(res, 200, plainPage());
    if (p === "/second") return send(res, 200, secondPage());
    if (p === "/edge") return send(res, 200, edgePage());
    if (p === "/notitle") return send(res, 200, noTitlePage());
    if (p === "/banned") return send(res, 200, bannedPage(), { "Content-Security-Policy": "frame-ancestors 'none'", "X-Frame-Options": "DENY" });
    if (p === "/strict") return send(res, 200, strictPage(), { "Content-Security-Policy": "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; frame-ancestors 'none'" });
    if (p === "/strict.css") return send(res, 200, STRICT_CSS, { "Content-Type": "text/css" });
    if (p === "/strict.js") return send(res, 200, STRICT_JS, { "Content-Type": "text/javascript" });
    if (p === "/assets") return send(res, 200, assetsPage());
    if (p === "/account") {
      if (!/(^|;\s*)extsession=ok/.test(cookie)) return send(res, 302, "", { Location: `${SITE_B}/login?back=${encodeURIComponent(`${SITE_A}/callback`)}` });
      return send(res, 200, accountPage());
    }
    if (p === "/callback") {
      if (url.searchParams.get("ticket") !== "t1") return send(res, 403, "no ticket");
      return send(res, 302, "", { Location: "/account", "Set-Cookie": "extsession=ok; Path=/; HttpOnly; SameSite=Lax" });
    }
    if (p === "/modal") return send(res, 200, modalPage());
    if (p === "/menu") return send(res, 200, menuPage());
    if (p === "/hostile") return send(res, 200, hostilePage());
    if (p === "/errors" || p.startsWith("/errors/")) return send(res, 200, errorsPage());
    if (p === "/flood") return send(res, 200, floodPage());
    if (p === "/styled") return send(res, 200, styledPage());
    if (p === "/inner") return send(res, 200, innerPage());
    if (p === "/draw") return send(res, 200, drawPage());
    return send(res, 404, "not here");
  });
}

function siteB(cockpit) {
  return http.createServer(async (req, res) => {
    const url = new URL(req.url, SITE_B);
    const p = url.pathname;
    if (p.startsWith("/api/browser-extension/")) {
      seen.redirected += 1;
      if (/^Bearer /.test(req.headers.authorization || "")) seen.bearer += 1;
      return send(res, 307, "", { Location: `${cockpit}${p}` });
    }
    if (p === "/b.css") return send(res, 200, B_CSS, { "Content-Type": "text/css" });
    if (p === "/b.svg") return send(res, 200, B_SVG, { "Content-Type": "image/svg+xml" });
    if (p === "/b.js") return send(res, 200, B_JS, { "Content-Type": "text/javascript" });
    if (p === "/login" && req.method === "GET") return send(res, 200, loginPage(url.searchParams.get("back") || ""));
    if (p === "/login" && req.method === "POST") {
      const back = new URLSearchParams(await readBody(req)).get("back") || "";
      if (!back.startsWith(SITE_A)) return send(res, 400, "bad back");
      return send(res, 302, "", { Location: `${back}?ticket=t1` });
    }
    return send(res, 404, "not here");
  });
}

function start(cockpit) {
  const a = siteA();
  const b = siteB(cockpit);
  return Promise.all([
    new Promise((resolve) => a.listen(PORT_A, "127.0.0.1", resolve)),
    new Promise((resolve) => b.listen(PORT_B, "127.0.0.1", resolve)),
  ]).then(() => ({ close: () => { a.close(); b.close(); } }));
}

function unzip(buffer) {
  const end = buffer.lastIndexOf(Buffer.from([0x50, 0x4b, 0x05, 0x06]));
  if (end < 0) throw new Error("no zip");
  const count = buffer.readUInt16LE(end + 10);
  let at = buffer.readUInt32LE(end + 16);
  const files = {};
  for (let i = 0; i < count; i++) {
    const method = buffer.readUInt16LE(at + 10);
    const size = buffer.readUInt32LE(at + 20);
    const nameLength = buffer.readUInt16LE(at + 28);
    const extra = buffer.readUInt16LE(at + 30);
    const comment = buffer.readUInt16LE(at + 32);
    const local = buffer.readUInt32LE(at + 42);
    const name = buffer.toString("utf8", at + 46, at + 46 + nameLength);
    const start = local + 30 + buffer.readUInt16LE(local + 26) + buffer.readUInt16LE(local + 28);
    const raw = buffer.subarray(start, start + size);
    files[name] = method === 8 ? zlib.inflateRawSync(raw) : raw;
    at += 46 + nameLength + extra + comment;
  }
  return files;
}

// Playwright gets no handle on the side panel, so it is driven over the
// DevTools session with real input events.
class Panel {
  constructor(session, sessionId) {
    this.session = session;
    this.sessionId = sessionId;
    this.next = 0;
    this.waiting = new Map();
    this.onMessage = (event) => {
      if (event.sessionId !== sessionId) return;
      const message = JSON.parse(event.message);
      const wait = message.id && this.waiting.get(message.id);
      if (!wait) return;
      this.waiting.delete(message.id);
      if (message.error) wait.reject(new Error(message.error.message));
      else wait.resolve(message.result);
    };
    session.on("Target.receivedMessageFromTarget", this.onMessage);
  }

  static async open(session, url, timeout = 10000) {
    const until = Date.now() + timeout;
    while (Date.now() < until) {
      const { targetInfos } = await session.send("Target.getTargets");
      const target = targetInfos.find((one) => one.url.startsWith(url));
      if (target) {
        const { sessionId } = await session.send("Target.attachToTarget", { targetId: target.targetId, flatten: false });
        const panel = new Panel(session, sessionId);
        panel.targetId = target.targetId;
        await panel.send("Runtime.enable");
        await panel.waitFor("document.readyState === 'complete' && !!document.querySelector('[data-pick]')");
        return panel;
      }
      await new Promise((resolve) => setTimeout(resolve, 150));
    }
    throw new Error(`no page at ${url}`);
  }

  send(method, params = {}) {
    this.next += 1;
    const id = this.next;
    return new Promise((resolve, reject) => {
      this.waiting.set(id, { resolve, reject });
      this.session.send("Target.sendMessageToTarget", { sessionId: this.sessionId, message: JSON.stringify({ id, method, params }) }).catch(reject);
    });
  }

  async eval(fn, arg) {
    const expression = typeof fn === "string" ? fn : `(${fn})(${JSON.stringify(arg === undefined ? null : arg)})`;
    const answer = await this.send("Runtime.evaluate", { expression, awaitPromise: true, returnByValue: true });
    if (answer.exceptionDetails) throw new Error(`panel: ${answer.exceptionDetails.exception?.description || answer.exceptionDetails.text}`);
    return answer.result.value;
  }

  async waitFor(expression, timeout = 8000, label = expression) {
    const until = Date.now() + timeout;
    while (Date.now() < until) {
      if (await this.eval(`Boolean(${expression})`).catch(() => false)) return;
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
    throw new Error(`the panel never showed ${label}`);
  }

  async center(selector) {
    const box = await this.eval((sel) => {
      const el = document.querySelector(sel);
      if (!el) return null;
      el.scrollIntoView({ block: "nearest" });
      const r = el.getBoundingClientRect();
      const s = getComputedStyle(el);
      return { x: r.x + r.width / 2, y: r.y + r.height / 2, seen: s.display !== "none" && s.visibility !== "hidden" && r.width > 0 && r.height > 0 };
    }, selector);
    if (!box || !box.seen) throw new Error(`${selector} is not shown in the panel`);
    return box;
  }

  async mouse(type, x, y, extra = {}) {
    await this.send("Input.dispatchMouseEvent", { type, x, y, button: type === "mouseMoved" ? "none" : "left", buttons: type === "mousePressed" ? 1 : 0, clickCount: type === "mouseMoved" ? 0 : 1, ...extra });
  }

  async hover(selector) {
    const { x, y } = await this.center(selector);
    await this.mouse("mouseMoved", x, y);
  }

  async click(selector) {
    const { x, y } = await this.center(selector);
    await this.mouse("mouseMoved", x, y);
    await this.mouse("mousePressed", x, y);
    await this.mouse("mouseReleased", x, y);
  }

  async clickText(selector, text) {
    const found = await this.eval(({ selector, text }) => {
      document.querySelectorAll("[data-runner-aim]").forEach((el) => el.removeAttribute("data-runner-aim"));
      const el = [...document.querySelectorAll(selector)].find((one) => (one.querySelector(".menu-name") || one).textContent === text);
      if (el) el.setAttribute("data-runner-aim", "");
      return Boolean(el);
    }, { selector, text });
    if (!found) throw new Error(`the panel shows no ${selector} with ${text}`);
    await this.click("[data-runner-aim]");
  }

  async type(text) {
    await this.send("Input.insertText", { text });
  }

  async key(key, code, keyCode, modifiers = 0) {
    for (const type of ["rawKeyDown", "keyUp"]) await this.send("Input.dispatchKeyEvent", { type, key, code, windowsVirtualKeyCode: keyCode, modifiers });
  }

  async scheme(dark) {
    await this.send("Emulation.setEmulatedMedia", { features: [{ name: "prefers-color-scheme", value: dark ? "dark" : "light" }] });
  }

  async shot(path) {
    const { data } = await this.send("Page.captureScreenshot", { format: "png" });
    require("fs").writeFileSync(path, Buffer.from(data, "base64"));
  }

  close() {
    this.session.off("Target.receivedMessageFromTarget", this.onMessage);
    return this.session.send("Target.detachFromTarget", { sessionId: this.sessionId }).catch(() => {});
  }
}

// The picker's shadow root is closed, only the DevTools protocol reads it.
async function overlay(page) {
  return readClosed(page, "DC-EXTENSION-PICK", async (host, { send, model }) => {
    const shadow = host.shadowRoots?.[0];
    const style = async (node) => {
      const { computedStyle } = await send("CSS.getComputedStyleForNode", { nodeId: node.nodeId });
      return Object.fromEntries(computedStyle.filter((p) => ["display", "visibility"].includes(p.name)).map((p) => [p.name, p.value]));
    };
    const box = async (node) => {
      const found = await model(node);
      if (!found) return null;
      const xs = found.border.filter((_, i) => i % 2 === 0);
      const ys = found.border.filter((_, i) => i % 2 === 1);
      return { x: Math.min(...xs), y: Math.min(...ys), w: Math.max(...xs) - Math.min(...xs), h: Math.max(...ys) - Math.min(...ys) };
    };
    const text = (node) => (node.nodeType === 3 ? node.nodeValue : (node.children || []).map(text).join(""));
    const [glass, hover, mark] = shadow.children;
    const read = async (node) => {
      const s = await style(node);
      const b = await box(node);
      const tag = (node.children || []).find((one) => one.nodeType === 1);
      const size = tag && (tag.children || []).filter((one) => one.nodeType === 1)[1];
      return { shown: s.display !== "none" && s.visibility !== "hidden" && Boolean(b && b.w > 0 && b.h > 0), box: b, label: text(node), tag: tag ? await box(tag) : null, size: size ? { text: text(size), box: await box(size) } : null };
    };
    return { host: true, mode: shadow.shadowRootType, glass: await read(glass), hover: await read(hover), mark: await read(mark) };
  });
}

// A node that leaves the page during a read goes stale, the read starts over
// on a fresh document, so an answer never mixes two states of the page. A
// node without a box, one not displayed, has no model.
const STALE = /Could not find node with given id/;

async function readClosed(page, name, read) {
  const cdp = await page.context().newCDPSession(page);
  const send = (method, params) => cdp.send(method, params);
  const model = async (node) => {
    try {
      return (await send("DOM.getBoxModel", { nodeId: node.nodeId })).model;
    } catch (error) {
      if (STALE.test(error.message)) throw error;
      return null;
    }
  };
  const find = (node) => {
    if (node.nodeName === name) return node;
    for (const child of [...(node.children || []), ...(node.shadowRoots || [])]) {
      const hit = find(child);
      if (hit) return hit;
    }
    return null;
  };
  try {
    await send("DOM.enable");
    await send("CSS.enable");
    for (let round = 1; ; round += 1) {
      try {
        const host = find((await send("DOM.getDocument", { depth: -1, pierce: true })).root);
        return host ? await read(host, { send, model }) : { host: false };
      } catch (error) {
        if (round === 5 || !STALE.test(error.message)) throw error;
      }
    }
  } finally {
    await cdp.detach().catch(() => {});
  }
}

const TAG = Date.now().toString(36).slice(-5);
const PROJECT = `extp-${TAG}`;
const ORIGIN = new URL(BASE).origin;
const QUIET = `http://localhost:${Number(process.env.EXT_PORT_QUIET || 3313)}`;
const SHOTS = process.env.EXT_SHOTS || "";
const NOTE = `Make the button ${TAG} wider.`;

function request(method, url, { token = "", body = null } = {}) {
  const target = new URL(url, BASE);
  const lib = target.protocol === "https:" ? https : http;
  return new Promise((resolve) => {
    const headers = { Accept: "application/json" };
    if (token) headers.Authorization = `Bearer ${token}`;
    if (body) Object.assign(headers, { "Content-Type": "application/x-www-form-urlencoded", "Content-Length": Buffer.byteLength(body) });
    const req = lib.request(target, { method, headers, rejectUnauthorized: false }, (res) => {
      let raw = "";
      res.on("data", (c) => { raw += c; });
      res.on("end", () => {
        let json = null;
        try { json = JSON.parse(raw); } catch {}
        resolve({ status: res.statusCode, json, text: raw });
      });
    });
    req.on("error", (e) => resolve({ status: 0, json: null, text: e.message }));
    req.end(body || undefined);
  });
}

function postAs(page, url, body) {
  return page.evaluate(async ({ url, body }) => {
    const token = document.querySelector('meta[name="csrf-token"]')?.content || "";
    const res = await fetch(url, { method: "POST", headers: { "X-CSRF-Token": token, Accept: "application/json" }, body: new URLSearchParams(body) });
    return { ok: res.ok, status: res.status, text: await res.text() };
  }, { url, body });
}

function shown(page, selector) {
  return page.evaluate((sel) => {
    const el = document.querySelector(sel);
    if (!el) return false;
    const s = getComputedStyle(el);
    const b = el.getBoundingClientRect();
    return s.display !== "none" && s.visibility !== "hidden" && b.width > 0 && b.height > 0;
  }, selector);
}

async function until(read, want, label, timeout = 8000) {
  const end = Date.now() + timeout;
  let got;
  while (Date.now() < end) {
    got = await read();
    if (want(got)) return got;
    await sleep(120);
  }
  throw new Error(`${label}: ${JSON.stringify(got)?.slice(0, 600)}`);
}

async function makeToken(page, name) {
  await page.goto(`${BASE}/settings/browser`, { waitUntil: "domcontentloaded" });
  await L.dismissUpdate(page);
  await page.waitForFunction(() => customElements.get("dc-browser-extension-tokens"));
  await page.click("[data-browser-extension-token-new]");
  await page.waitForSelector(".swal2-popup .swal2-input", { state: "visible" });
  await page.fill(".swal2-popup .swal2-input", name);
  await page.keyboard.press("Enter");
  await page.waitForSelector(".swal2-popup .dc-ask-code pre", { state: "visible", timeout: 10000 });
  const token = await page.textContent(".swal2-popup .dc-ask-code pre");
  await page.click(".swal2-popup .swal2-confirm");
  await page.waitForSelector(".swal2-container", { state: "detached", timeout: 5000 });
  return token;
}

async function revokeToken(page, name) {
  await page.goto(`${BASE}/settings/browser`, { waitUntil: "domcontentloaded" });
  const id = await page.evaluate((n) => [...document.querySelectorAll("[data-browser-extension-token]")].find((row) => row.querySelector("[data-browser-extension-token-name]").textContent === n)?.dataset.browserExtensionToken, name);
  if (id) await postAs(page, "/settings/browser", { form: "revoke-token", id });
  return Boolean(id);
}

async function tokenUsed(page, name) {
  await page.goto(`${BASE}/settings/browser`, { waitUntil: "domcontentloaded" });
  return page.evaluate((n) => [...document.querySelectorAll("[data-browser-extension-token]")].find((row) => row.querySelector("[data-browser-extension-token-name]").textContent === n)?.querySelector("[data-browser-extension-token-dates]").textContent.replace(/\s+/g, " ").trim() || "", name);
}

async function download(page, browserName) {
  await page.goto(`${BASE}/settings/browser#settings-browser-extension`, { waitUntil: "domcontentloaded" });
  await L.dismissUpdate(page);
  const [file] = await Promise.all([page.waitForEvent("download"), page.click(`[data-browser-extension-zip=${browserName}]`)]);
  const saved = path.join(os.tmpdir(), `dc-ext-${TAG}-${browserName}.zip`);
  await file.saveAs(saved);
  return { name: file.suggestedFilename(), bytes: fs.readFileSync(saved) };
}

// The grants go into the manifest, no runner answers the browser's prompt.
function unpack(files, granted, change = {}) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), `dc-ext-${TAG}-`));
  for (const [name, data] of Object.entries({ ...files, ...change })) {
    fs.mkdirSync(path.dirname(path.join(dir, name)), { recursive: true });
    fs.writeFileSync(path.join(dir, name), data);
  }
  const manifest = JSON.parse(files["manifest.json"]);
  manifest.host_permissions = granted.map((one) => (one === "<all_urls>" || one.endsWith("/*") ? one : `${one}/*`));
  fs.writeFileSync(path.join(dir, "manifest.json"), JSON.stringify(manifest, null, 2));
  return dir;
}

// Without a Playwright viewport, so a page is as wide as the browser shows it
// beside the side panel, as a picture of the tab takes it.
// Extensions in beside load too, their workers go by another name.
async function launchWith(dir, { trustAll = true, scale = 0, args = [], beside = [], profile = fs.mkdtempSync(path.join(os.tmpdir(), `dc-ext-profile-${TAG}-`)) } = {}) {
  const dirs = [dir, ...beside].join(",");
  const context = await chromium.launchPersistentContext(profile, {
    channel: "chromium",
    ignoreHTTPSErrors: trustAll,
    viewport: scale ? null : { width: 1440, height: 900 },
    args: [`--disable-extensions-except=${dirs}`, `--load-extension=${dirs}`, "--enable-unsafe-extension-debugging", "--no-sandbox", ...(scale ? [`--force-device-scale-factor=${scale}`, "--window-size=1440,900"] : []), ...args],
  });
  const ours = (one) => one.url().endsWith("/background.js");
  let worker = context.serviceWorkers().find(ours);
  if (!worker) worker = await context.waitForEvent("serviceworker", { predicate: ours, timeout: 15000 });
  const id = new URL(worker.url()).host;
  // Chromium installs the extension of --load-extension on every start. Until
  // the install has opened its setup, a press can come before the worker
  // listens, or the setup's tab comes to the front after it.
  await until(() => context.pages().map((one) => one.url()), (urls) => urls.includes(`chrome-extension://${id}/options.html`), "the install opened no setup", 15000);
  const session = await context.browser().newBrowserCDPSession();
  return { context, id, session, worker, profile, quit: () => context.close().catch(() => {}), close: async () => { await context.close().catch(() => {}); fs.rmSync(profile, { recursive: true, force: true }); } };
}

async function press(ext, page) {
  const { targetInfos } = await ext.session.send("Target.getTargets", { filter: [{ type: "tab" }] });
  const url = page.url();
  const tab = targetInfos.find((one) => one.type === "tab" && one.url === url);
  if (!tab) throw new Error(`no tab for ${url}`);
  await page.bringToFront();
  await ext.session.send("Extensions.triggerAction", { id: ext.id, targetId: tab.targetId });
}

async function setupPage(ext) {
  const page = await ext.context.newPage();
  await page.goto(`chrome-extension://${ext.id}/options.html`);
  await page.waitForSelector("[data-setup-form]");
  return page;
}

// The box is set after the page read what is saved, which sets it too.
async function setUp(ext, address, token, { every = true } = {}) {
  const page = await setupPage(ext);
  await page.fill("[data-address]", address);
  await page.fill("[data-token]", token);
  await sleep(250);
  await page.setChecked("[data-every-page]", every);
  await page.click("[data-save]");
  const said = await until(() => page.textContent("[data-result]"), (t) => t && t !== "Checking…" && !t.startsWith("Allow the extension to reach"), "the setup never answered", 12000);
  return { page, said, error: await page.evaluate(() => document.querySelector("[data-result]").classList.contains("error")) };
}

const chips = (panel) => panel.eval(() => [...document.querySelectorAll("[data-chips] .chip")].map((chip) => ({ key: chip.dataset.key, label: chip.querySelector("[data-chip-show]").textContent, title: chip.querySelector("[data-chip-show]").title, active: chip.classList.contains("active"), gone: chip.classList.contains("gone") })));
const panelState = (panel) => panel.eval(() => ({
  picking: document.querySelector("[data-pick]").getAttribute("aria-pressed") === "true",
  parent: !document.querySelector("[data-parent]").disabled,
  child: !document.querySelector("[data-child]").disabled,
  hint: document.querySelector("[data-hint]").textContent,
  alert: document.querySelector("[data-alert]").hidden ? "" : document.querySelector("[data-alert]").textContent,
  status: ((el) => { const st = getComputedStyle(el); const b = el.getBoundingClientRect(); return st.display !== "none" && st.visibility !== "hidden" && b.width > 0 && b.height > 0 ? el.innerText : ""; })(document.querySelector("[data-status]")),
  sending: document.querySelector("[data-send]").textContent === "Sending…",
  note: document.querySelector("[data-note]").value,
  access: ((el) => { const st = getComputedStyle(el); const b = el.getBoundingClientRect(); return st.display !== "none" && st.visibility !== "hidden" && b.width > 0 && b.height > 0 ? el.textContent : ""; })(document.querySelector("[data-access]")),
}));

async function boxOf(page, selector) {
  const box = await page.locator(selector).boundingBox();
  if (!box) throw new Error(`${selector} has no box`);
  return box;
}

async function pickAt(page, selector) {
  const box = await boxOf(page, selector);
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await sleep(150);
  await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2);
}

module.exports = { PORT_A, PORT_B, SITE_A, SITE_A_AGAIN, SITE_B, EVERY_PAGE, TAG, PROJECT, ORIGIN, QUIET, SHOTS, NOTE, seen, start, unzip, Panel, overlay, readClosed, request, postAs, shown, until, makeToken, revokeToken, tokenUsed, download, unpack, launchWith, press, setupPage, setUp, chips, panelState, boxOf, pickAt };
