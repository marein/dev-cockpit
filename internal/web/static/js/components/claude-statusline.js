import { escapeHtml } from "@dc/dom";

// The claude status line settings: one ordered list of entries, and a preview
// that follows it while it is edited. Adding, removing and dragging a row is
// deliberately the compose actions' gesture, grip handle and all, so the two
// settings lists are operated the same way with a mouse and with a finger; the
// drag below is that element's, kept in step with it on purpose.
const DRAG_THRESHOLD = 6;
const EDGE_ZONE = 56;
const EDGE_STEP = 10;

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
    this.wireDrag();
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  table(name, fallback = []) {
    try {
      return JSON.parse(this.getAttribute(name) || "") ?? fallback;
    } catch {
      return fallback;
    }
  }

  entryRows() {
    return this.rows ? [...this.rows.querySelectorAll("[data-entry-row]")] : [];
  }

  // syncRow makes a row show what its kind and value mean. The bounds are the
  // one part that may not travel: a row that is no number posts none of them
  // and says so in its count, or the flat list of bounds would be read into
  // the next row that does carry some.
  syncRow(row) {
    if (!row) return;
    const kind = row.querySelector("[data-entry-kind]")?.value || "value";
    const valueId = row.querySelector("[data-entry-value]")?.value || "";
    const value = this.values.get(valueId);
    const numeric = kind === "value" && !!value?.numeric;
    const own = kind === "value" && !!value?.own;
    row.dataset.entryNumeric = numeric ? "1" : "0";
    const shown = {
      value: kind === "value",
      // One field serves the two entries that carry a text of their own, the
      // separator and the free text value, so there is one input to post.
      text: kind === "separator" || own,
      color: kind === "value" && !numeric,
      thresholds: numeric,
    };
    for (const part of row.querySelectorAll("[data-entry-part]")) {
      part.hidden = !shown[part.dataset.entryPart];
    }
    const textLabel = row.querySelector("[data-entry-text-label]");
    if (textLabel) textLabel.textContent = kind === "separator" ? "Separator" : "Text";
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

  // paint renders the line the way the generated script builds it: pieces
  // joined by a single space, a separator only between two of them, and a line
  // break starting a line of its own.
  paint() {
    if (!this.preview) return;
    const lines = [];
    let line = [];
    let pending = "";
    for (const row of this.entryRows()) {
      const kind = row.querySelector("[data-entry-kind]")?.value || "value";
      if (kind === "break") {
        lines.push(line.join(" "));
        line = [];
        pending = "";
        continue;
      }
      if (kind === "separator") {
        if (line.length) {
          const text = row.querySelector('[name="entry_text"]')?.value.trim() || "·";
          pending = this.span("dim", text);
        }
        continue;
      }
      const piece = this.valuePiece(row);
      if (!piece) continue;
      if (line.length && pending) line.push(pending);
      pending = "";
      line.push(piece);
    }
    lines.push(line.join(" "));
    this.preview.innerHTML = lines.join("\n") || "&nbsp;";
  }

  valuePiece(row) {
    const value = this.values.get(row.querySelector("[data-entry-value]")?.value || "");
    if (!value) return "";
    // The free text value stands in for nothing, it shows what is typed, so
    // an empty one drops out of the line the way a missing value does.
    const shown = value.own ? row.querySelector('[name="entry_text"]')?.value.trim() || "" : value.sample;
    if (!shown) return "";
    const label = row.querySelector('[name="entry_label"]')?.value.trim() || "";
    const parts = [];
    if (label) parts.push(this.span(row.querySelector("[data-entry-label-color]")?.value, label));
    parts.push(this.span(this.valueColor(row, value), shown));
    return parts.join(" ");
  }

  // valueColor is the bound rule of the script in one place more: the highest
  // bound the number reaches wins, and a number below every bound wears the
  // terminal's own color.
  valueColor(row, value) {
    if (!value.numeric) return row.querySelector("[data-entry-color]")?.value;
    let picked = "";
    let at = null;
    for (const bound of row.querySelectorAll("[data-threshold-row]")) {
      const raw = Number(bound.querySelector('[name="threshold_at"]')?.value);
      if (!Number.isFinite(raw) || value.number < raw) continue;
      if (at === null || raw >= at) {
        at = raw;
        picked = bound.querySelector('[name="threshold_color"]')?.value || "";
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

  wireDrag() {
    if (!this.rows) return;
    const signal = this.ac.signal;
    let drag = null;
    const docY = (clientY) => clientY + window.scrollY;
    const update = () => {
      if (!drag || !drag.active) return;
      const dy = docY(drag.lastClientY) - drag.startDocY;
      const center = drag.centers[drag.fromIndex] + dy;
      let toIndex = 0;
      for (let i = 0; i < drag.centers.length; i++) {
        if (i !== drag.fromIndex && drag.centers[i] < center) toIndex += 1;
      }
      drag.toIndex = toIndex;
      drag.row.style.transform = `translateY(${dy}px)`;
      drag.els.forEach((el, i) => {
        if (el === drag.row) return;
        let shift = 0;
        if (i > drag.fromIndex && i <= drag.toIndex) shift = -drag.step;
        else if (i < drag.fromIndex && i >= drag.toIndex) shift = drag.step;
        el.style.transform = shift ? `translateY(${shift}px)` : "";
      });
    };
    const tick = () => {
      if (!drag || !drag.active) return;
      let delta = 0;
      if (drag.lastClientY < EDGE_ZONE) delta = -EDGE_STEP;
      else if (drag.lastClientY > window.innerHeight - EDGE_ZONE) delta = EDGE_STEP;
      if (delta) {
        const before = window.scrollY;
        window.scrollBy(0, delta);
        if (window.scrollY !== before) update();
      }
      drag.raf = window.requestAnimationFrame(tick);
    };
    const clear = () => {
      if (!drag) return;
      if (drag.active) {
        window.cancelAnimationFrame(drag.raf);
        this.classList.remove("dc-drag-list");
        drag.row.classList.remove("dc-drag-lift");
        for (const el of drag.els) el.style.transform = "";
      }
      drag = null;
    };
    this.addEventListener("pointerdown", (event) => {
      if (event.button !== 0 || drag) return;
      const grip = event.target.closest("[data-entry-grip]");
      if (!grip || !this.contains(grip)) return;
      const row = grip.closest("[data-entry-row]");
      if (!row) return;
      drag = {
        row,
        grip,
        pointerId: event.pointerId,
        startClientX: event.clientX,
        startClientY: event.clientY,
        lastClientY: event.clientY,
        active: false,
        raf: 0,
      };
    }, { signal });
    this.addEventListener("pointermove", (event) => {
      if (!drag || event.pointerId !== drag.pointerId) return;
      if (!drag.active) {
        if (Math.hypot(event.clientX - drag.startClientX, event.clientY - drag.startClientY) < DRAG_THRESHOLD) return;
        drag.active = true;
        drag.els = this.entryRows();
        drag.fromIndex = drag.els.indexOf(drag.row);
        drag.toIndex = drag.fromIndex;
        const rect = drag.row.getBoundingClientRect();
        drag.step = rect.height + parseFloat(getComputedStyle(drag.row).marginBottom) || rect.height;
        drag.centers = drag.els.map((el) => {
          const box = el.getBoundingClientRect();
          return box.top + box.height / 2 + window.scrollY;
        });
        drag.startDocY = docY(event.clientY);
        this.classList.add("dc-drag-list");
        drag.row.classList.add("dc-drag-lift");
        try {
          drag.grip.setPointerCapture(event.pointerId);
        } catch (error) {
          void error;
        }
        drag.raf = window.requestAnimationFrame(tick);
      }
      event.preventDefault();
      drag.lastClientY = event.clientY;
      update();
    }, { passive: false, signal });
    this.addEventListener("pointerup", (event) => {
      if (!drag || event.pointerId !== drag.pointerId) return;
      const done = drag;
      clear();
      if (!done.active || done.toIndex === done.fromIndex) return;
      const others = done.els.filter((el) => el !== done.row);
      this.rows.insertBefore(done.row, others[done.toIndex] || null);
      this.paint();
    }, { signal });
    this.addEventListener("pointercancel", clear, { signal });
  }
}

customElements.define("dc-claude-statusline", ClaudeStatusLine);
