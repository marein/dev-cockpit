const L = require("./lib");
const { assert, BASE, sleep } = L;
const path = require("path");
const zlib = require("zlib");

// The transcript follows its end the way a chat does, on the owner's page and
// on a guest's of a shared link. Every check reads what a frame paints: a
// sampler installed before the page loads re-arms a fresh ResizeObserver on
// the scroller in every animation frame, and a fresh observer is delivered
// after the page's own ones, after the layout and right before the paint. A
// jump that comes one frame late shows there as a frame off the end.
//
// Headless is fast, so a late frame hides: Chromium runs under a fourfold CPU
// throttle (CDP Emulation.setCPUThrottlingRate), the element's module is held
// back for the first paint checks (page.route), and the picture of the growth
// check arrives late, after its answer stands.
//
// - first paint: a full load stands at the end from its first frame, also with
//   a multi line draft in the composer, a #message address stands on that
//   message, and a boosted open on a message's address and Show earlier
//   messages keep the message where it belongs on every frame,
// - growth: a streamed answer and a picture without a size that decodes late
//   are followed on every frame, and the composer growing under a follower,
// - a send: a message sent through the composer stands at the end on every
//   frame until its answer, also when the pull of the sent message lands
//   between the same two frames as the send's answer (handed),
// - intent: a small wheel, a finger drag, an arrow key and a scroll the page
//   cannot attribute let go at once and the page stays put while the answer
//   streams, a stop short of the end stays, the real end follows again, also
//   when scroll events alone bring the reader there (an overlay scrollbar, an
//   autoscroll, a selection), and input that leaves the reader at the end (a
//   jerk of the wheel 2px up, a finger travelling 12px without a scroll or
//   moving the page 2px up) keeps following through a message written
//   elsewhere and its streamed answer,
// - the jump to end button: hidden exactly while the reader follows, shown
//   whenever they do not, after a plain scroll up as well as while an answer
//   streams, at the transcript's bottom right over the composer, a tap jumps
//   to the end and follows (SHOTS_DIR takes button-desktop.png and
//   button-phone.png after a plain scroll up, chromium),
// - a drag on the scrollbar's thumb lets go and a drag to the end follows
//   again, in a Chromium of its own that shows its scrollbars (headless hides
//   them, WebKit's are overlays without events),
// - a swap of the owner's surface (syncFromServer) keeps the reader's place,
//   or the follow,
// - the guest's conversation (dc-share-chat) does the same.
//
// Instance: like assistant.js's (tests/e2e/fakes first on PATH for the fake
// claude, a scratch HOME). The run makes one assistant and a link of it, and
// deletes both. Run with ENGINE=chromium,webkit.

const TAG = Date.now().toString(36).slice(-5);
const DESKTOP = { viewport: { width: 1440, height: 900 } };
const PHONE = { hasTouch: true, isMobile: true, viewport: { width: 390, height: 844 } };
const SHOTS_DIR = process.env.SHOTS_DIR || "";
const PIC = "https://pics.invalid/tall.png";
const MODULE = /\/js\/components\/(assistant|share-chat)\.[0-9a-f]{12}\.js$/;
const BUTTON = "[data-chat-end]";
// A list, so every line the fake streams stands on a line of its own and the
// thread grows with every delta, 0.4s apart, for 24s.
const ANSWER = Array.from({ length: 60 }, (_, i) => `- line ${i + 1} of an answer that keeps coming`).join("\n");
// Its own words, so a later wait for a line of ANSWER does not find it.
const SHORT = Array.from({ length: 14 }, (_, i) => `- step ${i + 1} of a shorter answer`).join("\n");

function png(width, height) {
  const table = Array.from({ length: 256 }, (_, n) => {
    let c = n;
    for (let k = 0; k < 8; k += 1) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    return c >>> 0;
  });
  const crc = (buf) => {
    let c = 0xffffffff;
    for (const b of buf) c = table[(c ^ b) & 0xff] ^ (c >>> 8);
    return (c ^ 0xffffffff) >>> 0;
  };
  const chunk = (type, data) => {
    const len = Buffer.alloc(4);
    len.writeUInt32BE(data.length);
    const body = Buffer.concat([Buffer.from(type), data]);
    const sum = Buffer.alloc(4);
    sum.writeUInt32BE(crc(body));
    return Buffer.concat([len, body, sum]);
  };
  const head = Buffer.alloc(13);
  head.writeUInt32BE(width, 0);
  head.writeUInt32BE(height, 4);
  head[8] = 8;
  head[9] = 2;
  const row = Buffer.alloc(1 + width * 3, 160);
  row[0] = 0;
  const raw = Buffer.concat(Array.from({ length: height }, () => row));
  return Buffer.concat([Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]), chunk("IHDR", head), chunk("IDAT", zlib.deflateSync(raw)), chunk("IEND", Buffer.alloc(0))]);
}

// The sampler: one entry per painted frame while window.__follow.on, the
// scroller's rest to its end, the message named in probe measured against the
// scroller's top, and whether the surface's element has upgraded yet.
function sampler() {
  const hash = window.location.hash;
  const state = { on: true, frames: [], probe: hash.startsWith("#message-") ? hash.slice("#message-".length) : "", mark: "" };
  window.__follow = state;
  const ids = new WeakMap();
  let next = 1;
  const read = (scroller) => {
    if (!state.on || !scroller.isConnected) return;
    const host = scroller.closest("dc-assistant, dc-share-chat");
    if (!ids.has(scroller)) ids.set(scroller, next++);
    const top = scroller.getBoundingClientRect().top;
    const probe = state.probe ? document.getElementById(`message-${state.probe}`) : null;
    const button = document.querySelector("[data-chat-end]");
    state.frames.push({
      mark: state.mark,
      surface: ids.get(scroller),
      rest: Math.round(scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight),
      top: Math.round(scroller.scrollTop),
      height: scroller.scrollHeight,
      defined: !!host && host.matches(":defined"),
      probe: probe ? Math.round(probe.getBoundingClientRect().top - top) : null,
      screen: probe ? Math.round(probe.getBoundingClientRect().top) : null,
      button: !!button && getComputedStyle(button).display !== "none",
    });
  };
  const arm = () => {
    const scroller = document.querySelector("[data-assistant-scroll], [data-share-scroll]");
    if (!scroller) {
      window.requestAnimationFrame(arm);
      return;
    }
    const observer = new ResizeObserver(() => {
      observer.disconnect();
      read(scroller);
      window.requestAnimationFrame(arm);
    });
    observer.observe(scroller);
  };
  window.requestAnimationFrame(arm);
}

const frames = (page) => page.evaluate(() => window.__follow.frames.splice(0));
const mark = (page, label) => page.evaluate((m) => { window.__follow.mark = m; }, label);
const sampling = (page, on) => page.evaluate((v) => { window.__follow.on = v; }, on);

function off(list, test, what) {
  const bad = list.filter((f) => !test(f));
  assert(!bad.length, `${bad.length} of ${list.length} frames ${what}: ${JSON.stringify(bad.slice(0, 4))}`);
}

async function postAs(page, url, body) {
  return page.evaluate(async ({ url, body }) => {
    const token = document.querySelector('meta[name="csrf-token"]')?.content || "";
    const res = await fetch(url, { method: "POST", headers: { "X-CSRF-Token": token, Accept: "application/json" }, body: new URLSearchParams(body) });
    return { ok: res.ok, status: res.status, text: await res.text() };
  }, { url, body });
}

async function postJSON(page, url, body) {
  const res = await postAs(page, url, body);
  assert(res.ok, `POST ${url} answered ${res.status}: ${res.text.slice(0, 200)}`);
  return res.text.trim().startsWith("{") ? JSON.parse(res.text) : {};
}

async function waitIdle(page, id) {
  const until = Date.now() + 40000;
  while (Date.now() < until) {
    const html = await page.evaluate(async (p) => (await fetch(p)).text(), `/assistants/${id}`);
    if (!/<dc-assistant\b[^>]*\srunning[\s>]/.test(html)) return;
    await sleep(300);
  }
  throw new Error(`the assistant ${id} never finished its turn`);
}

async function say(page, id, text) {
  await postJSON(page, `/assistants/${id}`, { form: "message", message: text });
  await sleep(300);
  await waitIdle(page, id);
}

const fx = {};

// opened is a page of its own under the sampler, signed in as the owner or
// bare for a guest, with the CPU throttled where the engine allows it, the
// element's module held back when asked and the late picture served late.
async function opened(browser, engine, bag, options, { owner = true, hold = false } = {}) {
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true, ...(owner ? { storageState: fx.state } : {}), ...options });
  const page = await ctx.newPage();
  L.wirePage(page, bag);
  await page.addInitScript(sampler);
  await page.route(PIC, async (route) => {
    await sleep(1500);
    await route.fulfill({ contentType: "image/png", body: fx.png }).catch(() => {});
  });
  if (hold) {
    await page.route(MODULE, async (route) => {
      await sleep(900);
      await route.continue().catch(() => {});
    });
  }
  let cdp = null;
  if (engine === "chromium") {
    cdp = await ctx.newCDPSession(page);
    await cdp.send("Emulation.setCPUThrottlingRate", { rate: 4 });
  }
  return { ctx, page, cdp };
}

async function landed(page, url, ready) {
  await page.goto(url, { waitUntil: "domcontentloaded" });
  await L.dismissUpdate(page);
  await page.waitForSelector(ready, { timeout: 20000 });
  await sleep(900);
  await sampling(page, false);
  return frames(page);
}

// ready is the surface's own sign that it stands: the owner's element marks
// itself, the guest's has no mark, its stream connecting is the sign.
const READY = { owner: "dc-assistant[ready]", guest: "dc-share-chat:defined" };

async function scrollerBox(page) {
  return page.$eval("[data-assistant-scroll], [data-share-scroll]", (s) => {
    const r = s.getBoundingClientRect();
    return { x: r.left + r.width / 2, y: r.top + r.height / 2 };
  });
}

// move scrolls the reader by dy, up when it is negative, with the gesture
// of the form: the wheel on a desktop, a finger on a phone, in Chromium as
// CDP touches and in WebKit, which has neither a touch driver nor a wheel on
// a phone and refuses to construct a Touch, as plain touch events carrying
// their touches, each with the scroll the finger makes. A finger moves
// at most 400px a stroke and rests before it lifts, so nothing flings.
async function move(live, form, dy) {
  const { page, cdp } = live;
  const at = await scrollerBox(page);
  if (form === "desktop") {
    await page.mouse.move(at.x, at.y);
    await page.mouse.wheel(0, dy);
    return "the wheel";
  }
  for (let left = dy; Math.abs(left) > 0.5;) {
    const stroke = Math.max(-400, Math.min(400, left));
    left -= stroke;
    const from = at.y + stroke / 2;
    const to = at.y - stroke / 2;
    if (cdp) {
      await cdp.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ x: at.x, y: from }] });
      for (let i = 1; i <= 8; i += 1) {
        await cdp.send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ x: at.x, y: from + ((to - from) * i) / 8 }] });
        await sleep(30);
      }
      await sleep(250);
      await cdp.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
    } else {
      await page.evaluate(async ({ x, from, to }) => {
        const s = document.querySelector("[data-assistant-scroll], [data-share-scroll]");
        const frame = () => new Promise((r) => requestAnimationFrame(r));
        const fire = (type, y, down) => {
          const event = new Event(type, { bubbles: true });
          const touches = down ? [{ identifier: 1, target: s, clientX: x, clientY: y }] : [];
          Object.defineProperty(event, "touches", { value: touches });
          s.dispatchEvent(event);
        };
        fire("touchstart", from, true);
        for (let i = 1; i <= 8; i += 1) {
          const y = from + ((to - from) * i) / 8;
          fire("touchmove", y, true);
          s.scrollTop -= (to - from) / 8;
          await frame();
        }
        await new Promise((r) => setTimeout(r, 250));
        fire("touchend", to, false);
      }, { x: at.x, from, to });
    }
    await sleep(100);
  }
  return "a finger";
}

// finger lays a finger on the middle of the thread and moves it 12px down,
// in 3px steps, rests and lifts it, while the page moves up by the given
// pixels: in Chromium a CDP touch short of the slop that starts a scroll,
// the move the page's scrollTop, in WebKit touch events like move's.
async function finger(live, by) {
  const { page, cdp } = live;
  const at = await scrollerBox(page);
  if (cdp) {
    await cdp.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ x: at.x, y: at.y }] });
    for (let i = 1; i <= 4; i += 1) {
      await cdp.send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ x: at.x, y: at.y + 3 * i }] });
      await sleep(30);
    }
    if (by) await page.evaluate((d) => { document.querySelector("[data-assistant-scroll], [data-share-scroll]").scrollTop -= d; }, by);
    await sleep(250);
    await cdp.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
    return;
  }
  await page.evaluate(async ({ x, y, by }) => {
    const s = document.querySelector("[data-assistant-scroll], [data-share-scroll]");
    const frame = () => new Promise((r) => requestAnimationFrame(r));
    const fire = (type, at, down) => {
      const event = new Event(type, { bubbles: true });
      const touches = down ? [{ identifier: 1, target: s, clientX: x, clientY: at }] : [];
      Object.defineProperty(event, "touches", { value: touches });
      s.dispatchEvent(event);
    };
    fire("touchstart", y, true);
    for (let i = 1; i <= 4; i += 1) {
      fire("touchmove", y + 3 * i, true);
      await frame();
    }
    s.scrollTop -= by;
    await new Promise((r) => setTimeout(r, 250));
    fire("touchend", y + 12, false);
  }, { x: at.x, y: at.y, by });
}

// Input that leaves a follower at the end, per form.
const NUDGES = {
  desktop: [["a jerk of the wheel 2px up", async ({ page: p }) => {
    const at = await scrollerBox(p);
    await p.mouse.move(at.x, at.y);
    await p.mouse.wheel(0, -2);
  }]],
  phone: [
    ["a finger travelling 12px without a scroll", (live) => finger(live, 0)],
    ["a finger travelling 12px that moves the page 2px up", (live) => finger(live, 2)],
  ],
};

// kept nudges a follower at the end and checks they still follow: the button
// stays hidden, and a message written on the owner's page and its streamed
// answer stand at the end on every frame, the button hidden on each.
async function kept(live, form, owner) {
  const p = live.page;
  const said = [];
  for (const [what, nudge] of NUDGES[form]) {
    await nudge(live);
    await sleep(400);
    const at = await place(p);
    assert(at.rest <= 2, `${what} took the reader ${at.rest}px off the end, the check says nothing`);
    const b = await shown(p);
    assert(b && !b.visible, `the button shows after ${what} at the end: ${JSON.stringify(b)}`);
    said.push(`${what} (${at.rest}px off)`);
  }
  await sampling(p, true);
  await frames(p);
  const before = await place(p);
  await postJSON(owner, `/assistants/${fx.id}`, { form: "message", message: `ECHO_ANSWER ${TAG}\n${SHORT}` });
  await p.waitForFunction(() => {
    const last = document.querySelector("[data-assistant-log], [data-share-log]")?.lastElementChild;
    return last?.dataset.role === "assistant" && (last.textContent || "").includes("step 6 of");
  }, null, { timeout: 20000 });
  await sleep(600);
  const list = await frames(p);
  const after = await place(p);
  assert(after.height > before.height + 300, `the thread grew from ${before.height} to ${after.height} only, the check says nothing`);
  assert(list.length > 3, `only ${list.length} frames were sampled`);
  off(list, (f) => f.rest <= 2, `after ${said.join(", ")} stood off the end`);
  off(list, (f) => !f.button, "showed the button while following");
  await waitIdle(owner, fx.id);
  return `${said.join(", ")}, ${list.length} frames at the end`;
}

// still waits until a scroll the reader started has come to rest: WebKit's
// wheel runs on for a while after its last step.
async function still(page) {
  let last = null;
  for (let calm = 0, i = 0; calm < 3 && i < 40; i += 1) {
    await sleep(100);
    const now = await page.$eval("[data-assistant-scroll], [data-share-scroll]", (s) => s.scrollTop);
    calm = now === last ? calm + 1 : 0;
    last = now;
  }
}

async function toEnd(live, form) {
  for (let i = 0; i < 20; i += 1) {
    const now = await place(live.page);
    if (now.rest <= 2) return;
    await move(live, form, form === "desktop" ? 8000 : now.rest + 100);
  }
}

const place = (page) => page.$eval("[data-assistant-scroll], [data-share-scroll]", (s) => ({
  top: Math.round(s.scrollTop),
  height: s.scrollHeight,
  rest: Math.round(s.scrollHeight - s.scrollTop - s.clientHeight),
}));

const shown = (page) => page.evaluate((sel) => {
  const b = document.querySelector(sel);
  if (!b) return null;
  const r = b.getBoundingClientRect();
  const s = document.querySelector("[data-assistant-scroll], [data-share-scroll]").getBoundingClientRect();
  const visible = getComputedStyle(b).display !== "none" && r.width > 0 && r.height > 0;
  return {
    visible,
    inside: r.top >= 0 && r.bottom <= window.innerHeight && r.left >= 0 && r.right <= window.innerWidth,
    width: Math.round(r.width),
    height: Math.round(r.height),
    right: Math.round(s.right - r.right),
    bottom: Math.round(s.bottom - r.bottom),
  };
}, BUTTON);

// cornered reads the button at the transcript's bottom right: inside the
// scroller's box, close to its right edge and its foot, so the composer below
// the scroller stays clear.
function cornered(b, what) {
  assert(b && b.visible && b.inside, `${what} is ${JSON.stringify(b)}`);
  assert(b.right >= 0 && b.right <= 48 && b.bottom >= 0 && b.bottom <= 32, `${what} does not stand at the transcript's bottom right: ${JSON.stringify(b)}`);
}

// plainly scrolls up by 300px with nothing streaming, checks the button at the
// bottom right, and taps it back to the end, where it hides again.
async function plainly(live, form, what) {
  const p = live.page;
  const before = await shown(p);
  assert(before && !before.visible, `${what} shows at the end: ${JSON.stringify(before)}`);
  const by = await move(live, form, -300);
  await still(p);
  const at = await place(p);
  assert(at.rest > 10, `the reader is back at the end (${at.rest}px), the move did not take`);
  const b = await shown(p);
  cornered(b, what);
  if (form === "phone") assert(b.width >= 44 && b.height >= 44, `${what} is ${b.width}x${b.height} on a phone`);
  return { by, b };
}

async function tapped(page, what) {
  await page.locator(BUTTON).click();
  await sleep(400);
  const now = await place(page);
  assert(now.rest <= 2, `the tap left the reader ${now.rest}px off the end`);
  const b = await shown(page);
  assert(b && !b.visible, `${what} still shows at the end: ${JSON.stringify(b)}`);
}

// stays watches the reader's place for a while the answer keeps streaming:
// the place must not move while the thread grows under it.
async function stays(page, ms, what) {
  const before = await place(page);
  await sleep(ms);
  const after = await place(page);
  assert(Math.abs(after.top - before.top) <= 1, `${what}: the page moved from ${before.top} to ${after.top} (rest ${before.rest} to ${after.rest})`);
  assert(after.height > before.height, `${what}: the answer did not grow meanwhile (${before.height} to ${after.height}), the check says nothing`);
  return after;
}

async function follows(page, ms, what) {
  await sleep(300);
  await frames(page);
  const before = await place(page);
  await sleep(ms);
  const list = await frames(page);
  const after = await place(page);
  assert(after.height > before.height, `${what}: the answer did not grow meanwhile, the check says nothing`);
  assert(list.length > 3, `${what}: only ${list.length} frames were sampled`);
  off(list, (f) => f.rest <= 2, `${what} stood off the end`);
}

// handed holds back the pull of a sent message, the one the stream's
// announcement starts, and hands it over right after the page handled the
// send's answer, between the same two frames: the held pull answers in
// microtasks, a Response's body would take a task in some engines. The send's
// answer waits for the announcement's pull and the answer's bubble, the order
// of a fast stream, and for a frame painted with them.
function handed() {
  const surface = document.querySelector("dc-assistant");
  const log = surface.querySelector("[data-assistant-log]");
  const pulls = new URL(surface.getAttribute("message-url"), location.href).href;
  const real = window.fetch;
  const seam = { handed: null, landed: null };
  window.__seam = seam;
  let frame = 0;
  const tick = () => {
    frame += 1;
    requestAnimationFrame(tick);
  };
  requestAnimationFrame(tick);
  let held = false;
  let hand = null;
  const ready = () => hand && log.querySelector('[data-role="assistant"][data-state="streaming"]');
  window.fetch = async (input, init = {}) => {
    if (init.method === "POST" && String(init.body).includes("form=message")) {
      const response = await real(input, init);
      const body = await response.text();
      while (!ready()) await new Promise((r) => setTimeout(r, 10));
      await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
      const watch = new MutationObserver(() => {
        watch.disconnect();
        seam.handed = frame;
        hand();
      });
      watch.observe(log, { childList: true });
      return new Response(body, { status: response.status, headers: response.headers });
    }
    if (!held && new URL(String(input), location.href).href.startsWith(pulls)) {
      held = true;
      const response = await real(input, init);
      const body = await response.text();
      await new Promise((r) => { hand = r; });
      return {
        ok: response.ok,
        status: response.status,
        headers: response.headers,
        text: async () => {
          seam.landed = frame;
          return body;
        },
      };
    }
    return real(input, init);
  };
}

async function streaming(page, id) {
  await postJSON(page, `/assistants/${id}`, { form: "message", message: `ECHO_ANSWER ${TAG}\n${ANSWER}` });
}

const guestURL = () => `${fx.link}/assistants/${fx.id}`;

L.runFeature("assistant-follow", async ({ engine, browser, page, run, bag }) => {
  if (!fx.id) {
    await run("the owner builds a thread longer than its window and shares it", async () => {
      fx.png = png(320, 960);
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      fx.id = (await postJSON(page, "/assistants/new", { form: "new", coder: "claude" })).id;
      assert(fx.id, "no assistant was made");
      const lines = Array.from({ length: 6 }, (_, i) => `a line ${i} of a question that takes some room`).join("\n");
      for (let i = 0; i < 12; i += 1) await say(page, fx.id, `MAGIC ${TAG} ${i}\n${lines}`);
      const res = await postAs(page, "/settings/sharing/new", [["amount", "1"], ["unit", "hours"], ["name", `follow-${TAG}`], ["assistant", fx.id]]);
      fx.link = (res.text.match(/value="([^"]*\/share\/[^"]+)"/) || [])[1];
      assert(res.ok && fx.link, `making the link answered ${res.status}`);
      fx.linkId = (res.text.match(/\/settings\/sharing\/([0-9a-z-]+)/) || [])[1] || "";
      fx.state = await page.context().storageState();
    });
  }
  if (!fx.id) return;
  const ownerURL = `${BASE}/assistants/${fx.id}`;

  for (const [form, options] of [["desktop", DESKTOP], ["phone", PHONE]]) {
    await run(`${form}: a full load paints its first frame at the end, with a draft of five lines in the composer`, async () => {
      await postJSON(page, `/assistants/${fx.id}`, { form: "draft", message: "one\ntwo\nthree\nfour\nfive" });
      const { ctx, page: p } = await opened(browser, engine, bag, options, { hold: true });
      try {
        const list = await landed(p, ownerURL, READY.owner);
        const early = list.filter((f) => !f.defined);
        assert(early.length, "no frame was painted before the element upgraded, the hold did not hold");
        off(list, (f) => f.rest <= 2, "stood off the end");
        const box = await p.$eval("[data-assistant-input]", (n) => n.getBoundingClientRect().height);
        assert(box > 60, `the composer did not grow for the draft (${box}px), the check says nothing`);
        return `${list.length} frames at the end, ${early.length} of them before the upgrade`;
      } finally {
        await ctx.close();
        await postJSON(page, `/assistants/${fx.id}`, { form: "draft", message: "" });
      }
    });

    await run(`${form}: a full load of a message's address paints every frame on that message`, async () => {
      const ids = await page.evaluate(async (u) => {
        const doc = new DOMParser().parseFromString(await (await fetch(u)).text(), "text/html");
        return [...doc.querySelectorAll("[data-assistant-log] [data-message-id]")].map((n) => n.dataset.messageId);
      }, ownerURL);
      fx.target = ids[4];
      assert(fx.target, "the window holds too few messages");
      const { ctx, page: p } = await opened(browser, engine, bag, options, { hold: true });
      try {
        const list = (await landed(p, `${ownerURL}#message-${fx.target}`, READY.owner)).filter((f) => f.probe !== null);
        assert(list.length && list.some((f) => !f.defined), "no frame was painted before the element upgraded");
        off(list, (f) => Math.abs(f.probe) <= 4, "stood off the message");
        return `${list.length} frames on the message`;
      } finally {
        await ctx.close();
      }
    });

    await run(`${form}: a boosted open of a message's address paints no frame anywhere else`, async () => {
      const { ctx, page: p } = await opened(browser, engine, bag, options);
      try {
        await landed(p, ownerURL, READY.owner);
        await p.evaluate((id) => { window.__follow.probe = id; window.__follow.frames.splice(0); window.__follow.on = true; }, fx.target);
        await p.evaluate((u) => window.app.navigate(u), `/assistants/${fx.id}#message-${fx.target}`);
        await p.waitForURL(/#message-/, { timeout: 15000 });
        await sleep(900);
        const list = (await frames(p)).filter((f) => f.surface > 1 && f.probe !== null);
        assert(list.length > 3, `only ${list.length} frames of the new surface`);
        off(list, (f) => Math.abs(f.probe) <= 4, "stood off the message");
        return `${list.length} frames on the message`;
      } finally {
        await ctx.close();
      }
    });

    await run(`${form}: show earlier messages keeps the reading place on every frame`, async () => {
      const opening = await opened(browser, engine, bag, options);
      const { ctx, page: p } = opening;
      try {
        await landed(p, ownerURL, READY.owner);
        await move(opening, form, -1200);
        await still(p);
        const anchor = await p.evaluate(() => {
          const s = document.querySelector("[data-assistant-scroll]");
          const top = s.getBoundingClientRect().top;
          const m = [...s.querySelectorAll("[data-assistant-message][data-message-id]")].find((n) => n.getBoundingClientRect().bottom > top);
          return { id: m.dataset.messageId, screen: Math.round(m.getBoundingClientRect().top) };
        });
        await p.evaluate((id) => { window.__follow.probe = id; window.__follow.frames.splice(0); window.__follow.on = true; }, anchor.id);
        await p.evaluate(() => document.querySelector("[data-assistant-all]").click());
        await p.waitForURL(/all=1/, { timeout: 15000 });
        await sleep(900);
        const list = (await frames(p)).filter((f) => f.surface > 1 && f.screen !== null);
        assert(list.length > 3, `only ${list.length} frames of the whole transcript`);
        off(list, (f) => Math.abs(f.screen - anchor.screen) <= 2, `moved the message off ${anchor.screen}px`);
        return `${list.length} frames, the message stood at ${anchor.screen}px`;
      } finally {
        await ctx.close();
      }
    });

    await run(`${form}: a sent message stands at the end on every frame until its answer, also when its pull lands with the send's answer`, async () => {
      await waitIdle(page, fx.id);
      const { ctx, page: p } = await opened(browser, engine, bag, options);
      const words = `LATER ${TAG} ${engine} ${form}`;
      try {
        await landed(p, ownerURL, READY.owner);
        await p.evaluate(handed);
        await p.fill("[data-assistant-input]", words);
        await sleep(400);
        await sampling(p, true);
        await frames(p);
        if (form === "desktop") await p.press("[data-assistant-input]", "Enter");
        else await p.tap("[data-assistant-send]");
        await p.waitForFunction(() => /AIRPORT/.test(document.querySelector("[data-assistant-log] > :last-child")?.textContent || ""), null, { timeout: 20000 });
        await sleep(600);
        const list = await frames(p);
        const seam = await p.evaluate(() => window.__seam);
        assert(seam.handed !== null && seam.landed === seam.handed, `the pull of the sent message did not land with the send's answer (${JSON.stringify(seam)}), the check says nothing`);
        assert(list.length > 30, `only ${list.length} frames were sampled`);
        off(list, (f) => f.rest <= 2, "stood off the end");
        const sent = await p.$$eval("[data-assistant-log] [data-role=user]", (nodes, w) => nodes.filter((n) => n.textContent.includes(w)).length, words);
        assert(sent === 1, `the sent message stands ${sent} times in the thread`);
        return `${list.length} frames at the end`;
      } finally {
        await ctx.close();
        await waitIdle(page, fx.id);
      }
    });

    let live = null;
    try {
      await run(`${form}: a streamed answer, a late picture and the growing composer are followed on every frame`, async () => {
        live = await opened(browser, engine, bag, options);
        const p = live.page;
        await landed(p, ownerURL, READY.owner);
        await sampling(p, true);
        await mark(p, "picture");
        await postJSON(p, `/assistants/${fx.id}`, { form: "message", message: `ECHO_ANSWER ${TAG}\none\ntwo\nthree\n![pic](${PIC})\nfour\nfive\nsix\nseven\neight\nnine\nten` });
        await p.waitForFunction(() => document.querySelector("dc-assistant")?.hasAttribute("running"), null, { timeout: 15000 });
        await p.waitForFunction(() => !document.querySelector("dc-assistant")?.hasAttribute("running"), null, { timeout: 30000 });
        await p.waitForFunction(() => [...document.querySelectorAll("[data-assistant-log] img")].some((i) => i.complete && i.naturalHeight > 0), null, { timeout: 15000 });
        await sleep(900);
        await mark(p, "composer");
        await p.fill("[data-assistant-input]", "one\ntwo\nthree\nfour\nfive\nsix");
        await sleep(900);
        await p.fill("[data-assistant-input]", "");
        await sleep(600);
        const list = await frames(p);
        const grown = list.filter((f) => f.mark === "picture").map((f) => f.height);
        assert(Math.max(...grown) - Math.min(...grown) > 900, "the picture did not grow the thread, the check says nothing");
        off(list, (f) => f.rest <= 2, "stood off the end");
        return `${list.length} frames at the end`;
      });

      await run(`${form}: a plain scroll up shows the jump to end button at the bottom right, a tap goes back to the end`, async () => {
        const { by, b } = await plainly(live, form, "the button");
        if (SHOTS_DIR && engine === "chromium") await live.page.screenshot({ path: path.join(SHOTS_DIR, `button-${form}.png`) });
        await tapped(live.page, "the button");
        return `${by}, ${b.width}x${b.height}, ${b.right}px from the right, ${b.bottom}px over the foot`;
      });

      await run(`${form}: input that leaves the reader at the end keeps following through the next message and its answer`, async () => kept(live, form, page));

      await run(`${form}: a small move up while the answer streams lets go at once and the page stays`, async () => {
        const p = live.page;
        await streaming(p, fx.id);
        await p.waitForFunction(() => (document.querySelector("[data-assistant-log] > :last-child")?.textContent || "").includes("line 3 of"), null, { timeout: 20000 });
        const by = await move(live, form, -40);
        await sleep(400);
        const at = await stays(p, 1500, `after ${by}`);
        assert(at.rest > 10, `the reader is back at the end (${at.rest}px), the move did not take`);
        return `${by} moved ${at.rest}px up and stayed`;
      });

      await run(`${form}: the jump to end button shows away from the end while it streams`, async () => {
        const b = await shown(live.page);
        cornered(b, "the button");
        return `${b.width}x${b.height}`;
      });

      await run(`${form}: a stop short of the end stays, the real end follows again and hides the button`, async () => {
        const p = live.page;
        const now = await place(p);
        await move(live, form, now.rest - 20);
        await sleep(500);
        await stays(p, 1200, "twenty pixels short of the end");
        await toEnd(live, form);
        await sleep(600);
        await follows(p, 1500, "back at the end");
        const b = await shown(p);
        assert(b && !b.visible, `the button still shows at the end: ${JSON.stringify(b)}`);
      });

      // Scroll events with no input before them, the way an overlay scrollbar,
      // a middle click autoscroll or a selection move the thread. The last
      // frames stay at the end, a thumb held there, so a delta landing between
      // a step and its scroll event cannot hide the pin.
      await run(`${form}: scroll events alone back to the end follow again while it streams`, async () => {
        const p = live.page;
        await p.evaluate(() => { document.querySelector("[data-assistant-scroll]").scrollTop -= 200; });
        await sleep(400);
        const away = await stays(p, 1000, "after a scroll from outside");
        await p.evaluate(async () => {
          const s = document.querySelector("[data-assistant-scroll]");
          const frame = () => new Promise((r) => requestAnimationFrame(r));
          for (let left = 6; left > 0; left -= 1) {
            s.scrollTop += (s.scrollHeight - s.clientHeight - s.scrollTop) / left;
            await frame();
          }
          for (let i = 0; i < 3; i += 1) {
            s.scrollTop = s.scrollHeight;
            await frame();
          }
        });
        await follows(p, 1500, "after scroll events alone");
        const b = await shown(p);
        assert(b && !b.visible, `the button still shows at the end: ${JSON.stringify(b)}`);
        return `${away.rest}px away, back at the end and followed`;
      });

      await run(`${form}: a key and a scroll nobody can attribute let go as well`, async () => {
        const p = live.page;
        const said = [];
        if (form === "desktop") {
          await p.click("[data-assistant-log] > :last-child [data-assistant-text]", { position: { x: 5, y: 5 } });
          await p.keyboard.press("ArrowUp");
          await sleep(400);
          await stays(p, 1200, "after ArrowUp");
          said.push("ArrowUp");
          await toEnd(live, form);
          await sleep(600);
        }
        await p.evaluate(() => { document.querySelector("[data-assistant-scroll]").scrollTop -= 50; });
        await sleep(400);
        await stays(p, 1200, "after a scroll from outside");
        said.push("a scroll from outside");
        return said.join(", ");
      });

      await run(`${form}: a tap on the button jumps to the end and follows`, async () => {
        const p = live.page;
        await p.locator(BUTTON).click();
        await follows(p, 1500, "after the tap");
        const b = await shown(p);
        assert(b && !b.visible, `the button still shows at the end: ${JSON.stringify(b)}`);
      });

      await run(`${form}: away from the end the button outlasts the answer, the end hides it`, async () => {
        const p = live.page;
        await move(live, form, -300);
        await p.waitForFunction(() => !document.querySelector("dc-assistant")?.hasAttribute("running"), null, { timeout: 30000 });
        await sleep(600);
        const b = await shown(p);
        assert(b && b.visible, `the button is gone after the answer ended away from the end: ${JSON.stringify(b)}`);
        await toEnd(live, form);
        await sleep(800);
        const after = await shown(p);
        assert(after && !after.visible, `the button still shows at the end: ${JSON.stringify(after)}`);
      });

      await run(`${form}: a swap from the server keeps the reader's place, and the follow at the end`, async () => {
        const p = live.page;
        const swap = async () => {
          const old = await p.evaluateHandle(() => document.querySelector("dc-assistant"));
          await p.evaluate(() => {
            const surface = document.querySelector("dc-assistant");
            surface.setAttribute("blocked", "");
            return surface.syncFromServer();
          });
          await p.waitForFunction((o) => document.querySelector("dc-assistant") !== o && document.querySelector("dc-assistant[ready]"), old, { timeout: 15000 });
          await sleep(700);
        };
        await move(live, form, -700);
        await still(p);
        const anchor = await p.evaluate(() => {
          const s = document.querySelector("[data-assistant-scroll]");
          const top = s.getBoundingClientRect().top;
          const m = [...s.querySelectorAll("[data-assistant-message][data-message-id]")].find((n) => n.getBoundingClientRect().bottom > top);
          return { id: m.dataset.messageId, screen: Math.round(m.getBoundingClientRect().top) };
        });
        await p.evaluate((id) => { window.__follow.probe = id; window.__follow.frames.splice(0); }, anchor.id);
        await swap();
        const list = (await frames(p)).filter((f) => f.surface > 1 && f.screen !== null);
        assert(list.length > 3, `only ${list.length} frames of the swapped surface`);
        off(list, (f) => Math.abs(f.screen - anchor.screen) <= 2, `moved the message off ${anchor.screen}px`);
        await toEnd(live, form);
        await sleep(700);
        await frames(p);
        await swap();
        off((await frames(p)).filter((f) => f.surface > 2), (f) => f.rest <= 2, "stood off the end after the second swap");
        return `${list.length} frames kept the place, the follower stayed at the end`;
      });
    } finally {
      await live?.ctx.close();
    }
  }

  // Headless Chromium hides its scrollbars, this one shows them, so the thumb
  // can be dragged like a reader drags it.
  if (engine === "chromium") {
    await run("desktop: a drag on the scrollbar lets go, a drag back to the end follows again", async () => {
      const shown = await L.ENGINES.chromium.launch({ args: ["--no-sandbox"], ignoreDefaultArgs: ["--hide-scrollbars"] });
      try {
        const bar = await opened(shown, engine, bag, DESKTOP);
        const p = bar.page;
        await landed(p, ownerURL, READY.owner);
        await sampling(p, true);
        const gutter = await p.$eval("[data-assistant-scroll]", (s) => s.offsetWidth - s.clientWidth);
        assert(gutter > 0, "the scroller shows no scrollbar, the check says nothing");
        await streaming(p, fx.id);
        await p.waitForFunction(() => (document.querySelector("[data-assistant-log] > :last-child")?.textContent || "").includes("line 3 of"), null, { timeout: 20000 });
        const thumb = () => p.$eval("[data-assistant-scroll]", (s) => {
          const r = s.getBoundingClientRect();
          const length = Math.max(20, (s.clientHeight * s.clientHeight) / s.scrollHeight);
          const top = r.top + (s.scrollTop / (s.scrollHeight - s.clientHeight)) * (s.clientHeight - length);
          return { x: r.right - (s.offsetWidth - s.clientWidth) / 2, y: top + length / 2 };
        });
        const at = await thumb();
        await p.mouse.move(at.x, at.y);
        await p.mouse.down();
        await p.mouse.move(at.x, at.y - 30, { steps: 5 });
        await sleep(300);
        const held = await stays(p, 1200, "while the thumb is held off the end");
        await p.mouse.up();
        await stays(p, 1000, "after the thumb was let go off the end");
        const back = await thumb();
        await p.mouse.move(back.x, back.y);
        await p.mouse.down();
        await p.mouse.move(back.x, back.y + 400, { steps: 8 });
        await p.mouse.up();
        await follows(p, 1500, "after the thumb was dragged to the end");
        await waitIdle(page, fx.id);
        return `the thumb held ${held.rest}px off the end, the end followed again`;
      } finally {
        await shown.close();
      }
    });
  }

  for (const [form, options] of [["desktop", DESKTOP], ["phone", PHONE]]) {
    await run(`guest ${form}: a full load paints its first frame at the end`, async () => {
      const { ctx, page: p } = await opened(browser, engine, bag, options, { owner: false, hold: true });
      try {
        const list = await landed(p, guestURL(), READY.guest);
        assert(list.some((f) => !f.defined), "no frame was painted before the element upgraded, the hold did not hold");
        off(list, (f) => f.rest <= 2, "stood off the end");
        return `${list.length} frames at the end`;
      } finally {
        await ctx.close();
      }
    });

    let guest = null;
    try {
      await run(`guest ${form}: a late picture is followed on every frame`, async () => {
        guest = await opened(browser, engine, bag, options, { owner: false });
        const g = guest.page;
        await landed(g, guestURL(), READY.guest);
        await sleep(1500);
        await sampling(g, true);
        await postJSON(page, `/assistants/${fx.id}`, { form: "message", message: `ECHO_ANSWER ${TAG}\none\ntwo\n![pic](${PIC})\nthree\nfour\nfive\nsix` });
        await waitIdle(page, fx.id);
        await g.waitForFunction(() => [...document.querySelectorAll("[data-share-log] img")].some((i) => i.complete && i.naturalHeight > 0), null, { timeout: 15000 });
        await sleep(1200);
        const list = await frames(g);
        assert(Math.max(...list.map((f) => f.height)) - Math.min(...list.map((f) => f.height)) > 900, "the picture did not grow the thread, the check says nothing");
        off(list, (f) => f.rest <= 2, "stood off the end");
        return `${list.length} frames at the end`;
      });

      await run(`guest ${form}: a plain scroll up shows the button at the bottom right, a tap goes back to the end`, async () => {
        const { by, b } = await plainly(guest, form, "the guest's button");
        await tapped(guest.page, "the guest's button");
        return `${by}, ${b.width}x${b.height}, ${b.right}px from the right, ${b.bottom}px over the foot`;
      });

      await run(`guest ${form}: input that leaves the guest at the end keeps following through the owner's next message and its answer`, async () => kept(guest, form, page));

      await run(`guest ${form}: a small move up lets go, the button shows, a tap follows again`, async () => {
        const g = guest.page;
        await streaming(page, fx.id);
        await g.waitForFunction(() => (document.querySelector("[data-share-log] > :last-child")?.textContent || "").includes("line 3 of"), null, { timeout: 20000 });
        const by = await move(guest, form, -40);
        await sleep(400);
        const at = await stays(g, 1500, `after ${by}`);
        assert(at.rest > 10, `the guest is back at the end (${at.rest}px), the move did not take`);
        cornered(await shown(g), "the guest's button");
        await g.locator(BUTTON).click();
        await follows(g, 1500, "after the tap");
        const after = await shown(g);
        assert(after && !after.visible, `the guest's button still shows at the end: ${JSON.stringify(after)}`);
        await waitIdle(page, fx.id);
        return `${by} moved ${at.rest}px up and stayed`;
      });
    } finally {
      await guest?.ctx.close();
    }
  }

  if (engine === L.engineList().slice(-1)[0]) {
    await run("the run deletes its link and its assistant", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      if (fx.linkId) await postAs(page, `/settings/sharing/${fx.linkId}`, { form: "revoke" });
      await postJSON(page, `/assistants/${fx.id}`, { form: "delete" });
    });
  }
});
