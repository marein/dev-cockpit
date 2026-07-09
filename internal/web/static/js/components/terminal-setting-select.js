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
    document.addEventListener("terminal-setting-change", (event) => {
      if (event.target === this || event.detail?.setting !== this.setting) return;
      this.currentValue = this.normalizeValue(String(event.detail.value));
      this.updateDisplay();
    }, { signal: this.ac.signal });
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
    this.select = null;
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
    const label = document.createElement("label");
    label.className = "dropdown-item d-flex align-items-center gap-2 terminal-setting-item";
    const icon = document.createElement("i");
    icon.className = `ti ti-${(this.getAttribute("icon") || "").trim()}`;
    icon.setAttribute("aria-hidden", "true");
    const text = document.createElement("span");
    text.className = "flex-fill";
    text.textContent = this.getAttribute("label") || "";
    this.select = document.createElement("select");
    this.select.className = "form-select form-select-sm";
    this.select.setAttribute("aria-label", this.getAttribute("label") || "");
    for (const value of this.options) {
      const option = document.createElement("option");
      option.value = value;
      option.textContent = value;
      this.select.appendChild(option);
    }
    this.select.addEventListener("change", () => this.handleSelect(this.select.value), { signal: this.ac.signal });
    label.append(icon, text, this.select);
    this.replaceChildren(label);
    this.updateDisplay();
  }

  updateDisplay() {
    this.select.value = this.currentValue;
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
