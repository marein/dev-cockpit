import { onServerEvent } from "@dc/events";
import { matchesTokens } from "@dc/filter";
import * as projectSort from "@dc/project-sort";
import { get, set } from "@dc/store";

const SORT_LABELS = { alpha: "Name", active: "Active first", recent: "Recently used" };
const FILTER_KEYS = { projects: "dc-project-filter", terminals: "dc-terminal-filter" };

class CtxSheet extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.ac = new AbortController();
    const { signal } = this.ac;
    this.panel = this.querySelector("[data-ctx-sheet-panel]");
    this.area = "";
    this.addEventListener("click", (event) => {
      if (event.target === this) {
        this.close();
        return;
      }
      if (event.defaultPrevented) return;
      const target = event.target instanceof Element ? event.target : null;
      if (!target) return;
      if (target.closest("[data-ctx-sheet-retry]")) {
        event.preventDefault();
        void this.load(this.area, true);
        return;
      }
      if (target.closest("[data-dc-focus='work']") || target.closest("a[href]")) {
        window.setTimeout(() => this.close(), 0);
      }
    }, { signal });
    document.addEventListener("keydown", (event) => {
      if (event.key === "Escape" && !this.hidden) this.close();
    }, { signal });
    window.addEventListener("dc:navigated", () => this.close(), { signal });
    document.addEventListener("show.bs.modal", () => this.close(), { signal });
    document.addEventListener("show.bs.offcanvas", () => this.close(), { signal });
    onServerEvent("projects", () => this.refreshIf("projects"), { signal });
    onServerEvent("terminals", () => this.refreshIf("projects"), { signal });
    onServerEvent("docker", () => this.refreshIf("projects"), { signal });
  }

  disconnectedCallback() {
    this.ac?.abort();
    this.ac = null;
  }

  refreshIf(area) {
    if (!this.hidden && this.area === area) void this.load(area, false);
  }

  async open(area) {
    if (!area) return;
    this.area = area;
    this.hidden = false;
    document.body.classList.add("dc-sheet-open");
    await this.load(area, true);
  }

  close() {
    if (this.hidden) return;
    this.hidden = true;
    this.area = "";
    this.applyFilter = null;
    this.panel.replaceChildren();
    document.body.classList.remove("dc-sheet-open");
  }

  async load(area, fresh) {
    const path = window.location.pathname + window.location.search;
    const token = (this.loadToken = (this.loadToken || 0) + 1);
    const live = () => this.loadToken === token && this.area === area && !this.hidden;
    const spinner = fresh ? window.setTimeout(() => { if (live()) this.panel.replaceChildren(this.placeholder(area, "loading")); }, 150) : 0;
    let doc = null;
    try {
      const response = await fetch(`/ctx/${encodeURIComponent(area)}?path=${encodeURIComponent(path)}`, { credentials: "same-origin", headers: { Accept: "text/html" } });
      if (!response.ok) throw new Error(`The list answered ${response.status}`);
      doc = new DOMParser().parseFromString(await response.text(), "text/html");
    } catch (error) {
      void error;
      window.clearTimeout(spinner);
      if (live() && (fresh || !this.panel.querySelector(".dc-ctx:not([data-ctx-sheet-placeholder])"))) {
        this.panel.replaceChildren(this.placeholder(area, "error"));
      }
      return;
    }
    window.clearTimeout(spinner);
    const column = doc.querySelector(".dc-ctx");
    if (!column || !live()) return;
    const back = column.querySelector(".dc-work-toggle");
    if (back) {
      back.classList.add("dc-sheet-close");
      back.setAttribute("aria-label", "Close");
      back.setAttribute("title", "Close");
      const icon = back.querySelector(".ti");
      if (icon) icon.className = "ti ti-x";
    }
    const current = this.panel.querySelector(".dc-ctx:not([data-ctx-sheet-placeholder])");
    if (fresh || !current) {
      this.panel.replaceChildren(document.adoptNode(column));
      window.app?.loadElements?.(this.panel);
    } else {
      const body = current.querySelector(".dc-ctx-body");
      const freshBody = column.querySelector(".dc-ctx-body");
      if (body && freshBody && body.innerHTML !== freshBody.innerHTML) {
        const top = body.scrollTop;
        body.replaceChildren(...freshBody.childNodes);
        body.scrollTop = top;
        window.app?.loadElements?.(body);
      }
    }
    this.decorate();
    if (fresh || !current) this.panel.querySelector("terminal-tabs")?.revealActive?.(true);
    document.dispatchEvent(new CustomEvent("dc:rendered", { detail: { root: this.panel } }));
  }

  placeholder(area, kind) {
    const titles = { projects: "Projects", terminals: "Terminals", settings: "Settings", docs: "Docs", assistant: "Assistant" };
    const column = document.createElement("div");
    column.className = "dc-ctx";
    column.dataset.ctxSheetPlaceholder = kind;
    const body = kind === "loading"
      ? '<div class="spinner-border text-secondary" role="status" aria-label="Loading the list"></div>'
      : '<div class="text-secondary" data-ctx-sheet-error>The list could not be loaded.</div><button type="button" class="btn" data-ctx-sheet-retry><i class="ti ti-refresh me-1" aria-hidden="true"></i>Try again</button>';
    column.innerHTML = '<div class="dc-ctx-head"><button type="button" class="btn btn-icon btn-ghost-secondary dc-work-toggle dc-sheet-close" data-dc-focus="work" aria-label="Close" title="Close"><i class="ti ti-x" aria-hidden="true"></i></button><h1 class="dc-ctx-title">'
      + (titles[area] || "List") + '</h1></div><div class="dc-ctx-body d-flex flex-column align-items-center justify-content-center gap-3 p-4">' + body + "</div>";
    return column;
  }

  decorate() {
    const column = this.panel.querySelector(".dc-ctx:not([data-ctx-sheet-placeholder])");
    if (!column) return;
    const index = column.querySelector("[data-project-index]");
    if (index) {
      projectSort.sort(index, projectSort.mode(), projectSort.INDEX);
      const options = Array.from(column.querySelectorAll("[data-ctx-sort-option]"));
      const mark = (mode) => options.forEach((opt) => opt.classList.toggle("active", opt.dataset.ctxSortOption === mode));
      mark(projectSort.mode());
      if (!column.dataset.ctxSortWired) {
        column.dataset.ctxSortWired = "1";
        for (const opt of options) {
          opt.addEventListener("click", () => {
            const mode = opt.dataset.ctxSortOption;
            set(projectSort.KEY, mode);
            const list = this.panel.querySelector("[data-project-index]");
            if (list) projectSort.sort(list, mode, projectSort.INDEX);
            mark(mode);
            const toggle = column.querySelector("[data-ctx-sort-toggle]");
            if (toggle) toggle.setAttribute("title", `Sort projects: ${SORT_LABELS[mode] || mode}`);
          });
        }
      }
    }
    const input = column.querySelector("[data-ctx-filter]");
    if (!input) this.applyFilter = null;
    if (input && !column.dataset.ctxFilterWired) {
      column.dataset.ctxFilterWired = "1";
      const shared = Boolean(index);
      const key = FILTER_KEYS[this.area] || "";
      if (key) input.value = get(key, "");
      const clear = column.querySelector("[data-ctx-filter-clear]");
      const apply = () => {
        const query = input.value.trim();
        if (key) set(key, input.value);
        if (clear) clear.classList.toggle("d-none", query === "");
        const rows = this.panel.querySelectorAll("[data-project-index] .list-group-item, .terminal-tabs-strip .terminal-tab, .terminal-tabs-strip .terminal-tab-member");
        for (const row of rows) {
          const haystack = shared
            ? `${row.dataset.indexProject || ""} ${row.dataset.indexWorktreeOf || ""}`
            : (row.textContent || "");
          const keep = query === "" || matchesTokens(haystack, query);
          row.classList.toggle("d-none", !keep);
        }
      };
      input.addEventListener("input", apply);
      clear?.addEventListener("click", () => { input.value = ""; apply(); input.focus(); });
      input.addEventListener("keydown", (event) => { if (event.key === "Escape" && input.value) { event.stopPropagation(); input.value = ""; apply(); } });
      const body = column.querySelector(".dc-ctx-body");
      if (body) new MutationObserver(() => { if (input.value) apply(); }).observe(body, { childList: true, subtree: true });
      this.applyFilter = apply;
    }
    if (this.applyFilter) this.applyFilter();
  }
}

customElements.define("dc-ctx-sheet", CtxSheet);
