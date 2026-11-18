import { latestServerEvent, missedServerEvent, onServerEvent } from "@dc/events";
import { appPath, getText } from "@dc/http";

// A live region, pulled again when the plugins event names a version it does
// not show. A child whose data-region-key stays is kept, so a mounted element
// keeps its state. A live plugin's elements are not in the import map, fixed
// at page load, so the region imports the module each mount names.
class PluginRegion extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    this.inFlight = false;
    this.dirty = false;
    onServerEvent("plugins", (event) => this.follow(event.detail), { signal: this.ac.signal });
    if (missedServerEvent("plugins")) this.follow(latestServerEvent("plugins"));
    this.define();
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  follow(data) {
    if (data?.version && data.version === this.dataset.version) return;
    this.refresh();
  }

  refresh() {
    if (this.inFlight) {
      this.dirty = true;
      return;
    }
    this.inFlight = true;
    const query = this.dataset.tab ? `?tab=${encodeURIComponent(this.dataset.tab)}` : "";
    getText(appPath(`/plugin-regions/${encodeURIComponent(this.dataset.region)}${query}`), { signal: this.ac?.signal })
      .then((html) => {
        if (!this.ac) return;
        const fresh = new DOMParser().parseFromString(html, "text/html").querySelector("dc-plugin-region");
        if (fresh) this.apply(fresh);
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

  apply(fresh) {
    this.dataset.version = fresh.dataset.version || "";
    const held = new Map([...this.children].map((child) => [child.dataset.regionKey, child]));
    const next = [...fresh.children].map((child) => held.get(child.dataset.regionKey) ?? document.importNode(child, true));
    const same = next.length === this.children.length && next.every((child, i) => child === this.children[i]);
    if (!same) this.replaceChildren(...next);
    this.define();
  }

  define() {
    const modules = new Set([...this.querySelectorAll("[data-dc-module]")].map((node) => node.dataset.dcModule));
    modules.forEach((url) => { import(url).catch(() => {}); });
  }
}

customElements.define("dc-plugin-region", PluginRegion);
