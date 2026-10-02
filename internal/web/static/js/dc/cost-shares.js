import { matchesTokens } from "@dc/filter";
import { get, set } from "@dc/store";

const KEY = "dc-costs-sort";
const FOLD = 12;
const FIRST = { usd: "desc", tokens: "desc", name: "asc", share: "desc" };
export const DEFAULT_SORT = "usd-desc";

export function parseSort(value) {
  const [key, dir] = String(value || "").split("-");
  if (!(key in FIRST)) return "";
  return `${key}-${dir === "asc" || dir === "desc" ? dir : FIRST[key]}`;
}

const compare = {
  usd: (a, b) => parseFloat(a.dataset.usd) - parseFloat(b.dataset.usd),
  tokens: (a, b) => parseFloat(a.dataset.tokens) - parseFloat(b.dataset.tokens),
  share: (a, b) => parseFloat(a.dataset.share) - parseFloat(b.dataset.share),
  name: (a, b) => a.dataset.name.localeCompare(b.dataset.name, undefined, { numeric: true, sensitivity: "base" }),
};

export function createShares(root, signal) {
  const open = new Set();
  let sort = parseSort(new URLSearchParams(location.search).get("sort")) || parseSort(get(KEY)) || DEFAULT_SORT;
  let typing = 0;

  const input = () => root.querySelector("[data-cost-query]");
  const query = () => (input()?.value || "").trim();

  function writeURL() {
    const url = new URL(location.href);
    if (sort === DEFAULT_SORT) url.searchParams.delete("sort");
    else url.searchParams.set("sort", sort);
    if (query()) url.searchParams.set("q", query());
    else url.searchParams.delete("q");
    if (url.href !== location.href) history.replaceState(history.state, "", url);
  }

  function order(list) {
    const rows = [...list.children].filter((row) => row.matches("[data-cost-row]"));
    const [key, dir] = sort.split("-");
    const index = new Map(rows.map((row, i) => [row, Number(row.dataset.order ?? i)]));
    rows.forEach((row) => { row.dataset.order = index.get(row); });
    rows.sort((a, b) => (dir === "asc" ? 1 : -1) * compare[key](a, b) || index.get(a) - index.get(b));
    list.append(...rows, ...[...list.children].filter((child) => !child.matches("[data-cost-row]")));
    for (const row of rows) {
      const children = row.querySelector(":scope > [data-cost-children]");
      if (children) order(children);
    }
  }

  function apply(scope) {
    const [key, dir] = sort.split("-");
    for (const button of scope.querySelectorAll("[data-cost-sort] [data-sort]")) {
      const active = button.dataset.sort === key;
      button.setAttribute("aria-pressed", String(active));
      button.classList.toggle("active", active);
      const arrow = button.querySelector(".ti");
      arrow.classList.toggle("ti-arrow-down", active && dir === "desc");
      arrow.classList.toggle("ti-arrow-up", active && dir === "asc");
    }
    const q = query();
    for (const card of scope.querySelectorAll("[data-cost-shares]")) {
      const list = card.querySelector("[data-cost-list]");
      order(list);
      const rows = [...list.children].filter((row) => row.matches("[data-cost-row]"));
      let shown = 0;
      for (const row of rows) {
        const self = !q || matchesTokens(row.dataset.text, q);
        let child = false;
        for (const sub of row.querySelectorAll(":scope > [data-cost-children] > [data-cost-row]")) {
          sub.hidden = !self && !matchesTokens(sub.dataset.text, q);
          child ||= !sub.hidden;
        }
        const match = self || child;
        row.hidden = !match || (!q && !open.has(card.dataset.costShares) && shown >= FOLD);
        if (match) shown += 1;
      }
      const more = card.querySelector("[data-cost-more]");
      more.hidden = Boolean(q) || shown <= FOLD;
      const toggle = more.querySelector("[data-cost-more-toggle]");
      const expanded = open.has(card.dataset.costShares);
      toggle.setAttribute("aria-expanded", String(expanded));
      toggle.querySelector("[data-cost-more-label]").textContent = expanded ? "Show fewer" : `Show all ${shown}`;
      card.querySelector("[data-cost-nomatch]").hidden = !q || shown > 0 || rows.length === 0;
    }
  }

  root.addEventListener("click", (event) => {
    const button = event.target.closest("[data-cost-sort] [data-sort]");
    if (button) {
      const [key, dir] = sort.split("-");
      const next = button.dataset.sort;
      sort = next === key ? `${key}-${dir === "asc" ? "desc" : "asc"}` : `${next}-${FIRST[next]}`;
      set(KEY, sort);
      writeURL();
      apply(root);
      return;
    }
    const more = event.target.closest("[data-cost-more-toggle]");
    if (more) {
      const card = more.closest("[data-cost-shares]").dataset.costShares;
      if (open.has(card)) open.delete(card);
      else open.add(card);
      apply(root);
    }
  }, { signal });
  root.addEventListener("input", (event) => {
    if (!event.target.matches("[data-cost-query]")) return;
    apply(root);
    clearTimeout(typing);
    typing = setTimeout(writeURL, 300);
  }, { signal });
  root.addEventListener("keydown", (event) => {
    if (event.key !== "Escape" || !event.target.matches("[data-cost-query]") || !event.target.value) return;
    event.target.value = "";
    apply(root);
    writeURL();
  }, { signal });
  signal.addEventListener("abort", () => clearTimeout(typing));

  return {
    apply,
    sort: () => sort,
    follow(url) {
      sort = parseSort(url.searchParams.get("sort")) || parseSort(get(KEY)) || DEFAULT_SORT;
    },
  };
}
