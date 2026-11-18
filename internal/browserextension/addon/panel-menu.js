import { el } from "./api.js";
import { rankRows, rankTokens } from "./filter.js";

const finePointer = matchMedia("(pointer: fine)");

// The Send menu follows the cockpit's + menu of Terminals: the cockpit's own
// filter.js ranks the rows under a field that keeps the keyboard, so the
// selection is a class on a row and never the focus.
export class SendMenu {
  constructor(menu, toggle) {
    this.menu = menu;
    this.toggle = toggle;
    this.field = menu.querySelector("[data-menu-filter]");
    this.clear = menu.querySelector("[data-menu-filter-clear]");
    this.list = menu.querySelector("[data-menu-rows]");
    this.rows = [];
    this.none = null;
    this.at = -1;
    this.inList = false;
    this.wire();
  }

  get open() {
    return !this.menu.hidden;
  }

  wire() {
    this.field.addEventListener("input", () => {
      this.at = 0;
      this.filter();
    });
    this.field.addEventListener("keydown", (event) => this.onKeydown(event));
    this.menu.addEventListener("mousedown", (event) => {
      if (event.target !== this.field) event.preventDefault();
    });
    this.field.parentElement.addEventListener("click", () => this.field.focus());
    this.clear.addEventListener("click", () => {
      this.field.value = "";
      this.filter();
    });
    const doc = this.menu.ownerDocument;
    doc.addEventListener("pointermove", (event) => this.pointed(event));
    doc.documentElement.addEventListener("pointerleave", (event) => this.pointed(event));
  }

  // When the panel scrolls, the rows slide under a resting pointer and the
  // browser reports it with pointerover and pointerleave, in Firefox even off
  // by a fraction of a pixel, but never with pointermove. So only a
  // pointermove, or the pointer leaving the panel, marks a row or clears the
  // mark.
  pointed(event) {
    if (event.pointerType === "touch" || !(event.target instanceof Element)) return;
    const inList = this.list.contains(event.target);
    const row = inList && event.target.closest("[role=menuitem]");
    if (row) this.mark(this.shown().indexOf(row));
    else if (this.inList && !inList) this.mark(-1);
    this.inList = inList;
  }

  show(byKeyboard) {
    this.at = byKeyboard ? 0 : -1;
    this.inList = false;
    this.list.replaceChildren(el("div", { class: "menu-empty" }, "Asking the cockpit…"));
    this.menu.hidden = false;
    this.toggle.setAttribute("aria-expanded", "true");
    this.filter();
    if (byKeyboard || finePointer.matches) this.field.focus();
  }

  close() {
    this.menu.hidden = true;
    this.toggle.setAttribute("aria-expanded", "false");
    this.field.value = "";
    this.list.replaceChildren();
    this.rows = [];
    this.none = null;
    this.at = -1;
  }

  fill(sections) {
    this.rows = [];
    const nodes = sections.map(({ kind, head = "", empty = "", rows }) => {
      const buttons = rows.map(({ name, hint = "", choose }) => {
        const button = el("button", { type: "button", role: "menuitem", tabindex: "-1", title: name, dataset: { target: kind, name, hint } }, el("span", { class: "menu-name" }, name), hint ? el("span", { class: "menu-hint" }, hint) : null);
        button.addEventListener("click", () => {
          this.close();
          choose();
        });
        return button;
      });
      this.rows.push(...buttons);
      return el("div", { role: "group", "aria-label": head || null, dataset: { menuSection: kind } },
        el("div", { class: "menu-line", role: "separator" }),
        head ? el("div", { class: "menu-head" }, head) : null,
        buttons,
        !buttons.length && empty ? el("div", { class: "menu-empty", dataset: { menuEmpty: "" } }, empty) : null);
    });
    this.none = el("div", { class: "menu-empty", hidden: true }, "No matches");
    this.list.replaceChildren(...nodes, this.none);
    this.filter();
    this.menu.scrollIntoView({ block: "nearest" });
  }

  filter() {
    const query = this.field.value.trim();
    this.clear.style.visibility = query ? "" : "hidden";
    if (!this.none) return;
    const { shown } = rankRows(this.rows, (row) => (query ? rankTokens(row.dataset.name, `${row.dataset.name} ${row.dataset.hint}`, query) : 0), {
      groupOf: (row) => row.dataset.target,
    });
    for (const section of this.list.querySelectorAll("[data-menu-section]")) {
      section.hidden = query ? !shown.some((row) => section.contains(row)) : !section.querySelector("[role=menuitem], [data-menu-empty]");
    }
    this.none.hidden = !query || shown.length > 0;
    this.paint();
  }

  shown() {
    return Array.from(this.list.querySelectorAll("[role=menuitem]")).filter((row) => row.offsetParent);
  }

  paint() {
    for (const row of this.rows) {
      row.classList.remove("selected");
      row.removeAttribute("aria-current");
    }
    const rows = this.shown();
    if (this.at < 0 || !rows.length) return;
    this.at = Math.min(this.at, rows.length - 1);
    const row = rows[this.at];
    row.classList.add("selected");
    row.setAttribute("aria-current", "true");
    const box = this.list.getBoundingClientRect();
    const rect = row.getBoundingClientRect();
    if (rect.top < box.top) this.list.scrollTop -= box.top - rect.top;
    else if (rect.bottom > box.bottom) this.list.scrollTop += rect.bottom - box.bottom;
  }

  mark(at) {
    if (at === this.at) return;
    this.at = at;
    this.paint();
  }

  move(step) {
    const rows = this.shown();
    if (!rows.length) return;
    this.at = this.at < 0 ? (step > 0 ? 0 : rows.length - 1) : (this.at + step + rows.length) % rows.length;
    this.paint();
  }

  // Tab is the menu's too, so it never pulls the focus out of the field.
  onKeydown(event) {
    if (event.isComposing) return;
    const actions = {
      ArrowDown: () => this.move(1),
      ArrowUp: () => this.move(-1),
      Enter: () => this.shown()[this.at]?.click(),
      Escape: () => {
        if (this.field.value) {
          this.field.value = "";
          this.filter();
          return;
        }
        this.close();
        this.toggle.focus();
      },
      Tab: () => {},
    };
    const action = actions[event.key];
    if (!action) return;
    event.preventDefault();
    action();
  }
}
