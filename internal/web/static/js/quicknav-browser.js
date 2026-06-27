(function () {
  "use strict";

  // The open tab is a coarse preference and is remembered across page loads. The
  // project sort order comes from the shared project-sort-compare.js (same key and
  // ordering as the projects page).
  const TAB_KEY = "dc-quicknav-tab";
  const FOLD_LIMIT = 5;

  // Navigation state: the open tab, the drilled-into project, and which detail
  // groups the user expanded. The tab is seeded from (and saved to) localStorage so
  // it survives reloads; the drilled project and the expanded set are memory only,
  // so they survive the dropdown closing and reopening within a page (and the
  // background refresh re-applies them instead of resetting the view) while a real
  // page load starts fresh.
  let view = null;

  function listEl() {
    return document.querySelector(".quicknav-list");
  }

  function tabsRoot() {
    const l = listEl();
    return l && l.querySelector("[data-quicknav-tabs]");
  }

  function initView() {
    const root = tabsRoot();
    const current = root ? root.getAttribute("data-quicknav-current-project") || "" : "";
    const savedTab = localStorage.getItem(TAB_KEY);
    // The current project is pre-selected so switching to Projects lands on its
    // assets (without forcing the user onto that tab).
    view = {
      tab: savedTab === "projects" ? "projects" : "active",
      project: current || null,
      expanded: new Set(),
    };
  }

  // Collapse a detail group to the first few entries with a "Show N more" toggle,
  // unless the user expanded it (tracked in view.expanded). Re-runs after every
  // background refresh so the server response never overwrites an expand the user
  // made before it arrived.
  function foldGroup(group) {
    const key = group.getAttribute("data-qn-fold");
    const existing = group.querySelector(":scope > [data-qn-fold-toggle]");
    if (existing) existing.remove();

    const items = Array.from(group.children);
    if (items.length <= FOLD_LIMIT) {
      items.forEach(function (it) {
        it.classList.remove("d-none");
      });
      return;
    }

    const expanded = view.expanded.has(key);
    const hidden = items.slice(FOLD_LIMIT);
    items.slice(0, FOLD_LIMIT).forEach(function (it) {
      it.classList.remove("d-none");
    });
    hidden.forEach(function (it) {
      it.classList.toggle("d-none", !expanded);
    });

    const toggle = document.createElement("button");
    toggle.type = "button";
    toggle.setAttribute("data-qn-fold-toggle", "");
    toggle.className = "dropdown-item text-center text-secondary small py-1";
    toggle.textContent = expanded ? "Show less" : "Show " + hidden.length + " more";
    group.appendChild(toggle);
  }

  // Reorder the project list to match the projects page sort, via the shared
  // comparator (same key and ordering).
  function applySort(browser) {
    const pbList = browser.querySelector("[data-pb-list]");
    if (pbList && window.dcProjectSort) window.dcProjectSort.sort(pbList);
  }

  function applyTab(root, tab) {
    root.querySelectorAll("[data-quicknav-pane]").forEach(function (p) {
      p.hidden = p.getAttribute("data-quicknav-pane") !== tab;
    });
    root.querySelectorAll("[data-quicknav-tab]").forEach(function (btn) {
      btn.classList.toggle("active", btn.getAttribute("data-quicknav-tab") === tab);
    });
  }

  // Show the drilled project's detail; returns false when that project is no
  // longer present (e.g. it was removed between refreshes).
  function showProject(browser, name) {
    let found = false;
    browser.querySelectorAll("[data-pb-detail]").forEach(function (d) {
      const match = d.getAttribute("data-pb-detail") === name;
      d.hidden = !match;
      if (match) found = true;
    });
    browser.querySelector("[data-pb-list]").hidden = found;
    return found;
  }

  function showList(browser) {
    browser.querySelectorAll("[data-pb-detail]").forEach(function (d) {
      d.hidden = true;
    });
    browser.querySelector("[data-pb-list]").hidden = false;
  }

  // Re-apply the in-memory view to the (freshly rendered) markup and re-sort the
  // project list. Runs on open and after every background refresh, so the view the
  // user is looking at is preserved while only the items update.
  function applyState() {
    const root = tabsRoot();
    if (!root) return;
    if (!view) initView();
    applyTab(root, view.tab);
    const browser = root.querySelector("[data-project-browser]");
    if (browser) {
      applySort(browser);
      if (view.project) {
        if (!showProject(browser, view.project)) view.project = null;
      } else {
        showList(browser);
      }
    }
    root.querySelectorAll("[data-qn-fold]").forEach(foldGroup);
  }

  document.addEventListener("click", function (e) {
    if (!view) initView();
    const tab = e.target.closest("[data-quicknav-tab]");
    if (tab) {
      e.preventDefault();
      e.stopPropagation();
      view.tab = tab.getAttribute("data-quicknav-tab");
      localStorage.setItem(TAB_KEY, view.tab);
      const root = tab.closest("[data-quicknav-tabs]");
      if (root) applyTab(root, view.tab);
      return;
    }
    const drill = e.target.closest("[data-pb-drill]");
    if (drill) {
      e.preventDefault();
      e.stopPropagation();
      view.project = drill.getAttribute("data-pb-drill");
      const browser = drill.closest("[data-project-browser]");
      if (browser) showProject(browser, view.project);
      return;
    }
    const back = e.target.closest("[data-pb-back]");
    if (back) {
      e.preventDefault();
      e.stopPropagation();
      view.project = null;
      const browser = back.closest("[data-project-browser]");
      if (browser) showList(browser);
      return;
    }
    const foldToggle = e.target.closest("[data-qn-fold-toggle]");
    if (foldToggle) {
      e.preventDefault();
      e.stopPropagation();
      const group = foldToggle.closest("[data-qn-fold]");
      if (group) {
        const key = group.getAttribute("data-qn-fold");
        if (view.expanded.has(key)) view.expanded.delete(key);
        else view.expanded.add(key);
        foldGroup(group);
      }
    }
  });

  const l = listEl();
  const navRoot = document.querySelector(".quicknav");
  if (l) {
    // Apply the view the moment the dropdown opens (against the current markup),
    // then again after the refresh swaps in fresh items.
    if (navRoot) navRoot.addEventListener("show.bs.dropdown", applyState);
    new MutationObserver(applyState).observe(l, { childList: true });
  }
})();
