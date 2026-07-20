import { openMenu } from "@dc/contextmenu";
import { confirm, promptText } from "@dc/dialog";
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
const GROUP_ZONE_RATIO = 0.3;
const GROUP_DWELL_MS = 220;

class TerminalTabs extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.strip = this.querySelector("[data-tabs-strip]");
    if (!this.strip) return;
    this.switcherOnly = this.hasAttribute("hidden");
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
    const editors = this.querySelector("[data-tabs-editors]");
    if (editors) projectSort.sort(editors);
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
    this.strip.addEventListener("contextmenu", (event) => this.onContextMenu(event), { signal });
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

  memberIds(tab) {
    return (tab.dataset.tabMembers || tab.dataset.tabId || "").split(" ").filter(Boolean);
  }

  persistOrder() {
    const ids = this.tabs().flatMap((tab) => this.memberIds(tab));
    postJSON("/terminal-tabs/order", { ids })
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

  onContextMenu(event) {
    const tab = event.target.closest(".terminal-tab");
    if (!tab) return;
    event.preventDefault();
    this.cancelDrag();
    const dataset = { ...tab.dataset };
    const split = dataset.tabKind === "split";
    const items = [];
    if (dataset.tabKind === "shell") {
      items.push({ label: "Rename", icon: "ti-pencil", action: () => void this.renameShell(dataset) });
    }
    if (split) {
      items.push({ label: "Rename split view", icon: "ti-pencil", action: () => void this.renameSplit(dataset) });
    }
    let newsTargets = [...tab.querySelectorAll("[data-notify-target].news")]
      .map((icon) => icon.getAttribute("data-notify-target")).filter(Boolean);
    if (split && !newsTargets.length && tab.querySelector("[data-tab-icon].news")) {
      newsTargets = this.memberIds(tab);
    }
    if (newsTargets.length) {
      items.push({
        label: "Mark read",
        icon: "ti-eye-check",
        action: () => newsTargets.forEach((id) => void this.markRead(id)),
      });
    }
    if (dataset.tabProject) {
      items.push({ divider: true });
      items.push({
        label: "Open project",
        icon: "ti-folder",
        action: () => this.navigate("/projects#project-" + dataset.tabProject),
      });
      items.push({
        label: "Open editor",
        icon: "ti-code",
        action: () => this.navigate(
          "/projects/" + encodeURIComponent(dataset.tabProject) + "/editor?return=" + encodeURIComponent(window.location.pathname),
        ),
      });
    }
    items.push({ divider: true });
    if (split) {
      items.push({ label: "Ungroup split view", icon: "ti-layout-off", action: () => void this.ungroupSplit(dataset) });
      items.push({
        label: "Close all terminals",
        icon: "ti-trash",
        danger: true,
        action: () => void this.closeSplitMembers(dataset),
      });
    } else {
      items.push({
        label: dataset.tabKind === "coder" ? "Stop coder" : "Delete shell",
        icon: dataset.tabKind === "coder" ? "ti-player-stop" : "ti-trash",
        danger: true,
        action: () => void this.closeTarget(dataset),
      });
    }
    openMenu({ x: event.clientX, y: event.clientY, items, signal: this.ac.signal });
  }

  async groupTabs(base, added) {
    const involved = base.classList.contains("active") || added.classList.contains("active");
    const ids = [...this.memberIds(base), ...this.memberIds(added)];
    try {
      const response = await postJSON("/terminal-tabs/group", { ids });
      await ensureOk(response, "Could not create the split view.");
      const data = await response.json();
      if (involved && data.url) {
        if (data.url === window.location.pathname) {
          if (window.app?.navigate) Promise.resolve(window.app.navigate(data.url)).catch(() => {});
        } else {
          this.navigate(data.url);
        }
      }
    } catch (error) {
      notifyError(error.message);
    }
  }

  async renameSplit({ tabId, tabName }) {
    if (this.confirming) return;
    this.confirming = true;
    try {
      const name = await promptText({
        title: `Rename split view "${tabName}"`,
        value: tabName,
        confirmText: "Rename",
        allowEmpty: true,
      });
      if (name === null || name === tabName) return;
      const response = await postJSON("/terminal-tabs/group/name", { group: tabId, name });
      await ensureOk(response, "Could not rename the split view.");
    } catch (error) {
      notifyError(error.message);
    } finally {
      this.confirming = false;
      this.tryRefresh();
    }
  }

  async ungroupSplit({ tabId, tabName }) {
    const tab = this.strip.querySelector(`.terminal-tab[data-tab-id="${CSS.escape(tabId)}"]`);
    if (!tab) return;
    const current = tab.classList.contains("active");
    try {
      const response = await postJSON("/terminal-tabs/ungroup", { ids: this.memberIds(tab) });
      await ensureOk(response, "Could not ungroup the split view.");
      const data = await response.json();
      notifySuccess(`Split view "${tabName}" ungrouped.`);
      if (current) this.navigate(data.url || "/projects");
    } catch (error) {
      notifyError(error.message);
    } finally {
      this.tryRefresh();
    }
  }

  async closeSplitMembers({ tabId, tabName }) {
    if (this.confirming) return;
    const tab = this.strip.querySelector(`.terminal-tab[data-tab-id="${CSS.escape(tabId)}"]`);
    if (!tab) return;
    const ids = this.memberIds(tab);
    const kinds = (tab.dataset.tabMemberKinds || "").split(" ").filter(Boolean);
    const current = tab.classList.contains("active");
    this.confirming = true;
    try {
      const ok = await confirm({
        title: `Close all ${ids.length} terminals in "${tabName}"?`,
        confirmText: "Close all",
      });
      if (!ok) return;
      let failed = 0;
      for (let i = 0; i < ids.length; i += 1) {
        window.dispatchEvent(new CustomEvent("dc:terminal-closing", { detail: { id: ids[i] } }));
        const action = kinds[i] === "shell" ? `/shells/${ids[i]}/delete` : `/coders/${ids[i]}/stop`;
        try {
          const response = await postForm(action, {});
          await ensureOk(response, "Could not close the session.");
        } catch (memberError) {
          void memberError;
          failed += 1;
        }
      }
      if (failed) notifyError(`Could not close ${failed} of ${ids.length} terminals.`);
      else notifySuccess(`Split view "${tabName}" closed.`);
      this.removeTab(tabId);
      if (current) {
        const tabs = this.tabs();
        this.navigate(tabs[0]?.getAttribute("href") || "/projects");
      }
    } catch (error) {
      notifyError(error.message);
    } finally {
      this.confirming = false;
      this.tryRefresh();
    }
  }

  async renameShell({ tabId, tabName }) {
    if (this.confirming) return;
    this.confirming = true;
    try {
      const name = await promptText({
        title: `Rename shell "${tabName}"`,
        value: tabName,
        confirmText: "Rename",
        validatorMessage: "Please enter a name.",
      });
      if (!name || name === tabName) return;
      const response = await postForm(`/shells/${tabId}/rename`, { name });
      await ensureOk(response, "Could not rename the shell.");
    } catch (error) {
      notifyError(error.message);
    } finally {
      this.confirming = false;
      this.tryRefresh();
    }
  }

  async markRead(id) {
    try {
      const response = await postForm("/notifications/read", { target: id });
      await ensureOk(response, "Could not mark the news read.");
    } catch (error) {
      notifyError(error.message);
    }
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
        tab.classList.remove("terminal-tab-group-target");
      }
      if (drag.groupTarget >= 0 && drag.tabs[drag.groupTarget]) {
        void this.groupTabs(drag.tabs[drag.groupTarget], drag.tab);
      } else if (drag.toIndex !== drag.fromIndex) {
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
      tab.classList.remove("terminal-tab-group-target");
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
    drag.widths = drag.tabs.map((tab) => tab.getBoundingClientRect().width);
    drag.startContentX = this.contentX(event.clientX);
    drag.groupTarget = -1;
    drag.groupPending = -1;
    drag.groupSince = 0;
    this.strip.classList.add("terminal-tabs-strip-dragging");
    drag.tab.classList.add("terminal-tab-dragging");
    drag.raf = window.requestAnimationFrame(() => this.tickEdgeScroll());
  }

  groupCandidate(drag, draggedCenter) {
    for (let i = 0; i < drag.tabs.length; i += 1) {
      if (i === drag.fromIndex) continue;
      let shift = 0;
      if (i > drag.fromIndex && i <= drag.toIndex) shift = -drag.width;
      else if (i < drag.fromIndex && i >= drag.toIndex) shift = drag.width;
      const visualCenter = drag.centers[i] + shift;
      if (Math.abs(draggedCenter - visualCenter) < drag.widths[i] * GROUP_ZONE_RATIO) {
        return i;
      }
    }
    return -1;
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
    drag.tabs.forEach((tab, i) => {
      tab.classList.toggle("terminal-tab-group-target", i === drag.groupTarget);
    });
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
    if (drag.groupPending >= 0 && drag.groupTarget === -1
      && Date.now() - drag.groupSince >= GROUP_DWELL_MS) {
      this.updateDrag();
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
      if (this.switcherOnly && !this.switcher) return;
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

  editorRow(link) {
    const project = link.dataset.editorProject || "";
    const row = el(
      "div",
      {
        role: "option",
        class: "terminal-switcher-item",
        dataset: {
          switcherUrl: link.getAttribute("href") || "",
          switcherName: project + " editor",
          switcherSection: "editors",
        },
      },
      el("span", { class: "terminal-tab-icon dc-term-icon", "aria-hidden": "true" }, el("i", { class: "ti ti-code" })),
      el("span", { class: "terminal-switcher-name text-truncate" }, project),
      el("span", { class: "terminal-switcher-project text-truncate" }, "Editor"),
    );
    row.addEventListener("click", () => this.navigate(row.dataset.switcherUrl), { signal: this.ac.signal });
    return row;
  }

  actionRow(link) {
    const kind = link.dataset.tabsNew;
    const label = kind === "coder" ? "New coder" : "New shell";
    const url = link.getAttribute("href") || "";
    const project = new URL(url, window.location.href).searchParams.get("project") || "";
    const row = el(
      "div",
      {
        role: "option",
        class: "terminal-switcher-item",
        dataset: {
          switcherUrl: url,
          switcherName: label + " " + project,
          switcherSection: "new",
        },
      },
      el("span", { class: "terminal-tab-icon dc-term-icon", "aria-hidden": "true" }, el("i", { class: kind === "coder" ? "ti ti-code-ai" : "ti ti-terminal-2" })),
      el("span", { class: "terminal-switcher-name text-truncate" }, label),
      project ? el("span", { class: "terminal-switcher-project text-truncate" }, project) : null,
    );
    row.addEventListener("click", () => this.navigate(row.dataset.switcherUrl), { signal: this.ac.signal });
    return row;
  }

  resumeRow(button, project, folded) {
    const dataset = {
      switcherResume: button.dataset.resumeId || "",
      switcherName: (button.dataset.resumeName || "") + " " + (project || ""),
      switcherGroup: project,
      switcherSection: "inactive",
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
        dataset: { switcherToggle: project, switcherGroup: project, switcherSection: "inactive" },
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
    const editorRows = Array.from(this.querySelectorAll("[data-tabs-editors] [data-editor-project]"))
      .map((link) => this.editorRow(link));
    const actionRows = Array.from(this.querySelectorAll(".terminal-tabs-new-menu [data-tabs-new]"))
      .map((link) => this.actionRow(link));
    const sections = [];
    const listNodes = [...rows];
    const addSection = (key, title, nodes) => {
      if (!nodes.length) return;
      const node = el("div", { class: "terminal-switcher-section", dataset: { switcherSection: key } }, title);
      sections.push({ key, node });
      listNodes.push(node, ...nodes);
    };
    addSection("inactive", "Inactive coders", inactiveNodes);
    addSection("editors", "Editors", editorRows);
    addSection("new", "New", actionRows);
    const cycleRows = rows.concat(resumeRows, editorRows, actionRows);
    return { rows, cycleRows, listNodes, sections, groupLabels };
  }

  openSwitcher(direction) {
    this.cancelDrag();
    const { rows, cycleRows, listNodes, sections, groupLabels } = this.buildSwitcherLists();
    if (!cycleRows.length) return;
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
        el("div", { class: "terminal-switcher-list", role: "listbox" }, listNodes, empty),
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
      rows: cycleRows,
      sections,
      groupLabels,
      expanded: new Set(),
      input,
      empty,
      visible: cycleRows.slice(),
      index: 0,
      prevFocus: document.activeElement,
    };
    input.focus();
    this.applyFilter();
    this.switcher.index = current === -1 ? 0 : (current + direction + rows.length) % rows.length;
    this.paintSelection();
    this.tryRefresh();
  }

  rebuildSwitcher() {
    const sw = this.switcher;
    if (!sw) return;
    const key = (row) => {
      if (!row) return "";
      if (row.dataset.switcherToggle) return "t:" + row.dataset.switcherToggle;
      if (row.dataset.switcherResume) return "r:" + row.dataset.switcherResume;
      return "a:" + (row.dataset.switcherId || row.dataset.switcherUrl || "");
    };
    const previous = key(sw.visible[sw.index]);
    const { cycleRows, listNodes, sections, groupLabels } = this.buildSwitcherLists();
    const list = sw.overlay.querySelector(".terminal-switcher-list");
    const scrollTop = list.scrollTop;
    list.replaceChildren(...listNodes, sw.empty);
    sw.rows = cycleRows;
    sw.sections = sections;
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
    // The strip refreshes even while hidden on a coarse pointer (mobile): it is
    // invisible there, but terminal-swipe-nav reads its rows as the swipe
    // targets, so the order must stay live.
    this.dirty = true;
    if (this.switcherOnly && !this.switcher) return;
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
    let path = window.location.pathname;
    const focused = document.querySelector("terminal-attach[active][split-group]");
    if (focused) path += "?focus=" + encodeURIComponent(focused.getAttribute("terminal-id") || "");
    getText(this.dataset.tabsUrl + "?path=" + encodeURIComponent(path))
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
          const editors = menu.querySelector("[data-tabs-editors]");
          if (editors) projectSort.sort(editors);
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
    for (const section of sw.sections) {
      section.node.hidden = !sw.visible.some((row) => row.dataset.switcherSection === section.key);
    }
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
    if (kind === "split") {
      void this.closeSplitMembers({ tabId: id, tabName: name });
      return;
    }
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
