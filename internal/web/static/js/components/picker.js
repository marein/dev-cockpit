import { paletteGroups } from "@dc/project-palette";
import * as projectSort from "@dc/project-sort";
import { rankTokens } from "@dc/filter";

const finePointer = window.matchMedia("(pointer: fine)");

function headed(group, label) {
  if (!label || group.querySelector(".editor-palette-group-head")) return group;
  const head = document.createElement("div");
  head.className = "editor-palette-group-head";
  head.textContent = label;
  group.prepend(head);
  return group;
}

function plainRows(rows, query) {
  const node = document.createElement("div");
  node.className = "editor-palette-group";
  const hits = query
    ? rows.map((row) => ({ row, rank: rankTokens(row.dataset.pickerName || "", row.dataset.pickerSearch || "", query) }))
      .filter((hit) => hit.rank >= 0)
      .sort((a, b) => a.rank - b.rank)
      .map((hit) => hit.row)
    : rows;
  hits.forEach((row) => node.appendChild(row.cloneNode(true)));
  return hits.length ? [node] : [];
}

class Picker extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.summary = this.querySelector("[data-picker-summary]");
    this.count = this.querySelector("[data-picker-count]");
    this.filter = this.querySelector("[data-picker-filter]");
    this.list = this.querySelector("[data-picker-list]");
    this.empty = this.querySelector("[data-picker-empty]");
    this.source = this.querySelector("[data-picker-source]");
    if (!this.summary || !this.count || !this.filter || !this.list || !this.empty || !this.source) return;
    this.ac = new AbortController();
    const signal = this.ac.signal;
    this.index = -1;
    this.source.querySelectorAll("[data-picker-projects]").forEach((kind) => projectSort.sort(kind));
    this.addEventListener("show.bs.dropdown", () => {
      this.filter.value = "";
      this.render();
    }, { signal });
    this.addEventListener("shown.bs.dropdown", () => {
      if (finePointer.matches) this.filter.focus();
    }, { signal });
    this.addEventListener("hidden.bs.dropdown", () => this.list.replaceChildren(), { signal });
    this.filter.addEventListener("input", () => this.render(), { signal });
    this.filter.addEventListener("keydown", (event) => this.onKeydown(event), { signal });
    this.list.addEventListener("mousedown", (event) => event.preventDefault(), { signal });
    this.list.addEventListener("click", (event) => {
      const row = event.target instanceof Element ? event.target.closest('[role="option"]') : null;
      if (row) this.pick(row);
    }, { signal });
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  values() {
    return Array.from(this.querySelectorAll("[data-picker-value]"));
  }

  rows() {
    return Array.from(this.list.querySelectorAll('[role="option"]'));
  }

  render() {
    const query = this.filter.value.trim();
    const groups = Array.from(this.source.querySelectorAll("[data-picker-kind]")).flatMap((kind) => {
      const rows = Array.from(kind.querySelectorAll("[data-picker-key]"));
      const found = "pickerProjects" in kind.dataset ? paletteGroups(rows, query) : plainRows(rows, query);
      return found.map((group) => headed(group, kind.dataset.pickerHead));
    });
    this.list.replaceChildren(...groups);
    const shown = this.rows();
    shown.forEach((row, i) => {
      row.id = `${this.list.id}-${i}`;
      this.paint(row);
    });
    this.empty.hidden = shown.length > 0;
    this.list.scrollTop = 0;
    this.mark(query && finePointer.matches ? 0 : -1);
  }

  paint(row) {
    const on = this.values().some((box) => box.checked && box.dataset.pickerValue === row.dataset.pickerKey);
    row.setAttribute("aria-selected", String(on));
    const box = row.querySelector('input[type="checkbox"]');
    if (box) box.checked = on;
  }

  pick(row) {
    const value = this.values().find((box) => box.dataset.pickerValue === row.dataset.pickerKey);
    if (value) this.set(value.dataset.pickerValue, !value.checked);
  }

  set(key, on) {
    const value = this.values().find((box) => box.dataset.pickerValue === key);
    if (!value || value.checked === on) return;
    value.checked = on;
    this.rows().filter((each) => each.dataset.pickerKey === key).forEach((each) => this.paint(each));
    const names = this.values().filter((box) => box.checked).map((box) => box.dataset.pickerLabel);
    this.summary.textContent = names.join(", ") || this.summary.dataset.pickerNone;
    this.summary.classList.toggle("text-secondary", !names.length);
    this.count.textContent = String(names.length);
    this.count.hidden = !names.length;
    value.dispatchEvent(new Event("change", { bubbles: true }));
  }

  mark(index) {
    const rows = this.rows();
    this.index = index >= 0 && index < rows.length ? index : -1;
    rows.forEach((row, i) => row.classList.toggle("active", i === this.index));
    const row = rows[this.index];
    if (row) {
      this.filter.setAttribute("aria-activedescendant", row.id);
      row.scrollIntoView({ block: "nearest" });
    } else {
      this.filter.removeAttribute("aria-activedescendant");
    }
  }

  onKeydown(event) {
    const rows = this.rows();
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      if (!rows.length) return;
      const delta = event.key === "ArrowDown" ? 1 : -1;
      this.mark(this.index < 0 ? (delta > 0 ? 0 : rows.length - 1) : (this.index + delta + rows.length) % rows.length);
      return;
    }
    if (event.key !== "Enter") return;
    event.preventDefault();
    const row = rows[this.index] || (this.filter.value.trim() ? rows[0] : null);
    if (row) this.pick(row);
  }
}

customElements.define("dc-picker", Picker);
