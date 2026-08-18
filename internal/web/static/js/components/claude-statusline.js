import { escapeHtml } from "@dc/dom";
import { RowDrag } from "@dc/rowdrag";

class ClaudeStatusLine extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    this.rows = this.querySelector("[data-entry-rows]");
    this.entryTemplate = this.querySelector("[data-entry-template]");
    this.boundTemplate = this.querySelector("[data-threshold-template]");
    this.preview = this.querySelector("[data-statusline-preview]");
    this.values = new Map();
    for (const value of this.table("values")) this.values.set(value.id, value);
    this.colors = this.table("colors", {});

    this.addEventListener("click", (event) => {
      const remove = event.target.closest("[data-entry-remove]");
      if (remove && this.contains(remove)) {
        event.preventDefault();
        remove.closest("[data-entry-row]")?.remove();
        this.paint();
        return;
      }
      const add = event.target.closest("[data-entry-add]");
      if (add && this.contains(add)) {
        event.preventDefault();
        this.addRow();
        return;
      }
      const addBound = event.target.closest("[data-threshold-add]");
      if (addBound && this.contains(addBound)) {
        event.preventDefault();
        this.addBound(addBound.closest("[data-entry-row]"));
        return;
      }
      const removeBound = event.target.closest("[data-threshold-remove]");
      if (removeBound && this.contains(removeBound)) {
        event.preventDefault();
        const row = removeBound.closest("[data-entry-row]");
        removeBound.closest("[data-threshold-row]")?.remove();
        this.syncRow(row);
        this.paint();
      }
    }, { signal: this.ac.signal });

    for (const type of ["change", "input"]) {
      this.addEventListener(type, (event) => {
        const row = event.target.closest("[data-entry-row]");
        if (row && this.contains(row)) {
          // A row that has just become a number starts with one bound, and
          // that bound comes out of the server's own template, so no color
          // name is written down a second time here. A row that was loaded
          // without bounds keeps none: no bound at all is an answer too.
          const was = row.dataset.entryNumeric === "1";
          this.syncRow(row);
          if (!was && row.dataset.entryNumeric === "1" && !row.querySelector("[data-threshold-row]")) {
            this.addBound(row, false);
          }
        }
        this.paint();
      }, { signal: this.ac.signal });
    }

    for (const row of this.entryRows()) this.syncRow(row);
    this.paint();
    for (const pre of this.querySelectorAll("[data-preset-preview]")) {
      pre.innerHTML = this.lineHTML(this.parse(pre.dataset.entries, []));
    }
    this.wireDrag();
  }

  disconnectedCallback() {
    this.rowDrag?.cancel();
    this.ac?.abort();
    this.ac = null;
  }

  table(name, fallback = []) {
    return this.parse(this.getAttribute(name), fallback);
  }

  parse(text, fallback) {
    try {
      return JSON.parse(text || "") ?? fallback;
    } catch {
      return fallback;
    }
  }

  entryRows() {
    return this.rows ? [...this.rows.querySelectorAll("[data-entry-row]")] : [];
  }

  // A row that is no number posts no bounds and says so in its count, or the
  // flat list of bounds would be read into the next row that does carry some.
  syncRow(row) {
    if (!row) return;
    const kind = row.querySelector("[data-entry-kind]")?.value || "value";
    const valueId = row.querySelector("[data-entry-value]")?.value || "";
    const value = this.values.get(valueId);
    const numeric = kind === "value" && !!value?.numeric;
    const own = kind === "value" && !!value?.own;
    const textLabel = kind === "separator" ? "Separator" : kind === "value" ? value?.textLabel || "" : "";
    row.dataset.entryNumeric = numeric ? "1" : "0";
    const shown = {
      value: kind === "value",
      // One field serves every entry that carries a text of its own, the
      // separator, the free text and the command, so there is one input to post.
      text: textLabel !== "",
      color: kind === "value" && !numeric,
      thresholds: numeric,
    };
    for (const part of row.querySelectorAll("[data-entry-part]")) {
      part.hidden = !shown[part.dataset.entryPart];
    }
    const label = row.querySelector("[data-entry-text-label]");
    if (label) label.textContent = textLabel;
    const text = row.querySelector('[name="entry_text"]');
    if (text) {
      if (kind === "separator" || own) text.setAttribute("maxlength", text.dataset.textMax);
      else text.removeAttribute("maxlength");
    }
    const hint = row.querySelector("[data-entry-hint]");
    if (hint) hint.textContent = value?.hint || "";
    const bounds = [...row.querySelectorAll("[data-threshold-row]")];
    for (const bound of bounds) {
      for (const field of bound.querySelectorAll("input, select")) field.disabled = !numeric;
    }
    const count = row.querySelector("[data-threshold-count]");
    if (count) count.value = numeric ? String(bounds.length) : "0";
  }

  addRow() {
    if (!this.rows || !this.entryTemplate) return;
    const row = this.entryTemplate.content.firstElementChild.cloneNode(true);
    this.rows.appendChild(row);
    this.querySelector("[data-entries-empty]")?.remove();
    this.syncRow(row);
    this.paint();
    row.querySelector("[data-entry-kind]")?.focus();
  }

  addBound(row, repaint = true) {
    if (!row || !this.boundTemplate) return;
    const list = row.querySelector("[data-threshold-rows]");
    if (!list) return;
    list.appendChild(this.boundTemplate.content.firstElementChild.cloneNode(true));
    this.syncRow(row);
    if (repaint) this.paint();
  }

  paint() {
    if (this.preview) this.preview.innerHTML = this.lineHTML(this.rowEntries());
  }

  // The list as data in the shape a saved entry has, so the preview of the
  // form and the preview of a preset come out of one renderer.
  rowEntries() {
    return this.entryRows().map((row) => ({
      kind: row.querySelector("[data-entry-kind]")?.value || "value",
      value: row.querySelector("[data-entry-value]")?.value || "",
      label: row.querySelector('[name="entry_label"]')?.value.trim() || "",
      labelColor: row.querySelector("[data-entry-label-color]")?.value || "",
      color: row.querySelector("[data-entry-color]")?.value || "",
      text: row.querySelector('[name="entry_text"]')?.value.trim() || "",
      thresholds: [...row.querySelectorAll("[data-threshold-row]")].map((bound) => ({
        at: Number(bound.querySelector('[name="threshold_at"]')?.value),
        color: bound.querySelector('[name="threshold_color"]')?.value || "",
      })),
    }));
  }

  lineHTML(entries) {
    const lines = [];
    let line = [];
    let pending = "";
    for (const entry of entries) {
      const kind = entry.kind || "value";
      if (kind === "break") {
        lines.push(line.join(" "));
        line = [];
        pending = "";
        continue;
      }
      if (kind === "separator") {
        if (line.length) pending = this.span("dim", entry.text || "·");
        continue;
      }
      const piece = this.valuePiece(entry);
      if (!piece) continue;
      if (line.length && pending) line.push(pending);
      pending = "";
      line.push(piece);
    }
    lines.push(line.join(" "));
    return lines.join("\n") || "&nbsp;";
  }

  valuePiece(entry) {
    const value = this.values.get(entry.value || "");
    if (!value) return "";
    // The free text value stands in for nothing, it shows what is typed, so
    // an empty one drops out of the line the way a missing value does.
    const shown = value.own ? entry.text || "" : value.sample;
    if (!shown) return "";
    const parts = [];
    if (entry.label) parts.push(this.span(entry.labelColor, entry.label));
    parts.push(this.span(this.valueColor(entry, value), shown));
    return parts.join(" ");
  }

  valueColor(entry, value) {
    if (!value.numeric) return entry.color;
    let picked = "";
    let at = null;
    for (const bound of entry.thresholds || []) {
      const raw = Number(bound.at);
      if (!Number.isFinite(raw) || value.number < raw) continue;
      if (at === null || raw >= at) {
        at = raw;
        picked = bound.color || "";
      }
    }
    return picked;
  }

  span(color, text) {
    const css = this.colors[color];
    const dim = color === "dim" ? " opacity:.75;" : "";
    const style = css ? ` style="color:${css};${dim}"` : "";
    return `<span${style}>${escapeHtml(text)}</span>`;
  }

  // The capture waits for the drag, so a press on a label or a field inside a
  // row still does what it does without one.
  wireDrag() {
    if (!this.rows) return;
    this.rowDrag = new RowDrag(this, {
      rowSelector: "[data-entry-row]",
      gripSelector: "[data-entry-grip]",
      ignoreSelector: "input, select, textarea, label, button:not([data-entry-grip])",
      capture: "drag",
      classes: { list: "dc-drag-list", row: "dc-drag-lift" },
      rows: () => this.entryRows(),
      scroller: () => this.closest(".dc-work-body") || this,
      onDrop: () => this.paint(),
    });
    this.rowDrag.wire(this.ac.signal);
  }
}

customElements.define("dc-claude-statusline", ClaudeStatusLine);
