// Frontend-only sort for the projects list. Reorders the project cards in place
// by the chosen mode; the mode is kept in localStorage so it stays applied across
// reloads, tabs and restarts. The order itself lives in project-sort-compare.js,
// shared with the quick nav. Orthogonal to the filter, which only hides cards.
(function () {
  const options = Array.from(document.querySelectorAll("[data-project-sort-option]"));
  if (options.length === 0) return;
  const list = document.querySelector(".projects-card .list-group");
  if (!list) return;
  const S = window.dcProjectSort;
  if (!S) return;
  const current = document.querySelector("[data-project-sort-current]");

  function apply(mode) {
    S.sort(list, mode);
    options.forEach((opt) => opt.classList.toggle("active", opt.dataset.projectSortOption === mode));
    const active = options.find((opt) => opt.dataset.projectSortOption === mode);
    if (current && active) current.textContent = active.textContent.trim();
  }

  apply(S.mode());

  options.forEach((opt) => {
    opt.addEventListener("click", () => {
      const mode = opt.dataset.projectSortOption;
      localStorage.setItem(S.KEY, mode);
      apply(mode);
    });
  });
})();
