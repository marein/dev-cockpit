// Per-project code editor: lazy directory tree (drawer on small screens, drag
// resizable column on wide ones), tabbed CodeMirror 6 buffers with per-tab undo
// history, quick open palette and a markdown preview. CodeMirror is loaded from
// a CDN; if that fails we fall back to a plain <textarea> so viewing/editing
// still works.
import { notifyError } from "@dc/toast";
import { onServerEvent } from "@dc/events";
import { menuJustClosed, openMenu, wireRowMenus } from "@dc/contextmenu";
import { available as dialogAvailable, confirm as confirmDialog, fire as fireDialog, promptText } from "@dc/dialog";
import { csrfHeaders, ensureOk, getJSON, postForm } from "@dc/http";
import * as projectSort from "@dc/project-sort";
import * as store from "@dc/store";

const MAX_SAVED_TREE_DIRS = 200;
const QUICK_OPEN_LIMIT = 100;
const PREVIEW_DEBOUNCE_MS = 500;
const DIFF_REV = "HEAD";
const TREE_WIDTH_KEY = "dc-editor-tree-width";
const FULLSCREEN_KEY = "dc-editor-fullscreen";
// Whether the tree column is folded away on a wide screen. Per device, like the
// column's width: it is about the screen in front of you, not about the project.
const TREE_FOLD_KEY = "dc-editor-tree-folded";
// How a changed path is marked in the tree: the letter at the end of the row and
// the color both the name and that letter take. The colors are Tabler variables
// through its text utilities, so they follow the light and dark theme. rank
// decides what a folder shows when several kinds sit under it: the most
// pressing one wins.
const GIT_MARKS = {
  conflict: { rank: 5, cls: "text-red", mark: "!", label: "Conflicted" },
  deleted: { rank: 4, cls: "text-red", mark: "D", label: "Deleted" },
  modified: { rank: 3, cls: "text-yellow", mark: "M", label: "Modified" },
  renamed: { rank: 2, cls: "text-azure", mark: "R", label: "Renamed" },
  added: { rank: 1, cls: "text-green", mark: "A", label: "Added" },
  untracked: { rank: 0, cls: "text-green", mark: "A", label: "Untracked" },
};
const GIT_MARK_CLASSES = Object.values(GIT_MARKS).map((m) => m.cls);

async function init(root) {
  const name = root.dataset.editorName;
  const base = `/projects/${encodeURIComponent(name)}/editor`;
  const tabsKey = `dc-editor-tabs:${name}`;
  const treeKey = `dc-editor-tree:${name}`;

  const bodyEl = root.querySelector(".editor-body");
  const treeEl = root.querySelector("[data-editor-tree]");
  const compareBarEl = root.querySelector("[data-editor-compare]");
  const compareNameEls = {
    left: root.querySelector('[data-editor-compare-name="left"]'),
    right: root.querySelector('[data-editor-compare-name="right"]'),
  };
  const compareSaveBtns = {
    left: root.querySelector('[data-editor-compare-save="left"]'),
    right: root.querySelector('[data-editor-compare-save="right"]'),
  };
  const dropHintEl = root.querySelector("[data-editor-drop-hint]");
  const treeColEl = root.querySelector(".editor-tree-col");
  const surfaceEl = root.querySelector("[data-editor-surface]");
  const placeholderEl = root.querySelector("[data-editor-placeholder]");
  const previewPaneEl = root.querySelector("[data-editor-preview-pane]");
  const tabsEl = root.querySelector("[data-editor-tabs]");
  const pathEl = root.querySelector("[data-editor-path]");
  const statusEl = root.querySelector("[data-editor-status]");
  const posEl = root.querySelector("[data-editor-pos]");
  const indentInfoEl = root.querySelector("[data-editor-indent-info]");
  const saveBtn = root.querySelector("[data-editor-save]");
  const refreshBtn = root.querySelector("[data-editor-refresh]");
  const uploadInput = root.querySelector("[data-editor-upload-input]");
  const uploadDirInput = root.querySelector("[data-editor-upload-dir-input]");
  const searchProjectItem = root.querySelector("[data-editor-search-project-item]");
  const findItem = root.querySelector("[data-editor-find-item]");
  const gotoItem = root.querySelector("[data-editor-goto]");
  const saveAllItem = root.querySelector("[data-editor-save-all]");
  const mergeEl = root.querySelector("[data-editor-merge]");
  const viewerEl = root.querySelector("[data-editor-viewer]");
  const drawerToggleBtn = root.querySelector("[data-editor-drawer-toggle]");
  const browseBtn = root.querySelector("[data-editor-browse]");
  const backdropEl = root.querySelector("[data-editor-backdrop]");
  const splitterEl = root.querySelector("[data-editor-splitter]");
  const quickOpenEl = root.querySelector("[data-editor-quickopen]");
  const quickOpenInput = root.querySelector("[data-editor-quickopen-input]");
  const quickOpenList = root.querySelector("[data-editor-quickopen-list]");
  const filesItem = root.querySelector("[data-editor-files-item]");
  const filesCountEl = root.querySelector("[data-editor-files-count]");
  const settingsMenuEl = root.querySelector("[data-editor-settings-menu]");
  const settingsItem = root.querySelector("[data-editor-settings-item]");
  const quickOpenItem = root.querySelector("[data-editor-quick-open-item]");
  const sheetEl = root.querySelector("[data-editor-sheet]");
  const sheetPanelEl = root.querySelector("[data-editor-sheet-panel]");
  const sheetTitleEl = root.querySelector("[data-editor-sheet-title]");
  const sheetBodyEl = root.querySelector("[data-editor-sheet-body]");
  const sheetCloseBtn = root.querySelector("[data-editor-sheet-close]");
  const paneColEl = root.querySelector(".editor-pane-col");

  const editorSettings = loadEditorSettings();
  // The diff runs in the browser, so the limits that keep a slow device
  // responsive ride on the page. How it looks does not: which of the two views
  // it uses and whether unchanged parts are folded are about the screen in
  // front of you, so they sit in the editor's own settings, per device.
  const diffSettings = {
    maxLines: Number(root.dataset.editorDiffMaxLines) || 0,
    maxKiB: Number(root.dataset.editorDiffMaxKib) || 0,
  };
  const ac = new AbortController();
  const signal = ac.signal;
  const mobileMedia = window.matchMedia("(max-width: 767.98px), (max-height: 500px)");
  const pointerMedia = window.matchMedia("(hover: hover) and (pointer: fine)");
  const wideMedia = window.matchMedia("(min-width: 992px)");

  // onCursor runs from inside createEditor's first update, before the const
  // below is bound, so anything of ours that reads the editor waits for this.
  let editorReady = false;
  const editor = await createEditor(surfaceEl, { onChange, onCursor }, editorSettings, signal, mergeEl);
  editorReady = true;
  setupSettingsUI(root, editor, editorSettings, (key) => {
    if (key === "diff_view" || key === "diff_collapse") void reapplyComparison(key);
  });
  wideMedia.addEventListener("change", () => {
    if (editorSettings.diff_view !== "side" && editorSettings.diff_view !== "inline") {
      void reapplyComparison("diff_view");
    }
  }, { signal });
  const syncIndentControl = setupIndentControl(root, editor, editorSettings);

  const tabs = [];
  let activePath = null;
  let selected = null; // { path, isDir } — the tree row used as the "create in" target
  // dir paths kept open so a rebuild doesn't collapse the tree; restored per
  // project so the folder layout survives a reload like the tabs do
  const expanded = new Set(store.getJSON(treeKey, []).filter((p) => typeof p === "string" && p));
  const opening = new Set();
  let statusTimer = 0;
  let previewTimer = 0;
  let svgPreviewUrl = null;
  // The git status arrives as one flat list of changed paths; what a folder
  // shows is derived from it here, so a folded folder still says that something
  // under it moved.
  const gitFiles = new Map();
  const gitDirs = new Map();
  // Entries git reports as a whole directory, path with its trailing slash.
  // They are never a row, they are a rule about everything below them.
  const gitPrefixes = new Map();
  // How many lines came and went per path. Not in the row: there is no width
  // for it in a tree, on a phone least of all, so it lives in the tooltip.
  const gitNumbers = new Map();
  let gitWatchTimer = 0;
  let gitWatching = false;
  // Which renewal chain is the current one. A page that comes back from the
  // background renews at once, and the chain it interrupts must not schedule a
  // second timer behind it.
  let gitWatchGen = 0;
  let gitRepo = false;
  let gitErrorSaid = false;
  // Two status requests can be in flight at once (an event and a click on
  // refresh); the one started last is the one that describes the repository
  // now, whichever order the answers arrive in.
  let gitSeq = 0;

  const activeTab = () => tabs.find((t) => t.path === activePath) || null;
  const tabByPath = (path) => tabs.find((t) => t.path === path) || null;
  const anyDirty = () => tabs.some((t) => t.dirty);
  const baseName = (path) => path.split("/").pop();
  const parentDir = (path) => {
    const i = path.lastIndexOf("/");
    return i >= 0 ? path.slice(0, i) : "";
  };
  const isMarkdown = (fileName) => /\.(md|markdown)$/i.test(fileName);
  const isSvg = (fileName) => /\.svg$/i.test(fileName);
  const isImage = (fileName) => /\.(png|jpe?g|gif|webp|avif|bmp|ico)$/i.test(fileName);
  const isArchive = (fileName) => /\.(tar\.gz|tgz|tar|zip)$/i.test(fileName);
  const isVideo = (fileName) => /\.(mp4|m4v|webm|ogv|mov)$/i.test(fileName);
  const isAudio = (fileName) => /\.(mp3|m4a|wav|oga|ogg|flac)$/i.test(fileName);
  const hasPreview = (fileName) => isMarkdown(fileName) || isSvg(fileName);
  const rawUrl = (path, download) => `${base}/raw?path=${encodeURIComponent(path)}${download ? "&download=1" : ""}`;

  // ---- statusbar -------------------------------------------------------------

  function status(msg, kind) {
    // Errors go to the global toast; the status line stays for transient
    // progress/success ("Saving…", "Saved X").
    clearTimeout(statusTimer);
    if (kind === "error") {
      statusEl.textContent = "";
      statusEl.classList.remove("text-success");
      notifyError(msg);
      return;
    }
    statusEl.textContent = msg || "";
    statusEl.classList.toggle("text-success", kind === "ok");
    if (kind === "ok") {
      statusTimer = setTimeout(() => {
        statusEl.textContent = "";
        statusEl.classList.remove("text-success");
      }, 4000);
    }
  }

  function onCursor(line, col) {
    posEl.textContent = `Ln ${line}, Col ${col}`;
    syncSwipeZone();
  }

  function syncIndentInfo() {
    const tab = activeTab();
    indentInfoEl.hidden = !tab || !!tab.kind || !!tab.compare;
    if (!tab || tab.kind || tab.compare) return;
    const ind = editor.getIndent();
    indentInfoEl.textContent = ind.style === "space" ? `Spaces: ${ind.size}` : `Tab: ${editor.getTabWidth()}`;
    indentInfoEl.title = ind.fromConfig ? "From .editorconfig" : "Editor setting";
  }

  // ---- tabs ------------------------------------------------------------------

  function tabElement(tab) {
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "editor-tab";
    btn.classList.toggle("active", tab.path === activePath);
    btn.classList.toggle("dirty", tab.dirty);
    btn.setAttribute("role", "tab");
    btn.setAttribute("aria-selected", tab.path === activePath ? "true" : "false");
    btn.dataset.path = tab.path;
    // A comparison's path is synthetic and encoded, so it names the two files
    // instead: what stands in the tab is what the tooltip should spell out.
    btn.title = tab.compare ? `${tab.compare.left} ⇄ ${tab.compare.right}` : tab.path;
    const nameEl = document.createElement("span");
    nameEl.className = "editor-tab-name";
    nameEl.textContent = tab.name;
    btn.appendChild(nameEl);
    if (tabs.some((t) => t !== tab && t.name === tab.name)) {
      const hintEl = document.createElement("span");
      hintEl.className = "editor-tab-hint";
      hintEl.textContent = parentDir(tab.path) || "/";
      btn.appendChild(hintEl);
    }
    const stateEl = document.createElement("span");
    stateEl.className = "editor-tab-state";
    stateEl.setAttribute("aria-label", `Close ${tab.name}`);
    stateEl.innerHTML = `<span class="editor-tab-dot"></span><i class="ti ti-x editor-tab-close"></i>`;
    stateEl.addEventListener("click", (e) => {
      e.stopPropagation();
      closeTab(tab.path);
    });
    btn.appendChild(stateEl);
    btn.addEventListener("click", () => {
      if (tab.path === activePath && !pointerMedia.matches && !menuJustClosed()) {
        const rect = btn.getBoundingClientRect();
        openTabMenu(tab.path, rect.left, rect.bottom + 4);
        return;
      }
      activateTab(tab.path);
    });
    btn.addEventListener("auxclick", (e) => {
      if (e.button === 1) {
        e.preventDefault();
        closeTab(tab.path);
      }
    });
    markGitTab(btn);
    return btn;
  }

  // markGitTab says the same thing about a tab that markGitRow says about a
  // tree row: the letter in front of the name and the name in that color. A
  // file git knows nothing about carries nothing at all, an empty strip is
  // quieter than a row of neutral placeholders.
  //
  // The dot on the right stays out of this on purpose. It means unsaved, the
  // buffer against the disk; the git mark means the disk against the
  // repository. Two statements, two places.
  function markGitTab(btn) {
    // A comparison carries no git mark and no path of its own, so its tooltip,
    // set where the tab is built, is left alone here.
    if (btn.dataset.path.startsWith("//compare/")) return;
    const kind = fileKind(btn.dataset.path);
    const nameEl = btn.querySelector(".editor-tab-name");
    if (!nameEl) return;
    nameEl.classList.remove(...GIT_MARK_CLASSES);
    let markEl = btn.querySelector("[data-git-mark]");
    if (!kind) {
      delete btn.dataset.gitStatus;
      markEl?.remove();
      btn.title = btn.dataset.path;
      return;
    }
    const info = GIT_MARKS[kind];
    btn.dataset.gitStatus = kind;
    btn.title = [btn.dataset.path, info.label, numbersText(btn.dataset.path)].filter(Boolean).join(" · ");
    nameEl.classList.add(info.cls);
    if (!markEl) {
      markEl = document.createElement("span");
      markEl.dataset.gitMark = "";
      // In front of the name, and it never shrinks: the name keeps the room it
      // needs to truncate and the close control keeps its full hit area.
      markEl.setAttribute("aria-hidden", "true");
      btn.insertBefore(markEl, nameEl);
    }
    markEl.className = `small fw-bold flex-shrink-0 ${info.cls}`;
    markEl.textContent = info.mark;
  }

  function renderTabs() {
    tabsEl.replaceChildren(...tabs.map(tabElement));
    tabsEl.querySelector(".editor-tab.active")?.scrollIntoView({ block: "nearest", inline: "nearest" });
    syncFilesItem();
    if (sheetKind === "files") renderFilesSheet();
  }

  // ---- the menu and the sheets behind it -------------------------------------

  // Everything the toolbar used to carry as its own icon lives in the one menu
  // now, on every width: the strip keeps the room, and there is one place to
  // look for an action instead of two that differ by screen size.
  function syncFilesItem() {
    filesItem.hidden = tabs.length === 0;
    filesCountEl.textContent = String(tabs.length);
    filesCountEl.hidden = tabs.length < 2;
  }

  // What the sheet is showing, and the nodes it borrowed to show it. The editor
  // settings are a dropdown menu that already carries its rows, their labels and
  // their handlers, so the sheet moves those very nodes in and puts them back on
  // close: one set of controls, one wiring, and every `root.querySelectorAll`
  // sync keeps working while they are adopted.
  let sheetKind = "";
  let sheetAdopted = [];
  let sheetDrag = null;
  const sheetDragging = () => !!(sheetDrag && sheetDrag.active);

  function adoptIntoSheet(node) {
    if (!node) return;
    sheetAdopted.push({ node, parent: node.parentNode, next: node.nextSibling });
    sheetBodyEl.appendChild(node);
  }

  function openSheet(kind, title) {
    if (sheetKind) closeSheet();
    sheetKind = kind;
    sheetTitleEl.textContent = title;
    sheetPanelEl.setAttribute("aria-label", title);
    sheetBodyEl.replaceChildren();
    sheetEl.hidden = false;
  }

  function closeSheet() {
    if (!sheetKind) return;
    for (const item of sheetAdopted.reverse()) item.parent.insertBefore(item.node, item.next);
    sheetAdopted = [];
    sheetBodyEl.replaceChildren();
    sheetEl.hidden = true;
    sheetKind = "";
  }

  function sheetRow(tab) {
    const row = document.createElement("div");
    row.className = "editor-sheet-row";
    row.classList.toggle("active", tab.path === activePath);
    row.dataset.path = tab.path;
    const open = document.createElement("button");
    open.type = "button";
    open.className = "editor-sheet-open";
    const kind = tab.compare || tab.kind ? undefined : fileKind(tab.path);
    if (kind) {
      const mark = document.createElement("span");
      mark.className = `small fw-bold flex-shrink-0 ${GIT_MARKS[kind].cls}`;
      mark.textContent = GIT_MARKS[kind].mark;
      mark.title = GIT_MARKS[kind].label;
      open.appendChild(mark);
    }
    const col = document.createElement("span");
    col.className = "d-flex flex-column min-w-0";
    const nameEl = document.createElement("span");
    nameEl.className = "editor-sheet-name text-truncate";
    nameEl.textContent = tab.name;
    if (kind) nameEl.classList.add(GIT_MARKS[kind].cls);
    const dirEl = document.createElement("span");
    dirEl.className = "editor-sheet-dir text-truncate";
    dirEl.textContent = tab.compare ? tab.compare.left : parentDir(tab.path) || "/";
    col.append(nameEl, dirEl);
    open.appendChild(col);
    if (tab.dirty) {
      const dot = document.createElement("span");
      dot.className = "editor-sheet-dot ms-auto";
      dot.dataset.editorSheetDirty = "";
      dot.title = "Unsaved";
      open.appendChild(dot);
    }
    open.addEventListener("click", () => {
      activateTab(tab.path);
      closeSheet();
    });
    const grip = document.createElement("span");
    grip.className = "editor-sheet-grip";
    grip.dataset.editorSheetHandle = "";
    grip.setAttribute("aria-hidden", "true");
    grip.innerHTML = `<i class="ti ti-grip-vertical"></i>`;
    const close = document.createElement("button");
    close.type = "button";
    close.className = "editor-sheet-close";
    close.dataset.editorSheetCloseTab = "";
    close.setAttribute("aria-label", `Close ${tab.name}`);
    close.innerHTML = `<i class="ti ti-x"></i>`;
    close.addEventListener("click", async () => {
      await closeTab(tab.path);
      if (tabs.length === 0) closeSheet();
    });
    row.append(open, grip, close);
    return row;
  }

  function renderFilesSheet() {
    if (sheetDragging()) return;
    if (tabs.length === 0) {
      closeSheet();
      return;
    }
    sheetBodyEl.replaceChildren(...tabs.map(sheetRow));
  }

  function openFilesSheet() {
    if (tabs.length === 0) return;
    openSheet("files", "Open files");
    renderFilesSheet();
  }

  function openSettingsSheet() {
    openSheet("settings", "Editor settings");
    adoptIntoSheet(settingsMenuEl);
  }

  function updateActionStates() {
    const tab = activeTab();
    const textTab = tab && !tab.kind ? tab : null;
    // A comparison has two buffers and two save buttons of its own, so the one
    // in the toolbar has nothing to act on.
    const fileTab = textTab && !textTab.compare ? textTab : null;
    saveBtn.disabled = !fileTab || !fileTab.dirty;
    // Save shows up when there is something to save instead of standing there
    // greyed out, on every width: the room belongs to the tab strip.
    saveBtn.hidden = saveBtn.disabled;
    for (const el of [findItem, gotoItem]) {
      el.disabled = !textTab;
    }
    // What a single file can do, copy path and download through rename and
    // delete, belongs to the file itself: the tab's menu and the tree row carry
    // it, so the editor menu keeps only what acts on the editor as a whole.
    saveAllItem.disabled = !tabs.some((t) => t.dirty);
  }

  function afterActiveChanged() {
    const tab = activeTab();
    syncCompareBar();
    void applyBlame();
    placeholderEl.hidden = !!tab;
    // A comparison stands for two files, so the line below names both.
    const shown = tab && tab.compare ? `${tab.compare.left} ⇄ ${tab.compare.right}` : tab ? tab.path : "";
    pathEl.textContent = shown;
    pathEl.title = shown;
    posEl.hidden = !tab || !!tab.kind;
    renderTabs();
    updateActionStates();
    syncSwipeZone();
    syncIndentControl();
    syncIndentInfo();
    syncPreview();
    if (tab && !tab.compare) markTreeSelection(tab.path);
    persistTabs();
  }

  function activateTab(path) {
    const tab = tabByPath(path);
    if (!tab || activePath === path) return;
    const prev = activeTab();
    if (prev && prev.compare) editor.captureCompare(prev);
    else if (prev && !prev.kind) editor.captureDoc(prev);
    activePath = path;
    if (tab.kind) {
      editor.setVisible(false);
      editor.exitDiff();
      renderViewer(tab);
    } else if (tab.compare) {
      viewerEl.hidden = true;
      // Nothing else here: the two sided view carries both documents itself,
      // showDoc would put the plain editor over it and drop it again.
    } else {
      viewerEl.hidden = true;
      editor.showDoc(tab);
      editor.setVisible(true);
      if (pointerMedia.matches) editor.focus();
    }
    afterActiveChanged();
    if (tab.compare) void showCompare(tab);
    // The tab carries its own diff mode, so switching back into one restores it.
    else if (!tab.kind) void applyTabDiff(tab);
  }

  function stepTab(direction) {
    if (tabs.length < 2) return;
    const i = tabs.findIndex((t) => t.path === activePath);
    const next = i < 0
      ? (direction > 0 ? 0 : tabs.length - 1)
      : (i + direction + tabs.length) % tabs.length;
    activateTab(tabs[next].path);
  }

  function showEmpty() {
    activePath = null;
    editor.exitDiff();
    editor.setVisible(false);
    viewerEl.hidden = true;
    afterActiveChanged();
  }

  // renderViewer fills the surface for non text tabs: images render inline via
  // the raw endpoint, everything else gets a file card, both with a download.
  function renderViewer(tab) {
    viewerEl.replaceChildren();
    if (tab.kind === "image") {
      const img = document.createElement("img");
      img.className = "editor-viewer-image";
      img.src = rawUrl(tab.path);
      img.alt = tab.name;
      viewerEl.appendChild(img);
    } else if (tab.kind === "video" || tab.kind === "audio") {
      // The player streams from the raw endpoint, so a large file neither loads
      // into the editor nor into memory before it starts.
      const media = document.createElement(tab.kind);
      media.className = tab.kind === "video" ? "editor-viewer-video" : "editor-viewer-audio";
      media.src = rawUrl(tab.path);
      media.controls = true;
      media.preload = "metadata";
      viewerEl.appendChild(media);
    } else {
      const icon = document.createElement("i");
      icon.className = "ti ti-file-unknown fs-1 d-block text-secondary";
      viewerEl.appendChild(icon);
    }
    const meta = document.createElement("div");
    meta.className = "small text-secondary text-break";
    meta.textContent = tab.size ? `${tab.name} · ${formatSize(tab.size)}` : tab.name;
    const actions = document.createElement("div");
    actions.className = "d-flex flex-wrap gap-2 justify-content-center";
    const dl = document.createElement("a");
    dl.className = "btn btn-sm btn-outline-secondary";
    dl.href = rawUrl(tab.path, true);
    dl.setAttribute("download", tab.name);
    dl.setAttribute("data-no-pe", "");
    dl.innerHTML = '<i class="ti ti-download me-1"></i>Download';
    actions.appendChild(dl);
    // An archive is more useful unpacked than downloaded, so it says so here too.
    if (isArchive(tab.name)) {
      const extract = document.createElement("button");
      extract.type = "button";
      extract.className = "btn btn-sm btn-outline-primary";
      extract.dataset.editorExtract = "";
      extract.innerHTML = '<i class="ti ti-file-zip me-1"></i>Extract here';
      extract.addEventListener("click", () => void extractArchive(tab.path));
      actions.appendChild(extract);
    }
    viewerEl.append(meta, actions);
    viewerEl.hidden = false;
  }

  async function closeTab(path, force = false) {
    const i = tabs.findIndex((t) => t.path === path);
    if (i < 0) return;
    const tab = tabs[i];
    if (!force && tab.dirty && !(await confirmDialog({ title: `Discard changes in "${tab.name}"?`, confirmText: "Discard" }))) {
      return;
    }
    tabs.splice(i, 1);
    if (activePath === path) {
      activePath = null;
      const next = tabs[i] || tabs[i - 1];
      if (next) activateTab(next.path);
      else showEmpty();
    } else {
      renderTabs();
      updateActionStates();
      persistTabs();
    }
  }

  async function closeMany(list) {
    for (const tab of [...list]) {
      await closeTab(tab.path);
    }
  }

  // The tab menu carries the same file actions as the tree row, minus the ones
  // that need a folder: a tab is always one file.
  function tabMenuItems(tab) {
    const index = tabs.indexOf(tab);
    const closing = [
      { label: "Close", icon: "ti-x", action: () => closeTab(tab.path) },
      { label: "Close others", icon: "ti-square-x", disabled: tabs.length < 2, action: () => closeMany(tabs.filter((t) => t !== tab)) },
      { label: "Close to the right", icon: "ti-arrow-bar-to-right", disabled: index === tabs.length - 1, action: () => closeMany(tabs.slice(index + 1)) },
      { label: "Close all", icon: "ti-circle-x", action: () => closeMany(tabs) },
    ];
    // A comparison stands for two files, so it keeps only what a tab as such
    // can do.
    if (tab.compare) return closing;
    return [
      ...closing,
      { divider: true },
      { label: "Copy file", icon: "ti-files", action: () => copyToClipboard(tab.path, false) },
      clipboard ? { label: `Paste "${baseName(clipboard.path)}"`, icon: "ti-clipboard", action: () => void pasteInto(parentDir(tab.path)) } : null,
      { label: "Copy path", icon: "ti-copy", action: () => copyPath(tab.path) },
      { label: "Download", icon: "ti-download", action: () => startDownload(tab.path) },
      isArchive(tab.name) ? { label: "Extract here", icon: "ti-file-zip", action: () => void extractArchive(tab.path) } : null,
      { label: "Reveal in tree", icon: "ti-list-tree", action: () => revealInTree(tab.path) },
      { divider: true },
      previewMenuItem(tab),
      diffMenuItem(tab),
      blameMenuItem(tab),
      { label: "Select for compare", icon: "ti-columns-2", action: () => selectForCompare(tab.path) },
      compareSelection && compareSelection !== tab.path ? {
        label: `Compare with "${baseName(compareSelection)}"`,
        icon: "ti-git-compare",
        action: () => void openCompare(compareSelection, tab.path),
      } : null,
      { divider: true },
      { label: "Rename", icon: "ti-pencil", action: () => renameEntry({ path: tab.path, name: tab.name, isDir: false }) },
      { label: "Delete", icon: "ti-trash", danger: true, action: () => deletePath(tab.path) },
    ].filter(Boolean);
  }

  function openTabMenu(path, x, y) {
    const tab = tabByPath(path);
    if (!tab) return;
    openMenu({ x, y, items: tabMenuItems(tab), signal });
  }

  function markDirty(tab, on) {
    if (tab.dirty === on) return;
    tab.dirty = on;
    renderTabs();
    updateActionStates();
    // The blame gutter belongs to the file on disk, so it goes while the buffer
    // is ahead of it and comes back when the save catches up.
    void applyBlame();
  }

  // Dirty means "differs from the saved content", not "was touched": undoing
  // or retyping back to the saved state clears the flag again.
  function onChange() {
    const tab = activeTab();
    if (!tab) return;
    // A comparison has two buffers, and its bar owns both dirty markers.
    if (tab.compare) {
      syncCompareBar();
      return;
    }
    markDirty(tab, !editor.isClean(tab, true));
    schedulePreview(PREVIEW_DEBOUNCE_MS);
  }

  // The open tabs, their order, the active one, the revision each one is
  // compared against and whether the blame gutter is on. Every entry carries a
  // type; a bare string is an older saved state and reads as type "file", so
  // nothing has to be migrated. A comparison is its two paths: the content
  // comes from the disk on restore, unsaved changes fall away like they do for
  // every other tab.
  function persistTabs() {
    const open = tabs.map((t) => {
      if (t.compare) return { type: "compare", left: t.compare.left, right: t.compare.right };
      if (!t.diffRev && !t.blameOn && !t.previewOn) return t.path;
      const entry = { type: "file", path: t.path };
      if (t.diffRev) entry.diff = t.diffRev;
      if (t.blameOn) entry.blame = true;
      if (t.previewOn) entry.preview = true;
      return entry;
    });
    store.setJSON(tabsKey, { open, active: activePath });
  }

  function persistExpanded() {
    store.setJSON(treeKey, [...expanded].slice(0, MAX_SAVED_TREE_DIRS));
  }

  // savedEntries reads a stored set, whatever age it is: bare strings are file
  // paths, and the legacy diff map marks which of them had a comparison open.
  function savedEntries(saved) {
    const legacy = saved.diff && typeof saved.diff === "object" ? saved.diff : {};
    const entries = [];
    for (const e of saved.open) {
      if (typeof e === "string" && e) {
        const old = legacy[e];
        entries.push({ type: "file", path: e, diff: old && old.mode && old.mode !== "off" ? DIFF_REV : "", blame: false, preview: false });
      } else if (e && typeof e === "object" && e.type === "compare" && typeof e.left === "string" && typeof e.right === "string") {
        entries.push(e);
      } else if (e && typeof e === "object" && typeof e.path === "string" && e.path) {
        entries.push({
          type: "file",
          path: e.path,
          diff: typeof e.diff === "string" ? e.diff : "",
          blame: e.blame === true,
          preview: e.preview === true,
        });
      }
    }
    return entries;
  }

  async function restoreTabs() {
    const saved = store.getJSON(tabsKey, null);
    if (!saved || !Array.isArray(saved.open) || saved.open.length === 0) return;
    const entries = savedEntries(saved);
    const results = await Promise.allSettled(entries.map((entry) => (entry.type === "compare"
      ? compareTabFor(entry.left, entry.right)
      : getJSON(`${base}/file?path=${encodeURIComponent(entry.path)}`, { signal }).then((data) => tabFor(entry.path, data)))));
    for (let i = 0; i < results.length; i++) {
      if (results[i].status !== "fulfilled" || !results[i].value || tabByPath(results[i].value.path)) continue;
      const tab = results[i].value;
      if (entries[i].type === "file" && entries[i].diff) tab.diffRev = entries[i].diff;
      if (entries[i].type === "file" && entries[i].blame && !tab.kind) tab.blameOn = true;
      // The two share the surface, so a stored state that somehow carries both
      // keeps the diff and drops the preview.
      if (entries[i].type === "file" && entries[i].preview && !tab.kind && !tab.diffRev) tab.previewOn = true;
      tabs.push(tab);
    }
    if (tabs.length === 0) {
      persistTabs();
      return;
    }
    activateTab(tabByPath(saved.active) ? saved.active : tabs[0].path);
  }

  // tabFor builds a tab from the /file response: a CodeMirror doc for text, a
  // viewer tab (image or plain binary) for everything the editor cannot edit.
  async function tabFor(path, data) {
    const name = baseName(path);
    if (data.binary) {
      const kind = isImage(name) ? "image" : isVideo(name) ? "video" : isAudio(name) ? "audio" : "binary";
      return { path, name, kind, size: data.size || 0, dirty: false };
    }
    return {
      path,
      name,
      handle: await editor.createDoc(data.content, name),
      editorConfig: data.editorConfig || {},
      dirty: false,
    };
  }

  async function openPath(path, { keepDrawer = false } = {}) {
    if (!keepDrawer) closeDrawer();
    if (tabByPath(path)) {
      activateTab(path);
      return;
    }
    if (opening.has(path)) return;
    opening.add(path);
    status("Loading…");
    try {
      const data = await getJSON(`${base}/file?path=${encodeURIComponent(path)}`, { signal });
      if (signal.aborted || tabByPath(path)) return;
      tabs.push(await tabFor(path, data));
      activateTab(path);
      status("");
    } catch (err) {
      status(err.message, "error");
    } finally {
      opening.delete(path);
    }
  }

  // ---- tree ------------------------------------------------------------------

  function setSelected(path, isDir, rowEl) {
    selected = { path, isDir };
    treeEl.querySelectorAll(".editor-item.selected").forEach((el) => el.classList.remove("selected"));
    if (rowEl) rowEl.classList.add("selected");
  }

  function markTreeSelection(path) {
    setSelected(path, false, treeEl.querySelector(`.editor-file[data-path="${CSS.escape(path)}"]`));
  }

  // targetDir is the folder new items are created in: the selected folder, or
  // the parent of the selected file, or "" for the project root.
  function targetDir() {
    if (!selected) return "";
    return selected.isDir ? selected.path : parentDir(selected.path);
  }

  async function listDir(path) {
    return (await getJSON(`${base}/list?path=${encodeURIComponent(path)}`, { signal })).entries || [];
  }

  // Folders that were open before a rebuild fetch their children after the first
  // paint. loadTree waits for them, otherwise the tree is still short when the
  // scroll position is put back.
  const pendingDirLoads = [];

  function renderEntries(container, entries, depth) {
    container.innerHTML = "";
    if (entries.length === 0) {
      const empty = document.createElement("div");
      empty.className = "editor-empty text-secondary small";
      empty.style.paddingLeft = `${depth * 14 + 12}px`;
      empty.textContent = "empty";
      container.appendChild(empty);
      return;
    }
    for (const entry of entries) {
      container.appendChild(entry.isDir ? dirNode(entry, depth) : fileNode(entry, depth));
    }
  }

  function rowLabel(entry, icon, depth) {
    const row = document.createElement("div");
    row.className = "editor-item";
    row.style.paddingLeft = `${depth * 14 + 8}px`;
    row.setAttribute("role", "treeitem");
    row.dataset.path = entry.path;
    // What the row says about itself before git says anything. markGitRow
    // composes the tooltip from it, so a mark never eats the size or the path.
    row.dataset.title = entry.path;
    if (entry.isDir) row.dataset.dir = "1";
    row.innerHTML = `<i class="ti ${icon} editor-item-icon"></i><span class="editor-item-name text-truncate">${escapeHtml(entry.name)}</span>`;
    // Draggable on a fine pointer only. A row that carries it hands the long
    // press to the browser's own drag lift on touch, and iOS then never lets
    // the press become the row's menu; moving a file is a mouse gesture here,
    // the menu is what touch needs.
    row.draggable = pointerMedia.matches;
    row.addEventListener("dragstart", (e) => {
      dragging = { path: entry.path, isDir: !!entry.isDir };
      row.classList.add("editor-dragging");
      e.dataTransfer.effectAllowed = "move";
      e.dataTransfer.setData("text/plain", entry.path);
    }, { signal });
    row.addEventListener("dragend", () => {
      row.classList.remove("editor-dragging");
      endDrag();
    }, { signal });
    markGitRow(row);
    return row;
  }

  function clearTreeSelection() {
    selected = null;
    treeEl.querySelectorAll(".editor-item.selected").forEach((el) => el.classList.remove("selected"));
  }

  function treeMenuItems(entry) {
    const items = [
      { label: "New file", icon: "ti-file-plus", action: () => createFile() },
      { label: "New folder", icon: "ti-folder-plus", action: () => createFolder() },
      { label: "Upload files", icon: "ti-upload", action: () => uploadInput.click() },
      { label: "Upload folder", icon: "ti-folder-up", action: () => uploadDirInput.click() },
    ];
    // The clipboard is this browser's own; paste targets the row's folder, or
    // the project root from the empty area below the tree.
    const pasteDir = entry ? (entry.isDir ? entry.path : parentDir(entry.path)) : "";
    if (clipboard) {
      items.push({ divider: true });
      items.push({
        label: `Paste "${baseName(clipboard.path)}"`,
        icon: "ti-clipboard",
        action: () => void pasteInto(pasteDir),
      });
    }
    if (!entry) {
      items.push({ divider: true });
      items.push({ label: "Refresh", icon: "ti-refresh", action: () => loadTree() });
      return items;
    }
    items.push({ divider: true });
    items.push({
      label: entry.isDir ? "Copy folder" : "Copy file",
      icon: "ti-files",
      action: () => copyToClipboard(entry.path, entry.isDir),
    });
    items.push({ label: "Copy path", icon: "ti-copy", action: () => copyPath(entry.path) });
    items.push({
      label: entry.isDir ? "Download as tar.gz" : "Download",
      icon: "ti-download",
      action: () => (entry.isDir ? startDownload(entry.path, true) : startDownload(entry.path)),
    });
    if (!entry.isDir && isArchive(baseName(entry.path))) {
      items.push({ label: "Extract here", icon: "ti-file-zip", action: () => void extractArchive(entry.path) });
    }
    // What git says about one file, then two files against each other, picked
    // in two steps because the second one is usually somewhere else in the
    // tree than the first.
    if (!entry.isDir) {
      items.push({ divider: true });
      const tab = tabByPath(entry.path);
      if (hasPreview(baseName(entry.path))) {
        items.push({
          label: tab && tab.previewOn ? "Hide preview" : "Show preview",
          icon: tab && tab.previewOn ? "ti-eye-off" : "ti-eye",
          action: () => void previewFromTree(entry.path),
        });
      }
      if (gitRepo && editor.canDiff) {
        items.push({
          label: tab && tab.diffRev ? "Hide git diff" : "Show git diff",
          icon: "ti-git-compare",
          action: () => void diffFromTree(entry.path),
        });
      }
      if (gitRepo && editor.canBlame) {
        items.push({
          label: tab && tab.blameOn ? "Hide git blame" : "Show git blame",
          icon: "ti-user-code",
          action: () => void blameFromTree(entry.path),
        });
      }
      items.push({ label: "Select for compare", icon: "ti-columns-2", action: () => selectForCompare(entry.path) });
      if (compareSelection && compareSelection !== entry.path) {
        items.push({
          label: `Compare with "${baseName(compareSelection)}"`,
          icon: "ti-git-compare",
          action: () => void openCompare(compareSelection, entry.path),
        });
      }
    }
    items.push({ divider: true });
    items.push({
      label: "Rename",
      icon: "ti-pencil",
      action: () => renameEntry({ path: entry.path, name: baseName(entry.path), isDir: entry.isDir }),
    });
    items.push({ label: "Delete", icon: "ti-trash", danger: true, action: () => deletePath(entry.path) });
    return items;
  }

  function openTreeMenu(row, x, y) {
    if (row && row.dataset.path) {
      setSelected(row.dataset.path, !!row.dataset.dir, row);
      openMenu({ x, y, items: treeMenuItems({ path: row.dataset.path, isDir: !!row.dataset.dir }), signal });
    } else {
      clearTreeSelection();
      openMenu({ x, y, items: treeMenuItems(null), signal });
    }
    return true;
  }

  function dirNode(entry, depth) {
    const wrap = document.createElement("div");
    const row = rowLabel(entry, "ti-chevron-right", depth);
    row.classList.add("editor-dir");
    // A drag that rests on a closed folder opens it, so the opener has to be
    // reachable from the drag handlers.
    dirOpeners.set(row, (open) => setOpen(open));
    const children = document.createElement("div");
    children.className = "editor-children";
    children.hidden = true;
    let loaded = false;

    async function setOpen(open) {
      children.hidden = !open;
      row.querySelector(".editor-item-icon").classList.toggle("editor-open", open);
      if (!open) {
        // Closing a folder closes everything inside it: the rendered children
        // fold back and their entries leave the saved set, so reopening it (now
        // or after a reload) starts collapsed.
        for (const path of [...expanded]) {
          if (path === entry.path || path.startsWith(entry.path + "/")) expanded.delete(path);
        }
        persistExpanded();
        for (const nested of children.querySelectorAll(".editor-children")) nested.hidden = true;
        for (const icon of children.querySelectorAll(".editor-item-icon.editor-open")) {
          icon.classList.remove("editor-open");
        }
        return;
      }
      expanded.add(entry.path);
      persistExpanded();
      if (loaded) return;
      children.innerHTML = `<div class="text-secondary small" style="padding-left:${(depth + 1) * 14 + 12}px">Loading…</div>`;
      try {
        renderEntries(children, await listDir(entry.path), depth + 1);
        loaded = true;
      } catch (err) {
        children.innerHTML = `<div class="text-danger small" style="padding-left:${(depth + 1) * 14 + 12}px">${escapeHtml(err.message)}</div>`;
      }
    }

    row.addEventListener("click", () => {
      setSelected(entry.path, true, row);
      setOpen(children.hidden);
    });
    wrap.appendChild(row);
    wrap.appendChild(children);
    // Re-open dirs that were open before a rebuild; children restore recursively.
    if (expanded.has(entry.path)) pendingDirLoads.push(setOpen(true));
    return wrap;
  }

  function fileNode(entry, depth) {
    const row = rowLabel(entry, fileIcon(entry.name), depth);
    row.classList.add("editor-file");
    row.dataset.title = entry.sizeText ? `${entry.path} · ${entry.sizeText}` : entry.path;
    markGitRow(row);
    if (entry.path === activePath) row.classList.add("selected");
    row.addEventListener("click", () => {
      setSelected(entry.path, false, row);
      openPath(entry.path);
    });
    return row;
  }

  // Every file action rebuilds the tree, so it also has to put the scroll
  // position back; landing at the top after a rename or an upload loses the
  // place you were working in.
  async function loadTree() {
    const top = treeEl.scrollTop;
    selected = null; // rebuilding the DOM drops the highlight; keep state in sync
    treeEl.innerHTML = `<div class="text-secondary small p-3">Loading…</div>`;
    try {
      pendingDirLoads.length = 0;
      renderEntries(treeEl, await listDir(""), 0);
      if (activePath) markTreeSelection(activePath);
      // Nested folders queue more loads while the outer ones settle.
      for (let round = 0; round < 12 && pendingDirLoads.length; round += 1) {
        await Promise.allSettled(pendingDirLoads.splice(0));
      }
      treeEl.scrollTop = top;
    } catch (err) {
      treeEl.innerHTML = `<div class="text-danger small p-3">${escapeHtml(err.message)}</div>`;
    }
  }

  // ---- git -------------------------------------------------------------------

  // gitKind reduces the two porcelain codes of one entry to what the tree shows.
  // The worktree code wins over the index one: what is on disk is what the
  // person is looking at.
  function gitKind(entry) {
    const index = entry.index || ".";
    const worktree = entry.worktree || ".";
    if (index === "U" || worktree === "U" || (index === "A" && worktree === "A") || (index === "D" && worktree === "D")) {
      return "conflict";
    }
    if (worktree === "?") return "untracked";
    const code = worktree !== "." ? worktree : index;
    if (code === "D") return "deleted";
    if (code === "A") return "added";
    if (code === "R" || code === "C") return "renamed";
    return "modified";
  }

  // applyGitState is the one place the marks come from: what the working copy
  // carries, path by path, with the line counts for the tooltips.
  function applyGitState(changes) {
    gitFiles.clear();
    gitDirs.clear();
    gitPrefixes.clear();
    gitNumbers.clear();
    const put = (path, kind, entry) => {
      // A path with a trailing slash is a directory git reports in one line,
      // which it does for a directory that is untracked as a whole. It is not a
      // file: a row from it would carry no name at all.
      if (path.endsWith("/")) gitPrefixes.set(path, kind);
      else gitFiles.set(path, kind);
      if (entry && (entry.added || entry.removed || entry.binary)) {
        gitNumbers.set(path, { added: entry.added || 0, removed: entry.removed || 0, binary: !!entry.binary });
      }
    };
    for (const entry of (changes && changes.worktree) || []) {
      put(entry.path, gitKind(entry), entry);
    }
    for (const path of gitFiles.keys()) {
      const kind = gitFiles.get(path);
      for (let dir = parentDir(path); dir; dir = parentDir(dir)) {
        const held = gitDirs.get(dir);
        if (!held || GIT_MARKS[kind].rank > GIT_MARKS[held].rank) gitDirs.set(dir, kind);
      }
    }
    for (const prefix of gitPrefixes.keys()) {
      const kind = gitPrefixes.get(prefix);
      for (let dir = parentDir(prefix); dir; dir = parentDir(dir)) {
        const held = gitDirs.get(dir);
        if (!held || GIT_MARKS[kind].rank > GIT_MARKS[held].rank) gitDirs.set(dir, kind);
      }
    }
    for (const row of treeEl.querySelectorAll(".editor-item[data-path]")) markGitRow(row);
    // The open tabs say the same thing, so a change from outside has to reach
    // them too, not only the tree.
    for (const btn of tabsEl.querySelectorAll(".editor-tab[data-path]")) markGitTab(btn);
    syncFilesItem();
    if (sheetKind === "files") renderFilesSheet();
  }

  // numbersText is what the tooltip says about size: how many lines came and
  // went, or that there are no lines to count at all.
  function numbersText(path) {
    const n = gitNumbers.get(path);
    if (!n) return "";
    if (n.binary) return "binary";
    return `+${n.added} −${n.removed}`;
  }

  // markGitRow puts the mark on one tree row, or takes it off again. It runs
  // when a row is built and when a fresh status arrives, so rows that appear
  // later (a folder opened after the fact) are marked too.
  // What a path carries. This is a question, never a precomputed list: a row
  // that comes into existence later, however deep, asks it and gets its answer
  // straight away. First the path's own entry, then the folders above it against
  // the directories git reported as a whole, because everything below such a
  // directory is new.
  function statusFor(path) {
    const own = gitFiles.get(path);
    if (own) return own;
    for (let dir = parentDir(path); ; dir = parentDir(dir)) {
      const rule = gitPrefixes.get(`${dir}/`);
      if (rule) return rule;
      if (!dir) return undefined;
    }
  }
  const fileKind = (path) => statusFor(path);
  // A folder shows what is summed up under it, and a folder inside a wholly new
  // one is new like everything else there.
  const dirKind = (path) => gitDirs.get(path) || statusFor(`${path}/`) || statusFor(path);

  function markGitRow(row) {
    const isDir = !!row.dataset.dir;
    const kind = isDir ? dirKind(row.dataset.path) : fileKind(row.dataset.path);
    const nameEl = row.querySelector(".editor-item-name");
    if (!nameEl) return;
    nameEl.classList.remove(...GIT_MARK_CLASSES);
    let markEl = row.querySelector("[data-git-mark]");
    if (!kind) {
      delete row.dataset.gitStatus;
      markEl?.remove();
      row.title = row.dataset.title || row.dataset.path;
      return;
    }
    const info = GIT_MARKS[kind];
    row.dataset.gitStatus = kind;
    nameEl.classList.add(info.cls);
    if (!markEl) {
      markEl = document.createElement("span");
      markEl.dataset.gitMark = "";
      row.appendChild(markEl);
    }
    markEl.className = `small fw-bold flex-shrink-0 ${info.cls}`;
    // A folder says that something under it moved, not what: a dot, in the color
    // of the most pressing kind it holds.
    markEl.textContent = isDir ? "•" : info.mark;
    markEl.title = isDir ? `${info.label} inside` : info.label;
    row.title = [row.dataset.title || row.dataset.path, isDir ? `${info.label} inside` : info.label, isDir ? "" : numbersText(row.dataset.path)]
      .filter(Boolean).join(" · ");
  }

  async function loadGitStatus() {
    const seq = ++gitSeq;
    try {
      const changes = await getJSON(`${base}/git/changes`, { signal });
      // A slower answer to an older question says nothing about the repository
      // as it is now, and applying it would undo the newer one.
      if (seq !== gitSeq) return;
      gitRepo = !!changes.repo;
      gitErrorSaid = false;
      // The answer on the root: the per-file git entries render on it, and a
      // test that waits for the status has something honest to wait for.
      root.dataset.gitRepo = gitRepo ? "1" : "0";
      applyGitState(changes);
      // Only a repository is worth watching. A project that becomes one later
      // shows up on the next refresh, which is cheaper than polling git for
      // every open editor that has no repository at all.
      if (gitRepo) startGitWatch();
      // A tab restored into a diff waits for this answer, see applyTabDiff.
      if (gitRepo) void resumeTabDiff();
      // A line that moved belongs to a different commit than it did before, so
      // the gutter is read again whenever the file could have moved under it.
      void applyBlame(true);
    } catch (err) {
      if (signal.aborted) return;
      console.warn("git status unavailable", err);
      if (!gitErrorSaid) {
        gitErrorSaid = true;
        status(err.message || "The git status could not be read.", "error");
      }
    }
  }

  // The server polls a project only while a client says it is watching, so the
  // page renews its watch for as long as it is open and lets it lapse when the
  // element goes away.
  function startGitWatch() {
    if (gitWatching) return;
    gitWatching = true;
    void renewGitWatch(++gitWatchGen);
  }

  // A page that was in the background had its renewal timer throttled, so the
  // window has most likely lapsed and the poller stopped. This renews on the
  // spot instead of waiting out the rest of a timer that was frozen.
  function renewGitWatchNow() {
    if (!gitWatching) return;
    clearTimeout(gitWatchTimer);
    void renewGitWatch(++gitWatchGen);
  }

  async function renewGitWatch(gen) {
    if (signal.aborted || gen !== gitWatchGen) return;
    let next = 15000;
    try {
      const res = await postForm(`${base}/git/watch`, {});
      const data = await res.json();
      if (!data.watching) { // polling is off, nothing to renew
        gitWatching = false;
        return;
      }
      next = Math.max(5000, (Number(data.seconds) || 30) * 500);
    } catch {
      // The next attempt tries again; a lapsed window only means the poller
      // stops until this page renews it.
    }
    if (signal.aborted || gen !== gitWatchGen) return;
    gitWatchTimer = setTimeout(() => renewGitWatch(gen), next);
  }

  // catchUpGit is what a page does when it has been away: on a locked phone the
  // renewal timer is throttled, the watch window lapses and the poller ends, so
  // everything that happened in the meantime was never published and never will
  // be. The page asks for all of it itself rather than leaving an old tree
  // standing until somebody presses refresh.
  function catchUpGit() {
    void loadGitStatus();
    void refreshDiffHead();
    renewGitWatchNow();
  }

  // ---- diff ------------------------------------------------------------------

  // The diff is one switch on a normal file tab: the same buffer, shown next
  // to or over what the revision on the tab has, which is HEAD, the last
  // commit, and nothing else for now. Nothing here ever
  // writes into that buffer, so a tab with unsaved work can be switched,
  // compared and switched back without losing a character. Side by side or
  // inline comes from the editor settings, automatic picks by the room.
  // Building a diff waits for a request; a newer build (or a tab switch) makes
  // the one in flight void, so a late answer never paints over what is open.
  let diffSeq = 0;

  // resolveDiffView turns the setting into one of the two views. Automatic is
  // side by side where there is room for two columns, inline below that.
  function resolveDiffView() {
    const view = editorSettings.diff_view;
    if (view === "side" || view === "inline") return view;
    return wideMedia.matches ? "side" : "inline";
  }

  // reapplyComparison rebuilds what is open so a setting that was just picked
  // shows at once, without a request: the revision text and both sides of a
  // comparison are already in hand. The view applies to a diff alone, a
  // comparison of two files is always side by side; the folding applies to
  // both. The price is the same one every view switch pays, the undo history,
  // see the comment on setDiff.
  async function reapplyComparison(key) {
    const tab = activeTab();
    if (!tab) return;
    if (tab.compare) {
      if (key === "diff_view" || !editor.comparing()) return;
      editor.captureCompare(tab);
      await showCompare(tab);
      return;
    }
    if (tab.kind || !tab.diffRev || tab.diffOriginal == null) return;
    await editor.setDiff({
      mode: resolveDiffView(),
      original: tab.diffOriginal,
      name: tab.name,
      collapse: editorSettings.diff_collapse,
      valid: () => activeTab() === tab,
    });
  }

  async function fetchHead(path) {
    return getJSON(`${base}/git/file?path=${encodeURIComponent(path)}`, { signal });
  }

  // withinDiffLimits keeps a phone from freezing on a file nobody wants to see
  // diffed. Over the limit it asks instead of deciding.
  async function withinDiffLimits(working, original) {
    const lines = Math.max(countLines(working), countLines(original));
    const kib = Math.max(byteLength(working), byteLength(original)) / 1024;
    const overLines = diffSettings.maxLines > 0 && lines > diffSettings.maxLines;
    const overSize = diffSettings.maxKiB > 0 && kib > diffSettings.maxKiB;
    if (!overLines && !overSize) return true;
    const reason = overLines
      ? `${lines.toLocaleString()} lines`
      : `${Math.round(kib).toLocaleString()} KiB`;
    return confirmDialog({
      title: "Build this diff?",
      html: `<div class="text-secondary">This file is ${escapeHtml(reason)}. Diffing it can take a while on a slow device.</div>`,
      confirmText: "Diff anyway",
      icon: "question",
    });
  }

  // The switch is an entry of the file's context menu, on its tab and on its
  // tree row, beside the blame gutter's: both are statements about one file,
  // so they live where the file is, not in the editor menu.
  function diffMenuItem(tab) {
    if (!gitRepo || !editor.canDiff || !tab || tab.kind || tab.compare) return null;
    return {
      label: tab.diffRev ? "Hide git diff" : "Show git diff",
      icon: "ti-git-compare",
      action: () => void toggleTabDiff(tab),
    };
  }

  async function toggleTabDiff(tab) {
    const next = tab.diffRev ? "" : DIFF_REV;
    if (tab.path === activePath) {
      await applyDiff(next);
      return;
    }
    // A background tab only carries the wish; applyTabDiff builds it when the
    // tab comes to the front.
    tab.diffRev = next;
    if (!next) tab.diffOriginal = null;
    persistTabs();
  }

  // diffFromTree reaches the same switch from a tree row: an open file toggles
  // in place, a closed one opens into the comparison.
  async function diffFromTree(path) {
    let tab = tabByPath(path);
    if (!tab) {
      await openPath(path);
      tab = tabByPath(path);
    }
    if (!tab || tab.kind || tab.compare) return;
    await toggleTabDiff(tab);
  }

  // applyDiff compares the active tab against rev; an empty rev takes the
  // comparison off again. ask is false only where the person already answered
  // the size question.
  async function applyDiff(rev, { ask = true } = {}) {
    const tab = activeTab();
    if (!tab || tab.kind || tab.compare || !editor.canDiff) return;
    const seq = ++diffSeq;
    const current = () => seq === diffSeq && activeTab() === tab;
    tab.diffRev = "";
    tab.diffOriginal = null;
    if (!rev) {
      await editor.setDiff({ mode: "off", name: tab.name, valid: current });
      persistTabs();
      return;
    }
    let data;
    try {
      data = await fetchHead(tab.path);
    } catch (err) {
      if (!current()) return;
      if (!signal.aborted) status(err.message, "error");
      // The switch was cleared above, so the stored state has to hear about it
      // too: leaving it on means a reload comes back into a comparison the tab
      // is not in, and any later save of the set would drop it after all.
      persistTabs();
      return;
    }
    if (!current()) return;
    if (data.binary) {
      status(data.reason === "large"
        ? "That revision is too large to diff."
        : "That revision holds binary content, there is nothing to diff.", "error");
      persistTabs();
      return;
    }
    const original = data.content || "";
    const working = editor.valueOf(tab, true);
    if (ask && !(await withinDiffLimits(working, original))) {
      // Declined: the tab is not in a comparison, and a reload must not put it
      // in one and ask again.
      if (current()) persistTabs();
      return;
    }
    if (!current()) return; // the tab or the wish changed while this loaded
    // Preview and diff both want the whole surface; the one you just asked for
    // wins.
    if (tab.previewOn) {
      tab.previewOn = false;
      syncPreview();
    }
    tab.diffRev = rev;
    tab.diffOriginal = original;
    try {
      await editor.setDiff({
        mode: resolveDiffView(),
        original,
        name: tab.name,
        collapse: editorSettings.diff_collapse,
        valid: current,
      });
    } catch (err) {
      // A diff that cannot be built must never leave an empty surface: the file
      // comes back the way it was, and the line below the editor says why.
      console.error("diff failed", err);
      editor.exitDiff();
      tab.diffRev = "";
      tab.diffOriginal = null;
      status("The diff could not be built, the file is open as usual.");
      notifyError(err.message || "The diff could not be built.");
      persistTabs();
      return;
    }
    status(data.exists === false ? "Not in HEAD yet" : "");
    persistTabs();
  }

  // applyTabDiff restores the switch a tab carries, after a tab switch or a
  // reload. It is the only place a diff is built without a click.
  //
  // Without a repository it builds nothing, and that is not the same as
  // forgetting the switch: HEAD would answer "not in there", the file would
  // read as one long addition, and the menu entry that could turn it off is
  // gone exactly then. The switch stays on the tab and resumeTabDiff takes it
  // up as soon as git says there is a repository after all.
  async function applyTabDiff(tab) {
    if (!tab || tab.kind || !tab.diffRev || !editor.canDiff || !gitRepo) {
      diffSeq += 1; // a build still in flight belongs to the tab we just left
      return;
    }
    await applyDiff(tab.diffRev);
  }

  // resumeTabDiff builds the diff a tab was restored with once the status has
  // arrived. Only an unbuilt one qualifies: a finished one carries its
  // revision text.
  async function resumeTabDiff() {
    const tab = activeTab();
    if (!tab || tab.kind || !tab.diffRev || tab.diffOriginal != null) return;
    await applyTabDiff(tab);
  }

  // refreshDiffHead follows HEAD under an open diff: when a commit moves it,
  // the revision side is fetched again and replaced in place. Only that side
  // moves, the buffer belongs to the person in front of it, and the dirty
  // marker and the undo history stay untouched.
  async function refreshDiffHead() {
    const tab = activeTab();
    if (!tab || tab.kind || tab.compare || !tab.diffRev || tab.diffOriginal == null) return;
    const seq = diffSeq;
    try {
      const data = await fetchHead(tab.path);
      if (data.binary || activeTab() !== tab || seq !== diffSeq) return;
      if (!tab.diffRev || tab.diffOriginal == null) return;
      const fresh = data.content || "";
      if (fresh === tab.diffOriginal) return;
      tab.diffOriginal = fresh;
      await editor.setOriginal({
        original: fresh,
        collapse: editorSettings.diff_collapse,
        valid: () => activeTab() === tab && seq === diffSeq,
      });
    } catch (err) {
      void err; // the next event asks again
    }
  }

  // ---- blame -----------------------------------------------------------------

  // Who last touched each line, in a gutter next to it. The switch belongs to
  // the file, not to the editor: it rides on the tab (`tab.blameOn`), persists
  // with the tab state, and is toggled from the file's own context menu, on its
  // tab or on its tree row.
  let blameFor = ""; // the path the blame in the editor belongs to
  let blameSeq = 0;

  function blameMenuItem(tab) {
    if (!gitRepo || !editor.canBlame || !tab || tab.kind || tab.compare) return null;
    return {
      label: tab.blameOn ? "Hide git blame" : "Show git blame",
      icon: "ti-user-code",
      action: () => toggleTabBlame(tab),
    };
  }

  function toggleTabBlame(tab) {
    tab.blameOn = !tab.blameOn;
    persistTabs();
    if (tab.blameOn && tab.dirty) status("Blame is what git has, it shows after the save.");
    if (tab.path === activePath) void applyBlame(true);
  }

  // blameFromTree reaches the same switch from a tree row: an open file toggles
  // in place, a closed one opens with the gutter on.
  async function blameFromTree(path) {
    let tab = tabByPath(path);
    if (!tab) {
      await openPath(path);
      tab = tabByPath(path);
    }
    if (!tab || tab.kind || tab.compare) return;
    toggleTabBlame(tab);
  }

  // applyBlame puts the gutter on the open file, or takes it off. It asks the
  // server again whenever the file changes under it, because a line that moved
  // belongs to a different commit than it did before.
  async function applyBlame(force = false) {
    const tab = activeTab();
    const textTab = tab && !tab.kind && !tab.compare ? tab : null;
    // The gutter says what git has, and git has what is on disk. An unsaved
    // buffer no longer lines up with it line for line, so it goes away rather
    // than pointing at the wrong commits, and comes back with the save.
    if (!textTab || !textTab.blameOn || !gitRepo || !editor.canBlame || textTab.dirty) {
      blameFor = "";
      editor.setBlame(null);
      return;
    }
    if (!force && blameFor === textTab.path) return;
    const seq = ++blameSeq;
    try {
      const data = await getJSON(`${base}/git/blame?path=${encodeURIComponent(textTab.path)}`, { signal });
      if (seq !== blameSeq || activeTab() !== textTab || !textTab.blameOn) return;
      blameFor = textTab.path;
      const has = !!data.repo && (data.lines || []).length > 0;
      editor.setBlame(has ? data : null);
      if (!has) status("Nothing to blame in this file, git does not know it yet.");
    } catch (err) {
      // A file git has never seen has no blame, and that is not an error worth
      // a toast: the gutter simply stays away. The same check as above first,
      // though: a failure for the file you just left must not clear the gutter
      // of the one you are in now, nor claim that one's place in blameFor.
      if (seq !== blameSeq || activeTab() !== textTab) return;
      if (!signal.aborted) console.warn("blame unavailable", err);
      blameFor = textTab.path;
      editor.setBlame(null);
    }
  }

  // ---- file comparison -------------------------------------------------------

  // Two files on the disk against each other. It is the one comparison where
  // neither side is a revision: both are real files, so both are writable and
  // each one saves itself. Picking is two steps in the tree menu, the way every
  // file manager does it, because the second file is usually somewhere else in
  // the tree than the first.
  //
  // The tab's path is synthetic, so it can never collide with a file: it starts
  // with a double slash, which no project relative path does, and both halves
  // are encoded, so the path stays usable in a selector.
  const comparePath = (left, right) => `//compare/${encodeURIComponent(left)}/${encodeURIComponent(right)}`;
  let compareSelection = null;

  function selectForCompare(path) {
    compareSelection = path;
    status(`${path} selected for compare`, "ok");
  }

  // compareTabFor reads both sides from the disk and builds the tab. It answers
  // null when one of them is not text; restoring a saved comparison uses it
  // too, which is why the size question is not asked in here.
  async function compareTabFor(left, right) {
    const [a, b] = await Promise.all([
      getJSON(`${base}/file?path=${encodeURIComponent(left)}`, { signal }),
      getJSON(`${base}/file?path=${encodeURIComponent(right)}`, { signal }),
    ]);
    if (a.binary || b.binary) return null;
    const leftText = a.content || "";
    const rightText = b.content || "";
    return {
      path: comparePath(left, right),
      name: `${baseName(left)} ⇄ ${baseName(right)}`,
      compare: {
        left,
        right,
        leftDoc: leftText,
        rightDoc: rightText,
        leftSaved: leftText,
        rightSaved: rightText,
        leftDirty: false,
        rightDirty: false,
      },
      dirty: false,
    };
  }

  async function openCompare(left, right) {
    const path = comparePath(left, right);
    if (tabByPath(path)) {
      activateTab(path);
      return;
    }
    closeDrawer();
    status("Loading…");
    try {
      const tab = await compareTabFor(left, right);
      if (signal.aborted) return;
      if (!tab) {
        status("A file the editor cannot open has nothing to compare.", "error");
        return;
      }
      if (!(await withinDiffLimits(tab.compare.leftDoc, tab.compare.rightDoc))) {
        status("");
        return;
      }
      if (signal.aborted || tabByPath(path)) return;
      tabs.push(tab);
      activateTab(path);
      status("");
    } catch (err) {
      if (!signal.aborted) status(err.message, "error");
    }
  }

  // showCompare builds the two sided view for a compare tab. Like the side by
  // side diff it is a MergeView, and like there a tab switch costs the undo
  // history of both sides, never their content.
  async function showCompare(tab) {
    editor.setVisible(true);
    try {
      await editor.setCompare({
        left: { name: baseName(tab.compare.left), doc: tab.compare.leftDoc },
        right: { name: baseName(tab.compare.right), doc: tab.compare.rightDoc },
        collapse: editorSettings.diff_collapse,
        valid: () => activeTab() === tab,
      });
    } catch (err) {
      console.error("compare failed", err);
      notifyError(err.message || "The comparison could not be built.");
      return;
    }
    // The build may have stepped aside for a tab switch, and then the bar
    // belongs to whatever is open now, not to this comparison.
    if (activeTab() !== tab) return;
    syncCompareBar();
  }

  function syncCompareBar() {
    const tab = activeTab();
    const on = !!(tab && tab.compare);
    compareBarEl.hidden = !on;
    if (!on) return;
    const state = tab.compare;
    for (const side of ["left", "right"]) {
      compareNameEls[side].textContent = state[side];
      compareNameEls[side].title = state[side];
    }
    // While the view is still being built there is nothing to read a buffer
    // from, and reading an empty one would call both sides changed.
    if (!editor.comparing()) return;
    state.leftDirty = editor.compareValue("left") !== state.leftSaved;
    state.rightDirty = editor.compareValue("right") !== state.rightSaved;
    compareSaveBtns.left.disabled = !state.leftDirty;
    compareSaveBtns.right.disabled = !state.rightDirty;
    markDirty(tab, state.leftDirty || state.rightDirty);
  }

  async function saveCompareSide(side) {
    const tab = activeTab();
    if (!tab || !tab.compare || !editor.comparing()) return;
    const path = tab.compare[side];
    const content = editor.compareValue(side);
    status("Saving…");
    try {
      await writeFile(path, content);
      tab.compare[`${side}Saved`] = content;
      syncCompareBar();
      status(`Saved ${path}`, "ok");
    } catch (err) {
      status(err.message, "error");
    }
  }

  // saveCompareTab saves whatever a comparison carries unsaved, both sides if
  // both moved. It is what the ordinary save paths (Ctrl+S, Save all) do with a
  // compare tab: a synthetic path is nothing the file route could write.
  async function saveCompareTab(tab) {
    if (tab.path === activePath && editor.comparing()) editor.captureCompare(tab);
    const state = tab.compare;
    for (const side of ["left", "right"]) {
      const content = state[`${side}Doc`];
      if (content === state[`${side}Saved`]) continue;
      await writeFile(state[side], content);
      state[`${side}Saved`] = content;
    }
    if (tab.path === activePath && editor.comparing()) {
      syncCompareBar();
    } else {
      state.leftDirty = state.leftDoc !== state.leftSaved;
      state.rightDirty = state.rightDoc !== state.rightSaved;
      markDirty(tab, state.leftDirty || state.rightDirty);
    }
  }

  // ---- file actions ----------------------------------------------------------

  async function writeFile(path, content) {
    const res = await postForm(`${base}/file`, { path, content });
    await ensureOk(res, "Failed to save file.");
  }

  async function saveTab(tab) {
    if (tab.compare) {
      await saveCompareTab(tab);
      return;
    }
    await writeFile(tab.path, editor.valueOf(tab, tab.path === activePath));
    editor.markSaved(tab, tab.path === activePath);
    markDirty(tab, false);
  }

  async function save() {
    const tab = activeTab();
    if (!tab || !tab.dirty) return;
    status("Saving…");
    saveBtn.disabled = true;
    try {
      await saveTab(tab);
      status(`Saved ${tab.compare ? tab.name : tab.path}`, "ok");
    } catch (err) {
      updateActionStates();
      status(err.message, "error");
    }
  }

  async function saveAll() {
    const dirtyTabs = tabs.filter((t) => t.dirty);
    if (dirtyTabs.length === 0) return;
    status("Saving…");
    try {
      for (const tab of dirtyTabs) {
        await saveTab(tab);
      }
      status(dirtyTabs.length === 1
        ? `Saved ${dirtyTabs[0].compare ? dirtyTabs[0].name : dirtyTabs[0].path}`
        : `Saved ${dirtyTabs.length} files`, "ok");
    } catch (err) {
      status(err.message, "error");
    }
  }

  async function deletePath(targetPath) {
    if (!(await confirmDialog({ title: `Delete "${targetPath}"?`, confirmText: "Delete" }))) return;
    status("Deleting…");
    try {
      const res = await postForm(`${base}/delete`, { path: targetPath });
      await ensureOk(res, "Failed to delete.");
      const data = await res.json();
      const gone = (p) => p === targetPath || p.startsWith(targetPath + "/");
      for (const tab of [...tabs]) {
        // A comparison goes with either of its sides: it can no longer save
        // that one, and the write route would put the deleted file back.
        if (tab.compare ? gone(tab.compare.left) || gone(tab.compare.right) : gone(tab.path)) {
          closeTab(tab.path, true);
        }
      }
      // Nothing may stay picked for a comparison that cannot be built any more.
      if (compareSelection && gone(compareSelection)) compareSelection = null;
      // Drop the deleted dir (and its descendants) from the kept-open set.
      for (const p of [...expanded]) {
        if (p === targetPath || p.startsWith(targetPath + "/")) expanded.delete(p);
      }
      persistExpanded();
      status(`Deleted ${data.entry ? data.entry.path : targetPath}`, "ok");
      await loadTree();
    } catch (err) {
      status(err.message, "error");
    }
  }

  async function renameEntry(entry) {
    const newName = await promptText({
      title: `Rename "${entry.name}"`,
      value: entry.name,
      confirmText: "Rename",
      validatorMessage: "Please enter a name.",
    });
    if (!newName || newName === entry.name) return;
    status("Renaming…");
    try {
      const res = await postForm(`${base}/rename`, { path: entry.path, newName });
      await ensureOk(res, "Failed to rename.");
      const data = await res.json();
      await applyNewPath(entry.path, data.entry.path);
      status(`Renamed to ${data.entry.path}`, "ok");
    } catch (err) {
      status(err.message, "error");
    }
  }

  // A rename and a move both change one path (and every path below it): carry
  // the open tabs, the active file and the unfolded dirs over to the new one.
  async function applyNewPath(oldPath, newPath) {
    const moved = (p) => (p === oldPath ? newPath : p.startsWith(oldPath + "/") ? newPath + p.slice(oldPath.length) : p);
    const wasActive = activePath;
    // The pick for a comparison is a path like any other and moves with it.
    if (compareSelection) compareSelection = moved(compareSelection);
    for (const tab of tabs) {
      // A comparison names two real files, and its own path is built from
      // both. Leaving them on the old name would keep its Save writing there,
      // and the write route creates what is not there any more: the file you
      // renamed away would come back and the renamed one stay untouched.
      if (tab.compare) {
        const left = moved(tab.compare.left);
        const right = moved(tab.compare.right);
        if (left === tab.compare.left && right === tab.compare.right) continue;
        tab.compare.left = left;
        tab.compare.right = right;
        const path = comparePath(left, right);
        if (wasActive === tab.path) activePath = path;
        tab.path = path;
        tab.name = `${baseName(left)} ⇄ ${baseName(right)}`;
        continue;
      }
      tab.path = moved(tab.path);
      tab.name = baseName(tab.path);
    }
    // A comparison that was active has already claimed its new path above.
    if (activePath && activePath === wasActive) activePath = moved(activePath);
    for (const p of [...expanded]) {
      const next = moved(p);
      if (next !== p) {
        expanded.delete(p);
        expanded.add(next);
      }
    }
    persistExpanded();
    const tab = activeTab();
    const shown = tab && tab.compare ? `${tab.compare.left} ⇄ ${tab.compare.right}` : tab ? tab.path : "";
    pathEl.textContent = shown;
    pathEl.title = shown;
    if (tab && !tab.compare && tab.path.startsWith(newPath)) {
      if (tab.kind) renderViewer(tab);
      else editor.refreshLanguage(tab.name);
    }
    renderTabs();
    updateActionStates();
    syncCompareBar();
    syncPreview();
    persistTabs();
    await loadTree();
  }

  function copyToClipboard(path, isDir) {
    clipboard = { path, isDir };
    status(`Copied ${path}`, "ok");
  }

  // extractArchive unpacks an archive into a fresh folder beside it. The name is
  // free by construction, so nothing existing is touched.
  async function extractArchive(path) {
    status("Extracting…");
    try {
      const res = await postForm(`${base}/extract`, { path });
      await ensureOk(res, "Failed to extract.");
      const data = await res.json();
      expandTo(parentDir(path));
      await loadTree();
      status(`Extracted to ${data.entry.path}`, "ok");
    } catch (err) {
      status(err.message, "error");
    }
  }

  // pasteInto copies whatever the tree clipboard holds into dir. The clipboard
  // belongs to this browser, nothing about it is shared or persisted.
  async function pasteInto(dir) {
    if (!clipboard) return;
    const source = clipboard.path;
    status("Copying…");
    try {
      let res = await postForm(`${base}/copy`, { path: source, dir });
      if (res.status === 409) {
        const name = baseName(source);
        const ok = await confirmDialog({
          title: `Replace "${name}"?`,
          text: `${dir || "The project root"} already holds a file called ${name}.`,
          confirmText: "Replace",
        });
        if (!ok) {
          status("");
          return;
        }
        res = await postForm(`${base}/copy`, { path: source, dir, overwrite: "1" });
      }
      await ensureOk(res, "Failed to copy.");
      const data = await res.json();
      if (dir) expandTo(dir);
      await loadTree();
      status(`Copied to ${data.entry.path}`, "ok");
    } catch (err) {
      status(err.message, "error");
    }
  }

  // Drop of a tree row onto a folder row (or the empty tree area for the root).
  async function moveEntry(path, dir) {
    if (!path || parentDir(path) === dir) return;
    status("Moving…");
    try {
      let res = await postForm(`${base}/move`, { path, dir });
      // 409 means the target folder already holds that name. Offer to replace
      // it rather than ending the drag with an error.
      if (res.status === 409) {
        const name = baseName(path);
        const ok = await confirmDialog({
          title: `Replace "${name}"?`,
          text: `${dir || "The project root"} already holds a file called ${name}.`,
          confirmText: "Replace",
        });
        if (!ok) {
          status("");
          return;
        }
        res = await postForm(`${base}/move`, { path, dir, overwrite: "1" });
      }
      await ensureOk(res, "Failed to move.");
      const data = await res.json();
      if (dir) expandTo(dir);
      await applyNewPath(path, data.entry.path);
      status(`Moved to ${data.entry.path}`, "ok");
    } catch (err) {
      status(err.message, "error");
    }
  }

  async function createFile() {
    const dir = targetDir();
    const fileName = await promptName("file", dir);
    if (!fileName) return;
    const path = dir ? `${dir}/${fileName}` : fileName;
    status("Creating…");
    try {
      const res = await postForm(`${base}/create`, { path });
      await ensureOk(res, "Failed to create file.");
      const data = await res.json();
      await loadTree();
      if (data.entry) await openPath(data.entry.path, { keepDrawer: true });
      status(`Created ${data.entry ? data.entry.path : path}`, "ok");
    } catch (err) {
      status(err.message, "error");
    }
  }

  async function createFolder() {
    const dir = targetDir();
    const folderName = await promptName("folder", dir);
    if (!folderName) return;
    const path = dir ? `${dir}/${folderName}` : folderName;
    status("Creating…");
    try {
      const res = await postForm(`${base}/mkdir`, { path });
      await ensureOk(res, "Failed to create folder.");
      const data = await res.json();
      const created = data.entry ? data.entry.path : path;
      await loadTree();
      // And it stays the selected row, so the file that usually follows lands
      // in it without hunting for it again. The row may not be there yet, an
      // empty folder only arrives with the listing, so the selection is state
      // first and a highlight when the row shows up.
      setSelected(created, true, treeEl.querySelector(`.editor-item[data-path="${CSS.escape(created)}"]`));
      status(`Created ${created}`, "ok");
    } catch (err) {
      status(err.message, "error");
    }
  }

  async function copyPath(path) {
    try {
      await navigator.clipboard.writeText(path);
      status(`Copied ${path}`, "ok");
    } catch {
      status("Clipboard is not available.", "error");
    }
  }

  async function revealInTree(path) {
    if (mobileMedia.matches) openDrawer();
    expandTo(parentDir(path));
    await loadTree();
    for (let i = 0; i < 40 && !signal.aborted; i++) {
      const row = treeEl.querySelector(`.editor-file[data-path="${CSS.escape(path)}"]`);
      if (row) {
        setSelected(path, false, row);
        row.scrollIntoView({ block: "nearest" });
        return;
      }
      await new Promise((resolve) => setTimeout(resolve, 50));
    }
  }

  // A folder is packed into a tar.gz on the fly, a file goes as it is.
  function startDownload(path, asArchive) {
    const a = document.createElement("a");
    a.href = asArchive ? `${base}/archive?path=${encodeURIComponent(path)}` : rawUrl(path, true);
    a.setAttribute("download", asArchive ? `${baseName(path)}.tar.gz` : baseName(path));
    a.setAttribute("data-no-pe", "");
    document.body.appendChild(a);
    a.click();
    a.remove();
  }

  // ---- upload ----------------------------------------------------------------

  // The dialog mirrors the new file/folder prompts: it names the target folder
  // before anything is sent, so a stray tree selection cannot route files to
  // the wrong place unnoticed. Button uploads wait for a confirm; drops name
  // their target implicitly, so they start right away and only show progress.
  // uploadFiles takes plain File objects or {file, rel} pairs, where rel is the
  // path inside a dropped folder. The folders themselves are made by the server
  // while the files land.
  async function uploadFiles(fileList, dir, { confirmFirst = true } = {}) {
    const items = Array.from(fileList || []).map((item) => (item instanceof File ? { file: item, rel: "" } : item));
    const files = items.map((item) => item.file);
    const nested = items.some((item) => item.rel);
    if (files.length === 0) return;
    if (!dialogAvailable()) {
      await uploadPlain(files, dir);
      return;
    }
    const where = dir ? `${dir}/` : "project root";
    // Names already in the target folder are named up front, so the upload asks
    // once instead of failing per file. A drop that collides opens the dialog
    // even though it would otherwise upload straight away.
    const taken = new Set((await listDir(dir).catch(() => [])).map((entry) => entry.name));
    const clashes = nested ? [] : files.filter((file) => taken.has(file.name));
    const content = document.createElement("div");
    const target = document.createElement("div");
    target.className = "text-secondary small mb-3";
    target.innerHTML = `in <code>${escapeHtml(where)}</code>`;
    const list = document.createElement("div");
    list.className = "editor-upload-list";
    items.forEach((item, index) => list.appendChild(uploadItem(item.file, index, item.rel)));
    content.append(target, list);
    if (clashes.length) {
      const warn = document.createElement("div");
      warn.className = "text-warning small mt-3 text-break";
      warn.dataset.uploadClash = String(clashes.length);
      warn.textContent = clashes.length === 1
        ? `${clashes[0].name} is already there and will be replaced.`
        : `${clashes.length} files are already there and will be replaced: ${clashes.map((f) => f.name).join(", ")}`;
      content.appendChild(warn);
    }
    let uploaded = 0;
    const result = await fireDialog({
      title: files.length === 1 ? "Upload 1 file" : `Upload ${files.length} files`,
      html: content,
      showCancelButton: true,
      confirmButtonText: clashes.length ? "Replace" : "Upload",
      cancelButtonText: "Cancel",
      reverseButtons: true,
      showLoaderOnConfirm: true,
      allowOutsideClick: () => !window.Swal.isLoading(),
      didOpen: () => {
        if (!confirmFirst && !clashes.length) window.Swal.clickConfirm();
      },
      preConfirm: async () => {
        const results = await runUploads(items, dir, list, clashes.length > 0);
        uploaded += results.filter((r) => r.status === "fulfilled" && r.value).length;
        const failed = results.filter((r) => r.status === "rejected");
        if (failed.length > 0) {
          window.Swal.showValidationMessage(failed[0].reason.message);
          return false;
        }
        return true;
      },
    });
    if (uploaded > 0) {
      expandTo(dir);
      // A folder upload lands in a new folder, open it so the result is visible.
      const top = items.find((item) => item.rel)?.rel.split("/")[0];
      if (top) expandTo(dir ? `${dir}/${top}` : top);
      await loadTree();
    }
    if (result.isConfirmed) {
      status(files.length === 1 ? `Uploaded ${dir ? `${dir}/` : ""}${files[0].name}` : `Uploaded ${files.length} files`, "ok");
    }
  }

  async function uploadPlain(files, dir) {
    status("Uploading…");
    const form = new FormData();
    form.append("dir", dir);
    for (const file of files) form.append("files", file);
    try {
      const res = await fetch(`${base}/upload`, {
        method: "POST",
        headers: csrfHeaders({ Accept: "application/json" }),
        body: form,
      });
      await ensureOk(res, "Failed to upload.");
      const data = await res.json();
      const count = (data.entries || []).length;
      expandTo(dir);
      await loadTree();
      status(count === 1 ? `Uploaded ${data.entries[0].path}` : `Uploaded ${count} files`, "ok");
    } catch (err) {
      status(err.message, "error");
    }
  }

  // Already uploaded rows keep data-done, so a retry after a partial failure
  // only sends the files that are still missing.
  function runUploads(items, dir, list, overwrite = false) {
    const jobs = items.map(({ file, rel }, index) => {
      const item = list.querySelector(`[data-file-index="${index}"]`);
      if (item.dataset.done === "true") return Promise.resolve(null);
      delete item.dataset.error;
      const bar = item.querySelector(".progress-bar");
      const outer = item.querySelector(".progress");
      const statusEl = item.querySelector("[data-file-status]");
      statusEl.textContent = "Uploading";
      const into = rel ? (dir ? `${dir}/${rel}` : rel) : dir;
      return uploadOne(file, into, overwrite, !!rel, (e) => {
        if (!e.lengthComputable) return;
        const percent = Math.round((e.loaded / e.total) * 100);
        bar.style.width = `${percent}%`;
        outer.setAttribute("aria-valuenow", String(percent));
        statusEl.textContent = `${percent}%`;
      }).then(
        (entry) => {
          bar.style.width = "100%";
          outer.setAttribute("aria-valuenow", "100");
          statusEl.textContent = "Done";
          item.dataset.done = "true";
          return entry;
        },
        (err) => {
          statusEl.textContent = "Failed";
          item.dataset.error = "true";
          throw err;
        },
      );
    });
    return Promise.allSettled(jobs);
  }

  function uploadOne(file, dir, overwrite, createDirs, onProgress) {
    return new Promise((resolve, reject) => {
      const xhr = new XMLHttpRequest();
      xhr.open("POST", `${base}/upload`);
      const headers = csrfHeaders({ Accept: "application/json" });
      for (const [key, value] of Object.entries(headers)) xhr.setRequestHeader(key, value);
      xhr.upload.addEventListener("progress", onProgress);
      xhr.addEventListener("load", () => {
        let data = null;
        try {
          data = JSON.parse(xhr.responseText);
        } catch {
          data = null;
        }
        if (xhr.status >= 200 && xhr.status < 300 && data && data.entries) {
          resolve(data.entries[0]);
        } else {
          reject(new Error((data && data.error) || xhr.statusText || `HTTP ${xhr.status}`));
        }
      });
      xhr.addEventListener("error", () => reject(new Error("Failed to upload.")));
      xhr.addEventListener("abort", () => reject(new Error("Upload canceled.")));
      const form = new FormData();
      form.append("dir", dir);
      if (overwrite) form.append("overwrite", "1");
      if (createDirs) form.append("dirs", "1");
      form.append("files", file, file.name);
      xhr.send(form);
    });
  }

  function uploadItem(file, index, rel) {
    const item = document.createElement("div");
    item.className = "editor-upload-item";
    item.dataset.fileIndex = String(index);
    const header = document.createElement("div");
    header.className = "d-flex justify-content-between gap-2 small mb-1";
    const nameEl = document.createElement("div");
    nameEl.className = "text-truncate";
    nameEl.textContent = rel ? `${rel}/${file.name}` : file.name;
    const statusEl = document.createElement("div");
    statusEl.className = "text-secondary text-nowrap";
    statusEl.dataset.fileStatus = "";
    statusEl.textContent = formatSize(file.size);
    const outer = document.createElement("div");
    outer.className = "progress";
    outer.setAttribute("role", "progressbar");
    outer.setAttribute("aria-valuemin", "0");
    outer.setAttribute("aria-valuemax", "100");
    outer.setAttribute("aria-valuenow", "0");
    const bar = document.createElement("div");
    bar.className = "progress-bar";
    bar.style.width = "0%";
    header.append(nameEl, statusEl);
    outer.append(bar);
    item.append(header, outer);
    return item;
  }

  function expandTo(dir) {
    if (!dir) return;
    let path = "";
    for (const part of dir.split("/")) {
      path = path ? `${path}/${part}` : part;
      expanded.add(path);
    }
    persistExpanded();
  }

  function dropDirFor(target) {
    const row = target.closest(".editor-item");
    if (!row || !row.dataset.path) return "";
    return row.dataset.dir ? row.dataset.path : parentDir(row.dataset.path);
  }

  // The row that owns the drop target directory, so the highlight sits where the
  // files actually land: the folder row itself, or the tree box for the root.
  function dropRowFor(target) {
    const dir = dropDirFor(target);
    if (!dir) return treeEl;
    return treeEl.querySelector(`.editor-dir[data-path="${CSS.escape(dir)}"]`) || treeEl;
  }

  // A folder cannot land in itself or in one of its own children.
  function dropAllowed(dir) {
    if (!dragging) return true;
    if (parentDir(dragging.path) === dir) return false;
    return !(dragging.isDir && (dir === dragging.path || dir.startsWith(dragging.path + "/")));
  }

  const dirOpeners = new WeakMap(); // folder row -> its open/close function
  let dragging = null;
  let clipboard = null; // { path, isDir } for copy and paste, per browser
  let dropHighlight = null;
  function setDropHighlight(el) {
    if (dropHighlight === el) return;
    dropHighlight?.classList.remove("editor-drop");
    dropHighlight = el;
    dropHighlight?.classList.add("editor-drop");
  }

  // While a drag hovers the top or bottom edge of the tree it scrolls, so a
  // folder far up or down the list stays reachable without letting go.
  const DRAG_EDGE = 32;
  const DRAG_STEP = 12;
  let edgeScroll = null;
  function stopEdgeScroll() {
    if (!edgeScroll) return;
    cancelAnimationFrame(edgeScroll.raf);
    edgeScroll = null;
  }
  function updateEdgeScroll(clientY) {
    const box = treeEl.getBoundingClientRect();
    const dir = clientY < box.top + DRAG_EDGE ? -1 : clientY > box.bottom - DRAG_EDGE ? 1 : 0;
    if (!dir) {
      stopEdgeScroll();
      return;
    }
    if (edgeScroll?.dir === dir) return;
    stopEdgeScroll();
    const step = () => {
      treeEl.scrollTop += dir * DRAG_STEP;
      if (edgeScroll) edgeScroll.raf = requestAnimationFrame(step);
    };
    edgeScroll = { dir, raf: requestAnimationFrame(step) };
  }

  // Resting on a closed folder while dragging opens it, the way a file manager
  // does, so a drop deep in the tree needs no detour to unfold it first.
  const HOVER_OPEN_MS = 600;
  let hoverOpen = null;
  function cancelHoverOpen() {
    if (!hoverOpen) return;
    clearTimeout(hoverOpen.timer);
    hoverOpen = null;
  }
  function scheduleHoverOpen(row) {
    if (hoverOpen && hoverOpen.row === row) return;
    cancelHoverOpen();
    if (!row || !row.dataset.dir) return;
    const children = row.nextElementSibling;
    if (!children || !children.classList.contains("editor-children") || !children.hidden) return;
    hoverOpen = {
      row,
      timer: setTimeout(() => {
        hoverOpen = null;
        dirOpeners.get(row)?.(true);
      }, HOVER_OPEN_MS),
    };
  }

  // The target folder can sit outside the scrolled view, so its name also shows
  // in a pill pinned to the tree while the drag runs. Pass null to hide it.
  function setDropHint(dir) {
    if (!dropHintEl) return;
    if (dir === null) {
      dropHintEl.hidden = true;
      return;
    }
    dropHintEl.hidden = false;
    dropHintEl.textContent = `Target → ${dir || "project root"}`;
  }

  function endDrag() {
    dragging = null;
    stopEdgeScroll();
    cancelHoverOpen();
    setDropHighlight(null);
    setDropHint(null);
  }

  // A dropped folder arrives as a directory entry, not as files. webkitGetAsEntry
  // walks it in every current browser; the File System Access API would only
  // cover Chromium. More than MAX_DROP_FILES entries is refused, an archive is
  // the better route for a tree that size (the tree menu unpacks it again).
  const MAX_DROP_FILES = 1000;

  function readEntries(reader) {
    return new Promise((resolve, reject) => reader.readEntries(resolve, reject));
  }

  async function walkEntry(entry, prefix, out) {
    if (out.length > MAX_DROP_FILES) return;
    if (entry.isFile) {
      const file = await new Promise((resolve, reject) => entry.file(resolve, reject));
      out.push({ file, rel: prefix });
      return;
    }
    const reader = entry.createReader();
    const here = prefix ? `${prefix}/${entry.name}` : entry.name;
    for (;;) {
      const batch = await readEntries(reader);
      if (!batch.length) return;
      for (const child of batch) await walkEntry(child, here, out);
    }
  }

  // collectDrop returns {file, rel} pairs for everything in the drop, or null
  // when the browser hands over no directory entries (plain files then).
  async function collectDrop(dataTransfer) {
    const entries = [...(dataTransfer.items || [])]
      .map((item) => (item.kind === "file" && item.webkitGetAsEntry ? item.webkitGetAsEntry() : null))
      .filter(Boolean);
    if (!entries.some((entry) => entry.isDirectory)) return null;
    const out = [];
    for (const entry of entries) await walkEntry(entry, "", out);
    if (out.length > MAX_DROP_FILES) {
      throw new Error(`That folder holds more than ${MAX_DROP_FILES} files. Upload it as an archive and extract it here.`);
    }
    return out;
  }

  function wireTreeDrop() {
    treeEl.addEventListener("dragover", (e) => {
      const files = e.dataTransfer?.types?.includes("Files");
      if (!files && !dragging) return;
      const dir = dropDirFor(e.target);
      if (!files && !dropAllowed(dir)) {
        e.dataTransfer.dropEffect = "none";
        cancelHoverOpen();
        setDropHighlight(null);
        setDropHint(null);
        return;
      }
      e.preventDefault();
      e.dataTransfer.dropEffect = files ? "copy" : "move";
      setDropHighlight(dropRowFor(e.target));
      setDropHint(dir);
      updateEdgeScroll(e.clientY);
      scheduleHoverOpen(e.target.closest(".editor-dir"));
    }, { signal });
    treeEl.addEventListener("dragleave", (e) => {
      if (!treeEl.contains(e.relatedTarget)) {
        stopEdgeScroll();
        cancelHoverOpen();
        setDropHighlight(null);
        setDropHint(null);
      }
    }, { signal });
    treeEl.addEventListener("drop", (e) => {
      const dir = dropDirFor(e.target);
      // Same signal as dragover: only an outside drop carries files, a tree row
      // drag carries its own path.
      if (e.dataTransfer?.types?.includes("Files")) {
        e.preventDefault();
        endDrag();
        const dropped = e.dataTransfer;
        // The entries have to be read before the event handler returns, the
        // items list is emptied afterwards.
        const items = [...(dropped.items || [])];
        const files = [...(dropped.files || [])];
        void (async () => {
          try {
            const walked = await collectDrop({ items });
            await uploadFiles(walked || files, dir, { confirmFirst: !!walked });
          } catch (err) {
            status(err.message, "error");
          }
        })();
        return;
      }
      if (!dragging || !dropAllowed(dir)) {
        endDrag();
        return;
      }
      e.preventDefault();
      const path = dragging.path;
      endDrag();
      void moveEntry(path, dir);
    }, { signal });
  }

  // ---- markdown preview ------------------------------------------------------

  // The preview is a per file switch like the diff and the blame gutter: it
  // says how you want to read this one file, so it rides on its tab and is
  // reached from the file's own context menu.
  function previewVisible() {
    const tab = activeTab();
    return !!(tab && !tab.kind && !tab.compare && tab.previewOn && hasPreview(tab.name));
  }

  function previewMenuItem(tab) {
    if (!tab || tab.kind || tab.compare || !hasPreview(tab.name)) return null;
    return {
      label: tab.previewOn ? "Hide preview" : "Show preview",
      icon: tab.previewOn ? "ti-eye-off" : "ti-eye",
      action: () => togglePreviewFor(tab),
    };
  }

  function togglePreviewFor(tab) {
    tab.previewOn = !tab.previewOn;
    // The surface belongs to one of them: a preview takes it back from a diff.
    if (tab.previewOn && tab.diffRev) {
      if (tab.path === activePath) void applyDiff("");
      else tab.diffRev = "";
    }
    persistTabs();
    if (tab.path === activePath) syncPreview();
  }

  async function previewFromTree(path) {
    let tab = tabByPath(path);
    if (!tab) {
      await openPath(path);
      tab = tabByPath(path);
    }
    if (!tab || tab.kind || tab.compare) return;
    togglePreviewFor(tab);
  }

  function syncPreview() {
    const show = previewVisible();
    previewPaneEl.hidden = !show;
    surfaceEl.classList.toggle("editor-preview-split", show);
    editor.measure();
    if (show) schedulePreview(0);
    else clearTimeout(previewTimer);
  }

  function schedulePreview(delay) {
    if (!previewVisible()) return;
    clearTimeout(previewTimer);
    previewTimer = setTimeout(renderPreview, delay);
  }

  async function renderPreview() {
    const tab = activeTab();
    if (!previewVisible() || !tab) return;
    if (isSvg(tab.name)) {
      renderSvgPreview(tab);
      return;
    }
    previewPaneEl.classList.remove("editor-preview-image");
    try {
      const res = await postForm(`${base}/preview`, { content: editor.valueOf(tab, true) });
      await ensureOk(res, "Failed to render the preview.");
      const data = await res.json();
      if (previewVisible()) previewPaneEl.innerHTML = data.html || "";
    } catch (err) {
      status(err.message, "error");
    }
  }

  // The SVG preview renders the current buffer through an <img> with a blob
  // URL: it tracks unsaved edits and scripts inside the SVG never run.
  function renderSvgPreview(tab) {
    const img = document.createElement("img");
    if (svgPreviewUrl) URL.revokeObjectURL(svgPreviewUrl);
    svgPreviewUrl = URL.createObjectURL(new Blob([editor.valueOf(tab, true)], { type: "image/svg+xml" }));
    img.src = svgPreviewUrl;
    img.alt = tab.name;
    previewPaneEl.classList.add("editor-preview-image");
    previewPaneEl.replaceChildren(img);
  }

  // ---- drawer ------------------------------------------------------------------

  function openDrawer() {
    root.classList.add("editor-drawer-open");
    backdropEl.hidden = false;
  }

  function closeDrawer() {
    root.classList.remove("editor-drawer-open");
    backdropEl.hidden = true;
  }

  // The same button on both widths, with the effect the width allows: where the
  // tree is a drawer it opens and closes it, where the tree is a column it folds
  // that column away and back. The fold is per device and comes back with the
  // page; the width the splitter set is untouched, so an unfolded column stands
  // where it stood.
  let treeFolded = store.get(TREE_FOLD_KEY, "") === "1";
  function paintTreeFold() {
    root.classList.toggle("editor-tree-folded", treeFolded);
    const drawer = mobileMedia.matches;
    drawerToggleBtn.setAttribute("aria-pressed", !drawer && !treeFolded ? "true" : "false");
    drawerToggleBtn.title = drawer ? "Files" : treeFolded ? "Show the file tree" : "Hide the file tree";
    editor.measure();
  }

  function toggleDrawer() {
    if (mobileMedia.matches) {
      if (root.classList.contains("editor-drawer-open")) closeDrawer();
      else openDrawer();
      return;
    }
    treeFolded = !treeFolded;
    store.set(TREE_FOLD_KEY, treeFolded ? "1" : "");
    paintTreeFold();
  }

  // ---- splitter ----------------------------------------------------------------

  function applyTreeWidth(px) {
    if (px > 0) bodyEl.style.setProperty("--editor-tree-width", `${px}px`);
    else bodyEl.style.removeProperty("--editor-tree-width");
  }

  function wireSplitter() {
    applyTreeWidth(parseInt(store.get(TREE_WIDTH_KEY, "0"), 10) || 0);
    let dragging = false;
    splitterEl.addEventListener("pointerdown", (e) => {
      dragging = true;
      splitterEl.classList.add("active");
      splitterEl.setPointerCapture(e.pointerId);
    }, { signal });
    splitterEl.addEventListener("pointermove", (e) => {
      if (!dragging) return;
      const rect = bodyEl.getBoundingClientRect();
      const px = Math.round(Math.min(Math.max(e.clientX - rect.left, 160), rect.width * 0.65));
      applyTreeWidth(px);
    }, { signal });
    splitterEl.addEventListener("pointerup", (e) => {
      dragging = false;
      splitterEl.classList.remove("active");
      splitterEl.releasePointerCapture(e.pointerId);
      store.set(TREE_WIDTH_KEY, String(Math.round(treeColEl.getBoundingClientRect().width)));
      editor.measure();
    }, { signal });
    splitterEl.addEventListener("dblclick", () => {
      applyTreeWidth(0);
      store.set(TREE_WIDTH_KEY, "0");
      editor.measure();
    }, { signal });
  }

  // ---- quick open --------------------------------------------------------------

  // The palette has two modes: "files" filters the file list client side,
  // "search" greps file contents server side and jumps to the matched line.
  let quickOpenMode = "files";
  let quickOpenFiles = null;
  let quickOpenMatches = [];
  let quickOpenActive = 0;
  let searchQuery = "";
  let searchSeq = 0;
  let searchTimer = 0;

  async function openQuickOpen(mode = "files") {
    closeDrawer();
    closeSheet();
    quickOpenMode = mode;
    quickOpenEl.hidden = false;
    quickOpenInput.value = "";
    quickOpenInput.placeholder = mode === "search" ? "Find in files…" : "Go to file…";
    quickOpenMatches = [];
    quickOpenInput.focus();
    if (mode === "search") {
      quickOpenList.innerHTML = `<div class="editor-quickopen-empty text-secondary small">Type at least 2 characters to search file contents.</div>`;
      return;
    }
    quickOpenList.innerHTML = `<div class="editor-quickopen-empty text-secondary small">Loading…</div>`;
    try {
      const data = await getJSON(`${base}/files`, { signal });
      quickOpenFiles = { files: data.files || [], truncated: !!data.truncated };
      renderQuickOpen();
    } catch (err) {
      quickOpenFiles = null;
      quickOpenList.innerHTML = `<div class="editor-quickopen-empty text-danger small">${escapeHtml(err.message)}</div>`;
    }
  }

  function closeQuickOpen() {
    quickOpenEl.hidden = true;
  }

  function filterFiles(files, query) {
    const tokens = query.toLowerCase().split(/\s+/).filter(Boolean);
    if (tokens.length === 0) return files;
    const scored = [];
    for (const path of files) {
      const lower = path.toLowerCase();
      if (!tokens.every((t) => lower.includes(t))) continue;
      const fileName = lower.slice(lower.lastIndexOf("/") + 1);
      const score = fileName.startsWith(tokens[0]) ? 0 : fileName.includes(tokens[0]) ? 1 : 2;
      scored.push([score, path.length, path]);
    }
    scored.sort((a, b) => a[0] - b[0] || a[1] - b[1] || (a[2] < b[2] ? -1 : 1));
    return scored.map((s) => s[2]);
  }

  function renderQuickOpen() {
    if (quickOpenEl.hidden || !quickOpenFiles) return;
    quickOpenMatches = filterFiles(quickOpenFiles.files, quickOpenInput.value).slice(0, QUICK_OPEN_LIMIT);
    quickOpenActive = 0;
    quickOpenList.innerHTML = "";
    if (quickOpenMatches.length === 0) {
      quickOpenList.innerHTML = `<div class="editor-quickopen-empty text-secondary small">No matching files.</div>`;
      return;
    }
    quickOpenMatches.forEach((path, i) => {
      const item = document.createElement("div");
      item.className = "editor-quickopen-item";
      item.classList.toggle("active", i === quickOpenActive);
      item.setAttribute("role", "option");
      item.dataset.path = path;
      item.innerHTML = `<i class="ti ti-file"></i><span class="editor-quickopen-name">${escapeHtml(baseName(path))}</span><span class="editor-quickopen-dir">${escapeHtml(parentDir(path))}</span>`;
      item.addEventListener("click", () => chooseQuickOpen(path));
      quickOpenList.appendChild(item);
    });
    if (quickOpenFiles.truncated) {
      const note = document.createElement("div");
      note.className = "editor-quickopen-empty text-secondary small";
      note.textContent = "File list is truncated, narrow the search.";
      quickOpenList.appendChild(note);
    }
  }

  function scheduleSearch() {
    clearTimeout(searchTimer);
    const q = quickOpenInput.value.trim();
    if (q.length < 2) {
      searchSeq++;
      quickOpenMatches = [];
      quickOpenList.innerHTML = `<div class="editor-quickopen-empty text-secondary small">Type at least 2 characters to search file contents.</div>`;
      return;
    }
    searchTimer = setTimeout(() => runSearch(q), 250);
  }

  async function runSearch(q) {
    const seq = ++searchSeq;
    quickOpenList.innerHTML = `<div class="editor-quickopen-empty text-secondary small">Searching…</div>`;
    try {
      const data = await getJSON(`${base}/search?q=${encodeURIComponent(q)}`, { signal });
      if (seq !== searchSeq || quickOpenEl.hidden || quickOpenMode !== "search") return;
      searchQuery = q;
      renderSearchResults(data.matches || [], !!data.truncated);
    } catch (err) {
      if (seq !== searchSeq) return;
      quickOpenList.innerHTML = `<div class="editor-quickopen-empty text-danger small">${escapeHtml(err.message)}</div>`;
    }
  }

  function renderSearchResults(matches, truncated) {
    quickOpenMatches = matches;
    quickOpenActive = 0;
    quickOpenList.innerHTML = "";
    if (matches.length === 0) {
      quickOpenList.innerHTML = `<div class="editor-quickopen-empty text-secondary small">No matches.</div>`;
      return;
    }
    matches.forEach((match, i) => {
      const item = document.createElement("div");
      item.className = "editor-quickopen-item editor-quickopen-match";
      item.classList.toggle("active", i === quickOpenActive);
      item.setAttribute("role", "option");
      const head = document.createElement("div");
      head.className = "editor-quickopen-match-head";
      head.innerHTML = `<i class="ti ti-file"></i><span class="editor-quickopen-name">${escapeHtml(baseName(match.path))}:${match.line}</span><span class="editor-quickopen-dir">${escapeHtml(parentDir(match.path))}</span>`;
      const text = document.createElement("div");
      text.className = "editor-quickopen-match-text";
      text.append(...markedFragments(match.text, searchQuery));
      item.append(head, text);
      item.addEventListener("click", () => chooseQuickOpen(match));
      quickOpenList.appendChild(item);
    });
    if (truncated) {
      const note = document.createElement("div");
      note.className = "editor-quickopen-empty text-secondary small";
      note.textContent = "Results are truncated, narrow the search.";
      quickOpenList.appendChild(note);
    }
  }

  function markedFragments(text, q) {
    const lower = text.toLowerCase();
    const needle = q.toLowerCase();
    const out = [];
    let i = 0;
    while (i <= text.length) {
      const idx = lower.indexOf(needle, i);
      if (idx < 0) {
        out.push(document.createTextNode(text.slice(i)));
        break;
      }
      if (idx > i) out.push(document.createTextNode(text.slice(i, idx)));
      const mark = document.createElement("mark");
      mark.textContent = text.slice(idx, idx + needle.length);
      out.push(mark);
      i = idx + needle.length;
    }
    return out;
  }

  function moveQuickOpenActive(delta) {
    if (quickOpenMatches.length === 0) return;
    quickOpenActive = (quickOpenActive + delta + quickOpenMatches.length) % quickOpenMatches.length;
    quickOpenList.querySelectorAll(".editor-quickopen-item").forEach((el, i) => {
      el.classList.toggle("active", i === quickOpenActive);
      if (i === quickOpenActive) el.scrollIntoView({ block: "nearest" });
    });
  }

  async function chooseQuickOpen(entry) {
    closeQuickOpen();
    if (typeof entry === "string") {
      openPath(entry);
      return;
    }
    await openPath(entry.path);
    const tab = activeTab();
    if (tab && tab.path === entry.path && !tab.kind) editor.jumpTo(entry.line);
  }

  function wireQuickOpen() {
    quickOpenItem.addEventListener("click", () => openQuickOpen("files"), { signal });
    searchProjectItem.addEventListener("click", () => openQuickOpen("search"), { signal });
    quickOpenInput.addEventListener("input", () => {
      if (quickOpenMode === "search") scheduleSearch();
      else renderQuickOpen();
    }, { signal });
    quickOpenInput.addEventListener("keydown", (e) => {
      if (e.key === "ArrowDown") {
        e.preventDefault();
        moveQuickOpenActive(1);
      } else if (e.key === "ArrowUp") {
        e.preventDefault();
        moveQuickOpenActive(-1);
      } else if (e.key === "Enter") {
        e.preventDefault();
        if (quickOpenMatches[quickOpenActive]) chooseQuickOpen(quickOpenMatches[quickOpenActive]);
      } else if (e.key === "Escape") {
        e.preventDefault();
        closeQuickOpen();
      }
    }, { signal });
    quickOpenEl.addEventListener("click", (e) => {
      if (e.target === quickOpenEl) closeQuickOpen();
    }, { signal });
  }

  // ---- wiring ----------------------------------------------------------------

  saveBtn.addEventListener("click", save, { signal });
  saveAllItem.addEventListener("click", saveAll, { signal });
  refreshBtn.addEventListener("click", () => {
    void loadTree();
    void loadGitStatus();
    void refreshDiffHead();
  }, { signal });
  // The poller signals movement, never the state itself: this page pulls the
  // status like every other surface pulls its own fragment. An open diff
  // follows only when HEAD moved, see refreshDiffHead: saving a file moves the
  // working copy, which is what the status describes, so a save never costs a
  // git show.
  onServerEvent("git", (event) => {
    if (!event.detail || event.detail.project !== name) return;
    void loadGitStatus();
    if (event.detail.base) void refreshDiffHead();
  }, { signal });
  // Nothing was published while this page was away, see catchUpGit.
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "visible") catchUpGit();
  }, { signal });
  for (const el of root.querySelectorAll("[data-editor-compare-save]")) {
    el.addEventListener("click", () => void saveCompareSide(el.dataset.editorCompareSave), { signal });
  }
  // The folder picker hands over every file with its path inside the folder.
  uploadDirInput?.addEventListener("change", () => {
    const items = Array.from(uploadDirInput.files || []).map((file) => ({
      file,
      rel: (file.webkitRelativePath || "").split("/").slice(0, -1).join("/"),
    }));
    uploadFiles(items, targetDir());
    uploadDirInput.value = "";
  }, { signal });

  uploadInput.addEventListener("change", () => {
    uploadFiles(uploadInput.files, targetDir());
    uploadInput.value = "";
  }, { signal });
  findItem.addEventListener("click", () => {
    if (!editor.search()) status("Search requires the CodeMirror editor.", "error");
  }, { signal });
  gotoItem.addEventListener("click", () => {
    if (!editor.gotoLine()) status("Go to line requires the CodeMirror editor.", "error");
  }, { signal });
  drawerToggleBtn.addEventListener("click", toggleDrawer, { signal });
  browseBtn.addEventListener("click", openDrawer, { signal });
  backdropEl.addEventListener("click", closeDrawer, { signal });
  tabsEl.addEventListener("wheel", (e) => {
    if (!e.deltaX && e.deltaY) {
      tabsEl.scrollLeft += e.deltaY;
      e.preventDefault();
    }
  }, { passive: false, signal });

  // Mouse drag reorders the tab strip like the terminal tabs: threshold, live
  // transform preview, edge auto scroll, then the tabs array is respliced and
  // persisted. Touch stays out, there the long press menu and the native
  // horizontal scroll own the gestures; the order is per device state anyway.
  function wireTabDrag() {
    let drag = null;
    let suppressed = false;
    const contentX = (clientX) => clientX - tabsEl.getBoundingClientRect().left + tabsEl.scrollLeft;
    const updateDrag = () => {
      if (!drag || !drag.active) return;
      const dx = contentX(drag.lastClientX) - drag.startContentX;
      const draggedCenter = drag.centers[drag.fromIndex] + dx;
      let toIndex = 0;
      for (let i = 0; i < drag.centers.length; i += 1) {
        if (i !== drag.fromIndex && drag.centers[i] < draggedCenter) toIndex += 1;
      }
      drag.toIndex = toIndex;
      drag.el.style.transform = `translateX(${dx}px)`;
      drag.els.forEach((el, i) => {
        if (el === drag.el) return;
        let shift = 0;
        if (i > drag.fromIndex && i <= drag.toIndex) shift = -drag.width;
        else if (i < drag.fromIndex && i >= drag.toIndex) shift = drag.width;
        el.style.transform = shift ? `translateX(${shift}px)` : "";
      });
    };
    const tickEdgeScroll = () => {
      if (!drag || !drag.active) return;
      const rect = tabsEl.getBoundingClientRect();
      let delta = 0;
      if (drag.lastClientX < rect.left + 32) delta = -12;
      else if (drag.lastClientX > rect.right - 32) delta = 12;
      if (delta) {
        const max = tabsEl.scrollWidth - tabsEl.clientWidth;
        const next = Math.max(0, Math.min(tabsEl.scrollLeft + delta, max));
        if (next !== tabsEl.scrollLeft) {
          tabsEl.scrollLeft = next;
          updateDrag();
        }
      }
      drag.raf = window.requestAnimationFrame(tickEdgeScroll);
    };
    const clearDrag = () => {
      if (!drag) return;
      if (drag.active) {
        window.cancelAnimationFrame(drag.raf);
        tabsEl.classList.remove("editor-tabs-dragging");
        drag.el.classList.remove("editor-tab-dragging");
        for (const el of drag.els) el.style.transform = "";
      }
      drag = null;
    };
    tabsEl.addEventListener("pointerdown", (e) => {
      if (e.button !== 0 || e.pointerType === "touch" || drag) return;
      if (e.target.closest(".editor-tab-state")) return;
      const el = e.target.closest(".editor-tab");
      if (!el) return;
      suppressed = false;
      drag = {
        el,
        pointerId: e.pointerId,
        startClientX: e.clientX,
        startClientY: e.clientY,
        lastClientX: e.clientX,
        active: false,
        raf: 0,
      };
      try {
        el.setPointerCapture(e.pointerId);
      } catch (error) {
        void error;
      }
    }, { signal });
    tabsEl.addEventListener("pointermove", (e) => {
      if (!drag || e.pointerId !== drag.pointerId) return;
      if (!drag.active) {
        if (!(e.buttons & 1)) {
          drag = null;
          return;
        }
        if (Math.hypot(e.clientX - drag.startClientX, e.clientY - drag.startClientY) < 6) return;
        drag.active = true;
        drag.els = Array.from(tabsEl.querySelectorAll(".editor-tab"));
        drag.fromIndex = drag.els.indexOf(drag.el);
        drag.toIndex = drag.fromIndex;
        drag.width = drag.el.getBoundingClientRect().width;
        const left = tabsEl.getBoundingClientRect().left;
        drag.centers = drag.els.map((tab) => {
          const rect = tab.getBoundingClientRect();
          return rect.left + rect.width / 2 - left + tabsEl.scrollLeft;
        });
        drag.startContentX = contentX(e.clientX);
        tabsEl.classList.add("editor-tabs-dragging");
        drag.el.classList.add("editor-tab-dragging");
        drag.raf = window.requestAnimationFrame(tickEdgeScroll);
      }
      e.preventDefault();
      drag.lastClientX = e.clientX;
      updateDrag();
    }, { signal });
    tabsEl.addEventListener("pointerup", (e) => {
      if (!drag || e.pointerId !== drag.pointerId) return;
      const done = drag;
      clearDrag();
      if (!done.active) return;
      suppressed = true;
      if (done.toIndex !== done.fromIndex) {
        const [moved] = tabs.splice(done.fromIndex, 1);
        tabs.splice(done.toIndex, 0, moved);
        const others = done.els.filter((el) => el !== done.el);
        tabsEl.insertBefore(done.el, others[done.toIndex] || null);
        persistTabs();
      }
    }, { signal });
    tabsEl.addEventListener("pointercancel", clearDrag, { signal });
    tabsEl.addEventListener("click", (e) => {
      if (!suppressed) return;
      suppressed = false;
      e.preventDefault();
      e.stopPropagation();
    }, { signal, capture: true });
  }

  // A horizontal swipe on the editor surface goes to the next or the previous
  // open file, in the order the sheet lists them, wrapping around at both ends
  // like the terminal swipe and like Ctrl+Tab does here. Threshold, damping and
  // abort are the terminal swipe's (terminal-scroll-zone locks the axis at
  // 12px, terminal-swipe-nav commits at 72px or a fling and lets the frame
  // follow damped), because it is the same gesture on the same devices.
  //
  // Its stability comes from doing what the terminal's zone does rather than
  // from listening harder: the whole gesture is taken from the browser (see
  // syncSwipeZone), the axis is decided here, and the pointer is captured the
  // moment it is, so the gesture is ours until the finger lifts. Leaving the
  // vertical axis with the browser looks like less to build and is worse: the
  // browser decides the axis at the first pixels and never revisits it, so a
  // swipe that drifted downwards became a page scroll and a pointercancel.
  // The price is that scrolling the text is ours too, the way it is the
  // terminal's, finger 1:1 plus a fling that decays.
  //
  // It applies only while lines wrap. With wrapping off the surface itself
  // scrolls sideways, and then the gesture belongs to the code: taking it away
  // there would make a long line unreadable. A mouse never swipes, and a
  // gesture that starts on a selection is the selection's.
  const SWIPE_AXIS_LOCK_PX = 12;
  const SWIPE_COMMIT_PX = 72;
  const SWIPE_FLING_VX = 0.5;
  const SWIPE_MAX_TX = 56;
  const SWIPE_FOLLOW = 0.35;
  const SWIPE_VELOCITY_WINDOW_MS = 100;
  const SWIPE_VELOCITY_MIN_SPAN_MS = 15;
  // Scrolling the text is ours too now, the way the terminal's zone scrolls its
  // history: the finger moves the content 1:1, a fast release keeps it moving
  // and decays, and a touch during that catches it. The terminal caps its start
  // speed against the round trip because its scroll travels the network; this
  // one is a scrollTop on the spot, so it only has the ceiling.
  const SWIPE_FLING_START_V = 0.35;
  const SWIPE_FLING_STOP_V = 0.04;
  const SWIPE_FLING_TAU_MS = 325;
  const SWIPE_FLING_MAX_V = 4;
  // Which gestures the browser may still take inside the surface. It decides
  // the axis itself at the first pixels, so pan-y answered every swipe that
  // drifted downwards with pointercancel and scrolled the page instead. The
  // class takes both pans away (see the stylesheet) and the handler below moves
  // the content, which is what makes the terminal's swipe feel the way it does.
  //
  // Only while lines wrap and never in a comparison: with wrapping off the
  // surface scrolls sideways itself, and a comparison has two scrollers side by
  // side. A selection hands the surface back as well, so its handles keep the
  // gesture the browser gives them.
  function syncSwipeZone() {
    if (!editorReady) return;
    const tab = activeTab();
    const on = !!editorSettings.line_wrap && !!tab && !tab.compare && !editor.hasSelection();
    surfaceEl.classList.toggle("editor-swipe-zone", on);
  }

  function wireSurfaceSwipe() {
    let gesture = null;
    let pill = null;
    let flingFrame = 0;
    const stopFling = () => {
      if (flingFrame) cancelAnimationFrame(flingFrame);
      flingFrame = 0;
    };
    // A fling outliving its editor would scroll whatever page came after it.
    signal.addEventListener("abort", stopFling);
    // The element the finger would have scrolled if the browser still could:
    // the first scrollable box between what was touched and the surface.
    const scrollerFor = (node) => {
      let el = node instanceof Element ? node : null;
      while (el && el !== surfaceEl.parentElement) {
        const overflow = getComputedStyle(el).overflowY;
        if ((overflow === "auto" || overflow === "scroll") && el.scrollHeight > el.clientHeight + 1) return el;
        el = el.parentElement;
      }
      return null;
    };
    // Whatever the scroller cannot take goes on to the page, so the end of a
    // file does not swallow the rest of the gesture.
    const scrollBy = (sc, px) => {
      let left = px;
      if (sc) {
        const before = sc.scrollTop;
        sc.scrollTop = before + left;
        left -= sc.scrollTop - before;
      }
      if (Math.abs(left) >= 0.5) window.scrollBy(0, left);
    };
    const startFling = (sc, vy) => {
      let v = Math.max(-SWIPE_FLING_MAX_V, Math.min(SWIPE_FLING_MAX_V, vy));
      if (Math.abs(v) < SWIPE_FLING_START_V) return;
      let last = performance.now();
      const step = (now) => {
        const dt = Math.min(64, now - last);
        last = now;
        scrollBy(sc, -v * dt);
        v *= Math.exp(-dt / SWIPE_FLING_TAU_MS);
        flingFrame = Math.abs(v) > SWIPE_FLING_STOP_V ? requestAnimationFrame(step) : 0;
      };
      flingFrame = requestAnimationFrame(step);
    };
    const removePill = () => {
      pill?.remove();
      pill = null;
    };
    // The same pill the terminal swipe shows, same class and same place: what
    // you would land on belongs in one spot, not one per feature. No pending
    // state here, a file switch has nothing to wait for.
    const showPill = (tab, dir, progress) => {
      if (!pill) {
        pill = document.createElement("div");
        pill.className = "dc-swipe-pill";
        pill.dataset.editorSwipePill = "";
        paneColEl.appendChild(pill);
      }
      if (pill.dataset.name !== tab.name + dir) {
        pill.dataset.name = tab.name + dir;
        pill.innerHTML = `${dir < 0 ? '<i class="ti ti-chevron-left" aria-hidden="true"></i>' : ""}<span class="dc-swipe-pill-name text-truncate">${escapeHtml(tab.name)}</span>${dir > 0 ? '<i class="ti ti-chevron-right" aria-hidden="true"></i>' : ""}`;
      }
      pill.style.opacity = String(0.35 + 0.65 * progress);
    };
    const resetSurface = () => {
      if (!surfaceEl.style.transform) {
        surfaceEl.style.transition = "";
        return;
      }
      surfaceEl.style.transition = "transform 0.18s ease";
      surfaceEl.style.transform = "";
      setTimeout(() => {
        surfaceEl.style.transition = "";
      }, 200);
    };
    const endGesture = (pointerId) => {
      if (gesture && gesture.axis && pointerId !== undefined) {
        try {
          surfaceEl.releasePointerCapture(pointerId);
        } catch (error) {
          void error;
        }
      }
      gesture = null;
      resetSurface();
      removePill();
    };
    const targetOf = (dx) => {
      const i = tabs.findIndex((t) => t.path === activePath);
      if (i < 0 || tabs.length < 2) return null;
      return tabs[(i + (dx < 0 ? 1 : -1) + tabs.length) % tabs.length];
    };
    const velocity = (now, axis) => {
      const recent = gesture.samples.filter((s) => now - s.t <= SWIPE_VELOCITY_WINDOW_MS);
      if (recent.length < 2) return 0;
      const span = recent[recent.length - 1].t - recent[0].t;
      if (span < SWIPE_VELOCITY_MIN_SPAN_MS) return 0;
      return (recent[recent.length - 1][axis] - recent[0][axis]) / span;
    };
    surfaceEl.addEventListener("pointerdown", (e) => {
      stopFling();
      if (gesture || e.pointerType !== "touch" || !e.isPrimary) return;
      // One source of truth: the class says the browser handed the pans over,
      // so everything the finger does from here is this handler's job.
      if (!surfaceEl.classList.contains("editor-swipe-zone")) return;
      if (!sheetEl.hidden || !quickOpenEl.hidden) return;
      gesture = {
        pointerId: e.pointerId,
        startX: e.clientX,
        startY: e.clientY,
        lastY: e.clientY,
        dx: 0,
        axis: null,
        scroller: scrollerFor(e.target),
        samples: [{ t: e.timeStamp, x: e.clientX, y: e.clientY }],
      };
    }, { signal });
    surfaceEl.addEventListener("pointermove", (e) => {
      if (!gesture || e.pointerId !== gesture.pointerId) return;
      gesture.samples.push({ t: e.timeStamp, x: e.clientX, y: e.clientY });
      while (gesture.samples.length > 1 && e.timeStamp - gesture.samples[0].t > SWIPE_VELOCITY_WINDOW_MS) gesture.samples.shift();
      const dx = e.clientX - gesture.startX;
      const dy = e.clientY - gesture.startY;
      if (gesture.axis === null) {
        if (Math.hypot(dx, dy) < SWIPE_AXIS_LOCK_PX) return;
        gesture.axis = Math.abs(dy) >= Math.abs(dx) ? "v" : "h";
        // From here the gesture is ours, whatever the page around it does.
        try {
          surfaceEl.setPointerCapture(e.pointerId);
        } catch (error) {
          void error;
        }
      }
      e.preventDefault();
      if (gesture.axis === "v") {
        scrollBy(gesture.scroller, gesture.lastY - e.clientY);
        gesture.lastY = e.clientY;
        return;
      }
      gesture.dx = dx;
      const target = targetOf(dx);
      const tx = Math.max(-SWIPE_MAX_TX, Math.min(SWIPE_MAX_TX, dx * SWIPE_FOLLOW));
      surfaceEl.style.transition = "none";
      surfaceEl.style.transform = target && tx ? `translateX(${tx}px)` : "";
      if (target) showPill(target, dx < 0 ? 1 : -1, Math.min(1, Math.abs(dx) / SWIPE_COMMIT_PX));
      else removePill();
    }, { passive: false, signal });
    surfaceEl.addEventListener("pointerup", (e) => {
      if (!gesture || e.pointerId !== gesture.pointerId) return;
      const dx = gesture.dx;
      const axis = gesture.axis;
      const scroller = gesture.scroller;
      const vx = velocity(e.timeStamp, "x");
      const vy = velocity(e.timeStamp, "y");
      const target = targetOf(dx);
      endGesture(e.pointerId);
      if (axis === "v") {
        startFling(scroller, vy);
        return;
      }
      if (axis !== "h" || !target) return;
      const fling = Math.abs(vx) > SWIPE_FLING_VX && Math.sign(vx) === Math.sign(dx);
      if (Math.abs(dx) > SWIPE_COMMIT_PX || fling) activateTab(target.path);
    }, { signal });
    surfaceEl.addEventListener("pointercancel", (e) => {
      if (gesture && e.pointerId === gesture.pointerId) endGesture(e.pointerId);
    }, { signal });
  }

  // Reordering the open files on a phone, the way the quick nav reorders the
  // terminals: with a finger only the grip handle drags, so the tap that
  // switches and the scroll of the list never fight it; a mouse takes the whole
  // row. The new order is the tab order and goes into the same per device store
  // the strip's drag writes.
  const SHEET_DRAG_THRESHOLD = 6;
  const SHEET_EDGE_ZONE = 28;
  const SHEET_EDGE_STEP = 10;
  function wireSheetDrag() {
    let suppressed = false;
    const contentY = (clientY) => clientY - sheetBodyEl.getBoundingClientRect().top + sheetBodyEl.scrollTop;
    const updateDrag = () => {
      if (!sheetDragging()) return;
      const drag = sheetDrag;
      const dy = contentY(drag.lastClientY) - drag.startContentY;
      const draggedCenter = drag.centers[drag.fromIndex] + dy;
      let toIndex = 0;
      for (let i = 0; i < drag.centers.length; i += 1) {
        if (i !== drag.fromIndex && drag.centers[i] < draggedCenter) toIndex += 1;
      }
      drag.toIndex = toIndex;
      drag.el.style.transform = `translateY(${dy}px)`;
      drag.els.forEach((el, i) => {
        if (el === drag.el) return;
        let shift = 0;
        if (i > drag.fromIndex && i <= drag.toIndex) shift = -drag.height;
        else if (i < drag.fromIndex && i >= drag.toIndex) shift = drag.height;
        el.style.transform = shift ? `translateY(${shift}px)` : "";
      });
    };
    const tickEdgeScroll = () => {
      if (!sheetDragging()) return;
      const rect = sheetBodyEl.getBoundingClientRect();
      let delta = 0;
      if (sheetDrag.lastClientY < rect.top + SHEET_EDGE_ZONE) delta = -SHEET_EDGE_STEP;
      else if (sheetDrag.lastClientY > rect.bottom - SHEET_EDGE_ZONE) delta = SHEET_EDGE_STEP;
      if (delta) {
        const max = sheetBodyEl.scrollHeight - sheetBodyEl.clientHeight;
        const next = Math.max(0, Math.min(sheetBodyEl.scrollTop + delta, max));
        if (next !== sheetBodyEl.scrollTop) {
          sheetBodyEl.scrollTop = next;
          updateDrag();
        }
      }
      sheetDrag.raf = window.requestAnimationFrame(tickEdgeScroll);
    };
    const clearDrag = () => {
      if (!sheetDrag) return;
      if (sheetDrag.active) {
        window.cancelAnimationFrame(sheetDrag.raf);
        sheetBodyEl.classList.remove("editor-sheet-dragging");
        sheetDrag.el.classList.remove("editor-sheet-row-dragging");
        for (const el of sheetDrag.els) el.style.transform = "";
      }
      sheetDrag = null;
    };
    sheetBodyEl.addEventListener("pointerdown", (e) => {
      if (e.button !== 0 || sheetDrag || sheetKind !== "files") return;
      // A new pointer voids a suppression the last gesture left behind: a
      // stream that ended without the click it expected must not swallow the
      // next tap.
      suppressed = false;
      if (e.target.closest("[data-editor-sheet-close-tab]")) return;
      if (e.pointerType === "touch" && !e.target.closest("[data-editor-sheet-handle]")) return;
      const el = e.target.closest(".editor-sheet-row");
      if (!el) return;
      suppressed = false;
      sheetDrag = { el, pointerId: e.pointerId, startClientX: e.clientX, startClientY: e.clientY, lastClientY: e.clientY, active: false, raf: 0 };
    }, { signal });
    sheetBodyEl.addEventListener("pointermove", (e) => {
      if (!sheetDrag || e.pointerId !== sheetDrag.pointerId) return;
      if (!sheetDrag.active) {
        if (Math.hypot(e.clientX - sheetDrag.startClientX, e.clientY - sheetDrag.startClientY) < SHEET_DRAG_THRESHOLD) return;
        sheetDrag.active = true;
        sheetDrag.els = Array.from(sheetBodyEl.querySelectorAll(".editor-sheet-row"));
        sheetDrag.fromIndex = sheetDrag.els.indexOf(sheetDrag.el);
        sheetDrag.toIndex = sheetDrag.fromIndex;
        sheetDrag.height = sheetDrag.el.getBoundingClientRect().height;
        const top = sheetBodyEl.getBoundingClientRect().top;
        sheetDrag.centers = sheetDrag.els.map((row) => {
          const rect = row.getBoundingClientRect();
          return rect.top + rect.height / 2 - top + sheetBodyEl.scrollTop;
        });
        sheetDrag.startContentY = contentY(e.clientY);
        sheetBodyEl.classList.add("editor-sheet-dragging");
        sheetDrag.el.classList.add("editor-sheet-row-dragging");
        try {
          sheetDrag.el.setPointerCapture(e.pointerId);
        } catch (error) {
          void error;
        }
        sheetDrag.raf = window.requestAnimationFrame(tickEdgeScroll);
      }
      e.preventDefault();
      sheetDrag.lastClientY = e.clientY;
      updateDrag();
    }, { passive: false, signal });
    sheetBodyEl.addEventListener("pointerup", (e) => {
      if (!sheetDrag || e.pointerId !== sheetDrag.pointerId) return;
      const done = sheetDrag;
      clearDrag();
      if (!done.active) return;
      suppressed = true;
      if (done.toIndex !== done.fromIndex) {
        const [moved] = tabs.splice(done.fromIndex, 1);
        tabs.splice(done.toIndex, 0, moved);
        persistTabs();
        renderTabs();
      }
      renderFilesSheet();
    }, { signal });
    sheetBodyEl.addEventListener("pointercancel", clearDrag, { signal });
    sheetBodyEl.addEventListener("click", (e) => {
      if (!suppressed) return;
      suppressed = false;
      e.preventDefault();
      e.stopPropagation();
    }, { signal, capture: true });
  }

  wireRowMenus(tabsEl, ".editor-tab", (row, x, y) => {
    if (!row) return false;
    openTabMenu(row.dataset.path, x, y);
    return true;
  }, { signal });
  wireRowMenus(treeEl, ".editor-item", openTreeMenu, { signal });
  wireTreeDrop();
  wireSplitter();
  wireQuickOpen();
  wireTabDrag();
  wireSheetDrag();
  wireSurfaceSwipe();
  syncSwipeZone();
  paintTreeFold();
  mobileMedia.addEventListener("change", paintTreeFold, { signal });
  root.querySelector('[data-editor-setting="line_wrap"]')
    ?.addEventListener("change", syncSwipeZone, { signal });

  filesItem.addEventListener("click", openFilesSheet, { signal });
  settingsItem.addEventListener("click", openSettingsSheet, { signal });
  sheetCloseBtn.addEventListener("click", closeSheet, { signal });
  sheetEl.addEventListener("click", (e) => {
    if (e.target === sheetEl) closeSheet();
  }, { signal });
  // A row of an adopted menu did what it says; the sheet has served its purpose
  // and gets out of the way so the answer is visible. The settings keep their
  // sheet, they are selects and a switch, not one-shot actions.
  sheetBodyEl.addEventListener("click", (e) => {
    if (sheetKind !== "settings" && e.target.closest(".dropdown-item")) closeSheet();
  }, { signal });

  const projectMenuEl = root.querySelector(".editor-project-menu");
  if (projectMenuEl) projectSort.sort(projectMenuEl);

  const fullscreenBtn = root.querySelector("[data-editor-fullscreen]");
  let fullscreenOn = store.get(FULLSCREEN_KEY, "") === "1";
  // A phone has no window around the page to grow out of: the browser's own
  // chrome is all there is, and the editor already fills what is left. So the
  // whole switch stays away below the width the drawer belongs to, and the
  // stored state comes back with the wider screen.
  const fullscreenApplies = () => !mobileMedia.matches;
  const paintFullscreen = () => {
    const applies = fullscreenApplies();
    document.documentElement.classList.toggle("dc-editor-fullscreen", fullscreenOn && applies);
    fullscreenBtn.hidden = !applies;
    fullscreenBtn.setAttribute("aria-pressed", fullscreenOn ? "true" : "false");
    fullscreenBtn.title = (fullscreenOn ? "Exit fullscreen" : "Fullscreen") + " (Ctrl+Shift+Enter)";
    fullscreenBtn.innerHTML = `<i class="ti ${fullscreenOn ? "ti-minimize" : "ti-maximize"} me-2"></i>${fullscreenOn ? "Exit fullscreen" : "Fullscreen"}`;
  };
  const setFullscreen = (on) => {
    if (fullscreenOn === on || !fullscreenApplies()) return;
    fullscreenOn = on;
    store.set(FULLSCREEN_KEY, on ? "1" : "");
    paintFullscreen();
  };
  paintFullscreen();
  mobileMedia.addEventListener("change", paintFullscreen, { signal });
  fullscreenBtn.addEventListener("click", () => setFullscreen(!fullscreenOn), { signal });
  tabsEl.addEventListener("dblclick", (e) => {
    if (!e.target.closest(".editor-tab")) setFullscreen(!fullscreenOn);
  }, { signal });

  // A double tap on bare Shift opens the quick open palette like Ctrl+O. Same
  // state machine as the terminal switcher's double Ctrl/Meta: a clean tap is
  // keydown then keyup with no chord, the second keydown inside the window
  // triggers, any other key resets.
  const SHIFT_TAP_MS = 400;
  let shiftTapPending = false;
  let shiftTapAt = 0;
  document.addEventListener("keydown", (e) => {
    if (e.key === "Shift" && !e.repeat && !e.ctrlKey && !e.altKey && !e.metaKey) {
      if (shiftTapAt && Date.now() - shiftTapAt < SHIFT_TAP_MS) {
        shiftTapPending = false;
        shiftTapAt = 0;
        if (quickOpenEl.hidden) {
          e.preventDefault();
          openQuickOpen("files");
        }
        return;
      }
      shiftTapPending = true;
      shiftTapAt = 0;
      return;
    }
    shiftTapPending = false;
    shiftTapAt = 0;
    if (e.key === "Tab" && e.ctrlKey && !e.altKey && !e.metaKey) {
      e.preventDefault();
      stepTab(e.shiftKey ? -1 : 1);
    } else if ((e.metaKey || e.ctrlKey) && e.shiftKey && !e.altKey && e.key === "Enter" && quickOpenEl.hidden) {
      e.preventDefault();
      setFullscreen(!fullscreenOn);
    } else if ((e.metaKey || e.ctrlKey) && !e.shiftKey && !e.altKey && e.key.toLowerCase() === "s") {
      e.preventDefault();
      save();
    } else if ((e.metaKey || e.ctrlKey) && !e.shiftKey && !e.altKey && e.key.toLowerCase() === "o") {
      e.preventDefault();
      openQuickOpen("files");
    } else if ((e.metaKey || e.ctrlKey) && e.shiftKey && !e.altKey && e.key.toLowerCase() === "f") {
      e.preventDefault();
      openQuickOpen("search");
    } else if ((e.metaKey || e.ctrlKey) && e.shiftKey && !e.altKey && !e.repeat
      && e.key.toLowerCase() === "x" && quickOpenEl.hidden) {
      const tab = activeTab();
      if (tab) {
        e.preventDefault();
        void closeTab(tab.path);
      }
    } else if (e.key === "Escape") {
      if (!quickOpenEl.hidden) closeQuickOpen();
      else if (sheetKind) closeSheet();
      else closeDrawer();
    }
  }, { signal });
  document.addEventListener("keyup", (e) => {
    if (shiftTapPending && e.key === "Shift") shiftTapAt = Date.now();
    shiftTapPending = false;
  }, { signal });
  window.addEventListener("beforeunload", (e) => {
    if (anyDirty()) {
      e.preventDefault();
      e.returnValue = "";
    }
  }, { signal });

  // beforeunload does not fire for a boosted navigation, so guard boosted link
  // clicks and form submits too. Native loads (data-no-pe) stay with beforeunload.
  const guard = (event, node) => {
    if (anyDirty() && node && !node.closest("[data-no-pe]") && !confirm("Discard unsaved changes?")) {
      event.preventDefault();
      event.stopPropagation();
    }
  };
  document.addEventListener("click", (e) => {
    const a = e.target.closest("a[href]");
    if (a && a.host === location.host && !a.hasAttribute("target")) guard(e, a);
  }, { capture: true, signal });
  document.addEventListener("submit", (e) => guard(e, e.target), { capture: true, signal });

  editor.setVisible(false);
  await Promise.all([loadTree(), restoreTabs(), loadGitStatus()]);
  if (tabs.length === 0 && mobileMedia.matches) openDrawer();

  return () => {
    ac.abort();
    clearTimeout(statusTimer);
    clearTimeout(previewTimer);
    clearTimeout(searchTimer);
    clearTimeout(gitWatchTimer);
    if (svgPreviewUrl) URL.revokeObjectURL(svgPreviewUrl);
    document.documentElement.classList.remove("dc-editor-fullscreen");
    editor.destroy();
  };
}

// ---- editor (CodeMirror 6 with textarea fallback) --------------------------

// indentPref maps a stored "indent" setting to a style/size descriptor.
function indentPref(value) {
  if (value === "2spaces") return { style: "space", size: 2 };
  if (value === "4spaces") return { style: "space", size: 4 };
  return { style: "tab" };
}

async function createEditor(host, hooks, settings, signal, mergeHost) {
  try {
    return await createCodeMirror(host, hooks, settings, signal, mergeHost);
  } catch (err) {
    console.warn("CodeMirror unavailable, using textarea", err);
    return createTextarea(host, hooks, settings);
  }
}

// Languages are dynamic-imported by full URL. The jsDelivr dist files keep their
// @codemirror/@lezer imports bare, so they resolve through the page import map to
// the single shared instances (otherwise instanceof checks break).
const langUrl = (pkg) => `https://cdn.jsdelivr.net/npm/@codemirror/${pkg}/dist/index.js`;

// Tabler carries a glyph for the common formats, everything else keeps the
// plain file icon. Names win over extensions (Dockerfile, LICENSE, dotfiles).
const FILE_ICONS = {
  js: "ti-file-type-js", mjs: "ti-file-type-js", cjs: "ti-file-type-js",
  jsx: "ti-file-type-jsx", ts: "ti-file-type-ts", tsx: "ti-file-type-tsx",
  css: "ti-file-type-css", scss: "ti-file-type-css", less: "ti-file-type-css",
  html: "ti-file-type-html", htm: "ti-file-type-html", vue: "ti-file-type-vue",
  php: "ti-file-type-php", xml: "ti-file-type-xml", svg: "ti-file-type-svg",
  sql: "ti-file-type-sql", rs: "ti-file-type-rs", csv: "ti-file-type-csv",
  txt: "ti-file-type-txt", log: "ti-file-type-txt", pdf: "ti-file-type-pdf",
  doc: "ti-file-type-doc", docx: "ti-file-type-doc",
  zip: "ti-file-zip", tar: "ti-file-zip", gz: "ti-file-zip", tgz: "ti-file-zip",
  png: "ti-file-type-png", jpg: "ti-file-type-jpg", jpeg: "ti-file-type-jpg",
  bmp: "ti-file-type-bmp", gif: "ti-photo", webp: "ti-photo", ico: "ti-photo",
  json: "ti-json", md: "ti-markdown", markdown: "ti-markdown",
  go: "ti-brand-golang", py: "ti-brand-python",
  yml: "ti-file-settings", yaml: "ti-file-settings", toml: "ti-file-settings",
  ini: "ti-file-settings", conf: "ti-file-settings", cfg: "ti-file-settings",
  env: "ti-key", lock: "ti-lock", db: "ti-database", sqlite: "ti-database",
  sh: "ti-terminal-2", bash: "ti-terminal-2", zsh: "ti-terminal-2",
  fish: "ti-terminal-2", bashrc: "ti-terminal-2", profile: "ti-terminal-2",
  c: "ti-file-code", h: "ti-file-code", cpp: "ti-file-code", cc: "ti-file-code",
  hpp: "ti-file-code", java: "ti-file-code", rb: "ti-file-code",
};

const NAME_ICONS = {
  dockerfile: "ti-brand-docker",
  "docker-compose.yml": "ti-brand-docker",
  "docker-compose.yaml": "ti-brand-docker",
  makefile: "ti-file-code",
  license: "ti-license",
  ".gitignore": "ti-brand-git",
  ".gitattributes": "ti-brand-git",
  ".gitmodules": "ti-brand-git",
  ".env": "ti-key",
};

function fileIcon(name) {
  const lower = (name || "").toLowerCase();
  if (NAME_ICONS[lower]) return NAME_ICONS[lower];
  if (lower.startsWith(".env.")) return "ti-key";
  const ext = lower.includes(".") ? lower.split(".").pop() : "";
  return FILE_ICONS[ext] || "ti-file";
}

// Shell, Dockerfile and TOML have no lezer grammar; the legacy stream modes are
// the official route and ship as standalone ESM files.
const modeUrl = (mode) => `https://cdn.jsdelivr.net/npm/@codemirror/legacy-modes@6.5.1/mode/${mode}.js`;

const STREAM_LANGS = {
  sh: ["shell", "shell"],
  bash: ["shell", "shell"],
  zsh: ["shell", "shell"],
  ksh: ["shell", "shell"],
  fish: ["shell", "shell"],
  bashrc: ["shell", "shell"],
  profile: ["shell", "shell"],
  toml: ["toml", "toml"],
  dockerfile: ["dockerfile", "dockerFile"], // the module exports it camel cased
};

// Files the shell modes own by name, they carry no extension.
const STREAM_NAMES = {
  ".bashrc": "sh",
  ".bash_profile": "sh",
  ".bash_aliases": "sh",
  ".profile": "sh",
  ".zshrc": "sh",
  ".zprofile": "sh",
  ".envrc": "sh",
  dockerfile: "dockerfile",
};

const LANGS = {
  js: ["lang-javascript@6.2.2", "javascript", { jsx: true }],
  jsx: ["lang-javascript@6.2.2", "javascript", { jsx: true }],
  mjs: ["lang-javascript@6.2.2", "javascript", {}],
  cjs: ["lang-javascript@6.2.2", "javascript", {}],
  ts: ["lang-javascript@6.2.2", "javascript", { typescript: true }],
  tsx: ["lang-javascript@6.2.2", "javascript", { typescript: true, jsx: true }],
  go: ["lang-go@6.0.0", "go", null],
  html: ["lang-html@6.4.9", "html", null],
  htm: ["lang-html@6.4.9", "html", null],
  vue: ["lang-html@6.4.9", "html", null],
  css: ["lang-css@6.2.1", "css", null],
  scss: ["lang-css@6.2.1", "css", null],
  less: ["lang-css@6.2.1", "css", null],
  json: ["lang-json@6.0.1", "json", null],
  md: ["lang-markdown@6.2.5", "markdown", null],
  markdown: ["lang-markdown@6.2.5", "markdown", null],
  py: ["lang-python@6.1.6", "python", null],
  php: ["lang-php@6.0.1", "php", null],
  yaml: ["lang-yaml@6.1.1", "yaml", null],
  yml: ["lang-yaml@6.1.1", "yaml", null],
  xml: ["lang-xml@6.1.0", "xml", null],
  svg: ["lang-xml@6.1.0", "xml", null],
  sql: ["lang-sql@6.7.0", "sql", null],
  rs: ["lang-rust@6.0.1", "rust", null],
  c: ["lang-cpp@6.0.2", "cpp", null],
  h: ["lang-cpp@6.0.2", "cpp", null],
  cpp: ["lang-cpp@6.0.2", "cpp", null],
  cc: ["lang-cpp@6.0.2", "cpp", null],
  hpp: ["lang-cpp@6.0.2", "cpp", null],
  java: ["lang-java@6.0.1", "java", null],
};

async function createCodeMirror(host, hooks, settings, signal, mergeHost) {
  const [cm, state, view, commands, language, search, theme] = await Promise.all([
    import("codemirror"),
    import("@codemirror/state"),
    import("@codemirror/view"),
    import("@codemirror/commands"),
    import("@codemirror/language"),
    import("@codemirror/search"),
    import("@codemirror/theme-one-dark"),
  ]);
  const { EditorView, basicSetup } = cm;
  const { EditorState, Compartment } = state;
  const { keymap } = view;
  const { indentWithTab } = commands;
  const { indentUnit } = language;
  const langConf = new Compartment();
  const tabSizeConf = new Compartment();
  const indentConf = new Compartment();
  const wrapConf = new Compartment();
  const fontConf = new Compartment();
  const themeConf = new Compartment();
  // The inline diff is an extension, so it swaps in and out of the open state
  // and the working copy keeps its history. Side by side is a widget of its
  // own, see setDiff.
  const mergeConf = new Compartment();
  const darkScheme = window.matchMedia("(prefers-color-scheme: dark)");
  const schemeTheme = () => (darkScheme.matches ? theme.oneDark : []);
  let langSeq = 0;
  let mergeMod = null;
  let mergeView = null;

  const fontTheme = (px) => EditorView.theme({ "&": { fontSize: `${px}px` } });

  // Indentation priority: the open file's .editorconfig wins, then the stored
  // preference (userIndent), then the default. tab_size is the fallback tab
  // width used wherever editorconfig leaves the width unset.
  let userTabSize = settings.tab_size;
  let userIndent = indentPref(settings.indent); // { style } | { style:"space", size }
  let fileConfig = {}; // { indentStyle, indentSize, tabWidth } from editorconfig

  // effectiveIndent resolves the chain above; fromConfig flags that the value
  // is dictated by .editorconfig (so the UI shows it read-only).
  function effectiveIndent() {
    if (fileConfig.indentStyle === "space") {
      return { style: "space", size: fileConfig.indentSize || fileConfig.tabWidth || userIndent.size || userTabSize, fromConfig: true };
    }
    if (fileConfig.indentStyle === "tab") {
      return { style: "tab", fromConfig: true };
    }
    return { ...userIndent, fromConfig: false };
  }

  function reconfigureIndent() {
    const tabWidth = fileConfig.tabWidth || userTabSize;
    const ind = effectiveIndent();
    const unit = ind.style === "space" ? " ".repeat(ind.size) : "\t";
    for (const view of liveViews()) {
      view.dispatch({
        effects: [
          tabSizeConf.reconfigure(EditorState.tabSize.of(tabWidth)),
          indentConf.reconfigure(indentUnit.of(unit)),
        ],
      });
    }
  }

  function reportCursor(viewState) {
    const head = viewState.selection.main.head;
    const line = viewState.doc.lineAt(head);
    hooks.onCursor(line.number, head - line.from + 1);
  }

  // Everything both sides share. The compartments live in both, so a font or
  // theme change reaches the read only side of a diff as well.
  const sharedExtensions = (langExt) => [
    basicSetup,
    themeConf.of(schemeTheme()),
    langConf.of(langExt),
    tabSizeConf.of(EditorState.tabSize.of(userTabSize)),
    indentConf.of(indentUnit.of("\t")),
    wrapConf.of(settings.line_wrap ? EditorView.lineWrapping : []),
    fontConf.of(fontTheme(settings.font_size)),
  ];

  // A standalone editor fills its box and scrolls inside it. The two editors of
  // a merge view must not: there the outer .cm-mergeView is the scroller and
  // the editors grow to their full height, otherwise each pane clips its own
  // document at the first screenful.
  const fillsTheBox = EditorView.theme({ "&": { height: "100%" }, ".cm-scroller": { overflow: "auto" } });

  // The blame gutter: the short commit and the author next to every line, with
  // the whole message in the tooltip. It is a compartment, so switching it on
  // and off never rebuilds the document, and it rides in the writable side's
  // extensions so a rebuilt side by side view keeps it.
  const blameConf = new Compartment();
  let blameData = null;

  class BlameMarker extends view.GutterMarker {
    constructor(text, title) {
      super();
      this.text = text;
      this.title = title;
    }

    eq(other) {
      return other.text === this.text;
    }

    toDOM() {
      const el = document.createElement("span");
      el.textContent = this.text;
      el.title = this.title;
      // Inline, because one gutter is not worth a stylesheet rule: quiet enough
      // to read past, wide enough not to wrap.
      el.style.cssText = "display:inline-block;padding:0 8px 0 4px;opacity:0.55;white-space:nowrap;font-size:85%";
      return el;
    }
  }

  // blameMarkers turns the answer into one marker per commit, which is what
  // makes a file of a few thousand lines cost a handful of DOM nodes' worth of
  // description instead of one per line.
  function blameExtension(data) {
    if (!data || !Array.isArray(data.lines) || data.lines.length === 0) return [];
    const lines = data.lines;
    const markers = (data.commits || []).map((commit) => new BlameMarker(
      commit.pending ? "uncommitted" : `${commit.short} ${shortName(commit.author)}`,
      commit.pending
        ? "Not committed yet"
        : `${commit.short} · ${commit.author} · ${commitDate(commit.time)}\n${commit.summary}`,
    ));
    if (markers.length === 0) return [];
    return view.gutter({
      class: "cm-blame",
      lineMarker(active, block) {
        const at = lines[active.state.doc.lineAt(block.from).number - 1];
        return at === undefined ? null : markers[at] || null;
      },
      // The gutter keeps its width from the first commit, so the text next to it
      // does not shift while scrolling through.
      initialSpacer: () => markers[0],
    });
  }

  const editableExtensions = (langExt) => [
    keymap.of([{ key: "Ctrl-o", run: () => true }, { key: "Ctrl-f", run: search.openSearchPanel }]),
    sharedExtensions(langExt),
    keymap.of([indentWithTab]),
    mergeConf.of([]),
    blameConf.of(blameExtension(blameData)),
    EditorView.updateListener.of((u) => {
      if (u.docChanged) hooks.onChange();
      if (u.docChanged || u.selectionSet) reportCursor(u.state);
    }),
  ];

  const baseExtensions = (langExt) => [editableExtensions(langExt), fillsTheBox];

  // The other side of a diff is a revision, not a file on disk, so it is read
  // only and reports nothing: the status bar follows the working copy.
  const readOnlyExtensions = (langExt) => [
    sharedExtensions(langExt),
    mergeConf.of([]),
    EditorState.readOnly.of(true),
    EditorView.editable.of(false),
  ];

  const editorView = new EditorView({
    parent: host,
    state: EditorState.create({ doc: "", extensions: baseExtensions([]) }),
  });

  // The editor of the working copy: the plain one, or the writable side of the
  // side by side view while that is up. Everything that reads or writes the
  // open buffer goes through it.
  const workView = () => (mergeView ? mergeView.b : editorView);
  const liveViews = () => (mergeView ? [mergeView.a, mergeView.b] : [editorView]);

  // What the tab machinery asked for, see setVisible.
  let surfaceOn = false;

  // Which of the two lives in the surface right now. It has to be visibility:
  // CodeMirror's own base theme carries `display: flex !important` on the
  // editor root, and an important declaration in a stylesheet beats a plain
  // inline style, so setting `display: none` on the editor does nothing at all.
  // It would keep painting over the merge view, and hiding it that way looked
  // exactly like the side by side view never opening.
  function syncSurface() {
    editorView.dom.style.visibility = surfaceOn && !mergeView ? "" : "hidden";
    mergeHost.style.visibility = surfaceOn ? "" : "hidden";
  }

  darkScheme.addEventListener("change", () => {
    for (const view of liveViews()) view.dispatch({ effects: themeConf.reconfigure(schemeTheme()) });
  }, { signal });

  function applySetting(key, value) {
    switch (key) {
      case "tab_size":
        userTabSize = value;
        reconfigureIndent();
        break;
      case "indent":
        userIndent = indentPref(value);
        reconfigureIndent();
        break;
      case "line_wrap":
        for (const view of liveViews()) {
          view.dispatch({ effects: wrapConf.reconfigure(value ? EditorView.lineWrapping : []) });
        }
        break;
      case "font_size":
        for (const view of liveViews()) {
          view.dispatch({ effects: fontConf.reconfigure(fontTheme(value)) });
          view.requestMeasure();
        }
        break;
    }
  }

  // A file without a known extension still says what it is on its first line.
  // Only the shells are read that way, their shebang is unambiguous.
  function shebangMode(firstLine) {
    const m = /^#!\s*(\S+)(?:\s+(\S+))?/.exec(firstLine || "");
    if (!m) return null;
    const interpreter = (m[1].endsWith("/env") ? m[2] || "" : m[1]).split("/").pop();
    return /^(sh|bash|zsh|ksh|dash|ash|fish)$/.test(interpreter || "") ? "shell" : null;
  }

  async function langFor(filename, firstLine) {
    const name = (filename || "").toLowerCase();
    const ext = name.includes(".") ? name.split(".").pop() : STREAM_NAMES[name] || "";
    // Dockerfile.dev and friends keep the type in front of the dot.
    const byName = STREAM_NAMES[name] || (name.startsWith("dockerfile.") ? "dockerfile" : "");
    const known = STREAM_LANGS[byName || ext];
    const shebang = !known && !LANGS[ext] ? shebangMode(firstLine) : null;
    const stream = known || (shebang ? [shebang, shebang] : null);
    try {
      if (stream) {
        const [mode, fn] = stream;
        const mod = await import(modeUrl(mode));
        return language.StreamLanguage.define(mod[fn]);
      }
      const spec = LANGS[ext];
      if (!spec) return [];
      const [pkg, fn, arg] = spec;
      const mod = await import(langUrl(pkg));
      return arg ? mod[fn](arg) : mod[fn]();
    } catch (err) {
      console.warn("language load failed", err);
      return [];
    }
  }

  function refreshLanguage(filename) {
    const seq = ++langSeq;
    langFor(filename, workView().state.doc.line(1).text).then((langExt) => {
      if (seq !== langSeq) return;
      for (const view of liveViews()) view.dispatch({ effects: langConf.reconfigure(langExt) });
    });
  }

  // ---- diff ------------------------------------------------------------------

  async function loadMerge() {
    if (!mergeMod) mergeMod = await import("@codemirror/merge");
    return mergeMod;
  }

  // How many unchanged lines stay visible around a change when the folding is
  // on. Three reads well on a phone and on a wide screen, it is not worth a
  // setting.
  const collapseMargin = 3;
  const collapseOption = (spec) => (spec.collapse ? { margin: collapseMargin } : undefined);

  // Whether the two sided view currently holds two files rather than a file and
  // a revision. Both are a MergeView; only this tells them apart.
  let compareOn = false;

  function dropMergeView() {
    if (!mergeView) return;
    mergeView.destroy();
    mergeView = null;
    compareOn = false;
    mergeHost.replaceChildren();
    mergeHost.hidden = true;
    syncSurface();
  }

  // setCompare builds the comparison of two files on disk. Both sides are
  // writable, because both are real files; the revision side of a diff is not,
  // which is the whole difference between this and setDiff. Each side gets the
  // language of its own name, so comparing a .js against a .txt still reads.
  async function setCompare(spec) {
    const merge = await loadMerge();
    const [leftLang, rightLang] = await Promise.all([
      langFor(spec.left.name, (spec.left.doc || "").split("\n", 1)[0]),
      langFor(spec.right.name, (spec.right.doc || "").split("\n", 1)[0]),
    ]);
    // Loading the two languages and the merge package takes a moment, and a
    // person can change tabs in it. Mounting then puts this comparison over
    // whatever is open now, and every later read of the buffer, the save
    // included, would address the wrong document. The caller says what still
    // counts, and nothing above touches the surface, so leaving is free.
    if (spec.valid && !spec.valid()) return;
    // Blame belongs to one file, so it does not ride into this view.
    blameData = null;
    dropMergeView();
    // Built without a parent and hung up afterwards, so a constructor that
    // throws leaves no empty surface behind, exactly like setDiff.
    const view = new merge.MergeView({
      a: { doc: spec.left.doc, extensions: editableExtensions(leftLang) },
      b: { doc: spec.right.doc, extensions: editableExtensions(rightLang) },
      collapseUnchanged: collapseOption(spec),
      highlightChanges: true,
      gutter: true,
    });
    view.dom.style.height = "100%";
    mergeHost.replaceChildren(view.dom);
    mergeHost.hidden = false;
    mergeView = view;
    compareOn = true;
    syncSurface();
    view.b.requestMeasure();
    reportCursor(view.b.state);
  }

  // setDiff switches the open buffer between the plain editor, the side by side
  // view and the inline one.
  //
  // The working copy's text is carried across every switch and its content is
  // never taken from the revision, so a buffer with unsaved work stays what it
  // was. Its undo history survives the inline switch, which only swaps an
  // extension on the very same editor. It does not survive the side by side
  // switch: MergeView builds its two editors from a document, CodeMirror offers
  // no way to hand it an existing EditorState, so the history starts over on
  // that side. Content and dirty marker survive, undo is the price.
  async function setDiff(spec) {
    const langExt = await langFor(spec.name, workView().state.doc.line(1).text);
    if (spec.mode === "side") {
      const merge = await loadMerge();
      // See setCompare: a tab switch during the two loads would mount this over
      // the file that is open now.
      if (spec.valid && !spec.valid()) return;
      // Read after the loads, never before: what was typed while they ran
      // belongs to the buffer, and a snapshot from before would drop it.
      const doc = workView().state.doc;
      dropMergeView();
      // Built first, without a parent, and only then put on screen: a
      // constructor that throws must leave the plain editor where it was
      // instead of an empty surface nobody can read anything from.
      const view = new merge.MergeView({
        a: { doc: spec.original, extensions: readOnlyExtensions(langExt) },
        b: { doc, extensions: editableExtensions(langExt) },
        collapseUnchanged: collapseOption(spec),
        highlightChanges: true,
        gutter: true,
      });
      view.dom.style.height = "100%";
      mergeHost.replaceChildren(view.dom);
      mergeHost.hidden = false;
      mergeView = view;
      syncSurface();
      view.b.requestMeasure();
      reportCursor(view.b.state);
      return;
    }
    const merge = spec.mode === "inline" ? await loadMerge() : null;
    // Everything below writes into the open editor, so the same question as in
    // the side by side branch has to be asked before the first of those writes.
    if (spec.valid && !spec.valid()) return;
    if (mergeView) {
      // Coming back from side by side: the text the person has now is what the
      // plain editor continues with.
      const text = mergeView.b.state.doc;
      dropMergeView();
      editorView.setState(EditorState.create({ doc: text, extensions: baseExtensions(langExt) }));
    }
    editorView.dispatch({
      effects: mergeConf.reconfigure(merge
        ? merge.unifiedMergeView({
          original: spec.original,
          // Nothing in the editor writes a chunk back into the buffer, the
          // revision side is there to be read.
          mergeControls: false,
          gutter: true,
          highlightChanges: true,
          collapseUnchanged: collapseOption(spec),
        })
        : []),
    });
    editorView.requestMeasure();
  }

  // setOriginal replaces the revision side without touching the working copy,
  // which is what the reload behind "base moved" needs: the buffer, its dirty
  // marker and its undo history stay exactly as they are.
  async function setOriginal(spec) {
    if (mergeView) {
      mergeView.a.dispatch({ changes: { from: 0, to: mergeView.a.state.doc.length, insert: spec.original } });
      return;
    }
    const merge = await loadMerge();
    if (spec.valid && !spec.valid()) return;
    editorView.dispatch({
      effects: mergeConf.reconfigure(merge.unifiedMergeView({
        original: spec.original,
        mergeControls: false,
        gutter: true,
        highlightChanges: true,
        collapseUnchanged: collapseOption(spec),
      })),
    });
  }

  // exitDiff drops any diff view without carrying a document anywhere. The
  // caller is about to show another tab or nothing at all.
  function exitDiff() {
    dropMergeView();
    editorView.dispatch({ effects: mergeConf.reconfigure([]) });
  }

  // Re-measure when the viewport changes (orientation flip resizes the editor
  // box; CodeMirror must re-layout or it paints nothing for the new size).
  window.addEventListener("resize", () => {
    for (const view of liveViews()) view.requestMeasure();
  }, { signal });

  return {
    canDiff: true,
    setDiff,
    setOriginal,
    exitDiff,
    setCompare,
    canBlame: true,
    setBlame(data) {
      blameData = data;
      workView().dispatch({ effects: blameConf.reconfigure(blameExtension(data)) });
    },
    comparing: () => compareOn && !!mergeView,
    compareValue(side) {
      if (!mergeView) return "";
      return (side === "left" ? mergeView.a : mergeView.b).state.doc.toString();
    },
    // Both documents come back to the tab, so a tab switch costs the two undo
    // histories and never a character. MergeView cannot be handed an existing
    // EditorState, which is the same limit setDiff runs into.
    captureCompare(tab) {
      // compareOn, not just a merge view: a diff is a merge view too, and
      // reading its two sides into a comparison's documents would put the
      // revision of another file where this tab's right hand file belongs.
      if (!mergeView || !compareOn) return;
      tab.compare.leftDoc = mergeView.a.state.doc.toString();
      tab.compare.rightDoc = mergeView.b.state.doc.toString();
    },
    async createDoc(content, filename) {
      const langExt = await langFor(filename, (content || "").split("\n", 1)[0]);
      const state = EditorState.create({ doc: content, extensions: baseExtensions(langExt) });
      return { state, saved: state.doc };
    },
    showDoc(tab) {
      exitDiff();
      editorView.setState(tab.handle.state);
      fileConfig = tab.editorConfig || {};
      reconfigureIndent();
      editorView.dispatch({
        effects: [
          wrapConf.reconfigure(settings.line_wrap ? EditorView.lineWrapping : []),
          fontConf.reconfigure(fontTheme(settings.font_size)),
          themeConf.reconfigure(schemeTheme()),
        ],
      });
      refreshLanguage(tab.name);
      reportCursor(editorView.state);
      // setState resets the scroller, so a tab switch would drop you at the top
      // of the file you come back to. The offset is captured per tab and set
      // again after the measure pass that follows the state swap.
      const top = tab.handle.scrollTop || 0;
      editorView.scrollDOM.scrollTop = top;
      // Force a layout pass in case the editor mounted at zero size.
      editorView.requestMeasure();
      requestAnimationFrame(() => {
        editorView.scrollDOM.scrollTop = top;
        editorView.requestMeasure();
      });
    },
    captureDoc(tab) {
      // The side by side editor's state carries the merge machinery, it cannot
      // be handed back to the plain editor. Its text can, so the tab keeps that
      // and rebuilds a plain state from it.
      if (mergeView) {
        tab.handle.state = EditorState.create({ doc: mergeView.b.state.doc, extensions: baseExtensions([]) });
        tab.handle.scrollTop = 0;
        return;
      }
      tab.handle.state = editorView.state;
      tab.handle.scrollTop = editorView.scrollDOM.scrollTop;
    },
    valueOf(tab, isActive) {
      return isActive ? workView().state.doc.toString() : tab.handle.state.doc.toString();
    },
    isClean(tab, isActive) {
      const doc = isActive ? workView().state.doc : tab.handle.state.doc;
      return doc.eq(tab.handle.saved);
    },
    markSaved(tab, isActive) {
      tab.handle.saved = isActive ? workView().state.doc : tab.handle.state.doc;
    },
    search() {
      search.openSearchPanel(workView());
      return true;
    },
    gotoLine() {
      search.gotoLine(workView());
      return true;
    },
    jumpTo(line) {
      const view = workView();
      const doc = view.state.doc;
      const pos = doc.line(Math.max(1, Math.min(line, doc.lines))).from;
      view.dispatch({
        selection: { anchor: pos },
        effects: EditorView.scrollIntoView(pos, { y: "center" }),
      });
      view.focus();
      return true;
    },
    // The swipe zone asks this one: a gesture must not take a selection's
    // place.
    hasSelection() {
      return !workView().state.selection.main.empty;
    },
    refreshLanguage,
    applyEditorConfig(ec) {
      fileConfig = ec || {};
      reconfigureIndent();
    },
    getIndent: effectiveIndent,
    getTabWidth: () => fileConfig.tabWidth || userTabSize,
    applySetting,
    setVisible(on) {
      surfaceOn = on;
      syncSurface();
    },
    focus() {
      workView().focus();
    },
    measure() {
      for (const view of liveViews()) view.requestMeasure();
    },
    destroy() {
      dropMergeView();
      editorView.destroy();
    },
  };
}

function createTextarea(host, hooks, settings) {
  const ta = document.createElement("textarea");
  ta.className = "editor-textarea form-control font-monospace";
  ta.spellcheck = false;
  ta.addEventListener("input", () => hooks.onChange());
  host.appendChild(ta);
  // The textarea cannot insert spaces on Tab, so indent here only drives the
  // visual tab width and the dropdown readout. Priority matches CodeMirror:
  // .editorconfig over the stored preference over the default.
  let userIndent = indentPref(settings.indent);
  let userTabSize = settings.tab_size;
  let fileConfig = {};
  const effectiveIndent = () => {
    if (fileConfig.indentStyle === "space") {
      return { style: "space", size: fileConfig.indentSize || fileConfig.tabWidth || userIndent.size || userTabSize, fromConfig: true };
    }
    if (fileConfig.indentStyle === "tab") return { style: "tab", fromConfig: true };
    return { ...userIndent, fromConfig: false };
  };
  const applyTabWidth = () => {
    ta.style.tabSize = String(fileConfig.tabWidth || userTabSize);
  };
  const applySetting = (key, value) => {
    if (key === "tab_size") {
      userTabSize = value;
      applyTabWidth();
    } else if (key === "font_size") ta.style.fontSize = `${value}px`;
    else if (key === "line_wrap") ta.style.whiteSpace = value ? "pre-wrap" : "pre";
    else if (key === "indent") userIndent = indentPref(value);
  };
  applySetting("tab_size", settings.tab_size);
  applySetting("font_size", settings.font_size);
  applySetting("line_wrap", settings.line_wrap);
  const reportCursor = () => {
    const before = ta.value.slice(0, ta.selectionStart);
    const lastBreak = before.lastIndexOf("\n");
    hooks.onCursor((before.match(/\n/g) || []).length + 1, before.length - lastBreak);
  };
  for (const type of ["input", "click", "keyup"]) {
    ta.addEventListener(type, reportCursor);
  }
  return {
    async createDoc(content) {
      return { value: content, saved: content };
    },
    showDoc(tab) {
      fileConfig = tab.editorConfig || {};
      ta.value = tab.handle.value;
      applyTabWidth();
      ta.scrollTop = tab.handle.scrollTop || 0;
      reportCursor();
    },
    captureDoc(tab) {
      tab.handle.value = ta.value;
      tab.handle.scrollTop = ta.scrollTop;
    },
    valueOf(tab, isActive) {
      return isActive ? ta.value : tab.handle.value;
    },
    isClean(tab, isActive) {
      return (isActive ? ta.value : tab.handle.value) === tab.handle.saved;
    },
    markSaved(tab, isActive) {
      tab.handle.saved = isActive ? ta.value : tab.handle.value;
    },
    search() {
      return false;
    },
    gotoLine() {
      return false;
    },
    jumpTo() {
      return false;
    },
    hasSelection() {
      return ta.selectionStart !== ta.selectionEnd;
    },
    refreshLanguage() {},
    // Without CodeMirror there is no diff and no comparison either: the
    // fallback exists so a failed CDN still lets you read and write files.
    canDiff: false,
    async setDiff() {},
    async setOriginal() {},
    exitDiff() {},
    async setCompare() {
      throw new Error("Comparing two files needs the CodeMirror editor.");
    },
    canBlame: false,
    setBlame() {},
    comparing: () => false,
    compareValue: () => "",
    captureCompare() {},
    applyEditorConfig(ec) {
      fileConfig = ec || {};
      applyTabWidth();
    },
    getIndent: effectiveIndent,
    getTabWidth: () => fileConfig.tabWidth || userTabSize,
    applySetting,
    setVisible(on) {
      ta.style.visibility = on ? "" : "hidden";
    },
    focus() {
      ta.focus();
    },
    measure() {},
    destroy() {},
  };
}

// Editor settings are stored per-device in localStorage (font size, wrap,
// indentation and which diff view to use depend on the device/screen, so they
// should not follow the user across machines). The stored indentation is the
// fallback used when the open file's .editorconfig does not dictate one.
const EDITOR_SETTINGS_KEY = "dc-editor-settings";

function loadEditorSettings() {
  const def = { tab_size: 4, indent: "tab", line_wrap: false, font_size: 14, diff_view: "auto", diff_collapse: true };
  let stored = {};
  try {
    stored = store.getJSON(EDITOR_SETTINGS_KEY, {}) || {};
  } catch {
    stored = {};
  }
  const s = { ...def, ...stored };
  if (![2, 4, 8].includes(s.tab_size)) s.tab_size = def.tab_size;
  if (!["tab", "2spaces", "4spaces"].includes(s.indent)) s.indent = def.indent;
  if (typeof s.line_wrap !== "boolean") s.line_wrap = def.line_wrap;
  if (!(s.font_size >= 10 && s.font_size <= 24)) s.font_size = def.font_size;
  if (!["auto", "side", "inline"].includes(s.diff_view)) s.diff_view = def.diff_view;
  if (typeof s.diff_collapse !== "boolean") s.diff_collapse = def.diff_collapse;
  return s;
}

function saveEditorSettings(settings) {
  try {
    store.setJSON(EDITOR_SETTINGS_KEY, settings);
  } catch (err) {
    console.warn("failed to save editor settings", err);
  }
}

// setupSettingsUI initializes the editor-settings dropdown controls from the
// stored values, then on change applies the value live and persists it. onChange
// carries the key on to settings the editor itself does not own, how a
// comparison looks for one, which belongs to the tab and not to the buffer.
function setupSettingsUI(root, editor, settings, onChange) {
  const numeric = (key) => key === "tab_size" || key === "font_size";
  root.querySelectorAll("[data-editor-setting]").forEach((el) => {
    const key = el.dataset.editorSetting;
    if (el.type === "checkbox") el.checked = !!settings[key];
    else el.value = String(settings[key]);
    el.addEventListener("change", () => {
      const value = el.type === "checkbox" ? el.checked : numeric(key) ? parseInt(el.value, 10) : el.value;
      settings[key] = value;
      editor.applySetting(key, value);
      saveEditorSettings(settings);
      onChange?.(key, value);
    });
  });
}

// setupIndentControl wires the Indentation dropdown. It shows the effective
// indentation (.editorconfig over the stored preference over the default). When
// .editorconfig dictates the value the control is read-only with a hint;
// otherwise editing it updates the stored preference. Returns a sync function
// the caller invokes after each file open.
function setupIndentControl(root, editor, settings) {
  const select = root.querySelector("[data-editor-indent]");
  if (!select) return () => {};
  const display = root.querySelector("[data-editor-indent-display]");
  const hint = root.querySelector("[data-editor-indent-hint]");
  const valueOf = (ind) => (ind.style === "space" ? `${ind.size}spaces` : "tab");
  const labelOf = (ind) => (ind.style === "space" ? `${ind.size} spaces` : "Tab");
  function sync() {
    const ind = editor.getIndent();
    const val = valueOf(ind);
    if (ind.style === "space" && !select.querySelector(`option[value="${val}"]`)) {
      const opt = document.createElement("option");
      opt.value = val;
      opt.textContent = `${ind.size} spaces`;
      select.appendChild(opt);
    }
    select.value = val;
    // From .editorconfig: show a disabled text input with the value; otherwise
    // the editable dropdown.
    const fromConfig = !!ind.fromConfig;
    select.hidden = fromConfig;
    if (display) {
      display.hidden = !fromConfig;
      display.value = labelOf(ind);
    }
    if (hint) hint.hidden = !fromConfig;
  }
  select.addEventListener("change", () => {
    settings.indent = select.value;
    editor.applySetting("indent", select.value);
    saveEditorSettings(settings);
    sync();
  });
  sync();
  return sync;
}

function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}

// commitDate is how a commit's time reads next to it: the device's own format,
// because that is the one its owner reads without translating.
function commitDate(seconds) {
  if (!seconds) return "";
  return new Date(seconds * 1000).toLocaleString();
}

// shortName keeps a gutter narrow. The full name is in the tooltip.
function shortName(name) {
  const first = String(name || "").split(/\s+/)[0] || "";
  return first.length > 12 ? `${first.slice(0, 11)}…` : first;
}

function formatSize(bytes) {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KiB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`;
}

// The two measures the diff limits are read against.
function countLines(text) {
  if (!text) return 0;
  let lines = 1;
  for (let i = 0; i < text.length; i += 1) {
    if (text.charCodeAt(i) === 10) lines += 1;
  }
  return lines;
}

const utf8 = new TextEncoder();

function byteLength(text) {
  return text ? utf8.encode(text).length : 0;
}

// promptName asks for just a name; the location (selected folder, or project
// root) is shown so the user never types a path. Returns the trimmed name, or
// null when cancelled/empty.
async function promptName(kind, dir) {
  const where = dir ? `${dir}/` : "project root";
  return promptText({
    title: `New ${kind}`,
    html: `<div class="text-secondary small mb-2">in <code>${escapeHtml(where)}</code></div>`,
    placeholder: kind === "folder" ? "folder name" : "file name",
    confirmText: "Create",
    validatorMessage: "Please enter a name.",
  });
}

class Editor extends HTMLElement {
  connectedCallback() {
    if (this.inited) return;
    this.inited = true;
    init(this)
      .then((teardown) => {
        if (this.isConnected) this.teardown = teardown;
        else teardown();
      })
      .catch((err) => {
        console.error("editor init failed", err);
        notifyError("Editor failed to load. Reload the page to try again.");
      });
  }

  disconnectedCallback() {
    this.teardown?.();
    this.teardown = null;
    this.inited = false;
  }
}

customElements.define("dc-editor", Editor);
