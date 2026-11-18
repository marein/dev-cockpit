import { api } from "./api.js";
import { relabel } from "./log.js";
import { dropAllPictures, keepPictures } from "./pictures.js";

// Trays and console lists stay in local storage, so an update of the
// extension keeps them. They name tabs by id and ids start over with the
// browser: an update keeps what belongs to a tab still open, a start of the
// browser or a new install keeps nothing, and so does a start with neither
// sign, as after the extension was switched off and on.

export const kept = api.storage.local;
export const trayKey = (tabId) => `tray:${tabId}`;
export const isKept = (key) => /^(?:tray|log):\d+$/.test(key);

// Session storage outlives a sleeping worker, not an update or a restart.
const RUNNING = "running";
const SIGN_WAIT = 1000;

let decided = false;
let updated = false;
let settle;
export const ready = new Promise((resolve) => { settle = resolve; });

async function forget() {
  const all = await kept.get(null).catch(() => ({}));
  await kept.remove(Object.keys(all).filter(isKept)).catch(() => {});
  await dropAllPictures();
}

// The catch of a page open across the update went with the old extension,
// its list stays and says the page needs a reload.
async function adopt() {
  const all = await kept.get(null).catch(() => ({}));
  const open = new Set((await api.tabs.query({}).catch(() => [])).map((tab) => tab.id));
  const gone = [];
  const logs = {};
  const pictures = [];
  for (const [key, value] of Object.entries(all)) {
    if (!isKept(key)) continue;
    const tabId = Number(key.slice(key.indexOf(":") + 1));
    if (!open.has(tabId) || !value || typeof value !== "object") gone.push(key);
    else if (key.startsWith("log:")) logs[key] = { ...value, stale: true };
    else pictures.push(...(Array.isArray(value.items) ? value.items : []).filter((one) => one?.item?.kind === "image").map((one) => one.key));
  }
  await kept.remove(gone).catch(() => {});
  await kept.set(logs).catch(() => {});
  await keepPictures(pictures);
  for (const [key, log] of Object.entries(logs)) await relabel(Number(key.slice(4)), log);
}

async function decide(update) {
  if (decided) return;
  decided = true;
  updated = update;
  await (update ? adopt() : forget());
  await api.storage.session.set({ [RUNNING]: true }).catch(() => {});
  settle();
}

export function extensionUpdated() {
  void decide(true);
}

// A browser that starts with an extension updated while it was closed signs
// both, the start wins. queue runs it behind every change already waiting.
export function browserStarted(queue) {
  if (!decided) {
    void decide(false);
    return;
  }
  if (!updated) return;
  updated = false;
  void queue(forget);
}

api.storage.session.get(RUNNING).then((had) => had[RUNNING] === true, () => false).then((running) => {
  if (!running) {
    setTimeout(() => void decide(false), SIGN_WAIT);
    return;
  }
  if (decided) return;
  decided = true;
  settle();
});
