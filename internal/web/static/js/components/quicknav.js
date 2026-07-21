import { confirm, promptText } from "@dc/dialog";
import { el } from "@dc/dom";
import { onServerEvent } from "@dc/events";
import * as store from "@dc/store";
import * as projectSort from "@dc/project-sort";
import { applyFold } from "@dc/fold";
import { ensureOk, getText, postForm, postJSON } from "@dc/http";
import { notifyError, notifySuccess } from "@dc/toast";

const TAB_KEY = "dc-quicknav-tab";
const FOLD_LIMIT = 5;
const DRAG_THRESHOLD = 6;
const EDGE_ZONE = 28;
const EDGE_STEP = 10;
const FLING_VX = 0.25;
const SNAP_MS = 250;
const GROUP_ZONE_RATIO = 0.3;
const GROUP_DWELL_MS = 220;

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
    this.spinner = el("div", { class: "dc-loading-bar", role: "status", "aria-label": "Loading" });
    this.inFlight = false;
    this.dirty = false;
    this.view = null;
    this.drag = null;
    this.swipe = null;
    this.openSwipe = null;
    this.confirming = false;
    this.suppressClick = false;
    this.opened = false;

    this.ac = new AbortController();
    const signal = this.ac.signal;
    this.addEventListener("show.bs.dropdown", () => {
      this.opened = true;
      this.applyState();
      this.refresh();
    }, { signal });
    // The confirm dialog opens outside the dropdown, which auto-close would read
    // as an outside click; keep the menu open under it.
    this.addEventListener("hide.bs.dropdown", (event) => { if (this.confirming) event.preventDefault(); }, { signal });
    this.addEventListener("hidden.bs.dropdown", () => {
      this.opened = false;
      this.closeSwipe();
    }, { signal });
    // A coder or shell started, stopped, was renamed or reordered elsewhere: pull
    // the fresh list while the menu is open. Closed, it already refetches on open.
    onServerEvent("terminals", () => { if (this.opened) this.refresh(); }, { signal });
    this.addEventListener("click", (event) => this.handleClick(event), { signal });
    // Capture so a click synthesised right after a gesture, a tap on the grip
    // handle, or a tap while a delete is revealed never reaches pe.js and
    // navigates the row. The revealed-state tap just closes the reveal, except on
    // the delete button itself.
    this.addEventListener("click", (event) => {
      if (this.suppressClick) {
        this.suppressClick = false;
        event.preventDefault();
        event.stopPropagation();
        return;
      }
      if (this.openSwipe && !event.target.closest("[data-qn-delete], [data-qn-ungroup], [data-qn-remove], [data-qn-rename]")) {
        this.closeSwipe();
        event.preventDefault();
        event.stopPropagation();
        return;
      }
      if (!event.target.closest("[data-qn-drag-handle]")) return;
      event.preventDefault();
      event.stopPropagation();
    }, { signal, capture: true });
    this.list.addEventListener("dragstart", (event) => event.preventDefault(), { signal });
    this.list.addEventListener("pointerdown", (event) => this.onPointerDown(event), { signal });
    this.list.addEventListener("pointermove", (event) => this.onPointerMove(event), { signal });
    this.list.addEventListener("pointerup", (event) => this.onPointerUp(event), { signal });
    this.list.addEventListener("pointercancel", () => {
      this.cancelDrag();
      this.cancelSwipe();
    }, { signal });
    this.observer = new MutationObserver(() => this.applyState());
    this.observer.observe(this.list, { childList: true });
  }

  disconnectedCallback() {
    this.cancelDrag();
    this.cancelSwipe();
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
    return list ? Array.from(list.querySelectorAll(":scope > .quicknav-swipe-row, :scope > [data-qn-block]")) : [];
  }

  swipeAnchor(row) {
    return row?.querySelector(".quicknav-active-item");
  }

  deleteWidth(row) {
    const buttons = row ? Array.from(row.querySelectorAll(":scope > .quicknav-swipe-delete")) : [];
    const width = buttons.reduce((sum, button) => sum + button.offsetWidth, 0);
    return width || 72;
  }

  // Content Y measured against the scrollable menu, so edge auto-scroll keeps the
  // math consistent while the list moves under the pointer.
  contentY(clientY) {
    return clientY - this.menu.getBoundingClientRect().top + this.menu.scrollTop;
  }

  // Persist the current active-list order to the same cross-device @dc_tab_pos
  // state the tab strip writes, so a drag in either place agrees. Group blocks
  // expand into their member ids like the strip does.
  persistOrder() {
    const ids = this.dragItems().flatMap((unit) => {
      const anchor = this.swipeAnchor(unit);
      if (!anchor) return [];
      const members = (anchor.dataset.tabMembers || "").split(" ").filter(Boolean);
      return members.length ? members : [anchor.dataset.tabId].filter(Boolean);
    });
    postJSON("/terminal-tabs/order", { ids })
      .then((response) => ensureOk(response, "Could not save the order."))
      .catch((error) => notifyError(error.message));
  }

  // Persist a member drag inside a group block: re-posting the member ids to
  // the group endpoint rewrites @dc_tab_gpos, the same way the split page's
  // pane drag does.
  persistGroup(block) {
    const ids = Array.from(block.querySelectorAll(":scope > [data-qn-group-member] .quicknav-active-item"))
      .map((anchor) => anchor.dataset.tabId)
      .filter(Boolean);
    postJSON("/terminal-tabs/group", { ids })
      .then((response) => ensureOk(response, "Could not save the pane order."))
      .catch((error) => notifyError(error.message));
  }

  onPointerDown(event) {
    if (event.button !== 0 || this.drag || this.swipe) return;
    const row = event.target.closest(".quicknav-swipe-row");
    if (!row) return;
    // A new pointer invalidates a pending post-gesture suppression, a gesture
    // that ended off-row would otherwise swallow the next tap.
    this.suppressClick = false;
    if (event.target.closest(".quicknav-swipe-delete")) return;
    // A mouse grabs the whole row to reorder. On touch the row body swipes
    // horizontally (vertical stays native scroll via pan-y) and only the grip
    // handle reorders, so the three gestures never fight. Swipe works on every
    // row, reorder only in the active list.
    // No pointer capture yet: capturing retargets the eventual click onto the
    // wrapper div, which hides the anchor from pe.js and kills plain clicks.
    // The gesture captures once it actually begins.
    if (event.pointerType === "touch" && !event.target.closest("[data-qn-drag-handle]")) {
      this.swipe = {
        row,
        pointerId: event.pointerId,
        startX: event.clientX,
        startY: event.clientY,
        lastX: event.clientX,
        lastT: event.timeStamp,
        vx: 0,
        x: 0,
        base: this.openSwipe === row ? -this.deleteWidth(row) : 0,
        width: 0,
        active: false,
      };
      return;
    }
    if (!this.activeList()?.contains(row)) return;
    let item = row;
    let container = this.activeList();
    let members = false;
    if (row.hasAttribute("data-qn-group-member")) {
      item = row;
      container = row.closest("[data-qn-block]");
      members = true;
    } else if (row.hasAttribute("data-qn-group")) {
      item = row.closest("[data-qn-block]");
    }
    this.drag = {
      item,
      container,
      members,
      pointerId: event.pointerId,
      startClientX: event.clientX,
      startClientY: event.clientY,
      lastClientY: event.clientY,
      active: false,
      raf: 0,
    };
  }

  onPointerMove(event) {
    const sw = this.swipe;
    if (sw && event.pointerId === sw.pointerId) {
      this.moveSwipe(event);
      return;
    }
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

  moveSwipe(event) {
    const sw = this.swipe;
    const dx = event.clientX - sw.startX;
    const dy = event.clientY - sw.startY;
    if (!sw.active) {
      if (Math.hypot(dx, dy) < DRAG_THRESHOLD) return;
      // Vertical intent belongs to the native scroll, let go of the gesture.
      if (Math.abs(dy) > Math.abs(dx)) {
        this.swipe = null;
        return;
      }
      sw.active = true;
      sw.width = this.deleteWidth(sw.row);
      this.closeSwipe(sw.row);
      sw.row.classList.add("quicknav-swiping");
      try {
        sw.row.setPointerCapture(event.pointerId);
      } catch (error) {
        void error;
      }
    }
    event.preventDefault();
    const dt = Math.max(1, event.timeStamp - sw.lastT);
    sw.vx = (event.clientX - sw.lastX) / dt;
    sw.lastX = event.clientX;
    sw.lastT = event.timeStamp;
    let x = sw.base + dx;
    if (x > 0) x = 0;
    // Damped overshoot past the fully revealed delete.
    if (x < -sw.width) x = -sw.width + (x + sw.width) * 0.2;
    sw.x = x;
    const anchor = this.swipeAnchor(sw.row);
    if (anchor) anchor.style.transform = "translateX(" + x + "px)";
  }

  onPointerUp(event) {
    const sw = this.swipe;
    if (sw && event.pointerId === sw.pointerId) {
      this.swipe = null;
      if (sw.active) {
        this.suppressClick = true;
        // Snap to open or closed: a fling decides by direction, a slow release
        // by whether the delete is at least half revealed.
        const open = Math.abs(sw.vx) > FLING_VX ? sw.vx < 0 : sw.x < -sw.width / 2;
        this.setSwipeOpen(sw.row, open);
        this.unswipeAfterSnap(sw.row);
      }
      if (this.dirty && this.opened) this.refresh();
      return;
    }
    const drag = this.drag;
    if (!drag || event.pointerId !== drag.pointerId) return;
    this.drag = null;
    if (drag.active) {
      window.cancelAnimationFrame(drag.raf);
      this.suppressClick = true;
      this.activeList()?.classList.remove("quicknav-active-list-dragging");
      for (const item of drag.items) {
        item.style.transform = "";
        item.classList.remove("quicknav-group-target");
      }
      drag.item.classList.remove("quicknav-active-item-dragging");
      if (drag.groupTarget >= 0 && drag.items[drag.groupTarget]) {
        void this.groupUnits(drag.items[drag.groupTarget], drag.item);
      } else if (drag.toIndex !== drag.fromIndex) {
        const others = drag.items.filter((item) => item !== drag.item);
        drag.container.insertBefore(drag.item, others[drag.toIndex] || null);
        if (drag.members) this.persistGroup(drag.container);
        else this.persistOrder();
      }
    }
    // Flush any event coalesced while the drag held the list.
    if (this.dirty && this.opened) this.refresh();
  }

  // Keep the delete visible until the snap animation settles, then drop the
  // swiping state so a closed row hides it again.
  unswipeAfterSnap(row) {
    const anchor = this.swipeAnchor(row);
    const done = () => row.classList.remove("quicknav-swiping");
    anchor?.addEventListener("transitionend", done, { once: true, signal: this.ac?.signal });
    setTimeout(done, SNAP_MS);
  }

  cancelSwipe() {
    const sw = this.swipe;
    this.swipe = null;
    if (!sw || !sw.active) return;
    this.setSwipeOpen(sw.row, false);
    this.unswipeAfterSnap(sw.row);
  }

  setSwipeOpen(row, open) {
    const anchor = this.swipeAnchor(row);
    if (!anchor) return;
    if (open) {
      row.classList.add("quicknav-swipe-open");
      anchor.style.transform = "translateX(-" + this.deleteWidth(row) + "px)";
      this.openSwipe = row;
      return;
    }
    if (this.openSwipe === row) this.openSwipe = null;
    anchor.style.transform = "";
    if (!row.classList.contains("quicknav-swipe-open")) return;
    const done = () => {
      if (!anchor.style.transform) row.classList.remove("quicknav-swipe-open");
    };
    anchor.addEventListener("transitionend", done, { once: true, signal: this.ac?.signal });
    setTimeout(done, SNAP_MS);
  }

  closeSwipe(except) {
    const row = this.openSwipe;
    if (!row || row === except) return;
    if (row.isConnected) this.setSwipeOpen(row, false);
    else this.openSwipe = null;
  }

  cancelDrag() {
    const drag = this.drag;
    this.drag = null;
    if (!drag || !drag.active) return;
    window.cancelAnimationFrame(drag.raf);
    this.activeList()?.classList.remove("quicknav-active-list-dragging");
    for (const item of drag.items) {
      item.style.transform = "";
      item.classList.remove("quicknav-group-target");
    }
    drag.item.classList.remove("quicknav-active-item-dragging");
  }

  // groupUnits merges the dragged unit into the drop target, mirroring the
  // strip's drag-to-group: blocks expand into their members, dropping onto a
  // group joins it, and when the open page is involved the client follows the
  // server to the split page.
  async groupUnits(target, dragged) {
    const expand = (unit) => {
      const anchor = this.swipeAnchor(unit);
      const members = (anchor?.dataset.tabMembers || "").split(" ").filter(Boolean);
      return members.length ? members : [anchor?.dataset.tabId].filter(Boolean);
    };
    const involved = Boolean(
      target.querySelector(".quicknav-active-item.active") || dragged.querySelector(".quicknav-active-item.active"),
    );
    const ids = [...expand(target), ...expand(dragged)];
    try {
      const response = await postJSON("/terminal-tabs/group", { ids });
      await ensureOk(response, "Could not create the split view.");
      const data = await response.json();
      if (involved && data.url) {
        if (window.bootstrap) window.bootstrap.Dropdown.getOrCreateInstance(this.toggle).hide();
        if (window.app?.navigate) Promise.resolve(window.app.navigate(data.url)).catch(() => {});
        else window.location.href = data.url;
        return;
      }
      if (this.opened) this.refresh();
    } catch (error) {
      notifyError(error.message);
    }
  }

  beginDrag(event) {
    const drag = this.drag;
    drag.active = true;
    try {
      drag.item.setPointerCapture(event.pointerId);
    } catch (error) {
      void error;
    }
    this.closeSwipe();
    drag.items = drag.members
      ? Array.from(drag.container.querySelectorAll(":scope > [data-qn-group-member]"))
      : this.dragItems();
    drag.fromIndex = drag.items.indexOf(drag.item);
    drag.toIndex = drag.fromIndex;
    drag.height = drag.item.getBoundingClientRect().height;
    drag.heights = drag.items.map((item) => item.getBoundingClientRect().height);
    drag.groupTarget = -1;
    drag.groupPending = -1;
    drag.groupSince = 0;
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

  groupCandidate(drag, draggedCenter) {
    if (drag.members) return -1;
    for (let i = 0; i < drag.items.length; i += 1) {
      if (i === drag.fromIndex) continue;
      let shift = 0;
      if (i > drag.fromIndex && i <= drag.toIndex) shift = -drag.height;
      else if (i < drag.fromIndex && i >= drag.toIndex) shift = drag.height;
      const visualCenter = drag.centers[i] + shift;
      if (Math.abs(draggedCenter - visualCenter) < drag.heights[i] * GROUP_ZONE_RATIO) {
        return i;
      }
    }
    return -1;
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
    const candidate = this.groupCandidate(drag, draggedCenter);
    if (candidate === -1) {
      drag.groupPending = -1;
      drag.groupTarget = -1;
    } else if (candidate !== drag.groupPending) {
      drag.groupPending = candidate;
      drag.groupSince = Date.now();
      drag.groupTarget = -1;
    } else if (drag.groupTarget === -1 && Date.now() - drag.groupSince >= GROUP_DWELL_MS) {
      drag.groupTarget = candidate;
    }
    drag.items.forEach((item, i) => {
      item.classList.toggle("quicknav-group-target", i === drag.groupTarget);
    });
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
    if (drag.groupPending >= 0 && drag.groupTarget === -1
      && Date.now() - drag.groupSince >= GROUP_DWELL_MS) {
      this.updateDrag();
    }
    drag.raf = window.requestAnimationFrame(() => this.tickEdgeScroll());
  }

  refresh() {
    // Never rebuild the list under an in-flight fetch, an active gesture (that
    // would detach the touched node) or an open confirm dialog; coalesce into
    // dirty and re-run afterward.
    if (this.inFlight || this.drag || this.swipe || this.confirming) {
      this.dirty = true;
      return;
    }
    this.dirty = false;
    this.inFlight = true;
    this.menu.prepend(this.spinner);
    // Background refresh on every open, kept silent on purpose (no toast storm).
    // On a split page the active pane rides along, so the member row marking
    // and the project context follow the pane the user is actually on.
    let path = location.pathname;
    const focused = document.querySelector("terminal-attach[active][split-group]");
    if (focused) path += "?focus=" + encodeURIComponent(focused.getAttribute("terminal-id") || "");
    getText(this.url + "?path=" + encodeURIComponent(path))
      .then((html) => {
        if (this.drag || this.swipe) {
          this.dirty = true;
          return;
        }
        this.openSwipe = null;
        this.list.innerHTML = html;
        this.reposition();
      })
      .catch(() => {})
      .finally(() => {
        this.spinner.remove();
        this.inFlight = false;
        if (this.dirty && this.opened) this.refresh();
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

  // deleteTarget stops a coder, deletes a shell or deletes an inactive coder
  // from the revealed swipe action, mirroring the desktop tab strip's close
  // control and the projects page: same confirm dialogs, same endpoints and
  // toasts. Deleting the active-list terminal you are attached to moves you to
  // its neighbor; the projects tab has no neighbor order, there the server's
  // redirect target wins.
  async deleteTarget(row) {
    if (this.confirming || !row) return;
    const item = this.swipeAnchor(row);
    if (!item) return;
    const { tabId: id, tabKind: kind, tabName: name } = item.dataset;
    const split = kind === "split";
    const memberBlock = !split && row.hasAttribute("data-qn-group-member") ? row.closest("[data-qn-block]") : null;
    const unit = split ? row.closest("[data-qn-block]") : (memberBlock || row);
    const current = item.classList.contains("active");
    let neighborUrl = null;
    if (memberBlock) {
      neighborUrl = this.swipeAnchor(memberBlock)?.getAttribute("href") || null;
    } else {
      const units = this.dragItems();
      const index = units.indexOf(unit);
      if (index >= 0) {
        const neighbor = units[index + 1] || units[index - 1] || null;
        neighborUrl = neighbor ? this.swipeAnchor(neighbor)?.getAttribute("href") : null;
      }
    }
    this.confirming = true;
    try {
      const memberIds = (item.dataset.tabMembers || "").split(" ").filter(Boolean);
      const memberKinds = (item.dataset.tabMemberKinds || "").split(" ").filter(Boolean);
      const ok = await confirm({
        title: split
          ? `Close all ${memberIds.length} terminals in "${name}"?`
          : kind === "coder" ? `Stop coder "${name}"?`
            : kind === "inactive" ? `Delete inactive coder "${name}"?`
              : `Delete shell "${name}"?`,
        confirmText: split ? "Close all" : (kind === "coder" ? "Stop" : "Delete"),
      });
      if (!ok) {
        this.closeSwipe();
        return;
      }
      let response = null;
      if (split) {
        let failed = 0;
        for (let i = 0; i < memberIds.length; i += 1) {
          window.dispatchEvent(new CustomEvent("dc:terminal-closing", { detail: { id: memberIds[i] } }));
          const action = memberKinds[i] === "shell" ? `/shells/${memberIds[i]}/delete` : `/coders/${memberIds[i]}/stop`;
          try {
            response = await postForm(action, {});
            await ensureOk(response, "Could not close the session.");
          } catch (memberError) {
            void memberError;
            failed += 1;
          }
        }
        if (failed) notifyError(`Could not close ${failed} of ${memberIds.length} terminals.`);
        else notifySuccess(`Split view "${name}" closed.`);
      } else if (kind === "inactive") {
        response = await postForm(`/coders/${id}/delete`, {});
        await ensureOk(response, "Could not delete the coder.");
        notifySuccess(`Inactive coder "${name}" deleted.`);
      } else {
        window.dispatchEvent(new CustomEvent("dc:terminal-closing", { detail: { id } }));
        const action = kind === "coder" ? `/coders/${id}/stop` : `/shells/${id}/delete`;
        response = await postForm(action, {});
        await ensureOk(response, "Could not close the session.");
        notifySuccess(kind === "coder" ? `Coder "${name}" stopped.` : `Shell "${name}" deleted.`);
      }
      if (this.openSwipe === row) this.openSwipe = null;
      unit.remove();
      if (current) {
        this.confirming = false;
        if (window.bootstrap) window.bootstrap.Dropdown.getOrCreateInstance(this.toggle).hide();
        const url = neighborUrl || response?.url || "/projects";
        if (window.app?.navigate) window.app.navigate(url);
        else window.location.href = url;
      }
    } catch (error) {
      notifyError(error.message);
    } finally {
      this.confirming = false;
      if (this.dirty && this.opened) this.refresh();
    }
  }

  // renameTarget renames a shell or a split view from the revealed swipe
  // action, mirroring the tab strip's context menu entries: same prompt, same
  // endpoints, and an emptied split view name falls back to the derived label.
  async renameTarget(row) {
    if (this.confirming || !row) return;
    const item = this.swipeAnchor(row);
    if (!item) return;
    const { tabId: id, tabKind: kind, tabName: name } = item.dataset;
    this.confirming = true;
    try {
      if (kind === "split") {
        const next = await promptText({
          title: `Rename split view "${name}"`,
          value: name,
          confirmText: "Rename",
          allowEmpty: true,
        });
        if (next === null || next === name) {
          this.closeSwipe();
          return;
        }
        const response = await postJSON("/terminal-tabs/group/name", { group: id, name: next });
        await ensureOk(response, "Could not rename the split view.");
      } else {
        const next = await promptText({
          title: `Rename shell "${name}"`,
          value: name,
          confirmText: "Rename",
          validatorMessage: "Please enter a name.",
        });
        if (!next || next === name) {
          this.closeSwipe();
          return;
        }
        const response = await postForm(`/shells/${id}/rename`, { name: next });
        await ensureOk(response, "Could not rename the shell.");
      }
      this.closeSwipe();
      if (this.opened) this.refresh();
    } catch (error) {
      notifyError(error.message);
    } finally {
      this.confirming = false;
      if (this.dirty && this.opened) this.refresh();
    }
  }

  // ungroupTarget dissolves a split view from the group row's swipe action
  // without killing any member, mirroring the strip's context menu entry.
  async ungroupTarget(row) {
    if (this.confirming || !row) return;
    const item = this.swipeAnchor(row);
    if (!item) return;
    const ids = (item.dataset.tabMembers || "").split(" ").filter(Boolean);
    const current = item.classList.contains("active");
    try {
      const response = await postJSON("/terminal-tabs/ungroup", { ids });
      await ensureOk(response, "Could not ungroup the split view.");
      const data = await response.json();
      notifySuccess(`Split view "${item.dataset.tabName}" ungrouped.`);
      this.closeSwipe();
      if (current) {
        if (window.bootstrap) window.bootstrap.Dropdown.getOrCreateInstance(this.toggle).hide();
        const url = data.url || "/projects";
        if (window.app?.navigate) window.app.navigate(url);
        else window.location.href = url;
      }
    } catch (error) {
      notifyError(error.message);
    } finally {
      if (this.dirty && this.opened) this.refresh();
    }
  }

  // removeTarget takes one member out of its split view without killing it,
  // mirroring the pane header's remove button on the split page. When the
  // open page is the member's split, the client follows the server (back to
  // the split while it lives, else to the survivor).
  async removeTarget(row) {
    if (this.confirming || !row) return;
    const item = this.swipeAnchor(row);
    if (!item) return;
    const block = row.closest("[data-qn-block]");
    const current = Boolean(block?.querySelector("[data-qn-group] .quicknav-active-item.active"));
    try {
      const response = await postJSON("/terminal-tabs/ungroup", { ids: [item.dataset.tabId] });
      await ensureOk(response, "Could not change the split view.");
      const data = await response.json();
      notifySuccess(`"${item.dataset.tabName}" removed from the split view.`);
      this.closeSwipe();
      if (current) {
        if (window.bootstrap) window.bootstrap.Dropdown.getOrCreateInstance(this.toggle).hide();
        const url = data.url || "/projects";
        if (window.app?.navigate) Promise.resolve(window.app.navigate(url)).catch(() => {});
        else window.location.href = url;
        return;
      }
      if (this.opened) this.refresh();
    } catch (error) {
      notifyError(error.message);
    } finally {
      if (this.dirty && this.opened) this.refresh();
    }
  }

  handleClick(event) {
    if (!this.view) this.initView();
    const del = event.target.closest("[data-qn-delete]");
    if (del) {
      event.preventDefault();
      event.stopPropagation();
      void this.deleteTarget(del.closest(".quicknav-swipe-row"));
      return;
    }
    const rename = event.target.closest("[data-qn-rename]");
    if (rename) {
      event.preventDefault();
      event.stopPropagation();
      void this.renameTarget(rename.closest(".quicknav-swipe-row"));
      return;
    }
    const ungroup = event.target.closest("[data-qn-ungroup]");
    if (ungroup) {
      event.preventDefault();
      event.stopPropagation();
      void this.ungroupTarget(ungroup.closest(".quicknav-swipe-row"));
      return;
    }
    const remove = event.target.closest("[data-qn-remove]");
    if (remove) {
      event.preventDefault();
      event.stopPropagation();
      void this.removeTarget(remove.closest(".quicknav-swipe-row"));
      return;
    }
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
