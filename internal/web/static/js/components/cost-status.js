import { onServerEvent } from "@dc/events";
import { createPull } from "@dc/pull";

// The status line's cost item. The server renders it into every page, this
// element pulls the same template on the costs event.
class CostStatus extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    const pull = createPull(() => "/costs/status", (doc) => this.apply(doc), this.ac.signal);
    onServerEvent("costs", pull, { signal: this.ac.signal });
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  apply(doc) {
    const fresh = doc.querySelector("dc-cost-status");
    if (!fresh) return;
    this.replaceChildren(...fresh.childNodes);
  }
}

customElements.define("dc-cost-status", CostStatus);
