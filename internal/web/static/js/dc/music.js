import { getPlayer, setMasterVolume } from "@marein/js-scriptune";

// Each player has its own volume, the master is only their common output.
setMasterVolume(1);
const music = getPlayer("music");

export const catalog = [
  {
    style: "Party DnB",
    icon: "ti-flame", color: "yellow",
    songs: {
      pinatubo: "Pinatubo", tambora: "Tambora", bromo: "Bromo", stromboli: "Stromboli", kilauea: "Kilauea",
      merapi: "Merapi", mayon: "Mayon", etna: "Etna", fuji: "Fuji", krakatoa: "Krakatoa",
      cotopaxi: "Cotopaxi", hekla: "Hekla", teide: "Teide", ruapehu: "Ruapehu", santorini: "Santorini",
      erebus: "Erebus", arenal: "Arenal", sakurajima: "Sakurajima", surtsey: "Surtsey", villarrica: "Villarrica",
    },
  },
  {
    style: "Party Dubstep",
    icon: "ti-wave-saw-tool", color: "lime",
    songs: {
      kraken: "Kraken", hydra: "Hydra", chimera: "Chimera", basilisk: "Basilisk", manticore: "Manticore",
      wyvern: "Wyvern", gorgon: "Gorgon", leviathan: "Leviathan", cerberus: "Cerberus", banshee: "Banshee",
    },
  },
  {
    style: "Dark Techno",
    icon: "ti-bolt", color: "indigo",
    songs: {
      basalt: "Basalt", obsidian: "Obsidian", cinder: "Cinder", graphite: "Graphite", magnetite: "Magnetite",
      slate: "Slate", caldera: "Caldera", faultline: "Fault Line", anthracite: "Anthracite", tephra: "Tephra",
    },
  },
  {
    style: "Deep Dubstep",
    icon: "ti-wave-sine", color: "cyan",
    songs: {
      undertow: "Undertow", fathom: "Fathom", pressure: "Pressure", silt: "Silt", ballast: "Ballast",
      nightferry: "Night Ferry", landslide: "Landslide", sonar: "Sonar", tremor: "Tremor", blackice: "Black Ice",
    },
  },
  {
    style: "Melodic House",
    icon: "ti-moon-stars", color: "pink",
    songs: {
      midnightbloom: "Midnight Bloom", afterglow: "Afterglow", oceanfloor: "Ocean Floor", citypulse: "City Pulse",
      velvethours: "Velvet Hours", distantshores: "Distant Shores", tidalechoes: "Tidal Echoes", weightless: "Weightless",
      parallellines: "Parallel Lines", daybreak: "Daybreak",
    },
  },
  {
    style: "Chill Electronic",
    icon: "ti-sunset-2", color: "orange",
    songs: {
      ambermile: "Amber Mile", goldenhour: "Golden Hour", saltflats: "Salt Flats", lowsun: "Low Sun",
      paperlanterns: "Paper Lanterns", coastaldrive: "Coastal Drive", willowcreek: "Willow Creek", driftwood: "Driftwood",
      quietcanyon: "Quiet Canyon", rearview: "Rearview",
    },
  },
  {
    style: "Space Ambient",
    icon: "ti-planet", color: "purple",
    songs: {
      apogee: "Apogee", perigee: "Perigee", aphelion: "Aphelion", zenith: "Zenith", parallax: "Parallax",
      albedo: "Albedo", umbra: "Umbra", lagrange: "Lagrange", redshift: "Redshift", nadir: "Nadir",
    },
  },
  {
    style: "Classic Rock",
    icon: "ti-guitar-pick", color: "red",
    songs: {
      chinook: "Chinook", bora: "Bora", meltemi: "Meltemi", khamsin: "Khamsin", tramontane: "Tramontane",
      zonda: "Zonda", halny: "Halny", sundowner: "Sundowner", shamal: "Shamal", williwaw: "Williwaw",
    },
  },
];

const titles = Object.assign({}, ...catalog.map((shelf) => shelf.songs));
const shelves = Object.fromEntries(catalog.flatMap((shelf) => Object.keys(shelf.songs).map((key) => [key, shelf])));
const lengths = new Map();
const listeners = new Set();
let sheets = null;
let attempts = 0;
let scope = null;
let queue = [];
let current = null;

// A module that failed to load stays failed in the browser's module map, so a
// retry asks for the same file under a fresh URL.
function loadSheets() {
  if (!sheets) {
    attempts++;
    const url = attempts === 1 ? "@dc/songs" : `${import.meta.resolve("@dc/songs")}?retry=${attempts}`;
    sheets = import(url).then((module) => module.compose, (error) => {
      sheets = null;
      throw error;
    });
  }
  return sheets;
}

const beats = { w: 4, h: 2, q: 1, e: 0.5, s: 0.25, dw: 6, dh: 3, dq: 1.5, de: 0.75, ds: 0.375 };

// scriptune tells nobody how long a sheet plays, so this reads it the way
// scriptune times it: one BPM for every track, read as a whole number, a loop
// handing its notes to the track that stands at its end, and the song over
// when its longest track is.
function seconds(sheet) {
  const tracks = new Map();
  const loops = [];
  let track = "default";
  let bpm = 120;
  const add = (time) => {
    if (loops.length) loops.at(-1).time += time;
    else tracks.set(track, (tracks.get(track) ?? 0) + time);
  };
  for (const raw of sheet.split("\n")) {
    const line = raw.replace(/;.*/, "").trim();
    if (line === "") continue;
    const [command, ...args] = line.startsWith("#") ? line.split(/\s+/) : ["#NOTES", ...line.split(/\s+/)];
    if (command === "#BPM") bpm = parseInt(args[0]);
    else if (command === "#TRACK") track = args[0];
    else if (command === "#LOOP") loops.push({ count: parseInt(args[0]), time: 0 });
    else if (command === "#ENDLOOP") {
      const loop = loops.pop();
      add(loop.count * loop.time);
    } else if (command === "#NOTES") {
      for (const cell of args) add((beats[cell.split(":")[1]] * 60) / bpm);
    }
  }
  return Math.max(0, ...tracks.values());
}

function shuffled() {
  if (!queue.length) {
    queue = Object.keys(scope?.songs ?? titles).filter((key) => key !== current?.key);
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

// Composing a song blocks the page for a moment, so the answer to a click
// gets a frame on the screen first. A song that follows on its own waits for
// none, a hidden page draws no frames.
const drawn = () => new Promise((resolve) => requestAnimationFrame(() => setTimeout(resolve)));

async function start(shelf, key, frame = true) {
  if (shelf !== scope) {
    scope = shelf;
    queue = [];
  }
  const next = key ?? shuffled();
  queue = queue.filter((other) => other !== next);
  current?.abort.abort();
  const abort = new AbortController();
  current = { key: next, abort, started: performance.now() };
  changed();
  let sheet;
  try {
    const compose = await loadSheets();
    if (frame) await drawn();
    if (abort.signal.aborted) return;
    sheet = compose(next);
  } catch (error) {
    if (current?.abort === abort) stop();
    throw error;
  }
  if (!lengths.has(next)) lengths.set(next, seconds(sheet));
  current.started = performance.now();
  changed();
  music.play(sheet, { signal: abort.signal })
    .then(() => current?.abort === abort && start(scope, null, false))
    .catch(() => current?.abort === abort && stop());
}

export function onChange(listener) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function title(key) {
  return titles[key] ?? key;
}

export function genreOf(key) {
  return shelves[key]?.style ?? null;
}

export function playing() {
  return current?.key ?? null;
}

// The genre the shuffle keeps to, null while it runs through every genre.
export function mode() {
  return current ? scope?.style ?? null : null;
}

export function position() {
  if (!current) return null;
  return { elapsed: (performance.now() - current.started) / 1000, length: lengths.get(current.key) };
}

export function play(key) {
  return start(shelves[key], key);
}

export function shuffle(style) {
  return start(catalog.find((shelf) => shelf.style === style) ?? null);
}

export function next() {
  return start(scope);
}

export function stop() {
  current?.abort.abort();
  current = null;
  changed();
}
