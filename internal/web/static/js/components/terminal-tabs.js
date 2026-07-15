import { confirm } from "@dc/dialog";
import { el } from "@dc/dom";
import { onServerEvent } from "@dc/events";
import { applyFold } from "@dc/fold";
import { ensureOk, getText, postForm, postJSON } from "@dc/http";
import * as projectSort from "@dc/project-sort";
import { notifyError, notifySuccess } from "@dc/toast";

const DRAG_THRESHOLD = 6;
const EDGE_ZONE = 32;
const EDGE_STEP = 12;
const TAP_WINDOW_MS = 400;
const RESUME_FOLD_LIMIT = 3;

class TerminalTabs extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.strip = this.querySelector("[data-tabs-strip]");
    if (!this.strip) return;
    this.ac = new AbortController();
    this.drag = null;
    this.switcher = null;
    this.suppressClick = false;
    this.confirming = false;
    this.resuming = false;
    this.inFlight = false;
    this.dirty = false;
    this.pendingIndex = null;
    this.tap = { pending: null, lastKey: null, lastTime: 0 };
    const signal = this.ac.signal;

    this.revealActive();
    this.expandedResume = new Set();
    const resumeProjects = this.querySelector("[data-tabs-resume-projects]");
    if (resumeProjects) projectSort.sort(resumeProjects);
    this.querySelectorAll("[data-tabs-resume-fold]").forEach((group) => this.foldResume(group));

    for (const toggle of this.querySelectorAll(".terminal-tabs-new-btn")) {
      const focus = toggle.focus.bind(toggle);
      toggle.focus = (options) => focus({ preventScroll: true, ...options });
      toggle.addEventListener("mousedown", (event) => event.preventDefault(), { signal });
    }
    this.strip.addEventListener("wheel", (event) => this.onWheel(event), { signal, passive: false });
    this.strip.addEventListener("dragstart", (event) => event.preventDefault(), { signal });
    this.strip.addEventListener("pointerdown", (event) => this.onPointerDown(event), { signal });
    this.strip.addEventListener("pointermove", (event) => this.onPointerMove(event), { signal });
    this.strip.addEventListener("pointerup", (event) => this.onPointerUp(event), { signal });
    this.strip.addEventListener("pointercancel", () => this.cancelDrag(), { signal });
    this.strip.addEventListener("click", (event) => this.onClick(event), { signal, capture: true });
    document.addEventListener("keydown", (event) => this.onKeydown(event), { signal, capture: true });
    document.addEventListener("keyup", (event) => this.onKeyup(event), { signal, capture: true });
    document.addEventListener("dc-renamed", (event) => this.onRenamed(event.detail || {}), { signal });
    // A coder or shell started, stopped, was renamed or reordered somewhere (this
    // device or another one): pull the fresh strip.
    onServerEvent("terminals", () => this.refresh(), { signal });
  }

  onRenamed({ url, name }) {
    if (!url || !name) return;
    const segments = new URL(url, window.location.href).pathname.split("/");
    const tab = this.tabs().find((candidate) => segments.includes(candidate.dataset.tabId));
    if (!tab) return;
    tab.dataset.tabName = name;
    const label = tab.querySelector(".terminal-tab-name");
    if (label) label.textContent = name;
    const project = tab.dataset.tabProject;
    tab.setAttribute("title", name + (project ? " (" + project + ")" : ""));
    const close = tab.querySelector("[data-tab-close]");
    if (close) {
      close.setAttribute("aria-label", (tab.dataset.tabKind === "coder" ? "Stop coder " : "Delete shell ") + name);
    }
  }

  disconnectedCallback() {
    this.closeSwitcher();
    this.cancelDrag();
    this.ac?.abort();
    this.ac = null;
  }

  tabs() {
    return Array.from(this.strip.querySelectorAll(".terminal-tab"));
  }

  persistOrder() {
    postJSON("/terminal-tabs/order", { ids: this.tabs().map((tab) => tab.dataset.tabId) })
      .then((response) => ensureOk(response, "Could not save the tab order."))
      .catch((error) => notifyError(error.message));
  }

  foldResume(group) {
    const key = group.getAttribute("data-tabs-resume-fold");
    applyFold(group, {
      limit: RESUME_FOLD_LIMIT,
      expanded: this.expandedResume.has(key),
      toggleAttr: "data-tabs-resume-toggle",
      toggleClass: "dropdown-item text-center text-secondary small py-1",
      signal: this.ac.signal,
      onToggle: (event, next) => {
        event.preventDefault();
        event.stopPropagation();
        if (next) this.expandedResume.add(key);
        else this.expandedResume.delete(key);
        this.foldResume(group);
      },
    });
  }

  revealTab(tab) {
    if (!tab) return;
    const strip = this.strip;
    if (tab.offsetLeft < strip.scrollLeft
      || tab.offsetLeft + tab.offsetWidth > strip.scrollLeft + strip.clientWidth) {
      strip.scrollLeft = tab.offsetLeft - (strip.clientWidth - tab.offsetWidth) / 2;
    }
  }

  revealActive() {
    this.revealTab(this.strip.querySelector(".terminal-tab.active"));
  }

  markPending(tab) {
    for (const other of this.strip.querySelectorAll(".terminal-tab-pending")) {
      if (other !== tab) other.classList.remove("terminal-tab-pending");
    }
    if (tab) {
      tab.classList.add("terminal-tab-pending");
      this.revealTab(tab);
    }
  }

  onWheel(event) {
    if (!event.deltaY || event.deltaX) return;
    if (this.strip.scrollWidth <= this.strip.clientWidth) return;
    event.preventDefault();
    this.strip.scrollLeft += event.deltaY;
  }

  onClick(event) {
    const close = event.target.closest("[data-tab-close]");
    if (close) {
      event.preventDefault();
      event.stopPropagation();
      const tab = close.closest(".terminal-tab");
      if (tab) void this.closeTarget(tab.dataset);
      return;
    }
    if (!this.suppressClick) return;
    this.suppressClick = false;
    event.preventDefault();
    event.stopPropagation();
  }

  contentX(clientX) {
    return clientX - this.strip.getBoundingClientRect().left + this.strip.scrollLeft;
  }

  onPointerDown(event) {
    if (event.button !== 0 || this.switcher || event.target.closest("[data-tab-close]")) return;
    const tab = event.target.closest(".terminal-tab");
    if (!tab) return;
    this.suppressClick = false;
    this.drag = {
      tab,
      pointerId: event.pointerId,
      startClientX: event.clientX,
      startClientY: event.clientY,
      lastClientX: event.clientX,
      active: false,
      raf: 0,
    };
    try {
      tab.setPointerCapture(event.pointerId);
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
    drag.lastClientX = event.clientX;
    this.updateDrag();
  }

  onPointerUp(event) {
    const drag = this.drag;
    if (!drag || event.pointerId !== drag.pointerId) return;
    this.drag = null;
    if (drag.active) {
      window.cancelAnimationFrame(drag.raf);
      this.suppressClick = true;
      this.strip.classList.remove("terminal-tabs-strip-dragging");
      drag.tab.classList.remove("terminal-tab-dragging");
      for (const tab of drag.tabs) {
        tab.style.transform = "";
      }
      if (drag.toIndex !== drag.fromIndex) {
        const others = drag.tabs.filter((tab) => tab !== drag.tab);
        this.strip.insertBefore(drag.tab, others[drag.toIndex] || null);
        this.persistOrder();
      }
    }
    // Flush any event that arrived while the drag held the strip.
    this.tryRefresh();
  }

  cancelDrag() {
    const drag = this.drag;
    this.drag = null;
    if (!drag || !drag.active) return;
    window.cancelAnimationFrame(drag.raf);
    this.strip.classList.remove("terminal-tabs-strip-dragging");
    drag.tab.classList.remove("terminal-tab-dragging");
    for (const tab of drag.tabs) {
      tab.style.transform = "";
    }
  }

  beginDrag(event) {
    const drag = this.drag;
    drag.active = true;
    drag.tabs = this.tabs();
    drag.fromIndex = drag.tabs.indexOf(drag.tab);
    drag.toIndex = drag.fromIndex;
    drag.width = drag.tab.getBoundingClientRect().width;
    const stripLeft = this.strip.getBoundingClientRect().left;
    drag.centers = drag.tabs.map((tab) => {
      const rect = tab.getBoundingClientRect();
      return rect.left + rect.width / 2 - stripLeft + this.strip.scrollLeft;
    });
    drag.startContentX = this.contentX(event.clientX);
    this.strip.classList.add("terminal-tabs-strip-dragging");
    drag.tab.classList.add("terminal-tab-dragging");
    drag.raf = window.requestAnimationFrame(() => this.tickEdgeScroll());
  }

  updateDrag() {
    const drag = this.drag;
    if (!drag || !drag.active) return;
    const dx = this.contentX(drag.lastClientX) - drag.startContentX;
    const draggedCenter = drag.centers[drag.fromIndex] + dx;
    let toIndex = 0;
    for (let i = 0; i < drag.centers.length; i += 1) {
      if (i !== drag.fromIndex && drag.centers[i] < draggedCenter) toIndex += 1;
    }
    drag.toIndex = toIndex;
    drag.tab.style.transform = "translateX(" + dx + "px)";
    drag.tabs.forEach((tab, i) => {
      if (tab === drag.tab) return;
      let shift = 0;
      if (i > drag.fromIndex && i <= drag.toIndex) shift = -drag.width;
      else if (i < drag.fromIndex && i >= drag.toIndex) shift = drag.width;
      tab.style.transform = shift ? "translateX(" + shift + "px)" : "";
    });
  }

  tickEdgeScroll() {
    const drag = this.drag;
    if (!drag || !drag.active) return;
    const rect = this.strip.getBoundingClientRect();
    let delta = 0;
    if (drag.lastClientX < rect.left + EDGE_ZONE) delta = -EDGE_STEP;
    else if (drag.lastClientX > rect.right - EDGE_ZONE) delta = EDGE_STEP;
    if (delta) {
      const max = this.strip.scrollWidth - this.strip.clientWidth;
      const next = Math.max(0, Math.min(this.strip.scrollLeft + delta, max));
      if (next !== this.strip.scrollLeft) {
        this.strip.scrollLeft = next;
        this.updateDrag();
      }
    }
    drag.raf = window.requestAnimationFrame(() => this.tickEdgeScroll());
  }

  trackTap(event) {
    if (event.key !== "Control" && event.key !== "Meta") {
      this.tap.pending = null;
      this.tap.lastKey = null;
      return false;
    }
    const otherModifiers = event.key === "Control"
      ? event.metaKey || event.altKey || event.shiftKey
      : event.ctrlKey || event.altKey || event.shiftKey;
    if (event.repeat || otherModifiers) {
      this.tap.pending = null;
      this.tap.lastKey = null;
      return false;
    }
    if (this.tap.lastKey === event.key && Date.now() - this.tap.lastTime < TAP_WINDOW_MS) {
      this.tap.pending = null;
      this.tap.lastKey = null;
      return true;
    }
    this.tap.pending = event.key;
    this.tap.lastKey = null;
    return false;
  }

  onKeyup(event) {
    if (this.tap.pending === event.key) {
      this.tap.lastKey = event.key;
      this.tap.lastTime = Date.now();
    }
    this.tap.pending = null;
  }

  onKeydown(event) {
    if (this.confirming) return;
    if (this.trackTap(event)) {
      event.preventDefault();
      event.stopPropagation();
      if (!this.switcher) this.openSwitcher(1);
      return;
    }
    if (event.key === "Tab" && event.ctrlKey && !event.altKey && !event.metaKey) {
      event.preventDefault();
      event.stopPropagation();
      if (this.switcher) this.moveSelection(event.shiftKey ? -1 : 1);
      else this.switchTo(event.shiftKey ? -1 : 1);
      return;
    }
    if (!this.switcher) return;
    if (event.key === "Control" || event.key === "Shift" || event.key === "Alt" || event.key === "Meta") return;
    const actions = {
      Tab: () => this.moveSelection(event.shiftKey ? -1 : 1),
      ArrowDown: () => this.moveSelection(1),
      ArrowUp: () => this.moveSelection(-1),
      Enter: () => this.commitSelection(),
      Escape: () => this.closeSwitcher(),
    };
    const action = actions[event.key];
    if (!action) return;
    event.preventDefault();
    event.stopPropagation();
    action();
  }

  switchTo(direction) {
    const tabs = this.tabs();
    if (tabs.length < 2) return;
    const active = tabs.findIndex((tab) => tab.classList.contains("active"));
    const base = this.pendingIndex ?? (active === -1 ? 0 : active);
    const next = (base + direction + tabs.length) % tabs.length;
    this.pendingIndex = next;
    const url = tabs[next].getAttribute("href");
    if (!url || url === window.location.pathname) {
      window.pe?.abortController?.abort();
      this.markPending(null);
      return;
    }
    this.markPending(tabs[next]);
    if (window.app?.navigate) Promise.resolve(window.app.navigate(url)).catch(() => {});
    else window.location.href = url;
  }

  switcherRow(tab) {
    const current = tab.classList.contains("active");
    const row = el(
      "div",
      {
        role: "option",
        class: "terminal-switcher-item" + (current ? " current" : ""),
        dataset: {
          switcherId: tab.dataset.tabId || "",
          switcherUrl: tab.getAttribute("href") || "",
          switcherName: (tab.dataset.tabName || "") + " " + (tab.dataset.tabProject || ""),
        },
      },
      tab.querySelector("[data-tab-icon]")?.cloneNode(true),
      el("span", { class: "terminal-switcher-name text-truncate" }, tab.dataset.tabName || ""),
      tab.dataset.tabProject
        ? el("span", { class: "terminal-switcher-project text-truncate" }, tab.dataset.tabProject)
        : null,
    );
    row.addEventListener("click", () => this.navigate(row.dataset.switcherUrl), { signal: this.ac.signal });
    return row;
  }

  resumeRow(button, project, folded) {
    const dataset = {
      switcherResume: button.dataset.resumeId || "",
      switcherName: (button.dataset.resumeName || "") + " " + (project || ""),
      switcherGroup: project,
    };
    if (folded) dataset.switcherFolded = project;
    const row = el(
      "div",
      { role: "option", class: "terminal-switcher-item terminal-switcher-item-inactive", dataset },
      button.querySelector("[data-resume-icon]")?.cloneNode(true),
      el("span", { class: "terminal-switcher-name text-truncate" }, button.dataset.resumeName || ""),
    );
    row.addEventListener("click", () => void this.resumeTarget(row), { signal: this.ac.signal });
    return row;
  }

  resumeToggleRow(project, count) {
    const row = el(
      "div",
      {
        role: "option",
        class: "terminal-switcher-item terminal-switcher-item-inactive terminal-switcher-item-toggle",
        dataset: { switcherToggle: project, switcherGroup: project },
      },
      el("span", { class: "terminal-switcher-name text-truncate" }, "Show " + count + " more"),
    );
    row.addEventListener("pointerdown", (event) => event.preventDefault(), { signal: this.ac.signal });
    row.addEventListener("click", () => this.expandGroup(project), { signal: this.ac.signal });
    return row;
  }

  expandGroup(project) {
    const sw = this.switcher;
    if (!sw) return;
    const list = sw.overlay.querySelector(".terminal-switcher-list");
    const scrollTop = list.scrollTop;
    sw.expanded.add(project);
    this.applyFilter();
    list.scrollTop = scrollTop;
    const first = sw.visible.find((row) => row.dataset.switcherFolded === project);
    if (first) {
      sw.index = sw.visible.indexOf(first);
      this.paintSelection();
    }
  }

  async resumeTarget(row) {
    if (this.resuming) return;
    this.resuming = true;
    try {
      const response = await postForm(`/coders/${row.dataset.switcherResume}/resume`, {});
      await ensureOk(response, "Could not resume the coder.");
      this.navigate(response.url);
    } catch (error) {
      notifyError(error.message);
      this.switcher?.input.focus();
    } finally {
      this.resuming = false;
    }
  }

  buildSwitcherLists() {
    const rows = this.tabs().map((tab) => this.switcherRow(tab));
    const resumeRows = [];
    const inactiveNodes = [];
    const groupLabels = [];
    for (const group of this.querySelectorAll("[data-tabs-resume-fold]")) {
      const project = group.getAttribute("data-tabs-resume-fold");
      const buttons = Array.from(group.querySelectorAll("[data-resume-id]"));
      if (!buttons.length) continue;
      const label = el(
        "div",
        { class: "terminal-switcher-group" },
        el("i", { class: "ti ti-folder", "aria-hidden": "true" }),
        el("span", { class: "text-truncate" }, project),
      );
      groupLabels.push({ key: project, node: label });
      inactiveNodes.push(label);
      buttons.forEach((button, index) => {
        const row = this.resumeRow(button, project, index >= RESUME_FOLD_LIMIT);
        resumeRows.push(row);
        inactiveNodes.push(row);
      });
      if (buttons.length > RESUME_FOLD_LIMIT) {
        const toggle = this.resumeToggleRow(project, buttons.length - RESUME_FOLD_LIMIT);
        resumeRows.push(toggle);
        inactiveNodes.push(toggle);
      }
    }
    const section = inactiveNodes.length
      ? el("div", { class: "terminal-switcher-section" }, "Inactive coders")
      : null;
    return { rows, resumeRows, inactiveNodes, section, groupLabels };
  }

  openSwitcher(direction) {
    const tabs = this.tabs();
    if (!tabs.length) return;
    this.cancelDrag();
    const { rows, resumeRows, inactiveNodes, section, groupLabels } = this.buildSwitcherLists();
    const input = el("input", {
      type: "text",
      class: "terminal-switcher-filter",
      placeholder: "Type to filter…",
      autocomplete: "off",
      autocorrect: "off",
      autocapitalize: "none",
      spellcheck: "false",
      "aria-label": "Filter coders and shells",
    });
    const empty = el("div", { class: "terminal-switcher-empty", hidden: true }, "No matches");
    const overlay = el(
      "div",
      { class: "terminal-switcher", role: "dialog", "aria-modal": "true", "aria-label": "Switch coder or shell" },
      el(
        "div",
        { class: "terminal-switcher-panel" },
        input,
        el("div", { class: "terminal-switcher-list", role: "listbox" }, rows, section, inactiveNodes, empty),
        el("div", { class: "terminal-switcher-hint" }, "Type to filter, Tab or arrows to cycle, Enter to switch, Esc to close"),
      ),
    );
    input.addEventListener("input", () => this.applyFilter(true), { signal: this.ac.signal });
    overlay.addEventListener("pointerdown", (event) => {
      if (event.target === overlay) this.closeSwitcher();
    }, { signal: this.ac.signal });
    document.body.appendChild(overlay);
    const current = rows.findIndex((row) => row.classList.contains("current"));
    this.switcher = {
      overlay,
      rows: rows.concat(resumeRows),
      section,
      groupLabels,
      expanded: new Set(),
      input,
      empty,
      visible: rows.concat(resumeRows),
      index: 0,
      prevFocus: document.activeElement,
    };
    input.focus();
    this.applyFilter();
    this.switcher.index = current === -1 ? 0 : (current + direction + rows.length) % rows.length;
    this.paintSelection();
  }

  rebuildSwitcher() {
    const sw = this.switcher;
    if (!sw) return;
    const key = (row) => {
      if (!row) return "";
      if (row.dataset.switcherToggle) return "t:" + row.dataset.switcherToggle;
      if (row.dataset.switcherResume) return "r:" + row.dataset.switcherResume;
      return "a:" + (row.dataset.switcherId || "");
    };
    const previous = key(sw.visible[sw.index]);
    const { rows, resumeRows, inactiveNodes, section, groupLabels } = this.buildSwitcherLists();
    const list = sw.overlay.querySelector(".terminal-switcher-list");
    const scrollTop = list.scrollTop;
    list.replaceChildren(...rows, ...(section ? [section] : []), ...inactiveNodes, sw.empty);
    sw.rows = rows.concat(resumeRows);
    sw.section = section;
    sw.groupLabels = groupLabels;
    sw.visible = sw.rows.slice();
    this.applyFilter();
    list.scrollTop = scrollTop;
    const kept = sw.visible.findIndex((row) => key(row) === previous);
    if (kept !== -1) {
      sw.index = kept;
      this.paintSelection();
    }
  }

  // refresh marks the strip dirty and pulls the fresh strip and menu when nothing
  // blocks it. A `terminals` event that lands during a fetch, or while a close
  // confirm or a drag owns the strip, is coalesced into `dirty` and re-run once
  // the fetch settles or the gesture releases (tryRefresh from closeTarget /
  // onPointerUp / cancelDrag), so no live change is ever dropped. The pull carries
  // this page's ?path so the active tab and the resume forms' CSRF stay right, and
  // keeps the + menu and switcher current so they need no refetch on open.
  refresh() {
    // Hidden on a coarse pointer (mobile): the strip, + menu and switcher aren't
    // shown there — navigation is the quick nav — so skip the fetch entirely.
    // offsetParent is null only under display:none (a sticky element always has one
    // when shown), so this mirrors the visible/hidden state without duplicating the
    // pointer media query.
    if (this.offsetParent === null) return;
    this.dirty = true;
    this.tryRefresh();
  }

  tryRefresh() {
    if (!this.dirty || this.inFlight || this.confirming || this.drag || !this.dataset.tabsUrl) return;
    this.dirty = false;
    this.inFlight = true;
    // Loading bar only in the + menu or switcher while one is open: the strip
    // refreshes constantly, a bar flashing over the tabs there just distracts.
    const hosts = [
      this.querySelector(".terminal-tabs-new-menu.show"),
      this.switcher?.overlay.querySelector(".terminal-switcher-panel"),
    ].filter(Boolean);
    const bars = hosts.map((host) => {
      const bar = el("div", { class: "dc-loading-bar", role: "status", "aria-label": "Refreshing" });
      host.prepend(bar);
      return bar;
    });
    getText(this.dataset.tabsUrl + "?path=" + encodeURIComponent(window.location.pathname))
      .then((html) => {
        const template = document.createElement("template");
        template.innerHTML = html;
        const fresh = template.content.querySelector("terminal-tabs");
        if (!fresh) return;
        const freshStrip = fresh.querySelector("[data-tabs-strip]");
        if (freshStrip) {
          this.cancelDrag();
          this.strip.innerHTML = freshStrip.innerHTML;
          this.revealActive();
        }
        const menu = this.querySelector(".terminal-tabs-new-menu");
        const freshMenu = fresh.querySelector(".terminal-tabs-new-menu");
        if (menu && freshMenu) {
          menu.innerHTML = freshMenu.innerHTML;
          const projects = menu.querySelector("[data-tabs-resume-projects]");
          if (projects) projectSort.sort(projects);
          menu.querySelectorAll("[data-tabs-resume-fold]").forEach((group) => this.foldResume(group));
        }
        if (this.switcher) this.rebuildSwitcher();
      })
      .catch(() => {})
      .finally(() => {
        bars.forEach((bar) => bar.remove());
        this.inFlight = false;
        this.tryRefresh();
      });
  }

  closeSwitcher() {
    const sw = this.switcher;
    if (!sw) return;
    this.switcher = null;
    sw.overlay.remove();
    if (sw.prevFocus?.isConnected) sw.prevFocus.focus();
  }

  applyFilter(fromInput) {
    const sw = this.switcher;
    if (!sw) return;
    const query = sw.input.value.trim().toLowerCase();
    const selected = sw.visible[sw.index];
    sw.visible = sw.rows.filter((row) => {
      let show;
      if (row.dataset.switcherToggle) {
        show = !query && !sw.expanded.has(row.dataset.switcherToggle);
      } else {
        show = !query || row.dataset.switcherName.toLowerCase().includes(query);
        if (show && row.dataset.switcherFolded && !query && !sw.expanded.has(row.dataset.switcherFolded)) {
          show = false;
        }
      }
      row.hidden = !show;
      return show;
    });
    sw.empty.hidden = sw.visible.length > 0;
    if (sw.section) sw.section.hidden = !sw.visible.some((row) => row.dataset.switcherGroup);
    for (const label of sw.groupLabels) {
      label.node.hidden = !sw.visible.some((row) => row.dataset.switcherGroup === label.key);
    }
    const kept = fromInput ? -1 : sw.visible.indexOf(selected);
    sw.index = kept === -1 ? 0 : kept;
    this.paintSelection();
  }

  paintSelection() {
    const sw = this.switcher;
    if (!sw) return;
    const selected = sw.visible[sw.index];
    for (const row of sw.rows) {
      row.classList.toggle("selected", row === selected);
      row.setAttribute("aria-selected", row === selected ? "true" : "false");
    }
    selected?.scrollIntoView({ block: "nearest" });
  }

  moveSelection(delta) {
    const sw = this.switcher;
    if (!sw || !sw.visible.length) return;
    sw.index = (sw.index + delta + sw.visible.length) % sw.visible.length;
    this.paintSelection();
  }

  commitSelection() {
    const sw = this.switcher;
    if (!sw) return;
    const selected = sw.visible[sw.index];
    if (!selected) return;
    if (selected.dataset.switcherToggle) this.expandGroup(selected.dataset.switcherToggle);
    else if (selected.dataset.switcherResume) void this.resumeTarget(selected);
    else this.navigate(selected.dataset.switcherUrl);
  }

  async closeTarget({ tabId, tabKind, tabName }) {
    if (this.confirming) return;
    const id = tabId;
    const tab = this.strip.querySelector(`.terminal-tab[data-tab-id="${CSS.escape(id)}"]`);
    if (!tab) return;
    const kind = tabKind || tab.dataset.tabKind;
    const name = tabName || tab.dataset.tabName;
    const current = tab.classList.contains("active");
    this.confirming = true;
    try {
      const ok = await confirm({
        title: kind === "coder" ? `Stop coder "${name}"?` : `Delete shell "${name}"?`,
        confirmText: kind === "coder" ? "Stop" : "Delete",
      });
      if (!ok) return;
      window.dispatchEvent(new CustomEvent("dc:terminal-closing", { detail: { id } }));
      const action = kind === "coder" ? `/coders/${id}/stop` : `/shells/${id}/delete`;
      const response = await postForm(action, {});
      await ensureOk(response, "Could not close the session.");
      notifySuccess(kind === "coder" ? `Coder "${name}" stopped.` : `Shell "${name}" deleted.`);
      if (current) {
        const tabs = this.tabs();
        const index = tabs.indexOf(tab);
        const neighbor = tabs[index + 1] || tabs[index - 1] || null;
        this.removeTab(id);
        this.navigate(neighbor?.getAttribute("href") || response.url || "/projects");
        return;
      }
      this.removeTab(id);
    } catch (error) {
      notifyError(error.message);
    } finally {
      this.confirming = false;
      // Flush any event that arrived while the confirm dialog was open.
      this.tryRefresh();
    }
  }

  removeTab(id) {
    this.strip.querySelector(`.terminal-tab[data-tab-id="${CSS.escape(id)}"]`)?.remove();
  }

  navigate(url) {
    this.closeSwitcher();
    if (!url || url === window.location.pathname) return;
    if (window.app?.navigate) window.app.navigate(url);
    else window.location.href = url;
  }
}

customElements.define("terminal-tabs", TerminalTabs);
