import { createChart } from "@dc/cost-chart";
import { createShares, DEFAULT_SORT } from "@dc/cost-shares";
import { onServerEvent } from "@dc/events";
import { createPull } from "@dc/pull";

// The cost page's board. The server renders every state of it, this element
// swaps a new one in place: a move to another state through pe.js and the
// costs event alike, keeping the text filter, the sort, the hidden series,
// the tooltip, the focus and the scroll of the work body.

const FOCUS_KEYS = ["data-cost-col", "data-series", "data-sort", "data-cost-by", "data-cost-unit", "data-cost-chip", "data-cost-more-toggle", "data-cost-drill"];

const serverState = (search) => {
  const params = new URLSearchParams(search);
  params.delete("sort");
  params.delete("q");
  return params.toString();
};

class Costs extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    const { signal } = this.ac;
    this.chart = createChart(this, signal);
    this.shares = createShares(this, signal);
    this.shares.apply(this);
    let pulled = "";
    const pull = createPull(
      () => {
        pulled = serverState(location.search);
        return `/costs/board${location.search}`;
      },
      (doc) => {
        if (pulled === serverState(location.search)) this.swap(doc.body, { live: true });
      },
      signal,
    );
    onServerEvent("costs", pull, { signal });
    window.addEventListener("pe:navigate", (event) => this.claim(event, new URL(event.detail.url, location.origin)), { signal });
    window.addEventListener("pe:form", (event) => this.claim(event, new URL(event.detail.form.action, location.origin)), { signal });
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  claim(event, target) {
    if (target.pathname !== "/costs" || location.pathname !== "/costs" || !this.isConnected) return;
    const back = event.type === "pe:navigate" && event.detail.url === location.href;
    const sort = this.shares.sort();
    const query = (this.querySelector("[data-cost-query]")?.value || "").trim();
    const render = event.detail.render;
    event.detail.render = (doc, dom) => {
      const fresh = dom.querySelector("dc-costs");
      if (!fresh) return render(doc, dom);
      doc.title = dom.title || doc.title;
      if (back) this.shares.follow(target);
      this.swap(fresh, { back });
    };
    this.classList.add("dc-cost-loading");
    event.detail.finally.push(() => this.classList.remove("dc-cost-loading"));
    if (back) return;
    event.detail.succeed.push(() => {
      const url = new URL(location.href);
      if (sort !== DEFAULT_SORT) url.searchParams.set("sort", sort);
      if (query) url.searchParams.set("q", query);
      history.replaceState(history.state, "", url);
    });
  }

  swap(fresh, { live = false, back = false }) {
    const focus = this.locate(document.activeElement);
    const tip = this.chart.state();
    const bar = this.querySelector(":scope > [data-cost-toolbar]");
    const freshBar = fresh.querySelector(":scope > [data-cost-toolbar]");
    if (bar && freshBar) {
      if (!live) {
        bar.firstElementChild.replaceWith(freshBar.firstElementChild);
        if (back) bar.querySelector("[data-cost-query]").value = freshBar.querySelector("[data-cost-query]").value;
      }
      freshBar.remove();
    }
    this.chart.prepare(fresh);
    this.shares.apply(fresh);
    if (bar && freshBar) {
      for (const node of [...this.childNodes]) if (node !== bar) node.remove();
      bar.after(...fresh.childNodes);
    } else {
      this.replaceChildren(...fresh.childNodes);
    }
    this.shares.apply(this);
    this.chart.restore(tip);
    if (focus && !this.contains(document.activeElement)) this.querySelector(focus)?.focus({ preventScroll: true });
  }

  locate(el) {
    if (!el || !this.contains(el)) return "";
    const attr = FOCUS_KEYS.find((name) => el.hasAttribute(name));
    if (!attr) return "";
    let selector = `[${attr}="${CSS.escape(el.getAttribute(attr))}"]`;
    const row = el.closest("[data-cost-row]");
    if (row) selector = `[data-cost-row][data-name="${CSS.escape(row.dataset.name)}"] > ${selector}`;
    const card = el.closest("[data-cost-shares]");
    if (card) selector = `[data-cost-shares="${CSS.escape(card.dataset.costShares)}"] ${selector}`;
    else if (el.closest("[data-cost-legend]")) selector = `[data-cost-legend] ${selector}`;
    return selector;
  }
}

customElements.define("dc-costs", Costs);
