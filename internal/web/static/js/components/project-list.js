import * as store from "@dc/store";
import * as projectSort from "@dc/project-sort";
import { confirm, promptText } from "@dc/dialog";
import { el } from "@dc/dom";
import { onServerEvent } from "@dc/events";
import { applyFold } from "@dc/fold";
import { openMenu, wireRowMenus } from "@dc/contextmenu";
import { ensureOk, postForm } from "@dc/http";
import { notifyError } from "@dc/toast";

const FILTER_KEY = "dc-project-filter";
const CHIP_LIMIT = 8;

// Owns the projects page interactivity: a frontend-only name filter, the sort
// switcher, the chip fold, the chip context menu, and the in place Stop/Delete
// on the session chips. Filter and sort persist to localStorage so they survive
// reloads, tabs and restarts. Filtering only hides rows; sorting reorders them;
// the two are orthogonal. The sort order is shared with the quick nav through
// @dc/project-sort (same key and comparator).
class ProjectList extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    this.inFlight = false;
    this.dirty = false;
    this.setupSort();
    this.setupFilter();
    this.querySelectorAll("[data-sessions-body]").forEach((body) => this.foldChips(body));
    this.addEventListener("submit", (event) => this.onAjaxSubmit(event), { signal: this.ac.signal });
    wireRowMenus(this, "[data-chip]", (chip, x, y) => {
      if (!chip) return false;
      this.openChipMenu(chip, x, y);
      return true;
    }, { signal: this.ac.signal });
    // A coder or shell started, stopped, was renamed or reordered somewhere: pull
    // a fresh /projects render and swap the affected chip lists in place, so the
    // start page tracks the tab strip live without disturbing unfolded chips, the
    // filter or the scroll. A project-less event (reorder, connect snapshot)
    // touches every project.
    onServerEvent("terminals", (event) => this.applyTerminals(event.detail), { signal: this.ac.signal });
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  // Stop and Delete on a chip (data-ajax-refresh) act in place.
  async onAjaxSubmit(event) {
    const form = event.target;
    if (!(form instanceof HTMLFormElement) || form.dataset.ajaxRefresh === undefined) return;
    event.preventDefault();
    event.stopPropagation();
    if (form.dataset.confirm) {
      const ok = await confirm({ title: form.dataset.confirm, confirmText: form.dataset.confirmButton || "Confirm" });
      if (!ok) return;
    }
    this.ajaxRefresh(form);
  }

  // The chip actions, offered on right click and on touch long press through
  // the shared wireRowMenus gesture, plus shell rename.
  openChipMenu(chip, x, y) {
    const main = chip.querySelector("[data-chip-main]");
    const mainForm = chip.querySelector("[data-chip-main-form]");
    const xForm = chip.querySelector("[data-chip-x]");
    const items = [];
    if (main) items.push({ label: "Attach", icon: "ti-plug-connected", action: () => main.click() });
    if (mainForm) items.push({ label: "Resume", icon: "ti-player-play", action: () => mainForm.requestSubmit() });
    if (chip.dataset.chipKind === "shell" && chip.dataset.chipId) {
      items.push({ label: "Rename", icon: "ti-pencil", action: () => void this.renameShell(chip) });
    }
    if (xForm) {
      items.push({ divider: true });
      const stop = xForm.action.endsWith("/stop");
      items.push({
        label: xForm.dataset.confirmButton || "Delete",
        icon: stop ? "ti-player-stop" : "ti-trash",
        danger: true,
        action: () => xForm.requestSubmit(),
      });
    }
    openMenu({ x, y, items, signal: this.ac.signal });
  }

  async renameShell(chip) {
    const current = chip.dataset.chipName || "";
    try {
      const name = await promptText({
        title: `Rename shell "${current}"`,
        value: current,
        confirmText: "Rename",
        validatorMessage: "Please enter a name.",
      });
      if (!name || name === current) return;
      const response = await postForm(`/shells/${chip.dataset.chipId}/rename`, { name });
      await ensureOk(response, "Could not rename the shell.");
      chip.dataset.chipName = name;
      const label = chip.querySelector(".project-chip-name");
      if (label) label.textContent = name;
    } catch (error) {
      notifyError(error.message);
    }
  }

  // Collapses a long chip list to the first few entries behind a "+N" chip. The
  // expanded flag lives on the persistent [data-sessions-body] container, so it
  // survives the live swaps.
  foldChips(body) {
    const fold = body.querySelector("[data-chip-fold]");
    if (!fold) return;
    const expanded = body.dataset.chipsExpanded === "1";
    applyFold(fold, {
      limit: CHIP_LIMIT,
      items: Array.from(fold.querySelectorAll(":scope > [data-chip]")),
      expanded,
      toggleAttr: "data-chips-toggle",
      toggleClass: "project-chip project-chip-more",
      label: (count) => `+${count}`,
      collapsedLabel: "Show less",
      signal: this.ac?.signal,
      onToggle: (event, next) => {
        body.dataset.chipsExpanded = next ? "1" : "";
        this.foldChips(body);
      },
    });
    const toggle = fold.querySelector("[data-chips-toggle]");
    if (toggle) {
      const hidden = fold.querySelectorAll(":scope > [data-chip]").length - CHIP_LIMIT;
      toggle.setAttribute("aria-label", expanded ? "Show fewer sessions" : `Show ${hidden} more sessions`);
      toggle.setAttribute("title", expanded ? "Show fewer sessions" : `Show ${hidden} more sessions`);
    }
  }

  // applyTerminals refreshes the affected project's chips, or every project when
  // the event names none (the connect snapshot).
  applyTerminals(detail) {
    this.refreshProjects(detail && detail.project ? [detail.project] : null);
  }

  // refreshProjects pulls a fresh /projects render and swaps the chip lists of
  // the named projects in place (all of them when names is null). The fetch is
  // this client's own, so the swapped-in forms already carry the right CSRF
  // token, and each chip list keeps its unfold: the flag sits on the container
  // that stays in the DOM.
  refreshProjects(names) {
    // Coalesce an event that lands during an in-flight fetch: re-run once it
    // settles (refreshing every project, a safe superset of any dropped scope).
    if (this.inFlight) {
      this.dirty = true;
      return;
    }
    this.dirty = false;
    this.inFlight = true;
    const card = this.querySelector(".projects-card");
    const bar = card && el("div", { class: "dc-loading-bar", role: "status", "aria-label": "Refreshing" });
    if (bar) card.prepend(bar);
    const wanted = names && new Set(names.map((n) => `project-${n}`));
    fetch("/projects", { credentials: "same-origin" })
      .then((response) => (response.ok ? response.text() : Promise.reject(new Error("refresh failed"))))
      .then((html) => {
        const doc = new DOMParser().parseFromString(html, "text/html");
        this.querySelectorAll("[data-sessions-body]").forEach((body) => {
          const section = body.closest("[id^='project-']");
          if (!section || (wanted && !wanted.has(section.id))) return;
          const fresh = doc.getElementById(section.id);
          const freshBody = fresh && fresh.querySelector("[data-sessions-body]");
          if (freshBody) this.swapChips(body, freshBody.innerHTML);
        });
      })
      .catch(() => {})
      .finally(() => {
        if (bar) bar.remove();
        this.inFlight = false;
        if (this.dirty) this.refreshProjects(null);
      });
  }

  swapChips(body, html) {
    const template = document.createElement("template");
    template.innerHTML = html;
    body.replaceChildren(...template.content.childNodes);
    window.app.loadElements(body);
    this.foldChips(body);
  }

  // Re-renders just the project row from the redirected /projects response.
  ajaxRefresh(form) {
    const section = form.closest('[id^="project-"]');
    fetch(form.action, { method: "POST", body: new URLSearchParams(new FormData(form)) })
      .then((response) => {
        if (!response.ok) throw new Error("submit failed");
        return response.text();
      })
      .then((html) => {
        const fresh = section ? new DOMParser().parseFromString(html, "text/html").getElementById(section.id) : null;
        if (!fresh || !section) throw new Error("section not found");
        section.replaceWith(fresh);
        window.app.loadElements(fresh);
        const body = fresh.querySelector("[data-sessions-body]");
        if (body) this.foldChips(body);
        document.dispatchEvent(new CustomEvent("dc:rendered", { detail: { root: fresh } }));
      })
      .catch(() => window.pe.submit(form));
  }

  setupSort() {
    const options = Array.from(this.querySelectorAll("[data-project-sort-option]"));
    const list = this.querySelector(".projects-card .list-group");
    if (options.length === 0 || !list) {
      return;
    }
    const current = this.querySelector("[data-project-sort-current]");

    const apply = (mode) => {
      projectSort.sort(list, mode);
      options.forEach((opt) => opt.classList.toggle("active", opt.dataset.projectSortOption === mode));
      const active = options.find((opt) => opt.dataset.projectSortOption === mode);
      if (current && active) current.textContent = active.textContent.trim();
    };

    apply(projectSort.mode());
    options.forEach((opt) => {
      opt.addEventListener("click", () => {
        const mode = opt.dataset.projectSortOption;
        store.set(projectSort.KEY, mode);
        apply(mode);
      }, { signal: this.ac.signal });
    });
  }

  setupFilter() {
    const input = this.querySelector("[data-project-filter]");
    if (!input) {
      return;
    }
    const empty = this.querySelector("[data-project-filter-empty]");
    const meta = this.querySelector("[data-project-filter-meta]");
    const count = this.querySelector("[data-project-filter-count]");
    const xBtn = this.querySelector(".input-icon [data-project-filter-clear]");

    const apply = (query) => {
      const needle = query.trim().toLowerCase();
      const cards = this.querySelectorAll(".projects-card [data-project-name]");
      let visible = 0;
      cards.forEach((card) => {
        const hit = needle === "" || card.dataset.projectName.toLowerCase().includes(needle);
        card.classList.toggle("d-none", !hit);
        if (hit) visible += 1;
      });
      const active = needle !== "";
      input.classList.toggle("border-primary", active);
      if (empty) empty.classList.toggle("d-none", visible !== 0);
      if (xBtn) xBtn.classList.toggle("d-none", !active);
      if (meta) meta.classList.toggle("d-none", !active);
      if (count) count.textContent = active ? `${visible} of ${cards.length}` : "";
    };

    const set = (value) => {
      input.value = value;
      store.set(FILTER_KEY, value);
      apply(value);
    };

    input.value = store.get(FILTER_KEY, "");
    apply(input.value);

    const revealHashTarget = () => {
      if (!location.hash.startsWith("#project-")) return;
      const target = document.getElementById(location.hash.slice(1));
      const needle = input.value.trim().toLowerCase();
      if (target && needle && !(target.dataset.projectName || "").toLowerCase().includes(needle)) {
        set("");
        target.scrollIntoView();
      }
    };
    revealHashTarget();
    window.addEventListener("dc:navigated", revealHashTarget, { signal: this.ac.signal });

    const signal = this.ac.signal;
    input.addEventListener("input", () => set(input.value), { signal });
    input.addEventListener("keydown", (event) => {
      if (event.key === "Escape") set("");
    }, { signal });
    this.querySelectorAll("[data-project-filter-clear]").forEach((node) => {
      node.addEventListener("click", (event) => {
        event.preventDefault();
        set("");
        input.focus();
      }, { signal });
    });
  }
}

customElements.define("dc-project-list", ProjectList);
