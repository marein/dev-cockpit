// Shared project sort, used by the projects list page (project-sort.js) and the
// quick nav project browser (quicknav-browser.js). Both sort their own DOM
// container but agree on the order through this one comparator and the shared
// "dc-project-sort" key. Sortable elements carry data-project-name, and for the
// non-alpha modes data-project-active ("true"/"false") and data-project-used (a
// unix timestamp).
//
// Modes: "alpha" by name; "active" puts projects with a running session or shell
// first (then alphabetical); "recent" by last opened, most recent first.
(function () {
  "use strict";

  const KEY = "dc-project-sort";
  const MODES = ["alpha", "active", "recent"];

  function mode() {
    const m = localStorage.getItem(KEY);
    return MODES.indexOf(m) >= 0 ? m : "alpha";
  }

  function byName(a, b) {
    const an = a.dataset.projectName.toLowerCase();
    const bn = b.dataset.projectName.toLowerCase();
    return an < bn ? -1 : an > bn ? 1 : 0;
  }

  function comparator(m) {
    return function (a, b) {
      if (m === "active") {
        const av = a.dataset.projectActive === "true" ? 0 : 1;
        const bv = b.dataset.projectActive === "true" ? 0 : 1;
        if (av !== bv) return av - bv;
      }
      if (m === "recent") {
        const au = Number(a.dataset.projectUsed) || 0;
        const bu = Number(b.dataset.projectUsed) || 0;
        if (au !== bu) return bu - au;
      }
      return byName(a, b);
    };
  }

  // Sort the [data-project-name] children of `container` in place by `m`
  // (defaults to the stored mode), then re-append them in order.
  function sort(container, m) {
    const items = Array.from(container.querySelectorAll("[data-project-name]"));
    items.sort(comparator(m || mode()));
    items.forEach(function (el) {
      container.appendChild(el);
    });
  }

  window.dcProjectSort = { KEY: KEY, MODES: MODES, mode: mode, comparator: comparator, sort: sort };
})();
