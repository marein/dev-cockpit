import { el } from "@dc/dom";
import * as store from "@dc/store";
import * as projectSort from "@dc/project-sort";
import { applyFold } from "@dc/fold";
import { getText } from "@dc/http";

const TAB_KEY = "dc-quicknav-tab";
const FOLD_LIMIT = 5;

// Floating quick nav: jump between live sessions/shells and browse projects. The
// menu content is fetched fresh each time the dropdown opens (background refresh)
// while the in-memory view (open tab, drilled project, expanded groups) is
// re-applied so the items update under the user's current view instead of
// resetting it. The project order is shared with the projects page through
// @dc/project-sort.
class QuickNav extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.menu = this.querySelector(".quicknav-menu[data-quicknav-url]");
    if (!this.menu) {
      return;
    }
    this.toggle = this.querySelector(".quicknav-toggle");
    this.list = this.menu.querySelector(".quicknav-list");
    this.url = this.menu.dataset.quicknavUrl;
    this.spinner = el("div", { class: "quicknav-refresh", role: "status", "aria-label": "Loading" });
    this.inFlight = false;
    this.view = null;

    this.ac = new AbortController();
    this.addEventListener("show.bs.dropdown", () => {
      this.applyState();
      this.refresh();
    }, { signal: this.ac.signal });
    this.addEventListener("click", (event) => this.handleClick(event), { signal: this.ac.signal });
    this.observer = new MutationObserver(() => this.applyState());
    this.observer.observe(this.list, { childList: true });
  }

  disconnectedCallback() {
    this.observer?.disconnect();
    this.observer = null;
    this.ac?.abort();
    this.ac = null;
  }

  reposition() {
    if (window.bootstrap) window.bootstrap.Dropdown.getOrCreateInstance(this.toggle).update();
  }

  refresh() {
    if (this.inFlight) return;
    this.inFlight = true;
    this.menu.appendChild(this.spinner);
    // Background refresh on every open, kept silent on purpose (no toast storm).
    getText(this.url + "?path=" + encodeURIComponent(location.pathname))
      .then((html) => {
        this.list.innerHTML = html;
        this.reposition();
      })
      .catch(() => {})
      .finally(() => {
        this.spinner.remove();
        this.inFlight = false;
      });
  }

  tabsRoot() {
    return this.list.querySelector("[data-quicknav-tabs]");
  }

  initView() {
    const root = this.tabsRoot();
    const current = root ? root.getAttribute("data-quicknav-current-project") || "" : "";
    const savedTab = store.get(TAB_KEY, "");
    // The current project is pre-selected so switching to Projects lands on its
    // assets (without forcing the user onto that tab).
    this.view = {
      tab: savedTab === "projects" ? "projects" : "active",
      project: current || null,
      expanded: new Set(),
    };
  }

  // Collapse a detail group to the first few entries with a "Show N more" toggle,
  // unless the user expanded it (tracked in view.expanded). Re-runs after every
  // background refresh so the server response never overwrites an expand the user
  // made before it arrived.
  foldGroup(group) {
    const key = group.getAttribute("data-qn-fold");
    applyFold(group, {
      limit: FOLD_LIMIT,
      items: Array.from(group.children),
      expanded: this.view.expanded.has(key),
      toggleAttr: "data-qn-fold-toggle",
      toggleClass: "dropdown-item text-center text-secondary small py-1",
      signal: this.ac?.signal,
      onToggle: (event, next) => {
        event.preventDefault();
        event.stopPropagation();
        if (next) this.view.expanded.add(key);
        else this.view.expanded.delete(key);
        this.foldGroup(group);
      },
    });
  }

  applySort(browser) {
    const pbList = browser.querySelector("[data-pb-list]");
    if (pbList) projectSort.sort(pbList);
  }

  applyTab(root, tab) {
    root.querySelectorAll("[data-quicknav-pane]").forEach((pane) => {
      pane.hidden = pane.getAttribute("data-quicknav-pane") !== tab;
    });
    root.querySelectorAll("[data-quicknav-tab]").forEach((btn) => {
      btn.classList.toggle("active", btn.getAttribute("data-quicknav-tab") === tab);
    });
  }

  // Show the drilled project's detail; returns false when that project is no
  // longer present (e.g. it was removed between refreshes).
  showProject(browser, name) {
    let found = false;
    browser.querySelectorAll("[data-pb-detail]").forEach((detail) => {
      const match = detail.getAttribute("data-pb-detail") === name;
      detail.hidden = !match;
      if (match) found = true;
    });
    browser.querySelector("[data-pb-list]").hidden = found;
    return found;
  }

  showList(browser) {
    browser.querySelectorAll("[data-pb-detail]").forEach((detail) => {
      detail.hidden = true;
    });
    browser.querySelector("[data-pb-list]").hidden = false;
  }

  // Re-apply the in-memory view to the (freshly rendered) markup and re-sort the
  // project list. Runs on open and after every background refresh, so the view the
  // user is looking at is preserved while only the items update.
  applyState() {
    const root = this.tabsRoot();
    if (!root) return;
    if (!this.view) this.initView();
    this.applyTab(root, this.view.tab);
    const browser = root.querySelector("[data-project-browser]");
    if (browser) {
      this.applySort(browser);
      if (this.view.project) {
        if (!this.showProject(browser, this.view.project)) this.view.project = null;
      } else {
        this.showList(browser);
      }
    }
    root.querySelectorAll("[data-qn-fold]").forEach((group) => this.foldGroup(group));
  }

  handleClick(event) {
    if (!this.view) this.initView();
    const tab = event.target.closest("[data-quicknav-tab]");
    if (tab) {
      event.preventDefault();
      event.stopPropagation();
      this.view.tab = tab.getAttribute("data-quicknav-tab");
      store.set(TAB_KEY, this.view.tab);
      const root = tab.closest("[data-quicknav-tabs]");
      if (root) this.applyTab(root, this.view.tab);
      return;
    }
    const drill = event.target.closest("[data-pb-drill]");
    if (drill) {
      event.preventDefault();
      event.stopPropagation();
      this.view.project = drill.getAttribute("data-pb-drill");
      const browser = drill.closest("[data-project-browser]");
      if (browser) this.showProject(browser, this.view.project);
      return;
    }
    const back = event.target.closest("[data-pb-back]");
    if (back) {
      event.preventDefault();
      event.stopPropagation();
      this.view.project = null;
      const browser = back.closest("[data-project-browser]");
      if (browser) this.showList(browser);
    }
  }
}

customElements.define("dc-quicknav", QuickNav);
