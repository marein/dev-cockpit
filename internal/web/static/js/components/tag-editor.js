import { matchesTokens } from "@dc/filter";

const tidy = (text) => text.trim().split(/\s+/).filter(Boolean).join(" ");
const keyOf = (name) => name.toLowerCase();

// The tags form's chips. A project's tags are chips holding the fields the
// form posts, a tag in use its id (tag) and a new one its name (new) with the
// color it shows (new-color:<name>), so the save is the plain form and
// nothing here talks to the server. A chip's cross takes it off and offers it
// below again. The field finds a tag among the offered ones by name and
// creates one that is not, and what still stands in it on save is taken as
// meant. A new tag takes the next of the colors the server listed, which is
// the color the save would give it anyway.
class TagEditor extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    const { signal } = this.ac;
    this.input = this.querySelector("[data-tag-input]");
    this.on = this.querySelector("[data-tags-on]");
    this.offered = this.querySelector("[data-tags-offered]");
    this.offeredLabel = this.querySelector("[data-tags-offered-label]");
    this.create = this.querySelector("[data-tag-create]");
    this.palette = (this.dataset.colors || "").split(" ").filter(Boolean);
    this.next = (this.dataset.nextColors || "").split(" ").filter((color) => this.palette.includes(color));
    this.created = 0;
    this.pattern = this.input?.pattern ? new RegExp(`^(?:${this.input.pattern})$`, "u") : null;
    if (!this.input || !this.on || !this.offered || !this.create) return;
    this.input.removeAttribute("name");
    this.addEventListener("click", (event) => this.onClick(event), { signal });
    this.input.addEventListener("input", () => this.onInput(), { signal });
    this.input.addEventListener("keydown", (event) => this.onKey(event), { signal });
    this.closest("form")?.addEventListener("submit", () => this.commit(), { signal });
    this.filter();
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  onClick(event) {
    const button = event.target instanceof Element ? event.target.closest("button") : null;
    if (!button || !this.contains(button)) return;
    if (button.matches("[data-tag-remove]")) this.remove(button.closest("[data-tag-on]"));
    else if (button.matches("[data-tag-offer]")) this.add(button.dataset.tagOffer);
    else if (button === this.create) this.addTyped(tidy(this.input.value));
  }

  // A comma ends a tag the way it did when tags were typed as one line. It is
  // read from the value, not the key, because a phone keyboard often names
  // no key at all.
  onInput() {
    const parts = this.input.value.split(",");
    if (parts.length > 1) {
      const rest = parts.pop();
      if (parts.every((part) => this.addTyped(tidy(part)))) this.input.value = rest;
    }
    this.filter();
  }

  onKey(event) {
    const typed = tidy(this.input.value);
    if (event.key === "Escape" && typed) {
      event.preventDefault();
      event.stopPropagation();
      this.input.value = "";
      this.filter();
    } else if (event.key === "Enter" && typed) {
      event.preventDefault();
      this.commit();
    }
  }

  commit() {
    const typed = tidy(this.input.value);
    if (typed) this.addTyped(typed);
  }

  fits(name) {
    const key = keyOf(name);
    return !this.pattern || this.pattern.test(name)
      || this.chips().some((each) => keyOf(this.nameOf(each)) === key)
      || this.offers().some((each) => keyOf(each.dataset.tagOffer) === key);
  }

  addTyped(name) {
    if (!name) return true;
    if (!this.fits(name)) {
      this.input.value = name;
      this.input.reportValidity();
      return false;
    }
    this.add(name);
    return true;
  }

  chips() {
    return Array.from(this.on.querySelectorAll("[data-tag-on]"));
  }

  nameOf(chip) {
    return chip.querySelector("[data-tag-name]").textContent;
  }

  offers() {
    return Array.from(this.offered.querySelectorAll("[data-tag-offer]"));
  }

  // add puts a tag on the project: one it carries already is left as it is,
  // one that is offered moves up with its id, its spelling and its color, and
  // anything else is new.
  add(name) {
    if (!name) return;
    const key = keyOf(name);
    this.input.value = "";
    if (this.chips().some((each) => keyOf(this.nameOf(each)) === key)) {
      this.filter();
      return;
    }
    const offer = this.offers().find((each) => keyOf(each.dataset.tagOffer) === key);
    let id = "";
    let color = this.next[this.created % this.next.length] || "";
    if (offer) {
      id = offer.dataset.tag;
      name = offer.dataset.tagOffer;
      color = offer.dataset.tagColor;
      offer.remove();
    } else {
      this.created += 1;
    }
    const added = this.fill(this.querySelector("[data-tag-on-template]").content.firstElementChild.cloneNode(true), id, name, color);
    this.on.querySelector("[data-tags-none]")?.before(added);
    this.filter();
  }

  remove(chip) {
    const name = this.nameOf(chip);
    const offer = this.fill(this.querySelector("[data-tag-offer-template]").content.firstElementChild.cloneNode(true), chip.dataset.tag, name, chip.dataset.tagColor);
    const after = this.offers().find((each) => keyOf(each.dataset.tagOffer) > keyOf(name));
    if (after) after.before(offer);
    else this.offered.append(offer);
    chip.remove();
    this.filter();
  }

  // fill names a chip cloned from one of the form's two templates, a tag in
  // use by its id and a new one, without an id, by its name and the color it
  // shows.
  fill(node, id, name, color) {
    node.dataset.tag = id;
    node.querySelector(".dc-tag-name").textContent = name;
    this.shade(node, color);
    if (node.matches("[data-tag-offer]")) {
      node.dataset.tagOffer = name;
      node.title = `Add ${name}`;
      return node;
    }
    const field = node.querySelector("[data-tag-field]");
    field.name = id ? "tag" : "new";
    field.value = id || name;
    const colorField = node.querySelector("[data-tag-color-field]");
    if (id) {
      colorField.removeAttribute("name");
      colorField.value = "";
    } else {
      colorField.name = `new-color:${name}`;
      colorField.value = node.dataset.tagColor;
    }
    const cross = node.querySelector("[data-tag-remove]");
    cross.title = `Remove ${name}`;
    cross.setAttribute("aria-label", `Remove ${name}`);
    return node;
  }

  shade(node, color) {
    const known = this.palette.includes(color);
    node.classList.remove(...this.palette.map((each) => `dc-tag-${each}`));
    if (known) node.classList.add(`dc-tag-${color}`);
    node.dataset.tagColor = known ? color : "";
  }

  // filter narrows the offered tags to what the field holds and says which
  // one Enter takes: the offered tag of exactly that name, else a new one.
  filter() {
    const typed = tidy(this.input.value);
    const key = keyOf(typed);
    let exact = null;
    let shown = 0;
    for (const offer of this.offers()) {
      offer.hidden = typed !== "" && !matchesTokens(offer.dataset.tagOffer, typed);
      offer.toggleAttribute("data-tag-next", false);
      if (!offer.hidden) shown += 1;
      if (keyOf(offer.dataset.tagOffer) === key) exact = offer;
    }
    if (this.offeredLabel) this.offeredLabel.hidden = shown === 0;
    const known = exact || this.chips().some((chip) => keyOf(this.nameOf(chip)) === key);
    this.create.hidden = typed === "" || known;
    this.create.toggleAttribute("data-tag-next", !this.create.hidden);
    this.create.querySelector("[data-tag-create-name]").textContent = typed;
    this.shade(this.create, this.next[this.created % this.next.length] || "");
    exact?.toggleAttribute("data-tag-next", true);
    this.on.querySelector("[data-tags-none]").hidden = this.chips().length > 0;
  }
}

customElements.define("dc-tag-editor", TagEditor);
