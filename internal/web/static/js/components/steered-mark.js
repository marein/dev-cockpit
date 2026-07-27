import { onServerEvent } from "@dc/events";
import { getText } from "@dc/http";

class SteeredMark extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    this.inFlight = false;
    this.dirty = false;
    onServerEvent("terminals", () => this.refresh(), { signal: this.ac.signal });
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  refresh() {
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
      })
      .catch(() => {})
      .finally(() => {
        this.inFlight = false;
        if (this.dirty) this.refresh();
      });
  }
}

customElements.define("dc-steered-mark", SteeredMark);
