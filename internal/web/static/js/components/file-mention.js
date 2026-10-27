import { getJSON, getText } from "@dc/http";
import { onServerEvent } from "@dc/events";
import { hitRanges } from "@dc/filter";
import { paletteGroups } from "@dc/project-palette";
import * as projectSort from "@dc/project-sort";
import { fileIcon } from "@dc/editor-filetypes";
import { baseName, parentDir } from "@dc/editor-format";

const WORD_BREAK = /[\s([{"'`]/;
const QUERY_DELAY_MS = 40;
const BUSY_DELAY_MS = 150;
const MIN_ROOM_PX = 168;
const WIDE_PX = 320;
const MAX_WIDTH_PX = 640;
const GAP_PX = 8;

function plural(n, one, many) {
  return `${n.toLocaleString()} ${n === 1 ? one : many}`;
}

function markText(root, query) {
  if (!root || !query.trim()) return;
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  const nodes = [];
  for (let node = walker.nextNode(); node; node = walker.nextNode()) nodes.push(node);
  for (const node of nodes) {
    const ranges = hitRanges(node.data, query);
    if (!ranges.length) continue;
    const parts = [];
    let at = 0;
    for (const [start, end] of ranges) {
      if (start > at) parts.push(document.createTextNode(node.data.slice(at, start)));
      const mark = document.createElement("mark");
      mark.textContent = node.data.slice(start, end);
      parts.push(mark);
      at = end;
    }
    if (at < node.data.length) parts.push(document.createTextNode(node.data.slice(at)));
    node.replaceWith(...parts);
  }
}

class FileMention extends HTMLElement {
  connectedCallback() {
    if (this.ac) return;
    this.form = this.closest("form");
    this.input = this.form?.querySelector(this.getAttribute("input") || "textarea");
    this.panel = this.querySelector("[data-mention-panel]");
    this.list = this.querySelector("[data-mention-list]");
    this.source = this.querySelector("[data-mention-projects]");
    this.fileTemplate = this.querySelector("[data-mention-file-template]");
    this.noteTemplate = this.querySelector("[data-mention-note-template]");
    this.backButton = this.querySelector("[data-mention-back]");
    this.heading = this.querySelector("[data-mention-title]");
    this.crumbTrail = this.querySelector("[data-mention-trail]");
    this.crumbIcon = this.querySelector("[data-mention-crumb-icon]");
    this.count = this.querySelector("[data-mention-count]");
    this.busy = this.querySelector("[data-mention-busy]");
    if (!this.input || !this.panel || !this.list || !this.source) return;
    this.ac = new AbortController();
    const signal = this.ac.signal;
    this.anchor = -1;
    this.filesSeq = 0;
    this.shownSeq = 0;
    this.loaded = false;
    this.form.addEventListener("keydown", (event) => this.onKeydown(event), { signal, capture: true });
    this.input.addEventListener("input", (event) => this.onInput(event), { signal });
    this.input.addEventListener("focus", () => void this.loadProjects(), { signal });
    this.input.addEventListener("blur", () => {
      if (document.hasFocus()) this.close();
    }, { signal });
    document.addEventListener("selectionchange", () => {
      if (document.activeElement === this.input) this.sync();
    }, { signal });
    this.input.addEventListener("keyup", () => this.sync(), { signal });
    this.input.addEventListener("click", () => this.sync(), { signal });
    this.panel.addEventListener("mousedown", (event) => event.preventDefault(), { signal });
    this.panel.addEventListener("click", (event) => this.onClick(event), { signal });
    this.list.addEventListener("mousemove", (event) => this.onPointer(event), { signal });
    this.list.addEventListener("animationend", () => this.list.classList.remove("dc-mention-forth", "dc-mention-back"), { signal });
    onServerEvent("projects", () => void this.refreshProjects(), { signal });
    window.visualViewport?.addEventListener("resize", () => this.place(), { signal });
    window.visualViewport?.addEventListener("scroll", () => this.place(), { signal });
    window.addEventListener("resize", () => this.place(), { signal });
    if (document.activeElement === this.input) void this.loadProjects();
  }

  disconnectedCallback() {
    this.close();
    this.ac?.abort();
    this.ac = null;
  }

  onInput(event) {
    if (this.applying) return;
    if (this.anchor >= 0) {
      this.sync();
      return;
    }
    if (event.inputType !== "insertText" && event.inputType !== "insertCompositionText") return;
    const { value, selectionStart: caret, selectionEnd } = this.input;
    if (caret !== selectionEnd || value[caret - 1] !== "@") return;
    if (caret > 1 && !WORD_BREAK.test(value[caret - 2])) return;
    this.open(caret - 1);
  }

  open(anchor) {
    this.anchor = anchor;
    this.step = "";
    this.project = "";
    this.query = null;
    this.folder = "";
    this.picks = new Map();
    this.backTo = "";
    this.pendingPick = false;
    this.requests = new AbortController();
    this.panel.hidden = false;
    this.input.setAttribute("aria-controls", this.list.id);
    this.input.setAttribute("aria-autocomplete", "list");
    void this.pullProjects();
    this.sync();
  }

  close() {
    if (!this.ac || this.anchor < 0) return;
    this.anchor = -1;
    this.step = "";
    this.filesSeq += 1;
    window.clearTimeout(this.filesTimer);
    window.clearTimeout(this.busyTimer);
    this.requests?.abort();
    this.requests = null;
    this.busy.hidden = true;
    this.panel.hidden = true;
    this.list.replaceChildren();
    this.list.removeAttribute("aria-busy");
    this.input.removeAttribute("aria-activedescendant");
    this.input.removeAttribute("aria-controls");
    this.input.removeAttribute("aria-autocomplete");
  }

  token() {
    const { value, selectionStart: caret, selectionEnd } = this.input;
    if (caret !== selectionEnd || caret <= this.anchor || value[this.anchor] !== "@") return null;
    if (this.anchor > 0 && !WORD_BREAK.test(value[this.anchor - 1])) return null;
    const text = value.slice(this.anchor + 1, caret);
    return /^\s|[\r\n]/.test(text) ? null : text;
  }

  sourceRows() {
    return Array.from(this.source.querySelectorAll("[data-project-name]"));
  }

  rows() {
    return Array.from(this.list.querySelectorAll('[role="option"]'));
  }

  sync(force = false, keep = "") {
    if (this.anchor < 0) return;
    const token = this.token();
    if (token === null) {
      this.close();
      return;
    }
    const slash = token.indexOf("/");
    const name = slash > 0 ? token.slice(0, slash) : "";
    const files = name !== "" && this.sourceRows().some((row) => row.dataset.projectName === name);
    const step = files ? "files" : "projects";
    const project = files ? name : "";
    const query = files ? token.slice(slash + 1) : token;
    this.place();
    if (!force && step === this.step && project === this.project && query === this.query) return;
    const moved = step !== this.step || project !== this.project;
    const direction = moved && this.step ? (files ? "forth" : "back") : "";
    this.step = step;
    this.project = project;
    this.query = query;
    if (moved) {
      this.folder = "";
      this.paintHead(direction);
    }
    if (files) {
      this.askFiles(moved);
      return;
    }
    const marked = moved ? this.backTo : keep;
    this.backTo = "";
    this.showProjects(marked);
  }

  stepIcon() {
    const source = this.sourceRows().find((row) => row.dataset.projectName === this.project);
    return source?.querySelector(".editor-palette-line > .ti")?.className || "ti ti-folder";
  }

  location() {
    return `${this.project}/${this.folder ? `${this.folder}/` : ""}`;
  }

  paintHead(direction) {
    const files = this.step === "files";
    const folder = files ? this.folder : "";
    const parent = parentDir(folder);
    this.backButton.hidden = !files;
    if (files) {
      const back = folder ? `Back to ${parent ? `${baseName(parent)}/` : this.project}` : "Back to the projects";
      this.backButton.setAttribute("aria-label", back);
      this.backButton.title = back;
    }
    this.crumbIcon.className = `${files ? (folder ? "ti ti-folder" : this.stepIcon()) : "ti ti-at"} text-primary`;
    this.heading.textContent = !files ? "Projects" : folder ? `${baseName(folder)}/` : this.project;
    this.crumbTrail.textContent = folder ? `${this.project}/${parent ? `${parent}/` : ""}` : "";
    this.crumbTrail.hidden = !folder;
    this.count.textContent = "";
    this.list.classList.remove("dc-mention-forth", "dc-mention-back");
    if (!direction) return;
    void this.list.offsetWidth;
    this.list.classList.add(`dc-mention-${direction}`);
  }

  note(text) {
    const note = this.noteTemplate.content.firstElementChild.cloneNode(true);
    note.textContent = text;
    this.list.replaceChildren(note);
    this.mark(-1);
  }

  showProjects(marked) {
    window.clearTimeout(this.filesTimer);
    this.filesSeq += 1;
    this.settle();
    if (!this.loaded) {
      this.note(this.failed || "Loading projects…");
      return;
    }
    const query = this.query.trim();
    const groups = paletteGroups(this.sourceRows(), query);
    if (!groups.length) {
      if (/\s$/.test(this.query)) {
        this.close();
        return;
      }
      this.note(`No project matches “${query}”.`);
      this.count.textContent = plural(0, "project", "projects");
      return;
    }
    this.list.replaceChildren(...groups);
    const rows = this.rows();
    rows.forEach((row, i) => {
      row.id = `dc-mention-option-${i}`;
      row.querySelectorAll(".editor-palette-repo, .editor-palette-branch, .editor-palette-sub").forEach((part) => markText(part, query));
    });
    const at = marked ? rows.findIndex((row) => row.dataset.projectName === marked) : -1;
    this.mark(Math.max(at, 0), true);
    const shown = query ? rows.length : this.sourceRows().length;
    this.count.textContent = plural(shown, "project", "projects");
  }

  askFiles(fresh) {
    this.pendingPick = false;
    window.clearTimeout(this.filesTimer);
    const seq = ++this.filesSeq;
    if (fresh) {
      this.shownSeq = seq - 1;
      this.note("Searching…");
    }
    this.list.setAttribute("aria-busy", "true");
    window.clearTimeout(this.busyTimer);
    this.busyTimer = window.setTimeout(() => {
      if (this.anchor >= 0 && this.list.getAttribute("aria-busy") === "true") this.busy.hidden = false;
    }, BUSY_DELAY_MS);
    this.filesTimer = window.setTimeout(() => void this.fetchFiles(seq), fresh ? 0 : QUERY_DELAY_MS);
  }

  async fetchFiles(seq) {
    const project = this.project;
    const query = this.query;
    const params = new URLSearchParams({ project, q: query });
    let data;
    try {
      data = await getJSON(`${this.getAttribute("files-url")}?${params}`, { signal: this.requests?.signal });
    } catch (error) {
      if (seq !== this.filesSeq || this.anchor < 0) return;
      this.settle();
      this.note(error.message || "The files could not be listed.");
      return;
    }
    if (this.anchor < 0 || this.step !== "files" || this.project !== project || seq <= this.shownSeq) return;
    this.shownSeq = seq;
    if (seq === this.filesSeq) this.settle();
    this.showFiles(data, seq === this.filesSeq);
  }

  settle() {
    window.clearTimeout(this.busyTimer);
    this.busy.hidden = true;
    this.list.removeAttribute("aria-busy");
  }

  showFiles(data, current) {
    const folder = data.folder || "";
    if (folder !== this.folder) {
      const deeper = folder.startsWith(this.folder ? `${this.folder}/` : "");
      this.folder = folder;
      this.paintHead(deeper ? "forth" : "back");
    }
    const query = data.query || "";
    const files = data.files || [];
    const rows = [];
    if (!query.trim() && data.ref) rows.push(this.fileRow({ here: true, path: folder, ref: data.ref }, query));
    for (const file of files) rows.push(this.fileRow(file, query));
    if (!rows.length) {
      if (current && /\s$/.test(query)) {
        this.close();
        return;
      }
      this.note(`No folder or file matches “${query.trim()}”.`);
      this.count.textContent = plural(0, "match", "matches");
      return;
    }
    this.list.replaceChildren(...rows);
    rows.forEach((row, i) => {
      row.id = `dc-mention-option-${i}`;
    });
    const left = current ? this.backTo : "";
    if (current) this.backTo = "";
    this.mark(Math.max(left ? rows.findIndex((row) => row.dataset.mentionWalk === left) : 0, 0), true);
    if (!query.trim()) this.count.textContent = plural(data.under || 0, "file", "files");
    else if (data.truncated) this.count.textContent = `${files.length} of ${plural(data.total || 0, "match", "matches")}`;
    else this.count.textContent = plural(data.total || 0, "match", "matches");
    if (current && this.pendingPick) {
      this.pendingPick = false;
      this.pick(this.rows()[0]);
    }
  }

  fileRow(file, query) {
    const row = this.fileTemplate.content.firstElementChild.cloneNode(true);
    const root = file.here && !file.path;
    const name = root ? this.project : baseName(file.path);
    const parent = parentDir(file.path);
    const dir = file.here ? file.ref : parent === this.folder ? "" : parent.slice(this.folder ? this.folder.length + 1 : 0);
    row.querySelector("[data-mention-icon]").className = root ? this.stepIcon() : file.here || file.folder ? "ti ti-folder" : `ti ${fileIcon(name)}`;
    const nameEl = row.querySelector("[data-mention-name]");
    const dirEl = row.querySelector("[data-mention-dir]");
    nameEl.textContent = file.here || file.folder ? `${name}/` : name;
    dirEl.textContent = dir;
    dirEl.hidden = !dir;
    if (!file.here) {
      markText(nameEl, query);
      markText(dirEl, query);
    }
    row.querySelector("[data-mention-walk]").hidden = !file.folder;
    if (file.folder) row.dataset.mentionWalk = file.path;
    else row.dataset.mentionRef = file.ref;
    row.title = file.here ? file.ref : file.folder ? `${file.path}/` : file.path;
    return row;
  }

  mark(index, reveal = false) {
    const rows = this.rows();
    this.index = rows.length && index >= 0 ? Math.min(index, rows.length - 1) : -1;
    rows.forEach((row, i) => {
      const on = i === this.index;
      row.classList.toggle("active", on);
      row.setAttribute("aria-selected", on ? "true" : "false");
    });
    const row = rows[this.index];
    if (!row) {
      this.input.removeAttribute("aria-activedescendant");
      return;
    }
    this.input.setAttribute("aria-activedescendant", row.id);
    if (!reveal) return;
    const top = row.offsetTop - this.list.offsetTop;
    if (top < this.list.scrollTop) this.list.scrollTop = top;
    else if (top + row.offsetHeight > this.list.scrollTop + this.list.clientHeight) this.list.scrollTop = top + row.offsetHeight - this.list.clientHeight;
  }

  move(delta) {
    const rows = this.rows();
    if (!rows.length) return;
    const next = this.index < 0 ? (delta > 0 ? 0 : rows.length - 1) : (this.index + delta + rows.length) % rows.length;
    this.mark(next, true);
  }

  onKeydown(event) {
    if (this.anchor < 0 || event.target !== this.input) return;
    if (event.isComposing || event.keyCode === 229 || event.altKey || event.ctrlKey || event.metaKey) return;
    const consume = () => {
      event.preventDefault();
      event.stopPropagation();
    };
    switch (event.key) {
      case "Escape":
        consume();
        this.close();
        return;
      case "ArrowDown":
      case "ArrowUp":
        if (!this.rows().length) return;
        consume();
        this.move(event.key === "ArrowDown" ? 1 : -1);
        return;
      case "Enter":
      case "Tab":
        if (event.shiftKey) return;
        if (this.waiting()) {
          consume();
          this.pendingPick = this.step === "files";
          return;
        }
        if (!this.rows().length) {
          if (event.key === "Tab") consume();
          return;
        }
        consume();
        this.pick(this.rows()[this.index] || this.rows()[0]);
        return;
      case "Backspace":
        if (event.shiftKey || this.step !== "files" || this.input.selectionStart !== this.input.selectionEnd || this.token() !== this.location()) return;
        consume();
        this.goBack();
        return;
      default:
    }
  }

  waiting() {
    if (this.step === "files") return this.list.getAttribute("aria-busy") === "true";
    return !this.loaded;
  }

  onClick(event) {
    if (event.target.closest("[data-mention-back]")) {
      if (this.step === "files") this.goBack();
      return;
    }
    const row = event.target.closest('[role="option"]');
    if (row && this.list.contains(row)) this.pick(row);
  }

  onPointer(event) {
    const row = event.target.closest('[role="option"]');
    if (!row) return;
    const at = this.rows().indexOf(row);
    if (at >= 0 && at !== this.index) this.mark(at);
  }

  pick(row) {
    if (!row || this.anchor < 0) return;
    const caret = this.input.selectionStart;
    const name = row.dataset.projectName;
    if (name) {
      this.picks.set(`${name}/`, this.query);
      this.replace(this.anchor, caret, `@${name}/`);
      return;
    }
    const walk = row.dataset.mentionWalk;
    if (walk) {
      const here = this.folder ? `${this.folder}/` : "";
      const child = walk.slice(here.length).split("/")[0];
      this.picks.set(`${this.project}/${here}${child}/`, `${this.project}/${this.query}`);
      this.replace(this.anchor, caret, `@${this.project}/${walk}/`);
      return;
    }
    const ref = row.dataset.mentionRef;
    if (!ref) return;
    const anchor = this.anchor;
    const next = this.input.value.charAt(caret);
    this.close();
    this.replace(anchor, caret, next && /\s/.test(next) ? ref : `${ref} `);
  }

  goBack() {
    const location = this.location();
    const before = this.picks.get(location) ?? location.slice(0, -1);
    for (const key of [...this.picks.keys()]) if (key.startsWith(location)) this.picks.delete(key);
    this.backTo = this.folder || this.project;
    this.replace(this.anchor, this.input.selectionStart, `@${before}`);
  }

  replace(start, end, text) {
    this.applying = true;
    try {
      if (document.activeElement !== this.input) this.input.focus({ preventScroll: true });
      this.input.setSelectionRange(start, end);
      if (!document.execCommand("insertText", false, text)) {
        this.input.setRangeText(text, start, end, "end");
        this.input.dispatchEvent(new Event("input", { bubbles: true }));
      }
    } finally {
      this.applying = false;
    }
    this.sync();
  }

  place() {
    if (this.anchor < 0) return;
    const form = this.form.getBoundingClientRect();
    const field = this.input.getBoundingClientRect();
    const view = window.visualViewport;
    const bound = this.getAttribute("bound") ? this.closest(this.getAttribute("bound")) : null;
    const top = Math.max(view ? view.offsetTop : 0, bound ? bound.getBoundingClientRect().top : 0);
    const pad = parseFloat(window.getComputedStyle(this.form).paddingLeft) || 0;
    const wide = form.right - pad - field.left;
    this.panel.style.setProperty("--dc-mention-room", `${Math.max(MIN_ROOM_PX, Math.floor(form.top - top - GAP_PX))}px`);
    this.panel.style.setProperty("--dc-mention-left", `${wide >= WIDE_PX ? field.left - form.left : pad}px`);
    this.panel.style.setProperty("--dc-mention-width", `${wide >= WIDE_PX ? Math.min(MAX_WIDTH_PX, wide) : form.width - 2 * pad}px`);
  }

  loadProjects() {
    if (this.loaded) return Promise.resolve();
    return this.loading || this.pullProjects();
  }

  pullProjects() {
    if (this.loading) {
      this.stale = true;
      return this.loading;
    }
    this.loading = this.fetchProjects().finally(() => {
      this.loading = null;
      if (!this.stale) return;
      this.stale = false;
      void this.pullProjects();
    });
    return this.loading;
  }

  async fetchProjects() {
    try {
      const html = await getText(this.getAttribute("projects-url"), { signal: this.ac?.signal });
      this.failed = "";
      if (this.loaded && html === this.sourceHTML) return;
      this.sourceHTML = html;
      this.source.innerHTML = html;
      projectSort.sort(this.source);
      this.loaded = true;
    } catch (error) {
      if (!this.ac || this.ac.signal.aborted) return;
      this.failed = error.message || "The projects could not be loaded.";
    }
    if (this.anchor >= 0) this.sync(this.step !== "files", this.rows()[this.index]?.dataset.projectName || "");
  }

  refreshProjects() {
    if (this.loaded) void this.pullProjects();
  }
}

customElements.define("dc-file-mention", FileMention);
