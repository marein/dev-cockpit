class ModelPick extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    const { signal } = this.ac;
    this.select = this.querySelector("[data-model-select]");
    this.other = this.querySelector("[data-model-other-input]");
    if (!this.select) return;
    this.field = this.select.getAttribute("name") || "";
    this.select.addEventListener("change", () => this.onSelect(), { signal });
    if (this.select.closest(".dropdown-menu")) {
      window.addEventListener("keydown", (event) => {
        if (event.target !== this.select) return;
        if (event.key !== "ArrowUp" && event.key !== "ArrowDown") return;
        event.stopPropagation();
      }, { capture: true, signal });
    }
    this.other?.addEventListener("change", () => this.emit(this.other.value.trim()), { signal });
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  otherOption() {
    return this.select?.querySelector("[data-model-other]");
  }

  typing() {
    return Boolean(this.other) && !this.other.hidden;
  }

  get value() {
    if (!this.select) return "";
    return this.typing() ? this.other.value.trim() : this.select.value;
  }

  onSelect() {
    const picked = this.select.selectedOptions[0];
    if (picked && picked.hasAttribute("data-model-other")) {
      this.showOther(true);
      this.other?.focus();
      return;
    }
    this.showOther(false);
    this.emit(this.select.value);
  }

  showOther(on) {
    if (!this.other) return;
    this.other.hidden = !on;
    this.other.disabled = !on;
    if (on) this.select.removeAttribute("name");
    else this.select.setAttribute("name", this.field);
  }

  set(value) {
    if (!this.select) return;
    const other = this.otherOption();
    let option = [...this.select.options].find((entry) => entry !== other && entry.value === value);
    if (!option) {
      option = document.createElement("option");
      option.value = value;
      option.textContent = value;
      this.select.insertBefore(option, other);
    }
    option.selected = true;
    if (other) other.value = value;
    if (this.other) this.other.value = "";
    this.showOther(false);
  }

  emit(value) {
    this.dispatchEvent(new CustomEvent("dc-model-change", { bubbles: true, detail: { value } }));
  }
}

customElements.define("dc-model-pick", ModelPick);
