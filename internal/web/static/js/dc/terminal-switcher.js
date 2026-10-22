import { el, syncAnimations } from "@dc/dom";
import { rankRows, rankTokens } from "@dc/filter";
import { ensureOk, landingURL, postForm } from "@dc/http";
import { RESUME_FOLD_LIMIT } from "@dc/terminal-new-menu";
import { notifyError } from "@dc/toast";

const switcherKey = ({ dataset: d }) => [d.switcherToggle, d.switcherResume, d.switcherId || d.switcherUrl].join("\n");

// Switcher is the switcher of a terminal-tabs element, its rows built from
// the element's strip and + menu. The element closes it through its
// closeSwitcher.
export class Switcher {
  // open puts one up, or answers null when there is nothing to list.
  static open(host, direction, signal) {
    const switcher = new Switcher(host, signal);
    return switcher.open(direction) ? switcher : null;
  }

  constructor(host, signal) {
    this.host = host;
    this.signal = signal;
    this.resuming = false;
  }

  switcherRow(tab) {
    const current = tab.classList.contains("active");
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
      tab.querySelector("[data-tab-icon]")?.cloneNode(true),
      el("span", { class: "terminal-switcher-name text-truncate" }, tab.dataset.tabName || ""),
      tab.dataset.tabProject
        ? el("span", { class: "terminal-switcher-project text-truncate" }, tab.dataset.tabProject)
        : null,
    );
    row.addEventListener("click", () => this.host.navigate(row.dataset.switcherUrl), { signal: this.signal });
    return row;
  }

  editorRow(link) {
    const row = el(
      "div",
      {
        role: "option",
        class: "terminal-switcher-item",
        dataset: {
          switcherUrl: link.getAttribute("href") || "",
          switcherName: `${link.dataset.editorProject || ""} ${link.dataset.editorTags || ""} editor`,
          switcherSection: "editors",
        },
      },
      Array.from(link.children, (node) => node.cloneNode(true)),
    );
    row.addEventListener("click", () => this.host.navigate(row.dataset.switcherUrl), { signal: this.signal });
    return row;
  }

  // The assistants are destinations like a terminal, not "new" actions: they
  // sit right under the open tabs so the switcher reaches them in the same two
  // keys.
  assistantRow(link) {
    const icon = link.querySelector(".dc-term-icon");
    const row = el(
      "div",
      {
        role: "option",
        class: "terminal-switcher-item",
        dataset: {
          switcherUrl: link.getAttribute("href") || "/assistants",
          switcherName: "Assistants",
          switcherSection: "assistant",
        },
      },
      icon ? icon.cloneNode(true) : el("span", { class: "terminal-tab-icon dc-term-icon assistant", "aria-hidden": "true" }, el("i", { class: "ti ti-sparkles" })),
      el("span", { class: "terminal-switcher-name text-truncate" }, "Assistants"),
    );
    row.addEventListener("click", () => this.host.navigate(row.dataset.switcherUrl), { signal: this.signal });
    return row;
  }

  assistantInstanceRow(link) {
    const name = link.dataset.assistantName || "";
    const row = el(
      "div",
      {
        role: "option",
        class: "terminal-switcher-item",
        dataset: {
          switcherUrl: link.getAttribute("href") || "",
          switcherName: name + " assistant",
          switcherSection: "assistant",
        },
      },
      link.querySelector(".dc-term-icon")?.cloneNode(true),
      el("span", { class: "terminal-switcher-name text-truncate" }, name),
      el("span", { class: "terminal-switcher-project text-truncate" }, "Assistant"),
    );
    row.addEventListener("click", () => this.host.navigate(row.dataset.switcherUrl), { signal: this.signal });
    return row;
  }

  actionRow(link) {
    const kind = link.dataset.tabsNew;
    const label = kind === "coder" ? "New coder" : "New shell";
    const url = link.getAttribute("href") || "";
    const project = new URL(url, window.location.href).searchParams.get("project") || "";
    const row = el(
      "div",
      {
        role: "option",
        class: "terminal-switcher-item",
        dataset: {
          switcherUrl: url,
          switcherName: label + " " + project,
          switcherSection: "new",
        },
      },
      el("span", { class: "terminal-tab-icon dc-term-icon", "aria-hidden": "true" }, el("i", { class: kind === "coder" ? "ti ti-robot" : "ti ti-terminal-2" })),
      el("span", { class: "terminal-switcher-name text-truncate" }, label),
      project ? el("span", { class: "terminal-switcher-project text-truncate" }, project) : null,
    );
    row.addEventListener("click", () => this.host.navigate(row.dataset.switcherUrl), { signal: this.signal });
    return row;
  }

  resumeRow(button, project, folded) {
    const dataset = {
      switcherResume: button.dataset.resumeId || "",
      switcherName: (button.dataset.resumeName || "") + " " + (project || ""),
      switcherGroup: project,
      switcherSection: "inactive",
    };
    if (folded) dataset.switcherFolded = project;
    const row = el(
      "div",
      { role: "option", class: "terminal-switcher-item terminal-switcher-item-inactive", dataset },
      button.querySelector("[data-resume-icon]")?.cloneNode(true),
      el("span", { class: "terminal-switcher-name text-truncate" }, button.dataset.resumeName || ""),
    );
    row.addEventListener("click", () => void this.resumeTarget(row), { signal: this.signal });
    return row;
  }

  resumeToggleRow(project, count) {
    const row = el(
      "div",
      {
        role: "option",
        class: "terminal-switcher-item terminal-switcher-item-inactive terminal-switcher-item-toggle",
        dataset: { switcherToggle: project, switcherGroup: project, switcherSection: "inactive" },
      },
      el("span", { class: "terminal-switcher-name text-truncate" }, "Show " + count + " more"),
    );
    row.addEventListener("pointerdown", (event) => event.preventDefault(), { signal: this.signal });
    row.addEventListener("click", () => this.expandGroup(project), { signal: this.signal });
    return row;
  }

  expandGroup(project) {
    const list = this.overlay.querySelector(".terminal-switcher-list");
    const scrollTop = list.scrollTop;
    this.expanded.add(project);
    this.applyFilter();
    list.scrollTop = scrollTop;
    const first = this.visible.find((row) => row.dataset.switcherFolded === project);
    if (first) {
      this.index = this.visible.indexOf(first);
      this.paint();
    }
  }

  async resumeTarget(row) {
    if (this.resuming) return;
    this.resuming = true;
    try {
      const id = row.dataset.switcherResume;
      const response = await postForm(`/coders/${id}/resume`, {});
      await ensureOk(response, "Could not resume the coder.");
      this.host.navigate((await landingURL(response)) || `/coders/${id}`);
    } catch (error) {
      notifyError(error.message);
      this.input.focus();
    } finally {
      this.resuming = false;
    }
  }

  buildLists() {
    const host = this.host;
    const rows = host.tabs().map((tab) => this.switcherRow(tab));
    const resumeRows = [];
    const inactiveNodes = [];
    const groupLabels = [];
    for (const group of host.querySelectorAll("[data-tabs-resume-fold]")) {
      const project = group.getAttribute("data-tabs-resume-fold");
      const buttons = Array.from(group.querySelectorAll("[data-resume-id]"));
      if (!buttons.length) continue;
      const label = el(
        "div",
        { class: "terminal-switcher-group" },
        el("i", { class: "ti ti-folder", "aria-hidden": "true" }),
        el("span", { class: "text-truncate" }, project),
      );
      groupLabels.push({ key: project, node: label });
      inactiveNodes.push(label);
      buttons.forEach((button, index) => {
        const row = this.resumeRow(button, project, index >= RESUME_FOLD_LIMIT);
        resumeRows.push(row);
        inactiveNodes.push(row);
      });
      if (buttons.length > RESUME_FOLD_LIMIT) {
        const toggle = this.resumeToggleRow(project, buttons.length - RESUME_FOLD_LIMIT);
        resumeRows.push(toggle);
        inactiveNodes.push(toggle);
      }
    }
    const editorRows = Array.from(host.querySelectorAll("[data-tabs-editors] [data-editor-project]"))
      .map((link) => this.editorRow(link));
    const actionRows = Array.from(host.querySelectorAll(".terminal-tabs-new-menu [data-tabs-new]"))
      .map((link) => this.actionRow(link));
    const assistantRows = Array.from(host.querySelectorAll("[data-tabs-assistant]"))
      .map((link) => this.assistantRow(link))
      .concat(Array.from(host.querySelectorAll("[data-tabs-assistant-instance]"))
        .map((link) => this.assistantInstanceRow(link)));
    const sections = [];
    const listNodes = [...rows];
    const addSection = (key, title, nodes) => {
      if (!nodes.length) return;
      const node = el("div", { class: "terminal-switcher-section", dataset: { switcherSection: key } }, title);
      sections.push({ key, node });
      listNodes.push(node, ...nodes);
    };
    addSection("assistant", "Assistants", assistantRows);
    addSection("inactive", "Inactive coders", inactiveNodes);
    addSection("editors", "Editors", editorRows);
    addSection("new", "New", actionRows);
    const cycleRows = rows.concat(assistantRows, resumeRows, editorRows, actionRows);
    return { rows, cycleRows, listNodes, sections, groupLabels };
  }

  open(direction) {
    const { rows, cycleRows, listNodes, sections, groupLabels } = this.buildLists();
    if (!cycleRows.length) return false;
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
        el("div", { class: "terminal-switcher-list", role: "listbox" }, listNodes, empty),
        el("div", { class: "terminal-switcher-hint" }, "Type to filter, arrows to cycle, Enter to switch, Esc to close"),
      ),
    );
    input.addEventListener("input", () => this.applyFilter(true), { signal: this.signal });
    overlay.addEventListener("pointerdown", (event) => {
      if (event.target === overlay) this.host.closeSwitcher();
    }, { signal: this.signal });
    document.body.appendChild(overlay);
    const current = rows.findIndex((row) => row.classList.contains("current"));
    Object.assign(this, {
      overlay,
      rows: cycleRows,
      sections,
      groupLabels,
      expanded: new Set(),
      input,
      empty,
      visible: cycleRows.slice(),
      index: 0,
      prevFocus: document.activeElement,
    });
    input.focus();
    this.applyFilter();
    this.index = current === -1 ? 0 : (current + direction + rows.length) % rows.length;
    this.paint();
    return true;
  }

  // The place is held by a row, not by pixels: the marked row while in view,
  // else the top row, keeps its offset. Revealing the mark undid a wheel scroll.
  rebuild() {
    const list = this.overlay.querySelector(".terminal-switcher-list");
    const top = list.getBoundingClientRect().top;
    const rect = (row) => row.getBoundingClientRect();
    const inView = (row) => row && rect(row).bottom > top && rect(row).top < top + list.clientHeight;
    const anchorAt = inView(this.visible[this.index]) ? this.index : this.visible.findIndex((row) => rect(row).bottom > top);
    const offset = anchorAt === -1 ? 0 : rect(this.visible[anchorAt]).top - top;
    const keys = this.visible.map(switcherKey);
    const { cycleRows, listNodes, sections, groupLabels } = this.buildLists();
    list.replaceChildren(...listNodes, this.empty);
    Object.assign(this, { rows: cycleRows, sections, groupLabels });
    this.filter();
    const fresh = new Map(this.visible.map((row, index) => [switcherKey(row), index]));
    const nearest = (at) => keys.flatMap((_, d) => [keys[at + d], keys[at - d]])
      .map((key) => fresh.get(key)).find((hit) => hit !== undefined) ?? -1;
    this.index = Math.max(nearest(this.index), 0);
    this.paint(false);
    const anchor = this.visible[nearest(anchorAt)];
    if (anchor) list.scrollTop += rect(anchor).top - top - offset;
  }

  close() {
    this.overlay.remove();
    if (this.prevFocus?.isConnected) this.prevFocus.focus();
  }

  applyFilter(fromInput) {
    const selected = this.visible[this.index];
    this.filter();
    this.index = Math.max(fromInput ? -1 : this.visible.indexOf(selected), 0);
    this.paint();
  }

  // Each group (the open tabs, a section, a project's inactive coders) sorts
  // its rows by rank on its own, so the sections keep their place.
  filter() {
    const query = this.input.value.trim();
    this.visible = rankRows(this.rows, (row) => {
      if (row.dataset.switcherToggle) return !query && !this.expanded.has(row.dataset.switcherToggle) ? 0 : -1;
      if (query) return rankTokens(row.querySelector(".terminal-switcher-name")?.textContent || "", row.dataset.switcherName, query);
      return row.dataset.switcherFolded && !this.expanded.has(row.dataset.switcherFolded) ? -1 : 0;
    }, { groupOf: (row) => (row.dataset.switcherSection || "") + "\n" + (row.dataset.switcherGroup || "") });
    this.empty.hidden = this.visible.length > 0;
    for (const { node, key } of this.sections) node.hidden = !this.visible.some((row) => row.dataset.switcherSection === key);
    for (const { node, key } of this.groupLabels) node.hidden = !this.visible.some((row) => row.dataset.switcherGroup === key);
    syncAnimations(this.overlay);
  }

  paint(reveal = true) {
    const selected = this.visible[this.index];
    for (const row of this.rows) {
      row.classList.toggle("selected", row === selected);
      row.setAttribute("aria-selected", row === selected ? "true" : "false");
    }
    if (reveal) selected?.scrollIntoView({ block: "nearest" });
  }

  move(delta) {
    if (!this.visible.length) return;
    this.index = (this.index + delta + this.visible.length) % this.visible.length;
    this.paint();
  }

  commit() {
    const selected = this.visible[this.index];
    if (!selected) return;
    if (selected.dataset.switcherToggle) this.expandGroup(selected.dataset.switcherToggle);
    else if (selected.dataset.switcherResume) void this.resumeTarget(selected);
    else this.host.navigate(selected.dataset.switcherUrl);
  }

  onKeydown(event) {
    if (event.key === "Control" || event.key === "Shift" || event.key === "Alt" || event.key === "Meta") return;
    // The switcher cycles with the arrows only. Tab stays swallowed so it neither
    // moves the selection nor pulls the focus out of the filter field.
    if (event.key === "Tab") {
      event.preventDefault();
      event.stopPropagation();
      return;
    }
    const actions = {
      ArrowDown: () => this.move(1),
      ArrowUp: () => this.move(-1),
      Enter: () => this.commit(),
      Escape: () => this.host.closeSwitcher(),
    };
    const action = actions[event.key];
    if (!action) return;
    event.preventDefault();
    event.stopPropagation();
    action();
  }
}
