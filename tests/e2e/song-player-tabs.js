const L = require("./lib");
const { assert, sleep } = L;

// Not a runner of its own: song-player.js runs these checks with its own
// helpers. Where the playback lives: in @dc/music, so a song plays on across a
// page change, and once per browser, so every tab of a context shows the one
// playback while a single tab sounds. A click makes its own tab the one that
// sounds, the tab that played goes quiet, a stop in any tab stops it, and a
// tab opened later shows what plays from its first paint on. Closing or
// reloading the sounding tab shows every other tab stopped, and so does one
// that dies without a goodbye, the others hear nothing more from it.
//
// Gotchas:
// - what sounds is read from AudioScheduledSourceNode.start, counted per tab
//   by an init script: scriptune schedules notes five seconds ahead and stops
//   scheduling on its abort, so a tab whose count still grows over three
//   seconds sounds. Party DnB keeps its hats running, no pause outlasts that.
// - the music gain is read from the AudioParam value setter, the only way
//   scriptune sets a player's volume.
// - a tab dying without a goodbye is a tab whose BroadcastChannel.postMessage
//   is a no-op before it closes. Chromium's Page.crash is no way to get there,
//   tabs of one site share a renderer, so the crash takes the observer along.

const GENRE = "Party DnB";
const genrePlay = (genre) => `dc-song-player [data-song-shelf="${genre}"] [data-song-genre-play]`;

function watchSound() {
  window.dcNotes = 0;
  window.dcValues = [];
  const start = AudioScheduledSourceNode.prototype.start;
  AudioScheduledSourceNode.prototype.start = function (...args) {
    window.dcNotes++;
    return start.apply(this, args);
  };
  const value = Object.getOwnPropertyDescriptor(AudioParam.prototype, "value");
  Object.defineProperty(AudioParam.prototype, "value", {
    get() { return value.get.call(this); },
    set(v) { window.dcValues.push(v); value.set.call(this, v); },
  });
}

module.exports.tabChecks = async function tabChecks({ page, run, ready, openPanel, closePanel, state, waitPlaying, toggle, bag }) {
  await run("a song plays on across a page change", async () => {
    await openPanel(page);
    await page.click(toggle);
    await waitPlaying(page, true);
    const before = (await state(page)).now;
    await closePanel(page);
    await page.evaluate(() => { window.__songPlayerPage = true; });
    await page.click('.dc-rail a[href="/docs"]');
    await page.waitForURL(/\/docs$/, { timeout: 10000 });
    await page.waitForFunction(() => document.querySelector("dc-song-player [data-song-now]")?.textContent, null, { timeout: 10000 });
    assert(await page.evaluate(() => window.__songPlayerPage === true), "the page change was a full reload");
    const after = await state(page);
    assert(after.now === before && /ti-player-stop/.test(after.icon), `after the page change ${JSON.stringify(after)}, before "${before}"`);
    await openPanel(page);
    await page.click(toggle);
    await waitPlaying(page, false);
    await closePanel(page);
  });

  const tabs = [];
  const open = async () => {
    const tab = await page.context().newPage();
    L.wirePage(tab, bag);
    await tab.addInitScript(watchSound);
    await ready(tab);
    tabs.push(tab);
    return tab;
  };
  const sounding = async (...list) => {
    const count = () => Promise.all(list.map((tab) => tab.evaluate(() => window.dcNotes)));
    const before = await count();
    await sleep(3000);
    return (await count()).map((n, i) => n > before[i]);
  };
  const shows = (tab, want) => tab.waitForFunction((now) => document.querySelector("dc-song-player [data-song-now]").textContent === now, want, { timeout: 3000 })
    .then(() => true, () => false);
  const startGenre = async (tab) => {
    await openPanel(tab);
    await tab.click(genrePlay(GENRE));
    await tab.waitForFunction((genre) => document.querySelector("dc-song-player [data-song][aria-current]")?.closest("[data-song-shelf]").dataset.songShelf === genre
      && document.querySelector("dc-song-player [data-song-length]").textContent !== "0:00", GENRE, { timeout: 15000 });
    return state(tab);
  };
  const look = (tab) => tab.evaluate(() => {
    const root = document.querySelector("dc-song-player");
    const shown = (el) => !!el && el.getBoundingClientRect().width > 0 && getComputedStyle(el).display !== "none" && !el.closest("[hidden]");
    return {
      dancer: document.querySelector("dc-song-dancer")?.dataset.dancerGenre ?? null,
      note: shown(root.querySelector("[data-song-note]")),
      noteEq: !root.querySelector("[data-song-note-eq]").hidden,
      artEq: shown(root.querySelector("[data-song-art] .dc-eq")),
      label: root.querySelector("[data-song-open]").getAttribute("aria-label"),
      elapsed: root.querySelector("[data-song-elapsed]").textContent,
      length: root.querySelector("[data-song-length]").textContent,
    };
  });
  const seconds = (clock) => clock.split(":").reduce((sum, part) => sum * 60 + Number(part), 0);

  try {
    await run("a song started in one tab shows in a tab opened later right away, with its toggle, equalizers, dancer and position, while that tab alone sounds", async () => {
      const a = await open();
      const playing = await startGenre(a);
      const b = await open();
      const first = await state(b);
      assert(first.now === playing.now && first.key === playing.key && first.genre === GENRE && first.mode === GENRE, `the later tab shows ${JSON.stringify(first)} while ${JSON.stringify(playing)} plays`);
      assert(/ti-player-stop/.test(first.icon) && first.title === `Stop ${playing.now}` && first.nextEnabled && first.running.join(",") === GENRE, `the later tab's controls ${JSON.stringify(first)}`);
      await openPanel(b);
      const marked = (await state(b)).marked;
      assert(marked.join(",") === GENRE, `the later tab marks ${JSON.stringify(marked)}`);
      await sleep(1500);
      const [seenA, seenB] = [await look(a), await look(b)];
      for (const [name, seen] of [["the playing tab", seenA], ["the later tab", seenB]]) {
        assert(seen.dancer === GENRE && !seen.note && seen.noteEq && seen.artEq && seen.label === `Songs, playing ${playing.now} from ${GENRE}`, `${name} ${JSON.stringify(seen)}`);
      }
      assert(seenB.length === seenA.length && seenB.length !== "0:00" && Math.abs(seconds(seenB.elapsed) - seconds(seenA.elapsed)) <= 1 && seconds(seenB.elapsed) >= 1, `the positions ${JSON.stringify({ seenA, seenB })}`);
      const [soundA, soundB] = await sounding(a, b);
      assert(soundA && !soundB, `sounding: playing tab ${soundA}, later tab ${soundB}`);
    });

    await run("the volume row of a tab that does not sound sets the volume of the one that does", async () => {
      const [a, b] = tabs;
      await a.evaluate(() => { window.dcValues = []; });
      const move = (tab, value) => tab.evaluate((v) => {
        const range = document.querySelector("dc-song-player dc-song-volume input");
        range.value = v;
        range.dispatchEvent(new Event("input", { bubbles: true }));
      }, value);
      try {
        await move(b, "0.3");
        const applied = await a.waitForFunction(() => window.dcValues.includes(0.3), null, { timeout: 3000 }).then(() => true, () => false);
        const badge = await a.textContent("dc-song-player dc-song-volume [data-volume-output]");
        assert(applied && badge === "30%", `the playing tab took ${JSON.stringify(await a.evaluate(() => window.dcValues))}, its row reads ${badge}`);
      } finally {
        await move(b, "1");
      }
    });

    await run("a stop in a tab that does not sound stops the tab that does", async () => {
      const [a, b] = tabs;
      await openPanel(b);
      await b.click(toggle);
      await waitPlaying(b, false);
      await waitPlaying(a, false);
      const [soundA, soundB] = await sounding(a, b);
      const seen = await look(a);
      assert(!soundA && !soundB && (await state(a)).now === "Nothing playing" && seen.dancer === "" && seen.note, `after the stop: sounding ${soundA}/${soundB}, ${JSON.stringify(seen)}`);
    });

    await run("a song started in a second tab while one plays, and next from the first, leave exactly one tab sounding", async () => {
      const [a, b] = tabs;
      await openPanel(a);
      await a.click(toggle);
      await waitPlaying(a, true);
      const taken = await startGenre(b);
      assert(await shows(a, taken.now) && await shows(page, taken.now), `the other tabs do not show ${taken.now}: ${(await state(a)).now}, ${(await state(page)).now}`);
      const [soundA, soundB] = await sounding(a, b);
      assert(!soundA && soundB, `after the second start: ${soundA}/${soundB}`);
      await openPanel(a);
      await a.click("dc-song-player [data-song-next]");
      await a.waitForFunction((was) => {
        const now = document.querySelector("dc-song-player [data-song-now]").textContent;
        return now !== was && now !== "Nothing playing";
      }, taken.now, { timeout: 15000 });
      const next = await state(a);
      assert(next.genre === GENRE && next.mode === GENRE && await shows(b, next.now), `next from the first tab ${JSON.stringify(next)}, the second shows ${(await state(b)).now}`);
      const [nextA, nextB] = await sounding(a, b);
      assert(nextA && !nextB, `after next: ${nextA}/${nextB}`);
    });

    await run("closing or reloading the sounding tab, or losing it without a goodbye, shows every other tab stopped", async () => {
      const [a, b] = tabs;
      await a.close();
      await waitPlaying(b, false);
      await waitPlaying(page, false);
      await startGenre(b);
      const c = await open();
      await waitPlaying(c, true);
      await b.reload({ waitUntil: "domcontentloaded" });
      await waitPlaying(c, false);
      assert((await state(c)).now === "Nothing playing", "a reload left the other tab playing");
      await startGenre(c);
      await waitPlaying(b, true);
      await c.evaluate(() => { BroadcastChannel.prototype.postMessage = () => {}; });
      await c.close();
      const at = Date.now();
      await waitPlaying(b, false);
      const after = Date.now() - at;
      assert(after > 1000 && after < 10000, `the silent tab showed stopped after ${after} ms`);
    });
  } finally {
    for (const tab of tabs) await tab.close().catch(() => {});
  }
};
