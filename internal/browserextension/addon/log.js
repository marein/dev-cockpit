import { api, everyPage } from "./api.js";
import { readLogEntry, samePage } from "./browser-context.js";

// The callers queue every change.

// Local, not session storage: an update of the extension keeps the lists.
const area = api.storage.local;
export const logKey = (tabId) => `log:${tabId}`;

const MAX_ENTRIES = 500;
const MAX_BATCH = 200;
const PAGES = ["http://*/*", "https://*/*"];
const CATCH = [
  { id: "dc-console-relay", js: ["relay.js"], matches: PAGES, runAt: "document_start" },
  { id: "dc-console-catch", js: ["console.js", "catch.js"], matches: PAGES, runAt: "document_start", world: "MAIN" },
];

let catching = Promise.resolve();

export function catchConsole() {
  catching = catching.then(async () => {
    const want = await everyPage();
    const ids = CATCH.map((one) => one.id);
    const have = (await api.scripting.getRegisteredContentScripts().catch(() => [])).filter((one) => ids.includes(one.id));
    if (want && have.length === CATCH.length) return;
    if (have.length) await api.scripting.unregisterContentScripts({ ids: have.map((one) => one.id) }).catch(() => {});
    if (want) await api.scripting.registerContentScripts(CATCH).catch(() => {});
  }, () => {});
  return catching;
}

const emptyLog = () => ({ entries: [], seq: 0, left: 0, pageLeft: 0, url: "", viewport: { w: 0, h: 0 }, stale: false });

function viewportOf(raw) {
  const size = (n) => (Number.isFinite(n) ? Math.max(0, Math.min(1e5, Math.round(n))) : 0);
  return raw && typeof raw === "object" ? { w: size(raw.w), h: size(raw.h) } : { w: 0, h: 0 };
}

function pageCounts(log) {
  let errors = 0;
  let warnings = 0;
  for (let i = log.entries.length - 1; i >= 0; i -= 1) {
    const entry = log.entries[i];
    if (entry.mark) break;
    if (entry.level === "warn") warnings += 1;
    else errors += 1;
  }
  return { errors, warnings };
}

export async function relabel(tabId, log) {
  const { errors, warnings } = pageCounts(log);
  const count = errors + warnings;
  try {
    if (count) await api.action.setBadgeBackgroundColor({ tabId, color: errors ? "#d63939" : "#f59f00" });
    await api.action.setBadgeText({ tabId, text: count > 99 ? "99+" : count ? String(count) : "" });
  } catch {}
}

async function keep(tabId, log) {
  try {
    await area.set({ [logKey(tabId)]: log });
  } catch {
    const cut = log.entries.length - 50;
    if (cut > 0) {
      log.left += log.entries.slice(0, cut).filter((one) => !one.mark).length;
      log.entries = log.entries.slice(cut);
    }
    await area.set({ [logKey(tabId)]: log }).catch(() => {});
  }
}

async function changeLog(tabId, change, { make = true } = {}) {
  const kept = (await area.get(logKey(tabId)))[logKey(tabId)];
  if (!kept && !make) return;
  const log = { ...emptyLog(), ...(kept || {}) };
  if (change(log) === false) return;
  await keep(tabId, log);
  await relabel(tabId, log);
}

function navigated(log, url, reload) {
  if (reload) {
    Object.assign(log, { entries: [], left: 0, pageLeft: 0 });
  } else if (url && log.entries.length) {
    const last = log.entries.at(-1);
    if (last.mark) Object.assign(last, { url, time: Date.now() });
    else log.entries.push({ mark: true, url, time: Date.now() });
  }
  log.url = url;
}

export function logDoc(tabId, url, message) {
  return changeLog(tabId, (log) => {
    log.stale = false;
    log.pageLeft = 0;
    log.viewport = viewportOf(message.viewport);
    navigated(log, url, message.nav === "reload");
  });
}

export function logBatch(tabId, url, message) {
  const batch = Array.isArray(message.entries) ? message.entries.slice(0, MAX_BATCH) : [];
  const now = Date.now();
  return changeLog(tabId, (log) => {
    log.stale = false;
    if (log.url && !samePage(log.url, url)) navigated(log, url, false);
    for (const raw of batch) {
      if (!raw || typeof raw !== "object" || raw.mark) continue;
      const entry = readLogEntry(raw);
      if (!entry) continue;
      log.seq += 1;
      log.entries.push({ ...entry, time: now, id: log.seq, page: url });
    }
    const dropped = Number.isSafeInteger(message.dropped) && message.dropped > 0 ? Math.min(message.dropped, 1e9) : 0;
    if (dropped > log.pageLeft) {
      log.left += dropped - log.pageLeft;
      log.pageLeft = dropped;
    }
    if (log.entries.length > MAX_ENTRIES) {
      const gone = log.entries.splice(0, log.entries.length - MAX_ENTRIES);
      log.left += gone.filter((one) => !one.mark).length;
    }
    log.viewport = viewportOf(message.viewport);
  });
}

export function logMoved(tabId, url) {
  return changeLog(tabId, (log) => {
    if (samePage(log.url, url)) return false;
    navigated(log, url, false);
  }, { make: false });
}
