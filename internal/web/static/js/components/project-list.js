import * as store from "@dc/store";
import * as projectSort from "@dc/project-sort";
import { confirm } from "@dc/dialog";
import { el } from "@dc/dom";
import { onServerEvent } from "@dc/events";

const FILTER_KEY = "dc-project-filter";

// Owns the projects page interactivity: a frontend-only name filter, the sort
// switcher, and the in place Stop/Delete on the coder and shell rows. Filter and
// sort persist to localStorage so they survive reloads, tabs and restarts.
// Filtering only hides cards; sorting reorders them; the two are orthogonal. The
// sort order is shared with the quick nav through @dc/project-sort (same key and
// comparator).
class ProjectList extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    this.inFlight = false;
    this.dirty = false;
    this.setupSort();
    this.setupFilter();
    this.addEventListener("submit", (event) => this.onAjaxSubmit(event), { signal: this.ac.signal });
    // A coder or shell started, stopped, was renamed or reordered somewhere: pull
    // a fresh /projects render and swap the affected sections in place, so the
    // start page tracks the tab strip live without disturbing unfolded lists, the
    // filter or the scroll. A project-less event (reorder, connect snapshot)
    // touches every project.
    onServerEvent("terminals", (event) => this.applyTerminals(event.detail), { signal: this.ac.signal });
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  // Stop (data-ajax-refresh) and Delete (data-ajax-delete) act in place.
  async onAjaxSubmit(event) {
    const form = event.target;
    if (!(form instanceof HTMLFormElement)) return;
    const isDelete = form.dataset.ajaxDelete !== undefined;
    const isRefresh = form.dataset.ajaxRefresh !== undefined;
    if (!isDelete && !isRefresh) return;
    event.preventDefault();
    event.stopPropagation();
    if (form.dataset.confirm) {
      const ok = await confirm({ title: form.dataset.confirm, confirmText: form.dataset.confirmButton || "Confirm" });
      if (!ok) return;
    }
    if (isDelete) this.ajaxDelete(form);
    else this.ajaxRefresh(form);
  }

  // Removes the row; drops the inner list once its last real entry is gone.
  ajaxDelete(form) {
    const row = form.closest(".list-group-item");
    const list = row ? row.parentElement : null;
    const section = form.closest('[id^="project-"]');
    fetch(form.action, { method: "POST", body: new URLSearchParams(new FormData(form)) })
      .then((response) => {
        if (!response.ok) throw new Error("delete failed");
        if (row) row.remove();
        if (list && list.querySelectorAll(".list-group-item:not([data-collapse-toggle])").length === 0) list.remove();
        if (section) document.dispatchEvent(new CustomEvent("dc:rendered", { detail: { root: section } }));
      })
      .catch(() => window.pe.submit(form));
  }

  // applyTerminals refreshes the affected project's sections, or every project when
  // the event names none (the connect snapshot).
  applyTerminals(detail) {
    this.refreshProjects(detail && detail.project ? [detail.project] : null);
  }

  // refreshProjects pulls a fresh /projects render and swaps the coder and shell
  // section bodies of the named projects in place (all of them when names is
  // null). The fetch is this client's own, so the swapped-in forms already carry
  // the right CSRF token, and each dc-collapse-list keeps its unfold: the old
  // expanded flag rides onto the fresh element before it connects.
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
    const wanted = names && new Set(names.flatMap((n) => [`project-${n}-coders`, `project-${n}-shells`]));
    fetch("/projects", { credentials: "same-origin" })
      .then((response) => (response.ok ? response.text() : Promise.reject(new Error("refresh failed"))))
      .then((html) => {
        const doc = new DOMParser().parseFromString(html, "text/html");
        this.querySelectorAll("[data-coders-body], [data-shells-body]").forEach((body) => {
          const section = body.closest("[id^='project-']");
          if (!section || (wanted && !wanted.has(section.id))) return;
          const fresh = doc.getElementById(section.id);
          const freshBody = fresh && fresh.querySelector(body.matches("[data-coders-body]") ? "[data-coders-body]" : "[data-shells-body]");
          if (freshBody) this.swapBody(body, freshBody.innerHTML);
        });
      })
      .catch(() => {})
      .finally(() => {
        if (bar) bar.remove();
        this.inFlight = false;
        if (this.dirty) this.refreshProjects(null);
      });
  }

  // swapBody replaces one section body with the fresh render, carrying the current
  // unfold state onto the new dc-collapse-list so a "Show N more" the user opened
  // stays open.
  swapBody(body, html) {
    const oldList = body.querySelector("dc-collapse-list");
    const expanded = oldList && oldList.dataset.collapseExpanded === "1";
    const template = document.createElement("template");
    template.innerHTML = html;
    const freshList = template.content.querySelector("dc-collapse-list");
    if (freshList && expanded) freshList.setAttribute("data-collapse-expanded", "1");
    body.replaceChildren(...template.content.childNodes);
    window.app.loadElements(body);
  }

  // Re-renders just the project section from the redirected /projects response.
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
