import { escapeHtml } from "@dc/dom";
import { rankTokens } from "@dc/filter";
import { KIND_ICONS, markedName } from "@dc/editor-types";

// The rows of the structure sheet of the open file. The sheet, its filter
// field, the keys and the jump are the editor's own, as for every sheet.

const ICONS = {
  ...KIND_ICONS,
  method: "ti-math-function",
  function: "ti-math-function",
  constructor: "ti-math-function",
  property: "ti-point",
  field: "ti-point",
  "enum member": "ti-point",
  variable: "ti-variable",
  constant: "ti-lock",
  namespace: "ti-braces",
  module: "ti-package",
  package: "ti-package",
  h1: "ti-h-1",
  h2: "ti-h-2",
  h3: "ti-h-3",
  h4: "ti-h-4",
  h5: "ti-h-5",
  h6: "ti-h-6",
};

// The tree in file order, each symbol with its depth, the names of the
// symbols it stands in and whether the cursor stands in it.
export function structureRows(symbols) {
  const rows = [];
  const walk = (list, parents) => {
    for (const sym of list || []) {
      rows.push({ name: sym.name, kind: sym.kind, detail: sym.detail || "", line: sym.line, character: sym.character, anchor: sym.anchor || "", cursor: !!sym.cursor, parents });
      walk(sym.children, [...parents, sym.name]);
    }
  };
  walk(symbols, []);
  return rows;
}

// By the quick open rule: every word stands in the name or in the names
// around it, the first ranks by where it hits the name, and equal ranks
// keep the file's order.
export function filterStructure(rows, query) {
  if (!query) return rows;
  return rows
    .map((row, index) => ({ row, index, rank: rankTokens(row.name, `${row.name} ${row.parents.join(" ")}`, query) }))
    .filter((entry) => entry.rank >= 0)
    .sort((a, b) => a.rank - b.rank || a.index - b.index)
    .map((entry) => entry.row);
}

// One row: the kind's icon and word, the name with the query marked, the
// detail and the line. In the tree a row stands one icon further in than
// its parent; a filtered list is ranked, so the parent is named instead.
export function structureRow(row, query) {
  const item = document.createElement("div");
  item.className = "editor-sheet-row";
  item.dataset.kind = row.kind;
  const open = document.createElement("button");
  open.type = "button";
  open.className = "editor-sheet-open";
  const parent = row.parents.at(-1) || "";
  open.title = `${row.kind} ${row.name}${parent ? ` in ${parent}` : ""}${row.detail ? ` ${row.detail}` : ""}, line ${row.line}`;
  open.innerHTML = (query ? "" : '<i class="ti ti-point invisible" aria-hidden="true"></i>'.repeat(row.parents.length))
    + `<i class="ti ${ICONS[row.kind] || "ti-code"} text-secondary flex-shrink-0" data-editor-structure-icon></i>`
    + `<span class="editor-quickopen-name">${markedName(row.name, query)}</span>`
    + (query && parent ? `<span class="text-secondary text-truncate" data-editor-structure-parent>${escapeHtml(parent)}</span>` : "")
    + `<span class="badge bg-secondary-lt flex-shrink-0">${escapeHtml(row.kind)}</span>`
    + `<span class="editor-quickopen-dir font-monospace">${escapeHtml(row.detail)}</span>`
    + `<span class="text-secondary flex-shrink-0" data-editor-structure-line>${row.line}</span>`;
  item.append(open);
  return item;
}
