import { openMenu } from "@dc/contextmenu";
import { confirm, promptText } from "@dc/dialog";
import { ensureOk, postForm, postJSON } from "@dc/http";
import { notifyError, notifySuccess } from "@dc/toast";

const DRAG_THRESHOLD = 6;

class TerminalSplit extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    this.drag = null;
    this.suppressClick = false;
    this.confirming = false;
    this.pendingSync = false;
    const signal = this.ac.signal;
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

    const headerClose = document.querySelector("[data-split-close]");
    headerClose?.addEventListener("click", (event) => {
      event.preventDefault();
      void this.closeSplit();
    }, { signal });

    document.addEventListener("keydown", (event) => this.onKeydown(event), { signal, capture: true });

    // The strip's live refresh already tracks every group change (members,
    // order, names); mirror its state into the open panes instead of pulling
    // a second fragment.
    const strip = document.querySelector("terminal-tabs [data-tabs-strip]");
    if (strip) {
      this.observer = new MutationObserver(() => this.syncWithStrip());
      this.observer.observe(strip, { childList: true, subtree: true });
    }
  }

  disconnectedCallback() {
    this.cancelDrag();
    this.observer?.disconnect();
    this.observer = null;
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
    const { tab, members, paneIds } = state;
    if (members.join(" ") !== paneIds.join(" ")) {
      members.forEach((id, index) => {
        const pane = this.querySelector(`.attach-split-pane[data-pane-id="${CSS.escape(id)}"]`);
        if (pane) pane.style.order = String(index);
      });
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
  }

  onKeydown(event) {
    if (!(event.ctrlKey || event.metaKey) || !event.shiftKey || event.altKey) return;
    if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
    const target = event.target;
    if (target instanceof Element
      && target.closest("input, [contenteditable], textarea:not(.xterm-helper-textarea)")) return;
    const panes = this.panes();
    if (panes.length < 2) return;
    event.preventDefault();
    event.stopPropagation();
    const active = panes.findIndex((pane) => pane.querySelector("terminal-attach[active]"));
    const base = active === -1 ? 0 : active;
    const next = (base + (event.key === "ArrowRight" ? 1 : -1) + panes.length) % panes.length;
    document.dispatchEvent(new CustomEvent("dc:activate-pane", { detail: { id: panes[next].dataset.paneId } }));
  }

  async closeSplit() {
    if (this.confirming) return;
    const panes = this.panes();
    this.confirming = true;
    try {
      const ok = await confirm({
        title: `Close all ${panes.length} terminals in this split view?`,
        confirmText: "Close all",
      });
      if (!ok) return;
      let failed = 0;
      let lastUrl = "";
      for (const pane of panes) {
        const { paneId, paneKind } = pane.dataset;
        window.dispatchEvent(new CustomEvent("dc:terminal-closing", { detail: { id: paneId } }));
        const action = paneKind === "shell" ? `/shells/${paneId}/delete` : `/coders/${paneId}/stop`;
        try {
          const response = await postForm(action, {});
          await ensureOk(response, "Could not close the session.");
          lastUrl = response.url || lastUrl;
        } catch (memberError) {
          void memberError;
          failed += 1;
        }
      }
      if (failed) {
        notifyError(`Could not close ${failed} of ${panes.length} terminals.`);
        this.refreshPage();
        return;
      }
      notifySuccess("Split view closed.");
      const url = lastUrl || "/projects";
      if (window.app?.navigate) Promise.resolve(window.app.navigate(url)).catch(() => {});
      else window.location.href = url;
    } finally {
      this.confirming = false;
      if (this.pendingSync) this.syncWithStrip();
    }
  }

  panes() {
    return Array.from(this.querySelectorAll(".attach-split-pane"))
      .sort((a, b) => (Number(a.style.order) || 0) - (Number(b.style.order) || 0));
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
      label: dataset.paneKind === "coder" ? "Stop coder" : "Delete shell",
      icon: dataset.paneKind === "coder" ? "ti-player-stop" : "ti-trash",
      danger: true,
      action: () => void this.closePane(dataset),
    });
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

  async closePane({ paneId, paneKind, paneName }) {
    if (this.confirming) return;
    this.confirming = true;
    try {
      const ok = await confirm({
        title: paneKind === "coder" ? `Stop coder "${paneName}"?` : `Delete shell "${paneName}"?`,
        confirmText: paneKind === "coder" ? "Stop" : "Delete",
      });
      if (!ok) return;
      window.dispatchEvent(new CustomEvent("dc:terminal-closing", { detail: { id: paneId } }));
      const action = paneKind === "coder" ? `/coders/${paneId}/stop` : `/shells/${paneId}/delete`;
      const response = await postForm(action, {});
      await ensureOk(response, "Could not close the session.");
      notifySuccess(paneKind === "coder" ? `Coder "${paneName}" stopped.` : `Shell "${paneName}" deleted.`);
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
    this.updateDrag();
  }

  beginDrag() {
    const drag = this.drag;
    drag.active = true;
    drag.panes = this.panes();
    drag.fromIndex = drag.panes.indexOf(drag.pane);
    drag.toIndex = drag.fromIndex;
    drag.panes.forEach((pane, i) => {
      pane.style.order = String(i);
    });
    this.classList.add("attach-split-dragging");
    drag.pane.classList.add("attach-split-pane-dragging");
  }

  updateDrag() {
    const drag = this.drag;
    if (!drag || !drag.active) return;
    const rect = this.getBoundingClientRect();
    const count = drag.panes.length;
    const toIndex = Math.max(0, Math.min(count - 1, Math.floor((drag.lastX - rect.left) / (rect.width / count))));
    if (toIndex === drag.toIndex) return;
    drag.toIndex = toIndex;
    const order = drag.panes.filter((pane) => pane !== drag.pane);
    order.splice(toIndex, 0, drag.pane);
    order.forEach((pane, i) => {
      pane.style.order = String(i);
    });
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
    this.classList.remove("attach-split-dragging");
    drag.pane.classList.remove("attach-split-pane-dragging");
    if (drag.toIndex !== drag.fromIndex) {
      const ids = this.panes().map((pane) => pane.dataset.paneId);
      postJSON("/terminal-tabs/group", { ids })
        .then((response) => ensureOk(response, "Could not save the pane order."))
        .catch((error) => notifyError(error.message));
    }
    if (this.pendingSync) this.syncWithStrip();
  }

  cancelDrag() {
    const drag = this.drag;
    this.drag = null;
    if (!drag || !drag.active) return;
    this.classList.remove("attach-split-dragging");
    drag.pane.classList.remove("attach-split-pane-dragging");
    drag.panes.forEach((pane, i) => {
      pane.style.order = String(i);
    });
  }
}

customElements.define("terminal-split", TerminalSplit);
