import { SwipeNav } from "@dc/swipe";

// Swipe left or right on the mobile terminal to move between the open
// terminals in tab order. The gesture itself comes from terminal-scroll-zone
// (axis locked horizontal, reported as terminal-swipe events), this element
// reads the order from the server rendered tab strip (hidden on mobile but
// present and @dc_tab_pos sorted) and hands it to SwipeNav from @dc/swipe,
// which slides the terminal a damped distance under the finger, shows a pill
// naming the terminal you would land on, and navigates through pe.js on
// release. The pill stays visible as a pending indicator until the new page
// arrives, so a slow connection still gives immediate feedback, and further
// swipes while one is loading chain from the pending target like Ctrl+Tab
// does on the desktop strip.
class TerminalSwipeNav extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    const terminal = document.getElementById("terminal") || document.querySelector(".attach-split");
    if (!terminal) return;
    this.ac = new AbortController();
    this.nav = new SwipeNav({ frame: terminal, host: this, stops: () => this.tabs(), pillClass: "terminal-swipe-pill" });
    document.addEventListener("terminal-swipe", (event) => this.nav.onSwipe(event.detail || {}), { signal: this.ac.signal });
  }

  disconnectedCallback() {
    this.nav?.destroy();
    this.nav = null;
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
}

customElements.define("terminal-swipe-nav", TerminalSwipeNav);
