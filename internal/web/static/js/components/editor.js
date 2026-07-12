// Per-project code editor: lazy directory tree (drawer on small screens, drag
// resizable column on wide ones), tabbed CodeMirror 6 buffers with per-tab undo
// history, quick open palette and a markdown preview. CodeMirror is loaded from
// a CDN; if that fails we fall back to a plain <textarea> so viewing/editing
// still works.
import { notifyError } from "@dc/toast";
import { available as dialogAvailable, confirm as confirmDialog, fire as fireDialog, promptText } from "@dc/dialog";
import { csrfHeaders, ensureOk, getJSON, postForm } from "@dc/http";
import * as store from "@dc/store";

const MAX_RESTORED_TABS = 20;
const MAX_SAVED_TREE_DIRS = 200;
const QUICK_OPEN_LIMIT = 100;
const PREVIEW_DEBOUNCE_MS = 500;
const TREE_WIDTH_KEY = "dc-editor-tree-width";

async function init(root) {
  const name = root.dataset.editorName;
  const base = `/projects/${encodeURIComponent(name)}/editor`;
  const tabsKey = `dc-editor-tabs:${name}`;
  const treeKey = `dc-editor-tree:${name}`;

  const bodyEl = root.querySelector(".editor-body");
  const treeEl = root.querySelector("[data-editor-tree]");
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
  const newFileBtn = root.querySelector("[data-editor-new-file]");
  const newFolderBtn = root.querySelector("[data-editor-new-folder]");
  const uploadBtn = root.querySelector("[data-editor-upload]");
  const uploadInput = root.querySelector("[data-editor-upload-input]");
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
    btn.addEventListener("click", () => activateTab(tab.path));
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
    } else {
      const icon = document.createElement("i");
      icon.className = "ti ti-file-unknown fs-1 d-block text-secondary";
      viewerEl.appendChild(icon);
    }
    const meta = document.createElement("div");
    meta.className = "small text-secondary text-break";
    meta.textContent = tab.size ? `${tab.name} · ${formatSize(tab.size)}` : tab.name;
    const dl = document.createElement("a");
    dl.className = "btn btn-sm btn-outline-secondary";
    dl.href = rawUrl(tab.path, true);
    dl.setAttribute("download", tab.name);
    dl.setAttribute("data-no-pe", "");
    dl.innerHTML = '<i class="ti ti-download me-1"></i>Download';
    viewerEl.append(meta, dl);
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
      return { path, name, kind: isImage(name) ? "image" : "binary", size: data.size || 0, dirty: false };
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
    const ren = document.createElement("button");
    ren.type = "button";
    ren.className = "editor-item-ren";
    ren.title = "Rename";
    ren.setAttribute("aria-label", `Rename ${entry.name}`);
    ren.innerHTML = `<i class="ti ti-pencil"></i>`;
    ren.addEventListener("click", (e) => {
      e.stopPropagation();
      renameEntry(entry);
    });
    row.appendChild(ren);
    const del = document.createElement("button");
    del.type = "button";
    del.className = "editor-item-del";
    del.title = "Delete";
    del.setAttribute("aria-label", `Delete ${entry.name}`);
    del.innerHTML = `<i class="ti ti-trash"></i>`;
    del.addEventListener("click", (e) => {
      e.stopPropagation();
      deletePath(entry.path);
    });
    row.appendChild(del);
    return row;
  }

  function dirNode(entry, depth) {
    const wrap = document.createElement("div");
    const row = rowLabel(entry, "ti-chevron-right", depth);
    row.classList.add("editor-dir");
    const children = document.createElement("div");
    children.className = "editor-children";
    children.hidden = true;
    let loaded = false;

    async function setOpen(open) {
      children.hidden = !open;
      row.querySelector(".editor-item-icon").classList.toggle("editor-open", open);
      if (!open) {
        expanded.delete(entry.path);
        persistExpanded();
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
    if (expanded.has(entry.path)) setOpen(true);
    return wrap;
  }

  function fileNode(entry, depth) {
    const row = rowLabel(entry, "ti-file", depth);
    row.classList.add("editor-file");
    row.title = entry.sizeText ? `${entry.path} · ${entry.sizeText}` : entry.path;
    if (entry.path === activePath) row.classList.add("selected");
    row.addEventListener("click", () => {
      setSelected(entry.path, false, row);
      openPath(entry.path);
    });
    return row;
  }

  async function loadTree() {
    selected = null; // rebuilding the DOM drops the highlight; keep state in sync
    treeEl.innerHTML = `<div class="text-secondary small p-3">Loading…</div>`;
    try {
      renderEntries(treeEl, await listDir(""), 0);
      if (activePath) markTreeSelection(activePath);
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
      const oldPath = entry.path;
      const newPath = data.entry.path;
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
      status(`Renamed to ${newPath}`, "ok");
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

  async function copyPath() {
    const tab = activeTab();
    if (!tab) return;
    try {
      await navigator.clipboard.writeText(tab.path);
      status(`Copied ${tab.path}`, "ok");
    } catch {
      status("Clipboard is not available.", "error");
    }
  }

  function startDownload(path) {
    const a = document.createElement("a");
    a.href = rawUrl(path, true);
    a.setAttribute("download", baseName(path));
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
  async function uploadFiles(fileList, dir, { confirmFirst = true } = {}) {
    const files = Array.from(fileList || []);
    if (files.length === 0) return;
    if (!dialogAvailable()) {
      await uploadPlain(files, dir);
      return;
    }
    const where = dir ? `${dir}/` : "project root";
    const content = document.createElement("div");
    const target = document.createElement("div");
    target.className = "text-secondary small mb-3";
    target.innerHTML = `in <code>${escapeHtml(where)}</code>`;
    const list = document.createElement("div");
    list.className = "editor-upload-list";
    files.forEach((file, index) => list.appendChild(uploadItem(file, index)));
    content.append(target, list);
    let uploaded = 0;
    const result = await fireDialog({
      title: files.length === 1 ? "Upload 1 file" : `Upload ${files.length} files`,
      html: content,
      showCancelButton: true,
      confirmButtonText: "Upload",
      cancelButtonText: "Cancel",
      reverseButtons: true,
      showLoaderOnConfirm: true,
      allowOutsideClick: () => !window.Swal.isLoading(),
      didOpen: () => {
        if (!confirmFirst) window.Swal.clickConfirm();
      },
      preConfirm: async () => {
        const results = await runUploads(files, dir, list);
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
  function runUploads(files, dir, list) {
    const jobs = files.map((file, index) => {
      const item = list.querySelector(`[data-file-index="${index}"]`);
      if (item.dataset.done === "true") return Promise.resolve(null);
      delete item.dataset.error;
      const bar = item.querySelector(".progress-bar");
      const outer = item.querySelector(".progress");
      const statusEl = item.querySelector("[data-file-status]");
      statusEl.textContent = "Uploading";
      return uploadOne(file, dir, (e) => {
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

  function uploadOne(file, dir, onProgress) {
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
      form.append("files", file, file.name);
      xhr.send(form);
    });
  }

  function uploadItem(file, index) {
    const item = document.createElement("div");
    item.className = "editor-upload-item";
    item.dataset.fileIndex = String(index);
    const header = document.createElement("div");
    header.className = "d-flex justify-content-between gap-2 small mb-1";
    const nameEl = document.createElement("div");
    nameEl.className = "text-truncate";
    nameEl.textContent = file.name;
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

  let dropHighlight = null;
  function setDropHighlight(el) {
    if (dropHighlight === el) return;
    dropHighlight?.classList.remove("editor-drop");
    dropHighlight = el;
    dropHighlight?.classList.add("editor-drop");
  }

  function wireTreeDrop() {
    treeEl.addEventListener("dragover", (e) => {
      if (!e.dataTransfer?.types?.includes("Files")) return;
      e.preventDefault();
      e.dataTransfer.dropEffect = "copy";
      const row = e.target.closest(".editor-item");
      setDropHighlight(row && row.dataset.dir ? row : treeEl);
    }, { signal });
    treeEl.addEventListener("dragleave", (e) => {
      if (!treeEl.contains(e.relatedTarget)) setDropHighlight(null);
    }, { signal });
    treeEl.addEventListener("drop", (e) => {
      if (!e.dataTransfer?.files?.length) return;
      e.preventDefault();
      const dir = dropDirFor(e.target);
      setDropHighlight(null);
      uploadFiles(e.dataTransfer.files, dir, { confirmFirst: false });
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
  copyPathItem.addEventListener("click", copyPath, { signal });
  downloadItem.addEventListener("click", () => {
    const tab = activeTab();
    if (tab) startDownload(tab.path);
  }, { signal });
  refreshBtn.addEventListener("click", loadTree, { signal });
  newFileBtn.addEventListener("click", createFile, { signal });
  newFolderBtn.addEventListener("click", createFolder, { signal });
  uploadBtn.addEventListener("click", () => uploadInput.click(), { signal });
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
  wireTreeDrop();
  wireSplitter();
  wireQuickOpen();

  document.addEventListener("keydown", (e) => {
    if ((e.metaKey || e.ctrlKey) && !e.shiftKey && !e.altKey && e.key.toLowerCase() === "s") {
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
    keymap.of([{ key: "Ctrl-o", run: () => true }]),
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

  async function langFor(filename) {
    const ext = (filename.split(".").pop() || "").toLowerCase();
    const spec = LANGS[ext];
    if (!spec) return [];
    try {
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
    langFor(filename).then((langExt) => {
      if (seq === langSeq) editorView.dispatch({ effects: langConf.reconfigure(langExt) });
    });
  }

  // Re-measure when the viewport changes (orientation flip resizes the editor
  // box; CodeMirror must re-layout or it paints nothing for the new size).
  window.addEventListener("resize", () => editorView.requestMeasure(), { signal });

  return {
    async createDoc(content, filename) {
      const langExt = await langFor(filename);
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
      // Force a layout pass in case the editor mounted at zero size.
      editorView.requestMeasure();
      requestAnimationFrame(() => editorView.requestMeasure());
    },
    captureDoc(tab) {
      tab.handle.state = editorView.state;
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
      reportCursor();
    },
    captureDoc(tab) {
      tab.handle.value = ta.value;
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
