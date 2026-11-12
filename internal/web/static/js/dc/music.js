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
const PING_MS = 1500;
const MISSES = 2;
let sheets = null;
let attempts = 0;
let sound = null;
let shown = null;
let ended = null;
let watch = null;
let unanswered = 0;

// The tabs of a browser share one playback. The tab that plays owns it and
// tells the others what it plays, they only show it. A click makes its own
// tab the owner, the autoplay policy lets no other tab start the sound.
const channel = typeof BroadcastChannel === "function" ? new BroadcastChannel("dc-music") : null;
const tab = Array.from(crypto.getRandomValues(new Uint32Array(2)), (part) => part.toString(36)).join("-");

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

// A sheet opens on its song's tempo, every later #BPM only times a voice's
// notes, so the first one is the beat the song's dancer follows.
function tempo(sheet) {
  return Number(/^#BPM (\d+)$/m.exec(sheet)?.[1]) || 120;
}

function shelfOf(style) {
  return catalog.find((shelf) => shelf.style === style) ?? null;
}

function shuffled(keys) {
  for (let i = keys.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1));
    [keys[i], keys[j]] = [keys[j], keys[i]];
  }
  return keys;
}

function post(message) {
  channel?.postMessage(message);
}

function show(state) {
  unanswered = 0;
  const same = JSON.stringify(state) === JSON.stringify(shown);
  shown = state;
  follow();
  if (!same) for (const listener of listeners) listener();
}

function publish(state) {
  show(state);
  post({ type: "state", state });
}

function silence() {
  sound?.abort();
  sound = null;
}

// Two tabs that start at once both claim the playback, the later click wins.
// A song that follows on its own keeps the claim of the click that started
// its round, so it never wins over a click in another tab.
function wins(a, b) {
  return a.claim > b.claim || (a.claim === b.claim && a.owner > b.owner);
}

// A tab that closes or crashes may never say so, so a tab that shows another
// tab's playback keeps asking after it and shows nothing once it stays silent.
function follow() {
  const away = !!shown && shown.owner !== tab;
  if (away === !!watch) return;
  clearInterval(watch);
  watch = away ? setInterval(() => (unanswered++ < MISSES ? post({ type: "ping" }) : show(null)), PING_MS) : null;
}

// A stop ends the round of its claim, an answer about that round still on its
// way when the stop arrives must not bring it back.
function receive({ type, state, owner, claim }) {
  if (type === "hello" || type === "ping") {
    if (shown?.owner === tab) post({ type: "state", state: shown });
  } else if (type === "stop") {
    ended = { owner, claim };
    if (owner === shown?.owner && claim >= shown.claim) {
      silence();
      show(null);
    }
  } else if (type === "state") {
    if (state.owner === ended?.owner && state.claim <= ended.claim) return;
    if (shown?.owner === tab && wins(shown, state)) post({ type: "state", state: shown });
    else if (!shown || state.owner === shown.owner || wins(state, shown)) {
      silence();
      show(state);
    }
  }
}

// Composing a song blocks the page for a moment, so the answer to a click
// gets a frame on the screen first. A song that follows on its own waits for
// none, a hidden page draws no frames.
const drawn = () => new Promise((resolve) => requestAnimationFrame(() => setTimeout(resolve)));

async function start(shelf, key, claim = Date.now(), frame = true) {
  const style = shelf?.style ?? null;
  let round = shown?.style === style ? shown.round : [];
  if (!key) {
    if (!round.length) round = shuffled(Object.keys(shelf?.songs ?? titles).filter((other) => other !== shown?.key));
    key = round.at(-1);
  }
  round = round.filter((other) => other !== key);
  silence();
  const abort = new AbortController();
  sound = abort;
  const song = { owner: tab, claim, key, style, round, started: Date.now(), length: lengths.get(key) };
  publish(song);
  let sheet;
  try {
    const compose = await loadSheets();
    if (frame) await drawn();
    if (abort.signal.aborted) return;
    sheet = compose(key);
  } catch (error) {
    if (sound === abort) stop();
    throw error;
  }
  if (!lengths.has(key)) lengths.set(key, seconds(sheet));
  publish({ ...song, started: Date.now(), length: lengths.get(key), bpm: tempo(sheet) });
  music.play(sheet, { signal: abort.signal })
    .then(() => sound === abort && start(shelf, null, claim, false))
    .catch(() => sound === abort && stop());
}

channel?.addEventListener("message", (event) => receive(event.data));
post({ type: "hello" });
addEventListener("pagehide", () => {
  if (shown?.owner === tab) stop();
});
addEventListener("pageshow", (event) => {
  if (!event.persisted) return;
  show(null);
  post({ type: "hello" });
});
// scriptune applies a volume only to the sound of the tab that sets it, so
// the playing tab takes a move made in another tab from their shared storage.
addEventListener("storage", () => music.setVolume(music.getVolume()));

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
  return shown?.key ?? null;
}

// The genre the shuffle keeps to, null while it runs through every genre.
export function mode() {
  return shown?.style ?? null;
}

export function position() {
  if (!shown) return null;
  return { elapsed: (Date.now() - shown.started) / 1000, length: shown.length, bpm: shown.bpm };
}

export function play(key) {
  return start(shelves[key], key);
}

export function shuffle(style) {
  return start(shelfOf(style));
}

export function next() {
  return start(shelfOf(shown?.style));
}

export function stop() {
  const was = shown;
  silence();
  show(null);
  if (!was) return;
  ended = { owner: was.owner, claim: was.claim };
  post({ type: "stop", ...ended });
}
