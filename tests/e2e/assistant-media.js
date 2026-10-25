const L = require("./lib");
const { assert, BASE, sleep } = L;

// Every clip in an assistant's thread holds its box before the browser has
// read its metadata: the server reads the clip's size and renders it as the
// player's width and height, the way it does for a picture, so the thread does
// not jump while the clips load. The runner holds every clip request back,
// measures the boxes, lets the requests through and checks that no box changed
// size when the metadata arrived and that each box has the shape of the clip
// the browser decoded. On the desktop and on a phone, for a loaded thread and
// for a clip just sent, whose box the page builds from the upload's answer.
//
// The clips are made at the start in Chromium (MediaRecorder): a portrait
// webm, a wide mp4 and a square H.264 file named .mov, which is what the
// server reads the size of.
//
// Instance: like assistant.js's (tests/e2e/fakes first on PATH for the fake
// claude, a scratch HOME). The run makes one assistant and deletes it.

const TAG = Date.now().toString(36).slice(-5);
const PHONE = { hasTouch: true, isMobile: true, viewport: { width: 390, height: 844 } };
const DESKTOP = { viewport: { width: 1440, height: 900 } };
const CLIPS = "[data-assistant-scroll] video";

async function makeClips() {
  const browser = await L.launch("chromium");
  try {
    const page = await browser.newPage();
    const clip = (type, width, height) => page.evaluate(async ({ type, width, height }) => {
      const canvas = document.createElement("canvas");
      canvas.width = width;
      canvas.height = height;
      const g = canvas.getContext("2d");
      const recorder = new MediaRecorder(canvas.captureStream(30), { mimeType: type, videoBitsPerSecond: 200000 });
      const chunks = [];
      recorder.ondataavailable = (event) => chunks.push(event.data);
      const stopped = new Promise((resolve) => { recorder.onstop = resolve; });
      recorder.start();
      const t0 = performance.now();
      await new Promise((resolve) => {
        const tick = () => {
          const t = performance.now() - t0;
          g.fillStyle = `hsl(${(t / 3) % 360} 60% 45%)`;
          g.fillRect(0, 0, width, height);
          if (t < 600) requestAnimationFrame(tick);
          else resolve();
        };
        tick();
      });
      recorder.stop();
      await stopped;
      const bytes = new Uint8Array(await new Blob(chunks).arrayBuffer());
      let s = "";
      for (const b of bytes) s += String.fromCharCode(b);
      return btoa(s);
    }, { type, width, height });
    const file = (name, mimeType, base64) => ({ name, mimeType, buffer: Buffer.from(base64, "base64") });
    return {
      tall: file(`tall-${TAG}.webm`, "video/webm", await clip("video/webm;codecs=vp8", 360, 640)),
      wide: file(`wide-${TAG}.mp4`, "video/mp4", await clip("video/mp4;codecs=avc1", 640, 360)),
      square: file(`square-${TAG}.mov`, "video/quicktime", await clip("video/mp4;codecs=avc1", 480, 480)),
    };
  } finally {
    await browser.close();
  }
}

// heldPage is a signed in page whose clip requests wait at a gate. Routing
// also turns the HTTP cache off, so no clip arrives from an earlier load.
async function heldPage(browser, bag, options) {
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true, ...options });
  const page = await ctx.newPage();
  L.wirePage(page, bag);
  await L.login(page);
  await L.dismissUpdate(page);
  let open = () => {};
  let gate = null;
  const gates = {
    hold: () => { gate = new Promise((resolve) => { open = resolve; }); },
    release: () => open(),
  };
  gates.hold();
  await page.route(/\/media\/.*\.(webm|mp4|mov)(\?|$)/, async (route) => {
    await gate;
    await route.continue().catch(() => {});
  });
  return { ctx, page, gates };
}

async function openThread(page) {
  await page.goto(`${BASE}/assistants/${fx.id}`, { waitUntil: "domcontentloaded" });
  await L.dismissUpdate(page);
  await page.waitForSelector("dc-assistant[ready]", { timeout: 15000 });
}

// boxes measures every clip of the thread and watches each one from then on,
// recording every later size of a clip still in the page that differs from
// the one measured here.
const boxes = (page) => page.evaluate((selector) => {
  window.__resized ||= [];
  return [...document.querySelectorAll(selector)].map((v) => {
    const box = v.getBoundingClientRect();
    const name = v.getAttribute("src").split("/").pop();
    if (!v.__watched) {
      v.__watched = true;
      new ResizeObserver(() => {
        if (!v.isConnected) return;
        const now = v.getBoundingClientRect();
        if (Math.abs(now.width - box.width) > 0.5 || Math.abs(now.height - box.height) > 0.5) {
          window.__resized.push(`${name} ${Math.round(box.width)}x${Math.round(box.height)} to ${Math.round(now.width)}x${Math.round(now.height)} at readyState ${v.readyState}`);
        }
      }).observe(v);
    }
    return {
      name,
      ready: v.readyState,
      width: Number(v.getAttribute("width")),
      height: Number(v.getAttribute("height")),
      box: { width: box.width, height: box.height },
    };
  });
}, CLIPS);

// boxedBeforeMetadata asserts that every clip stands in the box its size
// gives it while none of the held ones has read its metadata yet.
function boxedBeforeMetadata(clips, where) {
  const held = clips.filter((c) => c.ready === 0);
  assert(held.length, `${where}: no clip was held before its metadata (${clips.map((c) => `${c.name} readyState ${c.ready}`).join(", ")})`);
  const unsized = clips.filter((c) => !c.width || !c.height);
  assert(!unsized.length, `${where}: clips without a size: ${unsized.map((c) => c.name).join(", ")}`);
  const bent = clips.filter((c) => !c.box.height || Math.abs((c.box.width / c.box.height) / (c.width / c.height) - 1) > 0.02);
  assert(!bent.length, `${where}: clips whose box is not their size: ${JSON.stringify(bent)}`);
  return held.length;
}

// kept lets the held clips through and checks, once every clip read its
// metadata or gave up, that no box changed size and that each one has the
// shape of the clip the browser decoded.
async function kept(page, gates, before, where) {
  gates.release();
  await page.waitForFunction((selector) => {
    const clips = [...document.querySelectorAll(selector)];
    return clips.length > 0 && clips.every((v) => v.readyState >= 1 || v.error);
  }, CLIPS, { timeout: 30000 });
  await sleep(500);
  const resized = await page.evaluate(() => window.__resized);
  assert(!resized.length, `${where}: boxes changed size when the metadata arrived: ${resized.slice(0, 6).join(", ")}`);
  const after = await page.$$eval(CLIPS, (nodes) => nodes.map((v) => {
    const box = v.getBoundingClientRect();
    return { name: v.getAttribute("src").split("/").pop(), box: { width: box.width, height: box.height }, clip: v.videoWidth ? v.videoWidth / v.videoHeight : 0 };
  }));
  assert(after.length === before.length, `${where}: ${before.length} clips before the metadata, ${after.length} after`);
  const moved = after.filter((c, i) => c.name !== before[i].name || Math.abs(c.box.width - before[i].box.width) > 0.5 || Math.abs(c.box.height - before[i].box.height) > 0.5);
  assert(!moved.length, `${where}: boxes differ after the metadata: ${JSON.stringify(moved)}`);
  const decoded = after.filter((c) => c.clip);
  assert(decoded.length, `${where}: no clip was decoded`);
  const bent = decoded.filter((c) => Math.abs((c.box.width / c.box.height) / c.clip - 1) > 0.02);
  assert(!bent.length, `${where}: clips out of shape: ${JSON.stringify(bent)}`);
  return `${before.length} clips kept their boxes, ${decoded.length} decoded`;
}

async function postJSON(page, path, body) {
  const res = await page.evaluate(async ({ path, body }) => {
    const token = document.querySelector('meta[name="csrf-token"]')?.content || "";
    const r = await fetch(path, {
      method: "POST",
      headers: { "X-CSRF-Token": token, Accept: "application/json" },
      body: new URLSearchParams(body),
    });
    return { ok: r.ok, status: r.status, text: await r.text() };
  }, { path, body });
  assert(res.ok, `POST ${path} answered ${res.status}: ${res.text.slice(0, 200)}`);
  return res.text.trim().startsWith("{") ? JSON.parse(res.text) : res.text;
}

// turn sends one message carrying files and waits for the fake claude's
// answer, which echoes the lines after the first and so embeds a clip of the
// workspace too. sent runs once the user's message stands in the thread.
async function turn(page, text, files, sent = async () => {}) {
  await page.setInputFiles("input[data-assistant-file]", files);
  await page.waitForFunction((count) => document.querySelectorAll("[data-assistant-attachment-remove]").length === count, files.length, { timeout: 20000 });
  const answered = await page.locator('[data-role="assistant"][data-state="complete"]').count();
  const asked = await page.locator('[data-role="user"]').count();
  await page.fill("[data-assistant-input]", text);
  await page.click("[data-assistant-send]");
  await page.waitForFunction((n) => document.querySelectorAll('[data-role="user"]').length > n, asked, { timeout: 10000 });
  await sent();
  await page.waitForFunction(
    (n) => document.querySelectorAll('[data-role="assistant"][data-state="complete"]').length > n,
    answered,
    { timeout: 30000 },
  );
}

const fx = {};

L.runFeature("assistant-media", async ({ engine, browser, page, run, bag }) => {
  if (!fx.id) {
    await run("the owner builds a thread of clips", async () => {
      fx.clips = await makeClips();
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      fx.id = (await postJSON(page, "/assistants/new", { form: "new", coder: "claude" })).id;
      assert(fx.id, "no assistant was made");
      await openThread(page);
      const { tall, wide, square } = fx.clips;
      for (const [i, [file, embed]] of [[tall, wide], [wide, square], [square, tall]].entries()) {
        await turn(page, `ECHO_ANSWER\nanswer ${i} ![clip](user-upload/${embed.name})`, [file]);
      }
    });
  }
  if (!fx.id) return;

  for (const [form, options] of [["desktop", DESKTOP], ["phone", PHONE]]) {
    await run(`${form}: every clip of a loaded thread holds its box before its metadata and keeps it`, async () => {
      const { ctx, page: p, gates } = await heldPage(browser, bag, options);
      try {
        await openThread(p);
        const before = await boxes(p);
        const held = boxedBeforeMetadata(before, `${form} load`);
        assert(held === before.length, `${form} load: ${before.length - held} clips read their metadata through the hold`);
        return await kept(p, gates, before, `${form} load`);
      } finally {
        await ctx.close();
      }
    });

    await run(`${form}: a clip just sent holds its box before its metadata and keeps it`, async () => {
      const { ctx, page: p, gates } = await heldPage(browser, bag, options);
      try {
        await openThread(p);
        gates.release();
        await p.waitForFunction((selector) => [...document.querySelectorAll(selector)].every((v) => v.readyState >= 1 || v.error), CLIPS, { timeout: 30000 });
        gates.hold();
        // An upload keeps its name, and the thread holds the wide clip already,
        // loaded before the hold. A name of its own picks the player just sent.
        const clip = { ...fx.clips.wide, name: `sent-${engine}-${form}-${TAG}.mp4` };
        let sent = null;
        const player = `[data-role="user"] video[src$="/${clip.name}"]`;
        await turn(p, `ECHO_ANSWER\nanswer ![clip](user-upload/${clip.name})`, [clip], async () => {
          await p.waitForSelector(player, { state: "attached", timeout: 10000 });
          sent = await p.$eval(player, (v) => ({ size: `${v.getAttribute("width")}x${v.getAttribute("height")}`, ready: v.readyState }));
          await boxes(p);
        });
        assert(sent.size === "640x360", `the clip just sent stands in the box ${sent.size}, want 640x360`);
        assert(sent.ready === 0, `the clip just sent read its metadata through the hold (readyState ${sent.ready})`);
        const before = await boxes(p);
        const held = boxedBeforeMetadata(before, `${form} sent`);
        assert(held >= 2, `${form} sent: ${held} clips held, want the one sent and the one in the answer`);
        return await kept(p, gates, before, `${form} sent`);
      } finally {
        await ctx.close();
      }
    });
  }

  if (engine === L.engineList().slice(-1)[0]) {
    await run("the run deletes its assistant", async () => {
      await page.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await L.dismissUpdate(page);
      await postJSON(page, `/assistants/${fx.id}`, { form: "delete" });
    });
  }
});
