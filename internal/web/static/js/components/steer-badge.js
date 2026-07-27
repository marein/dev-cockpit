import { onServerEvent } from "@dc/events";
import { getText } from "@dc/http";

class SteerBadge extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    this.inFlight = false;
    this.dirty = false;
    onServerEvent("assistant", () => this.refresh(), { signal: this.ac.signal });
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  // A badge whose view was swapped away mid-fetch is gone: the finally below
  // re-enters, and without this the aborted element would reach for its own
  // torn down controller.
  refresh() {
    if (!this.ac) return;
    const src = this.getAttribute("src") || "/assistant/jobs";
    if (this.inFlight) {
      this.dirty = true;
      return;
    }
    this.inFlight = true;
    this.dirty = false;
    getText(src, { signal: this.ac.signal })
      .then((html) => {
        const holder = document.createElement("div");
        holder.innerHTML = html;
        const open = Number(holder.querySelector("[data-assistant-body]")?.dataset.assistantJobsOpen || 0);
        this.textContent = String(open);
        this.classList.toggle("d-none", open === 0);
      })
      .catch(() => {})
      .finally(() => {
        this.inFlight = false;
        if (this.dirty) this.refresh();
      });
  }
}

customElements.define("dc-steer-badge", SteerBadge);
