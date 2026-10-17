import { labelNodes } from "@dc/contextmenu";
import { el } from "@dc/dom";

export function sortComments(list) {
  return [...list].sort((a, b) => (a.path === b.path ? a.line - b.line : a.path < b.path ? -1 : 1));
}

const flatLine = (text) => String(text || "").replace(/[\u0000-\u0008\u000a-\u001f\u007f-\u009f\u2028\u2029]/g, " ");

const noteLines = (c) => c.text.split("\n").map((line) => (c.author ? `${flatLine(c.author)}: ${flatLine(line)}` : flatLine(line)).trimEnd());

export function findUniqueQuoteLine(doc, quote) {
  if (!quote) return 0;
  let at = 0;
  for (let n = 1; n <= doc.lines; n++) {
    if (doc.line(n).text === quote) {
      if (at) return 0;
      at = n;
    }
  }
  return at;
}

const SHORT_ESCAPES = { "\t": "\\t", "\n": "\\n", "\r": "\\r", "\f": "\\f", "\v": "\\v", "\b": "\\b", "\u0007": "\\a" };

function escapeOf(char) {
  const code = char.charCodeAt(0);
  return SHORT_ESCAPES[char] || (code < 0x100 ? `\\x${code.toString(16).padStart(2, "0")}` : `\\u${code.toString(16).padStart(4, "0")}`);
}

// visibleLine is a value that stands on one line, a path or a quoted code
// line, as nodes that keep it there: a control character or a line or
// paragraph separator shows as its escape in a code chip. WebKit breaks a line
// at the separators even where nothing may wrap.
export function visibleLine(text) {
  const value = String(text || "");
  const nodes = [];
  let at = 0;
  for (const match of value.matchAll(/[\u0000-\u001f\u007f-\u009f\u2028\u2029]/g)) {
    if (match.index > at) nodes.push(value.slice(at, match.index));
    nodes.push(el("code", {}, escapeOf(match[0])));
    at = match.index + 1;
  }
  if (at < value.length) nodes.push(value.slice(at));
  return nodes;
}

export const placeNodes = (path, line) => labelNodes({ head: visibleLine(path), tail: `:${line}` });

// commentRow is what one entry of the Line comments sheet shows, as the
// sheet's own row takes it: the path on one line and the note flattened to
// one, so nothing a comment holds breaks out of its entry, and under an
// outdated one the line it was made on.
export function commentRow(comment) {
  const note = flatLine(comment.text);
  const quote = String(comment.lineText || "").trim();
  const outdated = quote ? `Outdated · was: ${flatLine(quote)}` : "Outdated";
  const row = {
    icon: "ti-message-circle",
    iconClass: comment.outdated ? "text-orange" : undefined,
    label: { head: visibleLine(comment.path), tail: `:${comment.line}` },
    title: [...noteLines(comment), ...(comment.outdated ? [outdated] : [])].join("\n"),
  };
  if (!comment.outdated) return { ...row, sub: note };
  const old = el("span", { class: "text-orange text-truncate" }, quote ? ["Outdated · was: ", ...visibleLine(quote)] : outdated);
  return { ...row, subNodes: [el("span", { class: "d-flex flex-column min-w-0" }, el("span", { class: "text-truncate" }, note), old)] };
}

// authorHead gives the entry of a comment a guest wrote one more line at its
// head, the person icon and the guest's name.
export function authorHead(row, comment) {
  if (!comment.author) return row;
  const own = el("span", { class: "d-flex align-items-center gap-2 min-w-0" }, ...row.childNodes);
  row.classList.replace("align-items-center", "align-items-stretch");
  row.classList.replace("gap-2", "gap-1");
  row.classList.add("flex-column");
  row.replaceChildren(el(
    "span",
    { class: "d-flex align-items-center gap-2 min-w-0 text-secondary" },
    el("i", { class: "ti ti-user", "aria-hidden": "true" }),
    el("span", { class: "d-flex min-w-0" }, ...labelNodes({ head: visibleLine(comment.author) })),
  ), own);
  return row;
}
