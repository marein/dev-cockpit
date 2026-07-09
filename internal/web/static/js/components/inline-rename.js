import { postForm, ensureOk } from "@dc/http";
import { notifyError } from "@dc/toast";

// Inline rename: click the label to edit, Enter/blur saves via POST, Escape
// cancels. Generic over the target via the rename-url attribute and an optional
// document.title suffix kept in sync with the name.
class InlineRename extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    const signal = this.ac.signal;
    this.url = this.getAttribute("rename-url");
    this.titleSuffix = this.getAttribute("title-suffix") ?? "";
    this.label = this.querySelector("[data-rename-label]");
    this.input = this.querySelector("[data-rename-input]");
    if (!this.url || !this.label || !this.input) {
      return;
    }
    this.editing = false;
    this.saving = false;

    this.label.addEventListener("click", () => this.showInput(), { signal });
    this.label.addEventListener("keydown", (event) => {
      if (event.key === "Enter" || event.key === " ") {
        event.preventDefault();
        this.showInput();
      }
    }, { signal });
    this.input.addEventListener("keydown", (event) => {
      if (event.key === "Enter") {
        event.preventDefault();
        void this.save();
      } else if (event.key === "Escape") {
        event.preventDefault();
        this.input.value = this.label.textContent.trim();
        this.showLabel();
      }
    }, { signal });
    this.input.addEventListener("blur", () => {
      if (this.editing && !this.saving) {
        void this.save();
      }
    }, { signal });
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  showLabel() {
    this.input.classList.add("d-none");
    this.label.classList.remove("d-none");
    this.editing = false;
  }

  showInput() {
    if (this.editing) {
      return;
    }
    this.editing = true;
    this.input.value = this.label.textContent.trim();
    this.label.classList.add("d-none");
    this.input.classList.remove("d-none");
    this.input.focus();
    this.input.select();
  }

  applyName(name) {
    this.label.textContent = name;
    this.input.value = name;
    if (this.titleSuffix) {
      document.title = name + this.titleSuffix;
    }
    document.dispatchEvent(new CustomEvent("dc-renamed", { detail: { url: this.url, name } }));
  }

  async save() {
    if (this.saving) {
      return;
    }
    const name = this.input.value.trim();
    if (name === "" || name === this.label.textContent.trim()) {
      this.showLabel();
      return;
    }
    this.saving = true;
    try {
      const response = await postForm(this.url, { name });
      await ensureOk(response, "Could not rename.");
      const payload = await response.json().catch(() => ({}));
      this.applyName(payload.name || name);
    } catch (error) {
      this.input.value = this.label.textContent.trim();
      notifyError(error.message || "Could not rename.");
    } finally {
      this.saving = false;
      this.showLabel();
    }
  }
}

customElements.define("dc-inline-rename", InlineRename);
