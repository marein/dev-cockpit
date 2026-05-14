(() => {
  class SessionTerminalSettingSelect extends HTMLElement {
    connectedCallback() {
      if (this.select) {
        return;
      }
      this.setting = (this.getAttribute("setting") || "").trim();
      this.storageKey = (this.getAttribute("storage-key") || "").trim();
      this.options = this.readOptions();
      this.defaultValue = this.normalizeValue(this.getAttribute("default-value") || "", this.options[0] || "");
      this.render();
      this.select = this.querySelector("select");
      this.select.addEventListener("change", () => this.handleChange());
      const initialValue = this.restoreValue();
      this.select.value = initialValue;
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
      if (!this.storageKey) {
        return "";
      }
      try {
        return window.localStorage.getItem(this.storageKey) || "";
      } catch (error) {
        void error;
        return "";
      }
    }

    storeValue(value) {
      if (!this.storageKey) {
        return;
      }
      try {
        window.localStorage.setItem(this.storageKey, value);
      } catch (error) {
        void error;
      }
    }

    restoreValue() {
      const value = this.normalizeValue(this.readStoredValue(), this.defaultValue);
      this.storeValue(value);
      return value;
    }

    render() {
      const label = document.createElement("label");
      label.className = "d-flex align-items-center gap-1 mb-0";
      label.title = this.getAttribute("title") || "";

      const icon = document.createElement("i");
      icon.className = `ti ti-${(this.getAttribute("icon") || "").trim()} text-secondary`;
      icon.setAttribute("aria-hidden", "true");

      const select = document.createElement("select");
      select.className = "form-select form-select-sm w-auto";
      select.setAttribute("aria-label", this.getAttribute("label") || "");

      for (const value of this.options) {
        const option = document.createElement("option");
        option.value = value;
        option.textContent = value;
        select.appendChild(option);
      }

      label.append(icon, select);
      this.style.display = "inline-flex";
      this.style.flex = "0 0 auto";
      this.replaceChildren(label);
    }

    handleChange() {
      const value = this.normalizeValue(this.select.value, this.defaultValue);
      this.select.value = value;
      this.storeValue(value);
      this.dispatchEvent(new CustomEvent("session-terminal-setting-change", {
        bubbles: true,
        composed: true,
        detail: {
          setting: this.setting,
          value: parseInt(value, 10),
        },
      }));
    }

    get value() {
      return parseInt(this.select?.value || this.defaultValue, 10);
    }
  }

  customElements.define("session-terminal-setting-select", SessionTerminalSettingSelect);
})();
