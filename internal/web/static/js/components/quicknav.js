import { el } from "@dc/dom";
import * as store from "@dc/store";
import * as projectSort from "@dc/project-sort";
import { applyFold } from "@dc/fold";
import { ensureOk, getText, postJSON } from "@dc/http";
import { notifyError } from "@dc/toast";

const TAB_KEY = "dc-quicknav-tab";
const FOLD_LIMIT = 5;
const DRAG_THRESHOLD = 6;
const EDGE_ZONE = 28;
const EDGE_STEP = 10;

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
    this.drag = null;
    this.suppressClick = false;

    this.ac = new AbortController();
    const signal = this.ac.signal;
    this.addEventListener("show.bs.dropdown", () => {
      this.applyState();
      this.refresh();
    }, { signal });
    this.addEventListener("click", (event) => this.handleClick(event), { signal });
    // Capture so a click synthesised right after a drag, or a plain tap on the
    // grip handle, never reaches pe.js and navigates the row.
    this.addEventListener("click", (event) => {
      if (!this.suppressClick && !event.target.closest("[data-qn-drag-handle]")) return;
      this.suppressClick = false;
      event.preventDefault();
      event.stopPropagation();
    }, { signal, capture: true });
    this.list.addEventListener("dragstart", (event) => event.preventDefault(), { signal });
    this.list.addEventListener("pointerdown", (event) => this.onPointerDown(event), { signal });
    this.list.addEventListener("pointermove", (event) => this.onPointerMove(event), { signal });
    this.list.addEventListener("pointerup", (event) => this.onPointerUp(event), { signal });
    this.list.addEventListener("pointercancel", () => this.cancelDrag(), { signal });
    this.observer = new MutationObserver(() => this.applyState());
    this.observer.observe(this.list, { childList: true });
  }

  disconnectedCallback() {
    this.cancelDrag();
    this.observer?.disconnect();
    this.observer = null;
    this.ac?.abort();
    this.ac = null;
  }

  reposition() {
    if (window.bootstrap) window.bootstrap.Dropdown.getOrCreateInstance(this.toggle).update();
  }

  activeList() {
    return this.list.querySelector("[data-quicknav-active-list]");
  }

  dragItems() {
    const list = this.activeList();
    return list ? Array.from(list.querySelectorAll(".quicknav-active-item")) : [];
  }

  // Content Y measured against the scrollable menu, so edge auto-scroll keeps the
  // math consistent while the list moves under the pointer.
  contentY(clientY) {
    return clientY - this.menu.getBoundingClientRect().top + this.menu.scrollTop;
  }

  // Persist the current active-list order to the same cross-device @dc_tab_pos
  // state the tab strip writes, so a drag in either place agrees.
  persistOrder() {
    const ids = this.dragItems().map((item) => item.dataset.tabId);
    postJSON("/terminal-tabs/order", { ids })
      .then((response) => ensureOk(response, "Could not save the order."))
      .catch((error) => notifyError(error.message));
  }

  onPointerDown(event) {
    if (event.button !== 0 || this.drag) return;
    const item = event.target.closest(".quicknav-active-item");
    if (!item || !this.activeList()?.contains(item)) return;
    // A mouse grabs the whole row; a touch would fight the list's own scroll, so
    // it must start on the grip handle (touch-action: none) to reorder.
    if (event.pointerType === "touch" && !event.target.closest("[data-qn-drag-handle]")) return;
    this.suppressClick = false;
    this.drag = {
      item,
      pointerId: event.pointerId,
      startClientX: event.clientX,
      startClientY: event.clientY,
      lastClientY: event.clientY,
      active: false,
      raf: 0,
    };
    try {
      item.setPointerCapture(event.pointerId);
    } catch (error) {
      void error;
    }
  }

  onPointerMove(event) {
    const drag = this.drag;
    if (!drag || event.pointerId !== drag.pointerId) return;
    if (!drag.active) {
      if (!(event.buttons & 1)) {
        this.drag = null;
        return;
      }
      if (Math.hypot(event.clientX - drag.startClientX, event.clientY - drag.startClientY) < DRAG_THRESHOLD) return;
      this.beginDrag(event);
    }
    event.preventDefault();
    drag.lastClientY = event.clientY;
    this.updateDrag();
  }

  onPointerUp(event) {
    const drag = this.drag;
    if (!drag || event.pointerId !== drag.pointerId) return;
    this.drag = null;
    if (!drag.active) return;
    window.cancelAnimationFrame(drag.raf);
    this.suppressClick = true;
    this.activeList()?.classList.remove("quicknav-active-list-dragging");
    for (const item of drag.items) item.style.transform = "";
    drag.item.classList.remove("quicknav-active-item-dragging");
    if (drag.toIndex !== drag.fromIndex) {
      const others = drag.items.filter((item) => item !== drag.item);
      this.activeList().insertBefore(drag.item, others[drag.toIndex] || null);
      this.persistOrder();
    }
  }

  cancelDrag() {
    const drag = this.drag;
    this.drag = null;
    if (!drag || !drag.active) return;
    window.cancelAnimationFrame(drag.raf);
    this.activeList()?.classList.remove("quicknav-active-list-dragging");
    for (const item of drag.items) item.style.transform = "";
    drag.item.classList.remove("quicknav-active-item-dragging");
  }

  beginDrag(event) {
    const drag = this.drag;
    drag.active = true;
    drag.items = this.dragItems();
    drag.fromIndex = drag.items.indexOf(drag.item);
    drag.toIndex = drag.fromIndex;
    drag.height = drag.item.getBoundingClientRect().height;
    const menuTop = this.menu.getBoundingClientRect().top;
    drag.centers = drag.items.map((item) => {
      const rect = item.getBoundingClientRect();
      return rect.top + rect.height / 2 - menuTop + this.menu.scrollTop;
    });
    drag.startContentY = this.contentY(event.clientY);
    this.activeList().classList.add("quicknav-active-list-dragging");
    drag.item.classList.add("quicknav-active-item-dragging");
    drag.raf = window.requestAnimationFrame(() => this.tickEdgeScroll());
  }

  updateDrag() {
    const drag = this.drag;
    if (!drag || !drag.active) return;
    const dy = this.contentY(drag.lastClientY) - drag.startContentY;
    const draggedCenter = drag.centers[drag.fromIndex] + dy;
    let toIndex = 0;
    for (let i = 0; i < drag.centers.length; i += 1) {
      if (i !== drag.fromIndex && drag.centers[i] < draggedCenter) toIndex += 1;
    }
    drag.toIndex = toIndex;
    drag.item.style.transform = "translateY(" + dy + "px)";
    drag.items.forEach((item, i) => {
      if (item === drag.item) return;
      let shift = 0;
      if (i > drag.fromIndex && i <= drag.toIndex) shift = -drag.height;
      else if (i < drag.fromIndex && i >= drag.toIndex) shift = drag.height;
      item.style.transform = shift ? "translateY(" + shift + "px)" : "";
    });
  }

  tickEdgeScroll() {
    const drag = this.drag;
    if (!drag || !drag.active) return;
    const rect = this.menu.getBoundingClientRect();
    let delta = 0;
    if (drag.lastClientY < rect.top + EDGE_ZONE) delta = -EDGE_STEP;
    else if (drag.lastClientY > rect.bottom - EDGE_ZONE) delta = EDGE_STEP;
    if (delta) {
      const max = this.menu.scrollHeight - this.menu.clientHeight;
      const next = Math.max(0, Math.min(this.menu.scrollTop + delta, max));
      if (next !== this.menu.scrollTop) {
        this.menu.scrollTop = next;
        this.updateDrag();
      }
    }
    drag.raf = window.requestAnimationFrame(() => this.tickEdgeScroll());
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
