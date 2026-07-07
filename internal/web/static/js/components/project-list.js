import * as store from "@dc/store";
import * as projectSort from "@dc/project-sort";
import { confirm } from "@dc/dialog";

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
    this.setupSort();
    this.setupFilter();
    this.addEventListener("submit", (event) => this.onAjaxSubmit(event), { signal: this.ac.signal });
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
