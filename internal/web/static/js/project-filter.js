// Live, frontend-only filter for the projects list. Typing hides/shows project
// cards by name; the term is kept in localStorage so it stays applied across
// reloads, tabs and restarts. An active filter is highlighted (border + count +
// clear X / link) so it is obvious the list is filtered. Clearable via the X,
// the "Clear filter" link, or Escape.
(function () {
  const input = document.querySelector("[data-project-filter]");
  if (!input) return;
  const empty = document.querySelector("[data-project-filter-empty]");
  const meta = document.querySelector("[data-project-filter-meta]");
  const count = document.querySelector("[data-project-filter-count]");
  const xBtn = document.querySelector(".input-icon [data-project-filter-clear]");
  const KEY = "dc-project-filter";

  function apply(query) {
    const needle = query.trim().toLowerCase();
    const cards = document.querySelectorAll("[data-project-name]");
    let visible = 0;
    cards.forEach((card) => {
      const hit = needle === "" || card.dataset.projectName.toLowerCase().includes(needle);
      card.classList.toggle("d-none", !hit);
      if (hit) visible += 1;
    });
    const active = needle !== "";
    input.classList.toggle("border-primary", active);
    if (empty) empty.classList.toggle("d-none", visible !== 0);
    if (xBtn) xBtn.classList.toggle("d-none", !active);
    if (meta) meta.classList.toggle("d-none", !active);
    if (count) count.textContent = active ? `${visible} of ${cards.length}` : "";
  }

  function set(value) {
    input.value = value;
    localStorage.setItem(KEY, value);
    apply(value);
  }

  input.value = localStorage.getItem(KEY) || "";
  apply(input.value);

  // Explicit navigation to a specific project wins over a saved filter: if we
  // landed on a #project-… anchor that the filter would hide, drop the filter so
  // the target is visible, then scroll to it (it had no position while hidden).
  if (location.hash.startsWith("#project-")) {
    const target = document.getElementById(location.hash.slice(1));
    const needle = input.value.trim().toLowerCase();
    if (target && needle && !(target.dataset.projectName || "").toLowerCase().includes(needle)) {
      set("");
      target.scrollIntoView();
    }
  }

  input.addEventListener("input", () => set(input.value));
  input.addEventListener("keydown", (e) => {
    if (e.key === "Escape") set("");
  });
  document.querySelectorAll("[data-project-filter-clear]").forEach((el) => {
    el.addEventListener("click", (e) => {
      e.preventDefault();
      set("");
      input.focus();
    });
  });
})();
