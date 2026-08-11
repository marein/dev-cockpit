import { openMenu } from "@dc/contextmenu";
import { confirm, promptText } from "@dc/dialog";
import { ensureOk, postForm, postJSON } from "@dc/http";
import { splitCreateItems } from "@dc/split";
import { get, set } from "@dc/store";
import { releaseCoder, steerCoder } from "@dc/steer";
import { notifyError, notifySuccess } from "@dc/toast";

const DRAG_THRESHOLD = 6;
// The row tracks every column divides, capped like the server's splitLayout.
const MAX_GRID_ROWS = 512;
// What a stacked pane keeps whatever the budget says. A column of many panes
// grows past the rows setting instead of squeezing them into nothing.
const MIN_PANE_ROWS = 4;
// The 1px flex gap between the panes, the border color showing through.
const PANE_GAP = 1;
const DEFAULT_ROWS = 30;
const DEFAULT_FONT_SIZE = 14;

const gcd = (a, b) => (b ? gcd(b, a % b) : a);
const lcm = (a, b) => (a < 1 || b < 1 ? 1 : (a / gcd(a, b)) * b);

// groupColumns folds panes and their column indices into the columns they
// render as: panes sharing an index share a column, a pane without one stands
// alone, and a column stands where its first member stands in the flat order.
// That is the rule the server renders by (splitLayout), and it is what makes a
// group written by an older client render something defined.
const groupColumns = (panes, indices) => {
  const cols = [];
  const byIndex = new Map();
  panes.forEach((pane, i) => {
    const index = Number(indices[i]) || 0;
    let column = index > 0 ? byIndex.get(index) : null;
    if (!column) {
      column = [];
      cols.push(column);
      if (index > 0) byIndex.set(index, column);
    }
    column.push(pane);
  });
  return cols;
};

// signatureOf names a layout, so a drag preview and the strip mirror can tell
// whether anything actually moved before writing styles.
const signatureOf = (columns) => columns
  .map((column) => column.map((pane) => pane.dataset.paneId).join(","))
  .join("|");

class TerminalSplit extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    this.drag = null;
    this.suppressClick = false;
    this.confirming = false;
    this.pendingSync = false;
    this.cellHeight = 0;
    const signal = this.ac.signal;
    this.addEventListener("dc:terminal-metrics", (event) => this.onMetrics(event), { signal });
    document.addEventListener("terminal-setting-change", (event) => {
      if (event.detail?.setting === "rows" || event.detail?.setting === "font-size") this.applyBudget();
    }, { signal });
    this.addEventListener("contextmenu", (event) => this.onContextMenu(event), { signal });
    this.addEventListener("click", (event) => {
      const close = event.target.closest("[data-pane-close]");
      if (!close) return;
      event.preventDefault();
      event.stopPropagation();
      const pane = close.closest(".attach-split-pane");
      if (pane) void this.closePane({ ...pane.dataset });
    }, { signal });
    this.addEventListener("dragstart", (event) => event.preventDefault(), { signal });
    this.addEventListener("pointerdown", (event) => this.onPointerDown(event), { signal });
    this.addEventListener("pointermove", (event) => this.onPointerMove(event), { signal });
    this.addEventListener("pointerup", (event) => this.onPointerUp(event), { signal });
    this.addEventListener("pointercancel", () => this.cancelDrag(), { signal });
    this.addEventListener("click", (event) => this.onClick(event), { signal, capture: true });

    document.addEventListener("keydown", (event) => this.onKeydown(event), { signal, capture: true });

    // The strip's live refresh already tracks every group change (members,
    // order, names); mirror its state into the open panes instead of pulling
    // a second fragment.
    const strip = document.querySelector("terminal-tabs [data-tabs-strip]");
    if (strip) {
      this.observer = new MutationObserver(() => this.syncWithStrip());
      this.observer.observe(strip, { childList: true, subtree: true });
    }
    // Fullscreen gives the panes the whole viewport, so the budget steps
    // aside; the flag lives on the root element as a class.
    this.fullscreenObserver = new MutationObserver(() => this.applyBudget());
    this.fullscreenObserver.observe(document.documentElement, { attributes: true, attributeFilter: ["class"] });
    this.cellHeight = Number(this.querySelector("terminal-attach[data-cell-height]")?.dataset.cellHeight)
      || Number(get(this.cellKey(), ""))
      || 0;
    this.applyBudget();
  }

  disconnectedCallback() {
    this.cancelDrag();
    this.observer?.disconnect();
    this.observer = null;
    this.fullscreenObserver?.disconnect();
    this.fullscreenObserver = null;
    if (this.refreshTimer) {
      clearTimeout(this.refreshTimer);
      this.refreshTimer = null;
    }
    this.ac?.abort();
    this.ac = null;
  }

  groupId() {
    return this.querySelector("terminal-attach[split-group]")?.getAttribute("split-group") || "";
  }

  memberDrift() {
    const gid = this.groupId();
    if (!gid) return null;
    const tab = document.querySelector(`terminal-tabs .terminal-tab-split[data-tab-id="${CSS.escape(gid)}"]`);
    if (!tab) return { tab: null, drifted: true };
    const members = (tab.getAttribute("data-tab-members") || "").split(" ").filter(Boolean);
    const paneIds = this.panes().map((pane) => pane.dataset.paneId);
    const sameSet = members.length === paneIds.length && members.every((id) => paneIds.includes(id));
    return { tab, members, paneIds, drifted: !sameSet };
  }

  deferRefresh() {
    if (this.refreshTimer) return;
    this.refreshTimer = setTimeout(() => {
      this.refreshTimer = null;
      if (!this.isConnected) return;
      if (this.memberDrift()?.drifted) this.refreshPage();
    }, 600);
  }

  syncWithStrip() {
    if (this.drag || this.confirming) {
      this.pendingSync = true;
      return;
    }
    this.pendingSync = false;
    const state = this.memberDrift();
    if (!state) return;
    if (state.drifted) {
      this.deferRefresh();
      return;
    }
    const { tab, members } = state;
    // Order and columns both ride the strip: a layout change made on another
    // device arrives as the group tab's member list plus its column list, and
    // is re-applied in place so the streams stay connected.
    const desired = this.columnsOf(members, (tab.getAttribute("data-tab-member-cols") || "")
      .split(" ").filter(Boolean).map(Number));
    if (desired && signatureOf(desired) !== signatureOf(this.columns())) {
      this.applyColumns(desired);
    }
    for (const span of tab.querySelectorAll("[data-member-name]")) {
      const id = span.getAttribute("data-notify-target") || "";
      const name = span.getAttribute("data-member-name") || "";
      const pane = this.querySelector(`.attach-split-pane[data-pane-id="${CSS.escape(id)}"]`);
      const label = pane?.querySelector("[data-pane-label]");
      if (pane && label && name && label.textContent !== name) {
        label.textContent = name;
        pane.dataset.paneName = name;
      }
    }
    // The page heading and the browser title carry the group name, which the
    // server derives from the member names when the split has none of its own.
    // Renaming a member anywhere therefore has to land here too.
    const group = tab.dataset.tabName || "";
    const heading = document.querySelector("[data-split-title]");
    if (group && heading && heading.textContent !== group) {
      heading.textContent = group;
      document.title = group + (heading.dataset.titleSuffix || "");
    }
  }

  onKeydown(event) {
    if (!event.shiftKey || event.altKey) return;
    const stepping = (event.key === "ArrowLeft" || event.key === "ArrowRight")
      && (event.ctrlKey || event.metaKey);
    // Ctrl+Shift+X on the strip closes the whole split, this closes the one
    // pane. Cmd+Shift+Backspace clears the browsing data in the mac browsers,
    // so the pane close takes Ctrl alone while the step keeps both modifiers.
    const closing = event.key === "Backspace" && event.ctrlKey && !event.metaKey && !event.repeat;
    if (!stepping && !closing) return;
    const target = event.target;
    if (target instanceof Element
      && target.closest("input, [contenteditable], textarea:not(.xterm-helper-textarea)")) return;
    const panes = this.panes();
    if (closing) {
      // No guess at the target: without an active pane the key does nothing.
      const active = panes.find((pane) => pane.querySelector("terminal-attach[active]"));
      if (!active) return;
      event.preventDefault();
      event.stopPropagation();
      void this.closePane({ ...active.dataset });
      return;
    }
    if (panes.length < 2) return;
    event.preventDefault();
    event.stopPropagation();
    const active = panes.findIndex((pane) => pane.querySelector("terminal-attach[active]"));
    const base = active === -1 ? 0 : active;
    const next = (base + (event.key === "ArrowRight" ? 1 : -1) + panes.length) % panes.length;
    document.dispatchEvent(new CustomEvent("dc:activate-pane", { detail: { id: panes[next].dataset.paneId } }));
  }

  panes() {
    return Array.from(this.querySelectorAll(".attach-split-pane"))
      .sort((a, b) => (Number(a.style.order) || 0) - (Number(b.style.order) || 0));
  }

  // ---- Layout ---------------------------------------------------------------
  // A layout is which panes share a column and in which order they stack. The
  // panes stay flat siblings of one grid: moving a pane to another column is a
  // change of its placement, never of its parent, so the terminal island is
  // never re-created and its stream stays connected.

  // columns reads the rendered layout back out of the DOM.
  columns() {
    const panes = this.panes();
    return groupColumns(panes, panes.map((pane) => pane.dataset.paneCol));
  }

  // columnsOf builds a layout out of a member order and its column indices,
  // the pair the strip carries and the group route takes. A member the page
  // does not hold means the member set drifted, which the refresh handles.
  columnsOf(ids, indices) {
    const panes = ids.map((id) => this.querySelector(`.attach-split-pane[data-pane-id="${CSS.escape(id)}"]`));
    if (!panes.length || panes.some((pane) => !pane)) return null;
    return groupColumns(panes, indices);
  }

  applyColumns(cols) {
    let rows = 1;
    for (const column of cols) {
      const next = lcm(rows, column.length);
      if (next <= MAX_GRID_ROWS) rows = next;
    }
    this.style.setProperty("--dc-split-cols", String(cols.length));
    this.style.setProperty("--dc-split-rows", String(rows));
    let flat = 0;
    cols.forEach((column, c) => {
      const span = Math.max(1, Math.floor(rows / column.length));
      column.forEach((pane, r) => {
        const start = r * span + 1;
        // The last pane of a column takes what is left, so a depth the row
        // count does not divide evenly still fills its column.
        const end = r === column.length - 1 ? rows + 1 : start + span;
        pane.dataset.paneCol = String(c + 1);
        pane.style.gridColumn = String(c + 1);
        pane.style.gridRow = `${start} / ${end}`;
        // The flat order is the columns read left to right, top to bottom;
        // that is what @dc_tab_gpos holds and what the strip, the quick nav
        // and the mobile swipe walk.
        pane.style.order = String(flat);
        flat += 1;
      });
    });
    this.applyBudget();
  }

  // ---- The rows budget ------------------------------------------------------
  // The rows setting is the height of the vertical axis, not of every pane: a
  // column shows about that many terminal lines in total, stacked panes share
  // them minus their pane heads, and grouping or stacking never changes the
  // page height. The container therefore carries the height and the panes fit
  // their rows into the box they are given (the fullscreen mechanism, reused).
  applyBudget() {
    if (!this.isConnected) return;
    const off = window.matchMedia("(pointer: coarse)").matches
      || document.documentElement.classList.contains("dc-terminal-fullscreen")
      || !(this.cellHeight > 0);
    if (off) {
      if (this.style.height) {
        this.style.height = "";
        this.style.flex = "";
      }
      return;
    }
    const head = this.querySelector("[data-pane-head]")?.offsetHeight || 0;
    let depth = 1;
    for (const column of this.columns()) depth = Math.max(depth, column.length);
    // The height is a border box, so the container's own border rides along or
    // a single pane column comes out one line short of the setting.
    const border = Math.max(0, this.offsetHeight - this.clientHeight);
    const budget = this.settingValue("rows", DEFAULT_ROWS) * this.cellHeight + head;
    const floor = depth * (head + MIN_PANE_ROWS * this.cellHeight) + (depth - 1) * PANE_GAP;
    const height = `${Math.round(Math.max(budget, floor) + border)}px`;
    if (this.style.height === height) return;
    this.style.height = height;
    this.style.flex = "0 0 auto";
  }

  // One terminal line in pixels: only a rendered terminal knows it, so the
  // islands report it and it is remembered per font size. Without the memory
  // the first paint of every split page would be the flat fallback height and
  // reflow once the first pane has measured itself.
  onMetrics(event) {
    const cell = Number(event.detail?.cell) || 0;
    if (!(cell > 0) || Math.abs(cell - this.cellHeight) < 0.01) return;
    this.cellHeight = cell;
    set(this.cellKey(Number(event.detail?.fontSize) || 0), String(cell));
    this.applyBudget();
  }

  cellKey(fontSize) {
    return `dc-terminal-cell-${fontSize || this.settingValue("font-size", DEFAULT_FONT_SIZE)}`;
  }

  // Read straight from storage like terminal-attach does: the select is lazy
  // loaded and may not have upgraded yet.
  settingValue(setting, fallback) {
    const el = document.querySelector(`terminal-setting-select[setting="${setting}"]`);
    if (!el) return fallback;
    return parseInt(get(el.getAttribute("storage-key") || "", ""), 10)
      || parseInt(el.getAttribute("default-value") || "", 10)
      || fallback;
  }

  refreshPage() {
    const active = this.querySelector("terminal-attach[active]")?.getAttribute("terminal-id");
    const url = window.location.pathname
      + (active ? "?focus=" + encodeURIComponent(active) : window.location.search);
    if (window.app?.navigate) {
      Promise.resolve(window.app.navigate(url)).catch(() => {});
    } else {
      window.location.href = url;
    }
  }

  onClick(event) {
    if (!this.suppressClick) return;
    this.suppressClick = false;
    event.preventDefault();
    event.stopPropagation();
  }

  onContextMenu(event) {
    const head = event.target.closest("[data-pane-head]");
    if (!head) return;
    event.preventDefault();
    this.cancelDrag();
    const pane = head.closest(".attach-split-pane");
    const dataset = { ...pane.dataset };
    const items = [];
    if (dataset.paneKind === "shell") {
      items.push({ label: "Rename", icon: "ti-pencil", action: () => void this.renamePane(pane) });
    }
    if (head.querySelector("[data-notify-target].news")) {
      items.push({
        label: "Mark read",
        icon: "ti-eye-check",
        action: () => void postForm("/notifications/read", { target: dataset.paneId }).catch(() => {}),
      });
    }
    if (dataset.paneKind === "coder") {
      items.push(head.querySelector(".dc-term-icon.steered")
        ? {
          label: "Release",
          icon: "ti-steering-wheel-off",
          purple: true,
          action: () => void releaseCoder({ terminal: dataset.paneId, name: dataset.paneName }),
        }
        : {
          label: "Steer",
          icon: "ti-steering-wheel",
          purple: true,
          action: () => void steerCoder({
            terminal: dataset.paneId,
            name: dataset.paneName,
            prefill: dataset.paneSteerPrefill || "",
          }),
        });
    }
    // The pane the menu was opened on names the target column, which is what
    // makes these two entries unambiguous.
    items.push({ divider: true });
    items.push(...splitCreateItems({
      group: this.groupId(),
      column: dataset.paneId,
      project: dataset.paneProject || "",
    }));
    if (dataset.paneProject) {
      items.push({ divider: true });
      items.push({
        label: "Open project",
        icon: "ti-folder",
        action: () => this.navigate("/projects#project-" + dataset.paneProject),
      });
      items.push({
        label: "Open editor",
        icon: "ti-code",
        action: () => this.navigate(
          "/projects/" + encodeURIComponent(dataset.paneProject) + "/editor?return=" + encodeURIComponent(window.location.pathname),
        ),
      });
    }
    items.push({ divider: true });
    items.push({ label: "Remove from split view", icon: "ti-layout-off", action: () => void this.removePane(dataset.paneId) });
    items.push({
      label: dataset.paneKind === "coder" ? "Stop" : "Delete",
      icon: dataset.paneKind === "coder" ? "ti-player-stop" : "ti-trash",
      danger: dataset.paneKind !== "coder",
      warn: dataset.paneKind === "coder",
      action: () => void this.closePane(dataset),
    });
    if (dataset.paneKind === "coder") {
      items.push({
        label: "Delete",
        icon: "ti-trash",
        danger: true,
        action: () => void this.closePane(dataset, true),
      });
    }
    openMenu({ x: event.clientX, y: event.clientY, items, signal: this.ac.signal });
  }

  async renamePane(pane) {
    if (this.confirming) return;
    this.confirming = true;
    try {
      const current = pane.dataset.paneName || "";
      const name = await promptText({
        title: `Rename shell "${current}"`,
        value: current,
        confirmText: "Rename",
        validatorMessage: "Please enter a name.",
      });
      if (!name || name === current) return;
      const response = await postForm(`/shells/${pane.dataset.paneId}/rename`, { name });
      await ensureOk(response, "Could not rename the shell.");
      pane.dataset.paneName = name;
      const label = pane.querySelector("[data-pane-label]");
      if (label) label.textContent = name;
    } catch (error) {
      notifyError(error.message);
    } finally {
      this.confirming = false;
    }
  }

  async removePane(id) {
    try {
      const response = await postJSON("/terminal-tabs/ungroup", { ids: [id] });
      await ensureOk(response, "Could not change the split view.");
      const data = await response.json();
      if (data.url && data.url !== window.location.pathname) {
        this.navigate(data.url);
      } else {
        this.refreshPage();
      }
    } catch (error) {
      notifyError(error.message);
    }
  }

  async closePane({ paneId, paneKind, paneName }, purge = false) {
    if (this.confirming) return;
    const drop = purge && paneKind === "coder";
    this.confirming = true;
    try {
      const ok = await confirm({
        title: drop ? `Delete coder "${paneName}"?`
          : paneKind === "coder" ? `Stop coder "${paneName}"?` : `Delete shell "${paneName}"?`,
        text: drop ? "It is stopped first, its conversation cannot be resumed afterwards." : undefined,
        confirmText: paneKind === "coder" && !drop ? "Stop" : "Delete",
      });
      if (!ok) return;
      window.dispatchEvent(new CustomEvent("dc:terminal-closing", { detail: { id: paneId } }));
      const action = drop ? `/coders/${paneId}/delete`
        : paneKind === "coder" ? `/coders/${paneId}/stop` : `/shells/${paneId}/delete`;
      const response = await postForm(action, {});
      await ensureOk(response, "Could not close the session.");
      notifySuccess(drop ? `Coder "${paneName}" deleted.`
        : paneKind === "coder" ? `Coder "${paneName}" stopped.` : `Shell "${paneName}" deleted.`);
      this.refreshPage();
    } catch (error) {
      notifyError(error.message);
    } finally {
      this.confirming = false;
    }
  }

  navigate(url) {
    if (!url || url === window.location.pathname) return;
    if (window.app?.navigate) window.app.navigate(url);
    else window.location.href = url;
  }

  onPointerDown(event) {
    if (event.button !== 0 || window.matchMedia("(pointer: coarse)").matches) return;
    const head = event.target.closest("[data-pane-head]");
    if (!head || event.target.closest("[data-pane-close]")) return;
    this.suppressClick = false;
    this.drag = {
      head,
      pane: head.closest(".attach-split-pane"),
      pointerId: event.pointerId,
      startX: event.clientX,
      startY: event.clientY,
      lastX: event.clientX,
      lastY: event.clientY,
      active: false,
    };
    try {
      head.setPointerCapture(event.pointerId);
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
      if (Math.hypot(event.clientX - drag.startX, event.clientY - drag.startY) < DRAG_THRESHOLD) return;
      this.beginDrag();
    }
    event.preventDefault();
    drag.lastX = event.clientX;
    drag.lastY = event.clientY;
    this.updateDrag();
  }

  // The pane head drag is two-dimensional: up and down sorts the pane inside
  // its column, sideways moves it into another one, and a drop on an outer
  // edge opens a column of its own. The geometry is read once, at the start:
  // a preview that changes the column widths would otherwise move the ground
  // the pointer is measured against and the layout would flap under the hand.
  beginDrag() {
    const drag = this.drag;
    drag.active = true;
    drag.columns = this.columns();
    drag.ranges = drag.columns.map((column) => {
      const items = column.map((pane) => {
        const rect = pane.getBoundingClientRect();
        return { pane, mid: rect.top + rect.height / 2, left: rect.left, right: rect.right };
      });
      return {
        left: Math.min(...items.map((item) => item.left)),
        right: Math.max(...items.map((item) => item.right)),
        items,
      };
    });
    drag.signature = signatureOf(drag.columns);
    drag.applied = drag.signature;
    this.classList.add("attach-split-dragging");
    drag.pane.classList.add("attach-split-pane-dragging");
  }

  // dropTarget answers where the pointer wants the pane: inside a column at a
  // row, or as a new column before the first or after the last one.
  dropTarget(x, y) {
    const drag = this.drag;
    const rect = this.getBoundingClientRect();
    // The outer edge is the one place a drop opens a column, so it is wide
    // enough to aim at: it is also the only way to move a pane past the
    // column at the end without joining it.
    const edge = Math.min(96, Math.max(32, rect.width * 0.08));
    if (x < rect.left + edge) return { newColumn: 0 };
    if (x > rect.right - edge) return { newColumn: drag.columns.length };
    let column = drag.ranges.length - 1;
    for (let i = 0; i < drag.ranges.length; i += 1) {
      if (x <= drag.ranges[i].right) {
        column = i;
        break;
      }
    }
    const items = drag.ranges[column].items.filter((item) => item.pane !== drag.pane);
    let row = items.length;
    for (let i = 0; i < items.length; i += 1) {
      if (y < items[i].mid) {
        row = i;
        break;
      }
    }
    return { column, row };
  }

  updateDrag() {
    const drag = this.drag;
    if (!drag || !drag.active) return;
    const target = this.dropTarget(drag.lastX, drag.lastY);
    const newColumn = target.newColumn !== undefined;
    this.classList.toggle("attach-split-edge", newColumn);
    this.classList.toggle("attach-split-edge-start", target.newColumn === 0);
    this.classList.toggle("attach-split-edge-end", target.newColumn === drag.columns.length);
    // Always built from the layout the drag started with, never from the
    // preview standing right now, so the same pointer position always means
    // the same layout.
    const next = drag.columns.map((column) => column.filter((pane) => pane !== drag.pane));
    if (newColumn) next.splice(target.newColumn, 0, [drag.pane]);
    else next[target.column].splice(target.row, 0, drag.pane);
    const cols = next.filter((column) => column.length > 0);
    const signature = signatureOf(cols);
    if (signature === drag.applied) return;
    drag.applied = signature;
    this.applyColumns(cols);
  }

  onPointerUp(event) {
    const drag = this.drag;
    if (!drag || event.pointerId !== drag.pointerId) return;
    this.drag = null;
    if (!drag.active) {
      if (this.pendingSync) this.syncWithStrip();
      return;
    }
    this.suppressClick = true;
    this.clearDragMarks(drag);
    if (drag.applied !== drag.signature) {
      const flat = this.columns().flat();
      postJSON("/terminal-tabs/group", {
        ids: flat.map((pane) => pane.dataset.paneId),
        cols: flat.map((pane) => Number(pane.dataset.paneCol) || 0),
      })
        .then((response) => ensureOk(response, "Could not save the pane layout."))
        .catch((error) => notifyError(error.message));
    }
    if (this.pendingSync) this.syncWithStrip();
  }

  cancelDrag() {
    const drag = this.drag;
    this.drag = null;
    if (!drag || !drag.active) return;
    this.clearDragMarks(drag);
    this.applyColumns(drag.columns);
  }

  clearDragMarks(drag) {
    this.classList.remove("attach-split-dragging", "attach-split-edge", "attach-split-edge-start", "attach-split-edge-end");
    drag.pane.classList.remove("attach-split-pane-dragging");
  }
}

customElements.define("terminal-split", TerminalSplit);
