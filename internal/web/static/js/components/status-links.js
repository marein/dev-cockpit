import { labelNodes } from "@dc/contextmenu";
import { resolveHostlessLinks, splitAddress } from "@dc/docker";
import { onServerEvent } from "@dc/events";
import * as projectSort from "@dc/project-sort";
import { getJSON, setJSON } from "@dc/store";

// The links chip of the status line: the server renders it into every page and
// this element keeps it current, pulling the same template on the docker
// event, the way the tab strip pulls its fragment. The chip and the menu are
// swapped in place, so an open menu stays open. The groups stand in the app's
// project order (@dc/project-sort, sorted here off the keys the rows carry,
// and again the moment the pick changes), and the folds are this browser's:
// the projects a person unfolded stay unfolded across opens and page loads
// (dc-status-links-open), the rest folded, until nothing is stored, when the
// server's default stands, the page's project open. A stored name whose
// project is gone from the list, or answers nothing any more, is dropped.
// A published port has no host of its own, the server writes it as //:port
// and the page's own host completes it here. A routed address is split the
// way the container menus split theirs: the head shrinks with an ellipsis,
// the tail that tells two hosts apart always stands.

const KEY = "dc-status-links-open";
const ROWS = { selector: "[data-links-project]", name: "linksProject", active: "linksActive", used: "linksUsed", worktreeOf: "linksWorktreeOf" };

class StatusLinks extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    this.inFlight = false;
    this.dirty = false;
    resolveHostlessLinks(this);
    this.arrange();
    onServerEvent("docker", () => this.refresh(), { signal: this.ac.signal });
    projectSort.onModeChange(() => this.sortRows(), { signal: this.ac.signal });
    this.addEventListener("click", (event) => this.onClick(event), { signal: this.ac.signal });
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  get dropdown() {
    const toggle = this.querySelector("[data-links-toggle]");
    return toggle ? bootstrap.Dropdown.getInstance(toggle) : null;
  }

  get menu() {
    return this.querySelector("[data-links-menu]");
  }

  rows() {
    return Array.from(this.menu.querySelectorAll(ROWS.selector));
  }

  onClick(event) {
    if (!(event.target instanceof Element)) return;
    const fold = event.target.closest("[data-links-fold]");
    if (fold) {
      this.setOpen(fold.closest(ROWS.selector), fold.getAttribute("aria-expanded") !== "true");
      setJSON(KEY, this.rows().filter((row) => this.isOpen(row)).map((row) => row.dataset.linksProject));
      this.dropdown?.update();
      return;
    }
    if (event.target.closest("a[href]")) this.dropdown?.hide();
  }

  isOpen(row) {
    return row.querySelector("[data-links-fold]").getAttribute("aria-expanded") === "true";
  }

  setOpen(row, open) {
    row.querySelector("[data-links-fold]").setAttribute("aria-expanded", open ? "true" : "false");
    const chevron = row.querySelector("[data-links-chevron]");
    chevron.classList.toggle("ti-chevron-down", open);
    chevron.classList.toggle("ti-chevron-right", !open);
    row.querySelector("[data-links-stacks]").hidden = !open;
  }

  sortRows() {
    projectSort.sort(this.menu, undefined, ROWS);
    this.dropdown?.update();
  }

  labelLinks() {
    for (const link of this.menu.querySelectorAll("a[data-links-route]")) {
      if (link.querySelector(".dc-menu-label-head")) continue;
      link.replaceChildren(link.querySelector(".status-dot"), ...labelNodes(splitAddress(link.dataset.linksRoute)));
    }
  }

  arrange() {
    this.labelLinks();
    this.sortRows();
    const stored = getJSON(KEY, null);
    if (!Array.isArray(stored)) return;
    const names = new Set(this.rows().map((row) => row.dataset.linksProject));
    const kept = stored.filter((name) => names.has(name));
    for (const row of this.rows()) this.setOpen(row, kept.includes(row.dataset.linksProject));
    if (kept.length !== stored.length) setJSON(KEY, kept);
  }

  // The page's path travels along so the server opens the project the page
  // is about, resolved the same way as at render. A pull answered after a
  // navigation swapped this element away is dropped.
  refresh() {
    if (this.inFlight) {
      this.dirty = true;
      return;
    }
    this.inFlight = true;
    const path = window.location.pathname + window.location.search;
    fetch(`/docker/links?path=${encodeURIComponent(path)}`, { credentials: "same-origin", signal: this.ac.signal })
      .then((response) => (response.ok ? response.text() : Promise.reject(new Error("refresh failed"))))
      .then((html) => this.apply(html))
      .catch(() => {})
      .finally(() => {
        this.inFlight = false;
        if (this.dirty) {
          this.dirty = false;
          this.refresh();
        }
      });
  }

  apply(html) {
    if (!this.ac || !this.isConnected) return;
    const fresh = new DOMParser().parseFromString(html, "text/html").querySelector("dc-status-links");
    if (!fresh) return;
    if (fresh.hidden) this.dropdown?.hide();
    this.hidden = fresh.hidden;
    this.querySelector("[data-links-summary]").textContent = fresh.querySelector("[data-links-summary]").textContent;
    // The swap keeps what a person had: the row the keyboard was on and how
    // far the menu was scrolled.
    const focused = this.menu.contains(document.activeElement) ? document.activeElement.closest(ROWS.selector)?.dataset.linksProject : null;
    const top = this.menu.scrollTop;
    this.menu.replaceChildren(...fresh.querySelector("[data-links-menu]").childNodes);
    resolveHostlessLinks(this.menu);
    this.arrange();
    this.menu.scrollTop = top;
    if (focused) this.menu.querySelector(`[data-links-project="${CSS.escape(focused)}"] [data-links-fold]`)?.focus({ preventScroll: true });
    this.dropdown?.update();
  }
}

customElements.define("dc-status-links", StatusLinks);
