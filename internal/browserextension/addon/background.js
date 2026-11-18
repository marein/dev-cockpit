import { api, everyPage, fromOwnPage, PANEL_CLOSED } from "./api.js";
import { MAX_IMAGES, MAX_ITEMS, MAX_LOG_ENTRIES, MAX_LOGS, MAX_NOTE, readElement, readImage, readLog, sameElementPage } from "./browser-context.js";
import { browserStarted, extensionUpdated, isKept, kept, ready, trayKey } from "./kept.js";
import { catchConsole, logBatch, logDoc, logKey, logMoved } from "./log.js";
import { dropPictures } from "./pictures.js";
import { BROWSER } from "./version.js";

// State lives in storage, Chrome ends an idle service worker. Picking and
// drawing name the page's scripts of this run in session storage, trays and
// console lists stay as kept.js says. This is its one writer, the panel and
// the content scripts ask by message. It never holds the token.

const FILES = ["pick.js", "draw.js", "content.js"];
const pickKey = (tabId) => `pick:${tabId}`;
const drawKey = (tabId) => `draw:${tabId}`;
const TOOLS = "draw-tools";
const DRAW_TOOLS = ["pen", "arrow", "rect", "text"];
const DRAW_COLORS = ["red", "orange", "green", "blue", "purple"];

let queue = ready;

// Queued, so a pick that comes while the panel removes a chip loses neither.
function serial(work) {
  const run = queue.then(work, work);
  queue = run.catch(() => {});
  return run;
}

const areaOf = (key) => (isKept(key) ? kept : api.storage.session);

async function read(key, fallback) {
  if (isKept(key)) await ready;
  const stored = await areaOf(key).get(key);
  return stored[key] ?? fallback;
}

const write = (key, value) => areaOf(key).set({ [key]: value });

const emptyTray = () => ({ note: "", items: [], current: "", full: false, problem: "" });
const emptyDraw = () => ({ on: false, count: 0, undo: false, redo: false });

function newKey() {
  return `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 8)}`;
}

async function changeTray(tabId, change) {
  return serial(async () => {
    const tray = await read(trayKey(tabId), emptyTray());
    const answer = change(tray);
    await write(trayKey(tabId), tray);
    return answer;
  });
}

async function setPick(tabId, state) {
  await serial(async () => {
    const before = await read(pickKey(tabId), { on: false, doc: "", resume: false });
    await write(pickKey(tabId), { ...before, ...state });
  });
}

function drawState(answer) {
  if (!answer || typeof answer !== "object") return emptyDraw();
  const count = Number.isSafeInteger(answer.count) ? Math.max(0, Math.min(1000, answer.count)) : 0;
  return { on: answer.drawing === true, count, undo: answer.undo === true, redo: answer.redo === true };
}

const setDraw = (tabId, state) => serial(() => write(drawKey(tabId), state));

async function readTools() {
  const chosen = (await api.storage.local.get(TOOLS))[TOOLS] || {};
  return { tool: DRAW_TOOLS.includes(chosen.tool) ? chosen.tool : "pen", color: DRAW_COLORS.includes(chosen.color) ? chosen.color : "red" };
}

function openPanel(tab) {
  try {
    if (api.sidePanel?.open) api.sidePanel.open({ windowId: tab.windowId }).catch(() => {});
    else api.sidebarAction?.open?.().catch(() => {});
  } catch {}
}

async function tellTab(tabId, message) {
  try {
    return await api.tabs.sendMessage(tabId, message, { frameId: 0 });
  } catch {
    return null;
  }
}

// A tab whose address the extension may not read is one it may not enter
// either, the browser would hold the script back without an answer.
async function reach(tabId) {
  const there = await tellTab(tabId, { type: "hello" });
  if (there) return true;
  if (!(await api.tabs.get(tabId).catch(() => null))?.url) return false;
  try {
    await api.scripting.executeScript({ target: { tabId, frameIds: [0] }, files: FILES });
    return true;
  } catch {
    return false;
  }
}

const CLOSED = "The extension cannot pick or draw on this page. Browser pages and the extension stores stay closed to it.";
const NO_ACCESS = "The extension cannot pick or draw on this page. Browser pages and the extension stores stay closed to it, on other pages click its button in the toolbar or press its key.";
// Safari asks the user for each website, whatever the setup was granted.
const SAFARI = "The extension cannot pick or draw on this page. Safari's own pages stay closed to it, a website opens once you allow it under Safari › Settings › Websites.";

async function refuse(tabId) {
  const problem = BROWSER === "safari" ? SAFARI : (await everyPage()) ? CLOSED : NO_ACCESS;
  await changeTray(tabId, (tray) => { tray.problem = problem; });
  return problem;
}

async function setPicking(tabId, on) {
  if (on && !(await reach(tabId))) {
    await refuse(tabId);
    await setPick(tabId, { on: false, resume: false });
    return false;
  }
  if (on) await setDrawing(tabId, false);
  const answer = await tellTab(tabId, { type: "pick", on });
  const picking = Boolean(answer?.picking);
  await setPick(tabId, { on: picking, doc: typeof answer?.doc === "string" ? answer.doc : "", resume: picking });
  await changeTray(tabId, (tray) => {
    tray.problem = "";
    if (picking) tray.full = false;
  });
  return picking;
}

async function setDrawing(tabId, on) {
  const now = await read(drawKey(tabId), emptyDraw());
  if (!on && !now.on) return;
  if (on && !(await reach(tabId))) {
    await refuse(tabId);
    return;
  }
  if (on && (await read(pickKey(tabId), { on: false })).on) await setPicking(tabId, false);
  const tools = await readTools();
  await setDraw(tabId, drawState(await tellTab(tabId, { type: "draw", on, ...tools })));
  if (on) await changeTray(tabId, (tray) => { tray.problem = ""; });
}

// The page gets the tools before the panel shows them, so a click right after
// uses what the panel shows.
async function chooseTools(tabId, tool, color) {
  const tools = await readTools();
  const next = { tool: DRAW_TOOLS.includes(tool) ? tool : tools.tool, color: DRAW_COLORS.includes(color) ? color : tools.color };
  if ((await read(drawKey(tabId), emptyDraw())).on) await setDraw(tabId, drawState(await tellTab(tabId, { type: "draw", on: true, ...next })));
  await api.storage.local.set({ [TOOLS]: next });
}

async function stop(tabId) {
  const pick = await read(pickKey(tabId), { on: false, resume: false });
  if (pick.on || pick.resume) await setPicking(tabId, false);
  await setDrawing(tabId, false);
}

async function toggle(tab) {
  if (!tab || typeof tab.id !== "number" || tab.id < 0) return;
  const state = await read(pickKey(tab.id), { on: false });
  await setPicking(tab.id, !state.on);
}

api.runtime.onInstalled.addListener((details) => {
  if (details.reason === "update") extensionUpdated();
  else browserStarted(serial);
  if (details.reason === "install") void api.runtime.openOptionsPage();
});

api.runtime.onStartup.addListener(() => browserStarted(serial));

api.action.onClicked.addListener((tab) => {
  openPanel(tab);
  void toggle(tab);
});

function isElement(item) {
  return item.kind !== "log" && item.kind !== "image";
}

async function picked(tabId, message) {
  const pick = await read(pickKey(tabId), { on: false, doc: "" });
  const doc = typeof message.doc === "string" ? message.doc.slice(0, 40) : "";
  if (!doc || doc !== pick.doc) return;
  const handle = Number.isSafeInteger(message.handle) && message.handle > 0 ? message.handle : 0;
  const replaces = Number.isSafeInteger(message.replaces) && message.replaces > 0 ? message.replaces : 0;
  const raw = message.element && typeof message.element === "object" ? message.element : null;
  const item = readElement(raw, typeof message.url === "string" ? message.url : "");
  if (!handle || !item) return;
  const flags = { parent: raw.parent === true, child: raw.child === true };
  await changeTray(tabId, (tray) => {
    if (replaces) {
      const entry = tray.items.find((one) => one.handle === replaces && one.doc === doc);
      if (!entry) return;
      Object.assign(entry, { item, handle, gone: false, rev: (entry.rev || 0) + 1 }, flags);
      tray.current = entry.key;
      return;
    }
    if (!pick.on) return;
    const same = tray.items.find((one) => isElement(one.item) && one.item.selector === item.selector && sameElementPage(one.item.url, item.url));
    if (same) {
      Object.assign(same, { item, handle, doc, gone: false, rev: (same.rev || 0) + 1 }, flags);
      tray.current = same.key;
      return;
    }
    if (tray.items.filter((one) => isElement(one.item)).length >= MAX_ITEMS) {
      tray.full = true;
      return;
    }
    const key = newKey();
    tray.items.push({ key, item, handle, doc, gone: false, rev: 1, ...flags });
    tray.current = key;
  });
}

// The relay's address follows views the page loads itself, held to the origin
// the browser names, which the page cannot forge.
function shownAddress(sender, message) {
  const loaded = typeof sender.url === "string" ? sender.url : "";
  try {
    if (typeof message.url === "string" && new URL(message.url).origin === new URL(loaded).origin) return message.url;
  } catch {}
  return loaded;
}

async function fromContent(message, sender) {
  const tabId = sender.tab?.id;
  if (typeof tabId !== "number" || sender.frameId !== 0) return;
  switch (message.type) {
    case "picking":
      await setPick(tabId, { on: message.on === true, resume: message.on === true });
      if (message.on !== true) await changeTray(tabId, (tray) => { tray.full = false; });
      return;
    case "picked":
      await picked(tabId, message);
      return;
    case "highlighted": {
      const key = typeof message.key === "string" ? message.key.slice(0, 40) : "";
      await changeTray(tabId, (tray) => {
        const entry = tray.items.find((one) => one.key === key);
        if (entry) entry.gone = message.found !== true;
      });
      return;
    }
    case "drawn":
      await setDraw(tabId, drawState(message));
      return;
    case "draw-off":
      await serial(async () => write(drawKey(tabId), { ...(await read(drawKey(tabId), emptyDraw())), on: false }));
      return;
    case "console-doc":
      await serial(() => logDoc(tabId, shownAddress(sender, message), message));
      return;
    case "console":
      await serial(() => logBatch(tabId, shownAddress(sender, message), message));
      return;
    default:
  }
}

function entryOf(tray, key) {
  return tray.items.find((one) => one.key === key) || null;
}

const pictureKeys = (entries) => entries.filter((one) => one.item.kind === "image").map((one) => one.key);

async function highlight(tabId, key, reveal) {
  const tray = await read(trayKey(tabId), emptyTray());
  const pick = await read(pickKey(tabId), { on: false, doc: "" });
  const found = key ? entryOf(tray, key) : null;
  const entry = found && isElement(found.item) ? found : null;
  const address = (await api.tabs.get(tabId).catch(() => null))?.url || "";
  if (!entry || !address || !sameElementPage(entry.item.url, address)) {
    await tellTab(tabId, { type: "highlight", handle: 0, selector: "" });
    if (entry && address) await changeTray(tabId, (next) => { const one = entryOf(next, key); if (one) one.gone = true; });
    return;
  }
  const handle = entry.doc === pick.doc ? entry.handle : 0;
  const answered = await tellTab(tabId, { type: "highlight", handle, selector: entry.item.selector, reveal, key: entry.key });
  if (answered === null && (await reach(tabId))) await tellTab(tabId, { type: "highlight", handle: 0, selector: entry.item.selector, reveal, key: entry.key });
}

async function addLog(tabId, message) {
  const log = await read(logKey(tabId), null);
  if (!log) return {};
  const tab = await api.tabs.get(tabId).catch(() => null);
  const page = { url: tab?.url || log.url, title: tab?.title || "" };
  let raw;
  let logKeyOf;
  if (message.all === true) {
    const listed = log.entries.slice(-MAX_LOG_ENTRIES);
    const earlier = log.entries.slice(0, log.entries.length - listed.length).filter((one) => !one.mark).length;
    raw = { kind: "log", all: true, ...page, viewport: log.viewport, entries: listed, left: log.left + earlier };
    logKeyOf = "all";
  } else {
    const entry = log.entries.find((one) => one.id === message.id && !one.mark);
    if (!entry) return {};
    const url = entry.page || page.url;
    raw = { kind: "log", all: false, url, title: sameElementPage(url, page.url) ? page.title : "", viewport: log.viewport, entries: [entry], left: 0 };
    logKeyOf = `log-${entry.id}`;
  }
  const item = readLog(raw);
  if (!item) return {};
  return changeTray(tabId, (tray) => {
    const same = tray.items.find((one) => one.logKey === logKeyOf);
    if (same) {
      Object.assign(same, { item, rev: (same.rev || 0) + 1 });
      tray.current = same.key;
      return {};
    }
    if (tray.items.filter((one) => one.item.kind === "log").length >= MAX_LOGS) return { error: `The tray holds ${MAX_LOGS} console chips. Remove one to add another.` };
    const key = newKey();
    tray.items.push({ key, logKey: logKeyOf, item, handle: 0, doc: "", gone: false, rev: 1, parent: false, child: false });
    tray.current = key;
    return {};
  });
}

async function addImage(tabId, message) {
  const key = typeof message.key === "string" && /^[a-z0-9]{1,40}$/.test(message.key) ? message.key : "";
  const item = readImage(message.item);
  if (!key || !item) return { error: "The picture could not be added." };
  const added = await changeTray(tabId, (tray) => {
    if (tray.items.filter((one) => one.item.kind === "image").length >= MAX_IMAGES) return false;
    tray.items.push({ key, item, handle: 0, doc: "", gone: false, rev: 1, parent: false, child: false });
    tray.current = key;
    return true;
  });
  if (added) return { added: true };
  await dropPictures([key]);
  return { error: `The tray holds ${MAX_IMAGES} drawings. Remove one to add another.` };
}

async function prepare(tabId) {
  if (!(await reach(tabId))) return { error: await refuse(tabId) };
  if ((await read(pickKey(tabId), { on: false })).on) await setPicking(tabId, false);
  await tellTab(tabId, { type: "highlight", handle: 0, selector: "" });
  const page = await tellTab(tabId, { type: "draw-prepare" });
  return page && typeof page === "object" ? { page } : { error: "The page did not answer. Try again once it has loaded." };
}

async function fromPanel(message) {
  const tabId = Number.isSafeInteger(message.tabId) && message.tabId >= 0 ? message.tabId : -1;
  if (tabId < 0) return null;
  const key = typeof message.key === "string" ? message.key.slice(0, 40) : "";
  switch (message.op) {
    case "toggle": {
      const state = await read(pickKey(tabId), { on: false });
      return { picking: await setPicking(tabId, !state.on) };
    }
    case "stop":
      await stop(tabId);
      return {};
    case "note":
      await changeTray(tabId, (tray) => { tray.note = typeof message.note === "string" ? message.note.slice(0, MAX_NOTE) : ""; });
      return {};
    case "select":
      await changeTray(tabId, (tray) => { if (entryOf(tray, key)) tray.current = key; });
      await highlight(tabId, key, true);
      return {};
    case "highlight":
      await highlight(tabId, key, false);
      return {};
    case "remove": {
      const gone = await changeTray(tabId, (tray) => {
        const out = tray.items.filter((one) => one.key === key);
        tray.items = tray.items.filter((one) => one.key !== key);
        if (tray.current === key) tray.current = tray.items.at(-1)?.key || "";
        tray.full = false;
        return out;
      });
      await dropPictures(pictureKeys(gone));
      await highlight(tabId, "", false);
      return {};
    }
    case "clear": {
      const gone = await changeTray(tabId, (tray) => {
        const out = tray.items;
        Object.assign(tray, emptyTray());
        return out;
      });
      await dropPictures(pictureKeys(gone));
      await highlight(tabId, "", false);
      return {};
    }
    case "adjust": {
      const tray = await read(trayKey(tabId), emptyTray());
      const pick = await read(pickKey(tabId), { doc: "" });
      const entry = entryOf(tray, tray.current);
      if (entry && isElement(entry.item) && entry.doc === pick.doc && entry.handle) await tellTab(tabId, { type: "adjust", handle: entry.handle, toward: message.toward === "parent" ? "parent" : "child" });
      return {};
    }
    case "sent": {
      const went = new Map((Array.isArray(message.items) ? message.items : []).filter((one) => one && typeof one.key === "string").map((one) => [one.key, one.rev]));
      const gone = await changeTray(tabId, (tray) => {
        const out = tray.items.filter((one) => went.get(one.key) === one.rev);
        tray.items = tray.items.filter((one) => went.get(one.key) !== one.rev);
        if (!entryOf(tray, tray.current)) tray.current = tray.items.at(-1)?.key || "";
        if (typeof message.note === "string" && tray.note === message.note) tray.note = "";
        tray.full = false;
        return out;
      });
      await dropPictures(pictureKeys(gone));
      await highlight(tabId, "", false);
      if ((await read(pickKey(tabId), { on: false })).on) await setPicking(tabId, false);
      if (await read(drawKey(tabId), null)) await setDraw(tabId, drawState(await tellTab(tabId, { type: "draw-end" })));
      return {};
    }
    case "draw":
      await setDrawing(tabId, message.on === true);
      return {};
    case "tools":
      await chooseTools(tabId, message.tool, message.color);
      return {};
    case "draw-step":
      await tellTab(tabId, { type: "draw-step", step: message.step === 1 ? 1 : -1 });
      return {};
    case "draw-clear":
      await tellTab(tabId, { type: "draw-clear" });
      return {};
    case "draw-prepare":
      return prepare(tabId);
    case "add-image":
      return addImage(tabId, message);
    case "add-log":
      return addLog(tabId, message);
    default:
      return null;
  }
}

async function panelClosed(windowId) {
  for (const tab of await api.tabs.query({ windowId }).catch(() => [])) await stop(tab.id);
}

if (PANEL_CLOSED) api.sidePanel.onClosed.addListener((info) => void panelClosed(info.windowId));

const panels = new Map();

api.runtime.onConnect.addListener((port) => {
  const windowId = Number(/^panel:(\d{1,15})$/.exec(port.name)?.[1] ?? -1);
  if (windowId < 0 || !fromOwnPage(port.sender)) return;
  panels.set(windowId, (panels.get(windowId) || 0) + 1);
  port.onDisconnect.addListener(() => {
    const left = (panels.get(windowId) || 1) - 1;
    if (left) panels.set(windowId, left);
    else {
      panels.delete(windowId);
      void panelClosed(windowId);
    }
  });
});

api.runtime.onMessage.addListener((message, sender, reply) => {
  if (!message || typeof message !== "object" || sender.id !== api.runtime.id) return false;
  if (fromOwnPage(sender)) {
    fromPanel(message).then(reply, () => reply(null));
    return true;
  }
  if (sender.tab) void fromContent(message, sender);
  return false;
});

async function followDraw(tabId) {
  const state = drawState(await tellTab(tabId, { type: "moved" }));
  const before = await read(drawKey(tabId), null);
  if (before || state.on || state.count) await setDraw(tabId, state);
}

// The read queues behind the load's reset, else it could see the old page
// picking and leave the new one without the picker.
api.tabs.onUpdated.addListener((tabId, change) => {
  if (typeof change.url === "string") void serial(() => logMoved(tabId, change.url));
  if (typeof change.url === "string" || change.status === "complete") void followDraw(tabId);
  if (change.status === "loading") {
    void setPick(tabId, { on: false, doc: "" });
    return;
  }
  if (change.status !== "complete") return;
  void (async () => {
    const pick = await serial(() => read(pickKey(tabId), { on: false, resume: false }));
    if (!pick.resume || pick.on) return;
    if (!(await reach(tabId))) {
      await setPick(tabId, { resume: false });
      return;
    }
    await setPicking(tabId, true);
  })();
});

api.tabs.onRemoved.addListener((tabId) => {
  void serial(async () => {
    const tray = await read(trayKey(tabId), emptyTray());
    await kept.remove([trayKey(tabId), logKey(tabId)]);
    await api.storage.session.remove([pickKey(tabId), drawKey(tabId)]);
    await dropPictures(pictureKeys(tray.items));
  });
});

api.permissions.onAdded.addListener(() => void catchConsole());
api.permissions.onRemoved.addListener(() => void catchConsole());
void catchConsole();
