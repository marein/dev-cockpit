import { el } from "@dc/dom";

const COMMIT_PX = 72; // finger travel that commits a switch on release
const FLING_VX = 0.5; // px/ms release speed that commits regardless of travel
const MAX_TX = 56; // the terminal frame follows the finger at most this far
const FOLLOW = 0.35; // frame travel per finger px
const PENDING_FAILSAFE_MS = 12000; // drop a stuck pending indicator eventually

// Swipe left or right on the mobile terminal to move between the open
// terminals in tab order. The gesture itself comes from terminal-scroll-zone
// (axis locked horizontal, reported as terminal-swipe events), this element
// owns the rest: it reads the order from the server rendered tab strip (hidden
// on mobile but present and @dc_tab_pos sorted), slides the terminal a damped
// distance under the finger, shows a pill naming the terminal you would land
// on, and navigates through pe.js on release. The pill stays visible as a
// pending indicator until the new page arrives, so a slow connection still
// gives immediate feedback, and further swipes while one is loading chain from
// the pending target like Ctrl+Tab does on the desktop strip.
class TerminalSwipeNav extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.terminal = document.getElementById("terminal") || document.querySelector(".attach-split");
    if (!this.terminal) return;
    this.ac = new AbortController();
    this.gesture = null;
    this.pendingIndex = null;
    this.pill = null;
    this.failsafe = 0;
    document.addEventListener("terminal-swipe", (event) => this.onSwipe(event.detail || {}), { signal: this.ac.signal });
  }

  disconnectedCallback() {
    this.resetFrame();
    this.removePill();
    clearTimeout(this.failsafe);
    this.terminal = null;
    this.gesture = null;
    this.ac?.abort();
    this.ac = null;
  }

  tabs() {
    const activeIsland = document.querySelector("terminal-attach[active]")?.getAttribute("terminal-id") || "";
    return Array.from(document.querySelectorAll("terminal-tabs .terminal-tab")).flatMap((tab) => {
      const members = Array.from(tab.querySelectorAll("[data-member-url]"));
      if (!members.length) {
        return [{
          id: tab.dataset.tabId || "",
          name: tab.dataset.tabName || "",
          url: tab.getAttribute("href") || "",
          icon: tab.querySelector("[data-tab-icon]"),
          active: tab.classList.contains("active"),
        }];
      }
      return members.map((member) => ({
        id: member.getAttribute("data-notify-target") || "",
        name: member.getAttribute("data-member-name") || "",
        url: member.getAttribute("data-member-url") || "",
        icon: member,
        active: tab.classList.contains("active") && member.getAttribute("data-notify-target") === activeIsland,
      }));
    });
  }

  beginGesture() {
    const tabs = this.tabs();
    if (tabs.length < 2) return null;
    let base = this.pendingIndex ?? tabs.findIndex((tab) => tab.active);
    if (base < 0 || base >= tabs.length) base = 0;
    return { tabs, base, dir: 0, dx: 0 };
  }

  onSwipe(detail) {
    if (detail.phase === "move") {
      if (detail.begin || !this.gesture) {
        this.gesture = this.beginGesture();
        if (!this.gesture) return;
      }
      this.moveGesture(detail.dx || 0);
      return;
    }
    const gesture = this.gesture;
    this.gesture = null;
    if (!gesture) return;
    if (detail.phase === "end") {
      this.endGesture(gesture, detail.dx || 0, detail.vx || 0);
      return;
    }
    this.resetFrame();
    if (this.pendingIndex === null) this.removePill();
  }

  // Swiping left brings the next terminal in from the right, swiping right the
  // previous one, wrapping around at both ends of the tab order.
  targetIndex(gesture, dx) {
    const count = gesture.tabs.length;
    return (gesture.base + (dx < 0 ? 1 : -1) + count) % count;
  }

  moveGesture(dx) {
    const gesture = this.gesture;
    gesture.dx = dx;
    const target = this.targetIndex(gesture, dx);
    const tx = Math.max(-MAX_TX, Math.min(MAX_TX, dx * FOLLOW));
    this.terminal.style.transition = "none";
    this.terminal.style.transform = tx ? "translateX(" + tx + "px)" : "";
    this.showPill(gesture.tabs[target], dx < 0 ? 1 : -1, Math.min(1, Math.abs(dx) / COMMIT_PX));
  }

  endGesture(gesture, dx, vx) {
    const target = this.targetIndex(gesture, dx);
    const fling = Math.abs(vx) > FLING_VX && Math.sign(vx) === Math.sign(dx);
    const commit = Math.abs(dx) > COMMIT_PX || fling;
    this.resetFrame();
    if (!commit) {
      if (this.pendingIndex === null) this.removePill();
      return;
    }
    const tab = gesture.tabs[target];
    this.pendingIndex = target;
    this.showPill(tab, dx < 0 ? 1 : -1, 1, true);
    clearTimeout(this.failsafe);
    this.failsafe = setTimeout(() => {
      this.pendingIndex = null;
      this.removePill();
    }, PENDING_FAILSAFE_MS);
    if (!tab.url || tab.url === window.location.pathname + window.location.search) {
      // Swiped back onto the page already showing: abort the in-flight load.
      window.pe?.abortController?.abort();
      this.pendingIndex = null;
      this.removePill();
      return;
    }
    if (window.app?.navigate) Promise.resolve(window.app.navigate(tab.url)).catch(() => {});
    else window.location.href = tab.url;
  }

  resetFrame() {
    if (!this.terminal) return;
    if (!this.terminal.style.transform) {
      this.terminal.style.transition = "";
      return;
    }
    this.terminal.style.transition = "transform 0.18s ease";
    this.terminal.style.transform = "";
    setTimeout(() => {
      if (this.terminal) this.terminal.style.transition = "";
    }, 200);
  }

  showPill(tab, dir, progress, pending) {
    if (!this.pill) {
      this.pill = el("div", { class: "terminal-swipe-pill" });
      this.appendChild(this.pill);
    }
    const key = tab.id + ":" + dir;
    if (this.pill.dataset.key !== key) {
      this.pill.dataset.key = key;
      this.pill.replaceChildren(...[
        dir < 0 ? el("i", { class: "ti ti-chevron-left", "aria-hidden": "true" }) : null,
        tab.icon ? tab.icon.cloneNode(true) : null,
        el("span", { class: "terminal-swipe-pill-name text-truncate" }, tab.name),
        dir > 0 ? el("i", { class: "ti ti-chevron-right", "aria-hidden": "true" }) : null,
      ].filter(Boolean));
    }
    this.pill.style.opacity = String(0.35 + 0.65 * progress);
    this.pill.classList.toggle("terminal-swipe-pill-pending", Boolean(pending));
  }

  removePill() {
    this.pill?.remove();
    this.pill = null;
  }
}

customElements.define("terminal-swipe-nav", TerminalSwipeNav);
