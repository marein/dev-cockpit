import { api, el, everyPage, PANEL_CLOSED, POPUP } from "./api.js";
import { contextCard, contextMarkdown, entryCount, label, logText, MAX_ASSISTANT_BYTES, MAX_CARD_BYTES, MAX_CODER_BYTES, MAX_ITEMS } from "./browser-context.js";
import { call, CockpitError, DOWNLOAD_PATH, fit, HANDSHAKE, onSetupSaved, readSetup, VERSION } from "./cockpit.js";
import { ConsoleList } from "./panel-console.js";
import { DrawBar, pictureName } from "./panel-draw.js";
import { SendMenu } from "./panel-menu.js";
import { readPicture } from "./pictures.js";

// Only a click here takes a picture or sends. Nothing of a page reaches this
// page.

const ui = {
  version: document.querySelector("[data-version]"),
  setup: document.querySelector("[data-setup]"),
  alert: document.querySelector("[data-alert]"),
  notice: document.querySelector("[data-notice]"),
  pick: document.querySelector("[data-pick]"),
  parent: document.querySelector("[data-parent]"),
  child: document.querySelector("[data-child]"),
  hint: document.querySelector("[data-hint]"),
  access: document.querySelector("[data-access]"),
  chips: document.querySelector("[data-chips]"),
  note: document.querySelector("[data-note]"),
  send: document.querySelector("[data-send]"),
  copy: document.querySelector("[data-copy]"),
  clear: document.querySelector("[data-clear]"),
  menu: document.querySelector("[data-menu]"),
  status: document.querySelector("[data-status]"),
};

const emptyTray = () => ({ note: "", items: [], current: "", full: false, problem: "" });
const HEADING = "Picked with the browser extension";

let tabId = -1;
let windowId = -1;
let web = false;
let tray = emptyTray();
let pick = { on: false, doc: "" };
let setup = null;
let every = true;
let checked = { text: "", link: null };
let notice = { text: "", link: null };
let blocked = false;
let checkedAt = 0;
let checks = 0;
const sending = new Set();
const statuses = new Map();
let shortcut = "";
let noteTimer = 0;
let noteSent = "";

// A popup ends on the first click into the page, so it hands the note on at
// once, the side panel waits for a pause in typing.
const NOTE_WAIT = POPUP ? 0 : 300;

function ask(op, extra = {}) {
  const message = { op, tabId, ...extra };
  if (message.tabId < 0) return Promise.resolve(null);
  return api.runtime.sendMessage(message).catch(() => null);
}

function sentence(text) {
  return /[.!?…]["'”’)\]]*$/u.test(text) ? text : `${text}.`;
}

const SHORT_NAME = 40;
const graphemes = new Intl.Segmenter(undefined, { granularity: "grapheme" });

function shortName(name) {
  const kept = [];
  for (const { segment } of graphemes.segment(name)) {
    if (kept.length === SHORT_NAME) return `${kept.slice(0, -1).join("").trimEnd()}…`;
    kept.push(segment);
  }
  return name;
}

function say(text, { error = false, link = null, tab = tabId } = {}) {
  if (text) statuses.set(tab, { text, error, link });
  else statuses.delete(tab);
  if (tab === tabId) showStatus();
}

function showStatus() {
  const shown = statuses.get(tabId);
  ui.status.classList.toggle("error", Boolean(shown?.error));
  ui.status.replaceChildren(...(shown ? [shown.text, ...(shown.link ? [" ", shown.link] : [])] : []));
}

const panel = {
  ask,
  say,
  changed: () => render(),
  get tabId() {
    return tabId;
  },
  get windowId() {
    return windowId;
  },
};
const draw = new DrawBar(panel);
const consoleList = new ConsoleList(panel);
const menu = new SendMenu(ui.menu, ui.send);

const isElement = (item) => item.kind !== "log" && item.kind !== "image";

function words(n, one, many) {
  return `${n} ${n === 1 ? one : many}`;
}

function counted() {
  const elements = tray.items.filter((one) => isElement(one.item)).length;
  const logs = tray.items.filter((one) => one.item.kind === "log");
  const pictures = tray.items.filter((one) => one.item.kind === "image").length;
  if (!logs.length && !pictures) return `${elements} of ${MAX_ITEMS}`;
  const entries = logs.reduce((sum, one) => sum + entryCount(one.item), 0);
  return [elements ? words(elements, "element", "elements") : "", logs.length ? words(entries, "console entry", "console entries") : "", pictures ? words(pictures, "drawing", "drawings") : ""].filter(Boolean).join(", ");
}

function chipTitle(one) {
  if (one.item.kind === "image") return `${one.item.title || one.item.url}\nClick to see the picture.`;
  if (one.item.kind === "log") return one.item.entries.filter((entry) => !entry.mark).slice(0, 6).map((entry) => logText(entry).split("\n")[0].slice(0, 160)).join("\n");
  return one.gone ? `${one.item.selector}\nNot on this page any more, the pick stays.` : one.item.selector;
}

function alerts() {
  const lines = [];
  if (tray.problem) lines.push([tray.problem]);
  if (!setup) lines.push(["Set up the cockpit's address and a token first, under Setup."]);
  if (checked.text) lines.push(checked.link ? [checked.text, " ", checked.link] : [checked.text]);
  return lines;
}

function current() {
  return tray.items.find((one) => one.key === tray.current) || null;
}

function render() {
  const lines = alerts();
  ui.alert.replaceChildren(...lines.map((line) => el("div", null, ...line)));
  ui.alert.hidden = !lines.length;
  ui.notice.replaceChildren(...(notice.text ? [notice.text, " ", notice.link] : []));
  ui.notice.hidden = !notice.text;
  ui.pick.setAttribute("aria-pressed", pick.on ? "true" : "false");
  ui.pick.textContent = pick.on ? "Picking" : "Pick";
  const entry = current();
  const live = entry && pick.doc && entry.doc === pick.doc;
  ui.parent.disabled = !live || !entry.parent;
  ui.child.disabled = !live || !entry.child;
  const count = tray.items.length;
  const key = shortcut ? `, or press ${shortcut}` : "";
  const start = POPUP ? "Click Pick" : `Click Pick or the extension's button in the toolbar${key}`;
  ui.hint.textContent = count
    ? `${counted()}${tray.full ? ", full. Remove one to pick another." : ""}`
    : pick.on ? "Click an element in the page to pick it, Escape ends picking." : draw.state.on ? "Draw on the page, Escape ends drawing." : `${start}, then click an element in the page.`;
  ui.chips.replaceChildren(...tray.items.map((one) => {
    const text = label(one.item);
    const kind = one.item.kind === "log" ? ` log ${one.item.all ? "all" : one.item.entries[0].kind === "request" ? "error" : one.item.entries[0].level}` : "";
    return el("span", { class: `chip${kind}${one.key === tray.current ? " active" : ""}${one.gone ? " gone" : ""}`, role: "listitem", dataset: { key: one.key } },
      el("button", { type: "button", class: "chip-main mono", title: chipTitle(one), "aria-pressed": one.key === tray.current ? "true" : "false", dataset: { chipShow: "" } }, el("span", { class: "chip-text" }, text)),
      el("button", { type: "button", class: "chip-remove", title: "Remove", "aria-label": `Remove ${text}`, dataset: { chipRemove: "" } }, "×"));
  }));
  draw.follow(tray.items);
  ui.chips.hidden = !count;
  ui.access.hidden = every;
  if (document.activeElement !== ui.note && ui.note.value !== tray.note) ui.note.value = tray.note;
  ui.send.disabled = sending.has(tabId) || !count || !setup || blocked;
  ui.send.textContent = sending.has(tabId) ? "Sending…" : "Send";
  ui.copy.disabled = !count;
  ui.clear.disabled = !count && !ui.note.value;
}

// Session storage hands this page a change before the answer to a read that
// began earlier, so a change of the shown tab during a load loads it again.
let loading = null;

api.storage.onChanged.addListener((changes, area) => {
  if (loading && area === "session") for (const key of Object.keys(changes)) loading.add(key);
});

async function load() {
  const changed = new Set();
  loading = changed;
  const [tab] = await api.tabs.query({ active: true, currentWindow: true });
  tabId = typeof tab?.id === "number" ? tab.id : -1;
  windowId = typeof tab?.windowId === "number" ? tab.windowId : -1;
  web = /^https?:/.test(tab?.url || "");
  const kept = tabId < 0 ? {} : { ...(await api.storage.local.get([`tray:${tabId}`, `log:${tabId}`])), ...(await api.storage.session.get([`pick:${tabId}`, `draw:${tabId}`])) };
  tray = { ...emptyTray(), ...(kept[`tray:${tabId}`] || {}) };
  pick = { on: false, doc: "", ...(kept[`pick:${tabId}`] || {}) };
  if (loading === changed) loading = null;
  ui.note.value = tray.note;
  noteSent = tray.note;
  menu.close();
  draw.closePreview();
  draw.setState(kept[`draw:${tabId}`]);
  consoleList.set(kept[`log:${tabId}`], every, web);
  showStatus();
  render();
  if ([...changed].some((key) => key.endsWith(`:${tabId}`))) void load();
}

function fitted(answer) {
  const fits = fit(answer);
  const line = fits.text ? { text: fits.text, link: cockpitLink(DOWNLOAD_PATH, "Download") } : { text: "", link: null };
  blocked = fits.blocks;
  checked = blocked ? line : { text: "", link: null };
  notice = blocked ? { text: "", link: null } : line;
}

function unchecked(text = "") {
  blocked = false;
  checked = { text, link: null };
  notice = { text: "", link: null };
}

async function check(force = false) {
  const round = ++checks;
  const next = await readSetup().catch(() => null);
  if (round !== checks) return;
  const same = next?.origin === setup?.origin && next?.token === setup?.token;
  setup = next;
  if (same && !force && Date.now() - checkedAt < 30000) {
    render();
    return;
  }
  checkedAt = Date.now();
  if (!same) unchecked();
  render();
  if (!setup) return;
  let answer = null;
  let failed = "";
  try {
    answer = await call(setup, "GET", HANDSHAKE);
  } catch (error) {
    failed = error instanceof CockpitError ? error.message : "The cockpit could not be asked.";
  }
  if (round !== checks) return;
  if (answer) fitted(answer);
  else unchecked(failed);
  render();
}

function flushNote() {
  clearTimeout(noteTimer);
  noteTimer = 0;
  if (ui.note.value === noteSent) return;
  noteSent = ui.note.value;
  void ask("note", { note: ui.note.value });
}

ui.note.addEventListener("input", () => {
  clearTimeout(noteTimer);
  noteTimer = setTimeout(flushNote, NOTE_WAIT);
  ui.clear.disabled = !tray.items.length && !ui.note.value;
});
ui.note.addEventListener("blur", flushNote);
ui.note.addEventListener("keydown", (event) => {
  if (event.key !== "Enter" || !(event.ctrlKey || event.metaKey) || event.altKey) return;
  event.preventDefault();
  if (!ui.send.disabled) void openMenu(true);
});

ui.pick.addEventListener("click", async () => {
  await ask("toggle");
});
ui.parent.addEventListener("click", () => void ask("adjust", { toward: "parent" }));
ui.child.addEventListener("click", () => void ask("adjust", { toward: "child" }));
ui.setup.addEventListener("click", () => void api.runtime.openOptionsPage());

ui.chips.addEventListener("click", (event) => {
  const chip = event.target instanceof Element ? event.target.closest("[data-key]") : null;
  if (!chip) return;
  if (event.target.closest("[data-chip-remove]")) {
    void ask("remove", { key: chip.dataset.key });
    return;
  }
  void ask("select", { key: chip.dataset.key });
  const entry = tray.items.find((one) => one.key === chip.dataset.key);
  if (entry?.item.kind === "image") void draw.show(entry);
  else if (entry?.item.kind === "log") consoleList.reveal(entry.logKey);
});
ui.chips.addEventListener("pointerover", (event) => onChipHover(event, true));
ui.chips.addEventListener("pointerout", (event) => onChipHover(event, false));

function onChipHover(event, over) {
  const chip = event.target instanceof Element ? event.target.closest("[data-key]") : null;
  if (!chip) return;
  const into = event.relatedTarget instanceof Element ? event.relatedTarget.closest("[data-key]") : null;
  if (into === chip) return;
  void ask("highlight", { key: over ? chip.dataset.key : pick.on ? tray.current : "" });
}

ui.copy.addEventListener("click", async () => {
  if (!tray.items.length) return;
  try {
    await navigator.clipboard.writeText(contextMarkdown(ui.note.value, tray.items.map((one) => one.item), HEADING));
    say("Copied as Markdown.");
  } catch {
    say("The clipboard is not available.", { error: true });
  }
});

ui.clear.addEventListener("click", () => {
  ui.note.value = "";
  noteSent = "";
  clearTimeout(noteTimer);
  say("");
  draw.closePreview();
  void ask("clear");
});

async function openMenu(byKeyboard) {
  if (menu.open) {
    menu.close();
    return;
  }
  flushNote();
  const shown = { note: ui.note.value, items: tray.items.map(({ key, rev, item }) => ({ key, rev, item })) };
  if (!shown.items.length || !setup) return;
  menu.show(byKeyboard);
  let targets;
  try {
    fitted(await call(setup, "GET", HANDSHAKE));
    checkedAt = Date.now();
    if (!blocked) targets = await call(setup, "GET", "/api/browser-extension/targets");
  } catch (error) {
    menu.close();
    say(error instanceof CockpitError ? error.message : "The cockpit could not be asked.", { error: true });
    return;
  }
  render();
  if (blocked) menu.close();
  if (!menu.open) return;
  const to = (target) => () => void send(target, shown);
  const listed = (list) => (Array.isArray(list) ? list.filter((one) => one && typeof one.id === "string") : []);
  const newWith = listed(targets.assistantCoders);
  menu.fill([
    {
      kind: "new",
      rows: newWith.map((one) => {
        const coderName = String(one.name || one.id);
        const choose = to({ kind: "new", id: "", name: "the new assistant", coder: one.id });
        return newWith.length === 1 ? { name: "New assistant", hint: coderName, choose } : { name: `New with ${coderName}`, choose };
      }),
    },
    {
      kind: "assistant",
      head: "Assistants",
      rows: listed(targets.assistants).map((one) => ({ name: String(one.name || "Assistant"), choose: to({ kind: "assistant", id: one.id, name: String(one.name || "the assistant") }) })),
    },
    {
      kind: "coder",
      head: "Running coders",
      empty: "No coder runs.",
      rows: listed(targets.coders).map((one) => ({ name: String(one.name || one.id), hint: String(one.project || ""), choose: to({ kind: "coder", id: one.id, name: String(one.name || one.id) }) })),
    },
  ]);
}

ui.send.addEventListener("click", (event) => void openMenu(event.detail === 0));

document.addEventListener("keydown", (event) => {
  if (event.key !== "Escape" || event.defaultPrevented || menu.open || !(pick.on || draw.state.on)) return;
  event.preventDefault();
  void ask("stop");
});

document.addEventListener("pointerdown", (event) => {
  if (!menu.open || !(event.target instanceof Node)) return;
  if (!ui.menu.contains(event.target) && !ui.send.contains(event.target)) menu.close();
});

function byteSize(text) {
  return new Blob([text]).size;
}

const MAX_FILE = 1024;

function cockpitLink(path, text) {
  let url;
  try {
    url = new URL(path, setup.origin);
  } catch {
    return null;
  }
  if (url.origin !== setup.origin || typeof path !== "string" || !path.startsWith("/")) return null;
  return el("a", { href: url.href, target: "_blank", rel: "noopener", dataset: { open: "" } }, text);
}

function withFiles(shown, files) {
  return shown.items.map((one) => (one.item.kind === "image" ? { ...one.item, file: files.get(one.key) || "" } : one.item));
}

async function upload(shown, to, id) {
  const files = new Map();
  for (const one of shown.items) {
    if (one.item.kind !== "image") continue;
    const blob = await readPicture(one.key);
    if (!blob) continue;
    const form = new FormData();
    form.append("to", to);
    form.append("id", id);
    form.append("image", blob, pictureName(one.item.url, one.item.at));
    const answer = await call(setup, "POST", "/api/browser-extension/pictures", form);
    if (typeof answer.file === "string" && answer.file) files.set(one.key, answer.file.slice(0, MAX_FILE));
  }
  return files;
}

async function send(target, shown) {
  const from = tabId;
  if (sending.has(from) || !setup) return;
  const coder = target.kind === "coder";
  const widest = new Map(shown.items.filter((one) => one.item.kind === "image").map((one) => [one.key, "x".repeat(MAX_FILE)]));
  const longest = withFiles(shown, widest);
  const cardBytes = coder ? 0 : byteSize(JSON.stringify(contextCard(shown.note, longest)));
  const [bytes, max] = cardBytes > MAX_CARD_BYTES ? [cardBytes, MAX_CARD_BYTES] : [byteSize(contextMarkdown(shown.note, longest, HEADING)), coder ? MAX_CODER_BYTES : MAX_ASSISTANT_BYTES];
  if (bytes > max) {
    say(`This is too long for ${coder ? "a coder" : "an assistant"}, ${Math.ceil(bytes / 1024)} KB of ${max >> 10} KB. Remove a chip or shorten the note.`, { error: true, tab: from });
    return;
  }
  sending.add(from);
  say("", { tab: from });
  render();
  try {
    let { id, name } = target;
    if (target.kind === "new") {
      const made = await call(setup, "POST", "/api/browser-extension/assistants", { coder: target.coder });
      id = typeof made.id === "string" ? made.id : "";
      if (!id) throw new CockpitError("answer", "The new assistant could not be started.");
    }
    const files = await upload(shown, coder ? "coder" : "assistant", id);
    const items = withFiles(shown, files);
    const message = contextMarkdown(shown.note, items, HEADING);
    const form = new URLSearchParams({ to: coder ? "coder" : "assistant", id, message });
    if (!coder) {
      form.set("card", JSON.stringify(contextCard(shown.note, items)));
      for (const file of files.values()) form.append("picture", file);
    }
    const answer = await call(setup, "POST", "/api/browser-extension/send", form);
    if (!coder && typeof answer.name === "string" && answer.name) name = answer.name;
    await ask("sent", { tabId: from, items: shown.items.map(({ key, rev }) => ({ key, rev })), note: shown.note });
    if (from === tabId && ui.note.value === shown.note) {
      ui.note.value = "";
      noteSent = "";
    }
    say(`Sent to ${sentence(shortName(name))}`, { link: cockpitLink(answer.url, coder ? "Open the coder" : "Open the assistant"), tab: from });
  } catch (error) {
    say(error instanceof CockpitError ? error.message : "The cockpit did not take the message.", { error: true, tab: from });
  } finally {
    sending.delete(from);
    render();
  }
}

api.storage.onChanged.addListener((changes, area) => {
  if ((area !== "local" && area !== "session") || tabId < 0) return;
  const local = area === "local";
  const trayChange = local && changes[`tray:${tabId}`];
  const pickChange = !local && changes[`pick:${tabId}`];
  const drawChange = !local && changes[`draw:${tabId}`];
  const logChange = local && changes[`log:${tabId}`];
  if (trayChange) tray = { ...emptyTray(), ...(trayChange.newValue || {}) };
  if (pickChange) pick = { on: false, doc: "", ...(pickChange.newValue || {}) };
  if (drawChange) draw.setState(drawChange.newValue);
  if (logChange) consoleList.set(logChange.newValue, every, web);
  if (trayChange || pickChange) render();
});

api.tabs.onActivated.addListener((info) => {
  if (info.windowId !== windowId) return;
  flushNote();
  void load();
});

api.tabs.onRemoved.addListener((closed) => {
  statuses.delete(closed);
  consoleList.forget(closed);
  if (closed === tabId) void load();
});

async function readAccess() {
  every = await everyPage();
  const kept = tabId < 0 ? {} : await api.storage.local.get(`log:${tabId}`);
  consoleList.set(kept[`log:${tabId}`], every, web);
  render();
}

// The worker ends picking and drawing in the window once this port drops with
// the panel. An idle worker drops it too and wakes with the next connect.
function holdPort(current) {
  try {
    api.runtime.connect({ name: `panel:${current.id}` }).onDisconnect.addListener(() => holdPort(current));
  } catch {}
}

api.permissions.onAdded.addListener(() => void readAccess());
api.permissions.onRemoved.addListener(() => void readAccess());

window.addEventListener("focus", () => void check());
onSetupSaved(() => void check(true));

document.documentElement.classList.toggle("popup", POPUP);
if (POPUP) ui.pick.title = "Pick elements in this tab, Escape or this button stops";
ui.version.textContent = VERSION;
api.commands?.getAll?.().then((commands) => {
  shortcut = commands.find((one) => one.name === "_execute_action")?.shortcut || "";
  render();
}).catch(() => {});
if (!POPUP && !PANEL_CLOSED) api.windows.getCurrent().then(holdPort, () => {});
void load();
void check();
void readAccess();
