// Frontend-only sort for the projects list. Reorders the project cards in place
// by the chosen mode; the mode is kept in localStorage so it stays applied across
// reloads, tabs and restarts. "alpha" sorts by name; "active" puts projects with
// running sessions or shells first (alphabetical), then the rest (alphabetical);
// "recent" orders by last opened (server-tracked), most recent first, projects
// never opened last (alphabetical). Orthogonal to the filter, which only hides
// cards.
(function () {
  const options = Array.from(document.querySelectorAll("[data-project-sort-option]"));
  if (options.length === 0) return;
  const list = document.querySelector(".projects-card .list-group");
  if (!list) return;
  const current = document.querySelector("[data-project-sort-current]");
  const KEY = "dc-project-sort";

  function byName(a, b) {
    const an = a.dataset.projectName.toLowerCase();
    const bn = b.dataset.projectName.toLowerCase();
    return an < bn ? -1 : an > bn ? 1 : 0;
  }

  function apply(mode) {
    const cards = Array.from(list.querySelectorAll("[data-project-name]"));
    cards.sort((a, b) => {
      if (mode === "active") {
        const av = a.dataset.projectActive === "true" ? 0 : 1;
        const bv = b.dataset.projectActive === "true" ? 0 : 1;
        if (av !== bv) return av - bv;
      }
      if (mode === "recent") {
        const au = Number(a.dataset.projectUsed) || 0;
        const bu = Number(b.dataset.projectUsed) || 0;
        if (au !== bu) return bu - au;
      }
      return byName(a, b);
    });
    cards.forEach((card) => list.appendChild(card));
    options.forEach((opt) => opt.classList.toggle("active", opt.dataset.projectSortOption === mode));
    const active = options.find((opt) => opt.dataset.projectSortOption === mode);
    if (current && active) current.textContent = active.textContent.trim();
  }

  let mode = localStorage.getItem(KEY) || "alpha";
  if (!options.some((opt) => opt.dataset.projectSortOption === mode)) mode = "alpha";
  apply(mode);

  options.forEach((opt) => {
    opt.addEventListener("click", () => {
      mode = opt.dataset.projectSortOption;
      localStorage.setItem(KEY, mode);
      apply(mode);
    });
  });
})();
