import { getPlayer } from "@marein/js-scriptune";

const music = getPlayer("music");

export const catalog = [
  {
    style: "Chill Electronic",
    songs: {
      ambermile: "Amber Mile", goldenhour: "Golden Hour", saltflats: "Salt Flats", lowsun: "Low Sun",
      paperlanterns: "Paper Lanterns", coastaldrive: "Coastal Drive", willowcreek: "Willow Creek", driftwood: "Driftwood",
      quietcanyon: "Quiet Canyon", rearview: "Rearview",
    },
  },
  {
    style: "Melodic House",
    songs: {
      midnightbloom: "Midnight Bloom", afterglow: "Afterglow", oceanfloor: "Ocean Floor", citypulse: "City Pulse",
      velvethours: "Velvet Hours", distantshores: "Distant Shores", tidalechoes: "Tidal Echoes", weightless: "Weightless",
      parallellines: "Parallel Lines", daybreak: "Daybreak",
    },
  },
];

const titles = Object.assign({}, ...catalog.map((shelf) => shelf.songs));
const listeners = new Set();
let sheets = null;
let attempts = 0;
let queue = [];
let current = null;

// A module that failed to load stays failed in the browser's module map, so a
// retry asks for the same file under a fresh URL.
function loadSheets() {
  if (!sheets) {
    attempts++;
    const url = attempts === 1 ? "./songs.js" : `${import.meta.resolve("./songs.js")}?retry=${attempts}`;
    sheets = import(url).then((module) => module.songs, (error) => {
      sheets = null;
      throw error;
    });
  }
  return sheets;
}

function shuffled(all) {
  if (!queue.length) {
    queue = Object.keys(all).filter((key) => key !== current?.key);
    for (let i = queue.length - 1; i > 0; i--) {
      const j = Math.floor(Math.random() * (i + 1));
      [queue[i], queue[j]] = [queue[j], queue[i]];
    }
  }
  return queue.pop();
}

function changed() {
  for (const listener of listeners) listener();
}

export function onChange(listener) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function title(key) {
  return titles[key] ?? key;
}

export function playing() {
  return current?.key ?? null;
}

export async function play(key) {
  const all = await loadSheets();
  const next = key ?? shuffled(all);
  queue = queue.filter((other) => other !== next);
  current?.abort.abort();
  const abort = new AbortController();
  current = { key: next, abort };
  changed();
  music.play(all[next], { signal: abort.signal })
    .then(() => current?.abort === abort && play())
    .catch(() => current?.abort === abort && stop());
}

export function stop() {
  current?.abort.abort();
  current = null;
  changed();
}
