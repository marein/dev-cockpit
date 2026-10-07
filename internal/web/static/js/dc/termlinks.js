import { ensureOk, postJSON } from "@dc/http";

// Links in terminal text: web addresses, and paths of files of the terminal's
// project, which only the server can tell from other words.

const WEB_ADDRESS = /https?:\/\/[^\s"'<>`]+/g;

// The web addresses in a text. Trailing punctuation stays outside the link and
// inside the text, so a copy yields the same text either way.
export function webLinks(text) {
  const links = [];
  for (const match of text.matchAll(WEB_ADDRESS)) {
    const href = match[0].replace(/[).,;:!?\]]+$/, "");
    if (href) links.push({ start: match.index, end: match.index + href.length, href });
  }
  return links;
}

// A file opens in its project's editor, in place when this page is that
// editor (dc:open-file), else through a navigation. Ctrl, Cmd or Shift asks
// for a tab of its own, as on any link.
export function openFileLink(href, event) {
  if (event?.ctrlKey || event?.metaKey || event?.shiftKey) {
    window.open(href, "_blank", "noopener");
  } else if (document.dispatchEvent(new CustomEvent("dc:open-file", { cancelable: true, detail: { href } }))) {
    window.app.navigate(href);
  }
}

// ---- Live terminal ------------------------------------------------------------
// A link is resolved when somebody presses on it, from the buffer as it is at
// that moment, never on hover: xterm's link providers want their answers at
// once and keep them per row, a round trip to the server does neither.

// How many rows a mention may run over either way of the pressed one. The
// server joins up to twice as many and the pressed one (wrapRows in
// handlers_filelinks.go).
const WRAP_ROWS = 4;

// A token the server could take for a path holds a dot, a slash or a colon
// before a digit, as a :line does. Anything else is not worth a request.
const PATH_LIKE = /[./]|:\d/;

// The cell of the screen under a viewport point, counted from its top left
// one, beside the screen too.
export function screenCell(term, screen, { clientX, clientY }) {
  const rect = screen.getBoundingClientRect();
  return {
    x: Math.floor((clientX - rect.left) / (rect.width / term.cols)),
    y: Math.floor((clientY - rect.top) / (rect.height / term.rows)),
  };
}

// The buffer cell under a viewport point, null beside the screen.
export function cellAt(term, screen, point) {
  const { x, y } = screenCell(term, screen, point);
  if (x < 0 || y < 0 || x >= term.cols || y >= term.rows) return null;
  return { x, y: y + term.buffer.active.viewportY };
}

// xterm 5.5 keeps a program's OSC 8 link out of its public API: the cell
// carries an id, the core's link service the address.
function oscLink(term, cell) {
  const id = cell.extended?.urlId;
  return id ? term._core._oscLinkService.getLinkData(id)?.uri || "" : "";
}

// A row as text with the cells every UTF-16 unit stands on, because a wide
// glyph takes two cells and an emoji two units.
function rowAt(term, y) {
  const line = term.buffer.active.getLine(y);
  if (!line) return null;
  const row = { y, wrapped: line.isWrapped, text: "", units: [] };
  for (let x = 0; x < line.length; x++) {
    const cell = line.getCell(x);
    const width = cell.getWidth();
    if (!width) continue;
    const chars = cell.getChars() || " ";
    const unit = { x, y, width, osc: oscLink(term, cell) };
    row.text += chars;
    for (let i = 0; i < chars.length; i++) row.units.push(unit);
  }
  return row;
}

// A row the terminal wrapped goes on in the next, and so may one filled to the
// last column whose next row starts without a space, wrapped by a program.
const goesOn = (row, next) => next.wrapped || (/\S$/.test(row.text) && /^\S/.test(next.text));

// The rows around the pressed cell one mention may run over, those goesOn
// joins or else those a program wrapped the pressed word over with an indent.
// Where a program may have wrapped, the text holds a line break, which the
// server joins only when the joined text names a file.
function rowsAround(term, { x, y }) {
  const rows = [rowAt(term, y)];
  if (!rows[0]) return null;
  for (let i = 0; i < WRAP_ROWS; i++) {
    const prev = rowAt(term, rows[0].y - 1);
    if (!prev || !goesOn(prev, rows[0])) break;
    rows.unshift(prev);
  }
  for (let i = 0; i < WRAP_ROWS; i++) {
    const next = rowAt(term, rows.at(-1).y + 1);
    if (!next || !goesOn(rows.at(-1), next)) break;
    rows.push(next);
  }
  if (rows.length === 1) rows.splice(0, 1, ...indentedRows(term, rows[0], x));
  const joined = { text: "", units: [], rows };
  for (const [i, row] of rows.entries()) {
    if (i > 0 && !row.wrapped) {
      joined.text += "\n";
      joined.units.push(null);
    }
    joined.text += row.text;
    joined.units.push(...row.units);
  }
  return joined;
}

// The columns a row's text takes, from its first non blank cell to past its
// last one, null for a blank row.
function extent(row) {
  const first = row.text.search(/\S/);
  if (first < 0) return null;
  const last = row.units[row.text.trimEnd().length - 1];
  return { start: row.units[first].x, end: last.x + last.width };
}

// Where the word of text around index i starts and ends.
function wordAround(text, i) {
  return [text.slice(0, i).search(/\S*$/), i + text.slice(i).search(/\s|$/)];
}

// The rows a program wrapped the pressed word over by itself, as claude wraps
// a tool line: it fills a row up to its margin and goes on in the next at an
// indent where a word of the first row starts. So a word runs on from a row
// only into a next row at such an indent, and only from a row that reaches
// the margin, the farthest any row of that indented block ends. Such a
// program never lets a row run into the next, so a row that goesOn joins with
// a neighbour is none of them. The rows keep the margin and the indent, which
// tell the server that a mention may end at the break as well.
function indentedRows(term, pressed, x) {
  const seen = new Map();
  const lone = (y) => {
    if (!seen.has(y)) {
      const row = Math.abs(y - pressed.y) <= WRAP_ROWS && rowAt(term, y);
      const prev = rowAt(term, y - 1);
      const next = rowAt(term, y + 1);
      seen.set(y, row && extent(row) && !(prev && goesOn(prev, row)) && !(next && goesOn(row, next)) ? row : null);
    }
    return seen.get(y);
  };
  const wordStartsAt = (row, column) => {
    const i = row.units.findIndex((u) => u.x === column);
    return i > 0 && /\s\S/.test(row.text.slice(i - 1, i + 1));
  };
  const runsOn = (y) => {
    if (!lone(y) || !lone(y + 1)) return false;
    const indent = extent(lone(y + 1)).start;
    if (!wordStartsAt(lone(y), indent)) return false;
    const block = [];
    for (let k = y + 1; lone(k) && extent(lone(k)).start === indent; k++) block.push(lone(k));
    let k = y;
    for (; lone(k) && extent(lone(k)).start === indent; k--) block.push(lone(k));
    if (lone(k) && wordStartsAt(lone(k), indent)) block.push(lone(k));
    return extent(lone(y)).end === Math.max(...block.map((row) => extent(row).end));
  };
  const at = pressed.units.findIndex((u) => u.x <= x && x < u.x + u.width);
  if (at < 0 || /\s/.test(pressed.text[at])) return [pressed];
  const rows = [pressed];
  let [start, end] = wordAround(pressed.text, at);
  let row = pressed;
  while (end === row.text.trimEnd().length && runsOn(row.y)) {
    row = lone(row.y + 1);
    rows.push(row);
    end = wordAround(row.text, row.text.search(/\S/))[1];
  }
  row = pressed;
  while (start === row.text.search(/\S/) && runsOn(row.y - 1)) {
    row = lone(row.y - 1);
    rows.unshift(row);
    start = wordAround(row.text, row.text.trimEnd().length - 1)[0];
  }
  return rows;
}

// What a press on a cell may open: a program's OSC 8 link, a web address, or
// the rows around a token that may be a path, which fileLinkAt asks about.
// Within a row only a plain space ends the token, as the server's path token
// keeps any other blank, while any blank may pad a break.
export function linkAt(term, { x, y }) {
  const rows = rowsAround(term, { x, y });
  const at = rows ? rows.units.findIndex((u) => u?.y === y && u.x <= x && x < u.x + u.width) : -1;
  if (at < 0 || rows.text[at] === " ") return null;
  if (rows.units[at].osc) return { osc: rows.units[at].osc };
  const web = webLinks(rows.text).find((link) => link.start <= at && at < link.end);
  if (web) return { web: web.href };
  const word = [...rows.text.matchAll(/[^ \n]+(?:\s*\n\s*[^ \n]+)*/g)].find((m) => m.index <= at && at < m.index + m[0].length)[0];
  return PATH_LIKE.test(word.replace(/\s*\n\s*/g, "")) ? { ...rows, at } : null;
}

// The file the server finds where linkAt was pressed, once it answered and
// only while the rows the link runs over still read as they did: output may
// have moved or replaced them meanwhile, while a row beside them may change
// freely, a spinner or a counter does. A program's OSC 8 link wins on any
// cell it covers.
export async function fileLinkAt(term, url, { text, units, rows, at }) {
  const res = await ensureOk(await postJSON(url, { text }), "The links could not be read.");
  const link = ((await res.json()).links || []).find((l) => l.start <= at && at < l.end);
  if (!link) return "";
  const cells = units.slice(link.start, link.end).filter((u, i) => u && /\S/.test(text[link.start + i]));
  const over = rows.filter((row) => cells[0].y <= row.y && row.y <= cells.at(-1).y);
  return over.every((row) => rowAt(term, row.y)?.text === row.text) && !cells.some((u) => u.osc) ? link.href : "";
}
