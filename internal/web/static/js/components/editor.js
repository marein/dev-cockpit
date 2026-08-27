// Per-project code editor: lazy directory tree (drawer on small screens, drag
// resizable column on wide ones), tabbed CodeMirror 6 buffers with per-tab undo
// history, quick open palette and a markdown preview. CodeMirror is loaded from
// a CDN; if that fails we fall back to a plain <textarea> so viewing/editing
// still works.
import { notifyError, notifySuccess } from "@dc/toast";
import { onServerEvent } from "@dc/events";
import { focusRow, labelNodes, menuJustClosed, openMenu, rowsOf, stepRowFocus, wireRowMenus } from "@dc/contextmenu";
import { available as dialogAvailable, confirm as confirmDialog, fire as fireDialog, promptText } from "@dc/dialog";
import { applyFold } from "@dc/fold";
import { escapeHtml } from "@dc/dom";
import { DoubleTap } from "@dc/doubletap";
import { matchesTokens } from "@dc/filter";
import { csrfHeaders, ensureOk, getJSON, getText, postForm, postJSON } from "@dc/http";
import { releaseCoder, steerCoder } from "@dc/steer";
import * as dockerApi from "@dc/docker";
import * as editorLSP from "@dc/editor-lsp";
import * as projectSort from "@dc/project-sort";
import * as store from "@dc/store";

const MAX_SAVED_TREE_DIRS = 200;
const PREVIEW_DEBOUNCE_MS = 500;
const DIFF_REV = "HEAD";
const TREE_WIDTH_KEY = "dc-editor-tree-width";
const FULLSCREEN_KEY = "dc-editor-fullscreen";
const TERM_OPEN_KEY = "dc-editor-term-open";
const TERM_HEIGHT_KEY = "dc-editor-term-height";
const TERM_ACTIVE_KEY = "dc-editor-term-active";
// Whether the tree column is folded away on a wide screen. Per device, like the
// column's width: it is about the screen in front of you, not about the project.
const TREE_FOLD_KEY = "dc-editor-tree-folded";
// Whether the commit view groups its list by folder. Per device for the same
// reason, and for every project alike.
const COMMIT_GROUP_KEY = "dc-editor-commit-grouped";
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
  untracked: { rank: 0, cls: "text-cyan", mark: "U", label: "Untracked" },
};
const GIT_MARK_CLASSES = Object.values(GIT_MARKS).map((m) => m.cls);

async function init(root) {
  const name = root.dataset.editorName;
  const base = `/projects/${encodeURIComponent(name)}/editor`;
  const tabsKey = `dc-editor-tabs:${name}`;
  const treeKey = `dc-editor-tree:${name}`;
  const lsp = editorLSP.createClient(base, root.dataset.editorLsp);

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
  const statusEl = root.querySelector("[data-editor-status]");
  const posEl = root.querySelector("[data-editor-pos]");
  const saveBtn = root.querySelector("[data-editor-save]");
  const refreshBtn = root.querySelector("[data-editor-refresh]");
  const uploadInput = root.querySelector("[data-editor-upload-input]");
  const uploadDirInput = root.querySelector("[data-editor-upload-dir-input]");
  const searchProjectItem = root.querySelector("[data-editor-search-project-item]");
  const findItem = root.querySelector("[data-editor-find-item]");
  const gotoItem = root.querySelector("[data-editor-goto]");
  const commentsItem = root.querySelector("[data-editor-comments-item]");
  const commentsCountEl = root.querySelector("[data-editor-comments-count]");
  const commentModalHostEl = root.querySelector("[data-editor-comment-modal-host]");
  const commentModalEl = root.querySelector("[data-editor-comment-modal]");
  const commentModalTitleEl = root.querySelector("[data-editor-comment-title]");
  const commentModalPlaceEl = root.querySelector("[data-editor-comment-place]");
  const commentModalCodeEl = root.querySelector("[data-editor-comment-code]");
  const commentModalTextEl = root.querySelector("[data-editor-comment-text]");
  const commentModalSaveBtn = root.querySelector("[data-editor-comment-save]");
  const lspIndexEl = root.querySelector("[data-editor-lsp-index]");
  const lspIndexLabel = root.querySelector("[data-editor-lsp-index-label]");
  const lspIndexBar = root.querySelector("[data-editor-lsp-index-bar]");
  const readOnlyEl = root.querySelector("[data-editor-readonly]");
  const readOnlyPathEl = root.querySelector("[data-editor-readonly-path]");
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
  const quickOpenRegexBtn = root.querySelector("[data-editor-quickopen-regex]");
  const filesItem = root.querySelector("[data-editor-files-item]");
  const filesCountEl = root.querySelector("[data-editor-files-count]");
  const settingsMenuEl = root.querySelector("[data-editor-settings-menu]");
  const settingsItem = root.querySelector("[data-editor-settings-item]");
  const reindexItem = root.querySelector("[data-editor-reindex-item]");
  const quickOpenItem = root.querySelector("[data-editor-quick-open-item]");
  const sheetEl = root.querySelector("[data-editor-sheet]");
  const sheetPanelEl = root.querySelector("[data-editor-sheet-panel]");
  const sheetTitleEl = root.querySelector("[data-editor-sheet-title]");
  const sheetBodyEl = root.querySelector("[data-editor-sheet-body]");
  const sheetCloseBtn = root.querySelector("[data-editor-sheet-close]");
  const paneColEl = root.querySelector(".editor-pane-col");
  const dockerItem = root.querySelector("[data-editor-docker-item]");
  const dockerMenuEl = root.querySelector("[data-editor-docker-menu]");
  const dockerListEl = root.querySelector("[data-editor-docker-list]");
  const dockerStatusBtn = root.querySelector("[data-editor-docker-status]");
  const dockerStatusText = root.querySelector("[data-editor-docker-status-text]");
  const termStatusBtn = root.querySelector("[data-editor-term-status]");
  const commitEl = root.querySelector("[data-editor-commit]");
  const commitToggleBtn = root.querySelector("[data-editor-commit-toggle]");
  const commitCloseBtn = root.querySelector("[data-editor-commit-close]");
  const commitListEl = root.querySelector("[data-editor-commit-list]");
  const commitAllEl = root.querySelector("[data-editor-commit-all]");
  const commitSummaryEl = root.querySelector("[data-editor-commit-summary]");
  const commitBranchEl = root.querySelector("[data-editor-commit-branch]");
  const commitMsgEl = root.querySelector("[data-editor-commit-message]");
  const commitAmendEl = root.querySelector("[data-editor-commit-amend]");
  const commitBtn = root.querySelector("[data-editor-commit-button]");
  const commitErrorEl = root.querySelector("[data-editor-commit-error]");
  const commitLengthEl = root.querySelector("[data-editor-commit-length]");
  const gitItem = root.querySelector("[data-editor-git-item]");
  const gitItemCount = root.querySelector("[data-editor-git-item-count]");
  const gitStatusBtn = root.querySelector("[data-editor-git-status]");
  const gitBranchEl = root.querySelector("[data-editor-git-branch]");
  const gitAbEl = root.querySelector("[data-editor-git-ab]");
  const gitIconEl = root.querySelector("[data-editor-git-icon]");
  const gitSpinEl = root.querySelector("[data-editor-git-spin]");
  const commitGroupBtn = root.querySelector("[data-editor-commit-group]");
  const commitMoreBtn = root.querySelector("[data-editor-commit-more]");
  const commitPushItem = root.querySelector("[data-editor-commit-push]");

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
  const editor = await createEditor(surfaceEl, { onChange, onCursor, onFocusChange: syncSwipeZone, lspUsable, onLSPClick: goToDefinition, onFindUsages: findUsages, onGoToDefinition: goToDefinitionAtCursor, onDocChanged }, editorSettings, signal, mergeEl);
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
  let gitRetryTimer = 0;
  // Two status requests can be in flight at once (an event and a click on
  // refresh); the one started last is the one that describes the repository
  // now, whichever order the answers arrive in.
  let gitSeq = 0;
  // What the status headers said about where HEAD stands: branch, upstream,
  // ahead and behind. It feeds the statusbar segment and the git sheet.
  let gitBranch = null;
  // Whether the status has answered at all: the git surface renders on the
  // answer, repository or not, never on the guess.
  let gitLoaded = false;
  // One git write at a time: a second push while the first is on the network
  // would only race it. The action key is what lets the row that was tapped
  // carry the spinner while every other one waits disabled.
  let gitBusy = false;
  let gitBusyAction = "";
  // The commit view: the same flat list of changes the marks come from, plus
  // which of them the person picked into the next commit. Nothing starts
  // picked; the picks and the message live on the server per project, so a
  // second device takes the panel over where this one left it.
  let commitOn = false;
  let commitBusy = false;
  let commitInfo = null;
  let commitInfoSeq = 0;
  let commitChanges = [];
  const commitPicked = new Set();
  let commitStash = "";
  let commitDraftTimer = 0;
  let commitDraftSaving = false;
  let commitDraftAt = "";
  let commitDraftSaved = { message: "", paths: "", amend: false, amendMessage: "" };
  let commitGrouped = store.get(COMMIT_GROUP_KEY, "") !== "0";
  const commitMsgKey = `dc-editor-commit-msg:${name}`;
  let comments = [];
  let cursorLine = 1;

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
    cursorLine = line;
    posEl.textContent = `${line}:${col}`;
    syncSwipeZone();
  }

  // ---- code navigation --------------------------------------------------------

  // Real failures toast; lookup outcomes stay statusbar notes, a canceled
  // lookup stays silent.
  const LSP_STATUS_TEXT = {
    "not-installed": "No language server is installed for this file type.",
    disabled: "The language is switched off in the editor settings.",
    busy: "The language server limit is reached, try again in a moment.",
    unavailable: "The language server is not answering right now.",
    error: "The language server failed to answer.",
  };

  // The indicator follows the lsp event, no poll stands behind it; the seq
  // guard keeps a slow older answer from painting over a newer one.
  let lspIndexSeq = 0;
  async function pullLSPIndex() {
    if (!lsp) return;
    const seq = ++lspIndexSeq;
    try {
      const res = await getJSON(`${base}/lsp/status`, { signal });
      if (seq !== lspIndexSeq || signal.aborted) return;
      const indexing = (res.profiles || []).filter((p) => p.indexing);
      renderLSPIndex(indexing[0] || null);
    } catch {}
  }

  function renderLSPIndex(state) {
    if (!state) {
      lspIndexEl.hidden = true;
      return;
    }
    lspIndexLabel.textContent = state.preparing ? `Preparing ${state.label}…` : `Indexing ${state.label}…`;
    const pct = !state.preparing && typeof state.percentage === "number" ? state.percentage : -1;
    lspIndexBar.classList.toggle("progress-bar-indeterminate", pct < 0);
    lspIndexBar.style.width = pct >= 0 ? `${Math.max(2, Math.min(100, pct))}%` : "";
    lspIndexEl.hidden = false;
  }

  let lspAbort = null;

  // A read only tab is a source the server has in its index, so looking up
  // works in it exactly as it does in a file of the project: the path
  // travels absolute and the answer may lead back into the project, into
  // the next dependency, or nowhere.
  function lspUsable() {
    const tab = activeTab();
    return !!(lsp && tab && !tab.kind && !tab.compare && lsp.usable(tab.path));
  }

  function lspNote(msg) {
    status(msg);
    statusTimer = setTimeout(() => {
      if (statusEl.textContent === msg) status("");
    }, 4000);
  }

  // A newer gesture cancels an older one; an answer landing after a tab
  // switch is dropped.
  async function lspCall(kind, pos, note) {
    // The lookup may be what starts the server; the events cover the rest.
    void pullLSPIndex();
    const tab = activeTab();
    if (lspAbort) lspAbort.abort();
    const abort = new AbortController();
    lspAbort = abort;
    status(note);
    try {
      const res = await lsp[kind]({
        path: tab.path,
        content: editor.valueOf(tab, true),
        position: { line: pos.line, character: pos.character },
      }, abort.signal);
      if (abort.signal.aborted || activeTab() !== tab) return null;
      if (!res.available) {
        lsp.note(tab.path, res.status);
        const text = LSP_STATUS_TEXT[res.status];
        if (text) status(text, "error");
        else status("");
        return null;
      }
      return res;
    } catch (err) {
      if (!abort.signal.aborted) status(err.message, "error");
      return null;
    } finally {
      if (lspAbort === abort) lspAbort = null;
    }
  }

  async function goToDefinition(pos) {
    if (!lspUsable()) return;
    const res = await lspCall("definition", pos, "Looking up the definition…");
    if (!res) return;
    await applyDefinition(res, pos);
  }

  // On the declaration itself the usages take the jump's place.
  async function applyDefinition(res, pos) {
    if (res.declaration) {
      await findUsages(pos);
      return;
    }
    const locs = res.locations || [];
    if (locs.length === 0) {
      lspNote(res.outside ? "The definition is outside the project." : "No definition found.");
      return;
    }
    status("");
    if (locs.length === 1) {
      await goToLocation(locs[0]);
      return;
    }
    openUsages(pos.word || "definition", locs, res);
  }

  async function goToDefinitionAtCursor() {
    if (!lspUsable() || !editor.lspPosition) return;
    const pos = editor.lspPosition();
    if (!pos.word) {
      lspNote("Place the cursor on a symbol first.");
      return;
    }
    await goToDefinition(pos);
  }

  async function findUsages(at) {
    if (!lspUsable() || !editor.lspPosition) return;
    const pos = at || editor.lspPosition();
    if (!pos.word) {
      lspNote("Place the cursor on a symbol first.");
      return;
    }
    const res = await lspCall("references", pos, `Finding usages of "${pos.word}"…`);
    if (!res) return;
    const locs = res.locations || [];
    if (locs.length === 0) {
      lspNote(res.outside ? "Every usage is outside the project." : "No usages found.");
      return;
    }
    status("");
    openUsages(pos.word, locs, res);
  }

  async function goToLocation(loc) {
    if (activePath !== loc.path) {
      if (loc.external) await openExternal(loc.path);
      else await openPath(loc.path);
    }
    const tab = activeTab();
    if (tab && tab.path === loc.path && !tab.kind) editor.jumpTo(loc.line, loc.character);
  }

  // A target the language server answered from outside the project: a
  // dependency it downloaded, the standard library, one of its stubs. It
  // opens as a tab like any file and is read only, because nothing in
  // there is this project's to change and a save would have nowhere to go.
  // Its path is absolute, which no path of the project ever is, so the one
  // tab list holds both without a second key.
  async function openExternal(path) {
    closeDrawer();
    if (tabByPath(path)) {
      activateTab(path);
      return;
    }
    if (opening.has(path)) return;
    opening.add(path);
    status("Loading…");
    try {
      const data = await lsp.source(path, signal);
      if (signal.aborted || tabByPath(path)) return;
      tabs.push(await externalTabFor(path, data));
      activateTab(path);
      status("");
    } catch (err) {
      status(err.message, "error");
    } finally {
      opening.delete(path);
    }
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
    // A file from outside the project says so where the git mark of a file
    // of the project stands, and for the same reason: what a tab is has to
    // be readable on the tab.
    if (tab.external) {
      const lockEl = document.createElement("i");
      lockEl.className = "ti ti-lock small flex-shrink-0";
      lockEl.setAttribute("aria-hidden", "true");
      btn.appendChild(lockEl);
      btn.title = `${tab.path} · read only`;
    }
    const nameEl = document.createElement("span");
    nameEl.className = "editor-tab-name";
    nameEl.textContent = tab.name;
    btn.appendChild(nameEl);
    // The hint tells two tabs of one name apart, and it lives on the room
    // the name leaves, which on a tab is next to nothing. Where a file
    // comes from is therefore the statusbar's job for a file outside the
    // project, see syncReadOnly.
    if (tabs.some((t) => t !== tab && t.name === tab.name)) {
      const hintEl = document.createElement("span");
      hintEl.className = "editor-tab-hint";
      hintEl.textContent = tab.external ? externalHint(tab.path) : parentDir(tab.path) || "/";
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
    // A path starting with a separator is not a path of this project, and
    // git has nothing to say about it: a comparison's synthetic path and
    // the absolute one of a file outside the project. Their tooltip is set
    // where the tab is built and is left alone here.
    if (btn.dataset.path.startsWith("/")) return;
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
  // Where Escape leads inside a drilled sheet level: one step back, not all
  // the way out. Set by the level that has a Back, cleared with the sheet.
  let sheetBack = null;
  let sheetAdopted = [];
  let sheetDrag = null;
  const sheetDragging = () => !!(sheetDrag && sheetDrag.active);

  // A sheet is a list of rows, whichever sheet it is: the action rows the
  // docker and git sheets are built of, and the list rows of the open files,
  // the history and the pickers. The arrow keys walk them like the context
  // menu's, which is where the movement itself lives.
  const SHEET_ROW = ".dropdown-item, .editor-sheet-open";
  const sheetRows = () => rowsOf(sheetBodyEl, SHEET_ROW);
  const typingTarget = (node) => node instanceof Element
    && (node.isContentEditable || !!node.closest("input, textarea, select"));
  // Where the keyboard stands while a repaint takes the row away, remembered
  // as a position inside the very list that is being replaced: a git write
  // disables every action row at once, so the focus falls to the body and the
  // position is all there is to come back to. It is kept per list and not per
  // sheet, because the git sheet holds two of them and a position in the
  // actions means nothing in the history below them.
  let sheetFocus = { host: null, index: -1 };

  function focusSheetRow(index) {
    const rows = sheetRows();
    if (!rows.length) return;
    focusRow(sheetBodyEl, rows[Math.max(0, Math.min(index, rows.length - 1))]);
  }

  // sheetArrow walks the open sheet's rows. The arrows belong to the sheet
  // while one stands, and to whatever is typed in wherever that is: the
  // pickers' own filter walks their list from the field, and the editor, the
  // commit message and every select keep the arrows they always had.
  function sheetArrow(e) {
    const step = e.key === "ArrowDown" ? 1 : e.key === "ArrowUp" ? -1 : 0;
    if (!step || e.ctrlKey || e.altKey || e.metaKey || e.shiftKey || typingTarget(e.target)) return;
    e.preventDefault();
    stepRowFocus(sheetBodyEl, step, SHEET_ROW);
  }

  // Escape inside a sheet is one step back, out of a drilled level or out of
  // the sheet.
  function sheetEscape() {
    if (sheetBack) sheetBack();
    else closeSheet();
  }

  // keepSheetFocus puts the keyboard back on a position of one row list, and
  // only when the list it belongs to lost it.
  function keepSheetFocus(host, index) {
    const rows = rowsOf(host, SHEET_ROW);
    if (!rows.length || rows.includes(document.activeElement)) return;
    focusRow(sheetBodyEl, rows[Math.min(index, rows.length - 1)]);
  }

  // A sheet opens with its first row focused, so the arrows have somewhere to
  // start; force is what a level change inside a sheet passes, where the focus
  // stands on the row that led there and belongs back on top. What already
  // holds the focus is left alone, the pickers' filter and the git sheet's
  // actions while its history arrives, and that is the body alone: the close
  // button in the head keeps the focus a click on it left behind, and a sheet
  // opened after that one would find itself already focused. Only where there
  // is a keyboard: on a touch screen the focus ring is noise, the same reason
  // the pickers focus their filter only on a fine pointer.
  function focusSheetTop({ force = false } = {}) {
    if (!pointerMedia.matches) return;
    if (!force && sheetBodyEl.contains(document.activeElement)) return;
    focusSheetRow(0);
  }

  // repaintSheet is what every repaint of a row list runs through: the rows are
  // replaced and the focus falls to the body with them, so the position taken
  // before the paint is what the row landing there gets back, the last row when
  // the list got shorter. A repaint that leaves no row to focus at all (a git
  // write disables them while it runs) keeps the position for the repaint that
  // ends the write. A focus outside the repainted list is left alone.
  function repaintSheet(host, paint) {
    const before = rowsOf(host, SHEET_ROW);
    let index = before.indexOf(document.activeElement);
    if (index < 0 && sheetFocus.host === host) index = sheetFocus.index;
    if (index < 0) {
      paint();
      return;
    }
    sheetFocus = { host, index };
    paint();
    keepSheetFocus(host, index);
  }

  document.addEventListener("focusin", (e) => {
    if (!sheetFocus.host) return;
    const index = rowsOf(sheetFocus.host, SHEET_ROW).indexOf(e.target);
    if (index < 0) sheetFocus = { host: null, index: -1 };
    else sheetFocus.index = index;
  }, { signal });

  function adoptIntoSheet(node) {
    if (!node) return;
    sheetAdopted.push({ node, parent: node.parentNode, next: node.nextSibling });
    sheetBodyEl.appendChild(node);
  }

  function openSheet(kind, title) {
    if (sheetKind) closeSheet();
    sheetKind = kind;
    sheetFocus = { host: null, index: -1 };
    sheetTitleEl.textContent = title;
    sheetPanelEl.setAttribute("aria-label", title);
    sheetBodyEl.replaceChildren();
    sheetEl.hidden = false;
  }

  function closeSheet() {
    if (!sheetKind) return;
    for (const item of sheetAdopted.reverse()) item.parent.insertBefore(item.node, item.next);
    sheetAdopted = [];
    sheetBack = null;
    sheetFocus = { host: null, index: -1 };
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
    const kind = tab.compare || tab.kind || tab.external ? undefined : fileKind(tab.path);
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
    dirEl.textContent = tab.compare ? tab.compare.left : tab.external ? externalHint(tab.path) : parentDir(tab.path) || "/";
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
    repaintSheet(sheetBodyEl, paintFilesSheet);
  }

  function paintFilesSheet() {
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
    focusSheetTop();
  }

  function openSettingsSheet() {
    openSheet("settings", "Editor settings");
    adoptIntoSheet(settingsMenuEl);
  }

  let dockerData = null;
  let dockerSeq = 0;

  async function loadDocker() {
    const seq = ++dockerSeq;
    try {
      const res = await fetch(`${base}/docker`, { credentials: "same-origin", signal });
      if (!res.ok) return;
      const data = await res.json();
      if (seq !== dockerSeq) return;
      dockerData = data;
      paintDocker();
      if (sheetKind === "docker") renderDockerSheet();
    } catch {}
  }

  function paintDocker() {
    const data = dockerData;
    const show = !!(data && data.available && (data.containers.length || data.stacks.length));
    dockerItem.hidden = !show;
    dockerStatusBtn.hidden = !show;
    if (!show) {
      if (sheetKind === "docker") closeSheet();
      return;
    }
    const running = data.containers.filter((c) => c.running).length;
    const unwell = data.containers.some((c) => c.unwell);
    const working = (data.stacks || []).some((s) => s.busy || s.run?.running);
    dockerStatusText.textContent = data.containers.length ? `${running}/${data.containers.length}` : "";
    dockerStatusBtn.title = working
      ? "Docker: a compose command is running"
      : data.containers.length
        ? `Docker: ${running} of ${data.containers.length} running`
        : "Docker: nothing running";
    const icon = dockerStatusBtn.querySelector("i");
    icon.classList.toggle("text-danger", unwell);
    icon.classList.toggle("text-success", !unwell && running > 0);
    icon.classList.toggle("dc-docker-working", working);
  }

  // sheetActionRow is one action line of a sheet, the shape the docker and
  // git sheets share: an icon, a label that may keep its tail, a quiet second
  // line, and one click.
  function sheetActionRow({ icon, iconClass, label, title, sub, subNodes, disabled, busy, onClick }) {
    const row = document.createElement("button");
    row.type = "button";
    row.className = "dropdown-item d-flex align-items-center gap-2";
    if (disabled) row.disabled = true;
    if (title) row.title = title;
    // While this row's action runs, the spinner stands where its icon stood:
    // the busy state lives at the control that was tapped. It is sized to the
    // icon's 1em box, spinner-border-sm's fixed 1rem is wider at this font
    // size and pushed the label to the right.
    if (busy) {
      const spin = document.createElement("span");
      spin.className = "spinner-border spinner-border-sm flex-shrink-0";
      spin.style.width = "1em";
      spin.style.height = "1em";
      spin.setAttribute("aria-hidden", "true");
      row.appendChild(spin);
    } else {
      const mark = document.createElement("i");
      mark.className = `ti ${icon}${iconClass ? ` ${iconClass}` : ""}`;
      mark.setAttribute("aria-hidden", "true");
      row.appendChild(mark);
    }
    const col = document.createElement("span");
    col.className = "d-flex flex-column min-w-0 text-start";
    const nameEl = document.createElement("span");
    // A label may carry a head and a tail (an address keeps its end when it
    // does not fit), which is what @dc/contextmenu renders in a menu.
    nameEl.className = "d-flex min-w-0";
    nameEl.append(...labelNodes(label));
    col.appendChild(nameEl);
    if (subNodes) {
      const subEl = document.createElement("span");
      subEl.className = "small text-secondary d-flex align-items-center gap-1 min-w-0";
      subEl.append(...subNodes);
      col.appendChild(subEl);
    } else if (sub) {
      const subEl = document.createElement("span");
      subEl.className = "small text-secondary text-truncate";
      subEl.textContent = sub;
      col.appendChild(subEl);
    }
    row.appendChild(col);
    if (onClick) row.addEventListener("click", onClick, { signal });
    return row;
  }

  // paintDockerItems draws one list of @dc/docker entries into the sheet. It
  // is called with the project's own list and again with a container's
  // addresses when one of its entries drills in.
  function paintDockerItems(items) {
    for (const item of items) {
      if (item.divider) {
        const divider = document.createElement("div");
        divider.className = "dropdown-divider";
        dockerListEl.appendChild(divider);
        continue;
      }
      dockerListEl.appendChild(sheetActionRow({
        icon: item.icon || "ti-brand-docker",
        iconClass: item.iconClass,
        label: item.label,
        title: item.title,
        disabled: item.disabled,
        onClick: item.action,
      }));
    }
  }

  function renderDockerSheet() {
    repaintSheet(dockerListEl, paintDockerSheet);
  }

  function paintDockerSheet() {
    const data = dockerData;
    if (!data) return;
    dockerListEl.replaceChildren();
    // The project's own entries first: which container to reach, its logs, its
    // compose actions. Same list as the projects page builds, from @dc/docker,
    // and a container with several addresses drills into them here too: the
    // sheet then shows that one list, its first row leading back.
    const containers = data.containers;
    const drill = (items) => {
      if (!items) {
        renderDockerSheet();
        focusSheetTop({ force: true });
        return;
      }
      dockerListEl.replaceChildren();
      paintDockerItems(items);
      focusSheetTop({ force: true });
    };
    const items = data.cli
      ? dockerApi.projectMenuItems({
        project: name,
        stacks: data.stacks,
        containers,
        actions: data.actions || [],
        onLogs: (stack) => void composeLogsFromEditor(stack),
        onDrill: drill,
      })
      : dockerApi.projectMenuItems({ project: name, containers, onDrill: drill });
    paintDockerItems(items);
    if (items.length && data.containers.length) {
      const divider = document.createElement("div");
      divider.className = "dropdown-divider";
      dockerListEl.appendChild(divider);
    }
    // The containers stand next to each other, as many per line as the width
    // allows: a project with a dozen of them is a list nobody scrolls, and each
    // one is a name and a menu, not a paragraph. What a single one can do lives
    // in its menu, so no row carries buttons of its own.
    if (data.containers.length) {
      const grid = document.createElement("div");
      grid.className = "row row-deck g-0";
      for (const container of data.containers) {
        const iconClass = container.unwell ? "text-danger" : container.running ? "text-success" : "text-secondary";
        const cell = sheetActionRow({
          icon: "ti-brand-docker",
          iconClass,
          label: container.name,
          sub: container.portsLabel,
          onClick: (event) => {
            const info = { ...container, cli: data.cli };
            const menu = dockerApi.containerMenuItems(info, { onShell: dockerShellFromEditor });
            // A row reached with the keyboard clicks at no point at all, so the
            // row itself is the anchor then; at 0,0 the menu would sit in the
            // screen corner and read as not opening.
            const rect = event.currentTarget.getBoundingClientRect();
            const x = event.clientX || rect.left;
            const y = event.clientY || rect.bottom + 4;
            openMenu({ x: Math.round(x), y: Math.round(y), items: menu, signal });
          },
        });
        cell.dataset.dockerContainer = "";
        const col = document.createElement("div");
        col.className = "col-12 col-sm-6 col-lg-4";
        col.appendChild(cell);
        grid.appendChild(col);
      }
      dockerListEl.appendChild(grid);
    }
    if (!data.containers.length && !data.stacks.length) {
      dockerListEl.appendChild(sheetActionRow({ icon: "ti-brand-docker", label: "No containers.", disabled: true }));
    }
  }

  // The stack's logs are a terminal like a container's, so they land in the
  // editor's own panel on a desktop and on the shell page otherwise.
  async function composeLogsFromEditor(stack) {
    const data = await dockerApi.composeLogs(name, stack.label, stack.label || name);
    if (!data) return;
    await openShellFromEditor(data);
  }

  async function dockerShellFromEditor(info, kind) {
    const data = await dockerApi.openShell(info.id, kind, info.name);
    if (!data) return;
    await openShellFromEditor(data);
  }

  async function openShellFromEditor(data) {
    if (termApplies()) {
      closeSheet();
      termActiveId = data.id;
      if (!termOpen) await openTermPanel({ focus: true });
      else await loadTerminals({ focus: true });
      return;
    }
    if (data.url) dockerApi.navigate(data.url);
  }

  function openDockerSheet() {
    openSheet("docker", "Docker");
    adoptIntoSheet(dockerMenuEl);
    renderDockerSheet();
    focusSheetTop();
    void loadDocker();
  }

  function updateActionStates() {
    const tab = activeTab();
    const textTab = tab && !tab.kind ? tab : null;
    // A comparison has two buffers and two save buttons of its own, so the one
    // in the toolbar has nothing to act on, and a file from outside the
    // project has no save at all.
    const fileTab = textTab && !textTab.compare && !textTab.external ? textTab : null;
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
    void applyChangeBars();
    placeholderEl.hidden = !!tab;
    // A comparison stands for two files, so the line below names both.
    posEl.hidden = !tab || !!tab.kind;
    renderTabs();
    updateActionStates();
    syncSwipeZone();
    syncIndentControl();
    syncPreview();
    syncReadOnly();
    paintComments();
    // A file from outside the project stands in no tree of ours, and its
    // path must not become the folder a new file is created in.
    if (tab && !tab.compare && !tab.external) markTreeSelection(tab.path);
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
    else if (!tab.kind && !tab.external) void applyTabDiff(tab);
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
    if (lsp && !tab.kind && !tab.compare) lsp.closeDocument(tab.path);
    if (tab.dirty && !tab.kind && !tab.compare && !tab.external && commentsFor(path).length) void loadComments();
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
    // can do. A file from outside the project keeps its path on top of
    // that: it is the one thing about it worth taking away, and every
    // other entry here acts on a file this project owns.
    if (tab.compare) return closing;
    if (tab.external) {
      return [...closing, { divider: true }, { label: "Copy path", icon: "ti-copy", action: () => copyPath(tab.path) }];
    }
    return [
      ...closing,
      { divider: true },
      { label: "Copy file", icon: "ti-files", action: () => copyToClipboard(tab.path, false) },
      clipboard ? { label: `Paste "${baseName(clipboard.path)}"`, icon: "ti-clipboard", action: () => void pasteInto(parentDir(tab.path)) } : null,
      { label: "Copy path", icon: "ti-copy", action: () => copyPath(tab.path) },
      { label: "Copy contents", icon: "ti-file-text", action: () => copyContents(tab.path) },
      { label: "Download", icon: "ti-download", action: () => startDownload(tab.path) },
      isArchive(tab.name) ? { label: "Extract here", icon: "ti-file-zip", action: () => void extractArchive(tab.path) } : null,
      { label: "Reveal in tree", icon: "ti-list-tree", hint: "Ctrl+Alt+R", action: () => revealInTree(tab.path) },
      { divider: true },
      previewMenuItem(tab),
      diffMenuItem(tab),
      revDiffMenuItem(tab.path),
      historyMenuItem(tab.path),
      blameMenuItem(tab),
      { label: "Select for compare", icon: "ti-columns-2", action: () => selectForCompare(tab.path) },
      compareSelection && compareSelection !== tab.path ? {
        label: `Compare with "${baseName(compareSelection)}"`,
        icon: "ti-git-compare",
        action: () => void openCompare(compareSelection, tab.path),
      } : null,
      revertMenuItem(tab.path, false),
      { divider: true },
      { label: "Rename", icon: "ti-pencil", hint: "F2", action: () => renameEntry({ path: tab.path, name: tab.name, isDir: false }) },
      { label: "Delete", icon: "ti-trash", danger: true, action: () => deletePath(tab.path) },
    ].filter(Boolean);
  }

  function openTabMenu(path, x, y) {
    const tab = tabByPath(path);
    if (!tab) return;
    openMenu({ x, y, items: tabMenuItems(tab), signal });
  }

  // externalHint cuts an absolute path from the front, never from the end:
  // what says which file this is, the module and its version, stands at
  // the end of it, and that is exactly what an ellipsis on the right would
  // eat. The whole path stays on the tab as its tooltip.
  function externalHint(path) {
    const parts = parentDir(path).split("/").filter(Boolean);
    const tail = parts.slice(-2).join("/");
    return parts.length > 2 ? `…/${tail}` : `/${tail}`;
  }

  // syncReadOnly says on the statusbar that the file in front of you is
  // not yours to change, and where it comes from: the tab shows a name
  // that means nothing on its own, print.go exists a hundred times over,
  // and this is the one place with room for the folder it came from. The
  // whole path is the tooltip, the line only carries its end. This is the
  // one path the statusbar shows, and it is here because it is the only
  // place it can be read at all, unlike a project file's, which the tree
  // and the tab already say.
  function syncReadOnly() {
    const tab = activeTab();
    readOnlyEl.hidden = !(tab && tab.external);
    readOnlyEl.title = tab && tab.external ? tab.path : "";
    readOnlyPathEl.textContent = tab && tab.external ? `${externalHint(tab.path)}/${tab.name}` : "";
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
      if (t.external) return { type: "external", path: t.path };
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
      } else if (e && typeof e === "object" && e.type === "external" && typeof e.path === "string" && e.path) {
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
    const results = await Promise.allSettled(entries.map((entry) => {
      if (entry.type === "compare") return compareTabFor(entry.left, entry.right);
      // A file outside the project comes back through its own route: the
      // one the tree serves would take its absolute path for a relative
      // one and answer about a file inside the project.
      if (entry.type === "external") return lsp ? lsp.source(entry.path, signal).then((data) => externalTabFor(entry.path, data)) : Promise.reject(new Error("no language server"));
      return getJSON(`${base}/file?path=${encodeURIComponent(entry.path)}`, { signal }).then((data) => tabFor(entry.path, data));
    }));
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
      // The version of the bytes this buffer was built from. It rides the save
      // so the write can refuse to land on a file somebody else has written
      // since, and every save answers with the version of what it wrote.
      version: data.version || "",
      dirty: false,
    };
  }

  // externalTabFor builds the read only tab of a file outside the project.
  // It carries no version, and its document refuses every change, so no
  // save path can ever address it: the buffer is what the disk answered
  // and stays that way.
  async function externalTabFor(path, data) {
    const name = baseName(path);
    return {
      path,
      name,
      external: true,
      handle: await editor.createDoc(data.content || "", name, { readOnly: true }),
      editorConfig: {},
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
    if (!entry.isDir) {
      items.push({ label: "Copy contents", icon: "ti-file-text", action: () => copyContents(entry.path) });
    }
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
          hint: "Ctrl+Alt+P",
          action: () => void previewFromTree(entry.path),
        });
      }
      if (gitRepo && editor.canDiff) {
        items.push({
          label: tab && tab.diffRev ? "Hide git diff" : "Show git diff",
          icon: "ti-git-compare",
          hint: "Ctrl+Alt+D",
          action: () => void diffFromTree(entry.path),
        });
      }
      const revItem = revDiffMenuItem(entry.path);
      if (revItem) items.push(revItem);
      const histItem = historyMenuItem(entry.path);
      if (histItem) items.push(histItem);
      if (gitRepo && editor.canBlame) {
        items.push({
          label: tab && tab.blameOn ? "Hide git blame" : "Show git blame",
          icon: "ti-user-code",
          hint: "Ctrl+Alt+B",
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
    // The revert belongs to the git actions, so it closes their group; a
    // folder has no other git entries and the revert stands as that group
    // alone.
    const revertItem = revertMenuItem(entry.path, entry.isDir);
    if (revertItem) {
      if (entry.isDir) items.push({ divider: true });
      items.push(revertItem);
    }
    items.push({ divider: true });
    items.push({
      label: "Rename",
      icon: "ti-pencil",
      hint: entry.isDir ? undefined : "F2",
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
    commitChanges = ((changes && changes.worktree) || []).slice();
    // A picked path that left the list was committed or reverted elsewhere; it
    // leaves the pick too, and the next save writes the pruned draft. Nothing
    // is saved for the pruning alone: a commit on another device clears the
    // stored draft, and a save from here would put the old message back.
    pruneCommitPicked();
    syncCommitUI();
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
    // A fresh round replaces the pending retry of a failed one.
    clearTimeout(gitRetryTimer);
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
      gitLoaded = true;
      gitBranch = gitRepo ? changes.branch || null : null;
      paintGitStatus();
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
      void applyChangeBars();
    } catch (err) {
      if (signal.aborted) return;
      console.warn("git status unavailable", err);
      // A round that failed retries on its own: the event that asked for it is
      // spent and may have been the last one for hours (the same file changing
      // further moves nothing), so waiting for the next one means staying
      // stale. Only the newest round schedules, a newer one replaces it.
      if (seq === gitSeq) {
        gitRetryTimer = setTimeout(() => void loadGitStatus(), 8000);
      }
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
    dropChangeHeads();
    void loadGitStatus();
    void refreshDiffHead();
    void pullCommitDraft();
    renewGitWatchNow();
  }

  // ---- commit ----------------------------------------------------------------

  // The commit view borrows the tree's place in the column: it answers the same
  // question from the other side, what the working copy carries, and on a phone
  // it lives in the same drawer. What it commits is exactly the checked rows,
  // as a pathspec commit, so whatever a coder keeps staged next door stays
  // staged and stays out.

  function commitSelectable() {
    return commitChanges.filter((entry) => gitKind(entry) !== "conflict");
  }

  function commitSelectedPaths() {
    return commitSelectable()
      .filter((entry) => commitPicked.has(entry.path))
      .map((entry) => entry.path);
  }

  // pruneCommitPicked drops picks the changes list no longer holds. Only a
  // status that actually answered may prune: before the first one every list
  // is empty and pruning would eat the picks another device stored.
  function pruneCommitPicked() {
    if (!gitLoaded) return;
    for (const path of [...commitPicked]) {
      if (!commitChanges.some((entry) => entry.path === path)) commitPicked.delete(path);
    }
  }

  // commitDir is the folder a row is grouped under: the parent, with the
  // trailing slash of a directory entry out of the way first.
  function commitDir(entry) {
    return parentDir(entry.path.endsWith("/") ? entry.path.slice(0, -1) : entry.path);
  }

  function commitRow(entry, nested, depth) {
    const kind = gitKind(entry);
    const info = GIT_MARKS[kind];
    const row = document.createElement("div");
    row.className = "editor-commit-row";
    if (nested) {
      row.classList.add("editor-commit-nested");
      row.style.paddingLeft = `${0.5 + (depth || 1) * 1.1}rem`;
    }
    row.dataset.path = entry.path;
    const check = document.createElement("input");
    check.type = "checkbox";
    check.className = "form-check-input m-0 flex-shrink-0";
    check.setAttribute("aria-label", `Include ${entry.path}`);
    if (kind === "conflict") {
      check.disabled = true;
      check.title = "Resolve the conflict first.";
    } else {
      check.checked = commitPicked.has(entry.path);
      check.addEventListener("change", () => {
        if (check.checked) commitPicked.add(entry.path);
        else commitPicked.delete(entry.path);
        // Every folder above carries this file in its subtree, so each of
        // their rows may have to move to or from the mixed state.
        for (let dir = commitDir(entry); dir; dir = parentDir(dir)) syncGroupRow(dir);
        syncCommitControls();
        queueCommitDraft();
      });
    }
    const open = document.createElement("button");
    open.type = "button";
    open.className = "editor-commit-open";
    const isDirEntry = entry.path.endsWith("/");
    const shown = isDirEntry ? entry.path.slice(0, -1) : entry.path;
    const line = document.createElement("div");
    line.className = "editor-commit-line";
    const mark = document.createElement("span");
    mark.className = `small fw-bold flex-shrink-0 ${info.cls}`;
    mark.textContent = info.mark;
    const nameEl = document.createElement("span");
    nameEl.className = `text-truncate ${info.cls}`;
    nameEl.textContent = baseName(shown) + (isDirEntry ? "/" : "");
    line.append(mark, nameEl);
    const numbers = numbersText(entry.path);
    if (numbers) {
      const counts = document.createElement("span");
      counts.className = "small ms-auto ps-2 flex-shrink-0";
      if (entry.binary) {
        counts.classList.add("text-secondary");
        counts.textContent = numbers;
      } else {
        const plus = document.createElement("span");
        plus.className = "text-green";
        plus.textContent = `+${entry.added || 0}`;
        const minus = document.createElement("span");
        minus.className = "text-red ms-1";
        minus.textContent = `−${entry.removed || 0}`;
        counts.append(plus, minus);
      }
      line.append(counts);
    }
    open.append(line);
    // The full path on a line of its own, wrapping instead of being cut: a
    // name alone does not tell two same named files apart. A file at the
    // root would only repeat its name, and under a group row the folder
    // already stands above the file.
    if (!nested && parentDir(shown)) {
      const pathEl = document.createElement("div");
      pathEl.className = "editor-commit-path";
      pathEl.textContent = entry.path;
      open.append(pathEl);
    }
    open.title = [entry.path, info.label, numbers].filter(Boolean).join(" · ");
    // A deleted file is not on the disk and a directory is not a file, so
    // there is nothing to open for either; the row still commits them.
    if (isDirEntry || kind === "deleted" || kind === "conflict") {
      open.disabled = true;
    } else {
      open.addEventListener("click", () => void diffAgainst(entry.path, DIFF_REV));
    }
    row.append(check, open);
    return row;
  }

  // commitSubtree is everything a folder holds, its own files and its
  // subfolders' alike: that is what its checkbox and its count speak about.
  function commitSubtree(dir) {
    return commitChanges.filter((entry) => {
      const d = commitDir(entry);
      return d === dir || d.startsWith(`${dir}/`);
    });
  }

  // A group row is one folder's line over its whole subtree: the checkbox
  // picks and drops everything below it, a mixed pick reads as indeterminate.
  // The folder is only a grouping, what is committed are always the files.
  function commitGroupRow(dir, label, depth) {
    const row = document.createElement("div");
    row.className = "editor-commit-row editor-commit-grouprow";
    row.dataset.dir = dir;
    if (depth) row.style.paddingLeft = `${0.5 + depth * 1.1}rem`;
    const check = document.createElement("input");
    check.type = "checkbox";
    check.className = "form-check-input m-0 flex-shrink-0";
    check.setAttribute("aria-label", `Include everything in ${dir}`);
    check.addEventListener("change", () => {
      for (const entry of commitSubtree(dir)) {
        if (gitKind(entry) === "conflict") continue;
        if (check.checked) commitPicked.add(entry.path);
        else commitPicked.delete(entry.path);
        const fileCheck = commitListEl.querySelector(
          `.editor-commit-row[data-path="${CSS.escape(entry.path)}"] input`);
        if (fileCheck && !fileCheck.disabled) fileCheck.checked = check.checked;
      }
      // A subtree toggle moves rows in both directions, the groups inside it
      // and the ones this folder sits in.
      for (const groupRow of commitListEl.querySelectorAll(".editor-commit-grouprow")) {
        syncGroupRow(groupRow.dataset.dir);
      }
      syncCommitControls();
      queueCommitDraft();
    });
    const line = document.createElement("div");
    line.className = "editor-commit-line";
    const icon = document.createElement("i");
    icon.className = "ti ti-folder flex-shrink-0 text-secondary";
    icon.setAttribute("aria-hidden", "true");
    const nameEl = document.createElement("span");
    nameEl.className = "text-truncate small fw-medium";
    nameEl.textContent = label;
    const count = document.createElement("span");
    count.className = "small text-secondary ms-auto ps-2 flex-shrink-0";
    count.textContent = String(commitSubtree(dir).length);
    line.append(icon, nameEl, count);
    row.title = dir;
    row.append(check, line);
    return row;
  }

  // syncGroupRow reads a folder's pick state back onto its group row, so a
  // single file's checkbox never costs a rebuild of the list.
  function syncGroupRow(dir) {
    if (!dir) return;
    const input = commitListEl.querySelector(
      `.editor-commit-grouprow[data-dir="${CSS.escape(dir)}"] input`);
    if (!input) return;
    const selectable = commitSubtree(dir).filter((entry) => gitKind(entry) !== "conflict");
    const picked = selectable.filter((entry) => commitPicked.has(entry.path));
    input.disabled = selectable.length === 0;
    input.checked = selectable.length > 0 && picked.length === selectable.length;
    input.indeterminate = picked.length > 0 && picked.length < selectable.length;
  }

  function renderCommitList() {
    if (commitChanges.length === 0) {
      const empty = document.createElement("div");
      empty.className = "text-secondary small p-3";
      const line = document.createElement("div");
      line.textContent = "Nothing to commit, the working copy is clean.";
      empty.append(line);
      if (commitInfo && commitInfo.hasCommit && commitInfo.lastMessage) {
        const last = document.createElement("div");
        last.className = "mt-1 text-truncate";
        last.title = commitInfo.lastMessage;
        last.textContent = `Last commit: ${commitInfo.lastMessage.split("\n", 1)[0]}`;
        empty.append(last);
      }
      commitListEl.replaceChildren(empty);
      syncCommitControls();
      return;
    }
    const entries = [...commitChanges].sort((a, b) => a.path.localeCompare(b.path));
    if (!commitGrouped) {
      commitListEl.replaceChildren(...entries.map((entry) => commitRow(entry, false)));
      syncCommitControls();
      return;
    }
    // Grouped by folder, the way an IDE's directory tree reads: folders
    // first and files after them, on every level. A folder becomes a row of
    // its own as soon as it holds more than one thing; a chain of folders
    // that only hands down to a single subfolder and has no files of its own
    // merges into one row with the joined path as its label. A group's
    // checkbox covers its whole subtree, see commitGroupRow.
    const treeTop = { dirs: new Map(), files: [] };
    for (const entry of entries) {
      const dir = commitDir(entry);
      let node = treeTop;
      if (dir) {
        for (const segment of dir.split("/")) {
          if (!node.dirs.has(segment)) node.dirs.set(segment, { dirs: new Map(), files: [] });
          node = node.dirs.get(segment);
        }
      }
      node.files.push(entry);
    }
    const rows = [];
    const emit = (node, prefix, depth) => {
      for (const segment of [...node.dirs.keys()].sort((a, b) => a.localeCompare(b))) {
        let label = segment;
        let child = node.dirs.get(segment);
        while (child.files.length === 0 && child.dirs.size === 1) {
          const [next] = child.dirs.keys();
          label = `${label}/${next}`;
          child = child.dirs.get(next);
        }
        const dir = prefix ? `${prefix}/${label}` : label;
        rows.push(commitGroupRow(dir, label, depth));
        emit(child, dir, depth + 1);
      }
      for (const entry of node.files) {
        rows.push(commitRow(entry, depth > 0, depth));
      }
    };
    emit(treeTop, "", 0);
    commitListEl.replaceChildren(...rows);
    for (const groupRow of commitListEl.querySelectorAll(".editor-commit-grouprow")) {
      syncGroupRow(groupRow.dataset.dir);
    }
    syncCommitControls();
  }

  function syncCommitUI() {
    commitToggleBtn.hidden = !gitRepo;
    gitItem.hidden = !gitSurface();
    gitItemCount.textContent = commitChanges.length ? String(commitChanges.length) : "";
    gitItemCount.hidden = commitChanges.length === 0;
    commitToggleBtn.title = commitChanges.length
      ? `Commit changes (${commitChanges.length})`
      : "Commit changes";
    if (!gitRepo && commitOn) closeCommit();
    if (commitOn) renderCommitList();
    if (sheetKind === "git") renderGitSheet();
  }

  function syncCommitControls() {
    const selectable = commitSelectable();
    const picked = commitSelectedPaths();
    commitSummaryEl.textContent = selectable.length === 0
      ? "No changes"
      : `${picked.length} of ${selectable.length} ${selectable.length === 1 ? "change" : "changes"}`;
    commitAllEl.disabled = selectable.length === 0;
    commitAllEl.checked = selectable.length > 0 && picked.length === selectable.length;
    commitAllEl.indeterminate = picked.length > 0 && picked.length < selectable.length;
    const firstLine = commitMsgEl.value.split("\n", 1)[0] || "";
    commitLengthEl.textContent = firstLine.length > 72 ? String(firstLine.length) : "";
    commitLengthEl.title = firstLine.length > 72 ? "The subject line is longer than 72 characters." : "";
    // An amend commits with nothing picked: it then rewrites the message of
    // the last commit and nothing else, the everyday typo fix.
    commitBtn.disabled = commitBusy
      || gitBusy
      || (picked.length === 0 && !commitAmendEl.checked)
      || commitMsgEl.value.trim() === "";
    commitMoreBtn.disabled = commitBtn.disabled;
    // The running commit spins on its own button, like every git action does.
    const spin = commitBtn.querySelector("[data-editor-commit-spin]");
    const icon = commitBtn.querySelector("[data-editor-commit-icon]");
    if (spin && icon) {
      spin.hidden = !commitBusy;
      icon.hidden = commitBusy;
    }
  }

  // openCommit is "bring the commit view up", whatever stands: a view that is
  // already on but sits in a closed drawer or a folded column still has to
  // become visible, which is what the entry in the git sheet promises.
  function openCommit() {
    if (!gitRepo) return;
    if (!commitOn) {
      commitOn = true;
      treeEl.hidden = true;
      commitEl.hidden = false;
      commitToggleBtn.classList.add("active");
      commitToggleBtn.setAttribute("aria-pressed", "true");
      renderCommitList();
      void loadCommitInfo();
      void pullCommitDraft();
    }
    if (mobileMedia.matches) openDrawer();
    else if (treeFolded) toggleDrawer();
    if (pointerMedia.matches) commitMsgEl.focus();
  }

  function closeCommit() {
    if (!commitOn) return;
    commitOn = false;
    commitEl.hidden = true;
    treeEl.hidden = false;
    commitToggleBtn.classList.remove("active");
    commitToggleBtn.setAttribute("aria-pressed", "false");
  }

  function toggleCommit() {
    if (commitOn) closeCommit();
    else openCommit();
  }

  async function loadCommitInfo() {
    const seq = ++commitInfoSeq;
    try {
      const data = await getJSON(`${base}/git/commit`, { signal });
      if (seq !== commitInfoSeq || signal.aborted) return;
      commitInfo = data;
      commitBranchEl.textContent = data.branch || "?";
      commitAmendEl.disabled = !data.hasCommit;
      commitAmendEl.closest("label").title = data.hasCommit ? "" : "There is no commit to amend yet.";
      if (!data.hasCommit && commitAmendEl.checked) {
        commitAmendEl.checked = false;
        commitMsgEl.value = commitStash;
        queueCommitDraft();
      }
      if (commitOn && commitChanges.length === 0) renderCommitList();
    } catch (err) {
      if (!signal.aborted) status(err.message, "error");
    }
  }

  // The draft is the panel's unsent state, the message and the picks, and it
  // lives on the server per project so another device takes the panel over
  // where this one left it. It follows the assistant composer's pattern: a
  // debounced save is the only write path, a save that changed something is
  // published as the commitdraft event, and a pull never types over unsaved
  // local edits, the newer writer wins by the stored timestamp.

  // commitDraftState is what would be saved right now. While an amend borrows
  // the message field the draft's message stays the stash it displaced, so
  // amending never overwrites it; the amend itself travels as its own pair,
  // the flag and the borrowed text, and comes back checked on every device.
  function commitDraftState() {
    const amend = commitAmendEl.checked;
    return {
      message: amend ? commitStash : commitMsgEl.value,
      amend,
      amendMessage: amend ? commitMsgEl.value : "",
      paths: [...commitPicked].sort().join("\n"),
    };
  }

  function sameCommitDraft(a, b) {
    return !!a && !!b && a.message === b.message && a.amend === b.amend
      && a.amendMessage === b.amendMessage && a.paths === b.paths;
  }

  function queueCommitDraft() {
    window.clearTimeout(commitDraftTimer);
    commitDraftTimer = window.setTimeout(() => void saveCommitDraft(), 600);
  }

  async function saveCommitDraft() {
    window.clearTimeout(commitDraftTimer);
    if (!gitRepo) return;
    const state = commitDraftState();
    if (sameCommitDraft(commitDraftSaved, state)) return;
    commitDraftSaved = state;
    commitDraftSaving = true;
    try {
      const res = await postJSON(`${base}/git/commit-draft`, {
        message: state.message,
        amend: state.amend,
        amendMessage: state.amendMessage,
        paths: state.paths ? state.paths.split("\n") : [],
      });
      if (!res.ok) throw new Error("refused");
      const payload = await res.json().catch(() => ({}));
      // What this device wrote last: a pull only applies what is newer, so an
      // echo of the own save never lands back in the panel.
      if (payload.updatedAt) commitDraftAt = payload.updatedAt;
    } catch (err) {
      void err;
      commitDraftSaved = null; // the next change tries again
    } finally {
      commitDraftSaving = false;
    }
    // Look once after every save: two devices that wrote at the same moment
    // both end on what the server kept.
    await pullCommitDraft();
  }

  async function pullCommitDraft() {
    if (!gitRepo) return;
    // A save of this device is on its way; the answer of this pull would be
    // older than what the server is about to hold. saveCommitDraft pulls
    // again when it is through.
    if (commitDraftSaving) return;
    let draft;
    try {
      draft = await getJSON(`${base}/git/commit-draft`, { signal });
    } catch (err) {
      void err;
      return;
    }
    const at = draft.updatedAt || "";
    if (!at) {
      // TODO(v2.0.0): drop the lift of the pre-1.43 device-local draft.
      const seed = store.get(commitMsgKey, "");
      if (seed) {
        store.remove(commitMsgKey);
        if (!commitMsgEl.value && !commitAmendEl.checked) {
          commitMsgEl.value = seed;
          syncCommitControls();
          queueCommitDraft();
        }
      }
      return;
    }
    if (commitDraftAt && at <= commitDraftAt) return;
    const paths = (draft.paths || []).slice().sort();
    const incoming = {
      message: draft.message || "",
      amend: !!draft.amend,
      amendMessage: draft.amend ? draft.amendMessage || "" : "",
      paths: paths.join("\n"),
    };
    const held = commitDraftState();
    if (sameCommitDraft(incoming, held)) {
      commitDraftAt = at;
      return;
    }
    // Unsaved edits win until this device has written them; the save that the
    // pending debounce runs pulls again, so the two devices end on the same
    // draft instead of each keeping what the other one replaced.
    if (!sameCommitDraft(commitDraftSaved, held)) return;
    commitDraftAt = at;
    commitDraftSaved = incoming;
    // Setting checked by hand fires no change event, so the borrow/restore of
    // the local handler stays out and field plus stash are set explicitly.
    commitAmendEl.checked = incoming.amend;
    if (incoming.amend) {
      commitStash = incoming.message;
      commitMsgEl.value = incoming.amendMessage;
    } else {
      commitStash = "";
      commitMsgEl.value = incoming.message;
    }
    commitPicked.clear();
    for (const path of paths) commitPicked.add(path);
    pruneCommitPicked();
    if (commitOn) renderCommitList();
    else syncCommitControls();
  }

  async function doCommit(push) {
    if (commitBusy || gitBusy) return;
    const paths = commitSelectedPaths();
    const message = commitMsgEl.value.trim();
    if ((paths.length === 0 && !commitAmendEl.checked) || message === "") return;
    commitBusy = true;
    gitBusy = true;
    gitBusyAction = "commit";
    commitErrorEl.hidden = true;
    syncCommitControls();
    paintGitStatus();
    status(push ? "Committing and pushing…" : "Committing…");
    try {
      // What the person sees in the buffer is what the commit has to take, so
      // unsaved work on a picked path is written first, like every save path.
      const covered = (path) => paths.some((p) => path === p
        || (p.endsWith("/") ? path.startsWith(p) : path.startsWith(`${p}/`)));
      for (const tab of tabs) {
        if (!tab.dirty) continue;
        if (tab.compare
          ? covered(tab.compare.left) || covered(tab.compare.right)
          : covered(tab.path)) {
          // A save the disk refused is answered in a dialog, and a commit that
          // walked past that answer would record a file nobody in front of the
          // panel has seen. The commit ends here instead, with the picks and
          // the message still standing.
          if ((await saveTab(tab)) !== "saved") {
            throw new Error(`"${tab.compare ? tab.name : tab.path}" was not saved, so nothing was committed.`);
          }
        }
      }
      const res = await postJSON(`${base}/git/commit`, {
        message,
        paths,
        amend: commitAmendEl.checked,
        push: !!push,
      });
      await ensureOk(res, "The commit failed.");
      const data = await res.json();
      commitMsgEl.value = "";
      commitStash = "";
      commitAmendEl.checked = false;
      // The commit spent the draft; the server cleared its copy and published,
      // so the other devices empty themselves the same way.
      commitPicked.clear();
      window.clearTimeout(commitDraftTimer);
      commitDraftSaved = { message: "", paths: "", amend: false, amendMessage: "" };
      const stamp = data.hash ? ` ${data.hash} "${data.subject}"` : "";
      notifySuccess(data.pushed ? `Committed and pushed${stamp}` : `Committed${stamp}.`);
      // A refused push does not touch the commit: it stands, the refusal
      // stands beside it, and the message is gone because the commit took it.
      if (data.pushError) {
        commitErrorEl.textContent = data.pushError;
        commitErrorEl.hidden = false;
      }
      status("");
      // HEAD moved: the same pull the git event triggers, without waiting for
      // an event that may race the answer.
      dropChangeHeads();
      void loadGitStatus();
      void refreshDiffHead();
      void loadCommitInfo();
    } catch (err) {
      if (signal.aborted) return;
      commitErrorEl.textContent = err.message || "The commit failed.";
      commitErrorEl.hidden = false;
      status("");
    } finally {
      commitBusy = false;
      gitBusy = false;
      gitBusyAction = "";
      syncCommitControls();
      paintGitStatus();
    }
  }

  commitToggleBtn.addEventListener("click", toggleCommit, { signal });
  gitItem.addEventListener("click", () => openGitSheet(), { signal });
  commitCloseBtn.addEventListener("click", closeCommit, { signal });
  commitAllEl.addEventListener("change", () => {
    if (commitAllEl.checked) for (const entry of commitSelectable()) commitPicked.add(entry.path);
    else commitPicked.clear();
    renderCommitList();
    queueCommitDraft();
  }, { signal });
  commitMsgEl.addEventListener("input", () => {
    // commitDraftState knows whose text the field holds, the draft's or the
    // borrowed amend's, so every keystroke saves into the right slot.
    queueCommitDraft();
    commitErrorEl.hidden = true;
    syncCommitControls();
  }, { signal });
  commitMsgEl.addEventListener("keydown", (e) => {
    if ((e.ctrlKey || e.metaKey) && !e.shiftKey && !e.altKey && e.key === "Enter") {
      e.preventDefault();
      e.stopPropagation();
      void doCommit();
    }
  }, { signal });
  commitAmendEl.addEventListener("change", () => {
    commitErrorEl.hidden = true;
    if (commitAmendEl.checked) {
      commitStash = commitMsgEl.value;
      if (commitInfo && commitInfo.lastMessage) commitMsgEl.value = commitInfo.lastMessage;
    } else {
      commitMsgEl.value = commitStash;
    }
    syncCommitControls();
    queueCommitDraft();
  }, { signal });
  commitBtn.addEventListener("click", () => void doCommit(), { signal });
  commitPushItem.addEventListener("click", () => {
    // doCommit disables the arrow in this very click, and bootstrap's
    // auto-close skips a disabled toggle, so the menu would stay open.
    window.bootstrap?.Dropdown.getInstance(commitMoreBtn)?.hide();
    void doCommit(true);
  }, { signal });
  function paintCommitGroup() {
    commitGroupBtn.classList.toggle("active", commitGrouped);
    commitGroupBtn.setAttribute("aria-pressed", commitGrouped ? "true" : "false");
  }
  commitGroupBtn.addEventListener("click", () => {
    commitGrouped = !commitGrouped;
    store.set(COMMIT_GROUP_KEY, commitGrouped ? "1" : "0");
    paintCommitGroup();
    renderCommitList();
  }, { signal });
  paintCommitGroup();

  // ---- git surface -----------------------------------------------------------

  // The branch stands in the statusbar for as long as the project is a
  // repository: the name, and the two arrows that say how far it is from its
  // upstream. The button is the way into the git sheet, which holds every
  // action on the repository as a whole; what concerns one file stays in that
  // file's context menu.

  // gitSurface is the one answer to "is there a git surface at all": the status
  // has arrived, and either the project is no repository, which is what the
  // clone is reached from, or git named the branch it stands on. The statusbar
  // segment, the menu entry, the sheet and the shortcut all read this one, so
  // they cannot end up offering different halves of the same thing.
  function gitSurface() {
    return gitLoaded && (!gitRepo || !!(gitBranch && gitBranch.name));
  }

  function paintGitStatus() {
    const show = gitSurface();
    gitStatusBtn.hidden = !show;
    if (!show) return;
    gitIconEl.hidden = gitBusy;
    gitSpinEl.hidden = !gitBusy;
    paintGitLogBusy();
    if (!gitRepo) {
      gitBranchEl.textContent = "No repository";
      gitAbEl.hidden = true;
      gitStatusBtn.title = "Clone a repository into this project";
      if (sheetKind === "git") renderGitSheet();
      return;
    }
    gitBranchEl.textContent = gitBranch.name;
    const ab = [];
    if (gitBranch.counted) {
      if (gitBranch.ahead) ab.push(`↑${gitBranch.ahead}`);
      if (gitBranch.behind) ab.push(`↓${gitBranch.behind}`);
    }
    gitAbEl.textContent = ab.join(" ");
    gitAbEl.hidden = ab.length === 0;
    const parts = [gitBranch.detached ? `Detached at ${gitBranch.name}` : `On branch ${gitBranch.name}`];
    if (gitBranch.upstream) {
      parts.push(gitBranch.counted
        ? `${gitBranch.ahead} ahead, ${gitBranch.behind} behind ${gitBranch.upstream}`
        : `upstream ${gitBranch.upstream}`);
    } else if (!gitBranch.detached) {
      parts.push("no upstream");
    }
    gitStatusBtn.title = parts.join(" · ");
    if (sheetKind === "git") renderGitSheet();
  }

  // The sheet's two halves: the action rows the data repaints, and the
  // history below them, which loads once per open and pages by hand.
  let gitSheetEls = null;

  // fetch is false on the way back from a drilled level: the sheet is being
  // rebuilt, not opened, and the round it would run has already run.
  function openGitSheet({ fetch = true } = {}) {
    if (!gitSurface()) return;
    openSheet("git", "Git");
    const actions = document.createElement("div");
    const log = document.createElement("div");
    sheetBodyEl.append(actions, log);
    gitSheetEls = { actions, log };
    renderGitSheet();
    focusSheetTop();
    if (!gitRepo) return;
    void appendGitLog(log, "", 0);
    // The counts on show may be minutes old. The quiet fetch runs only when
    // they are, and its answer arrives as the ordinary git event.
    if (fetch) postJSON(`${base}/git/fetch`, { auto: true }).catch(() => {});
  }

  // The divider draws its own line: bootstrap's rule reads variables that
  // only a .dropdown-menu defines, and the sheet body is none.
  function gitDivider() {
    const divider = document.createElement("div");
    divider.className = "dropdown-divider";
    divider.style.borderTop = "1px solid var(--tblr-border-color)";
    divider.style.margin = "0.25rem 0";
    return divider;
  }

  function renderGitSheet() {
    repaintSheet(gitSheetEls ? gitSheetEls.actions : sheetBodyEl, paintGitSheet);
  }

  // The history is painted once and then stands, so the write that disables
  // every action row has to reach its cells here: one live row in a sheet
  // that is otherwise waiting is a second write one tap away.
  function paintGitLogBusy() {
    for (const cell of sheetBodyEl.querySelectorAll("[data-git-commit]")) cell.disabled = gitBusy || commitBusy;
  }

  function paintGitSheet() {
    if (sheetKind !== "git" || !gitSheetEls || !gitSheetEls.actions.isConnected) return;
    if (!gitRepo) {
      gitSheetEls.actions.replaceChildren(sheetActionRow({
        icon: "ti-cloud-download",
        label: "Clone repository",
        sub: "into this project folder",
        disabled: gitBusy,
        busy: gitBusyAction === "clone",
        onClick: () => void cloneDialog(),
      }));
      return;
    }
    const b = gitBranch || {};
    const count = commitChanges.length;
    const standing = b.detached ? `detached at ${b.name}` : `on ${b.name}`;
    const drift = b.upstream
      ? (b.counted ? `${b.ahead} ahead, ${b.behind} behind ${b.upstream}` : `upstream ${b.upstream}`)
      : (b.detached ? "" : "no upstream");
    const rows = [
      sheetActionRow({
        icon: "ti-git-branch",
        label: "Switch branch",
        title: "Switch branch",
        disabled: gitBusy,
        busy: gitBusyAction === "checkout",
        sub: [standing, drift].filter(Boolean).join(" · "),
        onClick: () => openBranchPicker(),
      }),
      sheetActionRow({
        icon: "ti-plus",
        label: "New branch",
        disabled: gitBusy,
        busy: gitBusyAction === "branch",
        sub: "created here and switched to",
        onClick: () => void createBranchDialog(),
      }),
      gitDivider(),
      sheetActionRow({
        icon: "ti-git-commit",
        label: "Commit",
        disabled: gitBusy,
        sub: count ? `${count} ${count === 1 ? "change" : "changes"}` : "the working copy is clean",
        onClick: () => {
          closeSheet();
          openCommit();
        },
      }),
      gitDivider(),
      sheetActionRow({
        icon: "ti-upload",
        label: "Push",
        disabled: gitBusy,
        busy: gitBusyAction === "push",
        sub: b.counted
          ? (b.ahead ? `${b.ahead} ${b.ahead === 1 ? "commit" : "commits"} to push` : "nothing to push")
          : "",
        onClick: () => void doPush(false),
      }),
      sheetActionRow({
        icon: "ti-download",
        label: "Pull",
        disabled: gitBusy,
        busy: gitBusyAction === "pull",
        sub: b.counted && b.behind ? `${b.behind} behind, fast forward only` : "fast forward only",
        onClick: () => void doPull(),
      }),
      sheetActionRow({
        icon: "ti-refresh",
        label: "Fetch",
        disabled: gitBusy,
        busy: gitBusyAction === "fetch",
        sub: "updates ahead and behind",
        onClick: () => void doFetch(),
      }),
      gitDivider(),
      sheetActionRow({
        icon: "ti-alert-triangle",
        iconClass: "text-danger",
        label: "Force push",
        disabled: gitBusy,
        busy: gitBusyAction === "force",
        sub: "with lease, asks first",
        onClick: () => void doPush(true),
      }),
    ];
    gitSheetEls.actions.replaceChildren(...rows);
  }

  function logDate(time) {
    if (!time) return "";
    return new Date(time * 1000).toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" });
  }

  async function copyHash(sha) {
    try {
      await navigator.clipboard.writeText(sha);
      notifySuccess(`Copied ${sha.slice(0, 7)}.`);
    } catch {
      notifyError("Clipboard is not available.");
    }
  }

  // gitLogCell is one commit of a history, built like a container of the
  // docker sheet: the whole cell is the control, a click on it opens the
  // commit's menu, and the cells stand next to each other where the width
  // allows it. What the commit can do lives in that menu alone, so the cell
  // carries no controls of its own and the keyboard reaches everything by
  // walking the rows and pressing Enter.
  function gitLogCell(commit, path) {
    const chipsEl = document.createElement("span");
    chipsEl.className = "d-flex align-items-center gap-1";
    const factsEl = document.createElement("span");
    factsEl.className = "text-truncate";
    factsEl.textContent = [commit.short, commit.author, logDate(commit.time)].filter(Boolean).join(" · ");
    const paintChips = () => chipsEl.replaceChildren(...(commit.tags || []).map(tagChip));
    paintChips();
    const cell = sheetActionRow({
      icon: "ti-git-commit",
      label: commit.summary || commit.short,
      subNodes: [chipsEl, factsEl],
      title: commit.summary || commit.short,
      onClick: (event) => {
        // A row reached with the keyboard clicks at no point at all, so the
        // row itself is the anchor then, like the docker sheet's cells.
        const rect = event.currentTarget.getBoundingClientRect();
        openMenu({
          x: Math.round(event.clientX || rect.left),
          y: Math.round(event.clientY || rect.bottom + 4),
          items: commitMenuItems(commit, path, paintChips),
          signal,
        });
      },
    });
    cell.dataset.gitCommit = commit.sha;
    const col = document.createElement("div");
    col.className = "col-12 col-lg-6";
    col.appendChild(cell);
    return col;
  }

  // commitMenuItems is what one commit can do, the app's own menu over the row
  // it belongs to: the history keeps standing where it was scrolled to, which
  // a drilled sheet level could not do, it renders itself and its Back put
  // the reader back at the top of a freshly loaded list.
  function commitMenuItems(commit, path, paintChips) {
    const diffTab = path ? null : activeTab();
    const diffPath = path || (diffTab && !diffTab.kind && !diffTab.compare && !diffTab.external ? diffTab.path : "");
    const items = [
      {
        label: diffPath ? `Diff ${baseName(diffPath)} against this` : "Show the diff",
        icon: "ti-git-compare",
        disabled: !diffPath,
        action: () => {
          closeSheet();
          void diffAgainst(diffPath, commit.sha);
        },
      },
      { label: "Copy the hash", icon: "ti-copy", action: () => void copyHash(commit.sha) },
      { divider: true },
      { label: "Tag this commit", icon: "ti-tag", action: () => void tagDialog(commit, paintChips) },
    ];
    for (const name of commit.tags || []) {
      items.push({ label: `Push ${name}`, icon: "ti-upload", action: () => void pushTag(name) });
      items.push({ label: `Delete ${name}`, icon: "ti-trash", danger: true, action: () => void deleteTagDialog(commit, name, paintChips) });
    }
    return items;
  }

  function tagChip(name) {
    const chip = document.createElement("span");
    chip.className = "badge bg-blue-lt flex-shrink-0";
    chip.textContent = name;
    return chip;
  }

  async function pushTag(name) {
    const data = await gitRun("tag/push", { tag: name }, `Pushing "${name}"…`, "tag");
    if (!data) return;
    notifySuccess(`Pushed "${name}".`);
  }

  async function deleteTagDialog(commit, name, paintChips) {
    let remote = false;
    if (dialogAvailable()) {
      const result = await fireDialog({
        title: `Delete "${name}"?`,
        html: `<div class="text-secondary small">The tag goes, the commit stays.</div>`
          + `<label class="form-check text-start mt-3 mb-0"><input class="form-check-input" type="checkbox" data-tag-remote checked>`
          + `<span class="form-check-label">Delete it on the remote too</span></label>`,
        showCancelButton: true,
        confirmButtonText: "Delete tag",
        cancelButtonText: "Cancel",
        reverseButtons: true,
        preConfirm: () => ({ remote: window.Swal.getHtmlContainer().querySelector("[data-tag-remote]").checked }),
      });
      if (!result.isConfirmed || !result.value) return;
      ({ remote } = result.value);
    } else if (!await confirmDialog({ title: `Delete "${name}"?`, confirmText: "Delete tag" })) {
      return;
    }
    const data = await gitRun("tag/delete", { tag: name, remote }, `Deleting "${name}"…`, "tag");
    if (!data) return;
    commit.tags = (commit.tags || []).filter((each) => each !== name);
    paintChips?.();
    if (data.remoteError) notifyError(data.remoteError);
    else notifySuccess(data.remote ? `Deleted "${name}" here and on the remote.` : `Deleted "${name}".`);
  }

  async function tagDialog(commit, paintChips) {
    const published = Boolean(gitBranch && gitBranch.upstream);
    let name = "";
    let message = "";
    let push = published;
    if (dialogAvailable()) {
      const result = await fireDialog({
        title: "Tag this commit",
        input: "text",
        inputPlaceholder: "v1.0.0",
        html: `<div class="text-secondary small text-truncate">${escapeHtml(commit.short)} · ${escapeHtml(commit.summary || "")}</div>`
          + `<input class="form-control mt-3" data-tag-message placeholder="Message, optional" aria-label="Tag message">`
          + `<div class="text-secondary small mt-1 text-start">A message makes it an annotated tag, which is what a release is.</div>`
          + `<label class="form-check text-start mt-3 mb-0"><input class="form-check-input" type="checkbox" data-tag-push${push ? " checked" : ""}>`
          + `<span class="form-check-label">Push the tag</span></label>`,
        showCancelButton: true,
        confirmButtonText: "Create tag",
        cancelButtonText: "Cancel",
        reverseButtons: true,
        preConfirm: (value) => {
          const normalized = normalizeBranchName(value);
          if (!normalized) {
            window.Swal.showValidationMessage("A tag name is required.");
            return false;
          }
          const box = window.Swal.getHtmlContainer();
          return {
            name: normalized,
            message: box.querySelector("[data-tag-message]").value.trim(),
            push: box.querySelector("[data-tag-push]").checked,
          };
        },
      });
      if (!result.isConfirmed || !result.value) return;
      ({ name, message, push } = result.value);
    } else {
      name = normalizeBranchName(await promptText({
        title: "Tag this commit",
        placeholder: "v1.0.0",
        confirmText: "Create tag",
      }) || "");
      if (!name) return;
    }
    const data = await gitRun("tag", { sha: commit.sha, tag: name, message, push }, `Tagging "${name}"…`, "tag");
    if (!data) return;
    commit.tags = [...(commit.tags || []), name];
    paintChips?.();
    if (data.pushError) notifyError(data.pushError);
    else notifySuccess(data.pushed ? `Tagged "${name}" and pushed it.` : `Tagged "${name}".`);
  }

  // appendGitLog fills one page of history into the sheet and hangs an
  // "older" row behind it while there is more. The host outliving the await
  // is the guard: a sheet that moved on took it out of the document.
  async function appendGitLog(host, path, skip) {
    const loading = document.createElement("div");
    loading.className = "text-secondary small px-3 py-2";
    loading.textContent = "Loading history…";
    host.appendChild(loading);
    let page;
    try {
      page = await getJSON(`${base}/git/log?skip=${skip}${path ? `&path=${encodeURIComponent(path)}` : ""}`, { signal });
    } catch (err) {
      if (!signal.aborted && loading.isConnected) loading.textContent = err.message || "The history could not be read.";
      return;
    }
    if (!loading.isConnected) return;
    loading.remove();
    if (skip === 0) {
      if (!path) {
        host.appendChild(gitDivider());
        const head = document.createElement("div");
        head.className = "dropdown-header";
        head.style.padding = "0.5rem 0.75rem 0.25rem";
        head.textContent = "History";
        host.appendChild(head);
      }
      if (page.commits.length === 0) {
        const empty = document.createElement("div");
        empty.className = "text-secondary small px-3 py-2";
        empty.textContent = path ? "No commit touches this file yet." : "No commits yet.";
        host.appendChild(empty);
        return;
      }
    }
    // One grid for the whole history, so a page asked for later joins the
    // lines that already stand instead of starting a second one.
    let grid = host.querySelector("[data-git-log-grid]");
    if (!grid) {
      grid = document.createElement("div");
      grid.className = "row row-deck g-0";
      grid.setAttribute("data-git-log-grid", "");
      host.appendChild(grid);
    }
    for (const commit of page.commits) grid.appendChild(gitLogCell(commit, path));
    paintGitLogBusy();
    // A history that is the whole sheet arrives after it opened, so the first
    // row takes the focus here; the git sheet's own actions already have it and
    // focusSheetTop leaves a sheet that holds the focus alone.
    if (skip === 0) focusSheetTop();
    if (page.more) {
      const row = document.createElement("div");
      row.className = "editor-sheet-row";
      const more = document.createElement("button");
      more.type = "button";
      more.className = "editor-sheet-open";
      more.innerHTML = `<i class="ti ti-chevron-down" aria-hidden="true"></i><span class="text-secondary">Older commits</span>`;
      // Asking for a page takes the row that asked away, so the focus lands
      // where it stood, which is the first commit of the page that arrived.
      more.addEventListener("click", async () => {
        const index = rowsOf(host, SHEET_ROW).indexOf(more);
        row.remove();
        await appendGitLog(host, path, skip + page.commits.length);
        if (index >= 0) keepSheetFocus(host, index);
      }, { signal });
      row.appendChild(more);
      host.appendChild(row);
    }
  }

  // The file's history lives in the file's context menu, like the diff and
  // the blame: a sheet of its commits, and picking one opens the diff against
  // exactly that state.
  function openFileHistory(path) {
    openSheet("history", `History of ${baseName(path)}`);
    const host = document.createElement("div");
    sheetBodyEl.appendChild(host);
    void appendGitLog(host, path, 0);
  }

  // diffAgainst opens a file's diff against a revision: an open tab switches
  // in place, a closed file opens into it. The history and the revision
  // picker both land here, filling the same field the HEAD switch fills.
  async function diffAgainst(path, rev) {
    let tab = tabByPath(path);
    if (!tab) {
      await openPath(path);
      tab = tabByPath(path);
    }
    if (!tab || tab.kind || tab.compare || tab.external) return;
    // The comparison is what this click is about, so the drawer goes on every
    // way out, not only when openPath opened the file fresh.
    closeDrawer();
    if (tab.path === activePath) {
      await applyDiff(rev);
      return;
    }
    tab.diffRev = rev;
    tab.diffOriginal = null;
    persistTabs();
    activateTab(path);
  }

  // openRefPicker is the one autocomplete over what the repository can be
  // asked about. The branch switch lists branches with the remotes fetched
  // fresh; the revision diff lists the names plus the commits and lets a raw
  // name or hash through as typed.
  //
  // The search is the server's (`git/refs?q=&kinds=`) and never a filter over
  // a list this page happens to hold. It used to be one, and that made the
  // list the whole world: a name outside the first page of each kind could not
  // be found by typing it, and a commit was not in the list at all, so the one
  // revision somebody usually wants to diff against was the one thing the
  // revision picker could not offer.
  //
  // Three things hang together for that to read as an autocomplete and not as
  // a request per keystroke: the typing is debounced, a round in flight says
  // so on the sheet, and every answer carries the number of the round it
  // belongs to, so a slow one that comes back after a newer one is dropped
  // instead of painting an older query's list — the same guard loadGitStatus
  // uses for the status. Typing voids the rounds in flight at once and not
  // only when the next one goes out: an answer to what stood there two letters
  // ago is not this list's answer any more.
  async function openRefPicker({ title, kinds, fetchFirst, raw, placeholder, onPick, onBack }) {
    openSheet("picker", title);
    // A picker that was drilled into from the git sheet carries the way back,
    // like the docker menus do: one Back row on top, above the filter.
    if (onBack) {
      sheetBack = onBack;
      const backRow = document.createElement("div");
      backRow.className = "editor-sheet-row";
      const back = document.createElement("button");
      back.type = "button";
      back.className = "editor-sheet-open";
      back.innerHTML = `<i class="ti ti-arrow-left" aria-hidden="true"></i><span>Back</span>`;
      back.addEventListener("click", () => onBack(), { signal });
      backRow.appendChild(back);
      sheetBodyEl.appendChild(backRow);
    }
    const wrap = document.createElement("div");
    wrap.className = "p-2 border-bottom";
    const input = document.createElement("input");
    input.type = "text";
    input.className = "form-control form-control-sm";
    input.placeholder = placeholder;
    input.autocomplete = "off";
    input.spellcheck = false;
    input.setAttribute("aria-label", title);
    wrap.appendChild(input);
    const listEl = document.createElement("div");
    // The round in flight is visible, and it is one element for both cases:
    // the list this sheet opens with is a round like any other.
    const loading = document.createElement("div");
    loading.className = "text-secondary small px-3 py-2 d-flex align-items-center gap-2";
    loading.setAttribute("data-picker-loading", "");
    loading.innerHTML = `<span class="spinner-border spinner-border-sm" aria-hidden="true"></span><span data-picker-loading-text>Loading…</span>`;
    const note = document.createElement("div");
    note.className = "text-secondary small px-3 py-2";
    note.hidden = true;
    sheetBodyEl.append(wrap, loading, listEl, note);
    if (pointerMedia.matches) input.focus();

    const kindIcon = { branch: "ti-git-branch", remote: "ti-cloud", tag: "ti-tag" };
    // One row model for both halves of the answer, so the list renders one
    // thing. A name is a place the repository keeps: picking a remote branch
    // means checking out its local name, which git creates as the tracking
    // branch when it is not there yet.
    const refEntry = (ref) => ({
      value: ref.kind === "remote" && ref.branch ? ref.branch : ref.name,
      name: ref.name,
      sub: ref.head ? "current branch" : ref.kind === "remote" ? `remote, checks out ${ref.branch || ref.name}` : ref.kind,
      icon: kindIcon[ref.kind] || "ti-git-branch",
      // Switching to where you already stand does nothing, so the row says so
      // instead of doing it; for a diff the current branch is as good a
      // revision as any.
      disabled: !!ref.head && !raw,
    });
    // A commit is a point in the history, and a row of them has to be told
    // apart from the next one: the short hash and the subject on the first
    // line, the author and the date under it. The value is the full hash, so
    // the diff is against exactly this commit and not against whatever a
    // prefix might grow into.
    const commitEntry = (commit) => ({
      value: commit.sha,
      name: `${commit.short} ${commit.summary}`,
      sub: [commit.author, commitDate(commit.time)].filter(Boolean).join(" · "),
      icon: "ti-git-commit",
      disabled: false,
    });

    let entries = [];
    let active = 0;
    let searchSeq = 0;
    let searching = false;
    let searchTimer = 0;
    let fetchError = "";

    const setSearching = (on, text) => {
      searching = on;
      loading.hidden = !on;
      if (on) loading.querySelector("[data-picker-loading-text]").textContent = text || "Searching…";
    };
    // Picking awaits the action with the picked row carrying the spinner;
    // only a success closes the sheet, a refusal leaves the list standing for
    // the next try. Quick picks close themselves inside onPick, and the
    // second close is a no-op.
    let picking = false;
    let busyValue = null;
    const pick = async (value) => {
      if (picking || !value) return;
      picking = true;
      busyValue = value;
      input.disabled = true;
      renderList();
      const ok = await onPick(value);
      if (signal.aborted || !listEl.isConnected) return;
      if (ok === false) {
        picking = false;
        busyValue = null;
        input.disabled = false;
        renderList();
        return;
      }
      closeSheet();
    };
    const renderList = () => {
      if (active >= entries.length) active = Math.max(0, entries.length - 1);
      listEl.replaceChildren(...entries.map((entry, i) => {
        const row = document.createElement("div");
        row.className = "editor-sheet-row";
        if (i === active) row.classList.add("active");
        const open = document.createElement("button");
        open.type = "button";
        open.className = "editor-sheet-open";
        let icon;
        if (busyValue === entry.value) {
          icon = document.createElement("span");
          icon.className = "spinner-border spinner-border-sm flex-shrink-0";
          icon.style.width = "1em";
          icon.style.height = "1em";
        } else {
          icon = document.createElement("i");
          icon.className = `ti ${entry.icon} flex-shrink-0`;
        }
        icon.setAttribute("aria-hidden", "true");
        const col = document.createElement("span");
        col.className = "d-flex flex-column min-w-0";
        const nameEl = document.createElement("span");
        nameEl.className = "editor-sheet-name text-truncate";
        nameEl.textContent = entry.name;
        const subEl = document.createElement("span");
        subEl.className = "editor-sheet-dir text-truncate";
        subEl.textContent = entry.sub;
        col.append(nameEl, subEl);
        open.append(icon, col);
        if (entry.disabled) {
          open.disabled = true;
          open.style.opacity = "0.55";
        } else if (picking) {
          open.disabled = true;
        } else {
          open.addEventListener("click", () => void pick(entry.value), { signal });
        }
        row.appendChild(open);
        return row;
      }));
      if (!entries.length && !searching) {
        const empty = document.createElement("div");
        empty.className = "text-secondary small px-3 py-2";
        empty.textContent = raw ? "Nothing matches; Enter uses what you typed." : "No matching branch.";
        listEl.appendChild(empty);
      }
    };
    // runSearch is one round. An empty text is the list the sheet opens with,
    // the recently moved names and no commits, which is what the server
    // answers for it.
    const runSearch = async (text) => {
      if (signal.aborted || !listEl.isConnected) return;
      const seq = ++searchSeq;
      setSearching(true, text ? "Searching…" : "Loading…");
      const params = new URLSearchParams({ kinds: kinds.join(",") });
      if (text) params.set("q", text);
      let data;
      try {
        data = await getJSON(`${base}/git/refs?${params.toString()}`, { signal });
      } catch (err) {
        if (seq !== searchSeq || signal.aborted || !listEl.isConnected) return;
        setSearching(false);
        entries = [];
        note.textContent = err.message || "The refs could not be read.";
        note.hidden = false;
        renderList();
        return;
      }
      if (seq !== searchSeq || !listEl.isConnected) return;
      setSearching(false);
      entries = [...(data.refs || []).map(refEntry), ...(data.commits || []).map(commitEntry)];
      active = 0;
      note.textContent = fetchError ? `The remotes could not be fetched: ${fetchError}` : "";
      note.hidden = !fetchError;
      renderList();
    };
    // The listeners go on before the first answer arrives: typing a raw name
    // and pressing Enter must work however slow the round is, and with raw off
    // an early Enter simply has nothing to pick yet.
    input.addEventListener("input", () => {
      searchSeq += 1;
      setSearching(true, input.value.trim() ? "Searching…" : "Loading…");
      clearTimeout(searchTimer);
      searchTimer = setTimeout(() => void runSearch(input.value.trim()), 200);
    }, { signal });
    input.addEventListener("keydown", (e) => {
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        if (!entries.length) return;
        active = (active + (e.key === "ArrowDown" ? 1 : -1) + entries.length) % entries.length;
        renderList();
      } else if (e.key === "Enter") {
        e.preventDefault();
        const typed = input.value.trim();
        // While a round is out the list belongs to an older text, so a row of
        // it is not what Enter means any more. A typed name still is, and that
        // is the path that must keep working however slow the search is.
        if (searching) {
          if (raw && typed) void pick(typed);
          return;
        }
        const chosen = entries[active];
        if (chosen && !chosen.disabled) void pick(chosen.value);
        else if (raw && typed) void pick(typed);
      }
    }, { signal });

    setSearching(true, "Loading…");
    if (fetchFirst) {
      try {
        await ensureOk(await postJSON(`${base}/git/fetch`, { auto: true }), "The remotes could not be fetched.");
      } catch (err) {
        fetchError = err.message || "The remotes could not be fetched.";
      }
      if (signal.aborted || !listEl.isConnected) return;
    }
    await runSearch("");
  }

  function openBranchPicker() {
    // Branches and remotes, and no commits: a checkout of a hash is a
    // detached HEAD, which is not what this picker is for.
    void openRefPicker({
      title: "Switch branch",
      kinds: ["branch", "remote"],
      fetchFirst: true,
      placeholder: "Branch…",
      onPick: (name) => doCheckout(name),
      onBack: () => openGitSheet({ fetch: false }),
    });
  }

  function openRevPicker(path) {
    void openRefPicker({
      title: `Diff ${baseName(path)} against`,
      kinds: ["branch", "remote", "tag", "commit"],
      raw: true,
      placeholder: "Branch, tag or commit…",
      onPick: (rev) => {
        closeSheet();
        void diffAgainst(path, rev);
      },
    });
  }

  // gitRun is the one door every write goes through, one at a time: the
  // statusbar icon becomes a spinner and the tapped row carries its own, so
  // the busy state stands at the control itself; the guard is what keeps a
  // double tap one action. The guard is this page's alone though, the server
  // holds the working copy for the write and answers a second one with its
  // own refusal, which lands in the toast like every other.
  async function gitRun(action, body, progress, busyKey) {
    if (gitBusy || commitBusy) return null;
    gitBusy = true;
    gitBusyAction = busyKey || action;
    paintGitStatus();
    syncCommitControls();
    status(progress);
    try {
      const res = await postJSON(`${base}/git/${action}`, body);
      await ensureOk(res, "The git action failed.");
      return await res.json();
    } catch (err) {
      if (!signal.aborted) notifyError(err.message || "The git action failed.");
      return null;
    } finally {
      gitBusy = false;
      gitBusyAction = "";
      paintGitStatus();
      syncCommitControls();
      status("");
    }
  }

  async function doPush(force) {
    if (force) {
      const target = gitBranch && gitBranch.upstream ? `"${escapeHtml(gitBranch.upstream)}"` : "the upstream";
      const ok = await confirmDialog({
        title: "Force push?",
        html: `<div class="text-secondary">Overwrites ${target} where it moved away. It runs as force-with-lease: work this repository has not fetched is never overwritten.</div>`,
        confirmText: "Force push",
      });
      if (!ok) return;
    }
    const data = await gitRun("push", { force: !!force }, force ? "Force pushing…" : "Pushing…", force ? "force" : "push");
    if (!data) return;
    notifySuccess(force ? "Force pushed." : "Pushed.");
    // No status round here: a push that went through publishes the git event
    // itself, and this page answers that event like every other one. Asking as
    // well was two rounds for one answer, and the second one raced the first.
  }

  async function doPull() {
    const data = await gitRun("pull", {}, "Pulling…");
    if (!data) return;
    notifySuccess("Pulled.");
    await afterGitMutation();
  }

  async function doFetch() {
    const data = await gitRun("fetch", { auto: false }, "Fetching…");
    if (!data) return;
    notifySuccess("Fetched.");
    // Same as the push: a fetch that brought something publishes the event,
    // and one that brought nothing has nothing for a status round to find.
  }

  async function doCheckout(name) {
    if (gitBranch && !gitBranch.detached && gitBranch.name === name) return true;
    const data = await gitRun("checkout", { branch: name }, `Switching to "${name}"…`, "checkout");
    if (!data) return false;
    notifySuccess(`Switched to "${name}".`);
    await afterGitMutation();
    return true;
  }

  function normalizeBranchName(raw) {
    let name = String(raw || "").trim().replace(/[^\w./-]+/g, "-");
    name = name.replace(/\.{2,}/g, ".");
    name = name.replace(/\/{2,}/g, "/");
    name = name.replace(/(^|\/)\.+/g, "$1");
    name = name.replace(/\.+(?=\/)/g, "");
    name = name.replace(/\.lock(?=\/|$)/g, "");
    name = name.replace(/^[-/.]+/, "").replace(/[-/.]+$/, "");
    return name;
  }

  async function createBranchDialog() {
    let name = "";
    if (dialogAvailable()) {
      const result = await fireDialog({
        title: "New branch",
        input: "text",
        inputPlaceholder: "feature/name",
        html: `<div class="text-secondary small">Created at the current state and switched to.</div>`
          + `<div class="small mt-2" data-branch-preview hidden>Will be created as <code></code></div>`,
        showCancelButton: true,
        confirmButtonText: "Create and switch",
        cancelButtonText: "Cancel",
        reverseButtons: true,
        didOpen: () => {
          const input = window.Swal.getInput();
          const preview = window.Swal.getHtmlContainer().querySelector("[data-branch-preview]");
          const code = preview.querySelector("code");
          input.addEventListener("input", () => {
            const normalized = normalizeBranchName(input.value);
            code.textContent = normalized;
            preview.hidden = normalized === "" || normalized === input.value.trim();
          });
        },
        preConfirm: (value) => {
          const normalized = normalizeBranchName(value);
          if (!normalized) {
            window.Swal.showValidationMessage("A branch name is required.");
            return false;
          }
          return normalized;
        },
      });
      if (!result.isConfirmed || !result.value) return;
      name = result.value;
    } else {
      name = normalizeBranchName(await promptText({
        title: "New branch",
        placeholder: "feature/name",
        confirmText: "Create and switch",
      }) || "");
      if (!name) return;
    }
    // A branch is created here and touches no remote, so it opens no bridge.
    const data = await gitRun("branch", { branch: name }, `Creating "${name}"…`, "branch");
    if (!data) return;
    notifySuccess(`On the new branch "${name}".`);
    void loadGitStatus();
  }

  async function cloneDialog() {
    const url = await promptText({
      title: "Clone repository",
      html: `<div class="text-secondary small">Cloned straight into this project folder, which has to be empty. Authentication is whatever git on this host can already do.</div>`,
      placeholder: "git@host:owner/repo.git",
      confirmText: "Clone",
    });
    if (!url) return;
    const data = await gitRun("clone", { url }, "Cloning…", "clone");
    if (!data) return;
    notifySuccess("Cloned.");
    await loadTree();
    await afterGitMutation();
  }

  // After a write that may have moved the working copy under the editor, a
  // checkout or a pull, everything follows: the caches go, clean tabs read
  // the disk again, and the fresh status paints tree, tabs and statusbar. A
  // dirty buffer stays what it is: unsaved work belongs to the person in
  // front of it, never to a branch move.
  async function afterGitMutation() {
    dropChangeHeads();
    await reloadCleanTabs();
    void loadGitStatus();
    void refreshDiffHead();
    void loadCommitInfo();
  }

  // applyDiskContent puts what the disk answered into a tab: a fresh document,
  // the version those very bytes carry, and the views that read the buffer. It
  // is the one place a tab takes the disk over, so the version can never be
  // left behind by one of them.
  async function applyDiskContent(tab, data) {
    const isActive = tab.path === activePath;
    tab.handle = await editor.createDoc(data.content || "", tab.name);
    tab.editorConfig = data.editorConfig || {};
    tab.version = data.version || "";
    tab.commentPos = null;
    tab.commentChanges = null;
    if (commentsFor(tab.path).length) void loadComments();
    markDirty(tab, false);
    if (isActive) {
      editor.showDoc(tab);
      void applyTabDiff(tab);
      void applyBlame(true);
      void applyChangeBars();
      paintComments();
    }
  }

  async function reloadCleanTabs() {
    for (const tab of [...tabs]) {
      if (tab.kind || tab.compare || tab.external || tab.dirty) continue;
      let data;
      try {
        data = await getJSON(`${base}/file?path=${encodeURIComponent(tab.path)}`, { signal });
      } catch (err) {
        if (signal.aborted) return;
        // Only the server's own no closes a tab: a file the new branch does not
        // hold answers a 4xx, and a clean tab of it has nothing left to show,
        // the way a deleted file's tab does. A request that never arrived says
        // nothing about the branch, and closing on it would take the whole open
        // set away over one hiccup on a bad line.
        if (!(err.status >= 400 && err.status < 500)) continue;
        await closeTab(tab.path, true);
        continue;
      }
      if (signal.aborted) return;
      if (data.binary) continue;
      // The version comes along even when the content did not move, and it is
      // free to trust: the token is over the content, so identical text is the
      // identical token whatever the branch move did to the timestamps.
      tab.version = data.version || "";
      if ((data.content || "") === editor.valueOf(tab, tab.path === activePath)) continue;
      await applyDiskContent(tab, data);
    }
  }

  // revertMenuItem is the one entry the tree rows and the changes list share:
  // one path back to HEAD. Only a path that carries a mark has anything to
  // revert, so a clean row does not offer it.
  function revertMenuItem(path, isDir) {
    if (!gitRepo) return null;
    if (!(isDir ? dirKind(path) : fileKind(path))) return null;
    return { label: "Revert changes", icon: "ti-arrow-back-up", danger: true, action: () => void revertPath(path, isDir) };
  }

  // revertPath discards what the working copy carries under one path, back to
  // HEAD. The confirmation is built from the status this page already holds,
  // so it says what the revert will hit before anything runs, and deletion is
  // said in so many words: a path without a state in HEAD, untracked or just
  // added, is not restored but deleted. The server asks status itself, so a
  // stale list here never widens what actually happens.
  async function revertPath(path, isDir) {
    const bare = (p) => (p.endsWith("/") ? p.slice(0, -1) : p);
    const under = (p) => bare(p) === path || (isDir && bare(p).startsWith(`${path}/`));
    const restored = [];
    const removed = [];
    const goneAfter = [];
    for (const entry of commitChanges) {
      if (!under(entry.path)) continue;
      const kind = gitKind(entry);
      if (kind === "untracked" || kind === "added") {
        removed.push(entry);
        goneAfter.push(bare(entry.path));
      } else {
        restored.push(entry);
        // A rename goes back as a tracked change, and still takes its target
        // off the disk: the old name comes back, the new one goes.
        if (kind === "renamed") goneAfter.push(bare(entry.path));
      }
    }
    let html = "";
    let confirmText = "Revert";
    if (!isDir) {
      const entry = restored.concat(removed).find((e) => bare(e.path) === path);
      const kind = entry ? gitKind(entry) : fileKind(path);
      if (kind === "untracked" || kind === "added") {
        html = `<div class="text-secondary">"${escapeHtml(path)}" has no state in HEAD. Reverting deletes the file.</div>`;
        confirmText = "Delete file";
      } else if (kind === "deleted") {
        html = `<div class="text-secondary">"${escapeHtml(path)}" comes back at its state in HEAD.</div>`;
      } else if (kind === "renamed" && entry && entry.from) {
        html = `<div class="text-secondary">The rename is taken back: "${escapeHtml(entry.from)}" comes back and "${escapeHtml(path)}" is deleted. Uncommitted changes are lost.</div>`;
      } else {
        html = `<div class="text-secondary">"${escapeHtml(path)}" goes back to its state in HEAD, staged edits included. Uncommitted changes are lost.</div>`;
      }
    } else {
      const parts = [];
      if (restored.length) parts.push(`${restored.length} tracked ${restored.length === 1 ? "change is" : "changes are"} restored`);
      if (removed.length) parts.push(`${removed.length} ${removed.length === 1 ? "file" : "files"} without a state in HEAD ${removed.length === 1 ? "is" : "are"} deleted`);
      html = parts.length
        ? `<div class="text-secondary">Everything under "${escapeHtml(path)}" goes back to HEAD: ${parts.join(", ")}. Uncommitted changes are lost.</div>`
        : `<div class="text-secondary">Everything under "${escapeHtml(path)}" goes back to its state in HEAD; files that have no state there are deleted.</div>`;
    }
    const ok = await confirmDialog({ title: `Revert "${path}"?`, html, confirmText });
    if (!ok) return;
    const data = await gitRun("revert", { path }, `Reverting "${path}"…`, "revert");
    if (!data) return;
    notifySuccess(`Reverted "${path}".`);
    await reloadRevertedTabs(path, isDir, goneAfter);
    await loadTree();
    void loadGitStatus();
  }

  // After a revert the disk under the path is HEAD again, and the open buffers
  // have to say so too: a file the revert deleted closes its tab the way a
  // delete does, everything else is read back from the disk, dirty buffers
  // included, because the revert was asked to discard exactly those changes.
  async function reloadRevertedTabs(path, isDir, gone) {
    const under = (p) => p === path || (isDir && p.startsWith(`${path}/`));
    const isGone = (p) => gone.some((g) => p === g || p.startsWith(`${g}/`));
    if (compareSelection && isGone(compareSelection)) compareSelection = null;
    for (const tab of [...tabs]) {
      // A comparison goes with either of its sides, like it does on a delete.
      if (tab.compare) {
        if (isGone(tab.compare.left) || isGone(tab.compare.right)) await closeTab(tab.path, true);
        continue;
      }
      if (isGone(tab.path)) {
        await closeTab(tab.path, true);
        continue;
      }
      if (tab.kind || tab.external || !under(tab.path)) continue;
      let data;
      try {
        data = await getJSON(`${base}/file?path=${encodeURIComponent(tab.path)}`, { signal });
      } catch (err) {
        if (signal.aborted) return;
        // Only the server's own no closes a tab, like reloadCleanTabs.
        if (err.status >= 400 && err.status < 500) await closeTab(tab.path, true);
        continue;
      }
      if (signal.aborted) return;
      if (data.binary) continue;
      await applyDiskContent(tab, data);
    }
  }

  function historyMenuItem(path) {
    if (!gitRepo) return null;
    return { label: "File history", icon: "ti-history", action: () => openFileHistory(path) };
  }

  function revDiffMenuItem(path) {
    if (!gitRepo || !editor.canDiff) return null;
    return { label: "Diff against revision", icon: "ti-versions", action: () => openRevPicker(path) };
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

  // fetchRev reads the file at a revision, the other side of a diff. HEAD is
  // the everyday one; the history and the revision picker put other names in
  // the same field.
  async function fetchRev(path, rev) {
    return getJSON(`${base}/git/file?path=${encodeURIComponent(path)}&rev=${encodeURIComponent(rev || DIFF_REV)}`, { signal });
  }

  async function fetchHead(path) {
    return fetchRev(path, DIFF_REV);
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
    if (!gitRepo || !editor.canDiff || !tab || tab.kind || tab.compare || tab.external) return null;
    return {
      label: tab.diffRev ? "Hide git diff" : "Show git diff",
      icon: "ti-git-compare",
      hint: "Ctrl+Alt+D",
      action: () => void toggleTabDiff(tab),
    };
  }

  async function toggleTabDiff(tab) {
    const next = tab.diffRev ? "" : DIFF_REV;
    if (tab.path === activePath) {
      if (next) closeDrawer();
      await applyDiff(next);
      return;
    }
    // Hiding only clears the wish the background tab carries; showing brings
    // the tab to the front, where activateTab builds the diff.
    tab.diffRev = next;
    if (!next) {
      tab.diffOriginal = null;
      persistTabs();
      return;
    }
    persistTabs();
    closeDrawer();
    activateTab(tab.path);
  }

  // diffFromTree reaches the same switch from a tree row: the active file
  // toggles in place, an open one comes to the front, a closed one opens into
  // the comparison.
  async function diffFromTree(path) {
    let tab = tabByPath(path);
    if (!tab) {
      await openPath(path);
      tab = tabByPath(path);
    }
    if (!tab || tab.kind || tab.compare || tab.external) return;
    await toggleTabDiff(tab);
  }

  // applyDiff compares the active tab against rev; an empty rev takes the
  // comparison off again. ask is false only where the person already answered
  // the size question.
  async function applyDiff(rev, { ask = true } = {}) {
    const tab = activeTab();
    if (!tab || tab.kind || tab.compare || tab.external || !editor.canDiff) return;
    const seq = ++diffSeq;
    const current = () => seq === diffSeq && activeTab() === tab;
    tab.diffRev = "";
    tab.diffOriginal = null;
    if (!rev) {
      await editor.setDiff({ mode: "off", name: tab.name, valid: current });
      status("");
      persistTabs();
      void applyChangeBars();
      return;
    }
    let data;
    try {
      data = await fetchRev(tab.path, rev);
    } catch (err) {
      if (!current()) return;
      if (!signal.aborted) status(err.message, "error");
      // The switch was cleared above, so the stored state has to hear about it
      // too: leaving it on means a reload comes back into a comparison the tab
      // is not in, and any later save of the set would drop it after all.
      persistTabs();
      void applyChangeBars();
      return;
    }
    if (!current()) return;
    if (data.binary) {
      status(data.reason === "large"
        ? "That revision is too large to diff."
        : "That revision holds binary content, there is nothing to diff.", "error");
      persistTabs();
      void applyChangeBars();
      return;
    }
    const original = data.content || "";
    const working = editor.valueOf(tab, true);
    if (ask && !(await withinDiffLimits(working, original))) {
      // Declined: the tab is not in a comparison, and a reload must not put it
      // in one and ask again.
      if (current()) {
        persistTabs();
        void applyChangeBars();
      }
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
      void applyChangeBars();
      return;
    }
    const label = rev === DIFF_REV ? "HEAD" : rev;
    status(data.exists === false ? `Not in ${label} yet` : rev === DIFF_REV ? "" : `Diff against ${label}`);
    persistTabs();
    void applyChangeBars();
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

  // refreshDiffHead follows a moved base under an open diff: the revision
  // side is fetched again and replaced in place. A branch name moves like
  // HEAD does, a tag or a hash answers the same text and the replace is a
  // no-op. Only that side moves, the buffer belongs to the person in front of
  // it, and the dirty marker and the undo history stay untouched.
  async function refreshDiffHead() {
    const tab = activeTab();
    if (!tab || tab.kind || tab.compare || tab.external || !tab.diffRev || tab.diffOriginal == null) return;
    const seq = diffSeq;
    try {
      const data = await fetchRev(tab.path, tab.diffRev);
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

  // ---- change bars -----------------------------------------------------------

  // What changed against HEAD, always visible in the gutter, no switch. The
  // HEAD text is cached on the tab and dropped whenever HEAD may have moved;
  // the bars themselves follow the buffer live, so typing needs no request and
  // no call here. While a comparison or a diff is up the bars rest, those views
  // show the changes themselves.
  let changesSeq = 0;

  async function applyChangeBars() {
    const tab = activeTab();
    const textTab = tab && !tab.kind && !tab.compare && !tab.external ? tab : null;
    const seq = ++changesSeq;
    const valid = () => seq === changesSeq && activeTab() === tab;
    if (!textTab || !gitRepo || !editor.canChanges || textTab.diffRev) {
      void editor.setChanges(null, valid);
      return;
    }
    if (textTab.changeHead === undefined) {
      let data;
      try {
        data = await fetchHead(textTab.path);
      } catch (err) {
        // A missing answer is not "no changes": the cache stays empty, the next
        // trigger asks again, and the bars that are up stay up.
        if (!signal.aborted) console.warn("change bars unavailable", err);
        return;
      }
      if (!valid()) return;
      textTab.changeHead = data.exists === false || data.binary ? null : data.content || "";
    }
    const head = textTab.changeHead;
    if (head == null || overChangeLimits(head, editor.valueOf(textTab, true))) {
      void editor.setChanges(null, valid);
      return;
    }
    void editor.setChanges(head, valid);
  }

  // The same limits the diff asks about, applied silently: bars that are always
  // on cannot ask a question every time a huge file opens, so they stay away.
  function overChangeLimits(head, working) {
    const lines = Math.max(countLines(working), countLines(head));
    const kib = Math.max(byteLength(working), byteLength(head)) / 1024;
    return (diffSettings.maxLines > 0 && lines > diffSettings.maxLines)
      || (diffSettings.maxKiB > 0 && kib > diffSettings.maxKiB);
  }

  function dropChangeHeads() {
    for (const t of tabs) t.changeHead = undefined;
  }

  // ---- blame -----------------------------------------------------------------

  // Who last touched each line, in a gutter next to it. The switch belongs to
  // the file, not to the editor: it rides on the tab (`tab.blameOn`), persists
  // with the tab state, and is toggled from the file's own context menu, on its
  // tab or on its tree row.
  let blameFor = ""; // the path the blame in the editor belongs to
  let blameSeq = 0;

  function blameMenuItem(tab) {
    if (!gitRepo || !editor.canBlame || !tab || tab.kind || tab.compare || tab.external) return null;
    return {
      label: tab.blameOn ? "Hide git blame" : "Show git blame",
      icon: "ti-user-code",
      hint: "Ctrl+Alt+B",
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
    if (!tab || tab.kind || tab.compare || tab.external) return;
    toggleTabBlame(tab);
  }

  // applyBlame puts the gutter on the open file, or takes it off. It asks the
  // server again whenever the file changes under it, because a line that moved
  // belongs to a different commit than it did before.
  async function applyBlame(force = false) {
    const tab = activeTab();
    const textTab = tab && !tab.kind && !tab.compare && !tab.external ? tab : null;
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
      if (!has) {
        status(data.large
          ? "This file is too large to blame."
          : "Nothing to blame in this file, git does not know it yet.");
      }
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

  const commentableTab = (tab) => (tab && !tab.kind && !tab.compare && !tab.external ? tab : null);
  const commentsFor = (path) => comments.filter((c) => c.path === path);
  const commentAt = (path, line) => comments.find((c) => c.path === path && c.line === line) || null;

  function sortedComments() {
    return [...comments].sort((a, b) => (a.path === b.path ? a.line - b.line : a.path < b.path ? -1 : 1));
  }

  function paintComments() {
    commentsCountEl.textContent = comments.length ? String(comments.length) : "";
    commentsCountEl.hidden = comments.length === 0;
    if (editor.canComments) {
      const tab = commentableTab(activeTab());
      editor.setComments(tab ? commentsFor(tab.path).map((c) => ({ line: c.line, outdated: !!c.outdated })) : null);
    }
    if (sheetKind === "comments") renderCommentsSheet();
  }

  async function loadComments() {
    let data;
    try {
      data = await getJSON(`${base}/comments`, { signal });
    } catch (err) {
      void err;
      return;
    }
    const fresh = Array.isArray(data.comments) ? data.comments : [];
    for (const c of fresh) {
      const tab = commentableTab(tabByPath(c.path));
      if (!tab || !tab.dirty) continue;
      const held = comments.find((old) => old.id === c.id);
      if (held) {
        c.line = held.line;
        c.lineText = held.lineText;
        c.outdated = held.outdated;
        continue;
      }
      if (!tab.commentChanges || c.outdated) continue;
      const mapped = editor.mapSavedLine(tab, tab.path === activePath, c.line, tab.commentChanges);
      if (!mapped) continue;
      if (mapped.removed) {
        c.outdated = true;
        continue;
      }
      c.line = mapped.line;
      c.lineText = mapped.text;
    }
    comments = fresh;
    paintComments();
  }

  function findUniqueQuoteLine(doc, quote) {
    if (!quote) return 0;
    let at = 0;
    for (let n = 1; n <= doc.lines; n++) {
      if (doc.line(n).text === quote) {
        if (at) return 0;
        at = n;
      }
    }
    return at;
  }

  function onDocChanged(update) {
    const tab = commentableTab(activeTab());
    if (!tab) return;
    tab.commentChanges = tab.commentChanges ? tab.commentChanges.composeDesc(update.changes.desc) : update.changes.desc;
    const list = commentsFor(tab.path);
    if (!list.length) return;
    if (!tab.commentPos) tab.commentPos = new Map();
    const startDoc = update.startState.doc;
    const doc = update.state.doc;
    let repaint = false;
    let insertedText = null;
    const insertedHolds = (quote) => {
      if (!quote) return false;
      if (insertedText === null) {
        const parts = [];
        update.changes.iterChanges((fromA, toA, fromB, toB, inserted) => parts.push(inserted.toString()));
        insertedText = parts.join("\n");
      }
      return insertedText.includes(quote);
    };
    const rebindTo = (c, at) => {
      c.outdated = false;
      c.line = at;
      tab.commentPos.set(c.id, doc.line(at).from);
      repaint = true;
    };
    for (const c of list) {
      if (c.outdated) {
        if (c.line >= 1 && c.line <= doc.lines && doc.line(c.line).text === (c.lineText || "")) {
          rebindTo(c, c.line);
        } else if (insertedHolds(c.lineText || "")) {
          const at = findUniqueQuoteLine(doc, c.lineText || "");
          if (at) rebindTo(c, at);
        }
        continue;
      }
      let pos = tab.commentPos.get(c.id);
      if (pos === undefined) pos = startDoc.line(Math.max(1, Math.min(c.line, startDoc.lines))).from;
      const oldLine = startDoc.lineAt(Math.min(pos, startDoc.length));
      let removed = false;
      update.changes.iterChangedRanges((fromA, toA) => {
        if (toA > fromA && fromA <= oldLine.from && toA >= oldLine.to) removed = true;
      });
      if (removed) {
        const at = findUniqueQuoteLine(doc, c.lineText || "");
        if (at) {
          rebindTo(c, at);
          continue;
        }
        c.outdated = true;
        tab.commentPos.delete(c.id);
        repaint = true;
        continue;
      }
      const mapped = doc.lineAt(Math.min(update.changes.mapPos(pos, 1), doc.length));
      tab.commentPos.set(c.id, mapped.from);
      if (c.line !== mapped.number) {
        c.line = mapped.number;
        repaint = true;
      }
      if ((c.lineText || "") !== mapped.text) {
        c.lineText = mapped.text;
      }
    }
    if (repaint) paintComments();
  }

  async function syncCommentMoves(path) {
    const positions = commentsFor(path).filter((c) => !c.outdated).map((c) => ({ id: c.id, line: c.line, lineText: c.lineText || "" }));
    if (positions.length) {
      try {
        const res = await postJSON(`${base}/comments/move`, { path, comments: positions });
        if (!res.ok) throw new Error("refused");
      } catch (err) {
        void err;
      }
    }
    void loadComments();
  }

  function currentLineText(path, line, existing) {
    const tab = commentableTab(tabByPath(path));
    if (tab) {
      const lines = editor.valueOf(tab, tab.path === activePath).split("\n");
      if (line >= 1 && line <= lines.length) return lines[line - 1];
    }
    return existing && existing.lineText ? existing.lineText : "";
  }

  async function saveComment(payload) {
    try {
      const res = await postJSON(`${base}/comments`, payload);
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error || "The comment could not be saved.");
      if (data.gone || !data.comment) {
        await loadComments();
        return;
      }
      const idx = comments.findIndex((c) => c.id === data.comment.id);
      if (idx >= 0) comments[idx] = data.comment;
      else comments.push(data.comment);
      paintComments();
    } catch (err) {
      notifyError(err.message || "The comment could not be saved.");
    }
  }

  async function deleteComments(ids, { all = false } = {}) {
    try {
      const res = await postJSON(`${base}/comments/delete`, all ? { all: true } : { ids });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        throw new Error(data.error || "The comment could not be deleted.");
      }
      comments = all ? [] : comments.filter((c) => !ids.includes(c.id));
      paintComments();
    } catch (err) {
      notifyError(err.message || "The comment could not be deleted.");
    }
  }

  function openCommentModal({ existing, path, line, lineText }) {
    commentModalTitleEl.textContent = existing ? "Edit line comment" : "Line comment";
    commentModalPlaceEl.textContent = `${path}:${line}`;
    const code = (lineText || "").trim();
    commentModalCodeEl.textContent = code;
    commentModalCodeEl.hidden = !code;
    commentModalTextEl.value = existing ? existing.text : "";
    commentModalTextEl.classList.remove("is-invalid");
    commentModalSaveBtn.textContent = existing ? "Save" : "Add";
    return new Promise((resolve) => {
      const modal = window.bootstrap.Modal.getOrCreateInstance(commentModalEl);
      const wires = new AbortController();
      let result = null;
      const submit = () => {
        const text = commentModalTextEl.value.trim();
        if (!text) {
          commentModalTextEl.classList.add("is-invalid");
          commentModalTextEl.focus();
          return;
        }
        result = { text };
        modal.hide();
      };
      commentModalSaveBtn.addEventListener("click", submit, { signal: wires.signal });
      commentModalTextEl.addEventListener("input", () => commentModalTextEl.classList.remove("is-invalid"), { signal: wires.signal });
      commentModalTextEl.addEventListener("keydown", (ev) => {
        if ((ev.ctrlKey || ev.metaKey) && !ev.shiftKey && !ev.altKey && ev.key === "Enter") {
          ev.preventDefault();
          ev.stopPropagation();
          submit();
        }
      }, { signal: wires.signal });
      commentModalEl.addEventListener("shown.bs.modal", () => commentModalTextEl.focus(), { once: true, signal: wires.signal });
      commentModalEl.addEventListener("hidden.bs.modal", () => {
        wires.abort();
        resolve(result);
      }, { once: true });
      modal.show();
    });
  }

  async function editLineComment(path, line) {
    const existing = commentAt(path, line);
    const lineText = currentLineText(path, line, existing);
    const outcome = await openCommentModal({ existing, path, line, lineText });
    if (!outcome) return;
    if (existing && outcome.text === existing.text) return;
    await saveComment({ id: existing ? existing.id : "", path, line, lineText, text: outcome.text });
  }

  function openLineCommentAt(line) {
    const tab = commentableTab(activeTab());
    if (!tab) return;
    void editLineComment(tab.path, line);
  }

  async function copyPathLine(path, line) {
    try {
      await navigator.clipboard.writeText(`${path}:${line}`);
      status(`Copied ${path}:${line}`, "ok");
    } catch {
      status("Clipboard is not available.", "error");
    }
  }

  function openGutterMenu(tab, line, x, y) {
    const existing = commentAt(tab.path, line);
    openMenu({
      x,
      y,
      signal,
      items: [
        existing
          ? { label: "Edit comment", icon: "ti-message-circle", hint: "Ctrl+Alt+C", action: () => void editLineComment(tab.path, line) }
          : { label: "Add line comment", icon: "ti-message-plus", hint: "Ctrl+Alt+C", action: () => void editLineComment(tab.path, line) },
        existing ? { label: "Delete comment", icon: "ti-trash", danger: true, action: () => void deleteCommentDialog(existing) } : null,
        { label: "Copy path:line", icon: "ti-copy", action: () => void copyPathLine(tab.path, line) },
        blameMenuItem(tab),
      ],
    });
  }

  function lineNumberMenu(row, x, y) {
    if (!row) return false;
    const tab = commentableTab(activeTab());
    if (!tab) return false;
    const rect = row.getBoundingClientRect();
    const line = editor.lineAtGutter(row, rect.top + rect.height / 2);
    if (!line) return false;
    openGutterMenu(tab, line, x, y);
    return true;
  }

  function gutterClick(e) {
    if (e.defaultPrevented || !(e.target instanceof Element)) return;
    const gutters = e.target.closest(".cm-gutters");
    if (!gutters) return;
    const foldCell = e.target.closest(".cm-foldGutter .cm-gutterElement");
    if (foldCell && foldCell.childElementCount > 0) return;
    const tab = commentableTab(activeTab());
    if (!tab || menuJustClosed()) return;
    const cell = e.target.closest(".cm-gutterElement");
    const rect = cell ? cell.getBoundingClientRect() : null;
    const line = editor.lineAtGutter(gutters, rect ? rect.top + rect.height / 2 : e.clientY);
    if (!line) return;
    openGutterMenu(tab, line, e.clientX, e.clientY);
  }

  function commentsMarkdown() {
    const parts = [`Line comments in ${name}:`];
    for (const comment of sortedComments()) {
      const quote = (comment.lineText || "").trim();
      const lines = [`${comment.path}:${comment.line}${comment.outdated ? " (outdated)" : ""}`, comment.text];
      if (quote) lines.push(`> ${quote}`);
      parts.push(lines.join("\n"));
    }
    return parts.join("\n\n");
  }

  async function copyCommentsMarkdown() {
    if (!comments.length) return;
    try {
      await navigator.clipboard.writeText(commentsMarkdown());
      notifySuccess("Copied the line comments as Markdown.");
    } catch {
      notifyError("Clipboard is not available.");
    }
  }

  async function clearCommentsDialog() {
    const count = comments.length;
    if (!count) return;
    if (!(await confirmDialog({
      title: "Delete all comments?",
      text: count === 1 ? "This removes the one comment of this project." : `This removes all ${count} comments of this project.`,
      confirmText: "Delete",
    }))) return;
    await deleteComments([], { all: true });
  }

  async function jumpToComment(comment) {
    closeSheet();
    await openPath(comment.path);
    const tab = commentableTab(tabByPath(comment.path));
    if (!tab) return;
    editor.jumpTo(comment.line, 0);
  }

  function openCommentsSheet() {
    openSheet("comments", "Line comments");
    renderCommentsSheet();
    focusSheetTop();
  }

  function renderCommentsSheet() {
    if (sheetKind !== "comments") return;
    repaintSheet(sheetBodyEl, paintCommentsSheet);
  }

  function commentCellMenuItems(comment) {
    return [
      { label: "Go to line", icon: "ti-arrow-right", action: () => void jumpToComment(comment) },
      { label: "Edit", icon: "ti-pencil", action: () => void editLineComment(comment.path, comment.line) },
      { label: "Delete", icon: "ti-trash", danger: true, action: () => void deleteCommentDialog(comment) },
    ];
  }

  async function deleteCommentDialog(comment) {
    if (!(await confirmDialog({
      title: "Delete this comment?",
      html: `<div>${escapeHtml(`${comment.path}:${comment.line}`)}</div><div class="text-secondary">${escapeHtml(comment.text)}</div>`,
      confirmText: "Delete",
    }))) return;
    await deleteComments([comment.id]);
  }

  function commentCell(comment) {
    let sub = comment.text;
    let subNodes;
    let title = comment.text;
    if (comment.outdated) {
      sub = undefined;
      const wrap = document.createElement("span");
      wrap.className = "d-flex flex-column min-w-0";
      const textEl = document.createElement("span");
      textEl.className = "text-truncate";
      textEl.textContent = comment.text;
      const oldEl = document.createElement("span");
      oldEl.className = "text-orange text-truncate";
      const quote = (comment.lineText || "").trim();
      oldEl.textContent = quote ? `Outdated · was: ${quote}` : "Outdated";
      wrap.append(textEl, oldEl);
      subNodes = [wrap];
      title = `${comment.text}\n${oldEl.textContent}`;
    }
    const cell = sheetActionRow({
      icon: "ti-message-circle",
      iconClass: comment.outdated ? "text-orange" : undefined,
      label: { head: comment.path, tail: `:${comment.line}` },
      sub,
      subNodes,
      title,
      onClick: (event) => {
        const rect = event.currentTarget.getBoundingClientRect();
        openMenu({
          x: Math.round(event.clientX || rect.left),
          y: Math.round(event.clientY || rect.bottom + 4),
          items: commentCellMenuItems(comment),
          signal,
        });
      },
    });
    const col = document.createElement("div");
    col.className = "col-12 col-lg-6";
    col.appendChild(cell);
    return col;
  }

  function paintCommentsSheet() {
    if (sheetKind !== "comments") return;
    sheetBodyEl.replaceChildren();
    const list = sortedComments();
    sheetBodyEl.appendChild(sheetActionRow({
      icon: "ti-copy",
      label: "Copy as Markdown",
      sub: "file, line and comment, the code line quoted",
      disabled: !list.length,
      onClick: () => {
        closeSheet();
        void copyCommentsMarkdown();
      },
    }));
    sheetBodyEl.appendChild(sheetActionRow({
      icon: "ti-trash",
      iconClass: "text-danger",
      label: "Delete all comments",
      disabled: !list.length,
      onClick: () => {
        closeSheet();
        void clearCommentsDialog();
      },
    }));
    sheetBodyEl.appendChild(gitDivider());
    if (!list.length) {
      const empty = document.createElement("div");
      empty.className = "text-secondary small px-3 py-2";
      empty.textContent = "No comments yet. Click a line number to add one.";
      sheetBodyEl.appendChild(empty);
      return;
    }
    const grid = document.createElement("div");
    grid.className = "row row-deck g-0";
    for (const comment of list) grid.appendChild(commentCell(comment));
    sheetBodyEl.appendChild(grid);
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
        // Two files are two versions: each side saves through the file route on
        // its own and answers for its own path alone.
        leftVersion: a.version || "",
        rightVersion: b.version || "",
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

  // writeCompareSide writes one side of a comparison, and answers the same
  // three words a tab's save does. A refused write asks the same two questions
  // here, per side and per path: each half of a comparison is a real file with
  // a version of its own.
  async function writeCompareSide(tab, side, content) {
    const state = tab.compare;
    const path = state[side];
    try {
      state[`${side}Version`] = await writeFile(path, content, state[`${side}Version`]);
    } catch (err) {
      if (err.conflict === "deleted") {
        if (!(await askRecreateFile(path))) return "kept";
        state[`${side}Version`] = await writeFile(path, content, "");
      } else if (err.conflict === "changed") {
        if (!(await askReloadOverBuffer(path))) return "kept";
        return (await reloadCompareSide(tab, side)) ? "reloaded" : "kept";
      } else {
        throw err;
      }
    }
    state[`${side}Doc`] = content;
    state[`${side}Saved`] = content;
    return "saved";
  }

  // reloadCompareSide replaces one half of a comparison with what is on the
  // disk. The panes hold what was typed, so they are taken onto the tab first
  // and the reloaded side then replaces its own half: reading them back after
  // the assignment would put the buffer straight back over the disk.
  async function reloadCompareSide(tab, side) {
    const state = tab.compare;
    const data = await getJSON(`${base}/file?path=${encodeURIComponent(state[side])}`, { signal });
    if (signal.aborted || data.binary) return false;
    const live = tab.path === activePath && editor.comparing();
    if (live) editor.captureCompare(tab);
    const text = data.content || "";
    state[`${side}Doc`] = text;
    state[`${side}Saved`] = text;
    state[`${side}Version`] = data.version || "";
    if (live) await showCompare(tab);
    return true;
  }

  async function saveCompareSide(side) {
    const tab = activeTab();
    if (!tab || !tab.compare || !editor.comparing()) return;
    const path = tab.compare[side];
    const content = editor.compareValue(side);
    status("Saving…");
    try {
      const result = await writeCompareSide(tab, side, content);
      syncCompareBar();
      status(saveOutcome(result, path), result === "kept" ? "error" : "ok");
    } catch (err) {
      status(err.message, "error");
    }
  }

  // saveCompareTab saves whatever a comparison carries unsaved, both sides if
  // both moved. It is what the ordinary save paths (Ctrl+S, Save all) do with a
  // compare tab: a synthetic path is nothing the file route could write. The
  // outcome is the worse of the two sides, so a comparison whose one half was
  // refused never reads as saved.
  async function saveCompareTab(tab) {
    if (tab.path === activePath && editor.comparing()) editor.captureCompare(tab);
    const state = tab.compare;
    let outcome = "saved";
    for (const side of ["left", "right"]) {
      const content = state[`${side}Doc`];
      if (content === state[`${side}Saved`]) continue;
      const result = await writeCompareSide(tab, side, content);
      if (result === "kept" || (result === "reloaded" && outcome === "saved")) outcome = result;
    }
    if (tab.path === activePath && editor.comparing()) {
      syncCompareBar();
    } else {
      state.leftDirty = state.leftDoc !== state.leftSaved;
      state.rightDirty = state.rightDoc !== state.rightSaved;
      markDirty(tab, state.leftDirty || state.rightDirty);
    }
    return outcome;
  }

  // ---- file actions ----------------------------------------------------------

  // A save carries the version the file was loaded with, and the server writes
  // only while that version still describes what is on the disk, so an open
  // buffer can never write over what a coder did to the same working copy in
  // the meantime. What comes back is the version of what was just written, so
  // saving twice in a row asks nothing. A refusal is not an error to show, it
  // is a question to ask, and it travels as one: err.conflict says "changed"
  // when somebody else wrote the file and "deleted" when it is gone.
  async function writeFile(path, content, version) {
    const res = await postForm(`${base}/file`, { path, content, version: version || "" });
    if (res.status === 409) {
      const data = await res.json().catch(() => null);
      const err = new Error((data && data.error) || "Failed to save file.");
      err.status = 409;
      if (data && (data.conflict === "changed" || data.conflict === "deleted")) err.conflict = data.conflict;
      throw err;
    }
    await ensureOk(res, "Failed to save file.");
    const data = await res.json().catch(() => null);
    return (data && data.version) || "";
  }

  // The two ways out of a refused save, and there is no third one anywhere: a
  // save never writes over what it did not see, so neither dialog offers to
  // force it through. Cancel is the same answer in both, the buffer stands
  // exactly as it is and nothing is written.
  function askReloadOverBuffer(path) {
    return confirmDialog({
      title: `"${path}" changed on disk.`,
      html: `<div class="text-secondary">Somebody wrote the file after you opened it, a coder or git, and nothing has been saved. <em>Reload</em> replaces what is in the editor with what is on the disk, so your unsaved changes are gone. <em>Cancel</em> keeps them and writes nothing.</div>`,
      confirmText: "Reload",
    });
  }

  function askRecreateFile(path) {
    return confirmDialog({
      title: `"${path}" no longer exists on the server.`,
      html: `<div class="text-secondary">The file was deleted after you opened it, by a coder or by git, and nothing has been written. <em>Create again</em> writes what is in the editor as a new file. <em>Cancel</em> keeps the buffer and writes nothing.</div>`,
      confirmText: "Create again",
    });
  }

  // saveOutcome puts one of the three words a save ends on into a status line.
  // "kept" says nothing was written and does not repeat why: the dialog that
  // asked said it a second ago.
  function saveOutcome(result, label) {
    if (result === "reloaded") return `Reloaded ${label} from disk`;
    if (result === "kept") return "Not saved";
    return `Saved ${label}`;
  }

  // reloadTabFromDisk reads the file back over a tab whatever its buffer holds.
  // It is the way out of a save the server refused as changed: somebody asked
  // for the disk's state, so the buffer goes. It answers false when there is
  // nothing to put in the buffer, a text file that is a binary one now.
  async function reloadTabFromDisk(tab) {
    const data = await getJSON(`${base}/file?path=${encodeURIComponent(tab.path)}`, { signal });
    if (signal.aborted || data.binary) return false;
    await applyDiskContent(tab, data);
    return true;
  }

  // saveTab writes one tab and says what happened to it, because a save the
  // server refused is not a failure: "saved" was written, "reloaded" is the
  // buffer replaced by the disk, "kept" is nothing written and the buffer
  // untouched. Only a real failure throws.
  async function saveTab(tab) {
    if (tab.compare) return await saveCompareTab(tab);
    // A file outside the project has no write route and its buffer cannot
    // move anyway. The guard stands because a save is the one thing that
    // must never find a way in: the write path would take the absolute
    // path for a relative one and create it inside the project.
    if (tab.external) return "kept";
    const content = editor.valueOf(tab, tab.path === activePath);
    let version;
    try {
      version = await writeFile(tab.path, content, tab.version);
    } catch (err) {
      if (err.conflict === "deleted") {
        if (!(await askRecreateFile(tab.path))) return "kept";
        // No version, which is the create path: whoever answered that dialog
        // asked for exactly that, a new file with the buffer in it.
        version = await writeFile(tab.path, content, "");
      } else if (err.conflict === "changed") {
        if (!(await askReloadOverBuffer(tab.path))) return "kept";
        return (await reloadTabFromDisk(tab)) ? "reloaded" : "kept";
      } else {
        throw err;
      }
    }
    tab.version = version;
    editor.markSaved(tab, tab.path === activePath);
    markDirty(tab, false);
    tab.commentChanges = null;
    if (commentsFor(tab.path).length) void syncCommentMoves(tab.path);
    return "saved";
  }

  async function save() {
    const tab = activeTab();
    if (!tab || !tab.dirty) return;
    status("Saving…");
    saveBtn.disabled = true;
    try {
      const result = await saveTab(tab);
      // A buffer that stands is still unsaved, and the button was disabled for
      // a save that never happened.
      if (result !== "saved") updateActionStates();
      status(saveOutcome(result, tab.compare ? tab.name : tab.path), result === "kept" ? "error" : "ok");
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
      // Each file is refused on its own, so a batch can end with some written
      // and some not, and "Saved 3 files" would cover that up.
      const results = [];
      for (const tab of dirtyTabs) results.push(await saveTab(tab));
      const saved = results.filter((r) => r === "saved").length;
      if (saved === results.length) {
        status(results.length === 1
          ? saveOutcome("saved", dirtyTabs[0].compare ? dirtyTabs[0].name : dirtyTabs[0].path)
          : `Saved ${results.length} files`, "ok");
      } else {
        status(saved === 0 ? "Nothing was saved" : `Saved ${saved} of ${results.length} files`, "error");
      }
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
      const goneComments = comments.filter((c) => gone(c.path)).map((c) => c.id);
      if (goneComments.length) void deleteComments(goneComments);
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
      const movedPath = moved(tab.path);
      // A renamed file keeps its buffer, not its HEAD text: what HEAD has under
      // the new name is a different question, usually "nothing yet".
      if (movedPath !== tab.path) tab.changeHead = undefined;
      tab.path = movedPath;
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
    let movedComments = false;
    for (const c of comments) {
      const next = moved(c.path);
      if (next !== c.path) {
        c.path = next;
        movedComments = true;
      }
    }
    if (movedComments) paintComments();
    const tab = activeTab();
    if (tab && !tab.compare && tab.path.startsWith(newPath)) {
      if (tab.kind) renderViewer(tab);
      else editor.refreshLanguage(tab.name);
    }
    renderTabs();
    updateActionStates();
    syncCompareBar();
    syncPreview();
    void applyChangeBars();
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

  const NOT_TEXT = "That file does not open as text, there is nothing to copy.";

  async function copyContents(path) {
    const tab = tabByPath(path);
    if (tab && tab.kind) {
      status(NOT_TEXT, "error");
      return;
    }
    let text;
    if (tab && tab.dirty) {
      text = editor.valueOf(tab, tab.path === activePath);
    } else {
      status("Loading…");
      try {
        const data = await getJSON(`${base}/file?path=${encodeURIComponent(path)}`, { signal });
        if (signal.aborted) return;
        if (data.binary) {
          status(NOT_TEXT, "error");
          return;
        }
        text = data.content || "";
      } catch (err) {
        status(err.message, "error");
        return;
      }
    }
    try {
      await navigator.clipboard.writeText(text);
      status(`Copied the contents of ${path}`, "ok");
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
        const box = treeEl.getBoundingClientRect();
        const rect = row.getBoundingClientRect();
        if (rect.top < box.top || rect.bottom > box.top + treeEl.clientHeight) {
          row.scrollIntoView({ block: "center" });
        }
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

  // The entries have to be read before the event handler returns, the items
  // list is emptied afterwards. What this answers stays usable from the walk.
  function transferEntries(items) {
    return [...(items || [])]
      .map((item) => (item.kind === "file" && item.webkitGetAsEntry ? item.webkitGetAsEntry() : null))
      .filter(Boolean);
  }

  // collectDrop returns {file, rel} pairs for everything in the drop, or null
  // when the browser hands over no directory entries (plain files then).
  async function collectDrop(entries) {
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
        const entries = transferEntries(dropped.items);
        const files = [...(dropped.files || [])];
        void (async () => {
          try {
            const walked = await collectDrop(entries);
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

  function wirePaste() {
    root.addEventListener("paste", (e) => {
      if (e.target instanceof Element && e.target.closest("[data-editor-term-panel]")) return;
      const clip = e.clipboardData;
      if (!clip) return;
      const entries = transferEntries(clip.items);
      const files = [...(clip.files || [])];
      if (!entries.length && !files.length) return;
      e.preventDefault();
      e.stopPropagation();
      const dir = targetDir();
      void (async () => {
        try {
          const walked = await collectDrop(entries);
          await uploadFiles(walked || files, dir, { confirmFirst: !!walked });
        } catch (err) {
          status(err.message, "error");
        }
      })();
    }, { signal, capture: true });
  }

  // ---- markdown preview ------------------------------------------------------

  // The preview is a per file switch like the diff and the blame gutter: it
  // says how you want to read this one file, so it rides on its tab and is
  // reached from the file's own context menu.
  function previewVisible() {
    const tab = activeTab();
    return !!(tab && !tab.kind && !tab.compare && !tab.external && tab.previewOn && hasPreview(tab.name));
  }

  function previewMenuItem(tab) {
    if (!tab || tab.kind || tab.compare || tab.external || !hasPreview(tab.name)) return null;
    return {
      label: tab.previewOn ? "Hide preview" : "Show preview",
      icon: tab.previewOn ? "ti-eye-off" : "ti-eye",
      hint: "Ctrl+Alt+P",
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
    if (!tab || tab.kind || tab.compare || tab.external) return;
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

  // The palette has two modes: "files" ranks paths, "search" greps file contents
  // and jumps to the matched line. Both ask the server, which answers from an
  // index of the whole project.
  let quickOpenMode = "files";
  let quickOpenFiles = null;
  let quickOpenMatches = [];
  let quickOpenActive = 0;
  let searchQuery = "";
  let searchSeq = 0;
  let searchTimer = 0;
  let filesSeq = 0;
  let filesTimer = 0;

  async function openQuickOpen(mode = "files") {
    closeDrawer();
    closeSheet();
    quickOpenMode = mode;
    quickOpenEl.hidden = false;
    quickOpenInput.value = "";
    quickOpenInput.placeholder = mode === "search" ? "Find in files…" : "Go to file…";
    quickOpenMatches = [];
    syncQuickOpenRegex();
    quickOpenInput.focus();
    if (mode === "search") {
      quickOpenList.innerHTML = `<div class="editor-quickopen-empty text-secondary small">Type at least 2 characters to search file contents.</div>`;
      return;
    }
    quickOpenList.innerHTML = `<div class="editor-quickopen-empty text-secondary small">Loading…</div>`;
    runFileQuery("");
  }

  // Every way out lands here; the surface takes the focus back.
  function closeQuickOpen() {
    if (quickOpenEl.hidden) return;
    quickOpenEl.hidden = true;
    editor.focus();
  }

  // The palette used to receive every path in the project and rank them here,
  // which capped the list server side and made anything past the cap
  // unreachable. Ranking now happens on the server against an index of the whole
  // tree; this only debounces the keystrokes and draws what comes back.
  function scheduleFileQuery() {
    clearTimeout(filesTimer);
    // The rendered rows must never belong to an older query than what stands in
    // the box: filtering used to be synchronous, so pressing Enter right after
    // typing could only ever open a file that matched. Going to the server
    // reintroduces that gap, and both lines below close it. Advancing the
    // sequence matters as much as clearing the rows: a request already in flight
    // for the older query would otherwise still pass its own guard and paint its
    // rows back over the cleared list, and an Enter landing in that window opens
    // a file the query never matched. The debounce stays short because the
    // answer comes out of an index in single digit milliseconds.
    filesSeq++;
    quickOpenMatches = [];
    quickOpenList.innerHTML = `<div class="editor-quickopen-empty text-secondary small">Searching…</div>`;
    filesTimer = setTimeout(() => runFileQuery(quickOpenInput.value), 40);
  }

  async function runFileQuery(q) {
    const seq = ++filesSeq;
    try {
      const data = await getJSON(`${base}/files?q=${encodeURIComponent(splitLineSuffix(q).query)}`, { signal });
      // A slower answer to an older keystroke must not overwrite a newer one.
      if (seq !== filesSeq || quickOpenEl.hidden || quickOpenMode !== "files") return;
      quickOpenFiles = { files: data.files || [], truncated: !!data.truncated, total: data.total || 0 };
      renderQuickOpen();
    } catch (err) {
      if (seq !== filesSeq || quickOpenMode !== "files") return;
      quickOpenFiles = null;
      quickOpenList.innerHTML = `<div class="editor-quickopen-empty text-danger small">${escapeHtml(err.message)}</div>`;
    }
  }

  function renderQuickOpen() {
    if (quickOpenEl.hidden || !quickOpenFiles) return;
    quickOpenMatches = quickOpenFiles.files;
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
      item.title = path;
      item.innerHTML = `<i class="ti ti-file"></i><span class="editor-quickopen-name">${escapeHtml(baseName(path))}</span><span class="editor-quickopen-dir">${escapeHtml(parentDir(path))}</span>`;
      item.addEventListener("click", () => chooseQuickOpen(path));
      quickOpenList.appendChild(item);
    });
    if (quickOpenFiles.truncated) {
      const note = document.createElement("div");
      note.className = "editor-quickopen-empty text-secondary small";
      note.textContent = quickOpenFiles.total
        ? `Showing ${quickOpenMatches.length} of ${quickOpenFiles.total} matches, narrow the search.`
        : "Results are truncated, narrow the search.";
      quickOpenList.appendChild(note);
    }
  }

  function syncQuickOpenRegex() {
    const show = quickOpenMode === "search";
    quickOpenRegexBtn.hidden = !show;
    quickOpenInput.classList.toggle("pe-5", show);
    quickOpenRegexBtn.classList.toggle("active", !!editorSettings.search_regex);
    quickOpenRegexBtn.setAttribute("aria-pressed", editorSettings.search_regex ? "true" : "false");
  }

  function toggleSearchRegex() {
    editorSettings.search_regex = !editorSettings.search_regex;
    saveEditorSettings(editorSettings);
    syncQuickOpenRegex();
    quickOpenInput.focus();
    clearTimeout(searchTimer);
    const q = quickOpenInput.value.trim();
    if (q.length >= 2) runSearch(q);
    else scheduleSearch();
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
      const re = editorSettings.search_regex ? "&re=1" : "";
      const data = await getJSON(`${base}/search?q=${encodeURIComponent(q)}${re}`, { signal });
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
      item.title = `${match.path}:${match.line}`;
      const head = document.createElement("div");
      head.className = "editor-quickopen-match-head";
      // A usage outside the project carries an absolute path, and its end
      // is what says which file it is, so it is cut like the tab's hint.
      const dir = match.external ? externalHint(match.path) : parentDir(match.path);
      head.innerHTML = `<i class="ti ti-${match.external ? "lock" : "file"}"></i><span class="editor-quickopen-name">${escapeHtml(baseName(match.path))}:${match.line}</span><span class="editor-quickopen-dir">${escapeHtml(dir)}</span>`;
      const text = document.createElement("div");
      text.className = "editor-quickopen-match-text";
      text.append(...(typeof match.start === "number"
        ? markedRange(match.text, match.start, match.len)
        : markedFragments(match.text, searchQuery)));
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

  function markedRange(text, start, len) {
    const out = [];
    if (start > 0) out.push(document.createTextNode(text.slice(0, start)));
    if (len > 0) {
      const mark = document.createElement("mark");
      mark.textContent = text.slice(start, start + len);
      out.push(mark);
    }
    if (start + len < text.length) out.push(document.createTextNode(text.slice(start + len)));
    return out;
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

  function usagesNote(locations, res) {
    if (res.truncated) return `Only the first ${locations.length} usages are shown.`;
    if (res.outside) return `${res.outside} more outside the project.`;
    return "";
  }

  let usagesAll = [];
  let usagesNoteText = "";

  // Bottom sheet on a small screen, else the search panel in usages mode.
  function openUsages(word, locations, res) {
    const title = `${locations.length} ${locations.length === 1 ? "usage" : "usages"} of "${word}"`;
    if (mobileMedia.matches) {
      openUsagesSheet(word, locations, res, title);
      return;
    }
    closeDrawer();
    closeSheet();
    quickOpenMode = "usages";
    quickOpenEl.hidden = false;
    quickOpenInput.value = "";
    quickOpenInput.placeholder = title;
    syncQuickOpenRegex();
    searchQuery = word;
    // external travels along: it is what the row's jump reads to know
    // which of the two ways of opening a file it takes.
    usagesAll = locations.map((l) => ({ path: l.path, line: l.line, character: l.character, external: l.external, text: l.preview || "" }));
    usagesNoteText = usagesNote(locations, res);
    paintUsages(usagesAll, true);
    quickOpenInput.focus();
  }

  // The note describes the whole answer, so it only stands unfiltered.
  function paintUsages(rows, withNote) {
    renderSearchResults(rows, false);
    if (withNote && usagesNoteText) {
      const el = document.createElement("div");
      el.className = "editor-quickopen-empty text-secondary small";
      el.textContent = usagesNoteText;
      quickOpenList.appendChild(el);
    }
  }

  function usageHaystack(m) {
    return `${m.path}:${m.line} ${m.text || m.preview || ""}`;
  }

  function filterUsages() {
    const q = quickOpenInput.value.trim();
    if (!q) {
      paintUsages(usagesAll, true);
      return;
    }
    paintUsages(usagesAll.filter((m) => matchesTokens(usageHaystack(m), q)), false);
  }

  function openUsagesSheet(word, locations, res, title) {
    closeDrawer();
    openSheet("usages", title);
    const wrap = document.createElement("div");
    wrap.className = "p-2 border-bottom position-sticky top-0 bg-surface";
    const input = document.createElement("input");
    input.type = "text";
    input.className = "form-control form-control-sm";
    input.placeholder = "Filter usages";
    input.autocomplete = "off";
    input.spellcheck = false;
    input.setAttribute("aria-label", "Filter usages");
    wrap.appendChild(input);
    const listEl = document.createElement("div");
    const note = usagesNote(locations, res);
    const paint = () => {
      const q = input.value.trim();
      const rows = q ? locations.filter((loc) => matchesTokens(usageHaystack(loc), q)) : locations;
      listEl.replaceChildren(...rows.map((loc) => usagesSheetRow(word, loc)));
      if (!q && note) {
        const el = document.createElement("div");
        el.className = "text-secondary small px-3 py-2";
        el.textContent = note;
        listEl.appendChild(el);
      }
    };
    input.addEventListener("input", paint, { signal });
    paint();
    sheetBodyEl.append(wrap, listEl);
    focusSheetTop();
  }

  function usagesSheetRow(word, loc) {
    const row = document.createElement("div");
    row.className = "editor-sheet-row";
    const open = document.createElement("button");
    open.type = "button";
    open.className = "editor-sheet-open";
    open.title = `${loc.path}:${loc.line}`;
    const col = document.createElement("span");
    col.className = "d-flex flex-column min-w-0";
    const nameEl = document.createElement("span");
    nameEl.className = "editor-sheet-name text-truncate";
    nameEl.textContent = `${baseName(loc.path)}:${loc.line}`;
    const dirEl = document.createElement("span");
    dirEl.className = "editor-sheet-dir text-truncate";
    dirEl.textContent = loc.external ? externalHint(loc.path) : parentDir(loc.path) || "/";
    col.append(nameEl, dirEl);
    if (loc.preview) {
      const text = document.createElement("span");
      text.className = "editor-sheet-dir text-truncate";
      text.append(...markedFragments(loc.preview, word));
      col.append(text);
    }
    open.appendChild(col);
    open.addEventListener("click", () => {
      closeSheet();
      void goToLocation(loc);
    });
    row.append(open);
    return row;
  }

  function moveQuickOpenActive(delta) {
    if (quickOpenMatches.length === 0) return;
    quickOpenActive = (quickOpenActive + delta + quickOpenMatches.length) % quickOpenMatches.length;
    quickOpenList.querySelectorAll(".editor-quickopen-item").forEach((el, i) => {
      el.classList.toggle("active", i === quickOpenActive);
      if (i === quickOpenActive) el.scrollIntoView({ block: "nearest" });
    });
  }

  // A trailing :line, optionally :line:column, is how editors everywhere say
  // "open it there". It is stripped before the query goes to the server, which
  // matches paths and knows nothing about lines. A bare ":42" with no path in
  // front of it stays a literal query: there is no file it could mean.
  function splitLineSuffix(raw) {
    const m = /^(.*\S)\s*:(\d+)(?::\d+)?\s*$/.exec(raw);
    return m ? { query: m[1], line: Number(m[2]) } : { query: raw, line: 0 };
  }

  async function chooseQuickOpen(entry) {
    // Read the wanted line before the palette closes and takes the input with it.
    const fromSearch = typeof entry !== "string";
    const path = fromSearch ? entry.path : entry;
    const line = fromSearch ? entry.line : splitLineSuffix(quickOpenInput.value).line;
    const isUsage = quickOpenMode === "usages";
    closeQuickOpen();
    if (isUsage) {
      await goToLocation(entry);
      return;
    }
    await openPath(path);
    if (!line) return;
    const tab = activeTab();
    if (tab && tab.path === path && !tab.kind) editor.jumpTo(line, fromSearch ? entry.character || 0 : 0);
  }

  function wireQuickOpen() {
    quickOpenItem.addEventListener("click", () => openQuickOpen("files"), { signal });
    searchProjectItem.addEventListener("click", () => openQuickOpen("search"), { signal });
    quickOpenRegexBtn.addEventListener("click", toggleSearchRegex, { signal });
    quickOpenInput.addEventListener("input", () => {
      // A usages list is an answer, not a query.
      if (quickOpenMode === "usages") {
        filterUsages();
        return;
      }
      if (quickOpenMode === "search") scheduleSearch();
      else scheduleFileQuery();
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
    dropChangeHeads();
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
    // A signal without a project is the snapshot after a reconnect: the moves
    // of the gap were published to nobody and never come again, so this page
    // pulls everything itself, the way it does when it comes back to the
    // front.
    if (!event.detail || !event.detail.project) {
      catchUpGit();
      return;
    }
    if (event.detail.project !== name) return;
    // A moved base means every cached HEAD text may be from before the move,
    // so they all go; loadGitStatus rebuilds the bars for the open file.
    if (event.detail.base) dropChangeHeads();
    void loadGitStatus();
    if (event.detail.base) void refreshDiffHead();
  }, { signal });
  // Another device saved the commit panel or a commit spent it; a bare signal
  // (the snapshot after a reconnect) means catch up too.
  onServerEvent("commitdraft", (event) => {
    if (event.detail && event.detail.project && event.detail.project !== name) return;
    void pullCommitDraft();
  }, { signal });
  onServerEvent("linecomments", (event) => {
    if (event.detail && event.detail.project && event.detail.project !== name) return;
    void loadComments();
  }, { signal });
  // The indexing picture moved; a bare signal (the snapshot after a
  // reconnect) covers a page that opened or came back mid-indexing.
  onServerEvent("lsp", (event) => {
    if (event.detail && event.detail.project && event.detail.project !== name) return;
    void pullLSPIndex();
  }, { signal });
  // Nothing was published while this page was away, see catchUpGit.
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "visible") {
      catchUpGit();
      void pullLSPIndex();
    }
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
  window.addEventListener("keyup", (e) => {
    if (e.key === "Control" || e.key === "Meta") editor.clearLSPHint?.();
  }, { signal });
  window.addEventListener("blur", () => editor.clearLSPHint?.(), { signal });
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
  // there would make a long line unreadable. A mouse never swipes, a gesture
  // that starts on a selection is the selection's, and one in a focused editor
  // is the cursor's: dragging it along the line has to keep working while
  // someone types.
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
  // gesture the browser gives them, and so does a focused editor: while someone
  // works in the text, a sideways drag is the cursor's, not the file strip's.
  function syncSwipeZone() {
    if (!editorReady) return;
    const tab = activeTab();
    const on = !!editorSettings.line_wrap && !!tab && !tab.compare
      && !editor.hasSelection() && !editor.hasFocus();
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
  // The changes list carries the revert where the change is listed: a file row
  // reverts the file, a folder row everything under it.
  wireRowMenus(commitListEl, ".editor-commit-row", (row, x, y) => {
    const raw = row && (row.dataset.dir || row.dataset.path);
    if (!raw) return false;
    const isDir = !!row.dataset.dir || raw.endsWith("/");
    const item = revertMenuItem(raw.endsWith("/") ? raw.slice(0, -1) : raw, isDir);
    if (!item) return false;
    openMenu({ x, y, items: [item], signal });
    return true;
  }, { signal });
  wireTreeDrop();
  wirePaste();
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
  commentsItem.addEventListener("click", openCommentsSheet, { signal });
  wireRowMenus(root, ".cm-gutters .cm-gutterElement", lineNumberMenu, { signal });
  root.addEventListener("click", gutterClick, { signal });
  reindexItem.hidden = !lsp;
  reindexItem.addEventListener("click", () => {
    void lsp?.reindex();
    void pullLSPIndex();
  }, { signal });
  dockerItem.addEventListener("click", openDockerSheet, { signal });
  dockerStatusBtn.addEventListener("click", openDockerSheet, { signal });
  gitStatusBtn.addEventListener("click", () => openGitSheet(), { signal });
  termStatusBtn.addEventListener("click", toggleTermPanel, { signal });
  sheetCloseBtn.addEventListener("click", closeSheet, { signal });
  sheetEl.addEventListener("click", (e) => {
    if (e.target === sheetEl) closeSheet();
  }, { signal });
  // A row of an adopted menu did what it says; the sheet has served its purpose
  // and gets out of the way so the answer is visible. The settings keep their
  // sheet, they are selects and a switch, not one-shot actions; the docker
  // sheet keeps it too, its rows open a menu or start a run the sheet then
  // shows as busy.
  sheetBodyEl.addEventListener("click", (e) => {
    // A row whose handler opened another sheet is detached by the time this
    // bubbles; closing then would close what just replaced it, which is how
    // the branch picker used to vanish the moment it opened.
    if (!sheetBodyEl.contains(e.target)) return;
    if (sheetKind !== "settings" && sheetKind !== "docker" && sheetKind !== "git" && sheetKind !== "comments" && e.target.closest(".dropdown-item")) closeSheet();
  }, { signal });

  const projectSwitchEl = root.querySelector(".editor-project-switch");
  const projectDropEl = projectSwitchEl?.closest(".dropdown");
  const projectListEl = root.querySelector("[data-editor-project-list]");
  const projectFilterEl = root.querySelector("[data-editor-project-filter]");
  const projectEmptyEl = root.querySelector("[data-editor-project-empty]");
  let projectMenuOpen = false;
  let projectMenuIndex = -1;
  let projectMenuFromKey = false;
  let projectsInFlight = false;
  let projectsDirty = false;
  if (projectListEl) projectSort.sort(projectListEl);

  const projectRows = () => (projectListEl
    ? Array.from(projectListEl.querySelectorAll(".dropdown-item")).filter((row) => !row.hidden)
    : []);

  function paintProjectMenu() {
    if (!projectListEl) return;
    for (const row of projectListEl.querySelectorAll(".dropdown-item.selected")) {
      row.classList.remove("selected");
      row.removeAttribute("aria-current");
    }
    const rows = projectRows();
    if (!projectMenuOpen || projectMenuIndex < 0 || !rows.length) return;
    projectMenuIndex = Math.min(projectMenuIndex, rows.length - 1);
    const selected = rows[projectMenuIndex];
    selected.classList.add("selected");
    selected.setAttribute("aria-current", "true");
    const menu = projectListEl.closest(".editor-project-menu");
    if (!menu) return;
    const top = selected.offsetTop;
    const bottom = top + selected.offsetHeight;
    if (top < menu.scrollTop) menu.scrollTop = top;
    else if (bottom > menu.scrollTop + menu.clientHeight) menu.scrollTop = bottom - menu.clientHeight;
  }

  function applyProjectFilter(fromInput) {
    if (!projectListEl) return;
    const query = (projectFilterEl?.value || "").trim();
    const marked = projectRows()[projectMenuIndex];
    for (const row of projectListEl.querySelectorAll(".dropdown-item")) {
      row.hidden = Boolean(query) && !matchesTokens(row.dataset.projectName || "", query);
    }
    const rows = projectRows();
    if (projectEmptyEl) projectEmptyEl.hidden = rows.length > 0;
    if (fromInput) projectMenuIndex = rows.length ? 0 : -1;
    else projectMenuIndex = rows.indexOf(marked);
    paintProjectMenu();
  }

  function moveProjectMenuSelection(delta) {
    const rows = projectRows();
    if (!rows.length) return;
    if (projectMenuIndex < 0) projectMenuIndex = delta > 0 ? 0 : rows.length - 1;
    else projectMenuIndex = (projectMenuIndex + delta + rows.length) % rows.length;
    paintProjectMenu();
  }

  function commitProjectMenuSelection() {
    const selected = projectRows()[projectMenuIndex];
    if (selected) selected.click();
  }

  function openProjectMenu() {
    if (!projectSwitchEl || !window.bootstrap?.Dropdown) return;
    projectMenuFromKey = true;
    window.bootstrap.Dropdown.getOrCreateInstance(projectSwitchEl).show();
  }

  function closeProjectMenu() {
    if (projectSwitchEl) window.bootstrap?.Dropdown.getInstance(projectSwitchEl)?.hide();
  }

  projectDropEl?.addEventListener("shown.bs.dropdown", () => {
    projectMenuOpen = true;
    const rows = projectRows();
    projectMenuIndex = projectMenuFromKey ? Math.max(rows.findIndex((row) => row.classList.contains("active")), 0) : -1;
    projectMenuFromKey = false;
    paintProjectMenu();
    if (pointerMedia.matches) projectFilterEl?.focus();
    else projectSwitchEl?.blur();
  }, { signal });
  projectDropEl?.addEventListener("hidden.bs.dropdown", () => {
    projectMenuOpen = false;
    projectMenuIndex = -1;
    projectMenuFromKey = false;
    if (projectFilterEl) projectFilterEl.value = "";
    applyProjectFilter(false);
  }, { signal });
  projectFilterEl?.addEventListener("input", () => applyProjectFilter(true), { signal });
  window.addEventListener("keydown", (e) => {
    if (!projectMenuOpen) return;
    const actions = {
      ArrowDown: () => moveProjectMenuSelection(1),
      ArrowUp: () => moveProjectMenuSelection(-1),
      Enter: () => commitProjectMenuSelection(),
      Escape: () => closeProjectMenu(),
    };
    const action = actions[e.key];
    if (!action) return;
    e.preventDefault();
    e.stopPropagation();
    action();
  }, { capture: true, signal });

  async function refreshProjects() {
    if (!projectListEl) return;
    if (projectsInFlight) {
      projectsDirty = true;
      return;
    }
    projectsInFlight = true;
    const ret = new URLSearchParams(window.location.search).get("return") || "";
    const html = await getText(`${base}/projects?return=${encodeURIComponent(ret)}`, { signal }).catch(() => "");
    if (html) {
      const marked = projectRows()[projectMenuIndex]?.dataset.projectName || "";
      projectListEl.innerHTML = html;
      projectSort.sort(projectListEl);
      applyProjectFilter(false);
      const kept = marked ? projectRows().findIndex((row) => row.dataset.projectName === marked) : -1;
      if (kept !== -1) {
        projectMenuIndex = kept;
        paintProjectMenu();
      }
    }
    projectsInFlight = false;
    if (projectsDirty) {
      projectsDirty = false;
      void refreshProjects();
    }
  }
  onServerEvent("projects", () => void refreshProjects(), { signal });

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

  const termPanelEl = root.querySelector("[data-editor-term-panel]");
  const termSplitterEl = root.querySelector("[data-editor-term-splitter]");
  const termTabsHostEl = root.querySelector("[data-editor-term-tabs-host]");
  const termBodyEl = root.querySelector("[data-editor-term-body]");
  const termEmptyEl = root.querySelector("[data-editor-term-empty]");
  const termItem = root.querySelector("[data-editor-term-item]");
  const termHideBtn = root.querySelector("[data-editor-term-hide]");
  const termPlusBtn = root.querySelector("[data-editor-term-plus]");
  const termNewBtns = [root.querySelector("[data-editor-term-new]"), root.querySelector("[data-editor-term-new-empty]")];
  const termFootsHostEl = root.querySelector("[data-editor-term-foots-host]");
  const termModalsHostEl = root.querySelector("[data-editor-term-modals-host]");
  const termResumeHostEl = root.querySelector("[data-editor-term-resume-host]");
  const termOpenKey = `${TERM_OPEN_KEY}:${name}`;
  const termActiveKey = `${TERM_ACTIVE_KEY}:${name}`;
  let termOpen = store.get(termOpenKey, "") === "1";
  let termLoaded = false;
  let termActiveId = store.get(termActiveKey, "") || null;
  let termSeq = 0;
  let termDragging = false;
  let termRefreshHeld = false;
  let termSuppressClick = false;
  let termFocusOwner = false;
  let termResumeExpanded = false;
  let termMenuOpen = false;
  let termMenuFromKey = false;
  let termMenuIndex = -1;
  const termApplies = () => pointerMedia.matches && !mobileMedia.matches;
  const termPanesEl = () => termBodyEl.querySelector("[data-editor-term-panes]");
  const termPaneFor = (id) => (id ? termBodyEl.querySelector(`[data-term-pane="${CSS.escape(id)}"]`) : null);
  const termTabFor = (id) => (id ? termTabsHostEl.querySelector(`[data-term-tab="${CSS.escape(id)}"]`) : null);
  const termIds = () => [...termBodyEl.querySelectorAll("[data-term-pane]")].map((el) => el.getAttribute("data-term-pane"));
  const termTabIds = () => [...termTabsHostEl.querySelectorAll("[data-term-tab]")].map((el) => el.getAttribute("data-term-tab"));

  function revealTermTab(tab) {
    if (!tab) return;
    const strip = tab.closest("[data-editor-term-tabs]");
    if (!strip || strip.clientWidth === 0) return;
    const left = tab.getBoundingClientRect().left - strip.getBoundingClientRect().left + strip.scrollLeft;
    if (left < strip.scrollLeft || left + tab.offsetWidth > strip.scrollLeft + strip.clientWidth) {
      strip.scrollLeft = left - (strip.clientWidth - tab.offsetWidth) / 2;
    }
  }

  function paintTermTabs() {
    for (const tab of termTabsHostEl.querySelectorAll("[data-term-tab]")) {
      const on = tab.getAttribute("data-term-tab") === termActiveId;
      tab.classList.toggle("active", on);
      tab.setAttribute("aria-selected", on ? "true" : "false");
    }
    revealTermTab(termTabFor(termActiveId));
    for (const pane of termBodyEl.querySelectorAll("[data-term-pane]")) {
      pane.classList.toggle("active", pane.getAttribute("data-term-pane") === termActiveId);
    }
    termEmptyEl.hidden = termIds().length > 0;
  }

  const termStripResize = new ResizeObserver(() => revealTermTab(termTabFor(termActiveId)));
  termStripResize.observe(termTabsHostEl);

  async function mountTermIsland(pane) {
    for (const other of document.querySelectorAll("terminal-attach[active]")) other.removeAttribute("active");
    const id = pane.getAttribute("data-term-pane");
    const attach = document.createElement("terminal-attach");
    attach.className = "attach-terminal editor-term-island min-w-0 w-100 position-relative";
    attach.setAttribute("terminal-id", id);
    attach.setAttribute("embedded", "");
    attach.setAttribute("stream-url", pane.getAttribute("data-stream-url") || "");
    attach.setAttribute("resize-url", pane.getAttribute("data-resize-url") || "");
    if (pane.hasAttribute("data-scroll-history")) attach.setAttribute("scroll-history", "");
    const input = document.createElement("terminal-input");
    input.setAttribute("terminal-id", id);
    input.setAttribute("input-url", pane.getAttribute("data-input-url") || "");
    if (pane.hasAttribute("data-scroll-history")) input.setAttribute("scroll-history", "");
    pane.append(attach, input);
    await window.app?.loadElements?.(pane);
    const upload = termFootsHostEl.querySelector(`[data-term-foot="${CSS.escape(id)}"] coder-file-upload`);
    if (upload) {
      const parent = upload.parentElement;
      const next = upload.nextSibling;
      upload.remove();
      parent.insertBefore(upload, next);
    }
  }

  function markTermRead(id) {
    const icon = termTabFor(id)?.querySelector(".dc-term-icon");
    if (!icon || !icon.classList.contains("news")) return;
    icon.classList.remove("news");
    void postForm("/notifications/read", { target: id });
  }

  // A terminal that has no pane is not a terminal this page can activate, and
  // half applying that leaves the strip with nothing marked and stores the dead
  // id as the project's active one. The URL's ?terminal= is where such an id
  // comes from: the session it names may be gone by the time the page is
  // reloaded. So the pane decides first, and this either activates or does
  // nothing; who is active then stays whatever the fragment settled on.
  async function activateTermPane(id, { focus = true } = {}) {
    const pane = termPaneFor(id);
    if (!pane) return;
    termActiveId = id;
    store.set(termActiveKey, id || "");
    paintTermTabs();
    if (!pane.querySelector("terminal-attach")) await mountTermIsland(pane);
    if (focus) document.dispatchEvent(new CustomEvent("dc:activate-pane", { detail: { id } }));
    markTermRead(id);
  }

  function syncTermKeyed(container, fresh, attr) {
    const wanted = new Map();
    for (const el of fresh.querySelectorAll(`[${attr}]`)) {
      wanted.set(el.getAttribute(attr), el);
    }
    for (const el of [...container.querySelectorAll(`[${attr}]`)]) {
      const id = el.getAttribute(attr);
      if (wanted.has(id)) wanted.delete(id);
      else el.remove();
    }
    for (const el of wanted.values()) container.appendChild(el);
  }

  function reconcileTerminals(doc, { focus = false } = {}) {
    const freshTabs = doc.querySelector("[data-editor-term-tabs]");
    const freshPanes = doc.querySelector("[data-editor-term-panes]");
    if (!freshTabs || !freshPanes) return;
    let panes = termPanesEl();
    if (!panes) {
      panes = document.createElement("div");
      panes.className = "editor-term-panes";
      panes.setAttribute("data-editor-term-panes", "");
      termBodyEl.appendChild(panes);
    }
    syncTermKeyed(panes, freshPanes, "data-term-pane");
    const freshFoots = doc.querySelector("[data-editor-term-foots]");
    if (freshFoots) {
      syncTermKeyed(termFootsHostEl, freshFoots, "data-term-foot");
      void window.app?.loadElements?.(termFootsHostEl);
    }
    const freshModals = doc.querySelector("[data-editor-term-modals]");
    if (freshModals) syncTermKeyed(termModalsHostEl, freshModals, "data-term-modal");
    const freshResume = doc.querySelector("[data-editor-term-resume]");
    if (freshResume && termResumeHostEl) {
      termResumeHostEl.replaceChildren(...freshResume.childNodes);
      foldTermResume();
    }
    termTabsHostEl.replaceChildren(freshTabs);
    const ids = termIds();
    if (!termActiveId || !ids.includes(termActiveId)) termActiveId = ids[0] || null;
    if (termOpen && termActiveId) void activateTermPane(termActiveId, { focus });
    else paintTermTabs();
  }

  async function loadTerminals({ focus = false } = {}) {
    if (termDragging) {
      termRefreshHeld = true;
      return;
    }
    const seq = ++termSeq;
    let text;
    try {
      text = await getText(`${base}/terminals`, { signal });
    } catch (err) {
      void err;
      if (seq === termSeq) status("Terminals could not be loaded.", "error");
      return;
    }
    if (seq !== termSeq) return;
    reconcileTerminals(new DOMParser().parseFromString(text, "text/html"), { focus });
  }

  function paintTermPanel() {
    const applies = termApplies();
    const shown = termOpen && applies;
    termItem.hidden = !applies;
    termItem.setAttribute("aria-pressed", shown ? "true" : "false");
    termStatusBtn.hidden = !applies;
    termStatusBtn.setAttribute("aria-pressed", shown ? "true" : "false");
    termPanelEl.hidden = !shown;
    editor.measure();
  }

  async function openTermPanel({ focus = true } = {}) {
    if (!termApplies()) return;
    termOpen = true;
    store.set(termOpenKey, "1");
    paintTermPanel();
    if (!termLoaded) {
      termLoaded = true;
      await loadTerminals({ focus });
      return;
    }
    if (termActiveId) await activateTermPane(termActiveId, { focus });
  }

  function closeTermPanel() {
    if (!termOpen) return;
    termOpen = false;
    store.set(termOpenKey, "");
    paintTermPanel();
    editor.focus();
  }

  function toggleTermPanel() {
    if (termOpen) closeTermPanel();
    else void openTermPanel();
  }

  function applyTermHeight(px) {
    if (px > 0) termPanelEl.style.setProperty("--editor-term-height", `${px}px`);
    else termPanelEl.style.removeProperty("--editor-term-height");
  }

  function wireTermSplitter() {
    applyTermHeight(parseInt(store.get(TERM_HEIGHT_KEY, "0"), 10) || 0);
    let dragging = false;
    termSplitterEl.addEventListener("pointerdown", (e) => {
      dragging = true;
      termSplitterEl.classList.add("active");
      termSplitterEl.setPointerCapture(e.pointerId);
    }, { signal });
    termSplitterEl.addEventListener("pointermove", (e) => {
      if (!dragging) return;
      const rect = termPanelEl.getBoundingClientRect();
      const colRect = paneColEl.getBoundingClientRect();
      const px = Math.round(Math.min(Math.max(rect.bottom - e.clientY, 96), colRect.height * 0.75));
      applyTermHeight(px);
    }, { signal });
    termSplitterEl.addEventListener("pointerup", (e) => {
      dragging = false;
      termSplitterEl.classList.remove("active");
      termSplitterEl.releasePointerCapture(e.pointerId);
      store.set(TERM_HEIGHT_KEY, String(Math.round(termPanelEl.getBoundingClientRect().height)));
      editor.measure();
    }, { signal });
  }

  async function createTermShell() {
    const res = await postForm("/shells/new", { project: root.dataset.editorProjectPath || "" });
    const landed = new URL(res.url, window.location.origin).pathname;
    const match = landed.match(/^\/shells\/(?!new$)([^/]+)$/);
    if (!res.ok || !match) {
      notifyError("The shell could not be created.");
      return;
    }
    termActiveId = match[1];
    if (!termOpen) await openTermPanel();
    else await loadTerminals({ focus: true });
  }

  async function closeTermSession(id, kind, sessionName, purge = false) {
    const coder = kind === "coder";
    const drop = purge && coder;
    const ok = await confirmDialog({
      title: drop ? `Delete coder "${sessionName}"?`
        : coder ? `Stop coder "${sessionName}"?` : `Delete shell "${sessionName}"?`,
      text: drop ? "It is stopped first, its conversation cannot be resumed afterwards." : undefined,
      confirmText: coder && !drop ? "Stop" : "Delete",
    });
    if (!ok) return;
    const wasActive = id === termActiveId;
    if (wasActive) {
      const ids = termTabIds();
      const i = ids.indexOf(id);
      termActiveId = ids[i + 1] || ids[i - 1] || null;
    }
    window.dispatchEvent(new CustomEvent("dc:terminal-closing", { detail: { id } }));
    const action = drop ? `/coders/${id}/delete` : coder ? `/coders/${id}/stop` : `/shells/${id}/delete`;
    const res = await postForm(action, {});
    if (!res.ok) {
      notifyError("Could not close the session.");
      return;
    }
    notifySuccess(drop ? `Coder "${sessionName}" deleted.`
      : coder ? `Coder "${sessionName}" stopped.` : `Shell "${sessionName}" deleted.`);
    await loadTerminals({ focus: wasActive });
    if (wasActive && !termActiveId) editor.focus();
  }

  function termNavigate(url) {
    if (anyDirty() && !window.confirm("Discard unsaved changes?")) return;
    window.app?.navigate?.(url);
  }

  async function renameTermShell(id, current) {
    const newName = await promptText({
      title: `Rename shell "${current}"`,
      value: current,
      confirmText: "Rename",
      validatorMessage: "Please enter a name.",
    });
    if (!newName || newName === current) return;
    const res = await postForm(`/shells/${id}/rename`, { name: newName });
    if (!res.ok) notifyError("The shell could not be renamed.");
  }

  function termMenuItems(tab) {
    const id = tab.getAttribute("data-term-tab");
    const kind = tab.getAttribute("data-term-kind");
    const sessionName = tab.getAttribute("data-term-name") || "";
    const url = tab.getAttribute("data-term-url") || "";
    const coder = kind === "coder";
    const items = [
      { label: "Open terminal page", icon: "ti-external-link", action: () => termNavigate(url) },
    ];
    if (!coder) {
      items.push({ label: "Rename", icon: "ti-pencil", action: () => void renameTermShell(id, sessionName) });
    }
    if (tab.querySelector(".dc-term-icon.news")) {
      items.push({
        label: "Mark read",
        icon: "ti-eye-check",
        action: () => void postForm("/notifications/read", { target: id }),
      });
    }
    if (coder) {
      items.push(tab.hasAttribute("data-term-steered")
        ? {
          label: "Release",
          icon: "ti-steering-wheel-off",
          purple: true,
          action: () => void releaseCoder({ terminal: id, name: sessionName }).then(() => loadTerminals()),
        }
        : {
          label: "Steer",
          icon: "ti-steering-wheel",
          purple: true,
          action: () => void steerCoder({
            terminal: id,
            name: sessionName,
            prefill: tab.getAttribute("data-term-steer-prefill") || "",
          }).then(() => loadTerminals()),
        });
    }
    items.push({ divider: true });
    items.push({ label: "Open project", icon: "ti-folder", action: () => termNavigate("/projects#project-" + name) });
    items.push({ divider: true });
    items.push({
      label: coder ? "Stop" : "Delete",
      icon: coder ? "ti-player-stop" : "ti-trash",
      danger: !coder,
      warn: coder,
      action: () => void closeTermSession(id, kind, sessionName),
    });
    if (coder) {
      items.push({
        label: "Delete",
        icon: "ti-trash",
        danger: true,
        action: () => void closeTermSession(id, kind, sessionName, true),
      });
    }
    return items;
  }

  function stepTermTab(direction) {
    const ids = termTabIds();
    if (ids.length < 2) return;
    const i = ids.indexOf(termActiveId);
    void activateTermPane(ids[(i + direction + ids.length) % ids.length]);
  }

  function openTermNewMenu() {
    if (!termPlusBtn || !window.bootstrap?.Dropdown) return;
    termMenuFromKey = true;
    window.bootstrap.Dropdown.getOrCreateInstance(termPlusBtn).show();
    termPlusBtn.blur();
  }

  function closeTermNewMenu() {
    if (termPlusBtn) window.bootstrap?.Dropdown.getInstance(termPlusBtn)?.hide();
  }

  function termMenuRows() {
    const menu = termPlusBtn?.closest(".dropdown")?.querySelector(".editor-term-new-menu");
    if (!menu) return [];
    return Array.from(menu.querySelectorAll(".dropdown-item")).filter((row) => row.offsetParent);
  }

  function paintTermMenuSelection() {
    const menu = termPlusBtn?.closest(".dropdown")?.querySelector(".editor-term-new-menu");
    if (!menu) return;
    const rows = termMenuRows();
    for (const row of menu.querySelectorAll(".dropdown-item.selected")) {
      row.classList.remove("selected");
      row.removeAttribute("aria-current");
    }
    if (!termMenuOpen || termMenuIndex < 0 || !rows.length) return;
    termMenuIndex = Math.min(termMenuIndex, rows.length - 1);
    const selected = rows[termMenuIndex];
    selected.classList.add("selected");
    selected.setAttribute("aria-current", "true");
    const top = selected.offsetTop;
    const bottom = top + selected.offsetHeight;
    if (top < menu.scrollTop) menu.scrollTop = top;
    else if (bottom > menu.scrollTop + menu.clientHeight) menu.scrollTop = bottom - menu.clientHeight;
  }

  function moveTermMenuSelection(delta) {
    const rows = termMenuRows();
    if (!rows.length) return;
    if (termMenuIndex < 0) termMenuIndex = delta > 0 ? 0 : rows.length - 1;
    else termMenuIndex = (termMenuIndex + delta + rows.length) % rows.length;
    paintTermMenuSelection();
  }

  function commitTermMenuSelection() {
    const selected = termMenuRows()[termMenuIndex];
    if (!selected) return;
    selected.click();
    if (termMenuOpen) paintTermMenuSelection();
  }

  function foldTermResume() {
    const group = termResumeHostEl?.querySelector("[data-term-resume-fold]");
    if (!group) return;
    applyFold(group, {
      limit: 3,
      expanded: termResumeExpanded,
      toggleAttr: "data-term-resume-toggle",
      toggleClass: "dropdown-item text-center text-secondary small py-1",
      signal,
      onToggle: (event, next) => {
        event.preventDefault();
        event.stopPropagation();
        termResumeExpanded = next;
        foldTermResume();
      },
    });
  }

  async function resumeTermCoder(action, id) {
    status("Resuming the coder…");
    const res = await postForm(action, {});
    if (!res.ok) {
      notifyError("The coder could not be resumed.");
      return;
    }
    if (id) termActiveId = id;
    await loadTerminals({ focus: true });
  }

  async function persistTermOrder() {
    const ids = termTabIds();
    try {
      await ensureOk(await postJSON("/terminal-tabs/order", { ids }), "Could not save the tab order.");
    } catch (err) {
      notifyError(err.message);
      void loadTerminals();
    }
  }

  function wireTermTabDrag() {
    let drag = null;
    const contentX = (strip, clientX) => clientX - strip.getBoundingClientRect().left + strip.scrollLeft;
    const updateDrag = () => {
      if (!drag || !drag.active) return;
      const dx = contentX(drag.strip, drag.lastClientX) - drag.startContentX;
      const draggedCenter = drag.centers[drag.fromIndex] + dx;
      let toIndex = 0;
      for (let i = 0; i < drag.centers.length; i += 1) {
        if (i !== drag.fromIndex && drag.centers[i] < draggedCenter) toIndex += 1;
      }
      drag.toIndex = toIndex;
      drag.tab.style.transform = `translateX(${dx}px)`;
      drag.tabs.forEach((tab, i) => {
        if (tab === drag.tab) return;
        let shift = 0;
        if (i > drag.fromIndex && i <= drag.toIndex) shift = -drag.width;
        else if (i < drag.fromIndex && i >= drag.toIndex) shift = drag.width;
        tab.style.transform = shift ? `translateX(${shift}px)` : "";
      });
    };
    const tickEdgeScroll = () => {
      if (!drag || !drag.active) return;
      const rect = drag.strip.getBoundingClientRect();
      let delta = 0;
      if (drag.lastClientX < rect.left + 32) delta = -12;
      else if (drag.lastClientX > rect.right - 32) delta = 12;
      if (delta) {
        const max = drag.strip.scrollWidth - drag.strip.clientWidth;
        const next = Math.max(0, Math.min(drag.strip.scrollLeft + delta, max));
        if (next !== drag.strip.scrollLeft) {
          drag.strip.scrollLeft = next;
          updateDrag();
        }
      }
      drag.raf = window.requestAnimationFrame(tickEdgeScroll);
    };
    const flushHeld = () => {
      if (!termRefreshHeld) return;
      termRefreshHeld = false;
      void loadTerminals();
    };
    const clearDrag = () => {
      if (!drag) return;
      if (drag.active) {
        window.cancelAnimationFrame(drag.raf);
        drag.strip.classList.remove("editor-term-tabs-dragging");
        drag.tab.classList.remove("editor-term-tab-dragging");
        for (const tab of drag.tabs) tab.style.transform = "";
        termDragging = false;
      }
      drag = null;
    };
    termTabsHostEl.addEventListener("pointerdown", (e) => {
      if (e.button !== 0 || !pointerMedia.matches || drag) return;
      const tab = e.target.closest("[data-term-tab]");
      if (!tab || e.target.closest("[data-term-close]")) return;
      const strip = tab.closest("[data-editor-term-tabs]");
      if (!strip) return;
      drag = {
        tab,
        strip,
        pointerId: e.pointerId,
        startClientX: e.clientX,
        startClientY: e.clientY,
        lastClientX: e.clientX,
        active: false,
        raf: 0,
      };
      try {
        tab.setPointerCapture(e.pointerId);
      } catch (err) {
        void err;
      }
    }, { signal });
    termTabsHostEl.addEventListener("pointermove", (e) => {
      if (!drag || e.pointerId !== drag.pointerId) return;
      if (!drag.active) {
        if (!(e.buttons & 1)) {
          drag = null;
          return;
        }
        if (Math.hypot(e.clientX - drag.startClientX, e.clientY - drag.startClientY) < 6) return;
        drag.active = true;
        termDragging = true;
        drag.tabs = [...drag.strip.querySelectorAll("[data-term-tab]")];
        drag.fromIndex = drag.tabs.indexOf(drag.tab);
        drag.toIndex = drag.fromIndex;
        drag.width = drag.tab.getBoundingClientRect().width;
        const left = drag.strip.getBoundingClientRect().left;
        drag.centers = drag.tabs.map((el) => {
          const rect = el.getBoundingClientRect();
          return rect.left + rect.width / 2 - left + drag.strip.scrollLeft;
        });
        drag.startContentX = contentX(drag.strip, e.clientX);
        drag.strip.classList.add("editor-term-tabs-dragging");
        drag.tab.classList.add("editor-term-tab-dragging");
        drag.raf = window.requestAnimationFrame(tickEdgeScroll);
      }
      e.preventDefault();
      drag.lastClientX = e.clientX;
      updateDrag();
    }, { signal });
    termTabsHostEl.addEventListener("pointerup", (e) => {
      if (!drag || e.pointerId !== drag.pointerId) return;
      const done = drag;
      clearDrag();
      if (!done.active) return;
      termSuppressClick = true;
      if (done.toIndex === done.fromIndex) {
        flushHeld();
        return;
      }
      const others = done.tabs.filter((tab) => tab !== done.tab);
      done.strip.insertBefore(done.tab, others[done.toIndex] || null);
      void persistTermOrder().finally(flushHeld);
    }, { signal });
    termTabsHostEl.addEventListener("pointercancel", () => {
      clearDrag();
      flushHeld();
    }, { signal });
  }

  termTabsHostEl.addEventListener("click", (e) => {
    if (termSuppressClick) {
      termSuppressClick = false;
      return;
    }
    const tab = e.target.closest("[data-term-tab]");
    if (!tab) return;
    if (e.target.closest("[data-term-close]")) {
      void closeTermSession(tab.getAttribute("data-term-tab"), tab.getAttribute("data-term-kind"), tab.getAttribute("data-term-name") || "");
      return;
    }
    if (menuJustClosed()) return;
    void activateTermPane(tab.getAttribute("data-term-tab"));
  }, { signal });
  termTabsHostEl.addEventListener("wheel", (e) => {
    const strip = e.target.closest("[data-editor-term-tabs]");
    if (strip && !e.deltaX && e.deltaY) {
      strip.scrollLeft += e.deltaY;
      e.preventDefault();
    }
  }, { passive: false, signal });
  wireRowMenus(termTabsHostEl, "[data-term-tab]", (row, x, y) => {
    openMenu({ x, y, items: termMenuItems(row), signal });
    return true;
  }, { signal });
  termItem.addEventListener("click", toggleTermPanel, { signal });
  termHideBtn.addEventListener("click", closeTermPanel, { signal });
  for (const btn of termNewBtns) btn?.addEventListener("click", () => void createTermShell(), { signal });
  termResumeHostEl?.addEventListener("submit", (e) => {
    const form = e.target.closest("form[data-term-resume]");
    if (!form) return;
    e.preventDefault();
    e.stopPropagation();
    const id = form.querySelector("[data-resume-id]")?.getAttribute("data-resume-id") || "";
    void resumeTermCoder(form.getAttribute("action") || "", id);
  }, { signal });
  const termPlusDrop = termPlusBtn?.closest(".dropdown");
  termPlusDrop?.addEventListener("shown.bs.dropdown", () => {
    termMenuOpen = true;
    termMenuIndex = termMenuFromKey ? 0 : -1;
    termMenuFromKey = false;
    paintTermMenuSelection();
  }, { signal });
  termPlusDrop?.addEventListener("hidden.bs.dropdown", () => {
    termMenuOpen = false;
    paintTermMenuSelection();
  }, { signal });
  onServerEvent("terminals", (event) => {
    if (!termLoaded) return;
    const project = event.detail && event.detail.project;
    if (project && project !== name) return;
    void loadTerminals();
  }, { signal });
  onServerEvent("docker", () => {
    void loadDocker();
  }, { signal });
  document.addEventListener("pointerdown", (e) => {
    if (e.target instanceof Element) termFocusOwner = !!e.target.closest("[data-editor-term-panel]");
  }, { capture: true, signal });
  document.addEventListener("focusin", (e) => {
    if (e.target instanceof Element) termFocusOwner = !!e.target.closest("[data-editor-term-panel]");
  }, { signal });
  document.addEventListener("keydown", (e) => {
    if ((e.ctrlKey || e.metaKey) && !e.shiftKey && !e.altKey && !e.repeat && e.key.toLowerCase() === "j") {
      if (!termApplies()) return;
      e.preventDefault();
      e.stopPropagation();
      toggleTermPanel();
      return;
    }
    if ((e.ctrlKey || e.metaKey) && e.shiftKey && !e.altKey && !e.repeat && e.key.toLowerCase() === "d") {
      if (dockerItem.hidden) return;
      e.preventDefault();
      e.stopPropagation();
      if (sheetKind === "docker") closeSheet();
      else openDockerSheet();
      return;
    }
    if (!termOpen) return;
    if (termMenuOpen) {
      const menuActions = {
        ArrowDown: () => moveTermMenuSelection(1),
        ArrowUp: () => moveTermMenuSelection(-1),
        Enter: () => commitTermMenuSelection(),
        Escape: () => closeTermNewMenu(),
      };
      const menuAction = menuActions[e.key];
      if (menuAction) {
        e.preventDefault();
        e.stopPropagation();
        menuAction();
        return;
      }
    }
    const fromPanel = e.target instanceof Element && !!e.target.closest("[data-editor-term-panel]");
    if (!fromPanel && !termFocusOwner) return;
    if (e.key === "Tab" && e.ctrlKey && !e.altKey && !e.metaKey) {
      e.preventDefault();
      e.stopPropagation();
      stepTermTab(e.shiftKey ? -1 : 1);
      return;
    }
    if ((e.ctrlKey || e.metaKey) && e.shiftKey && !e.altKey && !e.repeat && e.key === "Enter") {
      e.preventDefault();
      e.stopPropagation();
      setFullscreen(!fullscreenOn);
      return;
    }
    if ((e.ctrlKey || e.metaKey) && e.shiftKey && !e.altKey && !e.repeat && e.key.toLowerCase() === "x") {
      const tab = termTabFor(termActiveId);
      if (!tab) return;
      e.preventDefault();
      e.stopPropagation();
      void closeTermSession(termActiveId, tab.getAttribute("data-term-kind"), tab.getAttribute("data-term-name") || "");
      return;
    }
    if ((e.key === "t" || e.key === "T") && e.metaKey && !e.ctrlKey && !e.altKey) {
      e.preventDefault();
      e.stopPropagation();
      openTermNewMenu();
    }
  }, { capture: true, signal });
  mobileMedia.addEventListener("change", paintTermPanel, { signal });
  wireTermSplitter();
  wireTermTabDrag();
  paintTermPanel();
  void loadDocker();
  void pullLSPIndex();
  void loadComments();
  document.body.appendChild(termModalsHostEl);
  document.body.appendChild(commentModalHostEl);
  const pageTerminal = root.dataset.editorTerminal || "";
  if (pageTerminal && termApplies()) {
    termActiveId = pageTerminal;
    void openTermPanel({ focus: true });
  } else if (termOpen) {
    void openTermPanel({ focus: false });
  }

  // A double tap on bare Shift opens the quick open palette like Ctrl+O. Same
  // state machine as the terminal switcher's double Ctrl/Meta (@dc/doubletap):
  // two quick clean taps, keydown then keyup with no chord, holding a press is
  // not a tap, the gesture fires on the second tap's keyup, any other key
  // resets.
  const shiftTap = new DoubleTap();
  // A Shift+click is not a bare Shift tap.
  document.addEventListener("pointerdown", () => shiftTap.reset(), { capture: true, signal });
  document.addEventListener("keydown", (e) => {
    if (e.target instanceof Element && e.target.closest("[data-editor-term-panel]")) return;
    if (e.key === "Shift" && !e.repeat && !e.ctrlKey && !e.altKey && !e.metaKey) {
      shiftTap.keydown(e.key);
      return;
    }
    shiftTap.reset();
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
    } else if ((e.metaKey || e.ctrlKey) && !e.shiftKey && !e.altKey && !e.repeat
      && e.key.toLowerCase() === "k" && quickOpenEl.hidden) {
      if (gitRepo) {
        e.preventDefault();
        toggleCommit();
      }
    } else if ((e.metaKey || e.ctrlKey) && e.shiftKey && !e.altKey && !e.repeat
      && e.key.toLowerCase() === "p" && quickOpenEl.hidden) {
      e.preventDefault();
      if (projectMenuOpen) closeProjectMenu();
      else openProjectMenu();
    } else if ((e.metaKey || e.ctrlKey) && e.shiftKey && !e.altKey && !e.repeat
      && e.key.toLowerCase() === "g" && quickOpenEl.hidden) {
      if (gitSurface()) {
        e.preventDefault();
        if (sheetKind === "git") closeSheet();
        else openGitSheet();
      }
    } else if ((e.metaKey || e.ctrlKey) && e.shiftKey && !e.altKey && !e.repeat
      && e.key.toLowerCase() === "c" && quickOpenEl.hidden) {
      e.preventDefault();
      if (sheetKind === "comments") closeSheet();
      else openCommentsSheet();
    } else if ((e.metaKey || e.ctrlKey) && !e.shiftKey && e.altKey && !e.repeat
      && e.code === "KeyD" && quickOpenEl.hidden) {
      const tab = activeTab();
      if (gitRepo && editor.canDiff && tab && !tab.kind && !tab.compare && !tab.external) {
        e.preventDefault();
        void toggleTabDiff(tab);
      }
    } else if ((e.metaKey || e.ctrlKey) && !e.shiftKey && e.altKey && !e.repeat
      && e.code === "KeyB" && quickOpenEl.hidden) {
      const tab = activeTab();
      if (gitRepo && editor.canBlame && tab && !tab.kind && !tab.compare && !tab.external) {
        e.preventDefault();
        toggleTabBlame(tab);
      }
    } else if ((e.metaKey || e.ctrlKey) && !e.shiftKey && e.altKey && !e.repeat
      && e.code === "KeyC" && quickOpenEl.hidden) {
      if (commentableTab(activeTab())) {
        e.preventDefault();
        openLineCommentAt(cursorLine);
      }
    } else if ((e.metaKey || e.ctrlKey) && !e.shiftKey && e.altKey && !e.repeat
      && e.code === "KeyR" && quickOpenEl.hidden) {
      const tab = activeTab();
      if (tab && !tab.compare && !tab.external) {
        e.preventDefault();
        void revealInTree(tab.path);
      }
    } else if ((e.metaKey || e.ctrlKey) && !e.shiftKey && e.altKey && !e.repeat
      && e.code === "KeyP" && quickOpenEl.hidden) {
      const tab = activeTab();
      if (tab && !tab.kind && !tab.compare && !tab.external && hasPreview(tab.name)) {
        e.preventDefault();
        togglePreviewFor(tab);
      }
    } else if (e.key === "F2" && !e.ctrlKey && !e.metaKey && !e.altKey && !e.shiftKey
      && !e.repeat && quickOpenEl.hidden) {
      const tab = activeTab();
      if (tab && !tab.compare && !tab.external) {
        e.preventDefault();
        void renameEntry({ path: tab.path, name: tab.name, isDir: false });
      }
    } else if (sheetKind && (e.key === "ArrowDown" || e.key === "ArrowUp")) {
      sheetArrow(e);
    } else if (e.key === "Escape") {
      if (!quickOpenEl.hidden) closeQuickOpen();
      else if (sheetKind) sheetEscape();
      else if (commitOn && commitEl.contains(document.activeElement)) closeCommit();
      else closeDrawer();
    }
  }, { signal });
  // Bootstrap's dropdown answers ArrowUp, ArrowDown and Escape from inside a
  // .dropdown-menu by looking for the toggle that opened it, and the menus the
  // sheet borrows have none: the lookup throws, and on a select it swallows the
  // arrows before that. Its data api listens on the document in the capture
  // phase, so the one place ahead of it is the window, where those three keys
  // are taken out of the way for the sheet; what the sheet does with them is
  // the same as below.
  window.addEventListener("keydown", (e) => {
    if (!sheetKind || !(e.target instanceof Element)) return;
    if (e.key !== "Escape" && e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
    if (!sheetBodyEl.contains(e.target) || !e.target.closest(".dropdown-menu")) return;
    e.stopPropagation();
    if (e.key === "Escape") sheetEscape();
    else sheetArrow(e);
  }, { capture: true, signal });
  document.addEventListener("keyup", (e) => {
    if (e.target instanceof Element && e.target.closest("[data-editor-term-panel]")) return;
    if (shiftTap.keyup(e.key) && quickOpenEl.hidden) {
      e.preventDefault();
      openQuickOpen("files");
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
  await Promise.all([loadTree(), restoreTabs(), loadGitStatus()]);
  if (tabs.length === 0 && mobileMedia.matches) openDrawer();
  if (pageTerminal && termOpen && termApplies()) void activateTermPane(pageTerminal, { focus: true });

  return () => {
    ac.abort();
    termStripResize.disconnect();
    clearTimeout(statusTimer);
    clearTimeout(previewTimer);
    clearTimeout(searchTimer);
    clearTimeout(gitWatchTimer);
    if (svgPreviewUrl) URL.revokeObjectURL(svgPreviewUrl);
    if (lsp) {
      for (const tab of tabs) {
        if (!tab.kind && !tab.compare) lsp.closeDocument(tab.path);
      }
    }
    document.documentElement.classList.remove("dc-editor-fullscreen");
    termModalsHostEl.remove();
    window.bootstrap?.Modal?.getInstance(commentModalEl)?.dispose();
    commentModalHostEl.remove();
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
  mts: "ti-file-type-ts", cts: "ti-file-type-ts",
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
  mts: ["lang-javascript@6.2.2", "javascript", { typescript: true }],
  cts: ["lang-javascript@6.2.2", "javascript", { typescript: true }],
  tsx: ["lang-javascript@6.2.2", "javascript", { typescript: true, jsx: true }],
  go: ["lang-go@6.0.0", "go", null],
  html: ["lang-html@6.4.9", "html", null],
  htm: ["lang-html@6.4.9", "html", null],
  vue: ["lang-html@6.4.9", "html", null],
  gohtml: ["lang-html@6.4.9", "html", null],
  tmpl: ["lang-html@6.4.9", "html", null],
  gotmpl: ["lang-html@6.4.9", "html", null],
  twig: ["lang-jinja@6.0.1", "jinja", null],
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
  const { ChangeSet, EditorState, Compartment, StateEffect, StateField } = state;
  const { keymap, Decoration, showTooltip } = view;
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
  // Whether the compartment holds an inline diff right now. It is written by
  // the two functions that put one up and take one down and by nothing else,
  // so it says what the open state carries, which is what setOriginal reads to
  // decide between the package's own update and a fresh build.
  let unifiedOn = false;

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

  // ---- code navigation --------------------------------------------------------

  // The word under the mouse while Ctrl or Cmd is held.
  const setLSPHint = StateEffect.define();
  const lspHintField = StateField.define({
    create: () => null,
    update(value, tr) {
      for (const e of tr.effects) {
        if (e.is(setLSPHint)) value = e.value;
      }
      if (tr.docChanged) value = null;
      return value;
    },
    provide: (f) => EditorView.decorations.from(f, (r) => (
      r ? Decoration.set([Decoration.mark({ class: "cm-dc-lsp-target" }).range(r.from, r.to)]) : Decoration.none
    )),
  });
  const lspTheme = EditorView.theme({
    ".cm-dc-lsp-target": { textDecoration: "underline", cursor: "pointer" },
    ".cm-tooltip.dc-lsp-pill": { padding: "2px", borderRadius: "8px", display: "flex", gap: "2px" },
  });

  function lspHintRange(v, r) {
    const cur = v.state.field(lspHintField, false);
    if (cur === undefined) return;
    if (!r && !cur) return;
    if (r && cur && cur.from === r.from && cur.to === r.to) return;
    v.dispatch({ effects: setLSPHint.of(r ? { from: r.from, to: r.to } : null) });
  }

  // Off a word the modifier click stays CodeMirror's own add-cursor gesture.
  function lspWordAt(v, e) {
    const pos = v.posAtCoords({ x: e.clientX, y: e.clientY });
    if (pos == null) return null;
    const word = v.state.wordAt(pos);
    return word ? { from: word.from, to: word.to, pos } : null;
  }

  const lspMouse = EditorView.domEventHandlers({
    mousedown(e, v) {
      if (e.button !== 0 || !(e.ctrlKey || e.metaKey) || e.shiftKey || e.altKey) return false;
      if (!hooks.lspUsable?.()) return false;
      const target = lspWordAt(v, e);
      if (!target) return false;
      e.preventDefault();
      lspHintRange(v, null);
      const line = v.state.doc.lineAt(target.pos);
      hooks.onLSPClick?.({
        line: line.number - 1,
        character: target.pos - line.from,
        word: v.state.doc.sliceString(target.from, target.to),
      });
      return true;
    },
    mousemove(e, v) {
      if (!(e.ctrlKey || e.metaKey) || !hooks.lspUsable?.()) {
        lspHintRange(v, null);
        return false;
      }
      lspHintRange(v, lspWordAt(v, e));
      return false;
    },
  });
  // The cursor pill, the touch way to the lookups: static, no server
  // request rides on a cursor move; empty selection only, so it never
  // sits on selection handles.
  const setLSPPill = StateEffect.define();
  const lspPillField = StateField.define({
    create: () => null,
    update(value, tr) {
      let set = false;
      for (const e of tr.effects) {
        if (e.is(setLSPPill)) {
          value = e.value;
          set = true;
        }
      }
      if (!set && (tr.docChanged || tr.selection)) value = null;
      return value;
    },
    provide: (f) => showTooltip.from(f),
  });
  // Touch only: the window opens with a touch and closes with an edit.
  let lastSurfaceTouch = 0;
  const lspPillTouch = EditorView.domEventHandlers({
    touchstart() {
      lastSurfaceTouch = Date.now();
      return false;
    },
    // A re-tap on the same spot fires no update, so the lift looks itself.
    touchend(_, v) {
      setTimeout(() => syncLSPPill(v), 250);
      return false;
    },
  });
  const pillButton = (label, act) => {
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "btn btn-sm";
    btn.textContent = label;
    btn.addEventListener("click", (e) => {
      e.preventDefault();
      act();
    });
    return btn;
  };
  // The server decides definition or usages on invocation, so the label
  // must not pretend to know the case.
  const lspPillTooltip = (pos) => ({
    pos,
    above: true,
    create: (v) => {
      const dom = document.createElement("div");
      dom.className = "dc-lsp-pill";
      dom.setAttribute("data-editor-lsp-pill", "");
      const offset = { x: 0, y: 6 };
      const act = pillButton("Look up", () => {
        lastSurfaceTouch = 0;
        v.dispatch({ effects: setLSPPill.of(null) });
        void hooks.onGoToDefinition?.();
      });
      act.setAttribute("data-pill-action", "");
      dom.append(act);
      return { dom, offset };
    },
  });
  // A dispatch cannot run inside an update.
  let pillScheduled = false;
  function syncLSPPill(v) {
    if (pillScheduled) return;
    pillScheduled = true;
    requestAnimationFrame(() => {
      pillScheduled = false;
      const field = v.state.field(lspPillField, false);
      if (field === undefined) return;
      const sel = v.state.selection.main;
      const word = sel.empty ? v.state.wordAt(sel.head) : null;
      const want = !!word && Date.now() - lastSurfaceTouch < 800 && !!hooks.lspUsable?.();
      if (want && (!field || field.pos !== sel.head)) {
        v.dispatch({ effects: setLSPPill.of(lspPillTooltip(sel.head)) });
      } else if (!want && field) {
        v.dispatch({ effects: setLSPPill.of(null) });
      }
    });
  }
  const lspExtension = [lspHintField, lspTheme, lspMouse, lspPillField, lspPillTouch];

  // The swipe zone asks whether the text has the focus, so every view says when
  // that changes. It rides in the shared extensions because a side by side view
  // has two of them and either one can hold the focus.
  const focusReporter = EditorView.updateListener.of((u) => {
    if (u.focusChanged) hooks.onFocusChange?.();
  });

  // Everything both sides share. The compartments live in both, so a font or
  // theme change reaches the read only side of a diff as well.
  const sharedExtensions = (langExt) => [
    basicSetup,
    focusReporter,
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

  const commentsConf = new Compartment();
  let commentData = null;

  class CommentLineMarker extends view.GutterMarker {
    constructor(outdated) {
      super();
      this.outdated = outdated;
      this.elementClass = outdated ? "cm-comment-line cm-comment-line-outdated" : "cm-comment-line";
    }

    eq(other) {
      return other.outdated === this.outdated;
    }
  }

  const commentLineMarker = new CommentLineMarker(false);
  const commentLineOutdatedMarker = new CommentLineMarker(true);

  const commentsTheme = EditorView.theme({
    ".cm-gutters": { cursor: "pointer" },
    ".cm-gutters .cm-comment-line": {
      color: "var(--tblr-yellow, #f59f00)",
      fontWeight: "700",
      backgroundColor: "rgba(245, 159, 0, 0.18)",
    },
    ".cm-gutters .cm-comment-line.cm-comment-line-outdated": {
      color: "var(--tblr-orange, #f76707)",
      backgroundColor: "rgba(247, 103, 7, 0.18)",
      textDecoration: "line-through",
    },
  });

  function commentsExtension(data) {
    if (!data) return [];
    if (data.length === 0) return [commentsTheme];
    const build = (doc) => {
      const byLine = new Map();
      for (const c of data) {
        if (c.line < 1 || c.line > doc.lines) continue;
        byLine.set(c.line, byLine.get(c.line) || !!c.outdated);
      }
      const ranges = [];
      for (const [line, outdated] of byLine) {
        ranges.push((outdated ? commentLineOutdatedMarker : commentLineMarker).range(doc.line(line).from));
      }
      return state.RangeSet.of(ranges, true);
    };
    const field = state.StateField.define({
      create: (st) => build(st.doc),
      update: (set, tr) => (tr.docChanged ? set.map(tr.changes) : set),
      provide: (f) => view.gutterLineClass.from(f),
    });
    return [commentsTheme, field];
  }

  // ---- change bars -----------------------------------------------------------

  // The gutter bars mark what the buffer holds against HEAD: a blue bar on
  // changed lines, a green one on new lines, a grey tick where lines were
  // deleted. They are not a mode like the diff, but a fact about the file,
  // always visible, so there is no switch anywhere. The chunks live in a
  // state field and follow every keystroke incrementally; only the plain
  // editor carries them, a comparison already shows its changes.
  const changesConf = new Compartment();

  const CHANGE_COLORS = {
    mod: "var(--tblr-azure, #4299e1)",
    add: "var(--tblr-green, #2fb344)",
    del: "var(--tblr-secondary, #6b7280)",
  };

  class ChangeMarker extends view.GutterMarker {
    constructor(key) {
      super();
      this.key = key;
    }

    eq(other) {
      return other.key === this.key;
    }

    toDOM() {
      const [kind, first, last, cap] = this.key.split("|");
      const el = document.createElement("div");
      if (kind === "spacer") {
        el.style.cssText = "width:3px;height:100%;margin:0 2px";
        return el;
      }
      if (kind === "delTop" || kind === "delBottom" || kind === "delBoth") {
        el.style.cssText = "position:relative;width:3px;height:100%;margin:0 2px";
        if (kind !== "delBottom") el.appendChild(delTick("top:-3px"));
        if (kind !== "delTop") el.appendChild(delTick("bottom:-3px"));
        return el;
      }
      const r = (on) => (on === "1" ? "2px" : "0");
      el.style.cssText = `width:3px;height:100%;margin:0 2px;background:${CHANGE_COLORS[kind]};`
        + `border-radius:${r(first)} ${r(first)} ${r(last)} ${r(last)}`;
      if (cap === "1") {
        el.style.position = "relative";
        el.appendChild(delTick("bottom:-3px"));
      }
      return el;
    }
  }

  function delTick(edge) {
    const tick = document.createElement("div");
    tick.style.cssText = `position:absolute;left:-1px;width:6px;height:5px;border-radius:2px;background:${CHANGE_COLORS.del};${edge}`;
    return tick;
  }

  const changeMarkerCache = new Map();

  function changeMarker(kind, first = false, last = false, cap = false) {
    const key = `${kind}|${first ? "1" : "0"}|${last ? "1" : "0"}|${cap ? "1" : "0"}`;
    let marker = changeMarkerCache.get(key);
    if (!marker) {
      marker = new ChangeMarker(key);
      changeMarkerCache.set(key, marker);
    }
    return marker;
  }

  // changeMarkers turns the chunks into gutter marks. A chunk is a char level
  // answer that rounds to full lines and can therefore carry untouched
  // neighbour lines (a deleted trailing line rides in a chunk with the line
  // above it), so each chunk's lines are compared once more line by line: what
  // matches nothing is a changed or new line, and lines HEAD had that match
  // nothing put a tick on the boundary they vanished from.
  function changeMarkers(chunks, doc, headDoc) {
    const ranges = [];
    const clamp = (pos) => Math.max(0, Math.min(pos, doc.length));
    const headClamp = (pos) => Math.max(0, Math.min(pos, headDoc.length));
    const barRun = (kind, from, to, cap) => {
      for (let n = from; n <= to; n++) {
        ranges.push(changeMarker(kind, n === from, n === to, cap && n === to).range(doc.line(n).from));
      }
    };
    // Deletions above and below the same untouched line merge into one marker,
    // two would stack in the gutter cell and break its height.
    const ticks = new Map();
    const tick = (kind, lineNo) => ticks.set(lineNo, ticks.has(lineNo) && ticks.get(lineNo) !== kind ? "delBoth" : kind);
    for (const chunk of chunks) {
      if (chunk.fromB >= chunk.toB) {
        const line = doc.lineAt(clamp(chunk.fromB));
        tick(clamp(chunk.fromB) === line.from ? "delTop" : "delBottom", line.number);
        continue;
      }
      const fromLine = doc.lineAt(clamp(chunk.fromB)).number;
      const toLine = doc.lineAt(Math.max(clamp(chunk.fromB), clamp(chunk.toB - 1))).number;
      const bLines = [];
      for (let n = fromLine; n <= toLine; n++) bLines.push(doc.line(n).text);
      const aLines = [];
      if (chunk.toA > chunk.fromA) {
        const fromA = headDoc.lineAt(headClamp(chunk.fromA)).number;
        const toA = headDoc.lineAt(Math.max(headClamp(chunk.fromA), headClamp(chunk.toA - 1))).number;
        for (let n = fromA; n <= toA; n++) aLines.push(headDoc.line(n).text);
      }
      emitLineDiff(aLines, bLines, fromLine, barRun, tick);
    }
    for (const [lineNo, kind] of ticks) {
      ranges.push(changeMarker(kind).range(doc.line(lineNo).from));
    }
    return state.RangeSet.of(ranges, true);
  }

  // emitLineDiff aligns a chunk's HEAD lines with its buffer lines (LCS over
  // whole lines) and reads the runs between the matches: both sides present is
  // a modification, buffer only is an addition, HEAD only is a tick on the
  // boundary. A modification that swallowed more HEAD lines than it shows
  // carries the tick under its last line. A chunk too large to align falls
  // back to "all modified", which is what it visually is anyway.
  function emitLineDiff(aLines, bLines, firstLine, barRun, tick) {
    const n = aLines.length;
    const m = bLines.length;
    if (n === 0) {
      barRun("add", firstLine, firstLine + m - 1, false);
      return;
    }
    if (n * m > 20000) {
      barRun("mod", firstLine, firstLine + m - 1, n > m);
      return;
    }
    const w = m + 1;
    const lcs = new Int32Array((n + 1) * w);
    for (let i = n - 1; i >= 0; i--) {
      for (let j = m - 1; j >= 0; j--) {
        lcs[i * w + j] = aLines[i] === bLines[j]
          ? lcs[(i + 1) * w + j + 1] + 1
          : Math.max(lcs[(i + 1) * w + j], lcs[i * w + j + 1]);
      }
    }
    let i = 0;
    let j = 0;
    while (i < n || j < m) {
      if (i < n && j < m && aLines[i] === bLines[j]) {
        i += 1;
        j += 1;
        continue;
      }
      const jStart = j;
      let del = 0;
      let ins = 0;
      while (i < n || j < m) {
        if (i < n && j < m && aLines[i] === bLines[j]) break;
        if (i < n && (j >= m || lcs[(i + 1) * w + j] >= lcs[i * w + j + 1])) {
          i += 1;
          del += 1;
        } else {
          j += 1;
          ins += 1;
        }
      }
      if (ins > 0) barRun(del > 0 ? "mod" : "add", firstLine + jStart, firstLine + j - 1, del > ins);
      else if (j < m) tick("delTop", firstLine + j);
      else tick("delBottom", firstLine + m - 1);
    }
  }

  function changesExtension(text) {
    if (text == null || !mergeMod) return [];
    const headDoc = state.Text.of(text.split("\n"));
    const field = state.StateField.define({
      create: (st) => mergeMod.Chunk.build(headDoc, st.doc),
      update: (chunks, tr) => (tr.docChanged ? mergeMod.Chunk.updateB(chunks, headDoc, tr.newDoc, tr.changes) : chunks),
    });
    let cache = { chunks: null, doc: null, set: state.RangeSet.empty };
    return [field, view.gutter({
      class: "cm-changes",
      markers(v) {
        const chunks = v.state.field(field);
        if (cache.chunks !== chunks || cache.doc !== v.state.doc) {
          cache = { chunks, doc: v.state.doc, set: changeMarkers(chunks, v.state.doc, headDoc) };
        }
        return cache.set;
      },
      initialSpacer: () => changeMarker("spacer"),
    })];
  }

  const goToDefinitionKey = () => {
    if (hooks.lspUsable?.()) void hooks.onGoToDefinition?.();
    return true;
  };

  const editableExtensions = (langExt) => [
    keymap.of([
      { key: "Ctrl-o", run: () => true },
      { key: "Ctrl-f", run: search.openSearchPanel },
      {
        key: "Shift-F12",
        run: () => {
          if (!hooks.lspUsable?.()) return false;
          void hooks.onFindUsages?.();
          return true;
        },
      },
      {
        // Always claimed, or the surface takes it as a formatting command;
        // Mod is Cmd alone on a mac, so Ctrl-b binds beside it.
        key: "Mod-b",
        run: goToDefinitionKey,
      },
      { key: "Ctrl-b", run: goToDefinitionKey },
    ]),
    sharedExtensions(langExt),
    keymap.of([indentWithTab]),
    lspExtension,
    mergeConf.of([]),
    blameConf.of(blameExtension(blameData)),
    commentsConf.of(commentsExtension(commentData)),
    EditorView.updateListener.of((u) => {
      if (u.docChanged) hooks.onDocChanged?.(u);
      if (u.docChanged) hooks.onChange();
      if (u.docChanged || u.selectionSet) reportCursor(u.state);
      if (u.docChanged) lastSurfaceTouch = 0;
      if (u.docChanged || u.selectionSet) syncLSPPill(u.view);
    }),
  ];

  const baseExtensions = (langExt) => [editableExtensions(langExt), changesConf.of([]), fillsTheBox];

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

  // The listeners that tie the two sides together sideways, see
  // syncMergeScroll. One controller per merge view, aborted with it.
  let mergeScroll = null;

  // The two sides of a merge view already share the vertical axis: the outer
  // .cm-mergeView is their scroller and the editors grow to their full height,
  // see fillsTheBox. The horizontal axis is the one they do not share, every
  // editor keeps its own .cm-scroller for it, so with wrapping off the left
  // column stands at one place and the right one at another and the two halves
  // of a line are no longer next to each other. This ties them together on
  // that axis alone and leaves the vertical scroller exactly where the package
  // put it.
  //
  // Writing scrollLeft raises a scroll event of its own, and answering that
  // one would send the value straight back. Comparing the two values instead
  // of guarding is not enough either: a side whose longest line is shorter
  // clamps what it is given, keeps answering with its own end and pulls its
  // neighbour back there, which is a comparison that refuses to scroll past
  // the shorter file's width. So a write that really moved the other side
  // marks it, and that one event is spent instead of answered.
  function syncMergeScroll(view) {
    const sides = [view.a.scrollDOM, view.b.scrollDOM];
    const echo = new Set();
    mergeScroll = new AbortController();
    for (const [index, from] of sides.entries()) {
      const to = sides[index === 0 ? 1 : 0];
      from.addEventListener("scroll", () => {
        if (echo.delete(from)) return;
        const was = to.scrollLeft;
        if (was === from.scrollLeft) return;
        to.scrollLeft = from.scrollLeft;
        if (to.scrollLeft !== was) echo.add(to);
      }, { signal: mergeScroll.signal, passive: true });
    }
  }

  function dropMergeView() {
    if (mergeScroll) {
      mergeScroll.abort();
      mergeScroll = null;
    }
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
    syncMergeScroll(view);
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
      syncMergeScroll(view);
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
    if (merge) buildUnified(merge, spec);
    else dropUnified();
    editorView.requestMeasure();
  }

  // buildUnified puts the inline diff up against spec.original, and it takes
  // whatever inline diff stood there down first. That order is the whole
  // function.
  //
  // unifiedMergeView keeps the revision in a StateField and hands it its
  // starting value through StateField.init, and CodeMirror runs an init only
  // for a field the state does not already carry: reconfiguring a compartment
  // that already holds one of these keeps every field value it had, so the
  // extension is rebuilt around the revision that was already in there and the
  // new one never arrives. That is exactly what switching revisions looked
  // like — the picker closed, the status line named the new revision, and the
  // comparison on screen was still the old one. Emptying the compartment takes
  // the fields out of the state, so the build after it starts them over. Both
  // dispatches run in one task, so nothing is painted in between, and the undo
  // history belongs to the plain editor and is touched by neither.
  function buildUnified(merge, spec) {
    dropUnified();
    editorView.dispatch({
      effects: mergeConf.reconfigure(merge.unifiedMergeView({
        original: spec.original,
        // Nothing in the editor writes a chunk back into the buffer, the
        // revision side is there to be read.
        mergeControls: false,
        gutter: true,
        highlightChanges: true,
        collapseUnchanged: collapseOption(spec),
      })),
    });
    unifiedOn = true;
  }

  // dropUnified empties the compartment whatever is in it. It dispatches
  // unconditionally on purpose: a state restored by a tab switch carries the
  // inline diff it was captured with, so "the flag says there is none" is not
  // the same as "the state holds none", and this is what puts the two back in
  // step.
  function dropUnified() {
    editorView.dispatch({ effects: mergeConf.reconfigure([]) });
    unifiedOn = false;
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
    if (!unifiedOn) {
      buildUnified(merge, spec);
      return;
    }
    // The package's own way to move the revision side: an effect carrying the
    // new revision and the change that leads to it, which is what recomputes
    // the chunks. Reconfiguring instead would keep the old revision, see
    // buildUnified, and this is the path a moved base takes, so it would have
    // meant an open diff that never follows a commit.
    const before = merge.getOriginalDoc(editorView.state);
    const whole = ChangeSet.of({ from: 0, to: before.length, insert: spec.original }, before.length);
    editorView.dispatch({ effects: merge.originalDocChangeEffect(editorView.state, whole) });
  }

  // exitDiff drops any diff view without carrying a document anywhere. The
  // caller is about to show another tab or nothing at all.
  function exitDiff() {
    dropMergeView();
    dropUnified();
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
    canComments: true,
    setComments(data) {
      commentData = data;
      workView().dispatch({ effects: commentsConf.reconfigure(commentsExtension(data)) });
    },
    canChanges: true,
    // setChanges puts the bars for the given HEAD text on the plain editor, or
    // takes them off with null. It always reaches the plain editor, never a
    // merge view: a state restored by a tab switch carries whatever bars it had,
    // and the caller reapplies right after, so dispatching unconditionally is
    // what keeps the two in step. valid is checked after the one await, the
    // same guard every builder here uses.
    async setChanges(text, valid) {
      if (text != null) await loadMerge();
      if (valid && !valid()) return;
      editorView.dispatch({ effects: changesConf.reconfigure(changesExtension(text)) });
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
    // readOnly is the document of a file outside the project. It keeps the
    // whole surface, cursor, search and go to line included, and refuses
    // only the writing, which is what EditorState.readOnly says and
    // EditorView.editable would say too much of: a buffer nobody can focus
    // cannot be read with the keyboard either.
    async createDoc(content, filename, { readOnly = false } = {}) {
      const langExt = await langFor(filename, (content || "").split("\n", 1)[0]);
      const extensions = readOnly ? [baseExtensions(langExt), EditorState.readOnly.of(true)] : baseExtensions(langExt);
      const state = EditorState.create({ doc: content, extensions });
      return { state, saved: state.doc };
    },
    showDoc(tab) {
      dropMergeView();
      editorView.setState(tab.handle.state);
      // The stored state may carry an inline diff of its own: captureDoc keeps
      // the state as it is, undo history and all, and while an inline diff is
      // up that state holds the merge extension around the revision it was
      // built against. Emptying the compartment after the swap, not before it,
      // is what keeps the state and the flag in step, and applyTabDiff builds
      // this tab's own diff again from the revision the tab carries.
      dropUnified();
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
    jumpTo(line, character) {
      const view = workView();
      const doc = view.state.doc;
      const target = doc.line(Math.max(1, Math.min(line, doc.lines)));
      const pos = target.from + Math.max(0, Math.min(character || 0, target.length));
      view.dispatch({
        selection: { anchor: pos },
        effects: EditorView.scrollIntoView(pos, { y: "center" }),
      });
      view.focus();
      // A file that was only just opened has not been laid out yet, and a view
      // without a measured height computes that scroll against a geometry it
      // does not have: the cursor lands on the right line while the viewport
      // stays where it was. Asking once more after the frame is what actually
      // moves it. Harmless when the tab was already open, the view is then
      // already there and the second scroll is a no-op.
      requestAnimationFrame(() => {
        if (!view.dom.isConnected || workView() !== view) return;
        view.dispatch({ effects: EditorView.scrollIntoView(pos, { y: "center" }) });
      });
      return true;
    },
    // The swipe zone asks these two: a gesture must not take a selection's
    // place, nor the cursor's while someone is working in the text. Either side
    // of a comparison counts, whichever the finger last landed in.
    hasSelection() {
      return !workView().state.selection.main.empty;
    },
    hasFocus() {
      return liveViews().some((v) => v.hasFocus);
    },
    lineAtGutter(node, y) {
      const v = workView();
      if (!v.dom.contains(node)) return 0;
      const docY = y - v.documentTop;
      const block = v.lineBlockAtHeight(docY);
      if (!block || docY < block.top - 2 || docY > block.bottom + 2) return 0;
      return v.state.doc.lineAt(block.from).number;
    },
    mapSavedLine(tab, isActive, line, changes) {
      const saved = tab.handle.saved;
      if (line < 1 || line > saved.lines) return { line, removed: true, text: "" };
      const savedLine = saved.line(line);
      let removed = false;
      changes.iterChangedRanges((fromA, toA) => {
        if (toA > fromA && fromA <= savedLine.from && toA >= savedLine.to) removed = true;
      });
      if (removed) return { line, removed: true, text: "" };
      const doc = isActive ? workView().state.doc : tab.handle.state.doc;
      const mapped = doc.lineAt(Math.min(changes.mapPos(savedLine.from, 1), doc.length));
      return { line: mapped.number, removed: false, text: mapped.text };
    },
    lspPosition() {
      const st = workView().state;
      const head = st.selection.main.head;
      const line = st.doc.lineAt(head);
      const word = st.wordAt(head);
      return {
        line: line.number - 1,
        character: head - line.from,
        word: word ? st.doc.sliceString(word.from, word.to) : "",
      };
    },
    clearLSPHint() {
      for (const v of liveViews()) lspHintRange(v, null);
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
  for (const type of ["focus", "blur"]) {
    ta.addEventListener(type, () => hooks.onFocusChange?.());
  }
  return {
    async createDoc(content, filename, { readOnly = false } = {}) {
      return { value: content, saved: content, readOnly };
    },
    showDoc(tab) {
      fileConfig = tab.editorConfig || {};
      ta.value = tab.handle.value;
      ta.readOnly = !!tab.handle.readOnly;
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
    hasFocus() {
      return document.activeElement === ta;
    },
    lineAtGutter() {
      return 0;
    },
    mapSavedLine() {
      return null;
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
    canComments: false,
    setComments() {},
    canChanges: false,
    async setChanges() {},
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
  const def = { tab_size: 4, indent: "tab", line_wrap: false, font_size: 14, diff_view: "auto", diff_collapse: true, search_regex: false };
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
  if (typeof s.search_regex !== "boolean") s.search_regex = def.search_regex;
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
