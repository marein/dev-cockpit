// Per-project code editor: lazy directory tree (drawer on small screens, drag
// resizable column on wide ones), tabbed CodeMirror 6 buffers with per-tab undo
// history, quick open palette and a markdown preview. CodeMirror is loaded from
// a CDN; if that fails we fall back to a plain <textarea> so viewing/editing
// still works.
import { notifyError } from "@dc/toast";
import { menuJustClosed, openMenu, wireRowMenus } from "@dc/contextmenu";
import { available as dialogAvailable, confirm as confirmDialog, fire as fireDialog, promptText } from "@dc/dialog";
import { csrfHeaders, ensureOk, getJSON, postForm } from "@dc/http";
import * as projectSort from "@dc/project-sort";
import * as store from "@dc/store";

const MAX_RESTORED_TABS = 20;
const MAX_SAVED_TREE_DIRS = 200;
const QUICK_OPEN_LIMIT = 100;
const PREVIEW_DEBOUNCE_MS = 500;
const TREE_WIDTH_KEY = "dc-editor-tree-width";
const FULLSCREEN_KEY = "dc-editor-fullscreen";

async function init(root) {
  const name = root.dataset.editorName;
  const base = `/projects/${encodeURIComponent(name)}/editor`;
  const tabsKey = `dc-editor-tabs:${name}`;
  const treeKey = `dc-editor-tree:${name}`;

  const bodyEl = root.querySelector(".editor-body");
  const treeEl = root.querySelector("[data-editor-tree]");
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
  const quickOpenBtn = root.querySelector("[data-editor-quick-open]");
  const searchProjectBtn = root.querySelector("[data-editor-search-project]");
  const searchProjectItem = root.querySelector("[data-editor-search-project-item]");
  const findBtn = root.querySelector("[data-editor-find]");
  const findItem = root.querySelector("[data-editor-find-item]");
  const gotoItem = root.querySelector("[data-editor-goto]");
  const saveAllItem = root.querySelector("[data-editor-save-all]");
  const copyPathItem = root.querySelector("[data-editor-copy-path]");
  const downloadItem = root.querySelector("[data-editor-download]");
  const renameItem = root.querySelector("[data-editor-rename]");
  const deleteItem = root.querySelector("[data-editor-delete]");
  const previewToggleBtn = root.querySelector("[data-editor-preview-toggle]");
  const viewerEl = root.querySelector("[data-editor-viewer]");
  const drawerToggleBtn = root.querySelector("[data-editor-drawer-toggle]");
  const browseBtn = root.querySelector("[data-editor-browse]");
  const backdropEl = root.querySelector("[data-editor-backdrop]");
  const splitterEl = root.querySelector("[data-editor-splitter]");
  const quickOpenEl = root.querySelector("[data-editor-quickopen]");
  const quickOpenInput = root.querySelector("[data-editor-quickopen-input]");
  const quickOpenList = root.querySelector("[data-editor-quickopen-list]");

  const editorSettings = loadEditorSettings();
  const ac = new AbortController();
  const signal = ac.signal;
  const mobileMedia = window.matchMedia("(max-width: 767.98px), (max-height: 500px)");
  const pointerMedia = window.matchMedia("(hover: hover) and (pointer: fine)");

  const editor = await createEditor(surfaceEl, { onChange, onCursor }, editorSettings, signal);
  setupSettingsUI(root, editor, editorSettings);
  const syncIndentControl = setupIndentControl(root, editor, editorSettings);

  const tabs = [];
  let activePath = null;
  let previewOn = false;
  let selected = null; // { path, isDir } — the tree row used as the "create in" target
  // dir paths kept open so a rebuild doesn't collapse the tree; restored per
  // project so the folder layout survives a reload like the tabs do
  const expanded = new Set(store.getJSON(treeKey, []).filter((p) => typeof p === "string" && p));
  const opening = new Set();
  let statusTimer = 0;
  let previewTimer = 0;
  let svgPreviewUrl = null;

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
  }

  function syncIndentInfo() {
    const tab = activeTab();
    indentInfoEl.hidden = !tab || !!tab.kind;
    if (!tab || tab.kind) return;
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
    btn.title = tab.path;
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
    return btn;
  }

  function renderTabs() {
    tabsEl.replaceChildren(...tabs.map(tabElement));
    tabsEl.querySelector(".editor-tab.active")?.scrollIntoView({ block: "nearest", inline: "nearest" });
  }

  function updateActionStates() {
    const tab = activeTab();
    const textTab = tab && !tab.kind ? tab : null;
    saveBtn.disabled = !textTab || !textTab.dirty;
    for (const el of [findBtn, findItem, gotoItem]) {
      if (el) el.disabled = !textTab;
    }
    for (const el of [copyPathItem, downloadItem, renameItem, deleteItem]) {
      if (el) el.disabled = !tab;
    }
    saveAllItem.disabled = !anyDirty();
  }

  function afterActiveChanged() {
    const tab = activeTab();
    placeholderEl.hidden = !!tab;
    pathEl.textContent = tab ? tab.path : "";
    pathEl.title = tab ? tab.path : "";
    posEl.hidden = !tab || !!tab.kind;
    renderTabs();
    updateActionStates();
    syncIndentControl();
    syncIndentInfo();
    syncPreview();
    if (tab) markTreeSelection(tab.path);
    persistTabs();
  }

  function activateTab(path) {
    const tab = tabByPath(path);
    if (!tab || activePath === path) return;
    const prev = activeTab();
    if (prev && !prev.kind) editor.captureDoc(prev);
    activePath = path;
    if (tab.kind) {
      editor.setVisible(false);
      renderViewer(tab);
    } else {
      viewerEl.hidden = true;
      editor.showDoc(tab);
      editor.setVisible(true);
      if (pointerMedia.matches) editor.focus();
    }
    afterActiveChanged();
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
    return [
      { label: "Close", icon: "ti-x", action: () => closeTab(tab.path) },
      { label: "Close others", icon: "ti-square-x", disabled: tabs.length < 2, action: () => closeMany(tabs.filter((t) => t !== tab)) },
      { label: "Close to the right", icon: "ti-arrow-bar-to-right", disabled: index === tabs.length - 1, action: () => closeMany(tabs.slice(index + 1)) },
      { label: "Close all", icon: "ti-circle-x", action: () => closeMany(tabs) },
      { divider: true },
      { label: "Copy file", icon: "ti-files", action: () => copyToClipboard(tab.path, false) },
      clipboard ? { label: `Paste "${baseName(clipboard.path)}"`, icon: "ti-clipboard", action: () => void pasteInto(parentDir(tab.path)) } : null,
      { label: "Copy path", icon: "ti-copy", action: () => copyPath(tab.path) },
      { label: "Download", icon: "ti-download", action: () => startDownload(tab.path) },
      isArchive(tab.name) ? { label: "Extract here", icon: "ti-file-zip", action: () => void extractArchive(tab.path) } : null,
      { label: "Reveal in tree", icon: "ti-list-tree", action: () => revealInTree(tab.path) },
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
  }

  // Dirty means "differs from the saved content", not "was touched": undoing
  // or retyping back to the saved state clears the flag again.
  function onChange() {
    const tab = activeTab();
    if (!tab) return;
    markDirty(tab, !editor.isClean(tab, true));
    schedulePreview(PREVIEW_DEBOUNCE_MS);
  }

  function persistTabs() {
    store.setJSON(tabsKey, { open: tabs.map((t) => t.path), active: activePath });
  }

  function persistExpanded() {
    store.setJSON(treeKey, [...expanded].slice(0, MAX_SAVED_TREE_DIRS));
  }

  async function restoreTabs() {
    const saved = store.getJSON(tabsKey, null);
    if (!saved || !Array.isArray(saved.open) || saved.open.length === 0) return;
    const paths = saved.open.filter((p) => typeof p === "string" && p).slice(0, MAX_RESTORED_TABS);
    const results = await Promise.allSettled(
      paths.map((p) => getJSON(`${base}/file?path=${encodeURIComponent(p)}`, { signal })),
    );
    for (let i = 0; i < results.length; i++) {
      if (results[i].status !== "fulfilled" || tabByPath(paths[i])) continue;
      const data = results[i].value;
      tabs.push(await tabFor(paths[i], data));
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
    if (entry.isDir) row.dataset.dir = "1";
    row.innerHTML = `<i class="ti ${icon} editor-item-icon"></i><span class="editor-item-name text-truncate">${escapeHtml(entry.name)}</span>`;
    row.draggable = true;
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
    row.title = entry.sizeText ? `${entry.path} · ${entry.sizeText}` : entry.path;
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

  // ---- file actions ----------------------------------------------------------

  async function saveTab(tab) {
    const res = await postForm(`${base}/file`, { path: tab.path, content: editor.valueOf(tab, tab.path === activePath) });
    await ensureOk(res, "Failed to save file.");
  }

  async function save() {
    const tab = activeTab();
    if (!tab || !tab.dirty) return;
    status("Saving…");
    saveBtn.disabled = true;
    try {
      await saveTab(tab);
      editor.markSaved(tab, tab.path === activePath);
      markDirty(tab, false);
      status(`Saved ${tab.path}`, "ok");
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
        editor.markSaved(tab, tab.path === activePath);
        markDirty(tab, false);
      }
      status(dirtyTabs.length === 1 ? `Saved ${dirtyTabs[0].path}` : `Saved ${dirtyTabs.length} files`, "ok");
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
      for (const tab of [...tabs]) {
        if (tab.path === targetPath || tab.path.startsWith(targetPath + "/")) closeTab(tab.path, true);
      }
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
    for (const tab of tabs) {
      tab.path = moved(tab.path);
      tab.name = baseName(tab.path);
    }
    if (activePath) activePath = moved(activePath);
    for (const p of [...expanded]) {
      const next = moved(p);
      if (next !== p) {
        expanded.delete(p);
        expanded.add(next);
      }
    }
    persistExpanded();
    const tab = activeTab();
    pathEl.textContent = tab ? tab.path : "";
    pathEl.title = tab ? tab.path : "";
    if (tab && tab.path.startsWith(newPath)) {
      if (tab.kind) renderViewer(tab);
      else editor.refreshLanguage(tab.name);
    }
    renderTabs();
    updateActionStates();
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
      await loadTree();
      status(`Created ${data.entry ? data.entry.path : path}`, "ok");
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

  function previewVisible() {
    const tab = activeTab();
    return !!(tab && !tab.kind && previewOn && hasPreview(tab.name));
  }

  function syncPreview() {
    const tab = activeTab();
    previewToggleBtn.hidden = !tab || !!tab.kind || !hasPreview(tab.name);
    const show = previewVisible();
    previewPaneEl.hidden = !show;
    surfaceEl.classList.toggle("editor-preview-split", show);
    previewToggleBtn.classList.toggle("active", show);
    const icon = previewToggleBtn.querySelector("i");
    icon.classList.toggle("ti-eye", !show);
    icon.classList.toggle("ti-eye-off", show);
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

  function togglePreview() {
    previewOn = !previewOn;
    syncPreview();
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

  function toggleDrawer() {
    if (root.classList.contains("editor-drawer-open")) closeDrawer();
    else openDrawer();
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
    quickOpenBtn.addEventListener("click", () => openQuickOpen("files"), { signal });
    searchProjectBtn.addEventListener("click", () => openQuickOpen("search"), { signal });
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
  deleteItem.addEventListener("click", () => {
    const tab = activeTab();
    if (tab) deletePath(tab.path);
  }, { signal });
  renameItem.addEventListener("click", () => {
    const tab = activeTab();
    if (tab) renameEntry({ path: tab.path, name: tab.name, isDir: false });
  }, { signal });
  copyPathItem.addEventListener("click", () => {
    const tab = activeTab();
    if (tab) copyPath(tab.path);
  }, { signal });
  downloadItem.addEventListener("click", () => {
    const tab = activeTab();
    if (tab) startDownload(tab.path);
  }, { signal });
  refreshBtn.addEventListener("click", loadTree, { signal });
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
  for (const el of [findBtn, findItem]) {
    el?.addEventListener("click", () => {
      if (!editor.search()) status("Search requires the CodeMirror editor.", "error");
    }, { signal });
  }
  gotoItem.addEventListener("click", () => {
    if (!editor.gotoLine()) status("Go to line requires the CodeMirror editor.", "error");
  }, { signal });
  previewToggleBtn.addEventListener("click", togglePreview, { signal });
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

  const projectMenuEl = root.querySelector(".editor-project-menu");
  if (projectMenuEl) projectSort.sort(projectMenuEl);

  const fullscreenBtn = root.querySelector("[data-editor-fullscreen]");
  const finePointer = !window.matchMedia("(pointer: coarse)").matches;
  let fullscreenOn = finePointer && store.get(FULLSCREEN_KEY, "") === "1";
  const paintFullscreen = () => {
    document.documentElement.classList.toggle("dc-editor-fullscreen", fullscreenOn);
    fullscreenBtn.setAttribute("aria-pressed", fullscreenOn ? "true" : "false");
    fullscreenBtn.setAttribute("aria-label", fullscreenOn ? "Exit fullscreen" : "Fullscreen");
    fullscreenBtn.title = (fullscreenOn ? "Exit fullscreen" : "Fullscreen") + " (Ctrl+Shift+Enter)";
    const icon = fullscreenBtn.querySelector("i");
    if (icon) icon.className = fullscreenOn ? "ti ti-minimize" : "ti ti-maximize";
  };
  const setFullscreen = (on) => {
    if (!finePointer || fullscreenOn === on) return;
    fullscreenOn = on;
    store.set(FULLSCREEN_KEY, on ? "1" : "");
    paintFullscreen();
  };
  paintFullscreen();
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
    } else if (e.key === "Escape") {
      if (!quickOpenEl.hidden) closeQuickOpen();
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
  await Promise.all([loadTree(), restoreTabs()]);
  if (tabs.length === 0 && mobileMedia.matches) openDrawer();

  return () => {
    ac.abort();
    clearTimeout(statusTimer);
    clearTimeout(previewTimer);
    clearTimeout(searchTimer);
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

async function createEditor(host, hooks, settings, signal) {
  try {
    return await createCodeMirror(host, hooks, settings, signal);
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

async function createCodeMirror(host, hooks, settings, signal) {
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
  const darkScheme = window.matchMedia("(prefers-color-scheme: dark)");
  const schemeTheme = () => (darkScheme.matches ? theme.oneDark : []);
  let langSeq = 0;

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
    editorView.dispatch({
      effects: [
        tabSizeConf.reconfigure(EditorState.tabSize.of(tabWidth)),
        indentConf.reconfigure(indentUnit.of(unit)),
      ],
    });
  }

  function reportCursor(viewState) {
    const head = viewState.selection.main.head;
    const line = viewState.doc.lineAt(head);
    hooks.onCursor(line.number, head - line.from + 1);
  }

  const baseExtensions = (langExt) => [
    keymap.of([{ key: "Ctrl-o", run: () => true }, { key: "Ctrl-f", run: search.openSearchPanel }]),
    basicSetup,
    keymap.of([indentWithTab]),
    themeConf.of(schemeTheme()),
    langConf.of(langExt),
    tabSizeConf.of(EditorState.tabSize.of(userTabSize)),
    indentConf.of(indentUnit.of("\t")),
    wrapConf.of(settings.line_wrap ? EditorView.lineWrapping : []),
    fontConf.of(fontTheme(settings.font_size)),
    EditorView.updateListener.of((u) => {
      if (u.docChanged) hooks.onChange();
      if (u.docChanged || u.selectionSet) reportCursor(u.state);
    }),
    EditorView.theme({ "&": { height: "100%" }, ".cm-scroller": { overflow: "auto" } }),
  ];

  const editorView = new EditorView({
    parent: host,
    state: EditorState.create({ doc: "", extensions: baseExtensions([]) }),
  });

  darkScheme.addEventListener("change", () => {
    editorView.dispatch({ effects: themeConf.reconfigure(schemeTheme()) });
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
        editorView.dispatch({ effects: wrapConf.reconfigure(value ? EditorView.lineWrapping : []) });
        break;
      case "font_size":
        editorView.dispatch({ effects: fontConf.reconfigure(fontTheme(value)) });
        editorView.requestMeasure();
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
    langFor(filename, editorView.state.doc.line(1).text).then((langExt) => {
      if (seq === langSeq) editorView.dispatch({ effects: langConf.reconfigure(langExt) });
    });
  }

  // Re-measure when the viewport changes (orientation flip resizes the editor
  // box; CodeMirror must re-layout or it paints nothing for the new size).
  window.addEventListener("resize", () => editorView.requestMeasure(), { signal });

  return {
    async createDoc(content, filename) {
      const langExt = await langFor(filename, (content || "").split("\n", 1)[0]);
      const state = EditorState.create({ doc: content, extensions: baseExtensions(langExt) });
      return { state, saved: state.doc };
    },
    showDoc(tab) {
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
      tab.handle.state = editorView.state;
      tab.handle.scrollTop = editorView.scrollDOM.scrollTop;
    },
    valueOf(tab, isActive) {
      return isActive ? editorView.state.doc.toString() : tab.handle.state.doc.toString();
    },
    isClean(tab, isActive) {
      const doc = isActive ? editorView.state.doc : tab.handle.state.doc;
      return doc.eq(tab.handle.saved);
    },
    markSaved(tab, isActive) {
      tab.handle.saved = isActive ? editorView.state.doc : tab.handle.state.doc;
    },
    search() {
      search.openSearchPanel(editorView);
      return true;
    },
    gotoLine() {
      search.gotoLine(editorView);
      return true;
    },
    jumpTo(line) {
      const doc = editorView.state.doc;
      const pos = doc.line(Math.max(1, Math.min(line, doc.lines))).from;
      editorView.dispatch({
        selection: { anchor: pos },
        effects: EditorView.scrollIntoView(pos, { y: "center" }),
      });
      editorView.focus();
      return true;
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
      editorView.dom.style.visibility = on ? "" : "hidden";
    },
    focus() {
      editorView.focus();
    },
    measure() {
      editorView.requestMeasure();
    },
    destroy() {
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
    refreshLanguage() {},
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

// Editor settings are stored per-device in localStorage (font size, wrap and
// indentation depend on the device/screen, so they should not follow the user
// across machines). The stored indentation is the fallback used when the open
// file's .editorconfig does not dictate one.
const EDITOR_SETTINGS_KEY = "dc-editor-settings";

function loadEditorSettings() {
  const def = { tab_size: 4, indent: "tab", line_wrap: false, font_size: 14 };
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
// stored values, then on change applies the value live and persists it.
function setupSettingsUI(root, editor, settings) {
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

function formatSize(bytes) {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KiB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`;
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
