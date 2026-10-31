import { followPointer, openedByKeyboard, revealRow } from "@dc/contextmenu";
import { el, syncAnimations } from "@dc/dom";
import { rankRows, rankTokens } from "@dc/filter";
import { applyFold } from "@dc/fold";
import * as projectSort from "@dc/project-sort";

export const RESUME_FOLD_LIMIT = 3;
export const HIT_LIMIT = 30;

const finePointer = window.matchMedia("(pointer: fine)");

const nameOf = (row) => row.dataset.resumeName ?? row.textContent.trim();

export function showMore(row, count = 0) {
  const label = "Show " + count + " more";
  if (count && row.textContent !== label) row.lastChild.textContent = label;
  if (row.hidden !== !count) row.hidden = !count;
}

// NewMenu is the + menu of the tab strip: new coder, new shell and the
// inactive coders to resume, folded per project, under a filter field that
// hides and ranks the rows like the switcher. The keyboard stays in the
// field, so the selection is a class on a row and never the focus.
export class NewMenu {
  constructor(menu, signal) {
    this.menu = menu;
    this.dropdown = menu.closest(".dropdown");
    this.toggle = this.dropdown.querySelector("[data-tabs-new-menu]");
    this.list = menu.querySelector("[data-tabs-new-rows]");
    this.field = menu.querySelector("[data-tabs-new-filter]");
    this.clear = menu.querySelector("[data-tabs-new-filter-clear]");
    this.signal = signal;
    this.open = false;
    this.index = -1;
    this.filtering = false;
    this.expanded = new Set();
    this.uncapped = new Set();
    this.query = "";
    this.prevFocus = null;
    this.refocus = false;
    this.wire();
    this.prepare();
  }

  wire() {
    const signal = this.signal;
    const focus = this.toggle.focus.bind(this.toggle);
    this.toggle.focus = (options) => focus({ preventScroll: true, ...options });
    this.toggle.addEventListener("mousedown", (event) => event.preventDefault(), { signal });
    this.dropdown.addEventListener("show.bs.dropdown", () => {
      this.prevFocus = document.activeElement;
    }, { signal });
    this.dropdown.addEventListener("shown.bs.dropdown", () => {
      this.open = true;
      this.index = openedByKeyboard() ? 0 : -1;
      this.paint();
      if (openedByKeyboard() || finePointer.matches) this.field.focus({ preventScroll: true });
    }, { signal });
    this.dropdown.addEventListener("hide.bs.dropdown", () => {
      this.refocus = this.menu.contains(document.activeElement);
    }, { signal });
    this.dropdown.addEventListener("hidden.bs.dropdown", () => {
      this.open = false;
      this.field.value = "";
      this.filter();
      if (this.refocus && this.prevFocus?.isConnected) this.prevFocus.focus({ preventScroll: true });
    }, { signal });
    this.field.addEventListener("input", () => {
      this.index = 0;
      this.filter();
    }, { signal });
    this.list.addEventListener("contentvisibilityautostatechange", (event) => {
      if (!event.skipped) syncAnimations(event.target);
    }, { capture: true, signal });
    // Bootstrap takes Escape in a menu on the document while capturing and
    // answers it by focusing the toggle, so the field claims its keys on the
    // window first. Keys from anywhere else reach onKeydown through the strip.
    window.addEventListener("keydown", (event) => {
      if (event.target === this.field) this.onKeydown(event);
    }, { capture: true, signal });
    // A click in the menu leaves the keyboard in the field, and a click on the
    // field's row stays there: Bootstrap closes the menu on any other click
    // inside it.
    this.menu.addEventListener("mousedown", (event) => {
      if (event.target !== this.field) event.preventDefault();
    }, { signal });
    this.clear.addEventListener("click", () => {
      this.field.value = "";
      this.filter();
    }, { signal });
    this.field.parentElement.addEventListener("click", (event) => {
      event.stopPropagation();
      this.field.focus({ preventScroll: true });
    }, { signal });
    followPointer(this.menu, {
      signal,
      enter: (row) => this.mark(this.rows().indexOf(row)),
      leave: () => this.mark(-1),
    });
  }

  show() {
    if (!this.toggle.offsetParent || !window.bootstrap?.Dropdown) return false;
    window.bootstrap.Dropdown.getOrCreateInstance(this.toggle).show();
    return true;
  }

  close() {
    window.bootstrap?.Dropdown.getInstance(this.toggle)?.hide();
  }

  swap(fresh) {
    const rows = fresh.querySelector("[data-tabs-new-rows]");
    if (!rows) return;
    this.list.innerHTML = rows.innerHTML;
    this.prepare();
  }

  prepare() {
    for (const node of this.list.querySelectorAll("[data-tabs-resume-projects], [data-tabs-editors]")) projectSort.sort(node);
    this.order = Array.from(this.list.querySelectorAll("[data-tabs-new], [data-resume-id]"));
    this.more = new Map();
    for (const group of this.list.querySelectorAll("[data-tabs-resume-fold]")) {
      this.fold(group);
      if (group.querySelectorAll("[data-resume-id]").length > HIT_LIMIT) this.moreRow(group);
    }
    this.filter();
  }

  // With a query a group shows its best hits only, this row the rest. It
  // stands behind the fold's box, which counts every child as a row.
  moreRow(group) {
    const key = group.getAttribute("data-tabs-resume-fold");
    const row = el("button", {
      type: "button",
      class: "dropdown-item text-center text-secondary small py-1",
      hidden: true,
      "data-tabs-resume-more": key,
    }, el("span"));
    row.addEventListener("click", (event) => {
      event.preventDefault();
      event.stopPropagation();
      this.uncapped.add(key);
      this.filter();
    }, { signal: this.signal });
    group.after(row);
    this.more.set(key, row);
  }

  // A filter suspends the fold, every hit shows.
  fold(group) {
    const key = group.getAttribute("data-tabs-resume-fold");
    applyFold(group, {
      limit: RESUME_FOLD_LIMIT,
      expanded: this.filtering || this.expanded.has(key),
      toggleAttr: "data-tabs-resume-toggle",
      toggleClass: "dropdown-item text-center text-secondary small py-1",
      signal: this.signal,
      onToggle: (event, next) => {
        event.preventDefault();
        event.stopPropagation();
        if (next) this.expanded.add(key);
        else this.expanded.delete(key);
        this.fold(group);
      },
    });
    const toggle = group.querySelector(":scope > [data-tabs-resume-toggle]");
    if (toggle) toggle.hidden = this.filtering;
  }

  filter() {
    const query = this.field.value.trim();
    if (query !== this.query) {
      this.query = query;
      this.uncapped.clear();
    }
    const { shown, rest } = rankRows(this.order, (row) => (query ? rankTokens(nameOf(row), `${nameOf(row)} ${row.dataset.resumeProject ?? ""}`, query) : 0), {
      groupOf: (row) => row.dataset.resumeProject ?? "",
      unitOf: (row) => row.closest("form") ?? row,
      limitOf: (key) => (query && !this.uncapped.has(key) ? HIT_LIMIT : Infinity),
    });
    for (const [key, row] of this.more) showMore(row, rest.get(key));
    // The fold counts the rows in the order they stand, so it waits until an
    // emptied filter has put them back in theirs.
    if (this.filtering !== (query !== "")) {
      this.filtering = query !== "";
      this.list.querySelectorAll("[data-tabs-resume-fold]").forEach((group) => this.fold(group));
    }
    this.clear.style.visibility = query === "" ? "hidden" : "";
    const resumes = shown.some((row) => "resumeId" in row.dataset);
    const creates = shown.some((row) => "tabsNew" in row.dataset);
    for (const head of this.list.querySelectorAll("[data-tabs-resume-head]")) head.hidden = !resumes;
    for (const line of this.list.querySelectorAll("[data-tabs-resume-divider]")) line.hidden = !resumes || !creates;
    for (const project of this.list.querySelectorAll("[data-tabs-resume-projects] > [data-project-name]")) {
      project.hidden = !shown.some((row) => project.contains(row));
    }
    this.list.querySelector("[data-tabs-new-none]").hidden = shown.length > 0;
    this.paint();
    if (this.open) syncAnimations(this.list);
  }

  rows() {
    return Array.from(this.list.querySelectorAll(".dropdown-item")).filter((row) => row.offsetParent);
  }

  paint() {
    for (const row of this.list.querySelectorAll(".dropdown-item.selected")) {
      row.classList.remove("selected");
      row.removeAttribute("aria-current");
    }
    const rows = this.rows();
    if (!this.open || this.index < 0 || !rows.length) return;
    this.index = Math.min(this.index, rows.length - 1);
    const selected = rows[this.index];
    selected.classList.add("selected");
    selected.setAttribute("aria-current", "true");
    revealRow(this.list, selected);
  }

  move(delta) {
    const rows = this.rows();
    if (!rows.length) return;
    if (this.index < 0) this.index = delta > 0 ? 0 : rows.length - 1;
    else this.index = (this.index + delta + rows.length) % rows.length;
    this.paint();
  }

  mark(index) {
    if (index === this.index) return;
    this.index = index;
    this.paint();
  }

  commit() {
    const selected = this.rows()[this.index];
    if (!selected) return;
    selected.click();
    if (this.open) this.paint();
  }

  // Answers whether the key was the menu's. Tab is, so it never pulls the
  // focus out of the field.
  onKeydown(event) {
    const actions = {
      ArrowDown: () => this.move(1),
      ArrowUp: () => this.move(-1),
      Enter: () => this.commit(),
      Escape: () => this.close(),
      Tab: () => {},
    };
    const action = actions[event.key];
    if (!action) return false;
    event.preventDefault();
    event.stopPropagation();
    action();
    return true;
  }
}
