import { get, set } from "@dc/store";

class TerminalSettingSelect extends HTMLElement {
  connectedCallback() {
    if (this.ac) {
      return;
    }
    this.ac = new AbortController();
    this.setting = (this.getAttribute("setting") || "").trim();
    this.storageKey = (this.getAttribute("storage-key") || "").trim();
    this.options = this.readOptions();
    this.defaultValue = this.normalizeValue(this.getAttribute("default-value") || "", this.options[0] || "");
    this.currentValue = this.restoreValue();
    this.render();
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
    this.current = null;
    this.items = null;
  }

  readOptions() {
    return (this.getAttribute("options") || "")
      .split(",")
      .map((value) => value.trim())
      .filter((value) => value !== "");
  }

  normalizeValue(value, fallback = this.defaultValue) {
    const normalized = String(parseInt(value, 10) || "");
    if (this.options?.includes(normalized)) {
      return normalized;
    }
    return fallback;
  }

  readStoredValue() {
    return this.storageKey ? get(this.storageKey, "") : "";
  }

  storeValue(value) {
    if (this.storageKey) {
      set(this.storageKey, value);
    }
  }

  restoreValue() {
    const value = this.normalizeValue(this.readStoredValue(), this.defaultValue);
    this.storeValue(value);
    return value;
  }

  render() {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "btn btn-sm dropdown-toggle";
    button.setAttribute("data-bs-toggle", "dropdown");
    button.setAttribute("data-bs-auto-close", "true");
    button.setAttribute("aria-expanded", "false");
    button.setAttribute("aria-label", this.getAttribute("label") || "");
    button.title = this.getAttribute("title") || "";

    const icon = document.createElement("i");
    icon.className = `ti ti-${(this.getAttribute("icon") || "").trim()} me-1`;
    icon.setAttribute("aria-hidden", "true");

    this.current = document.createElement("span");

    const menu = document.createElement("div");
    menu.className = "dropdown-menu dropdown-menu-end";
    menu.style.maxHeight = "50vh";
    menu.style.overflowY = "auto";

    this.items = this.options.map((value) => {
      const item = document.createElement("button");
      item.type = "button";
      item.className = "dropdown-item";
      item.dataset.value = value;
      item.textContent = value;
      item.addEventListener("click", () => this.handleSelect(value), { signal: this.ac.signal });
      menu.appendChild(item);
      return item;
    });

    button.append(icon, this.current);
    this.classList.add("dropdown");
    this.style.display = "inline-flex";
    this.style.flex = "0 0 auto";
    this.replaceChildren(button, menu);
    this.updateDisplay();
  }

  updateDisplay() {
    this.current.textContent = this.currentValue;
    this.items.forEach((item) => item.classList.toggle("active", item.dataset.value === this.currentValue));
  }

  handleSelect(value) {
    const normalized = this.normalizeValue(value, this.defaultValue);
    if (normalized === this.currentValue) {
      return;
    }
    this.currentValue = normalized;
    this.storeValue(normalized);
    this.updateDisplay();
    this.dispatchEvent(new CustomEvent("terminal-setting-change", {
      bubbles: true,
      composed: true,
      detail: {
        setting: this.setting,
        value: parseInt(normalized, 10),
      },
    }));
  }

  get value() {
    return parseInt(this.currentValue || this.defaultValue, 10);
  }
}

customElements.define("terminal-setting-select", TerminalSettingSelect);
