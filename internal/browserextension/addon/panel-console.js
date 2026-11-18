import { el } from "./api.js";
import { clock, logText, logTitle } from "./browser-context.js";

// Every value is the page's own text and shows as text.

const KINDS = { error: "error", warn: "warning", request: "request" };

export class ConsoleList {
  constructor(panel) {
    this.panel = panel;
    this.list = document.querySelector("[data-console-list]");
    this.summary = document.querySelector("[data-console-summary]");
    this.empty = document.querySelector("[data-console-empty]");
    this.all = document.querySelector("[data-console-all]");
    this.log = null;
    this.every = true;
    this.web = true;
    this.open = new Map();
    this.queued = false;
    this.list.addEventListener("click", (event) => this.onClick(event));
    this.all.addEventListener("click", () => void this.add({ all: true }));
  }

  opened() {
    if (!this.open.has(this.panel.tabId)) this.open.set(this.panel.tabId, new Set());
    return this.open.get(this.panel.tabId);
  }

  forget(tabId) {
    this.open.delete(tabId);
  }

  set(log, every, web) {
    this.log = log && Array.isArray(log.entries) ? log : null;
    this.every = every;
    this.web = web;
    if (this.queued) return;
    this.queued = true;
    requestAnimationFrame(() => {
      this.queued = false;
      this.render();
    });
  }

  render() {
    const entries = this.log ? this.log.entries : [];
    const follow = this.list.scrollTop + this.list.clientHeight >= this.list.scrollHeight - 8;
    const rows = [];
    if (this.log?.left) rows.push(el("div", { class: "log-left muted small" }, `${this.log.left} more ${this.log.left === 1 ? "entry was" : "entries were"} not listed.`));
    for (const entry of entries) rows.push(this.row(entry));
    this.list.replaceChildren(...rows);
    if (follow) this.list.scrollTop = this.list.scrollHeight;
    let errors = 0;
    let warnings = 0;
    for (const entry of entries) {
      if (entry.mark) continue;
      if (entry.level === "warn") warnings += 1;
      else errors += 1;
    }
    this.summary.textContent = [errors ? `${errors} ${errors === 1 ? "error" : "errors"}` : "", warnings ? `${warnings} ${warnings === 1 ? "warning" : "warnings"}` : ""].filter(Boolean).join(", ");
    this.all.disabled = errors + warnings === 0;
    this.empty.textContent = this.sentence(errors + warnings);
    this.empty.hidden = !this.empty.textContent;
    this.list.hidden = !rows.length;
  }

  // Only a catch that ran from the page's first script on may say the page
  // logged nothing. One that came later, or went with an update of the
  // extension, catches again after a reload.
  sentence(count) {
    if (!this.every) return "The console needs access to every page, Setup gives it.";
    if (this.log?.stale) return "The extension was updated, reload the page to catch its console again.";
    if (!this.log) return this.web ? "Reload the page to catch its console." : "The console is caught on http and https pages.";
    return count ? "" : "No console error, warning or failed request on this page.";
  }

  row(entry) {
    if (entry.mark) {
      return el("div", { class: "log mark", dataset: { logMark: "" } }, el("span", { class: "log-time" }, clock(entry.time)), el("span", { class: "log-text" }, `Navigated to ${entry.url}`));
    }
    const kind = entry.kind === "request" ? "request" : entry.level;
    const open = this.opened().has(entry.id);
    return el("div", { class: `log ${kind}${open ? " open" : ""}`, dataset: { logId: String(entry.id) } },
      el("button", { type: "button", class: "log-main", "aria-expanded": entry.stack ? (open ? "true" : "false") : null, title: logTitle(entry), dataset: { logToggle: "" } },
        el("span", { class: "log-kind" }, KINDS[kind]),
        el("span", { class: "log-time" }, clock(entry.time)),
        el("span", { class: "log-text" }, logText(entry))),
      el("button", { type: "button", class: "log-add", title: "Add to the tray", "aria-label": `Add this ${KINDS[kind]} to the tray`, dataset: { logAdd: "" } }, "+"),
      entry.stack ? el("pre", { class: "log-stack", hidden: !open }, entry.stack) : null);
  }

  onClick(event) {
    const row = event.target instanceof Element ? event.target.closest("[data-log-id]") : null;
    if (!row) return;
    const id = Number(row.dataset.logId);
    if (event.target.closest("[data-log-add]")) {
      void this.add({ id });
      return;
    }
    const stack = row.querySelector(".log-stack");
    if (!stack) return;
    const opened = this.opened();
    const open = !opened.has(id);
    if (open) opened.add(id);
    else opened.delete(id);
    row.classList.toggle("open", open);
    row.querySelector("[data-log-toggle]").setAttribute("aria-expanded", open ? "true" : "false");
    stack.hidden = !open;
  }

  async add(what) {
    const tab = this.panel.tabId;
    const answer = await this.panel.ask("add-log", { ...what, tabId: tab });
    if (answer?.error) this.panel.say(answer.error, { error: true, tab });
  }

  reveal(logKey) {
    const id = /^log-(\d+)$/.exec(logKey || "")?.[1];
    const row = id ? this.list.querySelector(`[data-log-id="${id}"]`) : null;
    if (!row) return;
    row.scrollIntoView({ block: "nearest" });
    row.classList.add("flash");
    setTimeout(() => row.classList.remove("flash"), 900);
  }
}
