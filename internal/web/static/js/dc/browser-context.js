export const MAX_ITEMS = 8;
export const MAX_LOGS = 8;
export const MAX_NOTE = 4000;
export const MAX_LOG_ENTRIES = 60;
export const MAX_IMAGES = 4;
const MAX_MARKS = 50;
export const MAX_ASSISTANT_BYTES = 32 << 10;
// A coder's prompt travels as one tmux command, which tmux refuses from about 16 KB on.
export const MAX_CODER_BYTES = 15 << 10;

const CONTROL = /[\u0000-\u0008\u000b-\u001f\u007f-\u009f‎‏‪-‮⁦-⁩]/g;

export function plain(value, max) {
  if (typeof value !== "string") return "";
  const clean = value.replace(CONTROL, "");
  return clean.length > max ? `${clean.slice(0, max - 1)}…` : clean;
}

function line(value, max) {
  return plain(typeof value === "string" ? value.replace(/[\r\n\t]+/g, " ") : value, max);
}

function count(value, min = -1e6, max = 1e6) {
  const n = Number(value);
  return Number.isFinite(n) ? Math.min(max, Math.max(min, Math.round(n * 100) / 100)) : 0;
}

function pairs(value, limit, keyMax, valueMax) {
  if (!Array.isArray(value)) return [];
  const out = [];
  for (const pair of value.slice(0, limit)) {
    if (!Array.isArray(pair)) continue;
    const key = line(pair[0], keyMax);
    if (key) out.push([key, line(pair[1], valueMax)]);
  }
  return out;
}

export function readElement(raw, url) {
  if (!raw || typeof raw !== "object") return null;
  const tag = line(raw.tag, 40);
  if (!tag) return null;
  const viewport = raw.viewport && typeof raw.viewport === "object" ? raw.viewport : {};
  const box = raw.box && typeof raw.box === "object" ? raw.box : {};
  const component = raw.component && typeof raw.component === "object" && Array.isArray(raw.component.names)
    ? { framework: line(raw.component.framework, 20), names: raw.component.names.slice(0, 3).map((name) => line(name, 80)).filter(Boolean) }
    : null;
  return {
    url: line(url, 2048),
    title: line(raw.title, 200),
    viewport: { w: count(viewport.w, 0, 1e5), h: count(viewport.h, 0, 1e5), dpr: count(viewport.dpr, 0, 16) },
    selector: line(raw.selector, 600),
    tag,
    id: line(raw.id, 100),
    classes: Array.isArray(raw.classes) ? raw.classes.slice(0, 20).map((name) => line(name, 60)).filter(Boolean) : [],
    role: line(raw.role, 40),
    name: line(raw.name, 200),
    text: line(raw.text, 300),
    attrs: pairs(raw.attrs, 12, 40, 120),
    html: plain(raw.html, 3000),
    styles: pairs(raw.styles, 40, 40, 160),
    box: { x: count(box.x), y: count(box.y), w: count(box.w, 0), h: count(box.h, 0), sx: count(box.sx), sy: count(box.sy) },
    component: component && component.names.length ? component : null,
  };
}

const MARK_KINDS = new Set(["pen", "arrow", "rect", "text"]);
const MARK_COLORS = new Set(["red", "orange", "green", "blue", "purple"]);

export function readMarks(value) {
  if (!Array.isArray(value)) return [];
  const out = [];
  for (const raw of value.slice(0, MAX_MARKS)) {
    if (!raw || typeof raw !== "object" || !MARK_KINDS.has(raw.kind)) continue;
    const target = raw.target && typeof raw.target === "object" ? { label: line(raw.target.label, 160), selector: line(raw.target.selector, 600) } : null;
    out.push({
      kind: raw.kind,
      color: MARK_COLORS.has(raw.color) ? raw.color : "red",
      text: raw.kind === "text" ? line(raw.text, 200) : "",
      target: target?.label ? target : null,
      seen: raw.seen !== false,
    });
  }
  return out;
}

export function readImage(raw) {
  if (!raw || typeof raw !== "object" || raw.kind !== "image") return null;
  const viewport = raw.viewport && typeof raw.viewport === "object" ? raw.viewport : {};
  return {
    kind: "image",
    url: line(raw.url, 2048),
    title: line(raw.title, 200),
    viewport: { w: count(viewport.w, 0, 1e5), h: count(viewport.h, 0, 1e5) },
    width: count(raw.width, 0, 1e5),
    height: count(raw.height, 0, 1e5),
    ratio: count(raw.ratio, 0, 4),
    pixels: raw.pixels === true,
    marks: readMarks(raw.marks),
    at: count(raw.at, 0, 1e14),
    file: "",
  };
}

const LOG_LEVELS = new Set(["error", "warn"]);
const LOG_KINDS = new Set(["console", "exception", "rejection", "request"]);

function stackText(value, lines, max) {
  return plain(typeof value === "string" ? value.split("\n").slice(0, lines).join("\n") : "", max);
}

// Every value is the page's own text, so each is bounded and of a known kind.
export function readLogEntry(raw, { message = 2000, stack = 2000, lines = 15 } = {}) {
  if (!raw || typeof raw !== "object") return null;
  const time = count(raw.time, 0, 1e14);
  if (raw.mark === true) return { mark: true, url: line(raw.url, 2048), time };
  if (!LOG_LEVELS.has(raw.level) || !LOG_KINDS.has(raw.kind)) return null;
  const entry = { level: raw.level, kind: raw.kind, message: plain(raw.message, message), stack: stackText(raw.stack, lines, stack), time };
  if (raw.kind === "request") {
    entry.method = typeof raw.method === "string" && /^[A-Z]{1,12}$/.test(raw.method) ? raw.method : "GET";
    entry.url = line(raw.url, 2048);
    entry.status = Number.isInteger(raw.status) && raw.status >= 0 && raw.status < 1000 ? raw.status : 0;
    entry.message = line(raw.message, 200);
  }
  return entry;
}

export function readLog(raw) {
  if (!raw || typeof raw !== "object" || raw.kind !== "log") return null;
  const all = raw.all === true;
  const bounds = all ? { message: 500, stack: 600, lines: 4 } : {};
  const entries = (Array.isArray(raw.entries) ? raw.entries.slice(-(all ? MAX_LOG_ENTRIES : 1)) : []).map((one) => readLogEntry(one, bounds)).filter(Boolean);
  if (!entries.length || (!all && entries[0].mark)) return null;
  const viewport = raw.viewport && typeof raw.viewport === "object" ? raw.viewport : {};
  return {
    kind: "log",
    all,
    url: line(raw.url, 2048),
    title: line(raw.title, 200),
    viewport: { w: count(viewport.w, 0, 1e5), h: count(viewport.h, 0, 1e5) },
    entries,
    left: count(raw.left, 0, 1e9),
  };
}

function requestLine(entry) {
  const answer = entry.status ? String(entry.status) : "failed";
  const why = entry.message ? ` ${entry.message}` : "";
  return `${entry.method} ${entry.url} ${answer}${why}`;
}

export function entryCount(item) {
  return item.all ? item.entries.filter((one) => !one.mark).length + item.left : 1;
}

export function logText(entry) {
  return entry.kind === "request" ? requestLine(entry) : entry.message;
}

const LOG_TITLES = { console: { error: "Console error", warn: "Console warning" }, exception: "Uncaught error", rejection: "Unhandled rejection", request: "Failed request" };

export function logTitle(entry) {
  const title = LOG_TITLES[entry.kind];
  return typeof title === "string" ? title : title[entry.level];
}

export function clock(time) {
  const at = new Date(time);
  return [at.getHours(), at.getMinutes(), at.getSeconds()].map((n) => String(n).padStart(2, "0")).join(":");
}

export function label(item) {
  if (item.kind === "image") {
    const n = item.marks.length;
    return n ? `Drawing, ${n === 1 ? "one mark" : `${n} marks`}` : "Drawing";
  }
  if (item.kind === "log") {
    if (item.all) return `Console, ${entryCount(item)} entries`;
    const entry = item.entries[0];
    const text = entry.kind === "request" ? `${entry.method} ${entry.url.replace(/^[a-z]+:\/\/[^/]+/i, "") || "/"} ${entry.status || "failed"}` : entry.message.split("\n")[0];
    return `${entry.kind === "request" ? "request" : entry.level} "${text.length > 40 ? `${text.slice(0, 39)}…` : text}"`;
  }
  let head = item.tag;
  if (item.id) head += `#${item.id}`;
  else if (item.classes.length) head += `.${item.classes[0]}`;
  const name = item.name || item.text;
  const short = name.length > 28 ? `${name.slice(0, 27)}…` : name;
  return short ? `${head} "${short}"` : head;
}

export function sameElementPage(a, b) {
  const strip = (url) => url.replace(/#.*$/, "");
  return strip(a) === strip(b);
}

// A jump to a spot stays on the page shown, a hash router's view (#/cart,
// #!/cart) is a page of its own.
export function samePage(a, b) {
  const route = (url) => /^[^#]*#[/!]/.test(url);
  return a === b || (sameElementPage(a, b) && !route(a) && !route(b));
}

function code(value) {
  const flat = String(value).replace(/[\r\n]+/g, " ");
  const runs = flat.match(/`+/g) || [];
  const fence = "`".repeat(Math.max(0, ...runs.map((run) => run.length)) + 1);
  const pad = flat.startsWith("`") || flat.endsWith("`") ? " " : "";
  return `${fence}${pad}${flat}${pad}${fence}`;
}

function block(lang, value) {
  const runs = value.match(/`{3,}/g) || [];
  const fence = "`".repeat(Math.max(2, ...runs.map((run) => run.length)) + 1);
  return `${fence}${lang}\n${value}\n${fence}`;
}

function size(w, h) {
  return `${Math.round(w)}×${Math.round(h)}`;
}

function pageLine(item) {
  const title = item.title ? ` (${code(item.title)})` : "";
  return `${code(item.url)}${title}, viewport ${size(item.viewport.w, item.viewport.h)}`;
}

function elementMarkdown(item, index, ownPage) {
  const name = item.tag + (item.id ? `#${item.id}` : "") + item.classes.map((one) => `.${one}`).join("");
  const lines = [`### ${index}. ${code(name)}`, ""];
  if (ownPage) lines.push(`- Page: ${pageLine(item)}`);
  if (item.selector) lines.push(`- Selector: ${code(item.selector)}`);
  if (item.role || item.name) lines.push(`- Role: ${item.role ? code(item.role) : "none"}${item.name ? `, name ${code(item.name)}` : ""}`);
  if (item.component) lines.push(`- Component: ${item.component.names.map(code).join(" in ")} (${item.component.framework})`);
  if (item.text) lines.push(`- Text: ${code(item.text)}`);
  if (item.attrs.length) lines.push(`- Attributes: ${item.attrs.map(([key, value]) => code(`${key}="${value}"`)).join(", ")}`);
  const { x, y, w, h, sx, sy } = item.box;
  lines.push(`- Box: ${size(w, h)} at ${Math.round(x)}, ${Math.round(y)} in the viewport${sx || sy ? `, page scrolled to ${Math.round(sx)}, ${Math.round(sy)}` : ""}`);
  if (item.styles.length) {
    lines.push("", "Computed styles:", "", block("css", item.styles.map(([key, value]) => `${key}: ${value};`).join("\n")));
  }
  if (item.html) lines.push("", "HTML:", "", block("html", item.html));
  return lines.join("\n");
}

function logEntryMarkdown(item, index, ownPage) {
  const entry = item.entries[0];
  const lines = [`### ${index}. ${logTitle(entry)}`, ""];
  if (ownPage) lines.push(`- Page: ${pageLine(item)}`);
  if (entry.kind === "request") lines.push(`- Request: ${code(`${entry.method} ${entry.url}`)}, ${entry.status ? `status ${entry.status}${entry.message ? ` ${entry.message}` : ""}` : `failed, status unknown${entry.message ? ` (${entry.message.replace(/^\((.*)\)$/, "$1")})` : ""}`}`);
  lines.push(`- At ${clock(entry.time)}`);
  if (entry.kind !== "request") lines.push("", block("text", entry.message));
  if (entry.stack) lines.push("", "Stack:", "", block("text", entry.stack));
  return lines.join("\n");
}

const LOG_BLOCK_MAX = 8 << 10;
const utf8 = new TextEncoder();

function logRows(item) {
  const rows = item.entries.map((entry) => {
    if (entry.mark) return `${clock(entry.time)} -- navigated to ${entry.url}`;
    const head = `${clock(entry.time)} ${entry.kind === "request" ? "request" : entry.level} ${logText(entry).replace(/\s*\n\s*/g, " ")}`;
    return entry.stack ? `${head}\n${entry.stack.split("\n").map((one) => `    ${one.trim()}`).join("\n")}` : head;
  });
  let size = 0;
  let from = rows.length;
  while (from > 0) {
    const next = utf8.encode(rows[from - 1]).length + 1;
    if (size + next > LOG_BLOCK_MAX) break;
    from -= 1;
    size += next;
  }
  return { rows: rows.slice(from), entries: item.entries.slice(from), left: item.left + item.entries.slice(0, from).filter((one) => !one.mark).length };
}

function logAllMarkdown(item, index, ownPage) {
  const total = entryCount(item);
  const lines = [`### ${index}. The page's console, ${total} ${total === 1 ? "entry" : "entries"}`, ""];
  if (ownPage) lines.push(`- Page: ${pageLine(item)}`);
  const { rows, left } = logRows(item);
  if (left) lines.push(`- ${left} more ${left === 1 ? "entry is" : "entries are"} not listed.`);
  lines.push("", block("text", rows.join("\n")));
  return lines.join("\n");
}

const MARK_WORDS = { pen: "Pen", arrow: "Arrow", rect: "Box", text: "Text" };
const MARK_PLACES = { pen: "over", arrow: "pointing at", rect: "around", text: "at" };

function imageMarkdown(item, index, ownPage) {
  const lines = [`### ${index}. A drawing on the page`, ""];
  if (ownPage) lines.push(`- Page: ${pageLine(item)}`);
  const shown = `${size(item.width, item.height)}${item.ratio > 1 ? ` at ${item.ratio}x` : ""}`;
  const what = item.pixels ? "what the page showed with the drawing over it" : "the drawing over the page's background, without the page itself";
  lines.push(item.file ? `- Picture: ${code(item.file)}, ${what}, ${shown}` : `- Picture: not part of this text, ${what}, ${shown}`);
  if (!item.marks.length) {
    lines.push("- No marks.");
    return lines.join("\n");
  }
  lines.push("- Marks, each with the element under it:");
  item.marks.forEach((mark, i) => {
    const head = mark.kind === "text" ? `Text ${code(mark.text)}` : MARK_WORDS[mark.kind];
    const where = mark.target ? `${MARK_PLACES[mark.kind]} ${code(mark.target.label)}${mark.target.selector ? `, selector ${code(mark.target.selector)}` : ""}` : "over no element";
    lines.push(`  ${i + 1}. ${head}, ${mark.color}, ${where}${mark.seen ? "" : ", outside the picture"}`);
  });
  return lines.join("\n");
}

function counted(n, one, many) {
  return n === 1 ? one : `${n} ${many}`;
}

export function contextMarkdown(note, items, heading = "Picked in the Browser") {
  const parts = [];
  const text = note.trim();
  if (text) parts.push(text);
  if (!items.length) return parts.join("\n\n");
  const first = items[0];
  const onePage = items.every((item) => sameElementPage(item.url, first.url));
  const elements = items.filter((item) => item.kind !== "log" && item.kind !== "image");
  const logs = items.filter((item) => item.kind === "log");
  const images = items.filter((item) => item.kind === "image");
  const head = [`## ${heading}`, ""];
  const entries = logs.reduce((sum, item) => sum + entryCount(item), 0);
  const what = [
    elements.length ? counted(elements.length, "One element", "elements") : "",
    logs.length ? counted(entries, elements.length ? "one console entry" : "One console entry", "console entries") : "",
    images.length ? counted(images.length, elements.length || logs.length ? "a drawing" : "A drawing", "drawings") : "",
  ].filter(Boolean).join(" and ");
  head.push(onePage ? `${what} of ${pageLine(first)}.` : `${what} of ${new Set(items.map((item) => item.url)).size} pages.`);
  if (images.length) {
    const verbs = [elements.length ? "picked" : "", logs.length ? "logged" : "", "drawn"].filter(Boolean);
    head.push(`Everything below was read from the page when it was ${verbs.length > 1 ? `${verbs.slice(0, -1).join(", ")} or ${verbs.at(-1)}` : verbs[0]}.`);
  } else {
    head.push(logs.length ? "Everything below was read from the page when it was picked or logged." : "Everything below was read from the page when it was picked.");
  }
  if (elements.some((item) => item.selector.includes(" >>> ")) || images.some((item) => item.marks.some((mark) => mark.target?.selector.includes(" >>> ")))) head.push("A ` >>> ` in a selector steps into a shadow root or a frame.");
  parts.push(head.join("\n"));
  items.forEach((item, i) => {
    if (item.kind === "image") parts.push(imageMarkdown(item, i + 1, !onePage));
    else if (item.kind !== "log") parts.push(elementMarkdown(item, i + 1, !onePage));
    else if (item.all) parts.push(logAllMarkdown(item, i + 1, !onePage));
    else parts.push(logEntryMarkdown(item, i + 1, !onePage));
  });
  return parts.join("\n\n");
}

// The scheme and a lone trailing slash say little and take a phone's width.
export function shortAddress(url) {
  return url.replace(/^https?:\/\//, "").replace(/^([^/?#]+)\/$/, "$1");
}

// The card holds no more than the message does: the same items, as plain
// texts the server checks.
export const MAX_CARD_BYTES = 64 << 10;

function pageText(item) {
  return [item.title, shortAddress(item.url), size(item.viewport.w, item.viewport.h)].filter(Boolean).join(" · ");
}

function styleOf(item, name) {
  return item.styles.find(([key]) => key === name)?.[1] || "";
}

function elementSection(item, index, ownPage) {
  const rows = [];
  const row = (term, value, code = false) => value && rows.push(code ? { term, value, code } : { term, value });
  if (ownPage) row("Page", pageText(item));
  row("Selector", item.selector, true);
  if (item.role || item.name) row("Role", `${item.role || "none"}${item.name ? `, name "${item.name}"` : ""}`);
  if (item.component) row("Component", `${item.component.names.join(" in ")} (${item.component.framework})`);
  row("Text", item.text);
  row("Attributes", item.attrs.map(([key, value]) => `${key}="${value}"`).join(" "), true);
  const { x, y, w, h, sx, sy } = item.box;
  row("Box", `${size(w, h)} at ${Math.round(x)}, ${Math.round(y)}${sx || sy ? `, scrolled ${Math.round(sx)}, ${Math.round(sy)}` : ""}`);
  const font = [styleOf(item, "font-family"), styleOf(item, "font-size")].filter(Boolean).join(" ");
  const weight = styleOf(item, "font-weight");
  const height = styleOf(item, "line-height");
  row("Font", [font, weight, height ? `line ${height}` : ""].filter(Boolean).join(", "));
  const color = styleOf(item, "color");
  const background = styleOf(item, "background-color");
  row("Color", color && background ? `${color} on ${background}` : color || background);
  const blocks = [];
  if (item.styles.length) blocks.push({ label: "Styles", text: item.styles.map(([key, value]) => `${key}: ${value};`).join("\n") });
  if (item.html) blocks.push({ label: "HTML", text: item.html });
  return { kind: "element", title: `${index}. ${label(item)}`, rows, blocks };
}

function logLine(entry) {
  if (entry.mark) return { kind: "nav", text: `navigated to ${entry.url}`, meta: clock(entry.time) };
  return { kind: entry.level, text: logText(entry), meta: clock(entry.time) };
}

function logSection(item, index, ownPage) {
  const rows = ownPage ? [{ term: "Page", value: pageText(item) }] : [];
  if (!item.all) {
    const entry = item.entries[0];
    return { kind: "log", title: `${index}. ${logTitle(entry)}`, rows, lines: [logLine(entry)], blocks: entry.stack ? [{ label: "Stack", text: entry.stack }] : [] };
  }
  const { entries, left } = logRows(item);
  if (left) rows.push({ term: "Not listed", value: `${left} more ${left === 1 ? "entry" : "entries"}` });
  const total = entryCount(item);
  return { kind: "log", title: `${index}. The page's console, ${total} ${total === 1 ? "entry" : "entries"}`, rows, lines: entries.map(logLine) };
}

function imageSection(item, index, ownPage) {
  const rows = ownPage ? [{ term: "Page", value: pageText(item) }] : [];
  const shown = `${size(item.width, item.height)}${item.ratio > 1 ? ` at ${item.ratio}x` : ""}`;
  rows.push({ term: "Picture", value: `${shown}, ${item.pixels ? "the page with the drawing over it" : "the drawing alone, without the page"}${item.file ? "" : ", not sent"}` });
  const lines = item.marks.map((mark) => ({
    kind: "mark",
    text: `${mark.kind === "text" ? `Text "${mark.text}"` : MARK_WORDS[mark.kind]} ${mark.target ? `${MARK_PLACES[mark.kind]} ${mark.target.label}` : "over no element"}`,
    meta: `${mark.color}${mark.seen ? "" : ", outside the picture"}`,
  }));
  return { kind: "image", title: `${index}. ${label(item)}`, rows, lines, picture: item.file || "" };
}

function words(n, one, many) {
  return `${n} ${n === 1 ? one : many}`;
}

export function contextCard(note, items) {
  const first = items[0];
  const onePage = items.every((item) => sameElementPage(item.url, first.url));
  const elements = items.filter((item) => item.kind !== "log" && item.kind !== "image");
  const logs = items.filter((item) => item.kind === "log");
  const images = items.filter((item) => item.kind === "image");
  const chips = [];
  if (elements.length) chips.push({ kind: "element", text: elements.length === 1 ? label(elements[0]) : words(elements.length, "element", "elements") });
  if (logs.length) {
    const known = logs.flatMap((item) => item.entries.filter((entry) => !entry.mark));
    const total = logs.reduce((sum, item) => sum + entryCount(item), 0);
    const errors = known.filter((entry) => entry.level === "error").length;
    const text = errors === total ? words(total, "error", "errors") : errors === 0 && known.length === total ? words(total, "warning", "warnings") : words(total, "console entry", "console entries");
    chips.push({ kind: errors ? "error" : "warn", text });
  }
  if (images.length) chips.push({ kind: "image", text: images.length === 1 ? label(images[0]) : words(images.length, "drawing", "drawings") });
  const sections = items.map((item, i) => (item.kind === "image" ? imageSection(item, i + 1, !onePage) : item.kind === "log" ? logSection(item, i + 1, !onePage) : elementSection(item, i + 1, !onePage)));
  const head = onePage ? { title: first.title, address: shortAddress(first.url), viewport: size(first.viewport.w, first.viewport.h) } : { title: first.title, address: shortAddress(first.url), pages: new Set(items.map((item) => item.url)).size };
  return { note: note.trim(), ...head, chips, sections };
}
