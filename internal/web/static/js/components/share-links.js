import { missedServerEvent, onServerEvent } from "@dc/events";
import { getText } from "@dc/http";
import { openFormModal } from "dc-form-modal";

class ShareLinks extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    this.inFlight = false;
    this.dirty = false;
    onServerEvent("shares", () => this.refresh(), { signal: this.ac.signal });
    if (missedServerEvent("shares")) this.refresh();
    if (this.dataset.open) void this.open(this.dataset.open);
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  async open(url) {
    history.replaceState(null, "", window.location.pathname);
    await customElements.whenDefined("dc-form-modal");
    if (this.ac) openFormModal(url);
  }

  refresh() {
    if (this.inFlight) {
      this.dirty = true;
      return;
    }
    this.inFlight = true;
    const signal = this.ac?.signal;
    getText(window.location.pathname, { signal })
      .then((html) => {
        if (!this.ac) return;
        const fresh = new DOMParser().parseFromString(html, "text/html").querySelector("dc-share-links [data-share-list]");
        const list = this.querySelector("[data-share-list]");
        if (fresh && list) list.replaceWith(fresh);
      })
      .catch(() => {})
      .finally(() => {
        this.inFlight = false;
        if (this.dirty && this.ac) {
          this.dirty = false;
          this.refresh();
        }
      });
  }
}

customElements.define("dc-share-links", ShareLinks);
