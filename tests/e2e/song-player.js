#!/usr/bin/env node
const L = require("./lib");
const { assert, sleep, BASE, dismissUpdate } = L;

// The song player of the status line (dc-song-player, desktop only). The
// line carries a single muted music note, an animated equalizer while music
// plays (static bars with prefers-reduced-motion: reduce), its tooltip naming
// the title and the genre; a click on it opens the dropup panel and never
// stops the music. The panel opens on a now playing area (the genre's art,
// title, genre, the position against the length scriptune plays the sheet
// for, play or stop and next, the mode the shuffle runs in), the volume row
// (dc-song-volume, the music player's own volume), a filter row in the editor
// sheets' look and the 60 songs in six genres, top to bottom Dark Techno,
// Deep Dubstep, Melodic House, Chill Electronic, Space Ambient and Classic
// Rock. The genres stand folded until a click unfolds one, each row carrying
// its own play control. The list keeps one height, room for every genre with
// the largest one open, so opening or closing genres never changes the
// panel's box, at 1360 px and at 992 px, the narrowest width that has a
// status line. A lower window takes height from the list first, down to its
// six genre rows, and only then lets the whole panel scroll, so no part ever
// leaves the window.
//
// Two modes: the big play shuffles all genres, a genre's play control
// shuffles that genre without opening it, a title plays that song and then
// shuffles its genre, its round leaving the title out. Next stays in the mode
// that runs, the panel names it, and the running genre's control stops it.
// The current song carries the equalizer in place of its number, its genre
// row carries one too, folded or not. Stop ends it, and a song plays on
// across a page change because the playback lives in @dc/music, not in the
// element the swap replaces. The filter finds titles in every genre, folded
// or not, shows each genre with hits under a header that does not fold while
// a query stands, and typing never moves the panel. The menu carries
// data-own-selection, so app.js does not let the focus follow the pointer and
// typing in the filter keeps working while the mouse rests on the list; the
// arrows walk the hits from the filter on and Enter plays the focused one.
// The songs file (@dc/songs) loads on the first play; a load that fails says
// so in a toast and the next play tries again. The folds, the filter text and
// the list's scroll position are the browser's (localStorage dc-song-open,
// dc-song-filter, dc-song-scroll) and come back when the panel opens, also
// after a reload. The panel follows the light and the dark theme.
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
// - the panel's controls stand in the DOM while it is closed, and a folded
//   song stands in it too, so state is read from classes and attributes, what
//   is visible is read from the boxes with the panel open.
// - a song row's number is a ::before from data-song-num, so the row's text
//   is the bare title the filter and these checks read.
// - the box check compares the menu's rect, its list's rect and the filter's
//   place in the panel's content: at 340 px the panel scrolls, and a click
//   scrolls its target into view first, which moves the content, not the box.
//   Two genres open overflow the list, which also proves a scrollbar does not
//   widen the panel.
// - the shuffle check pins Math.random, so two fresh loads shuffle the same
//   round: the first finds the song next would play, the second picks it by
//   hand. The check ends on a reload, so the pin goes with the page.
// - Fathom's sheet plays for 174.5 s (the length scriptune 0.0.2 schedules),
//   which the now playing check reads as 2:54.

const NOTE = "dc-song-player [data-song-open]";
const MENU = "dc-song-player .dropdown-menu";
const TOGGLE = "dc-song-player [data-song-toggle]";
const NEXT = "dc-song-player [data-song-next]";
const FILTER = "dc-song-player [data-song-filter]";
const GENRES = ["Dark Techno", "Deep Dubstep", "Melodic House", "Chill Electronic", "Space Ambient", "Classic Rock"];
const SPACE = ["Apogee", "Perigee", "Aphelion", "Zenith", "Parallax", "Albedo", "Umbra", "Lagrange", "Redshift", "Nadir"];
const ROCK = ["Chinook", "Bora", "Meltemi", "Khamsin", "Tramontane", "Zonda", "Halny", "Sundowner", "Shamal", "Williwaw"];
const fold = (genre) => `dc-song-player [data-song-shelf="${genre}"] [data-song-genre]`;
const genrePlay = (genre) => `dc-song-player [data-song-shelf="${genre}"] [data-song-genre-play]`;

async function settle(page) {
  await dismissUpdate(page);
  assert((await L.waitUpgraded(page, ["dc-song-player", "dc-song-volume"], 8000)).length === 0, "dc-song-player not upgraded");
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
    const root = document.querySelector("dc-song-player");
    const current = root.querySelector("[data-song][aria-current]");
    const next = root.querySelector("[data-song-next]");
    const mode = root.querySelector("[data-song-mode]");
    const shown = (el) => el.getBoundingClientRect().height > 0 && getComputedStyle(el).visibility !== "hidden";
    return {
      icon: root.querySelector("[data-song-toggle] .ti").className,
      title: root.querySelector("[data-song-toggle]").getAttribute("title"),
      nextEnabled: !next.disabled,
      nextTitle: next.getAttribute("title"),
      now: root.querySelector("[data-song-now]").textContent,
      from: root.querySelector("[data-song-from]").textContent,
      mode: getComputedStyle(mode).visibility === "hidden" ? null : root.querySelector("[data-song-mode-label]").textContent,
      active: current ? current.textContent : null,
      key: current ? current.dataset.song : null,
      genre: current ? current.closest("[data-song-shelf]").dataset.songShelf : null,
      running: [...root.querySelectorAll(".dc-song-genre-row[data-song-running]")].map((row) => row.closest("[data-song-shelf]").dataset.songShelf),
      marked: [...root.querySelectorAll("[data-song-genre-eq]")].filter(shown).map((eq) => eq.closest("[data-song-shelf]").dataset.songShelf),
    };
  });
}

// What the open panel shows: the songs with a box, the genre rows with their
// fold state, and where the list and the filter stand.
async function view(page) {
  return page.evaluate(() => {
    const root = document.querySelector("dc-song-player");
    const shown = (el) => el.getBoundingClientRect().height > 0 && getComputedStyle(el).visibility !== "hidden";
    return {
      songs: [...root.querySelectorAll("[data-song]")].filter(shown).map((i) => i.textContent),
      genres: [...root.querySelectorAll("[data-song-genre]")].filter(shown).map((b) => ({
        name: b.querySelector(".dc-trunc").textContent,
        expanded: b.getAttribute("aria-expanded"),
        chevron: b.querySelector(".ti").classList.contains("ti-chevron-down") ? "down" : "right",
        count: b.querySelector("[data-song-count]").textContent,
        disabled: b.disabled,
      })),
      list: root.querySelector("[data-song-scroll]").getBoundingClientRect().height,
      overflow: (() => { const s = root.querySelector("[data-song-scroll]"); return s.scrollHeight - s.clientHeight; })(),
      viewport: { width: innerWidth, height: innerHeight },
      note: root.querySelector("[data-song-open]").getBoundingClientRect().top,
      filter: (() => { const m = root.querySelector(".dropdown-menu"); return root.querySelector("[data-song-filter]").getBoundingClientRect().top - m.getBoundingClientRect().top + m.scrollTop; })(),
      menu: root.querySelector(".dropdown-menu").getBoundingClientRect().toJSON(),
      stored: localStorage.getItem("dc-song-open"),
    };
  });
}

function box(v) {
  return { x: v.menu.x, y: v.menu.y, width: v.menu.width, height: v.menu.height, list: v.list, filter: v.filter };
}

function sameBox(a, b) {
  const [x, y] = [box(a), box(b)];
  return Object.keys(x).every((key) => Math.abs(x[key] - y[key]) < 0.5);
}

async function songsOf(page, genre) {
  return page.$$eval(`dc-song-player [data-song-shelf="${genre}"] [data-song]`, (items) => items.map((i) => i.textContent));
}

// setFolds unfolds exactly the genres in open, with an empty filter, since a
// genre row does not fold while a query stands.
async function setFolds(page, open) {
  await typeFilter(page, "");
  for (const genre of GENRES) {
    const expanded = (await page.getAttribute(fold(genre), "aria-expanded")) === "true";
    if (expanded !== open.includes(genre)) await page.click(fold(genre));
  }
}

async function waitPlaying(page, playing) {
  await page.waitForFunction((on) => {
    const icon = document.querySelector("dc-song-player [data-song-toggle] .ti").className;
    return on ? icon.includes("ti-player-stop") : icon.includes("ti-player-play");
  }, playing, { timeout: 15000 });
}

async function skip(page, from) {
  await page.click(NEXT);
  const moved = await page.waitForFunction((was) => {
    const now = document.querySelector("dc-song-player [data-song-now]").textContent;
    return now && now !== was;
  }, from, { timeout: 15000 }).then(() => true, () => false);
  assert(moved, `next after "${from}" played it again`);
  const next = await state(page);
  assert(next.active === next.now, `next ${JSON.stringify(next)}`);
  return next;
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
        const root = document.querySelector("dc-song-player");
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

    await run("a click on the note opens the panel with the cursor in the filter, the six genres in order and folded", async () => {
      await openPanel(page);
      const panel = await page.evaluate(() => ({
        focused: document.activeElement === document.querySelector("dc-song-player [data-song-filter]"),
        box: document.querySelector("dc-song-player .dropdown-menu").getBoundingClientRect().toJSON(),
        note: document.querySelector("dc-song-player [data-song-open]").getBoundingClientRect().top,
        height: window.innerHeight,
        songs: document.querySelectorAll("dc-song-player [data-song]").length,
      }));
      assert(panel.focused, "the filter does not hold the focus");
      assert(panel.box.top >= 0 && panel.box.bottom <= panel.height, `the panel leaves the viewport: ${JSON.stringify(panel)}`);
      assert(panel.box.bottom <= panel.note + 1, `the panel covers the note: ${JSON.stringify(panel)}`);
      assert(panel.songs === 60, `the list holds ${panel.songs} songs`);
      const folded = await view(page);
      assert(folded.genres.map((g) => g.name).join(",") === GENRES.join(","), `genres ${JSON.stringify(folded.genres.map((g) => g.name))}`);
      assert(folded.genres.every((g) => g.expanded === "false" && g.chevron === "right" && g.count === "10" && !g.disabled), `the genres do not stand folded: ${JSON.stringify(folded.genres)}`);
      assert(folded.songs.length === 0, `a folded panel shows ${JSON.stringify(folded.songs)}`);
      const space = await songsOf(page, "Space Ambient");
      assert(space.join(",") === SPACE.join(","), `Space Ambient holds ${JSON.stringify(space)}`);
      const rock = await songsOf(page, "Classic Rock");
      assert(rock.join(",") === ROCK.join(","), `Classic Rock holds ${JSON.stringify(rock)}`);
      assert(folded.stored === null, `a fresh browser stored folds ${folded.stored}`);
      const idle = await state(page);
      assert(idle.now === "Nothing playing" && idle.from === "60 songs in 6 genres" && idle.mode === null, `the idle stage reads ${JSON.stringify(idle)}`);
      assert(idle.title === "Shuffle all genres" && /ti-player-play/.test(idle.icon) && !idle.nextEnabled, `the idle controls ${JSON.stringify(idle)}`);
      const clock = await page.evaluate(() => [...document.querySelectorAll("dc-song-player [data-song-elapsed], dc-song-player [data-song-length]")].map((e) => e.textContent));
      assert(clock.join(" ") === "0:00 0:00", `the idle clock reads ${JSON.stringify(clock)}`);
    });

    await run("a click on a genre shows its songs, a second one folds them, the folds come back after a reload", async () => {
      await openPanel(page);
      const folded = await view(page);
      await page.click(fold("Deep Dubstep"));
      const open = await view(page);
      const dubstep = await songsOf(page, "Deep Dubstep");
      assert(dubstep.length === 10 && open.songs.join(",") === dubstep.join(","), `the open genre shows ${JSON.stringify(open.songs)}`);
      const row = open.genres.find((g) => g.name === "Deep Dubstep");
      assert(row.expanded === "true" && row.chevron === "down", `the open genre row ${JSON.stringify(row)}`);
      assert(open.genres.filter((g) => g.name !== "Deep Dubstep").every((g) => g.expanded === "false"), `other genres opened: ${JSON.stringify(open.genres)}`);
      assert(open.stored === '["Deep Dubstep"]', `stored folds ${open.stored}`);
      assert(sameBox(open, folded), `opening a genre changed the box: ${JSON.stringify({ folded: box(folded), open: box(open) })}`);
      await ready(page);
      await openPanel(page);
      const back = await view(page);
      assert(back.songs.join(",") === dubstep.join(","), `after a reload the panel shows ${JSON.stringify(back.songs)}`);
      await page.click(fold("Deep Dubstep"));
      const again = await view(page);
      assert(again.songs.length === 0 && again.stored === "[]", `the second click left ${JSON.stringify(again)}`);
      assert(sameBox(again, folded), `closing a genre changed the box: ${JSON.stringify({ folded: box(folded), again: box(again) })}`);
    });

    await run("opening and closing genres keeps the panel's box, and the panel shrinks with the window down to its six genre rows", async () => {
      const heights = {};
      try {
        for (const [width, height] of [[1360, 900], [992, 900], [1360, 620], [992, 450], [1360, 340]]) {
          const size = `${width}x${height}`;
          await page.setViewportSize({ width, height });
          await ready(page);
          await openPanel(page);
          await setFolds(page, []);
          const folded = await view(page);
          const room = await page.evaluate(() => {
            const menu = document.querySelector("dc-song-player .dropdown-menu");
            const list = document.querySelector("dc-song-player [data-song-scroll]").getBoundingClientRect();
            const rows = [...document.querySelectorAll("dc-song-player [data-song-genre]")].map((row) => row.getBoundingClientRect());
            return { panelScroll: menu.scrollHeight - menu.clientHeight, rowsInside: rows.every((row) => row.top >= list.top - 0.5 && row.bottom <= list.bottom + 0.5) };
          });
          heights[size] = folded.menu.height;
          assert(folded.viewport.width === width && folded.viewport.height === height, `the viewport is ${JSON.stringify(folded.viewport)}`);
          assert(folded.menu.x >= 0 && folded.menu.top >= 0 && folded.menu.right <= width && folded.menu.bottom <= folded.note + 1, `at ${size} the panel leaves the window or covers the note: ${JSON.stringify(folded.menu)}`);
          assert(room.rowsInside, `at ${size} the list cut a genre row: ${JSON.stringify(room)}`);
          assert(height >= 450 ? room.panelScroll <= 1 : room.panelScroll > 1, `at ${size} the panel scrolls ${room.panelScroll} px`);
          const steps = [
            ["Deep Dubstep", ["Deep Dubstep"]],
            ["Melodic House", ["Deep Dubstep", "Melodic House"]],
            ["Melodic House", ["Deep Dubstep"]],
            ["Deep Dubstep", []],
          ];
          for (const [genre, want] of steps) {
            await page.click(fold(genre));
            const now = await view(page);
            const open = now.genres.filter((g) => g.expanded === "true").map((g) => g.name);
            assert(open.join(",") === want.join(","), `at ${size} the open genres are ${JSON.stringify(open)}, want ${JSON.stringify(want)}`);
            assert(sameBox(now, folded), `at ${size} a click on ${genre} (open ${JSON.stringify(want)}) changed the box: ${JSON.stringify({ folded: box(folded), now: box(now) })}`);
            if (want.length === 1 && height === 900) assert(now.songs.length === 10 && now.overflow <= 1, `at ${size} one open genre does not fit: ${now.songs.length} songs, ${now.overflow} px to scroll`);
            if (want.length === 2) assert(now.overflow > 1, `at ${size} two open genres do not overflow the list, the scrollbar case went unchecked`);
          }
          await closePanel(page);
        }
      } finally {
        await page.setViewportSize({ width: 1360, height: 900 });
      }
      assert(heights["1360x620"] < heights["1360x900"] && heights["992x450"] < heights["1360x620"] && heights["1360x340"] < heights["992x450"], `the panel does not shrink with the window: ${JSON.stringify(heights)}`);
      return JSON.stringify(heights);
    });

    await run("a removed and re-inserted player lists every song once and keeps one equalizer in the art, still after Stop", async () => {
      await ready(page);
      await openPanel(page);
      await page.click(TOGGLE);
      await waitPlaying(page, true);
      await closePanel(page);
      const counts = await page.evaluate(() => {
        const player = document.querySelector("dc-song-player");
        const parent = player.parentNode;
        const next = player.nextSibling;
        player.remove();
        parent.insertBefore(player, next);
        return {
          songs: player.querySelectorAll("[data-song]").length,
          groups: player.querySelectorAll("[data-song-shelf]").length,
          players: document.querySelectorAll("dc-song-player").length,
        };
      });
      assert(counts.songs === 60 && counts.groups === 6 && counts.players === 1, `after re-inserting: ${JSON.stringify(counts)}`);
      await openPanel(page);
      await typeFilter(page, "canyon");
      const shown = (await view(page)).songs;
      assert(shown.join(",") === "Quiet Canyon", `the filter after re-inserting shows ${JSON.stringify(shown)}`);
      await typeFilter(page, "");
      const art = () => page.evaluate(() => {
        const eqs = [...document.querySelectorAll("dc-song-player [data-song-art] .dc-eq")];
        const shown = (el) => el.getBoundingClientRect().height > 0 && getComputedStyle(el).visibility !== "hidden";
        return { eqs: eqs.length, shown: eqs.filter(shown).length };
      });
      const playing = await art();
      assert(playing.eqs === 1 && playing.shown === 1, `the art while playing after re-inserting: ${JSON.stringify(playing)}`);
      await page.click(TOGGLE);
      await waitPlaying(page, false);
      const stopped = await art();
      assert(stopped.eqs === 1 && stopped.shown === 0, `the art after Stop: ${JSON.stringify(stopped)}`);
      await closePanel(page);
    });

    await run("the panel's volume row moves the music player alone", async () => {
      await openPanel(page);
      const read = () => page.evaluate(() => ({
        badge: document.querySelector("dc-song-player dc-song-volume [data-volume-output]").textContent,
        music: localStorage.getItem("scriptune-player-music-volume"),
        notification: localStorage.getItem("scriptune-player-notification-volume"),
        master: localStorage.getItem("scriptune-master-volume"),
      }));
      const start = await read();
      assert(start.badge === "100%" && start.music === "1", `the music volume does not start at 100%: ${JSON.stringify(start)}`);
      await page.evaluate(() => {
        const range = document.querySelector("dc-song-player dc-song-volume input");
        range.value = "0.2";
        range.dispatchEvent(new Event("input", { bubbles: true }));
      });
      const moved = await read();
      assert(moved.badge === "20%" && moved.music === "0.2", `the row did not move the music volume: ${JSON.stringify(moved)}`);
      assert(moved.notification === start.notification && moved.master === "1", `the row moved more than the music: ${JSON.stringify({ start, moved })}`);
      await page.evaluate(() => {
        const range = document.querySelector("dc-song-player dc-song-volume input");
        range.value = "1";
        range.dispatchEvent(new Event("input", { bubbles: true }));
      });
    });

    await run("play shuffles all genres, next stays in all genres and the panel names the mode", async () => {
      await ready(page);
      await openPanel(page);
      await setFolds(page, []);
      const idle = await view(page);
      await page.click(TOGGLE);
      await waitPlaying(page, true);
      const on = await state(page);
      assert(on.now && on.now === on.active && on.from === on.genre, `now "${on.now}" from "${on.from}", marked "${on.active}" in ${on.genre}`);
      assert(on.mode === "All genres" && on.title === `Stop ${on.now}` && on.nextEnabled && on.nextTitle === "Next in all genres", `playing controls ${JSON.stringify(on)}`);
      assert(on.running.length === 0, `a genre runs while all genres shuffle: ${JSON.stringify(on.running)}`);
      const loaded = await page.evaluate(() => performance.getEntriesByType("resource").some((e) => /\/js\/dc\/songs\.[^/]*js/.test(e.name)));
      assert(loaded, "the songs file was not loaded by the first play");
      assert(sameBox(await view(page), idle), "playing changed the panel's box");
      const genres = new Set([on.genre]);
      let last = on;
      for (let i = 0; i < 10 && genres.size < 2; i++) {
        last = await skip(page, last.now);
        assert(last.mode === "All genres" && last.from === last.genre, `next left all genres: ${JSON.stringify(last)}`);
        genres.add(last.genre);
      }
      assert(genres.size > 1, `ten nexts stayed in ${JSON.stringify([...genres])}`);
      await page.click(TOGGLE);
      await waitPlaying(page, false);
      const off = await state(page);
      assert(off.now === "Nothing playing" && off.active === null && !off.nextEnabled && off.title === "Shuffle all genres" && off.mode === null, `stopped controls ${JSON.stringify(off)}`);
    });

    await run("a genre's play control shuffles that genre without opening it, next stays in it and the control stops it", async () => {
      await ready(page);
      await openPanel(page);
      await setFolds(page, []);
      const idle = await view(page);
      const house = await songsOf(page, "Melodic House");
      await page.click(genrePlay("Melodic House"));
      await waitPlaying(page, true);
      const first = await state(page);
      assert(first.genre === "Melodic House" && first.mode === "Melodic House" && first.nextTitle === "Next in Melodic House", `the genre play ${JSON.stringify(first)}`);
      assert(first.running.join(",") === "Melodic House" && first.marked.join(",") === "Melodic House", `the running genre ${JSON.stringify(first)}`);
      const control = await page.$eval(genrePlay("Melodic House"), (b) => ({ label: b.getAttribute("aria-label"), icon: b.querySelector(".ti").className }));
      assert(control.label === "Stop Melodic House" && /ti-player-stop/.test(control.icon), `the running genre's control ${JSON.stringify(control)}`);
      const folded = await view(page);
      assert(folded.songs.length === 0 && folded.genres.every((g) => g.expanded === "false"), `the genre play opened a genre: ${JSON.stringify(folded.genres)}`);
      assert(sameBox(folded, idle), "the genre play changed the panel's box");
      const round = [first.now];
      while (round.length < house.length) {
        const next = await skip(page, round.at(-1));
        assert(next.genre === "Melodic House" && next.mode === "Melodic House", `next left the genre: ${JSON.stringify(next)}`);
        round.push(next.now);
      }
      assert(new Set(round).size === house.length, `the round played ${JSON.stringify(round)}`);
      await page.click(genrePlay("Melodic House"));
      await waitPlaying(page, false);
      const off = await state(page);
      assert(off.now === "Nothing playing" && off.running.length === 0 && off.marked.length === 0, `the control did not stop its genre: ${JSON.stringify(off)}`);
    });

    await run("a title plays that song and then shuffles its genre, its round leaving the title out", async () => {
      const shuffleTechno = async () => {
        await ready(page);
        await page.evaluate(() => { Math.random = () => 0; });
        await openPanel(page);
        await setFolds(page, []);
        await page.click(genrePlay("Dark Techno"));
        await waitPlaying(page, true);
        return state(page);
      };
      const techno = await songsOf(page, "Dark Techno");
      const first = await shuffleTechno();
      const queued = await skip(page, first.now);
      const again = await shuffleTechno();
      assert(again.now === first.now, `the pinned shuffle started with "${again.now}", before "${first.now}"`);
      await setFolds(page, ["Dark Techno"]);
      await page.click(`dc-song-player [data-song="${queued.key}"]`);
      await page.waitForFunction((title) => document.querySelector("dc-song-player [data-song-now]").textContent === title, queued.now, { timeout: 15000 });
      const picked = await state(page);
      assert(picked.active === queued.now && picked.title === `Stop ${queued.now}` && picked.mode === "Dark Techno", `picked ${JSON.stringify(picked)}`);
      const round = [first.now, queued.now];
      while (round.length < techno.length) round.push((await skip(page, round.at(-1))).now);
      assert(new Set(round).size === techno.length && round.every((t) => techno.includes(t)), `the round played ${JSON.stringify(round)}`);
      const onward = await skip(page, round.at(-1));
      assert(onward.genre === "Dark Techno" && onward.mode === "Dark Techno", `the next round left the genre: ${JSON.stringify(onward)}`);
      await page.click(TOGGLE);
      await waitPlaying(page, false);
      await setFolds(page, []);
      await ready(page);
    });

    await run("a title of another genre moves next into that genre, play after a stop shuffles all genres again", async () => {
      await ready(page);
      await openPanel(page);
      await page.click(TOGGLE);
      await waitPlaying(page, true);
      assert((await state(page)).mode === "All genres", "play did not shuffle all genres");
      await setFolds(page, ["Deep Dubstep"]);
      const dubstep = await songsOf(page, "Deep Dubstep");
      await page.click('dc-song-player [data-song="fathom"]');
      await page.waitForFunction(() => document.querySelector("dc-song-player [data-song-now]").textContent === "Fathom", null, { timeout: 15000 });
      const fathom = await state(page);
      assert(fathom.mode === "Deep Dubstep" && fathom.running.join(",") === "Deep Dubstep" && fathom.nextTitle === "Next in Deep Dubstep", `the title did not move the mode: ${JSON.stringify(fathom)}`);
      const round = ["Fathom"];
      while (round.length < dubstep.length) {
        const next = await skip(page, round.at(-1));
        assert(next.genre === "Deep Dubstep" && next.mode === "Deep Dubstep", `next left the genre: ${JSON.stringify(next)}`);
        round.push(next.now);
      }
      assert(new Set(round).size === dubstep.length, `the round played ${JSON.stringify(round)}`);
      await page.click(TOGGLE);
      await waitPlaying(page, false);
      await page.click(TOGGLE);
      await waitPlaying(page, true);
      const play = await state(page);
      assert(play.mode === "All genres" && play.running.length === 0, `play after a Deep Dubstep round ${JSON.stringify(play)}`);
      await page.click(TOGGLE);
      await waitPlaying(page, false);
      await setFolds(page, []);
    });

    await run("the now playing area shows the genre's art, the title, the genre and the position against the sheet's length", async () => {
      await ready(page);
      await openPanel(page);
      await setFolds(page, ["Deep Dubstep"]);
      await page.click('dc-song-player [data-song="fathom"]');
      await page.waitForFunction(() => document.querySelector("dc-song-player [data-song-now]").textContent === "Fathom", null, { timeout: 15000 });
      const read = () => page.evaluate(() => {
        const root = document.querySelector("dc-song-player");
        const shown = (el) => el.getBoundingClientRect().height > 0 && getComputedStyle(el).visibility !== "hidden";
        const seconds = (text) => text.split(":").map(Number).reduce((total, part) => total * 60 + part, 0);
        return {
          art: root.querySelector("[data-song-art] .ti").className,
          artEq: shown(root.querySelector("[data-song-art] .dc-eq")),
          elapsed: seconds(root.querySelector("[data-song-elapsed]").textContent),
          length: root.querySelector("[data-song-length]").textContent,
          bar: parseFloat(root.querySelector("[data-song-progress]").style.width),
          rowEq: shown(root.querySelector('[data-song="fathom"] [data-song-eq]')),
          rowNumber: getComputedStyle(root.querySelector('[data-song="fathom"]'), "::before").content,
          otherEq: shown(root.querySelector('[data-song="sonar"] [data-song-eq]')),
          otherNumber: getComputedStyle(root.querySelector('[data-song="sonar"]'), "::before").content,
          genreEq: [...root.querySelectorAll("[data-song-genre-eq]")].filter(shown).map((eq) => eq.closest("[data-song-shelf]").dataset.songShelf),
        };
      });
      const start = await read();
      await sleep(2500);
      const later = await read();
      const now = await state(page);
      assert(now.now === "Fathom" && now.from === "Deep Dubstep" && now.mode === "Deep Dubstep", `the stage reads ${JSON.stringify(now)}`);
      assert(/ti-wave-sine/.test(start.art) && start.artEq, `the art ${JSON.stringify(start)}`);
      assert(start.length === "2:54", `Fathom's length reads ${start.length}`);
      assert(later.elapsed >= start.elapsed + 1 && later.bar > start.bar && later.bar < 100, `the position did not move: ${JSON.stringify({ start, later })}`);
      assert(start.rowEq && start.rowNumber === "none" && !start.otherEq && start.otherNumber !== "none", `the current song's mark ${JSON.stringify(start)}`);
      assert(start.genreEq.join(",") === "Deep Dubstep", `the genre marks ${JSON.stringify(start.genreEq)}`);
      await setFolds(page, []);
      const folded = await read();
      assert(folded.genreEq.join(",") === "Deep Dubstep", `the folded genre lost its mark: ${JSON.stringify(folded.genreEq)}`);
      await page.click(TOGGLE);
      await waitPlaying(page, false);
    });

    await require("./song-player-genres").genreChecks({ page, run, ready, openPanel, setFolds, state, waitPlaying, toggle: TOGGLE });

    await run("the status line note turns into an equalizer naming the song while music plays, a click on it opens the player and never stops the music", async () => {
      await ready(page);
      const note = () => page.evaluate(() => {
        const root = document.querySelector("dc-song-player");
        const shown = (el) => el.getBoundingClientRect().height > 0 && getComputedStyle(el).visibility !== "hidden";
        const bars = [...root.querySelectorAll("[data-song-note-eq] > i")];
        return {
          icon: shown(root.querySelector("[data-song-note]")),
          eq: shown(root.querySelector("[data-song-note-eq]")),
          title: root.querySelector("[data-song-open]").getAttribute("title"),
          label: root.querySelector("[data-song-open]").getAttribute("aria-label"),
          animations: bars.map((bar) => getComputedStyle(bar).animationName),
          heights: bars.map((bar) => bar.getBoundingClientRect().height),
        };
      });
      const idle = await note();
      assert(idle.icon && !idle.eq && idle.title === "Songs" && idle.label === "Songs", `the idle note ${JSON.stringify(idle)}`);
      await openPanel(page);
      await page.click(TOGGLE);
      await waitPlaying(page, true);
      const song = await state(page);
      await closePanel(page);
      assert(/ti-player-stop/.test((await state(page)).icon), "a click on the note stopped the music");
      const on = await note();
      assert(!on.icon && on.eq, `the playing note ${JSON.stringify(on)}`);
      assert(on.title === `${song.now} · ${song.genre}` && on.label === `Songs, playing ${song.now} from ${song.genre}`, `the playing note names ${JSON.stringify(on)}`);
      assert(on.animations.length === 3 && on.animations.every((name) => name === "dc-eq"), `the equalizer does not move: ${JSON.stringify(on.animations)}`);
      try {
        await page.emulateMedia({ reducedMotion: "reduce" });
        const still = await note();
        assert(still.eq && still.animations.every((name) => name === "none") && still.heights.every((height) => height > 0), `with reduced motion the equalizer ${JSON.stringify(still)}`);
      } finally {
        await page.emulateMedia({ reducedMotion: "no-preference" });
      }
      await page.click(NOTE);
      await page.waitForFunction((sel) => document.querySelector(sel).classList.contains("show"), MENU, { timeout: 5000 });
      const opened = await state(page);
      assert(opened.now === song.now && /ti-player-stop/.test(opened.icon), `opening the player changed the music: ${JSON.stringify(opened)}`);
      await page.click(TOGGLE);
      await waitPlaying(page, false);
      const off = await note();
      assert(off.icon && !off.eq && off.title === "Songs", `the stopped note ${JSON.stringify(off)}`);
      await closePanel(page);
    });

    await run("the filter finds songs in folded genres, counts the hits and never moves the panel", async () => {
      for (const open of [[], ["Deep Dubstep"]]) {
        await openPanel(page);
        await setFolds(page, open);
        const before = await view(page);
        await typeFilter(page, "canyon");
        const canyon = await view(page);
        const side = await page.evaluate(() => ({
          count: document.querySelector("dc-song-player [data-song-filter-count]").textContent,
          clear: getComputedStyle(document.querySelector("dc-song-player [data-song-filter-clear]")).visibility,
        }));
        assert(canyon.songs.join(",") === "Quiet Canyon", `canyon shows ${JSON.stringify(canyon.songs)}`);
        assert(canyon.genres.length === 1 && canyon.genres[0].name === "Chill Electronic" && canyon.genres[0].disabled && canyon.genres[0].count === "1", `canyon heads ${JSON.stringify(canyon.genres)}`);
        assert(side.count === "1 of 60" && side.clear === "visible", `count and clear ${JSON.stringify(side)}`);
        await typeFilter(page, "o");
        const many = await view(page);
        assert(new Set(many.genres.map((g) => g.name)).size > 1, `"o" found songs in ${JSON.stringify(many.genres)}`);
        await typeFilter(page, "zzz");
        const none = await page.evaluate(() => {
          const empty = document.querySelector("dc-song-player [data-song-empty]");
          return { text: empty.textContent, shown: getComputedStyle(empty).display !== "none", count: document.querySelector("dc-song-player [data-song-filter-count]").textContent };
        });
        assert(none.shown && none.text === 'Nothing matches "zzz".' && none.count === "0 of 60", `no hits ${JSON.stringify(none)}`);
        const zzz = await view(page);
        for (const [name, during] of [["canyon", canyon], ["o", many], ["zzz", zzz]]) {
          assert(Math.abs(during.filter - before.filter) < 1 && Math.abs(during.menu.top - before.menu.top) < 1, `"${name}" moved the panel with ${JSON.stringify(open)} open: ${JSON.stringify({ before: before.menu, during: during.menu })}`);
        }
        await typeFilter(page, "");
        const after = await view(page);
        assert(after.songs.join(",") === before.songs.join(",") && after.genres.every((g) => !g.disabled), `clearing the filter did not bring the folds back: ${JSON.stringify(after)}`);
        assert(Math.abs(after.menu.top - before.menu.top) < 1, "clearing the filter moved the panel");
      }
      await typeFilter(page, "tidal");
      await page.press(FILTER, "Enter");
      await page.waitForFunction(() => document.querySelector("dc-song-player [data-song-now]").textContent === "Tidal Echoes", null, { timeout: 15000 });
      await page.click(TOGGLE);
      await waitPlaying(page, false);
      await setFolds(page, []);
    });

    await run("hovering the list while typing keeps the cursor in the filter, the arrows walk the hits and Enter plays one", async () => {
      await openPanel(page);
      await setFolds(page, ["Chill Electronic"]);
      await page.focus(FILTER);
      await page.hover('dc-song-player [data-song="goldenhour"]');
      await page.hover('dc-song-player [data-song="lowsun"]');
      await page.keyboard.type("ow");
      const typed = await page.evaluate(() => {
        const filter = document.querySelector("dc-song-player [data-song-filter]");
        return {
          focused: document.activeElement === filter,
          value: filter.value,
          hits: [...document.querySelectorAll("dc-song-player [data-song]")].filter((i) => !i.hidden).map((i) => i.textContent),
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
      await page.waitForFunction((title) => document.querySelector("dc-song-player [data-song-now]").textContent === title, back, { timeout: 15000 });
      await page.click(TOGGLE);
      await waitPlaying(page, false);
      await setFolds(page, []);
    });

    await run("Escape clears a filled filter and keeps the panel open, on an empty filter it closes the panel", async () => {
      await openPanel(page);
      await setFolds(page, []);
      await typeFilter(page, "tidal");
      await page.press(FILTER, "Escape");
      await sleep(400);
      const value = await page.inputValue(FILTER);
      assert(value === "", `the filter still reads "${value}"`);
      assert(await menuShown(page), "Escape on a filled filter closed the panel");
      const cleared = await view(page);
      assert(cleared.songs.length === 0 && cleared.genres.length === 6 && cleared.genres.every((g) => !g.disabled && g.expanded === "false"), `the cleared filter shows ${JSON.stringify(cleared)}`);
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
      await setFolds(page, ["Dark Techno", "Deep Dubstep"]);
      const scrolled = await page.evaluate(async () => {
        const scroller = document.querySelector("dc-song-player [data-song-scroll]");
        const room = scroller.scrollHeight - scroller.clientHeight;
        scroller.scrollTop = Math.min(80, room);
        await new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)));
        return { top: scroller.scrollTop, room, stored: localStorage.getItem("dc-song-scroll") };
      });
      assert(scrolled.top >= 20 && scrolled.stored === String(scrolled.top), `the list position was not stored: ${JSON.stringify(scrolled)}`);
      await ready(page);
      await openPanel(page);
      const back = await page.evaluate(() => ({
        filter: document.querySelector("dc-song-player [data-song-filter]").value,
        top: document.querySelector("dc-song-player [data-song-scroll]").scrollTop,
      }));
      assert(back.filter === "" && Math.abs(back.top - scrolled.top) <= 1, `after the reload ${JSON.stringify(back)}, stored ${scrolled.top}`);
      await setFolds(page, []);
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
      await page.waitForFunction(() => document.querySelector("dc-song-player [data-song-now]")?.textContent, null, { timeout: 10000 });
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
        await fresh.route(/\/js\/dc\/songs\.[^/]*js/, (route) => { blocked++; return route.abort(); });
        await openPanel(fresh);
        await fresh.click(TOGGLE);
        await fresh.waitForFunction(() => [...document.querySelectorAll(".dc-toast")].some((t) => t.textContent.includes("Could not load the songs.")), null, { timeout: 10000 });
        assert(blocked > 0, "the songs file was never requested");
        assert(/ti-player-play/.test((await state(fresh)).icon), "the player claims to play without songs");
        await fresh.unroute(/\/js\/dc\/songs\.[^/]*js/);
        await openPanel(fresh);
        await fresh.click(TOGGLE);
        await waitPlaying(fresh, true);
        await fresh.click(TOGGLE);
        await waitPlaying(fresh, false);
      } finally {
        await ctx.close().catch(() => {});
      }
    });

    await run("the panel follows the light and the dark theme", async () => {
      const look = {};
      try {
        for (const theme of ["light", "dark"]) {
          await page.evaluate((name) => localStorage.setItem("dc-theme", name), theme);
          await ready(page);
          await openPanel(page);
          await setFolds(page, ["Deep Dubstep"]);
          await page.click('dc-song-player [data-song="fathom"]');
          await page.waitForFunction(() => document.querySelector("dc-song-player [data-song-now]").textContent === "Fathom", null, { timeout: 15000 });
          look[theme] = await page.evaluate(() => {
            const light = (color) => {
              const [r, g, b] = color.match(/[\d.]+/g).map(Number);
              return (0.2126 * r + 0.7152 * g + 0.0722 * b) / 255;
            };
            const root = document.querySelector("dc-song-player");
            const cyan = getComputedStyle(document.documentElement).getPropertyValue("--tblr-cyan-rgb").split(",").map((part) => part.trim());
            return {
              scheme: document.documentElement.getAttribute("data-bs-theme"),
              menu: light(getComputedStyle(root.querySelector(".dropdown-menu")).backgroundColor),
              title: light(getComputedStyle(root.querySelector("[data-song-now]")).color),
              art: getComputedStyle(root.querySelector("[data-song-art]")).color,
              cyan: `rgb(${cyan.join(", ")})`,
              stage: getComputedStyle(root.querySelector("[data-song-stage]")).backgroundImage,
            };
          });
          await page.click(TOGGLE);
          await waitPlaying(page, false);
          await setFolds(page, []);
          await closePanel(page);
        }
      } finally {
        await page.evaluate(() => localStorage.removeItem("dc-theme"));
      }
      assert(look.light.scheme !== "dark" && look.light.menu > 0.8 && look.light.title < 0.4, `light ${JSON.stringify(look.light)}`);
      assert(look.dark.scheme === "dark" && look.dark.menu < 0.3 && look.dark.title > 0.7, `dark ${JSON.stringify(look.dark)}`);
      for (const theme of ["light", "dark"]) {
        assert(look[theme].art === look[theme].cyan && /gradient/.test(look[theme].stage), `${theme} art and stage ${JSON.stringify(look[theme])}`);
      }
      await ready(page);
    });

    await run("a phone never sees the player", async () => {
      const phone = await mobilePage();
      await phone.goto(`${BASE}/projects`, { waitUntil: "domcontentloaded" });
      await dismissUpdate(phone);
      const shown = await phone.$eval("dc-song-player", (el) => el.getBoundingClientRect().height > 0 && getComputedStyle(el.closest(".dc-status")).display !== "none");
      assert(!shown, "the player shows on a phone");
    });
  } finally {
    await page.evaluate(() => {
      const icon = document.querySelector("dc-song-player [data-song-toggle] .ti");
      if (icon && icon.className.includes("ti-player-stop")) document.querySelector("dc-song-player [data-song-toggle]").click();
    }).catch(() => {});
  }
});
