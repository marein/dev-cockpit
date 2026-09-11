import { setThemePreference, themePreference } from "@dc/theme";

const ORDER = ["light", "dark", "auto"];
const LABELS = { light: "Light", dark: "Dark", auto: "Follow the OS" };
const NEXT_HINTS = { light: "Click for light.", dark: "Click for dark.", auto: "Click to follow the OS." };

const nextPreference = (preference) => ORDER[(ORDER.indexOf(preference) + 1) % ORDER.length];

class ThemeCycle extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    const { signal } = this.ac;
    this.addEventListener("click", (event) => {
      const button = event.target instanceof Element && event.target.closest("[data-theme-cycle]");
      if (!button || !this.contains(button)) return;
      const preference = nextPreference(themePreference());
      setThemePreference(preference);
    }, { signal });
    document.addEventListener("dc:theme", () => this.sync(), { signal });
    this.sync();
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  sync() {
    const preference = themePreference();
    for (const mark of this.querySelectorAll("[data-theme-mark]")) {
      mark.hidden = mark.getAttribute("data-theme-mark") !== preference;
    }
    for (const label of this.querySelectorAll("[data-theme-label]")) {
      label.textContent = LABELS[preference] || preference;
    }
    const button = this.querySelector("[data-theme-cycle]");
    if (button) {
      const title = `Theme: ${LABELS[preference]}. ${NEXT_HINTS[nextPreference(preference)]}`;
      button.setAttribute("title", title);
      button.setAttribute("aria-label", title);
    }
  }
}

customElements.define("dc-theme-cycle", ThemeCycle);
