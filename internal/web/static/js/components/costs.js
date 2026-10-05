import { createChart } from "@dc/cost-chart";
import { onServerEvent } from "@dc/events";
import { createPull } from "@dc/pull";

// The cost page's board. The server renders every state of it, this element
// swaps a new one in place: a move to another state through pe.js and the
// costs event alike, keeping the hidden series, the tooltip, the focus and
// the scroll of the work body.

const FOCUS_KEYS = ["data-cost-col", "data-series"];

class Costs extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    const { signal } = this.ac;
    this.chart = createChart(this, signal);
    let pulled = "";
    const pull = createPull(
      () => {
        pulled = location.search;
        return `/costs/board${location.search}`;
      },
      (doc) => {
        if (pulled === location.search) this.swap(doc.body, true);
      },
      signal,
    );
    onServerEvent("costs", pull, { signal });
    this.addEventListener("show.bs.dropdown", (event) => this.fitMenu(event.target), { signal });
    window.addEventListener("pe:navigate", (event) => this.claim(event, new URL(event.detail.url, location.origin)), { signal });
    window.addEventListener("pe:form", (event) => this.claim(event, new URL(event.detail.form.action, location.origin)), { signal });
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  claim(event, target) {
    if (target.pathname !== "/costs" || location.pathname !== "/costs" || !this.isConnected) return;
    const render = event.detail.render;
    event.detail.render = (doc, dom) => {
      const fresh = dom.querySelector("dc-costs");
      if (!fresh) return render(doc, dom);
      doc.title = dom.title || doc.title;
      this.swap(fresh);
    };
    this.classList.add("dc-cost-loading");
    event.detail.finally.push(() => this.classList.remove("dc-cost-loading"));
  }

  swap(fresh, live = false) {
    const focus = this.locate(document.activeElement);
    const tip = this.chart.state();
    const bar = this.querySelector(":scope > [data-cost-toolbar]");
    const freshBar = fresh.querySelector(":scope > [data-cost-toolbar]");
    if (live && bar && freshBar) freshBar.replaceWith(bar);
    this.chart.prepare(fresh);
    this.replaceChildren(...fresh.childNodes);
    this.chart.restore(live ? tip : null);
    if (focus && !this.contains(document.activeElement)) this.querySelector(focus)?.focus({ preventScroll: true });
  }

  // fitMenu caps a menu at the larger room beside its toggle inside the work
  // body, before Popper places it, so Popper flips it to the side it fits.
  fitMenu(toggle) {
    const menu = toggle.parentElement.querySelector(".dropdown-menu");
    const body = this.closest(".dc-work-body");
    if (!menu || !body) return;
    const room = body.getBoundingClientRect();
    const at = toggle.getBoundingClientRect();
    menu.style.setProperty("--dc-cost-menu-room", `${Math.max(at.top - room.top, room.bottom - at.bottom)}px`);
  }

  locate(el) {
    if (!el || !this.contains(el)) return "";
    const attr = FOCUS_KEYS.find((name) => el.hasAttribute(name));
    if (!attr) return "";
    const selector = `[${attr}="${CSS.escape(el.getAttribute(attr))}"]`;
    return el.closest("[data-cost-legend]") ? `[data-cost-legend] ${selector}` : selector;
  }
}

customElements.define("dc-costs", Costs);
