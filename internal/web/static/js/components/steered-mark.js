import { menuJustClosed, openMenu } from "@dc/contextmenu";
import { syncAnimations } from "@dc/dom";
import { missedServerEvent, onServerEvent } from "@dc/events";
import { getText } from "@dc/http";
import { steeredByItem } from "@dc/steer";

// A mark carrying `menu` stands in a page's head, where no row of the coder
// stands on a phone, so a tap on it is the way to the steering assistant.
class SteeredMark extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    this.inFlight = false;
    this.dirty = false;
    onServerEvent("terminals", () => this.refresh(), { signal: this.ac.signal });
    if (missedServerEvent("terminals")) this.refresh();
    if (!this.hasAttribute("menu")) return;
    const signal = this.ac.signal;
    this.addEventListener("click", (event) => this.openMenu(event), { signal });
    this.addEventListener("contextmenu", (event) => this.openMenu(event), { signal });
    this.addEventListener("keydown", (event) => {
      if (event.key === "Enter" || event.key === " ") this.openMenu(event);
    }, { signal });
    this.syncMenu();
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  syncMenu() {
    if (!this.hasAttribute("menu")) return;
    if (steeredByItem(this)) {
      this.setAttribute("role", "button");
      this.setAttribute("aria-haspopup", "menu");
      this.tabIndex = 0;
      return;
    }
    this.removeAttribute("role");
    this.removeAttribute("aria-haspopup");
    this.removeAttribute("tabindex");
  }

  openMenu(event) {
    const item = steeredByItem(this);
    if (!item) return;
    event.preventDefault();
    if (menuJustClosed()) return;
    const rect = this.getBoundingClientRect();
    openMenu({ x: Math.round(rect.left), y: Math.round(rect.bottom + 4), items: [item], signal: this.ac.signal });
  }

  refresh() {
    if (!this.ac) return;
    const src = this.getAttribute("src");
    if (!src) return;
    if (this.inFlight) {
      this.dirty = true;
      return;
    }
    this.inFlight = true;
    this.dirty = false;
    getText(src, { signal: this.ac.signal })
      .then((html) => {
        this.innerHTML = html;
        syncAnimations(this);
        this.syncMenu();
      })
      .catch(() => {})
      .finally(() => {
        this.inFlight = false;
        if (this.dirty) this.refresh();
      });
  }
}

customElements.define("dc-steered-mark", SteeredMark);
