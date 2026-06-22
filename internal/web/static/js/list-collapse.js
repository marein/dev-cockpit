// Collapses session/shell lists in the project cards to the first few entries
// with a "Show N more" toggle. Frontend-only and idempotent: re-runs on load and
// whenever a card/list changes (ajax delete/stop) via the "dc:rendered" event, so
// the toggle and counts stay correct as rows come and go.
(function () {
  const LIMIT = 5;

  function collapse(list) {
    const existing = list.querySelector(":scope > [data-collapse-toggle]");
    if (existing) existing.remove();

    const items = Array.from(list.children).filter(
      (el) => el.classList.contains("list-group-item") && !el.dataset.collapseToggle
    );
    if (items.length <= LIMIT) {
      items.forEach((it) => it.classList.remove("d-none"));
      return;
    }

    const expanded = list.dataset.collapseExpanded === "1";
    const hidden = items.slice(LIMIT);
    items.slice(0, LIMIT).forEach((it) => it.classList.remove("d-none"));
    hidden.forEach((it) => it.classList.toggle("d-none", !expanded));

    const toggle = document.createElement("button");
    toggle.type = "button";
    toggle.dataset.collapseToggle = "1";
    toggle.className =
      "list-group-item list-group-item-action text-center text-secondary small py-1 bg-transparent border-0";
    toggle.textContent = expanded ? "Show less" : "Show " + hidden.length + " more";
    toggle.addEventListener("click", () => {
      list.dataset.collapseExpanded = expanded ? "" : "1";
      collapse(list);
    });
    list.appendChild(toggle);
  }

  function process(scope) {
    const root = scope && scope.querySelectorAll ? scope : document;
    root.querySelectorAll("[data-collapse-list]").forEach(collapse);
  }

  process(document);
  document.addEventListener("dc:rendered", (e) => process(e.detail && e.detail.root));
})();
