import { escapeHtml } from "@dc/dom";
import { baseName, externalHint } from "@dc/editor-format";
import { hitRanges } from "@dc/filter";

// The rows and the words of the palette's type search. The list, the keys
// and the jump are the palette's own, as for every other mode; this draws a
// row and says what stands between the query and the answer.

const KIND_ICONS = {
  class: "ti-letter-c",
  enum: "ti-list-numbers",
  interface: "ti-plug",
  struct: "ti-box",
  trait: "ti-puzzle",
  type: "ti-letter-t",
};

// A Go package path and a PHP namespace end in what says which package it
// is, so they are cut like the hint of a file outside the project; a name
// without slashes or backslashes stands whole.
export function packageHint(container) {
  const sep = (container || "").includes("/") ? "/" : "\\";
  const parts = (container || "").split(sep).filter(Boolean);
  return parts.length > 2 ? `…${sep}${parts.slice(-2).join(sep)}` : container || "";
}

// The server matches the parts of a qualified name as words, see queryParts.
function markedName(name, query) {
  let html = "";
  let at = 0;
  for (const [from, to] of hitRanges(name, query.replace(/[\\.]/g, " "))) {
    html += `${escapeHtml(name.slice(at, from))}<mark>${escapeHtml(name.slice(from, to))}</mark>`;
    at = to;
  }
  return html + escapeHtml(name.slice(at));
}

// One row: the kind's icon and word, the name with the query marked, the
// package, and under them the file and line it is declared on. A type of a
// dependency or the standard library says so beside its kind, and a file of
// it outside the project is cut from the front like its read only tab.
export function typeRow(entry, query) {
  const item = document.createElement("div");
  item.className = "editor-quickopen-item editor-quickopen-match";
  item.setAttribute("role", "option");
  item.dataset.kind = entry.kind;
  item.title = `${entry.kind} ${entry.name}${entry.container ? ` in ${entry.container}` : ""}, ${entry.path}:${entry.line}${entry.library ? ", outside the project" : ""}`;
  const head = document.createElement("div");
  head.className = "editor-quickopen-match-head";
  head.innerHTML = `<i class="ti ${KIND_ICONS[entry.kind] || "ti-code"}"></i>`
    + `<span class="editor-quickopen-name">${markedName(entry.name, query)}</span>`
    + `<span class="badge bg-secondary-lt flex-shrink-0">${escapeHtml(entry.kind)}</span>`
    + (entry.library ? `<span class="badge badge-outline text-secondary flex-shrink-0" data-editor-type-library>library</span>` : "")
    + `<span class="editor-quickopen-dir">${escapeHtml(packageHint(entry.container))}</span>`;
  const place = document.createElement("div");
  place.className = "editor-quickopen-match-text";
  place.textContent = `${entry.external ? `${externalHint(entry.path)}/${baseName(entry.path)}` : entry.path}:${entry.line}`;
  item.append(head, place);
  return item;
}

// What the project's servers are doing while an answer is not there yet:
// the image build, the index and how far it is, nothing once they stand.
export function typesWaitText(state) {
  if (!state) return "";
  if (state.preparing) return `Preparing ${state.label}…`;
  const pct = typeof state.percentage === "number" && state.percentage >= 0 ? ` ${state.percentage}%` : "";
  return `Indexing ${state.label}…${pct}`;
}

// The languages that could not answer, in the words of the lookups. A
// canceled one was overtaken by a newer query and says nothing.
export function typesStatusNote(statuses, statusText) {
  return (statuses || [])
    .filter((entry) => entry.status !== "canceled")
    .map((entry) => `${entry.label}: ${statusText[entry.status] || "The language server did not answer."}`)
    .join(" ");
}
