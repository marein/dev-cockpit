import { indentPref } from "@dc/editor-settings";

export function createTextarea(host, hooks, settings) {
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
    languageOf: () => [],
    adoptDoc() {},
    showDoc(tab) {
      fileConfig = tab.editorConfig || {};
      ta.value = tab.handle.value;
      ta.readOnly = !!tab.handle.readOnly;
      applyTabWidth();
      ta.scrollTop = tab.handle.scrollTop || 0;
      ta.scrollLeft = tab.handle.scrollLeft || 0;
      if (tab.handle.selectionStart != null) {
        ta.selectionStart = tab.handle.selectionStart;
        ta.selectionEnd = tab.handle.selectionEnd ?? tab.handle.selectionStart;
      }
      reportCursor();
    },
    // The fallback keeps plain offsets: it exists for the minutes the CDN is
    // down, and a textarea has no line index to map through anyway.
    captureView(tab, isActive) {
      if (!isActive) return { anchor: 0, head: 0, scrollTop: tab.handle.scrollTop || 0, scrollLeft: tab.handle.scrollLeft || 0 };
      return { anchor: ta.selectionStart, head: ta.selectionEnd, scrollTop: ta.scrollTop, scrollLeft: ta.scrollLeft };
    },
    restoreView(tab, view) {
      const length = (tab.handle.value || "").length;
      const at = (n) => Math.max(0, Math.min(n || 0, length));
      tab.handle.selectionStart = at(view.anchor);
      tab.handle.selectionEnd = at(view.head);
      tab.handle.scrollTop = view.scrollTop;
      tab.handle.scrollLeft = view.scrollLeft || 0;
    },
    captureDoc(tab) {
      tab.handle.value = ta.value;
      tab.handle.scrollTop = ta.scrollTop;
      tab.handle.scrollLeft = ta.scrollLeft;
    },
    scrollOf: () => null,
    expandedOf: () => [],
    valueOf(tab, isActive) {
      return isActive ? ta.value : tab.handle.value;
    },
    snapshot(tab, isActive) {
      return isActive ? ta.value : tab.handle.value;
    },
    isClean(tab, isActive) {
      return (isActive ? ta.value : tab.handle.value) === tab.handle.saved;
    },
    markSaved(tab, written) {
      tab.handle.saved = written;
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
