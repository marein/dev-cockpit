import { setThemePreference, themePreference } from "@dc/theme";

class ThemeSwitch extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    const { signal } = this.ac;
    this.addEventListener("click", (event) => {
      const button = event.target instanceof Element && event.target.closest("[data-theme-option]");
      if (!button || !this.contains(button)) return;
      setThemePreference(button.getAttribute("data-theme-option"));
    }, { signal });
    document.addEventListener("dc:theme", () => this.sync(), { signal });
    this.sync();
    this.settle();
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  settle() {
    if (!this.hasAttribute("data-boot")) return;
    void getComputedStyle(this, "::before").translate;
    this.removeAttribute("data-boot");
  }

  sync() {
    const preference = themePreference();
    for (const button of this.querySelectorAll("[data-theme-option]")) {
      const active = button.getAttribute("data-theme-option") === preference;
      button.classList.toggle("active", active);
      button.setAttribute("aria-pressed", active ? "true" : "false");
    }
  }
}

customElements.define("dc-theme-switch", ThemeSwitch);
