import { confirm } from "@dc/dialog";
import { el } from "@dc/dom";
import { ensureOk, postForm } from "@dc/http";
import { getJSON, setJSON } from "@dc/store";
import { notifyError, notifySuccess } from "@dc/toast";

const ORDER_KEY = "dc-terminal-tab-order";
const DRAG_THRESHOLD = 6;
const EDGE_ZONE = 32;
const EDGE_STEP = 12;
const TAP_WINDOW_MS = 400;

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
    this.tap = { pending: null, lastKey: null, lastTime: 0 };
    const signal = this.ac.signal;

    this.applyOrder();
    this.revealActive();

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

  applyOrder() {
    const stored = getJSON(ORDER_KEY, []);
    if (!Array.isArray(stored)) return this.persistOrder();
    const byId = new Map(this.tabs().map((tab) => [tab.dataset.tabId, tab]));
    for (const id of stored) {
      const tab = byId.get(id);
      if (!tab) continue;
      byId.delete(id);
      this.strip.appendChild(tab);
    }
    for (const tab of byId.values()) {
      this.strip.appendChild(tab);
    }
    this.persistOrder();
  }

  persistOrder() {
    setJSON(ORDER_KEY, this.tabs().map((tab) => tab.dataset.tabId));
  }

  revealActive() {
    const active = this.strip.querySelector(".terminal-tab.active");
    if (!active) return;
    const strip = this.strip;
    if (active.offsetLeft < strip.scrollLeft
      || active.offsetLeft + active.offsetWidth > strip.scrollLeft + strip.clientWidth) {
      strip.scrollLeft = active.offsetLeft - (strip.clientWidth - active.offsetWidth) / 2;
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
    if (!drag.active) return;
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
      else this.openSwitcher(event.shiftKey ? -1 : 1);
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

  switcherRow(tab) {
    const current = tab.classList.contains("active");
    const kind = tab.dataset.tabKind || "shell";
    const close = el(
      "button",
      {
        type: "button",
        class: "terminal-switcher-close",
        "aria-label": (kind === "coder" ? "Stop coder " : "Delete shell ") + (tab.dataset.tabName || ""),
        title: kind === "coder" ? "Stop coder" : "Delete shell",
      },
      el("i", { class: "ti ti-x", "aria-hidden": "true" }),
    );
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
      tab.querySelector(".status-dot")?.cloneNode(true),
      tab.querySelector("[data-tab-icon]")?.cloneNode(true),
      el("span", { class: "terminal-switcher-name text-truncate" }, tab.dataset.tabName || ""),
      tab.dataset.tabProject
        ? el("span", { class: "terminal-switcher-project text-truncate" }, tab.dataset.tabProject)
        : null,
      current ? el("span", { class: "terminal-switcher-badge" }, "current") : null,
      close,
    );
    close.addEventListener("click", (event) => {
      event.stopPropagation();
      void this.closeTarget(tab.dataset);
    }, { signal: this.ac.signal });
    row.addEventListener("click", () => this.navigate(row.dataset.switcherUrl), { signal: this.ac.signal });
    return row;
  }

  openSwitcher(direction) {
    const tabs = this.tabs();
    if (!tabs.length) return;
    this.cancelDrag();
    const rows = tabs.map((tab) => this.switcherRow(tab));
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
        el("div", { class: "terminal-switcher-list", role: "listbox" }, rows, empty),
        el("div", { class: "terminal-switcher-hint" }, "Type to filter, Tab or arrows to cycle, Enter to switch, Esc to close"),
      ),
    );
    input.addEventListener("input", () => this.applyFilter(true), { signal: this.ac.signal });
    overlay.addEventListener("pointerdown", (event) => {
      if (event.target === overlay) this.closeSwitcher();
    }, { signal: this.ac.signal });
    this.appendChild(overlay);
    const current = rows.findIndex((row) => row.classList.contains("current"));
    this.switcher = {
      overlay,
      rows,
      input,
      empty,
      visible: rows.slice(),
      index: current === -1 ? 0 : current,
      prevFocus: document.activeElement,
    };
    input.focus();
    this.moveSelection(current === -1 ? 0 : direction);
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
      const match = !query || row.dataset.switcherName.toLowerCase().includes(query);
      row.hidden = !match;
      return match;
    });
    sw.empty.hidden = sw.visible.length > 0;
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
    if (selected) this.navigate(selected.dataset.switcherUrl);
  }

  async closeTarget({ tabId, tabKind, tabName, switcherId }) {
    if (this.confirming) return;
    const id = tabId || switcherId;
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
      this.switcher?.input.focus();
    }
  }

  removeTab(id) {
    this.strip.querySelector(`.terminal-tab[data-tab-id="${CSS.escape(id)}"]`)?.remove();
    this.persistOrder();
    const sw = this.switcher;
    if (!sw) return;
    const index = sw.rows.findIndex((row) => row.dataset.switcherId === id);
    if (index !== -1) {
      sw.rows[index].remove();
      sw.rows.splice(index, 1);
    }
    this.applyFilter();
  }

  navigate(url) {
    this.closeSwitcher();
    if (!url || url === window.location.pathname) return;
    if (window.app?.navigate) window.app.navigate(url);
    else window.location.href = url;
  }
}

customElements.define("terminal-tabs", TerminalTabs);
