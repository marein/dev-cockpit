import { escapeHtml } from "@dc/dom";
import { matchesTokens } from "@dc/filter";

const tidy = (text) => text.trim().split(/\s+/).filter(Boolean).join(" ");
const keyOf = (name) => name.toLowerCase();
const counted = (count, one, many) => `${count} ${count === 1 ? one : many}`;

// Settings › Projects › Tags. Every row holds the fields the save posts, the
// tag as loaded and as it stands now, so the form is the plain form and this
// element only stages: a new tag, a new name, a color from the palette, a
// removal with its undo, a merge of the selected rows. A new tag takes the
// next of the colors the server listed, a name a tag has already makes none. Rows whose names end up the same
// merge on save into the first one that kept its name, the rule the server
// applies, and wear its color. The footer counts the rows that changed, a
// save that removes tags asks first through the form's data-confirm.
class TagsSettings extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.form = this.querySelector("[data-tags-form]");
    if (!this.form) return;
    this.ac = new AbortController();
    const { signal } = this.ac;
    this.list = this.querySelector("[data-tags-rows]");
    this.newList = this.querySelector("[data-tags-new]");
    this.newTemplate ??= this.querySelector("[data-tag-new-template]");
    this.next = (this.dataset.nextColors || "").split(" ").filter(Boolean);
    this.created = this.newRows().length;
    this.palette ??= this.querySelector("[data-tag-palette]");
    this.dialog ??= this.querySelector("[data-tags-merge-dialog]");
    this.option ??= this.querySelector("[data-tags-merge-option]");
    this.chip ??= this.querySelector("[data-tag-chip-template]");
    this.filterField = this.querySelector("[data-tags-filter]");
    document.body.append(this.palette, this.dialog);
    this.addEventListener("click", (event) => this.onClick(event), { signal });
    this.addEventListener("input", (event) => this.onInput(event), { signal });
    this.addEventListener("change", (event) => this.onChange(event), { signal });
    this.addEventListener("keydown", (event) => {
      if (event.key === "Enter" && event.target instanceof Element && event.target.matches("[data-tag-name]")) event.preventDefault();
    }, { signal });
    this.palette.addEventListener("click", (event) => {
      const swatch = event.target instanceof Element ? event.target.closest("[data-tag-swatch]") : null;
      if (swatch) this.pick(swatch.dataset.tagSwatch);
    }, { signal });
    document.addEventListener("pointerdown", (event) => {
      if (this.anchor && !this.palette.contains(event.target) && !this.anchor.contains(event.target)) this.closePalette();
    }, { signal, capture: true });
    document.addEventListener("keydown", (event) => {
      if (event.key !== "Escape" || !this.anchor) return;
      event.stopPropagation();
      const anchor = this.anchor;
      this.closePalette();
      anchor.focus();
    }, { signal, capture: true });
    this.querySelector(".dc-work-body")?.addEventListener("scroll", () => this.placePalette(), { signal, passive: true });
    window.addEventListener("resize", () => this.placePalette(), { signal });
    this.dialog.addEventListener("click", (event) => {
      const target = event.target instanceof Element ? event.target : null;
      const swatch = target?.closest("[data-tags-merge-color]");
      if (swatch) {
        this.merge.color = swatch.dataset.tagsMergeColor;
        this.merge.picked = true;
        this.showMerge();
      } else if (target?.closest("[data-tags-merge-apply]")) {
        this.applyMerge();
      }
    }, { signal });
    this.dialog.addEventListener("change", () => {
      const keep = this.dialog.querySelector("[name=tags-merge-keep]:checked");
      if (!keep) return;
      this.merge.keep = this.merge.rows.find((row) => row.dataset.tagRow === keep.value);
      if (!this.merge.picked) this.merge.color = this.shown(this.merge.keep);
      this.showMerge();
    }, { signal });
    this.filter();
    this.update();
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
    window.bootstrap?.Modal.getInstance(this.dialog)?.dispose();
    this.palette?.remove();
    this.dialog?.remove();
  }

  rows() {
    return Array.from(this.list.querySelectorAll("[data-tag-row]"));
  }

  newRows() {
    return Array.from(this.newList.querySelectorAll("[data-tag-new]"));
  }

  addNew() {
    const row = this.newTemplate.content.firstElementChild.cloneNode(true);
    row.querySelector("[data-tag-color-value]").value = this.next.length ? this.next[this.created % this.next.length] : "";
    this.created += 1;
    this.newList.append(row);
    this.update();
    row.querySelector("[data-tag-name]").focus();
  }

  state(row) {
    return {
      name: tidy(row.querySelector("[data-tag-name]").value),
      color: row.querySelector("[data-tag-color-value]").value,
      removed: row.querySelector("[data-tag-remove-value]").value === "1",
      carriers: (row.dataset.carriers || "").split("/").filter(Boolean),
    };
  }

  changed(row) {
    const state = this.state(row);
    return state.removed || state.name !== row.dataset.loadedName || state.color !== row.dataset.loadedColor;
  }

  // survivors answers, for every row a merge takes away, the row it merges
  // into: rows whose names match in any case, one of them changed, go into
  // the first that kept its name, else into the first of them.
  survivors() {
    const groups = new Map();
    for (const row of this.rows()) {
      const state = this.state(row);
      if (state.removed || !state.name) continue;
      const key = keyOf(state.name);
      if (!groups.has(key)) groups.set(key, []);
      groups.get(key).push(row);
    }
    const into = new Map();
    for (const members of groups.values()) {
      if (members.length < 2 || !members.some((row) => this.changed(row))) continue;
      const survivor = members.find((row) => this.state(row).name === row.dataset.loadedName) || members[0];
      for (const row of members) if (row !== survivor) into.set(row, survivor);
    }
    return into;
  }

  // shown is the color a row stands in: a row merging away wears the color
  // of the tag it merges into, which is the one the result keeps.
  shown(row, into = this.survivors()) {
    return this.state(into.get(row) || row).color;
  }

  onClick(event) {
    const target = event.target instanceof Element ? event.target : null;
    if (!target) return;
    const fresh = target.closest("[data-tag-new]");
    if (fresh && target.closest("[data-tag-color]")) {
      const button = target.closest("[data-tag-color]");
      const value = fresh.querySelector("[data-tag-color-value]");
      if (this.anchor === button) this.closePalette();
      else this.openPalette(button, value.value, (color) => { value.value = color; this.update(); });
      return;
    }
    if (fresh && target.closest("[data-tag-drop]")) {
      fresh.remove();
      this.update();
      return;
    }
    if (target.closest("[data-tags-add]")) {
      this.addNew();
      return;
    }
    const row = target.closest("[data-tag-row]");
    if (target.closest("[data-tag-color]") && row) {
      const button = target.closest("[data-tag-color]");
      if (this.anchor === button) this.closePalette();
      else this.openPalette(button, this.shown(row), (color) => this.paint(row, color));
    } else if (target.closest("[data-tag-remove]") && row) {
      this.setRemoved([row], true);
    } else if (target.closest("[data-tag-undo]") && row) {
      this.setRemoved([row], false);
    } else if (target.closest("[data-tags-remove-selected]")) {
      this.setRemoved(this.selected(), true);
      this.select([], false);
    } else if (target.closest("[data-tags-recolor]")) {
      const button = target.closest("[data-tags-recolor]");
      const rows = this.selected().filter((each) => !this.state(each).removed);
      if (this.anchor === button) this.closePalette();
      else if (rows.length) {
        this.openPalette(button, "", (color) => {
          rows.forEach((each) => this.paint(each, color));
          this.select([], false);
        });
      }
    } else if (target.closest("[data-tags-merge]")) {
      this.openMerge();
    } else if (target.closest("[data-tags-discard]")) {
      this.discard();
    } else if (target.closest("[data-tags-filter-clear]")) {
      this.filterField.value = "";
      this.filter();
      this.filterField.focus();
    }
  }

  onInput(event) {
    const target = event.target instanceof Element ? event.target : null;
    if (target?.matches("[data-tag-name]")) this.update();
    else if (target === this.filterField) this.filter();
  }

  onChange(event) {
    const target = event.target instanceof Element ? event.target : null;
    if (target?.matches("[data-tags-all]")) this.select(this.visible(), target.checked);
    else if (target?.matches("[data-tag-select]")) this.update();
  }

  // paint gives a row a color. A row merging away hands it to the tag it
  // merges into, the one whose color lives on.
  paint(row, color) {
    const target = this.survivors().get(row) || row;
    target.querySelector("[data-tag-color-value]").value = color;
    this.update();
  }

  setRemoved(rows, removed) {
    for (const row of rows) row.querySelector("[data-tag-remove-value]").value = removed ? "1" : "";
    this.update();
  }

  discard() {
    this.newRows().forEach((row) => row.remove());
    for (const row of this.rows()) {
      row.querySelector("[data-tag-name]").value = row.dataset.loadedName;
      row.querySelector("[data-tag-color-value]").value = row.dataset.loadedColor;
      row.querySelector("[data-tag-remove-value]").value = "";
      row.querySelector("[data-tag-select]").checked = false;
    }
    this.update();
  }

  visible() {
    return this.rows().filter((row) => !row.hidden);
  }

  selected() {
    return this.rows().filter((row) => row.querySelector("[data-tag-select]").checked);
  }

  select(rows, on) {
    if (rows.length === 0) this.rows().forEach((row) => { row.querySelector("[data-tag-select]").checked = false; });
    for (const row of rows) row.querySelector("[data-tag-select]").checked = on;
    this.update();
  }

  filter() {
    const query = this.filterField.value.trim();
    const rows = this.rows();
    let shown = 0;
    for (const row of rows) {
      row.hidden = query !== "" && !matchesTokens(`${this.state(row).name} ${row.dataset.loadedName}`, query);
      if (!row.hidden) shown += 1;
    }
    this.querySelector("[data-tags-filter-count]").textContent = query === "" ? counted(rows.length, "tag", "tags") : `${shown} of ${rows.length}`;
    this.querySelector("[data-tags-filter-clear]").classList.toggle("d-none", query === "");
    this.querySelector("[data-tags-none]").hidden = shown > 0;
    this.update();
  }

  // update draws what is staged: each row's color, its removal, its merge
  // with the line saying where it goes, the selection with its actions, and
  // the footer with the number of changes and what a save asks first.
  update() {
    const into = this.survivors();
    const members = new Map();
    for (const [row, survivor] of into) {
      if (!members.has(survivor)) members.set(survivor, [survivor]);
      members.get(survivor).push(row);
    }
    let changes = 0;
    let invalid = false;
    const removed = [];
    for (const row of this.rows()) {
      const state = this.state(row);
      const name = row.querySelector("[data-tag-name]");
      const color = this.shown(row, into);
      const dot = row.querySelector("[data-tag-color-dot]");
      dot.className = `dc-tag-dot dc-tag-${color}`;
      row.classList.toggle("is-removed", state.removed);
      name.readOnly = state.removed;
      name.required = !state.removed;
      name.classList.toggle("is-invalid", !state.removed && state.name === "");
      invalid ||= !state.removed && state.name === "";
      row.querySelector("[data-tag-color]").disabled = state.removed;
      const survivor = into.get(row);
      row.classList.toggle("is-merging", Boolean(survivor));
      const hint = row.querySelector("[data-tag-hint]");
      hint.hidden = !survivor;
      if (survivor) {
        const carriers = new Set(members.get(survivor).flatMap((each) => this.state(each).carriers));
        hint.replaceChildren(
          Object.assign(document.createElement("i"), { className: "ti ti-arrow-merge me-1", ariaHidden: "true" }),
          "Merges into ",
          this.chipOf(this.state(survivor).name, this.state(survivor).color),
          ` on save, then ${counted(carriers.size, "project carries", "projects carry")} it.`,
        );
      }
      if (this.changed(row)) changes += 1;
      if (state.removed) removed.push(row);
    }
    const taken = new Set(this.rows().map((row) => this.state(row)).filter((state) => !state.removed && state.name).map((state) => keyOf(state.name)));
    for (const row of this.newRows()) {
      const name = tidy(row.querySelector("[data-tag-name]").value);
      const color = row.querySelector("[data-tag-color-value]").value;
      row.querySelector("[data-tag-color-dot]").className = color ? `dc-tag-dot dc-tag-${color}` : "dc-tag-dot";
      const there = name !== "" && taken.has(keyOf(name));
      const hint = row.querySelector("[data-tag-hint]");
      hint.hidden = !there;
      hint.textContent = there ? "A tag of this name is there already, nothing is made." : "";
      if (name && !there) changes += 1;
      if (name) taken.add(keyOf(name));
    }
    this.querySelector("[data-tags-empty]")?.toggleAttribute("hidden", this.newRows().length > 0);
    const selected = this.selected();
    const visible = this.visible();
    const all = this.querySelector("[data-tags-all]");
    const shownSelected = visible.filter((row) => selected.includes(row)).length;
    all.checked = visible.length > 0 && shownSelected === visible.length;
    all.indeterminate = shownSelected > 0 && shownSelected < visible.length;
    this.querySelector("[data-tags-selected]").textContent = selected.length ? `${selected.length} selected` : "Select all";
    this.querySelector("[data-tags-bulk]").hidden = selected.length === 0;
    this.querySelector("[data-tags-merge]").disabled = selected.filter((row) => !this.state(row).removed).length < 2;
    this.querySelector("[data-tags-recolor]").disabled = selected.every((row) => this.state(row).removed);
    this.querySelector("[data-tags-head]").classList.toggle("is-selecting", selected.length > 0);
    const pending = document.querySelector("[data-tags-pending]");
    if (pending) pending.textContent = changes ? counted(changes, "change", "changes") : "No changes";
    const discard = document.querySelector("[data-tags-discard]");
    if (discard) discard.hidden = changes === 0;
    const save = document.querySelector("[data-tags-save]");
    if (save) save.disabled = changes === 0 || invalid;
    this.ask(removed);
  }

  // ask puts the question a save that removes tags asks on the form, the
  // cockpit's data-confirm, naming the projects they come off. The question
  // becomes the dialog's title, which SweetAlert reads as HTML, so a tag's
  // name is escaped there; the line under it is plain text.
  ask(removed) {
    const form = this.form;
    if (removed.length === 0) {
      delete form.dataset.confirm;
      delete form.dataset.confirmText;
      delete form.dataset.confirmButton;
      return;
    }
    const carriers = [...new Set(removed.flatMap((row) => this.state(row).carriers))].sort();
    const one = removed.length === 1;
    form.dataset.confirm = one ? `Remove tag "${escapeHtml(removed[0].dataset.loadedName)}"?` : `Remove ${removed.length} tags?`;
    form.dataset.confirmText = carriers.length === 0
      ? `No project carries ${one ? "it" : "them"}.`
      : `${one ? "It comes" : "They come"} off ${counted(carriers.length, "project", "projects")}: ${carriers.join(", ")}.`;
    form.dataset.confirmButton = "Remove";
  }

  chipOf(name, color) {
    const chip = this.chip.content.firstElementChild.cloneNode(true);
    chip.classList.add(`dc-tag-${color}`);
    chip.querySelector(".dc-tag-name").textContent = name;
    return chip;
  }

  openPalette(anchor, color, apply) {
    this.closePalette();
    this.anchor = anchor;
    this.paletteApply = apply;
    anchor.setAttribute("aria-expanded", "true");
    for (const swatch of this.palette.querySelectorAll("[data-tag-swatch]")) {
      swatch.setAttribute("aria-pressed", swatch.dataset.tagSwatch === color ? "true" : "false");
    }
    this.palette.classList.add("show");
    this.placePalette();
    if (window.matchMedia("(pointer: fine)").matches) {
      (this.palette.querySelector('[aria-pressed="true"]') || this.palette.querySelector("[data-tag-swatch]"))?.focus();
    }
  }

  // placePalette keeps the palette at its button, under it where there is
  // room, and closes it once the button scrolled out of sight.
  placePalette() {
    if (!this.anchor) return;
    const box = this.anchor.getBoundingClientRect();
    if (box.bottom < 0 || box.top > window.innerHeight) {
      this.closePalette();
      return;
    }
    const size = this.palette.getBoundingClientRect();
    const margin = 6;
    const below = box.bottom + 4 + size.height <= window.innerHeight - margin;
    const top = below ? box.bottom + 4 : Math.max(margin, box.top - 4 - size.height);
    const left = Math.max(margin, Math.min(box.left, window.innerWidth - size.width - margin));
    this.palette.style.top = `${Math.round(top)}px`;
    this.palette.style.left = `${Math.round(left)}px`;
  }

  closePalette() {
    if (!this.anchor) return;
    this.anchor.setAttribute("aria-expanded", "false");
    this.anchor = null;
    this.paletteApply = null;
    this.palette.classList.remove("show");
  }

  pick(color) {
    const apply = this.paletteApply;
    const anchor = this.anchor;
    this.closePalette();
    apply?.(color);
    if (anchor?.isConnected) anchor.focus({ preventScroll: true });
  }

  openMerge() {
    const rows = this.selected().filter((row) => !this.state(row).removed);
    if (rows.length < 2 || !window.bootstrap) return;
    const keep = rows.reduce((best, row) => (this.state(row).carriers.length > this.state(best).carriers.length ? row : best), rows[0]);
    this.merge = { rows, keep, color: this.shown(keep), picked: false };
    this.dialog.querySelector("[data-tags-merge-title]").textContent = `Merge ${rows.length} tags`;
    const names = this.dialog.querySelector("[data-tags-merge-names]");
    names.replaceChildren(...rows.map((row) => {
      const option = this.option.content.firstElementChild.cloneNode(true);
      const state = this.state(row);
      const radio = option.querySelector("input");
      radio.value = row.dataset.tagRow;
      radio.checked = row === keep;
      option.querySelector(".dc-tag").replaceWith(this.chipOf(state.name, this.shown(row)));
      option.querySelector("[data-tags-merge-count]").textContent = counted(state.carriers.length, "project", "projects");
      return option;
    }));
    this.showMerge();
    window.bootstrap.Modal.getOrCreateInstance(this.dialog).show();
  }

  showMerge() {
    const { rows, keep, color } = this.merge;
    for (const swatch of this.dialog.querySelectorAll("[data-tags-merge-color]")) {
      swatch.setAttribute("aria-pressed", swatch.dataset.tagsMergeColor === color ? "true" : "false");
    }
    const carriers = [...new Set(rows.flatMap((row) => this.state(row).carriers))].sort();
    const result = this.dialog.querySelector("[data-tags-merge-result]");
    const chip = this.chipOf(this.state(keep).name, color);
    result.replaceChildren(...(carriers.length === 0
      ? ["Afterwards no project carries ", chip, "."]
      : [`Afterwards ${counted(carriers.length, "project carries", "projects carry")} `, chip, `: ${carriers.join(", ")}.`]));
  }

  // applyMerge stages the merge: every other selected row takes the kept
  // row's name, so it merges into it on save, and the kept row the color.
  applyMerge() {
    const { rows, keep, color } = this.merge;
    const name = keep.querySelector("[data-tag-name]").value;
    for (const row of rows) if (row !== keep) row.querySelector("[data-tag-name]").value = name;
    keep.querySelector("[data-tag-color-value]").value = color;
    window.bootstrap?.Modal.getInstance(this.dialog)?.hide();
    this.select([], false);
  }
}

customElements.define("dc-tags-settings", TagsSettings);
