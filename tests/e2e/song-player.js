#!/usr/bin/env node
const L = require("./lib");
const { assert, sleep, BASE, dismissUpdate } = L;

// The song player of the status line (music-player, desktop only), the
// first party musicplayer plugin under the id the official build wires it with:
// the line carries a single muted music note, the button of a dropup panel
// holding play or stop and next, the volume row (music-volume, the music
// player's own volume), a filter row in the editor sheets' look and the 20
// songs by title, grouped by style. Play shuffles, a title plays that song and
// leaves the shuffle round, next skips, stop ends it, and a song plays on
// across a page change because the playback lives in the plugin's music.js,
// not in the element the swap replaces. The menu carries data-own-selection,
// so app.js does not let the focus follow the pointer and typing in the
// filter keeps working while the mouse rests on the list; the arrows walk the
// hits from the filter on and Enter plays the focused one. The songs file (the
// plugin's songs.js, imported relative to music.js and bound to its hashed
// URL by the import map) loads on the first play; a load that fails says so
// in a toast and the next play tries again.
// The filter text and the list's scroll position are the browser's
// (localStorage dc-song-filter, dc-song-scroll) and come back when the panel
// opens, also after a reload. The list area has a fixed height, so filtering
// never moves the panel.
//
// The runner needs a plain instance, it creates nothing on the server.
//
// Gotchas:
// - Bootstrap's dropdown handles Escape on the document while capturing, so a
//   handler on the filter itself never sees it; the Escape check is what
//   proves the player claims it first while the filter holds text.
// - a module that failed to load stays failed in the browser's module map,
//   which is why the failure check runs in its own fresh context, where the
//   songs file was never requested. WebKit fails a goto in such a second
//   context now and then when it follows right on the login's landing
//   ("WebKit encountered an internal error"), so the check stays on the page
//   the login lands on.
// - the panel's controls stand in the DOM while it is closed, so state is read
//   from classes and attributes, what is visible is read with it open.
// - the cache check reads the Cache-Control header through page.request, the
//   API client: a fetch() of a module inside the page makes the browser load it
//   from the network again on the next full load, in Chromium and WebKit.
// - the shuffle check pins Math.random, so two fresh loads shuffle the same
//   round: the first finds the song next would play, the second picks it by
//   hand. The check ends on a reload, so the pin goes with the page.

const NOTE = "music-player [data-song-open]";
const MENU = "music-player .dropdown-menu";
const TOGGLE = "music-player [data-song-toggle]";
const NEXT = "music-player [data-song-next]";
const FILTER = "music-player [data-song-filter]";
const SONGS = /\/plugins\/music\/assets\/songs\.[0-9a-f]{12}\.js/;

async function settle(page) {
  await dismissUpdate(page);
  assert((await L.waitUpgraded(page, ["music-player", "music-volume"], 8000)).length === 0, "music-player not upgraded");
}

async function ready(page, path = "/projects") {
  await page.goto(`${BASE}${path}`, { waitUntil: "domcontentloaded" });
  await settle(page);
}

async function menuShown(page) {
  return page.$eval(MENU, (el) => el.classList.contains("show") && getComputedStyle(el).display !== "none");
}

async function openPanel(page) {
  if (!(await menuShown(page))) await page.click(NOTE);
  await page.waitForFunction((sel) => document.querySelector(sel).classList.contains("show"), MENU, { timeout: 5000 });
}

async function closePanel(page) {
  if (await menuShown(page)) await page.click(NOTE);
  await page.waitForFunction((sel) => !document.querySelector(sel).classList.contains("show"), MENU, { timeout: 5000 });
}

async function state(page) {
  return page.evaluate(() => {
    const root = document.querySelector("music-player");
    const active = root.querySelector("[data-song].active");
    return {
      icon: root.querySelector("[data-song-toggle] .ti").className,
      title: root.querySelector("[data-song-toggle]").getAttribute("title"),
      nextShown: getComputedStyle(root.querySelector("[data-song-next]")).display !== "none",
      now: root.querySelector("[data-song-now]").textContent,
      active: active ? active.textContent : null,
      key: active ? active.dataset.song : null,
    };
  });
}

async function waitPlaying(page, playing) {
  await page.waitForFunction((on) => {
    const icon = document.querySelector("music-player [data-song-toggle] .ti").className;
    return on ? icon.includes("ti-player-stop") : icon.includes("ti-player-play");
  }, playing, { timeout: 15000 });
}

async function typeFilter(page, text) {
  await page.fill(FILTER, "");
  if (text) await page.type(FILTER, text);
}

L.runFeature("SONG-PLAYER", async ({ engine, browser, page, run, mobilePage }) => {
  try {
    await run("the line shows only the music note and the panel starts closed", async () => {
      await ready(page);
      const line = await page.evaluate(() => {
        const root = document.querySelector("music-player");
        const shown = [...root.querySelectorAll("button")].filter((b) => {
          const box = b.getBoundingClientRect();
          return box.width > 0 && box.height > 0 && getComputedStyle(b).visibility !== "hidden";
        });
        return { shown: shown.map((b) => b.querySelector(".ti").className), label: root.querySelector("[data-song-open]").getAttribute("aria-label") };
      });
      assert(line.shown.length === 1 && /ti-music/.test(line.shown[0]), `the line shows ${JSON.stringify(line.shown)}`);
      assert(line.label === "Songs", `note label "${line.label}"`);
      assert(!(await menuShown(page)), "the panel is open before a click");
    });

    await run("a click on the note opens the panel with the cursor in the filter", async () => {
      await openPanel(page);
      const panel = await page.evaluate(() => ({
        focused: document.activeElement === document.querySelector("music-player [data-song-filter]"),
        box: document.querySelector("music-player .dropdown-menu").getBoundingClientRect().toJSON(),
        note: document.querySelector("music-player [data-song-open]").getBoundingClientRect().top,
        height: window.innerHeight,
        songs: document.querySelectorAll("music-player [data-song]").length,
        groups: [...document.querySelectorAll("music-player [data-song-shelf] .dropdown-header")].map((h) => h.textContent),
      }));
      assert(panel.focused, "the filter does not hold the focus");
      assert(panel.box.top >= 0 && panel.box.bottom <= panel.height, `the panel leaves the viewport: ${JSON.stringify(panel)}`);
      assert(panel.box.bottom <= panel.note + 1, `the panel covers the note: ${JSON.stringify(panel)}`);
      assert(panel.songs === 20, `the list holds ${panel.songs} songs`);
      assert(panel.groups.join(",") === "Chill Electronic,Melodic House", `groups ${panel.groups}`);
    });

    await run("a removed and re-inserted player lists every song once", async () => {
      await ready(page);
      const counts = await page.evaluate(() => {
        const player = document.querySelector("music-player");
        const parent = player.parentNode;
        const next = player.nextSibling;
        player.remove();
        parent.insertBefore(player, next);
        return {
          songs: player.querySelectorAll("[data-song]").length,
          groups: player.querySelectorAll("[data-song-shelf]").length,
          players: document.querySelectorAll("music-player").length,
        };
      });
      assert(counts.songs === 20 && counts.groups === 2 && counts.players === 1, `after re-inserting: ${JSON.stringify(counts)}`);
      await openPanel(page);
      await typeFilter(page, "canyon");
      const shown = await page.$$eval("music-player [data-song]", (items) => items.filter((i) => !i.hidden).map((i) => i.textContent));
      assert(shown.join(",") === "Quiet Canyon", `the filter after re-inserting shows ${JSON.stringify(shown)}`);
      await typeFilter(page, "");
      await closePanel(page);
    });

    await run("the panel's volume row moves the music player alone", async () => {
      await openPanel(page);
      const read = () => page.evaluate(() => ({
        badge: document.querySelector("music-player music-volume [data-volume-output]").textContent,
        music: localStorage.getItem("scriptune-player-music-volume"),
        notification: localStorage.getItem("scriptune-player-notification-volume"),
        master: localStorage.getItem("scriptune-master-volume"),
      }));
      const start = await read();
      assert(start.badge === "100%" && start.music === "1", `the music volume does not start at 100%: ${JSON.stringify(start)}`);
      await page.evaluate(() => {
        const range = document.querySelector("music-player music-volume input");
        range.value = "0.2";
        range.dispatchEvent(new Event("input", { bubbles: true }));
      });
      const moved = await read();
      assert(moved.badge === "20%" && moved.music === "0.2", `the row did not move the music volume: ${JSON.stringify(moved)}`);
      assert(moved.notification === start.notification && moved.master === "1", `the row moved more than the music: ${JSON.stringify({ start, moved })}`);
      await page.evaluate(() => {
        const range = document.querySelector("music-player music-volume input");
        range.value = "1";
        range.dispatchEvent(new Event("input", { bubbles: true }));
      });
    });

    await run("play starts a song and stop ends it", async () => {
      await openPanel(page);
      await typeFilter(page, "");
      await page.click(TOGGLE);
      await waitPlaying(page, true);
      const on = await state(page);
      assert(on.now && on.now === on.active, `now "${on.now}", marked "${on.active}"`);
      assert(on.title === `Stop ${on.now}` && on.nextShown, `playing controls ${JSON.stringify(on)}`);
      const loaded = await page.evaluate((source) => performance.getEntriesByType("resource").some((e) => new RegExp(source).test(e.name)), SONGS.source);
      assert(loaded, "the songs file was not loaded by the first play");
      await page.click(TOGGLE);
      await waitPlaying(page, false);
      const off = await state(page);
      assert(off.now === "" && off.active === null && !off.nextShown && off.title === "Play a song", `stopped controls ${JSON.stringify(off)}`);
      return on.now;
    });

    await run("the music and songs modules load under hashed URLs and come from the cache on the next full load", async () => {
      const modules = () => page.evaluate(() => performance.getEntriesByType("resource")
        .map((e) => ({ path: new URL(e.name).pathname, transfer: e.transferSize }))
        .filter((e) => /^\/plugins\/music\/assets\/(music|songs)[./]/.test(e.path)));
      const first = await modules();
      const paths = first.map((e) => e.path).sort();
      assert(paths.length === 2 && /\/music\.[0-9a-f]{12}\.js$/.test(paths[0]) && /\/songs\.[0-9a-f]{12}\.js$/.test(paths[1]), `the modules loaded from ${JSON.stringify(paths)}`);
      const caching = await Promise.all(paths.map(async (path) => (await page.request.get(`${BASE}${path}`)).headers()["cache-control"]));
      assert(caching.every((value) => /max-age=31536000/.test(value) && /immutable/.test(value)), `the modules answer ${JSON.stringify(caching)}`);
      await ready(page);
      await openPanel(page);
      await page.click(TOGGLE);
      await waitPlaying(page, true);
      await page.click(TOGGLE);
      await waitPlaying(page, false);
      await closePanel(page);
      const again = await modules();
      assert(again.length === 2 && again.every((e) => paths.includes(e.path) && e.transfer === 0), `the next full load fetched ${JSON.stringify(again)}`);
    });

    await run("a title in the list plays that song and leaves the shuffle round", async () => {
      const shuffle = async () => {
        await ready(page);
        await page.evaluate(() => { Math.random = () => 0; });
        await openPanel(page);
        await typeFilter(page, "");
        await page.click(TOGGLE);
        await waitPlaying(page, true);
        return state(page);
      };
      const skip = async (from) => {
        await page.click(NEXT);
        const moved = await page.waitForFunction((was) => {
          const now = document.querySelector("music-player [data-song-now]").textContent;
          return now && now !== was;
        }, from, { timeout: 15000 }).then(() => true, () => false);
        assert(moved, `next after "${from}" played it again`);
        const next = await state(page);
        assert(next.active === next.now, `next ${JSON.stringify(next)}`);
        return next;
      };
      const first = await shuffle();
      const queued = await skip(first.now);
      const again = await shuffle();
      assert(again.now === first.now, `the pinned shuffle started with "${again.now}", before "${first.now}"`);
      await page.click(`music-player [data-song="${queued.key}"]`);
      await page.waitForFunction((title) => document.querySelector("music-player [data-song-now]").textContent === title, queued.now, { timeout: 15000 });
      const picked = await state(page);
      assert(picked.active === queued.now && picked.title === `Stop ${queued.now}`, `picked ${JSON.stringify(picked)}`);
      const round = [first.now, queued.now];
      while (round.length < 20) round.push((await skip(round.at(-1))).now);
      assert(new Set(round).size === 20, `the round played ${JSON.stringify(round)}`);
      await page.click(TOGGLE);
      await waitPlaying(page, false);
      await ready(page);
    });

    await run("the filter narrows the list, counts the hits and keeps the panel's height", async () => {
      await openPanel(page);
      await typeFilter(page, "");
      const height = () => page.$eval(MENU, (el) => el.getBoundingClientRect().height);
      const before = await height();
      await typeFilter(page, "canyon");
      const canyon = await page.evaluate(() => ({
        shown: [...document.querySelectorAll("music-player [data-song]")].filter((i) => !i.hidden).map((i) => i.textContent),
        groups: [...document.querySelectorAll("music-player [data-song-shelf]")].filter((g) => !g.hidden).length,
        count: document.querySelector("music-player [data-song-filter-count]").textContent,
        clear: getComputedStyle(document.querySelector("music-player [data-song-filter-clear]")).visibility,
      }));
      assert(canyon.shown.join(",") === "Quiet Canyon" && canyon.groups === 1, `canyon shows ${JSON.stringify(canyon)}`);
      assert(canyon.count === "1 of 20" && canyon.clear === "visible", `count and clear ${JSON.stringify(canyon)}`);
      assert(Math.abs((await height()) - before) < 1, "the panel changed its height while filtering");
      await typeFilter(page, "zzz");
      const none = await page.evaluate(() => {
        const empty = document.querySelector("music-player [data-song-empty]");
        return { text: empty.textContent, shown: getComputedStyle(empty).display !== "none", count: document.querySelector("music-player [data-song-filter-count]").textContent };
      });
      assert(none.shown && none.text === 'Nothing matches "zzz".' && none.count === "0 of 20", `no hits ${JSON.stringify(none)}`);
      assert(Math.abs((await height()) - before) < 1, "the panel changed its height without hits");
      await typeFilter(page, "tidal");
      await page.press(FILTER, "Enter");
      await page.waitForFunction(() => document.querySelector("music-player [data-song-now]").textContent === "Tidal Echoes", null, { timeout: 15000 });
      await page.click(TOGGLE);
      await waitPlaying(page, false);
    });

    await run("hovering the list while typing keeps the cursor in the filter, the arrows walk the hits and Enter plays one", async () => {
      await openPanel(page);
      await typeFilter(page, "");
      await page.hover('music-player [data-song="goldenhour"]');
      await page.hover('music-player [data-song="lowsun"]');
      await page.keyboard.type("ow");
      const typed = await page.evaluate(() => {
        const filter = document.querySelector("music-player [data-song-filter]");
        return {
          focused: document.activeElement === filter,
          value: filter.value,
          hits: [...document.querySelectorAll("music-player [data-song]")].filter((i) => !i.hidden).map((i) => i.textContent),
        };
      });
      assert(typed.focused && typed.value === "ow", `hovering the list took the typing: ${JSON.stringify(typed)}`);
      assert(typed.hits.length > 2, `"ow" leaves ${JSON.stringify(typed.hits)}`);
      const walked = [];
      while (walked.length < typed.hits.length) {
        await page.keyboard.press("ArrowDown");
        walked.push(await page.evaluate(() => document.activeElement.textContent));
      }
      assert(walked.join(",") === typed.hits.join(","), `the arrows walked ${JSON.stringify(walked)}, the hits are ${JSON.stringify(typed.hits)}`);
      await page.keyboard.press("ArrowUp");
      const back = await page.evaluate(() => document.activeElement.textContent);
      assert(back === typed.hits.at(-2), `ArrowUp from "${walked.at(-1)}" went to "${back}"`);
      await page.keyboard.press("Enter");
      await page.waitForFunction((title) => document.querySelector("music-player [data-song-now]").textContent === title, back, { timeout: 15000 });
      await page.click(TOGGLE);
      await waitPlaying(page, false);
      await typeFilter(page, "");
    });

    await run("Escape clears a filled filter and keeps the panel open, on an empty filter it closes the panel", async () => {
      await openPanel(page);
      await typeFilter(page, "tidal");
      await page.press(FILTER, "Escape");
      await sleep(400);
      const value = await page.inputValue(FILTER);
      assert(value === "", `the filter still reads "${value}"`);
      assert(await menuShown(page), "Escape on a filled filter closed the panel");
      const all = await page.$$eval("music-player [data-song]", (items) => items.filter((i) => !i.hidden).length);
      assert(all === 20, `the cleared filter shows ${all} songs`);
      await page.press(FILTER, "Escape");
      await page.waitForFunction((sel) => !document.querySelector(sel).classList.contains("show"), MENU, { timeout: 5000 });
    });

    await run("the filter text and the list position come back after a reload", async () => {
      await openPanel(page);
      await typeFilter(page, "o");
      assert(await page.evaluate(() => localStorage.getItem("dc-song-filter")) === "o", "the filter was not stored");
      await ready(page);
      await openPanel(page);
      const filter = await page.inputValue(FILTER);
      assert(filter === "o", `after the reload the filter reads "${filter}"`);
      await typeFilter(page, "");
      const scrolled = await page.evaluate(async () => {
        const scroller = document.querySelector("music-player [data-song-scroll]");
        const room = scroller.scrollHeight - scroller.clientHeight;
        scroller.scrollTop = Math.min(80, room);
        await new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)));
        return { top: scroller.scrollTop, room, stored: localStorage.getItem("dc-song-scroll") };
      });
      assert(scrolled.top >= 20 && scrolled.stored === String(scrolled.top), `the list position was not stored: ${JSON.stringify(scrolled)}`);
      await ready(page);
      await openPanel(page);
      const back = await page.evaluate(() => ({
        filter: document.querySelector("music-player [data-song-filter]").value,
        top: document.querySelector("music-player [data-song-scroll]").scrollTop,
      }));
      assert(back.filter === "" && Math.abs(back.top - scrolled.top) <= 1, `after the reload ${JSON.stringify(back)}, stored ${scrolled.top}`);
      await closePanel(page);
    });

    await run("a song plays on across a page change", async () => {
      await openPanel(page);
      await page.click(TOGGLE);
      await waitPlaying(page, true);
      const before = (await state(page)).now;
      await closePanel(page);
      await page.evaluate(() => { window.__songPlayerPage = true; });
      await page.click('.dc-rail a[href="/docs"]');
      await page.waitForURL(/\/docs$/, { timeout: 10000 });
      await page.waitForFunction(() => document.querySelector("music-player [data-song-now]")?.textContent, null, { timeout: 10000 });
      assert(await page.evaluate(() => window.__songPlayerPage === true), "the page change was a full reload");
      const after = await state(page);
      assert(after.now === before && /ti-player-stop/.test(after.icon), `after the page change ${JSON.stringify(after)}, before "${before}"`);
      await openPanel(page);
      await page.click(TOGGLE);
      await waitPlaying(page, false);
      await closePanel(page);
    });

    await run("a failed load says so in a toast and the next play loads the songs", async () => {
      const ctx = await L.newDesktop(browser, engine);
      const fresh = await ctx.newPage();
      try {
        await L.login(fresh);
        await settle(fresh);
        let blocked = 0;
        await fresh.route(SONGS, (route) => { blocked++; return route.abort(); });
        await openPanel(fresh);
        await fresh.click(TOGGLE);
        await fresh.waitForFunction(() => [...document.querySelectorAll(".dc-toast")].some((t) => t.textContent.includes("Could not load the songs.")), null, { timeout: 10000 });
        assert(blocked > 0, "the songs file was never requested");
        assert(/ti-player-play/.test((await state(fresh)).icon), "the player claims to play without songs");
        await fresh.unroute(SONGS);
        await openPanel(fresh);
        await fresh.click(TOGGLE);
        await waitPlaying(fresh, true);
        await fresh.click(TOGGLE);
        await waitPlaying(fresh, false);
      } finally {
        await ctx.close().catch(() => {});
      }
    });

    await run("a phone never sees the player", async () => {
      const phone = await mobilePage();
      await phone.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await dismissUpdate(phone);
      const shown = await phone.$eval("music-player", (el) => el.getBoundingClientRect().height > 0 && getComputedStyle(el.closest(".dc-status")).display !== "none");
      assert(!shown, "the player shows on a phone");
    });
  } finally {
    await page.evaluate(() => {
      const icon = document.querySelector("music-player [data-song-toggle] .ti");
      if (icon && icon.className.includes("ti-player-stop")) document.querySelector("music-player [data-song-toggle]").click();
    }).catch(() => {});
  }
});
