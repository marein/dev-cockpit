import { latestServerEvent, onServerEvent } from "@dc/events";
import { markWorking } from "@dc/dom";

// The working ring of every coder and shell icon on the page, out of the
// activity event. It stands in the rail, the owner's and a guest's, so one
// element serves every app page, and on connect it applies the last working
// set the stream brought, which a swapped page may not show yet.
class Working extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    onServerEvent("activity", (event) => decorate((event.detail && event.detail.since) || {}), { signal: this.ac.signal });
    const latest = latestServerEvent("activity");
    if (latest) decorate(latest.since || {});
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }
}

function decorate(since) {
  document.querySelectorAll("[data-notify-target]:not(.assistant)").forEach((icon) => {
    markWorking(icon, since[icon.getAttribute("data-notify-target")] || 0);
  });
  document.querySelectorAll("[data-notify-targets]").forEach((icon) => {
    const marks = (icon.getAttribute("data-notify-targets") || "").split(" ").map((id) => since[id]).filter(Boolean);
    markWorking(icon, marks.length ? Math.min(...marks) : 0);
  });
}

customElements.define("dc-working", Working);
