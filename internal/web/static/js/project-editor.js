// Per-project code editor: lazy directory tree on the left, CodeMirror 6 editor
// on the right. CodeMirror is loaded from a CDN; if that fails we fall back to a
// plain <textarea> so viewing/editing still works.

async function init(root) {
  const name = root.dataset.editorName;
  const csrf = root.dataset.editorCsrf;
  const maxKiB = root.dataset.editorMaxKib;
  const base = `/projects/${encodeURIComponent(name)}/editor`;

  const treeEl = root.querySelector("[data-editor-tree]");
  const surfaceEl = root.querySelector("[data-editor-surface]");
  const currentEl = root.querySelector("[data-editor-current]");
  const dirtyEl = root.querySelector("[data-editor-dirty]");
  const statusEl = root.querySelector("[data-editor-status]");
  const saveBtn = root.querySelector("[data-editor-save]");
  const deleteBtn = root.querySelector("[data-editor-delete]");
  const refreshBtn = root.querySelector("[data-editor-refresh]");
  const newFileBtn = root.querySelector("[data-editor-new-file]");
  const newFolderBtn = root.querySelector("[data-editor-new-folder]");

  const editorSettings = loadEditorSettings();

  const editor = await createEditor(surfaceEl, onDirty, editorSettings);
  setupSettingsUI(root, editor, editorSettings);

  let current = null; // { path, name }
  let dirty = false;
  let selected = null; // { path, isDir } — the tree row used as the "create in" target
  const expanded = new Set(); // dir paths kept open so a rebuild doesn't collapse the tree

  function setSelected(path, isDir, rowEl) {
    selected = { path, isDir };
    treeEl.querySelectorAll(".editor-item.selected").forEach((el) => el.classList.remove("selected"));
    if (rowEl) rowEl.classList.add("selected");
  }

  // targetDir is the folder new items are created in: the selected folder, or
  // the parent of the selected file, or "" for the project root.
  function targetDir() {
    if (!selected) return "";
    if (selected.isDir) return selected.path;
    const i = selected.path.lastIndexOf("/");
    return i >= 0 ? selected.path.slice(0, i) : "";
  }

  function status(msg, kind) {
    // Errors go to the global toast; the status line stays for transient
    // progress/success ("Saving…", "Saved X").
    if (kind === "error") {
      statusEl.textContent = "";
      statusEl.classList.remove("text-danger", "text-success");
      if (window.notifyError) window.notifyError(msg);
      else {
        statusEl.textContent = msg || "";
        statusEl.classList.add("text-danger");
      }
      return;
    }
    statusEl.textContent = msg || "";
    statusEl.classList.remove("text-danger");
    statusEl.classList.toggle("text-success", kind === "ok");
  }

  function setDirty(on) {
    dirty = on;
    dirtyEl.hidden = !on;
    saveBtn.disabled = !on || !current;
  }
  function onDirty() {
    if (current) setDirty(true);
  }

  // ---- tree ----------------------------------------------------------------

  // Reject with the server's message (JSON {error} or text body, via the shared
  // errorText helper) whenever a response is not ok. Always call this before
  // res.json(): error responses may be plain text (e.g. a 401 "session expired")
  // and would otherwise throw a raw "not valid JSON" SyntaxError at the user.
  async function ensureOk(res, fallback) {
    if (res.ok) return;
    throw new Error(window.errorText ? await window.errorText(res, fallback) : fallback);
  }

  async function listDir(path) {
    const res = await fetch(`${base}/list?path=${encodeURIComponent(path)}`, {
      headers: { Accept: "application/json" },
    });
    await ensureOk(res, "Failed to load files.");
    const data = await res.json();
    return data.entries || [];
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
    row.innerHTML = `<i class="ti ${icon} editor-item-icon"></i><span class="editor-item-name text-truncate">${escapeHtml(entry.name)}</span>`;
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
        return;
      }
      expanded.add(entry.path);
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
    row.dataset.path = entry.path;
    row.title = entry.sizeText ? `${entry.path} · ${entry.sizeText}` : entry.path;
    row.addEventListener("click", () => {
      setSelected(entry.path, false, row);
      openFile(entry);
    });
    return row;
  }

  async function loadTree() {
    selected = null; // rebuilding the DOM drops the highlight; keep state in sync
    treeEl.innerHTML = `<div class="text-secondary small p-3">Loading…</div>`;
    try {
      renderEntries(treeEl, await listDir(""), 0);
    } catch (err) {
      treeEl.innerHTML = `<div class="text-danger small p-3">${escapeHtml(err.message)}</div>`;
    }
  }

  // ---- file actions --------------------------------------------------------

  async function openFile(entry) {
    if (dirty && !confirm("Discard unsaved changes?")) return;
    status("Loading…");
    try {
      const res = await fetch(`${base}/file?path=${encodeURIComponent(entry.path)}`, {
        headers: { Accept: "application/json" },
      });
      await ensureOk(res, "Failed to open file.");
      const data = await res.json();
      current = { path: entry.path, name: entry.name };
      await editor.setValue(data.content, entry.name);
      currentEl.textContent = entry.path;
      currentEl.title = entry.path;
      deleteBtn.disabled = false;
      setDirty(false);
      status("");
    } catch (err) {
      status(err.message, "error");
    }
  }

  async function save() {
    if (!current || !dirty) return;
    status("Saving…");
    saveBtn.disabled = true;
    try {
      const body = new URLSearchParams();
      body.set("path", current.path);
      body.set("content", editor.getValue());
      const res = await fetch(`${base}/file`, {
        method: "POST",
        headers: { "X-CSRF-Token": csrf, "Content-Type": "application/x-www-form-urlencoded", Accept: "application/json" },
        body: body.toString(),
      });
      await ensureOk(res, "Failed to save file.");
      setDirty(false);
      status(`Saved ${current.path}`, "ok");
    } catch (err) {
      saveBtn.disabled = false;
      status(err.message, "error");
    }
  }

  function remove() {
    if (current) deletePath(current.path);
  }

  const postForm = (url, fields) =>
    fetch(url, {
      method: "POST",
      headers: { "X-CSRF-Token": csrf, "Content-Type": "application/x-www-form-urlencoded", Accept: "application/json" },
      body: new URLSearchParams(fields).toString(),
    });

  async function deletePath(targetPath) {
    if (!(await confirmDelete(targetPath))) return;
    status("Deleting…");
    try {
      const res = await postForm(`${base}/delete`, { path: targetPath });
      await ensureOk(res, "Failed to delete.");
      const data = await res.json();
      // Clear the editor if the open file was deleted (directly or via its folder).
      if (current && (current.path === targetPath || current.path.startsWith(targetPath + "/"))) {
        clearEditor();
        current = null;
      }
      // Drop the deleted dir (and its descendants) from the kept-open set.
      for (const p of [...expanded]) {
        if (p === targetPath || p.startsWith(targetPath + "/")) expanded.delete(p);
      }
      status(`Deleted ${data.entry ? data.entry.path : targetPath}`, "ok");
      await loadTree();
    } catch (err) {
      status(err.message, "error");
    }
  }

  async function createFile() {
    const dir = targetDir();
    const name = await promptName("file", dir);
    if (!name) return;
    const path = dir ? `${dir}/${name}` : name;
    status("Creating…");
    try {
      const res = await postForm(`${base}/create`, { path });
      await ensureOk(res, "Failed to create file.");
      const data = await res.json();
      await loadTree();
      if (data.entry) await openFile({ path: data.entry.path, name: data.entry.name });
      status(`Created ${data.entry ? data.entry.path : path}`, "ok");
    } catch (err) {
      status(err.message, "error");
    }
  }

  async function createFolder() {
    const dir = targetDir();
    const name = await promptName("folder", dir);
    if (!name) return;
    const path = dir ? `${dir}/${name}` : name;
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

  function clearEditor() {
    editor.clear();
    currentEl.textContent = "No file selected";
    currentEl.removeAttribute("title");
    setDirty(false);
    deleteBtn.disabled = true;
    saveBtn.disabled = true;
  }

  // ---- wiring --------------------------------------------------------------

  saveBtn.addEventListener("click", save);
  deleteBtn.addEventListener("click", remove);
  refreshBtn.addEventListener("click", loadTree);
  newFileBtn.addEventListener("click", createFile);
  newFolderBtn.addEventListener("click", createFolder);
  document.addEventListener("keydown", (e) => {
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "s") {
      e.preventDefault();
      save();
    }
  });
  window.addEventListener("beforeunload", (e) => {
    if (dirty) {
      e.preventDefault();
      e.returnValue = "";
    }
  });

  status(`Files up to ${maxKiB} KiB can be edited.`);
  await loadTree();
}

// ---- editor (CodeMirror 6 with textarea fallback) --------------------------

async function createEditor(host, onChange, settings) {
  try {
    return await createCodeMirror(host, onChange, settings);
  } catch (err) {
    console.warn("CodeMirror unavailable, using textarea", err);
    return createTextarea(host, onChange, settings);
  }
}

// indentString maps an indent setting to the literal unit CodeMirror inserts.
function indentString(indent) {
  return { tab: "\t", "2spaces": "  ", "4spaces": "    " }[indent] || "\t";
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

async function createCodeMirror(host, onChange, settings) {
  const [cm, state, view, commands, language, theme] = await Promise.all([
    import("codemirror"),
    import("@codemirror/state"),
    import("@codemirror/view"),
    import("@codemirror/commands"),
    import("@codemirror/language"),
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

  const fontTheme = (px) => EditorView.theme({ "&": { fontSize: `${px}px` } });

  host.innerHTML = "";
  const editorView = new EditorView({
    parent: host,
    state: EditorState.create({
      doc: "",
      extensions: [
        basicSetup,
        keymap.of([indentWithTab]),
        theme.oneDark,
        langConf.of([]),
        tabSizeConf.of(EditorState.tabSize.of(settings.tab_size)),
        indentConf.of(indentUnit.of(indentString(settings.indent))),
        wrapConf.of(settings.line_wrap ? EditorView.lineWrapping : []),
        fontConf.of(fontTheme(settings.font_size)),
        EditorView.updateListener.of((u) => {
          if (u.docChanged) onChange();
        }),
        EditorView.theme({ "&": { height: "100%" }, ".cm-scroller": { overflow: "auto" } }),
      ],
    }),
  });

  function applySetting(key, value) {
    switch (key) {
      case "tab_size":
        editorView.dispatch({ effects: tabSizeConf.reconfigure(EditorState.tabSize.of(value)) });
        break;
      case "indent":
        editorView.dispatch({ effects: indentConf.reconfigure(indentUnit.of(indentString(value))) });
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

  // Re-measure when the viewport changes (orientation flip resizes the editor
  // box; CodeMirror must re-layout or it paints nothing for the new size).
  window.addEventListener("resize", () => editorView.requestMeasure());

  return {
    async setValue(text, filename) {
      editorView.dispatch({ changes: { from: 0, to: editorView.state.doc.length, insert: text } });
      editorView.dispatch({ effects: langConf.reconfigure(await langFor(filename)) });
      // Force a layout pass in case the editor mounted at zero size.
      editorView.requestMeasure();
      requestAnimationFrame(() => editorView.requestMeasure());
    },
    getValue() {
      return editorView.state.doc.toString();
    },
    clear() {
      editorView.dispatch({ changes: { from: 0, to: editorView.state.doc.length, insert: "" } });
      editorView.dispatch({ effects: langConf.reconfigure([]) });
    },
    applySetting,
  };
}

function createTextarea(host, onChange, settings) {
  host.innerHTML = "";
  const ta = document.createElement("textarea");
  ta.className = "editor-textarea form-control font-monospace";
  ta.spellcheck = false;
  ta.addEventListener("input", onChange);
  host.appendChild(ta);
  const applySetting = (key, value) => {
    if (key === "tab_size") ta.style.tabSize = String(value);
    else if (key === "font_size") ta.style.fontSize = `${value}px`;
    else if (key === "line_wrap") ta.style.whiteSpace = value ? "pre-wrap" : "pre";
    // "indent" has no good textarea equivalent; ignored in the fallback.
  };
  applySetting("tab_size", settings.tab_size);
  applySetting("font_size", settings.font_size);
  applySetting("line_wrap", settings.line_wrap);
  return {
    async setValue(text) {
      ta.value = text;
    },
    getValue() {
      return ta.value;
    },
    clear() {
      ta.value = "";
    },
    applySetting,
  };
}

// Editor settings are stored per-device in localStorage (font size, wrap and
// indentation depend on the device/screen, so they should not follow the user
// across machines).
const EDITOR_SETTINGS_KEY = "dev-cockpit.editor-settings";

function loadEditorSettings() {
  const def = { tab_size: 4, indent: "tab", line_wrap: false, font_size: 14 };
  let stored = {};
  try {
    stored = JSON.parse(localStorage.getItem(EDITOR_SETTINGS_KEY) || "{}") || {};
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
    localStorage.setItem(EDITOR_SETTINGS_KEY, JSON.stringify(settings));
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

function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}

// promptName asks for just a name; the location (selected folder, or project
// root) is shown so the user never types a path. Returns the trimmed name, or
// null when cancelled/empty.
async function promptName(kind, dir) {
  const where = dir ? `${dir}/` : "project root";
  const title = `New ${kind}`;
  if (window.Swal) {
    const r = await Swal.fire({
      title,
      html: `<div class="text-secondary small mb-2">in <code>${escapeHtml(where)}</code></div>`,
      input: "text",
      inputPlaceholder: kind === "folder" ? "folder name" : "file name",
      showCancelButton: true,
      confirmButtonText: "Create",
      cancelButtonText: "Cancel",
      reverseButtons: true,
      background: "#1f2937",
      color: "#f8fafc",
      inputValidator: (v) => (v && v.trim() ? undefined : "Please enter a name."),
    });
    return r.isConfirmed && r.value ? r.value.trim() : null;
  }
  const v = prompt(`New ${kind} in ${where}`, "");
  return v && v.trim() ? v.trim() : null;
}

function confirmDelete(path) {
  if (window.Swal) {
    return Swal.fire({
      title: `Delete "${path}"?`,
      icon: "warning",
      showCancelButton: true,
      confirmButtonText: "Delete",
      cancelButtonText: "Cancel",
      reverseButtons: true,
      background: "#1f2937",
      color: "#f8fafc",
    }).then((r) => r.isConfirmed);
  }
  return Promise.resolve(confirm(`Delete "${path}"?`));
}

const editorRoot = document.querySelector("[data-editor]");
if (editorRoot) {
  init(editorRoot).catch((err) => {
    console.error("editor init failed", err);
    if (window.notifyError) window.notifyError("Editor failed to load. Reload the page to try again.");
  });
}
